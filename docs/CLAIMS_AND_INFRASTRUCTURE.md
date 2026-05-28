# Claims and Infrastructure

This document defines the universal participant model for Sylk: how every
deterministic subsystem — VFS volumes, knowledge graph, document DB, DAG
processor, guardian gates, identity registry, activation controller, boot
sequencer, tool runtime, LLM provider gateway, and so on — participates in
the claims plane as a first-class issuer, subject, and evaluator of claims,
testaments, and validations.

The motivating observation is that the existing claims documents
(`CLAIMS.md`, `CLAIMS_AND_DELTAS.md`, `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`,
`CLAIMS_VISIBILITY.md`) phrase the system as agent-driven by default. The
data model is participant-agnostic at the protocol level, but the prose
reads "an agent issues a claim against another agent." That language has
encouraged infrastructure subsystems to live outside the claims plane,
producing Go-error return paths, opaque internal state, and outcomes that
agents cannot see, reason about, or react to.

This document closes that gap. It does not introduce a second event bus,
a parallel board, a new transport, a separate validator authority, a
shadow identity registry, or an alternate evidence channel. It widens the
participant taxonomy, formalizes a programmatic validation discipline
alongside the existing agentic one, defines a service-handler dispatch
path alongside the existing agent ClaimsInbox path, and reconciles the
phrasing of the existing claims documents so the entirety of Sylk is one
coherent claims-and-deltas system.

## 1. Purpose

The claims plane should be the universal coordination primitive for every
mutation of board-visible state in Sylk, regardless of whether the actor
performing the mutation is an LLM agent, a deterministic Go service, or
a structural runtime emitter.

### 1.1 The Agent-Centric Bias

Re-reading the existing claims documents, the agent-centric framing
appears throughout:

- `CLAIMS.md §2.3` describes a claim as "issued by one agent against
  another."
- `CLAIMS.md §4.2` names the universal base field `AgentID`.
- `CLAIMS.md §5` says "every directed emission an agent performs is one
  of four async skills."
- `CLAIMS_AND_DELTAS.md §7` defines a "Canonical Agent Reference."
- `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §8` structures receiver semantics
  around "Target Agent" and "Source Agent."
- `CLAIMS_VISIBILITY.md §3.3` says "agents do not need an audience flag
  to access evidence."

None of these passages is formally restrictive. The `Relations` system
does not require `issuer` or `subject` to point at an agent. The
`Testament` and `Artifact` types do not encode an agent-only producer.
The validation flow does not encode an agent-only evaluator. The board
amplifier does not encode an agent-only emitter.

But the language has shaped the implementation. Today's infrastructure
systems — VFS provisioning, knowledge graph writes, document DB ingestion,
guardian decisions outside conversation, DAG allocation, identity
allocation, activation transitions, boot phases, tool runtime executions,
provider gateway calls — do not produce claims, do not produce testaments,
and do not respond to deltas. Their outcomes are Go errors, channel
closures, status fields on internal structs, and log lines.

### 1.2 The Cost of the Gap

When infrastructure outcomes are not claims-plane facts:

1. Agents cannot `traverse` to find out whether infrastructure work
   succeeded. The board has no record of it.
2. Agents cannot `query_claims_board` for "what happened when we tried
   to provision pipeline P." The result lives in logs, not on the board.
3. The Memory Forest cannot harvest infrastructure precedents. Successful
   pipeline allocations, VFS migrations, and KG writes are not branches
   in the forest, even though they are exactly the precedents the next
   session needs.
4. Replay cannot reconstruct infrastructure state at incident time. The
   board has the agent's perspective; the infrastructure's perspective is
   discarded.
5. Failures degrade to Go errors that travel up call stacks and die in
   log lines. Agents see "the tool returned an error" rather than "the
   VFS provisioner rejected the request because quota was exceeded, and
   here is the quota artifact and the policy reference."
6. The UI bridge has special-case paths for every infrastructure outcome
   it needs to render. The unified rendering contract described in
   `CLAIMS_VISIBILITY.md` does not apply because infrastructure outcomes
   never reach the bridge as testaments or artifacts.
7. Validators cannot evaluate infrastructure outcomes against quality
   bars. There is no "the pipeline allocated successfully and has N
   healthy pods" validation because there is no `pipeline_pod_state`
   artifact.
8. Determinism and idempotency are recovered ad hoc per subsystem rather
   than centrally. Each subsystem invents its own dedup and replay
   semantics.

### 1.3 The Universal Participant Goal

The goal of this document is to define the model where:

1. Every Sylk subsystem that mutates board-visible state is a
   *participant* — an entity with canonical identity, claims-board
   addressability, and durable testament emission.
2. Every mutation produces a claim (or fulfills one) and is answered by
   a testament with artifacts that capture the outcome.
3. Every outcome — success, partial success, refusal, impossibility,
   timeout, policy denial, infrastructure error — is an artifact, not a
   Go error.
4. Validation can be performed programmatically (deterministic Go code
   inspecting artifacts) or agentically (LLM reasoning about evidence)
   without changing the wire shape, the delta envelope, the UI rendering,
   or the replay path.
5. The board is the single durable source of truth for every workflow
   fact, infrastructure or otherwise.

## 2. Non-Negotiable Semantics

These semantics are load-bearing for the rest of the design. They override
any conflicting phrasing in the existing claims documents and are the
authority during reconciliation.

### 2.1 Participants Are Not All Agents

A claim is a directed assertion or work item issued by one *participant*
against another. A participant may be an agent (LLM-driven), a service
(deterministic), a system (structural runtime emitter), or external (the
user, an external CI, a deploy controller).

Every passage in the existing claims documents that says "agent" in a
context that could apply to any participant is reinterpreted to say
"participant." Passages that are specifically about LLM-driven
participants — tool loops, conversation, accumulator flush, prompt
construction — remain agent-specific.

### 2.2 The Wire Format Is Participant-Agnostic

Testaments, artifacts, validations, and deltas have one wire format
regardless of producer category. A testament from a deterministic VFS
provisioner uses the same envelope as a testament from an LLM-driven
architect agent. Validators and observers cannot distinguish them and
must not branch on category for protocol-level behavior.

Category appears in identity references (`ParticipantRef.Category`) and
in routing-layer subscription patterns. It does not appear in the
delta envelope's primary action, in the testament's shape, in the
validation outcome verdicts, or in any UI rendering rule.

### 2.3 Validation Can Be Programmatic or Agentic

`CLAIMS.md §2.4` already acknowledges that receipt validations are
mechanical. This document widens that acknowledgment: every validation
type may be evaluated programmatically when a registered programmatic
validator exists for the claim action and validation type. The fallback
is agentic evaluation by an evaluator participant.

Programmatic validation is preferred for mechanical quality bars (capacity
comparisons, identity resolution, content-hash checks, status equality,
schema validation). Agentic validation remains required for judgment
quality bars (code review, design review, plan inspection, conflict
assessment, ambiguity resolution).

Both produce identical `validation.evaluated` deltas with identical
verdict shapes. Downstream consumers cannot tell which evaluator ran and
must not branch on that distinction.

### 2.4 Infrastructure Outcomes Are Testaments With Artifacts

Every deterministic subsystem that today returns a Go error, sets an
internal status field, or closes a channel must instead produce a
testament with artifacts. The Go return path is preserved as a local
control-flow convenience, but the workflow-truth representation is the
durable testament on the board.

This rule generalizes the errors-as-artifacts principle from `CLAIMS.md`
to all infrastructure outcomes, not only failures.

### 2.5 The Board Is Still the Source of Truth

Infrastructure participants commit to the board, emit deltas through the
Guide event bus, and respond to claims like any other participant. They
do not maintain a parallel store, a parallel event channel, or a parallel
status surface. If an infrastructure outcome is observable, it is
observable as board state.

### 2.6 Identity Is Universal

Every participant has canonical identity in the same shape as agent
identity: `(uid, namespace, pod, name, type, generation, model_or_version,
scope_keys)`. Service identities derive deterministically from
(service_type, scope_keys) so the same logical service in the same
session always resolves to the same UID across restarts.

### 2.7 Replay Reconstructs Every Perspective

Replay must reconstruct both agent-perspective and infrastructure-
perspective board state from the WAL. The same claim, the same testament,
the same artifacts, the same validation verdicts, the same lifecycle
status history, the same delta keys.

### 2.8 No Untracked Goroutines, No Unbounded Queues, No Silent Drops

Service handler dispatch, programmatic validator dispatch, and the
runtime support for both must obey the project-wide concurrency
discipline: every goroutine is owned by a tracked `core/concurrency`
scope, every queue is bounded with explicit overflow telemetry, no drop
is silent, every cancellation produces a cancellation artifact, and
no resource grows without bound.

## 3. Vocabulary

This section establishes the vocabulary used throughout the rest of the
document. The terms align with the existing claims documents where
overlap exists and extend them where new concepts appear.

### 3.1 Participant

A *participant* is any entity that can issue, receive, or evaluate
claims, testaments, validations, or artifacts. Participants have
canonical identity, claims-board addressability, and durable testament
emission.

### 3.2 Participant Category

A *participant category* classifies how a participant consumes claims,
produces testaments, and integrates with the runtime.

| Category | Consumption | Production | Latency | Replay |
|---|---|---|---|---|
| `agent` | ClaimsInbox + LLM tool loop | Accumulator flush at turn end | Seconds–minutes | Deterministic up to LLM nondeterminism |
| `service` | Handler registry + Go function dispatch | Synchronous or async handler return | Microseconds–seconds | Fully deterministic when inputs are deterministic |
| `system` | Structural runtime hooks | Direct testament submission from runtime events | Microseconds | Fully deterministic |
| `external` | Outside Sylk | Inbound via gateway adapter producing claims/testaments | Variable | Reconstructible from gateway log |

### 3.3 Service Handler

A *service handler* is the deterministic Go function that consumes a
claim posted against a service participant. It receives the claim and
its delivery context, performs the requested work, and returns a
testament (or a queued promise of one for long-running work).

### 3.4 Programmatic Validator

A *programmatic validator* is a deterministic Go function registered
against a (claim action, validation type, scope) tuple. It evaluates a
testament's artifacts against a validation's quality bar and returns a
verdict without any LLM call.

### 3.5 Service Identity

A *service identity* is a canonical participant reference for a
deterministic subsystem. The UID is derived deterministically from
(service_type, scope_keys) so the same logical service in the same
session resolves to the same UID across process restarts.

### 3.6 Lifecycle Compression

*Lifecycle compression* is the optimization where a synchronous service
handler commits `claim.posted`, `claim.received`, `testament.posted`, and
`claim.satisfied` (when programmatic validation succeeds) within a
single board transaction. The wire-format lifecycle states are unchanged;
the commit timestamps are simply the same instant.

### 3.7 Service Scope Keys

*Service scope keys* are the inputs to deterministic service identity
derivation. Common scope keys are `session_id`, `pipeline_id`, `task_id`,
`tenant_id`, and `replica_index`. A per-pipeline VFS provisioner uses
`{session_id, pipeline_id}` as its scope keys; the singleton identity
registry uses `{}`; a sharded DAG processor uses
`{session_id, shard_index}`.

### 3.8 Handler Bound Context

A *handler bound context* is the per-claim execution context delivered
to a service handler. It carries the claim, the delivery delta, the
canonical issuer identity, the canonical subject identity (the service's
own), the goroutine scope, deadlines, cancellation, and the testament
accumulator binding so any artifact recorded by the handler reaches the
appropriate parent claim.

## 4. The Participant Taxonomy

### 4.1 Agent Participants

An agent participant is LLM-driven. It consumes claims by activating its
ClaimsInbox, which translates `claim.posted` deltas into work entries.
The agent's tool loop reads the entry, gathers context via `pull_work`
and named queries, decides on actions, emits skill calls, and produces
artifacts via the testament accumulator. At turn end, the accumulator
flushes one composite testament.

Agent identity carries the model name and replica generation:

```go
ParticipantRef{
    Category:   "agent",
    UID:        "018f0b3a-...",
    Namespace:  "session/s1",
    Pod:        "knowledge",
    Name:       "librarian",
    Type:       "librarian",
    Generation: 1,
    Model:      "claude-sonnet-4.6",
}
```

The agent consumption discipline is fully described in `CLAIMS.md §5` and
remains unchanged by this document.

### 4.2 Service Participants

A service participant is deterministic Go code. It consumes claims by
registering a handler for `(participant_type, claim_action)` tuples in
the service handler registry. The runtime delivers `claim.posted` deltas
that target the service to the matching handler, executes the handler
within a tracked goroutine scope, captures the returned testament, and
commits it to the board.

Service identity carries the process generation and optional replica
index:

```go
ParticipantRef{
    Category:    "service",
    UID:         "svc:vfs_provisioner:session/s1:pipeline/p1",
    Namespace:   "session/s1",
    Pod:         "vfs",
    Name:        "pipeline-vfs-provisioner",
    Type:        "vfs_provisioner",
    Generation:  ProcessGeneration,
    Version:     "v1.7.3",
    ScopeKeys:   map[string]string{"pipeline_id": "p1"},
}
```

UID derivation is deterministic:

```go
uid := DeriveServiceUID("vfs_provisioner", map[string]string{
    "session_id":  "s1",
    "pipeline_id": "p1",
})
```

Same inputs yield the same UID across restarts. This is what makes
"post a claim against the pipeline VFS provisioner for pipeline P"
replay-safe.

### 4.3 System Participants

A system participant is a structural runtime emitter. It is not the
target of claims in the normal sense; instead it emits claims and
testaments for structural events that the rest of the system needs to
observe: process boot phases, identity registry rotations, board open
and close transitions, bus topic registration, container lifecycle
events, fabric subscriber attach and detach.

System identity is a singleton per process:

```go
ParticipantRef{
    Category:   "system",
    UID:        "sys:identity_registry:proc/<process_uid>",
    Namespace:  "proc/<process_uid>",
    Pod:        "system",
    Name:       "identity-registry",
    Type:       "identity_registry",
    Generation: ProcessGeneration,
}
```

System participants are the cleanest demonstration that infrastructure
events are claims-plane events: a process boot is not a log line but a
sequence of claims and testaments durable in the WAL.

### 4.4 External Participants

An external participant is outside Sylk. The user is the canonical
external participant. CI controllers, deploy pipelines, and external
service callers may also be external participants if they need to issue
or respond to claims.

External participants enter the claims plane through gateway adapters
that produce claims (for prompts and approvals) and consume testaments
(for outbound notifications). The gateway is itself a service
participant.

External identity carries adapter and channel hints:

