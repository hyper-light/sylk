package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	pipelineProtocolNamespace             = "pipeline"
	pipelineProtocolEventHandoff          = "handoff_selected"
	pipelineProtocolEventValidation       = "validation_submitted"
	pipelineProtocolEventProcessed        = "validation_processed"
	pipelineProtocolEventReadyForOT       = "ready_for_ot"
	pipelineProtocolEventHandoffToGreen   = "handoff_to_green"
	pipelineProtocolEventTesterFinalize   = "tester_finalize"
	pipelineProtocolEventArtifactConsumed = "tester_artifact_consumed"
	pipelineMailboxItemKindObligation     = "protocol_obligation"
)

type pipelineReadyForOTEvent struct {
	ChallengeID  string   `json:"challenge_id,omitempty"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type pipelineTesterFinalizeEvent struct {
	Refs []PipelineHandoffArtifactRef `json:"refs"`
}

type pipelineArtifactConsumedEvent struct {
	Targets []string `json:"targets"`
}

type pipelineProtocolCheckpoint struct {
	Snapshot        *PipelineProtocolSnapshot             `json:"snapshot,omitempty"`
	Processed       []PipelineValidationProcessing        `json:"processed,omitempty"`
	RequiredAction  string                                `json:"required_action,omitempty"`
	RequiredReason  string                                `json:"required_reason,omitempty"`
	QueuedArtifacts map[string]PipelineHandoffArtifactRef `json:"queued_artifacts,omitempty"`
	// TerminalAction persists the single-shot terminal-action guard.
	// DUR-02 (parallel with globalReviewCheckpoint): losing this on
	// resume would let a crashed-mid-turn state select a second
	// terminal action on restart, which the in-memory guard was
	// supposed to prevent.
	TerminalAction *PipelineTurnAction `json:"terminal_action,omitempty"`
}

type pipelineProtocolObligation struct {
	Key     string         `json:"key"`
	Action  string         `json:"action"`
	Summary string         `json:"summary,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

func newPipelineProtocolStateForTask(task *PipelineTaskInput) (*PipelineProtocolState, error) {
	baseSnapshot, err := PipelineProtocolSnapshotFromTask(task)
	if err != nil {
		return nil, err
	}
	state := NewPipelineProtocolState(baseSnapshot)
	if task == nil || strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.SessionID) == "" {
		return state, nil
	}
	sessionDir := pipelineProtocolSessionDir(task)
	store, err := openDurableProtocolLog(sessionDir, pipelineProtocolNamespace, strings.TrimSpace(task.TaskID))
	if err != nil {
		return state, err
	}
	state.sessionDir = sessionDir
	state.scopeID = strings.TrimSpace(task.TaskID)
	state.store = store

	var checkpoint pipelineProtocolCheckpoint
	if seq, ok, err := store.LoadSnapshot(&checkpoint); err != nil {
		return state, err
	} else if ok {
		// Historical fields come from the persisted checkpoint — the
		// WAL is the authoritative record of what has happened on
		// this scope. These fields are never in task.Context, so
		// there is no merge conflict for them.
		state.processed = clonePipelineValidationProcessingList(checkpoint.Processed)
		state.requiredAction = PipelineProtocolActionType(strings.TrimSpace(checkpoint.RequiredAction))
		state.requiredReason = strings.TrimSpace(checkpoint.RequiredReason)
		state.queuedArtifacts = clonePipelineHandoffArtifactMap(checkpoint.QueuedArtifacts)
		state.terminalAction = clonePipelineTurnAction(checkpoint.TerminalAction)

		// In-flight fields require a merge: the checkpoint's snapshot
		// may be stale relative to task.Context, which carries the
		// dispatcher's most-recent view of the wire. Merge both
		// sources; baseSnapshot wins on in-flight fields when
		// present, but WAL resolutions in `processed` still trump
		// any stale PendingChallenge/Validation the baseSnapshot
		// may carry. See mergePipelineSnapshots for the exact
		// field-by-field rules.
		state.snapshot = mergePipelineSnapshots(checkpoint.Snapshot, baseSnapshot, state.processed)

		if err := state.replayFrom(seq); err != nil {
			return state, err
		}
	} else {
		// No checkpoint on disk — baseSnapshot is the only source.
		// Replay applies any events written since open.
		if err := state.replayFrom(0); err != nil {
			return state, err
		}
		if err := state.persistProjection(); err != nil {
			return state, err
		}
	}
	if err := state.syncMailboxes(); err != nil {
		return state, err
	}
	return state, nil
}

