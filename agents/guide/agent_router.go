package guide

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Agent Router
// =============================================================================
//
// TieredRouter provides tiered routing for agents, enabling:
// - Tier 1: Direct DSL routing (0 tokens, 2 hops)
// - Tier 2: Cached route lookup (0 tokens, 2 hops)
// - Tier 3: Guide classification (fallback)
//
// Agents embed TieredRouter to gain intelligent routing capabilities.

// TieredRouter provides tiered routing for agents
type TieredRouter struct {
	mu sync.RWMutex

	// Identity
	agentID   string
	agentName string

	// Event bus for publishing
	bus EventBus

	// Own channels
	channels *AgentChannels

	// DSL parser for Tier 1 routing
	parser *Parser

	// Local route cache for Tier 2 routing
	cache *RouteCache

	// Known agents (from registry announcements)
	// Key: agent ID, Value: announcement with capabilities
	knownAgents *ShardedMap[string, *AgentAnnouncement]

	// Ready agents (agents that have completed initialization)
	// Only route to agents that are ready
	readyAgents *ShardedMap[string, bool]

	// Pending requests (for tracking our outbound requests)
	pending *ShardedMap[string, *outboundRequest]

	// Subscriptions
	requestSub  Subscription
	responseSub Subscription
	registrySub Subscription
	routeSub    Subscription
	readySub    Subscription

	// Callbacks for handling responses
	responseHandlers *ShardedMap[string, ResponseCallback]

	// Resilience components
	circuits       *CircuitBreakerRegistry
	pendingCleanup *PendingCleanup
	dlq            *DeadLetterQueue
	retryQueue     *RetryQueue

	// Running state
	running bool
}

// outboundRequest tracks a request we sent
type outboundRequest struct {
	CorrelationID string
	TargetAgentID string
	FireAndForget bool
	SentAt        time.Time
	Callback      ResponseCallback
}

// ResponseCallback is called when a response is received
type ResponseCallback func(resp *RouteResponse, err error)

// TieredRouterConfig configures the agent router
type TieredRouterConfig struct {
	AgentID   string
	AgentName string
	Bus       EventBus

	// Optional: custom cache config
	CacheConfig *RouteCacheConfig

	// Optional: custom parser (uses default if nil)
	Parser *Parser

	// Optional: circuit breaker config
	CircuitBreakerConfig *CircuitBreakerConfig

	// Optional: retry policy
	RetryPolicy *RetryPolicy

	// Optional: request timeout
	RequestTimeout time.Duration
}

// NewTieredRouter creates a new agent router
func NewTieredRouter(cfg TieredRouterConfig) (*TieredRouter, error) {
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}
	if cfg.Bus == nil {
		return nil, fmt.Errorf("event bus is required")
	}

	agentName := cfg.AgentName
	if agentName == "" {
		agentName = cfg.AgentID
	}

	// Create parser
	parser := cfg.Parser
	if parser == nil {
		parser = NewParser("@")
	}

	// Create cache
	cacheConfig := DefaultRouteCacheConfig()
	if cfg.CacheConfig != nil {
		cacheConfig = *cfg.CacheConfig
	}

	// Create circuit breaker registry
	cbConfig := DefaultCircuitBreakerConfig()
	if cfg.CircuitBreakerConfig != nil {
		cbConfig = *cfg.CircuitBreakerConfig
	}
	circuits := NewCircuitBreakerRegistry(cbConfig)

	// Create DLQ
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{
		MaxSize: 1000,
	})

	// Create pending cleanup
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	pendingCleanup := NewPendingCleanup(PendingCleanupConfig{
		CheckInterval:  1 * time.Second,
		DefaultTimeout: requestTimeout,
		DLQ:            dlq,
		Circuits:       circuits,
	})

	// Create retry queue
	retryQueue := NewRetryQueue(cfg.Bus, 1*time.Second)

	return &TieredRouter{
		agentID:          cfg.AgentID,
		agentName:        agentName,
		bus:              cfg.Bus,
		channels:         NewAgentChannels(cfg.AgentID),
		parser:           parser,
		cache:            NewRouteCache(cacheConfig),
		knownAgents:      NewAgentMap(DefaultShardCount),
		readyAgents:      NewStringMap[bool](DefaultShardCount),
		pending:          NewPendingMap[*outboundRequest](DefaultShardCount),
		responseHandlers: NewStringMap[ResponseCallback](DefaultShardCount),
		circuits:         circuits,
		pendingCleanup:   pendingCleanup,
		dlq:              dlq,
		retryQueue:       retryQueue,
	}, nil
}

