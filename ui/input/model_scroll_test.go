package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestRenderBody_ShowsScrollIndicatorsInReservedRightGutter(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetSize(12, 4) // content width 10, visible text rows 2
	m.SetText("1234567\nABCDEFG\nijklmno")
	m.scrollOff = 0

	body := m.renderBody(false)
	lines := strings.Split(body, "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(lines))
	}
	if got := ansi.StringWidth(lines[0]); got != 10 {
		t.Fatalf("top row width = %d, want 10", got)
	}
	if got := ansi.StringWidth(lines[1]); got != 10 {
		t.Fatalf("bottom row width = %d, want 10", got)
	}

	top := ansi.Strip(lines[0])
	bottom := ansi.Strip(lines[1])
	if top != "1234567   " {
		t.Fatalf("top row = %q, want %q", top, "1234567   ")
	}
	if bottom != "ABCDEFG ⇓ " {
		t.Fatalf("bottom row = %q, want %q", bottom, "ABCDEFG ⇓ ")
	}
}

func TestRenderBody_ShowsUpIndicatorAfterScrolling(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetSize(12, 4) // content width 10, visible text rows 2
	m.SetText("1234567\nABCDEFG\nijklmno")
	m.scrollOff = 1

	body := m.renderBody(false)
	lines := strings.Split(body, "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(lines))
	}

	top := ansi.Strip(lines[0])
	bottom := ansi.Strip(lines[1])
	if top != "ABCDEFG ⇑ " {
		t.Fatalf("top row after scroll = %q, want %q", top, "ABCDEFG ⇑ ")
	}
	if bottom != "ijklmno   " {
		t.Fatalf("bottom row after scroll = %q, want %q", bottom, "ijklmno   ")
	}
}

func TestRenderBody_SingleVisibleRowUsesCombinedScrollIndicator(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetSize(12, 3) // content width 10, visible text rows 1
	m.SetText("rowonea\nrowtwob\nrowthrc")
	m.scrollOff = 1

	body := m.renderBody(false)
	lines := strings.Split(body, "\n")
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}
	if got := ansi.Strip(lines[0]); got != "rowtwob ⇕ " {
		t.Fatalf("single row = %q, want %q", got, "rowtwob ⇕ ")
	}
}
