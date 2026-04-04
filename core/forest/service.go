package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/knowledge/memory"
)

// Config configures the Memory Forest service.
type Config struct {
	DB           *sql.DB
	ContentStore *ctxpkg.UniversalContentStore
	Searcher     *ctxpkg.TieredSearcher
	Logger       *slog.Logger
	MaxTraces    int
	Booster      boosterConfig

	ReplayDelay         time.Duration
	SubstrateDebounce   time.Duration
	TrainingDebounce    time.Duration
	ReplayBatchSize     int
	SubstrateLimit      int
	TrainingMaxExamples int
}

// MemoryForest provides forest projection, retrieval, and learning services.
type MemoryForest struct {
	db           *sql.DB
	contentStore *ctxpkg.UniversalContentStore
	searcher     *ctxpkg.TieredSearcher
	logger       *slog.Logger
	warmth       *WarmthStore
	models       *learnedModelStore
	booster      boosterConfig

	runCtx    context.Context
	runCancel context.CancelFunc

	replayDelay         time.Duration
	substrateDebounce   time.Duration
	trainingDebounce    time.Duration
	replayBatchSize     int
	substrateLimit      int
	trainingMaxExamples int

	maintenanceRunMu sync.Mutex
	maintenanceMu    sync.Mutex
	maintenanceWake  chan struct{}
	pendingSubstrate map[string]scheduledForestWork
	replayDue        time.Time
	trainingWork     scheduledForestWork
	trainingDirty    bool

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New creates and initializes a Memory Forest service.
func New(cfg Config) (*MemoryForest, error) {
	if cfg.DB == nil {
		return nil, errors.New("forest db is required")
	}
	if err := ensureSchema(cfg.DB); err != nil {
		return nil, err
	}
	models, err := newLearnedModelStore(cfg.DB)
	if err != nil {
		return nil, err
	}
	booster := cfg.Booster
	if booster == (boosterConfig{}) {
		booster = defaultBoosterConfig()
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	service := &MemoryForest{
		db:                  cfg.DB,
		contentStore:        cfg.ContentStore,
		searcher:            cfg.Searcher,
		logger:              normalizeLogger(cfg.Logger),
		warmth:              newWarmthStore(cfg.DB, cfg.MaxTraces),
		models:              models,
		booster:             booster,
		runCtx:              runCtx,
		runCancel:           runCancel,
		replayDelay:         resolveForestReplayDelay(cfg.ReplayDelay),
		substrateDebounce:   resolveForestSubstrateDebounce(cfg.SubstrateDebounce),
		trainingDebounce:    resolveForestTrainingDebounce(cfg.TrainingDebounce),
		replayBatchSize:     resolveForestReplayBatchSize(cfg.ReplayBatchSize),
		substrateLimit:      resolveForestSubstrateLimit(cfg.SubstrateLimit),
		trainingMaxExamples: resolveForestTrainingExamples(cfg.TrainingMaxExamples),
		maintenanceWake:     make(chan struct{}, 1),
		pendingSubstrate:    make(map[string]scheduledForestWork),
		stopCh:              make(chan struct{}),
	}

	service.startMaintenance()
	return service, nil
}

// Close stops background maintenance loops.
func (m *MemoryForest) Close() error {
	m.stopOnce.Do(func() {
		if m.runCancel != nil {
			m.runCancel()
		}
		close(m.stopCh)
	})
	m.wg.Wait()
	return nil
}

func normalizeLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

// SetContentStore wires a content store after forest construction.
func (m *MemoryForest) SetContentStore(store *ctxpkg.UniversalContentStore) {
	if m == nil {
		return
	}
	m.contentStore = store
}

// SetSearcher wires a tiered searcher after forest construction.
func (m *MemoryForest) SetSearcher(searcher *ctxpkg.TieredSearcher) {
	if m == nil {
		return
	}
	m.searcher = searcher
}

// OnContentIndexed satisfies context.ContentIndexObserver.
func (m *MemoryForest) OnContentIndexed(ctx context.Context, entry *ctxpkg.ContentEntry) error {
	if entry == nil {
		return nil
	}
	if entry.Metadata != nil && entry.Metadata[ctxpkg.MetadataForestSynthetic] == "true" {
		return nil
	}
	return m.RecordContent(ctx, entry)
}

// RecordContent maps a content entry into the ledger and branch projection.
func (m *MemoryForest) RecordContent(ctx context.Context, entry *ctxpkg.ContentEntry) error {
	if entry == nil {
		return nil
	}
	event := eventFromContent(entry)
	return m.AppendEvent(ctx, event)
}

// RecordOutcome records explicit branch outcome feedback.
func (m *MemoryForest) RecordOutcome(ctx context.Context, record OutcomeRecord) error {
	if strings.TrimSpace(record.BranchID) == "" {
		return errors.New("branch_id is required")
	}

	branch, err := m.getBranch(ctx, record.BranchID)
	if err != nil {
		return err
	}
	if branch == nil {
		return fmt.Errorf("branch %s not found", record.BranchID)
	}

	event := &Event{
		SessionID:      firstNonEmpty(record.SessionID, branch.SessionID),
		TaskID:         firstNonEmpty(record.TaskID, branch.TaskID),
		AgentID:        record.AgentID,
		AgentType:      record.AgentType,
		EventType:      EventTypeOutcomeRecorded,
		Family:         TreeFamilyOutcome,
		Scope:          branch.Scope,
		RootID:         branch.RootID,
		BranchID:       branch.ID,
		ParentBranchID: branch.ParentID,
		IntentID:       firstNonEmpty(branch.IntentID, branch.RootID),
		Confidence:     defaultConfidence(record.Confidence),
		Salience:       defaultSalience(record.Salience),
		Timestamp:      time.Now().UTC(),
		Title:          summarizeText(record.Summary, 72),
		Summary:        summarizeText(record.Summary, 240),
		ProvenanceRefs: dedupeStrings(record.ProvenanceRefs),
		Supersedes:     dedupeStrings(record.Supersedes),
		Contradicts:    dedupeStrings(record.Contradicts),
		Payload: map[string]any{
			"status": record.Status,
		},
	}
	return m.AppendEvent(ctx, event)
}

// AppendEvent records an event and updates branch projections.
func (m *MemoryForest) AppendEvent(ctx context.Context, event *Event) error {
	prepared := prepareEvent(event)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin forest tx: %w", err)
	}
	defer tx.Rollback()

	branch, created, replayDue, err := m.projectEventTx(ctx, tx, prepared)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit forest tx: %w", err)
	}

	if created {
		_ = m.warmth.RecordAccess(ctx, branch.ID, memory.AccessCreation, string(prepared.EventType))
	}
	switch prepared.EventType {
	case EventTypeRecall:
		_ = m.warmth.RecordAccess(ctx, branch.ID, memory.AccessRetrieval, prepared.Title)
	case EventTypeValidation, EventTypeOutcomeRecorded, EventTypeReplayConsolidated:
		_ = m.warmth.RecordAccess(ctx, branch.ID, memory.AccessReinforcement, prepared.Title)
	}

	var labeledCount int64
	recordLabels := func(changed int64, err error) {
		if err != nil {
			if m.logger != nil {
				m.logger.Debug("forest: label examples failed", "error", err)
			}
			return
		}
		labeledCount += changed
	}
	switch prepared.EventType {
	case EventTypeOutcomeRecorded:
		status := OutcomeStatusMixed
		if raw, ok := prepared.Payload["status"].(OutcomeStatus); ok {
			status = raw
		} else if raw, ok := prepared.Payload["status"].(string); ok {
			status = OutcomeStatus(raw)
		}
		recordLabels(m.labelExamplesForOutcome(ctx, branch.ID, prepared.SessionID, status))
	case EventTypeValidation:
		recordLabels(m.labelExamplesForOutcome(ctx, branch.ID, prepared.SessionID, OutcomeStatusSucceeded))
	case EventTypeContradiction:
		recordLabels(m.labelExamplesForOutcome(ctx, branch.ID, prepared.SessionID, OutcomeStatusFailed))
	}

	m.scheduleSubstrateRefresh(prepared.SessionID)
	if !replayDue.IsZero() {
		m.scheduleReplayAt(replayDue)
	}
	if labeledCount > 0 {
		m.scheduleTraining()
	}
	return nil
}

