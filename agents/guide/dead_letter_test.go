package guide

import (
	"testing"
	"time"
)

func TestDeadLetterQueue_Add(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr1"},
		Reason:  DeadLetterReasonCircuitOpen,
		Topic:   "test.topic",
	})

	if dlq.Len() != 1 {
		t.Errorf("expected 1 letter, got %d", dlq.Len())
	}
}

func TestDeadLetterQueue_AddFromMessage(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	msg := &Message{
		ID:            "msg1",
		CorrelationID: "corr1",
		SourceAgentID: "source",
		TargetAgentID: "target",
		Timestamp:     time.Now(),
	}

	dlq.AddFromMessage(msg, "test.topic", DeadLetterReasonTimeout, nil, 3)

	letters := dlq.Get(DeadLetterFilter{})
	if len(letters) != 1 {
		t.Fatalf("expected 1 letter, got %d", len(letters))
	}

	letter := letters[0]
	if letter.Reason != DeadLetterReasonTimeout {
		t.Errorf("expected reason Timeout, got %v", letter.Reason)
	}
	if letter.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", letter.Attempts)
	}
	if letter.SourceAgentID != "source" {
		t.Errorf("expected source 'source', got %s", letter.SourceAgentID)
	}
}

func TestDeadLetterQueue_MaxSize(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 3})

	for i := 0; i < 5; i++ {
		dlq.Add(&DeadLetter{
			Message: &Message{CorrelationID: string(rune('a' + i))},
			Reason:  DeadLetterReasonTimeout,
		})
	}

	// Should only have 3 (oldest evicted)
	if dlq.Len() != 3 {
		t.Errorf("expected 3 letters (max size), got %d", dlq.Len())
	}

	// The first two (a, b) should have been evicted
	letters := dlq.Get(DeadLetterFilter{})
	corrIDs := make(map[string]bool)
	for _, l := range letters {
		corrIDs[l.Message.CorrelationID] = true
	}
	if corrIDs["a"] || corrIDs["b"] {
		t.Error("expected oldest letters to be evicted")
	}
}

func TestDeadLetterQueue_GetByCorrelationID(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr1"},
		Reason:  DeadLetterReasonCircuitOpen,
	})
	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr2"},
		Reason:  DeadLetterReasonTimeout,
	})

	letter := dlq.GetByCorrelationID("corr1")
	if letter == nil {
		t.Fatal("expected to find letter")
	}
	if letter.Reason != DeadLetterReasonCircuitOpen {
		t.Errorf("expected CircuitOpen, got %v", letter.Reason)
	}

	missing := dlq.GetByCorrelationID("missing")
	if missing != nil {
		t.Error("expected nil for missing correlation ID")
	}
}

func TestDeadLetterQueue_Filter(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	now := time.Now()

	dlq.Add(&DeadLetter{
		Message:       &Message{CorrelationID: "corr1"},
		Reason:        DeadLetterReasonCircuitOpen,
		SourceAgentID: "agent1",
		TargetAgentID: "agent2",
	})
	dlq.Add(&DeadLetter{
		Message:       &Message{CorrelationID: "corr2"},
		Reason:        DeadLetterReasonTimeout,
		SourceAgentID: "agent1",
		TargetAgentID: "agent3",
	})
	dlq.Add(&DeadLetter{
		Message:       &Message{CorrelationID: "corr3"},
		Reason:        DeadLetterReasonCircuitOpen,
		SourceAgentID: "agent2",
		TargetAgentID: "agent3",
	})

	// Filter by reason
	byReason := dlq.Get(DeadLetterFilter{Reason: DeadLetterReasonCircuitOpen})
	if len(byReason) != 2 {
		t.Errorf("expected 2 circuit open letters, got %d", len(byReason))
	}

	// Filter by source
	bySource := dlq.Get(DeadLetterFilter{SourceAgentID: "agent1"})
	if len(bySource) != 2 {
		t.Errorf("expected 2 letters from agent1, got %d", len(bySource))
	}

	// Filter by target
	byTarget := dlq.Get(DeadLetterFilter{TargetAgentID: "agent3"})
	if len(byTarget) != 2 {
		t.Errorf("expected 2 letters to agent3, got %d", len(byTarget))
	}

	// Filter by time
	time.Sleep(10 * time.Millisecond)
	byTime := dlq.Get(DeadLetterFilter{Since: now.Add(-1 * time.Hour)})
	if len(byTime) != 3 {
		t.Errorf("expected 3 letters since an hour ago, got %d", len(byTime))
	}
}

