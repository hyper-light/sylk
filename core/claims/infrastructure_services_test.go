package claims

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInfrastructureServicePhase6DAGVFSAndToolArtifacts(t *testing.T) {
	dag := NewDAGProcessorService(InfrastructureServiceConfig{DAGBackend: dagBackendFunc(func(_ context.Context, req DAGOperationRequest) (DAGOperationArtifactData, error) {
		return req.Requested, nil
	})})
	dagResult, err := dag.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(DAGProcessorToolAllocatePipeline, map[string]any{
		"pipeline_id":    "pipe-1",
		"nodes":          []string{"plan", "build"},
		"edges":          []map[string]any{{"from": "plan", "to": "build"}},
		"required_nodes": []string{"plan", "build"},
		"scope_boundary": map[string]string{"pipeline": "pipe-1"},
	})})
	if err != nil {
		t.Fatalf("dag HandleServiceClaim: %v", err)
	}
	dagData, err := ArtifactData[DAGOperationArtifactData](dagResult.Artifacts[0])
	if err != nil {
		t.Fatalf("DAG artifact data: %v", err)
	}
	if !dagData.CycleFree || dagData.NodeCount != 2 || dagData.EdgeCount != 1 || dagData.PlannedPods != 2 || dagData.DAGHash == "" {
		t.Fatalf("dag data = %+v, want valid derived topology evidence", dagData)
	}

	cyclicResult, err := dag.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(DAGProcessorToolRepair, map[string]any{
		"pipeline_id": "pipe-1",
		"nodes":       []string{"a", "b"},
		"edges":       []map[string]any{{"from": "a", "to": "b"}, {"from": "b", "to": "a"}},
	})})
	if err != nil {
		t.Fatalf("cyclic dag HandleServiceClaim: %v", err)
	}
	cyclicData, err := ArtifactData[DAGOperationArtifactData](cyclicResult.Artifacts[0])
	if err != nil {
		t.Fatalf("cyclic DAG artifact data: %v", err)
	}
	if cyclicData.Status != InfrastructureStatusFailed || cyclicData.FailureReason == "" || cyclicResult.Artifacts[0].Kind != ArtifactKindErrorDiagnostic {
		t.Fatalf("cyclic data/artifact = %+v/%s, want structured failure artifact", cyclicData, cyclicResult.Artifacts[0].Kind)
	}

	vfs := NewVFSProvisionerService("tool", InfrastructureServiceConfig{VFSBackend: vfsBackendFunc(func(_ context.Context, req VFSOperationRequest) (VFSOperationArtifactData, error) {
		return req.Requested, nil
	})})
	vfsResult, err := vfs.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(VFSProvisionerToolSnapshot, map[string]any{
		"mount_ref":             "tool:pipe-1:go-test",
		"capacity_budget_bytes": float64(128),
		"capacity_used_bytes":   float64(64),
		"base_version":          "base-1",
		"cow_layer_chain":       []string{"base-1", "tool-1"},
		"scope_boundary":        map[string]string{"pipeline": "pipe-1"},
	})})
	if err != nil {
		t.Fatalf("vfs HandleServiceClaim: %v", err)
	}
	vfsData, err := ArtifactData[VFSOperationArtifactData](vfsResult.Artifacts[0])
	if err != nil {
		t.Fatalf("VFS artifact data: %v", err)
	}
	if !vfsData.Attached || vfsData.Scope != "tool" || vfsData.SnapshotHash == "" || vfsData.Status != InfrastructureStatusOK {
		t.Fatalf("vfs data = %+v, want attached tool snapshot", vfsData)
	}

	conflictResult, err := vfs.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(VFSProvisionerToolMerge, map[string]any{
		"mount_ref":       "global:pipe-1",
		"conflict_set":    []string{"main.go"},
		"base_version":    "base-1",
		"cow_layer_chain": []string{"base-1", "tool-1"},
	})})
	if err != nil {
		t.Fatalf("conflict vfs HandleServiceClaim: %v", err)
	}
	conflictData, err := ArtifactData[VFSOperationArtifactData](conflictResult.Artifacts[0])
	if err != nil {
		t.Fatalf("conflict VFS artifact data: %v", err)
	}
	if conflictData.Status != InfrastructureStatusFailed || conflictData.FailureReason != "merge conflict" {
		t.Fatalf("conflict data = %+v, want merge conflict failure", conflictData)
	}

	tool := NewToolRuntimeService(InfrastructureServiceConfig{OutputSummaryLimit: len("short"), ToolBackend: toolBackendFunc(func(_ context.Context, req ToolRuntimeExecutionRequest) (ToolRuntimeExecutionArtifactData, error) {
		return req.Requested, nil
	})})
	toolResult, err := tool.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(ToolRuntimeToolExecute, map[string]any{
		"tool_name":       "go_test",
		"args":            map[string]any{"token": "secret", "package": "./core/claims"},
		"output_summary":  "short output",
		"output_refs":     []string{"artifact:test-output"},
		"sandbox_scope":   map[string]string{"pipeline": "pipe-1"},
		"execution_mode":  "sandboxed",
		"policy_decision": "allowed",
	})})
	if err != nil {
		t.Fatalf("tool HandleServiceClaim: %v", err)
	}
	toolData, err := ArtifactData[ToolRuntimeExecutionArtifactData](toolResult.Artifacts[0])
	if err != nil {
		t.Fatalf("tool artifact data: %v", err)
	}
	if !toolData.ContentTruncated || toolData.RedactedArgs["token"] != "[redacted]" || toolData.Status != InfrastructureStatusOK {
		t.Fatalf("tool data = %+v, want truncated redacted success", toolData)
	}
}

