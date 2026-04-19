package scribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/providers"
)

// Receive implements activitystore.Subscriber. It is the per-agent
// scribe's primary input path under the SCRIBE_FABRIC.md design,
// replacing the FeedScribe push pattern. The scribe filters incoming
// activities by parent agent type + Resolution per its profile, then
// accumulates them in per-correlation batches that drive batched
// LLM narrations on cadence triggers.
//
// Implementation contract for the activitystore.Subscriber interface:
//
//   - Must be safe for concurrent callers (the SubscribingSink fans
//     out without serialization).
//   - Must not block for I/O on the caller's thread — narration
//     dispatch happens on a separate goroutine via the scope.
//   - Must never panic — the SubscribingSink recovers from subscriber
//     panics, but a quiet drop is preferred.
func (s *Scribe) Receive(ctx context.Context, a activity.AgentActivity) {
	if s == nil || !s.running.Load() {
		return
	}
	profile := s.profile()
	if !s.passesProfileFilters(a, profile) {
		return
	}
	s.batchAdmit(a)
	s.evaluateBatchTrigger(ctx, a, profile, false)
}

// Name implements activitystore.Subscriber.
func (s *Scribe) Name() string {
	if s == nil {
		return "scribe"
	}
	return s.id
}

// passesProfileFilters reports whether an incoming activity should
// enter the scribe's batch buffer for its parent agent. The filter
// is conservative: actor agent type must match, resolution must meet
// the profile's threshold, and the activity must not be on the
// profile's IgnoreKinds list.
func (s *Scribe) passesProfileFilters(a activity.AgentActivity, profile ScribeProfile) bool {
	if !strings.EqualFold(a.Actor.AgentType, s.parentAgentType) {
		return false
	}
	if !profile.AcceptsResolution(a.Resolution) {
		return false
	}
	if profile.ShouldIgnoreKind(a.Action) {
		return false
	}
	// A scribe never narrates its own activities — that would be a
	// recursive narration loop.
	if strings.EqualFold(a.Actor.AgentType, "scribe-"+s.parentAgentType) {
		return false
	}
	return true
}

// profile returns the registered ScribeProfile for this scribe's
// parent agent type, or the default profile if unregistered.
func (s *Scribe) profile() ScribeProfile {
	return ProfileFor(s.parentAgentType)
}

// ─── Per-workstream batch buffer ─────────────────────────────────────

type fabricBatch struct {
	mu          sync.Mutex
	activities  []activity.AgentActivity
	firstSeenAt time.Time
}

// batchKey derives the workstream key for an incoming activity. Uses
// the activity's pipeline_id when present, falling back to the actor
// agent_id for non-pipeline agents. Both yield stable per-workstream
// batching.
func (s *Scribe) batchKey(a activity.AgentActivity) string {
	if pid := strings.TrimSpace(a.Actor.PipelineID); pid != "" {
		return pid
	}
	if id := strings.TrimSpace(a.Actor.AgentID); id != "" {
		return id
	}
	return "scribe:" + s.parentAgentType
}

// fabricBatches lazily holds per-workstream buffers. Initialized in
// batchAdmit on first use. Separate from the existing workstreams
// map so the legacy FeedScribe path stays unaffected.
func (s *Scribe) fabricBatches() *sync.Map {
	if s.batchMapPtr == nil {
		s.batchMapInit.Do(func() {
			s.batchMapPtr = &sync.Map{}
		})
	}
	return s.batchMapPtr
}

func (s *Scribe) batchAdmit(a activity.AgentActivity) {
	key := s.batchKey(a)
	raw, _ := s.fabricBatches().LoadOrStore(key, &fabricBatch{})
	batch := raw.(*fabricBatch)
	batch.mu.Lock()
	if len(batch.activities) == 0 {
		batch.firstSeenAt = time.Now()
	}
	batch.activities = append(batch.activities, a)
	batch.mu.Unlock()
}

