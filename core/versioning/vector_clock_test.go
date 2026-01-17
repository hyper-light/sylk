package versioning

import (
	"sync"
	"testing"
)

func TestNewVectorClock(t *testing.T) {
	vc := NewVectorClock()
	if vc == nil {
		t.Error("expected non-nil vector clock")
	}
	if len(vc) != 0 {
		t.Error("expected empty vector clock")
	}
}

func TestVectorClock_Increment(t *testing.T) {
	t.Run("increment new session", func(t *testing.T) {
		vc := NewVectorClock()
		vc2 := vc.Increment("session-1")

		if vc2["session-1"] != 1 {
			t.Errorf("expected 1, got %d", vc2["session-1"])
		}
		if vc["session-1"] != 0 {
			t.Error("original should be unchanged")
		}
	})

	t.Run("increment existing session", func(t *testing.T) {
		vc := VectorClock{"session-1": 5}
		vc2 := vc.Increment("session-1")

		if vc2["session-1"] != 6 {
			t.Errorf("expected 6, got %d", vc2["session-1"])
		}
	})

	t.Run("increment preserves other sessions", func(t *testing.T) {
		vc := VectorClock{"session-1": 5, "session-2": 3}
		vc2 := vc.Increment("session-1")

		if vc2["session-2"] != 3 {
			t.Errorf("expected session-2 to be 3, got %d", vc2["session-2"])
		}
	})
}

func TestVectorClock_Merge(t *testing.T) {
	t.Run("merge takes max", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 5, "session-2": 3}
		vc2 := VectorClock{"session-1": 3, "session-2": 7}

		merged := vc1.Merge(vc2)

		if merged["session-1"] != 5 {
			t.Errorf("expected 5, got %d", merged["session-1"])
		}
		if merged["session-2"] != 7 {
			t.Errorf("expected 7, got %d", merged["session-2"])
		}
	})

	t.Run("merge with new session", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 5}
		vc2 := VectorClock{"session-2": 3}

		merged := vc1.Merge(vc2)

		if merged["session-1"] != 5 {
			t.Errorf("expected 5, got %d", merged["session-1"])
		}
		if merged["session-2"] != 3 {
			t.Errorf("expected 3, got %d", merged["session-2"])
		}
	})

	t.Run("merge with empty", func(t *testing.T) {
		vc := VectorClock{"session-1": 5}
		merged := vc.Merge(VectorClock{})

		if merged["session-1"] != 5 {
			t.Errorf("expected 5, got %d", merged["session-1"])
		}
	})
}

func TestVectorClock_HappensBefore(t *testing.T) {
	t.Run("strictly before", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 2}

		if !vc1.HappensBefore(vc2) {
			t.Error("expected vc1 to happen before vc2")
		}
	})

	t.Run("not before when greater", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 2}
		vc2 := VectorClock{"session-1": 1}

		if vc1.HappensBefore(vc2) {
			t.Error("expected vc1 NOT to happen before vc2")
		}
	})

	t.Run("not before when equal", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 2}
		vc2 := VectorClock{"session-1": 2}

		if vc1.HappensBefore(vc2) {
			t.Error("equal clocks should not be before each other")
		}
	})

	t.Run("before with new session in other", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 1, "session-2": 1}

		if !vc1.HappensBefore(vc2) {
			t.Error("expected vc1 to happen before vc2")
		}
	})

	t.Run("empty clocks", func(t *testing.T) {
		vc1 := VectorClock{}
		vc2 := VectorClock{}

		if vc1.HappensBefore(vc2) {
			t.Error("empty clocks should not be before each other")
		}
	})

	t.Run("empty before non-empty", func(t *testing.T) {
		vc1 := VectorClock{}
		vc2 := VectorClock{"session-1": 1}

		if !vc1.HappensBefore(vc2) {
			t.Error("empty should happen before non-empty")
		}
	})
}

func TestVectorClock_Concurrent(t *testing.T) {
	t.Run("concurrent events", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 2, "session-2": 1}
		vc2 := VectorClock{"session-1": 1, "session-2": 2}

		if !vc1.Concurrent(vc2) {
			t.Error("expected concurrent")
		}
	})

	t.Run("not concurrent when ordered", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 2}

		if vc1.Concurrent(vc2) {
			t.Error("expected not concurrent")
		}
	})

	t.Run("equal clocks are concurrent", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 1}

		if !vc1.Concurrent(vc2) {
			t.Error("equal clocks should be concurrent")
		}
	})
}

