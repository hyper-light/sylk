package concurrency

import (
	"testing"
	"time"
)

func TestNewPipelinePriorityQueue(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	if pq.Len() != 0 {
		t.Errorf("got Len %d, want 0", pq.Len())
	}
}

func TestPipelinePriorityQueue_PushPop(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	p := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	pq.Push(p)

	if pq.Len() != 1 {
		t.Errorf("got Len %d, want 1", pq.Len())
	}

	popped := pq.Pop()
	if popped.ID != "p1" {
		t.Errorf("got ID %s, want p1", popped.ID)
	}

	if pq.Len() != 0 {
		t.Errorf("got Len %d, want 0", pq.Len())
	}
}

func TestPipelinePriorityQueue_PopEmpty(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	if pq.Pop() != nil {
		t.Error("Pop on empty queue should return nil")
	}
}

func TestPipelinePriorityQueue_PriorityOrdering(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	now := time.Now()
	pq.Push(&SchedulablePipeline{ID: "low", Priority: PriorityLow, SpawnTime: now})
	pq.Push(&SchedulablePipeline{ID: "critical", Priority: PriorityCritical, SpawnTime: now})
	pq.Push(&SchedulablePipeline{ID: "normal", Priority: PriorityNormal, SpawnTime: now})
	pq.Push(&SchedulablePipeline{ID: "high", Priority: PriorityHigh, SpawnTime: now})

	expected := []string{"critical", "high", "normal", "low"}
	for _, id := range expected {
		p := pq.Pop()
		if p.ID != id {
			t.Errorf("got ID %s, want %s", p.ID, id)
		}
	}
}

func TestPipelinePriorityQueue_SpawnTimeOrdering(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	base := time.Now()
	pq.Push(&SchedulablePipeline{ID: "later", Priority: PriorityNormal, SpawnTime: base.Add(time.Second)})
	pq.Push(&SchedulablePipeline{ID: "earlier", Priority: PriorityNormal, SpawnTime: base})

	first := pq.Pop()
	if first.ID != "earlier" {
		t.Errorf("got ID %s, want earlier", first.ID)
	}
}

func TestPipelinePriorityQueue_Remove(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	pq.Push(&SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()})
	pq.Push(&SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now()})
	pq.Push(&SchedulablePipeline{ID: "p3", Priority: PriorityNormal, SpawnTime: time.Now()})

	if !pq.Remove("p2") {
		t.Error("Remove should return true")
	}

	if pq.Len() != 2 {
		t.Errorf("got Len %d, want 2", pq.Len())
	}

	if pq.Contains("p2") {
		t.Error("p2 should not be in queue")
	}
}

func TestPipelinePriorityQueue_RemoveNotFound(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	if pq.Remove("nonexistent") {
		t.Error("Remove should return false for nonexistent")
	}
}

func TestPipelinePriorityQueue_Peek(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	if pq.Peek() != nil {
		t.Error("Peek on empty queue should return nil")
	}

	pq.Push(&SchedulablePipeline{ID: "p1", Priority: PriorityHigh, SpawnTime: time.Now()})
	pq.Push(&SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now()})

	p := pq.Peek()
	if p.ID != "p1" {
		t.Errorf("got ID %s, want p1", p.ID)
	}

	if pq.Len() != 2 {
		t.Errorf("Peek should not remove: got Len %d, want 2", pq.Len())
	}
}

func TestPipelinePriorityQueue_Contains(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	pq.Push(&SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()})

	if !pq.Contains("p1") {
		t.Error("Contains should return true for p1")
	}

	if pq.Contains("p2") {
		t.Error("Contains should return false for p2")
	}
}

func TestPipelinePriorityQueue_HeapProperty(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	now := time.Now()
	for i := 0; i < 100; i++ {
		pq.Push(&SchedulablePipeline{
			ID:        pipelineIDFromInt(i),
			Priority:  PipelinePriority(i % 4),
			SpawnTime: now.Add(time.Duration(i) * time.Millisecond),
		})
	}

	var lastPriority PipelinePriority = PriorityCritical + 1
	var lastTime time.Time

	for pq.Len() > 0 {
		p := pq.Pop()
		if p.Priority > lastPriority {
			t.Error("heap property violated: priority decreased")
		}
		if p.Priority == lastPriority && p.SpawnTime.Before(lastTime) {
			t.Error("heap property violated: time order wrong")
		}
		lastPriority = p.Priority
		lastTime = p.SpawnTime
	}
}

func pipelineIDFromInt(i int) string {
	return "p" + itoa(i)
}

func TestPipelinePriorityQueue_All(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	now := time.Now()
	pq.Push(&SchedulablePipeline{ID: "p1", Priority: PriorityHigh, SpawnTime: now})
	pq.Push(&SchedulablePipeline{ID: "p2", Priority: PriorityLow, SpawnTime: now})
	pq.Push(&SchedulablePipeline{ID: "p3", Priority: PriorityNormal, SpawnTime: now})

	all := pq.All()

	if len(all) != 3 {
		t.Errorf("All() should return 3 items, got %d", len(all))
	}

	ids := make(map[string]bool)
	for _, p := range all {
		ids[p.ID] = true
	}

	if !ids["p1"] || !ids["p2"] || !ids["p3"] {
		t.Error("All() should contain all pushed items")
	}
}

func TestPipelinePriorityQueue_All_Empty(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	all := pq.All()

	if len(all) != 0 {
		t.Errorf("All() on empty queue should return empty slice, got %d items", len(all))
	}
}

func TestPipelinePriorityQueue_All_DoesNotModifyQueue(t *testing.T) {
	pq := NewPipelinePriorityQueue()

	now := time.Now()
	pq.Push(&SchedulablePipeline{ID: "p1", Priority: PriorityHigh, SpawnTime: now})
	pq.Push(&SchedulablePipeline{ID: "p2", Priority: PriorityLow, SpawnTime: now})

	all := pq.All()
	all[0] = nil

	if pq.Len() != 2 {
		t.Error("Modifying All() result should not affect queue")
	}

	if pq.Peek() == nil {
		t.Error("Queue should still have items after modifying All() result")
	}
}