func (m *MemoryForest) projectEventTx(ctx context.Context, tx *sql.Tx, event *Event) (*Branch, bool, time.Time, error) {
	if err := insertEventTx(ctx, tx, event); err != nil {
		return nil, false, time.Time{}, err
	}

	branch, err := getBranchTx(ctx, tx, event.BranchID)
	if err != nil {
		return nil, false, time.Time{}, err
	}
	created := branch == nil
	branch = applyEvent(branch, event)

	if err := upsertBranchTx(ctx, tx, branch); err != nil {
		return nil, false, time.Time{}, err
	}
	if err := upsertRelayEdgesTx(ctx, tx, event); err != nil {
		return nil, false, time.Time{}, err
	}
	if err := refreshCanopiesTx(ctx, tx, event.SessionID, event.TaskID, event.IntentID); err != nil {
		return nil, false, time.Time{}, err
	}
	replayDue, err := m.enqueueReplayTx(ctx, tx, branch, event)
	if err != nil {
		return nil, false, time.Time{}, err
	}
	if err := markSubstrateDirtyTx(ctx, tx, event.SessionID, event.Timestamp); err != nil {
		return nil, false, time.Time{}, err
	}
	return branch, created, replayDue, nil
}

func prepareEvent(event *Event) *Event {
	if event == nil {
		event = &Event{}
	}
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now().UTC()
	}
	event.Timestamp = now
	if event.SessionID == "" {
		event.SessionID = "global"
	}
	event.TaskID = normalizeForestTaskID(firstNonEmpty(event.TaskID, forestTaskIDFromPayload(event.Payload)))
	if event.Scope == "" {
		event.Scope = ScopeEpisodic
	}
	if event.Family == "" {
		event.Family = TreeFamilyEvidence
	}
	event.Confidence = defaultConfidence(event.Confidence)
	event.Salience = defaultSalience(event.Salience)
	if event.Summary == "" {
		event.Summary = summarizeText(event.Title, 240)
	}
	if event.Title == "" {
		event.Title = summarizeText(event.Summary, 72)
	}
	if event.IntentID == "" {
		event.IntentID = stableID("intent", event.SessionID, event.TaskID, normalizeText(firstNonEmpty(event.Title, event.Summary, event.BranchID)))
	}
	if event.RootID == "" {
		event.RootID = stableID("root", event.SessionID, event.TaskID, event.IntentID, string(event.Family))
	}
	if event.BranchID == "" {
		event.BranchID = stableID("branch", event.RootID, event.TaskID, normalizeText(firstNonEmpty(event.Title, event.Summary)), event.AgentType)
	}
	if event.ID == "" {
		event.ID = stableID(
			"event",
			event.SessionID,
			event.TaskID,
			event.BranchID,
			string(event.EventType),
			event.Timestamp.UTC().Format(time.RFC3339Nano),
			event.Title,
			event.ContentID,
		)
	}
	event.ProvenanceRefs = dedupeStrings(event.ProvenanceRefs)
	event.Supersedes = dedupeStrings(event.Supersedes)
	event.Contradicts = dedupeStrings(event.Contradicts)
	event.RelatedBranchIDs = dedupeStrings(event.RelatedBranchIDs)
	return event
}

