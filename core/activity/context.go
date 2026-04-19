package activity

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FabricContext is the W3C-Trace-Context-inspired causal carrier the
// fabric propagates through context.Context, bus messages, and skill
// calls. Every chokepoint emission reads its parent span from the
// FabricContext, so the causal DAG (Caused / CausalChain) builds itself
// without manual bookkeeping at call sites.
type FabricContext struct {
	// TraceID identifies the root user-facing request that initiated
	// this chain of work. All activities in the same chain share a
	// TraceID.
	TraceID string

	// SpanID identifies this specific in-flight activity. When a
	// chokepoint emits a Span, it generates a new SpanID; downstream
	// chokepoints invoked under that span see this SpanID as their
	// ParentSpanID.
	SpanID string

	// ParentSpanID identifies the immediately-enclosing span. Used to
	// populate Caused on the emitted activity.
	ParentSpanID string

	// CausalChain is the denormalized list of ancestor span IDs from
	// the root to (but excluding) this span. Enables O(1) DAG walks
	// without recursive joins.
	CausalChain []string

	// Baggage carries cross-cutting context (e.g., session ID, agent
	// ID, pipeline ID) that propagates through every chokepoint and
	// can be denormalized onto activities.
	Baggage map[string]string
}

// fabricContextKey is the unexported context key used to attach a
// FabricContext to a context.Context.
type fabricContextKey struct{}

// WithFabricContext attaches a FabricContext to ctx. Subsequent calls
// to FabricContextFromContext on a derived context will return fc.
func WithFabricContext(ctx context.Context, fc FabricContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, fabricContextKey{}, fc)
}

// FabricContextFromContext extracts the attached FabricContext, or
// returns a zero-value FabricContext (with no TraceID, no SpanID) if
// none is attached. Callers that need a usable trace can call
// EnsureFabricContext to lazily mint one.
func FabricContextFromContext(ctx context.Context) FabricContext {
	if ctx == nil {
		return FabricContext{}
	}
	if fc, ok := ctx.Value(fabricContextKey{}).(FabricContext); ok {
		return fc
	}
	return FabricContext{}
}

// EnsureFabricContext returns a context guaranteed to carry a
// FabricContext with at least a TraceID. If ctx already carries one,
// it's returned unchanged; otherwise a fresh root trace is minted.
//
// Use this at process entry points (TUI startup, top-level request
// handlers) where there is no parent trace to inherit from.
func EnsureFabricContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	fc := FabricContextFromContext(ctx)
	if fc.TraceID != "" {
		return ctx
	}
	fc = FabricContext{
		TraceID: NewID(),
		SpanID:  "",
	}
	return WithFabricContext(ctx, fc)
}

// WithChildSpan returns a new context whose FabricContext represents
// a child span of the current one. Generates a fresh SpanID and
// records the previous SpanID as the parent. The CausalChain is
// extended with the previous SpanID so downstream activities can walk
// ancestors in O(1).
//
// Chokepoint instrumentation should call this at entry and pass the
// returned context to the wrapped operation.
func WithChildSpan(ctx context.Context) (context.Context, FabricContext) {
	parent := FabricContextFromContext(ctx)
	traceID := parent.TraceID
	if traceID == "" {
		traceID = NewID()
	}
	chain := parent.CausalChain
	if parent.SpanID != "" {
		chain = appendString(chain, parent.SpanID)
	}
	child := FabricContext{
		TraceID:      traceID,
		SpanID:       NewID(),
		ParentSpanID: parent.SpanID,
		CausalChain:  chain,
		Baggage:      cloneBaggage(parent.Baggage),
	}
	return WithFabricContext(ctx, child), child
}

// WithBaggage returns a context carrying additional baggage on top of
// any existing FabricContext. Baggage propagates through every
// downstream chokepoint emission.
func WithBaggage(ctx context.Context, key, value string) context.Context {
	fc := FabricContextFromContext(ctx)
	if fc.Baggage == nil {
		fc.Baggage = map[string]string{}
	} else {
		fc.Baggage = cloneBaggage(fc.Baggage)
	}
	fc.Baggage[key] = value
	return WithFabricContext(ctx, fc)
}

func appendString(s []string, v string) []string {
	out := make([]string, len(s)+1)
	copy(out, s)
	out[len(s)] = v
	return out
}

func cloneBaggage(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ─── Activity ID generator ────────────────────────────────────────────
//
// IDs are ULID-like: 48-bit timestamp (millisecond precision) prefix
// followed by 80 random bits. Sortable, monotonic per-process within a
// millisecond, and globally unique with overwhelming probability.

var idMu sync.Mutex
var idLastMs int64
var idLastSeq uint16

// NewID returns a fresh activity ID, sortable by emission time.
func NewID() string {
	now := time.Now().UnixMilli()

	idMu.Lock()
	if now == idLastMs {
		idLastSeq++
	} else {
		idLastMs = now
		idLastSeq = 0
	}
	seq := idLastSeq
	idMu.Unlock()

	var buf [16]byte
	// 6 bytes (48 bits) of millisecond timestamp, big-endian.
	buf[0] = byte(now >> 40)
	buf[1] = byte(now >> 32)
	buf[2] = byte(now >> 24)
	buf[3] = byte(now >> 16)
	buf[4] = byte(now >> 8)
	buf[5] = byte(now)

	// 2 bytes of monotonic per-ms sequence to break ties.
	binary.BigEndian.PutUint16(buf[6:8], seq)

	// 8 bytes of randomness for global uniqueness.
	rndCount, err := rand.Read(buf[8:])
	if err != nil || rndCount != 8 {
		// rand.Read on Linux/macOS only fails under exceptional
		// circumstances; if it does, fall back to a less-random but
		// still unique value derived from process counter so we never
		// emit a duplicate ID.
		fallback := atomic.AddUint64(&idFallbackCounter, 1)
		binary.BigEndian.PutUint64(buf[8:], fallback)
	}

	return strings.ToLower(hex.EncodeToString(buf[:]))
}

var idFallbackCounter uint64
