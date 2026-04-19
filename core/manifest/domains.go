package manifest

import (
	"fmt"
	"strings"
	"sync"
)

// DomainSpec describes a registered decision domain. Domains are registered
// at process start (typically in package init) and are visible to every
// agent that imports the manifest package. New domains can be added by any
// package — coordination service, pipeline protocol, agent-specific specs —
// without touching the manifest core.
type DomainSpec struct {
	// Name is the wire identifier (e.g. "test_framework"). Lowercase
	// snake_case by convention.
	Name string
	// Description is a short human-readable purpose statement; surfaced
	// in skill tool definitions and the TUI Decisions panel.
	Description string
	// RecommendedDimensions lists scope dimensions that are typically
	// relevant to this domain. Advisory only — declarations may include
	// other dimensions (the system tags them as "extended scope" but
	// otherwise honors them).
	RecommendedDimensions []Dimension
	// Compatibility compares two candidate values. Required for any
	// domain where automatic conflict-detection should distinguish
	// "version drift" / "equivalent framings" from genuine disagreement.
	// If nil, the system treats different values as Incompatible.
	Compatibility func(a, b string) Compatibility
	// ResolutionPolicy determines how matching decisions rank during
	// query resolution. Defaults to ResolvePolicySpecificityFirstMover
	// when unset.
	ResolutionPolicy ResolutionPolicy
}

var (
	domainRegistryMu sync.RWMutex
	domainRegistry   = map[string]DomainSpec{}
)

// RegisterDomain installs spec under spec.Name. Re-registering the same
// name overwrites — useful in tests, harmless in production where each
// domain is registered exactly once at init time. Empty Name panics.
func RegisterDomain(spec DomainSpec) {
	if strings.TrimSpace(spec.Name) == "" {
		panic("manifest.RegisterDomain: spec.Name is required")
	}
	if spec.ResolutionPolicy == "" {
		spec.ResolutionPolicy = ResolvePolicySpecificityFirstMover
	}
	domainRegistryMu.Lock()
	defer domainRegistryMu.Unlock()
	domainRegistry[spec.Name] = spec
}

// LookupDomain returns the spec for name. The bool is false when the
// domain is unknown — callers should treat unknown domains as legitimate
// (open vocabulary) but use the conservative default behavior:
// Incompatibility on value differences, specificity-first-mover policy.
func LookupDomain(name string) (DomainSpec, bool) {
	domainRegistryMu.RLock()
	defer domainRegistryMu.RUnlock()
	spec, ok := domainRegistry[name]
	return spec, ok
}

// RegisteredDomains returns a snapshot of all registered domains. Used by
// skill construction to enumerate the currently known decision space.
func RegisteredDomains() []DomainSpec {
	domainRegistryMu.RLock()
	defer domainRegistryMu.RUnlock()
	out := make([]DomainSpec, 0, len(domainRegistry))
	for _, spec := range domainRegistry {
		out = append(out, spec)
	}
	return out
}

// CompatibilityFor invokes the domain's Compatibility predicate, falling
// back to a conservative default when the domain is unregistered or
// declared no predicate. Equal values always report Equivalent regardless
// of registration state.
func CompatibilityFor(domain string, a, b string) Compatibility {
	an, bn := strings.TrimSpace(strings.ToLower(a)), strings.TrimSpace(strings.ToLower(b))
	if an == bn {
		return CompatibilityEquivalent
	}
	spec, ok := LookupDomain(domain)
	if !ok || spec.Compatibility == nil {
		return CompatibilityIncompatible
	}
	return spec.Compatibility(a, b)
}

// PolicyFor returns the resolution policy for domain (defaults to
// ResolvePolicySpecificityFirstMover for unregistered domains).
func PolicyFor(domain string) ResolutionPolicy {
	spec, ok := LookupDomain(domain)
	if !ok || spec.ResolutionPolicy == "" {
		return ResolvePolicySpecificityFirstMover
	}
	return spec.ResolutionPolicy
}

// ValidateDeclareInput performs basic shape validation on an inbound
// declare payload. Returns a precise error so the agent can correct
// the declaration without round-tripping through the LLM.
func ValidateDeclareInput(in *DeclareDecisionInput) error {
	if in == nil {
		return fmt.Errorf("declare_decision: payload is required")
	}
	if strings.TrimSpace(in.Domain) == "" {
		return fmt.Errorf("declare_decision: domain is required")
	}
	if strings.TrimSpace(in.Value) == "" {
		return fmt.Errorf("declare_decision: value is required")
	}
	switch in.Confidence {
	case ConfidenceTentative, ConfidenceCommitted, ConfidenceConsensus:
		// fine — Hint is reserved for system-injected priors and not
		// permitted as an agent-supplied declaration confidence.
	case "":
		in.Confidence = ConfidenceTentative
	case ConfidenceHint:
		return fmt.Errorf("declare_decision: confidence=%q is reserved for system-supplied priors", in.Confidence)
	default:
		return fmt.Errorf("declare_decision: confidence=%q is not one of tentative|committed|consensus", in.Confidence)
	}
	return nil
}
