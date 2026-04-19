# FIX: Agent Identity and Token Accounting

A structural rebuild aligned with the existing Kubernetes-derived runtime
model. This document supersedes every current identity convention in the
codebase (`cfg.AgentID`, `SetAgentID`, `VisibleAgentID(…)`,
`RuntimeAgentID(…)`, `ReplicaHandoffAgentID(…)`, `"#replica-"` delimiters,
`":engineer"` suffixes, `canonical_agent_id` metadata keys, etc.) with a
typed container-level identity that fits the Pod / Container / Tier /
Namespace primitives already present in `core/container/*`.

No legacy shims. No compatibility layer. No parse-string fallbacks. All
on-disk learning and accounting state is reset at cutover; HANDOFF.md's
hierarchical priors handle cold-start within one turn per (type, model).

## Why this document exists

Token attribution is broken because agent identity is broken. Every
correctness defect in the accounting path descends from the same root
cause: there is no single source of truth for "who is this agent." There
are at least five competing ones, loosely related, each patched wherever
they disagreed — `cfg.AgentID`, internal `id string` fields, the stringly-
typed `AgentID() string` accessor, `req.Metadata["agent_id"]` (possibly
empty), and the TUI's `VisibleAgentID(five, parameters)` reconciliation
cascade. The handoff system has an `InstanceID` field that nothing
populates. The provider gateway reads identity from `map[string]any`
metadata and silently proceeds when it's absent.

Three classes of symptom:

1. **Tokens accrue to the wrong key.** A model swap mid-session emits two
   different effective configurations under the same string `agent_id`;
   downstream aggregators either lump them (losing per-model cost) or
   split unpredictably (losing per-agent totals).
2. **Tokens accrue to no key at all.** Several LLM call sites bypass the
   gateway (raw Anthropic/Google/OpenAI clients in Guide and Architect),
   and several others fail to stamp identity before dispatch. The
   resulting `OnResponse` events either never fire or fire with
   `agent_id=""` and are bucketed into a phantom "unknown agent" total.
3. **The GP learner fits on bad data.** HANDOFF.md's dynamic handoff
   wants per-(instance, model) curves. Instance IDs are not first-class
   runtime values. Whatever curve the GP fits is an average across
   whatever name the code happened to use — sometimes a panel string,
   sometimes a UUID suffix, sometimes a hardcoded literal.

The fix is a replacement of the identity and accounting substrate with
correct primitives, integrated with the Pod/Container model already in
place. Every piece of scaffolding that existed to cope with the old
representations is deleted.

## The Kubernetes-shaped runtime (what already exists)

The codebase is K8s-derived:

| Sylk primitive | K8s analog | Location |
|---|---|---|
| `pod.PodID` | `metadata.name` of a Pod | `core/container/pod/pod_types.go:12` |
| `pod.PodType` ∈ {Daemon, Singleton, Pipeline} | Controller kind (DaemonSet / scale=1 Deployment / Deployment with replicas) | same |
| `pod.PodPolicy` | PodSpec + controller policy | `core/container/pod/pod_policy.go` |
| `pod.ActivationTier` ∈ {Cold, Cool, Warm, Hot} | Scheduling / readiness state | `pod_types.go:59` |
| `shared.AgentPod` | Pod (runtime object) | `agents/shared/agent_pod.go:123` |
| `container.Container` | Container (runtime object) | `core/container/container.go:51` |
| `container.ContainerSpec` (has `Labels`, `AgentType`) | Container spec | `core/container/spec.go:94` |
| `network.NetworkNamespace` (per-pod) | Pod network namespace | `core/container/network/namespace.go` |
| Session ID | `metadata.namespace` | scattered — promoted below |
| Correlation ID | N/A (per-request trace id) | — |

The holes we are filling:

1. No `Container`-level **identity object**. `Container` has a `podID`
   and a `ContainerSpec` but no typed UID, no labels-derived selector,
   no link to the agent's current model/generation, and no link to the
   task being serviced. `AgentID() string` on each agent is a stringly-
   typed stand-in.
2. No **Job analog**. Sylk has "tasks" in prose and in
   `agents/orchestrator/task_router.go`, but no typed `Task` value object
   that an in-flight request carries alongside its container identity.
3. No **canonical namespace**. Sessions are the de facto namespace but
   aren't typed that way; session IDs are raw strings.
4. No **ownerReferences**. Replica agents reference their parent via a
   magic `"#replica-<corr>"` delimiter in a free-form string, and only
   for knowledge-agent replicas — pipeline-worker replicas have no
   parent link at all.
5. No **single telemetry consumer** (analog of a metrics adapter).
   `TopicActivity` carries `EventTypeLLMResponse` events but only the
   TUI bridge reads them, so totals exist only as long as the TUI
   process does.

## Current state (audit)

### Identity shapes in use today

