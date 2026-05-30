package claims

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type infrastructureValidatorFixture struct {
	pass func(*testing.T) *Artifact
	fail func(*testing.T) *Artifact
}

func TestInfrastructureServiceContractsValidateClaimsOwnedCatalog(t *testing.T) {
	registry := NewValidatorRegistry()
	if err := RegisterDefaultInfrastructureValidators(registry); err != nil {
		t.Fatalf("RegisterDefaultInfrastructureValidators: %v", err)
	}
	if err := ValidateInfrastructureServiceContracts(ClaimsOwnedInfrastructureServiceContracts(), registry.List()); err != nil {
		t.Fatalf("ValidateInfrastructureServiceContracts: %v", err)
	}
}

func TestInfrastructureServiceContractsCoverInfrastructureDocCatalog(t *testing.T) {
	want := sortedStrings(documentedServiceParticipantTypes(t))
	got := sortedStrings(infrastructureContractParticipantTypes(InfrastructureServiceContracts()))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contract participant types = %v, want documented %v", got, want)
	}
}

func TestInfrastructureServiceContractsRejectMissingHandlerValidatorAndReceipt(t *testing.T) {
	validatorIDs := []ValidatorRegistration{{ValidatorID: "infrastructure.synthetic"}}
	base := InfrastructureServiceContract{
		ParticipantType: "synthetic_service",
		ParticipantID:   "sys:synthetic_service",
		Category:        ParticipantCategoryService,
		ScopeKeys:       []string{"session_id"},
		Determinism:     HandlerDeterminismPure,
		HandlerBoundary: "core/claims.NewSyntheticService",
		Actions: []InfrastructureServiceActionContract{{
			Action:                    ActionTypeTask,
			Tools:                     []string{"synthetic_tool"},
			RequiresReceiptValidation: true,
			ValidatorIDs:              []string{"infrastructure.synthetic"},
		}},
	}

	tests := []struct {
		name     string
		contract InfrastructureServiceContract
	}{
		{name: "missing handler", contract: mutateInfrastructureContract(base, func(c *InfrastructureServiceContract) { c.HandlerBoundary = "" })},
		{name: "missing determinism", contract: mutateInfrastructureContract(base, func(c *InfrastructureServiceContract) { c.Determinism = "" })},
		{name: "missing validator", contract: mutateInfrastructureContract(base, func(c *InfrastructureServiceContract) { c.Actions[0].ValidatorIDs = nil })},
		{name: "missing action handler coverage", contract: mutateInfrastructureContract(base, func(c *InfrastructureServiceContract) { c.Actions[0].Tools = nil })},
		{name: "missing receipt validation", contract: mutateInfrastructureContract(base, func(c *InfrastructureServiceContract) { c.Actions[0].RequiresReceiptValidation = false })},
		{name: "unregistered validator", contract: mutateInfrastructureContract(base, func(c *InfrastructureServiceContract) { c.Actions[0].ValidatorIDs = []string{"infrastructure.missing"} })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInfrastructureServiceContracts([]InfrastructureServiceContract{tt.contract}, validatorIDs)
			if !errors.Is(err, ErrInfrastructureServiceContractInvalid) {
				t.Fatalf("ValidateInfrastructureServiceContracts error = %v, want ErrInfrastructureServiceContractInvalid", err)
			}
		})
	}
}

func TestInfrastructureServicesDoNotExposeLegacyMutationBypassAPIs(t *testing.T) {
	assertNoExportedMethods(t, &IdentityRegistryService{}, []string{"Allocate", "Lineage"})
	assertNoExportedMethods(t, &ActivationControllerService{}, []string{"Activate", "Deactivate", "DeactivateRecord", "QueryTier"})
}

func TestDefaultInfrastructureValidatorsCoverPassFailIncompleteErrored(t *testing.T) {
	fixtures := infrastructureValidatorFixtures(t)
	for _, reg := range defaultInfrastructureValidatorRegistrations() {
		fixture, ok := fixtures[reg.ValidatorID]
		if !ok {
			t.Fatalf("missing validator fixture for %s", reg.ValidatorID)
		}
		t.Run(reg.ValidatorID, func(t *testing.T) {
			assertInfrastructureValidatorCoverage(t, reg, fixture)
		})
	}
}

func TestClaimsInfrastructureDocsReconciledWithInfrastructurePlan(t *testing.T) {
	root := claimsRepoRoot(t)
	tests := []struct {
		path    string
		markers []string
	}{
		{
			path: "docs/CLAIMS.md",
			markers: []string{
				"docs/CLAIMS_AND_INFRASTRUCTURE.md",
				"issued by one participant against another",
				"ParticipantID",
				"Participant Intake and Processing",
				"Infrastructure Participants",
			},
		},
		{
			path: "docs/CLAIMS_AND_DELTAS.md",
			markers: []string{
				"docs/CLAIMS_AND_INFRASTRUCTURE.md",
				"Canonical Participant Reference",
				"service.<service_uid>",
				"participant categories",
			},
		},
		{
			path: "docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md",
			markers: []string{
				"docs/CLAIMS_AND_INFRASTRUCTURE.md",
				"Target Participant",
				"Source Participant",
				"Service Participant Receiver Discipline",
				"Infrastructure outcomes are testaments with artifacts",
			},
		},
		{
			path: "docs/CLAIMS_VISIBILITY.md",
			markers: []string{
				"docs/CLAIMS_AND_INFRASTRUCTURE.md",
				"programmatic validators read artifacts via the same board API",
				"service-produced testaments carry presentation metadata",
				"guardian-denial testament",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			body := readClaimsFile(t, filepath.Join(root, tt.path))
			for _, marker := range tt.markers {
				if !strings.Contains(body, marker) {
					t.Fatalf("%s missing reconciliation marker %q", tt.path, marker)
				}
			}
		})
	}
}

