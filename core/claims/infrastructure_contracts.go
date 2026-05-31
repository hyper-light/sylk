package claims

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrInfrastructureServiceContractInvalid = errors.New("infrastructure service contract invalid")

type InfrastructureServiceActionContract struct {
	Action                    ActionType
	Tools                     []string
	RequiresReceiptValidation bool
	ValidatorIDs              []string
}

type InfrastructureServiceContract struct {
	ParticipantType string
	ParticipantID   string
	Category        ParticipantCategory
	ScopeKeys       []string
	Determinism     HandlerDeterminism
	HandlerBoundary string
	HandlerFactory  func() ServiceHandler
	Actions         []InfrastructureServiceActionContract
	ValidatorIDs    []string
}

func InfrastructureServiceContracts() []InfrastructureServiceContract {
	return append(ClaimsOwnedInfrastructureServiceContracts(), BootSequencerInfrastructureServiceContract())
}

func ClaimsOwnedInfrastructureServiceContracts() []InfrastructureServiceContract {
	contracts := []InfrastructureServiceContract{
		identityRegistryContract(),
		activationControllerContract(),
		dagProcessorContract(),
		pipelineVFSContract(),
		toolVFSContract(),
		globalVFSContract(),
		knowledgeWriterContract(),
		knowledgeReaderContract(),
		documentWriterContract(),
		documentReaderContract(),
		guardianContract(),
		toolRuntimeContract(),
		providerGatewayContract(),
		sessionManagerContract(),
		fabricSubscriberContract(),
		busAdministratorContract(),
	}
	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].ParticipantType < contracts[j].ParticipantType
	})
	return contracts
}

func BootSequencerInfrastructureServiceContract() InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: "boot_sequencer",
		ParticipantID:   "sys:boot_sequencer",
		Category:        ParticipantCategorySystem,
		ScopeKeys:       []string{"process_uid"},
		Determinism:     HandlerDeterminismSideEffect,
		HandlerBoundary: "core/boot.NewBootSequencerService",
		Actions: []InfrastructureServiceActionContract{{
			Action:                    ActionTypeBoot,
			Tools:                     []string{"boot_phase", "boot_setup", "boot_detect", "boot_allocate", "boot_ingest", "boot_commit", "boot_finalize"},
			RequiresReceiptValidation: true,
			ValidatorIDs:              []string{"infrastructure.boot.phase_order", "infrastructure.boot.duration"},
		}},
		ValidatorIDs: []string{"infrastructure.boot.setup", "infrastructure.boot.detect", "infrastructure.boot.allocate", "infrastructure.boot.ingest", "infrastructure.boot.commit", "infrastructure.boot.finalize"},
	}
}

func ValidateInfrastructureServiceContracts(contracts []InfrastructureServiceContract, validators []ValidatorRegistration) error {
	validatorIDs := infrastructureValidatorIDSet(validators)
	for _, contract := range contracts {
		if err := validateInfrastructureServiceContract(contract, validatorIDs); err != nil {
			return err
		}
	}
	return nil
}

func infrastructureValidatorIDSet(validators []ValidatorRegistration) map[string]struct{} {
	out := make(map[string]struct{}, len(validators))
	for _, validator := range validators {
		out[strings.TrimSpace(validator.ValidatorID)] = struct{}{}
	}
	return out
}