```go
ParticipantRef{
    Category:  "external",
    UID:       "ext:user:session/s1",
    Namespace: "session/s1",
    Pod:       "user",
    Name:      "human-user",
    Type:      "user",
    AdapterID: "tui",
}
```

## 5. Data Model Changes

The data model changes required by this document are small in surface
area but load-bearing in semantics.

### 5.1 ParticipantCategory Enum

```go
package claims

type ParticipantCategory string

const (
    ParticipantCategoryAgent    ParticipantCategory = "agent"
    ParticipantCategoryService  ParticipantCategory = "service"
    ParticipantCategorySystem   ParticipantCategory = "system"
    ParticipantCategoryExternal ParticipantCategory = "external"
)

func (c ParticipantCategory) Valid() bool {
    switch c {
    case ParticipantCategoryAgent,
        ParticipantCategoryService,
        ParticipantCategorySystem,
        ParticipantCategoryExternal:
        return true
    }
    return false
}
```

### 5.2 Generalized Participant Reference

Replace `AgentRef` with `ParticipantRef` in canonical delta envelopes
and in claim/testament/artifact actor and delivery fields. During
migration, `AgentRef` is a type alias that produces a `ParticipantRef`
with `Category: agent` so existing call sites compile.

```go
type ParticipantRef struct {
    Category   ParticipantCategory `json:"category"`
    UID        string              `json:"uid"`
    Namespace  string              `json:"namespace"`
    Pod        string              `json:"pod"`
    Name       string              `json:"name"`
    Type       string              `json:"type"`
    Generation uint64              `json:"generation"`

    // Agent-specific: model name (e.g., "claude-sonnet-4.6").
    // Service-specific: binary version (e.g., "v1.7.3").
    // System-specific: process binary version.
    // External-specific: empty.
    Model string `json:"model,omitempty"`

    // Service-specific: deterministic scope keys used to derive UID.
    // Empty for agents and singleton systems.
    ScopeKeys map[string]string `json:"scope_keys,omitempty"`

    // Task affinity reference for replicas with task context.
    Task *ParticipantTaskRef `json:"task,omitempty"`

    // External-specific: adapter identifier for gateway routing.
    AdapterID string `json:"adapter_id,omitempty"`

    // Labels for fabric and policy classification.
    Labels map[string]string `json:"labels,omitempty"`

    // Unresolved marks a degraded reference for which canonical
    // identity could not be obtained. Receivers must resolve before
    // treating it as authoritative.
    Unresolved       bool   `json:"unresolved,omitempty"`
    ResolutionReason string `json:"resolution_reason,omitempty"`
}
```

Universal base fields on `Action`, `Claim`, `Testament`, `Validation`,
and `Artifact` rename `AgentID` to `ParticipantID`. Backward compatibility
during migration preserves `AgentID` as a derived projection.

Artifact and validation lifecycle terminology is owned by
`docs/ARTIFACTS_AND_VALIDATIONS.md`. Infrastructure participants must
emit and consume the exact artifact states `artifact.generated`,
`artifact.generation_failed`, `artifact.received`,
`artifact.receipt_failed`, `artifact.attached`,
`artifact.validating`, `artifact.validation_failed`, and
`artifact.validated`; and the exact validation states
`validation.ready`, `validation.validating`,
`validation.validation_failed`,
`validation.validation_failed_not_required`, `validation.errored`,
`validation.errored_not_required`, `validation.validating_quality_bar`,
`validation.quality_bar_validation_failed`,
`validation.quality_bar_validation_failed_not_required`, and
`validation.validated`. Legacy validation statuses `pending`,
`in_progress`, `passed`, `incomplete`, `failed`, `errored`, and
`skipped` are compatibility projections only.

### 5.3 ProgrammaticValidator Interface

```go
package claims

// ProgrammaticValidator evaluates a single validation deterministically
// against the testament's artifacts. Implementations must be free of
// hidden state, must not call LLMs, and must return identical verdicts
// for identical inputs.
//
// Validators register against (action_type, validation_type, optional
// scope_predicate) tuples in the global registry. The board's
// EvaluateValidation path dispatches to the highest-priority matching
// validator before falling back to agentic evaluation.
type ProgrammaticValidator interface {
    // ID returns a stable identifier for this validator. Used in
    // verdict reason fields and in registration lookup.
    ID() string

    // Match reports whether this validator should evaluate the given
    // validation. Allows fine-grained scoping beyond (action,
    // validation_type).
    Match(claim *Claim, validation *Validation) bool

    // Evaluate returns the verdict. Implementations must be
    // deterministic: same (claim, validation, testament, artifacts)
    // inputs produce the same verdict. Implementations must not panic;
    // panics are recovered by the registry and produce
    // ValidationStatusErrored with an error artifact.
    Evaluate(
        ctx context.Context,
        claim *Claim,
        validation *Validation,
        testament *Testament,
        artifacts []*Artifact,
    ) ValidationResult
}

// ValidationResult is the structured return from a programmatic
// validator.
type ValidationResult struct {
    Status         ValidationStatus // passed | failed | errored | skipped
    Reason         string
    ReviewedRefs   []Relation       // Relations[reviews] for artifacts examined
    ErrorArtifacts []*Artifact      // Created when Status == errored
    EvidenceRefs   []Relation       // Optional additional evidence cites
}
```

### 5.4 ServiceHandler Interface

```go
package claims

// ServiceHandler is the deterministic Go function that consumes a claim
// directed at a service participant. The handler receives a bound
// context carrying the claim, the canonical issuer/subject identities,
// the goroutine scope, the testament accumulator, and the deadline.
//
// Handlers must return a Testament (or a continuation token for
// long-running work). Errors are recorded as error artifacts on the
// returned testament, never returned as Go errors that bypass the
// board. Panics are recovered by the dispatcher and produce a
// testament with kind=error_trace artifact.
type ServiceHandler interface {
    // Type returns the participant type this handler serves
    // (e.g., "vfs_provisioner", "dag_processor"). The registry
    // routes claim.posted deltas to handlers by participant type.
    Type() string

    // Actions returns the claim action types this handler accepts.
    // A handler may serve multiple actions if the service exposes a
    // small action surface (e.g., "task", "corrective").
    Actions() []ActionType

    // Handle processes a single claim and returns the testament that
    // answers it.
    Handle(ctx HandlerContext) HandlerResult
}

// HandlerContext is delivered to every Handle invocation.
type HandlerContext struct {
    Ctx          context.Context
    Claim        *Claim
    Delivery     *DeltaEnvelope
    Issuer       ParticipantRef
    Subject      ParticipantRef
    Board        BoardWriter
    Accumulator  *TestamentAccumulator
    Scope        *concurrency.GoroutineScope
    Deadline     time.Time
}

// HandlerResult is the structured return from a service handler.
type HandlerResult struct {
    // Testament is the response to the claim. Required unless
    // Continuation is non-nil for long-running work.
    Testament *Testament

    // Continuation is set when the handler cannot complete
    // synchronously. The dispatcher commits claim.progressed and
    // schedules a follow-up via the continuation store.
    Continuation *HandlerContinuation

    // ProgressUpdates are non-terminal updates the handler emits
    // before returning. Each becomes a claim.progressed delta.
    ProgressUpdates []ClaimProgressUpdate
}
```

### 5.5 ServiceRegistry Interface

```go
package claims

// ServiceRegistry is the global mapping from participant type to
// service handler. Registration is deterministic at process boot;
// handlers cannot be hot-swapped at runtime.
type ServiceRegistry interface {
    // Register adds a handler. Fails if the handler's Type() is
    // already registered.
    Register(handler ServiceHandler) error

    // Resolve returns the handler for a given participant type, or
    // nil if no handler is registered. The dispatcher uses this to
    // route incoming claim.posted deltas.
    Resolve(participantType string) (ServiceHandler, bool)

    // ParticipantTypes returns the set of types served by the
    // registry. Used for routing topic subscriptions.
    ParticipantTypes() []string
}
```

### 5.6 ValidatorRegistry Interface

```go
package claims

// ValidatorRegistry is the global mapping from (action_type,
// validation_type) tuples to programmatic validators. The board's
// EvaluateValidation path queries this registry before falling back
// to agentic evaluation.
type ValidatorRegistry interface {
    // Register adds a validator. Multiple validators may match the
    // same (action_type, validation_type) tuple; priority resolves
    // ordering. Fails if a validator with the same ID is already
    // registered.
    Register(actionType ActionType, validationType ValidationType,
        priority int, validator ProgrammaticValidator) error

    // Resolve returns the matching validators ordered by descending
    // priority. The dispatcher tries each in order until one returns
    // a non-errored verdict.
    Resolve(claim *Claim, validation *Validation) []ProgrammaticValidator
}
```

### 5.7 Identity Derivation Helpers

```go
package claims

// DeriveServiceUID computes a stable UID for a service participant.
// Same (serviceType, scopeKeys) yield the same UID across process
// restarts. UID format: "svc:<type>:<scope_key_1>=<value_1>:..."
// with scope keys sorted lexicographically for determinism.
func DeriveServiceUID(serviceType string, scopeKeys map[string]string) string

// DeriveSystemUID computes a stable UID for a system participant
// within a process. Used for identity-registry, board, bus, and
// boot-sequencer references.
func DeriveSystemUID(systemType string, processUID string) string

// DeriveExternalUID computes a stable UID for an external participant.
// Used for user references, gateway adapters, and CI controllers.
func DeriveExternalUID(externalType string, identityKey string) string
```

### 5.8 Lifecycle Compression Helper

```go
package claims

// CompressLifecycle commits posted, received, testament_generated,
// testament_posted, and (when programmatic validation succeeds)
// satisfied lifecycle states within a single board transaction.
// Used by synchronous service handlers whose work fits within one
// transaction boundary. The wire-format lifecycle states are
// unchanged; only the commit timestamps are coincident.
//
// Returns an error if the transaction cannot commit atomically.
// Partial commits are not possible; either all lifecycle states
// commit or none do.
func (b *ClaimsBoard) CompressLifecycle(
    ctx context.Context,
    claim *Claim,
    testament *Testament,
    validationResults []ValidationResult,
) error
```

### 5.9 No Overload of Existing Metadata

Service handler dispatch, programmatic validator results, and service
identity must not be encoded inside `Metadata` maps. Each gets a typed
field on the appropriate envelope. Following the same discipline as
`CLAIMS_VISIBILITY.md §4.2`, metadata remains kind-specific structured
data; cross-kind protocol concerns get typed fields.

## 6. Two Consumption Disciplines

There are two consumption disciplines for incoming claims, one per
non-external participant category. Both produce identical wire-format
testaments. The board, validators, bridge, and UI cannot distinguish
them at the protocol level and must not branch on category for
protocol behavior.

### 6.1 Agent Discipline (Recap)

The agent consumption discipline is described in detail in `CLAIMS.md §5`.
Summarized for completeness:

1. The ClaimsInbox subscribes to `claim.posted` deltas keyed by the
   agent's canonical UID topic.
2. On delivery, the inbox commits `claim.received` and enqueues a
   work entry.
3. The agent's tool loop pulls the work entry via `pull_work` and
   reads adjacent context via `query_claims_board` and lens queries.
4. The agent invokes skills as needed, recording artifacts via the
   testament accumulator on context.
5. At turn end, the accumulator flushes a single composite testament
   containing every artifact recorded during the turn.
6. Progress narration uses `update_claim_progress`, which emits
   `claim.progressed` deltas. Progress is non-terminal.

### 6.2 Service Discipline

The service consumption discipline is new and structurally simpler than
the agent discipline.

1. The service registers a `ServiceHandler` at process boot. The
   registry indexes handlers by `participant_type`.
2. The dispatcher subscribes to canonical topic patterns for every
   registered participant type. Subscription topics use service UIDs,
   not just types, so multi-instance services route correctly.
3. On `claim.posted` delivery, the dispatcher resolves the handler for
   the subject participant type, commits `claim.received`, and invokes
   the handler within a tracked goroutine scope.
4. The handler reads claim payload, executes its deterministic work,
   records artifacts via its bound testament accumulator, and returns
   a `HandlerResult`.
5. For synchronous handlers, the dispatcher invokes `CompressLifecycle`
   to commit `testament.posted`, run programmatic validators, and
   commit `claim.satisfied` (or appropriate terminal validation state)
   in a single transaction.
6. For async handlers (returning a `Continuation`), the dispatcher
   commits `claim.progressed` and schedules a follow-up. When the
   continuation resolves, the dispatcher commits the testament and
   runs validation as a separate transaction.
7. Handler panics are recovered. The dispatcher produces a testament
   with an `error_trace` artifact and commits
   `claim.testament_generation_failed` with the trace.

### 6.3 Discipline Comparison

| Concern | Agent | Synchronous Service | Asynchronous Service |
|---|---|---|---|
| Wake source | `claim.posted` → ClaimsInbox | `claim.posted` → handler dispatch | `claim.posted` → handler dispatch |
| Reasoning | LLM with tools | Pure Go | Pure Go |
| Response timing | Seconds to minutes | Microseconds to seconds | Seconds to minutes |
| Testament shape | Accumulator flush, one composite | Direct return, one testament | Continuation resolution, one testament |
| Lifecycle commits | One per state, separate transactions | All states one transaction | Posted/received one transaction, testament/satisfied in second |
| Goroutine ownership | Agent's tracked scope | Dispatcher's tracked scope (per-claim) | Dispatcher's scope + continuation worker scope |
| Idempotency key | Claim ID | Claim ID + content-derived service idempotency key | Claim ID + continuation token |
| Replay safety | Deterministic up to LLM nondeterminism | Fully deterministic | Fully deterministic when continuation state is durable |
| Failure shape | Error artifact in flushed testament | Error artifact in returned testament | Error artifact in resolved continuation testament |

### 6.4 Identical Wire Format

The crucial invariant: every testament, every artifact, every delta, and
every validation outcome produced by a service handler is bit-identical
in shape to one produced by an agent. The board's amplifier does not
branch on category. The Guide bus does not branch on category. The
bridge does not branch on category. The UI does not branch on category.

Where category surfaces is in:

- Topic routing (different subscription patterns per category).
- Identity rendering (UI may show a service badge vs. an agent badge).
- Continuation policy (services have their own continuation store).
- Validator dispatch (programmatic validators do not require agentic
  evaluator wakeup).

None of these branching points is a protocol-level distinction. They are
operational and presentational only.

## 7. Service Identity Model

Service identity is the load-bearing primitive that makes deterministic
infrastructure replay-safe and discoverable.

### 7.1 Deterministic UID Derivation

