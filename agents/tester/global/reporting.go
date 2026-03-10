package global

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (gt *GlobalTester) publishValidationVerdict(payload *agentshared.ValidationVerdictPayload) error {
	if gt.bus == nil {
		return fmt.Errorf("global tester bus not available")
	}
	if payload == nil {
		return fmt.Errorf("validation verdict payload is required")
	}
	if payload.CreatedAt.IsZero() {
		payload.CreatedAt = time.Now().UTC()
	}
	payload.SessionID = firstNonEmpty(payload.SessionID, gt.config.SessionID)
	payload.ValidatorAgentID = firstNonEmpty(payload.ValidatorAgentID, gt.id)
	payload.ValidatorType = firstNonEmpty(payload.ValidatorType, "global-tester")
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal validation verdict: %w", err)
	}

	req := &guide.RouteRequest{
		Input:           string(body),
		SourceAgentID:   gt.id,
		SourceAgentName: "tester",
		TargetAgentID:   "orchestrator",
		ExplicitTarget:  true,
		FireAndForget:   true,
		SessionID:       gt.config.SessionID,
		Timestamp:       time.Now(),
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindValidationVerdict,
		},
	}

	return gt.bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage(gt.generateMessageID(), req))
}

func diagnosisFindings(report *testershared.DiagnosisReport) []agentshared.ValidationFinding {
	if report == nil {
		return nil
	}
	findings := make([]agentshared.ValidationFinding, 0, len(report.RootCauses))
	for idx, root := range report.RootCauses {
		findings = append(findings, agentshared.ValidationFinding{
			ID:             fmt.Sprintf("tester_root_cause_%d", idx+1),
			Kind:           string(root.Category),
			Severity:       agentshared.ValidationSeverityCritical,
			File:           root.File,
			Line:           root.Line,
			Summary:        strings.TrimSpace(root.Description),
			Detail:         strings.TrimSpace(root.Description),
			Recommendation: formatSuggestedFixes(report.SuggestedFix),
		})
	}
	return findings
}

func buildDiagnosisVerdict(report *testershared.DiagnosisReport, affectedTasks []string) *agentshared.ValidationVerdictPayload {
	summary := "Global tester found a blocking failure."
	details := ""
	suggestedFixes := []string(nil)
	if report != nil && strings.TrimSpace(report.TestName) != "" {
		summary = fmt.Sprintf("Global tester found a blocking failure in %s.", report.TestName)
		details = formatSuggestedFixes(report.SuggestedFix)
		if details != "" {
			suggestedFixes = []string{details}
		}
	}
	return &agentshared.ValidationVerdictPayload{
		Kind:               agentshared.ValidationVerdictNeedsArchitectRemediation,
		Severity:           agentshared.ValidationSeverityCritical,
		AffectedTasks:      append([]string(nil), affectedTasks...),
		ShouldPause:        true,
		Summary:            summary,
		Details:            details,
		Findings:           diagnosisFindings(report),
		RecommendedActions: []string{"pause_dispatch", "architect_remediation"},
		SuggestedFixes:     suggestedFixes,
	}
}

// reportToOrchestrator submits a blocking validation verdict to the Orchestrator,
// requesting that new work dispatching be paused until the issue is resolved.
func (gt *GlobalTester) reportToOrchestrator(report *testershared.DiagnosisReport, affectedTasks []string) error {
	return gt.publishValidationVerdict(buildDiagnosisVerdict(report, affectedTasks))
}

// reportToArchitect is intentionally routed through the Orchestrator control
// plane so the hold/remediation loop stays authoritative and race-free.
func (gt *GlobalTester) reportToArchitect(report *testershared.DiagnosisReport, affectedTasks []string) error {
	return gt.publishValidationVerdict(buildDiagnosisVerdict(report, affectedTasks))
}

// escalateFailure submits a single authoritative validation verdict. The
// Orchestrator opens the hold and invokes Architect remediation itself.
func (gt *GlobalTester) escalateFailure(report *testershared.DiagnosisReport, affectedTasks []string) error {
	return gt.publishValidationVerdict(buildDiagnosisVerdict(report, affectedTasks))
}

// formatSuggestedFixes produces a compact string of suggested fixes for metadata.
func formatSuggestedFixes(fixes []testershared.SuggestedFix) string {
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
