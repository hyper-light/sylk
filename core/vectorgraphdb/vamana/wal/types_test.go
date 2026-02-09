package wal

import (
	"testing"
)

// allValidOps enumerates every defined OpType for table-driven tests.
var allValidOps = []struct {
	Op   OpType
	Name string
}{
	// IVF vector ops
	{OpInsert, "Insert"},
	{OpDelete, "Delete"},
	{OpCheckpoint, "Checkpoint"},
	{OpCompact, "Compact"},

	// Session storage ops
	{OpNodeInsert, "NodeInsert"},
	{OpNodeDelete, "NodeDelete"},
	{OpEdgeInsert, "EdgeInsert"},
	{OpEdgeUpdate, "EdgeUpdate"},
	{OpEdgeDelete, "EdgeDelete"},
	{OpVectorInsert, "VectorInsert"},
	{OpDocInsert, "DocInsert"},
	{OpChunkRefInsert, "ChunkRefInsert"},

	// Commit bracketing ops
	{OpCommitBegin, "CommitBegin"},
	{OpCommitNode, "CommitNode"},
	{OpCommitEdge, "CommitEdge"},
	{OpCommitIndex, "CommitIndex"},
	{OpCommitEnd, "CommitEnd"},

	// Version markers
	{OpVersionCheckpoint, "VersionCheckpoint"},
	{OpSessionCommit, "SessionCommit"},
	{OpShardSeal, "ShardSeal"},
}

func TestOpType_Valid(t *testing.T) {
	for _, tc := range allValidOps {
		if !tc.Op.Valid() {
			t.Errorf("OpType %d (%s) should be valid", tc.Op, tc.Name)
		}
	}
}

func TestOpType_Invalid(t *testing.T) {
	// Values in gaps and beyond defined ranges must be invalid.
	invalids := []OpType{4, 5, 10, 15, 24, 31, 37, 47, 51, 100, 255}
	for _, op := range invalids {
		if op.Valid() {
			t.Errorf("OpType %d should be invalid", op)
		}
	}
}

func TestOpType_String(t *testing.T) {
	for _, tc := range allValidOps {
		if got := tc.Op.String(); got != tc.Name {
			t.Errorf("OpType(%d).String() = %q, want %q", tc.Op, got, tc.Name)
		}
	}
}

func TestOpType_StringUnknown(t *testing.T) {
	invalids := []OpType{4, 15, 24, 31, 37, 51, 128, 255}
	for _, op := range invalids {
		if got := op.String(); got != "Unknown" {
			t.Errorf("OpType(%d).String() = %q, want %q", op, got, "Unknown")
		}
	}
}

func TestOpType_IsSessionOp(t *testing.T) {
	sessionOps := []OpType{
		OpNodeInsert, OpNodeDelete, OpEdgeInsert,
		OpEdgeUpdate, OpEdgeDelete, OpVectorInsert, OpDocInsert, OpChunkRefInsert,
	}
	for _, op := range sessionOps {
		if !op.IsSessionOp() {
			t.Errorf("OpType %d (%s) should be a session op", op, op)
		}
	}

	nonSession := []OpType{OpInsert, OpDelete, OpCommitBegin, OpVersionCheckpoint, 4, 15, 24}
	for _, op := range nonSession {
		if op.IsSessionOp() {
			t.Errorf("OpType %d should not be a session op", op)
		}
	}
}

func TestOpType_IsCommitOp(t *testing.T) {
	commitOps := []OpType{OpCommitBegin, OpCommitNode, OpCommitEdge, OpCommitIndex, OpCommitEnd}
	for _, op := range commitOps {
		if !op.IsCommitOp() {
			t.Errorf("OpType %d (%s) should be a commit op", op, op)
		}
	}

	nonCommit := []OpType{OpInsert, OpNodeInsert, OpVersionCheckpoint, 31, 37}
	for _, op := range nonCommit {
		if op.IsCommitOp() {
			t.Errorf("OpType %d should not be a commit op", op)
		}
	}
}

func TestOpType_IsMarker(t *testing.T) {
	markers := []OpType{OpVersionCheckpoint, OpSessionCommit, OpShardSeal}
	for _, op := range markers {
		if !op.IsMarker() {
			t.Errorf("OpType %d (%s) should be a marker", op, op)
		}
	}

	nonMarker := []OpType{OpInsert, OpNodeInsert, OpCommitBegin, 47, 51}
	for _, op := range nonMarker {
		if op.IsMarker() {
			t.Errorf("OpType %d should not be a marker", op)
		}
	}
}

