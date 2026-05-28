# Claims and Deltas

This document defines the simplified claims and deltas contract for Sylk.
It is intended to replace ad hoc peer routing, compatibility completion
events, UI-only lifecycle guesses, and underspecified board notifications
with one coherent mechanic:

```text
agent writes claim/testament/validation
  -> board commits durable state
  -> board emits canonical delta
  -> Guide event bus transports delta
  -> agents, UI, validators, and runtime continuations react to delta
```

The board owns truth. The Guide event bus owns delivery. Agents react to
deltas. Claims and testaments carry evidence and instructions. Deltas are
the committed facts that drive harness behavior.

## 1. Goals

The design has five goals.

1. Keep all agent-agent and agent-user message delivery on the Guide event
   bus.
2. Make the claims board the only source of workflow truth.
3. Make deltas explicit enough that receivers know what they are allowed
   and expected to do.
4. Remove duplicate peer-completion systems such as direct synchronous
   Guide routing for `consult_peer` after a claim has already been posted.
5. Let claims and validations express expected tool work so agents can
   perform and verify work deterministically while still retaining agentic
   judgment.

## 2. Non-Goals

This design does not introduce a second event bus, a second board, a new
agent messaging plane, or a replacement for Guide transport.

This design does not make deltas executable scripts. Deltas may carry
expected tool call specifications, but receivers must validate authority,
tool availability, arguments, deadlines, and local policy before executing
anything.

This design does not make claims optional. Peer work is represented by
claims, responses by testaments and artifacts, and verification by
validations. Legacy skill names may remain, but their implementation must
collapse to these primitives.

## 3. Core Terms

### Claim

A claim is directed work or an assertion requiring evidence. A claim may
name one or more directed recipients through relations. A receiving agent
responds by submitting one or more testaments linked to the claim.

Claims may include expected tool calls describing work the subject should
perform or consider performing.

### Testament

A testament is an immutable response to a claim. It carries a conclusion,
context, and artifacts. Errors are artifacts. A failed tool, missing file,
denied permission, timeout, or impossible request is represented by an
error artifact inside a testament, not by silently dropping the workflow.

### Artifact

An artifact is evidence attached to a testament. Artifacts may be content,
pointers, logs, tool outputs, diffs, code references, error reports,
diagnostics, or user-facing artifacts.

### Validation

A validation is a verification requirement on a claim. Each validation may
include expected tool calls describing how the evaluator should verify
the claim's testaments and artifacts.

Receipt validations are mechanical: a linked testament arriving is the
proof of receipt. Other validation types are agentic: the evaluator
uses tools, reads artifacts, reasons against the quality bar, and records
a verdict.

### Delta

A delta is an immutable fact emitted after a board mutation commits. It is
not a hint, not a command, and not a UI decoration. A receiver may act on
the delta without first querying the board. The board is queried only for
extra graph context beyond what the delta carries.

### Guide Event Bus

The Guide event bus is the transport for claims deltas and any user-visible
or agent-visible messages derived from them. The bus does not own workflow
truth. It delivers committed facts and runtime messages.

## 4. System Invariants

These invariants define the design.

1. Board mutations are durable before deltas are published.
2. Deltas are published through the Guide event bus.
3. Deltas are immutable.
4. Every delta has a stable idempotency key.
5. Every receiver deduplicates by delta key and sequence.
6. Agent work is triggered by `claim.directed`.
7. Agent responses are represented by `testament.submitted`.
8. Verification is represented by `validation.evaluated`.
9. Workflow closure is represented by `claim.transitioned`.
10. Progress is represented by `claim.progressed` only when it is useful.
11. Progress never completes work.
12. Context text never completes work.
13. UI rows close from testament, validation, or claim transition deltas.
14. Synchronous Guide route responses are not peer-work completion signals.
15. `consult_resolved` is compatibility glue only, not a primary semantic
    event.
16. Board phase changes are aggregate projections, not primary workflow
    events.
17. Expected tool calls are instructions, not authority bypasses.
18. Tool execution failures become artifacts for testaments.
19. Claims and validations must never target an agent by display string
    alone when canonical identity is available.
20. A receiving agent must never be woken by a self-targeted claim unless
    the claim explicitly represents a legitimate self-transfer such as a
    handoff.

## 5. Actions, Not Kinds

The delta's top-level verb is called `action`.

Do not use `kind` for the delta action. Do not use `action_kind` for the
parent claim action. The parent claim action is context about the claim,
not the delta action itself.

Example:

```json
{
  "action": "testament.submitted",
  "context": {
    "claim": {
      "action": "consultation"
    }
  }
}
```

Here:

- `testament.submitted` is the delta action.
- `consultation` is the claim action that caused the testament to matter.

## 6. Canonical Delta Envelope

Every canonical delta uses the same envelope.

```json
{
  "schema": "sylk.claims.delta.v1",
  "action": "claim.directed",
  "delta_id": "delta_01J...",
  "delta_key": "claim.directed:s1:b1:c1:subject:librarian_uid",
  "session_id": "s1",
  "board_id": "b1",
  "sequence": 42,
  "occurred_at": "2026-05-26T12:00:00Z",
  "actor": {
    "uid": "agent_uid",
    "namespace": "session_namespace",
    "pod": "architect",
    "name": "architect",
    "type": "architect",
    "generation": 1,
    "model": "claude-sonnet-4.6"
  },
  "delivery": {
    "to": [
      {
        "uid": "target_uid",
        "namespace": "session_namespace",
        "pod": "knowledge",
        "name": "librarian",
        "type": "librarian",
        "generation": 1,
        "model": "claude-sonnet-4.6"
      }
    ],
    "relationship": "subject"
  },
  "refs": [
    { "role": "action", "type": "action", "id": "a1" },
    { "role": "claim", "type": "claim", "id": "c1" }
  ],
  "context": {}
}
```

### Required Envelope Fields

`schema` is the versioned delta schema identifier.

`action` is the delta action. It is a closed enum for a given schema
version.

`delta_id` is a globally unique event ID.

`delta_key` is a stable idempotency key derived from the committed object
and receiver-relevant dimensions.

`session_id` identifies the session partition.

`board_id` identifies the board that committed the mutation.

