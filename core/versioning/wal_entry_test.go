package versioning

import (
	"testing"
	"time"
)

func TestWALEntry_EncodeDecodeRoundTrip(t *testing.T) {
	entry := &VersionedWALEntry{
		Kind:       WALEntryKindDelta,
		Version:    SemanticVersion{Major: 1, Minor: 3},
		PipelineID: "pipe-42",
		Timestamp:  time.Now().Truncate(time.Nanosecond),
		Deltas: []WALFileDelta{
			{Path: "/src/main.go", Op: WALDeltaOpCreate, NewContent: []byte("package main"), OldContent: nil},
			{Path: "/src/util.go", Op: WALDeltaOpModify, NewContent: []byte("new"), OldContent: []byte("old")},
			{Path: "/src/dead.go", Op: WALDeltaOpDelete, NewContent: nil, OldContent: []byte("removed")},
		},
	}

	encoded := EncodeWALEntry(entry)
	decoded, err := DecodeWALEntry(encoded)
	if err != nil {
		t.Fatalf("DecodeWALEntry: %v", err)
	}

	assertWALEntryEqual(t, entry, decoded)
}

func TestWALEntry_CheckpointRoundTrip(t *testing.T) {
	entry := &VersionedWALEntry{
		Kind:       WALEntryKindCheckpoint,
		Version:    SemanticVersion{Major: 5, Minor: 0},
		PipelineID: "",
		Timestamp:  time.Now().Truncate(time.Nanosecond),
		Deltas: []WALFileDelta{
			{Path: "/a.txt", Op: WALDeltaOpCreate, NewContent: []byte("hello"), OldContent: nil},
		},
	}

	encoded := EncodeWALEntry(entry)
	decoded, err := DecodeWALEntry(encoded)
	if err != nil {
		t.Fatalf("DecodeWALEntry: %v", err)
	}

	if decoded.Kind != WALEntryKindCheckpoint {
		t.Errorf("Kind = %d, want %d", decoded.Kind, WALEntryKindCheckpoint)
	}
	if decoded.Version.Minor != 0 {
		t.Errorf("Minor = %d, want 0", decoded.Version.Minor)
	}
}

func TestWALEntry_EmptyDeltas(t *testing.T) {
	entry := &VersionedWALEntry{
		Kind:       WALEntryKindDelta,
		Version:    SemanticVersion{Major: 0, Minor: 1},
		PipelineID: "empty-pipe",
		Timestamp:  time.Now().Truncate(time.Nanosecond),
		Deltas:     nil,
	}

	encoded := EncodeWALEntry(entry)
	decoded, err := DecodeWALEntry(encoded)
	if err != nil {
		t.Fatalf("DecodeWALEntry: %v", err)
	}

	if len(decoded.Deltas) != 0 {
		t.Errorf("Deltas len = %d, want 0", len(decoded.Deltas))
	}
}

func TestWALEntry_CRCValidation(t *testing.T) {
	entry := &VersionedWALEntry{
		Kind:       WALEntryKindDelta,
		Version:    SemanticVersion{Major: 1, Minor: 0},
		PipelineID: "test",
		Timestamp:  time.Now(),
		Deltas:     []WALFileDelta{{Path: "f.go", Op: WALDeltaOpCreate, NewContent: []byte("x")}},
	}

	encoded := EncodeWALEntry(entry)

	// Corrupt a payload byte.
	if len(encoded) > walEntryHeaderSize+1 {
		encoded[walEntryHeaderSize+1] ^= 0xFF
	}

	_, err := DecodeWALEntry(encoded)
	if err != ErrWALEntryCRCMismatch {
		t.Errorf("expected ErrWALEntryCRCMismatch, got %v", err)
	}
}

func TestWALEntry_TruncatedHeader(t *testing.T) {
	_, err := DecodeWALEntry([]byte{0x01, 0x02})
	if err != ErrWALEntryTruncated {
		t.Errorf("expected ErrWALEntryTruncated, got %v", err)
	}
}

func TestWALEntry_TruncatedPayload(t *testing.T) {
	entry := &VersionedWALEntry{
		Kind:       WALEntryKindDelta,
		Version:    SemanticVersion{Major: 1, Minor: 0},
		PipelineID: "test",
		Timestamp:  time.Now(),
		Deltas:     []WALFileDelta{{Path: "f.go", Op: WALDeltaOpCreate, NewContent: []byte("data")}},
	}

	encoded := EncodeWALEntry(entry)
	// Truncate payload.
	truncated := encoded[:walEntryHeaderSize+2]

	_, err := DecodeWALEntry(truncated)
	if err != ErrWALEntryTruncated {
		t.Errorf("expected ErrWALEntryTruncated, got %v", err)
	}
}

func TestWALDeltaOpFromFileOp(t *testing.T) {
	tests := []struct {
		in   FileOp
		want WALDeltaOp
	}{
		{FileOpCreate, WALDeltaOpCreate},
		{FileOpModify, WALDeltaOpModify},
		{FileOpDelete, WALDeltaOpDelete},
	}
	for _, tt := range tests {
		if got := WALDeltaOpFromFileOp(tt.in); got != tt.want {
			t.Errorf("WALDeltaOpFromFileOp(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func assertWALEntryEqual(t *testing.T, want, got *VersionedWALEntry) {
	t.Helper()
	if got.Kind != want.Kind {
		t.Errorf("Kind = %d, want %d", got.Kind, want.Kind)
	}
	if got.Version != want.Version {
		t.Errorf("Version = %s, want %s", got.Version, want.Version)
	}
	if got.PipelineID != want.PipelineID {
		t.Errorf("PipelineID = %q, want %q", got.PipelineID, want.PipelineID)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	if len(got.Deltas) != len(want.Deltas) {
		t.Fatalf("Deltas len = %d, want %d", len(got.Deltas), len(want.Deltas))
	}
	for i, wd := range want.Deltas {
		gd := got.Deltas[i]
		if gd.Path != wd.Path {
			t.Errorf("Delta[%d].Path = %q, want %q", i, gd.Path, wd.Path)
		}
		if gd.Op != wd.Op {
			t.Errorf("Delta[%d].Op = %d, want %d", i, gd.Op, wd.Op)
		}
		if string(gd.NewContent) != string(wd.NewContent) {
			t.Errorf("Delta[%d].NewContent = %q, want %q", i, gd.NewContent, wd.NewContent)
		}
		if string(gd.OldContent) != string(wd.OldContent) {
			t.Errorf("Delta[%d].OldContent = %q, want %q", i, gd.OldContent, wd.OldContent)
		}
	}
}
