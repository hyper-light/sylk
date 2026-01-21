// Package academic implements the Academic agent for external knowledge research.
// The Academic researches best practices, papers, and external sources, always
// validating recommendations against codebase reality via the Librarian.
package academic

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// Academic is the main agent for researching external knowledge and best practices.
// It uses Claude Opus 4.5 for complex reasoning and synthesis of research findings.
type Academic struct {
	config       Config
	domainFilter *AcademicDomainFilter
	logger       *slog.Logger

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Research state
	mu              sync.RWMutex
	researchCache   map[string]*ResearchResult
	sourceIndex     map[string]*Source
	pendingRequests map[string]*pendingResearch

	// Outcome tracking for maturity-aware recommendations
	outcomeHistory *OutcomeHistory
}

// Config holds configuration for the Academic agent.
type Config struct {
	// Anthropic API configuration
	AnthropicAPIKey string
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Research configuration
	MaxSources          int           // Max sources to consult per query (default: 10)
	CacheExpiry         time.Duration // How long to cache results (default: 30m)
	LibrarianTimeout    time.Duration // Timeout for Librarian consultation (default: 30s)
	RequireLibrarian    bool          // Require Librarian validation (default: true)
	MemoryThreshold     MemoryThreshold
	DefaultConfidence   ConfidenceLevel
	MinApplicability    float64 // Minimum applicability score to include (default: 0.3)
	OutcomeHistoryLimit int     // Max outcomes to track (default: 1000)

	// Logger
	Logger *slog.Logger
}

// pendingResearch tracks an in-flight research request.
type pendingResearch struct {
	query     *ResearchQuery
	createdAt time.Time
	timeout   time.Duration
}

// New creates a new Academic agent.
func New(cfg Config) (*Academic, error) {
	cfg = applyConfigDefaults(cfg)

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create skills registry and loader
	skillsRegistry := skills.NewRegistry()
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = []string{"research_topic", "find_best_practices", "compare_approaches"}
	skillsLoaderCfg.AutoLoadDomains = []string{"research", "knowledge"}
	skillLoader := skills.NewLoader(skillsRegistry, skillsLoaderCfg)

	// Create hook registry
	hookRegistry := skills.NewHookRegistry()

	a := &Academic{
		config:          cfg,
		domainFilter:    NewAcademicDomainFilter(logger),
		logger:          logger,
		skills:          skillsRegistry,
		skillLoader:     skillLoader,
		hooks:           hookRegistry,
		knownAgents:     make(map[string]*guide.AgentAnnouncement),
		researchCache:   make(map[string]*ResearchResult),
		sourceIndex:     make(map[string]*Source),
		pendingRequests: make(map[string]*pendingResearch),
		outcomeHistory:  NewOutcomeHistory(cfg.OutcomeHistoryLimit),
	}

	a.registerCoreSkills()
	a.registerExtendedSkills()

	return a, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.MaxSources == 0 {
		cfg.MaxSources = 10
	}
	if cfg.CacheExpiry == 0 {
		cfg.CacheExpiry = 30 * time.Minute
	}
	if cfg.LibrarianTimeout == 0 {
		cfg.LibrarianTimeout = 30 * time.Second
	}
	if cfg.RequireLibrarian {
		// Default is true, so no change needed
	}
	if cfg.MemoryThreshold.CheckpointThreshold == 0 {
		cfg.MemoryThreshold = DefaultMemoryThreshold()
	}
	if cfg.DefaultConfidence == "" {
		cfg.DefaultConfidence = ConfidenceLevelMedium
	}
	if cfg.MinApplicability == 0 {
		cfg.MinApplicability = 0.3
	}
	if cfg.OutcomeHistoryLimit == 0 {
		cfg.OutcomeHistoryLimit = 1000
	}
	return cfg
}