`sequence` is the board sequence at which the mutation committed.

`occurred_at` is UTC commit time.

`actor` is the canonical agent or system identity that caused the
mutation.

`delivery` identifies intended recipients when the event has directed
delivery semantics.

`refs` contains object references. Deltas must not assume a strict
one-to-one relationship with claims, testaments, artifacts, or validations.

`context` is the action-specific compact payload.

## 7. Canonical Participant Reference

Participant references must follow the identity model. They must not be
plain display names when canonical identity is available. Every
participant — agent, service, system, or external — uses the same
reference shape. The `category` field distinguishes the four
participant categories per `docs/CLAIMS_AND_INFRASTRUCTURE.md` §3.2.
During migration, the historical name `AgentRef` is preserved as a
backward-compatibility alias that produces a `ParticipantRef` with
`category: agent`.

```json
{
  "uid": "018f...",
  "namespace": "session_namespace",
  "pod": "knowledge",
  "name": "librarian",
  "type": "librarian",
  "category": "agent",
  "generation": 1,
  "model": "claude-sonnet-4.6",
  "task": {
    "uid": "task_uid",
    "display_id": "default",
    "pipeline_id": "pipeline_1"
  },
  "labels": {
    "scope": "knowledge"
  }
}
```

Minimum fields are:

- `uid`
- `namespace`
- `pod`
- `name`
- `type`
- `category` (one of `agent`, `service`, `system`, `external`)
- `generation`
- `model` (for agents: model name; for services: binary version; for
  system: process binary version; for external: empty)

Services additionally carry `scope_keys` (the deterministic inputs to
service-UID derivation per `docs/CLAIMS_AND_INFRASTRUCTURE.md` §7.1).
External participants carry `adapter_id`. See
`docs/CLAIMS_AND_INFRASTRUCTURE.md` §5.2 for the complete
`ParticipantRef` schema.

When canonical identity is unavailable, the delta may carry a degraded
participant ref:

```json
{
  "type": "librarian",
  "unresolved": true,
  "resolution_reason": "legacy claim relation carried only agent type"
}
```

Receivers must treat degraded refs as non-authoritative for identity and
must resolve them through the identity registry before starting work.
For services, identity resolution uses `DeriveServiceUID(type, scope_keys)`
per `docs/CLAIMS_AND_INFRASTRUCTURE.md` §7.1; the result is
deterministic across process restarts.

## 8. Object References

Deltas use `refs` rather than scalar `claim_id`, `testament_id`, or
`validation_id` fields.

```json
[
  { "role": "action", "type": "action", "id": "a1" },
  { "role": "claim", "type": "claim", "id": "c1" },
  { "role": "testament", "type": "testament", "id": "t1" },
  { "role": "artifact", "type": "artifact", "id": "art1" },
  { "role": "validation", "type": "validation", "id": "v1" }
]
```

Rules:

1. `refs` is ordered by causal importance, not by object type.
2. A delta may reference multiple claims.
3. A delta may reference multiple testaments.
4. A delta may reference multiple artifacts.
5. Receivers must not infer object cardinality from the delta action.
6. If the receiver needs full object content, it queries the board by ref.

## 9. Canonical Delta Actions

The core delta action set is intentionally small.

### claim.directed

Emitted when a claim is committed and directed at a recipient.

Primary receiver: claim subject, evaluator, reviewer, remediator, or other
agent named by a directed relation.

Required context:

```json
{
  "claim": {
    "id": "c1",
    "action": "consultation",
    "title": "Consult librarian",
    "description": "Determine whether a Python project already exists.",
    "status": "pending",
    "scope": [
      { "kind": "workspace", "key": "." }
    ],
    "validations": [
      {
        "id": "v1",
        "type": "receipt",
        "required": true,
        "description": "Peer responds to consultation",
        "quality_bar": "response.received"
      }
    ],
    "expected_tool_calls": []
  }
}
```

Receiver behavior:

1. Deduplicate by `delta_key`.
2. Resolve canonical identity for `delivery.to`.
3. Reject or ignore if this receiver is not the intended recipient.
4. Start work only if policy allows the claim action.
5. Use expected tool calls as work instructions when present.
6. Submit a testament with artifacts when work finishes, fails, or is
   impossible.

### claim.progressed

Emitted when non-terminal claim progress changes.

This replaces ambiguous `claim_context` terminology. It is optional and
never authoritative for completion.

Required context:

```json
{
  "claim": {
    "id": "c1",
    "status": "in_progress"
  },
  "progress": {
    "state": "awaiting_peer_response",
    "message": "Awaiting librarian response",
    "transition": 3
  }
}
```

Receiver behavior:

1. UI may update status text.
2. Observers may record telemetry.
3. Agents must not treat this as completion.
4. Continuations must not resume from this action.

### testament.submitted

Emitted when one or more testaments are committed.

Required context:

```json
{
  "claim": {
    "id": "c1",
    "action": "consultation",
    "status": "testified"
  },
  "testaments": [
    {
      "id": "t1",
      "verdict": "work_complete",
      "confidence": "high",
      "context": "No Python project structure exists.",
      "artifacts": [
        {
          "id": "art1",
          "kind": "response_text",
          "content_hash": "sha256:...",
          "ephemeral": false
        },
        {
          "id": "art2",
          "kind": "workspace_observation",
          "content_hash": "sha256:...",
          "ephemeral": false
        }
      ]
    }
  ]
}
```

Receiver behavior:

1. Issuer consumes the response.
2. Validators inspect artifacts and validation expected tool calls.
3. Receipt validations may already be passed by the board.
4. UI may close the responding peer row.
5. Continuations waiting on the claim may resume from this action.
6. Receivers query the board for full artifact content if needed.

### validation.evaluated

Emitted when a validation verdict is committed.

Required context:

```json
{
  "claim": {
    "id": "c1",
    "status": "testified"
  },
  "validation": {
    "id": "v1",
    "type": "test",
    "status": "passed",
    "required": true,
    "reason": "pytest passed",
    "remaining_required": 0
  }
}
```

Receiver behavior:

1. Issuer and subject update workflow.
2. Failed validations may cause remediation claims.
3. UI updates validation status.
4. This action may imply claim closure only if the same committed
   transaction also emits or includes a `claim.transitioned` fact.

