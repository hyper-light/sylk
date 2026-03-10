package credentials

import (
	"log/slog"
	"sync"
	"testing"
)

type testAuthPrefStore struct {
	mu    sync.Mutex
	prefs map[string]string
}

func newTestAuthPrefStore(prefs map[string]string) *testAuthPrefStore {
	cloned := make(map[string]string, len(prefs))
	for provider, method := range prefs {
		cloned[provider] = method
	}
	return &testAuthPrefStore{prefs: cloned}
}

func (s *testAuthPrefStore) Load(provider string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs[provider]
}

func (s *testAuthPrefStore) Save(provider, authMode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefs == nil {
		s.prefs = make(map[string]string)
	}
	s.prefs[provider] = authMode
	return nil
}

func newTestRegistry(
	resolver map[string]map[string]bool,
	prefs map[string]string,
) (*AuthRegistry, *[]AuthEvent) {
	var published []AuthEvent
	var mu sync.Mutex
	publisher := func(event AuthEvent) {
		mu.Lock()
		published = append(published, event)
		mu.Unlock()
	}

	resolve := func(providerType string) map[string]bool {
		methods := resolver[providerType]
		cloned := make(map[string]bool, len(methods))
		for method, available := range methods {
			cloned[method] = available
		}
		return cloned
	}

	reg := NewAuthRegistry(resolve, newTestAuthPrefStore(prefs), publisher, slog.Default())
	return reg, &published
}

func TestProbeAll_ResolvesPreferredAndFallbackMethods(t *testing.T) {
	reg, published := newTestRegistry(
		map[string]map[string]bool{
			"google":    {"service_account": true},
			"anthropic": {"oauth": true},
			"openai":    {"chatgpt": true},
		},
		map[string]string{
			"google":    "oauth",
			"anthropic": "api_key",
			"openai":    "oauth",
		},
	)

	reg.ProbeAll()

	if len(*published) != 3 {
		t.Fatalf("expected 3 events, got %d", len(*published))
	}

	if got := reg.ActiveMethod("google"); got != "service_account" {
		t.Fatalf("google active method = %q, want service_account", got)
	}
	if got := reg.PreferredMethod("google"); got != "oauth" {
		t.Fatalf("google preferred method = %q, want oauth", got)
	}

	if got := reg.ActiveMethod("anthropic"); got != "oauth" {
		t.Fatalf("anthropic active method = %q, want oauth", got)
	}
	if got := reg.PreferredMethod("anthropic"); got != "api_key" {
		t.Fatalf("anthropic preferred method = %q, want api_key", got)
	}

	// OpenAI legacy "oauth" preference is canonicalized to "chatgpt".
	if got := reg.ActiveMethod("openai"); got != "chatgpt" {
		t.Fatalf("openai active method = %q, want chatgpt", got)
	}
	if got := reg.PreferredMethod("openai"); got != "chatgpt" {
		t.Fatalf("openai preferred method = %q, want chatgpt", got)
	}
}

func TestNotifyCredentialChanged_PersistsCanonicalProviderMethod(t *testing.T) {
	store := newTestAuthPrefStore(nil)
	var published []AuthEvent
	reg := NewAuthRegistry(
		func(providerType string) map[string]bool {
			if providerType == "openai" {
				return map[string]bool{"chatgpt": true}
			}
			return nil
		},
		store,
		func(event AuthEvent) { published = append(published, event) },
		slog.Default(),
	)

	reg.NotifyCredentialChanged("openai", "oauth")

	if got := store.Load("openai"); got != "chatgpt" {
		t.Fatalf("stored openai method = %q, want chatgpt", got)
	}
	if got := reg.ActiveMethod("openai"); got != "chatgpt" {
		t.Fatalf("active openai method = %q, want chatgpt", got)
	}
	if len(published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(published))
	}
	if published[0].AuthMethod != "chatgpt" {
		t.Fatalf("published method = %q, want chatgpt", published[0].AuthMethod)
	}
}

func TestPrimeAll_SeedsUnavailableProvidersWithoutPublishing(t *testing.T) {
	reg, published := newTestRegistry(
		map[string]map[string]bool{
			"google": {},
		},
		map[string]string{
			"google": "oauth",
		},
	)

	reg.PrimeAll()

	if len(*published) != 0 {
		t.Fatalf("expected 0 published events, got %d", len(*published))
	}
	if reg.IsAvailable("google") {
		t.Fatal("google should be unavailable")
	}
	if got := reg.PreferredMethod("google"); got != "oauth" {
		t.Fatalf("preferred google method = %q, want oauth", got)
	}
	if got := reg.ActiveMethod("google"); got != "" {
		t.Fatalf("active google method = %q, want empty", got)
	}
}

func TestState_ReturnsMethodAvailabilitySnapshot(t *testing.T) {
	reg, _ := newTestRegistry(
		map[string]map[string]bool{
			"google": {
				"oauth":           true,
				"service_account": true,
			},
		},
		map[string]string{
			"google": "oauth",
		},
	)

	reg.PrimeAll()
	state := reg.State("google")

	if !state.Available {
		t.Fatal("expected google to be available")
	}
	if !state.AvailableMethods["oauth"] {
		t.Fatal("expected oauth availability")
	}
	if !state.AvailableMethods["service_account"] {
		t.Fatal("expected service_account availability")
	}
	if state.AvailableMethods["api_key"] {
		t.Fatal("did not expect api_key availability")
	}
}

func TestCanonicalAuthMethod(t *testing.T) {
	tests := []struct {
		provider string
		input    string
		want     string
	}{
		{provider: "openai", input: "oauth", want: "chatgpt"},
		{provider: "openai", input: "apikey", want: "api_key"},
		{provider: "google", input: "oauth", want: "oauth"},
		{provider: "anthropic", input: "apikey", want: "api_key"},
		{provider: "anthropic", input: "", want: ""},
	}
	for _, tc := range tests {
		if got := CanonicalAuthMethod(tc.provider, tc.input); got != tc.want {
			t.Fatalf("CanonicalAuthMethod(%q, %q) = %q, want %q", tc.provider, tc.input, got, tc.want)
		}
	}
}
