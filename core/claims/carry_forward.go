package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
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

const (
	RecallForwardStatusUsable       = "usable"
	RecallForwardStatusMiss         = "miss"
	RecallForwardStatusInsufficient = "insufficient"
	RecallForwardStatusPartial      = "partial"
	RecallForwardStatusStale        = "stale"
	RecallForwardStatusContradicted = "contradicted"
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
	AgentID            string
	Topic              string
	LookbackSessions   int
	MaxItems           int
	IncludeSources     string
	OpenBoard          SessionBoardOpener
	EnrichmentProvider RecallForwardEnrichmentProvider
}

type SessionBoardOpener func(ctx context.Context, sessionID string) (*ClaimsBoard, func(), error)

type RecallForwardEnrichmentProvider interface {
	LookupCarryForwardEnrichment(ctx context.Context, query RecallForwardEnrichmentQuery) ([]RecallForwardEnrichment, error)
}

var defaultRecallForwardEnrichment struct {
	mu       sync.RWMutex
	provider RecallForwardEnrichmentProvider
}

func SetDefaultRecallForwardEnrichmentProvider(provider RecallForwardEnrichmentProvider) {
	defaultRecallForwardEnrichment.mu.Lock()
	defaultRecallForwardEnrichment.provider = provider
	defaultRecallForwardEnrichment.mu.Unlock()
}

func DefaultRecallForwardEnrichmentProvider() RecallForwardEnrichmentProvider {
	defaultRecallForwardEnrichment.mu.RLock()
	provider := defaultRecallForwardEnrichment.provider
	defaultRecallForwardEnrichment.mu.RUnlock()
	return provider
}

type RecallForwardEnrichmentQuery struct {
	AgentID              string `json:"agent_id"`
	Topic                string `json:"topic"`
	SessionID            string `json:"session_id,omitempty"`
	BoardID              string `json:"board_id,omitempty"`
	ClaimID              string `json:"claim_id,omitempty"`
	TestamentID          string `json:"testament_id,omitempty"`
	EntityType           string `json:"entity_type,omitempty"`
	RelationType         string `json:"relation_type,omitempty"`
	RelationRelationship string `json:"relation_relationship,omitempty"`
	RelatedID            string `json:"related_id,omitempty"`
	MaxItems             int    `json:"max_items,omitempty"`
	IncludeSource        string `json:"include_source,omitempty"`
}

type RecallForwardEnrichment struct {
	Source             string  `json:"source"`
	DocumentID         string  `json:"document_id,omitempty"`
	Path               string  `json:"path,omitempty"`
	EntityType         string  `json:"entity_type,omitempty"`
	EntityID           string  `json:"entity_id,omitempty"`
	ClaimID            string  `json:"claim_id,omitempty"`
	TestamentID        string  `json:"testament_id,omitempty"`
	ArtifactID         string  `json:"artifact_id,omitempty"`
	SessionID          string  `json:"session_id,omitempty"`
	BoardID            string  `json:"board_id,omitempty"`
	AgentID            string  `json:"agent_id,omitempty"`
	Topic              string  `json:"topic,omitempty"`
	Score              float64 `json:"score,omitempty"`
	Summary            string  `json:"summary,omitempty"`
	EnrichedBy         string  `json:"enriched_by,omitempty"`
	GraphNodeID        string  `json:"graph_node_id,omitempty"`
	GraphNodeType      string  `json:"graph_node_type,omitempty"`
	AgenticNarrative   bool    `json:"agentic_narrative,omitempty"`
	ArchivalistEntryID string  `json:"archivalist_entry_id,omitempty"`
	Legacy             bool    `json:"legacy,omitempty"`
	ExactClaimSources  bool    `json:"exact_claim_sources,omitempty"`
}

type RecallForwardResult struct {
	AgentID               string                    `json:"agent_id"`
	Topic                 string                    `json:"topic"`
	LookbackSessions      int                       `json:"lookback_sessions"`
	IncludeSources        string                    `json:"include_sources"`
	Status                string                    `json:"status"`
	Usable                bool                      `json:"usable"`
	Reason                string                    `json:"reason,omitempty"`
	RecommendedNextAction string                    `json:"recommended_next_action,omitempty"`
	SourceIndexCount      int                       `json:"source_index_count"`
	HydrationAvailable    bool                      `json:"hydration_available"`
	Partial               bool                      `json:"partial"`
	Stale                 bool                      `json:"stale,omitempty"`
	Contradicted          bool                      `json:"contradicted,omitempty"`
	Diagnostics           []string                  `json:"diagnostics,omitempty"`
	Items                 []ContinuityRecallItem    `json:"items"`
	WorkingContext        string                    `json:"working_context,omitempty"`
	EvidenceDigest        string                    `json:"evidence_digest,omitempty"`
	Sources               []ForwardSource           `json:"sources,omitempty"`
	FullTestaments        []*Testament              `json:"full_testaments,omitempty"`
	FullArtifacts         []*Artifact               `json:"full_artifacts,omitempty"`
	Enrichment            []RecallForwardEnrichment `json:"enrichment,omitempty"`
}

type ContinuityRecallItem struct {
	SessionID            string          `json:"session_id"`
	BoardID              string          `json:"board_id"`
	ClaimID              string          `json:"claim_id,omitempty"`
	TestamentID          string          `json:"testament_id"`
	FromSequence         uint64          `json:"from_sequence"`
	ThroughSequence      uint64          `json:"through_sequence"`
	Stale                bool            `json:"stale,omitempty"`
	Contradicted         bool            `json:"contradicted,omitempty"`
	SourceIndexCount     int             `json:"source_index_count"`
	SourceIndexReturned  bool            `json:"source_index_returned"`
	HydrationAvailable   bool            `json:"hydration_available"`
	WorkingContext       string          `json:"working_context,omitempty"`
	EvidenceDigest       string          `json:"evidence_digest,omitempty"`
	Sources              []ForwardSource `json:"sources,omitempty"`
	Cursor               map[string]any  `json:"cursor,omitempty"`
	Reconstructed        bool            `json:"reconstructed,omitempty"`
	ReconstructionSource string          `json:"reconstruction_source,omitempty"`
	ExactClaimSources    bool            `json:"exact_claim_sources,omitempty"`
}

