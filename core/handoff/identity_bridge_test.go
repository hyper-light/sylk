package handoff

import (
	"testing"

	"github.com/adalundhe/sylk/core/agents/identity"
)

func testIdent(t *testing.T, kind identity.AgentType, uid string, model identity.ModelID, gen identity.Generation) *identity.AgentIdentity {
	t.Helper()
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        identity.UID(uid),
		Namespace:  "sess-handoff-test",
		Pod:        identity.PodRef{ID: "test-pod", Type: identity.PodTypeDaemon},
		Name:       identity.Name("agent-" + uid),
		Kind:       kind,
		Category:   identity.CategoryStandalone,
		Model:      model,
		Generation: gen,
		Window:     200_000,
	})
}

func TestProfileKeyForIdentity_UsesUIDAndModel(t *testing.T) {
	id := testIdent(t, identity.AgentTypeGuide, "uid-1", "claude-opus-4-6", 0)
	agentID, modelName, ok := ProfileKeyForIdentity(id)
	if !ok {
		t.Fatal("ok=false for non-nil identity")
	}
	if agentID != "uid-1" {
		t.Fatalf("agentID = %q, want uid-1", agentID)
	}
	if modelName != "claude-opus-4-6" {
		t.Fatalf("modelName = %q, want claude-opus-4-6", modelName)
	}
}

func TestProfileKeyForIdentity_NilIdentity(t *testing.T) {
	_, _, ok := ProfileKeyForIdentity(nil)
	if ok {
		t.Fatal("ok=true for nil identity")
	}
}

func TestBlenderKeysForIdentity_UsesKindModelUID(t *testing.T) {
	id := testIdent(t, identity.AgentTypeArchitect, "uid-2", "claude-sonnet-4-6", 0)
	kind, model, instanceUID, ok := BlenderKeysForIdentity(id)
	if !ok || kind != "architect" || model != "claude-sonnet-4-6" || instanceUID != "uid-2" {
		t.Fatalf("keys=(%q, %q, %q), want (architect, claude-sonnet-4-6, uid-2)", kind, model, instanceUID)
	}
}

func TestWALKeyForIdentity_FormatsCanonical(t *testing.T) {
	id := testIdent(t, identity.AgentTypeGuide, "uid-9", "haiku-4.5-200k", 3)
	key := WALKeyForIdentity(id)
	if key != "uid-9/gen-3/haiku-4.5-200k" {
		t.Fatalf("wal key = %q", key)
	}
}

func TestWALKeyForIdentity_NilReturnsEmpty(t *testing.T) {
	if key := WALKeyForIdentity(nil); key != "" {
		t.Fatalf("nil identity wal key = %q", key)
	}
}

func TestCorrelationForIdentity(t *testing.T) {
	task := identity.RebuildTaskForReplay(identity.ReplayTaskRef{
		UID:         "task-1",
		Correlation: "corr-42",
		Namespace:   "sess-handoff-test",
	})
	if got := CorrelationForIdentity(task); got != "corr-42" {
		t.Fatalf("correlation = %q", got)
	}
	if got := CorrelationForIdentity(nil); got != "" {
		t.Fatalf("nil task correlation = %q", got)
	}
}

func TestContextCheckStateForIdentity_StampsFields(t *testing.T) {
	id := testIdent(t, identity.AgentTypeArchitect, "uid-3", "claude-opus-4-6", 1)
	state := ContextCheckStateForIdentity(id, 1500, 200000, 5)
	if state == nil {
		t.Fatal("nil state")
	}
	if state.AgentID != "uid-3" || state.AgentType != "architect" {
		t.Fatalf("state ids = (%s,%s)", state.AgentID, state.AgentType)
	}
	if state.ContextSize != 1500 || state.MaxContextSize != 200000 || state.TurnNumber != 5 {
		t.Fatalf("context fields wrong: %+v", state)
	}
}

func TestContextCheckStateForIdentity_NilReturnsNil(t *testing.T) {
	if state := ContextCheckStateForIdentity(nil, 0, 0, 0); state != nil {
		t.Fatal("nil identity should return nil state")
	}
}

func TestProfileForIdentity_UsesLearner(t *testing.T) {
	cfg := DefaultProfileLearnerConfig()
	learner := NewProfileLearner(cfg, NewPriorHierarchy())
	id := testIdent(t, identity.AgentTypeGuide, "uid-4", "haiku-4.5-200k", 0)
	profile := ProfileForIdentity(learner, id)
	if profile == nil {
		t.Fatal("learner should auto-create profile")
	}
	if profile.AgentType != string(id.UID()) {
		// GetProfile stores AgentType = agentID argument — here that's UID
		t.Fatalf("profile.AgentType=%q, want %q", profile.AgentType, id.UID())
	}
	if profile.ModelID != "haiku-4.5-200k" {
		t.Fatalf("profile.ModelID=%q", profile.ModelID)
	}
}

func TestOptimalPreparedSizeForIdentity_HasDefaultPrior(t *testing.T) {
	cfg := DefaultProfileLearnerConfig()
	learner := NewProfileLearner(cfg, NewPriorHierarchy())
	id := testIdent(t, identity.AgentTypeGuide, "uid-5", "haiku-4.5-200k", 0)
	size := OptimalPreparedSizeForIdentity(learner, id)
	if size <= 0 {
		t.Fatalf("expected positive default Gamma posterior, got %f", size)
	}
}

func TestOptimalPreparedSizeForIdentity_NilReturnsSentinel(t *testing.T) {
	if got := OptimalPreparedSizeForIdentity(nil, nil); got != -1 {
		t.Fatalf("nil → %f, want -1", got)
	}
}

func TestUpdatePreparedContextForIdentity_FeedsObservation(t *testing.T) {
	cfg := DefaultProfileLearnerConfig()
	learner := NewProfileLearner(cfg, NewPriorHierarchy())
	id := testIdent(t, identity.AgentTypeGuide, "uid-6", "haiku-4.5-200k", 0)

	before := OptimalPreparedSizeForIdentity(learner, id)
	UpdatePreparedContextForIdentity(learner, id, 3, 5_000, 0.8, true)
	UpdatePreparedContextForIdentity(learner, id, 4, 5_000, 0.9, true)
	UpdatePreparedContextForIdentity(learner, id, 5, 5_000, 0.9, true)
	after := OptimalPreparedSizeForIdentity(learner, id)

	// Posterior should move toward observed size (5000) from prior mean (~100).
	if after <= before {
		t.Fatalf("posterior did not move: before=%f after=%f", before, after)
	}
}