func assertInfrastructureValidatorCoverage(t *testing.T, reg ValidatorRegistration, fixture infrastructureValidatorFixture) {
	t.Helper()
	assertValidatorPass(t, reg, fixture.pass(t))
	assertValidatorFail(t, reg, fixture.fail(t))
	assertValidatorErrored(t, reg, incompleteInfrastructureArtifact(reg), "incomplete")
	assertValidatorErrored(t, reg, malformedInfrastructureArtifact(reg), "malformed")
}

func assertValidatorPass(t *testing.T, reg ValidatorRegistration, artifact *Artifact) {
	t.Helper()
	result, err := reg.Handler.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: artifact})
	if err != nil || result.Error != nil {
		t.Fatalf("%s pass result = %+v err=%v, want pass", reg.ValidatorID, result, err)
	}
}

func assertValidatorFail(t *testing.T, reg ValidatorRegistration, artifact *Artifact) {
	t.Helper()
	result, err := reg.Handler.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: artifact})
	if err != nil || result.Error == nil {
		t.Fatalf("%s fail result = %+v err=%v, want validation failure", reg.ValidatorID, result, err)
	}
}

func assertValidatorErrored(t *testing.T, reg ValidatorRegistration, artifact *Artifact, name string) {
	t.Helper()
	if _, err := reg.Handler.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: artifact}); err == nil {
		t.Fatalf("%s %s artifact validated without error", reg.ValidatorID, name)
	}
}

func incompleteInfrastructureArtifact(reg ValidatorRegistration) *Artifact {
	return &Artifact{ArtifactName: reg.TargetArtifactName, DataType: reg.ArtifactDataType}
}

func malformedInfrastructureArtifact(reg ValidatorRegistration) *Artifact {
	data := []byte("{")
	return &Artifact{ArtifactName: reg.TargetArtifactName, DataType: reg.ArtifactDataType, Data: data, Size: int64(len(data)), ContentHash: ArtifactContentHash(data)}
}

func mutateInfrastructureContract(contract InfrastructureServiceContract, mutate func(*InfrastructureServiceContract)) InfrastructureServiceContract {
	out := contract
	out.ScopeKeys = append([]string(nil), contract.ScopeKeys...)
	out.ValidatorIDs = append([]string(nil), contract.ValidatorIDs...)
	out.Actions = append([]InfrastructureServiceActionContract(nil), contract.Actions...)
	for idx := range out.Actions {
		out.Actions[idx].Tools = append([]string(nil), contract.Actions[idx].Tools...)
		out.Actions[idx].ValidatorIDs = append([]string(nil), contract.Actions[idx].ValidatorIDs...)
	}
	mutate(&out)
	return out
}

func infrastructureContractParticipantTypes(contracts []InfrastructureServiceContract) []string {
	out := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, contract.ParticipantType)
	}
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func assertNoExportedMethods(t *testing.T, value any, methods []string) {
	t.Helper()
	typ := reflect.TypeOf(value)
	for _, method := range methods {
		if _, ok := typ.MethodByName(method); ok {
			t.Fatalf("%s exposes legacy bypass method %s", typ.String(), method)
		}
	}
}

