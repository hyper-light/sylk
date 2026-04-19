// Package identity provides the single canonical identifier for every
// runtime agent container in Sylk, plus the task-reference value that
// requests carry alongside that identity.
//
// Design (see docs/FIX_ID_AND_TOKENS.md):
//
//   - AgentIdentity is the K8s-shaped container-level identity:
//     {UID, Namespace, Pod, Name, Kind, Category, Model, Generation,
//     Window, Labels, Owner}. Immutable once minted. SwapModel produces
//     a new AgentIdentity with Generation+1, sealing the prior.
//
//   - TaskRef is the K8s Job-analog work-unit reference carried on the
//     request context alongside identity. Orthogonal to identity: a
//     container may service many tasks; a task may flow through many
//     containers.
//
//   - Both values are constructed exclusively by Factory. No
//     zero-value fallback, no string parsing, no defaults.
//
//   - AccountingKey = (UID, Generation, Model, TaskUID). The accountant
//     reduces over this canonical table for every view.
package identity

import (
	"fmt"
	"sort"
	"strings"
)

// PodID uniquely identifies a managed pod. Intentionally a type alias
// to string so it interoperates cleanly with core/container/pod.PodID
// (also a string alias) without either package importing the other.
type PodID = string

// PodType classifies a pod's membership and activation semantics. It
// mirrors core/container/pod.PodType by value — conversions at the
// boundary are explicit casts between two defined types with the same
// underlying int values, avoiding an import cycle through
// core/container/pod → core/container → … → core/providers.
type PodType int

const (
	// PodTypePipeline groups multiple pipeline agents (engineer,
	// designer, inspector-pipeline, tester-pipeline) that
	// activate/deactivate atomically.
	PodTypePipeline PodType = iota
	// PodTypeSingleton wraps a single agent (architect,
	// inspector-global, tester-global) with its own tier lifecycle.
	PodTypeSingleton
	// PodTypeDaemon marks always-hot pods (guide, orchestrator,
	// guardian) that are never demoted below TierHot.
	PodTypeDaemon
)

var podTypeNames = map[PodType]string{
	PodTypePipeline:  "pipeline",
	PodTypeSingleton: "singleton",
	PodTypeDaemon:    "daemon",
}

// String returns the canonical name for a PodType.
func (p PodType) String() string {
	if name, ok := podTypeNames[p]; ok {
		return name
	}
	return "unknown"
}

// IsDaemon reports whether this pod type is always-hot.
func (p PodType) IsDaemon() bool { return p == PodTypeDaemon }

// UID is a globally-unique, stable identifier for a running agent
// container, scoped by its Namespace. Produced by Factory (UUIDv7 in
// production; deterministic in tests).
type UID string

// Namespace is the logical grouping equivalent to a K8s namespace. In
// Sylk runtime this is the session id. Every identity and every task
// must have a non-empty namespace.
type Namespace string

// Name is the human-readable container name within a pod. Unique
// within (Namespace, Pod.ID). Derived deterministically by Factory
// from Kind + ordinal (for singletons) or Kind + Task suffix (for
// pipeline workers).
type Name string

// Generation increments on every SwapModel. The prior generation is
// sealed — in-flight requests keep their old identity; new requests
// use the bumped one. Starts at 0.
type Generation uint64

// ContextWindow is the model's maximum context in tokens.
type ContextWindow uint32

// Category mirrors handoff.AgentCategory: knowledge agents evict,
// standalone agents hand off same-type, pipeline agents execute
// within a pipeline goroutine. See docs/HANDOFF.md.
type Category uint8

const (
	CategoryUnspecified Category = iota
	CategoryKnowledge
	CategoryStandalone
	CategoryPipeline
	// CategorySystem marks identities used for non-agent internal
	// calls (OAuth refresh, health probes). Flows through the
	// gateway and is observable but never billable to any agent.
	CategorySystem
)

