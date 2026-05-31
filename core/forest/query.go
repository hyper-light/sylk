package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/google/uuid"
)

// Retrieve returns ranked branch packets for the supplied query.
//
// Emits a retrieval audit event after returning to the caller — the
// audit captures the full ranked candidate set (not just top-K) so
// counterfactual training can label every candidate against later
// outcome events. Audit emission is best-effort; failure logs but
// never affects the retrieval result.
//
// At entry the per-call HyperParameters snapshot is pinned via the
// tuner's A/B trial sampler — every helper invoked downstream reads
// from this same snapshot, so a snapshot replacement mid-retrieval
// can't produce a Frankenscore that mixes active + proposed values.
// The pinned snapshot id and proposed-arm flag are stamped onto the
// audit row so outcome-driven adaptation can attribute the
// observation back to the correct A/B arm.
func (m *MemoryForest) Retrieve(ctx context.Context, query Query) ([]*BranchPacket, error) {
	start := time.Now()
	auditQuery := query
	hp, isProposed := m.pinHyperparameterSnapshotForRetrieve()
	packets, audit, err := m.retrieveWithAudit(ctx, query, start, hp, isProposed)
	finalizeRetrievalAudit(audit, auditQuery, start, err)
	m.emitRetrievalAudit(ctx, audit)
	return packets, err
}

// pinHyperparameterSnapshotForRetrieve resolves the hyperparameter
// snapshot for one Retrieve call. Routes through the tuner's
// SnapshotForTrial when an A/B trial is active so a deterministic
// 50/50 split sees the proposed snapshot; otherwise returns the
// active snapshot. Nil-tuner path returns the live (placeholder or
// captured) snapshot via m.hyperparams() for tests that bypass the
// tuner.
func (m *MemoryForest) pinHyperparameterSnapshotForRetrieve() (*HyperParameters, bool) {
	if m == nil {
		return PlaceholderHyperParameters(), false
	}
	if m.tuner == nil {
		return m.hyperparams(), false
	}
	seed := m.retrievalTrialSeed()
	hp, isProposed := m.tuner.SnapshotForTrial(seed)
	if hp == nil {
		return m.hyperparams(), false
	}
	return hp, isProposed
}

// retrievalTrialSeed produces a per-call seed for SnapshotForTrial.
// Uses the per-forest exploration RNG (already mutex-guarded) so the
// A/B split is deterministic-given-RNG-state and uniformly
// distributed across calls.
func (m *MemoryForest) retrievalTrialSeed() uint32 {
	if m == nil || m.explorationRng == nil {
		return uint32(time.Now().UnixNano())
	}
	m.explorationRngMu.Lock()
	defer m.explorationRngMu.Unlock()
	return m.explorationRng.Uint32()
}

