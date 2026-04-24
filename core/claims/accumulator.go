package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// TestamentAccumulator collects observations within any bounded
// lifecycle — a request, a response relay, a planning protocol, a
// session — and flushes them as a single composite testament when the
// lifecycle completes. This avoids flooding the board with per-event
// testaments while preserving full audit trail fidelity.
//
// The lifecycle boundary is defined by the caller, not the type.
// A request handler creates and flushes one accumulator per request.
// A response handler creates one per response relay. A session manager
// creates one per session. Each produces one testament with all
// accumulated artifacts.
//
// Usage:
//
//	acc := claims.NewTestamentAccumulator("librarian", sessionID)
//	ctx = claims.WithTestamentAccumulator(ctx, acc)
//	defer acc.Flush(ctx, board, scope)
//
//	// Later, anywhere in the call chain:
//	if acc := claims.AccumulatorFromContext(ctx); acc != nil {
//	    acc.Record("search_result", "grep foo → 12 files")
//	}
type TestamentAccumulator struct {
	mu        sync.Mutex
	agentID   string
	sessionID string
	artifacts []*Artifact
	notes     []string
	started   time.Time
	flushed   bool
}

// NewTestamentAccumulator creates an accumulator for a request lifecycle.
func NewTestamentAccumulator(agentID, sessionID string) *TestamentAccumulator {
	return &TestamentAccumulator{
		agentID:   agentID,
		sessionID: sessionID,
		started:   time.Now().UTC(),
	}
}

// Record appends a single artifact observation. Thread-safe.
// No board interaction — artifacts are buffered until Flush.
func (a *TestamentAccumulator) Record(kind, reference string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.artifacts = append(a.artifacts, &Artifact{
		AgentID:   a.agentID,
		SessionID: a.sessionID,
		Kind:      kind,
		Reference: reference,
	})
	a.mu.Unlock()
}

// RecordJSON appends an artifact with a JSON-serialized reference.
func (a *TestamentAccumulator) RecordJSON(kind string, value any) {
	if a == nil {
		return
	}
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	a.Record(kind, string(ref))
}

// Note appends a human-readable summary line. Notes are joined
// into the testament's Summary field at flush time.
func (a *TestamentAccumulator) Note(summary string) {
	if a == nil || strings.TrimSpace(summary) == "" {
		return
	}
	a.mu.Lock()
	a.notes = append(a.notes, strings.TrimSpace(summary))
	a.mu.Unlock()
}

// Len returns the number of accumulated artifacts.
func (a *TestamentAccumulator) Len() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	n := len(a.artifacts)
	a.mu.Unlock()
	return n
}

// Flush submits the accumulated artifacts as a single composite
// testament to the board. No-op if empty or already flushed.
// Best-effort — board unavailability does not fail the caller.
//
// If scope is non-nil, the submission is dispatched async.
func (a *TestamentAccumulator) Flush(ctx context.Context, board *ClaimsBoard, scope ScopeProvider) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.flushed || len(a.artifacts) == 0 {
		a.mu.Unlock()
		return
	}
	a.flushed = true
	artifacts := make([]*Artifact, len(a.artifacts))
	copy(artifacts, a.artifacts)
	notes := make([]string, len(a.notes))
	copy(notes, a.notes)
	a.mu.Unlock()

	if board == nil {
		return
	}

	// Duration artifact.
	elapsed := time.Since(a.started)
	artifacts = append(artifacts, &Artifact{
		AgentID:   a.agentID,
		SessionID: a.sessionID,
		Kind:      "request_duration_ms",
		Reference: fmt.Sprintf("%d", elapsed.Milliseconds()),
	})

	summary := fmt.Sprintf("Request observations: %d artifacts", len(artifacts))
	if len(notes) > 0 {
		summary = strings.Join(notes, "; ")
	}

	testament := Testament{
		AgentID:    a.agentID,
		SessionID:  a.sessionID,
		Summary:    summary,
		Confidence: "committed",
		Relations: []Relation{
			{Related: a.agentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
	action := Action{AgentID: a.agentID, Type: ActionTypeTestament}

	submit := func(sctx context.Context) error {
		return board.SubmitTestaments(sctx, action, []Testament{testament})
	}

	if scope != nil {
		if err := scope.Go("accumulator_flush", 5*time.Second, func(gctx context.Context) error {
			return submit(gctx)
		}); err != nil {
			slog.Error("accumulator_flush_dispatch_failed",
				"agent", a.agentID, "artifacts", len(artifacts), "error", err.Error())
			board.RecordNotificationError("accumulator flush dispatch: " + err.Error())
		}
		return
	}
	if err := submit(ctx); err != nil {
		slog.Error("accumulator_flush_failed",
			"agent", a.agentID, "artifacts", len(artifacts), "error", err.Error())
		board.RecordNotificationError("accumulator flush: " + err.Error())
	}
}

// ── Context key pattern ──

type testamentAccumulatorKey struct{}

// WithTestamentAccumulator stores an accumulator in the context.
func WithTestamentAccumulator(ctx context.Context, acc *TestamentAccumulator) context.Context {
	return context.WithValue(ctx, testamentAccumulatorKey{}, acc)
}

// AccumulatorFromContext retrieves the accumulator, or nil if none.
func AccumulatorFromContext(ctx context.Context) *TestamentAccumulator {
	acc, _ := ctx.Value(testamentAccumulatorKey{}).(*TestamentAccumulator)
	return acc
}
