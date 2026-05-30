package claims

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdentityRegistryServiceAllocateLookupLineageAndGeneration(t *testing.T) {
	service := NewIdentityRegistryService(IdentityRegistryServiceConfig{})
	root, err := service.allocate(context.Background(), IdentityAllocationArtifactData{
		Category:   ParticipantCategoryService,
		RouteKey:   "identity_registry",
		Scope:      map[string]string{"session": "sess"},
		Generation: 1,
	})
	if err != nil {
		t.Fatalf("Allocate root: %v", err)
	}
	again, err := service.allocate(context.Background(), IdentityAllocationArtifactData{
		Category:   ParticipantCategoryService,
		RouteKey:   "identity_registry",
		Scope:      map[string]string{"session": "sess"},
		Generation: 1,
	})
	if err != nil {
		t.Fatalf("Allocate duplicate: %v", err)
	}
	if again.UID != root.UID {
		t.Fatalf("duplicate uid = %s, want %s", again.UID, root.UID)
	}
	child, err := service.allocate(context.Background(), IdentityAllocationArtifactData{
		Category:   ParticipantCategoryService,
		RouteKey:   "activation_controller",
		Scope:      map[string]string{"session": "sess"},
		Generation: 2,
		ParentUID:  root.UID,
	})
	if err != nil {
		t.Fatalf("Allocate child: %v", err)
	}
	if _, err := service.allocate(context.Background(), IdentityAllocationArtifactData{
		Category:   ParticipantCategoryService,
		RouteKey:   "activation_controller",
		Scope:      map[string]string{"session": "sess"},
		Generation: 1,
		ParentUID:  root.UID,
	}); !errors.Is(err, ErrIdentityGenerationRegression) {
		t.Fatalf("generation regression error = %v, want ErrIdentityGenerationRegression", err)
	}
	if got, ok := service.Lookup(context.Background(), root.UID); !ok || got.UID != root.UID {
		t.Fatalf("Lookup = %+v ok=%v, want root", got, ok)
	}
	lineage, err := service.lineage(context.Background(), child.UID)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if !lineage.Terminal || len(lineage.Lineage) != 2 || lineage.Lineage[0] != child.UID || lineage.Lineage[1] != root.UID {
		t.Fatalf("lineage = %+v, want child -> root terminal lineage", lineage)
	}
}

func TestIdentityRegistryServiceHandlerProducesTypedAllocationArtifact(t *testing.T) {
	service := NewIdentityRegistryService(IdentityRegistryServiceConfig{})
	claim := &Claim{ExpectedToolCalls: []ExpectedToolCall{{
		Tool: IdentityRegistryToolAllocate,
		Arguments: map[string]any{
			"category":   string(ParticipantCategoryService),
			"route_key":  "provider_gateway",
			"scope":      map[string]any{"session": "sess"},
			"generation": 1,
		},
	}}}
	result, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: claim})
	if err != nil {
		t.Fatalf("HandleServiceClaim: %v", err)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(result.Artifacts))
	}
	data, err := ArtifactData[IdentityAllocationArtifactData](result.Artifacts[0])
	if err != nil {
		t.Fatalf("ArtifactData: %v", err)
	}
	if data.RouteKey != "provider_gateway" || data.UID == "" {
		t.Fatalf("allocation data = %+v, want provider gateway uid", data)
	}
}