// =============================================================================
// Lifecycle
// =============================================================================

// Start begins listening on the agent's channels
func (r *TieredRouter) Start(requestHandler MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("agent router is already running")
	}

	var err error

	// Subscribe to own request channel
	r.requestSub, err = r.bus.SubscribeAsync(r.channels.Requests, requestHandler)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", r.channels.Requests, err)
	}

	// Subscribe to own response channel
	r.responseSub, err = r.bus.SubscribeAsync(r.channels.Responses, r.handleResponse)
	if err != nil {
		r.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", r.channels.Responses, err)
	}

	// Subscribe to agent registry (for registration/unregistration)
	r.registrySub, err = r.bus.SubscribeAsync(TopicAgentRegistry, r.handleRegistryAnnouncement)
	if err != nil {
		r.requestSub.Unsubscribe()
		r.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", TopicAgentRegistry, err)
	}

	// Subscribe to agent ready announcements
	r.readySub, err = r.bus.SubscribeAsync("agents.ready", r.handleAgentReady)
	if err != nil {
		// Non-fatal - ready tracking is optional
		r.readySub = nil
	}

	// Subscribe to route learned broadcasts
	r.routeSub, err = r.bus.SubscribeAsync("routes.learned", r.handleRouteLearned)
	if err != nil {
		// Non-fatal - route learning is optional
		r.routeSub = nil
	}

	// Start resilience components
	r.pendingCleanup.Start()
	r.retryQueue.Start()

	r.running = true
	return nil
}

// AnnounceReady sends an announcement that this agent is ready to receive requests.
// Call this after all initialization is complete.
func (r *TieredRouter) AnnounceReady(info *AgentRoutingInfo) error {
	msg := NewAgentReadyMessage(generateMessageID(), info)
	return r.bus.Publish("agents.ready", msg)
}

