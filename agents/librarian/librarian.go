package librarian

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// Librarian is the code search and pattern detection agent for the Sylk system.
// It serves as the SINGLE SOURCE OF TRUTH for formatters, linters, test frameworks,
// and coding patterns in a codebase.
type Librarian struct {
	config Config
	logger *slog.Logger

	// Search and indexing
	searchHandler *SearchHandler
	domainFilter  *LibrarianDomainFilter

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement
}

// Config holds configuration for the Librarian agent
type Config struct {
	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Search configuration
	SearchSystem SearchSystem // Required: the search backend

	// Logging
	Logger *slog.Logger // Optional, uses slog.Default() if nil
}

// Default configuration values
const (
	DefaultMaxOutputTokens = 4096
)

// New creates a new Librarian agent
func New(cfg Config) (*Librarian, error) {
	cfg = applyConfigDefaults(cfg)

	if cfg.SearchSystem == nil {
		return nil, fmt.Errorf("search system is required")
	}

	librarian := &Librarian{
		config:        cfg,
		logger:        cfg.Logger,
		searchHandler: NewSearchHandler(cfg.SearchSystem),
		domainFilter:  NewLibrarianDomainFilter(cfg.Logger),
		knownAgents:   make(map[string]*guide.AgentAnnouncement),
	}

	librarian.initSkills()

	return librarian, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func (l *Librarian) initSkills() {
	l.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{"search_codebase", "find_pattern", "locate_symbol"}
	loaderCfg.AutoLoadDomains = []string{"code", "search"}
	l.skillLoader = skills.NewLoader(l.skills, loaderCfg)

	l.registerCoreSkills()
}

// Close closes the librarian and its resources
func (l *Librarian) Close() error {
	l.Stop()
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The librarian subscribes to its own channels and the registry topic.
func (l *Librarian) Start(bus guide.EventBus) error {
	if l.running {
		return fmt.Errorf("librarian is already running")
	}

	l.bus = bus
	l.channels = guide.NewAgentChannels("librarian")

	// Subscribe to own request channel (librarian.requests)
	var err error
	l.requestSub, err = bus.SubscribeAsync(l.channels.Requests, l.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", l.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	l.responseSub, err = bus.SubscribeAsync(l.channels.Responses, l.handleBusResponse)
	if err != nil {
		l.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", l.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	l.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, l.handleRegistryAnnouncement)
	if err != nil {
		l.requestSub.Unsubscribe()
		l.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	l.running = true
	l.logger.Info("librarian started", "channels", l.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (l *Librarian) Stop() error {
	if !l.running {
		return nil
	}

	errs := l.unsubscribeAll()
	l.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	l.logger.Info("librarian stopped")
	return nil
}

func (l *Librarian) unsubscribeAll() []error {
	var errs []error
	if err := l.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := l.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := l.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (l *Librarian) unsubscribeRequest() error {
	if l.requestSub == nil {
		return nil
	}
	err := l.requestSub.Unsubscribe()
	l.requestSub = nil
	return err
}

func (l *Librarian) unsubscribeResponse() error {
	if l.responseSub == nil {
		return nil
	}
	err := l.responseSub.Unsubscribe()
	l.responseSub = nil
	return err
}

func (l *Librarian) unsubscribeRegistry() error {
	if l.registrySub == nil {
		return nil
	}
	err := l.registrySub.Unsubscribe()
	l.registrySub = nil
	return err
}

// IsRunning returns true if the librarian is actively processing bus messages
func (l *Librarian) IsRunning() bool {
	return l.running
}

// Bus returns the event bus used by the librarian
func (l *Librarian) Bus() guide.EventBus {
	return l.bus
}

// Channels returns the librarian's channel configuration
func (l *Librarian) Channels() *guide.AgentChannels {
	return l.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus
func (l *Librarian) handleBusRequest(msg *guide.Message) error {
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

	result, err := l.processForwardedRequest(ctx, fwd)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "librarian",
		RespondingAgentName: "librarian",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			l.generateMessageID(),
			fwd.CorrelationID,
			"librarian",
			err.Error(),
		)
		return l.bus.Publish(l.channels.Errors, errMsg)
	}

	resp.Data = result

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(l.generateMessageID(), resp)
	return l.bus.Publish(l.channels.Responses, respMsg)
}

func (l *Librarian) generateMessageID() string {
	return fmt.Sprintf("librarian_msg_%s", uuid.New().String())
}

// processForwardedRequest handles the actual request processing
func (l *Librarian) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := l.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (l *Librarian) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentFind, guide.IntentSearch, guide.IntentLocate:
		return l.handleSearch, nil
	case guide.IntentRecall:
		return l.handleRecall, nil
	case guide.IntentCheck:
		return l.handleCheck, nil
	default:
		return nil, fmt.Errorf("unsupported intent: %s", intent)
	}
}

// handleSearch processes search requests (find, search, locate intents)
func (l *Librarian) handleSearch(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &LibrarianRequest{
		ID:        uuid.New().String(),
		Intent:    IntentRecall,
		Domain:    DomainCode,
		Query:     fwd.Input,
		SessionID: "",
		Timestamp: time.Now(),
	}

	if fwd.Entities != nil {
		req.Params = make(map[string]any)
		if fwd.Entities.Limit > 0 {
			req.Params["limit"] = fwd.Entities.Limit
		}
		if len(fwd.Entities.FilePaths) > 0 {
			req.Params["path_prefix"] = fwd.Entities.FilePaths[0]
		}
	}

	return l.searchHandler.Handle(ctx, req)
}

// handleRecall processes recall requests (query past data)
func (l *Librarian) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	return l.handleSearch(ctx, fwd)
}

// handleCheck processes check/verification requests
func (l *Librarian) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Check if a pattern/file exists
	req := &LibrarianRequest{
		ID:        uuid.New().String(),
		Intent:    IntentCheck,
		Domain:    DomainCode,
		Query:     fwd.Input,
		Params:    map[string]any{"limit": 1},
		Timestamp: time.Now(),
	}

	resp, err := l.searchHandler.Handle(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert to check result format
	if resp.Success {
		data, ok := resp.Data.(map[string]any)
		if ok {
			results, _ := data["results"].([]EnrichedResult)
			return map[string]any{
				"found": len(results) > 0,
				"count": len(results),
				"data":  results,
			}, nil
		}
	}

	return map[string]any{
		"found": false,
		"count": 0,
	}, nil
}

// handleBusResponse processes responses to requests we made
func (l *Librarian) handleBusResponse(msg *guide.Message) error {
	// For now, just log responses to our requests
	// Future: implement callback handling for async sub-requests
	l.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (l *Librarian) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		l.knownAgents[ann.AgentID] = ann
		l.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(l.knownAgents, ann.AgentID)
		l.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the librarian knows about
func (l *Librarian) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(l.knownAgents))
	for k, v := range l.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// Direct API Methods
// =============================================================================

// Handle processes a LibrarianRequest directly (without event bus)
func (l *Librarian) Handle(ctx context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	switch req.Domain {
	case DomainCode:
		return l.searchHandler.Handle(ctx, req)
	case DomainPatterns:
		return l.handlePatternRequest(ctx, req)
	case DomainTooling:
		return l.handleToolingRequest(ctx, req)
	default:
		return l.searchHandler.Handle(ctx, req)
	}
}

// handlePatternRequest handles pattern detection requests
func (l *Librarian) handlePatternRequest(ctx context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	// Convert to code search with pattern focus
	codeReq := &LibrarianRequest{
		ID:        req.ID,
		Intent:    req.Intent,
		Domain:    DomainCode,
		Query:     req.Query,
		Params:    req.Params,
		SessionID: req.SessionID,
		Timestamp: req.Timestamp,
	}

	resp, err := l.searchHandler.Handle(ctx, codeReq)
	if err != nil {
		return nil, err
	}

	// Add pattern analysis metadata
	if resp.Success && resp.Data != nil {
		if data, ok := resp.Data.(map[string]any); ok {
			data["domain"] = "patterns"
			data["analysis_type"] = "pattern_detection"
		}
	}

	return resp, nil
}

// handleToolingRequest handles tooling configuration requests
func (l *Librarian) handleToolingRequest(ctx context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	start := time.Now()

	// Search for tooling configuration files
	toolingPatterns := []string{
		".golangci.yml", ".golangci.yaml", "golangci.toml",
		".eslintrc", ".eslintrc.js", ".eslintrc.json",
		"prettier.config.js", ".prettierrc",
		"pyproject.toml", "setup.cfg", ".flake8",
		"Makefile", "justfile",
		"go.mod", "package.json", "Cargo.toml", "requirements.txt",
	}

	results := make([]map[string]any, 0)
	for _, pattern := range toolingPatterns {
		if l.domainFilter.IsCodeContent(pattern) {
			results = append(results, map[string]any{
				"pattern": pattern,
				"type":    "tooling_config",
			})
		}
	}

	return &LibrarianResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Data: map[string]any{
			"tooling_patterns": toolingPatterns,
			"domain":           "tooling",
		},
		Took:      time.Since(start),
		Timestamp: time.Now(),
	}, nil
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the librarian's routing information for Guide registration
func (l *Librarian) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      "librarian",
		Name:    "librarian",
		Aliases: []string{"lib", "search", "find"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "search",
				Description:   "Search the codebase for code, patterns, or symbols",
				DefaultIntent: guide.IntentSearch,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "find",
				Description:   "Find specific files, symbols, or patterns",
				DefaultIntent: guide.IntentFind,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "locate",
				Description:   "Locate where a symbol is defined or used",
				DefaultIntent: guide.IntentLocate,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"find",
				"search",
				"locate",
				"where is",
				"show me",
				"look for",
				"grep",
				"definition of",
				"usages of",
				"references to",
				"pattern",
				"linter",
				"formatter",
				"test framework",
			},
			WeakTriggers: []string{
				"code",
				"file",
				"function",
				"class",
				"method",
				"symbol",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentFind: {
					"find",
					"where is",
					"locate",
					"show me where",
				},
				guide.IntentSearch: {
					"search",
					"look for",
					"grep",
					"scan",
				},
				guide.IntentLocate: {
					"definition",
					"declaration",
					"usages",
					"references",
					"implementations",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      "librarian",
			Name:    "librarian",
			Aliases: []string{"lib", "search", "find"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentFind,
					guide.IntentSearch,
					guide.IntentLocate,
					guide.IntentRecall,
					guide.IntentCheck,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
				},
				Tags: []string{"search", "code", "patterns", "symbols", "tooling"},
				Keywords: []string{
					"find", "search", "locate", "grep", "pattern",
					"symbol", "definition", "reference", "usage",
					"linter", "formatter", "test", "tooling",
				},
				Priority: 80,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Code search and pattern detection. SINGLE SOURCE OF TRUTH for formatters, linters, test frameworks, and coding patterns.",
			Priority:    80,
		},
	}
}

// PublishRequest publishes a request to the Guide for routing
func (l *Librarian) PublishRequest(req *guide.RouteRequest) error {
	if !l.running {
		return fmt.Errorf("librarian is not running")
	}

	req.SourceAgentID = "librarian"
	req.SourceAgentName = "librarian"

	msg := guide.NewRequestMessage(l.generateMessageID(), req)
	return l.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Skills System
// =============================================================================

// Skills returns the librarian's skill registry
func (l *Librarian) Skills() *skills.Registry {
	return l.skills
}

// GetToolDefinitions returns tool definitions for all loaded skills
func (l *Librarian) GetToolDefinitions() []map[string]any {
	return l.skills.GetToolDefinitions()
}
