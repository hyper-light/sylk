package orchestrator

import (
	"encoding/json"
	"time"
)

// =============================================================================
// Wave 6 (6.130-6.139) - Orchestrator Agent Types
// =============================================================================
//
// The Orchestrator is a read-only observer that coordinates workflows,
// monitors task health, and submits task events to the Archivalist.
// Identity: Claude Haiku 4.5, read-only observer

// =============================================================================
// Configuration Types
// =============================================================================

// Config configures the Orchestrator agent
type Config struct {
	// LLM configuration
	Model           string  `json:"model"`             // Default: claude-haiku-4-5-20251001
	MaxOutputTokens int     `json:"max_output_tokens"` // Default: 1024
	Temperature     float64 `json:"temperature"`       // Default: 0.0

	// Session information
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"` // Default: "orchestrator"

	// Health monitoring configuration
	HealthConfig HealthConfig `json:"health_config"`

	// Update buffer configuration (6.130)
	BufferConfig UpdateBufferConfig `json:"buffer_config"`

	// Summary generation configuration (6.133)
	SummaryConfig SummaryConfig `json:"summary_config"`

	// Archivalist integration
	ArchivalistEnabled bool `json:"archivalist_enabled"` // Enable Archivalist event submission
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Model:              "claude-haiku-4-5-20251001",
		MaxOutputTokens:    1024,
		Temperature:        0.0,
		AgentID:            "orchestrator",
		HealthConfig:       DefaultHealthConfig(),
		BufferConfig:       DefaultUpdateBufferConfig(),
		SummaryConfig:      DefaultSummaryConfig(),
		ArchivalistEnabled: true,
	}
}

// =============================================================================
// State Types
// =============================================================================

// State holds the current orchestrator state
type State struct {
	// Current workflows being monitored
	Workflows map[string]*WorkflowState `json:"workflows"`

	// Active tasks across all workflows
	Tasks map[string]*TaskRecord `json:"tasks"`

	// Update buffer for batching status updates (6.130)
	UpdateBuffer *UpdateBuffer `json:"update_buffer"`

	// Health metrics for monitored agents
	AgentHealth map[string]*AgentHealthMetrics `json:"agent_health"`

	// Session metadata
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`

	// Statistics
	Stats OrchestratorStats `json:"stats"`
}

// NewState creates a new orchestrator state
func NewState(sessionID string) *State {
	return &State{
		Workflows:    make(map[string]*WorkflowState),
		Tasks:        make(map[string]*TaskRecord),
		UpdateBuffer: NewUpdateBuffer(DefaultUpdateBufferConfig()),
		AgentHealth:  make(map[string]*AgentHealthMetrics),
		SessionID:    sessionID,
		StartedAt:    time.Now(),
	}
}

// OrchestratorStats tracks orchestrator statistics
type OrchestratorStats struct {
	TotalWorkflows   int64 `json:"total_workflows"`
	ActiveWorkflows  int   `json:"active_workflows"`
	CompletedTasks   int64 `json:"completed_tasks"`
	FailedTasks      int64 `json:"failed_tasks"`
	EventsSubmitted  int64 `json:"events_submitted"`
	SummariesCreated int64 `json:"summaries_created"`
}

// =============================================================================
// Workflow Types
// =============================================================================

// WorkflowState represents the state of a workflow being monitored
type WorkflowState struct {
	// Identity
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Status
	Status   WorkflowStatus `json:"status"`
	Phase    string         `json:"phase,omitempty"`
	Progress float64        `json:"progress"` // 0.0 to 1.0

	// Tasks in this workflow
	TaskIDs      []string `json:"task_ids"`
	CompletedIDs []string `json:"completed_ids"`
	FailedIDs    []string `json:"failed_ids"`
	PendingIDs   []string `json:"pending_ids"`

	// Timing
	StartedAt   time.Time  `json:"started_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Agent assignments
	LeadAgentID    string   `json:"lead_agent_id,omitempty"`
	ParticipantIDs []string `json:"participant_ids,omitempty"`

	// Context
	SessionID string         `json:"session_id"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// WorkflowStatus represents the status of a workflow
type WorkflowStatus string

const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusPaused    WorkflowStatus = "paused"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// IsTerminal returns true if the status is a terminal state
func (s WorkflowStatus) IsTerminal() bool {
	return s == WorkflowStatusCompleted || s == WorkflowStatusFailed || s == WorkflowStatusCancelled
}

// =============================================================================
// Task Record Types
// =============================================================================

// TaskRecord represents a task being tracked by the orchestrator
type TaskRecord struct {
	// Identity
	ID          string `json:"id"`
	WorkflowID  string `json:"workflow_id,omitempty"`
	ParentID    string `json:"parent_id,omitempty"` // For subtasks
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Status
	Status   TaskStatus `json:"status"`
	Priority int        `json:"priority"` // Higher = more important

	// Assignment
	AssignedAgentID string     `json:"assigned_agent_id,omitempty"`
	AssignedAt      *time.Time `json:"assigned_at,omitempty"`

	// Timing
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Deadline    *time.Time `json:"deadline,omitempty"`

	// Results
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`

	// Retry tracking
	Attempts    int `json:"attempts"`
	MaxAttempts int `json:"max_attempts"`

	// Context
	SessionID string         `json:"session_id"`
	Metadata  map[string]any `json:"metadata,omitempty"`

	// Event submission tracking
	EventSubmitted bool       `json:"event_submitted"`
	SubmittedAt    *time.Time `json:"submitted_at,omitempty"`
}