// Stop unsubscribes from all channels and stops resilience components
func (r *TieredRouter) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	var errs []error

	// Stop resilience components
	r.pendingCleanup.Stop()
	r.retryQueue.Stop()

	// Unsubscribe from all topics
	if r.requestSub != nil {
		if err := r.requestSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.responseSub != nil {
		if err := r.responseSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.registrySub != nil {
		if err := r.registrySub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.readySub != nil {
		if err := r.readySub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.routeSub != nil {
		if err := r.routeSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}

	r.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

// =============================================================================
// Tiered Routing
// =============================================================================

// Route routes a request using the tiered model.
// Returns the correlation ID for tracking.
//
// Decision flow:
//  1. DSL detected (@agent:action) → direct route
//  2. Capability match found → direct route
//  3. Cache hit → direct route
//  4. Fall back to Guide for LLM classification
func (r *TieredRouter) Route(ctx context.Context, input string, opts ...RouteOption) (string, error) {
	options := defaultRouteOptions()
	for _, opt := range opts {
		opt(&options)
	}

	correlationID := generateCorrelationID()

	// Tier 1: Try DSL parsing (direct routing)
	if cmd, err := r.parser.Parse(input); err == nil && cmd.TargetAgent != "" {
		return r.routeDirect(ctx, correlationID, cmd.TargetAgent, input, options)
	}

	// Tier 2: Check capability matching against known agents
	if match := r.FindMatchingAgent(input); match != "" {
		return r.routeDirect(ctx, correlationID, match, input, options)
	}

	// Tier 3: Check local cache
	if cached := r.cache.Get(input); cached != nil {
		return r.routeDirect(ctx, correlationID, cached.TargetAgentID, input, options)
	}

	// Tier 4: Fall back to Guide
	return r.routeViaGuide(ctx, correlationID, input, options)
}

// =============================================================================
// Programmatic Actions
// =============================================================================
//
// Use these methods when an agent needs to trigger another agent's behavior
// based on internal state (not user input). Examples:
// - Context window nearing limit → trigger archivalist to preserve state
// - Task completed → trigger notifier
// - Error occurred → trigger logger

// TriggerAction invokes a specific action on a target agent.
// This is for programmatic triggers, not NL routing.
//
// Example:
//
//	router.TriggerAction(ctx, "archivalist", "preserve", contextData, WithFireAndForget())
func (r *TieredRouter) TriggerAction(ctx context.Context, targetAgentID, action string, data any, opts ...RouteOption) (string, error) {
	options := defaultRouteOptions()
	for _, opt := range opts {
		opt(&options)
	}

	correlationID := generateCorrelationID()

	// Build action request
	actionReq := &ActionRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: options.parentCorrelationID,
		SourceAgentID:       r.agentID,
		SourceAgentName:     r.agentName,
		TargetAgentID:       targetAgentID,
		Action:              action,
		Data:                data,
		FireAndForget:       options.fireAndForget,
		Timestamp:           time.Now(),
	}

	// Track pending (unless fire-and-forget with no callback)
	if !options.fireAndForget || options.callback != nil {
		r.trackOutbound(correlationID, targetAgentID, options)
	}

	// Publish to target's request channel
	msg := NewActionMessage(generateMessageID(), actionReq)
	targetTopic := TopicRequests(targetAgentID)

	if err := r.bus.Publish(targetTopic, msg); err != nil {
		r.removeOutbound(correlationID)
		return "", fmt.Errorf("failed to publish action to %s: %w", targetTopic, err)
	}

	return correlationID, nil
}

// TriggerPreserve is a convenience method for triggering context preservation.
// Common use case: agent nearing context limit.
func (r *TieredRouter) TriggerPreserve(ctx context.Context, data *PreserveRequest) (string, error) {
	return r.TriggerAction(ctx, "archivalist", "preserve", data, WithFireAndForget())
}

// =============================================================================
// Capability Matching
// =============================================================================

// capabilityMatch represents a matched agent with scoring info
type capabilityMatch struct {
	AgentID     string
	Priority    int
	Specificity int
	MatchType   string // "pattern" or "keyword"
}

// FindMatchingAgent checks if input matches any known agent's capabilities.
// Returns the agent ID if a match is found, empty string otherwise.
//
// When multiple agents match:
//   - Higher priority wins
//   - If priorities are equal, higher specificity wins
//   - If still tied, pattern matches win over keyword matches
//
// Matching is based on:
//   - Capability patterns (regex)
//   - Capability keywords
func (r *TieredRouter) FindMatchingAgent(input string) string {
	// Normalize input for matching
	normalizedInput := normalizeInput(input)

	// Collect all matches from ready agents only
	var matches []capabilityMatch

	r.knownAgents.Range(func(agentID string, ann *AgentAnnouncement) bool {
		// Only consider ready agents
		if ready, _ := r.readyAgents.Get(agentID); !ready {
			return true // continue
		}

		// Check circuit breaker - skip failing agents
		if !r.circuits.Allow(agentID) {
			return true // continue
		}

		if match := r.findCapabilityMatch(normalizedInput, agentID, ann); match != nil {
			matches = append(matches, *match)
		}
		return true // continue
	})

	if len(matches) == 0 {
		return ""
	}

	// Find best match
	best := matches[0]
	for _, m := range matches[1:] {
		if r.isBetterMatch(m, best) {
			best = m
		}
	}

	return best.AgentID
}

// findCapabilityMatch checks if input matches an agent's capabilities
// Returns match info if found, nil otherwise
func (r *TieredRouter) findCapabilityMatch(input, agentID string, ann *AgentAnnouncement) *capabilityMatch {
	if ann.Capabilities == nil {
		return nil
	}

	priority := ann.Capabilities.Priority
	specificity := ann.Capabilities.Specificity

	// Check patterns (higher priority than keywords)
	for _, pattern := range ann.Capabilities.Patterns {
		if pattern != nil && pattern.MatchString(input) {
			return &capabilityMatch{
				AgentID:     agentID,
				Priority:    priority,
				Specificity: specificity + pattern.Specificity,
				MatchType:   "pattern",
			}
		}
	}

	// Check keywords
	for _, keyword := range ann.Capabilities.Keywords {
		if containsWord(input, keyword) {
			return &capabilityMatch{
				AgentID:     agentID,
				Priority:    priority,
				Specificity: specificity + len(keyword), // Longer keywords = more specific
				MatchType:   "keyword",
			}
		}
	}

	return nil
}

// isBetterMatch returns true if a is a better match than b
func (r *TieredRouter) isBetterMatch(a, b capabilityMatch) bool {
	// Higher priority wins
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}

	// Higher specificity wins
	if a.Specificity != b.Specificity {
		return a.Specificity > b.Specificity
	}

	// Pattern matches win over keyword matches
	if a.MatchType != b.MatchType {
		return a.MatchType == "pattern"
	}

	return false
}

// containsWord checks if input contains a keyword as a word boundary
func containsWord(input, keyword string) bool {
	keyword = normalizeInput(keyword)
	if keyword == "" {
		return false
	}

	// Simple substring match for now
	// Could enhance with word boundary detection
	return len(input) >= len(keyword) &&
		(input == keyword ||
			contains(input, " "+keyword+" ") ||
			hasPrefix(input, keyword+" ") ||
			hasSuffix(input, " "+keyword))
}

// String helpers to avoid importing strings package
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// RouteToAgent routes directly to a known agent (bypasses all classification)
func (r *TieredRouter) RouteToAgent(ctx context.Context, targetAgentID, input string, opts ...RouteOption) (string, error) {
	options := defaultRouteOptions()
	for _, opt := range opts {
		opt(&options)
	}

	correlationID := generateCorrelationID()
	return r.routeDirect(ctx, correlationID, targetAgentID, input, options)
}

// routeDirect publishes directly to target agent's request channel
func (r *TieredRouter) routeDirect(ctx context.Context, correlationID, targetAgentID, input string, opts routeOptions) (string, error) {
	// Check if target is known
	if !r.knownAgents.Has(targetAgentID) {
		// Target not known - fall back to Guide
		return r.routeViaGuide(ctx, correlationID, input, opts)
	}

	// Check if target is ready (registration handshake complete)
	if ready, _ := r.readyAgents.Get(targetAgentID); !ready {
		// Agent registered but not ready - fall back to Guide
		// Guide will queue or retry appropriately
		return r.routeViaGuide(ctx, correlationID, input, opts)
	}

	// Check circuit breaker - don't route to failing agents
	if !r.circuits.Allow(targetAgentID) {
		// Circuit open - add to DLQ and return error
		r.dlq.Add(&DeadLetter{
			Message: &Message{
				CorrelationID: correlationID,
				TargetAgentID: targetAgentID,
				Timestamp:     time.Now(),
			},
			Reason:        DeadLetterReasonCircuitOpen,
			Error:         "circuit breaker open for agent",
			TargetAgentID: targetAgentID,
			SourceAgentID: r.agentID,
		})
		return "", fmt.Errorf("circuit open for agent %s", targetAgentID)
	}

	// Build forwarded request
	fwd := &ForwardedRequest{
		CorrelationID:        correlationID,
		ParentCorrelationID:  opts.parentCorrelationID,
		Input:                input,
		SourceAgentID:        r.agentID,
		SourceAgentName:      r.agentName,
		FireAndForget:        opts.fireAndForget,
		Confidence:           1.0, // Direct routing has max confidence
		ClassificationMethod: "direct",
	}

	// Track pending request (unless fire-and-forget with no callback)
	if !opts.fireAndForget || opts.callback != nil {
		r.trackOutbound(correlationID, targetAgentID, opts)
	}

	// Publish to target's request channel
	msg := NewForwardMessage(generateMessageID(), fwd)
	msg.TargetAgentID = targetAgentID

	targetTopic := TopicRequests(targetAgentID)
	if err := r.bus.Publish(targetTopic, msg); err != nil {
		r.removeOutbound(correlationID)
		r.circuits.RecordFailure(targetAgentID)
		return "", fmt.Errorf("failed to publish to %s: %w", targetTopic, err)
	}

	return correlationID, nil
}

// routeViaGuide sends request to Guide for classification
func (r *TieredRouter) routeViaGuide(ctx context.Context, correlationID, input string, opts routeOptions) (string, error) {
	// Check Guide circuit breaker
	if !r.circuits.Allow("guide") {
		return "", fmt.Errorf("circuit open for guide")
	}

	req := &RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: opts.parentCorrelationID,
		Input:               input,
		SourceAgentID:       r.agentID,
		SourceAgentName:     r.agentName,
		FireAndForget:       opts.fireAndForget,
		Timestamp:           time.Now(),
	}

	// Track pending
	if !opts.fireAndForget || opts.callback != nil {
		r.trackOutbound(correlationID, "guide", opts)
	}

	// Publish to Guide
	msg := NewRequestMessage(generateMessageID(), req)
	if err := r.bus.Publish(TopicGuideRequests, msg); err != nil {
		r.removeOutbound(correlationID)
		r.circuits.RecordFailure("guide")
		return "", fmt.Errorf("failed to publish to guide: %w", err)
	}

	return correlationID, nil
}

// =============================================================================
// Response Handling
// =============================================================================

// handleResponse processes responses to our requests
func (r *TieredRouter) handleResponse(msg *Message) error {
	// Handle ACKs
	if msg.Type == MessageTypeAck {
		return r.handleAck(msg)
	}

	// Handle responses
	if msg.Type != MessageTypeResponse {
		return nil
	}

	resp, ok := msg.GetRouteResponse()
	if !ok {
		return nil
	}

	// Look up pending request
	pending, found := r.pending.Get(resp.CorrelationID)
	if !found {
		return nil // No pending request
	}

	// Remove from pending
	r.removeOutbound(resp.CorrelationID)

	// Record success in circuit breaker
	if pending.TargetAgentID != "" {
		r.circuits.RecordSuccess(pending.TargetAgentID)
	}

	// Call callback if registered
	if pending.Callback != nil {
		go pending.Callback(resp, nil)
	}

	// Also check handler registry
	handler, found := r.responseHandlers.Get(resp.CorrelationID)
	if found {
		r.responseHandlers.Delete(resp.CorrelationID)
		go handler(resp, nil)
	}

	return nil
}

// handleAck processes acknowledgments for fire-and-forget requests
func (r *TieredRouter) handleAck(msg *Message) error {
	ack, ok := msg.GetAckPayload()
	if !ok {
		return nil
	}

	// Look up pending request
	pending, found := r.pending.Get(msg.CorrelationID)
	if !found {
		return nil
	}

	// Record success in circuit breaker (ACK means agent is responding)
	if pending.TargetAgentID != "" {
		if ack.Received {
			r.circuits.RecordSuccess(pending.TargetAgentID)
		} else {
			r.circuits.RecordFailure(pending.TargetAgentID)
		}
	}

	// For fire-and-forget, ACK completes the request
	if pending.FireAndForget {
		r.removeOutbound(msg.CorrelationID)

		if pending.Callback != nil {
			var err error
			if !ack.Received {
				err = fmt.Errorf("request rejected: %s", ack.Message)
			}
			go pending.Callback(nil, err)
		}
	}

	return nil
}

// RegisterResponseHandler registers a one-time callback for a specific correlation ID
func (r *TieredRouter) RegisterResponseHandler(correlationID string, handler ResponseCallback) {
	r.responseHandlers.Set(correlationID, handler)
}

// =============================================================================
// Registry Handling
// =============================================================================

// handleRegistryAnnouncement processes agent registration events
func (r *TieredRouter) handleRegistryAnnouncement(msg *Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case MessageTypeAgentRegistered:
		// Store agent info but mark as NOT ready yet
		// We'll mark ready when we receive the ready announcement
		r.knownAgents.Set(ann.AgentID, ann)
		r.readyAgents.Set(ann.AgentID, false)

	case MessageTypeAgentUnregistered:
		r.knownAgents.Delete(ann.AgentID)
		r.readyAgents.Delete(ann.AgentID)
		r.circuits.Remove(ann.AgentID)
		// Invalidate cache entries for this agent
		r.cache.InvalidateForAgent(ann.AgentID)
		// Timeout all pending requests to this agent
		r.pendingCleanup.TimeoutAllForAgent(ann.AgentID)
	}

	return nil
}

