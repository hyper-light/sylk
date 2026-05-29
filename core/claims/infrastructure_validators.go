package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type DAGAcyclicityValidator struct{}
type DAGRequiredNodeCoverageValidator struct{}
type DAGScopeSubsetValidator struct{ Allowed map[string]string }
type DAGPodCountValidator struct{ MinPods, MaxPods int }
type DAGHealthValidator struct{}

type VFSAttachmentValidator struct{}
type VFSCapacityValidator struct{}
type VFSBaseVersionValidator struct{ ExpectedBaseVersion string }
type VFSScopeSubsetValidator struct{ Allowed map[string]string }
type VFSReadWriteBoundaryValidator struct{}
type VFSMergeAcyclicityValidator struct{}
type VFSConflictAbsenceValidator struct{}
type VFSMergedVersionReachableValidator struct{}

type ToolPolicyAllowValidator struct{}
type ToolExecutionModeValidator struct{ ExpectedMode string }
type ToolScopeBoundaryValidator struct{ Allowed map[string]string }
type ToolDurationValidator struct{ MaxDuration time.Duration }
type ToolExpectedOutputValidator struct{ Contains string }
type ToolRequiredArtifactPresenceValidator struct{}

type KnowledgeEmbeddingDimensionValidator struct{}
type KnowledgeNodeRetrievableValidator struct{}
type KnowledgeEdgeConsistencyValidator struct{}
type KnowledgeQueryShapeValidator struct{}
type KnowledgeScoreRangeValidator struct{}
type KnowledgeReadinessValidator struct{}

type CarryForwardSourceAvailabilityValidator struct{}
type CarryForwardCursorMonotonicityValidator struct{}
type CarryForwardDigestConsistencyValidator struct{}
type CarryForwardContradictionStateValidator struct{}

type DocumentIndexedValidator struct{}
type DocumentAttachmentListValidator struct{}
type DocumentChunkBoundariesValidator struct{}
type DocumentResultShapeValidator struct{}
type DocumentFullTextBackendReadinessValidator struct{}

type GuardianPolicyMatchValidator struct{ ExpectedRule string }
type GuardianUserApprovalPresentValidator struct{}
type GuardianBranchProtectionValidator struct{}
type GuardianDiffFindingsAbsentValidator struct{}
type GuardianRollbackReceiptValidator struct{}

type ProviderResponsePresentValidator struct{}
type ProviderUsageInBudgetValidator struct{}
type ProviderRateLimitNotExceededValidator struct{ MinimumRemaining int }
type ProviderModelAllowedValidator struct{ AllowedModels []string }
type ProviderIdentityPresentValidator struct{}

type ExternalSourceAuthenticityValidator struct{}
type ExternalSchemaValidator struct{ ExpectedSchema string }
type ExternalFreshnessValidator struct{}
type ExternalReferencedClaimValidator struct{ Board *ClaimsBoard }

func RegisterDefaultInfrastructureValidators(registry *ValidatorRegistry) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrValidatorRegistrationInvalid)
	}
	var out error
	for _, reg := range defaultInfrastructureValidatorRegistrations() {
		_, err := registry.Register(reg)
		out = errors.Join(out, err)
	}
	return out
}

func (DAGAcyclicityValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DAGOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.CycleFree && len(data.CyclePath) == 0 {
		return infrastructureValidatorPass("dag acyclic")
	}
	return infrastructureValidatorFailure(req, "DAG contains a cycle", map[string]any{"cycle_path": data.CyclePath})
}

func (DAGRequiredNodeCoverageValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DAGOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(data.MissingRequiredNodes) == 0 {
		return infrastructureValidatorPass("dag required nodes covered")
	}
	return infrastructureValidatorFailure(req, "DAG missing required nodes", map[string]any{"missing_nodes": data.MissingRequiredNodes})
}

func (v DAGScopeSubsetValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DAGOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if stringMapSubset(data.ScopeBoundary, v.Allowed) {
		return infrastructureValidatorPass("dag scope subset")
	}
	return infrastructureValidatorFailure(req, "DAG scope exceeds allowed boundary", map[string]any{"scope": data.ScopeBoundary, "allowed": v.Allowed})
}

