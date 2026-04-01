package librarian

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	bleve "github.com/blevesearch/bleve/v2"
)

type fakeKnowledgeSyncWatcher struct {
	ch chan git.StatusUpdate
}

func (w *fakeKnowledgeSyncWatcher) Events() <-chan git.StatusUpdate { return w.ch }

type fakeKnowledgeSyncBackend struct {
	refreshFromDiskCount atomic.Int64
}

type fakeKnowledgeSyncWaiter struct {
	ready chan struct{}
}

func (w *fakeKnowledgeSyncWaiter) Ready() <-chan struct{} { return w.ready }

func (w *fakeKnowledgeSyncWaiter) Progress() (indexed, total int64) { return 0, 0 }

func (w *fakeKnowledgeSyncWaiter) OnProgress(func(indexed, total int64)) {}

func (b *fakeKnowledgeSyncBackend) RefreshFromDisk(context.Context) error {
	b.refreshFromDiskCount.Add(1)
	return nil
}

func (b *fakeKnowledgeSyncBackend) RefreshWithBleveStore(context.Context, *sylkdir.GlobalVersionBleveStore) error {
	b.refreshFromDiskCount.Add(1)
	return nil
}

func (b *fakeKnowledgeSyncBackend) SearchInContext(context.Context, *bleve.SearchRequest) (*bleve.SearchResult, error) {
	return &bleve.SearchResult{}, nil
}

func TestKnowledgeSyncService_InitialAndEventDrivenSync(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	defer store.Close()

	watcher := &fakeKnowledgeSyncWatcher{ch: make(chan git.StatusUpdate, 4)}
	backend := &fakeKnowledgeSyncBackend{}
	var runs atomic.Int64

	service, err := NewKnowledgeSyncService(KnowledgeSyncConfig{
		ProjectRoot: ".",
		Watcher:     watcher,
		Store:       store,
		Backend:     backend,
		Debounce:    10 * time.Millisecond,
		InitialSync: true,
		RunBoot: func(context.Context) (*boot.PipelineResult, error) {
			runs.Add(1)
			return &boot.PipelineResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("new knowledge sync service: %v", err)
	}
	defer service.Close()

	if err := service.Start(); err != nil {
		t.Fatalf("start knowledge sync service: %v", err)
	}

	waitForKnowledgeSyncRuns(t, &runs, 1)
	waitForAtomicValue(t, &backend.refreshFromDiskCount, 1)
	if got := backend.refreshFromDiskCount.Load(); got != 1 {
		t.Fatalf("refresh count after initial sync = %d, want 1", got)
	}
	waitForKnowledgeReadiness(t, store, knowledge.ReadinessFull)
	if got := store.Level(); got != knowledge.ReadinessFull {
		t.Fatalf("knowledge store level after initial sync = %v, want %v", got, knowledge.ReadinessFull)
	}

	watcher.ch <- git.StatusUpdate{StatusMap: map[string]git.GitFileState{"pkg/service.go": git.GitModified}}
	waitForKnowledgeSyncRuns(t, &runs, 2)
	waitForAtomicValue(t, &backend.refreshFromDiskCount, 2)
	if got := backend.refreshFromDiskCount.Load(); got != 2 {
		t.Fatalf("refresh count after watcher sync = %d, want 2", got)
	}
}

func TestKnowledgeSyncService_IgnoresDuplicateStatusSnapshots(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	defer store.Close()

	watcher := &fakeKnowledgeSyncWatcher{ch: make(chan git.StatusUpdate, 4)}
	backend := &fakeKnowledgeSyncBackend{}
	var runs atomic.Int64

	service, err := NewKnowledgeSyncService(KnowledgeSyncConfig{
		ProjectRoot: ".",
		Watcher:     watcher,
		Store:       store,
		Backend:     backend,
		Debounce:    10 * time.Millisecond,
		RunBoot: func(context.Context) (*boot.PipelineResult, error) {
			runs.Add(1)
			return &boot.PipelineResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("new knowledge sync service: %v", err)
	}
	defer service.Close()

	if err := service.Start(); err != nil {
		t.Fatalf("start knowledge sync service: %v", err)
	}

	update := git.StatusUpdate{StatusMap: map[string]git.GitFileState{"pkg/service.go": git.GitModified}}
	watcher.ch <- update
	waitForKnowledgeSyncRuns(t, &runs, 1)
	waitForAtomicValue(t, &backend.refreshFromDiskCount, 1)

	watcher.ch <- update
	time.Sleep(50 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs after duplicate update = %d, want 1", got)
	}
	if got := backend.refreshFromDiskCount.Load(); got != 1 {
		t.Fatalf("refresh count after duplicate update = %d, want 1", got)
	}

	watcher.ch <- git.StatusUpdate{StatusMap: map[string]git.GitFileState{"pkg/service.go": git.GitModified, "pkg/other.go": git.GitModified}}
	waitForKnowledgeSyncRuns(t, &runs, 2)
	waitForAtomicValue(t, &backend.refreshFromDiskCount, 2)
}

func TestKnowledgeSyncService_ReplacesPendingFullPromotionWaiter(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	defer store.Close()

	scope := concurrency.NewGoroutineScope(context.Background(), "knowledge-sync-test", nil)
	scope.SetMaxLifetime(time.Minute)
	t.Cleanup(func() {
		_ = scope.Shutdown(100*time.Millisecond, time.Second)
	})

	service := &KnowledgeSyncService{
		cfg: KnowledgeSyncConfig{
			Store:  store,
			Scope:  scope,
			Logger: slog.Default(),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	waiter1 := &fakeKnowledgeSyncWaiter{ready: make(chan struct{})}
	waiter2 := &fakeKnowledgeSyncWaiter{ready: make(chan struct{})}

	service.startFullPromotionWaiter(ctx, 1, waiter1)
	waitForScopeWorkers(t, scope, 1)

	service.gen.Store(2)
	service.startFullPromotionWaiter(ctx, 2, waiter2)
	waitForScopeWorkers(t, scope, 1)

	close(waiter1.ready)
	time.Sleep(25 * time.Millisecond)
	if store.Level() == knowledge.ReadinessFull {
		t.Fatal("stale waiter unexpectedly promoted knowledge store to full")
	}

	close(waiter2.ready)
	waitForKnowledgeReadiness(t, store, knowledge.ReadinessFull)

	service.cancelPromotionWaiter()
	waitForScopeWorkers(t, scope, 0)
}

func waitForKnowledgeSyncRuns(t *testing.T, runs *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runs.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("knowledge sync runs = %d, want at least %d", runs.Load(), want)
}

func waitForAtomicValue(t *testing.T, value *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if value.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("atomic value = %d, want at least %d", value.Load(), want)
}

func waitForKnowledgeReadiness(t *testing.T, store *knowledge.KnowledgeStore, want knowledge.ReadinessLevel) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.Level() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("knowledge store level = %v, want at least %v", store.Level(), want)
}

func waitForScopeWorkers(t *testing.T, scope *concurrency.GoroutineScope, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if scope.WorkerCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scope worker count = %d, want %d", scope.WorkerCount(), want)
}