func TestOpType_ValueAssignments(t *testing.T) {
	// Verify non-contiguous groupings have correct base values.
	groups := []struct {
		name  string
		first OpType
		last  OpType
	}{
		{"IVF", OpInsert, OpCompact},
		{"Session", OpNodeInsert, OpChunkRefInsert},
		{"Commit", OpCommitBegin, OpCommitEnd},
		{"Marker", OpVersionCheckpoint, OpShardSeal},
	}

	for _, g := range groups {
		if g.first > g.last {
			t.Errorf("group %s: first (%d) > last (%d)", g.name, g.first, g.last)
		}
	}

	// Verify gaps between groups.
	if OpCompact >= OpNodeInsert {
		t.Errorf("IVF range should not overlap session range: %d >= %d", OpCompact, OpNodeInsert)
	}
	if OpChunkRefInsert >= OpCommitBegin {
		t.Errorf("session range should not overlap commit range: %d >= %d", OpChunkRefInsert, OpCommitBegin)
	}
	if OpCommitEnd >= OpVersionCheckpoint {
		t.Errorf("commit range should not overlap marker range: %d >= %d", OpCommitEnd, OpVersionCheckpoint)
	}
}

func TestWALEntry_MarshalUnmarshalAllOps(t *testing.T) {
	for _, tc := range allValidOps {
		entry := WALEntry{
			SequenceID: 42,
			OpType:     tc.Op,
			Timestamp:  1234567890,
			Data:       []byte("test-payload"),
		}

		data, err := entry.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary(%s): %v", tc.Name, err)
		}

		var decoded WALEntry
		if err := decoded.UnmarshalBinary(data); err != nil {
			t.Fatalf("UnmarshalBinary(%s): %v", tc.Name, err)
		}

		if decoded.OpType != tc.Op {
			t.Errorf("%s: OpType = %d, want %d", tc.Name, decoded.OpType, tc.Op)
		}
		if decoded.SequenceID != 42 {
			t.Errorf("%s: SequenceID = %d, want 42", tc.Name, decoded.SequenceID)
		}
		if decoded.Timestamp != 1234567890 {
			t.Errorf("%s: Timestamp = %d, want 1234567890", tc.Name, decoded.Timestamp)
		}
		if string(decoded.Data) != "test-payload" {
			t.Errorf("%s: Data = %q, want %q", tc.Name, decoded.Data, "test-payload")
		}
	}
}

func TestWALEntry_MarshalRejectsInvalidOp(t *testing.T) {
	entry := WALEntry{
		SequenceID: 1,
		OpType:     OpType(255),
		Timestamp:  0,
		Data:       nil,
	}

	if _, err := entry.MarshalBinary(); err != ErrInvalidOpType {
		t.Errorf("MarshalBinary(invalid op) = %v, want ErrInvalidOpType", err)
	}
}

func TestWALEntry_UnmarshalRejectsInvalidOp(t *testing.T) {
	// Craft a valid-looking entry with invalid OpType byte.
	entry := WALEntry{
		SequenceID: 1,
		OpType:     OpInsert,
		Timestamp:  0,
		Data:       []byte("x"),
	}

	data, err := entry.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Corrupt the OpType byte (offset 8) to an invalid value.
	data[8] = 255

	// Recompute CRC so checksum passes but OpType validation fails.
	// We don't recompute — the invalid OpType should be caught first.
	var decoded WALEntry
	if err := decoded.UnmarshalBinary(data); err != ErrInvalidOpType {
		t.Errorf("UnmarshalBinary(corrupted op) = %v, want ErrInvalidOpType", err)
	}
}

func TestWALEntry_EmptyData(t *testing.T) {
	entry := WALEntry{
		SequenceID: 1,
		OpType:     OpCommitBegin,
		Timestamp:  999,
		Data:       nil,
	}

	data, err := entry.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	var decoded WALEntry
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}

	if len(decoded.Data) != 0 {
		t.Errorf("Data length = %d, want 0", len(decoded.Data))
	}
	if decoded.OpType != OpCommitBegin {
		t.Errorf("OpType = %d, want %d", decoded.OpType, OpCommitBegin)
	}
}

func TestValidOps_DerivedFromOpNames(t *testing.T) {
	// Ensure the init() derivation is consistent.
	for i := range 256 {
		op := OpType(i)
		namePresent := opNames[op] != ""
		if op.Valid() != namePresent {
			t.Errorf("OpType(%d): Valid()=%v but opNames present=%v", i, op.Valid(), namePresent)
		}
	}
}
