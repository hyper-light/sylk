package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrClaimOrchestratorInvalid = errors.New("claim orchestrator invalid")

type ClaimOrchestratorConfig struct {
	Board                *ClaimsBoard
	ArtifactOrchestrator *ArtifactOrchestrator
	ResultBuilder        *ResultTestamentBuilder
	Scope                ScopeProvider
	ActorID              string
	ConcurrencyBudget    int
	Timeout              time.Duration
}

type ClaimOrchestrator struct {
	board                *ClaimsBoard
	artifactOrchestrator *ArtifactOrchestrator
	resultBuilder        *ResultTestamentBuilder
	scope                ScopeProvider
	actorID              string
	concurrencyBudget    int
	timeout              time.Duration
}

type ClaimValidationOutcome struct {
	ClaimID                string
	TestamentID            string
	Status                 TestamentLifecycleStatus
	ArtifactOutcomes       []ArtifactValidationOutcome
	ResultArtifactsByClaim map[string][]*Artifact
	ResultTestament        *ResultTestamentResult
	MissingArtifacts       []string
}

func NewClaimOrchestrator(cfg ClaimOrchestratorConfig) (*ClaimOrchestrator, error) {
	if cfg.Board == nil || cfg.ArtifactOrchestrator == nil {
		return nil, fmt.Errorf("%w: board and artifact orchestrator are required", ErrClaimOrchestratorInvalid)
	}
	scope := cfg.Scope
	if scope == nil {
		scope = cfg.Board.scope
	}
	return &ClaimOrchestrator{
		board:                cfg.Board,
		artifactOrchestrator: cfg.ArtifactOrchestrator,
		resultBuilder:        cfg.ResultBuilder,
		scope:                scope,
		actorID:              strings.TrimSpace(cfg.ActorID),
		concurrencyBudget:    cfg.ConcurrencyBudget,
		timeout:              cfg.Timeout,
	}, nil
}

func (o *ClaimOrchestrator) ValidateTestament(ctx context.Context, testamentID string) (ClaimValidationOutcome, error) {
	testament, claim, err := o.loadClaimValidationInputs(testamentID)
	if err != nil {
		return ClaimValidationOutcome{}, err
	}
	outcome := ClaimValidationOutcome{ClaimID: claim.ID, TestamentID: testament.ID, ResultArtifactsByClaim: map[string][]*Artifact{claim.ID: nil}}
	outcome.MissingArtifacts = missingValidationArtifacts(claim, testament)
	if err := o.beginTestamentValidation(ctx, testament); err != nil {
		return outcome, err
	}
	if len(outcome.MissingArtifacts) == 0 {
		outcome.ArtifactOutcomes = o.validateTestamentArtifacts(ctxOrBackground(ctx), claim, testament)
	}
	outcome.ResultArtifactsByClaim[claim.ID] = resultArtifactsFromArtifactOutcomes(outcome.ArtifactOutcomes)
	outcome.Status = testamentStatusForClaimOutcome(outcome)
	if err := o.completeTestamentValidation(ctxOrBackground(ctx), outcome); err != nil {
		return outcome, err
	}
	return o.buildResultTestament(ctxOrBackground(ctx), outcome)
}

func (o *ClaimOrchestrator) loadClaimValidationInputs(testamentID string) (*Testament, *Claim, error) {
	if o == nil || o.board == nil || o.artifactOrchestrator == nil {
		return nil, nil, fmt.Errorf("%w: orchestrator is not initialized", ErrClaimOrchestratorInvalid)
	}
	testament, ok := o.board.CloneTestament(strings.TrimSpace(testamentID))
	if !ok || testament == nil {
		return nil, nil, fmt.Errorf("%w: testament %q not found", ErrClaimOrchestratorInvalid, testamentID)
	}
	claim, ok := o.board.CloneClaim(ClaimIDFromRelations(testament.Relations))
	if !ok || claim == nil {
		return nil, nil, fmt.Errorf("%w: parent claim not found for testament %q", ErrClaimOrchestratorInvalid, testamentID)
	}
	return testament, claim, nil
}

func missingValidationArtifacts(claim *Claim, testament *Testament) []string {
	names := testamentArtifactNames(testament)
	missing := make([]string, 0)
	for _, validation := range claim.Validations {
		if validation == nil || strings.TrimSpace(validation.TargetArtifactName) == "" {
			continue
		}
		if _, ok := names[validation.TargetArtifactName]; !ok {
			missing = append(missing, validation.TargetArtifactName)
		}
	}
	return missing
}

func testamentArtifactNames(testament *Testament) map[string]struct{} {
	names := make(map[string]struct{}, len(testament.Artifacts))
	for _, artifact := range testament.Artifacts {
		if artifact != nil && strings.TrimSpace(artifact.ArtifactName) != "" {
			names[artifact.ArtifactName] = struct{}{}
		}
	}
	return names
}

func (o *ClaimOrchestrator) beginTestamentValidation(ctx context.Context, testament *Testament) error {
	if testament.LifecycleStatus == TestamentLifecycleValidating {
		return nil
	}
	return o.board.BeginTestamentValidation(ctx, testament.ID, firstNonEmpty(o.actorID, testament.AgentID))
}

func (o *ClaimOrchestrator) validateTestamentArtifacts(ctx context.Context, claim *Claim, testament *Testament) []ArtifactValidationOutcome {
	if o.scope == nil {
		return o.validateTestamentArtifactsSerial(ctx, claim, testament)
	}
	return o.validateTestamentArtifactsTracked(ctx, claim, testament)
}

