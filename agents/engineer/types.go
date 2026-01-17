// Package engineer provides types and functionality for the Engineer agent,
// which executes individual tasks and is invisible to users.
package engineer

import (
	"encoding/json"
	"time"
)

// TaskState represents the current state of a task being executed by the Engineer.
type TaskState string

const (
	// TaskStatePending indicates the task is waiting to be executed.
	TaskStatePending TaskState = "pending"
	// TaskStateRunning indicates the task is currently being executed.
	TaskStateRunning TaskState = "running"
	// TaskStateCompleted indicates the task has finished successfully.
	TaskStateCompleted TaskState = "completed"
	// TaskStateFailed indicates the task has failed.
	TaskStateFailed TaskState = "failed"
	// TaskStateBlocked indicates the task is blocked waiting for dependencies or clarification.
	TaskStateBlocked TaskState = "blocked"
	// TaskStateCancelled indicates the task was cancelled before completion.
	TaskStateCancelled TaskState = "cancelled"
)

// ValidTaskStates returns all valid task states.
func ValidTaskStates() []TaskState {
	return []TaskState{
		TaskStatePending,
		TaskStateRunning,
		TaskStateCompleted,
		TaskStateFailed,
		TaskStateBlocked,
		TaskStateCancelled,
	}
}

// TaskResult contains the outcome of a completed task execution.
type TaskResult struct {
	// TaskID is the unique identifier of the completed task.
	TaskID string `json:"task_id"`
	// Success indicates whether the task completed successfully.
	Success bool `json:"success"`
	// FilesChanged lists all files that were created, modified, or deleted.
	FilesChanged []FileChange `json:"files_changed,omitempty"`
	// Output contains the primary output or result of the task.
	Output string `json:"output,omitempty"`
	// Errors contains any error messages encountered during execution.
	Errors []string `json:"errors,omitempty"`
	// Duration is the time taken to complete the task.
	Duration time.Duration `json:"duration"`
	// Metadata contains additional task-specific information.
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ConsultationResult stores the response from consulting with the Architect agent.
type ConsultationResult struct {
	// QueryID is the unique identifier for this consultation.
	QueryID string `json:"query_id"`
	// Question is the original question asked to the Architect.
	Question string `json:"question"`
	// Response is the Architect's answer or guidance.
	Response string `json:"response"`
	// Timestamp is when the consultation occurred.
	Timestamp time.Time `json:"timestamp"`
	// RelevantFiles lists files referenced in the response.
	RelevantFiles []string `json:"relevant_files,omitempty"`
	// SuggestedApproach contains recommended implementation steps.
	SuggestedApproach []string `json:"suggested_approach,omitempty"`
}

// HelpRequest represents a request for clarification from the Engineer.
type HelpRequest struct {
	// RequestID is the unique identifier for this help request.
	RequestID string `json:"request_id"`
	// TaskID is the task that triggered the help request.
	TaskID string `json:"task_id"`
	// Question is the specific clarification being requested.
	Question string `json:"question"`
	// Context provides background information relevant to the question.
	Context string `json:"context,omitempty"`
	// Options lists possible choices if the request is multiple-choice.
	Options []string `json:"options,omitempty"`
	// Blocking indicates whether the task is blocked until this is resolved.
	Blocking bool `json:"blocking"`
	// Timestamp is when the help request was created.
	Timestamp time.Time `json:"timestamp"`
}

// ProgressReport provides a status update on task execution.
type ProgressReport struct {
	// TaskID is the task this report is for.
	TaskID string `json:"task_id"`
	// State is the current state of the task.
	State TaskState `json:"state"`
	// Progress is a percentage (0-100) indicating completion.
	Progress int `json:"progress"`
	// CurrentStep describes what the Engineer is currently doing.
	CurrentStep string `json:"current_step,omitempty"`
	// StepsCompleted is the number of steps finished.
	StepsCompleted int `json:"steps_completed"`
	// TotalSteps is the total number of steps expected.
	TotalSteps int `json:"total_steps"`
	// Timestamp is when this report was generated.
	Timestamp time.Time `json:"timestamp"`
	// ContextUsage is the current context window usage (0.0-1.0).
	ContextUsage float64 `json:"context_usage"`
}

// ApprovedCommandPatterns defines which shell commands the Engineer is allowed to execute.
type ApprovedCommandPatterns struct {
	// Patterns contains regex patterns for allowed commands.
	Patterns []string `json:"patterns"`
	// Blocklist contains patterns that are explicitly forbidden.
	Blocklist []string `json:"blocklist"`
}

// DefaultApprovedPatterns returns the default set of approved command patterns.
func DefaultApprovedPatterns() ApprovedCommandPatterns {
	return ApprovedCommandPatterns{
		Patterns: []string{
			`^go\s+(build|test|run|fmt|vet|mod|generate)`,
			`^git\s+(status|diff|log|show|branch|checkout|add|commit|stash)`,
			`^make\s+\w+`,
			`^npm\s+(install|run|test|build)`,
			`^yarn\s+(install|run|test|build)`,
			`^cargo\s+(build|test|run|check|fmt|clippy)`,
			`^python\s+-m\s+(pytest|unittest|pip)`,
			`^ls\s+`,
			`^cat\s+`,
			`^head\s+`,
			`^tail\s+`,
			`^grep\s+`,
			`^find\s+`,
			`^wc\s+`,
		},
		Blocklist: []string{
			`rm\s+-rf\s+/`,
			`rm\s+-rf\s+\*`,
			`>\s*/dev/sd`,
			`mkfs`,
			`dd\s+if=`,
			`:(){`,
			`chmod\s+-R\s+777`,
			`curl.*\|\s*(ba)?sh`,
			`wget.*\|\s*(ba)?sh`,
		},
	}
}

// EngineerConfig contains configuration options for the Engineer agent.
type EngineerConfig struct {
	// Model is the AI model to use (default: Opus 4.5).
	Model string `json:"model"`
	// MaxConcurrentTasks limits parallel task execution.
	MaxConcurrentTasks int `json:"max_concurrent_tasks"`
	// CommandTimeout is the maximum time for a single command execution.
	CommandTimeout time.Duration `json:"command_timeout"`
	// ApprovedCommands defines allowed shell command patterns.
	ApprovedCommands ApprovedCommandPatterns `json:"approved_commands"`
	// MemoryThreshold defines when to trigger pipeline handoff.
	MemoryThreshold MemoryThreshold `json:"memory_threshold"`
	// WorkingDirectory is the base directory for file operations.
	WorkingDirectory string `json:"working_directory"`
	// EnableFileWrites allows the Engineer to create and modify files.
	EnableFileWrites bool `json:"enable_file_writes"`
	// EnableCommands allows the Engineer to execute shell commands.
	EnableCommands bool `json:"enable_commands"`
}

// FileChange represents a modification to a file during task execution.
type FileChange struct {
	// Path is the file path relative to the working directory.
	Path string `json:"path"`
	// Action is the type of change: "create", "modify", or "delete".
	Action string `json:"action"`
	// OldContent contains the previous file content (empty for create).
	OldContent string `json:"old_content,omitempty"`
	// NewContent contains the new file content (empty for delete).
	NewContent string `json:"new_content,omitempty"`
	// LinesAdded is the count of lines added.
	LinesAdded int `json:"lines_added"`
	// LinesRemoved is the count of lines removed.
	LinesRemoved int `json:"lines_removed"`
}

// FileAction constants for FileChange.Action field.
const (
	FileActionCreate = "create"
	FileActionModify = "modify"
	FileActionDelete = "delete"
)

// CommandExecution records the details of a shell command execution.
type CommandExecution struct {
	// Command is the full command that was executed.
	Command string `json:"command"`
	// ExitCode is the process exit code (0 typically means success).
	ExitCode int `json:"exit_code"`
	// Stdout contains the standard output from the command.
	Stdout string `json:"stdout,omitempty"`
	// Stderr contains the standard error output from the command.
	Stderr string `json:"stderr,omitempty"`
	// Duration is how long the command took to execute.
	Duration time.Duration `json:"duration"`
	// StartTime is when the command started executing.
	StartTime time.Time `json:"start_time"`
	// WorkingDir is the directory the command was executed in.
	WorkingDir string `json:"working_dir"`
}

// MemoryThreshold defines context window usage thresholds for the Engineer.
type MemoryThreshold struct {
	// CheckpointThreshold triggers pipeline handoff when context usage exceeds this value.
	// Default is 0.95 (95%) - Engineer hands off to pipeline instead of local compaction.
	CheckpointThreshold float64 `json:"checkpoint_threshold"`
	// WarningThreshold triggers a warning when context usage exceeds this value.
	WarningThreshold float64 `json:"warning_threshold"`
}

// DefaultMemoryThreshold returns the default memory threshold configuration.
// The Engineer triggers pipeline handoff at 95% context usage rather than
// performing local compaction.
func DefaultMemoryThreshold() MemoryThreshold {
	return MemoryThreshold{
		CheckpointThreshold: 0.95,
		WarningThreshold:    0.85,
	}
}

type SignalType string

const (
	SignalTypeHelp       SignalType = "help"
	SignalTypeCompletion SignalType = "completion"
	SignalTypeFailure    SignalType = "failure"
	SignalTypeProgress   SignalType = "progress"
)

type EngineerIntent string

const (
	IntentComplete EngineerIntent = "complete"
	IntentHelp     EngineerIntent = "help"
)

type Signal struct {
	Type       SignalType  `json:"type"`
	TaskID     string      `json:"task_id"`
	EngineerID string      `json:"engineer_id"`
	SessionID  string      `json:"session_id"`
	Payload    interface{} `json:"payload,omitempty"`
	Timestamp  time.Time   `json:"timestamp"`
}

type ConsultTarget string

const (
	ConsultLibrarian   ConsultTarget = "librarian"
	ConsultArchivalist ConsultTarget = "archivalist"
	ConsultAcademic    ConsultTarget = "academic"
)

type Consultation struct {
	Target    ConsultTarget `json:"target"`
	Query     string        `json:"query"`
	Response  string        `json:"response,omitempty"`
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
}

type AgentStatus string

const (
	AgentStatusIdle     AgentStatus = "idle"
	AgentStatusBusy     AgentStatus = "busy"
	AgentStatusBlocked  AgentStatus = "blocked"
	AgentStatusShutdown AgentStatus = "shutdown"
)

type EngineerState struct {
	ID             string      `json:"id"`
	SessionID      string      `json:"session_id"`
	Status         AgentStatus `json:"status"`
	CurrentTaskID  string      `json:"current_task_id,omitempty"`
	TaskQueue      []string    `json:"task_queue"`
	CompletedCount int         `json:"completed_count"`
	FailedCount    int         `json:"failed_count"`
	TokensUsed     int         `json:"tokens_used"`
	StartedAt      time.Time   `json:"started_at"`
	LastActiveAt   time.Time   `json:"last_active_at"`
}

type AgentRoutingInfo struct {
	AgentID   string           `json:"agent_id"`
	AgentType string           `json:"agent_type"`
	Intents   []EngineerIntent `json:"intents"`
	Keywords  []string         `json:"keywords"`
	Priority  int              `json:"priority"`
	SessionID string           `json:"session_id"`
}

type ToolCall struct {
	ID        string                 `json:"id"`
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
	Result    string                 `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Duration  time.Duration          `json:"duration"`
	Timestamp time.Time              `json:"timestamp"`
}

type EngineerRequest struct {
	ID         string         `json:"id"`
	Intent     EngineerIntent `json:"intent"`
	TaskID     string         `json:"task_id,omitempty"`
	Prompt     string         `json:"prompt,omitempty"`
	Context    interface{}    `json:"context,omitempty"`
	EngineerID string         `json:"engineer_id"`
	SessionID  string         `json:"session_id"`
	Timestamp  time.Time      `json:"timestamp"`
}

type EngineerResponse struct {
	ID        string      `json:"id"`
	RequestID string      `json:"request_id"`
	Success   bool        `json:"success"`
	Result    *TaskResult `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

type CheckpointState struct {
	TaskID     string         `json:"task_id"`
	EngineerID string         `json:"engineer_id"`
	SessionID  string         `json:"session_id"`
	FileStates []FileSnapshot `json:"file_states"`
	Timestamp  time.Time      `json:"timestamp"`
}

type FileSnapshot struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	Exists      bool   `json:"exists"`
	Content     string `json:"content,omitempty"`
}

type FailureRecord struct {
	TaskID       string    `json:"task_id"`
	EngineerID   string    `json:"engineer_id"`
	AttemptCount int       `json:"attempt_count"`
	LastError    string    `json:"last_error"`
	Approach     string    `json:"approach,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}
