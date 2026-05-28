# Claims and Testaments Lifecycle

This document defines the canonical lifecycle for claims, testaments,
validations, and their deltas.

The purpose is to make claims and testaments the only workflow state
machine. The board owns durable truth. The Guide event bus transports
committed lifecycle deltas. Agents, UI, validators, and continuations react
to those deltas. There is no separate routing state machine, no inferred
consult completion path, and no hidden success or failure channel.

## 1. Core Principle

Every lifecycle fact that matters must be posted to the board.

That includes early facts such as "a claim was generated" and "a testament
was generated." These are not local-only process events. They are durable
board facts with explicit lifecycle statuses.

The critical distinction is:

- **Generated** means the object exists durably on the board but is not yet
  actionable by its downstream receiver.
- **Posted** means the object has been activated for downstream workflow.

For claims:

- `claim.generated` means the claim and its validations were durably
  generated on the board.
- `claim.posted` means the generated claim was activated as directed work.

For testaments:

- `testament.generated` means the testament and its artifact references were
  durably generated on the board.
- `testament.posted` means the testament was activated as the response to a
  claim and is now available to the claim source/evaluator.

This makes generation, activation, receipt, progress, validation, success,
and failure replayable and auditable.

## 2. Vocabulary

### Action

An Action is the top-level unit agents process. A claim action groups one or
more claims. A testament action groups one or more testaments responding to
claims.

Routing, planning, consultation, challenge, validation, remediation,
archival, and handoff are all Actions. They differ by action type and
relations, not by special-purpose transport mechanisms.

### Claim

A Claim is a durable assertion or directed work item. It carries title,
description, relations, scope, validations, expected tool calls, and
lifecycle status.

Claims are not merely messages. They are constraints and obligations.

### Testament

A Testament is a durable response to a claim. It carries context, verdict,
confidence, artifacts, relations, and lifecycle status.

Testaments are not only final answers. A system may use testament records to
represent generated responses before they are posted, partial work,
completion, refusal, impossibility, interruption, failure, or validation
evidence.

### Artifact

An Artifact is evidence attached to a testament. Artifacts include content,
references, logs, diffs, test output, tool output, diagnostics, and errors.

Errors are artifacts. If a tool call, claim generation, testament generation,
posting operation, receipt acknowledgment, or validation step fails, that
failure must be represented as an artifact wherever a parent durable object
exists to receive it.

### Validation

A Validation is a verification requirement on a claim. It defines what the
source/evaluator must check and what quality bar must be met.

Receipt validations are mechanical when they are intentionally scoped to a
specific receipt fact. Work validations are not satisfied by mere receipt.

### Delta

A Delta is an immutable event emitted after a board mutation commits.

Deltas are not commands. They are committed lifecycle facts. Receivers act
on deltas because the board state changed, not because the bus owns
workflow truth.

## 3. Posting Semantics

The word "posted" has historically been overloaded. This lifecycle uses
two separate concepts.

### Durable Generation

Durable generation creates a board record.

Examples:

- A planning agent creates a claim with validations.
- A worker agent creates a testament with artifacts.
- A validator creates validation evidence.

After durable generation, the object has an ID, relations, status, and
sequence. It can be replayed and inspected. It may not yet be actionable.

### Workflow Posting

Workflow posting activates a generated object.

Examples:

- A generated claim is posted to a target agent as work.
- A generated testament is posted to the claim source as a response.

Posting controls delivery and downstream work. A generated object can fail
to post without disappearing, because its failed posting status is itself a
durable lifecycle fact.

## 4. Claim Lifecycle

The canonical claim lifecycle is:

```text
claim.generated
  -> claim.posted
  -> claim.received
  -> claim.progressed
  -> claim.testament_generated
  -> claim.testament_acknowledged
  -> claim.validating
  -> claim.satisfied

Alternative validation outcomes:

claim.validating
  -> claim.validation_incomplete
claim.validating
  -> claim.validation_failed
claim.validating
  -> claim.validation_errored
```

Error states may occur at the corresponding lifecycle boundary:

```text
claim.generation_failed
claim.post_failed
claim.receipt_failed
claim.progress_failed
claim.testament_generation_failed
claim.testament_acknowledgement_failed
claim.validation_errored
```

### claim.generated

The claim has been generated, including validations, relations, scope,
expected tool calls, and metadata. It exists durably on the board but has
not yet been activated for target-agent work.

The board emits `claim.generated` after the generated claim record commits.

This state is useful for:

- plans that generate claims before user approval,
- Guide classification claims before routed work is activated,
- claim generation retries,
- UI display of draft or pending claim sets,
- durable failure reporting if activation later fails.

Required durable data:

- claim ID,
- claim action ID,
- source/issuer relation,
- target/subject relation when known,
- title and description,
- validations,
- status `generated`,
- status history entry,
- generation artifacts when applicable.

### claim.generation_failed

Claim generation failed before a usable claim could be activated.

If enough claim data exists to create a durable failed claim record, the
system posts that claim with status `generation_failed`. If no claim record
can be created, the failure must be represented as an error artifact on the
parent action or parent claim that requested generation.

Examples:

- Architect could not decompose a plan into claims.
- Guide could not classify the target well enough to create a routed work
  claim.
- Validation generation failed.

The failure must include error artifacts. It must not be represented only
by a returned Go error, log line, or UI spinner.

### claim.posted

The generated claim has been activated for workflow. It is now eligible for
delivery to the intended recipient, evaluator, reviewer, remediator, or
observer.

The board emits `claim.posted` after the status transition commits.

Posting does not mean the target agent has received the claim. It means the
claim is now a deliverable board fact.

### claim.post_failed

The claim existed durably but activation failed.

Examples:

- target identity could not be resolved,
- delivery policy denied the route,
- required relations were invalid,
- self-targeting was rejected for an action that does not allow
  self-transfer,
- expected tool call policy rejected the claim before activation.

The generated claim remains on the board with status `post_failed`. The
failure is attached as error artifacts on an associated failure testament or
status payload, depending on implementation.

### claim.received

The target agent received the posted claim and acknowledged receipt.

This is not emitted by the sender merely because a delta was published.
This is a durable acknowledgment generated by the receiving agent or its
claims intake after it accepts responsibility for processing the claim.

The target agent may acknowledge receipt by committing a lifecycle mutation
on the claim. The board then emits `claim.received`.

Receipt does not mean work is complete. Receipt does not satisfy work
validations. Receipt only proves the target has seen and accepted the
directed work for processing.

### claim.receipt_failed

An error occurred while the target agent received the claim or generated
receipt acknowledgment.

Examples:

- the target agent crashed while resolving the delta,
- the claim intake rejected a malformed but already posted claim,
- the identity registry failed during canonical identity verification,
- the acknowledgment mutation could not be committed.

If the target cannot acknowledge, the failure should be recorded by the
runtime or supervising agent as an error artifact linked to the claim.

### claim.progressed

The target agent has generated, is generating, or is actively working on
output for the claim.

Progress is non-terminal. It may include states such as:

- `work_started`,
- `running_expected_tool`,
- `awaiting_dependency`,
- `awaiting_peer_response`,
- `artifacts_generated`,
- `partial_output_available`,
- `blocked`,
- `interrupted`.

Progress must never close a UI row, satisfy a claim, or resume a
completion continuation. It may update UI status, telemetry, or scheduling.

### claim.progress_failed

The target agent encountered an error during work and did not generate or
post the corresponding testament.

Some artifacts may have been generated. Those artifacts must be preserved
where possible. The failure should be represented by error artifacts linked
to the claim through a failure testament or durable progress failure record.

Examples:

- expected tool execution failed before a testament was generated,
- the agent was interrupted before creating the testament,
- a local runtime error prevented response assembly,
- a dependency timed out and no response testament was produced.

### claim.testament_generated

The target agent completed enough work to generate the corresponding
testament and commit that generated testament record to the board.

This state means a testament exists durably. It does not necessarily mean
the source/evaluator has received it yet.

The claim-level event is emitted because the claim graph now has a
generated response. The testament itself also has its own lifecycle event:
`testament.generated`.

### claim.testament_generation_failed

An error occurred during the target agent's generation or posting of the
corresponding testament.

This includes:

- failure to assemble the testament,
- failure to attach required artifacts,
- failure to serialize artifact metadata,
- failure to commit the generated testament,
- failure to activate/post the testament after generation.

The failure must be represented with error artifacts wherever the claim,
generated testament, or parent action is available.

### claim.testament_acknowledged

The claim source/evaluator received the corresponding testament and
acknowledged receipt.

This should be emitted after the source/evaluator processes the testament
delta and commits an acknowledgment mutation.

Acknowledgment does not mean validation passed. It means the evaluator has
the response and can proceed to validation.

### claim.testament_acknowledgement_failed

An error occurred during source/evaluator ingestion or receipt
acknowledgment of the testament and/or its artifacts.