func TestInfrastructureServicePhase7KnowledgeMemoryAndDocumentArtifacts(t *testing.T) {
	knowledge := NewKnowledgeService(InfrastructureServiceConfig{KnowledgeBackend: knowledgeBackendFunc(func(_ context.Context, req KnowledgeOperationRequest) (KnowledgeOperationArtifactData, error) {
		return req.Requested, nil
	})})
	knowledgeResult, err := knowledge.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(KnowledgeToolUpdateEmbeddings, map[string]any{
		"node_count":                   float64(2),
		"edge_count":                   float64(1),
		"embedding_dimension":          float64(1536),
		"expected_embedding_dimension": float64(1536),
		"vector_index_refs":            []string{"hnsw:global"},
	})})
	if err != nil {
		t.Fatalf("knowledge HandleServiceClaim: %v", err)
	}
	knowledgeData, err := ArtifactData[KnowledgeOperationArtifactData](knowledgeResult.Artifacts[0])
	if err != nil {
		t.Fatalf("knowledge artifact data: %v", err)
	}
	if !knowledgeData.Ready || knowledgeData.FullTextBackend != "bleve" || knowledgeData.Backend != "vectorgraphdb" {
		t.Fatalf("knowledge data = %+v, want VectorDB/Bleve readiness", knowledgeData)
	}

	memory := NewMemoryForestService(InfrastructureServiceConfig{MemoryBackend: memoryBackendFunc(func(_ context.Context, req MemoryContinuityRequest) (MemoryContinuityArtifactData, error) {
		return req.Requested, nil
	})})
	memoryResult, err := memory.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(MemoryToolCarryForwardAdvance, map[string]any{
		"topic":           "claims-infra",
		"agent_id":        "librarian",
		"working_context": "phase evidence",
		"source_index":    []string{"claim:1", "testament:1"},
		"from_seq":        float64(10),
		"through_seq":     float64(12),
	})})
	if err != nil {
		t.Fatalf("memory HandleServiceClaim: %v", err)
	}
	memoryData, err := ArtifactData[MemoryContinuityArtifactData](memoryResult.Artifacts[0])
	if err != nil {
		t.Fatalf("memory artifact data: %v", err)
	}
	if memoryData.Status != InfrastructureStatusOK || memoryData.ContinuityCursor == "" || memoryData.EvidenceDigest == "" {
		t.Fatalf("memory data = %+v, want monotonic continuity evidence", memoryData)
	}

	legacyResult, err := memory.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(MemoryToolCarryForwardPreview, map[string]any{
		"topic":         "legacy",
		"legacy_no_wal": true,
	})})
	if err != nil {
		t.Fatalf("legacy memory HandleServiceClaim: %v", err)
	}
	legacyData, err := ArtifactData[MemoryContinuityArtifactData](legacyResult.Artifacts[0])
	if err != nil {
		t.Fatalf("legacy memory data: %v", err)
	}
	if legacyData.Status != InfrastructureStatusFailed || !legacyData.LegacyNoWAL || legacyData.SourceAvailable {
		t.Fatalf("legacy data = %+v, want failed no-WAL artifact", legacyData)
	}
	if legacyData.FailureReason != "durable carry-forward WAL missing" {
		t.Fatalf("legacy failure reason = %q, want durable WAL missing", legacyData.FailureReason)
	}

	document := NewDocumentService(InfrastructureServiceConfig{DocumentBackend: documentBackendFunc(func(_ context.Context, req DocumentOperationRequest) (DocumentOperationArtifactData, error) {
		return req.Requested, nil
	})})
	documentResult, err := document.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(DocumentToolIngest, map[string]any{
		"document_id":       "docs/CLAIMS_AND_INFRASTRUCTURE.md",
		"content":           "claims infrastructure",
		"chunk_boundaries":  []map[string]any{{"index": float64(0), "start": float64(0), "end": float64(6)}, {"index": float64(1), "start": float64(6), "end": float64(22)}},
		"attachment_refs":   []string{"artifact:plan"},
		"full_text_backend": "bleve",
	})})
	if err != nil {
		t.Fatalf("document HandleServiceClaim: %v", err)
	}
	documentData, err := ArtifactData[DocumentOperationArtifactData](documentResult.Artifacts[0])
	if err != nil {
		t.Fatalf("document artifact data: %v", err)
	}
	if !documentData.Indexed || documentData.ChunkCount != 2 || documentData.FullTextBackend != "bleve" || documentData.ContentHash == "" {
		t.Fatalf("document data = %+v, want indexed chunk evidence", documentData)
	}
}

