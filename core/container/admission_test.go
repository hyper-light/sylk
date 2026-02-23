package container

import (
	"errors"
	"testing"
)

func TestAdmissionController_ValidSpecPasses(t *testing.T) {
	ac := NewAdmissionController(nil, nil)

	spec := validContainerSpec()
	if err := ac.Admit(&spec); err != nil {
		t.Fatalf("valid spec should pass: %v", err)
	}
}

func TestAdmissionController_ValidatorRejects(t *testing.T) {
	validator := &ResourceLimitValidator{
		MaxGoroutineLimit: 10,
	}
	ac := NewAdmissionController(
		[]SpecValidator{validator},
		nil,
	)

	spec := validContainerSpec()
	spec.Resources.GoroutineLimit = 20

	err := ac.Admit(&spec)
	if !errors.Is(err, ErrAdmissionDenied) {
		t.Fatalf("expected ErrAdmissionDenied, got %v", err)
	}
}

func TestAdmissionController_MutatorAppliesDefaults(t *testing.T) {
	mutator := &DefaultsMutator{}
	ac := NewAdmissionController(nil, []SpecMutator{mutator})

	spec := ContainerSpec{
		Name:      "test",
		AgentType: "engineer",
	}
	if err := ac.Admit(&spec); err != nil {
		t.Fatalf("Admit failed: %v", err)
	}

	if spec.GracefulStop == 0 {
		t.Fatal("defaults mutator should set graceful stop")
	}
}

func TestAdmissionController_ValidatorBeforeMutator(t *testing.T) {
	// A validator that rejects everything
	rejectAll := &rejectingValidator{}
	mutator := &trackingMutator{}
	ac := NewAdmissionController(
		[]SpecValidator{rejectAll},
		[]SpecMutator{mutator},
	)

	spec := validContainerSpec()
	_ = ac.Admit(&spec)

	if mutator.called {
		t.Fatal("mutator should not run if validator rejects")
	}
}

func TestResourceLimitValidator_WithinLimits(t *testing.T) {
	v := &ResourceLimitValidator{
		MaxGoroutineLimit: 100,
		MaxContextWindow:  50000,
		MaxVFSQuotaBytes:  1024,
	}

	spec := &ContainerSpec{
		Resources: ResourceSpec{
			GoroutineLimit:     50,
			ContextWindowLimit: 10000,
			VFSQuotaBytes:      512,
		},
	}
	if err := v.Validate(spec); err != nil {
		t.Fatalf("should pass within limits: %v", err)
	}
}

func TestResourceLimitValidator_ExceedsContextWindow(t *testing.T) {
	v := &ResourceLimitValidator{
		MaxContextWindow: 10000,
	}

	spec := &ContainerSpec{
		Resources: ResourceSpec{
			ContextWindowLimit: 20000,
		},
	}
	if err := v.Validate(spec); err == nil {
		t.Fatal("should reject context window over max")
	}
}

func TestResourceLimitValidator_ExceedsVFS(t *testing.T) {
	v := &ResourceLimitValidator{
		MaxVFSQuotaBytes: 500,
	}

	spec := &ContainerSpec{
		Resources: ResourceSpec{
			VFSQuotaBytes: 1000,
		},
	}
	if err := v.Validate(spec); err == nil {
		t.Fatal("should reject VFS over max")
	}
}

// rejectingValidator always returns an error.
type rejectingValidator struct{}

func (r *rejectingValidator) Validate(_ *ContainerSpec) error {
	return errors.New("rejected")
}

// trackingMutator records whether it was called.
type trackingMutator struct {
	called bool
}

func (m *trackingMutator) Mutate(_ *ContainerSpec) error {
	m.called = true
	return nil
}