| Term | Where set | Where read |
|---|---|---|
| `cfg.AgentID` (string) | `cmd/tui.go` bootstrap | Per-agent `id` field |
| `AgentID() string` accessor | Per-agent | Handoff supervisor, UI registry, steering ledger |
| `req.Metadata["agent_id"]` | `agents/shared/context_governor.go:stampRequestIdentity` | `core/providers/gateway/proxy.go:extractRequestIdentity` |
| `req.Metadata["runtime_agent_id"]` | Same (only when different) | UI `agentidentity`, orchestrator task_router |
| `agent_type` metadata | Stream metadata, forwarded request | UI, telemetry |
| `pipeline_id` / `task_id` metadata | Orchestrator task_router | UI panel resolver, `pipelineWorkerVisibleAgentID` |
| `handoff.AgentHandoffProfile.InstanceID` | Struct field | Nothing — never populated |

### Per-agent identity-source table

| Agent | Canonical ID source | Replica mechanism | Gateway bypass? | Swap event? |
|---|---|---|---|---|
| Guide | `cfg.AgentID`, no fallback check | Singleton | **Yes** — `guide.go:602` raw `anthropic.NewClient`; `classification.go:175` raw client; `guide.go:3639` raw `NewGoogleProvider` in auth refresh | `SwapModel` yes; no event |
| Architect | `cfg.AgentID` | Singleton | **Partial** — `planner_anthropic.go:225` raw `NewAnthropicProvider`, then `PlannerProviderWrapper`; stream path skips `ApplyContextBudget` so metadata never stamped → gateway fires hooks with `agent_id=""` | `SwapModel` yes; no event |
| Orchestrator | `cfg.AgentID`, defaults to `"orchestrator"` at `orchestrator.go:568-569` | Singleton | No | No |
| Archivalist | `cfg.ID`, falls back to `uuid.New().String()[:8]` at `archivalist.go:295` | Knowledge replica: `"archivalist#replica-<corr>"` at `handoff_replica.go:60` | No | `SwapModel` yes; no event |
| Librarian | `cfg.AgentID` | Knowledge replica (same pattern) | No | `SwapModel` yes; no event |
| Academic | `cfg.AgentID` + `a.id = id` setter | Knowledge replica | No | Not implemented |
| Engineer | `cfg.AgentID` + `e.id = id` setter | Pipeline worker: visible = `pipelineID + ":engineer"` at `context_governor.go:166` | No | `SwapModel` yes; no event |
| Designer | `cfg.AgentID` | Pipeline worker | No | `SwapModel` yes; no event |
| Inspector-global | `cfg.AgentID` | Singleton | No | `SwapModel` yes |
| Inspector-pipeline | `cfg.AgentID` | Pipeline worker | No | `SwapModel` yes |
| Tester-global | `cfg.AgentID` | Singleton | No | `SwapModel` yes |
| Tester-pipeline | `cfg.AgentID` | Pipeline worker | No | `SwapModel` yes |
| Guardian | Hardcoded literal `"guardian"` at `guardian.go:1306, 1328` | Singleton | No | Not implemented |
| Scribe | Correlation-scoped (per workstream) | N/A | No | N/A |

### Token-counting call graph today

Single counting point, multiple leak sites.

**What fires correctly:** any LLM call that goes through
`gateway.GatewayProvider` AND has `agent_id` stamped on
`req.Metadata`. The gateway calls `hook.OnRequest(sessionID, agentID,
req.Model, 0)` then `hook.OnResponse(sessionID, agentID, resp.Model,
&resp.Usage, elapsed)` (non-streaming at `gateway/proxy.go:98`; streaming
at `:273` handler path and `:293` channel path). The
`LLMEventPublisherHook` converts these to `EventTypeLLMResponse` activity
events on `TopicActivity`.

**Confirmed leak sites:**

1. Guide classification: `guide/classification.go:175` raw
   `anthropic.NewClient`. Classification tokens never reach the hook.
2. Guide direct-client path: `guide/guide.go:602` raw
   `anthropic.NewClient` in `NewWithConfig`. Guide's own tokens invisible.
3. Guide auth refresh: `guide/guide.go:3639` raw `NewGoogleProvider`.
4. Architect planner: `planner_anthropic.go:774, 781` streams via a
   gateway-wrapped provider but skips `ApplyContextBudget` — metadata is
   never stamped → `extractRequestIdentity` returns `("", "")` → hook
   fires with empty strings.
5. Guide self-response: `guide_self_response.go:247` calls
   `r.provider.Stream(streamCtx, req)` with no `ApplyContextBudget`.

**Per-model split silent:** `SwapModel` emits no event. Successive
`OnResponse` calls carry different `model` fields under the same
`agent_id`. Aggregators choose their own key and disagree.

**Per-replica attribution partial:** `handoff_replica.go` sets
`runtime_agent_id` for knowledge replicas only; pipeline-worker replicas
never set it. The hook signature doesn't pass `runtime_agent_id` as a
first-class field even when present — it's in the event `Data` map.

**OnRequest token count is always zero:** `gateway/proxy.go:80, 114, 193`
all pass `tokenCount: 0`. Pre-flight counter exists but is not wired in.

**No server-side accountant:** the only consumer of
`EventTypeLLMResponse` is `ui/bridge/token_usage.go`, which runs in the
TUI. Totals die with the UI process. No persistence. No replay.

**Usage-terminal-chunk fragility:** `wrapStreamHandler` only fires
`OnResponse` when `chunk.Type == ChunkTypeEnd && chunk.Usage != nil`.
Providers whose terminal usage arrives on a non-end chunk lose the event.

