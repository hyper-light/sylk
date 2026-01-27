package sylkdir

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkSessionCreate measures session creation with initial structure.
func BenchmarkSessionCreate(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)
	baseSnapshot := &BaseSnapshot{
		CommittedSessions: []uint32{1, 2, 3},
		SnapshotAt:        time.Now(),
		NextNodeID:        10000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Create(uint32(i+1), baseSnapshot)
	}
}

// BenchmarkSessionLoad measures session loading from disk.
func BenchmarkSessionLoad(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)
	store.Create(1, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Load("ses_001")
	}
}

// BenchmarkSessionCheckpoint measures checkpoint creation.
func BenchmarkSessionCheckpoint(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess.Checkpoint(fmt.Sprintf("cp%d", i), "explicit")
	}
}

// BenchmarkSessionCheckout measures version checkout.
func BenchmarkSessionCheckout(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	// Create 10 versions
	for i := 0; i < 10; i++ {
		sess.Checkpoint(fmt.Sprintf("cp%d", i), "explicit")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Alternate between versions 1 and 5
		versionID := uint32((i % 2) * 4 + 1)
		sess.Checkout(versionID)
	}
}

// BenchmarkSessionGetAncestorChain measures ancestor chain computation.
func BenchmarkSessionGetAncestorChain(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	// Create 100 versions (deep history)
	for i := 0; i < 100; i++ {
		sess.Checkpoint(fmt.Sprintf("cp%d", i), "explicit")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess.GetAncestorChain()
	}
}

// BenchmarkSessionWithVersions_5 creates session with 5 versions (acceptance criteria).
func BenchmarkSessionWithVersions_5(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		sd := New(tmpDir)
		sd.Init()

		store := NewSessionStore(sd)
		sess, _ := store.Create(1, &BaseSnapshot{
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        1,
		})

		// Create 4 additional versions (total 5)
		for v := 0; v < 4; v++ {
			sess.Checkpoint(fmt.Sprintf("checkpoint-%d", v), "explicit")
		}

		// Verify structure
		if len(sess.Manifest.Versions) != 5 {
			b.Fatalf("Expected 5 versions, got %d", len(sess.Manifest.Versions))
		}
	}
}

// BenchmarkFullSessionWorkflow simulates a realistic session workflow.
// This is the benchmark that creates actual session data as requested.
func BenchmarkFullSessionWorkflow(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		sd := New(tmpDir)
		sd.Init()

		store := NewSessionStore(sd)

		// 1. Create session with base snapshot (simulating previous commits)
		baseSnapshot := &BaseSnapshot{
			CommittedSessions: []uint32{1, 2},
			SnapshotAt:        time.Now(),
			NextNodeID:        5000,
		}

		sess, err := store.Create(3, baseSnapshot)
		if err != nil {
			b.Fatalf("Create failed: %v", err)
		}

		// 2. Create checkpoints (simulating work)
		sess.Checkpoint("initial-indexing", "implicit")
		sess.Checkpoint("after-refactor", "explicit")
		sess.Checkpoint("auto-checkpoint", "auto_delta")

		// 3. Simulate writing docs to version (create batch.jsonl)
		docsPath := sess.DocsPath(sess.Manifest.Head)
		docContent := `{"id":"doc1","path":"/src/main.go","content":"package main"}
{"id":"doc2","path":"/src/util.go","content":"package util"}
{"id":"doc3","path":"/README.md","content":"# Project"}
`
		if err := os.WriteFile(filepath.Join(docsPath, "batch.jsonl"), []byte(docContent), 0644); err != nil {
			b.Fatalf("Write docs failed: %v", err)
		}

		// 4. Checkout earlier version
		sess.Checkout(2)

		// 5. Create branch
		sess.Checkpoint("branch-from-v2", "explicit")

		// 6. Get ancestor chain
		chain := sess.GetAncestorChain()
		if len(chain) != 3 { // v5 -> v2 -> v1
			b.Fatalf("Expected 3 ancestors, got %d", len(chain))
		}

		// 7. Set as active
		store.SetActive("ses_003")

		// 8. Verify final state
		if sess.VersionCount() != 5 {
			b.Fatalf("Expected 5 versions, got %d", sess.VersionCount())
		}

		// 9. Reload and verify persistence
		reloaded, _ := store.Load("ses_003")
		if reloaded.VersionCount() != 5 {
			b.Fatalf("Reloaded session has %d versions, want 5", reloaded.VersionCount())
		}
	}
}

// BenchmarkSessionStoreStats measures stats collection across sessions.
func BenchmarkSessionStoreStats(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)

	// Create 10 sessions with varying versions
	for i := uint32(1); i <= 10; i++ {
		sess, _ := store.Create(i, nil)
		for j := 0; j < int(i); j++ {
			sess.Checkpoint(fmt.Sprintf("cp%d", j), "explicit")
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Stats()
	}
}

// BenchmarkSessionList measures session listing.
func BenchmarkSessionList(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)

	// Create 50 sessions
	for i := uint32(1); i <= 50; i++ {
		store.Create(i, nil)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.List()
	}
}

// BenchmarkDeepVersionHistory measures performance with deep version history.
func BenchmarkDeepVersionHistory(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewSessionStore(sd)
	sess, _ := store.Create(1, nil)

	// Create 500 versions (deep history)
	for i := 0; i < 500; i++ {
		sess.Checkpoint("", "auto_delta")
	}

	b.ResetTimer()

	b.Run("GetAncestorChain", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sess.GetAncestorChain()
		}
	})

	b.Run("Checkout", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sess.Checkout(uint32(i%500 + 1))
		}
	})

	b.Run("ListVersions", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sess.ListVersions()
		}
	})
}
