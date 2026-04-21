package fabriclog

import (
	"context"

	"github.com/adalundhe/sylk/core/activity"
)

// RecordingSink wraps an activity.Sink and emits KindPublish (plus
// KindResolve on resolution edges) records into the FabricLogger
// for every Append call. The underlying sink is always invoked
// first so the fabric's own persistence path is never blocked or
// delayed by the observability layer.
//
// Install via NewRecordingSink and activity.SetDefaultSink. When the
// logger is nil the decorator is a transparent passthrough — the
// wrapping cost is one nil check per Append.
type RecordingSink struct {
	inner  activity.Sink
	logger *FabricLogger
}

// NewRecordingSink wraps sink with observability. If logger is nil,
// the returned decorator behaves exactly like sink. If sink is nil,
// the inner store is treated as a discard sink (emissions still
// record to the logger).
func NewRecordingSink(sink activity.Sink, logger *FabricLogger) *RecordingSink {
	return &RecordingSink{inner: sink, logger: logger}
}

// Append routes the activity to the inner sink and emits a
// KindPublish record into the logger. Never blocks on log I/O —
// enqueue is non-blocking by contract.
func (r *RecordingSink) Append(ctx context.Context, a activity.AgentActivity) {
	if r == nil {
		return
	}
	if r.inner != nil {
		r.inner.Append(ctx, a)
	}
	if r.logger != nil {
		r.logger.RecordPublish(a)
	}
}

// Inner returns the wrapped sink. Useful for teardown paths that
// need to close or replace the underlying store without disturbing
// the observability layer.
func (r *RecordingSink) Inner() activity.Sink {
	if r == nil {
		return nil
	}
	return r.inner
}
