package ui

import (
	"errors"
	"testing"
)

func TestResolveLoginPanelAPIKeyWithResolvers_PrefersSecure(t *testing.T) {
	secureCalls := 0
	legacyCalls := 0
	got := resolveLoginPanelAPIKeyWithResolvers(
		"google",
		func(_ string) (string, error) {
			secureCalls++
			return "AIza_secure_key_abcdefghijklmnopqrstuvwxyz", nil
		},
		func(_ string) (string, error) {
			legacyCalls++
			return "AIza_legacy_key_abcdefghijklmnopqrstuvwxyz", nil
		},
	)
	if got != "AIza_secure_key_abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("resolved key = %q, want secure key", got)
	}
	if secureCalls != 1 {
		t.Fatalf("secure resolver calls = %d, want 1", secureCalls)
	}
	if legacyCalls != 0 {
		t.Fatalf("legacy resolver calls = %d, want 0", legacyCalls)
	}
}

func TestResolveLoginPanelAPIKeyWithResolvers_FallsBackToLegacy(t *testing.T) {
	got := resolveLoginPanelAPIKeyWithResolvers(
		"google",
		func(_ string) (string, error) {
			return "", errors.New("no secure key")
		},
		func(_ string) (string, error) {
			return "AIza_legacy_key_abcdefghijklmnopqrstuvwxyz", nil
		},
	)
	if got != "AIza_legacy_key_abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("resolved key = %q, want legacy key", got)
	}
}

func TestResolveLoginPanelAPIKeyWithResolvers_RejectsInvalidFormat(t *testing.T) {
	got := resolveLoginPanelAPIKeyWithResolvers(
		"google",
		func(_ string) (string, error) {
			return "not_a_google_key", nil
		},
		func(_ string) (string, error) {
			return "also_not_a_google_key", nil
		},
	)
	if got != "" {
		t.Fatalf("resolved key = %q, want empty", got)
	}
}
