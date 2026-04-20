package fabric

import (
	"sync"
	"testing"
	"time"
)

func TestNoOpEmitter_Discards(t *testing.T) {
	e := NoOpEmitter{}
	e.Emit(Activity{Kind: ToolPinned})
	// Nothing to assert — NoOp should never panic, never block.
}

func TestCapturingEmitter_RecordsInOrder(t *testing.T) {
	c := NewCapturingEmitter()
	c.Emit(Activity{Kind: ToolPinned, Scope: "tooling/python/pytest"})
	c.Emit(Activity{Kind: ToolWitnessAdded, Scope: "tooling/python/pytest"})

	got := c.Activities()
	if len(got) != 2 {
		t.Fatalf("got %d activities, want 2", len(got))
	}
	if got[0].Kind != ToolPinned {
		t.Errorf("got[0].Kind = %v, want ToolPinned", got[0].Kind)
	}
	if got[1].Kind != ToolWitnessAdded {
		t.Errorf("got[1].Kind = %v, want ToolWitnessAdded", got[1].Kind)
	}
}

func TestCapturingEmitter_StampsTimestampWhenZero(t *testing.T) {
	c := NewCapturingEmitter()
	c.Emit(Activity{Kind: ToolPinned})
	got := c.Activities()
	if got[0].Timestamp.IsZero() {
		t.Error("expected Emit to stamp Timestamp when zero")
	}
}

func TestCapturingEmitter_PreservesProvidedTimestamp(t *testing.T) {
	c := NewCapturingEmitter()
	stamp := mustParseTime("2026-04-19T16:23:18Z")
	c.Emit(Activity{Kind: ToolPinned, Timestamp: stamp})
	if c.Activities()[0].Timestamp != stamp {
		t.Error("Emit replaced a provided Timestamp")
	}
}

func TestCapturingEmitter_CountByKind(t *testing.T) {
	c := NewCapturingEmitter()
	c.Emit(Activity{Kind: ToolPinned})
	c.Emit(Activity{Kind: ToolPinned})
	c.Emit(Activity{Kind: ToolDetached})
	if c.CountByKind(ToolPinned) != 2 {
		t.Errorf("CountByKind(ToolPinned) = %d, want 2", c.CountByKind(ToolPinned))
	}
	if c.CountByKind(ToolDetached) != 1 {
		t.Errorf("CountByKind(ToolDetached) = %d, want 1", c.CountByKind(ToolDetached))
	}
	if c.CountByKind(ToolUpgraded) != 0 {
		t.Errorf("CountByKind(ToolUpgraded) = %d, want 0", c.CountByKind(ToolUpgraded))
	}
}

func TestCapturingEmitter_Reset(t *testing.T) {
	c := NewCapturingEmitter()
	c.Emit(Activity{Kind: ToolPinned})
	c.Reset()
	if c.Len() != 0 {
		t.Errorf("after Reset, Len = %d, want 0", c.Len())
	}
}

func TestCapturingEmitter_Concurrent(t *testing.T) {
	c := NewCapturingEmitter()
	const goroutines = 32
	const perG = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				c.Emit(Activity{Kind: ToolPinned})
			}
		}()
	}
	wg.Wait()
	if got := c.Len(); got != goroutines*perG {
		t.Errorf("captured %d activities, want %d", got, goroutines*perG)
	}
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
