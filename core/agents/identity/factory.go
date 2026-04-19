package identity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Factory-time errors. Every invariant is surfaced as a named
// sentinel so tests and log handlers can distinguish classes of
// validation failure.
var (
	ErrNilOptions          = errors.New("identity: nil options")
	ErrMissingNamespace    = errors.New("identity: namespace required")
	ErrMissingKind         = errors.New("identity: kind required")
	ErrMissingModel        = errors.New("identity: model required")
	ErrMissingPod          = errors.New("identity: pod ref required")
	ErrUnknownModel        = errors.New("identity: model not registered for kind")
	ErrUnknownPod          = errors.New("identity: pod not registered")
	ErrPodTypeMismatch     = errors.New("identity: pod type does not match registry")
	ErrCategoryMismatch    = errors.New("identity: category does not match descriptor")
	ErrReservedLabel       = errors.New("identity: reserved label key supplied by caller")
	ErrNilParent           = errors.New("identity: replica requires non-nil parent")
	ErrSystemKind          = errors.New("identity: system identity must use AgentTypeSystem + CategorySystem")
	ErrNilIdentity         = errors.New("identity: nil identity")
	ErrMissingTaskCorr     = errors.New("identity: task requires correlation id")
	ErrMissingTaskSession  = errors.New("identity: task requires namespace")
	ErrNilParentTask       = errors.New("identity: subtask requires non-nil parent")
	ErrPipelineWithoutID   = errors.New("identity: pipeline ref requires non-empty id")
	ErrNameCollision       = errors.New("identity: name already in use within pod")
	ErrFactoryNotBound     = errors.New("identity: factory not bound to a namespace")
	ErrNilModelRegistry    = errors.New("identity: nil model registry")
	ErrNilPodRegistry      = errors.New("identity: nil pod registry")
)

// ModelRegistry resolves which ModelID is permitted for a given
// AgentType. Implementations typically wrap
// core/handoff/DescriptorRegistry.
type ModelRegistry interface {
	// AllowedModels returns the set of ModelIDs permitted for this
	// AgentType. An empty slice means "no models registered",
	// treated as an error for non-system kinds.
	AllowedModels(kind AgentType) []ModelID
	// Category returns the agent category registered for this kind.
	Category(kind AgentType) (Category, bool)
	// ContextWindow returns the model's context window in tokens.
	// Zero means "unknown" and is treated as an error for non-system
	// kinds.
	ContextWindow(kind AgentType, model ModelID) ContextWindow
}

// PodRegistry validates that a PodRef corresponds to a real pod.
// Implementations typically wrap the activation controller's pod
// policy registry.
type PodRegistry interface {
	// Lookup returns (type, true) when podID is registered, along
	// with the pod's declared PodType. Returns (0, false) when not
	// found.
	Lookup(podID PodID) (PodType, bool)
	// PipelinePodID returns the well-known PodID for the pipeline
	// pod. Pipeline-worker containers always mint against this.
	PipelinePodID() PodID
}

// Factory is the only path to producing AgentIdentity and TaskRef
// values. Bound at session start to a Namespace, a ModelRegistry,
// and a PodRegistry.
//
// Thread-safe. The name-ordinal counters are protected by an internal
// map+mutex so replica-ordinal assignment is deterministic across
// concurrent mints.
type Factory struct {
	clock        func() time.Time
	newUID       func() string
	models       ModelRegistry
	pods         PodRegistry
	namespace    Namespace
	nameOrdinals *ordinalRegistry
}

// FactoryConfig configures a Factory at construction time.
type FactoryConfig struct {
	Namespace Namespace
	Models    ModelRegistry
	Pods      PodRegistry
	// Clock is optional; defaults to time.Now.
	Clock func() time.Time
	// NewUID is optional; defaults to uuid.NewString (UUIDv4 for
	// broad Go-stdlib compat). Tests can inject a deterministic
	// generator.
	NewUID func() string
}

