package shared

import (
	"context"
	"encoding/json"
	"fmt"
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
	pipelineProtocolEventHandoffToOT      = "handoff_to_ot"
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
		state.snapshot = clonePipelineProtocolSnapshot(checkpoint.Snapshot)
		state.processed = clonePipelineValidationProcessingList(checkpoint.Processed)
		state.requiredAction = PipelineProtocolActionType(strings.TrimSpace(checkpoint.RequiredAction))
		state.requiredReason = strings.TrimSpace(checkpoint.RequiredReason)
		state.queuedArtifacts = clonePipelineHandoffArtifactMap(checkpoint.QueuedArtifacts)
		if err := state.replayFrom(seq); err != nil {
			return state, err
		}
	} else {
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

func (s *PipelineProtocolState) recordHandoffToOT(ctx context.Context, action *PipelineTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	return s.appendEvent(ctx, pipelineProtocolEventHandoffToOT, action)
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
	seq, event, err := s.store.Append(
		kind,
		pipelineProtocolAgentTypeFromContext(ctx),
		streamCorrelationID(stream),
		streamParentCorrelationID(stream),
		payload,
	)
	if err != nil {
		return err
	}
	if err := s.applyEvent(seq, event); err != nil {
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
	case pipelineProtocolEventHandoffToOT:
		var action PipelineTurnAction
		if err := decodeProtocolPayload(event.Payload, &action); err != nil {
			return err
		}
		s.applyHandoffToOTEvent(&action)
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
	s.requiredReason = "The audit already passed; `handoff_to_ot` is the only valid way to end this inspector turn."
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

func (s *PipelineProtocolState) applyHandoffToOTEvent(action *PipelineTurnAction) {
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
