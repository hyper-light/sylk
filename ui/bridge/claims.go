package bridge

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	claimsBridgeName    = "bridge.claims"
	claimsBridgeBuffer  = 64
	claimsBridgeTimeout = 0 // Zero uses scope's max lifetime.
)

// ClaimsBridge subscribes to the session's ClaimsBoard projection and
// converts board mutations into Bubble Tea messages:
//   - ClaimsProjectionMsg for pipeline counter updates (accepted/total)
//   - ActivityEventMsg for claims lifecycle events in the agent detail feed
//
// Follows the standard bridge pattern with a bounded buffer and drain
// goroutine, matching ActivityBridge and PipelineBridge.
type ClaimsBridge struct {
	id       string
	scope    *concurrency.GoroutineScope
	registry *claims.SessionBoardRegistry

	mu      sync.Mutex
	program TeaProgram
	unsub   func()

	// Diff state for projection comparison. Protected by mu.
	lastClaims       map[string]claims.ClaimStatus       // claimID → last known status
	lastValidations  map[string]claims.ValidationStatus  // validationID → last known status
	lastTestIDs      map[string]struct{}                 // seen testament IDs
	lastAccepted     int
	lastTotal        int

	outbox  chan any // bounded buffer; drained by scope goroutine
	dropped atomic.Int64
	done    chan struct{}
	stopOnce sync.Once
}

// NewClaimsBridge creates a bridge that converts claims board projections
// into Bubble Tea messages.
func NewClaimsBridge(
	id string,
	registry *claims.SessionBoardRegistry,
	scope *concurrency.GoroutineScope,
) *ClaimsBridge {
	return &ClaimsBridge{
		id:              id,
		scope:           scope,
		registry:        registry,
		lastClaims:      make(map[string]claims.ClaimStatus),
		lastValidations: make(map[string]claims.ValidationStatus),
		lastTestIDs:     make(map[string]struct{}),
		outbox:          make(chan any, claimsBridgeBuffer),
		done:            make(chan struct{}),
	}
}

// Start launches the drain goroutine via scope.
func (b *ClaimsBridge) Start(program TeaProgram) error {
	b.mu.Lock()
	b.program = program
	b.mu.Unlock()

	if b.scope == nil {
		return nil
	}
	return b.scope.Go(claimsBridgeName, claimsBridgeTimeout, b.drainFunc(program))
}

