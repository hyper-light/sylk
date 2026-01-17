package guide

import (
	"regexp"
	"time"
)

// =============================================================================
// Compiled Pattern
// =============================================================================

// CompiledPattern is a pre-compiled regex pattern for capability matching
type CompiledPattern struct {
	// Original pattern string
	Pattern string `json:"pattern"`

	// Compiled regex (not serialized)
	regex *regexp.Regexp

	// Specificity score (auto-calculated)
	// Higher = more specific pattern
	Specificity int `json:"specificity"`
}

// NewCompiledPattern creates a new compiled pattern
func NewCompiledPattern(pattern string) (*CompiledPattern, error) {
	regex, err := regexp.Compile("(?i)" + pattern) // Case-insensitive
	if err != nil {
		return nil, err
	}

	return &CompiledPattern{
		Pattern:     pattern,
		regex:       regex,
		Specificity: calculatePatternSpecificity(pattern),
	}, nil
}

// MatchString returns true if the input matches the pattern
func (p *CompiledPattern) MatchString(input string) bool {
	if p.regex == nil {
		// Try to compile if not already compiled
		regex, err := regexp.Compile("(?i)" + p.Pattern)
		if err != nil {
			return false
		}
		p.regex = regex
	}
	return p.regex.MatchString(input)
}

// calculatePatternSpecificity estimates how specific a pattern is
// More specific patterns should win over general ones
func calculatePatternSpecificity(pattern string) int {
	score := 0

	// Longer patterns are generally more specific
	score += len(pattern) / 5

	// Literal characters are more specific than wildcards
	for _, c := range pattern {
		switch c {
		case '.', '*', '+', '?', '[', ']', '(', ')', '|', '^', '$':
			// Wildcards reduce specificity
			score--
		default:
			// Literal characters increase specificity
			score++
		}
	}

	// Word boundaries increase specificity
	if len(pattern) > 2 && (pattern[:2] == "\\b" || pattern[len(pattern)-2:] == "\\b") {
		score += 5
	}

	return score
}

// =============================================================================
// Intent Classification
// =============================================================================

// Intent represents the classified intent of a query
type Intent string

const (
	// Query intents (retrieving information)
	IntentRecall Intent = "recall" // Query past data
	IntentCheck  Intent = "check"  // Verify against history

	// Store intents (recording information)
	IntentStore Intent = "store" // Record new data

	// Coordination intents
	IntentDeclare  Intent = "declare"  // Announce intent to work on something
	IntentComplete Intent = "complete" // Mark work as completed

	// Meta intents
	IntentHelp    Intent = "help"    // Request help/guidance
	IntentStatus  Intent = "status"  // Check system status
	IntentUnknown Intent = "unknown" // Could not classify
)

// AllIntents returns all valid intents
func AllIntents() []Intent {
	return []Intent{
		IntentRecall,
		IntentCheck,
		IntentStore,
		IntentDeclare,
		IntentComplete,
		IntentHelp,
		IntentStatus,
	}
}

// IsQueryIntent returns true if the intent is for querying data
func (i Intent) IsQueryIntent() bool {
	return i == IntentRecall || i == IntentCheck
}

// IsStoreIntent returns true if the intent is for storing data
func (i Intent) IsStoreIntent() bool {
	return i == IntentStore
}

// IsCoordinationIntent returns true if the intent is for coordination
func (i Intent) IsCoordinationIntent() bool {
	return i == IntentDeclare || i == IntentComplete
}

// =============================================================================
// Domain Classification
// =============================================================================

// Domain represents the domain/category of the query
type Domain string

const (
	// Historical domains (handled by Archivalist)
	DomainPatterns  Domain = "patterns"  // Code patterns, architectural patterns
	DomainFailures  Domain = "failures"  // Failed approaches, errors encountered
	DomainDecisions Domain = "decisions" // Design decisions, choices made
	DomainFiles     Domain = "files"     // File states, modifications made
	DomainLearnings Domain = "learnings" // Lessons learned, insights gained

	// Coordination domains
	DomainIntents Domain = "intents" // Work intentions, declarations
	DomainAgents  Domain = "agents"  // Agent status, registry

	// Meta domains
	DomainSystem  Domain = "system"  // System status, health
	DomainUnknown Domain = "unknown" // Could not classify
)

// AllDomains returns all valid domains
func AllDomains() []Domain {
	return []Domain{
		DomainPatterns,
		DomainFailures,
		DomainDecisions,
		DomainFiles,
		DomainLearnings,
		DomainIntents,
		DomainAgents,
		DomainSystem,
	}
}

