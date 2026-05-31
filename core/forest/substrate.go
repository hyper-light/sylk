package forest

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/viterin/vek/vek32"
)

type substrateSignal struct {
	Potential       float64
	Frontier        float64
	Inhibition      float64
	ConductanceMass float64
}

type substrateEdge struct {
	SourceID    string
	TargetID    string
	Conductance float64
	Flux        float64
	Redundancy  float64
	Inhibition  float64
}

const structuralSubstrateAgentType = "structural"

func substrateContextKey(sessionID string) string {
	return stableID("substrate", normalizeForestSessionID(sessionID))
}

// loadSubstrateSignals dispatches by SubstrateMode:
//
//   - SubstrateModeFull (default) reads the projector-maintained
//     forest_substrate_state via loadStoredSubstrateState — the
//     pre-Issue#7 path, unchanged.
//   - SubstrateModePageRank computes (or reuses cached) personalized
//     PageRank over the relay graph.
//   - SubstrateModeWarmthOnly returns an empty map so scoring falls
//     back to the other 11 components (no substrate / frontier
//     contribution).
//
// Empty / unrecognized modes route to the default path so a typo or
// pre-Issue#7 retrieval still gets a signal.
func (m *MemoryForest) loadSubstrateSignals(
	ctx context.Context,
	mode SubstrateMode,
	branches []*Branch,
	canopy *Canopy,
) (map[string]substrateSignal, error) {
	switch mode {
	case SubstrateModeWarmthOnly:
		return map[string]substrateSignal{}, nil
	case SubstrateModePageRank:
		return m.loadPageRankSubstrateSignals(ctx, branches, canopy)
	}
	return m.loadStoredSubstrateState(ctx, branches)
}

