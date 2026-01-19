package handoff

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// HO.11.2 ContextCheckHook - AgentContextCheck Hook for Handoff Integration
// =============================================================================
//
// ContextCheckHook implements a hook interface that monitors context utilization
// and triggers handoff recommendations when thresholds are exceeded. It integrates
// with the HandoffController for decision-making.
//
// Key features:
//   - OnContextUpdate(ctx) - evaluates context utilization
//   - Uses HandoffController for decision logic
//   - Triggers handoff recommendation if needed
//   - Non-blocking hook execution
//   - Configurable thresholds and behavior
//   - Metrics collection
//
// Thread Safety:
//   - All operations are protected by appropriate synchronization
//   - Safe for concurrent use from multiple goroutines
//
// Example usage:
//
//	controller := NewHandoffController(gp, profile, config)
//	hook := NewContextCheckHook(controller, hookConfig)
//	hook.Start()
//	defer hook.Stop()
//
//	// Called during context updates
//	recommendation := hook.OnContextUpdate(contextState)
//	if recommendation.ShouldHandoff {
//	    // Trigger handoff
//	}

// =============================================================================
// ContextCheckConfig - Hook Configuration
// =============================================================================

// ContextCheckConfig configures the ContextCheckHook behavior.
type ContextCheckConfig struct {
	// Name is the unique identifier for this hook.
	Name string `json:"name"`

	// Enabled controls whether the hook is active.
	Enabled bool `json:"enabled"`

	// MinCheckInterval is the minimum time between checks for the same agent.
	MinCheckInterval time.Duration `json:"min_check_interval"`

	// ContextThreshold is the utilization level that triggers evaluation.
	ContextThreshold float64 `json:"context_threshold"`

	// UseGPPrediction enables GP-based quality prediction.
	UseGPPrediction bool `json:"use_gp_prediction"`

	// NonBlocking makes hook execution non-blocking.
	NonBlocking bool `json:"non_blocking"`

	// NotificationChannel is the channel size for async notifications.
	NotificationChannel int `json:"notification_channel"`

	// AgentTypes lists the agent types this hook applies to.
	// Empty list means all agent types.
	AgentTypes []string `json:"agent_types,omitempty"`
}

// DefaultContextCheckConfig returns sensible defaults.
func DefaultContextCheckConfig() *ContextCheckConfig {
	return &ContextCheckConfig{
		Name:                "handoff_context_check",
		Enabled:             true,
		MinCheckInterval:    5 * time.Second,
		ContextThreshold:    0.7,
		UseGPPrediction:     true,
		NonBlocking:         true,
		NotificationChannel: 100,
		AgentTypes:          nil, // All agent types
	}
}

// Clone creates a deep copy of the config.
func (c *ContextCheckConfig) Clone() *ContextCheckConfig {
	if c == nil {
		return nil
	}
	cloned := &ContextCheckConfig{
		Name:                c.Name,
		Enabled:             c.Enabled,
		MinCheckInterval:    c.MinCheckInterval,
		ContextThreshold:    c.ContextThreshold,
		UseGPPrediction:     c.UseGPPrediction,
		NonBlocking:         c.NonBlocking,
		NotificationChannel: c.NotificationChannel,
	}
	if c.AgentTypes != nil {
		cloned.AgentTypes = make([]string, len(c.AgentTypes))
		copy(cloned.AgentTypes, c.AgentTypes)
	}
	return cloned
}

// =============================================================================
// HandoffRecommendation - Result of Context Check
// =============================================================================

// HandoffRecommendation represents the result of a context check.
type HandoffRecommendation struct {
	// ShouldHandoff indicates whether a handoff is recommended.
	ShouldHandoff bool `json:"should_handoff"`

	// Urgency indicates how urgently the handoff should be performed.
	// Range: [0, 1] where 1 is most urgent.
	Urgency float64 `json:"urgency"`

	// Reason describes why the handoff is recommended.
	Reason string `json:"reason"`

	// Trigger indicates what triggered the recommendation.
	Trigger HandoffTrigger `json:"trigger"`

	// Decision contains the full handoff decision if available.
	Decision *HandoffDecision `json:"decision,omitempty"`

	// AgentID is the agent this recommendation is for.
	AgentID string `json:"agent_id"`

	// AgentType is the type of the agent.
	AgentType string `json:"agent_type"`

	// Timestamp is when the recommendation was made.
	Timestamp time.Time `json:"timestamp"`

	// Metrics contains evaluation metrics.
	Metrics RecommendationMetrics `json:"metrics"`
}

