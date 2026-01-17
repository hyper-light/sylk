package versioning

import (
	"sync"
	"testing"
	"time"
)

func TestOpType_String(t *testing.T) {
	tests := []struct {
		op       OpType
		expected string
	}{
		{OpInsert, "insert"},
		{OpDelete, "delete"},
		{OpReplace, "replace"},
		{OpMove, "move"},
		{OpType(99), "unknown"},
	}

	for _, tt := range tests {
		if tt.op.String() != tt.expected {
			t.Errorf("OpType(%d).String() = %s, expected %s", tt.op, tt.op.String(), tt.expected)
		}
	}
}

func TestNewOperation(t *testing.T) {
	t.Run("creates operation with computed ID", func(t *testing.T) {
		baseVersion := ComputeVersionID([]byte("base"), nil)
		target := NewOffsetTarget(0, 100)
		clock := VectorClock{"session-1": 1}

		op := NewOperation(
			baseVersion,
			"test.go",
			target,
			OpInsert,
			[]byte("new content"),
			[]byte("old content"),
			"pipeline-1",
			"session-1",
			"agent-1",
			clock,
		)

		if op.ID.IsZero() {
			t.Error("expected non-zero operation ID")
		}
		if op.FilePath != "test.go" {
			t.Errorf("expected test.go, got %s", op.FilePath)
		}
		if op.Type != OpInsert {
			t.Errorf("expected OpInsert, got %v", op.Type)
		}
	})

	t.Run("clones content", func(t *testing.T) {
		content := []byte("content")
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			content,
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		content[0] = 'x'
		if op.Content[0] == 'x' {
			t.Error("content should be cloned")
		}
	})

	t.Run("clones clock", func(t *testing.T) {
		clock := VectorClock{"session-1": 1}
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			nil,
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			clock,
		)

		clock["session-1"] = 99
		if op.Clock["session-1"] == 99 {
			t.Error("clock should be cloned")
		}
	})

	t.Run("sets timestamp", func(t *testing.T) {
		before := time.Now()
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			nil,
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)
		after := time.Now()

		if op.Timestamp.Before(before) || op.Timestamp.After(after) {
			t.Error("timestamp should be set to current time")
		}
	})
}

func TestOperation_Invert(t *testing.T) {
	t.Run("insert inverts to delete", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			[]byte("new"),
			[]byte("old"),
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		inverted := op.Invert()

		if inverted.Type != OpDelete {
			t.Errorf("expected OpDelete, got %v", inverted.Type)
		}
		if string(inverted.Content) != "old" {
			t.Errorf("expected old, got %s", inverted.Content)
		}
		if string(inverted.OldContent) != "new" {
			t.Errorf("expected new, got %s", inverted.OldContent)
		}
	})

	t.Run("delete inverts to insert", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpDelete,
			[]byte("new"),
			[]byte("old"),
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		inverted := op.Invert()

		if inverted.Type != OpInsert {
			t.Errorf("expected OpInsert, got %v", inverted.Type)
		}
	})

	t.Run("replace stays replace", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpReplace,
			[]byte("new"),
			[]byte("old"),
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		inverted := op.Invert()

		if inverted.Type != OpReplace {
			t.Errorf("expected OpReplace, got %v", inverted.Type)
		}
	})

	t.Run("inverted has new ID", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			[]byte("new"),
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		inverted := op.Invert()

		if inverted.ID == op.ID {
			t.Error("inverted should have different ID")
		}
	})
}

func TestOperationID_String(t *testing.T) {
	var id OperationID
	for i := range id {
		id[i] = byte(i)
	}

	s := id.String()
	if len(s) != 64 {
		t.Errorf("expected 64 char hex string, got %d", len(s))
	}
}

func TestOperationID_Short(t *testing.T) {
	var id OperationID
	id[0], id[1], id[2], id[3] = 0xab, 0xcd, 0xef, 0x12

	if id.Short() != "abcdef12" {
		t.Errorf("expected abcdef12, got %s", id.Short())
	}
}

func TestOperationID_IsZero(t *testing.T) {
	t.Run("zero ID", func(t *testing.T) {
		var id OperationID
		if !id.IsZero() {
			t.Error("expected IsZero true")
		}
	})

	t.Run("non-zero ID", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			[]byte("content"),
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)
		if op.ID.IsZero() {
			t.Error("expected IsZero false")
		}
	})
}