// mergePipelineSnapshots reconciles two sources of truth for pipeline
// protocol state:
//
//   - checkpoint: the persisted snapshot from the durable log. Authoritative
//     for historical facts (what events have been processed, what is
//     queued, what terminal action was recorded).
//
//   - base: the fresh snapshot that rode along on task.Context from the
//     dispatcher. Authoritative for in-flight facts (which challenge is
//     pending right now, what validation just arrived, which agents are
//     active in the current turn).
//
// The merge rules, in order:
//
//  1. Start with the checkpoint's snapshot. Historical fields are already
//     correct there.
//  2. Replace in-flight fields (PendingChallenge, PendingValidation,
//     AuditLock, CurrentRequest, ActiveAgents, RequestedBy, Mode) with
//     values from `base` when `base` has them populated. `base` is
//     newer by construction — the dispatcher stamped it at dispatch
//     time, the checkpoint was captured earlier.
//  3. WAL resolutions trump stale in-flight state: if `processed`
//     contains a resolution for a ChallengeID that `base` still shows
//     as pending, drop the pending entry. The WAL's record of
//     resolution is historical and final; a stale in-flight entry
//     cannot resurrect it.
//
// Edge cases:
//
//   - base == nil: the task arrived without a pipeline_protocol snapshot
//     in its context (edge case; indicates a dispatcher bug or a
//     non-dispatched state open). Return the checkpoint verbatim.
//   - checkpoint == nil: first-ever open for this scope. Return `base`
//     verbatim.
//   - both nil: empty state.
//
// When the merge produces a divergence — a field where both sources have
// non-empty values and they disagree — a structured log event
// (pipeline_protocol.snapshot_merge_diverged) is emitted so observability
// can track whether this path is the load-bearing defense or just
// redundant belt-and-suspenders.
func mergePipelineSnapshots(
	checkpoint *PipelineProtocolSnapshot,
	base *PipelineProtocolSnapshot,
	processed []PipelineValidationProcessing,
) *PipelineProtocolSnapshot {
	if checkpoint == nil && base == nil {
		return nil
	}
	if base == nil {
		return clonePipelineProtocolSnapshot(checkpoint)
	}
	if checkpoint == nil {
		merged := clonePipelineProtocolSnapshot(base)
		applyProcessedResolutionsToMerged(merged, processed)
		return merged
	}

	merged := clonePipelineProtocolSnapshot(checkpoint)
	baseClone := clonePipelineProtocolSnapshot(base)

	divergedFields := make([]string, 0, 4)

	// PendingChallenge: base wins when present; checkpoint wins only
	// when base has none. Either way, if `processed` has a matching
	// resolution, the result is nil (WAL resolution trumps stale
	// pending).
	if baseClone.PendingChallenge != nil {
		if merged.PendingChallenge != nil &&
			!pipelineChallengeEqual(merged.PendingChallenge, baseClone.PendingChallenge) {
			divergedFields = append(divergedFields, "PendingChallenge")
			logSnapshotRescue("PendingChallenge", baseClone.PendingChallenge.ID, merged.PendingChallenge.ID)
		} else if merged.PendingChallenge == nil {
			// The critical case: checkpoint lacked the challenge the
			// dispatcher just committed. Rescue emits its own log
			// so the Fix-B defense is observable.
			logSnapshotRescue("PendingChallenge", baseClone.PendingChallenge.ID, "")
		}
		merged.PendingChallenge = baseClone.PendingChallenge
	}

	// PendingValidation: same rule as PendingChallenge.
	if baseClone.PendingValidation != nil {
		if merged.PendingValidation != nil &&
			!pipelineValidationEqual(merged.PendingValidation, baseClone.PendingValidation) {
			divergedFields = append(divergedFields, "PendingValidation")
		}
		merged.PendingValidation = baseClone.PendingValidation
	}

	// AuditLock: base wins when non-empty phase.
	if baseClone.AuditLock != nil && strings.TrimSpace(baseClone.AuditLock.Phase) != "" {
		if merged.AuditLock != nil &&
			strings.TrimSpace(merged.AuditLock.Phase) != "" &&
			merged.AuditLock.Phase != baseClone.AuditLock.Phase {
			divergedFields = append(divergedFields, "AuditLock")
		}
		merged.AuditLock = baseClone.AuditLock
	}

	// CurrentRequest: base wins when non-empty.
	if strings.TrimSpace(baseClone.CurrentRequest) != "" {
		if strings.TrimSpace(merged.CurrentRequest) != "" &&
			merged.CurrentRequest != baseClone.CurrentRequest {
			divergedFields = append(divergedFields, "CurrentRequest")
		}
		merged.CurrentRequest = baseClone.CurrentRequest
	}

	// ActiveAgents: base wins when non-empty.
	if len(baseClone.ActiveAgents) > 0 {
		merged.ActiveAgents = append([]string(nil), baseClone.ActiveAgents...)
	}

	// RequestedBy: base wins when non-empty.
	if strings.TrimSpace(baseClone.RequestedBy) != "" {
		merged.RequestedBy = baseClone.RequestedBy
	}

	// Mode: base wins when non-empty.
	if strings.TrimSpace(baseClone.Mode) != "" {
		merged.Mode = baseClone.Mode
	}

	// Apply WAL resolutions LAST so they trump any stale pending
	// entries that survived the above copies.
	applyProcessedResolutionsToMerged(merged, processed)

	if len(divergedFields) > 0 {
		logSnapshotDiverged(divergedFields, checkpoint, base)
	}

	return merged
}