func TestInfrastructureServicePhase8GuardianProviderAndExternalArtifacts(t *testing.T) {
	guardian := NewGuardianService(InfrastructureServiceConfig{GuardianBackend: guardianBackendFunc(func(_ context.Context, req GuardianDecisionRequest) (GuardianDecisionArtifactData, error) {
		return req.Requested, nil
	})})
	guardianResult, err := guardian.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(GuardianToolCommandGate, map[string]any{
		"policy_rule":      "command.requires_approval",
		"approval_subject": "rm -rf build",
		"user_decision":    "deny",
	})})
	if err != nil {
		t.Fatalf("guardian HandleServiceClaim: %v", err)
	}
	guardianData, err := ArtifactData[GuardianDecisionArtifactData](guardianResult.Artifacts[0])
	if err != nil {
		t.Fatalf("guardian artifact data: %v", err)
	}
	if guardianData.Allowed || guardianData.Status != InfrastructureStatusFailed || guardianResult.Artifacts[0].Kind != ArtifactKindPolicyDenied {
		t.Fatalf("guardian data/artifact = %+v/%s, want policy-denied testament artifact", guardianData, guardianResult.Artifacts[0].Kind)
	}

	provider := NewProviderGatewayService(InfrastructureServiceConfig{ProviderBackend: providerBackendFunc(func(_ context.Context, req ProviderGatewayCallRequest) (ProviderGatewayCallArtifactData, error) {
		return req.Requested, nil
	})})
	providerResult, err := provider.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(ProviderGatewayToolCompleteStreaming, map[string]any{
		"model":             "test-model",
		"identity":          "engineer",
		"task_ref":          "task-1",
		"budget_tokens":     float64(10),
		"input_tokens":      float64(7),
		"output_tokens":     float64(8),
		"response_summary":  "partial response",
		"finish_reason":     "length",
		"partial_summaries": []string{"chunk-1", "chunk-2"},
	})})
	if err != nil {
		t.Fatalf("provider HandleServiceClaim: %v", err)
	}
	providerData, err := ArtifactData[ProviderGatewayCallArtifactData](providerResult.Artifacts[0])
	if err != nil {
		t.Fatalf("provider artifact data: %v", err)
	}
	if providerData.Status != InfrastructureStatusFailed || providerData.FailureReason != "provider budget exceeded" {
		t.Fatalf("provider data = %+v, want structured budget failure", providerData)
	}

	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "external-board", SessionID: "external-session", TaskID: "task"})
	external := NewExternalAdapterService(InfrastructureServiceConfig{ExternalBackend: externalBackendFunc(func(_ context.Context, req ExternalAdapterEventRequest) (ExternalAdapterEventArtifactData, error) {
		return req.Requested, nil
	})})
	externalResult, err := external.HandleServiceClaim(context.Background(), ServiceClaimRequest{Board: board, Claim: infrastructureServiceClaim(ExternalAdapterToolCIResult, map[string]any{
		"source_id":           "ci/github",
		"schema":              "ci.result.v1",
		"raw_event":           `{"status":"passed"}`,
		"parsed_payload":      map[string]any{"status": "passed"},
		"referenced_claim_id": "missing-claim",
	})})
	if err != nil {
		t.Fatalf("external HandleServiceClaim: %v", err)
	}
	externalData, err := ArtifactData[ExternalAdapterEventArtifactData](externalResult.Artifacts[0])
	if err != nil {
		t.Fatalf("external artifact data: %v", err)
	}
	if externalData.Valid || !externalData.Rejected || externalData.Source.Category != string(ParticipantCategoryExternal) {
		t.Fatalf("external data = %+v, want rejected external participant evidence", externalData)
	}
}

