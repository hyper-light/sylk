package shared

// ReadinessReporter is implemented by agents that can be in a transient
// not-yet-ready state — typically because their LLM provider is wired
// asynchronously after agent construction (post-auth, post-credential-
// refresh). The dispatcher and activation controller check Ready() before
// routing or promoting so unready agents fail at the routing boundary
// rather than producing the historical "no LLM provider configured" error
// from the deepest layer.
//
// Agents that don't depend on an LLM (or that wire their dependencies
// synchronously at construction) DO NOT need to implement this — the
// dispatcher's type-assertion treats absence as "always ready," matching
// the pre-refactor behavior.
//
// Pair with shared.ErrAgentNotReady: when a Ready() check fails, callers
// should surface ErrAgentNotReady so retry-after-readiness semantics
// propagate.
type ReadinessReporter interface {
	// Ready reports whether the agent has all live dependencies needed to
	// serve LLM-backed work. Returns (false, reason) when not ready; the
	// reason is attached to ErrAgentNotReady-wrapped errors for diagnosis.
	// Implementations MUST be safe to call concurrently and MUST be cheap
	// — Ready() is queried on every routing decision.
	Ready() (ready bool, reason string)
}

// ReadyOrNotReady reports the readiness of any agent. Agents that don't
// implement ReadinessReporter are treated as always ready (the canonical
// fallback for agents whose dependencies are wired synchronously at
// construction). Returns (false, reason) only when the agent both
// implements ReadinessReporter and reports not-ready.
//
// Use this at every routing/dispatch boundary that touches an LLM-
// dependent agent so unready agents fail with a clear, attributed error
// rather than reaching the deepest tool-loop layer.
func ReadyOrNotReady(agent any) (bool, string) {
	reporter, ok := agent.(ReadinessReporter)
	if !ok {
		return true, ""
	}
	return reporter.Ready()
}