func eventFromContent(entry *ctxpkg.ContentEntry) *Event {
	entry = entry.Clone()
	entry.HydrateForestMetadata()

	family := familyFromContentEntry(entry)
	scope := scopeFromContentEntry(entry, family)
	taskID := forestTaskIDFromStringMetadata(entry.Metadata)
	intentID := firstNonEmpty(entry.IntentID, stableID("intent", entry.SessionID, taskID, normalizeText(entry.Content)))
	rootID := stableID("root", firstNonEmpty(entry.SessionID, "global"), taskID, intentID, string(family))
	branchID := firstNonEmpty(entry.BranchID, stableID("branch", rootID, taskID, entry.ParentID, entry.AgentType, fmt.Sprintf("%d", entry.TurnNumber)))
	eventType := eventTypeFromContentType(entry.ContentType)

	return prepareEvent(&Event{
		SessionID:      entry.SessionID,
		TaskID:         taskID,
		AgentID:        entry.AgentID,
		AgentType:      entry.AgentType,
		EventType:      eventType,
		Family:         family,
		Scope:          scope,
		RootID:         rootID,
		BranchID:       branchID,
		ParentBranchID: entry.ParentID,
		IntentID:       intentID,
		ContentID:      entry.ID,
		Confidence:     defaultConfidence(entry.Confidence),
		Salience:       defaultSalience(entry.Salience),
		Timestamp:      entry.Timestamp,
		Title:          summarizeText(entry.Content, 72),
		Summary:        summarizeText(entry.Content, 240),
		ProvenanceRefs: dedupeStrings(entry.ProvenanceRefs),
		Supersedes:     dedupeStrings(entry.Supersedes),
		Contradicts:    dedupeStrings(entry.Contradicts),
		Payload: map[string]any{
			"content_type":   entry.ContentType,
			"keywords":       entry.Keywords,
			"entities":       entry.Entities,
			"related_files":  entry.RelatedFiles,
			"facet":          entry.Facet,
			"outcome_ref":    entry.OutcomeRef,
			"metadata":       entry.EffectiveMetadata(),
			"parent_content": entry.ParentID,
		},
	})
}

func eventTypeFromContentType(contentType ctxpkg.ContentType) EventType {
	switch contentType {
	case ctxpkg.ContentTypeDecision:
		return EventTypeDecisionRecorded
	case ctxpkg.ContentTypeOutcome:
		return EventTypeOutcomeRecorded
	case ctxpkg.ContentTypePreference:
		return EventTypePreferenceRecorded
	case ctxpkg.ContentTypeHypothesis:
		return EventTypeHypothesisRecorded
	default:
		return EventTypeContentIndexed
	}
}

func familyFromContentEntry(entry *ctxpkg.ContentEntry) TreeFamily {
	switch entry.ContentType {
	case ctxpkg.ContentTypeIntentSnapshot, ctxpkg.ContentTypeUserPrompt:
		return TreeFamilyIntent
	case ctxpkg.ContentTypeConstraint, ctxpkg.ContentTypeSuccessCriterion:
		return TreeFamilyConstraint
	case ctxpkg.ContentTypeDecision, ctxpkg.ContentTypePlanWorkflow:
		return TreeFamilyDecision
	case ctxpkg.ContentTypeOutcome:
		return TreeFamilyOutcome
	case ctxpkg.ContentTypePreference:
		return TreeFamilyPreference
	case ctxpkg.ContentTypeHypothesis:
		if strings.EqualFold(entry.Facet, string(TreeFamilyConflict)) {
			return TreeFamilyConflict
		}
		return TreeFamilyOpportunity
	case ctxpkg.ContentTypeBranchSummary:
		if entry.Facet != "" {
			return TreeFamily(entry.Facet)
		}
		return TreeFamilyEvidence
	}

	agentType := strings.ToLower(entry.AgentType)
	switch {
	case strings.HasPrefix(agentType, "scribe-"):
		return TreeFamilyEvidence
	case agentType == "guardian":
		return TreeFamilyConstraint
	case agentType == "designer":
		return TreeFamilyIntent
	case agentType == "engineer":
		return TreeFamilyCapability
	case agentType == "archivalist":
		return TreeFamilyOutcome
	case agentType == "academic", agentType == "librarian":
		return TreeFamilyEvidence
	default:
		return TreeFamilyEvidence
	}
}

func scopeFromContentEntry(entry *ctxpkg.ContentEntry, family TreeFamily) MemoryScope {
	if entry.Scope != "" {
		return MemoryScope(entry.Scope)
	}
	switch entry.ContentType {
	case ctxpkg.ContentTypeResearchPaper, ctxpkg.ContentTypeWebFetch, ctxpkg.ContentTypeCodeFile:
		if entry.SessionID == "" {
			return ScopeSemantic
		}
		return ScopeEpisodic
	case ctxpkg.ContentTypeBranchSummary:
		return ScopeSemantic
	}

	agentType := strings.ToLower(entry.AgentType)
	switch {
	case strings.HasPrefix(agentType, "scribe-"):
		return ScopeWorking
	case agentType == "guardian":
		return ScopeSemantic
	default:
		if family == TreeFamilyPreference || family == TreeFamilyCapability {
			return ScopeSemantic
		}
		return ScopeEpisodic
	}
}

