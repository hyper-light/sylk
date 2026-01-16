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
- [ ] `DAGNode` with ID, agent type, prompt, context, dependencies, metadata
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

#### Guide Skills (Progressive Disclosure)

**Core Skills (always loaded):**
- [ ] `route` skill - classify and route requests (supports @to:agent syntax)
- [ ] `guide_route` skill - route prompt to specific agent via @guide:<agent> tag
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

#### Guide Hooks
- [ ] Pre-route hook: inject session context
- [ ] Post-route hook: update session state
- [ ] Pre-dispatch hook: validate target agent health
- [ ] Post-dispatch hook: record routing decision

**Tests:**
- [ ] Route requests with session context
- [ ] Verify session isolation in route cache
- [ ] Test new message types round-trip
- [ ] Test skill loading on demand

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

### 0.6 Bus Enhancements

Extend event bus for session-scoped topics and wildcards.

**Files to modify:**
- `agents/guide/bus.go`
- `agents/guide/channel_bus.go`
- `agents/guide/scalable_bus.go`

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

**Tests:**
- [ ] Publish/subscribe with session topics
- [ ] Wildcard subscription matching
- [ ] Message filtering by session

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

#### Librarian Hooks
- [ ] Pre-query hook: log query for analytics
- [ ] Post-query hook: cache frequently accessed files
- [ ] Pre-index hook: filter excluded paths
- [ ] Post-index hook: notify index updated event

**Tests:**
- [ ] Index a test codebase
- [ ] Query for patterns, verify results
- [ ] Test incremental update
- [ ] Test skill loading on demand

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

#### Academic Hooks
- [ ] Pre-research hook: check Archivalist for cached research
- [ ] Post-research hook: cache results in Archivalist
- [ ] Pre-ingest hook: validate source accessibility
- [ ] Post-ingest hook: trigger embedding generation

**Tests:**
- [ ] Ingest test sources
- [ ] Research query, verify output
- [ ] Test caching in Archivalist

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

**Tests:**
- [ ] Execute file read/write task
- [ ] Test consultation flow
- [ ] Test help request routing

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
- [ ] Dispatch nodes to Engineers via Guide (not direct)
- [ ] Correlate responses by correlation ID
- [ ] Track node state and timing

#### Engineer Management
- [ ] Create Engineers on demand (up to limit)
- [ ] Pool Engineers for reuse
- [ ] Destroy idle Engineers after timeout
- [ ] Session-scoped Engineer pool

#### Status Propagation
- [ ] Status updates to Architect
- [ ] Per-node progress events
- [ ] Overall DAG progress (layers completed / total)
- [ ] Estimated time remaining

#### Quality Loop
- [ ] Per-task validation: send to Inspector after each task
- [ ] Full validation: signal Inspector when DAG complete
- [ ] Test signal: signal Tester when ready for testing
- [ ] Handle corrections from Inspector/Tester

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

#### Architect Hooks
- [ ] Pre-plan hook: load session context from Archivalist
- [ ] Post-plan hook: store plan in Archivalist
- [ ] Pre-execute hook: confirm user approval
- [ ] Post-execute hook: update session state
- [ ] Pre-interrupt hook: pause current work gracefully
- [ ] Post-complete hook: generate and store summary

**Tests:**
- [ ] Generate plan from request
- [ ] Verify DAG structure
- [ ] Test user modification flow
- [ ] Test interrupt handling
- [ ] Test clarification routing

---

## Phase 4: Quality Agents

**Goal**: Build validation and testing agents with comprehensive skills.

**Dependencies**: Phase 3 complete

**Parallelization**: Items 4.1 and 4.2 can execute in parallel.

### 4.1 Inspector (Code Validator)

Validates code quality and implementation correctness.

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
- `agents/inspector/inspector_test.go`

**Acceptance Criteria:**

#### Per-Task Validation
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

**Tests:**
- [ ] Validate test code with known issues
- [ ] Verify issue detection
- [ ] Test override flow
- [ ] Test corrections generation

### 4.2 Tester (Test Planner & Executor)

Plans and executes tests, analyzes failures.

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
- `agents/tester/tester_test.go`

**Acceptance Criteria:**

#### Test Planning
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

**Tests:**
- [ ] Generate test plan
- [ ] Execute tests
- [ ] Analyze failures
- [ ] Test skip conditions

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

**Acceptance Criteria:**
- [ ] Test: User request → Architect → DAG → Engineers → Inspector → Tester → Complete
- [ ] Test: Multiple sessions executing concurrently
- [ ] Test: Session isolation (no context pollution)
- [ ] Test: Session switching with state preservation
- [ ] Test: Engineer clarification loop
- [ ] Test: Inspector rejection → fix workflow → re-validate
- [ ] Test: Tester failure → fix workflow → re-test
- [ ] Test: User interruption during execution
- [ ] Test: Agent failure recovery
- [ ] Test: Session cleanup on completion
- [ ] Test: Skill progressive loading
- [ ] Test: Cross-session Archivalist queries

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
Phase 0 (Foundation)              Phase 1 (Knowledge)      Phase 2 (Execution)      Phase 3         Phase 4 (Quality)      Phase 5
┌─────────────────────────────┐   ┌─────────────────┐      ┌─────────────────┐      ┌───────────┐   ┌────────────────┐      ┌──────────────────┐
│ 0.1 Session Manager         │   │ 1.1 Librarian   │      │ 2.1 Engineer    │      │ 3.1       │   │ 4.1 Inspector  │      │ 5.1 Coordinator  │
│ 0.2 DAG Engine              │   │ 1.2 Academic    │      │ 2.2 Orchestrator│      │ Architect │   │ 4.2 Tester     │      │ 5.2 Integration  │
│ 0.3 Worker Pool Enhancements│──▶│                 │─────▶│                 │─────▶│           │──▶│                │─────▶│ 5.3 Benchmarks   │
│ 0.4 Guide Enhancements      │   │                 │      │                 │      │           │   │                │      │                  │
│ 0.5 Archivalist Enhancements│   │                 │      │                 │      │           │   │                │      │                  │
│ 0.6 Bus Enhancements        │   │                 │      │                 │      │           │   │                │      │                  │
└─────────────────────────────┘   └─────────────────┘      └─────────────────┘      └───────────┘   └────────────────┘      └──────────────────┘
```

### Parallelization Summary

| Phase | Items | Parallel Execution |
|-------|-------|-------------------|
| 0 | 0.1, 0.2, 0.3, 0.4, 0.5, 0.6 | All six in parallel |
| 1 | 1.1, 1.2 | Both in parallel (after Phase 0) |
| 2 | 2.1, 2.2 | Both in parallel (after Phase 1) |
| 3 | 3.1 | Sequential (after Phase 2) |
| 4 | 4.1, 4.2 | Both in parallel (after Phase 3) |
| 5 | 5.1, 5.2, 5.3 | Sequential (after Phase 4) |

### Critical Path

```
0.1 (Session) → 0.4 (Guide) → 2.2 (Orchestrator) → 3.1 (Architect) → 5.1 (Coordinator)
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
