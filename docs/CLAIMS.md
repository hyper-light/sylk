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

Actions unify what were previously separate mechanisms (task dispatch, `challenge_peer`, `consult_peer`, error handling). A challenge is just an action whose claims assert a problem. A consultation is just an action whose claims request information. A corrective is just an action whose claims guide an agent back on track. They all flow through the same machinery.

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

An **Artifact** is a piece of evidence attached to a testament. Artifacts are polymorphic — the `Kind` field discriminates how to interpret the reference:

| Kind | Example | Used By |
|---|---|---|
| `code_reference` | `services/auth/jwk.go:47-89` | Engineer, Designer |
| `test_output` | `TestDeserializeHS256JWK_ValidKey PASS` | Tester |
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

### 2.7 Corrective Claims

When an agent acts out of order — invokes a skill without sufficient validated claims backing the invocation, works outside its claimed scope, attempts an operation that requires prerequisite claims — the system does NOT return an error. Instead, it issues the agent a **corrective action**: a set of claims that guide the agent toward the desired behavior.

For example, if an Engineer calls `write_pipeline_file` before acquiring a file scope claim:

- **Instead of**: error "insufficient scope claim"
- **The system issues**: a corrective action with claims like "Acquire exclusive scope on `services/auth/middleware.go`" and "Verify no peer claims overlap with target scope" — the Engineer processes these claims, acquires scope via `coord_claim_scope`, submits testaments with the scope receipt artifacts, and then proceeds to write the file.

This makes the system self-healing. Misbehavior produces more claims, not failures. The agent always has a path forward.

### 2.8 The Validation Flow

The claim's issuer (the claimant) validates the testament and its artifacts against the claim's validations:

```
1. Issuer creates Claim (with validations) against Subject
2. Subject does work
3. Subject issues Testament (with artifacts) back to Issuer
4. Issuer evaluates each Validation against the Testament's Artifacts
5a. All validations pass → Claim accepted
5b. Validation fails → Issuer may issue new corrective/remediation claims
```

For initial task claims, the Inspector is the issuer. The Inspector evaluates testaments from Engineer/Designer/Tester against each claim's validations. The Tester may also validate test-type validations by running tests and submitting their own evaluation. If validations fail, the Inspector or Tester issues new claims (remediation or corrective).

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

---

## 4. Core Data Model

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

---

## 5. Two-Phase Execution Model (Pipeline Context)

Within pipelines, the claims model operates in two phases. This is the pipeline-specific application of the universal claims model.

### 5.1 Implementation Phase

All non-inspector pipeline agents work simultaneously against their assigned claims.

1. **All agents work simultaneously.** Engineer, Designer, and Tester receive their claims and begin work in parallel.
2. **The Tester MAY NOT RUN TESTS.** `run_test_suite` is phase-gated. The Tester authors tests but does not execute them.
3. **Every action = atomic claim update.** Each meaningful action produces an `UpdateClaimProgress` on the board.
4. **Subjects submit testaments when done.** Instead of "marking complete," the subject submits a testament with artifacts proving each claim is satisfied.
5. **Agents communicate via actions.** Challenges and consultations are actions (sets of claims) — not separate skill types.
6. **Corrective claims instead of errors.** Out-of-order actions produce corrective claims, not errors.
7. **Phase ends when all claims reach `testified`.** Every subject has submitted a testament.

### 5.2 Validation Phase

The Inspector (as issuer of the initial claims) validates each testament's artifacts against each claim's validations. The Tester may also validate test-type validations.

1. **Issuer evaluates EVERY validation for EACH claim** against the testament's artifacts.
2. **The quality bar must be met.** Each validation's `QualityBar` statement defines the standard.
3. **Inspector and Tester collaborate via actions.** Consultation actions between validators, not separate skills.
4. **If validations fail, issuer posts new claims.** Corrective or remediation claims targeting the subject. The replacement claims carry a `supersedes` Relation to the rejected claim.
5. **New claims trigger re-entry to Implementation.** Only new claims need resolution.
6. **Bounded by `MaxReviewRounds`.**

### 5.3 Phase Transition Diagram

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

### 5.4 Phase Transition Control

