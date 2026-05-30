# CLAIMS_VISIBILITY: Testament and Artifact Presentation Architecture

## 1. Purpose

This document defines how Sylk makes selected testaments and artifacts visible
to the human user without changing their status as normal claims-board evidence.

The immediate motivating bug is the Architect saying "the plan is ready for
review" while the plan body is not rendered in chat. The deeper issue is that
the claims board already has the right ontology for proof:

```text
Action
  Claim
    Testament
      Artifact
```

but it does not yet have a durable presentation contract that says which
testaments or artifacts are intended to be rendered to a user-facing surface.

Visibility in this document means presentation/rendering intent. It does not
mean access control.

Visibility consumers use the lifecycle vocabulary from
`docs/ARTIFACTS_AND_VALIDATIONS.md` without inventing display-only
states: `artifact.generated`, `artifact.generation_failed`,
`artifact.received`, `artifact.receipt_failed`, `artifact.attached`,
`artifact.validating`, `artifact.validation_failed`,
`artifact.validated`, `validation.ready`, `validation.validating`,
`validation.validation_failed`,
`validation.validation_failed_not_required`, `validation.errored`,
`validation.errored_not_required`, `validation.validating_quality_bar`,
`validation.quality_bar_validation_failed`,
`validation.quality_bar_validation_failed_not_required`, and
`validation.validated`. Legacy validation statuses are rendered only
through compatibility projections.

## 2. Non-Negotiable Semantics

### 2.1 Claims are constraints, not display flags

A Claim is a precise assertion made against a subject, with validations. It is
the unit of work and the constraint the subject must satisfy. It should not
carry "show this to the user" flags.

Display intent belongs to the response and proof:

- Testament: what the subject says it did or found.
- Artifact: the evidence or concrete work product proving the testament.

### 2.2 User-visible does not mean user-only

A user-visible artifact remains ordinary board evidence. It must remain:

- queryable by `query_claims_board`;
- reachable through `traverse`;
- included in projections and WAL replay;
- usable by validators;
- available to agents as context;
- linkable by relations such as `derived_from`, `supersedes`, `amends`,
  `reviews`, and `depends_on`.

User visibility is an additional presentation property. It must never remove
an artifact from the agent-visible evidence graph.

### 2.3 Internal does not mean hidden from agents

The default presentation state is "not automatically rendered to the user".
That does not mean hidden. Internal artifacts are still evidence. They are just
not rendered into chat, approval, or side-panel surfaces without an explicit
presentation contract.

Examples:

| Entity | Presentation | Agent access |
|---|---|---|
| `plan_handoff_payload` artifact | Internal | Full board access |
| `plan_markdown` artifact | User-visible chat review | Full board access |
| `response_text` artifact | User-visible chat answer | Full board access |
| `error_trace` artifact | Usually internal, may be user-visible in diagnostics | Full board access |
| Academic research artifact | Often user-visible or validator-visible | Full board access |

### 2.4 The board is still the source of truth

The UI must not receive a separate non-claims copy of a plan, report, or diff.
If a user sees a plan in chat, that plan must correspond to a board artifact or
testament. Restart and replay must rebuild the same visible content from board
state.

### 2.5 Immutability still holds

Testaments and artifacts remain immutable. Updating user-visible content means
submitting a new testament and new artifact, linked by `supersedes` or `amends`
relations, and optionally sharing a presentation `replace_key` so the UI can
replace the rendered content in place.

## 3. Vocabulary

### 3.1 Accessibility

Accessibility means which system actors can inspect the entity as evidence.
This design does not introduce access control. Claims-board accessibility
continues to be governed by the board, sessions, scopes, and existing query
surfaces.

### 3.2 Presentation

Presentation means an entity has an explicit instruction for a UI surface.
Presentation answers:

- Should this entity render to a human-facing surface?
- Which surface should render it?
- What format should the renderer use?
- Should it replace an earlier visible artifact?
- Where should it appear relative to the final assistant response?

Per `docs/ARTIFACTS_AND_VALIDATIONS.md` §5, artifacts carry a first-class
eight-state lifecycle. Presentation rendering may key on lifecycle
state in addition to the static `Presentation` metadata fields: progress
indicators driven by `artifact.validating`, completion glyphs driven
by `artifact.validated`, error rendering driven by
`artifact.validation_failed` or `artifact.receipt_failed`. The
lifecycle-aware rendering does not replace the static contract; it
augments it so terminal-state visual changes happen automatically as
the underlying lifecycle progresses.

Agents and services do not need an audience flag to access evidence.
They use the board. Both participant categories produce presentable
artifacts via the same `Presentation` field on the typed artifact
record. Service-produced testaments (e.g., a guardian-denial testament
carrying a user-visible explanation, a VFS-provisioning testament
surfacing capacity status, a librarian consultation summary) route
through the same `ClaimPresentationMsg` bridge path as
agent-produced testaments.

### 3.3 Audience

Audience identifies who the presentation is for. It is not an ACL.

Initial audiences:

| Audience | Meaning |
|---|---|
| `user` | Render to the human user. |
| `operator` | Render to diagnostic/operator surfaces. |
| `developer` | Render to development/debug surfaces. |

Agents do not need an audience flag to access evidence. They use the board.

### 3.4 Surface

Surface identifies where a presentable entity should appear.

Initial surfaces:

| Surface | Meaning |
|---|---|
| `chat` | Main chat transcript. |
| `approval` | Approval dialog or review surface. |
| `side_panel` | Auxiliary inspector, plan, or artifact panel. |
| `diagnostics` | Debug or operator diagnostics view. |

### 3.5 Format

Format identifies how to render `Reference` or derived content.

Initial formats:

| Format | Meaning |
|---|---|
| `markdown` | CommonMark-compatible markdown. |
| `text` | Plain text. |
| `json` | Structured JSON. |
| `diff` | Unified diff or VFS diff. |
| `table` | Structured tabular data. |

### 3.6 Placement

Placement controls how a visible entity attaches to the active chat cycle.

Initial placements:

| Placement | Meaning |
|---|---|
| `before_response` | Render before the final assistant prose. |
| `after_response` | Render after the final assistant prose. |
| `inline` | Render at the point the artifact arrives. |
| `replace` | Replace prior rendered content with the same `replace_key`. |
| `panel_only` | Do not add a transcript entry; surface in a panel. |

## 4. Data Model

### 4.1 Add presentation metadata to Testament and Artifact

Add an optional presentation field to `claims.Testament` and
`claims.Artifact`.

```go
type PresentationAudience string

const (
    PresentationAudienceUser      PresentationAudience = "user"
    PresentationAudienceOperator  PresentationAudience = "operator"
    PresentationAudienceDeveloper PresentationAudience = "developer"
)

type PresentationSurface string

const (
    PresentationSurfaceChat        PresentationSurface = "chat"
    PresentationSurfaceApproval    PresentationSurface = "approval"
    PresentationSurfaceSidePanel   PresentationSurface = "side_panel"
    PresentationSurfaceDiagnostics PresentationSurface = "diagnostics"
)

type PresentationFormat string

const (
    PresentationFormatMarkdown PresentationFormat = "markdown"
    PresentationFormatText     PresentationFormat = "text"
    PresentationFormatJSON     PresentationFormat = "json"
    PresentationFormatDiff     PresentationFormat = "diff"
    PresentationFormatTable    PresentationFormat = "table"
)

type PresentationPlacement string

const (
    PresentationPlacementBeforeResponse PresentationPlacement = "before_response"
    PresentationPlacementAfterResponse  PresentationPlacement = "after_response"
    PresentationPlacementInline         PresentationPlacement = "inline"
    PresentationPlacementReplace        PresentationPlacement = "replace"
    PresentationPlacementPanelOnly      PresentationPlacement = "panel_only"
)

type Presentation struct {
    Audiences  []PresentationAudience `json:"audiences,omitempty"`
    Surfaces   []PresentationSurface  `json:"surfaces,omitempty"`
    Format     PresentationFormat     `json:"format,omitempty"`
    Title      string                 `json:"title,omitempty"`
    Placement  PresentationPlacement  `json:"placement,omitempty"`
    ReplaceKey string                 `json:"replace_key,omitempty"`
    Priority   int                    `json:"priority,omitempty"`
}

type Testament struct {
    ...
    Presentation *Presentation `json:"presentation,omitempty"`
}

type Artifact struct {
    ...
    Presentation *Presentation `json:"presentation,omitempty"`
}
```

