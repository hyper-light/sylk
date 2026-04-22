package shared

import (
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/agentlog"
)

// TestDurableProtocolLog_IdenticalPayloadRepeatDedupes is the retry-
// idempotency invariant: a second Append with the same (kind,
// correlation_id, payload) returns ErrDurableProtocolDuplicate and
// preserves the original sequence number.
func TestDurableProtocolLog_IdenticalPayloadRepeatDedupes(t *testing.T) {
	log := openTestProtocolLog(t)

	first, err := log.Append(AppendRequest{
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-123",
		Payload:       map[string]any{"v": 1},
	})
	if err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if first.Event == nil {
		t.Fatal("first Append returned nil event")
	}
	if first.Seq == 0 {
		t.Fatal("first Append returned zero seq")
	}

	second, dupErr := log.Append(AppendRequest{
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-123",
		Payload:       map[string]any{"v": 1},
	})
	if !errors.Is(dupErr, ErrDurableProtocolDuplicate) {
		t.Fatalf("second identical Append error = %v, want ErrDurableProtocolDuplicate", dupErr)
	}
	if second.Seq != first.Seq {
		t.Fatalf("duplicate Append seq = %d, want original seq %d", second.Seq, first.Seq)
	}
}

// TestDurableProtocolLog_DistinctPayloadsUnderSameCorrelationBothPersist
// is the critical correctness fix — the behavior that was broken before
// the three-layer dedupe landed. The live failure (challenge
// 169a56d1) happened exactly here: inspector appended a second
// handoff_selected under the same correlation_id as a prior one, with
// a DIFFERENT payload (the ChallengeID differed), and the old dedupe
// silently dropped the second write. After the fix, both writes
// persist — their payload fingerprints differ.
//
// If this assertion ever regresses, we will immediately reproduce the
// state-loss failure that wedged the tester pipeline. Do not "fix"
// this test by weakening it — fix the dedupe logic.
func TestDurableProtocolLog_DistinctPayloadsUnderSameCorrelationBothPersist(t *testing.T) {
	log := openTestProtocolLog(t)

	first, err := log.Append(AppendRequest{
		Kind:          "handoff_selected",
		AgentType:     "inspector-pipeline",
		CorrelationID: "pipe_93e5e6a6-ed9",
		Payload:       map[string]any{"ChallengeID": "", "Target": "tester"},
	})
	if err != nil {
		t.Fatalf("first distinct-payload Append: %v", err)
	}

	second, err := log.Append(AppendRequest{
		Kind:          "handoff_selected",
		AgentType:     "inspector-pipeline",
		CorrelationID: "pipe_93e5e6a6-ed9",
		Payload:       map[string]any{"ChallengeID": "task_1-challenge-169a56d1", "Target": "tester"},
	})
	if err != nil {
		t.Fatalf("distinct-payload Append under same correlation_id must succeed: %v", err)
	}
	if second.Seq == first.Seq {
		t.Fatalf("distinct payloads under same correlation produced identical seq %d — fingerprint dedupe collided on payload", first.Seq)
	}
	if second.PayloadFingerprint == first.PayloadFingerprint {
		t.Fatalf("distinct payloads produced equal fingerprints %q — fingerprint hash is broken", first.PayloadFingerprint)
	}
}

// TestDurableProtocolLog_ExplicitIdempotencyKeyOverridesFingerprint
// verifies the caller-supplied IdempotencyKey path: when set, it
// takes priority over payload fingerprinting. Two calls with the
// same IdempotencyKey dedupe even if their payloads differ —
// the caller has asserted logical-intent equivalence the hash can't
// see.
func TestDurableProtocolLog_ExplicitIdempotencyKeyOverridesFingerprint(t *testing.T) {
	log := openTestProtocolLog(t)

	first, err := log.Append(AppendRequest{
		Kind:           "kind.a",
		AgentType:      "engineer",
		CorrelationID:  "corr-a",
		IdempotencyKey: "logical-event-42",
		Payload:        map[string]any{"v": 1},
	})
	if err != nil {
		t.Fatalf("first Append: %v", err)
	}

	second, err := log.Append(AppendRequest{
		Kind:           "kind.a",
		AgentType:      "engineer",
		CorrelationID:  "corr-a",
		IdempotencyKey: "logical-event-42",
		Payload:        map[string]any{"v": 999}, // different payload, same idempotency key
	})
	if !errors.Is(err, ErrDurableProtocolDuplicate) {
		t.Fatalf("explicit IdempotencyKey repeat must dedupe even with different payload; err=%v", err)
	}
	if second.Seq != first.Seq {
		t.Fatalf("dedupe hit returned seq %d, want original %d", second.Seq, first.Seq)
	}
}