// handleAgentReady processes agent ready announcements
func (r *TieredRouter) handleAgentReady(msg *Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	// Only process if we know about this agent
	if r.knownAgents.Has(ann.AgentID) {
		// Mark agent as ready - now safe to route to
		r.readyAgents.Set(ann.AgentID, true)

		// Update the announcement with ready state
		r.knownAgents.Set(ann.AgentID, ann)
	}

	return nil
}

// handleRouteLearned processes route learned broadcasts from Guide
func (r *TieredRouter) handleRouteLearned(msg *Message) error {
	route, ok := msg.GetLearnedRoute()
	if !ok {
		return nil
	}

	// Add to local cache
	r.cache.SetFromRoute(route.Input, route.TargetAgentID, route.Intent, route.Domain)
	return nil
}

// =============================================================================
// Response Publishing
// =============================================================================

// SendResponse sends a response to a forwarded request.
// Publishes to own response channel (Guide or direct subscriber will pick it up).
func (r *TieredRouter) SendResponse(fwd *ForwardedRequest, data any, err error) error {
	resp := &RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		Data:                data,
		RespondingAgentID:   r.agentID,
		RespondingAgentName: r.agentName,
	}
	if err != nil {
		resp.Error = err.Error()
	}

	msg := NewResponseMessage(generateMessageID(), resp)

	// Publish to own response channel
	return r.bus.Publish(r.channels.Responses, msg)
}