## The fix

Two new typed values, both K8s-shaped. A single factory constructs them.
A single accountant consumes them. No third path exists.

### 1. `AgentIdentity` — the container-level identity

Analogous to a Kubernetes `Container`'s (podName, namespace, containerName)
tuple plus a UID, plus the fields needed for provider/model/generation
bookkeeping.

```go
// core/agents/identity/identity.go
package identity

type UID string              // globally unique, UUIDv7 (sortable)
type Namespace string        // == SessionID
type Name string             // container name within pod
type AgentType uint8         // enum: Guide, Architect, Engineer, ...
type ModelID string          // registered model ID from handoff.Descriptor
type Generation uint64       // bumps on SwapModel; prior seals
type Category uint8          // Knowledge, Standalone, Pipeline
type ContextWindow uint32    // tokens

type PodRef struct {
    ID   pod.PodID    // points to an existing AgentPod's ID
    Type pod.PodType  // Daemon, Singleton, Pipeline
}

type OwnerRef struct {
    UID  UID
    Name Name
    Kind AgentType
}

type Labels map[string]string  // selector labels; subset is reserved:
                               //   sylk/kind, sylk/model, sylk/pod,
                               //   sylk/category, sylk/pipeline, sylk/task

// AgentIdentity is the single canonical identifier for a running agent
// container. Immutable once minted; a SwapModel produces a new
// AgentIdentity with Generation+1 sharing UID/Namespace/Pod/Name/Kind
// with the prior. Equality is structural. Never parsed from a string —
// always constructed via Factory.
type AgentIdentity struct {
    // K8s-shaped identity
    UID       UID         // unique, stable for this container's lifetime
    Namespace Namespace   // == session
    Pod       PodRef      // which pod this container runs in
    Name      Name        // container name within pod

    // Agent-specific spec
    Kind       AgentType
    Category   Category
    Model      ModelID
    Generation Generation
    Window     ContextWindow
    Labels     Labels

    // Owner / parenthood (K8s metadata.ownerReferences equivalent)
    // nil for canonical agents; set on replicas.
    Owner *OwnerRef
}
```

Factory-enforced invariants at construction:

- `Kind != AgentTypeUnspecified`
- `UID != ""` (UUIDv7, stable per container lifetime)
- `Namespace != ""`
- `Pod.ID` is a registered pod ID in the activation controller
- `Pod.Type` matches the pod registered for `Kind` (Daemon/Singleton/Pipeline)
- `Name` is unique within `(Namespace, Pod.ID)`
- `Model` is in the allowed set for `Kind` (from `handoff.agent_descriptors`)
- `Category` matches the descriptor's category for `Kind`
- Reserved labels `sylk/kind`, `sylk/model`, `sylk/pod`, `sylk/category`
  are auto-populated from the other fields and can't be overridden
  through the public API

Derivations (deterministic, pure, no string parsing):

- `Visible() string` — user-facing panel name (e.g. `"engineer"` for
  singletons; pipeline workers derived from the `Task` that dispatched
  them, not from the identity).
- `Panel() string` — UI panel key (`"<namespace>/<pod>/<name>"`).
- `Key() AccountingKey` — `(UID, Generation, Model)`.
- `Selector() labels.Selector` — K8s-style selector over `Labels`.
- `String() string` — debug only; never parsed.

### 2. `TaskRef` — the work-unit reference (K8s Job analog)

A `Task` is a unit of work that may cross multiple agent containers and
multiple LLM calls. Tasks are orthogonal to identity: the same container
may process many tasks over its lifetime; the same task may be handled
by multiple containers (pipeline dispatch, replica fan-out). Tasks have
their own identity distinct from the containers that run them.

```go
// core/agents/identity/task.go
package identity

type TaskUID string                 // unique per task
type CorrelationID string           // per-request trace id
type PipelineID string              // grouping of related tasks
type PipelineStage string           // which DAG layer / phase

type PipelineRef struct {
    ID    PipelineID
    Stage PipelineStage
    // Parent TaskUID when this task was spawned from another.
    Parent TaskUID
}

// TaskRef describes the unit of work currently being serviced. Carried
// on request contexts; never on an AgentIdentity. A container has an
// identity; a request carries (identity, task).
type TaskRef struct {
    UID         TaskUID        // unique identifier for this task
    DisplayID   string          // human-readable slug
    Pipeline    *PipelineRef   // nil for standalone tasks
    Correlation CorrelationID  // current request's correlation
    Session     Namespace       // matches the identity's namespace
    Labels      Labels
}
```

Factory-enforced invariants:

- `UID != ""`
- `Session != ""`
- `Correlation != ""`
- `Pipeline != nil ⇒ Pipeline.ID != ""`

Derivations:

- `Visible() string` — panel-facing task string
- `Selector() labels.Selector`
- `IsPipelineStep() bool`

The user's pipeline-worker panel identity (`engineer` dispatched for
`task-abc` in pipeline `p1`) is **derived at display time** from
`(identity.Kind, task.Pipeline, task.UID)`, not carried as a fifth
encoding on the identity itself. The old `pipelineWorkerVisibleAgentID`
string-concatenation scheme goes away entirely.

