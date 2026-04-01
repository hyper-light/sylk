package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/knowledge/memory"
)

// Retrieve returns ranked branch packets for the supplied query.
func (m *MemoryForest) Retrieve(ctx context.Context, query Query) ([]*BranchPacket, error) {
	query = normalizeQuery(query)

	var (
		branches         []*Branch
		queryScores      map[string]float64
		evidenceByBranch map[string][]PacketEvidence
		canopy           *Canopy
		loadErr          error
		mu               sync.Mutex
		wg               sync.WaitGroup
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		loadedBranches, loadedScores, err := m.loadCandidateBranches(ctx, query)
		mu.Lock()
		defer mu.Unlock()
		if err != nil && loadErr == nil {
			loadErr = err
			return
		}
		branches = loadedBranches
		queryScores = loadedScores
	}()
	go func() {
		defer wg.Done()
		loadedEvidence, err := m.searchEvidence(ctx, query)
		mu.Lock()
		defer mu.Unlock()
		if err != nil && loadErr == nil {
			loadErr = err
			return
		}
		evidenceByBranch = loadedEvidence
	}()
	go func() {
		defer wg.Done()
		loadedCanopy, err := m.resolveCanopy(ctx, query)
		mu.Lock()
		defer mu.Unlock()
		if err != nil && loadErr == nil {
			loadErr = err
			return
		}
		canopy = loadedCanopy
	}()
	wg.Wait()
	if loadErr != nil {
		return nil, loadErr
	}

	if len(evidenceByBranch) > 0 {
		byID := make(map[string]*Branch, len(branches))
		for _, branch := range branches {
			byID[branch.ID] = branch
		}
		var missing []string
		for branchID := range evidenceByBranch {
			if _, ok := byID[branchID]; !ok {
				missing = append(missing, branchID)
			}
		}
		if len(missing) > 0 {
			extra, err := m.loadBranchesByID(ctx, missing)
			if err != nil {
				return nil, err
			}
			branches = append(branches, extra...)
		}
	}

	if len(branches) == 0 {
		return nil, nil
	}

	branchIDs := make([]string, 0, len(branches))
	for _, branch := range branches {
		branchIDs = append(branchIDs, branch.ID)
	}
	relayMass, err := m.loadRelayMass(ctx, branchIDs)
	if err != nil {
		return nil, err
	}
	depths := computeBranchDepths(branches)

	canopyRoots := make(map[string]struct{}, len(canopy.RootIDs))
	for _, rootID := range canopy.RootIDs {
		canopyRoots[rootID] = struct{}{}
	}
	structuralSubstrate, err := m.loadSubstrateSignals(ctx, branches)
	if err != nil {
		return nil, err
	}

	keys := make([]branchKey, 0, len(branches))
	for _, branch := range branches {
		keys = append(keys, branchKey{ID: branch.ID, Family: branch.Family})
	}
	warmth, err := m.warmth.BatchActivation(ctx, keys, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	packets := make([]*BranchPacket, 0, len(branches))
	inputs := make([]scoreInput, 0, len(branches))
	packetBranches := make([]*Branch, 0, len(branches))
	featureByBranch := make(map[string][]float32, len(branches))
	for _, branch := range branches {
		support, counter, err := m.loadBranchEvidencePacket(ctx, branch.ID, 4, query.IncludeCounterEvidence)
		if err != nil {
			return nil, err
		}
		if extra := evidenceByBranch[branch.ID]; len(extra) > 0 {
			support = mergeEvidence(extra, support)
		}
		canopyScore := 0.0
		if _, ok := canopyRoots[branch.RootID]; ok {
			canopyScore = 1.0
		}
		evidenceScore := evidenceSignal(support)
		structural := structuralSubstrate[branch.ID]
		substrate := retrievalSubstrateSignal(branch, structural, queryScores[branch.ID], canopyRoots)
		inputs = append(inputs, scoreInput{
			QueryMatch:       queryScores[branch.ID],
			Evidence:         evidenceScore,
			Canopy:           canopyScore,
			Substrate:        substrate.Potential,
			Frontier:         substrate.Frontier,
			Confidence:       branch.Confidence,
			Recency:          branchRecency(branch, now),
			Warmth:           warmth[branch.ID],
			Utility:          clamp01((branch.Utility + branch.SuccessRate) / 2),
			Salience:         branch.Salience,
			ConflictSafety:   1 - branch.ConflictScore,
			ScopeSafety:      1 - branch.ScopeRisk,
			InhibitionSafety: 1 - substrate.Inhibition,
		})
		packetBranches = append(packetBranches, branch)
		packets = append(packets, &BranchPacket{
			Branch:          branch,
			Support:         support,
			CounterEvidence: counter,
			Conflicts:       buildConflicts(branch, counter),
			NextActions:     buildNextActions(branch),
		})
	}

	scores := scoreBatch(inputs, now, packetBranches)
	for i := range packets {
		packets[i].Score = scores[i]
		featureByBranch[packetBranches[i].ID] = buildFeatureVector(
			query,
			packetBranches[i],
			inputs[i],
			packets[i].Support,
			packets[i].CounterEvidence,
			scores[i].Base,
			relayMass[packetBranches[i].ID],
			depths[packetBranches[i].ID],
			retrievalSubstrateSignal(packetBranches[i], structuralSubstrate[packetBranches[i].ID], queryScores[packetBranches[i].ID], canopyRoots),
		)
	}
	m.applyLearnedPredictions(query, packets, featureByBranch)
	sortPackets(packets)

	if err := m.recordRetrievalExamples(ctx, query, packets, featureByBranch); err != nil && m.logger != nil {
		m.logger.Debug("forest: record retrieval examples failed", "error", err)
	}
	if len(packets) > query.Limit {
		packets = packets[:query.Limit]
	}

	for _, packet := range packets {
		_ = m.warmth.RecordAccess(ctx, packet.Branch.ID, memory.AccessRetrieval, query.Query)
	}

	return packets, nil
}

// ResolveIntent returns the active intent frontier for the caller.
func (m *MemoryForest) ResolveIntent(ctx context.Context, input ResolveIntentInput) (*IntentResolution, error) {
	canopy, err := m.resolveCanopy(ctx, Query{
		Query:     input.Query,
		SessionID: input.SessionID,
		IntentID:  input.IntentID,
		Horizon:   input.Horizon,
		Limit:     input.Limit,
	})
	if err != nil {
		return nil, err
	}

	packets, err := m.Retrieve(ctx, Query{
		Query:     input.Query,
		SessionID: input.SessionID,
		AgentID:   input.AgentID,
		AgentType: input.AgentType,
		IntentID:  input.IntentID,
		Horizon:   input.Horizon,
		Limit:     maxInt(input.Limit, 8),
		Families: []TreeFamily{
			TreeFamilyIntent,
			TreeFamilyConstraint,
			TreeFamilyPreference,
			TreeFamilyOutcome,
		},
		IncludeCounterEvidence: true,
	})
	if err != nil {
		return nil, err
	}

	resolution := &IntentResolution{
		Query:       input.Query,
		ActiveRoots: canopy.RootIDs,
	}
	for _, packet := range packets {
		switch packet.Branch.Family {
		case TreeFamilyIntent:
			resolution.IntentBranches = append(resolution.IntentBranches, *packet)
			if resolution.PrimaryIntent == "" {
				resolution.PrimaryIntent = packet.Branch.Summary
			}
		case TreeFamilyConstraint:
			resolution.Constraints = append(resolution.Constraints, *packet)
		case TreeFamilyPreference:
			resolution.Preferences = append(resolution.Preferences, *packet)
		case TreeFamilyOutcome:
			resolution.OutcomeHints = append(resolution.OutcomeHints, *packet)
		}
	}
	return resolution, nil
}

// PredictNextBranches retrieves low-risk adjacent-value branches.
func (m *MemoryForest) PredictNextBranches(ctx context.Context, query Query) ([]*BranchPacket, error) {
	query = normalizeQuery(query)
	query.Families = []TreeFamily{
		TreeFamilyOpportunity,
		TreeFamilyCapability,
		TreeFamilyOutcome,
		TreeFamilyDecision,
	}
	query.IncludeCounterEvidence = true
	return m.Retrieve(ctx, query)
}

func normalizeQuery(query Query) Query {
	if query.Limit <= 0 {
		query.Limit = 8
	}
	if query.Horizon == "" {
		if query.SessionID != "" {
			query.Horizon = CanopyHorizonSession
		} else {
			query.Horizon = CanopyHorizonProject
		}
	}
	if len(query.Families) == 0 {
		query.Families = defaultFamilies()
	}
	return query
}

func (m *MemoryForest) loadCandidateBranches(ctx context.Context, query Query) ([]*Branch, map[string]float64, error) {
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		branchMap   = make(map[string]*Branch)
		queryScores = make(map[string]float64)
		firstErr    error
	)

	load := func(sessionID string, semanticOnly bool) {
		defer wg.Done()
		rows, err := m.queryBranches(ctx, sessionID, semanticOnly, query.Families, 128)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		for _, branch := range rows {
			score := lexicalScore(query.Query, branch.Title+" "+branch.Summary)
			if query.Query != "" && score == 0 && len(branchMap) > 64 {
				continue
			}
			if existing, ok := branchMap[branch.ID]; ok {
				if existing.UpdatedAt.After(branch.UpdatedAt) {
					continue
				}
			}
			branchMap[branch.ID] = branch
			queryScores[branch.ID] = score
		}
	}

	if query.SessionID != "" {
		wg.Add(1)
		go load(query.SessionID, false)
	}
	wg.Add(1)
	go load("", true)
	wg.Wait()
	if firstErr != nil {
		return nil, nil, firstErr
	}

	branches := make([]*Branch, 0, len(branchMap))
	for _, branch := range branchMap {
		branches = append(branches, branch)
	}
	return branches, queryScores, nil
}

