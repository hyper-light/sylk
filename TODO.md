# Sylk Implementation TODO

## Executive Summary

Build a highly concurrent multi-agent system capable of:
- **Dozens of concurrent sessions** with isolated workflows
- **Hundreds of sub-agents** per session executing DAG-based workflows
- **Maximized concurrency** through worker pools, sharded data structures, and parallel DAG execution
- **Zero direct agent-to-agent communication** - all messages route through Guide
- **Session isolation** with shared historical knowledge access
- **Progressive skill disclosure** - load capabilities only when needed

## Current State

| Component | Status | Location |
|-----------|--------|----------|
| Guide (Universal Router) | Needs Enhancement | `agents/guide/` |
| Archivalist (Historical RAG) | Needs Enhancement | `agents/archivalist/` |
| Core Messaging | Complete | `core/messaging/` |
| Core Skills | Complete | `core/skills/` |
| Session Manager | Not Started | - |
| DAG Engine | Not Started | - |
| Academic | Not Started | - |
| Architect | Not Started | - |
| Orchestrator | Not Started | - |
| Engineer | Not Started | - |
| Librarian | Not Started | - |
| Inspector | Not Started | - |
| Tester | Not Started | - |

---

## Existing Agent Modifications

This section details the specific changes required to make the existing Guide and Archivalist agents compatible with the multi-session architecture.

### Guide Agent Modifications (`agents/guide/`)

The existing Guide agent needs significant modifications to support multi-session routing, session-scoped state, and the new agent ecosystem.

#### Required Code Changes

**1. Session-Aware Routing (`guide.go`)**
```go
// CURRENT: Single-session routing
type Guide struct {
    sessionID string
    // ...
}

// REQUIRED: Multi-session support
type Guide struct {
    sessionManager    SessionManager           // Reference to session manager
    sessionRouters    map[string]*SessionRouter // Per-session routing state
    globalRouteCache  *RouteCache              // Shared route cache (read-only fallback)
    sharedAgents      map[string]Agent         // Librarian, Academic, Archivalist
    // ...
}

// Add session context to all routing methods
func (g *Guide) RouteWithSession(ctx context.Context, sessionID string, req *RouteRequest) (*RouteResult, error)
func (g *Guide) GetSessionRouter(sessionID string) (*SessionRouter, error)
func (g *Guide) CreateSessionRouter(sessionID string) (*SessionRouter, error)
func (g *Guide) CloseSessionRouter(sessionID string) error
```

**2. New SessionRouter Type (`session_router.go` - new file)**
```go
type SessionRouter struct {
    sessionID        string
    routeCache       *RouteCache           // Session-scoped cache
    pendingRequests  *PendingRequestStore  // Session-scoped pending
    agentRegistry    map[string]Agent      // Session-scoped agents (Architect, Orchestrator, etc.)
    subscriptions    []func()              // Cleanup functions for session topics
    mu               sync.RWMutex
}
```

**3. Message Type Extensions (`types.go`)**
- [ ] Add all new message types (DAG_EXECUTE, DAG_STATUS, DAG_CANCEL, etc.)
- [ ] Add SessionID field to RouteRequest
- [ ] Add SessionID field to RouteResult
- [ ] Add message priority field
- [ ] Add correlation ID for request/response matching

**4. Bus Integration Updates (`bus.go`)**
- [ ] Add session topic creation: `session.{id}.{agent}.{channel}`
- [ ] Add session topic cleanup on session close
- [ ] Add wildcard subscription support
- [ ] Add message filtering by session ID

**5. Skill System Updates (`skills.go` - new file)**
- [ ] Implement `SkillLoader` interface for progressive disclosure
- [ ] Add skill registration by tier (Core, Domain, Specialized)
- [ ] Add skill loading triggers (domain keywords, explicit request)
- [ ] Add skill unloading with LRU eviction
- [ ] Add token budget tracking for loaded skills

**6. Hook System Updates**
- [ ] Add pre-route hook injection point
- [ ] Add post-route hook injection point
- [ ] Add pre-dispatch hook injection point
- [ ] Add post-dispatch hook injection point
- [ ] Implement hook chain execution with abort capability

#### Required Interface Changes

```go
// Current Skills() signature - needs enhancement
func (g *Guide) Skills() []skills.Skill

// Required signature
func (g *Guide) Skills() []skills.Skill                    // Core skills (always loaded)
func (g *Guide) ExtendedSkills() []skills.Skill            // Extended skills (on demand)
func (g *Guide) LoadSkill(name string) error               // Load specific skill
func (g *Guide) UnloadSkill(name string) error             // Unload skill
func (g *Guide) LoadedSkills() []string                    // Currently loaded skills
func (g *Guide) SkillTokenBudget() (used int, max int)     // Token tracking

// Current Hooks() signature - needs enhancement
func (g *Guide) Hooks() skills.AgentHooks

// Required hook types
type GuideHooks struct {
    PreRoute     []func(ctx context.Context, req *RouteRequest) (*RouteRequest, error)
    PostRoute    []func(ctx context.Context, req *RouteRequest, result *RouteResult) error
    PreDispatch  []func(ctx context.Context, target string, msg *Message) (*Message, error)
    PostDispatch []func(ctx context.Context, target string, msg *Message, result any) error
}
```

#### Required New Methods

```go
// Session management
func (g *Guide) AttachSession(session *Session) error
func (g *Guide) DetachSession(sessionID string) error
func (g *Guide) ActiveSession() (*Session, bool)
func (g *Guide) SwitchSession(sessionID string) error

// Agent registration (session-scoped)
func (g *Guide) RegisterSessionAgent(sessionID string, agent Agent) error
func (g *Guide) UnregisterSessionAgent(sessionID string, agentID string) error
func (g *Guide) GetSessionAgents(sessionID string) []Agent

// Shared agent access
func (g *Guide) GetSharedAgent(agentID string) (Agent, bool)
func (g *Guide) RegisterSharedAgent(agent Agent) error

// Message routing
func (g *Guide) RouteToSession(sessionID string, msg *Message) error
func (g *Guide) BroadcastToSession(sessionID string, msg *Message) error

// Metrics (session-aware)
func (g *Guide) SessionMetrics(sessionID string) *SessionRouteMetrics
func (g *Guide) GlobalMetrics() *GlobalRouteMetrics
```

#### Files to Modify

| File | Changes |
|------|---------|
| `guide.go` | Add SessionManager reference, multi-session support, session-aware routing |
| `types.go` | Add new message types, SessionID fields, priority field |
| `routing.go` | Session-scoped route resolution, global fallback |
| `bus.go` | Session topic support, wildcard subscriptions |
| `route_cache.go` | Session-scoped cache with global fallback |
| `worker_pool.go` | Session-aware job scheduling, fair allocation |

#### Files to Create

| File | Purpose |
|------|---------|
| `session_router.go` | Per-session routing state and logic |
| `message_types.go` | New message type definitions |
| `skills.go` | Skill loading/unloading, progressive disclosure |
| `hooks.go` | Hook chain implementation |

---

### Archivalist Agent Modifications (`agents/archivalist/`)

The existing Archivalist agent needs modifications to enforce session isolation, support cross-session queries, and store workflow history.

#### Required Code Changes

**1. Session Enforcement (`archivalist.go`)**
```go
// CURRENT: Optional session tracking
type Archivalist struct {
    // sessionID used but not enforced
}

// REQUIRED: Mandatory session enforcement
type Archivalist struct {
    defaultSessionID string                    // Default session for writes
    sessionStores    map[string]*SessionStore  // Per-session storage partitions
    crossSessionIdx  *CrossSessionIndex        // Index for cross-session queries
    workflowStore    *WorkflowStore            // DAG execution history
    // ...
}

// All write operations MUST include session
func (a *Archivalist) Store(entry *Entry) error                          // Uses defaultSessionID
func (a *Archivalist) StoreInSession(sessionID string, entry *Entry) error // Explicit session

// All read operations default to current session
func (a *Archivalist) Query(query *ArchiveQuery) ([]*Entry, error)       // Current session only
func (a *Archivalist) QueryCrossSession(query *ArchiveQuery) ([]*Entry, error) // All sessions
```

**2. Session Store Type (`session_store.go` - new file)**
```go
type SessionStore struct {
    sessionID      string
    entries        map[string]*Entry
    facts          *FactStore           // Session-local facts
    summaries      []*CompactedSummary  // Session summaries
    tokenCount     int                  // Hot memory token tracking
    mu             sync.RWMutex
}

// Session-scoped operations
func (s *SessionStore) Insert(entry *Entry) error
func (s *SessionStore) Get(id string) (*Entry, bool)
func (s *SessionStore) Query(query *ArchiveQuery) ([]*Entry, error)
func (s *SessionStore) Archive(entryID string) error
func (s *SessionStore) TokenBudget() (used int, max int)
```

**3. Cross-Session Query Support (`cross_session.go` - new file)**
```go
type CrossSessionIndex struct {
    byCategory  map[Category][]string    // Entry IDs by category (all sessions)
    bySource    map[SourceModel][]string // Entry IDs by source
    byKeyword   map[string][]string      // Entry IDs by keyword
    sessionMap  map[string]string        // Entry ID → Session ID
    mu          sync.RWMutex
}

func (c *CrossSessionIndex) Search(query *ArchiveQuery) ([]CrossSessionResult, error)
func (c *CrossSessionIndex) IndexEntry(sessionID string, entry *Entry) error
func (c *CrossSessionIndex) RemoveEntry(entryID string) error

type CrossSessionResult struct {
    Entry     *Entry
    SessionID string
    Score     float64  // Relevance score
}
```

**4. Workflow Storage (`workflow_store.go` - new file)**
```go
type WorkflowStore struct {
    dags      map[string]*StoredDAG       // DAG ID → definition
    runs      map[string]*DAGRun          // Run ID → execution record
    bySession map[string][]string         // Session ID → Run IDs
    mu        sync.RWMutex
}

type StoredDAG struct {
    ID          string
    Definition  *DAG
    SessionID   string
    CreatedAt   time.Time
    ExecutionCount int
}

type DAGRun struct {
    ID           string
    DAGID        string
    SessionID    string
    StartTime    time.Time
    EndTime      *time.Time
    Status       DAGStatus
    NodeResults  map[string]*NodeResult
    Corrections  []*Correction
    FinalOutcome string
}

func (w *WorkflowStore) StoreDAG(sessionID string, dag *DAG) (string, error)
func (w *WorkflowStore) RecordRun(run *DAGRun) error
func (w *WorkflowStore) QuerySimilarWorkflows(query string) ([]*StoredDAG, error)
func (w *WorkflowStore) GetSessionHistory(sessionID string) ([]*DAGRun, error)
```

**5. Type Updates (`types.go`)**
- [ ] Add `IncludeArchived` flag to `ArchiveQuery` (already exists, verify usage)
- [ ] Add `SessionIDs` filter for cross-session queries (already exists, enhance)
- [ ] Add `WorkflowQuery` struct for DAG history queries
- [ ] Add `CrossSessionResult` wrapper type
- [ ] Add `DAGRun` and `StoredDAG` types

**6. Skill System Updates (`skills.go` - new file)**
```go
// Core Skills (always loaded)
var CoreSkills = []skills.Skill{
    skills.NewSkill("store").Description("Store an entry").Domain("storage"),
    skills.NewSkill("query").Description("Query entries").Domain("storage"),
    skills.NewSkill("briefing").Description("Get session briefing").Domain("status"),
}

// Extended Skills (on demand)
var ExtendedSkills = []skills.Skill{
    skills.NewSkill("cross_session_query").Description("Query across sessions").Domain("history"),
    skills.NewSkill("workflow_history").Description("Query past workflows").Domain("history"),
    skills.NewSkill("token_savings").Description("Get token savings report").Domain("metrics"),
    skills.NewSkill("session_timeline").Description("Get session event timeline").Domain("history"),
    skills.NewSkill("pattern_search").Description("Find similar patterns").Domain("patterns"),
    skills.NewSkill("failure_search").Description("Find similar failures").Domain("patterns"),
    skills.NewSkill("decision_search").Description("Find similar decisions").Domain("patterns"),
    skills.NewSkill("promote_to_global").Description("Promote entry to global scope").Domain("admin"),
}
```

#### Required Interface Changes

```go
// Current interface - needs enhancement
type Archivalist interface {
    Store(entry *Entry) (*SubmissionResult, error)
    Query(query *ArchiveQuery) ([]*Entry, error)
    // ...
}

// Required interface additions
type Archivalist interface {
    // Session management
    SetDefaultSession(sessionID string)
    GetDefaultSession() string

    // Session-scoped operations
    StoreInSession(sessionID string, entry *Entry) (*SubmissionResult, error)
    QueryInSession(sessionID string, query *ArchiveQuery) ([]*Entry, error)

    // Cross-session operations (read-only)
    QueryCrossSession(query *ArchiveQuery) ([]CrossSessionResult, error)
    GetSessionList() []SessionInfo
    GetSessionSummary(sessionID string) (*SessionSummary, error)

    // Workflow storage
    StoreWorkflow(sessionID string, dag *DAG) (string, error)
    RecordWorkflowRun(run *DAGRun) error
    QuerySimilarWorkflows(query string) ([]*StoredDAG, error)
    GetWorkflowHistory(sessionID string, limit int) ([]*DAGRun, error)

    // Token/savings tracking
    GetTokenSavings(sessionID string) (*TokenSavingsReport, error)
    GetGlobalTokenSavings() (*TokenSavingsReport, error)

    // Promotion (session-local → global)
    PromoteToGlobal(entryID string, reason string) error
}
```

#### Required New Methods

```go
// Session lifecycle hooks
func (a *Archivalist) OnSessionCreate(sessionID string) error
func (a *Archivalist) OnSessionClose(sessionID string) error
func (a *Archivalist) OnSessionSwitch(fromID, toID string) error

// Snapshot support for session persistence
func (a *Archivalist) CreateSessionSnapshot(sessionID string) (*ChronicleSnapshot, error)
func (a *Archivalist) RestoreSessionSnapshot(sessionID string, snapshot *ChronicleSnapshot) error

// Compaction (cross-session aware)
func (a *Archivalist) CompactSession(sessionID string) error
func (a *Archivalist) CompactGlobal() error
```

#### Files to Modify

| File | Changes |
|------|---------|
| `archivalist.go` | Session enforcement, cross-session query support |
| `storage.go` | Session-partitioned storage, workflow storage |
| `types.go` | New types for workflow storage, cross-session results |
| `query_cache.go` | Session-scoped caching, cross-session cache invalidation |

#### Files to Create

| File | Purpose |
|------|---------|
| `session_store.go` | Per-session storage partition |
| `cross_session.go` | Cross-session index and query logic |
| `workflow_store.go` | DAG definition and execution history storage |
| `skills.go` | Skill definitions with progressive disclosure |
| `hooks.go` | Hook implementations for session lifecycle |

---

### Migration Path

The following sequence ensures backward compatibility during migration:

1. **Phase A: Add Session Support (Non-Breaking)**
   - Add SessionID fields with defaults
   - Add new methods alongside existing ones
   - Existing code continues to work

2. **Phase B: Enable Session Isolation**
   - Enable session enforcement flag
   - Migrate existing data to "default" session
   - Update all callers to provide session context

3. **Phase C: Remove Legacy Methods**
   - Deprecate non-session-aware methods
   - Remove after all callers migrated
   - Enforce session context on all operations

---

## Phase 0: Infrastructure Foundation

**Goal**: Build shared infrastructure that all agents depend on.

**Dependencies**: None (can start immediately)

**Parallelization**: Items 0.1, 0.2, 0.3, 0.4, 0.5 can execute in parallel.

### 0.1 Session Manager

Creates and manages isolated sessions with their own state.

**Files to create:**
- `core/session/types.go`
- `core/session/session.go`
- `core/session/manager.go`
- `core/session/context.go`
- `core/session/persistence.go`
- `core/session/session_test.go`

**Acceptance Criteria:**

#### Session Lifecycle
- [ ] `Session` struct with unique ID, creation time, state, metadata
- [ ] Session state enum: `Created`, `Active`, `Paused`, `Suspended`, `Completed`, `Failed`
- [ ] `SessionManager` with `Create()`, `Get()`, `List()`, `Switch()`, `Pause()`, `Resume()`, `Close()` methods
- [ ] Thread-safe operations via sharded map
- [ ] Maximum concurrent sessions limit with backpressure
- [ ] Graceful shutdown with child resource cleanup

#### Session Context
- [ ] `SessionContext` holds all session-scoped state
- [ ] Isolated Guide instance per session (or session-scoped routing)
- [ ] Isolated agent registrations per session
- [ ] Session-scoped route cache
- [ ] Session-scoped pending requests
- [ ] Session-scoped workflow state (current DAG, phase, progress)

#### Session Switching
- [ ] `Switch(sessionID)` pauses current session, activates target
- [ ] Context preservation on pause (serialize state)
- [ ] Context restoration on resume (deserialize state)
- [ ] Active session tracking (only one active per user)

#### Session Persistence
- [ ] Save session state to disk on pause/suspend
- [ ] Restore session state from disk on resume
- [ ] Session metadata persistence (name, description, branch, timestamps)
- [ ] Automatic session save on graceful shutdown

#### Session Isolation Boundaries
- [ ] Session-local: Guide routing state, agent instances, workflow state, route cache
- [ ] Session-shared (read-only): Archivalist historical DB, Librarian index, Academic sources
- [ ] No context pollution between sessions

**Tests:**
- [ ] Create 100 sessions concurrently, verify isolation
- [ ] Switch between sessions, verify context preservation
- [ ] Pause/resume session, verify state restored
- [ ] Concurrent access to shared Archivalist, verify no pollution

```go
// Required interfaces
type SessionManager interface {
    Create(ctx context.Context, cfg SessionConfig) (*Session, error)
    Get(id string) (*Session, bool)
    GetActive() (*Session, bool)
    List() []*Session
    Switch(id string) error
    Pause(id string) error
    Resume(id string) error
    Close(id string) error
    CloseAll() error
    Stats() SessionManagerStats
}

type SessionContext interface {
    ID() string
    Guide() *guide.Guide
    Bus() guide.EventBus
    State() SessionState
    Metadata() map[string]any
    SetMetadata(key string, value any)
    Serialize() ([]byte, error)
    Restore(data []byte) error
}
```

### 0.2 DAG Engine

Executes directed acyclic graph workflows with parallel layer execution.

**Files to create:**
- `core/dag/types.go`
- `core/dag/node.go`
- `core/dag/dag.go`
- `core/dag/validator.go`
- `core/dag/executor.go`
- `core/dag/scheduler.go`
- `core/dag/dag_test.go`

**Acceptance Criteria:**

#### DAG Structure
- [ ] `DAG` struct with nodes, edges, execution order, policy
- [ ] `DAGNode` with Name (human-readable key, e.g. `create_dashboard`), agent type, prompt, context, dependencies, metadata
- [ ] **Node names are human-readable, snake_case, unique within DAG** (generated by Architect)
- [ ] `execution_order` as layered array (each layer runs in parallel)
- [ ] Policy: `max_concurrency`, `fail_fast`, `default_retry`

#### Node State Machine
- [ ] Node states: `Pending`, `Queued`, `Running`, `Succeeded`, `Failed`, `Blocked`, `Skipped`, `Cancelled`
- [ ] State transitions with validation
- [ ] State change events for observability

#### Validation
- [ ] Topological sort validation (reject cycles)
- [ ] Dependency validation (all deps exist)
- [ ] Agent type validation (agent exists)

#### Execution
- [ ] `DAGExecutor` executes layer-by-layer with bounded concurrency
- [ ] Dependency resolution (node starts only when all deps succeed)
- [ ] Result propagation from parent nodes to dependent nodes
- [ ] `fail_fast` policy support (stop on first failure)
- [ ] `continue_on_failure` policy support (mark deps as skipped)
- [ ] Per-node timeout with cancellation
- [ ] Per-node retry with backoff
- [ ] Cancellation support (cancel all pending/running nodes)

#### Context Propagation
- [ ] Node receives context from completed dependencies
- [ ] Output artifacts from parent nodes available to children
- [ ] Session context available to all nodes

**Tests:**
- [ ] Execute 5-layer DAG with 20 nodes, verify correct ordering
- [ ] Test fail_fast with mid-DAG failure
- [ ] Test continue_on_failure with skipped nodes
- [ ] Test cancellation mid-execution

```go
// Required interfaces
type DAGExecutor interface {
    Execute(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error)
    Cancel() error
    Status() *DAGStatus
    Subscribe(handler DAGEventHandler) func()
}

type NodeDispatcher interface {
    Dispatch(ctx context.Context, node *DAGNode, parentResults map[string]*NodeResult) (*NodeResult, error)
}

type DAGEventHandler func(event *DAGEvent)
```

### 0.3 Worker Pool Enhancements

Extend existing worker pool for multi-session fair scheduling.

**Files to modify:**
- `agents/guide/worker_pool.go`

**Files to create:**
- `core/pool/priority_pool.go`
- `core/pool/session_pool.go`
- `core/pool/fairness.go`
- `core/pool/pool_test.go`

**Acceptance Criteria:**

#### Priority Pool
- [ ] `PriorityWorkerPool` with priority lanes (Critical, High, Normal, Low, Background)
- [ ] Priority-based job selection (higher priority first)
- [ ] Job stealing between priority lanes when idle
- [ ] Starvation prevention (age-based promotion)

#### Session-Aware Pool
- [ ] `SessionAwarePool` with fair scheduling across sessions
- [ ] Per-session job limits (prevent session starvation)
- [ ] Per-session job queues
- [ ] Round-robin or weighted-fair selection across sessions
- [ ] Session job counts and wait times

#### Global Limits
- [ ] Global concurrency cap across all sessions
- [ ] Per-agent-type concurrency limits (e.g., max 10 Engineers)
- [ ] Dynamic limit adjustment based on load

#### Metrics
- [ ] Jobs per session
- [ ] Wait time per priority lane
- [ ] Processing time distribution
- [ ] Queue depths

**Tests:**
- [ ] Submit 1000 jobs from 10 sessions, verify fair distribution
- [ ] Verify priority ordering
- [ ] Verify starvation prevention

### 0.4 Guide Enhancements

Extend Guide for multi-session support and new message types.

**Files to modify:**
- `agents/guide/guide.go`
- `agents/guide/types.go`
- `agents/guide/routing.go`
- `agents/guide/bus.go`
- `agents/guide/route_cache.go`

**Files to create:**
- `agents/guide/session_router.go`
- `agents/guide/message_types.go`
- `agents/guide/skills.go`

**Acceptance Criteria:**

#### Session-Aware Routing
- [ ] `SessionRouter` wraps Guide with session context
- [ ] Session ID in all route requests
- [ ] Session-scoped agent registration
- [ ] Session-scoped route cache (with global fallback)
- [ ] Session-scoped pending request store

#### New Message Types
- [ ] `DAG_EXECUTE` - Architect → Orchestrator
- [ ] `DAG_STATUS` - Orchestrator → Architect
- [ ] `DAG_CANCEL` - User → Orchestrator
- [ ] `TASK_DISPATCH` - Orchestrator → Engineer
- [ ] `TASK_COMPLETE` - Engineer → Orchestrator
- [ ] `TASK_FAILED` - Engineer → Orchestrator
- [ ] `TASK_HELP` - Engineer → Orchestrator
- [ ] `CLARIFICATION_REQUEST` - Architect → User
- [ ] `CLARIFICATION_RESPONSE` - User → Architect
- [ ] `VALIDATE_TASK` - Orchestrator → Inspector
- [ ] `VALIDATION_RESULT` - Inspector → Orchestrator
- [ ] `VALIDATION_FULL` - Inspector → User
- [ ] `VALIDATION_CORRECTIONS` - Inspector → Architect
- [ ] `TEST_PLAN_REQUEST` - Architect → Tester
- [ ] `TEST_PLAN_RESPONSE` - Tester → User
- [ ] `TEST_DAG_REQUEST` - Tester → Architect
- [ ] `TESTS_READY` - Orchestrator → Tester
- [ ] `TEST_RESULTS` - Tester → User
- [ ] `TEST_CORRECTIONS` - Tester → Architect
- [ ] `USER_OVERRIDE` - User → Inspector/Tester
- [ ] `USER_INTERRUPT` - User → Architect
- [ ] `WORKFLOW_COMPLETE` - Architect → User

#### Session-Scoped Topics
- [ ] Topic pattern: `session.{id}.{agent}.requests`
- [ ] Global topics for cross-session (e.g., `archivalist.query`)
- [ ] Topic routing based on session context

#### Intent Classification for Librarian Routing

**CRITICAL**: When routing to Librarian, the Guide extracts additional metadata to enable intent-aware caching - at **zero additional token cost** (same LLM call as routing).

**Files to add:**
- `agents/guide/librarian_intent.go`

**Acceptance Criteria:**

##### Intent Types for Librarian Queries
- [ ] `QueryIntent` enum: `LOCATE`, `PATTERN`, `EXPLAIN`, `GENERAL`
- [ ] Classification happens during routing (no extra LLM call)
- [ ] Subject/concept extraction (e.g., "auth code" → "authentication")

| Intent | Trigger Patterns | Example |
|--------|-----------------|---------|
| LOCATE | "where is", "find", "show me", "which file" | "where is the auth code" |
| PATTERN | "strategy", "approach", "pattern", "how do we" | "what is our caching strategy" |
| EXPLAIN | "how does", "explain", "what does X do" | "how does CreateSession work" |
| GENERAL | Other codebase questions | "what languages are used" |

##### Routing Prompt Enhancement
- [ ] Add LibrarianRoutingInstructions to Guide's system prompt
- [ ] Extract intent and subject during Librarian routing
- [ ] Include confidence score in metadata

```go
// Added to Guide's routing prompt
const LibrarianRoutingInstructions = `
When routing to Librarian, also classify the query:

Intent (required):
- LOCATE: User wants to find where something is (file, function, struct)
- PATTERN: User asks about patterns, strategies, approaches, conventions
- EXPLAIN: User wants to understand how something works
- GENERAL: Other codebase questions

Subject (required): Primary entity/concept being asked about.
Normalize to snake_case (e.g., "auth code" → "authentication")

Examples:
- "where is the auth middleware" → LOCATE, "auth_middleware"
- "what is our caching strategy" → PATTERN, "caching"
- "how does CreateSession work" → EXPLAIN, "create_session"
`
```

##### Routed Message Enhancement
- [ ] `LibrarianRoutingMetadata` added to messages routed to Librarian
- [ ] Metadata includes: Intent, Subject, Confidence

```go
type LibrarianRoutingMetadata struct {
    Intent     QueryIntent `json:"intent"`
    Subject    string      `json:"subject"`
    Confidence float64     `json:"confidence"`
}

// Message to Librarian includes metadata
type LibrarianRequest struct {
    Query       string                    `json:"query"`
    SessionID   string                    `json:"session_id"`
    Metadata    *LibrarianRoutingMetadata `json:"metadata,omitempty"`
}
```

##### Token Cost Analysis
```
WITHOUT Intent Classification:
  Guide: ~200 tokens (routing only)
  Librarian: ~3000 tokens (every query)
  TOTAL: ~3200 tokens per query

WITH Intent Classification (cache hit):
  Guide: ~250 tokens (routing + intent, same call)
  Librarian: 0 tokens (cache hit)
  TOTAL: ~250 tokens per query

At 70% cache hit rate:
  100 queries old: 320,000 tokens
  100 queries new: 115,000 tokens
  SAVINGS: 64%
```

**Tests:**
- [ ] Test intent classification for LOCATE queries
- [ ] Test intent classification for PATTERN queries
- [ ] Test intent classification for EXPLAIN queries
- [ ] Test intent classification fallback to GENERAL
- [ ] Test subject extraction normalization
- [ ] Test confidence scoring
- [ ] Verify no additional LLM call (same as routing)

#### Guide Skills (Progressive Disclosure)

**Core Skills (always loaded):**
- [ ] `route` skill - classify and route requests (supports @to:agent syntax)
- [ ] `guide_route` skill - route prompt to specific agent via @guide:<agent> tag
- [ ] `task_interact` skill - route user message to specific pipeline via /task command (see Phase 2.3)
- [ ] `help` skill - provide routing help
- [ ] `status` skill - system status
- [ ] `agents` skill - list registered agents
- [ ] `route_to` skill - route message to another agent (common skill)
- [ ] `reply_to` skill - reply to routed message (common skill)
- [ ] `broadcast` skill - broadcast to multiple agents (common skill)

**@guide:<agent> Routing:**
```go
// guide_route - Route prompt to specific agent via @guide:<agent> tag
skills.NewSkill("guide_route").
    Description("Route a prompt directly to a specific agent via @guide:<agent> tag").
    Domain("routing").
    Keywords("@guide", "route", "direct", "agent").
    EnumParam("agent", "Target agent", []string{
        "academic",
        "architect",
        "librarian",
        "archivalist",
        "tester",
        "inspector",
    }, true).
    StringParam("prompt", "Prompt to route to the agent", true).
    ObjectParam("context", "Additional context to include", false)

// Example usage:
// @guide:architect "plan a new authentication feature"
// @guide:librarian "find all files using the database package"
// @guide:academic "research best practices for rate limiting"
// @guide:archivalist "find similar past decisions"
// @guide:tester "run tests for the auth module"
// @guide:inspector "validate the new changes"
```

**Extended Skills (loaded on demand):**
- [ ] `sessions` skill - list sessions
- [ ] `metrics` skill - routing metrics
- [ ] `switch_session` skill - switch active session
- [ ] `create_session` skill - create new session
- [ ] `close_session` skill - close session

#### Guide Tools
```go
var GuideTools = []ToolDefinition{
    // Core routing
    {Name: "guide_route", Skill: "route"},
    {Name: "guide_agent_route", Skill: "guide_route"},  // @guide:<agent> routing
    {Name: "guide_task_interact", Skill: "task_interact"},  // /task command for pipelines
    {Name: "guide_help", Skill: "help"},
    {Name: "guide_status", Skill: "status"},
    {Name: "guide_agents", Skill: "agents"},
    // Session management
    {Name: "guide_sessions", Skill: "sessions"},
    {Name: "guide_switch_session", Skill: "switch_session"},
    {Name: "guide_create_session", Skill: "create_session"},
    {Name: "guide_close_session", Skill: "close_session"},
    // Metrics
    {Name: "guide_metrics", Skill: "metrics"},
    // Routing (common skills)
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "broadcast", Skill: "broadcast"},
}
```

#### Guide Memory Management

**Thresholds**: 50%, 75%, 90% (checkpoint) | 95% (compact)

**Files to create:**
- `agents/guide/memory.go`
- `agents/guide/checkpoint.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Trigger checkpoint at 50%, 75%, 90%
- [ ] Trigger compaction at 95%

##### Checkpoint Summaries (Routing Knowledge)
- [ ] `GuideSummary` struct with routing information:
  - [ ] KnownRoutings (pattern → agent mapping)
  - [ ] FrequentMatches (common routes with confidence)
  - [ ] FailedRoutings (routes that didn't work)
  - [ ] AgentCapabilities (observed capabilities per agent)
  - [ ] RequestPatterns (common request types)
  - [ ] SessionRoutingStats
- [ ] Submit checkpoint to Archivalist with category `guide_checkpoint`

```go
type GuideSummary struct {
    Timestamp           time.Time                `json:"timestamp"`
    SessionID           string                   `json:"session_id"`
    ContextUsage        float64                  `json:"context_usage"`
    CheckpointIndex     int                      `json:"checkpoint_index"`
    KnownRoutings       map[string]string        `json:"known_routings"`
    FrequentMatches     []RoutingMatch           `json:"frequent_matches"`
    FailedRoutings      []FailedRouting          `json:"failed_routings"`
    AgentCapabilities   map[string][]string      `json:"agent_capabilities"`
    RequestPatterns     []RequestPattern         `json:"request_patterns"`
    SessionRoutingStats RoutingStats             `json:"session_routing_stats"`
}
```

##### Compaction at 95%
- [ ] Create final checkpoint before compacting
- [ ] Clear verbose routing logs from context
- [ ] Retain merged routing knowledge
- [ ] Target: ~30% context usage after compaction

#### Guide Hooks
- [ ] Pre-route hook: inject session context
- [ ] Post-route hook: update session state
- [ ] Pre-dispatch hook: validate target agent health
- [ ] Post-dispatch hook: record routing decision
- [ ] Context-threshold hook: trigger checkpoint at 50%, 75%, 90%
- [ ] Context-threshold hook: trigger compaction at 95%

**Tests:**
- [ ] Route requests with session context
- [ ] Verify session isolation in route cache
- [ ] Test new message types round-trip
- [ ] Test skill loading on demand
- [ ] Test checkpoint creation at 50%, 75%, 90%
- [ ] Test compaction at 95%
- [ ] Test routing knowledge preservation after compaction

#### Intent Classification for RAG Agents

**CRITICAL**: The Guide extracts intent metadata during routing (zero additional token cost) to enable intent-aware caching for both Librarian and Archivalist.

**Files to create:**
- `agents/guide/intent_classifier.go`

**Files to modify:**
- `agents/guide/guide.go`
- `agents/guide/routing.go`

##### Librarian Intent Classification

```go
// QueryIntent classifies Librarian queries for caching
type QueryIntent int

const (
    QueryIntentLocate  QueryIntent = iota  // "where is X", "find X"
    QueryIntentPattern                      // "what is our X strategy"
    QueryIntentExplain                      // "how does X work"
    QueryIntentGeneral                      // Fallback
)

// LibrarianRoutingMetadata extracted during routing
type LibrarianRoutingMetadata struct {
    Intent     QueryIntent `json:"intent"`
    Subject    string      `json:"subject"`     // Normalized subject (e.g., "auth_middleware")
    Confidence float64     `json:"confidence"`  // Use cache if > 0.8
}
```

##### Archivalist Intent Classification

```go
// ArchivalistIntent classifies Archivalist queries for caching
type ArchivalistIntent int

const (
    ArchivalistIntentHistorical ArchivalistIntent = iota  // "how did we handle X"
    ArchivalistIntentActivity                              // "what changed recently"
    ArchivalistIntentOutcome                               // "did X work"
    ArchivalistIntentSimilar                               // "similar issues to this"
    ArchivalistIntentResume                                // "where did we leave off"
    ArchivalistIntentGeneral                               // Fallback
)

