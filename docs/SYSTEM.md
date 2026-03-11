# Sylk System Architecture

## Overview

Sylk's runtime architecture mirrors a Kubernetes cluster. Individual agents are containers, pipelines are pods, DAG layers are nodes, the Guide is the mandatory service mesh, and the storage layer (VectorGraphDB + Bleve + Archivalist) is etcd. This document defines the complete mapping, the design invariants it produces, and the implementation consequences for every subsystem.

This is not a loose analogy. The Kubernetes model is the **design target** — every architectural decision should be evaluated against the properties that make K8s robust: blast-radius isolation, declarative desired-state reconciliation, layered storage with clear ownership, mandatory traffic interception, and admission control at every boundary.

---

## System Topology

```
┌──────────────────────────── CONTROL PLANE ────────────────────────────────┐
│                                                                           │
│  ┌───────────────────────────────────────────────────────────────────┐    │
│  │                    GUIDE (Network Fabric)                         │    │
│  │                                                                   │    │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────────┐  │    │
│  │  │  Ingress Layer   │  │  Mesh Data Plane │  │  Signal Bus      │  │    │
│  │  │  (classify,      │  │  (audit, inject,  │  │  (pause/resume/  │  │    │
│  │  │   validate,      │  │   rate limit,     │  │   cancel)        │  │    │
│  │  │   route)         │  │   permission)     │  │                  │  │    │
│  │  └─────────────────┘  └─────────────────┘  └──────────────────┘  │    │
│  └────────────────────────────┬──────────────────────────────────────┘    │
│                               │                                           │
│  ┌────────────┐  ┌──────────────────────┐  ┌────────────────┐  ┌────────┐ │
│  │  Architect  │  │     Orchestrator     │  │ LLM Providers   │  │Session │ │
│  │ (ctrl-mgr)  │  │  (kubelet + cAdvisor │  │ (cloud-ctrl-mgr)│  │Manager │ │
│  │             │  │   + node controller) │  │                 │  │        │ │
│  └──────┬─────┘  └──────────┬───────────┘  └────────────────┘  └────────┘ │
│         │                   │                                              │
│         │           ┌───────┴────────────────────────────┐                 │
│         │           │  Data Plane (Orchestrator-owned)    │                 │
│         │           │  SQLite + WAL + BufferRegistry      │                 │
│         │           │  (Ristretto hot → ring buffer warm  │                 │
│         │           │   → SQLite cold)                    │                 │
│         │           └────────────────────────────────────┘                 │
│         │                │                                                │
│  ┌──────┴────────────────┴────────────────────────────────────────────┐   │
│  │           etcd (VectorGraphDB + Bleve + Archivalist)               │   │
│  │           Persistent state: knowledge, history, decisions          │   │
│  └────────────────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────────────────┘

┌─────────────────── DaemonSet / StatefulSet ───────────────────────────────┐
│                                                                           │
│  ┌──────────────┐    ┌───────────────┐    ┌──────────────┐                │
│  │  Librarian    │    │  Archivalist   │    │  Academic     │               │
│  │  (codebase    │    │  (historical   │    │  (external    │               │
│  │   RAG)        │    │   RAG)         │    │   RAG)        │               │
│  │  StatefulSet  │    │  StatefulSet   │    │  StatefulSet  │               │
│  └──────────────┘    └───────────────┘    └──────────────┘                │
│                                                                           │
│  ┌──────────────────┐    ┌──────────────────┐                             │
│  │ Global Inspector  │    │  Global Tester    │                            │
│  │ (admission ctrl)  │    │  (integration     │                            │
│  │  DaemonSet        │    │   gate)           │                            │
│  │                   │    │  DaemonSet        │                            │
│  └──────────────────┘    └──────────────────┘                             │
└───────────────────────────────────────────────────────────────────────────┘

┌──────────────── DAG Layer 0 (Node) ───────────────────────────────────────┐
│                                                                           │
│  ┌──── Pipeline A (Pod) ────┐    ┌──── Pipeline B (Pod) ────┐            │
│  │  VFS Overlay (PVC)       │    │  VFS Overlay (PVC)       │            │
│  │  ┌───────┐ ┌────┐ ┌───┐ │    │  ┌───────┐ ┌────┐ ┌───┐ │            │
│  │  │ Insp. │ │Test│ │Eng│ │    │  │ Insp. │ │Test│ │Des│ │            │
│  │  │(probe)│ │(pr)│ │   │ │    │  │(probe)│ │(pr)│ │   │ │            │
│  │  └───────┘ └────┘ └───┘ │    │  └───────┘ └────┘ └───┘ │            │
│  │      PipelineBus         │    │      PipelineBus         │            │
│  └──────────────────────────┘    └──────────────────────────┘            │
│                                                                           │
│  ═══════════════════ LAYER GATE ═════════════════════════════════════════ │
│  Phase 1: OT Merge (reconcile VFS overlays via OT Engine)                │
│  Phase 2: Global Inspector (validate against full DAG criteria)          │
│  Phase 3: Global Tester (integration tests on merged output)             │
│  Pass → Layer 1 begins    Fail → feedback to Architect                   │
└───────────────────────────────────────────────────────────────────────────┘
                    │
                    ▼
┌──────────────── DAG Layer 1 (Node) ───────────────────────────────────────┐
│  ┌──── Pipeline C (Pod) ────┐    ┌──── Pipeline D (Pod) ────┐            │
│  │  ...                     │    │  ...                      │            │
│  └──────────────────────────┘    └──────────────────────────┘            │
│                                                                           │
│  ═══════════════════ LAYER GATE ═════════════════════════════════════════ │
└───────────────────────────────────────────────────────────────────────────┘
                    │
                    ▼
         Physical Filesystem (PersistentVolume)
         Read-only until layer gate commits
```

---

## Design Invariant: All Traffic Through the Guide

The Guide is not merely an ingress controller. It is the **entire network fabric**. There is no agent-to-agent communication that does not traverse the Guide. This is Istio in STRICT mTLS mode — every packet, even between co-located services, passes through the mesh data plane.

```
Normal routing:   Agent → Guide [audit → inject → classify → route → rate limit] → Target
Direct consult:   Agent → Guide [audit → inject →            route → rate limit] → Target
                                                     ▲
                                                skip classify
                                               (target pre-known)
```

The "Direct Consultation Protocol" is a **routing hint**, not a network bypass. When an Architect calls `consult_librarian()`, the message still flows through the Guide. The only difference is that the Guide skips the LLM classification step because the target is already known. Every other mesh function executes:

- **Audit logging** — every inter-agent message recorded with monotonic sequence and chaining hash
- **Session context injection** — SessionID, correlation chains, vector clock timestamps
- **Signal interception** — pause/resume/cancel signals can hold in-flight messages
- **Rate limiting** — even direct consultations count against LLM and throughput budgets
- **Permission enforcement** — AgentRole checked against target's access policy