The service UID is a pure function of (service_type, sorted scope keys
and values). The same logical service in the same scope produces the
same UID across process restarts.

```text
UID = "svc:" + service_type + ":" + sorted_scope_key_values

example:
  service_type = "vfs_provisioner"
  scope_keys   = {"session_id": "s1", "pipeline_id": "p1"}
  UID          = "svc:vfs_provisioner:pipeline_id=p1:session_id=s1"
```

This determinism enables three properties:

1. **Replay-safe targeting.** An issuer can post a claim against a
   service UID without coordinating with the registry; the UID is
   reconstructible from the claim's payload.
2. **Cross-restart continuity.** A pending claim posted before a process
   restart resolves to the same handler after restart. The handler can
   detect "I already produced a testament for this claim" by reading
   the board.
3. **Audit transparency.** Every service UID is human-readable and
   directly maps to the service that produced it. Forensics on a WAL
   replay does not require a UID-to-service lookup table.

### 7.2 Process Generation

Service identity carries the `Generation` field, set to the issuing
process's generation counter. The counter increments on every process
restart. Generation rotation enables the runtime to detect stale claims
posted to a dead generation and transition them to `post_failed` instead
of attempting redelivery.

A handler whose generation has rotated must:

1. Read the claim's delivery context to detect generation mismatch.
2. Commit `claim.receipt_failed` with an `identity_generation_mismatch`
   error artifact.
3. Optionally reissue the claim with current-generation identity if the
   service-type-specific policy permits.

### 7.3 Singleton vs. Sharded Services

Singleton services (identity registry, board, bus, boot sequencer) have
no scope keys; their UID is `svc:<type>:` with no scope suffix. Only
one instance exists per process.

Sharded services (per-pipeline VFS provisioners, per-session knowledge
graph writers, per-tenant document DB ingesters) have scope keys that
identify the shard. Their UIDs are unique per (type, scope_keys)
combination. Routing dispatches based on the resolved UID; the dispatcher
ensures only the matching shard's handler is invoked.

A service that is logically singleton per session but the process hosts
multiple sessions becomes sharded at the session boundary; its UID
carries `session_id` as a scope key.

### 7.4 Identity Registry as a Service

The identity registry is itself a system participant whose service
handler allocates participant UIDs. Allocating an agent's canonical UID
becomes a claim:

```text
Action {
  Type: task
  Issuer:  container(uid=...)
  Subject: identity_registry(uid=sys:identity_registry:proc/<process>)
  Claim {
    title: "Allocate canonical UID for participant type architect"
    expected_tool_calls: [
      { tool: "identity.allocate",
        arguments: {
          type:        "architect",
          namespace:   "session/s1",
          scope_keys:  {"session_id": "s1"}
        },
        produces_artifacts: ["uid_allocation"] }
    ]
    validations: [
      { type: receipt, required: true },
      { type: contract, required: true,
        description: "Allocated UID is unique within namespace",
        quality_bar: "registry.has_unique(uid)" },
      { type: contract, required: true,
        description: "UID is deterministic for input parameters",
        quality_bar: "DeriveServiceUID(type, scope) == uid" },
    ]
  }
}
```

The identity registry's handler runs deterministically, allocates the
UID, and returns a testament with a `uid_allocation` artifact carrying
the UID, generation, namespace, and parent lineage. Programmatic
validators check uniqueness and determinism. The whole transaction
completes in microseconds.

Bootstrapping is the only subtlety: the identity registry itself needs
a UID. The boot sequencer hardcodes the system identity registry's UID
as `sys:identity_registry:proc/<process_uid>` where `process_uid` is the
process's own startup-derived UID. Every other identity flows through
the registry's handler.

### 7.5 Identity Resolution at Receipt

Receipt-side identity verification is unchanged from
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §16 Phase 4.3`: receivers verify the
delivery's subject UID matches their own UID before committing
`claim.received`. For services, the dispatcher performs this check
before invoking the handler. If the UIDs do not match, the dispatcher
commits `claim.receipt_failed` and the handler is not invoked.

## 8. Service Handler Dispatch

The dispatcher is the runtime component that subscribes to canonical
topics, routes incoming `claim.posted` deltas to handlers, manages
goroutine ownership and timeouts, and commits the resulting lifecycle
states.

### 8.1 Subscription Model

At process boot, the dispatcher reads the service registry and
subscribes to the Guide event bus on patterns:

```text
claims.<session>.agent.<service_uid>.claim.posted
```

following the topic grammar from `CLAIMS_AND_DELTAS.md §13`. The
subscription is per-service-UID, not per-service-type, so multi-shard
services route correctly without dispatcher-side filtering.

Subscribers attach during boot under the boot sequencer's claim
lifecycle: a `boot.subscriptions_attached` claim is posted by the boot
sequencer and the dispatcher's testament records every subscription
attached with topic and handler ID artifacts.

### 8.2 Per-Claim Goroutine Scope

Every claim delivered to a handler runs within its own bounded child
scope of the dispatcher's parent goroutine scope. The child scope:

- Inherits the parent's quota and budget.
- Carries a deadline derived from `min(claim.deadline,
  handler_default_timeout)`.
- Is cancelled by the parent on dispatcher shutdown.
- Owns any nested goroutines the handler spawns (which must also be
  scope-tracked per the project-wide rule).
- Records its own resource usage for backpressure decisions.

Handler default timeouts are derived from the handler's declared
expected execution duration (a field on the handler registration), not
hardcoded. The timeout for a synchronous VFS-handle-validation handler
is on the order of the underlying filesystem operation's latency. The
timeout for an asynchronous pipeline-pod-allocation handler is on the
order of the underlying scheduler's allocation budget plus its
configured retry budget.

### 8.3 Bounded Dispatch Queue

The dispatcher maintains a bounded queue per registered handler. Queue
capacity is derived from:

```text
capacity = handler.expected_concurrent_claims * handler.expected_p99_duration / dispatcher.tick_interval
```

with no magic numbers; each input is declared at handler registration.
When a queue fills, the dispatcher commits `claim.receipt_failed` with
a `dispatcher_backpressure` error artifact and emits operational
telemetry. The claim remains on the board; the issuer can re-post or
post a corrective claim.

### 8.4 Handler Invocation Flow

For each delivered `claim.posted`:

```text
1. Dispatcher receives delta on subscribed topic.
2. Dispatcher deduplicates by delta_key.
3. Dispatcher resolves handler by subject participant type.
4. Dispatcher verifies subject UID matches a registered handler instance.
5. Dispatcher commits claim.received.
6. Dispatcher constructs HandlerContext.
7. Dispatcher spawns tracked goroutine within bounded scope.
8. Handler runs.
9. Handler returns HandlerResult.
10a. Synchronous: dispatcher invokes CompressLifecycle.
10b. Asynchronous: dispatcher commits claim.progressed, registers
     continuation, returns.
11. Dispatcher records handler runtime and goroutine usage in
    operational telemetry.
12. Dispatcher releases scope.
```

Each numbered step has explicit acceptance criteria in §17 (Phased
Implementation Plan).

### 8.5 Asynchronous Continuations

A handler that cannot complete within its deadline returns a
`HandlerContinuation` with:

- `ContinuationID` (durable across restarts).
- `ExpectedDuration` (informs progress emission cadence).
- `ResumeFn` (the Go function to call when the continuation completes;
  reconstructed from the registry on restart).
- `StateRef` (an artifact reference where the handler persisted its
  intermediate state; the resume function reads from it).

The dispatcher writes the continuation to a continuation store keyed by
`ContinuationID`. A continuation worker pool (tracked goroutine scope,
bounded queue) processes continuations as their preconditions are met.
On process restart, the worker pool reads pending continuations from
the store and resumes them.

Continuations are not a new continuation system; they reuse the
existing `consult_continuations.go` machinery with service-handler-
specific resume semantics.

### 8.6 Failure and Cancellation

A handler may fail in several ways. Each maps to a specific lifecycle
outcome and error artifact kind:

| Failure | Lifecycle outcome | Error artifact kind |
|---|---|---|
| Handler returns testament with verdict=failure | `claim.testament_generated` then validation failure | Error artifacts in the testament |
| Handler panics | `claim.testament_generation_failed` | `error_trace` |
| Handler exceeds deadline | `claim.progress_failed` | `tool_timeout` |
| Handler context cancelled (shutdown, interrupt) | `claim.progress_failed` | `interrupted` |
| Policy denial before handler runs | `claim.post_failed` (at post time) | `policy_denied` |
| Subject UID mismatch | `claim.receipt_failed` | `identity_mismatch` |
| Dispatcher queue full | `claim.receipt_failed` | `dispatcher_backpressure` |
| Handler returns nil testament without continuation | `claim.testament_generation_failed` | `handler_contract_violation` |

Every failure produces a durable lifecycle event and a queryable error
artifact. No failure is silent.

### 8.7 Idempotency and Replay

Handler idempotency uses a content-derived key:

```text
idempotency_key = sha256(
  claim.id +
  claim.title +
  claim.description +
  canonical_json(claim.expected_tool_calls) +
  canonical_json(claim.scope) +
  subject_uid +
  generation
)
```

Handlers store the idempotency key in their testament's metadata. On
replay, the dispatcher checks whether a testament with the same key
already exists; if so, the handler is not re-invoked.

Non-idempotent operations (key rotations, one-time allocations) carry
an explicit nonce in `claim.expected_tool_calls.arguments.nonce` that
makes the idempotency key unique per invocation. Replay still skips
re-execution because the key matches a prior testament.

## 9. Programmatic Validation

Programmatic validation is the second large addition. It generalizes the
mechanical-receipt-validation pattern from `CLAIMS.md §2.4` to all
validation types.

### 9.1 Registration

Validators register at process boot:

```go
registry.Register(
    claims.ActionTypeTask,
    claims.ValidationTypeContract,
    priority,
    &VFSCapacityValidator{},
)
```

Multiple validators may match the same `(action_type, validation_type)`
tuple. Each declares a `Match(claim, validation) bool` predicate for
fine-grained scoping. The dispatcher selects validators in descending
priority order and tries each until one returns a non-errored verdict.

### 9.2 Evaluation Flow

When the board commits a `testament.posted`, it inspects the parent
claim's pending validations. For each non-receipt validation:

```text
1. Board calls validatorRegistry.Resolve(claim, validation).
2. For each matching validator in priority order:
   a. Validator.Evaluate(ctx, claim, validation, testament, artifacts).
   b. Result returned.
   c. If Status == errored, try next validator.
   d. Otherwise, accept the result.
3. If no validator returns a non-errored result, fall back to agentic
   evaluation: post a validation-evaluator claim against the evaluator
   participant declared on the claim, with the testament's artifacts
   attached as evidence.