func applyEvent(branch *Branch, event *Event) *Branch {
	now := event.Timestamp
	if branch == nil {
		branch = &Branch{
			ID:         event.BranchID,
			RootID:     event.RootID,
			ParentID:   event.ParentBranchID,
			Family:     event.Family,
			Scope:      event.Scope,
			State:      BranchStateActive,
			SessionID:  event.SessionID,
			TaskID:     event.TaskID,
			AgentID:    event.AgentID,
			AgentType:  event.AgentType,
			IntentID:   event.IntentID,
			Title:      event.Title,
			Summary:    event.Summary,
			Confidence: event.Confidence,
			Salience:   event.Salience,
			Utility:    0.5,
			CreatedAt:  now,
			UpdatedAt:  now,
			Metadata:   map[string]any{},
		}
	}

	branch.RootID = firstNonEmpty(branch.RootID, event.RootID)
	branch.ParentID = firstNonEmpty(branch.ParentID, event.ParentBranchID)
	branch.Family = nonEmptyFamily(branch.Family, event.Family)
	branch.Scope = nonEmptyScope(branch.Scope, event.Scope)
	branch.SessionID = firstNonEmpty(branch.SessionID, event.SessionID)
	branch.TaskID = firstNonEmpty(branch.TaskID, event.TaskID)
	branch.AgentID = firstNonEmpty(branch.AgentID, event.AgentID)
	branch.AgentType = firstNonEmpty(branch.AgentType, event.AgentType)
	branch.IntentID = firstNonEmpty(branch.IntentID, event.IntentID)
	branch.Title = chooseText(branch.Title, event.Title)
	branch.Summary = chooseText(branch.Summary, event.Summary)
	branch.Confidence = blend(branch.Confidence, event.Confidence, event.Salience)
	branch.Salience = mathMax(branch.Salience*0.85, event.Salience)
	branch.UpdatedAt = now

	switch event.EventType {
	case EventTypeDecisionRecorded:
		branch.SupportCount++
		branch.Utility = clamp01(branch.Utility + 0.05)
	case EventTypePreferenceRecorded:
		branch.SupportCount++
		branch.Utility = clamp01(branch.Utility + 0.03)
	case EventTypeOutcomeRecorded:
		branch.SupportCount++
		applyOutcomeImpact(branch, event)
	case EventTypeValidation:
		branch.SupportCount++
		branch.State = BranchStateValidated
		branch.Utility = clamp01(branch.Utility + 0.08)
	case EventTypeContradiction:
		branch.CounterCount++
		branch.State = BranchStateContradicted
	case EventTypeReplayPromoted, EventTypeReplayConsolidated:
		branch.Scope = ScopeSemantic
		branch.Utility = clamp01(branch.Utility + 0.04)
	case EventTypeEcologyPruned:
		branch.State = BranchStateDormant
	case EventTypeEcologyRegrown:
		branch.State = BranchStateActive
	default:
		branch.SupportCount++
	}

	if len(event.Supersedes) > 0 {
		branch.State = BranchStateSuperseded
	}
	if len(event.Contradicts) > 0 {
		branch.CounterCount += len(event.Contradicts)
	}
	branch.ConflictScore = computeConflictScore(branch)
	if branch.State == BranchStateActive && branch.ConflictScore > 0.75 {
		branch.State = BranchStateContradicted
	}
	branch.SuccessRate = computeSuccessRate(branch)

	if branch.Metadata == nil {
		branch.Metadata = make(map[string]any)
	}
	branch.Metadata["last_event_type"] = event.EventType
	branch.Metadata["last_event_id"] = event.ID
	branch.Metadata["last_payload"] = event.Payload
	return branch
}

func applyOutcomeImpact(branch *Branch, event *Event) {
	status, _ := event.Payload["status"].(OutcomeStatus)
	if status == "" {
		if raw, ok := event.Payload["status"].(string); ok {
			status = OutcomeStatus(raw)
		}
	}
	switch status {
	case OutcomeStatusSucceeded:
		branch.SuccessCount++
		branch.Utility = clamp01(branch.Utility + 0.15)
		branch.State = BranchStateValidated
	case OutcomeStatusFailed:
		branch.FailureCount++
		branch.CounterCount++
		branch.ScopeRisk = clamp01(branch.ScopeRisk + 0.1)
		branch.Utility = clamp01(branch.Utility - 0.12)
	case OutcomeStatusMixed:
		branch.SuccessCount++
		branch.FailureCount++
		branch.Utility = clamp01(branch.Utility + 0.02)
	}
}

func computeConflictScore(branch *Branch) float64 {
	total := branch.SupportCount + branch.CounterCount
	if total == 0 {
		return 0
	}
	return float64(branch.CounterCount) / float64(total)
}

func computeSuccessRate(branch *Branch) float64 {
	total := branch.SuccessCount + branch.FailureCount
	if total == 0 {
		return 0.5
	}
	return float64(branch.SuccessCount) / float64(total)
}