This produces a critical system property: **the Guide is the single point where the entire communication graph is observable and enforceable**. No agent can operate outside the system's visibility.

### External Traffic (Ingress)

All external data enters through the Guide's ingress layer:

| Source | Consumer | Ingress Path |
|---|---|---|
| User prompts | Guide → any agent | Session-authenticated, classified, routed |
| Package registries | Librarian | Domain-allowlisted via NetworkProxy |
| Research APIs | Academic | Domain-allowlisted via NetworkProxy |
| Code repositories | Librarian | Domain-allowlisted, size-bounded |
| Web content | Academic | Cached, domain-filtered, sanitized |

External traffic receives the same treatment as internal traffic: audit, inject, validate, route. The NetworkProxy enforces per-agent domain allowlists on egress. An Engineer cannot hit arbitrary URLs. A Librarian can hit package registries. An Academic can hit research databases. All logged.

---

## Control Plane

### Guide = kube-apiserver + Service Mesh Data Plane

The Guide serves as both the API server (external entry point) and the mandatory service mesh (internal traffic interception). Every request — user input, inter-agent consultation, external data ingestion — flows through it.

**Responsibilities:**

| Function | K8s Equivalent |
|---|---|
| Intent classification | Path-based routing rules |
| Session context injection | Header injection (mutating admission) |
| Permission enforcement | RBAC + validating admission webhook |
| Rate limiting | API priority and fairness |
| Signal broadcasting | Kubernetes Events |
| Audit logging | Audit policy (RequestResponse level) |
| Streaming relay | Watch streams |

**The Guide is stateless.** It maintains runtime subscriptions and routing caches but holds no persistent state. All durable state lives in etcd (VectorGraphDB + Bleve + Archivalist). If the Guide restarts, it rebuilds from subscriptions. This mirrors kube-apiserver's stateless design backed by etcd.

### Architect = kube-controller-manager

The Architect is the reconciliation engine. It receives **desired state** (user intent) and produces the **spec** (DAG) that the scheduler executes.

```
User intent (desired state)
    │
    ▼
Architect decomposes request
    ├── Query Librarian (codebase patterns)     ← via Guide
    ├── Query Archivalist (past decisions)      ← via Guide
    ├── Query Academic (best practices)         ← via Guide
    ├── Clarify with user (last resort)         ← via Guide
    │
    ▼
DAG produced (spec)
    │
    ▼
Submitted to Orchestrator (scheduler)
```

Like kube-controller-manager running multiple sub-controllers (deployment-controller, replicaset-controller, job-controller), the Architect runs multiple sub-processes:

- **Request decomposition** — extract explicit requirements, implicit assumptions, ambiguities
- **Context gathering** — consult knowledge agents for grounding
- **Technical pushback** — challenge flawed approaches with evidence
- **Workflow construction** — build DAG with topological order, success criteria, risk mitigation
- **Execution oversight** — monitor Orchestrator signals, handle feedback from global validation

### Orchestrator = kubelet + cAdvisor + Node Controller

The Orchestrator is the **entire node runtime agent** — the execution substrate where pipelines are born, monitored, persisted, and terminated. It is not merely a scheduler. It owns the complete pipeline lifecycle from DAG receipt through execution, health monitoring, state persistence, crash recovery, and escalation.

The Guide is purely network fabric (routing, audit, session injection). The Orchestrator is where pipelines live and die.

**Subsystem mapping:**

| Orchestrator Subsystem | K8s Equivalent | Responsibility |
|---|---|---|
| DAGBridge | kubelet pod lifecycle manager | Execute/Cancel/Modify DAGs, track active pipelines |
| dag.Scheduler | Built-in node scheduler | Topological layer-by-layer execution within each DAG |
| BusNodeDispatcher | CRI (Container Runtime Interface) | Dispatch tasks to pipeline agents via event bus |
| HealthMonitor | kubelet probes + cAdvisor | Deterministic 10s health checks, error rate tracking |
| HealthCache (Ristretto) | cAdvisor metrics cache | Hot cache for health check results |
| BufferRegistry | Pod status cache + emptyDir | 3-tier persistence: Ristretto hot → ring buffer warm → SQLite cold |
| Store (SQLite) | Node local storage + status reporting | DAG executions, task updates, pipeline state, revisions |
| OrchestratorJournal (WAL) | kubelet checkpoint/restore | Crash recovery, incomplete DAG detection |
| LLM Loop (Gemini Flash) | Node Problem Detector | Intelligent event analysis with tool use (18 skills) |
| escalate_to_architect | Node condition reporting | Critical health transitions → Architect for replanning |

**Data plane architecture:**

```
┌─── Orchestrator Data Plane ──────────────────────────────────────────────┐
│                                                                          │
│  Inbound Events                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐     │
│  │  pipeline.update.*    →  BufferRegistry.Push()                   │     │
│  │  pipeline.state.*     →  Store.UpsertPipelineState()             │     │
│  │  tasks.dispatch       →  State.Tasks + HealthMonitor             │     │
│  │  tasks.complete       →  State.Tasks + Archivalist event         │     │
│  │  tasks.failed         →  State.Tasks + HealthMonitor + Archivalist│    │
│  │  dag.execute          →  DAGBridge.Execute()                     │     │
│  │  dag.modify           →  DAGBridge.Modify()                      │     │
│  │  dag.cancel           →  DAGBridge.Cancel()                      │     │
│  └─────────────────────────────────────────────────────────────────┘     │
│                                                                          │
│  Persistence Stack                                                       │
│  ┌──────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │ Ristretto │  │  Ring Buffer  │  │   SQLite     │  │    WAL       │    │
│  │  (hot)    │→ │  (warm)       │→ │  (cold)      │  │  (journal)   │    │
│  │  10s TTL  │  │  per-task     │  │  dag_exec    │  │  crash       │    │
│  │  latest   │  │  circular     │  │  task_update  │  │  recovery    │    │
│  │  entries  │  │  cap derived  │  │  pipeline_st  │  │  7d retain   │    │
│  └──────────┘  └──────────────┘  └──────────────┘  └──────────────┘    │
│                                                                          │
│  DAG Execution Engine                                                    │
│  ┌──────────────────────────────────────────────────────────────────┐    │
│  │  DAGBridge                                                        │    │
│  │  ├── dag.Scheduler (≤4 concurrent DAGs, ≤8 concurrency/DAG)      │    │
│  │  ├── BusNodeDispatcher (routes nodes → agent bus topics)          │    │
│  │  ├── dagEventForwarder (scheduler events → WAL + SQLite)          │    │
│  │  ├── RecoverFromWAL (crash recovery on startup)                   │    │
│  │  └── ActiveDAGMeta (plan ID, revision, cancel func, start time)   │    │
│  └──────────────────────────────────────────────────────────────────┘    │
│                                                                          │
│  Health Monitoring                                                       │
│  ┌──────────────────────────────────────────────────────────────────┐    │
│  │  HealthMonitor (deterministic, every 10s)                         │    │
│  │  ├── Per-agent: heartbeat, error rate, missed beats, active tasks │    │
│  │  ├── Alerts: timeout, heartbeat_lost, high_error_rate, storm      │    │
│  │  ├── Levels: healthy → degraded → unhealthy → critical            │    │
│  │  ├── Forward results → Archivalist (history)                      │    │
│  │  └── Auto-escalate critical transitions → Architect               │    │
│  └──────────────────────────────────────────────────────────────────┘    │
│                                                                          │
│  LLM Loop (Gemini Flash — optional, deterministic fallback)              │
│  ┌──────────────────────────────────────────────────────────────────┐    │
│  │  Event processing: 256-event buffer → batch (500ms window) → LLM  │    │
│  │  Tool use: 18 skills (query_task, execute_dag, modify_dag,        │    │
│  │            cancel_dag, escalate_to_architect, query_buffer,       │    │
│  │            query_agent_health, broadcast_status, ...)             │    │
│  │  Fallback: critical events auto-escalate without LLM              │    │
│  │  Startup: 5s grace period discards events during initialization   │    │
│  └──────────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────┘
```

