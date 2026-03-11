package shared

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultCoverageThreshold is the minimum acceptable coverage percentage.
const defaultCoverageThreshold = 80.0

// RunCoverage runs go test with coverage and parses the per-function output.
func RunCoverage(ctx context.Context, runner *ToolRunner, paths []string, threshold float64) []ValidationIssue {
	if !runner.Available("go") {
		return unavailableToolIssues("go")
	}

	if threshold <= 0 {
		threshold = defaultCoverageThreshold
	}

	coverFile := filepath.Join(runner.LogicalTempDir(), "sylk_cover.out")
	command := "go test -coverprofile=" + coverFile + " -count=1"
	if len(paths) == 0 {
		command += " ./..."
	} else {
		command += " " + strings.Join(paths, " ")
	}
	command += " && go tool cover -func=" + coverFile
	testResult, err := runner.ExecShell(ctx, command)
	if err != nil {
		return executionFailureIssues("coverage", err)
	}
	return parseCoverageOutput(string(testResult.Stdout), threshold)
}

func parseCoverageOutput(output string, threshold float64) []ValidationIssue {
	lines := strings.Split(output, "\n")
	var issues []ValidationIssue
	idx := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Format: file:line:	funcName		coverage%
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		percentStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		percent, err := strconv.ParseFloat(percentStr, 64)
		if err != nil {
			continue
		}

		if percent < threshold {
			fileLoc := fields[0]
			file, lineNum, _, _, _ := ParseGoToolLine(fileLoc + ": below threshold")

			funcName := ""
			if len(fields) >= 2 {
				funcName = fields[len(fields)-2]
			}

			issues = append(issues, ValidationIssue{
				ID:       fmt.Sprintf("cov_%d", idx),
				Severity: High,
				File:     file,
				Line:     lineNum,
				Message:  fmt.Sprintf("coverage %.1f%% below threshold %.1f%% for %s", percent, threshold, funcName),
				RuleID:   "coverage",
			})
			idx++
		}
	}

	return issues
}