// applyProcessedResolutionsToMerged drops any PendingChallenge or
// PendingValidation whose ChallengeID is in the processed list. The WAL's
// record of resolution is final; the merged snapshot must not carry
// contradicting stale-pending state forward.
func applyProcessedResolutionsToMerged(
	merged *PipelineProtocolSnapshot,
	processed []PipelineValidationProcessing,
) {
	if merged == nil || len(processed) == 0 {
		return
	}
	resolvedIDs := make(map[string]struct{}, len(processed))
	for _, entry := range processed {
		id := strings.TrimSpace(entry.ChallengeID)
		if id != "" {
			resolvedIDs[id] = struct{}{}
		}
	}
	if len(resolvedIDs) == 0 {
		return
	}
	if merged.PendingChallenge != nil {
		if _, resolved := resolvedIDs[strings.TrimSpace(merged.PendingChallenge.ID)]; resolved {
			merged.PendingChallenge = nil
		}
	}
	if merged.PendingValidation != nil {
		if _, resolved := resolvedIDs[strings.TrimSpace(merged.PendingValidation.ChallengeID)]; resolved {
			merged.PendingValidation = nil
		}
	}
}

// pipelineChallengeEqual compares two PipelineProtocolChallenge values for
// equivalence on the fields that identify a single logical challenge (ID
// + requesting agent). Two challenges with matching ID+requester are the
// same logical event even if metadata differs slightly between the
// checkpoint view and the dispatcher view.
func pipelineChallengeEqual(a, b *PipelineProtocolChallenge) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSpace(a.ID) == strings.TrimSpace(b.ID) &&
		normalizePipelineAgentType(a.RequestingAgent) == normalizePipelineAgentType(b.RequestingAgent)
}

// pipelineValidationEqual compares two PipelineValidationRecord values
// for equivalence on the identifying fields (ChallengeID + responding
// agent + status).
func pipelineValidationEqual(a, b *PipelineValidationRecord) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSpace(a.ChallengeID) == strings.TrimSpace(b.ChallengeID) &&
		normalizePipelineAgentType(a.RespondingAgent) == normalizePipelineAgentType(b.RespondingAgent) &&
		strings.TrimSpace(a.Status) == strings.TrimSpace(b.Status)
}

// logSnapshotRescue emits the Fix-B-specific observability signal: the
// baseSnapshot carried an in-flight item the checkpoint lacked (or had a
// different version of). Direct telemetry on how often the merge is the
// last line of defense.
func logSnapshotRescue(field, baseID, checkpointID string) {
	slog.Info("pipeline_protocol.snapshot_rescued_from_task_context",
		"field", field,
		"base_id", strings.TrimSpace(baseID),
		"checkpoint_id", strings.TrimSpace(checkpointID),
	)
}

// logSnapshotDiverged emits a signal when both sources have non-empty
// values and they disagree. This is a weaker signal than
// snapshot_rescued_from_task_context — it means the merge had a genuine
// choice to make. In a healthy system this fires rarely.
func logSnapshotDiverged(fields []string, checkpoint, base *PipelineProtocolSnapshot) {
	slog.Info("pipeline_protocol.snapshot_merge_diverged",
		"diverging_fields", fields,
		"checkpoint_pending_challenge_id", pendingChallengeID(checkpoint),
		"base_pending_challenge_id", pendingChallengeID(base),
	)
}

func pendingChallengeID(snapshot *PipelineProtocolSnapshot) string {
	if snapshot == nil || snapshot.PendingChallenge == nil {
		return ""
	}
	return strings.TrimSpace(snapshot.PendingChallenge.ID)
}

func pipelineProtocolSessionDir(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	if task.Context != nil {
		if sessionDir, _ := task.Context["session_dir"].(string); strings.TrimSpace(sessionDir) != "" {
			return strings.TrimSpace(sessionDir)
		}
	}
	if strings.TrimSpace(task.SessionID) == "" {
		return ""
	}
	return filepath.Join(".sylk", "sessions", strings.TrimSpace(task.SessionID))
}

