package guide

import (
	"context"
	"time"
)

// =============================================================================
// Guide Package Interfaces
// =============================================================================
//
// These interfaces enable dependency injection and mock generation via mockery.
// Use `mockery --name=<interface>` to generate mocks for testing.
//
// Existing interfaces in this package:
// - EventBus (bus.go) - for message passing
// - Subscription (bus.go) - for subscription management
// - ClassifierClient (classification.go) - for LLM API calls

// =============================================================================
// RouterService
// =============================================================================

// RouterService defines the interface for intent-based routing
//
//go:generate mockery --name=RouterService --output=./mocks --outpkg=mocks
type RouterService interface {
	// Route routes a request to the appropriate agent
	Route(ctx context.Context, request *RouteRequest) (*RouteResult, error)

	// AddCorrection adds a correction to improve future classification
	AddCorrection(correction CorrectionRecord)

	// IsDSL checks if input is a DSL command
	IsDSL(input string) bool

	// ParseDSL parses a DSL command without routing
	ParseDSL(input string) (*DSLCommand, error)

	// FormatAsDSL formats a route result as DSL
	FormatAsDSL(result *RouteResult) string
}

// Ensure Router implements RouterService
var _ RouterService = (*Router)(nil)

// =============================================================================
// ClassifierService
// =============================================================================

// ClassifierService defines the interface for LLM-based query classification
//
//go:generate mockery --name=ClassifierService --output=./mocks --outpkg=mocks
type ClassifierService interface {
	// Classify classifies a natural language query
	Classify(ctx context.Context, input string) (*ClassificationResult, error)

	// AddCorrection adds a correction for learning
	AddCorrection(correction CorrectionRecord)
}

// Ensure Classifier implements ClassifierService
var _ ClassifierService = (*Classifier)(nil)

// =============================================================================
// PendingStoreService
// =============================================================================

// PendingStoreService defines the interface for managing pending requests
//
//go:generate mockery --name=PendingStoreService --output=./mocks --outpkg=mocks
type PendingStoreService interface {
	// Add stores a new pending request and returns the correlation ID
	Add(req *RouteRequest, classification *RouteResult, targetAgentID string) string

	// Get retrieves a pending request by correlation ID
	Get(correlationID string) *PendingRequest

	// Remove removes a pending request and returns it
	Remove(correlationID string) *PendingRequest

	// GetBySource returns all pending requests from a source agent
	GetBySource(sourceAgentID string) []*PendingRequest

	// GetByTarget returns all pending requests to a target agent
	GetByTarget(targetAgentID string) []*PendingRequest

	// CleanupExpired removes expired pending requests and returns them
	CleanupExpired() []*PendingRequest

	// Stats returns statistics about pending requests
	Stats() PendingStats

	// Count returns the number of pending requests
	Count() int

	// CountBySource returns pending request count for a source agent
	CountBySource(sourceAgentID string) int

	// CountByTarget returns pending request count for a target agent
	CountByTarget(targetAgentID string) int
}

// Ensure PendingStore implements PendingStoreService
var _ PendingStoreService = (*PendingStore)(nil)

// =============================================================================
// RouteCacheService
// =============================================================================

// RouteCacheService defines the interface for caching classification results
//
//go:generate mockery --name=RouteCacheService --output=./mocks --outpkg=mocks
type RouteCacheService interface {
	// Get retrieves a cached route for the given input
	Get(input string) *CachedRoute

	// Set stores a classification result in the cache
	Set(input string, result *RouteResult)

	// SetFromRoute stores a route directly (for learned routes)
	SetFromRoute(input string, targetAgentID string, intent Intent, domain Domain)

	// Invalidate removes an entry from the cache
	Invalidate(input string)

	// InvalidateForAgent removes all entries targeting a specific agent
	InvalidateForAgent(agentID string)

	// Clear removes all entries from the cache
	Clear()

	// Cleanup removes expired entries
	Cleanup() int

	// Stats returns cache statistics
	Stats() RouteCacheStats

	// GetAll returns all cached routes (for syncing)
	GetAll() []*CachedRoute

	// SetBulk sets multiple routes at once (for syncing)
	SetBulk(routes []*CachedRoute)
}

// Ensure RouteCache implements RouteCacheService
var _ RouteCacheService = (*RouteCache)(nil)

// =============================================================================
// RegistryService
// =============================================================================

// RegistryService defines the interface for agent registration
//
//go:generate mockery --name=RegistryService --output=./mocks --outpkg=mocks
type RegistryService interface {
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

	// RegisterFromRoutingInfo registers an agent from its AgentRoutingInfo
	RegisterFromRoutingInfo(info *AgentRoutingInfo)
}

// Ensure Registry implements RegistryService
var _ RegistryService = (*Registry)(nil)

// =============================================================================
// HealthMonitorService
// =============================================================================

// HealthMonitorService defines the interface for health monitoring
//
//go:generate mockery --name=HealthMonitorService --output=./mocks --outpkg=mocks
type HealthMonitorService interface {
	// Start begins health monitoring
	Start(ctx context.Context)

	// Stop halts health monitoring
	Stop()

	// Register registers an agent for health monitoring
	Register(agentID string)

	// Unregister removes an agent from health monitoring
	Unregister(agentID string)

	// RecordResponse records a response from an agent
	RecordResponse(agentID string, responseTime time.Duration)

	// GetStatus returns the health status of an agent
	GetStatus(agentID string) HealthStatus

	// IsHealthy returns true if an agent is healthy
	IsHealthy(agentID string) bool

	// Stats returns health statistics for all agents
	Stats() HealthMonitorStats
}