func (m *MemoryForest) queryBranches(ctx context.Context, sessionID string, semanticOnly bool, families []TreeFamily, limit int) ([]*Branch, error) {
	query := `
		SELECT id, root_id, parent_id, family, scope, state, session_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata
		FROM forest_branches
		WHERE state != ?
	`
	args := []any{string(BranchStateSuperseded)}

	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if semanticOnly {
		query += " AND scope = ?"
		args = append(args, string(ScopeSemantic))
	}
	if len(families) > 0 {
		placeholders := make([]string, 0, len(families))
		for _, family := range families {
			placeholders = append(placeholders, "?")
			args = append(args, string(family))
		}
		query += " AND family IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY salience DESC, updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query branches: %w", err)
	}
	defer rows.Close()

	var branches []*Branch
	for rows.Next() {
		branch, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		if branch != nil {
			branches = append(branches, branch)
		}
	}
	return branches, rows.Err()
}

func (m *MemoryForest) loadBranchesByID(ctx context.Context, ids []string) ([]*Branch, error) {
	ids = dedupeStrings(ids)
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, root_id, parent_id, family, scope, state, session_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata
		FROM forest_branches
		WHERE id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query branches by id: %w", err)
	}
	defer rows.Close()

	var branches []*Branch
	for rows.Next() {
		branch, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		if branch != nil {
			branches = append(branches, branch)
		}
	}
	return branches, rows.Err()
}