// NewFactory builds a Factory. Panics on nil registries — the
// factory has no operating mode without them.
func NewFactory(cfg FactoryConfig) (*Factory, error) {
	if strings.TrimSpace(string(cfg.Namespace)) == "" {
		return nil, ErrFactoryNotBound
	}
	if cfg.Models == nil {
		return nil, ErrNilModelRegistry
	}
	if cfg.Pods == nil {
		return nil, ErrNilPodRegistry
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	newUID := cfg.NewUID
	if newUID == nil {
		newUID = uuid.NewString
	}
	return &Factory{
		clock:        clock,
		newUID:       newUID,
		models:       cfg.Models,
		pods:         cfg.Pods,
		namespace:    cfg.Namespace,
		nameOrdinals: newOrdinalRegistry(),
	}, nil
}

// Namespace returns the factory's bound namespace.
func (f *Factory) Namespace() Namespace { return f.namespace }

// MintOptions describes a canonical (non-replica) AgentIdentity.
type MintOptions struct {
	Kind   AgentType
	Pod    PodRef
	// Model is optional; when empty the factory uses the first
	// model allowed for Kind in the registry.
	Model  ModelID
	// Labels are merged with auto-populated reserved labels. Must
	// not contain any reserved-prefix keys.
	Labels Labels
	// NameHint lets callers nudge the generated name (rarely used).
	// Always combined with a factory-assigned ordinal for uniqueness.
	NameHint string
}

// Mint constructs a canonical AgentIdentity. Returns an error if
// any invariant fails.
func (f *Factory) Mint(opts MintOptions) (*AgentIdentity, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	if opts.Kind == AgentTypeUnspecified {
		return nil, ErrMissingKind
	}
	if strings.TrimSpace(string(opts.Pod.ID)) == "" {
		return nil, ErrMissingPod
	}

	podType, ok := f.pods.Lookup(opts.Pod.ID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPod, opts.Pod.ID)
	}
	if podType != opts.Pod.Type {
		return nil, fmt.Errorf("%w: pod %q is %s, got %s",
			ErrPodTypeMismatch, opts.Pod.ID, podType.String(), opts.Pod.Type.String())
	}

	category, catOK := f.models.Category(opts.Kind)
	if !catOK || category == CategoryUnspecified {
		return nil, fmt.Errorf("%w: no category for %s", ErrCategoryMismatch, opts.Kind)
	}

	model := opts.Model
	allowed := f.models.AllowedModels(opts.Kind)
	if model == "" {
		if len(allowed) == 0 {
			return nil, fmt.Errorf("%w: %s has no registered models", ErrMissingModel, opts.Kind)
		}
		model = allowed[0]
	} else if !containsModel(allowed, model) {
		return nil, fmt.Errorf("%w: %s not in allowed set for %s", ErrUnknownModel, model, opts.Kind)
	}

	window := f.models.ContextWindow(opts.Kind, model)

	if err := validateUserLabels(opts.Labels); err != nil {
		return nil, err
	}

	name, err := f.nameOrdinals.assign(opts.Pod.ID, opts.Kind, opts.NameHint)
	if err != nil {
		return nil, err
	}

	uid := UID(f.newUID())

	id := &AgentIdentity{
		uid:        uid,
		namespace:  f.namespace,
		pod:        opts.Pod,
		name:       name,
		kind:       opts.Kind,
		category:   category,
		model:      model,
		generation: 0,
		window:     window,
		labels:     buildLabels(opts.Kind, opts.Pod, category, f.namespace, opts.Labels, nil),
	}
	return id, nil
}

// MintReplicaOptions describes a replica AgentIdentity derived from
// a parent.
type MintReplicaOptions struct {
	Parent *AgentIdentity
	// Pod overrides the parent's pod when non-nil (e.g. a knowledge
	// agent spawning a replica that runs inside the pipeline pod).
	Pod *PodRef
	// Model overrides the parent's model when non-empty.
	Model ModelID
	// Labels are merged onto the parent's replica labels. Must not
	// contain reserved-prefix keys.
	Labels Labels
}

