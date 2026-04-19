package shared

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

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

	// ArtifactReviewCandidate names the review candidate the inspector
	// extracts during handoff_to_ot. Consumed by the orchestrator's
	// global-review followup dispatch.
	ArtifactReviewCandidate = "review_candidate"
)

// ProtocolContractViolation is the typed error raised when a skill's
// declared Produces / Consumes contract is not satisfied at runtime.
// The dispatcher surfaces it to the LLM as a tool result with the
// structured fields populated, so the LLM reads ArtifactName +
// RecoveryAction instead of parsing prose.
type ProtocolContractViolation struct {
	// SkillName is the skill whose contract was violated.
	SkillName string `json:"skill_name"`
	// Kind is either "produces" (skill returned but didn't deposit the
	// declared artifact) or "consumes" (skill invoked but required
	// artifact was not present in the current protocol state).
	Kind string `json:"kind"`
	// ArtifactName is the canonical artifact that should have been
	// produced or was missing for consumption.
	ArtifactName string `json:"artifact_name"`
	// RecoveryAction names the skill that typically produces the
	// missing artifact, or the corrective step the LLM should take.
	RecoveryAction string `json:"recovery_action"`
	// HumanMessage is the user-facing prose shown in error rows.
	HumanMessage string `json:"human_message"`
}

// Error implements the error interface.
func (p *ProtocolContractViolation) Error() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("skill %q %s contract violated: %s missing (%s)",
		p.SkillName, p.Kind, p.ArtifactName, p.RecoveryAction)
}

// ArtifactPresenceChecker is a predicate the dispatcher consults to
// decide whether a declared artifact is currently present in the
// protocol state. Agents wire this into their tool runtime via
// WithArtifactPresenceChecker; the checker typically reads the
// PipelineProjection / GlobalReviewProjection and returns true when the
// projection shows the artifact.
type ArtifactPresenceChecker func(artifact string) bool

// ValidateSkillContract enforces the skill's Consumes invariants before
// the handler runs. Called by the tool runtime. Returns a typed
// ProtocolContractViolation error when a required artifact is absent.
//
// Produces invariants are checked after the handler runs (see
// ValidateSkillProduction); both Consume and Produce checks are no-ops
// when the skill has no contract declared.
func ValidateSkillContract(skill *skills.Skill, presence ArtifactPresenceChecker) error {
	if skill == nil || skill.Contract == nil || presence == nil {
		return nil
	}
	for _, artifact := range skill.Contract.Consumes {
		if !presence(artifact) {
			return &ProtocolContractViolation{
				SkillName:      skill.Name,
				Kind:           "consumes",
				ArtifactName:   artifact,
				RecoveryAction: recoveryForArtifact(artifact),
				HumanMessage: fmt.Sprintf(
					"%q requires %s which is not present — %s",
					skill.Name, artifact, recoveryForArtifact(artifact),
				),
			}
		}
	}
	return nil
}

// ValidateSkillProduction enforces Produces invariants after the
// handler succeeds. Same typed error surface as ValidateSkillContract.
// The presence checker MUST reflect the post-handler state (reload the
// projection); the dispatcher arranges this.
func ValidateSkillProduction(skill *skills.Skill, presence ArtifactPresenceChecker) error {
	if skill == nil || skill.Contract == nil || presence == nil {
		return nil
	}
	for _, artifact := range skill.Contract.Produces {
		if !presence(artifact) {
			return &ProtocolContractViolation{
				SkillName:      skill.Name,
				Kind:           "produces",
				ArtifactName:   artifact,
				RecoveryAction: fmt.Sprintf("ensure %q populates %s in its return payload", skill.Name, artifact),
				HumanMessage: fmt.Sprintf(
					"%q completed but did not produce the declared %s artifact",
					skill.Name, artifact,
				),
			}
		}
	}
	return nil
}

// recoveryForArtifact returns a short prose hint naming the canonical
// producer of the missing artifact. Keeps error messages actionable
// without the LLM having to read the protocol-state projection first.
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
	case ArtifactReviewCandidate:
		return "call handoff_to_ot to extract the review candidate"
	default:
		return "produce the missing artifact before retrying"
	}
}