// Close closes the Academic agent and its resources.
func (a *Academic) Close() error {
	a.Stop()
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The Academic subscribes to its own channels and the registry topic.
func (a *Academic) Start(bus guide.EventBus) error {
	if a.running {
		return fmt.Errorf("academic is already running")
	}

	a.bus = bus
	a.channels = guide.NewAgentChannels("academic")

	// Subscribe to own request channel (academic.requests)
	var err error
	a.requestSub, err = bus.SubscribeAsync(a.channels.Requests, a.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	a.responseSub, err = bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		a.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	a.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, a.handleRegistryAnnouncement)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	a.running = true
	a.logger.Info("academic agent started",
		"request_channel", a.channels.Requests,
		"response_channel", a.channels.Responses,
	)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Academic) Stop() error {
	if !a.running {
		return nil
	}

	errs := a.unsubscribeAll()
	a.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	a.logger.Info("academic agent stopped")
	return nil
}

func (a *Academic) unsubscribeAll() []error {
	var errs []error
	if err := a.unsubscribeSafe(a.requestSub); err != nil {
		errs = append(errs, err)
	}
	a.requestSub = nil
	if err := a.unsubscribeSafe(a.responseSub); err != nil {
		errs = append(errs, err)
	}
	a.responseSub = nil
	if err := a.unsubscribeSafe(a.registrySub); err != nil {
		errs = append(errs, err)
	}
	a.registrySub = nil
	return errs
}

func (a *Academic) unsubscribeSafe(sub guide.Subscription) error {
	if sub == nil {
		return nil
	}
	return sub.Unsubscribe()
}

// IsRunning returns true if the Academic is actively processing bus messages.
func (a *Academic) IsRunning() bool {
	return a.running
}

// Bus returns the event bus used by the Academic.
func (a *Academic) Bus() guide.EventBus {
	return a.bus
}

// Channels returns the Academic's channel configuration.
func (a *Academic) Channels() *guide.AgentChannels {
	return a.channels
}

// handleBusRequest processes incoming forwarded requests from the event bus.
func (a *Academic) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	// Process the request
	ctx := context.Background()
	startTime := time.Now()

	result, err := a.processForwardedRequest(ctx, fwd)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "academic",
		RespondingAgentName: "academic",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			generateMessageID(),
			fwd.CorrelationID,
			"academic",
			err.Error(),
		)
		return a.bus.Publish(a.channels.Errors, errMsg)
	}

	resp.Data = result

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

func generateMessageID() string {
	return fmt.Sprintf("academic_msg_%d", time.Now().UnixNano())
}

// processForwardedRequest handles the actual request processing.
func (a *Academic) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := a.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (a *Academic) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentRecall:
		return a.handleRecall, nil
	case guide.IntentCheck:
		return a.handleCheck, nil
	default:
		return nil, fmt.Errorf("unsupported intent for academic: %s", intent)
	}
}

// handleRecall processes recall (query) requests for research.
func (a *Academic) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	query := &ResearchQuery{
		Query:     fwd.Input,
		SessionID: fwd.SourceAgentID,
	}

	// Map domain if present
	if fwd.Domain != "" {
		query.Domain = mapGuideDomainToResearch(fwd.Domain)
	}

	return a.Research(ctx, query)
}

// handleCheck processes check (verification) requests.
func (a *Academic) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Validate a claim against sources
	query := &ResearchQuery{
		Query:  fwd.Input,
		Intent: IntentCheck,
	}

	result, err := a.Research(ctx, query)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"validated": len(result.Findings) > 0,
		"findings":  result.Findings,
		"sources":   result.SourcesConsulted,
	}, nil
}

func mapGuideDomainToResearch(d guide.Domain) ResearchDomain {
	switch d {
	case guide.DomainPatterns:
		return DomainPatterns
	case guide.DomainDecisions:
		return DomainDecisions
	case guide.DomainLearnings:
		return DomainLearnings
	default:
		return ""
	}
}

// handleBusResponse processes responses to requests we made.
func (a *Academic) handleBusResponse(msg *guide.Message) error {
	// Handle responses from Librarian consultations
	resp, ok := msg.GetRouteResponse()
	if !ok {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if this is a response to a pending research request
	if pending, exists := a.pendingRequests[resp.CorrelationID]; exists {
		a.logger.Debug("received librarian response",
			"correlation_id", resp.CorrelationID,
			"success", resp.Success,
			"query", pending.query.Query,
		)
		delete(a.pendingRequests, resp.CorrelationID)
	}

	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events.
func (a *Academic) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		a.knownAgents[ann.AgentID] = ann
		a.logger.Debug("agent registered", "agent_id", ann.AgentID, "agent_name", ann.AgentName)
	case guide.MessageTypeAgentUnregistered:
		delete(a.knownAgents, ann.AgentID)
		a.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the Academic knows about.
func (a *Academic) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]*guide.AgentAnnouncement, len(a.knownAgents))
	for k, v := range a.knownAgents {
		result[k] = v
	}
	return result
}

