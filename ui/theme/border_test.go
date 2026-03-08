package theme

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestNormalizeBorderLinesTruncatesANSIContent(t *testing.T) {
	lines := normalizeBorderLines("\x1b[31mabcdef\x1b[0m", 4, 2)

	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(lines))
	}
	if got := ansi.StringWidth(lines[0]); got != 4 {
		t.Fatalf("ansi.StringWidth(lines[0]) = %d, want 4", got)
	}
	if got := ansi.StringWidth(lines[1]); got != 4 {
		t.Fatalf("ansi.StringWidth(lines[1]) = %d, want 4", got)
	}
}

func TestRenderGradientBorderKeepsExpectedDimensions(t *testing.T) {
	g := DefaultDark().Palette.FocusRingGradient()
	rendered := RenderGradientBorder("\x1b[31mabcdef\x1b[0m", g, 150*time.Millisecond, 4, 1, 0)
	lines := strings.Split(rendered, "\n")

	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 6 {
			t.Fatalf("ansi.StringWidth(lines[%d]) = %d, want 6", i, got)
		}
	}
}

func TestCachedBorderFrameBucketsReuseFrames(t *testing.T) {
	borderFrameCacheMu.Lock()
	clear(borderFrameCache)
	borderFrameCacheMu.Unlock()

	g := DefaultDark().Palette.FocusRingGradient()

	first := cachedBorderFrame(g, 110*time.Millisecond, 8, 3)
	second := cachedBorderFrame(g, 190*time.Millisecond, 8, 3)

	if first.top != second.top || first.bottom != second.bottom {
		t.Fatal("expected same cached border frame within one phase bucket")
	}

	borderFrameCacheMu.Lock()
	cacheSize := len(borderFrameCache)
	borderFrameCacheMu.Unlock()

	if cacheSize != 1 {
		t.Fatalf("len(borderFrameCache) = %d, want 1", cacheSize)
	}
}
