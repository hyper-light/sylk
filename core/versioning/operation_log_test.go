package versioning

import (
	"sync"
	"testing"
)

func TestNewMemoryOperationLog(t *testing.T) {
	log := NewMemoryOperationLog()
	if log == nil {
		t.Fatal("expected non-nil log")
	}
	if log.Count() != 0 {
		t.Errorf("expected 0 operations, got %d", log.Count())
	}
}

func TestMemoryOperationLog_Append(t *testing.T) {
	t.Run("appends operation", func(t *testing.T) {
		log := NewMemoryOperationLog()
		op := createTestOperation("test.go", "v1")

		err := log.Append(op)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if log.Count() != 1 {
			t.Errorf("expected 1 operation, got %d", log.Count())
		}
	})

	t.Run("clones operation", func(t *testing.T) {
		log := NewMemoryOperationLog()
		op := createTestOperation("test.go", "v1")

		log.Append(op)
		op.FilePath = "modified.go"

		retrieved, _ := log.Get(op.ID)
		if retrieved.FilePath == "modified.go" {
			t.Error("operation should be cloned")
		}
	})
}

func TestMemoryOperationLog_Get(t *testing.T) {
	t.Run("retrieves operation by ID", func(t *testing.T) {
		log := NewMemoryOperationLog()
		op := createTestOperation("test.go", "v1")
		log.Append(op)

		retrieved, err := log.Get(op.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if retrieved.ID != op.ID {
			t.Error("ID mismatch")
		}
	})

	t.Run("returns error for missing operation", func(t *testing.T) {
		log := NewMemoryOperationLog()
		var missingID OperationID

		_, err := log.Get(missingID)
		if err != ErrOperationNotFound {
			t.Errorf("expected ErrOperationNotFound, got %v", err)
		}
	})

	t.Run("returns clone", func(t *testing.T) {
		log := NewMemoryOperationLog()
		op := createTestOperation("test.go", "v1")
		log.Append(op)

		retrieved, _ := log.Get(op.ID)
		retrieved.FilePath = "modified.go"

		retrieved2, _ := log.Get(op.ID)
		if retrieved2.FilePath == "modified.go" {
			t.Error("Get should return a clone")
		}
	})
}

func TestMemoryOperationLog_GetByVersion(t *testing.T) {
	t.Run("retrieves operations by base version", func(t *testing.T) {
		log := NewMemoryOperationLog()
		baseVersion := ComputeVersionID([]byte("base"), nil)

		op1 := createTestOperationWithBase("test1.go", baseVersion)
		op2 := createTestOperationWithBase("test2.go", baseVersion)
		op3 := createTestOperationWithBase("test3.go", ComputeVersionID([]byte("other"), nil))

		log.Append(op1)
		log.Append(op2)
		log.Append(op3)

		ops, err := log.GetByVersion(baseVersion)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ops) != 2 {
			t.Errorf("expected 2 operations, got %d", len(ops))
		}
	})

	t.Run("returns empty for no matches", func(t *testing.T) {
		log := NewMemoryOperationLog()
		var missingVersion VersionID

		ops, err := log.GetByVersion(missingVersion)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ops) != 0 {
			t.Errorf("expected 0 operations, got %d", len(ops))
		}
	})
}

func TestMemoryOperationLog_GetByFile(t *testing.T) {
	t.Run("retrieves operations by file path", func(t *testing.T) {
		log := NewMemoryOperationLog()
		log.Append(createTestOperation("test.go", "v1"))
		log.Append(createTestOperation("test.go", "v2"))
		log.Append(createTestOperation("other.go", "v1"))

		ops, err := log.GetByFile("test.go", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ops) != 2 {
			t.Errorf("expected 2 operations, got %d", len(ops))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		log := NewMemoryOperationLog()
		log.Append(createTestOperation("test.go", "v1"))
		log.Append(createTestOperation("test.go", "v2"))
		log.Append(createTestOperation("test.go", "v3"))

		ops, _ := log.GetByFile("test.go", 2)
		if len(ops) != 2 {
			t.Errorf("expected 2 operations, got %d", len(ops))
		}
	})

	t.Run("returns newest first", func(t *testing.T) {
		log := NewMemoryOperationLog()
		log.Append(createTestOperation("test.go", "v1"))
		log.Append(createTestOperation("test.go", "v2"))
		log.Append(createTestOperation("test.go", "v3"))

		ops, _ := log.GetByFile("test.go", 1)
		if string(ops[0].Content) != "content-v3" {
			t.Error("expected newest operation first")
		}
	})
}

func TestMemoryOperationLog_GetSince(t *testing.T) {
	t.Run("retrieves operations after version", func(t *testing.T) {
		log := NewMemoryOperationLog()
		baseVersion := ComputeVersionID([]byte("base"), nil)

		op1 := createTestOperationWithBase("test.go", baseVersion)
		log.Append(op1)
		log.Append(createTestOperation("test.go", "v2"))
		log.Append(createTestOperation("test.go", "v3"))

		ops, err := log.GetSince(baseVersion, "test.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ops) != 2 {
			t.Errorf("expected 2 operations, got %d", len(ops))
		}
	})
}

func TestMemoryOperationLog_Clear(t *testing.T) {
	log := NewMemoryOperationLog()
	log.Append(createTestOperation("test.go", "v1"))
	log.Append(createTestOperation("test.go", "v2"))

	log.Clear()

	if log.Count() != 0 {
		t.Errorf("expected 0 after clear, got %d", log.Count())
	}
}

func TestMemoryOperationLog_ConcurrentAccess(t *testing.T) {
	log := NewMemoryOperationLog()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			op := createTestOperation("test.go", string(rune('a'+n%26)))
			log.Append(op)
			log.Get(op.ID)
			log.Count()
		}(i)
	}

	wg.Wait()
}

func TestOperationLogInterface(t *testing.T) {
	var _ OperationLog = (*MemoryOperationLog)(nil)
}

func createTestOperation(filePath, version string) Operation {
	return NewOperation(
		ComputeVersionID([]byte(version), nil),
		filePath,
		NewOffsetTarget(0, 100),
		OpInsert,
		[]byte("content-"+version),
		nil,
		"pipeline-1",
		"session-1",
		"agent-1",
		VectorClock{},
	)
}

func createTestOperationWithBase(filePath string, baseVersion VersionID) Operation {
	return NewOperation(
		baseVersion,
		filePath,
		NewOffsetTarget(0, 100),
		OpInsert,
		[]byte("content"),
		nil,
		"pipeline-1",
		"session-1",
		"agent-1",
		VectorClock{},
	)
}
