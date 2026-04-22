package shared

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/versioning"
)

// AuditRejectionSLATracker polls the session's commit queue for
// rejection entries without a supersedor and emits escalation events
// when they exceed a configured SLA threshold. Per
// docs/PARALLEL_GLOBAL_VFS.md §8: "Architect stall: backpressure
// kicks in; user-escalation on SLA breach (wall-clock measured in
// session-open epochs; closed session does not advance the timer)."
//
// The timebase is SessionVFS.Clock() — a monotonic accumulator that
// only advances while the session is open. A rejection recorded at
// clock=T1 only breaches its SLA after the clock reaches T1+threshold
// in open-session time, regardless of how long the session was
// closed in between.
//
// Breach events fire exactly once per rejection. Supersession or
// explicit abandonment clears the tracked mark so subsequent cycles
// do not re-escalate.
type AuditRejectionSLATracker struct {
	session   *versioning.SessionVFS
	bus       guide.EventBus
	logger    *slog.Logger
	threshold time.Duration
	poll      time.Duration
	scope     *concurrency.GoroutineScope

	mu      sync.Mutex
	marks   map[versioning.SemanticVersion]rejectionMark
	running bool
	stopCh  chan struct{}
	done    chan struct{}
}

type rejectionMark struct {
	mark    versioning.Mark
	breached bool
}

// AuditRejectionSLATrackerConfig configures the tracker.
type AuditRejectionSLATrackerConfig struct {
	// Session is the SessionVFS whose commit queue the tracker
	// observes. Required.
	Session *versioning.SessionVFS
	// Bus is the observability bus escalation events publish to.
	// Nil is acceptable (tracker still logs).
	Bus guide.EventBus
	// Threshold is the SLA window. A rejection whose open-time age
	// exceeds this triggers an escalation. Zero disables tracking.
	Threshold time.Duration
	// Poll is the cadence at which the tracker re-evaluates marks.
	// Zero defaults to 5s.
	Poll time.Duration
	// Scope is the GoroutineScope the tracker's poll loop runs in.
	// When nil, Start reads the scope from the supplied ctx. Start
	// refuses to run without a scope when Threshold > 0, so the
	// tracker never spawns an untracked goroutine.
	Scope *concurrency.GoroutineScope
	// Logger is the structured logger. Optional.
	Logger *slog.Logger
}

// NewAuditRejectionSLATracker constructs an unstarted tracker. Call
// Start to begin the poll loop.
func NewAuditRejectionSLATracker(cfg AuditRejectionSLATrackerConfig) *AuditRejectionSLATracker {
	poll := cfg.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditRejectionSLATracker{
		session:   cfg.Session,
		bus:       cfg.Bus,
		logger:    logger,
		threshold: cfg.Threshold,
		poll:      poll,
		scope:     cfg.Scope,
		marks:     make(map[versioning.SemanticVersion]rejectionMark),
	}
}

// AuditRejectionSLABreachTopic is the observability topic the
// tracker publishes on when a rejection exceeds its SLA.
const AuditRejectionSLABreachTopic = "audit.rejection.sla_breach"

// AuditRejectionSLABreachNotification is the payload published on
// AuditRejectionSLABreachTopic.
type AuditRejectionSLABreachNotification struct {
	SessionID        string                     `json:"session_id"`
	RejectedVersion  versioning.SemanticVersion `json:"rejected_version"`
	ThresholdSeconds float64                    `json:"threshold_seconds"`
	ElapsedSeconds   float64                    `json:"elapsed_seconds"`
	ObservedAt       time.Time                  `json:"observed_at"`
}