Examples:

- artifact headers could not be resolved,
- source agent identity could not be verified,
- the source agent crashed during acknowledgment,
- the acknowledgment mutation failed.

This is an infrastructure or receipt-path failure, not a verdict on the
quality of the testament.

### claim.validating

The source/evaluator has received the testament and is validating its
artifacts against the claim's validations.

The board emits `claim.validating` after validation work is committed as
started. This lets UI and continuations distinguish "response received" from
"response being checked."

Validation may be mechanical for receipt-only validations or agentic for
test, inspection, integration, contract, design, regression, or other
validation types.

### claim.satisfied

The source/evaluator completed all artifact validations for the testament,
and the artifacts fully satisfied the claim.

This is the successful terminal state for the claim. It corresponds to all
required validations passing.

This state replaces ambiguous terms like "complete" for claim lifecycle.
The claim is not satisfied merely because a target agent responded. It is
satisfied because the source/evaluator accepted the evidence against the
validations.

### claim.validation_incomplete

The source/evaluator completed validation and determined that one or more
required validations were missing corresponding artifacts or evidence.

This is not the same as validation failure. The evaluator could not fully
evaluate the quality bar because required evidence was absent.

Examples:

- required `test_output` artifact missing,
- expected `workspace_observation` artifact missing,
- no response text for a consultation requiring a response,
- claimed code change lacks code reference artifact.

Follow-up behavior should usually be a remediation claim requesting the
missing artifacts or a replacement testament.

### claim.validation_failed

The source/evaluator completed validation and found that all required
artifacts were present, but one or more artifacts did not satisfy the
provided validations.

Examples:

- tests were provided but failed,
- code reference exists but does not implement the requirement,
- consultation response is present but contradicts workspace evidence,
- design asset exists but fails accessibility requirements.

Follow-up behavior should usually be a remediation claim describing the
failed validation and required correction.

### claim.validation_errored

The source/evaluator encountered an internal validation error that is not
part of artifact absence or artifact quality failure.

This is analogous to an HTTP 5xx class failure. It means validation could
not be completed because the validation process itself failed.

Examples:

- validator crashed,
- expected validation tool unavailable,
- knowledge backend unavailable when required for validation,
- timeout while loading artifacts,
- evaluator could not commit verdict.

Validation errors must be captured as error artifacts.

## 5. Testament Lifecycle

The canonical testament lifecycle is:

```text
testament.generated
  -> testament.posted
  -> testament.received
  -> testament.validating
  -> testament.validated

Alternative validation outcomes:

testament.validating
  -> testament.validation_incomplete
testament.validating
  -> testament.validation_failed
testament.validating
  -> testament.validation_errored
```

Error states may be represented through claim lifecycle failures, testament
status failures, and error artifacts. The testament lifecycle focuses on the
response object itself. Claim lifecycle captures the broader workflow impact.

### testament.generated

The claim target generated the testament and committed it durably to the
board.

The testament may include:

- context,
- verdict,
- confidence,
- duration,
- relations to the claim and actions,
- artifact headers,
- full artifact references.

Generation does not mean the testament has been activated as the official
response to the claim.

### testament.posted

The generated testament was posted to the board as the active response to
the claim.

The board emits `testament.posted` after this activation commits. Posting a
testament should also cause the parent claim to emit or transition through
`claim.testament_generated`.

### testament.received

The claim source/evaluator received the posted testament and acknowledged
receipt.

This is the testament-side counterpart to
`claim.testament_acknowledged`.

The source/evaluator may now begin validation.

### testament.validating

The source/evaluator has begun validating the testament and its artifacts
against the claim's validations.

This is the testament-side counterpart to `claim.validating`.

### testament.validation_incomplete

Testament validation completed, but required artifacts or evidence were
missing.

The parent claim should transition to `claim.validation_incomplete` unless
another posted testament satisfies the missing requirements in the same
transaction.

### testament.validation_failed

Testament validation completed, required artifacts were present, and one or
more artifacts failed the corresponding validations.

The parent claim should transition to `claim.validation_failed` unless
another posted testament satisfies the failed validations in the same
transaction.

### testament.validation_errored

Testament validation could not complete because the validator, evaluator,
artifact reader, expected validation tool, or infrastructure failed.

This is not a verdict on missing evidence or evidence quality. It is the
testament-side counterpart to `claim.validation_errored`. The error must be
captured as an artifact linked to the testament, claim, or nearest durable
parent that exists.

### testament.validated

Testament validation completed and all artifacts met their validations.

The parent claim should transition to `claim.satisfied` when all required
claim validations are satisfied.

## 6. Claim and Testament Status Relationship

Claim and testament statuses are related but not identical.

The claim is the obligation. The testament is the response. The claim
lifecycle describes whether the obligation has been generated, posted,
received, worked, answered, acknowledged, validated, and satisfied. The
testament lifecycle describes whether the response has been generated,
posted, received, validated, or rejected as incomplete/failed.

Important relationships:

| Claim status | Testament status | Meaning |
| --- | --- | --- |
| `claim.generated` | none | A claim exists but no response exists. |
| `claim.posted` | none | The claim is active for work. |
| `claim.received` | none | The target acknowledged the claim. |
| `claim.progressed` | none or `testament.generated` | Work is underway or partial evidence exists. |
| `claim.testament_generated` | `testament.generated` or `testament.posted` | A response exists durably. |
| `claim.testament_acknowledged` | `testament.received` | The source/evaluator acknowledged the response. |
| `claim.validating` | `testament.validating` | The source/evaluator is checking artifacts. |
| `claim.satisfied` | `testament.validated` | Required validations passed. |
| `claim.validation_incomplete` | `testament.validation_incomplete` | Required evidence is missing. |
| `claim.validation_failed` | `testament.validation_failed` | Evidence exists but fails quality bars. |
| `claim.validation_errored` | `testament.validation_errored` | The validation process itself failed. |

## 7. Delta Actions

Lifecycle deltas use the same action names as lifecycle states.

Claim delta actions:

- `claim.generated`
- `claim.generation_failed`
- `claim.posted`
- `claim.post_failed`
- `claim.received`
- `claim.receipt_failed`
- `claim.progressed`
- `claim.progress_failed`
- `claim.testament_generated`
- `claim.testament_generation_failed`
- `claim.testament_acknowledged`
- `claim.testament_acknowledgement_failed`
- `claim.validating`
- `claim.satisfied`
- `claim.validation_incomplete`
- `claim.validation_failed`
- `claim.validation_errored`

Testament delta actions:

- `testament.generated`
- `testament.posted`
- `testament.received`
- `testament.validating`
- `testament.validation_incomplete`
- `testament.validation_failed`
- `testament.validation_errored`
- `testament.validated`

Validation verdict details may still be represented as validation records,
but the claim/testament lifecycle states above are the workflow-driving
facts.

## 8. Receiver Semantics

### Target Agent

The target agent reacts to `claim.posted` or a targeted delivery projection
derived from it.

The first durable response from the target should be `claim.received` if
the agent accepts the work for processing. If receipt fails, the system
records `claim.receipt_failed`.

The target then emits `claim.progressed` as useful, generates and posts a
testament, or records progress/testament failure with error artifacts.

### Source Agent

The source agent reacts to `testament.posted`.

It acknowledges receipt through `testament.received` and
`claim.testament_acknowledged`, then validates the testament by transitioning
the claim and testament through validating and terminal validation states.

### UI

The UI renders claim and testament lifecycle facts directly.

The UI must not infer completion from:

- progress text,
- elapsed time,
- child tool rows,
- Guide route completion,
- context strings,
- unlinked testaments,
- synchronous function returns.

The UI closes a row only from a terminal lifecycle delta:

- `claim.satisfied`,
- `claim.validation_incomplete`,
- `claim.validation_failed`,
- `claim.validation_errored`,
- terminal post/generation/receipt failure states when no continuation is
  possible,
- testament terminal validation states for testament-specific rows.

### Continuations

Continuations wait on lifecycle facts, not on direct function returns.

Examples:

- A consult continuation may wait for `claim.testament_acknowledged`,
  `claim.satisfied`, or `claim.validation_incomplete`, depending on whether
  it needs receipt only or accepted evidence.
- A validation continuation waits for `claim.satisfied`,
  `claim.validation_incomplete`, `claim.validation_failed`, or
  `claim.validation_errored`.
- A UI spinner waits for terminal claim/testament lifecycle states.

## 9. Routing as Lifecycle

Guide routing is represented by claims and testaments.

The user prompt becomes a claim action. The Guide classifies the prompt by
responding with a routing testament that contains artifacts such as:

- intent,
- domain,
- target agent identity,
- confidence,
- rationale,
- direct-address evidence,
- constraints from the prompt.

