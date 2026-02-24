package pipeline

import (
	"github.com/adalundhe/sylk/agents/tester/shared"
)

// Protocol phases for the Pipeline Tester's 6-phase testing protocol.
// Each phase is driven by the LLM through skills — the protocol is
// enforced by the system prompt, not by hardcoded sequencing.
//
// Phase 1: gateOnInspector    — check_inspector_gate
// Phase 2: analyzeRisks       — analyze_risk
// Phase 3: formulateTestPlan  — plan_tests
// Phase 4: implementTests     — write_test
// Phase 5: executeAndDiagnose — run_test_suite + diagnose_failure
// Phase 6: dispatchFeedback   — report_to_engineer / report_to_designer

// PipelineProtocolPhases returns the ordered phases of the pipeline testing protocol.
func PipelineProtocolPhases() []shared.ProtocolPhase {
	return []shared.ProtocolPhase{
		shared.PhaseGateInspector,
		shared.PhaseAnalyzeRisks,
		shared.PhasePlanTests,
		shared.PhaseImplementTests,
		shared.PhaseExecuteTests,
		shared.PhaseDispatchFeedback,
	}
}
