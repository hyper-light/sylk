package shared

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// staticcheckDiagnostic represents a single JSON-lines diagnostic from staticcheck.
type staticcheckDiagnostic struct {
	Code     string              `json:"code"`
	Severity string              `json:"severity"`
	Message  string              `json:"message"`
	Location staticcheckLocation `json:"location"`
}

type staticcheckLocation struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// RunStaticcheck runs staticcheck with JSON output and parses results.
func RunStaticcheck(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	if !runner.Available("staticcheck") {
		return unavailableToolIssues("staticcheck")
	}

	args := []string{"-f", "json"}
	args = append(args, normalizeGoPackageExecutionTargets(runner, paths)...)

	result, err := runner.Exec(ctx, "staticcheck", args...)
	if err != nil {
		return executionFailureIssues("staticcheck", err)
	}

	var issues []ValidationIssue
	scanner := bufio.NewScanner(bytes.NewReader(result.Stdout))
	idx := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var diag staticcheckDiagnostic
		if err := json.Unmarshal(line, &diag); err != nil {
			continue
		}

		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("sc_%d", idx),
			Severity: mapStaticcheckSeverity(diag.Code),
			File:     normalizeToolReportedPath(runner, diag.Location.File),
			Line:     diag.Location.Line,
			Column:   diag.Location.Column,
			Message:  diag.Message,
			RuleID:   diag.Code,
		})
		idx++
	}

	return DeduplicateIssues(issues)
}

func mapStaticcheckSeverity(code string) Severity {
	switch {
	case strings.HasPrefix(code, "SA"):
		return Critical
	case strings.HasPrefix(code, "S1"):
		return Medium
	case strings.HasPrefix(code, "ST"):
		return Low
	default:
		return Medium
	}
}
