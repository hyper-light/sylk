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
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/versioning"
)

func newSLATestScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), "sla-test", nil)
	t.Cleanup(func() {
		_ = scope.Shutdown(100*time.Millisecond, 2*time.Second)
	})
	return scope
}

func newSLATestSession(t *testing.T, name string) *versioning.SessionVFS {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sess, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:   versioning.SessionID(name),
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "session"),
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	return sess
}

type slaCaptureBus struct {
	mu        sync.Mutex
	published []*guide.Message
}

func (b *slaCaptureBus) Publish(topic string, msg *guide.Message) error {
	if topic != AuditRejectionSLABreachTopic {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, msg)
	return nil
}

func (b *slaCaptureBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *slaCaptureBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *slaCaptureBus) Close() error { return nil }

func (b *slaCaptureBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

// TestAuditRejectionSLATracker_EmitsBreach verifies the tracker
// escalates a rejection that has been pending beyond the SLA
// threshold.
func TestAuditRejectionSLATracker_EmitsBreach(t *testing.T) {
	sess := newSLATestSession(t, "sess-sla")
	defer sess.Close()

	bus := &slaCaptureBus{}
	tracker := NewAuditRejectionSLATracker(AuditRejectionSLATrackerConfig{
		Session:   sess,
		Bus:       bus,
		Threshold: 150 * time.Millisecond,
		Poll:      25 * time.Millisecond,
		Scope:     newSLATestScope(t),
	})
	if err := tracker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tracker.Stop()

	// Enqueue + reject a merge.
	desc := versioning.MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   versioning.SemanticVersion{Minor: 1},
		MergedVersion: versioning.SemanticVersion{Minor: 2},
	}
	if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkRejected(desc.MergedVersion, "r", "bad", nil); err != nil {
		t.Fatal(err)
	}

	// Wait for the SLA to breach.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.count() > 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if bus.count() == 0 {
		t.Fatal("tracker did not emit breach within deadline")
	}

	// Verify the payload.
	bus.mu.Lock()
	msg := bus.published[0]
	bus.mu.Unlock()
	raw, ok := msg.Payload.([]byte)
	if !ok {
		t.Fatalf("payload type = %T", msg.Payload)
	}
	var notif AuditRejectionSLABreachNotification
	if err := json.Unmarshal(raw, &notif); err != nil {
		t.Fatal(err)
	}
	if notif.RejectedVersion != desc.MergedVersion {
		t.Errorf("RejectedVersion = %v, want %v", notif.RejectedVersion, desc.MergedVersion)
	}
	if notif.ElapsedSeconds < notif.ThresholdSeconds {
		t.Errorf("elapsed (%v) should be >= threshold (%v)", notif.ElapsedSeconds, notif.ThresholdSeconds)
	}
}

// TestAuditRejectionSLATracker_ClearsOnSupersession verifies a mark
// is dropped when its entry transitions out of Rejected state (e.g.
// supersession) — preventing spurious escalations after remediation.
func TestAuditRejectionSLATracker_ClearsOnSupersession(t *testing.T) {
	sess := newSLATestSession(t, "sess-sla-sup")
	defer sess.Close()

	bus := &slaCaptureBus{}
	tracker := NewAuditRejectionSLATracker(AuditRejectionSLATrackerConfig{
		Session:   sess,
		Bus:       bus,
		Threshold: 500 * time.Millisecond,
		Poll:      25 * time.Millisecond,
		Scope:     newSLATestScope(t),
	})
	if err := tracker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tracker.Stop()

	rejected := versioning.MergeDescriptor{
		PipelineID:    "p1",
		BaseVersion:   versioning.SemanticVersion{Minor: 1},
		MergedVersion: versioning.SemanticVersion{Minor: 2},
	}
	supersedor := versioning.MergeDescriptor{
		PipelineID:    "p2",
		BaseVersion:   versioning.SemanticVersion{Minor: 2},
		MergedVersion: versioning.SemanticVersion{Minor: 3},
	}
	if _, err := sess.CommitQueue().Enqueue(rejected); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkRejected(rejected.MergedVersion, "r", "bad", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.CommitQueue().Enqueue(supersedor); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkAccepted(supersedor.MergedVersion, "r2"); err != nil {
		t.Fatal(err)
	}
	// Supersede well before the threshold.
	time.Sleep(100 * time.Millisecond)
	if err := sess.CommitQueue().MarkSuperseded(rejected.MergedVersion, supersedor.MergedVersion); err != nil {
		t.Fatal(err)
	}

	// Wait past the SLA window — no breach should fire.
	time.Sleep(700 * time.Millisecond)
	if bus.count() != 0 {
		t.Errorf("tracker emitted breach after supersession: %d events", bus.count())
	}
}
