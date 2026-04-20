package shared

// safeCallString resolves a lazy string getter, returning "" when the
// getter is nil. Used across the per-agent skill wiring code to keep
// nil-safety logic out of every call site.
func safeCallString(fn func() string) string {
	if fn == nil {
		return ""
	}
	return fn()
}
