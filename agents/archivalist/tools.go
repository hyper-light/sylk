package archivalist

// ToolName constants for all archivalist tools
const (
	ToolGetBriefing     = "archivalist_get_briefing"
	ToolQueryPatterns   = "archivalist_query_patterns"
	ToolQueryFailures   = "archivalist_query_failures"
	ToolQueryContext    = "archivalist_query_context"
	ToolQueryFileState  = "archivalist_query_file_state"
	ToolRecordPattern   = "archivalist_record_pattern"
	ToolRecordFailure   = "archivalist_record_failure"
	ToolUpdateFileState = "archivalist_update_file_state"
	ToolDeclareIntent   = "archivalist_declare_intent"
	ToolCompleteIntent  = "archivalist_complete_intent"
	ToolGetConflicts    = "archivalist_get_conflicts"
)

// ToolDefinition describes a tool for agents
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// GetBriefingInput is the input for get_briefing
type GetBriefingInput struct {
	Tier string `json:"tier"` // "micro", "standard", "full"
}

// QueryPatternsInput is the input for query_patterns
type QueryPatternsInput struct {
	Category string   `json:"category"`
	Scope    []string `json:"scope,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// QueryFailuresInput is the input for query_failures
type QueryFailuresInput struct {
	ErrorType   string `json:"error_type,omitempty"`
	FilePattern string `json:"file_pattern,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// QueryContextInput is the input for query_context
type QueryContextInput struct {
	Query string `json:"query"`
	Scope string `json:"scope"` // "session", "global", "all"
}

// QueryFileStateInput is the input for query_file_state
type QueryFileStateInput struct {
	Path           string `json:"path"`
	IncludeHistory bool   `json:"include_history,omitempty"`
}

// RecordPatternInput is the input for record_pattern
type RecordPatternInput struct {
	Pattern    string   `json:"pattern"`
	Category   string   `json:"category"`
	Scope      []string `json:"scope,omitempty"`
	Supersedes []string `json:"supersedes,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// RecordFailureInput is the input for record_failure
type RecordFailureInput struct {
	Error      string `json:"error"`
	Context    string `json:"context"`
	Approach   string `json:"approach"`
	Resolution string `json:"resolution"`
	Outcome    string `json:"outcome"` // "success", "partial", "failed"
}

// UpdateFileStateInput is the input for update_file_state
type UpdateFileStateInput struct {
	Path         string `json:"path"`
	Action       string `json:"action"` // "read", "modified", "created", "deleted"
	Summary      string `json:"summary"`
	LinesChanged string `json:"lines_changed,omitempty"`
}

// DeclareIntentInput is the input for declare_intent
type DeclareIntentInput struct {
	Type          string   `json:"type"` // "refactor", "rename", "api_change", "breaking_change"
	Description   string   `json:"description"`
	AffectedPaths []string `json:"affected_paths"`
	AffectedAPIs  []string `json:"affected_apis,omitempty"`
	Priority      string   `json:"priority"` // "low", "medium", "high", "critical"
}

// CompleteIntentInput is the input for complete_intent
type CompleteIntentInput struct {
	IntentID     string   `json:"intent_id"`
	Success      bool     `json:"success"`
	FilesChanged []string `json:"files_changed,omitempty"`
}

// GetConflictsInput is the input for get_conflicts
type GetConflictsInput struct {
	Paths        []string `json:"paths"`
	CheckIntents bool     `json:"check_intents"`
}

// AllToolDefinitions returns all tool definitions for agents
func AllToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		getBriefingDefinition(),
		queryPatternsDefinition(),
		queryFailuresDefinition(),
		queryContextDefinition(),
		queryFileStateDefinition(),
		recordPatternDefinition(),
		recordFailureDefinition(),
		updateFileStateDefinition(),
		declareIntentDefinition(),
		completeIntentDefinition(),
		getConflictsDefinition(),
	}
}

func getBriefingDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolGetBriefing,
		Description: "Get a handoff briefing for continuing work.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tier": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"micro", "standard", "full"},
					"description": "Briefing detail level",
				},
			},
		},
	}
}

func queryPatternsDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolQueryPatterns,
		Description: "Query coding patterns by category.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category": map[string]interface{}{
					"type":        "string",
					"description": "Hierarchical category (e.g., 'error.handling')",
				},
				"scope": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Filter by file paths",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Max results",
				},
			},
			"required": []string{"category"},
		},
	}
}

func queryFailuresDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolQueryFailures,
		Description: "Search failures and their resolutions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"error_type": map[string]interface{}{
					"type":        "string",
					"description": "Filter by error type",
				},
				"file_pattern": map[string]interface{}{
					"type":        "string",
					"description": "Filter by file pattern",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Max results",
				},
			},
		},
	}
}

func queryContextDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolQueryContext,
		Description: "Free-form query for any context using RAG.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Your question in natural language",
				},
				"scope": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"session", "global", "all"},
					"description": "Search scope",
				},
			},
			"required": []string{"query"},
		},
	}
}

func queryFileStateDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolQueryFileState,
		Description: "Get file state across sessions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path or pattern",
				},
				"include_history": map[string]interface{}{
					"type":        "boolean",
					"description": "Include modification history",
				},
			},
			"required": []string{"path"},
		},
	}
}

func recordPatternDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolRecordPattern,
		Description: "Record a new coding pattern.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "The pattern description",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"description": "Hierarchical category",
				},
				"scope": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "File paths this applies to",
				},
				"supersedes": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "IDs of patterns this replaces",
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "Why this supersedes",
				},
			},
			"required": []string{"pattern", "category"},
		},
	}
}

func recordFailureDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolRecordFailure,
		Description: "Report a failure and its resolution.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"error": map[string]interface{}{
					"type":        "string",
					"description": "The error message",
				},
				"context": map[string]interface{}{
					"type":        "string",
					"description": "What was being attempted",
				},
				"approach": map[string]interface{}{
					"type":        "string",
					"description": "What was tried",
				},
				"resolution": map[string]interface{}{
					"type":        "string",
					"description": "What worked instead",
				},
				"outcome": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"success", "partial", "failed"},
					"description": "Resolution outcome",
				},
			},
			"required": []string{"error", "approach", "resolution", "outcome"},
		},
	}
}

func updateFileStateDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolUpdateFileState,
		Description: "Update file state after reading/modifying.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path",
				},
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"read", "modified", "created", "deleted"},
					"description": "Action taken",
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "Summary of change",
				},
				"lines_changed": map[string]interface{}{
					"type":        "string",
					"description": "Lines affected (e.g., '45-89')",
				},
			},
			"required": []string{"path", "action"},
		},
	}
}

func declareIntentDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolDeclareIntent,
		Description: "Declare intent for cross-cutting work.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"refactor", "rename", "api_change", "breaking_change"},
					"description": "Type of change",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "What you're doing",
				},
				"affected_paths": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Paths affected",
				},
				"affected_apis": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "APIs affected",
				},
				"priority": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"low", "medium", "high", "critical"},
					"description": "Priority level",
				},
			},
			"required": []string{"type", "description", "affected_paths"},
		},
	}
}

func completeIntentDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolCompleteIntent,
		Description: "Mark an intent as completed.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"intent_id": map[string]interface{}{
					"type":        "string",
					"description": "Intent ID to complete",
				},
				"success": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether it succeeded",
				},
				"files_changed": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Files that were changed",
				},
			},
			"required": []string{"intent_id", "success"},
		},
	}
}

func getConflictsDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolGetConflicts,
		Description: "Check for conflicts with other sessions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"paths": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Paths to check",
				},
				"check_intents": map[string]interface{}{
					"type":        "boolean",
					"description": "Check for overlapping intents",
				},
			},
			"required": []string{"paths"},
		},
	}
}
