package providers

import (
	"errors"
	"testing"
)

func TestResolveGoogleProviderAPIKeyWithResolvers_PrefersGeminiEnv(t *testing.T) {
	lookup := envLookupMap(map[string]string{
		"GEMINI_API_KEY": " gemini_env_key ",
		"GOOGLE_API_KEY": "google_env_key",
	})
	secureCalled := 0
	legacyCalled := 0
	got := resolveGoogleProviderAPIKeyWithResolvers(
		lookup,
		func() string {
			secureCalled++
			return "secure_key"
		},
		func() (string, error) {
			legacyCalled++
			return "legacy_key", nil
		},
	)
	if got != "gemini_env_key" {
		t.Fatalf("resolved key = %q, want gemini_env_key", got)
	}
	if secureCalled != 0 {
		t.Fatalf("secure resolver called %d times, want 0", secureCalled)
	}
	if legacyCalled != 0 {
		t.Fatalf("legacy resolver called %d times, want 0", legacyCalled)
	}
}

func TestResolveGoogleProviderAPIKeyWithResolvers_UsesGoogleEnvFallback(t *testing.T) {
	lookup := envLookupMap(map[string]string{
		"GOOGLE_API_KEY": " google_env_key ",
	})
	got := resolveGoogleProviderAPIKeyWithResolvers(lookup, nil, nil)
	if got != "google_env_key" {
		t.Fatalf("resolved key = %q, want google_env_key", got)
	}
}

func TestResolveGoogleProviderAPIKeyWithResolvers_UsesSecureStoreBeforeLegacy(t *testing.T) {
	secureCalled := 0
	legacyCalled := 0
	got := resolveGoogleProviderAPIKeyWithResolvers(
		envLookupMap(nil),
		func() string {
			secureCalled++
			return " secure_key "
		},
		func() (string, error) {
			legacyCalled++
			return "legacy_key", nil
		},
	)
	if got != "secure_key" {
		t.Fatalf("resolved key = %q, want secure_key", got)
	}
	if secureCalled != 1 {
		t.Fatalf("secure resolver called %d times, want 1", secureCalled)
	}
	if legacyCalled != 0 {
		t.Fatalf("legacy resolver called %d times, want 0", legacyCalled)
	}
}

func TestResolveGoogleProviderAPIKeyWithResolvers_UsesLegacyWhenSecureMissing(t *testing.T) {
	legacyCalled := 0
	got := resolveGoogleProviderAPIKeyWithResolvers(
		envLookupMap(nil),
		func() string { return "" },
		func() (string, error) {
			legacyCalled++
			return " legacy_key ", nil
		},
	)
	if got != "legacy_key" {
		t.Fatalf("resolved key = %q, want legacy_key", got)
	}
	if legacyCalled != 1 {
		t.Fatalf("legacy resolver called %d times, want 1", legacyCalled)
	}
}

func TestResolveGoogleProviderAPIKeyWithResolvers_ReturnsEmptyOnLegacyError(t *testing.T) {
	got := resolveGoogleProviderAPIKeyWithResolvers(
		envLookupMap(nil),
		func() string { return "" },
		func() (string, error) { return "", errors.New("boom") },
	)
	if got != "" {
		t.Fatalf("resolved key = %q, want empty", got)
	}
}

func envLookupMap(values map[string]string) func(string) string {
	return func(key string) string {
		if values == nil {
			return ""
		}
		return values[key]
	}
}
