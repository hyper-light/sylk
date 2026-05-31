package boot

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

type bootValidatorFixture struct {
	pass func(*testing.T) *claims.Artifact
	fail func(*testing.T) *claims.Artifact
}

func TestBootSequencerInfrastructureServiceContract(t *testing.T) {
	registry := claims.NewValidatorRegistry()
	if err := RegisterDefaultBootValidators(registry); err != nil {
		t.Fatalf("RegisterDefaultBootValidators: %v", err)
	}
	contract := claims.BootSequencerInfrastructureServiceContract()
	contract.HandlerFactory = func() claims.ServiceHandler { return NewBootSequencerService(BootSequencerServiceConfig{}) }
	if err := claims.ValidateInfrastructureServiceContracts([]claims.InfrastructureServiceContract{contract}, registry.List()); err != nil {
		t.Fatalf("ValidateInfrastructureServiceContracts: %v", err)
	}
}

func TestBootSequencerServiceDoesNotExposeLegacyMutationBypassAPI(t *testing.T) {
	if _, ok := reflect.TypeOf(&BootSequencerService{}).MethodByName("RecordPhase"); ok {
		t.Fatal("BootSequencerService exposes legacy RecordPhase bypass")
	}
}

func TestDefaultBootValidatorsCoverPassFailIncompleteErrored(t *testing.T) {
	fixtures := bootValidatorFixtures()
	for _, reg := range defaultBootValidatorRegistrations() {
		fixture, ok := fixtures[reg.ValidatorID]
		if !ok {
			t.Fatalf("missing boot validator fixture for %s", reg.ValidatorID)
		}
		t.Run(reg.ValidatorID, func(t *testing.T) {
			assertBootValidatorCoverage(t, reg, fixture)
		})
	}
}

func assertBootValidatorCoverage(t *testing.T, reg claims.ValidatorRegistration, fixture bootValidatorFixture) {
	t.Helper()
	assertBootValidatorPass(t, reg, fixture.pass(t))
	assertBootValidatorFail(t, reg, fixture.fail(t))
	assertBootValidatorErrored(t, reg, incompleteBootArtifact(reg), "incomplete")
	assertBootValidatorErrored(t, reg, malformedBootArtifact(reg), "malformed")
}

func assertBootValidatorPass(t *testing.T, reg claims.ValidatorRegistration, artifact *claims.Artifact) {
	t.Helper()
	result, err := reg.Handler.ValidateArtifact(context.Background(), claims.ValidatorHandlerRequest{Artifact: artifact})
	if err != nil || result.Error != nil {
		t.Fatalf("%s pass result = %+v err=%v, want pass", reg.ValidatorID, result, err)
	}
}

func assertBootValidatorFail(t *testing.T, reg claims.ValidatorRegistration, artifact *claims.Artifact) {
	t.Helper()
	result, err := reg.Handler.ValidateArtifact(context.Background(), claims.ValidatorHandlerRequest{Artifact: artifact})
	if err != nil || result.Error == nil {
		t.Fatalf("%s fail result = %+v err=%v, want validation failure", reg.ValidatorID, result, err)
	}
}

func assertBootValidatorErrored(t *testing.T, reg claims.ValidatorRegistration, artifact *claims.Artifact, name string) {
	t.Helper()
	if _, err := reg.Handler.ValidateArtifact(context.Background(), claims.ValidatorHandlerRequest{Artifact: artifact}); err == nil {
		t.Fatalf("%s %s artifact validated without error", reg.ValidatorID, name)
	}
}

func bootValidatorFixtures() map[string]bootValidatorFixture {
	return map[string]bootValidatorFixture{
		"infrastructure.boot.setup":       {pass: bootSetupPassArtifact, fail: bootSetupFailArtifact},
		"infrastructure.boot.detect":      {pass: bootDetectPassArtifact, fail: bootDetectFailArtifact},
		"infrastructure.boot.allocate":    {pass: bootAllocatePassArtifact, fail: bootAllocateFailArtifact},
		"infrastructure.boot.ingest":      {pass: bootIngestPassArtifact, fail: bootIngestFailArtifact},
		"infrastructure.boot.commit":      {pass: bootCommitPassArtifact, fail: bootCommitFailArtifact},
		"infrastructure.boot.finalize":    {pass: bootFinalizePassArtifact, fail: bootFinalizeFailArtifact},
		"infrastructure.boot.phase_order": {pass: bootCommitPassArtifact, fail: bootPhaseOrderFailArtifact},
		"infrastructure.boot.duration":    {pass: bootCommitPassArtifact, fail: bootDurationFailArtifact},
	}
}

func bootSetupPassArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "setup", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootSetupFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "detect", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootDetectPassArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "detect", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootDetectFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "setup", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootAllocatePassArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "allocate", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootAllocateFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "allocate", false, claims.InfrastructureStatusFailed, "identity unavailable", time.Millisecond)
}

func bootIngestPassArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "ingest", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootIngestFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "ingest", false, claims.InfrastructureStatusFailed, "subscriber unavailable", time.Millisecond)
}

func bootCommitPassArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "commit", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootCommitFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "commit", false, claims.InfrastructureStatusFailed, "wal unavailable", time.Millisecond)
}

func bootFinalizePassArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "finalize", true, claims.InfrastructureStatusOK, "", time.Millisecond)
}

func bootFinalizeFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "finalize", false, claims.InfrastructureStatusFailed, "shutdown scope failed", time.Millisecond)
}

func bootPhaseOrderFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	data := bootPhaseData("commit", true, claims.InfrastructureStatusOK, "", time.Millisecond)
	data.PhaseOrder++
	return mustBootPhaseArtifact(t, data)
}

func bootDurationFailArtifact(t *testing.T) *claims.Artifact {
	t.Helper()
	return bootPhaseArtifact(t, "commit", true, claims.InfrastructureStatusOK, "", -time.Millisecond)
}

func bootPhaseArtifact(t *testing.T, phase string, ready bool, status, reason string, duration time.Duration) *claims.Artifact {
	t.Helper()
	return mustBootPhaseArtifact(t, bootPhaseData(phase, ready, status, reason, duration))
}

func bootPhaseData(phase string, ready bool, status, reason string, duration time.Duration) claims.BootPhaseArtifactData {
	order := bootPhaseOrder(phase)
	return claims.BootPhaseArtifactData{
		Phase:         phase,
		PhaseOrder:    order,
		ProcessUID:    "proc-phase-18",
		Duration:      duration,
		Ready:         ready,
		PriorPhases:   priorBootPhases(order),
		Status:        status,
		FailureReason: reason,
	}
}

func priorBootPhases(order int) []string {
	sequence := bootPhaseSequence()
	if order <= 1 || order > len(sequence) {
		return nil
	}
	return append([]string(nil), sequence[:order-1]...)
}

func mustBootPhaseArtifact(t *testing.T, data claims.BootPhaseArtifactData) *claims.Artifact {
	t.Helper()
	artifact, err := claims.NewBootPhaseArtifact(data)
	if err != nil {
		t.Fatalf("NewBootPhaseArtifact: %v", err)
	}
	return artifact
}

func incompleteBootArtifact(reg claims.ValidatorRegistration) *claims.Artifact {
	return &claims.Artifact{ArtifactName: reg.TargetArtifactName, DataType: reg.ArtifactDataType}
}

func malformedBootArtifact(reg claims.ValidatorRegistration) *claims.Artifact {
	data := []byte("{")
	return &claims.Artifact{ArtifactName: reg.TargetArtifactName, DataType: reg.ArtifactDataType, Data: data, Size: int64(len(data)), ContentHash: claims.ArtifactContentHash(data)}
}
