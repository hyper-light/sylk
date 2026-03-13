package global

import (
	"github.com/adalundhe/sylk/agents/tester/shared"
)

// Protocol phases for the Global Tester's 7-phase testing protocol.
// Each phase is driven by the LLM through skills — the protocol is
// enforced by the system prompt, not by hardcoded sequencing.
//
// Phase 1: assembleBatchContext     — analyze_batch
// Phase 2: analyzeIntegrationRisks — analyze_integration_risks
// Phase 3: architectTestStrategy   — plan_integration_tests + plan_e2e_tests
// Phase 4: constructHarness        — build_harness
// Phase 5: implementTests          — prepare_global_write_context +
//                                     write_integration_test/write_e2e_test
// Phase 6: executeTests            — run_test_suite
// Phase 7: diagnoseAndEscalate     — diagnose_failure + escalate_failure

// GlobalProtocolPhases returns the ordered phases of the global testing protocol.
func GlobalProtocolPhases() []shared.ProtocolPhase {
	return []shared.ProtocolPhase{
		shared.PhaseAssembleBatch,
		shared.PhaseIntegrationRisks,
		shared.PhaseArchitectStrategy,
		shared.PhaseConstructHarness,
		shared.PhaseImplementTests,
		shared.PhaseExecuteTests,
		shared.PhaseDiagnoseEscalate,
	}
}
