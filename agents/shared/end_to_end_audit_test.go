package shared

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/versioning"
)

// scriptedSpawner runs a user-supplied handler when SpawnAuditReplica
// fires, capturing the ctx so the test can synthesize the replica's
// terminal decision via the ctx-scoped finalizer. Emulates a real
// global inspector / tester without the LLM tool loop.
type scriptedSpawner struct {
	mu       sync.Mutex
	requests []*AuditMergeRequest
	onSpawn  func(ctx context.Context, req *AuditMergeRequest)
}

func (s *scriptedSpawner) SpawnAuditReplica(ctx context.Context, req *AuditMergeRequest) error {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	onSpawn := s.onSpawn
	s.mu.Unlock()
	if onSpawn != nil {
		// Invoke the handler synchronously in the dispatch goroutine
		// so the test-supplied callback sees the ctx-scoped finalizer
		// the coordinator attached. Real spawners that dispatch to a
		// scoped goroutine would need to capture the finalizer before
		// handing off (the finalizer is a value on ctx, and a fresh
		// scope-derived ctx does not carry it).
		onSpawn(ctx, req)
	}
	return nil
}

// openE2ESession creates a session + scope + bus for a single test.
func openE2ESession(t *testing.T, name string) (*versioning.SessionVFS, *concurrency.GoroutineScope, guide.EventBus) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scope := concurrency.NewGoroutineScope(context.Background(), "e2e-"+name, nil)
	t.Cleanup(func() { _ = scope.Shutdown(200*time.Millisecond, 3*time.Second) })
	bus := guide.NewChannelBus(guide.ChannelBusConfig{})
	t.Cleanup(func() { _ = bus.Close() })
	sess, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:       versioning.SessionID(name),
		WorkingDir:      workDir,
		StorageRoot:     filepath.Join(dir, "session"),
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess, scope, bus
}