func infrastructureValidatorFixtures(t *testing.T) map[string]infrastructureValidatorFixture {
	t.Helper()
	return map[string]infrastructureValidatorFixture{
		"infrastructure.identity.deterministic":        {pass: identityDeterministicPassArtifact, fail: identityDeterministicFailArtifact},
		"infrastructure.identity.generation_monotonic": {pass: identityGenerationPassArtifact, fail: identityGenerationFailArtifact},
		"infrastructure.activation.readiness":          {pass: activationReadyPassArtifact, fail: activationReadyFailArtifact},
		"infrastructure.activation.tier":               {pass: activationTierPassArtifact, fail: activationTierFailArtifact},
		"infrastructure.activation.replica_count":      {pass: activationReplicaPassArtifact, fail: activationReplicaFailArtifact},
		"infrastructure.activation.duration":           {pass: activationDurationPassArtifact, fail: activationDurationFailArtifact},
		"infrastructure.dag.acyclic":                   {pass: dagPassArtifact, fail: dagCycleFailArtifact},
		"infrastructure.dag.required_nodes":            {pass: dagPassArtifact, fail: dagMissingNodesFailArtifact},
		"infrastructure.dag.scope_subset":              {pass: dagPassArtifact, fail: dagScopeFailArtifact},
		"infrastructure.dag.pod_count":                 {pass: dagPassArtifact, fail: dagPodCountFailArtifact},
		"infrastructure.dag.health":                    {pass: dagPassArtifact, fail: dagHealthFailArtifact},
		"infrastructure.vfs.attachment":                {pass: vfsPassArtifact, fail: vfsAttachmentFailArtifact},
		"infrastructure.vfs.capacity":                  {pass: vfsPassArtifact, fail: vfsCapacityFailArtifact},
		"infrastructure.vfs.base_version":              {pass: vfsPassArtifact, fail: vfsBaseVersionFailArtifact},
		"infrastructure.vfs.scope_subset":              {pass: vfsPassArtifact, fail: vfsScopeFailArtifact},
		"infrastructure.vfs.read_write_boundary":       {pass: vfsPassArtifact, fail: vfsReadWriteFailArtifact},
		"infrastructure.vfs.merge_acyclicity":          {pass: vfsMergePassArtifact, fail: vfsMergeCycleFailArtifact},
		"infrastructure.vfs.conflict_absence":          {pass: vfsMergePassArtifact, fail: vfsConflictFailArtifact},
		"infrastructure.vfs.merged_version_reachable":  {pass: vfsMergePassArtifact, fail: vfsMergedVersionFailArtifact},
		"infrastructure.tool.policy_allow":             {pass: toolPassArtifact, fail: toolPolicyFailArtifact},
		"infrastructure.tool.execution_mode":           {pass: toolPassArtifact, fail: toolModeFailArtifact},
		"infrastructure.tool.scope_boundary":           {pass: toolPassArtifact, fail: toolScopeFailArtifact},
		"infrastructure.tool.duration":                 {pass: toolPassArtifact, fail: toolDurationFailArtifact},
		"infrastructure.tool.expected_output":          {pass: toolPassArtifact, fail: toolOutputFailArtifact},
		"infrastructure.tool.required_artifacts":       {pass: toolPassArtifact, fail: toolArtifactsFailArtifact},
		"infrastructure.knowledge.embedding_dimension": {pass: knowledgePassArtifact, fail: knowledgeEmbeddingFailArtifact},
		"infrastructure.knowledge.node_retrievable":    {pass: knowledgePassArtifact, fail: knowledgeNodeFailArtifact},
		"infrastructure.knowledge.edge_consistency":    {pass: knowledgePassArtifact, fail: knowledgeEdgeFailArtifact},
		"infrastructure.knowledge.query_shape":         {pass: knowledgePassArtifact, fail: knowledgeQueryShapeFailArtifact},
		"infrastructure.knowledge.score_range":         {pass: knowledgePassArtifact, fail: knowledgeScoreFailArtifact},
		"infrastructure.knowledge.readiness":           {pass: knowledgePassArtifact, fail: knowledgeReadinessFailArtifact},
		"infrastructure.memory.source_availability":    {pass: memoryPassArtifact, fail: memorySourceFailArtifact},
		"infrastructure.memory.cursor_monotonicity":    {pass: memoryPassArtifact, fail: memoryCursorFailArtifact},
		"infrastructure.memory.digest_consistency":     {pass: memoryPassArtifact, fail: memoryDigestFailArtifact},
		"infrastructure.memory.contradiction_state":    {pass: memoryPassArtifact, fail: memoryContradictionFailArtifact},
		"infrastructure.document.indexed":              {pass: documentPassArtifact, fail: documentIndexedFailArtifact},
		"infrastructure.document.attachment_list":      {pass: documentPassArtifact, fail: documentAttachmentFailArtifact},
		"infrastructure.document.chunk_boundaries":     {pass: documentPassArtifact, fail: documentChunkFailArtifact},
		"infrastructure.document.result_shape":         {pass: documentPassArtifact, fail: documentShapeFailArtifact},
		"infrastructure.document.full_text_backend":    {pass: documentPassArtifact, fail: documentBackendFailArtifact},
		"infrastructure.guardian.policy_match":         {pass: guardianPassArtifact, fail: guardianPolicyFailArtifact},
		"infrastructure.guardian.user_approval":        {pass: guardianPassArtifact, fail: guardianApprovalFailArtifact},
		"infrastructure.guardian.branch_protection":    {pass: guardianPassArtifact, fail: guardianBranchFailArtifact},
		"infrastructure.guardian.diff_findings_absent": {pass: guardianPassArtifact, fail: guardianDiffFailArtifact},
		"infrastructure.guardian.rollback_receipt":     {pass: guardianPassArtifact, fail: guardianRollbackFailArtifact},
		"infrastructure.provider.response_present":     {pass: providerPassArtifact, fail: providerResponseFailArtifact},
		"infrastructure.provider.usage_budget":         {pass: providerPassArtifact, fail: providerUsageFailArtifact},
		"infrastructure.provider.rate_limit":           {pass: providerPassArtifact, fail: providerRateLimitFailArtifact},
		"infrastructure.provider.model_allowed":        {pass: providerPassArtifact, fail: providerModelFailArtifact},
		"infrastructure.provider.identity_present":     {pass: providerPassArtifact, fail: providerIdentityFailArtifact},
		"infrastructure.external.source_authenticity":  {pass: externalPassArtifact, fail: externalSourceFailArtifact},
		"infrastructure.external.schema":               {pass: externalPassArtifact, fail: externalSchemaFailArtifact},
		"infrastructure.external.freshness":            {pass: externalPassArtifact, fail: externalFreshnessFailArtifact},
		"infrastructure.external.referenced_claim":     {pass: externalPassArtifact, fail: externalClaimFailArtifact},
		"infrastructure.session.state_consistent":      {pass: sessionPassArtifact, fail: sessionStateFailArtifact},
		"infrastructure.session.persistence":           {pass: sessionPassArtifact, fail: sessionPersistenceFailArtifact},
		"infrastructure.fabric.lens_query_shape":       {pass: fabricPassArtifact, fail: fabricLensFailArtifact},
		"infrastructure.fabric.subscription_attached":  {pass: fabricPassArtifact, fail: fabricAttachedFailArtifact},
		"infrastructure.bus.topic_name":                {pass: busPassArtifact, fail: busTopicFailArtifact},
		"infrastructure.bus.capacity_budget":           {pass: busPassArtifact, fail: busCapacityFailArtifact},
	}
}

func identityDeterministicPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	data := identityAllocationFixture(t)
	return mustTypedArtifact(t, "identity_allocation", ArtifactKindAgentID, data.UID, data)
}

func identityDeterministicFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	data := identityAllocationFixture(t)
	data.UID = "participant:service:bad"
	return mustTypedArtifact(t, "identity_allocation", ArtifactKindAgentID, data.UID, data)
}

func identityGenerationPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return identityDeterministicPassArtifact(t)
}

func identityGenerationFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	data := identityAllocationFixture(t)
	data.ParentUID = "participant:service:parent"
	data.Generation = 1
	return mustTypedArtifact(t, "identity_allocation", ArtifactKindAgentID, data.UID, data)
}

func identityAllocationFixture(t *testing.T) IdentityAllocationArtifactData {
	t.Helper()
	scope := map[string]string{"process_uid": "proc-phase-18"}
	uid, err := DeriveParticipantUID(ParticipantCategoryService, "identity_registry", scope)
	if err != nil {
		t.Fatalf("DeriveParticipantUID: %v", err)
	}
	return IdentityAllocationArtifactData{UID: uid, Category: ParticipantCategoryService, RouteKey: "identity_registry", Scope: scope, Generation: 1}
}

func activationReadyPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationArtifact(t, ActivationRecordArtifactData{ParticipantID: "sys:guardian", Operation: "activate", Tier: "always_hot", Ready: true, ReplicaCount: 1, Duration: time.Millisecond})
}

func activationReadyFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationArtifact(t, ActivationRecordArtifactData{ParticipantID: "sys:guardian", Operation: "activate", Tier: "always_hot", Ready: false, FailureReason: "replicas unavailable"})
}

func activationTierPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationReadyPassArtifact(t)
}

func activationTierFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationArtifact(t, ActivationRecordArtifactData{ParticipantID: "sys:guardian", Operation: "activate", Ready: true, ReplicaCount: 1})
}

func activationReplicaPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationReadyPassArtifact(t)
}

func activationReplicaFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationArtifact(t, ActivationRecordArtifactData{ParticipantID: "sys:guardian", Operation: "activate", Tier: "always_hot", Ready: true, ReplicaCount: -1})
}

func activationDurationPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationReadyPassArtifact(t)
}

func activationDurationFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return activationArtifact(t, ActivationRecordArtifactData{ParticipantID: "sys:guardian", Operation: "activate", Tier: "always_hot", Ready: true, ReplicaCount: 1, Duration: -time.Millisecond})
}

func activationArtifact(t *testing.T, data ActivationRecordArtifactData) *Artifact {
	t.Helper()
	artifact, err := activationRecordArtifact(data)
	if err != nil {
		t.Fatalf("activationRecordArtifact: %v", err)
	}
	return artifact
}

func dagPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return dagArtifact(t, DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, PipelineID: "pipe", DAGHash: "hash", Nodes: []string{"a"}, NodeCount: 1, CycleFree: true, PlannedPods: 1, HealthSummary: "ok", Status: InfrastructureStatusOK})
}

func dagCycleFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return dagArtifact(t, DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, CycleFree: false, CyclePath: []string{"a", "b", "a"}, PlannedPods: 1, HealthSummary: "ok", Status: InfrastructureStatusFailed})
}

func dagMissingNodesFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return dagArtifact(t, DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, CycleFree: true, MissingRequiredNodes: []string{"build"}, PlannedPods: 1, HealthSummary: "ok", Status: InfrastructureStatusOK})
}

func dagScopeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return dagArtifact(t, DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, CycleFree: true, ScopeBoundary: map[string]string{"session_id": "s1"}, PlannedPods: 1, HealthSummary: "ok", Status: InfrastructureStatusOK})
}

func dagPodCountFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return dagArtifact(t, DAGOperationArtifactData{Operation: DAGProcessorToolAllocatePipeline, CycleFree: true, PlannedPods: -1, HealthSummary: "ok", Status: InfrastructureStatusOK})
}

func dagHealthFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return dagArtifact(t, DAGOperationArtifactData{Operation: DAGProcessorToolReportHealth, CycleFree: true, PlannedPods: 1, Status: InfrastructureStatusFailed, FailureReason: "unhealthy"})
}

func dagArtifact(t *testing.T, data DAGOperationArtifactData) *Artifact {
	t.Helper()
	artifact, err := dagOperationArtifact(data)
	if err != nil {
		t.Fatalf("dagOperationArtifact: %v", err)
	}
	return artifact
}

func vfsPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolAttach, Scope: "tool", MountRef: "tool:pipe", CapacityBudgetBytes: 10, CapacityUsedBytes: 5, BaseVersion: "base-1", Attached: true, ReadOnly: true, Status: InfrastructureStatusOK})
}

func vfsMergePassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolMerge, Scope: "global", MountRef: "global:pipe", BaseVersion: "base-1", COWLayerChain: []string{"base-1", "tool-1"}, ResultingVersion: "global-2", Attached: true, Status: InfrastructureStatusOK})
}

func vfsAttachmentFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolAttach, MountRef: "tool:pipe", BaseVersion: "base-1", Attached: false})
}

func vfsCapacityFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolAttach, MountRef: "tool:pipe", BaseVersion: "base-1", Attached: true, CapacityBudgetBytes: 1, CapacityUsedBytes: 2})
}

func vfsBaseVersionFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolAttach, MountRef: "tool:pipe", Attached: true})
}

func vfsScopeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolAttach, MountRef: "tool:pipe", BaseVersion: "base-1", ScopeBoundary: map[string]string{"session_id": "s1"}, Attached: true})
}

func vfsReadWriteFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolProvision, MountRef: "tool:pipe", BaseVersion: "base-1", Attached: true, ReadOnly: true})
}

func vfsMergeCycleFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolMerge, MountRef: "global:pipe", BaseVersion: "base-1", COWLayerChain: []string{"base-1", "base-1"}, ResultingVersion: "global-2", Attached: true})
}

func vfsConflictFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolMerge, MountRef: "global:pipe", BaseVersion: "base-1", ConflictSet: []string{"main.go"}, ResultingVersion: "global-2", Attached: true})
}

func vfsMergedVersionFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return vfsArtifact(t, VFSOperationArtifactData{Operation: VFSProvisionerToolMerge, MountRef: "global:pipe", BaseVersion: "base-1", COWLayerChain: []string{"base-1"}, Attached: true})
}

func vfsArtifact(t *testing.T, data VFSOperationArtifactData) *Artifact {
	t.Helper()
	artifact, err := vfsOperationArtifact(data)
	if err != nil {
		t.Fatalf("vfsOperationArtifact: %v", err)
	}
	return artifact
}

func toolPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed", ExecutionMode: "sandboxed", Duration: time.Millisecond, OutputSummary: "ok", ProducedArtifactRefs: []string{"artifact:1"}, Status: InfrastructureStatusOK})
}

func toolPolicyFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "denied", ExecutionMode: "sandboxed", OutputSummary: "denied", ProducedArtifactRefs: []string{"artifact:1"}, Status: InfrastructureStatusFailed, FailureReason: "policy denied"})
}

func toolModeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed", OutputSummary: "ok", ProducedArtifactRefs: []string{"artifact:1"}})
}

func toolScopeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed", ExecutionMode: "sandboxed", SandboxScope: map[string]string{"session_id": "s1"}, OutputSummary: "ok", ProducedArtifactRefs: []string{"artifact:1"}})
}

func toolDurationFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed", ExecutionMode: "sandboxed", Duration: -time.Millisecond, OutputSummary: "ok", ProducedArtifactRefs: []string{"artifact:1"}})
}

func toolOutputFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed", ExecutionMode: "sandboxed", ProducedArtifactRefs: []string{"artifact:1"}})
}

func toolArtifactsFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return toolArtifact(t, ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed", ExecutionMode: "sandboxed", OutputSummary: "ok"})
}

func toolArtifact(t *testing.T, data ToolRuntimeExecutionArtifactData) *Artifact {
	t.Helper()
	artifact, err := toolRuntimeExecutionArtifact(data)
	if err != nil {
		t.Fatalf("toolRuntimeExecutionArtifact: %v", err)
	}
	return artifact
}

func knowledgePassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolQueryHybrid, NodeCount: 1, EdgeCount: 1, EmbeddingDimension: 3, ExpectedEmbeddingDimension: 3, QueryShape: "hybrid", ResultCount: 1, ScoreMin: 0.1, ScoreMax: 0.9, Ready: true, Backend: "vectorgraphdb", FullTextBackend: "bleve", Status: InfrastructureStatusOK})
}

func knowledgeEmbeddingFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolUpdateEmbeddings, EmbeddingDimension: 2, ExpectedEmbeddingDimension: 3, QueryShape: "hybrid", ScoreMin: 0.1, ScoreMax: 0.9, Ready: true, FullTextBackend: "bleve"})
}

func knowledgeNodeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolQueryGraph, MissingNodes: []string{"node:missing"}, QueryShape: "graph", ScoreMin: 0.1, ScoreMax: 0.9, Ready: true, FullTextBackend: "bleve"})
}

func knowledgeEdgeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolIngestEdges, DanglingEdges: []string{"edge:missing"}, QueryShape: "graph", ScoreMin: 0.1, ScoreMax: 0.9, Ready: true, FullTextBackend: "bleve"})
}

func knowledgeQueryShapeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolQueryHybrid, ScoreMin: 0.1, ScoreMax: 0.9, Ready: true, FullTextBackend: "bleve"})
}

func knowledgeScoreFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolQuerySemantic, QueryShape: "semantic", ScoreMin: -0.1, ScoreMax: 1.1, Ready: true, FullTextBackend: "bleve"})
}

func knowledgeReadinessFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return knowledgeArtifact(t, KnowledgeOperationArtifactData{Operation: KnowledgeToolReadiness, QueryShape: "readiness", ScoreMin: 0, ScoreMax: 1, Ready: false, FullTextBackend: "bleve", FailureReason: "offline"})
}

func knowledgeArtifact(t *testing.T, data KnowledgeOperationArtifactData) *Artifact {
	t.Helper()
	artifact, err := knowledgeOperationArtifact(data)
	if err != nil {
		t.Fatalf("knowledgeOperationArtifact: %v", err)
	}
	return artifact
}

func memoryPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return memoryArtifact(t, MemoryContinuityArtifactData{Operation: MemoryToolCarryForwardAdvance, Topic: "claims", EvidenceDigest: "digest", FromSeq: 1, ThroughSeq: 2, SourceAvailable: true, Status: InfrastructureStatusOK})
}

func memorySourceFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return memoryArtifact(t, MemoryContinuityArtifactData{Operation: MemoryToolCarryForwardAdvance, Topic: "claims", EvidenceDigest: "digest", FromSeq: 1, ThroughSeq: 2, SourceAvailable: false, FailureReason: "source missing"})
}

func memoryCursorFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return memoryArtifact(t, MemoryContinuityArtifactData{Operation: MemoryToolCarryForwardAdvance, Topic: "claims", EvidenceDigest: "digest", FromSeq: 3, ThroughSeq: 2, SourceAvailable: true})
}

func memoryDigestFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return memoryArtifact(t, MemoryContinuityArtifactData{Operation: MemoryToolCarryForwardAdvance, Topic: "claims", FromSeq: 1, ThroughSeq: 2, SourceAvailable: true})
}

func memoryContradictionFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return memoryArtifact(t, MemoryContinuityArtifactData{Operation: MemoryToolCarryForwardAdvance, Topic: "claims", EvidenceDigest: "digest", FromSeq: 1, ThroughSeq: 2, SourceAvailable: true, Contradicted: true})
}

func memoryArtifact(t *testing.T, data MemoryContinuityArtifactData) *Artifact {
	t.Helper()
	artifacts, err := memoryContinuityArtifacts(data)
	if err != nil {
		t.Fatalf("memoryContinuityArtifacts: %v", err)
	}
	return artifacts[0]
}

func documentPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return documentArtifact(t, DocumentOperationArtifactData{Operation: DocumentToolSearchBleve, DocumentID: "doc", Indexed: true, ResultShape: "results", ResultCount: 1, AttachmentRefs: []string{"artifact:1"}, ChunkBoundaries: []DocumentChunkBoundaryArtifactData{{Index: 0, Start: 0, End: 4}}, FullTextBackend: "bleve", Status: InfrastructureStatusOK})
}

func documentIndexedFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return documentArtifact(t, DocumentOperationArtifactData{Operation: DocumentToolIndex, DocumentID: "doc", Indexed: false, FullTextBackend: "bleve"})
}

func documentAttachmentFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return documentArtifact(t, DocumentOperationArtifactData{Operation: DocumentToolIngest, DocumentID: "doc", Indexed: true, AttachmentRefs: []string{"artifact:1", "artifact:1"}, FullTextBackend: "bleve"})
}

func documentChunkFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return documentArtifact(t, DocumentOperationArtifactData{Operation: DocumentToolChunk, DocumentID: "doc", Indexed: true, ChunkBoundaries: []DocumentChunkBoundaryArtifactData{{Index: 0, Start: 4, End: 1}}, FullTextBackend: "bleve"})
}

func documentShapeFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return documentArtifact(t, DocumentOperationArtifactData{Operation: DocumentToolLookup, DocumentID: "doc", Indexed: true, FullTextBackend: "bleve"})
}

func documentBackendFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return documentArtifact(t, DocumentOperationArtifactData{Operation: DocumentToolSearchBleve, DocumentID: "doc", Indexed: true, ResultShape: "results", FullTextBackend: "sqlite_fts"})
}