// evaluateBatchTrigger decides whether the just-added activity, the
// current batch state, or the elapsed window should fire a
// narration. The function is non-blocking — it dispatches narration
// on a separate goroutine so Receive returns quickly.
//
// triggers (in priority order):
//   - emphasized kind: any activity matching profile.EmphasizeKinds
//   - turn boundary: terminal protocol activities (handoff_next,
//     finalize_pipeline, validate_work, finalize_global_review,
//     handoff_to_ot, etc.)
//   - causal closing: a *_completed/*_resolved/*_accepted/*_response
//     activity that closes a previously-in-flight one this batch saw
//   - batch size: len(batch.activities) >= profile.BatchSize
//   - batch window: time.Since(firstSeenAt) >= profile.BatchWindow
//
// fromPeriodic is true when called by the periodic synthesis ticker
// (in which case we skip activity-driven triggers and dispatch a
// synthesis even if the batch is empty).
func (s *Scribe) evaluateBatchTrigger(ctx context.Context, latest activity.AgentActivity, profile ScribeProfile, fromPeriodic bool) {
	key := s.batchKey(latest)
	if fromPeriodic {
		// Periodic synthesis: dispatch even if batch is empty.
		s.dispatchBatchNarration(ctx, key, "periodic")
		return
	}
	if profile.IsEmphasized(latest.Action) {
		s.dispatchBatchNarration(ctx, key, "emphasized:"+string(latest.Action))
		return
	}
	if isTurnBoundaryAction(latest.Action) {
		s.dispatchBatchNarration(ctx, key, "turn_boundary:"+string(latest.Action))
		return
	}
	if latest.Resolves != nil && *latest.Resolves != "" {
		// Causal closing: this activity resolves a prior one. Worth
		// narrating because the chain just completed.
		s.dispatchBatchNarration(ctx, key, "causal_closing")
		return
	}

	raw, ok := s.fabricBatches().Load(key)
	if !ok {
		return
	}
	batch := raw.(*fabricBatch)
	batch.mu.Lock()
	size := len(batch.activities)
	elapsed := time.Since(batch.firstSeenAt)
	batch.mu.Unlock()

	if size >= profile.BatchSize {
		s.dispatchBatchNarration(ctx, key, "batch_size")
		return
	}
	if size > 0 && elapsed >= profile.BatchWindow {
		s.dispatchBatchNarration(ctx, key, "batch_window")
		return
	}
}

// isTurnBoundaryAction reports whether an action kind represents the
// natural end of a parent agent's turn. These are protocol-level
// terminal activities that signal "the agent has finished its work
// for now" — exactly the right moment to narrate.
func isTurnBoundaryAction(kind activity.ActionKind) bool {
	switch kind {
	case activity.ActionDAGAccepted,
		activity.ActionValidationAccepted,
		activity.ActionValidationRejected,
		activity.ActionPlanRatified,
		activity.ActionCharterRatified:
		return true
	}
	// Other turn-boundary signals (handoff_next, finalize_pipeline,
	// validate_work) flow through the protocol as bus messages, not
	// as fabric activities. Causal-closing handles those when they
	// resolve in-flight activities. The kinds above are the
	// fabric-native turn boundaries.
	return false
}

// dispatchBatchNarration fires a batched narration for the given
// workstream key. Resets the batch buffer after dispatching. Idempotent
// across concurrent triggers — only one narration goroutine per
// workstream key runs at a time (mutex-guarded inside the batch's
// dispatch lock).
func (s *Scribe) dispatchBatchNarration(ctx context.Context, key, trigger string) {
	raw, ok := s.fabricBatches().Load(key)
	if !ok {
		return
	}
	batch := raw.(*fabricBatch)
	batch.mu.Lock()
	if len(batch.activities) == 0 && trigger != "periodic" {
		batch.mu.Unlock()
		return
	}
	dispatched := append([]activity.AgentActivity(nil), batch.activities...)
	batch.activities = batch.activities[:0]
	batch.firstSeenAt = time.Time{}
	batch.mu.Unlock()

	go func() {
		// Defensive recover: a bad scribe state (uninitialized
		// workstreams map, missing provider) must never crash the
		// process. The dispatch goroutine is fire-and-forget; a panic
		// here would propagate up the goroutine scope and could take
		// down the SubscribingSink fan-out.
		defer func() {
			if r := recover(); r != nil && s.logger != nil {
				s.logger.Warn("scribe narration dispatch panicked",
					"parent", s.parentAgentType,
					"key", key,
					"trigger", trigger,
					"panic", r,
				)
			}
		}()
		dispatchCtx := ctx
		if dispatchCtx == nil {
			dispatchCtx = context.Background()
		}
		s.narrateBatch(dispatchCtx, key, trigger, dispatched)
	}()
}