func TestInfrastructureServicesMissingBackendEmitFailureArtifacts(t *testing.T) {
	tests := []struct {
		name string
		run  func() (*Artifact, error)
		want string
	}{
		{
			name: "dag",
			run: func() (*Artifact, error) {
				result, err := NewDAGProcessorService(InfrastructureServiceConfig{}).HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(DAGProcessorToolAllocatePipeline, map[string]any{"pipeline_id": "pipe-missing", "nodes": []string{"n"}})})
				return result.Artifacts[0], err
			},
			want: "DAG backend not configured",
		},
		{
			name: "provider",
			run: func() (*Artifact, error) {
				result, err := NewProviderGatewayService(InfrastructureServiceConfig{}).HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(ProviderGatewayToolComplete, map[string]any{"model": "m", "identity": "guide", "response_summary": "ok"})})
				return result.Artifacts[0], err
			},
			want: "provider gateway backend not configured",
		},
		{
			name: "guardian",
			run: func() (*Artifact, error) {
				result, err := NewGuardianService(InfrastructureServiceConfig{}).HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(GuardianToolPolicyCheck, map[string]any{"approval_subject": "cmd"})})
				return result.Artifacts[0], err
			},
			want: "guardian backend not configured",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact, err := tt.run()
			if err != nil {
				t.Fatalf("HandleServiceClaim: %v", err)
			}
			if artifact.Kind != ArtifactKindErrorDiagnostic && artifact.Kind != ArtifactKindPolicyDenied {
				t.Fatalf("artifact kind = %s, want failure artifact", artifact.Kind)
			}
			if len(artifact.Errors) == 0 || artifact.Errors[0].Description != tt.want {
				t.Fatalf("artifact errors = %+v, want %q", artifact.Errors, tt.want)
			}
		})
	}
}

func TestInfrastructureBackendFalseStatesAreAuthoritative(t *testing.T) {
	memory := NewMemoryForestService(InfrastructureServiceConfig{MemoryBackend: memoryBackendFunc(func(_ context.Context, req MemoryContinuityRequest) (MemoryContinuityArtifactData, error) {
		data := req.Requested
		data.SourceAvailable = false
		return data, nil
	})})
	memoryResult, err := memory.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(MemoryToolCarryForwardAdvance, map[string]any{
		"topic":            "claims-infra",
		"working_context":  "source was removed",
		"source_available": true,
	})})
	if err != nil {
		t.Fatalf("memory HandleServiceClaim: %v", err)
	}
	memoryData, err := ArtifactData[MemoryContinuityArtifactData](memoryResult.Artifacts[0])
	if err != nil {
		t.Fatalf("memory data: %v", err)
	}
	if memoryData.SourceAvailable || memoryData.Status != InfrastructureStatusFailed || memoryData.FailureReason != "carry-forward source unavailable" {
		t.Fatalf("memory data = %+v, want backend source unavailable failure", memoryData)
	}

	guardian := NewGuardianService(InfrastructureServiceConfig{GuardianBackend: guardianBackendFunc(func(_ context.Context, req GuardianDecisionRequest) (GuardianDecisionArtifactData, error) {
		data := req.Requested
		data.Allowed = false
		data.UserDecision = "allow"
		data.ApprovalPresent = false
		return data, nil
	})})
	guardianResult, err := guardian.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(GuardianToolApprovalCheck, map[string]any{
		"approval_subject": "deploy",
		"user_decision":    "allow",
	})})
	if err != nil {
		t.Fatalf("guardian HandleServiceClaim: %v", err)
	}
	guardianData, err := ArtifactData[GuardianDecisionArtifactData](guardianResult.Artifacts[0])
	if err != nil {
		t.Fatalf("guardian data: %v", err)
	}
	if guardianData.Allowed || guardianData.Status != InfrastructureStatusFailed || guardianData.FailureReason != "approval missing" {
		t.Fatalf("guardian data = %+v, want backend denial to remain authoritative", guardianData)
	}
}

