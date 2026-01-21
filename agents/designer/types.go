// Package designer provides types and functionality for the Designer agent,
// which handles UI/UX design tasks with a focus on accessibility, performance,
// and visual quality.
package designer

import (
	"encoding/json"
	"time"
)

// DesignTaskState represents the current state of a design task being executed.
type DesignTaskState string

const (
	// DesignTaskStatePending indicates the task is waiting to be executed.
	DesignTaskStatePending DesignTaskState = "pending"
	// DesignTaskStateRunning indicates the task is currently being executed.
	DesignTaskStateRunning DesignTaskState = "running"
	// DesignTaskStateCompleted indicates the task has finished successfully.
	DesignTaskStateCompleted DesignTaskState = "completed"
	// DesignTaskStateFailed indicates the task has failed.
	DesignTaskStateFailed DesignTaskState = "failed"
	// DesignTaskStateBlocked indicates the task is blocked waiting for dependencies or clarification.
	DesignTaskStateBlocked DesignTaskState = "blocked"
	// DesignTaskStateCancelled indicates the task was cancelled before completion.
	DesignTaskStateCancelled DesignTaskState = "cancelled"
)

// ValidDesignTaskStates returns all valid design task states.
func ValidDesignTaskStates() []DesignTaskState {
	return []DesignTaskState{
		DesignTaskStatePending,
		DesignTaskStateRunning,
		DesignTaskStateCompleted,
		DesignTaskStateFailed,
		DesignTaskStateBlocked,
		DesignTaskStateCancelled,
	}
}

// DesignTask represents a UI/UX design task to be executed.
type DesignTask struct {
	// TaskID is the unique identifier for this task.
	TaskID string `json:"task_id"`
	// Description is the human-readable description of the task.
	Description string `json:"description"`
	// Type is the type of design task (component, layout, style, a11y).
	Type DesignTaskType `json:"type"`
	// State is the current state of the task.
	State DesignTaskState `json:"state"`
	// ComponentSpec is the specification for component tasks.
	ComponentSpec *ComponentSpec `json:"component_spec,omitempty"`
	// LayoutSpec is the specification for layout tasks.
	LayoutSpec *LayoutSpec `json:"layout_spec,omitempty"`
	// StyleSpec is the specification for style tasks.
	StyleSpec *StyleSpec `json:"style_spec,omitempty"`
	// CreatedAt is when the task was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the task was last updated.
	UpdatedAt time.Time `json:"updated_at"`
}

// DesignTaskType indicates what kind of design task this is.
type DesignTaskType string

const (
	DesignTaskTypeComponent DesignTaskType = "component"
	DesignTaskTypeLayout    DesignTaskType = "layout"
	DesignTaskTypeStyle     DesignTaskType = "style"
	DesignTaskTypeA11y      DesignTaskType = "a11y"
	DesignTaskTypeRefactor  DesignTaskType = "refactor"
)

