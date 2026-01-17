package versioning

import (
	"sync"
	"testing"
	"time"
)

func TestNewFileVersion(t *testing.T) {
	t.Run("creates version with computed ID", func(t *testing.T) {
		content := []byte("file content")
		clock := VectorClock{"session-1": 1}

		fv := NewFileVersion(
			"test.go",
			content,
			nil,
			nil,
			"pipeline-1",
			"session-1",
			clock,
		)

		if fv.ID.IsZero() {
			t.Error("expected non-zero ID")
		}
		if fv.FilePath != "test.go" {
			t.Errorf("expected test.go, got %s", fv.FilePath)
		}
		if fv.ContentSize != int64(len(content)) {
			t.Errorf("expected %d, got %d", len(content), fv.ContentSize)
		}
	})

	t.Run("computes content hash", func(t *testing.T) {
		content := []byte("file content")
		fv := NewFileVersion(
			"test.go",
			content,
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		expected := ComputeContentHash(content)
		if fv.ContentHash != expected {
			t.Error("content hash mismatch")
		}
	})

	t.Run("clones parents", func(t *testing.T) {
		parentID := ComputeVersionID([]byte("parent"), nil)
		parents := []VersionID{parentID}

		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			parents,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		parents[0] = VersionID{}
		if fv.Parents[0].IsZero() {
			t.Error("parents should be cloned")
		}
	})

	t.Run("clones operations", func(t *testing.T) {
		opID := OperationID{1, 2, 3}
		ops := []OperationID{opID}

		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			ops,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		ops[0] = OperationID{}
		if fv.Operations[0].IsZero() {
			t.Error("operations should be cloned")
		}
	})

	t.Run("clones clock", func(t *testing.T) {
		clock := VectorClock{"session-1": 1}

		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			clock,
		)

		clock["session-1"] = 99
		if fv.Clock["session-1"] == 99 {
			t.Error("clock should be cloned")
		}
	})

	t.Run("sets timestamp", func(t *testing.T) {
		before := time.Now()
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)
		after := time.Now()

		if fv.Timestamp.Before(before) || fv.Timestamp.After(after) {
			t.Error("timestamp should be set to current time")
		}
	})

	t.Run("sets IsMerge for multiple parents", func(t *testing.T) {
		parent1 := ComputeVersionID([]byte("parent1"), nil)
		parent2 := ComputeVersionID([]byte("parent2"), nil)

		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			[]VersionID{parent1, parent2},
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if !fv.IsMerge {
			t.Error("expected IsMerge true for multiple parents")
		}
	})

	t.Run("IsMerge false for single parent", func(t *testing.T) {
		parent := ComputeVersionID([]byte("parent"), nil)

		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			[]VersionID{parent},
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if fv.IsMerge {
			t.Error("expected IsMerge false for single parent")
		}
	})
}

func TestFileVersion_Clone(t *testing.T) {
	original := NewFileVersion(
		"test.go",
		[]byte("content"),
		[]VersionID{ComputeVersionID([]byte("parent"), nil)},
		[]OperationID{{1, 2, 3}},
		"pipeline-1",
		"session-1",
		VectorClock{"session-1": 1},
	)
	groupID := "variant-group-1"
	original.VariantGroupID = &groupID
	original.VariantLabel = "original"

	clone := original.Clone()

	t.Run("same values", func(t *testing.T) {
		if clone.ID != original.ID {
			t.Error("IDs should match")
		}
		if clone.FilePath != original.FilePath {
			t.Error("FilePaths should match")
		}
	})

	t.Run("independent parents", func(t *testing.T) {
		clone.Parents[0] = VersionID{}
		if original.Parents[0].IsZero() {
			t.Error("parents should be independent")
		}
	})

	t.Run("independent operations", func(t *testing.T) {
		clone.Operations[0] = OperationID{}
		if original.Operations[0].IsZero() {
			t.Error("operations should be independent")
		}
	})

	t.Run("independent clock", func(t *testing.T) {
		clone.Clock["session-1"] = 99
		if original.Clock["session-1"] == 99 {
			t.Error("clock should be independent")
		}
	})

	t.Run("independent variant group ID", func(t *testing.T) {
		newGroupID := "modified"
		clone.VariantGroupID = &newGroupID
		if *original.VariantGroupID == "modified" {
			t.Error("variant group ID should be independent")
		}
	})
}

func TestFileVersion_HasParent(t *testing.T) {
	parent1 := ComputeVersionID([]byte("parent1"), nil)
	parent2 := ComputeVersionID([]byte("parent2"), nil)
	other := ComputeVersionID([]byte("other"), nil)

	fv := NewFileVersion(
		"test.go",
		[]byte("content"),
		[]VersionID{parent1, parent2},
		nil,
		"pipeline-1",
		"session-1",
		VectorClock{},
	)

	if !fv.HasParent(parent1) {
		t.Error("should have parent1")
	}
	if !fv.HasParent(parent2) {
		t.Error("should have parent2")
	}
	if fv.HasParent(other) {
		t.Error("should not have other")
	}
}

func TestFileVersion_IsRoot(t *testing.T) {
	t.Run("root version", func(t *testing.T) {
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if !fv.IsRoot() {
			t.Error("expected IsRoot true")
		}
	})

	t.Run("non-root version", func(t *testing.T) {
		parent := ComputeVersionID([]byte("parent"), nil)
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			[]VersionID{parent},
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if fv.IsRoot() {
			t.Error("expected IsRoot false")
		}
	})
}

