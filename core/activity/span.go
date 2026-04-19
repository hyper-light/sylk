package activity

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
)

// Span is the chokepoint-instrumentation primitive. Every funnel point
// in sylk wraps its primary work in:
//
//	span := activity.StartSpan(ctx, kind, payload)
//	defer span.End()
//
// or with explicit error tagging:
//
//	span := activity.StartSpan(ctx, kind, payload)
//	defer func() { span.EndWithError(err) }()
//
// StartSpan attaches a child FabricContext to the returned context,
// extends the causal chain, and emits an in-flight activity if the
// kind has terminal/in-flight semantics. End emits the terminal
// activity (or finalizes a point activity).
//
// Span methods are safe to call multiple times; only the first End or
// EndWithError emits a terminal activity, subsequent calls are no-ops.
type Span struct {
	// ctx is the child context attached during StartSpan. Callers use
	// Context() to thread it into the wrapped operation.
	ctx context.Context

	// fc is the FabricContext for this span (the child of the parent).
	fc FabricContext

	// kind is the ActionKind that was started.
	kind ActionKind

	// startedAt records when StartSpan was invoked.
	startedAt time.Time

	// actor records who invoked the span. Captured from baggage at
	// StartSpan time so subsequent Baggage mutations don't change it.
	actor Actor

	// sessionID is the session this span belongs to, captured from
	// baggage at StartSpan time.
	sessionID SessionID

	// subject is the typed coordinate bag identifying what the span
	// touches. Set at StartSpan time by the caller.
	subject Subject

	// payload carries the kind-specific typed data captured at start.
	// Augmented by SetAttribute and merged into the terminal activity.
	payload map[string]any

	// inFlightID is the ID of the in_flight activity emitted at start
	// for kinds with terminal semantics. Used as Resolves on the
	// terminal activity.
	inFlightID ActivityID

	// ended atomically guards single-shot termination.
	ended atomic.Bool
}

// StartSpan begins a new span instrumenting a chokepoint invocation.
// Returns a Span whose context (Span.Context()) carries the child
// FabricContext; pass that context to the wrapped operation so nested
// chokepoints see this span as their parent.
//
// If the ActionKind has terminal semantics (i.e., its complement
// kind exists, e.g., LLMRequestEmitted/LLMResponseCompleted), this
// emits an in_flight activity at start and a terminal activity at End.
// For point-shaped kinds it emits a single point activity at End.
func StartSpan(ctx context.Context, kind ActionKind, subject Subject) *Span {
	if ctx == nil {
		ctx = context.Background()
	}
	childCtx, fc := WithChildSpan(ctx)

	bag := fc.Baggage
	actor := Actor{
		AgentID:    bag["agent_id"],
		AgentType:  bag["agent_type"],
		PipelineID: bag["pipeline_id"],
	}
	sessionID := SessionID(bag["session_id"])

	span := &Span{
		ctx:       childCtx,
		fc:        fc,
		kind:      kind,
		startedAt: time.Now(),
		actor:     actor,
		sessionID: sessionID,
		subject:   subject,
		payload:   map[string]any{},
	}

	// Emit the in-flight start activity for kinds with terminal pairs.
	if startKind, hasPair := pairedStartKind(kind); hasPair {
		startID := ActivityID(fc.SpanID)
		span.inFlightID = startID
		Append(childCtx, span.buildActivity(startKind, StateInFlight, startID, nil))
	}

	return span
}

// Context returns the child context attached during StartSpan. Pass
// this into the wrapped operation so nested chokepoints see this span
// as their parent.
func (s *Span) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.ctx
}

// FabricContext returns the child FabricContext for this span. Useful
// when callers need to propagate the trace to a system that doesn't
// flow through context.Context (e.g., a bus message header).
func (s *Span) FabricContext() FabricContext {
	if s == nil {
		return FabricContext{}
	}
	return s.fc
}

// SetAttribute adds a typed payload field to the span. Merged into the
// terminal activity's payload at End. Repeated calls with the same key
// overwrite.
func (s *Span) SetAttribute(key string, value any) {
	if s == nil || s.payload == nil {
		return
	}
	s.payload[key] = value
}

// SetSubject overrides the span's subject after StartSpan. Useful when
// the subject's typed coordinates aren't known until partway through
// the wrapped operation (e.g., a bus message's destination resolved
// only after routing).
func (s *Span) SetSubject(subject Subject) {
	if s == nil {
		return
	}
	s.subject = subject
}

// End emits the terminal activity for this span, recording success and
// elapsed time. Safe to call multiple times; only the first call emits.
func (s *Span) End() {
	s.endWith(nil)
}

// EndWithError emits the terminal activity for this span, recording
// the error. Safe to call multiple times.
func (s *Span) EndWithError(err error) {
	s.endWith(err)
}