// DesignResult contains the outcome of a completed design task.
type DesignResult struct {
	// TaskID is the unique identifier of the completed task.
	TaskID string `json:"task_id"`
	// Success indicates whether the task completed successfully.
	Success bool `json:"success"`
	// FilesChanged lists all files that were created, modified, or deleted.
	FilesChanged []FileChange `json:"files_changed,omitempty"`
	// ComponentsCreated lists components that were created.
	ComponentsCreated []string `json:"components_created,omitempty"`
	// ComponentsModified lists components that were modified.
	ComponentsModified []string `json:"components_modified,omitempty"`
	// A11yIssuesFixed is the count of accessibility issues fixed.
	A11yIssuesFixed int `json:"a11y_issues_fixed,omitempty"`
	// TokensValidated indicates design tokens were validated.
	TokensValidated bool `json:"tokens_validated"`
	// A11yAuditPassed indicates the accessibility audit passed.
	A11yAuditPassed bool `json:"a11y_audit_passed"`
	// Output contains the primary output or result of the task.
	Output string `json:"output,omitempty"`
	// Errors contains any error messages encountered during execution.
	Errors []string `json:"errors,omitempty"`
	// Duration is the time taken to complete the task.
	Duration time.Duration `json:"duration"`
	// Metadata contains additional task-specific information.
	Metadata json.RawMessage `json:"metadata,omitempty"`
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

// =============================================================================
// Component Specifications
// =============================================================================

// ComponentSpec defines the specification for a UI component.
type ComponentSpec struct {
	// Name is the component name.
	Name string `json:"name"`
	// Description is a brief description of the component's purpose.
	Description string `json:"description,omitempty"`
	// Props defines the component's props/properties.
	Props []ComponentProp `json:"props,omitempty"`
	// Variants defines visual variants of the component.
	Variants []ComponentVariant `json:"variants,omitempty"`
	// States defines interactive states (hover, focus, active, disabled).
	States []ComponentState `json:"states,omitempty"`
	// DesignTokens lists design tokens used by this component.
	DesignTokens []string `json:"design_tokens,omitempty"`
	// A11yRequirements lists accessibility requirements for this component.
	A11yRequirements []A11yRequirement `json:"a11y_requirements,omitempty"`
	// ExistingPath is the path to an existing component (for modifications).
	ExistingPath string `json:"existing_path,omitempty"`
}

// ComponentProp defines a property of a component.
type ComponentProp struct {
	// Name is the prop name.
	Name string `json:"name"`
	// Type is the prop type (string, number, boolean, etc.).
	Type string `json:"type"`
	// Description describes the prop's purpose.
	Description string `json:"description,omitempty"`
	// Required indicates if the prop is required.
	Required bool `json:"required"`
	// DefaultValue is the default value if not provided.
	DefaultValue string `json:"default_value,omitempty"`
}

// ComponentVariant defines a visual variant of a component.
type ComponentVariant struct {
	// Name is the variant name (e.g., "primary", "secondary", "danger").
	Name string `json:"name"`
	// Description describes when to use this variant.
	Description string `json:"description,omitempty"`
	// Tokens maps design token categories to specific tokens for this variant.
	Tokens map[string]string `json:"tokens,omitempty"`
}

// ComponentState defines an interactive state of a component.
type ComponentState struct {
	// Name is the state name (e.g., "hover", "focus", "active", "disabled").
	Name string `json:"name"`
	// Description describes the state's visual appearance.
	Description string `json:"description,omitempty"`
	// Tokens maps design token categories to specific tokens for this state.
	Tokens map[string]string `json:"tokens,omitempty"`
}

// A11yRequirement defines an accessibility requirement.
type A11yRequirement struct {
	// Type is the WCAG criterion or requirement type.
	Type string `json:"type"`
	// Level is the WCAG level (A, AA, AAA).
	Level string `json:"level"`
	// Description describes the requirement.
	Description string `json:"description"`
	// Implementation notes for meeting the requirement.
	Implementation string `json:"implementation,omitempty"`
}

// =============================================================================
// Layout Specifications
// =============================================================================

// LayoutSpec defines the specification for a layout.
type LayoutSpec struct {
	// Name is the layout name.
	Name string `json:"name"`
	// Description is a brief description of the layout's purpose.
	Description string `json:"description,omitempty"`
	// Type is the layout type (grid, flex, stack, etc.).
	Type LayoutType `json:"type"`
	// Regions defines the named regions in the layout.
	Regions []LayoutRegion `json:"regions,omitempty"`
	// Breakpoints defines responsive breakpoints.
	Breakpoints []Breakpoint `json:"breakpoints,omitempty"`
	// DesignTokens lists design tokens used by this layout.
	DesignTokens []string `json:"design_tokens,omitempty"`
}

// LayoutType indicates the type of layout system.
type LayoutType string

const (
	LayoutTypeGrid  LayoutType = "grid"
	LayoutTypeFlex  LayoutType = "flex"
	LayoutTypeStack LayoutType = "stack"
)

// LayoutRegion defines a named region in a layout.
type LayoutRegion struct {
	// Name is the region name (e.g., "header", "sidebar", "main", "footer").
	Name string `json:"name"`
	// GridArea is the CSS grid-area value.
	GridArea string `json:"grid_area,omitempty"`
	// FlexBasis is the CSS flex-basis value.
	FlexBasis string `json:"flex_basis,omitempty"`
}

// Breakpoint defines a responsive breakpoint.
type Breakpoint struct {
	// Name is the breakpoint name (e.g., "mobile", "tablet", "desktop").
	Name string `json:"name"`
	// MinWidth is the minimum viewport width in pixels.
	MinWidth int `json:"min_width"`
	// MaxWidth is the maximum viewport width in pixels (0 = no max).
	MaxWidth int `json:"max_width,omitempty"`
	// LayoutChanges describes layout changes at this breakpoint.
	LayoutChanges string `json:"layout_changes,omitempty"`
}

// =============================================================================
// Style Specifications
// =============================================================================

// StyleSpec defines the specification for styles.
type StyleSpec struct {
	// TargetPath is the path to the file to style.
	TargetPath string `json:"target_path"`
	// Description describes the styling changes.
	Description string `json:"description,omitempty"`
	// DesignTokens lists design tokens to apply.
	DesignTokens []DesignTokenUsage `json:"design_tokens,omitempty"`
	// Transitions defines transition/animation specifications.
	Transitions []TransitionSpec `json:"transitions,omitempty"`
	// Typography defines typography specifications.
	Typography *TypographySpec `json:"typography,omitempty"`
}

// DesignTokenUsage represents usage of a design token.
type DesignTokenUsage struct {
	// Token is the design token name.
	Token string `json:"token"`
	// Property is the CSS property to apply the token to.
	Property string `json:"property"`
	// Selector is the CSS selector to apply the token to.
	Selector string `json:"selector,omitempty"`
}

// TransitionSpec defines a transition or animation.
type TransitionSpec struct {
	// Property is the CSS property to animate.
	Property string `json:"property"`
	// Duration is the animation duration.
	Duration string `json:"duration"`
	// TimingFunction is the CSS timing function.
	TimingFunction string `json:"timing_function,omitempty"`
	// Delay is the animation delay.
	Delay string `json:"delay,omitempty"`
}

// TypographySpec defines typography specifications.
type TypographySpec struct {
	// FontFamily is the font family token or value.
	FontFamily string `json:"font_family,omitempty"`
	// FontSize is the font size token or value.
	FontSize string `json:"font_size,omitempty"`
	// FontWeight is the font weight token or value.
	FontWeight string `json:"font_weight,omitempty"`
	// LineHeight is the line height token or value.
	LineHeight string `json:"line_height,omitempty"`
	// LetterSpacing is the letter spacing token or value.
	LetterSpacing string `json:"letter_spacing,omitempty"`
}

// =============================================================================
// Accessibility Types
// =============================================================================

// AccessibilityCheck represents an accessibility audit result.
type AccessibilityCheck struct {
	// CheckID is the unique identifier for this check.
	CheckID string `json:"check_id"`
	// Type is the type of check (color-contrast, keyboard-nav, screen-reader, etc.).
	Type A11yCheckType `json:"type"`
	// Passed indicates whether the check passed.
	Passed bool `json:"passed"`
	// Level is the WCAG level (A, AA, AAA).
	Level string `json:"level"`
	// Element is the element or component checked.
	Element string `json:"element,omitempty"`
	// Issue describes the accessibility issue found (if not passed).
	Issue string `json:"issue,omitempty"`
	// Suggestion provides a fix suggestion.
	Suggestion string `json:"suggestion,omitempty"`
	// Impact is the severity of the issue (critical, serious, moderate, minor).
	Impact A11yImpact `json:"impact,omitempty"`
}

// A11yCheckType indicates the type of accessibility check.
type A11yCheckType string

const (
	A11yCheckTypeColorContrast   A11yCheckType = "color-contrast"
	A11yCheckTypeKeyboardNav     A11yCheckType = "keyboard-nav"
	A11yCheckTypeScreenReader    A11yCheckType = "screen-reader"
	A11yCheckTypeFocusIndicator  A11yCheckType = "focus-indicator"
	A11yCheckTypeARIA            A11yCheckType = "aria"
	A11yCheckTypeSemanticHTML    A11yCheckType = "semantic-html"
	A11yCheckTypeMotionReduction A11yCheckType = "motion-reduction"
	A11yCheckTypeTextSpacing     A11yCheckType = "text-spacing"
	A11yCheckTypeTargetSize      A11yCheckType = "target-size"
)

// A11yImpact indicates the severity of an accessibility issue.
type A11yImpact string

const (
	A11yImpactCritical A11yImpact = "critical"
	A11yImpactSerious  A11yImpact = "serious"
	A11yImpactModerate A11yImpact = "moderate"
	A11yImpactMinor    A11yImpact = "minor"
)

// A11yAuditResult represents the result of a full accessibility audit.
type A11yAuditResult struct {
	// AuditID is the unique identifier for this audit.
	AuditID string `json:"audit_id"`
	// Passed indicates whether the overall audit passed.
	Passed bool `json:"passed"`
	// TotalChecks is the total number of checks performed.
	TotalChecks int `json:"total_checks"`
	// PassedChecks is the number of checks that passed.
	PassedChecks int `json:"passed_checks"`
	// FailedChecks is the number of checks that failed.
	FailedChecks int `json:"failed_checks"`
	// Checks contains the individual check results.
	Checks []AccessibilityCheck `json:"checks,omitempty"`
	// WCAGLevel is the WCAG conformance level achieved (A, AA, or AAA).
	WCAGLevel string `json:"wcag_level"`
	// Timestamp is when the audit was performed.
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// Design Token Types
// =============================================================================

// DesignTokenValidation represents the result of design token validation.
type DesignTokenValidation struct {
	// ValidationID is the unique identifier for this validation.
	ValidationID string `json:"validation_id"`
	// Valid indicates whether all tokens are valid.
	Valid bool `json:"valid"`
	// TokensChecked is the total number of tokens checked.
	TokensChecked int `json:"tokens_checked"`
	// InvalidTokens lists tokens that failed validation.
	InvalidTokens []InvalidToken `json:"invalid_tokens,omitempty"`
	// DeprecatedTokens lists tokens that are deprecated.
	DeprecatedTokens []DeprecatedToken `json:"deprecated_tokens,omitempty"`
	// SuggestedTokens suggests tokens for unrecognized values.
	SuggestedTokens []TokenSuggestion `json:"suggested_tokens,omitempty"`
	// Timestamp is when the validation was performed.
	Timestamp time.Time `json:"timestamp"`
}

// InvalidToken represents an invalid design token usage.
type InvalidToken struct {
	// Token is the token name.
	Token string `json:"token"`
	// Location is where the token is used.
	Location string `json:"location"`
	// Reason explains why the token is invalid.
	Reason string `json:"reason"`
}

// DeprecatedToken represents a deprecated design token.
type DeprecatedToken struct {
	// Token is the deprecated token name.
	Token string `json:"token"`
	// Location is where the token is used.
	Location string `json:"location"`
	// Replacement is the recommended replacement token.
	Replacement string `json:"replacement"`
}

// TokenSuggestion suggests a design token for a hard-coded value.
type TokenSuggestion struct {
	// Value is the hard-coded value found.
	Value string `json:"value"`
	// Location is where the value is used.
	Location string `json:"location"`
	// SuggestedToken is the recommended design token.
	SuggestedToken string `json:"suggested_token"`
	// Confidence is the confidence level of the suggestion (0.0-1.0).
	Confidence float64 `json:"confidence"`
}

// =============================================================================
// Agent State Types
// =============================================================================

// DesignerConfig contains configuration options for the Designer agent.
type DesignerConfig struct {
	// Model is the AI model to use (default: Claude Opus 4.5).
	Model string `json:"model"`
	// MaxConcurrentTasks limits parallel task execution.
	MaxConcurrentTasks int `json:"max_concurrent_tasks"`
	// WorkingDirectory is the base directory for file operations.
	WorkingDirectory string `json:"working_directory"`
	// EnableFileWrites allows the Designer to create and modify files.
	EnableFileWrites bool `json:"enable_file_writes"`
	// DesignSystemPath is the path to the design system/token definitions.
	DesignSystemPath string `json:"design_system_path,omitempty"`
	// A11yLevel is the target WCAG conformance level (A, AA, AAA).
	A11yLevel string `json:"a11y_level"`
	// MemoryThreshold defines when to trigger pipeline handoff.
	MemoryThreshold MemoryThreshold `json:"memory_threshold"`
}

// MemoryThreshold defines context window usage thresholds for the Designer.
type MemoryThreshold struct {
	// CheckpointThreshold triggers pipeline handoff when context usage exceeds this value.
	CheckpointThreshold float64 `json:"checkpoint_threshold"`
	// WarningThreshold triggers a warning when context usage exceeds this value.
	WarningThreshold float64 `json:"warning_threshold"`
}

// DefaultMemoryThreshold returns the default memory threshold configuration.
func DefaultMemoryThreshold() MemoryThreshold {
	return MemoryThreshold{
		CheckpointThreshold: 0.95,
		WarningThreshold:    0.85,
	}
}

// DesignerState represents the current state of the Designer agent.
type DesignerState struct {
	// ID is the unique identifier for this designer instance.
	ID string `json:"id"`
	// SessionID is the session this designer belongs to.
	SessionID string `json:"session_id"`
	// Status is the current status of the designer.
	Status AgentStatus `json:"status"`
	// CurrentTaskID is the ID of the task currently being worked on.
	CurrentTaskID string `json:"current_task_id,omitempty"`
	// TaskQueue contains IDs of tasks waiting to be processed.
	TaskQueue []string `json:"task_queue"`
	// CompletedCount is the number of tasks completed.
	CompletedCount int `json:"completed_count"`
	// FailedCount is the number of tasks that failed.
	FailedCount int `json:"failed_count"`
	// TokensUsed is the total tokens used.
	TokensUsed int `json:"tokens_used"`
	// StartedAt is when this designer instance started.
	StartedAt time.Time `json:"started_at"`
	// LastActiveAt is when this designer was last active.
	LastActiveAt time.Time `json:"last_active_at"`
}

// AgentStatus represents the status of the Designer agent.
type AgentStatus string

const (
	AgentStatusIdle     AgentStatus = "idle"
	AgentStatusBusy     AgentStatus = "busy"
	AgentStatusBlocked  AgentStatus = "blocked"
	AgentStatusShutdown AgentStatus = "shutdown"
)

// =============================================================================
// Request/Response Types
// =============================================================================

// DesignerIntent represents the intended action for a design request.
type DesignerIntent string

const (
	IntentDesignComponent DesignerIntent = "design_component"
	IntentDesignLayout    DesignerIntent = "design_layout"
	IntentDesignStyle     DesignerIntent = "design_style"
	IntentA11yAudit       DesignerIntent = "a11y_audit"
	IntentA11yFix         DesignerIntent = "a11y_fix"
	IntentTokenValidate   DesignerIntent = "token_validate"
	IntentRefactor        DesignerIntent = "refactor"
)

// DesignerRequest represents a request to the Designer agent.
type DesignerRequest struct {
	// ID is the unique identifier for this request.
	ID string `json:"id"`
	// Intent is the intended action.
	Intent DesignerIntent `json:"intent"`
	// TaskID is the task this request is for.
	TaskID string `json:"task_id,omitempty"`
	// Prompt is the design task description.
	Prompt string `json:"prompt,omitempty"`
	// Context contains additional context for the request.
	Context interface{} `json:"context,omitempty"`
	// DesignerID is the ID of the designer processing this request.
	DesignerID string `json:"designer_id"`
	// SessionID is the session this request belongs to.
	SessionID string `json:"session_id"`
	// Timestamp is when this request was created.
	Timestamp time.Time `json:"timestamp"`
}

// DesignerResponse represents a response from the Designer agent.
type DesignerResponse struct {
	// ID is the unique identifier for this response.
	ID string `json:"id"`
	// RequestID is the ID of the request this responds to.
	RequestID string `json:"request_id"`
	// Success indicates whether the request was successful.
	Success bool `json:"success"`
	// Result contains the design result (if successful).
	Result *DesignResult `json:"result,omitempty"`
	// Error contains the error message (if failed).
	Error string `json:"error,omitempty"`
	// Timestamp is when this response was created.
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// Consultation Types
// =============================================================================

// ConsultTarget represents which agent to consult.
type ConsultTarget string

const (
	ConsultLibrarian   ConsultTarget = "librarian"
	ConsultArchivalist ConsultTarget = "archivalist"
	ConsultAcademic    ConsultTarget = "academic"
)

// Consultation records a consultation with another agent.
type Consultation struct {
	// Target is the agent that was consulted.
	Target ConsultTarget `json:"target"`
	// Query is the question asked.
	Query string `json:"query"`
	// Response is the response received.
	Response string `json:"response,omitempty"`
	// Duration is how long the consultation took.
	Duration time.Duration `json:"duration"`
	// Timestamp is when the consultation occurred.
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// Failure Tracking
// =============================================================================

// FailureRecord tracks a failed design task attempt.
type FailureRecord struct {
	// TaskID is the task that failed.
	TaskID string `json:"task_id"`
	// DesignerID is the designer that attempted the task.
	DesignerID string `json:"designer_id"`
	// AttemptCount is the number of attempts made.
	AttemptCount int `json:"attempt_count"`
	// LastError is the most recent error message.
	LastError string `json:"last_error"`
	// Approach is the approach that was tried.
	Approach string `json:"approach,omitempty"`
	// Timestamp is when this failure was recorded.
	Timestamp time.Time `json:"timestamp"`
}