// IsHistoricalDomain returns true if the domain is handled by the Archivalist
func (d Domain) IsHistoricalDomain() bool {
	switch d {
	case DomainPatterns, DomainFailures, DomainDecisions, DomainFiles, DomainLearnings:
		return true
	default:
		return false
	}
}

// =============================================================================
// Target Agent Classification
// =============================================================================

// TargetAgent represents which agent should handle a request
type TargetAgent string

const (
	TargetArchivalist TargetAgent = "archivalist" // Historical data, patterns, failures
	TargetGuide       TargetAgent = "guide"       // Routing, help, status
	TargetUnknown     TargetAgent = "unknown"     // Could not determine target
)

// AllTargetAgents returns all valid target agents
func AllTargetAgents() []TargetAgent {
	return []TargetAgent{
		TargetArchivalist,
		TargetGuide,
	}
}

// =============================================================================
// Temporal Classification
// =============================================================================

// TemporalFocus indicates whether a query is about the past or future
type TemporalFocus string

const (
	TemporalPast    TemporalFocus = "past"    // Retrospective - about what happened
	TemporalFuture  TemporalFocus = "future"  // Prospective - about what should happen
	TemporalPresent TemporalFocus = "present" // Current state queries
	TemporalUnknown TemporalFocus = "unknown" // Could not determine
)

// =============================================================================
// Routing Types
// =============================================================================

// RouteRequest represents an incoming request to be routed
type RouteRequest struct {
	// Correlation ID (assigned by Guide if not provided)
	CorrelationID string `json:"correlation_id,omitempty"`

	// Parent correlation ID for sub-requests (enables request chaining/tracing)
	ParentCorrelationID string `json:"parent_correlation_id,omitempty"`

	// Input text (structured DSL or natural language)
	Input string `json:"input"`

	// Source agent information (required for return routing)
	SourceAgentID   string `json:"source_agent_id"`
	SourceAgentName string `json:"source_agent_name,omitempty"`

	// Optional: explicit target (bypasses classification)
	TargetAgentID string `json:"target_agent_id,omitempty"`

	// FireAndForget indicates this is an async sub-request that doesn't need
	// a response routed back to the source. The target processes it in the
	// background while the source continues with its work.
	FireAndForget bool `json:"fire_and_forget,omitempty"`

	// Session and request metadata
	SessionID string    `json:"session_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// RouteResponse represents a response from a target agent
type RouteResponse struct {
	// Correlation ID (REQUIRED - matches original request)
	CorrelationID string `json:"correlation_id"`

	// Response data
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`

	// Source of response (the target agent that processed the request)
	RespondingAgentID   string `json:"responding_agent_id"`
	RespondingAgentName string `json:"responding_agent_name,omitempty"`

	// Metadata
	ProcessingTime time.Duration `json:"processing_time,omitempty"`
}

type StreamResponse struct {
	CorrelationID     string       `json:"correlation_id"`
	RespondingAgentID string       `json:"responding_agent_id"`
	TargetAgentID     string       `json:"target_agent_id"`
	Event             *StreamEvent `json:"event"`
}

// ForwardedRequest is what the Guide sends to the target agent
type ForwardedRequest struct {
	// Correlation ID (for response routing back through Guide)
	CorrelationID string `json:"correlation_id"`

	// Parent correlation ID for request chain tracing
	ParentCorrelationID string `json:"parent_correlation_id,omitempty"`

	// Original request info
	Input    string             `json:"input"`
	Intent   Intent             `json:"intent"`
	Domain   Domain             `json:"domain"`
	Entities *ExtractedEntities `json:"entities,omitempty"`

	// Source agent info (for context, target knows who is asking)
	SourceAgentID   string `json:"source_agent_id"`
	SourceAgentName string `json:"source_agent_name,omitempty"`

	// FireAndForget indicates no response is expected.
	// Target should process async and not publish a response message.
	FireAndForget bool `json:"fire_and_forget,omitempty"`

	// Classification metadata
	Confidence           float64 `json:"confidence"`
	ClassificationMethod string  `json:"classification_method"` // "dsl" or "llm"
}