// ArchivalistRoutingMetadata extracted during routing
type ArchivalistRoutingMetadata struct {
    Intent     ArchivalistIntent `json:"intent"`
    Subject    string            `json:"subject"`     // Normalized subject (e.g., "auth_migration")
    TimeScope  string            `json:"time_scope"`  // Optional: "last_month", "recently", ""
    Confidence float64           `json:"confidence"`  // Use cache if > 0.8
}
```

##### Routing Prompt Enhancement

```go
const RAGRoutingInstructions = `
When routing to Librarian, classify the query:
- Intent: LOCATE | PATTERN | EXPLAIN | GENERAL
- Subject: Primary entity (snake_case)

When routing to Archivalist, classify the query:
- Intent: HISTORICAL | ACTIVITY | OUTCOME | SIMILAR | RESUME | GENERAL
- Subject: Primary concept (snake_case)
- Time Scope: "last_month", "yesterday", "recently", "" (optional)

Examples:
Librarian:
- "where is auth middleware" → LOCATE, "auth_middleware"
- "what is our caching strategy" → PATTERN, "caching"

Archivalist:
- "how did we handle auth migration" → HISTORICAL, "auth_migration", ""
- "what changed in API recently" → ACTIVITY, "api", "recently"
`
```

##### Unified RoutedMessage with Intent

```go
// RoutedMessage includes intent metadata for cache lookup
type RoutedMessage struct {
    Query     string `json:"query"`
    Target    string `json:"target"`
    SessionID string `json:"session_id"`

    // Intent metadata (target-specific)
    LibrarianMeta  *LibrarianRoutingMetadata  `json:"librarian_meta,omitempty"`
    ArchivalistMeta *ArchivalistRoutingMetadata `json:"archivalist_meta,omitempty"`
}
```

**Tests:**
- [ ] Librarian LOCATE intent detected for "where is X" patterns
- [ ] Librarian PATTERN intent detected for "strategy/approach" patterns
- [ ] Librarian EXPLAIN intent detected for "how does X work" patterns
- [ ] Librarian GENERAL fallback for unclassified queries
- [ ] Archivalist HISTORICAL intent detected for "how did we" patterns
- [ ] Archivalist ACTIVITY intent detected for "recent changes" patterns
- [ ] Archivalist OUTCOME intent detected for "did it work" patterns
- [ ] Archivalist SIMILAR intent detected for "similar to" patterns
- [ ] Archivalist RESUME intent detected for "continue/pick up" patterns
- [ ] Archivalist time scope extracted correctly
- [ ] Subject normalization to snake_case
- [ ] Confidence threshold (0.8) respected
- [ ] Intent metadata passed in RoutedMessage
- [ ] Zero additional LLM calls (same routing call)

### 0.5 Archivalist Enhancements

Extend Archivalist for multi-session support with shared historical access.

**Files to modify:**
- `agents/archivalist/archivalist.go`
- `agents/archivalist/storage.go`
- `agents/archivalist/types.go`
- `agents/archivalist/query_cache.go`

**Files to create:**
- `agents/archivalist/session_store.go`
- `agents/archivalist/cross_session.go`
- `agents/archivalist/workflow_store.go`
- `agents/archivalist/skills.go`

**Acceptance Criteria:**

#### Session Isolation
- [ ] Session ID on all stored entries
- [ ] Default queries scoped to current session
- [ ] Explicit cross-session queries with flag
- [ ] Session context in all store operations

#### Cross-Session Queries (Read-Only)
- [ ] `QueryCrossSession()` searches all sessions
- [ ] `QuerySessions()` returns matching sessions
- [ ] `GetSessionHistory()` returns session timeline
- [ ] Results tagged with source session ID
- [ ] No write access to other sessions

#### Workflow Storage
- [ ] Store DAG definitions with session ID
- [ ] Store DAG execution history (node results, timing)
- [ ] Store workflow outcomes (success, failure, corrections)
- [ ] Query similar past workflows

#### Token Savings Tracking
- [ ] Record cache hits per session
- [ ] Record skipped work (existing functionality found)
- [ ] Record consultation results
- [ ] Generate savings report per session/global

#### Archivalist Skills (Progressive Disclosure)

**Core Skills (always loaded):**
- [ ] `store` skill - store entries
- [ ] `query` skill - query entries
- [ ] `briefing` skill - get session briefing
- [ ] `route_to` skill - route message to another agent (common skill)
- [ ] `reply_to` skill - reply to routed message (common skill)

**Extended Skills (loaded on demand):**
- [ ] `cross_session_query` skill - query across sessions
- [ ] `workflow_history` skill - query past workflows
- [ ] `token_savings` skill - get savings report
- [ ] `session_timeline` skill - get session events
- [ ] `pattern_search` skill - find similar patterns
- [ ] `failure_search` skill - find similar failures
- [ ] `decision_search` skill - find similar decisions

#### Archivalist Tools
```go
var ArchivalistTools = []ToolDefinition{
    // Core storage
    {Name: "archivalist_store", Skill: "store"},
    {Name: "archivalist_query", Skill: "query"},
    {Name: "archivalist_briefing", Skill: "briefing"},
    // Extended queries
    {Name: "archivalist_cross_session_query", Skill: "cross_session_query"},
    {Name: "archivalist_workflow_history", Skill: "workflow_history"},
    {Name: "archivalist_token_savings", Skill: "token_savings"},
    {Name: "archivalist_session_timeline", Skill: "session_timeline"},
    {Name: "archivalist_pattern_search", Skill: "pattern_search"},
    {Name: "archivalist_failure_search", Skill: "failure_search"},
    {Name: "archivalist_decision_search", Skill: "decision_search"},
    // Routing
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
}
```

#### Archivalist Hooks
- [ ] Pre-store hook: inject session context
- [ ] Post-store hook: invalidate relevant caches
- [ ] Pre-query hook: add session filter
- [ ] Post-query hook: record query for analytics

**Tests:**
- [ ] Store entries from multiple sessions
- [ ] Query within session, verify isolation
- [ ] Cross-session query, verify results tagged
- [ ] Concurrent sessions storing, verify no corruption

#### Intent-Aware Query Caching

**CRITICAL**: The Archivalist benefits from intent-aware caching for historical queries. Unlike Librarian (live code), historical data is immutable, enabling higher cache hit rates.

**Files to create:**
- `agents/archivalist/intent_cache.go`

**Files to modify:**
- `agents/archivalist/query_cache.go`
- `agents/archivalist/archivalist.go`

##### ArchivalistIntent Type

```go
// ArchivalistIntent classifies historical queries for caching
type ArchivalistIntent int

const (
    ArchivalistIntentHistorical ArchivalistIntent = iota  // Past solutions: "how did we handle X"
    ArchivalistIntentActivity                              // Recent work: "what changed", "who worked on"
    ArchivalistIntentOutcome                               // Results: "did it work", "status of"
    ArchivalistIntentSimilar                               // Pattern matching: "similar issues"
    ArchivalistIntentResume                                // Session state: "where did we leave off"
    ArchivalistIntentGeneral                               // Fallback for other history queries
)
```

##### Intent-Aware Cache Structure

```go
// IntentCacheKey combines intent + subject + time scope for cache lookup
type IntentCacheKey struct {
    Intent    ArchivalistIntent `json:"intent"`
    Subject   string            `json:"subject"`     // Normalized subject (e.g., "auth_migration")
    TimeScope string            `json:"time_scope"`  // Optional: "last_month", "recently", ""
    SessionID string            `json:"session_id"`
}

// IntentCachedResponse wraps cached responses with intent metadata
type IntentCachedResponse struct {
    Key         IntentCacheKey `json:"key"`
    Response    []byte         `json:"response"`
    CreatedAt   time.Time      `json:"created_at"`
    ExpiresAt   time.Time      `json:"expires_at"`
    HitCount    int64          `json:"hit_count"`
    Confidence  float64        `json:"confidence"` // From Guide classification
}

// IntentCache provides intent-aware caching for Archivalist
type IntentCache struct {
    mu sync.RWMutex

    // Cache by intent type for fast lookup
    byIntent map[ArchivalistIntent]map[string]*IntentCachedResponse

    // Secondary index by subject for cross-intent queries
    bySubject map[string][]*IntentCachedResponse

    // Embedding cache for similarity fallback (95% threshold)
    embeddingCache *QueryCache  // Existing embedding-based cache

    // Statistics
    hits   int64
    misses int64
}
```

##### Intent-Specific TTLs

```go
func getTTLForArchivalistIntent(intent ArchivalistIntent) time.Duration {
    switch intent {
    case ArchivalistIntentHistorical:
        return 60 * time.Minute  // Past solutions rarely change

    case ArchivalistIntentActivity:
        return 5 * time.Minute   // Activity updates frequently

    case ArchivalistIntentOutcome:
        return 30 * time.Minute  // Outcomes stable once recorded

    case ArchivalistIntentSimilar:
        return 45 * time.Minute  // Similar patterns rarely change

    case ArchivalistIntentResume:
        return 1 * time.Minute   // Resume state is highly volatile

    case ArchivalistIntentGeneral:
        return 15 * time.Minute  // Default for unclassified

    default:
        return 15 * time.Minute
    }
}
```

##### Cache Lookup Flow

```go
// Get attempts to find a cached response for the intent + subject
func (ic *IntentCache) Get(key IntentCacheKey) (*IntentCachedResponse, bool) {
    ic.mu.RLock()
    defer ic.mu.RUnlock()

    // 1. Try exact intent + subject + time_scope match
    intentMap, ok := ic.byIntent[key.Intent]
    if ok {
        cacheKey := ic.buildCacheKey(key)
        if resp, found := intentMap[cacheKey]; found && !resp.IsExpired() {
            atomic.AddInt64(&ic.hits, 1)
            atomic.AddInt64(&resp.HitCount, 1)
            return resp, true
        }
    }

    // 2. Try broader time scope (e.g., no time scope if specific one not found)
    if key.TimeScope != "" {
        broadKey := key
        broadKey.TimeScope = ""
        if resp, found := ic.tryBroadMatch(broadKey); found {
            return resp, true
        }
    }

    // 3. Miss - caller should query Archivalist
    atomic.AddInt64(&ic.misses, 1)
    return nil, false
}
```

##### Invalidation Rules (Intent-Specific)

| Intent | Invalidation Trigger | Rationale |
|--------|---------------------|-----------|
| **HISTORICAL** | TTL only (60min) | Past solutions don't change |
| **ACTIVITY** | New entry in same subject | Activity queries show recent changes |
| **OUTCOME** | TTL only (30min) | Outcomes are immutable once recorded |
| **SIMILAR** | TTL only (45min) | Similar patterns change slowly |
| **RESUME** | Session activity | Any session activity invalidates resume state |
| **GENERAL** | TTL only (15min) | Conservative default |

```go
// InvalidateForNewEntry invalidates relevant caches when new entry stored
func (ic *IntentCache) InvalidateForNewEntry(entry *ArchiveEntry) {
    ic.mu.Lock()
    defer ic.mu.Unlock()

    // Activity queries for this subject should be invalidated
    if activityMap, ok := ic.byIntent[ArchivalistIntentActivity]; ok {
        for key := range activityMap {
            if strings.Contains(key, entry.Subject) {
                delete(activityMap, key)
            }
        }
    }

    // Resume queries for this session should be invalidated
    if resumeMap, ok := ic.byIntent[ArchivalistIntentResume]; ok {
        for key, resp := range resumeMap {
            if resp.Key.SessionID == entry.SessionID {
                delete(resumeMap, key)
            }
        }
    }
}
```

##### Expected Hit Rates by Intent

| Intent | Expected Hit Rate | Rationale |
|--------|------------------|-----------|
| HISTORICAL | 85% | Users often ask same questions about past work |
| ACTIVITY | 40% | Updates frequently, but often repeated "what changed" |
| OUTCOME | 75% | Status checks repeated, outcomes immutable |
| SIMILAR | 70% | Similar issues often queried multiple times |
| RESUME | 50% | Session state queried repeatedly in short windows |
| GENERAL | 60% | Mixed queries, moderate caching benefit |

**Weighted Average: ~65-70% hit rate**

**Tests:**
- [ ] Cache hit for exact intent + subject match
- [ ] Cache miss triggers Archivalist query
- [ ] TTL expiration by intent type
- [ ] Activity cache invalidated on new entry
- [ ] Resume cache invalidated on session activity
- [ ] Historical cache persists across new entries
- [ ] Outcome cache persists across new entries
- [ ] Broad time scope fallback works
- [ ] Session isolation (no cross-session cache hits)
- [ ] Statistics tracking (hits, misses, by intent)
- [ ] Concurrent access safety
- [ ] Memory limits respected (eviction when full)

### 0.6 Bus Enhancements

Extend event bus for session-scoped topics and wildcards.

**Files to modify:**
- `agents/guide/bus.go`
- `agents/guide/channel_bus.go` (add sharding)

**Files to create:**
- `agents/guide/topic_router.go`
- `agents/guide/session_bus.go`

**Acceptance Criteria:**

#### Session-Scoped Topics
- [ ] Topic format: `session.{session_id}.{agent}.{channel}`
- [ ] Session topic creation on session start
- [ ] Session topic cleanup on session close
- [ ] Global topics (no session prefix) for shared agents

#### Topic Wildcards
- [ ] Subscribe to `session.*.status` for all session status
- [ ] Subscribe to `*.requests` for all agent requests
- [ ] Efficient wildcard matching (trie or similar)

#### Message Filtering
- [ ] Filter by session ID in message metadata
- [ ] Filter by message type
- [ ] Filter by priority

#### Session Bus Wrapper
- [ ] `SessionBus` wraps EventBus with session context
- [ ] Auto-prefix topics with session ID
- [ ] Session-scoped subscriptions (auto-cleanup)

#### ChannelBus Sharding (`agents/guide/channel_bus.go`)

**Current Problem:** Single `sync.RWMutex` creates lock contention with many topics/sessions.

**Required Changes:**
- [ ] Replace single `subscriptions map[string][]*channelSubscription` with sharded structure
- [ ] Configurable shard count (default: 32, must be power of 2)
- [ ] Topic-to-shard mapping via FNV hash
- [ ] Per-shard mutex (eliminates single-lock bottleneck)
- [ ] Backwards compatible - same `EventBus` interface

```go
// BEFORE (current)
type ChannelBus struct {
    mu            sync.RWMutex
    subscriptions map[string][]*channelSubscription  // Single lock bottleneck
    // ...
}

// AFTER (sharded)
type ChannelBus struct {
    shards    []*busShard
    shardMask uint32  // shardCount - 1 for fast modulo
    // ...
}

type busShard struct {
    mu            sync.RWMutex
    subscriptions map[string][]*channelSubscription
}

func (b *ChannelBus) getShard(topic string) *busShard {
    h := fnv32a(topic)
    return b.shards[h & b.shardMask]
}

// Publish now only locks one shard
func (b *ChannelBus) Publish(topic string, msg *Message) error {
    shard := b.getShard(topic)
    shard.mu.RLock()
    subs := shard.subscriptions[topic]
    shard.mu.RUnlock()
    // ...
}
```

- [ ] Benchmark: 32 shards with 1000 topics should show < 10% lock contention
- [ ] Benchmark: Linear scaling up to shard count concurrent publishers

**Tests:**
- [ ] Publish/subscribe with session topics
- [ ] Wildcard subscription matching
- [ ] Message filtering by session
- [ ] Concurrent publish across shards (verify no single-lock bottleneck)
- [ ] 100+ topics distributed across shards

---

## Phase 1: Knowledge Agents

**Goal**: Build the three knowledge RAG agents with skills and progressive disclosure.

**Dependencies**: Phase 0 complete

**Parallelization**: Items 1.1, 1.2 can execute in parallel after 0.1 complete.

### 1.1 Librarian (Local Codebase RAG)

Indexes and queries the current codebase. Read-only operations.

**Files to create:**
- `agents/librarian/types.go`
- `agents/librarian/indexer.go`
- `agents/librarian/ast_parser.go`
- `agents/librarian/storage.go`
- `agents/librarian/query.go`
- `agents/librarian/embeddings.go`
- `agents/librarian/librarian.go`
- `agents/librarian/routing.go`
- `agents/librarian/skills.go`
- `agents/librarian/tools.go`
- `agents/librarian/hooks.go`
- `agents/librarian/librarian_test.go`

**Acceptance Criteria:**

#### Indexing
- [ ] Index all Go source files (AST parsing for symbols, types, functions)
- [ ] Index file structure and organization
- [ ] Index imports and dependencies
- [ ] Index inline comments and documentation
- [ ] Index configuration files (go.mod, yaml, json)
- [ ] Incremental index updates on file change
- [ ] Background indexing with progress events

#### Query
- [ ] Semantic search over indexed content
- [ ] Query by file path pattern (glob)
- [ ] Query by symbol name (function, type, const, var)
- [ ] Query by usage pattern ("how is X used")
- [ ] Query by dependency ("what uses package X")
- [ ] Query by documentation content

#### Bus Integration
- [ ] Subscribe to `librarian.requests` (global, shared across sessions)
- [ ] Publish to `librarian.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Recall` (query), `Check` (verify existence)
- [ ] Support domains: `Files`, `Patterns`, `Code`

#### Librarian Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// find_symbol - Find a symbol by name
skills.NewSkill("find_symbol").
    Description("Find a function, type, constant, or variable by name").
    Domain("codebase").
    Keywords("find", "where", "locate", "symbol", "function", "type").
    StringParam("name", "Symbol name to find", true).
    EnumParam("kind", "Symbol kind", []string{"function", "type", "const", "var", "any"}, false)

// search_code - Search code content
skills.NewSkill("search_code").
    Description("Search for code patterns or text in the codebase").
    Domain("codebase").
    Keywords("search", "grep", "find", "code", "pattern").
    StringParam("query", "Search query or pattern", true).
    StringParam("file_pattern", "File glob pattern to filter", false)

// get_file - Read a file
skills.NewSkill("get_file").
    Description("Read the contents of a file").
    Domain("codebase").
    Keywords("read", "show", "get", "file", "content").
    StringParam("path", "File path to read", true).
    IntParam("start_line", "Start line (optional)", false).
    IntParam("end_line", "End line (optional)", false)
```

**Extended Skills (loaded on demand):**
```go
// analyze_imports - Analyze import graph
skills.NewSkill("analyze_imports").
    Description("Analyze import relationships for a package").
    Domain("codebase").
    Keywords("imports", "dependencies", "packages", "graph").
    StringParam("package", "Package path to analyze", true)

// find_usages - Find all usages of a symbol
skills.NewSkill("find_usages").
    Description("Find all places where a symbol is used").
    Domain("codebase").
    Keywords("usages", "references", "callers", "used").
    StringParam("symbol", "Symbol to find usages of", true)

// explain_pattern - Explain a code pattern
skills.NewSkill("explain_pattern").
    Description("Explain a code pattern used in the codebase").
    Domain("codebase").
    Keywords("explain", "pattern", "convention", "style").
    StringParam("pattern", "Pattern to explain", true)

// list_files - List files matching pattern
skills.NewSkill("list_files").
    Description("List files matching a glob pattern").
    Domain("codebase").
    Keywords("list", "files", "glob", "directory").
    StringParam("pattern", "Glob pattern", true)

// find_files - Find files using find command
skills.NewSkill("find_files").
    Description("Find files using the find command with various criteria").
    Domain("codebase").
    Keywords("find", "locate", "files", "directory", "name", "type").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory, l=symlink)", false).
    StringParam("mtime", "Modified time (e.g., -7 for last 7 days)", false).
    IntParam("maxdepth", "Maximum directory depth", false)

// get_structure - Get file/package structure
skills.NewSkill("get_structure").
    Description("Get the structure of a file or package").
    Domain("codebase").
    Keywords("structure", "outline", "overview", "symbols").
    StringParam("path", "File or package path", true)
```

**Remote Repository Skills (dynamically configurable):**
```go
// scan_remote_repo - Scan a remote GitHub repository
skills.NewSkill("scan_remote_repo").
    Description("Scan a remote GitHub repository (including private)").
    Domain("remote").
    Keywords("github", "remote", "repo", "scan", "clone").
    StringParam("url", "GitHub repository URL", true).
    StringParam("auth_token", "GitHub auth token (for private repos)", false).
    BoolParam("shallow", "Shallow clone for faster scanning", false)

// identify_languages - Identify languages in repository
skills.NewSkill("identify_languages").
    Description("Identify programming languages used in repository").
    Domain("tooling").
    Keywords("languages", "detect", "identify", "stack").
    StringParam("path", "Repository path (local or remote)", true)

// identify_tooling - Identify development tooling
skills.NewSkill("identify_tooling").
    Description("Identify linters, formatters, LSPs, and build tools").
    Domain("tooling").
    Keywords("linter", "formatter", "lsp", "tooling", "eslint", "prettier", "gopls").
    StringParam("path", "Repository path", true).
    ArrayParam("languages", "Specific languages to check", false)

// get_tool_config - Get configuration for a specific tool
skills.NewSkill("get_tool_config").
    Description("Get configuration file and settings for a dev tool").
    Domain("tooling").
    Keywords("config", "settings", "configuration").
    StringParam("tool", "Tool name (eslint, prettier, gopls, etc.)", true).
    StringParam("path", "Repository path", true)
```

**Language/Tool Detection Registry (dynamically configurable):**
```go
// Tool configurations are loaded dynamically based on detected languages
type ToolRegistry struct {
    // Detected tools per language
    Linters    map[string][]ToolConfig  // language → linter configs
    Formatters map[string][]ToolConfig  // language → formatter configs
    LSPs       map[string][]ToolConfig  // language → LSP configs
    TypeCheckers map[string][]ToolConfig // language → type checker configs
}

// Example configurations (loaded dynamically)
var DefaultToolRegistry = ToolRegistry{
    Linters: map[string][]ToolConfig{
        "go":         {{Name: "golangci-lint", Command: "golangci-lint run"}},
        "typescript": {{Name: "eslint", Command: "eslint"}},
        "python":     {{Name: "ruff", Command: "ruff check"}, {Name: "pylint", Command: "pylint"}},
        "rust":       {{Name: "clippy", Command: "cargo clippy"}},
    },
    Formatters: map[string][]ToolConfig{
        "go":         {{Name: "gofmt", Command: "gofmt -w"}, {Name: "goimports", Command: "goimports -w"}},
        "typescript": {{Name: "prettier", Command: "prettier --write"}},
        "python":     {{Name: "black", Command: "black"}, {Name: "ruff", Command: "ruff format"}},
        "rust":       {{Name: "rustfmt", Command: "cargo fmt"}},
    },
    LSPs: map[string][]ToolConfig{
        "go":         {{Name: "gopls", Command: "gopls"}},
        "typescript": {{Name: "typescript-language-server", Command: "typescript-language-server --stdio"}},
        "python":     {{Name: "pyright", Command: "pyright-langserver --stdio"}, {Name: "pylsp", Command: "pylsp"}},
        "rust":       {{Name: "rust-analyzer", Command: "rust-analyzer"}},
    },
}
```

**Slash Command Skill:**
```go
// Librarian supports / commands for direct invocation
var LibrarianSlashCommands = map[string]string{
    "/find":      "Find a symbol, file, or pattern in the codebase",
    "/search":    "Search code content with regex",
    "/file":      "Read or display a file",
    "/imports":   "Analyze import relationships",
    "/usages":    "Find usages of a symbol",
    "/structure": "Get structure of a file or package",
    "/scan":      "Scan a remote repository",
    "/tooling":   "Identify languages and dev tools",
}
```

#### Librarian Tools (for LLM calls)
```go
// Tools exposed to LLM during agent execution
var LibrarianTools = []ToolDefinition{
    // Core tools
    {Name: "librarian_find_symbol", Skill: "find_symbol"},
    {Name: "librarian_search_code", Skill: "search_code"},
    {Name: "librarian_get_file", Skill: "get_file"},
    {Name: "librarian_analyze_imports", Skill: "analyze_imports"},
    {Name: "librarian_find_usages", Skill: "find_usages"},
    {Name: "librarian_find_files", Skill: "find_files"},
    {Name: "librarian_list_files", Skill: "list_files"},
    // Remote repository tools
    {Name: "librarian_scan_remote", Skill: "scan_remote_repo"},
    {Name: "librarian_identify_languages", Skill: "identify_languages"},
    {Name: "librarian_identify_tooling", Skill: "identify_tooling"},
    {Name: "librarian_get_tool_config", Skill: "get_tool_config"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Memory Management

**Files to create:**
- `agents/librarian/memory.go`
- `agents/librarian/checkpoint.go`
- `agents/librarian/compaction.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Configurable thresholds (default: 25%, 50%, 75%)
- [ ] Trigger checkpoint at each threshold
- [ ] Trigger compaction at 75%

##### Checkpoint Summaries (Onboarding-Style)
- [ ] `CodebaseSummary` struct with onboarding information:
  - [ ] DirectoryStructure (string description)
  - [ ] KeyPaths (important directories/files)
  - [ ] CodeStyle (formatting, line limits, conventions)
  - [ ] Architecture (high-level architecture description)
  - [ ] TestingStrategy (where tests live, how to run)
  - [ ] Tooling (linters, formatters, LSPs per language)
  - [ ] PackageManagers (go mod, npm, etc.)
  - [ ] Patterns (common code patterns observed)
  - [ ] Conventions (naming, file organization)
  - [ ] NewDiscoveries (delta from last checkpoint)
- [ ] `createCheckpointSummary()` generates summary from current context
- [ ] Submit checkpoint to Archivalist with category `librarian_checkpoint`
- [ ] Include context_usage and checkpoint_index in metadata

```go
type CodebaseSummary struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`
    CheckpointIndex    int               `json:"checkpoint_index"`
    DirectoryStructure string            `json:"directory_structure"`
    KeyPaths           []string          `json:"key_paths"`
    CodeStyle          string            `json:"code_style"`
    Architecture       string            `json:"architecture"`
    TestingStrategy    string            `json:"testing_strategy"`
    Tooling            ToolingSummary    `json:"tooling"`
    PackageManagers    []string          `json:"package_managers"`
    Patterns           []string          `json:"patterns"`
    Conventions        []string          `json:"conventions"`
    NewDiscoveries     []string          `json:"new_discoveries"`
}
```

##### Compaction at 75%
- [ ] Create final checkpoint summary before compacting
- [ ] Submit final checkpoint to Archivalist
- [ ] Clear detailed content from context:
  - [ ] Remove detailed file contents
  - [ ] Remove full AST analysis results
  - [ ] Remove verbose query results
- [ ] Retain merged summary from all checkpoints
- [ ] Add index noting "detailed info in Archivalist"
- [ ] Target: ~25% context usage after compaction

##### Archivalist Consultation for Agent Activity
- [ ] `getRecentChanges(sessionID)` queries Archivalist for `engineer_result` category
- [ ] Do NOT track other agents' file changes in own context
- [ ] Query patterns:
  - [ ] "What files did we modify?" → query `engineer_result`
  - [ ] "What has changed?" → query all agent results with time filter
  - [ ] "What did the last task do?" → query `engineer_result` by task_id
- [ ] When compacted, query Archivalist for `librarian_checkpoint` to answer structure questions

```go
// Query Archivalist for agent activity (not own context)
func (l *Librarian) getRecentChanges(sessionID string) ([]AgentChange, error) {
    return l.archivalist.Query(QueryRequest{
        Category:  "engineer_result",
        SessionID: sessionID,
        OrderBy:   "timestamp DESC",
        Limit:     50,
    })
}
```

##### Memory Management Skills
```go
// create_checkpoint - Manually trigger checkpoint
skills.NewSkill("create_checkpoint").
    Description("Create a checkpoint summary of codebase knowledge").
    Domain("memory").
    Keywords("checkpoint", "save", "snapshot", "remember").
    BoolParam("force", "Force checkpoint even if below threshold", false)

// get_checkpoint - Retrieve checkpoint from Archivalist
skills.NewSkill("get_checkpoint").
    Description("Retrieve a previous checkpoint summary").
    Domain("memory").
    Keywords("recall", "remember", "previous", "checkpoint").
    IntParam("index", "Checkpoint index (default: latest)", false)

// get_agent_activity - Query what other agents have done
skills.NewSkill("get_agent_activity").
    Description("Query Archivalist for recent agent activity").
    Domain("memory").
    Keywords("changes", "modified", "activity", "what changed", "what did").
    StringParam("agent_type", "Filter by agent type (optional)", false).
    StringParam("task_id", "Filter by task ID (optional)", false).
    IntParam("limit", "Max results (default: 20)", false)
```

#### Query Caching (Intent-Aware)

**Files to add:**
- `agents/librarian/query_cache.go`
- `agents/librarian/intent_cache.go`

**CRITICAL**: Librarian handles two fundamentally different query types:
- **Specific queries** (LOCATE): "where is X", "find Y" - point to locations
- **Abstract queries** (PATTERN): "what is our caching strategy" - synthesized understanding

Simple embedding similarity (0.95 threshold) **fails** for natural language variations:
- "where is the auth code" vs "show me authentication" = ~0.85 similarity (cache miss at 0.95!)
- "what is our caching strategy" vs "how do we handle caching" = ~0.82 similarity (cache miss!)

**Solution**: Intent-aware caching using Guide-provided classification.

##### Query Intent Types
- [ ] `QueryIntent` enum: `LOCATE`, `PATTERN`, `EXPLAIN`, `GENERAL`
- [ ] Intent provided by Guide during routing (zero additional cost)
- [ ] Subject/concept extracted by Guide (e.g., "auth code" → "authentication")

```go
type QueryIntent int

const (
    IntentLocate  QueryIntent = iota  // "where is X", "find X"
    IntentPattern                      // "what is our X strategy"
    IntentExplain                      // "how does X work"
    IntentGeneral                      // other codebase questions
)

// Received from Guide (pre-classified, no extra token cost)
type LibrarianRequest struct {
    Query       string      `json:"query"`
    SessionID   string      `json:"session_id"`
    Intent      QueryIntent `json:"intent"`      // Pre-classified by Guide
    Subject     string      `json:"subject"`     // Extracted entity/concept
    Confidence  float64     `json:"confidence"`  // Guide's classification confidence
}
```

##### Intent-Specific Cache Structures
- [ ] `LocateCacheEntry` for LOCATE queries (keyed by entity name)
- [ ] `PatternCacheEntry` for PATTERN queries (keyed by concept)
- [ ] `ExplainCacheEntry` for EXPLAIN queries (keyed by subject)
- [ ] `SemanticCache` fallback with 0.80 threshold (not 0.95!)

```go
type LibrarianQueryCache struct {
    // Layer 1: Intent + Subject based (high hit rate)
    locateCache  map[string]*LocateCacheEntry   // entity → location
    patternCache map[string]*PatternCacheEntry  // concept → synthesis
    explainCache map[string]*ExplainCacheEntry  // subject → explanation

    // Layer 2: Semantic fallback (lower threshold)
    semanticCache *SemanticCache  // threshold: 0.80

    // File change tracking for invalidation
    fileHashes map[string]string  // path → content hash

    mu sync.RWMutex
}

type LocateCacheEntry struct {
    Entity       string              `json:"entity"`
    Locations    []FileLocation      `json:"locations"`
    FileHashes   map[string]string   `json:"file_hashes"`  // For invalidation
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}

type PatternCacheEntry struct {
    Concept      string              `json:"concept"`
    Synthesis    string              `json:"synthesis"`
    SourceFiles  []string            `json:"source_files"`
    TTL          time.Duration       `json:"ttl"`          // 60 min default
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}

type ExplainCacheEntry struct {
    Subject      string              `json:"subject"`
    Explanation  string              `json:"explanation"`
    SourceFiles  []string            `json:"source_files"`
    FileHashes   map[string]string   `json:"file_hashes"`
    TTL          time.Duration       `json:"ttl"`          // 30 min default
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}
```

##### Cache Invalidation by Intent
- [ ] LOCATE: Invalidate when any referenced file changes (hash check)
- [ ] PATTERN: Invalidate on TTL (60 min) + major structural changes
- [ ] EXPLAIN: Invalidate on file change OR TTL (30 min)
- [ ] GENERAL: TTL only (15 min)

```go
func (lc *LibrarianQueryCache) IsStale(entry any, intent QueryIntent) bool {
    switch intent {
    case IntentLocate:
        e := entry.(*LocateCacheEntry)
        // Check if any referenced file changed
        for path, cachedHash := range e.FileHashes {
            if currentHash := lc.fileHashes[path]; currentHash != cachedHash {
                return true
            }
        }
        return false  // No TTL for location queries

    case IntentPattern:
        e := entry.(*PatternCacheEntry)
        return time.Since(e.CreatedAt) > e.TTL

    case IntentExplain:
        e := entry.(*ExplainCacheEntry)
        if time.Since(e.CreatedAt) > e.TTL {
            return true
        }
        for path, cachedHash := range e.FileHashes {
            if currentHash := lc.fileHashes[path]; currentHash != cachedHash {
                return true
            }
        }
        return false

    default:
        return time.Since(entry.(*GeneralCacheEntry).CreatedAt) > 15*time.Minute
    }
}
```

##### Cache Lookup Flow
- [ ] Use Guide's pre-classification if confidence >= 0.8
- [ ] Check intent-specific cache first (fast, high precision)
- [ ] Fall back to semantic cache with 0.80 threshold
- [ ] On miss: synthesize, cache with intent key, return

```go
func (l *Librarian) HandleQuery(req *LibrarianRequest) (*Response, error) {
    // Use Guide's pre-classification if confident
    if req.Confidence >= 0.8 {
        switch req.Intent {
        case IntentLocate:
            if cached, ok := l.cache.GetLocate(req.Subject); ok {
                l.stats.RecordHit(IntentLocate)
                return cached.ToResponse(), nil
            }
        case IntentPattern:
            if cached, ok := l.cache.GetPattern(req.Subject); ok {
                l.stats.RecordHit(IntentPattern)
                return cached.ToResponse(), nil
            }
        case IntentExplain:
            if cached, ok := l.cache.GetExplain(req.Subject); ok {
                l.stats.RecordHit(IntentExplain)
                return cached.ToResponse(), nil
            }
        }
    }

    // Cache miss - do full synthesis
    l.stats.RecordMiss(req.Intent)
    response, sources := l.synthesize(req.Query)

    // Cache the result using the classified intent
    l.cache.Store(req.Intent, req.Subject, response, sources)

    return response, nil
}
```

##### File Change Detection & Cache Invalidation

**Files to add:**
- `agents/librarian/file_watcher.go`
- `agents/librarian/debouncer.go`
- `agents/librarian/hash.go`

**Dependencies:** `github.com/fsnotify/fsnotify`, `github.com/cespare/xxhash/v2`

###### File Watcher (fsnotify)
- [ ] `FileWatcher` struct with fsnotify watcher, cache reference, index reference
- [ ] Watch all files in repo recursively on startup
- [ ] Ignore paths: `.git`, `node_modules`, `vendor`, `__pycache__`, `.idea`, `.vscode`, `dist`, `build`
- [ ] Handle events: Create, Write, Remove, Rename
- [ ] Run watcher in background goroutine
- [ ] Graceful shutdown via context cancellation

```go
type FileWatcher struct {
    watcher     *fsnotify.Watcher
    cache       *LibrarianQueryCache
    index       *CodebaseIndex
    debouncer   *Debouncer
    hashCache   map[string]string  // path → current hash
    ignorePaths []string
    mu          sync.RWMutex
    ctx         context.Context
    cancel      context.CancelFunc
}
```

###### Debouncing Rapid File Changes
- [ ] `Debouncer` struct with configurable delay (default: 100ms)
- [ ] Batch rapid events for same file (editors trigger multiple events per save)
- [ ] Cancel pending timer if new event arrives for same file
- [ ] Execute callback only after delay with no new events

```go
type Debouncer struct {
    delay   time.Duration
    timers  map[string]*time.Timer
    mu      sync.Mutex
}

func (d *Debouncer) Debounce(key string, fn func()) {
    d.mu.Lock()
    defer d.mu.Unlock()

    if timer, ok := d.timers[key]; ok {
        timer.Stop()
    }

    d.timers[key] = time.AfterFunc(d.delay, func() {
        d.mu.Lock()
        delete(d.timers, key)
        d.mu.Unlock()
        fn()
    })
}
```

###### Hash Computation (xxHash64)
- [ ] Use xxHash64 for fast hashing (~1GB/s, much faster than SHA256)
- [ ] Hash file contents, not metadata
- [ ] Skip hash if file unchanged (same hash = no invalidation)
- [ ] Handle file deletion (hash = empty string)

```go
import "github.com/cespare/xxhash/v2"

func computeHash(path string) (string, error) {
    file, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer file.Close()

    hasher := xxhash.New()
    if _, err := io.Copy(hasher, file); err != nil {
        return "", err
    }

    return fmt.Sprintf("%x", hasher.Sum64()), nil
}
```

###### Cache Invalidation on File Change
- [ ] `OnFileChanged(path, newHash)` updates hash and invalidates entries
- [ ] Invalidate LOCATE entries that reference the changed file
- [ ] Invalidate EXPLAIN entries that reference the changed file
- [ ] Invalidate PATTERN entries if file added/removed in source directory
- [ ] Track invalidation counts in statistics

```go
func (lc *LibrarianQueryCache) OnFileChanged(path string, newHash string) {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    // Update current hash
    if newHash == "" {
        delete(lc.fileHashes, path)
    } else {
        lc.fileHashes[path] = newHash
    }

    // Invalidate LOCATE entries that reference this file
    for entity, entry := range lc.locateCache {
        if _, ok := entry.FileHashes[path]; ok {
            delete(lc.locateCache, entity)
            lc.stats.LocateInvalidations++
        }
    }

    // Invalidate EXPLAIN entries that reference this file
    for subject, entry := range lc.explainCache {
        if _, ok := entry.FileHashes[path]; ok {
            delete(lc.explainCache, subject)
            lc.stats.ExplainInvalidations++
        }
    }

    // Check PATTERN entries for structural changes
    dir := filepath.Dir(path)
    for concept, entry := range lc.patternCache {
        for _, sourceFile := range entry.SourceFiles {
            if filepath.Dir(sourceFile) == dir {
                delete(lc.patternCache, concept)
                lc.stats.PatternInvalidations++
                break
            }
        }
    }
}
```

###### Batch Operations (git checkout, branch switch)
- [ ] `OnBatchChange(paths)` for operations that change many files at once
- [ ] Pause normal debouncing during batch
- [ ] For >100 files changed: clear all caches (more efficient than selective invalidation)
- [ ] For <100 files changed: selectively invalidate
- [ ] Coordinate with index batch reindexing

```go
func (fw *FileWatcher) OnBatchChange(paths []string) {
    fw.debouncer.Pause()
    defer fw.debouncer.Resume()

    changedPaths := make(map[string]string)
    for _, path := range paths {
        if hash, err := computeHash(path); err == nil {
            if oldHash := fw.hashCache[path]; oldHash != hash {
                changedPaths[path] = hash
            }
        }
    }

    fw.cache.OnBatchFileChanged(changedPaths)
    fw.index.OnBatchFileChanged(changedPaths)
}