// PublishRequest publishes a request to the Guide for routing.
func (a *Academic) PublishRequest(req *guide.RouteRequest) error {
	if !a.running {
		return fmt.Errorf("academic is not running")
	}

	req.SourceAgentID = "academic"
	req.SourceAgentName = "academic"

	msg := guide.NewRequestMessage(generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Core Research Methods
// =============================================================================

// Research performs research on a topic with mandatory Librarian consultation.
func (a *Academic) Research(ctx context.Context, query *ResearchQuery) (*ResearchResult, error) {
	// Check cache first
	cacheKey := a.cacheKey(query)
	if cached := a.getCached(cacheKey); cached != nil {
		a.logger.Debug("cache hit for research query", "query", query.Query)
		return cached, nil
	}

	// Check outcome history for similar queries
	pastOutcomes := a.outcomeHistory.GetSimilar(query.Query, 3)
	if len(pastOutcomes) > 0 {
		a.logger.Debug("found past outcomes for similar query",
			"query", query.Query,
			"past_outcomes", len(pastOutcomes),
		)
	}

	// Consult Librarian to validate against codebase reality
	var librarianContext *LibrarianContext
	if a.config.RequireLibrarian {
		var err error
		librarianContext, err = a.consultLibrarian(ctx, query)
		if err != nil {
			a.logger.Warn("librarian consultation failed, proceeding without codebase context",
				"error", err,
				"query", query.Query,
			)
		}
	}

	// Perform the research
	result, err := a.executeResearch(ctx, query, librarianContext, pastOutcomes)
	if err != nil {
		return nil, fmt.Errorf("research failed: %w", err)
	}

	// Cache the result
	a.setCached(cacheKey, result)

	return result, nil
}

// LibrarianContext contains codebase context from the Librarian.
type LibrarianContext struct {
	CodebaseMaturity      string         `json:"codebase_maturity"`
	ExistingPatterns      []string       `json:"existing_patterns"`
	RelevantFiles         []string       `json:"relevant_files"`
	ConflictingApproaches []string       `json:"conflicting_approaches"`
	Metadata              map[string]any `json:"metadata,omitempty"`
}

// consultLibrarian requests codebase context from the Librarian agent.
func (a *Academic) consultLibrarian(ctx context.Context, query *ResearchQuery) (*LibrarianContext, error) {
	if !a.running || a.bus == nil {
		return nil, fmt.Errorf("academic agent not running")
	}

	// Check if Librarian is available
	if _, exists := a.knownAgents["librarian"]; !exists {
		return nil, fmt.Errorf("librarian agent not available")
	}

	correlationID := uuid.New().String()
	req := &guide.RouteRequest{
		CorrelationID: correlationID,
		Input:         fmt.Sprintf("@librarian codebase context for: %s", query.Query),
		SourceAgentID: "academic",
		TargetAgentID: "librarian",
		Timestamp:     time.Now(),
	}

	// Track pending request
	a.mu.Lock()
	a.pendingRequests[correlationID] = &pendingResearch{
		query:     query,
		createdAt: time.Now(),
		timeout:   a.config.LibrarianTimeout,
	}
	a.mu.Unlock()

	// Publish request
	if err := a.PublishRequest(req); err != nil {
		a.mu.Lock()
		delete(a.pendingRequests, correlationID)
		a.mu.Unlock()
		return nil, err
	}

	// For now, return empty context - actual implementation would wait for response
	// In a production system, this would use channels or context with timeout
	return &LibrarianContext{
		CodebaseMaturity: "unknown",
	}, nil
}

// executeResearch performs the actual research logic.
func (a *Academic) executeResearch(ctx context.Context, query *ResearchQuery, libCtx *LibrarianContext, pastOutcomes []*OutcomeRecord) (*ResearchResult, error) {
	queryID := uuid.New().String()
	now := time.Now()

	// Build findings based on research
	findings := a.gatherFindings(ctx, query)

	// Build recommendations with applicability analysis
	recommendations := a.buildRecommendations(ctx, query, findings, libCtx, pastOutcomes)

	// Determine overall confidence
	confidence := a.calculateOverallConfidence(findings, recommendations, libCtx)

	result := &ResearchResult{
		QueryID:          queryID,
		Findings:         findings,
		Recommendations:  recommendations,
		SourcesConsulted: a.collectSourceIDs(findings),
		Confidence:       confidence,
		GeneratedAt:      now,
	}

	return result, nil
}

// gatherFindings collects research findings for the query.
func (a *Academic) gatherFindings(ctx context.Context, query *ResearchQuery) []Finding {
	// This would integrate with actual research sources
	// For now, return placeholder showing the structure
	return []Finding{}
}

// buildRecommendations creates recommendations with applicability analysis.
func (a *Academic) buildRecommendations(ctx context.Context, query *ResearchQuery, findings []Finding, libCtx *LibrarianContext, pastOutcomes []*OutcomeRecord) []Recommendation {
	var recommendations []Recommendation

	for _, finding := range findings {
		applicability := a.analyzeApplicability(finding, libCtx, pastOutcomes)

		if applicability.Score < a.config.MinApplicability {
			continue // Skip low-applicability findings
		}

		rec := Recommendation{
			ID:            uuid.New().String(),
			Title:         finding.Topic,
			Description:   finding.Summary,
			Rationale:     finding.Details,
			Applicability: applicability.Classification,
			Confidence:    a.adjustConfidence(finding.Confidence, applicability, pastOutcomes),
			SourceIDs:     finding.SourceIDs,
		}

		recommendations = append(recommendations, rec)
	}

	return recommendations
}

// ApplicabilityResult contains the applicability analysis.
type ApplicabilityResult struct {
	Score          float64 `json:"score"`
	Classification string  `json:"classification"` // DIRECT, ADAPTABLE, INCOMPATIBLE
	Reasoning      string  `json:"reasoning"`
}

// analyzeApplicability determines how applicable a finding is to the codebase.
func (a *Academic) analyzeApplicability(finding Finding, libCtx *LibrarianContext, pastOutcomes []*OutcomeRecord) *ApplicabilityResult {
	result := &ApplicabilityResult{
		Score:          0.5,
		Classification: "ADAPTABLE",
	}

	// Adjust based on codebase maturity
	if libCtx != nil {
		switch libCtx.CodebaseMaturity {
		case "mature":
			// Mature codebases need more careful integration
			result.Score *= 0.8
		case "new":
			// New codebases can adopt patterns more directly
			result.Score *= 1.2
		}

		// Check for conflicts with existing patterns
		for _, conflict := range libCtx.ConflictingApproaches {
			if containsSubstring(finding.Topic, conflict) {
				result.Score *= 0.5
				result.Classification = "INCOMPATIBLE"
				result.Reasoning = fmt.Sprintf("Conflicts with existing approach: %s", conflict)
				break
			}
		}
	}

	// Adjust based on past outcomes
	for _, outcome := range pastOutcomes {
		if outcome.Success {
			result.Score *= 1.1
		} else {
			result.Score *= 0.7
		}
	}

	// Clamp score
	if result.Score > 1.0 {
		result.Score = 1.0
	}
	if result.Score < 0.0 {
		result.Score = 0.0
	}

	// Update classification based on final score
	if result.Score >= 0.8 {
		result.Classification = "DIRECT"
	} else if result.Score >= 0.4 {
		result.Classification = "ADAPTABLE"
	} else {
		result.Classification = "INCOMPATIBLE"
	}

	return result
}

// adjustConfidence adjusts confidence based on applicability and past outcomes.
func (a *Academic) adjustConfidence(base ConfidenceLevel, applicability *ApplicabilityResult, pastOutcomes []*OutcomeRecord) ConfidenceLevel {
	// Start with base confidence as numeric
	var score float64
	switch base {
	case ConfidenceLevelHigh:
		score = 0.9
	case ConfidenceLevelMedium:
		score = 0.6
	case ConfidenceLevelLow:
		score = 0.3
	default:
		score = 0.5
	}

	// Adjust by applicability
	score *= applicability.Score

	// Adjust by past outcomes
	successCount := 0
	for _, outcome := range pastOutcomes {
		if outcome.Success {
			successCount++
		}
	}
	if len(pastOutcomes) > 0 {
		successRate := float64(successCount) / float64(len(pastOutcomes))
		score = score*0.7 + successRate*0.3
	}

	// Convert back to confidence level
	if score >= 0.7 {
		return ConfidenceLevelHigh
	} else if score >= 0.4 {
		return ConfidenceLevelMedium
	}
	return ConfidenceLevelLow
}

// calculateOverallConfidence determines the overall confidence for a result.
func (a *Academic) calculateOverallConfidence(findings []Finding, recommendations []Recommendation, libCtx *LibrarianContext) ConfidenceLevel {
	if len(findings) == 0 {
		return ConfidenceLevelLow
	}

	highCount := 0
	for _, f := range findings {
		if f.Confidence == ConfidenceLevelHigh {
			highCount++
		}
	}

	ratio := float64(highCount) / float64(len(findings))

	// Boost confidence if we have Librarian validation
	if libCtx != nil && libCtx.CodebaseMaturity != "unknown" {
		ratio += 0.1
	}

	if ratio >= 0.7 {
		return ConfidenceLevelHigh
	} else if ratio >= 0.4 {
		return ConfidenceLevelMedium
	}
	return ConfidenceLevelLow
}

// collectSourceIDs extracts all source IDs from findings.
func (a *Academic) collectSourceIDs(findings []Finding) []string {
	seen := make(map[string]bool)
	var result []string

	for _, f := range findings {
		for _, id := range f.SourceIDs {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}

	return result
}

// =============================================================================
// Caching
// =============================================================================

func (a *Academic) cacheKey(query *ResearchQuery) string {
	return fmt.Sprintf("%s:%s:%s", query.Query, query.Domain, query.LanguageFilter)
}

func (a *Academic) getCached(key string) *ResearchResult {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result, exists := a.researchCache[key]
	if !exists {
		return nil
	}

	// Check expiry
	if result.CachedAt != nil && time.Since(*result.CachedAt) > a.config.CacheExpiry {
		return nil
	}

	return result
}

func (a *Academic) setCached(key string, result *ResearchResult) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	result.CachedAt = &now
	a.researchCache[key] = result
}

// =============================================================================
// Outcome Tracking
// =============================================================================

// OutcomeRecord tracks the outcome of a recommendation.
type OutcomeRecord struct {
	ID               string    `json:"id"`
	Query            string    `json:"query"`
	RecommendationID string    `json:"recommendation_id"`
	Success          bool      `json:"success"`
	Notes            string    `json:"notes,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
}

// OutcomeHistory tracks historical outcomes for maturity-aware recommendations.
type OutcomeHistory struct {
	mu       sync.RWMutex
	outcomes []*OutcomeRecord
	limit    int
}

// NewOutcomeHistory creates a new outcome history tracker.
func NewOutcomeHistory(limit int) *OutcomeHistory {
	return &OutcomeHistory{
		outcomes: make([]*OutcomeRecord, 0),
		limit:    limit,
	}
}

// Record adds an outcome to the history.
func (h *OutcomeHistory) Record(outcome *OutcomeRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	outcome.RecordedAt = time.Now()
	h.outcomes = append(h.outcomes, outcome)

	// Trim if over limit
	if len(h.outcomes) > h.limit {
		h.outcomes = h.outcomes[1:]
	}
}

// GetSimilar returns outcomes for similar queries.
func (h *OutcomeHistory) GetSimilar(query string, limit int) []*OutcomeRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []*OutcomeRecord
	queryLower := toLower(query)

	for i := len(h.outcomes) - 1; i >= 0 && len(result) < limit; i-- {
		outcome := h.outcomes[i]
		if containsSubstring(toLower(outcome.Query), queryLower) ||
			containsSubstring(queryLower, toLower(outcome.Query)) {
			result = append(result, outcome)
		}
	}

	return result
}

// RecordOutcome records the outcome of a recommendation.
func (a *Academic) RecordOutcome(recommendationID string, success bool, notes string) {
	outcome := &OutcomeRecord{
		ID:               uuid.New().String(),
		RecommendationID: recommendationID,
		Success:          success,
		Notes:            notes,
	}
	a.outcomeHistory.Record(outcome)
}

// =============================================================================
// Guide Registration
// =============================================================================

// Registration returns the agent registration for the Guide.
func (a *Academic) Registration() *guide.AgentRegistration {
	return &guide.AgentRegistration{
		ID:      "academic",
		Name:    "academic",
		Aliases: []string{"research", "scholar"},
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall, guide.IntentCheck},
			Domains: []guide.Domain{guide.DomainPatterns, guide.DomainDecisions, guide.DomainLearnings},
			Tags:    []string{"research", "best-practices", "external-knowledge"},
			Keywords: []string{
				"research", "best practice", "recommend", "compare",
				"approach", "pattern", "methodology", "standard",
			},
			Priority: 50,
		},
		Constraints: guide.AgentConstraints{
			MinConfidence: 0.5,
		},
		Description: "Researches external knowledge, best practices, and technical approaches. Always validates against codebase reality via Librarian.",
		Priority:    50,
	}
}