The field is optional. Omitted presentation means no automatic user rendering.

### 4.2 Do not overload `Metadata`

`Metadata` remains kind-specific structured data. It should carry plan IDs,
epochs, hashes, task counts, file paths, source IDs, and similar domain data.
Presentation is a cross-kind protocol concern, so it gets a typed field.

Allowed:

```json
{
  "kind": "plan_markdown",
  "reference": "### Plan\n\n...",
  "presentation": {
    "audiences": ["user"],
    "surfaces": ["chat", "approval"],
    "format": "markdown",
    "title": "Plan",
    "placement": "before_response",
    "replace_key": "plan:8b31:ready"
  },
  "metadata": {
    "plan_id": "8b31",
    "epoch": 4,
    "content_hash": "sha256:..."
  }
}
```

Avoid:

```json
{
  "metadata": {
    "visible": true,
    "surface": "chat",
    "format": "markdown"
  }
}
```

The second form works mechanically but hides a core protocol contract inside
untyped, kind-specific data.

### 4.3 Presentation is inherited only by explicit rule

Presentation does not automatically flow from a testament to all artifacts or
from an artifact to its parent testament.

Rules:

1. A presentable testament renders its `Summary` or `Context`, depending on
   placement and lifecycle.
2. A presentable artifact renders its own `Reference` or dereferenced content.
3. A non-presentable testament can contain presentable artifacts.
4. A presentable testament can contain internal artifacts.
5. If both a testament and one of its artifacts are presentable, both are
   renderable unless their `replace_key` or relation graph indicates one
   supersedes the other.

Example:

- Testament summary: "Plan ready for review" - internal.
- Artifact `plan_markdown`: user-visible in chat and approval.
- Artifact `plan_handoff_payload`: internal.

This is the expected Architect plan shape.

### 4.4 Replacement identity

`Presentation.ReplaceKey` is a UI replacement key, not entity identity.
It lets immutable artifacts produce a mutable user experience.

Examples:

| Entity | Replace key |
|---|---|
| Ready plan markdown | `plan:<plan_id>:review` |
| Revised plan markdown | `plan:<plan_id>:review` |
| Freshness audit | `plan:<plan_id>:freshness` |
| Command preview | `command:<approval_id>:preview` |

The immutable artifact IDs remain distinct. The UI replaces the rendered row
only for presentation.

### 4.5 Relations remain authoritative for provenance

Presentation replacement does not replace graph relations.

If an artifact revises another artifact, it should include:

```go
claims.Relation{
    Related: priorArtifactID,
    RelatedType: claims.RelatedTypeArtifact,
    Relationship: claims.RelationshipSupersedes,
}
```

The UI may use `replace_key` for display and relations for provenance.
Validators should use relations, not only `replace_key`.

## 5. Canonical Examples

### 5.1 Architect ready plan

Claim:

```text
Architect produces a user-reviewable implementation plan for the request.
```

Validations:

```text
Receipt: A testament is submitted for the plan-ready claim.
Inspection: A `plan_markdown` artifact exists and is renderable to the user.
Quality bar: Artifact contains plan ID, epoch, tasks, acceptance criteria,
execution/dependency shape, and risk/tradeoff summary.
```

Testament:

```json
{
  "summary": "Plan ready for review.",
  "confidence": "committed",
  "artifacts": [
    {
      "kind": "plan_markdown",
      "reference": "### Plan\n\n**Status:** ready\n\n...",
      "presentation": {
        "audiences": ["user"],
        "surfaces": ["chat", "approval"],
        "format": "markdown",
        "title": "Plan",
        "placement": "before_response",
        "replace_key": "plan:a45ed92a:review"
      },
      "metadata": {
        "plan_id": "a45ed92a-a733-4525-bbf5-b9040bd6f80e",
        "epoch": 7,
        "content_hash": "sha256:...",
        "role": "primary_review_artifact"
      }
    },
    {
      "kind": "plan_handoff_payload",
      "reference": "{\"plan_id\":\"a45ed92a\",...}",
      "metadata": {
        "plan_id": "a45ed92a-a733-4525-bbf5-b9040bd6f80e",
        "epoch": 7
      }
    }
  ]
}
```

The user sees the plan markdown. Agents see both artifacts.

### 5.2 Librarian consultation

Claim:

```text
Librarian reports repository structure relevant to a new Python CLI.
```

Testament:

```json
{
  "summary": "Repository has no Python package infrastructure.",
  "confidence": "committed",
  "artifacts": [
    {
      "kind": "workspace_survey",
      "reference": "No pyproject.toml, setup.py, requirements.txt, or *.py files found.",
      "presentation": {
        "audiences": ["user"],
        "surfaces": ["chat"],
        "format": "markdown",
        "title": "Repository survey",
        "placement": "inline"
      },
      "metadata": {
        "query": "existing Python infrastructure",
        "files_checked": 1234
      }
    }
  ]
}
```

The same artifact can inform Architect, Guardian, Inspector, and the user.

### 5.3 Error artifact surfaced to the user

An error artifact can be user-visible when it explains a decision or blocker.

```json
{
  "kind": "error_diagnostic",
  "reference": "Could not inspect remote package metadata because network access is disabled.",
  "presentation": {
    "audiences": ["user"],
    "surfaces": ["chat"],
    "format": "text",
    "title": "Planning limitation",
    "placement": "inline"
  },
  "metadata": {
    "operation": "package_metadata_lookup",
    "retryable": false
  }
}
```

This remains an error artifact. It is not converted into an assistant-only
message.

## 6. Runtime Flow

### 6.1 Board submission

Agents submit testaments through `submit_testaments` or accumulator flush.
Presentable testaments and artifacts are stored exactly like all others. The
board assigns IDs, sequences, content hashes, relations, and projection entries.

### 6.2 Delta emission

The board emits canonical lifecycle deltas for every artifact and validation
state transition per `docs/ARTIFACTS_AND_VALIDATIONS.md` §12. The bridge
subscribes to artifact lifecycle actions (`artifact.generated`,
`artifact.attached`, `artifact.validating`, `artifact.validated`,
`artifact.validation_failed`, etc.) and validation lifecycle actions
(`validation.validating`, `validation.validated`,
`validation.validation_failed`, `validation.validating_quality_bar`, etc.)
in addition to the testament and claim deltas defined in earlier sections.

The bridge resolves the full entity from board projection or the artifact
progress sink. If the entity has presentation metadata for a supported
user-facing surface, the bridge emits a UI presentation message. The
bridge does not branch on participant category (agent vs service) for
delta consumption; the wire format is uniform per
`docs/CLAIMS_AND_INFRASTRUCTURE.md` §6.4.

