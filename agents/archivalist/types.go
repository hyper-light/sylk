package archivalist

import (
	"encoding/json"
	"time"
)

// SourceModel represents the AI model that submitted data
type SourceModel string

const (
	SourceModelGPT52Codex   SourceModel = "gpt-5.2-codex"
	SourceModelClaudeOpus45 SourceModel = "claude-opus-4-5-20251101"
	SourceModelUser         SourceModel = "user"
	SourceModelArchivalist  SourceModel = "archivalist"
)

// RequestType classifies incoming requests
type RequestType string

const (
	RequestTypeStore     RequestType = "store"
	RequestTypeRetrieve  RequestType = "retrieve"
	RequestTypeSummarize RequestType = "summarize"
	RequestTypeUpdate    RequestType = "update"
)

// Category represents the structured categories for chronicle entries
type Category string

const (
	CategoryTaskState   Category = "task_state"
	CategoryCodeStyle   Category = "code_style"
	CategoryCodebaseMap Category = "codebase_map"
	CategoryDecision    Category = "decision"
	CategoryIssue       Category = "issue"
	CategoryInsight     Category = "insight"
	CategoryUserVoice   Category = "user_voice"
	CategoryHypothesis  Category = "hypothesis"
	CategoryOpenThread  Category = "open_thread"
	CategoryTimeline    Category = "timeline"
	CategoryGeneral     Category = "general"
)

// Entry represents a single chronicle entry in the Archivalist
type Entry struct {
	ID             string         `json:"id"`
	Category       Category       `json:"category"`
	Title          string         `json:"title,omitempty"`
	Content        string         `json:"content"`
	Source         SourceModel    `json:"source"`
	SessionID      string         `json:"session_id"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ArchivedAt     *time.Time     `json:"archived_at,omitempty"`
	TokensEstimate int            `json:"tokens_estimate"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RelatedIDs     []string       `json:"related_ids,omitempty"`
}

