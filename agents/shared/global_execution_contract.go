package shared

import (
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
)

type GlobalExecutionMode string
type GlobalExecutionDeliverable string

const (
	GlobalExecutionModeRecall   GlobalExecutionMode = "recall"
	GlobalExecutionModePlan     GlobalExecutionMode = "plan"
	GlobalExecutionModeAuthor   GlobalExecutionMode = "author"
	GlobalExecutionModeExecute  GlobalExecutionMode = "execute"
	GlobalExecutionModeDiagnose GlobalExecutionMode = "diagnose"
	GlobalExecutionModeAudit    GlobalExecutionMode = "audit"
)

const (
	GlobalDeliverableContextSummary    GlobalExecutionDeliverable = "context_summary"
	GlobalDeliverableBatchContext      GlobalExecutionDeliverable = "batch_context"
	GlobalDeliverableRiskAssessment    GlobalExecutionDeliverable = "risk_assessment"
	GlobalDeliverableTestStrategy      GlobalExecutionDeliverable = "test_strategy"
	GlobalDeliverableHarnessPlan       GlobalExecutionDeliverable = "harness_plan"
	GlobalDeliverableTestArtifacts     GlobalExecutionDeliverable = "test_artifacts"
	GlobalDeliverableExecutionEvidence GlobalExecutionDeliverable = "execution_evidence"
	GlobalDeliverableFailureDiagnosis  GlobalExecutionDeliverable = "failure_diagnosis"
	GlobalDeliverableEscalation        GlobalExecutionDeliverable = "escalation_report"
	GlobalDeliverableCrossFileFindings GlobalExecutionDeliverable = "cross_file_findings"
	GlobalDeliverablePlanAdherence     GlobalExecutionDeliverable = "plan_adherence"
	GlobalDeliverableLayerJudgment     GlobalExecutionDeliverable = "layer_judgment"
)

type GlobalExecutionContract struct {
	Role         string
	Intent       guide.Intent
	Mode         GlobalExecutionMode
	Deliverables []GlobalExecutionDeliverable
}

func BuildGlobalExecutionContract(role string, intent guide.Intent, input string) *GlobalExecutionContract {
	role = strings.TrimSpace(role)
	if role == "" {
		return nil
	}
	contract := &GlobalExecutionContract{
		Role:   role,
		Intent: intent,
	}
	switch role {
	case "tester-global":
		classifyGlobalTesterExecution(contract, input)
	case "inspector-global":
		classifyGlobalInspectorExecution(contract, input)
	default:
		return nil
	}
	return contract
}

func AppendGlobalExecutionGuidance(base string, contract *GlobalExecutionContract, role string) string {
	guidance := strings.TrimSpace(globalExecutionGuidance(contract, role))
	if guidance == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return guidance
	}
	return strings.TrimSpace(base) + "\n\n---\n\n" + guidance
}

func classifyGlobalTesterExecution(contract *GlobalExecutionContract, input string) {
	if contract.Intent == guide.IntentRecall {
		contract.Mode = GlobalExecutionModeRecall
		contract.Deliverables = []GlobalExecutionDeliverable{GlobalDeliverableContextSummary}
		return
	}
	normalized := normalizeRequestText(input)
	switch {
	case containsGlobalRequestTerm(normalized, "diagnose", "root cause", "why fail", "why failing", "failure", "flaky", "broken"):
		contract.Mode = GlobalExecutionModeDiagnose
		contract.Deliverables = []GlobalExecutionDeliverable{
			GlobalDeliverableBatchContext,
			GlobalDeliverableRiskAssessment,
			GlobalDeliverableFailureDiagnosis,
			GlobalDeliverableEscalation,
		}
	case containsGlobalRequestTerm(normalized, "plan", "strategy", "coverage", "test approach", "test matrix"):
		contract.Mode = GlobalExecutionModePlan
		contract.Deliverables = []GlobalExecutionDeliverable{
			GlobalDeliverableBatchContext,
			GlobalDeliverableRiskAssessment,
			GlobalDeliverableTestStrategy,
		}
	case containsGlobalRequestTerm(normalized, "write test", "write tests", "add test", "add tests", "create test", "create tests", "integration test", "e2e test", "end-to-end test"):
		contract.Mode = GlobalExecutionModeAuthor
		contract.Deliverables = []GlobalExecutionDeliverable{
			GlobalDeliverableBatchContext,
			GlobalDeliverableRiskAssessment,
			GlobalDeliverableTestStrategy,
			GlobalDeliverableHarnessPlan,
			GlobalDeliverableTestArtifacts,
		}
	default:
		contract.Mode = GlobalExecutionModeExecute
		contract.Deliverables = []GlobalExecutionDeliverable{
			GlobalDeliverableBatchContext,
			GlobalDeliverableRiskAssessment,
			GlobalDeliverableHarnessPlan,
			GlobalDeliverableExecutionEvidence,
		}
	}
}

