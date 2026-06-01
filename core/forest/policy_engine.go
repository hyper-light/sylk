package forest

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	PolicyStatusActive              = "active"
	PolicyStatusRetired             = "retired"
	PolicyCandidateStatusTrialing   = "trialing"
	PolicyCandidateStatusProposed   = "promotion_proposed"
	PolicyCandidateStatusRejected   = "rejected"
	PolicyCandidateStatusPromoted   = "promoted"
	PolicyTrialArmChampion          = "champion"
	PolicyTrialArmChallenger        = "challenger"
	PolicyOutcomeArmChampion        = "champion"
	PolicyOutcomeArmChallenger      = "challenger"
	PolicySourceBootstrap           = "bootstrap"
	PolicySourceHyperHeuristic      = "hyper_heuristic"
	PolicyPromotionArtifactKind     = "forest_policy_promotion"
	PolicyPromotionDimension        = "policy_promotion"
	PolicyRollbackUsePreviousActive = "rollback_to_previous_active_policy"
)

//go:generate mockery --name=PolicyOutcomeSource --inpackage --output=. --filename=mock_policy_outcome_source_test.go --outpkg=forest
type PolicyOutcomeSource interface {
	PolicyOutcomes(ctx context.Context, candidateID string, limit int) ([]PolicyOutcome, error)
}

type ForestPolicyGenome struct {
	Version               string                            `json:"version"`
	Retrieval             RetrievalPolicyGenome             `json:"retrieval"`
	Pruning               BoundedPolicySwitch               `json:"pruning"`
	Replay                BoundedPolicySwitch               `json:"replay"`
	Clustering            BoundedPolicySwitch               `json:"clustering"`
	SubstrateUpdate       BoundedPolicySwitch               `json:"substrate_update"`
	ValidationRouting     ValidationRoutingPolicyGenome     `json:"validation_routing"`
	ContradictionHandling ContradictionHandlingPolicyGenome `json:"contradiction_handling"`
	SkillGeneration       SkillGenerationPolicyGenome       `json:"skill_generation"`
	Provenance            []string                          `json:"provenance,omitempty"`
	Permissions           []string                          `json:"permissions,omitempty"`
}

type RetrievalPolicyGenome struct {
	ExplorationRate      float64 `json:"exploration_rate"`
	DiversityLambda      float64 `json:"diversity_lambda"`
	CounterfactualWeight float64 `json:"counterfactual_weight"`
	BaseScoreL2Reg       float64 `json:"base_score_l2_reg"`
	RetrievalCooldown    float64 `json:"retrieval_cooldown"`
}

type BoundedPolicySwitch struct {
	Strategy string  `json:"strategy"`
	Budget   int     `json:"budget"`
	Weight   float64 `json:"weight"`
}

type ValidationRoutingPolicyGenome struct {
	RequireIndependentSignals int     `json:"require_independent_signals"`
	GuardianReviewThreshold   float64 `json:"guardian_review_threshold"`
}

type ContradictionHandlingPolicyGenome struct {
	QuarantineThreshold  float64 `json:"quarantine_threshold"`
	RemediationThreshold int     `json:"remediation_threshold"`
}

type SkillGenerationPolicyGenome struct {
	PopulationLimit int  `json:"population_limit"`
	GenerationLimit int  `json:"generation_limit"`
	ProposalOnly    bool `json:"proposal_only"`
}

type ForestPolicy struct {
	ID                     string
	Version                string
	Status                 string
	Source                 string
	Genome                 ForestPolicyGenome
	HyperParameters        *HyperParameters
	ValidationEvidenceRefs []string
	RollbackPolicy         string
	Fitness                PolicyFitness
	ActiveSince            time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Metadata               map[string]any
}

type PolicyCandidate struct {
	ID                     string
	ParentPolicyID         string
	Version                string
	Genome                 ForestPolicyGenome
	HyperParameters        *HyperParameters
	Status                 string
	Source                 string
	EvidenceRefs           []string
	ValidationEvidenceRefs []string
	RejectionReason        string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Metadata               map[string]any
}

type PolicyOutcome struct {
	CandidateID        string
	PolicyID           string
	TrialID            string
	Arm                string
	Correctness        float64
	Safety             float64
	Cost               float64
	Ecology            float64
	ValidationPassRate float64
	ArtifactQuality    float64
	LatencyMicros      int64
	ContradictionDelta float64
	SampleCount        int64
	EvidenceRefs       []string
	ObservedAt         time.Time
	Metadata           map[string]any
}

type PolicyFitness struct {
	Correctness            float64  `json:"correctness"`
	Safety                 float64  `json:"safety"`
	Cost                   float64  `json:"cost"`
	Ecology                float64  `json:"ecology"`
	ValidationPassRate     float64  `json:"validation_pass_rate"`
	ArtifactQuality        float64  `json:"artifact_quality"`
	ContradictionReduction float64  `json:"contradiction_reduction"`
	LatencyMicros          int64    `json:"latency_micros"`
	SampleCount            int64    `json:"sample_count"`
	ConfidenceLowerBound   float64  `json:"confidence_lower_bound"`
	EvidenceRefs           []string `json:"evidence_refs"`
	IndependentSignalCount int      `json:"independent_signal_count"`
	PromotionAllowed       bool     `json:"promotion_allowed"`
	PromotionBlockedReason string   `json:"promotion_blocked_reason,omitempty"`
}

type policyCandidateProposal struct {
	current *HyperParameters
	recent  []AdaptationObservation
}