### 3. Factory

```go
// core/agents/identity/factory.go

type Factory struct {
    clock          func() time.Time
    uuidFn         func() UID
    registry       *ModelRegistry  // model ∈ allowed(kind)
    podRegistry    PodRegistry     // pod must exist in activation controller
    namespace      Namespace       // session
}

type MintOptions struct {
    Kind     AgentType
    Pod      PodRef
    Name     Name
    Model    ModelID
    Labels   Labels
}

type MintReplicaOptions struct {
    Parent *AgentIdentity
    // If nonzero, this replica's pod differs from parent (e.g. a pipeline
    // replica spawned into the pipeline pod by a knowledge agent).
    Pod *PodRef
    // Additional labels for the replica (merged with inherited).
    Labels Labels
}

func (f *Factory) Mint(opts MintOptions) (*AgentIdentity, error)
func (f *Factory) MintReplica(opts MintReplicaOptions) (*AgentIdentity, error)
func (f *Factory) WithNewGeneration(prior *AgentIdentity, newModel ModelID) *AgentIdentity

// Task construction
func (f *Factory) NewTask(opts TaskOptions) (*TaskRef, error)
func (f *Factory) SubTask(parent *TaskRef, opts SubTaskOptions) (*TaskRef, error)
```

`AgentIdentity` and `TaskRef` are both immutable once constructed.
`WithNewGeneration` returns a new value — the agent atomically swaps its
held pointer.

### 4. Propagation contract

Both identity and task flow via context:

```go
// core/agents/identity/context.go

// Identity
func WithIdentity(ctx, *AgentIdentity) context.Context
func IdentityFromContext(ctx) (*AgentIdentity, bool)
func RequireIdentity(ctx) (*AgentIdentity, error)

// Task
func WithTask(ctx, *TaskRef) context.Context
func TaskFromContext(ctx) (*TaskRef, bool)
func RequireTask(ctx) (*TaskRef, error)
```

A provider call without an `AgentIdentity` on ctx returns an error
immediately. Lack of a `TaskRef` on ctx is also an error for
agent-serviced calls; system-internal calls (OAuth refresh, health
probes) use an explicit `identity.SystemIdentity()` that is
account-neutral but still flows through the gateway.

### 5. Gateway contract (rewritten)

```go
// core/providers/gateway/gateway.go

func (g *ProviderGateway) Admit(
    ctx context.Context,
    id *identity.AgentIdentity,
    task *identity.TaskRef,
    priority RequestPriority,
) error

func (p *GatewayProvider) Generate(
    ctx context.Context,
    id *identity.AgentIdentity,
    task *identity.TaskRef,
    req *Request,
) (*Response, error)

func (p *GatewayProvider) Stream(
    ctx context.Context,
    id *identity.AgentIdentity,
    task *identity.TaskRef,
    req *Request,
) (<-chan *StreamChunk, error)

func (p *GatewayProvider) StreamWithHandler(
    ctx context.Context,
    id *identity.AgentIdentity,
    task *identity.TaskRef,
    req *Request,
    h StreamHandler,
) error
```

The gateway passes `id + task` into the hook:

```go
// core/providers/event_publisher.go

type LLMProviderEventHook interface {
    OnRequest(id *identity.AgentIdentity, task *identity.TaskRef, estimatedTokens int)
    OnResponse(id *identity.AgentIdentity, task *identity.TaskRef, usage *Usage, elapsed time.Duration)
    OnError(id *identity.AgentIdentity, task *identity.TaskRef, err error)
}
```

`Request.Metadata` loses every identity-related key entirely — metadata
returns to being opaque, caller-defined data.

### 6. Accountant

```go
// core/llm/accounting/accountant.go

type AccountingKey struct {
    ContainerUID identity.UID
    Generation   identity.Generation
    Model        identity.ModelID
    TaskUID      identity.TaskUID
}

type AggregatedUsage struct {
    Input, Output         int64
    CacheRead, CacheWrite int64
    Reasoning             int64
    RequestCount          int64
    ErrorCount            int64
    FirstAt, LastAt       time.Time
}

// Primary views — each is a map reducer over the canonical table.
func (a *Accountant) ByKey(k AccountingKey) AggregatedUsage
func (a *Accountant) ByContainer(id UID) map[Generation]map[TaskUID]AggregatedUsage
func (a *Accountant) ByGeneration(id UID, gen Generation) AggregatedUsage
func (a *Accountant) ByKind(k AgentType) AggregatedUsage
func (a *Accountant) ByModel(m ModelID) AggregatedUsage
func (a *Accountant) ByPod(p pod.PodID) AggregatedUsage
func (a *Accountant) ByNamespace(n Namespace) AggregatedUsage
func (a *Accountant) ByTask(t TaskUID) AggregatedUsage
func (a *Accountant) ByPipeline(p PipelineID) AggregatedUsage
func (a *Accountant) BySelector(sel labels.Selector) AggregatedUsage
func (a *Accountant) All() []AccountingSnapshot

// Stream — single subscriber channel used by TUI, handoff GP, cost
// reporters, alerting, etc.
func (a *Accountant) Subscribe() <-chan UsageDelta
```

