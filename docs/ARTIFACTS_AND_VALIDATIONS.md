# Artifacts and Validations

This document defines the structure and lifecycle of artifacts and
validations as first-class entities in the Sylk claims plane. It
specifies how typed artifact data is carried, how validations bind to
artifacts, how validators are registered as typed handler functions,
how the two-phase validation lifecycle (deterministic then optional
quality bar) executes, and how validation outcomes feed back into the
claim and testament lifecycles documented in `CLAIMS.md`,
`CLAIMS_AND_DELTAS.md`, `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`,
`CLAIMS_VISIBILITY.md`, and `CLAIMS_AND_INFRASTRUCTURE.md`.

It also specifies the streaming-only artifact submission model: every
testament is the closing signal of a work cycle. Artifacts stream onto
the board as the target produces them; the testament is generated only
when all required work is complete or when the testifier has hit an
error and is signaling that the work cannot continue.

This document does not introduce a new board, a new transport, a new
event bus, or a new validation authority. It widens the artifact and
validation primitives within the existing claims plane and pins down
exactly how their lifecycles propagate through the existing claim and
testament state machines.

## 1. Purpose

The existing claims documents establish artifacts and validations as
core entities but treat them as immutable evidence and quality gates
respectively, with limited lifecycle modeling and no formal typed
handler discipline. The motivating observations:

1. Artifacts have no explicit lifecycle. They appear in testaments,
   are referenced as evidence, and are otherwise inert. There is no
   per-artifact replay, no per-artifact receipt acknowledgment, no
   per-artifact validation status, and no per-artifact UI rendering
   contract.
2. Validators are described in prose as "agentic" or "mechanical" with
   no formal type system. The existing model leaves the binding
   between a validation and the specific artifact it should evaluate
   underspecified, and it leaves the validator implementation contract
   underspecified.
3. The two-phase split between deterministic validation (a typed
   function inspecting structured artifact data) and quality validation
   (an agent's judgment against a non-deterministic quality bar text)
   is not formalized. The existing model has receipt validations as
   mechanical and all other types as agentic, which collapses an
   important distinction.
4. Validation results are evidence too. The current model has no
   contract for capturing, attaching, or rendering the structured
   evidence that a deterministic validator produces while evaluating an
   artifact.
5. Streaming artifact submission is not modeled. The current model
   assumes artifacts arrive atomically with their testament. Real
   workloads stream artifacts as work progresses, with the testament
   arriving as the closing signal.

This document closes those gaps. The result is a complete contract for
how artifacts come into existence, how they are received and
acknowledged, how validations bind to them, how validators execute, how
results are captured, how the two-phase validation lifecycle drives
artifact, testament, and claim state transitions, and how remediation
is initiated when validations fail or evidence is missing.

## 2. Non-Negotiable Semantics

These semantics are load-bearing for the rest of the document. They
override any conflicting phrasing in the existing claims documents and
are the authority during reconciliation.

### 2.1 Artifacts Have a Lifecycle

Artifacts are no longer inert evidence records. They have an explicit
lifecycle with durable state transitions and corresponding deltas. The
lifecycle is committed by different parties at different transitions;
no single actor owns the full lifecycle.

### 2.2 Artifact Data Is Typed

Every artifact carries a typed data payload. The data payload's shape
is declared by the artifact's `DataType` field and round-trips through
the board via a canonical serialization. Validators are registered
against `DataType` values and receive the typed payload directly,
deserialized by the validator dispatcher before invocation.

### 2.3 Validations Bind to Artifacts One-to-One

Each validation evaluates exactly one artifact. A validation's
`TargetArtifactName` field declares which artifact (by name within the
testament) it evaluates. The runtime pairs validations to artifacts at
validation dispatch time via name match. Validators do not aggregate
across multiple artifacts, do not depend on other validations'
outputs, and run in parallel for any given artifact.

If cross-artifact verification is needed, the claim issuer declares
multiple validations, one per artifact, with whatever logical
composition the issuer intends. The runtime treats each validation
independently.

### 2.4 Validators Are Typed Handler Functions

A validator is a Go function with signature:

```go
func(ctx context.Context, data T) (Artifact[R], error)
```

where `T` is the artifact data type the validator expects and `R` is
the result type the validator produces. Validators are registered by
ID in a process-global registry. A single registered function may back
multiple validation records, each with its own independent lifecycle
and result artifact reference.

### 2.5 Two-Phase Validation

A validation has up to two phases:

1. **Deterministic phase**: the typed handler runs. Pure Go,
   reproducible, no LLM involvement. Result is captured as an artifact
   of type `R`.
2. **Quality bar phase**: the claimant agent evaluates the artifact
   against the validation's free-form `QualityBar` text. This phase is
   agentic-exclusive.

A validation enters the quality bar phase only if the deterministic
phase succeeded and the validation declares a non-empty quality bar.
Non-agentic claimants cannot have validations with non-empty quality
bars; the registry rejects such configurations.

### 2.6 The Board Stores and Emits Deltas Only

The board does not compute aggregates, derive states, or orchestrate
validations. It records each state transition committed by the
appropriate party and emits a corresponding delta. All orchestration
logic lives in the claimant runtime (for received, validating,
validated, and failure transitions) or the testifier runtime (for
generated, generation_failed, and attached transitions).

### 2.7 Testaments Are Closing Signals

A testament is generated either when the testifier has completed all
required work and produced all required artifacts, or when the
testifier has encountered an error that prevents continuing. A
testament is the testifier's way of signaling "I am done." Artifacts
stream onto the board as the testifier produces them; the testament is
always the final commit of a work cycle.

There is no atomic artifact-and-testament submission in the sense of
"both objects share a single commit timestamp with no prior artifact
visibility." Artifacts always commit `artifact.generated` first; the
testament always commits last, atomically with its referenced artifacts'
`artifact.attached` transitions.

**Streaming vs lifecycle compression**: this streaming rule constrains
the *ordering* of artifact-creation commits relative to
testament-generation commits. It does not constrain how many lifecycle
*states* commit together once the testament is being generated. Per
`docs/CLAIMS_AND_INFRASTRUCTURE.md` §10, a synchronous service handler
may compress multiple lifecycle states (`testament.generated`,
`testament.posted`, `artifact.attached` for every referenced artifact,
`claim.received`, and potentially `claim.satisfied`) into a single
board transaction. The compression groups state transitions for atomic
durability; it does not collapse the per-artifact `generated` commits
that streamed in earlier. Streaming and compression are orthogonal
concerns:

- **Streaming** = artifacts commit `generated` before the testament
  commits `generated`. Always true.
- **Compression** = the testament's commit batch may include several
  lifecycle state transitions atomically. Optional, used by
  synchronous service handlers.

A fast deterministic service that produces one artifact and one
testament still streams (the artifact's `generated` commit happens
strictly before the testament's `generated` commit, even if only by
microseconds), and additionally may compress (the testament's batch
includes `attached` for the artifact, `received` for the claim, and
`satisfied` for the claim all in one transaction).

### 2.8 Claimant and Testifier Are Distinct Roles

The claimant issues the claim. The target receives the claim and works
it. The testifier is the target when it is responding with a
testament. The claimant receives testaments and their artifacts. The
target receives claims. These roles are not interchangeable. Receipt
of a testament is a claimant action; receipt of a claim is a target
action. Where this document refers to receipt of an artifact, the
receiver is always the claimant of the parent claim.

### 2.9 Result Artifacts and Result Testaments Are Asymmetric

Validator result artifacts are bundled into a per-claim result
testament issued by the claimant. The result testament has its
`ClaimID` set to the original claim. The result testament terminates
at `testament.posted` (no `received`, no validation chain), and the
result artifacts inside it terminate at `artifact.generated` (no
received, no attached, no validation chain). Result artifacts are
evidence the claimant generated for the audit trail; they are not
work product to be consumed or validated.

### 2.10 No Versioning

Artifacts, claims, validations, testaments, and result artifacts are
created once and live once. They traverse their lifecycle exactly once
and reach a terminal state. Versioning of artifact data types is not
in scope. All entities are timestamped.

### 2.11 No Untracked Goroutines, No Unbounded Queues, No Silent Drops

The validator dispatcher, the per-artifact orchestrator, the result
testament builder, and the remediation poster all obey the
project-wide concurrency discipline from `CLAUDE.md`. Every goroutine
is owned by a tracked `core/concurrency` scope, every queue is
bounded with declared capacities, every cancellation produces a
cancellation artifact, and every overflow produces durable telemetry.

## 3. Vocabulary

The vocabulary here aligns with the existing claims documents and
extends them where new concepts appear.

### 3.1 Claimant

The participant that issues the claim. Owns the orchestration of the
validation lifecycle once a testament arrives. Receives testaments and
their artifacts. May be an agent, a service, or a system per
`CLAIMS_AND_INFRASTRUCTURE.md`.

### 3.2 Target

The participant that the claim is directed at. Receives the claim and
performs the work it requires. When responding, the target acts as the
testifier.

### 3.3 Testifier

The target acting in its testament-producing role. Generates
artifacts, commits them to the board as work progresses, and generates
the testament as the closing signal of the work cycle. Commits the
`artifact.attached` transition atomically with the
`testament.generated` commit.

### 3.4 Artifact

A typed evidence record produced by a testifier (or by a claimant
running a validator). Carries typed data, structural metadata, parent
references, presentation hints, and lifecycle status.

### 3.5 ArtifactName

A free-form string that uniquely identifies an artifact within its
parent testament. Set by the testifier when generating the artifact.
Used by the runtime to pair the artifact with the validations that
target it (via `TargetArtifactName`).

### 3.6 DataType

A string discriminator for the artifact's typed data payload. Examples:
`vfs_handle`, `pipeline_pod_state`, `test_output`, `plan_markdown`,
`agent_health_probe`. Used at validator dispatch time to deserialize
the artifact's raw data into the validator's expected Go type and to
ensure the validator's declared input type matches.

### 3.7 Validation

A record declared by the claim issuer. Pairs to one artifact via
`TargetArtifactName`. References a registered validator handler by ID.
Carries its own lifecycle status, result artifact reference, quality
bar text, required flag, weight, and timeout.

### 3.8 Validator

A registered Go handler function with typed input and output, invoked
by the validator dispatcher to evaluate one artifact. Stateless, pure
with respect to artifact inputs (with `side_effect` determinism
permitted per `CLAIMS_AND_INFRASTRUCTURE.md §11.4`), returns a typed
result artifact wrapping the structured outcome.

### 3.9 Quality Bar

A free-form text body attached to a validation. Provides
non-deterministic criteria that an agent claimant uses to assess the
artifact in addition to the deterministic validator's verdict. Empty
for non-agentic claimants by registration-time enforcement.

### 3.10 Result Artifact

The typed return value of a validator's handler function, wrapped as
an `Artifact[R]`. Bundled into the per-claim result testament. Has a
degenerate terminal lifecycle (terminates at `artifact.generated`).

### 3.11 Result Testament

A testament issued by the claimant containing all result artifacts
from all validations on a single claim. Its `ClaimID` equals the
original claim's ID. Terminates at `testament.posted` (no receipt,
no validation chain).

### 3.12 Generated-but-Unattached Window

The time between a testifier committing `artifact.generated` and
committing `artifact.attached`. During this window the artifact is
visible to the claimant but not yet bound to a testament. The claimant
may commit `artifact.received` during this window but may not begin
validation.

## 4. Artifact Structure

This section defines the artifact record's structure. The structure
extends the existing `claims.Artifact` from `CLAIMS.md §4.7` with
typed data, structural parent references, lifecycle status, validation
binding metadata, error capture, and presentation metadata from
`CLAIMS_VISIBILITY.md`.

### 4.1 Universal Base Fields

Per `CLAIMS.md §4.2`, every artifact carries the nine universal base
fields, with one rename from this document forward: `AgentID` becomes
`ParticipantID` per `CLAIMS_AND_INFRASTRUCTURE.md §5.2`. During
migration the existing `AgentID` field is a backward-compatibility
alias.

```go
type Artifact struct {
    // ── Universal base (9 fields, per CLAIMS.md §4.2) ──
    ID            string     `json:"id"`
    ParticipantID string     `json:"participant_id"`
    SessionID     string     `json:"session_id"`
    PipelineID    string     `json:"pipeline_id"`
    TaskID        string     `json:"task_id"`
    Sequence      uint64     `json:"sequence"`
    Relations     []Relation `json:"relations"`
    Created       time.Time  `json:"created"`
    Accessed      time.Time  `json:"accessed"`

    // ── Structural parent references ──
    TestamentID string `json:"testament_id"`
    ClaimID     string `json:"claim_id"`

    // ── Name and type discrimination ──
    ArtifactName string `json:"artifact_name"`
    Kind         string `json:"kind"`
    DataType     string `json:"data_type"`

    // ── Typed data payload ──
    Data []byte `json:"data"`

    // ── Compatibility carriers ──
    Reference string         `json:"reference,omitempty"`
    Metadata  map[string]any `json:"metadata,omitempty"`

    // ── Integrity ──
    ContentHash string `json:"content_hash"`
    Size        int64  `json:"size"`
    Ephemeral   bool   `json:"ephemeral,omitempty"`

    // ── Lifecycle ──
    Status        ArtifactStatus  `json:"status"`
    StatusHistory []StatusChange  `json:"status_history"`

    // ── Error capture (populated on failure states) ──
    Errors []*ArtifactError `json:"errors,omitempty"`

    // ── Presentation hints (per CLAIMS_VISIBILITY.md §4.1) ──
    Presentation *Presentation `json:"presentation,omitempty"`
}
```

### 4.2 Structural Parent References

Both `TestamentID` and `ClaimID` are explicit fields on the artifact.
They are formal parent references, not free-form relations. Their
presence is required for any artifact that has reached
`artifact.attached`; before attachment, `TestamentID` may be empty
(the testament has not yet been generated). `ClaimID` is always
populated, even for unattached artifacts: a testifier always knows
which claim the artifact answers when it generates the artifact.

### 4.3 ArtifactName

`ArtifactName` is the testifier-stamped free-form string that
identifies the artifact within its parent testament. The pairing
between a validation and the artifact it evaluates is established
exclusively through `Validation.TargetArtifactName == Artifact.ArtifactName`.

The name is opaque to the runtime. The testifier chooses the name;
the issuer of the claim chooses the matching `TargetArtifactName`
when declaring validations.

The name must be unique within a single testament. Two artifacts in
the same testament cannot share a name. If duplicate names are
detected at testament-generation time, the testifier commits
`testament.generation_failed` per `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md
§4.testament_generation_failed` with a structured error artifact
explaining the duplicate.

### 4.4 DataType and Data

`DataType` is a string discriminator declared by the testifier when
generating the artifact. The runtime uses it at validator dispatch
time to deserialize the artifact's raw `Data` bytes into the
validator's expected Go type, and to verify the validator's declared
input type matches.

`Data` is the canonical serialized form of the typed payload. The
serialization is deterministic for replay safety: stable JSON ordering,
Unicode NFC normalization, smallest-int encoding where applicable, and
no whitespace.

Typed access is exposed via generic helpers:

```go
// ArtifactData deserializes the artifact's raw data into T.
// Returns a typed error if the artifact's DataType does not match
// the expected type tag.
func ArtifactData[T any](a *Artifact) (T, error)

// MustArtifactData is the panicking variant. Use only in test helpers
// or where the type contract is statically verified at the call site.
func MustArtifactData[T any](a *Artifact) T

// SetArtifactData serializes value into the artifact's Data field
// and stamps DataType using the registered type tag for T. Computes
// ContentHash and Size.
func SetArtifactData[T any](a *Artifact, value T) error
```

The mapping from Go type to `DataType` string is established via the
type registry described in §8.4.

### 4.5 Reference and Metadata Carriers

`Reference` and `Metadata` are preserved from the existing artifact
shape (`CLAIMS.md §4.7`). They remain in this document's artifact
structure for backward compatibility with existing artifact kinds that
do not yet have typed data payloads. New artifact kinds should declare
a typed payload via `DataType` and `Data` rather than relying on
`Reference` for content delivery. `Metadata` continues to carry
kind-specific structured data that is not part of the typed payload
(timestamps, source IDs, immutable provenance markers).

### 4.6 Integrity Fields

`ContentHash` is the SHA-256 of the canonical serialized `Data`
content. It is computed once at generation and is immutable.

`Size` is the byte length of `Data`. Used for resource accounting and
presentation truncation policy.

`Ephemeral` marks artifacts that may be evicted after the iteration
completes. Result artifacts are ephemeral by default. Work-product
artifacts are durable.

### 4.7 Lifecycle Status

`Status` is the artifact's current lifecycle state. `StatusHistory`
records every transition with from, to, reason, participant ID, and
timestamp. The full set of valid states and transitions is defined in
§5.

### 4.8 Errors

`Errors` is populated when the artifact is in a failure state
(`generation_failed`, `receipt_failed`, `validation_failed`). Each
entry carries a category, structured payload, originating party, and
timestamp. The shape:

```go
type ArtifactError struct {
    Category    ArtifactErrorCategory `json:"category"`
    Description string                `json:"description"`
    Payload     map[string]any        `json:"payload,omitempty"`
    Source      ParticipantRef        `json:"source"`
    OccurredAt  time.Time             `json:"occurred_at"`
}

type ArtifactErrorCategory string

const (
    ArtifactErrorCategoryGeneration        ArtifactErrorCategory = "generation"
    ArtifactErrorCategoryReceiptStructural ArtifactErrorCategory = "receipt_structural"
    ArtifactErrorCategoryReceiptMetadata   ArtifactErrorCategory = "receipt_metadata"
    ArtifactErrorCategoryValidation        ArtifactErrorCategory = "validation"
    ArtifactErrorCategoryValidatorErrored  ArtifactErrorCategory = "validator_errored"
    ArtifactErrorCategoryQualityBar        ArtifactErrorCategory = "quality_bar"
    ArtifactErrorCategoryTimeout           ArtifactErrorCategory = "timeout"
)
```

Errors are first-class evidence. They are not Go error returns. They
are durable on the artifact and observable by validators, claimants,
inspectors, the UI, and the Memory Forest.

### 4.9 Presentation

`Presentation` is the same optional metadata defined in
`CLAIMS_VISIBILITY.md §4.1`. Artifacts that should render to a
user-facing surface (chat, approval, side panel, diagnostics) declare
their presentation here. Internal artifacts omit it. Presentation does
not affect lifecycle.

## 5. Artifact Lifecycle

The artifact lifecycle is eight states with explicit commit ownership
per transition. The lifecycle is streaming-only: there is no atomic
artifact-and-testament submission.

### 5.1 State Set

```text
artifact.generated
artifact.generation_failed       (terminal)
artifact.received
artifact.receipt_failed          (terminal)
artifact.attached
artifact.validating
artifact.validation_failed       (terminal)
artifact.validated               (terminal)
```

### 5.2 Canonical Happy Path

```text
artifact.generated
  → artifact.received            (optional, during unattached window)
  → artifact.attached            (atomic with testament.generated)
  → artifact.validating          (claimant begins running validations)
  → artifact.validated           (all required validations passed)
```

The `received` transition is opportunistic: it commits when the
claimant observes the artifact during its unattached window. If the
testament is generated before the claimant has had a chance to observe
the artifact, the `received` transition is skipped and the artifact
proceeds directly from `generated` to `attached`. The WAL records
both paths as valid.

### 5.3 State Definitions

#### artifact.generated

The testifier has created the artifact and committed it to the board.
The artifact is durable, visible to the claimant, and has its
`DataType`, `Data`, `ContentHash`, `Size`, `ArtifactName`, `ClaimID`,
and `ParticipantID` populated. `TestamentID` is empty (the testament
has not yet been generated).

Committed by: testifier.

Required durable data: all universal base fields, `ArtifactName`,
`ClaimID`, `DataType`, `Data`, `ContentHash`, `Size`, status
`generated`, status history entry.

#### artifact.generation_failed

The testifier could not create the artifact. If enough data exists to
create a durable failed record, the artifact is committed with status
`generation_failed` and an `ArtifactError` of category `generation`.
If no durable record can be created, the failure is captured on the
parent claim per `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §4.testament_generation_failed`.

Committed by: testifier.

Terminal state. The artifact does not progress further.

#### artifact.received

The claimant has observed the artifact on the board during its
unattached window and has acknowledged its existence. The claimant
has not consumed the artifact, has not begun validation, and has not
performed any structural or semantic check beyond confirming the
artifact's presence. The transition is an acknowledgment commit only.

Committed by: claimant.

Optional. The transition is skipped when the testament generates
before the claimant observes the artifact independently.

#### artifact.receipt_failed

The claimant has observed the artifact and rejected it for structural
reasons. This is not a validation failure. Receipt failure indicates
the artifact is missing required metadata, has malformed structural
fields (invalid `DataType`, missing `ArtifactName`, unparseable
`Data`, etc.), or is otherwise unfit for entry into the validation
phase. The claimant commits the artifact's status to `receipt_failed`
with an `ArtifactError` of category `receipt_structural` or
`receipt_metadata`.

Committed by: claimant.

Terminal state. The artifact does not progress. The claimant typically
follows up with a corrective claim per §10.

#### artifact.attached

The testifier has bound the artifact to a testament. This transition
is committed atomically with `testament.generated`: the testament
record's commit batch includes one `artifact.attached` transition per
artifact referenced in the testament. The artifact's `TestamentID`
field is populated with the testament's ID at this commit.

Committed by: testifier (atomically with `testament.generated`).

By the semantics of attached, the testifier should have observed the
claimant's `received` commit for each attached artifact before
generating the testament. In the streaming model where artifacts trail
their corresponding work and the testament is the closing signal, the
claimant typically has ample time to commit `received` during the
unattached window. If the claimant did not commit `received` for an
artifact before the testifier generated the testament, the artifact's
status history shows `generated → attached` directly (no intermediate
`received`). This is a valid path.

#### artifact.validating

The claimant has begun running the validations targeting this
artifact. The transition is committed when the claimant orchestrator
dispatches the first validation for the artifact. All validations on a
single artifact run in parallel; the artifact remains in `validating`
until all are terminal or until a required validation reaches a
blocking failure state.

Committed by: claimant.

#### artifact.validation_failed

A required validation on the artifact has reached a blocking failure
state. The blocking failure states are:

- `validation.validation_failed`
- `validation.errored`
- `validation.quality_bar_validation_failed`

When any required validation reaches a blocking failure state, the
claimant orchestrator:

1. Stops dispatching remaining validations on this artifact (siblings
   remain at `validation.ready`).
