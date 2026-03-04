package agentlog

// ContextBudgetPayload records a context budget zone check.
type ContextBudgetPayload struct {
	EstimatedTokens int     `json:"estimated_tokens"`
	AvailableBudget int     `json:"available_budget"`
	Utilization     float64 `json:"utilization"`
	Zone            string  `json:"zone"`
}

// CompactionPayload records a turn group compaction or eviction.
type CompactionPayload struct {
	GroupsCompacted int `json:"groups_compacted"`
	TokensFreed     int `json:"tokens_freed"`
}

// CalibrationPayload records an adaptive calibration update.
type CalibrationPayload struct {
	ActualInputTokens    int     `json:"actual_input_tokens"`
	EstimatedInputTokens int     `json:"estimated_input_tokens"`
	OldRatio             float64 `json:"old_ratio"`
	NewRatio             float64 `json:"new_ratio"`
}

// OutputLimitedPayload records a tool output truncation.
type OutputLimitedPayload struct {
	ToolName     string `json:"tool_name"`
	OriginalLen  int    `json:"original_len"`
	LimitedLen   int    `json:"limited_len"`
	BudgetTokens int    `json:"budget_tokens"`
}
