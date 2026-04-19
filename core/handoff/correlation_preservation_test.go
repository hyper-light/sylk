package handoff

import (
	"testing"
)

// TestApplyPreparedContextSessionMetadata_ForwardsCorrelationID is
// the HAND-06 acceptance test. The bridge writes correlation_id into
// the PreparedContext metadata at every turn; executor code hands
// that context to the session creator and must forward correlation
// to the new session's config so the replacement agent's first turn
// inherits the original user-turn correlation. A regression here
// splits trace chains at every handoff: the replacement agent has
// no way to thread its first response back to the triggering request.
func TestApplyPreparedContextSessionMetadata_ForwardsCorrelationID(t *testing.T) {
	pc := NewPreparedContextDefault()
	pc.SetMetadata("agent_type", "engineer")
	pc.SetMetadata("correlation_id", "corr-user-turn-42")
	pc.SetMetadata("parent_correlation_id", "corr-parent-chain")
	pc.SetMetadata("session_id", "sess-9")

	meta := make(map[string]interface{})
	applyPreparedContextSessionMetadata(pc, meta)

	if got := meta["correlation_id"]; got != "corr-user-turn-42" {
		t.Errorf("correlation_id = %v, want corr-user-turn-42", got)
	}
	if got := meta["parent_correlation_id"]; got != "corr-parent-chain" {
		t.Errorf("parent_correlation_id = %v, want corr-parent-chain", got)
	}
	if got := meta["agent_type"]; got != "engineer" {
		t.Errorf("agent_type = %v, want engineer", got)
	}
	if got := meta["session_id"]; got != "sess-9" {
		t.Errorf("session_id = %v, want sess-9", got)
	}
}

// TestApplyPreparedContextSessionMetadata_MissingCorrelationIsNoop
// verifies the forwarder is silent about keys that don't exist —
// correlation is optional (transport-retry / bootstrap handoffs
// may lack it), and the absence must not produce spurious empty
// entries in the session metadata.
func TestApplyPreparedContextSessionMetadata_MissingCorrelationIsNoop(t *testing.T) {
	pc := NewPreparedContextDefault()
	pc.SetMetadata("agent_type", "engineer") // set only one key

	meta := make(map[string]interface{})
	applyPreparedContextSessionMetadata(pc, meta)

	if _, ok := meta["correlation_id"]; ok {
		t.Error("correlation_id should not be present when the PreparedContext has no correlation metadata")
	}
	if _, ok := meta["parent_correlation_id"]; ok {
		t.Error("parent_correlation_id should not be present when the PreparedContext has no parent correlation metadata")
	}
	if got := meta["agent_type"]; got != "engineer" {
		t.Errorf("agent_type still expected to forward: got %v", got)
	}
}

// TestHandoffBridge_SyncPreparedMetadata_CorrelationFlowsToPC
// exercises the upstream half of the HAND-06 chain: whatever
// correlation the bridge sees on a TurnRecord must land on the
// PreparedContext so the downstream executor has something to
// forward. Guards against a future refactor that drops the
// correlation write in syncPreparedMetadata.
func TestHandoffBridge_SyncPreparedMetadata_CorrelationFlowsToPC(t *testing.T) {
	pc := NewPreparedContextDefault()
	agent := &fakeHandoffableAgent{id: "uid-1", kind: "engineer"}
	bridge := &HandoffBridge{
		agent:    agent,
		prepared: pc,
	}

	bridge.syncPreparedMetadata(TurnRecord{
		CorrelationID: "corr-abc-123",
		SessionID:     "sess-1",
	})

	if got, _ := pc.GetMetadata("correlation_id"); got != "corr-abc-123" {
		t.Errorf("pc.correlation_id = %q, want corr-abc-123", got)
	}
	if got, _ := pc.GetMetadata("session_id"); got != "sess-1" {
		t.Errorf("pc.session_id = %q, want sess-1", got)
	}
}

// TestHandoffBridge_SyncPreparedMetadata_EmptyCorrelationIgnored
// covers the edge case where a TurnRecord arrives with a blank
// correlation — the bridge should not overwrite any existing
// correlation with the empty string, since earlier turns on the same
// bridge may have populated it.
func TestHandoffBridge_SyncPreparedMetadata_EmptyCorrelationIgnored(t *testing.T) {
	pc := NewPreparedContextDefault()
	pc.SetMetadata("correlation_id", "corr-earlier")
	agent := &fakeHandoffableAgent{id: "uid-1", kind: "engineer"}
	bridge := &HandoffBridge{
		agent:    agent,
		prepared: pc,
	}

	bridge.syncPreparedMetadata(TurnRecord{
		CorrelationID: "   ", // whitespace-only
		SessionID:     "sess-1",
	})

	if got, _ := pc.GetMetadata("correlation_id"); got != "corr-earlier" {
		t.Errorf("blank correlation should not clobber existing: got %q, want corr-earlier", got)
	}
}