type ForwardSource struct {
	TestamentID           string   `json:"testament_id,omitempty"`
	ArtifactID            string   `json:"artifact_id,omitempty"`
	AgentID               string   `json:"agent_id,omitempty"`
	Kind                  string   `json:"kind,omitempty"`
	Sequence              uint64   `json:"sequence,omitempty"`
	Reason                string   `json:"reason,omitempty"`
	SelectedBy            string   `json:"selected_by,omitempty"`
	Score                 int      `json:"score,omitempty"`
	Digest                string   `json:"digest,omitempty"`
	ProjectionDocumentIDs []string `json:"projection_document_ids,omitempty"`
}

type ContinuityDetails struct {
	AgentID         string          `json:"agent_id"`
	Topic           string          `json:"topic"`
	SessionID       string          `json:"session_id"`
	BoardID         string          `json:"board_id"`
	ClaimID         string          `json:"claim_id,omitempty"`
	TestamentID     string          `json:"testament_id"`
	FromSequence    uint64          `json:"from_sequence"`
	ThroughSequence uint64          `json:"through_sequence"`
	WorkingContext  string          `json:"working_context,omitempty"`
	EvidenceDigest  string          `json:"evidence_digest,omitempty"`
	Sources         []ForwardSource `json:"sources,omitempty"`
	Stale           bool            `json:"stale,omitempty"`
	Contradicted    bool            `json:"contradicted,omitempty"`
	Cursor          map[string]any  `json:"cursor,omitempty"`
	SessionCursor   map[string]any  `json:"session_cursor,omitempty"`
}

type continuityRecord struct {
	Testament       *Testament
	ClaimID         string
	FromSequence    uint64
	ThroughSequence uint64
	WorkingContext  string
	EvidenceDigest  string
	Sources         []ForwardSource
	Stale           bool
	Contradicted    bool
	Cursor          map[string]any
	SessionCursor   map[string]any
}

func IsContinuityTestament(t *Testament) bool {
	return isContinuityTestament(t)
}

func ContinuityDetailsForTestament(t *Testament) (*ContinuityDetails, bool) {
	if t == nil {
		return nil, false
	}
	for _, artifact := range t.Artifacts {
		if artifact == nil || artifact.Kind != ArtifactKindContinuityCursor {
			continue
		}
		agentID := strings.TrimSpace(metadataString(artifact.Metadata, "agent_id"))
		topic := normalizeContinuityTopic(metadataString(artifact.Metadata, "topic"))
		if agentID == "" || topic == "" {
			return nil, false
		}
		rec, ok := continuityFromTestament(t, agentID, topic)
		if !ok {
			return nil, false
		}
		return &ContinuityDetails{
			AgentID:         agentID,
			Topic:           topic,
			SessionID:       t.SessionID,
			BoardID:         metadataString(rec.Cursor, "board_id"),
			ClaimID:         rec.ClaimID,
			TestamentID:     t.ID,
			FromSequence:    rec.FromSequence,
			ThroughSequence: rec.ThroughSequence,
			WorkingContext:  rec.WorkingContext,
			EvidenceDigest:  rec.EvidenceDigest,
			Sources:         rec.Sources,
			Stale:           rec.Stale,
			Contradicted:    rec.Contradicted,
			Cursor:          cloneAnyMap(rec.Cursor),
			SessionCursor:   cloneAnyMap(rec.SessionCursor),
		}, true
	}
	return nil, false
}

type scoredForwardSource struct {
	source ForwardSource
	score  int
}

var carryForwardLocks sync.Map

