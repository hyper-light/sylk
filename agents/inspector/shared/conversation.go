package shared

import (
	"fmt"
	"strings"
	"time"
)

// ConversationResult holds the result of a conversation query.
type ConversationResult struct {
	Response string `json:"response"`
	Intent   string `json:"intent"`
	Static   bool   `json:"static"`
}

// ResponseText implements the guide-layer responseTexter interface so the
// conversation history stores the clean response string, not a JSON blob.
func (r *ConversationResult) ResponseText() string {
	if r == nil {
		return ""
	}
	return r.Response
}

// TryStaticReply attempts to answer simple queries without an LLM call.
// Returns nil if no static match is found.
func TryStaticReply(input string, state *InspectorState, issues []ValidationIssue) *ConversationResult {
	lower := strings.ToLower(strings.TrimSpace(input))

	switch {
	case containsAny(lower, "status", "state", "how are you"):
		return &ConversationResult{
			Response: formatStatus(state),
			Intent:   "status",
			Static:   true,
		}
	case containsAny(lower, "issue count", "how many issues", "total issues"):
		return &ConversationResult{
			Response: formatIssueSummary(state, issues),
			Intent:   "recall",
			Static:   true,
		}
	case containsAny(lower, "what do you do", "capabilities", "help"):
		return &ConversationResult{
			Response: inspectorCapabilities(),
			Intent:   "help",
			Static:   true,
		}
	}

	return nil
}

// ConversationFallback produces a formatted state summary when no provider is available.
func ConversationFallback(state *InspectorState) *ConversationResult {
	return &ConversationResult{
		Response: formatStatus(state),
		Intent:   "fallback",
		Static:   true,
	}
}

func containsAny(input string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(input, term) {
			return true
		}
	}
	return false
}

func formatStatus(state *InspectorState) string {
	if state == nil {
		return "Inspector is not initialized."
	}
	return fmt.Sprintf(
		"Inspector %s: mode=%s, issues_found=%d, issues_resolved=%d, loops=%d, last_active=%s",
		state.ID,
		state.Mode,
		state.IssuesFound,
		state.IssuesResolved,
		state.LoopsCompleted,
		state.LastActiveAt.Format(time.RFC3339),
	)
}

func formatIssueSummary(state *InspectorState, issues []ValidationIssue) string {
	if state == nil {
		return "No inspection data available."
	}

	counts := make(map[Severity]int, len(ValidSeverities()))
	for _, issue := range issues {
		counts[issue.Severity]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Total issues: %d\n", len(issues))
	for _, sev := range ValidSeverities() {
		if c := counts[sev]; c > 0 {
			fmt.Fprintf(&b, "  %s: %d\n", sev, c)
		}
	}
	return b.String()
}

func inspectorCapabilities() string {
	return "Inspector agent: code quality validation, lint checks, type checks, " +
		"security scans, complexity analysis, deadlock detection, memory leak analysis, " +
		"and tester-backed validation review. Operates in pipeline (per-task) " +
		"and global (cross-file audit) modes."
}