func (v DAGPodCountValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DAGOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if boundedInt(data.PlannedPods, v.MinPods, v.MaxPods) {
		return infrastructureValidatorPass("dag pod count in range")
	}
	return infrastructureValidatorFailure(req, "DAG pod count outside range", map[string]any{"planned_pods": data.PlannedPods, "min": v.MinPods, "max": v.MaxPods})
}

func (DAGHealthValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DAGOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Status == InfrastructureStatusOK && data.FailureReason == "" && data.HealthSummary != "" {
		return infrastructureValidatorPass("dag healthy")
	}
	return infrastructureValidatorFailure(req, "DAG health failed", map[string]any{"status": data.Status, "reason": data.FailureReason})
}

func (VFSAttachmentValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Operation == VFSProvisionerToolDetach || data.Attached {
		return infrastructureValidatorPass("vfs attachment state valid")
	}
	return infrastructureValidatorFailure(req, "VFS mount is not attached", map[string]any{"mount_ref": data.MountRef})
}

func (VFSCapacityValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.CapacityBudgetBytes <= 0 || data.CapacityUsedBytes <= data.CapacityBudgetBytes {
		return infrastructureValidatorPass("vfs capacity within budget")
	}
	return infrastructureValidatorFailure(req, "VFS capacity exceeded", map[string]any{"used": data.CapacityUsedBytes, "budget": data.CapacityBudgetBytes})
}

func (v VFSBaseVersionValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(v.ExpectedBaseVersion) == "" || data.BaseVersion == strings.TrimSpace(v.ExpectedBaseVersion) {
		return infrastructureValidatorPass("vfs base version valid")
	}
	return infrastructureValidatorFailure(req, "VFS base version mismatch", map[string]any{"got": data.BaseVersion, "want": v.ExpectedBaseVersion})
}

func (v VFSScopeSubsetValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if stringMapSubset(data.ScopeBoundary, v.Allowed) {
		return infrastructureValidatorPass("vfs scope subset")
	}
	return infrastructureValidatorFailure(req, "VFS scope exceeds allowed boundary", map[string]any{"scope": data.ScopeBoundary, "allowed": v.Allowed})
}

func (VFSReadWriteBoundaryValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if !data.ReadOnly || vfsReadOnlyOperation(data.Operation) {
		return infrastructureValidatorPass("vfs read/write boundary valid")
	}
	return infrastructureValidatorFailure(req, "VFS read-only boundary violated", map[string]any{"operation": data.Operation})
}

func (VFSMergeAcyclicityValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if noDuplicateStrings(data.COWLayerChain) {
		return infrastructureValidatorPass("vfs merge layer chain acyclic")
	}
	return infrastructureValidatorFailure(req, "VFS CoW layer chain contains a cycle", map[string]any{"cow_layer_chain": data.COWLayerChain})
}

func (VFSConflictAbsenceValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(data.ConflictSet) == 0 {
		return infrastructureValidatorPass("vfs conflicts absent")
	}
	return infrastructureValidatorFailure(req, "VFS merge conflicts present", map[string]any{"conflicts": data.ConflictSet})
}

func (VFSMergedVersionReachableValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[VFSOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Operation != VFSProvisionerToolMerge || data.ResultingVersion != "" {
		return infrastructureValidatorPass("vfs merged version reachable")
	}
	return infrastructureValidatorFailure(req, "VFS merge has no reachable resulting version", map[string]any{"mount_ref": data.MountRef})
}

func (ToolPolicyAllowValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ToolRuntimeExecutionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.PolicyDecision == "allowed" && data.FailureReason == "" {
		return infrastructureValidatorPass("tool policy allowed")
	}
	return infrastructureValidatorFailure(req, "tool policy denied", map[string]any{"policy_decision": data.PolicyDecision, "reason": data.FailureReason})
}

func (v ToolExecutionModeValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ToolRuntimeExecutionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(v.ExpectedMode) == "" || data.ExecutionMode == strings.TrimSpace(v.ExpectedMode) {
		return infrastructureValidatorPass("tool execution mode valid")
	}
	return infrastructureValidatorFailure(req, "tool execution mode mismatch", map[string]any{"got": data.ExecutionMode, "want": v.ExpectedMode})
}

func (v ToolScopeBoundaryValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ToolRuntimeExecutionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if stringMapSubset(data.SandboxScope, v.Allowed) {
		return infrastructureValidatorPass("tool scope subset")
	}
	return infrastructureValidatorFailure(req, "tool sandbox scope exceeds allowed boundary", map[string]any{"scope": data.SandboxScope, "allowed": v.Allowed})
}

func (v ToolDurationValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ToolRuntimeExecutionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Duration >= 0 && (v.MaxDuration <= 0 || data.Duration <= v.MaxDuration) {
		return infrastructureValidatorPass("tool duration within budget")
	}
	return infrastructureValidatorFailure(req, "tool duration exceeded budget", map[string]any{"duration": data.Duration.String(), "max": v.MaxDuration.String()})
}

func (v ToolExpectedOutputValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ToolRuntimeExecutionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(v.Contains) == "" || strings.Contains(data.OutputSummary, v.Contains) {
		return infrastructureValidatorPass("tool expected output present")
	}
	return infrastructureValidatorFailure(req, "tool expected output missing", map[string]any{"contains": v.Contains})
}

func (ToolRequiredArtifactPresenceValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ToolRuntimeExecutionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(data.ProducedArtifactRefs) != 0 || len(data.OutputRefs) != 0 {
		return infrastructureValidatorPass("tool artifacts present")
	}
	return infrastructureValidatorFailure(req, "tool produced no artifact references", map[string]any{"tool": data.ToolName})
}

func (KnowledgeEmbeddingDimensionValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[KnowledgeOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ExpectedEmbeddingDimension <= 0 || data.EmbeddingDimension == data.ExpectedEmbeddingDimension {
		return infrastructureValidatorPass("knowledge embedding dimension valid")
	}
	return infrastructureValidatorFailure(req, "knowledge embedding dimension mismatch", map[string]any{"got": data.EmbeddingDimension, "want": data.ExpectedEmbeddingDimension})
}

func (KnowledgeNodeRetrievableValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[KnowledgeOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(data.MissingNodes) == 0 {
		return infrastructureValidatorPass("knowledge nodes retrievable")
	}
	return infrastructureValidatorFailure(req, "knowledge nodes missing", map[string]any{"missing_nodes": data.MissingNodes})
}

func (KnowledgeEdgeConsistencyValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[KnowledgeOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(data.DanglingEdges) == 0 {
		return infrastructureValidatorPass("knowledge edges consistent")
	}
	return infrastructureValidatorFailure(req, "knowledge dangling edges", map[string]any{"dangling_edges": data.DanglingEdges})
}

func (KnowledgeQueryShapeValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[KnowledgeOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(data.QueryShape) != "" && data.ResultCount >= 0 {
		return infrastructureValidatorPass("knowledge query shape valid")
	}
	return infrastructureValidatorFailure(req, "knowledge query shape invalid", map[string]any{"query_shape": data.QueryShape, "result_count": data.ResultCount})
}

func (KnowledgeScoreRangeValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[KnowledgeOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ScoreMin <= data.ScoreMax {
		return infrastructureValidatorPass("knowledge score range valid")
	}
	return infrastructureValidatorFailure(req, "knowledge score range invalid", map[string]any{"min": data.ScoreMin, "max": data.ScoreMax})
}

func (KnowledgeReadinessValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[KnowledgeOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Ready && data.FullTextBackend == "bleve" && data.FailureReason == "" {
		return infrastructureValidatorPass("knowledge backend ready")
	}
	return infrastructureValidatorFailure(req, "knowledge backend not ready", map[string]any{"ready": data.Ready, "backend": data.FullTextBackend, "reason": data.FailureReason})
}

func (CarryForwardSourceAvailabilityValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[MemoryContinuityArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.SourceAvailable || data.LegacyNoWAL {
		return infrastructureValidatorPass("carry-forward source available")
	}
	return infrastructureValidatorFailure(req, "carry-forward source unavailable", map[string]any{"topic": data.Topic})
}

func (CarryForwardCursorMonotonicityValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[MemoryContinuityArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ThroughSeq >= data.FromSeq {
		return infrastructureValidatorPass("carry-forward cursor monotonic")
	}
	return infrastructureValidatorFailure(req, "carry-forward cursor regressed", map[string]any{"from": data.FromSeq, "through": data.ThroughSeq})
}

func (CarryForwardDigestConsistencyValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[MemoryContinuityArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(data.EvidenceDigest) != "" || data.LegacyNoWAL {
		return infrastructureValidatorPass("carry-forward digest present")
	}
	return infrastructureValidatorFailure(req, "carry-forward evidence digest missing", map[string]any{"topic": data.Topic})
}

func (CarryForwardContradictionStateValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[MemoryContinuityArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if !data.Contradicted {
		return infrastructureValidatorPass("carry-forward contradiction absent")
	}
	return infrastructureValidatorFailure(req, "carry-forward contradiction present", map[string]any{"topic": data.Topic})
}

func (DocumentIndexedValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DocumentOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Deleted || data.Indexed {
		return infrastructureValidatorPass("document indexed state valid")
	}
	return infrastructureValidatorFailure(req, "document not indexed", map[string]any{"document_id": data.DocumentID})
}

func (DocumentAttachmentListValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DocumentOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if noDuplicateStrings(data.AttachmentRefs) {
		return infrastructureValidatorPass("document attachment list valid")
	}
	return infrastructureValidatorFailure(req, "document attachment list contains duplicates", map[string]any{"attachments": data.AttachmentRefs})
}

func (DocumentChunkBoundariesValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DocumentOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if validChunkBoundaries(data.ChunkBoundaries) {
		return infrastructureValidatorPass("document chunk boundaries valid")
	}
	return infrastructureValidatorFailure(req, "document chunk boundaries invalid", map[string]any{"document_id": data.DocumentID})
}

func (DocumentResultShapeValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DocumentOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(data.ResultShape) != "" && data.ResultCount >= 0 {
		return infrastructureValidatorPass("document result shape valid")
	}
	return infrastructureValidatorFailure(req, "document result shape invalid", map[string]any{"shape": data.ResultShape, "result_count": data.ResultCount})
}

func (DocumentFullTextBackendReadinessValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[DocumentOperationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.FullTextBackend == "bleve" && data.FailureReason == "" {
		return infrastructureValidatorPass("document full-text backend ready")
	}
	return infrastructureValidatorFailure(req, "document full-text backend not ready", map[string]any{"backend": data.FullTextBackend, "reason": data.FailureReason})
}

func (v GuardianPolicyMatchValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[GuardianDecisionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(v.ExpectedRule) == "" || data.PolicyRule == strings.TrimSpace(v.ExpectedRule) {
		return infrastructureValidatorPass("guardian policy matched")
	}
	return infrastructureValidatorFailure(req, "guardian policy mismatch", map[string]any{"got": data.PolicyRule, "want": v.ExpectedRule})
}

func (GuardianUserApprovalPresentValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[GuardianDecisionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ApprovalPresent && data.UserDecision == "allow" {
		return infrastructureValidatorPass("guardian approval present")
	}
	return infrastructureValidatorFailure(req, "guardian approval missing or denied", map[string]any{"decision": data.UserDecision})
}

func (GuardianBranchProtectionValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[GuardianDecisionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.BranchProtected {
		return infrastructureValidatorPass("guardian branch protected")
	}
	return infrastructureValidatorFailure(req, "guardian branch protection failed", map[string]any{"branch_state": data.BranchState})
}

func (GuardianDiffFindingsAbsentValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[GuardianDecisionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(data.DiffFindings) == 0 {
		return infrastructureValidatorPass("guardian diff clean")
	}
	return infrastructureValidatorFailure(req, "guardian diff findings present", map[string]any{"findings": data.DiffFindings})
}

func (GuardianRollbackReceiptValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[GuardianDecisionArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Operation != GuardianToolRollbackAuthorization || data.RollbackReceipt != "" {
		return infrastructureValidatorPass("guardian rollback receipt valid")
	}
	return infrastructureValidatorFailure(req, "guardian rollback receipt missing", map[string]any{"operation": data.Operation})
}

func (ProviderResponsePresentValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ProviderGatewayCallArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ResponseSummary != "" || data.Operation == ProviderGatewayToolCountTokens {
		return infrastructureValidatorPass("provider response present")
	}
	return infrastructureValidatorFailure(req, "provider response missing", map[string]any{"request_id": data.ProviderRequestID})
}

func (ProviderUsageInBudgetValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ProviderGatewayCallArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.BudgetTokens <= 0 || data.TotalTokens <= data.BudgetTokens {
		return infrastructureValidatorPass("provider usage within budget")
	}
	return infrastructureValidatorFailure(req, "provider usage exceeds budget", map[string]any{"usage": data.TotalTokens, "budget": data.BudgetTokens})
}

func (v ProviderRateLimitNotExceededValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ProviderGatewayCallArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if !data.RateLimited && data.RateLimitRemaining >= v.MinimumRemaining {
		return infrastructureValidatorPass("provider rate limit available")
	}
	return infrastructureValidatorFailure(req, "provider rate limit exceeded", map[string]any{"remaining": data.RateLimitRemaining, "minimum": v.MinimumRemaining})
}

func (v ProviderModelAllowedValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ProviderGatewayCallArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if len(v.AllowedModels) == 0 || stringInList(data.Model, v.AllowedModels) {
		return infrastructureValidatorPass("provider model allowed")
	}
	return infrastructureValidatorFailure(req, "provider model not allowed", map[string]any{"model": data.Model, "allowed": v.AllowedModels})
}

func (ProviderIdentityPresentValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ProviderGatewayCallArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(data.Identity) != "" {
		return infrastructureValidatorPass("provider identity present")
	}
	return infrastructureValidatorFailure(req, "provider identity missing", map[string]any{"model": data.Model})
}

func (ExternalSourceAuthenticityValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ExternalAdapterEventArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Source.Category == string(ParticipantCategoryExternal) && data.Signature != "invalid" {
		return infrastructureValidatorPass("external source authentic")
	}
	return infrastructureValidatorFailure(req, "external source authenticity failed", map[string]any{"source": data.Source, "signature": data.Signature})
}

func (v ExternalSchemaValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ExternalAdapterEventArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if strings.TrimSpace(v.ExpectedSchema) == "" || data.Schema == strings.TrimSpace(v.ExpectedSchema) {
		return infrastructureValidatorPass("external schema valid")
	}
	return infrastructureValidatorFailure(req, "external schema mismatch", map[string]any{"got": data.Schema, "want": v.ExpectedSchema})
}

func (ExternalFreshnessValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ExternalAdapterEventArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Fresh {
		return infrastructureValidatorPass("external event fresh")
	}
	return infrastructureValidatorFailure(req, "external event stale", map[string]any{"event_timestamp": data.EventTimestamp})
}

func (v ExternalReferencedClaimValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ExternalAdapterEventArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ReferencedClaimID == "" || referencedClaimExists(v.Board, data.ReferencedClaimID) {
		return infrastructureValidatorPass("external referenced claim valid")
	}
	return infrastructureValidatorFailure(req, "external referenced claim missing", map[string]any{"claim_id": data.ReferencedClaimID})
}

func infrastructureValidatorPass(reference string) (ValidatorHandlerResult, error) {
	artifact := &Artifact{ArtifactName: "infrastructure_validation", Kind: ArtifactKindReadiness, Reference: reference}
	if err := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: "infrastructure_validation", Reference: reference}); err != nil {
		return ValidatorHandlerResult{}, err
	}
	return ValidatorHandlerResult{ResultArtifact: artifact}, nil
}

func infrastructureValidatorFailure(req ValidatorHandlerRequest, description string, payload map[string]any) (ValidatorHandlerResult, error) {
	err := &ValidationError{Category: ValidationErrorCategoryQualityBar, Description: description, Payload: payload, Source: validationErrorSource(ValidationDispatchRequest{Validation: req.Validation, Artifact: req.Artifact})}
	artifact := &Artifact{ArtifactName: "infrastructure_validation_failure", Kind: ArtifactKindErrorDiagnostic, Reference: description}
	if setErr := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: "infrastructure_validation_failure", Reference: description, Metadata: payload}); setErr != nil {
		return ValidatorHandlerResult{}, setErr
	}
	return ValidatorHandlerResult{ResultArtifact: artifact, Error: err}, nil
}

func stringMapSubset(subset, superset map[string]string) bool {
	if len(subset) == 0 || len(superset) == 0 {
		return true
	}
	for key, value := range subset {
		if superset[strings.TrimSpace(key)] != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}

func boundedInt(value, min, max int) bool {
	if min > 0 && value < min {
		return false
	}
	return max <= 0 || value <= max
}

func vfsReadOnlyOperation(operation string) bool {
	switch operation {
	case VFSProvisionerToolAttach, VFSProvisionerToolDetach, VFSProvisionerToolSnapshot:
		return true
	default:
		return false
	}
}

func noDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func stringInList(value string, allowed []string) bool {
	value = strings.TrimSpace(value)
	for _, candidate := range allowed {
		if value == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

func referencedClaimExists(board *ClaimsBoard, claimID string) bool {
	if board == nil {
		return true
	}
	_, ok := board.CloneClaim(claimID)
	return ok
}

func infrastructureValidationErrorf(req ValidatorHandlerRequest, format string, args ...any) (ValidatorHandlerResult, error) {
	return infrastructureValidatorFailure(req, fmt.Sprintf(format, args...), nil)
}

func defaultInfrastructureValidatorRegistrations() []ValidatorRegistration {
	return []ValidatorRegistration{
		infrastructureValidatorRegistration("dag.acyclic", ArtifactDataTypeDAGOperation, DAGAcyclicityValidator{}),
		infrastructureValidatorRegistration("dag.required_nodes", ArtifactDataTypeDAGOperation, DAGRequiredNodeCoverageValidator{}),
		infrastructureValidatorRegistration("dag.scope_subset", ArtifactDataTypeDAGOperation, DAGScopeSubsetValidator{}),
		infrastructureValidatorRegistration("dag.pod_count", ArtifactDataTypeDAGOperation, DAGPodCountValidator{}),
		infrastructureValidatorRegistration("dag.health", ArtifactDataTypeDAGOperation, DAGHealthValidator{}),
		infrastructureValidatorRegistration("vfs.attachment", ArtifactDataTypeVFSOperation, VFSAttachmentValidator{}),
		infrastructureValidatorRegistration("vfs.capacity", ArtifactDataTypeVFSOperation, VFSCapacityValidator{}),
		infrastructureValidatorRegistration("vfs.base_version", ArtifactDataTypeVFSOperation, VFSBaseVersionValidator{}),
		infrastructureValidatorRegistration("vfs.scope_subset", ArtifactDataTypeVFSOperation, VFSScopeSubsetValidator{}),
		infrastructureValidatorRegistration("vfs.read_write_boundary", ArtifactDataTypeVFSOperation, VFSReadWriteBoundaryValidator{}),
		infrastructureValidatorRegistration("vfs.merge_acyclicity", ArtifactDataTypeVFSOperation, VFSMergeAcyclicityValidator{}),
		infrastructureValidatorRegistration("vfs.conflict_absence", ArtifactDataTypeVFSOperation, VFSConflictAbsenceValidator{}),
		infrastructureValidatorRegistration("vfs.merged_version_reachable", ArtifactDataTypeVFSOperation, VFSMergedVersionReachableValidator{}),
		infrastructureValidatorRegistration("tool.policy_allow", ArtifactDataTypeToolRuntimeExecution, ToolPolicyAllowValidator{}),
		infrastructureValidatorRegistration("tool.execution_mode", ArtifactDataTypeToolRuntimeExecution, ToolExecutionModeValidator{}),
		infrastructureValidatorRegistration("tool.scope_boundary", ArtifactDataTypeToolRuntimeExecution, ToolScopeBoundaryValidator{}),
		infrastructureValidatorRegistration("tool.duration", ArtifactDataTypeToolRuntimeExecution, ToolDurationValidator{}),
		infrastructureValidatorRegistration("tool.expected_output", ArtifactDataTypeToolRuntimeExecution, ToolExpectedOutputValidator{}),
		infrastructureValidatorRegistration("tool.required_artifacts", ArtifactDataTypeToolRuntimeExecution, ToolRequiredArtifactPresenceValidator{}),
		infrastructureValidatorRegistration("knowledge.embedding_dimension", ArtifactDataTypeKnowledgeOperation, KnowledgeEmbeddingDimensionValidator{}),
		infrastructureValidatorRegistration("knowledge.node_retrievable", ArtifactDataTypeKnowledgeOperation, KnowledgeNodeRetrievableValidator{}),
		infrastructureValidatorRegistration("knowledge.edge_consistency", ArtifactDataTypeKnowledgeOperation, KnowledgeEdgeConsistencyValidator{}),
		infrastructureValidatorRegistration("knowledge.query_shape", ArtifactDataTypeKnowledgeOperation, KnowledgeQueryShapeValidator{}),
		infrastructureValidatorRegistration("knowledge.score_range", ArtifactDataTypeKnowledgeOperation, KnowledgeScoreRangeValidator{}),
		infrastructureValidatorRegistration("knowledge.readiness", ArtifactDataTypeKnowledgeOperation, KnowledgeReadinessValidator{}),
		infrastructureValidatorRegistration("memory.source_availability", ArtifactDataTypeMemoryContinuity, CarryForwardSourceAvailabilityValidator{}),
		infrastructureValidatorRegistration("memory.cursor_monotonicity", ArtifactDataTypeMemoryContinuity, CarryForwardCursorMonotonicityValidator{}),
		infrastructureValidatorRegistration("memory.digest_consistency", ArtifactDataTypeMemoryContinuity, CarryForwardDigestConsistencyValidator{}),
		infrastructureValidatorRegistration("memory.contradiction_state", ArtifactDataTypeMemoryContinuity, CarryForwardContradictionStateValidator{}),
		infrastructureValidatorRegistration("document.indexed", ArtifactDataTypeDocumentOperation, DocumentIndexedValidator{}),
		infrastructureValidatorRegistration("document.attachment_list", ArtifactDataTypeDocumentOperation, DocumentAttachmentListValidator{}),
		infrastructureValidatorRegistration("document.chunk_boundaries", ArtifactDataTypeDocumentOperation, DocumentChunkBoundariesValidator{}),
		infrastructureValidatorRegistration("document.result_shape", ArtifactDataTypeDocumentOperation, DocumentResultShapeValidator{}),
		infrastructureValidatorRegistration("document.full_text_backend", ArtifactDataTypeDocumentOperation, DocumentFullTextBackendReadinessValidator{}),
		infrastructureValidatorRegistration("guardian.policy_match", ArtifactDataTypeGuardianDecision, GuardianPolicyMatchValidator{}),
		infrastructureValidatorRegistration("guardian.user_approval", ArtifactDataTypeGuardianDecision, GuardianUserApprovalPresentValidator{}),
		infrastructureValidatorRegistration("guardian.branch_protection", ArtifactDataTypeGuardianDecision, GuardianBranchProtectionValidator{}),
		infrastructureValidatorRegistration("guardian.diff_findings_absent", ArtifactDataTypeGuardianDecision, GuardianDiffFindingsAbsentValidator{}),
		infrastructureValidatorRegistration("guardian.rollback_receipt", ArtifactDataTypeGuardianDecision, GuardianRollbackReceiptValidator{}),
		infrastructureValidatorRegistration("provider.response_present", ArtifactDataTypeProviderGatewayCall, ProviderResponsePresentValidator{}),
		infrastructureValidatorRegistration("provider.usage_budget", ArtifactDataTypeProviderGatewayCall, ProviderUsageInBudgetValidator{}),
		infrastructureValidatorRegistration("provider.rate_limit", ArtifactDataTypeProviderGatewayCall, ProviderRateLimitNotExceededValidator{}),
		infrastructureValidatorRegistration("provider.model_allowed", ArtifactDataTypeProviderGatewayCall, ProviderModelAllowedValidator{}),
		infrastructureValidatorRegistration("provider.identity_present", ArtifactDataTypeProviderGatewayCall, ProviderIdentityPresentValidator{}),
		infrastructureValidatorRegistration("external.source_authenticity", ArtifactDataTypeExternalAdapterEvent, ExternalSourceAuthenticityValidator{}),
		infrastructureValidatorRegistration("external.schema", ArtifactDataTypeExternalAdapterEvent, ExternalSchemaValidator{}),
		infrastructureValidatorRegistration("external.freshness", ArtifactDataTypeExternalAdapterEvent, ExternalFreshnessValidator{}),
		infrastructureValidatorRegistration("external.referenced_claim", ArtifactDataTypeExternalAdapterEvent, ExternalReferencedClaimValidator{}),
	}
}

func infrastructureValidatorRegistration(id, dataType string, handler ValidatorHandler) ValidatorRegistration {
	return ValidatorRegistration{
		ValidatorID:        "infrastructure." + id,
		ValidationType:     ValidationTypeContract,
		Determinism:        HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "infrastructure",
		ArtifactDataType:   dataType,
		ResultDataType:     ArtifactDataTypePresentationEvidence,
		Handler:            handler,
	}
}