type ForestPolicyEngine struct {
	db           *sql.DB
	store        *HyperParameterStore
	logger       *slog.Logger
	proposalSink ClaimProposalSink
	promotionMu  sync.Mutex
}

func NewForestPolicyEngine(ctx context.Context, db *sql.DB, store *HyperParameterStore, logger *slog.Logger, sink ClaimProposalSink) (*ForestPolicyEngine, error) {
	if db == nil {
		return nil, fmt.Errorf("policy engine: nil db")
	}
	if err := ensureForestPolicySchema(db); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	engine := &ForestPolicyEngine{db: db, store: store, logger: logger, proposalSink: sink}
	return engine, nil
}

func (e *ForestPolicyEngine) EnsureChampionPolicy(ctx context.Context, hp *HyperParameters, evidenceRefs []string) error {
	if e == nil {
		return nil
	}
	policy := policyFromHyperParameters(hp, PolicySourceBootstrap, evidenceRefs)
	active, err := e.loadActivePolicy(ctx)
	if err == nil && hyperparameterValuesEqual(active.HyperParameters, policy.HyperParameters) {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin champion policy tx: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE forest_policies SET status = ?, updated_at = ? WHERE status = ?`, PolicyStatusRetired, now, PolicyStatusActive); err != nil {
		return fmt.Errorf("retire previous champion policy: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO forest_policies
			(policy_id, policy_version, status, source, genome, hyperparameters,
			 validation_evidence_refs, rollback_policy, fitness, active_since,
			 created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(policy_id) DO UPDATE SET
			status = excluded.status,
			hyperparameters = excluded.hyperparameters,
			validation_evidence_refs = excluded.validation_evidence_refs,
			active_since = excluded.active_since,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`, policy.ID, policy.Version, policy.Status, policy.Source, marshalJSON(policy.Genome), marshalJSON(policy.HyperParameters),
		encodeStringList(policy.ValidationEvidenceRefs), policy.RollbackPolicy, marshalJSON(policy.Fitness),
		policy.ActiveSince.Unix(), policy.CreatedAt.Unix(), policy.UpdatedAt.Unix(), marshalJSON(policy.Metadata))
	if err != nil {
		return fmt.Errorf("ensure champion policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit champion policy tx: %w", err)
	}
	return nil
}

func policyFromHyperParameters(hp *HyperParameters, source string, evidenceRefs []string) ForestPolicy {
	if hp == nil {
		hp = PlaceholderHyperParameters()
	}
	now := time.Now().UTC()
	genome := PolicyGenomeFromHyperParameters(hp)
	policyID := stableID("forest_policy", genome.Version, marshalJSON(genome), encodeStringList(evidenceRefs))
	return ForestPolicy{
		ID:                     policyID,
		Version:                genome.Version,
		Status:                 PolicyStatusActive,
		Source:                 source,
		Genome:                 genome,
		HyperParameters:        hp.Clone(),
		ValidationEvidenceRefs: dedupeStrings(evidenceRefs),
		RollbackPolicy:         PolicyRollbackUsePreviousActive,
		Fitness:                PolicyFitness{PromotionAllowed: true, EvidenceRefs: dedupeStrings(evidenceRefs), IndependentSignalCount: policyRequiredSignalCount()},
		ActiveSince:            now,
		CreatedAt:              now,
		UpdatedAt:              now,
		Metadata:               map[string]any{"snapshot_id": hp.SnapshotID},
	}
}

func PolicyGenomeFromHyperParameters(hp *HyperParameters) ForestPolicyGenome {
	if hp == nil {
		hp = PlaceholderHyperParameters()
	}
	return ForestPolicyGenome{
		Version: "policy_genome_v1",
		Retrieval: RetrievalPolicyGenome{
			ExplorationRate:      hp.ExplorationRate,
			DiversityLambda:      hp.DiversityLambda,
			CounterfactualWeight: hp.CounterfactualWeight,
			BaseScoreL2Reg:       hp.BaseScoreL2Reg,
			RetrievalCooldown:    hp.RetrievalCooldownPenalty,
		},
		Pruning:               defaultBoundedPolicySwitch("bounded_prune"),
		Replay:                defaultBoundedPolicySwitch("bounded_replay"),
		Clustering:            defaultBoundedPolicySwitch("bounded_cluster"),
		SubstrateUpdate:       defaultBoundedPolicySwitch("bounded_substrate"),
		ValidationRouting:     defaultValidationRoutingGenome(),
		ContradictionHandling: defaultContradictionHandlingGenome(),
		SkillGeneration:       defaultSkillGenerationGenome(),
		Provenance:            []string{"hyperparameters"},
	}
}

func defaultBoundedPolicySwitch(strategy string) BoundedPolicySwitch {
	return BoundedPolicySwitch{Strategy: strategy, Budget: policyGenerationLimit(), Weight: 1}
}

func defaultValidationRoutingGenome() ValidationRoutingPolicyGenome {
	return ValidationRoutingPolicyGenome{
		RequireIndependentSignals: policyRequiredSignalCount(),
		GuardianReviewThreshold:   1 / float64(nodeProjectionRetryLimit()),
	}
}

func defaultContradictionHandlingGenome() ContradictionHandlingPolicyGenome {
	return ContradictionHandlingPolicyGenome{
		QuarantineThreshold:  1 / float64(nodeProjectionRetryLimit()),
		RemediationThreshold: nodeProjectionRetryLimit(),
	}
}

func defaultSkillGenerationGenome() SkillGenerationPolicyGenome {
	return SkillGenerationPolicyGenome{
		PopulationLimit: policyCandidatePopulationLimit(),
		GenerationLimit: policyGenerationLimit(),
		ProposalOnly:    true,
	}
}