The routed work is a generated claim against the target agent. It is then
posted for action. The target acknowledges with `claim.received`, works via
`claim.progressed`, and responds with a generated/posted testament.

There is no separate `ForwardedRequest` execution authority in the final
model. The Guide event bus remains the transport, but it transports board
lifecycle deltas rather than ad hoc routed work messages.

## 10. Consultations, Challenges, and Guardian Checks

Consultations, challenges, and guardian checks are claim actions.

### Consultation

A consultation claim asks another agent for information or judgment.

Typical lifecycle:

```text
claim.generated
claim.posted
claim.received
claim.progressed
testament.generated
testament.posted
testament.received
claim.testament_acknowledged
claim.validating
testament.validated
claim.satisfied
```

The issuer may choose receipt-only validation for simple consultations, or
inspection validation when the response must be checked against stronger
quality bars.

### Challenge

A challenge claim asserts that another agent's output has a problem.

The challenged agent responds with a testament containing rebuttal,
acceptance, correction, or error artifacts. The issuer validates the
response. The challenge may be satisfied, incomplete, failed, or errored.

The row may show "response received" after
`claim.testament_acknowledged`, but the challenge claim is not satisfied
until validation completes.

### Guardian Check

A guardian check is a claim against Guardian or a guardian-capable agent.

Permission decisions, safety scans, policy checks, command approvals, and
git gates are testaments with artifacts. The calling agent waits on the
appropriate lifecycle state.

## 11. Expected Tool Calls

Claims and validations may contain expected tool calls.

Claim-level expected tool calls guide target-agent work. Validation-level
expected tool calls guide source/evaluator verification.

Expected tool execution produces artifacts. If execution fails, the failure
is an error artifact.

Required expected tool calls do not bypass policy. The receiver must still
validate authority, tool availability, arguments, deadlines, and local
policy.

If a required expected tool call is not attempted, the target or evaluator
must explain why through a testament, validation result, or error artifact.

## 12. Failure Semantics

Failure states are first-class lifecycle states. They are not exceptions to
the lifecycle.

Use the most specific failure:

- generation failure when an object cannot be generated,
- post failure when a generated object cannot be activated,
- receipt failure when a receiver cannot acknowledge,
- progress failure when work fails before a response testament is posted,
- testament generation failure when the response cannot be generated or
  posted,
- testament acknowledgment failure when the source/evaluator cannot ingest
  the response,
- validation incomplete when evidence is missing,
- validation failed when evidence is present but does not satisfy
  validation,
- validation errored when the validation process itself failed.

Every failure must preserve evidence as artifacts when a durable parent
object exists.

## 13. Idempotency and Replay

Every lifecycle delta must have a stable idempotency key derived from:

- board ID,
- sequence or committed object version,
- lifecycle action,
- object refs,
- receiver dimension when the event is receiver-specific.

Receivers must deduplicate by delta key and sequence.

Replay must be safe:

- replaying `claim.generated` must not generate a duplicate claim,
- replaying `claim.received` must not start duplicate work,
- replaying `testament.posted` must not trigger duplicate validation,
- replaying terminal validation states must not reopen work.

## 14. Self-Targeting

Claims must not target the same canonical agent identity for consultation,
challenge, guardian check, or ordinary peer work unless the action
explicitly represents a legitimate self-transfer such as handoff or local
reflection.

Self-targeting must be rejected before `claim.posted` when possible. If the
claim was already generated, it transitions to `claim.post_failed` with an
error artifact explaining the identity violation.

## 15. Minimal Implementation Contract

A claims/testaments lifecycle implementation is correct only if:

1. Generated claims and generated testaments are durable board records.
2. Posted means activated for downstream workflow, not merely inserted.
3. Receiver acknowledgment is a receiver-committed fact, not a sender-side
   delivery guess.
4. Work progress is non-terminal.
5. Testaments and artifacts are the only substantive outcome channel.
6. Validation outcomes distinguish missing evidence, failed evidence, and
   validator errors.
7. Errors are artifacts.
8. UI and continuations react to lifecycle deltas, not synchronous route
   returns or inferred metadata.
9. Replay is idempotent.
10. Direct Guide routing does not remain a second workflow authority.

## 16. Phased Implementation Plan

This plan implements the lifecycle as the foundation for Guide routing,
claim/testament execution, UI rendering, validations, and peer work. The
phases are ordered to avoid another hybrid system: first make lifecycle
state durable and explicit, then make deltas and receivers obey it, then
move Guide and skills onto it, then delete legacy execution paths.

Each phase must be implemented with production-grade tests. Unit tests cover
pure lifecycle behavior. Integration tests use `github.com/vektra/mockery`
generated mocks for board stores, delta publishers, Guide bus publishers,
ClaimsInbox handlers, identity resolvers, tool runtimes, artifact stores,
and validators. End-to-end tests exercise the real session board, Guide bus,
agents, UI bridge, replay, interruption, and failure flows.

### Phase 1: Lifecycle Data Model and Transition Rules

Phase 1 makes claim and testament lifecycle states first-class board data.
It does not change Guide routing yet.

#### Item 1.1: Add Claim Lifecycle Statuses

**Description:** Add explicit claim lifecycle statuses for every state in
this document: `generated`, `generation_failed`, `posted`, `post_failed`,
`received`, `receipt_failed`, `progressed`, `progress_failed`,
`testament_generated`, `testament_generation_failed`,
`testament_acknowledged`, `testament_acknowledgement_failed`,
`validating`, `satisfied`, `validation_incomplete`, `validation_failed`,
and `validation_errored`. Preserve the existing generic claim status only as
a projection if callers still need coarse categories.

**Examples:**

- Architect generates a plan claim set before user approval. Claims are
  durable with status `generated`, but no Engineer or Tester wakes.
- A self-targeted consultation is generated, rejected before posting, and
  transitions to `post_failed` with an error artifact.
- Librarian receives a posted consultation and the claim transitions to
  `received` before any workspace work starts.

**Acceptance Criteria:**

- Claim lifecycle status is stored durably on the claim.
- Every status transition appends status history with from, to, reason,
  actor, and timestamp.
- Generated claims can exist with validations and expected tool calls.
- Generated claims do not imply target-agent delivery.
- Posting requires the claim to have a valid generated record.
- Terminal statuses are explicit and cannot transition back to active work.
- Failure statuses require an error artifact relation when a durable parent
  exists.
- Existing board queries can filter by lifecycle status without inferring
  from progress text or testament presence.

**Test Cases:**

- Unit: every allowed transition succeeds and records status history.
- Unit: illegal transitions fail deterministically with typed errors.
- Unit: terminal states reject further progress, receipt, or posting.
- Unit: `generated -> posted -> received -> progressed -> testament_generated -> testament_acknowledged -> validating -> satisfied` succeeds.
- Unit: `generated -> post_failed` succeeds and records error metadata.
- Unit: failure statuses without error artifacts are rejected when artifact
  parentage is available.
- Unit: unknown lifecycle status fails JSON decode validation.
- Integration with mockery board store: generated claims persist and reload
  with validations, expected tool calls, relations, and status history.
- Integration with mockery artifact store: post failure attaches an error
  artifact and relation to the failed claim.
- Integration with mockery identity resolver: unresolved subject prevents
  `posted` and transitions to `post_failed`.
- Integration race: two goroutines attempt to post the same generated claim;
  exactly one succeeds and the other observes idempotent state.
- Integration deadlock: transition hooks that query board snapshots do not
  re-enter the write lock.
- E2E happy: a generated task claim is posted and eventually satisfied.
- E2E negative: a generated claim with missing subject cannot post and does
  not wake an agent.
- E2E replay: replaying the WAL reconstructs identical lifecycle state and
  status history.
- E2E race: duplicate delivery of the same post attempt does not duplicate
  status history.

#### Item 1.2: Add Testament Lifecycle Statuses

**Description:** Add explicit testament lifecycle statuses:
`generated`, `posted`, `received`, `validating`, `validation_incomplete`,
`validation_failed`, `validation_errored`, and `validated`. Testament
status is independent from claim status while remaining related through
claim/testament relations.

**Examples:**

- Librarian finishes consultation work and generates a testament with
  workspace observation artifacts. It is durable as `testament.generated`.
- The generated testament is activated as the response with
  `testament.posted`.
- Architect receives the response and acknowledges `testament.received`
  before validation begins.

**Acceptance Criteria:**

- Testament lifecycle status is durable and immutable transitions are
  captured in status history.
- `testament.generated` requires a relation to the parent claim unless the
  testament is explicitly a standalone system or archival testament.
- `testament.posted` requires a generated testament.
- `testament.received` is committed by the source/evaluator side, not by the
  target agent or sender.
- Testament terminal validation statuses update related claim lifecycle
  according to the claim validation outcome.
- `testament.validation_errored` updates the related claim to
  `claim.validation_errored`, not `claim.validation_failed`.