// retrieveWithAudit runs the core retrieval pipeline and returns
// (top-K packets, audit event in-progress, err). The audit event has
// the full candidate set populated; finalizeRetrievalAudit fills in
// the remaining operational fields once the caller has the response.
//
// hp is the pinned-for-this-call hyperparameter snapshot — every
// downstream helper that depends on a tunable reads from this
// snapshot rather than via m.hyperparams() so an in-flight snapshot
// replacement can't tear scoring across helpers. isProposed is true
// when the snapshot came from the tuner's A/B trial proposed arm.
func (m *MemoryForest) retrieveWithAudit(ctx context.Context, query Query, start time.Time, hp *HyperParameters, isProposed bool) ([]*BranchPacket, *RetrievalAuditEvent, error) {
	if hp == nil {
		hp = m.hyperparams()
	}
	audit := newRetrievalAuditEvent(query, start, m.currentBranchProjectionSeq(ctx))
	audit.HyperparamSnapshotID = int64(hp.SnapshotID)
	audit.ProposedHyperparams = isProposed
	// Issue #7: pick the substrate mode for this retrieval up-front so
	// the same mode flows into staged loading + the audit row.
	audit.SubstrateMode = m.resolveRetrieveSubstrateMode()
	// Issue #8: pick the base-score variant up-front. The chosen model
	// (champion / challenger / nil-for-hardcoded) drives scoreBatch's
	// weight vector; the variant + version land on the audit row.
	variant, scorer := m.resolveRetrieveBaseScoreVariant()
	audit.BaseScoreVariant = variant
	if scorer != nil {
		audit.BaseScoreVersion = scorer.Version
	}

	normalized, err := normalizeQuery(query)
	if err != nil {
		return nil, audit, err
	}
	audit.Query = normalized.Query

	staged, err := m.loadRetrievalStage(ctx, normalized, audit.SubstrateMode)
	if err != nil {
		return nil, audit, err
	}
	if len(staged.branches) == 0 {
		// Issue #5 cold-start path: primary stage returned no
		// branches at all (fresh session, narrow query, empty
		// forest). Fall through to global priors so the caller
		// gets *something* rather than nothing. Tagged
		// RetrievalSourceGlobalPrior so the audit captures
		// cold-start frequency.
		fallback := m.fillWithGlobalPriors(ctx, normalized, nil, normalized.Limit)
		populateAuditCandidates(audit, fallback, nil, normalized.Limit)
		return fallback, audit, nil
	}

	scored, err := m.scoreRetrievalStage(ctx, normalized, staged, scorer)
	if err != nil {
		return nil, audit, err
	}

	m.applyLearnedPredictions(normalized, scored.packets, scored.featuresByBranch)

	// Issue #4 mechanism C: knock down branches that appeared in the
	// last N retrievals for this session. Applied to base scores so
	// the subsequent sort + MMR see the cooldown-adjusted ordering.
	m.applyRetrievalCooldownPenaltyWithHP(ctx, hp, normalized.SessionID, scored.packets)

	sortPackets(scored.packets)

	// Decide exploration BEFORE recording examples so the retrieval-
	// example rows carry the correct retrieval_mode/label_weight
	// (exploration outcomes are unbiased and trained higher) and so
	// the audit event's exploration_mode is accurate.
	exploring := m.shouldExploreWithHP(hp)
	retrievalMode := ""
	exampleWeight := 1.0
	if exploring {
		retrievalMode = string(RetrievalModeExploration)
		exampleWeight = hp.ExplorationLabelWeight
	}
	if recordErr := m.recordRetrievalExamplesWithSource(
		ctx, normalized, scored.packets, scored.featuresByBranch,
		retrievalMode, exampleWeight, audit.ID,
	); recordErr != nil && m.logger != nil {
		m.logger.Debug("forest: record retrieval examples failed", "error", recordErr)
	}

	populateAuditCandidates(audit, scored.packets, scored.featuresByBranch, normalized.Limit)

	// ε-greedy exploration: with probability explorationRate, replace
	// the model-ranked top-K with a random sample from the broader
	// candidate pool. Audit captures the flag so training can weight
	// outcomes from exploration retrievals higher (unbiased data).
	//
	// Issue #4 mechanism B: in the non-exploration path, MMR rerank
	// the candidate pool to penalize near-duplicates of already-picked
	// branches. Runs over the cooldown-adjusted scores from above.
	// Skipped when exploring (random sample is its own diversity).
	if exploring {
		audit.ExplorationMode = true
		scored.packets = m.explorationShuffleAndPick(scored.packets, normalized.Limit)
	} else {
		scored.packets = m.applyMMRRerankingWithHP(hp, scored.packets, scored.featuresByBranch, normalized.Limit)
		if len(scored.packets) > normalized.Limit {
			scored.packets = scored.packets[:normalized.Limit]
		}
	}
	tagPrimaryRetrievalSource(scored.packets)

	// Issue #5 / RetrievalSourcePrimed: promote Primary → Primed for
	// packets whose branches came in via a primed canopy (see
	// PrimeSession). Lets operators distinguish "this came from the
	// session bootstrap" from "this came from active session work."
	// Runs BEFORE fill/quota so the Primed → Primary fall-through
	// from those tags doesn't get reverted.
	tagPrimedRetrievalSource(scored.packets, staged.canopy, normalized.SessionID)

	// Issue #5 mechanism A: when primary retrieval came up short
	// (cold-start, narrow query), backfill with global priors scoped
	// to the requested family / agent_type. Tagged with
	// RetrievalSourceGlobalPrior so the audit captures cold-start
	// frequency. Skipped while exploring — exploration is its own
	// answer to "primary set is suspect."
	if !exploring {
		scored.packets = m.fillWithGlobalPriors(ctx, normalized, scored.packets, normalized.Limit)
	}

	// Issue #6: reserve up to defaultAntiPrecedentMaxSlots of the
	// bottom-K for matching anti-precedents (Failed-outcome or
	// AntiPattern). Always runs (even when IncludeCounterEvidence is
	// false) so agents always see negative space when it exists.
	// Skipped under exploration — random sampling is the diversity
	// answer for that path.
	if !exploring {
		scored.packets = m.reserveAntiPrecedentSlots(ctx, normalized, scored.packets, normalized.Limit)
	}

	// Issue #4 mechanism A: warmth is reinforced ONLY on outcome /
	// validation / replay events (the AccessReinforcement path in the
	// branch projector), never on retrieval. Retrieval is observation;
	// warmth tracks *use*, not *consideration*. The previous loop
	// here fed retrieval back into warmth and produced a positive
	// feedback runaway (retrieved → warmer → retrieved more).

	// Issue #9 Phase B.4: stamp the active model's calibration onto
	// every returned packet so callers see "the model behind these
	// scores has been verified at this confidence level" alongside
	// the scores themselves. Read once per Retrieve (cached); the
	// inner cache refreshes lazily.
	annotateModelCalibration(scored.packets, m.activeModelCalibration(ctx))

	return scored.packets, audit, nil
}

