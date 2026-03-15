package pipeline

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
)

type TaskStageResult struct {
	Plan         *testershared.TestPlan
	CreatedFiles []string
	SuiteResult  *tester.TestSuiteResult
}

// ResponseText implements the Guide response text contract so internal tester
// stage snapshots do not leak to users as raw JSON.
func (r *TaskStageResult) ResponseText() string {
	if r == nil {
		return ""
	}

	lines := make([]string, 0, 3)
	if len(r.CreatedFiles) > 0 {
		lines = append(lines, fmt.Sprintf("Created test artifacts: %s.", strings.Join(r.CreatedFiles, ", ")))
	}
	if r.SuiteResult != nil {
		total := r.SuiteResult.TotalTests
		if total <= 0 {
			total = r.SuiteResult.Passed + r.SuiteResult.Failed + r.SuiteResult.Skipped + r.SuiteResult.Errors
		}
		switch {
		case total > 0:
			lines = append(lines, fmt.Sprintf(
				"Latest test run: %d passed, %d failed, %d skipped, %d errors out of %d.",
				r.SuiteResult.Passed,
				r.SuiteResult.Failed,
				r.SuiteResult.Skipped,
				r.SuiteResult.Errors,
				total,
			))
		case strings.TrimSpace(r.SuiteResult.Name) != "":
			lines = append(lines, fmt.Sprintf("Executed test suite %q.", strings.TrimSpace(r.SuiteResult.Name)))
		}
	}
	if len(lines) == 0 && r.Plan != nil && strings.TrimSpace(r.Plan.Rationale) != "" {
		lines = append(lines, fmt.Sprintf("Prepared a test plan: %s.", strings.TrimSpace(r.Plan.Rationale)))
	}
	if len(lines) == 0 && r.Plan != nil {
		lines = append(lines, "Prepared a concrete test plan.")
	}
	return strings.Join(lines, "\n")
}

func taskContextString(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}