func insertEventTx(ctx context.Context, tx *sql.Tx, event *Event) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO forest_events
		(id, session_id, task_id, agent_id, agent_type, event_type, family, scope, root_id, branch_id,
		 parent_branch_id, intent_id, content_id, source_id, confidence, salience, timestamp,
		 title, summary, provenance_refs, supersedes, contradicts, related_branch_ids, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.SessionID, nullStringValue(event.TaskID), event.AgentID, event.AgentType, string(event.EventType), string(event.Family),
		string(event.Scope), event.RootID, event.BranchID, nullStringValue(event.ParentBranchID), nullStringValue(event.IntentID),
		nullStringValue(event.ContentID), nullStringValue(event.SourceID), event.Confidence, event.Salience,
		event.Timestamp.Unix(), event.Title, event.Summary, marshalJSON(event.ProvenanceRefs), marshalJSON(event.Supersedes),
		marshalJSON(event.Contradicts), marshalJSON(event.RelatedBranchIDs), marshalJSON(event.Payload))
	if err != nil {
		return fmt.Errorf("insert forest event: %w", err)
	}
	return nil
}

func getBranchTx(ctx context.Context, tx *sql.Tx, branchID string) (*Branch, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata
		FROM forest_branches
		WHERE id = ?
	`, branchID)
	return scanBranch(row)
}

func (m *MemoryForest) getBranch(ctx context.Context, branchID string) (*Branch, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata
		FROM forest_branches
		WHERE id = ?
	`, branchID)
	return scanBranch(row)
}

func scanBranch(row rowScanner) (*Branch, error) {
	var (
		branch         Branch
		parentID       sql.NullString
		taskID         sql.NullString
		agentID        sql.NullString
		agentType      sql.NullString
		intentID       sql.NullString
		lastAccessedAt int64
		createdAt      int64
		updatedAt      int64
		metadataRaw    sql.NullString
	)
	err := row.Scan(
		&branch.ID, &branch.RootID, &parentID, &branch.Family, &branch.Scope, &branch.State,
		&branch.SessionID, &taskID, &agentID, &agentType, &intentID, &branch.Title, &branch.Summary,
		&branch.Confidence, &branch.Salience, &branch.Utility, &branch.SuccessRate, &branch.ScopeRisk,
		&branch.ConflictScore, &branch.SupportCount, &branch.CounterCount, &branch.SuccessCount,
		&branch.FailureCount, &branch.AccessCount, &lastAccessedAt, &createdAt, &updatedAt, &metadataRaw,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan branch: %w", err)
	}
	branch.ParentID = parentID.String
	branch.TaskID = taskID.String
	branch.AgentID = agentID.String
	branch.AgentType = agentType.String
	branch.IntentID = intentID.String
	if lastAccessedAt > 0 {
		branch.LastAccessedAt = time.Unix(lastAccessedAt, 0).UTC()
	}
	branch.CreatedAt = time.Unix(createdAt, 0).UTC()
	branch.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if metadataRaw.Valid {
		_ = unmarshalJSON(metadataRaw.String, &branch.Metadata)
	}
	return &branch, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func upsertBranchTx(ctx context.Context, tx *sql.Tx, branch *Branch) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_branches
		(id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		 intent_id, title, summary, confidence, salience, utility, success_rate, scope_risk,
		 conflict_score, support_count, counter_count, success_count, failure_count,
		 access_count, last_accessed_at, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			root_id = excluded.root_id,
			parent_id = excluded.parent_id,
			family = excluded.family,
			scope = excluded.scope,
			state = excluded.state,
			session_id = excluded.session_id,
			task_id = excluded.task_id,
			agent_id = excluded.agent_id,
			agent_type = excluded.agent_type,
			intent_id = excluded.intent_id,
			title = excluded.title,
			summary = excluded.summary,
			confidence = excluded.confidence,
			salience = excluded.salience,
			utility = excluded.utility,
			success_rate = excluded.success_rate,
			scope_risk = excluded.scope_risk,
			conflict_score = excluded.conflict_score,
			support_count = excluded.support_count,
			counter_count = excluded.counter_count,
			success_count = excluded.success_count,
			failure_count = excluded.failure_count,
			access_count = excluded.access_count,
			last_accessed_at = excluded.last_accessed_at,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`, branch.ID, branch.RootID, nullStringValue(branch.ParentID), string(branch.Family), string(branch.Scope),
		string(branch.State), branch.SessionID, nullStringValue(branch.TaskID), nullStringValue(branch.AgentID), nullStringValue(branch.AgentType),
		nullStringValue(branch.IntentID), branch.Title, branch.Summary, branch.Confidence, branch.Salience, branch.Utility,
		branch.SuccessRate, branch.ScopeRisk, branch.ConflictScore, branch.SupportCount, branch.CounterCount,
		branch.SuccessCount, branch.FailureCount, branch.AccessCount, branch.LastAccessedAt.Unix(), branch.CreatedAt.Unix(),
		branch.UpdatedAt.Unix(), marshalJSON(branch.Metadata))
	if err != nil {
		return fmt.Errorf("upsert branch: %w", err)
	}
	return nil
}

func upsertRelayEdgesTx(ctx context.Context, tx *sql.Tx, event *Event) error {
	pairs := make([]relayUpdate, 0, len(event.RelatedBranchIDs)+len(event.Contradicts)+len(event.Supersedes)+1)
	if event.ParentBranchID != "" && event.ParentBranchID != event.BranchID {
		pairs = append(pairs, relayUpdate{event.BranchID, event.ParentBranchID, RelayRelationCoOccurs, 0.12})
	}
	for _, branchID := range event.RelatedBranchIDs {
		if branchID == event.BranchID {
			continue
		}
		pairs = append(pairs, relayUpdate{event.BranchID, branchID, RelayRelationSupports, 0.10})
	}
	for _, branchID := range event.Contradicts {
		pairs = append(pairs, relayUpdate{event.BranchID, branchID, RelayRelationContradicts, 0.18})
	}
	for _, branchID := range event.Supersedes {
		pairs = append(pairs, relayUpdate{event.BranchID, branchID, RelayRelationSupersedes, 0.16})
	}

	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_relay_edges
			(source_branch_id, target_branch_id, relation, weight, cofire_count, last_reinforced_at, metadata)
			VALUES (?, ?, ?, ?, 1, ?, '{}')
			ON CONFLICT(source_branch_id, target_branch_id, relation) DO UPDATE SET
				weight = MIN(2.0, forest_relay_edges.weight + excluded.weight),
				cofire_count = forest_relay_edges.cofire_count + 1,
				last_reinforced_at = excluded.last_reinforced_at
		`, pair.SourceID, pair.TargetID, string(pair.Relation), pair.Weight, event.Timestamp.Unix()); err != nil {
			return fmt.Errorf("upsert relay edge: %w", err)
		}
	}
	return nil
}