// SendAck sends an acknowledgment for a fire-and-forget request.
// Call this immediately upon receiving the request, before processing.
func (r *TieredRouter) SendAck(fwd *ForwardedRequest) error {
	msg := NewAckMessage(generateMessageID(), fwd.CorrelationID, r.agentID)

	// Publish to source agent's response channel
	sourceTopic := TopicResponses(fwd.SourceAgentID)
	return r.bus.Publish(sourceTopic, msg)
}

// SendAckError sends a negative acknowledgment (request rejected)
func (r *TieredRouter) SendAckError(fwd *ForwardedRequest, reason string) error {
	msg := NewAckErrorMessage(generateMessageID(), fwd.CorrelationID, r.agentID, reason)

	sourceTopic := TopicResponses(fwd.SourceAgentID)
	return r.bus.Publish(sourceTopic, msg)
}

// SendError sends an error to own error channel
func (r *TieredRouter) SendError(correlationID, errorMsg string) error {
	msg := NewErrorMessage(generateMessageID(), correlationID, r.agentID, errorMsg)
	return r.bus.Publish(r.channels.Errors, msg)
}

// =============================================================================
// Helpers
// =============================================================================

func (r *TieredRouter) trackOutbound(correlationID, targetAgentID string, opts routeOptions) {
	req := &outboundRequest{
		CorrelationID: correlationID,
		TargetAgentID: targetAgentID,
		FireAndForget: opts.fireAndForget,
		SentAt:        time.Now(),
		Callback:      opts.callback,
	}

	// Store in sharded map
	r.pending.Set(correlationID, req)

	// Also track in pending cleanup for timeout handling
	r.pendingCleanup.Add(&PendingEntry{
		CorrelationID: correlationID,
		TargetAgentID: targetAgentID,
		SentAt:        req.SentAt,
		Timeout:       opts.timeout,
		FireAndForget: opts.fireAndForget,
		OnTimeout: func(entry *PendingEntry) {
			// Callback with timeout error
			if opts.callback != nil {
				opts.callback(nil, fmt.Errorf("request timed out"))
			}
		},
	})
}

