package compositor

import (
	"strings"
	"testing"
)

func TestSetStructureResetsCache(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotLeft, SlotRight}, 3, 0, 1, 1)

	if c.HasCache() {
		t.Fatal("expected no cache after SetStructure")
	}
	if !c.IsDirty(SlotLeft) {
		t.Fatal("expected main dirty after SetStructure")
	}
	if !c.IsDirty(SlotInput) {
		t.Fatal("expected input dirty after SetStructure")
	}
	if !c.IsDirty(SlotStatus) {
		t.Fatal("expected status dirty after SetStructure")
	}
}

func TestSingleColumnCompose(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 2, 0, 1, 1)

	c.SetSlotLines(SlotCenter, []string{"chat-line-0", "chat-line-1"})
	c.SetSlotLines(SlotInput, []string{"input-line"})
	c.SetSlotLines(SlotStatus, []string{"status-line"})

	got := c.Compose()
	want := "chat-line-0\nchat-line-1\ninput-line\nstatus-line"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !c.HasCache() {
		t.Fatal("expected cache after Compose")
	}
}

func TestTwoColumnSplice(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotLeft, SlotRight}, 2, 0, 0, 0)

	c.SetSlotLines(SlotLeft, []string{"L0", "L1"})
	c.SetSlotLines(SlotRight, []string{"R0", "R1"})

	got := c.Compose()
	want := "L0R0\nL1R1"
	if got != want {
		t.Fatalf("got: %q, want: %q", got, want)
	}
}

func TestPartialDirty(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotLeft, SlotRight}, 2, 0, 1, 1)

	c.SetSlotLines(SlotLeft, []string{"L0", "L1"})
	c.SetSlotLines(SlotRight, []string{"R0", "R1"})
	c.SetSlotLines(SlotInput, []string{"inp"})
	c.SetSlotLines(SlotStatus, []string{"sts"})
	c.Compose()

	// Update only left column.
	c.SetSlotLines(SlotLeft, []string{"X0", "X1"})

	// Main should be dirty, input/status should not.
	if !c.IsDirty(SlotLeft) {
		t.Fatal("expected main dirty")
	}
	if c.IsDirty(SlotInput) {
		t.Fatal("expected input clean")
	}
	if c.IsDirty(SlotStatus) {
		t.Fatal("expected status clean")
	}

	got := c.Compose()
	want := "X0R0\nX1R1\ninp\nsts"
	if got != want {
		t.Fatalf("got: %q, want: %q", got, want)
	}
}

func TestInvalidateAll(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 1, 0, 1, 1)
	c.SetSlotLines(SlotCenter, []string{"c"})
	c.SetSlotLines(SlotInput, []string{"i"})
	c.SetSlotLines(SlotStatus, []string{"s"})
	c.Compose()

	c.InvalidateAll()
	if !c.IsDirty(SlotCenter) || !c.IsDirty(SlotInput) || !c.IsDirty(SlotStatus) {
		t.Fatal("expected all dirty after InvalidateAll")
	}
	if c.HasCache() {
		t.Fatal("expected no cache after InvalidateAll")
	}
}

func TestFourColumnSplice(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotLeft, SlotCenterLeft, SlotCenter, SlotRight}, 2, 0, 0, 0)

	c.SetSlotLines(SlotLeft, []string{"A0", "A1"})
	c.SetSlotLines(SlotCenterLeft, []string{"B0", "B1"})
	c.SetSlotLines(SlotCenter, []string{"C0", "C1"})
	c.SetSlotLines(SlotRight, []string{"D0", "D1"})

	got := c.Compose()
	want := "A0B0C0D0\nA1B1C1D1"
	if got != want {
		t.Fatalf("got: %q, want: %q", got, want)
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines("a\nb\nc")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestCachedFrameReturnsSame(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 1, 0, 0, 0)
	c.SetSlotLines(SlotCenter, []string{"hello"})
	first := c.Compose()
	second := c.CachedFrame()
	if first != second {
		t.Fatalf("CachedFrame mismatch: %q vs %q", first, second)
	}
}

func TestMarkDirty(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 1, 0, 1, 1)
	c.SetSlotLines(SlotCenter, []string{"c"})
	c.SetSlotLines(SlotInput, []string{"i"})
	c.SetSlotLines(SlotStatus, []string{"s"})
	c.Compose()

	c.MarkDirty(SlotInput)
	if !c.IsDirty(SlotInput) {
		t.Fatal("expected input dirty")
	}
	if c.IsDirty(SlotCenter) {
		t.Fatal("expected main clean")
	}
}