### claim.transitioned

Emitted when a claim lifecycle status changes.

Required context:

```json
{
  "claim": {
    "id": "c1",
    "action": "consultation",
    "from_status": "testified",
    "to_status": "accepted",
    "reason": "all required validations passed"
  }
}
```

Receiver behavior:

1. Treat this as the authoritative lifecycle state.
2. UI closes rows whose lifecycle depends on this claim.
3. Continuations waiting for terminal claim state resume or fail.
4. Archival and carry-forward logic may index the final state.

### Expanded Lifecycle Actions

The five canonical verbs above are the coarse contract. Per
`docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md` and
`docs/ARTIFACTS_AND_VALIDATIONS.md`, the full lifecycle uses an
expanded set of action names that map onto the coarse verbs as
projections:

**Claim lifecycle actions** (17, per LIFECYCLE §7): `claim.generated`,
`claim.generation_failed`, `claim.posted`, `claim.post_failed`,
`claim.received`, `claim.receipt_failed`, `claim.progressed`,
`claim.progress_failed`, `claim.testament_generated`,
`claim.testament_generation_failed`, `claim.testament_acknowledged`,
`claim.testament_acknowledgement_failed`, `claim.validating`,
`claim.satisfied`, `claim.validation_incomplete`,
`claim.validation_failed`, `claim.validation_errored`.

**Testament lifecycle actions** (7, per LIFECYCLE §7):
`testament.generated`, `testament.posted`, `testament.received`,
`testament.validating`, `testament.validation_incomplete`,
`testament.validation_failed`, `testament.validation_errored`,
`testament.validated`.

**Artifact lifecycle actions** (8, per ARTIFACTS_AND_VALIDATIONS §12.1):
`artifact.generated`, `artifact.generation_failed`,
`artifact.received`, `artifact.receipt_failed`, `artifact.attached`,
`artifact.validating`, `artifact.validation_failed`,
`artifact.validated`.

**Validation lifecycle actions** (10, per ARTIFACTS_AND_VALIDATIONS §12.2):
`validation.ready`, `validation.validating`,
`validation.validation_failed`, `validation.validation_failed_not_required`,
`validation.errored`, `validation.errored_not_required`,
`validation.validating_quality_bar`,
`validation.quality_bar_validation_failed`,
`validation.quality_bar_validation_failed_not_required`,
`validation.validated`.

Receivers that need only the coarse contract may subscribe to the five
verbs above. Receivers that need finer-grained lifecycle state (UI
bridges, per-artifact orchestrators, validator dispatchers,
continuation stores waiting on specific terminal states) subscribe to
the expanded action set. Both sets are emitted from the same board
mutations; the coarse verbs are projections.

## 10. Removed or Downgraded Delta Concepts

### phase

The current code has `BoardPhase`, but board phase is aggregate state. It
is derivable from claims and validations. It should not be a primary
agent-routing primitive.

If retained, it should be a projection event used by UI/analytics only,
not a workflow driver.

### claim_context

This should become `claim.progressed`. The old name suggests a separate
context system. The simplified name makes the semantics explicit:
progress only, not completion.

### testament_context

This should not be a core delta action. In-flight testament narration is
UI progress. If needed, it can be represented as `claim.progressed` or as
artifact/testament draft telemetry that does not wake agents.

### consult_resolved

This should not be a primary semantic event. A consult resolves when the
consultation claim receives a linked testament and then transitions through
receipt validation or claim status.

During migration, `consult_resolved` may be derived from
`testament.submitted` or `claim.transitioned` for compatibility with the
continuation store. It must not remain an independent source of truth.

## 11. Expected Tool Calls

Claims and validations may carry expected tool call specifications.

Claims use expected tool calls to describe work expected from the subject.
Validations use expected tool calls to describe verification expected from
the evaluator. When a validation's expected tool maps to a registered
typed validator handler (per `docs/ARTIFACTS_AND_VALIDATIONS.md` §8),
the tool name resolves to a `ValidatorID` and the dispatcher invokes
the typed handler directly rather than going through the agent's
tool-loop layer. For agentic validations with non-empty quality bars,
the expected tools describe the work the agent should perform during
its quality-bar assessment phase.

Expected tool calls are durable skill invocation specs, not provider
tool-call messages.

```json
{
  "id": "etc_1",
  "tool": "workspace_read",
  "arguments": {
    "op": "glob",
    "path": ".",
    "pattern": "**"
  },
  "purpose": "Find existing Python project structure before planning.",
  "required": true,
  "produces_artifacts": ["workspace_observation"],
  "timeout_seconds": 30,
  "policy": {
    "allow_agent_substitution": true,
    "requires_user_approval": false
  }
}
```

### Expected Tool Call Fields

`id` is optional at input and stamped by the board if missing.

`tool` is the Sylk skill/tool name.

`arguments` is structured JSON for that tool.

`purpose` explains why the tool is expected.

`required` means failure to attempt or justify refusal should be visible
in the testament or validation result.

`produces_artifacts` lists expected artifact kinds.

`timeout_seconds` is the maximum expected execution time.

`policy` carries local execution hints. It never bypasses runtime policy.

### Claim-Level Expected Tool Calls

Claim-level expected tools guide work.

Example claim excerpt:

```json
{
  "title": "Determine project shape",
  "description": "Inspect the workspace and identify existing Python packaging.",
  "expected_tool_calls": [
    {
      "tool": "workspace_read",
      "arguments": { "op": "glob", "path": ".", "pattern": "**" },
      "purpose": "Discover project files.",
      "required": true,
      "produces_artifacts": ["workspace_observation"]
    }
  ]
}
```

### Validation-Level Expected Tool Calls

Validation-level expected tools guide verification.

Example validation excerpt:

```json
{
  "description": "Verify CLI behavior",
  "quality_bar": "Command exits 0 and stdout contains the expected greeting.",
  "type": "test",
  "required": true,
  "expected_tool_calls": [
    {
      "tool": "workspace_read",
      "arguments": { "op": "read", "path": "tests/test_cli.py" },
      "purpose": "Inspect the test assertions.",
      "required": true,
      "produces_artifacts": ["code_reference"]
    },
    {
      "tool": "run_tests",
      "arguments": { "command": "pytest tests/test_cli.py" },
      "purpose": "Execute the validation.",
      "required": true,
      "produces_artifacts": ["test_output"]
    }
  ]
}
```

