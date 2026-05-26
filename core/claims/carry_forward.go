package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ArtifactKindWorkingContext   = "working_context"
	ArtifactKindEvidenceDigest   = "evidence_digest"
	ArtifactKindSourceIndex      = "source_index"
	ArtifactKindContinuityCursor = "continuity_cursor"
	ArtifactKindSessionCursor    = "session_cursor"
)

const (
	carryForwardScopeKind      = "continuity"
	carryForwardScopeKey       = "carry_forward"
	carryForwardTopicScopeKind = "continuity_topic"
	carryForwardAgentScopeKind = "continuity_agent"
)

type CarryForwardOptions struct {
	AgentID                       string
	Topic                         string
	PlanID                        string
	CorrelationID                 string
	PreviousSessionID             string
	PreviousBoardID               string
	PreviousContinuityTestamentID string
	MaxSources                    int
	Mode                          string
	SupersedePrior                bool
	FreshnessHorizon              time.Duration
	Now                           time.Time
}

type CarryForwardResult struct {
	AgentID               string          `json:"agent_id"`
	Topic                 string          `json:"topic"`
	SessionID             string          `json:"session_id"`
	BoardID               string          `json:"board_id"`
	Mode                  string          `json:"mode"`
	Mutated               bool            `json:"mutated"`
	NoopReason            string          `json:"noop_reason,omitempty"`
	ClaimID               string          `json:"claim_id,omitempty"`
	TestamentID           string          `json:"testament_id,omitempty"`
	PriorTestamentID      string          `json:"prior_testament_id,omitempty"`
	FromSequence          uint64          `json:"from_sequence"`
	ThroughSequence       uint64          `json:"through_sequence"`
	SourceCount           int             `json:"source_count"`
	Sources               []ForwardSource `json:"sources,omitempty"`
	WorkingContext        string          `json:"working_context,omitempty"`
	EvidenceDigest        string          `json:"evidence_digest,omitempty"`
	ContinuityClaimReused bool            `json:"continuity_claim_reused"`
}

type RecallForwardOptions struct {
	AgentID          string
	Topic            string
	LookbackSessions int
	MaxItems         int
	IncludeSources   string
	OpenBoard        SessionBoardOpener
}

type SessionBoardOpener func(ctx context.Context, sessionID string) (*ClaimsBoard, func(), error)

type RecallForwardResult struct {
	AgentID          string                 `json:"agent_id"`
	Topic            string                 `json:"topic"`
	LookbackSessions int                    `json:"lookback_sessions"`
	IncludeSources   string                 `json:"include_sources"`
	Partial          bool                   `json:"partial"`
	Diagnostics      []string               `json:"diagnostics,omitempty"`
	Items            []ContinuityRecallItem `json:"items"`
	WorkingContext   string                 `json:"working_context,omitempty"`
	EvidenceDigest   string                 `json:"evidence_digest,omitempty"`
	Sources          []ForwardSource        `json:"sources,omitempty"`
	FullTestaments   []*Testament           `json:"full_testaments,omitempty"`
	FullArtifacts    []*Artifact            `json:"full_artifacts,omitempty"`
}

type ContinuityRecallItem struct {
	SessionID       string          `json:"session_id"`
	BoardID         string          `json:"board_id"`
	ClaimID         string          `json:"claim_id,omitempty"`
	TestamentID     string          `json:"testament_id"`
	FromSequence    uint64          `json:"from_sequence"`
	ThroughSequence uint64          `json:"through_sequence"`
	WorkingContext  string          `json:"working_context,omitempty"`
	EvidenceDigest  string          `json:"evidence_digest,omitempty"`
	Sources         []ForwardSource `json:"sources,omitempty"`
	Cursor          map[string]any  `json:"cursor,omitempty"`
}

type ForwardSource struct {
	TestamentID string `json:"testament_id,omitempty"`
	ArtifactID  string `json:"artifact_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Sequence    uint64 `json:"sequence,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Digest      string `json:"digest,omitempty"`
}

type continuityRecord struct {
	Testament       *Testament
	ClaimID         string
	FromSequence    uint64
	ThroughSequence uint64
	WorkingContext  string
	EvidenceDigest  string
	Sources         []ForwardSource
	Cursor          map[string]any
	SessionCursor   map[string]any
}

type scoredForwardSource struct {
	source ForwardSource
	score  int
}