func carryForwardLock(board *ClaimsBoard, agentID, topic string) *sync.Mutex {
	key := strings.Join([]string{
		board.BoardID(),
		board.SessionID(),
		strings.TrimSpace(agentID),
		normalizeContinuityTopic(topic),
	}, "\x1f")
	actual, _ := carryForwardLocks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
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

	lock := carryForwardLock(board, agentID, topic)
	lock.Lock()
	defer lock.Unlock()

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

	sources := selectForwardSources(board, agentID, topic, fromSeq, throughSeq, maxSources, opts.FreshnessHorizon, now, incorporatedSourcesForRecord(board, prior, agentID, topic))
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
	if latest, ok := latestContinuityRecord(board, agentID, topic); ok && latest.ThroughSequence >= throughSeq && latest.Testament.ID != priorID {
		result.Mutated = false
		result.NoopReason = "continuity cursor already advanced by another carry_forward call"
		result.TestamentID = latest.Testament.ID
		result.PriorTestamentID = priorID
		return result, nil
	}

	t := Testament{
		AgentID:    agentID,
		Summary:    fmt.Sprintf("Carried forward %d durable source(s) for %q through board sequence %d.", len(result.Sources), topic, throughSeq),
		Confidence: "committed",
		Relations:  continuityRelations(claim.ID, priorID, result.Sources, opts.SupersedePrior),
		Artifacts:  continuityArtifacts(agentID, topic, board, fromSeq, throughSeq, opts, result.Sources, workingContext, evidenceDigest, priorID, now),
	}
	generated, err := board.GenerateTestamentAction(ctx, Action{AgentID: agentID, Type: ActionTypeTestament}, []Testament{t}, GenerateTestamentActionOptions{
		IdempotencyKey: fmt.Sprintf("carry_forward:%s:%s:%d", agentID, topic, throughSeq),
		Reason:         "continuity testament generated",
	})
	if err != nil {
		return nil, err
	}
	if len(generated.Testaments) == 0 {
		return nil, fmt.Errorf("continuity testament generation returned no testaments")
	}
	if err := board.PostGeneratedTestament(ctx, generated.Testaments[0].ID, agentID, TestamentPostOptions{Reason: "continuity testament posted"}); err != nil {
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
	appendRolloutRecallDiagnostics(board, result)
	appendProjectionDiagnostics(board, result)
	current := board
	remaining := lookback
	rec, ok := latestContinuityRecord(current, agentID, topic)
	if !ok {
		appendRecallEnrichment(ctx, board, opts.EnrichmentProvider, result, maxItems, RecallForwardEnrichmentQuery{
			AgentID:   result.AgentID,
			Topic:     result.Topic,
			SessionID: board.SessionID(),
			BoardID:   board.BoardID(),
		})
		synthesizeLegacyContinuity(board, result)
		return finalizeRecallForwardResult(result), nil
	}
	for {
		chain := appendContinuityChain(ctx, current, rec, include, maxItems, result)
		if len(result.Items) >= maxItems || remaining <= 0 {
			break
		}
		if !board.RolloutConfig().RecallForwardCrossSession {
			result.Diagnostics = append(result.Diagnostics, "cross-session recall disabled by "+EnvRecallForwardCrossSession+"; stopped at current session continuity")
			break
		}
		sessionAnchor := rec
		if chain.HasLast {
			sessionAnchor = chain.Last
		}
		nextSession := metadataString(sessionAnchor.SessionCursor, "previous_session_id")
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
			if IsLegacySessionBoardError(err) {
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("legacy previous session %s has no durable claims board WAL; using Archivalist/knowledge fallback only", nextSession))
			} else {
				result.Partial = true
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("open previous session %s: %v", nextSession, err))
			}
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
		appendProjectionDiagnostics(nextBoard, result)
		current = nextBoard
		nextID := metadataString(sessionAnchor.SessionCursor, "previous_continuity_testament_id")
		if nextID != "" {
			nextRec, ok := continuityRecordByID(current, nextID, agentID, topic)
			if !ok {
				result.Partial = true
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("broken continuity spine from testament=%s session=%s: previous_continuity_testament_id %s not found for agent=%s topic=%q", sessionAnchor.Testament.ID, nextSession, nextID, agentID, topic))
				break
			}
			rec = nextRec
		} else {
			nextRec, ok := latestContinuityRecord(current, agentID, topic)
			if !ok {
				result.Partial = true
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("continuity spine ended at session=%s: no continuity testament for agent=%s topic=%q", nextSession, agentID, topic))
				break
			}
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("session %s did not provide previous_continuity_testament_id; used latest matching continuity testament %s", nextSession, nextRec.Testament.ID))
			rec = nextRec
		}
		remaining--
	}
	appendRecallEnrichment(ctx, board, opts.EnrichmentProvider, result, maxItems, RecallForwardEnrichmentQuery{
		AgentID:   result.AgentID,
		Topic:     result.Topic,
		SessionID: board.SessionID(),
		BoardID:   board.BoardID(),
	})
	synthesizeLegacyContinuity(board, result)
	return finalizeRecallForwardResult(result), nil
}

func finalizeRecallForwardResult(result *RecallForwardResult) *RecallForwardResult {
	if result == nil {
		return nil
	}
	sourceIndexCount := 0
	hydrationAvailable := false
	usableItem := false
	for _, item := range result.Items {
		sourceIndexCount += item.SourceIndexCount
		hydrationAvailable = hydrationAvailable || item.HydrationAvailable
		if recallForwardItemIsUsable(item) {
			usableItem = true
		}
	}
	result.SourceIndexCount = sourceIndexCount
	result.HydrationAvailable = hydrationAvailable || len(result.FullTestaments) > 0 || len(result.FullArtifacts) > 0
	result.Status = ""
	result.Usable = false
	result.Reason = ""
	result.RecommendedNextAction = ""

	switch {
	case result.Contradicted:
		result.Status = RecallForwardStatusContradicted
		result.Reason = "carried-forward continuity is marked contradicted"
	case result.Stale:
		result.Status = RecallForwardStatusStale
		result.Reason = "carried-forward continuity is marked stale"
	case result.Partial:
		result.Status = RecallForwardStatusPartial
		result.Reason = "continuity traversal or enrichment was incomplete"
	case len(result.Items) == 0:
		if len(result.Enrichment) > 0 {
			result.Status = RecallForwardStatusInsufficient
			result.Reason = "related enrichment exists, but no source-indexed carry_forward continuity was found for this agent/topic"
		} else {
			result.Status = RecallForwardStatusMiss
			result.Reason = "no carried-forward continuity was found for this agent/topic"
		}
	case usableItem:
		result.Status = RecallForwardStatusUsable
		result.Usable = true
		if result.IncludeSources == "digest" && result.SourceIndexCount > len(result.Sources) {
			result.RecommendedNextAction = "reuse_recalled_evidence; call recall_forward(include_sources=source_index or full) only if exact source IDs or hydrated evidence are needed"
		} else {
			result.RecommendedNextAction = "reuse_recalled_evidence"
		}
	default:
		result.Status = RecallForwardStatusInsufficient
		result.Reason = recallForwardInsufficiencyReason(result)
	}
	if !result.Usable && result.RecommendedNextAction == "" {
		result.RecommendedNextAction = "perform the narrowest necessary evidence-gathering step, then call carry_forward(topic=..., mode=advance) to publish source-indexed continuity"
	}
	return result
}

func recallForwardItemIsUsable(item ContinuityRecallItem) bool {
	if item.Stale || item.Contradicted || item.Reconstructed {
		return false
	}
	if strings.TrimSpace(item.TestamentID) == "" || item.SourceIndexCount <= 0 {
		return false
	}
	return strings.TrimSpace(item.WorkingContext) != "" || strings.TrimSpace(item.EvidenceDigest) != ""
}