### Execution Rules

1. The harness validates every expected tool call before execution.
2. The receiver may execute allowed expected tools deterministically.
3. The receiver may choose agentic execution when direct deterministic
   execution is not safe or not expressive enough.
4. Tool failures become artifacts.
5. Missing required expected tools must be explained in the testament or
   validation verdict.
6. Expected tools must not call peer-routing skills unless the claim
   explicitly authorizes delegation.
7. Expected tools must be included in deltas as compact specs so receivers
   can act without a board read.

## 12. Error Semantics

Errors are evidence.

When work fails, the receiving agent submits a testament with error
artifacts. When validation fails, the evaluator attaches or references
error artifacts and records `validation.evaluated` with a failed verdict.

Examples of error artifact kinds:

- `error`
- `error_trace`
- `error_diagnostic`
- `tool_timeout`
- `permission_denied`
- `policy_denied`
- `missing_dependency`
- `invalid_expected_tool_call`

Infrastructure errors that prevent board commit or bus publication are
system failures. Work errors after the claim is received are artifacts.

## 13. Delivery Topics

The Guide event bus should carry canonical delta messages with topic
grammar derived from delta action and delivery dimensions.

Recommended topics:

```text
claims.<session_id>.agent.<agent_uid>.<action>
claims.<session_id>.service.<service_uid>.<action>
claims.<session_id>.system.<system_uid>.<action>
claims.<session_id>.external.<external_uid>.<action>
claims.<session_id>.claim.<claim_id>.<action>
claims.<session_id>.testament.<testament_id>.<action>
claims.<session_id>.artifact.<artifact_id>.<action>
claims.<session_id>.validation.<validation_id>.<action>
claims.<session_id>.board.<board_id>.<action>
```

The per-participant topic patterns use the canonical UID for each
participant category. Service UIDs are deterministically derived from
(service_type, scope_keys) per `docs/CLAIMS_AND_INFRASTRUCTURE.md`
§7.1; system and external UIDs use their respective derivation
helpers. Per-claim, per-testament, per-artifact, and per-validation
topics route lifecycle observers (UI bridges, validator dispatchers,
continuation stores) to the specific entities they wait on.

Rules:

1. Directed agent work is delivered to the agent topic.
2. Object lifecycle observers subscribe to claim or validation topics.
3. UI and archival observers may subscribe to board/session patterns.
4. The bus message payload is the canonical delta envelope.
5. Legacy delta payloads may be bridged during migration.

## 14. Consult and Challenge Semantics

`consult_peer` and `challenge_peer` are aliases over claim posting.

`consult_peer` posts a claim with action `consultation`.

`challenge_peer` posts a claim with action `challenge`.

Both produce `claim.directed`. The target replies with
`testament.submitted`. Receipt-only consults may accept automatically.
Inspection challenges may require `validation.evaluated` before final
acceptance.

The legacy shape must be removed:

```text
post claim
also call Guide RouteSync
also wait for direct Guide response
```

The correct shape is:

```text
post claim
wait or yield on canonical deltas
resume from testament.submitted or claim.transitioned
```

Guide remains the transport because deltas move over the Guide event bus.

## 15. User-Facing Messages

Agent-user communication also flows through the Guide event bus.

User-facing artifacts should be testaments or artifacts with presentation
metadata. The UI renders user-facing artifacts from canonical board state
or from deltas referencing that state.

Plans are user-facing artifacts. A planning agent should submit a
testament with a plan artifact marked for user presentation. The approval
dialog may reference the plan, but it must not be the only place the plan
appears.

## 16. Idempotency and Ordering

Every receiver must maintain a bounded dedup store keyed by `delta_key`.

Rules:

1. Duplicate delivery of the same delta is safe.
2. Replay of durable deltas is safe.
3. Deltas with lower sequence than the receiver's committed cursor may be
   ignored if their key has been processed.
4. Gaps in sequence require reconciliation by querying the board.
5. Processing must not hold board locks while publishing or while running
   agent/tool code.
6. Bus handlers must offload long work to the agent goroutine scope.
7. Dead receivers reconcile by replaying durable board deltas.

## 17. Phased Implementation Plan

### Phase 1: Canonical Schema and Types

Phase 1 creates the durable vocabulary. It does not remove legacy behavior.

#### Item 1.1: Define Canonical Delta Envelope

Description:

Add a canonical delta envelope type in `core/claims` with fields:
`schema`, `action`, `delta_id`, `delta_key`, `session_id`, `board_id`,
`sequence`, `occurred_at`, `actor`, `delivery`, `refs`, and `context`.
Add a closed enum for core delta actions:
`claim.directed`, `claim.progressed`, `testament.submitted`,
`validation.evaluated`, and `claim.transitioned`.

Acceptance criteria:

- The envelope can represent every current `InboxDelta`, `TestamentDelta`,
  `ValidationDelta`, `ClaimStatusDelta`, and useful progress update.
- The envelope does not use `kind` as the top-level semantic verb.
- The envelope does not use `action_kind`.
- The parent claim's action is represented as `context.claim.action`.
- The envelope supports multiple refs.
- `delta_key` is deterministic for the same committed fact.
- `delta_id` is unique per emitted delta.
- JSON encoding and decoding are stable.
- Unknown future fields are ignored by older decoders.
- Unknown action values are rejected by strict handlers and ignored by
  tolerant observer handlers.

Unit tests:

- Encode/decode round trip for each canonical action.
- Missing required fields fails validation.
- Unknown `action` fails strict validation.
- `delta_key` generation is deterministic.
- Two deltas with same action and refs but different recipients get
  different keys when delivery differs.
- Two replayed deltas with same committed object produce same key.
- `refs` supports multiple claims/testaments/artifacts.
- Context payload preserves nested JSON.

Integration tests using vektra/mockery:

- Mock a `DeltaPublisher` and assert exact envelope fields published from
  synthetic board mutations.
- Mock a strict receiver and verify unknown action rejection.
- Mock a tolerant UI receiver and verify unknown action is logged but does
  not abort the bus handler.