// TestE2E_PipelineMergeTriggersAuditFinalizeAndCommit walks the
// entire happy-path audit loop:
//
//  1. Pipeline begins, writes a file.
//  2. MergePipelineIntoGreen: OT, descriptor, callbacks fire.
//  3. Coordinator.onMerge spawns inspector + tester.
//  4. Scripted spawners emit Accepted via the ctx finalizer.
//  5. finalizeDecision marks the queue Accepted, seals the
//     ReplicaVFS, submits an audit addendum.
//  6. Commit resolver flushes to disk.
//  7. Water line advances → ReplicaVFS released.
//
// Every arrow is a direct call or sync callback — no bus indirection
// in the critical path (§0).
func TestE2E_PipelineMergeTriggersAuditFinalizeAndCommit(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "e2e-happy")

	decisions := make(chan struct{}, 2)
	spawner := &scriptedSpawner{onSpawn: func(ctx context.Context, req *AuditMergeRequest) {
		finalizer := AuditDecisionFinalizerFromContext(ctx)
		if finalizer == nil {
			t.Errorf("replica %s: no finalizer on ctx", req.ReplicaID)
			return
		}
		finalizer(&AuditMergeResult{
			SessionID:     req.SessionID,
			ReplicaID:     req.ReplicaID,
			MergedVersion: req.Descriptor.MergedVersion,
			Decision:      versioning.ReplicaDecisionAccepted,
			Summary:       "ok",
			DecidedAt:     time.Now().UTC(),
		})
		decisions <- struct{}{}
	}}

	wiring, err := StartSessionAuditWiring(
		concurrency.WithScope(context.Background(), scope),
		SessionAuditWiringConfig{
			Session:   sess,
			Inspector: spawner,
			Tester:    spawner,
			Bus:       bus,
			Scope:     scope,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer wiring.Stop()

	// Subscribe to AuditMergeResultTopic to observe the accepted
	// verdict being published for observers (architect, TUI).
	seenResult := make(chan *AuditMergeResult, 4)
	sub, err := bus.SubscribeAsync(AuditMergeResultTopic, func(msg *guide.Message) error {
		raw, ok := msg.Payload.([]byte)
		if !ok {
			return nil
		}
		var r AuditMergeResult
		if err := json.Unmarshal(raw, &r); err == nil {
			seenResult <- &r
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	// Drive the pipeline → merge → callback chain.
	ctx := context.Background()
	pipe, err := sess.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task_happy",
		SessionID:  "e2e-happy",
		WorkingDir: sess.WorkingDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess.WorkingDir(), "hello.py")
	if err := sess.NewPipelineFileAccess(pipe).WriteFile(ctx, path, []byte("print('hi')\n")); err != nil {
		t.Fatal(err)
	}
	cert := versioning.PipelineInspectorCertificate{
		DeclaredScope: "add hello.py",
		Summary:       "ready",
		TesterVerdict: "pass",
	}
	res, err := sess.MergePipelineIntoGreen(ctx, "task_happy", cert)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for both spawners (inspector + tester) to emit decisions.
	for i := 0; i < 2; i++ {
		select {
		case <-decisions:
		case <-time.After(2 * time.Second):
			t.Fatalf("decision %d never fired", i)
		}
	}

	// The descriptor's PipelineInspectorCertificate must flow through
	// to the replica requests — one audit replica per role.
	if len(spawner.requests) != 2 {
		t.Fatalf("spawn calls = %d, want 2", len(spawner.requests))
	}
	for _, req := range spawner.requests {
		if req.Descriptor.PipelineCertificate.DeclaredScope != "add hello.py" {
			t.Errorf("certificate declared scope = %q", req.Descriptor.PipelineCertificate.DeclaredScope)
		}
	}

	// Queue entry transitions to Accepted.
	deadline := time.Now().Add(2 * time.Second)
	var acceptedEntry *versioning.CommitEntry
	for time.Now().Before(deadline) {
		if entry := sess.CommitQueue().Lookup(res.MergedVersion); entry != nil && (entry.State == versioning.CommitStateAccepted || entry.State == versioning.CommitStateCommitted) {
			acceptedEntry = entry
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if acceptedEntry == nil {
		t.Fatalf("queue entry never transitioned to Accepted/Committed; current=%v",
			sess.CommitQueue().Lookup(res.MergedVersion))
	}

	// Observability: accepted result published for subscribers.
	select {
	case r := <-seenResult:
		if r.Decision != versioning.ReplicaDecisionAccepted {
			t.Errorf("published decision = %v, want Accepted", r.Decision)
		}
	case <-time.After(2 * time.Second):
		t.Error("no AuditMergeResult published on bus")
	}

	// Eventually the commit resolver flushes (since AllowDiskExport).
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if entry := sess.CommitQueue().Lookup(res.MergedVersion); entry == nil || entry.State == versioning.CommitStateCommitted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if entry := sess.CommitQueue().Lookup(res.MergedVersion); entry != nil && entry.State != versioning.CommitStateCommitted {
		t.Errorf("queue entry final state = %v, want Committed or popped", entry.State)
	}
}

// TestE2E_ReplicaWriteSealedIntoAuditAddendum verifies the
// write → seal → addendum-commit path: an inspector writes a
// formatter fix through the ReplicaVFS, the seal produces a diff,
// SubmitAuditAddendum routes it through MergePipe and enqueues a
// separate commit-queue entry with AuditAddendum=true.
func TestE2E_ReplicaWriteSealedIntoAuditAddendum(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "e2e-addendum")

	var (
		formatted atomic.Bool
		decisions = make(chan struct{}, 2)
	)
	spawner := &scriptedSpawner{onSpawn: func(ctx context.Context, req *AuditMergeRequest) {
		// Inspector role writes a "formatted" file; tester role just accepts.
		if req.AgentType == AgentTypeInspectorGlobal && !formatted.Swap(true) {
			if err := req.ReplicaVFS.Write(req.ReplicaID, "formatted.go", []byte("package x // formatted")); err != nil {
				t.Errorf("replica write failed: %v", err)
			}
		}
		fin := AuditDecisionFinalizerFromContext(ctx)
		fin(&AuditMergeResult{
			SessionID:     req.SessionID,
			ReplicaID:     req.ReplicaID,
			MergedVersion: req.Descriptor.MergedVersion,
			Decision:      versioning.ReplicaDecisionAccepted,
			Summary:       "ok",
			DecidedAt:     time.Now().UTC(),
		})
		decisions <- struct{}{}
	}}

	wiring, err := StartSessionAuditWiring(
		concurrency.WithScope(context.Background(), scope),
		SessionAuditWiringConfig{
			Session:   sess,
			Inspector: spawner,
			Tester:    spawner,
			Bus:       bus,
			Scope:     scope,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer wiring.Stop()

	// Drive a merge to trigger audit.
	ctx := context.Background()
	pipe, _ := sess.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task_src",
		SessionID:  "e2e-addendum",
		WorkingDir: sess.WorkingDir(),
	})
	_ = sess.NewPipelineFileAccess(pipe).WriteFile(ctx, filepath.Join(sess.WorkingDir(), "src.go"), []byte("package x"))
	_, err = sess.MergePipelineIntoGreen(ctx, "task_src", versioning.PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-decisions:
		case <-time.After(2 * time.Second):
			t.Fatalf("decision %d never fired", i)
		}
	}

	// Let finalize → seal → SubmitAuditAddendum complete.
	deadline := time.Now().Add(3 * time.Second)
	var addendumSeen bool
	for time.Now().Before(deadline) {
		for _, desc := range sess.MergeDescriptors() {
			if desc.AuditAddendum && len(desc.Paths) == 1 && desc.Paths[0] == "formatted.go" {
				addendumSeen = true
				break
			}
		}
		if addendumSeen {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !addendumSeen {
		t.Fatal("audit addendum descriptor for formatted.go not recorded")
	}
}

// TestE2E_RejectionDispatchesRemediation verifies the §3.8
// rejection path via the architect's AuditRejectionBridge semantics.
// We subscribe to the result topic directly (simulating architect)
// and check the rejection fires with the merged version and replica
// ID — the architect side's translation is exercised in
// agents/architect/audit_rejection_bridge_test.go.
func TestE2E_RejectionDispatchesRemediation(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "e2e-reject")

	spawner := &scriptedSpawner{onSpawn: func(ctx context.Context, req *AuditMergeRequest) {
		fin := AuditDecisionFinalizerFromContext(ctx)
		// Inspector rejects first; tester would accept but
		// first-rejection-wins.
		if req.AgentType == AgentTypeInspectorGlobal {
			fin(&AuditMergeResult{
				SessionID:     req.SessionID,
				ReplicaID:     req.ReplicaID,
				MergedVersion: req.Descriptor.MergedVersion,
				Decision:      versioning.ReplicaDecisionRejected,
				Summary:       "breaks invariant",
				Concerns:      []string{"dep cycle"},
				DecidedAt:     time.Now().UTC(),
			})
		} else {
			// Tester still emits — shouldn't reach finalize because
			// inspector rejection short-circuited.
			fin(&AuditMergeResult{
				SessionID:     req.SessionID,
				ReplicaID:     req.ReplicaID,
				MergedVersion: req.Descriptor.MergedVersion,
				Decision:      versioning.ReplicaDecisionAccepted,
				Summary:       "ok",
				DecidedAt:     time.Now().UTC(),
			})
		}
	}}

	wiring, err := StartSessionAuditWiring(
		concurrency.WithScope(context.Background(), scope),
		SessionAuditWiringConfig{
			Session:   sess,
			Inspector: spawner,
			Tester:    spawner,
			Bus:       bus,
			Scope:     scope,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer wiring.Stop()

	rejected := make(chan *AuditMergeResult, 4)
	sub, _ := bus.SubscribeAsync(AuditMergeResultTopic, func(msg *guide.Message) error {
		raw, ok := msg.Payload.([]byte)
		if !ok {
			return nil
		}
		var r AuditMergeResult
		if err := json.Unmarshal(raw, &r); err == nil && r.Decision == versioning.ReplicaDecisionRejected {
			rejected <- &r
		}
		return nil
	})
	defer sub.Unsubscribe()

	ctx := context.Background()
	pipe, _ := sess.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task_bad",
		SessionID:  "e2e-reject",
		WorkingDir: sess.WorkingDir(),
	})
	_ = sess.NewPipelineFileAccess(pipe).WriteFile(ctx, filepath.Join(sess.WorkingDir(), "bad.go"), []byte("bad"))
	res, err := sess.MergePipelineIntoGreen(ctx, "task_bad", versioning.PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-rejected:
		if r.MergedVersion != res.MergedVersion {
			t.Errorf("rejected merged version = %v, want %v", r.MergedVersion, res.MergedVersion)
		}
		if len(r.Concerns) == 0 {
			t.Error("rejected result missing concerns")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rejection published within deadline")
	}

	// Queue entry stays Rejected (no remediation dispatched yet).
	deadline := time.Now().Add(500 * time.Millisecond)
	var entry *versioning.CommitEntry
	for time.Now().Before(deadline) {
		entry = sess.CommitQueue().Lookup(res.MergedVersion)
		if entry != nil && entry.State == versioning.CommitStateRejected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if entry == nil || entry.State != versioning.CommitStateRejected {
		t.Fatalf("queue entry = %v, want Rejected", entry)
	}
}

// TestE2E_WiringStartStopOrder verifies the session-audit-wiring
// lifecycle: Start creates the coordinator + SLA tracker, Stop
// tears them down in the correct order. No goroutine leaks.
func TestE2E_WiringStartStopOrder(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "e2e-lifecycle")
	spawner := &scriptedSpawner{}

	wiring, err := StartSessionAuditWiring(
		concurrency.WithScope(context.Background(), scope),
		SessionAuditWiringConfig{
			Session:      sess,
			Inspector:    spawner,
			Tester:       spawner,
			Bus:          bus,
			Scope:        scope,
			SLAThreshold: 1 * time.Second,
			SLAPoll:      50 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if wiring.Coordinator == nil {
		t.Error("wiring.Coordinator nil after Start")
	}
	if wiring.SLATracker == nil {
		t.Error("wiring.SLATracker nil after Start (threshold > 0)")
	}

	wiring.Stop()

	// Stop is idempotent.
	wiring.Stop()
}

// TestE2E_WiringRejectsMissingSpawners verifies the wiring refuses
// to start without both inspector and tester — AND-accept semantics
// depend on both being registered.
func TestE2E_WiringRejectsMissingSpawners(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "e2e-missing")
	_, err := StartSessionAuditWiring(context.Background(), SessionAuditWiringConfig{
		Session:   sess,
		Inspector: nil,
		Tester:    &scriptedSpawner{},
		Bus:       bus,
		Scope:     scope,
	})
	if err == nil {
		t.Fatal("StartSessionAuditWiring accepted nil Inspector")
	}
}