// Session represents a working session
type Session struct {
	ID           string     `json:"id"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	PrimaryFocus string     `json:"primary_focus,omitempty"`
	EntryCount   int        `json:"entry_count"`
}

// EntryLink represents a relationship between entries
type EntryLink struct {
	FromID       string `json:"from_id"`
	ToID         string `json:"to_id"`
	Relationship string `json:"relationship"` // "blocks", "relates_to", "supersedes", "caused_by"
}

// ArchiveQuery specifies parameters for querying the archive
type ArchiveQuery struct {
	Categories  []Category  `json:"categories,omitempty"`
	Sources     []SourceModel `json:"sources,omitempty"`
	SessionIDs  []string    `json:"session_ids,omitempty"`
	Since       *time.Time  `json:"since,omitempty"`
	Until       *time.Time  `json:"until,omitempty"`
	SearchText  string      `json:"search_text,omitempty"`
	Limit       int         `json:"limit,omitempty"`
	IDs         []string    `json:"ids,omitempty"`
	IncludeArchived bool    `json:"include_archived"`
}

// ChronicleSnapshot represents a full snapshot of current state
type ChronicleSnapshot struct {
	Session        *Session     `json:"session"`
	Tasks          []*Entry     `json:"tasks,omitempty"`
	OpenThreads    []*Entry     `json:"open_threads,omitempty"`
	RecentTimeline []*Entry     `json:"recent_timeline,omitempty"`
	ActiveInsights []*Entry     `json:"active_insights,omitempty"`
	UserVoice      []*Entry     `json:"user_voice,omitempty"`
	Hypotheses     []*Entry     `json:"hypotheses,omitempty"`
	Stats          StorageStats `json:"stats"`
}

// StorageStats contains storage metrics
type StorageStats struct {
	TotalEntries         int                 `json:"total_entries"`
	EntriesByCategory    map[Category]int    `json:"entries_by_category"`
	EntriesBySource      map[SourceModel]int `json:"entries_by_source"`
	HotMemoryTokens      int                 `json:"hot_memory_tokens"`
	ArchivedEntries      int                 `json:"archived_entries"`
	CurrentSessionID     string              `json:"current_session_id"`
}

// SubmissionResult indicates the outcome of a submission
type SubmissionResult struct {
	Success bool   `json:"success"`
	ID      string `json:"id,omitempty"`
	Error   error  `json:"error,omitempty"`
}

// MarshalJSON custom marshaler for SubmissionResult to handle error
func (r SubmissionResult) MarshalJSON() ([]byte, error) {
	type Alias SubmissionResult
	var errStr string
	if r.Error != nil {
		errStr = r.Error.Error()
	}
	return json.Marshal(&struct {
		Alias
		Error string `json:"error,omitempty"`
	}{
		Alias: Alias(r),
		Error: errStr,
	})
}

// ValidSources returns the list of accepted source models for submissions
func ValidSources() []SourceModel {
	return []SourceModel{
		SourceModelGPT52Codex,
		SourceModelClaudeOpus45,
	}
}

// IsValidSource checks if a source model is accepted for submissions
func IsValidSource(source SourceModel) bool {
	for _, valid := range ValidSources() {
		if source == valid {
			return true
		}
	}
	return false
}

// AllCategories returns all valid categories
func AllCategories() []Category {
	return []Category{
		CategoryTaskState,
		CategoryCodeStyle,
		CategoryCodebaseMap,
		CategoryDecision,
		CategoryIssue,
		CategoryInsight,
		CategoryUserVoice,
		CategoryHypothesis,
		CategoryOpenThread,
		CategoryTimeline,
		CategoryGeneral,
	}
}

// EstimateTokens provides a rough token count (approximately 4 chars per token)
func EstimateTokens(content string) int {
	return len(content) / 4
}

// =============================================================================
// Facts - structured extraction from entries
// =============================================================================

// FactDecision represents an extracted decision
type FactDecision struct {
	ID             string    `json:"id"`
	Choice         string    `json:"choice"`
	Rationale      string    `json:"rationale,omitempty"`
	Context        string    `json:"context,omitempty"`
	Alternatives   []string  `json:"alternatives,omitempty"`
	Confidence     float64   `json:"confidence"`
	SourceEntryIDs []string  `json:"source_entry_ids"`
	SessionID      string    `json:"session_id"`
	ExtractedAt    time.Time `json:"extracted_at"`
	SupersededBy   string    `json:"superseded_by,omitempty"`
}

// FactPattern represents an extracted coding pattern
type FactPattern struct {
	ID             string    `json:"id"`
	Category       string    `json:"category"`
	Name           string    `json:"name"`
	Pattern        string    `json:"pattern"`
	Example        string    `json:"example,omitempty"`
	Rationale      string    `json:"rationale,omitempty"`
	UsageCount     int       `json:"usage_count"`
	SourceEntryIDs []string  `json:"source_entry_ids"`
	SessionID      string    `json:"session_id"`
	ExtractedAt    time.Time `json:"extracted_at"`
}

// FactFailure represents an extracted failure/learning
type FactFailure struct {
	ID                string    `json:"id"`
	Approach          string    `json:"approach"`
	Reason            string    `json:"reason"`
	Context           string    `json:"context,omitempty"`
	Resolution        string    `json:"resolution,omitempty"`
	ResolutionEntryID string    `json:"resolution_entry_id,omitempty"`
	SourceEntryIDs    []string  `json:"source_entry_ids"`
	SessionID         string    `json:"session_id"`
	ExtractedAt       time.Time `json:"extracted_at"`
}

// FileChangeType describes the type of file change
type FileChangeType string

const (
	FileChangeTypeCreated  FileChangeType = "created"
	FileChangeTypeModified FileChangeType = "modified"
	FileChangeTypeDeleted  FileChangeType = "deleted"
)

// FactFileChange represents an extracted file change
type FactFileChange struct {
	ID             string         `json:"id"`
	Path           string         `json:"path"`
	ChangeType     FileChangeType `json:"change_type"`
	Description    string         `json:"description,omitempty"`
	LineStart      int            `json:"line_start,omitempty"`
	LineEnd        int            `json:"line_end,omitempty"`
	SourceEntryIDs []string       `json:"source_entry_ids"`
	SessionID      string         `json:"session_id"`
	ExtractedAt    time.Time      `json:"extracted_at"`
}

// =============================================================================
// Summaries - hierarchical compression
// =============================================================================

// SummaryLevel indicates the granularity of a summary
type SummaryLevel string

const (
	SummaryLevelSpan    SummaryLevel = "span"    // ~10-20 entries
	SummaryLevelSession SummaryLevel = "session" // Full session
	SummaryLevelDaily   SummaryLevel = "daily"   // Day's work
	SummaryLevelWeekly  SummaryLevel = "weekly"  // Week's work
	SummaryLevelProject SummaryLevel = "project" // Project-level
)

// SummaryScope indicates what the summary covers
type SummaryScope string

const (
	SummaryScopeGeneral   SummaryScope = "general"   // Mixed content
	SummaryScopeTask      SummaryScope = "task"      // Single task
	SummaryScopeFile      SummaryScope = "file"      // Single file changes
	SummaryScopeDecisions SummaryScope = "decisions" // Decision-focused
	SummaryScopeFailures  SummaryScope = "failures"  // Failure/learning focused
)

// CompactedSummary represents a generated summary at any level
type CompactedSummary struct {
	ID               string       `json:"id"`
	Level            SummaryLevel `json:"level"`
	Scope            SummaryScope `json:"scope"`
	ScopeID          string       `json:"scope_id,omitempty"` // e.g., task ID, file path
	Content          string       `json:"content"`
	KeyPoints        []string     `json:"key_points,omitempty"`
	TokensEstimate   int          `json:"tokens_estimate"`
	SourceEntryIDs   []string     `json:"source_entry_ids"`
	SourceSummaryIDs []string     `json:"source_summary_ids,omitempty"` // For hierarchical summaries
	SessionID        string       `json:"session_id,omitempty"`
	TimeStart        *time.Time   `json:"time_start,omitempty"`
	TimeEnd          *time.Time   `json:"time_end,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
}

// SummaryQuery specifies parameters for querying summaries
type SummaryQuery struct {
	Levels     []SummaryLevel `json:"levels,omitempty"`
	Scopes     []SummaryScope `json:"scopes,omitempty"`
	SessionIDs []string       `json:"session_ids,omitempty"`
	Since      *time.Time     `json:"since,omitempty"`
	Until      *time.Time     `json:"until,omitempty"`
	SearchText string         `json:"search_text,omitempty"`
	Limit      int            `json:"limit,omitempty"`
}

// FactsQuery specifies parameters for querying facts
type FactsQuery struct {
	Types      []string   `json:"types,omitempty"` // "decisions", "patterns", "failures", "file_changes"
	SessionIDs []string   `json:"session_ids,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}