```

### 9.3 Validator Determinism

Programmatic validators must be deterministic. The board records this
contract by:

1. Requiring validators to declare themselves as `Deterministic() bool`
   in their interface implementation. Any validator returning `false`
   is treated as agentic.
2. Replay-time auditing: the board records the validator ID in the
   validation's status history. On replay, the same validator is
   re-invoked and the result is compared. Divergence produces a
   `validator_nondeterministic` error artifact and the original verdict
   is preserved on the board (replay does not overwrite committed
   state).
3. Validator-specific test suites that exercise edge cases the validator
   claims to cover.

### 9.4 Mixed Programmatic and Agentic Validation

A claim may carry multiple validations, some programmatic and some
agentic. The board evaluates them in parallel where dependencies permit:

```text
Claim {
  validations: [
    { id: v1, type: receipt    } -> auto-passes on testament.posted
    { id: v2, type: contract   } -> programmatic: VFSCapacityValidator
    { id: v3, type: inspection } -> agentic: posted to inspector
  ]
}
```

`claim.satisfied` requires all required validations pass. Programmatic
validators run first; their results may inform the agentic evaluator's
context (programmatic verdicts are visible in `query_claims_board` to
the agentic evaluator).

### 9.5 Validator Catalog

A non-exhaustive starting catalog of programmatic validators:

| Validator | Action types | Validation types | Quality bar examples |
|---|---|---|---|
| `ReceiptValidator` | all | receipt | Linked testament arrived |
| `VFSCapacityValidator` | task | contract | `vfs.capacity_mb >= requested_capacity_mb` |
| `VFSAttachmentValidator` | task | contract | `vfs.attached == true && vfs.mount_point != ""` |
| `VFSBaseVersionValidator` | task | contract | `vfs.base_version reachable from session_head` |
| `PipelinePodCountValidator` | task | contract | `len(pod_state.pods) == claim.expected_pod_count` |
| `AgentHealthValidator` | task | integration | All probe results `ready: true` |
| `IdentityUniqueValidator` | task | contract | UID not already registered |
| `IdentityDeterministicValidator` | task | contract | UID matches `DeriveServiceUID(type, scope)` |
| `ContentHashValidator` | any | contract | Artifact `ContentHash` matches expected |
| `SchemaValidator` | any | contract | Artifact JSON parses against registered schema |
| `ScopeSubsetValidator` | task | contract | Granted scopes ⊆ claim-requested scopes |
| `PolicyAllowValidator` | any | contract | Tool policy allows declared expected tools |
| `DurationBudgetValidator` | task | regression | Operation completed within `claim.deadline` |
| `ArtifactPresenceValidator` | any | contract | Required artifact kinds present in testament |
| `EmbeddingDimensionValidator` | task | contract | KG embedding dimensions match model config |
| `IndexedDocumentValidator` | task | contract | Document DB returns indexed status for new doc |
| `GuardianPolicyMatchValidator` | task | contract | Operation matches an allowed policy pattern |

Each validator is a few hundred lines of pure Go. None requires an LLM.

### 9.6 Validator Composition

Complex quality bars decompose into multiple validators. For example,
"the pipeline is fully provisioned and ready" decomposes into:

- `PipelinePodCountValidator` (pod count matches request)
- `AgentHealthValidator` (every pod's agent is healthy)
- `ScopeSubsetValidator` (every agent's scope grants are valid)
- `VFSAttachmentValidator` (the pipeline VFS attached successfully,
  via a sub-claim's testament)

The composition is expressed in the claim's `validations` array, not in
a single mega-validator. Each validator owns a narrow, testable
responsibility.

### 9.7 Validator Failure Modes

A validator returning `Status == errored` does not fail the claim's
validation outright. The dispatcher tries lower-priority validators. If
all validators error, the dispatcher commits `claim.validation_errored`
with all error artifacts from each validator's `ErrorArtifacts` field.
The issuer or evaluator can then decide whether to escalate or post a
remediation claim.

Common errored conditions:

- Required artifact missing entirely (incomplete, not failed).
- Artifact JSON malformed (errored, not failed — the validator could
  not parse).
- Artifact references a content reference that cannot be dereferenced
  (errored — the validator could not load).
- Validator's external dependency unavailable (errored — the validator
  could not execute).

Validators must distinguish "the evidence is missing" (incomplete) from
"the evidence is wrong" (failed) from "the validator could not evaluate"
(errored). The verdict semantics match `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §5.1`.

## 10. Lifecycle Compression for Synchronous Services

Synchronous services produce their testament within microseconds of
receiving a claim. The full lifecycle from `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §4`
applies, but the commit timestamps are coincident.

### 10.1 Wire Format Unchanged

Even with compression, every lifecycle state commits to the board.
Every state appears in the WAL. Every state emits its delta. Replay
reconstructs every state. The optimization is purely transactional:
multiple state transitions commit together rather than as separate
durability fences.

### 10.2 Single-Transaction State Set

The compressed transaction commits:

1. `claim.received` — dispatcher acknowledges delivery.
2. `claim.progressed` (optional) — if handler emits progress before
   returning.
3. `testament.generated` — testament durable.
4. `testament.posted` — testament activated.
5. `claim.testament_generated` — claim acknowledges generated testament.
6. `claim.testament_acknowledged` — issuer-side ack stamped (the
   dispatcher acts on behalf of the issuer for service-to-service
   acks because the issuer is itself a service or agent and ack
   protocol applies symmetrically).
7. `claim.validating` — validators begin.
8. `validation.evaluated` (per validation) — verdicts.
9. `testament.validated` / `testament.validation_failed` /
   `testament.validation_incomplete` — terminal testament state.
10. `claim.satisfied` / corresponding terminal — claim closes.

Every state emits a delta. Receivers see them in commit order. The
sequence numbers are strictly monotonic but commit timestamps are equal.

### 10.3 When Compression Is Disallowed

Compression is disabled when:

- Any required validation is agentic (the agentic evaluator must run
  in a separate turn).
- The handler returns a `Continuation` (long-running work).
- The handler explicitly opts out via `HandlerResult.NoCompression =
  true` (used when the issuer needs to observe `testament.posted`
  before any validation runs, for example when the testament's
  artifacts are needed by another participant before validation
  completes).
- Lock contention forces the dispatcher to abort the compressed
  transaction; in that case it commits the prior lifecycle states
  and retries the later ones.

### 10.4 Replay Equivalence

Compressed and uncompressed lifecycles are replay-equivalent. The WAL
records every state. The replay reducer ignores commit timestamps and
processes states in sequence order. The resulting board state is
identical regardless of whether the lifecycle was compressed at the
original commit.

## 11. Idempotency, Replay, and Determinism

These three properties are tightly coupled for deterministic
infrastructure. This section formalizes the contract.

### 11.1 Content-Derived Idempotency

Service handlers derive their idempotency key from the claim's content,
not the claim's ID. This is necessary because replayed claims arrive
with new claim IDs (when re-issued) but the same content; the handler
must recognize the equivalence.

The canonical idempotency key formula:

```text
key = sha256(
  canonical_json({
    "type": claim.action_type,
    "subject_uid": claim.subject.uid,
    "scope": canonicalize(claim.scope),
    "expected_tool_calls": canonicalize(claim.expected_tool_calls),
    "title": claim.title,
    "description": claim.description,
    "nonce": claim.metadata.idempotency_nonce, // optional
  })
)
```

`canonicalize` sorts arrays where order is non-significant, normalizes
JSON ordering, and applies Unicode NFC normalization to strings. The
output is deterministic for semantically equivalent claims.

Handlers persist this key as a relation on the testament: `Relation{
RelatedType: "idempotency_key", Related: <key>, Relationship:
"derived_from"}`. The dispatcher queries the board for testaments
matching the key before invoking the handler.

### 11.2 Replay Reconstruction

Replay of an infrastructure participant's WAL must reconstruct:

1. Every claim received with its delivery delta.
2. Every lifecycle state and its commit sequence.
3. Every testament generated, posted, received.
4. Every artifact attached and its content references.
5. Every validation evaluated and its verdict.
6. Every error artifact and its diagnostic payload.
7. Every continuation pending and its state reference.

The reconstruction is deterministic: same WAL, same reducer, same board
state.

### 11.3 Non-Idempotent Operations

Some operations are intrinsically non-idempotent: rotating a key,
allocating a one-time token, consuming a single-use resource. For these,
the claim must carry an `idempotency_nonce` that distinguishes one
invocation from another. The handler treats two claims with different
nonces as different invocations even when the rest of the content
matches.

The nonce is supplied by the issuer at claim generation. For replay
safety, the nonce must be deterministic from the issuer's own state at
the moment of generation. A common pattern is `nonce = issuer_uid +
issuer_sequence_counter`.

### 11.4 Handler Determinism Contract

Service handlers declare their determinism level:

```go
type HandlerDeterminism string

const (
    // Deterministic: identical inputs produce identical outputs.
    // Including timestamps, allocations, ordering. Used for pure
    // computation, schema validation, identity derivation.
    HandlerDeterminismPure HandlerDeterminism = "pure"

    // ContentDeterministic: identical inputs produce equivalent
    // outputs modulo timestamps and allocation IDs. The testament's
    // content_hash is stable; allocation IDs are tracked separately.
    HandlerDeterminismContent HandlerDeterminism = "content"

    // SideEffectDeterministic: handler performs the same external
    // operation on identical input, but the result depends on
    // external state (e.g., disk free space, network reachability).
    HandlerDeterminismSideEffect HandlerDeterminism = "side_effect"

    // Nondeterministic: handler intrinsically nondeterministic
    // (e.g., random ID generation without a seed). Replay relies
    // on the testament's stored output, not on re-execution.
    HandlerDeterminismNondeterministic HandlerDeterminism = "nondeterministic"
)
```

Replay strategy varies by determinism level:

- **Pure**: re-execute on replay; assert output matches stored testament.
- **Content**: re-execute on replay; assert content_hash matches.
- **SideEffect**: do not re-execute; trust the stored testament.
- **Nondeterministic**: do not re-execute; trust the stored testament.

Most infrastructure handlers are `Content` or `SideEffect`. Pure
handlers are common for derivation (identity, content hashes, scope
calculations).

### 11.5 Determinism Auditing

The board can audit handler determinism by re-running pure and content
handlers on replay and comparing outputs. Divergence indicates either:

- A handler bug (it was supposed to be deterministic but isn't).
- A drift in declared determinism level.
- A board corruption (the original testament was wrong).

Audits run during routine WAL replay and during dedicated audit
sessions. Auditing failures produce `validator_nondeterministic` error
artifacts; the operator decides whether to escalate.

## 12. Concurrency, Goroutine Ownership, and Bounded Resources

Every component this document introduces — handler dispatcher, validator
registry dispatch, continuation worker pool, subscription manager — must
follow the project-wide concurrency discipline articulated in
`CLAUDE.md` and reiterated across `CLAIMS_AND_DELTAS.md §16` and
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §16 Phase 9.2`.

### 12.1 Goroutine Ownership

Every goroutine is owned by a tracked `core/concurrency.GoroutineScope`.
The ownership graph:

```text
process scope
├── boot sequencer scope
├── dispatcher scope
│   ├── per-handler dispatch scope (per registered handler)
│   │   └── per-claim execution scope (per delivered claim)
│   └── continuation worker pool scope
│       └── per-continuation worker scope
└── validator dispatch scope
    └── per-validation evaluation scope
```

Every scope:

- Has a parent.
- Has a context with deadline and cancellation.
- Has a quota budget.
- Records its goroutine count.
- Cancels on parent cancellation.
- Joins on parent shutdown with a deterministic timeout.

No goroutine is spawned with `go func()` directly. Every spawn goes
through `scope.Go(name, timeout, fn)` and is observable in operational
telemetry.

### 12.2 Bounded Queues

Every queue is bounded:

- Dispatcher per-handler queue: capacity derived from
  `expected_concurrent_claims * expected_p99_duration / tick_interval`.
- Continuation pending queue: capacity derived from
  `expected_max_pending_continuations` declared at handler registration.
- Validator dispatch queue: capacity derived from
  `expected_concurrent_validations`.
- Delta subscription buffer: capacity derived from
  `expected_burst_size` per topic.

Overflow behavior:

- Dispatcher queue overflow: `claim.receipt_failed` with
  `dispatcher_backpressure` artifact.
- Continuation queue overflow: `claim.progress_failed` with
  `continuation_backpressure` artifact.
- Validator queue overflow: `claim.validation_errored` with
  `validator_backpressure` artifact.
- Subscription buffer overflow: subscriber's per-topic
  `subscription_overflow` counter increments; affected deltas are
  reconciled via durable board replay on subscriber catch-up.

No silent drops. Every overflow is observable on the board and in
telemetry.

### 12.3 No Lock Inversion

Lock-order discipline:

1. Board write lock is acquired only by the board's mutation methods.
   Handlers, validators, and dispatchers never hold the board write
   lock while calling out to handler code, validator code, bus
   publication, or artifact storage.
2. Dispatcher's per-handler queue mutex is acquired only inside
   dispatcher methods. Handlers do not acquire it.
3. Validator registry mutex is acquired only inside registry methods.
   Handlers do not acquire it during execution.
4. Cross-component lock ordering: board > dispatcher > registry. Locks
   are acquired in this order only and never the reverse.

Lock-inversion tests in §17 Phase 14 (Cleanup) include explicit
adversarial scenarios that would deadlock under inverted ordering.

### 12.4 Backpressure Visibility

Operational telemetry counters:

```text
claims_dispatcher_claims_received_total{handler}
claims_dispatcher_claims_completed_total{handler, outcome}
claims_dispatcher_queue_depth{handler}
claims_dispatcher_queue_overflows_total{handler}
claims_dispatcher_handler_panic_total{handler}
claims_dispatcher_handler_timeout_total{handler}
claims_validator_evaluations_total{validator, outcome}
claims_validator_evaluation_duration_seconds{validator}
claims_continuation_pending_count{handler}
claims_continuation_resumed_total{handler, outcome}
claims_subscription_overflows_total{topic}
```

Every counter has a documented derivation; none uses a magic
threshold. Alarm thresholds are declared in the participant's
registration metadata, not hardcoded.

### 12.5 Shutdown Ordering

Process shutdown drains the scope tree in deterministic order:

1. Stop accepting new bus deliveries.
2. Drain dispatcher per-handler queues with the per-handler deadline.
3. Cancel in-flight handler executions; their cancellation produces
   `interrupted` error artifacts.
4. Resolve continuations: those waiting for their preconditions become
   `claim.progress_failed` with `shutdown` error artifacts.
5. Drain validator dispatch queue.
6. Close board.
7. Close bus subscriptions.
8. Join scope tree.

Shutdown produces a `process.shutdown` system testament with artifacts
counting drained claims, resolved continuations, and recorded
interruption artifacts.

## 13. Worked Example: End-to-End Pipeline Provisioning

This section walks through a complete end-to-end interaction across
multiple service participants to ground the abstractions.

### 13.1 Setup

A user submits a prompt: "Implement HS256 JWK deserialization in the
auth service." The Guide classifies it as an Architect task. The
Architect (agent) decomposes it into a plan and submits the plan for
user approval. The user approves. The Orchestrator (agent) now must
dispatch the first task to a pipeline.

The systems involved:

- **Orchestrator** (agent): issues the task-dispatch claim.
- **DAG processor** (service, `dag_processor`): allocates the pipeline.
- **Pipeline VFS provisioner** (service, `vfs_provisioner`): allocates
  the pipeline's VFS.
- **Identity registry** (system, `identity_registry`): allocates
  agent UIDs as needed.
- **Activation controller** (service, `activation_controller`):
  brings agents from cold to hot tier.
- **Knowledge graph writer** (service, `kg_writer`): indexes the
  pipeline's artifacts as they accrue.
- **Document DB writer** (service, `doc_db_writer`): indexes the
  scribe's narrations.

### 13.2 Orchestrator Claim

The Orchestrator posts a single root claim against the DAG processor:

```text
Action {
  ID: action-001
  Type: task
  Issuer: orchestrator(uid=...)
  Claim {
    ID: claim-001
    Title: "Provision pipeline P for task T-jwt-impl"
    Description: "Allocate pipeline with engineer, tester, designer
                  for task T-jwt-impl. Attach pipeline VFS at
                  base version session_head with 1024 MB capacity."
    Scope: [pipeline:P, task:T-jwt-impl]
    Subject: dag_processor(uid=svc:dag_processor:session/s1)
    ActionType: task
    Expected Tool Calls: [
      {
        tool: "dag.allocate_pipeline"
        arguments: {
          pipeline_id: "P"
          task_id:     "T-jwt-impl"
          agents:      ["engineer", "tester", "designer"]
          vfs: { capacity_mb: 1024, base_version: "session_head" }
        }
        purpose: "Allocate pipeline pods and provision VFS"
        required: true
        produces_artifacts: ["pipeline_pod_state", "agent_health",
                             "vfs_attachment"]
      }
    ]
    Validations: [
      { id: v-001-receipt, type: receipt, required: true,
        description: "DAG processor responds with testament" }
      { id: v-001-podcount, type: contract, required: true,
        description: "Pipeline pod count matches agent list",
        quality_bar: "len(pipeline_pod_state.pods) == 3" }
      { id: v-001-health, type: integration, required: true,
        description: "All agent health probes pass",
        quality_bar: "all(h.ready for h in agent_health)" }
      { id: v-001-vfs, type: contract, required: true,
        description: "VFS attached at requested base version",
        quality_bar: "vfs_attachment.attached &&
                      vfs_attachment.base_version == 'session_head'" }
      { id: v-001-capacity, type: contract, required: true,
        description: "VFS capacity meets request",
        quality_bar: "vfs_attachment.capacity_mb >= 1024" }
    ]
  }
}
```

The board generates `claim-001`, then posts it. The Guide bus delivers
`claim.posted` to `claims.session/s1.agent.svc:dag_processor:session/s1.claim.posted`.

### 13.3 DAG Processor Handler

The DAG processor's handler picks up the delta. The handler is
deterministic Go code:

```go
func (h *DAGProcessorHandler) Handle(c HandlerContext) HandlerResult {
    args := parseAllocationArgs(c.Claim.ExpectedToolCalls[0].Arguments)

    // Spawn sub-claim against VFS provisioner.
    vfsClaim := h.buildVFSClaim(c.Claim, args.VFS)
    h.board.PostAction(c.Ctx, vfsAction, []Claim{vfsClaim})
    // ... posts the VFS sub-claim with caused_by relation to claim-001.

    // Allocate pods deterministically.
    podStates := []PodState{}
    for _, agentType := range args.Agents {
        uid := h.identityRegistry.AllocateOrLookup(agentType, sessionScope)
        pod := h.scheduler.AllocatePod(uid, agentType, args.PipelineID)
        podStates = append(podStates, pod)
    }

    // Probe each pod's agent health.
    healths := []AgentHealth{}
    for _, pod := range podStates {
        healths = append(healths, h.probeAgentHealth(pod))
    }

    // Wait for the VFS sub-claim to satisfy (this is a continuation
    // boundary if synchronous polling is not acceptable).
    vfsArtifact := h.waitForVFSAttachment(c.Ctx, vfsClaim.ID)

    return HandlerResult{
        Testament: &Testament{
            Issuer:  c.Subject,
            Subject: c.Issuer,
            Summary: "Pipeline P allocated with 3 agents, VFS attached",
            Confidence: ConfidenceCommitted,
            Artifacts: []*Artifact{
                {
                    Kind: "pipeline_pod_state",
                    Reference: marshalPodStates(podStates),
                    Metadata: map[string]any{
                        "pipeline_id": args.PipelineID,
                        "pod_count":   len(podStates),
                    },
                },
                {
                    Kind: "agent_health",
                    Reference: marshalAgentHealth(healths),
                    Metadata: map[string]any{
                        "all_ready": allReady(healths),
                    },
                },
                {
                    Kind: "vfs_attachment",
                    Reference: marshalVFSAttachment(vfsArtifact),
                    Metadata: map[string]any{
                        "vfs_claim_id": vfsClaim.ID,
                    },
                    Relations: []Relation{
                        {Related: vfsArtifact.ID, RelatedType: "artifact",
                         Relationship: "derived_from"},
                    },
                },
            },
            Relations: []Relation{
                {Related: c.Subject.UID, RelatedType: "agent",
                 Relationship: "issuer"},
                {Related: c.Issuer.UID, RelatedType: "agent",
                 Relationship: "subject"},
                {Related: c.Claim.ID, RelatedType: "claim",
                 Relationship: "claim"},
            },
        },
    }
}
```

The handler's cyclomatic complexity is kept below 4 by extracting
sub-steps (sub-claim build, pod allocation loop, health probe loop) into
their own methods. The handler itself is a sequence of well-named
function calls and a final return.

### 13.4 VFS Provisioner Sub-Claim

The DAG processor's sub-claim:

```text
Claim {
  ID: claim-002
  Title: "Provision pipeline VFS for pipeline P"
  Description: "Allocate VFS with capacity 1024 MB attached at base
                version session_head, mount under pipeline P."
  Scope: [pipeline:P, resource:vfs]
  Subject: vfs_provisioner(uid=svc:vfs_provisioner:session/s1:pipeline/P)
  ActionType: task
  Relations: [
    { related: claim-001, related_type: claim, relationship: caused_by }
  ]
  Expected Tool Calls: [
    {
      tool: "vfs.provision"
      arguments: { capacity_mb: 1024, base_version: "session_head",
                   pipeline_id: "P" }
      produces_artifacts: ["vfs_handle", "vfs_topology"]
    }
  ]
  Validations: [
    { id: v-002-receipt, type: receipt, required: true }
    { id: v-002-attached, type: contract, required: true,
      description: "VFS handle reports attached",
      quality_bar: "vfs_handle.attached == true" }
    { id: v-002-baseversion, type: contract, required: true,
      description: "VFS at requested base version",
      quality_bar: "vfs_handle.base_version == 'session_head'" }
    { id: v-002-capacity, type: integration, required: true,
      description: "VFS capacity meets request",
      quality_bar: "vfs_handle.capacity_mb >= 1024" }
  ]
}
```

### 13.5 VFS Provisioner Handler

```go
func (h *VFSProvisionerHandler) Handle(c HandlerContext) HandlerResult {
    args := parseVFSArgs(c.Claim.ExpectedToolCalls[0].Arguments)

    vfsHandle, err := h.cowEngine.Allocate(c.Ctx, args.CapacityMB,
                                            args.BaseVersion, args.PipelineID)
    if err != nil {
        return HandlerResult{
            Testament: errorTestament(c, err, "vfs.allocate"),
        }
    }

    topology := h.cowEngine.Topology(vfsHandle)

    return HandlerResult{
        Testament: &Testament{
            Issuer:  c.Subject,
            Subject: c.Issuer,
            Summary: fmt.Sprintf("VFS allocated for pipeline %s at %s",
                                  args.PipelineID, args.BaseVersion),
            Confidence: ConfidenceCommitted,
            Artifacts: []*Artifact{
                {
                    Kind: "vfs_handle",
                    Reference: marshalVFSHandle(vfsHandle),
                    Metadata: map[string]any{
                        "mount_point":    vfsHandle.MountPoint,
                        "base_version":   vfsHandle.BaseVersion,
                        "capacity_mb":    vfsHandle.CapacityMB,
                        "attached":       true,
                        "pipeline_id":    args.PipelineID,
                    },
                },
                {
                    Kind: "vfs_topology",
                    Reference: marshalTopology(topology),
                    Metadata: map[string]any{
                        "cow_layer_count": len(topology.Layers),
                        "parent_pipeline": args.PipelineID,
                    },
                },
            },
            Relations: []Relation{
                {Related: c.Subject.UID, RelatedType: "agent",
                 Relationship: "issuer"},
                {Related: c.Issuer.UID, RelatedType: "agent",
                 Relationship: "subject"},
                {Related: c.Claim.ID, RelatedType: "claim",
                 Relationship: "claim"},
            },
        },
    }
}
```

### 13.6 Programmatic Validation

When the VFS provisioner's testament posts, the board's validator
registry resolves matching validators for each pending validation:

| Validation | Validator | Result |
|---|---|---|
| v-002-receipt | `ReceiptValidator` | passed |
| v-002-attached | `VFSAttachmentValidator` | passed (`vfs_handle.attached == true`) |
| v-002-baseversion | `VFSBaseVersionValidator` | passed (`vfs_handle.base_version == "session_head"`) |
| v-002-capacity | `VFSCapacityValidator` | passed (`vfs_handle.capacity_mb == 1024 >= 1024`) |

All validations pass. The board commits `claim.satisfied` for `claim-002`.
Total elapsed time: a few milliseconds (the underlying CoW allocation is
the dominant cost; validators add microseconds each).

### 13.7 DAG Processor Continuation Resumes

The DAG processor's handler was waiting on `claim-002`'s satisfaction via
a continuation. The continuation worker pool detects `claim.satisfied`
for `claim-002` (linked via `caused_by` relation to `claim-001`),
reactivates the DAG processor's continuation, and the handler resumes.

The handler now has the VFS attachment artifact and completes its own
testament submission.

### 13.8 Orchestrator's Claim Satisfied

When the DAG processor's testament posts:

| Validation | Validator | Result |
|---|---|---|
| v-001-receipt | `ReceiptValidator` | passed |
| v-001-podcount | `PipelinePodCountValidator` | passed (3 pods) |
| v-001-health | `AgentHealthValidator` | passed (all ready) |
| v-001-vfs | `VFSAttachmentValidator` | passed |
| v-001-capacity | `VFSCapacityValidator` | passed |

`claim.satisfied` commits for `claim-001`. The Orchestrator's pending
continuation resumes; it can now dispatch the actual task work to the
allocated pipeline.

### 13.9 Replay

Replay of this session's WAL reconstructs:

- `claim-001` generated, posted, received, progressed, testament_generated,
  testament_posted, validating, satisfied — all with their commit
  sequences.
- `claim-002` similarly.
- Every testament's full artifact set.
- Every validation's verdict with its `reviews` relations.
- Every relation chain — `caused_by` from `claim-002` back to `claim-001`,
  `derived_from` from the DAG processor's `vfs_attachment` artifact to
  the VFS provisioner's `vfs_handle` artifact.
- Every error artifact (none in this happy path).

The Memory Forest harvests both accepted claims as branches. Next
session, when the Orchestrator considers dispatching a similar pipeline,
it can `forest_recall` and find this precedent: "Last time we allocated
a 3-agent pipeline with these scopes, here is what happened and how it
was validated."

### 13.10 Failure Variant

If the VFS provisioner's allocation fails (quota exceeded, scheduler
unavailable, base version unreachable), the failure flows naturally:

```text
VFSProvisionerHandler.Handle returns testament with:
  Confidence: ConfidenceCommitted
  Summary: "VFS allocation failed: quota exceeded for session s1"
  Artifacts: [
    { kind: "error", reference: "QuotaExceeded: 1024 MB requested, 256
                                  MB remaining in session quota" },
    { kind: "error_diagnostic", reference: <full quota state>,
      presentation: { audiences: [user], surfaces: [chat],
                      format: text, title: "VFS allocation failure" } },
  ]

Validator results:
  v-002-receipt: passed (testament arrived)
  v-002-attached: failed (vfs_handle absent or attached=false)
  v-002-baseversion: incomplete (no vfs_handle to inspect)
  v-002-capacity: incomplete

Lifecycle:
  claim-002: claim.validation_failed (because at least one required
             validation failed)

Cascading:
  claim-001's continuation observes claim-002 terminal but failed.
  DAG processor handler reads the failure, produces its own testament
  with the failure surfaced:
    artifacts: [
      { kind: "error", reference: "Pipeline allocation aborted: VFS
                                    provisioning failed" },
      { kind: "vfs_failure_chain", reference: <claim-002 ID> },
    ]
  claim-001: claim.validation_failed

Orchestrator (agent) observes claim.validation_failed on claim-001.
Its tool loop reads the error artifact, sees the VFS quota issue,
and posts a corrective claim:
  "Reclaim unused VFS allocation from session s1 before retrying
   pipeline P allocation"

The corrective claim targets a different service (e.g., a VFS
reclaimer). The flow continues. The user sees, in chat, the error
diagnostic from the VFS provisioner (because it was marked
presentable in CLAIMS_VISIBILITY.md style).
```

Every step is durable, queryable, traversable, and replayable.

## 14. Catalog of Systems to Convert

This section catalogs every Sylk subsystem that should become a service
participant. The catalog is comprehensive but ordered by migration
priority in §17.

### 14.1 Identity Registry

| Field | Value |
|---|---|
| Participant type | `identity_registry` |
| Category | system |
| Scope keys | `{process_uid}` (singleton per process) |
| Determinism | pure |
| Common actions | `task` (allocate, lookup, lineage) |
| Artifact kinds | `uid_allocation`, `identity_lineage`, `generation_record` |
| Programmatic validators | `IdentityUniqueValidator`, `IdentityDeterministicValidator`, `GenerationMonotonicValidator` |

### 14.2 Activation Controller

| Field | Value |
|---|---|
| Participant type | `activation_controller` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | side_effect |
| Common actions | `task` (activate, deactivate, query_tier) |
| Artifact kinds | `activation_record`, `tier_transition`, `replica_set`, `activation_failure` |
| Programmatic validators | `TierAchievedValidator`, `ReplicaCountValidator`, `ActivationDurationValidator` |

### 14.3 DAG Processor

| Field | Value |
|---|---|
| Participant type | `dag_processor` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | side_effect |
| Common actions | `task` (allocate_pipeline, deallocate, query_state, fix_dag) |
| Artifact kinds | `pipeline_pod_state`, `agent_health`, `scope_grants`, `readiness_signal`, `dag_correction_outcome` |
| Programmatic validators | `PipelinePodCountValidator`, `AgentHealthValidator`, `ScopeSubsetValidator`, `DAGAcyclicityValidator` |

### 14.4 Pipeline VFS Provisioner

| Field | Value |
|---|---|
| Participant type | `vfs_provisioner` |
| Category | service |
| Scope keys | `{session_id, pipeline_id}` |
| Determinism | side_effect |
| Common actions | `task` (provision, attach, detach, snapshot) |
| Artifact kinds | `vfs_handle`, `vfs_topology`, `vfs_snapshot`, `vfs_attachment`, `vfs_failure` |
| Programmatic validators | `VFSAttachmentValidator`, `VFSCapacityValidator`, `VFSBaseVersionValidator`, `CoWLayerChainValidator` |

### 14.5 Tool VFS Provisioner

| Field | Value |
|---|---|
| Participant type | `tool_vfs_provisioner` |
| Category | service |
| Scope keys | `{session_id, pipeline_id, agent_uid}` |
| Determinism | side_effect |
| Common actions | `task` (provision, grant_scope, revoke_scope) |
| Artifact kinds | `vfs_handle`, `read_set_grant`, `write_set_grant`, `scope_audit` |
| Programmatic validators | `ScopeSubsetValidator`, `ScopeBoundaryValidator`, `ReadWriteDisjointValidator` |

### 14.6 Global VFS Merger

| Field | Value |
|---|---|
| Participant type | `global_vfs_merger` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | content |
| Common actions | `task` (merge_pipeline, query_conflicts, rollback) |
| Artifact kinds | `merge_outcome`, `conflict_set`, `merged_version_ref`, `merge_failure` |
| Programmatic validators | `MergeAcyclicityValidator`, `ConflictAbsenceValidator`, `MergedVersionReachableValidator` |

### 14.7 Knowledge Graph Writer

| Field | Value |
|---|---|
| Participant type | `kg_writer` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | content |
| Common actions | `task` (embed, store_node, store_edge, supersede) |
| Artifact kinds | `embedding_id`, `vector_dimensions`, `graph_node_id`, `causal_edges`, `embedding_failure` |
| Programmatic validators | `EmbeddingDimensionValidator`, `NodeRetrievableValidator`, `EdgeConsistencyValidator` |

### 14.8 Knowledge Graph Reader

| Field | Value |
|---|---|
| Participant type | `kg_reader` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | content |
| Common actions | `task` (query_semantic, traverse, similarity_search) |
| Artifact kinds | `query_result`, `similarity_scores`, `retrieval_count` |
| Programmatic validators | `QueryResultShapeValidator`, `SimilarityScoreRangeValidator` |

### 14.9 Document DB Writer

| Field | Value |
|---|---|
| Participant type | `doc_db_writer` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | content |
| Common actions | `task` (ingest, supersede, query_status) |
| Artifact kinds | `document_id`, `fulltext_index_status`, `attachment_list`, `ingestion_failure` |
| Programmatic validators | `DocumentIndexedValidator`, `AttachmentListValidator` |

### 14.10 Document DB Reader