// RecommendationMetrics contains metrics for the recommendation.
type RecommendationMetrics struct {
	// EvaluationDuration is how long the evaluation took.
	EvaluationDuration time.Duration `json:"evaluation_duration"`

	// ContextUtilization is the context utilization at check time.
	ContextUtilization float64 `json:"context_utilization"`

	// PredictedQuality is the GP-predicted quality.
	PredictedQuality float64 `json:"predicted_quality"`

	// QualityUncertainty is the uncertainty in the quality prediction.
	QualityUncertainty float64 `json:"quality_uncertainty"`
}

// =============================================================================
// ContextCheckHook
// =============================================================================

// ContextCheckHook monitors context utilization and triggers handoff recommendations.
type ContextCheckHook struct {
	mu sync.RWMutex

	// controller is the HandoffController for decision making.
	controller *HandoffController

	// config holds hook configuration.
	config *ContextCheckConfig

	// lastChecks tracks when each agent was last checked.
	lastChecks map[string]time.Time

	// agentTypeSet is a set of agent types this hook applies to.
	agentTypeSet map[string]struct{}

	// notifications is the channel for async recommendations.
	notifications chan *HandoffRecommendation

	// statistics
	stats hookStatsInternal

	// lifecycle
	started atomic.Bool
	closed  atomic.Bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// hookStatsInternal holds internal statistics.
type hookStatsInternal struct {
	checksPerformed     atomic.Int64
	recommendationsGiven atomic.Int64
	checksSkipped       atomic.Int64
	checksThrottled     atomic.Int64
	totalDurationNs     atomic.Int64
	gpEvaluations       atomic.Int64
	thresholdTriggers   atomic.Int64
	qualityTriggers     atomic.Int64
}

// NewContextCheckHook creates a new ContextCheckHook.
func NewContextCheckHook(
	controller *HandoffController,
	config *ContextCheckConfig,
) *ContextCheckHook {
	if config == nil {
		config = DefaultContextCheckConfig()
	}

	// Build agent type set
	agentTypeSet := make(map[string]struct{})
	for _, t := range config.AgentTypes {
		agentTypeSet[t] = struct{}{}
	}

	channelSize := config.NotificationChannel
	if channelSize <= 0 {
		channelSize = 100
	}

	return &ContextCheckHook{
		controller:    controller,
		config:        config.Clone(),
		lastChecks:    make(map[string]time.Time),
		agentTypeSet:  agentTypeSet,
		notifications: make(chan *HandoffRecommendation, channelSize),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// =============================================================================
// Lifecycle Management
// =============================================================================

// Start begins the hook's operations.
func (h *ContextCheckHook) Start() error {
	if h.closed.Load() {
		return ErrAdapterClosed
	}

	if h.started.Swap(true) {
		return nil // Already started
	}

	return nil
}

// Stop gracefully stops the hook.
func (h *ContextCheckHook) Stop() error {
	if h.closed.Swap(true) {
		return ErrAdapterClosed
	}

	if !h.started.Load() {
		close(h.doneCh)
		return nil
	}

	close(h.stopCh)
	close(h.notifications)
	close(h.doneCh)

	return nil
}

// IsRunning returns true if the hook is running.
func (h *ContextCheckHook) IsRunning() bool {
	return h.started.Load() && !h.closed.Load()
}

// IsClosed returns true if the hook is closed.
func (h *ContextCheckHook) IsClosed() bool {
	return h.closed.Load()
}

// =============================================================================
// Hook Interface Methods
// =============================================================================

// Name returns the unique identifier for this hook.
func (h *ContextCheckHook) Name() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Name
}

// IsEnabled returns whether the hook is enabled.
func (h *ContextCheckHook) IsEnabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Enabled
}

// SetEnabled enables or disables the hook.
func (h *ContextCheckHook) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.config.Enabled = enabled
}

// ShouldRun checks if this hook should run for the given agent type.
func (h *ContextCheckHook) ShouldRun(agentType string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// If no agent types specified, run for all
	if len(h.agentTypeSet) == 0 {
		return true
	}

	_, ok := h.agentTypeSet[agentType]
	return ok
}

// =============================================================================
// Context Check Operations
// =============================================================================

// OnContextUpdate evaluates the context state and returns a handoff recommendation.
// This is the main entry point for context-based handoff triggering.
func (h *ContextCheckHook) OnContextUpdate(ctx *ContextState) *HandoffRecommendation {
	startTime := time.Now()

	// Check if enabled
	h.mu.RLock()
	if !h.config.Enabled {
		h.mu.RUnlock()
		h.stats.checksSkipped.Add(1)
		return nil
	}
	config := h.config.Clone()
	h.mu.RUnlock()

	if ctx == nil {
		h.stats.checksSkipped.Add(1)
		return nil
	}

	// Check throttling
	agentID := ctx.GetAgentID()
	if !h.shouldCheckAgent(agentID, config.MinCheckInterval) {
		h.stats.checksThrottled.Add(1)
		return nil
	}

	h.stats.checksPerformed.Add(1)

	// Perform evaluation
	recommendation := h.evaluateContext(ctx, config, startTime)

	// Update last check time
	h.updateLastCheck(agentID)

	// Record duration
	h.stats.totalDurationNs.Add(int64(time.Since(startTime)))

	// Send async notification if configured and recommendation given
	if recommendation != nil && recommendation.ShouldHandoff && config.NonBlocking {
		h.sendNotification(recommendation)
	}

	if recommendation != nil && recommendation.ShouldHandoff {
		h.stats.recommendationsGiven.Add(1)
	}

	return recommendation
}

