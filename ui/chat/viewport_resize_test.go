package chat

import (
	"fmt"
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func newResizeViewport(entryCount int, height int) *Viewport {
	history := NewHistory(entryCount)
	for i := range entryCount {
		label := fmt.Sprintf("entry-%d", i)
		history.Push(&ChatEntry{
			ID:            label,
			RenderedLines: []string{label},
			Height:        1,
		})
	}
	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(80, height)
	return vp
}

func TestViewportShrinkPreservesTopEntryWhileFollowing(t *testing.T) {
	vp := newResizeViewport(6, 4)
	before := vp.EntryAtViewLine(0)
	if before == nil {
		t.Fatal("expected visible top entry before shrink")
	}

	vp.SetSize(80, 3)
	after := vp.EntryAtViewLine(0)
	if after == nil {
		t.Fatal("expected visible top entry after shrink")
	}
	if after.ID != before.ID {
		t.Fatalf("top entry = %q, want %q", after.ID, before.ID)
	}

	vp.history.Push(&ChatEntry{ID: "entry-6", RenderedLines: []string{"entry-6"}, Height: 1})
	vp.OnNewEntry()
	if vp.scrollOff != 0 {
		t.Fatalf("scrollOff = %d, want 0 after new content", vp.scrollOff)
	}
	if vp.layoutCompensation != 0 {
		t.Fatalf("layoutCompensation = %d, want 0 after new content", vp.layoutCompensation)
	}
}

func TestViewportShrinkPreservesTopEntryWhileScrolledBack(t *testing.T) {
	vp := newResizeViewport(6, 4)
	if !vp.ScrollUp() {
		t.Fatal("expected scroll up to succeed")
	}
	before := vp.EntryAtViewLine(0)
	if before == nil {
		t.Fatal("expected visible top entry before shrink")
	}

	vp.SetSize(80, 3)
	after := vp.EntryAtViewLine(0)
	if after == nil {
		t.Fatal("expected visible top entry after shrink")
	}
	if after.ID != before.ID {
		t.Fatalf("top entry = %q, want %q", after.ID, before.ID)
	}
}