In this visibility model, service-produced testaments carry presentation metadata through the same
artifact and testament fields as agent-produced testaments. The same rule
holds for validation: programmatic validators read artifacts via the same board API as agentic validators,
so infrastructure evidence can become visible without a special UI path.
A guardian-denial testament with a user-visible explanation is therefore
a first-class presentation case, not a side-channel error.

### 6.3 UI bridge conversion

The bridge maps presentable entities into display messages:

```go
type ClaimPresentationMsg struct {
    SessionID      string
    CycleID        string
    ClaimID        string
    SourceType     string // "testament" | "artifact"
    SourceID       string
    TestamentID    string
    AgentID        string
    Title          string
    Content        string
    Format         string
    Placement      string
    ReplaceKey     string
    Metadata       map[string]any
    CreatedAt      time.Time
    Sequence       uint64
}
```

This message is a UI projection of claims-board state. It is not a new source
of truth.

### 6.4 Chat model handling

The chat model renders `ClaimPresentationMsg` according to:

1. `surface=chat`
2. `format`
3. `placement`
4. `replace_key`
5. cycle ownership and stream state

Markdown plan artifacts can be embedded before the final assistant response.
Revised plan artifacts replace the previous rendered plan row using the same
`replace_key`.

### 6.5 Approval handling

Approval proposals should refer to the same presentable artifact:

```json
{
  "plan_id": "a45ed92a",
  "plan_artifact_id": "artifact-plan-md-1",
  "plan_artifact_replace_key": "plan:a45ed92a:review"
}
```

`PlanText` can remain as a fallback during migration, but the canonical review
body should be the `plan_markdown` artifact.

## 7. Invariants

1. A user-visible artifact is still normal board evidence.
2. A user-visible artifact is still queryable by agents.
3. A presentable artifact must have a deterministic source entity ID.
4. A rendered chat row must be traceable to a testament or artifact ID.
5. A rendered chat row must be replayable from board state.
6. A rendered revision must preserve immutable artifact history.
7. A final response may not claim "review the plan" unless a presentable plan
   artifact exists in the same cycle or is explicitly referenced by ID.
8. Presentation metadata must not determine validation success by itself.
   Validations inspect the artifact content and relations.
9. Presentation must not create a second copy of the work product.
10. Presentation must not suppress normal testament or artifact deltas.

## 8. Phased Implementation Plan

The phases below are intentionally strict. Each phase has purpose, design,
examples, acceptance criteria, and test requirements.

---

## Phase 0 - Vocabulary, Contracts, and Guardrails

### 0.1 Document presentation as a Testament/Artifact concern

**Purpose**

Prevent future fixes from placing visibility on claims. Claims constrain work;
testaments and artifacts carry responses and proof. Presentation belongs to the
response/proof layer.

**Design**

Update architecture documents and code comments to define:

- accessibility vs presentation;
- user-visible vs user-only;
- presentable testament;
- presentable artifact;
- relation between presentation metadata and existing board evidence.

**Example**

Bad:

```json
{"claim": {"visible_to_user": true}}
```

Good:

```json
{
  "artifact": {
    "kind": "plan_markdown",
    "presentation": {
      "audiences": ["user"],
      "surfaces": ["chat"],
      "format": "markdown"
    }
  }
}
```

**Acceptance criteria**

- `CLAIMS.md` and this document agree that claims are assertions and not UI
  display flags.
- Any new public API names avoid "claim visibility".
- "User-visible" is documented as presentation only.
- Agent access remains board-based and unchanged.
- The plan review bug is documented as "missing presentable artifact", not
  "missing claim visibility".

**Unit tests**

- Documentation lint checks for forbidden phrases in new code comments:
  `claim visibility`, `claim visible`, `user-only artifact`.
- Type tests ensure no `Visibility` or `Presentation` field is added to
  `claims.Claim`.

**Integration tests with vektra/mockery**

- Generate mocks for a proposed `PresentationRouter` interface.
- Verify the router accepts testaments and artifacts, not claims.
- Verify a mock router receiving a claim-only event does not emit UI content.

**E2E tests**

- Happy path: Architect produces a ready plan, and the visible plan is traced to
  an artifact ID.
- Negative path: a claim with no presentable testament/artifact does not render
  as chat content.
- Edge case: a claim title that says "show this plan" does not render anything
  unless a presentable artifact exists.

### 0.2 Define canonical constants

**Purpose**

Avoid string drift across agents, bridge, UI, and tests.

**Design**

Add constants in `core/claims`:

```go
const ArtifactKindPlanMarkdown = "plan_markdown"

const (
    PresentationAudienceUser = "user"
    PresentationSurfaceChat = "chat"
    PresentationSurfaceApproval = "approval"
    PresentationFormatMarkdown = "markdown"
)
```

**Example**

Architect uses `claims.ArtifactKindPlanMarkdown`, not `"plan_markdown"`.

**Acceptance criteria**

- No new production code hard-codes presentation strings when constants exist.
- Constants are exported only where cross-package use is required.
- Constants are documented with examples.
- Existing artifact kinds remain valid.

**Unit tests**

- Compile-time tests assert constants match expected wire strings.
- JSON round-trip tests prove constants serialize as strings.

**Integration tests with vektra/mockery**

- Mock bridge receives artifacts using constants and emits expected message
  fields.
- Mock bridge receives typo strings and rejects or ignores them according to
  validation policy.

**E2E tests**

- A plan artifact using constants renders.
- A malformed artifact with `format=markdwon` does not crash the UI and falls
  back to plain text or logs a presentation warning.

### 0.3 Define presentation validation rules

**Purpose**

Prevent ambiguous or unsafe presentation metadata.

**Design**

Create normalization and validation helpers:

```go
func NormalizePresentation(p *claims.Presentation) *claims.Presentation
func ValidatePresentation(p *claims.Presentation) error
func IsUserChatPresentation(p *claims.Presentation) bool
```

Validation rules:

- Empty presentation means no automatic rendering.
- `surfaces` must be non-empty when `audiences` contains `user`.
- `format` defaults to `text`.
- `placement` defaults to `inline`.
- `replace_key` is optional but required for known replaceable kinds such as
  `plan_markdown`.
- Unknown surfaces are ignored by the UI but preserved on the board.

**Example**

Input:

```json
{"audiences":["user"],"surfaces":["chat"],"format":""}
```

Normalized:

```json
{"audiences":["user"],"surfaces":["chat"],"format":"text","placement":"inline"}
```

**Acceptance criteria**

- Normalization is deterministic.
- Validation never mutates immutable board entities after submission.
- Unknown values are preserved for future compatibility.
- The UI handles unsupported presentation as non-renderable, not fatal.

**Unit tests**

- Default format and placement are applied.
- Empty presentation is non-renderable.
- User audience without surface returns validation error or non-renderable.
- Unknown surface is preserved and skipped.
- Duplicate audiences and surfaces are deduplicated.

**Integration tests with vektra/mockery**

- Mock `PresentationSink` receives only normalized presentation.
- Mock sink is not called for invalid presentation.
- Mock logger records validation warnings without failing board submission.

**E2E tests**

- Malformed presentation metadata in WAL replay does not deadlock the UI.
- Unknown future surface is ignored while board replay continues.
- Large malformed JSON artifact does not block chat rendering.

### 0.4 Establish the "review claim requires review artifact" rule

**Purpose**

Make it impossible for an agent to say "review the plan" while only emitting
final prose.

**Design**

Define a reusable validation pattern:

```text
Validation: User-reviewable artifact exists.
Quality bar: A presentable artifact with audience=user and surface=chat or
approval exists, has non-empty content, and is linked to this testament.
```

