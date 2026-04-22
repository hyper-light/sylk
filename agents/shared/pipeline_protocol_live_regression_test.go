package shared

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestPipelineProtocol_LiveRegression_Challenge169a56d1 reproduces the
// exact live failure that drove Fix A + Fix B: on 2026-04-21 the
// inspector-pipeline agent created challenge `task_1-challenge-169a56d1`
// at 15:17:12; the tester-pipeline mailbox received the obligation; but
// when the tester reopened pipeline protocol state at 15:29:11 and
// asked for action=validate with the same challenge_id, the protocol
// responded `no_pending_challenge`.
//
// Forensic analysis pointed at two stacked bugs:
//
//  1. The write-side dedupe key was (kind, correlation_id). The
//     inspector's tool-call turn emitted multiple distinct
//     handoff_selected events under a single correlation_id (pipe_93e5e6…);
//     the durable store's dedupe-key composition conflated them and
//     silently dropped all but the first.
//
//  2. The read-side loadDurableProjection unconditionally clobbered
//     the task.Context-seeded in-flight snapshot with the persisted
//     checkpoint, so even when the dispatcher had stamped the fresh
//     challenge onto the task, reopening state on the tester side
//     erased it.
//
// This test stitches both failure modes into one end-to-end scenario:
// simulate an inspector turn that appends three distinct
// handoff_selected events under the same correlation_id, then simulate
// a tester reopening state with a task.Context that contains the third
// challenge. Before Fix A+Fix B, reopening lost the challenge. After,
// the reopened snapshot carries it.
func TestPipelineProtocol_LiveRegression_Challenge169a56d1(t *testing.T) {
	t.Parallel()
	sessionDir := t.TempDir()
	scopeID := "task_1"

	// --- WRITE SIDE (inspector turn) -------------------------------------
	// The inspector appends three distinct handoff_selected events under
	// a single correlation_id, matching the real inspector tool-call
	// behavior (pipe_93e5e6a6-ed9... was the actual correlation id).
	store, err := openDurableProtocolLog(sessionDir, pipelineProtocolNamespace, scopeID)
	if err != nil {
		t.Fatalf("openDurableProtocolLog: %v", err)
	}
	defer store.Close()

	const sharedCorrelation = "pipe_93e5e6a6-ed9"

	challenges := []PipelineTurnAction{
		{
			Type:             PipelineProtocolActionHandoff,
			AgentType:        "inspector-pipeline",
			TargetAgents:     []string{"tester-pipeline"},
			CreatesChallenge: true,
			ChallengeID:      "task_1-challenge-first",
			Summary:          "first distinct challenge",
			Request:          "validate the first proof",
			References:       []string{"step-1.md"},
		},
		{
			Type:             PipelineProtocolActionHandoff,
			AgentType:        "inspector-pipeline",
			TargetAgents:     []string{"tester-pipeline"},
			CreatesChallenge: true,
			ChallengeID:      "task_1-challenge-second",
			Summary:          "second distinct challenge",
			Request:          "validate the second proof",
			References:       []string{"step-2.md"},
		},
		{
			Type:             PipelineProtocolActionHandoff,
			AgentType:        "inspector-pipeline",
			TargetAgents:     []string{"tester-pipeline"},
			CreatesChallenge: true,
			ChallengeID:      "task_1-challenge-169a56d1",
			Summary:          "the challenge that the real forensic logs show lost",
			Request:          "validate the third proof",
			References:       []string{"step-3.md"},
		},
	}
	for i, action := range challenges {
		result, err := store.Append(AppendRequest{
			Kind:          pipelineProtocolEventHandoff,
			AgentType:     "inspector-pipeline",
			CorrelationID: sharedCorrelation,
			Payload:       action,
		})
		if err != nil {
			t.Fatalf("store.Append[%d] unexpected error: %v", i, err)
		}
		if result == nil || result.Seq == 0 {
			t.Fatalf("store.Append[%d] expected non-zero seq; got %+v", i, result)
		}
	}

	// Replay and count handoff_selected events carrying a non-empty
	// challenge_id. Under the pre-Fix-A bug, only the first Append
	// survived — the other two got deduped. This assertion is the
	// direct write-side regression guard.
	seenChallengeIDs := map[string]bool{}
	if err := store.Replay(0, func(seq uint64, event *durableProtocolEvent) error {
		if strings.TrimSpace(event.Kind) != pipelineProtocolEventHandoff {
			return nil
		}
		var action PipelineTurnAction
		if err := json.Unmarshal(event.Payload, &action); err != nil {
			return err
		}
		if id := strings.TrimSpace(action.ChallengeID); id != "" {
			seenChallengeIDs[id] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("store.Replay: %v", err)
	}

	for _, expected := range []string{"task_1-challenge-first", "task_1-challenge-second", "task_1-challenge-169a56d1"} {
		if !seenChallengeIDs[expected] {
			t.Fatalf("write-side regression: challenge %q missing from WAL after Append; seen=%v; Fix A would have dropped it under pre-fix dedupe", expected, seenChallengeIDs)
		}
	}

	// Persist a stale checkpoint that DOES NOT contain the third
	// challenge. This is exactly the situation the real scope hit:
	// at inspector-turn T, the checkpoint was snapshot'd with no
	// pending challenge; then the inspector emitted three handoff
	// events under a single correlation_id; under pre-Fix-A the later
	// events were silently dropped, so when the tester reopened at
	// T+ε the checkpoint's "no pending challenge" state combined with
	// the WAL-missing events to produce a state machine that had
	// forgotten the challenge existed.
	//
	// After Fix A, the WAL carries all three events. After Fix B, the
	// reopened state merges task.Context's fresh view with the
	// checkpoint, preferring the task context's PendingChallenge.
	staleCheckpoint := pipelineProtocolCheckpoint{
		Snapshot: &PipelineProtocolSnapshot{
			ActiveAgents:   []string{"inspector-pipeline"},
			CurrentRequest: "Choose the next action.",
		},
	}
	if err := store.SaveSnapshot(store.journal.LastSequence(), staleCheckpoint); err != nil {
		t.Fatalf("SaveSnapshot stale checkpoint: %v", err)
	}

	// --- READ SIDE (tester reopen) ---------------------------------------
	// Now simulate the tester reopening state via
	// newPipelineProtocolStateForTask. The task carries the third
	// challenge in task.Context.pipeline_protocol, matching the
	// dispatcher's behavior. The on-disk checkpoint is stale. Fix B
	// must merge the base snapshot in so the reopened state carries
	// the fresh PendingChallenge.
	testerTask := &PipelineTaskInput{
		TaskID:    scopeID,
		SessionID: filepath.Base(sessionDir),
		AgentType: "tester-pipeline",
		Context: map[string]any{
			"session_dir": sessionDir,
			"pipeline_protocol": &PipelineProtocolSnapshot{
				PendingChallenge: &PipelineProtocolChallenge{
					ID:              "task_1-challenge-169a56d1",
					RequestingAgent: "inspector-pipeline",
					TargetAgents:    []string{"tester-pipeline"},
					Request:         "validate the third proof",
				},
			},
		},
	}

	state, err := newPipelineProtocolStateForTask(testerTask)
	if err != nil {
		t.Fatalf("newPipelineProtocolStateForTask: %v", err)
	}
	if state == nil {
		t.Fatal("state must be non-nil")
	}
	defer func() {
		if state.store != nil {
			_ = state.store.Close()
		}
	}()

	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatal("reopened snapshot must be non-nil; Fix-B regression (checkpoint cleared snapshot)")
	}
	if snapshot.PendingChallenge == nil {
		t.Fatal("reopened snapshot must carry PendingChallenge from task.Context; Fix-B regression (unconditional clobber of base snapshot)")
	}
	if got := strings.TrimSpace(snapshot.PendingChallenge.ID); got != "task_1-challenge-169a56d1" {
		t.Fatalf("PendingChallenge.ID = %q, want task_1-challenge-169a56d1; Fix-B regression (wrong challenge survived merge)", got)
	}
}

// TestPipelineProtocol_LiveRegression_DistinctPayloadsUnderSameCorrelationBothPersist
// is the focused write-side regression: two Append calls with distinct
// payloads but the same (kind, correlation_id) MUST both persist. This
// is the core of Fix A and the narrowest possible repro of the
// correlation-id-only dedupe bug that the live scenario exposed.
func TestPipelineProtocol_LiveRegression_DistinctPayloadsUnderSameCorrelationBothPersist(t *testing.T) {
	t.Parallel()
	sessionDir := t.TempDir()
	store, err := openDurableProtocolLog(sessionDir, pipelineProtocolNamespace, "scope-live")
	if err != nil {
		t.Fatalf("openDurableProtocolLog: %v", err)
	}
	defer store.Close()

	first, err := store.Append(AppendRequest{
		Kind:          pipelineProtocolEventHandoff,
		CorrelationID: "shared-correlation",
		Payload:       map[string]any{"challenge_id": "a"},
	})
	if err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if first.Seq == 0 {
		t.Fatal("first Append must return non-zero seq")
	}

	second, err := store.Append(AppendRequest{
		Kind:          pipelineProtocolEventHandoff,
		CorrelationID: "shared-correlation",
		Payload:       map[string]any{"challenge_id": "b"},
	})
	if err != nil {
		t.Fatalf("second Append must succeed (distinct payload under same correlation_id); got err=%v", err)
	}
	if second.Seq == 0 {
		t.Fatal("second Append must return non-zero seq; Fix-A regression (distinct-payload dedupe)")
	}
	if first.Seq == second.Seq {
		t.Fatalf("distinct payloads produced same seq (%d); Fix-A regression (dedupe collapsed distinct events)", first.Seq)
	}
	if first.DedupeKey == second.DedupeKey {
		t.Fatalf("distinct payloads produced same dedupe key (%q); Fix-A regression", first.DedupeKey)
	}
}
