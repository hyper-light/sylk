package providers

import (
	"strings"
	"testing"
)

func TestEnsureToolCallID_PassesThroughNonEmpty(t *testing.T) {
	if got := EnsureToolCallID("tc_abc123"); got != "tc_abc123" {
		t.Fatalf("EnsureToolCallID(%q) = %q, want %q", "tc_abc123", got, "tc_abc123")
	}
}

func TestEnsureToolCallID_TrimsWhitespace(t *testing.T) {
	if got := EnsureToolCallID("  tc_x  "); got != "tc_x" {
		t.Fatalf("EnsureToolCallID trimmed = %q, want %q", got, "tc_x")
	}
}

func TestEnsureToolCallID_MintsOnEmpty(t *testing.T) {
	got := EnsureToolCallID("")
	if got == "" {
		t.Fatal("EnsureToolCallID on empty returned empty; expected a minted ID")
	}
	if !strings.HasPrefix(got, "tc_") {
		t.Fatalf("minted ID = %q, want prefix %q", got, "tc_")
	}
	// Minted IDs must be unique across calls so streams don't collide.
	other := EnsureToolCallID("")
	if other == got {
		t.Fatalf("two mints returned the same ID %q", got)
	}
}

func TestEnsureToolCallID_MintsOnWhitespaceOnly(t *testing.T) {
	got := EnsureToolCallID("   \t\n")
	if got == "" {
		t.Fatal("EnsureToolCallID on whitespace-only returned empty")
	}
	if !strings.HasPrefix(got, "tc_") {
		t.Fatalf("minted ID = %q, want prefix %q", got, "tc_")
	}
}
