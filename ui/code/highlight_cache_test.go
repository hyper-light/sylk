package code

import (
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestHighlighterHighlightContentCachesExactContent(t *testing.T) {
	h := NewHighlighter(theme.DefaultDark())
	defer h.Close()

	content := "package main\nfunc main() {}\n"
	_ = h.HighlightContent(content, "go")

	sentinel := [][]HighlightRegion{{{
		StartCol: 1,
		EndCol:   2,
		Category: theme.CatKeyword,
	}}}
	h.cachedRegions = sentinel
	h.cacheValid = true

	got := h.HighlightContent(content, "go")
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != sentinel[0][0] {
		t.Fatalf("expected exact-content cache hit, got %#v", got)
	}
}

func TestHighlighterHighlightContentInvalidatesOnLanguageChange(t *testing.T) {
	h := NewHighlighter(theme.DefaultDark())
	defer h.Close()

	content := "package main\nfunc main() {}\n"
	_ = h.HighlightContent(content, "go")

	sentinel := [][]HighlightRegion{{{
		StartCol: 7,
		EndCol:   8,
		Category: theme.CatKeyword,
	}}}
	h.cachedRegions = sentinel
	h.cacheValid = true

	got := h.HighlightContent(content, "python")
	if len(got) == 1 && len(got[0]) == 1 && got[0][0] == sentinel[0][0] {
		t.Fatal("language change reused stale cached regions")
	}
}