**Orchestrator skills (18 registered):**

| Category | Skills | Purpose |
|---|---|---|
| Task/Workflow | query_task, query_workflow, push_status, generate_summary | Point-in-time state queries |
| Failure handling | report_failure, submit_task_event, archivalist_request | Error recording + historical pattern queries |
| DAG execution | execute_dag, cancel_dag, modify_dag, query_dag_status | Pipeline lifecycle control |
| Monitoring | query_agent_health, query_health_history, query_buffer, query_pipeline_state | Health + progress observability |
| Communication | escalate_to_architect, broadcast_status, read_plan_file | Cross-agent coordination |

**Key design properties:**

1. **Persists everything.** Every pipeline update, health check, DAG execution, revision, task event, and state transition is written to at least one of: Ristretto cache, ring buffer, SQLite, or WAL journal. Nothing is lost.

2. **Crash-recoverable.** The WAL journal enables incomplete DAG detection on startup. `RecoverFromWAL` marks crashed DAGs as failed so the Architect can re-submit.

3. **Dual-mode intelligence.** When a Google provider is available, the LLM loop (Gemini Flash) analyzes event batches and uses tool calling to take action. When unavailable, deterministic fallback auto-escalates critical events. The system never depends on LLM availability.

4. **Health monitoring is deterministic.** The HealthMonitor runs on a fixed 10s cycle independent of the LLM loop. It detects heartbeat misses, error rate spikes, transient storms, and task timeouts. Critical health transitions auto-escalate to the Architect via the Guide.

5. **Mid-flight DAG modification.** The Architect can add/remove nodes from a running DAG via `modify_dag`. Modifications are WAL-journaled and revision-tracked in SQLite.

**LLM queue priority (QoS classes):**

| QoS Class | K8s Equivalent | Agents | Behavior |
|---|---|---|---|
| Guaranteed | User-interactive queue | Guide, Architect, Orchestrator, Librarian, Archivalist, Academic | Unbounded, absolute priority, always served |
| Burstable | Pipeline queue | Engineer, Designer, Inspector, Tester (pipeline-scoped) | Bounded by N_CPU_CORES, preemptible by Guaranteed |
| BestEffort | Background | Indexing, GC, periodic maintenance | Runs when capacity available |

### LLM Provider Layer = cloud-controller-manager

Manages external compute resources (LLM inference) behind a uniform interface:

```go
type ProviderAdapter interface {
    Name() string
    SupportedModels() []ModelInfo
    Complete(ctx, req *CompletionRequest) (*CompletionResponse, error)
    Stream(ctx, req *CompletionRequest) (<-chan StreamChunk, error)
    CountTokens(messages []Message) (int, error)
    MaxContextTokens(model string) int
    HealthCheck(ctx context.Context) error
}
```

Credential resolution follows a chain (like cloud credential providers):
1. Environment variable (ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY)
2. Config file (~/.sylk/credentials.yaml, encrypted at rest)
3. System keychain (future)

Usage tracking attributes every API call to SessionID, PipelineID, TaskID, AgentID — enabling per-session cost accounting and budget enforcement.

---

## Persistent State (etcd)

The persistent state layer maps to etcd — the single source of truth backing the control plane.

### VectorGraphDB (Semantic State)

Single SQLite file (`vector.db`) storing vector embeddings, HNSW indices, graph edges, and metadata. Domain-partitioned:

| Domain | Owner | Content |
|---|---|---|
| DomainCode (0) | Librarian | Codebase embeddings, file structure, API surfaces |
| DomainHistory (1) | Archivalist | Decision embeddings, failure patterns, outcomes |
| DomainAcademic (2) | Academic | Research embeddings, best practices |
| DomainPlanning (3) | Architect | Plan embeddings, decomposition patterns |
| DomainExecution (4) | Engineer | Implementation patterns |
| DomainUI (5) | Designer | Component patterns, design tokens |
| DomainQuality (6) | Inspector | Quality criteria, validation patterns |
| DomainTesting (7) | Tester | Test patterns, coverage models |
| DomainWorkflow (8) | Orchestrator | Workflow execution patterns |
| DomainRouting (9) | Guide | Routing patterns, classification history |

**Concurrency control:**
- Layer 1: HNSW Snapshot Isolation (copy-on-write, SeqNum-gated)
- Layer 2: Optimistic Concurrency Control (versioned reads, validate-on-commit)

### Bleve (Full-Text Search Index)

File-based Scorch index (`documents.bleve/`). Code-aware tokenization (CamelCase, snake_case). Document types: source_code, markdown, config, llm_prompt, llm_response, web_fetch, note, git_commit.

### Archivalist (Historical Record)

The Archivalist is etcd's revision history with compaction. It stores:

- **TaskCompletionRecord** — what succeeded, how, duration
- **TaskFailureRecord** — what failed, why, which dependents were affected
- **TaskCancelRecord** — why cancelled, upstream cause
- **OrchestratorHandoffState** — context snapshots at handoff boundaries

**Session isolation:** Session-scoped writes, cross-session reads for promoted entries. Like etcd namespacing with cross-namespace read access for shared resources.

### Hybrid Search (RRF Fusion)

All knowledge queries run parallel Bleve + VectorDB searches, fused via Reciprocal Rank Fusion:

```
SearchCoordinator
    ├── Bleve query (lexical, code-aware tokenization)
    ├── VectorDB HNSW search (semantic, domain-filtered)
    └── RRF fusion: 0.4 × lexical + 0.6 × semantic
```

---

## Node-Level Runtime

### DAG Layer = Node

A topological layer in the DAG is the execution boundary where multiple pipelines run in parallel. The layer gate enforces a scheduling barrier — no pipeline in Layer N+1 starts until all pipelines in Layer N complete and pass global validation.

```
Layer 0: [engineer] JWT utilities, [engineer] middleware         (parallel)
Layer 1: [engineer] Login handler, [engineer] Logout handler     (parallel, depends on Layer 0)
Layer 2: [engineer] Update routes                                (depends on Layer 1)
Layer 3: [tester] Write integration tests                        (depends on all above)
```

### Pipeline = Pod

A pipeline is a group of co-located agents sharing a PipelineBus (localhost network) and a VFS overlay (shared volume). Pipelines are ephemeral — created for a task, destroyed on completion.

**Pod structure:**

```
┌─── Pipeline (Pod) ──────────────────────────────────────────┐
│                                                              │
│  VFS Overlay (PVC)                                           │
│  ┌──────────────────────────────────────────────────────┐    │
│  │  preChangeVFS (frozen snapshot at S0)                 │    │
│  │  changeVFS (working copy, CoW modifications)          │    │
│  │  pendingOps (uncommitted operations for OT)           │    │
│  └──────────────────────────────────────────────────────┘    │
│                                                              │
│  Agents (Containers)                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐       │
│  │ Inspector │  │  Tester  │  │  Engineer / Designer │       │
│  │ (init +   │  │ (sidecar)│  │  (main container)    │       │
│  │  sidecar) │  │          │  │                      │       │
│  └──────────┘  └──────────┘  └──────────────────────┘       │
│                                                              │
│  PipelineBus (Pod network / localhost)                       │
│  ┌──────────────────────────────────────────────────────┐    │
│  │  inspectorFeedback  chan  (buffer-1)                   │    │
│  │  testerFeedback     chan  (buffer-1)                   │    │
│  │  workerDone         chan  (buffer-1)                   │    │
│  │  inspectorDone      chan  (buffer-1)                   │    │
│  │  testerDone         chan  (buffer-1)                   │    │
│  │  userMessages       chan  (buffer-4)                   │    │
│  └──────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
```

### Agent = Container

Each agent within a pipeline is an isolated container with:

- **Resource limits** — context window budget, goroutine budget (GoroutineBudget with soft/hard limits)
- **Restart policy** — on failure, the pipeline can restart just that agent without killing siblings
- **Health probes** — heartbeat checks (30s intervals), timeout detection (30min default)
- **Lifecycle hooks** — Start(), Stop(), Close() with graceful shutdown

**Container types within a pod:**

| Container Role | Agent | Behavior |
|---|---|---|
| Init container | Inspector.DefineCriteria() | Runs first. Must complete before main containers start. Defines acceptance criteria. |
| Main container | Engineer or Designer | The primary workload. Implements the task. |
| Sidecar (validation) | Inspector + Tester | Run in parallel during validation phase. Check the main container's output. |

### TDDExecutor = Container Runtime Shim (containerd-shim)

The TDDExecutor manages agent lifecycle **within a single pipeline** — it is not the kubelet (the Orchestrator is). It is analogous to the container runtime shim that manages container processes within a pod on behalf of the kubelet.

- Ensures phases run in order (define criteria → create tests → implement → validate)
- Monitors phase health (timeouts, errors)
- Reports status to Orchestrator via PipelineBus / event bus (like a shim reporting to kubelet)
- Handles restart on validation failure (loop back)
- Manages the GoroutineScope for tracked, bounded goroutine execution
- Wraps `PipelineRunner` (lifecycle state machine) as the execution backbone

### AgentFactory = Container Runtime Interface (CRI)

The factory creates and configures agent instances:

- `CreateInspector()` — configure with PipelineInternal mode
- `CreateTester()` — configure with task-scoped test requirements
- `CreateWorker(WorkerType)` — instantiate Engineer or Designer adapter

Like containerd implementing the CRI, the factory abstracts agent creation behind the WorkerAgent interface.

---

## Two-Tier Validation

Validation operates at two distinct levels with different scope, frequency, and feedback targets.

### Pipeline-Scoped (Per-Task) = Liveness/Readiness Probes

Inspector and Tester instances **inside the pod**, focused on the individual task's acceptance criteria.

```
Scope:       Single task within a single pipeline
Frequency:   Every TDD loop iteration
Question:    "Does this function compile? Do its unit tests pass?"
Feedback:    → Worker agent (same pod) via PipelineBus
On failure:  Loop back within the pipeline (restart container)
```

This is a readiness probe. If the probe fails, the container is restarted (the TDD loop iterates). The pod itself is not killed — only the failing phase re-executes.

### Global (Per-Layer / Full-DAG) = Admission Control

Inspector and Tester instances that sit **outside any pod**, at the layer gate, validating the combined output of all pipelines in the layer against the holistic DAG requirements.

```
Scope:       All tasks in the DAG layer, evaluated against full DAG requirements
Frequency:   Once per layer completion (after OT merge)
Question:    "Do these independently-implemented components work together?
              Does the auth middleware integrate with the login handler?
              Are cross-cutting concerns (logging, error handling) consistent?"
Feedback:    → Architect (control plane) for DAG replanning
On failure:  Architect may add corrective tasks, modify the DAG, or re-plan
```

This is a ValidatingAdmissionWebhook. It intercepts the "commit" at the layer boundary and rejects if cross-cutting concerns fail — even if every individual pod reported healthy.

**The global Inspector and Tester are DaemonSet workloads** — long-lived, not scoped to any pipeline, always available. They are cluster-level admission infrastructure, not application workloads.

### Layer Gate Sequence

```
All pipelines in Layer N complete
         │
         ▼
┌─ Phase 1: OT Merge ──────────────────────────────────────────────┐
│  Reconcile all pipeline VFS overlays into unified layer output    │
│  via OT Engine. AST-targeted operations ensure stability across   │
│  structural changes (line-shift immune).                          │
└──────────────────────┬────────────────────────────────────────────┘
                       │
                       ▼
┌─ Phase 2: Global Inspector ───────────────────────────────────────┐
│  Validate merged output against FULL DAG criteria:                │
│  • Cross-cutting architectural consistency                        │
│  • Constraint satisfaction across all tasks in the layer          │
│  • Quality gate thresholds (coverage, complexity, security)       │
│  • Integration with output from prior layers                      │
└──────────────────────┬────────────────────────────────────────────┘
                       │
                       ▼
┌─ Phase 3: Global Tester ──────────────────────────────────────────┐
│  Run integration tests on merged output:                          │
│  • Cross-component interaction tests                              │
│  • Regression against full codebase                               │
│  • Coverage against complete requirement set                      │
│  • End-to-end scenarios spanning multiple tasks                   │
└──────────────────────┬────────────────────────────────────────────┘
                       │
                Pass? ─┼─ No → Feedback to Architect (webhook rejection)
                       │       Architect may replan, add corrective tasks,
                       │       or modify the DAG
                       ▼
               Layer N+1 begins
```