// annotateModelCalibration writes the active-model calibration value
// into every packet's PacketScore.ModelCalibration. Nil-tolerant:
// nil packet entries are skipped without panicking.
func annotateModelCalibration(packets []*BranchPacket, calibration float64) {
	for _, p := range packets {
		if p == nil {
			continue
		}
		p.Score.ModelCalibration = calibration
	}
}

// retrievalStage bundles the parallel-loaded inputs to scoring.
type retrievalStage struct {
	branches            []*Branch
	queryScores         map[string]float64
	evidenceByBranch    map[string][]PacketEvidence
	canopy              *Canopy
	canopyRoots         map[string]struct{}
	relayMass           map[string]float64
	relayCofire         map[string]float64
	depths              map[string]int
	structuralSubstrate map[string]substrateSignal
	warmth              map[string]float64
}

// scoredRetrieval bundles the result of the scoring stage so the
// outer Retrieve flow stays flat.
type scoredRetrieval struct {
	packets          []*BranchPacket
	featuresByBranch map[string][]float32
}

// loadRetrievalStage runs branch / evidence / canopy loads in
// parallel via tracked goroutines (see retrievalParallelLoad) plus
// the dependent-after-load steps (relay, depth, substrate, warmth)
// sequentially. The substrate mode parameter selects between the
// pre-Issue#7 stored projection, the Issue#7 PageRank alternative,
// and the warmth-only baseline; canopy feeds PageRank's
// personalization vector.
func (m *MemoryForest) loadRetrievalStage(ctx context.Context, query Query, mode SubstrateMode) (retrievalStage, error) {
	branches, queryScores, evidenceByBranch, canopy, err := m.retrievalParallelLoad(ctx, query)
	if err != nil {
		return retrievalStage{}, err
	}
	branches, err = m.hydrateMissingEvidenceBranches(ctx, branches, evidenceByBranch)
	if err != nil {
		return retrievalStage{}, err
	}
	if len(branches) == 0 {
		return retrievalStage{}, nil
	}

	branchIDs := branchIDList(branches)
	relayMass, err := m.loadRelayMass(ctx, branchIDs)
	if err != nil {
		return retrievalStage{}, err
	}
	relayCofire, err := m.loadRelayCofire(ctx, branchIDs)
	if err != nil {
		return retrievalStage{}, err
	}
	structuralSubstrate, err := m.loadSubstrateSignals(ctx, mode, branches, canopy)
	if err != nil {
		return retrievalStage{}, err
	}
	keys := branchKeyList(branches)
	warmth, err := m.warmth.BatchActivation(ctx, keys, time.Now().UTC())
	if err != nil {
		return retrievalStage{}, err
	}

	return retrievalStage{
		branches:            branches,
		queryScores:         queryScores,
		evidenceByBranch:    evidenceByBranch,
		canopy:              canopy,
		canopyRoots:         canopyRootSet(canopy),
		relayMass:           relayMass,
		relayCofire:         relayCofire,
		depths:              computeBranchDepths(branches),
		structuralSubstrate: structuralSubstrate,
		warmth:              warmth,
	}, nil
}

// retrievalParallelLoad fans out the three independent loads onto a
// local WaitGroup. Goroutines complete before this function returns
// (wg.Wait below), so they don't outlive the call.
//
// No defensive recover: each loaded function (loadCandidateBranches,
// searchEvidence, resolveCanopy) returns errors explicitly through
// storeErr. A runtime panic in any of them is a real bug — surface
// it as a crash so the root cause gets fixed.
func (m *MemoryForest) retrievalParallelLoad(ctx context.Context, query Query) ([]*Branch, map[string]float64, map[string][]PacketEvidence, *Canopy, error) {
	var (
		branches         []*Branch
		queryScores      map[string]float64
		evidenceByBranch map[string][]PacketEvidence
		canopy           *Canopy
		loadErr          error
		mu               sync.Mutex
		wg               sync.WaitGroup
	)
	storeErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if loadErr == nil {
			loadErr = err
		}
		mu.Unlock()
	}

	wg.Add(3)
	go func() {
		defer wg.Done()
		loadedBranches, loadedScores, err := m.loadCandidateBranches(ctx, query)
		mu.Lock()
		branches, queryScores = loadedBranches, loadedScores
		mu.Unlock()
		storeErr(err)
	}()
	go func() {
		defer wg.Done()
		loaded, err := m.searchEvidence(ctx, query)
		mu.Lock()
		evidenceByBranch = loaded
		mu.Unlock()
		storeErr(err)
	}()
	go func() {
		defer wg.Done()
		loaded, err := m.resolveCanopy(ctx, query)
		mu.Lock()
		canopy = loaded
		mu.Unlock()
		storeErr(err)
	}()
	wg.Wait()
	return branches, queryScores, evidenceByBranch, canopy, loadErr
}