| Field | Value |
|---|---|
| Participant type | `doc_db_reader` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | content |
| Common actions | `task` (query_fulltext, query_by_claim) |
| Artifact kinds | `documents`, `relevance_scores` |
| Programmatic validators | `DocumentResultShapeValidator` |

### 14.11 Guardian Subsystem

`CLAIMS.md §14.8` already converts conversation-flow guardian work to
claims. This document extends the conversion to every guardian gate,
following the patterns in `CLAIMS.md §14.8 Tool Execution Control`,
`Content Scanning`, `Git Mutation Gating`, etc.

| Field | Value |
|---|---|
| Participant type | `guardian` (already partial) |
| Category | service (programmatic gates) and agent (conversational responses) |
| Scope keys | `{session_id}` |
| Determinism | side_effect for gates, content for scans |
| Common actions | `task` (approve_command, approve_plan, scan_content, gate_git, content_scan, rollback) |
| Artifact kinds | per existing `CLAIMS.md §14.8` taxonomy |
| Programmatic validators | `GuardianPolicyMatchValidator`, `UserApprovalPresentValidator`, `BranchProtectionValidator`, `DiffFindingsAbsentValidator` |

The conversion preserves the existing conversational guardian agent for
user-facing dialog. The deterministic gates (policy match, content
scan, git mutation, diff review) become a parallel guardian service
participant that handles the programmatic side.

### 14.12 Boot Sequencer

| Field | Value |
|---|---|
| Participant type | `boot_sequencer` |
| Category | system |
| Scope keys | `{process_uid}` (singleton per process) |
| Determinism | side_effect |
| Common actions | `task` per phase (setup, detect, allocate, ingest, commit, finalize) |
| Artifact kinds | `setup_complete`, `detect_result`, `allocate_outcome`, `ingest_status`, `commit_ref`, `finalize_signal`, `boot_failure` |
| Programmatic validators | per-phase contract validators, `BootPhaseOrderValidator`, `BootDurationValidator` |

### 14.13 Tool Runtime

| Field | Value |
|---|---|
| Participant type | `tool_runtime` |
| Category | service |
| Scope keys | `{session_id, agent_uid}` |
| Determinism | side_effect |
| Common actions | `task` (execute_tool, validate_invocation, query_policy) |
| Artifact kinds | `tool_started`, `tool_completed`, `tool_audit_record`, `tool_blocked`, `tool_policy_decision` |
| Programmatic validators | `ToolPolicyAllowValidator`, `ToolExecutionModeValidator`, `ToolScopeBoundaryValidator`, `ToolDurationValidator` |

### 14.14 LLM Provider Gateway

| Field | Value |
|---|---|
| Participant type | `provider_gateway` |
| Category | service |
| Scope keys | `{provider_type, session_id}` (one per provider per session) |
| Determinism | nondeterministic (LLM output) |
| Common actions | `task` (complete, complete_streaming, embedding, count_tokens) |
| Artifact kinds | `llm_response`, `usage`, `cache_hit`, `retry_record`, `provider_failure`, `rate_limit_encounter` |
| Programmatic validators | `ResponsePresentValidator`, `UsageInBudgetValidator`, `RateLimitNotExceededValidator` |

### 14.15 Session Manager

| Field | Value |
|---|---|
| Participant type | `session_manager` |
| Category | service |
| Scope keys | `{process_uid}` (process-singleton; session boards are children) |
| Determinism | side_effect |
| Common actions | `task` (open_session, close_session, persist, restore) |
| Artifact kinds | `session_handle`, `session_state`, `persist_outcome`, `restore_outcome` |
| Programmatic validators | `SessionStateConsistentValidator`, `SessionPersistenceValidator` |

### 14.16 Fabric Subscriber

| Field | Value |
|---|---|
| Participant type | `fabric_subscriber` |
| Category | service |
| Scope keys | `{session_id}` |
| Determinism | content |
| Common actions | `task` (attach, query_lens, harvest) |
| Artifact kinds | `lens_query_result`, `harvest_outcome`, `subscription_state` |
| Programmatic validators | `LensQueryShapeValidator`, `SubscriptionAttachedValidator` |

### 14.17 Bus Transport

The Guide event bus itself is unusual: it is the transport for every
claim and delta, including claims about its own state. To avoid
circular self-reference, the bus is not modeled as a claim subject for
its primary transport function. Bus configuration changes (topic
registration, subscription policy updates, capacity adjustments) are
modeled as claims against a `bus_administrator` service participant,
not against the bus itself.

| Field | Value |
|---|---|
| Participant type | `bus_administrator` |
| Category | system |
| Scope keys | `{process_uid}` |
| Determinism | side_effect |
| Common actions | `task` (register_topic, adjust_capacity, drain) |
| Artifact kinds | `topic_registration`, `capacity_record`, `drain_outcome` |
| Programmatic validators | `TopicNameValidatior`, `CapacityWithinBudgetValidator` |

## 15. Invariants

These invariants formalize the contract this document adds. They are
required for any implementation to be correct.

1. Every Sylk subsystem that mutates board-visible state is a
   participant with canonical identity.
2. Every participant's identity follows the canonical `ParticipantRef`
   shape. Display names alone are insufficient where UID is available.
3. Service identity is deterministic: same (type, scope_keys) yield
   the same UID across restarts.
4. Service handlers register at process boot and cannot be hot-swapped
   at runtime.
5. Every handler invocation runs within a tracked, bounded goroutine
   scope.
6. Every handler queue is bounded; overflow produces a durable
   `claim.receipt_failed` with a backpressure artifact.
7. Handler panics are recovered; recovery produces a
   `claim.testament_generation_failed` with an `error_trace` artifact.
8. Every handler-returned testament is bit-identical in shape to one
   produced by an agent.
9. Programmatic validators are deterministic; non-deterministic
   implementations are declared and treated as agentic for replay
   purposes.
10. Validators returning `Status == errored` do not fail the claim
    outright; lower-priority validators are tried before agentic
    fallback.
11. Every infrastructure outcome — success, partial success, refusal,
    impossibility, timeout, policy denial, infrastructure error — is a
    testament with artifacts, never a Go error that bypasses the board.
12. Lifecycle compression produces wire-identical state sequences; the
    only difference from non-compressed lifecycle is that commits are
    coincident in time.
13. Replay reconstructs every lifecycle state, every testament, every
    artifact, every validation verdict, and every relation regardless
    of producer category.
14. Idempotency keys are content-derived, not ID-derived, so replayed
    or re-issued claims with equivalent content do not re-execute the
    handler.
15. Non-idempotent handlers carry an `idempotency_nonce` supplied by
    the issuer; replay still skips re-execution because the nonce
    matches the prior testament.
16. The board's amplifier does not branch on participant category for
    delta emission.
17. The Guide bus does not branch on participant category for
    transport.
18. The UI bridge does not branch on participant category for delta
    consumption; rendering badges may be category-specific but
    routing is not.
19. Service handler dispatch and validator dispatch obey the
    project-wide lock-order discipline: board > dispatcher >
    registry; reversed acquisition is not permitted.
20. Cancellation, shutdown, and interruption produce durable artifacts
    and lifecycle transitions; no in-flight handler is killed silently.

## 16. Cross-Document Reconciliation

This document does not stand alone. It modifies the phrasing and
contracts of the existing claims documents. The reconciliation rules:

### 16.1 CLAIMS.md

Required updates to `docs/CLAIMS.md`:

1. §1 Motivation: add a sentence acknowledging that infrastructure
   subsystems are participants.
2. §2.3 Claim definition: change "issued by one agent against another"
   to "issued by one participant against another."
3. §2.5 Testament definition: change "When a subject completes work
   on a claim, they issue a testament" to "When a subject (agent or
   service) completes work on a claim, they issue a testament."
4. §4.2 Universal Base Fields: rename `AgentID` to `ParticipantID`
   with a backward-compatibility note that `AgentID` remains a derived
   projection during migration.
5. §5 Agent Intake: rename to "Participant Intake" and split into two
   subsections: 5.A Agent discipline (existing content) and 5.B
   Service discipline (cross-reference to this document).
6. §14 Conversion plan: add a tier (Tier 13: Infrastructure
   Participants) that references this document for VFS, KG, doc DB,
   DAG processor, identity registry, activation controller, boot
   sequencer, tool runtime, provider gateway.

### 16.2 CLAIMS_AND_DELTAS.md

Required updates to `docs/CLAIMS_AND_DELTAS.md`:

1. §7 Canonical Agent Reference: rename to "Canonical Participant
   Reference" with the generalized `ParticipantRef` schema including
   `category` field.
2. §8 Object References: no change; refs already participant-agnostic.
3. §13 Delivery Topics: add service-UID topic patterns parallel to the
   agent topic patterns. Both follow the same grammar; only the UID
   namespace differs.
4. §17 Phased Plan: add a sub-phase for service-handler-dispatch
   wire-format equivalence testing.

### 16.3 CLAIMS_AND_TESTAMENTS_LIFECYCLE.md

Required updates to `docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`:

1. §1 Core Principle: add a sentence noting that every lifecycle fact
   applies equally to agent and service participants.
2. §8 Receiver Semantics: rename "Target Agent" to "Target Participant"
   and "Source Agent" to "Source Participant"; add a "Service
   Participant" subsection describing the handler dispatch wake source.
3. §10 Consultations/Challenges/Guardian Checks: note that the same
   patterns apply to service-to-service consultations (e.g., a DAG
   processor consulting a VFS provisioner).
4. §15 Minimal Implementation Contract: add item 11 "Infrastructure
   outcomes are testaments with artifacts, not Go-error returns" and
   item 12 "Service handler dispatch follows the same lifecycle
   contracts as agent dispatch."

### 16.4 CLAIMS_VISIBILITY.md

Required updates to `docs/CLAIMS_VISIBILITY.md`:

1. §2.2 User-visible does not mean user-only: no change; already
   participant-agnostic.
2. §5.1 Validators inspect presentation artifacts: note that
   programmatic validators read artifacts via the same board API as
   agentic validators.
3. §6 Runtime Flow: note that service-produced testaments carry
   presentation metadata just like agent-produced testaments. A
   guardian-denial testament carrying a user-visible explanation is a
   first-class presentation case.

### 16.5 CLAUDE.md

Required updates to `CLAUDE.md` (project rules) — none. The existing
rules apply uniformly to service handlers, validators, and the
runtime support described here. Specifically:

- No magic numbers: handler timeouts, queue capacities, and validator
  priorities are derived from declared participant metadata.
- Cyclomatic complexity < 4: handlers and validators decompose into
  named sub-steps; the dispatcher's invocation flow uses helper
  methods.
- No untracked goroutines: every dispatch, handler, validator, and
  continuation runs within a tracked scope.
- No unbounded growth: every queue and accumulator is bounded; every
  cache has explicit eviction policy.
- No drops/leaks/races: backpressure is observable; cancellation
  produces artifacts; shutdown drains deterministically.
- Modern Go structures (Go 1.25+): use generics for handler registry,
  type-safe enums via string types with `Valid()` methods, and
  `context.Context` propagation throughout.

## 17. Phased Implementation Plan

The phased plan follows the same structure as the other claims
documents: each phase has a description, examples, acceptance criteria,
unit tests, vektra/mockery integration tests, E2E tests, and explicit
failure/race/deadlock test cases. Phases are ordered to avoid hybrid
states; each phase is independently shippable.

### Phase 0: Vocabulary and Doc Reconciliation

Phase 0 establishes the vocabulary in the documents before any code
changes. The goal is to make the participant taxonomy and the
two-consumption-disciplines framing canonical so subsequent code work
has a clear specification.

#### Item 0.1: Rename AgentRef to ParticipantRef in Specs

**Description:** Update `docs/CLAIMS.md`, `docs/CLAIMS_AND_DELTAS.md`,
`docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`, and `docs/CLAIMS_VISIBILITY.md`
to use `ParticipantRef` with a `category` field. Preserve `AgentRef`
in code as a type alias during migration.

**Acceptance criteria:**

- All four claims docs use `ParticipantRef` consistently.
- `category` field is documented with the enum from §3.2.
- Backward-compatibility note clarifies that existing `AgentRef` calls
  produce a `ParticipantRef` with `Category: agent`.

**Unit tests:**

- Doc-lint test: no remaining bare `AgentRef` outside designated
  migration sections.
- Type test: `AgentRef` type alias produces `ParticipantRef` with
  `Category: agent`.

**Integration tests with vektra/mockery:**

- Mock `IdentityResolver` returns canonical `ParticipantRef`; verify
  category is correctly set per resolved identity.

**E2E tests:**

- All existing E2E tests pass with the renamed reference type.
- New E2E test: identity resolution for a service participant produces
  `Category: service` ref.

**Failure/race/deadlock tests:**

- Concurrent resolution requests across categories do not interleave
  category fields.
- Identity registry rotation during resolution does not produce a ref
  with stale category.

#### Item 0.2: Document Two Consumption Disciplines

**Description:** Update `docs/CLAIMS.md §5` to describe both
disciplines. Cross-reference this document.

**Acceptance criteria:**

- §5 splits cleanly into agent and service subsections.
- Cross-reference points to §6 of this document.
- The four-async-skills discipline is marked as agent-specific.
- A new subsection describes the service handler registration and
  dispatch model at the spec level (full implementation details defer
  to this document).

**Unit tests:**

- Doc-lint test: §5 contains both agent and service subsections.
- Doc-lint test: discipline-comparison table from §6.3 is referenced.

**Integration tests with vektra/mockery:**

- Mock doc reader verifies both subsections render correctly.

**E2E tests:**

- Spec-driven E2E test (run agent and service paths in parallel; both
  succeed using the documented contracts).

#### Item 0.3: Document Programmatic Validation

**Description:** Update `docs/CLAIMS.md §2.4` and §2.8 to describe
programmatic validation alongside agentic validation.

**Acceptance criteria:**

- §2.4 Validation Types acknowledges programmatic evaluators exist for
  every type, not only receipt.
- §2.8 Validation Flow describes the validator-registry-first
  evaluation path with agentic fallback.
- A new validation type field `Deterministic` is documented.

**Unit tests:**

- Doc-lint test: §2.4 references programmatic validators.
- Doc-lint test: §2.8 describes the validator-registry-first flow.

**Integration tests with vektra/mockery:**

- Mock `ValidatorRegistry` returning programmatic validators is
  consulted before agentic fallback.

**E2E tests:**

- Mixed-validation E2E (receipt + programmatic contract + agentic
  inspection) completes correctly.

### Phase 1: Core Abstractions

Phase 1 lands the core Go types and interfaces in `core/claims` without
wiring them into any production path. Tests prove the abstractions work
against synthetic services.

