package shared

import (
	"context"
	"fmt"
	"strings"
)

// RunGoFmt runs gofmt -l and goimports -l to find unformatted files.
func RunGoFmt(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	var issues []ValidationIssue
	idx := 0

	// gofmt check
	if runner.Available("gofmt") {
		args := append([]string{"-l"}, paths...)
		result, err := runner.Exec(ctx, "gofmt", args...)
		if err == nil {
			for _, file := range parseFileList(result.Stdout) {
				issues = append(issues, ValidationIssue{
					ID:       fmt.Sprintf("fmt_%d", idx),
					Severity: Medium,
					File:     file,
					Message:  "file not formatted with gofmt",
					RuleID:   "gofmt",
				})
				idx++
			}
		}
	}

	// goimports check
	if runner.Available("goimports") {
		args := append([]string{"-l"}, paths...)
		result, err := runner.Exec(ctx, "goimports", args...)
		if err == nil {
			for _, file := range parseFileList(result.Stdout) {
				issues = append(issues, ValidationIssue{
					ID:       fmt.Sprintf("imports_%d", idx),
					Severity: Medium,
					File:     file,
					Message:  "file not formatted with goimports",
					RuleID:   "goimports",
				})
				idx++
			}
		}
	}

	return DeduplicateIssues(issues)
}

func parseFileList(output []byte) []string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files
}