func CarryForward(ctx context.Context, board *ClaimsBoard, opts CarryForwardOptions) (*CarryForwardResult, error) {
	if board == nil {
		return nil, fmt.Errorf("claims board is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	topic := normalizeContinuityTopic(opts.Topic)
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	mode := strings.TrimSpace(strings.ToLower(opts.Mode))
	if mode == "" {
		mode = "advance"
	}
	if mode != "advance" && mode != "preview" {
		return nil, fmt.Errorf("invalid carry_forward mode %q", opts.Mode)
	}
	maxSources := opts.MaxSources
	if maxSources <= 0 {
		maxSources = 8
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	prior, hasPrior := latestContinuityRecord(board, agentID, topic)
	fromSeq := uint64(0)
	priorID := ""
	if hasPrior {
		fromSeq = prior.ThroughSequence
		priorID = prior.Testament.ID
	}
	throughSeq := board.HighWaterSequence()
	result := &CarryForwardResult{
		AgentID:          agentID,
		Topic:            topic,
		SessionID:        board.SessionID(),
		BoardID:          board.BoardID(),
		Mode:             mode,
		PriorTestamentID: priorID,
		FromSequence:     fromSeq,
		ThroughSequence:  throughSeq,
	}
	if throughSeq <= fromSeq {
		result.NoopReason = "cursor already covers current board high-water"
		return result, nil
	}

	sources := selectForwardSources(board, agentID, topic, fromSeq, throughSeq, maxSources, opts.FreshnessHorizon, now)
	result.Sources = make([]ForwardSource, len(sources))
	for i := range sources {
		result.Sources[i] = sources[i].source
	}
	result.SourceCount = len(result.Sources)
	if len(result.Sources) == 0 {
		result.NoopReason = "no durable testaments or artifacts selected for carry-forward"
		return result, nil
	}

	workingContext := buildWorkingContext(topic, result.Sources)
	evidenceDigest := buildEvidenceDigest(result.Sources)
	result.WorkingContext = workingContext
	result.EvidenceDigest = evidenceDigest
	if mode == "preview" {
		result.NoopReason = "preview only"
		return result, nil
	}

	claim, reused, err := ensureContinuityClaim(ctx, board, agentID, topic, opts.PlanID, opts.CorrelationID)
	if err != nil {
		return nil, err
	}
	result.ClaimID = claim.ID
	result.ContinuityClaimReused = reused

	t := Testament{
		AgentID:    agentID,
		Summary:    fmt.Sprintf("Carried forward %d durable source(s) for %q through board sequence %d.", len(result.Sources), topic, throughSeq),
		Confidence: "committed",
		Relations:  continuityRelations(claim.ID, priorID, result.Sources, opts.SupersedePrior),
		Artifacts:  continuityArtifacts(agentID, topic, board, fromSeq, throughSeq, opts, result.Sources, workingContext, evidenceDigest, priorID, now),
	}
	if err := board.SubmitTestaments(ctx, Action{AgentID: agentID, Type: ActionTypeTestament}, []Testament{t}); err != nil {
		return nil, err
	}
	result.Mutated = true
	if latest, ok := latestContinuityRecord(board, agentID, topic); ok {
		result.TestamentID = latest.Testament.ID
	}
	return result, nil
}

func RecallForward(ctx context.Context, board *ClaimsBoard, opts RecallForwardOptions) (*RecallForwardResult, error) {
	if board == nil {
		return nil, fmt.Errorf("claims board is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	topic := normalizeContinuityTopic(opts.Topic)
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	include := strings.TrimSpace(strings.ToLower(opts.IncludeSources))
	if include == "" {
		include = "digest"
	}
	if include != "digest" && include != "source_index" && include != "full" {
		return nil, fmt.Errorf("invalid include_sources %q", opts.IncludeSources)
	}
	maxItems := opts.MaxItems
	if maxItems <= 0 {
		maxItems = 8
	}
	lookback := opts.LookbackSessions
	if lookback < 0 {
		lookback = 0
	}
	result := &RecallForwardResult{
		AgentID:          agentID,
		Topic:            topic,
		LookbackSessions: lookback,
		IncludeSources:   include,
	}
	current := board
	remaining := lookback
	for {
		rec, ok := latestContinuityRecord(current, agentID, topic)
		if !ok {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("no continuity testament for agent=%s topic=%q session=%s", agentID, topic, current.SessionID()))
			break
		}
		item := continuityRecallItem(current, rec, include)
		result.Items = append(result.Items, item)
		result.WorkingContext = appendContextBlock(result.WorkingContext, item.WorkingContext)
		result.EvidenceDigest = appendContextBlock(result.EvidenceDigest, item.EvidenceDigest)
		if include == "source_index" || include == "full" {
			result.Sources = appendBoundedSources(result.Sources, item.Sources, maxItems)
		}
		if include == "full" {
			appendFullSources(current, item.Sources, &result.FullTestaments, &result.FullArtifacts, maxItems)
		}
		if len(result.Items) >= maxItems || remaining <= 0 {
			break
		}
		nextSession := metadataString(rec.SessionCursor, "previous_session_id")
		if nextSession == "" {
			break
		}
		if opts.OpenBoard == nil {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("previous session %s referenced but no durable board opener is configured", nextSession))
			break
		}
		nextBoard, closeFn, err := opts.OpenBoard(ctx, nextSession)
		if err != nil {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("open previous session %s: %v", nextSession, err))
			break
		}
		if closeFn != nil {
			defer closeFn()
		}
		if nextBoard == nil {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("previous session %s opened nil board", nextSession))
			break
		}
		current = nextBoard
		remaining--
	}
	return result, nil
}

func latestContinuityRecord(board *ClaimsBoard, agentID, topic string) (continuityRecord, bool) {
	p := board.Projection()
	superseded := make(map[string]struct{})
	records := make([]continuityRecord, 0)
	for i := range p.Testaments {
		t, ok := board.CloneTestament(p.Testaments[i].ID)
		if !ok || t == nil {
			continue
		}
		for _, rel := range t.Relations {
			if rel.RelatedType == RelatedTypeTestament && rel.Relationship == RelationshipSupersedes {
				superseded[rel.Related] = struct{}{}
			}
		}
		rec, ok := continuityFromTestament(t, agentID, topic)
		if ok {
			records = append(records, rec)
		}
	}
	if len(records) == 0 {
		return continuityRecord{}, false
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Testament.Sequence > records[j].Testament.Sequence
	})
	for _, rec := range records {
		if _, ok := superseded[rec.Testament.ID]; ok {
			continue
		}
		return rec, true
	}
	return records[0], true
}

