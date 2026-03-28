package sylkdir

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBgIndexerEnv creates a SylkDir with an open GlobalVersionBleveStore for testing.
func setupBgIndexerEnv(t *testing.T) *GlobalVersionBleveStore {
	t.Helper()
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	require.NoError(t, sd.Init())

	gm := NewGlobalMetaFromSylkDir(sd)
	require.NoError(t, gm.Load())

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	require.NoError(t, gvbs.OpenHead())
	t.Cleanup(func() { gvbs.CloseAll() })

	return gvbs
}

func TestBackgroundIndexer_EmptyDocs(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	var completeCalled atomic.Bool
	bi := NewBackgroundIndexer(gvbs, nil, nil, func() error {
		completeCalled.Store(true)
		return nil
	})
	t.Cleanup(bi.Close)

	bi.Wait()

	select {
	case <-bi.Ready():
	default:
		t.Fatal("Ready channel should be closed after Wait")
	}

	indexed, total := bi.Progress()
	assert.Equal(t, int64(0), indexed)
	assert.Equal(t, int64(0), total)
	assert.True(t, completeCalled.Load(), "onComplete should be called")
}

func TestBackgroundIndexer_IndexesAllDocs(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	docs := []*VersionDocument{
		{ID: "doc_1", Content: "package main", Path: "main.go", Language: "go"},
		{ID: "doc_2", Content: "package util", Path: "util.go", Language: "go"},
		{ID: "doc_3", Content: "package test", Path: "test.go", Language: "go"},
	}

	var completeCalled atomic.Bool
	bi := NewBackgroundIndexer(gvbs, docs, nil, func() error {
		completeCalled.Store(true)
		return nil
	})
	t.Cleanup(bi.Close)

	bi.Wait()

	indexed, total := bi.Progress()
	assert.Equal(t, int64(3), indexed)
	assert.Equal(t, int64(3), total)
	assert.True(t, completeCalled.Load())

	count, err := gvbs.DocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count)
}

func TestBackgroundIndexer_PromoteDocsDeduplication(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	docs := []*VersionDocument{
		{ID: "doc_1", Content: "package main", Path: "main.go", Language: "go"},
		{ID: "doc_2", Content: "package util", Path: "util.go", Language: "go"},
	}

	// Create indexer but immediately promote doc_1 before worker processes it.
	bi := NewBackgroundIndexer(gvbs, docs, nil, nil)
	t.Cleanup(bi.Close)

	// Promote doc_1 — this races with the worker, but deduplication guarantees
	// doc_1 is indexed exactly once regardless of who wins.
	err := bi.PromoteDocs(context.Background(), []string{"doc_1"})
	require.NoError(t, err)

	bi.Wait()

	// Both docs should be indexed exactly once.
	count, err := gvbs.DocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), count)
}

func TestBackgroundIndexer_PromoteDocsUnknownIDs(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	bi := NewBackgroundIndexer(gvbs, nil, nil, nil)
	t.Cleanup(bi.Close)

	err := bi.PromoteDocs(context.Background(), []string{"nonexistent"})
	require.NoError(t, err)

	bi.Wait()

	count, err := gvbs.DocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count)
}

func TestBackgroundIndexer_CloseStopsWorker(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	// Create many docs to ensure worker has work to do.
	docs := make([]*VersionDocument, 100)
	for i := range docs {
		docs[i] = &VersionDocument{
			ID:       "doc_" + intToStr(i),
			Content:  "package test",
			Path:     "file_" + intToStr(i) + ".go",
			Language: "go",
		}
	}

	bi := NewBackgroundIndexer(gvbs, docs, nil, nil)

	// Close immediately — worker may have indexed some docs but should stop.
	bi.Close()

	select {
	case <-bi.Ready():
	default:
		t.Fatal("Ready channel should be closed after Close")
	}
}

func TestBackgroundIndexer_ReadyChannel(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	docs := []*VersionDocument{
		{ID: "doc_1", Content: "test content", Path: "a.go", Language: "go"},
	}

	bi := NewBackgroundIndexer(gvbs, docs, nil, nil)
	t.Cleanup(bi.Close)

	select {
	case <-bi.Ready():
		// OK — worker finished
	case <-time.After(10 * time.Second):
		t.Fatal("Ready channel not closed within timeout")
	}

	indexed, _ := bi.Progress()
	assert.Equal(t, int64(1), indexed)
}

func TestBackgroundIndexer_WithScopeTracksWorker(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	scope := concurrency.NewGoroutineScope(context.Background(), "bg-indexer-test", nil)
	scope.SetMaxLifetime(time.Minute)
	t.Cleanup(func() {
		_ = scope.Shutdown(100*time.Millisecond, time.Second)
	})

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once

	bi := NewBackgroundIndexerWithScope(scope, gvbs, nil, nil, func() error {
		close(started)
		<-release
		return nil
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		bi.Close()
	})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background indexer did not reach onComplete")
	}

	assert.Equal(t, 1, scope.WorkerCount(), "background indexer should be tracked by scope")

	releaseOnce.Do(func() { close(release) })

	select {
	case <-bi.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("background indexer did not finish after release")
	}

	assert.Eventually(t, func() bool {
		return scope.WorkerCount() == 0
	}, time.Second, 10*time.Millisecond, "background indexer worker should be removed from scope")
}

func TestBackgroundIndexer_NilStore(t *testing.T) {
	bi := NewBackgroundIndexer(nil, []*VersionDocument{
		{ID: "doc_1", Content: "test", Path: "a.go"},
	}, nil, nil)
	t.Cleanup(bi.Close)

	bi.Wait()

	indexed, total := bi.Progress()
	assert.Equal(t, int64(0), indexed)
	assert.Equal(t, int64(1), total)

	// PromoteDocs on nil store should not panic.
	err := bi.PromoteDocs(context.Background(), []string{"doc_1"})
	require.NoError(t, err)
}

func TestBackgroundIndexer_SupersededDeletion(t *testing.T) {
	gvbs := setupBgIndexerEnv(t)

	// Pre-index a document that will be superseded.
	oldDoc := &search.Document{
		ID:      "old_doc",
		Path:    "main.go",
		Content: "package old",
	}
	require.NoError(t, gvbs.Index(context.Background(), oldDoc))

	countBefore, err := gvbs.DocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), countBefore)

	// Create indexer that supersedes old_doc and adds new_doc.
	newDocs := []*VersionDocument{
		{ID: "new_doc", Content: "package new", Path: "main.go", Language: "go"},
	}

	bi := NewBackgroundIndexer(gvbs, newDocs, []string{"old_doc"}, nil)
	t.Cleanup(bi.Close)

	bi.Wait()

	// old_doc deleted, new_doc indexed → count = 1.
	countAfter, err := gvbs.DocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), countAfter)

	indexed, _ := bi.Progress()
	assert.Equal(t, int64(1), indexed)
}

// intToStr converts int to string without importing strconv (avoids dependency).
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