#### Item 1.1: Add ParticipantCategory Enum

**Description:** Add `ParticipantCategory` type to `core/claims` with
the four values and a `Valid()` method.

**Acceptance criteria:**

- Type compiles and is documented.
- `Valid()` returns true for the four valid values, false otherwise.
- JSON marshaling preserves the string form.

**Unit tests:**

- Each of the four values returns `Valid() == true`.
- Unknown values return `Valid() == false`.
- JSON round trip preserves string form.

**E2E tests:**

- Type used in a synthetic delta envelope encodes and decodes correctly.

**Failure/race/deadlock tests:**

- None applicable (pure type).

#### Item 1.2: Add Generalized ParticipantRef

**Description:** Add `ParticipantRef` struct with all fields from §5.2.
Make `AgentRef` a type alias that constructs `ParticipantRef` with
`Category: agent`.

**Acceptance criteria:**

- Struct compiles and is documented.
- JSON marshaling preserves all fields including optional ones.
- Backward-compat: existing code that creates `AgentRef` still compiles
  and produces a `ParticipantRef` with category=agent.

**Unit tests:**

- JSON round trip preserves all fields.
- Optional fields omitted from JSON when zero.
- AgentRef alias produces correct category.
- Degraded ref (`Unresolved: true`) preserves resolution reason.

**Integration tests with vektra/mockery:**

- Mock identity resolver returns ParticipantRefs across all categories;
  receivers handle each correctly.

**E2E tests:**

- Synthetic delta with all four category refs encodes and decodes.

**Failure/race/deadlock tests:**

- Concurrent JSON marshaling does not corrupt shared label maps.

#### Item 1.3: Add ServiceHandler Interface

**Description:** Add `ServiceHandler`, `HandlerContext`, `HandlerResult`,
and `HandlerContinuation` types to `core/claims`.

**Acceptance criteria:**

- Types compile and are documented.
- Interfaces define the methods from §5.4.
- A no-op implementation can be instantiated for testing.

**Unit tests:**

- Synthetic handler implements the interface and returns a valid
  `HandlerResult`.
- A handler returning nil testament without a continuation fails the
  handler-contract test.
- A handler panicking is recovered.

**Integration tests with vektra/mockery:**

- Mock handler with mockery-generated mock verifies dispatch
  invocations.

**E2E tests:**

- A test service registers a handler, receives a synthetic claim, and
  produces a testament. End-to-end commit and validation succeed.

**Failure/race/deadlock tests:**

- Handler panic during scope teardown is recovered and produces
  `error_trace`.
- Handler with deadline exceeded produces `tool_timeout`.

#### Item 1.4: Add ProgrammaticValidator Interface

**Description:** Add `ProgrammaticValidator`, `ValidationResult`, and
`ValidatorRegistry` types to `core/claims`.

**Acceptance criteria:**

- Types compile and are documented.
- Interfaces define the methods from §5.3 and §5.6.
- A no-op validator can be instantiated.

**Unit tests:**

- Synthetic validator implements interface and returns valid
  `ValidationResult`.
- Validator panicking is recovered.
- Registry returns validators in priority order.

**Integration tests with vektra/mockery:**

- Mock validator returns a fixed verdict; dispatcher records it
  correctly.
- Multiple matching validators in priority order: only the first
  non-errored verdict is used.

**E2E tests:**

- A test validator runs against a synthetic testament; verdict matches
  expectations.

**Failure/race/deadlock tests:**

- Concurrent validation evaluations across claims do not corrupt
  registry state.
- Validator panic during registry lookup is recovered.

#### Item 1.5: Add Service Identity Derivation

**Description:** Add `DeriveServiceUID`, `DeriveSystemUID`, and
`DeriveExternalUID` functions.

**Acceptance criteria:**

- Functions compile and are documented.
- Same inputs produce same outputs.
- Different inputs produce different outputs (modulo unlikely SHA
  collisions).
- Scope keys are sorted lexicographically for determinism.

**Unit tests:**

- Identical inputs produce identical UIDs.
- Different scope key orderings produce same UID (sorted internally).
- Different scope key values produce different UIDs.
- Empty scope keys produce a singleton UID for the type.
- Generation field is preserved separately (not in UID).

**Integration tests with vektra/mockery:**

- Mock identity registry derives UIDs for a set of test services; all
  unique and deterministic.

**E2E tests:**

- Restart-equivalence test: derive UID before restart, restart process,
  derive same UID after restart; assert equality.

**Failure/race/deadlock tests:**

- Concurrent UID derivation does not corrupt input maps.

#### Item 1.6: Add Handler Determinism Levels

**Description:** Add `HandlerDeterminism` enum and require handlers to
declare their level.

**Acceptance criteria:**

- Enum compiles with the four values from §11.4.
- Handler registration validates the declared level.

**Unit tests:**

- Each value compiles and is `Valid()`.
- Unknown values are rejected at registration.

**Integration tests with vektra/mockery:**

- Mock handler registration verifies determinism level is recorded.

**E2E tests:**

- Pure handler re-executed on replay produces identical output.
- Content handler re-executed produces identical content hash.

**Failure/race/deadlock tests:**

- A handler declaring `pure` but behaving nondeterministically is
  detected during replay audit; an error artifact is generated.

### Phase 2: Service Handler Dispatch

Phase 2 builds the dispatcher that consumes `claim.posted` deltas and
invokes registered handlers.

#### Item 2.1: Service Registry Implementation

**Description:** Implement `ServiceRegistry` in `core/claims`. Boot-time
registration only; no hot-swap.

**Acceptance criteria:**

- Register fails on duplicate type registration.
- Resolve returns the registered handler.
- Resolve returns `(nil, false)` for unknown types.
- `ParticipantTypes()` returns the registered set.

**Unit tests:**

- Register and resolve a synthetic handler.
- Duplicate registration fails with a typed error.
- Resolve before any registration returns false.
- ParticipantTypes returns the expected set in deterministic order.

**Integration tests with vektra/mockery:**

- Mock handler registration validates the participant type matches the
  handler's declared type.

**E2E tests:**

- Register two handlers, resolve both, dispatch deltas to both.

**Failure/race/deadlock tests:**

- Concurrent registration attempts during boot resolve deterministically.
- Resolve during registration is safe (read lock).

#### Item 2.2: Per-Handler Bounded Queue

**Description:** Each registered handler gets a bounded queue. Capacity
derives from the handler's declared expected concurrency and p99
duration; no magic numbers.

**Acceptance criteria:**

- Queue capacity derives from declared metadata.
- Enqueue blocks until space available or overflow policy triggers.
- Overflow produces `claim.receipt_failed` with
  `dispatcher_backpressure` artifact.
- Telemetry counter records overflow.

**Unit tests:**

- Capacity derivation matches the formula in §8.3 for a set of test
  inputs.
- Overflow triggers the documented failure outcome.
- Queue drains correctly on shutdown.

**Integration tests with vektra/mockery:**

- Mock board records the `claim.receipt_failed` lifecycle event under
  overflow.
- Mock telemetry sink records overflow counter increment.

**E2E tests:**

- Synthetic high-burst test fills the queue; subsequent claims fail
  with the documented artifact.
- After drain, queue accepts new claims.

**Failure/race/deadlock tests:**

- Concurrent enqueue and shutdown produces no leaked goroutines.
- Overflow under race with handler completion does not double-fail
  the claim.

#### Item 2.3: Per-Claim Bounded Goroutine Scope

**Description:** Every handler invocation runs in a tracked child scope.

**Acceptance criteria:**

- Scope is child of dispatcher's per-handler scope.
- Deadline derives from `min(claim.deadline, handler_default_timeout)`.
- Cancellation propagates to handler context.
- Scope cleanup is deterministic.

**Unit tests:**

- Scope deadline matches the formula.
- Scope cancellation triggers handler context cancellation.
- Handler observing context cancellation returns within bounded time.

**Integration tests with vektra/mockery:**

- Mock scope manager records every spawn and join.
- Mock handler observes cancellation and returns a cancellation
  testament.

**E2E tests:**

- Long-running handler with cancellation produces `interrupted` error
  artifact.

**Failure/race/deadlock tests:**

- Handler that spawns its own goroutines without scope tracking
  triggers a test-time assertion failure.
- Scope shutdown during handler execution does not leak goroutines.

#### Item 2.4: Dispatcher Subscription Setup

**Description:** Dispatcher subscribes to `claim.posted` topics for
every registered service UID at boot.

**Acceptance criteria:**

- Subscription topics use service UIDs, not just types.
- Subscriptions attach during boot under the `boot.subscriptions_attached`
  claim.
- Each subscription is durably recorded as an artifact.

**Unit tests:**

- Subscription topic format matches the grammar from §8.1.
- Subscription record artifact includes topic, handler ID, and UID.

**Integration tests with vektra/mockery:**

- Mock bus records each subscription with topic.
- Mock boot sequencer commits the subscriptions_attached testament.

**E2E tests:**

- Process boot completes with all expected subscriptions attached.

**Failure/race/deadlock tests:**

- Bus subscription failure produces a boot phase failure with a
  diagnostic artifact.
- Concurrent subscription attempts during boot resolve deterministically.

#### Item 2.5: Handler Invocation Flow Implementation

**Description:** Implement the 12-step flow from §8.4.

**Acceptance criteria:**

- Each step is a named method with cyclomatic complexity less than 4.
- The full flow is testable end-to-end with a synthetic claim.
- Telemetry counters from §12.4 increment correctly.

**Unit tests:**

- Each step is unit-tested in isolation.
- The full flow with a happy-path claim produces the expected
  testament.

**Integration tests with vektra/mockery:**

- Mock board, mock handler, mock telemetry; verify each step's
  observable side effect.

**E2E tests:**

- A real service handler dispatched against a real session board
  produces a valid satisfied claim.

**Failure/race/deadlock tests:**

- Step 4 (UID match) failing produces `claim.receipt_failed`.
- Step 8 (handler) panic produces `claim.testament_generation_failed`.
- Step 10b (continuation) failing produces `claim.progress_failed`.

#### Item 2.6: Async Continuation Support

**Description:** Implement `HandlerContinuation` and the continuation
worker pool.

**Acceptance criteria:**

- Continuation worker pool is bounded.
- Continuations resume on precondition satisfaction.
- Process restart re-reads pending continuations from the durable
  store.

**Unit tests:**

- Synthetic continuation resumes when its preconditions are met.
- Restart-equivalence: continuation persists, restart, resume produces
  the same testament.

**Integration tests with vektra/mockery:**

- Mock continuation store records each registration and resolution.
- Mock worker pool dispatches resumptions in order.

**E2E tests:**

- A long-running handler returning a continuation produces the
  testament after the precondition arrives.

**Failure/race/deadlock tests:**

- Continuation worker pool overflow produces
  `continuation_backpressure` artifact.
- Resume during shutdown produces `shutdown` artifact.

### Phase 3: Programmatic Validator Dispatch

Phase 3 wires validator registry into the board's `EvaluateValidation`
path.

#### Item 3.1: Validator Registry Implementation

**Description:** Implement `ValidatorRegistry`.

**Acceptance criteria:**

- Register accepts validators with priority.
- Resolve returns validators in descending priority order.
- Validator IDs are unique; duplicate registration fails.

**Unit tests:**

- Register and resolve return validators in priority order.
- Duplicate registration with same ID fails.
- Resolve for no matching validators returns empty.

**Integration tests with vektra/mockery:**

- Mock validator registers; resolve returns expected order.

**E2E tests:**

- Real validator registered, real testament submitted, validator
  evaluated.

#### Item 3.2: Board EvaluateValidation Hook

**Description:** Wire the registry into the board's existing
`EvaluateValidation` path. Programmatic validators run first; agentic
fallback only if no programmatic verdict is available.

**Acceptance criteria:**

- Programmatic validators are tried in priority order before agentic.
- A programmatic verdict commits `validation.evaluated` with the
  validator's ID in the status history.
- Agentic fallback runs only when all programmatic validators errored.

**Unit tests:**

- Validator passing produces `ValidationStatusPassed` delta.
- Validator failing produces `ValidationStatusFailed` delta.
- Validator erroring with no fallback produces
  `ValidationStatusErrored` delta.
- All validators erroring triggers agentic fallback.

**Integration tests with vektra/mockery:**

- Mock board commits validations with validator IDs.
- Mock agentic evaluator is only called when programmatic validators
  all error.

**E2E tests:**

- Synthetic claim with programmatic validations only completes without
  any agentic evaluator wakeup.
- Mixed validation claim completes programmatic first, then agentic.

**Failure/race/deadlock tests:**

- Validator panic during evaluation is recovered.
- Concurrent validator evaluations on different validations do not
  interfere.

#### Item 3.3: Validator Determinism Audit

**Description:** Replay-time auditing of pure and content
deterministic validators.

**Acceptance criteria:**

- Pure validators are re-executed during replay and outputs are
  compared.
- Divergence produces a `validator_nondeterministic` error artifact.
- The original verdict is preserved; replay does not overwrite
  committed state.

**Unit tests:**

- Pure validator re-execution matches original.
- Synthetic nondeterministic validator declared as pure triggers
  divergence detection.

**Integration tests with vektra/mockery:**

- Mock replay reducer compares validator outputs.

**E2E tests:**

- Audit session runs the replay pipeline over a recorded WAL; no
  divergence.

#### Item 3.4: Initial Validator Catalog

**Description:** Implement the validator catalog from §9.5.

**Acceptance criteria:**

- Every validator from the catalog is implemented as pure Go.
- Each validator has unit tests covering pass, fail, incomplete, and
  errored paths.
- Each validator declares its determinism level.

**Unit tests:**

- Each validator tested with a representative set of artifacts.
- Boundary conditions: missing fields, malformed JSON, edge values.

**Integration tests with vektra/mockery:**

- Each validator registered and dispatched through the registry.

**E2E tests:**

- A synthetic claim with each validator type completes correctly.

**Failure/race/deadlock tests:**

- Validator with malformed artifact does not panic; returns errored
  with `error_diagnostic`.

### Phase 4: Lifecycle Compression

Phase 4 implements single-transaction commits for synchronous
handlers.

#### Item 4.1: CompressLifecycle Board Method

**Description:** Implement `CompressLifecycle` on `ClaimsBoard`.

**Acceptance criteria:**

- Atomic commit of all named lifecycle states within one transaction.
- Returns error if transaction cannot commit atomically.
- Replay reconstructs identical state regardless of compression.

**Unit tests:**

- Successful compression commits all states in one transaction.
- Failure during compression rolls back all states.
- Replay of compressed lifecycle reconstructs state by sequence order.

