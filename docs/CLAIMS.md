# Claims-Based Execution

## 1. Motivation

The current pipeline system uses a state machine protocol (`PipelineProtocolSnapshot`) with sequential, handoff-driven execution. When the orchestrator dispatches a task to a pipeline, the flow is:

1. Inspector defines criteria (`StatusDefiningCriteria`)
2. Tester creates tests (`StatusCreatingTests`)
3. Engineer/Designer implements (`StatusExecuting`)
4. Inspector + Tester validate (`StatusValidating`)

Each transition requires a durable event append to the WAL, reducer replay to reconstruct state, mailbox obligation derivation to determine the next required action, and single-terminal-action guards to prevent double-dispatch. The protocol's reducer state machine cycles through seven states (`Idle -> ChallengeIssued -> ValidationPending -> ValidationProcessed -> FinalizeRequired -> ReadyForOT -> HandoffToOTRequired -> Completed`), with each agent turn constrained by a `PipelineTurnAction` that must match the `requiredAction` lock.

This architecture is fundamentally sequential. Only one agent acts at a time. The Inspector must handoff to the Tester, who must handoff to the Engineer, who must handoff back. Every transition is a durable protocol event. Every handoff is a full Guide route request. Every phase boundary is a terminal-action guard. The result is high latency for work that should be collaborative and parallel.

We are replacing this with a **claims-based execution model** — a universal coordination primitive for the entire agent system. Claims, testaments, and artifacts replace the protocol snapshot, the reducer state machine, the mailbox obligation system, the terminal-action guards, the sequential phase ordering, and the separate challenge/consult skill mechanisms. What remains is one uniform flow: issue claims, do work, submit testaments with artifacts, validate.

---

## 2. Core Concepts

### 2.1 The Hierarchy

```
Action (a set of claims — the unit agents process)
├── Claim (issuer, subject, validations)
│   └── Testament (issuer, subject, artifacts — the response)
│       └── Artifact (proof the claim was satisfied)
```

### 2.2 Action

An **Action** is a set of claims or testaments issued together. Actions are the top-level unit that agents process. Most of the time, agents work with actions — not individual claims. A skill invokes an action, which produces a set of claims against one or more subjects. When subjects respond, their testaments are likewise grouped into a testament action.

**Claim Action**: a set of claims issued together, sharing a `ClaimAction` ID.
**Testament Action**: a set of testaments issued together in response to a claim action, sharing a `TestamentAction` ID.

The full correlation chain:

```
ClaimAction (ID: CA-1, set of claims issued together)
├── Claim 1 (claim_action=CA-1)
├── Claim 2 (claim_action=CA-1)
└── Claim 3 (claim_action=CA-1)
                  │
TestamentAction (ID: TA-1, set of testaments responding to CA-1)
├── Testament 1 (claim=3, claim_action=CA-1, testament_action=TA-1)
├── Testament 2 (claim=2, claim_action=CA-1, testament_action=TA-1)
└── Testament 3 (claim=1, claim_action=CA-1, testament_action=TA-1)
```

You can walk from any testament back to: its testament action, the specific claim it answers, and the claim action that originated everything. You can walk from any claim to the action that produced it.

| Action Type | Issued By | Claims Against | Example |
|---|---|---|---|
| Task | Inspector (assembled by Architect) | Engineer, Designer, Tester | "Implement HS256 JWK deserialization", "Author `aud`/`iss` validation tests" |
| User Prompt | Guide/Orchestrator | Architect or classifier | "Intent classify this request", "Decompose into claims" |
| Challenge | Any agent | Peer agent | "Your token validation has a timing side-channel" |
| Consultation | Any agent | Peer agent | "What's your approach to shared test fixtures?" |
| Corrective | System/Inspector | Misbehaving agent | "Acquire file scope before writing — here are the claims" |
| Archival | Any agent | Scribe/knowledge agent | "Summarize architect's last context window" |

Actions unify what were previously separate mechanisms (task dispatch, `challenge_agent`, `challenge_peer`, `consult_peer`, coordination skills). A challenge is just an action whose claims assert a problem. A consultation is just an action whose claims request information. They all flow through `post_action` with the appropriate action type.

### 2.3 Claim

A **Claim** is a precise, atomic, specific assertion issued by one agent against another. A claim has:

- **Issuer**: the agent that made the claim (the claimant). For initial task claims, the Inspector is the issuer — the Architect assembles the claim set during planning, but the Inspector formally issues them when the board is populated.
- **Subject**: the agent the claim is made against — who must satisfy it.
- **Validations**: the quality gates. Each validation is a single precise, atomic means of verifying the claim, paired with a quality bar statement.

Claims are NOT vague task descriptions. They are specific, testable assertions about one concrete behavior or implementation detail:

| Level | Example | What it is |
|---|---|---|
| Action (set of claims) | "Implement JWT middleware" | A task — the Architect assembles this |
| Claim | "Implement HS256 JWK deserialization" | One atomic unit of work |
| Claim | "Validate `aud` and `iss` claims against user email" | Another claim in the same action |

A claim in a non-pipeline context might be: "Summarize architect's last context window" (issued against the Scribe), or "Provide best practices for JWK implementation" (issued against the Academic as a consultation).

### 2.4 Validation

A **Validation** is a single precise, thorough, atomic means of verifying a claim, paired with a quality bar statement describing the standards that must be met. Validations are concrete and verifiable from artifacts alone — the issuer validates by examining the testament's artifacts against the validation criteria, not by asking "did you do it?"

Examples for the claim "Implement HS256 JWK deserialization":

| Validation | Quality Bar |
|---|---|
| "A JWK with a valid HS256 key deserializes successfully" | "Returns a typed `JWK` struct with `Algorithm`, `KeyID`, and `KeyBytes` fields populated. No silent fallbacks to other algorithms." |
| "A JWK with an unsupported algorithm returns `ErrUnsupportedAlgorithm`" | "Error type is sentinel (not string comparison). Error message includes the unsupported algorithm name for debugging." |

Examples for the claim "Summarize architect's last context window":

| Validation | Quality Bar |
|---|---|
| "Archivalist acknowledges receipt" | "Archivalist ingestion response with status `ok` and a valid entry ID" |
| "Document DB ingestion returns success" | "Document DB write response with inserted document ID and no error" |
| "Knowledge graph vectors stored" | "Vector store response with embedding ID and dimensionality matching the configured model" |

#### Validation Types

| Type | Semantics | Evaluation |
|---|---|---|
| `receipt` | Proof of delivery — the testament arriving IS the proof. | **Auto-passed** by the board when a testament is submitted. No agent action needed. |
| `test` | Automated test execution verifies the claim. | Agent runs tests, inspects output artifacts, calls `evaluate_validation`. |
| `inspection` | Code review, quality audit, or manual verification. | Agent reads code artifacts, applies quality bar criteria, calls `evaluate_validation`. |
| `integration` | End-to-end system behavior verification. | Agent exercises the integration, checks output artifacts, calls `evaluate_validation`. |
| `contract` | API contract or interface compliance. | Agent verifies artifacts against contract spec, calls `evaluate_validation`. |
| `design` | Design review — visual, architectural, or UX. | Agent reviews design artifacts against quality bar, calls `evaluate_validation`. |
| `regression` | No existing behavior broken. | Agent runs regression tests, inspects for regressions in artifacts, calls `evaluate_validation`. |

Receipt validations are mechanical — the board auto-passes them when a testament links to the claim via a `RelationshipClaim` relation. All other validation types are **agentic** — the evaluator agent uses its full skill surface to assess the testament's artifacts against the validation's Description and QualityBar, then calls `evaluate_validation` with a pass/fail verdict and reason.

#### Validations as Agent Instructions

Validations are not passive checkboxes. They are **instructions to the evaluator agent**. The Description tells the agent *what to check*. The QualityBar tells the agent *what standard to meet*. Together they define the agent's evaluation task.

Well-written validations direct the agent to use specific skills and evidence:

| Validation Description | QualityBar | What the Agent Does |
|---|---|---|
| "Run the test suite and verify all tests pass" | "Zero test failures, coverage ≥ 80%" | Agent invokes test runner, reads test output artifacts, checks coverage numbers |
| "Code review for OWASP top 10 vulnerabilities" | "No critical or high-severity findings" | Agent reads code artifacts, applies security analysis, checks for injection/XSS/etc. |
| "Cross-reference implementation with academic research on HS256 best practices" | "Implementation follows RFC 7517 §5 key representation" | Agent queries board for academic consultation testaments, compares artifacts |
| "Verify the design matches the architect's plan" | "All plan requirements addressed, no omissions" | Agent traverses to architect's plan claims, compares testament artifacts |

#### Evidence Beyond the Claim

The evaluator agent is not limited to the testament's own artifacts. Agents can — and should — use the full board as evidence:

- **Non-claim-associated testaments** from other agents in the session provide contextual evidence. An inspector evaluating an engineer's implementation can reference the academic's research testaments, the librarian's workspace analysis, or the tester's prior test results.
- **`query_claims_board`** returns all claims, testaments, and artifacts on the board, not just those linked to the current claim.
- **`traverse`** walks the graph to discover related evidence — prior claims on the same scope, testaments from consultation exchanges, artifacts from earlier iterations.

The validation's Description and QualityBar should instruct the agent about what additional evidence to seek. A validation like "Verify implementation handles edge cases documented in the academic's prior research" tells the agent to traverse the board for academic consultation testaments and use those as evidence in its evaluation.

### 2.5 Testament

A **Testament** is the uniform response to a claim. When a subject completes work on a claim, they issue a testament back to the claim's issuer with artifacts proving the claim was satisfied.

- **Issuer**: the agent that created the testament (typically the claim's subject).
- **Subject**: the agent the testament is responding to (typically the claim's issuer).
- **Artifacts**: data, references, or other proof that the claim was satisfied.

The testament is a concrete statement of what was done:

| Claim | Testament |
|---|---|
| "Implement HS256 JWK deserialization" | "Implemented JWK validation using HS256 in `DeserializeHS256JWK()` method" |
| "Validate `aud` and `iss` claims against user email" | "Added `ValidateAudIss()` with email matching in `services/auth/claims.go`" |
| "Summarize architect's last context window" | "Submitted last architect context window at 2:01AM 04-21-2026" |
| "Provide best practices for JWK implementation" | "Researched sources for best JWK implementation methods" |

### 2.6 Artifact

An **Artifact** is a piece of evidence attached to a testament. Artifacts are polymorphic — the `Kind` field discriminates how to interpret the reference. **Errors are artifacts.** A failed operation does not return an error to the caller — it produces a testament with error artifacts. The issuer evaluates the testament, sees the error artifacts, and decides what to do. Nothing is silently dropped because every outcome — success or failure — is a testament with proof.

| Kind | Example | Used By |
|---|---|---|
| `code_reference` | `services/auth/jwk.go:47-89` | Engineer, Designer |
| `test_output` | `TestDeserializeHS256JWK_ValidKey PASS` | Tester |
| `error` | `ErrUnsupportedAlgorithm: HS384 not supported` | Any — captures operation failures |
| `error_trace` | Stack trace from panicked operation | Any — captures crash context |
| `error_diagnostic` | `timeout after 30s waiting for DB connection` | Any — captures environmental failures |
| `research_paper` | Academic research output on JWK best practices | Academic |
| `reference_links` | URLs to RFC 7517, stdlib `crypto/hmac` docs | Academic, Librarian |
| `knowledge_graph_vectors` | Embedding IDs from vector store | Knowledge agents |
| `document_db_snippet` | Document DB entry ID and content excerpt | Archivalist |
| `ingestion_response` | Archivalist/DB/KG receipt responses | Scribe |
| `design_asset` | Component mockup reference, design token mappings | Designer |
| `diagnosis_report` | Stack traces, timing analysis, root cause | Tester, Inspector |
| `lint_output` | Linter findings, type checker results | Inspector |
| `a11y_audit` | WCAG compliance results, contrast ratios | Inspector |
| `diff` | VFS diff of files changed | Any |

The system does not constrain what artifact kinds exist — new kinds can be added without schema changes. The `Kind` field is a string, not an enum.

**Errors-as-artifacts principle:** When an operation fails — a test execution crashes, a file write is denied, an LLM call times out, an ingestion returns an error — the agent captures the failure as an artifact on the testament it submits. The testament's `Summary` describes what happened ("Attempted HS256 deserialization but encountered unsupported algorithm"), and the artifacts carry the error details (`kind: "error"`, `reference: "ErrUnsupportedAlgorithm: HS384"`). The issuer evaluates the testament, sees the error artifacts, and decides: remediate with new claims, retry, or escalate. No error is ever silently dropped — every failure is durable, auditable, and visible on the board.

### 2.7 Claims as Constraints

If the claims and their validations are precise enough, and agents can see the board state via `query_claims_board` and ambient context, agents naturally do the right thing. The claims ARE the constraints. The board IS the state machine. The validations define what must be true.

There is no separate enforcement engine or corrective claims machinery. An agent acting "out of order" means the claims weren't specific enough or the agent couldn't see the board — fix the claims or fix the visibility, not bolt on an enforcement layer. Adding enforcement on top of claims reimplements the protocol state machine that claims replace.

**Design principle:** The Architect produces claims specific enough that the agents' work is fully constrained by the claims themselves. The Inspector's validations are precise enough that the quality bar is unambiguous. The board's phase is visible enough that agents know what to do. No additional machinery is needed.

### 2.8 The Validation Flow

The claim's issuer (the claimant) validates the testament and its artifacts against the claim's validations:

```
1. Issuer creates Claim (with validations) against Subject
2. Subject does work
3a. Work succeeds → Subject issues Testament with success artifacts
3b. Work fails → Subject issues Testament with error artifacts
4. Receipt validations auto-pass when the testament is submitted
5. The evaluator agent receives the TestamentDelta with:
   - The testament (summary, artifacts)
   - The parent claim (title, description, scope)
   - All pending validations (description, quality bar, type, required)
6. For each pending validation, the agent:
   a. Reads the validation's Description to understand WHAT to check
   b. Reads the QualityBar to understand WHAT STANDARD to meet
   c. Uses its skills to gather evidence:
      - Read testament artifacts directly
      - Query the board for related testaments/artifacts as additional evidence
      - Traverse the graph for prior work, consultations, research
      - Run tests, read files, execute commands as needed
   d. Calls evaluate_validation with:
      - claim_id, validation_id
      - verdict: "passed" or "failed"
      - reason: why the quality bar was or wasn't met
7a. All required validations pass → Claim auto-accepted by board
7b. Any required validation fails → Agent posts remediation claims
```

Both success and failure produce testaments. A testament with error artifacts is not a system error — it's a structured report of what went wrong, with the error details as auditable proof. The evaluator sees exactly what failed and can issue precise remediation claims targeting the specific failure.

**Agentic validation means the agent does the work.** The system delivers the context (testament + artifacts + parent claim + pending validations). The agent decides how to validate — read code, run tests, consult peers, query the board for additional evidence. The validation's Description and QualityBar are the agent's instructions. The agent's full skill surface is available.

**Evidence is not limited to the claim's own artifacts.** The agent can query the board for ANY testament or artifact from the session — prior consultations, academic research, earlier implementation attempts. A validation that says "verify implementation follows the patterns from the academic's research" directs the agent to find and use those non-claim-associated testaments as evidence.

**Receipt validations are the exception.** Receipt-type validations auto-pass at the board level when a testament is submitted — the testament arriving IS the proof. No agent action needed. All other validation types require agentic evaluation.

For initial task claims, the Inspector is the issuer and evaluator. The Inspector evaluates testaments from Engineer/Designer/Tester against each claim's validations using its full skill surface. The Tester may also validate test-type validations by running tests and submitting their own evaluation. If validations fail, the Inspector or Tester issues new claims (remediation).

---

## 3. Architectural Decision: Sovereign Store + Fabric Projection

### The Question

The Fabric's `activity.Append()` path is synchronous and durable — activities at Medium and Coarse resolution are written to SQLite inside `RunInWriteTx` before `Append` returns. We could model claims purely as Fabric activities, scoped by `task_id`, and reconstruct board state via a lens query. This would eliminate the need for a separate persistence layer.

### Why a Sovereign Store

Three reasons favor a dedicated sovereign store for the claims board:

**1. The Fabric is architecturally a read/observation layer.**

From `core/activity/types.go`:

> *"Sovereign systems own their own data... the fabric receives projections of their state changes via instrumentation chokepoints, never replaces their storage. Failure mode of the fabric = lose cross-cutting lenses; the sovereign systems keep working unchanged."*

Every existing sovereign system follows this pattern:

| Sovereign System | Owns | Projects to Fabric as |
|---|---|---|
| Decision Manifest | Typed decisions | `decision_declared`, `decision_promoted` |
| Coordination Service | Scope claims, artifacts, reviews | `claim_acquired`, `artifact_published` |
| Pipeline Protocol | Protocol state, durable events | `validation_started`, `validation_accepted` |
| **Claims Board (new)** | **Claims, testaments, artifacts, board state** | **`claim_issued`, `testament_submitted`, `artifact_published`, etc.** |

**2. Phase transitions need transactional consistency.** "Have all claims received accepted testaments?" requires an atomic check across the full board. A sovereign store with its own `sync.RWMutex` provides transactional reads under the write lock.

**3. Deterministic recovery.** The existing `durableProtocolLog` pattern provides structured WAL replay with checkpointing, well-tested across the pipeline protocol, coordination service, and decision manifest.

### What the Fabric Provides

The claims board projects every mutation to the Fabric via an amplifier. Claims, testaments, and artifacts are ALL published as Fabric activities, giving:

- **Cross-pipeline visibility**: agents in pipeline B see pipeline A's claims and testaments via `query_peer_activity` and ambient context
- **Ambient context**: the `AmbientEnvelope` includes a `ClaimsBoardDigest` showing claims, testaments, peer progress, and recent activity
- **Causal tracing**: every claim, testament, and artifact is a Fabric activity with full causal chain
- **Lens queries**: `query_claims_board`, `query_peer_claims`, `inspect_claim_conflicts` backed by the activity stream

### Task ID Scoping

Each claim carries a `TaskID` field. Pipeline agents receive their board's `TaskID` in their dispatch context. The Fabric amplifier tags every activity with `Subject.Coordinates["task_id"]` so cross-pipeline queries can filter by task.

### Substrate Integration (per docs/CLUSTER.md)

The claims board is implemented as a substrate subject — *the canonical
form prescribed by CLUSTER.md §11.4*. The hand-rolled `durableProtocolLog`,
custom Fabric amplifier, and bespoke board persistence collapse into one
primitive: a typed substrate subject backed by per-namespace Raft.

| Pre-substrate (this doc, sections 4-13) | Substrate (per CLUSTER.md) |
|---|---|
| `core/claims/board_durable.go` (custom WAL) | `sylk://session/<id>/claims/v3` (CLUSTER.md §11.4) |
| `core/claims/board.go` (in-memory state with `sync.RWMutex`) | Per-namespace Raft state machine (CLUSTER.md §3, §7) |
| `core/claims/board_amplifier.go` (Fabric activity emission) | Substrate consumer reading the claims subject (CLUSTER.md §11.3 fabric lens consumer) |
| `core/activity/lenses/ambient.go` (ad-hoc digest assembly) | Continuous queries over the claims subject + sibling subjects (CLUSTER.md §31.4 differential dataflow) |
| Per-process WAL file | Replicated, content-addressed, audit-grade (CLUSTER.md §17.1 accountability) |
| Single-host coordination | Multi-DC + federated (CLUSTER.md §20) |

Concretely, for every claim/testament/artifact mutation:

1. **Wire**: SWF-encoded body (CLUSTER.md §4.4-bis), zstd-compressed with
   the claims schema's pre-trained dictionary (CLUSTER.md §25.5), signed
   per CLUSTER.md §17.1, dispatched via adaptive transport selection
   (CLUSTER.md §4.5).
2. **Durability**: per-session Raft state machine; entries content-
   addressed by BLAKE3; cursor-resumable (CLUSTER.md §8); time-travelable
   (CLUSTER.md §12.1).
3. **Distribution**: cross-pipeline visibility happens via §11.4's
   substrate consumer reading the claims subject — no separate amplifier
   path. The Fabric's role becomes "consume the claims subject and
   render lens views," not "project sovereign state into a parallel
   stream."
4. **Audit**: every mutation is signed by the issuing agent's SVID;
   replicas verify before applying; provenance certificates (§31.9) carry
   end-to-end auditability.
5. **Multi-tenancy**: claims subjects scoped to `sylk://tenant/<t>/
   session/<id>/claims/v3`; per-tenant quotas (CLUSTER.md §22.1) and
   encryption envelope (§21.2) apply uniformly.

The "sovereign store + fabric projection" pattern doesn't go away — it
becomes literal: sovereign store = the Raft state machine for the claims
subject; fabric projection = lens consumers reading the subject. Same
mental model, substrate-grade durability and replication.

---

## 4. Core Data Model

> **Wire encoding note**: every type in this section is registered as a
> substrate schema (CLUSTER.md §3.3), serialized via Sylk Wire Format
> (CLUSTER.md §4.4-bis) — codegen'd zero-copy structural layout — and
> compressed with the claims-schema-trained zstd dictionary (CLUSTER.md
> §25.5). Determinism rules (canonical map ordering, smallest-int
> encoding, NFC strings) are enforced by codegen. Wire bytes are a pure
> function of struct content; same struct → same bytes everywhere.
> Claims interactions are also session-typed (CLUSTER.md §31.21): the
> claim → testament → validation → acceptance/rejection grammar is
> declared at registration; out-of-order publishes are wire-rejected by
> the substrate, not by the application.

### 4.1 Supporting Types

```go
package claims

import "time"

// Relation expresses a relationship between any two entities in the
// claims system. All structural, causal, and agent relationships are
// encoded uniformly as Relations — there are no special-case fields
// for issuer, subject, parent action, dependencies, or supersession.
type Relation struct {
    // Related is the ID of the related entity.
    Related string `json:"related"`

    // RelatedType identifies what kind of entity Related points to.
    // One of: "action", "claim", "testament", "validation",
    // "artifact", "agent".
    RelatedType string `json:"related_type"`

    // Relationship describes how the entities are related.
    // Open string — not a closed enum. Common values:
    //   Agent relationships:  "issuer", "subject", "evaluator"
    //   Structural:           "claim_action", "testament_action",
    //                         "claim", "testament"
    //   Causal/semantic:      "supersedes", "depends_on", "caused_by",
    //                         "refines", "conflicts_with", "derived_from",
    //                         "reviews", "amends"
    Relationship string `json:"relationship"`
}

// StatusChange records a single status transition on a stateful object
// (Action, Claim, or Validation). Every transition is recorded — the
// full lifecycle is auditable.
type StatusChange struct {
    From    string    `json:"from"`     // previous status value
    To      string    `json:"to"`       // new status value
    Reason  string    `json:"reason"`   // why the transition happened
    AgentID string    `json:"agent_id"` // which agent instance caused it
    Changed time.Time `json:"changed"`  // UTC timestamp
}

// ClaimScopeEntry identifies one element of a claim's affected scope.
type ClaimScopeEntry struct {
    Kind string `json:"kind"` // "file", "symbol", "api", "test_surface", "component", "ux_surface"
    Key  string `json:"key"`  // path, symbol name, endpoint, surface ID, etc.
}
```

### 4.2 Universal Base Fields

Every type in the claims system (Action, Claim, Testament, Validation, Artifact) carries these nine fields:

| Field | Type | Description | Reasoning | Example |
|---|---|---|---|---|
| `ID` | `string` | Globally unique identifier (UUID) | Stable identity for Relations, WAL events, and Fabric activities | `"c7a3e1b4-9f2d-4e8a-b5c1-3d7f9a2e4b6c"` |
| `AgentID` | `string` | Specific agent instance that spawned this object | Same agent type can have multiple replicas or handoff successors. Instance-level correlation for debugging, capacity tracking, and replica identification | `"engineer-pipeline-a3f2"` |
| `SessionID` | `string` | User-facing session scope | Denormalized for Fabric queries — "show me all claims in session X" without board join | `"session_2026-04-21_a8b3"` |
| `PipelineID` | `string` | Pipeline instance scope | Multiple pipelines run concurrently. Denormalized for cross-pipeline Fabric queries | `"pipe_jwt_auth_001"` |
| `TaskID` | `string` | DAG task node scope | Primary filter for board-level queries and Fabric activity tagging | `"task_implement_jwt"` |
| `Sequence` | `uint64` | Board-scoped monotonic counter | Optimistic concurrency, subscription polling ("events after seq 47"), deterministic ordering when timestamps collide | `142` |
| `Relations` | `[]Relation` | All relationships — agent, structural, causal, semantic | Unifies issuer/subject, parent correlations, dependencies, supersession, and cross-object references into one queryable mechanism | See section 4.3 |
| `Created` | `time.Time` | UTC creation timestamp. Immutable | Ordering, staleness detection, audit trail | `2026-04-21T02:01:00Z` |
| `Accessed` | `time.Time` | UTC last-access timestamp. Updated on every read or mutation | LRU eviction, staleness detection, activity-based ordering | `2026-04-21T02:03:45Z` |

### 4.3 Relations Reference

All structural, causal, and agent relationships are expressed as `Relation` entries. No dedicated fields for issuer, subject, parent action, dependencies, or supersession.

**Agent relationships:**

| Relationship | Meaning | Example |
|---|---|---|
| `issuer` | Agent that created/issued this object | `{Related: "inspector-pipe-a3f2", RelatedType: "agent", Relationship: "issuer"}` |
| `subject` | Agent this object is directed at | `{Related: "engineer-pipe-b7c4", RelatedType: "agent", Relationship: "subject"}` |
| `evaluator` | Agent that evaluated a validation | `{Related: "inspector-pipe-a3f2", RelatedType: "agent", Relationship: "evaluator"}` |

**Structural relationships (action/claim/testament/artifact grouping):**

| Relationship | Meaning | Example |
|---|---|---|
| `claim_action` | The claim action this object belongs to | `{Related: "action_001", RelatedType: "action", Relationship: "claim_action"}` |
| `testament_action` | The testament action this object belongs to | `{Related: "taction_001", RelatedType: "action", Relationship: "testament_action"}` |
| `claim` | The specific claim this object responds to or belongs to | `{Related: "claim_003", RelatedType: "claim", Relationship: "claim"}` |
| `testament` | The testament this artifact belongs to | `{Related: "testament_007", RelatedType: "testament", Relationship: "testament"}` |

**Causal and semantic relationships:**

| Relationship | Meaning | Example |
|---|---|---|
| `supersedes` | Replaces the related object | `{Related: "claim_002", RelatedType: "claim", Relationship: "supersedes"}` |
| `depends_on` | Cannot proceed until related is satisfied | `{Related: "claim_005", RelatedType: "claim", Relationship: "depends_on"}` |
| `caused_by` | Created in response to the related object | `{Related: "action_003", RelatedType: "action", Relationship: "caused_by"}` |
| `refines` | Narrows or clarifies the related object | `{Related: "claim_001", RelatedType: "claim", Relationship: "refines"}` |
| `conflicts_with` | Contradicts or competes with the related | `{Related: "claim_008", RelatedType: "claim", Relationship: "conflicts_with"}` |
| `derived_from` | Content was derived from the related object | `{Related: "artifact_004", RelatedType: "artifact", Relationship: "derived_from"}` |
| `reviews` | Evaluates or assesses the related object | `{Related: "artifact_012", RelatedType: "artifact", Relationship: "reviews"}` |
| `amends` | Modifies but does not replace the related | `{Related: "testament_005", RelatedType: "testament", Relationship: "amends"}` |

### 4.4 Action

An Action is a set of claims or testaments issued together. Actions are the top-level unit agents process.

```go
// Action is a set of claims or testaments issued together. A claim
// action groups claims; a testament action groups testaments responding
// to a claim action.
//
// Relations encode: issuer (agent), caused_by (parent action or trigger),
// and the claims/testaments belonging to this action.
type Action struct {
    // ── Universal base (9 fields) ──
    ID         string     `json:"id"`
    AgentID    string     `json:"agent_id"`
    SessionID  string     `json:"session_id"`
    PipelineID string     `json:"pipeline_id"`
    TaskID     string     `json:"task_id"`
    Sequence   uint64     `json:"sequence"`
    Relations  []Relation `json:"relations"`
    Created    time.Time  `json:"created"`
    Accessed   time.Time  `json:"accessed"`

    // ── Action-specific fields ──

    // Type classifies the action: task, challenge, consultation,
    // corrective, archival, prompt.
    Type ActionType `json:"type"`

    // Status tracks the action's aggregate lifecycle.
    Status ActionStatus `json:"status"`

    // StatusHistory records every status transition with
    // from/to/reason/who/when.
    StatusHistory []StatusChange `json:"status_history"`

    // Priority affects scheduling order when multiple actions compete
    // for agent attention. Higher = processed first. Corrective actions
    // should have elevated priority.
    Priority int `json:"priority,omitempty"`
}
```

### 4.5 Claim

A Claim is a precise, atomic, specific assertion issued by one agent against another.

```go
// Claim is a precise, atomic, specific assertion. The issuer makes the
// claim; the subject must satisfy it by submitting a Testament with
// Artifacts. The issuer then validates the artifacts against the claim's
// Validations.
//
// Relations encode: issuer (agent), subject (agent), claim_action
// (parent action), depends_on (prerequisite claims), supersedes
// (replaced claim).
type Claim struct {
    // ── Universal base (9 fields) ──
    ID         string     `json:"id"`
    AgentID    string     `json:"agent_id"`
    SessionID  string     `json:"session_id"`
    PipelineID string     `json:"pipeline_id"`
    TaskID     string     `json:"task_id"`
    Sequence   uint64     `json:"sequence"`
    Relations  []Relation `json:"relations"`
    Created    time.Time  `json:"created"`
    Accessed   time.Time  `json:"accessed"`

    // ── Claim-specific fields ──

    // Title is a concise human-readable label.
    Title string `json:"title"`

    // Description is a thorough description of what must be done.
    // Must be precise and atomic — not "implement JWT middleware" but
    // "implement HS256 JWK deserialization".
    Description string `json:"description"`

    // Scope lists files, symbols, APIs, surfaces this claim touches.
    Scope []ClaimScopeEntry `json:"scope"`

    // ActionType classifies the parent action (denormalized for fast
    // filtering without joining to the action).
    ActionType ActionType `json:"action_type"`

    // Status tracks the claim's lifecycle: pending, in_progress,
    // testified, accepted, rejected, superseded.
    Status ClaimStatus `json:"status"`

    // StatusHistory records every status transition with reason.
    // Replaces the old RejectionReason field — rejection is a
    // transition where To=="rejected" and Reason explains why.
    StatusHistory []StatusChange `json:"status_history"`

    // Priority affects scheduling within the action. Higher = worked
    // first. Claims on the critical path (others depend on them)
    // should have elevated priority.
    Priority int `json:"priority,omitempty"`

    // Deadline is the UTC time by which the subject should submit a
    // testament. Critical for consultations (bounded wait) and
    // corrective claims. Zero means no deadline.
    Deadline time.Time `json:"deadline,omitempty"`

    // Tags are free-form labels for categorization beyond ActionType.
    // Examples: "blocking", "security", "performance", "ux-critical".
    Tags []string `json:"tags,omitempty"`

    // Iteration records which implementation-validation cycle created
    // this claim. 0 = initial, 1+ = remediation/corrective.
    Iteration int `json:"iteration"`

    // Validations are the quality gates for this claim. Structural
    // ownership — each Validation belongs to exactly one Claim.
    // Relations encode cross-cutting relationships (evaluator, etc.);
    // this field encodes parent-child ownership.
    Validations []*Validation `json:"validations"`
}
```

### 4.6 Testament (immutable)

A Testament is the uniform response to a claim. Once created, never modified — corrections produce a new testament with a `supersedes` or `amends` relation.

```go
// Testament is the uniform response to a claim. Immutable — once
// created, never modified. The issuer evaluates the testament's
// artifacts against the claim's validations.
//
// Relations encode: issuer (agent), subject (agent), claim (the claim
// being answered), claim_action (originating action), testament_action
// (this testament's group), supersedes/amends (prior testament).
type Testament struct {
    // ── Universal base (9 fields) ──
    ID         string     `json:"id"`
    AgentID    string     `json:"agent_id"`
    SessionID  string     `json:"session_id"`
    PipelineID string     `json:"pipeline_id"`
    TaskID     string     `json:"task_id"`
    Sequence   uint64     `json:"sequence"`
    Relations  []Relation `json:"relations"`
    Created    time.Time  `json:"created"`
    Accessed   time.Time  `json:"accessed"`

    // ── Testament-specific fields ──

    // Summary is a concrete statement of what was done.
    // Examples:
    //   "Implemented JWK validation using HS256 in DeserializeHS256JWK()"
    //   "Submitted last architect context window at 2:01AM 04-21-2026"
    //   "Researched sources for best JWK implementation methods"
    Summary string `json:"summary"`

    // Confidence indicates how sure the issuer is in the testament's
    // completeness. Aligns with the Fabric's confidence model:
    // hint (partial/uncertain), tentative (best effort), committed
    // (confident), consensus (peer-validated).
    Confidence string `json:"confidence,omitempty"`

    // Duration is wall-clock time from claim assignment to testament
    // submission. Useful for capacity planning, identifying claims
    // scoped too broadly, and agent performance tracking.
    Duration time.Duration `json:"duration,omitempty"`

    // Artifacts are the proof attached to this testament. Structural
    // ownership — each Artifact belongs to exactly one Testament.
    Artifacts []*Artifact `json:"artifacts"`
}
```

### 4.7 Artifact (immutable)

An Artifact is evidence attached to a testament. Once created, never modified — updates produce a new artifact with a `supersedes` relation via a new testament.

```go
// Artifact is evidence attached to a testament. Immutable — once
// created, never modified. "Updating" evidence means the subject
// submits a new testament with new artifacts carrying a supersedes
// relation to the prior testament.
//
// Relations encode: issuer (agent), testament (parent), claim,
// claim_action, testament_action, derived_from (source artifact).
type Artifact struct {
    // ── Universal base (9 fields) ──
    ID         string     `json:"id"`

    // TestamentID is the structural parent — the testament this
    // artifact belongs to. Every Artifact has exactly one parent
    // Testament.
    TestamentID string    `json:"testament_id"`

    AgentID    string     `json:"agent_id"`
    SessionID  string     `json:"session_id"`
    PipelineID string     `json:"pipeline_id"`
    TaskID     string     `json:"task_id"`
    Sequence   uint64     `json:"sequence"`
    Relations  []Relation `json:"relations"`
    Created    time.Time  `json:"created"`
    Accessed   time.Time  `json:"accessed"`

    // ── Artifact-specific fields ──

    // Kind classifies the artifact. Free-form string — new kinds
    // added without schema changes. Common kinds: "code_reference",
    // "test_output", "research_paper", "reference_links",
    // "knowledge_graph_vectors", "document_db_snippet",
    // "ingestion_response", "design_asset", "diagnosis_report",
    // "lint_output", "a11y_audit", "diff".
    Kind string `json:"kind"`

    // Reference is the content or pointer. Interpretation depends
    // on Kind:
    //   code_reference: "services/auth/jwk.go:47-89"
    //   test_output: "TestDeserializeHS256JWK_ValidKey PASS (0.003s)"
    //   ingestion_response: JSON of archivalist receipt
    //   knowledge_graph_vectors: embedding ID from vector store
    Reference string `json:"reference"`

    // Metadata carries kind-specific structured data beyond the
    // reference string.
    Metadata map[string]any `json:"metadata,omitempty"`

    // ContentHash is SHA-256 of the artifact's content. Enables
    // deduplication and integrity verification. Computed once at
    // creation (immutable).
    ContentHash string `json:"content_hash,omitempty"`

    // Size is byte size of the artifact's content. Resource
    // management and unbounded-growth prevention.
    Size int64 `json:"size,omitempty"`

    // Ephemeral marks artifacts that are transient (test output,
    // build logs) vs durable (code references, design assets).
    // Ephemeral artifacts can be evicted after the iteration completes.
    Ephemeral bool `json:"ephemeral,omitempty"`
}
```

### 4.8 Validation (stateful)

A Validation is a quality gate on a claim. Validations have a lifecycle tracked via Status and StatusHistory.

```go
// Validation is a single precise, atomic means of verifying a claim.
// Stateful — has a lifecycle tracked via Status and StatusHistory.
// Validations are verifiable from artifacts alone.
//
// Relations encode: issuer (agent who created the validation), claim
// (parent claim), claim_action, evaluator (agent who evaluated),
// reviews (artifact IDs examined during evaluation).
type Validation struct {
    // ── Universal base (9 fields) ──
    ID         string     `json:"id"`

    // ClaimID is the structural parent — the claim this validation
    // belongs to. Every Validation has exactly one parent Claim.
    ClaimID    string     `json:"claim_id"`

    AgentID    string     `json:"agent_id"`
    SessionID  string     `json:"session_id"`
    PipelineID string     `json:"pipeline_id"`
    TaskID     string     `json:"task_id"`
    Sequence   uint64     `json:"sequence"`
    Relations  []Relation `json:"relations"`
    Created    time.Time  `json:"created"`
    Accessed   time.Time  `json:"accessed"`

    // ── Validation-specific fields ──

    // Description is the precise, atomic validation method.
    // Examples:
    //   "A JWK with a valid HS256 key deserializes successfully"
    //   "Archivalist acknowledges receipt with status ok"
    Description string `json:"description"`

    // QualityBar is the standards/expectations statement.
    // Examples:
    //   "Returns typed JWK struct with Algorithm, KeyID, KeyBytes.
    //    No silent fallbacks to other algorithms."
    //   "Ingestion response with status ok and valid entry ID"
    QualityBar string `json:"quality_bar"`

    // Type classifies the check: test, inspection, integration,
    // contract, design, regression, receipt.
    Type ValidationType `json:"type"`

    // Status tracks evaluation lifecycle: pending, in_progress,
    // passed, failed, skipped.
    Status ValidationStatus `json:"status"`

    // StatusHistory records every transition with reason. The
    // evaluation verdict is captured here — no separate
    // ValidationResult struct needed. The transition to passed/failed
    // carries the evaluator's summary as Reason, and the evaluator
    // agent is recorded in AgentID. Artifact references are captured
    // as "reviews" Relations added at evaluation time.
    StatusHistory []StatusChange `json:"status_history"`

    // Required marks whether this validation is mandatory for claim
    // acceptance. Advisory validations (Required=false) are evaluated
    // and recorded but don't block acceptance.
    Required bool `json:"required"`

    // Weight indicates relative importance. Higher = more critical.
    // Prioritizes evaluation order and surfaces the most impactful
    // failures first in ambient context.
    Weight int `json:"weight,omitempty"`

    // Deadline is UTC time by which the evaluator should complete.
    // Prevents validation from stalling the pipeline. Zero = no
    // deadline.
    Deadline time.Time `json:"deadline,omitempty"`
}
```

### 4.9 Enums

```go
// ActionType classifies the action a set of claims belongs to.
type ActionType string

const (
    ActionTypeTask         ActionType = "task"
    ActionTypeChallenge    ActionType = "challenge"
    ActionTypeConsultation ActionType = "consultation"
    ActionTypeCorrective   ActionType = "corrective"
    ActionTypeArchival     ActionType = "archival"
    ActionTypePrompt       ActionType = "prompt"
)

// ActionStatus tracks an action's aggregate lifecycle.
type ActionStatus string

const (
    ActionStatusPending   ActionStatus = "pending"    // claims not all testified
    ActionStatusActive    ActionStatus = "active"     // at least one claim in_progress
    ActionStatusTestified ActionStatus = "testified"  // all claims have testaments
    ActionStatusValidated ActionStatus = "validated"  // all claims accepted/rejected
    ActionStatusComplete  ActionStatus = "complete"   // terminal success
    ActionStatusFailed    ActionStatus = "failed"     // terminal failure
)

// ClaimStatus tracks where a claim is in its lifecycle.
type ClaimStatus string

const (
    ClaimStatusPending    ClaimStatus = "pending"
    ClaimStatusInProgress ClaimStatus = "in_progress"
    ClaimStatusTestified  ClaimStatus = "testified"
    ClaimStatusAccepted   ClaimStatus = "accepted"    // terminal
    ClaimStatusRejected   ClaimStatus = "rejected"    // terminal
    ClaimStatusSuperseded ClaimStatus = "superseded"  // terminal
)

// ValidationStatus tracks a validation's evaluation lifecycle.
type ValidationStatus string

const (
    ValidationStatusPending    ValidationStatus = "pending"
    ValidationStatusInProgress ValidationStatus = "in_progress"
    ValidationStatusPassed     ValidationStatus = "passed"     // terminal
    ValidationStatusFailed     ValidationStatus = "failed"     // terminal
    ValidationStatusSkipped    ValidationStatus = "skipped"    // terminal (waived)
)

// ValidationType classifies what kind of check a validation performs.
type ValidationType string

const (
    ValidationTypeTest        ValidationType = "test"
    ValidationTypeInspection  ValidationType = "inspection"
    ValidationTypeIntegration ValidationType = "integration"
    ValidationTypeContract    ValidationType = "contract"
    ValidationTypeDesign      ValidationType = "design"
    ValidationTypeRegression  ValidationType = "regression"
    ValidationTypeReceipt     ValidationType = "receipt"
)

// BoardPhase tracks the overall claims board lifecycle.
type BoardPhase string

const (
    BoardPhaseImplementation BoardPhase = "implementation"
    BoardPhaseValidation     BoardPhase = "validation"
    BoardPhaseComplete       BoardPhase = "complete"
)
```

### 4.10 Mutability Summary

| Type | Mutable? | Has Status? | Has StatusHistory? | Rationale |
|---|---|---|---|---|
| Action | Yes | Yes | Yes | Aggregate lifecycle tracks claim progress |
| Claim | Yes | Yes | Yes | Transitions through pending -> testified -> accepted/rejected |
| Testament | **Immutable** | No | No | Proof chain integrity — corrections produce new testaments with `supersedes` relation |
| Validation | Yes | Yes | Yes | Evaluation lifecycle: pending -> in_progress -> passed/failed/skipped |
| Artifact | **Immutable** | No | No | Evidence integrity — ContentHash always valid, validations reference exact evidence |

### 4.11 Complete Field Summary

**9 universal fields on every type:**

`ID`, `AgentID`, `SessionID`, `PipelineID`, `TaskID`, `Sequence`, `Relations`, `Created`, `Accessed`

**Per-type semantic fields:**

| Field | Action | Claim | Testament | Validation | Artifact |
|---|---|---|---|---|---|
| `Type` | x | | | x | |
| `ActionType` | | x | | | |
| `Status` | x | x | | x | |
| `StatusHistory` | x | x | | x | |
| `Priority` | x | x | | | |
| `Title` | | x | | | |
| `Description` | | x | | x | |
| `Scope` | | x | | | |
| `Deadline` | | x | | x | |
| `Tags` | | x | | | |
| `Iteration` | | x | | | |
| `Summary` | | | x | | |
| `Confidence` | | | x | | |
| `Duration` | | | x | | |
| `QualityBar` | | | | x | |
| `Required` | | | | x | |
| `Weight` | | | | x | |
| `Kind` | | | | | x |
| `Reference` | | | | | x |
| `Metadata` | | | | | x |
| `ContentHash` | | | | | x |
| `Size` | | | | | x |
| `Ephemeral` | | | | | x |

**Total: 9 universal + 4 Action + 9 Claim + 3 Testament + 8 Validation + 6 Artifact = 39 unique fields across 5 types.**

### 4.12 ClaimsBoard

```go
type ClaimsBoard struct {
    mu sync.RWMutex

    // Identity
    boardID    string
    pipelineID string
    taskID     string
    sessionDir string

    // Phase + iteration
    phase     BoardPhase
    iteration int

    // Storage: flat maps for O(1) access
    actions    map[string]*Action
    claims     map[string]*Claim
    testaments map[string]*Testament
    validations map[string]*Validation
    artifacts  map[string]*Artifact
    claimOrder []string // insertion order for deterministic display

    // Persistence
    store *durableProtocolLog

    // Fabric amplifier
    amplifier *ClaimsBoardAmplifier

    // Subscribers
    subscribersMu sync.Mutex
    subscribers   []claimsBoardSubscription
}
```

### 4.13 Board Operations

```go
// ── Lifecycle ──────────────────────────────────────────────────────

func NewClaimsBoard(cfg ClaimsBoardConfig) (*ClaimsBoard, error)
func OpenClaimsBoard(sessionDir, pipelineID, taskID string) (*ClaimsBoard, error)
func (b *ClaimsBoard) Close() error

// ── Actions ────────────────────────────────────────────────────────

// PostAction issues a set of claims as a claim action. Returns the
// action ID. All claims are stamped with the action's Relations.
func (b *ClaimsBoard) PostAction(ctx context.Context, action Action, claims []Claim) error

// SubmitTestaments records a set of testaments as a testament action.
// Each testament references its specific claim via Relations. Transitions
// each referenced claim to testified.
func (b *ClaimsBoard) SubmitTestaments(ctx context.Context, action Action, testaments []Testament) error

// ── Claim Mutations ────────────────────────────────────────────────

func (b *ClaimsBoard) UpdateClaimProgress(ctx context.Context, claimID string, update ClaimProgressUpdate) error

// ── Validation ─────────────────────────────────────────────────────

// EvaluateValidation transitions a validation through its lifecycle
// (pending -> in_progress -> passed/failed/skipped) with a StatusChange
// entry. Adds "evaluator" and "reviews" Relations. If all required
// validations on the claim pass, the claim auto-accepts.
func (b *ClaimsBoard) EvaluateValidation(ctx context.Context, validationID string, change StatusChange, artifactRefs []string) error

// ── Rejection ──────────────────────────────────────────────────────

// RejectClaim transitions a claim to rejected (with StatusChange
// reason) and optionally posts replacement claims as a new action.
func (b *ClaimsBoard) RejectClaim(ctx context.Context, claimID string, change StatusChange, replacements *Action, replacementClaims []Claim) error

// ── Phase Transitions ──────────────────────────────────────────────

func (b *ClaimsBoard) TransitionToValidation(ctx context.Context) error
func (b *ClaimsBoard) TransitionToImplementation(ctx context.Context) error
func (b *ClaimsBoard) MarkComplete(ctx context.Context) error

// ── Queries ────────────────────────────────────────────────────────

func (b *ClaimsBoard) Projection() *ClaimsBoardProjection
func (b *ClaimsBoard) ClaimsByRelation(relatedType, relationship, relatedID string) []*Claim
func (b *ClaimsBoard) TestamentsByClaim(claimID string) []*Testament
func (b *ClaimsBoard) ArtifactsByTestament(testamentID string) []*Artifact
func (b *ClaimsBoard) IncompleteClaims() []*Claim
func (b *ClaimsBoard) FailedValidations() []*Validation
func (b *ClaimsBoard) ClaimByID(id string) (*Claim, bool)
func (b *ClaimsBoard) ReadyForValidation() bool
func (b *ClaimsBoard) AllAccepted() bool
func (b *ClaimsBoard) SubscribeProjection(fn ClaimsBoardSubscriber) func()
```

### 4.14 ClaimsBoardProjection

```go
type ClaimsBoardProjection struct {
    BoardID   string     `json:"board_id"`
    TaskID    string     `json:"task_id"`
    Phase     BoardPhase `json:"phase"`
    Iteration int        `json:"iteration"`

    Actions    []Action    `json:"actions"`
    Claims     []Claim     `json:"claims"`
    Testaments []Testament `json:"testaments"`
    Validations []Validation `json:"validations"`
    Artifacts  []Artifact  `json:"artifacts"`

    // Claim status counts.
    TotalClaims     int `json:"total_claims"`
    PendingCount    int `json:"pending_count"`
    InProgressCount int `json:"in_progress_count"`
    TestifiedCount  int `json:"testified_count"`
    AcceptedCount   int `json:"accepted_count"`
    RejectedCount   int `json:"rejected_count"`

    // Validation status counts.
    TotalValidations   int `json:"total_validations"`
    PassedValidations  int `json:"passed_validations"`
    FailedValidations  int `json:"failed_validations"`
    SkippedValidations int `json:"skipped_validations"`

    // Action counts.
    TotalClaimActions     int `json:"total_claim_actions"`
    TotalTestamentActions int `json:"total_testament_actions"`

    // Testament/artifact counts.
    TotalTestaments int `json:"total_testaments"`
    TotalArtifacts  int `json:"total_artifacts"`

    Updated time.Time `json:"updated"`
}

// ClaimProgressUpdate is the payload for UpdateClaimProgress — how
// subjects report incremental work before submitting a testament.
type ClaimProgressUpdate struct {
    WorkSummary  string               `json:"work_summary,omitempty"`  // appends to existing
    Evidence     []activity.EvidenceRef `json:"evidence,omitempty"`    // appends to existing
    FilesChanged []string             `json:"files_changed,omitempty"` // scope tracking
}
```

### 4.15 TestamentAccumulator

High-frequency processes (search tool calls, route cache lookups, replica pool admissions, conversation flow observations) fire per-event — potentially dozens per request. Individual testaments per event flood the board with noise. Skipping them entirely leaves audit gaps.

The `TestamentAccumulator` solves this by collecting observations within any bounded lifecycle and flushing them as a single composite testament when the lifecycle completes:

```go
// Create at lifecycle start
acc := claims.NewTestamentAccumulator("librarian", sessionID)
ctx = claims.WithTestamentAccumulator(ctx, acc)
defer acc.Flush(ctx, board, scope) // ONE testament at lifecycle end

// Anywhere in the call chain — no board interaction, no async dispatch
if acc := claims.AccumulatorFromContext(ctx); acc != nil {
    acc.Record("search_result", "grep foo → 12 files")
    acc.Record("search_saturated", "2 consecutive searches returned only seen files")
    acc.Note("Health assessed: DISCIPLINED")
}
```

The lifecycle boundary is defined by the caller, not the type:
- **Request lifecycle**: Librarian creates one per forwarded request. Accumulates search results, health assessments, repo briefs, saturation events, replica admission state. Flushes one testament per request.
- **Route lifecycle**: Guide creates one per route request. Accumulates route cache hits, enrichment fanouts, conversation flow routing. Flushes one testament per route.
- **Session lifecycle**: Could accumulate session-scoped observations (session creation, model swaps, preference changes). Flushes on session close.
- **Planning lifecycle**: Architect could accumulate per-stage observations within a planning protocol. Flushes when the protocol completes.

Properties:
- **Thread-safe**: `Record`, `RecordJSON`, `Note` are mutex-guarded. Concurrent tool calls accumulate safely.
- **Zero-cost when empty**: `Flush` is a no-op if no artifacts were recorded.
- **Async flush**: When a `ScopeProvider` is passed, the flush dispatches via a tracked goroutine.
- **One testament per lifecycle**: The board sees one composite entry, not N individual entries.
- **Context-propagated**: `WithTestamentAccumulator` / `AccumulatorFromContext` — any code in the call chain can record without knowing who created the accumulator or when it flushes.

---

## 5. Agent Intake and Processing

The intake and processing mechanics are the universal shape every agent in the system shares. Every directed interaction an agent receives or emits flows through one discipline regardless of agent role, phase, or board scope.

### 5.1 Three Sources, One Discipline

Agents draw information from exactly three sources, each with a distinct role:

| Source | Role | Access pattern |
|---|---|---|
| **Event Bus** | Delivers explicit, authoritative delta records the moment a board mutation commits. Each delta fully describes the mutation that occurred. | Subscription-based, fire-and-forget, dimensioned topic keys. |
| **ClaimsBoard** | Durable archive and relational graph. Queried on-demand for adjacent context the event did not carry. | Named query skills with sharp signatures; indexed by `Relations`. |
| **Fabric Lens** | Cross-cutting projections over the activity stream. Queried for context that crosses session, pipeline, or task boundaries. | Named lens queries; backed by `AmbientFor` / peer lenses. |

Every directed emission an agent performs is one of four async skills; every read an agent performs is `pull_work()` (the turn envelope) plus on-demand named queries into the board or the fabric. There is no fourth source and no fifth emission.

### 5.2 Async Emission

The four board-mutating skills are strictly asynchronous. Each returns the committed IDs and nothing else; the issuer never blocks waiting for a peer response.

```
post_action(Action, []Claim)              -> {action_id, claim_ids[]}
update_claim_progress(claim_id, update)   -> {sequence}
submit_testaments(Action, []Testament)    -> {action_id, testament_ids[]}
evaluate_validation(validation_id, ...)   -> {sequence}
```

The LLM's mental model collapses to:

1. **Post is always fire-and-forget.** The issuer receives the committed ID; the peer responds on its own schedule.
2. **Continue or end the turn is the LLM's choice.** If the agent has independent work (progress updates on its other claims, conflict inspection, context pre-reads), it continues. If not, it ends the turn.
3. **Next `pull_work` sees everything.** That is the only synchronization point the LLM touches, and it is a runtime-owned read of already-durable state.

Removing the in-tool wait eliminates deadline tuning, subscription leaks on tool cancel, and partial-deadline ambiguity. The only durability fence that matters is durable-before-transient emission inside the amplifier.

### 5.3 Deltas

Every board mutation emits exactly one delta record. Four shapes cover every possible mutation.

```go
package claims

// InboxDelta is emitted when an Action posts claims directed at an
// agent. The receiver learns everything it needs about the mutation
// without re-reading the board.
type InboxDelta struct {
    // Dedup + ordering.
    ActionID  string    `json:"action_id"`
    ClaimID   string    `json:"claim_id"`
    Sequence  uint64    `json:"sequence"`
    EmittedAt time.Time `json:"emitted_at"`

    // Explicit description of the mutation.
    Relationship    string            `json:"relationship"`    // "subject" | "evaluator" | "consulted" | "blocked_on"
    ActionKind      ActionType        `json:"action_kind"`     // matches Action.Type
    Priority        uint8             `json:"priority"`
    Scope           []ClaimScopeEntry `json:"scope"`
    ValidationCount int               `json:"validation_count"`
    DependsOn       []string          `json:"depends_on"`
    IssuerAgentID   string            `json:"issuer_agent_id"`
    IssuerAgentType string            `json:"issuer_agent_type"`
    Deadline        time.Time         `json:"deadline,omitempty"`
}

// TestamentDelta is emitted when a testament is submitted against a
// claim. Delivered to the claim's issuer and any evaluators.
type TestamentDelta struct {
    ClaimID     string    `json:"claim_id"`
    TestamentID string    `json:"testament_id"`
    Sequence    uint64    `json:"sequence"`
    EmittedAt   time.Time `json:"emitted_at"`

    Verdict        string   `json:"verdict"`          // "work_complete" | "error" | "partial"
    ArtifactKinds  []string `json:"artifact_kinds"`   // e.g. ["code_reference", "test_output"]
    AutoAccepted   bool     `json:"auto_accepted"`
    EvaluatorAgent string   `json:"evaluator_agent,omitempty"`
    SubjectAgentID string   `json:"subject_agent_id"`
}

// ValidationDelta is emitted when a validation is evaluated
// (passed / failed / skipped). Delivered to the claim's subject and
// issuer.
type ValidationDelta struct {
    ClaimID      string    `json:"claim_id"`
    ValidationID string    `json:"validation_id"`
    Sequence     uint64    `json:"sequence"`
    EmittedAt    time.Time `json:"emitted_at"`

    Verdict             string   `json:"verdict"` // "passed" | "failed" | "skipped"
    EvaluatorAgentID    string   `json:"evaluator_agent_id"`
    RemainingOnClaim    int      `json:"remaining_on_claim"`
    FailedCountOnClaim  int      `json:"failed_count_on_claim"`
    RemediationClaimIDs []string `json:"remediation_claim_ids,omitempty"`
}

// PhaseDelta is emitted on board phase transitions. Delivered to every
// subscriber on the board's phase topic.
type PhaseDelta struct {
    BoardID   string    `json:"board_id"`
    TaskID    string    `json:"task_id"`
    Sequence  uint64    `json:"sequence"`
    EmittedAt time.Time `json:"emitted_at"`

    FromPhase BoardPhase `json:"from_phase"`
    ToPhase   BoardPhase `json:"to_phase"`
    Iteration int        `json:"iteration"`
    Reason    string     `json:"reason"`
}
```

**Deltas are not hints.** Each delta is the full, explicit description of what was committed to the board. Receivers act on the delta directly; they do not re-query the board to verify or expand the event. The board is consulted only when the receiver needs context beyond the event itself (§5.8).

### 5.4 Event Bus Topic Grammar

Deltas are published to the existing `ChannelBus` under dimensioned topic keys. The router's wildcard matching (`TopicRouter`) allows subscribers to narrow their channel to the dimensions they care about without receiver-side filtering.

```
claims.<session_id>.inbox.<agent_id>.<relationship>.<action_kind>
claims.<session_id>.claim.<claim_id>.<status>
claims.<session_id>.validation.<validation_id>.<verdict>
claims.<session_id>.phase.<phase>
```

Example subscription patterns:

| Subscriber | Pattern | Delivers |
|---|---|---|
| Engineer waiting for directed work | `claims.*.inbox.eng-a3f2.subject.*` | Claims where I am the subject |
| Inspector evaluating testaments | `claims.*.claim.*.testified` | Any claim that just received a testament |
| Guardian evaluating guardrails | `claims.*.inbox.guardian.evaluator.*` | Claims directing Guardian to evaluate |
| Orchestrator watching phase | `claims.<sid>.phase.*` | Phase transitions on this session's boards |
| Agent awaiting remediation | `claims.*.claim.*.rejected` | Rejected claims requiring remediation |

Overflow, subscription tracking, and cancellation follow existing `ChannelBus` semantics. No new transport primitives are introduced.

### 5.5 Amplifier Dual Emission

The `ClaimsBoardAmplifier` grows one responsibility beyond its existing fabric projection: publishing the matching delta to the bus. Each mutation executes under a strict ordering fence.

```
1. board.mu.Lock()
2. apply mutation in memory
3. build delta from committed state
4. durableProtocolLog.Append(delta record)   // durable-before-transient
5. board.mu.Unlock()
6. amplifier.PublishFabricActivity(...)      // cross-cutting projection
7. amplifier.PublishBusDelta(delta)          // transient notification
```

Step 4 must succeed before steps 6 or 7 run. A crash between steps 4 and 7 replays deterministically on restart: WAL recovery reconstructs the same delta records the bus would have delivered, and subscribers whose cursors lag behind `durableProtocolLog`'s tail receive the missing records on their next read.

**Recovery equivalence**: the WAL-replayed delta stream is byte-identical to the live-published delta stream. This is what keeps crashed and live subscribers converged.

### 5.6 ClaimsInbox — Event-Driven Expectation-Matching Engine

`ClaimsInbox` is a runtime type (not an LLM surface) attached to every agent replica. It is an **event-driven expectation-matching engine**: when a delta arrives on the bus and matches, the agent's processing function fires immediately from the bus subscription handler — no polling, no pull loop, no goroutine sitting on a channel.

An agent's inbox receives deltas from two sources, both fully determined:

1. **Explicit expectations** — registered at emission time. When the agent calls `post_action(kind=consultation, claim{subject=academic})`, the inbox registers an expectation: "when `TestamentDelta{claim_id=c-42}` arrives, resolve it and call my handler." The return path is known the moment the claim is issued.
2. **Standing subscriptions** — derived from the agent's identity. An engineer is `subject` on claims directed at it; an inspector is `evaluator` on validations it must judge. These are computed from the agent's `agentID`, not from explicit registration.

Together, explicit expectations + standing subscriptions define the **complete set of deltas the agent will ever receive**. Nothing else enters the inbox. No stale work, no unrelated deltas.

**Event-driven dispatch.** When `Ingest` matches a delta, it resolves the delta into a `GraphEntryPoint` and calls the agent's `OnResolved` callback directly — on the bus subscriber's goroutine. The `OnResolved` implementation dispatches into the agent's `GoroutineScope` for tracked, async execution. No intermediate queue, no consumer goroutine, no pull loop.

```go
package claims

// ClaimsInbox is the per-replica event-driven intake surface.
//
// Deltas arrive from bus subscriptions. The inbox matches each
// against registered expectations (from emissions) or standing
// subscriptions (from the agent's identity). Matched deltas are
// resolved into GraphEntryPoints and dispatched to the agent's
// OnResolved callback immediately. Unmatched deltas are discarded.
type ClaimsInbox struct {
    mu sync.Mutex

    agentID    string
    sessionID  string

    // Bus subscription handles.
    subscriptions []DeltaSubscription

    // Explicit expectations: claim_id → *Expectation.
    expectations map[string]*Expectation

    // Dedup: DeltaKey → highest Sequence applied.
    seen map[string]uint64

    // Board handle for graph resolution.
    board *ClaimsBoard

    // OnResolved is called when a delta matches. Runs on the bus
    // subscriber's goroutine — the implementation MUST dispatch
    // into the agent's scope for tracked execution.
    onResolved func(entry *GraphEntryPoint)
}

// InboxConfig bundles construction parameters.
type InboxConfig struct {
    AgentID    string
    SessionID  string
    Subscriber DeltaSubscriber
    Board      *ClaimsBoard

    // OnResolved is called when a delta matches an expectation or
    // standing subscription. The resolved GraphEntryPoint is passed
    // directly. The handler runs on the bus subscriber's goroutine
    // — it MUST dispatch into the agent's GoroutineScope for
    // tracked, async execution of the agent's tool loop.
    OnResolved func(entry *GraphEntryPoint)
}

// Expectation is registered at post_action time.
type Expectation struct {
    ClaimID       string
    ExpectedDelta string           // DeltaKindTestament | DeltaKindValidation
    ActionID      string
    IssuedAt      time.Time
    Priority      WorkUnitPriority
}

// WorkUnitPriority determines delivery order.
type WorkUnitPriority int

const (
    PriorityRemediation WorkUnitPriority = 1
    PriorityChallenge   WorkUnitPriority = 2
    PriorityDirected    WorkUnitPriority = 3
    PriorityResponse    WorkUnitPriority = 4
    PriorityEvaluation  WorkUnitPriority = 5
    PriorityPhase       WorkUnitPriority = 6
    PriorityAdvisory    WorkUnitPriority = 7
)

func (i *ClaimsInbox) Expect(e *Expectation)
func (i *ClaimsInbox) Ingest(d Delta)  // match → resolve → OnResolved
```

**Flow:**

```
Bus delta arrives
    → Ingest(delta)
        → dedup check
        → match against expectations (O(1) by claim_id)
        → match against standing subscriptions (by agentID)
        → if matched: ResolveEntryPoint(board, delta) → OnResolved(entry)
        → if unmatched: discard
```

**Agent wiring:**

```go
inbox := NewClaimsInbox(InboxConfig{
    AgentID:   agent.ID(),
    SessionID: sessionID,
    Board:     board,
    OnResolved: func(entry *GraphEntryPoint) {
        agent.scope.Go("process_claim", 0, func(ctx context.Context) error {
            return agent.processClaimsEntry(ctx, entry)
        })
    },
})
```

Properties:

- **Event-driven.** No polling, no pull loop, no consumer goroutine. The bus subscription is the trigger. The `OnResolved` callback fires on match.
- **Expectation-driven.** Explicit expectations from emissions + standing subscriptions from identity = the complete delta set. No stale or unrelated work.
- **Tracked execution.** `OnResolved` dispatches into the agent's `GoroutineScope`. Every claims-triggered turn is a tracked goroutine with timeout, panic recovery, and budget enforcement.
- **Dedup by `(DeltaKey, Sequence)`.** Re-deliveries, reconnect replays, and bus duplicates collapse to a single logical entry.
- **No intermediate buffer.** The `ChannelBus` already provides per-subscription bounded queues (4096 capacity). No second queue needed.

### 5.7 Graph Entry Points and Agent-Driven Traversal

The claims board is a graph. Actions, Claims, Testaments, Validations, and Artifacts are nodes connected by typed `Relations` edges (`caused_by`, `supersedes`, `claim_action`, `issuer`, `subject`, `evaluator`, `derived_from`, `amends`, `refines`, etc.). When a delta resolves in the inbox, the runtime delivers the **entry point** — the immediate node the delta references — plus the delta itself. The agent decides how far to traverse from there.

```go
// GraphEntryPoint is the unit of work delivered to the agent's turn
// loop. It contains the triggering delta and the immediate graph
// node that delta references (depth-1). The agent traverses deeper
// via the traverse tool (§5.8).
type GraphEntryPoint struct {
    // The delta that triggered this entry point.
    Delta Delta

    // The immediate node referenced by the delta.
    // For InboxDelta: the Claim where I am subject.
    // For TestamentDelta: the Testament submitted against my claim.
    // For ValidationDelta: the Validation verdict on my testament.
    // For PhaseDelta: the board's current phase record.
    Node GraphNode

    // Priority derived from the delta kind and any deadline.
    Priority WorkUnitPriority

    // The expectation this entry resolved (nil for standing subscriptions).
    Expectation *Expectation
}

// GraphNode is the depth-1 view of a single node in the claims
// graph. Contains the node's own data and the IDs of its immediate
// neighbors — enough for the agent to decide whether to traverse
// deeper, without pre-loading the full subgraph.
//
// For TestamentDelta resolution, BOTH Testament AND Claim are
// populated: the testament itself plus the parent claim it responds
// to. This ensures the evaluator agent sees the claim's pending
// validations (Description, QualityBar, Type) alongside the
// testament's artifacts in a single entry point — no traversal
// needed to begin evaluation.
type GraphNode struct {
    // For most deltas, exactly one of these is set.
    // For TestamentDelta: BOTH Testament and Claim are set —
    // the testament plus its parent claim with validations.
    Action     *Action
    Claim      *Claim
    Testament  *Testament
    Validation *Validation

    // Immediate neighbor IDs — not resolved, just their IDs and
    // relationship types. The agent calls traverse() to load any
    // of these it needs.
    Edges []GraphEdge
}

// GraphEdge is a typed pointer to an adjacent node.
type GraphEdge struct {
    TargetID     string           // the neighbor's ID
    TargetType   RelatedType      // Action | Claim | Testament | Validation | Artifact | Agent
    Relationship RelationshipType // caused_by | supersedes | claim_action | issuer | subject | ...
}
```

**Event-driven processing.** There is no turn loop polling for entries. The inbox's `OnResolved` callback fires when a delta matches. The agent's callback dispatches into its `GoroutineScope`:

```go
// OnResolved fires on the bus subscriber's goroutine.
// It dispatches into the agent's scope for tracked execution.
func (a *Agent) onClaimsResolved(entry *GraphEntryPoint) {
    a.scope.Go("process_claim", 0, func(ctx context.Context) error {
        return a.processClaimsEntry(ctx, entry)
    })
}

// processClaimsEntry handles one causally coherent concern.
func (a *Agent) processClaimsEntry(ctx context.Context, entry *GraphEntryPoint) error {
    // ComposeClaimsEntryPrompt builds the prompt from the entry point:
    //   - For InboxDelta: claim title, description, scope, validations
    //   - For TestamentDelta: testament summary + artifacts + parent
    //     claim's pending validations as evaluation instructions
    //   - For ValidationDelta: verdict details
    //
    // When a TestamentDelta arrives with pending validations, the
    // prompt explicitly lists each validation with its Description,
    // QualityBar, Type, and IDs — telling the agent to evaluate each
    // one using its skills and call evaluate_validation with a verdict.
    //
    // The agent's full skill surface is available: traverse the board
    // for additional evidence, query non-claim-associated testaments,
    // read files, run tests, consult peers.
    //
    // Run tool loop → agent evaluates → calls evaluate_validation
    // → board auto-accepts if all pass → emits claims/testaments
}
```

Each `processClaimsEntry` call handles **one causally coherent concern**. The agent processes a consultation answer, then a challenge, then a directed claim — never mixed into one prompt. If the agent needs serialization (one entry at a time), it uses its existing `requestSerializer`. If it can handle concurrent entries, it processes them in parallel under the scope's budget.

The graph structure ensures that when the agent needs more context, it asks for exactly what it needs via traversal, rather than receiving a pre-assembled grab-bag.

### 5.8 Graph Traversal

The agent's only read path into the claims board is **traversal from an entry point**. A single tool replaces the nine named query skills that previously existed as independent board reads.

```
traverse(node_id, edge_filter?, max_depth?)
```

| Parameter | Type | Default | Description |
|---|---|---|---|
| `node_id` | string | required | The starting node (any Action, Claim, Testament, Validation, or Artifact ID) |
| `edge_filter` | string | all edges | Relationship type filter: `caused_by`, `supersedes`, `claim_action`, `issuer`, `subject`, `amends`, `refines`, `derived_from`, etc. |
| `max_depth` | int | 1 | How many hops to traverse. Each hop returns the next ring of `GraphNode` records. |

Returns: `[]GraphNode` — the nodes reachable from `node_id` along the filtered edges, up to `max_depth` hops. Each returned node includes its own `Edges` so the agent can decide whether to traverse further.

The nine former named queries are now traversal patterns:

| Former query | Traversal equivalent |
|---|---|
| `trace_claim_ancestry(claim_id)` | `traverse(claim_id, "supersedes\|amends\|refines")` |
| `list_action_claims(action_id)` | `traverse(action_id, "claim_action")` |
| `trace_action_causality(action_id)` | `traverse(action_id, "caused_by")` |
| `find_overlapping_claims(scope)` | `traverse(scope_node_id, "scope")` — scope entries are nodes in the graph |
| `show_validation_history(claim_id)` | `traverse(claim_id, "claim_action", 2)` — claim → validation nodes |
| `list_testaments_for_claim(claim_id)` | `traverse(claim_id, "testament")` |
| `recall_artifact(artifact_id)` | `traverse(artifact_id)` — depth-0, returns the artifact node itself |
| `show_phase_history()` | `traverse(board_id, "phase")` |
| `inspect_claim_conflicts(scope)` | `traverse(scope_node_id, "scope")` + check for overlapping `ClaimScopeEntry` |

The board maintains secondary indexes over `Relations` — `(RelatedType, Relationship) -> []ObjectID` — so each traversal hop resolves in O(1) lookup + O(fan-out) reads. Indexes mutate inside the same write lock as the primary maps; traversal reads run under RLock.

**Why one tool instead of nine.** Named queries committed the board to fixed access patterns and required the LLM to know which query matched its intent. Traversal is the primitive — the agent starts at a known node and walks edges. The LLM's mental model is: "I have a node. I can see its neighbors. If I need more context, I follow an edge." No query taxonomy to learn. New relationship types don't require new skills.

### 5.9 Fabric Lens Context Queries

Fabric lens queries serve cross-cutting context that crosses board, pipeline, or session boundaries. They are thin named wrappers over the existing `AmbientFor` / `WhatAreTheyDoing` / peer-oriented lenses.

| Skill | Returns |
|---|---|
| `query_peer_claims(peer_agent_type, scope?)` | Claim state across boards for peers of a given type |
| `query_peer_activity(peer_agent_id, time_window?)` | Recent activities produced by a peer |
| `query_advisories(scope?)` | Knowledge-agent advisories and guardian notes in scope |
| `query_challenge_history(peer_agent_type)` | Past challenge outcomes with peers of a given type |

These skills do not touch the board. They read the `activity` stream, filtered by `SessionID`, `ActorAgentID`, `SubjectPathPrefix`, or `ActionKind`.

### 5.10 How Existing Primitives Collapse

Every directed interaction in the existing system maps to a claims emission. Legacy skill names remain as aliases that translate to `post_action` with the correct `ActionType` and `Relations` — LLMs do not relearn nomenclature; the wire format collapses underneath.

| Existing primitive | Under the claims model |
|---|---|
| `consult_peer(target, query)` | `post_action(kind=consultation, claim{subject=peer, validations=["answer addresses question"]})`. The peer's testament lands in the issuer's inbox as a `TestamentDelta`. |
| `challenge_peer` / `challenge_agent` | `post_action(kind=challenge, claim{subject=peer, validations=["defends or remediates the flagged behavior"]})`. `challenge_id ≡ claim_id`; the thread is the claim's testament lineage via `Relations`. |
| `validate_work` / `process_validation` | `submit_testaments(...)` by the challenged peer plus `evaluate_validation(...)` by the challenger. The validation lifecycle is the thread; no separate state machine. |
| `handoff_next` (turn-based) | Closing `submit_testaments` on the outgoing agent's claim set plus `post_action(kind=handoff, Relations=[handoff_from])` that seeds the successor's inbox. `HandoffManager` continues to drive the context-threshold trigger; semantic handoff flows through the board. |
| `handoff_to_ot` (pipeline → global) | Already aligned: pipeline claims serialize onto the `MergeDescriptor`. The global inspector reads them as directed claims on its per-merge replica board. |
| Guardian gate | Guardian evaluates `validation_kind=guardrail` on testaments an agent attempts to submit. Denial surfaces as a rejected claim with `evaluator=guardian` via a standard `ValidationDelta`. No separate approval envelope. |
| Coordination `manage_claim` | Re-expressed directly as claims whose `ClaimScopeEntry` populates scope. Release = testament "scope work concluded". Review request = claim with `subject=reviewer`. |

### 5.11 Correctness Invariants

- **Event-driven intake.** Delta arrival triggers processing directly via `OnResolved`. No polling, no pull loop, no consumer goroutine. The bus subscription handler is the trigger. Processing runs under the agent's `GoroutineScope`.
- **Expectation-driven matching.** Every delta the agent processes is either (a) the fulfillment of an explicit expectation registered at emission time or (b) a match against a standing subscription derived from the agent's identity. No stale, unrelated, or speculative deltas reach the agent.
- **Graph entry point + agent-driven traversal.** Each resolved delta delivers one entry point — the immediate graph node the delta references (depth-1). The agent decides traversal depth via the `traverse` tool. No pre-assembled grab-bags; no runtime heuristics about what context the agent might need.
- **One concern per dispatch.** Each `OnResolved` call delivers one causally coherent entry point. The agent processes a consultation answer, then a challenge, then a directed claim — never mixed. Serialization via `requestSerializer` when the agent requires single-entry processing.
- **Deltas are authoritative.** Each delta fully describes its mutation; receivers act on the delta directly. The board is consulted only when the agent traverses deeper than the entry point (§5.8).
- **Durable-before-transient.** `durableProtocolLog.Append` commits before the amplifier publishes the fabric activity or the bus delta. Crashes replay deterministically; subscriber cursors converge to live state via WAL read.
- **Exactly-once per mutation.** `(DeltaKey, Sequence)` is the dedup key at the inbox seam. Re-emissions, reconnect replays, and WAL recoveries are idempotent.
- **Bus is the transport.** The `ChannelBus` provides per-subscription bounded queues (4096 capacity) and async handler fan-out. No second buffer needed. Dropped messages degrade to WAL catch-up on next board read.
- **Tracked goroutines only.** `OnResolved` dispatches into the agent's `GoroutineScope`. Amplifier emission, subscriber dispatch, inbox ingestion, accumulator flush — all tracked. No bare `go`.
- **Errors as artifacts.** Guardian denials, test failures, tool timeouts, and LLM outages surface as `kind=error*` artifacts on the testament in flight. The issuer decides remediation; the board's audit trail is complete.
- **OT / replica symmetry.** A per-merge audit replica's inbox is `ClaimsInbox` filtered by the replica's `agent_id`. Global review operates via the same primitives as pipeline execution — same intake, same emission, same traversal.

---

## 6. Two-Phase Execution Model (Pipeline Context)

Within pipelines, the claims model operates in two phases. This is the pipeline-specific application of the universal claims model.

### 6.1 Implementation Phase

All non-inspector pipeline agents work simultaneously against their assigned claims.

1. **All agents work simultaneously.** Engineer, Designer, and Tester receive their claims and begin work in parallel.
2. **The Tester MAY NOT RUN TESTS.** `run_test_suite` is phase-gated. The Tester authors tests but does not execute them.
3. **Every action = atomic claim update.** Each meaningful action produces an `UpdateClaimProgress` on the board.
4. **Subjects submit testaments when done.** Instead of "marking complete," the subject submits a testament with artifacts proving each claim is satisfied.
5. **Agents communicate via actions.** Challenges and consultations are actions (sets of claims) — not separate skill types.
6. **Corrective claims instead of errors.** Out-of-order actions produce corrective claims, not errors.
7. **Phase ends when all claims reach `testified`.** Every subject has submitted a testament.

### 6.2 Validation Phase

The Inspector (as issuer of the initial claims) validates each testament's artifacts against each claim's validations. The Tester may also validate test-type validations.

1. **Issuer evaluates EVERY validation for EACH claim** against the testament's artifacts.
2. **The quality bar must be met.** Each validation's `QualityBar` statement defines the standard.
3. **Inspector and Tester collaborate via actions.** Consultation actions between validators, not separate skills.
4. **If validations fail, issuer posts new claims.** Corrective or remediation claims targeting the subject. The replacement claims carry a `supersedes` Relation to the rejected claim.
5. **New claims trigger re-entry to Implementation.** Only new claims need resolution.
6. **Bounded by `MaxReviewRounds`.**

### 6.3 Phase Transition Diagram

```
                    ┌───────────────────────────────────────────┐
                    │                                           │
                    v                                           │
    ┌──────────────────────────┐                                │
    │   IMPLEMENTATION PHASE   │                                │
    │                          │                                │
    │  Subjects work claims    │                                │
    │  Subjects submit         │                                │
    │  testaments + artifacts  │                                │
    │  (tester: no execution)  │                                │
    │                          │                                │
    │  Collaborate via actions │                                │
    │  (challenge/consult)     │                                │
    └──────────┬───────────────┘                                │
               │                                                │
               │ all claims -> testified                        │
               v                                                │
    ┌──────────────────────────┐                                │
    │    VALIDATION PHASE      │                                │
    │                          │                                │
    │  Issuer validates each   │                                │
    │  testament's artifacts   │                                │
    │  against validations     │                                │
    │                          │                                │
    │  Tester runs tests       │                                │
    └──────────┬───────────────┘                                │
               │                                                │
               ├─── all validations pass ──> COMPLETE           │
               │                                                │
               └─── validations fail ──> post new claims ───────┘
                    (bounded by MaxReviewRounds)
```

### 6.4 Phase Transition Control

| Transition | Trigger | Precondition |
|---|---|---|
| Start -> Implementation | Orchestrator creates board, dispatches agents | Board populated with claims |
| Implementation -> Validation | Orchestrator observes board state | All non-superseded claims in `testified` status |
| Validation -> Implementation | Orchestrator observes new claims | At least one `pending` claim posted |
| Validation -> Complete | Orchestrator observes board state | All non-superseded claims `accepted` |
| Any -> Failed | Orchestrator detects bound exceeded | `iteration >= MaxReviewRounds` with failing validations |

### 6.5 Pipeline-to-Global Handoff

When the pipeline board completes (all claims accepted), the pipeline inspector calls `handoff_to_ot` which triggers the pipeline-to-global review chain. Per `PARALLEL_GLOBAL_VFS.md`, this is a three-stage trigger chain:

```
Stage 1: Pipeline Inspector → handoff_to_ot
  → MergePipelineIntoGreen
      - Produces Copy_N at arrival_seq N
      - Serializes pipeline claims (with testaments + artifacts)
        onto the MergeDescriptor
  → Publishes bus handoff message: "new work for seq N"

Stage 2: Global Inspector + Global Tester receive bus message
  → Both begin watching for: VFS Copy_N materialized + claims ready

Stage 3: VFS Copy_N ready + Claims on descriptor ready
  → Global Inspector replica starts auditing against Copy_N + claims
  → Global Tester waits for inspector outcome

Stage 4: Global Inspector accepts
  → Global Tester replica starts testing against Copy_N + claims
  (If inspector rejects → tester does not run, rejection flows
   to architect for remediation)

Stage 5: Global Tester accepts
  → Tester posts acceptance claims (test results, coverage artifacts)
  → These claims trigger the Global Inspector

Stage 6: Global Inspector consumes tester's acceptance claims
  → Validates tester's testaments
  → Triggers disk commit on the commit queue for seq N

Stage 7: Commit queue advances → water line moves → next layer
```

**Key design points:**

- **Claims travel on the MergeDescriptor.** The pipeline's accepted claims with their testaments and artifacts are serialized onto the descriptor at handoff time. The global inspector replica reads them from the descriptor, not from a live board reference.

- **The bus handoff message is the alert, not the dispatch.** It tells global agents "prepare — work is coming for seq N." The actual work starts when the VFS Copy and claims are both confirmed ready.

- **Inspector gates tester.** Both are autonomous peers (not parent-child), but the inspector goes first. Its acceptance is the prerequisite for the tester to start.

- **The global inspector is the sole authority for disk commit.** The tester provides evidence (claims with testaments and artifacts), but the inspector consuming and validating that evidence is what triggers the commit.

- **Global agents are always-on.** They subscribe to handoff events on the bus. The orchestrator is not involved in the global review dispatch — the pipeline inspector publishes directly.

---

## 7. Comparison with Current System

| Aspect | Current (Protocol State Machine) | New (Claims + Testaments) |
|---|---|---|
| **Execution model** | Sequential: Inspector -> Tester -> Worker -> Verify | Parallel: all subjects work simultaneously |
| **State representation** | `PipelineProtocolSnapshot` with 7 reducer states | `ClaimsBoard` with 2 phases |
| **Agent coordination** | Turn-based handoffs via `handoff_next`, `challenge_agent` | Actions via `post_action`: challenges and consultations are claim sets |
| **Work tracking** | Single task prompt per agent | Granular claims with atomic updates + testaments |
| **Response mechanism** | Handoff with status update | Testament with artifacts (proof of work) |
| **Validation** | Inspector challenges agent, processes response | Issuer validates testament artifacts against validations |
| **Error handling** | Errors returned to agent | Corrective claims issued — agent always has a path forward |
| **Quality gates** | Inspector's `grade_task_quality` (holistic) | Per-claim, per-validation quality bar statements |
| **Cross-agent communication** | Separate `challenge_peer`, `consult_peer` skills | Uniform via `post_action`: challenge and consultation are action types |
| **Test execution** | Tester runs tests in sequential phase | Tester writes tests in Implementation, runs in Validation |
| **Pipeline terminal** | Inspector's `handoff_to_ot` -> MergePipelineIntoGreen | Board complete -> inspector calls `handoff_to_ot` -> MergePipelineIntoGreen -> claims serialized onto MergeDescriptor -> bus handoff message -> global review chain |
| **Scope** | Pipeline agents only | Universal — works for any agent (scribe, academic, etc.) |

---

## 8. Complete Pipeline Agent Skills Audit

### 8.1 Pipeline Inspector (post-refactor baseline -> claims conversion)

**RETIRE (11 skills) — protocol handoff/challenge/consult machinery replaced by claims/actions:**

| Skill | Current Purpose | Replacement |
|---|---|---|
| `challenge_agent` | Issue targeted follow-up to pipeline peer | Post a challenge action (set of claims) against the peer |
| `handoff_next` | Route to next agent in sequence | Eliminated — no sequential handoffs |
| `validate_work` | Validate peer work and return findings | `evaluate_validation` — evaluate testament artifacts against validations |
| `process_validation` | Process validation responses | Board tracks testament/validation results directly |
| `finalize_pipeline` | Final accept/reject + tester handoff | Board completion; inspector calls `handoff_to_ot` when board is complete |
| `discard_pipeline` | Discard after quality decision | Board bounded-failure triggers rollback |
| `discard_queued_artifacts` | Drop stale verification artifacts | Artifacts live on testaments, not a separate queue |
| `query_pipeline_state` | Protocol projection query | `query_claims_board` |
| `challenge_peer` | Fabric cross-pipeline challenge | Post a challenge action (claims) |
| `consult_peer` | Fabric cross-pipeline consult | Post a consultation action (claims) |
| `inspect_open_conflicts` | Return contested scopes | `inspect_claim_conflicts` — same data, claims-native |

**ADD (5 skills):**

| Skill | Purpose | Phase |
|---|---|---|
| `query_claims_board` | Read full board state | Both |
| `post_action` | Issue an action (set of claims) — covers task, challenge, consultation, corrective | Both |
| `evaluate_validation` | Evaluate a testament's artifacts against a claim's validations | Validation |
| `post_remediation_claims` | Reject a claim and post replacement claims | Validation |
| `inspect_claim_conflicts` | Surface overlapping claims, competing testaments | Both |

**MODIFY (4 skills):**

| Skill | Change |
|---|---|
| `handoff_to_ot` | Stays as the terminal pipeline skill. Gains: serializes pipeline claims (with testaments + artifacts) onto the MergeDescriptor. Gated by board phase = complete (inspector must accept all claims first). |
| `define_criteria` | Generates claims (via `post_action`) rather than standalone criteria |
| `validate_criteria` | Subsumed into `evaluate_validation` workflow |
| `inspect_open_activity` | Surfaces claim/testament conflicts from Fabric |

**KEEP UNCHANGED (post-refactor names):**

- **Analysis**: `run_analyzer(kind=...)` (1 skill, replaces 9 individual analyzer skills)
- **Design validation**: `validate_ui_compliance(aspect=...)` (1 skill, replaces 4)
- **VFS/workspace**: `workspace_read(op=...)`, `workspace_write(op=...)`, `prepare_write_context` (3 skills, replaces 12)
- **Command execution**: `bash` (1 skill)
- **Coordination**: `manage_claim(action=...)`, `publish_work_event(kind=...)` (2 skills, replaces 7 `coord_*`)
- **Memory forest**: generic (5) + inspector-specific (2)
- **Fabric awareness**: `query_peer_activity`, `causal_trace`, `find_related_activity`, `recall_my_history` (4)
- **Validation support**: `grade_task_quality`, `request_override` (2)
- **Dependency**: `dependency(action=research|install)` (1)
- **Diagnostics**: `self_diagnostic`, `reroute_request` (2)

### 8.2 Pipeline Tester (51 skills currently -> 45 after)

**RETIRE (9 skills):** Same 9 protocol + challenge/consult skills as Inspector minus inspector-only skills.

**ADD (5 skills):** Same 5 as Inspector: `query_claims_board`, `post_action`, `evaluate_validation`, `post_remediation_claims`, `inspect_claim_conflicts`.

**PHASE-GATE (1 skill):** `run_test_suite` — blocked during Implementation.

**KEEP UNCHANGED (39 skills):** Test authoring (8), VFS/workspace (13), Command (2), Coordination (7), Fabric awareness (4), Decision manifest (2), Other (3).

### 8.3 Engineer (52 skills currently -> 49 after)

**RETIRE (8 skills):** Protocol skills + challenge/consult.

**ADD (5 skills):**

| Skill | Purpose | Phase |
|---|---|---|
| `query_claims_board` | Read board state | Both |
| `post_action` | Issue actions (consultation, challenge) | Both |
| `submit_testaments` | Submit testament with artifacts for a claim | Implementation |
| `update_claim_progress` | Atomic progress update on a claim | Implementation |
| `inspect_claim_conflicts` | Surface conflicts | Both |

**KEEP UNCHANGED (43 skills):** File I/O (17), Code analysis (3), Consultation (4), Coordination (7), Fabric awareness (4), Discovery (2), Decision manifest (2), Quality (2), Communication (1), Other (3).

### 8.4 Designer (53 skills currently -> 50 after)

**RETIRE (8 skills):** Same as Engineer.

**ADD (5 skills):** Same as Engineer.

**KEEP UNCHANGED (44 skills):** File I/O (15), Component management (3), Design tokens/a11y (5), Coordination (7), Fabric awareness (4), Decision manifest (2), Communication (6), Other (2).

### 8.5 Skills Summary

| Agent | Before | Retire | Add | Phase-Gate | After |
|---|---|---|---|---|---|
| Inspector | 69 | 12 | 5 | 0 | 62 |
| Tester | 51 | 9 | 5 | 1 | 47 |
| Engineer | 52 | 8 | 5 | 0 | 49 |
| Designer | 53 | 8 | 5 | 0 | 50 |

**New shared skills (all pipeline agents):** `query_claims_board`, `post_action`, `inspect_claim_conflicts`
**New subject skills (Engineer, Designer, Tester-during-impl):** `submit_testaments`, `update_claim_progress`
**New issuer skills (Inspector, Tester-during-val):** `evaluate_validation`, `post_remediation_claims`
**Phase-gated:** `run_test_suite` (Tester, blocked during Implementation)

---

## 9. Fabric Integration

> **Substrate note**: per CLUSTER.md §11.5 the Fabric is itself a
> read-side projection over substrate subjects. The ActionKinds below
> are no longer raw Fabric activity types — they are the entry kinds
> on the claims subject. The Fabric "amplifier" becomes a substrate
> consumer reading the claims subject and rendering lens views; the
> AmbientEnvelope is a continuous-query result (CLUSTER.md §31.4 / §27.9)
> over the claims subject + sibling subjects (forest events, fabric
> activity, agent log). Below the surface, the wire is SWF (CLUSTER.md
> §4.4-bis); the transport is adaptive (CLUSTER.md §4.5); the encryption
> is per-tenant envelope (CLUSTER.md §21.2); the audit is signed
> (CLUSTER.md §17.1). The interface stays the same to existing Fabric
> consumers.

### 9.1 New ActionKinds

```go
// ─── Claims system kinds (sovereign amplifier) ─────────────────────

ActionClaimIssued            ActionKind = "claim_issued"             // claim posted to board
ActionClaimUpdated           ActionKind = "claim_updated"            // progress update
ActionTestamentSubmitted     ActionKind = "testament_submitted"      // subject's response
ActionArtifactPublished      ActionKind = "claim_artifact_published" // proof attached to testament
ActionClaimValidated         ActionKind = "claim_validated"          // issuer evaluated a validation
ActionClaimAccepted          ActionKind = "claim_accepted"           // all validations passed
ActionClaimRejected          ActionKind = "claim_rejected"           // validation failed
ActionClaimSuperseded        ActionKind = "claim_superseded"         // replaced by newer claim
ActionActionPosted           ActionKind = "action_posted"            // set of claims issued as action
ActionCorrectiveIssued       ActionKind = "corrective_issued"        // system corrective claims
ActionBoardPhaseChanged      ActionKind = "board_phase_changed"      // phase transition
ActionBoardComplete          ActionKind = "board_complete"           // pipeline terminal
```

### 9.2 Resolution Mapping

| ActionKind | Resolution | Rationale |
|---|---|---|
| `claim_issued` | Coarse | Semantic, permanent |
| `claim_updated` | Medium | Moderate-volume, durable |
| `testament_submitted` | Coarse | Semantic response — proof of work |
| `claim_artifact_published` | Medium | Evidence; needs full-text index for search |
| `claim_validated` | Coarse | Issuer's verdict |
| `claim_accepted` | Coarse | Semantic terminal |
| `claim_rejected` | Coarse | Triggers remediation |
| `claim_superseded` | Medium | Lineage tracking |
| `action_posted` | Coarse | Action-level grouping |
| `corrective_issued` | Coarse | System guidance — permanent record |
| `board_phase_changed` | Medium | Lifecycle |
| `board_complete` | Coarse | Pipeline terminal |

### 9.3 Terminal and Paired Kinds

Terminal: `ActionClaimAccepted`, `ActionClaimRejected`, `ActionBoardComplete`

Paired: `ActionClaimIssued` -> `ActionClaimAccepted` (default), error -> `ActionClaimRejected`

### 9.4 Ambient Context

```
claims_board:
  phase: implementation | iteration: 1
  my_claims: 2 in_progress
    - "Implement HS256 JWK deserialization" (claim_id=abc, updated 30s ago)
      scope: services/auth/jwk.go
      validations: 2 pending
    - "Validate aud/iss claims against user email" (claim_id=def, updated 2m ago)
      scope: services/auth/claims.go
      validations: 2 pending, blocked_on: claim abc
  peer_progress: 8/12 claims testified
    - designer: "Apply design tokens to login form inputs" TESTIFIED (1m ago)
      testament: "Applied spacing-md and color-primary tokens to LoginForm inputs"
      artifacts: 2 (code_reference, diff)
    - tester: "Author HS256 deserialization test cases" IN_PROGRESS (45s ago)
  recent_testaments: 1
    - designer submitted testament for "Design error state for auth failure" (2m ago)
      artifacts: design_asset, a11y_audit
```

---

## 10. End-to-End Example

### Scenario: Add JWT authentication to an API service

**1. Architect assembles claim set, Inspector issues:**

The Architect produces an action with 8 precise claims. The Inspector formally issues them when the board is populated:

| Claim | Subject | Validations |
|---|---|---|
| "Implement HS256 JWK deserialization" | Engineer | "A JWK with a valid HS256 key deserializes" / "Returns typed JWK struct, no silent fallbacks"; "Unsupported algorithm returns ErrUnsupportedAlgorithm" / "Sentinel error, includes algorithm name" |
| "Validate `aud` and `iss` claims against user email" | Engineer | "Missing `aud` fails with descriptive error" / "Error includes which field is missing"; "Mismatched `iss` returns ErrIssuerMismatch" / "Sentinel error type" |
| "Implement token expiry middleware guard" | Engineer | "Expired token returns 401" / "Response body is RFC 7807"; "Valid token passes through with claims in context" / "Claims accessible via `ctx.Value`" |
| "Wire auth middleware to protected API routes" | Engineer | "Every protected route passes through JWT validation" / "No route can bypass auth — verify via route table inspection"; "Unprotected routes remain accessible" / "Health check and login endpoints return 200 without token" |
| "Apply design tokens to login form inputs" | Designer | "All spacing from design tokens" / "Zero hard-coded px values"; "All colors from design tokens" / "Zero hard-coded hex/rgb values" |
| "Design error state for auth failure" | Designer | "WCAG 2.1 AA contrast on error text" / "Contrast >= 4.5:1"; "Screen reader announces error" / "aria-live region with role=alert" |
| "Author HS256 deserialization test cases" | Tester | "Test covers valid key, invalid key, wrong algorithm" / "Minimum 3 test cases"; "Test uses table-driven pattern" / "Subtests with descriptive names" |
| "Author token expiry integration tests" | Tester | "End-to-end: login -> get token -> call protected endpoint -> verify" / "Single test exercises full flow"; "Expired token test with clock mock" / "No flaky sleep-based expiry" |

**2. Implementation Phase:**

All three agents work in parallel. Each submits testaments when done:

- Engineer submits testament for "Implement HS256 JWK deserialization": *"Implemented JWK validation using HS256 in `DeserializeHS256JWK()` method"* with artifacts: `code_reference: services/auth/jwk.go:47-89`, `diff: +42 lines`
- Designer submits testament for "Apply design tokens to login form inputs": *"Applied spacing-md and color-primary tokens to LoginForm component"* with artifacts: `code_reference: components/LoginForm.tsx:12-34`, `design_asset: token mapping sheet`
- Tester submits testament for "Author HS256 deserialization test cases": *"Authored 4 table-driven test cases in `TestDeserializeHS256JWK`"* with artifacts: `code_reference: services/auth/jwk_test.go:15-67`, `test_output: 4 subtests defined`
- Engineer consults Designer via consultation action: claim "Provide error state design for failed login" — Designer responds with testament referencing the error state component

**3. Transition to Validation:**

Orchestrator observes all 8 claims in `testified` status.

**4. Validation Phase:**

Inspector evaluates each testament's artifacts against validations:
- Checks `DeserializeHS256JWK()` code reference — validates sentinel error pattern, no silent fallbacks
- Tester runs `run_test_suite`, submits validation results for test-type validations
- Tester finds: integration test is flaky (uses `time.Sleep` instead of clock mock)
- Tester posts remediation action: rejects "Author token expiry integration tests", issues corrective claim "Replace `time.Sleep` expiry test with clock mock" against Engineer

**5. Re-entry:**

Engineer receives remediation claim, refactors test, submits testament with artifacts (code diff, test output showing consistent pass). Inspector validates — all pass.

**6. Pipeline Complete → Handoff:**

Board reaches `complete` (all claims accepted). Pipeline inspector calls `handoff_to_ot`:
- `MergePipelineIntoGreen` merges the pipeline's VFS into green, producing Copy_N at arrival_seq N
- Pipeline claims (with testaments and artifacts) serialized onto the MergeDescriptor
- Bus handoff message published: "new work for seq N"
- Pipeline VFS and pod released

**7. Global Review:**

Global Inspector and Global Tester receive the bus handoff message and begin watching for Copy_N:

- Copy_N materializes (byte-for-byte VFS replica)
- Global Inspector replica starts: audits Copy_N against the pipeline's claims — checks that each testament's artifacts actually exist in the VFS, meet the validation quality bars, cohere architecturally
- Inspector accepts → Global Tester replica starts: runs integration tests against Copy_N, validates test-type claims
- Tester accepts → posts acceptance claims with test result artifacts
- Tester's acceptance claims trigger the Global Inspector → inspector validates tester's testaments → triggers disk commit on commit queue for seq N
- Commit queue advances, water line moves, next DAG layer proceeds

---

## 11. Changes by Component

### 11.1 New Package: `core/pipeline/claims/`

| File | Contents |
|---|---|
| `types.go` | Relation, StatusChange, ClaimScopeEntry, Action, Claim, Testament, Artifact, Validation, ClaimProgressUpdate, all enums |
| `board.go` | ClaimsBoard struct, all mutation operations, query operations, projection, subscription |
| `board_durable.go` | WAL persistence — event types, checkpoint, apply handlers, recovery |
| `board_amplifier.go` | Fabric activity emission for claims, testaments, artifacts |

**WAL event types:**
```go
const (
    eventActionPosted        = "action_posted"
    eventClaimUpdated        = "claim_updated"
    eventTestamentSubmitted  = "testament_submitted"
    eventValidationEvaluated = "validation_evaluated"
    eventClaimAccepted       = "claim_accepted"
    eventClaimRejected       = "claim_rejected"
    eventClaimSuperseded     = "claim_superseded"
    eventPhaseTransition     = "phase_transition"
    eventBoardComplete       = "board_complete"
)
```

### 11.2 Architect: Claim Generation

**Files:** `agents/architect/types.go`, `planner_anthropic.go`, `skills_planning.go`

- Add `TaskClaim` and `TaskClaimValidation` types (agent relationships expressed via Relations)
- Add `Claims []TaskClaim` to `HandoffTask` and `AtomicTask`
- Extend LLM prompt to produce precise, atomic claims (not vague task descriptions)
- Claims assembled by Architect, formally issued by Inspector

### 11.3 Orchestrator: Claims Pipeline Controller

**New file:** `agents/orchestrator/claims_pipeline.go`

- Creates board, Inspector-issues claims from Architect's assembly
- Dispatches all subjects simultaneously
- Monitors testament submissions
- Transitions to validation when all testified
- Handles remediation loop
- Calls PipelineCommitter on completion/failure

### 11.4 Agent Skills

**New file:** `agents/shared/claims_skills.go`

- `query_claims_board` — read board state
- `post_action` — issue action (set of claims) — covers challenge, consultation, corrective, task
- `submit_testaments` — submit a set of testaments (as a testament action) with artifacts
- `update_claim_progress` — atomic progress update
- `evaluate_validation` — evaluate testament artifacts against validations
- `post_remediation_claims` — reject + post replacements
- `inspect_claim_conflicts` — overlapping claims, competing testaments

### 11.5 Task Context Rendering

**File:** `agents/shared/pipeline_task_context.go`

Claims board section showing claims, testaments, artifacts, peer progress.

### 11.6 Task State

**File:** `core/pipeline/taskstate/state.go`

Add `StatusImplementing` replacing `StatusDefiningCriteria` + `StatusCreatingTests` + `StatusExecuting`.

---

## 12. Phased Implementation Plan

The conversion is broken into 14 phases, executed in dependency order.
Each phase contains items; each item has explicit acceptance criteria
and a complete test ladder (unit, integration, end-to-end, race
condition, negative / non-happy path) following the same conventions
as docs/CLUSTER.md's implementation plan. Phases are independently
shippable — landing phase N never destabilizes phase N-1's behavior.

**Test convention** throughout this section:

- **Unit tests**: single function/struct in isolation. `_test.go` per
  package. <100ms each.
- **Integration tests**: multi-component composition. Real disk, real
  goroutines, real claims board (in-process). <10s each.
- **End-to-end tests**: cross-process / cross-agent behavior. Real
  pipelines, real handoff, real Fabric. <60s each.
- **Race condition tests**: `go test -race`; concurrent mutations,
  memory ordering, pool/arena races.
- **Negative / non-happy path tests**: error cases, partial failures,
  malformed inputs, adversarial scenarios, edge cases, recovery.

Every item below also includes invariants the implementation must
preserve under all conditions (crash, partition, restart, concurrent
writes). These are testable via property-based tests (`testing/quick`).

**Substrate integration**: Phase 13 brings the entire claims system
onto the CLUSTER.md substrate. Phases 0-12 implement the claims model
on the existing pre-substrate foundation; Phase 13 migrates to the
substrate without changing semantics. This separation lets claims
ship before the substrate exists, then graduates onto the substrate
when ready.

---

### Phase 0 — Core Claims Infrastructure

**What**: Foundational types, board, persistence, and amplifier.
Everything else depends on this.

**Phase implementation overview**: Phase 0 builds the sovereign claims
store with its own WAL discipline and Fabric amplifier — exactly as
described in §3 / §14.2. This is the fastest path to a working claims
system; substrate migration (Phase 13) replaces the WAL + amplifier
with substrate primitives once the substrate exists. Common
dependencies: `core/activity` (existing), `core/concurrency`
(GoroutineScope), existing `durableProtocolLog` patterns.

#### 0.1 — Core types

**Description**: All claims types in `core/claims/types.go`. The 9
universal base fields + per-type semantic fields per §4.

**Implementation approach**:
- Package: `core/claims` (system-wide, not pipeline-specific).
- Types: `Relation`, `StatusChange`, `ClaimScopeEntry`, `Action`,
  `Claim`, `Testament`, `Artifact`, `Validation`,
  `ClaimProgressUpdate`, all enums.
- Relations are uniform: no special-case fields for issuer/subject/
  parent action/dependencies/supersession — all encoded as Relations.
- Errors-as-artifacts: `Artifact.Kind` is a string (not enum), `error`
  / `error_trace` / `error_diagnostic` are first-class kinds.
- All types JSON-serializable (for WAL); deterministic serialization
  via canonical JSON ordering.
- Code path: `core/claims/types.go` (~1500 LOC).

**Acceptance criteria**:
- All types compile, JSON-roundtrip, satisfy `testing/quick` round-
  trip property tests.
- 9 universal base fields present on all 5 entity types.
- Relations cover all relationship kinds (issuer, subject, peer,
  caused_by, supersedes, refines, depends_on, conflicts_with,
  derived_from, in_scope_of, direct_addressed).
- Enums (ClaimStatus, ValidationStatus, ActionType, ActionKind,
  ScopeKind, ConfidenceLevel) exhaustive.
- Status histories embedded on Action / Claim / Validation; each
  StatusChange carries reason, agent, timestamp.
- ContentHash field on Artifact computed via BLAKE3 over canonical
  reference encoding.

**Unit tests**:
- `TestClaims_Types_RoundTripJSON` (property) — All types JSON-RT.
- `TestClaims_Relations_AllKindsExpressible` — Each relationship kind
  serialises and dispatches correctly.
- `TestClaims_StatusHistory_TransitionsCarryMetadata` — reason / agent /
  timestamp populated.
- `TestClaims_Artifact_KindsAreStrings` — Polymorphic Kind respected
  in dispatch.
- `TestClaims_Errors_AsArtifacts` — `kind: "error"` round-trips.
- `TestClaims_ContentHash_Deterministic` — Same artifact reference →
  same hash.
- `TestClaims_Validation_RequiredVsOptional` — Required/optional flag
  respected in completion checks.

**Integration tests**:
- `TestClaims_TypeSet_SchemaCompleteness` — Every section-4 example
  expressible.
- `TestClaims_RelationGraph_CycleDetection` — `supersedes` /
  `caused_by` cycles detected.

**End-to-end tests**: deferred to Phase 2.

**Race condition tests**:
- `TestClaims_Types_ConcurrentMarshal` — `go test -race` parallel
  marshal/unmarshal; consistent.

**Negative / non-happy path tests**:
- `TestClaims_BadEnumValue_RejectedAtUnmarshal` — Unknown enum value
  errs cleanly.
- `TestClaims_MissingRequiredField_ErrorsAtCreation` — Builder catches
  required-field omission.
- `TestClaims_RelationToNonexistentEntity_DetectedDownstream` — Bad
  relation flagged at board insert (Phase 0.2).
- `TestClaims_OversizedDescription_Rejected` — Schema-bounded sizes
  enforced.
- `TestClaims_StatusHistoryUnboundedGrowth_Bounded` — Status history
  bounded per entity.

---

#### 0.2 — ClaimsBoard

**Description**: Sovereign store with all operations, projection,
subscription.

**Implementation approach**:
- Package: `core/claims/board.go`.
- In-memory state: flat maps for all 5 entity types
  (`actions`, `claims`, `testaments`, `artifacts`, `validations`)
  keyed by ID, plus relation index `(entity_id, role) → []entity_id`.
- Single `sync.RWMutex` protects the whole board (claims-board access
  is bursty, not high-throughput; lock contention is not the bottleneck).
- Operations: `PostAction`, `SubmitTestaments`, `EvaluateValidation`,
  `RejectClaim`, `UpdateClaimProgress`, `MarkComplete`, phase
  transitions, queries.
- Projection: read-side denormalised view derived from primary
  state; recomputed on each mutation under the write lock.
- Subscription: bounded async channel per subscriber; reactive updates
  on every mutation; backpressure via slow-consumer drop with metrics.
- GoroutineScope: every async dispatcher wrapped; clean shutdown.
- Code path: `core/claims/board.go` (~3000 LOC).

**Acceptance criteria**:
- All operations atomic under the write lock.
- Phase transitions enforce preconditions: testified (all claims
  testified), re-entry (board phase = validation), completion (all
  validations passed).
- Projection consistent with primary state at every mutation
  boundary.
- Subscriber dispatch bounded; slow subscriber dropped cleanly with
  alert.
- Read queries (no mutation) take the read lock; concurrent reads
  permitted.
- All goroutines tracked via GoroutineScope; clean shutdown via
  scope cancellation.
- No goroutine leaks (`goleak.VerifyNone(t)`).

**Unit tests**:
- `TestBoard_PostAction_AllRelationsRecorded` — Action posts; all
  relations indexed.
- `TestBoard_SubmitTestaments_StatusTransitions` — Claim →
  `testified`.
- `TestBoard_EvaluateValidation_AllPass_ClaimAccepted` — Acceptance.
- `TestBoard_RejectClaim_RemediationPath` — Rejection requires
  remediation claim.
- `TestBoard_UpdateClaimProgress_Append` — Progress entries
  accumulate.
- `TestBoard_MarkComplete_Preconditions` — Completion gated.
- `TestBoard_PhaseTransitions_AllRules` — Each precondition.
- `TestBoard_Projection_ConsistentAfterMutation` — Projection matches
  primary.
- `TestBoard_Subscription_BoundedChannel` — Bounded; slow consumer
  dropped.
- `TestBoard_NoGoroutineLeak` (`goleak.VerifyNone`).

**Integration tests**:
- `TestBoard_FullClaimLifecycle` — Issue → progress → testify →
  validate → accept.
- `TestBoard_RemediationLoop` — Reject → remediation claim → re-
  testify → accept.
- `TestBoard_PeerVisibility` — Cross-agent queries return correct
  view.
- `TestBoard_LargeBoard_Scalable` — 10K claims; queries bounded.

**End-to-end tests**: deferred to Phase 2.

**Race condition tests**:
- `TestBoard_ConcurrentPostAction` — `go test -race` 1K parallel
  posts; no race.
- `TestBoard_ConcurrentReadDuringMutation` — Reads consistent under
  concurrent mutation.
- `TestBoard_SubscriptionDispatchRace` — Subscriber dispatch race
  with mutation; consistent ordering.
- `TestBoard_ScopedShutdownRace` — Shutdown race with active
  mutations; clean.

**Negative / non-happy path tests**:
- `TestBoard_DuplicateClaimID_Rejected` — Duplicate at insert.
- `TestBoard_MalformedRelation_Rejected` — Missing fields at insert.
- `TestBoard_TransitionOutOfOrder_Refused` — Skipping testified
  before validation.
- `TestBoard_TestifyNonexistentClaim_Rejected` — Subject of testament
  doesn't exist.
- `TestBoard_RejectAlreadyAccepted_Refused` — Cannot reject after
  acceptance.
- `TestBoard_SubscribeAfterShutdown_ErrorReturned` — Subscribe post-
  shutdown errors.
- `TestBoard_OutOfMemoryProjection_BoundedDegradation` — Projection
  bounded under pathological input.

---

#### 0.3 — WAL persistence

**Description**: 10 WAL event types, checkpoint, apply handlers,
recovery via replay. Same `durableProtocolLog` pattern as existing
sovereign systems.

**Implementation approach**:
- Package: `core/claims/board_durable.go`.
- 10 events: `action_posted`, `claim_progress_updated`,
  `testament_submitted`, `artifact_published`, `validation_evaluated`,
  `claim_accepted`, `claim_rejected`, `claim_superseded`,
  `phase_changed`, `board_completed`.
- Checkpoint struct serializes full board state.
- Append path: write event → fsync → apply to in-memory board (under
  write lock).
- Recovery: load latest checkpoint → replay events from checkpoint
  HLC forward.
- WAL file format: existing `durableProtocolLog` framing.
- Code path: `core/claims/board_durable.go` (~2500 LOC).

**Acceptance criteria**:
- All board mutations go through WAL before in-memory apply.
- Crash mid-apply: recovery preserves in-flight mutation if WAL'd,
  drops if not.
- Idempotent apply: re-applying same WAL event is no-op.
- Checkpoint serialisation/deserialisation round-trips.
- Recovery time bounded by `events_since_last_checkpoint`.
- Concurrent appends serialised via the same write lock as in-memory.

**Unit tests**:
- `TestBoardWAL_AppendEvent` — Event appended; in-memory updated.
- `TestBoardWAL_CheckpointRoundTrip` — Checkpoint serialises +
  deserialises identically.
- `TestBoardWAL_ReplayFromCheckpoint` — Replay restores state.
- `TestBoardWAL_IdempotentApply` — Same event applied twice is no-op.
- `TestBoardWAL_FsyncBeforeApply` — fsync precedes apply.

**Integration tests**:
- `TestBoardWAL_CrashRecovery` — Subprocess kill; recovery restores.
- `TestBoardWAL_LargeWAL_Compaction` — Periodic checkpoints; WAL
  bounded.

**End-to-end tests**: deferred.

**Race condition tests**:
- `TestBoardWAL_ConcurrentAppend_Serialised` — `go test -race`.
- `TestBoardWAL_CheckpointRaceWithAppend` — Checkpoint mid-append;
  consistent.

**Negative / non-happy path tests**:
- `TestBoardWAL_TruncatedFile_LastEntryDropped` — Recovery handles
  truncation.
- `TestBoardWAL_CorruptedEvent_DetectedViaCRC` — Bit flip in event;
  detected.
- `TestBoardWAL_DiskFullDuringAppend_BackpressureToCaller` —
  No silent loss.
- `TestBoardWAL_CheckpointFailureMidWrite_OldVersionPreserved` —
  Atomic via temp + rename.
- `TestBoardWAL_StaleCheckpointMissingEvents_Detected` — Replay
  detects gap.

---

#### 0.4 — Board amplifier

**Description**: Fabric activity emission for every board mutation.
All 12 ActionKinds (§9.1).

**Implementation approach**:
- Package: `core/claims/board_amplifier.go`.
- Subscribes to board events; for each event, emits one Fabric
  activity with the corresponding ActionKind (§9.1).
- Activity fields: `Kind`, `Resolution` (from §9.2 mapping),
  `Subject.Coordinates["task_id"]`, `Causal` chain via Relation
  traversal.
- Async dispatch via GoroutineScope; backpressure-aware.
- Code path: `core/claims/board_amplifier.go` (~800 LOC).

**Acceptance criteria**:
- Every board mutation emits exactly one Fabric activity.
- ActionKind matches the mutation type per §9.1.
- Resolution per §9.2.
- Causal chain populated from Relations.
- Async; doesn't block board write path.
- Backpressure handled (drop oldest under pressure with metric).

**Unit tests**:
- `TestAmplifier_PostAction_EmitsActionPosted` — Correct kind.
- `TestAmplifier_TestamentSubmitted_EmitsKind` — Correct kind.
- `TestAmplifier_ArtifactPublished_LinkedToTestament` — Causal chain.
- `TestAmplifier_ResolutionMapping` — Per §9.2.
- `TestAmplifier_AsyncDispatch_BoardNotBlocked` — Board write path
  unaffected.

**Integration tests**:
- `TestAmplifier_FullLifecycle_AllActivitiesEmitted` — Lifecycle
  produces all expected activities.
- `TestAmplifier_FabricLensQuery` — Lens queries return amplifier
  output.

**End-to-end tests**: deferred to Phase 2.

**Race condition tests**:
- `TestAmplifier_ConcurrentEvents` — `go test -race`.
- `TestAmplifier_ShutdownRaceWithDispatch` — Shutdown drains.

**Negative / non-happy path tests**:
- `TestAmplifier_FabricUnavailable_BackpressureNotPropagated` —
  Fabric down; amplifier degrades; board unaffected.
- `TestAmplifier_OversizedActivity_Truncated` — Truncated with
  marker.
- `TestAmplifier_DispatchQueueFull_DropsOldest` — Bounded with
  metric.

---

#### 0.5 — Claims skill factories

**Description**: Shared skills for all agents:
`query_claims_board`, `post_action`, `submit_testaments`,
`update_claim_progress`, `evaluate_validation`,
`post_remediation_claims`, `inspect_claim_conflicts`.

**Implementation approach**:
- Package: `core/claims/skills.go`.
- Each skill is a factory: `func(board *ClaimsBoard,
  agent_id string) ToolDefinition`.
- Skill definitions follow existing tool-runtime conventions:
  `name`, `description`, `inputs` JSON schema, `handler` function.
- Skills validate inputs against §4 type schemas; reject malformed.
- Skills enforce per-agent permissions (e.g., only the claim's
  issuer can `evaluate_validation`).
- Code path: `core/claims/skills.go` (~1500 LOC).

**Acceptance criteria**:
- All 7 skills implemented and exposable to agents.
- Input validation rejects malformed inputs with actionable errors.
- Permissions enforced (issuer-only operations).
- Skills idempotent where appropriate (multiple `submit_testaments`
  for same claim coalesce / no-op).
- Skill output structured (testaments returned with full type).

**Unit tests**:
- `TestSkill_QueryClaimsBoard_Filters` — Filter by agent / status /
  scope.
- `TestSkill_PostAction_RoundTrip` — Action posted; visible.
- `TestSkill_SubmitTestaments_Idempotent` — Re-submit no-op.
- `TestSkill_EvaluateValidation_IssuerOnly` — Non-issuer rejected.
- `TestSkill_PostRemediationClaims_LinkedViaRelation` — Remediation
  links to original.
- `TestSkill_InspectClaimConflicts_FindsScopeOverlaps` — Detects.
- `TestSkill_AllInputsValidated` — Bad inputs rejected.

**Integration tests**:
- `TestSkill_FullAgentWorkflow` — Skill chain through full claim
  lifecycle.
- `TestSkill_CrossAgentInteraction` — Skills work cross-agent.

**End-to-end tests**:
- `TestSkillE2E_AllAgentsUseSharedSkills` — All agents use the same
  skill registrations.

**Race condition tests**:
- `TestSkill_ConcurrentInvocations` — `go test -race`.
- `TestSkill_BoardMutationDuringQuery` — Read consistent.

**Negative / non-happy path tests**:
- `TestSkill_UnknownClaimID_ErrorReturned` — Bad ID errs.
- `TestSkill_PermissionDenied_StructuredError` — Authorization fails;
  structured error.
- `TestSkill_BoardShutdownDuringInvocation_HandledGracefully` —
  Shutdown.
- `TestSkill_OversizedActionInput_Rejected` — Bounded.
- `TestSkill_RecursiveRelationGraph_BoundedTraversal` — Bounded.

---

### Phase 1 — Fabric Integration

**What**: The Fabric learns to observe and surface claims, testaments,
and artifacts via new ActionKinds, ambient context, and lens skills.

**Phase implementation overview**: Phase 1 wires the claims board's
output into the Fabric's existing ActionKind / lens / ambient context
machinery. The amplifier (Phase 0.4) emits the activities; this phase
adds the kinds + lens skills + ambient digest. Substrate migration
(Phase 13) re-routes the Fabric to consume the substrate claims
subject directly.

#### 1.1 — ActionKind constants and metadata

**Description**: 12 new ActionKinds (§9.1) wired into ResolutionFor,
IsTerminal, paired kinds.

**Implementation approach**:
- Package: `core/activity/action_kind.go`.
- Add constants per §9.1.
- Update `ResolutionFor` per §9.2.
- Add `IsTerminal` cases for `claim_accepted`, `claim_rejected`,
  `board_complete`.
- Add `PairedKind` mapping: `claim_issued` → `claim_accepted` (default).
- Code path: existing file extended (~200 LOC added).

**Acceptance criteria**:
- All 12 ActionKinds defined.
- ResolutionFor returns correct resolution per §9.2.
- IsTerminal correct.
- Paired-kind mapping correct.
- Existing tests for other ActionKinds unaffected.

**Unit tests**:
- `TestActionKind_AllClaimsKindsDefined` — 12 kinds present.
- `TestActionKind_ResolutionMapping` — §9.2 verified.
- `TestActionKind_TerminalDetection` — Terminal kinds detected.
- `TestActionKind_PairedMapping` — Mapping correct.

**Integration tests**: covered by Phase 0.4 amplifier tests.

**End-to-end tests**: deferred.

**Race condition tests**: N/A (constants).

**Negative / non-happy path tests**:
- `TestActionKind_UnknownKind_DefaultResolution` — Default fallback.

---

#### 1.2 — ClaimsBoardDigest in AmbientEnvelope

**Description**: Extend `AmbientEnvelope` with claims state: my claims,
peer progress, recent testaments, blocked claims, board phase. Per
§9.4.

**Implementation approach**:
- Package: `core/activity/lenses/ambient.go`.
- Add `ClaimsBoardDigest` struct with fields per §9.4.
- Render method consumes claims board projection + lens query result.
- Bounded size (max recent testaments, max peer progress entries).
- Code path: `core/activity/lenses/ambient.go` (~600 LOC added).

**Acceptance criteria**:
- AmbientEnvelope includes ClaimsBoardDigest.
- Size bounded.
- Renders within latency budget (<5ms typical).
- Stale-tolerant: digest is best-effort snapshot; no transactional
  consistency required.

**Unit tests**:
- `TestAmbient_ClaimsBoardDigest_Rendered` — Digest present.
- `TestAmbient_DigestSizeBounded` — Bounded.
- `TestAmbient_DigestRenderLatency` — < 5ms.
- `TestAmbient_StaleSnapshotTolerated` — Stale OK.

**Integration tests**:
- `TestAmbient_DigestRealisticBoard` — Realistic board shape.
- `TestAmbient_PeerProgressVisibility` — Cross-agent visibility.

**End-to-end tests**:
- `TestAmbientE2E_AgentSeesDigest` — Agent receives digest in tool
  results.

**Race condition tests**:
- `TestAmbient_DigestRenderDuringMutation` — `go test -race`.

**Negative / non-happy path tests**:
- `TestAmbient_BoardUnavailable_DegradedDigest` — Board down; digest
  shows degraded marker; no panic.
- `TestAmbient_OversizedBoardSnapshot_TruncatedGracefully` —
  Truncation with marker.

---

#### 1.3 — Claims awareness skills

**Description**: `query_claims_board` (Fabric lens), `query_peer_claims`,
`inspect_claim_conflicts`. Registered in
`FabricAwarenessSkillNames`.

**Implementation approach**:
- Package: `core/fabric/claim_awareness_skills.go`.
- Skills wrap claims-board queries with Fabric-style tool input/output
  conventions.
- `query_peer_claims`: queries claims by `Subject.Coordinates`.
- `inspect_claim_conflicts`: walks scope claims for overlaps; returns
  conflicting claims with their issuers.
- Code path: ~700 LOC.

**Acceptance criteria**:
- 3 skills implemented.
- Registered in FabricAwarenessSkillNames.
- Filter inputs: agent / status / scope / time range.
- Output structured (Claim records with relations expanded).

**Unit tests**:
- `TestAwarenessSkill_QueryClaimsBoard_Filters` — Filter dispatch.
- `TestAwarenessSkill_QueryPeerClaims_CrossAgent` — Cross-agent.
- `TestAwarenessSkill_InspectClaimConflicts_DetectsOverlap` —
  Overlapping scopes.

**Integration tests**:
- `TestAwarenessSkill_RealisticBoardShape` — Real board; queries
  meaningful.

**End-to-end tests**:
- `TestAwarenessSkillE2E_AgentDiscoversConflict` — Realistic
  conflict-detection scenario.

**Race condition tests**:
- `TestAwarenessSkill_ConcurrentQueries` — `go test -race`.

**Negative / non-happy path tests**:
- `TestAwarenessSkill_NoMatchingClaims_EmptyResult` — Empty result OK.
- `TestAwarenessSkill_LargeResult_PaginationOrTruncation` — Bounded.
- `TestAwarenessSkill_BadFilter_RejectedAtInput` — Validation.

---

#### 1.4 — Claim-scoped communication skill consolidation

**Description**: `consult_peer` and `challenge_peer` retired as
separate skills. Challenges and consultations are actions posted via
`post_action`. Existing Fabric lenses query these like any other
claim activity.

**Implementation approach**:
- Package: `core/fabric/awareness_skills.go`.
- Remove standalone `consult_peer` / `challenge_peer` registrations.
- Wire any agent code that previously called them to use
  `post_action` with `ActionType=Consultation` / `Challenge`.
- Update lens queries to filter by `Action.Type` for consultation /
  challenge views.
- Code path: refactor existing file (~400 LOC removed, ~200 LOC
  added).

**Acceptance criteria**:
- Standalone skills removed.
- Lens queries still surface consultation / challenge views.
- Agents that previously used these skills now use `post_action`.
- Backward-compat shim: legacy callers get a deprecation warning
  during cutover; removed at end of cutover window.

**Unit tests**:
- `TestSkillRetirement_StandaloneRemoved` — Skill not in
  AwarenessSkillNames.
- `TestSkillRetirement_PostActionPath` — Consultation via
  `post_action` works end-to-end.
- `TestSkillRetirement_LensFiltersByType` — Filter by ActionType.

**Integration tests**:
- `TestSkillRetirement_NoOrphanCallers` — Codebase scan; no remaining
  calls to removed skills.

**End-to-end tests**:
- `TestSkillRetirementE2E_RealAgentConsultation` — Real consultation
  scenario.

**Race condition tests**: N/A (refactor).

**Negative / non-happy path tests**:
- `TestSkillRetirement_LegacyCaller_DeprecationWarning` — Backward-
  compat warning during cutover.
- `TestSkillRetirement_PostCutover_LegacyCallerErrors` — After
  cutover, legacy caller errors clearly.

---

### Phase 2 — Pipeline Agent Conversion

**What**: The 4 pipeline agent types (Inspector, Tester, Engineer,
Designer) convert from the protocol state machine to claims. Pipeline
protocol retired.

**Phase implementation overview**: Phase 2 is the largest single phase
in the conversion. Each of the 4 agents loses its protocol state
machine + protocol-specific skills; gains the 7 claims skills (Phase
0.5); rewrites its tool loop to operate on the claims board. The
pipeline protocol itself (state machine, durable events, projection,
sub-node expansion in orchestrator) is fully retired. Common pattern
across agents: retire ~10 protocol skills, register ~5 claims skills,
rewrite agent's main loop to query board state, perform work, submit
testaments. The differences per agent are in *what* claims they issue
or testify against.

#### 2.1 — Pipeline Inspector

**Description**: Per §14.4 #2a. Retire 12 protocol skills; register 5
claims skills; issue claims on board creation; evaluate testaments;
post remediation; commit on board complete.

**Implementation approach**:
- Package: `agents/inspector/pipeline/`.
- Retire skills: `challenge_agent`, `handoff_next`, `validate_work`,
  `process_validation`, `finalize_pipeline`, `handoff_to_ot`,
  `discard_pipeline`, `discard_queued_artifacts`,
  `query_pipeline_state`, `challenge_peer`, `consult_peer`,
  `inspect_open_conflicts`.
- Register: `query_claims_board`, `post_action`, `evaluate_validation`,
  `post_remediation_claims`, `inspect_claim_conflicts`.
- On board creation: claims (assembled by architect) carry inspector's
  AgentID as issuer.
- During validation phase: evaluate each testament against each
  validation; status = `passed` / `failed`.
- On validation failure: `post_remediation_claims` with corrective
  validations.
- On `MarkComplete`: invoke `PipelineCommitter.MergePipelineIntoGreen`
  + bus event.
- Code path: ~1500 LOC modifications.

**Acceptance criteria**:
- All 12 protocol skills removed; not callable.
- All 5 claims skills registered and callable.
- Inspector issues claims with itself as issuer.
- Inspector evaluates each testament's artifacts against each
  validation independently.
- Remediation loop bounded (max iterations per §14 — pipeline
  protocol's `MaxReviewRounds`).
- Board complete triggers VFS merge + bus publish.
- Existing inspector behavior tests pass against claims-based
  implementation.

**Unit tests**:
- `TestInspector_ProtocolSkillsRetired` — Skills not in registry.
- `TestInspector_ClaimsSkillsRegistered` — All 5 present.
- `TestInspector_IssueClaimsOnBoardCreation` — Claims issued; correct
  issuer relation.
- `TestInspector_EvaluateValidationFromArtifacts` — Each validation
  evaluated independently.
- `TestInspector_PostRemediationOnFailure` — Remediation claim posted
  with correct supersedes relation.
- `TestInspector_BoundedRemediationLoop` — Max iterations.
- `TestInspector_BoardCompleteTriggersMerge` — Merge invoked.

**Integration tests**:
- `TestInspector_FullPipelineLifecycle` — Issue → testify → validate
  → accept → complete.
- `TestInspector_RemediationLoop` — Reject → remediate → re-testify →
  accept.
- `TestInspector_ExistingBehaviorPreserved` — Pre-conversion test
  suite passes.

**End-to-end tests**:
- `TestInspectorE2E_RealisticPipeline` — Real pipeline with real
  artifacts.

**Race condition tests**:
- `TestInspector_ConcurrentTestaments` — `go test -race` parallel
  testaments; consistent eval.
- `TestInspector_RemediationRaceWithCompletion` — Remediation race
  with completion attempt; deterministic.

**Negative / non-happy path tests**:
- `TestInspector_TestamentForUnknownClaim_Rejected` — Unknown claim.
- `TestInspector_EvaluateBeforeTestify_Refused` — Premature.
- `TestInspector_RemediationLoopExceedsBound_Rollback` — Max
  exceeded; rollback to architect.
- `TestInspector_VFSMergeFails_BoardStaysComplete_AlertEmitted` —
  Merge fails post-acceptance; alert.
- `TestInspector_ProtocolSkillCalled_DeprecationError` — Old skill
  call returns clear error.

---

#### 2.2 — Pipeline Tester

**Description**: Per §14.4 #2b. Retire 9 protocol+challenge skills;
register 7 claims skills; phase-gate test execution; submit testaments
for test authoring + execution; remediate on test failures.

**Implementation approach**:
- Package: `agents/tester/pipeline/`.
- Retire 9 protocol/challenge skills.
- Register 7: 5 standard claims skills + `submit_testaments` +
  `update_claim_progress`.
- Phase-gate `run_test_suite`: blocked during `implementation` phase;
  returns corrective action if invoked.
- During implementation phase: test authoring submits testaments with
  test file artifacts.
- During validation phase: test execution submits testaments with
  test execution artifacts.
- On test failure: `post_remediation_claims` against engineer with
  failure artifacts.
- Code path: ~1200 LOC modifications.

**Acceptance criteria**:
- 9 retired skills not callable.
- 7 claims skills registered.
- Phase gate enforced.
- Testaments distinguish authoring (test file artifacts) from
  execution (run output artifacts).
- Test failures produce remediation claims with diagnostic artifacts.
- Existing tester behavior tests pass.

**Unit tests**:
- `TestTester_ProtocolSkillsRetired` — Skills removed.
- `TestTester_ClaimsSkillsRegistered` — All 7 present.
- `TestTester_PhaseGateBlocksRunTestSuite` — Gate enforced.
- `TestTester_PhaseGateCorrectiveAction` — Corrective on premature
  call.
- `TestTester_AuthoringTestamentArtifacts` — Test file artifacts.
- `TestTester_ExecutionTestamentArtifacts` — Run output artifacts.
- `TestTester_FailureProducesRemediation` — Remediation with
  diagnostic.

**Integration tests**:
- `TestTester_FullTestLifecycle` — Author → execute → testify.
- `TestTester_FailureRemediationLoop` — Failure → remediation →
  pass.

**End-to-end tests**:
- `TestTesterE2E_RealisticPipeline` — Real test suite execution.

**Race condition tests**:
- `TestTester_ConcurrentTestExecution` — `go test -race`.
- `TestTester_AuthoringRaceWithExecution` — Phase boundary race.

**Negative / non-happy path tests**:
- `TestTester_RunTestSuiteBeforeImplementationDone_Refused` — Phase
  gate.
- `TestTester_TestExecutionTimesOut_TimeoutArtifact` — Captured.
- `TestTester_TestProcessKilled_ErrorArtifact` — Captured.
- `TestTester_TestOutputUnparseable_DiagnosticArtifact` — Diagnostic.
- `TestTester_FlakyTestDetection_QuarantineFlag` — Flaky flagged.

---

#### 2.3 — Engineer

**Description**: Per §14.4 #2c. Retire 8 protocol/challenge skills;
register 5 claims skills; work scope-bounded by claims; emit progress
on every operation; complete with code-reference + diff artifacts.

**Implementation approach**:
- Package: `agents/engineer/`.
- Retire 8 protocol/challenge skills.
- Register 5: `query_claims_board`, `post_action`,
  `submit_testaments`, `update_claim_progress`,
  `inspect_claim_conflicts`.
- Every file write / tool invocation produces an
  `update_claim_progress`.
- Completion produces a testament with `code_reference` + `diff`
  artifacts.
- Scope enforced by claims: VFS write paths must be within the
  active claim's scope; out-of-scope writes refused.
- Code path: ~1500 LOC modifications.

**Acceptance criteria**:
- 8 retired skills not callable.
- 5 claims skills registered.
- Scope enforcement: VFS write outside claim scope rejected.
- Progress entries on every operation.
- Completion testament has diff + code_reference artifacts.
- No protocol-state-machine references remain.

**Unit tests**:
- `TestEngineer_ProtocolSkillsRetired` — Skills removed.
- `TestEngineer_ClaimsSkillsRegistered` — All 5 present.
- `TestEngineer_VFSWriteScopeEnforced` — Out-of-scope write
  refused.
- `TestEngineer_ProgressOnEveryOperation` — Each write/invoke logs
  progress.
- `TestEngineer_CompletionTestamentArtifacts` — Diff +
  code_reference.

**Integration tests**:
- `TestEngineer_FullImplementationLifecycle` — Claim → work →
  testify.
- `TestEngineer_ScopeBoundedAcrossOps` — Multi-op, all scope-
  bounded.

**End-to-end tests**:
- `TestEngineerE2E_RealistFeature` — Real feature implementation.

**Race condition tests**:
- `TestEngineer_ConcurrentClaimsConcurrentScopes` — `go test -race`.
- `TestEngineer_ScopeChangesDuringWrite` — Scope updated mid-write;
  refused or completed cleanly.

**Negative / non-happy path tests**:
- `TestEngineer_OutOfScopeWrite_Rejected` — Scope refused.
- `TestEngineer_ToolFailureProducesErrorArtifact` — Errors-as-
  artifacts.
- `TestEngineer_OOMDuringWork_ErrorArtifactSubmitted` — Failure
  artifact.
- `TestEngineer_ConcurrentClaimsContendForScope_Refused` — Conflict.
- `TestEngineer_OversizedDiff_Truncated` — Diff truncated with
  marker.

---

#### 2.4 — Designer

**Description**: Per §14.4 #2d. Same shape as Engineer with design-
specific artifacts.

**Implementation approach**:
- Package: `agents/designer/`.
- Retire 8 protocol/challenge skills; register 5 claims skills.
- Testaments carry `design_asset`, `a11y_audit`, design-token
  mapping artifacts.
- Code path: ~1300 LOC modifications.

**Acceptance criteria**: as Engineer (2.3) but with designer-specific
artifact kinds.

**Unit tests**:
- `TestDesigner_ProtocolSkillsRetired`.
- `TestDesigner_ClaimsSkillsRegistered`.
- `TestDesigner_DesignAssetArtifact` — Asset reference.
- `TestDesigner_A11yAuditArtifact` — WCAG findings.
- `TestDesigner_TokenMappingArtifact` — Token mapping.

**Integration tests**:
- `TestDesigner_FullDesignLifecycle`.

**End-to-end tests**:
- `TestDesignerE2E_RealistFeature`.

**Race condition tests**:
- `TestDesigner_ConcurrentClaims` — `go test -race`.

**Negative / non-happy path tests**:
- `TestDesigner_A11yFailureProducesArtifact` — Failure captured.
- `TestDesigner_OutOfScopeAssetEdit_Rejected` — Scope.

---

#### 2.5 — Pipeline Protocol Retirement

**Description**: Per §14.4 #2e. Remove protocol state machine, durable
events, projection, sub-node expansion, orchestrator pipeline runtime
protocol path.

**Implementation approach**:
- Delete `agents/shared/pipeline_protocol.go` (snapshot, turn action,
  state, reducer, mailbox obligations, terminal action guards).
- Delete `agents/shared/pipeline_protocol_durable.go` (7 durable
  events).
- Delete `agents/shared/pipeline_projection.go` (replaced by
  `ClaimsBoardProjection`).
- Delete `agents/orchestrator/pipeline_expand.go` (sub-node
  expansion).
- Delete protocol-runtime path in `agents/orchestrator/
  pipeline_runtime.go`.
- Update all callers to use claims-board APIs.
- Code path: ~3000 LOC removed; ~500 LOC modified.

**Acceptance criteria**:
- All deleted files gone.
- No imports remaining from deleted files.
- All tests still pass.
- Codebase grep returns zero references to removed types.
- Pre-conversion test suite passes against claims-based
  implementation.

**Unit tests**:
- `TestRetirement_NoProtocolImports` — No imports from removed files.
- `TestRetirement_NoOrphanReferences` — Codebase scan clean.

**Integration tests**:
- `TestRetirement_FullPipelineWithoutProtocol` — Pipeline runs end-
  to-end without protocol.
- `TestRetirement_OrchestratorDispatchesViaClaims` — DAG dispatches
  through claims.

**End-to-end tests**:
- `TestRetirementE2E_RealPipelineNoRegressions` — Real pipeline; no
  regressions.

**Race condition tests**: covered by upstream tests.

**Negative / non-happy path tests**:
- `TestRetirement_LegacyProtocolFile_NotPresent` — File absent.
- `TestRetirement_LegacyAPICall_BuildError` — Compile-time error.
- `TestRetirement_OldWALDataMigrated` — Existing pre-conversion
  WAL data migrated to claims WAL via §14.x migration tooling.

---

### Phase 3 — Architect Conversion

**What**: Architect generates precise, atomic claims with validations
instead of vague task descriptions.

**Phase implementation overview**: Phase 3 rewrites the architect's
plan output to produce structured claims rather than free-text task
prompts. The architect assembles; the inspector issues. Per §14.5.

#### 3.1 — TaskClaim types

**Description**: Add `TaskClaim` (with Relations, validations) to
`HandoffTask` and `AtomicTask`. Remove `AcceptanceCriteria` /
`SuccessCriteria` as separate fields — they become validations.

**Implementation approach**:
- Package: `agents/architect/types.go`.
- New `TaskClaim` struct: claim title + description + scope +
  validations.
- `HandoffTask.Claims []TaskClaim` replaces
  `AcceptanceCriteria` / `SuccessCriteria`.
- `AtomicTask.Claims []TaskClaim` similarly.
- Code path: ~600 LOC modified.

**Acceptance criteria**:
- New struct shape compiles.
- Old fields removed; no orphan references.
- TaskClaim round-trip JSON serialization.

**Unit tests**:
- `TestTaskClaim_RoundTripJSON`.
- `TestTaskClaim_ScopeRequired`.
- `TestTaskClaim_ValidationsRequired`.

**Integration tests**:
- `TestTaskClaim_HandoffSerialization` — Travels through handoff.

**End-to-end tests**: covered by 3.2-3.5.

**Race condition tests**: N/A.

**Negative / non-happy path tests**:
- `TestTaskClaim_MissingValidations_Rejected` — Required.
- `TestTaskClaim_OversizedScope_Bounded` — Bounded.

---

#### 3.2 — LLM prompt rewrite

**Description**: Instruct the LLM to produce precise, atomic claims
(not vague descriptions) with validations.

**Implementation approach**:
- Package: `agents/architect/planner_anthropic.go`.
- New system prompt fragment instructing the LLM:
  - Claims are atomic; one behavior per claim.
  - Each claim has 1-N validations with quality bars.
  - Claims have explicit scope.
  - Claims have explicit subject (Engineer / Designer / Tester).
- Code path: ~400 LOC modified.

**Acceptance criteria**:
- LLM produces structured claim output.
- Output parses into TaskClaim structs.
- Vague descriptions ("implement JWT middleware") become atomic
  ("implement HS256 JWK deserialization" with validations).
- Realistic prompt corpus produces correct structured output.

**Unit tests**:
- `TestPlannerPrompt_ParsesClaimOutput` — Structured parse.
- `TestPlannerPrompt_VaguePromptProducesAtomicClaims` — Quality.

**Integration tests**:
- `TestPlannerPrompt_LLMRoundTrip` — Real LLM test (mocked).

**End-to-end tests**:
- `TestPlannerPromptE2E_RealisticPlanning` — Real planning produces
  high-quality claims.

**Race condition tests**: N/A.

**Negative / non-happy path tests**:
- `TestPlannerPrompt_LLMReturnsMalformed_Reprompt` — Reprompt on
  bad output.
- `TestPlannerPrompt_LLMTimeout_FallbackToManual` — Timeout
  handled.
- `TestPlannerPrompt_VaguePromptResistsAtomization_FlaggedToUser` —
  Pathological prompt.

---

#### 3.3 — `toTask` claim generation

**Description**: `toTask()` converts claim payload to TaskClaim.
Owner normalization, ID generation, validation type inference.

**Implementation approach**:
- Package: `agents/architect/planner_anthropic.go`.
- Convert LLM output → `TaskClaim` structs.
- Generate stable IDs (UUIDv7).
- Normalize agent owners (e.g., `engineer` → resolved AgentID).
- Infer validation type (`unit_test`, `integration_test`, `inspection`,
  etc.) from quality bar wording.
- Code path: ~600 LOC modified.

**Acceptance criteria**:
- Claim payload → TaskClaim conversion correct.
- IDs stable across re-runs of same plan.
- Owner normalization respects agent registry.
- Validation type inference accurate (≥ 90% on test corpus).

**Unit tests**:
- `TestToTask_ClaimGeneration_RoundTrip`.
- `TestToTask_StableIDs`.
- `TestToTask_OwnerNormalization`.
- `TestToTask_ValidationTypeInference`.

**Integration tests**:
- `TestToTask_RealisticPlanProduction`.

**End-to-end tests**: covered by 3.5.

**Race condition tests**:
- `TestToTask_ConcurrentConversion` — `go test -race`.

**Negative / non-happy path tests**:
- `TestToTask_UnknownAgentOwner_Rejected` — Bad owner.
- `TestToTask_AmbiguousValidationType_FallsBackToInspection` —
  Fallback.
- `TestToTask_DuplicateClaimsInOnePlan_Deduplicated` — Dedup.

---

#### 3.4 — Handoff wiring

**Description**: `atomicTaskToHandoff()` and `buildPlanHandoff()` carry
claims through to the orchestrator. The handoff payload is the
authoritative source of architect-assembled claims.

**Implementation approach**:
- Package: `agents/architect/skills_planning.go`.
- `HandoffTask` struct adds `Claims []TaskClaim` field; older
  task-prompt fields retained during cutover, ignored after.
- `atomicTaskToHandoff()` extracts claims from `AtomicTask.Claims`
  and embeds them in the handoff.
- `buildPlanHandoff()` aggregates per-task claims into the plan-
  level handoff; preserves Relations between tasks (caused_by
  chains for sequential plan steps).
- Serialization: claims travel via existing handoff transport
  (currently `MergeDescriptor` for pipeline handoff; bus message
  for global handoff). Post-Phase 13: substrate subject for handoff
  metadata.
- Receiver (orchestrator): extracts claims in `handleTaskDispatch`
  (Phase 7.6); validates structure; passes to claims-board
  initialization.
- Round-trip determinism: handoff serialization is canonical (per
  CLUSTER.md §4.4-bis post-Phase 13); same input → same bytes.
- Code path: `agents/architect/skills_planning.go` (~600 LOC) +
  `agents/orchestrator/task_dispatch.go` adapter (~300 LOC).
- Hard part: backward compat during cutover when both old (task-
  prompt-based) and new (claims-based) handoffs flow. Solved by
  feature-flagged dual-path receivers; cutover after parity check.

**Acceptance criteria**:
- Claims survive serialization through handoff transport.
- Round-trip produces identical claim sets (deterministic).
- Orchestrator extracts claims correctly into board.
- No data loss in handoff round-trip.
- Inter-task Relations (`caused_by`, `depends_on`) preserved.
- Handoff size bounded; oversized rejected with actionable error.
- Backward compat: legacy task-prompt-only handoffs still work
  during cutover; flagged for migration.
- Authority: handoff origin (architect SVID) verified at receiver.

**Unit tests**:
- `TestHandoff_ClaimsSurvive` — Round-trip claims.
- `TestHandoff_NoDataLoss` — Bit-equal round-trip.
- `TestHandoff_RelationsPreserved` — Inter-task Relations preserved.
- `TestHandoff_DeterministicSerialization` — Same plan → same bytes.
- `TestHandoff_OrchestratorExtractsCorrectly` — Orchestrator gets
  claims.
- `TestHandoff_BackwardCompatLegacyAccepted` — Legacy works during
  cutover.
- `TestHandoff_AuthorityVerified` — Architect SVID checked.

**Integration tests**:
- `TestHandoff_RealHandoffRoundTrip` — Real handoff transport.
- `TestHandoff_PlanLevelHandoffMultipleTasks` — Multi-task plan.
- `TestHandoff_DualPathDuringCutover` — Both old + new paths.

**End-to-end tests**:
- `TestHandoffE2E_ArchitectToOrchestrator` — Full architect → orchestrator
  flow with claims.

**Race condition tests**:
- `TestHandoff_ConcurrentHandoffs` — `go test -race` parallel
  handoffs; isolated.
- `TestHandoff_DualPathFlagToggleRace` — Flag flip mid-handoff.

**Negative / non-happy path tests**:
- `TestHandoff_OversizedClaims_Rejected` — Bounded.
- `TestHandoff_MalformedClaim_RejectedWithError` — Malformed.
- `TestHandoff_MissingArchitectSVID_Rejected` — Authority.
- `TestHandoff_RoundTripCorruption_DetectedViaHash` — Corruption.
- `TestHandoff_TransportFailure_RetriedOrEscalated` — Transport.
- `TestHandoff_LegacyHandoffWithoutClaims_FlaggedForMigration` —
  Cutover flag.
- `TestHandoff_DuplicatePlanID_Idempotent` — Idempotent.

---

#### 3.5 — Plan as action

**Description**: Plan handoff becomes an action; tasks become claims
within that action; architect assembles, inspector issues.

**Implementation approach**:
- Package: `agents/architect/skills_planning.go`.
- Plan handoff packaged as `Action` with `Type=Task`.
- Inspector formally issues claims when board is populated.
- Code path: ~500 LOC modified.

**Acceptance criteria**:
- Plan = Action with claim set.
- Inspector is the issuer when claims hit the board.
- Architect's authorship preserved via Relation (`assembled_by`).

**Unit tests**:
- `TestPlan_AsAction`.
- `TestPlan_InspectorIsIssuer`.
- `TestPlan_ArchitectAssemblerRelation`.

**Integration tests**:
- `TestPlan_FullArchitectToInspector`.

**End-to-end tests**:
- `TestPlanE2E_RealPlanning` — Architect produces plan; inspector
  issues; engineer/designer/tester see claims.

**Race condition tests**:
- `TestPlan_ConcurrentPlanGeneration` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPlan_EmptyClaimsRejected` — Plan with zero claims rejected.
- `TestPlan_AllClaimsAcceptedTriggersComplete` — Auto-completion.

---

### Phase 4 — Guide Conversion

**What**: Guide preserves intent classification + direct-comm protocol;
session gains claims board as persistent conversational state. Per
§14.6.

**Phase implementation overview**: Phase 4 fixes the conversation-
history-loss bug permanently by moving context from `ConversationHistory`
on `ForwardedRequest` to the session claims board. The Guide still
routes requests; the board carries context.

#### 4.1 — User prompt as action on session board

**Description**: Every user input becomes a prompt action posted to
session board before routing.

**Implementation approach**:
- Package: `agents/guide/session_routing.go`.
- On user prompt receipt: post `Action` with `Type=UserPrompt` to
  session board; then forward to target agent.
- Target agent receives forwarded request + can read session board
  for full context.
- Code path: ~600 LOC modified.

**Acceptance criteria**:
- Every prompt becomes an action.
- Target agent can read session board.
- Routing latency unchanged.

**Unit tests**:
- `TestPromptAction_PostedBeforeRouting`.
- `TestPromptAction_TargetReceivesBoardRef`.

**Integration tests**:
- `TestPromptAction_RealRoutingFlow`.

**End-to-end tests**:
- `TestPromptActionE2E_AgentSeesBoard`.

**Race condition tests**:
- `TestPromptAction_ConcurrentPrompts` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPromptAction_BoardUnavailable_DegradedToHistoryFallback` —
  Backward compat.
- `TestPromptAction_OversizedPrompt_Rejected` — Bounded.

---

#### 4.2 — Classification as testament

**Description**: Guide's classifier produces testament with
classification artifacts (intent, confidence, target agent, rationale).

**Implementation approach**:
- Package: `agents/guide/classification.go`.
- After classification, submit testament against the prompt action's
  claim with `kind=intent_classification` artifact.
- Subsequent agent work walks Relations from work → claim →
  testament → classification rationale.
- Code path: ~400 LOC modified.

**Acceptance criteria**:
- Classification produces testament.
- Artifact carries intent + confidence + target + rationale.
- Audit trail traceable.

**Unit tests**:
- `TestClassification_TestamentSubmitted`.
- `TestClassification_ArtifactStructure`.
- `TestClassification_AuditTraceable`.

**Integration tests**:
- `TestClassification_ReroutingScenario` — Re-classification via new
  testament.

**End-to-end tests**:
- `TestClassificationE2E_RealClassification`.

**Race condition tests**: N/A.

**Negative / non-happy path tests**:
- `TestClassification_LowConfidenceFlag` — Low confidence flagged.
- `TestClassification_FailedClassification_ErrorArtifact` — Error.

---

#### 4.3 — Context on the board, not in transit

**Description**: `ConversationHistory` becomes hint, not source of
truth.

**Implementation approach**:
- Package: `agents/guide/`.
- Mark `ConversationHistory` as best-effort.
- Agents read board first; fall back to history only if board
  unavailable.
- Backward-compat: keep history populated during cutover.
- Code path: ~800 LOC modified.

**Acceptance criteria**:
- Agents prefer board over history.
- History deprecation is gradual; can be disabled per cluster
  config.
- The conversation-history-loss bug is fixed (verified by
  regression test).

**Unit tests**:
- `TestContext_BoardPreferred`.
- `TestContext_HistoryFallback`.
- `TestContext_HistoryLossBugFixed`.

**Integration tests**:
- `TestContext_RealisticConversation` — Multi-turn; context preserved.

**End-to-end tests**:
- `TestContextE2E_LongConversation`.

**Race condition tests**:
- `TestContext_ConcurrentSessionAccess` — `go test -race`.

**Negative / non-happy path tests**:
- `TestContext_BoardCorrupted_HistoryFallback` — Resilient.
- `TestContext_HistoryAndBoardDisagree_BoardWins` — Board canonical.

---

#### 4.4 — Direct address preserved

**Description**: `@architect` style addressing still routes via direct-
comm protocol; additionally adds `direct_addressed` Relation.

**Implementation approach**:
- Package: `agents/guide/direct_address.go`.
- Detection unchanged.
- Add `direct_addressed` Relation to claim.
- Code path: ~200 LOC modified.

**Acceptance criteria**:
- Direct address detection unchanged.
- Relation added.
- Auditable.

**Unit tests**:
- `TestDirectAddress_DetectionUnchanged`.
- `TestDirectAddress_RelationAdded`.

**Integration tests**:
- `TestDirectAddress_FullFlow`.

**End-to-end tests**: covered by 4.1.

**Race condition tests**: N/A.

**Negative / non-happy path tests**:
- `TestDirectAddress_UnknownAgent_FallbackToClassification` —
  Fallback.

---

#### 4.5 — Session-scoped board

**Description**: Each session gets a root claims board.

**Implementation approach**:
- Package: `agents/guide/`, `core/session/`.
- New `SessionBoard` ID per session.
- Boards persist to disk per session (Phase 0.3 WAL).
- Pipeline boards (Phase 2) become children of session board.
- Code path: ~1000 LOC modified.

**Acceptance criteria**:
- Each session has unique board.
- Pipeline boards parent-child.
- Recovery restores full session state.

**Unit tests**:
- `TestSessionBoard_PerSession`.
- `TestSessionBoard_PipelineParentChild`.
- `TestSessionBoard_RecoveryRestores`.

**Integration tests**:
- `TestSessionBoard_MultipleSessionsConcurrent`.

**End-to-end tests**:
- `TestSessionBoardE2E_RealSession`.

**Race condition tests**:
- `TestSessionBoard_ConcurrentSessions` — `go test -race`.

**Negative / non-happy path tests**:
- `TestSessionBoard_PerSessionDiskFull_BackpressureOnly` —
  Per-session resource isolation.
- `TestSessionBoard_OrphanedSession_GarbageCollected` — GC.

---

#### 4.6 — ForwardedRequest carries board reference

**Description**: `ForwardedRequest.Metadata` gains `session_board_id`.

**Implementation approach**:
- Package: `agents/guide/`.
- Add metadata key.
- Target agent reads board via key.
- No structural change to `ForwardedRequest`.
- Code path: ~200 LOC modified.

**Acceptance criteria**: forward-compat; target reads board.

**Unit tests**:
- `TestForwardedRequest_MetadataKeyPresent`.
- `TestForwardedRequest_TargetReadsBoard`.

**Integration tests**: covered by 4.1.

**Negative / non-happy path tests**:
- `TestForwardedRequest_MissingBoardID_FallbackToHistory` —
  Backward-compat.

---

### Phase 5 — Knowledge Agent Conversion

Librarian, Academic, Archivalist become claims participants. Per §14.7.

**Phase implementation overview**: Phase 5 retires the standalone
`consult` skill and reframes knowledge interactions as actions.
Consultation requests are actions of `ActionType=Consultation`;
responses are testaments with knowledge artifacts. Knowledge agents
also become *proactive*: they observe scopes via Fabric (or Phase 13
substrate) and post anticipatory consultation testaments preemptively.
Common pattern across all three knowledge agents: retire the
standalone `consult` skill, register the 5 claims skills, accept
consultation actions, respond with testaments. The differences are in
the *artifact kinds* each agent produces.

#### 5.1 — Librarian

**Description**: Per §14.7 #5a. Librarian retires standalone `consult`
skill, registers claims skills, accepts consultation actions, responds
with testaments containing `reference_links` and `code_reference`
artifacts. Proactive claims when work in known scope.

**Implementation approach**:
- Package: `agents/librarian/`.
- Retire `consult` skill registration; remove from
  `LibrarianSkillNames`.
- Register the 5 claims skills (`query_claims_board`, `post_action`,
  `submit_testaments`, `update_claim_progress`,
  `inspect_claim_conflicts`).
- Subscription: librarian subscribes to claims subjects matching its
  scope domains (file paths, packages, languages); on relevant claims
  posted, librarian inspects + posts proactive consultation
  testament.
- Knowledge testaments include: `reference_links` (config files,
  doc pages), `code_reference` (file:line citations from existing
  codebase), `kind=knowledge_summary` for summaries.
- Code path: `agents/librarian/` (~1500 LOC modified).
- Hard part: avoiding pathological proactive claim flooding. Solved
  by per-scope rate limiting + relevance-score threshold; only
  publish when score > threshold.

**Acceptance criteria**:
- `consult` skill removed; not in `LibrarianSkillNames`.
- All 5 claims skills registered.
- Consultation actions of `ActionType=Consultation` accepted; subject
  agent + scope honored.
- Testaments include at least one `reference_links` or
  `code_reference` artifact.
- Proactive claims fire when scope match score > threshold.
- Per-scope rate limit prevents flooding (max N proactive claims
  per scope per minute).
- Existing librarian tests pass against claims-based path.
- Authority: librarian only responds to consultation actions
  authorized for its agent kind.

**Unit tests**:
- `TestLibrarian_ConsultRetired` — Skill not in names.
- `TestLibrarian_ClaimsSkillsRegistered` — All 5 present.
- `TestLibrarian_ConsultationHandled` — Action accepted, testament
  produced.
- `TestLibrarian_ReferenceLinksArtifact` — Artifact shape.
- `TestLibrarian_CodeReferenceArtifact` — Code-ref artifact.
- `TestLibrarian_ProactiveClaimOnScopeMatch` — Triggered.
- `TestLibrarian_ProactiveRateLimitEnforced` — Bounded.
- `TestLibrarian_RelevanceThresholdRespected` — Below threshold no
  proactive.
- `TestLibrarian_AuthorityCheck` — Unauthorized refused.

**Integration tests**:
- `TestLibrarian_KnowledgeWorkflowFull` — End-to-end consultation.
- `TestLibrarian_ProactiveDuringActiveImplementation` — Real-time
  proactive.
- `TestLibrarian_ExistingTestsPort` — Pre-conversion suite passes.

**End-to-end tests**:
- `TestLibrarianE2E_RealKnowledgeRequest` — Real consultation.
- `TestLibrarianE2E_ProactiveWorkflow` — Proactive insights flow
  to engineer.

**Race condition tests**:
- `TestLibrarian_ConcurrentConsultations` — `go test -race`.
- `TestLibrarian_ProactiveDispatchRace` — Race.
- `TestLibrarian_RateLimiterRace` — Counter consistency.

**Negative / non-happy path tests**:
- `TestLibrarian_NoMatchingKnowledge_EmptyTestament` — Graceful.
- `TestLibrarian_KnowledgeStoreUnavailable_ErrorArtifact` —
  Captured.
- `TestLibrarian_LegacyConsultCallerErrors_DeprecationDuringCutover`
  — Backward-compat.
- `TestLibrarian_OversizedConsultation_Rejected` — Bounded.
- `TestLibrarian_RelevanceScorePathological_FallsBackToTopK` —
  Pathological.
- `TestLibrarian_KnowledgeStaleByThreshold_FlaggedInTestament` —
  Stale flag.
- `TestLibrarian_ScopeMismatch_RefusesConsultation` — Out-of-scope.

---

#### 5.2 — Academic

**Description**: Per §14.7 #5b. Academic responds to research claims
with testaments containing `research_paper`, `reference_links`,
`knowledge_graph_vectors` artifacts. Recommendations carry validations
that Librarian evaluates.

**Implementation approach**:
- Package: `agents/academic/`.
- Retire standalone academic skills if any; register 5 claims
  skills.
- Research request = claim, e.g., "Research best practices for HS256
  JWK implementation"; subject = academic.
- Academic queries external research sources, knowledge graph,
  vector DB; submits testament with:
  - `research_paper`: full paper / blog / RFC reference.
  - `reference_links`: URLs.
  - `knowledge_graph_vectors`: embedding IDs for retrieved sources.
  - `kind=recommendation`: structured recommendations with
    citations.
- Validation `recommendation_aligns_with_codebase`: Librarian
  evaluates by checking recommendations against codebase patterns
  (cross-agent validation).
- Quality gate: Academic submits with `confidence: tentative` until
  Librarian validates → upgraded to `committed`.
- Code path: `agents/academic/` (~1200 LOC modified).

**Acceptance criteria**:
- Research as claim; testament structured with multiple artifact
  kinds.
- Librarian validates recommendation alignment.
- Confidence upgrade on Librarian validation.
- KG vectors stored alongside research.
- Sources cited for every recommendation.
- Authority: academic only responds to authorized research claims.

**Unit tests**:
- `TestAcademic_ResearchClaim` — Claim shape.
- `TestAcademic_ResearchArtifacts` — All artifact kinds.
- `TestAcademic_RecommendationKind` — Recommendation artifact.
- `TestAcademic_LibrarianValidation` — Cross-agent validation type.
- `TestAcademic_ConfidenceUpgradeOnValidation` — Tentative →
  committed.
- `TestAcademic_KGVectorsAttached` — Vectors stored.
- `TestAcademic_SourceCitations` — Every recommendation cited.

**Integration tests**:
- `TestAcademic_FullResearchFlow` — End-to-end research.
- `TestAcademic_LibrarianValidationCycle` — Validation roundtrip.
- `TestAcademic_KGIntegration` — Real KG.

**End-to-end tests**:
- `TestAcademicE2E_RealResearch` — Real research request.
- `TestAcademicE2E_RecommendationAdoptedByEngineer` — Realistic
  flow.

**Race condition tests**:
- `TestAcademic_ConcurrentResearch` — `go test -race`.
- `TestAcademic_ValidationRaceWithUpgrade` — Race.

**Negative / non-happy path tests**:
- `TestAcademic_LowQualityResearch_FlaggedByLibrarian` — Validation
  fails; recommendation flagged.
- `TestAcademic_NoSourcesFound_ErrorArtifact` — No results.
- `TestAcademic_ExternalSourceTimeout_PartialArtifacts` — Partial.
- `TestAcademic_UnverifiableRecommendation_RemainsTentative` —
  Stays tentative.
- `TestAcademic_HallucinationDetected_RecommendationRejected` —
  Hallucination caught by Librarian.
- `TestAcademic_DuplicateResearchAcrossSessions_Cached` — Cached
  via KG.
- `TestAcademic_DeprecatedRecommendationFlagged` — Stale source.

---

#### 5.3 — Archivalist

**Description**: Per §14.7 #5c. Ingestion is a claim; archivalist
responds with testament containing `ingestion_response` artifacts
(document DB IDs, KG vector IDs, entry IDs). Memory retrieval is
also a claim; testament has `document_db_snippet` and
`knowledge_graph_vectors` artifacts.

**Implementation approach**:
- Package: `agents/archivalist/`.
- Two claim shapes:
  - **Ingest**: claim "Ingest <content>" with `subject=archivalist`;
    testament with `ingestion_response` (DB ID, KG vector ID, entry
    ID), confirmation status.
  - **Retrieve**: claim "Retrieve prior <topic>" with
    `subject=archivalist`; testament with `document_db_snippet`,
    `knowledge_graph_vectors`, `relevance_scores` artifacts.
- Subscribe to scope-relevant claims for proactive memory surfacing
  (similar to Librarian).
- Validation `ingestion_durable`: KG + DocDB both confirmed.
- Code path: `agents/archivalist/` (~1500 LOC modified).

**Acceptance criteria**:
- Ingest + Retrieve claim shapes supported.
- Testaments structured with appropriate artifacts.
- `ingestion_durable` validation requires both KG and DocDB
  confirmation.
- Proactive memory surfacing on scope match.
- Per-tenant DP budget for retrieval (privacy if applicable).
- Existing archivalist tests pass.

**Unit tests**:
- `TestArchivalist_IngestionClaim` — Ingest claim shape.
- `TestArchivalist_IngestionArtifacts` — All artifacts.
- `TestArchivalist_RetrievalClaim` — Retrieve claim shape.
- `TestArchivalist_RetrievalArtifacts` — All artifacts.
- `TestArchivalist_DurabilityValidation` — Both stores confirmed.
- `TestArchivalist_RelevanceScoresAttached` — Scores included.
- `TestArchivalist_ProactiveMemorySurfacing` — Triggered.
- `TestArchivalist_PerTenantDPBudget` — Budget enforced.

**Integration tests**:
- `TestArchivalist_FullIngestionRetrievalFlow` — End-to-end.
- `TestArchivalist_DurabilityAcrossBothStores` — KG + DocDB.
- `TestArchivalist_ProactiveDuringActiveScope` — Proactive flow.

**End-to-end tests**:
- `TestArchivalistE2E_RealIngestion` — Real session content.
- `TestArchivalistE2E_LongTermMemoryRetrieval` — Cross-session.

**Race condition tests**:
- `TestArchivalist_ConcurrentIngestion` — `go test -race`.
- `TestArchivalist_RetrievalRaceWithIngestion` — Race.
- `TestArchivalist_ProactiveDispatchRace` — Race.

**Negative / non-happy path tests**:
- `TestArchivalist_DocDBUnavailable_ErrorArtifact` — Captured.
- `TestArchivalist_KGUnavailable_PartialArtifacts` — Partial.
- `TestArchivalist_BothStoresFail_DurabilityValidationFails` —
  Fails.
- `TestArchivalist_OversizedIngestion_Rejected` — Bounded.
- `TestArchivalist_DuplicateIngestion_Idempotent` — Idempotent.
- `TestArchivalist_RetrievalNoMatch_EmptyTestament` — Graceful.
- `TestArchivalist_RetrievalDPBudgetExceeded_Rejected` — Budget.
- `TestArchivalist_StaleIndexFlaggedInTestament` — Stale flag.

---

### Phase 6 — Infrastructure Agent Conversion

Scribe + Guardian. Per §14.8.

**Phase implementation overview**: Phase 6 makes scribe narration and
guardian processes flow *through* the claims board. Both are deeper
conversions than Phase 5 because every operation produces structured
claim/testament/artifact records.

#### 6.1 — Scribe

**Implementation approach** (per §14.8 #6a):
- Narration-as-testament: `store_archivalist` also submits testament
  with `narration` + `archivalist_receipt` artifacts.
- Precedent detection as validation: `precedent_worthy` flag becomes
  validation with type `precedent`.
- Handoff as claims: handoff trigger posts claim; new agent submits
  testament.
- Board preamble in tool loop.
- Full claims-skill registration.
- GoroutineScope for all goroutines.
- Code path: ~2500 LOC modified.

**Acceptance criteria**:
- Narration submits testament.
- Precedent flag becomes validation.
- Handoff is a claim.
- Board state visible in scribe LLM context.
- All async paths goroutine-tracked.

**Unit tests**:
- `TestScribe_NarrationAsTestament`.
- `TestScribe_PrecedentValidation`.
- `TestScribe_HandoffClaim`.
- `TestScribe_BoardPreamble`.
- `TestScribe_GoroutineScopeTracked`.

**Integration tests**:
- `TestScribe_FullNarrationCycle`.
- `TestScribe_HandoffIntegration`.

**End-to-end tests**:
- `TestScribeE2E_RealSession`.

**Race condition tests**:
- `TestScribe_ConcurrentNarrations` — `go test -race`.
- `TestScribe_HandoffRaceWithNarration` — Race.

**Negative / non-happy path tests**:
- `TestScribe_LLMTimeoutDuringNarration_ErrorArtifact`.
- `TestScribe_ArchivalistFailsIngestion_TestamentReflectsError`.
- `TestScribe_HandoffPartialState_RecoverableViaClaim` — Recovery.

---

#### 6.2 — Guardian

**Implementation approach** (per §14.8 #6b):
- Every guardian process (command approval, content scanning, git
  gating, plan approval, tool grants, diff review, checkpoints,
  health monitoring, conversation, context budget, rollback) → uniform
  Action → Claim → Testament structure as detailed in §14.8.
- Each process described in detail with its claim shape, validations,
  testament artifacts.
- Code path: ~3500 LOC modified.

**Acceptance criteria**:
- All listed guardian processes claim-based.
- Validation/testament artifact structure per §14.8 specs.
- Guardian's full audit trail accessible via claims queries.
- All async paths goroutine-tracked.

**Unit tests**:
- `TestGuardian_CommandApprovalClaim`.
- `TestGuardian_FetchApprovalClaim`.
- `TestGuardian_PlanApprovalClaim`.
- `TestGuardian_ToolExecutionClaim`.
- `TestGuardian_ContentScanClaim`.
- `TestGuardian_GitGatingClaim`.
- `TestGuardian_CheckpointClaim`.
- `TestGuardian_HealthMonitoringClaim`.
- `TestGuardian_DiffReviewClaim`.
- `TestGuardian_RollbackClaim`.
- `TestGuardian_ConversationTestament`.
- `TestGuardian_ContextBudgetClaim`.
- `TestGuardian_GoroutineScopeTracked`.

**Integration tests**:
- `TestGuardian_FullProcessLifecycles` — Each process E2E.

**End-to-end tests**:
- `TestGuardianE2E_RealSessionWithAllProcesses`.

**Race condition tests**:
- `TestGuardian_ConcurrentApprovals` — `go test -race`.
- `TestGuardian_HealthCheckRaceWithMutation` — Race.

**Negative / non-happy path tests**:
- `TestGuardian_DenialProducesCorrectiveAction` — Per §14.8.
- `TestGuardian_GuardianCrashedDuringApproval_RecoveredViaClaims` —
  Recovery.
- `TestGuardian_BadCommandPattern_RejectedWithReasonArtifact`.
- `TestGuardian_DomainReputationLow_FetchRejected`.
- `TestGuardian_ProtectedBranchPushNoApproval_Refused`.
- `TestGuardian_DirtyFileThresholdReached_CheckpointSuggested`.
- `TestGuardian_AnomalousAgentDetected_ClaimEmitted`.
- `TestGuardian_CredentialFoundInDiff_BlocksCommit`.
- `TestGuardian_RollbackPartialFailure_StateConsistent`.
- `TestGuardian_TokenBudgetExceeded_HandoffClaim`.

---

### Phase 7 — Orchestrator Conversion

Per §14.9. DAG nodes become actions; orchestrator stops mediating.

**Phase implementation overview**: Phase 7 retires the orchestrator's
intermediation role. The orchestrator stops dispatching pipelines via
the protocol handshake, stops carrying conversation context, stops
tracking pending checkpoint reviews. It manages DAG execution by
*observing* claims boards rather than driving them. Common pattern
across all sub-items: replace orchestrator-as-mediator with
orchestrator-as-observer. The orchestrator subscribes to claims-board
events; it doesn't dispatch protocol handoffs.

#### 7.1 — DAG nodes as actions

**Description**: Per §14.9. Each DAG node dispatch creates an action
on the pipeline's claims board; the node's task prompt becomes claims;
node completion = board `MarkComplete`.

**Implementation approach**:
- Package: `agents/orchestrator/dag_bridge.go`.
- DAG node → action: when DAG executor reaches a node, create an
  `Action` on the target pipeline's board with `ActionType=Task`,
  containing the node's claims (architect-assembled).
- Subscribe to board events: orchestrator subscribes to `MarkComplete`
  events on the target board; on completion, advances the DAG.
- Node-pipeline mapping persisted in DAG-bridge state for restart
  recovery.
- Code path: `agents/orchestrator/dag_bridge.go` (~1200 LOC modified).
- Hard part: ensuring DAG state and board state stay consistent
  across orchestrator restarts. Solved by treating board state as
  canonical; DAG bridge restart re-derives node status from board
  queries.

**Acceptance criteria**:
- DAG node dispatch creates action on target board.
- Node-claims mapping bidirectional and persisted.
- DAG advances automatically on board `MarkComplete`.
- DAG state recoverable from board state alone after orchestrator
  restart.
- No protocol handshake involved.
- Multiple DAG nodes can share one board (parallel claims) or use
  separate boards (serialized phases) per DAG declaration.
- Node failure (board rejection) bubbles up as DAG node failure.
- Integration with §22.1 quotas: per-tenant DAG dispatch rate
  bounded.

**Unit tests**:
- `TestDAG_NodeAsAction` — Node dispatch produces correct action shape.
- `TestDAG_NodeClaimsAttached` — Architect's claims attached to action.
- `TestDAG_CompletionMarksBoardComplete` — `MarkComplete` triggers DAG
  advance.
- `TestDAG_NodePipelineMappingPersisted` — Mapping survives orchestrator
  restart.
- `TestDAG_BoardCanonicalAfterRestart` — DAG re-derived from board.
- `TestDAG_ParallelNodesShareBoard` — Multi-node board mode.
- `TestDAG_SerializedNodesSeparateBoards` — Serial board mode.
- `TestDAG_NodeFailurePropagates` — Board reject → DAG failure.

**Integration tests**:
- `TestDAG_FullPipelineDispatch` — End-to-end DAG node → board →
  agents → completion → DAG advance.
- `TestDAG_RestartRecovery` — Kill orchestrator mid-DAG; restart;
  resume from board state.
- `TestDAG_ParallelDispatch` — Multiple nodes dispatch concurrently.

**End-to-end tests**:
- `TestDAGE2E_RealDAGExecution` — Real multi-node DAG; full lifecycle.
- `TestDAGE2E_OrchestratorRestartMidDAG` — Orchestrator restart during
  DAG execution; correctness preserved.

**Race condition tests**:
- `TestDAG_ConcurrentNodeDispatches` — `go test -race` parallel
  dispatch; correct serialization.
- `TestDAG_BoardCompletionRaceWithDispatch` — Completion event race
  with new dispatch; deterministic resolution.
- `TestDAG_RestartDuringActiveCompletion` — Restart mid-completion;
  no double-advance.
- `TestDAG_SubscriptionDispatchRace` — Subscriber race with mutation;
  consistent.

**Negative / non-happy path tests**:
- `TestDAG_NodeFailureProducesRejectedClaim` — Failure → rejection
  with diagnostic artifact.
- `TestDAG_OrphanedNode_GCAfterTimeout` — Node without board reaped
  after timeout.
- `TestDAG_BoardCorrupted_NodeFailsClearly` — Corrupted board; node
  fails with specific error; not silent.
- `TestDAG_DispatchOverQuota_BackpressureToCaller` — §22.1 quota hit;
  DAG node queued.
- `TestDAG_NodeCompletionLost_RecoveredViaBoardQuery` — Completion
  event lost; orchestrator queries board directly; advances.
- `TestDAG_DAGCycleDetection` — Cycle in DAG refused at submit.
- `TestDAG_NodeWithNoClaimsRefused` — Empty-claims node refused at
  dispatch.

---

#### 7.2 — Pipeline dispatch mediation removed

**Description**: Per §14.9. Remove orchestrator's intermediation
between pipeline inspector and global inspector. Pipeline inspector
routes directly.

**Implementation approach**:
- Package: `agents/orchestrator/pipeline_runtime.go`.
- Remove functions: `routeProtocolPipelineTask`,
  `publishOTGlobalFollowupRequest`, `recordPendingCheckpointReview`.
- Pipeline inspector publishes to a substrate subject the global
  inspector subscribes to (via §11.4 substrate consumer pattern, or
  pre-substrate via existing bus).
- Direct routing: pipeline inspector knows global inspector's address
  via discovery, skips the orchestrator hop.
- Code path: `agents/orchestrator/pipeline_runtime.go` (~1200 LOC
  removed); pipeline inspector + global inspector get ~300 LOC each
  for direct routing.
- Hard part: backward compat during cutover. Solved by feature flag
  routing both old (via orchestrator) and new (direct) paths in
  parallel; cutover after parity verification.

**Acceptance criteria**:
- 3 functions removed.
- Pipeline inspector → global inspector routing direct.
- No orchestrator code on this path.
- Latency reduction measurable (one fewer hop).
- Backward-compat cutover gated by feature flag + parity check.
- All existing tests still pass against direct path.

**Unit tests**:
- `TestOrchestrator_NoMediationCode` — Functions removed.
- `TestOrchestrator_NoOrphanReferences` — Codebase scan clean.
- `TestPipelineInspector_DirectRouting` — Pipeline inspector publishes
  directly.
- `TestGlobalInspector_DirectSubscription` — Global inspector receives.
- `TestRouting_CutoverFlag` — Flag toggles old/new path.

**Integration tests**:
- `TestOrchestrator_DirectInspectorRouting` — Direct path end-to-end.
- `TestRouting_LatencyImproved` — Measurable improvement vs old path.
- `TestRouting_BackwardCompatViaFlag` — Old path still works during
  cutover.

**End-to-end tests**: covered by 7.1.
- `TestRoutingE2E_FullPipelineToGlobalReview` — Real pipeline; direct
  routing throughout.

**Race condition tests**:
- `TestRouting_ConcurrentInspectorPublishes` — `go test -race`.
- `TestRouting_FlagToggleDuringActiveDispatch` — Flag flip mid-flight;
  in-flight either uses old or new path consistently.

**Negative / non-happy path tests**:
- `TestOrchestrator_LegacyMediationCall_NotPresent` — Removed
  function not callable.
- `TestRouting_GlobalInspectorUnreachable_BoundedRetry` — Bounded
  retry; eventual error.
- `TestRouting_ParityMismatchBlocksCutover` — Mismatch in parity
  check halts cutover.
- `TestRouting_RouteFailureDuringHandoff_BoardStateConsistent` —
  Even if direct route fails, board state remains consistent.

---

#### 7.3 — Checkpoint review tracking removed

**Description**: Per §14.9. Delete `pendingCheckpointReview`,
`HandleCheckpointReviewTerminal`, `completePendingCheckpointReview`,
`failPendingCheckpointReview`. DAG bridge subscribes to bus events.

**Implementation approach**:
- Package: `agents/orchestrator/checkpoint_review.go`.
- Delete state struct and 3 handlers.
- DAG bridge subscribes to `global_review.complete` bus event (or
  substrate equivalent post-Phase 13).
- Bus event delivery: at-least-once with §6 dedupe; idempotent
  apply.
- Code path: ~600 LOC removed; ~200 LOC added (subscription +
  idempotency wrapper).

**Acceptance criteria**:
- State and handlers removed.
- Bus subscription works.
- Event delivery at-least-once with idempotent apply.
- DAG advance triggered by bus event.
- Dropped events recoverable via board query.

**Unit tests**:
- `TestCheckpointReview_NoTrackingState` — State removed.
- `TestCheckpointReview_BusSubscription` — Subscription registered.
- `TestCheckpointReview_IdempotentApply` — Same event twice = no-op.
- `TestCheckpointReview_DAGAdvanceOnEvent` — Event → advance.

**Integration tests**:
- `TestCheckpointReview_GlobalReviewCompletion` — Full event flow.
- `TestCheckpointReview_AtLeastOnceDelivery` — Duplicate events
  handled.

**End-to-end tests**:
- `TestCheckpointReviewE2E_RealReview` — Full review cycle via bus.

**Race condition tests**:
- `TestCheckpointReview_ConcurrentEventDelivery` — `go test -race`.
- `TestCheckpointReview_ApplyDuringSubscriptionInit` — Race resolved.

**Negative / non-happy path tests**:
- `TestCheckpointReview_BusEventLost_ReconciliationViaClaims` —
  Lost event; DAG queries board directly; advances.
- `TestCheckpointReview_BusUnavailable_DAGStallsBounded` — Bus down;
  bounded stall; alert.
- `TestCheckpointReview_MalformedEventDropped_LoggedNotPanic` —
  Bad event logged + dropped.
- `TestCheckpointReview_OrphanedReview_GCAfterRetention` — Orphan
  reviews reaped.

---

#### 7.4 — Health monitoring via claims

**Description**: Per §14.9. Periodic claims "Report health status"
against each agent; agents respond with health testaments.

**Implementation approach**:
- Package: `agents/orchestrator/health_monitor.go`.
- Periodic dispatcher (configurable interval, default 30s) issues
  health-check claims against active agents.
- Health claim validations: "Agent responsive within 3s",
  "Token usage below 80% of budget", "No active corrective claims".
- Agents respond with testaments: `kind=health_snapshot` artifact
  with token usage, queue depth, last-activity timestamp.
- Health failures (timeout, validation rejection) trigger handoff
  claims (Phase 9.1).
- Code path: ~600 LOC.

**Acceptance criteria**:
- Periodic dispatch every 30s (configurable).
- Each active agent receives health claim.
- Validations cover: responsiveness, budget, no-stuck.
- Testament artifacts structured.
- Failures trigger handoff via §9.1.
- Health stats observable via §1.2 ClaimsBoardDigest.

**Unit tests**:
- `TestHealth_PeriodicDispatch` — Dispatch fires at interval.
- `TestHealth_ClaimDispatch` — Claim issued per agent.
- `TestHealth_ValidationsCorrect` — All 3 validations.
- `TestHealth_TestamentSubmitted` — Agent responds.
- `TestHealth_ResponsivenessValidation` — Timeout → fail validation.
- `TestHealth_BudgetValidation` — Over-budget → fail validation.

**Integration tests**:
- `TestHealth_AgentResponsiveness` — Real responsiveness check.
- `TestHealth_FailureTriggersHandoff` — Failed health → handoff
  claim posted.

**End-to-end tests**:
- `TestHealthE2E_RealMonitoring` — Live cluster; health monitored.

**Race condition tests**:
- `TestHealth_ConcurrentHealthClaims` — `go test -race`.
- `TestHealth_DispatchRaceWithAgentDeath` — Agent dies during
  dispatch; cleaned up.

**Negative / non-happy path tests**:
- `TestHealth_UnresponsiveAgent_TimeoutClaim` — Timeout claim posted.
- `TestHealth_AgentRefusesHealthClaim_LoggedAndEscalated` —
  Pathological agent; logged.
- `TestHealth_OrchestratorBackpressure_DispatchSkipped` —
  Backpressure; cycle skipped; alert.
- `TestHealth_HealthStorm_RateLimited` — Pathological response
  storm; rate-limited.

---

#### 7.5 — Coordination via claims

**Description**: Per §14.9. Coordination service (scope claims,
artifacts, reviews) becomes part of the claims board. Scope claims
are claims; artifact publishing is testament; review requests are
consultation actions.

**Implementation approach**:
- Package: `agents/orchestrator/`, `core/claims/`.
- Scope claims: `ActionType=Task`, claim with
  `Validation.Type=scope_grant`; subject = scope owner; testament
  acknowledges grant.
- Artifact publishing: agents submit testaments with artifact kinds
  (already covered by Phase 0.5 skills).
- Review requests: `ActionType=Consultation`; reviewer responds with
  testament containing review verdict + artifacts.
- Retire `core/coordination/` package; route existing callers to
  claims-board APIs.
- Code path: `core/claims/coordination_helpers.go` (~600 LOC) +
  `core/coordination/` removal (~1500 LOC).

**Acceptance criteria**:
- Scope claims supported with `scope_grant` validation type.
- Conflict detection: overlapping scopes detected at claim issue.
- Artifacts via testaments; same as Phase 0.5.
- Reviews as consultation actions.
- `core/coordination/` package removed.
- Existing coordination tests pass against claims-based.

**Unit tests**:
- `TestCoordination_ScopeClaims` — Scope claim validates.
- `TestCoordination_ScopeGrantValidationType` — Type recognized.
- `TestCoordination_ScopeConflictDetected` — Overlap caught.
- `TestCoordination_ArtifactsAsTestaments` — Artifacts via testament.
- `TestCoordination_ReviewActions` — Consultation = review.
- `TestCoordination_PackageRemoved` — `core/coordination/` gone.

**Integration tests**:
- `TestCoordination_RealisticContention` — Multiple agents contending
  for scope.
- `TestCoordination_ReviewFullFlow` — Request → review → verdict.
- `TestCoordination_ExistingTestsPort` — Existing test suite passes.

**End-to-end tests**:
- `TestCoordinationE2E_PipelinePeerCoordination` — Real pipeline
  with peer scope coordination.

**Race condition tests**:
- `TestCoordination_ConcurrentScopeAcquisitions` — `go test -race`.
- `TestCoordination_ConflictResolutionAtomic` — Atomic resolution.
- `TestCoordination_ReviewRequestRaceWithAcceptance` — Race resolved.

**Negative / non-happy path tests**:
- `TestCoordination_DoubleScopeAcquisition_Conflict` — Conflict
  detected; one wins.
- `TestCoordination_ScopeReleaseWithoutAcquisition_Refused` —
  Cannot release un-held scope.
- `TestCoordination_OrphanedScopeClaim_TimeoutRelease` — Scope
  released on agent death.
- `TestCoordination_ReviewOverdue_EscalatesToInspector` — Stuck
  review escalated.
- `TestCoordination_LegacyCoordinationAPICall_BuildError` — Old API
  errors at build.
- `TestCoordination_ContentionStorm_RateLimited` — Pathological
  contention rate-limited.

---

#### 7.6 — Task dispatch as action

**Description**: Per §14.9. `handleTaskDispatch` creates a claims
board, posts the architect's claims as an action, and dispatches
subjects. No protocol handshake.

**Implementation approach**:
- Package: `agents/orchestrator/task_dispatch.go`.
- New flow: receive plan handoff (Phase 3.5) → create claims board
  → inspector issues claims → dispatch agents.
- No protocol-snapshot creation; no handoff handshake.
- Subjects (Engineer / Designer / Tester) auto-discovered from
  claim subjects.
- Code path: `agents/orchestrator/task_dispatch.go` (~1000 LOC).

**Acceptance criteria**:
- Task dispatch creates board and posts claims.
- No protocol creation/dispatch path remains.
- Subject agents auto-dispatched per claim subjects.
- Existing dispatch tests pass.

**Unit tests**:
- `TestTaskDispatch_BoardCreatedFromPlan` — Board created.
- `TestTaskDispatch_InspectorIssues` — Inspector formal issuer.
- `TestTaskDispatch_NoProtocolPath` — Protocol code not invoked.
- `TestTaskDispatch_SubjectsAutoDispatched` — Subjects from claims.

**Integration tests**:
- `TestTaskDispatch_FullFlow` — End-to-end.
- `TestTaskDispatch_PreConversionTestsPort` — Existing tests pass.

**End-to-end tests**:
- `TestTaskDispatchE2E_RealPlan` — Real plan dispatched.

**Race condition tests**:
- `TestTaskDispatch_ConcurrentDispatches` — `go test -race`.
- `TestTaskDispatch_BoardCreationRaceWithSecondDispatch` — Race
  resolved deterministically.

**Negative / non-happy path tests**:
- `TestTaskDispatch_PlanWithEmptyClaims_Refused` — Empty plan
  rejected.
- `TestTaskDispatch_SubjectAgentUnavailable_BoardWaits` — Agent
  down; dispatch queued.
- `TestTaskDispatch_PlanFromUnauthorizedArchitect_Refused` —
  Authority enforced.
- `TestTaskDispatch_DuplicatePlanID_Idempotent` — Duplicate
  dispatched once.

---

### Phase 8 — Sovereign System Retirement

Three sovereign systems (Pipeline Protocol, Coordination Service,
Decision Manifest) retire into one claims board. Per §14.10.

**Phase implementation overview**: Phase 8 finalizes the conversion
that earlier phases set up. The pipeline protocol's structures and
durable events were retired in Phase 2.5; this phase closes out the
remaining two sovereign systems (Coordination Service, Decision
Manifest) and verifies no orphan references remain anywhere. Each
sub-phase follows the same pattern: dual-write window → shadow-read
parity → cutover → file removal → codebase scan.

#### 8.1 — Pipeline Protocol → Claims Board

**Description**: Per §14.10 #8a. Retire `PipelineProtocolSnapshot`,
`PipelineTurnAction`, `PipelineProtocolState` (reducer / WAL /
mailbox), durable events, terminal action guards, audit lock. Final
removal step; the code-level retirement happens in Phase 2.5.

**Implementation approach**:
- Code-level retirement: see Phase 2.5.
- Phase 8.1 is the *codebase verification* step:
  - grep for any remaining references to retired types.
  - Verify pre-conversion test suite passes against claims-based.
  - Verify pre-conversion WAL data migrated to claims WAL via
    migration tooling (`tools/migrate_protocol_wal/`).
  - Remove the old `core/protocol/` package directory and any unused
    shared types.
- Documentation: update `docs/PROTOCOL.md` to redirect readers to
  `docs/CLAIMS.md`.
- Code path: ~500 LOC removed (cleanup); migration tool ~800 LOC.

**Acceptance criteria**:
- Zero references to `PipelineProtocolSnapshot`, `PipelineTurnAction`,
  `PipelineProtocolState` in the entire codebase.
- Zero references to retired durable events.
- Migration tool successfully ports old WAL data to claims WAL.
- Pre-conversion test suite passes against claims-based
  implementation.
- `core/protocol/` package directory removed.
- `docs/PROTOCOL.md` updated.

**Unit tests**:
- `TestProtocolRetirement_NoSnapshotImports` — Codebase scan.
- `TestProtocolRetirement_NoTurnActionImports` — Scan.
- `TestProtocolRetirement_NoTerminalGuardCalls` — Scan.
- `TestProtocolRetirement_NoStateMachineReferences` — Scan.
- `TestProtocolRetirement_NoDurableEventReferences` — Scan.
- `TestProtocolRetirement_PackageDirectoryAbsent` — Directory gone.

**Integration tests**:
- `TestProtocolRetirement_FullPipelineRuns` — Pipeline runs without
  protocol.
- `TestProtocolRetirement_PreConversionTestSuiteAdapted` — Existing
  tests pass.
- `TestProtocolRetirement_WALMigrationTool` — Old WAL → new.

**End-to-end tests**:
- `TestProtocolRetirementE2E_NoRegressions` — Full session no
  regressions.
- `TestProtocolRetirementE2E_HistoricalDataMigrated` — Pre-conversion
  data accessible via claims.

**Race condition tests**:
- `TestProtocolRetirement_MigrationConcurrentWithLiveTraffic` —
  Migration during traffic.

**Negative / non-happy path tests**:
- `TestProtocolRetirement_LegacyDataMigratedOrFailsExplicitly` —
  Migration completes or fails clearly.
- `TestProtocolRetirement_LegacyImportInDevBranch_BuildErrors` —
  Build catches.
- `TestProtocolRetirement_DocsUpdatedNoStaleReferences` — Docs
  scan.
- `TestProtocolRetirement_PartialMigrationResumable` — Resumable.
- `TestProtocolRetirement_CorruptedLegacyWAL_DiagnosedAndQuarantined`
  — Corrupted source data handled.

---

#### 8.2 — Coordination Service → Claims Board

**Description**: Per §14.10 #8b. Retire `manage_claim`,
`publish_work_event`, `query_claims_board` (the legacy coordination
versions, not the new claims-board version), board subscription,
`ClaimMode` enum.

**Implementation approach**:
- Package: `core/coordination/` (full removal).
- Replacements:
  - `manage_claim(action=acquire/release)` → scope claims (Phase
    7.5).
  - `publish_work_event(kind=artifact/review_request/review_completion)`
    → testaments + consultation actions (Phase 0.5 skills).
  - `ClaimMode (exclusive/shared/review)` → relation types on scope
    claims (`exclusive_scope`, `shared_scope`, `review_scope`).
- Migration tool (`tools/migrate_coordination/`) ports existing
  coordination state to claims-board scope claims.
- Dual-write window: 7 days; shadow-read parity verification;
  cutover; remove `core/coordination/`.
- Code path: ~2000 LOC removed; migration tool ~600 LOC.

**Acceptance criteria**:
- `core/coordination/` package directory removed.
- All callers ported.
- Existing coordination test corpus passes against claims-based.
- Scope semantics preserved: exclusive / shared / review.
- Migration tool round-trips coordination state to claims state.

**Unit tests**:
- `TestCoordinationRetirement_NoManageClaim` — Function gone.
- `TestCoordinationRetirement_NoPublishWorkEvent` — Function gone.
- `TestCoordinationRetirement_NoClaimMode` — Enum gone.
- `TestCoordinationRetirement_PackageDirectoryAbsent`.
- `TestCoordinationRetirement_NoLegacySubscribers`.
- `TestCoordinationRetirement_RelationTypesEquivalent` — Scope
  relations equivalent to old ClaimMode semantics.

**Integration tests**:
- `TestCoordinationRetirement_FullCoordinationViaClaims` — End-to-
  end.
- `TestCoordinationRetirement_ScopeContentionEquivalent` — Same
  contention behavior.
- `TestCoordinationRetirement_ExistingTestSuiteAdapted` — Tests
  pass.

**End-to-end tests**:
- `TestCoordinationRetirementE2E_NoRegressions` — Full session.
- `TestCoordinationRetirementE2E_MigrationFlow` — Migration tool
  end-to-end.

**Race condition tests**:
- `TestCoordinationRetirement_MigrationDuringActiveContention` —
  Migration concurrent with contention.
- `TestCoordinationRetirement_DualWriteParityRace` — Dual-write
  consistent.

**Negative / non-happy path tests**:
- `TestCoordinationRetirement_LegacyDataMigrated` — All ported.
- `TestCoordinationRetirement_OrphanScopeClaimsCleanedUp` — Orphans.
- `TestCoordinationRetirement_LegacyAPICall_BuildError` — Build
  fails on legacy use.
- `TestCoordinationRetirement_PartialMigrationResumable` — Resumable.
- `TestCoordinationRetirement_ParityMismatchHaltsCutover` — Mismatch
  blocks.
- `TestCoordinationRetirement_ScopeOverlapDetectionPreserved` —
  Same detection.

---

#### 8.3 — Decision Manifest → Claims Board

**Description**: Per §14.10 #8c. Retire `declare_decision`,
`query_decisions`, decision confidence enum, auto-publish, manifest
reconciliation.

**Implementation approach**:
- Package: `core/decisions/` (full removal).
- Replacements:
  - `declare_decision` → claim "Declare <key>=<value>" with testament
    containing detection artifacts.
  - `query_decisions` → `query_claims_board` filtered by
    decision-type claims.
  - Decision confidence (Hint / Tentative / Committed / Consensus)
    → testament `Confidence` field.
  - Auto-publish on skill invocation → skill amplifiers emit claims
    (e.g., `detect_test_harness` → claim "test_framework detected"
    with testament containing detection artifact).
  - Manifest reconciliation → `inspect_claim_conflicts` for
    decision-type claims.
- Migration tool (`tools/migrate_decisions/`) ports existing
  manifest entries to claim-shaped entries.
- Dual-write + cutover.
- Code path: ~1500 LOC removed; migration tool ~500 LOC.

**Acceptance criteria**:
- `core/decisions/` package directory removed.
- All decision-emitting skills now emit claims.
- Existing decision queries work via `query_claims_board` with
  decision-type filter.
- Confidence levels preserved on testaments.
- Manifest reconciliation maps to conflict inspection.
- Migration tool ports historical decisions.

**Unit tests**:
- `TestDecisionRetirement_NoDeclareDecision` — Function gone.
- `TestDecisionRetirement_NoQueryDecisions` — Function gone.
- `TestDecisionRetirement_NoConfidenceEnum` — Enum gone.
- `TestDecisionRetirement_PackageDirectoryAbsent`.
- `TestDecisionRetirement_AmplifierEmitsClaim` — Skill amplifier.
- `TestDecisionRetirement_TestamentConfidenceField` — Field
  populated correctly.
- `TestDecisionRetirement_ConflictInspectionEquivalent` — Same
  detection.

**Integration tests**:
- `TestDecisionRetirement_FullDecisionsViaClaims` — End-to-end.
- `TestDecisionRetirement_AcrossPipelinesViaClaims` — Cross-pipeline
  decision visibility.
- `TestDecisionRetirement_ExistingTestSuiteAdapted` — Tests pass.

**End-to-end tests**:
- `TestDecisionRetirementE2E_NoRegressions` — Full session.
- `TestDecisionRetirementE2E_MigrationFlow` — Migration tool E2E.

**Race condition tests**:
- `TestDecisionRetirement_MigrationDuringActiveDecisionEmission` —
  Migration concurrent.
- `TestDecisionRetirement_DualWriteParityRace`.

**Negative / non-happy path tests**:
- `TestDecisionRetirement_LegacyDataMigrated` — All ported.
- `TestDecisionRetirement_LegacyAPICall_BuildError` — Build catches.
- `TestDecisionRetirement_ConflictingDecisionsSurfacedViaInspect` —
  Surface.
- `TestDecisionRetirement_StaleDecisionsRetentionPolicy` — Stale
  per retention.
- `TestDecisionRetirement_PartialMigrationResumable` — Resumable.
- `TestDecisionRetirement_AmbiguousConfidenceMapping_Documented` —
  Confidence-mapping ambiguity caught.

---

### Phase 9 — System Infrastructure Conversion

Handoff, Session, VFS, Errors, Steering. Per §14.11.

**Phase implementation overview**: Phase 9 converts the system-level
infrastructure where claims interact most heavily with non-agent
code: agent handoff (context-exhaustion-driven instance swap),
session management (long-lived conversational state), VFS (file
operations bounded by claim scope), error handling (failures as
corrective claims), steering (user / system directives as claims).
Each conversion preserves existing semantics while making everything
visible on the claims board.

#### 9.1 — Handoff Protocol

**Description**: Per §14.11 #9a. Board IS the handoff state — when
an agent hits context exhaustion, the new instance reads the claims
board to resume. The handoff itself becomes a claim.

**Implementation approach**:
- Package: `core/handoff/`, `core/concurrency/`,
  `agents/shared/context_governor.go`.
- Remove `BuildHandoffState` / `InjectHandoffState` interfaces (the
  HandoffableAgent interface simplifies to "agent reads board").
- Handoff trigger (context budget zone, quality drop, transport
  retry exhaustion) posts a *handoff claim* against the new agent
  instance.
- Old agent submits testament with `archivable_state` artifact —
  any state that *can't* be reconstructed from the board (open
  context windows, in-flight LLM call cursors, scratchpad).
- New agent submits testament confirming context injection +
  resumption; ongoing work claims continue from board state.
- Claim has `causality=happens-after` so new agent doesn't visibly
  proceed until old agent's testament is committed.
- Code path: `core/handoff/claims_handoff.go` (~1500 LOC) +
  removals (~600 LOC).
- Hard part: state extraction during context exhaustion. Solved by
  bounding extraction time + falling back to board-only resumption
  if extraction times out.

**Acceptance criteria**:
- `BuildHandoffState` / `InjectHandoffState` removed.
- Board IS handoff state for the typical case.
- Handoff itself is a claim.
- Old agent's testament includes `archivable_state` for non-board-
  reconstructible state.
- New agent confirms context injection + resumption with testament.
- Causal happens-after preserved.
- Extraction bounded in time; falls back to board-only on timeout.
- Quality validation: new agent's first-N-turn quality not
  significantly degraded vs old agent.
- Audit trail: every handoff queryable.

**Unit tests**:
- `TestHandoff_BoardIsState` — Board sufficient for resumption.
- `TestHandoff_AsClaim` — Handoff claim shape.
- `TestHandoff_OldTestamentArchivable` — Archivable state captured.
- `TestHandoff_NewTestamentInjection` — Injection confirmed.
- `TestHandoff_NewAgentResumesFromBoard` — Resumption works.
- `TestHandoff_CausalOrdering` — New awaits old.
- `TestHandoff_ExtractionTimeoutFallsBackToBoardOnly` — Fallback.
- `TestHandoff_QualityValidationOnNewAgent` — Quality check.
- `TestHandoff_AuditTrailComplete` — Trail queryable.

**Integration tests**:
- `TestHandoff_RealHandoffScenario` — Real context-exhaustion flow.
- `TestHandoff_QualityDriftTriggered` — Quality-drop trigger.
- `TestHandoff_TransportRetryExhausted` — Transport trigger.

**End-to-end tests**:
- `TestHandoffE2E_ContextExhaustion` — Real session crosses
  exhaustion boundary; resumption transparent to user.
- `TestHandoffE2E_HandoffMidCriticalWork` — Handoff during
  active critical work; correctness preserved.

**Race condition tests**:
- `TestHandoff_ConcurrentHandoffs` — `go test -race`.
- `TestHandoff_HandoffRaceWithNewWork` — New work claim race.
- `TestHandoff_OldAgentExtractsRaceWithNewAgentSpawn` — Race.

**Negative / non-happy path tests**:
- `TestHandoff_NewAgentReadFails_RetriedViaBoardRevert` — Retry.
- `TestHandoff_OldAgentCrashesDuringExtract_StatePreservedViaBoard`
  — Board-canonical fallback.
- `TestHandoff_HandoffStorm_RateLimited` — Pathological repeated
  handoffs throttled.
- `TestHandoff_NewAgentSpawnFails_OldAgentResumes` — Recovery.
- `TestHandoff_QualityDegradationAfterHandoff_RemediationClaim` —
  New handoff if quality bad.
- `TestHandoff_ContextInjectionFails_AlertOperator` — Escalate.
- `TestHandoff_ArchivableStateOversize_Truncated` — Bounded.
- `TestHandoff_BoardStateInconsistent_FailHandoffNotData` — Fail
  closed.

---

#### 9.2 — Session Management

**Description**: Per §14.11 #9b. Each session gets a root claims
board. User prompts, agent responses, cross-agent interactions are
all actions on this board. The board IS the conversation history.
Pipeline boards are children of the session board.

**Implementation approach**:
- Package: `core/session/`.
- `SessionBoard` per session; identified by session ID.
- Persistence path: `.sylk/sessions/<id>/claims/` (per-session WAL +
  checkpoints, per Phase 0.3).
- Pipeline boards reference session as parent via Relation
  (`parent_session`); session board references children via
  `child_pipelines` query.
- Session persistence: WAL fsync per critical mutation; bounded
  growth via §0.3 retention.
- Recovery: restart loads session board from disk; replays from
  checkpoint.
- GC: idle sessions past retention reaped after operator-confirmed
  archival.
- Code path: `core/session/session_claims.go` (~1500 LOC) +
  modifications (~500 LOC).

**Acceptance criteria**:
- Per-session SessionBoard with unique ID.
- Pipeline boards children of session board (via Relation).
- Persistence to `.sylk/sessions/<id>/claims/`.
- Recovery restores full session state on restart.
- Multiple concurrent sessions isolated.
- Session GC respects retention policy.
- Conversation-history-loss bug fixed (regression test).
- Per-session resource budget honored (§22.5).

**Unit tests**:
- `TestSession_BoardPerSession` — Unique board per session.
- `TestSession_PipelineParentChild` — Relation correct.
- `TestSession_RecoveryRestores` — Recovery from disk.
- `TestSession_ConversationHistoryFromBoard` — History queryable.
- `TestSession_HistoryLossBugRegressionTest` — Bug fixed.
- `TestSession_GCRespectsRetention` — Retention.
- `TestSession_PerSessionBudget` — Budget enforced.

**Integration tests**:
- `TestSession_MultipleSessions` — Concurrent sessions isolated.
- `TestSession_PipelineLifecycleWithinSession` — Lifecycle.
- `TestSession_LongSessionPersistence` — Long-running session.

**End-to-end tests**:
- `TestSessionE2E_FullLifecycle` — Real session create → use →
  close.
- `TestSessionE2E_RestartMidSession` — Restart preserves session.

**Race condition tests**:
- `TestSession_ConcurrentSessionAccess` — `go test -race`.
- `TestSession_GCDuringActiveSession` — GC vs activity race.
- `TestSession_RecoveryDuringConcurrentNewSessions` — Recovery
  race.

**Negative / non-happy path tests**:
- `TestSession_DiskFullPerSession_Isolated` — Per-session isolation.
- `TestSession_OrphanedSessions_GC` — Orphan reaping.
- `TestSession_SessionDeletedMidPipeline_GracefulHalt` — Halt.
- `TestSession_CorruptedSessionWAL_RecoveredViaCheckpoint` —
  Recovery.
- `TestSession_TooManyConcurrentSessions_BackpressureToCaller` —
  Backpressure.
- `TestSession_SessionIDCollision_Refused` — Collision detected.
- `TestSession_PrematureGCBlockedByActiveAgents` — GC respects
  active.

---

#### 9.3 — VFS Integration

**Description**: Per §14.11 #9c. Every `workspace_write` operates
within scope defined by agent's claims. Writes produce artifacts.
Commits produce testaments.

**Implementation approach**:
- Package: `core/versioning/`, `agents/shared/`.
- Pre-write check: VFS write op consults active claim's scope; if
  the target file path isn't within scope, refuse with corrective
  action ("Acquire scope for <path>").
- Post-write: every VFS write emits an `update_claim_progress` with
  artifact `kind=diff` (file path + line range + change summary).
- Commit (`MergePipelineIntoGreen`) produces testament with
  artifacts: `kind=merge_summary` (paths merged, version, base
  version), `kind=diff` aggregate.
- Read-only operations don't require scope.
- Code path: `core/versioning/claims_integration.go` (~1200 LOC).

**Acceptance criteria**:
- Pre-write scope check enforced.
- Out-of-scope writes refused with corrective action.
- Per-write progress artifacts.
- Commit testament with structured merge artifacts.
- Read-only operations exempt from scope check.
- Performance: scope check ≤ 50µs per write (cached).
- Authority: only the active claim's subject can write within its
  scope.

**Unit tests**:
- `TestVFS_ScopeFromClaim` — Scope derived from claim.
- `TestVFS_WriteProducesArtifact` — Per-write artifact.
- `TestVFS_CommitProducesTestament` — Commit testament.
- `TestVFS_DiffArtifactStructure` — Artifact shape.
- `TestVFS_MergeSummaryArtifact` — Merge artifact.
- `TestVFS_ReadOnlyExempt` — Reads don't need scope.
- `TestVFS_ScopeCheckCachedFast` — < 50µs.
- `TestVFS_AuthorityEnforced` — Only subject writes.

**Integration tests**:
- `TestVFS_FullWriteFlow` — Write → artifact → commit testament.
- `TestVFS_MultiFileWriteCoalesced` — Multi-file batched.
- `TestVFS_ScopeBoundaryEnforced` — Real boundary.

**End-to-end tests**:
- `TestVFSE2E_RealCommitCycle` — Real commit.
- `TestVFSE2E_AgentWorkflow` — Full agent VFS workflow.

**Race condition tests**:
- `TestVFS_ConcurrentScopedWrites` — `go test -race`.
- `TestVFS_ScopeChangeDuringActiveWrite` — Race.
- `TestVFS_CommitRaceWithWrite` — Commit during write.

**Negative / non-happy path tests**:
- `TestVFS_OutOfScopeWrite_RejectedWithCorrective` — Corrective.
- `TestVFS_MergeConflict_ProducesErrorArtifact` — Merge conflict.
- `TestVFS_DiskFullDuringWrite_BackpressureToAgent` — Disk full.
- `TestVFS_OversizedDiffArtifact_Truncated` — Bounded.
- `TestVFS_NoActiveClaimDuringWrite_Refused` — No claim → refuse.
- `TestVFS_ClaimRevokedMidWrite_WriteAborted` — Mid-write
  revocation.
- `TestVFS_ScopeIncludesNonexistentPath_RefusedAtCreation` — Bad
  scope.

---

#### 9.4 — Error Handling

**Description**: Per §14.11 #9d. Skill / LLM / tool failures produce
corrective claims that guide the agent to satisfy the failed
precondition. Errors are not raw error returns.

**Implementation approach**:
- Package: `core/claims/corrective.go`, `core/providers/`,
  `core/toolruntime/`.
- Skill precondition failures (missing scope, wrong phase,
  insufficient context): instead of `return error`, emit corrective
  action with claims:
  - Missing scope → "Acquire scope for <path> before <op>".
  - Wrong phase → "Wait for phase <X> before <op>".
  - Insufficient context → "Request consultation from <peer>".
- LLM failures (timeout, rate limit, context cancelled): corrective
  claims:
  - Timeout → "Retry with reduced context".
  - Rate limit → "Wait for rate-limit reset (HLC ≥ <eta>)".
  - Cancelled → "Investigate cancellation reason".
- Tool failures: corrective claims against the agent diagnosing the
  failure.
- Errors are still surfaced via testament artifacts (`kind=error`,
  `kind=error_trace`, `kind=error_diagnostic`); the corrective is
  the *next-step guidance*.
- Code path: `core/claims/corrective.go` (~1500 LOC) + adapter
  layers (~800 LOC).

**Acceptance criteria**:
- All listed failure categories produce corrective actions.
- Corrective actions include actionable claims (not just error
  text).
- Errors-as-artifacts preserved on testaments.
- Bounded retry: corrective doesn't loop unbounded.
- Escalation path: after N corrective iterations, escalate to
  operator / orchestrator.
- Audit: every corrective traceable to root failure.

**Unit tests**:
- `TestError_SkillPreconditionCorrective` — Precondition →
  corrective.
- `TestError_LLMFailureCorrective_Timeout` — Timeout corrective.
- `TestError_LLMFailureCorrective_RateLimit` — Rate-limit
  corrective.
- `TestError_LLMFailureCorrective_Cancelled` — Cancelled corrective.
- `TestError_ToolFailureCorrective` — Tool failure corrective.
- `TestError_ErrorArtifactPreserved` — Errors-as-artifacts.
- `TestError_BoundedRetry` — Bounded.
- `TestError_EscalationPath` — Escalation triggers.
- `TestError_AuditTrail` — Trail traceable.

**Integration tests**:
- `TestError_FullCorrectiveFlow` — Failure → corrective → resolve.
- `TestError_RealisticAgentFailureScenario` — Realistic flow.

**End-to-end tests**:
- `TestErrorE2E_LLMTimeoutRecovery` — Real LLM timeout recovery.

**Race condition tests**:
- `TestError_ConcurrentFailures` — `go test -race`.
- `TestError_CorrectiveRaceWithRecovery` — Race.

**Negative / non-happy path tests**:
- `TestError_NoRecoverableCorrective_EscalatesToOperator` —
  Escalate.
- `TestError_CorrectiveLoopExceedsBound_AbortsWithDiagnostic` —
  Bounded.
- `TestError_FailureDuringCorrective_ChainedCorrective` — Chained.
- `TestError_TenantBudgetExhausted_NoMoreCorrectives_Throttle` —
  Budget.
- `TestError_CatastrophicFailure_OperatorAlert` — Alert.
- `TestError_LLMHardFailure_AgentSuspended` — Suspension.

---

#### 9.5 — Steering Ledger

**Description**: Per §14.11 #9e. Steering directives ("Focus on
security-critical paths") become claims from user / inspector against
agents. Priority hints become `Priority` field; quality gates become
validations.

**Implementation approach**:
- Package: `core/steering/`, `agents/shared/`.
- Steering directives as `Action` with `ActionType=Steering` and
  claims:
  - Subject = target agent.
  - Priority ∈ `Critical / High / Standard / Low / Background`.
  - Validations = quality gates (e.g., "Code reviewed for security
    issues" with quality bar).
- Existing steering ledger ports its priority hints + quality bars
  to claim fields.
- Agents read steering claims as ambient context input.
- Conflicting steering: latest active steering wins (overrides
  earlier); explicit override creates supersedes Relation.
- Code path: `core/steering/steering_claims.go` (~800 LOC) +
  removal of legacy `core/steering/` (~600 LOC).

**Acceptance criteria**:
- Steering as claims with `ActionType=Steering`.
- Priority field maps to Sylk priority enum.
- Quality validations attached.
- Latest steering wins; supersedes Relations preserve history.
- Agents consume steering as ambient context.
- Operator can introspect / revoke steering at any time.

**Unit tests**:
- `TestSteering_AsClaims` — Claim shape.
- `TestSteering_PriorityField` — Priority levels.
- `TestSteering_QualityValidations` — Validations.
- `TestSteering_LatestWinsOverride` — Override.
- `TestSteering_SupersedesRelation` — History preserved.
- `TestSteering_AgentConsumesAsAmbient` — Visibility.
- `TestSteering_OperatorRevoke` — Revoke works.

**Integration tests**:
- `TestSteering_FullSteeringFlow` — Directive → ambient → action.
- `TestSteering_ConflictingDirectives` — Latest wins.

**End-to-end tests**:
- `TestSteeringE2E_RealUserDirective` — Real user-issued
  directive.

**Race condition tests**:
- `TestSteering_ConcurrentDirectives` — `go test -race`.
- `TestSteering_RevokeRaceWithApplication` — Race.

**Negative / non-happy path tests**:
- `TestSteering_ConflictingDirectives_LatestWins` — Latest.
- `TestSteering_RevokedSteeringNotApplied` — Revocation enforced.
- `TestSteering_OperatorAuthorityRequired_UserCannotElevate` —
  Authority.
- `TestSteering_StaleSteering_RetentionPolicy` — Stale per policy.
- `TestSteering_ContradictorySteering_OperatorAlert` — Alert on
  contradictory directives needing resolution.
- `TestSteering_UnboundedDirectiveStack_GCAfterRetention` — GC.

---

### Phase 10 — TUI Conversion

Render claims/testaments/artifacts instead of protocol state and task
prompts. Per §14.12.

**Phase implementation overview**: Phase 10 rewrites the terminal UI's
agent panels, pipeline visualization, chat rendering, and conversation
context to consume the claims board as the canonical state source.
The UI subscribes to board events for live updates with bounded
backpressure; falls back to digest queries on subscription gaps. UI
must remain responsive under high-volume update streams (claim
progress every few hundred ms during active implementation). Common
dependencies: `bubbletea` (or current Sylk TUI framework),
`reactive-style` updates over the §0.2 board subscription, bounded
LRU caches for view state.

#### 10.1 — Agent panel

**Description**: Per §14.12. Per-pipeline agent panel shows claims
board state: claims in progress, testified, accepted/rejected,
remediation. Replaces sequential phase display.

**Implementation approach**:
- Package: `ui/agent/`.
- Subscribe to pipeline's claims board (Phase 0.2 subscription).
- View model: list of claims grouped by status; per-claim details
  pane; testament + artifacts collapsible.
- Update batching: coalesce updates within 100ms window to avoid
  redraw thrash.
- Bounded subscription buffer: ≥ 1000 events; oldest dropped on
  overflow with marker.
- Status filter (in-progress / testified / accepted / rejected /
  all).
- Per-agent filter (mine / peers / all).
- Code path: `ui/agent/claims_panel.go` (~1200 LOC).

**Acceptance criteria**:
- Live updates render within 100ms of board mutation.
- Bounded subscription buffer; overflow marker visible.
- Status + agent filters work without re-fetch.
- Replaces current sequential phase display.
- Click on claim → detail pane with testament + artifact tree.
- Update batching prevents redraw thrash under high update volume.
- Keyboard navigation (j/k arrow keys, /-style search).
- Color-coded status (per a11y guidelines: shape + color, not color
  alone).
- Recovers cleanly from board subscription gaps via digest fallback.

**Unit tests**:
- `TestUI_AgentPanel_ClaimsRendered` — All claims appear.
- `TestUI_AgentPanel_StatusFiltering` — Filter dispatch.
- `TestUI_AgentPanel_AgentFiltering` — Per-agent filter.
- `TestUI_AgentPanel_ClaimDetailPane` — Detail rendered correctly.
- `TestUI_AgentPanel_TestamentTreeCollapsible` — Tree expand/collapse.
- `TestUI_AgentPanel_ColorBlindFriendly` — Shape + color.
- `TestUI_AgentPanel_KeyboardNav` — Key bindings.
- `TestUI_AgentPanel_UpdateBatching` — 100ms coalescing.

**Integration tests**:
- `TestUI_AgentPanel_RealisticPipeline` — Real pipeline; updates
  flow.
- `TestUI_AgentPanel_HighUpdateVolume_NoThrash` — High volume; no
  flicker.
- `TestUI_AgentPanel_SubscriptionGap_DigestRecovery` — Gap → digest
  fallback.

**End-to-end tests**:
- `TestUI_AgentPanelE2E_LiveUpdates` — Real session; live updates
  end-to-end.
- `TestUI_AgentPanelE2E_FullClaimLifecycle` — Lifecycle visible.

**Race condition tests**:
- `TestUI_AgentPanel_ConcurrentUpdatesAndUserInput` — `go test -race`.
- `TestUI_AgentPanel_FilterChangeDuringUpdate` — Filter race.
- `TestUI_AgentPanel_SubscriptionRestartRace` — Reconnect race.

**Negative / non-happy path tests**:
- `TestUI_AgentPanel_BoardCorrupted_DegradedUI` — Corrupted state;
  show degraded marker; no crash.
- `TestUI_AgentPanel_SubscriptionDropped_Reconnects` — Drop; auto-
  reconnect; backfill via digest.
- `TestUI_AgentPanel_OversizedTestament_Truncated` — Large
  testament truncated with marker.
- `TestUI_AgentPanel_BoardEmpty_HelpfulPlaceholder` — Empty state.
- `TestUI_AgentPanel_TerminalResizeDuringUpdate_Reflows` — Resize
  handled.
- `TestUI_AgentPanel_BoardDeleted_PanelClosesGracefully` — Board
  gone; panel cleans up.

---

#### 10.2 — Pipeline visualization

**Description**: Per §14.12. Pipeline panel shows progress bars,
testament counts, artifact counts. Color-coded by status.

**Implementation approach**:
- Package: `ui/pipeline/`.
- Per-pipeline summary: total claims / in-progress / testified /
  accepted / rejected counts.
- Progress bar = accepted/total ratio.
- Color coding: green (all accepted), yellow (in-progress), red
  (any rejected without remediation), blue (remediation in flight).
- Status icons (✓ ✗ ⟳) for accessibility (color + shape).
- Click → expand to per-claim detail (uses §10.1 panel).
- Code path: `ui/pipeline/pipeline_panel.go` (~800 LOC).

**Acceptance criteria**:
- Counts accurate vs board projection.
- Progress bar reflects acceptance ratio.
- Color + icon coding.
- Click → drill-down.
- Per-pipeline status visible at a glance.
- Updates live with 100ms coalesce.

**Unit tests**:
- `TestUI_PipelineViz_ProgressBars` — Progress correct.
- `TestUI_PipelineViz_Counts` — Counts match projection.
- `TestUI_PipelineViz_ColorCoding` — Color reflects status.
- `TestUI_PipelineViz_Icons` — Icons match.
- `TestUI_PipelineViz_DrillDown` — Click navigates.

**Integration tests**:
- `TestUI_PipelineViz_RealUpdates` — Live update flow.
- `TestUI_PipelineViz_MultiplePipelinesParallel` — Many pipelines
  visible.

**End-to-end tests**:
- `TestUI_PipelineVizE2E_FullSession` — Real session; visualization
  accurate.

**Race condition tests**:
- `TestUI_PipelineViz_ConcurrentPipelineUpdates` — `go test -race`.
- `TestUI_PipelineViz_PipelineDeletionRace` — Deletion mid-render.

**Negative / non-happy path tests**:
- `TestUI_PipelineViz_PipelineCrashed_StatusReflected` — Crash
  visible.
- `TestUI_PipelineViz_StaleProjection_AlertedToUser` — Staleness
  marker.
- `TestUI_PipelineViz_NoPipelines_PlaceholderRendered` — Empty.
- `TestUI_PipelineViz_PathologicalUpdateRate_DroppedNotJanky` — High
  rate; drop with marker.

---

#### 10.3 — Chat rendering

**Description**: Per §14.12. Testaments → structured responses with
collapsible artifact lists. Claims → task cards.

**Implementation approach**:
- Package: `ui/chat/`.
- Message types: user prompt (PromptAction display), agent response
  (testament display), system message (board phase changes).
- Testament rendering: summary + collapsible artifacts list with
  per-kind formatters (code_reference shows preview, design_asset
  shows thumbnail descriptor, error shows formatted error).
- Markdown rendering for summary text.
- Artifact previews bounded; click → full view.
- Per-artifact-kind icons.
- Code path: `ui/chat/render.go` (~1000 LOC).

**Acceptance criteria**:
- Testaments rendered with structure.
- Artifacts collapsible.
- Per-kind formatters dispatched correctly.
- Markdown summary rendered (with safe sanitization).
- Code references show file:line preview.
- Errors highlighted (red, error icon, structured).
- Bounded preview size.

**Unit tests**:
- `TestUI_Chat_TestamentRender` — Structured render.
- `TestUI_Chat_CollapsibleArtifacts` — Expand/collapse.
- `TestUI_Chat_PerKindFormatter_CodeRef` — Code preview.
- `TestUI_Chat_PerKindFormatter_Error` — Error highlighted.
- `TestUI_Chat_PerKindFormatter_Diff` — Diff colorized.
- `TestUI_Chat_MarkdownSafeSanitization` — XSS-safe.
- `TestUI_Chat_PreviewBounded` — Bounded.

**Integration tests**:
- `TestUI_Chat_RealConversation` — Real session conversation.
- `TestUI_Chat_MixedArtifactKinds` — Multi-kind testament.

**End-to-end tests**:
- `TestUI_ChatE2E_FullDialogue` — Full multi-turn dialogue.

**Race condition tests**:
- `TestUI_Chat_ConcurrentMessageInsert` — `go test -race`.
- `TestUI_Chat_ScrollDuringInsert` — Scroll race.

**Negative / non-happy path tests**:
- `TestUI_Chat_MalformedMarkdown_FallbackToPlainText` — Bad markdown
  falls back.
- `TestUI_Chat_OversizedArtifactPreview_Truncated` — Truncation.
- `TestUI_Chat_UnknownArtifactKind_GenericFormatter` — Unknown kind
  rendered with generic formatter.
- `TestUI_Chat_TestamentMissingArtifacts_RendersSummary` — Just
  summary.
- `TestUI_Chat_BinaryArtifactRef_HexPreview` — Binary safe.

---

#### 10.4 — Claims board view (dedicated panel)

**Description**: Per §14.12. Dedicated full-board panel with filters
by agent/status/action type. Shows claim → testament → validation
chain.

**Implementation approach**:
- Package: `ui/board/`.
- Three-pane layout: filters | claim list | claim detail (chain
  view).
- Chain view: claim → testaments (timeline) → validations (status
  per validation) → relations (graph view of supersedes / depends_on /
  caused_by).
- Filters: agent (multi-select), status, action type, time range,
  scope.
- Search: full-text over claim descriptions + testament summaries.
- Live updates with 100ms coalesce.
- Code path: `ui/board/board_panel.go` (~1500 LOC).

**Acceptance criteria**:
- Three-pane layout.
- Chain view shows full claim → testament → validation → relation
  graph.
- Filters work without re-fetch.
- Full-text search ≤ 50ms on 10K-claim board.
- Live updates.
- Keyboard nav.
- Export visible state to JSON / clipboard.

**Unit tests**:
- `TestUI_BoardView_Filters` — All filter dispatchers.
- `TestUI_BoardView_ClaimChain` — Chain rendered correctly.
- `TestUI_BoardView_TestamentTimeline` — Timeline order.
- `TestUI_BoardView_RelationGraph` — Graph rendered.
- `TestUI_BoardView_FullTextSearch` — Search results correct.
- `TestUI_BoardView_SearchPerformance` — ≤ 50ms.
- `TestUI_BoardView_ExportToJSON` — Export.

**Integration tests**:
- `TestUI_BoardView_RealisticBoard` — Realistic 10K-claim board.
- `TestUI_BoardView_MultipleFiltersCombined` — Filter combinations.

**End-to-end tests**:
- `TestUI_BoardViewE2E_LiveBoardUpdates` — Live updates flow.
- `TestUI_BoardViewE2E_AuditUseCase` — Auditor uses panel to
  trace claim history.

**Race condition tests**:
- `TestUI_BoardView_ConcurrentFilterChange` — `go test -race`.
- `TestUI_BoardView_SearchDuringUpdate` — Race.
- `TestUI_BoardView_ChainUpdateDuringRender` — Race.

**Negative / non-happy path tests**:
- `TestUI_BoardView_HugeBoardPagination` — Pagination.
- `TestUI_BoardView_OrphanedRelation_RenderedAsBroken` — Broken
  link visible.
- `TestUI_BoardView_DeletedClaimReferenced_RenderedAsTombstone` —
  Tombstone visible.
- `TestUI_BoardView_FilterMatchesNothing_HelpfulMessage` — Empty.
- `TestUI_BoardView_SubscriptionFails_DegradedReadOnly` — Read-only
  fallback.
- `TestUI_BoardView_ExportOversize_Chunked` — Chunked.

---

#### 10.5 — Conversation context (session board view)

**Description**: Per §14.12. The conversation IS the session board.
Prior turns visible as prior actions with their testaments. No lost
context between turns.

**Implementation approach**:
- Package: `ui/chat/`.
- Conversation timeline = chronological action list from session
  board (PromptActions + system actions + testament responses).
- Lazy load older turns on scroll.
- Search across all turns.
- Cross-link from current turn → past relevant turns via Relation
  traversal (`refines`, `derived_from`).
- Code path: `ui/chat/conversation.go` (~700 LOC).

**Acceptance criteria**:
- Timeline shows all session actions chronologically.
- Lazy load on scroll.
- Search works across all turns.
- Cross-links navigable.
- No lost context (regression test for the conversation-history-loss
  bug).
- Bounded memory (only N turns kept hot; older swapped out).

**Unit tests**:
- `TestUI_Conversation_BoardBacked` — Timeline from board.
- `TestUI_Conversation_PriorTurnsVisible` — Past turns appear.
- `TestUI_Conversation_LazyLoad` — Lazy loading works.
- `TestUI_Conversation_SearchAcrossTurns` — Search.
- `TestUI_Conversation_CrossLinkRelations` — Relation links.
- `TestUI_Conversation_BoundedMemory` — Bounded.
- `TestUI_Conversation_HistoryLossBugFixed` — Regression test.

**Integration tests**:
- `TestUI_Conversation_NoLostContext` — Multi-turn; context
  preserved.
- `TestUI_Conversation_HandoffPreservesUI` — Agent handoff; UI
  doesn't lose state.

**End-to-end tests**:
- `TestUI_ConversationE2E_LongSession` — Long session; performance
  + correctness.

**Race condition tests**:
- `TestUI_Conversation_ConcurrentTurnInsertion` — `go test -race`.
- `TestUI_Conversation_LazyLoadDuringScroll` — Race.

**Negative / non-happy path tests**:
- `TestUI_Conversation_BoardUnavailable_StaleViewWithMarker` —
  Stale marker.
- `TestUI_Conversation_PathologicalSessionLength_Pagination` —
  Paginated.
- `TestUI_Conversation_RelationLinkBroken_RenderedAsTombstone` —
  Visible.
- `TestUI_Conversation_OversizedSearchResult_Bounded` — Bounded.
- `TestUI_Conversation_TerminalResize_TimelineReflows` — Resize.

---

### Phase 11 — Boot and Lifecycle

Per §14.13. Boot, agent activation, and shutdown all flow through the
claims board.

**Phase implementation overview**: Phase 11 makes process lifecycle
(boot, agent spawning, graceful shutdown) claim-driven. Boot phases
become claims; activation and termination are claims; cluster
operations have full audit trail of every lifecycle transition.
Common pattern: every lifecycle event has a claim with validations
that gate progression; testaments carry artifacts proving the
transition completed. Common dependencies: `core/boot/` (existing
boot pipeline), `core/container/` (agent activation runtime).

#### 11.1 — Boot as claims

**Description**: Per §14.13. Boot pipeline phases (setup → detect →
allocate → ingest → commit → finalize) become claims on a dedicated
boot board. Each phase submits a testament with success artifacts.
Boot completes when all claims are accepted.

**Implementation approach**:
- Package: `core/boot/`.
- New `BootBoard` instance (per process boot); separate from session
  boards.
- Boot phases as ordered claims with `causality=happens-after` (per
  §27.7) so each phase awaits prior acceptance.
- Phase claims:
  - `setup`: subject = boot orchestrator; validations = config
    loaded, paths exist, permissions OK.
  - `detect`: validations = environment detected, decisions
    claimed.
  - `allocate`: validations = required services up, ports bound,
    DB schemas migrated.
  - `ingest`: validations = knowledge stack populated, indexes
    fresh.
  - `commit`: validations = all sovereign stores recovered.
  - `finalize`: validations = system ready, listeners up.
- Boot board persists per-process (replayable on crash).
- BootBoard amplifier emits Fabric activities so external
  observers (TUI, CLI status) can see boot progress.
- Code path: `core/boot/boot_claims.go` (~2500 LOC).
- Hard part: bootstrapping. The claims subsystem itself depends
  on `setup` having completed. Solved by scaffolding a minimal
  in-memory "pre-claims" boot phase that initialises
  `core/claims/` itself; subsequent phases use the real claims
  board.

**Acceptance criteria**:
- All 6 phases as ordered claims with causal dependencies.
- Each phase has 3-5 validations.
- Phases progress only after prior phase accepted.
- Phase failure halts boot; testament with error artifacts.
- Boot replay on crash: re-issues unaccepted phases; preserves
  accepted ones.
- TUI / CLI shows boot progress via amplifier output.
- Bootstrap of claims subsystem itself is bounded in time (≤ 200ms)
  via pre-claims minimal scaffold.
- Boot board retained per process for forensic analysis (not GC'd
  until process exit).

**Unit tests**:
- `TestBoot_PhasesAsClaims` — 6 phase claims posted.
- `TestBoot_PhaseTestaments` — Each phase produces testament.
- `TestBoot_PhaseValidationsCount` — 3-5 validations per phase.
- `TestBoot_CausalDependency_PhasesOrdered` — Phase N awaits N-1.
- `TestBoot_BootCompleteOnAccept` — All accepted → boot done.
- `TestBoot_PhaseFailureHalts` — Failure halts subsequent.
- `TestBoot_PreClaimsScaffold` — Bootstrap completes ≤ 200ms.
- `TestBoot_BootBoardForensicRetention` — Board retained.

**Integration tests**:
- `TestBoot_FullBootCycle` — End-to-end successful boot.
- `TestBoot_AmplifierEmissions` — TUI receives progress events.
- `TestBoot_PhasePerformance` — Each phase within budget.

**End-to-end tests**:
- `TestBootE2E_RealBoot` — Real cold boot.
- `TestBootE2E_WarmBoot` — Boot with prior state.

**Race condition tests**:
- `TestBoot_PhaseDispatchSerialization` — `go test -race` strictly
  serialised phases.
- `TestBoot_AmplifierConcurrentEmission` — Race.

**Negative / non-happy path tests**:
- `TestBoot_PhaseFailure_BootHalts` — Halt with error artifact.
- `TestBoot_RecoveryFromCrashedBoot` — Replay from crashed phase.
- `TestBoot_AllocatePortBindFails_ErrorArtifact` — Port conflict.
- `TestBoot_DetectMissingConfig_ActionableError` — Helpful error.
- `TestBoot_IngestPartialFailure_ContinuesOrHaltsPerPolicy` —
  Configurable.
- `TestBoot_BootBoardCorrupted_FailsClearlyAtPreClaims` — Bootstrap
  fails clearly.
- `TestBoot_PhaseTimeoutExceeded_HaltsWithDiagnostic` — Timeout
  diagnostic.
- `TestBoot_ConcurrentBootsRefused` — Only one boot at a time per
  process.

---

#### 11.2 — Agent activation as claims

**Description**: Per §14.13. Agent activation is a claim
("Activate engineer for session X"); the container responds with a
testament containing the agent ID and readiness status.

**Implementation approach**:
- Package: `core/container/`.
- Activation flow: caller posts claim
  `Activate{agent_type, session_id, options}`; subject = container.
- Container subscribes to activation claims; spawns the agent;
  submits testament with `kind=agent_id` artifact + readiness
  status.
- Validations: "Agent is responsive", "Capability bindings
  loaded", "Subscriptions active".
- Activation claim retains for audit (didn't immediately GC) — full
  history of when each agent was activated, by whom, with what
  options.
- Code path: `core/container/activation_claims.go` (~1000 LOC).

**Acceptance criteria**:
- Activation requests are claims.
- Container responds with testament + agent_id artifact.
- 3 validations enforced.
- Activation latency ≤ 500ms typical.
- Activation history queryable.
- Authorization: only authorized callers can activate.
- Idempotent: re-activation request for same `(agent_type, session)`
  returns existing agent or creates new per policy.

**Unit tests**:
- `TestActivation_AsClaim` — Claim shape correct.
- `TestActivation_TestamentReadiness` — Testament structure.
- `TestActivation_AgentIDArtifact` — Artifact present.
- `TestActivation_3Validations` — All 3.
- `TestActivation_LatencyBound` — ≤ 500ms.
- `TestActivation_HistoryQueryable` — Past activations queryable.
- `TestActivation_Authorization` — Unauthorized rejected.
- `TestActivation_IdempotentRequest` — Duplicate handled.

**Integration tests**:
- `TestActivation_RealActivation` — Real container spawns agent.
- `TestActivation_MultipleAgentsParallel` — Parallel activation.

**End-to-end tests**:
- `TestActivationE2E_FullSession` — Activation → work → completion.

**Race condition tests**:
- `TestActivation_ConcurrentSameAgentType` — `go test -race`.
- `TestActivation_ContainerRestartDuringActivation` — Race.

**Negative / non-happy path tests**:
- `TestActivation_AgentSpawnFails_ErrorArtifact` — Captured.
- `TestActivation_ContainerOOM_ActivationRejected` — Backpressure.
- `TestActivation_AgentBinaryMissing_ErrorArtifact` — Missing
  binary.
- `TestActivation_CapabilityBindingsFail_ActivationHalted` —
  Capability error.
- `TestActivation_NonExistentAgentType_Refused` — Unknown type.
- `TestActivation_QuotaExceededForTenant_Rejected` — §22.1 quota.
- `TestActivation_ActivationTimeout_AgentTerminated` — Stuck
  spawn.

---

#### 11.3 — Shutdown as claims

**Description**: Per §14.13. Graceful shutdown issues claims against
each active agent: "Persist state and terminate". Agents respond
with shutdown testaments.

**Implementation approach**:
- Package: `core/container/`.
- Shutdown coordinator posts claims to all active agents (one per
  agent).
- Each claim has validations: "Agent persists state",
  "Agent extracts archivable state", "Agent terminates within
  budget".
- Agents respond with testaments carrying `archivable_state`
  + final-status artifacts.
- Shutdown coordinator awaits all testaments OR timeout; on timeout,
  forces termination + records forced-shutdown artifact.
- Code path: `core/container/shutdown_claims.go` (~800 LOC).

**Acceptance criteria**:
- Shutdown issues one claim per active agent.
- Validations: state persistence, archivable extraction,
  termination.
- Agents respond with testaments + artifacts.
- Timeout → forced termination + artifact.
- Shutdown audit trail: every shutdown traceable.
- Shutdown ordering respects dependencies (e.g., scribe shuts
  down after its parent).

**Unit tests**:
- `TestShutdown_ClaimsIssued` — Per-agent claim.
- `TestShutdown_AgentTestaments` — Testaments collected.
- `TestShutdown_3Validations` — All 3 enforced.
- `TestShutdown_ArchivableStateArtifact` — Artifact present.
- `TestShutdown_ForcedTerminationOnTimeout` — Force after timeout.
- `TestShutdown_AuditTrail` — Trail queryable.
- `TestShutdown_DependencyOrdering` — Order respected.

**Integration tests**:
- `TestShutdown_RealShutdownCycle` — End-to-end shutdown.
- `TestShutdown_PartialShutdownThenAbort` — Cancel mid-shutdown.

**End-to-end tests**:
- `TestShutdownE2E_FullCluster` — Multi-agent cluster shutdown.

**Race condition tests**:
- `TestShutdown_ConcurrentShutdownRequests` — `go test -race`;
  serialised.
- `TestShutdown_AgentDeathDuringShutdown` — Race.
- `TestShutdown_NewActivationDuringShutdown_Refused` — Refused.

**Negative / non-happy path tests**:
- `TestShutdown_AgentRefusesShutdown_ForcedAfterTimeout` — Force.
- `TestShutdown_AgentCrashesDuringPersist_PartialStateArtifact` —
  Partial.
- `TestShutdown_ArchivableExtractionFails_BestEffortArtifact` —
  Best-effort.
- `TestShutdown_DependencyAgentDown_ShutdownProceedsWithWarning` —
  Continue with warning.
- `TestShutdown_ShutdownInterrupted_ResumesOnNextRequest` —
  Resumable.
- `TestShutdown_OperatorCancelMidShutdown_AgentsRecover` —
  Cancellable.

---

### Phase 12 — Persistence and Reasoning Infrastructure

Memory Forest, Knowledge Graph, Document DB, Fabric, Bleve. Per
§14.15.

**Phase implementation overview**: Phase 12 makes the persistence /
reasoning systems claim-aware. The Memory Forest harvests claims
(structured precedents). The Knowledge Graph nodes become claims /
testaments / artifacts. The Document DB indexes testament content.
Bleve indexes are claim-faceted. Fabric simplifies to one
amplifier path (the claims board).

#### 12.1 — Memory Forest

**Description**: Per §14.15 #12a. Memory Forest harvest shifts from
raw Fabric activities to *accepted claims with full testament + artifact
chains*. Each accepted claim becomes a forest branch carrying the
claim's description, testament summary, all artifact references,
validation verdicts, and quality bars. Rejected claims become
anti-precedents.

**Implementation approach**:
- Package: `core/forest/`.
- Subscribe to `ActionClaimAccepted` and `ActionClaimRejected`
  events from the claims subject (or pre-substrate amplifier output).
- Harvest pipeline:
  - Resolve full claim → testament(s) → artifact chain via Relation
    traversal.
  - Construct forest branch with: claim description (embedded),
    testament summary (embedded), per-artifact metadata, validation
    verdicts, quality bars, status history.
  - Persist branch + leaves (artifacts) to forest backend.
- Skills (`forest_recall`, `forest_resolve_intent`,
  `forest_predict_next_branches`): return claim chains, not raw
  decision records.
- Rejection flow: harvested as anti-precedent branch with
  `kind=rejected_claim`; rejection reason from StatusHistory.
- Cross-session lineage: when a new claim is similar (semantic
  retrieval) to a prior accepted claim, surface the prior chain as
  precedent.
- Code path: `core/forest/claims_harvester.go` (~2500 LOC) +
  modifications to existing forest skills (~1000 LOC).

**Acceptance criteria**:
- Forest harvests `claim_accepted` and `claim_rejected` events.
- Each branch carries full claim → testament → artifact chain.
- Anti-precedents (rejected) preserve rejection reason + failing
  validation verdicts.
- Recall returns structured chains, not opaque records.
- Cross-session lineage works (semantic match across sessions).
- Bounded harvest rate; backpressure on storage backend.
- Forest determinism: same input event → same branch.
- Forest invariants from CLUSTER.md §11.5 preserved.

**Unit tests**:
- `TestForest_HarvestAcceptedClaim` — Branch correctly populated.
- `TestForest_HarvestRejectedAsAntiPrecedent` — Rejection captured.
- `TestForest_RecallReturnsClaimChain` — Recall structured.
- `TestForest_CrossSessionLineage` — Cross-session match.
- `TestForest_BranchInvariants` — Invariants enforced.
- `TestForest_HarvestDeterministic` — Same input → same output.
- `TestForest_BranchSize_Bounded` — Bounded.
- `TestForest_ValidationVerdictsPreserved` — Verdicts captured.

**Integration tests**:
- `TestForest_RealisticHarvestingFlow` — Real claim flow → forest
  branch.
- `TestForest_LongHistoryHarvest` — Many accepted claims; forest
  populated correctly.
- `TestForest_PrecedentSemanticMatch` — Semantic retrieval across
  sessions.

**End-to-end tests**:
- `TestForestE2E_PrecedentRetrieval` — Engineer issues claim;
  precedent surfaced.
- `TestForestE2E_AntiPrecedentBlocksRepetition` — Prior failure
  surfaces as warning.

**Race condition tests**:
- `TestForest_ConcurrentHarvest` — `go test -race` parallel
  harvesting.
- `TestForest_BranchUpdateDuringRecall` — Race resolved.
- `TestForest_HarvestDuringRetentionGC` — Race.

**Negative / non-happy path tests**:
- `TestForest_MalformedClaimChain_Skipped` — Bad chain skipped.
- `TestForest_StorageBackendUnavailable_BackpressureBounded` —
  Bounded.
- `TestForest_HarvestQueueOverflow_OldestDropped` — Bounded queue.
- `TestForest_OversizedBranch_Truncated` — Truncation marker.
- `TestForest_StaleBranchPastRetention_Pruned` — Pruning.
- `TestForest_DuplicateHarvest_Idempotent` — Idempotent.
- `TestForest_PathologicalSemanticQuery_BoundedCost` — Bounded.

---

#### 12.2 — Knowledge Graph (VectorGraphDB)

**Description**: Per §14.15 #12b. Knowledge graph gains structured
nodes for every claims-system entity type (claim, testament, artifact,
validation, action). Causal edges from Relations. Validation verdicts
as edge weights.

**Implementation approach**:
- Package: `core/vectorgraphdb/`.
- Nodes:
  - `Claim` node: embedding of description, status, scope, relations.
  - `Testament` node: embedding of summary, link to claim.
  - `Artifact` node (for semantic content kinds): embedding of
    artifact content / metadata.
  - `Action` node: embedding of action description, link to claims.
- Edges from Relations:
  - `supersedes`, `caused_by`, `refines`, `derived_from`,
    `depends_on`, `conflicts_with`, `in_scope_of`, `direct_addressed`,
    `parent_session`, `child_pipelines` — first-class edge types.
- Validation verdict → edge weight: accepted = high-confidence
  (1.0), tentative = medium (0.6), rejected = low (0.1) with
  `failure_reason` metadata.
- Cross-pipeline edges: claims sharing scope entries connected by
  `conflicts_with` or `refines` edges (computed at harvest time).
- Embeddings: existing embedding model; vector dimension matches
  configured model.
- Code path: `core/vectorgraphdb/claims_integration.go` (~3000 LOC)
  + harvester (~1000 LOC).

**Acceptance criteria**:
- Nodes for all listed entity types.
- All Relation kinds are first-class edge types.
- Validation verdicts → edge weights.
- Cross-pipeline edges computed.
- Semantic search returns full chains.
- Embedding model agnostic; works with any backend.
- Bounded memory under high cardinality (graph compaction).

**Unit tests**:
- `TestKG_ClaimAsNode` — Node shape.
- `TestKG_TestamentAsNode`.
- `TestKG_ArtifactAsNode_SemanticOnly` — Only semantic kinds.
- `TestKG_ActionAsNode`.
- `TestKG_RelationAsEdge_AllKinds` — All relations.
- `TestKG_VerdictAsEdgeWeight` — Weights correct.
- `TestKG_CrossPipelineScopeEdges` — Cross-pipeline.
- `TestKG_EmbeddingDimensionMatchesModel` — Dimension.

**Integration tests**:
- `TestKG_FullGraphConstruction` — Realistic graph.
- `TestKG_SemanticSearchOverClaims` — Search returns chains.
- `TestKG_GraphCompactionPreservesChains` — Compaction safe.

**End-to-end tests**:
- `TestKGE2E_PriorWorkRetrieval` — Realistic precedent surfacing.
- `TestKGE2E_CrossPipelineConflictDetection` — Conflicts via edges.

**Race condition tests**:
- `TestKG_ConcurrentNodeInsertion` — `go test -race`.
- `TestKG_EdgeAdditionRaceWithSearch` — Race.
- `TestKG_CompactionRaceWithInsertion` — Race.

**Negative / non-happy path tests**:
- `TestKG_OrphanedNode_GC` — Orphans reaped.
- `TestKG_EmbeddingFailure_ErrorArtifact` — Captured.
- `TestKG_DuplicateInsertion_Idempotent` — Dedup.
- `TestKG_BrokenEdgeReference_RenderedWithMarker` — Broken edge.
- `TestKG_OversizedEmbedding_Rejected` — Bounded.
- `TestKG_GraphCorruption_DetectedAndQuarantined` — Detection.
- `TestKG_StorageBackendUnavailable_BackpressureBounded` —
  Backpressure.

---

#### 12.3 — Document DB

**Description**: Per §14.15 #12c. Document DB stores full-text
searchable documents anchored to specific claims and testaments.
Testaments with non-trivial summaries become documents; artifacts as
attachments; claim-scoped retrieval.

**Implementation approach**:
- Package: `core/knowledge/`.
- Documents:
  - Each testament with `len(summary) > threshold` becomes a
    document.
  - Document carries: testament ID, claim reference, artifact list,
    full Relation chain.
- Attachments: artifact references indexed as document attachments
  (searchable by artifact reference / kind).
- Retrieval:
  - "Show me all documents related to claim X" → document + its
    artifacts + Scribe narrations during the claim + Archivalist
    entries.
  - Full-text search over testament summaries.
- Ingestion receipts as artifacts: Archivalist's ingestion produces
  artifact on Scribe's testament; document DB entry links back to
  generating claim via artifact → testament → claim chain.
- Code path: `core/knowledge/claims_documents.go` (~1800 LOC) +
  refactor of existing knowledge interfaces (~700 LOC).

**Acceptance criteria**:
- Testaments → documents (per threshold).
- Artifact references indexed.
- Claim-scoped retrieval works.
- Document → claim chain queryable.
- Full-text search over testament summaries.
- Ingestion receipt artifacts link back to claims.
- Bounded document size; oversized chunked.

**Unit tests**:
- `TestDocDB_TestamentAsDocument` — Conversion.
- `TestDocDB_ThresholdRespected` — Threshold.
- `TestDocDB_ArtifactAsAttachment` — Indexing.
- `TestDocDB_ClaimScopedRetrieval` — Retrieval.
- `TestDocDB_DocumentToClaimChain` — Chain.
- `TestDocDB_FullTextSearch` — Search.
- `TestDocDB_IngestionReceiptLinksToClaim` — Receipt linkage.

**Integration tests**:
- `TestDocDB_FullIndexFlow` — End-to-end indexing.
- `TestDocDB_RealisticSearch` — Real queries.
- `TestDocDB_CrossClaimDiscovery` — Discovery via search.

**End-to-end tests**:
- `TestDocDBE2E_ClaimRetrieval` — Realistic retrieval scenario.
- `TestDocDBE2E_AuditQuery` — Auditor query → full doc chain.

**Race condition tests**:
- `TestDocDB_ConcurrentDocumentInsertion` — `go test -race`.
- `TestDocDB_SearchDuringIndexUpdate` — Race.

**Negative / non-happy path tests**:
- `TestDocDB_OversizedDocument_ChunkedOrRejected` — Bounded.
- `TestDocDB_StorageUnavailable_BackpressureBounded` — Backpressure.
- `TestDocDB_OrphanedAttachment_GC` — GC.
- `TestDocDB_BrokenChainReference_RenderedAsTombstone` — Broken
  link.
- `TestDocDB_DuplicateDocument_Idempotent` — Dedup.
- `TestDocDB_StaleDocumentPastRetention_Pruned` — Retention.
- `TestDocDB_SearchEmptyResult_HelpfulMessage` — Empty.

---

#### 12.4 — Fabric

**Description**: Per §14.15 #12d. The Fabric simplifies from "observe
3+ sovereign systems" to "observe one — the claims board." Activity
→ claim mapping; causal chains from Relations; ambient context = board
digest; lens queries = board queries.

**Implementation approach**:
- Package: `core/activity/`, `core/fabric/`.
- Single amplifier: the claims board amplifier (Phase 0.4 for
  pre-substrate; substrate consumer for Phase 13).
- Activity → claim mapping: every Fabric ActionKind maps to a claims
  entity (claim_issued → Claim; testament_submitted → Testament;
  etc.).
- Causal chain: walk Relations on entities directly; no parallel
  `Caused`/`Resolves` pointers.
- Ambient context: read directly from claims board projection (or
  Phase 13 continuous query); not merged from multiple sources.
- Lens queries: `query_peer_activity` supplemented by
  `query_claims_board` filtered by peer.
- Resolution tiers preserved: claim progress (Medium) evicts faster
  than accepted claims (Coarse).
- Chokepoint instrumentation simplified: 1 amplifier vs 6+ today;
  chokepoints emit infrastructure events (LLM calls, file writes,
  command exec) but semantic events flow through claims.
- Code path: `core/activity/` + `core/fabric/` (~3500 LOC modified;
  ~2000 LOC removed).

**Acceptance criteria**:
- Single amplifier.
- Mapping deterministic (per §9.1).
- Causal chains computed from Relations only.
- Ambient context single-source.
- Lens layer thinned (multi-amplifier merging removed).
- Resolution tiers respected.
- Chokepoints continue emitting infrastructure events; not
  duplicated for semantic events.
- All existing Fabric consumers (TUI, CLI, agents) work unchanged.

**Unit tests**:
- `TestFabric_OneAmplifier` — Single source.
- `TestFabric_ActivityToClaimMapping` — Each kind correct.
- `TestFabric_CausalFromRelations` — No `Caused`/`Resolves` paths.
- `TestFabric_AmbientFromBoard` — Single-source ambient.
- `TestFabric_LensQueriesViaBoard` — `query_peer_activity` works.
- `TestFabric_ResolutionTiersPreserved` — Tiers respected.
- `TestFabric_InfrastructureEventsStillEmit` — LLM / IO chokepoints
  still emit.

**Integration tests**:
- `TestFabric_LensQueriesVsBoardQueries` — Equivalent results.
- `TestFabric_ConsumerCompatibility` — Existing consumers work.
- `TestFabric_AmplifierRetirement` — Other amplifiers gone.

**End-to-end tests**:
- `TestFabricE2E_FullSession` — Real session; activities flow
  through one path.
- `TestFabricE2E_LensVisibility` — TUI / agents see activities.

**Race condition tests**:
- `TestFabric_ConcurrentAmplifierEvents` — `go test -race`.
- `TestFabric_ConsumerSubscriptionRace` — Race.

**Negative / non-happy path tests**:
- `TestFabric_LegacyAmplifierCalls_FlaggedAtCompile` — Build catches.
- `TestFabric_MalformedActivityDropped_Logged` — Bad event.
- `TestFabric_AmplifierBackpressure_BoundedDegradation` —
  Backpressure.
- `TestFabric_ResolutionMisconfigured_DefaultApplied` — Fallback.
- `TestFabric_ChokepointMissing_ActivityNotEmitted_Detected` —
  Detection.

---

#### 12.5 — Bleve

**Description**: Per §14.15 #12e. Bleve full-text index covers claim
descriptions, testament summaries, validation descriptions, quality
bars, artifact references. Faceted search by entity type.

**Implementation approach**:
- Package: `core/bleve/` (or wherever Bleve subscribers live).
- Documents indexed:
  - Claims: description, scope entries, validations.
  - Testaments: summary.
  - Validations: description, quality bar.
  - Artifacts: reference + kind.
- Facets: entity type (claim / testament / validation / artifact /
  action), agent, status, scope, time bucket.
- Live indexing via subscription to claims subject (or amplifier
  in pre-substrate path).
- Index recovery: on corruption, rebuild from claims-board WAL +
  current state.
- Bounded index size; old entries past retention pruned.
- Code path: `core/bleve/claims_index.go` (~1500 LOC).

**Acceptance criteria**:
- All listed fields indexed.
- Faceted search per listed facets.
- Live updates within 100ms of board mutation.
- Search latency ≤ 50ms p99 on 100K-claim corpus.
- Index recovery from corruption.
- Bounded size.

**Unit tests**:
- `TestBleve_ClaimDescIndexed` — Claim description searchable.
- `TestBleve_TestamentSummaryIndexed` — Summary searchable.
- `TestBleve_ValidationDescIndexed` — Validation searchable.
- `TestBleve_ArtifactRefIndexed` — Artifact searchable.
- `TestBleve_FacetedSearch_AllFacets` — All facets.
- `TestBleve_LiveUpdates` — < 100ms latency.
- `TestBleve_SearchLatencyBound` — < 50ms p99.

**Integration tests**:
- `TestBleve_RealisticSearchQueries` — Realistic corpus.
- `TestBleve_FacetCombinations` — Multi-facet queries.

**End-to-end tests**:
- `TestBleveE2E_RealSearch` — Real session search.

**Race condition tests**:
- `TestBleve_ConcurrentIndexAndSearch` — `go test -race`.
- `TestBleve_IndexRebuildRaceWithUpdates` — Race.

**Negative / non-happy path tests**:
- `TestBleve_IndexCorruption_RebuildAtBoot` — Recovery.
- `TestBleve_OversizedDocument_Truncated` — Bounded.
- `TestBleve_PathologicalQuery_BoundedTime` — Bounded.
- `TestBleve_StorageUnavailable_DegradesToReadOnly` — Read-only
  fallback.
- `TestBleve_DuplicateIndexEntry_Idempotent` — Dedup.
- `TestBleve_StaleEntriesPastRetention_Pruned` — Pruning.

---

### Phase 13 — Substrate Integration

**What**: Migrate the entire claims system from the bespoke sovereign
store + WAL + amplifier (Phases 0-12) onto the docs/CLUSTER.md
substrate. Same semantics, substrate-grade durability, replication,
encryption, audit, federation.

**Phase implementation overview**: Phase 13 is the final phase. By
this point, all of Phases 0-12 have shipped on the bespoke
implementation; the claims system works end-to-end. Phase 13 keeps
all observable semantics identical while replacing the underlying
machinery: Sylk's `core/claims/board_durable.go` becomes a substrate
subject; the amplifier becomes a substrate consumer; the Fabric
projection rides on substrate continuous queries; serialization
becomes SWF; transport becomes adaptive multi-stack. This is a
migration phase — every item is dual-write / shadow-read / cutover.

#### 13.1 — Claims subject schema registration

**Description**: Register `sylk://session/<id>/claims/v3` as a
substrate subject (CLUSTER.md §11.4) with full schema, codegen, dict
training (CLUSTER.md §25.5), session-type grammar (CLUSTER.md §31.21).

**Implementation approach**:
- Define schema in substrate schema language for all 5 entity types
  (claim, testament, artifact, validation, action).
- Register at substrate boot; trigger SWF codegen.
- Train per-schema zstd dict on representative corpus.
- Declare session-type grammar covering claim → testament →
  validation → accept/reject.
- Authority predicates: only authorized agents publish per their
  capabilities.
- Code path: `core/substrate/schemas/claims/` (~1500 LOC).

**Acceptance criteria**:
- Schema registered; codegen produces SWF encoders/decoders.
- Dict trained; compression ratio ≥ 10× on real claim corpus.
- Session-type grammar enforces claim lifecycle order.
- Authority predicates registered.
- Substrate-side validation rejects malformed entries.

**Unit tests**:
- `TestClaimsSchema_Registered`.
- `TestClaimsSchema_SWFCodegenProduces` — codegen output present.
- `TestClaimsSchema_DictTrained`.
- `TestClaimsSchema_SessionTypeRejectsOutOfOrder`.
- `TestClaimsSchema_AuthorityPredicate`.

**Integration tests**:
- `TestClaimsSchema_RoundTripViaSubstrate` — Publish → consume → equal.
- `TestClaimsSchema_DictCompressionRatio` — ≥ 10×.

**End-to-end tests**:
- `TestClaimsSchemaE2E_RealClaimsWorkflow`.

**Race condition tests**:
- `TestClaimsSchema_ConcurrentRegistrations` — `go test -race`.

**Negative / non-happy path tests**:
- `TestClaimsSchema_MalformedEntry_RejectedAtPublish`.
- `TestClaimsSchema_SessionTypeViolation_Rejected`.
- `TestClaimsSchema_UnauthorizedPublisher_Rejected`.
- `TestClaimsSchema_DictDriftDetected_Retrained`.

---

#### 13.2 — Migrate ClaimsBoard to substrate state machine

**Description**: Replace `core/claims/board.go` in-memory
implementation with substrate-backed state machine on `claims/v3`
subject.

**Implementation approach**:
- New `BoardSM` implements substrate SM interface (CLUSTER.md §24.2).
- Apply path: substrate entry → state mutation in Raft state machine.
- Reads: substrate cursor or Raft read-index.
- Existing `core/claims/board.go` becomes a thin wrapper over BoardSM
  during cutover.
- Dual-write window: writes go to both old WAL and substrate.
- Shadow-read verification: every old-WAL read also performed against
  substrate; mismatch logged.
- Cutover: 7-day zero-mismatch verification.
- Code path: `core/substrate/sm/claims/` (~3000 LOC) +
  `core/claims/board_substrate.go` (~800 LOC bridge).

**Acceptance criteria**:
- BoardSM implements SM interface; deterministic per CLUSTER.md §24.1.
- Dual-write maintains parity.
- Shadow-read shows zero mismatches over 7-day window.
- Cutover removes old WAL path; no regression.

**Unit tests**:
- `TestBoardSM_Apply` — Each entry kind applies correctly.
- `TestBoardSM_Deterministic` — Bit-equal across replicas.
- `TestBoardSM_Snapshot` — State snapshot/restore.
- `TestBoardBridge_DualWrite`.
- `TestBoardBridge_ShadowReadParity`.

**Integration tests**:
- `TestBoardSM_FullLifecycle` — End-to-end through SM.
- `TestBoardBridge_LongRunningParity` — 24h dual-write parity.

**End-to-end tests**:
- `TestBoardSME2E_RealClusterReplication`.
- `TestBoardSME2E_CutoverFlow`.

**Race condition tests**:
- `TestBoardSM_ConcurrentApply` — `go test -race`.

**Negative / non-happy path tests**:
- `TestBoardSM_DivergenceDetected_QuarantinePerCLUSTER` —
  CLUSTER.md §24.3.
- `TestBoardSM_ApplyFailure_RolledBack`.
- `TestBoardBridge_MismatchDetected_LoggedAndAlerted`.
- `TestBoardBridge_CutoverRollbackPath`.

---

#### 13.3 — Replace amplifier with substrate consumer

**Description**: Retire `board_amplifier.go`. Fabric becomes a
substrate consumer reading the claims subject directly (CLUSTER.md
§11.5).

**Implementation approach**:
- Remove `core/claims/board_amplifier.go`.
- New `core/fabric/claims_consumer.go`: substrate consumer
  subscribing to claims subject; emits Fabric activities.
- Causal chain populated from substrate parent edges (CLUSTER.md
  §7.1) instead of in-process Relations.
- Code path: `core/fabric/claims_consumer.go` (~1000 LOC) net
  positive after amplifier removal.

**Acceptance criteria**:
- Amplifier file gone.
- Fabric activity stream identical (or equivalent) to pre-cutover.
- Substrate parent edges drive causal chain.

**Unit tests**:
- `TestClaimsConsumer_SubstrateSubscription`.
- `TestClaimsConsumer_FabricActivityEmission`.
- `TestClaimsConsumer_CausalChainFromSubstrate`.

**Integration tests**:
- `TestClaimsConsumer_FullLensVisibility`.

**End-to-end tests**:
- `TestClaimsConsumerE2E_FabricUnchangedFromConsumer`.

**Race condition tests**:
- `TestClaimsConsumer_ConcurrentEvents` — `go test -race`.

**Negative / non-happy path tests**:
- `TestClaimsConsumer_SubstrateDown_DegradedDelivery` — Fabric
  degrades gracefully.
- `TestClaimsConsumer_LegacyAmplifierFile_Absent`.

---

#### 13.4 — Continuous-query ambient digest

**Description**: ClaimsBoardDigest computed via CLUSTER.md §31.4
differential dataflow over claims subject + sibling subjects (forest
events, fabric activity, agent log).

**Implementation approach**:
- Define dataflow plan: subscribe inputs → joins → aggregations →
  digest output.
- Subscribers (TUI, agents) consume digest as substrate subscription.
- Bounded staleness configurable.
- Code path: `core/fabric/digest_dataflow.go` (~1500 LOC).

**Acceptance criteria**:
- Digest equivalent to pre-cutover content.
- Update latency ≤ 100ms typical.
- Bounded memory.
- Bounded staleness.

**Unit tests**:
- `TestDigestDataflow_PlanCompiles`.
- `TestDigestDataflow_DeltaPropagation`.
- `TestDigestDataflow_BoundedMemory`.
- `TestDigestDataflow_StalenessBound`.

**Integration tests**:
- `TestDigestDataflow_RealisticBoard`.

**End-to-end tests**:
- `TestDigestDataflowE2E_LiveAgentDigest`.

**Race condition tests**:
- `TestDigestDataflow_ConcurrentSubscribers` — `go test -race`.

**Negative / non-happy path tests**:
- `TestDigestDataflow_PathologicalQuery_Bounded`.
- `TestDigestDataflow_SubstrateSubjectDeleted_FailsCleanly`.

---

#### 13.5 — Wire migration to SWF

**Description**: Claims serialization migrates from JSON (Phase 0)
to SWF (CLUSTER.md §4.4-bis).

**Implementation approach**:
- SWF codegen at schema registration (Phase 13.1) produces encoders.
- Wire path uses SWF; on-disk WAL during cutover migrates to SWF
  format too.
- Backward-compat reader: old JSON entries readable until horizon-
  compacted (CLUSTER.md §21.5).
- Code path: minimal — codegen does the work.

**Acceptance criteria**:
- Wire bytes are SWF.
- Old JSON readable.
- Determinism harness validates byte-equality across replicas.

**Unit tests**:
- `TestSWFMigration_WireBytesSWF`.
- `TestSWFMigration_OldJSONReadable`.
- `TestSWFMigration_DeterminismHarness`.

**Integration tests**:
- `TestSWFMigration_FullRoundTrip`.

**End-to-end tests**:
- `TestSWFMigrationE2E_NoBytesLost`.

**Race condition tests**:
- `TestSWFMigration_ConcurrentEncode` — `go test -race`.

**Negative / non-happy path tests**:
- `TestSWFMigration_OldJSONCorrupted_DetectedAtRead`.
- `TestSWFMigration_VersionedSchemaCoexist`.

---

#### 13.6 — Adaptive transport routing for claims

**Description**: Claims frames route via CLUSTER.md §4.5 adaptive
transport selection.

**Implementation approach**:
- Critical-class entries (claim_accepted, claim_rejected): QUIC
  datagram or stream.
- Standard-class (most others): QUIC stream + zstd dict.
- Bulk (artifact attachments > 1KB): QUIC stream + schema dict.
- Multipath for Critical (CLUSTER.md §25.2).
- Code path: configuration only; transport is in CLUSTER.md.

**Acceptance criteria**:
- Class assigned per entry kind.
- Adaptive transport selects correctly.
- Multipath for Critical.

**Unit tests**:
- `TestClaimsTransport_ClassAssignment`.
- `TestClaimsTransport_AdaptiveSelection`.
- `TestClaimsTransport_MultipathCritical`.

**Integration tests**:
- `TestClaimsTransport_RealisticTraffic`.

**End-to-end tests**:
- `TestClaimsTransportE2E_LowLatencyAcceptance`.

**Race condition tests**: covered by CLUSTER.md.

**Negative / non-happy path tests**:
- `TestClaimsTransport_PathFailoverCrtical` — Critical falls over.

---

#### 13.7 — Per-tenant encryption envelope

**Description**: Claims subjects encrypted via CLUSTER.md §21.2
per-tenant DEK envelope.

**Implementation approach**:
- Subject schema declares `encryption=tenant-DEK`.
- Substrate handles envelope via §21.2.
- Code path: schema field; substrate-side enforcement.

**Acceptance criteria**:
- Claims encrypted at rest.
- Per-tenant key isolation.
- Tenant offboard destroys keys (CLUSTER.md §22.4).

**Unit tests**:
- `TestClaimsEncryption_AtRest`.
- `TestClaimsEncryption_PerTenantIsolation`.

**Integration tests**:
- `TestClaimsEncryption_RealKMS`.

**End-to-end tests**:
- `TestClaimsEncryptionE2E_TenantOffboarding`.

**Negative / non-happy path tests**:
- `TestClaimsEncryption_KMSFailure_OperationsHalted`.

---

#### 13.8 — Cryptographic accountability

**Description**: Claims entries signed via CLUSTER.md §17.1; agent
SVID is the issuing key; replicas verify before applying.

**Implementation approach**:
- Substrate-side enforcement of signed-everything.
- Forensic provenance (CLUSTER.md §31.9) accessible via SQL function
  on claims (CLUSTER.md §27.20).
- Code path: configuration; substrate handles.

**Acceptance criteria**:
- Every claim signed by issuer's SVID.
- Replicas verify; tampered rejected.
- Provenance queryable.

**Unit tests**:
- `TestClaimsSig_SignedByIssuer`.
- `TestClaimsSig_TamperRejected`.
- `TestClaimsProvenance_QueryFunction`.

**Integration tests**:
- `TestClaimsSig_FullCluster`.

**End-to-end tests**:
- `TestClaimsSigE2E_AuditTrail`.

**Negative / non-happy path tests**:
- `TestClaimsSig_RevokedSVID_FutureRejected`.
- `TestClaimsSig_TermBoundKeysHonored`.

---

#### 13.9 — Federation cross-publish

**Description**: Cross-cluster claims visibility via CLUSTER.md §20.1.

**Implementation approach**:
- Federation control plane allowlists claim subjects per pair.
- Cross-cluster subscriptions deliver verified claims.
- Code path: configuration.

**Acceptance criteria**:
- Cross-cluster claim visibility.
- Per-pair allowlist enforced.
- Authority verified end-to-end.

**Unit tests**:
- `TestFederatedClaims_CrossClusterDelivery`.
- `TestFederatedClaims_AllowlistEnforced`.

**Integration tests**:
- `TestFederatedClaims_TwoClusters`.

**End-to-end tests**:
- `TestFederatedClaimsE2E_GlobalVisibility`.

**Negative / non-happy path tests**:
- `TestFederatedClaims_DisallowedSubject_Filtered`.
- `TestFederatedClaims_PeerCompromise_BoundedDamage`.

---

#### 13.10 — SQLite-compatible storage for persistence systems

**Description**: Persistence systems (Memory Forest, Knowledge Graph,
Document DB, Bleve indexes) use Sylk's SQLite-compatible engine
(CLUSTER.md §11.8 / §27) — *not* vanilla SQLite, *not* turso.

**Implementation approach**:
- Forest, KG, Doc DB, Bleve back onto `kind=sqlite` substrate
  subjects.
- §27 features used where applicable (CRDT tables for cross-DC
  forest merges; vector+SQL for KG; time-series for activity stream).
- Migration via dual-write / cutover per §13.2 pattern.
- Code path: per-system migration shim (~500-1500 LOC each).

**Acceptance criteria**:
- All persistence systems use Sylk SQLite engine.
- Existing query patterns work unchanged.
- §27 features enabled per-system as applicable.
- No vanilla SQLite or raw turso anywhere.

**Unit tests**:
- `TestPersistenceMigration_Forest`.
- `TestPersistenceMigration_KG`.
- `TestPersistenceMigration_DocDB`.
- `TestPersistenceMigration_Bleve`.

**Integration tests**:
- `TestPersistenceMigration_FullSystemPostMigration`.

**End-to-end tests**:
- `TestPersistenceMigrationE2E_RealisticWorkload`.

**Race condition tests**:
- `TestPersistenceMigration_ConcurrentReadWrite` — `go test -race`.

**Negative / non-happy path tests**:
- `TestPersistenceMigration_MismatchDuringDualWrite_Halts`.
- `TestPersistenceMigration_RollbackPath`.

---

#### 13.11 — Time-travel + audit surface

**Description**: Claims gain `AS OF HLC '...'` via CLUSTER.md §12.1;
audit chain via §17.1; provable audit via §12.3.

**Implementation approach**:
- SQL surface (CLUSTER.md §27.20) on claims tables.
- Audit tooling consumes substrate audit primitives.
- Code path: configuration + audit-tool integration.

**Acceptance criteria**:
- Time-travel queries work on claims.
- Audit chain verifiable end-to-end.
- Tenant-facing audit reports automated.

**Unit tests**:
- `TestClaimsTimeTravel_AsOfHLC`.
- `TestClaimsAudit_ChainVerified`.

**Integration tests**:
- `TestClaimsAudit_RealisticAudit`.

**End-to-end tests**:
- `TestClaimsAuditE2E_TenantReport`.

**Negative / non-happy path tests**:
- `TestClaimsTimeTravel_BeforeRetention_Refused`.
- `TestClaimsAudit_TamperedHistoricalEntry_Detected`.

---

#### 13.12 — Cutover and bespoke retirement

**Description**: After 13.1-13.11 verified for 7 days zero-mismatch,
remove bespoke `core/claims/board_durable.go`, `core/claims/
board_amplifier.go`, custom dispatch paths. Substrate is canonical.

**Implementation approach**:
- Operator gate: 7-day zero mismatches in dual-write.
- Performance regression check: ≤ 10% on any operation.
- Rollback path tested.
- Final removal of legacy files.
- Code path: ~5000 LOC removed.

**Acceptance criteria**:
- Bespoke files gone.
- Substrate canonical.
- No performance regression beyond budget.

**Unit tests**:
- `TestCutover_LegacyFilesGone`.
- `TestCutover_NoOrphanReferences`.

**Integration tests**:
- `TestCutover_PostMigrationFullPipeline`.

**End-to-end tests**:
- `TestCutoverE2E_NoRegressions`.

**Negative / non-happy path tests**:
- `TestCutover_RollbackPath_TestedInStaging`.
- `TestCutover_MismatchAtFinalSweep_Halts`.

---

### Implementation summary table

| Phase | Items | Approximate effort | Dependencies |
|-------|-------|--------------------|--------------|
| 0 — Core claims | 5 | 4 weeks | none |
| 1 — Fabric integration | 4 | 2 weeks | 0 |
| 2 — Pipeline agents | 5 | 8 weeks | 0, 1 |
| 3 — Architect | 5 | 3 weeks | 0, 1, 2 |
| 4 — Guide | 6 | 4 weeks | 0, 1 |
| 5 — Knowledge agents | 3 | 3 weeks | 0, 1 |
| 6 — Infrastructure agents | 2 | 6 weeks | 0, 1, 2 |
| 7 — Orchestrator | 6 | 4 weeks | 0-3 |
| 8 — Sovereign retirement | 3 | 2 weeks | 2, 7 |
| 9 — System infrastructure | 5 | 6 weeks | 0-7 |
| 10 — TUI | 5 | 3 weeks | 0-3 |
| 11 — Boot lifecycle | 3 | 2 weeks | 0-9 |
| 12 — Persistence | 5 | 6 weeks | 0-11 |
| 13 — Substrate integration | 12 | 16 weeks | all prior, plus CLUSTER.md Phases 0-27 |

**Total claims-conversion effort (Phases 0-12)**: ~53 weeks of
focused work, executed in dependency order. **Phase 13 substrate
migration**: +16 weeks; runs in parallel with the substrate
implementation timeline (CLUSTER.md Phases 17-27); strictly
zero-downtime via dual-write / shadow-read / cutover.

**Critical path**: 0 → 1 → 2 (~14 weeks) is the minimum to retire
the pipeline protocol and have a working claims-based pipeline.
Phases 3-12 expand the conversion surface; Phase 13 graduates
everything onto the substrate.

Each phase ships behind a feature flag; phase N's feature flag is
removed once phase N+1 (or later) depends on it irreversibly. This
makes rollback of any single phase possible.

---

## 13. Verification

| Test | What it verifies |
|---|---|
| Unit: Board operations | PostAction, UpdateClaimProgress, SubmitTestaments, EvaluateValidation, RejectClaim |
| Unit: Relations | Relation queries by RelatedType/Relationship, ClaimsByRelation, agent/structural/causal relations |
| Unit: StatusHistory | StatusChange recording on Action, Claim, Validation; transitions carry reason/agent/timestamp |
| Unit: Phase transitions | Testified precondition, re-entry precondition, completion precondition |
| Unit: WAL persistence | Checkpoint, event append, recovery, idempotency |
| Unit: Projection | Counts, action/testament/artifact summaries, all five entity collections |
| Unit: Immutability | Testament and Artifact cannot be mutated after creation; corrections produce new objects with supersedes Relation |
| Unit: Testament model | Testament with multiple artifacts, artifact kind polymorphism, ContentHash integrity |
| Integration: End-to-end pipeline | Architect claims -> board -> subjects work -> testaments -> validation -> complete -> handoff_to_ot |
| Integration: Remediation | Validation fails -> remediation claims -> re-implement -> pass |
| Integration: Bounded iteration | MaxReviewRounds exceeded -> rollback |
| Integration: Consultation | Engineer posts consultation action -> Designer responds with testament |
| Integration: Challenge | Inspector posts challenge action -> Engineer responds with testament |
| Integration: Pipeline-to-global handoff | Board complete -> handoff_to_ot -> MergeDescriptor with claims -> bus handoff message |
| Integration: Global review chain | Bus message -> VFS Copy watch -> inspector audits -> accepts -> tester tests -> tester posts claims -> inspector validates -> disk commit |
| Integration: Global inspector rejection | Inspector rejects -> tester does not run -> rejection to architect -> remediation DAG |
| Integration: Global tester rejection | Inspector accepts -> tester rejects -> tester posts rejection claims -> architect remediates |
| Fabric: Activity emission | Claims, testaments, artifacts all emit correct ActionKinds |
| Fabric: Ambient context | ClaimsBoardDigest shows testaments and artifacts |
| Fabric: Cross-pipeline | Claims/testaments from pipeline A visible to pipeline B |
| Recovery: Crash resilience | Kill mid-mutation -> WAL replay -> consistent state |
| Recovery: Global review restart | Replica crash -> descriptor state returns to auditing -> fresh replica relaunches |

---

## 14. Full System Conversion Plan

Every component, every agent, every interaction converted to claims-based execution. No exceptions.

> **Cross-reference**: this section is the *high-level conversion narrative*.
> The detailed phased implementation plan with explicit acceptance criteria
> and unit/integration/E2E/race/negative test ladders lives in **§12 Phased
> Implementation Plan** (above). Each tier in §14 maps to a phase in §12;
> use §14 for the architectural picture and §12 for the engineering
> contract.

### 14.1 Conversion Tiers

The conversion is structured in dependency order. Each tier builds on the prior tier. No tier is optional.

```
Tier 0:  Core claims infrastructure (types, board, WAL, amplifier)        → §12 Phase 0
Tier 1:  Fabric integration (ActionKinds, ambient context, lenses)        → §12 Phase 1
Tier 2:  Pipeline agent conversion (Inspector, Tester, Engineer, Designer)→ §12 Phase 2
Tier 3:  Architect conversion (claim generation replaces task generation) → §12 Phase 3
Tier 4:  Guide conversion (routing becomes action dispatch)               → §12 Phase 4
Tier 5:  Knowledge agent conversion (Librarian, Academic, Archivalist)    → §12 Phase 5
Tier 6:  Infrastructure agent conversion (Scribe, Guardian)               → §12 Phase 6
Tier 7:  Orchestrator conversion (DAG nodes become actions)               → §12 Phase 7
Tier 8:  Sovereign system retirement                                       → §12 Phase 8
Tier 9:  System infrastructure conversion (handoff, session, VFS, errors) → §12 Phase 9
Tier 10: TUI conversion (render claims/testaments/artifacts)              → §12 Phase 10
Tier 11: Boot and lifecycle conversion                                    → §12 Phase 11
Tier 12: Persistence and reasoning infrastructure                         → §12 Phase 12
Tier 13: Substrate integration (graduate onto docs/CLUSTER.md substrate)  → §12 Phase 13
```

Tier 13 is the substrate-graduation tier: after tiers 0-12 ship the
claims system on the bespoke sovereign-store + WAL + amplifier
foundation, tier 13 migrates the entire stack onto the
docs/CLUSTER.md substrate (subjects, multi-Raft, SWF wire format,
adaptive transport, encryption envelope, federation, audit).
Migration is dual-write / shadow-read / cutover; semantics are
preserved.

---

### 14.2 Tier 0: Core Claims Infrastructure

**What**: The foundational types, board, persistence, and amplifier. Everything else depends on this.

**Components:**

| Deliverable | Package | Description |
|---|---|---|
| Claims types | `core/claims/types.go` | Relation, StatusChange, ClaimScopeEntry, Action, Claim, Testament, Artifact, Validation, ClaimProgressUpdate, all enums. The 9 universal base fields + per-type semantic fields. |
| ClaimsBoard | `core/claims/board.go` | Sovereign store: PostAction, SubmitTestaments, EvaluateValidation, RejectClaim, phase transitions, queries, projection, subscription. Flat maps for all 5 entity types. |
| WAL persistence | `core/claims/board_durable.go` | 10 WAL event types, checkpoint struct, apply handlers, recovery via replay. Same `durableProtocolLog` pattern. |
| Board amplifier | `core/claims/board_amplifier.go` | Fabric activity emission for every board mutation. All 12 ActionKinds. |
| Claims skill factories | `core/claims/skills.go` | `query_claims_board`, `post_action`, `submit_testaments`, `update_claim_progress`, `evaluate_validation`, `post_remediation_claims`, `inspect_claim_conflicts`. Shared by all agents. |

**Note:** The package moves from `core/pipeline/claims/` to `core/claims/` — claims are system-wide, not pipeline-specific.

---

### 14.3 Tier 1: Fabric Integration

**What**: The Fabric learns to observe and surface claims, testaments, and artifacts.

| Deliverable | Package | Description |
|---|---|---|
| ActionKind constants | `core/activity/action_kind.go` | 12 new kinds: `claim_issued`, `claim_updated`, `testament_submitted`, `claim_artifact_published`, `claim_validated`, `claim_accepted`, `claim_rejected`, `claim_superseded`, `action_posted`, `corrective_issued`, `board_phase_changed`, `board_complete`. Wire into ResolutionFor, IsTerminal, paired kinds. |
| ClaimsBoardDigest | `core/activity/lenses/ambient.go` | Extend AmbientEnvelope with claims board state: my claims, peer progress, recent testaments, blocked claims, board phase. |
| Claims awareness skills | `core/fabric/claim_awareness_skills.go` | `query_claims_board` (Fabric lens), `query_peer_claims`, `inspect_claim_conflicts`. Register in FabricAwarenessSkillNames. |
| Claim-scoped communication | `core/fabric/awareness_skills.go` | `consult_peer` and `challenge_peer` retired as separate skills. Challenges and consultations are actions posted via `post_action`. Existing Fabric lenses query these like any other claim activity. |

---

### 14.4 Tier 2: Pipeline Agent Conversion

**What**: The 4 pipeline agent types (Inspector, Tester, Engineer, Designer) convert from the protocol state machine to claims. This is the largest single tier.

#### 2a. Pipeline Inspector

| Change | File(s) | Description |
|---|---|---|
| Retire protocol skills | `agents/inspector/pipeline/pipeline.go` | Remove: `challenge_agent`, `handoff_next`, `validate_work`, `process_validation`, `finalize_pipeline`, `handoff_to_ot`, `discard_pipeline`, `discard_queued_artifacts`, `query_pipeline_state`, `challenge_peer`, `consult_peer`, `inspect_open_conflicts` (12 skills) |
| Add claims skills | `agents/inspector/pipeline/pipeline.go` | Register: `query_claims_board`, `post_action`, `evaluate_validation`, `post_remediation_claims`, `inspect_claim_conflicts` (5 skills) |
| Issue claims on board creation | `agents/inspector/pipeline/pipeline.go` | When the board is populated from architect's assembly, the inspector is the formal issuer. Claims carry inspector's AgentID in the issuer Relation. |
| Evaluate testaments | `agents/inspector/pipeline/pipeline.go` | During validation phase, inspector evaluates each testament's artifacts against each claim's validations. Uses `evaluate_validation` skill. |
| Post remediation claims | `agents/inspector/pipeline/pipeline.go` | When validations fail, issues new corrective/remediation claims via `post_remediation_claims`. |
| VFS commit on board complete | `agents/inspector/pipeline/pipeline.go` | On `MarkComplete`, inspector calls `PipelineCommitter.MergePipelineIntoGreen()` and publishes bus event. |

#### 2b. Pipeline Tester

| Change | File(s) | Description |
|---|---|---|
| Retire protocol skills | `agents/tester/pipeline/pipeline.go` | Remove 9 protocol + challenge/consult skills. |
| Add claims skills | `agents/tester/pipeline/pipeline.go` | Register same 5 claims skills as inspector + `submit_testaments`, `update_claim_progress`. |
| Phase-gate run_test_suite | `agents/tester/pipeline/pipeline.go` | Blocked during implementation phase. Returns corrective claim directing tester to `write_test`. |
| Submit testaments for test authoring | `agents/tester/pipeline/pipeline.go` | During implementation: submits testaments with test file artifacts. During validation: submits testaments with test execution artifacts. |
| Post remediation claims for test failures | `agents/tester/pipeline/pipeline.go` | When tests fail, issues claims against engineer with failure artifacts. |

#### 2c. Engineer

| Change | File(s) | Description |
|---|---|---|
| Retire protocol skills | `agents/engineer/skills.go` | Remove 8 protocol + challenge/consult skills. |
| Add claims skills | `agents/engineer/skills.go` | Register: `query_claims_board`, `post_action`, `submit_testaments`, `update_claim_progress`, `inspect_claim_conflicts`. |
| Work against claims | `agents/engineer/skills.go` | Every file write, every tool invocation produces an `update_claim_progress`. Completion produces a testament with code reference + diff artifacts. |
| Scope defined by claims | N/A | Engineer's claims define scope entries — the claims ARE the authorization. No separate enforcement. |

#### 2d. Designer

| Change | File(s) | Description |
|---|---|---|
| Same pattern as Engineer | `agents/designer/skills.go` | Retire 8 protocol skills, add 5 claims skills. Testaments carry design_asset, a11y_audit, token mapping artifacts. |

#### 2e. Pipeline Protocol Retirement

| Change | File(s) | Description |
|---|---|---|
| Remove protocol state machine | `agents/shared/pipeline_protocol.go` | The entire `PipelineProtocolSnapshot`, `PipelineTurnAction`, `PipelineProtocolState`, reducer, mailbox obligations, terminal action guards — all replaced by the claims board. |
| Remove durable protocol events | `agents/shared/pipeline_protocol_durable.go` | `handoff_selected`, `validation_submitted`, `validation_processed`, `ready_for_ot`, `handoff_to_ot`, `tester_finalize`, `tester_artifact_consumed` — all replaced by claims WAL events. |
| Remove pipeline projection | `agents/shared/pipeline_projection.go` | Replaced by `ClaimsBoardProjection`. |
| Remove pipeline expand | `agents/orchestrator/pipeline_expand.go` | Sub-node expansion (StageInspect/StageTest/StageExecute) replaced by claims dispatch. |
| Remove pipeline runtime protocol path | `agents/orchestrator/pipeline_runtime.go` | `routeProtocolPipelineTask`, `pipelineProtocolEligible`, initial protocol snapshot — all replaced by claims dispatch. |

---

### 14.5 Tier 3: Architect Conversion

**What**: The Architect stops generating vague task descriptions and produces precise, atomic claims with validations.

| Change | File(s) | Description |
|---|---|---|
| TaskClaim types | `agents/architect/types.go` | Add `TaskClaim` (with Relations, validations) to `HandoffTask` and `AtomicTask`. Remove `AcceptanceCriteria`, `SuccessCriteria` as separate fields — they become validations on claims. |
| LLM prompt rewrite | `agents/architect/planner_anthropic.go` | Instruct the LLM to produce precise, atomic claims: not "implement JWT middleware" but "implement HS256 JWK deserialization" with validations like "JWK with valid key deserializes" / "returns typed struct, no silent fallbacks". |
| Claim generation in toTask | `agents/architect/planner_anthropic.go` | `toTask()` converts `claimPayload` to `TaskClaim`. Owner normalization, ID generation, validation type inference. |
| Handoff wiring | `agents/architect/skills_planning.go` | `atomicTaskToHandoff()` and `buildPlanHandoff()` carry claims through to the orchestrator. |
| Plan as action | `agents/architect/skills_planning.go` | The entire plan handoff becomes an action. The plan's tasks become claims within that action. The architect assembles; the inspector issues. |

---

### 14.6 Tier 4: Guide Conversion

**What**: The Guide's intent classification and direct communication protocol are preserved and enhanced — they are sound routing infrastructure. What changes is that the Guide stops being a **context carrier** and the session gains a **claims board** as persistent conversational state. The Guide routes requests; the board carries context.

**What stays (adapt and enhance):**
- **Intent classification** — determines which agent handles a request. Unchanged. Enhanced: the classification result is recorded as a testament on the session board (artifact: intent, confidence, target agent), making routing decisions auditable and reusable.
- **Direct communication protocol** (`RequestGuideRouteSync`, `InterAgentBranchSpec`, `ForwardedRequest`) — the transport mechanism for agent-to-agent routing. Unchanged. Claims travel through this protocol, not around it.
- **Direct address detection** (`@architect`, `@librarian`) — unchanged. The detected target is still used for routing. The address also generates a Relation on the resulting claim (relationship: `direct_addressed`).
- **Session routing preferences** — per-session agent preferences, LFU eviction, classification caching. Unchanged.

**What changes (context moves to the board):**
- `ConversationHistory` on `ForwardedRequest` is no longer the source of truth for prior context. It may still be populated as a convenience hint, but agents read the session board for authoritative context.
- The Guide posts every user prompt as an action on the session board before routing. The target agent sees the action's claims on the board when it receives the forwarded request.
- The Guide's classification result is a testament on the board — walking from any agent's work back to the user prompt that triggered it is a Relation traversal, not a `ConversationHistory` lookup.

| Change | File(s) | Description |
|---|---|---|
| User prompt as action on session board | `agents/guide/session_routing.go` | Every user input becomes a prompt action with claims posted to the session's claims board BEFORE the Guide routes the request. The target agent receives the forwarded request via the existing direct communication protocol AND reads the session board for full context. |
| Classification as testament | `agents/guide/classification.go` | The Guide's classifier produces a testament with classification artifacts (intent, confidence, target agent, routing rationale). Posted to the session board. Makes routing decisions auditable and queryable. |
| Context on the board, not in transit | `agents/guide/` | `ConversationHistory` on `ForwardedRequest` becomes a best-effort hint, not the source of truth. Prior conversation context lives on the session board — the target agent reads testaments from prior actions. This fixes the conversation history loss bug permanently. |
| Direct address preserved | `agents/guide/direct_address.go` | `@architect` still routes to the architect via the existing direct communication protocol. Additionally, a `direct_addressed` Relation is added to the claim for auditability. |
| Session-scoped board | `agents/guide/` | Each session gets a root claims board. All user interactions, agent responses, and cross-agent communications are actions on this board. The board IS the conversation history. |
| ForwardedRequest carries board reference | `agents/guide/` | `ForwardedRequest.Metadata` gains a `session_board_id` key so the target agent can locate the session board. The existing metadata mechanism is reused — no structural change to `ForwardedRequest`. |

---

### 14.7 Tier 5: Knowledge Agent Conversion

**What**: Librarian, Academic, and Archivalist become claims participants. Consultations are actions; responses are testaments.

#### 5a. Librarian

| Change | File(s) | Description |
|---|---|---|
| `consult` skill retirement | `agents/librarian/` | The standalone `consult` skill is retired. Agents issue consultation actions against the Librarian. |
| Consultation claims | `agents/librarian/` | Librarian receives claims like "Identify project formatters for Go modules", "Verify naming conventions in services/auth/". |
| Knowledge testaments | `agents/librarian/` | Librarian responds with testaments: "Identified gofumpt as project formatter" with artifacts: `reference_links` (config file path), `code_reference` (existing formatted files). |
| Proactive claims | `agents/librarian/` | When Librarian observes work in a scope it has knowledge about (via Fabric), it issues proactive consultation claims with testaments preemptively. |

#### 5b. Academic

| Change | File(s) | Description |
|---|---|---|
| Research as claims | `agents/academic/` | Research requests become claims: "Research best practices for HS256 JWK implementation". Academic responds with testaments containing research_paper, reference_links, knowledge_graph_vectors artifacts. |
| Librarian validation | `agents/academic/` | Academic's recommendations carry a validation: "Recommendation aligns with codebase patterns" — Librarian evaluates this by checking the testament's recommendations against actual project patterns. |

#### 5c. Archivalist

| Change | File(s) | Description |
|---|---|---|
| Ingestion as claims | `agents/archivalist/` | Every ingestion request is a claim: "Ingest architect context window summary". Archivalist responds with testament containing ingestion_response artifacts (document DB IDs, KG vector IDs, entry IDs). |
| Memory retrieval as claims | `agents/archivalist/` | "Retrieve prior failure modes for services/auth/" is a claim. Archivalist responds with testament containing document_db_snippet and knowledge_graph_vectors artifacts. |

---

### 14.8 Tier 6: Infrastructure Agent Conversion

#### 6a. Scribe — Narration as Testimony

The scribe's core output — structured commentary about its parent agent's work — IS testimony. Each narration cycle is an archival action containing a testament with the commentary as an artifact. The scribe doesn't just *have* claims skills as optional tools; its entire pipeline flows *through* the claims board.

**Narration-as-testament pipeline:**

```
Parent Agent Activity (Fabric)
  → Scribe batch trigger
  → LLM narrates
  → store_archivalist skill (existing)
  → ALSO: board.SubmitTestaments() with:
      Testament:
        summary: commentary.summary
        confidence: "committed"
        artifacts:
          - kind: "narration", reference: full commentary JSON
          - kind: "archivalist_receipt", reference: deterministic entry ID
        relations:
          - issuer: scribe-{parentAgentType}
          - subject: {parentAgentType} (who the narration is ABOUT)
          - caused_by: batch trigger activity ID
```

The archivalist publish (fire-and-forget bus message) remains for long-term storage. The testament submission adds the narration to the session's claims board so other agents can see it — the inspector can verify narration accuracy, the guide can reference it during conversation, cross-replica inheritance can query testaments instead of raw fabric activities.

**Precedent detection as validation:**

When the LLM flags a narration as precedent-worthy, this is a validation on the testament — not a separate fabric activity. The `precedent_worthy` flag and `precedent_why` become a validation with type `"precedent"` and status `passed`. The existing `ActionPrecedentEmitted` fabric activity is emitted by the board amplifier automatically when the validation is recorded.

**Handoff as claims:**

Agent handoff (context exhaustion, quality degradation, transport retry failure) is expressed as claims with validations:

```
Action (ActionTypeTask, agentID: "handoff_bridge")
  Claim: "Handoff {agent} due to {trigger}"
    scope: [{kind: "agent", key: old-agent-id}]
    relations:
      - issuer: handoff_bridge
      - subject: old-agent-id
    validations:
      - type: "context_budget", required: true
        description: "Context usage at {pct}%, zone: {zone}"
        quality_bar: "Context must be below critical threshold"
      - type: "quality_prediction", required: false
        description: "GP predicts quality drop to {score} within {turns} turns"

  Testament (old agent shutdown):
    summary: "Extracted archivable state for handoff"
    artifacts:
      - kind: "archivable_state", reference: JSON(ArchivableState)
      - kind: "prepared_context", reference: JSON(PreparedContext summary)

  Testament (new agent ready):
    summary: "Injected prepared context, resuming work"
    artifacts:
      - kind: "context_injection", reference: JSON({newAgentID, tokenCount, status})
    validations:
      - type: "integration", status: pending
        description: "New agent produces comparable quality"
        quality_bar: "First 3 turns must not regress quality metrics"
```

The handoff claim transitions: `pending` → `in_progress` (transfer executing) → `testified` (both testaments submitted) → `accepted` (quality validation passes) or `rejected` (quality validation fails → new handoff claim with `supersedes` relation).

| Change | File(s) | Description |
|---|---|---|
| Narration as testament | `agents/scribe/skills.go` | `store_archivalist` handler also submits testament with narration artifact to session claims board. Archivalist publish remains for long-term storage. |
| Precedent as validation | `agents/scribe/skills.go` | `precedent_worthy` flag becomes a validation on the narration testament. `ActionPrecedentEmitted` emitted via amplifier. |
| Board preamble | `agents/scribe/tool_loop.go` | System prompt prepended with board state so LLM narrates with claims context. |
| Claims skills | `agents/scribe/skills.go` | Full claims participant — query, post, submit, update, inspect. |
| Handoff claims | `core/handoff/bridge.go`, `agents/shared/context_governor.go` | Handoff triggers post claims to session board. State extraction/injection produce testaments with artifacts. |
| GoroutineScope | `agents/scribe/scribe.go` | All goroutines tracked via scope. Scope passed to board for async subscriber dispatch. |

#### 6b. Guardian — Every Process as Claims

Every guardian process — command approval, content scanning, git gating, health monitoring, plan approval, tool grants, diff review, checkpoints, reputation tracking, conversation response — is expressed as a uniform Action → Claim (with Relations, Scope, Validations) → Testament (with Artifacts, Relations, Confidence) exchange. No field omissions, no structural shortcuts.

##### Command Approval (`command_execution_control`)

```
Action (type: task, agent_id: requesting_agent)
  Claim:
    title: "Approve execution of `go test ./...` in services/auth/"
    description: "Engineer requests bash command execution"
    scope: [{kind: "command", key: "bash:go test"}, {kind: "path", key: "services/auth/"}]
    action_type: task
    relations:
      - related: requesting_agent, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "Command contains no destructive operations (rm -rf, drop, truncate)"
        quality_bar: "Zero destructive patterns detected"
      - type: inspection, required: true
        description: "Command is scoped to authorized paths"
        quality_bar: "All referenced paths within agent's declared scope"
      - type: inspection, required: true
        description: "Agent has permission for this operation class"
        quality_bar: "Stored rule or user authorization grants access"

  Testament (agent_id: guardian):
    summary: "Approved `go test ./...` — safe, scoped, authorized"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: requesting_agent, related_type: agent, relationship: subject
    artifacts:
      - kind: "safety_assessment", reference: JSON(CommandAnalysis)
      - kind: "scope_verification", reference: JSON(matched_rule or path_analysis)
      - kind: "approval_grant", reference: JSON(commandapproval.Evaluation)
```

On denial: testament summary states denial reason, `kind:"error"` artifact carries the denial, guardian posts a corrective action with claims like "Modify command to exclude `/etc/`" or "Request user authorization for elevated access".

##### Fetch Approval (`evaluateFetchApproval`)

```
Action (type: task, agent_id: requesting_agent)
  Claim:
    title: "Approve fetch of https://api.example.com/data"
    description: "Agent requests external HTTP fetch"
    scope: [{kind: "domain", key: "api.example.com"}, {kind: "url", key: "https://api.example.com/data"}]
    action_type: task
    relations:
      - related: requesting_agent, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "Domain is known/trusted or user-authorized"
        quality_bar: "DomainReputation.TrustLevel >= TrustKnown or stored allow rule"
      - type: inspection, required: false
        description: "Response content contains no credential leaks"
        quality_bar: "Zero credential findings in response body"

  Testament (agent_id: guardian):
    summary: "Approved fetch of api.example.com — domain trusted (12 clean fetches)"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: requesting_agent, related_type: agent, relationship: subject
    artifacts:
      - kind: "domain_reputation", reference: JSON(DomainReputation)
      - kind: "approval_grant", reference: JSON(commandapproval.Evaluation)
      - kind: "matched_rule", reference: JSON(stored_rule) // if rule-based decision
```

##### Plan Approval (`plan_approval_gate`)

```
Action (type: task, agent_id: architect)
  Claim:
    title: "Accept plan: Implement JWT middleware"
    description: "Architect requests user acceptance of implementation plan"
    scope: [{kind: "plan", key: plan_id}]
    action_type: task
    relations:
      - related: architect, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "User approves plan scope and approach"
        quality_bar: "User clicks Approve in the TUI dialog"
      - type: inspection, required: false
        description: "Plan reflects current codebase state"
        quality_bar: "Freshness summary shows no stale references"
      - type: inspection, required: false
        description: "No significant codebase drift since planning"
        quality_bar: "Drift signals below threshold"

  Testament (agent_id: guardian):
    summary: "Plan approved by user"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: architect, related_type: agent, relationship: subject
    artifacts:
      - kind: "verdict", reference: "approve" | "reject" | "modify"
      - kind: "user_reason", reference: reason text
      - kind: "freshness_analysis", reference: JSON(freshness_summary)
      - kind: "drift_analysis", reference: JSON(drift_signals)
```

##### Tool Execution Control (`tool_execution_control`)

```
Action (type: task, agent_id: requesting_agent)
  Claim:
    title: "Grant execution of `workspace_write` for engineer-1"
    description: "Agent requests guardian-controlled tool execution grant"
    scope: [{kind: "tool", key: "workspace_write"}, {kind: "agent", key: "engineer-1"}]
    action_type: task
    relations:
      - related: requesting_agent, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "Tool execution mode is guardian-controlled"
        quality_bar: "ToolPolicy.ExecutionMode == GuardianControlled"
      - type: inspection, required: true
        description: "Requester identity matches declared agent"
        quality_bar: "SourceAgentID matches or aliases to request agent"
      - type: inspection, required: true
        description: "Tool is flagged approval-sensitive"
        quality_bar: "ToolPolicy.ApprovalSensitive == true"

  Testament (agent_id: guardian):
    summary: "Granted `workspace_write` for engineer-1 (30s TTL)"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: requesting_agent, related_type: agent, relationship: subject
    artifacts:
      - kind: "execution_grant", reference: JSON(GuardianControlGrant)
```

##### Content Scanning (`content_scan`)

```
Action (type: task, agent_id: guardian)
  Claim:
    title: "Validate content contains no credentials or injection patterns"
    description: "Scan agent output for security violations"
    scope: [{kind: "content", key: correlation_id}]
    action_type: task
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "No secrets, API keys, or tokens detected"
        quality_bar: "SecretSanitizer returns zero credential findings"
      - type: inspection, required: true
        description: "No prompt injection or code injection patterns"
        quality_bar: "InjectionScanner returns zero findings"

  Testament (agent_id: guardian):
    summary: "Content clean — 0 findings across 2 scans"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
    artifacts:
      - kind: "credential_scan", reference: JSON([]Finding) // empty or populated
      - kind: "injection_scan", reference: JSON([]Finding)
```

When findings exist: artifacts carry the findings, testament summary states the count and severity, and the guardian posts a corrective action against the producing agent.

##### Git Mutation Gating (`GitObserver.GateCheck`)

```
Action (type: task, agent_id: guardian)
  Claim:
    title: "Gate push to protected branch `main`"
    description: "Pre-mutation hook on protected branch"
    scope: [{kind: "branch", key: "main"}, {kind: "operation", key: "push"}]
    action_type: task
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "Branch matches protected pattern"
        quality_bar: "Glob match against config.ProtectedBranches"
      - type: inspection, required: true
        description: "User explicitly authorizes protected branch mutation"
        quality_bar: "User clicks Approve in approval dialog"

  Testament (agent_id: guardian):
    summary: "Push to main approved by user"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
    artifacts:
      - kind: "branch_protection", reference: JSON(matched_patterns)
      - kind: "user_authorization", reference: JSON(ApprovalResult)
      - kind: "operation_context", reference: JSON(git_event_params)
```

##### Safety Checkpoints (`CheckpointManager`, periodic)

```
Action (type: archival, agent_id: guardian)
  Claim:
    title: "Create safety checkpoint — 12 dirty files exceed threshold of 10"
    description: "Periodic dirty file threshold evaluation"
    scope: [{kind: "git", key: "checkpoint"}]
    action_type: archival
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true, status: passed
        description: "Dirty file count exceeds safety threshold"
        quality_bar: "getDirtyFileCount() >= config.DirtyThreshold"
      - type: inspection, required: true
        description: "User approves checkpoint creation"
        quality_bar: "User clicks Approve in checkpoint dialog"

  Testament (agent_id: guardian):
    summary: "Safety checkpoint created at abc1234"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
    artifacts:
      - kind: "checkpoint_record", reference: JSON(CheckpointRecord)
      - kind: "dirty_file_count", reference: "12"
      - kind: "user_authorization", reference: JSON(ApprovalResult)
```

##### Health Monitoring (`HealthMonitor`, periodic)

```
Action (type: archival, agent_id: guardian)
  Claim:
    title: "System health check — periodic evaluation"
    description: "Detect unresponsive agents and budget violations"
    scope: [{kind: "system", key: "health"}]
    action_type: archival
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "All agents responsive within 3x heartbeat interval"
        quality_bar: "Zero unresponsive agents detected"
      - type: inspection, required: true
        description: "Token/cost usage below 80% warning threshold"
        quality_bar: "BudgetStatus.WarningExceeded == false"

  Testament (agent_id: guardian):
    summary: "1 anomaly: engineer-1 unresponsive for 90s"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
    artifacts:
      - kind: "health_snapshot", reference: JSON([]AgentHealthSnapshot)
      - kind: "budget_status", reference: JSON(BudgetStatus)
      - kind: "anomaly", reference: "engineer-1 unresponsive for 90s" // one per anomaly
```

When health is clean: all validations pass, testament summary states "System healthy", no anomaly artifacts.

##### Diff Review (`DiffGate`)

```
Action (type: task, agent_id: guardian)
  Claim:
    title: "Review staged diff for suspicious patterns"
    description: "Pre-commit diff scan for hardcoded credentials, debug code"
    scope: [{kind: "git", key: "staged_diff"}]
    action_type: task
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "No hardcoded credentials in staged changes"
        quality_bar: "Zero credential patterns in diff hunks"
      - type: inspection, required: true
        description: "No debug/test-only code in production paths"
        quality_bar: "Zero suspicious pattern matches"

  Testament (agent_id: guardian):
    summary: "Staged diff clean — 0 findings in 4 files"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
    artifacts:
      - kind: "diff_findings", reference: JSON([]DiffFinding)
      - kind: "files_reviewed", reference: JSON(file_paths)
```

##### Rollback (`rollback` skill)

```
Action (type: corrective, agent_id: guardian)
  Claim:
    title: "Rollback to snapshot abc1234"
    description: "Revert to prior safety checkpoint"
    scope: [{kind: "git", key: "rollback"}, {kind: "snapshot", key: "abc1234"}]
    action_type: corrective
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true
        description: "User authorizes rollback"
        quality_bar: "User clicks Approve in rollback dialog"

  Testament (agent_id: guardian):
    summary: "Rolled back to checkpoint abc1234"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
    artifacts:
      - kind: "rollback_target", reference: "abc1234"
      - kind: "user_authorization", reference: JSON(ApprovalResult)
```

##### Conversation Response

```
Action (type: testament, agent_id: guardian)
  Testament:
    summary: "Responded to user: system health report requested"
    confidence: committed
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: user, related_type: agent, relationship: subject
    artifacts:
      - kind: "intent_classification", reference: "report"
      - kind: "response_text", reference: response content
      - kind: "tool_calls", reference: JSON([]{name, result_summary})
      - kind: "usage", reference: JSON(StreamUsage)
```

##### Context Budget / Handoff

```
Action (type: task, agent_id: guardian)
  Claim:
    title: "Context budget critical — guardian at 96% usage"
    description: "Context governor detected critical zone"
    scope: [{kind: "agent", key: "guardian"}, {kind: "resource", key: "context_window"}]
    action_type: task
    relations:
      - related: guardian, related_type: agent, relationship: issuer
      - related: guardian, related_type: agent, relationship: subject
    validations:
      - type: inspection, required: true, status: failed
        description: "Context usage below critical threshold (95%)"
        quality_bar: "ContextGovernor.Zone < Critical"

  Testament (old agent):
    summary: "Extracted archivable state for handoff"
    confidence: committed
    artifacts:
      - kind: "archivable_state", reference: JSON(ArchivableState)
      - kind: "prepared_context", reference: JSON(PreparedContext summary)
      - kind: "context_usage", reference: "96%"

  Testament (new agent):
    summary: "Injected prepared context, resuming work"
    confidence: tentative
    artifacts:
      - kind: "context_injection", reference: JSON({newAgentID, tokenCount})
    validations:
      - type: integration, required: true, status: pending
        description: "New agent produces comparable quality"
        quality_bar: "First 3 turns must not regress quality metrics"
```

| Change | File(s) | Description |
|---|---|---|
| Claims skills | `agents/guardian/skills.go` | Full claims participant replacing `CrossPipelineSkills`. Board resolved from `activeSessionID` per-request under `requestSerializer`. |
| Board preamble | `agents/guardian/conversation.go` | User message prepended with board state. |
| Tool manifest | `agents/guardian/skills_api.go` | Claims skills in manifest with correct execution modes (read-only: local, mutating: local_worker). |
| Command approval as claims | `agents/guardian/skills_command_approval.go` | Evaluation flow posts action with claim, guardian responds with testament. Denial produces corrective action. |
| Fetch approval as claims | `agents/guardian/skills_command_approval.go` | Fetch evaluation posts claim with domain reputation validation, responds with testament. |
| Plan approval as claims | `agents/guardian/skills_plan_approval.go` | Plan posted as claim against guardian, user verdict is testament with artifacts. |
| Tool grants as claims | `agents/guardian/skills_control.go` | Grant request is claim, grant response is testament with execution_grant artifact. |
| Content scan as claims | `agents/guardian/content_validator.go` | Scan invocation is claim with inspection validations, results are testament with finding artifacts. |
| Git gating as claims | `agents/guardian/git_observer.go` | Pre-mutation gate is claim with branch protection + user authorization validations. |
| Checkpoints as claims | `agents/guardian/git_checkpoint.go` | Periodic threshold check is claim, checkpoint creation is testament. |
| Health monitoring as claims | `agents/guardian/health_monitor.go` | Periodic health check is claim with responsiveness + budget validations, snapshot is testament. |
| Diff review as claims | `agents/guardian/diff_gate.go` | Staged diff review is claim, findings are testament artifacts. |
| Rollback as claims | `agents/guardian/skills.go` | Rollback request is corrective claim, execution is testament. |
| Conversation as testament | `agents/guardian/conversation.go` | Every guardian response submits testament with intent, response, tool calls, usage artifacts. |
| Context budget as claims | `agents/guardian/guardian.go`, `agents/shared/context_governor.go` | Budget zone transitions post claims, handoff produces testaments. |
| GoroutineScope | `agents/guardian/guardian.go` | All subsystem goroutines (health monitor, checkpoint manager) tracked via scope. |

---

### 14.9 Tier 7: Orchestrator Conversion

**What**: The Orchestrator stops mediating agent interactions. It manages DAG execution by monitoring claims boards. It does not dispatch global reviews, carry conversation context, or track pending checkpoint reviews.

| Change | File(s) | Description |
|---|---|---|
| DAG nodes as actions | `agents/orchestrator/dag_bridge.go` | Each DAG node dispatch creates an action on the pipeline's claims board. The node's task prompt becomes claims. Node completion = board `MarkComplete`. |
| Remove pipeline dispatch mediation | `agents/orchestrator/pipeline_runtime.go` | Remove `routeProtocolPipelineTask`, `publishOTGlobalFollowupRequest`, `recordPendingCheckpointReview`. Pipeline inspector routes directly to global inspector. |
| Remove checkpoint review tracking | `agents/orchestrator/checkpoint_review.go` | Delete `pendingCheckpointReview`, `HandleCheckpointReviewTerminal`, `completePendingCheckpointReview`, `failPendingCheckpointReview`. DAG bridge subscribes to `global_review.complete` bus events. |
| Task dispatch as action | `agents/orchestrator/task_dispatch.go` | `handleTaskDispatch` creates a claims board, posts the architect's claims as an action, and dispatches subjects. No protocol handshake. |
| Health monitoring via claims | `agents/orchestrator/` | Agent health checks become periodic claims: "Report health status". Agents respond with testaments containing health artifacts. |
| Coordination via claims | `agents/orchestrator/` | The orchestrator's coordination service (scope claims, artifacts, reviews) becomes part of the claims board. Scope claims are claims. Artifact publishing is a testament. Review requests are consultation actions. |

---

### 14.10 Tier 8: Sovereign System Retirement

**What**: The three existing sovereign systems (Pipeline Protocol, Coordination Service, Decision Manifest) are subsumed by the claims board.

#### 8a. Pipeline Protocol → Claims Board

| What's Retired | Replaced By |
|---|---|
| `PipelineProtocolSnapshot` | `ClaimsBoardProjection` |
| `PipelineTurnAction` | `Action` with claims |
| `PipelineProtocolState` (reducer, WAL, mailbox) | `ClaimsBoard` (WAL, projection, subscription) |
| `handoff_selected`, `validation_submitted`, etc. | `action_posted`, `testament_submitted`, etc. |
| Terminal action guards | Board phase transitions |
| Audit lock | Eliminated — peers coordinate via claims |

#### 8b. Coordination Service → Claims Board

| What's Retired | Replaced By |
|---|---|
| `manage_claim(action=acquire\|release)` | Scope relations on claims: the claim's scope entries define file/symbol/API boundaries |
| `publish_work_event(kind=artifact\|review_request\|review_completion)` | Testaments with artifacts: agent publishes work as testament |
| `query_claims_board` | Full board state including scope claims |
| Board subscription | Reactive projection updates |
| `ClaimMode` (exclusive/shared/review) | Relation types on scope claims: `exclusive_scope`, `shared_scope`, `review_scope` |

#### 8c. Decision Manifest → Claims Board

| What's Retired | Replaced By |
|---|---|
| `declare_decision` | Claim: "Declare test_framework=pytest" with testament containing detection artifacts |
| `query_decisions` | `query_claims_board` filtered by decision-type claims |
| Decision confidence (Hint/Tentative/Committed/Consensus) | Testament `Confidence` field (hint/tentative/committed/consensus) |
| Auto-publish on skill invocation | Skill amplifiers emit claims: `detect_test_harness` → claim "test_framework detected" with testament |
| Manifest reconciliation | `inspect_claim_conflicts`: surface conflicting decision claims across pipelines |

---

### 14.11 Tier 9: System Infrastructure Conversion

#### 9a. Handoff Protocol → Claims Board

| Change | File(s) | Description |
|---|---|---|
| Handoff state is the board | `core/handoff/` | When an agent's context fills and triggers handoff, the new instance reads the claims board — all prior claims, testaments, artifacts, and progress are there. No separate handoff state transfer needed. |
| Remove `BuildHandoffState` / `InjectHandoffState` | `core/concurrency/pipeline_handoff_integration.go` | The `HandoffableAgent` interface is simplified: the board IS the handoff state. |
| Handoff as claim | `core/handoff/` | The handoff itself becomes a claim: "Continue work on claims [X, Y, Z]" issued against the new agent instance. The new instance submits testaments proving it resumed correctly. |

#### 9b. Session Management → Session Board

| Change | File(s) | Description |
|---|---|---|
| Session-scoped root board | `core/session/` | Every session gets a root claims board. User prompts, agent responses, cross-agent interactions — all are actions on this board. The board IS the conversation history. |
| Pipeline boards as children | `core/session/` | Pipeline-scoped boards are children of the session board. The session board provides the "global context" that the Guide currently fails to carry between turns. |
| Session persistence | `core/session/` | Session boards persist to `.sylk/sessions/<id>/claims/`. Recovery restores the full conversation state. |

#### 9c. VFS Integration

| Change | File(s) | Description |
|---|---|---|
| File scope as claim prerequisite | `core/versioning/` | Every `workspace_write(op=write|edit|delete)` operates within the scope defined by the agent's claims. The claims' scope entries define what files the agent is allowed to touch. |
| File writes produce artifacts | `agents/shared/` | Every VFS write automatically produces an artifact (kind: `diff`, reference: the changed file path + line range) attached to the active claim's progress. |
| VFS commit as testament | `core/versioning/` | `MergePipelineIntoGreen` success produces a testament with merge artifacts (paths merged, version, base version). |

#### 9d. Error Handling → Corrective Claims

| Change | File(s) | Description |
|---|---|---|
| Skill precondition failures | `core/claims/corrective.go` | Every skill that currently returns an error for a precondition (missing scope, wrong phase, insufficient context) instead generates a corrective action with claims that guide the agent to satisfy the precondition. |
| LLM failures | `core/providers/` | Provider errors (timeout, rate limit, context canceled) produce corrective claims: "Retry with reduced context", "Wait for rate limit reset", "Simplify the request". |
| Tool failures | `core/toolruntime/` | Tool execution failures produce claims against the agent: "Diagnose tool failure", "Retry with adjusted parameters". |

#### 9e. Steering Ledger → Claims

| Change | File(s) | Description |
|---|---|---|
| Steering as claims | `core/steering/`, `agents/shared/` | "Focus on security-critical paths" becomes a claim from the user/inspector against the engineer. The steering ledger's priority hints become claim `Priority` fields. Quality gates become validations. |

---

### 14.12 Tier 10: TUI Conversion

**What**: The terminal UI renders claims, testaments, and artifacts instead of protocol state and task prompts.

| Change | File(s) | Description |
|---|---|---|
| Agent panel | `ui/agent/` | Shows claims board state per pipeline: claims in progress, testified, accepted/rejected. Replaces the sequential phase display (defining criteria → creating tests → executing → validating). |
| Pipeline visualization | `ui/` | Pipeline panel shows claim progress bars, testament counts, artifact counts. Color-coded by status. |
| Chat rendering | `ui/chat/` | Testaments rendered as structured responses with collapsible artifact lists. Claims rendered as task cards. |
| Claims board view | `ui/` (new) | Dedicated claims board panel: full board state, filterable by agent/status/action type. Shows the claim → testament → validation chain. |
| Conversation context | `ui/chat/` | The conversation IS the session board. Prior turns are visible as prior actions with their testaments. No lost context between turns. |

---

### 14.13 Tier 11: Boot and Lifecycle Conversion

| Change | File(s) | Description |
|---|---|---|
| Boot as claims | `core/boot/` | Boot pipeline phases (setup → detect → allocate → ingest → commit → finalize) become claims on a boot board. Each phase submits a testament with success artifacts. Boot completes when all claims are accepted. |
| Agent activation as claims | `core/container/` | Agent activation is a claim: "Activate engineer for session X". The container responds with a testament containing the agent ID, readiness status. |
| Shutdown as claims | `core/container/` | Graceful shutdown issues claims against each active agent: "Persist state and terminate". Agents respond with shutdown testaments. |

---

### 14.14 Conversion Summary

| Tier | Components | Claims Board Scope | Replaces |
|---|---|---|---|
| 0 | Core types, board, WAL, amplifier, skills | System-wide | Nothing (new) |
| 1 | Fabric ActionKinds, ambient context, awareness skills | System-wide | Partial Fabric integration |
| 2 | Inspector, Tester, Engineer, Designer (pipeline) | Per-pipeline | Pipeline Protocol |
| 3 | Architect | Per-plan | AcceptanceCriteria, SuccessCriteria, task prompts |
| 4 | Guide | Per-session | ConversationHistory, ForwardedRequest routing |
| 5 | Librarian, Academic, Archivalist | Per-session | `consult` skill, consultation cache |
| 6 | Scribe, Guardian | Per-session | Narration stream, command approval gates |
| 7 | Orchestrator | Per-DAG | Pipeline dispatch, checkpoint review, health monitoring |
| 8 | Protocol, Coordination, Decision Manifest | N/A (retired) | Three sovereign systems → one claims board |
| 9 | Handoff, Session, VFS, Errors, Steering | Per-session | Handoff state, conversation context, error returns |
| 10 | TUI | N/A (rendering) | Protocol state display, sequential phase panels |
| 11 | Boot, Container lifecycle | Per-boot | Boot pipeline phases |

---

### 14.15 Tier 12: Persistence and Reasoning Infrastructure

These are the systems that store, index, retrieve, and reason over knowledge. Claims don't just flow through them — claims, testaments, and artifacts ARE the data they persist and reason over.

#### 12a. Memory Forest

The Memory Forest is the long-term cross-session precedent store. Currently it harvests "successful causal chains" from the Fabric's Coarse-resolution activities. With claims, the harvest material is richer and more structured.

| Change | File(s) | Description |
|---|---|---|
| Harvest claims, not activities | `core/forest/` | The Memory Forest subscriber shifts from harvesting raw `decision_declared` / `validation_accepted` activities to harvesting **accepted claims with their full testament+artifact chains**. An accepted claim is a proven assertion — its testament is the evidence, its artifacts are the proof. This is strictly richer than a raw activity. |
| Claims as forest branches | `core/forest/` | Each accepted claim becomes a forest branch. The branch carries: the claim's description, the testament's summary, all artifact references, the validation verdicts and their quality bars. Semantic retrieval queries match against claim descriptions and testament summaries. |
| Testament artifacts as leaf nodes | `core/forest/` | Artifacts (code references, test outputs, research papers, design assets) become leaf nodes on the branch. Retrieval returns the full claim → testament → artifact chain, not just a disembodied precedent. |
| Cross-session claim lineage | `core/forest/` | When a claim in session B is similar to an accepted claim from session A, the forest surfaces the prior claim's testament and artifacts as a precedent. The agent sees exactly what was done before, how it was validated, and what artifacts proved it. |
| `forest_recall` returns claims | `core/forest/` | The `forest_recall`, `forest_resolve_intent`, and `forest_predict_next_branches` skills return prior claims with their testaments, not raw decision records. Agents see structured precedent: "Last time someone claimed X, the testament was Y with artifacts Z, and it was accepted." |
| Rejection precedents | `core/forest/` | Rejected claims are also harvested — they're anti-precedents. "Last time someone tried X, it was rejected because Y." The rejection reason (from the StatusHistory) and the failing validation verdicts are preserved. |

#### 12b. Knowledge Graph (VectorGraphDB)

The knowledge graph stores semantic embeddings and causal edges. With claims, the graph gains structured nodes for every entity type.

| Change | File(s) | Description |
|---|---|---|
| Claims as graph nodes | `core/vectorgraphdb/` | Every claim becomes a node with an embedding of its description. The node carries the claim's Relations, status, and testament reference. |
| Testaments as graph nodes | `core/vectorgraphdb/` | Every testament becomes a node with an embedding of its summary. Edges connect testaments to their claims (Relation: `claim`) and to their artifacts. |
| Artifacts as graph nodes | `core/vectorgraphdb/` | Artifacts that carry semantic content (research papers, code references, diagnosis reports) become nodes. Artifacts that are purely structural (ingestion receipts, approval tokens) are stored as metadata on the testament node, not as separate nodes. |
| Causal edges from Relations | `core/vectorgraphdb/` | The Relation system maps directly to graph edges: `supersedes`, `depends_on`, `caused_by`, `refines`, `derived_from` all become first-class edge types. The graph can walk from any artifact back through its testament, claim, and action to the original user prompt. |
| Validation verdicts as edge weights | `core/vectorgraphdb/` | Edges from claims to testaments carry the validation verdict as weight. Accepted testaments have high-confidence edges. Rejected testaments have low-confidence edges with the failure reason as edge metadata. |
| Semantic search over claims | `core/vectorgraphdb/` | "Find prior work similar to HS256 JWK deserialization" searches claim descriptions. Returns the full chain: matching claims, their testaments, and their artifacts. Richer than searching raw activities. |
| Cross-pipeline claim graphs | `core/vectorgraphdb/` | Claims from different pipelines that share scope entries (same files, same symbols) are connected by `conflicts_with` or `refines` edges. The graph surfaces cross-pipeline interactions. |

#### 12c. Document DB

The document DB stores full-text searchable documents (Scribe narrations, Archivalist entries, research outputs). With claims, documents are anchored to specific claims and testaments.

| Change | File(s) | Description |
|---|---|---|
| Testaments as documents | `core/knowledge/` | Every testament with a non-trivial summary becomes a document in the DB. The document carries the testament's ID, claim reference, artifacts, and the full Relation chain. Full-text search over testament summaries. |
| Artifacts as document attachments | `core/knowledge/` | Artifact references are indexed as attachments on testament documents. Searching for "JWK deserialization" finds testaments whose artifacts reference JWK-related files. |
| Claim-scoped retrieval | `core/knowledge/` | "Show me all documents related to claim X" retrieves the claim's testament, all related artifacts, any narrations the Scribe produced during the claim's implementation, and any Archivalist entries generated from those narrations. |
| Ingestion receipts as artifacts | `core/knowledge/` | When the Archivalist ingests a document, its ingestion receipt is an artifact on the Scribe's testament. The document DB entry links back to the specific claim that generated it through the artifact → testament → claim chain. |

#### 12d. Fabric

The Fabric itself is the observation/coordination substrate. With claims as the universal primitive, the Fabric's role evolves from "observe sovereign systems and project" to "observe the claims board and project."

| Change | File(s) | Description |
|---|---|---|
| Single sovereign source | `core/activity/` | The Fabric currently observes 3+ sovereign systems (Decision Manifest, Coordination Service, Pipeline Protocol) via separate amplifiers. With claims, there is ONE sovereign source: the claims board. One amplifier, one projection path. All other sovereign systems are retired (Tier 8). |
| Activity → Claim mapping | `core/activity/` | Every Fabric activity maps to a claims entity. `claim_issued` → Claim. `testament_submitted` → Testament. `claim_artifact_published` → Artifact. `claim_validated` → Validation StatusChange. The activity stream becomes a projection of the claims board, not a parallel record. |
| Richer causal chains | `core/activity/` | Current causal chains link activities by `Caused` / `Resolves`. With claims, the causal chain is explicit in Relations: claim `caused_by` action, testament `caused_by` claim, artifact `caused_by` testament. The Fabric's `causal_trace` lens walks Relations, not ad-hoc `Caused` pointers. |
| Ambient context = board digest | `core/activity/lenses/ambient.go` | The ambient context envelope stops aggregating from multiple sovereign projections and reads directly from the claims board projection. One source of truth, not a merge of three. |
| Lens queries = board queries | `core/activity/lenses/` | `query_peer_activity` supplements with `query_claims_board` filtered by peer. `inspect_claim_conflicts` replaces `inspect_open_conflicts` for claims-specific conflict detection. `find_related_activity` searches claim/testament descriptions. The lens layer thins — the claims board is the primary coordination surface. |
| Resolution tiers still apply | `core/activity/` | Atomic/Fine/Medium/Coarse resolution tiers still determine storage lifetime. Claim progress updates (Medium) evict faster than accepted claims (Coarse, permanent). Artifacts tagged `Ephemeral` evict after iteration; durable artifacts persist to Coarse tier. |
| Chokepoint instrumentation simplified | `core/activity/span.go` | Currently, 10+ chokepoints emit raw activities and 6+ amplifiers project sovereign state. With one sovereign source, the amplifier count drops to 1 (the claims board amplifier). Chokepoints still emit infrastructure activities (LLM calls, file writes, command executions) but semantic activities all flow through claims. |

#### 12e. Bleve (Full-Text Search)

| Change | File(s) | Description |
|---|---|---|
| Index claims and testaments | Bleve subscriber | The Bleve full-text index currently indexes Fabric activities. With claims, it indexes claim descriptions, testament summaries, validation descriptions, quality bars, and artifact references. Searching "HS256 deserialization" returns claims AND testaments AND their artifacts. |
| Faceted search by entity type | Bleve subscriber | Search results faceted by: claims (what was asked), testaments (what was done), artifacts (what proof exists), validations (what was checked). Currently facets are by ActionKind — claims give semantic facets. |

---

### 14.16 Tier Summary (Updated)

| Tier | Components | Replaces |
|---|---|---|
| 0 | Core types, board, WAL, amplifier, skills | Nothing (new) |
| 1 | Fabric ActionKinds, ambient context, awareness skills | Partial Fabric integration |
| 2 | Inspector, Tester, Engineer, Designer (pipeline) | Pipeline Protocol |
| 3 | Architect | AcceptanceCriteria, SuccessCriteria, task prompts |
| 4 | Guide | ConversationHistory, ForwardedRequest routing |
| 5 | Librarian, Academic, Archivalist | `consult` skill, consultation cache |
| 6 | Scribe, Guardian, Handoff Bridge | Narration stream, command approval gates, handoff triggers |
| 7 | Orchestrator | Pipeline dispatch, checkpoint review, health monitoring |
| 8 | Protocol, Coordination, Decision Manifest | Three sovereign systems → one claims board |
| 9 | Handoff, Session, VFS, Errors, Steering | Handoff state, conversation context, error returns |
| 10 | TUI | Protocol state display, sequential phase panels |
| 11 | Boot, Container lifecycle | Boot pipeline phases |
| 12 | Memory Forest, Knowledge Graph, Document DB, Fabric, Bleve | Raw activity harvesting, multi-source projection, unanchored search |

**Total agents converted: 12**
**Total sovereign systems retired: 3** (Pipeline Protocol, Coordination Service, Decision Manifest)
**Total persistence systems converted: 5** (Memory Forest, Knowledge Graph, Document DB, Fabric projection, Bleve index)
**Total interaction patterns unified: 6** → all become claim → testament
**Fabric amplifiers reduced: 6+ → 1** (claims board amplifier)
**Bugs fixed by design: conversation history loss** (context lives on the board, not in transit)
