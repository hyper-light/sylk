package forest

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
)

type mockSkillArtifactWriter struct {
	mock.Mock
}

func (m *mockSkillArtifactWriter) WriteSkillCandidate(ctx context.Context, candidate GeneratedSkillCandidate, files []SkillCandidateFile) error {
	args := m.Called(ctx, candidate, files)
	return args.Error(0)
}

func TestPhase101112PolicyBlocksRetrievalOnlyPromotion(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)
	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	tuner, err := NewHyperParameterTuner(context.Background(), db, logger)
	if err != nil {
		t.Fatalf("new tuner: %v", err)
	}
	t.Cleanup(tuner.Stop)
	original := tuner.Get().SnapshotID

	drivePolicyProposal(t, tuner, nil)
	for i := 0; i < int(policyCandidatePopulationLimit()*minSamplesForAdaptDecision); i++ {
		tuner.ObserveAndAdapt(context.Background(), AdaptationObservation{
			CalibrationError: 0.40,
			RegretFrac:       0.20,
			SampleCount:      1,
			ObservedAt:       time.Now().UTC(),
		}, false)
		tuner.ObserveAndAdapt(context.Background(), AdaptationObservation{
			CalibrationError: 0.04,
			RegretFrac:       0.04,
			SampleCount:      1,
			ObservedAt:       time.Now().UTC(),
		}, true)
	}

	if got := tuner.Get().SnapshotID; got != original {
		t.Fatalf("retrieval-only promotion changed snapshot: got %d want %d", got, original)
	}
	assertPolicyCandidateStatus(t, db, PolicyCandidateStatusRejected)
}

func TestPhase101112PolicyPromotionCreatesArtifactAndClaim(t *testing.T) {
	sink := &mockClaimProposalSink{}
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()
	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.Dimension == PolicyPromotionDimension &&
			proposal.GuardianReviewRequired &&
			len(proposal.EvidenceRefs) >= policyMinimumOutcomeSamples()
	})).Return(nil).Once()

	tuner := forest.Tuner()
	drivePolicyProposal(t, tuner, nil)
	for i := 0; i < int(policyCandidatePopulationLimit()*minSamplesForAdaptDecision); i++ {
		tuner.ObserveAndAdapt(context.Background(), policyFitnessObservation("champion", i, 0.45, 0.70, 0.50, 0.45, 0.50, 0.30), false)
		tuner.ObserveAndAdapt(context.Background(), policyFitnessObservation("challenger", i, 0.95, 0.95, 0.90, 0.92, 0.98, 0.04), true)
	}

	assertOneActivePolicy(t, db)
	assertPolicyCandidateStatus(t, db, PolicyCandidateStatusPromoted)
	assertPolicyPromotionArtifact(t, db)
	sink.AssertExpectations(t)
}

func TestPhase101112ConcurrentPolicyPromotionHasSingleActivePolicy(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)
	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	tuner, err := NewHyperParameterTuner(context.Background(), db, logger)
	if err != nil {
		t.Fatalf("new tuner: %v", err)
	}
	t.Cleanup(tuner.Stop)

	candidate, err := tuner.policy.ProposeCandidateFromObservations(context.Background(), tuner.Get(), repeatedAdaptationObservations(policyMinimumOutcomeSamples(), 0.30, 0.20))
	if err != nil {
		t.Fatalf("propose candidate: %v", err)
	}
	if candidate == nil {
		t.Fatal("expected policy candidate")
	}
	fitness := ComputePolicyFitness(
		policyOutcomeSeries(candidate.ID, "champion", PolicyOutcomeArmChampion, policyMinimumOutcomeSamples(), 0.45),
		policyOutcomeSeries(candidate.ID, candidate.ID, PolicyOutcomeArmChallenger, policyMinimumOutcomeSamples(), 0.95),
	)
	if !fitness.PromotionAllowed {
		t.Fatalf("fitness unexpectedly blocked: %+v", fitness)
	}
	errs := make(chan error, policyTrialArmCount()*nodeProjectionRetryLimit())
	var wg sync.WaitGroup
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tuner.policy.PromoteCandidate(context.Background(), candidate.ID, fitness)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent promotions succeeded %d times, want 1", successes)
	}
	assertOneActivePolicy(t, db)
}