The accountant is the registered `LLMProviderEventHook` on every
gateway. Writes go to an in-memory table guarded by a single RWMutex
and fan out to the WAL + subscriber channel. WAL lives in
`.sylk/sessions/{namespace}/accounting/` — crash-recoverable.

`ui/bridge/token_usage.go` becomes a thin subscriber that forwards
deltas to Bubble Tea; TUI no longer aggregates. All downstream token
consumers read from the accountant's views.

### 7. Model swap semantics

`SwapModel(ctx, newModel, newProvider)` on any agent:

1. Calls `factory.WithNewGeneration(current, newModel)` — returns a new
   `*AgentIdentity`. Atomically swap the agent's held pointer.
2. Emits a `ModelSwap` event with `{from: *AgentIdentity, to:
   *AgentIdentity}`.
3. New requests use the new identity; in-flight requests finish under
   the prior identity (their AccountingKey includes the prior
   Generation).
4. Handoff system observes ModelSwap, seals the GP observation stream
   for `(UID, prior_Gen, prior_Model)`, starts a new stream for
   `(UID, new_Gen, new_Model)`.
5. Accountant treats the generation boundary as a hard split — old and
   new tokens never share a key.

### 8. Replica semantics

Knowledge-agent replicas (request-scoped per HANDOFF.md) are child
identities. `MintReplica` produces:

