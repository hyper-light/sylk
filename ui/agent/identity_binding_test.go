package agent

import (
	"testing"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// buildIdentity returns a synthesized canonical AgentIdentity for tests.
// Uses RebuildForReplay to bypass Factory (no ordinal registry involved).
func buildIdentity(uid string, kind identity.AgentType, model identity.ModelID, gen identity.Generation) *identity.AgentIdentity {
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        identity.UID(uid),
		Namespace:  "sess-test",
		Pod:        identity.PodRef{ID: identity.PodID(kind.String()), Type: identity.PodTypeDaemon},
		Name:       identity.Name(kind.String()),
		Kind:       kind,
		Category:   identity.CategoryStandalone,
		Model:      model,
		Generation: gen,
	})
}

// buildReplica returns a synthesized replica identity whose Owner
// back-references the given canonical parent UID.
func buildReplica(uid string, parent *identity.AgentIdentity, replicaName string) *identity.AgentIdentity {
	if parent == nil {
		return nil
	}
	owner := parent.Owner()
	_ = owner // parent has no owner (canonical); we build a fresh OwnerRef below
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        identity.UID(uid),
		Namespace:  parent.Namespace(),
		Pod:        parent.Pod(),
		Name:       identity.Name(replicaName),
		Kind:       parent.Kind(),
		Category:   parent.Category(),
		Model:      parent.Model(),
		Generation: 0,
		Owner: &identity.OwnerRef{
			UID:  parent.UID(),
			Name: parent.Name(),
			Kind: parent.Kind(),
		},
	})
}

// eventForIdentity wraps an AgentIdentity in an ActivityEventMsg with
// the AgentID string set to Identity.Panel() — mirrors what the
// provider-gateway hook emits in production.
func eventForIdentity(id *identity.AgentIdentity) msg.ActivityEventMsg {
	return msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			EventType: events.EventTypeLLMResponse,
			AgentID:   id.Panel(),
			SessionID: string(id.Namespace()),
			Identity:  id,
			Outcome:   events.OutcomeSuccess,
		},
	}
}

func TestBindIdentity_CanonicalAgentCreatesSingleRow(t *testing.T) {
	m := New(theme.DefaultDark())
	id := buildIdentity("uid-guide-1", identity.AgentTypeGuide, "haiku-4.5-200k", 0)

	_, _ = m.Update(eventForIdentity(id))

	// Exactly one row for guide, keyed on "guide" (the kind string).
	if got := len(m.agents); got != 1 {
		t.Fatalf("agents count = %d, want 1 (keys=%v)", got, mapKeys(m.agents))
	}
	agent, ok := m.agents["guide"]
	if !ok || agent == nil {
		t.Fatalf("guide row not created, keys=%v", mapKeys(m.agents))
	}
	if agent.Identity == nil || agent.Identity.UID() != "uid-guide-1" {
		t.Fatalf("identity not bound: %+v", agent.Identity)
	}
	if agent.ModelID != "haiku-4.5-200k" {
		t.Fatalf("model not applied: %q", agent.ModelID)
	}
	if m.byUID["uid-guide-1"] != "guide" {
		t.Fatalf("byUID mapping wrong: %v", m.byUID)
	}
}

func TestBindIdentity_ReplicaDoesNotCreateNewRow(t *testing.T) {
	m := New(theme.DefaultDark())
	parent := buildIdentity("uid-librarian-1", identity.AgentTypeLibrarian, "sonnet-4.5-1m", 0)
	_, _ = m.Update(eventForIdentity(parent))
	if got := len(m.agents); got != 1 {
		t.Fatalf("parent row count = %d", got)
	}

	// Three replicas of the same parent — no new rows should appear.
	for i, uid := range []string{"uid-librarian-r0", "uid-librarian-r1", "uid-librarian-r2"} {
		replica := buildReplica(uid, parent, "librarian-r"+string(rune('0'+i)))
		_, _ = m.Update(eventForIdentity(replica))
	}
	if got := len(m.agents); got != 1 {
		t.Fatalf("replicas spawned new rows: count=%d keys=%v", got, mapKeys(m.agents))
	}
	parentRow := m.agents["librarian"]
	if parentRow == nil {
		t.Fatal("librarian row missing")
	}
	if parentRow.ActiveReplicas != 3 {
		t.Fatalf("ActiveReplicas = %d, want 3", parentRow.ActiveReplicas)
	}
	// All three replica UIDs route to the parent row.
	for _, uid := range []string{"uid-librarian-r0", "uid-librarian-r1", "uid-librarian-r2"} {
		if got := m.byUID[identity.UID(uid)]; got != "librarian" {
			t.Fatalf("replica %s → %q, want librarian", uid, got)
		}
	}
}