func continuityFromTestament(t *Testament, agentID, topic string) (continuityRecord, bool) {
	if t == nil {
		return continuityRecord{}, false
	}
	if strings.TrimSpace(agentID) != "" && strings.TrimSpace(t.AgentID) != strings.TrimSpace(agentID) {
		return continuityRecord{}, false
	}
	var rec continuityRecord
	rec.Testament = t
	rec.ClaimID = ClaimIDFromRelations(t.Relations)
	for _, artifact := range t.Artifacts {
		if artifact == nil {
			continue
		}
		switch strings.TrimSpace(artifact.Kind) {
		case ArtifactKindContinuityCursor:
			if normalizeContinuityTopic(metadataString(artifact.Metadata, "topic")) != topic {
				return continuityRecord{}, false
			}
			if strings.TrimSpace(metadataString(artifact.Metadata, "agent_id")) != strings.TrimSpace(agentID) {
				return continuityRecord{}, false
			}
			rec.FromSequence = metadataUint64(artifact.Metadata, "from_sequence")
			rec.ThroughSequence = metadataUint64(artifact.Metadata, "through_sequence")
			rec.Cursor = cloneAnyMap(artifact.Metadata)
		case ArtifactKindWorkingContext:
			rec.WorkingContext = artifact.Reference
		case ArtifactKindEvidenceDigest:
			rec.EvidenceDigest = artifact.Reference
		case ArtifactKindSourceIndex:
			rec.Sources = parseForwardSources(artifact.Metadata["sources"])
		case ArtifactKindSessionCursor:
			rec.SessionCursor = cloneAnyMap(artifact.Metadata)
		}
	}
	if rec.Cursor == nil {
		return continuityRecord{}, false
	}
	return rec, true
}