- Large artifacts remain by reference; status transitions carry compact
  artifact headers only.

**Test Cases:**

- Unit: allowed testament lifecycle transitions succeed.
- Unit: `testament.posted` without generated status fails.
- Unit: posted testament missing claim relation fails unless explicitly
  standalone.
- Unit: terminal testament validation states reject revalidation without a
  superseding/amending testament.
- Unit: `testament.validation_errored` is terminal and classified as a
  failure.
- Integration with mockery board store: generated testament reloads with
  artifact headers and relations.
- Integration with mockery source ack handler: source commits
  `testament.received`, not the target.
- Integration race: source acknowledgment and replayed posted delta happen
  concurrently and produce one received transition.
- E2E happy: consultation response moves through generated, posted,
  received, validating, validated.
- E2E negative: relationless response does not resolve the parent claim.
- E2E edge: two testaments answer one claim; validation uses the selected or
  latest applicable testament according to relation and supersession rules.
- E2E validator-error: validator infrastructure failure moves the testament
  to `validation_errored` and the claim to `validation_errored` with an
  error artifact.

#### Item 1.3: Define Transition Graph and Terminality Helpers

**Description:** Centralize lifecycle transition rules in `core/claims`
instead of scattering `if status == ...` checks across Guide, ClaimsInbox,
UI, and skills.

**Examples:**

- `IsClaimActionable(status)` returns true for `posted` but false for
  `generated`.
- `IsClaimTerminal(status)` returns true for `satisfied`,
  `validation_incomplete`, `validation_failed`, `validation_errored`, and
  irrecoverable failure statuses.
- `CanTransitionClaim(from, to)` rejects `satisfied -> progressed`.

**Acceptance Criteria:**

- Transition legality is defined in one package.
- Actionable, terminal, failure, receipt, validation, and progress helpers
  are explicit and covered by table tests.
- No UI, Guide, skill, or inbox package owns its own lifecycle truth.
- Helper names reflect lifecycle semantics, not display labels.

**Test Cases:**

- Unit table tests cover every claim status pair.
- Unit table tests cover every testament status pair.
- Unit: helper classifications cover all statuses exactly once where
  applicable.
- Unit: claim/testament validation-error mapping is covered explicitly:
  `testament.validation_errored -> claim.validation_errored`.
- Integration static test: packages outside `core/claims` do not define
  duplicate terminal status lists.
- E2E replay: terminal states remain terminal after board restart.

#### Item 1.4: Preserve Errors as Artifacts for Lifecycle Failures

**Description:** Ensure every lifecycle failure path records an error
artifact when a durable parent object exists. Returned errors still exist
for local control flow, but workflow truth is the error artifact.

**Examples:**

- Claim post fails because target identity is ambiguous. The generated claim
  transitions to `post_failed` and gets an error artifact.
- Testament generation fails because an expected artifact cannot be
  serialized. The parent claim transitions to
  `testament_generation_failed` with the serialization error artifact.
- Testament validation errors because a validator backend is unavailable.
  The testament transitions to `validation_errored`; the claim transitions
  to `validation_errored`; the unavailable-backend error is preserved as an
  artifact.

**Acceptance Criteria:**

- Failure transitions include artifact references or a documented reason
  why no durable parent exists.
- Error artifact kind and diagnostic payload are structured, not only text.
- Internal errors, policy denials, timeouts, and interruptions use distinct
  error artifact categories.
- Failure artifacts are visible to validators and UI.

**Test Cases:**

- Unit: every failure status requires structured error payload.
- Unit: timeout, interruption, policy denial, unavailable dependency, and
  panic recovery map to distinct error artifact kinds.
- Unit: testament validation error paths preserve error artifact metadata
  and do not collapse into `validation_failed`.
- Integration with mockery artifact sink: failed lifecycle transition stores
  error artifact before or atomically with status transition.
- Integration negative: artifact sink failure is recorded on the nearest
  durable parent and does not panic the board.
- E2E: failed consultation shows error artifact and terminal failure row.
- E2E replay: failure artifact and failure status replay together.

### Phase 2: Board Mutation APIs and Durable Generated Records

Phase 2 exposes lifecycle operations as explicit board APIs. Callers stop
mutating claim/testament structs directly.

#### Item 2.1: Add Generated Claim APIs

**Description:** Add board APIs for generating claims without activating
them. The API validates relations, validations, expected tool specs, and
status initialization before committing the generated record.

**Examples:**

- `GenerateClaimAction` creates a claim action and several generated claims.
- `GenerateClaim` creates one generated claim under an existing action.
- Architect can generate plan claims that are visible to UI but not
  delivered to workers until user approval.

**Acceptance Criteria:**

- Generated claims are committed durably with status `generated`.
- Generation validates required fields and validation structure.
- Generated claims can be read and traversed before posting.
- Generated claims never publish target-actionable deltas.
- Duplicate generation requests with the same idempotency key return the
  existing generated claim.

**Test Cases:**

- Unit: generated claim validation rejects missing title, description,
  action, issuer relation, validation ID, or invalid expected tool spec.
- Unit: generated claim can omit subject only when explicitly allowed for
  classification or draft planning.
- Integration with mockery durable store: generation commit is atomic across
  action, claims, validations, and status history.
- Integration idempotency: same generation key returns same claim IDs.
- Integration race: concurrent generation for same deterministic key creates
  one record.
- E2E: Guide generates a route-classification claim without waking
  Architect.

#### Item 2.2: Add Claim Posting APIs

**Description:** Add explicit board APIs to activate generated claims for
workflow. Posting performs identity resolution, self-target checks, policy
validation, expected tool call validation, and delivery projection creation.

**Examples:**

- Guide posts a generated work claim to Architect after classification.
- Architect posts a generated consultation claim to Librarian.
- A self-targeted challenge transitions to `post_failed`.

**Acceptance Criteria:**

- Only generated claims can be posted.
- Posting resolves canonical subject/evaluator identities.
- Posting rejects display-name-only targets when canonical identity is
  available.
- Posting rejects unauthorized self-targeting before delivery.
- Posting emits non-actionable `claim.posted` board fact and an actionable
  receiver-specific delivery delta only after commit.
- Posting failure transitions the generated claim to `post_failed` with an
  error artifact.

**Test Cases:**

- Unit: `posted` transition fails from any state except generated or
  idempotent posted.
- Unit: self-target consultation, challenge, and guardian check fail.
- Unit: legitimate self-transfer handoff can post when action explicitly
  allows it.
- Integration with mockery identity registry: canonical UID is stamped in
  lifecycle delivery context.
- Integration with mockery policy engine: policy denial produces
  `post_failed`.
- Integration race: post and post_failed attempts cannot both commit.
- E2E: posted claim wakes exactly one canonical target agent.
- E2E negative: generated claim does not wake target before post.
- E2E replay: replayed posted claim does not wake duplicate work.

#### Item 2.3: Add Generated and Posted Testament APIs

**Description:** Add board APIs for generating a testament and then posting
it as an active response. This prevents target agents from half-posting
responses and lets failures be recorded before the source/evaluator wakes.

**Examples:**

- Librarian generates a response testament with workspace artifacts, then
  posts it to Architect.
- Tool failure before response assembly transitions the claim to
  `progress_failed`.
- Failure while posting the generated testament transitions the claim to
  `testament_generation_failed` with an error artifact.

**Acceptance Criteria:**

- Testament generation commits artifact headers and relations durably.
- Testament posting activates the generated testament as a response.
- Posted testament updates parent claim to `testament_generated`.
- Source/evaluator is not woken by `testament.generated`; it is woken by
  `testament.posted`.
- Post failure preserves generated testament and records failure state.
- Testament validation-error completion is exposed through the same
  testament validation API as validated/incomplete/failed completion, with
  `testament.validation_errored` as an allowed terminal target.

**Test Cases:**

- Unit: generated testament requires claim relation and testament action
  relation unless standalone.
- Unit: posted testament without generated status fails.
- Unit: completing testament validation with `validation_errored` succeeds
  only from `testament.validating`.
- Unit: artifact headers are validated for required fields and bounded
  sizes.
- Integration with mockery artifact store: full artifacts are stored before
  generated testament commit or commit fails cleanly.
- Integration race: two generated testaments post concurrently; board
  applies supersession/selection rules deterministically.
- E2E: target generates testament, source does not validate until posted.
- E2E failure: testament post fails and source agent is not woken.
- E2E validator-error: source/evaluator records a testament validation
  error through the board API and no extra workflow path is used.

#### Item 2.4: Add Receipt and Acknowledgment APIs

**Description:** Add receiver-committed APIs for claim receipt,
testament receipt, and claim testament acknowledgment. Senders must not mark
objects received on behalf of receivers.

**Examples:**

- Architect ClaimsInbox receives a posted work claim and commits
  `claim.received`.