Architect plan-ready flows must include this validation or an equivalent
runtime guard.

**Example**

Architect may write:

```text
Take a look at the plan.
```

only after submitting a `plan_markdown` artifact or referencing a prior
presentable artifact by ID.

**Acceptance criteria**

- The guard is enforced for plan-ready flows.
- The guard permits referencing an existing presentable artifact if it is still
  current for the plan epoch.
- The guard blocks final prose that implies review without evidence.
- The failure mode is a structured error artifact or corrective claim, not a
  silent omission.

**Unit tests**

- `CanClaimPlanReviewReady(plan, artifacts)` returns true with current
  `plan_markdown`.
- It returns false with only `plan_handoff_payload`.
- It returns false with stale epoch.
- It returns false with empty markdown content.

**Integration tests with vektra/mockery**

- Mock Architect presenter verifies `SubmitTestaments` is called before final
  response publication.
- Mock bridge verifies a visible artifact message exists before response text
  that says "review".

**E2E tests**

- Happy path: plan markdown appears before "take a look".
- Negative path: force plan artifact emission failure; Architect does not say
  "review the plan" without a fallback explanation.
- Race path: response text and artifact delta arrive out of order; chat still
  renders plan and final prose coherently.

---

## Phase 1 - Core Schema and Persistence

### 1.1 Add `Presentation` to `claims.Testament`

**Purpose**

Support user-visible conclusions, research summaries, diagnostics, and other
testament-level content without requiring a separate artifact for every
renderable summary.

**Design**

Add `Presentation *Presentation` to `Testament`. The testament summary is the
rendered content unless a future field provides a richer body.

Use cases:

- A librarian consultation summary should appear in chat.
- An Academic research conclusion should appear in chat.
- A Guardian decision summary should appear in approval history.

**Example**

```json
{
  "summary": "Repository has no Python package infrastructure.",
  "presentation": {
    "audiences": ["user"],
    "surfaces": ["chat"],
    "format": "markdown",
    "title": "Librarian finding",
    "placement": "inline"
  }
}
```

**Acceptance criteria**

- Omitted presentation preserves current behavior.
- Testament JSON round-trip preserves presentation.
- Board clone methods deep-copy presentation.
- Projection includes presentation.
- WAL replay restores presentation exactly.
- Presentation does not affect claim status or validation status.

**Unit tests**

- `TestTestamentJSONRoundTrip_WithPresentation`.
- `TestCloneTestament_DeepCopiesPresentation`.
- `TestProjection_IncludesTestamentPresentation`.
- `TestSubmitTestaments_PresentationDoesNotChangeValidation`.

**Integration tests with vektra/mockery**

- Mock delta bus receives a `TestamentDelta` for a presentable testament.
- Mock projection reader returns the full testament and bridge emits a
  presentation message.
- Mock validator confirms receipt validation still auto-passes normally.

**E2E tests**

- Happy path: a presentable consultation testament renders in chat.
- Negative path: an internal testament does not render.
- Race path: testament delta arrives before projection subscriber catches up;
  bridge retries or resolves without dropping content.
- Replay path: restart UI and visible testament summary reappears.

### 1.2 Add `Presentation` to `claims.Artifact`

**Purpose**

Support user-visible work products: plans, diffs, reports, diagnostics,
research, design assets, generated specs, and command previews.

**Design**

Add `Presentation *Presentation` to `Artifact`. The artifact `Reference` is
rendered according to presentation `Format`, unless future content-addressed
storage is used for large artifacts.

**Example**

```json
{
  "kind": "diff",
  "reference": "--- a/file.go\n+++ b/file.go\n...",
  "presentation": {
    "audiences": ["user"],
    "surfaces": ["chat"],
    "format": "diff",
    "title": "Proposed changes",
    "placement": "after_response"
  }
}
```

**Acceptance criteria**

- Artifact JSON round-trip preserves presentation.
- Artifact content hash ignores no fields that should affect integrity. If the
  hash covers the full artifact, presentation changes require a new artifact.
- Projection truncation must not destroy presentation metadata.
- Large `Reference` truncation must be explicit and must not masquerade as full
  content.
- Presentation does not affect artifact queryability.

**Unit tests**

- `TestArtifactJSONRoundTrip_WithPresentation`.
- `TestCloneArtifact_DeepCopiesPresentation`.
- `TestArtifactProjection_TruncatesReferenceButPreservesPresentation`.
- `TestArtifactHash_PresentationPolicy`.

**Integration tests with vektra/mockery**

- Mock board projection returns a presentable artifact and the bridge emits
  `ClaimPresentationMsg`.
- Mock board projection returns a truncated artifact and bridge emits a
  "content_truncated" warning or fetch request according to final design.

**E2E tests**

- Happy path: plan markdown artifact renders.
- Negative path: internal handoff artifact does not render.
- Edge path: artifact reference is empty; UI renders a warning row or skips
  with telemetry, but does not claim the plan is shown.
- Race path: two artifacts with same replace key arrive concurrently; highest
  sequence wins.

### 1.3 Persist presentation through WAL and projection

**Purpose**

Guarantee replay correctness. If the user saw content once, restart and replay
must reconstruct it.

**Design**

Update:

- WAL serialization;
- board projection;
- clone helpers;
- snapshot load/save;
- projection truncation;
- Fabric activity projection if it copies artifacts/testaments.

**Example**

Replay of a plan-ready cycle reconstructs:

1. claim row;
2. visible plan markdown row;
3. final Architect response;
4. approval dialog state when applicable.

**Acceptance criteria**

- WAL round-trip preserves all presentation fields.
- Projection caches invalidate when presentable testaments/artifacts are added.
- Presentation survives board close/open.
- Replay order is deterministic by sequence.
- Old WAL entries without presentation still load.

**Unit tests**

- WAL encode/decode with presentable testament.
- WAL encode/decode with presentable artifact.
- Projection cache invalidation after testament submission.
- Backward compatibility with missing field.

**Integration tests with vektra/mockery**

- Mock WAL store returns mixed old/new records.
- Mock bridge consumes replayed projection and emits exactly one presentation
  message per presentable entity.

**E2E tests**

- Create plan, restart app, verify plan still visible.
- Create revised plan, restart app, verify only latest replacement renders in
  the main row while old artifact remains inspectable.
- Corrupt one presentation field in WAL fixture; replay skips that one surface
  without wedging the board.

### 1.4 Add presentation-aware query helpers without restricting evidence

**Purpose**

Let agents and validators find presentable artifacts while preserving generic
board traversal.

**Design**

Add helper functions:

```go
func PresentableArtifacts(t *claims.Testament, audience, surface string) []*claims.Artifact
func HasPresentableArtifact(t *claims.Testament, kind, audience, surface string) bool
func IsPresentableToUserChat(p *claims.Presentation) bool
```

These helpers are conveniences. They do not replace `query_claims_board` or
`traverse`.

**Example**

Guardian can check:

```go
if !claims.HasPresentableArtifact(testament, claims.ArtifactKindPlanMarkdown, "user", "approval") {
    fail("plan is not reviewable")
}
```

**Acceptance criteria**

- Helpers return presentable artifacts only.
- Helpers never hide internal artifacts from generic board queries.
- Helpers handle nil and malformed presentation.
- Helpers are deterministic.

**Unit tests**

- Nil testament returns empty.
- Mixed internal/user artifacts returns only matching artifacts.
- Multi-surface artifact matches both surfaces.
- Unknown audience does not match user.

**Integration tests with vektra/mockery**

- Mock validator uses helper to pass a reviewability validation.
- Mock validator fails when plan markdown exists but lacks `approval` surface.