func TestInfrastructureValidatorsPassAndFail(t *testing.T) {
	dagArtifact, err := dagOperationArtifact(DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, CycleFree: true, HealthSummary: "ok", Status: InfrastructureStatusOK, PlannedPods: 1})
	if err != nil {
		t.Fatalf("dagOperationArtifact: %v", err)
	}
	if result, err := (DAGAcyclicityValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: dagArtifact}); err != nil || result.Error != nil {
		t.Fatalf("DAGAcyclicityValidator pass result=%+v err=%v", result, err)
	}
	badDAGArtifact, err := dagOperationArtifact(DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, CycleFree: false, CyclePath: []string{"a", "b", "a"}, FailureReason: "cycle detected", Status: InfrastructureStatusFailed})
	if err != nil {
		t.Fatalf("bad dagOperationArtifact: %v", err)
	}
	if result, err := (DAGAcyclicityValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: badDAGArtifact}); err != nil || result.Error == nil {
		t.Fatalf("DAGAcyclicityValidator fail result=%+v err=%v, want validation error", result, err)
	}

	documentArtifact, err := documentOperationArtifact(DocumentOperationArtifactData{Operation: DocumentToolSearchBleve, DocumentID: "doc", FullTextBackend: "bleve", Indexed: true, ResultShape: "document_results"})
	if err != nil {
		t.Fatalf("documentOperationArtifact: %v", err)
	}
	if result, err := (DocumentFullTextBackendReadinessValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: documentArtifact}); err != nil || result.Error != nil {
		t.Fatalf("DocumentFullTextBackendReadinessValidator pass result=%+v err=%v", result, err)
	}

	providerArtifact, err := providerGatewayCallArtifact(ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Model: "allowed", Identity: "guide", BudgetTokens: 100, TotalTokens: 50, ResponseSummary: "ok"})
	if err != nil {
		t.Fatalf("providerGatewayCallArtifact: %v", err)
	}
	if result, err := (ProviderModelAllowedValidator{AllowedModels: []string{"allowed"}}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: providerArtifact}); err != nil || result.Error != nil {
		t.Fatalf("ProviderModelAllowedValidator pass result=%+v err=%v", result, err)
	}
	if result, err := (ProviderModelAllowedValidator{AllowedModels: []string{"other"}}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: providerArtifact}); err != nil || result.Error == nil {
		t.Fatalf("ProviderModelAllowedValidator fail result=%+v err=%v, want validation error", result, err)
	}
}

func TestRegisterDefaultInfrastructureValidators(t *testing.T) {
	registry := NewValidatorRegistry()
	if err := RegisterDefaultInfrastructureValidators(registry); err != nil {
		t.Fatalf("RegisterDefaultInfrastructureValidators: %v", err)
	}
	if err := RegisterDefaultInfrastructureValidators(registry); err != nil {
		t.Fatalf("RegisterDefaultInfrastructureValidators duplicate-compatible: %v", err)
	}
}