- Guide receives Architect's posted planning testament and commits
  `testament.received` and `claim.testament_acknowledged`.

**Acceptance Criteria:**

- Receipt APIs require receiver canonical identity.
- Claim receipt can be committed only by the claim subject or another
  explicitly allowed receiver relation.
- Testament receipt can be committed only by the claim source/evaluator or
  allowed observer relation.
- Receipt failure records `receipt_failed` or
  `testament_acknowledgement_failed` with error artifacts.
- Receipts are idempotent per receiver.

**Test Cases:**

- Unit: sender cannot mark target claim received.
- Unit: target cannot mark source testament received.
- Unit: duplicate receipt is idempotent.
- Integration with mockery ClaimsInbox: processing posted delta commits
  receipt before starting work.
- Integration failure: receipt commit failure records receipt failure
  through supervisor path.
- E2E: all agent-to-agent handoffs show explicit received states.
- E2E race: receipt ack and cancellation race yields one coherent terminal
  state.

### Phase 3: Canonical Lifecycle Deltas and Guide Bus Transport

Phase 3 makes lifecycle deltas the only runtime signal used by agents,
validators, UI, and continuations.

#### Item 3.1: Replace Generic Delta Actions With Lifecycle Actions

**Description:** Update canonical delta action names to match this
document. Replace older `claim.directed`, `testament.submitted`, and broad
transition events with lifecycle-specific actions.

**Examples:**

- `claim.generated` is emitted after generated claim commit.
- `claim.posted` is emitted after claim activation.
- `testament.posted` wakes the source/evaluator.
- `claim.satisfied` closes a work row.

**Acceptance Criteria:**

- Delta schema contains every lifecycle action listed in this document.
- Old broad actions are removed or treated as projections, not workflow
  inputs.
- `testament.validation_errored` has a canonical delta action and round
  trips through the same schema as other testament validation terminal
  states.
- Generated deltas are observable but not actionable.
- Posted and received deltas carry receiver dimensions when relevant.
- Delta refs can reference actions, claims, testaments, validations, and
  artifacts without assuming one-to-one cardinality.

**Test Cases:**

- Unit: enum coverage test ensures every lifecycle status has a delta
  action or documented non-delta projection.
- Unit: JSON round trip for every lifecycle delta.
- Unit: `testament.validation_errored` maps to and from
  `TestamentLifecycleValidationErrored`.
- Unit: malformed lifecycle action fails decode.
- Integration with mockery delta publisher: board mutation emits exact
  lifecycle delta after commit.
- Integration with mockery store: failed commit emits no delta.
- E2E: generated claim appears in observer stream but does not wake target.
- E2E: posted claim wakes target exactly once.

#### Item 3.2: Derive Stable Idempotency Keys

**Description:** Implement stable idempotency keys for every lifecycle
delta. Keys must survive replay and include receiver dimensions for
receiver-specific facts.

**Examples:**

- `claim.posted:<board>:<claim>:<version>` for board fact.
- `claim.received:<board>:<claim>:<receiver_uid>` for receiver ack.
- `testament.posted:<board>:<testament>:<claim>` for response activation.
- `testament.validation_errored:<board>:<testament>:<claim>` for
  validator infrastructure failure.

**Acceptance Criteria:**

- Same committed fact always produces the same delta key.
- Different receiver acknowledgments produce different keys.
- Retries and durable replay do not produce duplicate work.
- Keys never include display names when canonical UID exists.
- Key generation is deterministic and allocation-bounded.

**Test Cases:**

- Unit: deterministic key table for every delta action.
- Unit: `testament.validation_errored` key is stable across replay and
  distinct from `testament.validation_failed`.
- Unit: receiver dimension changes key.
- Unit: replayed object version yields same key.
- Integration with mockery replay source: duplicate WAL events dedupe.
- E2E replay: restarting the app does not duplicate claim receipt or work.
- Fuzz: malformed refs cannot panic key generation.

#### Item 3.3: Publish Lifecycle Deltas Through the Guide Event Bus

**Description:** Wire board lifecycle deltas through the existing Guide
event bus. The Guide bus remains transport. It does not own workflow truth.

**Examples:**

- Board commits `claim.posted`; Guide bus publishes a claims lifecycle
  message to the target agent topic.
- Board commits `testament.posted`; Guide bus publishes to the source or
  evaluator topic.
- Board commits `testament.validation_errored`; Guide bus publishes the
  testament lifecycle fact to observer topics and the corresponding
  `claim.validation_errored` fact to claim observers/continuations.
- UI subscribes to session-wide lifecycle deltas.

**Acceptance Criteria:**

- All lifecycle deltas are published through Guide bus topics.
- Topic grammar uses session, board, lifecycle action, and canonical
  receiver identity where applicable.
- Wildcard observer topics receive all lifecycle facts for UI and logging.
- Actionable topics receive only posted/receipt/validation work signals.
- Publishing failure is surfaced as operational telemetry and retryable
  according to existing bus durability semantics.

**Test Cases:**

- Unit: topic helper table covers every action and receiver pattern.
- Unit: generated actions route only to observer topics.
- Unit: `testament.validation_errored` routes as a terminal testament
  lifecycle fact and does not wake target work.
- Integration with mockery Guide bus: exact topic and payload published.
- Integration failure: bus publish failure does not roll back committed board
  state and is retry-visible.
- Integration race: concurrent delta publication preserves board sequence
  order per session.
- E2E: UI and target agent both observe the same posted lifecycle fact.
- E2E partition: temporarily unavailable receiver catches up by replay.

#### Item 3.4: Enforce Non-Actionable Generated Deltas

**Description:** Ensure `claim.generated` and `testament.generated` never
trigger target/source agent execution, validation, continuation resume, or
UI terminal closure.

**Examples:**

- User asks for a plan. Architect-generated task claims appear as draft
  board facts but Engineers do not start work.
- Librarian generates a response testament but Architect does not validate
  until the testament is posted.

**Acceptance Criteria:**

- Generated deltas are classified as observer-only.
- ClaimsInbox ignores generated deltas for work execution.
- Continuation store ignores generated deltas for wakeups.
- UI may render generated state but cannot close or resume rows from it.
- `testament.validation_errored` is terminal, not generated/progress, and
  can close testament-specific rows while the paired
  `claim.validation_errored` closes claim-level waits.

**Test Cases:**

- Unit: actionability helper returns false for generated deltas.
- Integration with mockery ClaimsInbox: generated claim produces no
  ProcessEntry call.
- Integration with mockery continuation store: generated testament produces
  no resume.
- E2E: generated plan claims do not start implementation before approval.
- E2E race: generated and posted deltas delivered out of order still result
  in work starting only after posted is reconciled.

### Phase 4: ClaimsInbox, Continuations, and Receiver Behavior

Phase 4 updates receivers so lifecycle deltas, not forwarded requests or
tool-return side channels, drive execution.

#### Item 4.1: Update ClaimsInbox to Consume Lifecycle Deltas

**Description:** ClaimsInbox must resolve lifecycle delta envelopes into
work entries, acknowledgments, validation tasks, and continuation wakeups.
It should not process old raw claim-created events as work.

**Examples:**

- Architect ClaimsInbox receives `claim.posted`, commits `claim.received`,
  and starts work.
- Architect ClaimsInbox receives `testament.posted` for a consultation it
  issued, commits testament receipt and resumes waiting planning logic.

**Acceptance Criteria:**

- Work starts only from actionable posted claim lifecycle deltas.
- Receipt is committed before work execution begins.
- Source/evaluator receipt is committed before validation begins.
- Generated, progress, and observer-only deltas do not start tool loops.
- `testament.validation_errored` is observer/terminal-result information;
  it never starts target-agent work.
- Dedup store is bounded and keyed by delta key.
- ClaimsInbox has tracked goroutine ownership and clean shutdown.

**Test Cases:**

- Unit: lifecycle action dispatch table covers every action.
- Unit: generated and progress actions are non-executing.
- Integration with mockery board API: `claim.posted` causes receipt commit
  then ProcessEntry call.
- Integration with mockery board API: ProcessEntry is not called if receipt
  commit fails.
- Integration with mockery continuation store: `testament.posted` resolves
  waiting consult continuation exactly once.
- Integration with mockery ClaimsInbox: `testament.validation_errored`
  reaches observer projections but does not invoke target `ProcessEntry`.
- Integration race: duplicate posted delta and replayed receipt do not
  start duplicate work.
- Integration deadlock: ClaimsInbox close during delivery drains workers.
- E2E: Architect executes only from posted lifecycle delta.
- E2E interrupt: interrupted agent emits progress or failure lifecycle and
  no goroutine is leaked.

#### Item 4.2: Convert Continuations to Lifecycle Waits

**Description:** Continuations should wait on lifecycle states, not
synchronous Guide responses, tool-completed artifacts, or compatibility
events.

**Examples:**