func (r *TieredRouter) removeOutbound(correlationID string) {
	r.pending.Delete(correlationID)
	r.pendingCleanup.Remove(correlationID)
}

// Channels returns the agent's channel configuration
func (r *TieredRouter) Channels() *AgentChannels {
	return r.channels
}

// AgentID returns the agent's ID
func (r *TieredRouter) AgentID() string {
	return r.agentID
}

// Cache returns the local route cache
func (r *TieredRouter) Cache() *RouteCache {
	return r.cache
}

// KnownAgents returns all known agents
func (r *TieredRouter) KnownAgents() map[string]*AgentAnnouncement {
	return r.knownAgents.Snapshot()
}

// ReadyAgents returns IDs of agents that are ready to receive requests
func (r *TieredRouter) ReadyAgents() []string {
	var ready []string
	r.readyAgents.Range(func(agentID string, isReady bool) bool {
		if isReady {
			ready = append(ready, agentID)
		}
		return true
	})
	return ready
}

// IsAgentReady returns true if the agent is known and ready
func (r *TieredRouter) IsAgentReady(agentID string) bool {
	ready, found := r.readyAgents.Get(agentID)
	return found && ready
}

// CircuitBreakers returns the circuit breaker registry
func (r *TieredRouter) CircuitBreakers() *CircuitBreakerRegistry {
	return r.circuits
}