func (lc *LibrarianQueryCache) OnBatchFileChanged(changes map[string]string) {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    // For large batches, clear everything
    if len(changes) > 100 {
        lc.locateCache = make(map[string]*LocateCacheEntry)
        lc.explainCache = make(map[string]*ExplainCacheEntry)
        lc.patternCache = make(map[string]*PatternCacheEntry)
        return
    }

    // Selective invalidation for smaller batches
    for path := range changes {
        lc.invalidateByPath(path)
    }
}
```

###### Index Synchronization
- [ ] Coordinate cache invalidation with index updates
- [ ] On file delete: remove from index
- [ ] On file create: add to index
- [ ] On file modify: reindex file
- [ ] Index update triggers after cache invalidation

```go
func (idx *CodebaseIndex) OnFileChanged(path string, op fsnotify.Op) {
    switch {
    case op&fsnotify.Remove == fsnotify.Remove:
        idx.RemoveFile(path)
    case op&fsnotify.Create == fsnotify.Create:
        idx.IndexFile(path)
    case op&fsnotify.Write == fsnotify.Write:
        idx.ReindexFile(path)
    }
}
```

##### Cache Statistics
- [ ] Track hits/misses per intent type
- [ ] Track token savings (cache hit = ~3000 tokens saved)
- [ ] Report hit rate per intent type
- [ ] Report overall token savings

```go
type LibrarianCacheStats struct {
    LocateHits     int64   `json:"locate_hits"`
    LocateMisses   int64   `json:"locate_misses"`
    PatternHits    int64   `json:"pattern_hits"`
    PatternMisses  int64   `json:"pattern_misses"`
    ExplainHits    int64   `json:"explain_hits"`
    ExplainMisses  int64   `json:"explain_misses"`
    GeneralHits    int64   `json:"general_hits"`
    GeneralMisses  int64   `json:"general_misses"`
    EstimatedTokensSaved int64 `json:"estimated_tokens_saved"`
}
```

##### Expected Performance
| Query Type | Without Cache | With Intent Cache | Savings |
|------------|---------------|-------------------|---------|
| "where is auth.go" (repeated) | 3,000 tokens | 0 tokens | 100% |
| "where is authentication" (variation) | 3,000 tokens | 0 tokens | 100% |
| "what is our caching strategy" | 5,000 tokens | 0 tokens | 100% |
| "how do we handle caching" (variation) | 5,000 tokens | 0 tokens | 100% |

**Expected overall hit rate**: 70-85%
**Expected token savings**: 60-75% reduction per session

#### Librarian Hooks
- [ ] Pre-query hook: log query for analytics
- [ ] Post-query hook: cache frequently accessed files
- [ ] Pre-index hook: filter excluded paths
- [ ] Post-index hook: notify index updated event
- [ ] Context-threshold hook: trigger checkpoint at 25%, 50%
- [ ] Context-threshold hook: trigger checkpoint + compaction at 75%

**Tests:**
- [ ] Index a test codebase
- [ ] Query for patterns, verify results
- [ ] Test incremental update
- [ ] Test skill loading on demand
- [ ] Test checkpoint creation at 25% threshold
- [ ] Test checkpoint submission to Archivalist
- [ ] Test compaction at 75% threshold
- [ ] Test post-compaction context size (~25%)
- [ ] Test Archivalist consultation for agent activity
- [ ] Test checkpoint retrieval after compaction
- [ ] Test query cache LOCATE intent hit/miss
- [ ] Test query cache PATTERN intent hit/miss
- [ ] Test query cache EXPLAIN intent hit/miss
- [ ] Test cache invalidation on file change (LOCATE)
- [ ] Test cache invalidation on file change (EXPLAIN)
- [ ] Test cache TTL expiration (PATTERN)
- [ ] Test semantic fallback at 0.80 threshold
- [ ] Test cache statistics reporting
- [ ] Test natural language query variations hit same cache entry
- [ ] Benchmark: cache hit latency vs full synthesis
- [ ] Test file watcher detects file create
- [ ] Test file watcher detects file modify
- [ ] Test file watcher detects file delete
- [ ] Test debouncer batches rapid events
- [ ] Test hash computation with xxHash64
- [ ] Test LOCATE invalidation on referenced file change
- [ ] Test EXPLAIN invalidation on referenced file change
- [ ] Test PATTERN invalidation on directory structural change
- [ ] Test batch invalidation for >100 files (full cache clear)
- [ ] Test batch invalidation for <100 files (selective)
- [ ] Test index synchronization with cache invalidation
- [ ] Test ignored paths (.git, node_modules) not watched
- [ ] Benchmark: file change detection latency

### 1.2 Academic (External Knowledge RAG)

Research agent for external knowledge, best practices, papers.

**Files to create:**
- `agents/academic/types.go`
- `agents/academic/sources.go`
- `agents/academic/github.go`
- `agents/academic/articles.go`
- `agents/academic/embeddings.go`
- `agents/academic/researcher.go`
- `agents/academic/academic.go`
- `agents/academic/routing.go`
- `agents/academic/skills.go`
- `agents/academic/tools.go`
- `agents/academic/hooks.go`
- `agents/academic/academic_test.go`

**Acceptance Criteria:**

#### Source Ingestion
- [ ] GitHub repos: clone, index code, extract patterns
- [ ] Articles: fetch, parse, extract content
- [ ] Papers: PDF parsing, extract text
- [ ] RFCs: fetch, parse structured content
- [ ] Documentation sites: crawl, extract content

#### Research
- [ ] Embedding-based semantic search over sources
- [ ] Research paper generation (structured output)
- [ ] Source citation in research output
- [ ] Confidence scoring based on source quality
- [ ] Cache research results in Archivalist

#### Bus Integration
- [ ] Subscribe to `academic.requests` (global, shared across sessions)
- [ ] Publish to `academic.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Recall` (research), `Check` (verify claim)
- [ ] Support domains: `Patterns`, `Decisions`, `Learnings`
- [ ] Trigger patterns: "How would I design...", "What's the best approach..."

#### Academic Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// research - General research query
skills.NewSkill("research").
    Description("Research a topic and provide comprehensive analysis").
    Domain("research").
    Keywords("research", "how", "design", "implement", "best", "approach").
    StringParam("query", "Research question", true).
    IntParam("max_sources", "Maximum sources to consult", false)

// find_examples - Find implementation examples
skills.NewSkill("find_examples").
    Description("Find example implementations of a pattern or feature").
    Domain("research").
    Keywords("example", "implementation", "reference", "sample").
    StringParam("topic", "Topic to find examples for", true).
    StringParam("language", "Programming language filter", false)
```

**Extended Skills (loaded on demand):**
```go
// ingest_github - Ingest a GitHub repository
skills.NewSkill("ingest_github").
    Description("Ingest and index a GitHub repository for research").
    Domain("sources").
    Keywords("github", "repo", "ingest", "index").
    StringParam("url", "GitHub repository URL", true).
    BoolParam("deep", "Deep analysis including all files", false)

// ingest_article - Ingest an article
skills.NewSkill("ingest_article").
    Description("Ingest and index a web article").
    Domain("sources").
    Keywords("article", "web", "ingest", "read").
    StringParam("url", "Article URL", true)

// compare_approaches - Compare implementation approaches
skills.NewSkill("compare_approaches").
    Description("Compare different approaches to solving a problem").
    Domain("research").
    Keywords("compare", "tradeoff", "versus", "vs", "pros", "cons").
    StringParam("topic", "Topic to compare approaches for", true).
    ArrayParam("approaches", "Specific approaches to compare", false)

// find_rfc - Find relevant RFC
skills.NewSkill("find_rfc").
    Description("Find relevant RFC or specification").
    Domain("research").
    Keywords("rfc", "spec", "specification", "standard").
    StringParam("topic", "Topic to find RFC for", true)

// generate_paper - Generate research paper
skills.NewSkill("generate_paper").
    Description("Generate a comprehensive research paper on a topic").
    Domain("research").
    Keywords("paper", "report", "analysis", "comprehensive").
    StringParam("topic", "Topic for the paper", true).
    EnumParam("depth", "Analysis depth", []string{"overview", "detailed", "comprehensive"}, false)

// find_files - Find files in ingested sources
skills.NewSkill("find_files").
    Description("Find files in ingested repositories or sources").
    Domain("sources").
    Keywords("find", "locate", "files", "search").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)
```

#### Academic Tools
```go
var AcademicTools = []ToolDefinition{
    // Core research
    {Name: "academic_research", Skill: "research"},
    {Name: "academic_find_examples", Skill: "find_examples"},
    {Name: "academic_compare", Skill: "compare_approaches"},
    // Source ingestion
    {Name: "academic_ingest_github", Skill: "ingest_github"},
    {Name: "academic_ingest_article", Skill: "ingest_article"},
    // Advanced research
    {Name: "academic_find_rfc", Skill: "find_rfc"},
    {Name: "academic_generate_paper", Skill: "generate_paper"},
    // File operations
    {Name: "academic_find_files", Skill: "find_files"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Academic Memory Management

**Model**: Opus 4.5

**Thresholds**: 85% (checkpoint) | 95% (compact)

**Files to create:**
- `agents/academic/memory.go`
- `agents/academic/research_paper.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Trigger research paper generation at 85%
- [ ] Trigger compaction at 95%

##### Research Paper Summary (at 85%)
- [ ] `AcademicResearchPaper` struct:
  - [ ] Title (research focus)
  - [ ] Abstract (summary of findings)
  - [ ] TopicsResearched (list of topics covered)
  - [ ] KeyFindings (structured findings with confidence levels)
  - [ ] SourcesCited (references used)
  - [ ] Recommendations (actionable recommendations)
  - [ ] OpenQuestions (unresolved questions)
  - [ ] RelatedTopics (for future research)
- [ ] Submit to Archivalist with category `academic_research_paper`

```go
type AcademicResearchPaper struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`
    Title              string            `json:"title"`
    Abstract           string            `json:"abstract"`
    TopicsResearched   []string          `json:"topics_researched"`
    KeyFindings        []Finding         `json:"key_findings"`
    SourcesCited       []Source          `json:"sources_cited"`
    Recommendations    []string          `json:"recommendations"`
    OpenQuestions      []string          `json:"open_questions"`
    RelatedTopics      []string          `json:"related_topics"`
}
```

##### Compaction at 95%
- [ ] Create final research paper before compacting
- [ ] Clear detailed source content from context
- [ ] Retain summarized findings
- [ ] Target: ~30% context usage after compaction

#### Academic Hooks
- [ ] Pre-research hook: check Archivalist for cached research
- [ ] Post-research hook: cache results in Archivalist
- [ ] Pre-ingest hook: validate source accessibility
- [ ] Post-ingest hook: trigger embedding generation
- [ ] Context-threshold hook: generate research paper at 85%
- [ ] Context-threshold hook: compact at 95%

**Tests:**
- [ ] Ingest test sources
- [ ] Research query, verify output
- [ ] Test caching in Archivalist
- [ ] Test research paper generation at 85%
- [ ] Test compaction at 95%
- [ ] Test research continuity after compaction

### 1.3 Dynamic Tool Discovery Protocol

Implements the cascading discovery mechanism for linters, formatters, type checkers, test frameworks, and LSPs without hardcoded tool lists.

**Files to create:**
- `core/tooling/types.go`
- `core/tooling/discovery.go`
- `core/tooling/detection.go`
- `core/tooling/defaults.go`
- `core/tooling/cache.go`
- `core/tooling/installer.go`
- `core/tooling/discovery_test.go`
- `core/tooling/defaults_test.go`
- `agents/librarian/tool_scanner.go`
- `agents/academic/tool_research.go`

**Dependencies**: Phase 1.1 (Librarian), Phase 1.2 (Academic)

**Acceptance Criteria:**

#### Core Types (`core/tooling/types.go`)
- [ ] `ToolCategory` enum: `linter`, `formatter`, `type_checker`, `test_framework`, `lsp`
- [ ] `ConfidenceLevel` enum: `high`, `medium`, `low`
- [ ] `ToolDiscoveryRequest` struct with session ID, requesting agent, category, target path, languages
- [ ] `ToolDiscoveryResponse` struct with tier, confidence, tools, requires_user flag, user options
- [ ] `DiscoveredTool` struct with name, category, language, version, config path, run command, install command, is_installed, source, rationale
- [ ] `ToolOption` struct for user selection with tool, pros, cons, recommended flag
- [ ] `ToolDetectionRule` struct for config file → tool mapping
- [ ] `PackageRule` struct for dependency → tool mapping

#### Discovery Protocol (`core/tooling/discovery.go`)
- [ ] `ToolDiscoveryService` interface with `Discover(ctx, request) (response, error)`
- [ ] `discoveryService` implementation with Librarian and Academic clients
- [ ] Tier 1 execution: call Librarian's `scan_tools` skill
- [ ] Tier 2 escalation: if confidence < HIGH, call Academic's `research_tools` skill
- [ ] Tier 3 escalation: if satisfactory == false, return `RequiresUser=true` with options
- [ ] Timeout handling per tier (Tier 1: 10s, Tier 2: 30s, Tier 3: user-driven)
- [ ] Error handling with graceful degradation (if Academic unavailable, skip to Tier 3)

```go
// Required interface
type ToolDiscoveryService interface {
    Discover(ctx context.Context, req *ToolDiscoveryRequest) (*ToolDiscoveryResponse, error)
    DiscoverAll(ctx context.Context, path string, sessionID string) (map[ToolCategory]*ToolDiscoveryResponse, error)
    InvalidateCache(sessionID string, path string) error
}
```

#### Detection Patterns (`core/tooling/detection.go`)
- [ ] `ToolDetectionPatterns` map: config file name → `ToolDetectionRule`
- [ ] `PackageManifestRules` map: manifest file → `[]PackageRule`
- [ ] Support for at least:
  - [ ] JavaScript/TypeScript: eslint, oxlint, prettier, biome, jest, vitest, mocha, tsc
  - [ ] Python: ruff, black, pylint, mypy, pyright, pytest
  - [ ] Go: golangci-lint, gofmt, goimports, go test
  - [ ] Rust: clippy, rustfmt, cargo test
- [ ] `DetectFromConfigFile(path string) (*DiscoveredTool, error)`
- [ ] `DetectFromPackageManifest(path string, manifestType string) ([]*DiscoveredTool, error)`
- [ ] `InferLanguages(path string) ([]string, error)` - detect languages from file extensions

#### Language Defaults Registry (`core/tooling/defaults.go`)
- [ ] `LanguageDefaults` map: language → `LanguageToolset`
- [ ] `LanguageToolset` struct with PackageManager, Linter, Formatter, TypeChecker, LSP, TestFramework
- [ ] `ToolDefault` struct with Name and InstallCmd
- [ ] `GetDefaults(language string) (*LanguageToolset, bool)`
- [ ] `GetDefaultTool(language string, category ToolCategory) (*ToolDefault, bool)`

**Required Language Support:**
- [ ] Go: golangci-lint, gofmt, go vet, gopls, go test
- [ ] Python: uv (package manager), ruff (linter/formatter/type-checker/LSP), pytest
- [ ] JavaScript: npm, oxlint (linter), prettier, typescript-language-server, vitest
- [ ] TypeScript: npm, oxlint, prettier, tsc, typescript-language-server, vitest
- [ ] Rust: cargo, clippy, rustfmt, cargo check, rust-analyzer, cargo test
- [ ] Ruby: bundler, rubocop, ruby-lsp, rspec
- [ ] Java: maven/gradle, checkstyle, google-java-format, jdtls, junit
- [ ] Kotlin: gradle, ktlint, kotlin-language-server, junit
- [ ] C/C++: clang-tidy, clang-format, clangd, gtest/ctest
- [ ] C#: dotnet, omnisharp, dotnet test
- [ ] Elixir: mix, credo, mix format, dialyzer, elixir-ls, mix test
- [ ] Bash: shellcheck, shfmt, bash-language-server, bats
- [ ] PHP: composer, phpstan, php-cs-fixer, intelephense, phpunit
- [ ] Swift: swiftlint, swift-format, sourcekit-lsp, swift test
- [ ] Zig: zig fmt, zls, zig test
- [ ] OCaml: opam, ocamlformat, ocaml-lsp, alcotest
- [ ] Dart: pub, dart analyze, dart format, dart (LSP), dart test
- [ ] Terraform: tflint, terraform fmt, terraform-ls, terratest
- [ ] Nix: statix, nixfmt, nixd
- [ ] Vue: eslint-plugin-vue, prettier, vue-tsc, vue-language-server, vitest
- [ ] Svelte: eslint-plugin-svelte, prettier-plugin-svelte, svelte-check, svelte-language-server, vitest

**Key Recommendations (hardcoded preferences):**
```go
// These represent the modern, fast, well-maintained tools as of 2025
var RecommendedDefaults = map[string]map[ToolCategory]string{
    "python": {
        ToolCategoryLinter:      "ruff",      // Replaces flake8, pylint, isort, pyupgrade, etc.
        ToolCategoryFormatter:   "ruff",      // ruff format (replaces black)
        ToolCategoryTypeChecker: "ruff",      // Via ruff-lsp
        ToolCategoryLSP:         "ruff-lsp",  // Fast, integrated
        ToolCategoryTestFramework: "pytest",
    },
    "javascript": {
        ToolCategoryLinter:      "oxlint",    // 50-100x faster than ESLint
        ToolCategoryFormatter:   "prettier",
        ToolCategoryLSP:         "typescript-language-server",
        ToolCategoryTestFramework: "vitest",  // Modern, Vite-native
    },
    "typescript": {
        ToolCategoryLinter:      "oxlint",
        ToolCategoryFormatter:   "prettier",
        ToolCategoryTypeChecker: "tsc",
        ToolCategoryLSP:         "typescript-language-server",
        ToolCategoryTestFramework: "vitest",
    },
}

// Package manager preferences (check availability, use first available)
var PackageManagerPreference = map[string][]string{
    "python":     {"uv", "pip"},           // uv is 10-100x faster
    "javascript": {"pnpm", "yarn", "npm"}, // pnpm is fastest, most disk-efficient
    "typescript": {"pnpm", "yarn", "npm"},
    "ruby":       {"bundler"},
    "rust":       {"cargo"},
    "go":         {"go mod"},
}
```

#### Caching (`core/tooling/cache.go`)
- [ ] `ToolDiscoveryCache` interface with `Get`, `Set`, `Invalidate`
- [ ] Cache key: `{session_id}:{project_path}:{category}`
- [ ] Cache entry stores: tools, tier, discovered_at, user_overrides
- [ ] TTL-based expiration (default: 1 hour, configurable)
- [ ] Invalidation triggers:
  - [ ] Config file change detected by Librarian
  - [ ] Explicit invalidation request
  - [ ] Session changes branch/commit

```go
type ToolDiscoveryCache interface {
    Get(sessionID, projectPath string, category ToolCategory) (*ToolDiscoveryCacheEntry, bool)
    Set(entry *ToolDiscoveryCacheEntry) error
    Invalidate(sessionID, projectPath string) error
    InvalidateCategory(sessionID, projectPath string, category ToolCategory) error
}
```

#### Installer (`core/tooling/installer.go`)
- [ ] `ToolInstaller` interface with `Install`, `IsInstalled`, `GetVersion`
- [ ] Package manager detection: npm/yarn/pnpm, pip/poetry/uv, go install, cargo
- [ ] Installation in virtual environment when available
- [ ] Verify installation success
- [ ] Rollback on failure

```go
type ToolInstaller interface {
    IsInstalled(tool *DiscoveredTool) (bool, error)
    Install(ctx context.Context, tool *DiscoveredTool, env *VirtualEnv) error
    GetVersion(tool *DiscoveredTool) (string, error)
    Uninstall(ctx context.Context, tool *DiscoveredTool) error
}
```

#### Librarian Tool Scanner (`agents/librarian/tool_scanner.go`)
- [ ] `scan_tools` skill implementation
- [ ] Scan directory for config files matching `ToolDetectionPatterns`
- [ ] Parse package manifests for tool dependencies
- [ ] Check CI/CD configs for tool invocations (Makefile, GitHub Actions)
- [ ] Return confidence level based on source:
  - [ ] HIGH: explicit config file found
  - [ ] MEDIUM: found in package manifest dependencies
  - [ ] LOW: inferred from CI/CD or not found

```go
// Librarian skill
skills.NewSkill("scan_tools").
    Description("Scan codebase for configured development tools").
    Domain("tooling").
    Keywords("scan", "tools", "linter", "formatter", "discover").
    StringParam("path", "Directory to scan", true).
    EnumParam("category", "Tool category to scan for", []string{"linter", "formatter", "type_checker", "test_framework", "lsp", "all"}, false)
```

#### Academic Tool Research (`agents/academic/tool_research.go`)
- [ ] `research_tools` skill implementation
- [ ] Research query: "What {category} tools are recommended for {languages} in {year}?"
- [ ] Parse research results into structured recommendations
- [ ] Determine if clear best practice exists (satisfactory flag)
- [ ] Return multiple options if ambiguous

```go
// Academic skill
skills.NewSkill("research_tools").
    Description("Research recommended development tools for given languages").
    Domain("tooling").
    Keywords("research", "tools", "recommend", "best practice").
    ArrayParam("languages", "Languages to research tools for", true).
    EnumParam("category", "Tool category", []string{"linter", "formatter", "type_checker", "test_framework", "lsp"}, true)
```

#### User Escalation Integration
- [ ] When `RequiresUser=true`, format options for Guide to present
- [ ] Store user selection in Archivalist with category `user_preference`
- [ ] Load user preferences from Archivalist before Tier 1 (fast path for known preferences)
- [ ] User preference format:
```go
type UserToolPreference struct {
    Category     ToolCategory `json:"category"`
    Language     string       `json:"language"`
    ChosenTool   string       `json:"chosen_tool"`
    Rationale    string       `json:"rationale,omitempty"`
    SessionID    string       `json:"session_id"`
    CreatedAt    time.Time    `json:"created_at"`
    Promoted     bool         `json:"promoted"` // True = applies to all sessions
}
```

#### Agent Integration
- [ ] Add `discover_tools` skill to Engineer
- [ ] Add `discover_tools` skill to Inspector
- [ ] Add `discover_tools` skill to Tester
- [ ] Skill implementation calls `ToolDiscoveryService.Discover()`
- [ ] Post-discovery: install tool if not installed
- [ ] Post-install: run tool with discovered configuration

```go
// Common skill added to Engineer, Inspector, Tester
skills.NewSkill("discover_tools").
    Description("Discover appropriate tools via Librarian → Academic → User escalation").
    Domain("tooling").
    Keywords("discover", "tools", "linter", "formatter", "test").
    EnumParam("category", "Tool category", []string{"linter", "formatter", "type_checker", "test_framework", "lsp"}, true).
    StringParam("path", "Target path to analyze", true).
    BoolParam("force_refresh", "Bypass cache and re-discover", false).
    BoolParam("install", "Install tool if not present", false)
```

**Tests:**

#### Unit Tests (`core/tooling/discovery_test.go`)
- [ ] Test Tier 1 discovery with config file present → returns HIGH confidence
- [ ] Test Tier 1 discovery with package.json dependency → returns MEDIUM confidence
- [ ] Test Tier 2 escalation when Tier 1 returns LOW confidence
- [ ] Test Tier 3 escalation when Academic returns unsatisfactory
- [ ] Test cache hit returns immediately without scanning
- [ ] Test cache invalidation triggers re-scan
- [ ] Test user preference loading bypasses full protocol

#### Integration Tests
- [ ] Test full protocol flow: no config → Academic research → user selection
- [ ] Test with real project structure (Go, TypeScript, Python fixtures)
- [ ] Test tool installation and verification
- [ ] Test cross-session preference persistence

#### Acceptance Test Scenarios
- [ ] **Scenario A**: Go project with `golangci.yml` → returns golangci-lint immediately (Tier 1)
- [ ] **Scenario B**: TypeScript project with eslint in package.json devDeps → returns eslint with MEDIUM confidence (Tier 1)
- [ ] **Scenario C**: Python project with no tooling → Academic recommends ruff → returns ruff (Tier 2)
- [ ] **Scenario D**: New language with multiple options → presents options to user → stores selection (Tier 3)
- [ ] **Scenario E**: Second session for same project → loads cached tools immediately

```go
// Acceptance test structure
func TestToolDiscovery_GoProjectWithConfig(t *testing.T) {
    // Setup: Create temp dir with golangci.yml
    // Execute: ToolDiscoveryService.Discover(category=linter, path=tempDir)
    // Assert: response.Tier == 1
    // Assert: response.Confidence == ConfidenceHigh
    // Assert: response.Tools[0].Name == "golangci-lint"
    // Assert: response.Tools[0].ConfigPath contains "golangci.yml"
}

func TestToolDiscovery_EscalatesToAcademic(t *testing.T) {
    // Setup: Create temp dir with only .py files, no config
    // Mock: Academic returns ruff recommendation
    // Execute: ToolDiscoveryService.Discover(category=linter, path=tempDir)
    // Assert: response.Tier == 2
    // Assert: response.Tools[0].Name == "ruff"
    // Assert: response.Tools[0].Source == "academic_research"
}

func TestToolDiscovery_EscalatesToUser(t *testing.T) {
    // Setup: Create temp dir with ambiguous setup
    // Mock: Academic returns unsatisfactory=true with multiple options
    // Execute: ToolDiscoveryService.Discover(category=linter, path=tempDir)
    // Assert: response.Tier == 3
    // Assert: response.RequiresUser == true
    // Assert: len(response.UserOptions) > 1
}
```

---

## Phase 2: Execution Agents

**Goal**: Build the core execution pipeline with skills and tools.

**Dependencies**: Phase 0 complete, Phase 1.1 (Librarian) complete

**Parallelization**: Items 2.1 and 2.2 can execute in parallel.

### 2.1 Engineer (Task Executor)

Executes individual tasks. Invisible to user.

**Files to create:**
- `agents/engineer/types.go`
- `agents/engineer/executor.go`
- `agents/engineer/file_ops.go`
- `agents/engineer/bash_ops.go`
- `agents/engineer/consultation.go`
- `agents/engineer/engineer.go`
- `agents/engineer/routing.go`
- `agents/engineer/skills.go`
- `agents/engineer/tools.go`
- `agents/engineer/hooks.go`
- `agents/engineer/engineer_test.go`

**Acceptance Criteria:**

#### Task Execution
- [ ] Accept task dispatch from Orchestrator via bus
- [ ] Task structure: prompt, context, metadata, timeout
- [ ] Execute task using LLM with tools
- [ ] Return structured result (files changed, output, errors)

#### Consultation Pattern
- [ ] Librarian first: "Does solution exist in codebase?"
- [ ] Archivalist second: "Have we solved this before?"
- [ ] Academic last: "What's the best practice?"
- [ ] Consultation is for CONTEXT, never for skipping work

#### Signals
- [ ] Help signal: request clarification via bus → Orchestrator
- [ ] Completion signal: task done with result
- [ ] Failure signal: task failed with error
- [ ] Progress signal: intermediate status updates

#### Bus Integration
- [ ] Subscribe to `engineer.{id}.task` (per-engineer, session-scoped)
- [ ] Publish to `engineer.{id}.result`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Complete`, `Help`

#### Engineer Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// read_file - Read a file
skills.NewSkill("read_file").
    Description("Read the contents of a file").
    Domain("file_ops").
    Keywords("read", "file", "content", "show").
    StringParam("path", "File path to read", true)

// write_file - Write to a file
skills.NewSkill("write_file").
    Description("Write content to a file, creating or overwriting").
    Domain("file_ops").
    Keywords("write", "file", "create", "save").
    StringParam("path", "File path to write", true).
    StringParam("content", "Content to write", true)

// edit_file - Edit a file
skills.NewSkill("edit_file").
    Description("Edit a file by replacing content").
    Domain("file_ops").
    Keywords("edit", "replace", "modify", "change").
    StringParam("path", "File path to edit", true).
    StringParam("old_content", "Content to replace", true).
    StringParam("new_content", "Replacement content", true)

// run_command - Run a bash command
skills.NewSkill("run_command").
    Description("Run a bash command").
    Domain("bash").
    Keywords("run", "command", "bash", "shell", "execute").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false)
```

**Extended Skills (loaded on demand):**
```go
// consult_librarian - Query codebase
skills.NewSkill("consult_librarian").
    Description("Query the codebase for existing implementations").
    Domain("consultation").
    Keywords("codebase", "existing", "implemented", "find").
    StringParam("query", "What to look for", true)

// consult_archivalist - Query history
skills.NewSkill("consult_archivalist").
    Description("Query historical solutions and decisions").
    Domain("consultation").
    Keywords("history", "before", "past", "previous").
    StringParam("query", "What to look for", true)

// consult_academic - Research query
skills.NewSkill("consult_academic").
    Description("Research best practices and approaches").
    Domain("consultation").
    Keywords("research", "best", "practice", "approach").
    StringParam("query", "What to research", true)

// request_help - Ask for clarification
skills.NewSkill("request_help").
    Description("Request clarification from the Architect").
    Domain("communication").
    Keywords("help", "clarify", "confused", "unclear").
    StringParam("question", "What needs clarification", true).
    StringParam("context", "Relevant context", false)

// report_progress - Report task progress
skills.NewSkill("report_progress").
    Description("Report progress on the current task").
    Domain("communication").
    Keywords("progress", "status", "update").
    StringParam("status", "Current status", true).
    IntParam("percent_complete", "Completion percentage", false)
```

**Search & Navigation Skills (dynamically configurable):**
```go
// find_files - Find files using find command
skills.NewSkill("find_files").
    Description("Find files using the find command with various criteria").
    Domain("search").
    Keywords("find", "locate", "files", "directory", "name", "type").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory, l=symlink)", false).
    StringParam("mtime", "Modified time (e.g., -7 for last 7 days)", false).
    StringParam("size", "File size (e.g., +1M for >1MB)", false).
    IntParam("maxdepth", "Maximum directory depth", false).
    ArrayParam("exec", "Command to execute on found files", false)

// ast_grep - Search code using AST patterns
skills.NewSkill("ast_grep").
    Description("Search code using AST structural patterns").
    Domain("search").
    Keywords("ast", "pattern", "structural", "syntax").
    StringParam("pattern", "AST grep pattern", true).
    StringParam("language", "Target language", true).
    StringParam("path", "Search path", false)

// glob_search - Find files by glob pattern
skills.NewSkill("glob_search").
    Description("Find files matching a glob pattern").
    Domain("search").
    Keywords("glob", "find", "files", "pattern").
    StringParam("pattern", "Glob pattern (e.g., **/*.go)", true).
    StringParam("path", "Base path", false)

// grep_search - Search file contents with regex
skills.NewSkill("grep_search").
    Description("Search file contents using regex").
    Domain("search").
    Keywords("grep", "regex", "search", "content").
    StringParam("pattern", "Regex pattern", true).
    StringParam("path", "Search path", false).
    BoolParam("case_insensitive", "Case insensitive search", false)

// cat_file - Read file contents (alias for read_file with options)
skills.NewSkill("cat_file").
    Description("Display file contents with line numbers").
    Domain("file_ops").
    Keywords("cat", "display", "show", "print").
    StringParam("path", "File path", true).
    IntParam("from_line", "Start from line", false).
    IntParam("to_line", "End at line", false)

// sed_replace - Stream editor replace
skills.NewSkill("sed_replace").
    Description("Replace text patterns in files").
    Domain("file_ops").
    Keywords("sed", "replace", "substitute", "regex").
    StringParam("pattern", "Search pattern", true).
    StringParam("replacement", "Replacement text", true).
    StringParam("path", "File path", true).
    BoolParam("global", "Replace all occurrences", false)

// awk_process - Process text with awk
skills.NewSkill("awk_process").
    Description("Process text using awk patterns").
    Domain("file_ops").
    Keywords("awk", "process", "text", "columns").
    StringParam("program", "Awk program", true).
    StringParam("path", "Input file path", true)

// ping_host - Network connectivity check
skills.NewSkill("ping_host").
    Description("Check network connectivity to a host").
    Domain("network").
    Keywords("ping", "network", "connectivity", "host").
    StringParam("host", "Host to ping", true).
    IntParam("count", "Number of pings", false)
```

**Code Quality Tools (dynamically configurable):**
```go
// run_linter - Run configured linter
skills.NewSkill("run_linter").
    Description("Run the configured linter for the language").
    Domain("quality").
    Keywords("lint", "linter", "check", "style").
    StringParam("path", "Path to lint", true).
    StringParam("linter", "Specific linter (auto-detect if not specified)", false).
    BoolParam("fix", "Auto-fix issues if supported", false)

// run_formatter - Run configured formatter
skills.NewSkill("run_formatter").
    Description("Run the configured formatter for the language").
    Domain("quality").
    Keywords("format", "formatter", "style", "prettify").
    StringParam("path", "Path to format", true).
    StringParam("formatter", "Specific formatter (auto-detect if not specified)", false).
    BoolParam("check_only", "Only check, don't modify", false)

// query_lsp - Query language server for information
skills.NewSkill("query_lsp").
    Description("Query the language server for code intelligence").
    Domain("quality").
    Keywords("lsp", "language", "server", "completion", "hover").
    StringParam("path", "File path", true).
    IntParam("line", "Line number", true).
    IntParam("column", "Column number", true).
    EnumParam("query_type", "Type of query", []string{"hover", "definition", "references", "completion"}, true)
```

**Environment & Package Skills (dynamically configurable):**
```go
// create_venv - Create virtual environment
skills.NewSkill("create_venv").
    Description("Create a virtual environment for the project").
    Domain("environment").
    Keywords("venv", "virtualenv", "environment", "isolate").
    StringParam("path", "Environment path", true).
    EnumParam("type", "Environment type", []string{"python-venv", "python-virtualenv", "node-nvm", "go-mod"}, true).
    StringParam("version", "Language/runtime version", false)

// activate_venv - Activate virtual environment
skills.NewSkill("activate_venv").
    Description("Activate a virtual environment").
    Domain("environment").
    Keywords("activate", "venv", "environment").
    StringParam("path", "Environment path", true)

// install_packages - Install packages
skills.NewSkill("install_packages").
    Description("Install packages into the environment").
    Domain("environment").
    Keywords("install", "package", "dependency", "npm", "pip", "go").
    ArrayParam("packages", "Package names to install", true).
    StringParam("manager", "Package manager (auto-detect if not specified)", false).
    BoolParam("dev", "Install as dev dependency", false)

// list_packages - List installed packages
skills.NewSkill("list_packages").
    Description("List installed packages in the environment").
    Domain("environment").
    Keywords("list", "packages", "installed", "dependencies").
    StringParam("path", "Environment or project path", false)
```

**Bash Execution (user-approved commands):**
```go
// run_approved_bash - Run pre-approved bash command
skills.NewSkill("run_approved_bash").
    Description("Run a bash command from the approved list").
    Domain("bash").
    Keywords("bash", "shell", "command", "run").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false).
    BoolParam("background", "Run in background", false)

// Engineer maintains a list of user-approved command patterns
type ApprovedCommandPatterns struct {
    Patterns  []string  // Regex patterns for allowed commands
    Blocklist []string  // Explicitly blocked commands
}

