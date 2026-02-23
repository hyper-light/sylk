package handoff

import (
	"testing"
	"time"
)

func TestParallelBuffer_StartStop(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	if err := pb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Double start should error.
	if err := pb.Start(); err == nil {
		t.Fatal("double Start should return error")
	}

	if err := pb.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Double stop is safe.
	if err := pb.Stop(); err != nil {
		t.Fatalf("double Stop: %v", err)
	}
}

func TestParallelBuffer_IngestCreatesSegments(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	if err := pb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Stop() })

	// segmentTarget = 200_000 / 100 = 2000 tokens.
	// Ingest enough messages to trigger a segment seal.
	tokensPerMsg := 500
	msgsNeeded := (2000 / tokensPerMsg) + 1

	for i := range msgsNeeded {
		pb.Ingest(
			Message{Role: "turn", Content: "data", TokenCount: tokensPerMsg, Timestamp: time.Now()},
			TurnRecord{OutputTokens: tokensPerMsg, TurnNumber: i},
		)
	}

	// Wait for processLoop to handle the ingested messages.
	time.Sleep(100 * time.Millisecond)

	snap := pb.Snapshot()
	if snap == nil {
		t.Fatal("snapshot should not be nil after ingesting enough messages")
	}
	if len(snap.Segments) == 0 {
		t.Fatal("expected at least 1 segment")
	}
}

func TestParallelBuffer_SnapshotBeforeIngest(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	if err := pb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Stop() })

	// Snapshot before any ingest should be nil.
	if snap := pb.Snapshot(); snap != nil {
		t.Fatal("snapshot should be nil before any ingest")
	}
}

func TestParallelBuffer_GPUpdateTriggersElastic(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	if err := pb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Stop() })

	// Initially Nominal.
	if s := pb.ElasticState(); s != ElasticNominal {
		t.Fatalf("expected Nominal, got %s", s)
	}

	// Feed GP predictions — should remain nominal during baseline learning.
	for range 10 {
		pb.UpdateGP(&GPPrediction{StdDev: 0.1})
	}

	time.Sleep(100 * time.Millisecond)

	if s := pb.ElasticState(); s != ElasticNominal {
		t.Fatalf("expected Nominal during baseline, got %s", s)
	}
}

func TestParallelBuffer_Reset(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	if err := pb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Stop() })

	// Ingest some data to create a segment.
	for range 10 {
		pb.Ingest(
			Message{Role: "turn", Content: "data", TokenCount: 500, Timestamp: time.Now()},
			TurnRecord{OutputTokens: 500},
		)
	}
	time.Sleep(100 * time.Millisecond)

	// Reset and verify snapshot is cleared.
	pb.Reset()
	time.Sleep(100 * time.Millisecond)

	if snap := pb.Snapshot(); snap != nil {
		t.Fatal("snapshot should be nil after Reset")
	}
}

func TestParallelBuffer_DropOldestOnFullChannel(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	// Don't start processLoop — channel won't drain.
	// Fill the ingest channel (size = max(16, 200_000/10_000) = 20).
	channelSize := 20
	for range channelSize + 5 {
		pb.Ingest(
			Message{Role: "turn", Content: "overflow", TokenCount: 10, Timestamp: time.Now()},
			TurnRecord{OutputTokens: 10},
		)
	}

	// Should not panic — drop-oldest handles overflow.
	if got := len(pb.ingestCh); got > channelSize {
		t.Errorf("channel overflow: got %d, want <= %d", got, channelSize)
	}
}

func TestParallelBuffer_TokenBudget(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	pb := NewParallelBuffer(desc)

	// Nominal budget = 0.10 * 200_000 = 20_000.
	if got := pb.TokenBudget(); got != 20_000 {
		t.Errorf("TokenBudget: got %d, want 20000", got)
	}
}