func TestInfrastructureServiceConcurrentVFSAllocationsRemainIsolated(t *testing.T) {
	service := NewVFSProvisionerService("tool", InfrastructureServiceConfig{VFSBackend: vfsBackendFunc(func(_ context.Context, req VFSOperationRequest) (VFSOperationArtifactData, error) {
		return req.Requested, nil
	})})
	const workers = 32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for idx := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claim := infrastructureServiceClaim(VFSProvisionerToolProvision, map[string]any{
				"mount_ref":             "tool:pipe:" + string(rune('a'+i)),
				"scope_boundary":        map[string]string{"tool": string(rune('a' + i))},
				"capacity_budget_bytes": float64(1024),
			})
			result, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: claim})
			if err == nil && len(result.Artifacts) != 1 {
				err = errors.New("missing vfs artifact")
			}
			errs <- err
		}(idx)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent VFS allocation: %v", err)
		}
	}
}

func TestInfrastructureVFSShutdownRecordsDetachArtifacts(t *testing.T) {
	service := NewVFSProvisionerService("tool", InfrastructureServiceConfig{VFSBackend: vfsBackendFunc(func(_ context.Context, req VFSOperationRequest) (VFSOperationArtifactData, error) {
		return req.Requested, nil
	})})
	if _, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: infrastructureServiceClaim(VFSProvisionerToolProvision, map[string]any{
		"mount_ref": "tool:pipe:shutdown",
	})}); err != nil {
		t.Fatalf("provision HandleServiceClaim: %v", err)
	}
	result, err := service.ShutdownService(context.Background(), ServiceShutdownRequest{SessionID: "session"})
	if err != nil {
		t.Fatalf("ShutdownService: %v", err)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("shutdown artifacts = %d, want 1", len(result.Artifacts))
	}
	data, err := ArtifactData[VFSOperationArtifactData](result.Artifacts[0])
	if err != nil {
		t.Fatalf("shutdown artifact data: %v", err)
	}
	if data.Operation != VFSProvisionerToolDetach || data.Attached || !data.Shutdown {
		t.Fatalf("shutdown data = %+v, want detach shutdown evidence", data)
	}
}

func TestInfrastructureServiceE2EDispatchPostsServiceTestament(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "infra-board", SessionID: "infra-session", TaskID: "task"})
	participant, err := NewServiceParticipantRegistration("sys:dag_processor", map[string]string{"session": "infra-session"}, 8, 1, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	generated, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "orchestrator", Type: ActionTypeTask}, []Claim{{
		Title:       "Allocate DAG",
		Description: "DAG service should allocate pipeline topology.",
		ActionType:  ActionTypeTask,
		Relations: []Relation{
			{Related: "orchestrator", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "sys:dag_processor", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		ExpectedToolCalls: []ExpectedToolCall{{Tool: DAGProcessorToolAllocatePipeline, Arguments: map[string]any{
			"pipeline_id":    "pipe-e2e",
			"nodes":          []string{"plan"},
			"required_nodes": []string{"plan"},
		}}},
		Validations: []*Validation{{ID: "receipt", Type: ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}, GenerateClaimActionOptions{IdempotencyKey: "dag-e2e"})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "orchestrator", ClaimPostOptions{Reason: "dag"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	dispatcher, err := NewServiceDispatcher(ServiceDispatcherConfig{Board: board, Scope: immediateScope{}, Participant: participant, Handler: NewDAGProcessorService(InfrastructureServiceConfig{DAGBackend: dagBackendFunc(func(_ context.Context, req DAGOperationRequest) (DAGOperationArtifactData, error) {
		return req.Requested, nil
	})})})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDeltaForParticipant(board, claimID, participant, ActionTypeTask))
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DispatchDelta: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("DAG service dispatch deadlocked")
	}
	assertServiceClaimLifecycle(t, board, claimID, ClaimLifecycleSatisfied)
	artifact := findTestamentArtifactByName(t, board, claimID, "dag_operation")
	data, err := ArtifactData[DAGOperationArtifactData](artifact)
	if err != nil || data.PipelineID != "pipe-e2e" || !data.CycleFree {
		t.Fatalf("dag testament data = %+v err=%v, want pipe-e2e acyclic", data, err)
	}
}

func TestRecordInfrastructureEvidenceRecordsSystemClaimAndTestament(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "infra-evidence", SessionID: "infra-session", TaskID: "task"})
	artifact, err := vfsOperationArtifact(VFSOperationArtifactData{Operation: VFSProvisionerToolDetach, Scope: "tool", MountRef: "tool:old", Status: InfrastructureStatusOK})
	if err != nil {
		t.Fatalf("vfsOperationArtifact: %v", err)
	}
	result, err := RecordInfrastructureEvidence(context.Background(), InfrastructureEvidenceOptions{
		Board:     board,
		ActorID:   "sys:vfs_tool_provisioner",
		SubjectID: "sys:vfs_tool_provisioner",
		Operation: "vfs_detach",
		Artifact:  artifact,
	})
	if err != nil {
		t.Fatalf("RecordInfrastructureEvidence: %v", err)
	}
	assertServiceClaimLifecycle(t, board, result.ClaimID, ClaimLifecycleSatisfied)
	testaments := board.TestamentsByClaim(result.ClaimID)
	if len(testaments) != 1 || testaments[0].ID != result.TestamentID {
		t.Fatalf("testaments = %+v, want system evidence testament %s", testaments, result.TestamentID)
	}
}