// Default approved patterns (user can extend)
var DefaultApprovedPatterns = ApprovedCommandPatterns{
    Patterns: []string{
        `^go (build|test|run|fmt|vet|mod).*`,
        `^npm (install|run|test|build).*`,
        `^pip (install|list|freeze).*`,
        `^git (status|log|diff|branch|checkout).*`,
        `^cat .*`,
        `^ls .*`,
        `^grep .*`,
        `^find .*`,
    },
    Blocklist: []string{
        `rm -rf /`,
        `sudo .*`,
        `chmod 777.*`,
    },
}
```

#### Engineer Tools
```go
var EngineerTools = []ToolDefinition{
    // Core file operations
    {Name: "read_file", Skill: "read_file"},
    {Name: "write_file", Skill: "write_file"},
    {Name: "edit_file", Skill: "edit_file"},
    {Name: "run_command", Skill: "run_command"},
    // Consultation
    {Name: "consult_librarian", Skill: "consult_librarian"},
    {Name: "consult_archivalist", Skill: "consult_archivalist"},
    {Name: "request_help", Skill: "request_help"},
    // Search & navigation
    {Name: "find_files", Skill: "find_files"},
    {Name: "ast_grep", Skill: "ast_grep"},
    {Name: "glob_search", Skill: "glob_search"},
    {Name: "grep_search", Skill: "grep_search"},
    {Name: "cat_file", Skill: "cat_file"},
    {Name: "sed_replace", Skill: "sed_replace"},
    {Name: "awk_process", Skill: "awk_process"},
    // Code quality
    {Name: "run_linter", Skill: "run_linter"},
    {Name: "run_formatter", Skill: "run_formatter"},
    {Name: "query_lsp", Skill: "query_lsp"},
    // Environment
    {Name: "create_venv", Skill: "create_venv"},
    {Name: "activate_venv", Skill: "activate_venv"},
    {Name: "install_packages", Skill: "install_packages"},
    // Routing
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
}
```

#### Engineer Hooks
- [ ] Pre-execute hook: load relevant skills based on task
- [ ] Post-execute hook: record result in Archivalist
- [ ] Pre-file-write hook: validate file path safety
- [ ] Post-file-write hook: notify file changed event
- [ ] Pre-consultation hook: check if already consulted
- [ ] Post-consultation hook: cache consultation result

#### Memory Management & Pipeline Handoff

**Model**: Opus 4.5

**CRITICAL**: At 95%, Engineer triggers PIPELINE HANDOFF, NOT local compaction.

**Files to add:**
- `agents/engineer/memory.go`
- `agents/engineer/handoff.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 95% threshold: trigger pipeline handoff sequence
- [ ] Bundle complete handoff state: original prompt + accomplished + remaining + files changed
- [ ] Send `HANDOFF_REQUEST` to Architect (via Guide) with bundled state
- [ ] Wait for `HANDOFF_ACK` confirming new pipeline created
- [ ] Handoff state goes to Archivalist for persistence
- [ ] Retry logic: 3 attempts with exponential backoff
- [ ] Fallback: if handoff fails after retries, summarize → Archivalist → compact locally

```go
// Engineer handoff state (sent to Architect)
type EngineerHandoffState struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    OriginalPrompt    string            `json:"original_prompt"`
    Accomplished      []string          `json:"accomplished"`
    FilesChanged      []FileChange      `json:"files_changed"`
    Remaining         []string          `json:"remaining"`
    ContextNotes      string            `json:"context_notes"`
    ContextUsage      float64           `json:"context_usage"`
    HandoffReason     string            `json:"handoff_reason"`
}

type FileChange struct {
    Path        string `json:"path"`
    ChangeType  string `json:"change_type"`  // created, modified, deleted
    Summary     string `json:"summary"`
}

// Engineer context monitoring
func (e *Engineer) checkContextAndHandoff() error {
    usage := e.getContextUsage()

    if usage >= 0.95 {
        // Trigger pipeline handoff (NOT local compaction)
        return e.triggerPipelineHandoff()
    }
    return nil
}

func (e *Engineer) triggerPipelineHandoff() error {
    state := e.buildHandoffState()

    // Send to Architect via Guide
    msg := &Message{
        Type:    "HANDOFF_REQUEST",
        From:    e.ID,
        To:      "architect",  // Routed through Guide
        Payload: state,
    }

    // Retry logic
    for attempt := 0; attempt < 3; attempt++ {
        if err := e.bus.Publish("guide.route", msg); err != nil {
            time.Sleep(time.Duration(1<<attempt) * time.Second)
            continue
        }

        // Wait for acknowledgment
        select {
        case ack := <-e.handoffAck:
            return nil  // Handoff successful
        case <-time.After(30 * time.Second):
            continue  // Retry
        }
    }

    // Fallback: summarize and compact locally
    return e.fallbackCompact()
}
```

**Handoff Flow:**
```
Engineer (95%) ──────────────────────────────────────────────────────────────►
    │
    ▼
[Build Handoff State]
    │ - Original prompt
    │ - Accomplished tasks
    │ - Files changed
    │ - Remaining tasks
    │ - Context notes
    │
    ▼
[HANDOFF_REQUEST] ──► Guide ──► Architect
                                   │
                                   ▼
                          [Examine state]
                          [Adjust workflow if needed]
                                   │
                                   ▼
               [CREATE_PIPELINE_WITH_STATE] ──► Guide ──► Orchestrator
                                                            │
                                                            ▼
                                                    [Create new pipeline]
                                                    [Transfer E+I+T state]
                                                    [Close old pipeline]
                                                            │
                                                            ▼
                                                    [HANDOFF_COMPLETE] ──► Architect ──► Engineer
```

**Tests:**
- [ ] Execute file read/write task
- [ ] Test consultation flow
- [ ] Test help request routing
- [ ] Test handoff trigger at 95% context
- [ ] Test handoff retry logic
- [ ] Test handoff fallback to local compaction
- [ ] Test handoff state bundling completeness

### 2.2 Orchestrator (DAG Execution Engine)

Executes DAG workflows, manages Engineers.

**Files to create:**
- `agents/orchestrator/types.go`
- `agents/orchestrator/dispatcher.go`
- `agents/orchestrator/correlator.go`
- `agents/orchestrator/engineer_pool.go`
- `agents/orchestrator/orchestrator.go`
- `agents/orchestrator/routing.go`
- `agents/orchestrator/skills.go`
- `agents/orchestrator/tools.go`
- `agents/orchestrator/hooks.go`
- `agents/orchestrator/orchestrator_test.go`

**Acceptance Criteria:**

#### DAG Execution
- [ ] Accept DAG from Architect via bus
- [ ] Use DAGExecutor from Phase 0.2
- [ ] **Create Pipelines for engineer tasks** (not bare Engineers) - see Phase 2.3
- [ ] Correlate responses by correlation ID
- [ ] Track node state and timing

#### Pipeline Management (replaces direct Engineer management)
- [ ] **Create Pipeline for each engineer task** (contains Engineer + Inspector + Tester)
- [ ] Use PipelineManager from Phase 2.3
- [ ] Wait for PIPELINE_COMPLETE or PIPELINE_FAILED
- [ ] Aggregate pipeline results for DAG node completion
- [ ] Session-scoped pipeline tracking

#### Engineer Management (for non-pipeline tasks)
- [ ] Create Engineers on demand for non-code tasks (research, context gathering)
- [ ] Pool Engineers for reuse
- [ ] Destroy idle Engineers after timeout
- [ ] Session-scoped Engineer pool

#### Status Propagation
- [ ] Status updates to Architect
- [ ] Per-node progress events
- [ ] Overall DAG progress (layers completed / total)
- [ ] Estimated time remaining

#### Quality Loop (Two-Level)

**Level 1: Pipeline-Internal (handled within Pipeline - see Phase 2.3)**
- [ ] Pipeline handles per-task Inspector validation internally
- [ ] Pipeline handles per-task Tester validation internally
- [ ] Direct feedback loops within pipeline (no Architect involvement)

**Level 2: Session-Wide (after all Pipelines complete)**
- [ ] Full validation: signal Inspector when ALL pipelines complete
- [ ] Full test suite: signal Tester when Inspector passes
- [ ] Handle corrections from session-wide Inspector/Tester via Architect
- [ ] Architect creates FIX DAG for session-wide issues → new Pipelines

#### Clarification Routing
- [ ] Engineer help requests → Architect
- [ ] Architect responses → Engineer via correlation

#### Bus Integration
- [ ] Subscribe to `orchestrator.execute` (session-scoped)
- [ ] Subscribe to `orchestrator.cancel` (session-scoped)
- [ ] Publish to `orchestrator.status`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `DAG_EXECUTE`, `DAG_STATUS`, `DAG_CANCEL`

#### Orchestrator Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// execute_dag - Execute a DAG workflow
skills.NewSkill("execute_dag").
    Description("Execute a DAG workflow").
    Domain("orchestration").
    Keywords("execute", "run", "dag", "workflow").
    ObjectParam("dag", "DAG definition", true)

// get_status - Get execution status
skills.NewSkill("get_status").
    Description("Get current execution status").
    Domain("orchestration").
    Keywords("status", "progress", "state").
    StringParam("dag_id", "DAG ID (optional, defaults to current)", false)

// cancel_execution - Cancel current execution
skills.NewSkill("cancel_execution").
    Description("Cancel the current DAG execution").
    Domain("orchestration").
    Keywords("cancel", "stop", "abort").
    StringParam("dag_id", "DAG ID (optional, defaults to current)", false)
```

**Extended Skills (loaded on demand):**
```go
// pause_execution - Pause execution
skills.NewSkill("pause_execution").
    Description("Pause the current DAG execution").
    Domain("orchestration").
    Keywords("pause", "hold", "wait").
    StringParam("dag_id", "DAG ID", false)

// resume_execution - Resume execution
skills.NewSkill("resume_execution").
    Description("Resume a paused DAG execution").
    Domain("orchestration").
    Keywords("resume", "continue", "unpause").
    StringParam("dag_id", "DAG ID", false)

// retry_node - Retry a failed node
skills.NewSkill("retry_node").
    Description("Retry a specific failed node").
    Domain("orchestration").
    Keywords("retry", "again", "rerun").
    StringParam("node_id", "Node ID to retry", true)

// skip_node - Skip a failed node
skills.NewSkill("skip_node").
    Description("Skip a failed node and continue").
    Domain("orchestration").
    Keywords("skip", "ignore", "bypass").
    StringParam("node_id", "Node ID to skip", true)

// get_node_result - Get result of a node
skills.NewSkill("get_node_result").
    Description("Get the result of a specific node").
    Domain("orchestration").
    Keywords("result", "output", "node").
    StringParam("node_id", "Node ID", true)
```

**Workflow Adjustment Skills:**
```go
// receive_workflow_update - Receive updated workflow from Architect
skills.NewSkill("receive_workflow_update").
    Description("Receive and apply an updated workflow from Architect").
    Domain("orchestration").
    Keywords("receive", "update", "workflow", "modified").
    StringParam("dag_id", "Original DAG ID being updated", true).
    ObjectParam("updated_dag", "New DAG definition", true).
    EnumParam("merge_strategy", "How to merge with current state", []string{"replace", "merge_pending", "abort_and_replace"}, false)

// receive_workflow_cancellation - Receive workflow cancellation
skills.NewSkill("receive_workflow_cancellation").
    Description("Receive and process workflow cancellation signal").
    Domain("orchestration").
    Keywords("cancel", "abort", "stop", "workflow").
    StringParam("dag_id", "DAG ID to cancel", true).
    EnumParam("cleanup_mode", "Cleanup mode", []string{"graceful", "immediate", "wait_current"}, false).
    StringParam("reason", "Cancellation reason", false)

// apply_workflow_adjustment - Apply runtime workflow adjustment
skills.NewSkill("apply_workflow_adjustment").
    Description("Apply runtime adjustments to executing workflow").
    Domain("orchestration").
    Keywords("adjust", "runtime", "modify", "live").
    StringParam("dag_id", "DAG ID to adjust", true).
    ObjectParam("adjustments", "List of adjustments", true)

// Adjustment types
type WorkflowAdjustment struct {
    Type       string // "add_node", "remove_node", "modify_node", "change_deps", "reprioritize"
    NodeID     string
    Details    map[string]any
}
```

**Plan Change Request Skills:**
```go
// request_plan_change - Request Architect to modify plan
skills.NewSkill("request_plan_change").
    Description("Request Architect to create new or modified workflow").
    Domain("orchestration").
    Keywords("request", "plan", "change", "modify", "architect").
    EnumParam("change_type", "Type of change requested", []string{"extend", "modify", "replace", "fix"}, true).
    StringParam("reason", "Reason for requested change", true).
    ObjectParam("context", "Context for the request (failed nodes, blockers, etc.)", true).
    ObjectParam("suggested_changes", "Optional suggested modifications", false)

// escalate_blocker - Escalate blocking issue to Architect
skills.NewSkill("escalate_blocker").
    Description("Escalate a blocking issue that requires plan changes").
    Domain("orchestration").
    Keywords("escalate", "blocker", "issue", "stuck").
    StringParam("node_id", "Blocked node ID", true).
    StringParam("blocker_type", "Type of blocker", true).
    StringParam("description", "Description of the blocker", true).
    ArrayParam("attempted_resolutions", "What was already tried", false)

// Request/response types for plan changes
type PlanChangeRequest struct {
    ID              string
    ChangeType      string                 // extend, modify, replace, fix
    Reason          string
    BlockedNodes    []string               // Nodes that are blocked
    FailedNodes     []string               // Nodes that failed
    Context         map[string]any         // Additional context
    SuggestedChanges []WorkflowAdjustment  // Optional suggestions
    Priority        string                 // How urgent is this
    CreatedAt       time.Time
}

type PlanChangeResponse struct {
    RequestID       string
    Approved        bool
    NewDAG          *DAG                   // New or modified DAG if approved
    Modifications   []WorkflowAdjustment   // Specific modifications if partial
    Reason          string                 // Reason if rejected
    Instructions    string                 // Additional instructions
}
```

#### Orchestrator Tools
```go
var OrchestratorTools = []ToolDefinition{
    // Core execution
    {Name: "execute_dag", Skill: "execute_dag"},
    {Name: "get_status", Skill: "get_status"},
    {Name: "cancel_execution", Skill: "cancel_execution"},
    // Workflow management
    {Name: "pause_execution", Skill: "pause_execution"},
    {Name: "resume_execution", Skill: "resume_execution"},
    {Name: "retry_node", Skill: "retry_node"},
    {Name: "skip_node", Skill: "skip_node"},
    {Name: "get_node_result", Skill: "get_node_result"},
    // Workflow adjustment
    {Name: "receive_workflow_update", Skill: "receive_workflow_update"},
    {Name: "receive_workflow_cancellation", Skill: "receive_workflow_cancellation"},
    {Name: "apply_workflow_adjustment", Skill: "apply_workflow_adjustment"},
    // Plan changes
    {Name: "request_plan_change", Skill: "request_plan_change"},
    {Name: "escalate_blocker", Skill: "escalate_blocker"},
    // Routing
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
}
```

#### Orchestrator Hooks
- [ ] Pre-execute hook: validate DAG structure
- [ ] Post-execute hook: store execution history in Archivalist
- [ ] Pre-dispatch hook: check Engineer availability
- [ ] Post-dispatch hook: record dispatch time
- [ ] Pre-complete hook: trigger Inspector validation
- [ ] Post-complete hook: notify Architect

**Tests:**
- [ ] Execute multi-layer DAG
- [ ] Test Engineer pool management
- [ ] Test clarification routing
- [ ] Test cancellation

### 2.3 Pipeline Infrastructure

Isolated execution contexts containing Engineer + Inspector + Tester with direct feedback loops.

**CRITICAL**: Pipelines enable TWO-LEVEL quality assurance:
1. **Pipeline-internal (task-specific)**: Direct Engineer ↔ Inspector ↔ Tester feedback loops
2. **Session-wide (post-DAG)**: Full validation through Architect after all pipelines complete

**Files to create:**
- `core/pipeline/types.go`
- `core/pipeline/pipeline.go`
- `core/pipeline/bus.go`
- `core/pipeline/manager.go`
- `core/pipeline/lifecycle.go`
- `core/pipeline/pipeline_test.go`

**Dependencies**: Phase 2.1 (Engineer), Phase 2.2 (Orchestrator)

**Acceptance Criteria:**

#### Pipeline Data Model (`core/pipeline/types.go`)
- [ ] `PipelineID` type with unique generation
- [ ] `PipelineState` enum: `Created`, `Running`, `Inspecting`, `Testing`, `Completed`, `Failed`
- [ ] `Pipeline` struct with:
  - [ ] ID (PipelineID), SessionID, DAGID, TaskName (human-readable, e.g. `create_dashboard`)
  - [ ] State, CreatedAt, CompletedAt
  - [ ] EngineerID, InspectorID, TesterID (co-located instances)
  - [ ] `PipelineContext` (shared by all three agents)
  - [ ] InspectorLoops, TesterLoops, MaxLoops counters
  - [ ] EngineerResult, InspectorResult, TesterResult
- [ ] `PipelineContext` struct with:
  - [ ] TaskPrompt, TaskConstraints, ComplianceCriteria
  - [ ] UpstreamOutputs (from DAG dependencies)
  - [ ] ModifiedFiles, CreatedFiles (tracked by pipeline)
  - [ ] InspectorFeedback history, TesterFeedback history
- [ ] `InspectorFeedback` struct with Loop, Timestamp, Issues, Passed
- [ ] `InspectorIssue` struct with File, Line, Category, Severity, Message, Suggestion
- [ ] `TesterFeedback` struct with Loop, Timestamp, TestsRun, TestsPassed, Failures, Passed
- [ ] `TestFailure` struct with TestName, File, Message, Expected, Actual, StackTrace

#### Pipeline Internal Bus (`core/pipeline/bus.go`)
- [ ] `PipelineBus` struct for direct communication (NOT routed through Guide)
- [ ] Channels: inspectorToEngineer, testerToEngineer
- [ ] Channels: engineerDone, inspectorDone, testerDone
- [ ] `SendInspectorFeedback()` - direct to Engineer, bypasses Guide
- [ ] `SendTesterFeedback()` - direct to Engineer, bypasses Guide
- [ ] Context-based cancellation
- [ ] Graceful shutdown

```go
type PipelineBus struct {
    pipelineID          PipelineID
    inspectorToEngineer chan *InspectorFeedback
    testerToEngineer    chan *TesterFeedback
    engineerDone        chan *EngineerResult
    inspectorDone       chan *InspectorResult
    testerDone          chan *TesterResult
    ctx                 context.Context
    cancel              context.CancelFunc
    closed              atomic.Bool
}
```

#### Pipeline Lifecycle (`core/pipeline/lifecycle.go`)
- [ ] State machine: Created → Running → Inspecting → Testing → Completed
- [ ] State machine: Any state → Failed (on error or max loops exceeded)
- [ ] Engineer execution phase
- [ ] Inspector validation phase with direct feedback loop
- [ ] Tester validation phase with direct feedback loop
- [ ] Max loops enforcement (default: 3)
- [ ] User override handling (`/task <name> ignore_inspector`, `/task <name> ignore_tester`)

```
Pipeline Lifecycle:
  CREATED → RUNNING (Engineer executes)
          → INSPECTING (Inspector validates)
            ├── Issues + loops < max → RUNNING (direct feedback)
            ├── Issues + loops >= max → User prompted
            └── Pass → TESTING (Tester runs)
                       ├── Failures + loops < max → RUNNING (direct feedback)
                       ├── Failures + loops >= max → User prompted
                       └── Pass → COMPLETED
```

#### Pipeline Manager (`core/pipeline/manager.go`)
- [ ] `PipelineManager` interface with Create, Start, Cancel
- [ ] `PipelineManager` interface with Get, GetBySession, GetByDAG, GetActive
- [ ] `PipelineManager` interface with RouteUserMessage (for /task command)
- [ ] `PipelineManager` interface with GetResult, CloseAll
- [ ] `CreatePipelineConfig` with SessionID, DAGID, TaskName (human-readable), TaskPrompt, TaskConstraints, ComplianceCriteria, UpstreamOutputs, MaxLoops
- [ ] `PipelineResult` with PipelineID, Success, EngineerResult, InspectorResult, TesterResult, ModifiedFiles, CreatedFiles, LoopsUsed, Duration
- [ ] Concurrent pipeline execution within session
- [ ] Pipeline-scoped resource allocation

```go
type PipelineManager interface {
    Create(ctx context.Context, cfg CreatePipelineConfig) (*Pipeline, error)
    Start(ctx context.Context, id PipelineID) error
    Cancel(ctx context.Context, id PipelineID) error
    Get(id PipelineID) (*Pipeline, bool)
    GetBySession(sessionID string) []*Pipeline
    GetByDAG(dagID string) []*Pipeline
    GetActive() []*Pipeline
    RouteUserMessage(pipelineID PipelineID, msg string) error
    GetResult(id PipelineID) (*PipelineResult, error)
    CloseAll() error
}
```

#### Guide Integration for /task Command
- [ ] New Guide skill: `task_interact` for routing to specific pipelines
- [ ] Actions: prompt, query, interrupt, ignore_inspector, ignore_tester, handoff, stop_handoff
- [ ] Route user messages through Guide to Pipeline to Engineer
- [ ] **Task names are human-readable, generated by Architect as DAG node keys**
- [ ] Support task name lookup (exact match or fuzzy match for convenience)

```go
// Guide skill for pipeline interaction
skills.NewSkill("task_interact").
    Description("Route user message to a specific pipeline's engineer").
    Domain("routing").
    Keywords("/task", "pipeline", "engineer").
    StringParam("task_name", "Human-readable task name (DAG node key)", true).
    EnumParam("action", "Action type", []string{"prompt", "query", "interrupt", "ignore_inspector", "ignore_tester", "handoff", "stop_handoff"}, true).
    StringParam("message", "Message content (for prompt/query)", false)

// Usage examples (task names generated by Architect):
// /task create_dashboard prompt "Focus on error handling first"
// /task setup_auth_middleware query "What files have you modified?"
// /task implement_user_model interrupt
// /task add_api_routes ignore_inspector
// /task write_unit_tests ignore_tester
// /task create_dashboard handoff         ← User-triggered pipeline handoff
// /task setup_auth_middleware stop_handoff  ← Cancel/prevent handoff
```

#### Message Types (Pipeline-Specific)
- [ ] `INSPECTOR_FEEDBACK` - Inspector → Engineer (pipeline-internal, NOT through Guide)
- [ ] `TESTER_FEEDBACK` - Tester → Engineer (pipeline-internal, NOT through Guide)
- [ ] `PIPELINE_COMPLETE` - Pipeline → Orchestrator
- [ ] `PIPELINE_FAILED` - Pipeline → Orchestrator
- [ ] `USER_TASK_PROMPT` - User → Guide → Pipeline (through Guide)
- [ ] `USER_TASK_QUERY` - User → Guide → Pipeline (through Guide)
- [ ] `USER_TASK_INTERRUPT` - User → Guide → Pipeline (through Guide)
- [ ] `USER_IGNORE_INSPECTOR` - User → Guide → Pipeline (through Guide)
- [ ] `USER_IGNORE_TESTER` - User → Guide → Pipeline (through Guide)
- [ ] `USER_TRIGGER_HANDOFF` - User → Guide → Pipeline → Engineer (user-initiated handoff)
- [ ] `USER_STOP_HANDOFF` - User → Guide → Pipeline (cancel/prevent handoff)

#### Session Context Updates
- [ ] Add `ActivePipelines map[string]*PipelineState` to SessionContext
- [ ] Pipeline state visible in session status
- [ ] Pipeline cleanup on session close

#### Pipeline Handoff Mechanism

**CRITICAL**: When Engineer hits 95% context, triggers PIPELINE HANDOFF (not local compaction).

**Files to add:**
- `core/pipeline/handoff.go`

**Acceptance Criteria:**

##### Handoff Data Structures
- [ ] `PipelineHandoff` struct containing:
  - [ ] OldPipelineID, NewPipelineID
  - [ ] SessionID, DAGID, TaskID
  - [ ] HandoffReason, HandoffIndex (chains are traceable)
  - [ ] Timestamp
  - [ ] EngineerHandoffState, InspectorHandoffState, TesterHandoffState
- [ ] `EngineerHandoffState` with OriginalPrompt, Accomplished, FilesChanged, Remaining, ContextNotes
- [ ] `InspectorHandoffState` with CurrentFindings, ResolvedByEngineer, PendingForEngineer
- [ ] `TesterHandoffState` with TestsCreated, TestResults, PendingFailures, TestPlanRemaining

```go
type PipelineHandoff struct {
    OldPipelineID   PipelineID              `json:"old_pipeline_id"`
    NewPipelineID   PipelineID              `json:"new_pipeline_id"`
    SessionID       string                  `json:"session_id"`
    DAGID           string                  `json:"dag_id"`
    TaskName        string                  `json:"task_name"`  // Human-readable, e.g. "create_dashboard"
    HandoffReason   string                  `json:"handoff_reason"`
    HandoffIndex    int                     `json:"handoff_index"`  // 0 = first, chains traceable
    Timestamp       time.Time               `json:"timestamp"`
    EngineerState   *EngineerHandoffState   `json:"engineer_state"`
    InspectorState  *InspectorHandoffState  `json:"inspector_state"`
    TesterState     *TesterHandoffState     `json:"tester_state"`
}
```

##### Handoff Flow
- [ ] Engineer detects 95% context usage
- [ ] Engineer builds `EngineerHandoffState` (original prompt, accomplished, remaining, files)
- [ ] Engineer sends `HANDOFF_REQUEST` to Architect (via Guide)
- [ ] Architect examines handoff state
- [ ] Architect adjusts workflow if needed
- [ ] Architect sends `CREATE_PIPELINE_WITH_STATE` to Orchestrator (via Guide)
- [ ] Orchestrator creates new Pipeline with inherited state
- [ ] Inspector and Tester build their handoff states
- [ ] Old Pipeline transfers combined E+I+T state atomically
- [ ] New Pipeline receives state, starts execution
- [ ] Orchestrator closes old Pipeline
- [ ] Handoff state persisted to Archivalist

```
Handoff Flow:
Engineer (95%) ──────────────────────────────────────────────────────────────►
    │
    ▼
[Build Handoff State]
    │ - Original prompt
    │ - Accomplished tasks
    │ - Files changed
    │ - Remaining tasks
    │ - Context notes
    │
    ▼
[HANDOFF_REQUEST] ──► Guide ──► Architect
                                   │
                                   ▼
                          [Examine state]
                          [Adjust workflow if needed]
                                   │
                                   ▼
               [CREATE_PIPELINE_WITH_STATE] ──► Guide ──► Orchestrator
                                                            │
                                                            ▼
                                                    [Create new pipeline]
                                                    [Transfer E+I+T state]
                                                    [Close old pipeline]
                                                            │
                                                            ▼
                                                    [HANDOFF_COMPLETE] ──► Architect ──► Engineer
```

##### Handoff Message Types
- [ ] `HANDOFF_REQUEST` - Engineer → Guide → Architect
- [ ] `CREATE_PIPELINE_WITH_STATE` - Architect → Guide → Orchestrator
- [ ] `HANDOFF_COMPLETE` - Orchestrator → Guide → Architect → Engineer
- [ ] `HANDOFF_FAILED` - Any step can fail with reason

##### Retry and Fallback Logic
- [ ] Retry `HANDOFF_REQUEST` up to 3 times with exponential backoff
- [ ] If retries fail, fallback: summarize → Archivalist → compact locally
- [ ] Handoff failure does NOT lose work (fallback ensures persistence)

##### Handoff Chaining & User Control
- [ ] Handoffs can chain infinitely (Pipeline A → B → C → ...)
- [ ] `HandoffIndex` tracks chain depth
- [ ] User can trigger handoff manually via `/task <name> handoff`
- [ ] User can stop handoff chain via `/task <name> stop_handoff`
- [ ] Each handoff increments index for traceability
- [ ] User-triggered handoff uses same flow as automatic (Engineer 95%)

##### PipelineManager Handoff Methods
- [ ] `CreateWithState(ctx, cfg, handoffState) (*Pipeline, error)` - create pipeline with inherited state
- [ ] `InitiateHandoff(ctx, oldPipelineID) (*PipelineHandoff, error)` - start handoff process
- [ ] `CompleteHandoff(ctx, handoff *PipelineHandoff) error` - finalize handoff
- [ ] `CancelHandoff(ctx, handoff *PipelineHandoff) error` - cancel in-progress handoff

```go
// Extended PipelineManager interface
type PipelineManager interface {
    // ... existing methods ...

    // Handoff methods
    CreateWithState(ctx context.Context, cfg CreatePipelineConfig, state *PipelineHandoff) (*Pipeline, error)
    InitiateHandoff(ctx context.Context, oldPipelineID PipelineID) (*PipelineHandoff, error)
    CompleteHandoff(ctx context.Context, handoff *PipelineHandoff) error
    CancelHandoff(ctx context.Context, handoff *PipelineHandoff) error
    GetHandoffHistory(pipelineID PipelineID) ([]*PipelineHandoff, error)
}
```

**Tests:**
- [ ] Create pipeline, verify all three agents instantiated
- [ ] Execute pipeline, verify Engineer → Inspector → Tester flow
- [ ] Test Inspector feedback loop (issue → fix → re-validate)
- [ ] Test Tester feedback loop (failure → fix → re-test)
- [ ] Test max loops enforcement
- [ ] Test /task command routing through Guide
- [ ] Test user override (ignore_inspector, ignore_tester)
- [ ] Test concurrent pipelines within session
- [ ] Test pipeline cancellation
- [ ] Test handoff trigger at Engineer 95% context
- [ ] Test handoff state bundling (E+I+T combined)
- [ ] Test handoff flow: Engineer → Guide → Architect → Guide → Orchestrator
- [ ] Test handoff retry logic (3 attempts, exponential backoff)
- [ ] Test handoff fallback (summarize → Archivalist → compact)
- [ ] Test handoff chaining (A → B → C)
- [ ] Test user-triggered handoff (/task <name> handoff)
- [ ] Test user stop handoff (/task <name> stop_handoff)
- [ ] Test handoff state persistence to Archivalist

---

## Phase 3: Planning Agent

**Goal**: Build the user-facing planning agent with comprehensive skills.

**Dependencies**: Phase 2 complete

### 3.1 Architect (Primary User Agent)

User's main interface. Creates DAGs from abstract requests.

**Files to create:**
- `agents/architect/types.go`
- `agents/architect/planner.go`
- `agents/architect/dag_builder.go`
- `agents/architect/user_interface.go`
- `agents/architect/interrupt_handler.go`
- `agents/architect/clarification.go`
- `agents/architect/architect.go`
- `agents/architect/routing.go`
- `agents/architect/skills.go`
- `agents/architect/tools.go`
- `agents/architect/hooks.go`
- `agents/architect/architect_test.go`

**Acceptance Criteria:**

#### Planning
- [ ] Accept user requests from Guide
- [ ] Query Librarian for codebase context
- [ ] Query Archivalist for past patterns
- [ ] Query Academic for research (optional, user-triggered)
- [ ] Generate implementation plan with tasks
- [ ] Convert plan to DAG with explicit execution order
- [ ] **Generate human-readable task names as DAG node keys** (e.g., `create_dashboard`, `setup_auth_middleware`)
- [ ] Task names must be unique within DAG, descriptive, snake_case
- [ ] Edge case analysis (empty inputs, boundaries, concurrency)
- [ ] Failure mode analysis (what could go wrong)
- [ ] Mitigation proposals for identified risks

#### User Interaction
- [ ] Present plan to user for approval
- [ ] Handle user modifications to plan
- [ ] Handle user interruptions during execution
- [ ] Route Engineer clarifications to user
- [ ] Deliver status updates during execution
- [ ] Deliver completion summary

#### Fix Workflows
- [ ] Create fix DAGs from Inspector corrections
- [ ] Create fix DAGs from Tester corrections
- [ ] Minimal fix scope (don't re-do working parts)

#### Bus Integration
- [ ] Subscribe to `architect.requests` (session-scoped)
- [ ] Publish to `architect.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Plan`, `Status`, `Help`, `Interrupt`
- [ ] Support domains: `System`, `Agents`, `Workflow`

#### Architect Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// plan - Create implementation plan
skills.NewSkill("plan").
    Description("Create an implementation plan for a request").
    Domain("planning").
    Keywords("plan", "implement", "build", "create", "add").
    StringParam("request", "What to implement", true).
    BoolParam("with_research", "Include Academic research", false)

// status - Get current status
skills.NewSkill("status").
    Description("Get current execution status and progress").
    Domain("status").
    Keywords("status", "progress", "where", "what").
    BoolParam("detailed", "Include detailed breakdown", false)

// approve_plan - Approve a plan
skills.NewSkill("approve_plan").
    Description("Approve the current plan and begin execution").
    Domain("planning").
    Keywords("approve", "ok", "yes", "proceed", "go").
    StringParam("plan_id", "Plan ID (optional)", false)

// modify_plan - Modify the plan
skills.NewSkill("modify_plan").
    Description("Modify the current plan").
    Domain("planning").
    Keywords("modify", "change", "also", "add", "remove").
    StringParam("modification", "What to change", true)
```

**Extended Skills (loaded on demand):**
```go
// interrupt - Interrupt execution
skills.NewSkill("interrupt").
    Description("Interrupt the current execution").
    Domain("control").
    Keywords("stop", "wait", "pause", "interrupt", "hold").
    StringParam("reason", "Why interrupting", false)

// clarify - Request clarification from user
skills.NewSkill("clarify").
    Description("Request clarification from the user").
    Domain("communication").
    Keywords("clarify", "question", "unclear", "which").
    StringParam("question", "What needs clarification", true).
    ArrayParam("options", "Possible options to choose from", false)

// summarize - Summarize completed work
skills.NewSkill("summarize").
    Description("Generate a summary of completed work").
    Domain("reporting").
    Keywords("summarize", "summary", "done", "complete", "finished").
    StringParam("scope", "What to summarize", false)

// analyze_risk - Analyze implementation risks
skills.NewSkill("analyze_risk").
    Description("Analyze risks in the implementation plan").
    Domain("planning").
    Keywords("risk", "danger", "problem", "issue", "concern").
    StringParam("plan_id", "Plan ID (optional)", false)

// query_context - Query for context
skills.NewSkill("query_context").
    Description("Query Librarian, Archivalist, or Academic for context").
    Domain("context").
    Keywords("context", "find", "check", "search").
    StringParam("query", "What to look for", true).
    EnumParam("source", "Where to look", []string{"librarian", "archivalist", "academic", "all"}, false)

// create_fix - Create fix workflow
skills.NewSkill("create_fix").
    Description("Create a fix workflow for corrections").
    Domain("planning").
    Keywords("fix", "correct", "repair", "address").
    ObjectParam("corrections", "List of corrections", true)
```

**Read-Only Bash Skills (for context gathering):**
```go
// find_files - Find files for planning context
skills.NewSkill("find_files").
    Description("Find files using the find command for planning context").
    Domain("search").
    Keywords("find", "locate", "files", "search").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)

// grep_search - Search file contents
skills.NewSkill("grep_search").
    Description("Search file contents using regex for planning context").
    Domain("search").
    Keywords("grep", "regex", "search", "content").
    StringParam("pattern", "Regex pattern", true).
    StringParam("path", "Search path", false).
    BoolParam("case_insensitive", "Case insensitive search", false)

// glob_search - Find files by glob pattern
skills.NewSkill("glob_search").
    Description("Find files matching a glob pattern").
    Domain("search").
    Keywords("glob", "find", "files", "pattern").
    StringParam("pattern", "Glob pattern (e.g., **/*.go)", true).
    StringParam("path", "Base path", false)

// cat_file - Read file contents
skills.NewSkill("cat_file").
    Description("Display file contents for planning context").
    Domain("read").
    Keywords("cat", "read", "show", "display", "file").
    StringParam("path", "File path", true).
    IntParam("from_line", "Start from line", false).
    IntParam("to_line", "End at line", false)

// run_readonly_bash - Run read-only bash commands
skills.NewSkill("run_readonly_bash").
    Description("Run non-destructive bash commands for context gathering").
    Domain("bash").
    Keywords("bash", "shell", "command", "read").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false)

// Read-only command whitelist for Architect (excludes git write operations)
var ArchitectReadOnlyCommands = []string{
    `^cat .*`,
    `^head .*`,
    `^tail .*`,
    `^grep .*`,
    `^find .* -type [fd].*`,
    `^ls .*`,
    `^wc .*`,
    `^diff .*`,
    `^tree .*`,
    `^file .*`,
    `^stat .*`,
    `^du .*`,
    `^pwd$`,
    `^env$`,
    `^which .*`,
    `^type .*`,
    // Note: Excludes git commands (handled by Librarian)
    // Note: Excludes github/remote operations (handled by Librarian)
}
```

