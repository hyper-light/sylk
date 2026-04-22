package versioning

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlEntry_RoundTrip_AllKinds(t *testing.T) {
	cases := []struct {
		name    string
		kind    ControlEntryKind
		payload ControlEntryPayload
	}{
		{
			name: "enqueue",
			kind: ControlKindCommitQueueEnqueue,
			payload: ControlEntryPayload{
				MergedVersion: SemanticVersion{Major: 3, Minor: 7},
				BaseVersion:   SemanticVersion{Major: 3, Minor: 6},
				PipelineID:    "task_hello",
				PathCount:     2,
				BasePath:      []string{"src/a.go", "src/b.go"},
			},
		},
		{
			name: "mark_accepted",
			kind: ControlKindCommitQueueMarkAccepted,
			payload: ControlEntryPayload{
				MergedVersion: SemanticVersion{Major: 3, Minor: 7},
				ReplicaID:     "inspector-global#replica-sess-1:3.7",
			},
		},
		{
			name: "mark_rejected",
			kind: ControlKindCommitQueueMarkRejected,
			payload: ControlEntryPayload{
				MergedVersion:   SemanticVersion{Major: 3, Minor: 7},
				ReplicaID:       "inspector-global#replica-sess-1:3.7",
				RejectionReason: "interface shape conflicts with prior",
				Concerns:        []string{"convention drift", "duplicate interface"},
			},
		},
		{
			name: "mark_superseded",
			kind: ControlKindCommitQueueMarkSuperseded,
			payload: ControlEntryPayload{
				MergedVersion: SemanticVersion{Major: 3, Minor: 7},
				SupersedorVer: SemanticVersion{Major: 3, Minor: 9},
			},
		},
		{
			name: "mark_committed",
			kind: ControlKindCommitQueueMarkCommitted,
			payload: ControlEntryPayload{
				MergedVersion: SemanticVersion{Major: 3, Minor: 9},
			},
		},
		{
			name: "abandon",
			kind: ControlKindCommitQueueAbandon,
			payload: ControlEntryPayload{
				MergedVersion:   SemanticVersion{Major: 3, Minor: 8},
				RejectionReason: "dag cancelled",
			},
		},
		{
			name: "retention_retain",
			kind: ControlKindRetentionRetain,
			payload: ControlEntryPayload{
				RetentionVersion:  SemanticVersion{Major: 3, Minor: 7},
				RetentionHolderID: "inspector-global#replica-sess-1:3.7",
			},
		},
		{
			name: "retention_release",
			kind: ControlKindRetentionRelease,
			payload: ControlEntryPayload{
				RetentionVersion:  SemanticVersion{Major: 3, Minor: 7},
				RetentionHolderID: "inspector-global#replica-sess-1:3.7",
			},
		},
		{
			name: "water_line",
			kind: ControlKindRetentionAdvanceWaterLine,
			payload: ControlEntryPayload{
				WaterLine: SemanticVersion{Major: 4, Minor: 2},
			},
		},
		{
			name: "replica_spawned",
			kind: ControlKindReplicaSpawned,
			payload: ControlEntryPayload{
				ReplicaMergeVersion: SemanticVersion{Major: 3, Minor: 7},
				ReplicaAgentType:    "tester-global",
				ReplicaID:           "tester-global#replica-sess-1:3.7",
			},
		},
		{
			name: "replica_decided",
			kind: ControlKindReplicaDecided,
			payload: ControlEntryPayload{
				ReplicaMergeVersion: SemanticVersion{Major: 3, Minor: 7},
				ReplicaID:           "tester-global#replica-sess-1:3.7",
				ReplicaDecision:     ReplicaDecisionAccepted,
				ReplicaDecisionText: "green, no cross-module regressions",
				Concerns:            []string{"slow test in pkg/util"},
			},
		},
		{
			name: "replica_crashed",
			kind: ControlKindReplicaCrashed,
			payload: ControlEntryPayload{
				ReplicaMergeVersion: SemanticVersion{Major: 3, Minor: 7},
				ReplicaID:           "tester-global#replica-sess-1:3.7",
				RejectionReason:     "context cancelled during tool call",
			},
		},
		{
			name: "session_open",
			kind: ControlKindSessionOpenEpoch,
			payload: ControlEntryPayload{
				EpochMarker: "user-initiated",
			},
		},
		{
			name: "session_close",
			kind: ControlKindSessionCloseEpoch,
			payload: ControlEntryPayload{
				EpochMarker: "clean-shutdown",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := &ControlEntry{
				Kind:    tc.kind,
				Seq:     42,
				Payload: tc.payload,
			}
			buf := EncodeControlEntry(src)
			got, consumed, err := DecodeControlEntry(buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if consumed != len(buf) {
				t.Fatalf("consumed = %d, want %d", consumed, len(buf))
			}
			if got.Kind != src.Kind || got.Seq != src.Seq {
				t.Fatalf("envelope mismatch: %+v vs %+v", got, src)
			}
			assertControlPayloadEqual(t, got.Payload, src.Payload)
		})
	}
}

func TestControlEntry_Decode_CRCMismatchIsDetected(t *testing.T) {
	buf := EncodeControlEntry(&ControlEntry{
		Kind: ControlKindCommitQueueMarkCommitted,
		Seq:  1,
		Payload: ControlEntryPayload{
			MergedVersion: SemanticVersion{Major: 1, Minor: 1},
		},
	})
	// Flip a payload byte to trigger CRC mismatch.
	buf[len(buf)-1] ^= 0xFF
	_, _, err := DecodeControlEntry(buf)
	if err != ErrControlEntryCRCMismatch {
		t.Fatalf("expected CRC mismatch, got %v", err)
	}
}

func TestControlEntry_Decode_TruncatedIsDetected(t *testing.T) {
	buf := EncodeControlEntry(&ControlEntry{
		Kind:    ControlKindCommitQueueEnqueue,
		Seq:     1,
		Payload: ControlEntryPayload{},
	})
	// Drop last 5 bytes of payload.
	buf = buf[:len(buf)-5]
	_, _, err := DecodeControlEntry(buf)
	if err != ErrControlEntryTruncated {
		t.Fatalf("expected truncation, got %v", err)
	}
}

func TestControlWAL_AppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()

	for i := range 5 {
		if _, err := w.Append(ControlKindCommitQueueEnqueue, ControlEntryPayload{
			MergedVersion: SemanticVersion{Major: 1, Minor: uint32(i + 1)},
			PipelineID:    "pipe",
			PathCount:     1,
			BasePath:      []string{"x.go"},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	var seqs []uint64
	if err := w.Replay(func(e *ControlEntry) error {
		seqs = append(seqs, e.Seq)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(seqs) != 5 {
		t.Fatalf("replay got %d entries, want 5", len(seqs))
	}
	for i, seq := range seqs {
		if seq != uint64(i+1) {
			t.Errorf("seq[%d] = %d, want %d", i, seq, i+1)
		}
	}
}

func TestControlWAL_ReopenRestoresNextSeq(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := w.Append(ControlKindSessionOpenEpoch, ControlEntryPayload{EpochMarker: "first"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := w.Append(ControlKindSessionCloseEpoch, ControlEntryPayload{EpochMarker: "first"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w2, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w2.Close()
	if got := w2.NextSeq(); got != 3 {
		t.Fatalf("NextSeq after reopen = %d, want 3", got)
	}

	// Confirm replay returns both prior entries in order.
	var kinds []ControlEntryKind
	if err := w2.Replay(func(e *ControlEntry) error {
		kinds = append(kinds, e.Kind)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(kinds) != 2 || kinds[0] != ControlKindSessionOpenEpoch || kinds[1] != ControlKindSessionCloseEpoch {
		t.Fatalf("replay kinds = %v, want [open, close]", kinds)
	}
}

func TestControlWAL_TruncatesPartialTailOnReopen(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Write one good entry, close cleanly.
	if _, err := w.Append(ControlKindCommitQueueEnqueue, ControlEntryPayload{
		MergedVersion: SemanticVersion{Major: 1, Minor: 1},
		PipelineID:    "p",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Append garbage to simulate a torn write.
	logPath := filepath.Join(dir, "control-wal", "log.bin")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for torn write: %v", err)
	}
	if _, err := f.Write([]byte{0x99, 0x99, 0x99, 0x99, 0x99}); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	f.Close()

	// Reopen — the torn write should be truncated away, restoring
	// the file to the last valid entry.
	w2, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w2.Close()

	if got := w2.NextSeq(); got != 2 {
		t.Fatalf("NextSeq after torn-write reopen = %d, want 2 (one good entry survived)", got)
	}

	count := 0
	if err := w2.Replay(func(_ *ControlEntry) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if count != 1 {
		t.Fatalf("replay count = %d, want 1", count)
	}
}

func assertControlPayloadEqual(t *testing.T, got, want ControlEntryPayload) {
	t.Helper()
	if got.MergedVersion != want.MergedVersion {
		t.Errorf("MergedVersion: got %v want %v", got.MergedVersion, want.MergedVersion)
	}
	if got.BaseVersion != want.BaseVersion {
		t.Errorf("BaseVersion: got %v want %v", got.BaseVersion, want.BaseVersion)
	}
	if got.PipelineID != want.PipelineID {
		t.Errorf("PipelineID: got %q want %q", got.PipelineID, want.PipelineID)
	}
	if got.PathCount != want.PathCount {
		t.Errorf("PathCount: got %d want %d", got.PathCount, want.PathCount)
	}
	if !stringSlicesEqual(got.BasePath, want.BasePath) {
		t.Errorf("BasePath: got %v want %v", got.BasePath, want.BasePath)
	}
	if got.ReplicaID != want.ReplicaID {
		t.Errorf("ReplicaID: got %q want %q", got.ReplicaID, want.ReplicaID)
	}
	if got.RejectionReason != want.RejectionReason {
		t.Errorf("RejectionReason: got %q want %q", got.RejectionReason, want.RejectionReason)
	}
	if !stringSlicesEqual(got.Concerns, want.Concerns) {
		t.Errorf("Concerns: got %v want %v", got.Concerns, want.Concerns)
	}
	if got.SupersedorVer != want.SupersedorVer {
		t.Errorf("SupersedorVer: got %v want %v", got.SupersedorVer, want.SupersedorVer)
	}
	if got.RetentionVersion != want.RetentionVersion {
		t.Errorf("RetentionVersion: got %v want %v", got.RetentionVersion, want.RetentionVersion)
	}
	if got.RetentionHolderID != want.RetentionHolderID {
		t.Errorf("RetentionHolderID: got %q want %q", got.RetentionHolderID, want.RetentionHolderID)
	}
	if got.WaterLine != want.WaterLine {
		t.Errorf("WaterLine: got %v want %v", got.WaterLine, want.WaterLine)
	}
	if got.ReplicaMergeVersion != want.ReplicaMergeVersion {
		t.Errorf("ReplicaMergeVersion: got %v want %v", got.ReplicaMergeVersion, want.ReplicaMergeVersion)
	}
	if got.ReplicaAgentType != want.ReplicaAgentType {
		t.Errorf("ReplicaAgentType: got %q want %q", got.ReplicaAgentType, want.ReplicaAgentType)
	}
	if got.ReplicaDecision != want.ReplicaDecision {
		t.Errorf("ReplicaDecision: got %d want %d", got.ReplicaDecision, want.ReplicaDecision)
	}
	if got.ReplicaDecisionText != want.ReplicaDecisionText {
		t.Errorf("ReplicaDecisionText: got %q want %q", got.ReplicaDecisionText, want.ReplicaDecisionText)
	}
	if got.EpochMarker != want.EpochMarker {
		t.Errorf("EpochMarker: got %q want %q", got.EpochMarker, want.EpochMarker)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