| Transition | Trigger | Precondition |
|---|---|---|
| Start -> Implementation | Orchestrator creates board, dispatches agents | Board populated with claims |
| Implementation -> Validation | Orchestrator observes board state | All non-superseded claims in `testified` status |
| Validation -> Implementation | Orchestrator observes new claims | At least one `pending` claim posted |
| Validation -> Complete | Orchestrator observes board state | All non-superseded claims `accepted` |
| Any -> Failed | Orchestrator detects bound exceeded | `iteration >= MaxReviewRounds` with failing validations |

---

## 6. Comparison with Current System

| Aspect | Current (Protocol State Machine) | New (Claims + Testaments) |
|---|---|---|
| **Execution model** | Sequential: Inspector -> Tester -> Worker -> Verify | Parallel: all subjects work simultaneously |
| **State representation** | `PipelineProtocolSnapshot` with 7 reducer states | `ClaimsBoard` with 2 phases |
| **Agent coordination** | Turn-based handoffs via `handoff_next`, `challenge_agent` | Actions: challenges and consultations are claim sets |
| **Work tracking** | Single task prompt per agent | Granular claims with atomic updates + testaments |
| **Response mechanism** | Handoff with status update | Testament with artifacts (proof of work) |
| **Validation** | Inspector challenges agent, processes response | Issuer validates testament artifacts against validations |
| **Error handling** | Errors returned to agent | Corrective claims issued — agent always has a path forward |
| **Quality gates** | Inspector's `grade_task_quality` (holistic) | Per-claim, per-validation quality bar statements |
| **Cross-agent communication** | Separate `challenge_peer`, `consult_peer` skills | Uniform: challenge and consult are action types |
| **Test execution** | Tester runs tests in sequential phase | Tester writes tests in Implementation, runs in Validation |
| **Pipeline terminal** | Inspector's `handoff_to_ot` -> PipelineCommitter | Board `MarkComplete` -> PipelineCommitter |
| **Scope** | Pipeline agents only | Universal — works for any agent (scribe, academic, etc.) |

---

## 7. Complete Pipeline Agent Skills Audit

### 7.1 Pipeline Inspector (69 skills currently -> 60 after)

**RETIRE (12 skills) — protocol handoff/challenge/consult machinery replaced by claims/actions:**

| Skill | Current Purpose | Replacement |
|---|---|---|
| `challenge_agent` | Issue targeted follow-up to pipeline peer | Post a challenge action (set of claims) against the peer |
| `handoff_next` | Route to next agent in sequence | Eliminated — no sequential handoffs |
| `validate_work` | Validate peer work and return findings | `evaluate_validation` — evaluate testament artifacts against validations |
| `process_validation` | Process validation responses | Board tracks testament/validation results directly |
| `finalize_pipeline` | Final accept/reject + tester handoff | Board completion triggers PipelineCommitter |
| `handoff_to_ot` | Terminal handoff to orchestrator | Board `MarkComplete` triggers extract |
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

**MODIFY (3 skills):**

| Skill | Change |
|---|---|
| `define_criteria` | Generates claims (via `post_action`) rather than standalone criteria |
| `validate_criteria` | Subsumed into `evaluate_validation` workflow |
| `inspect_open_activity` | Surfaces claim/testament conflicts from Fabric |

**KEEP UNCHANGED (51 skills):**

- Analysis/linting (7), Design validation (4), VFS/workspace (15), Command execution (2), Coordination (7), Memory forest (7), Fabric awareness (4: `query_peer_activity`, `causal_trace`, `find_related_activity`, `recall_my_history`), Validation support (3), Dependency (2), Diagnostics (2), Status (1)

### 7.2 Pipeline Tester (51 skills currently -> 45 after)

**RETIRE (9 skills):** Same 9 protocol + challenge/consult skills as Inspector minus inspector-only skills.

**ADD (5 skills):** Same 5 as Inspector: `query_claims_board`, `post_action`, `evaluate_validation`, `post_remediation_claims`, `inspect_claim_conflicts`.

**PHASE-GATE (1 skill):** `run_test_suite` — blocked during Implementation.

**KEEP UNCHANGED (39 skills):** Test authoring (8), VFS/workspace (13), Command (2), Coordination (7), Fabric awareness (4), Decision manifest (2), Other (3).

### 7.3 Engineer (52 skills currently -> 49 after)

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

### 7.4 Designer (53 skills currently -> 50 after)

**RETIRE (8 skills):** Same as Engineer.

**ADD (5 skills):** Same as Engineer.