**Orchestrator Coordination Skills:**
```go
// schedule_work - Schedule work with orchestrator
skills.NewSkill("schedule_work").
    Description("Schedule work items with the orchestrator for execution").
    Domain("orchestration").
    Keywords("schedule", "queue", "dispatch", "orchestrator").
    ObjectParam("dag", "DAG definition to schedule", true).
    EnumParam("priority", "Execution priority", []string{"critical", "high", "normal", "low"}, false).
    ObjectParam("constraints", "Scheduling constraints (resources, timing)", false)

// adjust_workflow - Adjust executing workflow
skills.NewSkill("adjust_workflow").
    Description("Adjust or modify a currently executing workflow").
    Domain("orchestration").
    Keywords("adjust", "modify", "workflow", "change").
    StringParam("dag_id", "DAG ID to adjust", true).
    EnumParam("action", "Adjustment action", []string{"pause", "resume", "cancel", "modify", "reprioritize"}, true).
    ObjectParam("modifications", "Specific modifications (for 'modify' action)", false)

// signal_orchestrator - Send signal to orchestrator
skills.NewSkill("signal_orchestrator").
    Description("Send control signal to the orchestrator").
    Domain("orchestration").
    Keywords("signal", "orchestrator", "control", "notify").
    EnumParam("signal", "Signal type", []string{"pause_all", "resume_all", "cancel_all", "status_request", "health_check"}, true).
    StringParam("session_id", "Target session (optional)", false)

// get_workflow_status - Get detailed workflow status
skills.NewSkill("get_workflow_status").
    Description("Get detailed status of workflow execution").
    Domain("orchestration").
    Keywords("workflow", "status", "progress", "dag").
    StringParam("dag_id", "DAG ID (optional, defaults to current)", false).
    BoolParam("include_node_details", "Include per-node status", false)
```

#### Architect Tools
```go
var ArchitectTools = []ToolDefinition{
    // Core planning
    {Name: "architect_plan", Skill: "plan"},
    {Name: "architect_status", Skill: "status"},
    {Name: "architect_approve_plan", Skill: "approve_plan"},
    {Name: "architect_modify_plan", Skill: "modify_plan"},
    // Context & communication
    {Name: "architect_clarify", Skill: "clarify"},
    {Name: "architect_query_context", Skill: "query_context"},
    // Control
    {Name: "architect_interrupt", Skill: "interrupt"},
    {Name: "architect_summarize", Skill: "summarize"},
    {Name: "architect_analyze_risk", Skill: "analyze_risk"},
    {Name: "architect_create_fix", Skill: "create_fix"},
    // Orchestrator coordination
    {Name: "architect_schedule_work", Skill: "schedule_work"},
    {Name: "architect_adjust_workflow", Skill: "adjust_workflow"},
    {Name: "architect_signal_orchestrator", Skill: "signal_orchestrator"},
    {Name: "architect_get_workflow_status", Skill: "get_workflow_status"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Architect Memory Management

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact)

**Files to create:**
- `agents/architect/memory.go`
- `agents/architect/workflow_summary.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Trigger workflow summary at 85%
- [ ] Trigger compaction at 95%

##### Workflow Summary (at 85%)
- [ ] `ArchitectWorkflowSummary` struct (retrievable/parseable by other Architects):
  - [ ] OriginalRequest (verbatim user request)
  - [ ] ImplementationPlan (current plan state)
  - [ ] CurrentDAG (DAG summary with node counts, layers)
  - [ ] CompletedTasks (tasks finished with results)
  - [ ] PendingTasks (tasks not yet started)
  - [ ] BlockedTasks (tasks waiting on dependencies)
  - [ ] ArchitecturalDecisions (decisions made and rationale)
  - [ ] Risks (identified risks)
  - [ ] Assumptions (assumptions made)
- [ ] Submit to Archivalist with category `architect_workflow`

```go
type ArchitectWorkflowSummary struct {
    Timestamp              time.Time          `json:"timestamp"`
    SessionID              string             `json:"session_id"`
    ContextUsage           float64            `json:"context_usage"`
    OriginalRequest        string             `json:"original_request"`
    ImplementationPlan     string             `json:"implementation_plan"`
    CurrentDAG             *DAGSummary        `json:"current_dag"`
    CompletedTasks         []TaskSummary      `json:"completed_tasks"`
    PendingTasks           []TaskSummary      `json:"pending_tasks"`
    BlockedTasks           []TaskSummary      `json:"blocked_tasks"`
    ArchitecturalDecisions []Decision         `json:"architectural_decisions"`
    Risks                  []Risk             `json:"risks"`
    Assumptions            []string           `json:"assumptions"`
}
```

##### Compaction at 95%
- [ ] Create final workflow summary before compacting
- [ ] Clear verbose conversation history from context
- [ ] Retain workflow state and decisions
- [ ] Target: ~30% context usage after compaction

##### Pipeline Handoff Handling
- [ ] Receive `HANDOFF_REQUEST` from Engineer (via Guide)
- [ ] Examine handoff state, adjust workflow if needed
- [ ] Send `CREATE_PIPELINE_WITH_STATE` to Orchestrator (via Guide)
- [ ] Receive `HANDOFF_COMPLETE` confirmation

#### Architect Hooks
- [ ] Pre-plan hook: load session context from Archivalist
- [ ] Post-plan hook: store plan in Archivalist
- [ ] Pre-execute hook: confirm user approval
- [ ] Post-execute hook: update session state
- [ ] Pre-interrupt hook: pause current work gracefully
- [ ] Post-complete hook: generate and store summary
- [ ] Context-threshold hook: generate workflow summary at 85%
- [ ] Context-threshold hook: compact at 95%
- [ ] Handoff hook: handle pipeline handoff requests

**Tests:**
- [ ] Generate plan from request
- [ ] Verify DAG structure
- [ ] Test user modification flow
- [ ] Test interrupt handling
- [ ] Test clarification routing
- [ ] Test workflow summary generation at 85%
- [ ] Test compaction at 95%
- [ ] Test pipeline handoff request handling

---

## Phase 4: Quality Agents

**Goal**: Build validation and testing agents with comprehensive skills.

**Dependencies**: Phase 3 complete

**Parallelization**: Items 4.1 and 4.2 can execute in parallel.

### 4.1 Inspector (Code Validator)

Validates code quality and implementation correctness.

**CRITICAL**: Inspector operates in TWO modes:
1. **Pipeline-internal mode**: Task-specific validation within a Pipeline, direct feedback to Engineer
2. **Session-wide mode**: Full validation after all Pipelines complete, feedback through Architect

**Files to create:**
- `agents/inspector/types.go`
- `agents/inspector/validators.go`
- `agents/inspector/static_analysis.go`
- `agents/inspector/pattern_matcher.go`
- `agents/inspector/deep_analyzer.go`
- `agents/inspector/inspector.go`
- `agents/inspector/routing.go`
- `agents/inspector/skills.go`
- `agents/inspector/tools.go`
- `agents/inspector/hooks.go`
- `agents/inspector/pipeline_mode.go` (NEW - pipeline-internal validation)
- `agents/inspector/inspector_test.go`

**Acceptance Criteria:**

#### Dual-Mode Operation
- [ ] `InspectorMode` enum: `PipelineInternal`, `SessionWide`
- [ ] Pipeline-internal: receive from PipelineBus, send feedback directly to Engineer
- [ ] Session-wide: receive from Guide/Bus, send corrections through Architect
- [ ] Mode determined by instantiation context (Pipeline vs standalone)

#### Pipeline-Internal Mode (`pipeline_mode.go`)
- [ ] Receive task result from PipelineBus (not Guide)
- [ ] Run task-specific validation (lint, format, type-check on modified files only)
- [ ] Check task compliance criteria from PipelineContext
- [ ] Send `InspectorFeedback` directly to Engineer via PipelineBus
- [ ] NO routing through Guide or Architect
- [ ] Quick validation (< 5 seconds per task)

```go
// Pipeline-internal validation flow
func (i *Inspector) ValidateInPipeline(ctx context.Context, bus *PipelineBus, result *EngineerResult) (*InspectorFeedback, error) {
    // Validate only the files modified by this task
    issues := i.validateFiles(result.ModifiedFiles)

    // Check task-specific compliance
    compliance := i.checkCompliance(result, bus.Context().ComplianceCriteria)

    feedback := &InspectorFeedback{
        Loop:      bus.Context().InspectorLoops,
        Timestamp: time.Now(),
        Issues:    append(issues, compliance...),
        Passed:    len(issues) == 0 && len(compliance) == 0,
    }

    // Send directly to Engineer (NOT through Guide)
    return feedback, bus.SendInspectorFeedback(feedback)
}
```

#### Per-Task Validation (Session-Wide Mode)
- [ ] Naming conventions check
- [ ] File organization check
- [ ] Import ordering check
- [ ] Error handling patterns check
- [ ] Matches existing codebase patterns (via Librarian)
- [ ] Quick validation (< 5 seconds per task)

#### Full Validation
- [ ] Implementation vs. proposal compliance (Academic output)
- [ ] All components connected and wired correctly
- [ ] No dead code paths
- [ ] Initialization order correct
- [ ] Cleanup/shutdown handled properly
- [ ] API contracts satisfied

#### Deep Analysis
- [ ] Edge cases (empty inputs, boundary conditions, concurrent access)
- [ ] Race conditions (shared state, lock ordering, atomic operations)
- [ ] Memory leaks (unclosed resources, goroutine leaks)
- [ ] Swallowed errors (ignored returns, empty catch blocks)
- [ ] Security issues (injection, path traversal, etc.)

#### Corrections
- [ ] Generate detailed corrections list for Architect
- [ ] Severity levels: Critical, High, Medium, Low, Info
- [ ] Suggested fixes for each issue
- [ ] File and line references

#### User Override
- [ ] Support `USER_OVERRIDE` messages to ignore issues
- [ ] Track overridden issues separately
- [ ] Include overrides in final summary

#### Bus Integration
- [ ] Subscribe to `inspector.requests` (session-scoped)
- [ ] Publish to `inspector.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Check`, `Validate`

#### Inspector Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// validate_task - Validate a single task result
skills.NewSkill("validate_task").
    Description("Validate the output of a single task").
    Domain("validation").
    Keywords("validate", "check", "task", "result").
    ObjectParam("task_result", "Task result to validate", true)

// validate_full - Full implementation validation
skills.NewSkill("validate_full").
    Description("Perform full validation of the implementation").
    Domain("validation").
    Keywords("validate", "full", "complete", "all").
    ObjectParam("results", "All task results", true).
    ObjectParam("proposal", "Original proposal/plan", true)

// get_issues - Get current issues
skills.NewSkill("get_issues").
    Description("Get all issues found during validation").
    Domain("validation").
    Keywords("issues", "problems", "errors", "warnings").
    EnumParam("severity", "Filter by severity", []string{"critical", "high", "medium", "low", "all"}, false)
```

**Extended Skills (loaded on demand):**
```go
// analyze_patterns - Check pattern compliance
skills.NewSkill("analyze_patterns").
    Description("Check code against codebase patterns").
    Domain("analysis").
    Keywords("patterns", "conventions", "style", "compliance").
    StringParam("file", "File to analyze", true)

// analyze_concurrency - Check for race conditions
skills.NewSkill("analyze_concurrency").
    Description("Analyze code for concurrency issues").
    Domain("analysis").
    Keywords("race", "concurrent", "lock", "mutex", "atomic").
    StringParam("file", "File to analyze", true)

// analyze_resources - Check for resource leaks
skills.NewSkill("analyze_resources").
    Description("Analyze code for resource leaks").
    Domain("analysis").
    Keywords("leak", "resource", "close", "defer", "cleanup").
    StringParam("file", "File to analyze", true)

// analyze_errors - Check error handling
skills.NewSkill("analyze_errors").
    Description("Analyze error handling patterns").
    Domain("analysis").
    Keywords("error", "handling", "swallowed", "ignored").
    StringParam("file", "File to analyze", true)

// override_issue - Mark issue as overridden
skills.NewSkill("override_issue").
    Description("Mark an issue as user-overridden").
    Domain("validation").
    Keywords("override", "ignore", "skip", "accept").
    StringParam("issue_id", "Issue ID to override", true).
    StringParam("reason", "Reason for override", false)
```

**Code Quality Execution (dynamically configurable):**
```go
// run_linter - Run linter for validation
skills.NewSkill("run_linter").
    Description("Run linter on code to find style issues").
    Domain("quality").
    Keywords("lint", "linter", "eslint", "golangci", "ruff", "clippy").
    StringParam("path", "Path to lint", true).
    StringParam("linter", "Specific linter (auto-detect by language)", false).
    ArrayParam("rules", "Specific rules to check", false)

// run_formatter_check - Check formatting without modifying
skills.NewSkill("run_formatter_check").
    Description("Check code formatting without modifying files").
    Domain("quality").
    Keywords("format", "check", "style", "prettier", "gofmt", "black").
    StringParam("path", "Path to check", true).
    StringParam("formatter", "Specific formatter (auto-detect by language)", false)

// run_type_checker - Run static type checker
skills.NewSkill("run_type_checker").
    Description("Run static type checker for type errors").
    Domain("quality").
    Keywords("type", "check", "typescript", "mypy", "pyright").
    StringParam("path", "Path to check", true).
    StringParam("checker", "Specific type checker (auto-detect by language)", false).
    BoolParam("strict", "Enable strict mode", false)

// query_lsp_diagnostics - Get LSP diagnostics
skills.NewSkill("query_lsp_diagnostics").
    Description("Get diagnostics from language server").
    Domain("quality").
    Keywords("lsp", "diagnostics", "errors", "warnings").
    StringParam("path", "File path", true).
    StringParam("lsp", "Specific LSP (auto-detect by language)", false)

// query_lsp_hover - Get hover information from LSP
skills.NewSkill("query_lsp_hover").
    Description("Get hover documentation from language server").
    Domain("quality").
    Keywords("lsp", "hover", "docs", "type").
    StringParam("path", "File path", true).
    IntParam("line", "Line number", true).
    IntParam("column", "Column number", true)

// query_lsp_references - Find all references via LSP
skills.NewSkill("query_lsp_references").
    Description("Find all references to a symbol via LSP").
    Domain("quality").
    Keywords("lsp", "references", "usages", "find").
    StringParam("path", "File path", true).
    IntParam("line", "Line number", true).
    IntParam("column", "Column number", true)
```

**Environment Skills (for running quality tools):**
```go
// ensure_venv - Ensure virtual environment exists
skills.NewSkill("ensure_venv").
    Description("Ensure virtual environment exists for running tools").
    Domain("environment").
    Keywords("venv", "environment", "setup").
    StringParam("path", "Project path", true).
    EnumParam("type", "Environment type", []string{"auto", "python", "node"}, false)

// install_tool - Install quality tool in environment
skills.NewSkill("install_tool").
    Description("Install a quality tool in the environment").
    Domain("environment").
    Keywords("install", "tool", "linter", "formatter").
    StringParam("tool", "Tool name", true).
    StringParam("version", "Tool version (optional)", false)

// list_available_tools - List available quality tools
skills.NewSkill("list_available_tools").
    Description("List available quality tools for detected languages").
    Domain("environment").
    Keywords("list", "tools", "available", "linters", "formatters").
    StringParam("path", "Project path", true)

// find_files - Find files for validation
skills.NewSkill("find_files").
    Description("Find files to validate using the find command").
    Domain("search").
    Keywords("find", "locate", "files", "search").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)
```

**Tool Configuration Registry (dynamically loaded):**
```go
// Inspector uses the same ToolRegistry as Librarian but for execution
type InspectorToolConfig struct {
    Name        string
    Command     string
    CheckOnly   string   // Command for check-only mode
    Languages   []string // Supported languages
    ConfigFiles []string // Config file names to look for
    Severity    string   // Default severity for issues
}

// Example configurations
var InspectorTools = map[string]InspectorToolConfig{
    "eslint": {
        Name: "eslint",
        Command: "eslint --format json",
        CheckOnly: "eslint --format json",
        Languages: []string{"javascript", "typescript"},
        ConfigFiles: []string{".eslintrc", ".eslintrc.js", ".eslintrc.json"},
        Severity: "warning",
    },
    "golangci-lint": {
        Name: "golangci-lint",
        Command: "golangci-lint run --out-format json",
        CheckOnly: "golangci-lint run --out-format json",
        Languages: []string{"go"},
        ConfigFiles: []string{".golangci.yml", ".golangci.yaml"},
        Severity: "warning",
    },
    "ruff": {
        Name: "ruff",
        Command: "ruff check --output-format json",
        CheckOnly: "ruff check --output-format json",
        Languages: []string{"python"},
        ConfigFiles: []string{"ruff.toml", "pyproject.toml"},
        Severity: "warning",
    },
    "mypy": {
        Name: "mypy",
        Command: "mypy --output json",
        CheckOnly: "mypy --output json",
        Languages: []string{"python"},
        ConfigFiles: []string{"mypy.ini", "pyproject.toml"},
        Severity: "error",
    },
    "pyright": {
        Name: "pyright",
        Command: "pyright --outputjson",
        CheckOnly: "pyright --outputjson",
        Languages: []string{"python"},
        ConfigFiles: []string{"pyrightconfig.json"},
        Severity: "error",
    },
}
```

#### Inspector Tools
```go
var InspectorTools = []ToolDefinition{
    // Core validation
    {Name: "inspector_validate_task", Skill: "validate_task"},
    {Name: "inspector_validate_full", Skill: "validate_full"},
    {Name: "inspector_get_issues", Skill: "get_issues"},
    // Analysis
    {Name: "inspector_analyze_patterns", Skill: "analyze_patterns"},
    {Name: "inspector_analyze_concurrency", Skill: "analyze_concurrency"},
    {Name: "inspector_analyze_resources", Skill: "analyze_resources"},
    {Name: "inspector_analyze_errors", Skill: "analyze_errors"},
    // Code quality execution
    {Name: "inspector_run_linter", Skill: "run_linter"},
    {Name: "inspector_run_formatter_check", Skill: "run_formatter_check"},
    {Name: "inspector_run_type_checker", Skill: "run_type_checker"},
    {Name: "inspector_query_lsp_diagnostics", Skill: "query_lsp_diagnostics"},
    // Environment
    {Name: "inspector_ensure_venv", Skill: "ensure_venv"},
    {Name: "inspector_install_tool", Skill: "install_tool"},
    // Search
    {Name: "inspector_find_files", Skill: "find_files"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Inspector Hooks
- [ ] Pre-validate hook: load codebase patterns from Librarian
- [ ] Post-validate hook: store validation results in Archivalist
- [ ] Pre-deep-analysis hook: check if analysis already cached
- [ ] Post-corrections hook: format corrections for Architect

#### Memory Management (Local Compaction)

**Model**: OpenAI Codex 5.2

**NOTE**: Inspector compacts LOCALLY. It does NOT trigger pipeline handoff (only Engineer can).

**Files to add:**
- `agents/inspector/memory.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 85% threshold: checkpoint to Archivalist
- [ ] At 95% threshold: compact locally (NOT trigger handoff)
- [ ] Checkpoint includes: findings summary, resolved issues, unresolved issues, priorities, fix references
- [ ] If Engineer triggers handoff, Inspector participates in state transfer

```go
// Inspector checkpoint summary (sent to Archivalist at 85%)
type InspectorCheckpointSummary struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    SessionID         string            `json:"session_id"`
    Timestamp         time.Time         `json:"timestamp"`
    ContextUsage      float64           `json:"context_usage"`
    CheckpointIndex   int               `json:"checkpoint_index"`

    // Validation state
    TotalIssuesFound  int               `json:"total_issues_found"`
    ResolvedIssues    []ResolvedIssue   `json:"resolved_issues"`
    UnresolvedIssues  []UnresolvedIssue `json:"unresolved_issues"`
    OverriddenIssues  []OverriddenIssue `json:"overridden_issues"`

    // Priority fixes
    CriticalFixes     []FixReference    `json:"critical_fixes"`
    HighPriorityFixes []FixReference    `json:"high_priority_fixes"`

    // Loop statistics
    LoopsCompleted    int               `json:"loops_completed"`
    AverageLoopTime   time.Duration     `json:"average_loop_time"`
}

type FixReference struct {
    IssueID     string   `json:"issue_id"`
    Severity    string   `json:"severity"`
    FilePath    string   `json:"file_path"`
    LineNumber  int      `json:"line_number"`
    Description string   `json:"description"`
    SuggestedFix string  `json:"suggested_fix"`
}

// Inspector handoff state (when Engineer triggers handoff)
type InspectorHandoffState struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    CurrentFindings   []Finding         `json:"current_findings"`
    ResolvedByEngineer []ResolvedIssue  `json:"resolved_by_engineer"`
    PendingForEngineer []FixReference   `json:"pending_for_engineer"`
    ValidationHistory []ValidationRun   `json:"validation_history"`
}

func (i *Inspector) checkContextAndManage() error {
    usage := i.getContextUsage()

    if usage >= 0.95 {
        // Compact locally - DO NOT trigger handoff
        return i.compactLocally()
    }

    if usage >= 0.85 {
        // Checkpoint to Archivalist
        return i.submitCheckpoint()
    }

    return nil
}
```

**Tests:**
- [ ] Validate test code with known issues
- [ ] Verify issue detection
- [ ] Test override flow
- [ ] Test corrections generation
- [ ] Test checkpoint at 85% context
- [ ] Test local compaction at 95% context
- [ ] Test handoff state bundling when Engineer triggers handoff

### 4.2 Tester (Test Planner & Executor)

Plans and executes tests, analyzes failures.

**CRITICAL**: Tester operates in TWO modes:
1. **Pipeline-internal mode**: Task-specific tests within a Pipeline, direct feedback to Engineer
2. **Session-wide mode**: Full test suite after all Pipelines complete, feedback through Architect

**Files to create:**
- `agents/tester/types.go`
- `agents/tester/planner.go`
- `agents/tester/generator.go`
- `agents/tester/executor.go`
- `agents/tester/analyzer.go`
- `agents/tester/tester.go`
- `agents/tester/routing.go`
- `agents/tester/skills.go`
- `agents/tester/tools.go`
- `agents/tester/hooks.go`
- `agents/tester/pipeline_mode.go` (NEW - pipeline-internal testing)
- `agents/tester/tester_test.go`

**Acceptance Criteria:**

#### Dual-Mode Operation
- [ ] `TesterMode` enum: `PipelineInternal`, `SessionWide`
- [ ] Pipeline-internal: receive from PipelineBus, send feedback directly to Engineer
- [ ] Session-wide: receive from Guide/Bus, send corrections through Architect
- [ ] Mode determined by instantiation context (Pipeline vs standalone)

#### Pipeline-Internal Mode (`pipeline_mode.go`)
- [ ] Receive validated task from PipelineBus (after Inspector passes)
- [ ] Generate tests for THIS specific task/requirement only
- [ ] Run only relevant tests (not full suite)
- [ ] Send `TesterFeedback` directly to Engineer via PipelineBus
- [ ] NO routing through Guide or Architect
- [ ] Focused testing (task-specific unit tests only)

```go
// Pipeline-internal testing flow
func (t *Tester) TestInPipeline(ctx context.Context, bus *PipelineBus, result *EngineerResult) (*TesterFeedback, error) {
    // Generate tests for this specific task
    tests := t.generateTaskTests(result, bus.Context().TaskPrompt)

    // Run only the generated tests
    testResult := t.runTests(tests)

    feedback := &TesterFeedback{
        Loop:        bus.Context().TesterLoops,
        Timestamp:   time.Now(),
        TestsRun:    testResult.Total,
        TestsPassed: testResult.Passed,
        Failures:    testResult.Failures,
        Passed:      len(testResult.Failures) == 0,
    }

    // Send directly to Engineer (NOT through Guide)
    return feedback, bus.SendTesterFeedback(feedback)
}
```

#### Test Planning (Session-Wide Mode)
- [ ] Identify test cases from implementation
  - [ ] Unit tests (per function/method)
  - [ ] Integration tests (component interaction)
  - [ ] Edge case tests (boundaries, empty, nil)
  - [ ] Failure mode tests (error paths)
- [ ] Generate test plan for user approval
- [ ] Estimate test coverage

#### Test Implementation
- [ ] Create test implementation DAG for Architect
- [ ] Generate test file structure
- [ ] Generate test cases with assertions

#### Test Execution
- [ ] Execute tests via `go test` command
- [ ] Capture test output
- [ ] Parse test results
- [ ] Track test timing

#### Failure Analysis
- [ ] Determine if TEST issue or IMPLEMENTATION issue
- [ ] Test issues: generate test fix
- [ ] Implementation issues: generate corrections for Architect
- [ ] Root cause analysis

#### User Control
- [ ] Skip condition support (--no-test-run)
- [ ] User override support (skip failing tests)
- [ ] Test subset selection

#### Bus Integration
- [ ] Subscribe to `tester.requests` (session-scoped)
- [ ] Publish to `tester.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Plan`, `Execute`, `Analyze`

#### Tester Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// plan_tests - Create test plan
skills.NewSkill("plan_tests").
    Description("Create a test plan for the implementation").
    Domain("testing").
    Keywords("test", "plan", "cases", "coverage").
    ObjectParam("implementation", "Implementation results", true)

// run_tests - Execute tests
skills.NewSkill("run_tests").
    Description("Execute the test suite").
    Domain("testing").
    Keywords("run", "execute", "test").
    StringParam("path", "Test path (optional)", false).
    BoolParam("verbose", "Verbose output", false)

// get_results - Get test results
skills.NewSkill("get_results").
    Description("Get test execution results").
    Domain("testing").
    Keywords("results", "output", "pass", "fail").
    StringParam("run_id", "Test run ID (optional)", false)
```

**Extended Skills (loaded on demand):**
```go
// analyze_failure - Analyze a test failure
skills.NewSkill("analyze_failure").
    Description("Analyze why a test failed").
    Domain("testing").
    Keywords("analyze", "failure", "why", "failed").
    StringParam("test_name", "Name of failed test", true)

// generate_test - Generate a test case
skills.NewSkill("generate_test").
    Description("Generate a specific test case").
    Domain("testing").
    Keywords("generate", "create", "test", "case").
    StringParam("function", "Function to test", true).
    EnumParam("type", "Test type", []string{"unit", "integration", "edge", "failure"}, false)

// skip_test - Skip a test
skills.NewSkill("skip_test").
    Description("Mark a test to be skipped").
    Domain("testing").
    Keywords("skip", "ignore", "disable").
    StringParam("test_name", "Test name to skip", true).
    StringParam("reason", "Reason for skipping", true)

// coverage_report - Generate coverage report
skills.NewSkill("coverage_report").
    Description("Generate a test coverage report").
    Domain("testing").
    Keywords("coverage", "report", "covered", "percent").
    StringParam("package", "Package to report on (optional)", false)

// benchmark - Run benchmarks
skills.NewSkill("benchmark").
    Description("Run performance benchmarks").
    Domain("testing").
    Keywords("benchmark", "performance", "speed", "timing").
    StringParam("pattern", "Benchmark pattern", false)
```

**Test Framework Skills (dynamically configurable):**
```go
// run_test_framework - Run specific test framework
skills.NewSkill("run_test_framework").
    Description("Run tests using a specific framework").
    Domain("testing").
    Keywords("test", "framework", "jest", "pytest", "go", "mocha").
    StringParam("framework", "Test framework (auto-detect if not specified)", false).
    StringParam("path", "Test path or pattern", false).
    ArrayParam("args", "Additional arguments", false).
    BoolParam("verbose", "Verbose output", false).
    BoolParam("coverage", "Include coverage", false)

// configure_test_framework - Configure test framework
skills.NewSkill("configure_test_framework").
    Description("Configure a test framework for the project").
    Domain("testing").
    Keywords("configure", "setup", "framework").
    StringParam("framework", "Framework to configure", true).
    ObjectParam("config", "Configuration options", false)

// list_test_frameworks - List available test frameworks
skills.NewSkill("list_test_frameworks").
    Description("List available test frameworks for detected languages").
    Domain("testing").
    Keywords("list", "frameworks", "available").
    StringParam("path", "Project path", true)
```

**Test Framework Registry (dynamically loaded):**
```go
type TestFrameworkConfig struct {
    Name         string
    Language     string
    RunCommand   string            // Command to run tests
    CoverageCmd  string            // Command for coverage
    ConfigFiles  []string          // Config file names
    OutputFormat string            // Output format (json, tap, etc.)
    ParseFunc    string            // Function name for parsing output
}

// Example configurations (user can extend/override)
var TestFrameworks = map[string]TestFrameworkConfig{
    "go-test": {
        Name: "go test",
        Language: "go",
        RunCommand: "go test -json",
        CoverageCmd: "go test -coverprofile=coverage.out -json",
        ConfigFiles: []string{},
        OutputFormat: "json",
    },
    "pytest": {
        Name: "pytest",
        Language: "python",
        RunCommand: "pytest --tb=short -q",
        CoverageCmd: "pytest --cov --cov-report=json",
        ConfigFiles: []string{"pytest.ini", "pyproject.toml", "setup.cfg"},
        OutputFormat: "pytest",
    },
    "jest": {
        Name: "jest",
        Language: "javascript",
        RunCommand: "jest --json",
        CoverageCmd: "jest --coverage --json",
        ConfigFiles: []string{"jest.config.js", "jest.config.ts", "package.json"},
        OutputFormat: "json",
    },
    "mocha": {
        Name: "mocha",
        Language: "javascript",
        RunCommand: "mocha --reporter json",
        CoverageCmd: "nyc mocha --reporter json",
        ConfigFiles: []string{".mocharc.js", ".mocharc.json"},
        OutputFormat: "json",
    },
    "vitest": {
        Name: "vitest",
        Language: "javascript",
        RunCommand: "vitest run --reporter json",
        CoverageCmd: "vitest run --coverage --reporter json",
        ConfigFiles: []string{"vitest.config.ts", "vite.config.ts"},
        OutputFormat: "json",
    },
    "cargo-test": {
        Name: "cargo test",
        Language: "rust",
        RunCommand: "cargo test -- --format json",
        CoverageCmd: "cargo tarpaulin --out json",
        ConfigFiles: []string{"Cargo.toml"},
        OutputFormat: "json",
    },
    "rspec": {
        Name: "rspec",
        Language: "ruby",
        RunCommand: "rspec --format json",
        CoverageCmd: "rspec --format json",
        ConfigFiles: []string{".rspec", "spec/spec_helper.rb"},
        OutputFormat: "json",
    },
}
```

**Environment Skills (for test execution):**
```go
// ensure_test_env - Ensure test environment exists
skills.NewSkill("ensure_test_env").
    Description("Ensure test environment is properly configured").
    Domain("environment").
    Keywords("environment", "setup", "venv", "dependencies").
    StringParam("path", "Project path", true).
    BoolParam("install_deps", "Install test dependencies", false)

// create_venv - Create virtual environment for tests
skills.NewSkill("create_venv").
    Description("Create a virtual environment for test isolation").
    Domain("environment").
    Keywords("venv", "virtualenv", "isolate").
    StringParam("path", "Environment path", true).
    EnumParam("type", "Environment type", []string{"python-venv", "node-sandbox"}, true)

// install_test_deps - Install test dependencies
skills.NewSkill("install_test_deps").
    Description("Install test dependencies").
    Domain("environment").
    Keywords("install", "dependencies", "packages", "dev").
    StringParam("path", "Project path", true).
    ArrayParam("packages", "Additional packages to install", false)

// list_test_deps - List test dependencies
skills.NewSkill("list_test_deps").
    Description("List installed test dependencies").
    Domain("environment").
    Keywords("list", "dependencies", "installed").
    StringParam("path", "Project path", true)
```

**Read-Only Bash Skills (non-write operations):**
```go
// cat_file - Read file contents
skills.NewSkill("cat_file").
    Description("Display file contents").
    Domain("read").
    Keywords("cat", "read", "show", "display").
    StringParam("path", "File path", true).
    IntParam("head", "Show first N lines", false).
    IntParam("tail", "Show last N lines", false)

// grep_output - Search test output
skills.NewSkill("grep_output").
    Description("Search test output or log files").
    Domain("read").
    Keywords("grep", "search", "find", "pattern").
    StringParam("pattern", "Search pattern", true).
    StringParam("path", "File or log path", true).
    BoolParam("case_insensitive", "Case insensitive", false)

// run_readonly_bash - Run read-only bash commands
skills.NewSkill("run_readonly_bash").
    Description("Run non-destructive bash commands for test analysis").
    Domain("bash").
    Keywords("bash", "shell", "command").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false)

// find_files - Find test files
skills.NewSkill("find_files").
    Description("Find test files using the find command").
    Domain("search").
    Keywords("find", "locate", "files", "test").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)

// Read-only command whitelist for Tester
var TesterReadOnlyCommands = []string{
    `^cat .*`,
    `^head .*`,
    `^tail .*`,
    `^grep .*`,
    `^find .* -type f.*`,
    `^ls .*`,
    `^wc .*`,
    `^diff .*`,
    `^git (log|diff|status|show).*`,
    `^go test -list.*`,
    `^npm test -- --listTests.*`,
    `^pytest --collect-only.*`,
}
```

#### Tester Tools
```go
var TesterTools = []ToolDefinition{
    // Core testing
    {Name: "tester_plan_tests", Skill: "plan_tests"},
    {Name: "tester_run_tests", Skill: "run_tests"},
    {Name: "tester_get_results", Skill: "get_results"},
    // Analysis
    {Name: "tester_analyze_failure", Skill: "analyze_failure"},
    {Name: "tester_coverage", Skill: "coverage_report"},
    {Name: "tester_generate_test", Skill: "generate_test"},
    {Name: "tester_skip_test", Skill: "skip_test"},
    {Name: "tester_benchmark", Skill: "benchmark"},
    // Framework management
    {Name: "tester_run_framework", Skill: "run_test_framework"},
    {Name: "tester_configure_framework", Skill: "configure_test_framework"},
    {Name: "tester_list_frameworks", Skill: "list_test_frameworks"},
    // Environment
    {Name: "tester_ensure_env", Skill: "ensure_test_env"},
    {Name: "tester_create_venv", Skill: "create_venv"},
    {Name: "tester_install_deps", Skill: "install_test_deps"},
    // Read-only bash
    {Name: "tester_cat_file", Skill: "cat_file"},
    {Name: "tester_grep_output", Skill: "grep_output"},
    {Name: "tester_run_readonly_bash", Skill: "run_readonly_bash"},
    {Name: "tester_find_files", Skill: "find_files"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Tester Hooks
- [ ] Pre-plan hook: load existing tests from Librarian
- [ ] Post-plan hook: store test plan in Archivalist
- [ ] Pre-run hook: check for skip conditions
- [ ] Post-run hook: store results in Archivalist
- [ ] Pre-analysis hook: load similar failures from Archivalist
- [ ] Post-corrections hook: format for Architect

#### Memory Management (Local Compaction)

**Model**: OpenAI Codex 5.2

**NOTE**: Tester compacts LOCALLY. It does NOT trigger pipeline handoff (only Engineer can).

**Files to add:**
- `agents/tester/memory.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 85% threshold: checkpoint to Archivalist
- [ ] At 95% threshold: compact locally (NOT trigger handoff)
- [ ] Checkpoint includes: tests created, tests run, pass/fail status, failure descriptions
- [ ] If Engineer triggers handoff, Tester participates in state transfer

```go
// Tester checkpoint summary (sent to Archivalist at 85%)
type TesterCheckpointSummary struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    SessionID         string            `json:"session_id"`
    Timestamp         time.Time         `json:"timestamp"`
    ContextUsage      float64           `json:"context_usage"`
    CheckpointIndex   int               `json:"checkpoint_index"`

    // Test state
    TestsCreated      []TestInfo        `json:"tests_created"`
    TestsRun          int               `json:"tests_run"`
    TestsPassed       int               `json:"tests_passed"`
    TestsFailed       int               `json:"tests_failed"`
    TestsSkipped      int               `json:"tests_skipped"`

    // Failure details
    Failures          []TestFailure     `json:"failures"`
    FlakeyTests       []string          `json:"flakey_tests"`

    // Coverage (if available)
    CoveragePercent   float64           `json:"coverage_percent,omitempty"`
    UncoveredPaths    []string          `json:"uncovered_paths,omitempty"`

    // Loop statistics
    LoopsCompleted    int               `json:"loops_completed"`
    FixesVerified     int               `json:"fixes_verified"`
}

type TestInfo struct {
    Name        string   `json:"name"`
    FilePath    string   `json:"file_path"`
    TestType    string   `json:"test_type"`  // unit, integration, e2e
    ForTask     string   `json:"for_task"`   // task ID this test validates
}

type TestFailure struct {
    TestName    string   `json:"test_name"`
    FilePath    string   `json:"file_path"`
    ErrorMsg    string   `json:"error_msg"`
    StackTrace  string   `json:"stack_trace,omitempty"`
    FixAttempts int      `json:"fix_attempts"`
}

// Tester handoff state (when Engineer triggers handoff)
type TesterHandoffState struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    TestsCreated      []TestInfo        `json:"tests_created"`
    TestResults       *TestRunSummary   `json:"test_results"`
    PendingFailures   []TestFailure     `json:"pending_failures"`
    TestPlanRemaining []string          `json:"test_plan_remaining"`
}

func (t *Tester) checkContextAndManage() error {
    usage := t.getContextUsage()

    if usage >= 0.95 {
        // Compact locally - DO NOT trigger handoff
        return t.compactLocally()
    }

    if usage >= 0.85 {
        // Checkpoint to Archivalist
        return t.submitCheckpoint()
    }

    return nil
}
```

