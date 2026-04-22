package shared

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/versioning"
)

// TestSessionLifecycleWiring_FullCycle verifies the full
// session-lifecycle wiring contract:
//
//   - StartSessionAuditWiring stands up coordinator + SLA tracker.
//   - Coordinator handles merges fire → audit replicas spawn.
//   - SLA tracker polls the commit queue and emits breaches.
//   - Stop tears down in correct order: SLA first, coordinator
//     second, scope last.
//   - The owned scope is cleaned up (no leaked goroutines under
//     the race detector).
//
// This is the production lifecycle contract cmd/tui.go's hooks rely
// on. A regression here is a regression in production session
// behaviour.
func TestSessionLifecycleWiring_FullCycle(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "lifecycle-full")

	var (
		spawnCount atomic.Int32
		decisions  = make(chan struct{}, 4)
	)
	spawner := &scriptedSpawner{onSpawn: func(ctx context.Context, req *AuditMergeRequest) {
		spawnCount.Add(1)
		fin := AuditDecisionFinalizerFromContext(ctx)
		// Reject one merge to exercise the SLA path.
		if req.AgentType == AgentTypeInspectorGlobal {
			fin(&AuditMergeResult{
				SessionID:     req.SessionID,
				ReplicaID:     req.ReplicaID,
				MergedVersion: req.Descriptor.MergedVersion,
				Decision:      versioning.ReplicaDecisionRejected,
				Summary:       "nope",
				DecidedAt:     time.Now().UTC(),
			})
		} else {
			fin(&AuditMergeResult{
				SessionID:     req.SessionID,
				ReplicaID:     req.ReplicaID,
				MergedVersion: req.Descriptor.MergedVersion,
				Decision:      versioning.ReplicaDecisionAccepted,
				Summary:       "ok",
				DecidedAt:     time.Now().UTC(),
			})
		}
		decisions <- struct{}{}
	}}

	// Collect SLA breach events.
	breaches := make(chan *AuditRejectionSLABreachNotification, 4)
	breachSub, err := bus.SubscribeAsync(AuditRejectionSLABreachTopic, func(msg *guide.Message) error {
		raw, ok := msg.Payload.([]byte)
		if !ok {
			return nil
		}
		var n AuditRejectionSLABreachNotification
		if err := json.Unmarshal(raw, &n); err == nil {
			breaches <- &n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer breachSub.Unsubscribe()

	wiring, err := StartSessionAuditWiring(
		concurrency.WithScope(context.Background(), scope),
		SessionAuditWiringConfig{
			Session:      sess,
			Inspector:    spawner,
			Tester:       spawner,
			Bus:          bus,
			Scope:        scope,
			SLAThreshold: 120 * time.Millisecond,
			SLAPoll:      20 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Trigger a merge — coordinator must pick it up.
	ctx := context.Background()
	pipe, err := sess.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task_lifecycle",
		SessionID:  "lifecycle-full",
		WorkingDir: sess.WorkingDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.NewPipelineFileAccess(pipe).WriteFile(ctx, sess.WorkingDir()+"/x.go", []byte("x"))
	res, err := sess.MergePipelineIntoGreen(ctx, "task_lifecycle", versioning.PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}
	_ = res

	// Wait for both spawners to emit.
	for i := 0; i < 2; i++ {
		select {
		case <-decisions:
		case <-time.After(2 * time.Second):
			t.Fatalf("decision %d never fired", i)
		}
	}
	if spawnCount.Load() != 2 {
		t.Errorf("spawnCount = %d, want 2", spawnCount.Load())
	}

	// Wait for SLA breach (rejection stays pending; tracker fires
	// past the 120ms threshold).
	select {
	case b := <-breaches:
		if b.SessionID != "lifecycle-full" {
			t.Errorf("breach session = %q", b.SessionID)
		}
		if b.RejectedVersion != res.MergedVersion {
			t.Errorf("breach version = %v, want %v", b.RejectedVersion, res.MergedVersion)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SLA breach never fired")
	}

	// Tear down. Stop must be idempotent and not leak goroutines —
	// the race detector + the test's scope.Cleanup catches leaks.
	wiring.Stop()
	wiring.Stop()
}

// TestSessionLifecycleWiring_ResumeInFlightReplicasOnStart verifies
// that Start calls ResumeInFlightReplicas so a restarted session
// reconstructs its pending audits. Writes a Spawned lifecycle record
// before wiring starts to simulate a pre-existing in-flight replica.
func TestSessionLifecycleWiring_ResumeInFlightReplicasOnStart(t *testing.T) {
	sess, scope, bus := openE2ESession(t, "lifecycle-resume")

	// Seed state: one merge descriptor + one Spawned replica on the
	// lifecycle log. On StartSessionAuditWiring the coordinator's
	// ResumeInFlightReplicas should re-dispatch.
	desc := versioning.MergeDescriptor{
		PipelineID:    "p_resume",
		BaseVersion:   versioning.SemanticVersion{},
		MergedVersion: versioning.SemanticVersion{Minor: 1},
	}
	sess.RecordMergeDescriptorForTest(desc)
	if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
		t.Fatal(err)
	}
	inspectorID := MergeReplicaAgentID("inspector-global", "lifecycle-resume", desc.MergedVersion)
	testerID := MergeReplicaAgentID("tester-global", "lifecycle-resume", desc.MergedVersion)
	if err := sess.ReplicaLifecycleLog().RecordSpawned(inspectorID, "inspector-global", desc.MergedVersion); err != nil {
		t.Fatal(err)
	}
	if err := sess.ReplicaLifecycleLog().RecordSpawned(testerID, "tester-global", desc.MergedVersion); err != nil {
		t.Fatal(err)
	}

	spawner := &scriptedSpawner{}
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

	// Give the resume path a moment.
	time.Sleep(50 * time.Millisecond)

	// The coordinator should have re-dispatched both replicas with
	// IsReAudit=true and the original deterministic IDs.
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	if len(spawner.requests) != 2 {
		t.Fatalf("resume spawn count = %d, want 2", len(spawner.requests))
	}
	ids := map[string]bool{}
	for _, req := range spawner.requests {
		ids[req.ReplicaID] = true
		if !req.IsReAudit {
			t.Errorf("resumed replica %s missing IsReAudit flag", req.ReplicaID)
		}
	}
	if !ids[inspectorID] || !ids[testerID] {
		t.Errorf("resume missed a replica. ids=%v", ids)
	}
}
