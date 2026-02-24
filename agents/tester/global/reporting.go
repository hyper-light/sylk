package global

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/tester/shared"
)

// reportToOrchestrator publishes a failure report to the Orchestrator,
// requesting that new work dispatching be paused until the issue is resolved.
func (gt *GlobalTester) reportToOrchestrator(report *shared.DiagnosisReport, affectedTasks []string) error {
	if gt.bus == nil {
		return fmt.Errorf("global tester bus not available")
	}

	feedback := map[string]any{
		"type":           "test_failure_escalation",
		"severity":       "critical",
		"test_name":      report.TestName,
		"is_product_bug": report.IsProductBug,
		"confidence":     report.Confidence,
		"affected_tasks": affectedTasks,
		"action":         "pause_dispatching",
		"from_agent":     gt.id,
	}

	if len(report.RootCauses) > 0 {
		feedback["root_cause_file"] = report.RootCauses[0].File
		feedback["root_cause_line"] = report.RootCauses[0].Line
		feedback["root_cause_desc"] = report.RootCauses[0].Description
	}

	req := &guide.RouteRequest{
		Input:           fmt.Sprintf("Test failure escalation: %s — pause dispatching", report.TestName),
		SourceAgentID:   gt.id,
		SourceAgentName: "tester",
		TargetAgentID:   "orchestrator",
		FireAndForget:   true,
		SessionID:       gt.config.SessionID,
		Timestamp:       time.Now(),
	}

	msg := guide.NewRequestMessage(gt.generateMessageID(), req)
	msg.Metadata = feedback

	return gt.bus.Publish(guide.TopicGuideRequests, msg)
}

// reportToArchitect publishes a failure report to the Architect,
// requesting a plan modification to address the root cause.
func (gt *GlobalTester) reportToArchitect(report *shared.DiagnosisReport, affectedTasks []string) error {
	if gt.bus == nil {
		return fmt.Errorf("global tester bus not available")
	}

	feedback := map[string]any{
		"type":           "test_failure_plan_modification",
		"test_name":      report.TestName,
		"is_product_bug": report.IsProductBug,
		"confidence":     report.Confidence,
		"affected_tasks": affectedTasks,
		"action":         "modify_plan",
		"from_agent":     gt.id,
	}

	if len(report.RootCauses) > 0 {
		feedback["root_cause_file"] = report.RootCauses[0].File
		feedback["root_cause_line"] = report.RootCauses[0].Line
		feedback["root_cause_desc"] = report.RootCauses[0].Description
		feedback["root_cause_category"] = string(report.RootCauses[0].Category)
	}

	if len(report.SuggestedFix) > 0 {
		feedback["suggested_fix"] = formatSuggestedFixes(report.SuggestedFix)
	}

	req := &guide.RouteRequest{
		Input:           fmt.Sprintf("Test failure — plan modification needed: %s", report.TestName),
		SourceAgentID:   gt.id,
		SourceAgentName: "tester",
		TargetAgentID:   "architect",
		FireAndForget:   true,
		SessionID:       gt.config.SessionID,
		Timestamp:       time.Now(),
	}

	msg := guide.NewRequestMessage(gt.generateMessageID(), req)
	msg.Metadata = feedback

	return gt.bus.Publish(guide.TopicGuideRequests, msg)
}

// escalateFailure reports to both Orchestrator (pause) and Architect (fix plan).
func (gt *GlobalTester) escalateFailure(report *shared.DiagnosisReport, affectedTasks []string) error {
	orchErr := gt.reportToOrchestrator(report, affectedTasks)
	archErr := gt.reportToArchitect(report, affectedTasks)

	switch {
	case orchErr != nil && archErr != nil:
		return fmt.Errorf("orchestrator: %w; architect: %v", orchErr, archErr)
	case orchErr != nil:
		return fmt.Errorf("orchestrator: %w", orchErr)
	case archErr != nil:
		return fmt.Errorf("architect: %w", archErr)
	default:
		return nil
	}
}

// formatSuggestedFixes produces a compact string of suggested fixes for metadata.
func formatSuggestedFixes(fixes []shared.SuggestedFix) string {
	var b strings.Builder
	for i, fix := range fixes {
		if i > 0 {
			b.WriteString("; ")
		}
		target := "product"
		if fix.IsTestFix {
			target = "test"
		}
		b.WriteString(fmt.Sprintf("[%s] %s:%d — %s", target, fix.File, fix.Line, fix.Description))
	}
	return b.String()
}