**Tests:**
- [ ] Generate test plan
- [ ] Execute tests
- [ ] Analyze failures
- [ ] Test skip conditions
- [ ] Test checkpoint at 85% context
- [ ] Test local compaction at 95% context
- [ ] Test handoff state bundling when Engineer triggers handoff

---

## Phase 5: Integration

**Goal**: Wire everything together, integration tests.

**Dependencies**: Phases 0-4 complete

### 5.1 Agent Coordinator

Coordinates agent lifecycle and inter-agent communication.

**Files to create:**
- `core/coordinator/coordinator.go`
- `core/coordinator/lifecycle.go`
- `core/coordinator/health.go`
- `core/coordinator/coordinator_test.go`

**Acceptance Criteria:**
- [ ] Start all agents in correct order
- [ ] Register all agents with Guide
- [ ] Handle agent failures with graceful degradation
- [ ] Shutdown all agents in correct order
- [ ] Health monitoring across all agents
- [ ] Metrics collection from all agents
- [ ] Session-scoped agent instances
- [ ] Shared agent instances (Librarian, Academic, Archivalist)

### 5.2 Full Workflow Integration Tests

**Files to create:**
- `tests/integration/workflow_test.go`
- `tests/integration/multi_session_test.go`
- `tests/integration/session_isolation_test.go`
- `tests/integration/session_switching_test.go`
- `tests/integration/failure_test.go`
- `tests/integration/quality_loop_test.go`
- `tests/integration/skill_loading_test.go`
- `tests/integration/pipeline_test.go` (NEW)
- `tests/integration/two_level_qa_test.go` (NEW)

**Acceptance Criteria:**

#### Basic Workflow
- [ ] Test: User request → Architect → DAG → Pipelines → Complete
- [ ] Test: Multiple sessions executing concurrently
- [ ] Test: Session isolation (no context pollution)
- [ ] Test: Session switching with state preservation
- [ ] Test: Engineer clarification loop
- [ ] Test: User interruption during execution
- [ ] Test: Agent failure recovery
- [ ] Test: Session cleanup on completion
- [ ] Test: Skill progressive loading
- [ ] Test: Cross-session Archivalist queries

#### Pipeline-Specific Tests (`pipeline_test.go`)
- [ ] Test: Pipeline creation with Engineer + Inspector + Tester
- [ ] Test: Pipeline-internal Inspector feedback loop
- [ ] Test: Pipeline-internal Tester feedback loop
- [ ] Test: Pipeline max loops enforcement
- [ ] Test: /task command routing through Guide to Pipeline
- [ ] Test: User override (ignore_inspector) in Pipeline
- [ ] Test: User override (ignore_tester) in Pipeline
- [ ] Test: Concurrent Pipelines within same DAG
- [ ] Test: Pipeline cancellation mid-execution
- [ ] Test: Pipeline failure propagation to Orchestrator

#### Two-Level QA Tests (`two_level_qa_test.go`)
- [ ] Test: Pipeline-internal Inspector pass → Pipeline-internal Tester pass → Pipeline complete
- [ ] Test: Pipeline-internal Inspector fail → direct feedback → Engineer fixes → re-validate
- [ ] Test: Pipeline-internal Tester fail → direct feedback → Engineer fixes → re-test
- [ ] Test: All Pipelines complete → Session-wide Inspector triggers
- [ ] Test: Session-wide Inspector fail → Architect creates FIX DAG → new Pipelines
- [ ] Test: Session-wide Tester fail → Architect creates FIX DAG → new Pipelines
- [ ] Test: Full flow: Pipelines → Session Inspector → Session Tester → Complete

### 5.3 Performance Benchmarks

**Files to create:**
- `tests/benchmark/session_bench_test.go`
- `tests/benchmark/dag_bench_test.go`
- `tests/benchmark/throughput_bench_test.go`
- `tests/benchmark/skill_loading_bench_test.go`

**Acceptance Criteria:**
- [ ] Benchmark: 10 concurrent sessions with 50 Engineers each
- [ ] Benchmark: DAG execution with 100 nodes
- [ ] Benchmark: Message throughput through Guide
- [ ] Benchmark: Worker pool fairness under load
- [ ] Benchmark: Skill loading latency
- [ ] Benchmark: Session switch latency
- [ ] Target: < 10ms routing latency (cache hit)
- [ ] Target: < 100ms routing latency (LLM classification)
- [ ] Target: < 50ms skill loading latency
- [ ] Target: < 100ms session switch latency
- [ ] Target: Linear scaling to 100 sessions

---

## Execution Workflow

```
Phase 0 (Foundation)              Phase 1 (Knowledge)      Phase 2 (Execution)           Phase 3         Phase 4 (Quality)      Phase 5
┌─────────────────────────────┐   ┌─────────────────┐      ┌──────────────────────┐      ┌───────────┐   ┌────────────────┐      ┌──────────────────┐
│ 0.1 Session Manager         │   │ 1.1 Librarian   │      │ 2.1 Engineer         │      │ 3.1       │   │ 4.1 Inspector  │      │ 5.1 Coordinator  │
│ 0.2 DAG Engine              │   │ 1.2 Academic    │      │ 2.2 Orchestrator     │      │ Architect │   │ 4.2 Tester     │      │ 5.2 Integration  │
│ 0.3 Worker Pool Enhancements│──▶│ 1.3 Tool Disc.  │─────▶│ 2.3 Pipeline Infra   │─────▶│           │──▶│                │─────▶│ 5.3 Benchmarks   │
│ 0.4 Guide Enhancements      │   │                 │      │     (E + I + T)      │      │           │   │                │      │                  │
│ 0.5 Archivalist Enhancements│   │                 │      │                      │      │           │   │                │      │                  │
│ 0.6 Bus Enhancements        │   │                 │      │                      │      │           │   │                │      │                  │
└─────────────────────────────┘   └─────────────────┘      └──────────────────────┘      └───────────┘   └────────────────┘      └──────────────────┘
```

### Parallelization Summary

| Phase | Items | Parallel Execution |
|-------|-------|-------------------|
| 0 | 0.1, 0.2, 0.3, 0.4, 0.5, 0.6 | All six in parallel |
| 1 | 1.1, 1.2, 1.3 | 1.1 and 1.2 in parallel; 1.3 after 1.1+1.2 |
| 2 | 2.1, 2.2, 2.3 | 2.1 and 2.2 in parallel; 2.3 after 2.1+2.2 |
| 3 | 3.1 | Sequential (after Phase 2) |
| 4 | 4.1, 4.2 | Both in parallel (after Phase 3) |
| 5 | 5.1, 5.2, 5.3 | Sequential (after Phase 4) |

**Note**: Phase 2.3 (Pipeline Infrastructure) depends on 2.1 (Engineer) and 2.2 (Orchestrator) as it combines them with Inspector and Tester instances into isolated execution contexts.

### Critical Path

```
0.1 (Session) → 0.4 (Guide) → 2.2 (Orchestrator) → 2.3 (Pipeline) → 3.1 (Architect) → 5.1 (Coordinator)
```

---

## Session Architecture

### Session Isolation Model

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                              SESSION ISOLATION MODEL                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  SESSION-LOCAL (Isolated per session):                                              │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • Guide routing state (pending requests, session route cache)                 │ │
│  │  • Agent instances (Architect, Orchestrator, Engineers, Inspector, Tester)     │ │
│  │  • Workflow state (current DAG, phase, progress)                               │ │
│  │  • Session context (metadata, branch, user preferences)                        │ │
│  │  • Bus subscriptions (session-scoped topics)                                   │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  SESSION-SHARED (Read-only access across sessions):                                 │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • Archivalist historical DB (query across sessions, write to own session)     │ │
│  │  • Librarian index (shared codebase, read-only)                                │ │
│  │  • Academic sources (shared research, read-only)                               │ │
│  │  • Global route cache (common routes, read-only fallback)                      │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  CONTEXT POLLUTION PREVENTION:                                                      │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • All writes tagged with session ID                                           │ │
│  │  • Default queries filtered by session ID                                      │ │
│  │  • Cross-session queries explicit and read-only                                │ │
│  │  • No shared mutable state between sessions                                    │ │
│  │  • Agent instances never shared (except Librarian, Academic, Archivalist)      │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Session Lifecycle

```
CREATE          ACTIVE              PAUSE              RESUME            COMPLETE
   │               │                   │                  │                  │
   ▼               ▼                   ▼                  ▼                  ▼
┌──────┐      ┌────────┐          ┌────────┐        ┌────────┐        ┌──────────┐
│Create│─────▶│ Active │─────────▶│ Paused │───────▶│ Active │───────▶│ Complete │
│State │      │Working │          │Saved   │        │Restored│        │ Archived │
└──────┘      └────────┘          └────────┘        └────────┘        └──────────┘
   │               │                   │                  │                  │
   │               │                   │                  │                  │
   │          ┌────┴────┐              │                  │                  │
   │          ▼         ▼              │                  │                  │
   │     Execute    Interrupt          │                  │                  │
   │     Workflow   (User)             │                  │                  │
   │          │         │              │                  │                  │
   │          └────┬────┘              │                  │                  │
   │               │                   │                  │                  │
   │               └───────────────────┘                  │                  │
   │                                                      │                  │
   └──────────────────────────────────────────────────────┘                  │
                                                                             │
                      Archivalist stores all session history ◀───────────────┘
```

---

## Common Agent Skills

All agents MUST implement the following common skills for inter-agent routing and communication.

### Routing Skills (All Agents)

Every agent must support routing commands via `@to:<agent>` and `@from:<agent>` patterns as specified in the Guide routing protocol.

```go
// Common routing skills - ALL agents must implement these
var CommonRoutingSkills = []skills.Skill{
    // route_to - Route message to another agent
    skills.NewSkill("route_to").
        Description("Route a message to another agent via Guide").
        Domain("routing").
        Keywords("@to", "route", "send", "forward").
        StringParam("target", "Target agent (e.g., 'architect', 'librarian')", true).
        StringParam("message", "Message content to route", true).
        ObjectParam("context", "Additional context to include", false),

    // route_from - Handle message routed from another agent
    skills.NewSkill("route_from").
        Description("Process a message routed from another agent").
        Domain("routing").
        Keywords("@from", "receive", "handle").
        StringParam("source", "Source agent that sent the message", true).
        StringParam("message", "Message content received", true).
        StringParam("correlation_id", "Correlation ID for response matching", true),

    // reply_to - Reply to a routed message
    skills.NewSkill("reply_to").
        Description("Reply to a previously received routed message").
        Domain("routing").
        Keywords("reply", "respond", "answer").
        StringParam("correlation_id", "Correlation ID of original message", true).
        StringParam("response", "Response content", true).
        BoolParam("complete", "Mark conversation as complete", false),

    // broadcast - Broadcast to multiple agents
    skills.NewSkill("broadcast").
        Description("Broadcast a message to multiple agents").
        Domain("routing").
        Keywords("broadcast", "notify", "all").
        ArrayParam("targets", "List of target agents", true).
        StringParam("message", "Message to broadcast", true),
}

// Routing pattern syntax (parsed by Guide)
// @to:architect "please plan this task"
// @to:librarian "find all files matching *.go"
// @from:engineer "task completed successfully"
// @broadcast:inspector,tester "validation complete"
```

### Slash Command Skills (Academic, Architect, Librarian, Inspector, Tester)

The Academic, Architect, Librarian, Inspector, and Tester agents should support slash commands for direct invocation of specific operations.

```go
// Slash command skill - implemented by Academic, Architect, Inspector, Tester
var SlashCommandSkill = skills.NewSkill("run_command").
    Description("Execute a slash command").
    Domain("commands").
    Keywords("/", "slash", "command").
    StringParam("command", "Slash command to execute (e.g., /research, /plan)", true).
    ArrayParam("args", "Command arguments", false).
    ObjectParam("options", "Command options", false)

// Agent-specific slash commands

// Academic slash commands
var AcademicSlashCommands = map[string]string{
    "/research":  "Research a topic and provide comprehensive analysis",
    "/compare":   "Compare approaches or technologies",
    "/ingest":    "Ingest a new source (GitHub repo, article, etc.)",
    "/cite":      "Get citations for previous research",
    "/sources":   "List available sources",
}

// Architect slash commands
var ArchitectSlashCommands = map[string]string{
    "/plan":      "Create an implementation plan",
    "/status":    "Get current execution status",
    "/approve":   "Approve the current plan",
    "/modify":    "Modify the current plan",
    "/interrupt": "Interrupt current execution",
    "/clarify":   "Request clarification",
    "/schedule":  "Schedule work with orchestrator",
    "/workflow":  "View or adjust current workflow",
}

// Librarian slash commands
var LibrarianSlashCommands = map[string]string{
    "/find":      "Find a symbol, file, or pattern in the codebase",
    "/search":    "Search code content with regex",
    "/file":      "Read or display a file",
    "/imports":   "Analyze import relationships",
    "/usages":    "Find usages of a symbol",
    "/structure": "Get structure of a file or package",
    "/scan":      "Scan a remote repository",
    "/tooling":   "Identify languages and dev tools",
}

// Inspector slash commands
var InspectorSlashCommands = map[string]string{
    "/validate":  "Validate implementation",
    "/lint":      "Run linter on code",
    "/format":    "Check code formatting",
    "/typecheck": "Run type checker",
    "/issues":    "List current issues",
    "/override":  "Override a validation issue",
    "/analyze":   "Deep analysis of code",
}

// Tester slash commands
var TesterSlashCommands = map[string]string{
    "/test":      "Run tests",
    "/coverage":  "Generate coverage report",
    "/plan":      "Create test plan",
    "/analyze":   "Analyze test failure",
    "/skip":      "Skip a test",
    "/benchmark": "Run benchmarks",
    "/watch":     "Watch mode for tests",
}
```

---

## Progressive Skill Disclosure

### Skill Loading Strategy

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         PROGRESSIVE SKILL DISCLOSURE                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TIER 1: CORE SKILLS (Always loaded, ~5-10 per agent)                               │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  Essential skills for basic operation                                          │ │
│  │  Examples: read_file, write_file, query, store, plan, validate                 │ │
│  │  Token cost: ~500-1000 tokens per agent                                        │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  TIER 2: DOMAIN SKILLS (Loaded when domain activated)                               │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  Loaded when: User query matches domain keywords                               │ │
│  │  Examples: analyze_imports (when discussing dependencies)                      │ │
│  │           compare_approaches (when asking "X vs Y")                            │ │
│  │  Token cost: ~200-500 tokens per domain                                        │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  TIER 3: SPECIALIZED SKILLS (Loaded on explicit request)                            │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  Loaded when: Explicit tool call or user command                               │ │
│  │  Examples: benchmark, ingest_github, generate_paper                            │ │
│  │  Token cost: ~100-300 tokens each                                              │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  SKILL UNLOADING:                                                                   │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • Unload unused skills after N turns without use                              │ │
│  │  • Unload when approaching token budget                                        │ │
│  │  • Never unload Tier 1 core skills                                             │ │
│  │  • LRU eviction for Tier 2/3 skills                                            │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Concurrency Design Principles

### 1. All Messages Through Guide
```go
// CORRECT: Route through Guide
guide.PublishRequest(&RouteRequest{
    SessionID:     session.ID(),
    Input:         "query codebase for middleware",
    TargetAgentID: "librarian",
})

// WRONG: Direct agent call
librarian.Query("middleware") // Never do this between agents
```

### 2. Session-Scoped State
```go
// All state operations include session context
store.Insert(entry, session.ID())
cache.Get(key, session.ID())
pending.Add(request, session.ID())
```

### 3. Shared Read, Isolated Write
```go
// Read from any session (explicit)
archivalist.QueryCrossSession(query)

// Write only to own session (enforced)
archivalist.Store(entry) // Automatically tagged with session ID
```

### 4. Bounded Concurrency
```go
type SessionConfig struct {
    MaxConcurrentTasks int // e.g., 50
    MaxEngineers       int // e.g., 20
}

type SystemConfig struct {
    MaxSessions        int // e.g., 100
    MaxTotalEngineers  int // e.g., 500
}
```

---

## Testing Strategy

### Unit Tests
Each component has `*_test.go` with:
- Constructor tests
- Method tests
- Error handling tests
- Concurrency tests (race detector)
- Skill loading tests

### Integration Tests
```
tests/integration/
├── workflow_test.go          # Full workflow end-to-end
├── multi_session_test.go     # Concurrent sessions
├── session_isolation_test.go # No context pollution
├── session_switching_test.go # State preservation
├── failure_test.go           # Failure recovery
├── quality_loop_test.go      # Inspector + Tester loop
└── skill_loading_test.go     # Progressive disclosure
```

### Running Tests
```bash
go test ./... -race
go test ./tests/integration/... -v -timeout 10m
go test ./tests/benchmark/... -bench=. -benchmem
```

---

## Acceptance Verification

Before marking a phase complete:

1. **Build passes**: `go build ./...`
2. **Vet passes**: `go vet ./...`
3. **Tests pass**: `go test ./... -race`
4. **Coverage > 70%**: `go test ./... -cover`
5. **No race conditions**: Tests pass with `-race` flag
6. **Integration works**: Component integrates with existing system
7. **Skills defined**: All skills documented and registered
8. **Hooks implemented**: All lifecycle hooks in place
9. **Session-aware**: All state operations include session context

---

## Notes

- All agents must implement bus integration (Start/Stop/IsRunning)
- All agents must provide `AgentRoutingInfo` for Guide registration
- All agents must define core skills (always loaded) and extended skills (on demand)
- All state operations must be session-aware
- Engineers are invisible to users - all clarifications route through Architect
- Guide is the only message router - no direct agent communication
- Archivalist stores all workflow history for future reference
- Cross-session queries are read-only
- Use existing patterns from Guide and Archivalist as templates

---

## Phase 6: VectorGraphDB Implementation

**Goal**: Build a unified embedded database combining vector similarity search with graph-based structural queries across all three domains (Code, History, Academic).

**Dependencies**: Phase 0 (Session Manager), Phase 1 (Knowledge Agents - particularly Archivalist and Librarian foundations)

**Parallelization**: Items 6.1-6.4 can execute in parallel. Items 6.5-6.11 (mitigations) can execute in parallel after 6.1-6.4. Item 6.12 requires all prior items.

---

### 6.1 SQLite Schema & Core Types

Creates the database schema and type definitions for the VectorGraphDB.

**Files to create:**
- `core/vectorgraphdb/schema.sql`
- `core/vectorgraphdb/types.go`
- `core/vectorgraphdb/constants.go`
- `core/vectorgraphdb/db.go`
- `core/vectorgraphdb/db_test.go`

**Acceptance Criteria:**

#### Schema Creation
- [ ] Create `nodes` table with: id, domain, node_type, content_hash, metadata (JSON), created_at, updated_at, accessed_at
- [ ] Create `edges` table with: id, from_node_id, to_node_id, edge_type, weight, metadata (JSON), created_at
- [ ] Create `vectors` table with: id, node_id, embedding (BLOB - 768 float32s packed), magnitude, model_version
- [ ] Create `provenance` table with: id, node_id, source_type, source_id, confidence, verified_at, verifier
- [ ] Create `conflicts` table with: id, node_a_id, node_b_id, conflict_type, detected_at, resolution, resolved_at
- [ ] Create `hnsw_graph` table with: id, node_id, layer, neighbors (JSON array)
- [ ] Create `hnsw_metadata` table with: id, entry_point, max_level, M, ef_construct
- [ ] Create all required indexes for efficient querying
- [ ] Enable WAL mode for concurrent read/write
- [ ] Create migration support for schema versioning

#### Type Definitions
- [ ] Define `Domain` type with constants: `DomainCode`, `DomainHistory`, `DomainAcademic`
- [ ] Define `NodeType` type with all node types per domain:
  - Code: `NodeTypeFile`, `NodeTypeFunction`, `NodeTypeType`, `NodeTypePackage`, `NodeTypeImport`
  - History: `NodeTypeSession`, `NodeTypeDecision`, `NodeTypeFailure`, `NodeTypePattern`, `NodeTypeWorkflow`
  - Academic: `NodeTypeRepo`, `NodeTypeDoc`, `NodeTypeArticle`, `NodeTypeConcept`, `NodeTypeBestPractice`
- [ ] Define `EdgeType` type with all edge types:
  - Structural: `EdgeTypeCalls`, `EdgeTypeImports`, `EdgeTypeDefines`, `EdgeTypeImplements`, `EdgeTypeContains`
  - Temporal: `EdgeTypeFollows`, `EdgeTypeCauses`, `EdgeTypeResolves`
  - Cross-domain: `EdgeTypeReferences`, `EdgeTypeAppliesTo`, `EdgeTypeDocuments`, `EdgeTypeModified`
- [ ] Define `GraphNode` struct with all fields
- [ ] Define `GraphEdge` struct with all fields
- [ ] Define `VectorData` struct for embedding storage
- [ ] Define `Provenance` struct for source tracking
- [ ] Define `Conflict` struct for contradiction detection

#### Database Operations
- [ ] Implement `Open(path string) (*VectorGraphDB, error)`
- [ ] Implement `Close() error`
- [ ] Implement `Migrate() error` for schema migrations
- [ ] Implement `Vacuum() error` for database compaction
- [ ] Implement `Stats() (*DBStats, error)` for metrics

**Tests:**
- [ ] Schema creation succeeds on fresh database
- [ ] Migrations apply correctly
- [ ] Concurrent read/write operations succeed (WAL mode)
- [ ] Foreign key constraints enforced
- [ ] Index performance verified with EXPLAIN QUERY PLAN

```go
// Required types
type Domain string
const (
    DomainCode     Domain = "code"
    DomainHistory  Domain = "history"
    DomainAcademic Domain = "academic"
)

type NodeType string
// ... (full list per domain)

type EdgeType string
// ... (full list)

type GraphNode struct {
    ID          string            `json:"id"`
    Domain      Domain            `json:"domain"`
    NodeType    NodeType          `json:"node_type"`
    ContentHash string            `json:"content_hash"`
    Metadata    map[string]any    `json:"metadata"`
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
    AccessedAt  time.Time         `json:"accessed_at"`
}
```

---

### 6.2 HNSW Index Implementation

Implements Hierarchical Navigable Small World index for O(log n) approximate nearest neighbor search in pure Go.

**Files to create:**
- `core/vectorgraphdb/hnsw/index.go`
- `core/vectorgraphdb/hnsw/layer.go`
- `core/vectorgraphdb/hnsw/distance.go`
- `core/vectorgraphdb/hnsw/persistence.go`
- `core/vectorgraphdb/hnsw/hnsw_test.go`

**Acceptance Criteria:**

#### Index Structure
- [ ] Implement multi-layer graph structure with exponential decay
- [ ] Store vectors with magnitudes for fast cosine similarity
- [ ] Track domain and node type per vector for filtered search
- [ ] Thread-safe with RWMutex for concurrent operations
- [ ] Configurable parameters: M (connections per node), efConstruction, efSearch

#### Insert Operation
- [ ] Random level assignment using exponential distribution: `floor(-log(rand) * levelMult)`
- [ ] Search for neighbors at each layer during insertion
- [ ] Connect to M best neighbors per layer
- [ ] Update entry point if new node has higher level
- [ ] Handle insertions with existing ID (update vector)

#### Search Operation
- [ ] Start from entry point at top layer
- [ ] Greedy search descending through layers
- [ ] Expand search at layer 0 with efSearch candidates
- [ ] Return top-k results sorted by similarity
- [ ] Support filtered search by domain and node type
- [ ] Support multi-domain search with result merging

#### Distance Functions
- [ ] Implement cosine similarity: `dot(a,b) / (mag(a) * mag(b))`
- [ ] Pre-compute magnitudes on insert for performance
- [ ] SIMD-optimized dot product (optional, fallback to scalar)

#### Persistence
- [ ] Save index to SQLite tables (hnsw_graph, hnsw_metadata)
- [ ] Load index from SQLite on startup
- [ ] Incremental save support (only changed nodes)
- [ ] Atomic save with transaction

#### Performance Requirements
- [ ] Insert: < 1ms for index with < 100K nodes
- [ ] Search: < 5ms for k=10 on index with < 100K nodes
- [ ] Memory: ~100 bytes per node overhead (connections + metadata)

**Tests:**
- [ ] Insert 10K random vectors, verify all retrievable
- [ ] Search returns correct nearest neighbors (vs brute force)
- [ ] Filtered search respects domain constraints
- [ ] Concurrent insert/search operations (race detector)
- [ ] Persistence: save, reload, verify identical results
- [ ] Performance benchmarks for insert and search

```go
// Required interface
type HNSWIndex struct {
    mu          sync.RWMutex
    layers      []map[string][]string  // layer -> nodeID -> neighbor IDs
    vectors     map[string][]float32   // nodeID -> embedding
    magnitudes  map[string]float64     // nodeID -> pre-computed magnitude
    domains     map[string]Domain      // nodeID -> domain
    nodeTypes   map[string]NodeType    // nodeID -> node type
    M           int                    // max connections per node per layer
    efConstruct int                    // construction search width
    efSearch    int                    // search width
    levelMult   float64                // level generation multiplier
    maxLevel    int                    // current max level
    entryPoint  string                 // entry node ID
}

func (h *HNSWIndex) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error
func (h *HNSWIndex) Search(query []float32, k int, filter *SearchFilter) []SearchResult
func (h *HNSWIndex) Delete(id string) error
func (h *HNSWIndex) Save(db *sql.DB) error
func (h *HNSWIndex) Load(db *sql.DB) error
```

---

### 6.3 Node & Edge Management

Implements CRUD operations for nodes and edges with cross-domain support.

**Files to create:**
- `core/vectorgraphdb/nodes.go`
- `core/vectorgraphdb/edges.go`
- `core/vectorgraphdb/batch.go`
- `core/vectorgraphdb/nodes_test.go`
- `core/vectorgraphdb/edges_test.go`

**Acceptance Criteria:**

#### Node Operations
- [ ] `InsertNode(node *GraphNode, embedding []float32) error`
- [ ] `GetNode(id string) (*GraphNode, error)`
- [ ] `UpdateNode(node *GraphNode) error`
- [ ] `DeleteNode(id string) error` (cascade to edges, vectors, provenance)
- [ ] `GetNodesByType(domain Domain, nodeType NodeType, limit int) ([]*GraphNode, error)`
- [ ] `GetNodesByContentHash(hash string) ([]*GraphNode, error)`
- [ ] `TouchNode(id string) error` (update accessed_at)
- [ ] Automatic content hash computation on insert/update
- [ ] Automatic embedding insertion into HNSW index

#### Edge Operations
- [ ] `InsertEdge(edge *GraphEdge) error`
- [ ] `GetEdge(id string) (*GraphEdge, error)`
- [ ] `GetEdgesBetween(fromID, toID string) ([]*GraphEdge, error)`
- [ ] `GetOutgoingEdges(nodeID string, edgeTypes ...EdgeType) ([]*GraphEdge, error)`
- [ ] `GetIncomingEdges(nodeID string, edgeTypes ...EdgeType) ([]*GraphEdge, error)`
- [ ] `DeleteEdge(id string) error`
- [ ] `DeleteEdgesBetween(fromID, toID string) error`
- [ ] Validate edge endpoints exist before insert
- [ ] Cross-domain edge validation (certain edge types allowed between domains)

#### Batch Operations
- [ ] `BatchInsertNodes(nodes []*GraphNode, embeddings [][]float32) error`
- [ ] `BatchInsertEdges(edges []*GraphEdge) error`
- [ ] `BatchDeleteNodes(ids []string) error`
- [ ] Transaction support for atomic batch operations
- [ ] Progress callback for large batch operations

#### Cross-Domain Edge Rules
- [ ] `EdgeTypeReferences`: Code ↔ Academic (references doc to code)
- [ ] `EdgeTypeAppliesTo`: Academic → Code (best practice applies to file)
- [ ] `EdgeTypeDocuments`: Academic → Code (article documents pattern)
- [ ] `EdgeTypeModified`: History → Code (session modified file)
- [ ] `EdgeTypeLedTo`: History → History (decision led to outcome)
- [ ] `EdgeTypeUsedPattern`: History → Academic (session used pattern)
- [ ] Reject invalid cross-domain edges

**Tests:**
- [ ] Insert and retrieve nodes across all domains
- [ ] Insert and retrieve edges across all types
- [ ] Delete node cascades to related data
- [ ] Batch insert 10K nodes in single transaction
- [ ] Cross-domain edge validation enforced
- [ ] Invalid edges rejected with clear error

---

### 6.4 Vector Search & Graph Traversal

Implements combined vector similarity and graph traversal queries.

**Files to create:**
- `core/vectorgraphdb/search.go`
- `core/vectorgraphdb/traversal.go`
- `core/vectorgraphdb/query.go`
- `core/vectorgraphdb/search_test.go`
- `core/vectorgraphdb/traversal_test.go`

**Acceptance Criteria:**

#### Vector Search
- [ ] `VectorSearch(query []float32, k int, filter *SearchFilter) ([]*SearchResult, error)`
- [ ] `VectorSearchByText(text string, k int, filter *SearchFilter) ([]*SearchResult, error)` (uses embedder)
- [ ] `VectorSearchMultiDomain(query []float32, k int, domains []Domain) ([]*SearchResult, error)`
- [ ] Filter by domain, node type, min similarity threshold
- [ ] Return results with similarity score, node, and metadata
- [ ] Support hybrid scoring (vector similarity + graph distance)

#### Graph Traversal
- [ ] `GetNeighbors(nodeID string, depth int, edgeTypes ...EdgeType) ([]*GraphNode, error)`
- [ ] `ShortestPath(fromID, toID string, edgeTypes ...EdgeType) ([]*GraphNode, error)`
- [ ] `GetConnectedComponent(nodeID string, maxNodes int) ([]*GraphNode, error)`
- [ ] `FindPath(fromID, toID string, constraints *PathConstraints) ([]*PathResult, error)`
- [ ] BFS and DFS traversal options
- [ ] Cycle detection and prevention

#### Combined Queries
- [ ] `HybridQuery(text string, constraints *QueryConstraints) ([]*QueryResult, error)`
- [ ] First: vector search to find semantic matches
- [ ] Then: graph expansion to find related context
- [ ] Score combination: `finalScore = α * vectorSim + (1-α) * graphScore`
- [ ] Configurable alpha parameter for balance

#### Cross-Domain Queries
- [ ] Find code files → related academic docs → history of changes
- [ ] Find failure → resolution → similar code patterns
- [ ] Find concept → implementing code → usage examples
- [ ] Query result includes domain path for provenance

#### Query Optimization
- [ ] Use domain hints to reduce search space
- [ ] Cache frequent traversal paths
- [ ] Early termination for low-relevance branches
- [ ] Parallel traversal for independent subgraphs

**Tests:**
- [ ] Vector search returns semantically similar nodes
- [ ] Graph traversal respects depth limits
- [ ] Combined query returns relevant cross-domain results
- [ ] Performance: hybrid query < 50ms for 100K node graph
- [ ] Cycle detection prevents infinite loops

```go
// Query types
type SearchFilter struct {
    Domains       []Domain
    NodeTypes     []NodeType
    MinSimilarity float64
    MaxResults    int
    IncludeEdges  bool
}

type QueryConstraints struct {
    Text          string
    Domains       []Domain
    GraphDepth    int
    EdgeTypes     []EdgeType
    Alpha         float64  // vector vs graph balance
    MinScore      float64
    MaxResults    int
}

type QueryResult struct {
    Node         *GraphNode
    VectorScore  float64
    GraphScore   float64
    FinalScore   float64
    Path         []*GraphNode  // path from query origin
    Edges        []*GraphEdge  // edges traversed
}
```

---

### 6.5 Mitigation 1: Hallucination Firewall

Prevents storage of unverified LLM outputs to avoid contaminating the knowledge base.

**Files to create:**
- `core/vectorgraphdb/mitigations/hallucination_firewall.go`
- `core/vectorgraphdb/mitigations/verification.go`
- `core/vectorgraphdb/mitigations/hallucination_firewall_test.go`

**Acceptance Criteria:**

#### Firewall Implementation
- [ ] Intercept all LLM-sourced data before storage
- [ ] Verify existence of referenced files, functions, patterns
- [ ] Cross-reference claims against existing verified data
- [ ] Assign confidence scores based on verification depth
- [ ] Block storage for unverifiable claims
- [ ] Queue ambiguous claims for human review

#### Verification Strategies
- [ ] `VerifyFileExists(path string) (bool, error)` - check file system
- [ ] `VerifyFunctionExists(file, funcName string) (bool, error)` - parse AST
- [ ] `VerifyPatternExists(pattern string) (bool, float64, error)` - search codebase
- [ ] `CrossReferenceNode(node *GraphNode) (float64, []string, error)` - check against DB
- [ ] `VerifyEdgeValid(edge *GraphEdge) (bool, error)` - validate relationship

#### Confidence Scoring
- [ ] 1.0: Directly verified (file exists, AST confirms)
- [ ] 0.8-0.99: Cross-referenced with multiple sources
- [ ] 0.6-0.79: Partial verification (some claims verified)
- [ ] 0.4-0.59: Low verification (inference from patterns)
- [ ] 0.0-0.39: Unverifiable (blocked)
- [ ] Configurable threshold for storage (default: 0.6)

#### Review Queue
- [ ] `QueueForReview(node *GraphNode, reason string) error`
- [ ] `GetReviewQueue(limit int) ([]*ReviewItem, error)`
- [ ] `ApproveReview(id string, reviewer string) error`
- [ ] `RejectReview(id string, reason string) error`
- [ ] Automatic expiration of stale review items

#### Metrics
- [ ] Track verified vs rejected claims
- [ ] Track verification failure reasons
- [ ] Track review queue depth and resolution time

**Tests:**
- [ ] Valid file references pass verification
- [ ] Invalid file references blocked
- [ ] Cross-referenced data gets high confidence
- [ ] Ambiguous data queued for review
- [ ] Review workflow completes correctly

```go
type HallucinationFirewall struct {
    db                *VectorGraphDB
    librarian         LibrarianClient  // for file/code verification
    minConfidence     float64
    reviewQueue       chan *ReviewItem
    verificationCache *VerificationCache
}

func (f *HallucinationFirewall) Verify(ctx context.Context, node *GraphNode, source SourceType) (*VerificationResult, error)
func (f *HallucinationFirewall) Store(ctx context.Context, node *GraphNode, verification *VerificationResult) error

type VerificationResult struct {
    Verified     bool
    Confidence   float64
    Checks       []VerificationCheck
    FailedChecks []string
    ShouldQueue  bool
    QueueReason  string
}
```

---

### 6.6 Mitigation 2: Freshness Tracking & Decay

Tracks data freshness and applies temporal decay to prevent stale data from polluting results.

**Files to create:**
- `core/vectorgraphdb/mitigations/freshness.go`
- `core/vectorgraphdb/mitigations/decay.go`
- `core/vectorgraphdb/mitigations/freshness_test.go`

**Acceptance Criteria:**

#### Freshness Tracking
- [ ] Track last_verified timestamp for each node
- [ ] Track source_modified timestamp (e.g., file mtime)
- [ ] Detect stale nodes: node.updated_at < source.modified_at
- [ ] Mark nodes requiring re-verification
- [ ] Automatic staleness detection on access

#### Decay Functions
- [ ] Exponential decay: `score * exp(-λ * age_hours)`
- [ ] Linear decay with cliff: `max(0, score - age_days * decay_rate)`
- [ ] Domain-specific decay rates:
  - Code: λ = 0.01 (slow decay, code changes less frequently)
  - History: λ = 0.05 (moderate decay)
  - Academic: λ = 0.001 (very slow decay, docs are stable)
- [ ] Apply decay to search result scores

#### Freshness Score Calculation
- [ ] `ComputeFreshnessScore(node *GraphNode) float64`
- [ ] Consider: time since update, time since access, source freshness
- [ ] Weight formula: `freshness = base * decay(age) * accessBoost(recency)`
- [ ] Configurable weights per domain

#### Staleness Resolution
- [ ] `FindStaleNodes(domain Domain, threshold time.Duration) ([]*GraphNode, error)`
- [ ] `MarkStale(nodeID string) error`
- [ ] `RefreshNode(nodeID string) error` (re-verify and update)
- [ ] Batch refresh for bulk staleness resolution
- [ ] Background staleness scanner (configurable interval)

