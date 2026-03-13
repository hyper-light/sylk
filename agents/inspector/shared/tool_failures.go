package shared

import (
	"fmt"
	"strings"
)

func unavailableToolIssues(tool string) []ValidationIssue {
	return []ValidationIssue{{
		ID:       tool + "_unavailable",
		Severity: Info,
		Message:  fmt.Sprintf("%s is unavailable in the current execution environment", tool),
		RuleID:   tool + "-unavailable",
	}}
}

func executionFailureIssues(tool string, err error) []ValidationIssue {
	return []ValidationIssue{{
		ID:       tool + "_execution_failed",
		Severity: Medium,
		Message:  fmt.Sprintf("%s execution failed: %v", tool, err),
		RuleID:   tool + "-execution",
	}}
}

func parseFailureIssues(tool string, err error) []ValidationIssue {
	return []ValidationIssue{{
		ID:       tool + "_parse_failed",
		Severity: Medium,
		Message:  fmt.Sprintf("%s output could not be parsed: %v", tool, err),
		RuleID:   tool + "-parse",
	}}
}

func informationalToolIssues(tool, message string) []ValidationIssue {
	id := strings.NewReplacer(" ", "_", "/", "_", "-", "_").Replace(strings.TrimSpace(tool))
	if id == "" {
		id = "tool"
	}
	return []ValidationIssue{{
		ID:       id + "_info",
		Severity: Info,
		Message:  strings.TrimSpace(message),
		RuleID:   id + "-info",
	}}
}