func TestMissingSlotProducesEmptyLines(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotLeft, SlotRight}, 2, 0, 0, 0)

	// Only set left, right is missing.
	c.SetSlotLines(SlotLeft, []string{"L0", "L1"})

	got := c.Compose()
	lines := strings.Split(got, "\n")
	if lines[0] != "L0" || lines[1] != "L1" {
		t.Fatalf("unexpected: %v", lines)
	}
}

func TestQueueSlotCompose(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 2, 1, 1, 1)

	c.SetSlotLines(SlotCenter, []string{"chat-0", "chat-1"})
	c.SetSlotLines(SlotQueue, []string{"queue-0"})
	c.SetSlotLines(SlotInput, []string{"input-0"})
	c.SetSlotLines(SlotStatus, []string{"status-0"})

	got := c.Compose()
	want := "chat-0\nchat-1\nqueue-0\ninput-0\nstatus-0"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestQueueSlotZeroHeight(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 2, 0, 1, 1)

	c.SetSlotLines(SlotCenter, []string{"chat-0", "chat-1"})
	c.SetSlotLines(SlotInput, []string{"input-0"})
	c.SetSlotLines(SlotStatus, []string{"status-0"})

	got := c.Compose()
	want := "chat-0\nchat-1\ninput-0\nstatus-0"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestQueueDirtyFlag(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 2, 1, 1, 1)
	c.SetSlotLines(SlotCenter, []string{"c0", "c1"})
	c.SetSlotLines(SlotQueue, []string{"q0"})
	c.SetSlotLines(SlotInput, []string{"i0"})
	c.SetSlotLines(SlotStatus, []string{"s0"})
	c.Compose()

	// Only update queue.
	c.SetSlotLines(SlotQueue, []string{"q1"})
	if !c.IsDirty(SlotQueue) {
		t.Fatal("expected queue dirty")
	}
	if c.IsDirty(SlotCenter) {
		t.Fatal("expected main clean")
	}
	if c.IsDirty(SlotInput) {
		t.Fatal("expected input clean")
	}

	got := c.Compose()
	want := "c0\nc1\nq1\ni0\ns0"
	if got != want {
		t.Fatalf("got: %q, want: %q", got, want)
	}
}

func TestAdjustVerticalSections(t *testing.T) {
	c := New()
	// Total: main=3 + queue=1 + input=1 + status=1 = 6 lines.
	c.SetStructure([]SlotID{SlotCenter}, 3, 1, 1, 1)
	c.SetSlotLines(SlotCenter, []string{"c0", "c1", "c2"})
	c.SetSlotLines(SlotQueue, []string{"q0"})
	c.SetSlotLines(SlotInput, []string{"i0"})
	c.SetSlotLines(SlotStatus, []string{"s0"})
	c.Compose()

	// Shrink main by 1, grow queue to 2.
	c.SetSlotLines(SlotCenter, []string{"c0", "c1"})
	c.SetSlotLines(SlotQueue, []string{"q0", "q1"})
	c.AdjustVerticalSections(2, 2, 1, SlotCenter)

	got := c.Compose()
	want := "c0\nc1\nq0\nq1\ni0\ns0"
	if got != want {
		t.Fatalf("got: %q, want: %q", got, want)
	}
}

func TestInvalidateAllIncludesQueue(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 1, 1, 1, 1)
	c.SetSlotLines(SlotCenter, []string{"c"})
	c.SetSlotLines(SlotQueue, []string{"q"})
	c.SetSlotLines(SlotInput, []string{"i"})
	c.SetSlotLines(SlotStatus, []string{"s"})
	c.Compose()

	c.InvalidateAll()
	if !c.IsDirty(SlotQueue) {
		t.Fatal("expected queue dirty after InvalidateAll")
	}
}

func TestSnapshotTracksVersionAndAbsoluteDirtyRows(t *testing.T) {
	c := New()
	c.SetStructure([]SlotID{SlotCenter}, 2, 0, 1, 1)
	c.SetSlotLines(SlotCenter, []string{"chat-0", "chat-1"})
	c.SetSlotLines(SlotInput, []string{"input-0"})
	c.SetSlotLines(SlotStatus, []string{"status-0"})
	c.Compose()

	first := c.Snapshot()
	if first.Version != 1 {
		t.Fatalf("first.Version = %d, want 1", first.Version)
	}
	if !first.Full {
		t.Fatal("expected first snapshot to be a full frame")
	}

	c.SetSlotLines(SlotInput, []string{"input-1"})
	c.Compose()

	second := c.Snapshot()
	if second.Version != 2 {
		t.Fatalf("second.Version = %d, want 2", second.Version)
	}
	if second.Full {
		t.Fatal("expected incremental snapshot after input-only update")
	}
	if len(second.DirtyRows) != 1 || second.DirtyRows[0] != 2 {
		t.Fatalf("second.DirtyRows = %v, want [2]", second.DirtyRows)
	}
}
