package shared

import (
	"context"
	"encoding/json"
	"fmt"
)

// golangciLintOutput represents the JSON output of golangci-lint.
type golangciLintOutput struct {
	Issues []golangciLintIssue `json:"Issues"`
}

type golangciLintIssue struct {
	FromLinter string          `json:"FromLinter"`
	Text       string          `json:"Text"`
	Severity   string          `json:"Severity"`
	Pos        golangciLintPos `json:"Pos"`
}

type golangciLintPos struct {
	Filename string `json:"Filename"`
	Line     int    `json:"Line"`
	Column   int    `json:"Column"`
}

// RunGolangCILint runs golangci-lint with JSON output and parses the results.
func RunGolangCILint(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	if !runner.Available("golangci-lint") {
		return unavailableToolIssues("golangci-lint")
	}

	args := []string{"run", "--out-format", "json"}
	args = append(args, paths...)

	result, err := runner.Exec(ctx, "golangci-lint", args...)
	if err != nil {
		return executionFailureIssues("golangci-lint", err)
	}

	var output golangciLintOutput
	if err := json.Unmarshal(result.Stdout, &output); err != nil {
		return parseFailureIssues("golangci-lint", err)
	}

	issues := make([]ValidationIssue, 0, len(output.Issues))
	for idx, lint := range output.Issues {
		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("lint_%d", idx),
			Severity: mapGolangCILintSeverity(lint.Severity),
			File:     lint.Pos.Filename,
			Line:     lint.Pos.Line,
			Column:   lint.Pos.Column,
			Message:  lint.Text,
			RuleID:   lint.FromLinter,
		})
	}

	return DeduplicateIssues(issues)
}

func mapGolangCILintSeverity(sev string) Severity {
	switch sev {
	case "error":
		return High
	case "warning":
		return Medium
	default:
		return Medium
	}
}