func (s *Span) endWith(err error) {
	if s == nil || !s.ended.CompareAndSwap(false, true) {
		return
	}
	elapsed := time.Since(s.startedAt)
	s.payload["elapsed_ns"] = elapsed.Nanoseconds()
	if err != nil {
		s.payload["error"] = err.Error()
	}

	terminalKind := s.kind
	state := StatePoint
	var resolves *ActivityID
	if endKind, hasPair := pairedEndKind(s.kind); hasPair {
		terminalKind = endKind
		state = StateResolved
		id := s.inFlightID
		resolves = &id
	}

	// Errors override the kind to ActionStoreWriteFailed for store
	// writes (and similar) so error-rate queries are simple.
	if err != nil {
		if errKind, hasErrPair := errorKindFor(s.kind); hasErrPair {
			terminalKind = errKind
		}
	}

	Append(s.ctx, s.buildActivity(terminalKind, state, NewActivityID(), resolves))
}

// NewActivityID returns a fresh activity ID. Exposed so per-system
// amplifiers (which emit activities directly without going through a
// span) can mint IDs.
func NewActivityID() ActivityID {
	return ActivityID(NewID())
}

func (s *Span) buildActivity(kind ActionKind, state ActivityState, id ActivityID, resolves *ActivityID) AgentActivity {
	payloadJSON, _ := json.Marshal(s.payload)
	parent := s.fc.ParentSpanID
	var caused *ActivityID
	if parent != "" {
		c := ActivityID(parent)
		caused = &c
	}
	chain := make([]ActivityID, 0, len(s.fc.CausalChain))
	for _, anc := range s.fc.CausalChain {
		chain = append(chain, ActivityID(anc))
	}
	return AgentActivity{
		ID:          id,
		SessionID:   s.sessionID,
		Timestamp:   time.Now(),
		Resolution:  ResolutionFor(kind),
		Actor:       s.actor,
		Action:      kind,
		Subject:     s.subject,
		Payload:     payloadJSON,
		Caused:      caused,
		Resolves:    resolves,
		CausalChain: chain,
		State:       state,
	}
}

// pairedStartKind returns the in-flight start kind for the given kind
// when the kind is a paired-emit kind (e.g., StoreWriteCommitted is
// the success terminal of a store write; the start kind for the in-
// flight phase is the same kind itself). The current model uses the
// "start" kind as both the in_flight emission and the success terminal
// because they semantically describe the same operation; consumers
// distinguish via the State field.
//
// A handful of kinds have distinct start/end pairs (LLMRequest →
// LLMResponse, ToolCallStarted → ToolCallCompleted); for those,
// pairedEndKind returns the end kind so the terminal emission uses it.
//
// Returns (kind, true) when an in-flight start activity should be
// emitted at StartSpan time.
func pairedStartKind(kind ActionKind) (ActionKind, bool) {
	if _, ok := pairedEndKind(kind); ok {
		// Emit the start kind as in_flight; End re-emits the terminal
		// kind as resolved.
		return kind, true
	}
	return "", false
}

// pairedEndKind returns the terminal kind that should be emitted at
// End for kinds that have distinct start/end ActionKinds. Kinds whose
// emission is point-shaped (a single activity at completion) return
// false here.
func pairedEndKind(kind ActionKind) (ActionKind, bool) {
	switch kind {
	case ActionLLMRequestEmitted:
		return ActionLLMResponseCompleted, true
	case ActionToolCallStarted:
		return ActionToolCallCompleted, true
	case ActionBusMessageEmitted:
		return ActionBusMessageDelivered, true
	case ActionGoroutineStarted:
		return ActionGoroutineStopped, true
	case ActionClaimAcquired:
		return ActionClaimReleased, true
	case ActionReviewRequested:
		return ActionReviewCompleted, true
	case ActionValidationStarted:
		return ActionValidationAccepted, true
	case ActionHoldAcquired:
		return ActionHoldReleased, true
	case ActionRemediationOpened:
		return ActionRemediationResolved, true
	case ActionChallengeEmitted:
		return ActionChallengeResponse, true
	case ActionConsultEmitted:
		return ActionConsultResponse, true
	}
	return "", false
}

// errorKindFor returns a kind to use when a span ends with error,
// when an error-specific kind exists. Most kinds use the same terminal
// kind regardless of error (the error is captured in payload); only
// store writes have a distinct ActionStoreWriteFailed kind because
// that error path drives separate retry telemetry.
func errorKindFor(kind ActionKind) (ActionKind, bool) {
	switch kind {
	case ActionStoreWriteCommitted:
		return ActionStoreWriteFailed, true
	}
	return "", false
}