// Ensure HealthMonitor implements HealthMonitorService
var _ HealthMonitorService = (*HealthMonitor)(nil)

// =============================================================================
// DeadLetterQueueService
// =============================================================================

// DeadLetterQueueService defines the interface for dead letter queue
//
//go:generate mockery --name=DeadLetterQueueService --output=./mocks --outpkg=mocks
type DeadLetterQueueService interface {
	// Add adds a dead letter to the queue
	Add(letter *DeadLetter)

	// AddFromMessage creates and adds a dead letter from a message
	AddFromMessage(msg *Message, topic string, reason DeadLetterReason, err error, attempts int)

	// Get returns dead letters matching the filter
	Get(filter DeadLetterFilter) []*DeadLetter

	// GetByCorrelationID returns dead letter by correlation ID
	GetByCorrelationID(correlationID string) *DeadLetter

	// MarkRetried marks a dead letter as retried
	MarkRetried(correlationID string) bool

	// Remove removes a dead letter by correlation ID
	Remove(correlationID string) bool

	// Clear removes all dead letters
	Clear()

	// Len returns the number of dead letters
	Len() int

	// Stats returns DLQ statistics
	Stats() DeadLetterStats
}

// Ensure DeadLetterQueue implements DeadLetterQueueService
var _ DeadLetterQueueService = (*DeadLetterQueue)(nil)

// =============================================================================
// CircuitBreakerService
// =============================================================================

// CircuitBreakerService defines the interface for circuit breaker
//
//go:generate mockery --name=CircuitBreakerService --output=./mocks --outpkg=mocks
type CircuitBreakerService interface {
	// Allow checks if a request should be allowed
	Allow() bool

	// RecordSuccess records a successful request
	RecordSuccess()

	// RecordFailure records a failed request
	RecordFailure()

	// ForceOpen forces the circuit open
	ForceOpen()

	// Reset resets the circuit to closed state
	Reset()

	// State returns the current state
	State() CircuitState

	// Stats returns circuit breaker stats
	Stats() CircuitBreakerStats
}

// Ensure CircuitBreaker implements CircuitBreakerService
var _ CircuitBreakerService = (*CircuitBreaker)(nil)

// =============================================================================
// CircuitBreakerRegistryService
// =============================================================================

// CircuitBreakerRegistryService defines the interface for circuit breaker registry
//
//go:generate mockery --name=CircuitBreakerRegistryService --output=./mocks --outpkg=mocks
type CircuitBreakerRegistryService interface {
	// Get returns the circuit breaker for an agent (creates if needed)
	Get(agentID string) *CircuitBreaker

	// Allow checks if a request to an agent should be allowed
	Allow(agentID string) bool

	// RecordSuccess records a successful request to an agent
	RecordSuccess(agentID string)

	// RecordFailure records a failed request to an agent
	RecordFailure(agentID string)

	// Remove removes the circuit breaker for an agent
	Remove(agentID string)

	// Stats returns stats for all circuit breakers
	Stats() map[string]CircuitBreakerStats

	// OpenCircuits returns the IDs of agents with open circuits
	OpenCircuits() []string
}

// Ensure CircuitBreakerRegistry implements CircuitBreakerRegistryService
var _ CircuitBreakerRegistryService = (*CircuitBreakerRegistry)(nil)

// =============================================================================
// ParserService
// =============================================================================

// ParserService defines the interface for DSL parsing
//
//go:generate mockery --name=ParserService --output=./mocks --outpkg=mocks
type ParserService interface {
	// IsDSL checks if input is a DSL command
	IsDSL(input string) bool

	// Parse parses a single DSL command
	Parse(input string) (*DSLCommand, error)

	// ParseMultiple parses multiple DSL commands (semicolon-separated)
	ParseMultiple(input string) ([]*DSLCommand, []error)
}

// Ensure Parser implements ParserService
var _ ParserService = (*Parser)(nil)

// =============================================================================
// RetryPolicyService
// =============================================================================

// RetryPolicyService defines the interface for retry policies
//
//go:generate mockery --name=RetryPolicyService --output=./mocks --outpkg=mocks
type RetryPolicyService interface {
	// ShouldRetry checks if retry should be attempted
	ShouldRetry(attempt int, err error) bool

	// NextDelay returns the delay before next retry
	NextDelay(attempt int) time.Duration

	// MaxAttempts returns the maximum number of attempts
	MaxAttempts() int
}

// =============================================================================
// HeartbeatSenderService
// =============================================================================

// HeartbeatSenderService defines the interface for heartbeat sending
//
//go:generate mockery --name=HeartbeatSenderService --output=./mocks --outpkg=mocks
type HeartbeatSenderService interface {
	// Start begins sending heartbeats
	Start()

	// Stop halts heartbeat sending
	Stop()
}

// Ensure HeartbeatSender implements HeartbeatSenderService
var _ HeartbeatSenderService = (*HeartbeatSender)(nil)