func (m *MemoryForest) hydrateMissingEvidenceBranches(ctx context.Context, branches []*Branch, evidenceByBranch map[string][]PacketEvidence) ([]*Branch, error) {
	if len(evidenceByBranch) == 0 {
		return branches, nil
	}
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
	if len(missing) == 0 {
		return branches, nil
	}
	extra, err := m.loadBranchesByID(ctx, missing)
	if err != nil {
		return nil, err
	}
	return append(branches, extra...), nil
}

// scoreRetrievalStage produces packets + per-branch feature vectors
// from the loaded stage. The packets carry component scores already;
// learned predictions are applied separately by the caller. The
// supplied scorer model determines the weight vector + bias used to
// aggregate the 13 components into a Base score; nil falls back to
// hardcoded defaults via scoreBatch's own validation.
func (m *MemoryForest) scoreRetrievalStage(ctx context.Context, query Query, stage retrievalStage, scorer *BaseScoreModel) (scoredRetrieval, error) {
	now := time.Now().UTC()
	packets := make([]*BranchPacket, 0, len(stage.branches))
	inputs := make([]scoreInput, 0, len(stage.branches))
	packetBranches := make([]*Branch, 0, len(stage.branches))
	features := make(map[string][]float32, len(stage.branches))

	for _, branch := range stage.branches {
		support, counter, err := m.loadBranchEvidencePacket(ctx, branch.ID, 4, query.IncludeCounterEvidence)
		if err != nil {
			return scoredRetrieval{}, err
		}
		if extra := stage.evidenceByBranch[branch.ID]; len(extra) > 0 {
			support = mergeEvidence(extra, support)
		}
		input := buildScoreInput(branch, stage, support, now)
		inputs = append(inputs, input)
		packetBranches = append(packetBranches, branch)
		packets = append(packets, &BranchPacket{
			Branch:          branch,
			Support:         support,
			CounterEvidence: counter,
			Conflicts:       buildConflicts(branch, counter),
			NextActions:     buildNextActions(branch),
		})
	}

	weights, bias := baseScoreWeightsForModel(scorer)
	scores := scoreBatch(inputs, now, packetBranches, weights, bias)
	for i := range packets {
		packets[i].Score = scores[i]
		features[packetBranches[i].ID] = buildFeatureVector(
			query,
			packetBranches[i],
			inputs[i],
			packets[i].Support,
			packets[i].CounterEvidence,
			scores[i].Base,
			stage.relayMass[packetBranches[i].ID],
			stage.relayCofire[packetBranches[i].ID],
			stage.depths[packetBranches[i].ID],
			retrievalSubstrateSignal(packetBranches[i], stage.structuralSubstrate[packetBranches[i].ID], stage.queryScores[packetBranches[i].ID], stage.canopyRoots),
		)
	}
	return scoredRetrieval{packets: packets, featuresByBranch: features}, nil
}

func buildScoreInput(branch *Branch, stage retrievalStage, support []PacketEvidence, now time.Time) scoreInput {
	canopyScore := 0.0
	if _, ok := stage.canopyRoots[branch.RootID]; ok {
		canopyScore = 1.0
	}
	substrate := retrievalSubstrateSignal(branch, stage.structuralSubstrate[branch.ID], stage.queryScores[branch.ID], stage.canopyRoots)
	return scoreInput{
		QueryMatch:       stage.queryScores[branch.ID],
		Evidence:         evidenceSignal(support),
		Canopy:           canopyScore,
		Substrate:        substrate.Potential,
		Frontier:         substrate.Frontier,
		Confidence:       branch.Confidence,
		Recency:          branchRecency(branch, now),
		Warmth:           stage.warmth[branch.ID],
		Utility:          clamp01((branch.Utility + branch.SuccessRate) / 2),
		Salience:         branch.Salience,
		ConflictSafety:   1 - severityAdjustedConflict(branch),
		ScopeSafety:      1 - severityAdjustedScopeRisk(branch),
		InhibitionSafety: 1 - substrate.Inhibition,
	}
}

// severityAdjustedConflict scales a Constraint-family branch's
// ConflictScore by its severity (Phase 1 of Issue #11). Hard rules
// keep full effect (violation is an error); soft rules halve effect
// (violation is suboptimal). Non-Constraint families pass through
// unchanged.
func severityAdjustedConflict(branch *Branch) float64 {
	if branch == nil {
		return 0
	}
	if branch.Family != TreeFamilyConstraint {
		return branch.ConflictScore
	}
	if branch.ConstraintSeverity == ConstraintSeveritySoft {
		return branch.ConflictScore * 0.5
	}
	return branch.ConflictScore
}

