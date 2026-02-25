package designer

// DesignProtocolPhase identifies a phase within the designer's 6-phase protocol.
// Each phase is driven by the LLM through skills — the protocol is
// enforced by the system prompt, not by hardcoded sequencing.
//
// Phase 1: Understand    — LLM reasoning (no skill)
// Phase 2: Research      — component_search, bus consultations
// Phase 3: Plan          — scope validation
// Phase 4: Implement     — component_create, component_modify, token_suggest
// Phase 5: Validate      — token_validate, a11y_audit, a11y_fix_suggest, contrast_check
// Phase 6: Collaborate   — request_engineer_review, request_inspector_check,
//
//	request_tester_validation, report_to_engineer,
//	report_to_orchestrator, ask_user_clarification
type DesignProtocolPhase string

const (
	PhaseUnderstand   DesignProtocolPhase = "understand"
	PhaseResearch     DesignProtocolPhase = "research"
	PhasePlan         DesignProtocolPhase = "plan"
	PhaseImplement    DesignProtocolPhase = "implement"
	PhaseValidate     DesignProtocolPhase = "validate"
	PhaseCollaborate  DesignProtocolPhase = "collaborate"
)

// DesignProtocolPhases returns the ordered phases of the designer protocol.
func DesignProtocolPhases() []DesignProtocolPhase {
	return []DesignProtocolPhase{
		PhaseUnderstand,
		PhaseResearch,
		PhasePlan,
		PhaseImplement,
		PhaseValidate,
		PhaseCollaborate,
	}
}
