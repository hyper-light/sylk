package forest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

func BenchmarkPhase123AppendLedgerRecord(b *testing.B) {
	forest, _ := newTestForest(b)
	defer forest.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := forest.AppendLedgerRecord(ctx, LedgerRecord{
			SourceKind:  LedgerSourceMaintenance,
			SourceID:    fmt.Sprintf("source-%d", i),
			SourceKey:   fmt.Sprintf("bench-ledger:%d", i),
			EventKind:   "benchmark.ledger",
			SessionID:   "bench-session",
			SubjectType: "benchmark",
			SubjectID:   fmt.Sprintf("subject-%d", i),
			OccurredAt:  time.Unix(int64(i+1), 0),
		})
		if err != nil {
			b.Fatalf("append ledger: %v", err)
		}
	}
}

func BenchmarkPhase123AppendCanonicalArtifactDelta(b *testing.B) {
	forest, _ := newTestForest(b)
	defer forest.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sequence := uint64(i + 1)
		delta := claims.NewCanonicalDelta(
			claims.DeltaActionArtifactGenerated,
			"bench-session",
			"bench-board",
			sequence,
			time.Unix(int64(sequence), 0),
			claims.DegradedAgentRef("bench", "benchmark"),
			[]claims.DeltaRef{
				{Role: "claim", Type: claims.RelatedTypeClaim, ID: fmt.Sprintf("claim-%d", i)},
				{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: fmt.Sprintf("artifact-%d", i)},
			},
			nil,
			map[string]any{"artifact": map[string]any{"id": fmt.Sprintf("artifact-%d", i), "kind": "bench", "content_hash": fmt.Sprintf("sha256:%d", i)}},
		)
		if _, err := forest.AppendCanonicalDelta(ctx, delta); err != nil {
			b.Fatalf("append canonical delta: %v", err)
		}
	}
}

func BenchmarkRuntimeWorkerTaskThroughput(b *testing.B) {
	rt := newRuntime(context.Background(), nil)
	queue := make(chan struct{}, projectorBatchSize*8)
	if err := rt.RegisterQueue("bench_tasks", cap(queue)); err != nil {
		b.Fatalf("register queue: %v", err)
	}
	done := make(chan struct{})
	if err := rt.StartWorker("bench_worker", cap(queue), func(ctx context.Context) error {
		processed := 0
		for processed < b.N {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-queue:
				processed++
			}
		}
		close(done)
		<-ctx.Done()
		return nil
	}); err != nil {
		b.Fatalf("start worker: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		queue <- struct{}{}
	}
	<-done
	b.StopTimer()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		b.Fatalf("close runtime: %v", err)
	}
}