// OnContextUpdateAsync performs context check asynchronously.
// Returns immediately and sends recommendation to notification channel.
func (h *ContextCheckHook) OnContextUpdateAsync(ctx *ContextState) {
	go func() {
		recommendation := h.OnContextUpdate(ctx)
		if recommendation != nil && recommendation.ShouldHandoff {
			h.sendNotification(recommendation)
		}
	}()
}

// evaluateContext performs the actual context evaluation.
func (h *ContextCheckHook) evaluateContext(
	ctx *ContextState,
	config *ContextCheckConfig,
	startTime time.Time,
) *HandoffRecommendation {
	recommendation := &HandoffRecommendation{
		ShouldHandoff: false,
		AgentID:       ctx.GetAgentID(),
		AgentType:     ctx.GetAgentType(),
		Timestamp:     time.Now(),
		Metrics: RecommendationMetrics{
			ContextUtilization: ctx.Utilization(),
		},
	}

	// Check simple threshold first
	utilization := ctx.Utilization()
	if utilization >= config.ContextThreshold {
		h.stats.thresholdTriggers.Add(1)
		recommendation.ShouldHandoff = true
		recommendation.Urgency = utilization // Higher utilization = higher urgency
		recommendation.Reason = "Context utilization exceeds threshold"
		recommendation.Trigger = TriggerContextFull
		recommendation.Metrics.EvaluationDuration = time.Since(startTime)
		return recommendation
	}

	// Use GP prediction if enabled and controller available
	if config.UseGPPrediction && h.controller != nil {
		h.stats.gpEvaluations.Add(1)

		decision := h.controller.EvaluateHandoff(ctx)
		if decision != nil {
			recommendation.Decision = decision

			if decision.ShouldHandoff {
				recommendation.ShouldHandoff = true
				recommendation.Urgency = decision.Confidence
				recommendation.Reason = decision.Reason
				recommendation.Trigger = decision.Trigger
				recommendation.Metrics.PredictedQuality = decision.Factors.PredictedQuality
				recommendation.Metrics.QualityUncertainty = decision.Factors.QualityUncertainty

				if decision.Trigger == TriggerQualityDegrading {
					h.stats.qualityTriggers.Add(1)
				}
			}
		}
	}

	recommendation.Metrics.EvaluationDuration = time.Since(startTime)
	return recommendation
}

// shouldCheckAgent determines if an agent should be checked based on throttling.
func (h *ContextCheckHook) shouldCheckAgent(agentID string, minInterval time.Duration) bool {
	if agentID == "" || minInterval == 0 {
		return true
	}

	h.mu.RLock()
	lastCheck, exists := h.lastChecks[agentID]
	h.mu.RUnlock()

	if !exists {
		return true
	}

	return time.Since(lastCheck) >= minInterval
}

// updateLastCheck updates the last check time for an agent.
func (h *ContextCheckHook) updateLastCheck(agentID string) {
	if agentID == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastChecks[agentID] = time.Now()
}

// sendNotification sends a recommendation to the notification channel.
func (h *ContextCheckHook) sendNotification(recommendation *HandoffRecommendation) {
	select {
	case h.notifications <- recommendation:
		// Sent successfully
	default:
		// Channel full, skip notification
	}
}

// =============================================================================
// Notification Channel
// =============================================================================

// Notifications returns the channel for receiving handoff recommendations.
func (h *ContextCheckHook) Notifications() <-chan *HandoffRecommendation {
	return h.notifications
}

// =============================================================================
// Configuration Management
// =============================================================================

// GetConfig returns a copy of the current configuration.
func (h *ContextCheckHook) GetConfig() *ContextCheckConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Clone()
}

// SetConfig updates the hook configuration.
func (h *ContextCheckHook) SetConfig(config *ContextCheckConfig) {
	if config == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.config = config.Clone()

	// Update agent type set
	h.agentTypeSet = make(map[string]struct{})
	for _, t := range config.AgentTypes {
		h.agentTypeSet[t] = struct{}{}
	}
}

// SetController updates the HandoffController.
func (h *ContextCheckHook) SetController(controller *HandoffController) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.controller = controller
}