### Validation Hierarchy Summary

```
                    Scope           Frequency          Feedback Target
                    ─────           ─────────          ───────────────
Pipeline Insp/Test  Single task     Every TDD loop     Worker (same pod)
Global Inspector    Full DAG layer  Every layer gate    Architect (control plane)
Global Tester       Full DAG layer  Every layer gate    Architect (control plane)
```

---

## Storage Architecture

### VFS Layering = PersistentVolume + PersistentVolumeClaim

The filesystem follows a layered model analogous to K8s persistent storage:

| K8s Concept | Sylk Equivalent | Description |
|---|---|---|
| PersistentVolume | Physical filesystem | The project directory on disk. Exists independent of any pipeline. Read-only to agents until commit. |
| PersistentVolumeClaim | PipelineVFS overlay | Per-pipeline claim on the filesystem. Copy-on-write overlay forked from baseline S0. Multiple claims can bind to the same volume with isolation. |
| StorageClass | VFS configuration | CoW semantics, staging directory, MVCC settings, cleanup policy. preChangeVFS = ReadOnlyMany. changeVFS = ReadWriteOnce. |
| CSI Driver | Central Version Store (CVS) | The storage driver implementing versioned I/O. MVCC, content-addressable blobs, WAL for crash recovery, OT merge engine. |

### Central Version Store (CVS)

The CVS is the CSI driver — the interface between VFS abstractions and the underlying filesystem.

```
CVS Responsibilities:
    ├── MVCC versioning (FileVersion DAG per file)
    ├── Content-addressable blob storage (SHA-256 deduplication)
    ├── Write-ahead log (crash recovery)
    ├── Vector clock (causal ordering across sessions/pipelines)
    ├── Per-file commit locks (serialized writes)
    ├── Pipeline lifecycle (BeginPipeline, CommitPipeline, RollbackPipeline)
    └── OT merge (ThreeWayMerge with ConflictResolver)
```

### PipelineVFS (Dual VFS)

Each pipeline maintains two virtual filesystems:

```
preChangeVFS (frozen)     — Snapshot at task start (S0)
                           Immune to external changes during work
                           Baseline for diff computation

changeVFS (working)       — Copy-on-write modifications
                           All agent writes go here
                           Tracks pendingOps for OT

Read path:  changeVFS → fallthrough to preChangeVFS → fallthrough to physical FS
Write path: Always to changeVFS (physical FS never written directly)
```

### Operational Transformation (OT) Engine

OT handles concurrent modifications **across parallel pipelines** — not within a single pipeline. When multiple pipelines in the same DAG layer modify overlapping files, OT transforms their operations for convergence at the layer gate.

```go
type OTEngine interface {
    Transform(op1, op2 *Operation) (*Operation, error)
    TransformBatch(ops1, ops2 []*Operation) ([]*Operation, error)
    DetectConflict(op1, op2 *Operation) *Conflict
    CanMergeAutomatically(op1, op2 *Operation) bool
}
```

**Why AST-targeted operations:**

```
Line-based (BREAKS under concurrent modification):
    Pipeline A: "Modify lines 50-60"
    Pipeline B: "Insert 20 lines at line 10"
    → Pipeline A's target is now wrong (should be 70-80)

AST-based (STABLE across structural changes):
    Pipeline A: "Modify func:HandleRequest.body.if[0]"
    Pipeline B: "Insert func:NewHelper before func:HandleRequest"
    → Pipeline A's target unchanged — still func:HandleRequest.body.if[0]
```

Operations use AST node paths as primary targets with character offsets as fallback for non-parseable files. This makes OT transforms stable even when pipelines insert or delete large blocks of code.

**Conflict types and resolution:**

| Conflict Type | Description | Resolution |
|---|---|---|
| OverlappingEdit | Both pipelines edit the same AST node | Route through Guide to user |
| DeleteEdit | One pipeline deletes what another edited | Route through Guide to user |
| MoveEdit | One pipeline moves what another edited | Auto-resolve if target still valid |
| SemanticConflict | AST-level type mismatch or broken references | Route through Guide to user |

**AutoResolvePolicy:** None (always prompt), KeepNewest, KeepOldest, KeepBoth. Semantic conflicts always prompt.

---

## Workload Types

### Pipeline (Job)

Run-to-completion workload. Created for a task, executes the TDD loop, terminates.

```
Analogous to: Kubernetes Job
Properties:
    backoffLimit     = MaxLoops (TDD loop iterations before failure)
    activeDeadline   = PipelineTimeout
    completions      = 1 (single completion required)
    parallelism      = 1 (sequential phases within the pipeline)
```

### Variant Group (Deployment + ReplicaSet)

Multiple parallel pipelines exploring alternative implementations from the same baseline.

```
Analogous to: Kubernetes Deployment with N replicas
Properties:
    replicas         = Number of variants (original + alternatives)
    strategy         = Recreate (not rolling — all run in parallel, one selected)
    selector         = VariantGroupID
    startingPoint    = S0 (shared git baseline)

Variant lifecycle:
    RUNNING → READY → SELECTED | DISCARDED | CANCELLED | FAILED

Selection invariants:
    • NO auto-select (timeout may auto-CANCEL, never auto-SELECT)
    • Atomic: one selected, all others discarded simultaneously
    • Blocking: next DAG layer waits for explicit user selection
    • Mandatory diff presentation: side-by-side, per-file, per-variant
```

### Knowledge RAGs (StatefulSet)

Long-lived, stateful workloads with persistent identity and ordered startup.

```
Analogous to: Kubernetes StatefulSet
Properties:
    replicas         = 1 per type (Librarian-0, Archivalist-0, Academic-0)
    podManagementPolicy = OrderedReady (startup indexer must complete)
    volumeClaimTemplates = domain-partitioned VectorDB + Bleve storage

Identity:
    Librarian    → "What EXISTS?" (current codebase state)
    Archivalist  → "What was DONE?" (past decisions, failures)
    Academic     → "What CAN be done?" (world knowledge, standards)
```