// TestDurableProtocolLog_DistinctCorrelationsEachAppend verifies the
// dedupe set does not falsely collide: different correlation ids
// share the same kind and must each produce a distinct entry.
func TestDurableProtocolLog_DistinctCorrelationsEachAppend(t *testing.T) {
	log := openTestProtocolLog(t)

	first, err := log.Append(AppendRequest{Kind: "kind.a", AgentType: "engineer", CorrelationID: "corr-1"})
	if err != nil {
		t.Fatalf("Append corr-1: %v", err)
	}
	second, err := log.Append(AppendRequest{Kind: "kind.a", AgentType: "engineer", CorrelationID: "corr-2"})
	if err != nil {
		t.Fatalf("Append corr-2: %v", err)
	}
	if second.Seq == first.Seq {
		t.Fatalf("distinct correlations produced the same seq %d — dedupe set collided", first.Seq)
	}
}

// TestDurableProtocolLog_DistinctKindsEachAppend verifies (kind, corr,
// fingerprint) is a composite key: same correlation and payload but
// different kinds are not duplicates.
func TestDurableProtocolLog_DistinctKindsEachAppend(t *testing.T) {
	log := openTestProtocolLog(t)

	if _, err := log.Append(AppendRequest{Kind: "kind.a", AgentType: "engineer", CorrelationID: "corr-shared"}); err != nil {
		t.Fatalf("Append kind.a: %v", err)
	}
	if _, err := log.Append(AppendRequest{Kind: "kind.b", AgentType: "engineer", CorrelationID: "corr-shared"}); err != nil {
		t.Fatalf("Append kind.b with same correlation must succeed: %v", err)
	}
}

// TestDurableProtocolLog_EmptyCorrelationAndPayloadBypassesDedupe covers
// the design choice: events without a correlation id AND without a
// payload cannot be discriminated, so dedupe is disabled for them.
// Two such calls both succeed with distinct seqs.
func TestDurableProtocolLog_EmptyCorrelationAndPayloadBypassesDedupe(t *testing.T) {
	log := openTestProtocolLog(t)

	first, err := log.Append(AppendRequest{Kind: "system.init"})
	if err != nil {
		t.Fatalf("first no-discriminator Append: %v", err)
	}
	second, err := log.Append(AppendRequest{Kind: "system.init"})
	if err != nil {
		t.Fatalf("second no-discriminator Append must succeed (no dedupe key): %v", err)
	}
	if first.Seq == second.Seq {
		t.Fatalf("no-discriminator Appends collapsed to the same seq %d — dedupe should be disabled for empty keys", first.Seq)
	}
}