func (m *MemoryForest) loadStoredSubstrateState(
	ctx context.Context,
	branches []*Branch,
) (map[string]substrateSignal, error) {
	result := make(map[string]substrateSignal, len(branches))
	if len(branches) == 0 {
		return result, nil
	}

	sessionIDs := substrateSessionIDsFromBranches(branches)
	readySessions, err := m.loadReadySubstrateSessions(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}

	contextKeys := make([]string, 0, len(readySessions))
	seenContexts := make(map[string]struct{}, len(readySessions))
	branchIDs := make([]string, 0, len(branches))
	for _, branch := range branches {
		sessionID := normalizeForestSessionID(branch.SessionID)
		if !readySessions[sessionID] {
			continue
		}
		branchIDs = append(branchIDs, branch.ID)
		contextKey := substrateContextKey(sessionID)
		if _, ok := seenContexts[contextKey]; ok {
			continue
		}
		seenContexts[contextKey] = struct{}{}
		contextKeys = append(contextKeys, contextKey)
	}
	branchIDs = dedupeStrings(branchIDs)
	if len(branchIDs) == 0 || len(contextKeys) == 0 {
		return result, nil
	}

	contextPlaceholders := make([]string, 0, len(contextKeys))
	branchPlaceholders := make([]string, 0, len(branchIDs))
	args := make([]any, 0, len(contextKeys)+len(branchIDs))
	for _, contextKey := range contextKeys {
		contextPlaceholders = append(contextPlaceholders, "?")
		args = append(args, contextKey)
	}
	for _, branchID := range branchIDs {
		branchPlaceholders = append(branchPlaceholders, "?")
		args = append(args, branchID)
	}

	m.substrateStateMu.RLock()
	defer m.substrateStateMu.RUnlock()
	rows, err := m.db.QueryContext(ctx, `
		SELECT branch_id, nutrient_potential, frontier_score, inhibition, conductance_mass
		FROM forest_substrate_state
		WHERE context_key IN (`+strings.Join(contextPlaceholders, ",")+`)
		  AND branch_id IN (`+strings.Join(branchPlaceholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query substrate state: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			branchID string
			signal   substrateSignal
		)
		if err := rows.Scan(&branchID, &signal.Potential, &signal.Frontier, &signal.Inhibition, &signal.ConductanceMass); err != nil {
			return nil, fmt.Errorf("scan substrate state: %w", err)
		}
		result[branchID] = signal
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *MemoryForest) loadReadySubstrateSessions(ctx context.Context, sessionIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(sessionIDs))
	sessionIDs = dedupeStrings(sessionIDs)
	if len(sessionIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, 0, len(sessionIDs))
	args := make([]any, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		placeholders = append(placeholders, "?")
		args = append(args, normalizeForestSessionID(sessionID))
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id, graph_version, substrate_version, dirty
		FROM forest_substrate_sessions
		WHERE session_id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query substrate sessions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			sessionID        string
			graphVersion     int64
			substrateVersion int64
			dirty            int
		)
		if err := rows.Scan(&sessionID, &graphVersion, &substrateVersion, &dirty); err != nil {
			return nil, fmt.Errorf("scan substrate session: %w", err)
		}
		result[normalizeForestSessionID(sessionID)] = dirty == 0 && substrateVersion == graphVersion
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *MemoryForest) dirtySubstrateSessions(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id
		FROM forest_substrate_sessions
		WHERE dirty = 1
		ORDER BY dirty_at ASC, updated_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query dirty substrate sessions: %w", err)
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, fmt.Errorf("scan dirty substrate session: %w", err)
		}
		sessions = append(sessions, normalizeForestSessionID(sessionID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dedupeStrings(sessions), nil
}

func (m *MemoryForest) currentSubstrateGraphVersion(ctx context.Context, sessionID string) (int64, error) {
	sessionID = normalizeForestSessionID(sessionID)
	var version int64
	err := m.db.QueryRowContext(ctx, `
		SELECT graph_version
		FROM forest_substrate_sessions
		WHERE session_id = ?
	`, sessionID).Scan(&version)
	switch {
	case err == nil:
		return version, nil
	case err == sql.ErrNoRows:
		return 0, nil
	default:
		return 0, fmt.Errorf("load substrate graph version: %w", err)
	}
}

func (m *MemoryForest) loadSessionCanopyRoots(ctx context.Context, sessionID string) (map[string]struct{}, error) {
	canopy, err := m.resolveCanopy(ctx, Query{SessionID: sessionID, Horizon: CanopyHorizonSession})
	if err != nil {
		return nil, err
	}
	roots := make(map[string]struct{}, len(canopy.RootIDs))
	for _, rootID := range canopy.RootIDs {
		roots[rootID] = struct{}{}
	}
	return roots, nil
}

func substrateSessionIDsFromBranches(branches []*Branch) []string {
	sessionIDs := make([]string, 0, len(branches))
	for _, branch := range branches {
		if branch == nil {
			continue
		}
		sessionIDs = append(sessionIDs, normalizeForestSessionID(branch.SessionID))
	}
	return dedupeStrings(sessionIDs)
}

func retrievalSubstrateSignal(branch *Branch, signal substrateSignal, queryScore float64, canopyRoots map[string]struct{}) substrateSignal {
	if branch == nil {
		return signal
	}
	if signal == (substrateSignal{}) {
		return signal
	}
	canopyBoost := 0.0
	if _, ok := canopyRoots[branch.RootID]; ok {
		canopyBoost = 1.0
	}
	frontier := clamp01(
		(0.70 * signal.Frontier) +
			(0.15 * clamp01(queryScore)) +
			(0.10 * canopyBoost) +
			(0.05 * (1 - accessPressure(branch))),
	)
	return substrateSignal{
		Potential:       signal.Potential,
		Frontier:        frontier,
		Inhibition:      signal.Inhibition,
		ConductanceMass: signal.ConductanceMass,
	}
}

func (m *MemoryForest) refreshSubstrateState(
	ctx context.Context,
	sessionID string,
	branches []*Branch,
	canopyRoots map[string]struct{},
) (map[string]substrateSignal, SubstrateResult, error) {
	result := make(map[string]substrateSignal, len(branches))
	sessionID = normalizeForestSessionID(sessionID)
	contextKey := substrateContextKey(sessionID)
	targetVersion, err := m.currentSubstrateGraphVersion(ctx, sessionID)
	if err != nil {
		return nil, SubstrateResult{}, err
	}
	if len(branches) == 0 {
		if err := m.clearSubstrateState(ctx, sessionID, targetVersion); err != nil {
			return nil, SubstrateResult{}, err
		}
		return result, SubstrateResult{}, nil
	}

	byID := make(map[string]*Branch, len(branches))
	indexByID := make(map[string]int, len(branches))
	for idx, branch := range branches {
		byID[branch.ID] = branch
		indexByID[branch.ID] = idx
	}

	edges, err := m.loadSubstrateAdjacency(ctx, sessionID, branches)
	if err != nil {
		return nil, SubstrateResult{}, err
	}
	degrees := make(map[string]int, len(branches))
	for _, edge := range edges {
		degrees[edge.SourceID]++
		degrees[edge.TargetID]++
	}

	sources := make([]float32, len(branches))
	inhibitions := make([]float32, len(branches))
	for idx, branch := range branches {
		sources[idx] = float32(substrateSourceScore(branch, canopyRoots))
		inhibitions[idx] = float32(substrateInhibition(branch, byID, edges))
	}

	current := append([]float32(nil), sources...)
	for iter := 0; iter < 4; iter++ {
		diffused := make([]float32, len(branches))
		mass := make([]float32, len(branches))
		for _, edge := range edges {
			sourceIndex, okSource := indexByID[edge.SourceID]
			targetIndex, okTarget := indexByID[edge.TargetID]
			if !okSource || !okTarget {
				continue
			}
			weight := float32(clamp01(edge.Conductance * (1 - (0.5 * edge.Inhibition))))
			if weight <= 0 {
				continue
			}
			diffused[sourceIndex] += current[targetIndex] * weight
			diffused[targetIndex] += current[sourceIndex] * weight
			mass[sourceIndex] += weight
			mass[targetIndex] += weight
		}
		for i := range diffused {
			if mass[i] > 0 {
				diffused[i] /= mass[i]
			} else {
				diffused[i] = current[i]
			}
		}
		next := vek32.MulNumber(sources, float32(0.55))
		vek32.Add_Inplace(next, vek32.MulNumber(diffused, float32(0.45)))
		for i := range next {
			next[i] *= float32(1 - (0.35 * clamp01(float64(inhibitions[i]))))
		}
		current = next
	}

	maxConductanceMass := 0.0
	conductanceMass := make(map[string]float64, len(branches))
	for _, edge := range edges {
		conductanceMass[edge.SourceID] += edge.Conductance
		conductanceMass[edge.TargetID] += edge.Conductance
	}
	for _, mass := range conductanceMass {
		if mass > maxConductanceMass {
			maxConductanceMass = mass
		}
	}

	now := time.Now().UTC()
	updatedEdges := make([]substrateEdge, 0, len(edges))
	for _, edge := range edges {
		sourceIndex, okSource := indexByID[edge.SourceID]
		targetIndex, okTarget := indexByID[edge.TargetID]
		if !okSource || !okTarget {
			continue
		}
		avgSuccess := (successBalance(byID[edge.SourceID]) + successBalance(byID[edge.TargetID])) / 2
		avgSource := (float64(sources[sourceIndex]) + float64(sources[targetIndex])) / 2
		avgInhibition := (float64(inhibitions[sourceIndex]) + float64(inhibitions[targetIndex])) / 2
		delta := math.Abs(float64(current[sourceIndex]) - float64(current[targetIndex]))
		redundancy := clamp01(float64(degrees[edge.SourceID]+degrees[edge.TargetID]) / 8.0)
		targetConductance := clamp01(0.35*delta + 0.25*avgSource + 0.20*avgSuccess + 0.10*redundancy + 0.10*(1-avgInhibition))
		if edge.Conductance == 0 {
			edge.Conductance = 0.2
		}
		edge.Redundancy = redundancy
		edge.Inhibition = clamp01(avgInhibition)
		edge.Conductance = clamp01((edge.Conductance * 0.60) + (targetConductance * 0.40))
		edge.Flux = clamp01(edge.Conductance * delta)
		updatedEdges = append(updatedEdges, edge)
	}

	frontiers := make([]struct {
		branchID string
		rootID   string
		score    float64
	}, 0, len(branches))
	for idx, branch := range branches {
		massSignal := 0.0
		if maxConductanceMass > 0 {
			massSignal = clamp01(conductanceMass[branch.ID] / maxConductanceMass)
		}
		frontier := clamp01(
			(0.40 * float64(current[idx])) +
				(0.20 * branch.Salience) +
				(0.15 * branch.Utility) +
				(0.10 * successBalance(branch)) +
				(0.15 * (1 - float64(inhibitions[idx]))),
		)
		result[branch.ID] = substrateSignal{
			Potential:       clamp01(float64(current[idx])),
			Frontier:        frontier,
			Inhibition:      clamp01(float64(inhibitions[idx])),
			ConductanceMass: massSignal,
		}
		frontiers = append(frontiers, struct {
			branchID string
			rootID   string
			score    float64
		}{
			branchID: branch.ID,
			rootID:   branch.RootID,
			score:    frontier,
		})
	}

	sort.SliceStable(frontiers, func(i, j int) bool {
		return frontiers[i].score > frontiers[j].score
	})
	if len(frontiers) > 8 {
		frontiers = frontiers[:8]
	}

	m.substrateStateMu.Lock()
	defer m.substrateStateMu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, SubstrateResult{}, fmt.Errorf("begin substrate tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM forest_substrate_state
		WHERE context_key = ?
	`, contextKey); err != nil {
		return nil, SubstrateResult{}, fmt.Errorf("clear substrate state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM forest_substrate_frontiers
		WHERE context_key = ? AND agent_type = ?
	`, contextKey, structuralSubstrateAgentType); err != nil {
		return nil, SubstrateResult{}, fmt.Errorf("clear substrate frontiers: %w", err)
	}
	// Issue #10 Phase C: replaced the DELETE-all-then-INSERT-all
	// pattern with a diff-based update. Load current edges, build
	// a desired set, UPSERT changed/added rows, DELETE the orphans
	// that no longer appear. Bounded by actual change, not by the
	// graph's full size.
	if err := applySubstrateEdgeDiffTx(ctx, tx, sessionID, contextKey, updatedEdges, now); err != nil {
		return nil, SubstrateResult{}, err
	}

	for branchID, signal := range result {
		branch := byID[branchID]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_substrate_state
			(context_key, session_id, intent_id, horizon, agent_type, branch_id, nutrient_potential,
			 frontier_score, inhibition, conductance_mass, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, contextKey, sessionID, nil, string(CanopyHorizonSession), nil, branchID,
			signal.Potential, signal.Frontier, signal.Inhibition, signal.ConductanceMass, now.Unix()); err != nil {
			return nil, SubstrateResult{}, fmt.Errorf("insert substrate state: %w", err)
		}
		branch.Metadata = cloneMetadata(branch.Metadata)
		branch.Metadata["substrate_potential"] = signal.Potential
		branch.Metadata["substrate_frontier"] = signal.Frontier
		branch.Metadata["substrate_inhibition"] = signal.Inhibition
		if _, err := tx.ExecContext(ctx, `
			UPDATE forest_branches
			SET metadata = ?
			WHERE id = ?
		`, marshalJSON(branch.Metadata), branchID); err != nil {
			return nil, SubstrateResult{}, fmt.Errorf("update branch substrate metadata: %w", err)
		}
	}

	// Edges already applied via applySubstrateEdgeDiffTx above; the
	// previous unconditional INSERT loop is replaced by the diff
	// path so we don't redo the work here.

	for _, frontier := range frontiers {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_substrate_frontiers
			(context_key, agent_type, root_id, branch_id, frontier_score, budget, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, contextKey, structuralSubstrateAgentType, frontier.rootID, frontier.branchID,
			frontier.score, clamp01(frontier.score*1.25), now.Unix()); err != nil {
			return nil, SubstrateResult{}, fmt.Errorf("insert substrate frontier: %w", err)
		}
	}

	if err := m.markSubstrateVersionTx(ctx, tx, sessionID, targetVersion, now); err != nil {
		return nil, SubstrateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return nil, SubstrateResult{}, fmt.Errorf("commit substrate tx: %w", err)
	}

	return result, SubstrateResult{
		StatesUpdated:    len(result),
		EdgesUpdated:     len(updatedEdges),
		FrontiersUpdated: len(frontiers),
	}, nil
}

func (m *MemoryForest) markSubstrateVersionTx(ctx context.Context, tx *sql.Tx, sessionID string, targetVersion int64, at time.Time) error {
	sessionID = normalizeForestSessionID(sessionID)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_substrate_sessions
		(session_id, graph_version, substrate_version, dirty, dirty_at, updated_at)
		VALUES (?, ?, ?, 0, 0, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			substrate_version = CASE
				WHEN forest_substrate_sessions.graph_version = excluded.substrate_version THEN excluded.substrate_version
				ELSE forest_substrate_sessions.substrate_version
			END,
			dirty = CASE
				WHEN forest_substrate_sessions.graph_version = excluded.substrate_version THEN 0
				ELSE 1
			END,
			updated_at = excluded.updated_at
	`, sessionID, targetVersion, targetVersion, at.Unix())
	if err != nil {
		return fmt.Errorf("mark substrate version: %w", err)
	}
	return nil
}

func (m *MemoryForest) clearSubstrateState(ctx context.Context, sessionID string, targetVersion int64) error {
	sessionID = normalizeForestSessionID(sessionID)
	contextKey := substrateContextKey(sessionID)
	now := time.Now().UTC()

	m.substrateStateMu.Lock()
	defer m.substrateStateMu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear substrate tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM forest_substrate_state WHERE context_key = ?`, contextKey); err != nil {
		return fmt.Errorf("clear substrate state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM forest_substrate_frontiers WHERE context_key = ? AND agent_type = ?`, contextKey, structuralSubstrateAgentType); err != nil {
		return fmt.Errorf("clear substrate frontiers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM forest_substrate_edges WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("clear substrate edges: %w", err)
	}
	if err := m.markSubstrateVersionTx(ctx, tx, sessionID, targetVersion, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear substrate tx: %w", err)
	}
	return nil
}

func (m *MemoryForest) loadSubstrateAdjacency(ctx context.Context, sessionID string, branches []*Branch) ([]substrateEdge, error) {
	branchIDs := make([]string, 0, len(branches))
	branchByID := make(map[string]*Branch, len(branches))
	for _, branch := range branches {
		branchIDs = append(branchIDs, branch.ID)
		branchByID[branch.ID] = branch
	}
	if len(branchIDs) == 0 {
		return nil, nil
	}

	type edgeSeed struct {
		SourceID    string
		TargetID    string
		Conductance float64
		Flux        float64
		Redundancy  float64
		Inhibition  float64
	}

	edgeMap := make(map[string]edgeSeed)
	addEdge := func(sourceID, targetID string, conductance, flux, redundancy, inhibition float64) {
		sourceID, targetID = canonicalEdge(sourceID, targetID)
		if sourceID == "" || targetID == "" || sourceID == targetID {
			return
		}
		key := sourceID + "|" + targetID
		if existing, ok := edgeMap[key]; ok {
			existing.Conductance = mathMax(existing.Conductance, conductance)
			existing.Flux = mathMax(existing.Flux, flux)
			existing.Redundancy = mathMax(existing.Redundancy, redundancy)
			existing.Inhibition = mathMax(existing.Inhibition, inhibition)
			edgeMap[key] = existing
			return
		}
		edgeMap[key] = edgeSeed{
			SourceID:    sourceID,
			TargetID:    targetID,
			Conductance: conductance,
			Flux:        flux,
			Redundancy:  redundancy,
			Inhibition:  inhibition,
		}
	}

	placeholders := make([]string, 0, len(branchIDs))
	args := make([]any, 0, len(branchIDs)*3)
	for _, branchID := range branchIDs {
		placeholders = append(placeholders, "?")
		args = append(args, branchID)
	}
	for _, branchID := range branchIDs {
		args = append(args, branchID)
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT source_branch_id, target_branch_id, weight
		FROM forest_relay_edges
		WHERE source_branch_id IN (`+strings.Join(placeholders, ",")+`)
		   OR target_branch_id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query relay adjacency: %w", err)
	}
	for rows.Next() {
		var (
			sourceID string
			targetID string
			weight   float64
		)
		if err := rows.Scan(&sourceID, &targetID, &weight); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan relay adjacency: %w", err)
		}
		if _, ok := branchByID[sourceID]; !ok {
			if _, ok := branchByID[targetID]; !ok {
				continue
			}
		}
		addEdge(sourceID, targetID, clamp01(weight/2), 0, 0, 0)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	persistedArgs := make([]any, 0, len(branchIDs)*2+1)
	persistedArgs = append(persistedArgs, sessionID)
	for _, branchID := range branchIDs {
		persistedArgs = append(persistedArgs, branchID)
	}
	for _, branchID := range branchIDs {
		persistedArgs = append(persistedArgs, branchID)
	}
	rows, err = m.db.QueryContext(ctx, `
		SELECT source_branch_id, target_branch_id, conductance, flux, redundancy, inhibition
		FROM forest_substrate_edges
		WHERE session_id = ?
		  AND (source_branch_id IN (`+strings.Join(placeholders, ",")+`)
		       OR target_branch_id IN (`+strings.Join(placeholders, ",")+`))
	`, persistedArgs...)
	if err != nil {
		return nil, fmt.Errorf("query substrate edges: %w", err)
	}
	for rows.Next() {
		var edge substrateEdge
		if err := rows.Scan(&edge.SourceID, &edge.TargetID, &edge.Conductance, &edge.Flux, &edge.Redundancy, &edge.Inhibition); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan substrate edge: %w", err)
		}
		addEdge(edge.SourceID, edge.TargetID, edge.Conductance, edge.Flux, edge.Redundancy, edge.Inhibition)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for _, branch := range branches {
		if branch.ParentID == "" {
			continue
		}
		if _, ok := branchByID[branch.ParentID]; !ok {
			continue
		}
		addEdge(branch.ID, branch.ParentID, 0.25, 0, 0.1, 0)
	}

	edges := make([]substrateEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, substrateEdge{
			SourceID:    edge.SourceID,
			TargetID:    edge.TargetID,
			Conductance: edge.Conductance,
			Flux:        edge.Flux,
			Redundancy:  edge.Redundancy,
			Inhibition:  edge.Inhibition,
		})
	}
	return edges, nil
}

func canonicalEdge(sourceID, targetID string) (string, string) {
	sourceID = strings.TrimSpace(sourceID)
	targetID = strings.TrimSpace(targetID)
	if sourceID == "" || targetID == "" {
		return "", ""
	}
	if sourceID <= targetID {
		return sourceID, targetID
	}
	return targetID, sourceID
}

func substrateSourceScore(branch *Branch, canopyRoots map[string]struct{}) float64 {
	if branch == nil {
		return 0
	}
	canopyBoost := 0.0
	if _, ok := canopyRoots[branch.RootID]; ok {
		canopyBoost = 1.0
	}
	return clamp01(
		(0.30 * branch.Utility) +
			(0.25 * branch.Salience) +
			(0.20 * successBalance(branch)) +
			(0.15 * branchRecency(branch, time.Now().UTC())) +
			(0.10 * canopyBoost),
	)
}

func substrateInhibition(branch *Branch, byID map[string]*Branch, edges []substrateEdge) float64 {
	if branch == nil {
		return 0
	}
	inhibition := (0.45 * branch.ScopeRisk) + (0.35 * branch.ConflictScore)
	if strings.EqualFold(branch.AgentType, "guardian") || branch.Family == TreeFamilyConstraint || branch.Family == TreeFamilyAntiPattern {
		inhibition += 0.15
	}
	guardianNeighborMass := 0.0
	for _, edge := range edges {
		if edge.SourceID != branch.ID && edge.TargetID != branch.ID {
			continue
		}
		otherID := edge.SourceID
		if otherID == branch.ID {
			otherID = edge.TargetID
		}
		other := byID[otherID]
		if other == nil {
			continue
		}
		if strings.EqualFold(other.AgentType, "guardian") || other.Family == TreeFamilyConstraint || other.Family == TreeFamilyAntiPattern {
			guardianNeighborMass += edge.Conductance
		}
	}
	return clamp01(inhibition + (0.15 * clamp01(guardianNeighborMass)))
}

func (m *MemoryForest) RunSubstrateMaintenance(ctx context.Context, limit int) (SubstrateResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runSubstrateMaintenance(ctx, limit)
}

func (m *MemoryForest) runSubstrateMaintenance(ctx context.Context, limit int) (SubstrateResult, error) {
	if limit <= 0 {
		limit = 64
	}

	sessions, err := m.dirtySubstrateSessions(ctx)
	if err != nil {
		return SubstrateResult{}, err
	}
	if len(sessions) == 0 {
		sessions, err = m.recentSubstrateSessions(ctx)
		if err != nil {
			return SubstrateResult{}, err
		}
	}
	if len(sessions) == 0 {
		sessions = append(sessions, "global")
	}

	var result SubstrateResult
	for _, sessionID := range sessions {
		refreshed, err := m.runSubstrateMaintenanceForSession(ctx, sessionID, limit)
		if err != nil {
			return result, err
		}
		result.StatesUpdated += refreshed.StatesUpdated
		result.EdgesUpdated += refreshed.EdgesUpdated
		result.FrontiersUpdated += refreshed.FrontiersUpdated
	}
	return result, nil
}

func (m *MemoryForest) RunSubstrateMaintenanceForSession(ctx context.Context, sessionID string, limit int) (SubstrateResult, error) {
	m.maintenanceRunMu.Lock()
	defer m.maintenanceRunMu.Unlock()
	return m.runSubstrateMaintenanceForSession(ctx, sessionID, limit)
}

func (m *MemoryForest) runSubstrateMaintenanceForSession(ctx context.Context, sessionID string, limit int) (SubstrateResult, error) {
	if limit <= 0 {
		limit = 64
	}
	sessionID = normalizeForestSessionID(sessionID)
	hasNodes, err := m.hasSubstrateNodes(ctx, sessionID)
	if err != nil {
		return SubstrateResult{}, err
	}
	if hasNodes {
		return m.refreshSubstrateChannelState(ctx, sessionID, limit)
	}
	return SubstrateResult{}, nil
}

func (m *MemoryForest) countSubstrateBranches(ctx context.Context, sessionID string) (int, error) {
	var count int
	if err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_branches
		WHERE session_id = ? OR ? = 'global'
	`, normalizeForestSessionID(sessionID), normalizeForestSessionID(sessionID)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count substrate branches: %w", err)
	}
	return count, nil
}

func (m *MemoryForest) hasSubstrateNodes(ctx context.Context, sessionID string) (bool, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM forest_nodes
		WHERE session_id = ? OR ? = 'global'
	`, normalizeForestSessionID(sessionID), normalizeForestSessionID(sessionID)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count substrate nodes: %w", err)
	}
	return count > 0, nil
}

func (m *MemoryForest) recentSubstrateSessions(ctx context.Context) ([]string, error) {
	nodeSessions, err := m.recentNodeSubstrateSessions(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodeSessions) > 0 {
		return nodeSessions, nil
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id
		FROM (
			SELECT session_id, MAX(updated_at) AS max_updated_at
			FROM forest_branches
			WHERE session_id != ''
			GROUP BY session_id
		)
		ORDER BY max_updated_at DESC
		LIMIT 4
	`)
	if err != nil {
		return nil, fmt.Errorf("query recent substrate sessions: %w", err)
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, fmt.Errorf("scan recent substrate session: %w", err)
		}
		sessions = append(sessions, normalizeForestSessionID(sessionID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dedupeStrings(sessions), nil
}

func (m *MemoryForest) recentNodeSubstrateSessions(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id
		FROM forest_nodes
		WHERE session_id != ''
		GROUP BY session_id
		ORDER BY MAX(last_seen_at) DESC
		LIMIT ?
	`, m.substrateLimit)
	if err != nil {
		return nil, fmt.Errorf("query recent node substrate sessions: %w", err)
	}
	defer rows.Close()
	var sessions []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, fmt.Errorf("scan recent node substrate session: %w", err)
		}
		sessions = append(sessions, normalizeForestSessionID(sessionID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dedupeStrings(sessions), nil
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}