// MintReplica constructs a child AgentIdentity whose Owner points
// at the parent. Generation starts at 0 for the replica; labels
// and category are inherited.
func (f *Factory) MintReplica(opts MintReplicaOptions) (*AgentIdentity, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	if opts.Parent == nil {
		return nil, ErrNilParent
	}
	parent := opts.Parent
	if parent.namespace != f.namespace {
		return nil, fmt.Errorf("identity: parent namespace %q does not match factory %q",
			parent.namespace, f.namespace)
	}

	podRef := parent.pod
	if opts.Pod != nil {
		if opts.Pod.ID == "" {
			return nil, ErrMissingPod
		}
		pt, ok := f.pods.Lookup(opts.Pod.ID)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownPod, opts.Pod.ID)
		}
		if pt != opts.Pod.Type {
			return nil, fmt.Errorf("%w: pod %q is %s, got %s",
				ErrPodTypeMismatch, opts.Pod.ID, pt.String(), opts.Pod.Type.String())
		}
		podRef = *opts.Pod
	}

	model := opts.Model
	if model == "" {
		model = parent.model
	} else {
		allowed := f.models.AllowedModels(parent.kind)
		if !containsModel(allowed, model) {
			return nil, fmt.Errorf("%w: %s not in allowed set for %s",
				ErrUnknownModel, model, parent.kind)
		}
	}
	window := f.models.ContextWindow(parent.kind, model)

	if err := validateUserLabels(opts.Labels); err != nil {
		return nil, err
	}

	replicaName, err := f.nameOrdinals.assignReplica(podRef.ID, parent.name)
	if err != nil {
		return nil, err
	}

	owner := &OwnerRef{
		UID:  parent.uid,
		Name: parent.name,
		Kind: parent.kind,
	}

	id := &AgentIdentity{
		uid:        UID(f.newUID()),
		namespace:  parent.namespace,
		pod:        podRef,
		name:       replicaName,
		kind:       parent.kind,
		category:   parent.category,
		model:      model,
		generation: 0,
		window:     window,
		labels:     buildLabels(parent.kind, podRef, parent.category, parent.namespace, opts.Labels, owner),
		owner:      owner,
	}
	return id, nil
}

// WithNewGeneration returns a new AgentIdentity that shares
// UID/Namespace/Pod/Name/Kind/Category/Owner with prior but bumps
// Generation and replaces Model (and Window, via registry lookup).
// The prior identity is sealed for accounting; in-flight requests
// continue to carry it.
func (f *Factory) WithNewGeneration(prior *AgentIdentity, newModel ModelID) (*AgentIdentity, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	if prior == nil {
		return nil, ErrNilIdentity
	}
	if newModel == "" {
		return nil, ErrMissingModel
	}
	allowed := f.models.AllowedModels(prior.kind)
	if !containsModel(allowed, newModel) {
		return nil, fmt.Errorf("%w: %s not in allowed set for %s",
			ErrUnknownModel, newModel, prior.kind)
	}
	window := f.models.ContextWindow(prior.kind, newModel)

	labels := prior.labels.Clone()
	if labels == nil {
		labels = make(Labels)
	}
	labels[LabelModel] = string(newModel)

	next := &AgentIdentity{
		uid:        prior.uid,
		namespace:  prior.namespace,
		pod:        prior.pod,
		name:       prior.name,
		kind:       prior.kind,
		category:   prior.category,
		model:      newModel,
		generation: prior.generation + 1,
		window:     window,
		labels:     labels,
		owner:      clonedOwner(prior.owner),
	}
	return next, nil
}

// SystemIdentity returns a pseudo-identity for non-agent internal
// calls (OAuth refresh, health probes). Flows through the gateway
// and is observable in the accountant but marked non-billable by
// Category=System.
//
// Purpose is a short free-form tag (e.g. "oauth-refresh",
// "health-probe") that surfaces in logs and telemetry.
func (f *Factory) SystemIdentity(purpose string) (*AgentIdentity, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = "system"
	}

	name, err := f.nameOrdinals.assign(systemPodID, AgentTypeSystem, purpose)
	if err != nil {
		return nil, err
	}

	labels := Labels{
		LabelKind:      AgentTypeSystem.String(),
		LabelCategory:  CategorySystem.String(),
		LabelPod:       systemPodID,
		LabelNamespace: string(f.namespace),
		"sylk/purpose": purpose,
	}

	id := &AgentIdentity{
		uid:        UID(f.newUID()),
		namespace:  f.namespace,
		pod:        PodRef{ID: systemPodID, Type: PodTypeDaemon},
		name:       name,
		kind:       AgentTypeSystem,
		category:   CategorySystem,
		model:      ModelID(purpose),
		generation: 0,
		window:     0,
		labels:     labels,
	}
	return id, nil
}