// DeadLetterQueue returns the dead letter queue
func (r *TieredRouter) DeadLetterQueue() *DeadLetterQueue {
	return r.dlq
}

// Stats returns router statistics
func (r *TieredRouter) Stats() TieredRouterStats {
	return TieredRouterStats{
		AgentID:        r.agentID,
		KnownAgents:    r.knownAgents.Len(),
		ReadyAgents:    len(r.ReadyAgents()),
		PendingReqs:    r.pending.Len(),
		CacheStats:     r.cache.Stats(),
		CircuitStats:   r.circuits.Stats(),
		DLQStats:       r.dlq.Stats(),
		PendingCleanup: r.pendingCleanup.Stats(),
	}
}

// TieredRouterStats contains router statistics
type TieredRouterStats struct {
	AgentID        string                       `json:"agent_id"`
	KnownAgents    int                          `json:"known_agents"`
	ReadyAgents    int                          `json:"ready_agents"`
	PendingReqs    int                          `json:"pending_requests"`
	CacheStats     RouteCacheStats              `json:"cache"`
	CircuitStats   map[string]CircuitBreakerStats `json:"circuits"`
	DLQStats       DeadLetterStats              `json:"dlq"`
	PendingCleanup PendingCleanupStats          `json:"pending_cleanup"`
}

// =============================================================================
// Route Options
// =============================================================================

type routeOptions struct {
	fireAndForget       bool
	parentCorrelationID string
	callback            ResponseCallback
	timeout             time.Duration
}

func defaultRouteOptions() routeOptions {
	return routeOptions{
		timeout: 30 * time.Second,
	}
}

// RouteOption configures a route request
type RouteOption func(*routeOptions)

// WithFireAndForget marks the request as fire-and-forget
func WithFireAndForget() RouteOption {
	return func(o *routeOptions) {
		o.fireAndForget = true
	}
}

// WithParentCorrelation sets the parent correlation ID for request chaining
func WithParentCorrelation(parentID string) RouteOption {
	return func(o *routeOptions) {
		o.parentCorrelationID = parentID
	}
}

// WithCallback registers a callback for the response
func WithCallback(cb ResponseCallback) RouteOption {
	return func(o *routeOptions) {
		o.callback = cb
	}
}

// WithTimeout sets the request timeout
func WithTimeout(d time.Duration) RouteOption {
	return func(o *routeOptions) {
		o.timeout = d
	}
}

// =============================================================================
// ID Generation (Lock-Free)
// =============================================================================

var correlationCounter int64

func generateCorrelationID() string {
	id := atomic.AddInt64(&correlationCounter, 1)
	return fmt.Sprintf("corr_%d_%d", time.Now().UnixNano(), id)
}

// Note: generateMessageID is defined in guide.go and shared across the package
