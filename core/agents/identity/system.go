package identity

// This file carries small, self-contained helpers related to the
// CategorySystem branch of the identity model. The factory itself
// defines the sentinel SystemTaskUID (task.go) and SystemIdentity
// mint path (factory.go). Here we surface a shared-purpose predicate
// used by the accountant and the gateway.

// IsSystem reports whether an identity is a CategorySystem identity
// (OAuth refresh, health probes, etc.). Used by the accountant to
// skip billable-totals while still retaining the record, and by
// log handlers to render system calls distinctly.
func IsSystem(id *AgentIdentity) bool {
	if id == nil {
		return false
	}
	return id.category == CategorySystem
}

// SystemPurpose returns the free-form purpose tag assigned to a
// SystemIdentity at mint time (e.g. "oauth-refresh"). Empty for
// non-system identities.
func SystemPurpose(id *AgentIdentity) string {
	if !IsSystem(id) {
		return ""
	}
	return id.labels.Get("sylk/purpose")
}
