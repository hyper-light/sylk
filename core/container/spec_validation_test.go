package container

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testProbeHandler is a stub ProbeHandler for testing.
type testProbeHandler struct{}

func (h *testProbeHandler) Execute(_ context.Context) (ProbeResult, error) {
	return ProbeResult{Success: true}, nil
}

func validContainerSpec() ContainerSpec {
	return ContainerSpec{
		Name:      "test-container",
		AgentType: "engineer",
		Role:      RoleMain,
	}
}

func TestValidateContainerSpec_Valid(t *testing.T) {
	spec := validContainerSpec()
	if err := ValidateContainerSpec(&spec); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateContainerSpec_EmptyName(t *testing.T) {
	spec := validContainerSpec()
	spec.Name = ""
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecNameEmpty) {
		t.Fatalf("expected ErrSpecNameEmpty, got %v", err)
	}
}

func TestValidateContainerSpec_EmptyAgentType(t *testing.T) {
	spec := validContainerSpec()
	spec.AgentType = ""
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecAgentTypeEmpty) {
		t.Fatalf("expected ErrSpecAgentTypeEmpty, got %v", err)
	}
}

func TestValidateContainerSpec_NegativeResources(t *testing.T) {
	spec := validContainerSpec()
	spec.Resources.GoroutineLimit = -1
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecNegativeResource) {
		t.Fatalf("expected ErrSpecNegativeResource, got %v", err)
	}
}

func TestValidateContainerSpec_DuplicateMountPaths(t *testing.T) {
	spec := validContainerSpec()
	spec.Mounts = []MountSpec{
		{Name: "vol1", MountPath: "/data"},
		{Name: "vol2", MountPath: "/data"},
	}
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecMountConflict) {
		t.Fatalf("expected ErrSpecMountConflict, got %v", err)
	}
}

func TestValidateContainerSpec_ProbeThresholdZero(t *testing.T) {
	spec := validContainerSpec()
	spec.Probes = []ProbeSpec{{
		Type:             ProbeLiveness,
		Handler:          &testProbeHandler{},
		SuccessThreshold: 0, // invalid
		FailureThreshold: 3,
	}}
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecProbeInvalid) {
		t.Fatalf("expected ErrSpecProbeInvalid, got %v", err)
	}
}

func TestValidateContainerSpec_ProbeNoHandler(t *testing.T) {
	spec := validContainerSpec()
	spec.Probes = []ProbeSpec{{
		Type:             ProbeLiveness,
		Handler:          nil,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}}
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecProbeInvalid) {
		t.Fatalf("expected ErrSpecProbeInvalid, got %v", err)
	}
}

func TestValidateContainerSpec_NegativeGracefulStop(t *testing.T) {
	spec := validContainerSpec()
	spec.GracefulStop = -1 * time.Second
	err := ValidateContainerSpec(&spec)
	if !errors.Is(err, ErrSpecGracefulStopNeg) {
		t.Fatalf("expected ErrSpecGracefulStopNeg, got %v", err)
	}
}

func TestDefaultContainerSpec_SetsGracefulStop(t *testing.T) {
	spec := validContainerSpec()
	DefaultContainerSpec(&spec)
	if spec.GracefulStop != defaultGracefulStopDuration {
		t.Fatalf("expected %v, got %v", defaultGracefulStopDuration, spec.GracefulStop)
	}
}

func TestDefaultContainerSpec_InitializesMaps(t *testing.T) {
	spec := validContainerSpec()
	DefaultContainerSpec(&spec)
	if spec.Labels == nil {
		t.Fatal("expected labels to be initialized")
	}
	if spec.Env == nil {
		t.Fatal("expected env to be initialized")
	}
}