func classifyGlobalInspectorExecution(contract *GlobalExecutionContract, input string) {
	if contract.Intent == guide.IntentRecall {
		contract.Mode = GlobalExecutionModeRecall
		contract.Deliverables = []GlobalExecutionDeliverable{GlobalDeliverableContextSummary}
		return
	}
	normalized := normalizeRequestText(input)
	deliverables := []GlobalExecutionDeliverable{
		GlobalDeliverableCrossFileFindings,
		GlobalDeliverablePlanAdherence,
		GlobalDeliverableLayerJudgment,
	}
	if containsGlobalRequestTerm(normalized, "escalate", "block", "pause", "route findings") {
		deliverables = append(deliverables, GlobalDeliverableEscalation)
	}
	contract.Mode = GlobalExecutionModeAudit
	contract.Deliverables = uniqueGlobalDeliverables(deliverables)
}

func globalExecutionGuidance(contract *GlobalExecutionContract, role string) string {
	if contract == nil {
		return ""
	}
	lines := []string{
		"# Global Execution Contract",
		"",
		"- Treat the routed request intent and derived global deliverables as the primary driver for tool choice.",
		"- This is a global-scope request. Do not import pipeline-task ledger assumptions unless the request explicitly supplies them.",
	}
	if mode := describeGlobalExecutionMode(contract.Mode); mode != "" {
		lines = append(lines, "- Current request mode: "+mode)
	}
	lines = append(lines, roleSpecificGlobalGuidance(contract, role)...)
	if len(contract.Deliverables) > 0 {
		lines = append(lines, "", "Required Deliverables:")
		for _, deliverable := range contract.Deliverables {
			lines = append(lines, "- "+describeGlobalExecutionDeliverable(deliverable))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func roleSpecificGlobalGuidance(contract *GlobalExecutionContract, role string) []string {
	switch role {
	case "tester-global":
		switch contract.Mode {
		case GlobalExecutionModeRecall:
			return []string{"- This request is recall-oriented. Synthesize prior validation context and gaps; do not force new test execution or writes unless the request explicitly asks for them."}
		case GlobalExecutionModePlan:
			return []string{"- This request is planning-oriented. Produce the strategy and risk frame without pretending execution or authored tests already exist."}
		case GlobalExecutionModeAuthor:
			return []string{"- This request requires authored global tests or harness material. Do not stop at analysis once test artifacts are owed."}
		case GlobalExecutionModeDiagnose:
			return []string{"- This request is diagnosis-oriented. Build the failure narrative from concrete evidence before escalating."}
		default:
			return []string{"- This request requires execution evidence. Do not stop at planning if the deliverables still require harness preparation or suite results."}
		}
	case "inspector-global":
		if contract.Mode == GlobalExecutionModeRecall {
			return []string{"- This request is recall-oriented. Summarize plan, diff, or audit context rather than issuing a blocking quality judgment unless the request explicitly asks for it."}
		}
		return []string{
			"- This request is audit-oriented. Start with the changed files, directly affected interfaces, and current tester or protocol evidence.",
			"- Judge against merged and pending plan obligations, but expand to adjacent files, broader plan context, or specialist consults only when a concrete unresolved risk requires it.",
			"- Pending planned work is a compatibility contract: verify the current change does not block, contradict, or mis-shape future tasks. Do not reopen unrelated pending tasks that this change does not affect.",
		}
	default:
		return nil
	}
}

func normalizeRequestText(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func containsGlobalRequestTerm(input string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(input, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func uniqueGlobalDeliverables(values []GlobalExecutionDeliverable) []GlobalExecutionDeliverable {
	if len(values) == 0 {
		return nil
	}
	out := make([]GlobalExecutionDeliverable, 0, len(values))
	seen := make(map[GlobalExecutionDeliverable]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func describeGlobalExecutionMode(mode GlobalExecutionMode) string {
	switch mode {
	case GlobalExecutionModeRecall:
		return "recall and synthesize prior global context"
	case GlobalExecutionModePlan:
		return "plan cross-pipeline validation strategy"
	case GlobalExecutionModeAuthor:
		return "author reusable global tests or harness assets"
	case GlobalExecutionModeExecute:
		return "execute global validation and gather evidence"
	case GlobalExecutionModeDiagnose:
		return "diagnose failing global validation and prepare escalation"
	case GlobalExecutionModeAudit:
		return "audit cross-file changes against the plan"
	default:
		return ""
	}
}

func describeGlobalExecutionDeliverable(deliverable GlobalExecutionDeliverable) string {
	switch deliverable {
	case GlobalDeliverableContextSummary:
		return "summarized global context or prior results"
	case GlobalDeliverableBatchContext:
		return "assembled batch or cross-component context"
	case GlobalDeliverableRiskAssessment:
		return "risk assessment for the affected integration surface"
	case GlobalDeliverableTestStrategy:
		return "explicit global test strategy or coverage plan"
	case GlobalDeliverableHarnessPlan:
		return "prepared or planned reusable harness state"
	case GlobalDeliverableTestArtifacts:
		return "authored global test artifacts"
	case GlobalDeliverableExecutionEvidence:
		return "execution evidence from the relevant suites"
	case GlobalDeliverableFailureDiagnosis:
		return "root-cause diagnosis for failing validation"
	case GlobalDeliverableEscalation:
		return "escalation report for orchestrator or architect"
	case GlobalDeliverableCrossFileFindings:
		return "cross-file architectural findings"
	case GlobalDeliverablePlanAdherence:
		return "plan-adherence assessment"
	case GlobalDeliverableLayerJudgment:
		return "layer-level quality judgment or grade"
	default:
		return string(deliverable)
	}
}