func (o *ClaimOrchestrator) validateTestamentArtifactsSerial(ctx context.Context, claim *Claim, testament *Testament) []ArtifactValidationOutcome {
	out := make([]ArtifactValidationOutcome, 0, len(testament.Artifacts))
	for _, artifact := range testament.Artifacts {
		if artifact != nil {
			out = append(out, o.validateOneArtifact(ctx, claim.ID, testament.ID, artifact.ID))
		}
	}
	return out
}

func (o *ClaimOrchestrator) validateTestamentArtifactsTracked(ctx context.Context, claim *Claim, testament *Testament) []ArtifactValidationOutcome {
	artifacts := nonNilArtifacts(testament.Artifacts)
	workers := boundedWorkerCount(o.concurrencyBudget, len(artifacts))
	tasks := make(chan *Artifact, len(artifacts))
	results := make(chan claimArtifactWorkerResult, len(artifacts)+workers)
	for _, artifact := range artifacts {
		tasks <- artifact
	}
	close(tasks)
	o.startClaimArtifactWorkers(ctx, workers, claim.ID, testament.ID, tasks, results)
	return collectClaimArtifactWorkerResults(results, workers)
}

type claimArtifactWorkerResult struct {
	Outcome ArtifactValidationOutcome
	Done    bool
}

func (o *ClaimOrchestrator) startClaimArtifactWorkers(ctx context.Context, workers int, claimID, testamentID string, tasks <-chan *Artifact, results chan<- claimArtifactWorkerResult) {
	for idx := 0; idx < workers; idx++ {
		if err := o.scope.Go("claims_testament_artifact_validation", o.timeout, func(workerCtx context.Context) error {
			defer func() { results <- claimArtifactWorkerResult{Done: true} }()
			for artifact := range tasks {
				results <- claimArtifactWorkerResult{Outcome: o.validateOneArtifact(workerCtx, claimID, testamentID, artifact.ID)}
			}
			return nil
		}); err != nil {
			results <- claimArtifactWorkerResult{Outcome: artifactValidationLaunchOutcome(claimID, testamentID, err)}
			results <- claimArtifactWorkerResult{Done: true}
		}
	}
}

func collectClaimArtifactWorkerResults(results <-chan claimArtifactWorkerResult, workers int) []ArtifactValidationOutcome {
	out := make([]ArtifactValidationOutcome, 0)
	done := 0
	for done < workers {
		item := <-results
		if item.Done {
			done++
			continue
		}
		out = append(out, item.Outcome)
	}
	return out
}

func (o *ClaimOrchestrator) validateOneArtifact(ctx context.Context, claimID, testamentID, artifactID string) ArtifactValidationOutcome {
	outcome, err := o.artifactOrchestrator.ValidateArtifact(ctx, claimID, testamentID, artifactID)
	if err == nil {
		return outcome
	}
	return artifactValidationLaunchOutcome(claimID, testamentID, err)
}

func artifactValidationLaunchOutcome(claimID, testamentID string, err error) ArtifactValidationOutcome {
	return ArtifactValidationOutcome{
		ClaimID:       claimID,
		TestamentID:   testamentID,
		Status:        ArtifactStatusValidationFailed,
		BlockingError: &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: err.Error(), Source: DegradedAgentRef("claims.claim_orchestrator", "artifact validation launch failed"), OccurredAt: time.Now().UTC()},
	}
}

func nonNilArtifacts(artifacts []*Artifact) []*Artifact {
	out := make([]*Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact != nil {
			out = append(out, artifact)
		}
	}
	return out
}

func resultArtifactsFromArtifactOutcomes(outcomes []ArtifactValidationOutcome) []*Artifact {
	out := make([]*Artifact, 0)
	for _, outcome := range outcomes {
		for _, artifact := range outcome.ResultArtifacts {
			out = append(out, CloneArtifact(artifact))
		}
	}
	return out
}

func testamentStatusForClaimOutcome(outcome ClaimValidationOutcome) TestamentLifecycleStatus {
	if len(outcome.MissingArtifacts) != 0 {
		return TestamentLifecycleValidationIncomplete
	}
	for _, artifact := range outcome.ArtifactOutcomes {
		if artifact.Status == ArtifactStatusValidationFailed {
			if artifactFailureIsInfrastructureError(artifact.BlockingError) {
				return TestamentLifecycleValidationErrored
			}
			return TestamentLifecycleValidationFailed
		}
	}
	return TestamentLifecycleValidated
}

func artifactFailureIsInfrastructureError(err *ArtifactError) bool {
	if err == nil {
		return false
	}
	return err.Category == ArtifactErrorCategoryInternal ||
		err.Category == ArtifactErrorCategoryTimeout ||
		err.Category == ArtifactErrorCategoryPanic ||
		err.Category == ArtifactErrorCategoryInterruption
}

func (o *ClaimOrchestrator) completeTestamentValidation(ctx context.Context, outcome ClaimValidationOutcome) error {
	return o.board.CompleteTestamentValidation(ctx, outcome.TestamentID, o.actorID, outcome.Status, "claim validation orchestrator completed")
}

func (o *ClaimOrchestrator) buildResultTestament(ctx context.Context, outcome ClaimValidationOutcome) (ClaimValidationOutcome, error) {
	if o.resultBuilder == nil || len(outcome.ResultArtifactsByClaim[outcome.ClaimID]) == 0 {
		return outcome, nil
	}
	result, err := o.resultBuilder.Build(ctx, ResultTestamentRequest{
		ClaimID:           outcome.ClaimID,
		SourceTestamentID: outcome.TestamentID,
		Artifacts:         outcome.ResultArtifactsByClaim[outcome.ClaimID],
		ActorID:           o.actorID,
		Reason:            "claim validation result testament posted",
	})
	if err != nil {
		return outcome, err
	}
	outcome.ResultTestament = &result
	return outcome, nil
}