E2E tests:

- Post a claim in a real session and observe one `claim.directed` envelope
  on the Guide bus.
- Restart/replay the session and verify the replayed envelope has the same
  `delta_key`.
- Inject duplicate delivery and verify the agent processes it once.
- Simulate dropped bus delivery and verify reconciliation from durable
  board state produces the same delta.

Failure, race, and deadlock tests:

- Publisher returns error after board commit: board state remains committed
  and durable replay can recover the delta.
- Concurrent claim posts produce monotonically increasing sequences.
- Bus handler that blocks does not hold the board mutex.
- Receiver dedup under concurrent duplicate delivery processes once.

#### Item 1.2: Define Canonical AgentRef

Description:

Create a claims-level `AgentRef` that mirrors canonical identity without
requiring claims to import UI or container packages. It must represent UID,
namespace, pod, name, type, category, generation, model, optional task
reference, labels, and degraded unresolved refs.

Acceptance criteria:

- `AgentRef` can be built from `identity.AgentIdentity`.
- `AgentRef` can represent legacy refs containing only an agent type.
- Deltas always use `actor` and `delivery.to` agent refs, not scalar
  `issuer_agent_id` or `subject_agent_id`.
- Degraded refs are explicit with `unresolved=true`.
- Receivers can distinguish canonical identity from display name.
- Agent type is present for policy and UI display.
- Agent UID is present for exact routing when available.

Unit tests:

- Build `AgentRef` from canonical identity.
- Build degraded `AgentRef` from legacy string.
- JSON round trip preserves identity fields.
- Missing UID with `unresolved=false` fails validation.
- Type mismatch between UID-resolved identity and claimed type fails
  resolver validation.

Integration tests using vektra/mockery:

- Mock identity resolver and verify legacy type refs are resolved before
  agent wakeup.
- Mock resolver failure and verify receiver rejects wakeup with diagnostic
  artifact or log.
- Mock replica identity and verify generation/model are preserved.

E2E tests:

- Architect posts a consult to Librarian and the delta routes by canonical
  Librarian identity.
- A self-targeted degraded ref does not wake the issuer unless it is an
  explicit handoff.
- A stale replica generation does not receive work intended for the new
  generation.

Failure, race, and deadlock tests:

- Identity resolver timeout produces no agent wakeup and no goroutine leak.
- Concurrent identity rotations do not misdeliver deltas.
- Missing identity registry degrades safely without executing tools.

#### Item 1.3: Add Expected Tool Call Specs

Description:

Add `ExpectedToolCall` to claims. Add `ExpectedToolCalls` to `Claim` and
to `Validation`. The spec is a Sylk skill invocation contract, not a
provider tool call. It includes tool name, structured arguments, purpose,
required flag, produced artifact kinds, timeout, and policy hints.

Acceptance criteria:

- Claims can carry work expected tool calls.
- Validations can carry verification expected tool calls.
- Specs are JSON serializable and durable.
- Specs reject provider-specific fields such as raw `tool_call_id`.
- Specs validate tool name and argument shape when a registry is present.
- Required expected tools are visible in `claim.directed` and
  `testament.submitted` or validation-triggering deltas.
- Expected tool failure is representable as artifact evidence.

Unit tests:

- Empty expected tools are allowed.
- Required expected tool without tool name fails validation.
- Invalid JSON arguments fail validation.
- Provider-only tool-call fields are rejected.
- `produces_artifacts` preserves order and removes empty values.
- Validation expected tools serialize independently from claim expected
  tools.

Integration tests using vektra/mockery:

- Mock skill registry and verify expected tool call validation accepts
  known skills and rejects unknown skills.
- Mock policy engine and verify denied expected tools are not executed.
- Mock artifact sink and verify failed expected tool emits error artifact.

E2E tests:

- Claim directed to Librarian contains expected `workspace_read`; Librarian
  executes it and submits a workspace observation artifact.
- Validation contains expected `run_tests`; evaluator runs it and records a
  test output artifact before evaluating validation.
- Unknown expected tool produces error artifact and failed validation,
  not a hung workflow.

Failure, race, and deadlock tests:

- Expected tool timeout records `tool_timeout` artifact.
- Two expected tools with same ID fail validation.
- Parallel expected tools cannot mutate shared accumulator unsafely.
- Policy denial does not deadlock validation.

### Phase 2: Board Emission and Guide Bus Transport

Phase 2 makes canonical deltas flow over the existing Guide event bus.

#### Item 2.1: Emit Canonical Deltas From Board Mutations

Description:

Extend the board amplifier so each committed board mutation builds and
publishes canonical delta envelopes. Legacy delta structs may still be
emitted during migration, but canonical envelopes must be the measured
contract.

Acceptance criteria:

- `PostAction` emits `claim.directed` for each directed recipient.
- `UpdateClaimProgress` emits `claim.progressed`.
- `SubmitTestaments` emits `testament.submitted`.
- `EvaluateValidation` emits `validation.evaluated`.
- Claim status changes emit `claim.transitioned`.
- Canonical deltas are emitted after durable commit.
- Canonical deltas are transported through the Guide event bus adapter.
- Publication never occurs while holding the board lock.
- System-internal actions do not wake agents.

Unit tests:

- Each board mutation emits the expected canonical action.
- System-internal actions do not emit `claim.directed`.
- Receipt validation auto-pass results in correct follow-up transition.
- Multi-recipient claim emits one directed delta per recipient.
- Claim status transition delta includes from/to status.

Integration tests using vektra/mockery:

- Mock Guide bus publisher and assert topics plus payloads.
- Mock failing publisher and verify durable commit remains intact.
- Mock projector and verify projection does not alter delta payload.
- Mock subscriber backpressure and verify board mutation returns according
  to configured policy without holding locks.

E2E tests:

- Start TUI session, issue prompt, observe canonical deltas on Guide bus.
- Kill process after board commit but before bus publish, restart, replay,
  and observe missing delta recovered.
- Submit testament with artifacts and verify issuer and UI receive the
  same canonical `testament.submitted` delta.

Failure, race, and deadlock tests:

