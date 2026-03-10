package credentials

import (
	"log/slog"
	"maps"
	"sort"
	"sync"
	"time"
)

// AuthEvent describes a credential state change for a provider.
type AuthEvent struct {
	ProviderType    string // "google", "anthropic", "openai"
	AuthMethod      string // canonical active method: "oauth", "api_key", "service_account", "chatgpt"
	PreferredMethod string // canonical persisted preference from config
	Available       bool   // true when at least one usable credential exists
	Timestamp       time.Time
}

// AuthPublisher is a callback invoked when a credential state changes.
// Uses the callback pattern (not interface) to avoid circular dependency
// with downstream packages.
type AuthPublisher func(event AuthEvent)

// AuthMethodResolver returns provider-native auth methods that are currently
// usable for the given provider type.
type AuthMethodResolver func(providerType string) map[string]bool

// AuthPreferenceStore persists the preferred auth mode in project config.
type AuthPreferenceStore interface {
	Load(provider string) string
	Save(provider, authMode string) error
}

type authPreferenceStore struct{}

func (authPreferenceStore) Load(provider string) string {
	return LoadAuthPref(provider)
}

func (authPreferenceStore) Save(provider, authMode string) error {
	return SaveAuthPref(provider, authMode)
}

// ProviderAuthState is the centralized runtime auth view for one provider.
type ProviderAuthState struct {
	ProviderType      string
	PreferredMethod   string
	ActiveMethod      string
	Available         bool
	AvailableMethods  map[string]bool
	LastResolvedAtUTC time.Time
}

// knownProviders enumerates the provider types the registry tracks.
var knownProviders = [3]string{"google", "anthropic", "openai"}

// AuthRegistry tracks provider auth state and broadcasts effective changes.
// It is the runtime source of truth for auth resolution; project config is
// only the persisted preference backing store.
type AuthRegistry struct {
	mu        sync.RWMutex
	latest    map[string]*ProviderAuthState
	publisher AuthPublisher
	resolver  AuthMethodResolver
	store     AuthPreferenceStore
	logger    *slog.Logger
}

// NewAuthRegistry creates a registry that resolves auth methods centrally and
// broadcasts effective state changes.
func NewAuthRegistry(
	resolver AuthMethodResolver,
	store AuthPreferenceStore,
	publisher AuthPublisher,
	logger *slog.Logger,
) *AuthRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		store = authPreferenceStore{}
	}
	return &AuthRegistry{
		latest:    make(map[string]*ProviderAuthState, len(knownProviders)),
		publisher: publisher,
		resolver:  resolver,
		store:     store,
		logger:    logger.With("component", "auth_registry"),
	}
}

// NotifyCredentialChanged persists the preferred auth mode for the provider,
// re-resolves provider auth state, and publishes the effective result.
func (r *AuthRegistry) NotifyCredentialChanged(providerType, authMethod string) {
	providerType = normalizeAuthProvider(providerType)
	if providerType == "" {
		return
	}
	canonical := CanonicalAuthMethod(providerType, authMethod)
	if canonical != "" && r.store != nil {
		if err := r.store.Save(providerType, canonical); err != nil {
			r.logger.Warn("persist preferred auth method failed",
				"provider", providerType,
				"method", canonical,
				"error", err)
		}
	}
	r.refreshProvider(providerType, true, "credential state changed")
}

// IsAvailable returns whether any usable credentials are known for the
// provider type.
func (r *AuthRegistry) IsAvailable(providerType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.latest[normalizeAuthProvider(providerType)]
	return ok && state.Available
}

// ActiveMethod returns the effective runtime auth mode for the provider.
func (r *AuthRegistry) ActiveMethod(providerType string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.latest[normalizeAuthProvider(providerType)]
	if !ok || !state.Available {
		return ""
	}
	return state.ActiveMethod
}