**KEEP UNCHANGED (44 skills):** File I/O (15), Component management (3), Design tokens/a11y (5), Coordination (7), Fabric awareness (4), Decision manifest (2), Communication (6), Other (2).

### 7.5 Skills Summary

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

## 8. Fabric Integration

### 8.1 New ActionKinds

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

### 8.2 Resolution Mapping

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

### 8.3 Terminal and Paired Kinds

Terminal: `ActionClaimAccepted`, `ActionClaimRejected`, `ActionBoardComplete`

Paired: `ActionClaimIssued` -> `ActionClaimAccepted` (default), error -> `ActionClaimRejected`

### 8.4 Ambient Context

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

## 9. End-to-End Example

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

Engineer receives corrective claim, refactors test, submits testament with artifacts (code diff, test output showing consistent pass). Inspector validates — all pass.

**6. Complete:**

`MarkComplete()`. `PipelineCommitter.ExtractReviewCandidate()` promotes VFS overlay.

---

## 10. Changes by Component

### 10.1 New Package: `core/pipeline/claims/`

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
    eventCorrectiveIssued    = "corrective_issued"
    eventPhaseTransition     = "phase_transition"
    eventBoardComplete       = "board_complete"
)
```

### 10.2 Architect: Claim Generation

**Files:** `agents/architect/types.go`, `planner_anthropic.go`, `skills_planning.go`

- Add `TaskClaim` and `TaskClaimValidation` types (agent relationships expressed via Relations)
- Add `Claims []TaskClaim` to `HandoffTask` and `AtomicTask`
- Extend LLM prompt to produce precise, atomic claims (not vague task descriptions)
- Claims assembled by Architect, formally issued by Inspector

### 10.3 Orchestrator: Claims Pipeline Controller

**New file:** `agents/orchestrator/claims_pipeline.go`

- Creates board, Inspector-issues claims from Architect's assembly
- Dispatches all subjects simultaneously
- Monitors testament submissions
- Transitions to validation when all testified
- Handles remediation loop
- Calls PipelineCommitter on completion/failure

### 10.4 Agent Skills

**New file:** `agents/shared/claims_skills.go`

- `query_claims_board` — read board state
- `post_action` — issue action (set of claims) — covers challenge, consultation, corrective, task
- `submit_testaments` — submit a set of testaments (as a testament action) with artifacts
- `update_claim_progress` — atomic progress update
- `evaluate_validation` — evaluate testament artifacts against validations
- `post_remediation_claims` — reject + post replacements
- `inspect_claim_conflicts` — overlapping claims, competing testaments

### 10.5 Task Context Rendering

**File:** `agents/shared/pipeline_task_context.go`

Claims board section showing claims, testaments, artifacts, peer progress.

### 10.6 Task State

**File:** `core/pipeline/taskstate/state.go`

Add `StatusImplementing` replacing `StatusDefiningCriteria` + `StatusCreatingTests` + `StatusExecuting`.

---

## 11. Implementation Order

| Step | Deliverable |
|---|---|
| 1 | Core types: Relation, StatusChange, ClaimScopeEntry, Action, Claim, Testament, Artifact, Validation, ClaimProgressUpdate, enums |
| 2 | ClaimsBoard: struct, all operations, projection, subscription |
| 3 | WAL persistence: events, checkpoint, apply handlers, recovery |
| 4 | Fabric ActionKinds: 12 new kinds + resolution + terminal + paired |
| 5 | Board amplifier: emit activities for claims, testaments, artifacts |
| 6 | Agent skills: 7 new skill factories |
| 7 | Architect claim generation: types, planner, handoff wiring |
| 8 | Orchestrator: ClaimsPipelineController, claims dispatch path |
| 9 | Task context rendering: claims board section |
| 10 | Fabric ambient context: ClaimsBoardDigest with testaments |
| 11 | Fabric awareness skills: query_claims_board, inspect_claim_conflicts |
| 12 | Phase gating: tester run_test_suite blocked during implementation |
| 13 | Agent skill registration: conditional claims vs protocol skills |
| 14 | Task state: StatusImplementing |
| 15 | Corrective claims: skill invocation guards that issue claims instead of errors |

---

## 12. Verification

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
| Integration: End-to-end | Architect claims -> board -> subjects work -> testaments -> validation -> complete |
| Integration: Remediation | Validation fails -> corrective claims -> re-implement -> pass |
| Integration: Corrective | Agent acts out of order -> corrective claims issued -> agent adjusts -> succeeds |
| Integration: Bounded iteration | MaxReviewRounds exceeded -> rollback |
| Integration: Consultation | Engineer posts consultation action -> Designer responds with testament |
| Integration: Challenge | Inspector posts challenge action -> Engineer responds with testament |
| Fabric: Activity emission | Claims, testaments, artifacts all emit correct ActionKinds |
| Fabric: Ambient context | ClaimsBoardDigest shows testaments and artifacts |
| Fabric: Cross-pipeline | Claims/testaments from pipeline A visible to pipeline B |
| Skills: Phase gating | run_test_suite blocked during implementation |
| Skills: Disposition | Protocol skills absent for claims pipelines, claims skills present |
| Recovery: Crash resilience | Kill mid-mutation -> WAL replay -> consistent state |

---

## 13. Full System Conversion Plan

Every component, every agent, every interaction converted to claims-based execution. No exceptions.

### 13.1 Conversion Tiers

The conversion is structured in dependency order. Each tier builds on the prior tier. No tier is optional.

```
Tier 0: Core claims infrastructure (types, board, WAL, amplifier)
Tier 1: Fabric integration (ActionKinds, ambient context, lenses)
Tier 2: Pipeline agent conversion (Inspector, Tester, Engineer, Designer)
Tier 3: Architect conversion (claim generation replaces task generation)
Tier 4: Guide conversion (routing becomes action dispatch)
Tier 5: Knowledge agent conversion (Librarian, Academic, Archivalist)
Tier 6: Infrastructure agent conversion (Scribe, Guardian)
Tier 7: Orchestrator conversion (DAG nodes become actions, remove mediation)
Tier 8: Sovereign system retirement (protocol, coordination service, decision manifest)
Tier 9: System infrastructure conversion (handoff, session, VFS, error handling)
Tier 10: TUI conversion (render claims/testaments/artifacts)
Tier 11: Boot and lifecycle conversion
```

---

### 13.2 Tier 0: Core Claims Infrastructure

**What**: The foundational types, board, persistence, and amplifier. Everything else depends on this.

**Components:**

| Deliverable | Package | Description |
|---|---|---|
| Claims types | `core/claims/types.go` | Relation, StatusChange, ClaimScopeEntry, Action, Claim, Testament, Artifact, Validation, ClaimProgressUpdate, all enums. The 9 universal base fields + per-type semantic fields. |
| ClaimsBoard | `core/claims/board.go` | Sovereign store: PostAction, SubmitTestaments, EvaluateValidation, RejectClaim, phase transitions, queries, projection, subscription. Flat maps for all 5 entity types. |
| WAL persistence | `core/claims/board_durable.go` | 10 WAL event types, checkpoint struct, apply handlers, recovery via replay. Same `durableProtocolLog` pattern. |
| Board amplifier | `core/claims/board_amplifier.go` | Fabric activity emission for every board mutation. All 12 ActionKinds. |
| Corrective claims engine | `core/claims/corrective.go` | Intercepts skill precondition failures, generates corrective claim sets instead of errors. Registered as a pre-skill hook. |
| Claims skill factories | `core/claims/skills.go` | `query_claims_board`, `post_action`, `submit_testaments`, `update_claim_progress`, `evaluate_validation`, `post_remediation_claims`, `inspect_claim_conflicts`. Shared by all agents. |

**Note:** The package moves from `core/pipeline/claims/` to `core/claims/` — claims are system-wide, not pipeline-specific.

---

### 13.3 Tier 1: Fabric Integration

**What**: The Fabric learns to observe and surface claims, testaments, and artifacts.

| Deliverable | Package | Description |
|---|---|---|
| ActionKind constants | `core/activity/action_kind.go` | 12 new kinds: `claim_issued`, `claim_updated`, `testament_submitted`, `claim_artifact_published`, `claim_validated`, `claim_accepted`, `claim_rejected`, `claim_superseded`, `action_posted`, `corrective_issued`, `board_phase_changed`, `board_complete`. Wire into ResolutionFor, IsTerminal, paired kinds. |
| ClaimsBoardDigest | `core/activity/lenses/ambient.go` | Extend AmbientEnvelope with claims board state: my claims, peer progress, recent testaments, blocked claims, board phase. |
| Claims awareness skills | `core/fabric/claim_awareness_skills.go` | `query_claims_board` (Fabric lens), `query_peer_claims`, `inspect_claim_conflicts`. Register in FabricAwarenessSkillNames. |
| Claim-scoped communication | `core/fabric/awareness_skills.go` | `consult_peer` and `challenge_peer` retired as separate skills. Challenges and consultations are actions posted via `post_action`. Existing Fabric lenses query these like any other claim activity. |

---

### 13.4 Tier 2: Pipeline Agent Conversion

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
| Corrective claims on scope violation | `core/claims/corrective.go` | Engineer calling `write_pipeline_file` without scope claim triggers corrective action, not error. |

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

### 13.5 Tier 3: Architect Conversion

**What**: The Architect stops generating vague task descriptions and produces precise, atomic claims with validations.

| Change | File(s) | Description |
|---|---|---|
| TaskClaim types | `agents/architect/types.go` | Add `TaskClaim` (with Relations, validations) to `HandoffTask` and `AtomicTask`. Remove `AcceptanceCriteria`, `SuccessCriteria` as separate fields — they become validations on claims. |
| LLM prompt rewrite | `agents/architect/planner_anthropic.go` | Instruct the LLM to produce precise, atomic claims: not "implement JWT middleware" but "implement HS256 JWK deserialization" with validations like "JWK with valid key deserializes" / "returns typed struct, no silent fallbacks". |
| Claim generation in toTask | `agents/architect/planner_anthropic.go` | `toTask()` converts `claimPayload` to `TaskClaim`. Owner normalization, ID generation, validation type inference. |
| Handoff wiring | `agents/architect/skills_planning.go` | `atomicTaskToHandoff()` and `buildPlanHandoff()` carry claims through to the orchestrator. |
| Plan as action | `agents/architect/skills_planning.go` | The entire plan handoff becomes an action. The plan's tasks become claims within that action. The architect assembles; the inspector issues. |

---

### 13.6 Tier 4: Guide Conversion

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

### 13.7 Tier 5: Knowledge Agent Conversion

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

### 13.8 Tier 6: Infrastructure Agent Conversion

#### 6a. Scribe

| Change | File(s) | Description |
|---|---|---|
| Summarization as claims | `agents/scribe/` | "Summarize architect's last context window" is a claim issued against the Scribe. Scribe responds with testament: "Submitted architect context summary at 2:01AM" with artifacts: archivalist receipt, document DB ingestion response, KG vector storage response. |
| Per-agent narration as claims | `agents/scribe/` | The Scribe's continuous narration stream becomes a series of archival actions with claims against itself and the Archivalist. Each narration cycle produces testaments with ingestion artifacts. |

#### 6b. Guardian

| Change | File(s) | Description |
|---|---|---|
| Command approval as claims | `agents/guardian/`, `core/commandapproval/` | An agent needing to execute a mutating command issues a claim against the Guardian: "Approve execution of `go test ./...` in services/auth/". Validation: "Command is safe, scoped, and authorized". Guardian evaluates, responds with testament: "Approved `go test` execution" with artifacts: approval receipt, scope verification, safety assessment. |
| Content validation as claims | `agents/guardian/` | Guardian's content scanning becomes claims: "Validate output contains no credentials" with validations against the content. Testament contains scan results. |
| Corrective claims on denial | `agents/guardian/` | Instead of returning an error on command denial, Guardian issues corrective claims: "Modify command to exclude sensitive paths", "Request user authorization for elevated access". |

---

### 13.9 Tier 7: Orchestrator Conversion

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

### 13.10 Tier 8: Sovereign System Retirement

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
| `coord_claim_scope` | Scope claim: claim with scope entries, issuer=requesting agent, subject=coordinator |
| `coord_publish_artifact` | Testament with artifact: agent publishes work as testament |
| `coord_request_review` | Consultation action: agent issues review claim against peer |
| `coord_resolve_artifact` | `evaluate_validation`: reviewer evaluates testament artifacts |
| `coord_query_view` | `query_claims_board`: full board state including scope claims |
| `coord_watch_updates` | Board subscription: reactive projection updates |
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

### 13.11 Tier 9: System Infrastructure Conversion

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
| File scope as claim prerequisite | `core/versioning/`, `core/claims/corrective.go` | Every `write_pipeline_file`, `edit_pipeline_file`, `delete_pipeline_file` requires a validated scope claim. If missing, a corrective claim is issued instead of an error. |
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

### 13.12 Tier 10: TUI Conversion

**What**: The terminal UI renders claims, testaments, and artifacts instead of protocol state and task prompts.

| Change | File(s) | Description |
|---|---|---|
| Agent panel | `ui/agent/` | Shows claims board state per pipeline: claims in progress, testified, accepted/rejected. Replaces the sequential phase display (defining criteria → creating tests → executing → validating). |
| Pipeline visualization | `ui/` | Pipeline panel shows claim progress bars, testament counts, artifact counts. Color-coded by status. |
| Chat rendering | `ui/chat/` | Testaments rendered as structured responses with collapsible artifact lists. Claims rendered as task cards. |
| Claims board view | `ui/` (new) | Dedicated claims board panel: full board state, filterable by agent/status/action type. Shows the claim → testament → validation chain. |
| Conversation context | `ui/chat/` | The conversation IS the session board. Prior turns are visible as prior actions with their testaments. No lost context between turns. |

---

### 13.13 Tier 11: Boot and Lifecycle Conversion

| Change | File(s) | Description |
|---|---|---|
| Boot as claims | `core/boot/` | Boot pipeline phases (setup → detect → allocate → ingest → commit → finalize) become claims on a boot board. Each phase submits a testament with success artifacts. Boot completes when all claims are accepted. |
| Agent activation as claims | `core/container/` | Agent activation is a claim: "Activate engineer for session X". The container responds with a testament containing the agent ID, readiness status. |
| Shutdown as claims | `core/container/` | Graceful shutdown issues claims against each active agent: "Persist state and terminate". Agents respond with shutdown testaments. |

---

### 13.14 Conversion Summary

| Tier | Components | Claims Board Scope | Replaces |
|---|---|---|---|
| 0 | Core types, board, WAL, amplifier, corrective engine, skills | System-wide | Nothing (new) |
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

### 13.15 Tier 12: Persistence and Reasoning Infrastructure

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
| Lens queries = board queries | `core/activity/lenses/` | `query_peer_activity` becomes `query_claims_board` filtered by peer. `inspect_open_conflicts` becomes `inspect_claim_conflicts`. `find_related_activity` searches claim/testament descriptions. The lens layer thins — it's querying one store, not correlating across three. |
| Resolution tiers still apply | `core/activity/` | Atomic/Fine/Medium/Coarse resolution tiers still determine storage lifetime. Claim progress updates (Medium) evict faster than accepted claims (Coarse, permanent). Artifacts tagged `Ephemeral` evict after iteration; durable artifacts persist to Coarse tier. |
| Chokepoint instrumentation simplified | `core/activity/span.go` | Currently, 10+ chokepoints emit raw activities and 6+ amplifiers project sovereign state. With one sovereign source, the amplifier count drops to 1 (the claims board amplifier). Chokepoints still emit infrastructure activities (LLM calls, file writes, command executions) but semantic activities all flow through claims. |

#### 12e. Bleve (Full-Text Search)

| Change | File(s) | Description |
|---|---|---|
| Index claims and testaments | Bleve subscriber | The Bleve full-text index currently indexes Fabric activities. With claims, it indexes claim descriptions, testament summaries, validation descriptions, quality bars, and artifact references. Searching "HS256 deserialization" returns claims AND testaments AND their artifacts. |
| Faceted search by entity type | Bleve subscriber | Search results faceted by: claims (what was asked), testaments (what was done), artifacts (what proof exists), validations (what was checked). Currently facets are by ActionKind — claims give semantic facets. |

---

### 13.16 Tier Summary (Updated)

| Tier | Components | Replaces |
|---|---|---|
| 0 | Core types, board, WAL, amplifier, corrective engine, skills | Nothing (new) |
| 1 | Fabric ActionKinds, ambient context, awareness skills | Partial Fabric integration |
| 2 | Inspector, Tester, Engineer, Designer (pipeline) | Pipeline Protocol |
| 3 | Architect | AcceptanceCriteria, SuccessCriteria, task prompts |
| 4 | Guide | ConversationHistory, ForwardedRequest routing |
| 5 | Librarian, Academic, Archivalist | `consult` skill, consultation cache |
| 6 | Scribe, Guardian | Narration stream, command approval gates |
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