var categoryNames = map[Category]string{
	CategoryUnspecified: "unspecified",
	CategoryKnowledge:   "knowledge",
	CategoryStandalone:  "standalone",
	CategoryPipeline:    "pipeline",
	CategorySystem:      "system",
}

// String returns the canonical name for a Category.
func (c Category) String() string {
	if name, ok := categoryNames[c]; ok {
		return name
	}
	return "unknown"
}

// IsBillable reports whether LLM usage under this category should
// accrue to an agent's accounting bucket. Only CategorySystem is
// non-billable.
func (c Category) IsBillable() bool {
	return c != CategorySystem && c != CategoryUnspecified
}

// AgentType is the closed enum of supported agent kinds. Adding a new
// kind requires extending this enum and registering the agent with
// core/handoff/agent_descriptors.go.
type AgentType uint8

const (
	AgentTypeUnspecified AgentType = iota
	AgentTypeGuide
	AgentTypeArchitect
	AgentTypeOrchestrator
	AgentTypeArchivalist
	AgentTypeLibrarian
	AgentTypeAcademic
	AgentTypeEngineer
	AgentTypeDesigner
	AgentTypeInspectorGlobal
	AgentTypeInspectorPipeline
	AgentTypeTesterGlobal
	AgentTypeTesterPipeline
	AgentTypeGuardian
	AgentTypeScribe
	// AgentTypeSystem is used by CategorySystem identities (OAuth
	// refresh, probes). Not a real agent kind.
	AgentTypeSystem
)

var agentTypeNames = map[AgentType]string{
	AgentTypeUnspecified:       "unspecified",
	AgentTypeGuide:             "guide",
	AgentTypeArchitect:         "architect",
	AgentTypeOrchestrator:      "orchestrator",
	AgentTypeArchivalist:       "archivalist",
	AgentTypeLibrarian:         "librarian",
	AgentTypeAcademic:          "academic",
	AgentTypeEngineer:          "engineer",
	AgentTypeDesigner:          "designer",
	AgentTypeInspectorGlobal:   "inspector",
	AgentTypeInspectorPipeline: "inspector-pipeline",
	AgentTypeTesterGlobal:      "tester",
	AgentTypeTesterPipeline:    "tester-pipeline",
	AgentTypeGuardian:          "guardian",
	AgentTypeScribe:            "scribe",
	AgentTypeSystem:            "system",
}

// agentTypeByName is the reverse lookup from canonical string form
// (as used in handoff.AgentDescriptor.AgentType) to AgentType.
var agentTypeByName = map[string]AgentType{
	"guide":              AgentTypeGuide,
	"architect":          AgentTypeArchitect,
	"orchestrator":       AgentTypeOrchestrator,
	"archivalist":        AgentTypeArchivalist,
	"librarian":          AgentTypeLibrarian,
	"academic":           AgentTypeAcademic,
	"engineer":           AgentTypeEngineer,
	"designer":           AgentTypeDesigner,
	"inspector":          AgentTypeInspectorGlobal,
	"inspector-pipeline": AgentTypeInspectorPipeline,
	"tester":             AgentTypeTesterGlobal,
	"tester-pipeline":    AgentTypeTesterPipeline,
	"guardian":           AgentTypeGuardian,
	"scribe":             AgentTypeScribe,
	"system":             AgentTypeSystem,
}

// String returns the canonical agent-type name used in descriptors
// and log output.
func (k AgentType) String() string {
	if name, ok := agentTypeNames[k]; ok {
		return name
	}
	return "unknown"
}

// AgentTypeFromString resolves a canonical name to an AgentType.
// Returns (AgentTypeUnspecified, false) when the name is unknown.
func AgentTypeFromString(s string) (AgentType, bool) {
	k, ok := agentTypeByName[strings.TrimSpace(s)]
	return k, ok
}

// ModelID is the registered model identifier. Validated by the
// factory against ModelRegistry on construction.
type ModelID string

// String returns the raw model identifier.
func (m ModelID) String() string { return string(m) }

// PodRef identifies the pod that hosts a container. The ID must
// match a pod registered in the activation controller; the type
// must match that pod's classification.
type PodRef struct {
	ID   PodID
	Type PodType
}