type relayUpdate struct {
	SourceID string
	TargetID string
	Relation RelayRelation
	Weight   float64
}

func refreshCanopiesTx(ctx context.Context, tx *sql.Tx, sessionID, taskID, intentID string) error {
	if sessionID == "" {
		sessionID = "global"
	}
	taskID = normalizeForestTaskID(taskID)
	sessionRoots, err := topRootsTx(ctx, tx, `
		SELECT root_id
		FROM forest_branches
		WHERE session_id = ? AND state != ?
		GROUP BY root_id
		ORDER BY MAX(salience) DESC, MAX(updated_at) DESC
		LIMIT 8
	`, sessionID, string(BranchStateDormant))
	if err != nil {
		return err
	}
	if err := upsertCanopyVariantsTx(ctx, tx, Canopy{
		SessionID: sessionID,
		Horizon:   CanopyHorizonSession,
		RootIDs:   sessionRoots,
		Summary:   summarizeText(strings.Join(sessionRoots, " "), 160),
		UpdatedAt: time.Now().UTC(),
	}, intentID); err != nil {
		return err
	}

	if taskID != "" {
		taskRoots, err := topRootsTx(ctx, tx, `
			SELECT root_id
			FROM forest_branches
			WHERE session_id = ? AND task_id = ? AND state != ?
			GROUP BY root_id
			ORDER BY MAX(salience) DESC, MAX(updated_at) DESC
			LIMIT 8
		`, sessionID, taskID, string(BranchStateDormant))
		if err != nil {
			return err
		}
		if err := upsertCanopyVariantsTx(ctx, tx, Canopy{
			SessionID: sessionID,
			TaskID:    taskID,
			Horizon:   CanopyHorizonTask,
			RootIDs:   taskRoots,
			Summary:   summarizeText(strings.Join(taskRoots, " "), 160),
			UpdatedAt: time.Now().UTC(),
		}, intentID); err != nil {
			return err
		}
	}

	projectRoots, err := topRootsTx(ctx, tx, `
		SELECT root_id
		FROM forest_branches
		WHERE state != ?
		GROUP BY root_id
		ORDER BY MAX(utility) DESC, MAX(updated_at) DESC
		LIMIT 8
	`, string(BranchStateDormant))
	if err != nil {
		return err
	}
	return upsertCanopyTx(ctx, tx, Canopy{
		Horizon:   CanopyHorizonProject,
		RootIDs:   projectRoots,
		Summary:   summarizeText(strings.Join(projectRoots, " "), 160),
		UpdatedAt: time.Now().UTC(),
	})
}

func upsertCanopyVariantsTx(ctx context.Context, tx *sql.Tx, canopy Canopy, intentID string) error {
	canopy.IntentID = ""
	canopy.Key = canopyKey(canopy.Horizon, canopy.SessionID, canopy.TaskID, "")
	if err := upsertCanopyTx(ctx, tx, canopy); err != nil {
		return err
	}
	intentID = strings.TrimSpace(intentID)
	if intentID == "" {
		return nil
	}
	canopy.IntentID = intentID
	canopy.Key = canopyKey(canopy.Horizon, canopy.SessionID, canopy.TaskID, intentID)
	return upsertCanopyTx(ctx, tx, canopy)
}

func topRootsTx(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query canopy roots: %w", err)
	}
	defer rows.Close()

	var roots []string
	for rows.Next() {
		var rootID string
		if err := rows.Scan(&rootID); err != nil {
			return nil, fmt.Errorf("scan canopy root: %w", err)
		}
		roots = append(roots, rootID)
	}
	return roots, rows.Err()
}

func upsertCanopyTx(ctx context.Context, tx *sql.Tx, canopy Canopy) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_canopies (canopy_key, session_id, task_id, intent_id, horizon, root_ids, summary, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(canopy_key) DO UPDATE SET
			root_ids = excluded.root_ids,
			summary = excluded.summary,
			updated_at = excluded.updated_at
	`, canopy.Key, nullStringValue(canopy.SessionID), nullStringValue(canopy.TaskID), nullStringValue(canopy.IntentID), string(canopy.Horizon),
		marshalJSON(canopy.RootIDs), canopy.Summary, canopy.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("upsert canopy: %w", err)
	}
	return nil
}

func markSubstrateDirtyTx(ctx context.Context, tx *sql.Tx, sessionID string, at time.Time) error {
	sessionID = normalizeForestSessionID(sessionID)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_substrate_sessions
		(session_id, graph_version, substrate_version, dirty, dirty_at, updated_at)
		VALUES (?, 1, 0, 1, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			graph_version = forest_substrate_sessions.graph_version + 1,
			dirty = 1,
			dirty_at = excluded.dirty_at,
			updated_at = excluded.updated_at
	`, sessionID, at.Unix(), at.Unix())
	if err != nil {
		return fmt.Errorf("mark substrate dirty: %w", err)
	}
	return nil
}

