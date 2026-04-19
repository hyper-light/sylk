package skills

import (
	"context"
	"fmt"
)

// ArtifactPresenceChecker is a predicate the dispatcher consults to
// decide whether a declared artifact is currently present in the
// protocol state. Agents install one via WithArtifactPresenceChecker
// when they open a protocol state; the tool runtime looks it up via
// ArtifactPresenceCheckerFromContext and runs it against the invoked
// skill's declared Contract. A nil checker disables contract
// enforcement (the common case for agents with no protocol state).
type ArtifactPresenceChecker func(artifact string) bool

// ContractViolation is the typed error raised when a skill's Contract
// is violated at runtime. The tool runtime surfaces it as a tool result
// so the LLM reads the structured fields (SkillName, Kind,
// ArtifactName, RecoveryAction, HumanMessage) instead of parsing prose.
type ContractViolation struct {
	SkillName      string `json:"skill_name"`
	Kind           string `json:"kind"`
	ArtifactName   string `json:"artifact_name"`
	RecoveryAction string `json:"recovery_action"`
	HumanMessage   string `json:"human_message"`
}

// Error implements the error interface.
func (v *ContractViolation) Error() string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("skill %q %s contract violated: %s missing (%s)",
		v.SkillName, v.Kind, v.ArtifactName, v.RecoveryAction)
}

// RecoveryHintFn translates a canonical artifact name to a short
// recovery hint. Agents register one per-protocol so the tool runtime
// surfaces the same hint prose the LLM would otherwise have to infer.
type RecoveryHintFn func(artifact string) string

type artifactPresenceCheckerKey struct{}
type recoveryHintKey struct{}

// WithArtifactPresenceChecker stores checker in ctx. Pass nil to
// deliberately disable contract enforcement (useful in tests).
func WithArtifactPresenceChecker(ctx context.Context, checker ArtifactPresenceChecker) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, artifactPresenceCheckerKey{}, checker)
}

// ArtifactPresenceCheckerFromContext returns the installed checker, or
// nil when none has been set.
func ArtifactPresenceCheckerFromContext(ctx context.Context) ArtifactPresenceChecker {
	if ctx == nil {
		return nil
	}
	checker, _ := ctx.Value(artifactPresenceCheckerKey{}).(ArtifactPresenceChecker)
	return checker
}

// WithRecoveryHintFn stores a recovery-hint mapper in ctx.
func WithRecoveryHintFn(ctx context.Context, fn RecoveryHintFn) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, recoveryHintKey{}, fn)
}

// RecoveryHintFnFromContext returns the installed recovery-hint mapper.
// A nil result means no mapper was registered; callers should fall back
// to a generic message.
func RecoveryHintFnFromContext(ctx context.Context) RecoveryHintFn {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(recoveryHintKey{}).(RecoveryHintFn)
	return fn
}

// ValidateContractConsumes enforces the skill's Consumes invariants
// before the handler runs. Returns a typed ContractViolation when a
// required artifact is absent. No-op when skill has no Contract or
// checker is nil.
func ValidateContractConsumes(ctx context.Context, skill *Skill) error {
	if skill == nil || skill.Contract == nil {
		return nil
	}
	checker := ArtifactPresenceCheckerFromContext(ctx)
	if checker == nil {
		return nil
	}
	hint := RecoveryHintFnFromContext(ctx)
	for _, artifact := range skill.Contract.Consumes {
		if !checker(artifact) {
			recovery := defaultRecoveryHint(artifact)
			if hint != nil {
				if h := hint(artifact); h != "" {
					recovery = h
				}
			}
			return &ContractViolation{
				SkillName:      skill.Name,
				Kind:           "consumes",
				ArtifactName:   artifact,
				RecoveryAction: recovery,
				HumanMessage: fmt.Sprintf("%q requires %s which is not present — %s",
					skill.Name, artifact, recovery),
			}
		}
	}
	return nil
}

// ValidateContractProduces enforces the skill's Produces invariants
// after the handler succeeds. The checker MUST reflect post-handler
// state; the dispatcher arranges this by re-reading the projection.
func ValidateContractProduces(ctx context.Context, skill *Skill) error {
	if skill == nil || skill.Contract == nil {
		return nil
	}
	checker := ArtifactPresenceCheckerFromContext(ctx)
	if checker == nil {
		return nil
	}
	for _, artifact := range skill.Contract.Produces {
		if !checker(artifact) {
			return &ContractViolation{
				SkillName:      skill.Name,
				Kind:           "produces",
				ArtifactName:   artifact,
				RecoveryAction: fmt.Sprintf("ensure %q populates %s in its return payload", skill.Name, artifact),
				HumanMessage: fmt.Sprintf("%q completed but did not produce the declared %s artifact",
					skill.Name, artifact),
			}
		}
	}
	return nil
}

func defaultRecoveryHint(artifact string) string {
	return fmt.Sprintf("produce the missing artifact %q before retrying", artifact)
}
