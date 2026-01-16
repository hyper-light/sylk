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

---

## Agent Roles, Skills, Tools, and Hooks

### Complete Agent Summary

| Agent | Role | User Interaction | Primary Responsibility |
|-------|------|------------------|------------------------|
| **Academic** | External knowledge RAG | DIRECT (triggered by research queries) | Research papers, best practices, external references |
| **Architect** | Planning & coordination | PRIMARY (default agent) | Abstract → Concrete, DAG design, user coordination |
| **Orchestrator** | Workflow execution | NONE (invisible) | Execute DAGs, manage Engineers, status propagation |
| **Engineer** | Task execution | NONE (invisible) | Code writing, problem solving |
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