**Integration tests with vektra/mockery:**

- Mock WAL records each compressed transaction as a contiguous run.
- Mock delta publisher emits every lifecycle state in sequence.

**E2E tests:**

- Synchronous handler dispatched and satisfied within one transaction.
- Replay produces identical state.

**Failure/race/deadlock tests:**

- Lock contention during compression aborts the transaction; partial
  state is not committed.
- Concurrent compressions on different claims do not interfere.

#### Item 4.2: Dispatcher Compression Integration

**Description:** Wire `CompressLifecycle` into the dispatcher's
synchronous-handler path.

**Acceptance criteria:**

- Synchronous handlers automatically use compression unless they opt
  out via `HandlerResult.NoCompression`.
- Async handlers (with continuations) do not use compression.
- Compression disabled when any required validation is agentic.

**Unit tests:**

- Synchronous handler dispatch uses compression.
- Async handler dispatch does not use compression.
- Mixed validation (programmatic + agentic) disables compression.

**Integration tests with vektra/mockery:**

- Mock dispatcher verifies compression invocations.

**E2E tests:**

- Synchronous service handler completes a claim in one observable
  transaction.

**Failure/race/deadlock tests:**

- Compression failure falls back to uncompressed lifecycle without
  losing state.

### Phase 5: Identity Registry as a Service

Phase 5 is the first concrete migration. The identity registry is
chosen first because every other service participant depends on it.

#### Item 5.1: Identity Registry Handler

**Description:** Convert the existing identity registry into a service
handler.

**Acceptance criteria:**

- `identity_registry` service handler implements `allocate`, `lookup`,
  and `lineage` actions.
- Handler is `pure` determinism level.
- Existing identity-allocation call sites route through the service.

**Unit tests:**

- Allocate returns same UID for same inputs.
- Lookup returns the same record for a known UID.
- Lineage returns the parent UID chain.

**Integration tests with vektra/mockery:**

- Mock service dispatcher routes identity claims to the handler.

**E2E tests:**

- Process boot allocates UIDs for every registered service via the
  identity registry service.

**Failure/race/deadlock tests:**

- Concurrent allocation requests for the same logical service produce
  one UID (idempotency).
- Allocation during shutdown produces `shutdown` artifact.

#### Item 5.2: Identity Registry Validators

**Description:** Implement `IdentityUniqueValidator`,
`IdentityDeterministicValidator`, `GenerationMonotonicValidator`.

**Acceptance criteria:**

- Unique validator detects UID collisions.
- Deterministic validator detects UIDs that do not match
  `DeriveServiceUID`.
- Monotonic validator detects generation regressions.

**Unit tests:**

- Each validator tested with passing and failing artifact inputs.

**Integration tests with vektra/mockery:**

- Mock registry validator is invoked for every allocation testament.

**E2E tests:**

- A test colliding allocation triggers validation failure.

### Phase 6: DAG Processor as a Service

Phase 6 converts the DAG processor.

#### Item 6.1: DAG Processor Handler

**Description:** Convert the existing DAG processor.

**Acceptance criteria:**

- `dag_processor` service handler implements `allocate_pipeline`,
  `deallocate`, `query_state`, and `fix_dag` actions.
- Handler runs deterministically up to scheduler side effects
  (declared `side_effect` determinism).
- All existing DAG-related orchestrator paths route through the service.

**Unit tests:**

- Allocate pipeline produces expected pod state.
- Deallocate cleans up tracked state.
- Query state returns current snapshot.
- Fix DAG corrections produce valid task graph.

**Integration tests with vektra/mockery:**

- Mock scheduler responds with synthetic pod states; handler returns
  testament.

**E2E tests:**

- Real pipeline allocation through the service handler.

**Failure/race/deadlock tests:**

- Pod allocation failure produces error artifacts in testament.
- Concurrent pipeline allocations do not race.

#### Item 6.2: DAG Processor Validators

**Description:** Implement `PipelinePodCountValidator`,
`AgentHealthValidator`, `ScopeSubsetValidator`, `DAGAcyclicityValidator`.

**Acceptance criteria:**

- Each validator implements the corresponding quality bar from §14.3.

**Unit tests:**

- Each validator tested with passing, failing, and incomplete inputs.

**Integration tests with vektra/mockery:**

- Validators dispatched through registry for DAG processor testaments.

**E2E tests:**

- End-to-end pipeline allocation with validator chain completes
  satisfied.

### Phase 7: Pipeline VFS Provisioner

Phase 7 converts the pipeline VFS provisioner.

#### Item 7.1: VFS Provisioner Handler

**Description:** Convert pipeline VFS provisioning into a service
handler.

**Acceptance criteria:**

- `vfs_provisioner` service handler implements `provision`, `attach`,
  `detach`, `snapshot` actions.
- Handler is `side_effect` determinism.
- Existing pipeline-VFS allocation paths route through the service.

**Unit tests:**

- Provision allocates with declared capacity and base version.
- Attach mounts at the requested mount point.
- Detach cleans up tracked state.
- Snapshot produces a snapshot reference.

**Integration tests with vektra/mockery:**

- Mock CoW engine responds with synthetic handles; handler returns
  testament.

**E2E tests:**

- Real VFS allocation through the service handler.

**Failure/race/deadlock tests:**

- Quota exceeded produces `error` artifact.
- Concurrent allocations for different pipelines do not race.

#### Item 7.2: VFS Validators

**Description:** Implement `VFSAttachmentValidator`,
`VFSCapacityValidator`, `VFSBaseVersionValidator`,
`CoWLayerChainValidator`.

**Acceptance criteria:**

- Each validator implements the corresponding quality bar.

**Unit tests/integration/E2E:** as above.

### Phase 8: Tool VFS Provisioner

Phase 8 converts the per-tool-execution VFS provisioner.

#### Item 8.1: Tool VFS Handler

**Description:** Same shape as Phase 7 but scoped per
(session, pipeline, agent_uid).

**Acceptance criteria, tests:** same pattern as Phase 7.

#### Item 8.2: Tool VFS Validators

**Description:** Implement `ScopeSubsetValidator`,
`ScopeBoundaryValidator`, `ReadWriteDisjointValidator`.

### Phase 9: Global VFS Merger

Phase 9 converts the global CoW versioned VFS merge path.

#### Item 9.1: Global VFS Merger Handler

**Description:** Convert pipeline-to-global merges into a service
handler.

**Acceptance criteria, tests:** same pattern.

#### Item 9.2: Merge Validators

**Description:** Implement `MergeAcyclicityValidator`,
`ConflictAbsenceValidator`, `MergedVersionReachableValidator`.

### Phase 10: Knowledge Graph

Phase 10 converts KG read and write paths.

#### Item 10.1: KG Writer Handler

**Description:** Convert embedding storage, node storage, edge storage.

#### Item 10.2: KG Reader Handler

**Description:** Convert semantic queries, traversals, similarity
search.

#### Item 10.3: KG Validators

**Description:** Implement `EmbeddingDimensionValidator`,
`NodeRetrievableValidator`, `EdgeConsistencyValidator`,
`QueryResultShapeValidator`, `SimilarityScoreRangeValidator`.

### Phase 11: Document DB

Phase 11 converts doc DB read and write paths.

#### Item 11.1: Doc DB Writer Handler

**Description:** Convert ingestion paths.

#### Item 11.2: Doc DB Reader Handler

**Description:** Convert fulltext queries (using Bleve per CLAUDE.md).

#### Item 11.3: Doc DB Validators

**Description:** Implement `DocumentIndexedValidator`,
`AttachmentListValidator`, `DocumentResultShapeValidator`.

### Phase 12: Guardian Remaining Gates

Phase 12 converts the deterministic guardian gates not already covered
in `CLAIMS.md §14.8`.

#### Item 12.1: Guardian Service Handler

**Description:** Convert content scanning, branch protection, diff
review, rollback, and command/fetch approval gates into a service
handler. Conversational guardian responses remain agent-driven.

**Acceptance criteria, tests:** same pattern as earlier phases.

#### Item 12.2: Guardian Validators

**Description:** Implement `GuardianPolicyMatchValidator`,
`UserApprovalPresentValidator`, `BranchProtectionValidator`,
`DiffFindingsAbsentValidator`.

### Phase 13: Boot Sequencer

Phase 13 converts the process boot sequence.

#### Item 13.1: Boot Sequencer Handler

**Description:** Convert boot phases into a service handler. Each
phase is a claim against the boot sequencer; its testament records
the phase's outcome.

#### Item 13.2: Boot Validators

**Description:** Implement per-phase validators and
`BootPhaseOrderValidator`, `BootDurationValidator`.

### Phase 14: Tool Runtime

Phase 14 converts the tool runtime.

#### Item 14.1: Tool Runtime Handler

**Description:** Convert tool execution into a service handler. The
existing tool runtime's `recordToolCallStart` and `recordToolCallEnd`
become testament artifacts in this model.

#### Item 14.2: Tool Runtime Validators

**Description:** Implement `ToolPolicyAllowValidator`,
`ToolExecutionModeValidator`, `ToolScopeBoundaryValidator`,
`ToolDurationValidator`.

### Phase 15: LLM Provider Gateway

Phase 15 converts the LLM provider gateway.

#### Item 15.1: Provider Gateway Handler

**Description:** Convert LLM calls into a service handler. The handler
is `nondeterministic` determinism; replay trusts the stored testament.

#### Item 15.2: Provider Gateway Validators

**Description:** Implement `ResponsePresentValidator`,
`UsageInBudgetValidator`, `RateLimitNotExceededValidator`.

### Phase 16: Activation Controller

Phase 16 converts the activation controller.

#### Item 16.1: Activation Handler

**Description:** Convert tier transitions and replica management.

#### Item 16.2: Activation Validators

**Description:** Implement `TierAchievedValidator`,
`ReplicaCountValidator`, `ActivationDurationValidator`.

### Phase 17: Session Manager and Fabric Subscriber

Phase 17 converts session lifecycle and fabric subscription
management.

### Phase 18: Cleanup and Contract Enforcement

Phase 18 removes legacy paths and adds enforcement.

#### Item 18.1: Remove Bypass Paths

**Description:** Delete or hard-disable Go-error return paths for
infrastructure outcomes once the service path is canonical.

**Acceptance criteria:**

- Static checks fail if infrastructure subsystems return Go errors
  that bypass the claims plane.
- No legacy bypass remains in production code.

#### Item 18.2: Contract Tests

**Description:** Add comprehensive contract tests.

**Acceptance criteria:**

- New service type registration requires a handler, determinism level,
  and at least one validator.
- New action type for a service requires handler coverage and at
  least receipt validation.
- New validator requires (pass, fail, incomplete, errored) test coverage.

#### Item 18.3: Doc Reconciliation Completion

**Description:** Update all four other claims docs per §16.

**Acceptance criteria:**

- All four docs reference this document where infrastructure
  participation is described.
- Doc-lint tests pass across the full set.

## 18. Migration Notes

The migration is incremental and shippable per phase. The ordering is
chosen so that each phase's outputs are observable and replayable
without requiring downstream phases.

### 18.1 Coexistence with Legacy Paths

During migration, infrastructure subsystems support both paths:

1. The new service handler responds to `claim.posted` deltas.
2. The legacy direct-call API remains operational.

Both paths produce the same observable state at the end. When direct
calls are made, the subsystem internally synthesizes the equivalent
claim and testament so the board records the outcome. When deltas
arrive, the subsystem's handler dispatches normally.

This dual-path coexistence is removed phase-by-phase as each subsystem's
callers migrate to the service path.

### 18.2 Replay Determinism Across Versions

A WAL recorded before this migration contains direct-call outcomes as
testaments-by-synthesis (where the dual-path coexistence stamped them)
or as gaps (where no synthesis happened). The replay reducer treats
gaps as deterministic absence: the absence of a service testament
for a known direct call is recorded as `legacy_direct_call_uninstrumented`.

After full migration, all WAL records contain real service testaments.
Replay is fully reproducible.

### 18.3 Telemetry Continuity

Existing infrastructure telemetry counters continue to operate during
migration. New counters from §12.4 are added in parallel. Once
migration completes, the legacy counters are derivable from the
service testaments and may be deprecated.

### 18.4 Identity Migration

UIDs for service participants must be deterministic from the start. Any
subsystem migrated in an early phase that later changes its scope keys
breaks UID continuity. The mitigation:

- Scope keys are declared at participant type registration; changing
  them requires a new participant type or an explicit migration claim.
- The identity registry records UID lineage so historical UIDs remain
  resolvable to the current logical service.

### 18.5 Validator Backward Compatibility

Existing agentic validation paths continue to function during
migration. As programmatic validators are added per phase, the board's
`EvaluateValidation` automatically begins routing through them. No
caller-side change is required.

A claim's `validations` array may contain pre-migration validation
records that lack the determinism field; the registry treats them as
agentic. Post-migration, all validations carry the field.

### 18.6 Cancellation and Shutdown

The shutdown protocol from §12.5 applies throughout migration.
Synchronous handlers complete within the per-handler deadline.
Asynchronous continuations resolve to `shutdown` failure artifacts.
The legacy direct-call paths continue to function during shutdown
until their callers complete.

### 18.7 No Hybrid Authority

At no point during migration should a workflow truth live in two
places. If a subsystem has a service handler, that handler's testament
is the truth. The dual-path coexistence is for callers, not for
truth. A direct call still produces a testament; that testament is the
same canonical record the service handler would produce for the same
inputs.

## 19. Final Architecture Statement

Claims constrain work. Testaments answer claims. Artifacts prove
testaments. Validations check artifacts. Deltas transport committed
facts.

Every Sylk participant — agent, service, system, external — issues,
receives, and evaluates claims, testaments, validations, and artifacts
using the same wire format, the same delta envelope, the same board,
the same Guide event bus, the same WAL, and the same replay reducer.
The only differences between participant categories are in consumption
discipline (LLM tool loop vs. registered Go handler), evaluation
discipline (LLM judgment vs. deterministic validator), and identity
derivation (model-and-replica-keyed vs. scope-keyed).

Infrastructure outcomes are first-class durable facts on the board, not
Go errors that travel up call stacks. Validation can be programmatic or
agentic without changing the wire shape or the lifecycle. Replay
reconstructs every perspective deterministically. The Memory Forest
harvests infrastructure precedents alongside agent precedents. The UI
renders service-produced testaments through the same presentation
contract as agent-produced testaments.

The board is the single source of workflow truth. The Guide event bus
is the single transport. The claims plane is the universal coordination
primitive for the entirety of Sylk.