- `consult_peer` waits for `claim.testament_acknowledged` for receipt-only
  consults or `claim.satisfied` for validated consults.
- `challenge_peer` waits for testament acknowledgment but keeps validation
  pending until inspection completes.
- Knowledge readiness waits for the readiness claim/testament lifecycle.

**Acceptance Criteria:**

- Continuation wait predicates are lifecycle-state based.
- Waits have bounded timeout and cancellation.
- Wait completion records the delta key that satisfied the wait.
- Timeouts produce error artifacts and failure lifecycle states.
- Duplicate terminal deltas are idempotent.
- Testament validation-error waits complete from the paired
  `claim.validation_errored` lifecycle fact, and the satisfying delta key is
  recorded.

**Test Cases:**

- Unit: wait predicate matches correct lifecycle state only.
- Unit: timeout produces typed error artifact spec.
- Integration with mockery continuation store: duplicate terminal deltas
  wake once.
- Integration race: terminal and timeout fire concurrently; exactly one
  outcome commits.
- Integration negative: progress delta does not wake consult wait.
- Integration negative: `testament.validation_errored` alone does not wake a
  claim-level consult wait unless the paired `claim.validation_errored` is
  also committed.
- E2E: consult resumes only after posted testament is acknowledged.
- E2E: challenge response row closes while claim validation remains pending
  when configured that way.
- E2E cancellation: interrupted wait records failure lifecycle and exits.

#### Item 4.3: Enforce Canonical Identity and Self-Target Rules at Receipt

**Description:** Receivers must verify canonical identity before acknowledging
receipt. Self-targeted work must fail before execution unless explicitly
allowed.

**Examples:**

- Architect receives a claim whose subject is Architect's own UID for a
  consultation. Receipt is rejected and the claim transitions to
  `receipt_failed` or `post_failed`, depending on when detected.
- Librarian receives a degraded display-name subject and resolves it before
  work starts.

**Acceptance Criteria:**

- Receipt requires receiver UID match or authorized relation.
- Degraded agent refs are resolved before receipt.
- Self-target policy is checked both at post and receipt boundaries.
- Identity mismatch produces error artifact and no work execution.

**Test Cases:**

- Unit: UID mismatch rejects receipt.
- Unit: allowed observer relation cannot start subject work.
- Integration with mockery identity resolver: degraded ref resolves to UID
  before receipt.
- Integration negative: resolver ambiguity records receipt failure.
- E2E: Architect cannot consult or challenge itself accidentally.
- E2E race: identity rotation during delivery does not misdeliver work.

### Phase 5: Validation Semantics and Expected Tool Execution

Phase 5 makes validation lifecycle precise and prevents receipt from being
confused with work satisfaction.

#### Item 5.1: Separate Receipt, Completeness, Quality, and Internal Errors

**Description:** Implement validation outcomes that distinguish received
responses, missing evidence, failed evidence, and validator errors.

**Examples:**

- Consultation has response text but no required workspace observation:
  `validation_incomplete`.
- Tests artifact exists but contains failures: `validation_failed`.
- Test runner unavailable: `validation_errored`.
- Required artifacts and quality bars pass: `satisfied`.

**Acceptance Criteria:**

- Receipt validation can pass without satisfying work validations.
- Missing artifacts produce `claim.validation_incomplete` and
  `testament.validation_incomplete`.
- Present but failing artifacts produce `claim.validation_failed` and
  `testament.validation_failed`.
- Validator infrastructure failure produces `claim.validation_errored` and
  `testament.validation_errored` when a testament is under validation.
- Validation outcomes include reason, reviewed artifacts, evaluator
  identity, and status history.

**Test Cases:**

- Unit: receipt validation does not imply work satisfaction.
- Unit: missing artifact classification is incomplete, not failed.
- Unit: failing artifact classification is failed, not incomplete.
- Unit: validator panic recovers as validation_errored with error artifact.
- Integration with mockery artifact reader: missing artifact ref produces
  incomplete.
- Integration with mockery tool runtime: test command failure produces
  failed; tool unavailable produces errored.
- E2E: consultation with response only satisfies receipt but not inspection.
- E2E: full response with required artifacts satisfies claim.
- E2E replay: validation outcome remains stable after restart.

#### Item 5.2: Execute Validation Expected Tool Calls as Artifacts

**Description:** Validation expected tools should be run by the evaluator
according to policy, and every attempt should produce artifacts.

**Examples:**

- A test validation expects `run_tests`; evaluator runs it and attaches
  `test_output`.
- A design validation expects an accessibility check; evaluator attaches
  `a11y_audit`.
- Tool denial attaches policy error artifact and validation_errored or
  validation_incomplete depending on semantics.

**Acceptance Criteria:**

- Expected validation tool specs are validated before execution.
- Required expected tool attempts are recorded as artifacts.
- Successful tool outputs are linked to the validation and testament.
- Failed tool attempts are error artifacts.
- Concurrent expected tools cannot corrupt shared accumulators.
- Tool execution respects timeouts, cancellation, user approval, and policy.

**Test Cases:**

- Unit: expected tool spec validation rejects unknown tool, duplicate ID,
  invalid arguments, and unsafe policy.
- Unit: required expected tool refusal must generate error artifact.
- Integration with mockery tool runtime: allowed tool runs with exact
  arguments and emits artifact.
- Integration with mockery policy engine: denied tool produces error
  artifact and no runtime call.
- Integration race: parallel expected tools produce unique artifact IDs.
- Integration cancellation: canceling validation stops tools and records
  interruption artifact.
- E2E: test validation runs expected test tool and satisfies claim on pass.
- E2E negative: test tool output present but failing results in
  validation_failed.
- E2E deadlock: validation tool waiting on board read cannot block board
  write lock.

#### Item 5.3: Wire Remediation Claims From Validation Outcomes

**Description:** Validation failure and incompleteness should produce
precise remediation claims when the issuer/evaluator decides more work is
needed. This uses the same lifecycle, not a separate corrective system.

**Examples:**

- Missing test output creates a remediation claim: "Submit test output for
  claim X."
- Failed inspection creates a corrective claim against the original subject.
- Validator internal error creates a claim against the appropriate system or
  guardian agent if recovery requires external action.

**Acceptance Criteria:**

- Remediation claims are generated with relations to the failed claim,
  failed validation, and offending/missing artifact.
- Remediation claims start as `generated` and post only when authorized.
- Original claim remains terminal incomplete/failed/errored unless amended
  by a superseding testament and validation.
- Remediation never silently rewrites the original testament.

**Test Cases:**

- Unit: remediation relation graph includes caused_by, reviews, and
  supersedes/amends when applicable.
- Integration with mockery board: failed validation generates remediation
  claim action atomically or records generation failure.
- Integration negative: remediation generation failure becomes error
  artifact.
- E2E: failed validation creates visible remediation claim and target works
  it through lifecycle.
- E2E replay: remediation claim is not duplicated after restart.

### Phase 6: Guide Routing Refactor

Phase 6 removes Guide's direct execution authority. Guide remains the
transport and classifier, but routing is represented by claim/testament
lifecycle.

#### Item 6.1: Model User Prompt and Classification as Lifecycle Work

**Description:** Convert top-level user prompts into generated claim actions.
Guide classifies by responding with a routing testament containing route
artifacts.

**Examples:**

- User prompt generates a prompt/classification claim against Guide.
- Guide posts or receives the classification claim as appropriate.
- Guide submits a routing testament with intent, domain, target agent UID,
  confidence, direct-address evidence, and rationale artifacts.

**Acceptance Criteria:**

- User prompt is durable before classification side effects.
- Classification result is a testament, not unlinked narration.
- Classification errors are error artifacts on a failure testament or claim
  generation failure.
- Direct address, cached route, LLM route, and fallback route all produce
  equivalent route-decision artifacts.
- Classification claim lifecycle can be replayed without re-calling the LLM
  when a committed testament exists.

**Test Cases:**

- Unit: route-decision testament requires target identity artifact.
- Unit: direct-address route includes direct-address artifact.
- Integration with mockery classifier: classification success posts routing
  testament.
- Integration with mockery classifier error: error artifact and failure
  lifecycle produced.
- Integration replay: existing route testament suppresses duplicate
  classifier call.
- E2E: new TUI prompt creates prompt claim, Guide route testament, and
  routed work claim.
- E2E negative: classifier unavailable records lifecycle failure and does
  not wake target.

#### Item 6.2: Generate and Post Routed Work Claims

**Description:** After classification, Guide generates a routed work claim
against the target agent. It posts the claim only when all routing evidence,
identity, policy, and self-target checks pass.

**Examples:**

- Guide generates Architect work claim from user prompt and route testament.
- Guide posts Architect claim; Architect receives and acknowledges.
- If the target is invalid, the claim remains `post_failed` with error
  artifact.

**Acceptance Criteria:**