func (s *PipelineProtocolState) recordHandoffAction(ctx context.Context, action *PipelineTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	if err := s.appendEvent(ctx, pipelineProtocolEventHandoff, action); err != nil {
		return err
	}
	return nil
}

func (s *PipelineProtocolState) recordValidation(ctx context.Context, record *PipelineValidationRecord) error {
	if s == nil || record == nil {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventValidation, record)
}

func (s *PipelineProtocolState) recordValidationProcessing(ctx context.Context, entry PipelineValidationProcessing) error {
	if s == nil {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventProcessed, entry)
}

func (s *PipelineProtocolState) recordReadyForOT(ctx context.Context, summary string, evidenceRefs []string, record *PipelineValidationRecord) error {
	if s == nil {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventReadyForOT, pipelineReadyForOTEvent{
		ChallengeID:  firstChallengeID(record),
		Summary:      strings.TrimSpace(summary),
		EvidenceRefs: normalizeStringList(evidenceRefs),
	})
}

func (s *PipelineProtocolState) recordHandoffToGreen(ctx context.Context, action *PipelineTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventHandoffToGreen, action)
}

// recordTesterFinalize appends the per-recipient verification artifact refs
// produced by the tester finalize_pipeline call. The apply handler populates
// the protocol-state queue so subsequent handoff_next/validate_work dispatch
// can thread the right ref to each recipient.
func (s *PipelineProtocolState) recordTesterFinalize(ctx context.Context, refs []PipelineHandoffArtifactRef) error {
	if s == nil || len(refs) == 0 {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventTesterFinalize, pipelineTesterFinalizeEvent{
		Refs: clonePipelineHandoffArtifactRefList(refs),
	})
}

// sweepAgedArtifacts auto-discards queued artifacts whose age (current
// iteration minus QueuedAtIteration) exceeds pipelineArtifactMaxIterations.
// Bounded-loss convergence guard: prevents queue accumulation across
// pipeline iterations when an LLM finalizes for a recipient but never
// routes work toward them. The auto-discard surfaces as a normal
// consumed event in the durable log so observability tooling can see
// what was dropped and at what age. No error is returned — the LLM sees
// the post-sweep state via its next queue_state advisory and can
// re-finalize if it still needs the artifact.
func (s *PipelineProtocolState) sweepAgedArtifacts(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	currentIteration := 0
	if s.snapshot != nil {
		currentIteration = s.snapshot.Iteration
	}
	aged := make([]string, 0)
	for target, ref := range s.queuedArtifacts {
		if ref.QueuedAtIteration <= 0 {
			continue
		}
		if currentIteration-ref.QueuedAtIteration > pipelineArtifactMaxIterations {
			aged = append(aged, target)
		}
	}
	s.mu.RUnlock()
	if len(aged) == 0 {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventArtifactConsumed, pipelineArtifactConsumedEvent{
		Targets: aged,
	})
}

// consumeQueuedArtifacts records that the named targets' queued artifact refs
// have been consumed by a successful dispatch. Cleared from the protocol-state
// queue so the next turn doesn't re-thread stale refs.
func (s *PipelineProtocolState) consumeQueuedArtifacts(ctx context.Context, targets []string) error {
	if s == nil {
		return nil
	}
	normalized := make([]string, 0, len(targets))
	for _, target := range targets {
		t := normalizePipelineAgentType(target)
		if t == "" {
			continue
		}
		normalized = append(normalized, t)
	}
	if len(normalized) == 0 {
		return nil
	}
	s.mu.RLock()
	hasQueued := false
	for _, target := range normalized {
		if _, ok := s.queuedArtifacts[target]; ok {
			hasQueued = true
			break
		}
	}
	s.mu.RUnlock()
	if !hasQueued {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventArtifactConsumed, pipelineArtifactConsumedEvent{
		Targets: normalized,
	})
}

func (s *PipelineProtocolState) CurrentAgentObligations(agentType string) []map[string]any {
	agentType = normalizePipelineAgentType(agentType)
	if agentType == "" {
		return nil
	}
	desired := s.desiredMailboxItems()
	items := desired[agentType]
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"key":     item.Key,
			"action":  item.Action,
			"summary": item.Summary,
			"payload": decodeMailboxPayload(item.Payload),
		})
	}
	return out
}