- fresh `UID`
- inherited `Namespace`, `Pod`, `Kind`, `Category`, `Window`
- `Generation = 0` (replica starts fresh)
- `Owner = OwnerRef{parent.UID, parent.Name, parent.Kind}`
- `Model` inherited from parent unless explicitly overridden
- `Name` = `parent.Name + "-r" + ordinal` (replica ordinal within
  parent's lifetime), deterministic and unique within the pod

Pipeline-worker replicas (per-task engineer/designer/tester-pipeline
/inspector-pipeline dispatches) are full `Mint` constructions with
`Pod = {pipelinePodID, PodTypePipeline}` and `Labels["sylk/task"]` set
from the dispatched `TaskRef.UID`. Each dispatch is its own Container
(its own identity); the pipeline pod hosts many such containers
concurrently.

## Action list — what gets done

### A. New core packages

1. Create `core/agents/identity/` package:
   - `identity.go` — `AgentIdentity`, `UID`, `Name`, `Namespace`,
     `AgentType`, `ModelID`, `Generation`, `Category`, `PodRef`,
     `OwnerRef`, `Labels`, all derivations.
   - `task.go` — `TaskRef`, `TaskUID`, `PipelineRef`, `PipelineID`,
     `PipelineStage`, `CorrelationID`, derivations.
   - `factory.go` — `Factory`, `Mint`, `MintReplica`,
     `WithNewGeneration`, `NewTask`, `SubTask`, `ModelRegistry`,
     `PodRegistry` interface.
   - `context.go` — `WithIdentity`/`RequireIdentity`/`FromContext` plus
     task analogs.
   - `selector.go` — K8s-style `labels.Selector` over `Labels`.
   - `system.go` — `SystemIdentity()` for account-neutral system calls
     (OAuth refresh, probes). Runs through gateway but explicitly
     tagged so the accountant skips billing.
2. Create `core/llm/accounting/`:
   - `accountant.go` — aggregation, all view reducers.
   - `wal.go` — session-scoped WAL format + replay.
   - `delta.go` — `UsageDelta` record.
   - `accountant_test.go` — per-container, per-generation, per-task,
     per-model, per-pod, per-namespace; swap boundary; parallel
     replicas; all three providers; stream/non-stream/error; WAL
     replay.

### B. Rewrite hook + gateway

3. `core/providers/event_publisher.go` — `LLMProviderEventHook`
   methods take `(*AgentIdentity, *TaskRef, ...)`. Update
   `LLMEventPublisherHook`, `NoOpLLMEventHook`, tests.
4. `core/providers/gateway/proxy.go` — all three dispatch methods
   accept `(*AgentIdentity, *TaskRef)` as explicit parameters. Delete
   `extractRequestIdentity`.
5. `core/providers/gateway/gateway.go:Admit` accepts identity + task.
6. Delete every `req.Metadata["agent_id"|"session_id"|
   "runtime_agent_id"|"canonical_agent_id"|"agent_type"|
   "pipeline_id"|"task_id"]` access in the gateway and downstream.
   Metadata returns to being opaque caller data.
7. `core/events/activity_types.go` — `ActivityEvent.AgentID string`
   and `AgentType string` replaced by `Identity *AgentIdentity` and
   `Task *TaskRef`. All publishers in `core/events/publishers.go`
   updated to take typed identities.

### C. Delete old identity scaffolding

8. Delete `agents/shared/context_governor.go:stampRequestIdentity`,
   `requestIdentityForContext`, `pipelineWorkerVisibleAgentID`.
9. Delete `ui/agentidentity/identity.go` entirely — every function is
   replaced by methods on `*AgentIdentity` and `*TaskRef`.
10. Delete `agents/shared/handoff_replica.go:ReplicaHandoffAgentID`,
    the `"#replica-"` delimiter, the associated string parsing in
    callers.
11. Delete `cfg.AgentID string` on every agent Config. Replace with
    `cfg.Identity *identity.AgentIdentity`.
12. Delete every `SetAgentID(string)` setter.
13. Delete every `cfg.AgentID == ""` fallback.
14. Delete `archivalist.go:295`'s `uuid.New().String()[:8]` fallback.
15. Delete hardcoded literals: `guardian.go:1306, 1328`;
    `orchestrator.go:568-569`.
16. Delete `AgentID() string` accessors; replace with `Identity()
    *AgentIdentity` where the interface requires it.

### D. Bootstrap (cmd/tui.go)

17. At session start, construct:
    - `identity.Namespace` = session ID (typed).
    - `pod.PodRegistry` adapter over `activation.ActivationController`.
    - `identity.ModelRegistry` loaded from
      `core/handoff/agent_descriptors.go`.
    - `identity.Factory` bound to the above.
    - `accounting.Accountant` with WAL path
      `.sylk/sessions/{namespace}/accounting/`.
18. Wire the accountant as the **sole** `LLMProviderEventHook` on all
    three gateways. Remove the old `LLMEventPublisherHook` +
    `TopicActivity`-based flow for token events. Non-token activity
    (routing, tool calls, agent state) remains on `TopicActivity`.
19. For every agent, mint `*AgentIdentity` via
    `factory.Mint({Kind, Pod, Name, Model, Labels})`. The `Pod` is
    looked up in the pod registry from the activation controller —
    agents under `pod.PodTypePipeline` share the same pod, agents
    under `PodTypeSingleton` each get their own.
20. Pass `cfg.Identity` into each agent constructor.

### E. Per-agent surgery

For each agent (Guide, Architect, Orchestrator, Archivalist,
Librarian, Academic, Engineer, Designer, Inspector-Global,
Inspector-Pipeline, Tester-Global, Tester-Pipeline, Guardian, Scribe):

21. Replace `cfg.AgentID string` with `cfg.Identity *identity.AgentIdentity`.
22. Replace internal `id string` with `identity atomic.Pointer[identity.AgentIdentity]`
    (atomic so `SwapModel` is racing-safe for in-flight request reads).
23. Replace `AgentID() string` with `Identity() *identity.AgentIdentity`.
24. Replace every string-keyed usage (`a.id`, `e.id`, …) with
    appropriate derivations off the current identity.
25. Replace `ApplyContextBudget` with the decomposed pair:
    - `agent.Dispatch(ctx, task, req)` — attaches identity + task to
      ctx, runs context governor, invokes gateway with typed args.
    - `context_governor.ApplyBudget(ctx, turn, maxRuns, req)` —
      budget only; no identity side effects.
26. Every `SwapModel` calls `factory.WithNewGeneration`, atomically
    swaps the identity pointer, emits `ModelSwap` event.

### F. Fix the three Guide raw-client bypasses

27. `agents/guide/classification.go:175` — replace raw
    `anthropic.NewClient` with a gateway-wrapped provider constructed
    via the same path the rest of Guide uses. Classification LLM
    calls now flow through the accountant.
28. `agents/guide/guide.go:602` — delete the direct `anthropic.NewClient`
    path in `NewWithConfig`. Guide constructs only via `New` /
    `NewWithProvider`, both of which receive gateway-wrapped providers.
29. `agents/guide/guide.go:3639` — the OAuth refresh uses
    `identity.SystemIdentity(Kind=SystemOAuthRefresh)` for its gateway
    dispatch. Accountant sees it (observable, countable) but marks it
    as non-billable against any agent.

### G. Fix the three metadata-stamping gaps

30. `agents/architect/planner_anthropic.go:774, 781` — planner
    streaming routes through `agent.Dispatch(ctx, task, req)` first,
    so identity + task are on ctx; gateway receives them via the new
    typed arguments.
31. `agents/guide/guide_self_response.go:247` — same treatment.
32. Remove `PlannerProviderWrapper` function in favor of the uniform
    gateway path. The Architect planner uses the same dispatch
    contract as every other agent.

### H. Pipeline workers + Tasks

33. Orchestrator's `task_router` is the primary `TaskRef` source. On
    every DAG-node dispatch:
    - `task := factory.NewTask({Pipeline: {ID: pipelineID, Stage:
      layerName, Parent: parentTaskUID}})` or a top-level
      `factory.NewTask({})`.
    - For pipeline workers (engineer, designer, inspector-pipeline,
      tester-pipeline): `id := factory.Mint({Kind, Pod:
      {ID: pipelinePodID, Type: PodTypePipeline}, Name: "<kind>-<task_uid>",
      Labels: {"sylk/task": task.UID, "sylk/pipeline": pipelineID}})`.
    - The forwarded request carries both via typed fields.
34. `ForwardedRequest` grows typed fields:
    - `Identity *identity.AgentIdentity`
    - `Task     *identity.TaskRef`
    Remove the string-form `SourceAgentID`, `SourceAgentName`, and
    metadata-based task/pipeline keys.
35. `RouteRequest` likewise: source becomes `*AgentIdentity`, request's
    associated task becomes `*TaskRef` (optional for top-level user
    requests at the Guide).
36. Guide mints a top-level `*TaskRef` at user-request ingress
    (`factory.NewTask({Correlation, Session, DisplayID: inputSlug})`)
    — every downstream agent inherits it unless it sub-tasks.

### I. Knowledge replicas

37. `agents/shared/handoff_replica.go:AttachReplicaHandoffBridge` takes
    a factory, mints a child via `factory.MintReplica({Parent,
    Labels: {"sylk/correlation": corrID}})`, registers with the
    handoff supervisor, attaches to request ctx.
38. `handoff.AgentHandoffProfile.InstanceID` is populated from
    `identity.UID`.
39. Handoff supervisor keys its registry on `identity.UID`, not on a
    string name.

### J. Handoff integration (HAND-01 through HAND-09)

40. `HAND-01` — `HandoffSupervisor` is instantiated in bootstrap.
    Each agent auto-registers a profile keyed on its factory-minted
    `UID`.
41. `HAND-02` — `HandoffController.ShouldTakeAction` keys on
    `identity.Key()`. `HierarchicalParamBlender.RegisterAgentModel`
    keys on `(Kind, Model)`. Per-instance posteriors key on `UID`.
    Observations flow in every turn.
42. `HAND-03` — `PreparedContext.Update(turn, files)` is called from
    `agent.Dispatch` — same single choke point as identity attach.
43. `HAND-04` — `LearnedCount` (Poisson-Gamma) implemented
    independently; no identity coupling.
44. `HAND-05` — Archivalist `ArchiveRequest` carries `*AgentIdentity`
    and `*TaskRef`; archivalist files by UID + TaskUID.
45. `HAND-06` — handoff preserves correlation via the task's
    `Correlation` field and the identity's `UID`.
46. `HAND-07` — WAL entries key on `(UID, Generation, Model)` for
    profile state and `(UID, Generation, Model, TaskUID)` for
    accounting state.
47. `HAND-08` — `ContextCheckHook` registered in `agent.Dispatch`
    wrapper.
48. `HAND-09` — `OptimalPreparedSize` learned per `(UID, Model)`;
    `PreparedContext.TrimToSize` reads from the current identity's
    profile.

### K. UI

49. `ui/agent/model.go` keys the agent panel registry on `UID`, not
    on reconciled strings. Display derives from `identity.Panel()`
    or `task.Visible() + ":" + identity.Kind.String()` depending on
    mode.
50. `ui/bridge/token_usage.go` subscribes to
    `accountant.Subscribe()`; forwards deltas as a typed
    `TokenDeltaMsg` carrying `*AgentIdentity` and `*TaskRef`.
51. Agent panel displays `(Kind, Model)` as header,
    `Generation + UID[:8]` as diagnostic sub-line.
52. Status bar reads `accountant.ByNamespace(sessionNS).TotalTokens()`.

### L. Telemetry

53. Remove the separate `TokenUsageMsg` bespoke path. Token telemetry
    flows through the accountant only.
54. Every log site that embeds `agent_id` uses `slog.Any("identity",
    id)` and `slog.Any("task", task)`. The handler renders with the
    K8s-shaped `Panel()` form.
55. Activity events lose their `Data["agent_id|agent_type|
    runtime_agent_id|canonical_agent_id|pipeline_id|task_id"]`
    pollution — the `ActivityEvent.Identity` + `Task` fields carry
    this typed.

### M. Message envelope

56. `messaging.Message[T].SessionID string` becomes a derived view
    over `Identity.Namespace`. For over-the-wire JSON, it's marshaled
    for human-readability; Go consumers read from `Identity`.
57. WAL formats (orchestrator, architect, archivalist, durable
    protocol log) serialize `*AgentIdentity` / `*TaskRef` as typed
    embedded objects with stable JSON schema.

### N. Tests

58. Every agent constructor test: `a.Identity()` non-nil; UID,
    Namespace, Pod, Name, Kind, Model all set.
59. Integration test: full bootstrap, forwarded request per agent,
    assert every `OnResponse` has a matching identity+task, and
    `accountant.All()` totals match sum of provider-reported usage.
60. Swap test: `SwapModel` twice mid-session per agent; assert three
    generation entries with disjoint totals in
    `accountant.ByContainer(UID)`.
61. Replica test: three parallel knowledge replicas of one agent;
    each has its own UID with `Owner.UID == parent.UID`;
    `accountant` shows them as four buckets (parent + three children)
    with a roll-up view via `ByKind`.
62. Task test: one pipeline, five tasks, four pipeline workers;
    accountant gives per-task breakdown, per-pipeline roll-up,
    per-worker-kind roll-up — all three consistent.
63. Race test: 100 concurrent requests per agent; no attribution
    crossover.
64. Property test: for any sequence of `Mint | MintReplica |
    WithNewGeneration | NewTask | SubTask | Dispatch`, accountant
    total tokens equals sum of provider-reported usage.

### O. Documentation

65. `docs/ARCHITECTURE.md` gets an Identity + Accounting section.
    Invariants documented. Factory is the only construction path.
    Accountant is the only aggregator. K8s mapping table is first-
    class (mirrors this doc's "K8s-shaped runtime" section).
66. `docs/HANDOFF.md` cross-refs `AgentHandoffProfile.InstanceID`
    to `identity.AgentIdentity.UID`.
67. `docs/DURABLE_PROTOCOLS.md` updates WAL record descriptions to
    include typed identity/task fields.
68. Auto-memory entries about `cfg.AgentID`, `SetAgentID`,
    `VisibleAgentID`, `runtime_agent_id`, `canonical_agent_id`,
    `"#replica-"` are rewritten or removed.

### P. Cutover

69. All on-disk learning state (HANDOFF GP WAL, accountant WAL,
    archivalist handoff archives, durable protocol logs) is
    invalidated at cutover. Fresh start. HANDOFF.md's priors handle
    cold-start within ~1 turn per (Kind, Model).
70. Any session directory referencing old identity encodings is
    truncated, not migrated.
71. Retire `agents/shared/handoff_replica.go` (old helpers),
    `ui/agentidentity/identity.go`, the `#replica-` delimiter, the
    `":engineer"` suffix convention, `pipelineWorkerVisibleAgentID`,
    `ReplicaHandoffAgentID`, `stampRequestIdentity`. All code
    referencing these is deleted, not deprecated.

## Non-negotiables

- No `AgentID string` / `agent_id string` anywhere in agent
  construction, runtime, or message envelopes. The only string form is
  for log output and is produced by `identity.AgentIdentity.String()`.
- No fallback to a default identity when one is missing. Missing
  identity at a provider call site is an error that aborts the call.
- No parsing of identity from a string. All identity flows come from
  the factory, carry through context / typed message fields, and exit
  through typed accessors.
- No gateway bypass that isn't tagged with `identity.SystemIdentity()`.
  OAuth refresh is the only currently identified legitimate exemption.
- No per-subsystem aggregator. One accountant owns token totals; UI,
  handoff, and future cost/billing/alerting read from it.
- Identity and Task are orthogonal. An identity never carries
  task-scope fields (Pipeline, Task, Correlation). A task never
  carries container-scope fields (Pod, Kind, Model, Generation).
- Labels conform to the K8s convention: prefixed keys (`sylk/...`) are
  reserved; callers supply only unprefixed custom labels.

## Sequencing

One coordinated change, not an interleaved migration. Intermediate
compile states are desirable — each removed representation of identity
reveals downstream code that depends on it, and we want the compiler
to flag that explicitly.

Order within the PR for review sanity:

1. Ship packages in A (identity + accountant) with tests, no wiring
   changes yet.
2. Rewrite hook + gateway + activity event shape (B). Everything
   downstream breaks.
3. Delete old scaffolding (C). Everything that was implicit becomes a
   compile error.
4. Sweep per-agent surgery E (mechanical, same pattern per agent).
5. Fix Guide bypasses F. Fix metadata gaps G.
6. Pipeline workers + Tasks (H). Replicas (I).
7. Handoff integration (J) — payoff shows up.
8. UI + telemetry + envelope cleanup (K + L + M).
9. Bootstrap wiring (D) is actually done together with 4–8 because the
   factory must exist before any agent constructor succeeds.
10. Cutover P.

The intermediate failures during steps 2–6 surface exactly the places
where identity used to be implicit — that's the audit this PR needs to
produce.

## Verification

The refactor is complete when all of these hold:

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test -race ./...` passes.
- `rg 'cfg\.AgentID'` across production code returns zero hits.
- `rg 'SetAgentID\('` returns zero.
- `rg 'runtime_agent_id|canonical_agent_id'` returns zero.
- `rg 'stampRequestIdentity|VisibleAgentID\(|ReplicaHandoffAgentID|
  pipelineWorkerVisibleAgentID'` returns zero.
- `rg '#replica-'` returns zero outside tests that assert the
  delimiter no longer exists.
- `rg 'AgentID\(\)\s+string'` returns zero outside interface
  definitions retained for compatibility with `HandoffableAgent`
  (which itself should take a typed identity by this point).
- Integration test: a full bootstrap smoke run produces an
  `accountant.All()` snapshot whose total tokens equal the sum of
  provider-reported usage; no `AccountingKey` has empty UID; no
  `Kind` is missing from the per-kind breakdown; every LLM call
  surfaces a matching `(Identity, Task)` tuple.
- Swap test: `SwapModel` on each swap-capable agent increments that
  agent's Generation by exactly 1; tokens accrue to the new
  generation; the accountant surfaces both generations with disjoint
  totals.
- Replica test: forwarded knowledge-agent requests produce child
  identities with `Owner.UID == parent.UID`; the accountant shows
  parent and children as distinct buckets with a roll-up view via
  `ByKind`.
- Pipeline test: a pipeline with N task dispatches produces N
  pipeline-worker container identities, each with a distinct `UID`,
  shared `Pod.ID == pipelinePodID`, and a `Labels["sylk/task"]`
  matching the dispatched `TaskRef.UID`. The accountant's
  `ByTask(taskUID)` sums the tokens of all workers that serviced
  that task, across all pipeline stages.
- Task test: a multi-stage pipeline produces a `TaskRef` tree
  (parent task + sub-tasks per stage). `accountant.ByPipeline()`
  returns a sum that equals the summation of `ByTask()` over every
  task in that pipeline.

If any of these fails, the change isn't done.