// TaskOptions describes a new top-level task.
type TaskOptions struct {
	DisplayID   string
	Correlation CorrelationID
	Pipeline    *PipelineRef
	Labels      Labels
}

// NewTask mints a fresh TaskRef. The namespace is the factory's
// bound namespace. Correlation is required — a task without a
// triggering request is meaningless.
func (f *Factory) NewTask(opts TaskOptions) (*TaskRef, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	if strings.TrimSpace(string(opts.Correlation)) == "" {
		return nil, ErrMissingTaskCorr
	}
	if err := validateUserLabels(opts.Labels); err != nil {
		return nil, err
	}
	if opts.Pipeline != nil && opts.Pipeline.ID == "" {
		return nil, ErrPipelineWithoutID
	}
	labels := buildTaskLabels(f.namespace, opts.Pipeline, opts.Labels, "")
	return &TaskRef{
		uid:         TaskUID(f.newUID()),
		displayID:   strings.TrimSpace(opts.DisplayID),
		pipeline:    clonedPipelineRef(opts.Pipeline),
		correlation: opts.Correlation,
		namespace:   f.namespace,
		labels:      labels,
	}, nil
}

// SubTaskOptions describes a child task derived from a parent.
type SubTaskOptions struct {
	Parent      *TaskRef
	DisplayID   string
	Correlation CorrelationID
	// Pipeline overrides the parent's pipeline when non-nil. Most
	// sub-tasks stay inside the parent's pipeline and leave this nil.
	Pipeline *PipelineRef
	Stage    PipelineStage
	Labels   Labels
}

// SubTask mints a child task. Parent's TaskUID becomes the
// child's PipelineRef.Parent; the child inherits the parent's
// pipeline unless Options.Pipeline overrides.
func (f *Factory) SubTask(opts SubTaskOptions) (*TaskRef, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	if opts.Parent == nil {
		return nil, ErrNilParentTask
	}
	parent := opts.Parent
	if parent.namespace != f.namespace {
		return nil, fmt.Errorf("identity: parent task namespace %q does not match factory %q",
			parent.namespace, f.namespace)
	}
	correlation := opts.Correlation
	if correlation == "" {
		correlation = parent.correlation
	}
	if err := validateUserLabels(opts.Labels); err != nil {
		return nil, err
	}

	pipeline := clonedPipelineRef(parent.pipeline)
	if opts.Pipeline != nil {
		if opts.Pipeline.ID == "" {
			return nil, ErrPipelineWithoutID
		}
		p := *opts.Pipeline
		pipeline = &p
	}
	if pipeline != nil && opts.Stage != "" {
		pipeline.Stage = opts.Stage
	}
	if pipeline != nil {
		pipeline.Parent = parent.uid
	}
	labels := parent.labels.Clone()
	if labels == nil {
		labels = make(Labels)
	}
	labels = labels.Merge(opts.Labels)
	labels = buildTaskLabels(f.namespace, pipeline, labels, parent.uid)
	return &TaskRef{
		uid:         TaskUID(f.newUID()),
		displayID:   strings.TrimSpace(opts.DisplayID),
		pipeline:    pipeline,
		correlation: correlation,
		namespace:   f.namespace,
		labels:      labels,
	}, nil
}

// SystemTask returns a pseudo-task for CategorySystem requests. Its
// UID is the sentinel SystemTaskUID; correlation is supplied by the
// caller (OAuth refresh, probes still have a correlation id).
func (f *Factory) SystemTask(correlation CorrelationID) (*TaskRef, error) {
	if f == nil {
		return nil, ErrFactoryNotBound
	}
	if correlation == "" {
		return nil, ErrMissingTaskCorr
	}
	return &TaskRef{
		uid:         SystemTaskUID,
		displayID:   "system",
		correlation: correlation,
		namespace:   f.namespace,
		labels:      Labels{LabelNamespace: string(f.namespace)},
	}, nil
}

// systemPodID is the synthetic pod id under which CategorySystem
// identities live. Not registered in the activation controller —
// the factory short-circuits pod lookups for SystemIdentity.
const systemPodID PodID = "__system__"

