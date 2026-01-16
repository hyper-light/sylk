# Sylk Architecture

## Overview

Sylk is a multi-agent system built in Go. It uses a **central Guide** as the universal message router for **all** inter-agent communication. The system is designed for **high concurrency**: dozens of sessions, hundreds to thousands of subagents, and workflows executed as DAGs (Directed Acyclic Graphs) with explicit execution order.

This document defines:

- Agent roles, responsibilities, and user interaction patterns
- The Guide as universal message router
- **Session management system** (creation, switching, isolation, context preservation)
- Message envelope and routing model
- Knowledge layer (three RAGs: Academic, Librarian, Archivalist)
- DAG planning and execution
- Quality assurance loop (Inspector + Tester)
- **Skills per agent** (progressive disclosure)
- **Skill definitions per agent**
- **LLM hooks per agent**
- State transitions and lifecycle tracking
- Failure handling and recovery
- Concurrency and backpressure
- Token savings model
- Implementation guide

---

## High-Level System Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                                      USER                                           │
└───────────────────────────────────────┬─────────────────────────────────────────────┘
                                        │
                                        ▼
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                             SESSION MANAGER                                          │
│                       (Session Lifecycle Control)                                    │
│                                                                                      │
│   Creates, switches, preserves, and isolates session contexts                       │
│                                                                                      │
└───────────────────────────────────────╦─────────────────────────────────────────────┘
                                        ║
                                        ▼
╔═════════════════════════════════════════════════════════════════════════════════════╗
║                                     GUIDE                                           ║
║                            (Universal Message Router)                               ║
║                                                                                     ║
║   ALL messages flow through here - user requests AND inter-agent communication     ║
║   Session-scoped routing, context injection, and message correlation               ║
║                                                                                     ║
╚═══════════════════════════════════════╦═════════════════════════════════════════════╝
                                        ║
    ┌───────────┬───────────┬───────────╫───────────┬───────────┬───────────┐
    │           │           │           ║           │           │           │
    ▼           ▼           ▼           ▼           ▼           ▼           ▼
┌───────┐ ┌──────────┐ ┌──────────┐ ┌───────┐ ┌──────────┐ ┌──────────┐ ┌───────┐
│ACADEM-│ │ARCHITECT │ │ORCHESTRA-│ │ENGINE-│ │LIBRARIAN │ │ARCHIVAL- │ │INSPECT│
│IC     │ │          │ │TOR       │ │ER(s)  │ │          │ │IST       │ │OR     │
│       │ │          │ │          │ │       │ │          │ │          │ │       │
│Extern-│ │Abstract→ │ │DAG       │ │Task   │ │Local     │ │Historic- │ │Code   │
│al RAG │ │Concrete  │ │Execution │ │Execut-│ │Code RAG  │ │al RAG    │ │Valid- │
│       │ │          │ │          │ │ion    │ │          │ │          │ │ation  │
└───────┘ └──────────┘ └──────────┘ └───────┘ └──────────┘ └──────────┘ └───────┘
                                                                              │
                                                                        ┌─────┴─────┐
                                                                        │  TESTER   │
                                                                        │           │
                                                                        │ Test Plan │
                                                                        │ + Execute │
                                                                        └───────────┘
```

---

## Session Management System

**CRITICAL: Sessions are the fundamental unit of isolation in Sylk. Every operation happens within a session context.**

### Session Principles

1. **Maximal Independence**: Sessions maintain isolated contexts to prevent context pollution
2. **Shared Knowledge Access**: Sessions can query historical data from ANY Archivalist across ALL sessions
3. **No Cross-Contamination**: Active state (current task, blockers, in-progress work) is session-private
4. **Preservation**: Sessions can be suspended and resumed with full context restoration

### Session Architecture

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                            SESSION MANAGEMENT LAYER                                  │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                         SESSION MANAGER                                      │   │
│  │                                                                              │   │
│  │  Responsibilities:                                                           │   │
│  │  ├── Create new sessions with unique IDs                                     │   │
│  │  ├── Switch active session for user                                          │   │
│  │  ├── List all sessions (active, suspended, completed)                        │   │
│  │  ├── Suspend sessions (preserve full context)                                │   │
│  │  ├── Resume sessions (restore full context)                                  │   │
│  │  ├── Close/archive sessions                                                  │   │
│  │  ├── Enforce session isolation                                               │   │
│  │  └── Manage session lifecycle hooks                                          │   │
│  │                                                                              │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                        │                                            │
│          ┌─────────────────────────────┼─────────────────────────────┐              │
│          │                             │                             │              │
│          ▼                             ▼                             ▼              │
│  ┌───────────────┐            ┌───────────────┐            ┌───────────────┐       │
│  │  SESSION A    │            │  SESSION B    │            │  SESSION C    │       │
│  │  (Active)     │            │  (Suspended)  │            │  (Active)     │       │
│  │               │            │               │            │               │       │
│  │ ┌───────────┐ │            │ ┌───────────┐ │            │ ┌───────────┐ │       │
│  │ │ Context   │ │            │ │ Context   │ │            │ │ Context   │ │       │
│  │ │ (Private) │ │            │ │ (Frozen)  │ │            │ │ (Private) │ │       │
│  │ └───────────┘ │            │ └───────────┘ │            │ └───────────┘ │       │
│  │               │            │               │            │               │       │
│  │ ┌───────────┐ │            │ ┌───────────┐ │            │ ┌───────────┐ │       │
│  │ │ Agents    │ │            │ │ Agents    │ │            │ │ Agents    │ │       │
│  │ │ (Scoped)  │ │            │ │ (Paused)  │ │            │ │ (Scoped)  │ │       │
│  │ └───────────┘ │            │ └───────────┘ │            │ └───────────┘ │       │
│  │               │            │               │            │               │       │
│  │ ┌───────────┐ │            │ ┌───────────┐ │            │ ┌───────────┐ │       │
│  │ │ DAG State │ │            │ │ DAG State │ │            │ │ DAG State │ │       │
│  │ │ (Active)  │ │            │ │ (Frozen)  │ │            │ │ (Active)  │ │       │
│  │ └───────────┘ │            │ └───────────┘ │            │ └───────────┘ │       │
│  └───────────────┘            └───────────────┘            └───────────────┘       │
│          │                             │                             │              │
│          └─────────────────────────────┼─────────────────────────────┘              │
│                                        │                                            │
│                                        ▼                                            │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                     SHARED ARCHIVALIST DATABASE                              │   │
│  │                                                                              │   │
│  │  All sessions can READ from:                                                 │   │
│  │  ├── Past decisions (from any session)                                       │   │
│  │  ├── Learned patterns (from any session)                                     │   │
│  │  ├── Historical failures (from any session)                                  │   │
│  │  ├── Codebase changes (from any session)                                     │   │
│  │  └── User preferences (from any session)                                     │   │
│  │                                                                              │   │
│  │  Sessions WRITE to session-scoped partitions:                                │   │
│  │  ├── Entry.SessionID = current session ID                                    │   │
│  │  ├── Enables per-session filtering                                           │   │
│  │  └── Enables cross-session querying                                          │   │
│  │                                                                              │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Session Data Model

```go
// core/session/session.go

type SessionID string

type SessionState string

const (
    SessionStateCreated   SessionState = "created"
    SessionStateActive    SessionState = "active"
    SessionStateSuspended SessionState = "suspended"
    SessionStateCompleted SessionState = "completed"
    SessionStateFailed    SessionState = "failed"
)

// Session represents an isolated working context
type Session struct {
    ID            SessionID              `json:"id"`
    Name          string                 `json:"name,omitempty"`
    State         SessionState           `json:"state"`

    // Timestamps
    CreatedAt     time.Time              `json:"created_at"`
    ActivatedAt   *time.Time             `json:"activated_at,omitempty"`
    SuspendedAt   *time.Time             `json:"suspended_at,omitempty"`
    CompletedAt   *time.Time             `json:"completed_at,omitempty"`
    LastActiveAt  time.Time              `json:"last_active_at"`

    // Context (session-private, isolated)
    Context       *SessionContext        `json:"context"`

    // Workflow state
    ActiveDAGID   string                 `json:"active_dag_id,omitempty"`
    DAGHistory    []string               `json:"dag_history,omitempty"`

    // Git isolation
    BranchName    string                 `json:"branch_name,omitempty"`
    BaseBranch    string                 `json:"base_branch,omitempty"`

    // Resource limits
    Config        SessionConfig          `json:"config"`

    // Metadata
    Metadata      map[string]any         `json:"metadata,omitempty"`
}

// SessionContext holds session-private state (NOT shared across sessions)
type SessionContext struct {
    // Current work state
    CurrentTask       string             `json:"current_task,omitempty"`
    CurrentStep       string             `json:"current_step,omitempty"`
    CurrentObjective  string             `json:"current_objective,omitempty"`
    CompletedSteps    []string           `json:"completed_steps,omitempty"`
    NextSteps         []string           `json:"next_steps,omitempty"`
    Blockers          []string           `json:"blockers,omitempty"`

    // File tracking (session-local)
    ModifiedFiles     map[string]*FileState  `json:"modified_files,omitempty"`
    ReadFiles         map[string]*FileRead   `json:"read_files,omitempty"`
    CreatedFiles      map[string]*FileCreate `json:"created_files,omitempty"`

    // Pattern tracking (session-local discoveries)
    LocalPatterns     []*Pattern            `json:"local_patterns,omitempty"`

    // Failure tracking (session-local)
    LocalFailures     []*Failure            `json:"local_failures,omitempty"`

    // User intents (session-local)
    UserWants         []*Intent             `json:"user_wants,omitempty"`
    UserRejects       []*Intent             `json:"user_rejects,omitempty"`

    // Agent states (which agents are active in this session)
    ActiveAgents      map[string]*AgentState `json:"active_agents,omitempty"`

    // Conversation context
    ConversationID    string                 `json:"conversation_id,omitempty"`
    MessageHistory    []string               `json:"message_history,omitempty"`
}

// SessionConfig configures session resource limits
type SessionConfig struct {
    MaxConcurrentTasks  int           `json:"max_concurrent_tasks"`
    MaxEngineers        int           `json:"max_engineers"`
    TaskTimeout         time.Duration `json:"task_timeout"`
    SessionTimeout      time.Duration `json:"session_timeout"`
    AutoSuspendAfter    time.Duration `json:"auto_suspend_after"`
    EnableGitIsolation  bool          `json:"enable_git_isolation"`
}
```

### Session Manager Interface

```go
// core/session/manager.go

type SessionManager interface {
    // Lifecycle
    Create(ctx context.Context, cfg CreateSessionConfig) (*Session, error)
    Activate(ctx context.Context, id SessionID) error
    Suspend(ctx context.Context, id SessionID) error
    Resume(ctx context.Context, id SessionID) error
    Complete(ctx context.Context, id SessionID, summary string) error
    Close(ctx context.Context, id SessionID) error

    // Queries
    Get(id SessionID) (*Session, bool)
    GetActive() *Session
    GetByState(state SessionState) []*Session
    List() []*Session

    // Context management
    GetContext(id SessionID) (*SessionContext, error)
    UpdateContext(id SessionID, updates func(*SessionContext)) error

    // Switching
    Switch(ctx context.Context, toID SessionID) (*Session, error)

    // Preservation
    Snapshot(id SessionID) (*SessionSnapshot, error)
    Restore(snapshot *SessionSnapshot) (*Session, error)

    // Cross-session queries (delegates to Archivalist)
    QueryHistory(ctx context.Context, query HistoryQuery) ([]*HistoryEntry, error)

    // Resource management
    Stats() SessionManagerStats
    CloseAll() error
}

type CreateSessionConfig struct {
    Name              string
    BaseBranch        string
    EnableGitIsolation bool
    Metadata          map[string]any
    Config            *SessionConfig  // nil = use defaults
}
```

### Session Isolation Rules

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                           SESSION ISOLATION RULES                                    │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ISOLATED (Session-Private):                                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  ✓ Current task, step, objective                                             │   │
│  │  ✓ Completed steps, next steps, blockers                                     │   │
│  │  ✓ Modified/created files (in-progress)                                      │   │
│  │  ✓ Read file tracking                                                        │   │
│  │  ✓ Active DAG state and execution progress                                   │   │
│  │  ✓ Engineer instances and their work                                         │   │
│  │  ✓ Clarification requests pending                                            │   │
│  │  ✓ User intent for THIS session                                              │   │
│  │  ✓ Conversation context                                                      │   │
│  │  ✓ Git branch (if isolation enabled)                                         │   │
│  │                                                                              │   │
│  │  WHY: Prevents one session's in-progress work from polluting another         │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  SHARED (Cross-Session Readable):                                                   │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  ✓ Committed decisions (with session_id for filtering)                       │   │
│  │  ✓ Learned patterns (promoted from session-local)                            │   │
│  │  ✓ Historical failures and resolutions                                       │   │
│  │  ✓ Past workflow outcomes                                                    │   │
│  │  ✓ User preferences (promoted as global)                                     │   │
│  │  ✓ Codebase patterns from Librarian                                          │   │
│  │  ✓ Research from Academic                                                    │   │
│  │                                                                              │   │
│  │  WHY: Enables learning across sessions without contamination                 │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  PROMOTION RULES:                                                                   │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  Session-local → Shared happens when:                                        │   │
│  │  ├── Session completes successfully                                          │   │
│  │  ├── User explicitly promotes a pattern/decision                             │   │
│  │  ├── Inspector validates and approves                                        │   │
│  │  └── Pattern is used successfully N times                                    │   │
│  │                                                                              │   │
│  │  Shared data includes session_id for:                                        │   │
│  │  ├── Filtering queries to specific sessions                                  │   │
│  │  ├── Attribution and traceability                                            │   │
│  │  └── Rollback if needed                                                      │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Session Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                            SESSION LIFECYCLE                                         │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│                              ┌──────────┐                                           │
│                              │ CREATED  │                                           │
│                              └────┬─────┘                                           │
│                                   │ Activate()                                      │
│                                   ▼                                                 │
│                              ┌──────────┐                                           │
│                    ┌─────────│  ACTIVE  │─────────┐                                 │
│                    │         └────┬─────┘         │                                 │
│                    │              │               │                                 │
│         Suspend()  │              │ Complete()    │ Fail                            │
│                    │              │               │                                 │
│                    ▼              ▼               ▼                                 │
│              ┌──────────┐   ┌──────────┐   ┌──────────┐                             │
│              │SUSPENDED │   │COMPLETED │   │  FAILED  │                             │
│              └────┬─────┘   └──────────┘   └──────────┘                             │
│                   │                                                                 │
│         Resume()  │                                                                 │
│                   │                                                                 │
│                   └───────────────┐                                                 │
│                                   │                                                 │
│                                   ▼                                                 │
│                              ┌──────────┐                                           │
│                              │  ACTIVE  │                                           │
│                              └──────────┘                                           │
│                                                                                     │
│  State Transitions:                                                                 │
│  ├── CREATED → ACTIVE: Session initialization complete                             │
│  ├── ACTIVE → SUSPENDED: User switches to different session                        │
│  ├── ACTIVE → COMPLETED: All tasks done, user confirms                             │
│  ├── ACTIVE → FAILED: Unrecoverable error                                          │
│  ├── SUSPENDED → ACTIVE: User returns to session (Resume)                          │
│  └── SUSPENDED → COMPLETED: Cleanup of abandoned session                           │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Session Context Preservation

```go
// What gets preserved when a session is suspended
type SessionSnapshot struct {
    Session           *Session              `json:"session"`
    ArchivalistState  *ArchivalistSnapshot  `json:"archivalist_state"`
    GuideState        *GuideSnapshot        `json:"guide_state"`
    DAGState          *DAGSnapshot          `json:"dag_state,omitempty"`
    PendingMessages   []*PendingMessage     `json:"pending_messages,omitempty"`
    AgentStates       map[string]*AgentSnapshot `json:"agent_states,omitempty"`
    CreatedAt         time.Time             `json:"created_at"`
}

// ArchivalistSnapshot captures session-specific Archivalist state
type ArchivalistSnapshot struct {
    SessionID         string                `json:"session_id"`
    AgentContext      *AgentBriefing        `json:"agent_context"`
    ResumeState       *ResumeState          `json:"resume_state"`
    LocalEntryIDs     []string              `json:"local_entry_ids"`
    PendingWrites     []*Entry              `json:"pending_writes,omitempty"`
}

// GuideSnapshot captures session-specific Guide state
type GuideSnapshot struct {
    SessionID         string                `json:"session_id"`
    PendingRequests   []*PendingRequest     `json:"pending_requests"`
    ActiveRoutes      map[string]string     `json:"active_routes"`
}

// DAGSnapshot captures in-progress workflow state
type DAGSnapshot struct {
    DAGID             string                `json:"dag_id"`
    CurrentLayer      int                   `json:"current_layer"`
    NodeStates        map[string]NodeState  `json:"node_states"`
    CompletedResults  map[string]*NodeResult `json:"completed_results"`
    PendingNodes      []string              `json:"pending_nodes"`
}
```

### Cross-Session Queries

```go
// Archivalist supports cross-session queries with explicit session filtering
type ArchiveQuery struct {
    // Existing fields...
    Categories      []Category    `json:"categories,omitempty"`
    Sources         []SourceModel `json:"sources,omitempty"`
    Since           *time.Time    `json:"since,omitempty"`
    Until           *time.Time    `json:"until,omitempty"`
    SearchText      string        `json:"search_text,omitempty"`
    Limit           int           `json:"limit,omitempty"`

    // Session filtering (NEW)
    SessionIDs      []string      `json:"session_ids,omitempty"`      // Filter to specific sessions
    ExcludeSessions []string      `json:"exclude_sessions,omitempty"` // Exclude specific sessions
    CurrentOnly     bool          `json:"current_only"`               // Only current session
    CrossSession    bool          `json:"cross_session"`              // Explicitly query all sessions

    // Promotion status
    PromotedOnly    bool          `json:"promoted_only"`              // Only globally promoted entries
}
```

---

## The Guide: Universal Message Router

**CRITICAL: Every message between ANY two agents flows through the Guide. No agent communicates directly with another agent.**

### Guide Responsibilities

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                           GUIDE RESPONSIBILITIES                                    │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  1. MESSAGE ROUTING                                                                 │
│     ├── Receive all messages from all agents                                        │
│     ├── Classify message intent/type                                                │
│     ├── Determine target agent                                                      │
│     ├── Deliver to target agent's inbox                                             │
│     ├── Handle responses back to source                                             │
│     └── SESSION-SCOPE all messages (inject session_id)                              │
│                                                                                     │
│  2. USER INTENT CLASSIFICATION                                                      │
│     ├── Research queries → Academic                                                 │
│     ├── Codebase queries → Librarian                                                │
│     ├── History queries → Archivalist                                               │
│     ├── Implementation requests → Architect                                         │
│     ├── Inspection phase queries → Inspector                                        │
│     ├── Testing phase queries → Tester                                              │
│     └── Session commands → Session Manager                                          │
│                                                                                     │
│  3. INTER-AGENT ROUTING                                                             │
│     ├── Context requests → Librarian                                                │
│     ├── History requests → Archivalist                                              │
│     ├── Research requests → Academic                                                │
│     ├── Task dispatches → specific Engineer                                         │
│     ├── Clarifications → Architect                                                  │
│     ├── Validations → Inspector                                                     │
│     ├── Test signals → Tester                                                       │
│     └── Storage → Archivalist                                                       │
│                                                                                     │
│  4. SESSION CONTEXT INJECTION                                                       │
│     ├── Attach session_id to all messages                                           │
│     ├── Verify agent belongs to session                                             │
│     ├── Enforce session isolation                                                   │
│     └── Route cross-session queries appropriately                                   │
│                                                                                     │
│  5. WORKFLOW SIGNALS                                                                │
│     ├── Detect when workflow modification needed                                    │
│     ├── Signal Orchestrator of changes                                              │
│     ├── Track phase transitions                                                     │
│     └── Maintain conversation context                                               │
│                                                                                     │
│  6. OPTIMIZATION                                                                    │
│     ├── Cache routing decisions                                                     │
│     ├── DSL fast-path for common routes                                             │
│     ├── LLM classification only when needed                                         │
│     └── Batch similar requests                                                      │
│                                                                                     │
│  7. OBSERVABILITY                                                                   │
│     ├── Log all message flows                                                       │
│     ├── Track latencies                                                             │
│     ├── Monitor agent health                                                        │
│     └── Provide debugging info                                                      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### All Message Flows Through Guide

```
USER → AGENT:
┌──────────────────────────────────────────────────────────────────────────────┐
│  User                                                                        │
│    │                                                                         │
│    │ "How would I design a rate limiter?"                                    │
│    │                                                                         │
│    ▼                                                                         │
│  GUIDE ─── classifies: research query ───▶ ACADEMIC                          │
│         ─── injects: session_id ───▶                                         │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘

AGENT → AGENT:
┌──────────────────────────────────────────────────────────────────────────────┐
│  Architect                                                                   │
│    │                                                                         │
│    │ "I need codebase context for middleware patterns"                       │
│    │                                                                         │
│    ▼                                                                         │
│  GUIDE ─── routes: context request ───▶ LIBRARIAN                            │
│    │    ─── verifies: same session ───▶                                      │
│    │                                                                         │
│    │◀─── response: [middleware patterns] ◀─── LIBRARIAN                      │
│    │                                                                         │
│    ▼                                                                         │
│  Architect (receives context)                                                │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Guide Intent Classification for Librarian

**CRITICAL**: The Guide already classifies intent for routing. When routing to Librarian, the Guide extracts additional metadata to enable intent-aware caching - at zero additional token cost.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    GUIDE INTENT CLASSIFICATION FOR LIBRARIAN                         │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  User: "what patterns do we use for error handling"                                 │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  GUIDE ROUTING (already happening)                                          │   │
│  │                                                                             │   │
│  │  Standard classification:                                                   │   │
│  │    - Is this a codebase question? → YES → Route to Librarian               │   │
│  │                                                                             │   │
│  │  NEW: Additional metadata extraction (same LLM call, no extra cost):       │   │
│  │    - Query Intent: PATTERN (strategy/approach question)                    │   │
│  │    - Subject/Concept: "error_handling"                                     │   │
│  │    - Confidence: 0.92                                                      │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  ROUTED MESSAGE TO LIBRARIAN                                                │   │
│  │                                                                             │   │
│  │  {                                                                          │   │
│  │    "query": "what patterns do we use for error handling",                  │   │
│  │    "target": "librarian",                                                  │   │
│  │    "session_id": "sess_abc123",                                            │   │
│  │    "intent": "PATTERN",           ← Enables cache lookup by concept        │   │
│  │    "subject": "error_handling",   ← Cache key                              │   │
│  │    "confidence": 0.92             ← Use cache if > 0.8                     │   │
│  │  }                                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Intent Types for Librarian Queries

| Intent | Trigger Patterns | Example Queries |
|--------|-----------------|-----------------|
| **LOCATE** | "where is", "find", "show me", "which file", "locate" | "where is the auth code", "find CreateUser function" |
| **PATTERN** | "strategy", "approach", "pattern", "how do we", "convention" | "what is our caching strategy", "how do we handle errors" |
| **EXPLAIN** | "how does", "explain", "what does X do", "walk through" | "how does the auth flow work", "explain the pipeline" |
| **GENERAL** | Other codebase questions | "what languages are used", "list all API endpoints" |

#### Guide Routing Prompt Enhancement

```go
// Added to Guide's system prompt for Librarian routing
const LibrarianRoutingInstructions = `
When routing to Librarian, also classify the query:

Intent (required):
- LOCATE: User wants to find where something is (file, function, struct, etc.)
- PATTERN: User asks about patterns, strategies, approaches, conventions
- EXPLAIN: User wants to understand how something works
- GENERAL: Other codebase questions

Subject (required): The primary entity or concept being asked about.
Normalize to snake_case (e.g., "auth code" → "authentication", "error handling" → "error_handling")

Examples:
- "where is the auth middleware" → LOCATE, "auth_middleware"
- "what is our caching strategy" → PATTERN, "caching"
- "how does CreateSession work" → EXPLAIN, "create_session"
- "what testing frameworks do we use" → PATTERN, "testing"
`

// Guide extracts this during routing (same LLM call)
type LibrarianRoutingMetadata struct {
    Intent     QueryIntent `json:"intent"`
    Subject    string      `json:"subject"`
    Confidence float64     `json:"confidence"`
}
```

#### Why This Works (Zero Additional Cost)

```
WITHOUT Intent Classification:
┌────────────────────────────────────────────────────────────────────────────────────┐
│ Guide LLM Call: "Route this message"              → ~200 tokens                    │
│ Librarian LLM Call: "Answer this question"        → ~3000 tokens (every time)     │
│ TOTAL: ~3200 tokens per query                                                      │
└────────────────────────────────────────────────────────────────────────────────────┘

WITH Intent Classification (cache hit):
┌────────────────────────────────────────────────────────────────────────────────────┐
│ Guide LLM Call: "Route this + classify intent"    → ~250 tokens (+50 for metadata)│
│ Librarian: Cache hit using intent + subject       → 0 tokens                       │
│ TOTAL: ~250 tokens per query (on cache hit)                                        │
└────────────────────────────────────────────────────────────────────────────────────┘

WITH Intent Classification (cache miss):
┌────────────────────────────────────────────────────────────────────────────────────┐
│ Guide LLM Call: "Route this + classify intent"    → ~250 tokens                    │
│ Librarian LLM Call: "Answer this question"        → ~3000 tokens                   │
│ TOTAL: ~3250 tokens (same as before, now cached)                                   │
└────────────────────────────────────────────────────────────────────────────────────┘

At 70% cache hit rate:
- Old: 100 queries × 3200 tokens = 320,000 tokens
- New: 30 misses × 3250 + 70 hits × 250 = 97,500 + 17,500 = 115,000 tokens
- SAVINGS: 64%
```

### Guide Intent Classification for Archivalist

**CRITICAL**: The same zero-cost intent classification approach applies to Archivalist routing. The Guide extracts intent metadata during routing to enable intent-aware historical caching.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    GUIDE INTENT CLASSIFICATION FOR ARCHIVALIST                       │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  User: "how did we handle the auth migration last month"                            │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  GUIDE ROUTING (already happening)                                          │   │
│  │                                                                             │   │
│  │  Standard classification:                                                   │   │
│  │    - Is this a history/past question? → YES → Route to Archivalist         │   │
│  │                                                                             │   │
│  │  NEW: Additional metadata extraction (same LLM call, no extra cost):       │   │
│  │    - Query Intent: HISTORICAL (past solution/approach)                     │   │
│  │    - Subject/Concept: "auth_migration"                                     │   │
│  │    - Time Scope: "last_month"                                              │   │
│  │    - Confidence: 0.89                                                      │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  ROUTED MESSAGE TO ARCHIVALIST                                              │   │
│  │                                                                             │   │
│  │  {                                                                          │   │
│  │    "query": "how did we handle the auth migration last month",             │   │
│  │    "target": "archivalist",                                                │   │
│  │    "session_id": "sess_abc123",                                            │   │
│  │    "intent": "HISTORICAL",        ← Enables cache lookup by subject        │   │
│  │    "subject": "auth_migration",   ← Cache key component                    │   │
│  │    "time_scope": "last_month",    ← Temporal partition hint                │   │
│  │    "confidence": 0.89             ← Use cache if > 0.8                     │   │
│  │  }                                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Intent Types for Archivalist Queries

| Intent | Trigger Patterns | Example Queries |
|--------|-----------------|-----------------|
| **HISTORICAL** | "how did we", "what approach", "last time", "previously" | "how did we handle auth migration", "what was the solution for X" |
| **ACTIVITY** | "what has", "recent changes", "who worked on", "history of" | "what has happened with the API", "recent changes to auth" |
| **OUTCOME** | "did it work", "result of", "status of", "how did X turn out" | "did the migration succeed", "result of the refactor" |
| **SIMILAR** | "similar to", "like before", "same as", "comparable" | "similar issues to this error", "problems like this before" |
| **RESUME** | "where was I", "continue", "pick up", "last session" | "where did we leave off", "continue from yesterday" |
| **GENERAL** | Other history questions | "what did the team work on last week" |

#### Guide Routing Prompt Enhancement for Archivalist

```go
// Added to Guide's system prompt for Archivalist routing
const ArchivalistRoutingInstructions = `
When routing to Archivalist, also classify the query:

Intent (required):
- HISTORICAL: User asks about past solutions, approaches, decisions
- ACTIVITY: User asks about recent work, changes, who did what
- OUTCOME: User asks about results, success/failure of past work
- SIMILAR: User asks about similar problems or patterns from history
- RESUME: User wants to continue previous work or session state
- GENERAL: Other history questions

Subject (required): The primary entity or concept being asked about.
Normalize to snake_case (e.g., "auth migration" → "auth_migration")

Time Scope (optional): Extract any temporal hints:
- "last month", "yesterday", "last week" → specific time range
- "recently" → last 7 days
- "before" → historical, no specific time
- Empty if no time reference

Examples:
- "how did we handle the auth migration" → HISTORICAL, "auth_migration", ""
- "what changed in the API recently" → ACTIVITY, "api", "recently"
- "did the database fix work" → OUTCOME, "database_fix", ""
- "similar errors to this before" → SIMILAR, "errors", ""
- "where did we leave off yesterday" → RESUME, "session", "yesterday"
`

// Guide extracts this during routing (same LLM call)
type ArchivalistRoutingMetadata struct {
    Intent     ArchivalistIntent `json:"intent"`
    Subject    string            `json:"subject"`
    TimeScope  string            `json:"time_scope,omitempty"`
    Confidence float64           `json:"confidence"`
}
```

#### Archivalist vs Librarian Intent Classification

```
┌────────────────────────────────────────────────────────────────────────────────────┐
│                    KEY DIFFERENCES IN INTENT CLASSIFICATION                         │
├────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                    │
│  LIBRARIAN (Live Code)                 │  ARCHIVALIST (Historical Data)           │
│  ─────────────────────────────────────┼──────────────────────────────────────────│
│  Subject: File/Function/Struct names   │  Subject: Problem/Solution domains       │
│  Example: "auth_middleware"            │  Example: "auth_migration"               │
│                                        │                                          │
│  No time scope (current state)         │  Time scope (temporal partitions)        │
│  Example: "where is auth"              │  Example: "last month's auth work"       │
│                                        │                                          │
│  Invalidation: File changes            │  Invalidation: Session/TTL only          │
│  Dynamic cache keys                    │  Immutable once recorded                 │
│                                        │                                          │
│  Cache duration: 5-30 minutes          │  Cache duration: 1-60 minutes            │
│  (depends on file volatility)          │  (depends on query type)                 │
│                                        │                                          │
└────────────────────────────────────────────────────────────────────────────────────┘
```

#### Expected Token Savings for Archivalist

```
WITHOUT Intent Classification:
┌────────────────────────────────────────────────────────────────────────────────────┐
│ Guide LLM Call: "Route this message"              → ~200 tokens                    │
│ Archivalist LLM Call: "Search history"            → ~2500 tokens (every time)     │
│ TOTAL: ~2700 tokens per query                                                      │
└────────────────────────────────────────────────────────────────────────────────────┘

WITH Intent Classification (cache hit):
┌────────────────────────────────────────────────────────────────────────────────────┐
│ Guide LLM Call: "Route this + classify intent"    → ~280 tokens (+80 for metadata)│
│ Archivalist: Cache hit using intent + subject     → 0 tokens                       │
│ TOTAL: ~280 tokens per query (on cache hit)                                        │
└────────────────────────────────────────────────────────────────────────────────────┘

At 75% cache hit rate (higher than Librarian due to immutable history):
- Old: 100 queries × 2700 tokens = 270,000 tokens
- New: 25 misses × 2780 + 75 hits × 280 = 69,500 + 21,000 = 90,500 tokens
- SAVINGS: 66%
```

---

## Agent Roles, Skills, Tools, and Hooks

### Complete Agent Summary

| Agent | Role | User Interaction | Primary Responsibility |
|-------|------|------------------|------------------------|
| **Academic** | External knowledge RAG | DIRECT (triggered by research queries) | Research papers, best practices, external references |
| **Architect** | Planning & coordination | PRIMARY (default agent) | Abstract → Concrete, DAG design, user coordination |
| **Orchestrator** | Workflow execution | NONE (invisible) | Execute DAGs, manage Engineers, status propagation |
| **Engineer** | Task execution | NONE (invisible) | Code writing, problem solving |
| **Designer** | UI/UX implementation | PRIMARY (during UI work) | Component design, styling, accessibility, design systems |
| **Librarian** | Local codebase RAG | DIRECT (triggered by codebase queries) | Code context, pattern detection |
| **Archivalist** | Historical RAG | DIRECT (triggered by history queries) | Past decisions, solution patterns |
| **Inspector** | Code validation | PRIMARY (during inspection) | Compliance checking, issue detection |
| **Tester** | Test planning & execution | PRIMARY (during testing) | Test planning, execution, failure analysis |
| **Guide** | Universal router | NONE (invisible) | Intent classification, message routing |

---

## Agent Skills (Progressive Disclosure)

Each agent has skills that are loaded progressively based on context. Skills are loaded lazily to minimize token usage.

### Skill Loading Strategy

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                          PROGRESSIVE SKILL DISCLOSURE                                │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TIER 1: CORE SKILLS (Always Loaded)                                                │
│  ├── Essential for agent's primary function                                         │
│  ├── ~5-10 skills per agent                                                         │
│  └── Loaded at agent startup                                                        │
│                                                                                     │
│  TIER 2: CONTEXTUAL SKILLS (Loaded on Demand)                                       │
│  ├── Triggered by keywords in user input                                            │
│  ├── Triggered by workflow phase                                                    │
│  ├── Triggered by other agent requests                                              │
│  └── Unloaded when context changes                                                  │
│                                                                                     │
│  TIER 3: SPECIALIZED SKILLS (Explicitly Requested)                                  │
│  ├── Advanced capabilities                                                          │
│  ├── Loaded via DSL command or explicit request                                     │
│  └── Higher token cost, used sparingly                                              │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Guide Skills

```go
// Core Skills (Tier 1)
guide_skills_core := []Skill{
    {
        Name:        "route",
        Description: "Route a message to the appropriate agent",
        Domain:      "routing",
        Keywords:    []string{"route", "send", "forward", "dispatch"},
        Priority:    100,
        Parameters: []Param{
            {Name: "input", Type: "string", Required: true},
            {Name: "target", Type: "string", Required: false},
            {Name: "intent", Type: "enum", Values: []string{"recall", "store", "check", "declare", "complete"}},
        },
    },
    {
        Name:        "classify",
        Description: "Classify user intent without routing",
        Domain:      "routing",
        Keywords:    []string{"classify", "analyze", "understand"},
        Priority:    90,
    },
    {
        Name:        "help",
        Description: "Provide help about available commands and agents",
        Domain:      "system",
        Keywords:    []string{"help", "?", "how", "what"},
        Priority:    100,
    },
    {
        Name:        "status",
        Description: "Get current system status",
        Domain:      "system",
        Keywords:    []string{"status", "health", "agents"},
        Priority:    80,
    },
}

// Contextual Skills (Tier 2)
guide_skills_contextual := []Skill{
    {
        Name:        "session_switch",
        Description: "Switch to a different session",
        Domain:      "session",
        Keywords:    []string{"switch", "session", "change context"},
        LoadTrigger: "session|switch|context",
    },
    {
        Name:        "session_list",
        Description: "List all available sessions",
        Domain:      "session",
        Keywords:    []string{"sessions", "list", "show sessions"},
        LoadTrigger: "session|list|sessions",
    },
    {
        Name:        "broadcast",
        Description: "Broadcast a message to multiple agents",
        Domain:      "routing",
        Keywords:    []string{"broadcast", "notify all", "announce"},
        LoadTrigger: "broadcast|notify|announce",
    },
}
```

### Archivalist Skills

```go
// Core Skills (Tier 1)
archivalist_skills_core := []Skill{
    {
        Name:        "store",
        Description: "Store information in the chronicle",
        Domain:      "chronicle",
        Keywords:    []string{"store", "save", "record", "remember", "log"},
        Priority:    100,
        Parameters: []Param{
            {Name: "content", Type: "string", Required: true},
            {Name: "category", Type: "enum", Values: []string{"decision", "insight", "pattern", "failure", "task_state", "timeline", "user_voice", "hypothesis", "open_thread", "general"}, Required: true},
            {Name: "title", Type: "string", Required: false},
        },
    },
    {
        Name:        "query",
        Description: "Query the chronicle for stored information",
        Domain:      "chronicle",
        Keywords:    []string{"query", "find", "search", "recall", "retrieve", "what", "when", "how"},
        Priority:    100,
        Parameters: []Param{
            {Name: "search", Type: "string", Required: false},
            {Name: "category", Type: "enum", Values: []string{"all", "decision", "insight", "pattern", "failure"}, Required: false},
            {Name: "limit", Type: "int", Required: false},
            {Name: "cross_session", Type: "bool", Required: false, Description: "Query across all sessions"},
        },
    },
    {
        Name:        "briefing",
        Description: "Get current session state briefing",
        Domain:      "memory",
        Keywords:    []string{"briefing", "status", "context", "state", "progress", "current"},
        Priority:    90,
        Parameters: []Param{
            {Name: "tier", Type: "enum", Values: []string{"micro", "standard", "full"}, Required: false},
        },
    },
}

// Contextual Skills (Tier 2)
archivalist_skills_contextual := []Skill{
    {
        Name:        "cross_session_query",
        Description: "Query historical data from other sessions",
        Domain:      "chronicle",
        Keywords:    []string{"other sessions", "past work", "historical", "before"},
        LoadTrigger: "other session|past|historical|before|previous",
        Parameters: []Param{
            {Name: "session_ids", Type: "array", Required: false},
            {Name: "exclude_current", Type: "bool", Required: false},
        },
    },
    {
        Name:        "promote_pattern",
        Description: "Promote a session-local pattern to global",
        Domain:      "chronicle",
        Keywords:    []string{"promote", "global", "share"},
        LoadTrigger: "promote|global|share pattern",
    },
    {
        Name:        "session_summary",
        Description: "Generate summary of a session's work",
        Domain:      "chronicle",
        Keywords:    []string{"summarize session", "session summary"},
        LoadTrigger: "summarize|summary",
    },
}

// Specialized Skills (Tier 3)
archivalist_skills_specialized := []Skill{
    {
        Name:        "fact_extraction",
        Description: "Extract structured facts from entries",
        Domain:      "analysis",
        Keywords:    []string{"extract facts", "analyze entries"},
        RequiresExplicit: true,
    },
    {
        Name:        "conflict_resolution",
        Description: "Resolve conflicts between concurrent writes",
        Domain:      "concurrency",
        Keywords:    []string{"conflict", "resolve", "merge"},
        RequiresExplicit: true,
    },
}
```

### Architect Skills

```go
// Core Skills (Tier 1)
architect_skills_core := []Skill{
    {
        Name:        "plan",
        Description: "Create an implementation plan from requirements",
        Domain:      "planning",
        Keywords:    []string{"plan", "implement", "build", "create", "add"},
        Priority:    100,
        Parameters: []Param{
            {Name: "request", Type: "string", Required: true},
            {Name: "constraints", Type: "array", Required: false},
        },
    },
    {
        Name:        "clarify",
        Description: "Ask user for clarification on requirements",
        Domain:      "coordination",
        Keywords:    []string{"clarify", "unclear", "question"},
        Priority:    90,
    },
    {
        Name:        "status_update",
        Description: "Provide execution status to user",
        Domain:      "coordination",
        Keywords:    []string{"status", "progress", "update"},
        Priority:    80,
    },
    {
        Name:        "approve_plan",
        Description: "Present plan for user approval",
        Domain:      "coordination",
        Keywords:    []string{"approve", "review", "confirm"},
        Priority:    90,
    },
}

// Contextual Skills (Tier 2)
architect_skills_contextual := []Skill{
    {
        Name:        "modify_plan",
        Description: "Modify existing plan based on feedback",
        Domain:      "planning",
        Keywords:    []string{"change", "modify", "update plan"},
        LoadTrigger: "change|modify|different|instead",
    },
    {
        Name:        "create_fix_dag",
        Description: "Create fix workflow from Inspector/Tester corrections",
        Domain:      "planning",
        Keywords:    []string{"fix", "correct", "repair"},
        LoadTrigger: "fix|correct|repair|issue|fail",
    },
    {
        Name:        "interrupt_handler",
        Description: "Handle user interruption during execution",
        Domain:      "coordination",
        Keywords:    []string{"stop", "wait", "pause", "interrupt"},
        LoadTrigger: "stop|wait|pause|hold",
    },
}
```

### Engineer Skills

```go
// Core Skills (Tier 1)
engineer_skills_core := []Skill{
    {
        Name:        "execute_task",
        Description: "Execute an assigned task",
        Domain:      "execution",
        Keywords:    []string{"execute", "do", "implement", "code"},
        Priority:    100,
    },
    {
        Name:        "read_file",
        Description: "Read a file's contents",
        Domain:      "files",
        Keywords:    []string{"read", "view", "show", "cat"},
        Priority:    100,
    },
    {
        Name:        "write_file",
        Description: "Write content to a file",
        Domain:      "files",
        Keywords:    []string{"write", "create", "save"},
        Priority:    100,
    },
    {
        Name:        "edit_file",
        Description: "Edit an existing file",
        Domain:      "files",
        Keywords:    []string{"edit", "modify", "change", "update"},
        Priority:    100,
    },
    {
        Name:        "run_command",
        Description: "Execute a shell command",
        Domain:      "execution",
        Keywords:    []string{"run", "execute", "shell", "bash"},
        Priority:    90,
    },
    {
        Name:        "request_help",
        Description: "Request clarification via Orchestrator",
        Domain:      "coordination",
        Keywords:    []string{"help", "unclear", "confused", "question"},
        Priority:    80,
    },
}

// Contextual Skills (Tier 2)
engineer_skills_contextual := []Skill{
    {
        Name:        "consult_librarian",
        Description: "Query codebase context from Librarian",
        Domain:      "consultation",
        Keywords:    []string{"where is", "how does", "show me", "find"},
        LoadTrigger: "where|how|find|existing|pattern",
    },
    {
        Name:        "consult_archivalist",
        Description: "Query historical context from Archivalist",
        Domain:      "consultation",
        Keywords:    []string{"before", "last time", "previously", "history"},
        LoadTrigger: "before|last|previous|history|did we",
    },
    {
        Name:        "consult_academic",
        Description: "Query research from Academic",
        Domain:      "consultation",
        Keywords:    []string{"best practice", "how should", "recommended"},
        LoadTrigger: "best practice|recommend|should|standard",
    },
}
```

### Designer Skills

```go
// Core Skills (Tier 1)
designer_skills_core := []Skill{
    {
        Name:        "create_component",
        Description: "Create a new UI component",
        Domain:      "ui",
        Keywords:    []string{"component", "create", "new", "ui"},
        Priority:    100,
        Parameters: []Param{
            {Name: "name", Type: "string", Required: true},
            {Name: "type", Type: "enum", Values: []string{"functional", "class", "styled"}, Required: false},
            {Name: "framework", Type: "enum", Values: []string{"react", "vue", "svelte", "solid"}, Required: false},
        },
    },
    {
        Name:        "style_component",
        Description: "Apply styling to a component",
        Domain:      "styling",
        Keywords:    []string{"style", "css", "tailwind", "styled"},
        Priority:    100,
        Parameters: []Param{
            {Name: "component", Type: "string", Required: true},
            {Name: "approach", Type: "enum", Values: []string{"css-modules", "tailwind", "styled-components", "css-in-js"}, Required: false},
        },
    },
    {
        Name:        "get_design_tokens",
        Description: "Retrieve design system tokens",
        Domain:      "design_system",
        Keywords:    []string{"token", "color", "spacing", "typography", "theme"},
        Priority:    100,
        Parameters: []Param{
            {Name: "category", Type: "enum", Values: []string{"color", "spacing", "typography", "shadow", "border", "all"}, Required: false},
            {Name: "name", Type: "string", Required: false},
        },
    },
    {
        Name:        "find_component",
        Description: "Find existing UI components in the codebase",
        Domain:      "ui",
        Keywords:    []string{"find", "existing", "component", "reuse"},
        Priority:    100,
        Parameters: []Param{
            {Name: "query", Type: "string", Required: true},
            {Name: "type", Type: "enum", Values: []string{"component", "hook", "utility", "all"}, Required: false},
        },
    },
    {
        Name:        "preview_ui",
        Description: "Preview UI changes in isolation",
        Domain:      "preview",
        Keywords:    []string{"preview", "render", "show", "display"},
        Priority:    90,
    },
    {
        Name:        "check_accessibility",
        Description: "Check component accessibility compliance",
        Domain:      "accessibility",
        Keywords:    []string{"a11y", "accessibility", "aria", "wcag"},
        Priority:    90,
        Parameters: []Param{
            {Name: "component", Type: "string", Required: true},
            {Name: "level", Type: "enum", Values: []string{"A", "AA", "AAA"}, Required: false},
        },
    },
}

// Contextual Skills (Tier 2)
designer_skills_contextual := []Skill{
    {
        Name:        "analyze_layout",
        Description: "Analyze and suggest layout improvements",
        Domain:      "layout",
        Keywords:    []string{"layout", "grid", "flex", "responsive"},
        LoadTrigger: "layout|grid|flex|responsive|breakpoint",
    },
    {
        Name:        "suggest_animation",
        Description: "Suggest appropriate animations",
        Domain:      "animation",
        Keywords:    []string{"animate", "transition", "motion"},
        LoadTrigger: "animate|transition|motion|effect",
    },
    {
        Name:        "audit_consistency",
        Description: "Audit design consistency across components",
        Domain:      "design_system",
        Keywords:    []string{"consistency", "audit", "design system"},
        LoadTrigger: "consistent|audit|design system|theme",
    },
    {
        Name:        "generate_variants",
        Description: "Generate component variants (sizes, states)",
        Domain:      "ui",
        Keywords:    []string{"variant", "size", "state"},
        LoadTrigger: "variant|size|state|small|large|disabled",
    },
    {
        Name:        "consult_engineer",
        Description: "Consult Engineer for implementation details",
        Domain:      "consultation",
        Keywords:    []string{"implement", "logic", "state management"},
        LoadTrigger: "implement|logic|state|hook|data",
    },
    {
        Name:        "consult_academic",
        Description: "Research UI/UX best practices",
        Domain:      "consultation",
        Keywords:    []string{"best practice", "pattern", "guideline"},
        LoadTrigger: "best practice|pattern|guideline|ux",
    },
}

// Specialized Skills (Tier 3)
designer_skills_specialized := []Skill{
    {
        Name:        "design_system_scaffold",
        Description: "Scaffold a complete design system",
        Domain:      "design_system",
        Keywords:    []string{"scaffold", "design system", "foundation"},
        LoadTrigger: "explicit_request",
    },
    {
        Name:        "theme_migration",
        Description: "Migrate between design systems or themes",
        Domain:      "design_system",
        Keywords:    []string{"migrate", "theme", "redesign"},
        LoadTrigger: "explicit_request",
    },
    {
        Name:        "responsive_audit",
        Description: "Full responsive design audit",
        Domain:      "responsive",
        Keywords:    []string{"responsive", "mobile", "tablet", "breakpoint"},
        LoadTrigger: "explicit_request",
    },
}
```

### Librarian Skills

```go
// Core Skills (Tier 1)
librarian_skills_core := []Skill{
    {
        Name:        "search_code",
        Description: "Search codebase for patterns or symbols",
        Domain:      "code",
        Keywords:    []string{"search", "find", "where", "grep"},
        Priority:    100,
        Parameters: []Param{
            {Name: "query", Type: "string", Required: true},
            {Name: "file_pattern", Type: "string", Required: false},
            {Name: "symbol_type", Type: "enum", Values: []string{"function", "type", "const", "var", "all"}, Required: false},
        },
    },
    {
        Name:        "get_context",
        Description: "Get contextual information about code",
        Domain:      "code",
        Keywords:    []string{"context", "explain", "what is", "how does"},
        Priority:    100,
    },
    {
        Name:        "list_files",
        Description: "List files matching pattern",
        Domain:      "files",
        Keywords:    []string{"list", "show files", "ls"},
        Priority:    90,
    },
    {
        Name:        "get_structure",
        Description: "Get codebase structure overview",
        Domain:      "code",
        Keywords:    []string{"structure", "overview", "organization"},
        Priority:    80,
    },
}

// Contextual Skills (Tier 2)
librarian_skills_contextual := []Skill{
    {
        Name:        "analyze_dependencies",
        Description: "Analyze dependency relationships",
        Domain:      "code",
        Keywords:    []string{"dependencies", "imports", "uses"},
        LoadTrigger: "depend|import|uses|requires",
    },
    {
        Name:        "detect_patterns",
        Description: "Detect coding patterns in codebase",
        Domain:      "code",
        Keywords:    []string{"patterns", "conventions", "style"},
        LoadTrigger: "pattern|convention|style|how do we",
    },
}
```

### Academic Skills

```go
// Core Skills (Tier 1)
academic_skills_core := []Skill{
    {
        Name:        "research",
        Description: "Research a topic and produce findings",
        Domain:      "research",
        Keywords:    []string{"research", "investigate", "study", "learn about"},
        Priority:    100,
        Parameters: []Param{
            {Name: "topic", Type: "string", Required: true},
            {Name: "depth", Type: "enum", Values: []string{"quick", "standard", "deep"}, Required: false},
        },
    },
    {
        Name:        "compare",
        Description: "Compare approaches or technologies",
        Domain:      "research",
        Keywords:    []string{"compare", "vs", "versus", "or", "tradeoffs"},
        Priority:    90,
    },
    {
        Name:        "best_practices",
        Description: "Find best practices for a topic",
        Domain:      "research",
        Keywords:    []string{"best practice", "recommended", "standard", "how should"},
        Priority:    90,
    },
}

// Contextual Skills (Tier 2)
academic_skills_contextual := []Skill{
    {
        Name:        "ingest_source",
        Description: "Ingest external source for research",
        Domain:      "research",
        Keywords:    []string{"github", "paper", "article", "rfc"},
        LoadTrigger: "github|paper|article|rfc|spec",
    },
    {
        Name:        "design_proposal",
        Description: "Create a design proposal document",
        Domain:      "research",
        Keywords:    []string{"design", "architecture", "proposal"},
        LoadTrigger: "design|architect|proposal",
    },
}
```

### Inspector Skills

```go
// Core Skills (Tier 1)
inspector_skills_core := []Skill{
    {
        Name:        "validate_task",
        Description: "Validate a completed task",
        Domain:      "validation",
        Keywords:    []string{"validate", "check", "verify"},
        Priority:    100,
    },
    {
        Name:        "validate_full",
        Description: "Full validation of implementation",
        Domain:      "validation",
        Keywords:    []string{"full validation", "complete check"},
        Priority:    100,
    },
    {
        Name:        "check_style",
        Description: "Check code style compliance",
        Domain:      "validation",
        Keywords:    []string{"style", "lint", "format"},
        Priority:    90,
    },
    {
        Name:        "report_issues",
        Description: "Report found issues to Architect",
        Domain:      "validation",
        Keywords:    []string{"issues", "problems", "corrections"},
        Priority:    90,
    },
}

// Contextual Skills (Tier 2)
inspector_skills_contextual := []Skill{
    {
        Name:        "deep_analysis",
        Description: "Deep analysis for race conditions, leaks, etc.",
        Domain:      "validation",
        Keywords:    []string{"race", "leak", "deadlock", "security"},
        LoadTrigger: "race|leak|deadlock|security|deep",
    },
    {
        Name:        "explain_issue",
        Description: "Explain an issue to user",
        Domain:      "validation",
        Keywords:    []string{"explain", "why", "what's wrong"},
        LoadTrigger: "explain|why|what",
    },
}
```

### Tester Skills

```go
// Core Skills (Tier 1)
tester_skills_core := []Skill{
    {
        Name:        "plan_tests",
        Description: "Create test plan for implementation",
        Domain:      "testing",
        Keywords:    []string{"test plan", "what to test", "tests needed"},
        Priority:    100,
    },
    {
        Name:        "run_tests",
        Description: "Execute test suite",
        Domain:      "testing",
        Keywords:    []string{"run tests", "execute tests", "test"},
        Priority:    100,
    },
    {
        Name:        "analyze_failures",
        Description: "Analyze test failures",
        Domain:      "testing",
        Keywords:    []string{"failure", "failed", "why failed"},
        Priority:    90,
    },
    {
        Name:        "report_results",
        Description: "Report test results to user",
        Domain:      "testing",
        Keywords:    []string{"results", "report", "summary"},
        Priority:    90,
    },
}

// Contextual Skills (Tier 2)
tester_skills_contextual := []Skill{
    {
        Name:        "generate_test_code",
        Description: "Generate test implementation",
        Domain:      "testing",
        Keywords:    []string{"generate", "write tests", "implement tests"},
        LoadTrigger: "generate|write|implement test",
    },
    {
        Name:        "coverage_analysis",
        Description: "Analyze test coverage",
        Domain:      "testing",
        Keywords:    []string{"coverage", "uncovered", "missing tests"},
        LoadTrigger: "coverage|uncovered|missing",
    },
}
```

---

## Agent Skill Definitions

Each agent exposes specific skills that other agents (via Guide) can invoke.

### Skill Naming Convention

```
<agent_name>_<action>_<domain>

Examples:
- archivalist_query_chronicle
- librarian_search_code
- engineer_write_files
- inspector_validate_task
```

### Guide Skill Definitions

```go
guide_skills := []SkillDefinition{
    {
        Name:        "guide_route_message",
        Description: "Route a message to the appropriate agent",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "input":    {"type": "string", "description": "The message to route"},
                "target":   {"type": "string", "description": "Optional explicit target agent"},
                "session_id": {"type": "string", "description": "Session context"},
            },
            "required": []string{"input"},
        },
    },
    {
        Name:        "guide_classify_intent",
        Description: "Classify user intent without routing",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "input": {"type": "string", "description": "The input to classify"},
            },
            "required": []string{"input"},
        },
    },
    {
        Name:        "guide_get_status",
        Description: "Get system status",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{},
        },
    },
}
```

### Archivalist Skill Definitions

```go
archivalist_skills := []SkillDefinition{
    {
        Name:        "archivalist_store_entry",
        Description: "Store an entry in the chronicle",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "content":  {"type": "string"},
                "category": {"type": "string", "enum": []string{"decision", "insight", "pattern", "failure", "task_state", "timeline", "user_voice", "hypothesis", "open_thread", "general"}},
                "title":    {"type": "string"},
                "session_id": {"type": "string"},
            },
            "required": []string{"content", "category"},
        },
    },
    {
        Name:        "archivalist_query_chronicle",
        Description: "Query the chronicle for information",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "search_text":    {"type": "string"},
                "categories":     {"type": "array", "items": {"type": "string"}},
                "session_ids":    {"type": "array", "items": {"type": "string"}},
                "cross_session":  {"type": "boolean"},
                "limit":          {"type": "integer"},
            },
        },
    },
    {
        Name:        "archivalist_get_briefing",
        Description: "Get session briefing",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "tier": {"type": "string", "enum": []string{"micro", "standard", "full"}},
            },
        },
    },
    {
        Name:        "archivalist_record_file",
        Description: "Record file operation",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":        {"type": "string"},
                "operation":   {"type": "string", "enum": []string{"read", "modified", "created"}},
                "summary":     {"type": "string"},
                "changes":     {"type": "array"},
            },
            "required": []string{"path", "operation"},
        },
    },
    {
        Name:        "archivalist_record_pattern",
        Description: "Record a coding pattern",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "category":    {"type": "string"},
                "name":        {"type": "string"},
                "pattern":     {"type": "string"},
                "example":     {"type": "string"},
            },
            "required": []string{"category", "name", "pattern"},
        },
    },
    {
        Name:        "archivalist_record_failure",
        Description: "Record a failure and optionally its resolution",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "approach":   {"type": "string"},
                "reason":     {"type": "string"},
                "context":    {"type": "string"},
                "resolution": {"type": "string"},
            },
            "required": []string{"approach", "reason"},
        },
    },
}
```

### Librarian Skill Definitions

```go
librarian_skills := []SkillDefinition{
    {
        Name:        "librarian_search_code",
        Description: "Search codebase for patterns, symbols, or text",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query":       {"type": "string"},
                "file_glob":   {"type": "string"},
                "symbol_type": {"type": "string", "enum": []string{"function", "type", "const", "var", "all"}},
                "limit":       {"type": "integer"},
            },
            "required": []string{"query"},
        },
    },
    {
        Name:        "librarian_get_file",
        Description: "Get file contents with optional line range",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":       {"type": "string"},
                "start_line": {"type": "integer"},
                "end_line":   {"type": "integer"},
            },
            "required": []string{"path"},
        },
    },
    {
        Name:        "librarian_get_structure",
        Description: "Get codebase structure overview",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":  {"type": "string"},
                "depth": {"type": "integer"},
            },
        },
    },
    {
        Name:        "librarian_get_dependencies",
        Description: "Get dependency graph for a file or package",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":      {"type": "string"},
                "direction": {"type": "string", "enum": []string{"imports", "imported_by", "both"}},
            },
            "required": []string{"path"},
        },
    },
}
```

### Engineer Skill Definitions

```go
engineer_skills := []SkillDefinition{
    {
        Name:        "engineer_read_file",
        Description: "Read a file's contents",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":       {"type": "string"},
                "start_line": {"type": "integer"},
                "end_line":   {"type": "integer"},
            },
            "required": []string{"path"},
        },
    },
    {
        Name:        "engineer_write_file",
        Description: "Write content to a file",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":    {"type": "string"},
                "content": {"type": "string"},
            },
            "required": []string{"path", "content"},
        },
    },
    {
        Name:        "engineer_edit_file",
        Description: "Edit a file with search/replace",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path":        {"type": "string"},
                "old_content": {"type": "string"},
                "new_content": {"type": "string"},
            },
            "required": []string{"path", "old_content", "new_content"},
        },
    },
    {
        Name:        "engineer_run_command",
        Description: "Execute a shell command",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "command": {"type": "string"},
                "timeout": {"type": "integer"},
            },
            "required": []string{"command"},
        },
    },
    {
        Name:        "engineer_signal_complete",
        Description: "Signal task completion",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "result":  {"type": "string"},
                "files_modified": {"type": "array", "items": {"type": "string"}},
            },
            "required": []string{"result"},
        },
    },
    {
        Name:        "engineer_request_help",
        Description: "Request clarification via Orchestrator",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "question": {"type": "string"},
                "context":  {"type": "string"},
            },
            "required": []string{"question"},
        },
    },
}
```

---

## LLM Hooks

Hooks allow agents to inject logic before/after LLM calls and tool executions.

### Hook Types

```go
type HookPriority int

const (
    HookPriorityFirst    HookPriority = 0
    HookPriorityEarly    HookPriority = 25
    HookPriorityNormal   HookPriority = 50
    HookPriorityLate     HookPriority = 75
    HookPriorityLast     HookPriority = 100
)

type PromptHookFunc func(ctx context.Context, data *PromptHookData) (*PromptHookData, error)
type ToolCallHookFunc func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error)
```

### Hook Execution Flow

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                              HOOK EXECUTION FLOW                                     │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  LLM CALL:                                                                          │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  1. PRE-PROMPT HOOKS (ordered by priority)                                   │   │
│  │     ├── session_context_injection (inject session state)                     │   │
│  │     ├── skill_loading (load contextual skills)                               │   │
│  │     ├── history_injection (inject relevant history)                          │   │
│  │     └── token_budget_check (verify within limits)                            │   │
│  │                                                                              │   │
│  │  2. LLM CALL                                                                 │   │
│  │                                                                              │   │
│  │  3. POST-PROMPT HOOKS (ordered by priority)                                  │   │
│  │     ├── response_logging (log to Archivalist)                                │   │
│  │     ├── pattern_extraction (extract patterns for learning)                   │   │
│  │     └── token_accounting (track token usage)                                 │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  TOOL CALL:                                                                         │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  1. PRE-TOOL HOOKS (ordered by priority)                                     │   │
│  │     ├── permission_check (verify allowed in session)                         │   │
│  │     ├── parameter_validation (validate inputs)                               │   │
│  │     └── session_scoping (add session context to params)                      │   │
│  │                                                                              │   │
│  │  2. TOOL EXECUTION                                                           │   │
│  │                                                                              │   │
│  │  3. POST-TOOL HOOKS (ordered by priority)                                    │   │
│  │     ├── result_logging (log to Archivalist)                                  │   │
│  │     ├── file_tracking (record file operations)                               │   │
│  │     └── state_update (update session context)                                │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Per-Agent Hooks

#### Guide Hooks

```go
guide_hooks := []Hook{
    // Pre-prompt hooks
    {
        Name:     "session_context_injection",
        Type:     PrePrompt,
        Priority: HookPriorityFirst,
        Handler:  func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
            // Inject current session context into system prompt
            session := ctx.Value("session").(*Session)
            data.SystemPrompt = appendSessionContext(data.SystemPrompt, session)
            return data, nil
        },
    },
    {
        Name:     "route_cache_lookup",
        Type:     PrePrompt,
        Priority: HookPriorityEarly,
        Handler:  func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
            // Check route cache before LLM classification
            if cached := routeCache.Get(data.Input); cached != nil {
                data.SkipLLM = true
                data.CachedResult = cached
            }
            return data, nil
        },
    },
    // Post-prompt hooks
    {
        Name:     "route_cache_store",
        Type:     PostPrompt,
        Priority: HookPriorityNormal,
        Handler:  func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
            // Cache classification result
            if data.ClassificationMethod == "llm" {
                routeCache.Set(data.Input, data.Result)
            }
            return data, nil
        },
    },
}
```

#### Archivalist Hooks

```go
archivalist_hooks := []Hook{
    // Pre-prompt hooks
    {
        Name:     "cross_session_query_handler",
        Type:     PrePrompt,
        Priority: HookPriorityEarly,
        Handler:  func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
            // Handle cross-session query requests
            if data.CrossSession {
                data.QueryScope = "all_sessions"
            } else {
                data.QueryScope = ctx.Value("session_id").(string)
            }
            return data, nil
        },
    },
    // Post-tool hooks
    {
        Name:     "entry_session_tagging",
        Type:     PostTool,
        Priority: HookPriorityFirst,
        Handler:  func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
            // Tag all stored entries with session_id
            if data.ToolName == "archivalist_store_entry" {
                entry := data.Result.(*Entry)
                entry.SessionID = ctx.Value("session_id").(string)
            }
            return data, nil
        },
    },
    {
        Name:     "pattern_promotion_check",
        Type:     PostTool,
        Priority: HookPriorityNormal,
        Handler:  func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
            // Check if pattern should be promoted to global
            if data.ToolName == "archivalist_record_pattern" {
                pattern := data.Input.(*Pattern)
                if shouldPromote(pattern) {
                    promoteToGlobal(pattern)
                }
            }
            return data, nil
        },
    },
}
```

#### Engineer Hooks

```go
engineer_hooks := []Hook{
    // Pre-tool hooks
    {
        Name:     "file_operation_tracking",
        Type:     PreTool,
        Priority: HookPriorityFirst,
        Handler:  func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
            // Track file operations for Archivalist
            if isFileOperation(data.ToolName) {
                path := data.Input["path"].(string)
                archivalist.RecordFileOperation(ctx, path, data.ToolName)
            }
            return data, nil
        },
    },
    // Post-tool hooks
    {
        Name:     "consultation_logging",
        Type:     PostTool,
        Priority: HookPriorityNormal,
        Handler:  func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
            // Log consultations to Archivalist
            if isConsultation(data.ToolName) {
                archivalist.StoreEntry(ctx, &Entry{
                    Category: CategoryTimeline,
                    Content:  fmt.Sprintf("Consulted %s: %s", data.ToolName, data.Input["query"]),
                })
            }
            return data, nil
        },
    },
}
```

---

## Session Integration with Agents

### Guide Session Awareness

```go
// Guide session-aware routing
func (g *Guide) Route(ctx context.Context, request *RouteRequest) (*ForwardedRequest, error) {
    // Ensure session context
    if request.SessionID == "" {
        request.SessionID = g.sessionManager.GetActive().ID
    }

    // Validate session exists and is active
    session, exists := g.sessionManager.Get(request.SessionID)
    if !exists {
        return nil, fmt.Errorf("session %s not found", request.SessionID)
    }
    if session.State != SessionStateActive {
        return nil, fmt.Errorf("session %s is not active (state: %s)", request.SessionID, session.State)
    }

    // Execute pre-prompt hooks with session context
    hookCtx := context.WithValue(ctx, "session", session)
    hookCtx = context.WithValue(hookCtx, "session_id", request.SessionID)

    // ... routing logic ...

    // Inject session_id into forwarded request
    forwarded.SessionID = request.SessionID

    return forwarded, nil
}
```

### Archivalist Session Awareness

```go
// Archivalist session-aware queries
func (a *Archivalist) Query(ctx context.Context, query ArchiveQuery) ([]*Entry, error) {
    sessionID := ctx.Value("session_id").(string)

    // Apply session filtering based on query type
    if query.CurrentOnly {
        query.SessionIDs = []string{sessionID}
    } else if !query.CrossSession {
        // Default: current session only for non-promoted entries
        query.SessionIDs = []string{sessionID}
        // But include promoted entries from all sessions
        query.IncludePromoted = true
    }
    // CrossSession=true: query all sessions explicitly

    return a.store.Query(query)
}

// Archivalist session-aware storage
func (a *Archivalist) StoreEntry(ctx context.Context, entry *Entry) SubmissionResult {
    sessionID := ctx.Value("session_id").(string)

    // Tag entry with session
    entry.SessionID = sessionID

    // ... storage logic ...
}
```

### Orchestrator Session Awareness

```go
// Orchestrator session-scoped DAG execution
func (o *Orchestrator) Execute(ctx context.Context, dag *DAG) error {
    sessionID := ctx.Value("session_id").(string)
    session := o.sessionManager.Get(sessionID)

    // Update session with active DAG
    session.ActiveDAGID = dag.ID

    // Create session-scoped engineers
    for i := 0; i < o.config.MaxEngineers; i++ {
        engineer := o.createEngineer(sessionID, i)
        session.Context.ActiveAgents[engineer.ID] = &AgentState{
            ID:     engineer.ID,
            Status: "ready",
        }
    }

    // Execute with session context
    return o.executeDAG(ctx, dag, session)
}
```

---

## The Three Knowledge RAGs

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                            THREE KNOWLEDGE DOMAINS                                  │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌────────────────┬─────────────────────┬─────────────────────────────────────────┐ │
│  │ Agent          │ Question Answered   │ Session Behavior                        │ │
│  ├────────────────┼─────────────────────┼─────────────────────────────────────────┤ │
│  │                │                     │                                         │ │
│  │ LIBRARIAN      │ "What EXISTS?"      │ Session-independent (codebase is        │ │
│  │ (Local RAG)    │                     │ shared across all sessions)             │ │
│  │                │ Current codebase    │                                         │ │
│  │                │ state               │ Note: Tracks which files were read      │ │
│  │                │                     │ per-session for deduplication           │ │
│  │                │                     │                                         │ │
│  ├────────────────┼─────────────────────┼─────────────────────────────────────────┤ │
│  │                │                     │                                         │ │
│  │ ARCHIVALIST    │ "What was DONE?"    │ Session-scoped writes, cross-session    │ │
│  │ (Historical    │                     │ reads for promoted entries              │ │
│  │  RAG)          │ Past decisions,     │                                         │ │
│  │                │ solutions, outcomes │ Entry.SessionID tracks origin           │ │
│  │                │                     │ Entry.Promoted=true for global access   │ │
│  │                │                     │                                         │ │
│  ├────────────────┼─────────────────────┼─────────────────────────────────────────┤ │
│  │                │                     │                                         │ │
│  │ ACADEMIC       │ "What CAN be done?" │ Session-independent (research is        │ │
│  │ (External RAG) │                     │ globally applicable)                    │ │
│  │                │ World knowledge,    │                                         │ │
│  │                │ best practices      │ Results cached for cross-session reuse  │ │
│  │                │                     │                                         │ │
│  └────────────────┴─────────────────────┴─────────────────────────────────────────┘ │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Dynamic Tool Discovery Protocol

**CRITICAL: Agents MUST NOT rely on hardcoded tool lists. Tool discovery is dynamic and follows a cascading consultation pattern.**

The Engineer, Inspector, and Tester agents need to run code quality tools (linters, formatters, type checkers, test frameworks) but maintaining hardcoded tool registries is unsustainable. Instead, tools are discovered dynamically through a three-tier escalation protocol.

### Discovery Escalation Chain

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        DYNAMIC TOOL DISCOVERY PROTOCOL                               │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TIER 1: LIBRARIAN CONSULTATION ("What IS configured?")                             │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  Agent → Librarian: "What linters/formatters/test frameworks exist here?"    │   │
│  │                                                                              │   │
│  │  Librarian inspects:                                                         │   │
│  │  ├── Config files (.eslintrc, golangci.yml, pyproject.toml, etc.)            │   │
│  │  ├── Package manifests (package.json devDependencies, requirements-dev.txt) │   │
│  │  ├── CI/CD configs (.github/workflows/*.yml, Makefile, etc.)                 │   │
│  │  └── IDE settings (.vscode/settings.json, .idea/*.xml)                       │   │
│  │                                                                              │   │
│  │  Response includes:                                                          │   │
│  │  ├── Detected tools with exact versions                                      │   │
│  │  ├── Configuration file locations                                            │   │
│  │  ├── Run commands (from scripts, Makefile targets, etc.)                     │   │
│  │  └── Confidence score (HIGH if config found, LOW if inferred)                │   │
│  │                                                                              │   │
│  │  IF confidence >= HIGH: Use discovered tools directly                        │   │
│  │  IF confidence < HIGH: Escalate to Tier 2                                    │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                        │                                            │
│                                        ▼ (low confidence)                           │
│                                                                                     │
│  TIER 2: ACADEMIC RESEARCH ("What SHOULD be used?")                                 │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  Librarian → Academic: "What tools are recommended for [Go, TypeScript]?"    │   │
│  │                                                                              │   │
│  │  Academic researches:                                                        │   │
│  │  ├── Current best practices (2024-2025 recommendations)                      │   │
│  │  ├── Community standards (official style guides, popular choices)            │   │
│  │  ├── Tool maturity and maintenance status                                    │   │
│  │  └── Compatibility considerations                                            │   │
│  │                                                                              │   │
│  │  Response includes:                                                          │   │
│  │  ├── Recommended tools with rationale                                        │   │
│  │  ├── Installation commands                                                   │   │
│  │  ├── Default configurations                                                  │   │
│  │  └── Satisfactory flag (TRUE if clear best practice, FALSE if ambiguous)     │   │
│  │                                                                              │   │
│  │  IF satisfactory == TRUE: Use recommended tools                              │   │
│  │  IF satisfactory == FALSE: Escalate to Tier 3                                │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                        │                                            │
│                                        ▼ (unsatisfactory)                           │
│                                                                                     │
│  TIER 3: USER DECISION ("What do YOU want?")                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                              │   │
│  │  Guide → User: "Multiple options exist for TypeScript linting:"              │   │
│  │                                                                              │   │
│  │  Presents options with context:                                              │   │
│  │  ├── Option A: ESLint (most configurable, largest ecosystem)                 │   │
│  │  ├── Option B: Biome (fastest, all-in-one)                                   │   │
│  │  ├── Option C: TypeScript compiler only (minimal, built-in)                  │   │
│  │  └── Option D: User specifies custom tool                                    │   │
│  │                                                                              │   │
│  │  User selection is:                                                          │   │
│  │  ├── Applied immediately                                                     │   │
│  │  ├── Stored in Archivalist as user preference                                │   │
│  │  └── Used for future sessions (cross-session knowledge)                      │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Protocol Data Structures

```go
// ToolDiscoveryRequest initiates the discovery protocol
type ToolDiscoveryRequest struct {
    SessionID     string           `json:"session_id"`
    RequestingAgent string         `json:"requesting_agent"` // "engineer", "inspector", "tester"
    ToolCategory  ToolCategory     `json:"tool_category"`    // linter, formatter, type_checker, test_framework
    TargetPath    string           `json:"target_path"`      // File or directory to analyze
    Languages     []string         `json:"languages,omitempty"` // If already known
}

type ToolCategory string

const (
    ToolCategoryLinter      ToolCategory = "linter"
    ToolCategoryFormatter   ToolCategory = "formatter"
    ToolCategoryTypeChecker ToolCategory = "type_checker"
    ToolCategoryTestFramework ToolCategory = "test_framework"
    ToolCategoryLSP         ToolCategory = "lsp"
)

// ToolDiscoveryResponse returns discovered tools
type ToolDiscoveryResponse struct {
    Tier          int              `json:"tier"`             // 1, 2, or 3
    Confidence    ConfidenceLevel  `json:"confidence"`
    Tools         []DiscoveredTool `json:"tools"`
    RequiresUser  bool             `json:"requires_user"`    // True if Tier 3 escalation needed
    UserOptions   []ToolOption     `json:"user_options,omitempty"`
}

type ConfidenceLevel string

const (
    ConfidenceHigh   ConfidenceLevel = "high"   // Config file found
    ConfidenceMedium ConfidenceLevel = "medium" // Inferred from dependencies
    ConfidenceLow    ConfidenceLevel = "low"    // Language-only detection
)

// DiscoveredTool represents a tool found through discovery
type DiscoveredTool struct {
    Name          string            `json:"name"`
    Category      ToolCategory      `json:"category"`
    Language      string            `json:"language"`
    Version       string            `json:"version,omitempty"`
    ConfigPath    string            `json:"config_path,omitempty"`
    RunCommand    string            `json:"run_command"`
    InstallCmd    string            `json:"install_cmd,omitempty"`
    IsInstalled   bool              `json:"is_installed"`
    Source        string            `json:"source"` // "config_file", "package_manifest", "academic_research", "user_choice"
    Rationale     string            `json:"rationale,omitempty"`
}

// ToolOption for user selection (Tier 3)
type ToolOption struct {
    Tool          DiscoveredTool    `json:"tool"`
    Pros          []string          `json:"pros"`
    Cons          []string          `json:"cons"`
    Recommended   bool              `json:"recommended"`
}
```

### Librarian Detection Patterns

The Librarian uses pattern matching to detect existing tool configurations:

```go
// ToolDetectionPatterns maps config files to tools
var ToolDetectionPatterns = map[string]ToolDetectionRule{
    // Linters
    ".eslintrc":           {Tool: "eslint", Category: ToolCategoryLinter, Language: "javascript"},
    ".eslintrc.js":        {Tool: "eslint", Category: ToolCategoryLinter, Language: "javascript"},
    ".eslintrc.json":      {Tool: "eslint", Category: ToolCategoryLinter, Language: "javascript"},
    "eslint.config.js":    {Tool: "eslint", Category: ToolCategoryLinter, Language: "javascript"},
    ".golangci.yml":       {Tool: "golangci-lint", Category: ToolCategoryLinter, Language: "go"},
    ".golangci.yaml":      {Tool: "golangci-lint", Category: ToolCategoryLinter, Language: "go"},
    "ruff.toml":           {Tool: "ruff", Category: ToolCategoryLinter, Language: "python"},
    ".ruff.toml":          {Tool: "ruff", Category: ToolCategoryLinter, Language: "python"},
    "clippy.toml":         {Tool: "clippy", Category: ToolCategoryLinter, Language: "rust"},

    // Formatters
    ".prettierrc":         {Tool: "prettier", Category: ToolCategoryFormatter, Language: "javascript"},
    ".prettierrc.js":      {Tool: "prettier", Category: ToolCategoryFormatter, Language: "javascript"},
    "prettier.config.js":  {Tool: "prettier", Category: ToolCategoryFormatter, Language: "javascript"},
    "rustfmt.toml":        {Tool: "rustfmt", Category: ToolCategoryFormatter, Language: "rust"},
    ".editorconfig":       {Tool: "editorconfig", Category: ToolCategoryFormatter, Language: "*"},

    // Type checkers
    "tsconfig.json":       {Tool: "tsc", Category: ToolCategoryTypeChecker, Language: "typescript"},
    "pyrightconfig.json":  {Tool: "pyright", Category: ToolCategoryTypeChecker, Language: "python"},
    "mypy.ini":            {Tool: "mypy", Category: ToolCategoryTypeChecker, Language: "python"},

    // Test frameworks
    "jest.config.js":      {Tool: "jest", Category: ToolCategoryTestFramework, Language: "javascript"},
    "jest.config.ts":      {Tool: "jest", Category: ToolCategoryTestFramework, Language: "typescript"},
    "vitest.config.ts":    {Tool: "vitest", Category: ToolCategoryTestFramework, Language: "typescript"},
    "pytest.ini":          {Tool: "pytest", Category: ToolCategoryTestFramework, Language: "python"},
    "setup.cfg":           {Tool: "pytest", Category: ToolCategoryTestFramework, Language: "python", ParseSection: "[tool:pytest]"},
    ".mocharc.js":         {Tool: "mocha", Category: ToolCategoryTestFramework, Language: "javascript"},
}

// PackageManifestRules for inferring tools from dependencies
var PackageManifestRules = map[string][]PackageRule{
    "package.json": {
        {Package: "eslint", Tool: "eslint", Category: ToolCategoryLinter},
        {Package: "prettier", Tool: "prettier", Category: ToolCategoryFormatter},
        {Package: "jest", Tool: "jest", Category: ToolCategoryTestFramework},
        {Package: "vitest", Tool: "vitest", Category: ToolCategoryTestFramework},
        {Package: "mocha", Tool: "mocha", Category: ToolCategoryTestFramework},
        {Package: "typescript", Tool: "tsc", Category: ToolCategoryTypeChecker},
        {Package: "biome", Tool: "biome", Category: ToolCategoryLinter}, // Also formatter
    },
    "pyproject.toml": {
        {Package: "ruff", Tool: "ruff", Category: ToolCategoryLinter},
        {Package: "black", Tool: "black", Category: ToolCategoryFormatter},
        {Package: "pytest", Tool: "pytest", Category: ToolCategoryTestFramework},
        {Package: "mypy", Tool: "mypy", Category: ToolCategoryTypeChecker},
        {Package: "pyright", Tool: "pyright", Category: ToolCategoryTypeChecker},
    },
    "go.mod": {
        // Go tools are typically not in go.mod, check for tool binaries or Makefile
    },
    "Cargo.toml": {
        // Rust tools are typically via cargo, check for [dev-dependencies]
    },
}
```

### Caching and Persistence

Discovered tools are cached in the Archivalist to avoid repeated discovery:

```go
// ToolDiscoveryCache entry stored in Archivalist
type ToolDiscoveryCacheEntry struct {
    ID            string            `json:"id"`
    Category      Category          `json:"category"` // CategoryToolDiscovery
    SessionID     string            `json:"session_id"`
    ProjectPath   string            `json:"project_path"`
    DiscoveredAt  time.Time         `json:"discovered_at"`
    Tools         []DiscoveredTool  `json:"tools"`
    Tier          int               `json:"tier"`
    UserOverrides map[string]string `json:"user_overrides,omitempty"` // Category -> chosen tool
}

// Cache invalidation triggers
// - File change in config files (via Librarian file watcher)
// - User explicitly requests re-discovery
// - Session references different branch/commit
```

### Protocol Sequence Diagram

```
Engineer/Inspector/Tester              Librarian                    Academic                      User
         │                                │                            │                            │
         │  ToolDiscoveryRequest          │                            │                            │
         │  (category=linter, path=/src)  │                            │                            │
         │ ──────────────────────────────>│                            │                            │
         │                                │                            │                            │
         │                                │ [Scan for config files]    │                            │
         │                                │ [Parse package manifests]  │                            │
         │                                │                            │                            │
         │                     ┌──────────┴──────────┐                 │                            │
         │                     │ Config found?       │                 │                            │
         │                     └──────────┬──────────┘                 │                            │
         │                                │                            │                            │
         │               ┌────────────────┴────────────────┐           │                            │
         │               │                                 │           │                            │
         │             [YES]                             [NO]          │                            │
         │               │                                 │           │                            │
         │               │                                 │ "What tools for [Go]?"                 │
         │               │                                 │ ─────────────────────>│                │
         │               │                                 │                       │                │
         │               │                                 │            [Research] │                │
         │               │                                 │                       │                │
         │               │                                 │         ┌─────────────┴─────────────┐  │
         │               │                                 │         │ Clear best practice?      │  │
         │               │                                 │         └─────────────┬─────────────┘  │
         │               │                                 │                       │                │
         │               │                                 │         ┌─────────────┴─────────────┐  │
         │               │                                 │       [YES]                       [NO] │
         │               │                                 │         │                           │  │
         │               │                                 │<────────┘                           │  │
         │               │                                 │                                     │  │
         │               │                                 │              "Choose linter:"       │  │
         │               │                                 │              ─────────────────────────>│
         │               │                                 │                                     │  │
         │               │                                 │                          [User picks] │
         │               │                                 │<──────────────────────────────────────┘
         │               │                                 │                                        │
         │               ▼                                 ▼                                        │
         │<──────────────────────────────────────────────────                                       │
         │  ToolDiscoveryResponse (tools, commands, etc.)                                           │
         │                                                                                          │
         │ [Install if needed]                                                                      │
         │ [Run tool]                                                                               │
         │                                                                                          │
```

### Language Defaults Registry

When no tools are configured and the user doesn't specify preferences, these defaults are recommended. These represent modern, fast, well-maintained tools as of 2025.

```go
// LanguageDefaults defines recommended tools per language
// Used by Academic when research yields no clear best practice
// Used as recommendations when presenting options to users
var LanguageDefaults = map[string]LanguageToolset{
    "go": {
        PackageManager: "go mod",
        Linter:         ToolDefault{Name: "golangci-lint", InstallCmd: "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"},
        Formatter:      ToolDefault{Name: "gofmt", InstallCmd: ""}, // Built-in
        TypeChecker:    ToolDefault{Name: "go vet", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "gopls", InstallCmd: "go install golang.org/x/tools/gopls@latest"},
        TestFramework:  ToolDefault{Name: "go test", InstallCmd: ""}, // Built-in
    },
    "python": {
        PackageManager: "uv", // Preferred over pip for speed; fallback to pip if unavailable
        Linter:         ToolDefault{Name: "ruff", InstallCmd: "uv pip install ruff || pip install ruff"},
        Formatter:      ToolDefault{Name: "ruff", InstallCmd: "uv pip install ruff || pip install ruff"}, // ruff format
        TypeChecker:    ToolDefault{Name: "ruff", InstallCmd: "uv pip install ruff || pip install ruff"}, // ruff includes type checking
        LSP:            ToolDefault{Name: "ruff", InstallCmd: "uv pip install ruff-lsp || pip install ruff-lsp"}, // ruff-lsp
        TestFramework:  ToolDefault{Name: "pytest", InstallCmd: "uv pip install pytest || pip install pytest"},
    },
    "javascript": {
        PackageManager: "npm", // Or yarn/pnpm if detected
        Linter:         ToolDefault{Name: "oxlint", InstallCmd: "npm install -D oxlint"}, // Fastest JS linter
        Formatter:      ToolDefault{Name: "prettier", InstallCmd: "npm install -D prettier"},
        TypeChecker:    ToolDefault{Name: "tsc", InstallCmd: "npm install -D typescript"}, // For .ts files
        LSP:            ToolDefault{Name: "typescript-language-server", InstallCmd: "npm install -D typescript-language-server"},
        TestFramework:  ToolDefault{Name: "vitest", InstallCmd: "npm install -D vitest"}, // Modern, fast
    },
    "typescript": {
        PackageManager: "npm",
        Linter:         ToolDefault{Name: "oxlint", InstallCmd: "npm install -D oxlint"},
        Formatter:      ToolDefault{Name: "prettier", InstallCmd: "npm install -D prettier"},
        TypeChecker:    ToolDefault{Name: "tsc", InstallCmd: "npm install -D typescript"},
        LSP:            ToolDefault{Name: "typescript-language-server", InstallCmd: "npm install -D typescript-language-server"},
        TestFramework:  ToolDefault{Name: "vitest", InstallCmd: "npm install -D vitest"},
    },
    "rust": {
        PackageManager: "cargo",
        Linter:         ToolDefault{Name: "clippy", InstallCmd: "rustup component add clippy"},
        Formatter:      ToolDefault{Name: "rustfmt", InstallCmd: "rustup component add rustfmt"},
        TypeChecker:    ToolDefault{Name: "cargo check", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "rust-analyzer", InstallCmd: "rustup component add rust-analyzer"},
        TestFramework:  ToolDefault{Name: "cargo test", InstallCmd: ""}, // Built-in
    },
    "ruby": {
        PackageManager: "bundler",
        Linter:         ToolDefault{Name: "rubocop", InstallCmd: "gem install rubocop"},
        Formatter:      ToolDefault{Name: "rubocop", InstallCmd: "gem install rubocop"}, // rubocop -a
        TypeChecker:    ToolDefault{Name: "sorbet", InstallCmd: "gem install sorbet"},
        LSP:            ToolDefault{Name: "ruby-lsp", InstallCmd: "gem install ruby-lsp"},
        TestFramework:  ToolDefault{Name: "rspec", InstallCmd: "gem install rspec"},
    },
    "java": {
        PackageManager: "maven", // Or gradle if detected
        Linter:         ToolDefault{Name: "checkstyle", InstallCmd: ""}, // Usually via Maven/Gradle plugin
        Formatter:      ToolDefault{Name: "google-java-format", InstallCmd: ""},
        TypeChecker:    ToolDefault{Name: "javac", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "jdtls", InstallCmd: ""}, // Eclipse JDT Language Server
        TestFramework:  ToolDefault{Name: "junit", InstallCmd: ""}, // Via Maven/Gradle
    },
    "kotlin": {
        PackageManager: "gradle",
        Linter:         ToolDefault{Name: "ktlint", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "ktlint", InstallCmd: ""},
        TypeChecker:    ToolDefault{Name: "kotlinc", InstallCmd: ""},
        LSP:            ToolDefault{Name: "kotlin-language-server", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "junit", InstallCmd: ""},
    },
    "c": {
        PackageManager: "", // System-dependent
        Linter:         ToolDefault{Name: "clang-tidy", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "clang-format", InstallCmd: ""},
        TypeChecker:    ToolDefault{Name: "clang", InstallCmd: ""},
        LSP:            ToolDefault{Name: "clangd", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "ctest", InstallCmd: ""},
    },
    "cpp": {
        PackageManager: "", // System-dependent
        Linter:         ToolDefault{Name: "clang-tidy", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "clang-format", InstallCmd: ""},
        TypeChecker:    ToolDefault{Name: "clang", InstallCmd: ""},
        LSP:            ToolDefault{Name: "clangd", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "gtest", InstallCmd: ""},
    },
    "csharp": {
        PackageManager: "dotnet",
        Linter:         ToolDefault{Name: "dotnet format", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "dotnet format", InstallCmd: ""},
        TypeChecker:    ToolDefault{Name: "dotnet build", InstallCmd: ""},
        LSP:            ToolDefault{Name: "omnisharp", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "dotnet test", InstallCmd: ""},
    },
    "elixir": {
        PackageManager: "mix",
        Linter:         ToolDefault{Name: "credo", InstallCmd: "mix deps.get"},
        Formatter:      ToolDefault{Name: "mix format", InstallCmd: ""}, // Built-in
        TypeChecker:    ToolDefault{Name: "dialyzer", InstallCmd: ""},
        LSP:            ToolDefault{Name: "elixir-ls", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "mix test", InstallCmd: ""}, // Built-in
    },
    "bash": {
        PackageManager: "",
        Linter:         ToolDefault{Name: "shellcheck", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "shfmt", InstallCmd: "go install mvdan.cc/sh/v3/cmd/shfmt@latest"},
        TypeChecker:    ToolDefault{Name: "", InstallCmd: ""}, // N/A
        LSP:            ToolDefault{Name: "bash-language-server", InstallCmd: "npm install -g bash-language-server"},
        TestFramework:  ToolDefault{Name: "bats", InstallCmd: ""},
    },
    "php": {
        PackageManager: "composer",
        Linter:         ToolDefault{Name: "phpstan", InstallCmd: "composer require --dev phpstan/phpstan"},
        Formatter:      ToolDefault{Name: "php-cs-fixer", InstallCmd: "composer require --dev friendsofphp/php-cs-fixer"},
        TypeChecker:    ToolDefault{Name: "phpstan", InstallCmd: ""},
        LSP:            ToolDefault{Name: "intelephense", InstallCmd: "npm install -g intelephense"},
        TestFramework:  ToolDefault{Name: "phpunit", InstallCmd: "composer require --dev phpunit/phpunit"},
    },
    "swift": {
        PackageManager: "swift package",
        Linter:         ToolDefault{Name: "swiftlint", InstallCmd: "brew install swiftlint"},
        Formatter:      ToolDefault{Name: "swift-format", InstallCmd: "brew install swift-format"},
        TypeChecker:    ToolDefault{Name: "swiftc", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "sourcekit-lsp", InstallCmd: ""}, // Included with Xcode
        TestFramework:  ToolDefault{Name: "swift test", InstallCmd: ""}, // Built-in
    },
    "zig": {
        PackageManager: "zig",
        Linter:         ToolDefault{Name: "zig", InstallCmd: ""}, // zig build has lint-like features
        Formatter:      ToolDefault{Name: "zig fmt", InstallCmd: ""}, // Built-in
        TypeChecker:    ToolDefault{Name: "zig", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "zls", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "zig test", InstallCmd: ""}, // Built-in
    },
    "ocaml": {
        PackageManager: "opam",
        Linter:         ToolDefault{Name: "", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "ocamlformat", InstallCmd: "opam install ocamlformat"},
        TypeChecker:    ToolDefault{Name: "ocaml", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "ocaml-lsp", InstallCmd: "opam install ocaml-lsp-server"},
        TestFramework:  ToolDefault{Name: "alcotest", InstallCmd: "opam install alcotest"},
    },
    "dart": {
        PackageManager: "pub",
        Linter:         ToolDefault{Name: "dart analyze", InstallCmd: ""}, // Built-in
        Formatter:      ToolDefault{Name: "dart format", InstallCmd: ""}, // Built-in
        TypeChecker:    ToolDefault{Name: "dart analyze", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "dart", InstallCmd: ""}, // Built-in language server
        TestFramework:  ToolDefault{Name: "dart test", InstallCmd: ""}, // Built-in
    },
    "terraform": {
        PackageManager: "",
        Linter:         ToolDefault{Name: "tflint", InstallCmd: ""},
        Formatter:      ToolDefault{Name: "terraform fmt", InstallCmd: ""}, // Built-in
        TypeChecker:    ToolDefault{Name: "terraform validate", InstallCmd: ""}, // Built-in
        LSP:            ToolDefault{Name: "terraform-ls", InstallCmd: ""},
        TestFramework:  ToolDefault{Name: "terratest", InstallCmd: ""},
    },
    "nix": {
        PackageManager: "nix",
        Linter:         ToolDefault{Name: "statix", InstallCmd: "nix-env -iA nixpkgs.statix"},
        Formatter:      ToolDefault{Name: "nixfmt", InstallCmd: "nix-env -iA nixpkgs.nixfmt"},
        TypeChecker:    ToolDefault{Name: "", InstallCmd: ""},
        LSP:            ToolDefault{Name: "nixd", InstallCmd: "nix-env -iA nixpkgs.nixd"},
        TestFramework:  ToolDefault{Name: "", InstallCmd: ""},
    },
    "vue": {
        PackageManager: "npm",
        Linter:         ToolDefault{Name: "eslint", InstallCmd: "npm install -D eslint eslint-plugin-vue"},
        Formatter:      ToolDefault{Name: "prettier", InstallCmd: "npm install -D prettier"},
        TypeChecker:    ToolDefault{Name: "vue-tsc", InstallCmd: "npm install -D vue-tsc"},
        LSP:            ToolDefault{Name: "vue-language-server", InstallCmd: "npm install -D @vue/language-server"},
        TestFramework:  ToolDefault{Name: "vitest", InstallCmd: "npm install -D vitest"},
    },
    "svelte": {
        PackageManager: "npm",
        Linter:         ToolDefault{Name: "eslint", InstallCmd: "npm install -D eslint eslint-plugin-svelte"},
        Formatter:      ToolDefault{Name: "prettier", InstallCmd: "npm install -D prettier prettier-plugin-svelte"},
        TypeChecker:    ToolDefault{Name: "svelte-check", InstallCmd: "npm install -D svelte-check"},
        LSP:            ToolDefault{Name: "svelte-language-server", InstallCmd: "npm install -D svelte-language-server"},
        TestFramework:  ToolDefault{Name: "vitest", InstallCmd: "npm install -D vitest"},
    },
}

type LanguageToolset struct {
    PackageManager string
    Linter         ToolDefault
    Formatter      ToolDefault
    TypeChecker    ToolDefault
    LSP            ToolDefault
    TestFramework  ToolDefault
}

type ToolDefault struct {
    Name       string
    InstallCmd string
}
```

**Key Recommendations:**
- **Python**: Use `uv` for package management (10-100x faster than pip), `ruff` for everything else (linting, formatting, type checking via ruff-lsp)
- **JavaScript/TypeScript**: Use `oxlint` for linting (fastest), `prettier` for formatting, `vitest` for testing (modern, fast)
- **Go**: Built-in tools are excellent; `golangci-lint` aggregates multiple linters
- **Rust**: Official toolchain components are preferred (clippy, rustfmt, rust-analyzer)

### Integration with Agents

Each agent integrates with the protocol through a common skill:

```go
// discover_tools - Common skill for Engineer, Inspector, Tester
skills.NewSkill("discover_tools").
    Description("Discover appropriate tools for the target code via Librarian consultation").
    Domain("tooling").
    Keywords("discover", "tools", "linter", "formatter", "test").
    EnumParam("category", "Tool category", []string{"linter", "formatter", "type_checker", "test_framework", "lsp"}, true).
    StringParam("path", "Target path to analyze", true).
    BoolParam("force_refresh", "Bypass cache and re-discover", false)
```

---

## Phase-Based User Interaction

### Phase Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        PHASE-BASED USER INTERACTION                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  PHASE 0: SESSION MANAGEMENT (always available)                                     │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ SESSION MANAGER (via Guide)                                         │   │
│  │                                                                              │   │
│  │  User: "/session new rate-limiter-feature"                                   │   │
│  │  User: "/session list"                                                       │   │
│  │  User: "/session switch <id>"                                                │   │
│  │  User: "/session suspend"                                                    │   │
│  │  User: "/session resume <id>"                                                │   │
│  │                                                                              │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  PHASE 1: RESEARCH (optional, user-triggered)                                       │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ ACADEMIC                                                            │   │
│  │                                                                              │   │
│  │  User: "How would I design a distributed rate limiter?"                      │   │
│  │  Academic: [Research paper with recommendations]                             │   │
│  │                                                                              │   │
│  │  User: "I want to implement this" → transitions to PLANNING                  │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                     │                                               │
│                                     ▼                                               │
│  PHASE 2: PLANNING                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ ARCHITECT (primary)                                                 │   │
│  │                                                                              │   │
│  │  Architect presents implementation plan for approval                         │   │
│  │  Plan is session-scoped (stored in session context)                          │   │
│  │                                                                              │   │
│  │  User can:                                                                   │   │
│  │  - Approve → proceed to execution                                            │   │
│  │  - Modify → "Also add per-endpoint config"                                   │   │
│  │  - Query → "Why 3 levels?" (Architect explains)                              │   │
│  │  - Query codebase → "Show me existing middleware" (→ Librarian, returns)     │   │
│  │  - Switch session → work is preserved                                        │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                     │                                               │
│                                     ▼                                               │
│  PHASE 3: EXECUTION                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ ARCHITECT (status updates only)                                     │   │
│  │                                                                              │   │
│  │  Architect provides progress updates                                         │   │
│  │  Progress tracked in session context                                         │   │
│  │                                                                              │   │
│  │  User can INTERRUPT at any time:                                             │   │
│  │  - "Stop" → Architect halts, awaits instructions                             │   │
│  │  - "Actually, also add X" → Architect revises plan                           │   │
│  │  - "What's taking so long?" → Architect explains current state               │   │
│  │  - "/session suspend" → Preserve state, switch to other work                 │   │
│  │                                                                              │   │
│  │  If Engineer needs clarification:                                            │   │
│  │  - Engineer → Orchestrator → ARCHITECT → User                                │   │
│  │  - User responds to Architect (never sees Engineer)                          │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                     │                                               │
│                                     ▼                                               │
│  PHASE 4: INSPECTION                                                                │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ INSPECTOR (primary for results review)                              │   │
│  │  User ←→ ARCHITECT (for fix approval)                                        │   │
│  │                                                                              │   │
│  │  Inspector presents findings                                                 │   │
│  │  Issues recorded in session context                                          │   │
│  │                                                                              │   │
│  │  User can:                                                                   │   │
│  │  - Ask Inspector for details → "Explain the race condition"                  │   │
│  │  - Override → "Ignore the per-endpoint config for now"                       │   │
│  │  - Approve fixes → "Fix all of these"                                        │   │
│  │                                                                              │   │
│  │  On fix approval:                                                            │   │
│  │  - Inspector → ARCHITECT (with corrections)                                  │   │
│  │  - Architect presents fix plan to user                                       │   │
│  │  - User approves → back to EXECUTION phase                                   │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                     │                                               │
│                                     ▼                                               │
│  PHASE 5: TESTING                                                                   │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ TESTER (primary for test planning/results)                          │   │
│  │  User ←→ ARCHITECT (for implementation fix approval)                         │   │
│  │                                                                              │   │
│  │  Tester presents test plan for approval                                      │   │
│  │                                                                              │   │
│  │  User can:                                                                   │   │
│  │  - Approve → Tester → Architect (test DAG) → execution                       │   │
│  │  - Modify → "Also add a test for X"                                          │   │
│  │  - Skip tests → "Skip tests for now"                                         │   │
│  │                                                                              │   │
│  │  After test execution:                                                       │   │
│  │  - Tester presents results                                                   │   │
│  │  - If failures, Tester explains if test or implementation issue              │   │
│  │  - Implementation issues → Architect for fix workflow                        │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                     │                                               │
│                                     ▼                                               │
│  PHASE 6: COMPLETE                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ ARCHITECT (final summary)                                           │   │
│  │                                                                              │   │
│  │  Architect presents completion summary:                                      │   │
│  │  - Files created/modified                                                    │   │
│  │  - Tests passed                                                              │   │
│  │  - Validation status                                                         │   │
│  │  - Deferred items (user overrides)                                           │   │
│  │                                                                              │   │
│  │  Session transitions to COMPLETED state                                      │   │
│  │  Patterns/decisions promoted to global Archivalist                           │   │
│  └──────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Message Envelope

### Session-Aware Message

```go
// core/messaging/message.go

type Message[T any] struct {
    ID            string        `json:"id"`
    CorrelationID string        `json:"correlation_id,omitempty"`
    ParentID      string        `json:"parent_id,omitempty"`

    // Session context (REQUIRED for most messages)
    SessionID     string        `json:"session_id"`

    Source string      `json:"source"`
    Target string      `json:"target,omitempty"`
    Type   MessageType `json:"type"`

    Payload   T          `json:"payload"`
    Timestamp time.Time  `json:"timestamp"`
    Deadline  *time.Time `json:"deadline,omitempty"`
    TTL       time.Duration `json:"ttl,omitempty"`

    Status      MessageStatus `json:"status"`
    Attempt     int           `json:"attempt"`
    MaxAttempts int           `json:"max_attempts,omitempty"`

    Priority  Priority       `json:"priority"`
    Metadata  map[string]any `json:"metadata,omitempty"`
    Error     string         `json:"error,omitempty"`
    ProcessedAt *time.Time   `json:"processed_at,omitempty"`
}
```

### Message Types

```
USER_INPUT              User message, needs intent classification
USER_RESPONSE           User response to agent question

SESSION_CREATE          Create new session
SESSION_SWITCH          Switch active session
SESSION_SUSPEND         Suspend current session
SESSION_RESUME          Resume suspended session
SESSION_COMPLETE        Complete session

RESEARCH_REQUEST        Query for Academic
RESEARCH_RESPONSE       Academic's research paper

CONTEXT_REQUEST         Query for Librarian
CONTEXT_RESPONSE        Librarian's codebase context

HISTORY_REQUEST         Query for Archivalist
HISTORY_RESPONSE        Archivalist's historical context
STORE_REQUEST           Store data in Archivalist

PLAN_REQUEST            Request Architect to create plan
PLAN_RESPONSE           Architect's plan (to User for approval)
PLAN_APPROVED           User approved plan
PLAN_MODIFIED           User wants changes

DAG_EXECUTE             Architect → Orchestrator: execute this DAG
DAG_STATUS              Orchestrator → Architect: status update

TASK_DISPATCH           Orchestrator → Engineer: do this task
TASK_COMPLETE           Engineer → Orchestrator: task done
TASK_FAILED             Engineer → Orchestrator: task failed
TASK_HELP               Engineer → Orchestrator: need clarification

CLARIFICATION_REQUEST   Architect → User: need input
CLARIFICATION_RESPONSE  User → Architect: here's the answer

VALIDATE_TASK           Orchestrator → Inspector: validate this
VALIDATION_RESULT       Inspector → Orchestrator: pass/fail
VALIDATION_FULL         Inspector → User: full validation results
VALIDATION_CORRECTIONS  Inspector → Architect: fixes needed

TEST_PLAN_REQUEST       Architect → Tester: create test plan
TEST_PLAN_RESPONSE      Tester → User: proposed tests
TEST_DAG_REQUEST        Tester → Architect: implement these tests
TESTS_READY             Orchestrator → Tester: tests implemented
TEST_RESULTS            Tester → User: test results
TEST_CORRECTIONS        Tester → Architect: impl fixes needed

USER_OVERRIDE           User → Inspector/Tester: ignore this issue
USER_INTERRUPT          User → Architect: stop, I want to change

WORKFLOW_COMPLETE       Architect → User: all done, summary
```

---

## DAG Planning (Architect Skill)

### DAG Output Schema

```json
{
  "id": "workflow-uuid",
  "session_id": "session-uuid",
  "prompt": "Original user request",
  "nodes": [
    {
      "id": "t1",
      "agent": "librarian",
      "prompt": "Index current middleware patterns",
      "context": {
        "relevant_files": ["src/middleware/"],
        "reason": "Need to understand existing patterns"
      },
      "metadata": {
        "priority": "high",
        "timeout_ms": 60000,
        "retry": {"max": 1, "backoff_ms": 1000},
        "file_operations": ["read"]
      },
      "depends_on": []
    },
    {
      "id": "t2",
      "agent": "engineer",
      "prompt": "Create rate limiter interface following existing middleware pattern",
      "context": {
        "upstream_outputs": ["t1"],
        "constraints": ["Follow existing middleware pattern"]
      },
      "metadata": {
        "priority": "normal",
        "file_operations": ["create", "write"]
      },
      "depends_on": ["t1"]
    }
  ],
  "execution_order": [
    ["t1"],
    ["t2", "t3"],
    ["t4"]
  ],
  "policy": {
    "max_concurrency": 4,
    "fail_fast": true,
    "default_retry": {"max": 2, "backoff_ms": 2000}
  }
}
```

---

## Pipeline Architecture

**CRITICAL: Pipelines are isolated execution contexts that enable tight feedback loops for individual tasks while preserving the session-wide quality assurance flow.**

### Two-Level Quality Assurance

Sylk implements quality assurance at TWO levels:

1. **Pipeline-Level (task-specific)**: Direct feedback loops within an isolated context
2. **Session-Level (integration)**: Full validation after all pipelines complete, routed through Architect

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        TWO-LEVEL QUALITY ASSURANCE                                   │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  LEVEL 1: PIPELINE-INTERNAL (per-task, direct feedback)                             │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │                                                                               │  │
│  │   Engineer ◄─────────────────────────────────────────────────┐                │  │
│  │      │                                                       │                │  │
│  │      │ completes task                                        │                │  │
│  │      ▼                                                       │                │  │
│  │   Inspector (task-specific)                                  │                │  │
│  │      │ - Lint this file                                      │                │  │
│  │      │ - Format this file                                    │                │  │
│  │      │ - Type-check this file                                │                │  │
│  │      │ - Task compliance check                               │                │  │
│  │      ▼                                                       │                │  │
│  │   ┌──────┐                                                   │                │  │
│  │   │ PASS?├──No──► DIRECT FEEDBACK (no Architect) ────────────┘                │  │
│  │   └──┬───┘                                                                    │  │
│  │      │ Yes                                                                    │  │
│  │      ▼                                                       ┌────────────────┘  │
│  │   Tester (task-specific)                                     │                   │
│  │      │ - Generate tests for THIS requirement                 │                   │
│  │      │ - Run tests for THIS task only                        │                   │
│  │      ▼                                                       │                   │
│  │   ┌──────┐                                                   │                   │
│  │   │ PASS?├──No──► DIRECT FEEDBACK (no Architect) ────────────┘                   │
│  │   └──┬───┘                                                                       │
│  │      │ Yes                                                                       │
│  │      ▼                                                                           │
│  │   PIPELINE COMPLETE                                                              │
│  │                                                                                  │
│  └──────────────────────────────────────────────────────────────────────────────────┘
│                                                                                     │
│  LEVEL 2: SESSION-WIDE (post-DAG, through Architect)                                │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │                                                                               │  │
│  │   ALL Pipelines Complete                                                      │  │
│  │      │                                                                        │  │
│  │      ▼                                                                        │  │
│  │   Orchestrator → Architect: "DAG execution done"                              │  │
│  │      │                                                                        │  │
│  │      ▼                                                                        │  │
│  │   INSPECTOR (session-wide)                                                    │  │
│  │      │ - Full scan of ALL changes together                                    │  │
│  │      │ - Integration validation                                               │  │
│  │      │ - Cross-cutting concerns (security, patterns)                          │  │
│  │      │ - Codebase-wide compliance                                             │  │
│  │      ▼                                                                        │  │
│  │   ┌──────┐                                                                    │  │
│  │   │ PASS?├──No──► Architect creates FIX DAG ──► New Pipelines ──► Loop        │  │
│  │   └──┬───┘                                                                    │  │
│  │      │ Yes                                                                    │  │
│  │      ▼                                                                        │  │
│  │   TESTER (session-wide)                                                       │  │
│  │      │ - Full test suite (integration, regression, e2e)                       │  │
│  │      │ - Cross-change validation                                              │  │
│  │      │ - Tests affected by ANY change in DAG                                  │  │
│  │      ▼                                                                        │  │
│  │   ┌──────┐                                                                    │  │
│  │   │ PASS?├──No──► Architect creates FIX DAG ──► New Pipelines ──► Loop        │  │
│  │   └──┬───┘                                                                    │  │
│  │      │ Yes                                                                    │  │
│  │      ▼                                                                        │  │
│  │   WORKFLOW COMPLETE → Architect → User                                        │  │
│  │                                                                               │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Pipeline vs Session-Level QA Scope

| Concern | Pipeline-Level | Session-Level |
|---------|----------------|---------------|
| **Scope** | Single task's changes | All DAG changes combined |
| **Feedback routing** | Direct to Engineer | Through Architect |
| **Creates new DAG?** | No (internal loop) | Yes (fix DAG) |
| **Inspector focus** | File-level lint/format/types | Integration & cross-cutting |
| **Tester focus** | Task-specific unit tests | Full suite, regression, e2e |
| **User override** | `/task <id>` can ignore | `USER_OVERRIDE` message |

### Pipeline Data Model

```go
// core/pipeline/pipeline.go

type PipelineID string

type PipelineState string

const (
    PipelineStateCreated    PipelineState = "created"
    PipelineStateRunning    PipelineState = "running"
    PipelineStateInspecting PipelineState = "inspecting"
    PipelineStateTesting    PipelineState = "testing"
    PipelineStateCompleted  PipelineState = "completed"
    PipelineStateFailed     PipelineState = "failed"
)

// Pipeline is an isolated execution context containing Engineer + Inspector + Tester
type Pipeline struct {
    ID            PipelineID             `json:"id"`
    SessionID     string                 `json:"session_id"`
    DAGID         string                 `json:"dag_id"`
    TaskID        string                 `json:"task_id"`

    // State
    State         PipelineState          `json:"state"`
    CreatedAt     time.Time              `json:"created_at"`
    CompletedAt   *time.Time             `json:"completed_at,omitempty"`

    // Co-located agents (pipeline-scoped instances)
    EngineerID    string                 `json:"engineer_id"`
    InspectorID   string                 `json:"inspector_id"`
    TesterID      string                 `json:"tester_id"`

    // Task context (shared by all three agents)
    Context       *PipelineContext       `json:"context"`

    // Iteration tracking
    InspectorLoops int                   `json:"inspector_loops"`
    TesterLoops    int                   `json:"tester_loops"`
    MaxLoops       int                   `json:"max_loops"`

    // Results
    EngineerResult  *EngineerResult      `json:"engineer_result,omitempty"`
    InspectorResult *InspectorResult     `json:"inspector_result,omitempty"`
    TesterResult    *TesterResult        `json:"tester_result,omitempty"`
}

// PipelineContext is shared state within a pipeline (all three agents can access)
type PipelineContext struct {
    // Task definition
    TaskPrompt       string              `json:"task_prompt"`
    TaskConstraints  []string            `json:"task_constraints,omitempty"`
    ComplianceCriteria []string          `json:"compliance_criteria,omitempty"`

    // Upstream context (from DAG dependencies)
    UpstreamOutputs  map[string]any      `json:"upstream_outputs,omitempty"`

    // Files touched by this pipeline
    ModifiedFiles    []string            `json:"modified_files,omitempty"`
    CreatedFiles     []string            `json:"created_files,omitempty"`

    // Feedback history (for context in subsequent loops)
    InspectorFeedback []InspectorFeedback `json:"inspector_feedback,omitempty"`
    TesterFeedback    []TesterFeedback    `json:"tester_feedback,omitempty"`
}

// InspectorFeedback is direct feedback from Inspector to Engineer
type InspectorFeedback struct {
    Loop        int                     `json:"loop"`
    Timestamp   time.Time               `json:"timestamp"`
    Issues      []InspectorIssue        `json:"issues"`
    Passed      bool                    `json:"passed"`
}

type InspectorIssue struct {
    File        string                  `json:"file"`
    Line        int                     `json:"line,omitempty"`
    Category    string                  `json:"category"` // lint, format, type, compliance
    Severity    string                  `json:"severity"` // error, warning
    Message     string                  `json:"message"`
    Suggestion  string                  `json:"suggestion,omitempty"`
}

// TesterFeedback is direct feedback from Tester to Engineer
type TesterFeedback struct {
    Loop        int                     `json:"loop"`
    Timestamp   time.Time               `json:"timestamp"`
    TestsRun    int                     `json:"tests_run"`
    TestsPassed int                     `json:"tests_passed"`
    Failures    []TestFailure           `json:"failures,omitempty"`
    Passed      bool                    `json:"passed"`
}

type TestFailure struct {
    TestName    string                  `json:"test_name"`
    File        string                  `json:"file"`
    Message     string                  `json:"message"`
    Expected    string                  `json:"expected,omitempty"`
    Actual      string                  `json:"actual,omitempty"`
    StackTrace  string                  `json:"stack_trace,omitempty"`
}
```

### Pipeline Internal Bus

Pipelines use a dedicated internal bus for direct Engineer ↔ Inspector ↔ Tester communication that bypasses the Guide:

```go
// core/pipeline/bus.go

// PipelineBus handles direct communication within a pipeline
// This is NOT routed through Guide - it's pipeline-internal only
type PipelineBus struct {
    pipelineID PipelineID

    // Direct feedback channels
    inspectorToEngineer chan *InspectorFeedback
    testerToEngineer    chan *TesterFeedback

    // Control channels
    engineerDone        chan *EngineerResult
    inspectorDone       chan *InspectorResult
    testerDone          chan *TesterResult

    // Cancellation
    ctx    context.Context
    cancel context.CancelFunc

    closed atomic.Bool
}

func NewPipelineBus(ctx context.Context, pipelineID PipelineID) *PipelineBus {
    ctx, cancel := context.WithCancel(ctx)
    return &PipelineBus{
        pipelineID:          pipelineID,
        inspectorToEngineer: make(chan *InspectorFeedback, 8),
        testerToEngineer:    make(chan *TesterFeedback, 8),
        engineerDone:        make(chan *EngineerResult, 1),
        inspectorDone:       make(chan *InspectorResult, 1),
        testerDone:          make(chan *TesterResult, 1),
        ctx:                 ctx,
        cancel:              cancel,
    }
}

// SendInspectorFeedback sends feedback directly to Engineer (no Guide routing)
func (b *PipelineBus) SendInspectorFeedback(feedback *InspectorFeedback) error {
    if b.closed.Load() {
        return ErrPipelineClosed
    }
    select {
    case b.inspectorToEngineer <- feedback:
        return nil
    case <-b.ctx.Done():
        return b.ctx.Err()
    }
}

// SendTesterFeedback sends feedback directly to Engineer (no Guide routing)
func (b *PipelineBus) SendTesterFeedback(feedback *TesterFeedback) error {
    if b.closed.Load() {
        return ErrPipelineClosed
    }
    select {
    case b.testerToEngineer <- feedback:
        return nil
    case <-b.ctx.Done():
        return b.ctx.Err()
    }
}
```

### Pipeline Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                           PIPELINE LIFECYCLE                                         │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Orchestrator receives TASK_DISPATCH for engineer task                              │
│      │                                                                              │
│      ▼                                                                              │
│  ┌──────────┐                                                                       │
│  │ CREATED  │  Pipeline created with:                                               │
│  │          │  - Engineer instance                                                  │
│  └────┬─────┘  - Inspector instance (task-scoped)                                   │
│       │        - Tester instance (task-scoped)                                      │
│       │        - Shared PipelineContext                                             │
│       │        - Internal PipelineBus                                               │
│       ▼                                                                             │
│  ┌──────────┐                                                                       │
│  │ RUNNING  │  Engineer executes task                                               │
│  │          │  - Reads/writes files                                                 │
│  │          │  - Updates PipelineContext.ModifiedFiles                              │
│  └────┬─────┘                                                                       │
│       │                                                                             │
│       ▼                                                                             │
│  ┌────────────┐                                                                     │
│  │ INSPECTING │  Inspector validates Engineer's output                              │
│  │            │  - Runs lint/format/type checks on ModifiedFiles                    │
│  │            │  - Checks task compliance                                           │
│  └────┬───────┘                                                                     │
│       │                                                                             │
│       ├── Issues found & loops < max ──► SendInspectorFeedback ──► RUNNING          │
│       │                                                                             │
│       ├── Issues found & loops >= max ──► User prompted (ignore/fail)               │
│       │                                                                             │
│       ▼                                                                             │
│  ┌──────────┐                                                                       │
│  │ TESTING  │  Tester generates & runs task-specific tests                          │
│  │          │  - Creates tests for THIS requirement                                 │
│  │          │  - Runs only relevant tests                                           │
│  └────┬─────┘                                                                       │
│       │                                                                             │
│       ├── Failures & loops < max ──► SendTesterFeedback ──► RUNNING                 │
│       │                                                                             │
│       ├── Failures & loops >= max ──► User prompted (ignore/fail)                   │
│       │                                                                             │
│       ▼                                                                             │
│  ┌───────────┐                                                                      │
│  │ COMPLETED │  Pipeline done, results sent to Orchestrator                         │
│  └───────────┘                                                                      │
│                                                                                     │
│  At any point:                                                                      │
│  ├── User /task <id> ──► Guide routes to Pipeline ──► Engineer receives             │
│  ├── Unrecoverable error ──► FAILED                                                 │
│  └── User cancellation ──► FAILED                                                   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Pipeline Manager

```go
// core/pipeline/manager.go

type PipelineManager interface {
    // Lifecycle
    Create(ctx context.Context, cfg CreatePipelineConfig) (*Pipeline, error)
    Start(ctx context.Context, id PipelineID) error
    Cancel(ctx context.Context, id PipelineID) error

    // Queries
    Get(id PipelineID) (*Pipeline, bool)
    GetBySession(sessionID string) []*Pipeline
    GetByDAG(dagID string) []*Pipeline
    GetActive() []*Pipeline

    // User interaction (via Guide)
    RouteUserMessage(pipelineID PipelineID, msg string) error

    // Results
    GetResult(id PipelineID) (*PipelineResult, error)

    // Cleanup
    CloseAll() error
}

type CreatePipelineConfig struct {
    SessionID          string
    DAGID              string
    TaskID             string
    TaskPrompt         string
    TaskConstraints    []string
    ComplianceCriteria []string
    UpstreamOutputs    map[string]any
    MaxLoops           int  // Default: 3
}

type PipelineResult struct {
    PipelineID      PipelineID          `json:"pipeline_id"`
    Success         bool                `json:"success"`
    EngineerResult  *EngineerResult     `json:"engineer_result"`
    InspectorResult *InspectorResult    `json:"inspector_result"`
    TesterResult    *TesterResult       `json:"tester_result"`
    ModifiedFiles   []string            `json:"modified_files"`
    CreatedFiles    []string            `json:"created_files"`
    LoopsUsed       int                 `json:"loops_used"`
    Duration        time.Duration       `json:"duration"`
}
```

### Guide Integration for /task Command

The Guide routes user messages to specific pipelines via the `/task` command:

```go
// Guide skill for pipeline interaction
{
    Name:        "task_interact",
    Description: "Route user message to a specific pipeline's engineer",
    Domain:      "routing",
    Keywords:    []string{"/task"},
    Priority:    100,
    Parameters: []Param{
        {Name: "pipeline_id", Type: "string", Required: true, Description: "Pipeline ID or index"},
        {Name: "action", Type: "enum", Values: []string{"prompt", "query", "interrupt", "ignore_inspector", "ignore_tester"}, Required: true},
        {Name: "message", Type: "string", Required: false},
    },
}
```

User interaction examples:
```
/task 1 prompt "Focus on error handling first"     → Guide → Pipeline 1 → Engineer
/task 2 query "What files have you modified?"      → Guide → Pipeline 2 → Engineer
/task 1 interrupt                                  → Guide → Pipeline 1 → Pause
/task 3 ignore_inspector                           → Guide → Pipeline 3 → Skip inspector loop
/task 2 ignore_tester                              → Guide → Pipeline 2 → Skip tester loop
```

### Orchestrator Changes for Pipelines

The Orchestrator's task dispatch now creates pipelines instead of bare engineers:

```go
// Before (current): Orchestrator spawns Engineer directly
func (o *Orchestrator) dispatchTask(task *DAGNode) error {
    engineer := o.engineerPool.Acquire()
    result, err := engineer.Execute(task)
    // ... handle result, report to Architect
}

// After (with pipelines): Orchestrator creates Pipeline
func (o *Orchestrator) dispatchTask(task *DAGNode) error {
    pipeline, err := o.pipelineManager.Create(ctx, CreatePipelineConfig{
        SessionID:          task.SessionID,
        DAGID:              task.DAGID,
        TaskID:             task.ID,
        TaskPrompt:         task.Prompt,
        TaskConstraints:    task.Constraints,
        ComplianceCriteria: task.ComplianceCriteria,
        UpstreamOutputs:    o.gatherUpstreamOutputs(task),
        MaxLoops:           3,
    })
    if err != nil {
        return err
    }

    // Start pipeline (runs Engineer → Inspector → Tester loop internally)
    if err := o.pipelineManager.Start(ctx, pipeline.ID); err != nil {
        return err
    }

    // Wait for pipeline completion
    result, err := o.pipelineManager.GetResult(pipeline.ID)
    // ... handle result, report to Architect
}
```

### Message Types

New pipeline-internal messages (NOT routed through Guide):
```
INSPECTOR_FEEDBACK      Inspector → Engineer: task-specific issues (pipeline-internal)
TESTER_FEEDBACK         Tester → Engineer: task-specific test failures (pipeline-internal)
PIPELINE_COMPLETE       Pipeline → Orchestrator: all loops passed
PIPELINE_FAILED         Pipeline → Orchestrator: max loops exceeded or error
```

Existing messages (unchanged - used for session-level QA):
```
INSPECTION_REQUEST      Architect → Inspector: validate ALL changes (session-wide)
INSPECTION_RESULTS      Inspector → Architect: session-wide issues found
TEST_PLAN_REQUEST       Architect → Tester: create test plan (session-wide)
TEST_RESULTS            Tester → Architect: session-wide test results
TEST_CORRECTIONS        Tester → Architect: impl fixes needed (creates fix DAG)
```

New Guide-routed messages:
```
USER_TASK_PROMPT        User → Guide → Pipeline: prompt specific engineer
USER_TASK_QUERY         User → Guide → Pipeline: query specific engineer
USER_TASK_INTERRUPT     User → Guide → Pipeline: interrupt pipeline
USER_IGNORE_INSPECTOR   User → Guide → Pipeline: skip inspector for this pipeline
USER_IGNORE_TESTER      User → Guide → Pipeline: skip tester for this pipeline
```

---

## Agent Memory Management

Each agent has specific memory management strategies based on their role, model, and context window characteristics. All checkpoints and compaction summaries are submitted to the Archivalist for persistence.

### Agent Model Assignments

| Agent | Model | Context Strategy |
|-------|-------|------------------|
| **Librarian** | (TBD) | Frequent checkpoints (25%, 50%, 75%), compact at 75% |
| **Guide** | (TBD) | Routing-focused checkpoints (50%, 75%, 90%), compact at 95% |
| **Academic** | Opus 4.5 | Research paper at 85%, compact at 95% |
| **Architect** | OpenAI Codex 5.2 | Workflow/plan at 85%, compact at 95% |
| **Engineer** | Opus 4.5 | **PIPELINE HANDOFF at 95%** (special case) |
| **Inspector** | OpenAI Codex 5.2 | Findings summary at 85%, compact at 95% |
| **Tester** | OpenAI Codex 5.2 | Test summary at 85%, compact at 95% |

### Memory Management Summary

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                     AGENT MEMORY MANAGEMENT THRESHOLDS                               │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  LIBRARIAN:    25%──────50%──────75%                                                │
│                 │        │        │                                                 │
│                 ▼        ▼        ▼                                                 │
│              CKPT     CKPT    CKPT+COMPACT                                          │
│                                                                                     │
│  GUIDE:                 50%──────75%──────90%──────95%                              │
│                          │        │        │        │                               │
│                          ▼        ▼        ▼        ▼                               │
│                        CKPT     CKPT     CKPT    COMPACT                            │
│                                                                                     │
│  ACADEMIC:                               85%──────95%                               │
│  (Opus 4.5)                               │        │                                │
│                                           ▼        ▼                                │
│                                      RESEARCH   COMPACT                             │
│                                       PAPER                                         │
│                                                                                     │
│  ARCHITECT:                              85%──────95%                               │
│  (Codex 5.2)                              │        │                                │
│                                           ▼        ▼                                │
│                                       WORKFLOW  COMPACT                             │
│                                        + PLAN                                       │
│                                                                                     │
│  ENGINEER:                                       95%                                │
│  (Opus 4.5)                                       │                                 │
│                                                   ▼                                 │
│                                           *** PIPELINE ***                          │
│                                           *** HANDOFF  ***                          │
│                                                                                     │
│  INSPECTOR:                              85%──────95%                               │
│  (Codex 5.2)                              │        │                                │
│                                           ▼        ▼                                │
│                                       FINDINGS  COMPACT                             │
│                                       SUMMARY   (local)                             │
│                                                                                     │
│  TESTER:                                 85%──────95%                               │
│  (Codex 5.2)                              │        │                                │
│                                           ▼        ▼                                │
│                                         TEST    COMPACT                             │
│                                       SUMMARY   (local)                             │
│                                                                                     │
│  All checkpoints/summaries → ARCHIVALIST (persistent storage)                       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

### Librarian Memory Management

The Librarian continuously learns about the codebase through indexing and queries. This knowledge must be persisted to survive context window limits.

**Thresholds**: 25%, 50%, 75% (checkpoint) | 75% (compact)

**Checkpoint Summary (Onboarding-Style)**:

```go
type CodebaseSummary struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`
    CheckpointIndex    int               `json:"checkpoint_index"`

    // Onboarding information
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

**Archivalist Category**: `librarian_checkpoint`

**Consult Archivalist For**: Agent activity (what files changed, what other agents did)

---

### Librarian Query Caching (Intent-Aware)

The Librarian handles two fundamentally different query types that require different caching strategies:

**Specific Queries** (Location-based):
- "where is the auth code"
- "show me the User struct"
- "find the CreateSession function"
- Answer: A specific location in the codebase

**Abstract Queries** (Pattern-based):
- "what is our caching strategy"
- "how do we handle errors in this repo"
- "what patterns do we use for authentication"
- Answer: Synthesized understanding from multiple sources

#### Why Simple Embedding Similarity Fails

With typical embedding models and a 0.95 similarity threshold:

| Query A | Query B | Typical Similarity |
|---------|---------|-------------------|
| "where can I find the auth code" | "where is authentication located" | ~0.82-0.88 |
| "what is our caching strategy" | "how do we handle caching" | ~0.78-0.85 |
| "where is X" | "find X" | ~0.85-0.90 |

**A 0.95 threshold misses most natural language variations.** Users don't ask the same question the same way twice.

#### Intent-Aware Caching Architecture

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         LIBRARIAN QUERY PROCESSING PIPELINE                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  User Query: "what patterns do we use for error handling"                           │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  STEP 1: Intent Classification (provided by Guide during routing)           │   │
│  │                                                                             │   │
│  │  Intent Types:                                                              │   │
│  │    LOCATE  - "where is", "find", "show me", "which file"                   │   │
│  │    PATTERN - "strategy", "approach", "pattern", "how do we"                │   │
│  │    EXPLAIN - "how does", "explain", "what does X do"                       │   │
│  │    GENERAL - other codebase questions                                       │   │
│  │                                                                             │   │
│  │  Result: Intent = PATTERN                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  STEP 2: Subject/Concept Extraction (provided by Guide)                     │   │
│  │                                                                             │   │
│  │  Query: "what patterns do we use for error handling"                        │   │
│  │                           │                                                 │   │
│  │                           ▼                                                 │   │
│  │  Concept: "error_handling"                                                  │   │
│  │  Related: ["errors", "error patterns", "exception handling"]               │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│       │                                                                             │
│       ▼                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  STEP 3: Intent-Specific Cache Lookup                                       │   │
│  │                                                                             │   │
│  │  LOCATE:  Key = entity name, Invalidate = file change                      │   │
│  │  PATTERN: Key = concept, Invalidate = TTL + structural change              │   │
│  │  EXPLAIN: Key = subject, Invalidate = file change + TTL                    │   │
│  │  GENERAL: Key = embedding (0.80 threshold), Invalidate = TTL               │   │
│  │                                                                             │   │
│  │  Cache Key: "pattern:error_handling"                                        │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│       │                                                                             │
│       ▼                                                                             │
│  ┌───────────────────────────────┐    ┌────────────────────────────────────────┐   │
│  │  CACHE HIT                    │    │  CACHE MISS                            │   │
│  │  Return cached response       │    │  Synthesize → Cache → Return           │   │
│  │  (0 tokens, <5ms)            │    │  (2,500-10,000 tokens, 500-2000ms)     │   │
│  └───────────────────────────────┘    └────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Cache Strategies by Intent

```go
type LibrarianQueryCache struct {
    // Layer 1: Intent + Subject based (high hit rate for repeated concepts)
    locateCache  map[string]*LocateCacheEntry   // entity → location + file_hash
    patternCache map[string]*PatternCacheEntry  // concept → synthesis + sources
    explainCache map[string]*ExplainCacheEntry  // subject → explanation + sources

    // Layer 2: Semantic fallback (lower threshold than Archivalist)
    semanticCache *SemanticCache  // embedding similarity at 0.80, not 0.95

    // File change tracking for invalidation
    fileHashes map[string]string  // path → content hash

    mu sync.RWMutex
}

// Cache entry for LOCATE intent ("where is X")
type LocateCacheEntry struct {
    Entity       string              `json:"entity"`        // normalized entity name
    Locations    []FileLocation      `json:"locations"`     // where it was found
    FileHashes   map[string]string   `json:"file_hashes"`   // path → hash at cache time
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}

// Cache entry for PATTERN intent ("what is our X strategy")
type PatternCacheEntry struct {
    Concept      string              `json:"concept"`       // normalized concept
    Synthesis    string              `json:"synthesis"`     // synthesized answer
    SourceFiles  []string            `json:"source_files"`  // files used to generate
    TTL          time.Duration       `json:"ttl"`           // 60 min default
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}

// Cache entry for EXPLAIN intent ("how does X work")
type ExplainCacheEntry struct {
    Subject      string              `json:"subject"`       // what's being explained
    Explanation  string              `json:"explanation"`   // the explanation
    SourceFiles  []string            `json:"source_files"`  // files referenced
    FileHashes   map[string]string   `json:"file_hashes"`   // for invalidation
    TTL          time.Duration       `json:"ttl"`           // 30 min default
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}
```

#### Cache Invalidation by Intent

| Intent | Cache Key | Invalidation Trigger | TTL |
|--------|-----------|---------------------|-----|
| **LOCATE** | Entity name | Any file in result changes | Until file change |
| **PATTERN** | Concept | TTL + major structural change | 60 min |
| **EXPLAIN** | Subject | Referenced files change + TTL | 30 min |
| **GENERAL** | Embedding (0.80) | TTL only | 15 min |

```go
func (lc *LibrarianQueryCache) IsStale(entry any, intent QueryIntent) bool {
    switch intent {
    case IntentLocate:
        e := entry.(*LocateCacheEntry)
        // Check if any referenced file has changed
        for path, cachedHash := range e.FileHashes {
            if currentHash := lc.fileHashes[path]; currentHash != cachedHash {
                return true  // File changed, invalidate
            }
        }
        return false  // No TTL for location queries

    case IntentPattern:
        e := entry.(*PatternCacheEntry)
        // TTL-based + check for major structural changes
        if time.Since(e.CreatedAt) > e.TTL {
            return true
        }
        // Check if any source directory has new files
        return lc.hasStructuralChanges(e.SourceFiles)

    case IntentExplain:
        e := entry.(*ExplainCacheEntry)
        // Hybrid: file change OR TTL
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
        // General: TTL only
        return time.Since(entry.(*GeneralCacheEntry).CreatedAt) > 15*time.Minute
    }
}
```

#### File Change Detection & Cache Invalidation

The Librarian uses fsnotify to watch for file changes and invalidate cache entries in real-time.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         FILE CHANGE DETECTION PIPELINE                               │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐   │
│  │   fsnotify   │────▶│   Debouncer  │────▶│ Hash Computer│────▶│ Invalidator  │   │
│  │   Watcher    │     │  (100ms)     │     │              │     │              │   │
│  └──────────────┘     └──────────────┘     └──────────────┘     └──────────────┘   │
│         │                    │                    │                    │            │
│         ▼                    ▼                    ▼                    ▼            │
│  Watch all files      Batch rapid         Compute xxHash64      Invalidate:        │
│  in repo (recursive)  changes together    (fast, 1GB/s)         - LOCATE entries   │
│                                                                 - EXPLAIN entries  │
│                                                                 - Update fileHashes│
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

##### File Watcher Integration (fsnotify)

```go
type FileWatcher struct {
    watcher     *fsnotify.Watcher
    cache       *LibrarianQueryCache
    index       *CodebaseIndex
    debouncer   *Debouncer
    hashCache   map[string]string  // path → current hash

    // Configuration
    ignorePaths []string  // .git, node_modules, vendor, etc.

    mu          sync.RWMutex
    ctx         context.Context
    cancel      context.CancelFunc
}

func NewFileWatcher(cache *LibrarianQueryCache, index *CodebaseIndex) *FileWatcher {
    watcher, _ := fsnotify.NewWatcher()
    ctx, cancel := context.WithCancel(context.Background())

    fw := &FileWatcher{
        watcher:   watcher,
        cache:     cache,
        index:     index,
        debouncer: NewDebouncer(100 * time.Millisecond),
        hashCache: make(map[string]string),
        ignorePaths: []string{
            ".git", "node_modules", "vendor", "__pycache__",
            ".idea", ".vscode", "dist", "build", ".cache",
        },
        ctx:    ctx,
        cancel: cancel,
    }

    go fw.run()
    return fw
}

func (fw *FileWatcher) run() {
    for {
        select {
        case event, ok := <-fw.watcher.Events:
            if !ok {
                return
            }
            fw.handleEvent(event)

        case err, ok := <-fw.watcher.Errors:
            if !ok {
                return
            }
            log.Printf("file watcher error: %v", err)

        case <-fw.ctx.Done():
            return
        }
    }
}

func (fw *FileWatcher) handleEvent(event fsnotify.Event) {
    // Ignore non-relevant events
    if fw.shouldIgnore(event.Name) {
        return
    }

    // Debounce rapid changes (e.g., editor save creates multiple events)
    fw.debouncer.Debounce(event.Name, func() {
        fw.processFileChange(event)
    })
}
```

##### Debouncing Rapid File Changes

Editors often trigger multiple events for a single save (write, chmod, rename). Debouncing batches these together.

```go
type Debouncer struct {
    delay   time.Duration
    timers  map[string]*time.Timer
    mu      sync.Mutex
}

func NewDebouncer(delay time.Duration) *Debouncer {
    return &Debouncer{
        delay:  delay,
        timers: make(map[string]*time.Timer),
    }
}

func (d *Debouncer) Debounce(key string, fn func()) {
    d.mu.Lock()
    defer d.mu.Unlock()

    // Cancel existing timer for this key
    if timer, ok := d.timers[key]; ok {
        timer.Stop()
    }

    // Set new timer
    d.timers[key] = time.AfterFunc(d.delay, func() {
        d.mu.Lock()
        delete(d.timers, key)
        d.mu.Unlock()
        fn()
    })
}
```

##### Hash Computation (xxHash64)

Using xxHash64 for fast file hashing (~1GB/s, much faster than SHA256).

```go
import "github.com/cespare/xxhash/v2"

func (fw *FileWatcher) computeHash(path string) (string, error) {
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

func (fw *FileWatcher) processFileChange(event fsnotify.Event) {
    path := event.Name

    fw.mu.Lock()
    defer fw.mu.Unlock()

    var newHash string
    var err error

    switch {
    case event.Op&fsnotify.Remove == fsnotify.Remove:
        // File deleted
        newHash = ""
        delete(fw.hashCache, path)

    case event.Op&fsnotify.Write == fsnotify.Write,
         event.Op&fsnotify.Create == fsnotify.Create:
        // File created or modified
        newHash, err = fw.computeHash(path)
        if err != nil {
            return
        }

        // Check if hash actually changed (content change, not just touch)
        if oldHash, ok := fw.hashCache[path]; ok && oldHash == newHash {
            return  // No actual content change
        }
        fw.hashCache[path] = newHash
    }

    // Notify cache of file change
    fw.cache.OnFileChanged(path, newHash)

    // Also update the index
    fw.index.OnFileChanged(path, event.Op)
}
```

##### Cache Invalidation on File Change

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

    // Check PATTERN entries for structural changes (new/deleted files)
    dir := filepath.Dir(path)
    for concept, entry := range lc.patternCache {
        for _, sourceFile := range entry.SourceFiles {
            if filepath.Dir(sourceFile) == dir {
                // File added/removed in a directory we synthesized from
                delete(lc.patternCache, concept)
                lc.stats.PatternInvalidations++
                break
            }
        }
    }
}
```

##### Index Synchronization

The cache invalidation coordinates with the Librarian's code index:

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

##### Batch Operations for Large Changes

For operations like `git checkout` that change many files at once:

```go
func (fw *FileWatcher) OnBatchChange(paths []string) {
    fw.mu.Lock()
    defer fw.mu.Unlock()

    // Pause normal watching during batch
    fw.debouncer.Pause()
    defer fw.debouncer.Resume()

    // Collect all changed hashes
    changedPaths := make(map[string]string)
    for _, path := range paths {
        if hash, err := fw.computeHash(path); err == nil {
            if oldHash := fw.hashCache[path]; oldHash != hash {
                changedPaths[path] = hash
                fw.hashCache[path] = hash
            }
        }
    }

    // Batch invalidate
    fw.cache.OnBatchFileChanged(changedPaths)
    fw.index.OnBatchFileChanged(changedPaths)
}

func (lc *LibrarianQueryCache) OnBatchFileChanged(changes map[string]string) {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    // Update all hashes
    for path, hash := range changes {
        lc.fileHashes[path] = hash
    }

    // For large batches, it's more efficient to clear caches entirely
    if len(changes) > 100 {
        lc.locateCache = make(map[string]*LocateCacheEntry)
        lc.explainCache = make(map[string]*ExplainCacheEntry)
        lc.patternCache = make(map[string]*PatternCacheEntry)
        return
    }

    // For smaller batches, selectively invalidate
    for path := range changes {
        lc.invalidateByPath(path)
    }
}
```

#### Query Handling with Pre-Classified Intent

The Guide classifies intent during routing (no additional cost to Librarian):

```go
// Message from Guide includes intent classification
type LibrarianRequest struct {
    Query       string      `json:"query"`
    SessionID   string      `json:"session_id"`

    // Pre-classified by Guide (no additional LLM cost)
    Intent      QueryIntent `json:"intent"`      // LOCATE, PATTERN, EXPLAIN, GENERAL
    Subject     string      `json:"subject"`     // extracted entity/concept
    Confidence  float64     `json:"confidence"`  // Guide's classification confidence
}

func (l *Librarian) HandleQuery(req *LibrarianRequest) (*Response, error) {
    // Use Guide's pre-classification if confident
    if req.Confidence >= 0.8 {
        switch req.Intent {
        case IntentLocate:
            if cached, ok := l.cache.GetLocate(req.Subject); ok {
                return cached.ToResponse(), nil
            }
        case IntentPattern:
            if cached, ok := l.cache.GetPattern(req.Subject); ok {
                return cached.ToResponse(), nil
            }
        case IntentExplain:
            if cached, ok := l.cache.GetExplain(req.Subject); ok {
                return cached.ToResponse(), nil
            }
        }
    }

    // Cache miss or low confidence - do full synthesis
    response, sources := l.synthesize(req.Query)

    // Cache the result using the classified intent
    l.cache.Store(req.Intent, req.Subject, response, sources)

    return response, nil
}
```

#### Expected Cache Performance

| Query Type | Without Cache | With Intent Cache | Improvement |
|------------|---------------|-------------------|-------------|
| "where is auth.go" (repeated) | 3,000 tokens | 0 tokens | 100% |
| "where is authentication" (variation) | 3,000 tokens | 0 tokens | 100% |
| "what is our caching strategy" | 5,000 tokens | 0 tokens | 100% |
| "how do we handle caching" (variation) | 5,000 tokens | 0 tokens | 100% |
| "explain the auth flow" | 4,000 tokens | 0 tokens | 100% |

**Overall expected hit rate**: 70-85% for typical usage patterns

**Token savings per session**: 60-75% reduction

---

### Guide Memory Management

The Guide tracks routing decisions, matches, and request patterns. This information helps optimize future routing.

**Thresholds**: 50%, 75%, 90% (checkpoint) | 95% (compact)

**Checkpoint Summary**:

```go
type GuideSummary struct {
    Timestamp          time.Time                `json:"timestamp"`
    SessionID          string                   `json:"session_id"`
    ContextUsage       float64                  `json:"context_usage"`
    CheckpointIndex    int                      `json:"checkpoint_index"`

    // Routing knowledge
    KnownRoutings      map[string]string        `json:"known_routings"`      // pattern → agent
    FrequentMatches    []RoutingMatch           `json:"frequent_matches"`    // common routes
    FailedRoutings     []FailedRouting          `json:"failed_routings"`     // routes that didn't work
    AgentCapabilities  map[string][]string      `json:"agent_capabilities"`  // agent → capabilities observed
    RequestPatterns    []RequestPattern         `json:"request_patterns"`    // common request types
    SessionRoutingStats RoutingStats            `json:"session_routing_stats"`
}

type RoutingMatch struct {
    Pattern     string `json:"pattern"`
    Agent       string `json:"agent"`
    Confidence  float64 `json:"confidence"`
    UsageCount  int    `json:"usage_count"`
}
```

**Archivalist Category**: `guide_checkpoint`

---

### Archivalist Query Caching (Intent-Aware)

The Archivalist handles historical queries - past decisions, agent activity, workflow outcomes. Users phrase these queries in many ways that miss the standard 0.95 embedding similarity threshold.

#### Why Intent-Aware Caching for Archivalist

**The Problem**: Same intent, different phrasing:

| Query A | Query B | Similarity | Result at 0.95 |
|---------|---------|------------|----------------|
| "What did we do before for auth" | "Past solutions for authentication" | ~0.82 | MISS |
| "What files changed" | "Recent modifications by engineer" | ~0.80 | MISS |
| "Did the tests pass" | "What was the test outcome" | ~0.78 | MISS |
| "Have we seen this error before" | "Similar past failures" | ~0.75 | MISS |

**Key Difference from Librarian**: Historical data is **immutable**. Once stored, it doesn't change. Invalidation is simpler - mostly TTL-based or "new data added", not file-change-based.

#### Archivalist Query Intents

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         ARCHIVALIST QUERY INTENT TYPES                               │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  HISTORICAL - Past solutions, decisions, approaches                                 │
│  ├── "What did we do before for X"                                                 │
│  ├── "Past solutions for X"                                                        │
│  ├── "How did we handle X previously"                                              │
│  └── Cache: topic-based, TTL 30-60 min (history doesn't change)                    │
│                                                                                     │
│  ACTIVITY - Agent activity, file changes, task results                             │
│  ├── "What files did the engineer change"                                          │
│  ├── "What happened in the last task"                                              │
│  ├── "Show me recent modifications"                                                │
│  └── Cache: session-scoped, short TTL 5 min (new activity happens frequently)      │
│                                                                                     │
│  OUTCOME - Results, status, completion                                             │
│  ├── "Did the tests pass"                                                          │
│  ├── "What issues did the inspector find"                                          │
│  ├── "What was the result of the workflow"                                         │
│  └── Cache: task/workflow ID, invalidate when result updated                       │
│                                                                                     │
│  SIMILAR - Pattern matching, similarity search                                     │
│  ├── "Have we seen this error before"                                              │
│  ├── "Find similar past decisions"                                                 │
│  ├── "What worked for problems like this"                                          │
│  └── Cache: embedding-based with 0.80 threshold, TTL 30 min                        │
│                                                                                     │
│  RESUME - Session state, continuation                                              │
│  ├── "Where did we leave off"                                                      │
│  ├── "Resume context"                                                              │
│  ├── "What's the current status"                                                   │
│  └── Cache: session ID, very short TTL 1-2 min (state changes constantly)          │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Intent-Specific Cache Structures

```go
type ArchivalistQueryCache struct {
    // Intent-based caches
    historicalCache map[string]*HistoricalCacheEntry  // topic → past solutions
    activityCache   map[string]*ActivityCacheEntry    // session+type → activity
    outcomeCache    map[string]*OutcomeCacheEntry     // task/workflow ID → result
    similarCache    *SimilarityCache                  // embedding-based, 0.80 threshold
    resumeCache     map[string]*ResumeCacheEntry      // session ID → state

    // Existing query cache (fallback)
    legacyCache     *QueryCache  // Original 0.95 threshold cache

    mu sync.RWMutex
}

type HistoricalCacheEntry struct {
    Topic        string        `json:"topic"`         // "authentication", "caching", etc.
    Solutions    []Solution    `json:"solutions"`     // Past approaches found
    SessionIDs   []string      `json:"session_ids"`   // Sessions these came from
    TTL          time.Duration `json:"ttl"`           // 30-60 min
    CreatedAt    time.Time     `json:"created_at"`
    HitCount     int64         `json:"hit_count"`
}

type ActivityCacheEntry struct {
    SessionID    string        `json:"session_id"`
    ActivityType string        `json:"activity_type"` // "file_changes", "task_results", etc.
    Activities   []Activity    `json:"activities"`
    TTL          time.Duration `json:"ttl"`           // 5 min (short, new activity frequent)
    CreatedAt    time.Time     `json:"created_at"`
    LastActivity time.Time     `json:"last_activity"` // Invalidate if new activity after this
}

type OutcomeCacheEntry struct {
    WorkflowID   string        `json:"workflow_id"`
    TaskID       string        `json:"task_id,omitempty"`
    Outcome      *Outcome      `json:"outcome"`
    IsComplete   bool          `json:"is_complete"`   // If false, short TTL
    TTL          time.Duration `json:"ttl"`
    CreatedAt    time.Time     `json:"created_at"`
}

type ResumeCacheEntry struct {
    SessionID    string        `json:"session_id"`
    State        *SessionState `json:"state"`
    TTL          time.Duration `json:"ttl"`           // 1-2 min (state changes constantly)
    CreatedAt    time.Time     `json:"created_at"`
    StateVersion int64         `json:"state_version"` // Invalidate if version changed
}
```

#### Cache Invalidation by Intent

| Intent | Cache Key | Invalidation Trigger | TTL |
|--------|-----------|---------------------|-----|
| **HISTORICAL** | Topic (normalized) | TTL only (history is immutable) | 30-60 min |
| **ACTIVITY** | Session + activity type | New activity recorded OR TTL | 5 min |
| **OUTCOME** | Workflow/Task ID | Result updated OR TTL | Until complete, then 30 min |
| **SIMILAR** | Embedding (0.80 threshold) | TTL only | 30 min |
| **RESUME** | Session ID | Any session activity OR TTL | 1-2 min |

```go
func (ac *ArchivalistQueryCache) IsStale(entry any, intent ArchivalistIntent) bool {
    switch intent {
    case IntentHistorical:
        e := entry.(*HistoricalCacheEntry)
        // History doesn't change - TTL only
        return time.Since(e.CreatedAt) > e.TTL

    case IntentActivity:
        e := entry.(*ActivityCacheEntry)
        // Check if new activity since cache
        if ac.hasNewActivity(e.SessionID, e.LastActivity) {
            return true
        }
        return time.Since(e.CreatedAt) > e.TTL

    case IntentOutcome:
        e := entry.(*OutcomeCacheEntry)
        // If workflow not complete, check if result updated
        if !e.IsComplete {
            if ac.outcomeUpdated(e.WorkflowID, e.TaskID) {
                return true
            }
        }
        return time.Since(e.CreatedAt) > e.TTL

    case IntentSimilar:
        // Similarity cache uses TTL only
        e := entry.(*SimilarCacheEntry)
        return time.Since(e.CreatedAt) > e.TTL

    case IntentResume:
        e := entry.(*ResumeCacheEntry)
        // Check if session state version changed
        if ac.sessionStateVersion(e.SessionID) != e.StateVersion {
            return true
        }
        return time.Since(e.CreatedAt) > e.TTL

    default:
        return true
    }
}
```

#### Guide Intent Classification for Archivalist

The Guide classifies Archivalist query intent during routing (same LLM call, no extra cost):

```go
type ArchivalistIntent int

const (
    ArchivalistIntentHistorical ArchivalistIntent = iota  // Past solutions
    ArchivalistIntentActivity                              // Agent activity
    ArchivalistIntentOutcome                               // Results/status
    ArchivalistIntentSimilar                               // Similarity search
    ArchivalistIntentResume                                // Session state
    ArchivalistIntentGeneral                               // Fallback
)

// Intent classification patterns
var archivalistIntentPatterns = map[ArchivalistIntent][]string{
    ArchivalistIntentHistorical: {
        "what did we do", "before", "previously", "past", "last time",
        "how did we handle", "prior", "earlier", "history",
    },
    ArchivalistIntentActivity: {
        "what changed", "what did .* do", "modifications", "recent",
        "files changed", "activity", "what happened",
    },
    ArchivalistIntentOutcome: {
        "did .* pass", "result", "outcome", "issues found",
        "status of", "complete", "succeed", "fail",
    },
    ArchivalistIntentSimilar: {
        "similar", "like this", "seen before", "pattern",
        "resembles", "related", "comparable",
    },
    ArchivalistIntentResume: {
        "where did we", "resume", "continue", "left off",
        "current state", "pick up",
    },
}

// Message to Archivalist includes intent
type ArchivalistRequest struct {
    Query       string             `json:"query"`
    SessionID   string             `json:"session_id"`
    Intent      ArchivalistIntent  `json:"intent"`
    Subject     string             `json:"subject"`     // Topic/entity being queried
    Confidence  float64            `json:"confidence"`
}
```

#### Expected Performance

| Intent | Typical Query Cost | With Cache Hit | Savings |
|--------|-------------------|----------------|---------|
| HISTORICAL | ~4,000 tokens | 0 tokens | 100% |
| ACTIVITY | ~2,000 tokens | 0 tokens | 100% |
| OUTCOME | ~1,500 tokens | 0 tokens | 100% |
| SIMILAR | ~5,000 tokens | 0 tokens | 100% |
| RESUME | ~1,000 tokens | 0 tokens | 100% |

**Expected hit rates by intent**:
- HISTORICAL: 80-90% (same topics asked repeatedly)
- ACTIVITY: 60-70% (frequent but session-specific)
- OUTCOME: 70-80% (asked multiple times during workflow)
- SIMILAR: 50-60% (varied queries, but patterns repeat)
- RESUME: 40-50% (short TTL, but frequently asked)

**Overall expected hit rate**: 60-75%
**Token savings per session**: 50-65% reduction

---

### Academic Memory Management

The Academic researches topics and produces findings. At checkpoint, it produces a "research paper" summarizing its findings.

**Model**: Opus 4.5

**Thresholds**: 85% (checkpoint) | 95% (compact)

**Checkpoint Summary (Research Paper Format)**:

```go
type AcademicResearchPaper struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`

    // Research paper structure
    Title              string            `json:"title"`
    Abstract           string            `json:"abstract"`
    TopicsResearched   []string          `json:"topics_researched"`
    KeyFindings        []Finding         `json:"key_findings"`
    SourcesCited       []Source          `json:"sources_cited"`
    Recommendations    []string          `json:"recommendations"`
    OpenQuestions      []string          `json:"open_questions"`
    RelatedTopics      []string          `json:"related_topics"`
}

type Finding struct {
    Topic       string   `json:"topic"`
    Summary     string   `json:"summary"`
    Confidence  string   `json:"confidence"` // high, medium, low
    Sources     []string `json:"sources"`
}
```

**Archivalist Category**: `academic_research_paper`

---

### Architect Memory Management

The Architect creates implementation plans and DAGs. At checkpoint, it produces a retrievable workflow/plan document.

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact)

**Checkpoint Summary (Retrievable Workflow Format)**:

```go
type ArchitectWorkflowSummary struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`

    // Workflow state (parseable by other Architects)
    OriginalRequest    string            `json:"original_request"`
    ImplementationPlan string            `json:"implementation_plan"`
    CurrentDAG         *DAGSummary       `json:"current_dag"`
    CompletedTasks     []TaskSummary     `json:"completed_tasks"`
    PendingTasks       []TaskSummary     `json:"pending_tasks"`
    BlockedTasks       []TaskSummary     `json:"blocked_tasks"`
    ArchitecturalDecisions []Decision   `json:"architectural_decisions"`
    Risks              []Risk            `json:"risks"`
    Assumptions        []string          `json:"assumptions"`
}

type DAGSummary struct {
    ID             string            `json:"id"`
    TotalNodes     int               `json:"total_nodes"`
    CompletedNodes int               `json:"completed_nodes"`
    CurrentLayer   int               `json:"current_layer"`
    ExecutionOrder [][]string        `json:"execution_order"` // layer → node IDs
}

type TaskSummary struct {
    ID          string `json:"id"`
    Description string `json:"description"`
    Status      string `json:"status"`
    AssignedTo  string `json:"assigned_to,omitempty"`
    Result      string `json:"result,omitempty"`
}
```

**Archivalist Category**: `architect_workflow`

---

### Inspector Memory Management

The Inspector validates code and tracks issues. At checkpoint, it summarizes findings, fixes, and priorities.

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact locally)

**Note**: Inspector compacts locally at 95%. Does NOT trigger pipeline handoff.

**Checkpoint Summary**:

```go
type InspectorFindingsSummary struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    PipelineID         string            `json:"pipeline_id,omitempty"`
    ContextUsage       float64           `json:"context_usage"`

    // Findings state
    ChecksPerformed    []string          `json:"checks_performed"`
    IssuesFound        int               `json:"issues_found"`
    IssuesResolved     int               `json:"issues_resolved"`
    IssuesRemaining    []InspectorIssue  `json:"issues_remaining"`
    FixesCompleted     []CompletedFix    `json:"fixes_completed"`
    FixesPending       []PendingFix      `json:"fixes_pending"`
    ValidationState    map[string]bool   `json:"validation_state"` // category → pass
}

type CompletedFix struct {
    IssueID     string `json:"issue_id"`
    File        string `json:"file"`
    Line        int    `json:"line"`
    Description string `json:"description"`
    FixApplied  string `json:"fix_applied"`
}

type PendingFix struct {
    IssueID     string `json:"issue_id"`
    File        string `json:"file"`
    Line        int    `json:"line"`
    Category    string `json:"category"`
    Severity    string `json:"severity"` // critical, high, medium, low
    Priority    int    `json:"priority"` // 1 = highest
    Description string `json:"description"`
    SuggestedFix string `json:"suggested_fix"`
}
```

**Archivalist Category**: `inspector_findings`

---

### Tester Memory Management

The Tester creates and runs tests. At checkpoint, it summarizes test state and results.

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact locally)

**Note**: Tester compacts locally at 95%. Does NOT trigger pipeline handoff.

**Checkpoint Summary**:

```go
type TesterSummary struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    PipelineID         string            `json:"pipeline_id,omitempty"`
    ContextUsage       float64           `json:"context_usage"`

    // Test state
    TestsCreated       []TestInfo        `json:"tests_created"`
    TestsRun           []TestResult      `json:"tests_run"`
    PassCount          int               `json:"pass_count"`
    FailCount          int               `json:"fail_count"`
    SkipCount          int               `json:"skip_count"`
    CoverageNeeded     []string          `json:"coverage_needed"`
    FailureDescriptions []FailureDesc    `json:"failure_descriptions"`
}

type TestInfo struct {
    File     string `json:"file"`
    TestName string `json:"test_name"`
    Type     string `json:"type"` // unit, integration, e2e
    ForTask  string `json:"for_task,omitempty"`
}

type TestResult struct {
    TestName string `json:"test_name"`
    Status   string `json:"status"` // pass, fail, skip
    Duration string `json:"duration,omitempty"`
}

type FailureDesc struct {
    TestName    string `json:"test_name"`
    Error       string `json:"error"`
    Expected    string `json:"expected,omitempty"`
    Actual      string `json:"actual,omitempty"`
    Suggestion  string `json:"suggestion,omitempty"`
}
```

**Archivalist Category**: `tester_summary`

---

### Engineer Memory Management: Pipeline Handoff

**CRITICAL**: The Engineer does NOT compact locally. At 95% context, it triggers a **PIPELINE HANDOFF**.

**Model**: Opus 4.5

**Threshold**: 95% → **PIPELINE HANDOFF**

This is a special mechanism where the entire pipeline (Engineer + Inspector + Tester) transfers state to a new pipeline, minimizing re-learning.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    PIPELINE HANDOFF (ENGINEER AT 95%)                                │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  OLD PIPELINE                                                         NEW PIPELINE  │
│  (Eng+Insp+Test)       GUIDE          ARCHITECT       ORCHESTRATOR   (Eng+Insp+Test)│
│       │                  │                │                │               │        │
│  ┌────┴────┐             │                │                │               │        │
│  │Engineer │             │                │                │               │        │
│  │ at 95%  │             │                │                │               │        │
│  └────┬────┘             │                │                │               │        │
│       │                  │                │                │               │        │
│       │ Prepare state    │                │                │               │        │
│       │ (E + I + T)      │                │                │               │        │
│       │                  │                │                │               │        │
│       │══"HANDOFF_REQ"══▶│                │                │               │        │
│       │  + full state    │══════════════▶│                │               │        │
│       │  (E+I+T summary) │                │                │               │        │
│       │                  │                │                │               │        │
│       │                  │          ┌─────┴─────┐          │               │        │
│       │                  │          │ Examine   │          │               │        │
│       │                  │          │ state,    │          │               │        │
│       │                  │          │ adjust    │          │               │        │
│       │                  │          │ workflow  │          │               │        │
│       │                  │          │ if needed │          │               │        │
│       │                  │          └─────┬─────┘          │               │        │
│       │                  │                │                │               │        │
│       │                  │                │══"CREATE_NEW"═▶│               │        │
│       │                  │                │  + state       │               │        │
│       │                  │                │  + "close old" │               │        │
│       │                  │                │                │               │        │
│       │                  │                │                │──create──────▶│        │
│       │                  │                │                │  (inject      │        │
│       │                  │                │                │   state)      │        │
│       │                  │                │                │               │        │
│       │                  │                │                │◀──"STARTED"───│        │
│       │                  │                │                │               │        │
│       │◀══"CLOSE_NOW"════│◀═══════════════│◀═══════════════│               │        │
│       │                  │                │                │               │        │
│       X                  │                │                │         ┌─────┴─────┐  │
│  (old closes)            │                │                │         │ EXECUTING │  │
│                          │                │                │         │ (resumed) │  │
│                          │                │                │         └───────────┘  │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Handoff State Structure

```go
type PipelineHandoff struct {
    // Metadata
    OldPipelineID   PipelineID    `json:"old_pipeline_id"`
    SessionID       string        `json:"session_id"`
    DAGID           string        `json:"dag_id"`
    TaskID          string        `json:"task_id"`
    HandoffReason   string        `json:"handoff_reason"` // "engineer_context_95%"
    Timestamp       time.Time     `json:"timestamp"`
    HandoffIndex    int           `json:"handoff_index"`  // For chaining (1, 2, 3...)

    // Bundled state from all three agents
    EngineerState   *EngineerHandoffState   `json:"engineer_state"`
    InspectorState  *InspectorHandoffState  `json:"inspector_state"`
    TesterState     *TesterHandoffState     `json:"tester_state"`
}

type EngineerHandoffState struct {
    OriginalPrompt    string            `json:"original_prompt"`    // Verbatim
    Accomplished      []string          `json:"accomplished"`       // What was done
    FilesChanged      []FileChange      `json:"files_changed"`      // Specific changes
    Remaining         []string          `json:"remaining"`          // TODOs to complete
    ContextNotes      string            `json:"context_notes"`      // Critical context
}

type InspectorHandoffState struct {
    ChecksPerformed   []string          `json:"checks_performed"`
    FixesCompleted    []CompletedFix    `json:"fixes_completed"`    // With file:line refs
    FixesRemaining    []PendingFix      `json:"fixes_remaining"`    // With priority
    ValidationState   map[string]bool   `json:"validation_state"`   // category → pass/fail
}

type TesterHandoffState struct {
    TestsCreated      []TestInfo        `json:"tests_created"`      // File, name
    TestResults       []TestResult      `json:"test_results"`       // Pass/fail
    FailureDescs      []FailureDesc     `json:"failure_descs"`      // Brief errors
    CoverageNeeded    []string          `json:"coverage_needed"`    // What still needs tests
}
```

#### Handoff Message Flow

```
1. Engineer (old) prepares bundled state (Engineer + Inspector + Tester)

2. Engineer (old) ──GUIDE──▶ Architect    : "HANDOFF_REQUEST" + full state

3. Architect examines state, adjusts workflow if necessary

4. Architect ──GUIDE──▶ Orchestrator      : "CREATE_PIPELINE_WITH_STATE" {
                                              state: <bundled state>,
                                              close_pipeline: <old pipeline ID>
                                            }

5. Orchestrator:
   - Creates new pipeline
   - Injects state into new pipeline (all three agents start with context)
   - New pipeline starts executing
   - Closes old pipeline

6. Orchestrator ──GUIDE──▶ Architect      : "HANDOFF_COMPLETE"
```

#### Handoff Rules

| Rule | Description |
|------|-------------|
| **Trigger** | Engineer at 95% OR user request |
| **Who triggers** | Only Engineer triggers handoff (Inspector/Tester compact locally) |
| **State bundling** | Engineer collects state from Inspector + Tester |
| **Architect review** | Architect examines and may adjust workflow |
| **Archivalist** | Handoff state also stored for audit/recovery |
| **Retry** | If handoff fails, retry. If retries fail, fallback to summarize → Archivalist → compact |
| **Chaining** | Handoffs can chain infinitely (tracked by HandoffIndex) |
| **User control** | User can stop a handoff chain if desired |

#### New Pipeline Receives

The new pipeline's agents start with full context:

- **New Engineer**: Knows original prompt, what was done, what remains
- **New Inspector**: Knows what passed, what failed, pending fixes with priorities
- **New Tester**: Knows existing tests, results, what coverage is still needed

This minimizes re-learning and re-discovery.

**Archivalist Category**: `pipeline_handoff`

---

## Multi-Session Architecture

### Session Coordination

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                     MULTI-SESSION ARCHITECTURE                                      │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Session A ──┐                                                                      │
│  Session B ──┼──→ Session Manager ──→ Guide (shared) ──→ Orchestrator (shared)      │
│  Session C ──┘           │                    │                    │                │
│                          │                    │                    │                │
│                          ▼                    ▼                    ▼                │
│                    ┌─────────────────────────────────────┐                          │
│                    │      ARCHIVALIST (shared)           │                          │
│                    │                                     │                          │
│                    │  Session-scoped writes:             │                          │
│                    │  ├── Entry.SessionID = source       │                          │
│                    │  ├── Isolated active state          │                          │
│                    │  └── Per-session file tracking      │                          │
│                    │                                     │                          │
│                    │  Cross-session reads:               │                          │
│                    │  ├── Promoted patterns (any)        │                          │
│                    │  ├── Promoted decisions (any)       │                          │
│                    │  ├── Historical failures (any)      │                          │
│                    │  └── Query with session_ids filter  │                          │
│                    │                                     │                          │
│                    └─────────────────────────────────────┘                          │
│                                     │                                               │
│                                     ▼                                               │
│                    ┌─────────────────────────────────────┐                          │
│                    │      RESOURCE MANAGER (shared)      │                          │
│                    │                                     │                          │
│                    │  ├── File-level read/write locks    │                          │
│                    │  ├── Branch management per session  │                          │
│                    │  └── Merge coordination             │                          │
│                    └─────────────────────────────────────┘                          │
│                                     │                                               │
│                                     ▼                                               │
│                    ┌─────────────────────────────────────┐                          │
│                    │      WORKER POOL (shared)           │                          │
│                    │                                     │                          │
│                    │  ├── Bounded total concurrency      │                          │
│                    │  ├── Fair scheduling across sessions│                          │
│                    │  ├── Per-session task limits        │                          │
│                    │  └── Priority lanes                 │                          │
│                    └─────────────────────────────────────┘                          │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### File Conflict Resolution

For multiple sessions operating on the same codebase:

1. **Branch-isolated workflows**: Each session operates on its own git branch (if enabled)
2. **Shared read cache**: All sessions read from same Librarian cache
3. **Deferred conflicts**: Merge conflicts handled at session completion
4. **Bounded resources**: Single worker pool prevents resource exhaustion

---

## VectorGraphDB: Unified Knowledge Graph

### Overview

The VectorGraphDB is a unified, embedded knowledge graph that combines **vector similarity search** with **graph-based structural queries** across three domains: Code (Librarian), History (Archivalist), and Academic (external knowledge). It uses SQLite as the storage backend with in-memory HNSW indexes for fast vector search - no external dependencies required.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    VECTORGRAPHDB: THREE-DOMAIN ARCHITECTURE                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐             │
│  │   LIBRARIAN     │  │   ARCHIVALIST   │  │    ACADEMIC     │             │
│  │   (Code)        │  │   (History)     │  │   (Knowledge)   │             │
│  │                 │  │                 │  │                 │             │
│  │  Domain: 0      │  │  Domain: 1      │  │  Domain: 2      │             │
│  │                 │  │                 │  │                 │             │
│  │  Nodes:         │  │  Nodes:         │  │  Nodes:         │             │
│  │  • File         │  │  • Entry        │  │  • Paper        │             │
│  │  • Function     │  │  • Session      │  │  • Docs         │             │
│  │  • Struct       │  │  • Workflow     │  │  • BestPractice │             │
│  │  • Interface    │  │  • Outcome      │  │  • RFC          │             │
│  │  • Package      │  │  • Decision     │  │  • Tutorial     │             │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘             │
│           │                    │                    │                       │
│           └────────────────────┼────────────────────┘                       │
│                                │                                            │
│                    CROSS-DOMAIN EDGES                                       │
│           ┌────────────────────┼────────────────────┐                       │
│           │                    │                    │                       │
│           ▼                    ▼                    ▼                       │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                      VECTORGRAPHDB                                   │   │
│  │                    (Single SQLite File)                              │   │
│  │                                                                     │   │
│  │  ┌─────────────────────────────────────────────────────────────┐   │   │
│  │  │  NODES TABLE                                                 │   │   │
│  │  │  • Unified schema for all domains                           │   │   │
│  │  │  • Domain field partitions data                             │   │   │
│  │  │  • Type field identifies node kind                          │   │   │
│  │  └─────────────────────────────────────────────────────────────┘   │   │
│  │                                                                     │   │
│  │  ┌─────────────────────────────────────────────────────────────┐   │   │
│  │  │  EDGES TABLE                                                 │   │   │
│  │  │  • Structural: calls, imports, implements                   │   │   │
│  │  │  • Temporal: produced_by, resulted_in                       │   │   │
│  │  │  • Cross-domain: modified, based_on, documents              │   │   │
│  │  └─────────────────────────────────────────────────────────────┘   │   │
│  │                                                                     │   │
│  │  ┌─────────────────────────────────────────────────────────────┐   │   │
│  │  │  VECTORS TABLE (BLOBs)        HNSW INDEX (In-Memory)        │   │   │
│  │  │  • Embeddings as binary       • O(log n) search             │   │   │
│  │  │  • Pre-computed magnitudes    • Loaded on startup           │   │   │
│  │  │  • Persisted to disk          • ~50MB per 10K nodes         │   │   │
│  │  └─────────────────────────────────────────────────────────────┘   │   │
│  │                                                                     │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### SQLite Schema

```sql
-- =============================================================================
-- DOMAIN AND TYPE ENUMS (stored as integers)
-- =============================================================================
-- Domain: 0 = code, 1 = history, 2 = academic
-- NodeType: See node type constants below

-- =============================================================================
-- NODES TABLE (unified for all domains)
-- =============================================================================
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    domain INTEGER NOT NULL,
    node_type INTEGER NOT NULL,
    name TEXT NOT NULL,

    -- Code domain fields
    path TEXT,
    package TEXT,
    line_start INTEGER,
    line_end INTEGER,
    signature TEXT,

    -- History domain fields
    session_id TEXT,
    timestamp DATETIME,
    category TEXT,

    -- Academic domain fields
    url TEXT,
    source TEXT,
    authors JSON,
    published_at DATETIME,

    -- Common fields
    content TEXT,
    content_hash TEXT,
    metadata JSON,

    -- Verification and trust
    verified BOOLEAN DEFAULT FALSE,
    verification_type INTEGER,
    confidence REAL DEFAULT 1.0,
    trust_level INTEGER DEFAULT 50,

    -- Temporal tracking
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    superseded_by TEXT,

    CHECK (domain IN (0, 1, 2)),
    FOREIGN KEY (superseded_by) REFERENCES nodes(id) ON DELETE SET NULL
);

-- =============================================================================
-- EDGES TABLE
-- =============================================================================
CREATE TABLE edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    edge_type INTEGER NOT NULL,
    weight REAL DEFAULT 1.0,
    metadata JSON,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE (source_id, target_id, edge_type)
);

-- =============================================================================
-- VECTORS TABLE (embeddings as BLOBs)
-- =============================================================================
CREATE TABLE vectors (
    node_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    magnitude REAL NOT NULL,
    dimensions INTEGER NOT NULL DEFAULT 768,
    domain INTEGER NOT NULL,
    node_type INTEGER NOT NULL,

    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- =============================================================================
-- PROVENANCE TABLE (tracks source of information)
-- =============================================================================
CREATE TABLE provenance (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL,
    source_type INTEGER NOT NULL,
    source_node_id TEXT,
    source_path TEXT,
    source_url TEXT,
    confidence REAL NOT NULL,
    verified_at DATETIME,

    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (source_node_id) REFERENCES nodes(id) ON DELETE SET NULL
);

-- =============================================================================
-- CONFLICTS TABLE (detected contradictions)
-- =============================================================================
CREATE TABLE conflicts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conflict_type INTEGER NOT NULL,
    subject TEXT NOT NULL,
    node_id_a TEXT NOT NULL,
    node_id_b TEXT NOT NULL,
    description TEXT NOT NULL,
    resolution TEXT,
    resolved BOOLEAN DEFAULT FALSE,
    detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,

    FOREIGN KEY (node_id_a) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (node_id_b) REFERENCES nodes(id) ON DELETE CASCADE
);

-- =============================================================================
-- HNSW INDEX PERSISTENCE
-- =============================================================================
CREATE TABLE hnsw_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE hnsw_edges (
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    level INTEGER NOT NULL,
    PRIMARY KEY (source_id, target_id, level)
);

-- =============================================================================
-- ACADEMIC-SPECIFIC TABLES
-- =============================================================================
CREATE TABLE academic_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_endpoint TEXT,
    rate_limit_per_min INTEGER,
    requires_auth BOOLEAN DEFAULT FALSE,
    last_crawled_at DATETIME
);

CREATE TABLE academic_chunks (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE (node_id, chunk_index)
);

CREATE TABLE library_docs (
    library_path TEXT PRIMARY KEY,
    doc_node_id TEXT,
    FOREIGN KEY (doc_node_id) REFERENCES nodes(id) ON DELETE SET NULL
);

-- =============================================================================
-- INDEXES
-- =============================================================================
CREATE INDEX idx_nodes_domain_type ON nodes(domain, node_type);
CREATE INDEX idx_nodes_path ON nodes(path) WHERE path IS NOT NULL;
CREATE INDEX idx_nodes_name ON nodes(name);
CREATE INDEX idx_nodes_session ON nodes(session_id) WHERE session_id IS NOT NULL;
CREATE INDEX idx_nodes_hash ON nodes(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX idx_nodes_superseded ON nodes(superseded_by) WHERE superseded_by IS NOT NULL;

CREATE INDEX idx_edges_source ON edges(source_id, edge_type);
CREATE INDEX idx_edges_target ON edges(target_id, edge_type);
CREATE INDEX idx_edges_type ON edges(edge_type);

CREATE INDEX idx_vectors_domain ON vectors(domain);
CREATE INDEX idx_vectors_domain_type ON vectors(domain, node_type);

CREATE INDEX idx_provenance_node ON provenance(node_id);
CREATE INDEX idx_conflicts_unresolved ON conflicts(resolved) WHERE resolved = FALSE;

CREATE INDEX idx_hnsw_edges_level ON hnsw_edges(level, source_id);
CREATE INDEX idx_chunks_node ON academic_chunks(node_id);
```

### Node and Edge Types

```go
// =============================================================================
// DOMAIN CONSTANTS
// =============================================================================

type Domain int

const (
    DomainCode     Domain = 0  // Librarian - live codebase
    DomainHistory  Domain = 1  // Archivalist - historical data
    DomainAcademic Domain = 2  // Academic - external knowledge
)

// =============================================================================
// NODE TYPES BY DOMAIN
// =============================================================================

type NodeType int

// Code domain (0-99)
const (
    NodeTypeFile      NodeType = 0
    NodeTypePackage   NodeType = 1
    NodeTypeFunction  NodeType = 2
    NodeTypeMethod    NodeType = 3
    NodeTypeStruct    NodeType = 4
    NodeTypeInterface NodeType = 5
    NodeTypeVariable  NodeType = 6
    NodeTypeConstant  NodeType = 7
    NodeTypeImport    NodeType = 8
)

// History domain (100-199)
const (
    NodeTypeHistoryEntry NodeType = 100
    NodeTypeSession      NodeType = 101
    NodeTypeWorkflow     NodeType = 102
    NodeTypeOutcome      NodeType = 103
    NodeTypeDecision     NodeType = 104
)

// Academic domain (200-299)
const (
    NodeTypePaper         NodeType = 200
    NodeTypeDocumentation NodeType = 201
    NodeTypeBestPractice  NodeType = 202
    NodeTypeRFC           NodeType = 203
    NodeTypeStackOverflow NodeType = 204
    NodeTypeBlogPost      NodeType = 205
    NodeTypeTutorial      NodeType = 206
)

// =============================================================================
// EDGE TYPES
// =============================================================================

type EdgeType int

// Code structural edges (0-49)
const (
    EdgeTypeCalls         EdgeType = 0   // function calls function
    EdgeTypeCalledBy      EdgeType = 1   // reverse of Calls
    EdgeTypeImports       EdgeType = 2   // file imports package
    EdgeTypeImportedBy    EdgeType = 3   // reverse of Imports
    EdgeTypeImplements    EdgeType = 4   // struct implements interface
    EdgeTypeImplementedBy EdgeType = 5   // reverse of Implements
    EdgeTypeEmbeds        EdgeType = 6   // struct embeds struct
    EdgeTypeHasField      EdgeType = 7   // struct has field of type
    EdgeTypeHasMethod     EdgeType = 8   // type has method
    EdgeTypeDefines       EdgeType = 9   // file defines symbol
    EdgeTypeDefinedIn     EdgeType = 10  // reverse of Defines
    EdgeTypeReturns       EdgeType = 11  // function returns type
    EdgeTypeReceives      EdgeType = 12  // function receives param type
)

// History edges (50-99)
const (
    EdgeTypeProducedBy  EdgeType = 50  // entry produced by session
    EdgeTypeResultedIn  EdgeType = 51  // workflow resulted in outcome
    EdgeTypeSimilarTo   EdgeType = 52  // semantically similar
    EdgeTypeFollowedBy  EdgeType = 53  // temporal sequence
    EdgeTypeSupersedes  EdgeType = 54  // newer supersedes older
)

// Cross-domain edges (100-149)
const (
    EdgeTypeModified    EdgeType = 100  // history entry modified code
    EdgeTypeCreated     EdgeType = 101  // history entry created code
    EdgeTypeDeleted     EdgeType = 102  // history entry deleted code
    EdgeTypeBasedOn     EdgeType = 103  // decision based on academic
    EdgeTypeReferences  EdgeType = 104  // entry references academic
    EdgeTypeValidatedBy EdgeType = 105  // approach validated by academic
    EdgeTypeDocuments   EdgeType = 106  // academic documents code
    EdgeTypeUsesLibrary EdgeType = 107  // code uses library (→ docs)
    EdgeTypeImplementsPattern EdgeType = 108  // code implements pattern
)

// Academic edges (150-199)
const (
    EdgeTypeCites       EdgeType = 150  // paper cites paper
    EdgeTypeRelatedTo   EdgeType = 151  // conceptually related
)

// =============================================================================
// VERIFICATION AND TRUST TYPES
// =============================================================================

type VerificationType int

const (
    VerificationNone         VerificationType = 0
    VerificationAgainstCode  VerificationType = 1
    VerificationAgainstHistory VerificationType = 2
    VerificationByUser       VerificationType = 3
)

type SourceType int

const (
    SourceTypeCode         SourceType = 0
    SourceTypeHistory      SourceType = 1
    SourceTypeAcademic     SourceType = 2
    SourceTypeLLMInference SourceType = 3
    SourceTypeUserProvided SourceType = 4
)

type TrustLevel int

const (
    TrustLevelGround     TrustLevel = 100  // Current code (source of truth)
    TrustLevelRecent     TrustLevel = 80   // Recent verified history
    TrustLevelStandard   TrustLevel = 70   // RFCs, official docs
    TrustLevelAcademic   TrustLevel = 60   // Peer-reviewed papers
    TrustLevelOldHistory TrustLevel = 40   // Old history (may be stale)
    TrustLevelBlog       TrustLevel = 30   // Blog posts, SO answers
    TrustLevelLLM        TrustLevel = 20   // LLM inference (unverified)
)

type ConflictType int

const (
    ConflictTypeTemporal       ConflictType = 0  // Old vs new data
    ConflictTypeSourceMismatch ConflictType = 1  // Code vs history
    ConflictTypeSemantic       ConflictType = 2  // Contradictory claims
)
```

### HNSW Index Implementation (Pure Go, No Extensions)

The HNSW (Hierarchical Navigable Small World) index provides O(log n) approximate nearest neighbor search without requiring sqlite-vec or any external dependencies.

```go
// =============================================================================
// HNSW INDEX (Hierarchical Navigable Small World)
// =============================================================================

// HNSWIndex provides O(log n) approximate nearest neighbor search
type HNSWIndex struct {
    mu sync.RWMutex

    // Graph layers (layer 0 = all nodes, higher layers = fewer nodes)
    layers []map[string][]string  // layer -> nodeID -> neighbor IDs

    // Node data
    vectors    map[string][]float32
    magnitudes map[string]float64
    domains    map[string]Domain
    nodeTypes  map[string]NodeType

    // Parameters
    M            int     // Max connections per node (default: 16)
    efConstruct  int     // Beam width during construction (default: 200)
    efSearch     int     // Beam width during search (default: 50)
    levelMult    float64 // Level multiplier (default: 1/ln(M))
    maxLevel     int     // Current max level
    entryPoint   string  // Entry node ID
}

// HNSWConfig configures the HNSW index
type HNSWConfig struct {
    M           int  // Max connections per node
    EfConstruct int  // Beam width during construction
    EfSearch    int  // Beam width during search
}

// DefaultHNSWConfig returns sensible defaults
func DefaultHNSWConfig() HNSWConfig {
    return HNSWConfig{
        M:           16,
        EfConstruct: 200,
        EfSearch:    50,
    }
}

// NewHNSWIndex creates a new HNSW index
func NewHNSWIndex(config HNSWConfig) *HNSWIndex {
    if config.M == 0 {
        config = DefaultHNSWConfig()
    }

    return &HNSWIndex{
        layers:      make([]map[string][]string, 0),
        vectors:     make(map[string][]float32),
        magnitudes:  make(map[string]float64),
        domains:     make(map[string]Domain),
        nodeTypes:   make(map[string]NodeType),
        M:           config.M,
        efConstruct: config.EfConstruct,
        efSearch:    config.EfSearch,
        levelMult:   1.0 / math.Log(float64(config.M)),
    }
}

// Add inserts a node into the index
func (h *HNSWIndex) Add(nodeID string, embedding []float32, domain Domain, nodeType NodeType) {
    h.mu.Lock()
    defer h.mu.Unlock()

    mag := magnitude(embedding)
    h.vectors[nodeID] = embedding
    h.magnitudes[nodeID] = mag
    h.domains[nodeID] = domain
    h.nodeTypes[nodeID] = nodeType

    // Random level for this node
    level := h.randomLevel()

    // Ensure we have enough layers
    for len(h.layers) <= level {
        h.layers = append(h.layers, make(map[string][]string))
    }

    if h.entryPoint == "" {
        // First node
        h.entryPoint = nodeID
        h.maxLevel = level
        for l := 0; l <= level; l++ {
            h.layers[l][nodeID] = []string{}
        }
        return
    }

    // Find entry point at top level, descend
    currNode := h.entryPoint

    for l := h.maxLevel; l > level; l-- {
        currNode = h.greedySearchSingle(embedding, currNode, l)
    }

    // Insert at each level
    for l := min(level, h.maxLevel); l >= 0; l-- {
        neighbors := h.searchLayer(embedding, currNode, h.efConstruct, l)
        selected := h.selectNeighbors(embedding, neighbors, h.M)

        h.layers[l][nodeID] = selected
        for _, neighbor := range selected {
            h.layers[l][neighbor] = append(h.layers[l][neighbor], nodeID)
            if len(h.layers[l][neighbor]) > h.M*2 {
                h.layers[l][neighbor] = h.selectNeighbors(
                    h.vectors[neighbor],
                    h.layers[l][neighbor],
                    h.M,
                )
            }
        }

        if l > 0 && len(neighbors) > 0 {
            currNode = neighbors[0]
        }
    }

    if level > h.maxLevel {
        h.maxLevel = level
        h.entryPoint = nodeID
    }
}

// Search finds k nearest neighbors
func (h *HNSWIndex) Search(query []float32, k int, filter *SearchFilter) []ScoredNode {
    h.mu.RLock()
    defer h.mu.RUnlock()

    if h.entryPoint == "" {
        return nil
    }

    // Descend from top to layer 0
    currNode := h.entryPoint
    for l := h.maxLevel; l > 0; l-- {
        currNode = h.greedySearchSingle(query, currNode, l)
    }

    // Search at layer 0
    ef := h.efSearch
    if k > ef {
        ef = k
    }

    candidates := h.searchLayer(query, currNode, ef*2, 0)  // Get extra for filtering

    // Apply filter and score
    queryMag := magnitude(query)
    var results []ScoredNode

    for _, nodeID := range candidates {
        // Apply domain/type filter
        if filter != nil {
            if filter.Domain != nil && h.domains[nodeID] != *filter.Domain {
                continue
            }
            if filter.NodeType != nil && h.nodeTypes[nodeID] != *filter.NodeType {
                continue
            }
        }

        sim := cosineSimilarityPrecomputed(query, h.vectors[nodeID], queryMag, h.magnitudes[nodeID])
        results = append(results, ScoredNode{
            NodeID:     nodeID,
            Similarity: sim,
            Domain:     h.domains[nodeID],
            NodeType:   h.nodeTypes[nodeID],
        })
    }

    // Sort by similarity
    sort.Slice(results, func(i, j int) bool {
        return results[i].Similarity > results[j].Similarity
    })

    if len(results) > k {
        results = results[:k]
    }

    return results
}

// Delete removes a node from the index
func (h *HNSWIndex) Delete(nodeID string) {
    h.mu.Lock()
    defer h.mu.Unlock()

    delete(h.vectors, nodeID)
    delete(h.magnitudes, nodeID)
    delete(h.domains, nodeID)
    delete(h.nodeTypes, nodeID)

    // Remove from all layers
    for level := range h.layers {
        delete(h.layers[level], nodeID)
        // Remove references from neighbors
        for nid, neighbors := range h.layers[level] {
            newNeighbors := make([]string, 0, len(neighbors))
            for _, n := range neighbors {
                if n != nodeID {
                    newNeighbors = append(newNeighbors, n)
                }
            }
            h.layers[level][nid] = newNeighbors
        }
    }

    // Update entry point if deleted
    if h.entryPoint == nodeID {
        h.entryPoint = ""
        for _, layer := range h.layers {
            for nid := range layer {
                h.entryPoint = nid
                break
            }
            if h.entryPoint != "" {
                break
            }
        }
    }
}

// Helper functions
func (h *HNSWIndex) randomLevel() int {
    level := 0
    for rand.Float64() < 0.5 && level < 16 {
        level++
    }
    return level
}

func (h *HNSWIndex) greedySearchSingle(query []float32, entry string, level int) string {
    curr := entry
    currDist := h.distance(query, curr)

    for {
        changed := false
        for _, neighbor := range h.layers[level][curr] {
            dist := h.distance(query, neighbor)
            if dist < currDist {
                curr = neighbor
                currDist = dist
                changed = true
            }
        }
        if !changed {
            break
        }
    }

    return curr
}

func (h *HNSWIndex) searchLayer(query []float32, entry string, ef int, level int) []string {
    visited := make(map[string]bool)
    visited[entry] = true

    candidates := []string{entry}
    results := []string{entry}

    for len(candidates) > 0 {
        // Get closest candidate
        closest := candidates[0]
        closestDist := h.distance(query, closest)
        closestIdx := 0
        for i, c := range candidates[1:] {
            d := h.distance(query, c)
            if d < closestDist {
                closest = c
                closestDist = d
                closestIdx = i + 1
            }
        }
        candidates = append(candidates[:closestIdx], candidates[closestIdx+1:]...)

        // Get furthest result
        furthest := results[len(results)-1]
        furthestDist := h.distance(query, furthest)
        for _, r := range results {
            d := h.distance(query, r)
            if d > furthestDist {
                furthest = r
                furthestDist = d
            }
        }

        if closestDist > furthestDist {
            break
        }

        // Explore neighbors
        for _, neighbor := range h.layers[level][closest] {
            if visited[neighbor] {
                continue
            }
            visited[neighbor] = true

            neighborDist := h.distance(query, neighbor)
            if neighborDist < furthestDist || len(results) < ef {
                candidates = append(candidates, neighbor)
                results = append(results, neighbor)

                // Keep only ef best
                if len(results) > ef {
                    sort.Slice(results, func(i, j int) bool {
                        return h.distance(query, results[i]) < h.distance(query, results[j])
                    })
                    results = results[:ef]
                }
            }
        }
    }

    return results
}

func (h *HNSWIndex) selectNeighbors(query []float32, candidates []string, m int) []string {
    if len(candidates) <= m {
        return candidates
    }

    // Sort by distance
    sort.Slice(candidates, func(i, j int) bool {
        return h.distance(query, candidates[i]) < h.distance(query, candidates[j])
    })

    return candidates[:m]
}

func (h *HNSWIndex) distance(query []float32, nodeID string) float64 {
    return 1.0 - cosineSimilarityPrecomputed(
        query,
        h.vectors[nodeID],
        magnitude(query),
        h.magnitudes[nodeID],
    )
}

// SearchFilter filters search results
type SearchFilter struct {
    Domain   *Domain
    NodeType *NodeType
}

// ScoredNode is a search result with similarity score
type ScoredNode struct {
    NodeID     string
    Similarity float64
    Domain     Domain
    NodeType   NodeType
}

// Utility functions
func magnitude(v []float32) float64 {
    var sum float64
    for _, x := range v {
        sum += float64(x) * float64(x)
    }
    return math.Sqrt(sum)
}

func cosineSimilarityPrecomputed(a, b []float32, magA, magB float64) float64 {
    var dot float64
    for i := range a {
        dot += float64(a[i]) * float64(b[i])
    }
    if magA == 0 || magB == 0 {
        return 0
    }
    return dot / (magA * magB)
}

func serializeEmbedding(embedding []float32) []byte {
    buf := make([]byte, len(embedding)*4)
    for i, v := range embedding {
        binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
    }
    return buf
}

func deserializeEmbedding(data []byte) []float32 {
    embedding := make([]float32, len(data)/4)
    for i := range embedding {
        embedding[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
    }
    return embedding
}
```

### Core VectorGraphDB Structure

```go
// =============================================================================
// VECTORGRAPHDB CORE
// =============================================================================

// VectorGraphDB is the unified knowledge graph with vector search
type VectorGraphDB struct {
    mu sync.RWMutex

    // SQLite connection
    db *sql.DB

    // In-memory HNSW index
    hnsw *HNSWIndex

    // Embedder for generating vectors
    embedder Embedder

    // Prepared statements
    stmtInsertNode   *sql.Stmt
    stmtInsertEdge   *sql.Stmt
    stmtInsertVector *sql.Stmt
    stmtGetNode      *sql.Stmt
    stmtGetEdges     *sql.Stmt

    // Configuration
    config VectorGraphDBConfig
}

// VectorGraphDBConfig configures the database
type VectorGraphDBConfig struct {
    DBPath          string
    EmbeddingDims   int
    HNSWConfig      HNSWConfig
    MaxNodes        int
    EnableWAL       bool
}

// DefaultVectorGraphDBConfig returns sensible defaults
func DefaultVectorGraphDBConfig(dbPath string) VectorGraphDBConfig {
    return VectorGraphDBConfig{
        DBPath:        dbPath,
        EmbeddingDims: 768,
        HNSWConfig:    DefaultHNSWConfig(),
        MaxNodes:      1000000,
        EnableWAL:     true,
    }
}

// NewVectorGraphDB creates a new VectorGraphDB
func NewVectorGraphDB(config VectorGraphDBConfig, embedder Embedder) (*VectorGraphDB, error) {
    // Open SQLite with WAL mode
    dsn := config.DBPath
    if config.EnableWAL {
        dsn += "?_journal_mode=WAL&_synchronous=NORMAL"
    }

    db, err := sql.Open("sqlite3", dsn)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }

    vdb := &VectorGraphDB{
        db:       db,
        hnsw:     NewHNSWIndex(config.HNSWConfig),
        embedder: embedder,
        config:   config,
    }

    // Initialize schema
    if err := vdb.initSchema(); err != nil {
        return nil, fmt.Errorf("failed to init schema: %w", err)
    }

    // Prepare statements
    if err := vdb.prepareStatements(); err != nil {
        return nil, fmt.Errorf("failed to prepare statements: %w", err)
    }

    // Load HNSW index from disk
    if err := vdb.loadHNSWIndex(); err != nil {
        return nil, fmt.Errorf("failed to load HNSW index: %w", err)
    }

    return vdb, nil
}

// AddNode adds a node to the graph
func (vdb *VectorGraphDB) AddNode(ctx context.Context, node *Node) error {
    vdb.mu.Lock()
    defer vdb.mu.Unlock()

    // Generate embedding if content provided
    var embedding []float32
    var mag float64
    if node.Content != "" && vdb.embedder != nil {
        var err error
        embedding, err = vdb.embedder.Embed(ctx, node.Name+" "+node.Content)
        if err != nil {
            return fmt.Errorf("failed to generate embedding: %w", err)
        }
        mag = magnitude(embedding)
    }

    // Insert node
    _, err := vdb.stmtInsertNode.ExecContext(ctx,
        node.ID, node.Domain, node.Type, node.Name,
        node.Path, node.Package, node.LineStart, node.LineEnd, node.Signature,
        node.SessionID, node.Timestamp, node.Category,
        node.URL, node.Source, node.Authors, node.PublishedAt,
        node.Content, node.ContentHash, node.Metadata,
        node.Verified, node.VerificationType, node.Confidence, node.TrustLevel,
        node.ExpiresAt, node.SupersededBy,
    )
    if err != nil {
        return fmt.Errorf("failed to insert node: %w", err)
    }

    // Insert vector if embedding generated
    if len(embedding) > 0 {
        blob := serializeEmbedding(embedding)
        _, err = vdb.stmtInsertVector.ExecContext(ctx,
            node.ID, blob, mag, len(embedding), node.Domain, node.Type,
        )
        if err != nil {
            return fmt.Errorf("failed to insert vector: %w", err)
        }

        // Add to HNSW index
        vdb.hnsw.Add(node.ID, embedding, node.Domain, node.Type)
    }

    return nil
}

// AddEdge adds an edge between nodes
func (vdb *VectorGraphDB) AddEdge(ctx context.Context, sourceID, targetID string, edgeType EdgeType, weight float64, metadata map[string]any) error {
    vdb.mu.Lock()
    defer vdb.mu.Unlock()

    metaJSON, _ := json.Marshal(metadata)

    _, err := vdb.stmtInsertEdge.ExecContext(ctx,
        sourceID, targetID, edgeType, weight, metaJSON,
    )
    if err != nil {
        return fmt.Errorf("failed to insert edge: %w", err)
    }

    return nil
}

// SimilarNodes finds similar nodes using vector search
func (vdb *VectorGraphDB) SimilarNodes(ctx context.Context, query string, k int, filter *SearchFilter) ([]ScoredNode, error) {
    // Generate query embedding
    embedding, err := vdb.embedder.Embed(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("failed to embed query: %w", err)
    }

    // Search HNSW index
    vdb.mu.RLock()
    results := vdb.hnsw.Search(embedding, k, filter)
    vdb.mu.RUnlock()

    return results, nil
}

// SimilarToNode finds nodes similar to an existing node
func (vdb *VectorGraphDB) SimilarToNode(ctx context.Context, nodeID string, k int, filter *SearchFilter) ([]ScoredNode, error) {
    vdb.mu.RLock()
    embedding, ok := vdb.hnsw.vectors[nodeID]
    vdb.mu.RUnlock()

    if !ok {
        return nil, fmt.Errorf("node not found: %s", nodeID)
    }

    vdb.mu.RLock()
    results := vdb.hnsw.Search(embedding, k+1, filter)  // +1 because it will find itself
    vdb.mu.RUnlock()

    // Remove self from results
    filtered := make([]ScoredNode, 0, k)
    for _, r := range results {
        if r.NodeID != nodeID {
            filtered = append(filtered, r)
        }
    }

    return filtered, nil
}

// TraverseEdges traverses edges from a node
func (vdb *VectorGraphDB) TraverseEdges(ctx context.Context, nodeID string, edgeType EdgeType, direction string) ([]*Node, error) {
    var query string
    if direction == "outgoing" {
        query = `
            SELECT n.* FROM nodes n
            JOIN edges e ON e.target_id = n.id
            WHERE e.source_id = ? AND e.edge_type = ?
        `
    } else {
        query = `
            SELECT n.* FROM nodes n
            JOIN edges e ON e.source_id = n.id
            WHERE e.target_id = ? AND e.edge_type = ?
        `
    }

    return vdb.queryNodes(ctx, query, nodeID, edgeType)
}

// GetNode retrieves a node by ID
func (vdb *VectorGraphDB) GetNode(ctx context.Context, nodeID string) (*Node, error) {
    row := vdb.stmtGetNode.QueryRowContext(ctx, nodeID)
    return vdb.scanNode(row)
}

// Close closes the database
func (vdb *VectorGraphDB) Close() error {
    // Save HNSW index
    if err := vdb.saveHNSWIndex(); err != nil {
        return fmt.Errorf("failed to save HNSW index: %w", err)
    }

    return vdb.db.Close()
}
```

### Cross-Domain Query Examples

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CROSS-DOMAIN QUERY EXAMPLES                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  QUERY: "What patterns does our auth code implement?"                       │
│  ═══════════════════════════════════════════════════                        │
│                                                                             │
│       LIBRARIAN                           ACADEMIC                          │
│       (Code)                              (Knowledge)                       │
│           │                                   │                             │
│           ▼                                   │                             │
│  1. Vector search: "authentication"          │                             │
│     Returns: auth/middleware.go,             │                             │
│              auth/jwt.go, etc.               │                             │
│           │                                   │                             │
│           ▼                                   │                             │
│  2. Traverse IMPLEMENTS_PATTERN edges ──────▶ 3. Get pattern nodes         │
│                                               │                             │
│                                               ▼                             │
│                                            Result:                          │
│                                            • "JWT Bearer Token Pattern"     │
│                                            • "Middleware Chain Pattern"     │
│  Token cost: 0 (graph traversal only)                                      │
│                                                                             │
│  ───────────────────────────────────────────────────────────────────────   │
│                                                                             │
│  QUERY: "Why did we choose JWT over sessions?"                             │
│  ═════════════════════════════════════════════                              │
│                                                                             │
│       ARCHIVALIST                         ACADEMIC                          │
│       (History)                           (Knowledge)                       │
│           │                                   │                             │
│           ▼                                   │                             │
│  1. Search history: "JWT sessions decision"  │                             │
│     Returns: decision entry from 2024-06     │                             │
│           │                                   │                             │
│           ▼                                   │                             │
│  2. Traverse BASED_ON edges ────────────────▶ 3. Get referenced sources    │
│                                               │                             │
│                                               ▼                             │
│                                            Result:                          │
│                                            • RFC 7519 (JWT spec)            │
│                                            • "Stateless Auth at Scale"      │
│  Token cost: 0 (graph traversal only)                                      │
│                                                                             │
│  ───────────────────────────────────────────────────────────────────────   │
│                                                                             │
│  QUERY: "What changes did we make to auth code last week?"                 │
│  ═════════════════════════════════════════════════════════                  │
│                                                                             │
│       ARCHIVALIST              LIBRARIAN                                    │
│       (History)                (Code)                                       │
│           │                        │                                        │
│           ▼                        │                                        │
│  1. Query entries from             │                                        │
│     last 7 days with               │                                        │
│     subject "auth"                 │                                        │
│           │                        │                                        │
│           ▼                        │                                        │
│  2. Traverse MODIFIED edges ──────▶ 3. Resolve to current code nodes       │
│                                    │                                        │
│                                    ▼                                        │
│                                 Result:                                     │
│                                 • auth/jwt.go:ValidateToken (modified)     │
│                                 • auth/refresh.go (created)                │
│  Token cost: 0 (graph traversal only)                                      │
│                                                                             │
│  ───────────────────────────────────────────────────────────────────────   │
│                                                                             │
│  QUERY: "Have we had issues with this function before?"                    │
│  ══════════════════════════════════════════════════════                     │
│                                                                             │
│       LIBRARIAN                ARCHIVALIST                                  │
│       (Code)                   (History)                                    │
│           │                        │                                        │
│           ▼                        │                                        │
│  1. Identify function node         │                                        │
│     (from context or query)        │                                        │
│           │                        │                                        │
│           ▼                        │                                        │
│  2. Traverse MODIFIED edges ──────▶ 3. Get history entries that            │
│     (reverse direction)               modified this function               │
│                                    │                                        │
│                                    ▼                                        │
│                                 4. Filter for outcomes with                │
│                                    category "bug_fix" or "issue"           │
│                                    │                                        │
│                                    ▼                                        │
│                                 Result:                                     │
│                                 • 2024-01-05: Token expiry bypass (fixed)  │
│                                 • 2024-02-12: Algorithm confusion (fixed)  │
│  Token cost: 0 (graph traversal only)                                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

```go
// Cross-domain query implementations

// GetHistoryForCode returns history entries that modified a code node
func (vdb *VectorGraphDB) GetHistoryForCode(ctx context.Context, codeNodeID string) ([]*Node, error) {
    query := `
        SELECT n.*
        FROM nodes n
        JOIN edges e ON e.source_id = n.id
        WHERE e.target_id = ?
          AND e.edge_type IN (?, ?, ?)
          AND n.domain = ?
        ORDER BY n.timestamp DESC
    `
    return vdb.queryNodes(ctx, query,
        codeNodeID,
        EdgeTypeModified, EdgeTypeCreated, EdgeTypeDeleted,
        DomainHistory,
    )
}

// GetCodeForHistory returns code nodes referenced by a history entry
func (vdb *VectorGraphDB) GetCodeForHistory(ctx context.Context, historyNodeID string) ([]*Node, error) {
    query := `
        SELECT n.*
        FROM nodes n
        JOIN edges e ON e.target_id = n.id
        WHERE e.source_id = ?
          AND e.edge_type IN (?, ?, ?)
          AND n.domain = ?
    `
    return vdb.queryNodes(ctx, query,
        historyNodeID,
        EdgeTypeModified, EdgeTypeCreated, EdgeTypeDeleted,
        DomainCode,
    )
}

// GetAcademicForCode returns academic nodes that document code
func (vdb *VectorGraphDB) GetAcademicForCode(ctx context.Context, codeNodeID string) ([]*Node, error) {
    query := `
        SELECT n.*
        FROM nodes n
        JOIN edges e ON e.source_id = n.id
        WHERE e.target_id = ?
          AND e.edge_type IN (?, ?)
          AND n.domain = ?
    `
    return vdb.queryNodes(ctx, query,
        codeNodeID,
        EdgeTypeDocuments, EdgeTypeImplementsPattern,
        DomainAcademic,
    )
}

// GetAcademicForDecision returns academic nodes that a decision was based on
func (vdb *VectorGraphDB) GetAcademicForDecision(ctx context.Context, historyNodeID string) ([]*Node, error) {
    query := `
        SELECT n.*
        FROM nodes n
        JOIN edges e ON e.target_id = n.id
        WHERE e.source_id = ?
          AND e.edge_type IN (?, ?, ?)
          AND n.domain = ?
    `
    return vdb.queryNodes(ctx, query,
        historyNodeID,
        EdgeTypeBasedOn, EdgeTypeReferences, EdgeTypeValidatedBy,
        DomainAcademic,
    )
}

// SimilarAcrossDomainsWithContext performs vector search with graph context
func (vdb *VectorGraphDB) SimilarAcrossDomainsWithContext(
    ctx context.Context,
    query string,
    k int,
    pathFilter string,
) (*CrossDomainResult, error) {
    // 1. Vector search across all domains
    embedding, err := vdb.embedder.Embed(ctx, query)
    if err != nil {
        return nil, err
    }

    similar := vdb.hnsw.Search(embedding, k*3, nil)  // Get extra for filtering

    // 2. Filter by path if specified
    var filtered []ScoredNode
    for _, node := range similar {
        if pathFilter != "" {
            n, err := vdb.GetNode(ctx, node.NodeID)
            if err != nil {
                continue
            }
            if n.Path != "" && !strings.Contains(n.Path, pathFilter) {
                continue
            }
        }
        filtered = append(filtered, node)
        if len(filtered) >= k {
            break
        }
    }

    // 3. Enrich with cross-domain context
    result := &CrossDomainResult{
        Query:   query,
        Results: make([]EnrichedNode, len(filtered)),
    }

    for i, scored := range filtered {
        node, _ := vdb.GetNode(ctx, scored.NodeID)
        enriched := EnrichedNode{
            Node:       node,
            Similarity: scored.Similarity,
        }

        // Get cross-domain context based on domain
        switch node.Domain {
        case DomainCode:
            enriched.History, _ = vdb.GetHistoryForCode(ctx, node.ID)
            enriched.Academic, _ = vdb.GetAcademicForCode(ctx, node.ID)
        case DomainHistory:
            enriched.Code, _ = vdb.GetCodeForHistory(ctx, node.ID)
            enriched.Academic, _ = vdb.GetAcademicForDecision(ctx, node.ID)
        case DomainAcademic:
            // Get code that references this academic node
            enriched.Code, _ = vdb.queryNodes(ctx, `
                SELECT n.* FROM nodes n
                JOIN edges e ON e.source_id = n.id
                WHERE e.target_id = ? AND n.domain = ?
            `, node.ID, DomainCode)
        }

        result.Results[i] = enriched
    }

    return result, nil
}

// CrossDomainResult contains enriched search results
type CrossDomainResult struct {
    Query   string
    Results []EnrichedNode
}

// EnrichedNode is a node with cross-domain context
type EnrichedNode struct {
    Node       *Node
    Similarity float64
    Code       []*Node  // Related code nodes
    History    []*Node  // Related history nodes
    Academic   []*Node  // Related academic nodes
}
```

### Mitigation 1: Hallucination Firewall (Verify Before Store)

**Risk**: LLM hallucinates facts, they get stored, then retrieved as "evidence" creating a feedback loop.

**Mitigation**: Verify all LLM-generated content against source of truth before storing.

```go
// =============================================================================
// MITIGATION 1: HALLUCINATION FIREWALL
// =============================================================================

// HallucinationFirewall verifies content before storage
type HallucinationFirewall struct {
    vdb       *VectorGraphDB
    librarian *Librarian  // For code verification
}

// VerificationResult contains verification outcome
type VerificationResult struct {
    Verified       bool
    VerificationType VerificationType
    Confidence     float64
    Warnings       []string
    Contradictions []string
    SourceNodes    []string  // Nodes used for verification
}

// VerifyBeforeStore verifies an entry before storing
func (hf *HallucinationFirewall) VerifyBeforeStore(ctx context.Context, entry *HistoryEntry) (*VerificationResult, error) {
    result := &VerificationResult{
        Verified:   true,
        Confidence: 1.0,
    }

    // 1. Extract claims from content
    claims := hf.extractCodeClaims(entry.Content)

    // 2. Verify each claim against code
    for _, claim := range claims {
        verified, sources, err := hf.verifyClaimAgainstCode(ctx, claim)
        if err != nil {
            result.Warnings = append(result.Warnings,
                fmt.Sprintf("Could not verify claim: %s", claim.Text))
            continue
        }

        if !verified {
            result.Verified = false
            result.Contradictions = append(result.Contradictions,
                fmt.Sprintf("Claim '%s' contradicts code", claim.Text))
            result.Confidence *= 0.5
        } else {
            result.SourceNodes = append(result.SourceNodes, sources...)
        }
    }

    // 3. Verify referenced files exist
    for _, filePath := range entry.ReferencedFiles {
        exists, err := hf.librarian.FileExists(ctx, filePath)
        if err != nil || !exists {
            result.Warnings = append(result.Warnings,
                fmt.Sprintf("Referenced file not found: %s", filePath))
            result.Confidence *= 0.8
        }
    }

    // 4. Check for contradictions with recent history
    contradictions, err := hf.findHistoryContradictions(ctx, entry)
    if err == nil && len(contradictions) > 0 {
        result.Contradictions = append(result.Contradictions, contradictions...)
        result.Confidence *= 0.7
    }

    // 5. Set verification type
    if result.Verified && result.Confidence >= 0.8 {
        result.VerificationType = VerificationAgainstCode
    } else {
        result.VerificationType = VerificationNone
    }

    return result, nil
}

// Claim represents an extractable claim from content
type Claim struct {
    Text    string
    Subject string
    Type    string  // "uses", "implements", "has", "is"
}

// extractCodeClaims extracts verifiable claims from content
func (hf *HallucinationFirewall) extractCodeClaims(content string) []Claim {
    var claims []Claim

    // Pattern: "we use X for Y"
    usePatterns := regexp.MustCompile(`(?i)we use (\w+) for (\w+)`)
    matches := usePatterns.FindAllStringSubmatch(content, -1)
    for _, m := range matches {
        claims = append(claims, Claim{
            Text:    m[0],
            Subject: m[1],
            Type:    "uses",
        })
    }

    // Pattern: "X implements Y"
    implPatterns := regexp.MustCompile(`(?i)(\w+) implements (\w+)`)
    matches = implPatterns.FindAllStringSubmatch(content, -1)
    for _, m := range matches {
        claims = append(claims, Claim{
            Text:    m[0],
            Subject: m[1],
            Type:    "implements",
        })
    }

    // Pattern: "the X is in Y"
    locPatterns := regexp.MustCompile(`(?i)the (\w+) is in (\S+)`)
    matches = locPatterns.FindAllStringSubmatch(content, -1)
    for _, m := range matches {
        claims = append(claims, Claim{
            Text:    m[0],
            Subject: m[1],
            Type:    "location",
        })
    }

    return claims
}

// verifyClaimAgainstCode verifies a claim against the codebase
func (hf *HallucinationFirewall) verifyClaimAgainstCode(ctx context.Context, claim Claim) (bool, []string, error) {
    // Search code for evidence
    results, err := hf.vdb.SimilarNodes(ctx, claim.Subject, 10, &SearchFilter{
        Domain: ptr(DomainCode),
    })
    if err != nil {
        return false, nil, err
    }

    if len(results) == 0 {
        return false, nil, nil  // No evidence found
    }

    // Check if any result supports the claim
    var sources []string
    for _, result := range results {
        node, err := hf.vdb.GetNode(ctx, result.NodeID)
        if err != nil {
            continue
        }

        // Simple heuristic: if claim subject appears in code content
        if strings.Contains(strings.ToLower(node.Content), strings.ToLower(claim.Subject)) {
            sources = append(sources, node.ID)
        }
    }

    return len(sources) > 0, sources, nil
}

// findHistoryContradictions finds contradictions with recent history
func (hf *HallucinationFirewall) findHistoryContradictions(ctx context.Context, entry *HistoryEntry) ([]string, error) {
    // Get recent entries about same subject
    results, err := hf.vdb.SimilarNodes(ctx, entry.Summary, 20, &SearchFilter{
        Domain: ptr(DomainHistory),
    })
    if err != nil {
        return nil, err
    }

    var contradictions []string

    // Check for contradicting claims
    entryClaims := hf.extractCodeClaims(entry.Content)

    for _, result := range results {
        if result.Similarity < 0.7 {
            continue  // Not similar enough to be relevant
        }

        node, err := hf.vdb.GetNode(ctx, result.NodeID)
        if err != nil {
            continue
        }

        historyClaims := hf.extractCodeClaims(node.Content)

        for _, ec := range entryClaims {
            for _, hc := range historyClaims {
                if ec.Subject == hc.Subject && ec.Type == hc.Type {
                    // Same subject and type but different claim
                    if ec.Text != hc.Text {
                        contradictions = append(contradictions,
                            fmt.Sprintf("New: '%s' vs History: '%s'", ec.Text, hc.Text))
                    }
                }
            }
        }
    }

    return contradictions, nil
}

// StoreWithVerification stores an entry with verification
func (hf *HallucinationFirewall) StoreWithVerification(ctx context.Context, entry *HistoryEntry) error {
    // Verify first
    result, err := hf.VerifyBeforeStore(ctx, entry)
    if err != nil {
        return err
    }

    // Create node with verification metadata
    node := &Node{
        ID:               entry.ID,
        Domain:           DomainHistory,
        Type:             NodeTypeHistoryEntry,
        Name:             entry.Summary,
        Content:          entry.Content,
        SessionID:        entry.SessionID,
        Timestamp:        entry.Timestamp,
        Category:         entry.Category,
        Verified:         result.Verified,
        VerificationType: result.VerificationType,
        Confidence:       result.Confidence,
        TrustLevel:       int(TrustLevelRecent),
    }

    // Mark as unverified if failed
    if !result.Verified {
        node.TrustLevel = int(TrustLevelLLM)
        node.Content = "[UNVERIFIED] " + node.Content

        // Store warnings in metadata
        meta := map[string]any{
            "warnings":       result.Warnings,
            "contradictions": result.Contradictions,
            "needs_review":   true,
        }
        metaJSON, _ := json.Marshal(meta)
        node.Metadata = string(metaJSON)
    }

    // Store node
    if err := hf.vdb.AddNode(ctx, node); err != nil {
        return err
    }

    // Create provenance edges to source nodes
    for _, sourceID := range result.SourceNodes {
        hf.vdb.AddEdge(ctx, node.ID, sourceID, EdgeTypeReferences, 1.0, nil)
    }

    return nil
}

func ptr[T any](v T) *T { return &v }
```

### Mitigation 2: Freshness Tracking & Decay

**Risk**: Stale data gets retrieved and presented as current truth.

**Mitigation**: Track creation time, apply decay, prefer fresh data.

```go
// =============================================================================
// MITIGATION 2: FRESHNESS TRACKING & DECAY
// =============================================================================

// FreshnessTracker manages temporal relevance of nodes
type FreshnessTracker struct {
    vdb *VectorGraphDB
}

// FreshnessScore represents a node's temporal relevance
type FreshnessScore struct {
    Score           float64
    Age             time.Duration
    IsSuperseded    bool
    SupersededBy    string
    Warning         string
}

// CalculateFreshness calculates freshness score for a node
func (ft *FreshnessTracker) CalculateFreshness(ctx context.Context, node *Node) FreshnessScore {
    result := FreshnessScore{
        Score: 1.0,
    }

    // Check if superseded
    if node.SupersededBy != "" {
        result.IsSuperseded = true
        result.SupersededBy = node.SupersededBy
        result.Score = 0.1  // Heavily penalize superseded nodes
        result.Warning = "⚠️ SUPERSEDED - newer information available"
        return result
    }

    // Calculate age
    if !node.Timestamp.IsZero() {
        result.Age = time.Since(node.Timestamp)
    } else {
        result.Age = time.Since(node.CreatedAt)
    }

    // Apply domain-specific decay
    switch node.Domain {
    case DomainCode:
        // Code freshness based on content hash (checked separately)
        result.Score = 1.0  // Assume fresh if hash matches
        result.Warning = ""

    case DomainHistory:
        // History decays over time
        // 1 day = 0.95, 1 week = 0.8, 1 month = 0.6, 6 months = 0.3
        days := result.Age.Hours() / 24
        result.Score = math.Max(0.2, 1.0-days/180.0*0.8)

        if days > 30 {
            result.Warning = fmt.Sprintf("⚠️ %d days old - may be outdated", int(days))
        }

    case DomainAcademic:
        // Academic content decays slower
        days := result.Age.Hours() / 24
        result.Score = math.Max(0.3, 1.0-days/365.0*0.7)

        if days > 180 {
            result.Warning = fmt.Sprintf("⚠️ %d days old - verify still current", int(days))
        }
    }

    // Check expiration
    if node.ExpiresAt != nil && time.Now().After(*node.ExpiresAt) {
        result.Score *= 0.5
        result.Warning = "⚠️ EXPIRED - should be refreshed"
    }

    return result
}

// GetWithFreshness retrieves a node with freshness information
func (ft *FreshnessTracker) GetWithFreshness(ctx context.Context, nodeID string) (*NodeWithFreshness, error) {
    node, err := ft.vdb.GetNode(ctx, nodeID)
    if err != nil {
        return nil, err
    }

    freshness := ft.CalculateFreshness(ctx, node)

    // If superseded, optionally get the newer version
    var currentVersion *Node
    if freshness.IsSuperseded {
        currentVersion, _ = ft.vdb.GetNode(ctx, freshness.SupersededBy)
    }

    return &NodeWithFreshness{
        Node:           node,
        Freshness:      freshness,
        CurrentVersion: currentVersion,
    }, nil
}

// NodeWithFreshness wraps a node with freshness metadata
type NodeWithFreshness struct {
    Node           *Node
    Freshness      FreshnessScore
    CurrentVersion *Node  // If superseded, this is the newer version
}

// SupersedeNode marks a node as superseded by another
func (ft *FreshnessTracker) SupersedeNode(ctx context.Context, oldNodeID, newNodeID string) error {
    _, err := ft.vdb.db.ExecContext(ctx, `
        UPDATE nodes SET superseded_by = ?, updated_at = CURRENT_TIMESTAMP
        WHERE id = ?
    `, newNodeID, oldNodeID)
    if err != nil {
        return err
    }

    // Add supersedes edge
    return ft.vdb.AddEdge(ctx, newNodeID, oldNodeID, EdgeTypeSupersedes, 1.0, nil)
}

// RefreshRanking applies freshness to search results
func (ft *FreshnessTracker) RefreshRanking(ctx context.Context, results []ScoredNode) []ScoredNodeWithFreshness {
    ranked := make([]ScoredNodeWithFreshness, len(results))

    for i, result := range results {
        node, err := ft.vdb.GetNode(ctx, result.NodeID)
        if err != nil {
            ranked[i] = ScoredNodeWithFreshness{
                ScoredNode:   result,
                Freshness:    FreshnessScore{Score: 0.5},
                CombinedScore: result.Similarity * 0.5,
            }
            continue
        }

        freshness := ft.CalculateFreshness(ctx, node)

        // Combined score: similarity * freshness
        combined := result.Similarity * freshness.Score

        ranked[i] = ScoredNodeWithFreshness{
            ScoredNode:    result,
            Freshness:     freshness,
            CombinedScore: combined,
        }
    }

    // Sort by combined score
    sort.Slice(ranked, func(i, j int) bool {
        return ranked[i].CombinedScore > ranked[j].CombinedScore
    })

    return ranked
}

// ScoredNodeWithFreshness extends ScoredNode with freshness
type ScoredNodeWithFreshness struct {
    ScoredNode
    Freshness     FreshnessScore
    CombinedScore float64
}

// CleanupExpired removes or archives expired nodes
func (ft *FreshnessTracker) CleanupExpired(ctx context.Context) (int, error) {
    result, err := ft.vdb.db.ExecContext(ctx, `
        DELETE FROM nodes
        WHERE expires_at IS NOT NULL
          AND expires_at < datetime('now', '-30 days')
          AND domain = ?
    `, DomainHistory)  // Only auto-cleanup history, not code or academic

    if err != nil {
        return 0, err
    }

    affected, _ := result.RowsAffected()
    return int(affected), nil
}
```

### Mitigation 3: Source Attribution & Provenance

**Risk**: User can't tell where information came from (code? history? academic? LLM inference?).

**Mitigation**: Track and expose provenance for all information.

```go
// =============================================================================
// MITIGATION 3: SOURCE ATTRIBUTION & PROVENANCE
// =============================================================================

// ProvenanceTracker tracks and exposes information sources
type ProvenanceTracker struct {
    vdb *VectorGraphDB
}

// ProvenanceChain represents the full source chain for information
type ProvenanceChain struct {
    Sources []ProvenanceSource
}

// ProvenanceSource represents a single source in the chain
type ProvenanceSource struct {
    Type       SourceType
    NodeID     string
    Path       string    // File path (if code)
    URL        string    // URL (if academic)
    Timestamp  time.Time
    Confidence float64
    Verified   bool
}

// AddProvenance records provenance for a node
func (pt *ProvenanceTracker) AddProvenance(ctx context.Context, nodeID string, source ProvenanceSource) error {
    _, err := pt.vdb.db.ExecContext(ctx, `
        INSERT INTO provenance (node_id, source_type, source_node_id, source_path, source_url, confidence, verified_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, nodeID, source.Type, source.NodeID, source.Path, source.URL, source.Confidence,
        func() any { if source.Verified { return time.Now() } else { return nil } }())

    return err
}

// GetProvenance retrieves provenance chain for a node
func (pt *ProvenanceTracker) GetProvenance(ctx context.Context, nodeID string) (*ProvenanceChain, error) {
    rows, err := pt.vdb.db.QueryContext(ctx, `
        SELECT source_type, source_node_id, source_path, source_url, confidence, verified_at
        FROM provenance
        WHERE node_id = ?
        ORDER BY confidence DESC
    `, nodeID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    chain := &ProvenanceChain{}

    for rows.Next() {
        var source ProvenanceSource
        var verifiedAt sql.NullTime

        err := rows.Scan(&source.Type, &source.NodeID, &source.Path, &source.URL, &source.Confidence, &verifiedAt)
        if err != nil {
            continue
        }

        source.Verified = verifiedAt.Valid
        if verifiedAt.Valid {
            source.Timestamp = verifiedAt.Time
        }

        chain.Sources = append(chain.Sources, source)
    }

    return chain, nil
}

// AnnotatedResponse is a response with source annotations
type AnnotatedResponse struct {
    Text     string
    Segments []AnnotatedSegment
}

// AnnotatedSegment is a text segment with provenance
type AnnotatedSegment struct {
    Text       string
    Start      int
    End        int
    Provenance ProvenanceChain
    SourceRef  string  // [1], [2], etc.
}

// AnnotateResponse adds source citations to a response
func (pt *ProvenanceTracker) AnnotateResponse(ctx context.Context, response string, usedNodes []string) (*AnnotatedResponse, error) {
    annotated := &AnnotatedResponse{
        Text: response,
    }

    // Build source references
    sourceRefs := make(map[string]string)  // nodeID -> [n]
    var sourceList []string

    for i, nodeID := range usedNodes {
        ref := fmt.Sprintf("[%d]", i+1)
        sourceRefs[nodeID] = ref
        sourceList = append(sourceList, nodeID)
    }

    // Create segments for each source mention
    for nodeID, ref := range sourceRefs {
        provenance, err := pt.GetProvenance(ctx, nodeID)
        if err != nil {
            continue
        }

        // Find where this source's content appears in response
        node, err := pt.vdb.GetNode(ctx, nodeID)
        if err != nil {
            continue
        }

        // Simple heuristic: look for key terms from the node
        keyTerms := extractKeyTerms(node.Content)
        for _, term := range keyTerms {
            idx := strings.Index(strings.ToLower(response), strings.ToLower(term))
            if idx >= 0 {
                annotated.Segments = append(annotated.Segments, AnnotatedSegment{
                    Text:       term,
                    Start:      idx,
                    End:        idx + len(term),
                    Provenance: *provenance,
                    SourceRef:  ref,
                })
                break  // One segment per source
            }
        }
    }

    return annotated, nil
}

// FormatSourceList formats sources for display
func (pt *ProvenanceTracker) FormatSourceList(ctx context.Context, nodeIDs []string) (string, error) {
    var lines []string

    for i, nodeID := range nodeIDs {
        node, err := pt.vdb.GetNode(ctx, nodeID)
        if err != nil {
            continue
        }

        provenance, _ := pt.GetProvenance(ctx, nodeID)

        var sourceDesc string
        var warning string

        switch node.Domain {
        case DomainCode:
            sourceDesc = fmt.Sprintf("Code: %s", node.Path)
            if node.LineStart > 0 {
                sourceDesc += fmt.Sprintf(":%d-%d", node.LineStart, node.LineEnd)
            }
            warning = "(verified, current)"

        case DomainHistory:
            age := time.Since(node.Timestamp)
            sourceDesc = fmt.Sprintf("History: %s", node.Timestamp.Format("2006-01-02"))
            if age.Hours() > 24*30 {
                warning = fmt.Sprintf("(%d days old)", int(age.Hours()/24))
            } else {
                warning = "(recent)"
            }

        case DomainAcademic:
            sourceDesc = fmt.Sprintf("Academic: %s", node.Name)
            if node.URL != "" {
                sourceDesc += fmt.Sprintf(" (%s)", node.URL)
            }
            switch node.Type {
            case NodeTypeRFC:
                warning = "(authoritative)"
            case NodeTypeDocumentation:
                warning = "(official docs)"
            default:
                warning = "(external)"
            }
        }

        if !node.Verified {
            warning = "⚠️ UNVERIFIED"
        }

        // Add provenance details
        if provenance != nil && len(provenance.Sources) > 0 {
            sourceDesc += " via " + sourceTypeName(provenance.Sources[0].Type)
        }

        lines = append(lines, fmt.Sprintf("[%d] %s %s", i+1, sourceDesc, warning))
    }

    return strings.Join(lines, "\n"), nil
}

func sourceTypeName(t SourceType) string {
    switch t {
    case SourceTypeCode:
        return "code analysis"
    case SourceTypeHistory:
        return "history lookup"
    case SourceTypeAcademic:
        return "external reference"
    case SourceTypeLLMInference:
        return "inference"
    case SourceTypeUserProvided:
        return "user input"
    default:
        return "unknown"
    }
}

func extractKeyTerms(content string) []string {
    // Simple term extraction (in practice, use NLP)
    words := strings.Fields(content)
    var terms []string
    for _, word := range words {
        if len(word) > 5 {  // Only meaningful words
            terms = append(terms, word)
        }
        if len(terms) >= 5 {
            break
        }
    }
    return terms
}
```

### Mitigation 4: Trust Hierarchy

**Risk**: LLM doesn't know which sources to trust when they conflict.

**Mitigation**: Explicit trust hierarchy with code as ground truth.

```go
// =============================================================================
// MITIGATION 4: TRUST HIERARCHY
// =============================================================================

// TrustHierarchy manages source trustworthiness
type TrustHierarchy struct {
    vdb *VectorGraphDB
}

// GetTrustLevel returns the trust level for a node
func (th *TrustHierarchy) GetTrustLevel(ctx context.Context, node *Node) TrustLevel {
    // Code is always ground truth
    if node.Domain == DomainCode {
        return TrustLevelGround
    }

    // Check if verified
    if !node.Verified {
        return TrustLevelLLM
    }

    // History trust depends on age and verification
    if node.Domain == DomainHistory {
        age := time.Since(node.Timestamp)
        if age < 7*24*time.Hour {
            return TrustLevelRecent
        } else if age < 30*24*time.Hour {
            return TrustLevel(60)  // Between recent and academic
        } else {
            return TrustLevelOldHistory
        }
    }

    // Academic trust depends on type
    if node.Domain == DomainAcademic {
        switch node.Type {
        case NodeTypeRFC:
            return TrustLevelStandard
        case NodeTypeDocumentation:
            return TrustLevelStandard
        case NodeTypePaper:
            return TrustLevelAcademic
        case NodeTypeBestPractice:
            return TrustLevelAcademic
        case NodeTypeStackOverflow, NodeTypeBlogPost:
            return TrustLevelBlog
        default:
            return TrustLevelAcademic
        }
    }

    return TrustLevel(node.TrustLevel)
}

// TrustLevelName returns a human-readable trust level name
func TrustLevelName(level TrustLevel) string {
    switch {
    case level >= TrustLevelGround:
        return "Ground Truth (Code)"
    case level >= TrustLevelRecent:
        return "Recent Verified"
    case level >= TrustLevelStandard:
        return "Authoritative"
    case level >= TrustLevelAcademic:
        return "Academic"
    case level >= TrustLevelOldHistory:
        return "Old History"
    case level >= TrustLevelBlog:
        return "Informal"
    default:
        return "Unverified/Inferred"
    }
}

// TrustWarning returns a warning message for low-trust content
func (th *TrustHierarchy) TrustWarning(level TrustLevel) string {
    switch {
    case level >= TrustLevelGround:
        return ""
    case level >= TrustLevelRecent:
        return "From recent history, may need verification"
    case level >= TrustLevelStandard:
        return "From external docs, verify applies to your context"
    case level >= TrustLevelAcademic:
        return "Academic source, may not apply directly"
    case level >= TrustLevelOldHistory:
        return "⚠️ OLD DATA - verify this is still accurate"
    case level >= TrustLevelBlog:
        return "⚠️ Informal source - verify independently"
    default:
        return "⚠️ UNVERIFIED - LLM inference, may be incorrect"
    }
}

// RankByTrust sorts nodes by trust level
func (th *TrustHierarchy) RankByTrust(ctx context.Context, nodes []*Node) []*NodeWithTrust {
    ranked := make([]*NodeWithTrust, len(nodes))

    for i, node := range nodes {
        trust := th.GetTrustLevel(ctx, node)
        ranked[i] = &NodeWithTrust{
            Node:    node,
            Trust:   trust,
            Warning: th.TrustWarning(trust),
        }
    }

    // Sort by trust level (highest first)
    sort.Slice(ranked, func(i, j int) bool {
        return ranked[i].Trust > ranked[j].Trust
    })

    return ranked
}

// NodeWithTrust wraps a node with trust information
type NodeWithTrust struct {
    Node    *Node
    Trust   TrustLevel
    Warning string
}

// ResolveConflict resolves conflicts between nodes using trust hierarchy
func (th *TrustHierarchy) ResolveConflict(ctx context.Context, nodeA, nodeB *Node) *ConflictResolution {
    trustA := th.GetTrustLevel(ctx, nodeA)
    trustB := th.GetTrustLevel(ctx, nodeB)

    resolution := &ConflictResolution{
        NodeA:      nodeA,
        NodeB:      nodeB,
        TrustA:     trustA,
        TrustB:     trustB,
    }

    // Code always wins
    if nodeA.Domain == DomainCode && nodeB.Domain != DomainCode {
        resolution.Winner = nodeA
        resolution.Reason = "Code is source of truth"
        return resolution
    }
    if nodeB.Domain == DomainCode && nodeA.Domain != DomainCode {
        resolution.Winner = nodeB
        resolution.Reason = "Code is source of truth"
        return resolution
    }

    // Higher trust wins
    if trustA > trustB {
        resolution.Winner = nodeA
        resolution.Reason = fmt.Sprintf("%s > %s", TrustLevelName(trustA), TrustLevelName(trustB))
    } else if trustB > trustA {
        resolution.Winner = nodeB
        resolution.Reason = fmt.Sprintf("%s > %s", TrustLevelName(trustB), TrustLevelName(trustA))
    } else {
        // Same trust - prefer newer
        if nodeA.Timestamp.After(nodeB.Timestamp) {
            resolution.Winner = nodeA
            resolution.Reason = "Same trust level, preferring newer"
        } else {
            resolution.Winner = nodeB
            resolution.Reason = "Same trust level, preferring newer"
        }
    }

    return resolution
}

// ConflictResolution contains the resolution of a conflict
type ConflictResolution struct {
    NodeA   *Node
    NodeB   *Node
    TrustA  TrustLevel
    TrustB  TrustLevel
    Winner  *Node
    Reason  string
}
```

### Mitigation 5: Conflict Detection

**Risk**: Contradictory information retrieved without the user knowing.

**Mitigation**: Detect and surface conflicts before presenting to LLM/user.

```go
// =============================================================================
// MITIGATION 5: CONFLICT DETECTION
// =============================================================================

// ConflictDetector detects contradictions in retrieved context
type ConflictDetector struct {
    vdb   *VectorGraphDB
    trust *TrustHierarchy
}

// Conflict represents a detected contradiction
type Conflict struct {
    Type        ConflictType
    Subject     string
    NodeA       *Node
    NodeB       *Node
    Description string
    Resolution  string
    Resolved    bool
}

// DetectConflicts finds conflicts in a set of retrieved nodes
func (cd *ConflictDetector) DetectConflicts(ctx context.Context, nodes []*Node) ([]Conflict, error) {
    var conflicts []Conflict

    // 1. Temporal conflicts (old vs new on same subject)
    temporal := cd.detectTemporalConflicts(nodes)
    conflicts = append(conflicts, temporal...)

    // 2. Source conflicts (code says X, history says Y)
    source := cd.detectSourceConflicts(ctx, nodes)
    conflicts = append(conflicts, source...)

    // 3. Semantic contradictions (claim A contradicts claim B)
    semantic := cd.detectSemanticConflicts(nodes)
    conflicts = append(conflicts, semantic...)

    // Store detected conflicts
    for _, c := range conflicts {
        cd.storeConflict(ctx, c)
    }

    return conflicts, nil
}

// detectTemporalConflicts finds conflicts due to time differences
func (cd *ConflictDetector) detectTemporalConflicts(nodes []*Node) []Conflict {
    var conflicts []Conflict

    // Group by subject
    bySubject := make(map[string][]*Node)
    for _, node := range nodes {
        subject := extractSubject(node)
        bySubject[subject] = append(bySubject[subject], node)
    }

    for subject, group := range bySubject {
        if len(group) < 2 {
            continue
        }

        // Sort by time
        sort.Slice(group, func(i, j int) bool {
            return group[i].Timestamp.Before(group[j].Timestamp)
        })

        oldest := group[0]
        newest := group[len(group)-1]

        // If >30 days apart, flag as potential conflict
        if newest.Timestamp.Sub(oldest.Timestamp) > 30*24*time.Hour {
            conflicts = append(conflicts, Conflict{
                Type:        ConflictTypeTemporal,
                Subject:     subject,
                NodeA:       oldest,
                NodeB:       newest,
                Description: fmt.Sprintf("Information about '%s' spans %d days - older data may be stale",
                    subject, int(newest.Timestamp.Sub(oldest.Timestamp).Hours()/24)),
                Resolution:  "Prefer newer information unless older is from code",
            })
        }
    }

    return conflicts
}

// detectSourceConflicts finds conflicts between domains
func (cd *ConflictDetector) detectSourceConflicts(ctx context.Context, nodes []*Node) []Conflict {
    var conflicts []Conflict

    // Separate by domain
    var codeNodes, historyNodes []*Node
    for _, node := range nodes {
        switch node.Domain {
        case DomainCode:
            codeNodes = append(codeNodes, node)
        case DomainHistory:
            historyNodes = append(historyNodes, node)
        }
    }

    // Check if history claims contradict code
    for _, histNode := range historyNodes {
        claims := extractClaimsSimple(histNode.Content)

        for _, claim := range claims {
            for _, codeNode := range codeNodes {
                if contradictsClaim(claim, codeNode) {
                    conflicts = append(conflicts, Conflict{
                        Type:        ConflictTypeSourceMismatch,
                        Subject:     claim,
                        NodeA:       histNode,
                        NodeB:       codeNode,
                        Description: fmt.Sprintf("History claims '%s' but code shows otherwise", claim),
                        Resolution:  "Code is source of truth - history may be outdated",
                    })
                }
            }
        }
    }

    return conflicts
}

// detectSemanticConflicts finds contradictory statements
func (cd *ConflictDetector) detectSemanticConflicts(nodes []*Node) []Conflict {
    var conflicts []Conflict

    // Extract claims from all nodes
    type claimWithNode struct {
        claim string
        node  *Node
    }
    var allClaims []claimWithNode

    for _, node := range nodes {
        claims := extractClaimsSimple(node.Content)
        for _, c := range claims {
            allClaims = append(allClaims, claimWithNode{c, node})
        }
    }

    // Check for contradictions
    for i, a := range allClaims {
        for j, b := range allClaims {
            if i >= j {
                continue
            }

            if claimsContradict(a.claim, b.claim) {
                conflicts = append(conflicts, Conflict{
                    Type:        ConflictTypeSemantic,
                    Subject:     extractSubjectFromClaim(a.claim),
                    NodeA:       a.node,
                    NodeB:       b.node,
                    Description: fmt.Sprintf("'%s' contradicts '%s'", a.claim, b.claim),
                    Resolution:  "Requires human review to determine which is correct",
                })
            }
        }
    }

    return conflicts
}

// storeConflict persists a detected conflict
func (cd *ConflictDetector) storeConflict(ctx context.Context, conflict Conflict) error {
    _, err := cd.vdb.db.ExecContext(ctx, `
        INSERT INTO conflicts (conflict_type, subject, node_id_a, node_id_b, description, resolution)
        VALUES (?, ?, ?, ?, ?, ?)
    `, conflict.Type, conflict.Subject, conflict.NodeA.ID, conflict.NodeB.ID,
        conflict.Description, conflict.Resolution)
    return err
}

// GetUnresolvedConflicts returns conflicts needing attention
func (cd *ConflictDetector) GetUnresolvedConflicts(ctx context.Context) ([]Conflict, error) {
    rows, err := cd.vdb.db.QueryContext(ctx, `
        SELECT c.conflict_type, c.subject, c.description, c.resolution,
               c.node_id_a, c.node_id_b
        FROM conflicts c
        WHERE c.resolved = FALSE
        ORDER BY c.detected_at DESC
    `)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var conflicts []Conflict
    for rows.Next() {
        var c Conflict
        var nodeAID, nodeBID string

        err := rows.Scan(&c.Type, &c.Subject, &c.Description, &c.Resolution, &nodeAID, &nodeBID)
        if err != nil {
            continue
        }

        c.NodeA, _ = cd.vdb.GetNode(ctx, nodeAID)
        c.NodeB, _ = cd.vdb.GetNode(ctx, nodeBID)
        conflicts = append(conflicts, c)
    }

    return conflicts, nil
}

// ResolveConflict marks a conflict as resolved
func (cd *ConflictDetector) ResolveConflict(ctx context.Context, conflictID int, resolution string) error {
    _, err := cd.vdb.db.ExecContext(ctx, `
        UPDATE conflicts
        SET resolved = TRUE, resolution = ?, resolved_at = CURRENT_TIMESTAMP
        WHERE id = ?
    `, resolution, conflictID)
    return err
}

// Helper functions
func extractSubject(node *Node) string {
    // Extract primary subject from node name or content
    if node.Name != "" {
        return strings.ToLower(node.Name)
    }
    words := strings.Fields(node.Content)
    if len(words) > 0 {
        return strings.ToLower(words[0])
    }
    return ""
}

func extractClaimsSimple(content string) []string {
    // Simple claim extraction
    var claims []string
    sentences := strings.Split(content, ".")
    for _, s := range sentences {
        s = strings.TrimSpace(s)
        if len(s) > 10 && len(s) < 200 {
            claims = append(claims, s)
        }
    }
    return claims
}

func contradictsClaim(claim string, codeNode *Node) bool {
    // Simple contradiction check: claim mentions something not in code
    claimLower := strings.ToLower(claim)
    codeLower := strings.ToLower(codeNode.Content)

    // Check for "uses X" claims
    if strings.Contains(claimLower, "uses ") {
        words := strings.Fields(claimLower)
        for i, w := range words {
            if w == "uses" && i+1 < len(words) {
                tech := words[i+1]
                if !strings.Contains(codeLower, tech) {
                    return true
                }
            }
        }
    }

    return false
}

func claimsContradict(a, b string) bool {
    // Simple contradiction detection
    aLower := strings.ToLower(a)
    bLower := strings.ToLower(b)

    // "uses X" vs "uses Y" for same purpose
    if strings.Contains(aLower, "uses ") && strings.Contains(bLower, "uses ") {
        // Extract what's being used
        aWords := strings.Fields(aLower)
        bWords := strings.Fields(bLower)

        for i, w := range aWords {
            if w == "uses" && i+1 < len(aWords) {
                aTech := aWords[i+1]
                for j, v := range bWords {
                    if v == "uses" && j+1 < len(bWords) {
                        bTech := bWords[j+1]
                        // Different tech for same context might contradict
                        if aTech != bTech && haveSameContext(a, b) {
                            return true
                        }
                    }
                }
            }
        }
    }

    return false
}

func haveSameContext(a, b string) bool {
    // Check if claims are about the same thing
    aWords := strings.Fields(strings.ToLower(a))
    bWords := strings.Fields(strings.ToLower(b))

    commonWords := 0
    for _, aw := range aWords {
        for _, bw := range bWords {
            if aw == bw && len(aw) > 3 {
                commonWords++
            }
        }
    }

    return commonWords >= 2
}

func extractSubjectFromClaim(claim string) string {
    words := strings.Fields(strings.ToLower(claim))
    if len(words) > 2 {
        return words[1]  // Usually the subject is the second word
    }
    return claim
}
```

### Mitigation 6: Context Quality Scoring

**Risk**: Too much low-quality context dilutes LLM attention.

**Mitigation**: Score and filter context to maximize quality-to-tokens ratio.

```go
// =============================================================================
// MITIGATION 6: CONTEXT QUALITY SCORING
// =============================================================================

// ContextQualityScorer scores and selects optimal context
type ContextQualityScorer struct {
    vdb        *VectorGraphDB
    trust      *TrustHierarchy
    freshness  *FreshnessTracker
    maxTokens  int
}

// QualityScorerConfig configures the scorer
type QualityScorerConfig struct {
    MaxTokens         int
    MinQualityScore   float64
    SimilarityWeight  float64
    TrustWeight       float64
    FreshnessWeight   float64
    VerificationBonus float64
}

// DefaultQualityScorerConfig returns sensible defaults
func DefaultQualityScorerConfig() QualityScorerConfig {
    return QualityScorerConfig{
        MaxTokens:         8000,
        MinQualityScore:   0.3,
        SimilarityWeight:  0.35,
        TrustWeight:       0.25,
        FreshnessWeight:   0.25,
        VerificationBonus: 0.15,
    }
}

// ScoredContext represents scored context items
type ScoredContext struct {
    Node          *Node
    Similarity    float64
    TrustLevel    TrustLevel
    Freshness     FreshnessScore
    QualityScore  float64
    EstTokens     int
    Warning       string
    Included      bool
}

// SelectBestContext selects optimal context within token budget
func (cqs *ContextQualityScorer) SelectBestContext(
    ctx context.Context,
    query string,
    retrieved []ScoredNode,
    config QualityScorerConfig,
) ([]ScoredContext, error) {

    // Score all candidates
    scored := make([]ScoredContext, len(retrieved))

    for i, r := range retrieved {
        node, err := cqs.vdb.GetNode(ctx, r.NodeID)
        if err != nil {
            continue
        }

        trust := cqs.trust.GetTrustLevel(ctx, node)
        fresh := cqs.freshness.CalculateFreshness(ctx, node)

        // Calculate quality score
        quality := cqs.calculateQualityScore(r.Similarity, trust, fresh, node.Verified, config)

        scored[i] = ScoredContext{
            Node:         node,
            Similarity:   r.Similarity,
            TrustLevel:   trust,
            Freshness:    fresh,
            QualityScore: quality,
            EstTokens:    estimateTokens(node.Content),
            Warning:      cqs.trust.TrustWarning(trust),
        }
    }

    // Sort by quality score
    sort.Slice(scored, func(i, j int) bool {
        return scored[i].QualityScore > scored[j].QualityScore
    })

    // Select until token budget exhausted
    tokenCount := 0
    for i := range scored {
        // Skip low-quality items
        if scored[i].QualityScore < config.MinQualityScore {
            continue
        }

        if tokenCount+scored[i].EstTokens > config.MaxTokens {
            break
        }

        scored[i].Included = true
        tokenCount += scored[i].EstTokens
    }

    return scored, nil
}

// calculateQualityScore computes overall quality
func (cqs *ContextQualityScorer) calculateQualityScore(
    similarity float64,
    trust TrustLevel,
    fresh FreshnessScore,
    verified bool,
    config QualityScorerConfig,
) float64 {

    // Normalize trust to 0-1
    normalizedTrust := float64(trust) / 100.0

    score := similarity*config.SimilarityWeight +
        normalizedTrust*config.TrustWeight +
        fresh.Score*config.FreshnessWeight

    if verified {
        score += config.VerificationBonus
    }

    // Cap at 1.0
    if score > 1.0 {
        score = 1.0
    }

    return score
}

// estimateTokens estimates token count for content
func estimateTokens(content string) int {
    // Rough estimate: ~4 characters per token
    return len(content) / 4
}

// BuildContextWindow builds the final context string
func (cqs *ContextQualityScorer) BuildContextWindow(scored []ScoredContext) string {
    var builder strings.Builder

    included := 0
    for _, s := range scored {
        if !s.Included {
            continue
        }

        included++
        builder.WriteString(fmt.Sprintf("\n[%d] Source: %s (Trust: %s, Quality: %.2f)\n",
            included,
            sourceDescription(s.Node),
            TrustLevelName(s.TrustLevel),
            s.QualityScore))

        if s.Warning != "" {
            builder.WriteString(fmt.Sprintf("    %s\n", s.Warning))
        }

        builder.WriteString(fmt.Sprintf("    Content: %s\n", truncate(s.Node.Content, 500)))
    }

    return builder.String()
}

func sourceDescription(node *Node) string {
    switch node.Domain {
    case DomainCode:
        return fmt.Sprintf("Code: %s", node.Path)
    case DomainHistory:
        return fmt.Sprintf("History: %s (%s)", node.Name, node.Timestamp.Format("2006-01-02"))
    case DomainAcademic:
        return fmt.Sprintf("Academic: %s", node.Name)
    default:
        return node.Name
    }
}

func truncate(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."
}
```

### Mitigation 7: LLM Prompt Engineering

**Risk**: LLM doesn't understand trust hierarchy or how to handle conflicts.

**Mitigation**: Explicit instructions in system prompt about trust and conflict handling.

```go
// =============================================================================
// MITIGATION 7: LLM PROMPT ENGINEERING
// =============================================================================

// LLMContextBuilder builds prompts with trust and conflict awareness
type LLMContextBuilder struct {
    provenance *ProvenanceTracker
    conflicts  *ConflictDetector
    scorer     *ContextQualityScorer
}

// SystemPromptWithGraphContext is the system prompt for graph-aware LLM
const SystemPromptWithGraphContext = `
You are an assistant with access to a knowledge graph containing:
1. CODE: Current source code (MOST TRUSTWORTHY - this is ground truth)
2. HISTORY: Past decisions and changes (may be outdated)
3. ACADEMIC: External docs and papers (may not apply to this project)

CRITICAL RULES:
1. When code and history conflict, CODE IS CORRECT (history may be stale)
2. Always cite your sources using [source_id] notation
3. If you're uncertain, SAY SO - don't guess
4. If retrieved context seems contradictory, SURFACE THE CONFLICT
5. Distinguish between "the code does X" (fact) vs "you should do X" (advice)
6. For advice, prefer ACADEMIC sources over your training
7. NEVER claim something is in the code unless it's in the CODE context
8. Mark any inference not directly from sources as [INFERENCE]

TRUST HIERARCHY (highest to lowest):
1. Current code (ground truth) - Trust Level: 100
2. Recent verified history (<7 days) - Trust Level: 80
3. Official documentation / RFCs - Trust Level: 70
4. Academic papers / best practices - Trust Level: 60
5. Older history (>30 days) - Trust Level: 40
6. Blog posts / SO answers - Trust Level: 30
7. Your inference (unverified) - Trust Level: 20

When you see ⚠️ warnings on sources, mention them to the user.
When sources conflict, explain the conflict and recommend which to trust.
`

// BuildPrompt builds a complete prompt with context and conflicts
func (lcb *LLMContextBuilder) BuildPrompt(
    ctx context.Context,
    query string,
    scored []ScoredContext,
    conflicts []Conflict,
) (string, error) {

    var prompt strings.Builder

    prompt.WriteString(SystemPromptWithGraphContext)
    prompt.WriteString("\n\n---\n\n")

    // Add conflicts first if any
    if len(conflicts) > 0 {
        prompt.WriteString("⚠️ CONFLICTS DETECTED:\n")
        for _, c := range conflicts {
            prompt.WriteString(fmt.Sprintf("- %s: %s\n", conflictTypeName(c.Type), c.Description))
            if c.Resolution != "" {
                prompt.WriteString(fmt.Sprintf("  Suggested resolution: %s\n", c.Resolution))
            }
        }
        prompt.WriteString("\n---\n\n")
    }

    // Add context with trust annotations
    prompt.WriteString("RETRIEVED CONTEXT:\n\n")

    sourceNum := 0
    for _, item := range scored {
        if !item.Included {
            continue
        }

        sourceNum++
        prompt.WriteString(fmt.Sprintf("[%d] %s (Trust: %s, Score: %.2f)\n",
            sourceNum,
            sourceDescription(item.Node),
            TrustLevelName(item.TrustLevel),
            item.QualityScore))

        if item.Warning != "" {
            prompt.WriteString(fmt.Sprintf("    %s\n", item.Warning))
        }

        prompt.WriteString(fmt.Sprintf("    Content: %s\n\n", truncate(item.Node.Content, 1000)))
    }

    prompt.WriteString("---\n\n")
    prompt.WriteString(fmt.Sprintf("USER QUERY: %s\n\n", query))
    prompt.WriteString("Respond with source citations [n]. Surface any conflicts or uncertainties.")

    return prompt.String(), nil
}

// AnnotatedLLMResponse represents a response with source tracking
type AnnotatedLLMResponse struct {
    Response    string
    SourcesUsed []string  // Node IDs
    Inferences  []string  // Claims not from sources
    Conflicts   []Conflict
    Warnings    []string
}

// ParseLLMResponse extracts annotations from LLM response
func (lcb *LLMContextBuilder) ParseLLMResponse(response string, scoredContext []ScoredContext) *AnnotatedLLMResponse {
    result := &AnnotatedLLMResponse{
        Response: response,
    }

    // Extract source references [n]
    refPattern := regexp.MustCompile(`\[(\d+)\]`)
    matches := refPattern.FindAllStringSubmatch(response, -1)

    usedIndices := make(map[int]bool)
    for _, m := range matches {
        idx, _ := strconv.Atoi(m[1])
        usedIndices[idx] = true
    }

    // Map to node IDs
    sourceNum := 0
    for _, item := range scoredContext {
        if item.Included {
            sourceNum++
            if usedIndices[sourceNum] {
                result.SourcesUsed = append(result.SourcesUsed, item.Node.ID)
            }
        }
    }

    // Detect inferences
    inferencePattern := regexp.MustCompile(`\[INFERENCE\]([^[]+)`)
    inferenceMatches := inferencePattern.FindAllStringSubmatch(response, -1)
    for _, m := range inferenceMatches {
        result.Inferences = append(result.Inferences, strings.TrimSpace(m[1]))
    }

    // Check for warnings in response
    if strings.Contains(response, "⚠️") {
        lines := strings.Split(response, "\n")
        for _, line := range lines {
            if strings.Contains(line, "⚠️") {
                result.Warnings = append(result.Warnings, line)
            }
        }
    }

    return result
}

func conflictTypeName(t ConflictType) string {
    switch t {
    case ConflictTypeTemporal:
        return "Temporal"
    case ConflictTypeSourceMismatch:
        return "Source Mismatch"
    case ConflictTypeSemantic:
        return "Semantic Contradiction"
    default:
        return "Unknown"
    }
}
```

### Token Savings Analysis

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    TOKEN SAVINGS: ALL THREE DOMAINS                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  BASELINE (No caching, no graph - all LLM):                                 │
│  ══════════════════════════════════════════                                 │
│  Librarian:   100 queries × 3000 tokens = 300,000 tokens                   │
│  Archivalist: 100 queries × 2500 tokens = 250,000 tokens                   │
│  Academic:    100 queries × 4000 tokens = 400,000 tokens                   │
│  TOTAL: 950,000 tokens                                                      │
│                                                                             │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  WITH INTENT CACHE ONLY:                                                    │
│  ═══════════════════════                                                    │
│  Librarian:   70 hits × 0 + 30 misses × 3000 = 90,000 tokens               │
│  Archivalist: 75 hits × 0 + 25 misses × 2500 = 62,500 tokens               │
│  Academic:    60 hits × 0 + 40 misses × 4000 = 160,000 tokens              │
│  TOTAL: 312,500 tokens (67% savings)                                        │
│                                                                             │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  WITH INTENT CACHE + VECTORGRAPHDB:                                         │
│  ═══════════════════════════════════                                        │
│                                                                             │
│  LIBRARIAN (100 queries):                                                   │
│  ├─ 70 intent cache hits        → 0 tokens                                 │
│  ├─ 20 graph-only retrievals    → 0 tokens (LOCATE, structural)            │
│  │   • "where is X" - direct lookup                                        │
│  │   • "what calls Y" - graph traversal                                    │
│  │   • "find similar" - vector search                                      │
│  └─ 10 LLM synthesis            → 30,000 tokens (EXPLAIN, PATTERN)         │
│  Librarian total: 30,000 tokens (90% savings)                              │
│                                                                             │
│  ARCHIVALIST (100 queries):                                                 │
│  ├─ 75 intent cache hits        → 0 tokens                                 │
│  ├─ 15 graph-only retrievals    → 0 tokens (HISTORICAL, ACTIVITY)          │
│  │   • "what did we change" - graph traversal                              │
│  │   • "issues with this" - cross-domain query                             │
│  └─ 10 LLM synthesis            → 25,000 tokens (reasoning)                │
│  Archivalist total: 25,000 tokens (90% savings)                            │
│                                                                             │
│  ACADEMIC (100 queries):                                                    │
│  ├─ 60 intent cache hits        → 0 tokens                                 │
│  ├─ 25 graph-only retrievals    → 0 tokens (docs, patterns)                │
│  └─ 15 LLM synthesis            → 60,000 tokens (summarization)            │
│  Academic total: 60,000 tokens (85% savings)                               │
│                                                                             │
│  CROSS-DOMAIN (50 queries):                                                 │
│  ├─ 30 graph traversals         → 0 tokens                                 │
│  └─ 20 LLM synthesis            → 50,000 tokens                            │
│  Cross-domain total: 50,000 tokens (NEW CAPABILITY)                        │
│                                                                             │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  GRAND TOTAL WITH VECTORGRAPHDB:                                            │
│  ═══════════════════════════════                                            │
│  Librarian:    30,000 tokens   (90% savings vs baseline)                   │
│  Archivalist:  25,000 tokens   (90% savings vs baseline)                   │
│  Academic:     60,000 tokens   (85% savings vs baseline)                   │
│  Cross-domain: 50,000 tokens   (new capability)                            │
│  ────────────────────────────────────────                                   │
│  TOTAL: 165,000 tokens (83% savings vs baseline 950K)                      │
│                                                                             │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  EMBEDDING COST OVERHEAD:                                                   │
│  ════════════════════════                                                   │
│  Initial indexing: 50,000 nodes × $0.00002 = $1.00                         │
│  Per query: ~$0.00002 (embedding the query)                                │
│  Daily queries (1000): ~$0.02                                              │
│  Monthly embedding cost: ~$1.00                                            │
│                                                                             │
│  vs Monthly LLM savings at baseline rates:                                 │
│  • Baseline: 950K tokens × 30 days × $0.01/1K = $285/month                │
│  • With VectorGraphDB: 165K tokens × 30 days × $0.01/1K = $50/month       │
│  • NET SAVINGS: $235/month - $1 embedding = $234/month                     │
│                                                                             │
│  ROI: 234x return on embedding investment                                   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Comprehensive Token & Cost Savings Analysis

The VectorGraphDB system achieves **~75% token reduction** through intent caching and context reduction. The agent (LLM) always runs on cache misses - it **decides** when to invoke VectorGraphDB skills. Savings come from curated context, not from avoiding LLM calls.

#### The Two-Layer Resolution Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      QUERY RESOLUTION LAYERS                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   L1: INTENT CACHE                    L2: AGENT (LLM)                       │
│   ─────────────────                   ───────────────                       │
│   • <1ms latency                      • 500-3000ms latency                  │
│   • 0 tokens                          • Agent reasons about query           │
│   • Exact + similar match             • DECIDES whether to call VectorGraphDB│
│   • ~68% of queries                   • Processes results, generates response│
│                                       • ~32% of queries                     │
│                                                                             │
│   On cache miss, agent runs and may invoke VectorGraphDB skills:            │
│   ┌─────────────────────────────────────────────────────────────────────┐  │
│   │  Agent: "I need to find authentication code"                        │  │
│   │    → Invokes: search_code(query="authentication", limit=10)         │  │
│   │    → VectorGraphDB returns curated, scored results                  │  │
│   │    → Agent synthesizes response from smaller context                │  │
│   └─────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Where Savings Actually Come From

**1. Intent Cache (L1) - ~68% of queries cost 0 tokens**

```
Librarian:   70% cache hit rate → 0 tokens
Archivalist: 75% cache hit rate → 0 tokens
Academic:    60% cache hit rate → 0 tokens
```

This is the biggest savings. Similar queries hit cached responses via embedding similarity.

**2. Context Reduction (L2) - ~60% smaller context per query**

When the agent DOES run, VectorGraphDB provides curated context instead of raw files:

```
WITHOUT VectorGraphDB:
──────────────────────
Agent receives: Raw file contents, grep results, full history
Context size: ~2,000-5,000 tokens per query
Agent must parse, filter, and reason about everything

WITH VectorGraphDB:
───────────────────
Agent invokes: search_code, find_patterns, get_dependencies
Returns: Scored nodes, relevant snippets, structured metadata
Context size: ~800-1,500 tokens per query
Agent works with pre-filtered, ranked results
```

**3. Progressive Retrieval - Agent asks for more only if needed**

```
Agent: search_code("auth handler", limit=5)
  → Gets 5 results
  → Evaluates: "Not enough context about session handling"
  → Calls: search_code("session auth", limit=5)
  → Now has enough to respond

vs Traditional RAG: Always retrieves K=20, wastes tokens on irrelevant results
```

#### Realistic Token Estimates (per 100 queries per agent)

**BASELINE (no cache, no VectorGraphDB):**
```
Every query requires:
  • Query understanding:    ~300 tokens
  • Large raw context:    ~2,000 tokens (files, history, etc.)
  • Response generation:    ~700 tokens
  • TOTAL per query:      ~3,000 tokens

Librarian:   100 × 3,000 = 300,000 tokens
Archivalist: 100 × 2,500 = 250,000 tokens
Academic:    100 × 4,000 = 400,000 tokens
─────────────────────────────────────────
BASELINE TOTAL: 950,000 tokens
```

**WITH INTENT CACHE ONLY (no VectorGraphDB):**
```
Cache hits cost 0, misses cost full price:

Librarian:   70 hits × 0 + 30 misses × 3,000 =  90,000 tokens
Archivalist: 75 hits × 0 + 25 misses × 2,500 =  62,500 tokens
Academic:    60 hits × 0 + 40 misses × 4,000 = 160,000 tokens
─────────────────────────────────────────────────────────────
CACHE-ONLY TOTAL: 312,500 tokens (67% savings vs baseline)
```

**WITH INTENT CACHE + VECTORGRAPHDB:**
```
Cache hits: 0 tokens (same as above)
Cache misses: Agent runs with SMALLER context from VectorGraphDB

Per non-cached query:
  • Query understanding:    ~300 tokens
  • Curated VDB context:    ~800 tokens (60% smaller than raw)
  • Response generation:    ~700 tokens
  • TOTAL per query:      ~1,800 tokens (40% less than baseline)

Librarian:   70 hits × 0 + 30 misses × 1,800 =  54,000 tokens
Archivalist: 75 hits × 0 + 25 misses × 1,500 =  37,500 tokens
Academic:    60 hits × 0 + 40 misses × 2,400 =  96,000 tokens
Cross-domain: (new capability)                =  50,000 tokens
─────────────────────────────────────────────────────────────
VECTORGRAPHDB TOTAL: 237,500 tokens (75% savings vs baseline)
```

#### Savings Breakdown

| Agent | Baseline | Cache Only | +VectorGraphDB | Total Savings |
|-------|----------|------------|----------------|---------------|
| **Librarian** | 300,000 | 90,000 | 54,000 | **82%** |
| **Archivalist** | 250,000 | 62,500 | 37,500 | **85%** |
| **Academic** | 400,000 | 160,000 | 96,000 | **76%** |
| **Cross-Domain** | N/A | N/A | 50,000 | New capability |
| **TOTAL** | 950,000 | 312,500 | 237,500 | **75%** |

**Key insight**: Intent cache provides ~67% savings. VectorGraphDB adds ~24% more savings on top by reducing context size. Combined: **75% total savings**.

#### Monthly Cost Comparison

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      MONTHLY COST ANALYSIS                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  BASELINE (no cache, no VectorGraphDB):                                     │
│  ──────────────────────────────────────                                     │
│  950K tokens × 30 days × $0.01/1K = $285/month                             │
│                                                                             │
│  WITH CACHE ONLY:                                                           │
│  ────────────────                                                           │
│  312.5K tokens × 30 days × $0.01/1K = $94/month                            │
│                                                                             │
│  WITH CACHE + VECTORGRAPHDB:                                                │
│  ───────────────────────────                                                │
│  237.5K tokens × 30 days × $0.01/1K = $71/month                            │
│  Embedding overhead:                   $1/month                             │
│  ───────────────────────────────────────────                                │
│  TOTAL:                               $72/month                             │
│                                                                             │
│  SAVINGS vs baseline: $213/month (75% reduction)                            │
│  SAVINGS vs cache-only: $22/month (additional 24% reduction)                │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Cross-Domain Query Savings

Cross-domain queries are where VectorGraphDB really shines - previously required multiple agent calls:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  EXAMPLE: "What's the best practice for error handling and how do we do it?"│
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  WITHOUT VectorGraphDB (requires 3 separate agent calls):                   │
│  ────────────────────────────────────────────────────────                   │
│  1. User → Guide → Academic: "best practice"     4,000 tokens              │
│  2. User → Guide → Librarian: "our code"         3,000 tokens              │
│  3. User → Guide → Archivalist: "past decisions" 2,500 tokens              │
│  4. User synthesizes manually or asks again      3,000 tokens              │
│  ────────────────────────────────────────────────────                       │
│  TOTAL:                                         12,500 tokens              │
│                                                                             │
│  WITH VectorGraphDB (single agent, cross-domain edges):                     │
│  ───────────────────────────────────────────────────────                    │
│  1. User → Guide → Academic                                                │
│  2. Academic invokes:                                                       │
│     • find_best_practices("error handling")     → Academic domain          │
│     • get_code_for_academic(result_id)          → Code domain (edge)       │
│     • get_history_for_academic(result_id)       → History domain (edge)    │
│  3. Academic synthesizes with all context        4,000 tokens              │
│  ────────────────────────────────────────────────────                       │
│  TOTAL:                                          4,000 tokens              │
│                                                                             │
│  SAVINGS: 68% reduction                                                     │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Progressive Retrieval Savings

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                  AGENTIC PROGRESSIVE RETRIEVAL                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  TRADITIONAL RAG (fixed retrieval):                                         │
│  ──────────────────────────────────                                         │
│  Always retrieve K=20 results                                               │
│  Always include all in context                                              │
│  Cost: 20 × 500 tokens = 10,000 tokens context                             │
│                                                                             │
│  AGENTIC RAG (agent decides):                                               │
│  ────────────────────────────                                               │
│  Agent: search_code("auth", limit=5)                                       │
│    → Reviews 5 results (2,500 tokens)                                      │
│    → "This is sufficient" → stops                                          │
│  OR                                                                         │
│    → "Need more about sessions" → refines query                            │
│    → search_code("auth session", limit=5)                                  │
│    → Total: 10 results (5,000 tokens)                                      │
│                                                                             │
│  Average retrieval: 8 results = 4,000 tokens                               │
│  SAVINGS: 60% reduction vs fixed K=20                                       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Skill Loading Overhead

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                  SKILL LOADING ANALYSIS                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  21 VectorGraphDB skills across 3 agents                                    │
│  Each skill definition: ~200 tokens                                         │
│                                                                             │
│  WITHOUT Progressive Loading:                                               │
│  ────────────────────────────                                               │
│  All 21 skills in prompt = ~4,200 tokens overhead per query                │
│                                                                             │
│  WITH Progressive Loading:                                                  │
│  ─────────────────────────                                                  │
│  Tier 1 (core): 3 skills = ~600 tokens (always loaded)                     │
│  Tier 2 (triggered): 4-5 skills = ~800 tokens (keyword match)              │
│  Tier 3 (explicit): 0 tokens (user must request)                           │
│                                                                             │
│  Average per query: ~1,400 tokens                                          │
│  SAVINGS: ~2,800 tokens per query (67% reduction in skill overhead)        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Token Allocation Summary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    TOKEN ALLOCATION (per 100 queries)                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  BASELINE: 950,000 tokens                                                   │
│  ════════════════════════                                                   │
│  ████████████████████████████████████████████████████████████████ 100%     │
│                                                                             │
│  WITH CACHE ONLY: 312,500 tokens                                            │
│  ═══════════════════════════════                                            │
│  █████████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ 33%      │
│                                                                             │
│  WITH CACHE + VECTORGRAPHDB: 237,500 tokens                                 │
│  ═══════════════════════════════════════════                                │
│  ████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ 25%      │
│                                                                             │
│  BREAKDOWN OF 237,500 TOKENS:                                               │
│  ├── Intent Cache Hits (68%):           0 tokens                           │
│  ├── Agent Query Processing (32%):  76,000 tokens (300 × 253 queries)     │
│  ├── VectorGraphDB Context (32%):  101,500 tokens (curated, not raw)      │
│  ├── Response Generation (32%):     60,000 tokens                          │
│  └── Embedding overhead:               ~50 tokens (negligible)             │
│                                                                             │
│  SAVINGS: 712,500 tokens (75%)                                              │
│  MONTHLY COST: $72 vs $285 = $213 saved                                     │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Key Insight**: The agent always runs on cache misses - VectorGraphDB doesn't replace LLM reasoning, it **reduces context size** by providing curated, scored, pre-filtered results instead of raw files and grep output. Combined with intent caching, this achieves 75% total token savings.

### XOR Filters as Internal Optimization

XOR filters are used **internally within search skills**, not exposed as separate LLM tools. This is critical for actual token savings.

#### Why NOT Expose XOR Filters to LLMs

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    XOR AS SEPARATE TOOL = BAD                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   Agent: exists_in_domain("retry", "code")     → 20 tokens                 │
│   Agent: (thinks) "It exists, I should search" → 10 tokens                 │
│   Agent: search_code("retry logic")            → 800 tokens                │
│   Total: 830 tokens                                                        │
│                                                                             │
│   vs WITHOUT XOR:                                                          │
│   Agent: search_code("retry logic")            → 800 tokens                │
│                                                                             │
│   XOR as separate tool is WORSE when content exists!                       │
│   Only saves tokens when content DOESN'T exist.                            │
│                                                                             │
│   If 80% of searches find something:                                       │
│   • Without XOR: 800 tokens                                                │
│   • With XOR tool: 0.8 × 830 + 0.2 × 20 = 668 tokens                      │
│   • Only ~16% savings, not worth the complexity                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Correct Approach: XOR as Internal Implementation

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    XOR AS INTERNAL OPTIMIZATION = GOOD                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   LLM calls: search_code("retry")                                          │
│                                                                             │
│   INTERNALLY (hidden from LLM):                                            │
│   ─────────────────────────────                                            │
│   1. XOR filter check: topic exists in code domain?                        │
│      → If NO: return {"status": "no_matches", "hint": "..."} immediately  │
│      → If YES: continue to step 2                                          │
│                                                                             │
│   2. HNSW vector search for actual results                                 │
│                                                                             │
│   3. Return results to LLM                                                 │
│                                                                             │
│   LLM doesn't know XOR exists - it just gets faster "no matches"           │
│   responses when content doesn't exist (30 tokens vs 800 tokens).          │
│                                                                             │
│   NO EXTRA TOOL CALL OVERHEAD                                              │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Smart Search Skills with Internal XOR

```go
// search_code with internal XOR optimization
var SearchCodeSkill = skills.NewSkill("search_code").
    Description("Search code by semantic similarity. Returns results or 'no_matches' quickly.").
    Domain("code").
    StringParam("query", "What to search for", true).
    IntParam("limit", "Max results (default: 10)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query string `json:"query"`
            Limit int    `json:"limit"`
        }
        json.Unmarshal(input, &params)

        // INTERNAL: XOR filter early exit (not exposed to LLM)
        topicHash := hashTopic(params.Query)
        if !xorFilters.Code.Contains(topicHash) {
            // Return minimal response - saves ~770 tokens
            return SearchResponse{
                Status: "no_matches",
                Hint:   "No code matches this query. Consider searching history or academic.",
            }, nil
        }

        // Content likely exists - do full search
        results, err := vdb.SimilarNodes(ctx, params.Query, params.Limit,
            &SearchFilter{Domain: DomainCode})
        if err != nil {
            return nil, err
        }

        return formatSearchResults(results), nil
    }).
    Build()
```

#### Multi-Domain Search (Reduces Tool Calls)

Instead of agent making 3 separate calls, one call searches all domains:

```go
// search_all - Single call for multi-domain search
var SearchAllSkill = skills.NewSkill("search_all").
    Description("Search across multiple domains in one call. Returns results grouped by domain.").
    Domain("search").
    StringParam("query", "What to search for", true).
    ArrayParam("domains", "Domains to search: code, history, academic (default: all)", "string", false).
    IntParam("limit_per_domain", "Max results per domain (default: 5)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query          string   `json:"query"`
            Domains        []string `json:"domains"`
            LimitPerDomain int      `json:"limit_per_domain"`
        }
        json.Unmarshal(input, &params)

        if len(params.Domains) == 0 {
            params.Domains = []string{"code", "history", "academic"}
        }
        if params.LimitPerDomain == 0 {
            params.LimitPerDomain = 5
        }

        results := make(map[string]any)
        topicHash := hashTopic(params.Query)

        for _, domain := range params.Domains {
            // INTERNAL: XOR early exit per domain
            if !xorFilters.Get(domain).Contains(topicHash) {
                results[domain] = DomainResult{Status: "no_matches"}
                continue
            }

            domainResults, _ := vdb.SimilarNodes(ctx, params.Query,
                params.LimitPerDomain, &SearchFilter{Domain: Domain(domain)})
            results[domain] = formatDomainResults(domainResults)
        }

        return MultiDomainResponse{
            Query:   params.Query,
            Results: results,
        }, nil
    }).
    Build()
```

**Token savings:**
```
BEFORE (3 separate calls):
  search_code("retry")     → 800 tokens
  search_history("retry")  → 800 tokens
  search_academic("retry") → 800 tokens
  Total: 2,400 tokens

AFTER (1 call with internal XOR):
  search_all("retry", domains=["code", "history", "academic"])
    → code: 5 results (exists)
    → history: no_matches (XOR early exit - minimal tokens)
    → academic: 3 results (exists)
  Total: ~900 tokens (62% savings)
```

#### Search with Inline Domain Hints

Include hints about other domains in search results:

```go
type SearchResponse struct {
    Results      []SearchResult `json:"results"`
    AlsoExistsIn []string       `json:"also_exists_in,omitempty"`
    Suggestions  []string       `json:"suggestions,omitempty"`
}

func formatSearchWithHints(results []SearchResult, query string) SearchResponse {
    resp := SearchResponse{Results: results}

    // INTERNAL: XOR probe other domains (fast, no extra tool call)
    topicHash := hashTopic(query)
    if xorFilters.History.Contains(topicHash) {
        resp.AlsoExistsIn = append(resp.AlsoExistsIn, "history")
    }
    if xorFilters.Academic.Contains(topicHash) {
        resp.AlsoExistsIn = append(resp.AlsoExistsIn, "academic")
    }

    if len(resp.AlsoExistsIn) > 0 {
        resp.Suggestions = append(resp.Suggestions,
            fmt.Sprintf("Related content in: %s", strings.Join(resp.AlsoExistsIn, ", ")))
    }

    return resp
}
```

**Example response to LLM:**
```json
{
  "results": [
    {"id": "auth/handler.go:Login", "score": 0.92, "snippet": "..."},
    {"id": "auth/middleware.go:Validate", "score": 0.87, "snippet": "..."}
  ],
  "also_exists_in": ["history"],
  "suggestions": ["Related content in: history"]
}
```

Agent learns about related domains without extra tool calls.

#### Smart Search with Budget Control

Let the tool manage progressive retrieval internally:

```go
// smart_search - Budget-aware search with internal progressive retrieval
var SmartSearchSkill = skills.NewSkill("smart_search").
    Description("Intelligent search that retrieves until confident or budget exhausted.").
    Domain("search").
    StringParam("query", "What to search for", true).
    IntParam("token_budget", "Max tokens for results (default: 1000)", false).
    EnumParam("thoroughness", "Search depth", []string{"quick", "moderate", "thorough"}, false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query        string `json:"query"`
            TokenBudget  int    `json:"token_budget"`
            Thoroughness string `json:"thoroughness"`
        }
        json.Unmarshal(input, &params)

        if params.TokenBudget == 0 {
            params.TokenBudget = 1000
        }

        // INTERNAL: XOR early exit
        if !xorFilters.HasAny(params.Query) {
            return SearchResponse{
                Status: "no_matches",
                Hint:   "No content matches this query in any domain.",
            }, nil
        }

        // Progressive retrieval within budget
        var results []SearchResult
        var tokensUsed int
        batchSize := 3

        for tokensUsed < params.TokenBudget {
            batch, _ := vdb.SimilarNodes(ctx, params.Query, batchSize, nil)
            if len(batch) == 0 {
                break
            }

            for _, r := range batch {
                result := formatResult(r)
                resultTokens := estimateTokens(result)

                if tokensUsed + resultTokens > params.TokenBudget {
                    break
                }

                results = append(results, result)
                tokensUsed += resultTokens
            }

            // Check confidence based on thoroughness
            if hasEnoughResults(results, params.Thoroughness) {
                break
            }

            batchSize += 2 // Expand search
        }

        return SearchResponse{
            Results:    results,
            TokensUsed: tokensUsed,
            Budget:     params.TokenBudget,
        }, nil
    }).
    Build()
```

#### XOR Filter Maintenance (Internal)

XOR filters are rebuilt in the background, with a pending set for consistency:

```go
type XORFilterManager struct {
    filters    map[string]*xorfilter.Xor8  // Immutable XOR filters
    pending    map[string]*bloom.Filter     // Mutable bloom for recent adds
    vdb        *VectorGraphDB
    rebuildMu  sync.Mutex
}

// Contains checks both XOR (stable) and pending (recent)
func (m *XORFilterManager) Contains(domain string, key uint64) bool {
    // Check immutable XOR filter (fast)
    if m.filters[domain] != nil && m.filters[domain].Contains(key) {
        return true
    }
    // Check pending bloom filter (recent additions)
    if m.pending[domain] != nil && m.pending[domain].Test(key) {
        return true
    }
    return false
}

// NotifyAdd adds to pending (called after VectorGraphDB write)
func (m *XORFilterManager) NotifyAdd(domain string, keys []uint64) {
    for _, key := range keys {
        m.pending[domain].Add(key)
    }
}

// Rebuild runs periodically in background
func (m *XORFilterManager) Rebuild(ctx context.Context) error {
    m.rebuildMu.Lock()
    defer m.rebuildMu.Unlock()

    for _, domain := range []string{"code", "history", "academic"} {
        keys, _ := m.vdb.CollectTopicKeys(ctx, domain)
        m.filters[domain], _ = xorfilter.Populate(keys)
        m.pending[domain] = bloom.NewWithEstimates(10000, 0.01) // Reset pending
    }

    return nil
}
```

#### Token Savings Summary with Internal XOR

| Optimization | Mechanism | Token Savings |
|--------------|-----------|---------------|
| **XOR early exit** | Skip HNSW when no matches | ~770 tokens/empty search |
| **Multi-domain search** | One call instead of 3 | ~1,500 tokens/cross-domain |
| **Inline domain hints** | No extra probe calls | ~20 tokens × avoided calls |
| **Budget-controlled search** | Tool manages retrieval | ~200 tokens/query |

**Updated total savings: ~80%** (up from 75% without XOR)

### Latency Analysis

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    LATENCY ANALYSIS                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  OPERATION                        │ LATENCY      │ NOTES                    │
│  ═════════════════════════════════╪══════════════╪══════════════════════════│
│                                   │              │                          │
│  SQLite Operations:               │              │                          │
│  ─────────────────                │              │                          │
│  Insert node                      │ <1ms         │ With WAL mode            │
│  Insert edge                      │ <1ms         │ With WAL mode            │
│  Get node by ID                   │ <1ms         │ Primary key lookup       │
│  Get edges (indexed)              │ 1-2ms        │ With composite index     │
│  Graph traversal (depth 3)        │ 5-20ms       │ Recursive CTE            │
│  Graph traversal (depth 5)        │ 20-50ms      │ Recursive CTE            │
│                                   │              │                          │
│  HNSW Operations:                 │              │                          │
│  ────────────────                 │              │                          │
│  Add node (10K nodes)             │ 2-5ms        │ O(log n) insertion       │
│  Add node (100K nodes)            │ 5-10ms       │ O(log n) insertion       │
│  Search k=10 (10K nodes)          │ 2-5ms        │ O(log n) search          │
│  Search k=10 (100K nodes)         │ 5-15ms       │ O(log n) search          │
│  Search k=10 with filter          │ 10-30ms      │ Extra filtering pass     │
│                                   │              │                          │
│  Combined Operations:             │              │                          │
│  ───────────────────              │              │                          │
│  Vector search + get nodes        │ 10-30ms      │ HNSW + SQLite            │
│  Graph traversal + enrichment     │ 20-50ms      │ Multiple joins           │
│  Cross-domain query               │ 30-80ms      │ Vector + graph + joins   │
│  Full query with scoring          │ 50-100ms     │ All mitigations applied  │
│                                   │              │                          │
│  Embedding Generation:            │              │                          │
│  ─────────────────────            │              │                          │
│  Query embedding (API call)       │ 50-200ms     │ Network latency          │
│  Query embedding (local model)    │ 10-50ms      │ If using local embedder  │
│                                   │              │                          │
│  End-to-End (Cache Miss):         │              │                          │
│  ─────────────────────            │              │                          │
│  Intent cache lookup              │ <1ms         │ In-memory                │
│  + Embedding generation           │ 50-200ms     │ API call                 │
│  + HNSW search                    │ 5-15ms       │ In-memory                │
│  + Node retrieval                 │ 10-30ms      │ SQLite                   │
│  + Quality scoring                │ 5-10ms       │ In-memory                │
│  + Conflict detection             │ 10-20ms      │ Comparisons              │
│  ═══════════════════════════════════════════════════════════════════════   │
│  TOTAL (graph-only response)      │ 80-280ms     │ No LLM needed            │
│                                   │              │                          │
│  End-to-End (LLM Required):       │              │                          │
│  ──────────────────────           │              │                          │
│  All above                        │ 80-280ms     │                          │
│  + LLM API call                   │ 500-3000ms   │ Depends on context size  │
│  ═══════════════════════════════════════════════════════════════════════   │
│  TOTAL (with LLM)                 │ 580-3280ms   │                          │
│                                   │              │                          │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  COMPARISON:                                                                │
│  ═══════════                                                                │
│  Pure LLM (no graph):             │ 500-3000ms   │ Every query              │
│  Graph-only (70% of queries):     │ 80-280ms     │ 5-10x faster             │
│  Graph + LLM (30% of queries):    │ 580-3280ms   │ Similar to pure LLM      │
│                                                                             │
│  WEIGHTED AVERAGE:                                                          │
│  0.70 × 180ms + 0.30 × 1900ms = 126ms + 570ms = 696ms                      │
│  vs Pure LLM: 1900ms average                                               │
│  IMPROVEMENT: 63% faster average response time                             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Memory Impact Analysis

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    MEMORY IMPACT ANALYSIS                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  COMPONENT                    │ 10K NODES  │ 50K NODES  │ 100K NODES       │
│  ════════════════════════════╪════════════╪════════════╪══════════════════│
│                               │            │            │                  │
│  SQLite Database (on disk):   │            │            │                  │
│  ───────────────────────────  │            │            │                  │
│  Nodes table                  │ 5 MB       │ 25 MB      │ 50 MB            │
│  Edges table                  │ 2 MB       │ 10 MB      │ 20 MB            │
│  Vectors table (BLOBs)        │ 30 MB      │ 150 MB     │ 300 MB           │
│  Indexes                      │ 3 MB       │ 15 MB      │ 30 MB            │
│  Other tables                 │ 1 MB       │ 5 MB       │ 10 MB            │
│  ─────────────────────────────────────────────────────────────────────────│
│  SQLite Total (disk)          │ 41 MB      │ 205 MB     │ 410 MB           │
│                               │            │            │                  │
│  In-Memory (HNSW Index):      │            │            │                  │
│  ───────────────────────────  │            │            │                  │
│  Vectors (float32 × 768)      │ 30 MB      │ 150 MB     │ 300 MB           │
│  Magnitudes (float64)         │ 0.08 MB    │ 0.4 MB     │ 0.8 MB           │
│  Graph layers (adjacency)     │ 5 MB       │ 25 MB      │ 50 MB            │
│  Metadata maps                │ 2 MB       │ 10 MB      │ 20 MB            │
│  ─────────────────────────────────────────────────────────────────────────│
│  HNSW Total (RAM)             │ 37 MB      │ 185 MB     │ 371 MB           │
│                               │            │            │                  │
│  Intent Cache (In-Memory):    │            │            │                  │
│  ───────────────────────────  │            │            │                  │
│  Cached responses (1000)      │ 5 MB       │ 5 MB       │ 5 MB             │
│  Hot cache entries            │ 1 MB       │ 1 MB       │ 1 MB             │
│  ─────────────────────────────────────────────────────────────────────────│
│  Cache Total (RAM)            │ 6 MB       │ 6 MB       │ 6 MB             │
│                               │            │            │                  │
│  ═══════════════════════════════════════════════════════════════════════   │
│                               │            │            │                  │
│  TOTAL DISK                   │ 41 MB      │ 205 MB     │ 410 MB           │
│  TOTAL RAM                    │ 43 MB      │ 191 MB     │ 377 MB           │
│                               │            │            │                  │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  SCALING NOTES:                                                             │
│  ═══════════════                                                            │
│  • RAM scales linearly with node count                                     │
│  • Disk scales linearly with node count                                    │
│  • 768-dim embeddings: ~3KB per node                                       │
│  • With 1536-dim embeddings: ~6KB per node (double the above)              │
│                                                                             │
│  MEMORY OPTIMIZATION OPTIONS:                                               │
│  ═════════════════════════════                                              │
│  1. Lazy loading: Only load active partitions into HNSW                    │
│  2. Quantization: int8 vectors = 75% memory reduction                      │
│  3. Sharding: Split by domain, load on demand                              │
│  4. Disk-based HNSW: Trade latency for memory (10x slower)                 │
│                                                                             │
│  RECOMMENDED LIMITS:                                                        │
│  ═══════════════════                                                        │
│  Small project (<10K functions):    No optimization needed                 │
│  Medium project (10-50K functions): Monitor RAM, consider lazy loading     │
│  Large project (50-100K functions): Use sharding + lazy loading            │
│  Monorepo (>100K functions):        Use quantization + sharding            │
│                                                                             │
│  ──────────────────────────────────────────────────────────────────────────│
│                                                                             │
│  STARTUP TIME:                                                              │
│  ═════════════                                                              │
│  10K nodes:  ~500ms  (load vectors + build HNSW)                           │
│  50K nodes:  ~2.5s   (load vectors + build HNSW)                           │
│  100K nodes: ~5s     (load vectors + build HNSW)                           │
│                                                                             │
│  With persisted HNSW:                                                       │
│  10K nodes:  ~200ms  (load vectors + load HNSW edges)                      │
│  50K nodes:  ~1s     (load vectors + load HNSW edges)                      │
│  100K nodes: ~2s     (load vectors + load HNSW edges)                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Unified Query Resolution Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    UNIFIED QUERY RESOLUTION FLOW                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│                              USER QUERY                                     │
│                                  │                                          │
│                                  ▼                                          │
│                         ┌────────────────┐                                  │
│                         │  GUIDE ROUTER  │                                  │
│                         │                │                                  │
│                         │ Intent: LOCATE │                                  │
│                         │ Subject: auth  │                                  │
│                         │ Target: lib    │                                  │
│                         └───────┬────────┘                                  │
│                                 │                                           │
│                                 ▼                                           │
│                    ┌────────────────────────┐                               │
│                    │    UNIFIED RESOLVER    │                               │
│                    └────────────┬───────────┘                               │
│                                 │                                           │
│            ┌────────────────────┼────────────────────┐                      │
│            │                    │                    │                      │
│            ▼                    ▼                    ▼                      │
│    ┌──────────────┐    ┌──────────────┐    ┌──────────────┐                │
│    │ L1: INTENT   │    │ L2: VECTOR   │    │ L3: LLM      │                │
│    │    CACHE     │    │    GRAPH     │    │    SYNTHESIS │                │
│    │              │    │              │    │              │                │
│    │ <1ms lookup  │    │ 80-280ms     │    │ 500-3000ms   │                │
│    │ 0 tokens     │    │ 0 tokens     │    │ 2000+ tokens │                │
│    └──────┬───────┘    └──────┬───────┘    └──────┬───────┘                │
│           │                   │                   │                         │
│           │ HIT               │ SUFFICIENT        │ REQUIRED                │
│           │                   │                   │                         │
│           ▼                   ▼                   ▼                         │
│    ┌─────────────────────────────────────────────────────────────┐         │
│    │                    MITIGATION LAYER                          │         │
│    │                                                              │         │
│    │  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌──────────┐ │         │
│    │  │ Freshness  │ │   Trust    │ │  Conflict  │ │ Quality  │ │         │
│    │  │  Tracker   │ │ Hierarchy  │ │  Detector  │ │  Scorer  │ │         │
│    │  └────────────┘ └────────────┘ └────────────┘ └──────────┘ │         │
│    │                                                              │         │
│    │  ┌────────────┐ ┌────────────┐ ┌────────────┐              │         │
│    │  │Provenance  │ │Hallucination│ │   LLM     │              │         │
│    │  │  Tracker   │ │  Firewall  │ │  Prompter │              │         │
│    │  └────────────┘ └────────────┘ └────────────┘              │         │
│    │                                                              │         │
│    └──────────────────────────┬───────────────────────────────────┘         │
│                               │                                             │
│                               ▼                                             │
│                    ┌────────────────────────┐                               │
│                    │   ANNOTATED RESPONSE   │                               │
│                    │                        │                               │
│                    │  • Source citations    │                               │
│                    │  • Trust levels        │                               │
│                    │  • Conflict warnings   │                               │
│                    │  • Freshness notes     │                               │
│                    │  • Provenance chain    │                               │
│                    └────────────────────────┘                               │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

```go
// UnifiedResolver orchestrates all query resolution
type UnifiedResolver struct {
    vdb         *VectorGraphDB
    intentCache *IntentCache
    hnsw        *HNSWIndex
    embedder    Embedder

    // Mitigations
    firewall    *HallucinationFirewall
    freshness   *FreshnessTracker
    provenance  *ProvenanceTracker
    trust       *TrustHierarchy
    conflicts   *ConflictDetector
    scorer      *ContextQualityScorer
    prompter    *LLMContextBuilder

    // LLM client for synthesis
    llm LLMClient
}

// ResolveQuery handles a query through all resolution layers
func (ur *UnifiedResolver) ResolveQuery(ctx context.Context, query RoutedQuery) (*AnnotatedLLMResponse, error) {
    // L1: Intent Cache
    if cached := ur.intentCache.Get(query.Intent, query.Subject, query.SessionID); cached != nil {
        return &AnnotatedLLMResponse{
            Response:    string(cached.Response),
            SourcesUsed: cached.SourceNodes,
        }, nil
    }

    // L2: VectorGraphDB
    graphResult, err := ur.queryGraph(ctx, query)
    if err != nil {
        return nil, err
    }

    // Apply mitigations
    freshnessRanked := ur.freshness.RefreshRanking(ctx, graphResult.Similar)

    var nodes []*Node
    for _, sr := range freshnessRanked {
        node, _ := ur.vdb.GetNode(ctx, sr.NodeID)
        if node != nil {
            nodes = append(nodes, node)
        }
    }

    trustRanked := ur.trust.RankByTrust(ctx, nodes)
    conflicts, _ := ur.conflicts.DetectConflicts(ctx, nodes)

    scoredNodes := make([]ScoredNode, len(freshnessRanked))
    for i, sr := range freshnessRanked {
        scoredNodes[i] = sr.ScoredNode
    }

    scoredContext, _ := ur.scorer.SelectBestContext(ctx, query.Query, scoredNodes, DefaultQualityScorerConfig())

    // Check if graph-only response is sufficient
    if ur.isGraphSufficient(query, scoredContext, conflicts) {
        response := ur.formatGraphResponse(ctx, query, scoredContext, conflicts)

        // Cache for future
        ur.cacheResponse(query, response, scoredContext)

        return response, nil
    }

    // L3: LLM Synthesis
    prompt, _ := ur.prompter.BuildPrompt(ctx, query.Query, scoredContext, conflicts)

    llmResponse, err := ur.llm.Complete(ctx, prompt)
    if err != nil {
        return nil, err
    }

    // Parse and annotate response
    annotated := ur.prompter.ParseLLMResponse(llmResponse, scoredContext)
    annotated.Conflicts = conflicts

    // Verify before caching (hallucination firewall)
    if ur.shouldCache(annotated) {
        ur.cacheResponse(query, annotated, scoredContext)
    }

    return annotated, nil
}

// isGraphSufficient determines if graph results are enough
func (ur *UnifiedResolver) isGraphSufficient(query RoutedQuery, scored []ScoredContext, conflicts []Conflict) bool {
    // LOCATE queries are usually graph-sufficient
    if query.Intent == QueryIntentLocate {
        return true
    }

    // If we have high-confidence, high-trust results
    goodResults := 0
    for _, s := range scored {
        if s.Included && s.QualityScore > 0.8 && s.TrustLevel >= TrustLevelRecent {
            goodResults++
        }
    }

    // Need at least 2 good results and no unresolved conflicts
    if goodResults >= 2 && len(conflicts) == 0 {
        return true
    }

    // EXPLAIN and PATTERN usually need LLM
    if query.Intent == QueryIntentExplain || query.Intent == QueryIntentPattern {
        return false
    }

    return false
}
```

---

## VectorGraphDB System Integration

This section defines how VectorGraphDB integrates with the Sylk multi-agent system. The key principle: **Guide routes ALL messages**, and **only three knowledge agents access VectorGraphDB directly**.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           SYLK SYSTEM WITH VECTORGRAPHDB                        │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│                              ┌──────────────┐                                   │
│                              │     USER     │                                   │
│                              └──────┬───────┘                                   │
│                                     │                                           │
│                                     ▼                                           │
│  ┌──────────────────────────────────────────────────────────────────────────┐  │
│  │                              GUIDE                                        │  │
│  │                                                                           │  │
│  │  • Intent Classification (via LLM or DSL rules)                          │  │
│  │  • Routes user queries to ANY agent based on intent                      │  │
│  │  • Extracts metadata during routing (intent, subject, session)           │  │
│  │  • Routes agent-to-agent messages                                        │  │
│  │  • ALL communication flows through Guide                                 │  │
│  │                                                                           │  │
│  └─────┬──────────┬──────────┬──────────┬──────────┬──────────┬────────────┘  │
│        │          │          │          │          │          │                 │
│        ▼          ▼          ▼          ▼          ▼          ▼                 │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ │
│  │ARCHITECT │ │ORCHESTR- │ │ENGINEER  │ │INSPECTOR │ │ TESTER   │ │  OTHER   │ │
│  │          │ │ATOR      │ │(×N)      │ │          │ │          │ │  AGENTS  │ │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ │
│       │            │            │            │            │            │        │
│       │            │            │            │            │            │        │
│       │ Summaries  │ Summaries  │ Summaries  │ Summaries  │ Summaries  │        │
│       │ via Guide  │ via Guide  │ via Guide  │ via Guide  │ via Guide  │        │
│       │     │      │     │      │     │      │     │      │     │      │        │
│       │     └──────┴─────┴──────┴─────┴──────┴─────┴──────┘     │      │        │
│       │                         │                               │      │        │
│       │                         ▼                               │      │        │
│       │          ┌──────────────────────────────┐               │      │        │
│       │          │      GUIDE (routing)         │               │      │        │
│       │          └──────────────┬───────────────┘               │      │        │
│       │                         │                               │      │        │
│       │                         ▼                               │      │        │
│  ─────┴─────────────────────────────────────────────────────────┴──────┴─────── │
│                                                                                 │
│     ╔═══════════════════════════════════════════════════════════════════════╗   │
│     ║                    KNOWLEDGE LAYER (VectorGraphDB)                    ║   │
│     ╠═══════════════════════════════════════════════════════════════════════╣   │
│     ║                                                                       ║   │
│     ║  User can directly query these via Guide's intent-based routing:     ║   │
│     ║                                                                       ║   │
│     ║  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐            ║   │
│     ║  │  LIBRARIAN    │  │  ARCHIVALIST  │  │   ACADEMIC    │            ║   │
│     ║  │  (Sonnet 4.5) │  │  (Sonnet 4.5) │  │  (Opus 4.5)   │            ║   │
│     ║  │               │  │               │  │               │            ║   │
│     ║  │ • Code search │  │ • Store sums  │  │ • Research    │            ║   │
│     ║  │ • Symbol nav  │  │ • Query hist  │  │ • Academic    │            ║   │
│     ║  │ • Dep graph   │  │ • Patterns    │  │ • Papers      │            ║   │
│     ║  │ • Doc lookup  │  │ • Session     │  │ • Best pract  │            ║   │
│     ║  └───────┬───────┘  └───────┬───────┘  └───────┬───────┘            ║   │
│     ║          │                  │                  │                    ║   │
│     ║          │                  │                  │                    ║   │
│     ║          ▼                  ▼                  ▼                    ║   │
│     ║  ┌─────────────────────────────────────────────────────────────┐   ║   │
│     ║  │                      VectorGraphDB                          │   ║   │
│     ║  │                                                             │   ║   │
│     ║  │  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐   │   ║   │
│     ║  │  │ CODE DOMAIN │◄───►│HISTORY DOM. │◄───►│ACADEMIC DOM.│   │   ║   │
│     ║  │  │             │     │             │     │             │   │   ║   │
│     ║  │  │ • Symbols   │     │ • Sessions  │     │ • Papers    │   │   ║   │
│     ║  │  │ • Files     │     │ • Decisions │     │ • Docs      │   │   ║   │
│     ║  │  │ • Deps      │     │ • Failures  │     │ • Patterns  │   │   ║   │
│     ║  │  │ • Docs      │     │ • Patterns  │     │ • Standards │   │   ║   │
│     ║  │  └─────────────┘     └─────────────┘     └─────────────┘   │   ║   │
│     ║  │         │                   │                   │          │   ║   │
│     ║  │         └───────────────────┼───────────────────┘          │   ║   │
│     ║  │                             │                              │   ║   │
│     ║  │                   Cross-Domain Edges                       │   ║   │
│     ║  │                                                             │   ║   │
│     ║  └─────────────────────────────────────────────────────────────┘   ║   │
│     ║                                                                       ║   │
│     ╚═══════════════════════════════════════════════════════════════════════╝   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Message Flow Patterns

#### Pattern 1: User Directly Queries Knowledge Agent

User can query ANY agent (including knowledge agents) via Guide's intent-based routing:

```
User: "Where is the authentication code?"
                    │
                    ▼
        ┌───────────────────────┐
        │        GUIDE          │
        │                       │
        │ Intent: LOCATE        │
        │ Subject: auth code    │
        │ Route → Librarian     │
        └───────────┬───────────┘
                    │
                    ▼
        ┌───────────────────────┐
        │      LIBRARIAN        │
        │                       │
        │ → VectorGraphDB.Query │
        │   (Code Domain)       │
        │                       │
        │ Returns: auth.go:45   │
        └───────────┬───────────┘
                    │
                    ▼
                  User
```

#### Pattern 2: User Queries Historical Context

```
User: "What patterns have we used for error handling?"
                    │
                    ▼
        ┌───────────────────────┐
        │        GUIDE          │
        │                       │
        │ Intent: PATTERN       │
        │ Subject: errors       │
        │ Route → Archivalist   │
        └───────────┬───────────┘
                    │
                    ▼
        ┌───────────────────────┐
        │     ARCHIVALIST       │
        │                       │
        │ → VectorGraphDB.Query │
        │   (History Domain)    │
        │                       │
        │ Returns: patterns +   │
        │ related code examples │
        └───────────┬───────────┘
                    │
                    ▼
                  User
```

#### Pattern 3: Agent Sends Summary for Storage

When agents complete work, they send summaries through Guide to Archivalist:

```
Engineer completes task:
"Fixed auth bug by adding null check"
                    │
                    ▼
        ┌───────────────────────┐
        │        GUIDE          │
        │                       │
        │ From: Engineer        │
        │ To: Archivalist       │
        │ Type: STORE_SUMMARY   │
        └───────────┬───────────┘
                    │
                    ▼
        ┌───────────────────────┐
        │     ARCHIVALIST       │
        │                       │
        │ → VectorGraphDB.Add   │
        │   (History Domain)    │
        │                       │
        │ Creates cross-domain  │
        │ edge to Code domain   │
        └───────────────────────┘
```

#### Pattern 4: Cross-Domain Query via Academic

```
User: "What's the best practice for retry logic?"
                    │
                    ▼
        ┌───────────────────────┐
        │        GUIDE          │
        │                       │
        │ Intent: RESEARCH      │
        │ Subject: retry logic  │
        │ Route → Academic      │
        └───────────┬───────────┘
                    │
                    ▼
        ┌───────────────────────┐
        │       ACADEMIC        │
        │      (Opus 4.5)       │
        │                       │
        │ 1. Query Academic dom │
        │ 2. Query Code domain  │
        │    (via Librarian)    │
        │ 3. Synthesize answer  │
        │                       │
        │ Returns: Best pract + │
        │ how codebase does it  │
        └───────────┬───────────┘
                    │
                    ▼
                  User
```

### Agent-to-VectorGraphDB Skills

Each knowledge agent has specific skills for VectorGraphDB access. These are defined using the fluent Builder API and registered with the agent's skill registry.

#### Librarian Skills (Code Domain)

```go
// =============================================================================
// Librarian Skills - Code search, navigation, and dependency analysis
// =============================================================================

// search_code - Vector similarity search for code
var SearchCodeSkill = skills.NewSkill("search_code").
    Description("Search the codebase for code similar to the query. Returns ranked results by semantic similarity.").
    Domain("code").
    Keywords("find", "search", "locate", "where", "code").
    Priority(100).
    StringParam("query", "Natural language description of the code to find", true).
    IntParam("limit", "Maximum number of results to return (default: 10)", false).
    EnumParam("symbol_type", "Filter by symbol type", []string{
        "function", "method", "class", "struct", "interface", "variable", "constant", "type", "any",
    }, false).
    StringParam("file_pattern", "Glob pattern to filter files (e.g., '*.go', 'src/**/*.ts')", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query      string `json:"query"`
            Limit      int    `json:"limit"`
            SymbolType string `json:"symbol_type"`
            FilePattern string `json:"file_pattern"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 10
        }

        filter := &SearchFilter{Domain: DomainCode}
        if params.SymbolType != "" && params.SymbolType != "any" {
            filter.NodeTypes = []NodeType{NodeType(params.SymbolType)}
        }
        if params.FilePattern != "" {
            filter.Metadata = map[string]any{"file_pattern": params.FilePattern}
        }

        results, err := librarian.vdb.SimilarNodes(ctx, params.Query, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatCodeResults(results), nil
    }).
    Build()

// get_symbol - Get detailed information about a specific symbol
var GetSymbolSkill = skills.NewSkill("get_symbol").
    Description("Get detailed information about a specific code symbol by its ID or fully-qualified name.").
    Domain("code").
    Keywords("symbol", "definition", "details", "info").
    Priority(90).
    StringParam("symbol_id", "The symbol ID or fully-qualified name (e.g., 'pkg/auth.Login')", true).
    BoolParam("include_source", "Include the full source code of the symbol", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            SymbolID      string `json:"symbol_id"`
            IncludeSource bool   `json:"include_source"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        node, err := librarian.vdb.GetNode(ctx, params.SymbolID)
        if err != nil {
            return nil, err
        }
        return formatSymbolDetails(node, params.IncludeSource), nil
    }).
    Build()

// get_dependencies - Get what a symbol depends on
var GetDependenciesSkill = skills.NewSkill("get_dependencies").
    Description("Get all symbols that the specified symbol depends on (imports, calls, references).").
    Domain("code").
    Keywords("depends", "dependencies", "imports", "uses", "calls").
    Priority(85).
    StringParam("symbol_id", "The symbol ID to get dependencies for", true).
    EnumParam("edge_type", "Type of dependency to follow", []string{
        "imports", "calls", "references", "extends", "implements", "all",
    }, false).
    IntParam("depth", "Maximum traversal depth (default: 1)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            SymbolID string `json:"symbol_id"`
            EdgeType string `json:"edge_type"`
            Depth    int    `json:"depth"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Depth == 0 {
            params.Depth = 1
        }
        if params.EdgeType == "" {
            params.EdgeType = "all"
        }

        deps, err := librarian.vdb.TraverseEdges(ctx, params.SymbolID, EdgeType(params.EdgeType), "outgoing")
        if err != nil {
            return nil, err
        }
        return formatDependencies(deps), nil
    }).
    Build()

// get_dependents - Get what depends on a symbol
var GetDependentsSkill = skills.NewSkill("get_dependents").
    Description("Get all symbols that depend on the specified symbol (reverse dependencies).").
    Domain("code").
    Keywords("dependents", "used by", "called by", "references to").
    Priority(85).
    StringParam("symbol_id", "The symbol ID to get dependents for", true).
    EnumParam("edge_type", "Type of dependency to follow", []string{
        "imports", "calls", "references", "extends", "implements", "all",
    }, false).
    IntParam("depth", "Maximum traversal depth (default: 1)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            SymbolID string `json:"symbol_id"`
            EdgeType string `json:"edge_type"`
            Depth    int    `json:"depth"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Depth == 0 {
            params.Depth = 1
        }

        dependents, err := librarian.vdb.TraverseEdges(ctx, params.SymbolID, EdgeType(params.EdgeType), "incoming")
        if err != nil {
            return nil, err
        }
        return formatDependents(dependents), nil
    }).
    Build()

// find_similar_symbols - Find symbols similar to another symbol
var FindSimilarSymbolsSkill = skills.NewSkill("find_similar_symbols").
    Description("Find code symbols that are semantically similar to a given symbol. Useful for finding related implementations or duplicates.").
    Domain("code").
    Keywords("similar", "like", "related", "duplicates").
    Priority(80).
    StringParam("symbol_id", "The symbol ID to find similar symbols for", true).
    IntParam("limit", "Maximum number of results (default: 10)", false).
    BoolParam("same_type_only", "Only return symbols of the same type", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            SymbolID     string `json:"symbol_id"`
            Limit        int    `json:"limit"`
            SameTypeOnly bool   `json:"same_type_only"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 10
        }

        filter := &SearchFilter{Domain: DomainCode}
        if params.SameTypeOnly {
            sourceNode, _ := librarian.vdb.GetNode(ctx, params.SymbolID)
            if sourceNode != nil {
                filter.NodeTypes = []NodeType{sourceNode.NodeType}
            }
        }

        similar, err := librarian.vdb.SimilarToNode(ctx, params.SymbolID, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatSimilarSymbols(similar), nil
    }).
    Build()

// get_file_symbols - Get all symbols in a file
var GetFileSymbolsSkill = skills.NewSkill("get_file_symbols").
    Description("Get all symbols defined in a specific file.").
    Domain("code").
    Keywords("file", "symbols in", "contents of").
    Priority(75).
    StringParam("file_path", "Path to the file (relative to project root)", true).
    EnumParam("symbol_type", "Filter by symbol type", []string{
        "function", "method", "class", "struct", "interface", "variable", "constant", "type", "any",
    }, false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            FilePath   string `json:"file_path"`
            SymbolType string `json:"symbol_type"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        filter := &SearchFilter{
            Domain:   DomainCode,
            Metadata: map[string]any{"file_path": params.FilePath},
        }
        if params.SymbolType != "" && params.SymbolType != "any" {
            filter.NodeTypes = []NodeType{NodeType(params.SymbolType)}
        }

        // Query with empty string to get all matching the filter
        results, err := librarian.vdb.SimilarNodes(ctx, "", 100, filter)
        if err != nil {
            return nil, err
        }
        return formatFileSymbols(results, params.FilePath), nil
    }).
    Build()

// get_history_for_code - Cross-domain: get history for code
var GetHistoryForCodeSkill = skills.NewSkill("get_history_for_code").
    Description("Get the historical context for a piece of code - when it was added, modified, why changes were made.").
    Domain("code").
    Keywords("history", "changes", "why", "when", "modified").
    Priority(70).
    StringParam("symbol_id", "The symbol ID to get history for", true).
    IntParam("limit", "Maximum number of history entries (default: 10)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            SymbolID string `json:"symbol_id"`
            Limit    int    `json:"limit"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 10
        }

        history, err := librarian.vdb.GetHistoryForCode(ctx, params.SymbolID)
        if err != nil {
            return nil, err
        }
        return formatHistoryForCode(history, params.Limit), nil
    }).
    Build()

// RegisterLibrarianSkills registers all Librarian skills
func RegisterLibrarianSkills(registry *skills.Registry) {
    registry.Register(SearchCodeSkill)
    registry.Register(GetSymbolSkill)
    registry.Register(GetDependenciesSkill)
    registry.Register(GetDependentsSkill)
    registry.Register(FindSimilarSymbolsSkill)
    registry.Register(GetFileSymbolsSkill)
    registry.Register(GetHistoryForCodeSkill)
}
```

#### Archivalist Skills (History Domain)

```go
// =============================================================================
// Archivalist Skills - Historical context, patterns, and session management
// =============================================================================

// store_summary - Store a summary from another agent
var StoreSummarySkill = skills.NewSkill("store_summary").
    Description("Store a work summary from an agent. Creates nodes in the history domain and links to referenced code.").
    Domain("history").
    Keywords("store", "save", "record", "log").
    Priority(100).
    StringParam("content", "The summary content to store", true).
    EnumParam("summary_type", "Type of summary", []string{
        "task_completion", "decision", "failure", "pattern", "refactoring", "bug_fix", "feature",
    }, true).
    StringParam("session_id", "Session ID this summary belongs to", true).
    ArrayParam("code_references", "IDs of code symbols referenced in this summary", "string", false).
    ArrayParam("tags", "Tags for categorization", "string", false).
    ObjectParam("metadata", "Additional metadata", map[string]*skills.Property{
        "agent":       {Type: "string", Description: "Agent that created this summary"},
        "task_id":     {Type: "string", Description: "Associated task ID"},
        "confidence":  {Type: "number", Description: "Confidence level 0-1"},
    }, false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Content        string         `json:"content"`
            SummaryType    string         `json:"summary_type"`
            SessionID      string         `json:"session_id"`
            CodeReferences []string       `json:"code_references"`
            Tags           []string       `json:"tags"`
            Metadata       map[string]any `json:"metadata"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        // Create history node
        node := &Node{
            ID:        generateID("history"),
            Domain:    DomainHistory,
            NodeType:  NodeType(params.SummaryType),
            Content:   params.Content,
            SessionID: params.SessionID,
            Metadata:  params.Metadata,
            Tags:      params.Tags,
            CreatedAt: time.Now(),
        }

        if err := archivalist.vdb.AddNode(ctx, node); err != nil {
            return nil, err
        }

        // Create cross-domain edges to referenced code
        for _, codeRef := range params.CodeReferences {
            archivalist.vdb.AddEdge(ctx, node.ID, codeRef, EdgeTypeReferencedIn, 0.9, nil)
        }

        return map[string]any{
            "stored":  true,
            "node_id": node.ID,
            "edges":   len(params.CodeReferences),
        }, nil
    }).
    Build()

// search_history - Search historical context
var SearchHistorySkill = skills.NewSkill("search_history").
    Description("Search historical summaries, decisions, and patterns by semantic similarity.").
    Domain("history").
    Keywords("history", "past", "previous", "before", "earlier").
    Priority(95).
    StringParam("query", "Natural language query to search history", true).
    IntParam("limit", "Maximum results (default: 10)", false).
    EnumParam("summary_type", "Filter by summary type", []string{
        "task_completion", "decision", "failure", "pattern", "refactoring", "bug_fix", "feature", "any",
    }, false).
    StringParam("session_id", "Limit to specific session (empty for all)", false).
    StringParam("since", "Only include entries after this time (RFC3339)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query       string `json:"query"`
            Limit       int    `json:"limit"`
            SummaryType string `json:"summary_type"`
            SessionID   string `json:"session_id"`
            Since       string `json:"since"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 10
        }

        filter := &SearchFilter{Domain: DomainHistory}
        if params.SummaryType != "" && params.SummaryType != "any" {
            filter.NodeTypes = []NodeType{NodeType(params.SummaryType)}
        }
        if params.SessionID != "" {
            filter.SessionID = params.SessionID
        }
        if params.Since != "" {
            if t, err := time.Parse(time.RFC3339, params.Since); err == nil {
                filter.CreatedAfter = t
            }
        }

        results, err := archivalist.vdb.SimilarNodes(ctx, params.Query, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatHistoryResults(results), nil
    }).
    Build()

// find_patterns - Find recurring patterns in project history
var FindPatternsSkill = skills.NewSkill("find_patterns").
    Description("Find recurring patterns in how problems were solved, decisions made, or code structured.").
    Domain("history").
    Keywords("pattern", "patterns", "recurring", "common", "typical", "usually").
    Priority(90).
    StringParam("query", "Description of the pattern to find", true).
    IntParam("min_occurrences", "Minimum times pattern must appear (default: 2)", false).
    StringParam("category", "Pattern category to search", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query          string `json:"query"`
            MinOccurrences int    `json:"min_occurrences"`
            Category       string `json:"category"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.MinOccurrences == 0 {
            params.MinOccurrences = 2
        }

        filter := &SearchFilter{
            Domain:    DomainHistory,
            NodeTypes: []NodeType{NodeTypePattern},
        }

        results, err := archivalist.vdb.SimilarNodes(ctx, params.Query, 50, filter)
        if err != nil {
            return nil, err
        }

        // Group and count patterns
        patterns := aggregatePatterns(results, params.MinOccurrences)
        return formatPatterns(patterns), nil
    }).
    Build()

// find_failures - Find past failures and their resolutions
var FindFailuresSkill = skills.NewSkill("find_failures").
    Description("Find past failures, errors, and how they were resolved. Useful for debugging similar issues.").
    Domain("history").
    Keywords("failure", "error", "bug", "issue", "problem", "failed", "broke", "fix").
    Priority(90).
    StringParam("query", "Description of the failure or error", true).
    IntParam("limit", "Maximum results (default: 10)", false).
    BoolParam("resolved_only", "Only show failures that have resolutions", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query        string `json:"query"`
            Limit        int    `json:"limit"`
            ResolvedOnly bool   `json:"resolved_only"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 10
        }

        filter := &SearchFilter{
            Domain:    DomainHistory,
            NodeTypes: []NodeType{NodeTypeFailure},
        }
        if params.ResolvedOnly {
            filter.Metadata = map[string]any{"resolved": true}
        }

        results, err := archivalist.vdb.SimilarNodes(ctx, params.Query, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatFailures(results), nil
    }).
    Build()

// get_session_history - Get history for a specific session
var GetSessionHistorySkill = skills.NewSkill("get_session_history").
    Description("Get the complete history of a session - all tasks, decisions, and outcomes.").
    Domain("history").
    Keywords("session", "this session", "current", "today").
    Priority(85).
    StringParam("session_id", "Session ID to get history for (empty for current)", false).
    BoolParam("chronological", "Order by time (default: true)", false).
    EnumParam("include", "What to include", []string{"all", "decisions", "tasks", "failures"}, false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            SessionID     string `json:"session_id"`
            Chronological bool   `json:"chronological"`
            Include       string `json:"include"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.SessionID == "" {
            params.SessionID = getCurrentSessionID(ctx)
        }

        filter := &SearchFilter{
            Domain:    DomainHistory,
            SessionID: params.SessionID,
        }

        if params.Include != "" && params.Include != "all" {
            filter.NodeTypes = []NodeType{NodeType(params.Include)}
        }

        results, err := archivalist.vdb.SimilarNodes(ctx, "", 100, filter)
        if err != nil {
            return nil, err
        }

        if params.Chronological {
            sortByTime(results)
        }
        return formatSessionHistory(results), nil
    }).
    Build()

// get_code_for_history - Cross-domain: get code for history entry
var GetCodeForHistorySkill = skills.NewSkill("get_code_for_history").
    Description("Get the code that was referenced or modified in a historical entry.").
    Domain("history").
    Keywords("code", "what code", "which code", "related code").
    Priority(75).
    StringParam("history_id", "The history entry ID", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            HistoryID string `json:"history_id"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        code, err := archivalist.vdb.GetCodeForHistory(ctx, params.HistoryID)
        if err != nil {
            return nil, err
        }
        return formatCodeForHistory(code), nil
    }).
    Build()

// get_decisions - Get architectural and design decisions
var GetDecisionsSkill = skills.NewSkill("get_decisions").
    Description("Get past architectural and design decisions, with rationale and context.").
    Domain("history").
    Keywords("decision", "decided", "chose", "why", "rationale", "architectural").
    Priority(85).
    StringParam("query", "Topic or area to find decisions about", true).
    IntParam("limit", "Maximum results (default: 10)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query string `json:"query"`
            Limit int    `json:"limit"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 10
        }

        filter := &SearchFilter{
            Domain:    DomainHistory,
            NodeTypes: []NodeType{NodeTypeDecision},
        }

        results, err := archivalist.vdb.SimilarNodes(ctx, params.Query, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatDecisions(results), nil
    }).
    Build()

// RegisterArchivalistSkills registers all Archivalist skills
func RegisterArchivalistSkills(registry *skills.Registry) {
    registry.Register(StoreSummarySkill)
    registry.Register(SearchHistorySkill)
    registry.Register(FindPatternsSkill)
    registry.Register(FindFailuresSkill)
    registry.Register(GetSessionHistorySkill)
    registry.Register(GetCodeForHistorySkill)
    registry.Register(GetDecisionsSkill)
}
```

#### Academic Skills (Academic Domain)

```go
// =============================================================================
// Academic Skills - External research, papers, best practices (Opus 4.5)
// =============================================================================

// research - Deep research on a topic
var ResearchSkill = skills.NewSkill("research").
    Description("Conduct deep research on a technical topic. Searches academic papers, documentation, and best practices. Uses Opus 4.5 for complex reasoning.").
    Domain("academic").
    Keywords("research", "learn", "understand", "best practice", "how to", "should I").
    Priority(100).
    StringParam("query", "The research question or topic", true).
    IntParam("depth", "Research depth: 1=quick, 2=moderate, 3=comprehensive (default: 2)", false).
    ArrayParam("focus_areas", "Specific areas to focus on", "string", false).
    BoolParam("include_papers", "Include academic papers (default: true)", false).
    BoolParam("include_docs", "Include official documentation (default: true)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query         string   `json:"query"`
            Depth         int      `json:"depth"`
            FocusAreas    []string `json:"focus_areas"`
            IncludePapers bool     `json:"include_papers"`
            IncludeDocs   bool     `json:"include_docs"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Depth == 0 {
            params.Depth = 2
        }

        // Calculate result limits based on depth
        limit := params.Depth * 10

        filter := &SearchFilter{Domain: DomainAcademic}
        var nodeTypes []NodeType
        if params.IncludePapers {
            nodeTypes = append(nodeTypes, NodeTypePaper)
        }
        if params.IncludeDocs {
            nodeTypes = append(nodeTypes, NodeTypeDocumentation)
        }
        if len(nodeTypes) > 0 {
            filter.NodeTypes = nodeTypes
        }

        results, err := academic.vdb.SimilarNodes(ctx, params.Query, limit, filter)
        if err != nil {
            return nil, err
        }

        // Use Opus 4.5 to synthesize research findings
        synthesis := academic.synthesizeResearch(ctx, params.Query, results, params.FocusAreas)
        return synthesis, nil
    }).
    Build()

// find_best_practices - Find established best practices
var FindBestPracticesSkill = skills.NewSkill("find_best_practices").
    Description("Find established best practices for a technology, pattern, or approach.").
    Domain("academic").
    Keywords("best practice", "recommended", "standard", "convention", "proper way").
    Priority(95).
    StringParam("topic", "The topic to find best practices for", true).
    StringParam("technology", "Specific technology context (e.g., 'Go', 'React')", false).
    IntParam("limit", "Maximum results (default: 5)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Topic      string `json:"topic"`
            Technology string `json:"technology"`
            Limit      int    `json:"limit"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 5
        }

        query := params.Topic
        if params.Technology != "" {
            query = fmt.Sprintf("%s %s best practices", params.Technology, params.Topic)
        }

        filter := &SearchFilter{
            Domain:    DomainAcademic,
            NodeTypes: []NodeType{NodeTypeBestPractice, NodeTypeDocumentation},
        }

        results, err := academic.vdb.SimilarNodes(ctx, query, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatBestPractices(results), nil
    }).
    Build()

// find_papers - Find relevant academic papers
var FindPapersSkill = skills.NewSkill("find_papers").
    Description("Find academic papers relevant to a topic. Returns abstracts and key findings.").
    Domain("academic").
    Keywords("paper", "papers", "academic", "research", "study", "publication").
    Priority(90).
    StringParam("query", "Topic to find papers about", true).
    IntParam("limit", "Maximum papers (default: 5)", false).
    IntParam("min_year", "Minimum publication year", false).
    ArrayParam("venues", "Preferred venues/conferences", "string", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Query   string   `json:"query"`
            Limit   int      `json:"limit"`
            MinYear int      `json:"min_year"`
            Venues  []string `json:"venues"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 5
        }

        filter := &SearchFilter{
            Domain:    DomainAcademic,
            NodeTypes: []NodeType{NodeTypePaper},
        }
        if params.MinYear > 0 {
            filter.Metadata = map[string]any{"min_year": params.MinYear}
        }

        results, err := academic.vdb.SimilarNodes(ctx, params.Query, params.Limit, filter)
        if err != nil {
            return nil, err
        }
        return formatPapers(results), nil
    }).
    Build()

// compare_approaches - Compare different technical approaches
var CompareApproachesSkill = skills.NewSkill("compare_approaches").
    Description("Compare different technical approaches, patterns, or technologies based on academic research and best practices. Uses Opus 4.5 for complex analysis.").
    Domain("academic").
    Keywords("compare", "versus", "vs", "difference", "tradeoff", "which is better").
    Priority(90).
    ArrayParam("approaches", "The approaches to compare (2-4)", "string", true).
    StringParam("context", "The context for comparison (e.g., 'high-throughput API')", false).
    ArrayParam("criteria", "Specific criteria to compare on", "string", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Approaches []string `json:"approaches"`
            Context    string   `json:"context"`
            Criteria   []string `json:"criteria"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        // Search for each approach
        var allResults []ScoredNode
        for _, approach := range params.Approaches {
            query := approach
            if params.Context != "" {
                query = fmt.Sprintf("%s for %s", approach, params.Context)
            }
            results, _ := academic.vdb.SimilarNodes(ctx, query, 5, &SearchFilter{Domain: DomainAcademic})
            allResults = append(allResults, results...)
        }

        // Use Opus 4.5 to synthesize comparison
        comparison := academic.synthesizeComparison(ctx, params.Approaches, allResults, params.Criteria)
        return comparison, nil
    }).
    Build()

// synthesize_with_codebase - Cross-domain: combine academic knowledge with codebase
var SynthesizeWithCodebaseSkill = skills.NewSkill("synthesize_with_codebase").
    Description("Synthesize academic knowledge with how things are actually implemented in the codebase. Shows theory vs practice.").
    Domain("academic").
    Keywords("how we do", "our implementation", "in practice", "codebase").
    Priority(85).
    StringParam("topic", "The topic to synthesize", true).
    BoolParam("show_gaps", "Highlight gaps between best practice and implementation", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Topic    string `json:"topic"`
            ShowGaps bool   `json:"show_gaps"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        // Get academic knowledge
        academicResults, _ := academic.vdb.SimilarNodes(ctx, params.Topic, 5, &SearchFilter{Domain: DomainAcademic})

        // Get codebase implementations
        codeResults, _ := academic.vdb.SimilarNodes(ctx, params.Topic, 5, &SearchFilter{Domain: DomainCode})

        // Synthesize with Opus 4.5
        synthesis := academic.synthesizeTheoryAndPractice(ctx, params.Topic, academicResults, codeResults, params.ShowGaps)
        return synthesis, nil
    }).
    Build()

// get_code_for_academic - Cross-domain: get code implementing academic concept
var GetCodeForAcademicSkill = skills.NewSkill("get_code_for_academic").
    Description("Find code in the codebase that implements a specific academic concept or pattern.").
    Domain("academic").
    Keywords("implementation", "example", "code for").
    Priority(80).
    StringParam("academic_id", "The academic node ID", true).
    IntParam("limit", "Maximum code examples (default: 5)", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            AcademicID string `json:"academic_id"`
            Limit      int    `json:"limit"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }
        if params.Limit == 0 {
            params.Limit = 5
        }

        code, err := academic.vdb.GetCodeForAcademic(ctx, params.AcademicID)
        if err != nil {
            return nil, err
        }
        return formatCodeForAcademic(code, params.Limit), nil
    }).
    Build()

// get_history_for_academic - Cross-domain: get history related to academic concept
var GetHistoryForAcademicSkill = skills.NewSkill("get_history_for_academic").
    Description("Find historical decisions and discussions related to an academic concept.").
    Domain("academic").
    Keywords("history", "decisions about", "discussions").
    Priority(75).
    StringParam("academic_id", "The academic node ID", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            AcademicID string `json:"academic_id"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, err
        }

        history, err := academic.vdb.GetHistoryForAcademic(ctx, params.AcademicID)
        if err != nil {
            return nil, err
        }
        return formatHistoryForAcademic(history), nil
    }).
    Build()

// RegisterAcademicSkills registers all Academic skills
func RegisterAcademicSkills(registry *skills.Registry) {
    registry.Register(ResearchSkill)
    registry.Register(FindBestPracticesSkill)
    registry.Register(FindPapersSkill)
    registry.Register(CompareApproachesSkill)
    registry.Register(SynthesizeWithCodebaseSkill)
    registry.Register(GetCodeForAcademicSkill)
    registry.Register(GetHistoryForAcademicSkill)
}
```

#### Skill Summary Table

| Agent | Skill | Description | Cross-Domain |
|-------|-------|-------------|--------------|
| **Librarian** | `search_code` | Semantic search for code | No |
| | `get_symbol` | Get symbol details | No |
| | `get_dependencies` | What a symbol depends on | No |
| | `get_dependents` | What depends on a symbol | No |
| | `find_similar_symbols` | Find similar code | No |
| | `get_file_symbols` | All symbols in a file | No |
| | `get_history_for_code` | History for code | Yes → History |
| **Archivalist** | `store_summary` | Store agent summaries | Yes → Code |
| | `search_history` | Search past context | No |
| | `find_patterns` | Find recurring patterns | No |
| | `find_failures` | Find past failures | No |
| | `get_session_history` | Session history | No |
| | `get_code_for_history` | Code for history | Yes → Code |
| | `get_decisions` | Past decisions | No |
| **Academic** | `research` | Deep research (Opus) | No |
| | `find_best_practices` | Best practices | No |
| | `find_papers` | Academic papers | No |
| | `compare_approaches` | Compare options (Opus) | No |
| | `synthesize_with_codebase` | Theory vs practice (Opus) | Yes → Code |
| | `get_code_for_academic` | Code for concept | Yes → Code |
| | `get_history_for_academic` | History for concept | Yes → History |

### Cross-Domain Edge Creation

Knowledge agents create cross-domain edges when relationships are discovered:

```go
// When Archivalist stores a summary that references code
func (a *Archivalist) StoreSummary(ctx context.Context, summary AgentSummary) error {
    // 1. Store in History domain
    historyNode, err := a.vdb.AddNode(ctx, &Node{
        ID:        summary.ID,
        Domain:    DomainHistory,
        NodeType:  NodeTypeSession,
        Content:   summary.Content,
        Metadata:  summary.Metadata,
    })

    // 2. Find referenced code and create cross-domain edges
    for _, codeRef := range summary.CodeReferences {
        a.vdb.AddEdge(ctx, historyNode.ID, codeRef.ID, EdgeTypeReferencedIn, 0.9, nil)
    }

    return nil
}

// When Librarian discovers code implements an academic pattern
func (l *Librarian) LinkToAcademic(ctx context.Context, codeID string, academicID string) error {
    return l.vdb.AddEdge(ctx, codeID, academicID, EdgeTypeImplements, 0.85, nil)
}

// When Academic finds research relevant to historical decisions
func (ac *Academic) LinkToHistory(ctx context.Context, academicID string, historyID string) error {
    return ac.vdb.AddEdge(ctx, academicID, historyID, EdgeTypeInspiredBy, 0.8, nil)
}
```

### Summary Storage Flow

All agent summaries flow through Guide to Archivalist:

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        SUMMARY STORAGE FLOW                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐               │
│  │ Architect  │  │Orchestrator│  │  Engineer  │  │  Inspector │               │
│  └─────┬──────┘  └─────┬──────┘  └─────┬──────┘  └─────┬──────┘               │
│        │               │               │               │                        │
│        │ Summary       │ Summary       │ Summary       │ Summary                │
│        │               │               │               │                        │
│        └───────────────┴───────────────┴───────────────┘                        │
│                                  │                                              │
│                                  ▼                                              │
│                        ┌─────────────────┐                                      │
│                        │      GUIDE      │                                      │
│                        │                 │                                      │
│                        │ Routes to:      │                                      │
│                        │ archivalist/    │                                      │
│                        │ store           │                                      │
│                        └────────┬────────┘                                      │
│                                 │                                               │
│                                 ▼                                               │
│                        ┌─────────────────┐                                      │
│                        │   ARCHIVALIST   │                                      │
│                        │                 │                                      │
│                        │ 1. Store node   │                                      │
│                        │ 2. Create edges │                                      │
│                        │ 3. Index embed  │                                      │
│                        └────────┬────────┘                                      │
│                                 │                                               │
│                                 ▼                                               │
│                        ┌─────────────────┐                                      │
│                        │  VectorGraphDB  │                                      │
│                        │                 │                                      │
│                        │ History Domain  │                                      │
│                        └─────────────────┘                                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### LLM Selection by Agent

Knowledge agents use appropriate LLM models based on task complexity:

| Agent | Model | Rationale |
|-------|-------|-----------|
| Librarian | Sonnet 4.5 | Fast code search, symbol navigation - speed critical |
| Archivalist | Sonnet 4.5 | Pattern matching, history queries - speed critical |
| Academic | Opus 4.5 | Complex research, synthesis - reasoning critical |

```go
// Guide selects model based on routed agent
func (g *Guide) selectModel(routedAgent string) string {
    switch routedAgent {
    case "librarian", "archivalist":
        return "claude-sonnet-4-5"  // Fast, efficient
    case "academic":
        return "claude-opus-4-5"    // Complex reasoning
    default:
        return "claude-sonnet-4-5"
    }
}
```

### Agentic RAG: Progressive Retrieval

Knowledge agents use progressive retrieval - the LLM decides when and how much to retrieve:

```go
// AgenticRAGConfig for progressive retrieval
type AgenticRAGConfig struct {
    // Initial retrieval count
    InitialK int

    // Maximum retrieval depth
    MaxDepth int

    // Confidence threshold to stop retrieving
    ConfidenceThreshold float64

    // Maximum tokens to spend on retrieval
    MaxRetrievalTokens int
}

// Progressive retrieval loop
func (agent *KnowledgeAgent) progressiveRetrieve(ctx context.Context, query string) ([]Node, error) {
    var accumulated []Node
    depth := 0
    confidence := 0.0

    for depth < agent.config.MaxDepth && confidence < agent.config.ConfidenceThreshold {
        // Retrieve next batch
        results, err := agent.vdb.SimilarNodes(ctx, query, agent.config.InitialK, nil)
        if err != nil {
            return accumulated, err
        }

        // LLM evaluates: do we have enough?
        evaluation := agent.llm.Evaluate(ctx, query, accumulated, results)

        if evaluation.Sufficient {
            confidence = evaluation.Confidence
            break
        }

        // LLM suggests next query refinement
        query = evaluation.RefinedQuery
        accumulated = append(accumulated, evaluation.SelectedNodes...)
        depth++
    }

    return accumulated, nil
}
```

---

## Agent Efficiency Techniques

This section describes techniques to help agents work more efficiently, avoid repeated mistakes, and maintain consistency. These address common inefficiencies in LLM-based coding agents.

### Agent Pain Points Addressed

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    AGENT INEFFICIENCIES ADDRESSED                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   CONTEXT LOSS                        REPEATED WORK                         │
│   ─────────────                       ─────────────                         │
│   • Forgets what it learned           • Reads same files repeatedly        │
│   • Loses track of current task       • Searches for same things           │
│   • Re-gathers context it just had    • Rebuilds context unnecessarily     │
│                                                                             │
│   PATTERN BLINDNESS                   STYLE DRIFT                           │
│   ────────────────                    ───────────                           │
│   • Doesn't recognize similar work    • Inconsistent code style            │
│   • Reinvents solved problems         • Mismatched UI patterns             │
│   • Misses reusable components        • Naming inconsistencies             │
│                                                                             │
│   MISTAKE REPETITION                  USER PREFERENCE AMNESIA              │
│   ─────────────────                   ───────────────────────              │
│   • Makes same errors repeatedly      • Forgets stated preferences         │
│   • Doesn't learn from failures       • Re-asks clarifying questions       │
│   • Falls into known pitfalls         • Ignores implicit preferences       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Technique Summary

| Technique | Problem Solved | Token Savings | Complexity |
|-----------|---------------|---------------|------------|
| **Scratchpad Memory** | Context loss within session | ~15% | Low |
| **Style Inference** | Style drift, inconsistency | ~10% | Medium |
| **Component Registry** | Duplicate components | ~20% | Medium |
| **Mistake Memory** | Repeated errors | ~15% | Medium |
| **Diff Preview** | Wrong edits | ~5% | Low |
| **User Preferences** | Re-asking, wrong choices | ~10% | Medium |
| **File Snapshot Cache** | Re-reading files | ~10% | Low |
| **Dependency Awareness** | Duplicate deps | ~5% | Low |
| **Task Continuity** | Context rebuild on resume | ~20% | Medium |
| **Design Token Awareness** | UI inconsistency | ~5% | Low |

---

### 1. Scratchpad Memory (Working Memory)

Within-session memory that persists across tool calls. Agents write notes to themselves.

```go
// Scratchpad provides within-session working memory for agents
type Scratchpad struct {
    mu      sync.RWMutex
    notes   map[string]ScratchpadNote
    session string
}

type ScratchpadNote struct {
    Key       string    `json:"key"`
    Content   string    `json:"content"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    TTL       time.Duration `json:"ttl,omitempty"`
}

// Skills for scratchpad access
var ScratchpadWriteSkill = skills.NewSkill("scratchpad_write").
    Description("Write a note to yourself for later reference in this session. Use for tracking progress, decisions, things tried, or user preferences.").
    Domain("memory").
    Keywords("remember", "note", "track", "save").
    Priority(100).
    StringParam("key", "Short key for this note (e.g., 'current_task', 'user_preference', 'tried_failed')", true).
    StringParam("note", "The note content", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Key  string `json:"key"`
            Note string `json:"note"`
        }
        json.Unmarshal(input, &params)

        session := getSession(ctx)
        session.Scratchpad.Set(params.Key, params.Note)

        return map[string]any{
            "stored": true,
            "key":    params.Key,
        }, nil
    }).
    Build()

var ScratchpadReadSkill = skills.NewSkill("scratchpad_read").
    Description("Read a note you wrote earlier in this session.").
    Domain("memory").
    StringParam("key", "Key to read (or 'all' for all notes)", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Key string `json:"key"`
        }
        json.Unmarshal(input, &params)

        session := getSession(ctx)

        if params.Key == "all" {
            return session.Scratchpad.GetAll(), nil
        }

        note, exists := session.Scratchpad.Get(params.Key)
        if !exists {
            return map[string]any{"exists": false}, nil
        }

        return map[string]any{
            "exists":  true,
            "content": note.Content,
        }, nil
    }).
    Build()
```

**Use cases:**
```
Agent: scratchpad_write("current_approach", "Using JWT with refresh tokens, user confirmed")
Agent: scratchpad_write("tried_failed", "Redis caching failed - version mismatch with existing redis 5.x")
Agent: scratchpad_write("user_prefers", "Functional components, minimal comments, early returns")
Agent: scratchpad_write("files_modified", "auth/handler.go, auth/middleware.go, auth/types.go")

Later in session...
Agent: scratchpad_read("current_approach")
  → "Using JWT with refresh tokens, user confirmed"
Agent: scratchpad_read("tried_failed")
  → "Redis caching failed - version mismatch with existing redis 5.x"
```

**Token savings**: Avoids re-asking user, avoids re-gathering context, tracks what was tried.

---

### 2. Code Style Inference

Automatically analyze and enforce codebase style without agent effort.

```go
// StyleInference analyzes codebase to infer coding conventions
type StyleInference struct {
    mu sync.RWMutex

    // Inferred styles by language
    styles map[string]*LanguageStyle

    // Last analysis time
    analyzedAt time.Time
}

type LanguageStyle struct {
    Language string `json:"language"`

    // Naming conventions
    Naming NamingStyle `json:"naming"`

    // Formatting
    Formatting FormattingStyle `json:"formatting"`

    // Patterns
    Patterns PatternStyle `json:"patterns"`

    // Examples from codebase
    Examples []StyleExample `json:"examples,omitempty"`
}

type NamingStyle struct {
    Functions   string `json:"functions"`    // camelCase, snake_case, PascalCase
    Variables   string `json:"variables"`
    Constants   string `json:"constants"`
    Types       string `json:"types"`
    Files       string `json:"files"`        // kebab-case, snake_case, camelCase
    Components  string `json:"components"`   // For React/Vue
}

type FormattingStyle struct {
    IndentStyle string `json:"indent_style"` // tabs, spaces
    IndentSize  int    `json:"indent_size"`
    QuoteStyle  string `json:"quote_style"`  // single, double
    Semicolons  bool   `json:"semicolons"`
    TrailingComma string `json:"trailing_comma"` // all, es5, none
}

type PatternStyle struct {
    ErrorHandling  string `json:"error_handling"`  // try-catch, Result, early-return
    AsyncStyle     string `json:"async_style"`     // async-await, promises, callbacks
    ComponentStyle string `json:"component_style"` // functional, class
    ExportStyle    string `json:"export_style"`    // named, default, mixed
    ImportOrder    []string `json:"import_order"`  // ["builtin", "external", "internal", "relative"]
}

// Skill to get style guide
var GetStyleGuideSkill = skills.NewSkill("get_style_guide").
    Description("Get the inferred code style guide for this project. Use BEFORE writing code to match existing patterns.").
    Domain("code").
    Keywords("style", "convention", "format", "naming").
    Priority(90).
    StringParam("language", "Language to get style for (or 'all')", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Language string `json:"language"`
        }
        json.Unmarshal(input, &params)

        if params.Language == "" || params.Language == "all" {
            return styleInference.GetAll(), nil
        }

        return styleInference.Get(params.Language), nil
    }).
    Build()
```

**Example response:**
```json
{
  "language": "typescript",
  "naming": {
    "functions": "camelCase",
    "variables": "camelCase",
    "constants": "UPPER_SNAKE_CASE",
    "types": "PascalCase",
    "files": "kebab-case",
    "components": "PascalCase"
  },
  "formatting": {
    "indent_style": "spaces",
    "indent_size": 2,
    "quote_style": "single",
    "semicolons": false,
    "trailing_comma": "es5"
  },
  "patterns": {
    "error_handling": "try-catch with custom errors",
    "async_style": "async-await",
    "component_style": "functional with hooks",
    "export_style": "named exports",
    "import_order": ["react", "external", "internal", "relative", "styles"]
  },
  "examples": [
    {"pattern": "error_handling", "file": "src/utils/api.ts", "line": 45}
  ]
}
```

**Token savings**: Agent doesn't guess style, produces consistent code first time.

---

### 3. Component/Pattern Registry

Know what reusable components and patterns exist before writing new ones.

```go
// ComponentRegistry indexes reusable code artifacts
type ComponentRegistry struct {
    mu sync.RWMutex

    // Indexed components by type
    uiComponents []UIComponent
    hooks        []Hook
    utilities    []Utility
    patterns     []Pattern

    // Search index
    searchIndex *SearchIndex
}

type UIComponent struct {
    Name        string            `json:"name"`
    Path        string            `json:"path"`
    Description string            `json:"description"`
    Props       []PropDefinition  `json:"props"`
    UsageExample string           `json:"usage_example"`
    Tags        []string          `json:"tags"`
    Variants    []string          `json:"variants,omitempty"`
}

type Hook struct {
    Name        string   `json:"name"`
    Path        string   `json:"path"`
    Description string   `json:"description"`
    Parameters  []Param  `json:"parameters"`
    Returns     string   `json:"returns"`
    UsageExample string  `json:"usage_example"`
}

type Utility struct {
    Name        string   `json:"name"`
    Path        string   `json:"path"`
    Description string   `json:"description"`
    Signature   string   `json:"signature"`
    UsageExample string  `json:"usage_example"`
}

// Skill to search for existing components
var FindComponentSkill = skills.NewSkill("find_component").
    Description("Search for existing reusable components, hooks, or utilities BEFORE writing new ones. Prevents duplication.").
    Domain("code").
    Keywords("component", "hook", "utility", "reuse", "existing").
    Priority(95).
    StringParam("need", "What you need (e.g., 'modal dialog', 'date formatting', 'auth hook')", true).
    EnumParam("type", "Type to search for", []string{"ui", "hook", "utility", "pattern", "any"}, false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Need string `json:"need"`
            Type string `json:"type"`
        }
        json.Unmarshal(input, &params)

        if params.Type == "" {
            params.Type = "any"
        }

        matches := componentRegistry.Search(params.Need, params.Type)

        if len(matches) == 0 {
            return map[string]any{
                "found": false,
                "suggestion": "No existing component found. You may need to create one.",
            }, nil
        }

        return map[string]any{
            "found":   true,
            "matches": formatComponentMatches(matches),
        }, nil
    }).
    Build()
```

**Example usage:**
```
Agent: find_component("confirmation dialog", type="ui")
Response: {
  "found": true,
  "matches": [
    {
      "name": "ConfirmModal",
      "path": "src/components/modals/ConfirmModal.tsx",
      "description": "Reusable confirmation dialog with customizable actions",
      "props": [
        {"name": "title", "type": "string", "required": true},
        {"name": "message", "type": "string", "required": true},
        {"name": "onConfirm", "type": "() => void", "required": true},
        {"name": "onCancel", "type": "() => void", "required": false},
        {"name": "confirmText", "type": "string", "default": "Confirm"},
        {"name": "variant", "type": "'danger' | 'warning' | 'info'", "default": "warning"}
      ],
      "usage_example": "<ConfirmModal\n  title=\"Delete Item?\"\n  message=\"This cannot be undone.\"\n  onConfirm={handleDelete}\n  variant=\"danger\"\n/>"
    }
  ]
}
```

Agent uses existing `ConfirmModal` instead of creating a duplicate.

**Token savings**: Avoids writing duplicate components, ensures UI consistency.

---

### 4. Mistake Memory (Anti-Pattern Database)

Learn from failures and share knowledge across sessions.

```go
// MistakeMemory stores known anti-patterns and failures
type MistakeMemory struct {
    mu sync.RWMutex

    mistakes []Mistake
    index    *SearchIndex

    // Persistence
    storage Storage
}

type Mistake struct {
    ID          string    `json:"id"`
    Pattern     string    `json:"pattern"`      // What was tried
    Error       string    `json:"error"`        // What went wrong
    Fix         string    `json:"fix"`          // How to fix/avoid
    Context     string    `json:"context"`      // When this applies
    Tags        []string  `json:"tags"`
    Confidence  float64   `json:"confidence"`   // How reliable (0-1)
    Occurrences int       `json:"occurrences"`  // Times encountered
    CreatedAt   time.Time `json:"created_at"`
    LastSeenAt  time.Time `json:"last_seen_at"`
}

// Skill to check for known mistakes
var CheckMistakesSkill = skills.NewSkill("check_mistakes").
    Description("Check if an approach has known issues BEFORE trying it. Learns from past failures across all sessions.").
    Domain("memory").
    Keywords("mistake", "issue", "problem", "avoid", "pitfall").
    Priority(95).
    StringParam("approach", "What you're planning to do", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Approach string `json:"approach"`
        }
        json.Unmarshal(input, &params)

        matches := mistakeMemory.Search(params.Approach)

        if len(matches) == 0 {
            return map[string]any{
                "known_issues": false,
                "safe_to_proceed": true,
            }, nil
        }

        return map[string]any{
            "known_issues": true,
            "warnings": formatMistakeWarnings(matches),
        }, nil
    }).
    Build()

// Skill to report a new mistake
var ReportMistakeSkill = skills.NewSkill("report_mistake").
    Description("Report a mistake or anti-pattern you discovered. Helps future sessions avoid it.").
    Domain("memory").
    Keywords("report", "learned", "discovered", "anti-pattern").
    Priority(80).
    StringParam("pattern", "What was tried", true).
    StringParam("error", "What went wrong", true).
    StringParam("fix", "How to fix or avoid this", true).
    StringParam("context", "When this applies (optional)", false).
    ArrayParam("tags", "Tags for categorization", "string", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Pattern string   `json:"pattern"`
            Error   string   `json:"error"`
            Fix     string   `json:"fix"`
            Context string   `json:"context"`
            Tags    []string `json:"tags"`
        }
        json.Unmarshal(input, &params)

        mistake := &Mistake{
            ID:          generateID("mistake"),
            Pattern:     params.Pattern,
            Error:       params.Error,
            Fix:         params.Fix,
            Context:     params.Context,
            Tags:        params.Tags,
            Confidence:  0.7, // Initial confidence
            Occurrences: 1,
            CreatedAt:   time.Now(),
            LastSeenAt:  time.Now(),
        }

        mistakeMemory.Add(mistake)

        return map[string]any{
            "recorded": true,
            "id":       mistake.ID,
        }, nil
    }).
    Build()
```

**Example - checking before trying:**
```
Agent: check_mistakes("using localStorage for auth tokens")
Response: {
  "known_issues": true,
  "warnings": [
    {
      "pattern": "storing auth tokens in localStorage",
      "error": "XSS vulnerability - tokens accessible via JavaScript injection",
      "fix": "Use httpOnly cookies for sensitive tokens, or sessionStorage for less sensitive data",
      "context": "Web applications with authentication",
      "confidence": 0.95,
      "occurrences": 12
    }
  ]
}
Agent: "I'll use httpOnly cookies instead"
```

**Example - reporting a new mistake:**
```
Agent: report_mistake(
  pattern="using moment.js for new projects",
  error="moment.js is deprecated and has large bundle size",
  fix="Use date-fns or dayjs instead - smaller, tree-shakeable",
  context="JavaScript/TypeScript date handling",
  tags=["javascript", "dependencies", "performance"]
)
```

**Token savings**: Avoids making known mistakes, avoids debugging sessions.

---

### 5. Diff Preview

Show what will change before making changes. Catches errors early.

```go
// DiffPreview shows changes before applying them
type DiffPreview struct {
    Diff          string   `json:"diff"`
    LinesAdded    int      `json:"lines_added"`
    LinesRemoved  int      `json:"lines_removed"`
    FilesAffected int      `json:"files_affected"`
    Warnings      []string `json:"warnings,omitempty"`
    Reversible    bool     `json:"reversible"`
}

var PreviewEditSkill = skills.NewSkill("preview_edit").
    Description("Preview what an edit will look like BEFORE applying it. Shows diff and potential issues.").
    Domain("code").
    Keywords("preview", "diff", "check", "before").
    Priority(85).
    StringParam("file_path", "File to edit", true).
    StringParam("old_string", "Text to replace", true).
    StringParam("new_string", "Replacement text", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            FilePath  string `json:"file_path"`
            OldString string `json:"old_string"`
            NewString string `json:"new_string"`
        }
        json.Unmarshal(input, &params)

        // Read current file
        content, err := readFile(params.FilePath)
        if err != nil {
            return nil, err
        }

        // Check if old_string exists
        if !strings.Contains(content, params.OldString) {
            return map[string]any{
                "error": "old_string not found in file",
                "suggestion": "Check for whitespace differences or use a larger context",
            }, nil
        }

        // Check for multiple matches
        matchCount := strings.Count(content, params.OldString)

        // Generate preview
        newContent := strings.Replace(content, params.OldString, params.NewString, 1)
        diff := generateUnifiedDiff(params.FilePath, content, newContent)

        preview := DiffPreview{
            Diff:          diff,
            LinesAdded:    countLines(params.NewString),
            LinesRemoved:  countLines(params.OldString),
            FilesAffected: 1,
            Reversible:    true,
        }

        if matchCount > 1 {
            preview.Warnings = append(preview.Warnings,
                fmt.Sprintf("old_string matches %d times - only first will be replaced", matchCount))
        }

        return preview, nil
    }).
    Build()
```

**Example:**
```
Agent: preview_edit(
  file_path="src/auth/handler.ts",
  old_string="const token = localStorage.getItem('token')",
  new_string="const token = await getSecureToken()"
)
Response: {
  "diff": "--- src/auth/handler.ts\n+++ src/auth/handler.ts\n@@ -45,7 +45,7 @@\n-  const token = localStorage.getItem('token')\n+  const token = await getSecureToken()",
  "lines_added": 1,
  "lines_removed": 1,
  "files_affected": 1,
  "warnings": [],
  "reversible": true
}
```

**Token savings**: Catches errors before they happen, avoids fix-up cycles.

---

### 6. User Preference Learning

Automatically learn and apply user preferences.

```go
// UserPreferences tracks explicit and inferred preferences
type UserPreferences struct {
    mu sync.RWMutex

    // Explicitly stated by user
    Explicit map[string]string `json:"explicit"`

    // Inferred from behavior
    Implicit map[string]InferredPreference `json:"implicit"`

    // Session overrides (temporary)
    SessionOverrides map[string]string `json:"session_overrides,omitempty"`
}

type InferredPreference struct {
    Value       string   `json:"value"`
    Confidence  float64  `json:"confidence"`  // 0-1
    Evidence    []string `json:"evidence"`    // Why we think this
    ObservedAt  time.Time `json:"observed_at"`
}

// Learn from user feedback
func (p *UserPreferences) LearnFromFeedback(category, suggestion, response string) {
    p.mu.Lock()
    defer p.mu.Unlock()

    existing, exists := p.Implicit[category]

    if response == "accepted" {
        if exists && existing.Value == suggestion {
            // Reinforce
            existing.Confidence = min(existing.Confidence + 0.1, 1.0)
            existing.Evidence = append(existing.Evidence, "accepted: "+suggestion)
        } else {
            // New preference
            p.Implicit[category] = InferredPreference{
                Value:      suggestion,
                Confidence: 0.6,
                Evidence:   []string{"accepted: " + suggestion},
                ObservedAt: time.Now(),
            }
        }
    } else if response == "rejected" {
        if exists && existing.Value == suggestion {
            // Diminish confidence
            existing.Confidence = max(existing.Confidence - 0.2, 0)
        }
    }
}

// Skill to get preferences
var GetPreferencesSkill = skills.NewSkill("get_preferences").
    Description("Get known user preferences to inform your approach. Includes explicit and inferred preferences.").
    Domain("memory").
    Keywords("preference", "like", "prefer", "style").
    Priority(90).
    StringParam("category", "Category: code_style, ui, workflow, tools, all", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Category string `json:"category"`
        }
        json.Unmarshal(input, &params)

        if params.Category == "" || params.Category == "all" {
            return userPreferences.GetAll(), nil
        }

        return userPreferences.GetCategory(params.Category), nil
    }).
    Build()

// Skill to set explicit preference
var SetPreferenceSkill = skills.NewSkill("set_preference").
    Description("Record an explicit user preference.").
    Domain("memory").
    StringParam("category", "Preference category", true).
    StringParam("preference", "The preference value", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Category   string `json:"category"`
            Preference string `json:"preference"`
        }
        json.Unmarshal(input, &params)

        userPreferences.SetExplicit(params.Category, params.Preference)

        return map[string]any{"stored": true}, nil
    }).
    Build()
```

**Example response:**
```json
{
  "explicit": {
    "framework": "React with TypeScript",
    "styling": "Tailwind CSS",
    "testing": "Prefer integration tests over unit tests",
    "comments": "Only for complex logic"
  },
  "implicit": {
    "error_handling": {
      "value": "early return pattern",
      "confidence": 0.85,
      "evidence": ["accepted in auth.ts", "accepted in api.ts", "used in user code"]
    },
    "component_size": {
      "value": "small, single responsibility",
      "confidence": 0.78,
      "evidence": ["broke up large component when suggested", "approved split"]
    },
    "variable_naming": {
      "value": "descriptive, avoid abbreviations",
      "confidence": 0.72,
      "evidence": ["renamed 'usr' to 'user'", "renamed 'btn' to 'button'"]
    }
  }
}
```

**Token savings**: Avoids re-asking user, makes choices that match expectations.

---

### 7. File Snapshot Cache

Avoid re-reading files that haven't changed within a session.

```go
// FileSnapshotCache caches file contents within a session
type FileSnapshotCache struct {
    mu        sync.RWMutex
    snapshots map[string]*FileSnapshot
    watcher   *fsnotify.Watcher
    maxAge    time.Duration
}

type FileSnapshot struct {
    Path       string    `json:"path"`
    Content    string    `json:"-"`  // Not serialized
    Hash       string    `json:"hash"`
    Size       int64     `json:"size"`
    ModTime    time.Time `json:"mod_time"`
    ReadAt     time.Time `json:"read_at"`
    TokenCount int       `json:"token_count"`
    HitCount   int       `json:"hit_count"`
}

// GetOrRead returns cached content or reads fresh
func (c *FileSnapshotCache) GetOrRead(ctx context.Context, path string) (*FileSnapshot, bool, error) {
    c.mu.RLock()
    snapshot, exists := c.snapshots[path]
    c.mu.RUnlock()

    if exists {
        // Check if file has changed
        stat, err := os.Stat(path)
        if err == nil && stat.ModTime().Equal(snapshot.ModTime) && stat.Size() == snapshot.Size {
            // Cache hit
            c.mu.Lock()
            snapshot.HitCount++
            c.mu.Unlock()
            return snapshot, true, nil
        }
    }

    // Cache miss - read file
    content, err := os.ReadFile(path)
    if err != nil {
        return nil, false, err
    }

    stat, _ := os.Stat(path)

    snapshot = &FileSnapshot{
        Path:       path,
        Content:    string(content),
        Hash:       hashContent(content),
        Size:       stat.Size(),
        ModTime:    stat.ModTime(),
        ReadAt:     time.Now(),
        TokenCount: estimateTokens(string(content)),
        HitCount:   0,
    }

    c.mu.Lock()
    c.snapshots[path] = snapshot
    c.mu.Unlock()

    return snapshot, false, nil
}

// Integrated into Read skill handler
func enhancedReadHandler(ctx context.Context, input json.RawMessage) (any, error) {
    var params struct {
        FilePath string `json:"file_path"`
    }
    json.Unmarshal(input, &params)

    snapshot, cacheHit, err := fileCache.GetOrRead(ctx, params.FilePath)
    if err != nil {
        return nil, err
    }

    return FileReadResponse{
        Content:   snapshot.Content,
        CacheHit:  cacheHit,
        TokenCount: snapshot.TokenCount,
    }, nil
}
```

**Token savings**: Avoids re-reading unchanged files (common in iterative work).

---

### 8. Dependency Awareness

Know what's available before suggesting new dependencies.

```go
// DependencyRegistry tracks project dependencies and their capabilities
type DependencyRegistry struct {
    mu sync.RWMutex

    // Parsed from package.json, go.mod, requirements.txt, etc.
    dependencies map[string]*Dependency

    // Capability index
    capabilities map[string][]*Dependency  // capability -> deps that provide it
}

type Dependency struct {
    Name         string            `json:"name"`
    Version      string            `json:"version"`
    Type         string            `json:"type"`  // runtime, dev, peer
    Capabilities []string          `json:"capabilities"`
    UsageExamples map[string]string `json:"usage_examples"`
}

// Skill to check for existing capabilities
var CheckDependencySkill = skills.NewSkill("check_dependency").
    Description("Check if a capability is already available via existing dependencies BEFORE suggesting a new one.").
    Domain("code").
    Keywords("dependency", "package", "library", "install").
    Priority(90).
    StringParam("need", "What capability you need (e.g., 'date formatting', 'HTTP client', 'form validation')", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Need string `json:"need"`
        }
        json.Unmarshal(input, &params)

        existing := dependencyRegistry.FindCapability(params.Need)

        if len(existing) > 0 {
            return map[string]any{
                "already_available": true,
                "packages": formatDependencies(existing),
            }, nil
        }

        // Suggest alternatives
        suggestions := dependencyRegistry.SuggestPackages(params.Need)

        return map[string]any{
            "already_available": false,
            "suggestions":       suggestions,
        }, nil
    }).
    Build()
```

**Example:**
```
Agent: check_dependency("date formatting")
Response: {
  "already_available": true,
  "packages": [
    {
      "name": "date-fns",
      "version": "2.30.0",
      "type": "runtime",
      "capabilities": ["date formatting", "date parsing", "date manipulation"],
      "usage_examples": {
        "format": "import { format } from 'date-fns'\nformat(new Date(), 'yyyy-MM-dd')",
        "parse": "import { parse } from 'date-fns'\nparse('2024-01-15', 'yyyy-MM-dd', new Date())"
      }
    }
  ]
}
Agent: "I'll use the existing date-fns instead of adding moment.js"
```

**Token savings**: Avoids adding duplicate dependencies, knows how to use existing ones.

---

### 9. Task Continuity (Checkpoint/Resume)

Resume interrupted work without re-gathering context.

```go
// TaskCheckpoint saves progress for later resumption
type TaskCheckpoint struct {
    ID          string          `json:"id"`
    SessionID   string          `json:"session_id"`
    Description string          `json:"description"`
    Status      string          `json:"status"`  // in_progress, blocked, paused, completed

    // Progress tracking
    Progress    []string        `json:"progress"`     // Steps completed
    NextSteps   []string        `json:"next_steps"`   // What's remaining

    // Context preservation
    Context     map[string]any  `json:"context"`      // Gathered context
    FilesRead   []string        `json:"files_read"`   // Files already read
    FilesModi   []string        `json:"files_modified"` // Files changed
    Decisions   []Decision      `json:"decisions"`    // Decisions made

    // Scratchpad at time of checkpoint
    Scratchpad  map[string]string `json:"scratchpad"`

    CreatedAt   time.Time       `json:"created_at"`
    UpdatedAt   time.Time       `json:"updated_at"`
}

type Decision struct {
    Question string `json:"question"`
    Choice   string `json:"choice"`
    Reason   string `json:"reason"`
}

// Skill to save checkpoint
var SaveCheckpointSkill = skills.NewSkill("save_checkpoint").
    Description("Save your current progress so you can resume later if interrupted. Include what you've done and what's next.").
    Domain("memory").
    Keywords("save", "checkpoint", "pause", "resume later").
    Priority(85).
    StringParam("summary", "Brief summary of what you've done so far", true).
    ArrayParam("completed", "Steps you've completed", "string", true).
    ArrayParam("next_steps", "Steps remaining", "string", true).
    StringParam("status", "Status: in_progress, blocked, paused", false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Summary   string   `json:"summary"`
            Completed []string `json:"completed"`
            NextSteps []string `json:"next_steps"`
            Status    string   `json:"status"`
        }
        json.Unmarshal(input, &params)

        session := getSession(ctx)

        checkpoint := &TaskCheckpoint{
            ID:          generateID("checkpoint"),
            SessionID:   session.ID,
            Description: params.Summary,
            Status:      params.Status,
            Progress:    params.Completed,
            NextSteps:   params.NextSteps,
            FilesRead:   session.FilesRead,
            FilesModi:   session.FilesModified,
            Scratchpad:  session.Scratchpad.GetAll(),
            CreatedAt:   time.Now(),
            UpdatedAt:   time.Now(),
        }

        checkpointStore.Save(checkpoint)

        return map[string]any{
            "saved": true,
            "id":    checkpoint.ID,
            "hint":  "Use resume_task with this ID to continue later",
        }, nil
    }).
    Build()

// Skill to resume from checkpoint
var ResumeTaskSkill = skills.NewSkill("resume_task").
    Description("Resume a previously saved task with full context restored.").
    Domain("memory").
    Keywords("resume", "continue", "pick up").
    Priority(100).
    StringParam("task_id", "Checkpoint ID to resume (or 'latest' for most recent)", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            TaskID string `json:"task_id"`
        }
        json.Unmarshal(input, &params)

        var checkpoint *TaskCheckpoint
        if params.TaskID == "latest" {
            checkpoint = checkpointStore.GetLatest()
        } else {
            checkpoint = checkpointStore.Get(params.TaskID)
        }

        if checkpoint == nil {
            return map[string]any{
                "found": false,
                "error": "No checkpoint found",
            }, nil
        }

        // Restore scratchpad
        session := getSession(ctx)
        for k, v := range checkpoint.Scratchpad {
            session.Scratchpad.Set(k, v)
        }

        return TaskResume{
            ID:            checkpoint.ID,
            Summary:       checkpoint.Description,
            Completed:     checkpoint.Progress,
            NextSteps:     checkpoint.NextSteps,
            FilesRead:     checkpoint.FilesRead,
            FilesModified: checkpoint.FilesModi,
            Scratchpad:    checkpoint.Scratchpad,
            Hint:          fmt.Sprintf("Continue with: %s", checkpoint.NextSteps[0]),
        }, nil
    }).
    Build()
```

**Example - saving:**
```
Agent: save_checkpoint(
  summary="Implementing JWT auth - middleware done, need handler",
  completed=["Created auth types", "Implemented JWT middleware", "Added config"],
  next_steps=["Implement login handler", "Implement refresh endpoint", "Add tests"],
  status="paused"
)
Response: {"saved": true, "id": "checkpoint_abc123"}
```

**Example - resuming:**
```
Agent: resume_task("checkpoint_abc123")
Response: {
  "id": "checkpoint_abc123",
  "summary": "Implementing JWT auth - middleware done, need handler",
  "completed": ["Created auth types", "Implemented JWT middleware", "Added config"],
  "next_steps": ["Implement login handler", "Implement refresh endpoint", "Add tests"],
  "files_read": ["auth/types.go", "auth/middleware.go", "config/config.go"],
  "files_modified": ["auth/types.go", "auth/middleware.go"],
  "scratchpad": {
    "user_prefers": "Early returns, minimal error wrapping",
    "token_expiry": "15 minutes access, 7 days refresh - user confirmed"
  },
  "hint": "Continue with: Implement login handler"
}
```

**Token savings**: Avoids re-gathering context after interruption.

---

### 10. Design Token Awareness

For UI work, know the design system and use consistent values.

```go
// DesignSystem stores design tokens and patterns
type DesignSystem struct {
    mu sync.RWMutex

    // Core tokens
    Colors     map[string]string `json:"colors"`
    Spacing    map[string]string `json:"spacing"`
    Typography map[string]TypographyToken `json:"typography"`
    Shadows    map[string]string `json:"shadows"`
    Radii      map[string]string `json:"radii"`

    // Component patterns
    ComponentPatterns map[string]ComponentPattern `json:"component_patterns"`

    // Source files
    Sources    []string `json:"sources"`  // Where tokens are defined
}

type TypographyToken struct {
    FontFamily string `json:"font_family"`
    FontSize   string `json:"font_size"`
    FontWeight string `json:"font_weight"`
    LineHeight string `json:"line_height"`
}

type ComponentPattern struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Tokens      []string `json:"tokens"`  // Design tokens used
    Example     string   `json:"example"`
}

// Skill to get design tokens
var GetDesignTokensSkill = skills.NewSkill("get_design_tokens").
    Description("Get the project's design tokens. Use these values instead of hardcoding colors, spacing, etc.").
    Domain("ui").
    Keywords("design", "token", "color", "spacing", "theme").
    Priority(90).
    EnumParam("category", "Token category", []string{"colors", "spacing", "typography", "shadows", "radii", "all"}, false).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Category string `json:"category"`
        }
        json.Unmarshal(input, &params)

        if params.Category == "" || params.Category == "all" {
            return designSystem.GetAll(), nil
        }

        return designSystem.GetCategory(params.Category), nil
    }).
    Build()

// Skill to find token by value
var FindDesignTokenSkill = skills.NewSkill("find_design_token").
    Description("Find the design token name for a hardcoded value. Helps replace magic values with tokens.").
    Domain("ui").
    StringParam("value", "The value to find (e.g., '#3B82F6', '16px')", true).
    Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
        var params struct {
            Value string `json:"value"`
        }
        json.Unmarshal(input, &params)

        matches := designSystem.FindByValue(params.Value)

        if len(matches) == 0 {
            return map[string]any{
                "found": false,
                "suggestion": "No matching token found. Consider adding one or using a similar existing token.",
            }, nil
        }

        return map[string]any{
            "found":   true,
            "matches": matches,
        }, nil
    }).
    Build()
```

**Example - getting tokens:**
```
Agent: get_design_tokens(category="colors")
Response: {
  "colors": {
    "primary": "#3B82F6",
    "primary-hover": "#2563EB",
    "primary-light": "#DBEAFE",
    "secondary": "#6B7280",
    "error": "#EF4444",
    "error-light": "#FEE2E2",
    "success": "#10B981",
    "warning": "#F59E0B",
    "text": "#1F2937",
    "text-muted": "#6B7280",
    "background": "#FFFFFF",
    "background-secondary": "#F9FAFB",
    "border": "#E5E7EB"
  }
}
```

**Example - finding a token:**
```
Agent: find_design_token(value="#3B82F6")
Response: {
  "found": true,
  "matches": [
    {"category": "colors", "name": "primary", "value": "#3B82F6"}
  ]
}
Agent: "I should use 'colors.primary' instead of hardcoding '#3B82F6'"
```

**Token savings**: Ensures UI consistency, avoids hardcoded values that break themes.

---

### Agent-to-Technique Mapping

Each efficiency technique is applicable to specific agents based on their responsibilities:

```
┌─────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                              AGENT EFFICIENCY TECHNIQUE MATRIX                                           │
├────────────────────────┬────────┬──────────┬──────────┬──────────┬────────┬──────────┬─────────────────┤
│ Technique              │ Router │ Architect│ Engineer │ Inspector│ Tester │ Designer │ Knowledge RAGs  │
├────────────────────────┼────────┼──────────┼──────────┼──────────┼────────┼──────────┼─────────────────┤
│ Scratchpad Memory      │   -    │    ✓     │    ✓     │    ✓     │   ✓    │    ✓     │       -         │
│ Style Inference        │   -    │    -     │    ✓     │    ✓     │   ✓    │    -     │       -         │
│ Component Registry     │   -    │    -     │    ✓     │    -     │   -    │    ✓     │       -         │
│ Mistake Memory         │   -    │    -     │    ✓     │    ✓     │   ✓    │    -     │  Archivalist*   │
│ Diff Preview           │   -    │    -     │    ✓     │    -     │   -    │    ✓     │       -         │
│ User Preferences       │   ✓    │    ✓     │    ✓     │    -     │   -    │    ✓     │       -         │
│ File Snapshot Cache    │   -    │    -     │    ✓     │    ✓     │   ✓    │    -     │   Librarian*    │
│ Dependency Awareness   │   -    │    -     │    ✓     │    ✓     │   ✓    │    -     │   Academic*     │
│ Task Continuity        │   -    │    ✓     │    ✓     │    -     │   ✓    │    -     │       -         │
│ Design Token Awareness │   -    │    -     │    ✓     │    ✓     │   -    │    ✓     │       -         │
├────────────────────────┼────────┼──────────┼──────────┼──────────┼────────┼──────────┼─────────────────┤
│ TOTAL TECHNIQUES       │   1    │    3     │   10     │    6     │   6    │    5     │      3*         │
└────────────────────────┴────────┴──────────┴──────────┴──────────┴────────┴──────────┴─────────────────┘

* Knowledge RAG agents (Librarian, Archivalist, Academic) serve as SOURCES for some techniques:
  - Archivalist: SOURCE for Mistake Memory (provides historical error data)
  - Librarian: SOURCE for File Snapshot Cache (indexes code files)
  - Academic: SOURCE for Dependency Awareness (provides best practices)
```

#### Agent-Specific Rationale

**Router (Guide)**
- **User Preferences only** - Can route based on preferred workflow
- No code writing, no memory needs, no task state

**Architect**
- **Scratchpad** - Track requirements, decisions, blockers during planning
- **User Preferences** - Adapt verbosity, detail level to user style
- **Task Continuity** - Multi-step planning can be interrupted

**Engineer**
- **All 10 techniques** - Primary code writer benefits from everything
- Critical: Style Inference, Component Registry, Mistake Memory, Diff Preview

**Inspector**
- **Scratchpad** - Track issues found during review
- **Style Inference** - Must know style to review against
- **Mistake Memory** - Flag known anti-patterns
- **File Snapshot** - Efficient reading during review
- **Dependency Awareness** - Flag deprecated API usage
- **Design Tokens** - Flag raw values instead of tokens

**Tester**
- **Scratchpad** - Track test cases to cover
- **Style Inference** - Tests should match project style
- **Mistake Memory** - Know common failure patterns
- **File Snapshot** - Efficient test file reading
- **Dependency Awareness** - Mock dependencies correctly
- **Task Continuity** - Large test suites can be interrupted

**Designer**
- **Scratchpad** - Track design decisions, user feedback
- **Component Registry** - Know existing components to reuse
- **Diff Preview** - Preview UI changes
- **User Preferences** - Design style preferences
- **Design Tokens** - Primary user of design system

**Knowledge RAGs (Librarian, Archivalist, Academic)**
- Primarily serve as data SOURCES for other agents
- Archivalist indexes and retrieves historical mistakes
- Librarian provides cached file snapshots
- Academic provides dependency best practices

---

### Token Savings Summary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    AGENT EFFICIENCY TECHNIQUES - SAVINGS                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   Technique                    │ Primary Benefit           │ Token Savings │
│   ════════════════════════════╪═══════════════════════════╪═══════════════│
│   Scratchpad Memory           │ Context preservation      │     ~15%     │
│   Style Inference             │ Consistent code style     │     ~10%     │
│   Component Registry          │ No duplicate components   │     ~20%     │
│   Mistake Memory              │ Avoid known pitfalls      │     ~15%     │
│   Diff Preview                │ Catch errors early        │      ~5%     │
│   User Preferences            │ Match expectations        │     ~10%     │
│   File Snapshot Cache         │ Avoid re-reading          │     ~10%     │
│   Dependency Awareness        │ Use existing packages     │      ~5%     │
│   Task Continuity             │ Resume without re-context │     ~20%     │
│   Design Token Awareness      │ Consistent UI             │      ~5%     │
│   ──────────────────────────────────────────────────────────────────────── │
│                                                                             │
│   Note: Savings are not additive - they apply to different scenarios.     │
│   Estimated combined impact: 15-25% additional savings on top of          │
│   VectorGraphDB + XOR optimizations.                                       │
│                                                                             │
│   TOTAL ESTIMATED SAVINGS:                                                  │
│   • Baseline:                     0% savings                               │
│   • + Intent Cache:              67% savings                               │
│   • + VectorGraphDB:             75% savings                               │
│   • + XOR Internal:              80% savings                               │
│   • + Agent Efficiency:          85-88% savings                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Implementation Guide

### Session Management
- `core/session/session.go`: Session data model
- `core/session/manager.go`: Session lifecycle management
- `core/session/context.go`: Session context (isolated state)
- `core/session/snapshot.go`: Session preservation/restoration
- `core/session/manager_test.go`: Session manager tests

### Guide and Bus
- `agents/guide/guide.go`: Central routing, session-aware correlation
- `agents/guide/bus.go`: Bus interface and topic constants
- `agents/guide/channel_bus.go`: In-process bus (sharded for multi-session scalability)

### Routing and Classification
- `agents/guide/agent_router.go`: Tiered routing (DSL → cache → LLM)
- `agents/guide/classification.go`: LLM-based routing
- `agents/guide/route_cache.go`: Cached route results

### Resilience
- `agents/guide/retry.go`: Retry queue
- `agents/guide/dead_letter.go`: Dead letter queue
- `agents/guide/circuit_breaker.go`: Circuit breaker
- `agents/guide/health.go`: Health monitoring

### Message Envelope
- `core/messaging/message.go`: Envelope with session_id field

### Skills
- `core/skills/skills.go`: Skill registry and loading
- `core/skills/loader.go`: Progressive skill loading
- `core/skills/hooks.go`: Hook registry and execution

### Agents (to be implemented)
- `agents/academic/`: External knowledge RAG
- `agents/architect/`: Planning and coordination
- `agents/orchestrator/`: DAG execution (session-aware)
- `agents/engineer/`: Task execution (session-scoped)
- `agents/designer/`: UI/UX implementation (components, styling, accessibility, design systems)
- `agents/librarian/`: Local codebase RAG
- `agents/archivalist/`: Historical RAG (session-aware storage, cross-session queries)
- `agents/inspector/`: Code validation
- `agents/tester/`: Test planning and execution

---

## Summary

Sylk combines **DAG-based orchestration** with a **Guide-centered universal routing bus** and **LLM skill planning**.

Key architectural principles:

1. **Guide routes ALL messages** - No direct agent-to-agent communication
2. **Sessions are the unit of isolation** - Context pollution prevention
3. **Shared historical knowledge** - Cross-session learning via Archivalist
4. **Architect is the user's primary interface** - All status, plans, and questions flow through Architect
5. **Engineers are invisible to users** - Managed by Orchestrator, clarifications route through Architect
6. **Three knowledge RAGs** - Librarian (local code), Archivalist (history), Academic (external)
7. **Quality loop** - Inspector validates, Tester tests, fixes loop back through Architect
8. **Progressive skill disclosure** - Skills loaded on demand to minimize tokens
9. **Hooks for extensibility** - Pre/post hooks for prompts and tool calls

The system is designed for large-scale concurrency across many sessions and subagents, with strong resilience, operational visibility, and token efficiency.