func TestBindIdentity_ReplicaDoesNotOverwriteParentModel(t *testing.T) {
	m := New(theme.DefaultDark())
	parent := buildIdentity("uid-parent", identity.AgentTypeArchivalist, "sonnet-4.5-1m", 0)
	_, _ = m.Update(eventForIdentity(parent))
	parentRow := m.agents["archivalist"]
	if parentRow == nil {
		t.Fatal("parent row not created")
	}

	// Replica arrives with the SAME kind but a (hypothetically) different model.
	// Replica must not stomp the parent row's model.
	replica := identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        "uid-replica",
		Namespace:  parent.Namespace(),
		Pod:        parent.Pod(),
		Name:       "archivalist-r0",
		Kind:       parent.Kind(),
		Category:   parent.Category(),
		Model:      "not-the-parent-model",
		Generation: 0,
		Owner: &identity.OwnerRef{
			UID:  parent.UID(),
			Name: parent.Name(),
			Kind: parent.Kind(),
		},
	})
	_, _ = m.Update(eventForIdentity(replica))

	if parentRow.ModelID != "sonnet-4.5-1m" {
		t.Fatalf("replica stomped parent model: %q", parentRow.ModelID)
	}
	if parentRow.Identity == nil || parentRow.Identity.UID() != "uid-parent" {
		t.Fatalf("parent Identity overwritten: %+v", parentRow.Identity)
	}
}

func TestBindIdentity_SwapModelUpdatesInPlace(t *testing.T) {
	m := New(theme.DefaultDark())
	gen0 := buildIdentity("uid-arch", identity.AgentTypeArchitect, "claude-opus-4-6", 0)
	_, _ = m.Update(eventForIdentity(gen0))

	row := m.agents["architect"]
	if row == nil || row.Generation != 0 {
		t.Fatalf("gen0 row wrong: %+v", row)
	}

	// SwapModel — same UID, bumped generation, different model.
	gen1 := buildIdentity("uid-arch", identity.AgentTypeArchitect, "claude-sonnet-4-6", 1)
	_, _ = m.Update(eventForIdentity(gen1))

	if got := len(m.agents); got != 1 {
		t.Fatalf("SwapModel should not spawn new rows: count=%d keys=%v", got, mapKeys(m.agents))
	}
	if row.Generation != 1 {
		t.Fatalf("Generation not bumped: %d", row.Generation)
	}
	if row.ModelID != "claude-sonnet-4-6" {
		t.Fatalf("Model not updated: %q", row.ModelID)
	}
	if row.Identity.UID() != "uid-arch" {
		t.Fatalf("UID drifted: %s", row.Identity.UID())
	}
}

func TestBindIdentity_SeededPlaceholderBindsOnFirstEvent(t *testing.T) {
	m := New(theme.DefaultDark())
	// Seed a placeholder with agent-type key but no Identity (mirrors
	// cmd/tui.go's SeedAgents call at bootstrap).
	m.SeedAgent("guide", "guide", "Guide", nil, "", "")

	id := buildIdentity("uid-g", identity.AgentTypeGuide, "haiku-4.5-200k", 0)
	_, _ = m.Update(eventForIdentity(id))

	if got := len(m.agents); got != 1 {
		t.Fatalf("expected seed row bound in place, got %d rows: %v", got, mapKeys(m.agents))
	}
	row := m.agents["guide"]
	if row == nil || row.Name != "Guide" {
		t.Fatalf("seed display name lost: %+v", row)
	}
	if row.Identity == nil || row.Identity.UID() != "uid-g" {
		t.Fatalf("Identity not bound to seed: %+v", row.Identity)
	}
}

func mapKeys(m map[string]*AgentState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