func (m *MemoryForest) enqueueReplayTx(ctx context.Context, tx *sql.Tx, branch *Branch, event *Event) (time.Time, error) {
	priority := replayPriority(branch, event)
	if priority < 0.45 {
		return time.Time{}, nil
	}
	availableAt := event.Timestamp.Add(m.replayDelay).UTC()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_replay_queue
		(branch_id, root_id, priority, reason, state, available_at, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, branch.ID, branch.RootID, priority, string(event.EventType), string(ReplayStateQueued),
		availableAt.Unix(), marshalJSON(event.Payload))
	if err != nil {
		return time.Time{}, fmt.Errorf("enqueue replay: %w", err)
	}
	return availableAt, nil
}

func replayPriority(branch *Branch, event *Event) float64 {
	priority := (branch.Salience * 0.35) + (branch.Confidence * 0.25) + (branch.Utility * 0.20) + (1-branch.ConflictScore)*0.20
	if branch != nil && branch.Metadata != nil {
		priority += 0.10 * metadataFloat(branch.Metadata, "substrate_frontier")
		priority -= 0.08 * metadataFloat(branch.Metadata, "substrate_inhibition")
	}
	switch {
	case strings.EqualFold(branch.AgentType, "guardian"):
		priority += 0.15
	case strings.HasPrefix(strings.ToLower(branch.AgentType), "scribe-"):
		priority += 0.10
	}
	switch event.EventType {
	case EventTypeOutcomeRecorded, EventTypeContradiction, EventTypeValidation:
		priority += 0.10
	}
	return clamp01(priority)
}

func (m *MemoryForest) RunReplay(ctx context.Context, limit int) (ReplayResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()

	result, _, err := m.runReplay(ctx, limit)
	return result, err
}

func (m *MemoryForest) runReplay(ctx context.Context, limit int) (ReplayResult, []string, error) {
	if limit <= 0 {
		limit = 8
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT id, branch_id, root_id, priority, reason, payload
		FROM forest_replay_queue
		WHERE state = ? AND available_at <= ?
		ORDER BY priority DESC, available_at ASC
		LIMIT ?
	`, string(ReplayStateQueued), time.Now().UTC().Unix(), limit)
	if err != nil {
		return ReplayResult{}, nil, fmt.Errorf("query replay queue: %w", err)
	}

	var items []replayItem
	for rows.Next() {
		var item replayItem
		if err := rows.Scan(&item.ID, &item.BranchID, &item.RootID, &item.Priority, &item.Reason, &item.Payload); err != nil {
			_ = rows.Close()
			return ReplayResult{}, nil, fmt.Errorf("scan replay queue: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return ReplayResult{}, nil, err
	}
	if err := rows.Err(); err != nil {
		return ReplayResult{}, nil, err
	}

	var (
		result          ReplayResult
		touchedSessions []string
	)
	for _, item := range items {
		result.Processed++
		promoted, sessionID, err := m.replayBranch(ctx, item.ID, item.BranchID, item.RootID, item.Priority, item.Reason, item.Payload.String)
		if err != nil {
			return result, touchedSessions, err
		}
		if promoted {
			result.Promoted++
		}
		if sessionID != "" {
			touchedSessions = append(touchedSessions, sessionID)
		}
	}
	return result, dedupeStrings(touchedSessions), nil
}

type replayItem struct {
	ID       int64
	BranchID string
	RootID   string
	Priority float64
	Reason   string
	Payload  sql.NullString
}

func (m *MemoryForest) replayBranch(ctx context.Context, queueID int64, branchID, rootID string, priority float64, reason, payload string) (bool, string, error) {
	branch, err := m.getBranch(ctx, branchID)
	if err != nil {
		return false, "", err
	}
	if branch == nil {
		_, _ = m.db.ExecContext(ctx, `UPDATE forest_replay_queue SET state = ? WHERE id = ?`, string(ReplayStateComplete), queueID)
		return false, "", nil
	}

	_, err = m.db.ExecContext(ctx, `UPDATE forest_replay_queue SET state = ?, attempts = attempts + 1 WHERE id = ?`, string(ReplayStateRunning), queueID)
	if err != nil {
		return false, "", fmt.Errorf("mark replay running: %w", err)
	}

	promoted := false
	if branch.Scope == ScopeEpisodic || branch.Scope == ScopeWorking {
		now := time.Now().UTC()
		semanticID := stableID("semantic", branch.RootID, branch.TaskID, normalizeText(branch.Title))
		semanticBranch := &Branch{
			ID:             semanticID,
			RootID:         branch.RootID,
			ParentID:       branch.ID,
			Family:         branch.Family,
			Scope:          ScopeSemantic,
			State:          BranchStateActive,
			SessionID:      branch.SessionID,
			TaskID:         branch.TaskID,
			AgentID:        branch.AgentID,
			AgentType:      branch.AgentType,
			IntentID:       firstNonEmpty(branch.IntentID, rootID),
			Title:          branch.Title,
			Summary:        summarizeText(branch.Summary, 240),
			Confidence:     clamp01((branch.Confidence + priority) / 2),
			Salience:       clamp01(branch.Salience),
			Utility:        clamp01((branch.Utility + priority) / 2),
			SuccessRate:    branch.SuccessRate,
			ScopeRisk:      branch.ScopeRisk,
			ConflictScore:  branch.ConflictScore,
			SupportCount:   branch.SupportCount,
			CounterCount:   branch.CounterCount,
			SuccessCount:   branch.SuccessCount,
			FailureCount:   branch.FailureCount,
			AccessCount:    branch.AccessCount,
			LastAccessedAt: branch.LastAccessedAt,
			CreatedAt:      now,
			UpdatedAt:      now,
			Metadata: map[string]any{
				"replay_reason":  reason,
				"replay_payload": payload,
				"source_branch":  branch.ID,
			},
		}

		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return false, "", fmt.Errorf("begin replay tx: %w", err)
		}
		defer tx.Rollback()

		if err := upsertBranchTx(ctx, tx, semanticBranch); err != nil {
			return false, "", err
		}
		if err := upsertRelayEdgesTx(ctx, tx, &Event{
			BranchID:         semanticBranch.ID,
			ParentBranchID:   branch.ID,
			RelatedBranchIDs: []string{branch.ID},
			Timestamp:        now,
		}); err != nil {
			return false, "", err
		}
		if err := refreshCanopiesTx(ctx, tx, branch.SessionID, branch.TaskID, semanticBranch.IntentID); err != nil {
			return false, "", err
		}
		if err := markSubstrateDirtyTx(ctx, tx, branch.SessionID, now); err != nil {
			return false, "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE forest_replay_queue SET state = ? WHERE id = ?`, string(ReplayStateComplete), queueID); err != nil {
			return false, "", err
		}
		if err := tx.Commit(); err != nil {
			return false, "", fmt.Errorf("commit replay tx: %w", err)
		}
		_ = m.warmth.RecordAccess(ctx, semanticBranch.ID, memory.AccessReinforcement, "replay")
		promoted = true
		return promoted, branch.SessionID, nil
	}

	_, err = m.db.ExecContext(ctx, `UPDATE forest_replay_queue SET state = ? WHERE id = ?`, string(ReplayStateComplete), queueID)
	if err != nil {
		return false, "", fmt.Errorf("complete replay queue item: %w", err)
	}
	return false, "", nil
}