func TestIdentityValidatorsPassAndFailDeterministically(t *testing.T) {
	service := NewIdentityRegistryService(IdentityRegistryServiceConfig{})
	allocation, err := service.allocate(context.Background(), IdentityAllocationArtifactData{Category: ParticipantCategoryService, RouteKey: "guardian", Scope: map[string]string{"session": "sess"}, Generation: 1})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	artifact, err := identityAllocationArtifact(allocation)
	if err != nil {
		t.Fatalf("identityAllocationArtifact: %v", err)
	}
	if result, err := (IdentityDeterministicValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: artifact}); err != nil || result.Error != nil {
		t.Fatalf("deterministic pass result=%+v err=%v", result, err)
	}
	bad := allocation
	bad.UID = "bad-uid"
	badArtifact := &Artifact{ArtifactName: "identity_allocation", Kind: ArtifactKindAgentID, Reference: bad.UID}
	if err := SetArtifactData(badArtifact, bad); err != nil {
		t.Fatalf("SetArtifactData bad: %v", err)
	}
	if result, err := (IdentityDeterministicValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: badArtifact}); err != nil || result.Error == nil {
		t.Fatalf("deterministic failure result=%+v err=%v, want validation error", result, err)
	}
	service.byUID[allocation.UID] = IdentityAllocationArtifactData{UID: allocation.UID, Category: ParticipantCategoryService, RouteKey: "different"}
	if result, err := (IdentityUniqueValidator{Registry: service}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: artifact}); err != nil || result.Error == nil {
		t.Fatalf("unique failure result=%+v err=%v, want validation error", result, err)
	}
	readyArtifact, err := activationRecordArtifact(ActivationRecordArtifactData{ParticipantID: "guardian", Ready: true, ReplicaCount: 1, Duration: time.Millisecond})
	if err != nil {
		t.Fatalf("activationRecordArtifact ready: %v", err)
	}
	if result, err := (ActivationReadinessValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: readyArtifact}); err != nil || result.Error != nil {
		t.Fatalf("activation pass result=%+v err=%v", result, err)
	}
	failedArtifact, err := activationRecordArtifact(ActivationRecordArtifactData{ParticipantID: "guardian", Ready: false, FailureReason: "replicas unavailable"})
	if err != nil {
		t.Fatalf("activationRecordArtifact failed: %v", err)
	}
	if result, err := (ActivationReadinessValidator{}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: failedArtifact}); err != nil || result.Error == nil {
		t.Fatalf("activation failure result=%+v err=%v, want validation error", result, err)
	}
	tierValidator := TierAchievedValidator{ExpectedTier: "always_hot"}
	tierArtifact, err := activationRecordArtifact(ActivationRecordArtifactData{ParticipantID: "guardian", Tier: "always_hot", Ready: true, ReplicaCount: 2, Duration: time.Millisecond})
	if err != nil {
		t.Fatalf("activationRecordArtifact tier: %v", err)
	}
	if result, err := tierValidator.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: tierArtifact}); err != nil || result.Error != nil {
		t.Fatalf("tier pass result=%+v err=%v", result, err)
	}
	wrongTierArtifact, err := activationRecordArtifact(ActivationRecordArtifactData{ParticipantID: "guardian", Tier: "warm", Ready: true, ReplicaCount: 2, Duration: time.Millisecond})
	if err != nil {
		t.Fatalf("activationRecordArtifact wrong tier: %v", err)
	}
	if result, err := tierValidator.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: wrongTierArtifact}); err != nil || result.Error == nil {
		t.Fatalf("tier failure result=%+v err=%v, want validation error", result, err)
	}
	replicaValidator := ReplicaCountValidator{ExpectedReplicaCount: 2}
	if result, err := replicaValidator.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: tierArtifact}); err != nil || result.Error != nil {
		t.Fatalf("replica pass result=%+v err=%v", result, err)
	}
	if result, err := (ReplicaCountValidator{ExpectedReplicaCount: 3}).ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: tierArtifact}); err != nil || result.Error == nil {
		t.Fatalf("replica failure result=%+v err=%v, want validation error", result, err)
	}
	durationValidator := ActivationDurationValidator{MaxDuration: 2 * time.Millisecond}
	if result, err := durationValidator.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: tierArtifact}); err != nil || result.Error != nil {
		t.Fatalf("duration pass result=%+v err=%v", result, err)
	}
	slowArtifact, err := activationRecordArtifact(ActivationRecordArtifactData{ParticipantID: "guardian", Tier: "always_hot", Ready: true, ReplicaCount: 2, Duration: 3 * time.Millisecond})
	if err != nil {
		t.Fatalf("activationRecordArtifact slow: %v", err)
	}
	if result, err := durationValidator.ValidateArtifact(context.Background(), ValidatorHandlerRequest{Artifact: slowArtifact}); err != nil || result.Error == nil {
		t.Fatalf("duration failure result=%+v err=%v, want validation error", result, err)
	}
}