| RAG | Model | Context | Eviction Strategy | Session Behavior |
|---|---|---|---|---|
| Librarian | Sonnet 4.5 (1M) | Tiered (recency) | Session-independent (codebase shared) |
| Archivalist | Sonnet 4.5 (1M) | Tiered (recency) | Session-scoped writes, cross-session promoted reads |
| Academic | Opus 4.5 (200K) | Topic Cluster | Session-independent (research globally applicable) |

### Global Inspector + Tester (DaemonSet)

Always-running admission control infrastructure.

```
Analogous to: Kubernetes DaemonSet
Properties:
    Runs on every "node" (available at every layer gate)
    Not scoped to any pipeline
    Validates merged layer output against full DAG criteria
    Feedback target: Architect (control plane)
```

---

## Resource Management

### Context Windows = Memory Requests/Limits

Each agent type has a context window allocation:

| Agent | Model | Context Window | K8s Equivalent |
|---|---|---|---|
| Guide | Gemini Flash | Unbounded routing | Guaranteed QoS |
| Architect | Opus 4.5 | 200K tokens | Guaranteed QoS, medium memory |
| Orchestrator | Haiku 4.5 | Lightweight | Guaranteed QoS, low memory |
| Engineer | Opus 4.5 | 200K tokens | Burstable QoS, high memory |
| Designer | Sonnet 4.5 | 200K tokens | Burstable QoS, high memory |
| Inspector | Codex 5.2 | 200K tokens | Burstable QoS, medium memory |
| Tester | Codex 5.2 | 200K tokens | Burstable QoS, medium memory |
| Librarian | Sonnet 4.5 | 1M tokens | Guaranteed QoS, very high memory |
| Archivalist | Sonnet 4.5 | 1M tokens | Guaranteed QoS, very high memory |
| Academic | Opus 4.5 | 200K tokens | Guaranteed QoS, medium memory |

### GoroutineBudget = CPU Requests/Limits

Per-agent goroutine limits with pressure-aware dynamic scaling:

```
typeWeights:
    engineer    = 1.0  (highest goroutine allocation)
    tester      = 0.8
    architect   = 0.5
    inspector   = 0.5
    librarian   = 0.3
    archivalist = 0.3
    guide       = 0.2  (lowest — stateless router)

Pressure levels (like K8s memory pressure):
    Normal    → 1.0× budget
    Elevated  → 0.75× budget
    High      → 0.50× budget
    Critical  → 0.25× budget
```

### Same-Type Handoff = Horizontal Pod Autoscaler

When an agent's context fills to threshold, a new instance spins up with transferred state:

```
Trigger:     GP-detected quality degradation (not fixed percentage)
Process:     Old agent builds handoff state → new agent receives state → old drains
Analogous:   HPA scaling with custom metrics (GP model = Prometheus adapter)

Handoff state includes:
    • Completed/failed/pending/running task records
    • Workflow summary and current position
    • Metrics (durations, error rates, heartbeat delays)
    • Context virtualization references (CTX-REF markers)
```

---

## Context Virtualization = Cache Hierarchy

The LLM context window is treated like a CPU cache backed by infinite storage.

```
L1 Cache     = Active context window (200K-1M tokens)
L2 Cache     = Context virtualization references (CTX-REF markers, ~100 tokens each)
Main Memory  = Bleve + VectorDB (searchable, retrievable)
Disk         = Archivalist (persistent, session-scoped)

Eviction: Evicted content → replaced with compact reference marker:
    [CTX-REF:conversation | 15 turns (4,200 tokens) @ 14:23 |
     Topics: auth flow, JWT, middleware | retrieve_context(ref_id="abc123")]

Retrieval: On cache miss, fetch from Bleve/VectorDB and inject into context
```

**Eviction strategies by agent type:**

| Strategy | Used By | Behavior |
|---|---|---|
| Tiered (recency) | Librarian, Archivalist | Evict oldest turns first, preserve recent |
| Topic Cluster | Academic | Evict complete research topics together |
| Task Completion | Architect | Evict completed task context, preserve active |
| Same-Type Handoff | Engineer, Designer, Inspector, Tester | Spin up new instance with transferred state |

All strategies are GP-triggered — Gaussian Process models detect quality degradation curves and fire eviction/handoff at the statistically optimal point, replacing fixed percentage thresholds.

---

## Networking

### PipelineBus = Pod Network (localhost)

Intra-pipeline communication over bounded typed channels:

```
Channel                  Buffer    Direction
inspectorFeedback        1         Inspector → Worker
testerFeedback           1         Tester → Worker
workerDone               1         Worker → Validation
inspectorDone            1         Inspector validation result
testerDone               1         Tester validation result
userMessages             4         User → Pipeline (overrides)
```

Buffer-1 channels enforce single-flight semantics. No unbounded growth. Context-gated send/recv for cancellation safety.

### Direct Consultation = Service Mesh with Pre-Resolved Endpoint

Internal service-to-service communication with known targets. Skips Guide classification but still traverses the mesh data plane.

```
Consultation matrix (who can consult whom):
    Guide       → all agents
    Architect   → engineer, designer, inspector, tester, librarian, archivalist, academic
    Engineer    → architect, designer, inspector, tester, librarian, archivalist, academic
    Inspector   → tester, designer, librarian, archivalist
    Tester      → inspector, designer, librarian, archivalist
    Designer    → inspector, tester, librarian, archivalist
    Librarian   → archivalist, academic
    Archivalist → librarian, academic
    Academic    → librarian, archivalist
```

### NetworkProxy = Egress Gateway

Controls outbound traffic from agents:

```
Per-agent domain allowlists:
    Engineer  → npm registry, go proxy, GitHub API
    Librarian → package registries, code hosting
    Academic  → research APIs, documentation sites
    Designer  → design system CDNs, font services

All egress routed through proxy:
    HTTP_PROXY / HTTPS_PROXY environment variables
    Domain checked against allowlist
    Allowed → forward + audit log
    Blocked → 403 + audit log
```

### Signal Bus = Kubernetes Events

Broadcast signals for coordination:

```
SignalPauseAll        → Rate limit hit: pause all agents
SignalResumeAll       → Rate limit cleared: resume all
SignalPausePipeline   → Pause specific pipeline
SignalResumePipeline  → Resume specific pipeline
SignalCancelTask      → Cancel current task
SignalAbortSession    → Abort entire session
SignalQuotaWarning    → Approaching budget (informational)
```

Signals flow through the Guide (mandatory mesh). RequiresAck signals block until all subscribers acknowledge. Agent state machine on signal receipt:

```
Idle → Running → [Signal] → Checkpointing → Paused → Resuming → Running
```

---

## Session Isolation = Namespaces

Sessions are the fundamental isolation boundary, mapping directly to K8s Namespaces.

### Namespace-Scoped (Private)

