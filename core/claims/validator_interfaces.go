package claims

import (
	"context"
	"time"
)

//go:generate mockery --name=ValidationDispatcher --output=./mocks --outpkg=mocks
//go:generate mockery --name=ValidatorHandler --output=./mocks --outpkg=mocks
//go:generate mockery --name=AgentTurnEvaluator --output=./mocks --outpkg=mocks
//go:generate mockery --name=ArtifactLifecycleBusSink --output=./mocks --outpkg=mocks
//go:generate mockery --name=ValidationLifecycleBusSink --output=./mocks --outpkg=mocks
//go:generate mockery --name=ClaimsClock --output=./mocks --outpkg=mocks
//go:generate mockery --name=TestamentGenerator --output=./mocks --outpkg=mocks

type ValidationDispatchRequest struct {
	Claim      *Claim
	Artifact   *Artifact
	Validation *Validation
	StartedAt  time.Time
}

type ValidationDispatchResult struct {
	ValidationID     string
	Status           ValidationStatus
	ResultArtifact   *Artifact
	Error            *ValidationError
	CompletedAt      time.Time
	ShortCircuitRest bool
}

// ValidationDispatcher dispatches one typed validation without owning
// board mutation. The caller commits status transitions.
type ValidationDispatcher interface {
	DispatchValidation(ctx context.Context, req ValidationDispatchRequest) (ValidationDispatchResult, error)
}

type ValidatorHandlerRequest struct {
	Artifact   *Artifact
	Validation *Validation
	StartedAt  time.Time
}

type ValidatorHandlerResult struct {
	ResultArtifact *Artifact
	Error          *ValidationError
}

// ValidatorHandler evaluates one artifact against one validation.
type ValidatorHandler interface {
	ValidateArtifact(ctx context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error)
}

type QualityBarEvaluationRequest struct {
	Claim          *Claim
	Artifact       *Artifact
	Validation     *Validation
	ResultArtifact *Artifact
}

type QualityBarEvaluationResult struct {
	Status      ValidationStatus
	Error       *ValidationError
	Evaluator   ParticipantRef
	EvaluatedAt time.Time
}

// AgentTurnEvaluator covers the agentic quality-bar phase only.
type AgentTurnEvaluator interface {
	EvaluateArtifactQualityBar(ctx context.Context, req QualityBarEvaluationRequest) (QualityBarEvaluationResult, error)
}

type ArtifactLifecycleEvent struct {
	Artifact *Artifact
	From     ArtifactStatus
	To       ArtifactStatus
	ActorID  string
	At       time.Time
}

type ValidationLifecycleEvent struct {
	Validation *Validation
	From       ValidationStatus
	To         ValidationStatus
	ActorID    string
	At         time.Time
}

type ArtifactLifecycleBusSink interface {
	PublishArtifactLifecycle(ctx context.Context, event ArtifactLifecycleEvent) error
}

type ValidationLifecycleBusSink interface {
	PublishValidationLifecycle(ctx context.Context, event ValidationLifecycleEvent) error
}

type ClaimsClock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

type ArtifactReadStore interface {
	CloneArtifact(id string) (*Artifact, bool)
}

type ClaimReadStore interface {
	CloneClaim(id string) (*Claim, bool)
	CloneValidation(id string) (*Validation, *Claim, bool)
}

type ValidationEvaluator interface {
	EvaluateValidation(ctx context.Context, claimID, validationID string, change StatusChange) error
}

type TestamentGenerator interface {
	GenerateTestamentAction(ctx context.Context, action Action, testaments []Testament, opts GenerateTestamentActionOptions) (*GeneratedTestamentAction, error)
}

var (
	_ ClaimsClock         = SystemClock{}
	_ ArtifactReadStore   = (*ClaimsBoard)(nil)
	_ ClaimReadStore      = (*ClaimsBoard)(nil)
	_ ValidationEvaluator = (*ClaimsBoard)(nil)
	_ TestamentGenerator  = (*ClaimsBoard)(nil)
	_ ValidationEvaluator = (*DurableBoard)(nil)
	_ TestamentGenerator  = (*DurableBoard)(nil)
)
