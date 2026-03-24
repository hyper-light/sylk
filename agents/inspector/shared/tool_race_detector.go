package shared

import (
	"context"
	"fmt"
	"strings"
)

// raceBlockMarker identifies the start of a DATA RACE block in go test -race output.
const raceBlockMarker = "DATA RACE"

// RunRaceDetector runs go test with race detection and parses DATA RACE blocks.
func RunRaceDetector(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	if !runner.Available("go") {
		return unavailableToolIssues("go")
	}

	args := []string{"test", "-race", "-count=1", "-timeout=60s"}
	args = append(args, normalizeGoPackageExecutionTargets(runner, paths)...)

	result, err := runner.Exec(ctx, "go", args...)
	if err != nil {
		return executionFailureIssues("race-detector", err)
	}

	return parseRaceOutput(runner, string(result.Stderr))
}

func parseRaceOutput(runner *ToolRunner, stderr string) []ValidationIssue {
	if !strings.Contains(stderr, raceBlockMarker) {
		return nil
	}

	blocks := strings.Split(stderr, "==================")
	var issues []ValidationIssue

	idx := 0
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if !strings.Contains(block, raceBlockMarker) {
			continue
		}

		// Extract file locations from the race block
		file, lineNum := extractRaceLocation(runner, block)
		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("race_%d", idx),
			Severity: Critical,
			File:     file,
			Line:     lineNum,
			Message:  summarizeRaceBlock(block),
			RuleID:   "data-race",
		})
		idx++
	}

	return DeduplicateIssues(issues)
}

func extractRaceLocation(runner *ToolRunner, block string) (string, int) {
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Race detector outputs locations like: path/file.go:123 +0x...
		if strings.Contains(trimmed, ".go:") && !strings.HasPrefix(trimmed, "goroutine") {
			file, lineNum, _, _, ok := ParseGoToolLine(trimmed)
			if ok {
				return normalizeToolReportedPath(runner, file), lineNum
			}
			// Try splitting on space for the address suffix
			parts := strings.Fields(trimmed)
			if len(parts) > 0 {
				fileLine := parts[0]
				f, l, _, _, ok := ParseGoToolLine(fileLine + ": race")
				if ok {
					return normalizeToolReportedPath(runner, f), l
				}
			}
		}
	}
	return "", 0
}

func summarizeRaceBlock(block string) string {
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "read") || strings.Contains(trimmed, "write") {
			return "DATA RACE: " + trimmed
		}
	}
	return "DATA RACE detected"
}
