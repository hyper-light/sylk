package pod

import "errors"

var (
	ErrPodReleased  = errors.New("pod has been released")
	ErrPodNotHot    = errors.New("pod is not at TierHot")
	ErrUnknownAgent = errors.New("unknown agent type in pod")
)

// PodID uniquely identifies a managed pod.
type PodID = string

// PodType classifies a pod's membership and activation semantics.
type PodType int

const (
	// PodTypePipeline groups multiple pipeline agents (engineer, designer,
	// inspector-pipeline, tester-pipeline) that activate/deactivate atomically.
	PodTypePipeline PodType = iota

	// PodTypeSingleton wraps a single agent (architect, inspector-global,
	// tester-global) with its own tier lifecycle.
	PodTypeSingleton

	// PodTypeDaemon marks always-hot pods (guide, orchestrator, guardian)
	// that are never demoted below TierHot.
	PodTypeDaemon
)

var podTypeNames = map[PodType]string{
	PodTypePipeline:  "pipeline",
	PodTypeSingleton: "singleton",
	PodTypeDaemon:    "daemon",
}

// String returns the string representation of a PodType.
func (pt PodType) String() string {
	if name, ok := podTypeNames[pt]; ok {
		return name
	}
	return "unknown"
}

// IsDaemon returns true if the pod type is always-hot.
func (pt PodType) IsDaemon() bool {
	return pt == PodTypeDaemon
}

// ActivationTier represents the resource/latency tradeoff for a pod.
//
//	Cold: No state exists, full creation needed.
//	Cool: State serialized on disk, moderate resume.
//	Warm: Container paused in memory, fast resume.
//	Hot:  Container running and ready.
type ActivationTier = int32

const (
	TierCold ActivationTier = iota
	TierCool
	TierWarm
	TierHot
)

var tierNames = map[ActivationTier]string{
	TierCold: "cold",
	TierCool: "cool",
	TierWarm: "warm",
	TierHot:  "hot",
}

// TierString returns the string name for an ActivationTier.
func TierString(t ActivationTier) string {
	if name, ok := tierNames[t]; ok {
		return name
	}
	return "unknown"
}