func recallForwardInsufficiencyReason(result *RecallForwardResult) string {
	if result == nil {
		return "recall result was empty"
	}
	if len(result.Items) == 0 {
		return "no carried-forward continuity items were returned"
	}
	reconstructed := 0
	for _, item := range result.Items {
		if item.Reconstructed {
			reconstructed++
		}
	}
	if reconstructed == len(result.Items) {
		return "only reconstructed legacy continuity was available; it lacks source-indexed carry_forward artifacts"
	}
	if result.SourceIndexCount == 0 {
		return "continuity items did not expose source_index artifacts back to durable testaments/artifacts"
	}
	return "continuity did not contain enough source-backed context to replace fresh evidence gathering"
}

func appendRecallEnrichment(ctx context.Context, board *ClaimsBoard, provider RecallForwardEnrichmentProvider, result *RecallForwardResult, maxItems int, fallback RecallForwardEnrichmentQuery) {
	if provider == nil {
		provider = DefaultRecallForwardEnrichmentProvider()
	}
	if provider == nil || result == nil {
		return
	}
	limit := maxItems
	if limit <= 0 {
		limit = 8
	}
	seen := make(map[string]struct{})
	if len(result.Items) == 0 {
		fallback.MaxItems = limit
		fallback.IncludeSource = result.IncludeSources
		appendEnrichmentHits(ctx, board, provider, result, fallback, seen, limit)
		return
	}
	for _, item := range result.Items {
		if len(result.Enrichment) >= limit {
			return
		}
		query := RecallForwardEnrichmentQuery{
			AgentID:       result.AgentID,
			Topic:         result.Topic,
			SessionID:     item.SessionID,
			BoardID:       item.BoardID,
			ClaimID:       item.ClaimID,
			TestamentID:   item.TestamentID,
			MaxItems:      limit - len(result.Enrichment),
			IncludeSource: result.IncludeSources,
		}
		if appendEnrichmentHits(ctx, board, provider, result, query, seen, limit) {
			return
		}
	}
}

func appendEnrichmentHits(ctx context.Context, board *ClaimsBoard, provider RecallForwardEnrichmentProvider, result *RecallForwardResult, query RecallForwardEnrichmentQuery, seen map[string]struct{}, limit int) bool {
	hits, err := provider.LookupCarryForwardEnrichment(ctx, query)
	if err != nil {
		result.Partial = true
		result.Diagnostics = append(result.Diagnostics, "archivalist enrichment lookup: "+err.Error())
		recordRecallEnrichmentProjectionError(board, query, err)
		return true
	}
	for _, hit := range hits {
		key := strings.Join([]string{hit.Source, hit.DocumentID, hit.EntityType, hit.EntityID, hit.Path}, "\x1f")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result.Enrichment = append(result.Enrichment, hit)
		if len(result.Enrichment) >= limit {
			return true
		}
	}
	return false
}

func recordRecallEnrichmentProjectionError(board *ClaimsBoard, query RecallForwardEnrichmentQuery, err error) {
	if board == nil || err == nil {
		return
	}
	reference := fmt.Sprintf("projection_error projector=%s board=%s session=%s operation=recall_forward_enrichment topic=%q agent=%s: %s", ProjectorKnowledge, firstNonBlankString(query.BoardID, board.BoardID()), firstNonBlankString(query.SessionID, board.SessionID()), query.Topic, query.AgentID, err.Error())
	artifact := &Artifact{
		AgentID:   projectionDiagnosticsAgentID,
		SessionID: board.SessionID(),
		Kind:      ArtifactKindProjectionError,
		Reference: reference,
		Metadata: map[string]any{
			"operation":  "recall_forward_enrichment",
			"projector":  ProjectorKnowledge,
			"board_id":   firstNonBlankString(query.BoardID, board.BoardID()),
			"session_id": firstNonBlankString(query.SessionID, board.SessionID()),
			"agent_id":   query.AgentID,
			"topic":      query.Topic,
			"error":      err.Error(),
		},
	}
	generated, genErr := board.GenerateTestamentAction(context.Background(), Action{AgentID: projectionDiagnosticsAgentID, Type: ActionTypeTestament}, []Testament{{
		AgentID:    projectionDiagnosticsAgentID,
		SessionID:  board.SessionID(),
		Summary:    reference,
		Confidence: "committed",
		Artifacts:  []*Artifact{artifact},
	}}, GenerateTestamentActionOptions{
		IdempotencyKey:  "recall_forward_enrichment_error:" + firstNonBlankString(query.AgentID, "unknown") + ":" + firstNonBlankString(query.Topic, "unknown") + ":" + truncateForDigest(err.Error(), 64),
		AllowStandalone: true,
		Reason:          "recall enrichment projection error generated",
	})
	if genErr != nil {
		board.RecordNotificationError("recall enrichment projection error artifact: " + genErr.Error())
		return
	}
	if len(generated.Testaments) == 0 {
		board.RecordNotificationError("recall enrichment projection error artifact: no generated testament")
		return
	}
	if submitErr := board.PostGeneratedTestament(context.Background(), generated.Testaments[0].ID, projectionDiagnosticsAgentID, TestamentPostOptions{Reason: "recall enrichment projection error posted"}); submitErr != nil {
		board.RecordNotificationError("recall enrichment projection error artifact: " + submitErr.Error())
	}
}