- Concurrent post/testament/evaluate operations preserve sequence order.
- Subscriber panic is recovered and does not corrupt board state.
- Bus publish timeout does not hold board mutex.
- Replay under duplicate WAL records deduplicates by delta key.

#### Item 2.2: Define Topics and Subscriptions

Description:

Add canonical topic helpers for agent, claim, validation, and board delta
delivery. Wire ClaimsInbox to subscribe to canonical topics while retaining
legacy subscriptions during migration.

Acceptance criteria:

- Directed agent work routes to `claims.<session>.agent.<uid>.<action>`.
- Claim lifecycle observers can subscribe by claim ID.
- Validation observers can subscribe by validation ID.
- UI can subscribe by board/session.
- Legacy topic helpers are covered by compatibility tests.
- No broad firehose is required for normal agents.

Unit tests:

- Topic builders normalize illegal topic characters.
- Wildcard patterns match intended topics only.
- Agent topic uses UID when available.
- Legacy type-only route cannot spoof UID route.

Integration tests using vektra/mockery:

- Mock bus subscription and verify ClaimsInbox subscribes to narrow
  patterns.
- Mock wildcard UI subscriber and verify it receives all canonical deltas.
- Mock agent subscriber and verify it does not receive another agent's
  directed work.

E2E tests:

- Architect consults Librarian; only Librarian receives `claim.directed`.
- Inspector subscription receives testified/validation deltas but not
  unrelated directed work.
- UI receives progress and terminal lifecycle events for rendering.

Failure, race, and deadlock tests:

- Subscription drop counter increments under overflow.
- Overflow of low-priority UI progress does not drop directed work first.
- Agent restart resubscribes without duplicate processing.
- Unsubscribe during publish does not panic or deadlock.

### Phase 3: Agent Intake and Peer Workflow Simplification

Phase 3 removes peer routing duplication and makes agents consume canonical
deltas.

#### Item 3.1: Make ClaimsInbox Consume Canonical Deltas

Description:

Update ClaimsInbox to resolve canonical delta envelopes into graph entry
points. `claim.directed` wakes the recipient. `testament.submitted`,
`validation.evaluated`, and `claim.transitioned` resolve explicit
expectations. `claim.progressed` updates observers but does not wake agent
inference unless explicitly configured.

Acceptance criteria:

- `claim.directed` becomes the only normal peer-work activation.
- Expectations register against claim IDs and desired canonical actions.
- Testament and terminal claim deltas can resolve continuations.
- Progress deltas do not trigger tool loops.
- Dedup works for replayed canonical deltas.
- Legacy deltas can be adapted to canonical envelopes during migration.

Unit tests:

- `claim.directed` resolves to claim graph entry.
- `testament.submitted` resolves registered expectation.
- `claim.transitioned` resolves terminal expectation.
- `claim.progressed` does not call ProcessEntry by default.
- Duplicate canonical delta is ignored.
- Legacy delta adapter emits equivalent graph entry.

Integration tests using vektra/mockery:

- Mock ProcessEntry and verify exact invocations per delta action.
- Mock continuation store and verify testament/terminal deltas wake it.
- Mock board reader and verify no board query is performed when delta has
  enough context.
- Mock board reader and verify board query happens only for requested
  adjacent context.

E2E tests:

- Librarian receives a consultation entirely from `claim.directed`, works
  it, and submits a testament.
- Architect receives Librarian testament from `testament.submitted` and
  continues planning without direct Guide route response.
- Duplicate bus delivery does not cause duplicate LLM turns.

Failure, race, and deadlock tests:

- Delta arrives before expectation registration and is reconciled by claim
  lookup or orphan buffer.
- Expectation expires and the eventual testament records late arrival
  without hanging.
- ProcessEntry panic is contained by goroutine scope.
- ClaimsInbox close during delivery does not leak goroutines.

#### Item 3.2: Retire Direct RouteSync for Consult and Challenge

Description:

Change `consult_peer` and `challenge_peer` so they post claims and then
wait/yield on canonical claim/testament deltas. They must not call direct
synchronous Guide route after a claim is posted. Guide remains the bus
transport for the deltas.

Acceptance criteria:

- `consult_peer` posts a consultation claim and emits `claim.directed`.
- `challenge_peer` posts a challenge claim and emits `claim.directed`.
- Neither skill calls `RouteSync` for peer execution.
- Synchronous user-facing behavior, when required, waits on canonical
  deltas.
- Asynchronous behavior yields with continuation keyed to claim refs, not
  separate consult-resolved state.
- Existing public skill names remain usable.
- Self-targeting is rejected or normalized before claim post unless it is
  an explicit handoff.

Unit tests:

- `consult_peer` without continuation posts claim and registers
  expectation.
- `consult_peer` with continuation yields on claim/testament refs.
- RouteSync mock is not called.
- Self consult is rejected.
- Challenge uses inspection validation where appropriate.
- Receipt-only consultation auto-accepts on testament.

Integration tests using vektra/mockery:

- Mock board, inbox, and bus to assert claim post and expected delta wait.
- Mock RouteSync and assert zero calls.
- Mock continuation store and assert continuation keys use claim refs.
- Mock identity resolver and assert target type resolves to canonical
  agent before post.

E2E tests:

- Architect consults Librarian; no self rows appear.
- Consult row closes when Librarian submits testament.
- Challenge row closes response receipt while claim remains pending if
  inspection validation still needs evaluation.
- Long-running plan resumes after peer testament without duplicated work.

Failure, race, and deadlock tests:

- Peer testament arrives before await registration; continuation still
  resolves.
- Peer never responds; timeout produces claim progress/error artifact and
  no hung UI row.
- Issuer interrupted while awaiting; cancellation records artifacts and
  unsubscribes.
- Multiple concurrent consults resolve independently.

### Phase 4: Testament and Artifact Semantics

Phase 4 makes responses and errors fully evidence-driven.

#### Item 4.1: Enforce Testament Relation and Artifact Requirements

Description:

Ensure every response to directed work submits a testament with explicit
claim relations. Ensure errors, refusals, timeouts, and impossible work
are represented as artifacts.

Acceptance criteria:

- Directed claim responses include `RelationshipClaim`.
- Relationless testaments do not resolve directed claims.
- Error artifacts are first-class and visible to validations.
- Testament deltas include compact artifact headers.
- Full artifact content remains on the board.
- User-facing artifacts include presentation metadata.