| Resource | Description |
|---|---|
| Active DAG state | Current workflow, task progress |
| Conversation history | User messages and agent responses |
| Modified files (in-progress) | Uncommitted changes in pipeline VFS overlays |
| Read file tracking | Deduplication of Librarian queries |
| Engineer instances | Pipeline-scoped agent instances |
| Active pipeline state | Running TDD loops, variant groups |

### Cluster-Scoped (Shared)

| Resource | Description |
|---|---|
| Codebase (Librarian) | Physical filesystem, indexed in Bleve + VectorDB |
| Promoted Archivalist entries | Decisions marked as cross-session applicable |
| Academic research cache | Research results reusable across sessions |
| User preferences | Global configuration |
| Git repository state | Shared branch state |

### Cross-Namespace Access

Archivalist entries have session-scoped writes with optional promotion to cluster-scope:

```
Write:  Always to current session's partition
Read:   Session-scoped entries + all promoted entries
Promote: Manual (user) or automatic (quality-gated)
```

---

## Security = Pod Security + RBAC + Admission Control

### Sandbox Architecture (Pod Security Standards)

Four-layer defense-in-depth, all optional (off by default, enabled via `/sandbox enable`):

```
Layer 1: OS-Level Sandbox
    Linux  → bubblewrap (namespace isolation, filesystem restrictions)
    macOS  → Seatbelt (sandbox-exec with generated profiles)
    Provides: Process isolation, filesystem boundaries, resource limits

Layer 2: Virtual Filesystem (VFS)
    Copy-on-write for all modifications
    Path escape prevention (symlink resolution, .. traversal, absolute paths)
    Working directory boundary enforcement

Layer 3: Network Proxy
    All subprocess traffic routed through Sylk
    Domain allowlist enforcement
    Request/response logging, bandwidth limits

Layer 4: Permission System
    Command allowlist (ignores arguments)
    Per-project persistent permissions
    Safe defaults
```

Graceful degradation: if a sandbox layer fails to initialize, the system can fall back to unsandboxed execution with a warning (configurable via `FallbackOnError`).

### RBAC (AgentRole + PermissionManager)

Every Operation carries an AgentRole for authorization:

```go
type Operation struct {
    // ...
    AgentID     string
    AgentRole   AgentRole   // Security: role that performed this operation
    // ...
}
```

### Audit Logging

Tamper-evident audit trail with monotonic sequence and chaining hash:

```
Categories:
    Permission events   (granted, denied, escalation)
    File operations     (read, write, delete, with hash)
    Process execution   (command, exit code, blocked)
    Network activity    (allowed, blocked, with domain)
    LLM interactions    (provider, model, tokens, cost)
    Session events      (start, end, agent spawn/terminate)
    Config changes      (permission changes, credential access)
```

---

## Health Monitoring = kubelet Probes + cAdvisor + Node Problem Detector

### HealthMonitor (Deterministic, Orchestrator-Owned)

The HealthMonitor runs inside the Orchestrator on a fixed 10-second cycle. It is deterministic — no LLM involvement. It computes per-agent health levels, detects transitions, and auto-escalates critical changes to the Architect.

```
Health levels: healthy → degraded → unhealthy → critical

Per-agent metrics:
    Heartbeat tracking     (last seen, missed count)
    Error rate             (windowed: 5 min, threshold: 50%)
    Active/completed/failed/timed-out task counts
    Response time tracking (avg, max rolling window)

Alert types:
    timeout              Task exceeded deadline (default 5 min)
    heartbeat_lost       Missed heartbeats (30s intervals, 30s timeout)
    high_error_rate      Error rate > threshold within window
    transient_storm      >5 failures in 1 minute
    task_backlog         Excessive queued tasks

On each cycle:
    1. Compute AgentCheckResult per registered agent
    2. Detect level transitions (e.g., degraded → critical)
    3. Cache in HealthCache (Ristretto, fast skill retrieval)
    4. Forward to Archivalist (historical storage via Guide)
    5. Auto-escalate critical transitions → Architect (deterministic)
```

### BufferRegistry (3-Tier State Persistence)

The Orchestrator persists all pipeline state through a 3-tier hierarchy owned by the BufferRegistry:

```
Tier 1 — Ristretto Hot Cache
    Purpose:    Fastest reads for skill queries (query_agent_health, query_buffer)
    TTL:        10 minutes
    Contents:   Latest TaskUpdateEntry per task, latest HealthCheckResult
    Cost-based: Ristretto MaxCost derived from capacity × maxBuffers × avgEntryCost

Tier 2 — Ring Buffer (Warm)
    Purpose:    Per-task circular buffer of pipeline updates
    Capacity:   max(32, maxConcurrency×4) per task
    Max buffers: max(256, maxConcurrency×16) active tasks
    Semantics:  Circular (overwrites oldest when full)
    GC:         30s interval, flushes idle buffers (>10 min) to Tier 3

Tier 3 — SQLite (Cold)
    Purpose:    Durable storage for evicted buffers, historical queries
    Tables:     dag_executions, dag_revisions, task_updates, pipeline_state
    Lifecycle:  GC'd buffers flushed here, closed buffers flushed on shutdown
    Queries:    QueryTaskUpdates, QueryTaskUpdatesSince, GetDAGExecution, GetPipelineState
```

### WAL Journal (Crash Recovery)

The OrchestratorJournal is a write-ahead log for DAG lifecycle events:

```
Entry types:
    DAGStart   → before scheduler submission
    DAGComplete → after terminal state
    DAGAbort   → on failure or crash recovery
    DAGCancel  → on cancellation
    DAGModify  → on mid-flight modification (with revision)
    NodeDispatch → before node task dispatch
    NodeResult  → after node completion/failure

Recovery:
    On startup → FindIncompleteDAGs() → mark as failed
    7-day retention, 24h GC interval
```

---

## Complete K8s Mapping Reference

### Control Plane

| Kubernetes | Sylk |
|---|---|
| kube-apiserver | Guide (ingress + mesh data plane) |
| etcd | VectorGraphDB + Bleve + Archivalist |
| kube-controller-manager | Architect |
| cloud-controller-manager | LLM Provider Layer |

### Node Runtime

| Kubernetes | Sylk |
|---|---|
| Node | DAG Layer (topological execution boundary) |
| kubelet + cAdvisor + Node Controller | Orchestrator (DAGBridge, HealthMonitor, BufferRegistry, Store, WAL, LLM Loop) |
| Container Runtime Shim | TDDExecutor / PipelineRunner (per-pipeline agent lifecycle) |
| CRI | AgentFactory + GoroutineScope |
| Pod | Pipeline (agents + VFS + bus) |
| Container | Agent instance (Inspector, Tester, Engineer, Designer) |
| Init Container | Inspector.DefineCriteria() |
| Sidecar Container | Inspector + Tester (validation phase) |