**E2E tests**

- Guardian approves only after presentable approval artifact exists.
- Inspector can still find internal handoff payload while user sees markdown.

---

## Phase 2 - Bridge and UI Message Projection

### 2.1 Add `ClaimPresentationMsg`

**Purpose**

Represent user-facing content derived from a testament or artifact without
pretending it is a tool row or final assistant response.

**Design**

Add to `ui/msg/msg.go`:

```go
type ClaimPresentationMsg struct {
    SessionID   string
    CycleID     string
    ClaimID     string
    SourceType  string
    SourceID    string
    TestamentID string
    AgentID     string
    Title       string
    Content     string
    Format      string
    Placement   string
    ReplaceKey  string
    Metadata    map[string]any
    CreatedAt   time.Time
    Sequence    uint64
}
```

`SourceType` is `testament` or `artifact`.

**Example**

A `plan_markdown` artifact becomes:

```json
{
  "source_type": "artifact",
  "source_id": "artifact-plan-md-1",
  "title": "Plan",
  "format": "markdown",
  "placement": "before_response",
  "replace_key": "plan:a45ed92a:review"
}
```

**Acceptance criteria**

- Message includes enough IDs to trace back to board state.
- Message includes no data that cannot be reconstructed from board state.
- Message supports replacement.
- Message supports multiple formats.
- Message does not conflict with `ClaimArtifactAddedMsg`.

**Unit tests**

- Message zero value is safe.
- Serialization round-trip keeps IDs, format, placement, replace key.
- UI route table includes `ClaimPresentationMsg`.

**Integration tests with vektra/mockery**

- Mock `ChatSink` receives `ClaimPresentationMsg` for presentable artifact.
- Mock sink does not receive message for internal artifact.

**E2E tests**

- Plan artifact produces visible markdown row.
- Diagnostic artifact produces visible plain text row.
- Tool-start artifacts still render as tool rows, not presentation rows.

### 2.2 Extend `ClaimsBridge.OnArtifactAdded`

**Purpose**

Route presentable artifacts from the board into chat/panel surfaces.

**Design**

Add a branch before started/completed tool artifact handling:

```go
case isPresentableArtifact(art, "user", "chat"):
    out = append(out, b.claimPresentationMsgLocked(sessionID, claimID, art))
```

Do not remove existing handling for `agent_state`, `response_text`,
`*_started`, or `*_completed`.

**Example**

`plan_markdown` with `surface=chat` emits presentation.
`plan_handoff_payload` emits nothing unless it explicitly has presentation.

**Acceptance criteria**

- Presentable artifacts emit exactly once.
- Internal artifacts emit no presentation message.
- Existing tool row behavior is unchanged.
- If an artifact is both a started artifact and presentable, policy is
  deterministic. Recommended: lifecycle kinds remain lifecycle rows unless a
  specific allowlist says otherwise.
- Bridge preserves cycle attribution using existing resolver logic.

**Unit tests**

- `OnArtifactAdded` emits for `plan_markdown`.
- `OnArtifactAdded` skips internal artifact.
- Duplicate artifact ID is idempotent.
- Missing claim ID is resolved from projection when possible.
- Suppressed chat cycles honor suppression policy.

**Integration tests with vektra/mockery**

- Mock current board returns claim metadata needed for cycle resolution.
- Mock output queue receives presentation message after artifact add.
- Mock resolver returns no cycle; bridge defers or drops with telemetry
  according to existing deferred-artifact policy.

**E2E tests**

- Happy path: artifact appears under the correct Architect cycle.
- Negative path: artifact for inactive session does not render.
- Race path: artifact arrives before claim registration; bridge eventually
  binds it or logs a deterministic drop.
- Deadlock path: bridge does not hold mutex while enqueueing to chat.

### 2.3 Extend testament handling

**Purpose**

Render presentable testament summaries, especially consultation summaries,
decision summaries, and user-facing validation outcomes.

**Design**

When a `TestamentDelta` arrives or projection updates, if the testament has
presentation metadata for `user/chat`, emit `ClaimPresentationMsg` with
`SourceType=testament` and `Content=testament.Summary` or `Context`.

Use `Context` only for in-flight mutable display. Use `Summary` after flush.

**Example**

Academic testament:

```json
{
  "summary": "Use argparse for stdlib-only toy CLI; Click is acceptable if external dependency is allowed.",
  "presentation": {
    "audiences": ["user"],
    "surfaces": ["chat"],
    "format": "markdown",
    "title": "Academic recommendation"
  }
}
```

**Acceptance criteria**

- Flushed testament summary renders once.
- In-flight context updates continue using `TestamentContextMsg`.
- If both context and final summary render, final summary replaces or completes
  the in-flight row, not duplicate.
- Internal testaments remain invisible in chat.
- Agent accessibility is unchanged.

**Unit tests**

- Presentable testament emits presentation.
- Internal testament does not.
- Empty summary is skipped with warning.
- Testament with presentable artifact can render both only if configured.

**Integration tests with vektra/mockery**

- Mock board projection returns testament after delta.
- Mock chat sink receives one final presentation after flush.
- Mock sink verifies ordering with associated artifacts.

**E2E tests**

- Librarian final finding renders as a readable chat row.
- Failed consultation with user-visible diagnostic renders error artifact, not
  duplicate summary spam.
- Race path: context updates and final testament arrive out of order; final
  row is coherent.

### 2.4 Replacement and ordering

**Purpose**

Support immutable artifacts with mutable UI display.

**Design**

Bridge and chat model use:

1. sequence number;
2. created timestamp;
3. source ID;
4. replace key.

If two messages share a replace key, the highest sequence wins. Ties break by
source ID lexical order for deterministic replay.

**Example**

Original plan:

```text
replace_key = plan:a45ed92a:review
sequence = 42
```

Revision:

```text
replace_key = plan:a45ed92a:review
sequence = 57
```

The UI displays the revision while the board preserves both artifacts.

**Acceptance criteria**

- Replacement is deterministic.
- Older replayed messages do not overwrite newer visible content.
- Missing replace key appends a new row.
- Replacement does not delete board history.
- Supersedes relation and replace key can coexist.

**Unit tests**

- Newer sequence replaces older.
- Older sequence after newer is ignored for display.
- Missing replace key appends.
- Equal sequence tie-break is deterministic.

**Integration tests with vektra/mockery**

- Mock chat model receives out-of-order replacement messages.
- Mock renderer ends with newest content.
- Mock board still returns both artifacts.

**E2E tests**

- Revise plan twice; transcript shows latest plan once.
- Expand artifact history; old revisions remain inspectable.
- Race path: two goroutines emit revisions; UI converges deterministically.

### 2.5 Content size and dereferencing

**Purpose**

Prevent large artifacts from blocking UI rendering or being silently truncated
as if complete.

**Design**

For large artifacts, `Reference` may be a pointer:

```json
{
  "kind": "plan_markdown",
  "reference": "artifact://blob/sha256:...",
  "metadata": {
    "content_inline": false,
    "content_size": 58231
  }
}
```

Initial implementation may inline plans because they are small, but the
contract should support pointers.

**Acceptance criteria**

- Inline content below configured limit renders directly.
- Oversized inline content is either rejected, converted to pointer, or marked
  truncated explicitly.
- UI never renders truncated content as complete.
- Dereference failure produces a visible diagnostic if the artifact was
  user-visible.

**Unit tests**

- Small markdown renders.
- Oversized markdown marks `content_truncated=true`.
- Pointer URI parses.
- Invalid pointer produces diagnostic.

**Integration tests with vektra/mockery**