func TestFileVersion_IsVariant(t *testing.T) {
	t.Run("not a variant", func(t *testing.T) {
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if fv.IsVariant() {
			t.Error("expected IsVariant false")
		}
	})

	t.Run("is a variant", func(t *testing.T) {
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)
		fv.SetVariant("group-1", "variant-1")

		if !fv.IsVariant() {
			t.Error("expected IsVariant true")
		}
	})
}

func TestFileVersion_SetVariant(t *testing.T) {
	fv := NewFileVersion(
		"test.go",
		[]byte("content"),
		nil,
		nil,
		"pipeline-1",
		"session-1",
		VectorClock{},
	)

	fv.SetVariant("group-1", "variant-1")

	if fv.VariantGroupID == nil || *fv.VariantGroupID != "group-1" {
		t.Error("variant group ID not set correctly")
	}
	if fv.VariantLabel != "variant-1" {
		t.Errorf("expected variant-1, got %s", fv.VariantLabel)
	}
}

func TestFileVersion_AddOperation(t *testing.T) {
	fv := NewFileVersion(
		"test.go",
		[]byte("content"),
		nil,
		nil,
		"pipeline-1",
		"session-1",
		VectorClock{},
	)

	opID := OperationID{1, 2, 3}
	fv.AddOperation(opID)

	if len(fv.Operations) != 1 {
		t.Errorf("expected 1 operation, got %d", len(fv.Operations))
	}
	if fv.Operations[0] != opID {
		t.Error("operation not added correctly")
	}
}

func TestFileVersion_Equal(t *testing.T) {
	content := []byte("content")
	fv1 := NewFileVersion(
		"test.go",
		content,
		nil,
		nil,
		"pipeline-1",
		"session-1",
		VectorClock{},
	)

	t.Run("equal by ID", func(t *testing.T) {
		fv2 := fv1.Clone()
		if !fv1.Equal(&fv2) {
			t.Error("expected Equal true")
		}
	})

	t.Run("different IDs", func(t *testing.T) {
		fv2 := NewFileVersion(
			"test.go",
			[]byte("different"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if fv1.Equal(&fv2) {
			t.Error("expected Equal false")
		}
	})

	t.Run("nil comparison", func(t *testing.T) {
		if fv1.Equal(nil) {
			t.Error("expected Equal false for nil")
		}
	})

	t.Run("both nil", func(t *testing.T) {
		var fv1, fv2 *FileVersion
		if !fv1.Equal(fv2) {
			t.Error("expected Equal true for both nil")
		}
	})
}

func TestFileVersion_HappensBefore(t *testing.T) {
	t.Run("happens before", func(t *testing.T) {
		fv1 := NewFileVersion(
			"test.go",
			[]byte("content1"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{"session-1": 1},
		)

		fv2 := NewFileVersion(
			"test.go",
			[]byte("content2"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{"session-1": 2},
		)

		if !fv1.HappensBefore(&fv2) {
			t.Error("expected HappensBefore true")
		}
	})

	t.Run("nil comparison", func(t *testing.T) {
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if fv.HappensBefore(nil) {
			t.Error("expected HappensBefore false for nil")
		}
	})
}

func TestFileVersion_Concurrent(t *testing.T) {
	t.Run("concurrent versions", func(t *testing.T) {
		fv1 := NewFileVersion(
			"test.go",
			[]byte("content1"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{"session-1": 2, "session-2": 1},
		)

		fv2 := NewFileVersion(
			"test.go",
			[]byte("content2"),
			nil,
			nil,
			"pipeline-1",
			"session-2",
			VectorClock{"session-1": 1, "session-2": 2},
		)

		if !fv1.Concurrent(&fv2) {
			t.Error("expected Concurrent true")
		}
	})

	t.Run("nil comparison", func(t *testing.T) {
		fv := NewFileVersion(
			"test.go",
			[]byte("content"),
			nil,
			nil,
			"pipeline-1",
			"session-1",
			VectorClock{},
		)

		if fv.Concurrent(nil) {
			t.Error("expected Concurrent false for nil")
		}
	})
}

func TestFileVersion_DeterministicID(t *testing.T) {
	content := []byte("content")
	parents := []VersionID{ComputeVersionID([]byte("parent"), nil)}
	clock := VectorClock{"session-1": 1}

	fv1 := NewFileVersion("test.go", content, parents, nil, "pipeline-1", "session-1", clock)
	fv2 := NewFileVersion("test.go", content, parents, nil, "pipeline-1", "session-1", clock)

	if fv1.ID != fv2.ID {
		t.Error("same inputs should produce same ID")
	}
}

func TestFileVersion_ConcurrentAccess(t *testing.T) {
	fv := NewFileVersion(
		"test.go",
		[]byte("content"),
		[]VersionID{ComputeVersionID([]byte("parent"), nil)},
		[]OperationID{{1, 2, 3}},
		"pipeline-1",
		"session-1",
		VectorClock{"session-1": 1},
	)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fv.Clone()
			_ = fv.IsRoot()
			_ = fv.IsVariant()
			_ = fv.HasParent(VersionID{})
		}()
	}
	wg.Wait()
}