func (m *MemoryForest) resolveCanopy(ctx context.Context, query Query) (*Canopy, error) {
	key := canopyKey(query.Horizon, query.SessionID, query.IntentID)
	row := m.db.QueryRowContext(ctx, `
		SELECT canopy_key, session_id, intent_id, horizon, root_ids, summary, updated_at
		FROM forest_canopies
		WHERE canopy_key = ?
	`, key)

	var (
		canopy    Canopy
		sessionID sql.NullString
		intentID  sql.NullString
		rootIDs   string
		updatedAt int64
	)
	err := row.Scan(&canopy.Key, &sessionID, &intentID, &canopy.Horizon, &rootIDs, &canopy.Summary, &updatedAt)
	if err == sql.ErrNoRows {
		if query.SessionID == "" && query.Horizon != CanopyHorizonProject {
			query.Horizon = CanopyHorizonProject
			return m.resolveCanopy(ctx, query)
		}
		return &Canopy{Key: key, Horizon: query.Horizon, UpdatedAt: time.Now().UTC()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load canopy: %w", err)
	}
	canopy.SessionID = sessionID.String
	canopy.IntentID = intentID.String
	canopy.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	_ = unmarshalJSON(rootIDs, &canopy.RootIDs)
	return &canopy, nil
}

func (m *MemoryForest) searchEvidence(ctx context.Context, query Query) (map[string][]PacketEvidence, error) {
	if strings.TrimSpace(query.Query) == "" {
		return map[string][]PacketEvidence{}, nil
	}

	results := make(map[string]*ctxpkg.ContentEntry)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	if m.contentStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			filters := &ctxpkg.SearchFilters{}
			if query.SessionID != "" {
				filters.SessionID = query.SessionID
			}
			entries, err := m.contentStore.Search(query.Query, filters, query.Limit*4)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for _, entry := range entries {
				results[entry.ID] = entry
			}
		}()
	}

	if m.searcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			search := m.searcher.SearchWithBudget(ctx, query.Query, ctxpkg.TierFullBudget)
			mu.Lock()
			defer mu.Unlock()
			for _, entry := range search.Results {
				results[entry.ID] = entry
			}
		}()
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if len(results) == 0 {
		return map[string][]PacketEvidence{}, nil
	}

	contentIDs := make([]string, 0, len(results))
	for id := range results {
		contentIDs = append(contentIDs, id)
	}
	contentToBranch, err := m.findBranchIDsForContent(ctx, contentIDs)
	if err != nil {
		return nil, err
	}

	byBranch := make(map[string][]PacketEvidence)
	for contentID, entry := range results {
		branchIDs := contentToBranch[contentID]
		for _, branchID := range branchIDs {
			byBranch[branchID] = append(byBranch[branchID], PacketEvidence{
				ContentID:      entry.ID,
				ContentType:    string(entry.ContentType),
				Summary:        summarizeText(entry.Content, 220),
				Confidence:     defaultConfidence(entry.Confidence),
				Salience:       defaultSalience(entry.Salience),
				ProvenanceRefs: dedupeStrings(entry.ProvenanceRefs),
				Timestamp:      entry.Timestamp,
			})
		}
	}
	return byBranch, nil
}