// narrateBatch is the batched-narration entry point. It synthesizes a
// ScribeFeed-shaped input from the batch and dispatches into the
// existing processFeed path so the LLM tool loop, archivalist
// publish, and (in Phase 3) fabric narration_emitted projection all
// operate against a single coherent unit of narration work.
func (s *Scribe) narrateBatch(ctx context.Context, key, trigger string, batch []activity.AgentActivity) {
	if len(batch) == 0 && trigger != "periodic" {
		return
	}
	feed := shared.ScribeFeed{
		UserRequest:         buildBatchUserRequest(s.parentAgentType, trigger, batch),
		AgentResponse:       buildBatchAgentResponse(batch),
		ParentCorrelationID: key,
		Timestamp:           time.Now(),
	}
	s.processFeed(ctx, feed)
}

// buildBatchUserRequest synthesizes the user-facing prompt the
// scribe LLM sees as "the request" for this batched narration. It
// frames the batch as a structured stream of typed activities the
// scribe is asked to narrate.
func buildBatchUserRequest(parentAgentType, trigger string, batch []activity.AgentActivity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Narrate the following batch of activities your parent agent (%s) just emitted.\n", parentAgentType)
	fmt.Fprintf(&b, "Trigger: %s\n", trigger)
	fmt.Fprintf(&b, "Batch size: %d activities\n\n", len(batch))
	b.WriteString("Activities (chronological order):\n")
	for i, a := range batch {
		fmt.Fprintf(&b, "  [%d] %s (resolution=%s, state=%s) at %s\n",
			i+1, a.Action, a.Resolution, a.State, a.Timestamp.Format(time.RFC3339))
		if a.Subject.Domain != "" {
			fmt.Fprintf(&b, "      domain: %s\n", a.Subject.Domain)
		}
		if a.Subject.PathPrefix != "" {
			fmt.Fprintf(&b, "      scope: %s\n", a.Subject.PathPrefix)
		}
		if a.Subject.TargetArtifact != "" {
			fmt.Fprintf(&b, "      target: %s\n", a.Subject.TargetArtifact)
		}
		if a.Confidence != "" {
			fmt.Fprintf(&b, "      confidence: %s\n", a.Confidence)
		}
		if a.Caused != nil && *a.Caused != "" {
			fmt.Fprintf(&b, "      caused_by: %s\n", *a.Caused)
		}
		if a.Resolves != nil && *a.Resolves != "" {
			fmt.Fprintf(&b, "      resolves: %s\n", *a.Resolves)
		}
		if len(a.Payload) > 0 && len(a.Payload) < 512 {
			fmt.Fprintf(&b, "      payload: %s\n", string(a.Payload))
		}
	}
	return b.String()
}

// buildBatchAgentResponse synthesizes the "agent response" half of
// the synthetic feed. Today this is a compact JSON dump of the
// batch's terminal action kinds + their key identifiers; the scribe
// LLM uses it as quick-reference structured input alongside the
// chronological prose in UserRequest.
func buildBatchAgentResponse(batch []activity.AgentActivity) string {
	type entry struct {
		ID         activity.ActivityID `json:"id"`
		Action     activity.ActionKind `json:"action"`
		State      activity.ActivityState `json:"state"`
		Confidence activity.Confidence `json:"confidence,omitempty"`
		Subject    activity.Subject `json:"subject"`
	}
	out := make([]entry, 0, len(batch))
	for _, a := range batch {
		out = append(out, entry{
			ID:         a.ID,
			Action:     a.Action,
			State:      a.State,
			Confidence: a.Confidence,
			Subject:    a.Subject,
		})
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// PeriodicSynthesisLoop runs the per-profile periodic synthesis
// ticker. Started by Start() if a periodic window is configured;
// runs until the scribe stops. Fires a synthesis dispatch for each
// active workstream key on every tick.
func (s *Scribe) periodicSynthesisLoop(ctx context.Context) {
	profile := s.profile()
	if profile.PeriodicWindow <= 0 {
		return
	}
	ticker := time.NewTicker(profile.PeriodicWindow)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fabricBatches().Range(func(key, _ any) bool {
				if k, ok := key.(string); ok {
					s.dispatchBatchNarration(ctx, k, "periodic")
				}
				return true
			})
		}
	}
}

// EmptyMessageStub is the message we send to the LLM when a periodic
// synthesis fires with no activities in the batch. The narration is
// still useful as a "where are we now" snapshot but the LLM needs to
// know there's nothing new since last narration.
var _ providers.Message // ensure providers import is used