// severityAdjustedScopeRisk scales a Constraint-family branch's
// ScopeRisk by its severity. Same intent as severityAdjustedConflict
// — hard constraint risks count fully; soft constraint risks are
// bounded penalty rather than blocking signal.
func severityAdjustedScopeRisk(branch *Branch) float64 {
	if branch == nil {
		return 0
	}
	if branch.Family != TreeFamilyConstraint {
		return branch.ScopeRisk
	}
	if branch.ConstraintSeverity == ConstraintSeveritySoft {
		return branch.ScopeRisk * 0.5
	}
	return branch.ScopeRisk
}

func branchIDList(branches []*Branch) []string {
	out := make([]string, 0, len(branches))
	for _, branch := range branches {
		out = append(out, branch.ID)
	}
	return out
}

func branchKeyList(branches []*Branch) []branchKey {
	out := make([]branchKey, 0, len(branches))
	for _, branch := range branches {
		out = append(out, branchKey{ID: branch.ID, Family: branch.Family})
	}
	return out
}

func canopyRootSet(canopy *Canopy) map[string]struct{} {
	if canopy == nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(canopy.RootIDs))
	for _, rootID := range canopy.RootIDs {
		out[rootID] = struct{}{}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────
// Audit bookkeeping
// ────────────────────────────────────────────────────────────────────

// newRetrievalAuditEvent constructs the audit shell. The ID is
// generated up front so downstream side-effects (training-example
// rows, retrieval-candidate projection) can use it as a stable join
// key even before the audit row hits disk. prepareRetrievalAuditEvent
// preserves any pre-set ID and only fills in when empty, so this
// remains backwards-compatible with callers that mutate the audit.
func newRetrievalAuditEvent(query Query, start time.Time, projectionSeq int64) *RetrievalAuditEvent {
	return &RetrievalAuditEvent{
		ID:                     "retrieval_" + uuid.NewString(),
		SessionID:              query.SessionID,
		TaskID:                 query.TaskID,
		AgentID:                query.AgentID,
		AgentType:              query.AgentType,
		IntentID:               query.IntentID,
		Query:                  query.Query,
		Horizon:                query.Horizon,
		Families:               append([]TreeFamily(nil), query.Families...),
		RequestedLimit:         query.Limit,
		IncludeCounterEvidence: query.IncludeCounterEvidence,
		RequestedAt:            start,
		BranchProjectionSeq:    projectionSeq,
		Candidates:             []RetrievalAuditCandidate{},
	}
}

func finalizeRetrievalAudit(audit *RetrievalAuditEvent, query Query, start time.Time, err error) {
	if audit == nil {
		return
	}
	audit.Duration = time.Since(start)
	if err != nil {
		audit.ErrorMessage = err.Error()
	}
	audit.RequestedLimit = query.Limit
}

// populateAuditCandidates fills audit.Candidates with the full ranked
// set BEFORE truncation to top-K. Each candidate's RankPosition is
// 1..K for those that will be returned; 0 for candidates that fall
// outside the requested limit. Called after sortPackets so order
// reflects the final ranking.
func populateAuditCandidates(audit *RetrievalAuditEvent, packets []*BranchPacket, features map[string][]float32, limit int) {
	if audit == nil {
		return
	}
	audit.CandidateCount = len(packets)
	if limit < 0 {
		limit = 0
	}
	returned := len(packets)
	if returned > limit {
		returned = limit
	}
	audit.ReturnedCount = returned

	candidates := make([]RetrievalAuditCandidate, 0, len(packets))
	for i, packet := range packets {
		entry := RetrievalAuditCandidate{
			BranchID:         packet.Branch.ID,
			RankPosition:     0,
			Returned:         false,
			BaseScore:        packet.Score.Base,
			FinalScore:       packet.Score.Total,
			PredictedUtility: predictedUtilityOrZero(packet.Prediction),
			PredictedRisk:    predictedRiskOrZero(packet.Prediction),
			Components:       scoreComponentsMap(packet.Score),
			FeatureVector:    cloneFeatureVector(features[packet.Branch.ID]),
		}
		if i < returned {
			entry.RankPosition = i + 1
			entry.Returned = true
		}
		candidates = append(candidates, entry)
	}
	audit.Candidates = candidates
}

func scoreComponentsMap(score PacketScore) map[string]float64 {
	return map[string]float64{
		"query_match":       score.QueryMatch,
		"evidence":          score.Evidence,
		"canopy":            score.Canopy,
		"substrate":         score.Substrate,
		"frontier":          score.Frontier,
		"confidence":        score.Confidence,
		"recency":           score.Recency,
		"warmth":            score.Warmth,
		"utility":           score.Utility,
		"salience":          score.Salience,
		"conflict_safety":   score.Conflict,
		"scope_safety":      score.ScopeSafety,
		"inhibition_safety": score.InhibitionSafety,
	}
}

func cloneFeatureVector(src []float32) []float32 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]float32, len(src))
	copy(dst, src)
	return dst
}

