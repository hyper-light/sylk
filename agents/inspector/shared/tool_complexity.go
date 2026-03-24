package shared

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// defaultCyclomaticThreshold is the CLAUDE.md mandated max cyclomatic complexity.
const defaultCyclomaticThreshold = 4

// defaultCognitiveThreshold is the max cognitive complexity threshold.
const defaultCognitiveThreshold = 8

// RunComplexity runs gocyclo and/or gocognit and parses results.
// Output format: <score> <pkg> <func> <file>:<line>
func RunComplexity(ctx context.Context, runner *ToolRunner, paths []string, maxCyclomatic, maxCognitive int) []ValidationIssue {
	if maxCyclomatic <= 0 {
		maxCyclomatic = defaultCyclomaticThreshold
	}
	if maxCognitive <= 0 {
		maxCognitive = defaultCognitiveThreshold
	}

	var issues []ValidationIssue
	idx := 0
	fileTargets, err := normalizeGoFileExecutionTargets(ctx, runner, paths)
	if err != nil {
		return executionFailureIssues("complexity", err)
	}
	if len(fileTargets) == 0 {
		return nil
	}

	// Cyclomatic complexity
	if runner.Available("gocyclo") {
		args := append([]string{"-over", strconv.Itoa(maxCyclomatic)}, fileTargets...)
		result, err := runner.Exec(ctx, "gocyclo", args...)
		if err != nil {
			issues = append(issues, executionFailureIssues("gocyclo", err)...)
		} else {
			for _, issue := range parseComplexityOutput(runner, result.Stdout, "cyclomatic", maxCyclomatic, idx) {
				issues = append(issues, issue)
				idx++
			}
		}
	} else {
		issues = append(issues, unavailableToolIssues("gocyclo")...)
	}

	// Cognitive complexity
	if runner.Available("gocognit") {
		args := append([]string{"-over", strconv.Itoa(maxCognitive)}, fileTargets...)
		result, err := runner.Exec(ctx, "gocognit", args...)
		if err != nil {
			issues = append(issues, executionFailureIssues("gocognit", err)...)
		} else {
			for _, issue := range parseComplexityOutput(runner, result.Stdout, "cognitive", maxCognitive, idx) {
				issues = append(issues, issue)
				idx++
			}
		}
	} else {
		issues = append(issues, unavailableToolIssues("gocognit")...)
	}

	return DeduplicateIssues(issues)
}

func parseComplexityOutput(runner *ToolRunner, output []byte, kind string, threshold, startIdx int) []ValidationIssue {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var issues []ValidationIssue

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Format: <score> <pkg> <func> <file>:<line>
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		score, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		funcName := fields[2]
		file, lineNum, _, _, ok := ParseGoToolLine(fields[3] + ": complex")
		if !ok {
			continue
		}

		sev := High
		if score > threshold*2 {
			sev = Critical
		}

		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("cx_%d", startIdx+i),
			Severity: sev,
			File:     normalizeToolReportedPath(runner, file),
			Line:     lineNum,
			Message:  fmt.Sprintf("%s complexity %d exceeds threshold %d in %s", kind, score, threshold, funcName),
			RuleID:   kind + "-complexity",
		})
	}

	return issues
}