func documentArtifact(t *testing.T, data DocumentOperationArtifactData) *Artifact {
	t.Helper()
	artifact, err := documentOperationArtifact(data)
	if err != nil {
		t.Fatalf("documentOperationArtifact: %v", err)
	}
	return artifact
}

func guardianPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return guardianArtifact(t, GuardianDecisionArtifactData{Operation: GuardianToolApprovalCheck, PolicyRule: "command.requires_approval", ApprovalSubject: "deploy", UserDecision: "allow", BranchState: "protected", Allowed: true, ApprovalPresent: true, BranchProtected: true, Status: InfrastructureStatusOK})
}

func guardianPolicyFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return guardianArtifact(t, GuardianDecisionArtifactData{Operation: GuardianToolPolicyCheck, Allowed: true, ApprovalPresent: true, BranchProtected: true})
}

func guardianApprovalFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return guardianArtifact(t, GuardianDecisionArtifactData{Operation: GuardianToolApprovalCheck, PolicyRule: "approval", UserDecision: "deny", Allowed: false, ApprovalPresent: false, BranchProtected: true, FailureReason: "denied"})
}

func guardianBranchFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return guardianArtifact(t, GuardianDecisionArtifactData{Operation: GuardianToolBranchProtection, PolicyRule: "branch", UserDecision: "allow", Allowed: false, ApprovalPresent: true, BranchProtected: false, FailureReason: "branch unprotected"})
}

func guardianDiffFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return guardianArtifact(t, GuardianDecisionArtifactData{Operation: GuardianToolDiffReview, PolicyRule: "diff", UserDecision: "allow", Allowed: true, ApprovalPresent: true, BranchProtected: true, DiffFindings: []string{"secret"}})
}

func guardianRollbackFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return guardianArtifact(t, GuardianDecisionArtifactData{Operation: GuardianToolRollbackAuthorization, PolicyRule: "rollback", UserDecision: "allow", Allowed: true, ApprovalPresent: true, BranchProtected: true})
}

func guardianArtifact(t *testing.T, data GuardianDecisionArtifactData) *Artifact {
	t.Helper()
	artifact, err := guardianDecisionArtifact(data)
	if err != nil {
		t.Fatalf("guardianDecisionArtifact: %v", err)
	}
	return artifact
}

func providerPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return providerArtifact(t, ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Model: "mock-model", Identity: "guide", BudgetTokens: 100, TotalTokens: 50, RateLimitRemaining: 1, ResponseSummary: "ok", ProviderRequestID: "req-1", Status: InfrastructureStatusOK})
}

func providerResponseFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return providerArtifact(t, ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Model: "mock-model", Identity: "guide", BudgetTokens: 100, TotalTokens: 50, RateLimitRemaining: 1, ProviderRequestID: "req-1"})
}

func providerUsageFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return providerArtifact(t, ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Model: "mock-model", Identity: "guide", BudgetTokens: 10, TotalTokens: 50, RateLimitRemaining: 1, ResponseSummary: "ok"})
}

func providerRateLimitFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return providerArtifact(t, ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Model: "mock-model", Identity: "guide", BudgetTokens: 100, TotalTokens: 50, RateLimitRemaining: -1, ResponseSummary: "ok"})
}

func providerModelFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return providerArtifact(t, ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Identity: "guide", BudgetTokens: 100, TotalTokens: 50, RateLimitRemaining: 1, ResponseSummary: "ok"})
}

func providerIdentityFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return providerArtifact(t, ProviderGatewayCallArtifactData{Operation: ProviderGatewayToolComplete, Model: "mock-model", BudgetTokens: 100, TotalTokens: 50, RateLimitRemaining: 1, ResponseSummary: "ok"})
}

func providerArtifact(t *testing.T, data ProviderGatewayCallArtifactData) *Artifact {
	t.Helper()
	artifact, err := providerGatewayCallArtifact(data)
	if err != nil {
		t.Fatalf("providerGatewayCallArtifact: %v", err)
	}
	return artifact
}

func externalPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return externalArtifact(t, ExternalAdapterEventArtifactData{Operation: ExternalAdapterToolCIResult, Source: ParticipantRef{Category: string(ParticipantCategoryExternal), UID: "external:ci"}, IdempotencyKey: "event-1", RawEventDigest: "digest", Schema: "ci.event.v1", Signature: "sig", EventTimestamp: time.Now().UTC(), Fresh: true, Valid: true, Status: InfrastructureStatusOK})
}

func externalSourceFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return externalArtifact(t, ExternalAdapterEventArtifactData{Operation: ExternalAdapterToolCIResult, Source: ParticipantRef{Category: string(ParticipantCategoryService), UID: "svc"}, IdempotencyKey: "event-1", Schema: "ci.event.v1", Signature: "sig", Fresh: true, Valid: true})
}

func externalSchemaFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return externalArtifact(t, ExternalAdapterEventArtifactData{Operation: ExternalAdapterToolCIResult, Source: ParticipantRef{Category: string(ParticipantCategoryExternal), UID: "external:ci"}, IdempotencyKey: "event-1", Signature: "sig", Fresh: true, Valid: true})
}

func externalFreshnessFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return externalArtifact(t, ExternalAdapterEventArtifactData{Operation: ExternalAdapterToolCIResult, Source: ParticipantRef{Category: string(ParticipantCategoryExternal), UID: "external:ci"}, IdempotencyKey: "event-1", Schema: "ci.event.v1", Signature: "sig", EventTimestamp: time.Now().Add(-48 * time.Hour), Fresh: false, Valid: true})
}

func externalClaimFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return externalArtifact(t, ExternalAdapterEventArtifactData{Operation: ExternalAdapterToolCIResult, Source: ParticipantRef{Category: string(ParticipantCategoryExternal), UID: "external:ci"}, IdempotencyKey: "event-1", Schema: "ci.event.v1", Signature: "sig", Fresh: true, ReferencedClaimID: "claim-missing", Valid: true})
}

func externalArtifact(t *testing.T, data ExternalAdapterEventArtifactData) *Artifact {
	t.Helper()
	artifact, err := externalAdapterEventArtifact(data)
	if err != nil {
		t.Fatalf("externalAdapterEventArtifact: %v", err)
	}
	return artifact
}

func sessionPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return sessionArtifact(t, SessionLifecycleArtifactData{Operation: "persist", SessionID: "session-1", Active: true, Persisted: true, Status: InfrastructureStatusOK})
}

func sessionStateFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return sessionArtifact(t, SessionLifecycleArtifactData{Operation: "persist", SessionID: "session-1", Active: false, Persisted: false, Status: InfrastructureStatusFailed, FailureReason: "closed"})
}

func sessionPersistenceFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return sessionArtifact(t, SessionLifecycleArtifactData{Operation: "persist", SessionID: "session-1", Active: true, Persisted: false, Status: InfrastructureStatusFailed, FailureReason: "disk full"})
}

func sessionArtifact(t *testing.T, data SessionLifecycleArtifactData) *Artifact {
	t.Helper()
	artifact, err := sessionLifecycleArtifact(data, "session_lifecycle", ArtifactKindSessionState)
	if err != nil {
		t.Fatalf("sessionLifecycleArtifact: %v", err)
	}
	return artifact
}

func fabricPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return fabricArtifact(t, FabricSubscriptionArtifactData{Operation: FabricSubscriberToolQueryLens, SessionID: "session-1", AgentID: "guide", Topic: "claims.system", LensQueryShape: "claim_id,status", Attached: true, Status: InfrastructureStatusOK})
}

func fabricLensFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return fabricArtifact(t, FabricSubscriptionArtifactData{Operation: FabricSubscriberToolQueryLens, SessionID: "session-1", Topic: "claims.system", Attached: true, Status: InfrastructureStatusOK})
}

func fabricAttachedFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return fabricArtifact(t, FabricSubscriptionArtifactData{Operation: FabricSubscriberToolAttach, SessionID: "session-1", Topic: "claims.system", Attached: false, Status: InfrastructureStatusFailed, FailureReason: "attach failed"})
}

func fabricArtifact(t *testing.T, data FabricSubscriptionArtifactData) *Artifact {
	t.Helper()
	artifact, err := fabricSubscriptionArtifact(data, "fabric_subscription", ArtifactKindSubscriptionState)
	if err != nil {
		t.Fatalf("fabricSubscriptionArtifact: %v", err)
	}
	return artifact
}

func busPassArtifact(t *testing.T) *Artifact {
	t.Helper()
	return busArtifact(t, BusTransportArtifactData{Operation: BusAdministratorToolRegisterTopic, ProcessUID: "proc", Topic: "claims.system", Capacity: 8, QueueDepth: 4, Status: InfrastructureStatusOK})
}

func busTopicFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return busArtifact(t, BusTransportArtifactData{Operation: BusAdministratorToolRegisterTopic, ProcessUID: "proc", Topic: "bad topic", Capacity: 8, QueueDepth: 4, Status: InfrastructureStatusOK})
}

func busCapacityFailArtifact(t *testing.T) *Artifact {
	t.Helper()
	return busArtifact(t, BusTransportArtifactData{Operation: BusAdministratorToolAdjustCapacity, ProcessUID: "proc", Topic: "claims.system", Capacity: 4, QueueDepth: 8, Status: InfrastructureStatusOK})
}

func busArtifact(t *testing.T, data BusTransportArtifactData) *Artifact {
	t.Helper()
	artifact, err := busTransportArtifact(data, "bus_transport", ArtifactKindCapacityRecord)
	if err != nil {
		t.Fatalf("busTransportArtifact: %v", err)
	}
	return artifact
}

func mustTypedArtifact[T any](t *testing.T, name, kind, reference string, data T) *Artifact {
	t.Helper()
	artifact := &Artifact{ArtifactName: name, Kind: kind, Reference: reference}
	if err := SetArtifactData(artifact, data); err != nil {
		t.Fatalf("SetArtifactData %s: %v", name, err)
	}
	return artifact
}
