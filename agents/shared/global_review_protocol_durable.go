package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	globalReviewNamespace             = "global_review"
	globalReviewEventChallenge        = "challenge_selected"
	globalReviewEventValidation       = "validation_submitted"
	globalReviewEventProcessed        = "validation_processed"
	globalReviewEventReadyCheckpoint  = "ready_for_checkpoint"
	globalReviewEventReadyForCommit   = "ready_for_commit"
	globalReviewEventAcceptCheckpoint = "accept_checkpoint"
	globalReviewEventCommitToDisk     = "commit_to_disk"
	globalReviewMailboxItemKind       = "protocol_obligation"
)

type globalReviewReadyForCommitEvent struct {
	ChallengeID  string   `json:"challenge_id,omitempty"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type globalReviewCheckpoint struct {
	Snapshot       *GlobalReviewSnapshot              `json:"snapshot,omitempty"`
	Processed      []GlobalReviewValidationProcessing `json:"processed,omitempty"`
	RequiredAction string                             `json:"required_action,omitempty"`
	RequiredReason string                             `json:"required_reason,omitempty"`
}

func (s *GlobalReviewState) loadDurableProjection() error {
	if s == nil || s.store == nil {
		return nil
	}
	var checkpoint globalReviewCheckpoint
	if seq, ok, err := s.store.LoadSnapshot(&checkpoint); err != nil {
		return err
	} else if ok {
		s.snapshot = cloneGlobalReviewSnapshot(checkpoint.Snapshot)
		s.processed = cloneGlobalReviewValidationProcessingList(checkpoint.Processed)
		s.requiredAction = GlobalReviewActionType(strings.TrimSpace(checkpoint.RequiredAction))
		s.requiredReason = strings.TrimSpace(checkpoint.RequiredReason)
		return s.replayFrom(seq)
	}
	if err := s.replayFrom(0); err != nil {
		return err
	}
	return s.persistProjection()
}

func (s *GlobalReviewState) recordChallenge(ctx context.Context, action *GlobalReviewTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventChallenge, action)
}

func (s *GlobalReviewState) recordValidation(ctx context.Context, record *GlobalReviewValidationRecord) error {
	if s == nil || record == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventValidation, record)
}

func (s *GlobalReviewState) recordValidationProcessing(ctx context.Context, entry GlobalReviewValidationProcessing) error {
	if s == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventProcessed, entry)
}

func (s *GlobalReviewState) recordReadyForCommit(ctx context.Context, summary string, evidenceRefs []string, record *GlobalReviewValidationRecord) error {
	if s == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventReadyForCommit, globalReviewReadyForCommitEvent{
		ChallengeID:  globalReviewChallengeID(record),
		Summary:      strings.TrimSpace(summary),
		EvidenceRefs: normalizeStringList(evidenceRefs),
	})
}

func (s *GlobalReviewState) recordReadyForCheckpoint(ctx context.Context, summary string, evidenceRefs []string, record *GlobalReviewValidationRecord) error {
	if s == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventReadyCheckpoint, globalReviewReadyForCommitEvent{
		ChallengeID:  globalReviewChallengeID(record),
		Summary:      strings.TrimSpace(summary),
		EvidenceRefs: normalizeStringList(evidenceRefs),
	})
}

func (s *GlobalReviewState) recordCheckpointAccepted(ctx context.Context, action *GlobalReviewTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventAcceptCheckpoint, action)
}

func (s *GlobalReviewState) recordCommitToDisk(ctx context.Context, action *GlobalReviewTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	return s.appendEvent(ctx, globalReviewEventCommitToDisk, action)
}

func (s *GlobalReviewState) CurrentAgentObligations(agentType string) []map[string]any {
	agentType = normalizeGlobalReviewAgent(agentType)
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

func (s *GlobalReviewState) appendEvent(ctx context.Context, kind string, payload any) error {
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
			Namespace: globalReviewNamespace,
			ScopeID:   s.scopeID,
			Kind:      kind,
			AgentType: globalReviewAgentTypeFromContext(ctx),
			Payload:   encoded,
		})
	}
	stream, _ := StreamMetadataFromContext(ctx)
	seq, event, err := s.store.Append(kind, globalReviewAgentTypeFromContext(ctx), streamCorrelationID(stream), streamParentCorrelationID(stream), payload)
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

func (s *GlobalReviewState) replayFrom(seq uint64) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Replay(seq, func(nextSeq uint64, event *durableProtocolEvent) error {
		return s.applyEvent(nextSeq, event)
	})
}

func (s *GlobalReviewState) applyEvent(seq uint64, event *durableProtocolEvent) error {
	if s == nil || event == nil {
		return nil
	}
	switch strings.TrimSpace(event.Kind) {
	case globalReviewEventChallenge:
		var action GlobalReviewTurnAction
		if err := decodeProtocolPayload(event.Payload, &action); err != nil {
			return err
		}
		s.snapshot = buildGlobalReviewSnapshotAfterChallenge(s.snapshot, &action)
	case globalReviewEventValidation:
		var record GlobalReviewValidationRecord
		if err := decodeProtocolPayload(event.Payload, &record); err != nil {
			return err
		}
		s.applyValidationEvent(&record)
	case globalReviewEventProcessed:
		var entry GlobalReviewValidationProcessing
		if err := decodeProtocolPayload(event.Payload, &entry); err != nil {
			return err
		}
		s.processed = append(s.processed, cloneGlobalReviewValidationProcessing(entry))
		if s.snapshot != nil && s.snapshot.PendingValidation != nil &&
			strings.TrimSpace(s.snapshot.PendingValidation.ChallengeID) == strings.TrimSpace(entry.ChallengeID) {
			s.snapshot.PendingValidation = nil
			s.snapshot.CurrentRequest = fmt.Sprintf(
				"Choose the next global review action after processing challenge %s.",
				strings.TrimSpace(entry.ChallengeID),
			)
		}
	case globalReviewEventReadyForCommit:
		var ready globalReviewReadyForCommitEvent
		if err := decodeProtocolPayload(event.Payload, &ready); err != nil {
			return err
		}
		s.requiredAction = GlobalReviewActionCommit
		s.requiredReason = "The tester-backed global review already passed; `commit_to_disk` is the only valid way to end this inspector turn."
		if s.snapshot == nil {
			s.snapshot = &GlobalReviewSnapshot{}
		}
		appendGlobalReviewEvent(s.snapshot, GlobalReviewEvent{
			Type:      "finalize_global_review",
			AgentType: GlobalReviewAgentInspector,
			Summary:   firstNonEmpty(strings.TrimSpace(ready.Summary), "Global review ready for commit."),
		})
	case globalReviewEventReadyCheckpoint:
		var ready globalReviewReadyForCommitEvent
		if err := decodeProtocolPayload(event.Payload, &ready); err != nil {
			return err
		}
		s.requiredAction = GlobalReviewActionAccept
		s.requiredReason = "The tester-backed checkpoint review already passed; `accept_checkpoint` is the only valid way to end this inspector turn."
		if s.snapshot == nil {
			s.snapshot = &GlobalReviewSnapshot{}
		}
		appendGlobalReviewEvent(s.snapshot, GlobalReviewEvent{
			Type:      "finalize_global_review",
			AgentType: GlobalReviewAgentInspector,
			Summary:   firstNonEmpty(strings.TrimSpace(ready.Summary), "Global review ready for checkpoint acceptance."),
		})
	case globalReviewEventAcceptCheckpoint:
		var action GlobalReviewTurnAction
		if err := decodeProtocolPayload(event.Payload, &action); err != nil {
			return err
		}
		s.requiredAction = ""
		s.requiredReason = ""
		if s.snapshot == nil {
			s.snapshot = &GlobalReviewSnapshot{}
		}
		s.snapshot.ActiveAgents = nil
		s.snapshot.CurrentRequest = ""
		s.snapshot.PendingChallenge = nil
		s.snapshot.PendingValidation = nil
		appendGlobalReviewEvent(s.snapshot, GlobalReviewEvent{
			Type:      string(GlobalReviewActionAccept),
			AgentType: GlobalReviewAgentInspector,
			Summary:   strings.TrimSpace(action.Summary),
		})
	case globalReviewEventCommitToDisk:
		var action GlobalReviewTurnAction
		if err := decodeProtocolPayload(event.Payload, &action); err != nil {
			return err
		}
		s.requiredAction = ""
		s.requiredReason = ""
		if s.snapshot == nil {
			s.snapshot = &GlobalReviewSnapshot{}
		}
		s.snapshot.ActiveAgents = nil
		s.snapshot.CurrentRequest = ""
		s.snapshot.PendingChallenge = nil
		s.snapshot.PendingValidation = nil
		appendGlobalReviewEvent(s.snapshot, GlobalReviewEvent{
			Type:      string(GlobalReviewActionCommit),
			AgentType: GlobalReviewAgentInspector,
			Summary:   strings.TrimSpace(action.Summary),
		})
	}
	_ = seq
	return nil
}

func (s *GlobalReviewState) applyValidationEvent(record *GlobalReviewValidationRecord) {
	if record == nil {
		return
	}
	if s.snapshot == nil {
		s.snapshot = &GlobalReviewSnapshot{}
	}
	s.snapshot.ActiveAgents = []string{normalizeGlobalReviewAgent(record.RequestingAgent)}
	s.snapshot.RequestedBy = normalizeGlobalReviewAgent(record.RespondingAgent)
	s.snapshot.CurrentRequest = fmt.Sprintf("Process validation response for challenge %s and decide the next handoff.", strings.TrimSpace(record.ChallengeID))
	s.snapshot.PendingChallenge = nil
	s.snapshot.PendingValidation = cloneGlobalReviewValidationRecord(record)
	appendGlobalReviewEvent(s.snapshot, GlobalReviewEvent{
		Type:        string(GlobalReviewActionValidate),
		AgentType:   normalizeGlobalReviewAgent(record.RespondingAgent),
		TargetAgent: normalizeGlobalReviewAgent(record.RequestingAgent),
		Summary:     strings.TrimSpace(record.Summary),
	})
}

func (s *GlobalReviewState) persistProjection() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.SaveSnapshot(s.store.journal.LastSequence(), globalReviewCheckpoint{
		Snapshot:       cloneGlobalReviewSnapshot(s.snapshot),
		Processed:      cloneGlobalReviewValidationProcessingList(s.processed),
		RequiredAction: strings.TrimSpace(string(s.requiredAction)),
		RequiredReason: strings.TrimSpace(s.requiredReason),
	})
}

func (s *GlobalReviewState) syncMailboxes() error {
	if s == nil || s.sessionDir == "" || s.scopeID == "" {
		return nil
	}
	desired := s.desiredMailboxItems()
	for _, agent := range globalReviewMailboxAgents(s.Snapshot()) {
		mailbox, err := openDurableAgentMailbox(s.sessionDir, agent)
		if err != nil {
			return err
		}
		if err := mailbox.Sync(globalReviewNamespace, s.scopeID, desired[agent]); err != nil {
			_ = mailbox.Close()
			return err
		}
		if err := mailbox.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *GlobalReviewState) desiredMailboxItems() map[string][]durableMailboxItem {
	desired := map[string][]durableMailboxItem{}
	snapshot := materializeGlobalReviewSnapshot(s)
	if snapshot == nil {
		return desired
	}
	if challenge := snapshot.PendingChallenge; challenge != nil {
		target := normalizeGlobalReviewAgent(challenge.TargetAgent)
		if target != "" {
			desired[target] = append(desired[target], durableMailboxItem{
				Key:      fmt.Sprintf("global_review:%s:%s:challenge:%s", s.scopeID, target, strings.TrimSpace(challenge.ID)),
				ItemKind: globalReviewMailboxItemKind,
				Action:   "validate_work",
				Summary:  strings.TrimSpace(challenge.Request),
				Payload:  mustMarshalRaw(cloneGlobalReviewChallenge(challenge)),
			})
		}
	}
	if validation := snapshot.PendingValidation; validation != nil {
		requesting := normalizeGlobalReviewAgent(validation.RequestingAgent)
		if requesting != "" {
			desired[requesting] = append(desired[requesting], durableMailboxItem{
				Key:      fmt.Sprintf("global_review:%s:%s:process:%s", s.scopeID, requesting, strings.TrimSpace(validation.ChallengeID)),
				ItemKind: globalReviewMailboxItemKind,
				Action:   "process_validation",
				Summary:  strings.TrimSpace(validation.Summary),
				Payload:  mustMarshalRaw(cloneGlobalReviewValidationRecord(validation)),
			})
		}
	}
	if strings.TrimSpace(string(s.requiredAction)) == string(GlobalReviewActionAccept) {
		desired[GlobalReviewAgentInspector] = append(desired[GlobalReviewAgentInspector], durableMailboxItem{
			Key:      fmt.Sprintf("global_review:%s:%s:required:%s", s.scopeID, GlobalReviewAgentInspector, GlobalReviewActionAccept),
			ItemKind: globalReviewMailboxItemKind,
			Action:   "accept_checkpoint",
			Summary:  strings.TrimSpace(s.requiredReason),
			Payload: mustMarshalRaw(map[string]any{
				"required_action": string(GlobalReviewActionAccept),
				"reason":          strings.TrimSpace(s.requiredReason),
			}),
		})
	} else if strings.TrimSpace(string(s.requiredAction)) == string(GlobalReviewActionCommit) {
		desired[GlobalReviewAgentInspector] = append(desired[GlobalReviewAgentInspector], durableMailboxItem{
			Key:      fmt.Sprintf("global_review:%s:%s:required:%s", s.scopeID, GlobalReviewAgentInspector, GlobalReviewActionCommit),
			ItemKind: globalReviewMailboxItemKind,
			Action:   "commit_to_disk",
			Summary:  strings.TrimSpace(s.requiredReason),
			Payload: mustMarshalRaw(map[string]any{
				"required_action": string(GlobalReviewActionCommit),
				"reason":          strings.TrimSpace(s.requiredReason),
			}),
		})
	} else if !globalReviewReviewClosed(snapshot) {
		if record, ok := finalizeGlobalReviewValidationReady(snapshot, s.processed); ok && snapshot.PendingValidation == nil {
			desired[GlobalReviewAgentInspector] = append(desired[GlobalReviewAgentInspector], durableMailboxItem{
				Key:      fmt.Sprintf("global_review:%s:%s:finalize:%s", s.scopeID, GlobalReviewAgentInspector, strings.TrimSpace(record.ChallengeID)),
				ItemKind: globalReviewMailboxItemKind,
				Action:   "finalize_global_review",
				Summary:  "A tester-backed global review is accepted; finalize the review now.",
				Payload:  mustMarshalRaw(cloneGlobalReviewValidationRecord(record)),
			})
		}
	}
	return desired
}

func globalReviewReviewClosed(snapshot *GlobalReviewSnapshot) bool {
	if snapshot == nil || len(snapshot.RecentEvents) == 0 {
		return false
	}
	switch strings.TrimSpace(snapshot.RecentEvents[len(snapshot.RecentEvents)-1].Type) {
	case string(GlobalReviewActionAccept), string(GlobalReviewActionCommit):
		return true
	default:
		return false
	}
}

func buildGlobalReviewSnapshotAfterChallenge(base *GlobalReviewSnapshot, action *GlobalReviewTurnAction) *GlobalReviewSnapshot {
	snapshot := cloneGlobalReviewSnapshot(base)
	if snapshot == nil {
		snapshot = &GlobalReviewSnapshot{}
	}
	snapshot.ActiveAgents = []string{normalizeGlobalReviewAgent(action.TargetAgent)}
	snapshot.RequestedBy = normalizeGlobalReviewAgent(action.AgentType)
	snapshot.CurrentRequest = strings.TrimSpace(action.Request)
	snapshot.PendingValidation = nil
	snapshot.PendingChallenge = nil
	if action.CreatesChallenge {
		snapshot.PendingChallenge = &GlobalReviewChallenge{
			ID:              strings.TrimSpace(action.ChallengeID),
			RequestingAgent: normalizeGlobalReviewAgent(action.AgentType),
			TargetAgent:     normalizeGlobalReviewAgent(action.TargetAgent),
			Reason:          strings.TrimSpace(action.Reason),
			Request:         strings.TrimSpace(action.Request),
			RequiredOutput:  append([]string(nil), action.RequiredOutput...),
			References:      append([]string(nil), action.References...),
		}
	}
	appendGlobalReviewEvent(snapshot, GlobalReviewEvent{
		Type:        string(action.Type),
		AgentType:   normalizeGlobalReviewAgent(action.AgentType),
		TargetAgent: normalizeGlobalReviewAgent(action.TargetAgent),
		Summary:     firstNonEmpty(strings.TrimSpace(action.Request), strings.TrimSpace(action.Summary)),
	})
	return snapshot
}

func globalReviewMailboxAgents(snapshot *GlobalReviewSnapshot) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(agent string) {
		agent = normalizeGlobalReviewAgent(agent)
		if agent == "" {
			return
		}
		if _, ok := seen[agent]; ok {
			return
		}
		seen[agent] = struct{}{}
		out = append(out, agent)
	}
	add(GlobalReviewAgentInspector)
	add(GlobalReviewAgentTester)
	add(GlobalReviewAgentArchitect)
	add(GlobalReviewAgentOrchestrator)
	if snapshot != nil {
		for _, active := range snapshot.ActiveAgents {
			add(active)
		}
		if snapshot.PendingChallenge != nil {
			add(snapshot.PendingChallenge.RequestingAgent)
			add(snapshot.PendingChallenge.TargetAgent)
		}
		if snapshot.PendingValidation != nil {
			add(snapshot.PendingValidation.RequestingAgent)
			add(snapshot.PendingValidation.RespondingAgent)
		}
	}
	return out
}

func cloneGlobalReviewValidationProcessingList(values []GlobalReviewValidationProcessing) []GlobalReviewValidationProcessing {
	if len(values) == 0 {
		return nil
	}
	out := make([]GlobalReviewValidationProcessing, len(values))
	for i, value := range values {
		out[i] = cloneGlobalReviewValidationProcessing(value)
	}
	return out
}

func globalReviewAgentTypeFromContext(ctx context.Context) string {
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		if value, _ := stream.Metadata["agent_type"].(string); strings.TrimSpace(value) != "" {
			return normalizeGlobalReviewAgent(value)
		}
	}
	return ""
}

func snapshotReviewID(snapshot *GlobalReviewSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strings.TrimSpace(snapshot.ReviewID)
}

func globalReviewChallengeID(record *GlobalReviewValidationRecord) string {
	if record == nil {
		return ""
	}
	return strings.TrimSpace(record.ChallengeID)
}