// validateUserLabels fails if any reserved-prefix key is present.
func validateUserLabels(labels Labels) error {
	for k := range labels {
		if IsReservedLabel(k) {
			return fmt.Errorf("%w: %q", ErrReservedLabel, k)
		}
	}
	return nil
}

// buildLabels composes the final label set by merging auto-
// populated reserved keys over user labels.
func buildLabels(kind AgentType, podRef PodRef, category Category, ns Namespace, user Labels, owner *OwnerRef) Labels {
	out := make(Labels, len(user)+5)
	for k, v := range user {
		out[k] = v
	}
	out[LabelKind] = kind.String()
	out[LabelPod] = string(podRef.ID)
	out[LabelCategory] = category.String()
	out[LabelNamespace] = string(ns)
	if owner != nil {
		out[LabelOwnerUID] = string(owner.UID)
	}
	return out
}

// buildTaskLabels composes the final task label set.
func buildTaskLabels(ns Namespace, pipeline *PipelineRef, user Labels, parent TaskUID) Labels {
	out := make(Labels, len(user)+4)
	for k, v := range user {
		out[k] = v
	}
	out[LabelNamespace] = string(ns)
	if pipeline != nil {
		out[LabelPipeline] = string(pipeline.ID)
		if pipeline.Stage != "" {
			out[LabelStage] = string(pipeline.Stage)
		}
	}
	if parent != "" {
		out["sylk/parent-task"] = string(parent)
	}
	return out
}

// clonedOwner returns a value-copy of the owner pointer, preserving
// nil semantics.
func clonedOwner(o *OwnerRef) *OwnerRef {
	if o == nil {
		return nil
	}
	copy := *o
	return &copy
}

// clonedPipelineRef returns a value-copy of the pipeline pointer,
// preserving nil semantics.
func clonedPipelineRef(p *PipelineRef) *PipelineRef {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

// containsModel reports whether m is in the slice. Small slices; O(n)
// is fine.
func containsModel(set []ModelID, m ModelID) bool {
	for _, v := range set {
		if v == m {
			return true
		}
	}
	return false
}

// ordinalRegistry tracks per-(pod, kind) and per-parent-name replica
// ordinals for deterministic name assignment under concurrent mints.
//
// Under the hood a single RWMutex guards two maps. Name lookups are
// O(1); the critical section is tiny.
type ordinalRegistry struct {
	mu      sync.Mutex
	byKind  map[ordinalKey]*uint64
	byOwner map[ordinalKey]*uint64
}

type ordinalKey struct {
	pod  PodID
	name string
}

func newOrdinalRegistry() *ordinalRegistry {
	return &ordinalRegistry{
		byKind:  make(map[ordinalKey]*uint64),
		byOwner: make(map[ordinalKey]*uint64),
	}
}

// assign produces a canonical container name of the form
// "<kind>[-<hint>]-<ordinal>" unique within the pod. For singleton
// pods the ordinal will usually be 0.
func (r *ordinalRegistry) assign(p PodID, kind AgentType, hint string) (Name, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := ordinalKey{pod: p, name: kind.String() + "/" + strings.TrimSpace(hint)}
	counter, ok := r.byKind[key]
	if !ok {
		var c uint64
		counter = &c
		r.byKind[key] = counter
	}
	n := atomic.AddUint64(counter, 1) - 1
	base := kind.String()
	if h := strings.TrimSpace(hint); h != "" {
		base = base + "-" + h
	}
	if n == 0 {
		return Name(base), nil
	}
	return Name(base + "-" + strconv.FormatUint(n, 10)), nil
}

// assignReplica produces a replica name of the form
// "<parent>-r<ordinal>" unique within the pod and parent.
func (r *ordinalRegistry) assignReplica(p PodID, parent Name) (Name, error) {
	if parent == "" {
		return "", ErrNameCollision
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := ordinalKey{pod: p, name: "replica/" + string(parent)}
	counter, ok := r.byOwner[key]
	if !ok {
		var c uint64
		counter = &c
		r.byOwner[key] = counter
	}
	n := atomic.AddUint64(counter, 1) - 1
	return Name(string(parent) + "-r" + strconv.FormatUint(n, 10)), nil
}