func infrastructureServiceClaim(tool string, args map[string]any) *Claim {
	return &Claim{
		ID:         "claim-" + tool,
		AgentID:    "issuer",
		SessionID:  "session",
		PipelineID: "pipeline",
		TaskID:     "task",
		Relations: []Relation{
			{Related: "issuer", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "service", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		ExpectedToolCalls: []ExpectedToolCall{{ID: "call-" + tool, Tool: tool, Arguments: args}},
	}
}

type dagBackendFunc func(context.Context, DAGOperationRequest) (DAGOperationArtifactData, error)

func (fn dagBackendFunc) HandleDAGOperation(ctx context.Context, req DAGOperationRequest) (DAGOperationArtifactData, error) {
	return fn(ctx, req)
}

type vfsBackendFunc func(context.Context, VFSOperationRequest) (VFSOperationArtifactData, error)

func (fn vfsBackendFunc) HandleVFSOperation(ctx context.Context, req VFSOperationRequest) (VFSOperationArtifactData, error) {
	return fn(ctx, req)
}

type toolBackendFunc func(context.Context, ToolRuntimeExecutionRequest) (ToolRuntimeExecutionArtifactData, error)

func (fn toolBackendFunc) HandleToolRuntimeExecution(ctx context.Context, req ToolRuntimeExecutionRequest) (ToolRuntimeExecutionArtifactData, error) {
	return fn(ctx, req)
}

type knowledgeBackendFunc func(context.Context, KnowledgeOperationRequest) (KnowledgeOperationArtifactData, error)

func (fn knowledgeBackendFunc) HandleKnowledgeOperation(ctx context.Context, req KnowledgeOperationRequest) (KnowledgeOperationArtifactData, error) {
	return fn(ctx, req)
}

type memoryBackendFunc func(context.Context, MemoryContinuityRequest) (MemoryContinuityArtifactData, error)

func (fn memoryBackendFunc) HandleMemoryContinuity(ctx context.Context, req MemoryContinuityRequest) (MemoryContinuityArtifactData, error) {
	return fn(ctx, req)
}

type documentBackendFunc func(context.Context, DocumentOperationRequest) (DocumentOperationArtifactData, error)

func (fn documentBackendFunc) HandleDocumentOperation(ctx context.Context, req DocumentOperationRequest) (DocumentOperationArtifactData, error) {
	return fn(ctx, req)
}

type guardianBackendFunc func(context.Context, GuardianDecisionRequest) (GuardianDecisionArtifactData, error)

func (fn guardianBackendFunc) HandleGuardianDecision(ctx context.Context, req GuardianDecisionRequest) (GuardianDecisionArtifactData, error) {
	return fn(ctx, req)
}

type providerBackendFunc func(context.Context, ProviderGatewayCallRequest) (ProviderGatewayCallArtifactData, error)

func (fn providerBackendFunc) HandleProviderGatewayCall(ctx context.Context, req ProviderGatewayCallRequest) (ProviderGatewayCallArtifactData, error) {
	return fn(ctx, req)
}

type externalBackendFunc func(context.Context, ExternalAdapterEventRequest) (ExternalAdapterEventArtifactData, error)

func (fn externalBackendFunc) HandleExternalAdapterEvent(ctx context.Context, req ExternalAdapterEventRequest) (ExternalAdapterEventArtifactData, error) {
	return fn(ctx, req)
}