func validateInfrastructureServiceContract(contract InfrastructureServiceContract, validators map[string]struct{}) error {
	if err := validateInfrastructureServiceIdentityContract(contract); err != nil {
		return err
	}
	if len(contractValidatorIDs(contract)) == 0 {
		return fmt.Errorf("%w: %s requires at least one validator", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	if err := validateInfrastructureServiceActionContracts(contract, validators); err != nil {
		return err
	}
	return validateInfrastructureServiceValidatorIDs(contract.ParticipantType, contractValidatorIDs(contract), validators)
}

func validateInfrastructureServiceIdentityContract(contract InfrastructureServiceContract) error {
	if strings.TrimSpace(contract.ParticipantType) == "" || strings.TrimSpace(contract.ParticipantID) == "" {
		return fmt.Errorf("%w: participant type and id are required", ErrInfrastructureServiceContractInvalid)
	}
	if !contract.Category.Valid() || !contract.Determinism.Valid() {
		return fmt.Errorf("%w: %s category and determinism are required", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	if len(contract.ScopeKeys) == 0 || strings.TrimSpace(contract.HandlerBoundary) == "" {
		return fmt.Errorf("%w: %s requires scope keys and handler boundary", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	if contract.HandlerFactory == nil {
		return fmt.Errorf("%w: %s requires handler factory", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	handler := contract.HandlerFactory()
	if handler == nil {
		return fmt.Errorf("%w: %s handler factory returned nil", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	if err := validateInfrastructureHandlerToolCoverage(contract, handler); err != nil {
		return err
	}
	return nil
}

func validateInfrastructureHandlerToolCoverage(contract InfrastructureServiceContract, handler ServiceHandler) error {
	catalog, ok := handler.(ServiceToolCatalog)
	if !ok {
		return fmt.Errorf("%w: %s handler does not expose tool catalog", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	tools := trimmedStringSet(catalog.ServiceTools())
	for _, action := range contract.Actions {
		for _, tool := range action.Tools {
			if _, ok := tools[strings.TrimSpace(tool)]; !ok {
				return fmt.Errorf("%w: %s action %q references unhandled tool %s", ErrInfrastructureServiceContractInvalid, contract.ParticipantType, action.Action, tool)
			}
		}
	}
	return nil
}

func validateInfrastructureServiceActionContracts(contract InfrastructureServiceContract, validators map[string]struct{}) error {
	if len(contract.Actions) == 0 {
		return fmt.Errorf("%w: %s requires action coverage", ErrInfrastructureServiceContractInvalid, contract.ParticipantType)
	}
	for _, action := range contract.Actions {
		if err := validateInfrastructureServiceActionContract(contract, action, validators); err != nil {
			return err
		}
	}
	return nil
}

func validateInfrastructureServiceActionContract(contract InfrastructureServiceContract, action InfrastructureServiceActionContract, validators map[string]struct{}) error {
	if strings.TrimSpace(string(action.Action)) == "" || len(action.Tools) == 0 || !action.RequiresReceiptValidation {
		return fmt.Errorf("%w: %s action %q requires handler tools and receipt validation", ErrInfrastructureServiceContractInvalid, contract.ParticipantType, action.Action)
	}
	return validateInfrastructureServiceValidatorIDs(contract.ParticipantType, action.ValidatorIDs, validators)
}

func validateInfrastructureServiceValidatorIDs(participantType string, ids []string, validators map[string]struct{}) error {
	for _, id := range ids {
		if _, ok := validators[strings.TrimSpace(id)]; !ok {
			return fmt.Errorf("%w: %s references unregistered validator %s", ErrInfrastructureServiceContractInvalid, participantType, id)
		}
	}
	return nil
}

func contractValidatorIDs(contract InfrastructureServiceContract) []string {
	out := append([]string(nil), contract.ValidatorIDs...)
	for _, action := range contract.Actions {
		out = append(out, action.ValidatorIDs...)
	}
	sort.Strings(out)
	return out
}

func trimmedStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[strings.TrimSpace(value)] = struct{}{}
	}
	return out
}

func serviceTaskContract(tools, validators []string) []InfrastructureServiceActionContract {
	return []InfrastructureServiceActionContract{{
		Action:                    ActionTypeTask,
		Tools:                     append([]string(nil), tools...),
		RequiresReceiptValidation: true,
		ValidatorIDs:              append([]string(nil), validators...),
	}}
}

func identityRegistryContract() InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: "identity_registry",
		ParticipantID:   "sys:identity_registry",
		Category:        ParticipantCategorySystem,
		ScopeKeys:       []string{"process_uid"},
		Determinism:     HandlerDeterminismPure,
		HandlerBoundary: "core/claims.NewIdentityRegistryService",
		HandlerFactory:  func() ServiceHandler { return NewIdentityRegistryService(IdentityRegistryServiceConfig{}) },
		Actions: serviceTaskContract(
			[]string{IdentityRegistryToolAllocate, IdentityRegistryToolLookup, IdentityRegistryToolLineage},
			[]string{"infrastructure.identity.deterministic", "infrastructure.identity.generation_monotonic"},
		),
	}
}

func activationControllerContract() InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: "activation_controller",
		ParticipantID:   "sys:activation_controller",
		Category:        ParticipantCategoryService,
		ScopeKeys:       []string{"session_id"},
		Determinism:     HandlerDeterminismSideEffect,
		HandlerBoundary: "core/claims.NewActivationControllerService",
		HandlerFactory:  func() ServiceHandler { return NewActivationControllerService(ActivationControllerServiceConfig{}) },
		Actions: serviceTaskContract(
			[]string{ActivationControllerToolActivate, ActivationControllerToolDeactivate, ActivationControllerToolQueryTier},
			[]string{"infrastructure.activation.tier", "infrastructure.activation.replica_count", "infrastructure.activation.duration"},
		),
		ValidatorIDs: []string{"infrastructure.activation.readiness"},
	}
}

func dagProcessorContract() InfrastructureServiceContract {
	return infrastructureServiceContract("dag_processor", "sys:dag_processor", HandlerDeterminismSideEffect, "core/claims.NewDAGProcessorService", func() ServiceHandler {
		return NewDAGProcessorService(InfrastructureServiceConfig{})
	}, []string{DAGProcessorToolAllocatePipeline, DAGProcessorToolDeallocatePipeline, DAGProcessorToolQueryState, DAGProcessorToolRepair, DAGProcessorToolReportHealth}, []string{"infrastructure.dag.acyclic", "infrastructure.dag.required_nodes", "infrastructure.dag.scope_subset", "infrastructure.dag.pod_count", "infrastructure.dag.health"})
}

func pipelineVFSContract() InfrastructureServiceContract {
	return vfsContract("vfs_provisioner", "sys:vfs_pipeline_provisioner", "pipeline")
}

func toolVFSContract() InfrastructureServiceContract {
	return vfsContract("tool_vfs_provisioner", "sys:vfs_tool_provisioner", "tool")
}

func globalVFSContract() InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: "global_vfs_merger",
		ParticipantID:   "sys:vfs_global_provisioner",
		Category:        ParticipantCategoryService,
		ScopeKeys:       []string{"session_id"},
		Determinism:     HandlerDeterminismContent,
		HandlerBoundary: "core/claims.NewVFSProvisionerService",
		HandlerFactory:  func() ServiceHandler { return NewVFSProvisionerService("global", InfrastructureServiceConfig{}) },
		Actions: serviceTaskContract(
			[]string{VFSProvisionerToolMerge, VFSProvisionerToolRollback, VFSProvisionerToolPromote},
			[]string{"infrastructure.vfs.merge_acyclicity", "infrastructure.vfs.conflict_absence", "infrastructure.vfs.merged_version_reachable"},
		),
	}
}

func vfsContract(participantType, participantID, scope string) InfrastructureServiceContract {
	return infrastructureServiceContract(participantType, participantID, HandlerDeterminismSideEffect, "core/claims.NewVFSProvisionerService", func() ServiceHandler {
		return NewVFSProvisionerService(scope, InfrastructureServiceConfig{})
	}, []string{VFSProvisionerToolProvision, VFSProvisionerToolAttach, VFSProvisionerToolDetach, VFSProvisionerToolSnapshot, VFSProvisionerToolPromote, VFSProvisionerToolMerge, VFSProvisionerToolRollback}, []string{"infrastructure.vfs.attachment", "infrastructure.vfs.capacity", "infrastructure.vfs.base_version", "infrastructure.vfs.scope_subset", "infrastructure.vfs.read_write_boundary"})
}

func knowledgeWriterContract() InfrastructureServiceContract {
	return infrastructureServiceContract("kg_writer", "sys:knowledge_graph_writer", HandlerDeterminismContent, "core/claims.NewKnowledgeService", func() ServiceHandler {
		return NewKnowledgeService(InfrastructureServiceConfig{})
	}, []string{KnowledgeToolIngestNodes, KnowledgeToolIngestEdges, KnowledgeToolUpdateEmbeddings, KnowledgeToolDeleteStale, KnowledgeToolReadiness}, []string{"infrastructure.knowledge.embedding_dimension", "infrastructure.knowledge.node_retrievable", "infrastructure.knowledge.edge_consistency", "infrastructure.knowledge.readiness"})
}

func knowledgeReaderContract() InfrastructureServiceContract {
	return infrastructureServiceContract("kg_reader", "sys:knowledge_graph_reader", HandlerDeterminismContent, "core/claims.NewKnowledgeService", func() ServiceHandler {
		return NewKnowledgeService(InfrastructureServiceConfig{})
	}, []string{KnowledgeToolQueryGraph, KnowledgeToolQuerySemantic, KnowledgeToolQueryHybrid, KnowledgeToolReadiness}, []string{"infrastructure.knowledge.query_shape", "infrastructure.knowledge.score_range", "infrastructure.knowledge.readiness"})
}

func documentWriterContract() InfrastructureServiceContract {
	return infrastructureServiceContract("doc_db_writer", "sys:document_db_writer", HandlerDeterminismContent, "core/claims.NewDocumentService", func() ServiceHandler {
		return NewDocumentService(InfrastructureServiceConfig{})
	}, []string{DocumentToolIngest, DocumentToolUpdate, DocumentToolDelete, DocumentToolChunk, DocumentToolIndex}, []string{"infrastructure.document.indexed", "infrastructure.document.attachment_list", "infrastructure.document.chunk_boundaries", "infrastructure.document.full_text_backend"})
}

func documentReaderContract() InfrastructureServiceContract {
	return infrastructureServiceContract("doc_db_reader", "sys:document_db_reader", HandlerDeterminismContent, "core/claims.NewDocumentService", func() ServiceHandler {
		return NewDocumentService(InfrastructureServiceConfig{})
	}, []string{DocumentToolLookup, DocumentToolMetadataQuery, DocumentToolSearchBleve, DocumentToolResultShape}, []string{"infrastructure.document.result_shape", "infrastructure.document.full_text_backend"})
}

func guardianContract() InfrastructureServiceContract {
	return infrastructureServiceContract("guardian", "sys:guardian_service", HandlerDeterminismSideEffect, "core/claims.NewGuardianService", func() ServiceHandler {
		return NewGuardianService(InfrastructureServiceConfig{})
	}, []string{GuardianToolApprovalCheck, GuardianToolPolicyCheck, GuardianToolBranchProtection, GuardianToolDiffReview, GuardianToolRollbackAuthorization, GuardianToolCommandGate, GuardianToolApproveCommand, GuardianToolApprovePlan, GuardianToolScanContent, GuardianToolContentScan, GuardianToolGateGit, GuardianToolFetchApproval, GuardianToolRollback}, []string{"infrastructure.guardian.policy_match", "infrastructure.guardian.user_approval", "infrastructure.guardian.branch_protection", "infrastructure.guardian.diff_findings_absent", "infrastructure.guardian.rollback_receipt"})
}

func toolRuntimeContract() InfrastructureServiceContract {
	return infrastructureServiceContract("tool_runtime", "sys:tool_runtime", HandlerDeterminismSideEffect, "core/claims.NewToolRuntimeService", func() ServiceHandler {
		return NewToolRuntimeService(InfrastructureServiceConfig{})
	}, []string{ToolRuntimeToolExecute, ToolRuntimeToolExecuteTool, ToolRuntimeToolValidateInvocation, ToolRuntimeToolQueryPolicy}, []string{"infrastructure.tool.policy_allow", "infrastructure.tool.execution_mode", "infrastructure.tool.scope_boundary", "infrastructure.tool.duration", "infrastructure.tool.expected_output", "infrastructure.tool.required_artifacts"})
}

func providerGatewayContract() InfrastructureServiceContract {
	return infrastructureServiceContract("provider_gateway", "sys:provider_gateway", HandlerDeterminismNondeterministic, "core/claims.NewProviderGatewayService", func() ServiceHandler {
		return NewProviderGatewayService(InfrastructureServiceConfig{})
	}, []string{ProviderGatewayToolComplete, ProviderGatewayToolCompleteStreaming, ProviderGatewayToolEmbedding, ProviderGatewayToolCountTokens}, []string{"infrastructure.provider.response_present", "infrastructure.provider.usage_budget", "infrastructure.provider.rate_limit", "infrastructure.provider.model_allowed", "infrastructure.provider.identity_present"})
}

func sessionManagerContract() InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: "session_manager",
		ParticipantID:   "sys:session_manager",
		Category:        ParticipantCategoryService,
		ScopeKeys:       []string{"process_uid"},
		Determinism:     HandlerDeterminismSideEffect,
		HandlerBoundary: "core/claims.NewSessionManagerService",
		HandlerFactory:  func() ServiceHandler { return NewSessionManagerService(SystemParticipantServiceConfig{}) },
		Actions:         serviceTaskContract(systemParticipantTools(systemParticipantSession), []string{"infrastructure.session.state_consistent", "infrastructure.session.persistence"}),
	}
}

func fabricSubscriberContract() InfrastructureServiceContract {
	return infrastructureServiceContract("fabric_subscriber", "sys:fabric_subscriber", HandlerDeterminismContent, "core/claims.NewFabricSubscriberService", func() ServiceHandler {
		return NewFabricSubscriberService(SystemParticipantServiceConfig{})
	}, systemParticipantTools(systemParticipantFabric), []string{"infrastructure.fabric.lens_query_shape", "infrastructure.fabric.subscription_attached"})
}

func busAdministratorContract() InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: "bus_administrator",
		ParticipantID:   "sys:bus_administrator",
		Category:        ParticipantCategorySystem,
		ScopeKeys:       []string{"process_uid"},
		Determinism:     HandlerDeterminismSideEffect,
		HandlerBoundary: "core/claims.NewBusAdministratorService",
		HandlerFactory:  func() ServiceHandler { return NewBusAdministratorService(SystemParticipantServiceConfig{}) },
		Actions:         serviceTaskContract(systemParticipantTools(systemParticipantBus), []string{"infrastructure.bus.topic_name", "infrastructure.bus.capacity_budget"}),
	}
}

func infrastructureServiceContract(participantType, participantID string, determinism HandlerDeterminism, boundary string, factory func() ServiceHandler, tools, validators []string) InfrastructureServiceContract {
	return InfrastructureServiceContract{
		ParticipantType: participantType,
		ParticipantID:   participantID,
		Category:        ParticipantCategoryService,
		ScopeKeys:       []string{"session_id"},
		Determinism:     determinism,
		HandlerBoundary: boundary,
		HandlerFactory:  factory,
		Actions:         serviceTaskContract(tools, validators),
	}
}