// GetController returns the current HandoffController.
func (h *ContextCheckHook) GetController() *HandoffController {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.controller
}

// =============================================================================
// Agent Management
// =============================================================================

// ResetAgent clears tracking data for a specific agent.
func (h *ContextCheckHook) ResetAgent(agentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.lastChecks, agentID)
}

// ResetAllAgents clears all agent tracking data.
func (h *ContextCheckHook) ResetAllAgents() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastChecks = make(map[string]time.Time)
}

// GetLastCheck returns when an agent was last checked.
func (h *ContextCheckHook) GetLastCheck(agentID string) (time.Time, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	t, ok := h.lastChecks[agentID]
	return t, ok
}

// =============================================================================
// Statistics
// =============================================================================

// ContextCheckHookStats contains statistics about the hook.
type ContextCheckHookStats struct {
	ChecksPerformed      int64         `json:"checks_performed"`
	RecommendationsGiven int64         `json:"recommendations_given"`
	ChecksSkipped        int64         `json:"checks_skipped"`
	ChecksThrottled      int64         `json:"checks_throttled"`
	TotalDuration        time.Duration `json:"total_duration"`
	AverageDuration      time.Duration `json:"average_duration"`
	GPEvaluations        int64         `json:"gp_evaluations"`
	ThresholdTriggers    int64         `json:"threshold_triggers"`
	QualityTriggers      int64         `json:"quality_triggers"`
	TrackedAgents        int           `json:"tracked_agents"`
	IsRunning            bool          `json:"is_running"`
	IsClosed             bool          `json:"is_closed"`
}

// Stats returns statistics about the hook.
func (h *ContextCheckHook) Stats() ContextCheckHookStats {
	h.mu.RLock()
	trackedAgents := len(h.lastChecks)
	h.mu.RUnlock()

	checks := h.stats.checksPerformed.Load()
	totalDuration := time.Duration(h.stats.totalDurationNs.Load())

	averageDuration := time.Duration(0)
	if checks > 0 {
		averageDuration = totalDuration / time.Duration(checks)
	}

	return ContextCheckHookStats{
		ChecksPerformed:      checks,
		RecommendationsGiven: h.stats.recommendationsGiven.Load(),
		ChecksSkipped:        h.stats.checksSkipped.Load(),
		ChecksThrottled:      h.stats.checksThrottled.Load(),
		TotalDuration:        totalDuration,
		AverageDuration:      averageDuration,
		GPEvaluations:        h.stats.gpEvaluations.Load(),
		ThresholdTriggers:    h.stats.thresholdTriggers.Load(),
		QualityTriggers:      h.stats.qualityTriggers.Load(),
		TrackedAgents:        trackedAgents,
		IsRunning:            h.IsRunning(),
		IsClosed:             h.IsClosed(),
	}
}

// =============================================================================
// JSON Serialization
// =============================================================================

// hookJSON is used for JSON marshaling.
type hookJSON struct {
	Config *ContextCheckConfig    `json:"config"`
	Stats  ContextCheckHookStats `json:"stats"`
}

// MarshalJSON implements json.Marshaler.
func (h *ContextCheckHook) MarshalJSON() ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return json.Marshal(hookJSON{
		Config: h.config,
		Stats:  h.Stats(),
	})
}

// =============================================================================
// Extended ContextState for Hook Use
// =============================================================================

// ContextState extension to include agent information.
// Note: These fields are added to the existing ContextState for hook use.

// AgentID field accessor for ContextState.
// Returns the agent ID from metadata or the struct field if available.
func (cs *ContextState) GetAgentID() string {
	// In the base ContextState, we don't have AgentID
	// This is extended by callers who set it via embedding or wrapper
	return ""
}

// AgentType field accessor for ContextState.
// Returns the agent type from metadata or the struct field if available.
func (cs *ContextState) GetAgentType() string {
	// In the base ContextState, we don't have AgentType
	// This is extended by callers who set it via embedding or wrapper
	return ""
}

// ContextStateWithAgent extends ContextState with agent information.
type ContextStateWithAgent struct {
	*ContextState

	// AgentID is the identifier of the agent.
	AgentID string `json:"agent_id"`

	// AgentType is the type of the agent.
	AgentType string `json:"agent_type"`
}

// NewContextStateWithAgent creates a ContextState with agent information.
func NewContextStateWithAgent(
	base *ContextState,
	agentID, agentType string,
) *ContextStateWithAgent {
	if base == nil {
		base = &ContextState{}
	}
	return &ContextStateWithAgent{
		ContextState: base,
		AgentID:      agentID,
		AgentType:    agentType,
	}
}

// ToContextState returns the base ContextState.
func (cs *ContextStateWithAgent) ToContextState() *ContextState {
	return cs.ContextState
}