- Mock blob store returns content.
- Mock blob store times out.
- Mock renderer receives fallback diagnostic.

**E2E tests**

- Large generated report renders through pointer path.
- Blob missing after restart produces a clear error row, not blank space.
- Deadlock path: UI dereference timeout does not block event loop.

---

## Phase 3 - Chat, Approval, and Replay Surfaces

### 3.1 Render `ClaimPresentationMsg` in chat

**Purpose**

Make user-visible artifacts appear in the main transcript as first-class
content.

**Design**

Add chat model handling:

```go
case msg.ClaimPresentationMsg:
    return m, m.handleClaimPresentation(typed)
```

The handler:

- resolves cycle;
- applies placement;
- renders markdown/text/json/diff;
- indexes by replace key;
- preserves source IDs for inspection.

**Example**

The Architect cycle displays:

```text
### Plan

**Status:** ready

1. Create Python package...
...

Straightforward single-task plan. The main tradeoff...
```

**Acceptance criteria**

- Markdown is rendered with existing markdown renderer.
- Plain text is escaped and rendered safely.
- JSON can render as fenced code or compact structured view.
- Diff can render as fenced diff initially.
- Source artifact ID is retained for debug/inspection.

**Unit tests**

- Chat handles markdown message.
- Chat handles replacement by key.
- Chat handles unknown format as plain text.
- Chat preserves source ID.

**Integration tests with vektra/mockery**

- Mock renderer invoked with markdown content.
- Mock renderer failure falls back to plain text.
- Mock chat history receives one entry.

**E2E tests**

- Plan markdown appears in transcript.
- Unsupported format does not crash.
- Replacement after scroll preserves viewport sanity.
- Race path: final response arrives before presentation; handler reorders or
  inserts according to placement.

### 3.2 Active stream embedding

**Purpose**

Keep plan content visually associated with the Architect response that produced
it.

**Design**

If a presentation message belongs to an active stream cycle:

- `before_response`: insert before accumulated final prose;
- `after_response`: append after final prose;
- `inline`: insert immediately;
- `replace`: update current embedded content with same replace key.

If no active stream exists, render a standalone entry.

**Acceptance criteria**

- Active stream embedding works.
- Late presentation still renders standalone if stream closed.
- Placement is deterministic.
- No duplicate markdown on stream finalization.

**Unit tests**

- Active slot receives before-response content.
- Finalization preserves embedded content.
- Late content creates separate row.
- Duplicate source ID is ignored.

**Integration tests with vektra/mockery**

- Mock stream accumulator receives insert.
- Mock stream closes during insert; handler falls back safely.

**E2E tests**

- Architect plan appears before assessment prose.
- Interrupt during planning does not leave half-rendered plan markdown.
- Race path: stream complete and artifact presentation arrive concurrently.

### 3.3 Approval dialog uses artifact source

**Purpose**

Ensure the approval dialog and chat review display the same plan body.

**Design**

Extend plan approval proposal:

```go
PlanArtifactID string
PlanReplaceKey string
PlanText string // migration fallback only
```

Dialog loads content from the artifact when available. `PlanText` remains for
backward compatibility.

**Acceptance criteria**

- Approval dialog displays artifact content when artifact ID is present.
- Dialog falls back to `PlanText` only when artifact is unavailable.
- Dialog metadata links to same plan ID and epoch as artifact.
- Chat and dialog content hashes match.

**Unit tests**

- Proposal with artifact ID uses artifact.
- Missing artifact falls back to text.
- Hash mismatch produces warning.

**Integration tests with vektra/mockery**

- Mock artifact resolver returns plan markdown.
- Mock artifact resolver returns not found; fallback used.
- Mock approval publisher includes artifact ID.

**E2E tests**

- User sees same plan in chat and approval panel.
- Revision updates both chat and approval.
- Negative path: stale approval proposal cannot approve newer plan epoch.

### 3.4 Replay from projection

**Purpose**

Guarantee restart correctness.

**Design**

On UI boot, claims bridge rebuilds presentation entries from board projection:

1. iterate testaments by sequence;
2. emit presentable testament messages;
3. emit presentable artifact messages;
4. apply replacement rules;
5. rebuild active dialogs if pending.

**Acceptance criteria**

- Replay produces the same visible rows as live deltas.
- Replay is idempotent.
- Replacement semantics match live path.
- Old sessions do not leak into active session.

**Unit tests**

- Projection replay emits messages in sequence order.
- Replay duplicate suppression works.
- Replacement replay collapses correctly.

**Integration tests with vektra/mockery**

- Mock board projection with old and new artifacts.
- Mock chat sink receives latest replacement only.

**E2E tests**

- Create plan, restart, verify visible plan.
- Create plan revision, restart, verify latest visible plan and old inspectable
  artifact.
- Corrupted presentation is skipped with telemetry.

---

## Phase 4 - Architect Plan Integration

### 4.1 Emit `plan_markdown` artifact at Ready

**Purpose**

Fix the direct bug: a ready plan must produce a user-reviewable artifact before
the Architect asks the user to review it.

**Design**

When plan reaches Ready:

1. compute `markdown := formatPlanForChat(plan)`;
2. submit testament "Plan ready for review";
3. include `plan_markdown` artifact with chat and approval presentation;
4. include internal `plan_handoff_payload` artifact separately;
5. persist artifact ID on plan or continuation metadata.

**Example**

```go
planArtifact := a.architectArtifact(claims.ArtifactKindPlanMarkdown, formatPlanForChat(plan))
planArtifact.Presentation = &claims.Presentation{
    Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
    Surfaces: []claims.PresentationSurface{
        claims.PresentationSurfaceChat,
        claims.PresentationSurfaceApproval,
    },
    Format: claims.PresentationFormatMarkdown,
    Title: "Plan",
    Placement: claims.PresentationPlacementBeforeResponse,
    ReplaceKey: "plan:" + plan.ID + ":review",
}
```

**Acceptance criteria**

- Every Ready plan has one current `plan_markdown` artifact.
- Artifact content is non-empty.
- Artifact metadata includes plan ID, epoch, task count, content hash.
- Artifact presentation includes `user`, `chat`, `approval`, `markdown`.
- Internal handoff payload remains internal.

**Unit tests**

- Ready plan presentation builder returns valid artifact.
- Empty task list fails review artifact guard.
- Content hash changes when plan markdown changes.
- Replace key stable across revisions of same plan.

**Integration tests with vektra/mockery**

- Mock board `SubmitTestaments` receives both artifacts.
- Mock bridge emits presentation for plan markdown only.
- Mock approval publisher receives plan artifact ID.

**E2E tests**

- User requests planning; plan appears in chat.
- User gets approval dialog using same plan artifact.
- Negative path: simulate board submission failure; Architect does not claim
  plan is shown.
- Race path: plan ready and response complete arrive concurrently; chat shows
  plan before/with final prose.

### 4.2 Add Architect response guard

**Purpose**

Stop misleading final text.

**Design**

Before emitting final response text that includes review phrases:

- detect phrases such as `review the plan`, `take a look at the plan`,
  `plan is ready`;
- verify same cycle has current presentable plan artifact;
- otherwise either emit the artifact first or rewrite response to explain the
  plan could not be rendered.

**Acceptance criteria**

- Guard catches known review phrases.
- Guard is scoped to plan review contexts.
- Guard does not block generic conversations.
- Guard is deterministic and testable.

**Unit tests**

- Review phrase plus artifact passes.
- Review phrase without artifact fails.
- Non-review phrase without artifact passes.
- Stale epoch artifact fails.

**Integration tests with vektra/mockery**

