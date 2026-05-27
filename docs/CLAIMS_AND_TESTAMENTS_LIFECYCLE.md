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
| `claim.validation_errored` | any | The validation process itself failed. |

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