func ensureContinuityClaim(ctx context.Context, board *ClaimsBoard, agentID, topic, planID, correlationID string) (*Claim, bool, error) {
	p := board.Projection()
	for i := range p.Claims {
		c := p.Claims[i]
		if c.ActionType != ActionTypeArchival || c.Status == ClaimStatusSuperseded || c.Status == ClaimStatusRejected {
			continue
		}
		if !claimHasScope(c.Scope, carryForwardScopeKind, carryForwardScopeKey) ||
			!claimHasScope(c.Scope, carryForwardTopicScopeKind, topic) ||
			!claimHasScope(c.Scope, carryForwardAgentScopeKind, agentID) {
			continue
		}
		clone, ok := board.CloneClaim(c.ID)
		if ok {
			return clone, true, nil
		}
	}
	claim := Claim{
		Title:       fmt.Sprintf("Carry forward continuity evidence for %s", topic),
		Description: "Record a continuity testament that carries durable testaments and artifacts forward for the same agent and topic.",
		Scope: []ClaimScopeEntry{
			{Kind: carryForwardScopeKind, Key: carryForwardScopeKey},
			{Kind: carryForwardTopicScopeKind, Key: topic},
			{Kind: carryForwardAgentScopeKind, Key: agentID},
		},
		Relations: []Relation{
			{Related: agentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: agentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			Description: "Continuity testament submitted",
			QualityBar:  "a continuity testament with working_context, evidence_digest, source_index, continuity_cursor, and session_cursor artifacts exists",
			Type:        ValidationTypeReceipt,
			Required:    true,
		}},
	}
	if strings.TrimSpace(planID) != "" {
		claim.Scope = append(claim.Scope, ClaimScopeEntry{Kind: "plan", Key: strings.TrimSpace(planID)})
	}
	if strings.TrimSpace(correlationID) != "" {
		claim.Scope = append(claim.Scope, ClaimScopeEntry{Kind: "correlation", Key: strings.TrimSpace(correlationID)})
	}
	action := Action{AgentID: agentID, Type: ActionTypeArchival}
	if err := board.PostAction(ctx, action, []Claim{claim}); err != nil {
		return nil, false, err
	}
	for _, id := range board.ClaimIDsWithScope(carryForwardTopicScopeKind, topic) {
		c, ok := board.CloneClaim(id)
		if ok && claimHasScope(c.Scope, carryForwardAgentScopeKind, agentID) && claimHasScope(c.Scope, carryForwardScopeKind, carryForwardScopeKey) {
			return c, false, nil
		}
	}
	return nil, false, fmt.Errorf("continuity claim for agent=%s topic=%q was not found after post", agentID, topic)
}

func selectForwardSources(board *ClaimsBoard, agentID, topic string, fromSeq, throughSeq uint64, maxSources int, horizon time.Duration, now time.Time) []scoredForwardSource {
	p := board.Projection()
	out := make([]scoredForwardSource, 0, maxSources)
	seen := make(map[string]struct{})
	for i := range p.Testaments {
		t, ok := board.CloneTestament(p.Testaments[i].ID)
		if !ok || t == nil || t.Sequence <= fromSeq || t.Sequence > throughSeq {
			continue
		}
		if horizon > 0 && !t.Created.IsZero() && now.Sub(t.Created) > horizon {
			continue
		}
		if isContinuityTestament(t) || isProjectionDiagnosticTestament(t) {
			continue
		}
		for _, artifact := range t.Artifacts {
			if artifact == nil || artifact.Sequence <= fromSeq || artifact.Sequence > throughSeq {
				continue
			}
			if horizon > 0 && !artifact.Created.IsZero() && now.Sub(artifact.Created) > horizon {
				continue
			}
			score, reason := scoreForwardArtifact(t, artifact, topic)
			if score <= 0 {
				continue
			}
			key := strings.TrimSpace(artifact.Kind) + "\x00" + strings.TrimSpace(artifact.Reference)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, scoredForwardSource{
				score: score,
				source: ForwardSource{
					TestamentID: t.ID,
					ArtifactID:  artifact.ID,
					AgentID:     firstNonEmpty(artifact.AgentID, t.AgentID, agentID),
					Kind:        artifact.Kind,
					Sequence:    artifact.Sequence,
					Reason:      reason,
					Digest:      sourceDigest(t, artifact),
				},
			})
		}
		if len(t.Artifacts) == 0 {
			score, reason := scoreForwardTestament(t, topic)
			if score <= 0 {
				continue
			}
			key := "testament\x00" + strings.TrimSpace(t.Summary)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, scoredForwardSource{
				score: score,
				source: ForwardSource{
					TestamentID: t.ID,
					AgentID:     firstNonEmpty(t.AgentID, agentID),
					Kind:        "testament",
					Sequence:    t.Sequence,
					Reason:      reason,
					Digest:      truncateForDigest(t.Summary, 240),
				},
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].source.Sequence != out[j].source.Sequence {
			return out[i].source.Sequence < out[j].source.Sequence
		}
		return out[i].source.ArtifactID < out[j].source.ArtifactID
	})
	if len(out) > maxSources {
		out = out[:maxSources]
	}
	return out
}