func (g ForestPolicyGenome) Validate() error {
	if strings.TrimSpace(g.Version) == "" {
		return fmt.Errorf("policy genome version is required")
	}
	if len(g.Permissions) > 0 {
		return fmt.Errorf("policy genome cannot request permissions")
	}
	if err := validateRetrievalGenome(g.Retrieval); err != nil {
		return err
	}
	if !g.SkillGeneration.ProposalOnly {
		return fmt.Errorf("skill generation policy must be proposal-only")
	}
	return validatePolicyBudget(g.SkillGeneration.PopulationLimit, g.SkillGeneration.GenerationLimit)
}

func validateRetrievalGenome(g RetrievalPolicyGenome) error {
	hp := PlaceholderHyperParameters()
	if !withinPolicyBound(g.ExplorationRate, 0, hp.ExplorationRate*float64(policyCandidatePopulationLimit())) {
		return fmt.Errorf("exploration_rate outside policy bounds")
	}
	if !withinPolicyBound(g.DiversityLambda, 0, 1) {
		return fmt.Errorf("diversity_lambda outside policy bounds")
	}
	if !withinPolicyBound(g.CounterfactualWeight, 0, 1) {
		return fmt.Errorf("counterfactual_weight outside policy bounds")
	}
	if !withinPolicyBound(g.BaseScoreL2Reg, 0, 1) {
		return fmt.Errorf("base_score_l2_reg outside policy bounds")
	}
	return nil
}

func validatePolicyBudget(population, generations int) error {
	if population <= 0 || population > policyCandidatePopulationLimit() {
		return fmt.Errorf("policy population outside bounds")
	}
	if generations <= 0 || generations > policyGenerationLimit() {
		return fmt.Errorf("policy generation count outside bounds")
	}
	return nil
}