func TestVectorClock_Clone(t *testing.T) {
	t.Run("creates independent copy", func(t *testing.T) {
		vc := VectorClock{"session-1": 5}
		clone := vc.Clone()

		clone["session-1"] = 10

		if vc["session-1"] != 5 {
			t.Error("original should be unchanged")
		}
	})

	t.Run("empty clone", func(t *testing.T) {
		vc := VectorClock{}
		clone := vc.Clone()

		if len(clone) != 0 {
			t.Error("clone should be empty")
		}
	})
}

func TestVectorClock_Get(t *testing.T) {
	vc := VectorClock{"session-1": 5}

	if vc.Get("session-1") != 5 {
		t.Errorf("expected 5, got %d", vc.Get("session-1"))
	}
	if vc.Get("session-2") != 0 {
		t.Errorf("expected 0 for missing session, got %d", vc.Get("session-2"))
	}
}

func TestVectorClock_Set(t *testing.T) {
	vc := VectorClock{"session-1": 5}
	vc2 := vc.Set("session-2", 10)

	if vc2["session-2"] != 10 {
		t.Errorf("expected 10, got %d", vc2["session-2"])
	}
	if vc["session-2"] != 0 {
		t.Error("original should be unchanged")
	}
}

func TestVectorClock_IsZero(t *testing.T) {
	t.Run("empty is zero", func(t *testing.T) {
		vc := VectorClock{}
		if !vc.IsZero() {
			t.Error("empty should be zero")
		}
	})

	t.Run("all zeros is zero", func(t *testing.T) {
		vc := VectorClock{"session-1": 0, "session-2": 0}
		if !vc.IsZero() {
			t.Error("all zeros should be zero")
		}
	})

	t.Run("non-zero value", func(t *testing.T) {
		vc := VectorClock{"session-1": 1}
		if vc.IsZero() {
			t.Error("should not be zero")
		}
	})
}

func TestVectorClock_Equal(t *testing.T) {
	t.Run("equal clocks", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1, "session-2": 2}
		vc2 := VectorClock{"session-1": 1, "session-2": 2}

		if !vc1.Equal(vc2) {
			t.Error("expected equal")
		}
	})

	t.Run("different values", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 2}

		if vc1.Equal(vc2) {
			t.Error("expected not equal")
		}
	})

	t.Run("different sessions", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-2": 1}

		if vc1.Equal(vc2) {
			t.Error("expected not equal")
		}
	})

	t.Run("different lengths", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 1, "session-2": 2}

		if vc1.Equal(vc2) {
			t.Error("expected not equal")
		}
	})
}

func TestVectorClock_Compare(t *testing.T) {
	t.Run("before returns -1", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 2}

		if vc1.Compare(vc2) != -1 {
			t.Errorf("expected -1, got %d", vc1.Compare(vc2))
		}
	})

	t.Run("after returns 1", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 2}
		vc2 := VectorClock{"session-1": 1}

		if vc1.Compare(vc2) != 1 {
			t.Errorf("expected 1, got %d", vc1.Compare(vc2))
		}
	})

	t.Run("equal returns 0", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 1}
		vc2 := VectorClock{"session-1": 1}

		if vc1.Compare(vc2) != 0 {
			t.Errorf("expected 0, got %d", vc1.Compare(vc2))
		}
	})

	t.Run("concurrent returns 2", func(t *testing.T) {
		vc1 := VectorClock{"session-1": 2, "session-2": 1}
		vc2 := VectorClock{"session-1": 1, "session-2": 2}

		if vc1.Compare(vc2) != 2 {
			t.Errorf("expected 2, got %d", vc1.Compare(vc2))
		}
	})
}

func TestVectorClock_Sessions(t *testing.T) {
	vc := VectorClock{"session-1": 1, "session-2": 2}
	sessions := vc.Sessions()

	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}

	found := make(map[SessionID]bool)
	for _, s := range sessions {
		found[s] = true
	}

	if !found["session-1"] || !found["session-2"] {
		t.Error("missing expected sessions")
	}
}

func TestVectorClock_ConcurrentAccess(t *testing.T) {
	vc := NewVectorClock()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			session := SessionID("session")
			_ = vc.Increment(session)
			_ = vc.Clone()
			_ = vc.Get(session)
			_ = vc.IsZero()
		}(i)
	}

	wg.Wait()
}
