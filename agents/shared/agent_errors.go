package shared

import (
	"errors"
)

// Sentinel errors that classify agent-side failures by recovery semantics.
// Tool loops and dispatchers branch on the error class to decide whether to
// abort, retry, queue, or surface to the user.
//
// Background: an agent that hits "no LLM provider configured" deep inside a
// tool handler used to surface that failure as a generic error string. The
// caller could not tell whether this was a permanent misconfiguration (e.g.
// a missing API key, surface to user) or a transient startup race (provider
// being wired post-construction, retry after readiness). Both produced the
// same error message and the same caller behavior. Classifying the failure
// here lets the dispatcher refuse the call cleanly when transient, surface
// the error when permanent, and keep them visually distinct in logs.
var (
	// ErrAgentNotReady indicates the agent received a request that requires
	// a capability whose dependencies are not yet wired (typically the LLM
	// provider during the post-init / pre-auth window). Recoverable —
	// callers should retry once the agent's Ready() returns true rather
	// than treat this as a permanent failure. Wrap via fmt.Errorf("%w: …",
	// ErrAgentNotReady, detail) to add context while preserving the kind.
	ErrAgentNotReady = errors.New("agent not ready")

	// ErrAgentMisconfigured indicates a permanent configuration defect that
	// will not resolve through retry — wrong model name, missing required
	// skill registration, structurally invalid agent setup. Surface to the
	// user. Wrap via fmt.Errorf("%w: …", ErrAgentMisconfigured, detail).
	ErrAgentMisconfigured = errors.New("agent misconfigured")
)

// IsAgentNotReady reports whether err is or wraps ErrAgentNotReady.
func IsAgentNotReady(err error) bool {
	return errors.Is(err, ErrAgentNotReady)
}

// IsAgentMisconfigured reports whether err is or wraps ErrAgentMisconfigured.
func IsAgentMisconfigured(err error) bool {
	return errors.Is(err, ErrAgentMisconfigured)
}