func TestDeadLetterQueue_MarkRetried(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr1"},
		Reason:  DeadLetterReasonTimeout,
	})

	ok := dlq.MarkRetried("corr1")
	if !ok {
		t.Error("expected MarkRetried to return true")
	}

	letter := dlq.GetByCorrelationID("corr1")
	if !letter.Retried {
		t.Error("expected letter to be marked as retried")
	}
	if letter.RetriedAt.IsZero() {
		t.Error("expected RetriedAt to be set")
	}

	// Should be filtered out by default
	letters := dlq.Get(DeadLetterFilter{})
	if len(letters) != 0 {
		t.Error("expected retried letters to be filtered out by default")
	}

	// Include retried
	letters = dlq.Get(DeadLetterFilter{IncludeRetried: true})
	if len(letters) != 1 {
		t.Error("expected 1 letter when including retried")
	}
}

func TestDeadLetterQueue_Remove(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr1"},
		Reason:  DeadLetterReasonTimeout,
	})

	ok := dlq.Remove("corr1")
	if !ok {
		t.Error("expected Remove to return true")
	}

	if dlq.Len() != 0 {
		t.Errorf("expected 0 letters after remove, got %d", dlq.Len())
	}

	// Remove missing should return false
	ok = dlq.Remove("missing")
	if ok {
		t.Error("expected Remove to return false for missing letter")
	}
}

func TestDeadLetterQueue_Clear(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	for i := 0; i < 5; i++ {
		dlq.Add(&DeadLetter{
			Message: &Message{CorrelationID: string(rune('a' + i))},
		})
	}

	dlq.Clear()

	if dlq.Len() != 0 {
		t.Errorf("expected 0 letters after clear, got %d", dlq.Len())
	}
}

func TestDeadLetterQueue_Stats(t *testing.T) {
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{MaxSize: 100})

	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr1"},
		Reason:  DeadLetterReasonCircuitOpen,
	})
	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr2"},
		Reason:  DeadLetterReasonTimeout,
	})
	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr3"},
		Reason:  DeadLetterReasonCircuitOpen,
	})

	dlq.MarkRetried("corr1")

	stats := dlq.Stats()

	if stats.Size != 3 {
		t.Errorf("expected size 3, got %d", stats.Size)
	}
	if stats.TotalAdded != 3 {
		t.Errorf("expected total added 3, got %d", stats.TotalAdded)
	}
	if stats.TotalRetried != 1 {
		t.Errorf("expected total retried 1, got %d", stats.TotalRetried)
	}
	if stats.ByReason[DeadLetterReasonCircuitOpen] != 2 {
		t.Errorf("expected 2 circuit open, got %d", stats.ByReason[DeadLetterReasonCircuitOpen])
	}
	if stats.ByReason[DeadLetterReasonTimeout] != 1 {
		t.Errorf("expected 1 timeout, got %d", stats.ByReason[DeadLetterReasonTimeout])
	}
}

func TestDeadLetterQueue_OnAddCallback(t *testing.T) {
	called := false
	dlq := NewDeadLetterQueue(DeadLetterQueueConfig{
		MaxSize: 100,
		OnAdd: func(letter *DeadLetter) {
			called = true
		},
	})

	dlq.Add(&DeadLetter{
		Message: &Message{CorrelationID: "corr1"},
	})

	// Give callback goroutine time to run
	time.Sleep(10 * time.Millisecond)

	if !called {
		t.Error("expected OnAdd callback to be called")
	}
}
