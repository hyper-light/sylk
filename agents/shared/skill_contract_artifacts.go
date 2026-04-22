package shared

import "strings"

// Canonical protocol-state artifact names used by Skill.Contract
// declarations. These are short, stable strings — the protocol layer
// owns the set; individual skills reference them by name so the
// dispatcher can enforce Produces/Consumes invariants uniformly.
//
// Adding a new artifact here is a protocol-level decision: at least one
// skill must produce it and at least one should consume it, and the
// projection must expose whether it's currently present so the LLM can
// introspect via query_pipeline_state / query_global_review_state.
const (
	// ArtifactTestSnapshot is a snapshot the tester captures when
	// run_test_suite succeeds. finalize_pipeline requires it when the
	// tester is packaging verification artifacts; without it, the
	// verification package references stale results from a prior turn.
	ArtifactTestSnapshot = "test_snapshot"

	// ArtifactPendingChallenge names the pending-challenge record a
	// challenge_agent or challenge_* call creates. Consumed by
	// validate_work / process_validation. The dispatcher will block
	// terminal actions other than validate_work / process_validation
	// while this artifact is outstanding.
	ArtifactPendingChallenge = "pending_challenge"

	// ArtifactVerificationArtifact names the per-recipient tester
	// verification artifact queued for the next handoff_next /
	// validate_work call. Consumed by those dispatch skills.
	ArtifactVerificationArtifact = "verification_artifact"

	// ArtifactPendingValidation names the validation response waiting
	// to be consumed by process_validation on the originator.
	ArtifactPendingValidation = "pending_validation"
)

// recoveryForArtifact returns a short prose hint naming the canonical
// producer of the missing artifact. The tool runtime's ContractViolation
// surface and the typed ProtocolError both use this mapping so the LLM
// sees the same recovery prose whatever path surfaces the error.
func recoveryForArtifact(artifact string) string {
	switch strings.TrimSpace(artifact) {
	case ArtifactTestSnapshot:
		return "call run_test_suite to capture a test snapshot before finalizing"
	case ArtifactPendingChallenge:
		return "challenge_agent (or challenge_*) must be invoked to create a pending challenge"
	case ArtifactVerificationArtifact:
		return "call finalize_pipeline (tester variant) to package the verification artifact"
	case ArtifactPendingValidation:
		return "wait for the peer's validate_work response to arrive"
	default:
		return "produce the missing artifact before retrying"
	}
}