// PreferredMethod returns the persisted auth preference for the provider.
func (r *AuthRegistry) PreferredMethod(providerType string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.latest[normalizeAuthProvider(providerType)]
	if !ok {
		return ""
	}
	return state.PreferredMethod
}

// State returns a copy of the provider auth state.
func (r *AuthRegistry) State(providerType string) ProviderAuthState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.latest[normalizeAuthProvider(providerType)]
	if !ok || state == nil {
		return ProviderAuthState{ProviderType: normalizeAuthProvider(providerType)}
	}
	return cloneProviderAuthState(*state)
}

// ProbeAll re-resolves and publishes current provider auth state for all
// tracked providers.
func (r *AuthRegistry) ProbeAll() {
	r.probeAll(true)
}

// PrimeAll seeds runtime state without publishing.
func (r *AuthRegistry) PrimeAll() {
	r.probeAll(false)
}

func (r *AuthRegistry) probeAll(publish bool) {
	for _, provider := range knownProviders {
		r.refreshProvider(provider, publish, "auth probe")
	}
}

func (r *AuthRegistry) refreshProvider(providerType string, publish bool, logMsg string) {
	state := r.resolveProviderState(providerType)
	r.recordState(state, publish, logMsg)
}

func (r *AuthRegistry) resolveProviderState(providerType string) ProviderAuthState {
	providerType = normalizeAuthProvider(providerType)
	preferred := ""
	if r.store != nil {
		preferred = CanonicalAuthMethod(providerType, r.store.Load(providerType))
	}
	availableMethods := r.resolveAvailableMethods(providerType)
	active := selectActiveMethod(providerType, preferred, availableMethods)
	return ProviderAuthState{
		ProviderType:      providerType,
		PreferredMethod:   preferred,
		ActiveMethod:      active,
		Available:         active != "",
		AvailableMethods:  availableMethods,
		LastResolvedAtUTC: time.Now().UTC(),
	}
}

func (r *AuthRegistry) resolveAvailableMethods(providerType string) map[string]bool {
	resolved := make(map[string]bool)
	if r.resolver == nil {
		return resolved
	}
	for method, available := range r.resolver(providerType) {
		canonical := CanonicalAuthMethod(providerType, method)
		if canonical == "" {
			continue
		}
		if available {
			resolved[canonical] = true
		}
	}
	return resolved
}

func selectActiveMethod(providerType, preferred string, available map[string]bool) string {
	providerType = normalizeAuthProvider(providerType)
	preferred = CanonicalAuthMethod(providerType, preferred)
	if preferred != "" && available[preferred] {
		return preferred
	}
	for _, method := range authFallbackOrder(providerType) {
		if available[method] {
			return method
		}
	}
	return ""
}

func (r *AuthRegistry) recordState(state ProviderAuthState, publish bool, logMsg string) {
	state = cloneProviderAuthState(state)

	r.mu.Lock()
	r.latest[state.ProviderType] = &state
	r.mu.Unlock()

	r.logger.Info(logMsg,
		"provider", state.ProviderType,
		"preferred_method", state.PreferredMethod,
		"active_method", state.ActiveMethod,
		"available", state.Available,
		"available_methods", availableMethodNames(state.AvailableMethods))

	if publish && r.publisher != nil {
		r.publisher(AuthEvent{
			ProviderType:    state.ProviderType,
			AuthMethod:      state.ActiveMethod,
			PreferredMethod: state.PreferredMethod,
			Available:       state.Available,
			Timestamp:       state.LastResolvedAtUTC,
		})
	}
}

func cloneProviderAuthState(state ProviderAuthState) ProviderAuthState {
	state.AvailableMethods = maps.Clone(state.AvailableMethods)
	return state
}

func availableMethodNames(methods map[string]bool) []string {
	names := make([]string, 0, len(methods))
	for method, available := range methods {
		if available {
			names = append(names, method)
		}
	}
	sort.Strings(names)
	return names
}