// String renders a PodRef as "<id>:<type>" for log output.
func (p PodRef) String() string {
	return string(p.ID) + ":" + p.Type.String()
}

// OwnerRef is the K8s metadata.ownerReferences equivalent: a typed
// back-pointer to the parent identity of a replica. Non-nil only
// for replicas; canonical agents have Owner == nil.
type OwnerRef struct {
	UID  UID
	Name Name
	Kind AgentType
}

// String renders an OwnerRef for log output.
func (o OwnerRef) String() string {
	return fmt.Sprintf("%s/%s#%s", o.Kind.String(), o.Name, o.UID)
}

// Reserved label keys. Factory auto-populates these from the
// identity's typed fields; callers cannot set them via user labels.
const (
	LabelKind      = "sylk/kind"
	LabelModel     = "sylk/model"
	LabelPod       = "sylk/pod"
	LabelCategory  = "sylk/category"
	LabelNamespace = "sylk/namespace"
	LabelOwnerUID  = "sylk/owner-uid"
	// LabelTask / LabelPipeline are applied by request-dispatch sites
	// when a request is task-scoped. Not set at Mint time.
	LabelTask     = "sylk/task"
	LabelPipeline = "sylk/pipeline"
	LabelStage    = "sylk/pipeline-stage"

	// reservedLabelPrefix is the namespace for all Sylk-internal
	// label keys. User-supplied labels cannot use this prefix.
	reservedLabelPrefix = "sylk/"
)

// IsReservedLabel reports whether key is part of the reserved Sylk
// label namespace (sylk/...). Factory enforces that user-supplied
// Labels do not contain any reserved keys.
func IsReservedLabel(key string) bool {
	return strings.HasPrefix(key, reservedLabelPrefix)
}

// Labels is a K8s-style label map. Reserved keys (sylk/...) are
// auto-populated by Factory from the identity's fields; user-
// supplied labels use any other key space. Nil and empty are
// interchangeable.
type Labels map[string]string

