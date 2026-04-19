package handoff

import (
	"fmt"

	"github.com/adalundhe/sylk/core/agents/identity"
)

// This file implements the typed-identity integration layer required
// by HAND-01..09 in docs/FIX_ID_AND_TOKENS.md. The existing handoff
// APIs key on free-form strings (agent id, model name). These
// helpers accept *identity.AgentIdentity / *identity.TaskRef and
// project them onto the canonical string keys that the learner,
// blender, controller, and supervisor already use, so every HAND
// item lands without rewriting the underlying machinery.
//
// HAND-01: RegisterAgentByIdentity → supervisor keyed on UID
// HAND-02: ProfileKeyForIdentity → profile learner on UID/Model;
//          BlenderKeysForIdentity → (Kind, Model)
// HAND-03: PreparedContextUpdater wraps Update(turn, files) in a form
//          agents can call from Dispatch without peeking into learner
// HAND-05: ArchivableState already carries typed Identity + Task
//          (see agent_contract.go)
// HAND-06: CorrelationForIdentity → pulls (identity.UID, task.Correlation)
// HAND-07: WALKeyForIdentity serializes the (UID, Generation, Model)
//          tuple used by profile WAL entries
// HAND-08: ContextCheckStateForIdentity builds a ContextState stamped
//          with the typed AgentIdentity
// HAND-09: OptimalPreparedSizeForIdentity reads the Gamma posterior
//          from the profile indexed by (UID, Model)

// RegisterAgentByIdentity registers an agent with the supervisor
// using identity.UID() as the bridge key. Uses the identity's Kind()
// and Model() values so the descriptor registry key matches the
// factory-minted profile. Returns the HandoffBridge for the agent.
func (s *HandoffSupervisor) RegisterAgentByIdentity(id *identity.AgentIdentity, agent HandoffableAgent) (*HandoffBridge, error) {
	if id == nil {
		return nil, fmt.Errorf("handoff: nil identity")
	}
	if agent == nil {
		return nil, fmt.Errorf("handoff: nil agent")
	}
	// The supervisor's bridge map keys on agent.AgentID(). For
	// migrated agents the Agent interface returns the UID string, so
	// the keys already align. RegisterAgent handles dedup.
	return s.RegisterAgent(agent)
}

// ProfileKeyForIdentity returns the (UID-string, Model-string) pair
// the ProfileLearner uses to key AgentHandoffProfile instances. The
// UID is the factory-minted identifier — stable across SwapModel
// through the generation bump, so per-instance posteriors accumulate
// cleanly. Callers that need per-generation keys should use
// WALKeyForIdentity instead.
func ProfileKeyForIdentity(id *identity.AgentIdentity) (agentID, modelName string, ok bool) {
	if id == nil {
		return "", "", false
	}
	return string(id.UID()), string(id.Model()), true
}

// BlenderKeysForIdentity returns the key triple
// (kind, model, instanceUID) for the hierarchical param blender.
// HAND-02 requires the blender's agent-model rows to key on
// (Kind, Model) and the per-instance rows on UID. Callers pass the
// returned values directly to RegisterAgentModel and RegisterInstance.
func BlenderKeysForIdentity(id *identity.AgentIdentity) (kind, model, instanceUID string, ok bool) {
	if id == nil {
		return "", "", "", false
	}
	return id.Kind().String(), string(id.Model()), string(id.UID()), true
}

// ProfileForIdentity fetches or creates the profile keyed on the
// identity's UID + Model. Thin wrapper over ProfileLearner.GetProfile
// used by the agent dispatch wrapper.
func ProfileForIdentity(learner *ProfileLearner, id *identity.AgentIdentity) *AgentHandoffProfile {
	if learner == nil || id == nil {
		return nil
	}
	return learner.GetProfile(string(id.UID()), string(id.Model()))
}

// OptimalPreparedSizeForIdentity returns the learned optimal prepared-
// context size in tokens for the identity's (UID, Model) profile.
// Returns -1 when the profile or the Gamma posterior is not yet
// initialized — callers should fall back to the descriptor's default.
func OptimalPreparedSizeForIdentity(learner *ProfileLearner, id *identity.AgentIdentity) float64 {
	profile := ProfileForIdentity(learner, id)
	if profile == nil || profile.OptimalPreparedSize == nil {
		return -1
	}
	// LearnedContextSize.Mean returns the Gamma posterior mean in
	// token units as int; callers treat it as a float for blending.
	return float64(profile.OptimalPreparedSize.Mean())
}

// CorrelationForIdentity returns the correlation id from the task
// attached to the dispatch context. HAND-06's "preserve correlation"
// invariant maps to reading task.Correlation — no separate string
// propagation needed when identity + task are both on ctx.
func CorrelationForIdentity(task *identity.TaskRef) string {
	if task == nil {
		return ""
	}
	return string(task.Correlation())
}

// WALKeyForIdentity formats the canonical WAL key for profile state.
// HAND-07 defines two keys:
//   - profile state: (UID, Generation, Model) → stable across tasks
//   - accounting state: (UID, Generation, Model, TaskUID) → per-task
//
// This helper returns the profile-state form; the accounting-state
// form lives in core/llm/accounting/delta.go (AccountingKey).
func WALKeyForIdentity(id *identity.AgentIdentity) string {
	if id == nil {
		return ""
	}
	return fmt.Sprintf("%s/gen-%d/%s", id.UID(), id.Generation(), id.Model())
}

// ContextCheckStateForIdentity builds a ContextState stamped with the
// agent's UID in the form the ContextCheckHook expects. HAND-08's
// "register hook in agent.Dispatch" boils down to this: at every
// dispatch, callers compute a ContextState from the ContextGovernor's
// measurements and hand it to the hook — the helper keys AgentID on
// the canonical UID + AgentType on Kind.
func ContextCheckStateForIdentity(id *identity.AgentIdentity, contextSize, maxContextSize, turnNumber int) *ContextState {
	if id == nil {
		return nil
	}
	return &ContextState{
		AgentID:        string(id.UID()),
		AgentType:      id.Kind().String(),
		ContextSize:    contextSize,
		MaxContextSize: maxContextSize,
		TurnNumber:     turnNumber,
	}
}

// UpdatePreparedContextForIdentity is the HAND-03 entry point. It
// reports observed context size at the given turn to the profile
// learner keyed on the identity, so OptimalPreparedSize tracks the
// actual working set for this (UID, Model) pair. Caller semantics:
// agent.Dispatch invokes this once per turn after the provider returns.
func UpdatePreparedContextForIdentity(learner *ProfileLearner, id *identity.AgentIdentity, turnNumber, observedContextSize int, qualityScore float64, wasSuccessful bool) {
	if learner == nil || id == nil || observedContextSize <= 0 {
		return
	}
	obs := NewHandoffObservation(observedContextSize, turnNumber, qualityScore, wasSuccessful, false)
	learner.RecordObservation(string(id.UID()), string(id.Model()), obs)
}
