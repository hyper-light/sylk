package container

import (
	"errors"
	"fmt"
)

var (
	ErrAdmissionDenied = errors.New("admission denied")
)

// SpecValidator validates a container spec before the runtime accepts it.
type SpecValidator interface {
	Validate(spec *ContainerSpec) error
}

// SpecMutator applies defaults or transformations to a container spec.
type SpecMutator interface {
	Mutate(spec *ContainerSpec) error
}

// AdmissionController validates and mutates container specs before
// the runtime creates containers. Validators run first; if all pass,
// mutators run in order.
type AdmissionController struct {
	validators []SpecValidator
	mutators   []SpecMutator
}

// NewAdmissionController creates an admission controller with the
// given validator and mutator chains.
func NewAdmissionController(validators []SpecValidator, mutators []SpecMutator) *AdmissionController {
	v := make([]SpecValidator, len(validators))
	copy(v, validators)
	m := make([]SpecMutator, len(mutators))
	copy(m, mutators)
	return &AdmissionController{
		validators: v,
		mutators:   m,
	}
}

// Admit runs validators then mutators on the spec. Returns the first
// validation error encountered, or the first mutation error.
func (ac *AdmissionController) Admit(spec *ContainerSpec) error {
	if err := ac.validate(spec); err != nil {
		return err
	}
	return ac.mutate(spec)
}

func (ac *AdmissionController) validate(spec *ContainerSpec) error {
	for _, v := range ac.validators {
		if err := v.Validate(spec); err != nil {
			return fmt.Errorf("%w: %w", ErrAdmissionDenied, err)
		}
	}
	return nil
}

func (ac *AdmissionController) mutate(spec *ContainerSpec) error {
	for _, m := range ac.mutators {
		if err := m.Mutate(spec); err != nil {
			return fmt.Errorf("admission mutator: %w", err)
		}
	}
	return nil
}

// DefaultsMutator applies default values to container specs.
type DefaultsMutator struct{}

// Mutate applies defaults to the spec using DefaultContainerSpec.
func (d *DefaultsMutator) Mutate(spec *ContainerSpec) error {
	DefaultContainerSpec(spec)
	return nil
}

// ResourceLimitValidator ensures resource specs are within acceptable bounds.
type ResourceLimitValidator struct {
	MaxGoroutineLimit int64
	MaxContextWindow  int
	MaxVFSQuotaBytes  int64
}

// Validate checks that resource limits don't exceed system bounds.
func (v *ResourceLimitValidator) Validate(spec *ContainerSpec) error {
	r := &spec.Resources
	if v.MaxGoroutineLimit > 0 && r.GoroutineLimit > v.MaxGoroutineLimit {
		return fmt.Errorf("goroutine limit %d exceeds max %d", r.GoroutineLimit, v.MaxGoroutineLimit)
	}
	if v.MaxContextWindow > 0 && r.ContextWindowLimit > v.MaxContextWindow {
		return fmt.Errorf("context window %d exceeds max %d", r.ContextWindowLimit, v.MaxContextWindow)
	}
	if v.MaxVFSQuotaBytes > 0 && r.VFSQuotaBytes > v.MaxVFSQuotaBytes {
		return fmt.Errorf("vfs quota %d exceeds max %d", r.VFSQuotaBytes, v.MaxVFSQuotaBytes)
	}
	return nil
}