// TaskStatus represents the status of a task
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
	TaskStatusTimedOut  TaskStatus = "timed_out"
	TaskStatusRetrying  TaskStatus = "retrying"
)

// IsTerminal returns true if the status is a terminal state
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled, TaskStatusTimedOut:
		return true
	default:
		return false
	}
}

// =============================================================================
// Update Buffer Types (6.130)
// =============================================================================

// UpdateBufferConfig configures the update buffer
type UpdateBufferConfig struct {
	// Maximum updates to buffer before flushing
	MaxUpdates int `json:"max_updates"`

	// Maximum time to buffer updates before flushing
	FlushInterval time.Duration `json:"flush_interval"`

	// Enable compression of repeated updates
	EnableCompression bool `json:"enable_compression"`
}

// DefaultUpdateBufferConfig returns sensible defaults
func DefaultUpdateBufferConfig() UpdateBufferConfig {
	return UpdateBufferConfig{
		MaxUpdates:        100,
		FlushInterval:     5 * time.Second,
		EnableCompression: true,
	}
}

// UpdateBuffer batches status updates for efficiency
type UpdateBuffer struct {
	Config  UpdateBufferConfig `json:"config"`
	Updates []*StatusUpdate    `json:"updates"`

	// Compression tracking
	LastUpdateByTask map[string]int `json:"last_update_by_task"` // task_id -> index in Updates
	CompressedCount  int            `json:"compressed_count"`

	// Timing
	CreatedAt   time.Time `json:"created_at"`
	LastFlushAt time.Time `json:"last_flush_at"`
}

// NewUpdateBuffer creates a new update buffer
func NewUpdateBuffer(cfg UpdateBufferConfig) *UpdateBuffer {
	return &UpdateBuffer{
		Config:           cfg,
		Updates:          make([]*StatusUpdate, 0, cfg.MaxUpdates),
		LastUpdateByTask: make(map[string]int),
		CreatedAt:        time.Now(),
		LastFlushAt:      time.Now(),
	}
}

