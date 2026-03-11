package logging

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	testCases := []struct {
		input string
		want  slog.Level
		ok    bool
	}{
		{input: "trace", want: LevelTrace, ok: true},
		{input: "debug", want: slog.LevelDebug, ok: true},
		{input: "info", want: slog.LevelInfo, ok: true},
		{input: "warn", want: slog.LevelWarn, ok: true},
		{input: "error", want: slog.LevelError, ok: true},
		{input: "bogus", want: slog.LevelInfo, ok: false},
	}

	for _, tc := range testCases {
		got, ok := ParseLevel(tc.input)
		if ok != tc.ok {
			t.Fatalf("ParseLevel(%q) ok=%v, want %v", tc.input, ok, tc.ok)
		}
		if got != tc.want {
			t.Fatalf("ParseLevel(%q)=%v, want %v", tc.input, got, tc.want)
		}
	}
}