func scoreForwardArtifact(t *Testament, a *Artifact, topic string) (int, string) {
	kind := strings.TrimSpace(strings.ToLower(a.Kind))
	ref := strings.TrimSpace(a.Reference)
	if ref == "" && len(a.Metadata) == 0 {
		return 0, ""
	}
	if isContinuityArtifactKind(kind) {
		return 0, ""
	}
	if isNoiseArtifactKind(kind) {
		return 0, ""
	}
	if a.Ephemeral && !isCarryForwardErrorArtifactKind(kind) {
		return 0, ""
	}
	score := 1
	reason := "durable artifact"
	switch {
	case isCarryForwardErrorArtifactKind(kind):
		score = 95
		reason = "error/blocker evidence"
	case kind == ArtifactKindResponseText || kind == "consult_response" || kind == "challenge_response":
		score = 90
		reason = "peer answer/testament response"
	case strings.Contains(kind, "test") || strings.Contains(kind, "verification"):
		score = 86
		reason = "test or verification evidence"
	case strings.Contains(kind, "workspace") || strings.Contains(kind, "code") || strings.Contains(kind, "diff") || strings.Contains(kind, "file"):
		score = 82
		reason = "workspace/code discovery evidence"
	case strings.Contains(kind, "decision") || strings.Contains(kind, "plan") || strings.Contains(kind, "design"):
		score = 78
		reason = "decision or design evidence"
	case strings.Contains(kind, "research") || strings.Contains(kind, "source") || strings.Contains(kind, "citation"):
		score = 74
		reason = "research evidence"
	}
	if matchesTopic(topic, ref) || matchesTopic(topic, t.Summary) {
		score += 8
		reason += " matching topic"
	}
	return score, reason
}

func scoreForwardTestament(t *Testament, topic string) (int, string) {
	summary := strings.TrimSpace(t.Summary)
	if summary == "" {
		return 0, ""
	}
	score := 35
	reason := "testament summary"
	if matchesTopic(topic, summary) {
		score += 10
		reason += " matching topic"
	}
	return score, reason
}

func continuityRelations(claimID, priorID string, sources []ForwardSource, supersede bool) []Relation {
	relations := []Relation{
		{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
	}
	if strings.TrimSpace(priorID) != "" {
		relationship := RelationshipAmends
		if supersede {
			relationship = RelationshipSupersedes
		}
		relations = append(relations, Relation{Related: priorID, RelatedType: RelatedTypeTestament, Relationship: relationship})
	}
	for _, source := range sources {
		if source.TestamentID != "" {
			relations = append(relations, Relation{
				Related:      source.TestamentID,
				RelatedType:  RelatedTypeTestament,
				Relationship: RelationshipDerivedFrom,
			})
		}
		if source.ArtifactID != "" {
			relations = append(relations, Relation{
				Related:      source.ArtifactID,
				RelatedType:  RelatedTypeArtifact,
				Relationship: RelationshipDerivedFrom,
			})
		}
	}
	return dedupeRelations(relations)
}

func continuityArtifacts(agentID, topic string, board *ClaimsBoard, fromSeq, throughSeq uint64, opts CarryForwardOptions, sources []ForwardSource, workingContext, evidenceDigest, priorID string, now time.Time) []*Artifact {
	sourceIDs := make([]string, 0, len(sources))
	artifactIDs := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.TestamentID != "" {
			sourceIDs = append(sourceIDs, source.TestamentID)
		}
		if source.ArtifactID != "" {
			artifactIDs = append(artifactIDs, source.ArtifactID)
		}
	}
	base := map[string]any{
		"agent_id":       agentID,
		"topic":          topic,
		"session_id":     board.SessionID(),
		"board_id":       board.BoardID(),
		"plan_id":        strings.TrimSpace(opts.PlanID),
		"correlation_id": strings.TrimSpace(opts.CorrelationID),
		"created_at":     now.Format(time.RFC3339Nano),
	}
	cursor := cloneAnyMap(base)
	cursor["from_sequence"] = fromSeq
	cursor["through_sequence"] = throughSeq
	cursor["source_testament_ids"] = sourceIDs
	cursor["source_artifact_ids"] = artifactIDs
	if priorID != "" {
		cursor["previous_continuity_testament_id"] = priorID
	}
	sessionCursor := cloneAnyMap(base)
	sessionCursor["through_sequence"] = throughSeq
	sessionCursor["continuity_topic"] = topic
	if prev := strings.TrimSpace(opts.PreviousSessionID); prev != "" {
		sessionCursor["previous_session_id"] = prev
	}
	if prev := strings.TrimSpace(opts.PreviousBoardID); prev != "" {
		sessionCursor["previous_board_id"] = prev
	}
	if prev := strings.TrimSpace(opts.PreviousContinuityTestamentID); prev != "" {
		sessionCursor["previous_continuity_testament_id"] = prev
	} else if priorID != "" {
		sessionCursor["previous_continuity_testament_id"] = priorID
	}
	return []*Artifact{
		{Kind: ArtifactKindWorkingContext, Reference: workingContext, Metadata: cloneAnyMap(base)},
		{Kind: ArtifactKindEvidenceDigest, Reference: evidenceDigest, Metadata: map[string]any{"findings": sourceDigests(sources), "topic": topic, "agent_id": agentID}},
		{Kind: ArtifactKindSourceIndex, Reference: fmt.Sprintf("%d source(s)", len(sources)), Metadata: map[string]any{"sources": sources, "topic": topic, "agent_id": agentID}},
		{Kind: ArtifactKindContinuityCursor, Reference: fmt.Sprintf("%d..%d", fromSeq, throughSeq), Metadata: cursor},
		{Kind: ArtifactKindSessionCursor, Reference: fmt.Sprintf("session=%s board=%s through=%d", board.SessionID(), board.BoardID(), throughSeq), Metadata: sessionCursor},
	}
}