// Clone returns a deep copy. Never aliases the original map.
func (l Labels) Clone() Labels {
	if len(l) == 0 {
		return nil
	}
	out := make(Labels, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

// Merge returns a new Labels containing all keys from l followed by
// all keys from other (other takes precedence on conflicts). Nil
// inputs are treated as empty. Result is never aliased to either
// input.
func (l Labels) Merge(other Labels) Labels {
	if len(l) == 0 && len(other) == 0 {
		return nil
	}
	out := make(Labels, len(l)+len(other))
	for k, v := range l {
		out[k] = v
	}
	for k, v := range other {
		out[k] = v
	}
	return out
}

// Has reports whether key is present (any value).
func (l Labels) Has(key string) bool {
	if l == nil {
		return false
	}
	_, ok := l[key]
	return ok
}

// Get returns the value for key, or "" when absent.
func (l Labels) Get(key string) string {
	if l == nil {
		return ""
	}
	return l[key]
}

// SortedKeys returns the label keys in sorted order. Useful for
// deterministic serialization and log output.
func (l Labels) SortedKeys() []string {
	if len(l) == 0 {
		return nil
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AgentIdentity is the single canonical identifier for a running
// agent container. Immutable once minted; SwapModel produces a new
// AgentIdentity sharing UID/Namespace/Pod/Name/Kind with the prior
// but bumping Generation and replacing Model.
//
// Equality is structural. Never parsed from a string. Construction
// is exclusively via Factory.
type AgentIdentity struct {
	// K8s-shaped identity
	uid       UID
	namespace Namespace
	pod       PodRef
	name      Name

	// Agent spec
	kind       AgentType
	category   Category
	model      ModelID
	generation Generation
	window     ContextWindow
	labels     Labels

	// Ownership (nil for canonical agents)
	owner *OwnerRef
}

// UID returns the container's globally-unique identifier.
func (a *AgentIdentity) UID() UID { return a.uid }

// Namespace returns the session/namespace this container lives in.
func (a *AgentIdentity) Namespace() Namespace { return a.namespace }

// Pod returns the pod that hosts this container.
func (a *AgentIdentity) Pod() PodRef { return a.pod }

// Name returns the container name within its pod.
func (a *AgentIdentity) Name() Name { return a.name }

// Kind returns the agent kind.
func (a *AgentIdentity) Kind() AgentType { return a.kind }

// Category returns the agent category.
func (a *AgentIdentity) Category() Category { return a.category }

// Model returns the current model identifier.
func (a *AgentIdentity) Model() ModelID { return a.model }

// Generation returns the current generation counter.
func (a *AgentIdentity) Generation() Generation { return a.generation }

// Window returns the model's context window in tokens.
func (a *AgentIdentity) Window() ContextWindow { return a.window }

// Labels returns a defensive copy of the label map. Callers may
// mutate the result without affecting the identity.
func (a *AgentIdentity) Labels() Labels { return a.labels.Clone() }

// Label returns the value for key, or "" when absent.
func (a *AgentIdentity) Label(key string) string { return a.labels.Get(key) }

// Owner returns the parent replica's OwnerRef, or nil for canonical
// agents.
func (a *AgentIdentity) Owner() *OwnerRef {
	if a.owner == nil {
		return nil
	}
	o := *a.owner
	return &o
}

// IsReplica reports whether this identity is a replica (Owner != nil).
func (a *AgentIdentity) IsReplica() bool { return a.owner != nil }

// Visible returns the user-facing panel name. Pipeline workers
// whose Labels include a task reference return a task-scoped form
// via the accompanying TaskRef at display time — Visible alone
// returns the kind.
func (a *AgentIdentity) Visible() string {
	return a.kind.String()
}

// Panel returns the K8s-style addressable string
// "<namespace>/<pod>/<name>". Used for log correlation and the UI
// agent-panel key.
func (a *AgentIdentity) Panel() string {
	return string(a.namespace) + "/" + string(a.pod.ID) + "/" + string(a.name)
}

// Key returns the (UID, Generation, Model) triple that identifies
// one continuous run of this container under one model. The
// accountant keys on (Key, TaskUID).
func (a *AgentIdentity) Key() ContainerKey {
	return ContainerKey{
		UID:        a.uid,
		Generation: a.generation,
		Model:      a.model,
	}
}

// String is the debug form. Never parsed.
func (a *AgentIdentity) String() string {
	if a == nil {
		return "<nil identity>"
	}
	owner := ""
	if a.owner != nil {
		owner = " owner=" + a.owner.String()
	}
	return fmt.Sprintf(
		"%s[uid=%s gen=%d model=%s pod=%s ns=%s%s]",
		a.kind.String(), a.uid, a.generation, a.model, a.pod, a.namespace, owner,
	)
}

// Equal reports whether two identities are structurally identical.
// Useful for tests; runtime code compares by UID + Generation.
func (a *AgentIdentity) Equal(b *AgentIdentity) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.uid != b.uid || a.namespace != b.namespace || a.name != b.name ||
		a.pod != b.pod || a.kind != b.kind || a.category != b.category ||
		a.model != b.model || a.generation != b.generation || a.window != b.window {
		return false
	}
	if (a.owner == nil) != (b.owner == nil) {
		return false
	}
	if a.owner != nil && *a.owner != *b.owner {
		return false
	}
	if len(a.labels) != len(b.labels) {
		return false
	}
	for k, v := range a.labels {
		if b.labels[k] != v {
			return false
		}
	}
	return true
}

// ContainerKey is the (UID, Generation, Model) triple that the
// accountant uses — combined with a TaskUID — to key usage totals.
type ContainerKey struct {
	UID        UID
	Generation Generation
	Model      ModelID
}

// String renders a ContainerKey for log output.
func (k ContainerKey) String() string {
	return fmt.Sprintf("%s/gen-%d/%s", k.UID, k.Generation, k.Model)
}