func (s *PipelineProtocolState) appendEvent(ctx context.Context, kind string, payload any) error {
	if s == nil {
		return nil
	}
	if s.store == nil {
		var encoded json.RawMessage
		if payload != nil {
			data, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			encoded = data
		}
		return s.applyEvent(0, &durableProtocolEvent{
			Namespace: pipelineProtocolNamespace,
			ScopeID:   s.scopeID,
			Kind:      kind,
			AgentType: pipelineProtocolAgentTypeFromContext(ctx),
			CreatedAt: time.Now().UTC(),
			Payload:   encoded,
		})
	}
	stream, _ := StreamMetadataFromContext(ctx)
	result, err := s.store.Append(AppendRequest{
		Kind:                kind,
		AgentType:           pipelineProtocolAgentTypeFromContext(ctx),
		CorrelationID:       streamCorrelationID(stream),
		ParentCorrelationID: streamParentCorrelationID(stream),
		// IdempotencyKey is intentionally left empty — the
		// three-layer dedupe (kind, correlation_id, payload
		// fingerprint) composed by the durable log is sufficient
		// for pipeline protocol events. Distinct PipelineTurnActions
		// differ in their ChallengeID / target / request / payload,
		// so their fingerprints differ; retries of the exact same
		// logical event produce identical fingerprints and are
		// correctly deduped.
		Payload: payload,
	})
	if err != nil {
		// DUR-03: a duplicate fingerprint indicates this event was
		// already appended and projected in a prior turn or crash-
		// recovery. The projection and mailboxes are already in the
		// expected post-event state, so downstream work would be a
		// no-op at best and a double-apply at worst. Treat as
		// success — the protocol step's external contract (event
		// recorded, state advanced) is satisfied.
		//
		// The durableProtocolLog emits a structured
		// pipeline_protocol.dedupe_hit log line on every dedupe so
		// observability catches unexpected same-payload repeats
		// (caller bugs) while silently absorbing the expected
		// crash-recovery case.
		if errors.Is(err, ErrDurableProtocolDuplicate) {
			return nil
		}
		return err
	}
	if err := s.applyEvent(result.Seq, result.Event); err != nil {
		return err
	}
	if err := s.persistProjection(); err != nil {
		return err
	}
	return s.syncMailboxes()
}

func (s *PipelineProtocolState) replayFrom(seq uint64) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Replay(seq, func(nextSeq uint64, event *durableProtocolEvent) error {
		return s.applyEvent(nextSeq, event)
	})
}

func (s *PipelineProtocolState) applyEvent(seq uint64, event *durableProtocolEvent) error {
	if s == nil || event == nil {
		return nil
	}
	switch strings.TrimSpace(event.Kind) {
	case pipelineProtocolEventHandoff:
		var action PipelineTurnAction
		if err := decodeProtocolPayload(event.Payload, &action); err != nil {
			return err
		}
		s.applyHandoffEvent(&action)
	case pipelineProtocolEventValidation:
		var record PipelineValidationRecord
		if err := decodeProtocolPayload(event.Payload, &record); err != nil {
			return err
		}
		s.applyValidationEvent(&record)
	case pipelineProtocolEventProcessed:
		var entry PipelineValidationProcessing
		if err := decodeProtocolPayload(event.Payload, &entry); err != nil {
			return err
		}
		s.applyProcessedValidationEvent(entry)
	case pipelineProtocolEventReadyForOT:
		var ready pipelineReadyForOTEvent
		if err := decodeProtocolPayload(event.Payload, &ready); err != nil {
			return err
		}
		s.applyReadyForOTEvent(ready)
	case pipelineProtocolEventHandoffToGreen:
		var action PipelineTurnAction
		if err := decodeProtocolPayload(event.Payload, &action); err != nil {
			return err
		}
		s.applyHandoffToGreenEvent(&action)
	case pipelineProtocolEventTesterFinalize:
		var finalize pipelineTesterFinalizeEvent
		if err := decodeProtocolPayload(event.Payload, &finalize); err != nil {
			return err
		}
		s.applyTesterFinalizeEvent(finalize)
	case pipelineProtocolEventArtifactConsumed:
		var consumed pipelineArtifactConsumedEvent
		if err := decodeProtocolPayload(event.Payload, &consumed); err != nil {
			return err
		}
		s.applyArtifactConsumedEvent(consumed)
	}
	_ = seq
	return nil
}

func (s *PipelineProtocolState) persistProjection() error {
	if s == nil || s.store == nil {
		return nil
	}
	checkpoint := pipelineProtocolCheckpoint{
		Snapshot:        clonePipelineProtocolSnapshot(s.snapshot),
		Processed:       clonePipelineValidationProcessingList(s.processed),
		RequiredAction:  strings.TrimSpace(string(s.requiredAction)),
		RequiredReason:  strings.TrimSpace(s.requiredReason),
		QueuedArtifacts: clonePipelineHandoffArtifactMap(s.queuedArtifacts),
		TerminalAction:  clonePipelineTurnAction(s.terminalAction),
	}
	return s.store.SaveSnapshot(s.store.journal.LastSequence(), checkpoint)
}