func withinPolicyBound(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func (e *ForestPolicyEngine) ProposeCandidateFromObservations(ctx context.Context, current *HyperParameters, recent []AdaptationObservation) (*PolicyCandidate, error) {
	proposal := policyCandidateProposal{current: current, recent: recent}
	candidate, err := e.buildPolicyCandidate(ctx, proposal)
	if err != nil || candidate == nil {
		return candidate, err
	}
	if err := e.insertPolicyCandidate(ctx, candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (e *ForestPolicyEngine) buildPolicyCandidate(ctx context.Context, proposal policyCandidateProposal) (*PolicyCandidate, error) {
	if e == nil {
		return nil, fmt.Errorf("policy engine is required")
	}
	if ok, err := e.candidatePopulationAvailable(ctx); err != nil || !ok {
		return nil, err
	}
	current := proposal.current
	if current == nil {
		current = PlaceholderHyperParameters()
	}
	hp := mutatePolicyHyperParameters(current, proposal.recent)
	if hyperparameterValuesEqual(current, hp) {
		return nil, nil
	}
	genome := PolicyGenomeFromHyperParameters(hp)
	if err := genome.Validate(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	candidateID := stableID("policy_candidate", fmt.Sprint(current.SnapshotID), marshalJSON(genome), policyObservationSignature(proposal.recent))
	return &PolicyCandidate{
		ID:              candidateID,
		ParentPolicyID:  e.activePolicyID(ctx),
		Version:         genome.Version,
		Genome:          genome,
		HyperParameters: hp,
		Status:          PolicyCandidateStatusTrialing,
		Source:          PolicySourceHyperHeuristic,
		EvidenceRefs:    evidenceRefsFromObservations(proposal.recent),
		CreatedAt:       now,
		UpdatedAt:       now,
		Metadata: map[string]any{
			"observation_count": len(proposal.recent),
		},
	}, nil
}

func mutatePolicyHyperParameters(current *HyperParameters, recent []AdaptationObservation) *HyperParameters {
	candidate := current.Clone()
	avgRegret, avgCal := policyObservationAverages(recent)
	if avgRegret > 1/float64(policyCandidatePopulationLimit()) && current.Provenance["exploration_rate"] != SourceConfig {
		candidate.ExplorationRate = clamp(candidate.ExplorationRate*(1+1/float64(nodeProjectionRetryLimit())), 0, PlaceholderHyperParameters().ExplorationRate*float64(policyCandidatePopulationLimit()))
	}
	if avgCal > 1/float64(nodeProjectionRetryLimit()) && current.Provenance["base_score_l2_reg"] != SourceConfig {
		candidate.BaseScoreL2Reg = clamp(candidate.BaseScoreL2Reg*(1+1/float64(nodeProjectionRetryLimit())), 0, 1)
	}
	if avgCal > 1/float64(nodeProjectionRetryLimit()) && current.Provenance["counterfactual_weight"] != SourceConfig {
		candidate.CounterfactualWeight = clamp(candidate.CounterfactualWeight*(1-1/float64(policyCandidatePopulationLimit())), 0, 1)
	}
	return candidate
}

func policyObservationAverages(recent []AdaptationObservation) (float64, float64) {
	if len(recent) == 0 {
		return 0, 0
	}
	var regret, calibration float64
	for _, obs := range recent {
		regret += obs.RegretFrac
		calibration += obs.CalibrationError
	}
	return regret / float64(len(recent)), calibration / float64(len(recent))
}

func policyObservationSignature(recent []AdaptationObservation) string {
	regret, calibration := policyObservationAverages(recent)
	return fmt.Sprintf("n=%d:regret=%.4f:cal=%.4f", len(recent), regret, calibration)
}

func evidenceRefsFromObservations(recent []AdaptationObservation) []string {
	var refs []string
	for idx, obs := range recent {
		if len(obs.EvidenceRefs) > 0 {
			refs = append(refs, obs.EvidenceRefs...)
			continue
		}
		refs = append(refs, "adaptation_observation:"+stableID(
			fmt.Sprint(idx),
			fmt.Sprintf("%.6f", obs.CalibrationError),
			fmt.Sprintf("%.6f", obs.RegretFrac),
			fmt.Sprint(obs.SampleCount),
			obs.ObservedAt.UTC().Format(time.RFC3339Nano),
		))
	}
	return dedupeStrings(refs)
}

func (e *ForestPolicyEngine) candidatePopulationAvailable(ctx context.Context) (bool, error) {
	var count int
	err := e.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM forest_policy_candidates
			WHERE status IN (?, ?)
		`, PolicyCandidateStatusTrialing, PolicyCandidateStatusProposed).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count policy candidates: %w", err)
	}
	return count < policyCandidatePopulationLimit(), nil
}

func (e *ForestPolicyEngine) activePolicyID(ctx context.Context) string {
	var policyID string
	_ = e.db.QueryRowContext(ctx, `
		SELECT policy_id
		FROM forest_policies
		WHERE status = ?
		ORDER BY active_since DESC
		LIMIT 1
	`, PolicyStatusActive).Scan(&policyID)
	return policyID
}

func (e *ForestPolicyEngine) loadActivePolicy(ctx context.Context) (*ForestPolicy, error) {
	row := e.db.QueryRowContext(ctx, `
		SELECT policy_id, policy_version, status, source, genome, hyperparameters,
		       validation_evidence_refs, rollback_policy, fitness, active_since,
		       created_at, updated_at, metadata
		FROM forest_policies
		WHERE status = ?
		ORDER BY active_since DESC, updated_at DESC
		LIMIT 1
	`, PolicyStatusActive)
	return scanForestPolicy(row)
}

func scanForestPolicy(row interface{ Scan(dest ...any) error }) (*ForestPolicy, error) {
	var genomeJSON, hpJSON, validationRefs, fitnessJSON, metadata string
	var activeSince, createdAt, updatedAt int64
	policy := &ForestPolicy{}
	if err := row.Scan(&policy.ID, &policy.Version, &policy.Status, &policy.Source, &genomeJSON, &hpJSON,
		&validationRefs, &policy.RollbackPolicy, &fitnessJSON, &activeSince, &createdAt, &updatedAt, &metadata); err != nil {
		return nil, err
	}
	policy.HyperParameters = &HyperParameters{}
	if err := unmarshalJSON(genomeJSON, &policy.Genome); err != nil {
		return nil, fmt.Errorf("scan forest policy genome: %w", err)
	}
	if err := unmarshalJSON(hpJSON, policy.HyperParameters); err != nil {
		return nil, fmt.Errorf("scan forest policy hyperparameters: %w", err)
	}
	_ = unmarshalJSON(fitnessJSON, &policy.Fitness)
	policy.ValidationEvidenceRefs = decodeStringList(validationRefs)
	policy.ActiveSince = time.Unix(activeSince, 0).UTC()
	policy.CreatedAt = time.Unix(createdAt, 0).UTC()
	policy.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	policy.Metadata = map[string]any{}
	_ = unmarshalJSON(metadata, &policy.Metadata)
	return policy, nil
}

func (e *ForestPolicyEngine) insertPolicyCandidate(ctx context.Context, candidate *PolicyCandidate) error {
	if candidate == nil {
		return fmt.Errorf("policy candidate is required")
	}
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO forest_policy_candidates
			(candidate_id, parent_policy_id, policy_version, genome, hyperparameters,
			 status, source, evidence_refs, validation_evidence_refs, rejection_reason,
			 created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(candidate_id) DO UPDATE SET
			updated_at = excluded.updated_at
	`, candidate.ID, candidate.ParentPolicyID, candidate.Version, marshalJSON(candidate.Genome), marshalJSON(candidate.HyperParameters),
		candidate.Status, candidate.Source, encodeStringList(candidate.EvidenceRefs), encodeStringList(candidate.ValidationEvidenceRefs),
		candidate.RejectionReason, candidate.CreatedAt.Unix(), candidate.UpdatedAt.Unix(), marshalJSON(candidate.Metadata))
	if err != nil {
		return fmt.Errorf("insert policy candidate: %w", err)
	}
	return nil
}

func (e *ForestPolicyEngine) RejectCandidate(ctx context.Context, candidateID, reason string) error {
	if e == nil {
		return nil
	}
	return e.markPolicyCandidateRejected(ctx, candidateID, reason)
}

func (e *ForestPolicyEngine) AssignTrial(ctx context.Context, candidateID, cohortKey string, seed uint32) (string, string, error) {
	if e == nil {
		return "", "", fmt.Errorf("policy engine is required")
	}
	arm := PolicyTrialArmChampion
	if seed%uint32(policyTrialArmCount()) == 0 {
		arm = PolicyTrialArmChallenger
	}
	trialID := stableID("policy_trial", candidateID, strings.TrimSpace(cohortKey), fmt.Sprint(seed))
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO forest_policy_trials
			(trial_id, candidate_id, champion_policy_id, cohort_key, assigned_arm,
			 seed, started_at, status, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(trial_id) DO NOTHING
	`, trialID, candidateID, e.activePolicyID(ctx), strings.TrimSpace(cohortKey), arm, int64(seed), time.Now().UTC().Unix(), "assigned", "{}")
	if err != nil {
		return "", "", fmt.Errorf("assign policy trial: %w", err)
	}
	return trialID, arm, nil
}

func (e *ForestPolicyEngine) RecordPolicyOutcome(ctx context.Context, outcome PolicyOutcome) error {
	outcome = normalizePolicyOutcome(outcome)
	if outcome.CandidateID == "" || outcome.PolicyID == "" {
		return fmt.Errorf("policy outcome requires candidate_id and policy_id")
	}
	_, err := e.db.ExecContext(ctx, `
			INSERT INTO forest_policy_outcomes
				(outcome_id, candidate_id, policy_id, trial_id, arm, correctness,
				 safety, cost, ecology, validation_pass_rate, latency_micros,
				 artifact_quality, contradiction_delta, sample_count, evidence_refs,
				 observed_at, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(outcome_id) DO NOTHING
		`, policyOutcomeID(outcome), outcome.CandidateID, outcome.PolicyID, outcome.TrialID, outcome.Arm,
		outcome.Correctness, outcome.Safety, outcome.Cost, outcome.Ecology, outcome.ValidationPassRate,
		outcome.LatencyMicros, outcome.ArtifactQuality, outcome.ContradictionDelta, outcome.SampleCount,
		encodeStringList(outcome.EvidenceRefs), outcome.ObservedAt.Unix(), marshalJSON(outcome.Metadata))
	if err != nil {
		return fmt.Errorf("record policy outcome: %w", err)
	}
	return nil
}

func normalizePolicyOutcome(outcome PolicyOutcome) PolicyOutcome {
	outcome.CandidateID = strings.TrimSpace(outcome.CandidateID)
	outcome.PolicyID = strings.TrimSpace(outcome.PolicyID)
	outcome.TrialID = strings.TrimSpace(outcome.TrialID)
	outcome.Arm = strings.TrimSpace(outcome.Arm)
	outcome.Correctness = clampFinite01(outcome.Correctness)
	outcome.Safety = clampFinite01(outcome.Safety)
	outcome.Cost = clampFinite01(outcome.Cost)
	outcome.Ecology = clampFinite01(outcome.Ecology)
	outcome.ValidationPassRate = clampFinite01(outcome.ValidationPassRate)
	outcome.ArtifactQuality = clampFinite01(outcome.ArtifactQuality)
	outcome.EvidenceRefs = dedupeStrings(outcome.EvidenceRefs)
	if outcome.ObservedAt.IsZero() {
		outcome.ObservedAt = time.Now().UTC()
	}
	if outcome.SampleCount <= 0 {
		outcome.SampleCount = 1
	}
	return outcome
}

func policyOutcomeID(outcome PolicyOutcome) string {
	return stableID("policy_outcome", outcome.CandidateID, outcome.PolicyID, outcome.TrialID, outcome.Arm, outcome.ObservedAt.Format(time.RFC3339Nano), encodeStringList(outcome.EvidenceRefs))
}

func ComputePolicyFitness(champion, challenger []PolicyOutcome) PolicyFitness {
	fitness := aggregatePolicyFitness(challenger)
	championFitness := aggregatePolicyFitness(champion)
	fitness.PromotionAllowed, fitness.PromotionBlockedReason = policyPromotionDecision(championFitness, fitness)
	return fitness
}

func aggregatePolicyFitness(outcomes []PolicyOutcome) PolicyFitness {
	var f PolicyFitness
	if len(outcomes) == 0 {
		f.PromotionBlockedReason = "missing_outcomes"
		return f
	}
	var totalLatency int64
	for _, outcome := range outcomes {
		normalized := normalizePolicyOutcome(outcome)
		f.Correctness += normalized.Correctness
		f.Safety += normalized.Safety
		f.Cost += normalized.Cost
		f.Ecology += normalized.Ecology
		f.ValidationPassRate += normalized.ValidationPassRate
		f.ArtifactQuality += normalized.ArtifactQuality
		f.ContradictionReduction += normalized.ContradictionDelta
		f.SampleCount += normalized.SampleCount
		f.EvidenceRefs = append(f.EvidenceRefs, normalized.EvidenceRefs...)
		totalLatency += normalized.LatencyMicros
	}
	denom := float64(len(outcomes))
	f.Correctness /= denom
	f.Safety /= denom
	f.Cost /= denom
	f.Ecology /= denom
	f.ValidationPassRate /= denom
	f.ArtifactQuality /= denom
	f.ContradictionReduction /= denom
	f.EvidenceRefs = dedupeStrings(f.EvidenceRefs)
	f.IndependentSignalCount = policyIndependentSignals(f)
	f.ConfidenceLowerBound = policyConfidenceLowerBound(f)
	f.LatencyMicros = totalLatency / int64(len(outcomes))
	return f
}

func policyPromotionDecision(champion, challenger PolicyFitness) (bool, string) {
	if challenger.SampleCount < int64(policyMinimumOutcomeSamples()) {
		return false, "sample_size_below_confidence_floor"
	}
	if len(challenger.EvidenceRefs) == 0 || challenger.ValidationPassRate <= 0 {
		return false, "missing_validation_evidence"
	}
	if challenger.ArtifactQuality <= 0 {
		return false, "missing_artifact_quality_evidence"
	}
	if challenger.IndependentSignalCount < policyRequiredSignalCount() {
		return false, "insufficient_independent_signals"
	}
	if challenger.Safety < champion.Safety || challenger.ValidationPassRate < champion.ValidationPassRate {
		return false, "safety_or_validation_regressed"
	}
	if challenger.ConfidenceLowerBound <= champion.ConfidenceLowerBound {
		return false, "confidence_interval_does_not_clear_champion"
	}
	return true, ""
}

func policyIndependentSignals(f PolicyFitness) int {
	count := 0
	for _, value := range []float64{f.Correctness, f.Safety, f.Cost, f.Ecology} {
		if value > 0 {
			count++
		}
	}
	return count
}

func policyConfidenceLowerBound(f PolicyFitness) float64 {
	total := (f.Correctness + f.Safety + f.Cost + f.Ecology) / float64(policyRequiredSignalCount())
	margin := 1 / math.Sqrt(float64(maxInt64(f.SampleCount, 1))*float64(policyCandidatePopulationLimit()))
	return clampFinite01(total - margin)
}

func (e *ForestPolicyEngine) ProposeCandidatePromotion(ctx context.Context, candidateID string, fitness PolicyFitness) error {
	e.promotionMu.Lock()
	defer e.promotionMu.Unlock()
	candidate, err := e.loadPolicyCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if candidate.Status != PolicyCandidateStatusTrialing {
		if candidate.Status == PolicyCandidateStatusProposed {
			return e.ensureExistingPolicyPromotionProposal(ctx, candidate, fitness)
		}
		return fmt.Errorf("policy candidate %s status %s is not proposal-ready", candidate.ID, candidate.Status)
	}
	if !fitness.PromotionAllowed {
		return e.rejectPolicyCandidate(ctx, candidate.ID, fitness.PromotionBlockedReason)
	}
	if len(fitness.EvidenceRefs) == 0 {
		return e.rejectPolicyCandidate(ctx, candidate.ID, "missing_validation_evidence")
	}
	return e.commitPolicyPromotionProposal(ctx, candidate, fitness)
}

func (e *ForestPolicyEngine) ActivateAcceptedCandidate(ctx context.Context, candidateID, acceptedClaimID string) (*HyperParameters, error) {
	e.promotionMu.Lock()
	defer e.promotionMu.Unlock()
	if strings.TrimSpace(acceptedClaimID) == "" {
		return nil, fmt.Errorf("accepted policy promotion claim is required")
	}
	accepted, err := e.acceptedForestClaimExists(ctx, acceptedClaimID)
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, fmt.Errorf("accepted policy promotion claim %s not found", acceptedClaimID)
	}
	candidate, err := e.loadPolicyCandidate(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	if candidate.Status != PolicyCandidateStatusProposed {
		return nil, fmt.Errorf("policy candidate %s status %s is not activation-ready", candidate.ID, candidate.Status)
	}
	fitness := policyFitnessFromCandidate(candidate)
	if !fitness.PromotionAllowed {
		return nil, e.rejectPolicyCandidate(ctx, candidate.ID, fitness.PromotionBlockedReason)
	}
	if err := e.commitPolicyActivation(ctx, candidate, fitness, acceptedClaimID); err != nil {
		return nil, err
	}
	promoted := candidate.HyperParameters.Clone()
	if e.store != nil {
		if err := e.store.Save(ctx, promoted); err != nil {
			return nil, fmt.Errorf("persist accepted policy snapshot: %w", err)
		}
	}
	return promoted, nil
}

func (e *ForestPolicyEngine) loadPolicyCandidate(ctx context.Context, candidateID string) (*PolicyCandidate, error) {
	row := e.db.QueryRowContext(ctx, `
		SELECT candidate_id, parent_policy_id, policy_version, genome, hyperparameters,
		       status, source, evidence_refs, validation_evidence_refs,
		       rejection_reason, created_at, updated_at, metadata
		FROM forest_policy_candidates
		WHERE candidate_id = ?
	`, strings.TrimSpace(candidateID))
	return scanPolicyCandidate(row)
}

func scanPolicyCandidate(row interface{ Scan(dest ...any) error }) (*PolicyCandidate, error) {
	var genomeJSON, hpJSON, evidenceRefs, validationRefs, metadata string
	var createdAt, updatedAt int64
	candidate := &PolicyCandidate{}
	if err := row.Scan(&candidate.ID, &candidate.ParentPolicyID, &candidate.Version, &genomeJSON, &hpJSON,
		&candidate.Status, &candidate.Source, &evidenceRefs, &validationRefs, &candidate.RejectionReason,
		&createdAt, &updatedAt, &metadata); err != nil {
		return nil, fmt.Errorf("scan policy candidate: %w", err)
	}
	candidate.HyperParameters = &HyperParameters{}
	if err := unmarshalJSON(genomeJSON, &candidate.Genome); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(hpJSON, candidate.HyperParameters); err != nil {
		return nil, err
	}
	candidate.EvidenceRefs = decodeStringList(evidenceRefs)
	candidate.ValidationEvidenceRefs = decodeStringList(validationRefs)
	candidate.CreatedAt = time.Unix(createdAt, 0).UTC()
	candidate.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	candidate.Metadata = map[string]any{}
	_ = unmarshalJSON(metadata, &candidate.Metadata)
	return candidate, nil
}

func policyFitnessFromCandidate(candidate *PolicyCandidate) PolicyFitness {
	var fitness PolicyFitness
	if candidate == nil || len(candidate.Metadata) == 0 {
		return fitness
	}
	raw, ok := candidate.Metadata["fitness"]
	if !ok {
		return fitness
	}
	_ = unmarshalJSON(marshalJSON(raw), &fitness)
	return fitness
}

func (e *ForestPolicyEngine) rejectPolicyCandidate(ctx context.Context, candidateID, reason string) error {
	if err := e.markPolicyCandidateRejected(ctx, candidateID, reason); err != nil {
		return err
	}
	return fmt.Errorf("policy candidate rejected: %s", reason)
}

func (e *ForestPolicyEngine) markPolicyCandidateRejected(ctx context.Context, candidateID, reason string) error {
	_, err := e.db.ExecContext(ctx, `
		UPDATE forest_policy_candidates
		SET status = ?, rejection_reason = ?, updated_at = ?
		WHERE candidate_id = ? AND status != ?
	`, PolicyCandidateStatusRejected, strings.TrimSpace(reason), time.Now().UTC().Unix(), candidateID, PolicyCandidateStatusPromoted)
	if err != nil {
		return fmt.Errorf("reject policy candidate: %w", err)
	}
	return nil
}

func (e *ForestPolicyEngine) commitPolicyPromotionProposal(ctx context.Context, candidate *PolicyCandidate, fitness PolicyFitness) error {
	if err := ensureForestGovernanceSchema(e.db); err != nil {
		return err
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy promotion proposal: %w", err)
	}
	defer tx.Rollback()
	if err := updatePolicyPromotionProposalRows(ctx, tx, candidate, fitness); err != nil {
		return err
	}
	if err := e.upsertPolicyPromotionArtifactTx(ctx, tx, candidate, fitness, "proposed", "requires_validation", ""); err != nil {
		return err
	}
	if _, err := insertGovernanceProposalTx(ctx, tx, policyPromotionGovernanceProposal(candidate, fitness, ForestProposalStatusProposed, "")); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy promotion proposal: %w", err)
	}
	proposal := policyPromotionGovernanceProposal(candidate, fitness, ForestProposalStatusProposed, "")
	return emitGovernanceProposalClaimWithProposal(ctx, e.db, e.proposalSink, proposal, policyPromotionClaimProposal(candidate, fitness))
}

func (e *ForestPolicyEngine) ensureExistingPolicyPromotionProposal(ctx context.Context, candidate *PolicyCandidate, fitness PolicyFitness) error {
	if len(fitness.EvidenceRefs) == 0 {
		fitness.EvidenceRefs = candidate.ValidationEvidenceRefs
	}
	if len(fitness.EvidenceRefs) == 0 {
		return fmt.Errorf("policy candidate %s proposed without validation evidence", candidate.ID)
	}
	if err := ensureForestGovernanceSchema(e.db); err != nil {
		return err
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin existing policy promotion proposal: %w", err)
	}
	defer tx.Rollback()
	proposal := policyPromotionGovernanceProposal(candidate, fitness, ForestProposalStatusProposed, "")
	if _, err := insertGovernanceProposalTx(ctx, tx, proposal); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return emitGovernanceProposalClaimWithProposal(ctx, e.db, e.proposalSink, proposal, policyPromotionClaimProposal(candidate, fitness))
}

func updatePolicyPromotionProposalRows(ctx context.Context, tx *sql.Tx, candidate *PolicyCandidate, fitness PolicyFitness) error {
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_policy_candidates
		SET status = ?, validation_evidence_refs = ?, updated_at = ?, metadata = ?
		WHERE candidate_id = ? AND status = ?
	`, PolicyCandidateStatusProposed, encodeStringList(fitness.EvidenceRefs), now,
		marshalJSON(map[string]any{
			"fitness":     fitness,
			"proposal_id": policyPromotionProposalID(candidate.ID, fitness.EvidenceRefs),
		}), candidate.ID, PolicyCandidateStatusTrialing); err != nil {
		return fmt.Errorf("mark policy candidate promotion proposed: %w", err)
	}
	return nil
}

func (e *ForestPolicyEngine) commitPolicyActivation(ctx context.Context, candidate *PolicyCandidate, fitness PolicyFitness, acceptedClaimID string) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accepted policy activation: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE forest_policies SET status = ?, updated_at = ? WHERE status = ?`, PolicyStatusRetired, now, PolicyStatusActive); err != nil {
		return fmt.Errorf("retire active policy: %w", err)
	}
	policyID := stableID("forest_policy", candidate.ID, encodeStringList(fitness.EvidenceRefs), acceptedClaimID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_policies
			(policy_id, policy_version, status, source, genome, hyperparameters,
			 validation_evidence_refs, rollback_policy, fitness, active_since,
			 created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, policyID, candidate.Version, PolicyStatusActive, candidate.Source, marshalJSON(candidate.Genome), marshalJSON(candidate.HyperParameters),
		encodeStringList(fitness.EvidenceRefs), PolicyRollbackUsePreviousActive, marshalJSON(fitness), now, now, now,
		marshalJSON(map[string]any{"candidate_id": candidate.ID, "accepted_claim_id": acceptedClaimID})); err != nil {
		return fmt.Errorf("insert accepted policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE forest_policy_candidates
		SET status = ?, updated_at = ?, metadata = ?
		WHERE candidate_id = ? AND status = ?
	`, PolicyCandidateStatusPromoted, now,
		marshalJSON(map[string]any{"fitness": fitness, "accepted_claim_id": acceptedClaimID}), candidate.ID, PolicyCandidateStatusProposed); err != nil {
		return fmt.Errorf("mark policy candidate activated: %w", err)
	}
	if err := e.upsertPolicyPromotionArtifactTx(ctx, tx, candidate, fitness, "accepted", "validated", acceptedClaimID); err != nil {
		return err
	}
	if err := updateGovernanceProposalStatusTx(ctx, tx, policyPromotionProposalID(candidate.ID, fitness.EvidenceRefs), ForestProposalStatusAccepted, acceptedClaimID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit accepted policy activation: %w", err)
	}
	return nil
}

func (e *ForestPolicyEngine) upsertPolicyPromotionArtifactTx(ctx context.Context, tx *sql.Tx, candidate *PolicyCandidate, fitness PolicyFitness, artifactStatus, validationStatus, acceptedClaimID string) error {
	if ok, err := e.tableExistsTx(ctx, tx, "forest_artifacts"); err != nil || !ok {
		return err
	}
	artifactID := policyPromotionArtifactID(candidate.ID, fitness.EvidenceRefs)
	now := time.Now().UTC().Unix()
	metadata := map[string]any{
		"candidate":       candidate,
		"fitness":         fitness,
		"rollback_policy": PolicyRollbackUsePreviousActive,
		"proposal_id":     policyPromotionProposalID(candidate.ID, fitness.EvidenceRefs),
	}
	if strings.TrimSpace(acceptedClaimID) != "" {
		metadata["accepted_claim_id"] = strings.TrimSpace(acceptedClaimID)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO forest_artifacts
			(artifact_id, claim_id, generator_participant, artifact_name, artifact_kind,
			 data_type, content_ref, status, validation_status, last_sequence,
			 first_seen_at, last_seen_at, payload_hash, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
			ON CONFLICT(artifact_id) DO UPDATE SET
				status = excluded.status,
				validation_status = excluded.validation_status,
				last_seen_at = excluded.last_seen_at,
				metadata = excluded.metadata
	`, artifactID, policyPromotionProposalID(candidate.ID, fitness.EvidenceRefs), "memory_forest", "Forest policy promotion", PolicyPromotionArtifactKind,
		"policy", "forest_policy:"+candidate.ID, artifactStatus, validationStatus, now, now,
		stableID("policy_payload", candidate.ID, marshalJSON(fitness)),
		marshalJSON(metadata))
	if err != nil {
		return fmt.Errorf("upsert policy promotion artifact: %w", err)
	}
	return nil
}

func (e *ForestPolicyEngine) tableExistsTx(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	return tableExistsTx(ctx, tx, table)
}

func tableExistsTx(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	return true, nil
}

func (e *ForestPolicyEngine) acceptedForestClaimExists(ctx context.Context, claimID string) (bool, error) {
	return acceptedForestClaimExists(ctx, e.db, claimID)
}

func policyPromotionProposalID(candidateID string, refs []string) string {
	return "forest_policy_promotion:" + stableID(strings.TrimSpace(candidateID), encodeStringList(refs))
}

func policyPromotionArtifactID(candidateID string, refs []string) string {
	return stableID(PolicyPromotionArtifactKind, strings.TrimSpace(candidateID), encodeStringList(refs))
}

func policyPromotionGovernanceProposal(candidate *PolicyCandidate, fitness PolicyFitness, status, acceptedClaimID string) ForestGovernanceProposal {
	now := time.Now().UTC()
	proposalID := policyPromotionProposalID(candidate.ID, fitness.EvidenceRefs)
	metadata := map[string]any{
		"candidate_id":       candidate.ID,
		"fitness":            fitness,
		"rollback_policy":    PolicyRollbackUsePreviousActive,
		"summary":            "Promote validated forest policy candidate " + candidate.ID,
		"accepted_claim_id":  strings.TrimSpace(acceptedClaimID),
		"validation_refs":    fitness.EvidenceRefs,
		"parent_policy_id":   candidate.ParentPolicyID,
		"candidate_version":  candidate.Version,
		"governance_version": forestProjectionVersionPhase131415,
	}
	return ForestGovernanceProposal{
		ProposalID:             proposalID,
		Kind:                   ForestProposalKindPolicyPromotion,
		SubjectType:            "forest_policy",
		SubjectID:              candidate.ID,
		Status:                 status,
		ClaimID:                proposalID,
		ArtifactID:             policyPromotionArtifactID(candidate.ID, fitness.EvidenceRefs),
		EvidenceRefs:           dedupeStrings(fitness.EvidenceRefs),
		RollbackPath:           PolicyRollbackUsePreviousActive,
		GuardianReviewRequired: true,
		Metadata:               metadata,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

func (e *ForestPolicyEngine) emitPolicyPromotionProposal(ctx context.Context, candidate *PolicyCandidate, fitness PolicyFitness) error {
	if e.proposalSink == nil {
		return nil
	}
	if err := e.proposalSink.ProposeClaim(ctx, policyPromotionClaimProposal(candidate, fitness)); err != nil {
		return fmt.Errorf("emit policy promotion proposal: %w", err)
	}
	return nil
}

func policyPromotionClaimProposal(candidate *PolicyCandidate, fitness PolicyFitness) ForestClaimProposal {
	return ForestClaimProposal{
		ID:                     policyPromotionProposalID(candidate.ID, fitness.EvidenceRefs),
		ClusterID:              "forest_policy",
		Dimension:              PolicyPromotionDimension,
		Summary:                "Promote validated forest policy candidate " + candidate.ID,
		EvidenceRefs:           fitness.EvidenceRefs,
		GuardianReviewRequired: true,
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func policyCandidatePopulationLimit() int {
	return len(defaultEcologyPolicy().Channels) * nodeProjectionRetryLimit()
}

func policyGenerationLimit() int {
	return nodeProjectionRetryLimit()
}

func policyRequiredSignalCount() int {
	return len([]string{"correctness", "safety", "cost", "ecology"})
}

func policyMinimumOutcomeSamples() int {
	return nodeProjectionRetryLimit() * nodeProjectionRetryLimit()
}

func policyTrialArmCount() int {
	return len([]string{PolicyTrialArmChampion, PolicyTrialArmChallenger})
}
