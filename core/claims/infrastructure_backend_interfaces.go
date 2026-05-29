package claims

import "context"

//go:generate mockery --name=DAGProcessorBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=VFSProvisionerBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=ToolRuntimeBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=KnowledgeBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=MemoryContinuityBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=DocumentBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=GuardianBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=ProviderGatewayBackend --output=./mocks --outpkg=mocks
//go:generate mockery --name=ExternalAdapterBackend --output=./mocks --outpkg=mocks

type InfrastructureBackendRequest[T any] struct {
	Board       *ClaimsBoard
	Claim       *Claim
	Delta       CanonicalDelta
	Participant ParticipantRegistration
	Call        ExpectedToolCall
	Requested   T
}

type DAGOperationRequest = InfrastructureBackendRequest[DAGOperationArtifactData]
type VFSOperationRequest = InfrastructureBackendRequest[VFSOperationArtifactData]
type ToolRuntimeExecutionRequest = InfrastructureBackendRequest[ToolRuntimeExecutionArtifactData]
type KnowledgeOperationRequest = InfrastructureBackendRequest[KnowledgeOperationArtifactData]
type MemoryContinuityRequest = InfrastructureBackendRequest[MemoryContinuityArtifactData]
type DocumentOperationRequest = InfrastructureBackendRequest[DocumentOperationArtifactData]
type GuardianDecisionRequest = InfrastructureBackendRequest[GuardianDecisionArtifactData]
type ProviderGatewayCallRequest = InfrastructureBackendRequest[ProviderGatewayCallArtifactData]
type ExternalAdapterEventRequest = InfrastructureBackendRequest[ExternalAdapterEventArtifactData]

type DAGProcessorBackend interface {
	HandleDAGOperation(context.Context, DAGOperationRequest) (DAGOperationArtifactData, error)
}

type VFSProvisionerBackend interface {
	HandleVFSOperation(context.Context, VFSOperationRequest) (VFSOperationArtifactData, error)
}

type ToolRuntimeBackend interface {
	HandleToolRuntimeExecution(context.Context, ToolRuntimeExecutionRequest) (ToolRuntimeExecutionArtifactData, error)
}

type KnowledgeBackend interface {
	HandleKnowledgeOperation(context.Context, KnowledgeOperationRequest) (KnowledgeOperationArtifactData, error)
}

type MemoryContinuityBackend interface {
	HandleMemoryContinuity(context.Context, MemoryContinuityRequest) (MemoryContinuityArtifactData, error)
}

type DocumentBackend interface {
	HandleDocumentOperation(context.Context, DocumentOperationRequest) (DocumentOperationArtifactData, error)
}

type GuardianBackend interface {
	HandleGuardianDecision(context.Context, GuardianDecisionRequest) (GuardianDecisionArtifactData, error)
}

type ProviderGatewayBackend interface {
	HandleProviderGatewayCall(context.Context, ProviderGatewayCallRequest) (ProviderGatewayCallArtifactData, error)
}

type ExternalAdapterBackend interface {
	HandleExternalAdapterEvent(context.Context, ExternalAdapterEventRequest) (ExternalAdapterEventArtifactData, error)
}