// Start launches the poll loop. Idempotent. Requires a
// GoroutineScope — either supplied in the config or stamped on ctx.
// Returns an error when Threshold > 0 and no scope is available so
// the tracker never silently spawns an untracked goroutine.
func (t *AuditRejectionSLATracker) Start(ctx context.Context) error {
	if t == nil {
		return nil
	}
	if t.session == nil {
		return fmt.Errorf("audit rejection sla tracker: nil session")
	}
	if t.threshold <= 0 {
		return nil
	}
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return nil
	}
	scope := t.scope
	if scope == nil {
		scope = concurrency.ScopeFromContext(ctx)
	}
	if scope == nil {
		t.mu.Unlock()
		return fmt.Errorf("audit rejection sla tracker: Start requires a GoroutineScope (in config or on ctx) — untracked goroutines are forbidden")
	}
	t.running = true
	t.stopCh = make(chan struct{})
	t.done = make(chan struct{})
	t.mu.Unlock()

	return scope.Go("audit-sla-tracker:run", 0, func(ctx context.Context) error {
		t.run(ctx)
		return nil
	})
}

// Stop halts the poll loop. Idempotent.
func (t *AuditRejectionSLATracker) Stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	close(t.stopCh)
	done := t.done
	t.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (t *AuditRejectionSLATracker) run(ctx context.Context) {
	defer close(t.done)
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.scan()
		}
	}
}

// scan walks the commit queue, ingesting new rejections into marks,
// dropping marks for entries that have transitioned out of Rejected
// (superseded, abandoned), and emitting breach events for marks
// whose open-time age exceeds the threshold.
func (t *AuditRejectionSLATracker) scan() {
	queue := t.session.CommitQueue()
	if queue == nil {
		return
	}
	clock := t.session.Clock()
	if clock == nil {
		return
	}
	now := time.Now().UTC()
	currentlyRejected := make(map[versioning.SemanticVersion]bool)

	for _, entry := range queue.Snapshot() {
		if entry.State != versioning.CommitStateRejected {
			continue
		}
		currentlyRejected[entry.Descriptor.MergedVersion] = true
	}

	t.mu.Lock()
	// Ingest new rejections.
	for ver := range currentlyRejected {
		if _, ok := t.marks[ver]; !ok {
			t.marks[ver] = rejectionMark{mark: clock.MarkNow(now)}
		}
	}
	// Drop marks whose entries are no longer Rejected.
	for ver := range t.marks {
		if !currentlyRejected[ver] {
			delete(t.marks, ver)
		}
	}
	// Evaluate breaches.
	breaches := make([]breachDetail, 0)
	for ver, rm := range t.marks {
		if rm.breached {
			continue
		}
		elapsed := rm.mark.Since(clock, now)
		if elapsed < t.threshold {
			continue
		}
		rm.breached = true
		t.marks[ver] = rm
		breaches = append(breaches, breachDetail{
			ver:     ver,
			elapsed: elapsed,
		})
	}
	t.mu.Unlock()

	for _, b := range breaches {
		t.publishBreach(b.ver, b.elapsed, now)
	}
}

type breachDetail struct {
	ver     versioning.SemanticVersion
	elapsed time.Duration
}

func (t *AuditRejectionSLATracker) publishBreach(ver versioning.SemanticVersion, elapsed time.Duration, now time.Time) {
	sessionID := string(t.session.SessionID())
	notif := &AuditRejectionSLABreachNotification{
		SessionID:        sessionID,
		RejectedVersion:  ver,
		ThresholdSeconds: t.threshold.Seconds(),
		ElapsedSeconds:   elapsed.Seconds(),
		ObservedAt:       now,
	}
	t.logger.Warn("audit rejection SLA breached",
		"session_id", sessionID,
		"rejected_version", ver.String(),
		"threshold_seconds", notif.ThresholdSeconds,
		"elapsed_seconds", notif.ElapsedSeconds,
	)
	if t.bus == nil {
		return
	}
	body, err := jsonMarshalSLABreach(notif)
	if err != nil {
		t.logger.Warn("sla breach marshal failed", "error", err.Error())
		return
	}
	msg := guide.NewBridgeMessage(sessionID, "observers", body)
	if err := t.bus.Publish(AuditRejectionSLABreachTopic, msg); err != nil {
		t.logger.Warn("sla breach publish failed", "error", err.Error())
	}
}
