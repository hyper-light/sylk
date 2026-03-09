package container

import (
	"context"
	"time"
)

// ContainerID uniquely identifies a container within the runtime.
type ContainerID string

// PodID uniquely identifies a pod within the runtime.
type PodID string

// ContainerRole defines how a container participates in a pod.
type ContainerRole int

const (
	RoleInit    ContainerRole = iota // Run-to-completion before main containers
	RoleMain                         // Primary workload container
	RoleSidecar                      // Runs alongside main, shares pod namespace
)

// roleNames maps container roles to their string representation.
var roleNames = map[ContainerRole]string{
	RoleInit:    "init",
	RoleMain:    "main",
	RoleSidecar: "sidecar",
}

// String returns the string representation of a container role.
func (r ContainerRole) String() string {
	if name, ok := roleNames[r]; ok {
		return name
	}
	return "unknown"
}

// RestartPolicy determines container restart behavior on exit.
type RestartPolicy int

const (
	RestartAlways    RestartPolicy = iota // Restart unconditionally
	RestartOnFailure                      // Restart only on non-zero exit
	RestartNever                          // Never restart
)

// restartPolicyNames maps restart policies to their string representation.
var restartPolicyNames = map[RestartPolicy]string{
	RestartAlways:    "always",
	RestartOnFailure: "on-failure",
	RestartNever:     "never",
}

// String returns the string representation of a restart policy.
func (p RestartPolicy) String() string {
	if name, ok := restartPolicyNames[p]; ok {
		return name
	}
	return "unknown"
}

// AgentDescriptorRef captures agent metadata for container spec without
// importing the handoff package directly (avoids transitive import issues).
type AgentDescriptorRef struct {
	AgentType     string
	ModelID       string
	ContextWindow int
	Category      int // Maps to handoff.AgentCategory
}

// FileAccessType identifies how a container accesses the filesystem.
type FileAccessType int

const (
	FileAccessDisk   FileAccessType = iota // Direct disk I/O (Architect, Guide, Librarian, etc.)
	FileAccessVFS                          // Per-pipeline VFS overlay (Engineer, Designer, Pipeline Inspector/Tester)
	FileAccessGlobal                       // Per-session in-memory global overlay (Global Inspector, Global Tester)
)

var fileAccessTypeNames = map[FileAccessType]string{
	FileAccessDisk:   "disk",
	FileAccessVFS:    "vfs",
	FileAccessGlobal: "global",
}

func (f FileAccessType) String() string {
	if name, ok := fileAccessTypeNames[f]; ok {
		return name
	}
	return "unknown"
}

// ContainerSpec is the declarative specification for a single container.
type ContainerSpec struct {
	Name           string
	AgentType      string
	Descriptor     AgentDescriptorRef
	Role           ContainerRole
	RestartPolicy  RestartPolicy
	FileAccessType FileAccessType
	Resources      ResourceSpec
	Mounts         []MountSpec
	Network        NetworkAttachment
	Security       SecurityContextSpec
	Probes         []ProbeSpec
	Hooks          LifecycleHookSpec
	Env            map[string]string
	GracefulStop   time.Duration
	Labels         map[string]string
}

// ResourceSpec defines resource requests (soft) and limits (hard).
type ResourceSpec struct {
	GoroutineRequest     int64 // Soft goroutine limit (maps to GoroutineBudget soft)
	GoroutineLimit       int64 // Hard goroutine limit (maps to GoroutineBudget hard)
	ContextWindowRequest int   // Soft token limit
	ContextWindowLimit   int   // Hard token limit (from AgentDescriptor.ContextWindow)
	VFSQuotaBytes        int64 // Max writable bytes in container VFS
	CacheMaxCost         int64 // Ristretto cost allocation
}

// IsZero returns true if no resource constraints are specified.
func (r ResourceSpec) IsZero() bool {
	return r.GoroutineRequest == 0 &&
		r.GoroutineLimit == 0 &&
		r.ContextWindowRequest == 0 &&
		r.ContextWindowLimit == 0 &&
		r.VFSQuotaBytes == 0 &&
		r.CacheMaxCost == 0
}

// MountSpec defines a filesystem mount into a container.
type MountSpec struct {
	Name      string // Logical volume name (references PodSpec.Volumes)
	MountPath string // Path inside container's VFS namespace
	ReadOnly  bool
	SubPath   string // Mount sub-path of volume
}