// PendingRequest tracks a request awaiting response
type PendingRequest struct {
	CorrelationID   string        `json:"correlation_id"`
	SourceAgentID   string        `json:"source_agent_id"`
	SourceAgentName string        `json:"source_agent_name,omitempty"`
	TargetAgentID   string        `json:"target_agent_id"`
	Request         *RouteRequest `json:"request"`
	Classification  *RouteResult  `json:"classification,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	ExpiresAt       time.Time     `json:"expires_at"`
}

// RouteResult represents the result of routing a request
type RouteResult struct {
	// Classification results
	Intent        Intent        `json:"intent"`
	Domain        Domain        `json:"domain"`
	TargetAgent   TargetAgent   `json:"target_agent"`
	TemporalFocus TemporalFocus `json:"temporal_focus"`

	// Extracted entities
	Entities *ExtractedEntities `json:"entities,omitempty"`

	// Confidence and decision
	Confidence float64     `json:"confidence"`
	Action     RouteAction `json:"action"`
	Rejected   bool        `json:"rejected"`
	Reason     string      `json:"reason,omitempty"`

	// For multi-intent queries
	SubResults []*RouteResult `json:"sub_results,omitempty"`

	// Metadata
	ClassificationMethod string        `json:"classification_method"` // "dsl" or "llm"
	ProcessingTime       time.Duration `json:"processing_time"`
}

// RouteAction indicates what action to take based on confidence
type RouteAction string

const (
	RouteActionExecute RouteAction = "execute" // Execute immediately (confidence >= 0.90)
	RouteActionLog     RouteAction = "log"     // Execute and log for review (0.75-0.89)
	RouteActionSuggest RouteAction = "suggest" // Suggest interpretation (0.50-0.74)
	RouteActionReject  RouteAction = "reject"  // Reject with explanation (< 0.50)
)

// ExtractedEntities contains entities extracted from the query
type ExtractedEntities struct {
	// Scope/context
	Scope     string   `json:"scope,omitempty"`      // Area being queried (e.g., "authentication")
	FilePaths []string `json:"file_paths,omitempty"` // File paths mentioned

	// Time references
	Timeframe string `json:"timeframe,omitempty"` // Time reference (e.g., "yesterday")

	// Agent references
	AgentID   string `json:"agent_id,omitempty"`   // Specific agent mentioned
	AgentName string `json:"agent_name,omitempty"` // Agent name mentioned

	// Error/failure context
	ErrorType    string `json:"error_type,omitempty"`    // Type of error
	ErrorMessage string `json:"error_message,omitempty"` // Error message

	// Data payload (for store operations)
	Data map[string]any `json:"data,omitempty"`

	// Query parameters
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Query  string `json:"query,omitempty"` // Free-form query text
}

// =============================================================================
// Classification Types
// =============================================================================

// ClassificationResult represents the output of LLM classification
type ClassificationResult struct {
	// Temporal gate
	IsRetrospective bool   `json:"is_retrospective"`
	RejectionReason string `json:"rejection_reason,omitempty"`

	// Classification
	Intent Intent `json:"intent"`
	Domain Domain `json:"domain"`

	// Entities
	Entities *ExtractedEntities `json:"entities,omitempty"`

	// Confidence
	Confidence float64 `json:"confidence"`

	// For multi-intent
	MultiIntent bool                    `json:"multi_intent"`
	SubResults  []*ClassificationResult `json:"sub_results,omitempty"`
}

// =============================================================================
// Learning Types
// =============================================================================

// CorrectionRecord represents a correction to a classification
type CorrectionRecord struct {
	// Original input
	Input string `json:"input"`

	// Wrong classification
	WrongIntent Intent      `json:"wrong_intent"`
	WrongDomain Domain      `json:"wrong_domain"`
	WrongTarget TargetAgent `json:"wrong_target"`

	// Correct classification
	CorrectIntent Intent      `json:"correct_intent"`
	CorrectDomain Domain      `json:"correct_domain"`
	CorrectTarget TargetAgent `json:"correct_target"`

	// Metadata
	CorrectedBy string    `json:"corrected_by"` // Agent or user who corrected
	CorrectedAt time.Time `json:"corrected_at"`
	Reason      string    `json:"reason,omitempty"`
}

// QueryPattern tracks patterns in queries for learning
type QueryPattern struct {
	// Pattern signature (normalized query)
	Pattern string `json:"pattern"`

	// Most common classification
	Intent Intent      `json:"intent"`
	Domain Domain      `json:"domain"`
	Target TargetAgent `json:"target"`

	// Statistics
	SuccessCount    int64   `json:"success_count"`
	FailureCount    int64   `json:"failure_count"`
	CorrectionCount int64   `json:"correction_count"`
	AvgConfidence   float64 `json:"avg_confidence"`

	// Timestamps
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// =============================================================================
// DSL Types
// =============================================================================

// DSLCommandType indicates what kind of DSL command this is
type DSLCommandType string

const (
	// DSLCommandTypeFull is the full DSL: @agent:intent:domain?params{data}
	DSLCommandTypeFull DSLCommandType = "full"

	// DSLCommandTypeDirect is direct routing: @to:<agent> or @from:<agent>
	DSLCommandTypeDirect DSLCommandType = "direct"

	// DSLCommandTypeIntent is intent-based routing: @guide <query>
	DSLCommandTypeIntent DSLCommandType = "intent"

	// DSLCommandTypeAction is action shortcut: @archive, @recall, @store
	DSLCommandTypeAction DSLCommandType = "action"
)

// DSLDirection indicates if this is a request or response
type DSLDirection string

const (
	DSLDirectionTo   DSLDirection = "to"   // Request going to an agent
	DSLDirectionFrom DSLDirection = "from" // Response coming from an agent
)

// DSLCommand represents a parsed structured command
type DSLCommand struct {
	// Command type
	Type DSLCommandType `json:"type"`

	// For direct routing (@to/@from)
	Direction   DSLDirection `json:"direction,omitempty"`
	TargetAgent string       `json:"target_agent,omitempty"` // Agent name for direct routing

	// For full DSL commands
	Intent Intent `json:"intent,omitempty"`
	Domain Domain `json:"domain,omitempty"`

	// Parameters (for full DSL)
	Params map[string]string `json:"params,omitempty"`

	// Data payload (for store operations or direct routing)
	Data map[string]any `json:"data,omitempty"`

	// The query/payload text (for @guide, @to, @from, @archive)
	Query string `json:"query,omitempty"`

	// Raw input
	Raw string `json:"raw"`
}

// IsDirectRoute returns true if this is a direct routing command
func (cmd *DSLCommand) IsDirectRoute() bool {
	return cmd.Type == DSLCommandTypeDirect
}

// IsIntentRoute returns true if this requires intent-based classification
func (cmd *DSLCommand) IsIntentRoute() bool {
	return cmd.Type == DSLCommandTypeIntent
}

// NeedsClassification returns true if LLM classification is needed
func (cmd *DSLCommand) NeedsClassification() bool {
	return cmd.Type == DSLCommandTypeIntent
}

// DSLParseError represents an error parsing a DSL command
type DSLParseError struct {
	Input    string `json:"input"`
	Position int    `json:"position"`
	Expected string `json:"expected"`
	Got      string `json:"got"`
	Message  string `json:"message"`
}

func (e *DSLParseError) Error() string {
	return e.Message
}

// =============================================================================
// Agent Registration Types
// =============================================================================

// AgentRegistration describes a registered agent and its capabilities.
// This is what the Guide uses to determine which agent can handle a request.
type AgentRegistration struct {
	// Identity
	ID   string `json:"id"`   // Unique agent ID
	Name string `json:"name"` // Human-readable name

	// Aliases for DSL routing (e.g., "arch" -> "archivalist")
	Aliases []string `json:"aliases,omitempty"`

	// What this agent handles
	Capabilities AgentCapabilities `json:"capabilities"`

	// Constraints on what this agent accepts
	Constraints AgentConstraints `json:"constraints"`

	// Brief description for LLM context (keep short for token efficiency)
	Description string `json:"description"`

	// Priority for routing conflicts (higher = preferred)
	Priority int `json:"priority"`
}

// AgentCapabilities declares what an agent can handle
type AgentCapabilities struct {
	// Supported intents (empty = all intents)
	Intents []Intent `json:"intents,omitempty"`

	// Supported domains (empty = all domains)
	Domains []Domain `json:"domains,omitempty"`

	// Custom capability tags for extensibility
	Tags []string `json:"tags,omitempty"`

	// Patterns for capability-based routing (compiled regexes)
	// Agents register patterns that match inputs they can handle
	Patterns []*CompiledPattern `json:"patterns,omitempty"`

	// Keywords for simple matching (case-insensitive)
	// If any keyword appears in input, this agent is a candidate
	Keywords []string `json:"keywords,omitempty"`

	// Priority for routing conflicts (higher = preferred)
	// When multiple agents match, highest priority wins
	// Default: 0, range: -100 to 100
	Priority int `json:"priority,omitempty"`

	// Specificity score (auto-calculated from patterns/keywords)
	// More specific matchers get higher scores
	// Used as tiebreaker when priorities are equal
	Specificity int `json:"specificity,omitempty"`
}

// AgentConstraints declares restrictions on what an agent accepts
type AgentConstraints struct {
	// Temporal constraint
	TemporalFocus TemporalFocus `json:"temporal_focus,omitempty"` // "past", "present", "future", or empty for any

	// If true, agent only accepts retrospective (past) queries
	RetrospectiveOnly bool `json:"retrospective_only,omitempty"`

	// If true, agent requires explicit DSL commands (no NL)
	DSLOnly bool `json:"dsl_only,omitempty"`

	// Minimum confidence required to route to this agent
	MinConfidence float64 `json:"min_confidence,omitempty"`
}

// SupportsIntent returns true if the agent supports the given intent
func (c *AgentCapabilities) SupportsIntent(intent Intent) bool {
	if len(c.Intents) == 0 {
		return true // Empty means all intents
	}
	for _, i := range c.Intents {
		if i == intent {
			return true
		}
	}
	return false
}

// SupportsDomain returns true if the agent supports the given domain
func (c *AgentCapabilities) SupportsDomain(domain Domain) bool {
	if len(c.Domains) == 0 {
		return true // Empty means all domains
	}
	for _, d := range c.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// Accepts returns true if the agent accepts the given route result
func (r *AgentRegistration) Accepts(result *RouteResult) bool {
	// Check capabilities
	if !r.Capabilities.SupportsIntent(result.Intent) {
		return false
	}
	if !r.Capabilities.SupportsDomain(result.Domain) {
		return false
	}

	// Check constraints
	if r.Constraints.RetrospectiveOnly && result.TemporalFocus != TemporalPast {
		return false
	}
	if r.Constraints.MinConfidence > 0 && result.Confidence < r.Constraints.MinConfidence {
		return false
	}

	return true
}

// MatchScore returns a score indicating how well this agent matches a route result.
// Higher score = better match. Returns 0 if agent doesn't accept the result.
func (r *AgentRegistration) MatchScore(result *RouteResult) int {
	if !r.Accepts(result) {
		return 0
	}

	score := r.Priority

	// Bonus for explicit intent match
	if len(r.Capabilities.Intents) > 0 {
		score += 10
	}

	// Bonus for explicit domain match
	if len(r.Capabilities.Domains) > 0 {
		score += 10
	}

	// Bonus for temporal match
	if r.Constraints.TemporalFocus != "" && r.Constraints.TemporalFocus == result.TemporalFocus {
		score += 5
	}

	return score
}

// =============================================================================
// Registry Types
// =============================================================================

// AgentRegistry manages registered agents
type AgentRegistry interface {
	// Register adds or updates an agent registration
	Register(registration *AgentRegistration)

	// Unregister removes an agent by ID
	Unregister(id string)

	// Get retrieves an agent by ID
	Get(id string) *AgentRegistration

	// GetByName retrieves an agent by name or alias
	GetByName(name string) *AgentRegistration

	// FindBestMatch finds the best agent for a route result
	FindBestMatch(result *RouteResult) *AgentRegistration

	// GetAll returns all registered agents
	GetAll() []*AgentRegistration
}

// =============================================================================
// Configuration Types
// =============================================================================

// RouterConfig configures the guide router
type RouterConfig struct {
	// LLM configuration
	Model       string  `json:"model"`       // Model for classification
	MaxTokens   int     `json:"max_tokens"`  // Max tokens for classification response
	Temperature float64 `json:"temperature"` // Temperature for classification

	// Confidence thresholds
	ExecuteThreshold float64 `json:"execute_threshold"` // >= this: execute (default: 0.90)
	LogThreshold     float64 `json:"log_threshold"`     // >= this: execute+log (default: 0.75)
	SuggestThreshold float64 `json:"suggest_threshold"` // >= this: suggest (default: 0.50)

	// Few-shot corrections
	MaxCorrections int `json:"max_corrections"` // Max corrections for few-shot prompt

	// DSL configuration
	DSLPrefix string `json:"dsl_prefix"` // Prefix for DSL commands (default: "@")

	// Timeouts
	ClassificationTimeout time.Duration `json:"classification_timeout"`
}

// DefaultRouterConfig returns sensible defaults
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		Model:                 "claude-haiku-4-5-20251001", // Claude Haiku 4.5
		MaxTokens:             512,
		Temperature:           0.0,
		ExecuteThreshold:      0.90,
		LogThreshold:          0.75,
		SuggestThreshold:      0.50,
		MaxCorrections:        50,
		DSLPrefix:             "@",
		ClassificationTimeout: 10 * time.Second,
	}
}
