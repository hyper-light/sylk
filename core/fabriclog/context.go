package fabriclog

import "context"

// readerCtxKey carries the identity of the agent performing a read
// through context. Populating it at lens entry points lets the
// RecordingSource attribute KindRead and KindConsume records to the
// calling agent. When the key is absent, reader identity is empty —
// the record still carries filter + returned IDs, just without
// caller attribution.
type readerCtxKey struct{}

// WithReader attaches reader identity to ctx. Call at lens skill
// entry points (e.g. query_peer_activity skill handler) so the
// RecordingSource knows who is asking. The wrapping is cheap and the
// value is immutable, so callers can safely re-wrap a ctx that
// already carries a reader — the deepest wrap wins.
func WithReader(ctx context.Context, r AgentRef) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, readerCtxKey{}, r)
}

// ReaderFromContext returns the reader identity previously attached
// via WithReader. Zero-value AgentRef when absent.
func ReaderFromContext(ctx context.Context) AgentRef {
	if ctx == nil {
		return AgentRef{}
	}
	if v, ok := ctx.Value(readerCtxKey{}).(AgentRef); ok {
		return v
	}
	return AgentRef{}
}
