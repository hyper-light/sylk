package shared

import (
	"context"
	"fmt"
	"strings"
)

// RunGoVet runs go vet and parses the diagnostic output from stderr.
func RunGoVet(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	if !runner.Available("go") {
		return nil
	}

	args := []string{"vet"}
	args = append(args, paths...)

	result, err := runner.Exec(ctx, "go", args...)
	if err != nil {
		return nil
	}

	// go vet outputs diagnostics to stderr
	lines := strings.Split(string(result.Stderr), "\n")
	issues := make([]ValidationIssue, 0, len(lines))

	for idx, line := range lines {
		file, lineNum, col, msg, ok := ParseGoToolLine(line)
		if !ok {
			continue
		}
		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("vet_%d", idx),
			Severity: Critical,
			File:     file,
			Line:     lineNum,
			Column:   col,
			Message:  msg,
			RuleID:   "go-vet",
		})
	}

	return DeduplicateIssues(issues)
}