### Storage

| Kubernetes | Sylk |
|---|---|
| PersistentVolume | Physical filesystem (project directory) |
| PersistentVolumeClaim | PipelineVFS overlay (CoW on baseline) |
| StorageClass | VFS configuration (MVCC, staging, cleanup) |
| CSI Driver | Central Version Store (CVS) |
| ConfigMap | Agent configs (InspectorConfig, etc.) |
| Secret | Credential store (API keys, encrypted) |
| emptyDir | Ring buffers (warm tier, GC'd to SQLite) |
| Node local storage | Orchestrator SQLite + WAL (dag_executions, task_updates, pipeline_state) |
| cAdvisor metrics | HealthCache (Ristretto hot cache of health check results) |

### Networking

| Kubernetes | Sylk |
|---|---|
| Pod network (localhost) | PipelineBus (intra-pipeline channels) |
| Service Mesh (Istio STRICT) | Guide (mandatory traffic interception) |
| ClusterIP Service | Direct Consultation (pre-resolved, still through mesh) |
| Gateway API / Ingress | Guide ingress layer (user prompts, external data) |
| Egress Gateway | NetworkProxy (domain-allowlisted outbound) |
| NetworkPolicy | Per-agent ingress/egress rules |
| CoreDNS | Guide routing cache + classification |

### Workload Types

| Kubernetes | Sylk |
|---|---|
| Job | Pipeline execution (run-to-completion) |
| Deployment + ReplicaSet | Variant group (parallel alternatives) |
| DaemonSet | Guide, Global Inspector, Global Tester |
| StatefulSet | Knowledge RAGs (Librarian, Archivalist, Academic) |
| CronJob | Periodic maintenance (health checks, GC, indexing) |

### Scaling + Resources

| Kubernetes | Sylk |
|---|---|
| HorizontalPodAutoscaler | Same-type handoff (GP-triggered) |
| VerticalPodAutoscaler | GP-based context eviction |
| Resource requests/limits | Context window + GoroutineBudget |
| QoS classes | LLM priority queues (Guaranteed/Burstable/BestEffort) |
| ResourceQuota | Session-level token budgets |
| LimitRange | Per-agent type defaults (typeWeights) |
| PriorityClass | Architect-assigned pipeline priority |

### Operations

| Kubernetes | Sylk |
|---|---|
| Kubernetes Events | Signal Bus (pause/resume/cancel) |
| Prometheus metrics + cAdvisor | Orchestrator HealthMonitor + HealthCache + token tracking |
| Node Problem Detector | Orchestrator LLM loop (intelligent event analysis, deterministic fallback) |
| Audit logging | AuditLogger (tamper-evident, chained hash) |
| kubectl | TUI + slash commands |
| Namespace | Session (isolation boundary) |
| RBAC | AgentRole + PermissionManager |
| Pod Security Standards | Sandbox layers (OS, VFS, Network, Permission) |
| ValidatingAdmissionWebhook | Global Inspector (layer gate validation) |
| Liveness/Readiness Probes | Pipeline Inspector + Tester (per-task TDD) |
| Rolling Update | Same-type handoff |
| Blue/Green Deployment | Variant selection (atomic commit/discard) |
| Service Mesh OT | OT Engine (cross-pipeline merge at layer gate) |

---

## Data Flow: End to End

```
USER INPUT
    │
    ▼
[Session Manager] ← Namespace binding
    │
    ▼
GUIDE (Mandatory Mesh)
    ├── Audit log entry
    ├── Session context injection
    ├── Intent classification (or skip if direct consultation)
    ├── Permission check
    ├── Rate limit check
    └── Route to target
         │
         ├──→ ARCHITECT (ctrl-mgr)
         │     ├── Query Librarian    ← via Guide ← SearchCoordinator
         │     ├── Query Archivalist  ← via Guide ← ArchivalistDB
         │     ├── Query Academic     ← via Guide ← External APIs
         │     ├── Clarify with user  ← via Guide (last resort)
         │     └── Submit DAG         → via Guide → Orchestrator
         │
         ├──→ ORCHESTRATOR (node runtime agent)
         │     ├── Receive DAG        ← Architect (via Guide)
         │     ├── DAGBridge.Execute  → dag.Scheduler → BusNodeDispatcher → agents
         │     ├── BufferRegistry     ← pipeline.update.* (Ristretto → ring → SQLite)
         │     ├── HealthMonitor      → 10s cycle → HealthCache + Archivalist
         │     ├── LLM Loop           → event batches → Gemini Flash → 18 skills
         │     ├── WAL Journal        → crash recovery on startup
         │     ├── Escalate critical  → via Guide → Architect
         │     ├── Persist state      → SQLite (DAG exec, revisions, pipeline state)
         │     ├── Mid-flight modify  ← Architect modify_dag → DAGBridge.Modify
         │     └── Submit events      → via Guide → Archivalist (mandatory)
         │
         ├──→ PIPELINE (pod)
         │     ├── TDD Loop
         │     │    ├── Inspector.DefineCriteria()    (init container)
         │     │    ├── Tester.CreateTests()           (RED)
         │     │    ├── Worker.Execute()               (GREEN)
         │     │    └── Inspector + Tester.Validate()  (sidecar probes)
         │     ├── Push updates → TaskUpdateBuffer
         │     └── On completion → Layer gate
         │
         ├──→ LAYER GATE (admission control)
         │     ├── OT Merge (reconcile VFS overlays)
         │     ├── Global Inspector (full DAG validation)
         │     ├── Global Tester (integration tests)
         │     ├── Pass → commit to physical FS, start Layer N+1
         │     └── Fail → feedback to Architect for replanning
         │
         ├──→ KNOWLEDGE RAGs (StatefulSets)
         │     ├── Librarian:   Bleve + VectorDB (DomainCode)
         │     ├── Archivalist: Bleve + VectorDB (DomainHistory)
         │     └── Academic:    External APIs + cache (DomainAcademic)
         │
         └──→ USER RESPONSE
              ├── Streamed LLM output (StreamChunk → Event Bus)
              ├── Status updates (pushed on state change)
              └── Signal delivery (pause/resume/cancel)

PERSISTENT STORAGE (etcd)
    ├── VectorGraphDB (vector.db): embeddings + HNSW + graph + metadata
    ├── Bleve Index (documents.bleve/): full-text search (Scorch segments)
    └── Archivalist Entries: task records, decisions, patterns (both stores)
```

---

*This document defines the system-level architecture of Sylk. For agent-specific behavior, see ARCHITECTURE.md. For filesystem versioning, see FILESYSTEM.md. For context management, see CONTEXT.md. For handoff decisions, see HANDOFF.md.*