// VolumeType identifies the backing storage for a volume.
type VolumeType int

const (
	VolumeTypeVFS      VolumeType = iota // PipelineVFS overlay (CoW)
	VolumeTypeEmptyDir                   // Ephemeral per-pod storage
	VolumeTypeHostPath                   // Direct host FS (read-only enforced)
)

// volumeTypeNames maps volume types to their string representation.
var volumeTypeNames = map[VolumeType]string{
	VolumeTypeVFS:      "vfs",
	VolumeTypeEmptyDir: "emptydir",
	VolumeTypeHostPath: "hostpath",
}

// String returns the string representation of a volume type.
func (v VolumeType) String() string {
	if name, ok := volumeTypeNames[v]; ok {
		return name
	}
	return "unknown"
}

// VolumeSpec defines a named volume for a pod.
type VolumeSpec struct {
	Name      string
	Type      VolumeType
	HostPath  string // Only for VolumeTypeHostPath
	SizeLimit int64  // Max bytes (0 = no limit beyond ResourceSpec.VFSQuotaBytes)
}

// NetworkAttachment specifies how a container connects to the mesh.
type NetworkAttachment struct {
	PublishTopics   []string // Topics this container may publish to
	SubscribeTopics []string // Topics this container may subscribe to
	AllowedPeers    []string // Agent IDs this container can communicate with
	DeniedPeers     []string // Agent IDs blocked from reaching this container
}

// SecurityContextSpec defines security constraints for a container.
// Uses string-based role to decouple from security package at the spec level.
type SecurityContextSpec struct {
	Role          string // Maps to security.AgentRole (e.g. "worker", "supervisor")
	Capabilities  CapabilitySpec
	RunAsReadOnly bool
	SecretRefs    []string
}

// CapabilitySpec defines what a container is allowed to do.
type CapabilitySpec struct {
	Signals         []string // Signal type names this container can send/receive
	PublishTopics   []string // Bus topics publishable
	SubscribeTopics []string // Bus topics subscribable
	PathRead        []string // Paths with read access
	PathWrite       []string // Paths with write access
	NetworkEgress   []string // Allowed egress domain patterns (e.g. "*.golang.org", "*")
	NetworkIngress  []string // Allowed ingress content types (e.g. "text/html", "application/pdf")
	CanEscalate     bool     // Can request additional capabilities at runtime
}

// ProbeType distinguishes the three categories of health probes.
type ProbeType int

const (
	ProbeLiveness  ProbeType = iota // Container alive?
	ProbeReadiness                  // Ready for traffic?
	ProbeStartup                    // Started successfully?
)

// probeTypeNames maps probe types to their string representation.
var probeTypeNames = map[ProbeType]string{
	ProbeLiveness:  "liveness",
	ProbeReadiness: "readiness",
	ProbeStartup:   "startup",
}

// String returns the string representation of a probe type.
func (p ProbeType) String() string {
	if name, ok := probeTypeNames[p]; ok {
		return name
	}
	return "unknown"
}

// ProbeSpec configures a health probe for a container.
type ProbeSpec struct {
	Type             ProbeType
	Handler          ProbeHandler
	InitialDelay     time.Duration
	Period           time.Duration
	Timeout          time.Duration
	SuccessThreshold int // Consecutive successes to mark healthy
	FailureThreshold int // Consecutive failures to mark unhealthy
}

// ProbeHandler executes a health check.
type ProbeHandler interface {
	Execute(ctx context.Context) (ProbeResult, error)
}

// ProbeResult is the outcome of a single probe execution.
type ProbeResult struct {
	Success   bool
	Message   string
	Latency   time.Duration
	Timestamp time.Time
}

// HookPhase identifies when a lifecycle hook runs.
type HookPhase int

const (
	HookPreStart  HookPhase = iota // Before container starts
	HookPostStart                  // After container enters Running
	HookPreStop                    // Before container stops
	HookPostStop                   // After container stops
)

// LifecycleHookSpec configures hooks for container lifecycle events.
type LifecycleHookSpec struct {
	PreStart  HookAction
	PostStart HookAction
	PreStop   HookAction
	PostStop  HookAction
}

// HookAction defines the action to execute for a lifecycle hook.
type HookAction struct {
	Handler HookHandler
	Timeout time.Duration
}

// HookHandler executes a lifecycle hook action.
type HookHandler interface {
	Execute(ctx context.Context) error
}