#### Integration with Search
- [ ] Apply decay before returning search results
- [ ] Filter out nodes below freshness threshold
- [ ] Return freshness score in result metadata
- [ ] Option to include stale results with warning flag

**Tests:**
- [ ] Fresh nodes have freshness score near 1.0
- [ ] Old nodes have decayed freshness score
- [ ] Stale node detection works correctly
- [ ] Search results respect freshness thresholds
- [ ] Background scanner identifies stale nodes

```go
type FreshnessTracker struct {
    db          *VectorGraphDB
    decayRates  map[Domain]float64
    scanner     *StalenessScannerConfig
    refreshChan chan string
}

func (f *FreshnessTracker) GetFreshness(nodeID string) (*FreshnessInfo, error)
func (f *FreshnessTracker) ApplyDecay(results []*SearchResult) []*SearchResult
func (f *FreshnessTracker) MarkStale(nodeID string) error
func (f *FreshnessTracker) RefreshNode(ctx context.Context, nodeID string) error
func (f *FreshnessTracker) StartScanner(ctx context.Context) error

type FreshnessInfo struct {
    NodeID          string
    LastVerified    time.Time
    SourceModified  time.Time
    IsStale         bool
    FreshnessScore  float64
    DecayRate       float64
    NextScanAt      time.Time
}
```

---

### 6.7 Mitigation 3: Source Attribution & Provenance

Tracks the origin and verification chain for all stored information.

**Files to create:**
- `core/vectorgraphdb/mitigations/provenance.go`
- `core/vectorgraphdb/mitigations/attribution.go`
- `core/vectorgraphdb/mitigations/provenance_test.go`

**Acceptance Criteria:**

#### Provenance Record
- [ ] Store source type: `verified_code`, `llm_inference`, `user_input`, `academic_source`, `cross_reference`
- [ ] Store source ID: file path, document URL, session ID, etc.
- [ ] Store confidence score from verification
- [ ] Store verifier: human, automated, cross-reference
- [ ] Store verification chain (what verified what)

#### Source Types Hierarchy
- [ ] `SourceTypeCode` (1.0 base trust) - directly from codebase
- [ ] `SourceTypeUser` (0.95 base trust) - explicit user input
- [ ] `SourceTypeAcademic` (0.85 base trust) - documentation, articles
- [ ] `SourceTypeCrossRef` (0.75 base trust) - inferred from multiple sources
- [ ] `SourceTypeLLM` (0.5 base trust) - LLM inference (requires verification)

#### Attribution Operations
- [ ] `RecordProvenance(nodeID string, prov *Provenance) error`
- [ ] `GetProvenance(nodeID string) ([]*Provenance, error)`
- [ ] `GetProvenanceChain(nodeID string, depth int) ([]*ProvenanceChain, error)`
- [ ] `FindBySource(sourceType SourceType, sourceID string) ([]*GraphNode, error)`
- [ ] `InvalidateBySource(sourceID string) error` (mark all from source as stale)

#### Verification Chain
- [ ] Track what data verified other data
- [ ] Build verification DAG for complex claims
- [ ] Compute transitive confidence: `conf = prod(chain_confs) * base_conf`
- [ ] Detect circular verification (reject)

#### Citation Generation
- [ ] `GenerateCitation(nodeID string) (string, error)`
- [ ] Include source, verification status, timestamp
- [ ] Format for LLM context injection
- [ ] Support multiple citation formats (inline, footnote, structured)

**Tests:**
- [ ] Provenance recorded correctly for all source types
- [ ] Provenance chain retrieved correctly
- [ ] Confidence correctly propagated through chain
- [ ] Circular verification detected and rejected
- [ ] Citation generation produces valid output

```go
type ProvenanceTracker struct {
    db *VectorGraphDB
}

type Provenance struct {
    ID          string     `json:"id"`
    NodeID      string     `json:"node_id"`
    SourceType  SourceType `json:"source_type"`
    SourceID    string     `json:"source_id"`
    Confidence  float64    `json:"confidence"`
    VerifiedAt  time.Time  `json:"verified_at"`
    Verifier    string     `json:"verifier"`  // "human", "automated", "cross_ref"
    VerifiedBy  []string   `json:"verified_by"`  // IDs of verifying nodes
}

type ProvenanceChain struct {
    Node             *GraphNode
    DirectProvenance *Provenance
    Chain            []*Provenance
    TransitiveConf   float64
}
```

---

### 6.8 Mitigation 4: Trust Hierarchy

Implements a trust scoring system that weights information by source reliability.

**Files to create:**
- `core/vectorgraphdb/mitigations/trust.go`
- `core/vectorgraphdb/mitigations/trust_scoring.go`
- `core/vectorgraphdb/mitigations/trust_test.go`

**Acceptance Criteria:**

#### Trust Levels
- [ ] Level 6 - Verified Code (1.0): AST-parsed, type-checked
- [ ] Level 5 - Recent History (0.9): Last 24h decisions/outcomes
- [ ] Level 4 - Official Docs (0.8): README, API docs, comments
- [ ] Level 3 - Old History (0.7): >24h ago, verified outcomes
- [ ] Level 2 - External Articles (0.5): Blogs, tutorials, StackOverflow
- [ ] Level 1 - LLM Inference (0.3): Unverified LLM output
- [ ] Level 0 - Unknown (0.1): No provenance

#### Trust Score Computation
- [ ] `ComputeTrustScore(node *GraphNode) (float64, error)`
- [ ] Consider: source type, age, verification status, cross-references
- [ ] Formula: `trust = baseTrust * freshnessDecay * verificationBoost * crossRefBoost`
- [ ] Cross-reference boost: +0.1 per independent verification (max +0.3)

#### Trust-Weighted Search
- [ ] Apply trust scores to search results
- [ ] Option to filter by minimum trust level
- [ ] Sort by: `finalScore = similarity * trustWeight`
- [ ] Return trust metadata with results

#### Trust Promotion/Demotion
- [ ] `PromoteTrust(nodeID string, reason string) error` (human verification)
- [ ] `DemoteTrust(nodeID string, reason string) error` (contradicted)
- [ ] Automatic demotion on staleness
- [ ] Automatic promotion on cross-reference verification

#### Trust Audit Log
- [ ] Log all trust changes with timestamp, reason, actor
- [ ] Query trust history for a node
- [ ] Export trust report for analysis

**Tests:**
- [ ] Trust levels assigned correctly by source
- [ ] Trust decay applied correctly over time
- [ ] Search results weighted by trust
- [ ] Promotion/demotion changes trust correctly
- [ ] Audit log records all changes

```go
type TrustHierarchy struct {
    db         *VectorGraphDB
    levels     map[SourceType]TrustLevel
    decayRates map[TrustLevel]float64
}

type TrustLevel int
const (
    TrustUnknown TrustLevel = iota
    TrustLLMInference
    TrustExternalArticle
    TrustOldHistory
    TrustOfficialDocs
    TrustRecentHistory
    TrustVerifiedCode
)

type TrustInfo struct {
    NodeID          string
    TrustLevel      TrustLevel
    TrustScore      float64
    BaseScore       float64
    FreshnessBoost  float64
    VerifyBoost     float64
    CrossRefBoost   float64
    EffectiveScore  float64
}

func (t *TrustHierarchy) GetTrustInfo(nodeID string) (*TrustInfo, error)
func (t *TrustHierarchy) ApplyTrust(results []*SearchResult) []*SearchResult
func (t *TrustHierarchy) Promote(nodeID string, reason string, actor string) error
func (t *TrustHierarchy) Demote(nodeID string, reason string, actor string) error
```

---

### 6.9 Mitigation 5: Conflict Detection

Detects and tracks contradictions between stored information.

**Files to create:**
- `core/vectorgraphdb/mitigations/conflicts.go`
- `core/vectorgraphdb/mitigations/contradiction.go`
- `core/vectorgraphdb/mitigations/conflicts_test.go`

**Acceptance Criteria:**

#### Conflict Detection
- [ ] Detect semantic contradictions using embedding similarity + content analysis
- [ ] Detect temporal contradictions (newer data contradicts older)
- [ ] Detect structural contradictions (graph inconsistencies)
- [ ] Run detection on insert and on query

#### Conflict Types
- [ ] `ConflictSemantic`: Similar embeddings, contradictory content
- [ ] `ConflictTemporal`: Same topic, different answers at different times
- [ ] `ConflictStructural`: Graph edges that shouldn't coexist
- [ ] `ConflictProvenance`: Same source, different claims

#### Detection Algorithms
- [ ] `DetectOnInsert(node *GraphNode) ([]*Conflict, error)` - check against existing
- [ ] `DetectInResults(results []*QueryResult) ([]*Conflict, error)` - check within results
- [ ] `ScanForConflicts(domain Domain) ([]*Conflict, error)` - batch scan
- [ ] Semantic comparison: high similarity (>0.9) + low content match (<0.5) = potential conflict
- [ ] Temporal comparison: same subject, different values, temporal gap

#### Conflict Resolution
- [ ] `ResolveConflict(id string, resolution Resolution) error`
- [ ] Resolution options: `KeepNewer`, `KeepTrusted`, `KeepBoth`, `MarkBothStale`, `HumanReview`
- [ ] Automatic resolution for clear cases (much newer + higher trust)
- [ ] Queue ambiguous conflicts for human review

#### Conflict Reporting
- [ ] `GetActiveConflicts(limit int) ([]*Conflict, error)`
- [ ] `GetConflictsForNode(nodeID string) ([]*Conflict, error)`
- [ ] `GetConflictStats() (*ConflictStats, error)`
- [ ] Include conflicts in search result metadata

**Tests:**
- [ ] Semantic conflicts detected correctly
- [ ] Temporal conflicts detected correctly
- [ ] Automatic resolution works for clear cases
- [ ] Ambiguous conflicts queued for review
- [ ] Resolved conflicts marked correctly

```go
type ConflictDetector struct {
    db              *VectorGraphDB
    hnsw            *HNSWIndex
    semanticThresh  float64  // similarity threshold for potential conflict
    contentAnalyzer ContentAnalyzer
}

type Conflict struct {
    ID           string       `json:"id"`
    NodeAID      string       `json:"node_a_id"`
    NodeBID      string       `json:"node_b_id"`
    ConflictType ConflictType `json:"conflict_type"`
    Similarity   float64      `json:"similarity"`
    Details      string       `json:"details"`
    DetectedAt   time.Time    `json:"detected_at"`
    Resolution   *Resolution  `json:"resolution,omitempty"`
    ResolvedAt   *time.Time   `json:"resolved_at,omitempty"`
}

func (d *ConflictDetector) DetectOnInsert(ctx context.Context, node *GraphNode, embedding []float32) ([]*Conflict, error)
func (d *ConflictDetector) Resolve(conflictID string, resolution Resolution) error
func (d *ConflictDetector) AnnotateResults(results []*QueryResult) []*QueryResult
```

---

### 6.10 Mitigation 6: Context Quality Scoring

Optimizes context selection to maximize information density while minimizing token usage.

**Files to create:**
- `core/vectorgraphdb/mitigations/quality.go`
- `core/vectorgraphdb/mitigations/scoring.go`
- `core/vectorgraphdb/mitigations/quality_test.go`

**Acceptance Criteria:**

#### Quality Metrics
- [ ] `Relevance`: Vector similarity to query
- [ ] `Freshness`: Temporal decay score
- [ ] `Trust`: Trust hierarchy score
- [ ] `Density`: Information per token (unique content / token count)
- [ ] `Diversity`: Penalty for redundant information

#### Quality Score Formula
- [ ] `quality = w1*relevance + w2*freshness + w3*trust + w4*density - w5*redundancy`
- [ ] Default weights: relevance=0.35, freshness=0.20, trust=0.25, density=0.15, redundancy=0.05
- [ ] Configurable weights per use case

#### Token Estimation
- [ ] `EstimateTokens(node *GraphNode) int` - estimate token count for node
- [ ] Use tiktoken-compatible estimation for accuracy
- [ ] Cache estimates for performance
- [ ] Account for formatting overhead