// drainFunc returns the drain goroutine that reads from the outbox and
// sends to the tea program. Matches PipelineBridge pattern.
func (b *ClaimsBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case m := <-b.outbox:
				program.Send(m)
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Stop unsubscribes and releases resources. Idempotent.
func (b *ClaimsBridge) Stop() {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		if b.unsub != nil {
			b.unsub()
			b.unsub = nil
		}
		b.mu.Unlock()
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *ClaimsBridge) Name() string { return claimsBridgeName }

// DroppedCount returns events dropped due to backpressure.
func (b *ClaimsBridge) DroppedCount() int64 { return b.dropped.Load() }

// SwitchSession unsubscribes from the old board and subscribes to the
// new session's board. Called by AppModel on SessionEventMsg.
func (b *ClaimsBridge) SwitchSession(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.unsub != nil {
		b.unsub()
		b.unsub = nil
	}

	b.resetDiffState()

	if b.registry == nil || strings.TrimSpace(sessionID) == "" {
		return
	}

	board := b.registry.Lookup(sessionID)
	if board == nil {
		return
	}

	b.unsub = board.SubscribeProjection(func(proj *claims.ClaimsBoardProjection) error {
		b.onProjection(proj)
		return nil
	})
}

// resetDiffState clears all diff tracking maps. Caller holds mu.
func (b *ClaimsBridge) resetDiffState() {
	b.lastClaims = make(map[string]claims.ClaimStatus)
	b.lastValidations = make(map[string]claims.ValidationStatus)
	b.lastTestIDs = make(map[string]struct{})
	b.lastAccepted = 0
	b.lastTotal = 0
}

// onProjection diffs the new projection against the last-seen state,
// collects messages, and enqueues them to the outbox. Never blocks —
// drops on backpressure. Runs under the board's scope dispatch.
func (b *ClaimsBridge) onProjection(proj *claims.ClaimsBoardProjection) {
	if proj == nil {
		return
	}

	var pending []any

	b.mu.Lock()
	pending = b.diffProjection(proj)
	b.evictTerminalClaims(proj)
	b.mu.Unlock()

	for _, m := range pending {
		b.enqueue(m)
	}
}

// diffProjection computes the set of messages to emit from the
// projection diff. Caller holds mu.
func (b *ClaimsBridge) diffProjection(proj *claims.ClaimsBoardProjection) []any {
	var out []any

	if counterMsg, changed := b.diffCounter(proj); changed {
		out = append(out, counterMsg)
	}
	out = append(out, b.diffClaims(proj)...)
	out = append(out, b.diffTestaments(proj)...)
	return out
}

// diffCounter checks if accepted/total changed. Caller holds mu.
func (b *ClaimsBridge) diffCounter(proj *claims.ClaimsBoardProjection) (msg.ClaimsProjectionMsg, bool) {
	changed := proj.AcceptedCount != b.lastAccepted || proj.TotalClaims != b.lastTotal
	b.lastAccepted = proj.AcceptedCount
	b.lastTotal = proj.TotalClaims
	if !changed {
		return msg.ClaimsProjectionMsg{}, false
	}
	return msg.ClaimsProjectionMsg{
		SessionID:     proj.BoardID,
		BoardID:       proj.BoardID,
		TaskID:        proj.TaskID,
		AcceptedCount: proj.AcceptedCount,
		TotalClaims:   proj.TotalClaims,
	}, true
}

// diffClaims emits events for new/changed claims and their validations.
// Caller holds mu.
func (b *ClaimsBridge) diffClaims(proj *claims.ClaimsBoardProjection) []any {
	var out []any
	for i := range proj.Claims {
		c := &proj.Claims[i]
		out = append(out, b.diffSingleClaim(c)...)
		out = append(out, b.diffClaimValidations(c)...)
	}
	return out
}

// diffSingleClaim checks if a claim is new or changed status. Caller holds mu.
func (b *ClaimsBridge) diffSingleClaim(c *claims.Claim) []any {
	prev, seen := b.lastClaims[c.ID]
	b.lastClaims[c.ID] = c.Status

	if !seen {
		return []any{claimsActivityMsg(events.EventTypeClaimReceived, events.OutcomePending,
			claims.SubjectAgentID(c.Relations), c.Title)}
	}
	if prev == c.Status {
		return nil
	}

	switch c.Status {
	case claims.ClaimStatusAccepted:
		return []any{claimsActivityMsg(events.EventTypeClaimAccepted, events.OutcomeSuccess,
			claims.SubjectAgentID(c.Relations), c.Title)}
	case claims.ClaimStatusRejected:
		return []any{claimsActivityMsg(events.EventTypeValidationFailed, events.OutcomeFailure,
			claims.SubjectAgentID(c.Relations), c.Title)}
	default:
		return nil
	}
}

// diffClaimValidations tracks per-validation state independently of claim
// status, so validation verdicts are emitted even when the parent claim
// hasn't transitioned. Caller holds mu.
func (b *ClaimsBridge) diffClaimValidations(c *claims.Claim) []any {
	var out []any
	for _, v := range c.Validations {
		if v == nil {
			continue
		}
		prev, seen := b.lastValidations[v.ID]
		b.lastValidations[v.ID] = v.Status
		if seen && prev == v.Status {
			continue
		}
		switch v.Status {
		case claims.ValidationStatusPassed:
			out = append(out, claimsActivityMsg(events.EventTypeValidationPassed, events.OutcomeSuccess,
				claims.SubjectAgentID(c.Relations), v.Description))
		case claims.ValidationStatusFailed:
			out = append(out, claimsActivityMsg(events.EventTypeValidationFailed, events.OutcomeFailure,
				claims.SubjectAgentID(c.Relations), v.Description))
		}
	}
	return out
}

// diffTestaments emits events for newly seen testaments. Caller holds mu.
func (b *ClaimsBridge) diffTestaments(proj *claims.ClaimsBoardProjection) []any {
	var out []any
	for i := range proj.Testaments {
		t := &proj.Testaments[i]
		if _, seen := b.lastTestIDs[t.ID]; seen {
			continue
		}
		b.lastTestIDs[t.ID] = struct{}{}
		out = append(out, claimsActivityMsg(events.EventTypeTestamentSubmitted, events.OutcomeSuccess,
			t.AgentID, t.Summary))
	}
	return out
}

// evictTerminalClaims removes tracking entries for claims that have
// reached a terminal status (accepted, rejected, superseded), preventing
// unbounded map growth. Testaments are evicted alongside their claim.
// Caller holds mu.
func (b *ClaimsBridge) evictTerminalClaims(proj *claims.ClaimsBoardProjection) {
	activeClaims := make(map[string]struct{}, len(proj.Claims))
	for i := range proj.Claims {
		activeClaims[proj.Claims[i].ID] = struct{}{}
	}
	for id := range b.lastClaims {
		if _, active := activeClaims[id]; !active {
			delete(b.lastClaims, id)
		}
	}

	activeTestaments := make(map[string]struct{}, len(proj.Testaments))
	for i := range proj.Testaments {
		activeTestaments[proj.Testaments[i].ID] = struct{}{}
	}
	for id := range b.lastTestIDs {
		if _, active := activeTestaments[id]; !active {
			delete(b.lastTestIDs, id)
		}
	}

	// Evict validation entries for claims no longer present.
	activeValidations := make(map[string]struct{})
	for i := range proj.Claims {
		for _, v := range proj.Claims[i].Validations {
			if v != nil {
				activeValidations[v.ID] = struct{}{}
			}
		}
	}
	for id := range b.lastValidations {
		if _, active := activeValidations[id]; !active {
			delete(b.lastValidations, id)
		}
	}
}

// enqueue sends a message to the outbox. Non-blocking; drops on overflow.
func (b *ClaimsBridge) enqueue(m any) {
	select {
	case b.outbox <- m:
	default:
		total := b.dropped.Add(1)
		slog.Warn("claims bridge drop: outbox full",
			"bridge_id", b.id,
			"total_dropped", total)
	}
}

// claimsActivityMsg builds an ActivityEventMsg for claims lifecycle events.
func claimsActivityMsg(eventType events.EventType, outcome events.EventOutcome, agentID, content string) msg.ActivityEventMsg {
	ev := events.NewActivityEvent(eventType, "", content)
	ev.AgentID = agentID
	ev.Outcome = outcome
	ev.Visibility = events.VisibilityUser
	ev.Timestamp = time.Now()
	ev.Category = "claims"
	return msg.ActivityEventMsg{Event: ev}
}