- Mock artifact registry reports artifact exists.
- Mock response publisher verifies guarded order.

**E2E tests**

- No "take a look" without visible plan.
- Forced artifact failure produces honest fallback text.
- Interrupt before artifact publish does not emit misleading final prose.

### 4.3 Plan revisions supersede and replace

**Purpose**

Keep immutable evidence while avoiding duplicate visible plans.

**Design**

On plan revision:

- create new `plan_markdown` artifact;
- link to prior plan artifact with `supersedes`;
- reuse replace key;
- increment epoch metadata;
- update approval proposal to new artifact ID.

**Acceptance criteria**

- New artifact supersedes old artifact by relation.
- UI shows only newest plan for replace key.
- Board retains both artifacts.
- Validators can traverse revision history.

**Unit tests**

- Revision builder adds `supersedes`.
- Replacement key unchanged.
- Epoch incremented.

**Integration tests with vektra/mockery**

- Mock bridge receives old then new artifact and emits replacement.
- Mock validator traverses supersedes chain.

**E2E tests**

- User requests changes; revised plan replaces old visible plan.
- Approval applies to latest epoch only.
- Race path: old artifact delta arrives after new; UI keeps new.

### 4.4 Remove prompt assumptions that UI shows hidden plans

**Purpose**

Prompts currently say "the system renders the plan separately" even when the
artifact pipeline can fail. That should only be true after the invariant exists.

**Design**

Update Architect prompts:

- "After generate_tasks, emit/persist the user-reviewable plan artifact."
- "Once the artifact is available, write brief assessment."
- "Do not duplicate the plan in prose because the artifact is rendered."

**Acceptance criteria**

- Prompt no longer assumes rendering without artifact.
- Prompt instructs artifact-first behavior.
- Prompt keeps brief final assessment.

**Unit tests**

- Prompt tests assert artifact-first wording.
- Prompt tests reject stale "user already sees it" wording unless qualified.

**Integration tests with vektra/mockery**

- Mock planner output final text without artifact is corrected by guard.

**E2E tests**

- Model follows artifact-first flow under normal conditions.
- If model skips artifact, runtime guard repairs or blocks misleading prose.

---

## Phase 5 - Agent Validation and Evidence Use

### 5.1 Validators inspect presentation artifacts as evidence

**Purpose**

Make presentable artifacts useful to agents, services, and the UI uniformly.

**Design**

Validation descriptions can require:

```text
Verify the plan_markdown artifact is present, user-renderable, and matches the
plan tasks and workflow.
```

Programmatic validators (typed Go handlers registered per
`docs/ARTIFACTS_AND_VALIDATIONS.md` §8) read the artifact's typed `Data`
field via the type registry and execute deterministic checks. They
return a structured `Artifact[R]` result that captures the inspection
evidence.

Agentic evaluators (when a validation declares a non-empty `QualityBar`)
query/traverse the board, inspect both `Data` and `Presentation`
metadata, apply the quality bar criteria, and call `evaluate_validation`.
Both validator disciplines access artifacts via the same board API; the
type registry decouples typed-data access from rendering.

**Acceptance criteria**

- Validator can find presentable artifact through normal board queries.
- Validator does not need UI messages.
- Validation failure produces remediation claims.
- Internal artifacts remain available as supporting evidence.

**Unit tests**

- Validation helper finds plan markdown.
- Helper compares plan ID/epoch metadata.
- Helper detects missing chat surface.

**Integration tests with vektra/mockery**

- Mock claims board returns testament with plan markdown.
- Mock evaluator passes validation.
- Mock evaluator fails validation when artifact missing.

**E2E tests**

- Guardian approval sees same artifact as user.
- Inspector validates implementation against plan artifact.
- Negative path: visible artifact content mismatches plan tasks; validation
  fails.

### 5.2 Do not special-case user-visible as trusted

**Purpose**

Prevent UI presentation from becoming a trust signal.

**Design**

Presentation says "render this", not "this is correct". Validations still use
claim descriptions and quality bars.

**Acceptance criteria**

- A visible artifact can fail validation.
- An internal artifact can pass validation.
- Validation code does not shortcut on `audience=user`.

**Unit tests**

- Visible malformed plan fails.
- Internal valid payload passes handoff validation.
- Presentation-only metadata cannot satisfy content validation.

**Integration tests with vektra/mockery**

- Mock validator receives visible artifact but rejects content.
- Mock validator receives internal artifact and passes content.

**E2E tests**

- User-visible but incomplete plan is rendered and then challenged/failed.
- Remediation claim produces revised visible artifact.

### 5.3 Preserve board graph traversal

**Purpose**

Ensure presentable artifacts are discoverable by all agents through the same
relations and query mechanisms.

**Design**

No separate "UI artifact store". If content is pointer-backed, pointer metadata
is still on the board artifact and dereferenced by authorized content readers.

**Acceptance criteria**

- `traverse` from claim reaches testament and presentable artifact.
- `query_claims_board` includes presentable artifact.
- Fabric projection includes artifact kind and metadata needed for awareness.

**Unit tests**

- Traverse includes presentable artifact.
- Query board returns presentation field.
- Fabric harvester indexes kind/title without requiring full reference.

**Integration tests with vektra/mockery**

- Mock traversal returns artifact chain.
- Mock Fabric sink receives activity for presentable artifact.

**E2E tests**

- Later Architect turn recalls prior visible plan via board.
- Academic challenge references visible plan artifact.
- Race path: traversal while revision artifact arrives returns consistent
  snapshot.

---

## Phase 6 - Migration and Cleanup

### 6.1 Migrate `response_text`

**Purpose**

Bring final assistant responses into the same presentation model without
breaking current behavior.

**Design**

Keep `ArtifactKindResponseText` as the artifact `Kind`, and pair it with
a typed `DataType` and `Data` payload per
`docs/ARTIFACTS_AND_VALIDATIONS.md` §4.4. The canonical shape:

```go
// Artifact field values for a response_text artifact
Artifact{
    Kind:     "response_text",
    DataType: "response_text.markdown",  // or "response_text.plain"
    Data:     <serialized ResponseTextPayload>,
    Presentation: &Presentation{
        Audiences: []PresentationAudience{PresentationAudienceUser},
        Surfaces:  []PresentationSurface{PresentationSurfaceChat},
        Format:    PresentationFormatMarkdown,
        Placement: PresentationPlacementAfterResponse,
    },
}

// Typed payload registered with the type registry
type ResponseTextPayload struct {
    Text       string         `json:"text"`
    TokenCount int            `json:"token_count,omitempty"`
    Truncated  bool           `json:"truncated,omitempty"`
    Metadata   map[string]any `json:"metadata,omitempty"`
}
```

**Validator binding**: `response_text` artifacts typically do not have
programmatic validators. They are unstructured prose; deterministic
validation of prose content is not generally meaningful. The artifact
relies on receipt-time structural validation only (non-empty text,
valid `DataType`, etc.).

