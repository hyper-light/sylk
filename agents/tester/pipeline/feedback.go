package pipeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/tester/shared"
)

// FormatDiagnosisForEngineer formats a DiagnosisReport into structured
// feedback suitable for the Engineer agent's consumption.
func FormatDiagnosisForEngineer(report *shared.DiagnosisReport) string {
	if report == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Test Failure Diagnosis: %s\n\n", report.TestName))
	b.WriteString(fmt.Sprintf("**Confidence:** %.0f%%\n", report.Confidence*100))
	b.WriteString(fmt.Sprintf("**Product Bug:** %t\n\n", report.IsProductBug))

	if len(report.RootCauses) > 0 {
		b.WriteString("### Root Causes\n")
		for i, rc := range report.RootCauses {
			b.WriteString(fmt.Sprintf("%d. **%s:%d** — %s (category: %s)\n",
				i+1, rc.File, rc.Line, rc.Description, rc.Category))
		}
		b.WriteString("\n")
	}

	if len(report.SuggestedFix) > 0 {
		b.WriteString("### Suggested Fixes\n")
		for i, fix := range report.SuggestedFix {
			target := "product"
			if fix.IsTestFix {
				target = "test"
			}
			b.WriteString(fmt.Sprintf("%d. [%s] **%s:%d** — %s\n",
				i+1, target, fix.File, fix.Line, fix.Description))
		}
		b.WriteString("\n")
	}

	if len(report.Trail) > 0 {
		b.WriteString("### Investigation Trail\n")
		for i, step := range report.Trail {
			b.WriteString(fmt.Sprintf("%d. **%s** → %s → *%s*\n",
				i+1, step.Action, step.Finding, step.Conclusion))
		}
	}

	return b.String()
}

// PipelineUpdateFromDiagnosis creates a pipeline update payload from a diagnosis.
func PipelineUpdateFromDiagnosis(
	agentID, dagID, nodeID, taskID string,
	report *shared.DiagnosisReport,
) map[string]any {
	status := "failed"
	if report == nil {
		status = "succeeded"
	}

	update := map[string]any{
		"dag_id":     dagID,
		"node_id":    nodeID,
		"task_id":    taskID,
		"agent_id":   agentID,
		"agent_type": "tester-pipeline",
		"status":     status,
		"timestamp":  time.Now(),
	}

	if report != nil {
		update["error"] = fmt.Sprintf("test failure: %s", report.TestName)
		update["message"] = FormatDiagnosisForEngineer(report)
	}

	return update
}
