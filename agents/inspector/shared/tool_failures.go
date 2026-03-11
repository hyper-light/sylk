package shared

import "fmt"

func unavailableToolIssues(tool string) []ValidationIssue {
	return []ValidationIssue{{
		ID:       tool + "_unavailable",
		Severity: Critical,
		Message:  fmt.Sprintf("%s is unavailable in the current execution environment", tool),
		RuleID:   tool + "-unavailable",
	}}
}

func executionFailureIssues(tool string, err error) []ValidationIssue {
	return []ValidationIssue{{
		ID:       tool + "_execution_failed",
		Severity: Critical,
		Message:  fmt.Sprintf("%s execution failed: %v", tool, err),
		RuleID:   tool + "-execution",
	}}
}

func parseFailureIssues(tool string, err error) []ValidationIssue {
	return []ValidationIssue{{
		ID:       tool + "_parse_failed",
		Severity: Critical,
		Message:  fmt.Sprintf("%s output could not be parsed: %v", tool, err),
		RuleID:   tool + "-parse",
	}}
}