2. Commits `artifact.validation_failed` with an `ArtifactError`
   referencing the failed validation's ID and category.
3. Propagates the failure to the parent testament per §11.

Committed by: claimant.

Terminal state.

#### artifact.validated

All required validations on the artifact have reached `validation.validated`.
Non-required validations may have reached `_not_required` failure
variants; these are recorded but do not block. The claimant commits
`artifact.validated` once the last required validation terminates
successfully.

Committed by: claimant.

Terminal state.

### 5.4 Transition Rules

```text
generated         → received | receipt_failed | attached
generation_failed → (terminal)
received          → attached  (testifier observes and proceeds)
                  → receipt_failed  (claimant detects structural issue after received)
receipt_failed    → (terminal)
attached          → validating
validating        → validated | validation_failed
validation_failed → (terminal)
validated         → (terminal)
```

Notes:

- `generated → attached` is permitted when the claimant did not commit
  `received` before the testifier generated the testament.
- `received → receipt_failed` is permitted: a claimant may initially
  acknowledge an artifact and then, on closer inspection during the
  receipt-time structural check, detect a problem and reject it.
- `receipt_failed` does not transition further. The claimant's
  recovery path is to post a corrective claim per §10.

### 5.5 Streaming-Only Submission

The streaming model is the only model. There is no atomic
artifact-and-testament submission. The lifecycle in the streaming
context looks like:

```text
time →
testifier:
  [A1.generated] ───── [A2.generated] ───── [A3.generated] ───── [work done] ─→
                                                                              ↓
  [testament.generated + A1.attached + A2.attached + A3.attached]

claimant:
                [A1.received]         [A2.received]         [A3.received]
                                                                              ↓
                                                                  [testament.received]
                                                                              ↓
                                                                  [validating per artifact]
```

The testifier always generates the testament as the closing signal of
the work cycle. There is never a case where artifact and testament
arrive in the same commit with no preceding `generated` commits for
the artifacts; even instantaneous workloads commit the artifacts first
and the testament second.

This model has three important properties:

1. **Faster claimant exposure.** The claimant sees evidence as it
   accrues, not only when the work cycle closes.
2. **Stronger crash resilience.** A testifier that crashes mid-work
   leaves a partial trail of generated artifacts that downstream
   observers can use to reason about progress and to schedule
   remediation.
3. **Cleaner failure semantics.** The testifier always generates a
   closing testament, even on failure. The testament's artifacts in
   the failure case include `ArtifactError` records and `error`-kind
   artifacts that explain what went wrong. There is no "the work
   silently stopped" path.

### 5.6 Receipt Granularity

Receipt operates at two levels: per-artifact (during the unattached
window) and per-testament (after the testament is generated). Both
exist; they describe different acknowledgments and do not conflict.

**Per-artifact receipt** (`artifact.received`): the claimant observes
each generated artifact independently during its unattached window
and commits `artifact.received` for that specific artifact. This is
opportunistic: the claimant commits it if and when it observes the
artifact on the board before the testament arrives. If the testament
arrives before the claimant gets a chance to observe an artifact
individually, the per-artifact receipt is skipped (the artifact's
status history shows `generated → attached` directly).

**Per-testament receipt** (`testament.received` per
`docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md` §5): the claimant
acknowledges the testament as a whole after it is generated and
posted. This is a separate commit that occurs after the testament is
generated, regardless of whether the claimant committed per-artifact
receipts for the testament's artifacts during their unattached
windows.

**Interaction**: the two receipts are independent commits that may
or may not both occur for a given artifact. The canonical sequences:

- **Streaming with per-artifact observation**: per-artifact `received`
  → testament `generated` + artifact `attached` → testament
  `received`. Both receipt levels fire.
- **Streaming without per-artifact observation** (testament arrives
  before claimant observes individual artifacts): testament
  `generated` + artifact `attached` → testament `received`. Only
  per-testament receipt fires; per-artifact receipt is skipped.
- **Synchronous service compression**: testament + artifact attached
  commit in one transaction; per-artifact receipt is skipped;
  per-testament receipt commits immediately after.

All receipt commits are idempotent: a duplicate commit by the same
claimant for the same artifact or testament is a no-op. The claimant
may commit per-artifact receipt at any point during the unattached
window (multiple times safely; only the first has effect). The
claimant commits per-testament receipt exactly once per testament.

Per-artifact receipt is purely a "the claimant has seen this artifact
on the board" marker. It does not consume the artifact, does not
begin validation, and does not perform structural checking beyond
presence acknowledgment. Validation begins only after `attached`.

### 5.7 Receipt Failure Is Structural

`artifact.receipt_failed` is exclusively structural. The conditions
that trigger it:

1. The artifact's `DataType` is empty, unrecognized, or invalid.
2. The artifact's `Data` cannot be deserialized under its declared
   `DataType`.
3. The artifact's `ArtifactName` is empty or collides with another
   artifact in the same parent testament.
4. The artifact's `ContentHash` does not match the SHA-256 of `Data`.
5. The artifact's `ClaimID` is empty (cannot pair to a claim).
6. The artifact's required metadata for its `Kind` is missing or
   malformed.
7. The artifact's `Presentation` metadata (if present) is invalid
   under the validation rules in `CLAIMS_VISIBILITY.md §0.3`.

Receipt failure is not a validation failure. The artifact never enters
the validation phase. The claimant's recovery path is to post a
corrective claim back to the testifier per §10, requesting a corrected
artifact.

### 5.8 Artifact Status History

Every transition appends a `StatusChange` record per `CLAIMS.md §4.1`:

```go
type StatusChange struct {
    From          string         `json:"from"`
    To            string         `json:"to"`
    Reason        string         `json:"reason"`
    ParticipantID string         `json:"participant_id"`
    Changed       time.Time      `json:"changed"`
}
```

The committing party stamps `ParticipantID` with its own canonical
UID. The full status history is replayable from the WAL and provides
the durable audit trail for the artifact's lifecycle.

## 6. Validation Structure

This section defines the validation record's structure. Validations
extend the existing `claims.Validation` from `CLAIMS.md §4.8` with
typed handler binding, target artifact name, quality bar text,
extended lifecycle status, timeout, and result artifact reference.

### 6.1 Universal Base Fields

```go
type Validation struct {
    // ── Universal base (9 fields, per CLAIMS.md §4.2) ──
    ID            string     `json:"id"`
    ParticipantID string     `json:"participant_id"`
    SessionID     string     `json:"session_id"`
    PipelineID    string     `json:"pipeline_id"`
    TaskID        string     `json:"task_id"`
    Sequence      uint64     `json:"sequence"`
    Relations     []Relation `json:"relations"`
    Created       time.Time  `json:"created"`
    Accessed      time.Time  `json:"accessed"`

    // ── Structural parent reference ──
    ClaimID string `json:"claim_id"`

    // ── Validation-target binding ──
    TargetArtifactName string `json:"target_artifact_name"`

    // ── Handler binding ──
    ValidatorID         string `json:"validator_id"`
    ArtifactDataType    string `json:"artifact_data_type"`
    ResultDataType      string `json:"result_data_type"`

    // ── Specification (per CLAIMS.md §4.8) ──
    Type        ValidationType `json:"type"`
    Description string         `json:"description"`
    QualityBar  string         `json:"quality_bar,omitempty"`
    Required    bool           `json:"required"`
    Weight      int            `json:"weight,omitempty"`
    Timeout     time.Duration  `json:"timeout"`
    Deadline    time.Time      `json:"deadline,omitempty"`

    // ── Lifecycle ──
    Status        ValidationStatus `json:"status"`
    StatusHistory []StatusChange   `json:"status_history"`

    // ── Result capture ──
    ResultArtifactID string             `json:"result_artifact_id,omitempty"`
    Error            *ValidationError   `json:"error,omitempty"`
    EvaluatedAt      time.Time          `json:"evaluated_at,omitempty"`
    EvaluatorRef     *ParticipantRef    `json:"evaluator_ref,omitempty"`
}
```

### 6.2 Parent Reference

`ClaimID` is the formal parent reference. A validation belongs to
exactly one claim. The pairing of validation to artifact is
established via `TargetArtifactName`, which is matched against the
`ArtifactName` field of artifacts in the testament that responds to
the parent claim.

### 6.3 TargetArtifactName

The 1-1 binding mechanism. The claim issuer declares the name of the
artifact this validation will evaluate. At validation dispatch time,
the runtime locates the artifact in the parent claim's responding
testament whose `ArtifactName` matches `TargetArtifactName` and
invokes the registered validator handler against it.

If no matching artifact is present in the testament, the claimant
commits a `claim.validation_incomplete` per
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §4.validation_incomplete` and
follows the remediation flow in §10. The validation itself remains at
`validation.ready` (it was never dispatched).

### 6.4 Handler Binding

`ValidatorID` is the registry key for the typed handler function. The
validator dispatcher resolves this ID at dispatch time to obtain the
type-erased adapter for invocation.

`ArtifactDataType` is the expected `DataType` of the target artifact.
The dispatcher verifies the target artifact's `DataType` matches
before invoking the handler. A mismatch produces `validation.errored`
with category `artifact_type_mismatch`.

`ResultDataType` is the expected `DataType` of the result artifact
that the handler produces. The dispatcher stamps the returned result
artifact with this `DataType`.

### 6.5 Specification Fields

`Type` classifies the validation per the enum from `CLAIMS.md §4.9`:
`test`, `inspection`, `integration`, `contract`, `design`,
`regression`, `receipt`.

`Description` is the human-readable, evaluator-facing description of
what the validation checks. This is the same field defined in
`CLAIMS.md §4.8`.

`QualityBar` is the free-form text body that drives the agentic
quality-bar phase. Non-empty quality bars are agentic-exclusive: the
registry rejects a validation declaration with a non-empty `QualityBar`
when the claimant is non-agentic. Empty `QualityBar` skips the quality
phase entirely; the validation terminates at `validation.validated`
when its deterministic phase succeeds.

`Required` marks the validation as mandatory for the parent claim's
satisfaction. Required failures block (`validation.validation_failed`,
`validation.errored`, `validation.quality_bar_validation_failed`).
Non-required failures land in the corresponding `_not_required`
variants and do not block.

`Weight` is an advisory ordering hint used by the claimant orchestrator
to prioritize evaluation when resources are constrained. Higher weight
means earlier dispatch. Has no semantic effect on outcomes.

`Timeout` is the maximum wall-clock duration the validator handler may
execute. Default `0` means no timeout (an infinite deadline). Agents
may set non-zero values per-skill based on skill metadata. Exceeding
the timeout transitions the validation to `validation.validation_failed`
(not `errored`), because the timeout indicates the artifact failed to
be processable in the specified time rather than indicating validator
infrastructure failure.

`Deadline` is an absolute UTC deadline derived from `Created + Timeout`
when `Timeout > 0`. Used by the claimant orchestrator to detect
timeout conditions and trigger the transition.

### 6.6 Lifecycle Status

`Status` is the validation's current lifecycle state.
`StatusHistory` records every transition. The full set of valid
states and transitions is defined in §7.

### 6.7 Result Capture

`ResultArtifactID` is the ID of the result artifact produced by the
handler's successful invocation. The result artifact lives in the
per-claim result testament described in §9. If the validation reaches
a failure state without producing a result, `ResultArtifactID` is
empty.

`Error` captures structured failure detail for non-terminal-success
states. The shape:

```go
type ValidationError struct {
    Category    ValidationErrorCategory `json:"category"`
    Description string                  `json:"description"`
    Payload     map[string]any          `json:"payload,omitempty"`
    Source      ParticipantRef          `json:"source"`
    OccurredAt  time.Time               `json:"occurred_at"`
}

type ValidationErrorCategory string

const (
    ValidationErrorCategoryHandler       ValidationErrorCategory = "handler"
    ValidationErrorCategoryQualityBar    ValidationErrorCategory = "quality_bar"
    ValidationErrorCategoryTimeout       ValidationErrorCategory = "timeout"
    ValidationErrorCategoryArtifactType  ValidationErrorCategory = "artifact_type_mismatch"
    ValidationErrorCategoryDispatcher    ValidationErrorCategory = "dispatcher"
    ValidationErrorCategoryDependency    ValidationErrorCategory = "dependency_unavailable"
    ValidationErrorCategoryPanic         ValidationErrorCategory = "handler_panic"
)
```

`EvaluatedAt` is the UTC timestamp of the terminal transition.

`EvaluatorRef` is the canonical participant reference of the evaluator
agent for the quality-bar phase. Empty when the validation has no
quality bar or has not yet entered the quality phase.

## 7. Validation Lifecycle

The validation lifecycle is ten states, including the symmetric
`_not_required` variants for each of the three failure modes.

### 7.1 State Set

```text
validation.ready                                       (initial)
validation.validating                                  (deterministic phase running)
validation.validation_failed                           (terminal, blocking)
validation.validation_failed_not_required              (terminal, non-blocking)
validation.errored                                     (terminal, blocking)
validation.errored_not_required                        (terminal, non-blocking)
validation.validating_quality_bar                      (agentic quality phase running)
validation.quality_bar_validation_failed               (terminal, blocking)
validation.quality_bar_validation_failed_not_required  (terminal, non-blocking)
validation.validated                                   (terminal success)
```

### 7.2 Canonical Happy Path (Deterministic Only)

```text
validation.ready
  → validation.validating
  → validation.validated
```

Used when the validation has an empty `QualityBar` (the claimant is
non-agentic, or the agentic claimant declared the validation without
a quality bar).

### 7.3 Canonical Happy Path (With Quality Bar)

```text
validation.ready
  → validation.validating
  → validation.validating_quality_bar    (agent assesses artifact against quality bar)
  → validation.validated
```

The deterministic phase must succeed before the quality-bar phase
begins. The quality-bar phase runs only when the claimant is agentic.

### 7.4 State Definitions

#### validation.ready

The validation has been generated and is ready to execute. The
claimant orchestrator has not yet dispatched it. The validation
remains in this state until the orchestrator picks it up, or
indefinitely if the orchestrator never picks it up (e.g., the artifact
the validation targets is missing, or a sibling required validation
failed).

Committed by: claimant (validation creation).

#### validation.validating

The claimant orchestrator has dispatched the validation to the
validator dispatcher. The dispatcher has resolved the handler,
verified the artifact data type matches, deserialized the artifact's
`Data` into the handler's input type, and is invoking the handler.

Committed by: claimant (dispatcher entry).

#### validation.validation_failed

The deterministic handler completed and reported failure (handler
returned a non-nil error, or handler returned a result with
`Confidence` below an acceptance threshold, or the timeout was
exceeded). The validation is required; the failure is blocking.

Committed by: claimant.

Terminal state. The `Error` field is populated with category `handler`
or `timeout`.

#### validation.validation_failed_not_required

Same as `validation.validation_failed` but for non-required
validations. The failure is recorded but does not block the parent
artifact's progression.

Committed by: claimant.

Terminal state. The `Error` field is populated.

#### validation.errored

The validator infrastructure failed. The handler could not execute,
or panicked, or its declared dependency was unavailable, or the
artifact data could not be deserialized under the validator's expected
input type, or the dispatcher rejected the invocation for any
non-handler-execution reason. The validation is required; the failure
is blocking and propagates per §11.

Committed by: claimant.

Terminal state. The `Error` field is populated with category
`dispatcher`, `dependency`, `panic`, or `artifact_type_mismatch`.

Propagation: `validation.errored` → `testament.validation_errored` →
`claim.validation_errored` per `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md
§4.validation_errored`.

#### validation.errored_not_required

Same as `validation.errored` but for non-required validations.
Recorded, does not block.

Committed by: claimant.

Terminal state. The `Error` field is populated.

#### validation.validating_quality_bar

The deterministic phase succeeded and the validation has a non-empty
`QualityBar`. The agentic claimant is assessing the artifact against
the quality bar text. The dispatch mechanism is described in §7.6.

Committed by: claimant (entry into quality phase).

The transition is valid only when the claimant participant has
`Category: agent`. Non-agentic claimants cannot reach this state by
construction (the registry rejects non-empty quality bars on
non-agentic claims at declaration time).

#### validation.quality_bar_validation_failed

The agent's quality-bar assessment produced a failure verdict. The
validation is required; the failure is blocking.

Committed by: claimant agent (via `evaluate_validation` skill call or
equivalent).

Terminal state. The `Error` field is populated with category
`quality_bar`.

#### validation.quality_bar_validation_failed_not_required

Same as `validation.quality_bar_validation_failed` but for non-required
validations. Recorded, does not block.

Committed by: claimant agent.

Terminal state. The `Error` field is populated.

#### validation.validated

All validation phases completed and the artifact passed:

- The deterministic handler completed successfully.
- If `QualityBar` is non-empty, the agent's quality-bar assessment
  produced a passing verdict.
- The result artifact is captured (handler result wrapped as
  `Artifact[R]`) and its ID is in `ResultArtifactID`.

Committed by: claimant.

Terminal success state.

### 7.5 Transition Rules

```text
ready                                          → validating
                                                | (skipped by short-circuit)

validating (deterministic phase)               → validated  (when QualityBar is empty)
                                               → validating_quality_bar
                                                            (when QualityBar non-empty)
                                               → validation_failed
                                               → validation_failed_not_required
                                               → errored
                                               → errored_not_required

validating_quality_bar                         → validated
                                               → quality_bar_validation_failed
                                               → quality_bar_validation_failed_not_required
```

Notes:

- The `ready → validating` transition can be skipped: when a sibling
  required validation reaches a blocking failure state, the claimant
  orchestrator stops dispatching remaining validations on the same
  artifact. Those siblings remain at `ready` as their terminal state
  (there is no `skipped` state). The status history records that the
  validation was never dispatched.
- `validating → validation_failed` includes the timeout case: when the
  handler exceeds `Validation.Timeout`, the claimant transitions the
  validation with `Error.Category = timeout`.
- `validating_quality_bar → quality_bar_validation_failed` happens
  when the agent posts a failing verdict via the
  `evaluate_validation` skill or equivalent.

### 7.6 Quality-Bar Phase Mechanics

The quality-bar phase is part of the claimant agent's existing tool
loop. It is not dispatched as a sub-claim. When the deterministic
phase succeeds and the validation has a non-empty `QualityBar`:

1. The claimant orchestrator commits `validation.validating_quality_bar`.
2. The orchestrator presents the artifact (or a reference to it) and
   the quality bar text to the claimant agent as input.
3. The agent's LLM evaluates the quality bar against the artifact
   data, the deterministic result artifact, and any other context the
   agent finds relevant via `query_claims_board` or `traverse`.
4. The agent emits a verdict via the `evaluate_validation` skill,
   which commits either `validation.validated` (if pass) or
   `validation.quality_bar_validation_failed` /
   `_not_required` (if fail).

The artifact and quality bar text are injected into the agent's prompt
context. If the artifact is too large to fit in the prompt (large
diffs, plan markdown, embedded research), the quality bar text must
specify how the agent should retrieve the artifact (e.g., "Inspect the
`code_reference` artifact via `traverse`"). The orchestrator does not
synthesize alternative dispatch paths; it relies on the quality bar's
own self-description.

### 7.7 Parallelism and Short-Circuit

All validations targeting a single artifact run in parallel. They are
stateless single-artifact assertions; there are no dependencies between
them. The claimant orchestrator dispatches them to its bounded
validator dispatch pool and awaits their completions concurrently.

When a required validation reaches a blocking failure state
(`validation_failed`, `errored`, or `quality_bar_validation_failed`),
the orchestrator stops dispatching remaining validations on the same
artifact. Already-dispatched validations are allowed to complete (the
orchestrator does not cancel in-flight handlers). Validations that
have not yet been dispatched remain at `ready`.

The orchestrator then commits `artifact.validation_failed` per §5.3.

Non-required failures do not trigger short-circuit. Other validations
on the same artifact continue to run to completion.

### 7.8 Validation Status History

Every transition appends a `StatusChange` record per `CLAIMS.md §4.1`.
The history is replayable from the WAL.

## 8. Handler Registry

The handler registry holds typed validator functions and presents a
type-erased dispatch surface to the orchestrator. The registry is
process-global and registered at boot time; handlers cannot be
hot-swapped at runtime.

### 8.1 Typed Handler Interface

```go
// Validator is the typed validator function shape. Implementations
// are pure functions of (artifact data) → (result artifact) with
// optional error. Validators must not maintain state across
// invocations and must be deterministic per CLAIMS_AND_INFRASTRUCTURE.md §11.4
// for replay safety.
type Validator[T, R any] func(ctx context.Context, data T) (Artifact[R], error)
```

The `Artifact[R]` return is the standard `Artifact` struct with a
typed accessor convention: the validator constructs the result by
calling `SetArtifactData[R](&result, value)` and returns the artifact.
The dispatcher stamps the result's `DataType` based on the
validator's registered `ResultDataType` and populates the result's
parent references (`ClaimID` to the original claim, `TestamentID` set
when bundled into the result testament).

### 8.2 Type-Erased Adapter

The dispatcher invokes validators through a type-erased adapter
generated at registration time:

```go
type ValidatorAdapter struct {
    ID                string
    ArtifactDataType  string
    ResultDataType    string
    Determinism       HandlerDeterminism  // per CLAIMS_AND_INFRASTRUCTURE.md §11.4
    Invoke            func(ctx context.Context, raw []byte) (*Artifact, error)
}
```

The `Invoke` function deserializes `raw` into the typed input,
invokes the typed validator, captures the returned artifact, and
returns it. The adapter erases `T` and `R` at the dispatch boundary
so the registry can hold heterogeneous validators in a uniform map.

### 8.3 Registration

Registration is type-safe at the call site:

```go
type ValidatorConfig struct {
    ID               string
    ArtifactDataType string
    ResultDataType   string
    Determinism      HandlerDeterminism
}

// RegisterValidator registers a typed validator with the global
// registry. Generic type parameters T and R declare the input and
// output types. The registry generates the erased adapter and
// indexes by ID.
func RegisterValidator[T, R any](
    registry *ValidatorRegistry,
    config ValidatorConfig,
    handler Validator[T, R],
) error
```

Example:

```go
type VFSHandle struct {
    MountPoint  string `json:"mount_point"`
    Attached    bool   `json:"attached"`
    CapacityMB  int64  `json:"capacity_mb"`
    BaseVersion string `json:"base_version"`
}

type VFSCapacityResult struct {
    Requested int64 `json:"requested"`
    Actual    int64 `json:"actual"`
    Satisfied bool  `json:"satisfied"`
}

