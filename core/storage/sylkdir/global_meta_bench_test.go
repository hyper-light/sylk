package sylkdir

import (
	"testing"
)

func BenchmarkGlobalMetaLoad(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		meta := NewGlobalMetaFromSylkDir(sd)
		if err := meta.Load(); err != nil {
			b.Fatalf("Load failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaSave(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := meta.Save(); err != nil {
			b.Fatalf("Save failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaAllocateNodeID(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := meta.AllocateNodeID(); err != nil {
			b.Fatalf("AllocateNodeID failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaAllocateNodeIDsBatch(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := meta.AllocateNodeIDs(100); err != nil {
			b.Fatalf("AllocateNodeIDs failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaAllocateSessionID(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := meta.AllocateSessionID(); err != nil {
			b.Fatalf("AllocateSessionID failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaRegisterCommit(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use unique session ID for each iteration
		sessionID := uint32(i + 1)
		if err := meta.RegisterCommit(sessionID, 1); err != nil {
			b.Fatalf("RegisterCommit failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaIsSessionCommitted(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	// Add some committed sessions
	for i := uint32(1); i <= 100; i++ {
		meta.RegisterCommit(i, 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = meta.IsSessionCommitted(50) // Check middle element
	}
}

func BenchmarkGlobalMetaWithFileLock(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		locked, err := meta.WithFileLock()
		if err != nil {
			b.Fatalf("WithFileLock failed: %v", err)
		}
		locked.Release()
	}
}
