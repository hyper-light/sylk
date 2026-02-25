package credentials

import (
	"log/slog"
	"sync"
	"testing"
)

// newTestRegistry builds an AuthRegistry with a stub probe that checks the
// given key map. The returned slice collects published events.
func newTestRegistry(keys map[string]string) (*AuthRegistry, *[]AuthEvent) {
	var published []AuthEvent
	var mu sync.Mutex
	publisher := func(event AuthEvent) {
		mu.Lock()
		published = append(published, event)
		mu.Unlock()
	}

	probe := func(providerType string) bool {
		key, ok := keys[providerType]
		return ok && key != ""
	}

	reg := NewAuthRegistry(probe, publisher, slog.Default())
	return reg, &published
}

func TestProbeAll_MixedAvailability(t *testing.T) {
	reg, published := newTestRegistry(map[string]string{
		"google": "gkey",
		"openai": "okey",
		// anthropic missing
	})

	reg.ProbeAll()

	if len(*published) != 2 {
		t.Fatalf("expected 2 events, got %d", len(*published))
	}

	// Verify google and openai are available.
	for _, ev := range *published {
		if !ev.Available {
			t.Errorf("expected Available=true for %s", ev.ProviderType)
		}
		if ev.ProviderType != "google" && ev.ProviderType != "openai" {
			t.Errorf("unexpected provider %s", ev.ProviderType)
		}
	}

	if reg.IsAvailable("anthropic") {
		t.Error("anthropic should not be available")
	}
}

func TestNotifyCredentialChanged_Publishes(t *testing.T) {
	reg, published := newTestRegistry(map[string]string{
		"openai": "key123",
	})

	reg.NotifyCredentialChanged("openai", "api_key")

	if len(*published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(*published))
	}
	ev := (*published)[0]
	if ev.ProviderType != "openai" || !ev.Available || ev.AuthMethod != "api_key" {
		t.Errorf("unexpected event: %+v", ev)
	}
}

func TestIsAvailable_CorrectState(t *testing.T) {
	reg, _ := newTestRegistry(map[string]string{
		"google": "gkey",
	})

	reg.ProbeAll()

	if !reg.IsAvailable("google") {
		t.Error("google should be available")
	}
	if reg.IsAvailable("openai") {
		t.Error("openai should not be available")
	}
	if reg.IsAvailable("anthropic") {
		t.Error("anthropic should not be available")
	}
}

func TestLatest_Bounded(t *testing.T) {
	reg, _ := newTestRegistry(map[string]string{
		"google":    "g",
		"anthropic": "a",
		"openai":    "o",
	})

	reg.ProbeAll()

	reg.mu.RLock()
	count := len(reg.latest)
	reg.mu.RUnlock()

	if count != 3 {
		t.Errorf("expected exactly 3 entries in latest, got %d", count)
	}
}
