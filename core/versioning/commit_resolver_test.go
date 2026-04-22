package versioning

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

func newResolverTestCtx(t *testing.T, parent context.Context) context.Context {
	t.Helper()
	scope := concurrency.NewGoroutineScope(parent, "resolver-test", nil)
	t.Cleanup(func() { _ = scope.Shutdown(100*time.Millisecond, 2*time.Second) })
	return concurrency.WithScope(parent, scope)
}

// TestCommitResolver_AcceptAdvancesHeadAndFlushesDisk verifies the
// happy-path flow: pipeline merge → enqueue → mark accepted → resolver
// flushes to disk → head popped.
func TestCommitResolver_AcceptAdvancesHeadAndFlushesDisk(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "test-session",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_x",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	path := filepath.Join(dir, "out.py")
	if err := svfs.NewPipelineFileAccess(pipe).WriteFile(ctx, path, []byte("print('ok')\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res, err := svfs.MergePipelineIntoGreen(ctx, "task_x", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen: %v", err)
	}

	queue := svfs.CommitQueue()
	if queue == nil {
		t.Fatal("CommitQueue: nil")
	}
	if queue.Depth() != 1 {
		t.Fatalf("queue depth = %d, want 1", queue.Depth())
	}

	// Simulate audit replica accepting this merge.
	if err := queue.MarkAccepted(res.MergedVersion, "replica-test"); err != nil {
		t.Fatalf("MarkAccepted: %v", err)
	}

	resolver := NewCommitResolver(CommitResolverConfig{
		Session:      svfs,
		PollInterval: 10 * time.Millisecond,
	})
	rctx, cancel := context.WithTimeout(newResolverTestCtx(t, context.Background()), 2*time.Second)
	defer cancel()
	if err := resolver.Start(rctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer resolver.Stop()

	// Wait for resolver to advance the head.
	deadline := time.After(1500 * time.Millisecond)
	for queue.Depth() > 0 {
		select {
		case <-deadline:
			t.Fatalf("resolver did not advance accepted head: depth still %d", queue.Depth())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestCommitResolver_RejectedBlocksFIFO verifies that a rejected head
// prevents subsequent accepted entries from advancing.
func TestCommitResolver_RejectedBlocksFIFO(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "test-session",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	// Merge A then B.
	for _, name := range []string{"task_a", "task_b"} {
		pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
			PipelineID: name,
			SessionID:  "test-session",
			WorkingDir: dir,
		})
		if err != nil {
			t.Fatalf("BeginPipeline %s: %v", name, err)
		}
		path := filepath.Join(dir, name+".py")
		if err := svfs.NewPipelineFileAccess(pipe).WriteFile(ctx, path, []byte(name)); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		if _, err := svfs.MergePipelineIntoGreen(ctx, name, PipelineInspectorCertificate{}); err != nil {
			t.Fatalf("MergePipelineIntoGreen %s: %v", name, err)
		}
	}

	queue := svfs.CommitQueue()
	if queue.Depth() != 2 {
		t.Fatalf("queue depth = %d, want 2", queue.Depth())
	}

	descs := svfs.MergeDescriptors()
	if len(descs) != 2 {
		t.Fatalf("descriptors = %d, want 2", len(descs))
	}
	aVer := descs[0].MergedVersion
	bVer := descs[1].MergedVersion

	// A rejects; B accepts.
	if err := queue.MarkRejected(aVer, "r-a", "breaks architecture", nil); err != nil {
		t.Fatalf("MarkRejected A: %v", err)
	}
	if err := queue.MarkAccepted(bVer, "r-b"); err != nil {
		t.Fatalf("MarkAccepted B: %v", err)
	}

	resolver := NewCommitResolver(CommitResolverConfig{
		Session:      svfs,
		PollInterval: 10 * time.Millisecond,
	})
	rctx, cancel := context.WithTimeout(newResolverTestCtx(t, context.Background()), 1*time.Second)
	defer cancel()
	if err := resolver.Start(rctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer resolver.Stop()

	// Give the resolver time to realize it can't make progress.
	time.Sleep(300 * time.Millisecond)

	if queue.Depth() != 2 {
		t.Fatalf("queue depth after resolver tick = %d, want 2 (rejected head blocks)", queue.Depth())
	}
	if queue.DepthBlocked() != 1 {
		t.Fatalf("blocked depth = %d, want 1", queue.DepthBlocked())
	}
}

// TestCommitResolver_SupersessionUnblocksQueue verifies that marking a
// rejected head as superseded by an accepted later descriptor unblocks
// the queue: the resolver flushes the supersedor in the rejected slot,
// pops, and advances.
func TestCommitResolver_SupersessionUnblocksQueue(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "test-session",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	// Merge A (will be rejected).
	pipeA, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_a",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svfs.NewPipelineFileAccess(pipeA).WriteFile(ctx, filepath.Join(dir, "a.py"), []byte("a1")); err != nil {
		t.Fatal(err)
	}
	resA, err := svfs.MergePipelineIntoGreen(ctx, "task_a", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	// Merge remediation.
	pipeR, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID:      "task_a_fix",
		SessionID:       "test-session",
		WorkingDir:      dir,
		BaseCopyVersion: resA.MergedVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svfs.NewPipelineFileAccess(pipeR).WriteFile(ctx, filepath.Join(dir, "a.py"), []byte("a1-fixed")); err != nil {
		t.Fatal(err)
	}
	resR, err := svfs.MergePipelineIntoGreen(ctx, "task_a_fix", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	queue := svfs.CommitQueue()
	if queue.Depth() != 2 {
		t.Fatalf("queue depth = %d, want 2", queue.Depth())
	}

	// Reject A, accept remediation, declare supersession.
	if err := queue.MarkRejected(resA.MergedVersion, "r-a", "needs a fix", nil); err != nil {
		t.Fatal(err)
	}
	if err := queue.MarkAccepted(resR.MergedVersion, "r-r"); err != nil {
		t.Fatal(err)
	}
	if err := queue.MarkSuperseded(resA.MergedVersion, resR.MergedVersion); err != nil {
		t.Fatal(err)
	}

	resolver := NewCommitResolver(CommitResolverConfig{
		Session:      svfs,
		PollInterval: 10 * time.Millisecond,
	})
	rctx, cancel := context.WithTimeout(newResolverTestCtx(t, context.Background()), 2*time.Second)
	defer cancel()
	if err := resolver.Start(rctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer resolver.Stop()

	deadline := time.After(1500 * time.Millisecond)
	for queue.Depth() > 0 {
		select {
		case <-deadline:
			t.Fatalf("resolver stuck with depth %d (supersession should unblock)", queue.Depth())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