func (s *PipelineProtocolState) syncMailboxes() error {
	if s == nil || s.sessionDir == "" || s.scopeID == "" {
		return nil
	}
	desired := s.desiredMailboxItems()
	for _, agentType := range pipelineProtocolMailboxAgents(s.Snapshot()) {
		mailbox, err := openDurableAgentMailbox(s.sessionDir, agentType)
		if err != nil {
			return err
		}
		if err := mailbox.Sync(pipelineProtocolNamespace, s.scopeID, desired[agentType]); err != nil {
			_ = mailbox.Close()
			return err
		}
		if err := mailbox.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *PipelineProtocolState) desiredMailboxItems() map[string][]durableMailboxItem {
	desired := map[string][]durableMailboxItem{}
	snapshot := materializePipelineProtocolSnapshot(s)
	if snapshot == nil {
		return desired
	}
	if challenge := snapshot.PendingChallenge; challenge != nil {
		for _, rawTarget := range challenge.TargetAgents {
			target := normalizePipelineAgentType(rawTarget)
			if target == "" {
				continue
			}
			desired[target] = append(desired[target], durableMailboxItem{
				Key:      fmt.Sprintf("pipeline:%s:%s:challenge:%s", s.scopeID, target, strings.TrimSpace(challenge.ID)),
				ItemKind: pipelineMailboxItemKindObligation,
				Action:   "validate_work",
				Summary:  strings.TrimSpace(challenge.Request),
				Payload: mustMarshalRaw(map[string]any{
					"challenge_id":        strings.TrimSpace(challenge.ID),
					"requesting_agent":    strings.TrimSpace(challenge.RequestingAgent),
					"requesting_agent_id": strings.TrimSpace(challenge.RequestingAgentID),
					"required_output":     append([]string(nil), challenge.RequiredOutput...),
					"references":          append([]string(nil), challenge.References...),
				}),
			})
		}
	}
	if validation := snapshot.PendingValidation; validation != nil {
		requesting := normalizePipelineAgentType(validation.RequestingAgent)
		if requesting != "" {
			desired[requesting] = append(desired[requesting], durableMailboxItem{
				Key:      fmt.Sprintf("pipeline:%s:%s:process:%s", s.scopeID, requesting, strings.TrimSpace(validation.ChallengeID)),
				ItemKind: pipelineMailboxItemKindObligation,
				Action:   "process_validation",
				Summary:  strings.TrimSpace(validation.Summary),
				Payload:  mustMarshalRaw(cloneValidationRecord(validation)),
			})
		}
	}
	if strings.TrimSpace(string(s.requiredAction)) == string(PipelineProtocolActionOT) {
		desired[PipelineAgentInspector] = append(desired[PipelineAgentInspector], durableMailboxItem{
			Key:      fmt.Sprintf("pipeline:%s:%s:required:%s", s.scopeID, PipelineAgentInspector, PipelineProtocolActionOT),
			ItemKind: pipelineMailboxItemKindObligation,
			Action:   string(PipelineProtocolActionOT),
			Summary:  strings.TrimSpace(s.requiredReason),
			Payload: mustMarshalRaw(map[string]any{
				"required_action": string(PipelineProtocolActionOT),
				"reason":          strings.TrimSpace(s.requiredReason),
			}),
		})
	} else if record, ok := finalizePipelineValidationReady(snapshot, s.processed); ok && snapshot.PendingValidation == nil {
		desired[PipelineAgentInspector] = append(desired[PipelineAgentInspector], durableMailboxItem{
			Key:      fmt.Sprintf("pipeline:%s:%s:finalize:%s", s.scopeID, PipelineAgentInspector, strings.TrimSpace(record.ChallengeID)),
			ItemKind: pipelineMailboxItemKindObligation,
			Action:   "finalize_pipeline",
			Summary:  "A tester-backed final audit is accepted; finalize the pipeline now.",
			Payload:  mustMarshalRaw(cloneValidationRecord(record)),
		})
	}
	return desired
}

func (s *PipelineProtocolState) applyHandoffEvent(action *PipelineTurnAction) {
	if action == nil {
		return
	}
	s.snapshot = buildPipelineSnapshotAfterHandoff(s.snapshot, action)
}

func (s *PipelineProtocolState) applyValidationEvent(record *PipelineValidationRecord) {
	if record == nil {
		return
	}
	if s.snapshot == nil {
		s.snapshot = &PipelineProtocolSnapshot{}
	}
	s.snapshot.ActiveAgents = []string{normalizePipelineAgentType(record.RequestingAgent)}
	s.snapshot.RequestedBy = normalizePipelineAgentType(record.RespondingAgent)
	s.snapshot.Mode = string(PipelineTurnModeSingle)
	s.snapshot.CurrentRequest = fmt.Sprintf("Process validation response for challenge %s and decide the next handoff.", strings.TrimSpace(record.ChallengeID))
	s.snapshot.PendingChallenge = nil
	s.snapshot.PendingValidation = cloneValidationRecord(record)
	appendPipelineProtocolEvent(s.snapshot, PipelineProtocolEvent{
		Type:      string(PipelineProtocolActionValidate),
		AgentType: record.RespondingAgent,
		Targets:   []string{record.RequestingAgent},
		Summary:   strings.TrimSpace(record.Summary),
	})
}

func (s *PipelineProtocolState) applyProcessedValidationEvent(entry PipelineValidationProcessing) {
	s.processed = append(s.processed, clonePipelineValidationProcessing(entry))
	if s.snapshot == nil {
		return
	}
	pending := s.snapshot.PendingValidation
	if pending == nil {
		return
	}
	if strings.TrimSpace(pending.ChallengeID) != strings.TrimSpace(entry.ChallengeID) {
		return
	}
	s.snapshot.PendingValidation = nil
	s.snapshot.CurrentRequest = fmt.Sprintf(
		"Choose the next pipeline action after processing challenge %s.",
		strings.TrimSpace(entry.ChallengeID),
	)
}

func (s *PipelineProtocolState) applyReadyForOTEvent(ready pipelineReadyForOTEvent) {
	s.requiredAction = PipelineProtocolActionOT
	s.requiredReason = "The audit already passed; `handoff_to_green` is the only valid way to end this inspector turn."
	if s.snapshot == nil {
		s.snapshot = &PipelineProtocolSnapshot{}
	}
	appendPipelineProtocolEvent(s.snapshot, PipelineProtocolEvent{
		Type:      "finalize_pipeline",
		AgentType: PipelineAgentInspector,
		Targets:   []string{PipelineAgentInspector},
		Summary:   firstNonEmpty(strings.TrimSpace(ready.Summary), "Pipeline ready for OT handoff."),
	})
}

func (s *PipelineProtocolState) applyHandoffToGreenEvent(action *PipelineTurnAction) {
	s.requiredAction = ""
	s.requiredReason = ""
	if s.snapshot == nil {
		s.snapshot = &PipelineProtocolSnapshot{}
	}
	s.snapshot.PendingChallenge = nil
	s.snapshot.PendingValidation = nil
	appendPipelineProtocolEvent(s.snapshot, PipelineProtocolEvent{
		Type:      string(PipelineProtocolActionOT),
		AgentType: PipelineAgentInspector,
		Targets:   []string{PipelineAgentInspector},
		Summary:   strings.TrimSpace(action.Summary),
	})
}

// applyTesterFinalizeEvent populates the queued-artifacts map with the per-
// recipient refs the tester finalize_pipeline produced. Re-finalize for an
// existing target overwrites the prior ref (the no-repeat guard in the skill
// handler ensures the suite changed before this overwrite is allowed).
func (s *PipelineProtocolState) applyTesterFinalizeEvent(event pipelineTesterFinalizeEvent) {
	if len(event.Refs) == 0 {
		return
	}
	if s.queuedArtifacts == nil {
		s.queuedArtifacts = make(map[string]PipelineHandoffArtifactRef, len(event.Refs))
	}
	for _, ref := range event.Refs {
		target := normalizePipelineAgentType(ref.Target)
		if target == "" {
			continue
		}
		s.queuedArtifacts[target] = cloneHandoffArtifactRef(ref)
	}
	if s.snapshot == nil {
		s.snapshot = &PipelineProtocolSnapshot{}
	}
	targets := make([]string, 0, len(event.Refs))
	for _, ref := range event.Refs {
		targets = append(targets, normalizePipelineAgentType(ref.Target))
	}
	appendPipelineProtocolEvent(s.snapshot, PipelineProtocolEvent{
		Type:      pipelineProtocolEventTesterFinalize,
		AgentType: PipelineAgentTester,
		Targets:   targets,
		Summary:   "Tester verification artifacts finalized for downstream recipients.",
	})
}

// applyArtifactConsumedEvent removes the named targets' refs from the queue.
// Successful handoff_next/validate_work dispatch records this so the queue
// doesn't survive into the next turn with stale refs.
func (s *PipelineProtocolState) applyArtifactConsumedEvent(event pipelineArtifactConsumedEvent) {
	if len(event.Targets) == 0 || len(s.queuedArtifacts) == 0 {
		return
	}
	for _, target := range event.Targets {
		t := normalizePipelineAgentType(target)
		if t == "" {
			continue
		}
		delete(s.queuedArtifacts, t)
	}
	if len(s.queuedArtifacts) == 0 {
		s.queuedArtifacts = nil
	}
}

func buildPipelineSnapshotAfterHandoff(base *PipelineProtocolSnapshot, action *PipelineTurnAction) *PipelineProtocolSnapshot {
	snapshot := clonePipelineProtocolSnapshot(base)
	if snapshot == nil {
		snapshot = &PipelineProtocolSnapshot{}
	}
	snapshot.ActiveAgents = append([]string(nil), action.TargetAgents...)
	snapshot.RequestedBy = action.AgentType
	snapshot.Mode = string(action.Mode)
	snapshot.CurrentRequest = strings.TrimSpace(action.Request)
	snapshot.AuditLock = nextPipelineAuditLock(snapshot.AuditLock, action)
	snapshot.PendingValidation = nil
	snapshot.PendingChallenge = nil
	if action.CreatesChallenge {
		snapshot.PendingChallenge = &PipelineProtocolChallenge{
			ID:                firstNonEmpty(strings.TrimSpace(action.ChallengeID), uuid.NewString()),
			RequestingAgent:   normalizePipelineAgentType(action.AgentType),
			RequestingAgentID: strings.TrimSpace(action.AgentID),
			TargetAgents:      append([]string(nil), action.TargetAgents...),
			Mode:              string(action.Mode),
			Reason:            strings.TrimSpace(action.Reason),
			Request:           strings.TrimSpace(action.Request),
			RequiredOutput:    append([]string(nil), action.RequiredOutput...),
			References:        append([]string(nil), action.References...),
		}
	}
	appendPipelineProtocolEvent(snapshot, PipelineProtocolEvent{
		Type:                 string(action.Type),
		AgentType:            action.AgentType,
		Targets:              append([]string(nil), action.TargetAgents...),
		Summary:              firstNonEmpty(strings.TrimSpace(action.Request), strings.TrimSpace(action.Summary)),
		CreatesChallenge:     action.CreatesChallenge,
		WorkspaceFingerprint: strings.TrimSpace(action.WorkspaceFingerprint),
	})
	return snapshot
}

func pipelineProtocolMailboxAgents(snapshot *PipelineProtocolSnapshot) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(agent string) {
		agent = normalizePipelineAgentType(agent)
		if agent == "" {
			return
		}
		if _, ok := seen[agent]; ok {
			return
		}
		seen[agent] = struct{}{}
		out = append(out, agent)
	}
	add(PipelineAgentInspector)
	add(PipelineAgentTester)
	add(PipelineAgentEngineer)
	add(PipelineAgentDesigner)
	if snapshot != nil {
		for _, member := range snapshot.Roster {
			add(member.AgentType)
		}
		if snapshot.PendingChallenge != nil {
			add(snapshot.PendingChallenge.RequestingAgent)
			for _, target := range snapshot.PendingChallenge.TargetAgents {
				add(target)
			}
		}
		if snapshot.PendingValidation != nil {
			add(snapshot.PendingValidation.RequestingAgent)
			add(snapshot.PendingValidation.RespondingAgent)
		}
		for _, active := range snapshot.ActiveAgents {
			add(active)
		}
	}
	return out
}

