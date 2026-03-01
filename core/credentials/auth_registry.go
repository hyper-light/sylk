package credentials

import (
	"log/slog"
	"sync"
	"time"
)

// AuthEvent describes a credential availability change for a provider.
type AuthEvent struct {
	ProviderType string    // "google", "anthropic", "openai"
	AuthMethod   string    // "oauth", "api_key", "service_account", "chatgpt"
	Available    bool      // true=available, false=revoked
	Timestamp    time.Time
}

// AuthPublisher is a callback invoked when a credential state changes.
// Uses the callback pattern (not interface) to avoid circular dependency
// with downstream packages — same pattern as network/bus_bridge.go:PublishFunc.
type AuthPublisher func(event AuthEvent)

// AuthProbe checks whether credentials are available for a given provider type.
// Returns true when a usable credential exists.
type AuthProbe func(providerType string) bool

// knownProviders enumerates the provider types the registry probes.
// Bounded to exactly 3 entries.
var knownProviders = [3]string{"google", "anthropic", "openai"}

// AuthRegistry tracks credential availability and broadcasts changes.
// Agents start without providers; when credentials become available
// (startup probe or user login), the registry broadcasts via the publisher.
type AuthRegistry struct {
	mu        sync.RWMutex
	latest    map[string]*AuthEvent // bounded: 3 entries max (google, anthropic, openai)
	publisher AuthPublisher
	probe     AuthProbe
	logger    *slog.Logger
}

// NewAuthRegistry creates a registry that probes credentials via the provided
// probe function and broadcasts changes via the publisher callback.
func NewAuthRegistry(probe AuthProbe, publisher AuthPublisher, logger *slog.Logger) *AuthRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthRegistry{
		latest:    make(map[string]*AuthEvent, len(knownProviders)),
		publisher: publisher,
		probe:     probe,
		logger:    logger.With("component", "auth_registry"),
	}
}

// NotifyCredentialChanged probes the credential store for the given provider
// and publishes an AuthEvent reflecting current availability.
func (r *AuthRegistry) NotifyCredentialChanged(providerType, authMethod string) {
	available := r.probeAvailable(providerType)
	method := normalizeMethod(authMethod)
	event := AuthEvent{
		ProviderType: providerType,
		AuthMethod:   method,
		Available:    available,
		Timestamp:    time.Now(),
	}

	r.mu.Lock()
	r.latest[providerType] = &event
	r.mu.Unlock()

	r.logger.Info("credential state changed",
		"provider", providerType,
		"method", method,
		"available", available)

	if r.publisher != nil {
		r.publisher(event)
	}
}

// IsAvailable returns whether credentials are currently known to be
// available for the given provider type.
func (r *AuthRegistry) IsAvailable(providerType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ev, ok := r.latest[providerType]
	return ok && ev.Available
}

// ProbeAll scans all known providers and publishes events for any that are
// available. Called once after DaemonSet agents start to trigger the initial
// credential broadcast.
func (r *AuthRegistry) ProbeAll() {
	for _, provider := range knownProviders {
		available := r.probeAvailable(provider)
		if !available {
			continue
		}
		method := normalizeMethod(LoadAuthPref(provider))
		if method == "" {
			method = "api_key"
		}
		event := AuthEvent{
			ProviderType: provider,
			AuthMethod:   method,
			Available:    true,
			Timestamp:    time.Now(),
		}

		r.mu.Lock()
		r.latest[provider] = &event
		r.mu.Unlock()

		r.logger.Info("initial credential probe",
			"provider", provider,
			"method", method,
			"available", true)

		if r.publisher != nil {
			r.publisher(event)
		}
	}
}

// ActiveMethod returns the auth method currently active for the given
// provider type. Returns "" if no credentials are known.
func (r *AuthRegistry) ActiveMethod(providerType string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ev, ok := r.latest[providerType]
	if !ok || !ev.Available {
		return ""
	}
	return ev.AuthMethod
}

// probeAvailable checks whether credentials are available for the given
// provider type using the configured probe function.
func (r *AuthRegistry) probeAvailable(providerType string) bool {
	if r.probe == nil {
		return false
	}
	return r.probe(providerType)
}

// normalizeMethod maps login panel method labels to provider auth mode
// constants. The login panel stores "apikey" (no underscore) but provider
// configs use "api_key" (with underscore).
func normalizeMethod(method string) string {
	if method == "apikey" {
		return "api_key"
	}
	return method
}
