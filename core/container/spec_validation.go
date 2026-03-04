package container

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSpecNameEmpty           = errors.New("spec name must not be empty")
	ErrSpecAgentTypeEmpty      = errors.New("spec agent type must not be empty")
	ErrSpecNegativeResource    = errors.New("resource value must not be negative")
	ErrSpecMountConflict       = errors.New("duplicate mount path detected")
	ErrSpecMountNameEmpty      = errors.New("mount name must not be empty")
	ErrSpecMountPathEmpty      = errors.New("mount path must not be empty")
	ErrSpecVolumeNameEmpty     = errors.New("volume name must not be empty")
	ErrSpecVolumeNameDuplicate = errors.New("duplicate volume name detected")
	ErrSpecProbeInvalid        = errors.New("probe has invalid configuration")
	ErrSpecGracefulStopNeg     = errors.New("graceful stop duration must not be negative")
)

// ValidateContainerSpec validates a container specification.
func ValidateContainerSpec(spec *ContainerSpec) error {
	if err := validateContainerIdentity(spec); err != nil {
		return err
	}
	if err := validateResources(&spec.Resources); err != nil {
		return err
	}
	if err := validateMountsUnique(spec.Mounts); err != nil {
		return err
	}
	if err := validateProbes(spec.Probes); err != nil {
		return err
	}
	return validateGracefulStop(spec.GracefulStop)
}

func validateContainerIdentity(spec *ContainerSpec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return ErrSpecNameEmpty
	}
	if strings.TrimSpace(spec.AgentType) == "" {
		return ErrSpecAgentTypeEmpty
	}
	return nil
}

func validateResources(r *ResourceSpec) error {
	if r.GoroutineRequest < 0 {
		return fmt.Errorf("goroutine request: %w", ErrSpecNegativeResource)
	}
	if r.GoroutineLimit < 0 {
		return fmt.Errorf("goroutine limit: %w", ErrSpecNegativeResource)
	}
	if r.ContextWindowRequest < 0 {
		return fmt.Errorf("context window request: %w", ErrSpecNegativeResource)
	}
	if r.ContextWindowLimit < 0 {
		return fmt.Errorf("context window limit: %w", ErrSpecNegativeResource)
	}
	if r.VFSQuotaBytes < 0 {
		return fmt.Errorf("vfs quota: %w", ErrSpecNegativeResource)
	}
	if r.CacheMaxCost < 0 {
		return fmt.Errorf("cache max cost: %w", ErrSpecNegativeResource)
	}
	return nil
}

func validateMountsUnique(mounts []MountSpec) error {
	paths := make(map[string]struct{}, len(mounts))
	for _, m := range mounts {
		if strings.TrimSpace(m.Name) == "" {
			return ErrSpecMountNameEmpty
		}
		if strings.TrimSpace(m.MountPath) == "" {
			return ErrSpecMountPathEmpty
		}
		if _, exists := paths[m.MountPath]; exists {
			return fmt.Errorf("%w: %s", ErrSpecMountConflict, m.MountPath)
		}
		paths[m.MountPath] = struct{}{}
	}
	return nil
}

func validateProbes(probes []ProbeSpec) error {
	for i := range probes {
		if err := validateSingleProbe(&probes[i]); err != nil {
			return err
		}
	}
	return nil
}

func validateSingleProbe(p *ProbeSpec) error {
	if p.SuccessThreshold < 1 || p.FailureThreshold < 1 {
		return fmt.Errorf("%w: thresholds must be >= 1 for %s probe", ErrSpecProbeInvalid, p.Type)
	}
	if p.Handler == nil {
		return fmt.Errorf("%w: handler required for %s probe", ErrSpecProbeInvalid, p.Type)
	}
	return nil
}

func validateGracefulStop(d time.Duration) error {
	if d < 0 {
		return ErrSpecGracefulStopNeg
	}
	return nil
}

// DefaultContainerSpec applies sensible defaults to a container spec.
func DefaultContainerSpec(spec *ContainerSpec) {
	if spec.GracefulStop == 0 {
		spec.GracefulStop = defaultGracefulStopDuration
	}
	if spec.Labels == nil {
		spec.Labels = make(map[string]string)
	}
	if spec.Env == nil {
		spec.Env = make(map[string]string)
	}
	defaultProbes(spec)
}

const defaultGracefulStopDuration = 30 * time.Second

func defaultProbes(spec *ContainerSpec) {
	for i := range spec.Probes {
		p := &spec.Probes[i]
		if p.SuccessThreshold == 0 {
			p.SuccessThreshold = 1
		}
		if p.FailureThreshold == 0 {
			p.FailureThreshold = defaultProbeFailureThreshold
		}
		if p.Period == 0 {
			p.Period = defaultProbePeriod
		}
		if p.Timeout == 0 {
			p.Timeout = defaultProbeTimeout
		}
	}
}

const (
	defaultProbeFailureThreshold = 3
	defaultProbePeriod           = 10 * time.Second
	defaultProbeTimeout          = 5 * time.Second
)

