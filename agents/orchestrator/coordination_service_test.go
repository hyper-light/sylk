package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

func TestCoordinationService_PublishArtifactConcurrentVersionAllocation(t *testing.T) {
	svc := newCoordinationTestService(t)
	ctx := context.Background()
	actor := coordination.Actor{AgentID: "tester-1", AgentType: "tester-pipeline"}
	const publishCount = 12

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for i := 0; i < publishCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.PublishArtifact(ctx, actor, coordination.PublishArtifactInput{
				TaskID:         "task-concurrent-publish",
				TaskName:       "Concurrent Publish",
				Kind:           "verification_result",
				Summary:        fmt.Sprintf("artifact-%d", i),
				ScopeKind:      coordination.ScopeKindTestSurface,
				ScopeKey:       "tests/test_cli.py",
				IdempotencyKey: fmt.Sprintf("publish-%d", i),
			})
			if err == nil {
				return
			}
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("PublishArtifact concurrent errors = %v", errs)
	}

	artifacts, err := svc.listArtifacts(ctx, "task-concurrent-publish", true)
	if err != nil {
		t.Fatalf("listArtifacts: %v", err)
	}
	if len(artifacts) != publishCount {
		t.Fatalf("artifact count = %d, want %d", len(artifacts), publishCount)
	}

	seenVersions := make(map[int]struct{}, publishCount)
	for _, artifact := range artifacts {
		if artifact.Version < 1 || artifact.Version > publishCount {
			t.Fatalf("artifact version = %d, want within [1,%d]", artifact.Version, publishCount)
		}
		if _, exists := seenVersions[artifact.Version]; exists {
			t.Fatalf("duplicate artifact version detected: %d", artifact.Version)
		}
		seenVersions[artifact.Version] = struct{}{}
	}
}

func TestCoordinationService_PublishArtifactConcurrentIdempotentRetryCollapses(t *testing.T) {
	svc := newCoordinationTestService(t)
	ctx := context.Background()
	actor := coordination.Actor{AgentID: "tester-1", AgentType: "tester-pipeline"}
	const publishAttempts = 8

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		errs      []error
		artifacts []*coordination.Artifact
	)
	for i := 0; i < publishAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			artifact, err := svc.PublishArtifact(ctx, actor, coordination.PublishArtifactInput{
				TaskID:         "task-idempotent-publish",
				TaskName:       "Idempotent Publish",
				Kind:           "verification_result",
				Summary:        "stable publish result",
				ScopeKind:      coordination.ScopeKindTestSurface,
				ScopeKey:       "tests/test_cli.py",
				IdempotencyKey: "same-publish",
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			artifacts = append(artifacts, artifact)
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("PublishArtifact idempotent errors = %v", errs)
	}
	if len(artifacts) != publishAttempts {
		t.Fatalf("artifact results = %d, want %d", len(artifacts), publishAttempts)
	}

	first := artifacts[0]
	for _, artifact := range artifacts[1:] {
		if artifact.ID != first.ID {
			t.Fatalf("artifact IDs = %q and %q, want same persisted artifact", first.ID, artifact.ID)
		}
		if artifact.Version != first.Version {
			t.Fatalf("artifact versions = %d and %d, want same persisted artifact", first.Version, artifact.Version)
		}
	}

	persisted, err := svc.listArtifacts(ctx, "task-idempotent-publish", true)
	if err != nil {
		t.Fatalf("listArtifacts: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted artifact count = %d, want 1", len(persisted))
	}
}