- Routed work claim links to user prompt action and route testament.
- Routed work claim carries canonical target agent identity.
- Routed work claim includes expected tool calls or protocol instructions
  only when authorized by Guide/Architect policy.
- `claim.generated` does not wake target.
- `claim.posted` wakes exactly the target's ClaimsInbox.
- Guide does not publish `ForwardedRequest` as execution authority.

**Test Cases:**

- Unit: routed claim relation graph includes prompt, route testament, issuer,
  subject, and caused_by.
- Unit: target identity validation rejects display-name-only target.
- Integration with mockery Guide bus: only lifecycle deltas are published
  for work delivery.
- Integration negative: post failure records error artifact and no target
  ProcessEntry call.
- E2E: Architect starts work from posted claim, not ForwardedRequest.
- E2E replay: already posted routed claim does not trigger duplicate
  Architect cycle.

#### Item 6.3: Remove ForwardedRequest Execution for Normal Work

**Description:** Delete or hard-disable direct `ForwardedRequest` execution
for normal Guide-to-agent work. If any transport struct remains for
non-work telemetry, it must not start agent work.

**Examples:**

- `publishForwardedRequest` no longer sends executable work messages.
- Architect no longer handles normal user work in `handleForwardBusRequest`.
- Existing tests expecting forwarded execution are rewritten around
  lifecycle deltas.

**Acceptance Criteria:**

- No normal user prompt can start an agent without a posted claim lifecycle
  delta.
- Direct Guide route responses cannot close UI rows or satisfy
  continuations.
- Legacy execution path is removed rather than preserved behind flags.
- Static tests fail if normal work calls `MessageTypeForward` execution
  handlers.

**Test Cases:**

- Unit/static: forbidden call graph from Guide route to ForwardedRequest
  execution is absent.
- Integration with mockery bus: Guide route publishes lifecycle delta
  messages only.
- Integration negative: synthetic ForwardedRequest does not start Architect
  work.
- E2E: TUI prompt flows fully through claims lifecycle.
- E2E: disabling lifecycle delivery prevents work, proving no hidden
  fallback route exists.

#### Item 6.4: Refactor Guide Skills to Lifecycle APIs

**Description:** Guide-facing skills should generate, post, acknowledge,
and validate lifecycle records. They must not bypass the board by returning
implicit routing outcomes.

**Examples:**

- Route skill generates route/classification claim and testament.
- User approval skill posts previously generated work claims.
- Guide interruption skill posts progress/failure lifecycle and error
  artifacts.

**Acceptance Criteria:**

- Guide skills use board lifecycle APIs exclusively for workflow state.
- Skill errors become error artifacts when parent claim/action exists.
- Skills are idempotent under repeated tool calls and replay.
- Prompt wording tells Guide to produce claims/testaments/artifacts, not
  ad hoc prose state.

**Test Cases:**

- Unit: each Guide skill validates lifecycle preconditions.
- Integration with mockery board: skill calls exact lifecycle API sequence.
- Integration negative: board failure returns local error and records error
  artifact where possible.
- E2E: Guide skill replay does not duplicate claims or testaments.
- E2E interruption: Guide reports interruption through lifecycle failure.

### Phase 7: Peer Skills, Guardian Checks, and Knowledge Readiness

Phase 7 converts peer work to lifecycle-driven claims and removes
compatibility completion events.

#### Item 7.1: Convert consult, challenge, and guardian skills

**Description:** `consult_peer`, `challenge_peer`, and guardian check skills
must generate and post claim actions, then wait on lifecycle states. They
must not call synchronous Guide routing after the claim is posted.

**Examples:**

- Architect consults Librarian by generating/posting a consultation claim.
- Librarian receives, progresses, posts a response testament.
- Architect acknowledges and validates according to consultation policy.

**Acceptance Criteria:**

- Peer skills generate claims with canonical subject identity.
- Self-targeting is rejected before work starts.
- Skills wait on lifecycle deltas, not tool-completed artifacts or
  `consult_resolved`.
- Receipt-only consults and inspection-required challenges use different
  wait predicates.
- Timeouts and interruptions produce error artifacts and failure lifecycle.

**Test Cases:**

- Unit: consult wait predicate does not match progress or generated states.
- Unit: challenge wait predicate distinguishes response receipt from
  validation satisfaction.
- Integration with mockery board/bus: consult posts claim, target receives,
  target testament wakes issuer.
- Integration negative: self-consult transitions to post_failed.
- Integration race: response and timeout race resolves once.
- E2E: Architect consults Librarian and resumes from lifecycle deltas.
- E2E: challenge remains validating after response until inspection passes.
- E2E: Guardian check denial is a testament with policy artifact.

#### Item 7.2: Convert Knowledge Backend Readiness Waits

**Description:** Knowledge readiness should use lifecycle claims and
testaments. Search requests should wait for readiness lifecycle instead of
degrading silently when readiness is pending.

**Examples:**

- Knowledge backend generates/posts readiness claim.
- Backend posts readiness testament when text search/vector graph/document
  DB are ready.
- Search waits for `claim.satisfied` or records readiness timeout as error
  artifact.

**Acceptance Criteria:**

- Readiness state is represented as claim/testament lifecycle.
- Search checks board for satisfied readiness before using backend.
- Missing readiness posts or observes readiness claim and waits on lifecycle
  delta.
- Timeout produces error artifact, not silent degradation.
- Readiness replay prevents duplicate backend initialization.

**Test Cases:**

- Unit: readiness wait predicates match only satisfied readiness lifecycle.
- Integration with mockery backend: readiness testament posts after backend
  ready.
- Integration failure: backend init error posts error artifact and
  validation_errored.
- E2E: first search waits, backend posts readiness, search continues.
- E2E timeout: search produces error artifact and claim failure state.
- E2E replay: satisfied readiness claim avoids redundant wait.

#### Item 7.3: Convert Carry Forward and Recall Skills

**Description:** Carry-forward and recall skills should publish and consume
claims/testaments/artifacts through lifecycle states. Recall should not
consult Archivalist when direct carried-forward testaments satisfy the
request.

**Examples:**

- Architect publishes continuity testament after a turn.
- Next turn recall reads satisfied continuity claims/testaments.
- Cross-session recall waits on durable board or archival ingestion
  lifecycle before falling back to Archivalist consult.

**Acceptance Criteria:**

- Carry-forward publishes generated/posted testaments with artifact refs.
- Recall reads only posted/validated/satisfied lifecycle facts unless asked
  for drafts.
- Recall failure or missing durability is an error artifact or archival
  claim, not silent context loss.
- Archivalist consult is used only when direct lifecycle evidence is absent
  or semantically insufficient.

**Test Cases:**

- Unit: recall filter excludes generated-only drafts by default.
- Integration with mockery board reader: recall returns ordered testament
  chain by relations and lifecycle status.
- Integration negative: missing durable board creates archival claim or
  error artifact.
- E2E: phase-two Architect planning reuses prior continuity testament and
  avoids duplicate consult.
- E2E cross-session: recall walks back configured session count and stops at
  boundary.

### Phase 8: UI Projection and Chat Rendering

Phase 8 makes UI a projection of lifecycle facts and removes inferred
inter-agent branch heuristics.

#### Item 8.1: Render Claim and Testament Lifecycle Directly

**Description:** Chat and agent panels render lifecycle deltas and board refs
instead of manufacturing rows from tool args or legacy metadata.

**Examples:**

- `claim.generated` appears as draft/planned work when user-facing.
- `claim.posted` opens a work row.
- `claim.received` shows target acknowledged.
- `claim.progressed` updates text/spinner only.
- `claim.satisfied` closes row successfully.
- `claim.validation_failed` closes row with failed validation details.

**Acceptance Criteria:**

- UI rows are keyed by claim/testament refs, not display strings.
- Generated states do not show live target work.
- Progress cannot overwrite terminal state.
- Testament rows render artifact headers and error artifacts.
- Terminal rows freeze elapsed time and spinner state.
- Duplicate terminal deltas are idempotent.

**Test Cases:**

- Unit: renderer maps every lifecycle status to display state.
- Unit: progress after terminal state is ignored.
- Integration with mockery bridge: lifecycle deltas mutate chat model
  exactly once.
- Integration negative: metadata-only inter-agent event cannot create peer
  row without claim ref.
- E2E: consult row opens on posted, acknowledges receipt, closes on
  satisfied or configured response receipt.
- E2E: spinner stops on terminal failure and success.
- E2E replay: UI rebuilds identical tree from durable lifecycle deltas.

#### Item 8.2: Remove Inferred Branch Projection and Completion Heuristics

**Description:** Delete or hard-disable UI paths that infer consult/challenge
rows from tool arguments, orphan heuristics, elapsed time, sibling
completion, or synchronous tool return.

**Examples:**

