package shared

import (
	"context"
	"fmt"
	"strings"
)

// RunMemoryAnalyzer runs go build with escape analysis flags and parses output.
func RunMemoryAnalyzer(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	if !runner.Available("go") {
		return unavailableToolIssues("go")
	}

	args := []string{"build", "-gcflags=-m -m"}
	args = append(args, paths...)

	result, err := runner.Exec(ctx, "go", args...)
	if err != nil {
		return executionFailureIssues("escape-analysis", err)
	}

	return parseEscapeAnalysis(string(result.Stderr))
}

func parseEscapeAnalysis(stderr string) []ValidationIssue {
	lines := strings.Split(stderr, "\n")
	var issues []ValidationIssue
	idx := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		file, lineNum, col, msg, ok := ParseGoToolLine(line)
		if !ok {
			continue
		}

		// Only report concerning escape patterns
		sev, relevant := classifyEscapeMessage(msg)
		if !relevant {
			continue
		}

		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("mem_%d", idx),
			Severity: sev,
			File:     file,
			Line:     lineNum,
			Column:   col,
			Message:  msg,
			RuleID:   "escape-analysis",
		})
		idx++
	}

	return DeduplicateIssues(issues)
}

func classifyEscapeMessage(msg string) (Severity, bool) {
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "leaking param"):
		return High, true
	case strings.Contains(lower, "moved to heap"):
		// Only flag large allocations or loop-variable escapes
		if strings.Contains(lower, "loop") || strings.Contains(lower, "slice") {
			return Medium, true
		}
		return Info, false
	case strings.Contains(lower, "escapes to heap"):
		return Medium, true
	default:
		return Info, false
	}
}