Unit tests:

- Testament with claim relation transitions claim to testified.
- Testament without claim relation does not resolve claim.
- Error artifact derives error verdict.
- Mixed success and error artifacts derive partial verdict.
- Presentation metadata normalizes.

Integration tests using vektra/mockery:

- Mock accumulator flush and verify claim relation is attached when
  responding to directed claim.
- Mock artifact projector and verify artifacts are indexed after commit.
- Mock UI bridge and verify user-facing artifact appears in chat panel.

E2E tests:

- Librarian finds no file and submits error/diagnostic artifacts; issuer
  receives testament and workflow closes or fails validation correctly.
- Architect plan artifact appears in chat panel after completion.
- Tool timeout becomes artifact and validation can evaluate it.

Failure, race, and deadlock tests:

- Accumulator flush races with cancellation and still produces one
  authoritative testament or a cancellation artifact.
- Duplicate flush does not duplicate testament resolution.
- Large artifact is stored by reference and delta carries only header.
- Artifact projector failure does not prevent board commit.

#### Item 4.2: Normalize Testament Context

Description:

Replace ambiguous summary-only semantics with structured testament context.
Keep `Summary` as legacy/display if needed, but canonical deltas should
use `context` for compact receiver-usable information.

Acceptance criteria:

- Testament deltas expose `context`, not only `summary`.
- `summary` may remain inside board entity for compatibility.
- Receivers do not depend on summary for routing or closure.
- Empty context is allowed when artifacts carry all evidence.
- User-facing text lives in testament/artifact presentation metadata.

Unit tests:

- Testament with summary maps to delta context during migration.
- Empty summary with artifacts still emits valid delta.
- Context is bounded to compact size.
- Oversized context is truncated with artifact reference.

Integration tests using vektra/mockery:

- Mock UI renderer and verify displayed content comes from presentable
  artifact/testament, not approval dialog only.
- Mock issuer receiver and verify it queries board when context is
  truncated.

E2E tests:

- Plan output appears as chat artifact.
- Long consult response with truncated context still lets issuer retrieve
  full artifact content.
- Empty-context testament with structured artifacts still validates.

Failure, race, and deadlock tests:

- Context truncation cannot corrupt JSON envelope.
- Concurrent artifact writes do not change emitted context after commit.
- Missing artifact content produces diagnostic artifact on read.

### Phase 5: Validation Execution

Phase 5 uses validation expected tools to make verification deterministic.

#### Item 5.1: Validation Tool Planning and Execution

Description:

When a claim receives testaments, validators inspect each pending
non-receipt validation. The validation's expected tool calls are exposed
to the evaluator. The evaluator or harness executes allowed tools,
records artifacts, and calls validation evaluation.

Acceptance criteria:

- Receipt validations auto-pass only from linked testament arrival.
- Non-receipt validations do not auto-pass.
- Expected validation tools are visible in validation work deltas.
- Allowed expected tools can be executed deterministically.
- Disallowed expected tools produce error artifacts.
- Validation verdict references evidence.
- Failed validation can drive remediation claims.

Unit tests:

- Receipt validation auto-passes on testament.
- Inspection validation remains pending until evaluated.
- Expected validation tool success produces artifact refs.
- Expected validation tool failure produces error artifact.
- Validation cannot evaluate unknown validation ID.

Integration tests using vektra/mockery:

- Mock tool runtime and verify expected validation tools are invoked in
  order when required.
- Mock policy denial and verify failed validation with reason.
- Mock remediation poster and verify failed required validation can create
  corrective claim.
- Mock artifact reader and verify validator reads testament artifacts.

E2E tests:

- Tester receives validation work, runs tests, submits artifacts, and
  passes validation.
- Guardian denies unsafe tool; validation fails with policy artifact.
- Inspector challenges response and posts remediation claim.

Failure, race, and deadlock tests:

- Validation evaluation races with late testament and remains consistent.
- Duplicate validator delivery does not double-evaluate terminal
  validation.
- Tool runtime panic becomes error artifact and failed validation.
- Dead validator is recoverable by replaying pending validation work.

#### Item 5.2: Expected Tool Call Audit Trail

Description:

Record attempted expected tool calls and results as artifacts. The audit
trail must distinguish expected tool specs, actual invocations, outputs,
errors, and policy decisions.

Acceptance criteria:

- Each expected tool call attempt has an artifact or artifact metadata
  entry.
- Skipped required tools are explained.
- Policy-denied tools are recorded as `policy_denied` artifacts.
- Actual tool arguments are captured after redaction.
- Sensitive values are redacted by existing credential policy.
- Validation verdicts can cite tool attempt artifacts.

Unit tests:

- Tool success artifact includes expected tool ID.
- Tool failure artifact includes expected tool ID and error kind.
- Redaction removes secret values from arguments.
- Skipped required tool fails audit validation.

Integration tests using vektra/mockery:

- Mock credential redactor and verify redacted artifact metadata.
- Mock tool runtime and verify attempt/result artifact pairing.
- Mock board and verify artifacts are committed before validation verdict.

E2E tests:

- CLI validation shows test command artifact and validation pass.
- Missing dependency shows missing dependency artifact and validation fail.
- User-denied approval shows policy artifact and no hidden tool execution.

Failure, race, and deadlock tests:

- Tool result arrives after context cancellation and is recorded once or
  safely discarded with cancellation artifact.
- Redactor failure prevents secret artifact commit and records safe error.
- Concurrent expected tools do not reuse artifact IDs.

### Phase 6: UI, Continuations, and Legacy Compatibility

Phase 6 removes UI heuristics and continuation-specific semantic events.

#### Item 6.1: Drive UI Rows From Canonical Deltas

Description:

Update UI bridge and chat renderer so peer rows, tool rows, agent rows,
plans, and terminal state render from canonical deltas and board refs.
Remove orphan heuristics as authoritative behavior.

Acceptance criteria:

- Peer parent rows close on `testament.submitted` or
  `claim.transitioned`.
- Tool rows close on actual tool completion artifacts or error artifacts.
- Progress text comes from `claim.progressed` only.
- Complete status never coexists with streaming spinner.
- User-facing plan artifacts appear in chat panel.
- Approval dialog references the same plan artifact rather than owning
  the only copy.

