package sylkdir

import (
	"encoding/json"
	"os"
	"testing"
	"time"
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

	sessionFinalVersion := SemanticVersion{Major: 1, Minor: 1, Patch: 0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use unique session ID for each iteration
		sessionID := uint32(i + 1)
		if err := meta.RegisterCommit(sessionID, sessionFinalVersion); err != nil {
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
	v := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	for i := uint32(1); i <= 100; i++ {
		meta.RegisterCommit(i, v)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = meta.IsSessionCommitted(50) // Check middle element
	}
}

func BenchmarkGlobalMetaBumpMinorVersion(b *testing.B) {
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
		if _, err := meta.BumpMinorVersion(); err != nil {
			b.Fatalf("BumpMinorVersion failed: %v", err)
		}
	}
}

func BenchmarkGlobalMetaBumpPatchVersion(b *testing.B) {
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
		if _, err := meta.BumpPatchVersion(); err != nil {
			b.Fatalf("BumpPatchVersion failed: %v", err)
		}
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

// BenchmarkFullCommitWorkflow measures the complete session-to-global commit
// flow including: session create → checkpoints → pre-commit → RegisterCommit
// → reload global meta → create new session with updated snapshot.
func BenchmarkFullCommitWorkflow(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		b.Fatalf("Load: %v", err)
	}

	store := NewSessionStore(sd)
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sessionID := uint32(i + 1)

		// Create session with snapshot
		snap := &BaseSnapshot{
			GlobalVersion:     gm.GetVersion(),
			CommittedSessions: []uint32{},
			SnapshotAt:        now,
			NextNodeID:        gm.GetCurrentNodeID(),
		}
		sess, err := store.Create(sessionID, snap)
		if err != nil {
			b.Fatalf("Create: %v", err)
		}

		// Simulate work: two patch checkpoints
		sess.Checkpoint("work-1", CheckpointPatch)
		sess.Checkpoint("work-2", CheckpointPatch)

		// Pre-commit minor checkpoint
		sess.Checkpoint("pre-commit", CheckpointMinor)

		// Register commit (major bump on global)
		if err := gm.RegisterCommit(sessionID, sess.Manifest.Head); err != nil {
			b.Fatalf("RegisterCommit: %v", err)
		}
	}
	b.StopTimer()

	// Verify the final global version is vN.0.0 where N = b.N
	expectedMajor := uint16(b.N)
	if gm.GetVersion().Major != expectedMajor {
		b.Errorf("final global version major = %d, want %d", gm.GetVersion().Major, expectedMajor)
	}
}

// BenchmarkCommitFileRoundTrip measures write + read-back of meta.json after
// a commit, which is the critical durability path.
func BenchmarkCommitFileRoundTrip(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init: %v", err)
	}

	sessionFinal := SemanticVersion{Major: 1, Minor: 1, Patch: 0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Write
		meta := NewGlobalMetaFromSylkDir(sd)
		if err := meta.Load(); err != nil {
			b.Fatalf("Load: %v", err)
		}
		sessionID := uint32(i + 1)
		if err := meta.RegisterCommit(sessionID, sessionFinal); err != nil {
			b.Fatalf("RegisterCommit: %v", err)
		}

		// Read back raw file and parse
		raw, err := os.ReadFile(sd.MetaPath())
		if err != nil {
			b.Fatalf("ReadFile: %v", err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			b.Fatalf("Unmarshal: %v", err)
		}
	}
}
