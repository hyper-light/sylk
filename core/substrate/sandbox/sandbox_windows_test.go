//go:build windows

package sandbox

import (
	"testing"
)

func TestNewWindows_Platform(t *testing.T) {
	s := NewWindows()
	if s.Platform().OS != "windows" {
		t.Errorf("Platform.OS = %q", s.Platform().OS)
	}
	if s.Platform().Libc != "msvc" {
		t.Errorf("Platform.Libc = %q, want msvc", s.Platform().Libc)
	}
}

func TestQuoteCommandLineArg_NoQuotingNeeded(t *testing.T) {
	cases := []string{"simple", "no-spaces", "with-dash"}
	for _, in := range cases {
		got := quoteCommandLineArg(in)
		if got != in {
			t.Errorf("quoteCommandLineArg(%q) = %q, want %q (unchanged)", in, got, in)
		}
	}
}

func TestQuoteCommandLineArg_SpacesQuoted(t *testing.T) {
	got := quoteCommandLineArg("with spaces")
	want := `"with spaces"`
	if got != want {
		t.Errorf("quoteCommandLineArg = %q, want %q", got, want)
	}
}

func TestQuoteCommandLineArg_EmbeddedQuotesEscaped(t *testing.T) {
	got := quoteCommandLineArg(`with "quotes"`)
	// Per CommandLineToArgvW rules, embedded double-quotes are escaped
	// with a single preceding backslash. The outer quoting wraps the
	// whole token.
	want := `"with \"quotes\""`
	if got != want {
		t.Errorf("quoteCommandLineArg = %q, want %q", got, want)
	}
}

func TestQuoteCommandLineArg_BackslashesBeforeQuote(t *testing.T) {
	// A backslash before a double-quote must be doubled. Two backslashes
	// before a quote become five (2*2 + 1 for the quote escape).
	got := quoteCommandLineArg(`a\\"b`)
	want := `"a\\\\\"b"`
	if got != want {
		t.Errorf("quoteCommandLineArg = %q, want %q", got, want)
	}
}

func TestQuoteCommandLineArg_EmptyString(t *testing.T) {
	if got := quoteCommandLineArg(""); got != `""` {
		t.Errorf("quoteCommandLineArg(\"\") = %q, want \"\"", got)
	}
}

func TestNeedsQuoting(t *testing.T) {
	cases := map[string]bool{
		"simple":        false,
		"with-dash":     false,
		"with space":    true,
		"with\ttab":     true,
		`with"quote`:    true,
	}
	for in, want := range cases {
		if got := needsQuoting(in); got != want {
			t.Errorf("needsQuoting(%q) = %v, want %v", in, got, want)
		}
	}
}