func (m *MemoryForest) findBranchIDsForContent(ctx context.Context, contentIDs []string) (map[string][]string, error) {
	contentIDs = dedupeStrings(contentIDs)
	if len(contentIDs) == 0 {
		return map[string][]string{}, nil
	}

	placeholders := make([]string, 0, len(contentIDs))
	args := make([]any, 0, len(contentIDs))
	for _, contentID := range contentIDs {
		placeholders = append(placeholders, "?")
		args = append(args, contentID)
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT content_id, branch_id
		FROM forest_events
		WHERE content_id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query forest events by content: %w", err)
	}
	defer rows.Close()

	byContent := make(map[string][]string)
	for rows.Next() {
		var contentID, branchID string
		if err := rows.Scan(&contentID, &branchID); err != nil {
			return nil, fmt.Errorf("scan event content lookup: %w", err)
		}
		byContent[contentID] = append(byContent[contentID], branchID)
	}
	return byContent, rows.Err()
}

func (m *MemoryForest) loadBranchEvidencePacket(ctx context.Context, branchID string, limit int, includeCounter bool) ([]PacketEvidence, []PacketEvidence, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT content_id, title, summary, confidence, salience, timestamp, provenance_refs, contradicts, payload
		FROM forest_events
		WHERE branch_id = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, branchID, limit*3)
	if err != nil {
		return nil, nil, fmt.Errorf("query branch evidence: %w", err)
	}
	defer rows.Close()

	support := make([]PacketEvidence, 0, limit)
	counter := make([]PacketEvidence, 0, limit)
	for rows.Next() {
		var (
			contentID      sql.NullString
			title          sql.NullString
			summary        sql.NullString
			confidence     float64
			salience       float64
			timestamp      int64
			provenanceRaw  sql.NullString
			contradictsRaw sql.NullString
			payloadRaw     sql.NullString
		)
		if err := rows.Scan(&contentID, &title, &summary, &confidence, &salience, &timestamp, &provenanceRaw, &contradictsRaw, &payloadRaw); err != nil {
			return nil, nil, fmt.Errorf("scan branch evidence: %w", err)
		}

		evidence := PacketEvidence{
			ContentID:  contentID.String,
			Summary:    chooseText(summary.String, title.String),
			Confidence: confidence,
			Salience:   salience,
			Timestamp:  time.Unix(timestamp, 0).UTC(),
		}
		_ = unmarshalJSON(provenanceRaw.String, &evidence.ProvenanceRefs)
		var payload map[string]any
		_ = unmarshalJSON(payloadRaw.String, &payload)
		if raw, ok := payload["content_type"].(string); ok {
			evidence.ContentType = raw
		}

		var contradicts []string
		_ = unmarshalJSON(contradictsRaw.String, &contradicts)
		if len(contradicts) > 0 && includeCounter {
			if len(counter) < limit {
				counter = append(counter, evidence)
			}
			continue
		}
		if len(support) < limit {
			support = append(support, evidence)
		}
	}
	return support, counter, rows.Err()
}