// StatusUpdate represents a single status update
type StatusUpdate struct {
	TaskID     string         `json:"task_id"`
	WorkflowID string         `json:"workflow_id,omitempty"`
	AgentID    string         `json:"agent_id,omitempty"`
	Status     TaskStatus     `json:"status"`
	Message    string         `json:"message,omitempty"`
	Progress   float64        `json:"progress,omitempty"` // 0.0 to 1.0
	Timestamp  time.Time      `json:"timestamp"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Add adds an update to the buffer, compressing if enabled
func (b *UpdateBuffer) Add(update *StatusUpdate) bool {
	if b.Config.EnableCompression {
		// Check for existing update for this task
		if idx, ok := b.LastUpdateByTask[update.TaskID]; ok && idx < len(b.Updates) {
			// Replace existing update (compression)
			b.Updates[idx] = update
			b.CompressedCount++
			return false // Not added, compressed
		}
	}

	// Add new update
	b.LastUpdateByTask[update.TaskID] = len(b.Updates)
	b.Updates = append(b.Updates, update)

	return len(b.Updates) >= b.Config.MaxUpdates // Return true if buffer is full
}

// ShouldFlush returns true if the buffer should be flushed
func (b *UpdateBuffer) ShouldFlush() bool {
	if len(b.Updates) >= b.Config.MaxUpdates {
		return true
	}
	if time.Since(b.LastFlushAt) >= b.Config.FlushInterval && len(b.Updates) > 0 {
		return true
	}
	return false
}

// Flush returns all updates and resets the buffer
func (b *UpdateBuffer) Flush() []*StatusUpdate {
	updates := b.Updates
	b.Updates = make([]*StatusUpdate, 0, b.Config.MaxUpdates)
	b.LastUpdateByTask = make(map[string]int)
	b.CompressedCount = 0
	b.LastFlushAt = time.Now()
	return updates
}

// =============================================================================
// Summary Types (6.133)
// =============================================================================

// SummaryConfig configures summary generation
type SummaryConfig struct {
	// Maximum tokens for generated summaries
	MaxTokens int `json:"max_tokens"`

	// Summary generation interval
	GenerateInterval time.Duration `json:"generate_interval"`

	// Include workflow details in summary
	IncludeWorkflowDetails bool `json:"include_workflow_details"`

	// Include task details in summary
	IncludeTaskDetails bool `json:"include_task_details"`

	// Include health metrics in summary
	IncludeHealthMetrics bool `json:"include_health_metrics"`
}

// DefaultSummaryConfig returns sensible defaults
func DefaultSummaryConfig() SummaryConfig {
	return SummaryConfig{
		MaxTokens:              500,
		GenerateInterval:       1 * time.Minute,
		IncludeWorkflowDetails: true,
		IncludeTaskDetails:     true,
		IncludeHealthMetrics:   true,
	}
}

// OrchestratorSummary is the schema for orchestrator summaries (6.133)
type OrchestratorSummary struct {
	// Identity
	ID          string    `json:"id"`
	GeneratedAt time.Time `json:"generated_at"`
	SessionID   string    `json:"session_id"`

	// Overview
	Overview string `json:"overview"`

	// Workflow summary
	Workflows WorkflowsSummary `json:"workflows"`

	// Task summary
	Tasks TasksSummary `json:"tasks"`

	// Health summary
	Health HealthSummary `json:"health"`

	// Key events since last summary
	KeyEvents []KeyEvent `json:"key_events,omitempty"`

	// Recommendations
	Recommendations []string `json:"recommendations,omitempty"`

	// Token estimate
	TokenEstimate int `json:"token_estimate"`
}

// WorkflowsSummary summarizes workflow states
type WorkflowsSummary struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Paused    int `json:"paused"`

	// Active workflow details
	ActiveWorkflows []WorkflowBrief `json:"active_workflows,omitempty"`
}

// WorkflowBrief is a brief summary of a workflow
type WorkflowBrief struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Status   WorkflowStatus `json:"status"`
	Progress float64        `json:"progress"`
	Phase    string         `json:"phase,omitempty"`
}

// TasksSummary summarizes task states
type TasksSummary struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	TimedOut  int `json:"timed_out"`

	// Recent failures
	RecentFailures []TaskBrief `json:"recent_failures,omitempty"`
}

// TaskBrief is a brief summary of a task
type TaskBrief struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Status  TaskStatus `json:"status"`
	AgentID string     `json:"agent_id,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// HealthSummary summarizes health metrics
type HealthSummary struct {
	OverallStatus   HealthLevel `json:"overall_status"`
	AgentCount      int         `json:"agent_count"`
	HealthyAgents   int         `json:"healthy_agents"`
	DegradedAgents  int         `json:"degraded_agents"`
	UnhealthyAgents int         `json:"unhealthy_agents"`

	// Alerts
	ActiveAlerts []HealthAlert `json:"active_alerts,omitempty"`
}

// KeyEvent represents a significant event for summaries
type KeyEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Severity    string    `json:"severity"` // info, warning, error
}

// =============================================================================
// Health Metrics Types (6.132)
// =============================================================================