For agentic claimants that want to assess response quality, a
validation may declare a non-empty `QualityBar` text describing the
quality criteria (e.g., "Response addresses the user's question
directly without hand-waving, cites specific evidence, avoids
unnecessary qualifications"). The claimant agent's quality-bar phase
evaluates the response against the bar per
`docs/ARTIFACTS_AND_VALIDATIONS.md` §7.6.

For non-agentic claimants (services issuing claims that receive
response_text testaments), no quality bar is permitted; the artifact
is treated as opaque evidence and the claim is satisfied based purely
on the receipt validation.

During migration, bridge may continue to route legacy `response_text`
artifacts (those carrying only `Reference` without typed `Data`)
through the existing path; new artifacts use the typed payload.

**Acceptance criteria**

- Existing chat responses still render.
- New response artifacts carry presentation.
- No duplicate response rows.
- Old response artifacts without presentation still render via compatibility
  path.

**Unit tests**

- New response artifact has default presentation.
- Old response artifact still routes.
- Duplicate suppression works.

**Integration tests with vektra/mockery**

- Mock accumulator flush records response presentation.
- Mock bridge emits only one chat message.

**E2E tests**

- Conversation response renders once.
- Restart preserves response.
- Mixed old/new session replay renders correctly.

### 6.2 Deprecate plan-specific side channels

**Purpose**

Reduce duplicate pathways for plan rendering.

**Design**

Keep `PlanUpdateMsg` and proposal `PlanText` during migration. Mark them as
fallbacks once `plan_markdown` artifacts are stable.

**Acceptance criteria**

- Plan artifact is canonical for chat.
- Plan approval can fall back to `PlanText`.
- No code path assumes hidden plan render.
- Deprecated paths have removal issue references.

**Unit tests**

- Artifact path preferred over `PlanText`.
- Fallback path still works.

**Integration tests with vektra/mockery**

- Mock proposal with artifact and text uses artifact.
- Mock proposal with only text uses fallback.

**E2E tests**

- New plan uses artifact path.
- Legacy replay uses fallback.
- No duplicate plan body.

### 6.3 Backfill historical board projections

**Purpose**

Avoid breaking old sessions.

**Design**

For old plan-ready testaments with no `plan_markdown` artifact, optionally
synthesize a transient presentation row from stored plan state. Mark it as
`synthetic=true` and do not write it back as durable evidence unless a migration
tool explicitly does so.

**Acceptance criteria**

- Old sessions open.
- Synthetic rows are visibly marked in metadata.
- Synthetic rows do not appear as real board artifacts.
- Migration tool can write durable artifacts if requested.

**Unit tests**

- Old projection with no presentation does not panic.
- Synthetic builder creates correct row when plan state exists.
- Synthetic row is not traversable as artifact.

**Integration tests with vektra/mockery**

- Mock old board projection.
- Mock plan store returns plan.
- Mock chat sink receives synthetic row.

**E2E tests**

- Open old session with ready plan.
- Verify user can still review plan.
- Verify agents do not treat synthetic UI row as evidence.

---

## Phase 7 - Reliability, Security, and Observability

### 7.1 Metrics and logs

**Purpose**

Make presentation failures diagnosable.

**Design**

Add counters/logs:

- `claims_presentation_artifacts_seen`
- `claims_presentation_messages_emitted`
- `claims_presentation_messages_dropped`
- `claims_presentation_replacements`
- `claims_presentation_invalid`
- `claims_presentation_dereference_failures`

**Acceptance criteria**

- Every drop has a reason.
- Every invalid presentation logs source ID.
- Metrics include surface and format labels.
- Logs do not include huge artifact content.

**Unit tests**

- Invalid presentation increments invalid counter.
- Drop without cycle increments dropped counter.
- Replacement increments replacement counter.

**Integration tests with vektra/mockery**

- Mock metrics sink receives expected increments.
- Mock logger redacts long content.

**E2E tests**

- Force malformed artifact and inspect logs.
- Force missing blob and inspect metric.
- High-volume artifact stream does not flood logs.

### 7.2 Concurrency and deadlock safety

**Purpose**

Claims bridge and chat model must not deadlock under concurrent deltas.

**Design**

Rules:

- Do not enqueue UI messages while holding bridge mutex.
- Do not call board projection while holding chat history mutex.
- Do not dereference large content on the Bubble Tea update path.
- Use sequence ordering for races.

**Acceptance criteria**

- Race detector passes bridge/chat tests.
- Presentation handling is idempotent.
- Out-of-order deltas converge.
- Dead content fetch cannot block UI event loop.

**Unit tests**

- Concurrent replace messages converge.
- Duplicate source IDs ignored.
- Mutex ordering test with fake locks.

**Integration tests with vektra/mockery**

- Mock board blocks during projection; bridge does not hold enqueue lock.
- Mock chat sink blocks; bridge does not deadlock.

**E2E tests**

- Run planning while consultation nested tools emit artifacts.
- Interrupt during artifact presentation.
- Restart during plan artifact emission.
- Race detector e2e if feasible in CI.

### 7.3 Security and rendering safety

**Purpose**

Rendering user-visible artifacts must not execute content or leak secrets.

**Design**

- Markdown renderer must not execute HTML/script.
- JSON rendering must redact configured secret keys.
- Diff rendering must handle binary markers.
- Huge content must be bounded.
- File/path references must not auto-open.

**Acceptance criteria**

- Markdown HTML is escaped or sanitized according to renderer policy.
- Secret keys are redacted in user-visible JSON/diff.
- Binary data is summarized.
- Content size limits are enforced.

**Unit tests**

- Markdown containing `<script>` is safe.
- JSON containing `token`, `api_key`, `password` redacts.
- Binary diff renders summary.
- Oversized content bounded.

**Integration tests with vektra/mockery**

- Mock renderer receives sanitized content.
- Mock redactor called before render.

**E2E tests**

- Artifact with malicious markdown does not execute or corrupt terminal.
- Artifact with secret metadata does not display secret.
- Artifact with massive content does not freeze UI.

### 7.4 Failure-as-artifact for presentation failures

**Purpose**

Stay aligned with the CLAIMS.md errors-as-artifacts principle.

**Design**

If a user-visible artifact cannot be rendered, emit or record a diagnostic
artifact:

```json
{
  "kind": "error_diagnostic",
  "reference": "Could not render plan_markdown artifact: unsupported format",
  "metadata": {
    "source_artifact_id": "artifact-plan-md-1",
    "surface": "chat"
  }
}
```

This diagnostic may itself be user-visible if it explains a missing review
surface.

**Acceptance criteria**

- Rendering failures are not silent.
- Diagnostic links to source artifact.
- Diagnostic does not recurse infinitely if it cannot render.
- Diagnostic is queryable by agents.

**Unit tests**

- Unsupported format produces diagnostic.
- Diagnostic has `derived_from` relation.
- Diagnostic render failure stops after one fallback.

**Integration tests with vektra/mockery**

- Mock renderer fails.
- Mock board records diagnostic artifact.
- Mock chat receives fallback text.

**E2E tests**

- Force renderer error and verify visible diagnostic.
- Verify agent can query diagnostic artifact later.
- Verify no infinite diagnostic loop.

## 9. Implementation Order Summary

1. Add presentation types and constants.
2. Add optional `Presentation` fields to Testament and Artifact.
3. Wire JSON, clone, projection, WAL, and replay.
4. Add helpers to classify user/chat presentability.
5. Add `ClaimPresentationMsg`.
6. Teach ClaimsBridge to emit presentation messages for presentable
   testaments/artifacts.
7. Teach chat to render and replace presentation messages.
8. Emit Architect `plan_markdown` artifacts when plans become Ready.
9. Link approval proposals to the same artifact.
10. Add guards so Architect cannot claim a plan is reviewable without a
    current presentable artifact.
11. Migrate response text and legacy plan paths.
12. Add reliability, security, race, and replay tests.

## 10. Final Architecture Statement

Claims constrain work. Testaments answer claims. Artifacts prove testaments.
Presentation metadata on testaments and artifacts tells UI subscribers how to
render selected evidence to the human user.

Presentation does not create a second channel, does not create user-only
artifacts, and does not reduce agent accessibility. It is a durable,
replayable, board-backed rendering contract layered onto the existing claims
graph.