func continuityRecallItem(board *ClaimsBoard, rec continuityRecord, include string) ContinuityRecallItem {
	item := ContinuityRecallItem{
		SessionID:       board.SessionID(),
		BoardID:         board.BoardID(),
		ClaimID:         rec.ClaimID,
		TestamentID:     rec.Testament.ID,
		FromSequence:    rec.FromSequence,
		ThroughSequence: rec.ThroughSequence,
		WorkingContext:  rec.WorkingContext,
		EvidenceDigest:  rec.EvidenceDigest,
		Cursor:          cloneAnyMap(rec.Cursor),
	}
	if include == "source_index" || include == "full" {
		item.Sources = rec.Sources
	}
	return item
}

func appendFullSources(board *ClaimsBoard, sources []ForwardSource, testaments *[]*Testament, artifacts *[]*Artifact, maxItems int) {
	seenT := make(map[string]struct{})
	seenA := make(map[string]struct{})
	for _, source := range sources {
		if maxItems > 0 && len(*testaments)+len(*artifacts) >= maxItems {
			return
		}
		if source.TestamentID != "" {
			if _, ok := seenT[source.TestamentID]; !ok {
				if t, found := board.CloneTestament(source.TestamentID); found {
					*testaments = append(*testaments, t)
					seenT[source.TestamentID] = struct{}{}
				}
			}
		}
		if source.ArtifactID != "" {
			if _, ok := seenA[source.ArtifactID]; !ok {
				if a, found := board.CloneArtifact(source.ArtifactID); found {
					*artifacts = append(*artifacts, a)
					seenA[source.ArtifactID] = struct{}{}
				}
			}
		}
	}
}

func isContinuityTestament(t *Testament) bool {
	for _, artifact := range t.Artifacts {
		if artifact != nil && artifact.Kind == ArtifactKindContinuityCursor {
			return true
		}
	}
	return false
}

func isProjectionDiagnosticTestament(t *Testament) bool {
	for _, artifact := range t.Artifacts {
		if artifact == nil {
			continue
		}
		if artifact.Kind == ArtifactKindProjectionError || artifact.Kind == ArtifactKindProjectionReceipt {
			return true
		}
	}
	return false
}

func isContinuityArtifactKind(kind string) bool {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case ArtifactKindWorkingContext, ArtifactKindEvidenceDigest, ArtifactKindSourceIndex, ArtifactKindContinuityCursor, ArtifactKindSessionCursor:
		return true
	default:
		return false
	}
}