func TestPhase1112MemesExtractSuppressAndRecombine(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	insertRepeatedMemeNodes(t, db, "session-memes", ForestNodeValidation, EvidenceGradeValidated, "validated retry harness passes", nodeProjectionRetryLimit())
	insertRepeatedMemeNodes(t, db, "session-memes", ForestNodeValidation, EvidenceGradeValidated, "validated rollback harness passes", nodeProjectionRetryLimit())
	insertRepeatedMemeNodes(t, db, "session-memes", ForestNodeValidation, EvidenceGradeFailed, "unsafe network mutation failed", nodeProjectionRetryLimit())

	result, err := forest.RunMemeExtraction(context.Background(), "session-memes", policyCandidatePopulationLimit()*policyCandidatePopulationLimit())
	if err != nil {
		t.Fatalf("extract memes: %v", err)
	}
	if result.Activated < nodeProjectionRetryLimit() || result.Negative == 0 {
		t.Fatalf("unexpected extraction result: %+v", result)
	}
	suppressed, memeID, err := forest.ActiveNegativeMemeSuppresses(context.Background(), "unsafe network mutation failed during candidate synthesis")
	if err != nil {
		t.Fatalf("negative meme suppress: %v", err)
	}
	if !suppressed || memeID == "" {
		t.Fatalf("expected negative meme suppression, suppressed=%v meme=%q", suppressed, memeID)
	}
	generated, err := forest.GenerateMemeCandidates(context.Background(), policyCandidatePopulationLimit())
	if err != nil {
		t.Fatalf("generate memes: %v", err)
	}
	if generated.Candidates == 0 {
		t.Fatalf("expected recombined meme candidate: %+v", generated)
	}
}

func TestPhase12SkillCandidateProposalValidationPromotionIsInert(t *testing.T) {
	sink := &mockClaimProposalSink{}
	writer := &mockSkillArtifactWriter{}
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink, SkillArtifactWriter: writer})
	defer forest.Close()
	defer db.Close()
	writer.On("WriteSkillCandidate", mock.Anything, mock.MatchedBy(func(candidate GeneratedSkillCandidate) bool {
		return candidate.Status == SkillCandidateStatusProposed && candidate.ArtifactType == SkillCandidateArtifactType
	}), mock.MatchedBy(func(files []SkillCandidateFile) bool {
		return len(files) >= policyRequiredSignalCount()
	})).Return(nil).Once()
	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.Dimension == "generated_skill_candidate"
	})).Return(nil).Once()
	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.Dimension == "skill_candidate_promotion"
	})).Return(nil).Once()

	candidate, err := forest.ProposeGeneratedSkillCandidate(context.Background(), SkillCandidateInput{
		Name:                 "Evidence Remediation Author",
		RoleScope:            "guardian",
		Trigger:              "validated remediation pattern appears",
		SourceMemeIDs:        []string{"meme:validated-remediation"},
		SourceValidationRefs: []string{"validation:remediation-harness"},
		PromotionRationale:   "Produce a proposed remediation skill when repeated validated remediation patterns appear.",
	})
	if err != nil {
		t.Fatalf("propose skill: %v", err)
	}
	assertSkillCandidateStatus(t, db, candidate.CandidateID, SkillCandidateStatusProposed)
	if validations, err := forest.ValidateSkillCandidate(context.Background(), candidate.CandidateID); err != nil {
		t.Fatalf("validate skill: %v", err)
	} else if skillValidationsFailed(validations) {
		t.Fatalf("expected validations to pass: %+v", validations)
	}
	assertSkillCandidateStatus(t, db, candidate.CandidateID, SkillCandidateStatusValidated)
	if err := forest.PromoteSkillCandidate(context.Background(), candidate.CandidateID); err != nil {
		t.Fatalf("promote skill candidate: %v", err)
	}
	assertSkillCandidateStatus(t, db, candidate.CandidateID, SkillCandidateStatusAcceptedPendingActivation)
	if err := forest.ActivateSkillCandidate(context.Background(), candidate.CandidateID); err == nil {
		t.Fatal("memory forest must not activate generated skills")
	}
	writer.AssertExpectations(t)
	sink.AssertExpectations(t)
}

func TestPhase12SkillCandidateRejectsPermissionExpansionWithoutClaim(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	candidate, err := forest.ProposeGeneratedSkillCandidate(context.Background(), SkillCandidateInput{
		Name:                 "Network Skill",
		RoleScope:            "architect",
		Trigger:              "validated network workflow appears",
		SourceMemeIDs:        []string{"meme:network"},
		SourceValidationRefs: []string{"validation:network"},
		RequestedPermissions: []string{"network"},
	})
	if err == nil {
		t.Fatal("expected permission expansion rejection")
	}
	assertSkillCandidateStatus(t, db, candidate.CandidateID, SkillCandidateStatusRejected)
}

