package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

func newCoordinationTestService(t *testing.T) *CoordinationService {
	t.Helper()
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	svc, err := NewCoordinationService(store, DefaultCoordinationServiceConfig())
	if err != nil {
		t.Fatalf("NewCoordinationService: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

func TestCoordinationService_ClaimConflict(t *testing.T) {
	svc := newCoordinationTestService(t)
	ctx := context.Background()

	_, err := svc.ClaimScope(ctx, coordination.Actor{AgentID: "a1", AgentType: "engineer"}, coordination.ClaimScopeInput{
		TaskID:    "task-1",
		TaskName:  "Auth Checkout",
		ScopeKind: coordination.ScopeKindFile,
		ScopeKey:  "pkg/auth/middleware.go",
	})
	if err != nil {
		t.Fatalf("ClaimScope(first): %v", err)
	}

	_, err = svc.ClaimScope(ctx, coordination.Actor{AgentID: "a2", AgentType: "designer"}, coordination.ClaimScopeInput{
		TaskID:    "task-1",
		ScopeKind: coordination.ScopeKindFile,
		ScopeKey:  "pkg/auth/middleware.go",
	})
	if err == nil {
		t.Fatal("ClaimScope(second) succeeded, want conflict")
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("ClaimScope(second) error = %v, want conflict", err)
	}
}

func TestCoordinationService_QueryViewBuildsWorkerPacket(t *testing.T) {
	svc := newCoordinationTestService(t)
	ctx := context.Background()

	artifact, err := svc.PublishArtifact(ctx, coordination.Actor{AgentID: "ins-1", AgentType: "inspector-pipeline"}, coordination.PublishArtifactInput{
		TaskID:    "task-2",
		TaskName:  "Checkout Error State",
		Kind:      "risk_map",
		Summary:   "Auth middleware fails closed on missing session",
		ScopeKind: coordination.ScopeKindInvariant,
		ScopeKey:  "no-unauthenticated-write",
	})
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}
	if _, err := svc.RequestReview(ctx, coordination.Actor{AgentID: "ins-1", AgentType: "inspector-pipeline"}, coordination.RequestReviewInput{
		TaskID:       "task-2",
		ArtifactID:   artifact.ID,
		ReviewerType: "engineer",
		Summary:      "Confirm implementation plan covers this invariant",
	}); err != nil {
		t.Fatalf("RequestReview: %v", err)
	}

	result, err := svc.QueryView(ctx, coordination.QueryViewInput{
		TaskID:     "task-2",
		TaskName:   "Checkout Error State",
		WorkerType: "engineer",
	})
	if err != nil {
		t.Fatalf("QueryView: %v", err)
	}
	if result.Packet == nil {
		t.Fatal("QueryView packet = nil")
	}
	if len(result.Packet.RelevantArtifacts) != 1 {
		t.Fatalf("RelevantArtifacts = %d, want 1", len(result.Packet.RelevantArtifacts))
	}
	if len(result.Packet.PendingReviews) != 1 {
		t.Fatalf("PendingReviews = %d, want 1", len(result.Packet.PendingReviews))
	}
	if result.Packet.Contract == nil || result.Packet.Contract.MinimumClaims != 1 {
		t.Fatalf("WorkerPacket contract = %#v, want minimum claim contract", result.Packet.Contract)
	}
}

func TestCoordinationService_WatchUpdatesNotifiesOnArtifactPublish(t *testing.T) {
	svc := newCoordinationTestService(t)
	ctx := context.Background()

	initial, err := svc.QueryView(ctx, coordination.QueryViewInput{
		TaskID:     "task-3",
		TaskName:   "Payment Failure Recovery",
		WorkerType: "engineer",
	})
	if err != nil {
		t.Fatalf("QueryView(initial): %v", err)
	}

	done := make(chan *coordination.WatchUpdatesResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := svc.WatchUpdates(ctx, coordination.WatchUpdatesInput{
			TaskID:       "task-3",
			TaskName:     "Payment Failure Recovery",
			WorkerType:   "engineer",
			AfterVersion: initial.View.Version,
			WaitSeconds:  2,
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- res
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := svc.PublishArtifact(ctx, coordination.Actor{AgentID: "ins-1", AgentType: "inspector-pipeline"}, coordination.PublishArtifactInput{
		TaskID:    "task-3",
		TaskName:  "Payment Failure Recovery",
		Kind:      "risk_map",
		Summary:   "Retry loop must preserve idempotency",
		ScopeKind: coordination.ScopeKindInvariant,
		ScopeKey:  "idempotent-retry",
	}); err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("WatchUpdates: %v", err)
	case res := <-done:
		if !res.HasChanges {
			t.Fatalf("WatchUpdates HasChanges = false, want true")
		}
		if len(res.Packet.RelevantArtifacts) == 0 {
			t.Fatalf("WatchUpdates packet missing artifacts: %#v", res.Packet)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WatchUpdates timed out")
	}
}

func TestCoordinationService_ValidateWorkerCompletion(t *testing.T) {
	svc := newCoordinationTestService(t)
	ctx := context.Background()

	if err := svc.ValidateWorkerCompletion(ctx, "task-4", "tester-pipeline", "tester-1"); err == nil {
		t.Fatal("ValidateWorkerCompletion without coordination data succeeded, want failure")
	}

	if _, err := svc.ClaimScope(ctx, coordination.Actor{AgentID: "tester-1", AgentType: "tester-pipeline"}, coordination.ClaimScopeInput{
		TaskID:    "task-4",
		TaskName:  "Checkout Error State",
		ScopeKind: coordination.ScopeKindTestSurface,
		ScopeKey:  "checkout-error-state",
	}); err != nil {
		t.Fatalf("ClaimScope: %v", err)
	}
	if _, err := svc.PublishArtifact(ctx, coordination.Actor{AgentID: "tester-1", AgentType: "tester-pipeline"}, coordination.PublishArtifactInput{
		TaskID:    "task-4",
		TaskName:  "Checkout Error State",
		Kind:      "verification_result",
		Summary:   "Visual regression found in checkout error banner",
		ScopeKind: coordination.ScopeKindTestSurface,
		ScopeKey:  "checkout-error-state",
	}); err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}

	if err := svc.ValidateWorkerCompletion(ctx, "task-4", "tester-pipeline", "tester-1"); err != nil {
		t.Fatalf("ValidateWorkerCompletion(after coordination): %v", err)
	}
}