func TestOperation_Clone(t *testing.T) {
	original := NewOperation(
		ComputeVersionID([]byte("base"), nil),
		"test.go",
		NewOffsetTarget(0, 100),
		OpInsert,
		[]byte("content"),
		[]byte("old"),
		"pipeline-1",
		"session-1",
		"agent-1",
		VectorClock{"session-1": 1},
	)

	clone := original.Clone()

	t.Run("same values", func(t *testing.T) {
		if clone.ID != original.ID {
			t.Error("IDs should match")
		}
		if clone.FilePath != original.FilePath {
			t.Error("FilePaths should match")
		}
		if clone.Type != original.Type {
			t.Error("Types should match")
		}
	})

	t.Run("independent content", func(t *testing.T) {
		clone.Content[0] = 'x'
		if original.Content[0] == 'x' {
			t.Error("content should be independent")
		}
	})

	t.Run("independent clock", func(t *testing.T) {
		clone.Clock["session-1"] = 99
		if original.Clock["session-1"] == 99 {
			t.Error("clock should be independent")
		}
	})

	t.Run("independent target", func(t *testing.T) {
		clone.Target.StartOffset = 999
		if original.Target.StartOffset == 999 {
			t.Error("target should be independent")
		}
	})
}

func TestOperation_Size(t *testing.T) {
	t.Run("content only", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			[]byte("12345"),
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		if op.Size() != 5 {
			t.Errorf("expected 5, got %d", op.Size())
		}
	})

	t.Run("content and old content", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpReplace,
			[]byte("12345"),
			[]byte("abc"),
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		if op.Size() != 8 {
			t.Errorf("expected 8, got %d", op.Size())
		}
	})
}

func TestOperation_IsEmpty(t *testing.T) {
	t.Run("empty content non-delete", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			nil,
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		if !op.IsEmpty() {
			t.Error("expected IsEmpty true")
		}
	})

	t.Run("empty content delete", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpDelete,
			nil,
			[]byte("content"),
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		if op.IsEmpty() {
			t.Error("expected IsEmpty false for delete")
		}
	})

	t.Run("non-empty content", func(t *testing.T) {
		op := NewOperation(
			VersionID{},
			"test.go",
			Target{},
			OpInsert,
			[]byte("content"),
			nil,
			"pipeline-1",
			"session-1",
			"agent-1",
			VectorClock{},
		)

		if op.IsEmpty() {
			t.Error("expected IsEmpty false")
		}
	})
}

func TestOperation_DeterministicID(t *testing.T) {
	baseVersion := ComputeVersionID([]byte("base"), nil)
	target := NewOffsetTarget(0, 100)
	clock := VectorClock{"session-1": 1}
	timestamp := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	op1 := Operation{
		BaseVersion: baseVersion,
		FilePath:    "test.go",
		Target:      target,
		Type:        OpInsert,
		Content:     []byte("content"),
		PipelineID:  "pipeline-1",
		SessionID:   "session-1",
		Clock:       clock,
		Timestamp:   timestamp,
	}
	op1.ID = op1.computeID()

	op2 := Operation{
		BaseVersion: baseVersion,
		FilePath:    "test.go",
		Target:      target,
		Type:        OpInsert,
		Content:     []byte("content"),
		PipelineID:  "pipeline-1",
		SessionID:   "session-1",
		Clock:       clock,
		Timestamp:   timestamp,
	}
	op2.ID = op2.computeID()

	if op1.ID != op2.ID {
		t.Error("same inputs should produce same ID")
	}
}

func TestOperation_ConcurrentAccess(t *testing.T) {
	op := NewOperation(
		VersionID{},
		"test.go",
		NewOffsetTarget(0, 100),
		OpInsert,
		[]byte("content"),
		[]byte("old"),
		"pipeline-1",
		"session-1",
		"agent-1",
		VectorClock{"session-1": 1},
	)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = op.Clone()
			_ = op.Size()
			_ = op.IsEmpty()
			_ = op.ID.String()
		}()
	}
	wg.Wait()
}