// TestDurableProtocolLog_WarmDedupeFromWALAcrossRestart is the crash-
// recovery invariant: closing and re-opening the log reconstructs the
// dedupe set from the WAL. After restart, a re-Append of an already-
// persisted event must still be rejected as a duplicate.
func TestDurableProtocolLog_WarmDedupeFromWALAcrossRestart(t *testing.T) {
	sessionDir := t.TempDir()

	first, err := openDurableProtocolLog(sessionDir, "test", "scope-1")
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	originalResult, err := first.Append(AppendRequest{
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-persist",
		Payload:       map[string]any{"v": 1},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — warmDedupeFromWAL runs and should recover corr-persist.
	second, err := openDurableProtocolLog(sessionDir, "test", "scope-1")
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	defer second.Close()

	reResult, reErr := second.Append(AppendRequest{
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-persist",
		Payload:       map[string]any{"v": 1},
	})
	if !errors.Is(reErr, ErrDurableProtocolDuplicate) {
		t.Fatalf("re-Append after restart must be rejected as duplicate; got err=%v", reErr)
	}
	if reResult.Seq != originalResult.Seq {
		t.Fatalf("re-Append seq = %d, want original %d — warm-up did not recover seq", reResult.Seq, originalResult.Seq)
	}
}

// TestDurableProtocolLog_WarmDedupeFromPreFixWAL asserts that entries
// written before the payload-fingerprint upgrade are still recovered
// during warm-up: decodeProtocolEvent backfills the fingerprint from
// the stored payload so the dedupe set's composed key matches what a
// post-fix Append would produce.
func TestDurableProtocolLog_WarmDedupeFromPreFixWAL(t *testing.T) {
	sessionDir := t.TempDir()
	log, err := openDurableProtocolLog(sessionDir, "test", "scope-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Write a pre-fix event directly via the journal, bypassing
	// Append so the record has no PayloadFingerprint and no
	// IdempotencyKey — mimicking what an older process emitted.
	preFixEvent := &durableProtocolEvent{
		EventID:       "legacy-evt-1",
		Namespace:     "test",
		ScopeID:       "scope-1",
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-legacy",
		Payload:       mustJSON(t, map[string]any{"v": 1}),
	}
	if _, err := log.journal.AppendJSON(agentlog.EventProtocolEventAppended, preFixEvent); err != nil {
		t.Fatalf("raw AppendJSON: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen — warmDedupeFromWAL should backfill the fingerprint and
	// insert the legacy event into the dedupe set.
	reopened, err := openDurableProtocolLog(sessionDir, "test", "scope-1")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	_, retryErr := reopened.Append(AppendRequest{
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-legacy",
		Payload:       map[string]any{"v": 1},
	})
	if !errors.Is(retryErr, ErrDurableProtocolDuplicate) {
		t.Fatalf("post-fix retry of pre-fix event must dedupe; got %v", retryErr)
	}
}

// TestDurableProtocolLog_ReplaySkipsDuplicates exercises the Replay-side
// guard: if an on-disk WAL holds two entries for the same fingerprint
// (possible via a pre-fix log or a crash between AppendJSON and
// dedupe set update), Replay invokes fn only for the first occurrence.
func TestDurableProtocolLog_ReplaySkipsDuplicates(t *testing.T) {
	sessionDir := t.TempDir()
	log, err := openDurableProtocolLog(sessionDir, "test", "scope-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer log.Close()

	// Force two WAL entries with identical fingerprints by writing
	// via the journal directly, bypassing Append's dedupe guard —
	// this simulates a pre-fix WAL whose duplicates would otherwise
	// cause double-apply on Replay.
	const kind = "kind.dup"
	const corr = "corr-dup"
	payloadJSON := mustJSON(t, map[string]any{"v": 1})
	fingerprint := computePayloadFingerprint(payloadJSON)
	for i := 0; i < 2; i++ {
		event := log.newEvent(kind, "engineer", corr, "", "", fingerprint, payloadJSON)
		if _, err := log.journal.AppendJSON(agentlog.EventProtocolEventAppended, event); err != nil {
			t.Fatalf("raw AppendJSON iteration %d: %v", i, err)
		}
	}

	// Replay: count callback invocations per fingerprint.
	calls := 0
	_ = log.Replay(0, func(_ uint64, e *durableProtocolEvent) error {
		if e.Kind == kind && e.CorrelationID == corr {
			calls++
		}
		return nil
	})
	if calls > 1 {
		t.Fatalf("Replay invoked fn %d times for duplicate fingerprint — dedupe failed", calls)
	}
}

// TestDurableProtocolLog_SeqForFingerprintReturnsRecorded verifies the
// public accessor returns the seq of a recorded fingerprint and
// reports (0, false) for unknown ones.
func TestDurableProtocolLog_SeqForFingerprintReturnsRecorded(t *testing.T) {
	log := openTestProtocolLog(t)

	result, err := log.Append(AppendRequest{
		Kind:          "kind.a",
		AgentType:     "engineer",
		CorrelationID: "corr-known",
		Payload:       map[string]any{"v": 1},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, ok := log.SeqForFingerprint(result.DedupeKey)
	if !ok {
		t.Fatal("SeqForFingerprint missed a recorded key")
	}
	if got != result.Seq {
		t.Fatalf("SeqForFingerprint = %d, want %d", got, result.Seq)
	}
	if _, ok := log.SeqForFingerprint("kind.a\x1funknown-corr\x1funknown-fp"); ok {
		t.Fatal("SeqForFingerprint should return false for unknown key")
	}
}

// TestDurableProtocolLog_PayloadFingerprintEmptyWhenNilPayload confirms
// the empty-payload edge case: a nil payload produces an empty
// fingerprint, which combines with a present correlation_id to still
// produce a non-empty dedupe key (so dedupe still works for
// correlation-only retries).
func TestDurableProtocolLog_PayloadFingerprintEmptyWhenNilPayload(t *testing.T) {
	log := openTestProtocolLog(t)

	first, err := log.Append(AppendRequest{Kind: "kind.a", AgentType: "engineer", CorrelationID: "corr-a"})
	if err != nil {
		t.Fatalf("first nil-payload Append: %v", err)
	}
	if first.PayloadFingerprint != "" {
		t.Fatalf("nil payload produced non-empty fingerprint %q", first.PayloadFingerprint)
	}

	second, err := log.Append(AppendRequest{Kind: "kind.a", AgentType: "engineer", CorrelationID: "corr-a"})
	if !errors.Is(err, ErrDurableProtocolDuplicate) {
		t.Fatalf("repeat nil-payload Append under same (kind,corr) must dedupe; err=%v seq=%d", err, second.Seq)
	}
}

func openTestProtocolLog(t *testing.T) *durableProtocolLog {
	t.Helper()
	log, err := openDurableProtocolLog(t.TempDir(), "test", "scope-1")
	if err != nil {
		t.Fatalf("openDurableProtocolLog: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := encodeProtocolPayload(v)
	if err != nil {
		t.Fatalf("encodeProtocolPayload: %v", err)
	}
	return data
}
