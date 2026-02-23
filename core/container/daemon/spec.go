package daemon

import (
	"github.com/adalundhe/sylk/core/container"
)

// UpdateStrategy determines how daemon set containers are updated.
type UpdateStrategy int

const (
	UpdateRolling  UpdateStrategy = iota // Replace one at a time
	UpdateOnDelete                       // Replace only when manually deleted
)

// Toleration exempts a daemon set from eviction conditions.
type Toleration struct {
	Key      string // e.g., "resource-pressure"
	Operator string // "Exists" or "Equal"
	Value    string
	Effect   string // "NoSchedule" or "NoExecute"
}

// DaemonSetSpec defines a daemon set — ensures exactly one container instance
// per session for long-running system services.
type DaemonSetSpec struct {
	Name           string
	ContainerSpec  container.ContainerSpec
	Tolerations    []Toleration
	Priority       int // Higher priority than workload containers
	UpdateStrategy UpdateStrategy
}

// DaemonSetStatus is a snapshot of a daemon set's state.
type DaemonSetStatus struct {
	Name          string
	Running       bool
	ContainerID   container.ContainerID
	RestartCount  int64
	Healthy       bool
}

// Matches reports whether the toleration matches the given taint key/value/effect.
func (t Toleration) Matches(key, value, effect string) bool {
	if t.Key != key {
		return false
	}
	if t.Effect != "" && t.Effect != effect {
		return false
	}
	if t.Operator == "Exists" {
		return true
	}
	return t.Value == value
}

// HasToleration reports whether any toleration in the set matches.
func HasToleration(tolerations []Toleration, key, value, effect string) bool {
	for _, t := range tolerations {
		if t.Matches(key, value, effect) {
			return true
		}
	}
	return false
}