func validateVFSCapacity(ctx context.Context, h VFSHandle) (Artifact[VFSCapacityResult], error) {
    result := Artifact[VFSCapacityResult]{
        Kind:         "vfs_capacity_validation_result",
        ArtifactName: "vfs_capacity_check",
        Ephemeral:    true,
    }
    actual := h.CapacityMB
    requested := requestedCapacityFromContext(ctx)
    err := SetArtifactData(&result.Artifact, VFSCapacityResult{
        Requested: requested,
        Actual:    actual,
        Satisfied: actual >= requested,
    })
    if err != nil {
        return result, err
    }
    if actual < requested {
        return result, fmt.Errorf("vfs capacity %d below requested %d",
            actual, requested)
    }
    return result, nil
}

RegisterValidator(registry, ValidatorConfig{
    ID:               "vfs.capacity",
    ArtifactDataType: "vfs_handle",
    ResultDataType:   "vfs_capacity_validation_result",
    Determinism:      HandlerDeterminismPure,
}, validateVFSCapacity)
```

A single registered handler may be referenced by many validation
records. Each validation record carries the handler's ID; each record
has its own independent lifecycle and result-artifact reference.

### 8.4 Type Registry

A sibling type registry maps `DataType` strings to Go types for
serialization and deserialization:

```go
type TypeRegistry interface {
    // RegisterType associates a DataType string with a Go type's
    // serializer and deserializer. Called at boot for every type
    // a validator references.
    RegisterType(dataType string, codec TypeCodec) error

    // Codec returns the registered codec for a DataType, or nil if
    // none is registered.
    Codec(dataType string) (TypeCodec, bool)
}

type TypeCodec interface {
    Marshal(value any) ([]byte, error)
    Unmarshal(raw []byte, target any) error
    Validate(raw []byte) error
}
```

Codecs perform deterministic serialization: stable JSON ordering,
Unicode NFC normalization for strings, smallest-int encoding for
integers, no whitespace. Replay produces bit-identical bytes for
identical inputs.

Registration of a typed validator via `RegisterValidator[T, R]`
implicitly registers `T` and `R` with the type registry if not
already registered. The registration uses Go's reflection to inspect
the types and synthesize default codecs for value types; explicit
codec registration is required for types with custom serialization
needs.

### 8.5 Dispatcher

The validator dispatcher is responsible for resolving handler IDs to
adapters, deserializing artifact data, invoking handlers within
bounded goroutine scopes, capturing results and errors, and committing
lifecycle transitions.

```go
type ValidatorDispatcher interface {
    // Dispatch resolves the validation's ValidatorID, deserializes
    // the target artifact's Data, invokes the typed handler, and
    // returns the result artifact along with any error. The
    // dispatcher does not commit lifecycle transitions; that is the
    // orchestrator's responsibility.
    Dispatch(
        ctx context.Context,
        validation *Validation,
        artifact *Artifact,
    ) (resultArtifact *Artifact, err error)
}
```

Dispatch flow:

1. Resolve `Validation.ValidatorID` to a `ValidatorAdapter` from the
   registry. If not found, return an error of category `dispatcher`.
2. Verify `Artifact.DataType == Adapter.ArtifactDataType`. Mismatch
   returns an error of category `artifact_type_mismatch`.
3. Verify `Validation.ArtifactDataType == Adapter.ArtifactDataType`.
   Mismatch returns an error of category `artifact_type_mismatch`.
4. Spawn a tracked goroutine in the orchestrator's scope. Configure
   deadline from `Validation.Timeout` if non-zero.
5. Invoke `Adapter.Invoke(ctx, artifact.Data)`. Recover panics into an
   error of category `panic`.
6. If the handler returned an error, propagate it for the orchestrator
   to commit `validation.validation_failed` or `errored` based on
   error category.
7. If the handler returned a result artifact, stamp the result's
   `DataType` and `ClaimID` (the original claim), set
   `Ephemeral=true`, and return.

### 8.6 Validator Determinism

Validators inherit the determinism levels from
`CLAIMS_AND_INFRASTRUCTURE.md §11.4`:

- `pure` — same inputs produce identical outputs.
- `content` — same inputs produce equivalent outputs modulo
  timestamps.
- `side_effect` — handler depends on external state.
- `nondeterministic` — handler's output cannot be replayed.

Most artifact validators are `pure` or `content`. Validators that
inspect external state (filesystem, network, real-time data) are
`side_effect`. Validators that produce nondeterministic output (e.g.,
embedding a sample of the artifact) are `nondeterministic`.

Determinism level affects replay strategy. Pure and content validators
may be re-executed during replay audit; side_effect and
nondeterministic validators are not re-executed.

### 8.7 Registry Bounds and Concurrency

The registry is read-mostly after boot. Registration occurs only
during boot. Lookups are non-blocking and lock-free.

The dispatcher maintains a bounded per-process invocation pool whose
capacity is derived from the registered validators' declared
expected-concurrent-invocation metadata. No magic numbers; capacity is
derived from declared inputs at boot time.

Overflow produces a dispatch error of category `dispatcher` with
payload `{"reason": "backpressure"}`. The orchestrator commits
`validation.errored` (or `_not_required` variant) and proceeds.

## 9. Result Testament Chain

Result artifacts are bundled into a per-claim result testament issued
by the claimant. This section defines the bundling, structural binding,
and asymmetric terminal lifecycle.

### 9.1 Per-Claim Bundling

For a given claim, every validation that produces a result artifact
contributes that artifact to a single result testament. The claimant
orchestrator collects result artifacts as validations terminate and,
once the claim's validation phase concludes (all required validations
reached terminal states), generates the result testament containing
the full bundle.

The bundling is per-claim, not per-validation and not per-artifact.
If a claim has ten validations producing ten result artifacts, those
all land in one result testament. The result testament's artifact
list contains all ten in the order the validations were dispatched.

### 9.2 Result Testament Structure

```go
type ResultTestament struct {
    // Same shape as a normal Testament.
    Testament

    // ClaimID is the original claim's ID, not a derived ID. The
    // result testament is structurally a child of the original claim.
    ClaimID string

    // Issuer is the claimant of the original claim.
    Relations []Relation  // includes issuer=claimant, claim=originalClaimID
}
```

The result testament is structurally identical to a normal testament
at the wire level. The only thing that distinguishes it is the
asymmetry of its lifecycle (described in §9.4) and the fact that its
issuer is the claimant of the parent claim rather than the target.

### 9.3 Result Artifact Structure

Result artifacts are standard `Artifact` records with the typed payload
from the validator's return value:

```go
Artifact {
    ID:               <generated>
    ParticipantID:    <claimant's UID>
    ClaimID:          <original claim's ID>
    TestamentID:      <result testament's ID, set when bundled>
    ArtifactName:     <derived from validator ID + validation index>
    Kind:             "validation_result"
    DataType:         <validator's ResultDataType>
    Data:             <serialized R>
    ContentHash:      <SHA-256 of Data>
    Ephemeral:        true
    Status:           ArtifactStatusGenerated   (terminal)
    Errors:           nil
    Presentation:     nil  (typically; validator may set if user-visible)
}
```

The `ArtifactName` is derived as `result:<validator_id>:<validation_id>`
so that result artifacts are uniquely named within the result testament.

### 9.4 Asymmetric Terminal Lifecycle

The result testament and its result artifacts have a degenerate
lifecycle that terminates early. This is intentional: the result
artifacts are evidence the claimant generated for the audit trail;
they are not work product to be consumed or re-validated.

Result testament lifecycle:

```text
testament.generated  (committed by claimant)
testament.posted     (committed by claimant; terminal)
```

Result artifact lifecycle:

```text
artifact.generated   (committed by claimant during validator dispatch; terminal)
```

The result testament never reaches `testament.received` because there
is no consuming participant (the claimant is both issuer and
testifier). The result artifacts never reach `artifact.received`,
`artifact.attached`, `artifact.validating`, or `artifact.validated`
because no consumer would receive or validate them.

This asymmetry is explicit and documented as an exception to the
general lifecycle rules in §5.2. The WAL records the truncated
lifecycle. Replay reconstructs the artifacts at `generated` and the
testament at `posted` without further transitions.

### 9.5 Why Not Symmetric Lifecycle?

A symmetric lifecycle would require the claimant to "receive" their
own testament, "attach" their own artifacts, "validate" them against
some meta-validator, and so on. This adds commit overhead and
artificial state transitions without producing useful evidence: the
claimant already knows the artifacts exist (they generated them) and
the artifacts are already evidence (no re-validation adds value).

The asymmetric model captures the evidence durably while avoiding the
recursion of validating validation results.

### 9.6 Querying Result Artifacts

Result artifacts are first-class evidence on the board despite their
truncated lifecycle. They are queryable via `query_claims_board` and
`traverse`. They appear in projections. They contribute to the Memory
Forest's harvest of accepted claims per `CLAIMS.md §14.15`.

Specifically: when an evaluator wants to examine the deterministic
verdict that a validator reached, it traverses from the original
claim, finds the result testament (by `caused_by` relation back to the
claim or by `issuer=claimant` filter), and reads the result artifacts.

### 9.7 Result Testament Issuance Timing

The claimant generates the result testament once the original claim's
validation phase concludes. The concluding event is the last required
validation reaching a terminal state. The result testament's commit
includes:

1. `testament.generated` for the result testament.
2. The result testament's full artifact list (all result artifacts
   from all validations on the claim).
3. `testament.posted` for the result testament (the result testament's
   terminal state).

No per-artifact attachment is committed for result artifacts because
their lifecycle terminates at `generated` (see §9.4).

## 10. Remediation via Corrective Claims

When validation surfaces structural issues, missing evidence, or
quality failures that suggest the original target should retry the
work, the claimant generates a corrective claim. This section defines
the corrective claim protocol.

### 10.1 Triggers

The claimant generates a corrective claim in the following situations:

1. The parent claim has reached `claim.validation_incomplete` because
   one or more required validations could not find their target
   artifact (the testament does not contain an artifact with the
   matching `ArtifactName`).
2. An artifact reached `artifact.receipt_failed` due to structural
   issues that the target can correct (malformed `Data`, missing
   metadata, invalid `DataType`).
3. A required validation reached `validation.validation_failed` due to
   semantic issues that suggest the target should retry with revised
   work (e.g., a test failed, the implementation does not satisfy the
   specification).
4. A required validation reached `validation.quality_bar_validation_failed`
   and the agent's quality assessment suggests retry would yield
   improved evidence.

Validations that reached `validation.errored` typically do not trigger
corrective claims; they trigger operator escalation, validator fixes,
or infrastructure repair per `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md
§4.validation_errored`.

### 10.2 Corrective Claim Structure

A corrective claim is a normal claim with the following distinctions:

1. Its `ActionType` is `claims.ActionTypeCorrective` per `CLAIMS.md
   §4.9`.
2. Its `Relations` include a relation pointing at the original claim
   with relationship `caused_by`:
   ```go
   Relation{
       Related:      originalClaimID,
       RelatedType:  claims.RelatedTypeClaim,
       Relationship: claims.RelationshipCausedBy,
   }
   ```
3. Its `Subject` (target) is the original target — the same participant
   that produced the original testament.
4. Its `Validations` array contains only the validations corresponding
   to the artifacts that need correction. The claimant does not re-issue
   validations that already succeeded against existing artifacts.

The corrective claim's payload describes what needs correction:

```go
Claim {
    Title:       "Correct missing/malformed artifacts for claim <original>"
    Description: "<specific corrections requested>"
    ActionType:  claims.ActionTypeCorrective
    Relations: [
        { Related: originalClaimID, RelatedType: claim, Relationship: caused_by },
        { Related: claimantUID,     RelatedType: agent, Relationship: issuer },
        { Related: targetUID,       RelatedType: agent, Relationship: subject },
    ]
    Validations: [<only the validations for the artifacts needing correction>]
    Scope: <same as original claim's scope, or narrowed to the affected artifacts>
}
```

### 10.3 Corrective Cycle

The corrective cycle is structurally identical to the original cycle:

1. Claimant generates and posts the corrective claim.
2. Target receives, performs corrective work, streams corrected
   artifacts.
3. Target generates a new testament against the corrective claim
   (the testament's `ClaimID` is the corrective claim's ID).
4. Claimant validates the new testament's artifacts using the
   corrective claim's declared validations.
5. If validation succeeds, the corrective claim reaches
   `claim.satisfied`.

The original claim's status is not modified by the corrective cycle's
success. The original claim remains at its terminal state (typically
`claim.validation_incomplete` or `claim.validation_failed`). The
corrective claim establishes a separate satisfied-state record.

### 10.4 Aggregation Across the Caused-By Chain

When an inspector or remediator examines the parent claim's full
remediation history, it traverses the `caused_by` relation chain
backwards. The chain may have arbitrary depth (a corrective claim may
itself need correction, and so on). The traversal collects:

- The original claim and its terminal state.
- Each corrective claim in chronological order and its terminal state.
- The final corrective claim that achieved satisfaction (if any).

This traversal is straightforward via `traverse` and `query_claims_board`.
No special aggregation primitive is needed; the relation graph
captures the lineage.

### 10.5 Corrective Claim Issuer Identity

The corrective claim's issuer is the claimant (the same participant
that issued the original claim). The corrective claim is not issued by
the target or by a third party. The participant that owns the
validation outcome owns the remediation.

In service participant scenarios where the claimant is non-agentic,
the corrective claim is generated automatically by the claimant's
runtime as part of validation orchestration. In agentic claimant
scenarios, the agent's tool loop generates the corrective claim via a
skill call after observing the validation outcome.

## 11. Cross-Document Lifecycle Propagation

The artifact and validation lifecycles defined in this document drive
testament and claim lifecycle transitions per
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`. This section pins down the
propagation rules without restating the testament/claim state
definitions, which are owned by that document.

### 11.1 Artifact to Validation Propagation

Validations begin executing only after their target artifact is in
`artifact.validating`. Concretely:

1. Artifact reaches `artifact.attached`.
2. Claimant orchestrator commits `artifact.validating`.
3. Orchestrator dispatches all validations targeting this artifact in
   parallel, transitioning each from `validation.ready` to
   `validation.validating`.

If the artifact's `Validating` state is short-circuited by a required
validation failure, the orchestrator commits `artifact.validation_failed`
per §5.3 and remaining validations stay at `validation.ready`.

### 11.2 Validation to Artifact Propagation

When validations terminate, their outcomes drive the parent artifact's
state:

| Validation outcome | Artifact propagation |
|---|---|
| All required validations reach `validation.validated` | `artifact.validated` |
| Any required validation reaches `validation.validation_failed` | `artifact.validation_failed` |
| Any required validation reaches `validation.errored` | `artifact.validation_failed` (errored sub-category) |
| Any required validation reaches `validation.quality_bar_validation_failed` | `artifact.validation_failed` (quality_bar sub-category) |
| Non-required validation reaches any `_not_required` variant | No artifact state change; failure recorded in artifact status history |

The orchestrator commits the artifact transition atomically with the
last validation transition that triggers it (for `validated`: the
last required validation reaching success; for `validation_failed`:
the first required validation reaching a blocking failure).

### 11.3 Artifact to Testament Propagation

Artifact transitions drive testament transitions per
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §6`. The propagation rules:

| Artifact state (all artifacts in testament) | Testament propagation |
|---|---|
| All artifacts at `artifact.validated` | `testament.validated` |
| Any artifact at `artifact.validation_failed` (required) | `testament.validation_failed` |
| Any artifact at `artifact.receipt_failed` | `testament.validation_incomplete` |
| Any required artifact missing from testament (no match for a declared validation's TargetArtifactName) | `testament.validation_incomplete` |
| Any validation reaches `validation.errored` (required) | `testament.validation_errored` |

The orchestrator commits testament transitions atomically with the
artifact transitions that trigger them.

### 11.4 Testament to Claim Propagation

Testament transitions drive claim transitions per
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §6`. Refer to that document for
the full claim state machine. Briefly:

| Testament state | Claim propagation |
|---|---|
| `testament.validated` | `claim.satisfied` |
| `testament.validation_failed` | `claim.validation_failed` |
| `testament.validation_incomplete` | `claim.validation_incomplete` |
| `testament.validation_errored` | `claim.validation_errored` |

This document does not redefine claim states. It defines only the
artifact and validation states whose outcomes propagate upward through
the testament layer into the claim layer.

### 11.5 Result Testament Does Not Propagate Upward

The per-claim result testament described in §9 does not affect the
original claim's lifecycle. The original claim's satisfaction depends
on the original target's testament reaching `testament.validated`. The
result testament is a parallel evidence record issued by the claimant
that captures the validator outputs; it terminates at `testament.posted`
and does not feed any lifecycle decision.

### 11.6 Propagation Atomicity

Each propagation commit (artifact → testament, testament → claim) is
atomic: the triggering child transition and the parent transition
commit within the same board transaction. This ensures replay
reconstructs the lifecycle in dependency order without partial states.

### 11.7 Propagation Authority

The claimant is the sole party committing artifact, testament, and
claim transitions in the propagation chain. The board does not compute
aggregates. The testifier commits only `artifact.generated`,
`artifact.attached` (atomically with `testament.generated`), and
`artifact.generation_failed`. All downstream propagation is the
claimant's responsibility.

## 12. Delta Actions

Every artifact and validation state transition emits a canonical delta
per the envelope in `CLAIMS_AND_DELTAS.md §6`. This section enumerates
the delta actions and pins down receiver behavior.

### 12.1 Artifact Delta Actions

| Action | Source | Required context fields |
|---|---|---|
| `artifact.generated` | testifier | artifact ID, claim ID, ArtifactName, DataType, ContentHash, Size, Ephemeral |
| `artifact.generation_failed` | testifier | artifact ID (when generated), claim ID, error category and payload |
| `artifact.received` | claimant | artifact ID, claim ID, ArtifactName |
| `artifact.receipt_failed` | claimant | artifact ID, claim ID, error category and payload |
| `artifact.attached` | testifier | artifact ID, testament ID, claim ID |
| `artifact.validating` | claimant | artifact ID, testament ID, claim ID, validation count |
| `artifact.validation_failed` | claimant | artifact ID, testament ID, claim ID, triggering validation ID, error category and payload |
| `artifact.validated` | claimant | artifact ID, testament ID, claim ID |

### 12.2 Validation Delta Actions

| Action | Source | Required context fields |
|---|---|---|
| `validation.ready` | claimant (validation creation) | validation ID, claim ID, ValidatorID, TargetArtifactName, Required |
| `validation.validating` | claimant (dispatch entry) | validation ID, claim ID, target artifact ID, validator ID |
| `validation.validation_failed` | claimant | validation ID, claim ID, target artifact ID, error |
| `validation.validation_failed_not_required` | claimant | validation ID, claim ID, target artifact ID, error |
| `validation.errored` | claimant | validation ID, claim ID, target artifact ID (when resolved), error |
| `validation.errored_not_required` | claimant | validation ID, claim ID, target artifact ID (when resolved), error |
| `validation.validating_quality_bar` | claimant agent | validation ID, claim ID, target artifact ID, evaluator ref, deterministic result artifact ID |
| `validation.quality_bar_validation_failed` | claimant agent | validation ID, claim ID, target artifact ID, evaluator ref, verdict reason |
| `validation.quality_bar_validation_failed_not_required` | claimant agent | validation ID, claim ID, target artifact ID, evaluator ref, verdict reason |
| `validation.validated` | claimant | validation ID, claim ID, target artifact ID, result artifact ID |

### 12.3 Delta Envelope Compliance

Every artifact and validation delta uses the canonical envelope from
`CLAIMS_AND_DELTAS.md §6`:

```json
{
  "schema": "sylk.claims.delta.v1",
  "action": "artifact.attached",
  "delta_id": "<unique>",
  "delta_key": "<deterministic>",
  "session_id": "<session>",
  "board_id": "<board>",
  "sequence": <board sequence>,
  "occurred_at": "<UTC timestamp>",
  "actor": <ParticipantRef of committing party>,
  "delivery": <when applicable, the intended receiver>,
  "refs": [
    { "role": "artifact",  "type": "artifact",  "id": "<artifact_id>" },
    { "role": "testament", "type": "testament", "id": "<testament_id>" },
    { "role": "claim",     "type": "claim",     "id": "<claim_id>" }
  ],
  "context": { /* action-specific payload */ }
}
```

### 12.4 Idempotency Keys

Per `CLAIMS_AND_DELTAS.md §6`, every delta has a stable
`delta_key`. For artifact and validation deltas:

```text
artifact.<action>:<board>:<artifact_id>:<sequence_at_transition>
validation.<action>:<board>:<validation_id>:<sequence_at_transition>
```

Receiver dimensions are added when applicable (e.g., for
`artifact.received` and `validation.validating_quality_bar`, the
receiver UID is part of the key).

### 12.5 UI Bridge Consumption

Per `CLAIMS_VISIBILITY.md §6`, the UI bridge consumes artifact and
validation deltas to render lifecycle-driven chat rows and progress
state. Artifact deltas drive per-artifact row rendering; validation
deltas drive per-validation status rendering. Result artifacts with
`Presentation` metadata are surfaced to the chat panel as user-visible
content (e.g., a Librarian's repository survey rendered as a chat
testament).

### 12.6 Continuation Resolution

Per `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md §16 Phase 4.2`, continuations
wait on lifecycle deltas. For waiting on artifact-level outcomes, a
continuation may wait on:

- `artifact.attached` for a specific artifact ID (e.g., a downstream
  consumer waiting for an upstream-produced artifact to be bound to a
  testament before proceeding).
- `artifact.validated` for a specific artifact ID.
- `validation.validated` for a specific validation ID.

These artifact-level waits supplement the testament- and claim-level
waits already specified in the lifecycle doc. Waits are bounded by
deadline per the same discipline.

## 13. Concurrency and Goroutine Ownership

Every component this document introduces — the validator dispatcher,
the per-artifact orchestrator, the result testament builder, the
remediation poster — obeys the project-wide concurrency discipline
from `CLAUDE.md` and reiterated across the claims documents.

### 13.1 Goroutine Ownership Graph

```text
process scope
└── claimant runtime scope
    ├── per-claim orchestrator scope
    │   ├── per-artifact orchestrator scope (one per artifact in testament)
    │   │   └── per-validation dispatch scope (one per validation on artifact)
    │   │       └── validator handler execution scope
    │   └── result testament builder scope
    └── remediation poster scope
```

Every scope:

- Has a parent.
- Has a context with deadline and cancellation.
- Has a quota budget.
- Records its goroutine count.
- Cancels on parent cancellation.
- Joins on parent shutdown with a deterministic timeout.

No goroutine is spawned via bare `go func()`. Every spawn goes through
`scope.Go(name, timeout, fn)` and is observable in operational
telemetry.

### 13.2 Bounded Queues

| Queue | Capacity derivation | Overflow behavior |
|---|---|---|
| Per-claim orchestrator inbound | `expected_concurrent_claims_per_claimant` declared at boot | `claim.validation_errored` with `dispatcher_backpressure` artifact |
| Per-artifact orchestrator inbound | `expected_artifacts_per_testament * concurrent_claims` declared at boot | `artifact.validation_failed` with `dispatcher_backpressure` error |
| Per-validation dispatch | `expected_concurrent_validations * artifacts_per_testament` declared at boot | `validation.errored` with `dispatcher_backpressure` error |
| Validator handler invocation pool | sum of handler-declared `expected_concurrent_invocations` | `validation.errored` with `dispatcher_backpressure` error |
| Result testament builder | one slot per pending result testament | block briefly; if blocked beyond declared timeout, escalate |
| Remediation poster | `expected_concurrent_corrective_claims` declared at boot | block briefly; if blocked beyond declared timeout, escalate |

No magic numbers. Every capacity derives from declared metadata at
participant-registration or boot time.

### 13.3 Validator Timeouts

Per-validation timeouts (`Validation.Timeout`) apply at the validator
handler execution scope. The dispatcher creates a child context with
deadline derived from `Validation.Timeout` (when non-zero) and invokes
the handler. If the handler exceeds the deadline, the dispatcher:

1. Cancels the handler's context.
2. Spawns a tracked cleanup goroutine to wait for the handler to
   actually return (handlers may not honor cancellation immediately).
3. Returns an error of category `timeout` to the orchestrator.
4. The orchestrator commits `validation.validation_failed` with the
   timeout error category per §6.7.

Cleanup goroutines have their own bounded scope and deadline; if a
handler hangs beyond the cleanup deadline, the runtime logs a
panic-equivalent event and forcibly releases the goroutine slot for
the dispatcher to reuse.

### 13.4 Parallelism Discipline

All validations on a single artifact execute in parallel. The
orchestrator dispatches them concurrently and awaits their
completions. Parallel execution is bounded by the per-validation
dispatch queue capacity (§13.2).

Within a single validation, the deterministic handler and the
quality-bar phase are sequential. The quality-bar phase only begins
after the deterministic phase returns successfully. The quality-bar
phase is bounded by the claimant agent's tool loop budget; it is not
a dispatched sub-task.

### 13.5 Lock Ordering

The lock-order discipline:

1. Board write lock is acquired only by the board's mutation methods.
   Validator handlers, orchestrators, dispatchers, and remediation
   posters never hold the board write lock while invoking handlers or
   publishing deltas.
2. Registry mutex (typed validators, type registry) is acquired only
   inside registry methods. Handlers do not acquire it.
3. Per-artifact orchestrator mutex is acquired only inside orchestrator
   methods. Validators do not acquire it.
4. Cross-component order: board > orchestrator > registry. Locks are
   acquired in this order only and never the reverse.

### 13.6 Shutdown Ordering

Process shutdown drains the orchestrator tree in deterministic order:

1. Stop accepting new testament deliveries.
2. Drain per-claim orchestrator queues with per-claim deadlines.
3. Cancel in-flight validator handlers; their cancellation produces
   `interrupted` error category on the corresponding validation
   records.
4. Resolve pending continuations waiting on validation outcomes; they
   become `validation.errored` with `shutdown` category if their
   target validation has not yet reached terminal.
5. Drain result testament builders; commit any partially-bundled
   result testaments at their current state with appropriate error
   artifacts for missing pieces.
6. Drain remediation poster queue.
7. Close board.
8. Join scope tree.

Shutdown produces a `process.shutdown` system testament per
`CLAIMS_AND_INFRASTRUCTURE.md §12.5`. Validator-related shutdown
events appear as artifacts on that testament.

### 13.7 Operational Telemetry

```text
claims_artifact_generated_total{participant_type, artifact_kind}
claims_artifact_attached_total{participant_type}
claims_artifact_received_total{claimant_type}
claims_artifact_receipt_failed_total{claimant_type, error_category}
claims_artifact_validated_total{claimant_type}
claims_artifact_validation_failed_total{claimant_type, triggering_validation_type}
claims_validation_ready_total
claims_validation_validating_total
claims_validation_validated_total{validator_id}
claims_validation_failed_total{validator_id, error_category, required}
claims_validation_errored_total{validator_id, error_category, required}
claims_validation_quality_bar_validated_total{validator_id, required}
claims_validation_quality_bar_failed_total{validator_id, required}
claims_validator_handler_duration_seconds{validator_id}
claims_validator_handler_timeout_total{validator_id}
claims_validator_handler_panic_total{validator_id}
claims_validator_dispatch_queue_depth{queue}
claims_validator_dispatch_overflow_total{queue}
claims_result_testament_bundled_total
claims_result_testament_bundle_size{quantile}
claims_remediation_corrective_claim_posted_total{trigger}
```

Every counter has a documented derivation. None uses a magic
threshold. Alarm thresholds are declared per participant in
registration metadata, not hardcoded.

## 14. Worked Examples

Concrete examples to ground the abstractions.

### 14.1 Deterministic Validation: VFS Provisioning

**Setup**: The Orchestrator agent issues a claim against the VFS
provisioner service to allocate a pipeline VFS.

**Claim**:

```text
Action {
  Type: task
  Issuer: orchestrator(uid=...)
  Subject: vfs_provisioner(uid=svc:vfs_provisioner:session/s1:pipeline/p1)
  Claim {
    ID: claim-001
    Title: "Provision pipeline VFS for pipeline P1"
    ActionType: task
    Validations: [
      {
        ID: v-001-receipt
        Type: receipt
        Required: true
        TargetArtifactName: "vfs_handle"
        ValidatorID: "claims.receipt"
        ArtifactDataType: "vfs_handle"
        ResultDataType: "receipt_result"
        Description: "VFS provisioner submits a vfs_handle artifact"
        QualityBar: ""
      },
      {
        ID: v-001-attached
        Type: contract
        Required: true
        TargetArtifactName: "vfs_handle"
        ValidatorID: "vfs.attachment"
        ArtifactDataType: "vfs_handle"
        ResultDataType: "vfs_attachment_result"
        Description: "VFS handle reports attached=true and non-empty mount"
        QualityBar: ""
      },
      {
        ID: v-001-capacity
        Type: contract
        Required: true
        TargetArtifactName: "vfs_handle"
        ValidatorID: "vfs.capacity"
        ArtifactDataType: "vfs_handle"
        ResultDataType: "vfs_capacity_result"
        Description: "VFS capacity meets the requested 1024 MB"
        QualityBar: ""
        Timeout: 0
      },
      {
        ID: v-001-base-version
        Type: contract
        Required: true
        TargetArtifactName: "vfs_handle"
        ValidatorID: "vfs.base_version"
        ArtifactDataType: "vfs_handle"
        ResultDataType: "vfs_version_result"
        Description: "VFS base_version equals session_head"
        QualityBar: ""
      }
    ]
  }
}
```

**Timeline**:

```text
t=0 ms:    Claim posted by Orchestrator → board commits claim-001
t=2 ms:    VFS provisioner handler dispatched, begins allocation
t=18 ms:   VFS provisioner generates vfs_handle artifact
           → artifact.generated for A1 (ArtifactName="vfs_handle")
t=19 ms:   Orchestrator observes A1 → artifact.received for A1
t=20 ms:   VFS provisioner generates vfs_topology artifact
           → artifact.generated for A2 (ArtifactName="vfs_topology")
t=20 ms:   Orchestrator observes A2 → artifact.received for A2
t=22 ms:   VFS provisioner work complete; generates testament
           → testament.generated for T1
           → artifact.attached for A1
           → artifact.attached for A2
t=22 ms:   Orchestrator observes T1 → testament.received for T1
t=22 ms:   Orchestrator commits artifact.validating for A1 (A2 has no targeting validations)
t=22 ms:   Orchestrator dispatches validations v-001-receipt, v-001-attached,
           v-001-capacity, v-001-base-version in parallel against A1
           → validation.validating for each
t=22 ms:   v-001-receipt completes (auto-pass on testament arrival)
           → validation.validated, result artifact R1 generated
t=23 ms:   v-001-attached handler runs validateVFSAttachment(ctx, vfs_handle)
           → returns Artifact[VFSAttachmentResult]{Attached:true, MountPoint:"/pipe/p1"}
           → validation.validated, result artifact R2 generated
t=23 ms:   v-001-capacity handler runs validateVFSCapacity(ctx, vfs_handle)
           → returns Artifact[VFSCapacityResult]{Requested:1024, Actual:1024, Satisfied:true}
           → validation.validated, result artifact R3 generated
t=23 ms:   v-001-base-version handler runs validateVFSBaseVersion(ctx, vfs_handle)
           → returns Artifact[VFSVersionResult]{BaseVersion:"session_head", Reachable:true}
           → validation.validated, result artifact R4 generated
t=23 ms:   All required validations on A1 reached validation.validated
           → artifact.validated for A1
t=23 ms:   All artifacts in T1 are at terminal state (A1.validated, A2 unvalidated terminal)
           → testament.validated for T1
           → claim.satisfied for claim-001
t=24 ms:   Orchestrator generates result testament T_results
           → testament.generated for T_results (ClaimID=claim-001)
           → testament.posted for T_results (terminal)
           Result artifacts R1, R2, R3, R4 attached to T_results
           Each result artifact terminates at artifact.generated
```

**End state**: claim-001 satisfied. Orchestrator has durable record of
the VFS allocation outcome and the per-validation evidence. Memory
Forest can harvest the accepted claim plus its result testament for
precedent.

### 14.2 Agentic Validation with Quality Bar: Plan Review

**Setup**: The user issues a prompt; the Architect agent produces a
plan. The user-approval Guardian agent acts as claimant against the
Architect, validating the plan's correctness and quality.

**Claim**:

```text
Action {
  Type: task
  Issuer: guardian(uid=agent:guardian:session/s1)
  Subject: architect(uid=agent:architect:session/s1)
  Claim {
    ID: claim-plan-001
    Title: "Architect produces user-reviewable plan for the request"
    ActionType: task
    Validations: [
      {
        ID: v-plan-receipt
        Type: receipt
        Required: true
        TargetArtifactName: "plan_markdown"
        ValidatorID: "claims.receipt"
      },
      {
        ID: v-plan-structure
        Type: contract
        Required: true
        TargetArtifactName: "plan_markdown"
        ValidatorID: "plan.structure"
        ArtifactDataType: "plan_markdown"
        ResultDataType: "plan_structure_result"
        Description: "Plan markdown contains required sections"
        QualityBar: ""
      },
      {
        ID: v-plan-quality
        Type: inspection
        Required: true
        TargetArtifactName: "plan_markdown"
        ValidatorID: "plan.basic_structure"
        ArtifactDataType: "plan_markdown"
        ResultDataType: "plan_basic_structure_result"
        Description: "Plan structure is well-formed and parses as markdown"
        QualityBar: |
          Inspect the plan_markdown artifact and assess:
            1. Does the plan address the user's actual request?
            2. Are the proposed tasks specific enough that an engineer
               can implement them without follow-up questions?
            3. Are the dependencies between tasks correctly captured?
            4. Are there any obvious risks or tradeoffs the plan ignores?
          The plan should rise to the quality bar of a senior engineer's
          design review. Reject if the plan glosses over implementation
          detail, hand-waves dependencies, or omits material risks.
        Timeout: 0
      }
    ]
  }
}
```

**Timeline**:

```text
t=0 s:     claim-plan-001 posted
t=0.1 s:   Architect agent begins planning
t=8.2 s:   Architect generates plan_markdown artifact → artifact.generated
t=8.2 s:   Guardian (claimant) observes the artifact → artifact.received
t=8.3 s:   Architect work complete; generates testament T_plan
           → testament.generated, artifact.attached for plan_markdown
t=8.3 s:   Guardian → testament.received for T_plan
t=8.3 s:   Guardian → artifact.validating for plan_markdown
t=8.3 s:   Guardian orchestrator dispatches three validations in parallel
t=8.3 s:   v-plan-receipt auto-passes (testament arrived)
           → validation.validated, R1 generated
t=8.31 s:  v-plan-structure handler runs validateBasicStructure(ctx, plan)
           → returns Artifact[PlanBasicStructureResult]{Parseable: true,
                                                         RequiredSectionsPresent: true,
                                                         TaskCount: 4}
           → validation.validated, R2 generated
t=8.32 s:  v-plan-quality handler runs validatePlanStructure(ctx, plan)
           → returns Artifact[PlanStructureResult]{Sections: [...], TaskCount: 4}
           → deterministic phase succeeds → validation.validating_quality_bar
t=8.32 s:  Guardian orchestrator injects plan_markdown content + quality bar text
           into Guardian agent's next prompt
t=8.32 s:  Guardian agent's next turn evaluates the plan against the quality bar
t=14.7 s:  Guardian agent emits evaluate_validation skill call:
             validation_id: v-plan-quality, verdict: passed,
             reason: "Plan covers all four phases with specific tasks,
                      explicit dependencies, and identified risks around
                      database migration ordering."
           → validation.validated for v-plan-quality, R3 generated
t=14.7 s:  All required validations on plan_markdown reached validated
           → artifact.validated for plan_markdown
           → testament.validated for T_plan
           → claim.satisfied for claim-plan-001
t=14.8 s:  Guardian generates result testament T_results
           Result artifacts R1, R2, R3 bundled, T_results terminates at posted.
```

**Notes**:

- The quality-bar phase added ~6.4 seconds of agent reasoning latency.
- The deterministic phase took under 30 ms.
- If the quality bar verdict had been failure, v-plan-quality would
  transition to `validation.quality_bar_validation_failed` (required,
  blocking), artifact.validation_failed would propagate, and Guardian
  would generate a corrective claim back to the Architect describing
  the quality issues.

### 14.3 Corrective Claim Cycle: Missing Artifact

**Setup**: A claim against the Librarian for a workspace survey
expects two artifacts. The Librarian's first testament includes only
one of them.

**Initial claim**:

```text
Claim {
  ID: claim-survey-001
  Title: "Survey existing Python infrastructure"
  Validations: [
    { ID: v-survey-files,    Required: true,
      TargetArtifactName: "workspace_files" },
    { ID: v-survey-deps,     Required: true,
      TargetArtifactName: "dependency_manifest" }
  ]
}
```

**Librarian's first testament**: contains only `workspace_files`
artifact. `dependency_manifest` is absent.

**Timeline**:

```text
t=0:     claim-survey-001 posted
t=2 s:   Librarian generates workspace_files → artifact.generated
t=2 s:   Architect (claimant) observes → artifact.received
t=2 s:   Librarian work complete; generates testament T1
         → testament.generated, artifact.attached for workspace_files
t=2 s:   Architect → testament.received for T1
t=2 s:   Architect attempts to dispatch v-survey-files against workspace_files
         → succeeds, validation.validated
t=2 s:   Architect attempts to dispatch v-survey-deps against dependency_manifest
         → no artifact with ArtifactName="dependency_manifest" in T1
         → testament.validation_incomplete for T1
         → claim.validation_incomplete for claim-survey-001
         → v-survey-deps remains at validation.ready (never dispatched)

t=2.1 s: Architect generates corrective claim:
         Claim {
           ID: claim-survey-002
           Title: "Correct missing dependency_manifest for claim claim-survey-001"
           ActionType: corrective
           Relations: [
             { Related: claim-survey-001, Type: claim, Relationship: caused_by },
             { Related: architect, Type: agent, Relationship: issuer },
             { Related: librarian, Type: agent, Relationship: subject }
           ]
           Validations: [
             { ID: v-survey-deps-corrective, Required: true,
               TargetArtifactName: "dependency_manifest", ... }
           ]
         }
t=2.1 s: claim-survey-002 posted

t=3 s:   Librarian generates dependency_manifest → artifact.generated
t=3.1 s: Architect → artifact.received
t=3.2 s: Librarian generates testament T2 against claim-survey-002
         → testament.generated, artifact.attached
t=3.2 s: Architect → testament.received for T2
t=3.2 s: Architect dispatches v-survey-deps-corrective → validation.validated
t=3.2 s: artifact.validated, testament.validated, claim.satisfied for claim-survey-002

End state:
  claim-survey-001: validation_incomplete (terminal — does not retroactively change)
  claim-survey-002: satisfied
  The remediation chain is queryable via the caused_by relation.
```

### 14.4 Streaming Failure: Testifier Errors Mid-Work

**Setup**: A claim against the DAG processor to allocate three
agents fails after the second agent allocation: the third agent's pod
cannot be scheduled.

**Timeline**:

```text
t=0:     claim-pipeline-001 posted (allocate 3 agents to pipeline P5)
t=10 ms: DAG processor generates pipeline_pod_state for engineer
         → artifact.generated (ArtifactName="pod_state_engineer")
t=11 ms: Orchestrator → artifact.received
t=15 ms: DAG processor generates pipeline_pod_state for tester
         → artifact.generated (ArtifactName="pod_state_tester")
t=16 ms: Orchestrator → artifact.received
t=25 ms: DAG processor attempts to allocate designer; scheduler rejects
         (no capacity)
t=25 ms: DAG processor generates error_diagnostic artifact
         → artifact.generated (ArtifactName="allocation_failure")
t=26 ms: Orchestrator → artifact.received
t=26 ms: DAG processor work complete (failure); generates testament T1
         → testament.generated containing pod_state_engineer,
           pod_state_tester, allocation_failure
         → artifact.attached for all three
t=26 ms: Orchestrator → testament.received for T1
t=26 ms: Orchestrator dispatches validations:
         - v-001-receipt: passes (testament arrived)
         - v-001-pod-count: fails (expected 3, got 2)
           → validation.validation_failed (required)
t=26 ms: Orchestrator stops dispatching remaining validations
         (sibling validations stay at ready)
         → artifact.validation_failed for pod_state_engineer
           (pod_state_tester and allocation_failure also have validation
           records targeting them; those are similarly short-circuited
           or never dispatched)
         → testament.validation_failed
         → claim.validation_failed for claim-pipeline-001
t=27 ms: Orchestrator agent observes claim.validation_failed; reads
         the allocation_failure artifact; decides to either retry with
         smaller agent count or escalate. Posts a corrective claim
         accordingly.
```

**Notes**:

- The DAG processor produced a testament even on failure. The
  testament is the closing signal; failure does not exempt the
  testifier from generating it.
- The partial work (two successful pod allocations) is durable as
  artifacts. The Orchestrator's recovery path may reuse them rather
  than re-allocating from scratch.
- The corrective claim is generated by the Orchestrator (the
  claimant), not by the DAG processor (the target).

## 15. Invariants

These invariants formalize the contract this document adds. They are
required for any implementation to be correct.

1. Every artifact carries an `ArtifactName` unique within its parent
   testament.
2. Every artifact carries a `DataType` string that matches a
   registered type-registry entry.
3. Every validation carries a `TargetArtifactName` that, at dispatch
   time, must match exactly one artifact's `ArtifactName` in the
   parent claim's responding testament.
4. Every validator is a typed Go function with signature
   `func(ctx, T) (Artifact[R], error)` registered against a stable
   `ValidatorID`.
5. A single registered validator may back multiple validation records;
   each record has its own independent lifecycle and result-artifact
   reference.
6. Validations are 1-1 with artifacts. No validator inspects more than
   one artifact. Cross-artifact verification is expressed as multiple
   validations, not as a single multi-artifact validator.
7. All validations targeting the same artifact run in parallel and do
   not depend on each other's outputs.
8. Required validation failure short-circuits remaining validations on
   the same artifact; siblings remain at `validation.ready`.
9. The deterministic phase always runs first; the quality-bar phase
   only runs if the deterministic phase succeeded and the validation
   declares a non-empty quality bar.
10. The quality-bar phase is agentic-exclusive. Non-agentic claimants
    cannot have validations with non-empty quality bars by
    registration-time enforcement.
11. The quality-bar phase is not a sub-claim; it runs inline in the
    claimant agent's existing tool loop.
12. Result artifacts are bundled into a per-claim result testament
    issued by the claimant with `ClaimID` set to the original claim.
13. The per-claim result testament terminates at `testament.posted`;
    result artifacts inside terminate at `artifact.generated`.
14. The testifier commits `artifact.generated`, `artifact.generation_failed`,
    and `artifact.attached`. The claimant commits all other artifact
    transitions.
15. `artifact.attached` is committed atomically with `testament.generated`.
16. The streaming-only model is the only model: testaments are
    generated when all required work is complete or when the testifier
    has encountered an error; artifacts always stream onto the board
    before the testament's commit.
17. Validation timeouts (`Validation.Timeout > 0`) trigger
    `validation.validation_failed` (not `validation.errored`), treating
    the timeout as the artifact failing to be processed in the
    specified time.
18. Validator infrastructure failures (panic, dependency unavailable,
    artifact-type mismatch, dispatcher backpressure) trigger
    `validation.errored` (or `_not_required` variant), which propagates
    to `testament.validation_errored` → `claim.validation_errored`.
19. The board stores transitions and emits deltas; it does not
    compute aggregates or derive states. All orchestration logic lives
    in the claimant runtime.
20. Receipt is per-artifact, opportunistic, and a status update only.
    Receipt does not consume the artifact, does not begin validation,
    and does not perform structural checking beyond presence
    acknowledgment.
21. `artifact.receipt_failed` is structural rejection (missing
    metadata, malformed data, name collision, hash mismatch, etc.).
    Receipt failure does not trigger validation; the claimant generates
    a corrective claim per §10.
22. Corrective claims use `ActionType: corrective` and a `caused_by`
    relation pointing at the parent claim per `CLAIMS.md`'s
    `Relations` field. Corrective cycles structurally mirror the
    original cycle.
23. Successful corrective cycles do not modify the original claim's
    terminal state. The original claim's terminal state is preserved
    in the WAL; the corrective claim establishes its own satisfied
    state.
24. Every transition appends a `StatusChange` record per
    `CLAIMS.md §4.1`.
25. Every artifact and validation state transition emits a canonical
    delta per `CLAIMS_AND_DELTAS.md §6` with the appropriate envelope,
    refs, and idempotency key.