func mergeEvidence(primary, secondary []PacketEvidence) []PacketEvidence {
	if len(primary) == 0 {
		return secondary
	}
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	result := make([]PacketEvidence, 0, len(primary)+len(secondary))
	for _, evidence := range append(primary, secondary...) {
		key := firstNonEmpty(evidence.ContentID, evidence.Summary)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, evidence)
	}
	return result
}

func evidenceSignal(evidence []PacketEvidence) float64 {
	if len(evidence) == 0 {
		return 0
	}
	var total float64
	for _, item := range evidence {
		total += clamp01((item.Confidence + item.Salience) / 2)
	}
	return clamp01(total / float64(len(evidence)))
}

func buildConflicts(branch *Branch, counter []PacketEvidence) []PacketConflict {
	conflicts := make([]PacketConflict, 0, len(counter)+1)
	if branch.ConflictScore > 0.25 {
		conflicts = append(conflicts, PacketConflict{
			Summary:  "branch has unresolved contradictory evidence",
			Severity: branch.ConflictScore,
		})
	}
	for _, item := range counter {
		conflicts = append(conflicts, PacketConflict{
			Summary:  item.Summary,
			Severity: clamp01((item.Confidence + item.Salience) / 2),
		})
	}
	return conflicts
}

func buildNextActions(branch *Branch) []PacketAction {
	switch branch.Family {
	case TreeFamilyIntent:
		return []PacketAction{{Label: "refine", Description: "refine the active intent into stronger constraints or subgoals"}}
	case TreeFamilyConstraint:
		return []PacketAction{{Label: "respect", Description: "use this branch as a hard constraint during planning and execution"}}
	case TreeFamilyEvidence:
		return []PacketAction{{Label: "ground", Description: "use this evidence to justify or challenge the current branch"}}
	case TreeFamilyDecision:
		return []PacketAction{{Label: "compare", Description: "compare this decision against alternatives and record the outcome"}}
	case TreeFamilyOutcome:
		return []PacketAction{{Label: "learn", Description: "feed this outcome back into capability and preference priors"}}
	case TreeFamilyPreference:
		return []PacketAction{{Label: "adapt", Description: "adjust tone, scope, or tradeoffs to match this preference"}}
	case TreeFamilyCapability:
		return []PacketAction{{Label: "route", Description: "choose the agent, tool, or workflow path that best matches this precedent"}}
	case TreeFamilyOpportunity:
		return []PacketAction{{Label: "propose", Description: "offer this as a safe surplus-quality upgrade if scope risk stays low"}}
	case TreeFamilyConflict:
		return []PacketAction{{Label: "resolve", Description: "surface the contradiction and gather evidence before proceeding"}}
	default:
		return nil
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
