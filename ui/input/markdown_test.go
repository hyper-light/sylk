package input

import (
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func testTheme() *theme.Theme {
	return theme.DefaultDark()
}

func TestParseMdDisplay_Bold(t *testing.T) {
	lines := [][]rune{[]rune("hello **bold** world")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	ml := md.lines[0]
	// "hello bold world" = 16 display chars, raw = 19
	dispRunes := md.displayRunes(0, lines[0])
	got := string(dispRunes)
	want := "hello bold world"
	if got != want {
		t.Errorf("displayRunes = %q, want %q", got, want)
	}

	// Check that "bold" region exists with mdBold kind.
	found := false
	for _, r := range ml.regions {
		if r.kind == mdBold {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected mdBold region in regions")
	}
}

func TestParseMdDisplay_Italic(t *testing.T) {
	lines := [][]rune{[]rune("hello *italic* world")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	dispRunes := md.displayRunes(0, lines[0])
	got := string(dispRunes)
	want := "hello italic world"
	if got != want {
		t.Errorf("displayRunes = %q, want %q", got, want)
	}

	found := false
	for _, r := range md.lines[0].regions {
		if r.kind == mdItalic {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected mdItalic region")
	}
}

func TestParseMdDisplay_Code(t *testing.T) {
	lines := [][]rune{[]rune("use `code` here")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	dispRunes := md.displayRunes(0, lines[0])
	got := string(dispRunes)
	want := "use code here"
	if got != want {
		t.Errorf("displayRunes = %q, want %q", got, want)
	}
}

func TestParseMdDisplay_Strike(t *testing.T) {
	lines := [][]rune{[]rune("hello ~~strike~~ end")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	dispRunes := md.displayRunes(0, lines[0])
	got := string(dispRunes)
	want := "hello strike end"
	if got != want {
		t.Errorf("displayRunes = %q, want %q", got, want)
	}
}

func TestParseMdDisplay_Link(t *testing.T) {
	lines := [][]rune{[]rune("click [here](http://x) now")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	dispRunes := md.displayRunes(0, lines[0])
	got := string(dispRunes)
	want := "click here now"
	if got != want {
		t.Errorf("displayRunes = %q, want %q", got, want)
	}

	found := false
	for _, r := range md.lines[0].regions {
		if r.kind == mdLink {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected mdLink region")
	}
}

func TestParseMdDisplay_UnclosedDelimiters(t *testing.T) {
	lines := [][]rune{[]rune("**partial bold")}
	md := parseMdDisplay(lines, testTheme())
	// Unclosed delimiters: goldmark treats as literal text, so no formatting.
	if md != nil {
		// md may be nil (no formatting) which is correct.
		// If non-nil, there should be no hidden chars.
		dispRunes := md.displayRunes(0, lines[0])
		got := string(dispRunes)
		want := "**partial bold"
		if got != want {
			t.Errorf("displayRunes = %q, want %q (unclosed should be literal)", got, want)
		}
	}
}

func TestParseMdDisplay_NestedEmphasis(t *testing.T) {
	lines := [][]rune{[]rune("***bold italic***")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	dispRunes := md.displayRunes(0, lines[0])
	got := string(dispRunes)
	want := "bold italic"
	if got != want {
		t.Errorf("displayRunes = %q, want %q", got, want)
	}
}

func TestRawColToDisplay_Roundtrip(t *testing.T) {
	lines := [][]rune{[]rune("a **b** c")}
	md := parseMdDisplay(lines, testTheme())
	if md == nil {
		t.Fatal("expected non-nil mdDisplay")
	}

	ml := md.lines[0]
	// For each visible display col, roundtrip through dispToRaw → rawToDisplay.
	for dispCol := range ml.dispToRaw {
		rawCol := ml.displayColToRaw(dispCol)
		backDisp := ml.rawColToDisplay(rawCol)
		if backDisp != dispCol {
			t.Errorf("roundtrip: dispCol=%d → rawCol=%d → dispCol=%d (want %d)",
				dispCol, rawCol, backDisp, dispCol)
		}
	}
}

func TestParseMdDisplay_NoMarkdown(t *testing.T) {
	lines := [][]rune{[]rune("plain text no formatting")}
	md := parseMdDisplay(lines, testTheme())
	if md != nil {
		t.Error("expected nil for plain text")
	}
}