26. Every dispatcher, orchestrator, builder, and remediation poster
    runs within a tracked goroutine scope; every queue is bounded with
    capacity derived from declared metadata; every overflow produces a
    durable error artifact and operational telemetry.
27. No artifact, validation, claim, or testament is versioned. Each is
    created once and traverses its lifecycle exactly once.
28. Lifecycle propagation (artifact → testament → claim) is committed
    atomically with the triggering child transition.
29. The Memory Forest may harvest accepted claims along with their
    result testaments as precedents per `CLAIMS.md §14.15`.
30. The UI bridge consumes artifact and validation deltas via the same
    canonical envelope as all other deltas. No category-specific
    bridge paths.

## 16. Cross-Document Reconciliation

This document modifies the phrasing and contracts of the existing
claims documents.

### 16.1 CLAIMS.md

Required updates to `docs/CLAIMS.md`:

1. §4.7 Artifact: extend with the structure from §4.1 of this
   document. Add `Status`, `StatusHistory`, `ArtifactName`, `DataType`,
   `Data`, `Errors`, `TestamentID`, `ClaimID` fields. Note that
   `Status` is a new field; legacy artifact records without status
   are treated as already-terminal-at-attached for replay
   compatibility.
2. §4.8 Validation: extend with the structure from §6.1 of this
   document. Add `TargetArtifactName`, `ValidatorID`,
   `ArtifactDataType`, `ResultDataType`, `Timeout`,
   `ResultArtifactID`, `Error`, `EvaluatedAt`, `EvaluatorRef` fields.
3. §4.9 Enums: add `ArtifactStatus` (8 states), expand `ValidationStatus`
   to 10 states with the new entries from §7.1 of this document.
4. §2.4 Validation Types: note that validations may be evaluated
   deterministically (typed validator handler) or agentically (quality
   bar phase). Cross-reference this document.
5. §2.5 Testament: note that testaments are streaming-only closing
   signals; artifacts stream first, testament closes. Cross-reference
   this document.
6. §2.6 Artifact: note that artifacts have a lifecycle. Cross-reference
   this document.

### 16.2 CLAIMS_AND_DELTAS.md

Required updates to `docs/CLAIMS_AND_DELTAS.md`:

1. §9 Canonical Delta Actions: add the artifact and validation delta
   actions from §12.1 and §12.2 of this document.
2. §11 Expected Tool Calls: note that expected tool calls map to
   validator handler invocations when validators register against the
   tool name. Cross-reference §8 of this document.

### 16.3 CLAIMS_AND_TESTAMENTS_LIFECYCLE.md

Required updates to `docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`:

1. §5 Testament Lifecycle: note that the testament lifecycle is
   driven by aggregation over its artifacts' lifecycles. The
   testament's transitions are committed by the claimant atomically
   with the artifact transitions that trigger them (per §11.6 of this
   document).
2. §4 Claim Lifecycle: note that `claim.validation_incomplete`,
   `claim.validation_failed`, and `claim.validation_errored` are
   triggered by artifact-level outcomes per §11 of this document.
3. §10 Consultations, Challenges, and Guardian Checks: note that
   peer-response testaments carry typed artifacts whose validation
   drives the consultation's lifecycle through this document's
   propagation rules.
4. §16 Phase 5 Validation Semantics: expand to reference the
   ten-state validation lifecycle from §7 of this document. Note that
   programmatic validators and agentic quality-bar phases are the two
   evaluation disciplines defined here.

### 16.4 CLAIMS_VISIBILITY.md

Required updates to `docs/CLAIMS_VISIBILITY.md`:

1. §3.2 Presentation: note that artifacts gain a lifecycle (per
   §5 of this document); UI rendering may key on artifact lifecycle
   states (e.g., progress bars driven by `artifact.validating`,
   completion glyphs driven by `artifact.validated`).
2. §5.1 Validators inspect presentation artifacts as evidence: note
   that programmatic validators read artifact `Data` via the type
   registry; agentic quality-bar evaluators read both `Data` and any
   `Presentation` metadata.
3. §6.2 Bridge: add handling for the new artifact and validation
   delta actions from §12 of this document. Result artifacts with
   `Presentation` set are surfaced through the same `ClaimPresentationMsg`
   path as any other presentable artifact.

### 16.5 CLAIMS_AND_INFRASTRUCTURE.md

Required updates to `docs/CLAIMS_AND_INFRASTRUCTURE.md`:

1. §9 Programmatic Validation: expand to reference the typed
   `Validator[T, R]` interface from §8.1 of this document. The
   `ProgrammaticValidator` interface defined there is the type-erased
   adapter surface; the typed `Validator[T, R]` is the registration-
   site interface.
2. §11.4 Handler Determinism: apply identically to typed validators.
   A pure validator is re-executed during replay audit; nondeterministic
   validators trust the stored result artifact.
3. §14 Catalog of Systems to Convert: each entry's
   "Programmatic validators" column refers to typed validators
   registered via this document's registration API. The validator IDs
   appear in the referenced Validation records' `ValidatorID` fields.

### 16.6 CLAUDE.md

Required updates to `CLAUDE.md`: none. The existing project rules
apply uniformly to typed validators, the dispatcher, the
orchestrator, the result testament builder, and the remediation
poster. No magic numbers, complexity bounded below 4 per function,
tracked goroutines, bounded queues, no silent drops, modern Go
structures.

## 17. Phased Implementation Plan

The phased plan follows the same structure as the other claims
documents. Each phase has a description, examples, acceptance criteria,
unit tests, vektra/mockery integration tests, E2E tests, and explicit
failure/race/deadlock test cases.

The phase list below is the compact migration summary. The normative
implementation contract is the phase matrix in section 17.1. When the
summary and the matrix differ, the matrix wins because it names the
files, existing APIs, integration points, acceptance criteria, and
required test cases.

### Phase 0: Documentation Reconciliation

Phase 0 updates the existing claims documents to reference this
document and establishes the vocabulary discipline before any code
work. No code changes.

#### Item 0.1: Update CLAIMS.md

**Description**: Apply the changes from §16.1 to `docs/CLAIMS.md`.

**Acceptance criteria**:

- Artifact and Validation structs include the new fields.
- Status and StatusHistory fields are documented.
- The new ArtifactStatus enum and expanded ValidationStatus enum are
  added.
- Cross-references to this document appear in §2.4, §2.5, §2.6.

**Tests**:

- Doc-lint: no remaining `AgentID` references in artifact/validation
  field definitions outside the migration note.
- Doc-lint: artifact lifecycle states listed match this document.
- Doc-lint: validation lifecycle states listed match this document.

#### Item 0.2: Update CLAIMS_AND_DELTAS.md

**Description**: Apply the changes from §16.2.

**Acceptance criteria**:

- §9 Canonical Delta Actions enumerates all 8 artifact and 10
  validation delta actions.
- Cross-reference to this document's §12.

#### Item 0.3: Update CLAIMS_AND_TESTAMENTS_LIFECYCLE.md

**Description**: Apply the changes from §16.3.

**Acceptance criteria**:

- Testament lifecycle aggregates over artifact lifecycles per §11.3
  of this document.
- Claim lifecycle propagation rules cross-reference this document.

#### Item 0.4: Update CLAIMS_VISIBILITY.md

**Description**: Apply the changes from §16.4.

**Acceptance criteria**:

- Artifact lifecycle awareness added to presentation contract.
- Bridge handles new delta actions.

#### Item 0.5: Update CLAIMS_AND_INFRASTRUCTURE.md

**Description**: Apply the changes from §16.5.

**Acceptance criteria**:

- ProgrammaticValidator interface clarified as the erased adapter for
  the typed Validator[T, R].
- Catalog entries reference typed validator IDs.

### Phase 1: Type System Foundation

Phase 1 lands the typed validator and artifact infrastructure in
`core/claims`. No production callers yet; tests exercise the
abstractions end-to-end against synthetic types.

#### Item 1.1: Artifact Structure Extensions

**Description**: Add the new fields from §4.1 to `claims.Artifact`:
`TestamentID`, `ClaimID`, `ArtifactName`, `DataType`, `Data`,
`Status`, `StatusHistory`, `Errors`, `Presentation`.

**Acceptance criteria**:

- Struct compiles and JSON round-trips preserve all fields.
- Existing fields (`Reference`, `Metadata`, `Kind`, etc.) preserved.
- Empty new fields serialize as omitted.
- Status defaults to `ArtifactStatusGenerated` for new artifacts.

**Unit tests**:

- JSON round-trip with all fields populated.
- JSON round-trip with only legacy fields (backward compat).
- Status history append preserves chronological order.
- Errors array supports multiple entries with distinct categories.

**Integration tests with vektra/mockery**:

- Mock board records artifact and reproduces it via projection with
  all fields intact.
- Mock WAL replay reconstructs identical artifact state.

**E2E tests**:

- A test claim with a generated artifact records the artifact with
  all fields and replays it.

**Failure/race/deadlock tests**:

- Concurrent reads of an artifact's status history do not race.

#### Item 1.2: ArtifactStatus Enum

**Description**: Add `ArtifactStatus` enum with the 8 states from
§5.1.

**Acceptance criteria**:

- Enum compiles with `Valid()` method returning true for valid states.
- JSON marshaling preserves string form.
- Transition helpers in `claims.CanTransitionArtifact(from, to)` cover
  every valid transition from §5.4.
- Terminal-state helpers identify generation_failed, receipt_failed,
  validation_failed, validated.

**Unit tests**:

- Each state returns `Valid()` true.
- Unknown states return false.
- Transition table covers all valid transitions.
- Invalid transitions return typed errors.

**Integration tests with vektra/mockery**:

- Mock orchestrator commits valid transitions; invalid attempts fail
  deterministically.

**E2E tests**:

- Synthetic artifact traverses generated → received → attached →
  validating → validated and replays.

**Failure/race/deadlock tests**:

- Concurrent transition attempts on the same artifact resolve to one
  commit (idempotent).

#### Item 1.3: Validation Structure Extensions

**Description**: Add the new fields from §6.1 to `claims.Validation`:
`ClaimID`, `TargetArtifactName`, `ValidatorID`, `ArtifactDataType`,
`ResultDataType`, `Timeout`, `ResultArtifactID`, `Error`,
`EvaluatedAt`, `EvaluatorRef`.

**Acceptance criteria**:

- Struct compiles and JSON round-trips preserve all fields.
- Existing fields preserved.
- `Timeout` defaults to zero meaning no timeout.

**Unit tests**: same shape as 1.1.

**Integration tests with vektra/mockery**: same shape.

**E2E tests**: same shape.

#### Item 1.4: ValidationStatus Enum Expansion

**Description**: Expand `ValidationStatus` to the 10 states from
§7.1.

**Acceptance criteria**:

- Enum compiles.
- Transition table covers §7.5.
- Terminal-state helpers identify validated, all failed variants,
  errored variants, quality bar failed variants.
- Blocking-failure helpers identify required-failure terminal states.

**Unit tests**:

- Each state returns `Valid()` true.
- Transition table covers all valid transitions.
- Blocking-failure helper returns true for validation_failed,
  errored, quality_bar_validation_failed; false for `_not_required`
  variants.

**Integration tests with vektra/mockery**: same shape.

**E2E tests**: traversal of every state pathway.

#### Item 1.5: ArtifactError and ValidationError Types

**Description**: Add `ArtifactError`, `ArtifactErrorCategory`,
`ValidationError`, `ValidationErrorCategory` types per §4.8 and §6.7.

**Acceptance criteria**:

- Types compile and JSON round-trip.
- Categories cover all defined cases.
- `Source` carries canonical `ParticipantRef`.

**Unit tests**:

- Each category serializes correctly.
- Empty error round-trips as zero value.

**Integration tests with vektra/mockery**: error attachment recorded
on artifact and validation correctly.

### Phase 2: Type Registry and Typed Helpers

Phase 2 implements the type registry and the generic typed-access
helpers for artifacts.

#### Item 2.1: TypeRegistry Implementation

**Description**: Implement `TypeRegistry` and `TypeCodec` interfaces
from §8.4.

**Acceptance criteria**:

- Registry holds (DataType string → TypeCodec) mappings.
- Registration fails on duplicate DataType.
- `Codec()` lookup returns the codec or false.
- Built-in JSON codec handles deterministic serialization (sorted
  keys, NFC normalization, smallest-int encoding, no whitespace).

**Unit tests**:

- Register and lookup round-trips.
- Duplicate registration fails.
- Built-in JSON codec produces identical bytes for identical inputs.
- Codec rejects malformed JSON.

**Integration tests with vektra/mockery**:

- Mock codec recorded for a synthetic type; lookup returns it.

**E2E tests**:

- Round-trip a typed payload through the registry.

**Failure/race/deadlock tests**:

- Concurrent registrations resolve deterministically.
- Codec panic during marshal is recovered as a typed error.

#### Item 2.2: ArtifactData Generic Helpers

**Description**: Implement `ArtifactData[T]`, `MustArtifactData[T]`,
`SetArtifactData[T]` from §4.4.

**Acceptance criteria**:

- `ArtifactData` deserializes via the registry codec.
- Type mismatch (artifact.DataType not matching T's registered type)
  returns typed error.
- `SetArtifactData` serializes, computes `ContentHash`, and sets
  `DataType` and `Size`.
- Generic types compile and instantiate for representative T values.

**Unit tests**:

- Round-trip set then get for a synthetic typed struct.
- Type mismatch returns typed error.
- Empty data deserializes as zero value.
- ContentHash is stable across reads.

**Integration tests with vektra/mockery**: mock codec invoked
correctly.

**E2E tests**: serialize then deserialize through board.

### Phase 3: Validator Registry

Phase 3 implements the validator registry and the typed registration
helper.

#### Item 3.1: ValidatorAdapter Type

**Description**: Implement `ValidatorAdapter` from §8.2.

**Acceptance criteria**:

- Adapter struct compiles with all fields.
- `Invoke` function is callable with raw bytes and returns artifact.

**Unit tests**:

- Synthetic adapter invokes a typed validator correctly.

#### Item 3.2: ValidatorRegistry Implementation

**Description**: Implement the registry with typed `RegisterValidator[T, R]`
and lookup by ID.

**Acceptance criteria**:

- `RegisterValidator[T, R]` compiles for arbitrary type pairs.
- Registration fails on duplicate ValidatorID.
- Lookup by ID returns the adapter.
- Registration auto-registers T and R types with the TypeRegistry if
  not present.

**Unit tests**:

- Register a typed validator and lookup by ID.
- Duplicate registration fails.
- Auto-registration of T and R works.

**Integration tests with vektra/mockery**:

- Mock validator invoked correctly through registry adapter.

**E2E tests**:

- End-to-end registration and dispatch produces the expected result
  artifact.

#### Item 3.3: ValidatorDispatcher Implementation

**Description**: Implement the dispatch flow from §8.5.

**Acceptance criteria**:

- Dispatcher resolves ValidatorID and verifies type matches.
- Deserialization, invocation, and result capture proceed in order.
- Panics in handlers are recovered as `ValidationError{Category: panic}`.
- Timeout deadlines enforced.
- Result artifact's parent fields (ClaimID, DataType) are stamped.

**Unit tests**:

- Each step in the dispatch flow tested in isolation.
- Panic recovery returns proper error category.
- Timeout returns proper error category.
- Type mismatch returns proper error category.

**Integration tests with vektra/mockery**:

- Mock handler invoked exactly once per dispatch.
- Mock orchestrator receives dispatch results in correct shape.

**E2E tests**:

- End-to-end dispatch with a real handler produces a result artifact
  attached to the original claim.

**Failure/race/deadlock tests**:

- Handler timeout cleanup goroutine releases scope cleanly.
- Concurrent dispatches on same validator do not corrupt registry.
- Handler panic during scope teardown is recovered.
- Backpressure overflow produces `dispatcher_backpressure` error.

### Phase 4: Per-Artifact Orchestrator

Phase 4 implements the per-artifact orchestrator that dispatches
validations in parallel and handles short-circuit.

#### Item 4.1: PerArtifactOrchestrator Implementation

**Description**: Implement orchestrator that, given an artifact and
its set of validations, dispatches them all in parallel and waits for
terminal states, committing transitions.

**Acceptance criteria**:

- All validations dispatched concurrently within bounded scope.
- Required failure short-circuits remaining dispatches.
- Non-required failures do not short-circuit.
- All result artifacts collected for result testament bundling.
- Artifact transitions committed atomically with terminal validation
  transitions.

**Unit tests**:

- Synthetic test with N validations: all succeed → artifact.validated.
- Synthetic test with one required failure → artifact.validation_failed
  with siblings at ready.
- Synthetic test with one optional failure → other validations
  continue, artifact.validated.
- Synthetic test with all optional → artifact.validated with
  failures recorded in status history.

**Integration tests with vektra/mockery**:

- Mock dispatcher invoked correctly for each validation.
- Mock board records all transitions in correct order.

**E2E tests**:

- Real claim with multiple validations succeeds end-to-end.

**Failure/race/deadlock tests**:

- Parallel dispatch under high load does not race.
- Short-circuit during in-flight dispatch does not cancel
  already-dispatched validations.
- Cancellation propagates to handler scopes correctly.

#### Item 4.2: PerClaimOrchestrator Implementation

**Description**: Implement orchestrator that, given a claim's
testament, iterates artifacts and spawns per-artifact orchestrators.

**Acceptance criteria**:

- Spawns one per-artifact orchestrator per artifact in testament.
- Aggregates artifact transitions to testament transitions.
- Handles missing-artifact case (no testament artifact matches a
  validation's TargetArtifactName).
- Commits final claim transition when all artifacts terminal.

**Unit tests**:

- All artifacts succeed → testament.validated → claim.satisfied.
- One artifact fails → testament.validation_failed → claim.validation_failed.
- Missing artifact → testament.validation_incomplete → claim.validation_incomplete.

**Integration tests with vektra/mockery**: as above.

**E2E tests**: end-to-end claim with multi-artifact testament.

### Phase 5: Result Testament Builder

Phase 5 implements the per-claim result testament builder.

#### Item 5.1: ResultTestamentBuilder Implementation

**Description**: Implement builder that collects result artifacts
from a claim's validations and generates the bundled result testament.

**Acceptance criteria**:

- Bundles all result artifacts for a single claim into one testament.
- Result testament's ClaimID = original claim ID.
- Result testament terminates at `testament.posted`.
- Result artifacts terminate at `artifact.generated`.

**Unit tests**:

- Bundle multiple result artifacts and verify testament structure.
- Empty bundle produces zero-artifact result testament.
- Result testament does not advance past posted.
- Result artifacts do not advance past generated.

**Integration tests with vektra/mockery**:

- Mock board records the result testament and artifacts at correct
  terminal states.

**E2E tests**:

- End-to-end: real validations produce result artifacts bundled into
  result testament.

**Failure/race/deadlock tests**:

- Builder bounded by per-claim concurrency.
- Partial bundle on shutdown commits with appropriate state.

### Phase 6: Receipt and Attachment Orchestration

Phase 6 wires the receipt and attachment commit flows.

#### Item 6.1: Testifier Attach-On-Generation

**Description**: Wire testifier runtime to commit `artifact.attached`
atomically with `testament.generated`.

**Acceptance criteria**:

- Testament generation commits artifact.attached for every artifact in
  the testament in one board transaction.
- Atomicity is verifiable in WAL.

**Unit tests**:

- Synthetic testament with N artifacts triggers N attached
  transitions in one transaction.

**Integration tests with vektra/mockery**: mock board records all
transitions atomically.

**E2E tests**: real testifier commits as expected.

**Failure/race/deadlock tests**:

- Testament generation failure aborts attached transitions (no
  partial commit).

#### Item 6.2: Claimant Receipt Commit

**Description**: Wire claimant runtime to commit `artifact.received`
during the unattached window and `artifact.receipt_failed` when
structural issues are detected.

**Acceptance criteria**:

- Claimant observes artifact via delta subscription.
- Claimant commits received within bounded latency.
- Structural checks (DataType, ArtifactName, ContentHash, ClaimID
  presence) run before received commit; failure triggers receipt_failed.
- Receipt idempotent.

**Unit tests**:

- Receipt for valid artifact succeeds.
- Receipt for artifact with empty DataType triggers receipt_failed.
- Receipt for artifact with bad content hash triggers receipt_failed.
- Receipt for artifact with duplicate ArtifactName (within testament)
  triggers receipt_failed.

**Integration tests with vektra/mockery**: mock subscriber and board
record receipts correctly.

**E2E tests**: real claim with valid and invalid artifacts produces
correct receipt outcomes.

**Failure/race/deadlock tests**:

- Concurrent receipt commits for the same artifact idempotent.
- Receipt during shutdown produces graceful exit.

### Phase 7: Quality-Bar Phase Dispatch

Phase 7 implements the quality-bar phase for agentic claimants.

#### Item 7.1: Quality-Bar Eligibility Enforcement

**Description**: At validation registration time, enforce that
non-empty quality bars are paired with agentic claimants. Reject at
registration if the claim's issuer category is not `agent`.

**Acceptance criteria**:

- Validation declaration with non-empty QualityBar and non-agent
  claimant is rejected.
- Validation declaration with empty QualityBar succeeds for any
  claimant category.
- Validation declaration with non-empty QualityBar succeeds for
  agentic claimant.

**Unit tests**:

- Each category × QualityBar empty/non-empty combination behaves as
  documented.

**Integration tests with vektra/mockery**: mock registration enforces
constraints.

**E2E tests**: real service claim with QualityBar fails to register.

#### Item 7.2: Quality-Bar Dispatcher

**Description**: When deterministic phase succeeds and QualityBar is
non-empty, transition to `validation.validating_quality_bar` and
inject the artifact and quality bar text into the claimant agent's
next turn.

**Acceptance criteria**:

- Transition to `validating_quality_bar` only after deterministic
  success.
- Artifact data is included in prompt context (or, if too large, the
  quality bar text instructs the agent how to retrieve it).
- Agent verdict captured via `evaluate_validation` skill call.
- Terminal transition to validated or quality_bar_validation_failed.

**Unit tests**:

- Synthetic agent verdict triggers correct terminal transition.
- Large artifact triggers retrieval instruction path.
- Quality bar verdict not yet emitted: validation remains at
  validating_quality_bar until verdict.

**Integration tests with vektra/mockery**:

- Mock agent invokes evaluate_validation correctly.
- Mock orchestrator handles agent timeout.

**E2E tests**:

- Real agent claimant processes quality bar and emits verdict.

**Failure/race/deadlock tests**:

- Agent turn cancellation produces interrupted artifact.
- Concurrent quality bar evaluations on different validations do not
  interfere.

### Phase 8: Lifecycle Propagation

Phase 8 wires the artifact → testament → claim propagation.

#### Item 8.1: ArtifactToTestamentPropagator

**Description**: Implement the propagation logic from §11.3.

**Acceptance criteria**:

- All artifacts validated → testament.validated.
- Any artifact validation_failed → testament.validation_failed.
- Any artifact receipt_failed → testament.validation_incomplete.
- Any missing artifact → testament.validation_incomplete.
- Any validation errored → testament.validation_errored.

**Unit tests**: each rule tested independently.

**Integration tests with vektra/mockery**: mock orchestrator drives
testament transitions correctly.

**E2E tests**: end-to-end claims exercise each propagation path.

#### Item 8.2: TestamentToClaimPropagator

**Description**: Cross-reference and wire to
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md` testament-to-claim rules.

**Acceptance criteria**:

- testament.validated → claim.satisfied.
- testament.validation_failed → claim.validation_failed.
- testament.validation_incomplete → claim.validation_incomplete.
- testament.validation_errored → claim.validation_errored.

**Unit/integration/E2E tests**: as above.

### Phase 9: Remediation

Phase 9 implements the corrective claim flow from §10.

#### Item 9.1: CorrectiveClaimGenerator

**Description**: Implement claimant runtime that generates corrective
claims when validation outcomes warrant them.

**Acceptance criteria**:

- claim.validation_incomplete with missing artifact triggers
  corrective claim with only the missing validation.
- claim.validation_failed (when configured) triggers corrective claim.
- Corrective claim's relations include caused_by.
- Corrective claim posted to original target.

**Unit tests**:

- Each trigger produces a corrective claim with correct structure.

**Integration tests with vektra/mockery**: mock board records the
corrective claim.

**E2E tests**: real remediation cycle (validation_incomplete →
corrective claim → satisfied).

**Failure/race/deadlock tests**:

- Corrective claim generation bounded by queue.
- Concurrent corrective claims for different artifacts do not
  interleave.

### Phase 10: UI Bridge and Delta Emission

Phase 10 wires artifact and validation deltas through the bridge.

#### Item 10.1: Delta Envelope Emission

**Description**: Emit canonical deltas per §12 for every artifact and
validation transition.

**Acceptance criteria**:

- Each transition produces a delta with correct action, refs, and
  context.
- Deltas have stable idempotency keys.

**Unit tests**: each action emits correct envelope.

**Integration tests with vektra/mockery**: mock bus records expected
deltas.

**E2E tests**: end-to-end flow produces complete delta stream.

#### Item 10.2: UI Bridge Consumption

**Description**: Update UI bridge to consume the new artifact and
validation deltas and render per-artifact rows alongside per-testament
rows.

**Acceptance criteria**:

- Artifact deltas drive per-artifact row state.
- Validation deltas drive per-validation status.
- Result artifacts with Presentation surface via ClaimPresentationMsg.
- No special-case paths for service-produced vs agent-produced
  artifacts.

**Unit tests**: bridge handles each delta action.

**Integration tests with vektra/mockery**: mock chat sink receives
expected messages.

**E2E tests**: real claim renders correctly.

### Phase 11: Cleanup and Contract Tests

Phase 11 removes legacy artifact/validation handling and locks in the
new contracts.

#### Item 11.1: Remove Direct Validation Returns

**Description**: Delete or hard-disable any code path where validation
outcomes are returned as Go values rather than recorded as durable
validation transitions.

**Acceptance criteria**:

- No production path returns a validation verdict as a Go value
  bypassing the board.
- Static checks fail if any such path is reintroduced.

#### Item 11.2: Contract Tests

**Description**: Comprehensive contract tests that enforce the
artifact and validation lifecycle rules.

**Acceptance criteria**:

- New artifact data type registration requires a registered codec.
- New validator registration requires declared input type, output
  type, and determinism level.
- New validation declaration requires either empty QualityBar or
  agentic claimant.
- No production code constructs an Artifact with empty ArtifactName,
  empty DataType, or empty ClaimID.
- No production code constructs a Validation without a matching
  registered ValidatorID.

**Unit/integration/E2E tests**: contract violations detected by tests.

### 17.1 Normative Phase Matrix

This matrix is the implementation backlog. Every phase is independently
reviewable and shippable, and every production-facing change must land
with the tests named in the same phase. The compact phase list above
states intent; this matrix states the work.

#### Global implementation rules

- Reuse the existing claims board as the source of truth. The board
  records durable state transitions; orchestrators decide when to ask
  for those transitions.
- Keep all new concurrency inside tracked scopes. Existing claims code
  uses bounded board/projector scopes; new artifact and validation
  dispatch must follow the same pattern and must not start untracked
  goroutines.
- Add mockable boundaries before integration or E2E tests depend on
  external behavior. Interfaces must be narrow, live near the consumer,
  and include `//go:generate mockery` comments matching the existing
  style in `core/session/interfaces.go` and the repository
  `.mockery.yaml`.
- Use vektra/mockery for integration and E2E mocks. Integration tests
  mock collaborators around the package under test; E2E tests use real
  claims board, WAL, projector, and UI bridge surfaces wherever those
  are the behavior being verified, while mocking provider, agent-turn,
  bus, clock, and validator-handler boundaries.
- All lifecycle transitions need idempotency tests, replay tests, and
  duplicate-delivery tests. Replays must not create extra artifacts,
  validations, deltas, result testaments, UI rows, or remediation
  claims.
- Race and deadlock tests must run under `go test -race` for packages
  that introduce dispatch, projection, bridge delivery, or WAL replay
  concurrency. Timeouts and queue bounds must be derived from test data
  or configuration, not embedded magic numbers.
- No production caller may bypass the board for artifact, validation,
  testament, or claim truth. Return values can report local execution
  results, but durable truth must be represented as board state,
  canonical deltas, and replayable WAL events.

#### Existing implementation surfaces to maximize

- Types and clones: `core/claims/types.go`, especially `Claim`,
  `Testament`, `Artifact`, `Validation`, `StatusChange`,
  `CloneArtifact`, `CloneTestamentEntity`, and `CloneClaimEntity`.
- Board mutation APIs: `core/claims/board.go` (`PostAction`,
  `SubmitTestaments`, `EvaluateValidation`) and
  `core/claims/board_lifecycle.go` (`GenerateClaimAction`,
  `PostGeneratedClaim`, `GenerateTestamentAction`,
  `AcknowledgeTestamentReceipt`, `BeginTestamentValidation`,
  `CompleteTestamentValidation`).
- Durable storage and replay: `core/claims/board_durable.go`,
  `core/claims/outbox.go`, `core/claims/canonical_delta_projector.go`,
  and `core/claims/projection_rebuild.go`.
- Delta vocabulary and projection: `core/claims/canonical_delta.go`,
  `core/claims/deltas.go`, `core/claims/board_amplifier.go`, and
  `docs/CLAIMS_AND_DELTAS.md`.
- Existing validation/evidence adapters:
  `core/claims/expected_tool_execution.go`,
  `core/claims/presentation_evidence.go`, and `core/claims/skills.go`
  (`EvaluateValidationSkill`).
- Agent skill wiring: `agents/*/skills.go`, especially the claimant
  agents that already expose validation-related skills.
- UI bridge and rendering: `ui/bridge/claims.go`, `ui/msg`,
  `ui/chat/claim_render.go`, `ui/chat/model.go`, and `ui/chat/renderer.go`.
- Existing mockery conventions: `.mockery.yaml`,
  `core/session/interfaces.go`, `core/session/mocks`, and
  `agents/archivalist/mocks`.

### Phase 0: Contract Reconciliation and Test Harness

Phase 0 does not change runtime behavior. It makes the written contract,
interface boundaries, fixtures, and test harness precise enough that the
later implementation cannot drift.

#### Item 0.A: Cross-document contract reconciliation

**Description:** Reconcile this document with the surrounding claims
documents so artifact and validation lifecycle terminology has one
meaning. This includes all struct fields, status names, delta actions,
propagation rules, and migration semantics.

**Files and integration points:** `docs/CLAIMS.md`,
`docs/CLAIMS_AND_DELTAS.md`,
`docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`, `docs/CLAIMS_VISIBILITY.md`,
`docs/CLAIMS_AND_INFRASTRUCTURE.md`, `docs/CLAIMS_OPERATIONS.md`, and
this file.

**Existing APIs:** Document the current `Claim`, `Testament`,
`Artifact`, `Validation`, `StatusChange`, `ClaimLifecycleStatus`,
`TestamentLifecycleStatus`, `DeltaAction`, and `EvaluateValidationSkill`
surfaces before naming the new fields or statuses.

**Acceptance criteria:**

- Every status and delta action named in sections 5, 7, 11, and 12 is
  listed in the owning claims document.
- Field names in prose match Go field names exactly.
- The streaming-only artifact model is stated consistently across this
  file, `CLAIMS.md`, and `CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`.
- Legacy compatibility rules in section 18 do not contradict any new
  lifecycle rule.
- The documentation clearly states which phase first permits production
  callers.

**Test cases:**

- Unit/doc lint: table-driven checks over the markdown files verify that
  artifact statuses, validation statuses, and delta actions are not
  missing from cross-referenced documents.
- Negative path: doc lint fails when a lifecycle state is added to this
  file but not to the corresponding owning document.
- Edge case: legacy statuses such as `passed` and `failed` remain
  documented only as compatibility projections when the new lifecycle
  statuses exist.
- Simulated usage: a generated markdown fixture renders the complete
  lifecycle sequence from artifact generation through claim terminal
  state without unresolved references.

#### Item 0.B: Mockery boundary inventory

**Description:** Define the interfaces that later phases will mock, but
do not change runtime behavior. Interfaces should exist only where tests
or orchestration require substitutable collaborators.

**Files and integration points:** New or updated interface files near
their consumers, for example `core/claims/validator_interfaces.go`,
`core/claims/orchestrator_interfaces.go`, `ui/bridge/claims_interfaces.go`,
and package-local mocks under `core/claims/mocks` and `ui/bridge/mocks`.
Update `.mockery.yaml` when a package-level mock configuration is the
repository convention for that package.

**Existing APIs:** Model the generation comments after
`core/session/interfaces.go`. Reuse existing interfaces such as
`ExpectedToolExecutor`, `ExpectedToolPolicy`,
`ExpectedToolArgumentRedactor`, `ValidationExpectedToolRemediationPoster`,
`PresentationMetricSink`, `TeaProgram`, and bridge sinks where they
already fit.

**Acceptance criteria:**

- Every new orchestrator, dispatcher, validator handler, agent-turn
  evaluator, bus sink, clock, and durable collaborator required by later
  tests has an explicit interface.
- No broad "god" interfaces are introduced. Each interface is scoped to
  one consumer workflow.
- Generated mocks are deterministic and live in package-local `mocks`
  directories.
- Mockery generation is reproducible with `go generate` or the existing
  `.mockery.yaml` package entry.

**Test cases:**

- Unit: compile-time interface assertions verify real implementations
  satisfy the interfaces.
- Integration/mockery: generated mocks compile and can assert one
  expected call with argument matching.
- Negative path: attempting to use a mock without satisfying required
  methods fails at compile time.
- Simulated usage: a fake validation dispatcher test uses only the
  interface and mock, proving downstream tests do not reach into concrete
  internals.

#### Item 0.C: Fixture and invariant catalog

**Description:** Create reusable test fixtures for claims, testaments,
artifacts, validations, deltas, WAL events, and UI messages. The
fixtures should make lifecycle transitions readable while avoiding
hard-coded sleeps or hidden global state.

**Files and integration points:** `core/claims/test_helpers_test.go`,
`core/claims/lifecycle_test.go`, `core/claims/board_durable_test.go`,
`core/claims/bus_integration_test.go`, `ui/bridge/claims_integration_test.go`,
and any package-local fixture files needed by later phases.

**Existing APIs:** `NewClaimsBoard`, `DurableBoard`, `Projection`,
`SubscribeProjection`, `DrainOutbox`, `GenerateTestamentAction`,
`AcknowledgeTestamentReceipt`, `BeginTestamentValidation`, and
`CompleteTestamentValidation`.

**Acceptance criteria:**

- Fixture builders can produce a valid claim with one artifact and one
  validation, a multi-artifact claim, and a malformed legacy artifact.
- Fixtures derive IDs, queue lengths, deadlines, and timeout budgets from
  test inputs.
- Fixtures expose deterministic clocks or injected time where replay and
  ordering tests require stable output.
- The same fixtures support unit, integration, and E2E tests.

**Test cases:**

- Unit: fixture-generated claims pass existing board validation.
- Integration/mockery: fixture-generated dispatcher mocks drive a
  successful validation transition.
- E2E: fixture-generated WAL data replays through `DurableBoard` and
  projects equivalent board state.
- Race/deadlock: repeated fixture setup and teardown under `t.Parallel`
  does not race, leak goroutines, or leave blocked subscriptions.

### Phase 1: Type System and Lifecycle Foundation

Phase 1 lands the schema, enums, helper methods, and compatibility
projections. Production callers must still use existing paths.

#### Item 1.A: Artifact schema, status, and errors

**Description:** Extend `Artifact` into the first-class typed evidence
record described in section 4. Add lifecycle status and error metadata
without breaking existing artifact producers.

**Files and integration points:** `core/claims/types.go`,
`core/claims/presentation.go`, `core/claims/presentation_evidence.go`,
`core/claims/render.go`, and any JSON/WAL fixture code that touches
artifacts.

**Existing APIs:** `Artifact`, `CloneArtifact`, `CloneTestamentEntity`,
`SubmitTestaments`, `GenerateTestamentAction`, `Presentation`,
`ValidateArtifactEvidence`, and presentation artifact kinds such as
`ArtifactKindPlanMarkdown`.

**Acceptance criteria:**

- `Artifact` has `ClaimID`, `ArtifactName`, `DataType`, `Data`,
  `Status`, `StatusHistory`, and `Errors` fields in addition to the
  current `TestamentID`, `Kind`, `Reference`, `Metadata`, `ContentHash`,
  `Size`, `Ephemeral`, and `Presentation`.
- Legacy artifacts with only `Kind` and `Reference` continue to
  deserialize and project.
- New artifacts default to the generated status at creation boundaries,
  not inside JSON unmarshalling.
- `CloneArtifact` deep-copies lifecycle history, errors, raw data, and
  presentation metadata.
- Error categories distinguish generation, receipt metadata, receipt
  structural, validation, timeout, panic, interruption, and internal
  failures.

**Test cases:**

- Unit happy path: JSON round-trip preserves every new artifact field.
- Unit negative path: an invalid status is rejected by validation
  helpers and cannot transition.
- Unit edge case: legacy artifacts deserialize with zero-value lifecycle
  fields and project as legacy-compatible attached artifacts.
- Integration/mockery: a mock board collaborator receives a generated
  artifact and observes the same copied fields via projection.
- E2E: a durable board writes a generated artifact, closes, reopens, and
  replays the artifact with identical status history and content hash.
- Race/deadlock: concurrent projections while artifact status history is
  appended do not race and do not expose mutable slices.

#### Item 1.B: Validation schema, status, and errors

**Description:** Extend `Validation` into a targetable lifecycle record
that can bind to a typed artifact, a registered deterministic validator,
and an optional agentic quality bar.

**Files and integration points:** `core/claims/types.go`,
`core/claims/board.go`, `core/claims/expected_tool_execution.go`,
`core/claims/skills.go`, `agents/*/skills.go`, and any claim fixture
builders.

**Existing APIs:** `Validation`, `ValidationStatus`,
`ValidationType`, `ExpectedToolCall`, `EvaluateValidation`,
`EvaluateValidationSkill`, and `ValidationExpectedToolExecutionResult`.

**Acceptance criteria:**

- `Validation` has `TargetArtifactName`, `ValidatorID`,
  `ArtifactDataType`, `ResultDataType`, `Timeout`, `ResultArtifactID`,
  `Error`, `EvaluatedAt`, and `EvaluatorRef`.
- Existing `pending`, `in_progress`, `passed`, `failed`, `errored`, and
  `skipped` states continue to load as compatibility statuses.
- New lifecycle helpers distinguish deterministic execution states,
  quality-bar states, optional failure states, required blocking states,
  and terminal states.
- `CloneClaimEntity` deep-copies validation lifecycle fields and errors.
- Validation declarations can still include `ExpectedToolCalls`, but the
  execution result is represented as artifacts and lifecycle transitions.

**Test cases:**

- Unit happy path: a validation targeting an artifact by name round-trips
  through JSON and clone helpers.
- Unit negative path: missing `TargetArtifactName` is rejected for typed
  validators.
- Unit edge case: a quality-bar-only legacy validation remains valid
  when no `ValidatorID` exists.
- Integration/mockery: a mock validator dispatcher updates exactly one
  validation and leaves unrelated validations unchanged.
- E2E: a claim with mixed legacy and typed validations replays without
  losing either validation shape.
- Race/deadlock: concurrent validation projections expose defensive
  copies and never share mutable status histories.

#### Item 1.C: Transition tables and compatibility projections

**Description:** Implement explicit transition validation for artifact
and validation lifecycle states and define how old coarse statuses map
into the new state machine.

**Files and integration points:** `core/claims/types.go`,
`core/claims/board.go`, `core/claims/board_lifecycle.go`,
`core/claims/lifecycle_test.go`, and `docs/CLAIMS.md`.

**Existing APIs:** `StatusChange`, `ClaimStatus`, `ValidationStatus`,
`ClaimLifecycleStatus`, `TestamentLifecycleStatus`, and the existing
claim/testament transition helpers.

**Acceptance criteria:**

- Transition helpers cover every edge in sections 5.4 and 7.5.
- Invalid transitions return typed errors that include entity ID, from
  status, to status, and actor.
- Idempotent duplicate transitions return a no-op result rather than
  appending duplicate history.
- Compatibility projections from old statuses are documented and tested.
- No caller can skip from generated directly to validated, from
  validating back to attached, or from any terminal state to a
  non-terminal state.

**Test cases:**

- Unit happy path: every valid artifact and validation transition is
  accepted by a table-driven test.
- Unit negative path: every forbidden transition in the inverse table is
  rejected.
- Unit edge case: duplicate terminal transition is idempotent and does
  not append history.
- Integration/mockery: mocked orchestrator attempts an invalid transition
  and receives the typed error without mutating board state.
- E2E: WAL replay of a legacy validation status produces the documented
  compatibility projection.
- Race/deadlock: concurrent duplicate transitions on the same entity
  serialize through the board lock and produce one committed transition.

### Phase 2: Type Registry and Artifact Data Helpers

Phase 2 makes typed artifact data usable without changing production
orchestration.

#### Item 2.A: Type registry and deterministic codecs

**Description:** Implement the registry that maps `DataType` strings to
codecs and gives validators deterministic bytes for hashing, replay, and
WAL comparison.

**Files and integration points:** New `core/claims/type_registry.go`,
new `core/claims/type_registry_test.go`, `core/claims/types.go`, and
fixture code from Phase 0.

**Existing APIs:** `Artifact.ContentHash`, `Artifact.Size`,
`Artifact.Metadata`, `ClaimScopeEntry`, and existing JSON artifact
metadata paths.

**Acceptance criteria:**

- `TypeRegistry` can register, look up, and list codecs by data type.
- Duplicate registrations fail deterministically.
- Registry reads are safe while other goroutines perform lookups.
- Built-in JSON codec produces deterministic bytes for JSON-compatible
  values and rejects malformed input.
- Codec errors include the data type and operation.

**Test cases:**

- Unit happy path: register and look up a synthetic type codec.
- Unit negative path: duplicate registration and unknown lookup return
  typed errors.
- Unit edge case: nil and zero-value payloads produce deterministic
  encoded data or explicit typed errors.
- Integration/mockery: a mock codec verifies marshal and unmarshal calls
  are made exactly once per helper invocation.
- E2E: typed artifact data survives board commit, WAL replay, and
  projection with the same content hash.
- Race/deadlock: concurrent lookups and registrations under the race
  detector do not corrupt the registry or block forever.

#### Item 2.B: Generic artifact data helpers

**Description:** Implement typed helpers for reading and writing
artifact payloads while preserving existing artifact metadata fields.

**Files and integration points:** New `core/claims/artifact_data.go`,
new `core/claims/artifact_data_test.go`, `core/claims/types.go`, and
`core/claims/presentation_evidence.go`.

**Existing APIs:** `Artifact`, `CloneArtifact`, `ContentHash`,
`Size`, `Presentation`, and the artifact kinds already used for plan
markdown and expected tool execution.

**Acceptance criteria:**

- `SetArtifactData[T]` encodes the payload, sets `DataType`, `Data`,
  `ContentHash`, and `Size`, and preserves unrelated fields.
- `ArtifactData[T]` decodes only when the artifact data type matches the
  registered type for `T`.
- `MustArtifactData[T]` is test-only or internal-only and never used on
  untrusted production data.
- Content hashes are computed from canonical data bytes, not from
  presentation metadata or references.
- Large payloads use bounded allocations and never copy data more than
  required by defensive ownership.

**Test cases:**

- Unit happy path: set then get a typed struct and compare the result.
- Unit negative path: type mismatch returns a typed artifact-data error.
- Unit edge case: empty data, nil artifact, unknown codec, and hash
  mismatch each produce distinct outcomes.
- Integration/mockery: mock codec panics during marshal; helper recovers
  or returns a controlled typed error according to the registry policy.
- E2E: board projection returns a defensive copy whose data mutation
  cannot affect board state.
- Race/deadlock: repeated typed reads from concurrent goroutines do not
  race with projection cloning.

#### Item 2.C: Built-in artifact data catalog

**Description:** Register concrete data types for the artifact shapes
already present in Sylk so migrations can target real subsystems rather
than synthetic-only types.

**Files and integration points:** `core/claims/expected_tool_execution.go`,
`core/claims/presentation_evidence.go`, `core/claims/carry_forward.go`,
`core/claims/knowledge_readiness.go`, `docs/CLAIMS_AND_INFRASTRUCTURE.md`,
and new catalog tests.

**Existing APIs:** `ArtifactKindPlanMarkdown`,
`ArtifactKindExpectedToolInvocation`, `ArtifactKindExpectedToolOutput`,
`ArtifactKindExpectedToolSkipped`, carry-forward testament artifacts,
and presentation evidence helpers.

**Acceptance criteria:**

- Each existing artifact kind that should become typed has a named data
  type and codec ownership.
- Existing string/reference artifacts remain readable during migration.
- The catalog is queryable by tests and future validators.
- Data type names are stable and documented.

**Test cases:**

- Unit happy path: each built-in data type registers successfully.
- Unit negative path: duplicate built-in registration fails in a
  controlled initialization test.
- Unit edge case: legacy reference-only artifacts are not forced through
  typed decoding.
- Integration/mockery: mock catalog consumer resolves the expected
  codec for plan markdown and expected-tool output.
- E2E: a plan markdown artifact can be produced through the old path and
  read through the typed helper when the migration bridge is enabled.

### Phase 3: Board Transition APIs, WAL, and Replay

Phase 3 adds board-level mutation APIs for artifacts and validations.
This is the first phase where the durable substrate understands the new
lifecycles, but production orchestrators still remain off.

#### Item 3.A: Artifact lifecycle board APIs

**Description:** Add board methods for generating, receiving, failing,
attaching, validating, and terminally resolving artifacts. The board
must validate transitions and stamp history; callers must not mutate
artifact status directly.

**Files and integration points:** `core/claims/board_lifecycle.go`,
`core/claims/board.go`, `core/claims/board_durable.go`,
`core/claims/types.go`, and `core/claims/lifecycle_test.go`.

**Existing APIs:** `GenerateTestamentAction`,
`AcknowledgeTestamentReceipt`, `BeginTestamentValidation`,
`CompleteTestamentValidation`, `SubmitTestaments`,
`appendDurableEventLocked`, and `outboxRecordLocked`.

**Acceptance criteria:**

- Methods exist for `artifact.generated`, `artifact.received`,
  `artifact.receipt_failed`, `artifact.attached`,
  `artifact.validating`, `artifact.validated`, and
  `artifact.validation_failed`.
- Each method stamps participant, time, sequence, status history, parent
  claim ID, and parent testament ID where applicable.
- `artifact.attached` is committed atomically with testament generation.
- `artifact.received` is idempotent during the unattached window.
- Terminal artifact states reject all non-idempotent follow-up
  transitions.

**Test cases:**

- Unit happy path: generated to received to attached to validating to
  validated records the expected history.
- Unit negative path: attached before generated and received after
  terminal state are rejected.
- Unit edge case: generated to attached directly is valid when the
  claimant did not observe the unattached window.
- Integration/mockery: a mock lifecycle observer receives exactly one
  transition callback per committed mutation.
- E2E: durable board replays artifact lifecycle transitions with stable
  sequences and no duplicate histories.
- Race/deadlock: concurrent receive and attach attempts serialize and
  produce a legal final state without blocking the board lock.

#### Item 3.B: Validation lifecycle board APIs

**Description:** Add board methods that transition validations through
ready, deterministic validation, deterministic result, quality-bar
validation, and terminal states.

**Files and integration points:** `core/claims/board_lifecycle.go`,
`core/claims/board.go`, `core/claims/board_durable.go`,
`core/claims/expected_tool_execution.go`, and `core/claims/skills.go`.

**Existing APIs:** `EvaluateValidation`, `ValidationStatus`,
`StatusChange`, `EvaluateValidationSkill`,
`CompleteTestamentValidation`, and expected-tool validation execution.

**Acceptance criteria:**

- Board APIs exist for every validation delta action in section 12.
- Existing `EvaluateValidation` becomes a compatibility adapter or a
  thin wrapper over the lifecycle APIs.
- Deterministic validator result artifacts are attached to
  `ResultArtifactID` through board mutation, not local assignment.
- Optional validation failures use `_not_required` terminal states and
  do not block artifact validation.
- Required failure states are queryable by artifact and testament
  propagation code.

**Test cases:**

- Unit happy path: ready to validating to validated records evaluator
  and result artifact references.
- Unit negative path: quality-bar validation before deterministic
  success is rejected.
- Unit edge case: optional deterministic failure terminates as
  `validation_failed_not_required`.
- Integration/mockery: `EvaluateValidationSkill` drives the new board
  API through a mocked claimant actor.
- E2E: expected-tool execution records validation artifacts and updates
  validation lifecycle through the compatibility adapter.
- Race/deadlock: concurrent evaluation attempts for the same validation
  are idempotent or rejected without duplicate result artifacts.

#### Item 3.C: WAL event and outbox support

**Description:** Persist every new artifact and validation mutation to
the WAL and project canonical outbox records. WAL-first semantics must
match the existing durable board rules.

**Files and integration points:** `core/claims/board_durable.go`,
`core/claims/outbox.go`, `core/claims/canonical_delta_projector.go`,
`core/claims/projection_rebuild.go`, and durable board tests.

**Existing APIs:** `DurableBoard`, `appendEvent`,
`appendCommittedEvent`, `applyEvent`, `DrainOutbox`, `ClaimsOutbox`,
and existing WAL event payload patterns for claim and testament
lifecycle transitions.

**Acceptance criteria:**

- New WAL event kinds exist for artifact and validation lifecycle
  transitions.
- Replaying from a snapshot after partial WAL progress reconstructs the
  same board state as a fresh board.
- WAL replay deduplicates events by existing event identity rules.
- Outbox records include entity type, entity ID, sequence, mutation
  kind, and created time for artifact and validation events.
- A failed outbox insert records a board notification error and does not
  corrupt committed board state.

**Test cases:**

- Unit happy path: WAL payload marshal/unmarshal preserves all fields.
- Unit negative path: malformed WAL payload produces a replay error with
  sequence and prefix context.
- Unit edge case: replay after a snapshot skips already-snapshotted
  events and applies later artifact transitions only once.
- Integration/mockery: mock outbox insert failure is surfaced as a
  notification error while board state remains committed.
- E2E: generate artifact, attach, validate, close board, reopen, drain
  outbox, and compare deltas to pre-close state.
- Race/deadlock: outbox projection under cancellation exits its tracked
  scope and never holds the board lock while invoking external
  projectors.

### Phase 4: Canonical Deltas and Projection Compatibility

Phase 4 makes the new lifecycles visible to observers while preserving
legacy consumers until the UI and agent consumers are migrated.

#### Item 4.A: Artifact and validation delta vocabulary

**Description:** Extend canonical delta actions to include every
artifact and validation transition and define tolerant/strict validation
behavior.

**Files and integration points:** `core/claims/canonical_delta.go`,
`core/claims/deltas.go`, `docs/CLAIMS_AND_DELTAS.md`, and
`core/claims/bus_integration_test.go`.

**Existing APIs:** `DeltaAction`, `KnownDeltaAction`,
`CanonicalDelta`, `NewCanonicalDelta`,
`ValidateCanonicalDeltaStrict`, `ValidateCanonicalDeltaTolerant`,
`BuildCanonicalDeltaKey`, and legacy `DeltaEnvelope`.

**Acceptance criteria:**

- All artifact actions from section 12.1 and validation actions from
  section 12.2 are constants.
- Strict validation rejects unknown actions; tolerant validation allows
  future actions while preserving envelope checks.
- Delta keys include enough refs to be idempotent per entity transition.
- Context payloads include status, previous status, actor, parent claim,
  parent testament when present, and error category when present.
- Legacy delta projection remains available for existing UI paths until
  Phase 10 removes the fallback.

**Test cases:**

- Unit happy path: each new action validates strictly.
- Unit negative path: unknown strict action fails with a typed error.
- Unit edge case: two deltas for the same artifact transition produce
  the same idempotency key while different transitions do not.
- Integration/mockery: mock bus receives canonical artifact and
  validation deltas in board commit order.
- E2E: a full artifact and validation lifecycle emits the complete delta
  sequence after WAL replay and outbox drain.
- Race/deadlock: concurrent projector delivery cannot reorder deltas
  within the same entity sequence.

#### Item 4.B: Projector and amplifier wiring

**Description:** Teach the board amplifier and canonical outbox
projector to create observer-visible deltas for the new lifecycle events.

**Files and integration points:** `core/claims/board_amplifier.go`,
`core/claims/canonical_delta_projector.go`,
`core/claims/outbox_projectors_test.go`, and `core/claims/deltas.go`.

**Existing APIs:** `BoardAmplifier`, `ClaimsOutboxProjector`,
`ClaimsOutboxRecord`, `outboxRecordsForPostActionLocked`,
`outboxRecordsForSubmitTestamentsLocked`, and projection subscriptions.

**Acceptance criteria:**

- Every committed artifact and validation lifecycle mutation creates a
  recoverable outbox record.
- Amplifier paths and outbox projectors agree on delta action names and
  refs.
- Projection subscribers receive defensive copies of changed entities.
- Duplicate outbox records do not produce duplicate UI or bus messages.
- Error artifact and validation deltas include enough context for later
  remediation and UI diagnostics.

**Test cases:**

- Unit happy path: projector maps each mutation kind to the expected
  canonical delta.
- Unit negative path: unknown mutation kind is recorded as a projection
  error without panicking.
- Unit edge case: legacy `artifact_published` records still project for
  older WALs.
- Integration/mockery: mocked projector returns transient failure, then
  succeeds on retry without duplicate delivered records.
- E2E: durable board outbox drains after restart and the UI bridge sees
  the same final projection.
- Race/deadlock: projector cancellation and retry loops release locks
  and do not leak goroutines.

### Phase 5: Validator Registry and Dispatcher

Phase 5 introduces deterministic validation execution behind explicit
interfaces and mocks. Production rollout remains opt-in.

#### Item 5.A: Validator registration APIs

**Description:** Implement the typed validator registry that maps
`ValidatorID` to a handler, input data type, output data type, and
execution metadata.

**Files and integration points:** New `core/claims/validator_registry.go`,
new `core/claims/validator_registry_test.go`,
`core/claims/type_registry.go`, `docs/CLAIMS_AND_INFRASTRUCTURE.md`,
and package mocks.

**Existing APIs:** `Validation.ValidatorID`, `Artifact.DataType`,
`Artifact.ResultDataType`, type registry from Phase 2, and existing
validation declarations in claims.

**Acceptance criteria:**

- `RegisterValidator[T, R]` registers a typed handler and records input
  and output data types.
- Duplicate validator IDs fail deterministically.
- Validator IDs are stable strings suitable for WAL and docs.
- Registration does not implicitly allow unbounded execution; timeout
  and concurrency policy are explicit metadata.
- Registry lookup is read-safe under concurrent dispatch.

**Test cases:**

- Unit happy path: register, look up, and invoke a synthetic validator.
- Unit negative path: duplicate ID, unknown type, and nil handler fail.
- Unit edge case: registering a validator with the same type pair but a
  different ID succeeds when policy allows it.
- Integration/mockery: mock registry verifies dispatcher performs one
  lookup per validation.
- E2E: a registered validator is resolved through a real board claim and
  produces a result artifact.
- Race/deadlock: concurrent registration during test initialization and
  concurrent lookup during dispatch do not corrupt registry state.

#### Item 5.B: Validator dispatcher

**Description:** Implement the deterministic execution path that reads
artifact data, invokes a registered validator, writes result artifacts,
and commits validation lifecycle transitions.

**Files and integration points:** New `core/claims/validator_dispatcher.go`,
`core/claims/artifact_data.go`, `core/claims/board_lifecycle.go`,
`core/claims/expected_tool_execution.go`, and generated mocks.

**Existing APIs:** Type registry helpers, validation lifecycle board
  APIs, `ExpectedToolExecutor`, `ExpectedToolPolicy`, and
  `ValidationExpectedToolExecutionResult`.

**Acceptance criteria:**

- Dispatcher verifies artifact name, data type, validator ID, required
  status, timeout, and board parentage before invoking a handler.
- Handler panic is recovered into a validation error and an error
  artifact.
- Handler timeout cancels the handler context and records an errored
  validation state.
- Successful handler output is stored as a result artifact with content
  hash and data type.
- Dispatcher never holds the board lock while executing validator code.

**Test cases:**

- Unit happy path: deterministic handler returns a result artifact and
  `validation.validated`.
- Unit negative path: missing artifact, type mismatch, unknown validator,
  panic, and timeout each produce the correct terminal state.
- Unit edge case: optional validation failure records
  `_not_required` without blocking siblings.
- Integration/mockery: mocked board, registry, clock, and handler verify
  call order and no board lock is held during handler execution.
- E2E: real board plus mock handler validates an artifact, writes a WAL
  record, and replays the result artifact.
- Race/deadlock: many validations dispatch concurrently under a bounded
  scope; cancellation drains all workers and leaves no blocked sends.

#### Item 5.C: Determinism and side-effect policy

**Description:** Make deterministic validators mechanically safe:
bounded input, bounded output, explicit side-effect declarations, and
observable failure modes.

**Files and integration points:** `core/claims/validator_registry.go`,
`core/claims/validator_dispatcher.go`,
`docs/CLAIMS_AND_INFRASTRUCTURE.md`, and test fixtures.

**Existing APIs:** `ExpectedToolPolicy`, existing permission and tool
  approval concepts, validator metadata, and artifact content hashes.

**Acceptance criteria:**

- Validators declare whether they are pure, workspace-reading, or
  tool-executing.
- Tool-executing validators must pass through existing policy and
  approval surfaces rather than invoking tools directly.
- Dispatcher enforces maximum input and output sizes from configuration.
- Dispatcher records redacted arguments and outputs where policy
  requires it.
- Policy failures are represented as validation errors and artifacts,
  not dropped logs.

**Test cases:**

- Unit happy path: pure validator executes without policy calls.
- Unit negative path: tool-executing validator without approval is
  denied and recorded.
- Unit edge case: oversized output is truncated or rejected according to
  policy and includes a diagnostic artifact.
- Integration/mockery: mocked policy and redactor are called for
  tool-executing validators and not called for pure validators.
- E2E: expected-tool validation migrates through the dispatcher while
  preserving remediation behavior.
- Race/deadlock: policy cancellation while handler is waiting exits all
  scopes and records an interrupted validation.

### Phase 6: Artifact and Claim Orchestrators

Phase 6 wires deterministic execution into bounded orchestrators and
builds claimant-issued result testaments.

#### Item 6.A: Per-artifact orchestrator

**Description:** Dispatch all validations for one artifact, short-circuit
required failures, collect result artifacts, and commit the artifact
terminal state.

**Files and integration points:** New `core/claims/artifact_orchestrator.go`,
new `core/claims/orchestrator_interfaces.go`,
`core/claims/board_lifecycle.go`, `core/claims/validator_dispatcher.go`,
and mocks.

**Existing APIs:** `Validation.Required`, validation lifecycle board
  APIs, artifact lifecycle board APIs, tracked board/projector scopes,
  and projection helpers.

**Acceptance criteria:**

- All validations for an artifact are dispatched within a bounded
  tracked scope.
- Required failure stops dispatch of not-yet-started sibling
  validations and commits `artifact.validation_failed`.
- Already-started validations are allowed to finish or observe context
  cancellation according to dispatcher policy.
- Optional failures are recorded but do not prevent
  `artifact.validated`.
- Result artifacts are returned to the per-claim orchestrator without
  becoming local truth.

**Test cases:**

- Unit happy path: all validations succeed and artifact becomes
  validated.
- Unit negative path: one required failure prevents later sibling
  dispatch and marks artifact failed.
- Unit edge case: all validations optional and all fail still produces a
  terminal artifact state according to the documented optional semantics.
- Integration/mockery: mock dispatcher verifies bounded parallel fan-out
  and short-circuit behavior.
- E2E: real board with mocked validators validates a multi-validation
  artifact and replays the same terminal status.
- Race/deadlock: simultaneous cancellation, required failure, and
  dispatcher completion cannot deadlock or send on closed channels.

#### Item 6.B: Per-claim orchestrator

**Description:** Run artifact orchestrators for every artifact attached
to a testament, aggregate outcomes into testament and claim lifecycle
transitions, and produce claimant result testament input.

**Files and integration points:** New `core/claims/claim_orchestrator.go`,
`core/claims/board_lifecycle.go`, `core/claims/projection_rebuild.go`,
and propagation tests.

**Existing APIs:** `Testament.Artifacts`, `Claim.Validations`,
`CompleteTestamentValidation`, `BeginTestamentValidation`,
claim lifecycle transition helpers, and relations on claims/testaments.

**Acceptance criteria:**

- The orchestrator matches validations to artifacts by
  `TargetArtifactName`.
- Missing artifacts produce validation incomplete, not validation
  failed.
- All artifacts validated propagates to testament validated and claim
  satisfied.
- Any required artifact failure propagates according to section 11.
- Result artifacts are grouped by original claim ID.

**Test cases:**

- Unit happy path: two artifacts validate and satisfy the claim.
- Unit negative path: missing target artifact produces testament and
  claim validation incomplete.
- Unit edge case: one artifact has no required validations and still
  participates in aggregate state consistently.
- Integration/mockery: mocked per-artifact orchestrators return mixed
  outcomes and the claim orchestrator commits the expected aggregate.
- E2E: real board, mocked validators, and durable replay verify
  aggregate claim state.
- Race/deadlock: parallel artifact orchestrators complete under
  cancellation without blocking result aggregation.

#### Item 6.C: Result testament builder

**Description:** Build the claimant-issued result testament that bundles
validation result artifacts and closes the validation cycle for the
original claim.

**Files and integration points:** New `core/claims/result_testament.go`,
`core/claims/board_lifecycle.go`, `core/claims/board_durable.go`, and
result testament tests.

**Existing APIs:** `GenerateTestamentAction`,
`PostGeneratedTestament`, `Testament.Relations`, `Artifact.Relations`,
`Relation`, and result artifacts from the dispatcher.

**Acceptance criteria:**

- One result testament is generated per original claim validation pass.
- Result testament relations point to the original claim and source
  testament.
- Result artifacts terminate at `artifact.generated`; the result
  testament terminates at `testament.posted` because there is no
  downstream claimant.
- Empty result bundles are explicitly represented or explicitly skipped
  by documented policy.
- WAL replay preserves the result testament and never re-runs
  validators.

**Test cases:**

- Unit happy path: multiple result artifacts bundle into one testament
  with correct relations.
- Unit negative path: result artifact with mismatched claim ID is
  rejected.
- Unit edge case: no result artifacts follows the documented empty-bundle
  policy.
- Integration/mockery: mock board verifies generation and post calls
  happen once and in order.
- E2E: validation results are bundled, posted, replayed, and visible in
  projection without further lifecycle advancement.
- Race/deadlock: simultaneous result artifact completion cannot produce
  duplicate result testaments.

### Phase 7: Streaming Artifact Submission and Receipt

Phase 7 turns the streaming model into runtime behavior. Testifiers
publish artifacts as they work; the closing testament attaches them.

#### Item 7.A: Testifier artifact generation API

**Description:** Provide a runtime API for participants to publish
artifacts before testament generation, with durable board commits and
claimant visibility.

**Files and integration points:** `core/claims/board_lifecycle.go`,
`core/claims/accumulator.go`, `agents/*` testament/accumulator call
sites, `core/claims/carry_forward.go`, and expected-tool/presentation
artifact producers.

**Existing APIs:** `SubmitTestaments`, `GenerateTestamentAction`,
`Accumulator`, `Artifact`, and current artifact-stamping logic in
`stampTestamentLocked`.

**Acceptance criteria:**

- Artifacts can be generated with `TestamentID` empty and `ClaimID`
  populated.
- Generated artifacts are visible to projections and delta subscribers.
- Artifact IDs, names, data types, content hashes, and sizes are stamped
  before commit.
- Generation failure creates a durable failed artifact when possible or
  a parent lifecycle failure when not.
- No code path relies on atomic artifact-and-testament submission for
  new artifacts.

**Test cases:**

- Unit happy path: generated artifact appears in projection without a
  testament ID.
- Unit negative path: generation with empty claim ID, empty artifact
  name, invalid data type, or hash mismatch fails.
- Unit edge case: instantaneous work still commits artifact generation
  before testament generation.
- Integration/mockery: mock participant publishes several artifacts and
  receives generated IDs for later testament attachment.
- E2E: an agent-like simulated run streams artifacts, then posts a
  testament that attaches them.
- Race/deadlock: concurrent artifact generation for the same claim
  produces unique names or deterministic duplicate-name rejection.

#### Item 7.B: Atomic attachment on testament generation

**Description:** When the testifier generates the closing testament,
attach all referenced generated artifacts in the same board transaction
as `testament.generated`.

**Files and integration points:** `core/claims/board_lifecycle.go`,
`core/claims/board.go`, `core/claims/board_durable.go`, and
`core/claims/accumulator.go`.

**Existing APIs:** `GenerateTestamentAction`,
`stampGeneratedTestamentLocked`, `stampTestamentLocked`,
`outboxRecordsForSubmitTestamentsLocked`, and testament lifecycle
transitions.

**Acceptance criteria:**

- Testament generation validates every referenced artifact belongs to
  the same claim and participant scope.
- Attachment populates `TestamentID` and appends `artifact.attached`
  history in the same durable event batch.
- Partial attachment cannot commit if testament generation fails.
- Duplicate artifact references are rejected or de-duplicated by
  documented policy.
- Legacy testaments with embedded artifacts continue to work through a
  compatibility path.

**Test cases:**

- Unit happy path: three generated artifacts attach atomically to one
  testament.
- Unit negative path: one artifact belongs to another claim and the
  entire generation fails without partial attachment.
- Unit edge case: an artifact skipped `received` and transitions
  generated to attached directly.
- Integration/mockery: mock WAL verifies one durable batch contains
  testament generated and all artifact attached transitions.
- E2E: crash/reopen after attachment replays testament and artifacts in
  a consistent state.
- Race/deadlock: concurrent claimant receipt and testifier attachment
  produce a legal sequence without lock inversion.

#### Item 7.C: Claimant receipt and structural failure

**Description:** Let the claimant observe generated artifacts during the
unattached window, acknowledge receipt, or reject structurally malformed
artifacts before validation begins.

**Files and integration points:** `core/claims/board_lifecycle.go`,
new claimant receipt runtime in the relevant agent/orchestrator package,
`core/claims/canonical_delta_projector.go`, and UI bridge tests.

**Existing APIs:** Projection subscription, artifact lifecycle board
  APIs, type registry, content hash helpers, and presentation validation
  helpers.

**Acceptance criteria:**

- Receipt checks presence, claim ID, artifact name, data type,
  decodability, content hash, and required metadata.
- Valid artifacts transition to `artifact.received` when observed before
  attachment.
- Invalid artifacts transition to `artifact.receipt_failed` and never
  enter validation.
- Receipt is idempotent and safe after duplicate deltas.
- Receipt skipped because attachment arrived first is a valid path.

**Test cases:**

- Unit happy path: generated artifact receives successfully.
- Unit negative path: malformed data, bad hash, missing claim ID, and
  duplicate artifact name fail receipt structurally.
- Unit edge case: attachment arrives before receipt and validation still
  proceeds.
- Integration/mockery: mock delta subscriber delivers duplicate and
  reordered generated events; receipt commits once.
- E2E: simulated streaming run shows generated, received, attached, and
  validating deltas in order when observation happens.
- Race/deadlock: receipt, attachment, and shutdown cancellation cannot
  leave the claimant receipt loop blocked.

### Phase 8: Agentic Quality-Bar Dispatch

Phase 8 connects deterministic validation to agentic review when a
validation declares a non-empty quality bar.

#### Item 8.A: Quality-bar eligibility and declaration validation

**Description:** Enforce that quality bars are allowed only when the
claimant is an agentic participant capable of evaluating them.

**Files and integration points:** `core/claims/board.go`,
`core/claims/board_lifecycle.go`, agent identity/category code under
`core/agents/identity`, and claim posting tests.

**Existing APIs:** `Claim.AgentID`, `ParticipantRef`, claimant identity
normalization in canonical deltas, validation declarations, and agent
skill registration.

**Acceptance criteria:**

- Non-empty `QualityBar` plus non-agentic claimant is rejected at claim
  or validation declaration time.
- Empty `QualityBar` remains valid for any participant category.
- Agentic claimant eligibility is computed from participant identity,
  not string prefixes.
- Rejections are durable claim post failures where the claim was already
  in a generated lifecycle path.

**Test cases:**

- Unit happy path: agentic claimant with quality bar is accepted.
- Unit negative path: service/system claimant with quality bar is
  rejected.
- Unit edge case: whitespace-only quality bar is treated as empty.
- Integration/mockery: mock identity resolver proves eligibility uses
  identity metadata rather than agent ID text.
- E2E: posting a service claim with a quality bar fails and emits the
  documented delta.

#### Item 8.B: Agent turn injection and verdict capture

**Description:** After deterministic validation succeeds, inject the
artifact, deterministic result, and quality-bar instruction into the
claimant agent's normal turn and capture the verdict through
`evaluate_validation`.

**Files and integration points:** `core/claims/skills.go`,
`agents/*/skills.go`, claimant agent runtime, prompt/context assembly
code, and quality-bar orchestrator tests.

**Existing APIs:** `EvaluateValidationSkill`, validation lifecycle board
  APIs, `ArtifactData`, `Presentation`, and agent tool-loop skill
  registration.

**Acceptance criteria:**

- The validation transitions to `validation.validating_quality_bar`
  only after deterministic success.
- Small artifacts are included directly in agent context; large artifacts
  are represented by retrieval references and explicit instructions.
- The agent verdict must be committed through `evaluate_validation` or a
  dedicated lifecycle wrapper, not through local memory mutation.
- Agent timeout, cancellation, refusal, or malformed verdict becomes a
  validation error artifact and terminal errored state.
- Multiple simultaneous quality-bar validations keep separate context and
  result artifacts.

**Test cases:**

- Unit happy path: synthetic agent verdict marks validation validated.
- Unit negative path: malformed verdict and timeout record errored
  validation states.
- Unit edge case: large artifact takes the retrieval-reference path and
  never injects oversized context.
- Integration/mockery: mocked agent turn receives quality-bar context and
  calls the expected skill exactly once.
- E2E: simulated claimant agent reviews a deterministic result and emits
  a quality-bar failure that propagates.
- Race/deadlock: two quality-bar evaluations in the same agent session
  cannot overwrite each other's pending validation IDs.

### Phase 9: Propagation and Remediation

Phase 9 makes validation outcomes affect testaments, claims, and
follow-up corrective work.

#### Item 9.A: Artifact to testament propagation

**Description:** Aggregate artifact terminal states into the parent
testament lifecycle exactly as defined in section 11.3.

**Files and integration points:** `core/claims/board_lifecycle.go`,
`core/claims/claim_orchestrator.go`, `core/claims/projection_rebuild.go`,
and lifecycle propagation tests.

**Existing APIs:** `CompleteTestamentValidation`,
`TestamentLifecycleStatus`, artifact lifecycle APIs, and projection
helpers.

**Acceptance criteria:**

- All required artifacts validated produces testament validated.
- Any required artifact validation failure produces testament validation
  failed.
- Receipt failure, missing artifact, or missing required validation
  produces testament validation incomplete.
- Validator infrastructure errors produce testament validation errored.
- Propagation is atomic with the terminal artifact transition that
  triggered it when possible.

**Test cases:**

- Unit happy path: all artifacts validated produces
  `testament.validated`.
- Unit negative path: one failed required artifact produces
  `testament.validation_failed`.
- Unit edge case: optional artifact/validation failure does not block
  testament validation.
- Integration/mockery: mock board verifies propagation commits only once
  after duplicate terminal artifact events.
- E2E: durable replay reconstructs testament state from artifact
  transitions.
- Race/deadlock: simultaneous artifact terminal transitions aggregate
  deterministically without double-finalizing the testament.

#### Item 9.B: Testament to claim propagation

**Description:** Propagate testament outcomes into the original claim
lifecycle without duplicating logic already owned by
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`.

**Files and integration points:** `core/claims/board_lifecycle.go`,
`core/claims/board.go`, `core/claims/canonical_delta.go`, and claim
lifecycle tests.

**Existing APIs:** claim lifecycle transition helpers,
`ClaimLifecycleStatus`, `ClaimStatus`, `AllValidationsPassed`, and
existing testament-to-claim validation code.

**Acceptance criteria:**

- `testament.validated` maps to claim satisfied.
- `testament.validation_failed` maps to claim validation failed.
- `testament.validation_incomplete` maps to claim validation incomplete.
- `testament.validation_errored` maps to claim validation errored.
- Coarse `ClaimStatus` remains a compatibility projection and does not
  become the source of truth.

**Test cases:**

- Unit happy path: validated testament satisfies claim.
- Unit negative path: failed testament rejects or marks the claim with
  the documented failure lifecycle.
- Unit edge case: legacy `ValidationStatusPassed` still projects to the
  expected coarse status during migration.
- Integration/mockery: mock delta bus receives claim and testament
  deltas in causal order.
- E2E: multi-artifact validation flow reaches terminal claim state and
  replays to the same projection.
- Race/deadlock: duplicate testament terminal transitions do not double
  append claim history.

#### Item 9.C: Corrective claim generation

**Description:** Generate corrective claims for incomplete, failed, or
errored validation outcomes according to section 10 and the configured
remediation policy.

**Files and integration points:** New `core/claims/remediation.go`,
`core/claims/expected_tool_execution.go`,
`core/claims/carry_forward.go`, agent orchestrator packages, and
remediation tests.

**Existing APIs:** `PostAction`, `RejectClaim`,
`ValidationExpectedToolRemediationPoster`, `Relation`, claim priority,
deadline, tags, and carry-forward testament publication.

**Acceptance criteria:**

- Missing artifacts create corrective claims targeting only the missing
  evidence.
- Validation failures create corrective claims only when policy enables
  retry or repair.
- Corrective claims include `caused_by`, `corrects`, or supersession
  relations to the failed validation/artifact/testament.
- Duplicate failures do not create duplicate corrective claims.
- Corrective claim generation is bounded by policy and queue capacity.

**Test cases:**

- Unit happy path: missing artifact produces one corrective claim with
  exact target and relation metadata.
- Unit negative path: disabled remediation policy produces no corrective
  claim and records the skipped reason.
- Unit edge case: repeated failure deltas for the same validation are
  idempotent.
- Integration/mockery: mock remediation poster verifies expected claim
  structure and post call.
- E2E: validation incomplete leads to corrective claim, corrected
  artifact, and final satisfied claim.
- Race/deadlock: concurrent remediation triggers for the same root cause
  coalesce without blocking the remediation worker.

### Phase 10: UI Bridge, Chat Rendering, and Observability

Phase 10 makes the new lifecycle visible and understandable without
polluting sessions, duplicating rows, or hiding artifacts.

#### Item 10.A: Bridge consumption of artifact and validation deltas

**Description:** Update the UI bridge to consume canonical artifact and
validation deltas and convert them into stable UI messages.

**Files and integration points:** `ui/bridge/claims.go`,
`ui/bridge/claims_integration_test.go`, `ui/msg`, and bridge observability
metrics.

**Existing APIs:** `ClaimPresentationMsg`, `ClaimArtifactAddedMsg`,
`ClaimArtifactCompletedMsg`, `TestamentContextMsg`,
`PresentationMetricSink`, and existing canonical claims bridge code.

**Acceptance criteria:**

- Generated, received, attached, validating, validated, and failed
  artifact states render as updates to the same artifact row.
- Validation lifecycle states render under the correct artifact and
  claim, not as top-level agents.
- Result artifacts with presentation metadata surface through the
  existing presentation message path.
- Duplicate or replayed deltas update existing rows idempotently.
- Bridge state is scoped by current Sylk session/run and cannot display
  stale prior-session rows as active output.

**Test cases:**

- Unit happy path: each artifact and validation delta maps to the
  expected message.
- Unit negative path: malformed refs are dropped with a metric and do
  not panic.
- Unit edge case: replayed deltas for a previous session are ignored or
  stored only in historical views according to session scope.
- Integration/mockery: mock Tea program receives stable message
  sequences for streaming artifacts.
- E2E: simulated claim run shows plan/artifact markdown in the active
  session and no stale prior-session output.
- Race/deadlock: bridge delivery under rapid delta bursts does not block
  the TUI update loop or leak goroutines.

#### Item 10.B: Chat and approval rendering

**Description:** Render artifacts, validation rows, markdown plans,
approval popups, and diagnostics in a compact, scrollable, session-local
way.

**Files and integration points:** `ui/chat/claim_render.go`,
`ui/chat/model.go`, `ui/chat/renderer.go`, `ui/modal`, and relevant TUI
tests.

**Existing APIs:** current chat message model, modal content interfaces,
claim presentation rendering, and existing markdown artifact rendering.

**Acceptance criteria:**

- Markdown plan artifacts render in markdown form when present and never
  fall back to a truncation diagnostic unless the artifact itself is
  unavailable.
- Approval/modify/reject popups keep the specified maximum inner height
  and scroll internally when content exceeds that height.
- Consult/challenge/validation helper agents render nested under their
  owning top-level agent or claim and close when their operation
  completes.
- New runs start with an empty active-session transcript and may expose
  prior-session data only through explicit history views.
- Rendering never blocks on artifact retrieval; unavailable large
  artifacts show a bounded loading/error state.

**Test cases:**

- Unit happy path: markdown artifact renders headings, lists, and code
  blocks in the expected model nodes.
- Unit negative path: truncated artifact projection triggers retrieval
  or bounded diagnostic, not raw error text in the main transcript.
- Unit edge case: approval content longer than the popup height scrolls
  without resizing the popup.
- Integration/mockery: mock artifact loader returns delayed, missing,
  and oversized artifacts while the chat model remains responsive.
- E2E: scripted TUI run opens a plan, shows markdown, opens approval,
  scrolls approval content, then starts a new session with no stale
  transcript pollution.
- Race/deadlock: rapid artifact updates while modal is open do not cause
  overlapping text, blocked input, or stuck helper-agent rows.

#### Item 10.C: Observability and diagnostics

**Description:** Add metrics and bounded diagnostics for artifact and
validation visibility failures so regressions are detectable without
spamming the user transcript.

**Files and integration points:** `ui/bridge/presentation_observability.go`,
`core/claims/board.go`, `core/claims/board_durable.go`, and logs/metrics
surfaces already used by claims.

**Existing APIs:** `PresentationMetricSink`, board notification errors,
projector errors, and canonical delta context.

**Acceptance criteria:**

- Metrics distinguish dropped delta, malformed delta, missing artifact,
  truncated projection, retrieval failure, and stale-session suppression.
- Diagnostics are bounded and de-duplicated by entity and session.
- User-visible errors include actionable entity IDs only when they help
  locate missing data.
- Metrics survive WAL replay and outbox retry without double-counting
  delivered success.

**Test cases:**

- Unit happy path: each diagnostic category increments the expected
  metric.
- Unit negative path: repeated identical diagnostics de-duplicate.
- Unit edge case: stale-session suppression records a metric but does
  not render transcript output.
- Integration/mockery: mock metric sink observes bridge and renderer
  failure categories.
- E2E: simulated missing artifact produces a bounded diagnostic and no
  transcript flood.
- Race/deadlock: metrics recording from bridge and renderer goroutines
  does not race or block UI delivery.

### Phase 11: Subsystem Migration, Cleanup, and Contract Enforcement

Phase 11 migrates existing subsystems onto the new lifecycle and removes
legacy paths that can bypass durable truth.

#### Item 11.A: Migrate existing artifact producers

**Description:** Convert plan markdown, presentation evidence,
expected-tool execution, carry-forward publication, and knowledge
readiness artifacts to the typed streaming lifecycle.

**Files and integration points:** `core/claims/presentation_evidence.go`,
`core/claims/expected_tool_execution.go`, `core/claims/carry_forward.go`,
`core/claims/knowledge_readiness.go`, `agents/architect`, `agents/librarian`,
and UI presentation paths.

**Existing APIs:** `ValidateArtifactEvidence`,
`ValidationExpectedToolExecutionResult`, `CarryForwardPublisher`,
`GenerateTestamentAction`, `ArtifactKindPlanMarkdown`, and
`ArtifactKindExpectedToolOutput`.

**Acceptance criteria:**

- Each migrated subsystem publishes typed generated artifacts before
  testament generation.
- Each subsystem attaches artifacts through the Phase 7 testament
  generation path.
- Legacy reference-only paths remain read-compatible but are not used
  by new production writes.
- Carry-forward testaments publish compact indexes plus retrievable
  artifacts, allowing recalling agents to ingest useful information
  without large inline projections.
- Expected-tool validation outcomes are lifecycle transitions and result
  artifacts, not standalone Go return values.

**Test cases:**

- Unit happy path: each subsystem fixture produces the expected typed
  artifact data.
- Unit negative path: malformed subsystem artifact data fails receipt or
  validation with the correct category.
- Unit edge case: carry-forward index references an artifact that is
  unavailable; recall reports a bounded missing-artifact diagnostic.
- Integration/mockery: mock artifact retrieval and carry-forward
  publisher verify indexes and artifacts are published exactly once.
- E2E: architect plan run publishes markdown artifact, visible plan,
  carry-forward index, recallable artifact, validation result, and final
  claim state.
- Race/deadlock: simultaneous carry-forward publication and recall does
  not duplicate work or block agent progress.

#### Item 11.B: Remove or seal legacy bypasses

**Description:** Delete, seal, or adapt any production code path that
returns validation truth, artifact visibility, or presentation output
without committing the corresponding board state.

**Files and integration points:** `core/claims/expected_tool_execution.go`,
`core/claims/presentation_evidence.go`, `core/claims/skills.go`,
`ui/bridge/claims.go`, `agents/*`, and static/contract tests.

**Existing APIs:** `EvaluateValidation`, `EvaluateValidationSkill`,
presentation evidence helpers, bridge message paths, and old artifact
publication helper code.

**Acceptance criteria:**

- No production validator returns a verdict without a board lifecycle
  transition.
- No production artifact renderer depends on inline projection when a
  retrievable artifact reference exists.
- No production helper mutates `Artifact.Status`,
  `Validation.Status`, or histories directly.
- Compatibility adapters are explicitly named and tested as legacy-only.
- Static checks fail if new direct mutations or bypasses are introduced.

**Test cases:**

- Unit/static happy path: allowed board APIs pass direct-mutation checks.
- Unit/static negative path: a fixture containing direct status mutation
  fails the checker.
- Unit edge case: test-only fixtures can construct states only through
  approved helper packages.
- Integration/mockery: mocked legacy adapter proves it still emits board
  transitions before returning.
- E2E: all migrated flows pass without invoking legacy bypass paths.
- Race/deadlock: cleanup does not remove cancellation paths needed by
  existing orchestrator scopes.

#### Item 11.C: Full contract, replay, and stress suite

**Description:** Lock in the full architecture with contract tests that
exercise happy paths, failures, edge cases, replay, races, deadlocks, and
simulated user-visible sessions.

**Files and integration points:** `core/claims/*_test.go`,
`ui/bridge/*_test.go`, `ui/chat/*_test.go`, package mocks generated by
mockery, and CI test commands.

**Existing APIs:** All APIs named in the prior phases plus `go test
./...`, race-test package subsets, durable board replay, outbox drain,
and UI bridge rendering.

**Acceptance criteria:**

- `go test ./...` passes.
- Race-targeted claims, bridge, and chat packages pass under
  `go test -race`.
- Contract tests prove every artifact and validation status is reachable
  only through documented transitions.
- E2E tests prove a new Sylk run is session-local and not polluted by
  prior sessions.
- Stress tests prove bounded queues, bounded goroutines, bounded
  diagnostics, bounded artifact projections, and deterministic
  idempotency.

**Test cases:**

- Unit happy path: full transition tables for artifacts and validations.
- Unit negative path: invalid status, invalid parentage, unknown
  validator, unknown codec, bad hash, bad receipt, and malformed delta.
- Unit edge cases: legacy artifact replay, duplicate deltas, direct
  generated-to-attached path, optional validation failure, large artifact
  retrieval, empty result bundle, and interrupted agent quality-bar turn.
- Integration/mockery: mock validators, agents, policy, redactor, bus,
  outbox, metric sink, and artifact loader cover failure and retry
  behavior.
- E2E: simulated architect plan run, expected-tool validation run,
  carry-forward recall run, remediation run, UI plan review run, and
  restart/replay run.
- Race/deadlock: run claims orchestrator, durable outbox projection,
  bridge delivery, chat rendering, remediation, and carry-forward recall
  under the race detector with cancellation and duplicate-delivery
  stress.

## 18. Migration Notes

The migration is incremental and shippable per phase. Coexistence
with legacy artifact/validation paths during migration:

### 18.1 Legacy Artifact Compatibility

Existing artifacts without `Status`, `ArtifactName`, `DataType`, or
typed `Data` are treated as legacy. Legacy artifacts:

- Default to `Status: ArtifactStatusAttached` (treated as already
  past the receipt and attachment phases).
- Have empty `ArtifactName`; validations targeting them must use
  legacy binding (typically by `Kind` match) until migrated.
- Do not have typed data; validators that need typed access reject
  them with `ValidationError{Category: artifact_type_mismatch}`.

Legacy artifacts continue to function for read operations. New
artifacts produced by upgraded subsystems use the full typed shape.

### 18.2 Legacy Validation Compatibility

Existing validations without `TargetArtifactName`, `ValidatorID`, or
typed handler:

- Continue to work as agentic-only validations (no deterministic
  phase, only quality-bar phase).
- The quality-bar phase is gated on agentic claimant per §7.6.

### 18.3 Subsystem Migration Order

Recommended migration order:

1. Phase 0: reconcile documents, mock boundaries, fixtures, and
   invariants.
2. Phase 1-2: land foundation types, lifecycle helpers, typed codecs,
   artifact-data helpers, and built-in data catalogs with no production
   callers.
3. Phase 3-4: make the board, WAL, outbox, canonical deltas, amplifier,
   and projector understand artifact and validation lifecycle events.
4. Phase 5: add deterministic validator registration, dispatch,
   timeout, panic, policy, and result-artifact handling behind explicit
   interfaces and mockery-backed tests.
5. Phase 6: add per-artifact and per-claim orchestrators plus result
   testament building, still behind synthetic or opt-in callers.
6. Phase 7: enable streaming artifact generation, claimant receipt, and
   atomic testament attachment.
7. Phase 8: enable agentic quality-bar validation for eligible
   claimants only.
8. Phase 9: enable lifecycle propagation and corrective claim
   remediation.
9. Phase 10: migrate UI bridge, chat rendering, approval rendering, and
   visibility observability.
10. Phase 11: migrate existing artifact producers, remove legacy
    bypasses, and lock the system with full contract, replay, stress,
    race, and E2E tests.

Subsystem-level migration of artifact and validation usage follows the
per-system catalog in `CLAIMS_AND_INFRASTRUCTURE.md §14`. Each system's
conversion to claims-driven includes registering its artifact types,
its programmatic validators, and (where applicable) its quality bar
agentic validations.

### 18.4 Replay Compatibility

Replayed WALs from before this migration contain artifacts without
status fields. The replay reducer treats them as
`ArtifactStatusAttached` and does not attempt to derive earlier states.

Validations in the legacy WAL similarly load with degenerate lifecycle
state (terminal at validated or failed based on legacy status).

### 18.5 No Hybrid Authority

At no point during migration does workflow truth live in two places.
If a validation has a typed handler registered, that handler's
result is the truth. If not, agentic evaluation is the truth. There is
no parallel "shadow validation" path.

## 19. Final Architecture Statement

Artifacts are typed evidence with a first-class lifecycle. Validations
are 1-1 typed assertions against artifacts, expressed as registered Go
handler functions returning typed result artifacts. The lifecycle is
streaming-only: artifacts stream onto the board as the testifier
produces them, and the testament is generated as the closing signal of
the work cycle.

Validation has two phases: a deterministic phase that runs a typed
handler function, and an optional agentic quality-bar phase that runs
in the claimant agent's existing tool loop. The deterministic phase is
mechanical and replayable; the quality-bar phase is agentic and
non-deterministic. Non-agentic claimants cannot have validations with
quality bars by registration-time enforcement.

Validator result artifacts are bundled into a per-claim result
testament issued by the claimant. The result testament and its
artifacts have an asymmetric terminal lifecycle that terminates at
`testament.posted` and `artifact.generated` respectively, because no
consuming participant exists.

Artifact and validation transitions propagate atomically into
testament and claim transitions per
`CLAIMS_AND_TESTAMENTS_LIFECYCLE.md`. The board stores transitions and
emits deltas; the claimant runtime orchestrates the full lifecycle.
Errors are first-class artifacts at every boundary. Remediation is
expressed as corrective claims with explicit `caused_by` relations,
forming a queryable lineage that the Memory Forest can harvest.

The wire format is uniform across all participant categories.
Validators registered by agents, services, system participants, and
infrastructure components all produce identical artifacts, identical
validation records, identical deltas, and identical lifecycle
propagation. The claims plane remains the universal coordination
primitive for the entirety of Sylk; this document widens it to give
artifacts and validations the typed, lifecycle-driven shape that the
existing claims documents assumed but did not formalize.