#### Context Selection
- [ ] `SelectContext(results []*QueryResult, tokenBudget int) ([]*ContextItem, error)`
- [ ] Greedy selection: highest quality-per-token first
- [ ] Respect token budget strictly
- [ ] Ensure diversity (don't select redundant items)
- [ ] Return selection rationale

#### Redundancy Detection
- [ ] Detect overlapping content between nodes
- [ ] Compute content similarity matrix
- [ ] Apply diversity penalty for similar selections
- [ ] Prefer unique perspectives

#### Budget Optimization
- [ ] Given budget B, maximize sum of quality scores
- [ ] Knapsack-style optimization (approximation OK for speed)
- [ ] Return unused budget for caller

**Tests:**
- [ ] Quality scores computed correctly
- [ ] Token estimation matches actual
- [ ] Context selection respects budget
- [ ] Diversity penalty applied correctly
- [ ] Selection maximizes quality within budget

```go
type ContextQualityScorer struct {
    db           *VectorGraphDB
    weights      QualityWeights
    tokenizer    Tokenizer
    embedder     Embedder
}

type QualityWeights struct {
    Relevance   float64
    Freshness   float64
    Trust       float64
    Density     float64
    Redundancy  float64
}

type ContextItem struct {
    Node          *GraphNode
    QualityScore  float64
    TokenCount    int
    ScorePerToken float64
    Components    QualityComponents
    Selected      bool
    Reason        string
}

type QualityComponents struct {
    Relevance   float64
    Freshness   float64
    Trust       float64
    Density     float64
    Redundancy  float64
}

func (s *ContextQualityScorer) Score(node *GraphNode, query []float32) (*ContextItem, error)
func (s *ContextQualityScorer) SelectContext(results []*QueryResult, budget int) ([]*ContextItem, int, error)
```

---

### 6.11 Mitigation 7: LLM Prompt Engineering

Implements structured context building for LLM prompts with explicit trust and conflict information.

**Files to create:**
- `core/vectorgraphdb/mitigations/prompt.go`
- `core/vectorgraphdb/mitigations/context_builder.go`
- `core/vectorgraphdb/mitigations/prompt_test.go`

**Acceptance Criteria:**

#### Context Structure
- [ ] Group context by domain (Code, History, Academic)
- [ ] Include trust level annotation for each item
- [ ] Include freshness indication (verified date)
- [ ] Include conflict warnings where applicable
- [ ] Include provenance summary

#### Prompt Template
- [ ] Inject structured preamble explaining trust levels
- [ ] Format context items with clear delineation
- [ ] Include explicit instructions about handling conflicts
- [ ] Include instructions about trusting recent code over old docs

#### LLM Context Builder
- [ ] `BuildContext(items []*ContextItem) (*LLMContext, error)`
- [ ] Generate structured markdown for context injection
- [ ] Generate JSON for structured API calls
- [ ] Generate plain text for simple models

#### Trust Instructions
- [ ] "Code from the codebase is authoritative"
- [ ] "Recent session decisions (last 24h) reflect current intent"
- [ ] "Older documentation may be outdated"
- [ ] "If information conflicts, prefer higher trust sources"
- [ ] "Flag any unresolved conflicts in your response"

#### Conflict Handling
- [ ] Annotate conflicting items in context
- [ ] Include resolution guidance
- [ ] Request explicit acknowledgment of conflicts in response

#### Output Formats
- [ ] `FormatAsMarkdown() string` - for human-readable
- [ ] `FormatAsJSON() string` - for structured API
- [ ] `FormatAsXML() string` - for specific models
- [ ] `EstimateTokens() int` - for budget checking

**Tests:**
- [ ] Context built correctly with trust annotations
- [ ] Conflict warnings included
- [ ] Output formats valid
- [ ] Token estimate accurate
- [ ] Preamble instructions present

```go
type LLMContextBuilder struct {
    scorer     *ContextQualityScorer
    trust      *TrustHierarchy
    conflicts  *ConflictDetector
    provenance *ProvenanceTracker
}

type LLMContext struct {
    Preamble     string
    CodeContext  []*AnnotatedItem
    HistContext  []*AnnotatedItem
    AcadContext  []*AnnotatedItem
    Conflicts    []*ConflictSummary
    TotalTokens  int
}

type AnnotatedItem struct {
    Content     string
    Domain      Domain
    TrustLevel  TrustLevel
    TrustScore  float64
    Freshness   time.Time
    Source      string
    Conflicts   []string  // IDs of conflicting items
}

func (b *LLMContextBuilder) Build(ctx context.Context, items []*ContextItem) (*LLMContext, error)
func (c *LLMContext) FormatAsMarkdown() string
func (c *LLMContext) FormatAsJSON() string
func (c *LLMContext) FormatAsSystemPrompt() string
```

---

### 6.12 Unified Query Resolution

Integrates all components into a unified query resolution pipeline that **agents invoke via skills**.

**IMPORTANT**: The Unified Resolver is NOT a replacement for agent (LLM) reasoning. It is a **skill implementation** that agents call when they decide they need VectorGraphDB context. The agent always runs first, decides what context it needs, then invokes resolver skills.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    RESOLVER IN CONTEXT                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   1. User query arrives                                                     │
│   2. Intent cache checked (if hit → return cached, 0 tokens)               │
│   3. Cache miss → Agent (LLM) runs                                         │
│   4. Agent decides: "I need code context about auth"                       │
│   5. Agent invokes: search_code("authentication handler")                  │
│      └─── This skill calls UnifiedResolver internally                      │
│   6. UnifiedResolver returns curated context (~800 tokens)                 │
│   7. Agent synthesizes response with curated context                       │
│                                                                             │
│   The agent ALWAYS runs on cache miss. Resolver provides curated context.  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Files to create:**
- `core/vectorgraphdb/resolver.go`
- `core/vectorgraphdb/pipeline.go`
- `core/vectorgraphdb/resolver_test.go`

**Acceptance Criteria:**

#### Resolution Pipeline (called by skills, not directly by users)
1. [ ] Parse query intent (code, history, academic, hybrid)
2. [ ] Check intent cache for exact/similar match
3. [ ] Generate query embedding
4. [ ] Execute HNSW search (domain-filtered)
5. [ ] Graph expansion for context
6. [ ] Apply freshness decay
7. [ ] Apply trust weighting
8. [ ] Detect conflicts in results
9. [ ] Score context quality
10. [ ] Select within token budget
11. [ ] Format results for agent consumption (NOT raw LLM prompt)
12. [ ] Cache result for future queries

#### Unified Resolver
- [ ] `Resolve(ctx context.Context, query string, opts *ResolveOptions) (*Resolution, error)`
- [ ] Single entry point for all queries
- [ ] Automatic domain detection from query text
- [ ] Configurable pipeline stages

#### Resolution Options
- [ ] `Domains []Domain` - restrict to specific domains
- [ ] `TokenBudget int` - max tokens for context
- [ ] `MinTrust TrustLevel` - minimum trust threshold
- [ ] `MinFreshness time.Duration` - max age for results
- [ ] `IncludeConflicts bool` - include conflicting data with warnings
- [ ] `MaxGraphDepth int` - limit graph expansion
- [ ] `CacheResult bool` - whether to cache this resolution

#### Resolution Result
- [ ] Selected context items with annotations
- [ ] Formatted LLM prompt
- [ ] Pipeline metrics (timing per stage)
- [ ] Conflict report
- [ ] Token usage summary
- [ ] Cache hit/miss info

#### Performance Requirements
- [ ] Full resolution < 100ms for typical query
- [ ] Cache hit < 10ms
- [ ] Pipeline stage timing tracked

**Tests:**
- [ ] End-to-end resolution works
- [ ] All pipeline stages execute correctly
- [ ] Cache integration works
- [ ] Options respected
- [ ] Performance targets met

```go
type UnifiedResolver struct {
    db          *VectorGraphDB
    hnsw        *HNSWIndex
    embedder    Embedder
    intentCache *IntentCache
    firewall    *HallucinationFirewall
    freshness   *FreshnessTracker
    provenance  *ProvenanceTracker
    trust       *TrustHierarchy
    conflicts   *ConflictDetector
    scorer      *ContextQualityScorer
    prompter    *LLMContextBuilder
}

type ResolveOptions struct {
    Domains         []Domain
    TokenBudget     int
    MinTrust        TrustLevel
    MinFreshness    time.Duration
    IncludeConflicts bool
    MaxGraphDepth   int
    CacheResult     bool
}

type Resolution struct {
    Query           string
    Context         *LLMContext
    Items           []*ContextItem
    Conflicts       []*Conflict
    Metrics         *PipelineMetrics
    TokensUsed      int
    TokenBudget     int
    CacheHit        bool
}

type PipelineMetrics struct {
    IntentDetect   time.Duration
    CacheCheck     time.Duration
    Embedding      time.Duration
    VectorSearch   time.Duration
    GraphExpand    time.Duration
    Freshness      time.Duration
    Trust          time.Duration
    Conflict       time.Duration
    Scoring        time.Duration
    ContextBuild   time.Duration
    Total          time.Duration
}

func (r *UnifiedResolver) Resolve(ctx context.Context, query string, opts *ResolveOptions) (*Resolution, error)
```

---

### 6.13 Agent Integration: Librarian

Integrates VectorGraphDB with the Librarian agent for code domain. The Librarian agent (LLM) **decides** when to invoke these skills based on the query.

**IMPORTANT**: Skills return curated context to the agent, not raw files. Target ~800 tokens of context per skill invocation vs ~2,000 tokens of raw file content.

**Files to create:**
- `agents/librarian/vectorgraphdb.go`
- `agents/librarian/code_indexer.go`
- `agents/librarian/skills_vectorgraphdb.go`
- `agents/librarian/vectorgraphdb_test.go`

**Acceptance Criteria:**

#### Code Indexing
- [ ] Index files as `NodeTypeFile` with content embeddings
- [ ] Index functions as `NodeTypeFunction` with signature + doc embeddings
- [ ] Index types as `NodeTypeType` with definition embeddings
- [ ] Index packages as `NodeTypePackage`
- [ ] Create structural edges: `Calls`, `Imports`, `Defines`, `Implements`, `Contains`
- [ ] Incremental indexing on file change

#### Query Integration
- [ ] VectorGraphDB skills available for agent to invoke (agent decides when)
- [ ] Skills return scored, curated results (not raw files)
- [ ] Results include relevant snippets, not entire file contents
- [ ] Target: ~800 tokens of context per skill invocation

#### Skills (Agent Decides When to Invoke)
- [ ] `search_code` - Semantic search, returns scored snippets (~10 results max)
- [ ] `get_symbol` - Get specific symbol details with source
- [ ] `get_dependencies` - Graph traversal for what symbol depends on
- [ ] `get_dependents` - Graph traversal for what depends on symbol
- [ ] `find_similar_symbols` - Find semantically similar code
- [ ] `get_file_symbols` - All symbols in a file
- [ ] `get_history_for_code` - Cross-domain: history for code (via edges)

**Tests:**
- [ ] File indexing creates correct nodes/edges
- [ ] Semantic search finds relevant code
- [ ] Graph queries return correct relationships
- [ ] Incremental indexing updates correctly

---

### 6.14 Agent Integration: Archivalist

Integrates VectorGraphDB with the Archivalist agent for history domain. The Archivalist agent (LLM) **decides** when to invoke these skills based on the query.

**IMPORTANT**: Skills return curated context to the agent, not full history dumps. Target ~800 tokens of context per skill invocation.

**Files to create:**
- `agents/archivalist/vectorgraphdb.go`
- `agents/archivalist/history_indexer.go`
- `agents/archivalist/skills_vectorgraphdb.go`
- `agents/archivalist/vectorgraphdb_test.go`

**Acceptance Criteria:**

#### History Indexing
- [ ] Index sessions as `NodeTypeSession`
- [ ] Index decisions as `NodeTypeDecision` with context embeddings
- [ ] Index failures as `NodeTypeFailure` with error + resolution embeddings
- [ ] Index patterns as `NodeTypePattern`
- [ ] Index workflows as `NodeTypeWorkflow`
- [ ] Create temporal edges: `Follows`, `Causes`, `Resolves`
- [ ] Create cross-domain edges: `Modified` (to code files)

#### Query Integration
- [ ] VectorGraphDB skills available for agent to invoke (agent decides when)
- [ ] Skills return scored, curated results (not full history)
- [ ] Results include relevant context, not entire session logs
- [ ] Target: ~800 tokens of context per skill invocation

#### Skills (Agent Decides When to Invoke)
- [ ] `store_summary` - Store summaries from other agents (creates cross-domain edges)
- [ ] `search_history` - Semantic search over historical context
- [ ] `find_patterns` - Find recurring patterns with min occurrence threshold
- [ ] `find_failures` - Find similar failures with resolutions
- [ ] `get_session_history` - Get history for specific session
- [ ] `get_code_for_history` - Cross-domain: code referenced in history (via edges)
- [ ] `get_decisions` - Find past architectural/design decisions

**Tests:**
- [ ] History entries indexed correctly
- [ ] Similar failure search works
- [ ] Pattern matching finds relevant patterns
- [ ] Cross-domain edges to code created

---

### 6.15 Agent Integration: Academic

Integrates VectorGraphDB with the Academic agent for academic domain. The Academic agent (Opus 4.5 for complex reasoning) **decides** when to invoke these skills based on the query.

**IMPORTANT**: Academic uses Opus 4.5 for complex reasoning tasks like research synthesis and approach comparison. Skills return curated context; the agent does the heavy reasoning.

**Files to create:**
- `agents/academic/vectorgraphdb.go`
- `agents/academic/knowledge_indexer.go`
- `agents/academic/skills_vectorgraphdb.go`
- `agents/academic/vectorgraphdb_test.go`

**Acceptance Criteria:**

#### Knowledge Indexing
- [ ] Index GitHub repos as `NodeTypeRepo`
- [ ] Index documentation as `NodeTypeDoc`
- [ ] Index articles as `NodeTypeArticle`
- [ ] Index concepts as `NodeTypeConcept`
- [ ] Index best practices as `NodeTypeBestPractice`
- [ ] Create semantic edges between related concepts
- [ ] Create cross-domain edges: `References`, `AppliesTo`, `Documents`

#### Query Integration
- [ ] VectorGraphDB skills available for agent to invoke (agent decides when)
- [ ] Skills return scored, curated results (not full documents)
- [ ] Agent (Opus 4.5) synthesizes and reasons over curated context
- [ ] Target: ~1,200 tokens of context per skill invocation (higher for research)

#### Skills (Agent Decides When to Invoke)
- [ ] `research` - Deep research with configurable depth (agent synthesizes)
- [ ] `find_best_practices` - Find established best practices
- [ ] `find_papers` - Find academic papers with abstracts
- [ ] `compare_approaches` - Get context for comparing approaches (agent reasons)
- [ ] `synthesize_with_codebase` - Cross-domain: theory vs practice (via edges)
- [ ] `get_code_for_academic` - Cross-domain: code implementing concepts (via edges)
- [ ] `get_history_for_academic` - Cross-domain: history related to concepts (via edges)

**Tests:**
- [ ] Academic content indexed correctly
- [ ] Concept search finds relevant knowledge
- [ ] Cross-domain queries work (academic → code)
- [ ] Best practice recommendations work

---

### 6.16 Cross-Domain Query Integration

Implements unified cross-domain queries across all three agents.

**Files to create:**
- `core/vectorgraphdb/crossdomain.go`
- `core/vectorgraphdb/crossdomain_test.go`

**Acceptance Criteria:**

#### Unified Query Interface
- [ ] Single query entry point for all domains
- [ ] Automatic domain routing based on query intent
- [ ] Result merging from multiple domains
- [ ] Cross-domain path discovery

#### Cross-Domain Scenarios
- [ ] "How do I implement X?" → Academic (patterns) → Code (examples) → History (past attempts)
- [ ] "Why did this fail?" → History (failure) → Code (file) → Academic (known issues)
- [ ] "Best way to do Y?" → Academic (best practices) → Code (existing impl) → History (outcomes)

#### Result Aggregation
- [ ] Merge results from multiple domains
- [ ] De-duplicate overlapping information
- [ ] Order by combined relevance + trust + freshness
- [ ] Annotate with domain source

**Tests:**
- [ ] Cross-domain queries return results from all relevant domains
- [ ] Result merging works correctly
- [ ] Domain paths tracked correctly

---

### 6.17 Performance Benchmarks

Creates benchmarks to validate performance targets.

**Files to create:**
- `core/vectorgraphdb/benchmark_test.go`

**Acceptance Criteria:**

#### Insert Benchmarks
- [ ] Insert 10K nodes: < 10 seconds
- [ ] Insert 100K nodes: < 2 minutes
- [ ] Insert single node: < 1ms average

#### Search Benchmarks
- [ ] Vector search k=10, 10K nodes: < 5ms
- [ ] Vector search k=10, 100K nodes: < 20ms
- [ ] Graph traversal depth=3, 10K nodes: < 10ms
- [ ] Hybrid query, 10K nodes: < 50ms

#### Full Pipeline Benchmarks
- [ ] Unified resolution (cache miss): < 100ms
- [ ] Unified resolution (cache hit): < 10ms
- [ ] Context building: < 20ms

#### Memory Benchmarks
- [ ] Memory per 10K nodes: < 50MB
- [ ] Memory per 100K nodes: < 500MB
- [ ] No memory leaks over 1M operations

**Tests:**
- [ ] All benchmarks pass performance targets
- [ ] Results logged for regression tracking

---

### 6.18 Integration Tests

Creates comprehensive integration tests for the full VectorGraphDB system.

**Files to create:**
- `tests/integration/vectorgraphdb_test.go`
- `tests/integration/crossdomain_test.go`
- `tests/integration/mitigations_test.go`

**Acceptance Criteria:**

#### End-to-End Tests
- [ ] Index codebase, query for function, get results with context
- [ ] Store session history, query for similar failure, get resolution
- [ ] Ingest documentation, query for best practice, get recommendations
- [ ] Cross-domain query spanning all three domains

#### Mitigation Tests
- [ ] Hallucination firewall blocks unverified LLM output
- [ ] Freshness decay affects search results correctly
- [ ] Trust hierarchy weights results correctly
- [ ] Conflict detection finds contradictions
- [ ] Quality scoring maximizes information density

#### Failure Mode Tests
- [ ] Graceful degradation on embedder failure
- [ ] Graceful degradation on SQLite failure
- [ ] Recovery after crash (WAL replay)
- [ ] Concurrent access stress test

**Tests:**
- [ ] All integration tests pass
- [ ] No race conditions detected
- [ ] Recovery tests pass

---

### 6.19 XOR Filter Internal Optimization

Implements XOR filters as **internal optimizations** within search skills, NOT as separate LLM tools. This is critical for actual token savings.

**IMPORTANT**: XOR filters are NOT exposed to LLMs. They are used internally by search skills to return "no_matches" quickly when content doesn't exist.

**Files to create:**
- `core/vectorgraphdb/xorfilter.go`
- `core/vectorgraphdb/xorfilter_manager.go`
- `core/vectorgraphdb/xorfilter_test.go`

**Acceptance Criteria:**

#### XOR Filter Manager
- [ ] `XORFilterManager` struct with filters per domain (code, history, academic)
- [ ] `Contains(domain, topicHash) bool` - checks XOR filter + pending
- [ ] `NotifyAdd(domain, keys)` - adds to pending bloom filter after VDB write
- [ ] `Rebuild(ctx) error` - rebuilds all filters from VectorGraphDB
- [ ] Background goroutine rebuilds periodically (configurable interval)
- [ ] Pending bloom filter for recent additions (zero staleness)
- [ ] Thread-safe with RWMutex for filter access

#### XOR Filter Data Structure
- [ ] Use `github.com/FastFilter/xorfilter` or equivalent
- [ ] Xor8 filters for ~0.3% false positive rate
- [ ] Topic key hashing: consistent hash function for query terms
- [ ] Filter size tracking and metrics

#### Integration with Search Skills
- [ ] `search_code` uses XOR internally for early exit
- [ ] `search_history` uses XOR internally for early exit
- [ ] `search_academic` uses XOR internally for early exit
- [ ] `search_all` uses XOR per-domain for selective searching
- [ ] Early exit returns `{"status": "no_matches", "hint": "..."}` (~30 tokens vs ~800)

#### Multi-Domain Search Skill
- [ ] `search_all` skill searches multiple domains in ONE call
- [ ] Internal XOR check per domain before searching
- [ ] Skips domains with no matches (minimal tokens)
- [ ] Returns results grouped by domain
- [ ] Reduces 3 tool calls to 1 (saves ~1,500 tokens/cross-domain query)

```go
// search_all response format
type MultiDomainResponse struct {
    Query   string                      `json:"query"`
    Results map[string]DomainResult     `json:"results"`
}

type DomainResult struct {
    Status  string         `json:"status"`  // "found" or "no_matches"
    Results []SearchResult `json:"results,omitempty"`
    Count   int            `json:"count"`
}
```

#### Inline Domain Hints
- [ ] Search results include `also_exists_in` field
- [ ] XOR probes other domains after primary search (internal, fast)
- [ ] Agent learns about related domains without extra tool calls
- [ ] Suggestions field with actionable hints

```go
type SearchResponse struct {
    Results      []SearchResult `json:"results"`
    AlsoExistsIn []string       `json:"also_exists_in,omitempty"`
    Suggestions  []string       `json:"suggestions,omitempty"`
}
```

#### Smart Search with Budget Control
- [ ] `smart_search` skill with token budget parameter
- [ ] Internal progressive retrieval within budget
- [ ] Thoroughness levels: quick, moderate, thorough
- [ ] Returns `tokens_used` and `budget` in response
- [ ] Agent doesn't manage retrieval depth - skill handles it

#### Pending Set for Zero Staleness
- [ ] Bloom filter for pending additions (fast, mutable)
- [ ] Updated atomically with VectorGraphDB writes
- [ ] Cleared during XOR rebuild
- [ ] `Contains()` checks both XOR and pending

```go
func (m *XORFilterManager) Contains(domain string, key uint64) bool {
    // Check immutable XOR filter (stable content)
    if m.filters[domain].Contains(key) {
        return true
    }
    // Check pending bloom filter (recent additions)
    if m.pending[domain].Test(key) {
        return true
    }
    return false
}
```

#### Topic Key Extraction
- [ ] Extract topic keys from node content during indexing
- [ ] Consistent hashing for query terms
- [ ] Handle multi-word topics (n-grams or word-level)
- [ ] Normalize: lowercase, stemming optional

**Tests:**
- [ ] XOR filter contains returns correct results
- [ ] Pending set catches recent additions
- [ ] Rebuild correctly merges pending into XOR
- [ ] Early exit returns minimal tokens
- [ ] Multi-domain search reduces token usage
- [ ] Inline hints probe other domains correctly
- [ ] Budget control stays within limits
- [ ] No false negatives (pending + XOR covers all)

---

### 6.20 Architect Planning Preflight

Special planning-oriented preflight for Architect agent to scope work before detailed planning.

**Files to create:**
- `agents/architect/planning_preflight.go`
- `agents/architect/planning_preflight_test.go`

**Acceptance Criteria:**

#### Planning Preflight Skill
- [ ] `plan_preflight` skill for Architect only
- [ ] Checks all 3 domains in one call
- [ ] Returns existence flags per domain
- [ ] Includes planning hints based on what exists
- [ ] Uses XOR filters internally (fast)

```go
// plan_preflight response
type PlanningPreflight struct {
    Topic         string                  `json:"topic"`
    Domains       map[string]DomainCheck  `json:"domains"`
    PlanningHints []string                `json:"planning_hints"`
}

type DomainCheck struct {
    Exists bool   `json:"exists"`
    Hint   string `json:"hint"`
}
```

#### Planning Hints Generation
- [ ] "Greenfield work" when code domain is empty
- [ ] "Historical context available" when history has matches
- [ ] "Best practices exist" when academic has matches
- [ ] "Suggested agent order" based on what exists

**Example response:**
```json
{
  "topic": "retry logic",
  "domains": {
    "code": {"exists": false, "hint": "No existing implementation"},
    "history": {"exists": true, "hint": "Past solutions available"},
    "academic": {"exists": true, "hint": "Best practices available"}
  },
  "planning_hints": [
    "This is greenfield work (no existing code)",
    "Historical context available - learn from past",
    "Best practices exist - follow standards",
    "Suggested: Archivalist → Academic → Engineer"
  ]
}
```

**Tests:**
- [ ] Preflight returns correct domain flags
- [ ] Planning hints are actionable
- [ ] Fast response (<50ms)

---

## Token Savings Targets

### CRITICAL: How VectorGraphDB Saves Tokens

**VectorGraphDB does NOT replace LLM calls.** The agent (LLM) always runs on cache misses and **decides** when to invoke VectorGraphDB skills. Savings come from **context reduction**, not from avoiding LLM reasoning.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    CORRECT ARCHITECTURE                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   L1: INTENT CACHE                    L2: AGENT (LLM)                       │
│   ─────────────────                   ───────────────                       │
│   • 0 tokens on hit                   • Agent ALWAYS runs on cache miss    │
│   • ~68% of queries                   • Agent DECIDES to call VDB skills   │
│                                       • Agent processes curated results    │
│                                       • ~32% of queries                    │
│                                                                             │
│   Example flow:                                                             │
│   1. Cache miss → Agent runs (~300 tokens for reasoning)                   │
│   2. Agent invokes: search_code("auth handler", limit=10)                  │
│   3. VectorGraphDB returns curated results (~800 tokens vs ~2000 raw)      │
│   4. Agent synthesizes response (~700 tokens)                              │
│   5. Total: ~1,800 tokens (vs ~3,000 baseline)                             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Token Savings Breakdown

| Source | Savings | How It Works |
|--------|---------|--------------|
| **Intent Cache** | ~67% | Similar queries hit cache (0 tokens) |
| **Context Reduction** | ~24% additional | VDB returns curated ~800 tokens vs raw ~2,000 tokens |
| **Combined** | **~75%** | Total savings vs baseline |

### Per-Query Token Estimates

| Component | Without VDB | With VDB | Savings |
|-----------|-------------|----------|---------|
| Query understanding | 300 tokens | 300 tokens | 0% |
| Context from search | 2,000 tokens | 800 tokens | **60%** |
| Response generation | 700 tokens | 700 tokens | 0% |
| **Total per query** | 3,000 tokens | 1,800 tokens | **40%** |

### Agent Token Estimates (per 100 queries)

| Agent | Baseline | Cache Only | +VectorGraphDB | +XOR Internal | Total Savings |
|-------|----------|------------|----------------|---------------|---------------|
| **Librarian** | 300,000 | 90,000 | 54,000 | 48,000 | **84%** |
| **Archivalist** | 250,000 | 62,500 | 37,500 | 33,000 | **87%** |
| **Academic** | 400,000 | 160,000 | 96,000 | 82,000 | **80%** |
| **Cross-Domain** | N/A | N/A | 50,000 | 35,000 | **~65% vs non-XOR** |
| **TOTAL** | 950,000 | 312,500 | 237,500 | 198,000 | **~80%** |

**XOR Internal Optimization Savings:**
- Empty search early exit: ~770 tokens saved per empty search
- Multi-domain `search_all`: ~1,500 tokens saved per cross-domain query
- Inline domain hints: ~100 tokens saved per query (avoids extra probe calls)
- Architect planning preflight: ~750 tokens saved per planning decision

### Monthly Cost Targets

| Scenario | Monthly Cost | vs Baseline |
|----------|-------------|-------------|
| Baseline (no cache, no VDB) | $285/month | - |
| Cache only | $94/month | 67% savings |
| Cache + VectorGraphDB | $72/month | 75% savings |
| **Cache + VectorGraphDB + XOR** | **$60/month** | **~80% savings** |

**Overall Target**: 80% token reduction across all agents (with XOR optimization).

### Implementation Guidelines

**DO:**
- Design skills that return curated, scored results
- Return ~5-10 relevant results, not 50
- Include only necessary metadata in results
- Let the agent decide retrieval depth (progressive retrieval)
- Cache skill results where appropriate
- Use XOR filters INTERNALLY in search skills for early exit
- Return "no_matches" quickly when XOR says content doesn't exist
- Include `also_exists_in` hints in search responses
- Use `search_all` for multi-domain queries (1 call vs 3)

**DON'T:**
- Assume VectorGraphDB calls are "free" (agent still runs)
- Return raw file contents in skill results
- Return all matching results (use limits)
- Bypass the agent with "graph-only" resolution
- Expose XOR filters as separate LLM tools (adds tool call overhead)
- Make agents call existence checks before searches (baked into search internally)

---

## Latency Targets

| Operation | Target | P95 |
|-----------|--------|-----|
| Vector insert | < 1ms | < 5ms |
| Vector search (k=10) | < 5ms | < 20ms |
| Graph traversal (depth=3) | < 10ms | < 30ms |
| Hybrid query | < 50ms | < 100ms |
| Full resolution (cache miss) | < 100ms | < 200ms |
| Full resolution (cache hit) | < 10ms | < 25ms |

---

## Memory Targets

| Scale | RAM Usage | SQLite Size |
|-------|-----------|-------------|
| 10K nodes | ~50MB | ~100MB |
| 50K nodes | ~200MB | ~500MB |
| 100K nodes | ~400MB | ~1GB |

---

## Implementation Order

1. **Week 1-2**: 6.1 (Schema), 6.2 (HNSW) - can parallelize
2. **Week 3**: 6.3 (Nodes/Edges), 6.4 (Search/Traversal) - can parallelize
3. **Week 4-5**: 6.5-6.11 (All Mitigations) - can parallelize
4. **Week 6**: 6.12 (Unified Resolver)
5. **Week 7-8**: 6.13-6.16 (Agent Integrations) - can parallelize
6. **Week 9**: 6.19 (XOR Filter Optimization) - integrates with 6.13-6.16 search skills
7. **Week 10**: 6.20 (Architect Planning Preflight)
8. **Week 11-12**: 6.21-6.24 (Scratchpad, Style Inference, Component Registry, Mistake Memory) - can parallelize
9. **Week 13-14**: 6.25-6.28 (Diff Preview, Preferences, File Snapshot, Dependency Awareness) - can parallelize
10. **Week 15**: 6.29-6.30 (Task Continuity, Design Tokens)
11. **Week 16**: 6.31 (Designer Agent Implementation)
12. **Week 17-18**: 6.32-6.42 (Skill/Agent Integrations) - can parallelize
13. **Week 19**: 6.17-6.18 (Benchmarks, Integration Tests) - validates all optimizations

**Note**: Weeks are relative units of work, not calendar estimates.

**XOR Filter Integration Points:**
- 6.19 must be integrated with search skills from 6.13-6.16
- All search skills gain internal XOR early exit
- `search_all` multi-domain skill added
- Inline domain hints added to responses

**Agent Efficiency Integration Points:**
- 6.31 (Designer) can be parallelized with 6.21-6.30
- 6.33-6.42 depend on 6.21-6.30 (skills must exist before integration)
- See section 6.32 for full agent-to-technique mapping

---

## Agent Efficiency Techniques

### 6.21 Scratchpad Memory

Within-session working memory for agents to track notes, reasoning, and temporary state.

**Files to create:**
- `agents/common/scratchpad.go`
- `agents/common/scratchpad_test.go`
- `skills/scratchpad_skill.go`

**Acceptance Criteria:**

#### Scratchpad Data Structure
- [ ] `ScratchpadEntry` with key, value, category, timestamp, expiry
- [ ] `Scratchpad` manager with `Set()`, `Get()`, `Delete()`, `GetByCategory()`, `All()`
- [ ] Session-scoped (entries tied to session ID)
- [ ] Auto-cleanup on session end
- [ ] Thread-safe with RWMutex

```go
type ScratchpadEntry struct {
    Key       string    `json:"key"`
    Value     string    `json:"value"`
    Category  string    `json:"category"`
    CreatedAt time.Time `json:"created_at"`
    ExpiresAt time.Time `json:"expires_at,omitempty"`
}
```

#### Scratchpad Skills
- [ ] `scratchpad_set` - Store a note (key, value, category optional)
- [ ] `scratchpad_get` - Retrieve a note by key
- [ ] `scratchpad_list` - List all notes (optional category filter)
- [ ] `scratchpad_delete` - Remove a note by key

#### Common Use Cases
- [ ] "Requirements noted from user" category
- [ ] "Partial work" category for intermediate results
- [ ] "Decisions made" category for reasoning trail
- [ ] "Blockers found" category for issues

**Tests:**
- [ ] Set and get works correctly
- [ ] Category filtering works
- [ ] Expiry removes old entries
- [ ] Session isolation (can't access other session's notes)
- [ ] Thread-safe under concurrent access

---

### 6.22 Code Style Inference

Automatic detection of project code style from existing files.

**Files to create:**
- `agents/engineer/style_inference.go`
- `agents/engineer/style_inference_test.go`
- `skills/style_check_skill.go`

**Acceptance Criteria:**

#### Style Analysis
- [ ] `InferredStyle` struct with naming, formatting, patterns
- [ ] `StyleInferrer.Analyze()` reads sample files
- [ ] Detect naming conventions (camelCase, snake_case, PascalCase)
- [ ] Detect indentation (tabs vs spaces, indent width)
- [ ] Detect quote style (single vs double)
- [ ] Detect trailing comma preference
- [ ] Detect import organization pattern

```go
type InferredStyle struct {
    Language       string              `json:"language"`
    NamingConvention struct {
        Variables string `json:"variables"` // "camelCase", "snake_case"
        Functions string `json:"functions"`
        Types     string `json:"types"`
        Constants string `json:"constants"` // "SCREAMING_SNAKE"
    } `json:"naming_convention"`
    Formatting struct {
        IndentStyle string `json:"indent_style"` // "tabs", "spaces"
        IndentSize  int    `json:"indent_size"`
        QuoteStyle  string `json:"quote_style"` // "single", "double"
        TrailingComma bool `json:"trailing_comma"`
    } `json:"formatting"`
    Patterns struct {
        ErrorHandling string `json:"error_handling"` // "early_return", "nested"
        Imports       string `json:"imports"`        // "grouped", "ungrouped"
    } `json:"patterns"`
}
```

#### Style Check Skill
- [ ] `check_style` skill returns project style for language
- [ ] Caches inferred styles per project/language
- [ ] Updates when new files detected
- [ ] Returns actionable guidance

**Tests:**
- [ ] Correctly detects camelCase vs snake_case
- [ ] Correctly detects tabs vs spaces
- [ ] Handles mixed styles (reports majority)
- [ ] Cache invalidation on new files
- [ ] Multi-language project support

---

### 6.23 Component/Pattern Registry

Index of reusable UI components, hooks, utilities, and patterns in the project.

**Files to create:**
- `agents/engineer/component_registry.go`
- `agents/engineer/component_registry_test.go`
- `skills/find_component_skill.go`

**Acceptance Criteria:**

#### Registry Data Structure
- [ ] `RegisteredComponent` with name, type, file path, exports, props/params
- [ ] `ComponentRegistry` with `Register()`, `Find()`, `FindByType()`, `GetAll()`
- [ ] Auto-scan project for components on startup
- [ ] Watch for file changes and update registry

```go
type RegisteredComponent struct {
    Name        string            `json:"name"`
    Type        string            `json:"type"`  // "component", "hook", "utility", "pattern"
    FilePath    string            `json:"file_path"`
    Exports     []string          `json:"exports"`
    Props       []ComponentProp   `json:"props,omitempty"`
    Description string            `json:"description,omitempty"`
    Examples    []string          `json:"examples,omitempty"`
}

type ComponentProp struct {
    Name     string `json:"name"`
    Type     string `json:"type"`
    Required bool   `json:"required"`
    Default  string `json:"default,omitempty"`
}
```

#### Component Scanning
- [ ] Scan React/Vue/Svelte component files
- [ ] Extract exported names
- [ ] Parse props/parameters
- [ ] Detect custom hooks (use* pattern)
- [ ] Detect utility functions

#### Find Component Skill
- [ ] `find_component` - Search by name or purpose
- [ ] Returns existing components that match need
- [ ] Includes usage examples from codebase
- [ ] Warns before suggesting new component that exists

**Tests:**
- [ ] Correctly finds React components
- [ ] Extracts props from TypeScript interfaces
- [ ] Detects hooks correctly
- [ ] Search by partial name works
- [ ] Updates on file change

---

### 6.24 Mistake Memory

Cross-session learning from errors and anti-patterns.

**Files to create:**
- `agents/common/mistake_memory.go`
- `agents/common/mistake_memory_test.go`
- `skills/recall_mistakes_skill.go`

**Acceptance Criteria:**

#### Mistake Data Structure
- [ ] `RecordedMistake` with pattern, context, resolution, occurrences
- [ ] `MistakeMemory` with `Record()`, `Recall()`, `RecallByCategory()`
- [ ] Persistent storage (SQLite)
- [ ] Relevance scoring (recent + frequent = higher priority)

```go
type RecordedMistake struct {
    ID         string    `json:"id"`
    Pattern    string    `json:"pattern"`     // What went wrong
    Category   string    `json:"category"`    // "syntax", "logic", "import", "api_misuse"
    Context    string    `json:"context"`     // Where/when it happened
    Resolution string    `json:"resolution"`  // How to fix
    Occurrences int      `json:"occurrences"`
    FirstSeen  time.Time `json:"first_seen"`
    LastSeen   time.Time `json:"last_seen"`
    FilePaths  []string  `json:"file_paths"`  // Where it occurred
}
```

#### Mistake Recording
- [ ] Automatic recording from failed builds/tests
- [ ] Manual recording via skill
- [ ] Increment occurrences on repeat
- [ ] Extract patterns from error messages

#### Recall Skill
- [ ] `recall_mistakes` - Get relevant past mistakes
- [ ] Accepts optional category filter
- [ ] Returns most relevant (recent + frequent) first
- [ ] Used by Engineer before writing similar code

**Tests:**
- [ ] Records mistakes correctly
- [ ] Increments occurrences on repeat
- [ ] Persistence across restarts
- [ ] Relevance scoring works
- [ ] Category filtering works

---

### 6.25 Diff Preview

Preview changes before applying them.

**Files to create:**
- `agents/engineer/diff_preview.go`
- `agents/engineer/diff_preview_test.go`
- `skills/diff_preview_skill.go`

**Acceptance Criteria:**

#### Diff Generation
- [ ] `DiffPreview` with original, proposed, unified diff, stats
- [ ] Generate unified diff format
- [ ] Calculate stats (lines added, removed, changed)
- [ ] Support multi-file diffs
- [ ] Syntax highlighting hints

```go
type DiffPreview struct {
    FilePath    string `json:"file_path"`
    Original    string `json:"original,omitempty"`
    Proposed    string `json:"proposed,omitempty"`
    UnifiedDiff string `json:"unified_diff"`
    Stats       DiffStats `json:"stats"`
}

type DiffStats struct {
    LinesAdded   int `json:"lines_added"`
    LinesRemoved int `json:"lines_removed"`
    LinesChanged int `json:"lines_changed"`
    Hunks        int `json:"hunks"`
}
```

#### Preview Skill
- [ ] `preview_diff` - Show what changes would be made
- [ ] Accepts file path and proposed content
- [ ] Returns unified diff and stats
- [ ] Can preview multiple files at once

#### Validation
- [ ] Flag potentially dangerous changes (>100 lines removed)
- [ ] Flag changes to sensitive files (.env, credentials)
- [ ] Suggest review for large refactors

**Tests:**
- [ ] Unified diff format correct
- [ ] Stats calculation accurate
- [ ] Multi-file preview works
- [ ] Sensitive file detection works
- [ ] Large change warning triggers

---

### 6.26 User Preference Learning

Learn and apply user preferences over time.

**Files to create:**
- `agents/common/preference_learning.go`
- `agents/common/preference_learning_test.go`
- `skills/preferences_skill.go`

**Acceptance Criteria:**

#### Preference Data Structure
- [ ] `UserPreference` with domain, key, value, confidence, source
- [ ] `PreferenceStore` with `Learn()`, `Get()`, `GetAll()`
- [ ] Persistent storage (SQLite)
- [ ] Confidence increases with repeated signals

```go
type UserPreference struct {
    Domain     string    `json:"domain"`      // "code_style", "communication", "workflow"
    Key        string    `json:"key"`         // "verbosity", "test_framework"
    Value      string    `json:"value"`       // "concise", "jest"
    Confidence float64   `json:"confidence"`  // 0.0-1.0
    Source     string    `json:"source"`      // "explicit", "implicit", "inferred"
    LearnedAt  time.Time `json:"learned_at"`
    SeenCount  int       `json:"seen_count"`
}
```

#### Learning Sources
- [ ] Explicit: User says "I prefer X"
- [ ] Implicit: User accepts/rejects suggestions
- [ ] Inferred: Patterns from code edits

#### Preference Application
- [ ] `get_preferences` - Retrieve preferences for domain
- [ ] `set_preference` - Explicitly set a preference
- [ ] Preferences influence response generation
- [ ] Confidence-weighted (high confidence = strong preference)

**Tests:**
- [ ] Explicit preferences stored correctly
- [ ] Confidence increases on repeated signals
- [ ] Implicit learning from accept/reject
- [ ] Persistence across sessions
- [ ] Domain filtering works

---

### 6.27 File Snapshot Cache

Efficient caching of file states to avoid re-reading.

**Files to create:**
- `agents/common/file_snapshot.go`
- `agents/common/file_snapshot_test.go`

**Acceptance Criteria:**

#### Snapshot Data Structure
- [ ] `FileSnapshot` with path, content hash, last modified, content
- [ ] `SnapshotCache` with `Get()`, `Invalidate()`, `InvalidateAll()`
- [ ] Content-addressable (hash-based)
- [ ] LRU eviction when over capacity

```go
type FileSnapshot struct {
    Path         string    `json:"path"`
    ContentHash  string    `json:"content_hash"`
    LastModified time.Time `json:"last_modified"`
    Content      []byte    `json:"-"`
    LineCount    int       `json:"line_count"`
    Size         int64     `json:"size"`
}

type SnapshotCache struct {
    maxSize    int64
    currentSize int64
    snapshots  map[string]*FileSnapshot
    lru        *list.List
    mu         sync.RWMutex
}
```

#### Cache Operations
- [ ] Check modified time before returning cached
- [ ] Automatic invalidation on file change detection
- [ ] Pre-warm cache for frequently accessed files
- [ ] Track access patterns for eviction

#### Integration
- [ ] File read operations use cache
- [ ] Invalidate on write operations
- [ ] Stats: hits, misses, evictions

**Tests:**
- [ ] Cache hit returns correct content
- [ ] Modified file triggers re-read
- [ ] LRU eviction works correctly
- [ ] Write invalidates cache
- [ ] Concurrent access safe

---

### 6.28 Dependency Awareness

Track project dependencies and their APIs.

**Files to create:**
- `agents/engineer/dependency_awareness.go`
- `agents/engineer/dependency_awareness_test.go`
- `skills/check_dependency_skill.go`

**Acceptance Criteria:**

#### Dependency Tracking
- [ ] `TrackedDependency` with name, version, import path, common APIs
- [ ] `DependencyTracker` with `Scan()`, `Get()`, `GetAll()`
- [ ] Parse package.json, go.mod, requirements.txt, etc.
- [ ] Track which dependencies are actually used

```go
type TrackedDependency struct {
    Name        string   `json:"name"`
    Version     string   `json:"version"`
    ImportPath  string   `json:"import_path"`
    CommonAPIs  []string `json:"common_apis"`
    UsedIn      []string `json:"used_in"`      // File paths
    DevOnly     bool     `json:"dev_only"`
    Deprecated  bool     `json:"deprecated,omitempty"`
}
```

#### API Awareness
- [ ] Index common APIs from frequently-used deps
- [ ] Detect deprecated API usage
- [ ] Suggest correct imports
- [ ] Version compatibility warnings

#### Check Dependency Skill
- [ ] `check_dependency` - Get info about a dependency
- [ ] Returns version, common APIs, usage examples
- [ ] Warns about deprecated/insecure versions

**Tests:**
- [ ] Parses package.json correctly
- [ ] Parses go.mod correctly
- [ ] Tracks usage across files
- [ ] Detects deprecated APIs
- [ ] Version parsing correct

---

### 6.29 Task Continuity

Checkpoint and resume interrupted tasks.

**Files to create:**
- `agents/common/task_continuity.go`
- `agents/common/task_continuity_test.go`
- `skills/checkpoint_skill.go`

**Acceptance Criteria:**

#### Checkpoint Data Structure
- [ ] `TaskCheckpoint` with task ID, description, progress, state, files modified
- [ ] `ContinuityManager` with `Checkpoint()`, `Resume()`, `List()`, `Clear()`
- [ ] Persistent storage (JSON files or SQLite)
- [ ] Auto-checkpoint on significant progress

```go
type TaskCheckpoint struct {
    ID            string            `json:"id"`
    TaskDescription string          `json:"task_description"`
    Progress      TaskProgress      `json:"progress"`
    State         map[string]any    `json:"state"`
    FilesModified []string          `json:"files_modified"`
    CreatedAt     time.Time         `json:"created_at"`
    UpdatedAt     time.Time         `json:"updated_at"`
}

type TaskProgress struct {
    TotalSteps     int      `json:"total_steps"`
    CompletedSteps int      `json:"completed_steps"`
    CurrentStep    string   `json:"current_step"`
    NextSteps      []string `json:"next_steps"`
    Blockers       []string `json:"blockers,omitempty"`
}
```

#### Checkpoint Skills
- [ ] `checkpoint_task` - Save current progress
- [ ] `resume_task` - Resume from checkpoint
- [ ] `list_checkpoints` - Show incomplete tasks
- [ ] Auto-checkpoint on multi-step tasks

#### Resume Logic
- [ ] Restore state from checkpoint
- [ ] Validate files haven't changed significantly
- [ ] Generate handoff context for agent
- [ ] Clear checkpoint on task completion

**Tests:**
- [ ] Checkpoint saves all state
- [ ] Resume restores correctly
- [ ] File change detection works
- [ ] Persistence across restarts
- [ ] Auto-checkpoint triggers correctly

---

### 6.30 Design Token Awareness

Track and apply design system tokens.

**Files to create:**
- `agents/designer/design_tokens.go`
- `agents/designer/design_tokens_test.go`
- `skills/design_token_skill.go`

**Acceptance Criteria:**

#### Token Data Structure
- [ ] `DesignToken` with category, name, value, usage context
- [ ] `DesignTokenRegistry` with `Get()`, `GetByCategory()`, `Suggest()`
- [ ] Parse from CSS variables, Tailwind config, theme files
- [ ] Auto-scan project for design system

```go
type DesignToken struct {
    Category string `json:"category"` // "color", "spacing", "typography", "shadow"
    Name     string `json:"name"`     // "primary", "md", "heading-1"
    Value    string `json:"value"`    // "#3b82f6", "16px", "24px"
    Usage    string `json:"usage"`    // "Primary brand color for buttons and links"
    CSSVar   string `json:"css_var,omitempty"`  // "--color-primary"
    Tailwind string `json:"tailwind,omitempty"` // "text-primary"
}
```

#### Token Discovery
- [ ] Scan CSS custom properties (--var-name)
- [ ] Parse Tailwind theme config
- [ ] Parse design system JSON/JS exports
- [ ] Map values to semantic names

#### Design Token Skill
- [ ] `get_design_token` - Get token by category/name
- [ ] `suggest_token` - Find token for a use case
- [ ] Returns value, CSS var, Tailwind class if available
- [ ] Warns when using raw value instead of token

**Tests:**
- [ ] Parses CSS variables correctly
- [ ] Parses Tailwind config correctly
- [ ] Suggestion finds appropriate tokens
- [ ] Multiple design system formats supported
- [ ] Raw value warning triggers

---

## Agent Efficiency Token Savings

### Additional Savings from Efficiency Techniques

| Technique | Savings Per Use | Frequency | Monthly Impact |
|-----------|-----------------|-----------|----------------|
| **Scratchpad** | ~50 tokens (avoids re-explain) | ~20% of queries | ~2% |
| **Style Inference** | ~100 tokens (no style questions) | ~15% of edits | ~1.5% |
| **Component Registry** | ~200 tokens (avoids duplication) | ~10% of UI work | ~1% |
| **Mistake Memory** | ~150 tokens (avoids repeat errors) | ~8% of builds | ~1% |
| **Diff Preview** | ~100 tokens (confident changes) | ~25% of edits | ~1.5% |
| **Preference Learning** | ~75 tokens (no clarification) | ~30% of queries | ~2% |
| **File Snapshot** | ~50 tokens (no re-read) | ~40% of file ops | ~1.5% |
| **Dependency Awareness** | ~100 tokens (correct imports) | ~15% of edits | ~1% |
| **Task Continuity** | ~300 tokens (resume vs restart) | ~5% of sessions | ~0.5% |
| **Design Tokens** | ~75 tokens (correct values) | ~20% of UI work | ~0.5% |
| **TOTAL** | | | **~12.5%** |

### Updated Cost Projections

| Scenario | Monthly Cost | vs Baseline |
|----------|-------------|-------------|
| Baseline (no optimization) | $285/month | - |
| Cache only | $94/month | 67% savings |
| Cache + VectorGraphDB | $72/month | 75% savings |
| Cache + VectorGraphDB + XOR | $60/month | ~80% savings |
| **+ Agent Efficiency** | **$45/month** | **~85% savings** |

**Target**: 85% token reduction with all optimizations enabled.

---

## Skill/Agent Integrations

### 6.31 Designer Agent Implementation

New agent for UI/UX implementation tasks.

**Files to create:**
- `agents/designer/designer.go`
- `agents/designer/designer_test.go`
- `agents/designer/skills.go`

**Acceptance Criteria:**

#### Core Designer Agent
- [ ] Designer agent struct with standard agent interface
- [ ] Session-scoped state for design context
- [ ] Integration with Guide for routing
- [ ] LLM model selection (Sonnet for speed)

#### Designer Skills (Tier 1 - Core)
- [ ] `create_component` - Create new UI component
- [ ] `style_component` - Apply styling to component
- [ ] `get_design_tokens` - Retrieve design system tokens
- [ ] `find_component` - Find existing UI components
- [ ] `preview_ui` - Preview UI changes in isolation
- [ ] `check_accessibility` - Check a11y compliance

#### Designer Skills (Tier 2 - Contextual)
- [ ] `analyze_layout` - Suggest layout improvements
- [ ] `suggest_animation` - Suggest appropriate animations
- [ ] `audit_consistency` - Audit design consistency
- [ ] `generate_variants` - Generate component variants
- [ ] `consult_engineer` - Consult on implementation
- [ ] `consult_academic` - Research UI/UX best practices

#### Designer Skills (Tier 3 - Specialized)
- [ ] `design_system_scaffold` - Scaffold complete design system
- [ ] `theme_migration` - Migrate between themes
- [ ] `responsive_audit` - Full responsive audit

#### Guide Routing Integration
- [ ] Route UI/UX intents to Designer
- [ ] Keywords: "component", "ui", "style", "css", "tailwind", "design", "a11y"
- [ ] Handoff patterns between Designer and Engineer

**Tests:**
- [ ] Designer agent responds to UI intents
- [ ] All core skills work correctly
- [ ] Guide routes to Designer appropriately
- [ ] Designer/Engineer handoff works

---

### 6.32 Skill/Agent Integration Matrix

Integrate efficiency techniques with appropriate agents per the mapping.

**Reference Matrix:**

| Technique              | Guide | Architect | Engineer | Inspector | Tester | Designer |
|------------------------|-------|-----------|----------|-----------|--------|----------|
| Scratchpad Memory      |   -   |     ✓     |    ✓     |     ✓     |   ✓    |    ✓     |
| Style Inference        |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Component Registry     |   -   |     -     |    ✓     |     -     |   -    |    ✓     |
| Mistake Memory         |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Diff Preview           |   -   |     -     |    ✓     |     -     |   -    |    ✓     |
| User Preferences       |   ✓   |     ✓     |    ✓     |     -     |   -    |    ✓     |
| File Snapshot Cache    |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Dependency Awareness   |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Task Continuity        |   -   |     ✓     |    ✓     |     -     |   ✓    |    -     |
| Design Token Awareness |   -   |     -     |    ✓     |     ✓     |   -    |    ✓     |

---

### 6.33 Scratchpad Integration

Integrate scratchpad skills with: **Architect, Engineer, Inspector, Tester, Designer**

**Files to modify:**
- `agents/architect/skills.go` - Add scratchpad skills
- `agents/engineer/skills.go` - Add scratchpad skills
- `agents/inspector/skills.go` - Add scratchpad skills
- `agents/tester/skills.go` - Add scratchpad skills
- `agents/designer/skills.go` - Add scratchpad skills

**Acceptance Criteria:**
- [ ] `scratchpad_write` skill available to Architect
- [ ] `scratchpad_write` skill available to Engineer
- [ ] `scratchpad_write` skill available to Inspector
- [ ] `scratchpad_write` skill available to Tester
- [ ] `scratchpad_write` skill available to Designer
- [ ] `scratchpad_read` skill available to all above
- [ ] Session-scoped isolation verified
- [ ] Agent-specific suggested keys documented

**Suggested Keys by Agent:**
- **Architect**: `requirements`, `decisions`, `blockers`, `plan_state`
- **Engineer**: `current_task`, `tried_failed`, `partial_work`
- **Inspector**: `issues_found`, `review_notes`, `flagged_patterns`
- **Tester**: `test_cases`, `coverage_gaps`, `flaky_tests`
- **Designer**: `design_decisions`, `component_notes`, `accessibility_flags`

---

### 6.34 Style Inference Integration

Integrate style inference with: **Engineer, Inspector, Tester**

**Files to modify:**
- `agents/engineer/skills.go` - Add style check skill
- `agents/inspector/skills.go` - Add style check skill
- `agents/tester/skills.go` - Add style check skill

**Acceptance Criteria:**
- [ ] `check_style` skill available to Engineer
- [ ] `check_style` skill available to Inspector
- [ ] `check_style` skill available to Tester
- [ ] Engineer uses style before writing code
- [ ] Inspector compares against detected style
- [ ] Tester matches test style to project style

---

### 6.35 Component Registry Integration

Integrate component registry with: **Engineer, Designer**

**Files to modify:**
- `agents/engineer/skills.go` - Add find_component skill
- `agents/designer/skills.go` - Add find_component skill

**Acceptance Criteria:**
- [ ] `find_component` skill available to Engineer
- [ ] `find_component` skill available to Designer
- [ ] Auto-suggest existing components before creating new
- [ ] Warn when creating duplicate component
- [ ] Designer priority access (primary user)

---

### 6.36 Mistake Memory Integration

Integrate mistake memory with: **Engineer, Inspector, Tester** (Archivalist as source)

**Files to modify:**
- `agents/engineer/skills.go` - Add recall_mistakes skill
- `agents/inspector/skills.go` - Add recall_mistakes skill
- `agents/tester/skills.go` - Add recall_mistakes skill
- `agents/archivalist/skills.go` - Add mistake storage/retrieval

**Acceptance Criteria:**
- [ ] `recall_mistakes` skill available to Engineer
- [ ] `recall_mistakes` skill available to Inspector
- [ ] `recall_mistakes` skill available to Tester
- [ ] Archivalist records mistakes from failed builds/tests
- [ ] Engineer queries before similar work
- [ ] Inspector flags known anti-patterns
- [ ] Tester knows common failure patterns

---

### 6.37 Diff Preview Integration

Integrate diff preview with: **Engineer, Designer**

**Files to modify:**
- `agents/engineer/skills.go` - Add preview_diff skill
- `agents/designer/skills.go` - Add preview_diff skill

**Acceptance Criteria:**
- [ ] `preview_diff` skill available to Engineer
- [ ] `preview_diff` skill available to Designer
- [ ] Preview before large changes (>50 lines)
- [ ] Warn on sensitive file changes
- [ ] Stats: lines added/removed/changed

---

### 6.38 User Preferences Integration

Integrate user preferences with: **Guide, Architect, Engineer, Designer**

**Files to modify:**
- `agents/guide/skills.go` - Add preference skills
- `agents/architect/skills.go` - Add preference skills
- `agents/engineer/skills.go` - Add preference skills
- `agents/designer/skills.go` - Add preference skills

**Acceptance Criteria:**
- [ ] `get_preferences` skill available to Guide
- [ ] `get_preferences` skill available to Architect
- [ ] `get_preferences` skill available to Engineer
- [ ] `get_preferences` skill available to Designer
- [ ] Guide routes based on workflow preferences
- [ ] Architect adjusts verbosity per preference
- [ ] Engineer follows code style preferences
- [ ] Designer follows design style preferences

---

### 6.39 File Snapshot Cache Integration

Integrate file snapshot cache with: **Engineer, Inspector, Tester** (Librarian as source)

**Files to modify:**
- `agents/engineer/file_ops.go` - Use snapshot cache
- `agents/inspector/file_ops.go` - Use snapshot cache
- `agents/tester/file_ops.go` - Use snapshot cache
- `agents/librarian/indexer.go` - Populate snapshot cache

**Acceptance Criteria:**
- [ ] Engineer reads use snapshot cache
- [ ] Inspector reads use snapshot cache
- [ ] Tester reads use snapshot cache
- [ ] Librarian populates cache during indexing
- [ ] Cache invalidation on write operations
- [ ] Stats: cache hits, misses, evictions

---

### 6.40 Dependency Awareness Integration

Integrate dependency awareness with: **Engineer, Inspector, Tester** (Academic as source)

**Files to modify:**
- `agents/engineer/skills.go` - Add check_dependency skill
- `agents/inspector/skills.go` - Add check_dependency skill
- `agents/tester/skills.go` - Add check_dependency skill
- `agents/academic/skills.go` - Provide dependency best practices

**Acceptance Criteria:**
- [ ] `check_dependency` skill available to Engineer
- [ ] `check_dependency` skill available to Inspector
- [ ] `check_dependency` skill available to Tester
- [ ] Engineer uses correct imports
- [ ] Inspector flags deprecated deps
- [ ] Tester mocks dependencies correctly
- [ ] Academic provides version/security info

---

### 6.41 Task Continuity Integration

Integrate task continuity with: **Architect, Engineer, Tester**

**Files to modify:**
- `agents/architect/skills.go` - Add checkpoint skills
- `agents/engineer/skills.go` - Add checkpoint skills
- `agents/tester/skills.go` - Add checkpoint skills

**Acceptance Criteria:**
- [ ] `checkpoint_task` skill available to Architect
- [ ] `checkpoint_task` skill available to Engineer
- [ ] `checkpoint_task` skill available to Tester
- [ ] `resume_task` skill available to all above
- [ ] Auto-checkpoint on significant progress
- [ ] Resume generates handoff context

---

### 6.42 Design Token Integration

Integrate design tokens with: **Engineer, Inspector, Designer**

**Files to modify:**
- `agents/engineer/skills.go` - Add design token skills
- `agents/inspector/skills.go` - Add design token skills
- `agents/designer/skills.go` - Add design token skills

**Acceptance Criteria:**
- [ ] `get_design_tokens` skill available to Engineer
- [ ] `get_design_tokens` skill available to Inspector
- [ ] `get_design_tokens` skill available to Designer
- [ ] `find_design_token` skill available to all above
- [ ] Engineer uses tokens instead of hardcoded values
- [ ] Inspector flags hardcoded values
- [ ] Designer primary user of tokens

---

## Updated Implementation Order

1. **Week 1-2**: 6.1 (Schema), 6.2 (HNSW) - can parallelize
2. **Week 3**: 6.3 (Nodes/Edges), 6.4 (Search/Traversal) - can parallelize
3. **Week 4-5**: 6.5-6.11 (All Mitigations) - can parallelize
4. **Week 6**: 6.12 (Unified Resolver)
5. **Week 7-8**: 6.13-6.16 (Agent Integrations) - can parallelize
6. **Week 9**: 6.19 (XOR Filter Optimization)
7. **Week 10**: 6.20 (Architect Planning Preflight)
8. **Week 11-12**: 6.21-6.24 (Scratchpad, Style Inference, Component Registry, Mistake Memory)
9. **Week 13-14**: 6.25-6.28 (Diff Preview, Preferences, File Snapshot, Dependency Awareness)
10. **Week 15**: 6.29-6.30 (Task Continuity, Design Tokens)
11. **Week 16**: 6.31 (Designer Agent Implementation)
12. **Week 17-18**: 6.32-6.42 (Skill/Agent Integrations) - can parallelize
13. **Week 19**: 6.17-6.18 (Benchmarks, Integration Tests) - validates all optimizations

**Note**: Weeks are relative units of work, not calendar estimates.

**Integration Dependencies:**
- 6.33-6.42 depend on 6.21-6.30 (skills must exist before integration)
- 6.31 (Designer) can be parallelized with 6.21-6.30
- 6.32 (Matrix) is a reference, not implementation
- 6.42 (Design Tokens) depends on 6.31 (Designer agent)