func predictedUtilityOrZero(p *LearnedPrediction) float64 {
	if p == nil {
		return 0
	}
	return p.Utility
}

func predictedRiskOrZero(p *LearnedPrediction) float64 {
	if p == nil {
		return 0
	}
	return p.Risk
}

// currentBranchProjectionSeq reads the current branch projector
// watermark for audit replayability. Returns 0 if unavailable
// (synchronous-projection mode, schema not yet migrated).
func (m *MemoryForest) currentBranchProjectionSeq(ctx context.Context) int64 {
	if m == nil || m.db == nil {
		return 0
	}
	row := m.db.QueryRowContext(ctx, `
		SELECT last_applied_seq
		FROM   forest_projector_state
		WHERE  projector_name = ?
	`, projectorBranchName)
	var seq int64
	if err := row.Scan(&seq); err != nil {
		return 0
	}
	return seq
}

// ResolveIntent returns the active intent frontier for the caller.
func (m *MemoryForest) ResolveIntent(ctx context.Context, input ResolveIntentInput) (*IntentResolution, error) {
	query, err := normalizeQuery(Query{
		Query:     input.Query,
		SessionID: input.SessionID,
		TaskID:    input.TaskID,
		IntentID:  input.IntentID,
		Horizon:   input.Horizon,
		Limit:     input.Limit,
	})
	if err != nil {
		return nil, err
	}
	canopy, err := m.resolveCanopy(ctx, query)
	if err != nil {
		return nil, err
	}

	packets, err := m.Retrieve(ctx, Query{
		Query:     query.Query,
		SessionID: query.SessionID,
		TaskID:    query.TaskID,
		AgentID:   input.AgentID,
		AgentType: input.AgentType,
		IntentID:  query.IntentID,
		Horizon:   query.Horizon,
		Limit:     maxInt(query.Limit, 8),
		Families: []TreeFamily{
			TreeFamilyIntent,
			TreeFamilyConstraint,
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
			// Issue #11 Phase 3: Preference is now Constraint with
			// severity=soft. Route soft-severity constraints into the
			// Preferences slot for the operator-facing IntentResolution
			// surface; hard-severity into Constraints.
			if packet.Branch.ConstraintSeverity == ConstraintSeveritySoft {
				resolution.Preferences = append(resolution.Preferences, *packet)
			} else {
				resolution.Constraints = append(resolution.Constraints, *packet)
			}
		case TreeFamilyOutcome:
			resolution.OutcomeHints = append(resolution.OutcomeHints, *packet)
		}
	}
	return resolution, nil
}

// PredictNextBranches retrieves low-risk adjacent-value branches.
func (m *MemoryForest) PredictNextBranches(ctx context.Context, query Query) ([]*BranchPacket, error) {
	var err error
	query, err = normalizeQuery(query)
	if err != nil {
		return nil, err
	}
	// Issue #11 Phase 3: Opportunity, Capability, Decision merged
	// into Intent. PredictNextBranches now scans the consolidated
	// Intent + Outcome surface to find adjacent-value branches.
	query.Families = []TreeFamily{
		TreeFamilyIntent,
		TreeFamilyOutcome,
	}
	query.IncludeCounterEvidence = true
	return m.Retrieve(ctx, query)
}

func normalizeQuery(query Query) (Query, error) {
	query.Query = strings.TrimSpace(query.Query)
	query.SessionID = strings.TrimSpace(query.SessionID)
	query.TaskID = normalizeForestTaskID(query.TaskID)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.AgentType = strings.TrimSpace(query.AgentType)
	query.IntentID = strings.TrimSpace(query.IntentID)
	if query.Limit <= 0 {
		query.Limit = 8
	}
	if query.Horizon == "" {
		switch {
		case query.TaskID != "":
			query.Horizon = CanopyHorizonTask
		case query.SessionID != "":
			query.Horizon = CanopyHorizonSession
		default:
			query.Horizon = CanopyHorizonProject
		}
	}
	switch query.Horizon {
	case CanopyHorizonTurn, CanopyHorizonTask, CanopyHorizonSession, CanopyHorizonUser, CanopyHorizonProject:
	default:
		return query, fmt.Errorf("invalid horizon %q", query.Horizon)
	}
	if query.Horizon == CanopyHorizonTask && query.TaskID == "" {
		return query, fmt.Errorf("task_id is required when horizon is task")
	}
	if len(query.Families) == 0 {
		query.Families = defaultFamilies()
	} else {
		// Canonicalize legacy family filters so callers using
		// TreeFamilyDecision/Preference/Capability/Opportunity/Conflict
		// retrieve from the consolidated families they were merged
		// into. Deprecated families never surface as branch.Family
		// post-migration, so without this rewrite the filter would
		// match nothing.
		query.Families = canonicalizeFamilies(query.Families)
	}
	return query, nil
}

func (m *MemoryForest) loadCandidateBranches(ctx context.Context, query Query) ([]*Branch, map[string]float64, error) {
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		branchMap   = make(map[string]*Branch)
		queryScores = make(map[string]float64)
		firstErr    error
	)

	load := func(sessionID, taskID string, semanticOnly bool) {
		defer wg.Done()
		rows, err := m.queryBranches(ctx, sessionID, taskID, semanticOnly, query.Families, 128)
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

	if query.TaskID != "" {
		wg.Add(1)
		go load(query.SessionID, query.TaskID, false)
	} else if query.SessionID != "" {
		wg.Add(1)
		go load(query.SessionID, "", false)
	}
	wg.Add(1)
	go load("", "", true)
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

func (m *MemoryForest) queryBranches(ctx context.Context, sessionID, taskID string, semanticOnly bool, families []TreeFamily, limit int) ([]*Branch, error) {
	query := `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata,
		       last_applied_seq, constraint_severity
		FROM forest_branches
		WHERE state != ?
	`
	args := []any{string(BranchStateSuperseded)}

	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if taskID != "" {
		query += " AND task_id = ?"
		args = append(args, taskID)
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
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata,
		       last_applied_seq, constraint_severity
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
	candidates := buildCanopyLookupCandidates(query)
	for _, candidate := range candidates {
		canopy, found, err := m.loadCanopyCandidate(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if found {
			return canopy, nil
		}
	}

	if query.SessionID == "" && query.Horizon != CanopyHorizonProject && query.Horizon != CanopyHorizonTask {
		query.Horizon = CanopyHorizonProject
		query.IntentID = ""
		return m.resolveCanopy(ctx, query)
	}

	return &Canopy{
		Key:       canopyKey(query.Horizon, query.SessionID, query.TaskID, query.IntentID),
		SessionID: query.SessionID,
		TaskID:    query.TaskID,
		IntentID:  query.IntentID,
		Horizon:   query.Horizon,
		UpdatedAt: time.Now().UTC(),
	}, nil
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
				if !matchesEvidenceScope(entry, query) {
					continue
				}
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
				if !matchesEvidenceScope(entry, query) {
					continue
				}
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

type canopyLookupCandidate struct {
	Horizon   CanopyHorizon
	SessionID string
	TaskID    string
	IntentID  string
}

func buildCanopyLookupCandidates(query Query) []canopyLookupCandidate {
	sessionID := normalizeForestSessionID(query.SessionID)
	taskID := normalizeForestTaskID(query.TaskID)
	intentID := strings.TrimSpace(query.IntentID)

	candidates := make([]canopyLookupCandidate, 0, 2)
	if intentID != "" {
		candidates = append(candidates, canopyLookupCandidate{
			Horizon:   query.Horizon,
			SessionID: sessionID,
			TaskID:    taskID,
			IntentID:  intentID,
		})
	}
	candidates = append(candidates, canopyLookupCandidate{
		Horizon:   query.Horizon,
		SessionID: sessionID,
		TaskID:    taskID,
		IntentID:  "",
	})
	return candidates
}

func (m *MemoryForest) loadCanopyCandidate(ctx context.Context, candidate canopyLookupCandidate) (*Canopy, bool, error) {
	statement := `
		SELECT canopy_key, session_id, task_id, intent_id, horizon, root_ids, summary, updated_at
		FROM forest_canopies
		WHERE horizon = ?
	`
	args := []any{string(candidate.Horizon)}

	switch candidate.Horizon {
	case CanopyHorizonTask:
		if candidate.TaskID == "" {
			return nil, false, nil
		}
		statement += " AND task_id = ?"
		args = append(args, candidate.TaskID)
		if candidate.SessionID != "" {
			statement += " AND session_id = ?"
			args = append(args, candidate.SessionID)
		}
	case CanopyHorizonSession:
		if candidate.SessionID == "" {
			return nil, false, nil
		}
		statement += " AND session_id = ?"
		args = append(args, candidate.SessionID)
	case CanopyHorizonProject:
		statement += " AND COALESCE(session_id, '') = '' AND COALESCE(task_id, '') = ''"
	default:
		if candidate.SessionID == "" {
			return nil, false, nil
		}
		statement += " AND session_id = ?"
		args = append(args, candidate.SessionID)
	}

	if candidate.IntentID != "" {
		statement += " AND intent_id = ?"
		args = append(args, candidate.IntentID)
	} else {
		statement += " AND COALESCE(intent_id, '') = ''"
	}
	statement += " ORDER BY updated_at DESC LIMIT 1"

	row := m.db.QueryRowContext(ctx, statement, args...)

	var (
		canopy    Canopy
		sessionID sql.NullString
		taskID    sql.NullString
		intentID  sql.NullString
		rootIDs   string
		updatedAt int64
	)
	err := row.Scan(&canopy.Key, &sessionID, &taskID, &intentID, &canopy.Horizon, &rootIDs, &canopy.Summary, &updatedAt)
	switch {
	case err == sql.ErrNoRows:
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("load canopy: %w", err)
	}

	canopy.SessionID = normalizeForestSessionID(sessionID.String)
	canopy.TaskID = normalizeForestTaskID(taskID.String)
	canopy.IntentID = strings.TrimSpace(intentID.String)
	canopy.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	_ = unmarshalJSON(rootIDs, &canopy.RootIDs)
	return &canopy, true, nil
}

func matchesEvidenceScope(entry *ctxpkg.ContentEntry, query Query) bool {
	if entry == nil {
		return false
	}
	if query.SessionID != "" && normalizeForestSessionID(entry.SessionID) != normalizeForestSessionID(query.SessionID) {
		return false
	}
	if query.TaskID != "" && forestTaskIDFromStringMetadata(entry.Metadata) != normalizeForestTaskID(query.TaskID) {
		return false
	}
	return true
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
		SELECT r.ref_id, l.subject_id
		FROM forest_ledger_refs r
		JOIN forest_ledger l ON l.id = r.ledger_id
		WHERE l.source_kind = ?
		  AND l.subject_type = 'branch'
		  AND r.role = 'content'
		  AND r.ref_type = 'content'
		  AND r.ref_id IN (`+strings.Join(placeholders, ",")+`)
	`, append([]any{string(LedgerSourceForestEvent)}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("query forest ledger refs by content: %w", err)
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
		SELECT l.occurred_at, p.payload
		FROM forest_ledger l
		JOIN forest_ledger_payloads p ON p.ledger_id = l.id
		WHERE l.source_kind = ?
		  AND l.subject_type = 'branch'
		  AND l.subject_id = ?
		ORDER BY l.occurred_at DESC, l.seq DESC
		LIMIT ?
	`, string(LedgerSourceForestEvent), branchID, limit*3)
	if err != nil {
		return nil, nil, fmt.Errorf("query branch evidence ledger: %w", err)
	}
	defer rows.Close()

	support := make([]PacketEvidence, 0, limit)
	counter := make([]PacketEvidence, 0, limit)
	for rows.Next() {
		var (
			occurredAt int64
			payloadRaw string
		)
		if err := rows.Scan(&occurredAt, &payloadRaw); err != nil {
			return nil, nil, fmt.Errorf("scan branch evidence: %w", err)
		}
		var event Event
		if err := unmarshalJSON(payloadRaw, &event); err != nil {
			return nil, nil, fmt.Errorf("decode branch evidence event: %w", err)
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Unix(occurredAt, 0).UTC()
		}

		evidence := PacketEvidence{
			ContentID:      event.ContentID,
			Summary:        chooseText(event.Summary, event.Title),
			Confidence:     event.Confidence,
			Salience:       event.Salience,
			Timestamp:      event.Timestamp,
			ProvenanceRefs: append([]string(nil), event.ProvenanceRefs...),
		}
		if raw, ok := event.Payload["content_type"].(string); ok {
			evidence.ContentType = raw
		}

		if len(event.Contradicts) > 0 && includeCounter {
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
	// Canonicalize so any branch row that slipped through with a
	// deprecated family (e.g. external writer pre-migration) still
	// gets a sensible action for its post-Phase-3 successor.
	switch canonicalizeFamily(branch.Family) {
	case TreeFamilyIntent:
		return []PacketAction{{Label: "refine", Description: "refine the active intent into stronger constraints or subgoals"}}
	case TreeFamilyConstraint:
		if branch.ConstraintSeverity == ConstraintSeveritySoft {
			return []PacketAction{{Label: "adapt", Description: "adjust tone, scope, or tradeoffs to match this preference"}}
		}
		return []PacketAction{{Label: "respect", Description: "use this branch as a hard constraint during planning and execution"}}
	case TreeFamilyEvidence:
		return []PacketAction{{Label: "ground", Description: "use this evidence to justify or challenge the current branch"}}
	case TreeFamilyOutcome:
		return []PacketAction{{Label: "learn", Description: "feed this outcome back into intent and constraint priors"}}
	case TreeFamilyAntiPattern:
		return []PacketAction{{Label: "avoid", Description: "treat this branch as a known anti-pattern; prefer alternative approaches"}}
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
