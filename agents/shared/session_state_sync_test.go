package shared

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/versioning"
)

// resyncBus captures SessionStateResyncTopic publications.
type resyncBus struct {
	mu  sync.Mutex
	got []*guide.Message
}

func (b *resyncBus) Publish(topic string, msg *guide.Message) error {
	if topic != SessionStateResyncTopic {
		return nil
	}
	b.mu.Lock()
	b.got = append(b.got, msg)
	b.mu.Unlock()
	return nil
}
func (b *resyncBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *resyncBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *resyncBus) Close() error                            { return nil }
func (b *resyncBus) CloseWithContext(_ context.Context) error { return nil }

func openResyncTestSession(t *testing.T, name string) *versioning.SessionVFS {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:   versioning.SessionID(name),
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "session"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// TestPublishSessionStateResync_EmitsSummary verifies the resync
// notification carries the session's current commit-queue and
// replica-lifecycle state so the TUI can rebuild its view.
func TestPublishSessionStateResync_EmitsSummary(t *testing.T) {
	sess := openResyncTestSession(t, "sess-resync")
	defer sess.Close()

	// Seed commit-queue state so the resync has something to report.
	desc := versioning.MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   versioning.SemanticVersion{},
		MergedVersion: versioning.SemanticVersion{Minor: 1},
	}
	if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkRejected(desc.MergedVersion, "r", "stuck", nil); err != nil {
		t.Fatal(err)
	}

	// Let the clock accumulate a little.
	time.Sleep(30 * time.Millisecond)

	bus := &resyncBus{}
	if err := PublishSessionStateResync(bus, sess); err != nil {
		t.Fatalf("publish: %v", err)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.got) != 1 {
		t.Fatalf("publish count = %d, want 1", len(bus.got))
	}
	raw, ok := bus.got[0].Payload.([]byte)
	if !ok {
		t.Fatalf("payload = %T", bus.got[0].Payload)
	}
	var n SessionStateResyncNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	if n.SessionID != "sess-resync" {
		t.Errorf("SessionID = %q", n.SessionID)
	}
	if n.BlockedCount != 1 {
		t.Errorf("BlockedCount = %d, want 1", n.BlockedCount)
	}
	if n.CommitQueueDepth == 0 {
		t.Errorf("CommitQueueDepth = 0, expected > 0")
	}
	if n.ElapsedOpenSecs <= 0 {
		t.Errorf("ElapsedOpenSecs = %v, want > 0", n.ElapsedOpenSecs)
	}
}

// TestPublishSessionStateResync_NilBusIsNoop verifies the publisher
// tolerates nil inputs quietly (deployment paths may not have wired
// observability yet).
func TestPublishSessionStateResync_NilBusIsNoop(t *testing.T) {
	sess := openResyncTestSession(t, "sess-resync-nil")
	defer sess.Close()
	if err := PublishSessionStateResync(nil, sess); err != nil {
		t.Errorf("nil bus: %v", err)
	}
	if err := PublishSessionStateResync(&resyncBus{}, nil); err != nil {
		t.Errorf("nil session: %v", err)
	}
	_ = context.Background
}
