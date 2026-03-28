package forest

import (
	"context"
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

func substrateContextKey(query Query) string {
	return stableID(
		"substrate",
		firstNonEmpty(query.SessionID, "global"),
		query.IntentID,
		string(query.Horizon),
		normalizeRequesterAgentType(query.AgentType),
		normalizeText(query.Query),
	)
}

func (m *MemoryForest) loadSubstrateSignals(
	ctx context.Context,
	query Query,
	branches []*Branch,
	queryScores map[string]float64,
	canopyRoots map[string]struct{},
) (map[string]substrateSignal, error) {
	contextKey := substrateContextKey(query)
	signals := make(map[string]substrateSignal, len(branches))
	if len(branches) == 0 {
		return signals, nil
	}

	branchIDs := make([]string, 0, len(branches))
	for _, branch := range branches {
		branchIDs = append(branchIDs, branch.ID)
	}

	cached, err := m.loadStoredSubstrateState(ctx, contextKey, branchIDs)
	if err != nil {
		return nil, err
	}
	if len(cached) == len(branches) {
		return cached, nil
	}

	refreshed, _, err := m.refreshSubstrateState(ctx, query, branches, queryScores, canopyRoots)
	if err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (m *MemoryForest) loadStoredSubstrateState(
	ctx context.Context,
	contextKey string,
	branchIDs []string,
) (map[string]substrateSignal, error) {
	result := make(map[string]substrateSignal, len(branchIDs))
	branchIDs = dedupeStrings(branchIDs)
	if len(branchIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, 0, len(branchIDs))
	args := make([]any, 0, len(branchIDs)+2)
	args = append(args, contextKey, time.Now().UTC().Add(-2*time.Minute).Unix())
	for _, branchID := range branchIDs {
		placeholders = append(placeholders, "?")
		args = append(args, branchID)
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT branch_id, nutrient_potential, frontier_score, inhibition, conductance_mass
		FROM forest_substrate_state
		WHERE context_key = ? AND updated_at >= ? AND branch_id IN (`+strings.Join(placeholders, ",")+`)
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

func (m *MemoryForest) refreshSubstrateState(
	ctx context.Context,
	query Query,
	branches []*Branch,
	queryScores map[string]float64,
	canopyRoots map[string]struct{},
) (map[string]substrateSignal, SubstrateResult, error) {
	result := make(map[string]substrateSignal, len(branches))
	if len(branches) == 0 {
		return result, SubstrateResult{}, nil
	}

	sessionID := firstNonEmpty(query.SessionID, "global")
	contextKey := substrateContextKey(query)
	branchIDs := make([]string, 0, len(branches))
	byID := make(map[string]*Branch, len(branches))
	indexByID := make(map[string]int, len(branches))
	for idx, branch := range branches {
		branchIDs = append(branchIDs, branch.ID)
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
		sources[idx] = float32(substrateSourceScore(query, branch, queryScores[branch.ID], canopyRoots))
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
				(0.20 * (1 - accessPressure(branch))) +
				(0.15 * agentFamilyAffinity(query.AgentType, branch.Family)) +
				(0.15 * massSignal) +
				(0.10 * (1 - float64(inhibitions[idx]))),
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

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, SubstrateResult{}, fmt.Errorf("begin substrate tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM forest_substrate_frontiers
		WHERE context_key = ? AND agent_type = ?
	`, contextKey, normalizeRequesterAgentType(query.AgentType)); err != nil {
		return nil, SubstrateResult{}, fmt.Errorf("clear substrate frontiers: %w", err)
	}

	for branchID, signal := range result {
		branch := byID[branchID]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_substrate_state
			(context_key, session_id, intent_id, horizon, agent_type, branch_id, nutrient_potential,
			 frontier_score, inhibition, conductance_mass, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(context_key, branch_id) DO UPDATE SET
				nutrient_potential = excluded.nutrient_potential,
				frontier_score = excluded.frontier_score,
				inhibition = excluded.inhibition,
				conductance_mass = excluded.conductance_mass,
				updated_at = excluded.updated_at
		`, contextKey, sessionID, nullStringValue(query.IntentID), string(query.Horizon),
			nullStringValue(normalizeRequesterAgentType(query.AgentType)), branchID, signal.Potential,
			signal.Frontier, signal.Inhibition, signal.ConductanceMass, now.Unix()); err != nil {
			return nil, SubstrateResult{}, fmt.Errorf("upsert substrate state: %w", err)
		}
		branch.Metadata = cloneMetadata(branch.Metadata)
		branch.Metadata["substrate_potential"] = signal.Potential
		branch.Metadata["substrate_frontier"] = signal.Frontier
		branch.Metadata["substrate_inhibition"] = signal.Inhibition
	}

	for _, edge := range updatedEdges {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_substrate_edges
			(session_id, source_branch_id, target_branch_id, conductance, flux, redundancy, inhibition, updated_at, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, source_branch_id, target_branch_id) DO UPDATE SET
				conductance = excluded.conductance,
				flux = excluded.flux,
				redundancy = excluded.redundancy,
				inhibition = excluded.inhibition,
				updated_at = excluded.updated_at,
				metadata = excluded.metadata
		`, sessionID, edge.SourceID, edge.TargetID, edge.Conductance, edge.Flux, edge.Redundancy, edge.Inhibition,
			now.Unix(), marshalJSON(map[string]any{"context_key": contextKey})); err != nil {
			return nil, SubstrateResult{}, fmt.Errorf("upsert substrate edge: %w", err)
		}
	}

	for _, frontier := range frontiers {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_substrate_frontiers
			(context_key, agent_type, root_id, branch_id, frontier_score, budget, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(context_key, agent_type, branch_id) DO UPDATE SET
				frontier_score = excluded.frontier_score,
				budget = excluded.budget,
				updated_at = excluded.updated_at
		`, contextKey, normalizeRequesterAgentType(query.AgentType), frontier.rootID, frontier.branchID,
			frontier.score, clamp01(frontier.score*1.25), now.Unix()); err != nil {
			return nil, SubstrateResult{}, fmt.Errorf("upsert substrate frontier: %w", err)
		}
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

func substrateSourceScore(query Query, branch *Branch, queryScore float64, canopyRoots map[string]struct{}) float64 {
	if branch == nil {
		return 0
	}
	canopyBoost := 0.0
	if _, ok := canopyRoots[branch.RootID]; ok {
		canopyBoost = 1.0
	}
	novelty := 1 - accessPressure(branch)
	if strings.TrimSpace(query.Query) == "" {
		return clamp01(
			(0.30 * branch.Utility) +
				(0.25 * branch.Salience) +
				(0.20 * successBalance(branch)) +
				(0.15 * novelty) +
				(0.10 * canopyBoost),
		)
	}
	return clamp01(
		(0.35 * queryScore) +
			(0.20 * branch.Utility) +
			(0.15 * branch.Salience) +
			(0.10 * successBalance(branch)) +
			(0.10 * novelty) +
			(0.10 * canopyBoost),
	)
}

func substrateInhibition(branch *Branch, byID map[string]*Branch, edges []substrateEdge) float64 {
	if branch == nil {
		return 0
	}
	inhibition := (0.45 * branch.ScopeRisk) + (0.35 * branch.ConflictScore)
	if strings.EqualFold(branch.AgentType, "guardian") || branch.Family == TreeFamilyConstraint || branch.Family == TreeFamilyConflict {
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
		if strings.EqualFold(other.AgentType, "guardian") || other.Family == TreeFamilyConstraint || other.Family == TreeFamilyConflict {
			guardianNeighborMass += edge.Conductance
		}
	}
	return clamp01(inhibition + (0.15 * clamp01(guardianNeighborMass)))
}

func (m *MemoryForest) RunSubstrateMaintenance(ctx context.Context, limit int) (SubstrateResult, error) {
	if limit <= 0 {
		limit = 64
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
		return SubstrateResult{}, fmt.Errorf("query substrate sessions: %w", err)
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return SubstrateResult{}, fmt.Errorf("scan substrate session: %w", err)
		}
		sessions = append(sessions, sessionID)
	}
	if err := rows.Err(); err != nil {
		return SubstrateResult{}, err
	}
	if len(sessions) == 0 {
		sessions = append(sessions, "global")
	}

	var result SubstrateResult
	for _, sessionID := range sessions {
		branches, err := m.queryBranches(ctx, sessionID, false, defaultFamilies(), limit)
		if err != nil {
			return result, err
		}
		if len(branches) == 0 {
			continue
		}
		query := Query{
			SessionID: sessionID,
			AgentType: "guide",
			Horizon:   CanopyHorizonSession,
			Limit:     limit,
		}
		canopy, err := m.resolveCanopy(ctx, query)
		if err != nil {
			return result, err
		}
		canopyRoots := make(map[string]struct{}, len(canopy.RootIDs))
		for _, rootID := range canopy.RootIDs {
			canopyRoots[rootID] = struct{}{}
		}
		_, refreshed, err := m.refreshSubstrateState(ctx, query, branches, map[string]float64{}, canopyRoots)
		if err != nil {
			return result, err
		}
		result.StatesUpdated += refreshed.StatesUpdated
		result.EdgesUpdated += refreshed.EdgesUpdated
		result.FrontiersUpdated += refreshed.FrontiersUpdated
	}
	return result, nil
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