func drivePolicyProposal(t *testing.T, tuner *HyperParameterTuner, refs []string) {
	t.Helper()
	for i := 0; i < int(policyCandidatePopulationLimit()*minSamplesForAdaptDecision); i++ {
		obs := AdaptationObservation{
			CalibrationError: 0.40,
			RegretFrac:       0.20,
			SampleCount:      1,
			EvidenceRefs:     refs,
			ObservedAt:       time.Now().UTC(),
		}
		tuner.ObserveAndAdapt(context.Background(), obs, false)
	}
	if _, ok := tuner.SnapshotForTrial(0); !ok {
		t.Fatal("expected policy candidate trial")
	}
}

func policyFitnessObservation(prefix string, index int, correctness, safety, cost, ecology, validation, calErr float64) AdaptationObservation {
	return AdaptationObservation{
		CalibrationError:   calErr,
		RegretFrac:         calErr,
		SampleCount:        1,
		Correctness:        correctness,
		Safety:             safety,
		Cost:               cost,
		Ecology:            ecology,
		ValidationPassRate: validation,
		ContradictionDelta: correctness - 0.5,
		LatencyMicros:      int64(policyCandidatePopulationLimit() * nodeProjectionRetryLimit()),
		EvidenceRefs:       []string{fmt.Sprintf("validation:%s:%d", prefix, index)},
		ObservedAt:         time.Now().UTC(),
	}
}

func repeatedAdaptationObservations(count int, calErr, regret float64) []AdaptationObservation {
	observations := make([]AdaptationObservation, 0, count)
	for i := 0; i < count; i++ {
		observations = append(observations, AdaptationObservation{
			CalibrationError: calErr,
			RegretFrac:       regret,
			SampleCount:      1,
			ObservedAt:       time.Now().UTC(),
		})
	}
	return observations
}

func policyOutcomeSeries(candidateID, policyID, arm string, count int, score float64) []PolicyOutcome {
	outcomes := make([]PolicyOutcome, 0, count)
	for i := 0; i < count; i++ {
		outcomes = append(outcomes, PolicyOutcome{
			CandidateID:        candidateID,
			PolicyID:           policyID,
			Arm:                arm,
			Correctness:        score,
			Safety:             score,
			Cost:               score,
			Ecology:            score,
			ValidationPassRate: score,
			SampleCount:        1,
			EvidenceRefs:       []string{fmt.Sprintf("validation:%s:%d", arm, i)},
			ObservedAt:         time.Now().UTC(),
		})
	}
	return outcomes
}

func insertRepeatedMemeNodes(t testing.TB, db *sql.DB, sessionID string, kind ForestNodeKind, grade EvidenceGrade, summary string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		node := insertPhase789Node(t, db, phase789NodeSpec{
			ID:      stableID(summary, fmt.Sprint(i)),
			Session: sessionID,
			Kind:    kind,
			Grade:   grade,
			Seq:     int64(i + 1),
		})
		if _, err := db.Exec(`UPDATE forest_nodes SET summary = ?, title = ? WHERE node_id = ?`, summary, summary, node.ID); err != nil {
			t.Fatalf("set repeated summary: %v", err)
		}
	}
}

func assertPolicyCandidateStatus(t testing.TB, db *sql.DB, status string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_policy_candidates WHERE status = ?`, status).Scan(&count); err != nil {
		t.Fatalf("count policy candidates: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected policy candidate status %s", status)
	}
}

func assertOneActivePolicy(t testing.TB, db *sql.DB) {
	t.Helper()
	var active int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_policies WHERE status = ?`, PolicyStatusActive).Scan(&active); err != nil {
		t.Fatalf("count active policies: %v", err)
	}
	if active != 1 {
		t.Fatalf("active policy count = %d, want 1", active)
	}
}

func assertPolicyPromotionArtifact(t testing.TB, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_artifacts WHERE artifact_kind = ?`, PolicyPromotionArtifactKind).Scan(&count); err != nil {
		t.Fatalf("count policy artifacts: %v", err)
	}
	if count == 0 {
		t.Fatal("expected policy promotion artifact")
	}
}

func assertSkillCandidateStatus(t testing.TB, db *sql.DB, candidateID, status string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`SELECT status FROM forest_skill_candidates WHERE candidate_id = ?`, candidateID).Scan(&got); err != nil {
		t.Fatalf("skill candidate status query: %v", err)
	}
	if strings.TrimSpace(got) != status {
		t.Fatalf("skill candidate status = %s, want %s", got, status)
	}
}