func firstNonBlankString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func latestContinuityRecord(board *ClaimsBoard, agentID, topic string) (continuityRecord, bool) {
	superseded := make(map[string]struct{})
	records := continuityRecordsFor(board, agentID, topic)
	for _, rec := range records {
		t := rec.Testament
		for _, rel := range t.Relations {
			if rel.RelatedType == RelatedTypeTestament && rel.Relationship == RelationshipSupersedes {
				superseded[rel.Related] = struct{}{}
			}
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

func continuityRecordsFor(board *ClaimsBoard, agentID, topic string) []continuityRecord {
	if board == nil {
		return nil
	}
	claimIDs := board.ClaimIDsWithScope(carryForwardTopicScopeKind, topic)
	records := make([]continuityRecord, 0)
	seen := make(map[string]struct{})
	for _, claimID := range claimIDs {
		c, ok := board.CloneClaim(claimID)
		if !ok || c == nil {
			continue
		}
		if !isRecallableClaimLifecycle(c.LifecycleStatus) {
			continue
		}
		if !claimHasScope(c.Scope, carryForwardScopeKind, carryForwardScopeKey) ||
			!claimHasScope(c.Scope, carryForwardAgentScopeKind, agentID) {
			continue
		}
		for _, id := range board.ObjectIDsWithRelation(RelatedTypeClaim, RelationshipClaim, claimID) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			rec, ok := continuityRecordByID(board, id, agentID, topic)
			if ok {
				records = append(records, rec)
			}
		}
	}
	if len(records) > 0 {
		return records
	}
	// Legacy fallback for continuity testaments written before the
	// continuity claim/scope index existed. Normal recall stays bounded by
	// claim scope and relation indexes.
	p := board.Projection()
	for i := range p.Testaments {
		id := p.Testaments[i].ID
		if _, ok := seen[id]; ok {
			continue
		}
		rec, ok := continuityRecordByID(board, id, agentID, topic)
		if ok {
			records = append(records, rec)
		}
	}
	return records
}

func continuityRecordByID(board *ClaimsBoard, testamentID, agentID, topic string) (continuityRecord, bool) {
	t, ok := board.CloneTestament(strings.TrimSpace(testamentID))
	if !ok || t == nil {
		return continuityRecord{}, false
	}
	return continuityFromTestament(t, agentID, topic)
}

func continuityFromTestament(t *Testament, agentID, topic string) (continuityRecord, bool) {
	if t == nil {
		return continuityRecord{}, false
	}
	if !isRecallableTestamentLifecycle(t.LifecycleStatus) {
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
		stale, contradicted := continuityArtifactStatus(artifact)
		rec.Stale = rec.Stale || stale
		rec.Contradicted = rec.Contradicted || contradicted
	}
	if rec.Cursor == nil {
		return continuityRecord{}, false
	}
	return rec, true
}

func isRecallableClaimLifecycle(status ClaimLifecycleStatus) bool {
	switch status {
	case ClaimLifecyclePosted,
		ClaimLifecycleReceived,
		ClaimLifecycleProgressed,
		ClaimLifecycleTestamentGenerated,
		ClaimLifecycleTestamentAcknowledged,
		ClaimLifecycleValidating,
		ClaimLifecycleSatisfied,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored:
		return true
	default:
		return false
	}
}

func isRecallableTestamentLifecycle(status TestamentLifecycleStatus) bool {
	switch status {
	case TestamentLifecyclePosted,
		TestamentLifecycleReceived,
		TestamentLifecycleValidating,
		TestamentLifecycleValidated,
		TestamentLifecycleValidationIncomplete,
		TestamentLifecycleValidationFailed,
		TestamentLifecycleValidationErrored:
		return true
	default:
		return false
	}
}

func ensureContinuityClaim(ctx context.Context, board *ClaimsBoard, agentID, topic, planID, correlationID string) (*Claim, bool, error) {
	p := board.Projection()
	for i := range p.Claims {
		c := p.Claims[i]
		if c.ActionType != ActionTypeArchival || c.Status == ClaimStatusSuperseded || c.Status == ClaimStatusRejected {
			continue
		}
		if !isRecallableClaimLifecycle(c.LifecycleStatus) && c.LifecycleStatus != ClaimLifecycleGenerated {
			continue
		}
		if !claimHasScope(c.Scope, carryForwardScopeKind, carryForwardScopeKey) ||
			!claimHasScope(c.Scope, carryForwardTopicScopeKind, topic) ||
			!claimHasScope(c.Scope, carryForwardAgentScopeKind, agentID) {
			continue
		}
		clone, ok := board.CloneClaim(c.ID)
		if ok {
			if clone.LifecycleStatus == ClaimLifecycleGenerated {
				if err := board.PostGeneratedClaim(ctx, clone.ID, agentID, ClaimPostOptions{Reason: "continuity claim posted"}); err != nil {
					return nil, false, err
				}
				if posted, postedOK := board.CloneClaim(clone.ID); postedOK {
					return posted, true, nil
				}
			}
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
			ID:          "continuity." + agentID + "." + topic + ".receipt",
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
	generated, err := board.GenerateClaimAction(ctx, action, []Claim{claim}, GenerateClaimActionOptions{
		IdempotencyKey: "carry_forward:claim:" + agentID + ":" + topic,
		Reason:         "continuity claim generated",
	})
	if err != nil {
		return nil, false, err
	}
	if len(generated.Claims) == 0 {
		return nil, false, fmt.Errorf("continuity claim generation returned no claims")
	}
	if err := board.PostGeneratedClaim(ctx, generated.Claims[0].ID, agentID, ClaimPostOptions{Reason: "continuity claim posted"}); err != nil {
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

func selectForwardSources(board *ClaimsBoard, agentID, topic string, fromSeq, throughSeq uint64, maxSources int, horizon time.Duration, now time.Time, incorporated []ForwardSource) []scoredForwardSource {
	p := board.Projection()
	out := make([]scoredForwardSource, 0, maxSources)
	seen := make(map[string]struct{})
	prior := incorporatedSourceIndex(incorporated)
	for i := range p.Testaments {
		t, ok := board.CloneTestament(p.Testaments[i].ID)
		if !ok || t == nil || t.Sequence <= fromSeq || t.Sequence > throughSeq {
			continue
		}
		if !isRecallableTestamentLifecycle(t.LifecycleStatus) {
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
			digest := sourceDigest(t, artifact)
			if prior.containsArtifact(artifact.ID) || (prior.containsDigest(artifact.Kind, digest) && !artifactCarriesRefreshStatus(artifact)) {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, scoredForwardSource{
				score: score,
				source: ForwardSource{
					TestamentID:           t.ID,
					ArtifactID:            artifact.ID,
					AgentID:               firstNonEmpty(artifact.AgentID, t.AgentID, agentID),
					Kind:                  artifact.Kind,
					Sequence:              artifact.Sequence,
					Reason:                reason,
					SelectedBy:            selectorNameForArtifact(artifact.Kind),
					Score:                 score,
					Digest:                digest,
					ProjectionDocumentIDs: projectionDocumentIDsFromMetadata(artifact.Metadata),
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
			digest := truncateForDigest(t.Summary, 240)
			if prior.containsTestament(t.ID) || prior.containsDigest("testament", digest) {
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
					SelectedBy:  "testament_summary_policy",
					Score:       score,
					Digest:      digest,
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

type incorporatedSourceSet struct {
	testaments map[string]struct{}
	artifacts  map[string]struct{}
	digests    map[string]struct{}
}

func incorporatedSourceIndex(sources []ForwardSource) incorporatedSourceSet {
	set := incorporatedSourceSet{
		testaments: make(map[string]struct{}, len(sources)),
		artifacts:  make(map[string]struct{}, len(sources)),
		digests:    make(map[string]struct{}, len(sources)),
	}
	for _, source := range sources {
		if source.TestamentID != "" {
			set.testaments[source.TestamentID] = struct{}{}
		}
		if source.ArtifactID != "" {
			set.artifacts[source.ArtifactID] = struct{}{}
		}
		if source.Digest != "" {
			set.digests[sourceDigestKey(source.Kind, source.Digest)] = struct{}{}
		}
	}
	return set
}

func (s incorporatedSourceSet) containsTestament(id string) bool {
	_, ok := s.testaments[strings.TrimSpace(id)]
	return ok
}

func (s incorporatedSourceSet) containsArtifact(id string) bool {
	_, ok := s.artifacts[strings.TrimSpace(id)]
	return ok
}

func (s incorporatedSourceSet) containsDigest(kind, digest string) bool {
	_, ok := s.digests[sourceDigestKey(kind, digest)]
	return ok
}

func sourceDigestKey(kind, digest string) string {
	return strings.TrimSpace(strings.ToLower(kind)) + "\x00" + strings.TrimSpace(digest)
}

func artifactCarriesRefreshStatus(a *Artifact) bool {
	if a == nil || len(a.Metadata) == 0 {
		return false
	}
	return metadataBool(a.Metadata, "revalidated") ||
		metadataBool(a.Metadata, "refresh") ||
		metadataBool(a.Metadata, "stale") ||
		metadataBool(a.Metadata, "contradicted") ||
		strings.EqualFold(metadataString(a.Metadata, "status"), "revalidated") ||
		strings.EqualFold(metadataString(a.Metadata, "status"), "stale") ||
		strings.EqualFold(metadataString(a.Metadata, "status"), "contradicted")
}

func selectorNameForArtifact(kind string) string {
	kind = strings.TrimSpace(strings.ToLower(kind))
	switch {
	case isCarryForwardErrorArtifactKind(kind):
		return "error_blocker_policy"
	case kind == ArtifactKindResponseText || kind == "consult_response" || kind == "challenge_response":
		return "peer_answer_policy"
	case strings.Contains(kind, "test") || strings.Contains(kind, "verification"):
		return "test_verification_policy"
	case strings.Contains(kind, "workspace") || strings.Contains(kind, "code") || strings.Contains(kind, "diff") || strings.Contains(kind, "file"):
		return "workspace_discovery_policy"
	case strings.Contains(kind, "decision") || strings.Contains(kind, "plan") || strings.Contains(kind, "design"):
		return "decision_design_policy"
	case strings.Contains(kind, "research") || strings.Contains(kind, "source") || strings.Contains(kind, "citation"):
		return "research_evidence_policy"
	default:
		return "durable_artifact_policy"
	}
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
	} else if strings.TrimSpace(opts.PreviousSessionID) == "" && priorID != "" {
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
		SessionID:           board.SessionID(),
		BoardID:             board.BoardID(),
		ClaimID:             rec.ClaimID,
		TestamentID:         rec.Testament.ID,
		FromSequence:        rec.FromSequence,
		ThroughSequence:     rec.ThroughSequence,
		Stale:               rec.Stale,
		Contradicted:        rec.Contradicted,
		SourceIndexCount:    len(rec.Sources),
		SourceIndexReturned: include == "source_index" || include == "full",
		HydrationAvailable:  len(rec.Sources) > 0,
		WorkingContext:      rec.WorkingContext,
		EvidenceDigest:      rec.EvidenceDigest,
		Cursor:              cloneAnyMap(rec.Cursor),
	}
	if include == "source_index" || include == "full" {
		item.Sources = rec.Sources
	}
	return item
}

type continuityChainAppendResult struct {
	Last    continuityRecord
	HasLast bool
}

func appendContinuityChain(ctx context.Context, board *ClaimsBoard, start continuityRecord, include string, maxItems int, result *RecallForwardResult) continuityChainAppendResult {
	var chain continuityChainAppendResult
	if board == nil || result == nil {
		return chain
	}
	seen := make(map[string]struct{})
	current := start
	for {
		if err := ctx.Err(); err != nil {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, "recall cancelled while traversing continuity chain: "+err.Error())
			return chain
		}
		if _, ok := seen[current.Testament.ID]; ok {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("cycle in continuity amends chain at testament=%s session=%s", current.Testament.ID, board.SessionID()))
			return chain
		}
		seen[current.Testament.ID] = struct{}{}
		if maxItems > 0 && len(result.Items) >= maxItems {
			return chain
		}
		item := continuityRecallItem(board, current, include)
		result.Items = append(result.Items, item)
		chain.Last = current
		chain.HasLast = true
		result.WorkingContext = appendContextBlock(result.WorkingContext, item.WorkingContext)
		result.EvidenceDigest = appendContextBlock(result.EvidenceDigest, item.EvidenceDigest)
		result.Stale = result.Stale || item.Stale
		result.Contradicted = result.Contradicted || item.Contradicted
		if item.Stale {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("continuity testament %s is marked stale", item.TestamentID))
		}
		if item.Contradicted {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("continuity testament %s is marked contradicted", item.TestamentID))
		}
		if include == "source_index" || include == "full" {
			result.Sources = appendBoundedSources(result.Sources, item.Sources, maxItems)
		}
		if include == "full" {
			appendFullSources(board, item.Sources, &result.FullTestaments, &result.FullArtifacts, maxItems)
		}

		nextID := amendedContinuityTestamentID(current.Testament)
		if nextID == "" {
			return chain
		}
		next, ok := continuityRecordByID(board, nextID, current.Testament.AgentID, normalizeContinuityTopic(metadataString(current.Cursor, "topic")))
		if !ok {
			result.Partial = true
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("broken continuity amends link from testament=%s to testament=%s session=%s", current.Testament.ID, nextID, board.SessionID()))
			return chain
		}
		current = next
	}
}

func amendedContinuityTestamentID(t *Testament) string {
	if t == nil {
		return ""
	}
	for _, rel := range t.Relations {
		if rel.RelatedType == RelatedTypeTestament && rel.Relationship == RelationshipAmends {
			return strings.TrimSpace(rel.Related)
		}
	}
	return ""
}

func incorporatedSourcesForRecord(board *ClaimsBoard, start continuityRecord, agentID, topic string) []ForwardSource {
	if board == nil || start.Testament == nil {
		return nil
	}
	var out []ForwardSource
	seen := make(map[string]struct{})
	seenRecords := make(map[string]struct{})
	current := start
	for {
		if _, ok := seenRecords[current.Testament.ID]; ok {
			return out
		}
		seenRecords[current.Testament.ID] = struct{}{}
		for _, source := range current.Sources {
			key := source.TestamentID + "\x00" + source.ArtifactID + "\x00" + sourceDigestKey(source.Kind, source.Digest)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, source)
		}
		nextID := amendedContinuityTestamentID(current.Testament)
		if nextID == "" {
			return out
		}
		next, ok := continuityRecordByID(board, nextID, agentID, topic)
		if !ok {
			return out
		}
		current = next
	}
}

func appendFullSources(board *ClaimsBoard, sources []ForwardSource, testaments *[]*Testament, artifacts *[]*Artifact, maxItems int) {
	seenT := make(map[string]struct{}, len(*testaments))
	seenA := make(map[string]struct{}, len(*artifacts))
	for _, t := range *testaments {
		if t != nil {
			seenT[t.ID] = struct{}{}
		}
	}
	for _, a := range *artifacts {
		if a != nil {
			seenA[a.ID] = struct{}{}
		}
	}
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

func continuityArtifactStatus(a *Artifact) (stale bool, contradicted bool) {
	if a == nil || len(a.Metadata) == 0 {
		return false, false
	}
	stale = metadataBool(a.Metadata, "stale") ||
		metadataBool(a.Metadata, "is_stale") ||
		strings.EqualFold(metadataString(a.Metadata, "status"), "stale") ||
		strings.EqualFold(metadataString(a.Metadata, "freshness"), "stale") ||
		strings.EqualFold(metadataString(a.Metadata, "freshness_status"), "stale")
	contradicted = metadataBool(a.Metadata, "contradicted") ||
		metadataBool(a.Metadata, "is_contradicted") ||
		strings.EqualFold(metadataString(a.Metadata, "status"), "contradicted") ||
		strings.EqualFold(metadataString(a.Metadata, "verdict"), "contradicted")
	return stale, contradicted
}

func appendProjectionDiagnostics(board *ClaimsBoard, result *RecallForwardResult) {
	if board == nil || result == nil {
		return
	}
	rollout := board.RolloutConfig()
	health := board.ProjectionHealth()
	if rollout.ProjectionWarningsAuthoritative() {
		for _, warning := range health.Warnings {
			warning = strings.TrimSpace(warning)
			if warning == "" {
				continue
			}
			result.Diagnostics = append(result.Diagnostics, warning)
		}
		if health.QueueDepth > 0 {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("projection backlog queue_depth=%d max_lag=%d retry_count=%d terminal_failures=%d", health.QueueDepth, health.MaxLag, health.RetryCount, health.TerminalFailures))
		}
	} else if health.QueueDepth > 0 || health.RetryCount > 0 || health.TerminalFailures > 0 || health.LeaseExpirations > 0 {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("projection diagnostics shadowed by %s=%s queue_depth=%d max_lag=%d retry_count=%d terminal_failures=%d", EnvClaimsKnowledgeMirror, rollout.ClaimsKnowledgeMirror, health.QueueDepth, health.MaxLag, health.RetryCount, health.TerminalFailures))
	}
	for _, diff := range health.ShadowDiffs {
		diff = strings.TrimSpace(diff)
		if diff != "" {
			result.Diagnostics = append(result.Diagnostics, diff)
		}
	}
	p := board.Projection()
	for _, msg := range p.NotificationErrors {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		if strings.Contains(strings.ToLower(msg), "projection") {
			result.Diagnostics = append(result.Diagnostics, msg)
		}
	}
	count := 0
	for i := range p.Testaments {
		if count >= 8 {
			break
		}
		t, ok := board.CloneTestament(p.Testaments[i].ID)
		if !ok || t == nil {
			continue
		}
		for _, artifact := range t.Artifacts {
			if artifact == nil || artifact.Kind != ArtifactKindProjectionError {
				continue
			}
			msg := strings.TrimSpace(artifact.Reference)
			if msg == "" {
				msg = "projection_error artifact " + artifact.ID
			}
			result.Diagnostics = append(result.Diagnostics, msg)
			count++
			if count >= 8 {
				break
			}
		}
	}
}

func appendRolloutRecallDiagnostics(board *ClaimsBoard, result *RecallForwardResult) {
	if board == nil || result == nil {
		return
	}
	rollout := board.RolloutConfig()
	if !rollout.ClaimsOutbox {
		result.Diagnostics = append(result.Diagnostics, "claims projection outbox disabled by "+EnvClaimsOutbox+"; canonical board recall remains available but derived projections will not catch up")
	}
	if !rollout.ClaimsKnowledgeMirrorEnabled() {
		result.Diagnostics = append(result.Diagnostics, "claims knowledge mirror disabled by "+EnvClaimsKnowledgeMirror+"; Archivalist/knowledge enrichment is unavailable unless another provider is configured")
	}
	if board.LegacySessionNoWAL() {
		result.Diagnostics = append(result.Diagnostics, "legacy session has no pre-existing durable claims board WAL; recall will use exact board continuity only for new work and Archivalist/knowledge fallback for pre-migration context")
	}
}

func synthesizeLegacyContinuity(board *ClaimsBoard, result *RecallForwardResult) {
	if result == nil || len(result.Items) > 0 || len(result.Enrichment) == 0 {
		return
	}
	for _, hit := range result.Enrichment {
		if !legacyReconstructionCandidate(hit) {
			continue
		}
		exact := hit.ExactClaimSources || strings.TrimSpace(hit.ClaimID) != "" || strings.TrimSpace(hit.TestamentID) != "" || strings.TrimSpace(hit.ArtifactID) != ""
		sessionID := firstNonBlankString(hit.SessionID)
		boardID := firstNonBlankString(hit.BoardID)
		if board != nil {
			sessionID = firstNonBlankString(sessionID, board.SessionID())
			boardID = firstNonBlankString(boardID, board.BoardID())
		}
		source := legacyReconstructionSource(hit)
		cursor := map[string]any{
			"reconstructed":       true,
			"source":              source,
			"exact_claim_sources": exact,
		}
		if hit.DocumentID != "" {
			cursor["projection_document_id"] = hit.DocumentID
		}
		if hit.ArchivalistEntryID != "" {
			cursor["archivalist_entry_id"] = hit.ArchivalistEntryID
		}
		item := ContinuityRecallItem{
			SessionID:            sessionID,
			BoardID:              boardID,
			ClaimID:              idIfExact(hit.ClaimID, exact),
			TestamentID:          idIfExact(hit.TestamentID, exact),
			WorkingContext:       hit.Summary,
			EvidenceDigest:       legacyEvidenceDigest(hit),
			Cursor:               cursor,
			Reconstructed:        true,
			ReconstructionSource: source,
			ExactClaimSources:    exact,
		}
		result.Items = append(result.Items, item)
		if result.WorkingContext == "" {
			result.WorkingContext = item.WorkingContext
		}
		if result.EvidenceDigest == "" {
			result.EvidenceDigest = item.EvidenceDigest
		}
		if !exact {
			result.Diagnostics = append(result.Diagnostics, "reconstructed legacy continuity from "+source+" has no exact claim/testament/artifact source IDs")
		}
		return
	}
}

func legacyReconstructionCandidate(hit RecallForwardEnrichment) bool {
	return hit.Legacy ||
		hit.AgenticNarrative ||
		strings.TrimSpace(hit.ArchivalistEntryID) != "" ||
		strings.Contains(strings.ToLower(hit.Source), "legacy") ||
		strings.Contains(strings.ToLower(hit.Path), "archivalist/")
}

func legacyReconstructionSource(hit RecallForwardEnrichment) string {
	switch {
	case strings.TrimSpace(hit.ArchivalistEntryID) != "":
		return "archivalist_scribe_entries"
	case strings.TrimSpace(hit.Source) != "":
		return strings.TrimSpace(hit.Source)
	default:
		return "knowledge_fallback"
	}
}

func legacyEvidenceDigest(hit RecallForwardEnrichment) string {
	source := legacyReconstructionSource(hit)
	summary := strings.TrimSpace(hit.Summary)
	if summary == "" {
		return "Reconstructed continuity candidate from " + source + "."
	}
	return "Reconstructed continuity candidate from " + source + ": " + summary
}

func idIfExact(value string, exact bool) string {
	if !exact {
		return ""
	}
	return strings.TrimSpace(value)
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

func sourceDigests(sources []ForwardSource) []map[string]any {
	out := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		finding := map[string]any{
			"summary":             source.Digest,
			"reason":              source.Reason,
			"selected_by":         source.SelectedBy,
			"kind":                source.Kind,
			"source_testament_id": source.TestamentID,
			"source_artifact_id":  source.ArtifactID,
		}
		if len(source.ProjectionDocumentIDs) > 0 {
			finding["projection_document_ids"] = source.ProjectionDocumentIDs
		}
		out = append(out, finding)
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

func metadataBool(md map[string]any, key string) bool {
	if len(md) == 0 {
		return false
	}
	switch v := md[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1", "stale", "contradicted", "revalidated":
			return true
		default:
			return false
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return false
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

func projectionDocumentIDsFromMetadata(md map[string]any) []string {
	if len(md) == 0 {
		return nil
	}
	out := make([]string, 0, 2)
	appendOne := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		for _, existing := range out {
			if existing == raw {
				return
			}
		}
		out = append(out, raw)
	}
	for _, key := range []string{"projection_document_id", "document_id", "claims_document_id"} {
		appendOne(metadataString(md, key))
	}
	for _, key := range []string{"projection_document_ids", "document_ids", "claims_document_ids"} {
		switch v := md[key].(type) {
		case []string:
			for _, item := range v {
				appendOne(item)
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					appendOne(s)
				}
			}
		}
	}
	return out
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