func (m *MemoryForest) RunEcology(ctx context.Context) (EcologyResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()

	now := time.Now().UTC()
	result := EcologyResult{}

	dormantRows, err := m.db.QueryContext(ctx, `
		SELECT id, updated_at, access_count
		FROM forest_branches
		WHERE state NOT IN (?, ?) AND scope != ?
	`, string(BranchStateDormant), string(BranchStateSuperseded), string(ScopeSemantic))
	if err != nil {
		return result, fmt.Errorf("query ecology candidates: %w", err)
	}
	defer dormantRows.Close()

	for dormantRows.Next() {
		var (
			branchID    string
			updatedAt   int64
			accessCount int
		)
		if err := dormantRows.Scan(&branchID, &updatedAt, &accessCount); err != nil {
			return result, fmt.Errorf("scan ecology candidate: %w", err)
		}
		updated := time.Unix(updatedAt, 0).UTC()
		if accessCount == 0 && now.Sub(updated) > 7*24*time.Hour {
			if _, err := m.db.ExecContext(ctx, `
				UPDATE forest_branches
				SET state = ?, scope = ?
				WHERE id = ?
			`, string(BranchStateDormant), string(ScopeDormant), branchID); err != nil {
				return result, fmt.Errorf("mark dormant: %w", err)
			}
			result.Dormant++
		}
	}
	if err := dormantRows.Err(); err != nil {
		return result, err
	}

	_, err = m.db.ExecContext(ctx, `
		UPDATE forest_branches
		SET state = ?, scope = ?
		WHERE state = ? AND access_count > 0 AND updated_at >= ?
	`, string(BranchStateActive), string(ScopeEpisodic), string(BranchStateDormant), now.Add(-24*time.Hour).Unix())
	if err != nil {
		return result, fmt.Errorf("regrow dormant branches: %w", err)
	}

	row := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_branches
		WHERE state = ? AND access_count > 0 AND updated_at >= ?
	`, string(BranchStateActive), now.Add(-24*time.Hour).Unix())
	_ = row.Scan(&result.Regrown)

	return result, nil
}

func chooseText(current, candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return current
	}
	if strings.TrimSpace(current) == "" || len(candidate) > len(current) {
		return candidate
	}
	return current
}

func blend(current, candidate, salience float64) float64 {
	if current == 0 {
		return candidate
	}
	weight := clamp01(salience)
	if weight == 0 {
		weight = 0.5
	}
	return (current * (1 - weight)) + (candidate * weight)
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nonEmptyFamily(current, candidate TreeFamily) TreeFamily {
	if current != "" {
		return current
	}
	return candidate
}

func nonEmptyScope(current, candidate MemoryScope) MemoryScope {
	if current != "" {
		return current
	}
	return candidate
}

func nullStringValue(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func forestTaskIDFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if taskID := forestTaskIDFromAny(payload["task_id"]); taskID != "" {
		return taskID
	}
	if metadata, ok := payload["metadata"].(map[string]any); ok {
		return forestTaskIDFromAnyMetadata(metadata)
	}
	return ""
}