- No `InterAgentToolEvent` row unless backed by a claim ref.
- No orphan `?` completion based on completed siblings.
- No "Complete" status while streaming spinner remains active.

**Acceptance Criteria:**

- Inter-agent branches require claim/testament refs.
- Tool rows may show expected tool attempts, but cannot substitute for
  claim lifecycle.
- Orphan heuristics do not close or mark peer rows.
- UI terminal state comes only from lifecycle terminal deltas.

**Test Cases:**

- Unit/static: inferred branch creation paths are absent.
- Integration with mockery bridge: tool metadata without claim ref renders
  as a tool row only, not peer branch.
- Integration race: child tool completion before parent lifecycle does not
  close parent.
- E2E: self-consult metadata cannot render self row without claim graph.
- E2E: long-running consult displays actual lifecycle state, not orphan
  heuristic.

#### Item 8.3: Render User-Facing Artifacts From Posted Testaments

**Description:** User-facing artifacts such as plans must appear in chat
from posted/received/validated testament lifecycle, not inside approval
dialog internals alone.

**Examples:**

- Architect plan testament is posted with a plan artifact.
- Chat renders the plan artifact.
- Approval dialog references the same artifact ID.

**Acceptance Criteria:**

- User-facing artifacts are attached to posted testaments.
- Approval UI and chat use the same artifact refs.
- Generated plan drafts do not wake implementation agents.
- Plan approval posts the corresponding work claims.

**Test Cases:**

- Unit: plan artifact classification is user-visible.
- Integration with mockery UI bridge: posted plan testament creates chat
  artifact entry and approval dialog reference.
- Integration negative: approval dialog cannot show artifact absent from
  board.
- E2E: Architect plan appears in chat after agent completes planning.
- E2E: approving plan posts generated implementation claims.

### Phase 9: Durable Replay, Concurrency, and Performance Hardening

Phase 9 proves the lifecycle is safe under production conditions.

#### Item 9.1: Durable Replay and Idempotent Restart

**Description:** Replay must reconstruct board state, receiver dedup state,
UI projection, and pending continuations without duplicate work.

**Examples:**

- App restarts after `claim.posted` but before `claim.received`; target
  receives once after replay.
- App restarts after `testament.posted`; source/evaluator resumes
  validation once.

**Acceptance Criteria:**

- Board replay reconstructs exact statuses, history, relations, artifacts,
  and sequences.
- Receiver cursor and dedup store recover or reconcile from board state.
- Continuations reconcile pending waits from lifecycle state.
- UI projection rebuilds from lifecycle deltas and board refs.
- Replay is bounded and does not leak goroutines.

**Test Cases:**

- Unit: replay reducer is deterministic for shuffled same-sequence-invalid
  inputs by rejecting invalid order.
- Integration with mockery durable log: crash after commit before publish
  re-publishes missing delta.
- Integration with mockery inbox: replayed posted delta starts work only if
  receipt absent.
- E2E crash/restart at every lifecycle boundary.
- E2E duplicate WAL records dedupe by lifecycle key.
- E2E long session replay remains within memory budget.

#### Item 9.2: Race, Deadlock, and Goroutine Ownership Audits

**Description:** Lifecycle handling must not introduce untracked goroutines,
unbounded queues, lock inversion, or dropped deltas.

**Examples:**

- Board emits deltas after commit without holding write lock through
  receiver callbacks.
- ClaimsInbox workers stop on context cancellation.
- Delta queues are bounded and backpressure is explicit.

**Acceptance Criteria:**

- No board write lock is held while calling external bus, artifact, tool, or
  agent code.
- Every goroutine is owned by a context and wait group.
- Queues have bounded capacity and overflow behavior is explicit.
- Race detector passes for claims, Guide, inbox, UI bridge, and peer skill
  packages.
- Deadlock tests cover lock-order inversions.

**Test Cases:**

- Unit: lock-order tests for board transition and delta publication.
- Integration race: run lifecycle tests with `-race`.
- Integration deadlock: mock bus blocks publish; board remains readable and
  shutdown completes.
- Integration backpressure: bounded queue full produces retry/telemetry, not
  silent drop.
- E2E interrupt during consult wait shuts down all workers.
- E2E high concurrency: many agents post/ack/validate without duplicate
  work or missed terminal states.

#### Item 9.3: Performance and Bounded Resource Use

**Description:** Lifecycle state must be precise without making common paths
too slow or memory-heavy.

**Examples:**

- Claim generation/posting should avoid full board scans.
- Delta filtering should use refs, session, board, action, and receiver UID.
- UI should store compact projections instead of full artifact bodies.

**Acceptance Criteria:**

- Posting, receipt, testament posting, and validation are indexed by ID and
  relation lookups, not broad scans.
- Dedup stores are bounded with safe eviction.
- Artifact bodies are lazy-loaded by ref.
- Benchmarks define budgets for common lifecycle operations.
- Memory usage remains bounded during long sessions.

**Test Cases:**

- Unit benchmark: generate/post/receive/validate operations under target
  allocation budgets.
- Integration benchmark: 1,000 claim lifecycle operations with mock bus and
  artifact store.
- Integration memory: dedup eviction preserves correctness for replayed
  recent deltas.
- E2E soak: long planning session with consults, recalls, validations, and
  interruptions remains responsive.
- E2E edge: many artifacts attached to one testament render by headers
  without loading all bodies.

### Phase 10: Legacy Removal and Contract Enforcement

Phase 10 removes legacy execution paths and adds guardrails so future code
cannot reintroduce duplicate authorities.

#### Item 10.1: Delete Legacy Normal-Work Routing

**Description:** Remove direct normal-work execution paths that bypass
claim/testament lifecycle, including synchronous Guide route execution,
legacy forwarded request processing for work, peer completion heuristics,
and independent consult resolved events.

**Examples:**

- Guide route can generate and post lifecycle records, but cannot directly
  call Architect work handlers.
- `consult_resolved` is absent as a primary event.
- Tool completion for consult-yield is not interpreted as peer response.

**Acceptance Criteria:**

- No normal agent work can execute without posted claim lifecycle.
- No peer completion can occur without posted testament or terminal claim
  lifecycle.
- Removed code paths have replacement lifecycle tests.
- Static checks fail on imports or call sites for removed APIs.

**Test Cases:**

- Unit/static: forbidden symbols are not referenced outside migration tests.
- Integration with mockery Guide bus: no `ForwardedRequest` work messages.
- Integration negative: tool_completed artifact does not complete consult.
- E2E: all top-level, consult, challenge, guardian, and validation flows run
  without legacy route execution.

#### Item 10.2: Add Contract Tests for New Code

**Description:** Add tests and static checks that enforce lifecycle rules for
future features.

**Examples:**

- New lifecycle status requires transition table, delta schema, renderer,
  and replay tests.
- New peer skill must use lifecycle APIs.
- New validation type must classify incomplete, failed, and errored paths.

**Acceptance Criteria:**

- Status enum coverage tests fail when status lacks transition and delta
  coverage.
- Delta action coverage tests fail when lifecycle actions lack topic,
  idempotency, receiver, and renderer definitions.
- Skill registration tests fail when peer-routing skills bypass board
  lifecycle APIs.
- UI coverage tests fail when terminal states are not rendered.

**Test Cases:**

- Unit/static: lifecycle status coverage.
- Unit/static: delta action coverage.
- Unit/static: renderer state coverage.
- Integration with mockery skill registry: peer skills invoke lifecycle API
  mocks, not Guide route mocks.
- E2E contract: representative prompt, consult, challenge, guardian check,
  readiness wait, validation failure, and interruption all complete through
  lifecycle states only.

#### Item 10.3: Update Documentation and Agent Prompt Language

**Description:** Update agent system prompts, skill descriptions, and docs so
agents understand generated vs posted, receipt vs work completion, and
testament/artifact obligations.

**Examples:**

- Architect prompt says generated implementation claims do not start work
  until posted after user approval.
- Librarian prompt says receipt should be acknowledged, work should progress,
  and final output must be a posted testament with artifacts.
- Guide prompt says routing decisions are testaments and routed work claims
  must be posted, not forwarded.

**Acceptance Criteria:**

- Agent prompts mention lifecycle obligations for claims, testaments,
  artifacts, and validation.
- Skill descriptions use lifecycle terms consistently.
- Docs cross-link this lifecycle spec, `CLAIMS.md`, and
  `CLAIMS_AND_DELTAS.md`.
- Prompt changes are covered by behavior tests where possible.

**Test Cases:**

- Unit/static: prompt fixtures contain required lifecycle terms.
- Integration with mockery LLM/tool harness: agents choose lifecycle skills
  for routed, consult, and validation work.
- E2E: clean session toy CLI planning uses recall/generation/posting
  correctly and does not duplicate consults across phases.
- E2E negative: agent attempts self-consult; lifecycle policy rejects before
  work starts and prompt recovery produces correct alternative.
