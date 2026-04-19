package shared

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ActivitySession is a first-class unit of work tied to a user request.
// Sessions decouple activity-state publishing from the text-stream
// lifecycle, so bus handlers (pipeline update processors, async dispatch
// paths, lifecycle reapers) can narrate what they are doing even when no
// stream is open.
//
// Why this exists: `PublishAgentState` originally required stream metadata
// on ctx to derive a correlation. That coupling left whole classes of code
// (every `bus.Subscribe` handler, every async dispatcher) unable to emit
// activity transitions — exactly the sites where inter-agent routing gaps
// produce UI silence. Sessions give those sites a first-class identity
// the activity channel can route on.
//
// Sessions form a tree via ParentSessionID. The orchestrator's pipeline
// supervisor opens a session per pipeline update it processes; when it
// dispatches a child request, the child agent's session lists the
// supervisor's as parent. The UI doesn't rely on the tree today (it
// routes by correlation), but the linkage is preserved so future tree-
// rendering work can consume it without schema churn.
type ActivitySession struct {
	// ID uniquely identifies this session within the process. Auto-
	// generated when omitted via NewActivitySession.
	ID string
	// OwnerAgentType / OwnerAgentID name the agent or subsystem that opened
	// the session. Used for the activity-indicator label.
	OwnerAgentType string
	OwnerAgentID   string
	// SessionID is the user session this session belongs to. Mirrors the
	// same field on StreamContext so downstream routing is consistent.
	SessionID string
	// ParentSessionID links to the parent session when this session was
	// spawned by another session. Empty for top-level sessions.
	ParentSessionID string
	// PipelineID associates the session with a concrete pipeline when the
	// work is part of pipeline processing. Empty for non-pipeline
	// sessions (ordinary chat flows).
	PipelineID string
	// CorrelationID is the identifier the UI activity channel routes on.
	// When the session corresponds to an in-flight user-facing request
	// this will be the request's correlation id; when the session is a
	// bus-handler-owned activity (e.g., pipeline supervisor reacting to a
	// pipeline update) this is a synthetic id derived from the session id
	// so the UI can still render bridge rows for it.
	CorrelationID string
	// StartedAt is the monotonic timestamp the session was opened.
	// Thinking-spinner elapsed counters are measured relative to this.
	StartedAt time.Time
}

// NewActivitySession constructs a session with the provided identity and
// fills in ID / StartedAt / CorrelationID defaults. A blank CorrelationID
// is replaced with a synthetic `act_<uuid>` id so every session has a
// stable routing key for the UI activity channel. Passing a non-empty
// CorrelationID (typically the request's correlation) is the normal case
// when the session mirrors an in-flight user request.
func NewActivitySession(session ActivitySession) *ActivitySession {
	if strings.TrimSpace(session.ID) == "" {
		session.ID = "asess_" + uuid.NewString()[:12]
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now()
	}
	if strings.TrimSpace(session.CorrelationID) == "" {
		session.CorrelationID = "act_" + uuid.NewString()[:12]
	}
	return &session
}

type activitySessionKey struct{}

// WithActivitySession attaches an ActivitySession to ctx. Returns ctx
// unchanged when session is nil (a legitimate no-op for call sites that
// conditionally construct a session). A session already on ctx is
// preserved as ParentSessionID on the new session so nested bus-handler
// dispatches form a tree rather than overwriting each other.
func WithActivitySession(ctx context.Context, session *ActivitySession) context.Context {
	if session == nil {
		return ctx
	}
	if existing := ActivitySessionFromContext(ctx); existing != nil {
		if strings.TrimSpace(session.ParentSessionID) == "" {
			session.ParentSessionID = existing.ID
		}
	}
	return context.WithValue(ctx, activitySessionKey{}, session)
}

// ActivitySessionFromContext returns the current session on ctx, or nil
// when none is attached. Callers that need to emit state events should
// check this first and fall back to stream metadata only when no session
// is present; the session is the canonical identity for activity events.
func ActivitySessionFromContext(ctx context.Context) *ActivitySession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(activitySessionKey{}).(*ActivitySession)
	return session
}
