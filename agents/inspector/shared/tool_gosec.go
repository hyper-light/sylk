package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// gosecOutput represents the JSON output of gosec.
type gosecOutput struct {
	Issues []gosecIssue `json:"Issues"`
}

type gosecIssue struct {
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	RuleID     string `json:"rule_id"`
	Details    string `json:"details"`
	File       string `json:"file"`
	Line       string `json:"line"`
	Column     string `json:"column"`
	Code       string `json:"code"`
}

// RunGoSec runs gosec with JSON output and parses results.
func RunGoSec(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	if !runner.Available("gosec") {
		return unavailableToolIssues("gosec")
	}

	args := []string{"-fmt=json", "-quiet"}
	args = append(args, normalizeGoPackageExecutionTargets(runner, paths)...)

	result, err := runner.Exec(ctx, "gosec", args...)
	if err != nil {
		return executionFailureIssues("gosec", err)
	}

	var output gosecOutput
	if err := json.Unmarshal(result.Stdout, &output); err != nil {
		return parseFailureIssues("gosec", err)
	}

	issues := make([]ValidationIssue, 0, len(output.Issues))
	for idx, sec := range output.Issues {
		lineNum, _ := strconv.Atoi(sec.Line)
		colNum, _ := strconv.Atoi(sec.Column)

		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("sec_%d", idx),
			Severity: mapGoSecSeverity(sec.Severity),
			File:     normalizeToolReportedPath(runner, sec.File),
			Line:     lineNum,
			Column:   colNum,
			Message:  sec.Details,
			RuleID:   sec.RuleID,
		})
	}

	return DeduplicateIssues(issues)
}

func mapGoSecSeverity(sev string) Severity {
	switch sev {
	case "HIGH":
		return Critical
	case "MEDIUM":
		return High
	case "LOW":
		return Medium
	default:
		return Medium
	}
}