Unit tests:

- `testament.submitted` closes consult row.
- `claim.progressed` updates text without closing row.
- `claim.transitioned` terminal closes spinner.
- Duplicate terminal deltas are idempotent.
- Plan artifact renders in chat panel.

Integration tests using vektra/mockery:

- Mock bridge receiving canonical deltas and verify chat model mutations.
- Mock renderer and verify no spinner after terminal delta.
- Mock approval dialog and verify it references chat artifact ID.

E2E tests:

- Long consult completes visibly when peer testament lands.
- Challenge response receipt shows done while inspection remains pending.
- Guide complete row stops spinner.
- Plan appears in chat panel after planning turn completes.

Failure, race, and deadlock tests:

- Terminal delta arrives before progress delta; progress cannot overwrite
  terminal state.
- UI receives duplicate terminal delta and remains stable.
- Missing artifact content shows diagnostic row, not blank panel.
- Bridge restart replays deltas and rebuilds same UI tree.

#### Item 6.2: Derive or Remove ConsultResolvedDelta

Description:

Migrate continuations to wake from `testament.submitted` and
`claim.transitioned`. During migration, derive legacy `consult_resolved`
from canonical deltas only if old continuation code still needs it.

Acceptance criteria:

- New continuations key on claim refs.
- New continuations resume from canonical deltas.
- Legacy `consult_resolved` is never emitted independently of board truth.
- Timeouts and cancellations become claim transitions or error artifacts.
- Orphan resolution buffering handles early testament delivery.

Unit tests:

- Continuation resumes on `testament.submitted`.
- Continuation resumes/fails on terminal `claim.transitioned`.
- Derived legacy consult event matches canonical source.
- Independent consult-resolved emission path is removed or disabled.
- Timeout creates terminal/error state exactly once.

Integration tests using vektra/mockery:

- Mock continuation store and canonical delta subscriber.
- Mock early testament delivery before continuation registration.
- Mock timeout scheduler and verify claim/error artifact path.
- Mock legacy consumer and verify derived compatibility event.

E2E tests:

- Plan yields waiting for Librarian, resumes when testament arrives.
- Multiple consults resume only after all required claims resolve.
- Interrupted agent cancels continuation and records cancellation artifact.

Failure, race, and deadlock tests:

- Testament and timeout race resolves exactly once.
- Cancellation and testament race resolves exactly once.
- Resume failure records error artifact and does not loop.
- Continuation store replay does not duplicate LLM turn.

### Phase 7: Cleanup and Enforcement

Phase 7 removes old paths and makes the simplified model enforceable.

#### Item 7.1: Remove Direct Peer Route Authority

Description:

Delete or hard-disable direct synchronous Guide peer route execution for
consult/challenge/guardian-check once canonical deltas cover all flows.
Guide route remains for top-level user routing and bus transport.

Acceptance criteria:

- Peer skill implementations do not call `RouteSync`.
- Tests fail if RouteSync is invoked for claim-backed peer work.
- All peer completion comes from canonical board deltas.
- No self rows are created by compatibility branch metadata.
- Legacy branch UI metadata is removed or derived from claim refs.

Unit tests:

- Static or behavioral test proves RouteSync unused for peer skills.
- Peer interaction artifacts map to claim refs only.
- Self-target claim guard rejects invalid target.

Integration tests using vektra/mockery:

- Mock RouteSync and assert no calls across consult/challenge paths.
- Mock Guide bus and assert only claims delta messages are published.
- Mock UI bridge and assert rows derive from claim refs.

E2E tests:

- Reproduce historical consult hang scenario and verify it completes.
- Reproduce self-consult scenario and verify it cannot happen.
- Reproduce interrupt scenario and verify running tools/claims cancel or
  artifactize cleanly.

Failure, race, and deadlock tests:

- Peer agent crash leaves claim pending until timeout, then records
  artifact/transition.
- Issuer crash before receiving testament resumes from replay.
- Bus partition heals and no duplicate peer work executes.

#### Item 7.2: Contract Tests and Lints

Description:

Add tests and static checks that enforce the claims/deltas contract.

Acceptance criteria:

- New delta actions require schema tests.
- New action types must be classified as agent-waking or system-internal.
- New validation types require evaluation semantics documentation.
- New expected tool call fields require JSON round trip tests.
- No code path treats progress/context as completion.
- No code path treats Guide RouteResponse as peer-work completion for
  claim-backed consults or challenges.

Unit tests:

- Enum partition tests for delta actions.
- Enum partition tests for claim actions.
- Validation type documentation test.
- Progress-not-terminal tests.
- RouteResponse-not-terminal tests.

Integration tests using vektra/mockery:

- Mock all peer skill dependencies and verify canonical path only.
- Mock bus replay and verify receivers dedup.
- Mock UI bridge and verify terminal source restrictions.

E2E tests:

- Full planning turn with consult, carry-forward, plan artifact, approval,
  validation, and terminal UI state.
- Full challenge flow with response, inspection validation, remediation,
  and closure.
- Full failure flow where expected tool fails, error artifact is submitted,
  validation fails, and remediation claim is posted.

Failure, race, and deadlock tests:

- Stress test concurrent claims/testaments/validations under race detector.
- Deadlock test with slow bus subscriber and slow board projector.
- Fuzz test delta JSON decoding with unknown fields and malformed refs.
- Replay test with duplicate and out-of-order delivery.

## 18. Migration Notes

The migration should be additive first.

1. Add canonical schema and expected tool call fields.
2. Emit canonical deltas alongside legacy deltas.
3. Teach ClaimsInbox and UI bridge to consume canonical deltas.
4. Move consult/challenge continuations to canonical delta wakeups.
5. Remove direct RouteSync peer execution.
6. Stop emitting independent `consult_resolved`.
7. Downgrade `phase`, `claim_context`, and `testament_context` to
   projection/UI compatibility or remove them.
8. Add contract tests so the old shape cannot return.

At no point should agent-agent work bypass the Guide event bus. The board
commits truth, the Guide bus delivers facts, and agents respond by writing
new claims, testaments, artifacts, and validations.