func firstChallengeID(record *PipelineValidationRecord) string {
	if record == nil {
		return ""
	}
	return strings.TrimSpace(record.ChallengeID)
}

func clonePipelineValidationProcessingList(values []PipelineValidationProcessing) []PipelineValidationProcessing {
	if len(values) == 0 {
		return nil
	}
	out := make([]PipelineValidationProcessing, len(values))
	for i, value := range values {
		out[i] = clonePipelineValidationProcessing(value)
	}
	return out
}

func decodeProtocolPayload(data json.RawMessage, out any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func mustMarshalRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func decodeMailboxPayload(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return out
}

func streamCorrelationID(stream StreamContext) string {
	return strings.TrimSpace(stream.CorrelationID)
}

func streamParentCorrelationID(stream StreamContext) string {
	if len(stream.Metadata) == 0 {
		return ""
	}
	if value, _ := stream.Metadata[streamMetadataParentCorrelation].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func pipelineProtocolAgentTypeFromContext(ctx context.Context) string {
	if contract := TaskExecutionContractFromContext(ctx); contract != nil {
		if agentType := normalizePipelineAgentType(contract.RuntimeAgentType); agentType != "" {
			return agentType
		}
	}
	if task := PipelineTaskFromContext(ctx); task != nil {
		if agentType := normalizePipelineAgentType(task.AgentType); agentType != "" {
			return agentType
		}
	}
	return ""
}