// HealthConfig configures health monitoring
type HealthConfig struct {
	// Timeout detection
	TaskTimeout      time.Duration `json:"task_timeout"`
	HeartbeatTimeout time.Duration `json:"heartbeat_timeout"`

	// Heartbeat monitoring
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`

	// Error rate thresholds
	ErrorRateWindow    time.Duration `json:"error_rate_window"`
	ErrorRateThreshold float64       `json:"error_rate_threshold"` // 0.0 to 1.0

	// Transient storm detection
	StormWindow    time.Duration `json:"storm_window"`
	StormThreshold int           `json:"storm_threshold"` // Failures in window
}

// DefaultHealthConfig returns sensible defaults
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		TaskTimeout:        5 * time.Minute,
		HeartbeatTimeout:   30 * time.Second,
		HeartbeatInterval:  10 * time.Second,
		ErrorRateWindow:    5 * time.Minute,
		ErrorRateThreshold: 0.5, // 50% error rate triggers alert
		StormWindow:        1 * time.Minute,
		StormThreshold:     5, // 5 failures in 1 minute
	}
}

// AgentHealthMetrics tracks health metrics for an agent
type AgentHealthMetrics struct {
	AgentID string `json:"agent_id"`

	// Overall health level
	Level HealthLevel `json:"level"`

	// Heartbeat tracking
	LastHeartbeat    time.Time `json:"last_heartbeat"`
	MissedHeartbeats int       `json:"missed_heartbeats"`

	// Response time tracking (rolling window)
	AvgResponseTimeMs float64 `json:"avg_response_time_ms"`
	MaxResponseTimeMs float64 `json:"max_response_time_ms"`

	// Error tracking
	TotalRequests int64   `json:"total_requests"`
	TotalErrors   int64   `json:"total_errors"`
	ErrorRate     float64 `json:"error_rate"`

	// Recent errors (for pattern detection)
	RecentErrors []ErrorRecord `json:"recent_errors,omitempty"`

	// Task tracking
	ActiveTasks    int   `json:"active_tasks"`
	CompletedTasks int64 `json:"completed_tasks"`
	FailedTasks    int64 `json:"failed_tasks"`
	TimedOutTasks  int64 `json:"timed_out_tasks"`

	// Alerts
	ActiveAlerts []HealthAlert `json:"active_alerts,omitempty"`

	// Timestamps
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// HealthLevel represents the health status of an agent
type HealthLevel string

const (
	HealthLevelHealthy   HealthLevel = "healthy"
	HealthLevelDegraded  HealthLevel = "degraded"
	HealthLevelUnhealthy HealthLevel = "unhealthy"
	HealthLevelCritical  HealthLevel = "critical"
	HealthLevelUnknown   HealthLevel = "unknown"
)

// HealthAlert represents an active health alert
type HealthAlert struct {
	ID          string      `json:"id"`
	AgentID     string      `json:"agent_id"`
	Type        AlertType   `json:"type"`
	Level       HealthLevel `json:"level"`
	Message     string      `json:"message"`
	TriggeredAt time.Time   `json:"triggered_at"`
	ResolvedAt  *time.Time  `json:"resolved_at,omitempty"`
}

// AlertType categorizes health alerts
type AlertType string

const (
	AlertTypeTimeout        AlertType = "timeout"
	AlertTypeHeartbeatLost  AlertType = "heartbeat_lost"
	AlertTypeHighErrorRate  AlertType = "high_error_rate"
	AlertTypeTransientStorm AlertType = "transient_storm"
	AlertTypeTaskBacklog    AlertType = "task_backlog"
)

// ErrorRecord tracks a single error occurrence
type ErrorRecord struct {
	Timestamp time.Time `json:"timestamp"`
	TaskID    string    `json:"task_id,omitempty"`
	Error     string    `json:"error"`
	Category  string    `json:"category,omitempty"` // timeout, validation, system, etc.
}

// =============================================================================
// Task Event Types (for Archivalist submission)
// =============================================================================

// TaskEvent represents an event to submit to Archivalist for terminal task states
type TaskEvent struct {
	// Event identity
	ID        string    `json:"id"`
	Type      string    `json:"type"` // task_completed, task_failed, task_timed_out
	Timestamp time.Time `json:"timestamp"`

	// Task details
	TaskID     string     `json:"task_id"`
	TaskName   string     `json:"task_name"`
	WorkflowID string     `json:"workflow_id,omitempty"`
	Status     TaskStatus `json:"status"`

	// Agent details
	AgentID string `json:"agent_id,omitempty"`

	// Result/error details
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`

	// Timing
	Duration    time.Duration `json:"duration,omitempty"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	CompletedAt time.Time     `json:"completed_at"`

	// Context
	SessionID string         `json:"session_id"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ToJSON converts the event to JSON for submission
func (e *TaskEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// =============================================================================
// Failure Pattern Types (for Archivalist queries)
// =============================================================================

// FailurePattern represents a detected failure pattern from Archivalist
type FailurePattern struct {
	// Pattern identity
	ID          string `json:"id"`
	Type        string `json:"type"` // repeated, cascading, periodic, etc.
	Description string `json:"description"`

	// Affected entities
	AgentIDs    []string `json:"agent_ids,omitempty"`
	TaskTypes   []string `json:"task_types,omitempty"`
	WorkflowIDs []string `json:"workflow_ids,omitempty"`

	// Pattern details
	Occurrences int       `json:"occurrences"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`

	// Severity
	Severity string `json:"severity"` // low, medium, high, critical

	// Recommended action
	Recommendation string `json:"recommendation,omitempty"`
}

// FailureQuery specifies parameters for querying failure patterns from Archivalist
type FailureQuery struct {
	AgentIDs    []string   `json:"agent_ids,omitempty"`
	TaskTypes   []string   `json:"task_types,omitempty"`
	WorkflowIDs []string   `json:"workflow_ids,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Limit       int        `json:"limit,omitempty"`
}