func isNoiseArtifactKind(kind string) bool {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "", ArtifactKindTiming, ArtifactKindStats, ArtifactKindReadiness, ArtifactKindAgentID, ArtifactKindShutdownAck, ArtifactKindStateHash, ArtifactKindAgentState, ArtifactKindProjectionReceipt:
		return true
	default:
		return strings.Contains(kind, "spinner") || strings.Contains(kind, "progress") || strings.Contains(kind, "heartbeat")
	}
}

func isCarryForwardErrorArtifactKind(kind string) bool {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case ArtifactKindError, ArtifactKindErrorTrace, ArtifactKindErrorDiagnostic, ArtifactKindProjectionError:
		return true
	default:
		return strings.Contains(kind, "error") || strings.Contains(kind, "failure") || strings.Contains(kind, "blocker")
	}
}

func sourceDigest(t *Testament, a *Artifact) string {
	ref := strings.TrimSpace(a.Reference)
	if ref == "" {
		if payload, err := json.Marshal(a.Metadata); err == nil {
			ref = string(payload)
		}
	}
	if ref == "" {
		ref = strings.TrimSpace(t.Summary)
	}
	return truncateForDigest(ref, 240)
}

func buildWorkingContext(topic string, sources []ForwardSource) string {
	var b strings.Builder
	b.WriteString("Topic: ")
	b.WriteString(topic)
	b.WriteString("\n")
	for i, source := range sources {
		b.WriteString("- ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(source.Digest)
		if source.Reason != "" {
			b.WriteString(" (")
			b.WriteString(source.Reason)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func buildEvidenceDigest(sources []ForwardSource) string {
	var b strings.Builder
	for i, source := range sources {
		b.WriteString("- finding ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(": ")
		b.WriteString(source.Digest)
		if source.ArtifactID != "" || source.TestamentID != "" {
			b.WriteString(" [")
			if source.TestamentID != "" {
				b.WriteString("testament:")
				b.WriteString(source.TestamentID)
			}
			if source.ArtifactID != "" {
				if source.TestamentID != "" {
					b.WriteString(" ")
				}
				b.WriteString("artifact:")
				b.WriteString(source.ArtifactID)
			}
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func sourceDigests(sources []ForwardSource) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.Digest)
	}
	return out
}

func parseForwardSources(raw any) []ForwardSource {
	if raw == nil {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []ForwardSource
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil
	}
	return out
}

func metadataString(md map[string]any, key string) string {
	if len(md) == 0 {
		return ""
	}
	switch v := md[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func metadataUint64(md map[string]any, key string) uint64 {
	if len(md) == 0 {
		return 0
	}
	switch v := md[key].(type) {
	case uint64:
		return v
	case uint:
		return uint64(v)
	case int:
		if v < 0 {
			return 0
		}
		return uint64(v)
	case int64:
		if v < 0 {
			return 0
		}
		return uint64(v)
	case float64:
		if v < 0 {
			return 0
		}
		return uint64(v)
	case json.Number:
		n, _ := strconv.ParseUint(string(v), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func normalizeContinuityTopic(topic string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(topic))), " ")
}

func matchesTopic(topic, text string) bool {
	topic = normalizeContinuityTopic(topic)
	text = normalizeContinuityTopic(text)
	if topic == "" || text == "" {
		return false
	}
	if strings.Contains(text, topic) {
		return true
	}
	parts := strings.Fields(topic)
	if len(parts) == 0 {
		return false
	}
	matches := 0
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		if strings.Contains(text, part) {
			matches++
		}
	}
	return matches >= 2 || (len(parts) == 1 && matches == 1)
}

func truncateForDigest(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-1]) + "..."
}

func claimHasScope(scope []ClaimScopeEntry, kind, key string) bool {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	for _, entry := range scope {
		if strings.TrimSpace(entry.Kind) == kind && strings.TrimSpace(entry.Key) == key {
			return true
		}
	}
	return false
}

func dedupeRelations(in []Relation) []Relation {
	out := make([]Relation, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		key := r.RelatedType + "\x00" + r.Relationship + "\x00" + r.Related
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out
}

func appendContextBlock(existing, block string) string {
	block = strings.TrimSpace(block)
	if block == "" {
		return existing
	}
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return block
	}
	return existing + "\n\n" + block
}

func appendBoundedSources(dst, src []ForwardSource, max int) []ForwardSource {
	for _, source := range src {
		if max > 0 && len(dst) >= max {
			break
		}
		dst = append(dst, source)
	}
	return dst
}
