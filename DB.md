# Sylk Knowledge Graph Database Architecture

## Overview

Sylk is a multi-agent knowledge graph database designed for real-time, conversation-driven knowledge accumulation. Multiple agents concurrently read and write to a shared graph during user sessions, requiring high throughput, low latency, and strong durability guarantees.

**Design Principles:**
- Correctness over simplicity
- Robustness over ease of implementation
- Performance over complexity concerns
- No data loss, ever
- Lock-free reads where possible
- Sharded writes to eliminate contention
- Append-only structures for crash safety
- Session isolation with explicit global access

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              AGENT LAYER                                     │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐           │
│  │ Agent A │  │ Agent B │  │ Agent C │  │ Agent D │  │ Agent E │           │
│  │ (code)  │  │ (search)│  │ (papers)│  │ (chat)  │  │ (tools) │           │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘           │
│       │            │            │            │            │                 │
└───────┼────────────┼────────────┼────────────┼────────────┼─────────────────┘
        │            │            │            │            │
        └────────────┴────────────┴────────────┴────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          KNOWLEDGE GRAPH API                                 │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         READ OPERATIONS                               │   │
│  │  • GetNode(id) ────────────────────────────── Lock-free              │   │
│  │  • FindNode(canonicalKey) ─────────────────── Sharded lock (1/64)    │   │
│  │  • GetOutgoingEdges(nodeID) ───────────────── Sharded lock (1/64)    │   │
│  │  • GetIncomingEdges(nodeID) ───────────────── Sharded lock (1/64)    │   │
│  │  • TraverseGraph(start, depth) ────────────── Multi-shard, parallel  │   │
│  │  • VectorSearch(query, k) ─────────────────── IVF index              │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                        WRITE OPERATIONS                               │   │
│  │  • AddNode(canonicalKey, data) ────────────── Sharded + atomic ID    │   │
│  │  • UpsertEdge(src, dst, type, weight) ─────── Sharded lock (1/64)    │   │
│  │  • UpdateEdgeWeight(src, dst, type, weight) ─ Sharded lock (1/64)    │   │
│  │  • AddVector(nodeID, embedding) ───────────── IVF index              │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                      VERSION OPERATIONS                               │   │
│  │  • Checkpoint(name) ───────────────────────── Create named version   │   │
│  │  • Checkout(versionID) ────────────────────── Switch HEAD pointer    │   │
│  │  • ListVersions() ─────────────────────────── Show version DAG       │   │
│  │  • Commit() ───────────────────────────────── Merge to global        │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           STORAGE ENGINE                                     │
│                                                                              │
│  ┌────────────────────┐  ┌────────────────────┐  ┌────────────────────┐    │
│  │    NODE STORE      │  │    EDGE STORE      │  │   VECTOR STORE     │    │
│  │                    │  │                    │  │                    │    │
│  │  • Append-only     │  │  • Dynamic shards  │  │  • IVF + BBQ       │    │
│  │  • Lock-free reads │  │  • Lock-free R/W   │  │  • Sharded storage │    │
│  │  • Block-allocated │  │  • Per-shard WAL   │  │  • Vamana graph    │    │
│  │  • Supersession    │  │                    │  │                    │    │
│  └────────────────────┘  └────────────────────┘  └────────────────────┘    │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         SHARD ALIGNMENT                               │   │
│  │                                                                       │   │
│  │  NumShards = ⌈TotalNodes / 65536⌉  (derived from 16-bit addressing)  │   │
│  │                                                                       │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐      │   │
│  │  │  Shard 0   │  │  Shard 1   │  │  Shard 2   │  │    ...     │      │   │
│  │  │ 0-65535    │  │65536-131071│  │131072-...  │  │  (grows)   │      │   │
│  │  │            │  │            │  │            │  │            │      │   │
│  │  │ NodeShard  │  │ NodeShard  │  │ NodeShard  │  │ NodeShard  │      │   │
│  │  │ EdgeShard  │  │ EdgeShard  │  │ EdgeShard  │  │ EdgeShard  │      │   │
│  │  │VectorShard │  │VectorShard │  │VectorShard │  │VectorShard │      │   │
│  │  └────────────┘  └────────────┘  └────────────┘  └────────────┘      │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Session Model

### Session Definition

A **Session** is an isolated conversation with a fully independent set of agents. Sessions do not share agent instances, and their uncommitted work is invisible to other sessions.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           SESSION MODEL                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Session = Independent conversation with isolated agent pool                │
│                                                                              │
│  Session 1 (Monday):                                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ Librarian, Academic, Archivalist (spawned for Session 1)   │   │
│  │                                                                      │   │
│  │  Work stored in: .sylk/sessions/ses_001/versions/                   │   │
│  │  Visibility: Session 1 only (until committed)                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  Session 2 (Tuesday):                                                       │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  User ←→ Librarian, Academic, Archivalist (NEW instances)           │   │
│  │                                                                      │   │
│  │  Work stored in: .sylk/sessions/ses_002/versions/                   │   │
│  │  Visibility: Session 2 only (until committed)                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  Both sessions contribute to ONE shared Knowledge Graph upon commit.        │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Session Isolation

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        SESSION ISOLATION MODEL                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  DEFAULT VISIBILITY (isolated):                                              │
│                                                                              │
│    Session sees ONLY:                                                        │
│      • Its own versions (up to HEAD)                                        │
│      • Global committed state at session START (snapshot)                   │
│                                                                              │
│    Session does NOT see:                                                     │
│      • Other active sessions' uncommitted work                              │
│      • Commits made AFTER this session started                              │
│                                                                              │
│  EXPLICIT GLOBAL QUERY (user command):                                       │
│                                                                              │
│    /query-global "search term"                                              │
│      → Queries current global state (including recent commits)              │
│      → Read-only, does not affect session isolation                         │
│      → Results can be "imported" into session if user chooses               │
│                                                                              │
│  WHY SNAPSHOT ISOLATION:                                                     │
│    • Prevents "phantom" results during session                              │
│    • Session's work is deterministic                                        │
│    • No races with concurrent sessions                                      │
│    • User explicitly controls when to see new global state                  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Session Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        SESSION LIFECYCLE                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. SESSION START                                                            │
│     ┌─────────────────────────────────────────────────────────────────┐     │
│     │  • Create session directory: .sylk/sessions/{session_id}/       │     │
│     │  • Snapshot current global committed state                      │     │
│     │  • Initialize version manifest with v1.0.0 (initial version)     │     │
│     │  • Spawn agent instances for this session                       │     │
│     └─────────────────────────────────────────────────────────────────┘     │
│                           │                                                  │
│                           ▼                                                  │
│  2. SESSION ACTIVE                                                           │
│     ┌─────────────────────────────────────────────────────────────────┐     │
│     │  • Agents read/write to session-local version folders           │     │
│     │  • Automatic checkpoints on delta thresholds                    │     │
│     │  • User can create explicit checkpoints: /checkpoint "name"     │     │
│     │  • User can checkout older versions: /checkout v000003          │     │
│     │  • User can query global: /query-global "search"                │     │
│     └─────────────────────────────────────────────────────────────────┘     │
│                           │                                                  │
│                           ▼                                                  │
│  3. SESSION COMMIT (explicit /commit OR session end)                         │
│     ┌─────────────────────────────────────────────────────────────────┐     │
│     │  • Merge session data into global knowledge graph               │     │
│     │  • Deduplicate via canonical key (supersession model)           │     │
│     │  • Update global meta.json with committed session               │     │
│     │  • Mark session status = "committed"                            │     │
│     │  • Session folder persists (for history/audit)                  │     │
│     └─────────────────────────────────────────────────────────────────┘     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Versioning Model

### Per-Session Versioning

Each session maintains its own version history using **semantic versioning** (Major.Minor.Patch), independent of other sessions.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      PER-SESSION VERSIONING                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Session S (semantic versioning: vMajor.Minor.Patch):                       │
│                                                                              │
│    v1.0.0 ←── v1.0.1 ←── v1.0.2 ←── v1.0.3 ←── v1.0.4                       │
│                           ↑                                                  │
│                          HEAD                                                │
│                                                                              │
│    All versions exist. HEAD points to "current view".                       │
│    Checkout v1.0.1 → HEAD moves to v1.0.1, later versions still exist.      │
│                                                                              │
│  If user does new work while HEAD=v1.0.2:                                    │
│                                                                              │
│    v1.0.0 ←── v1.0.1 ←── v1.0.2 ←── v1.0.3 ←── v1.0.4  (orphaned branch)   │
│                           ↑                                                  │
│                           └──── v1.0.3' ←── v1.0.4'                         │
│                                              ↑                               │
│                                             HEAD                             │
│                                                                              │
│    Orphaned v1.0.3/v1.0.4 preserved. Can switch back anytime.               │
│    At commit, only ancestor chain of HEAD is merged to global.              │
│                                                                              │
│  Version Bump Types:                                                         │
│    • Patch (v1.0.0 → v1.0.1): Normal checkpoint, auto-checkpoint            │
│    • Minor (v1.0.1 → v1.1.0): Feature milestone, explicit checkpoint        │
│    • Major (v1.1.0 → v2.0.0): Breaking changes, significant refactor        │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Version Storage (Shared Data + Index)

Each version folder contains **indexes** (OffsetIndex files) that reference records in shared data files. No raw data is duplicated between versions — only the index mapping (ID → byte offset) is cloned at checkpoint time.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    VERSION FOLDER STRUCTURE                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  sessions/{session_id}/versions/v1.0.2/                                     │
│  │                                                                          │
│  ├── meta.json              # Version metadata                              │
│  │   {                                                                      │
│  │     "created_at": "2025-01-26T10:15:00Z",                               │
│  │     "version": "v1.0.2"                                                  │
│  │   }                                                                      │
│  │                                                                          │
│  ├── nodes/                                                                 │
│  │   └── index.bin          # OffsetIndex: nodeID → offset in data.bin     │
│  │                                                                          │
│  ├── edges/                                                                 │
│  │   └── data.bin           # Per-version edge records (35-byte each)      │
│  │                                                                          │
│  ├── vectors/                                                               │
│  │   └── index.bin          # OffsetIndex: nodeID → offset in data.bin     │
│  │                                                                          │
│  ├── docs/                                                                  │
│  │   └── index.bin          # OffsetIndex: docID → offset in data.bin      │
│  │                                                                          │
│  └── bleve/                 # Bleve snapshot (optional, rebuilt on demand)  │
│                                                                              │
│  Checkpoint operation:                                                      │
│    1. Clone current OffsetIndex files to new version folder                │
│    2. Copy edge data.bin (per-version, not shared)                          │
│    3. No node/vector/doc data copied (shared data files)                   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Checkpoint Triggers

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       CHECKPOINT TRIGGERS                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. EXPLICIT (user command with checkpoint type)                             │
│     /checkpoint "before-refactor" --minor   # v1.0.1 → v1.1.0              │
│     /checkpoint "major-release" --major     # v1.1.0 → v2.0.0              │
│     /checkpoint                             # patch bump (default)          │
│                                                                              │
│  2. AUTOMATIC (delta-based algorithm) - Always Patch bump                    │
│                                                                              │
│     Track deltas since last checkpoint:                                      │
│       Δnodes     = nodes created since last checkpoint                      │
│       Δedges     = edges created/modified                                   │
│       Δvectors   = vectors created                                          │
│       Δdocs      = bytes added to Document DB                               │
│       Δtime      = time since last checkpoint                               │
│                                                                              │
│     Trigger auto-checkpoint when ANY threshold exceeded:                     │
│                                                                              │
│       ┌────────────────┬─────────────────┬─────────────────────────────┐    │
│       │ Metric         │ Default         │ Rationale                   │    │
│       ├────────────────┼─────────────────┼─────────────────────────────┤    │
│       │ Δnodes         │ >= 50           │ Significant code indexing   │    │
│       │ Δedges         │ >= 200          │ Major relationship changes  │    │
│       │ Δvectors       │ >= 50           │ Significant embedding work  │    │
│       │ Δdocs          │ >= 500KB        │ Large content ingestion     │    │
│       │ Δtime          │ >= 10 minutes   │ Periodic safety net         │    │
│       └────────────────┴─────────────────┴─────────────────────────────┘    │
│                                                                              │
│     Thresholds configurable in .sylk/config.yaml                            │
│                                                                              │
│  3. IMPLICIT (session boundaries) - Always creates v1.0.0                    │
│     Session start  → automatic v1.0.0 checkpoint (initial version)          │
│     Session commit → automatic final checkpoint before merge                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Global Version Management

The global knowledge graph maintains its own semantic version, separate from session versions. Each session commit causes a **MAJOR** version bump on the global KG.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     GLOBAL VERSIONING STRATEGY                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SESSION COMMIT FLOW:                                                        │
│                                                                              │
│    ┌─────────────────┐     ┌──────────────────┐     ┌──────────────────┐   │
│    │ Session S       │     │ Pre-Commit       │     │ Global KG        │   │
│    │ HEAD = v1.2.3   │ ──► │ Checkpoint       │ ──► │ Major Bump       │   │
│    └─────────────────┘     │ (Minor: v1.3.0)  │     │ v2.0.0 → v3.0.0  │   │
│                            └──────────────────┘     └──────────────────┘   │
│                                                                              │
│  1. User requests session commit (merge session to global)                   │
│  2. Create pre-commit checkpoint in session (minor bump)                     │
│     - HEAD v1.2.3 → v1.3.0 (minor bump, captures final state)               │
│  3. Merge session HEAD ancestry to global KG                                 │
│  4. MAJOR bump on global KG (session commit = major milestone)              │
│     - Global v2.0.0 → v3.0.0                                                │
│  5. Record commit: session_id, final_version, global_version, timestamp     │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  GLOBAL VERSION BUMP RULES:                                                  │
│                                                                              │
│    ┌────────────────────────────────┬───────────────────────────────────┐   │
│    │ Event                          │ Version Bump                       │   │
│    ├────────────────────────────────┼───────────────────────────────────┤   │
│    │ Session commit (merge)         │ MAJOR (e.g., v2.0.0 → v3.0.0)    │   │
│    │ Schema migration               │ MAJOR (breaking change)           │   │
│    │ Incremental updates (future)   │ MINOR (e.g., v2.0.0 → v2.1.0)    │   │
│    │ Index rebuild                  │ PATCH (e.g., v2.0.0 → v2.0.1)    │   │
│    │ Data repair/integrity fix      │ PATCH (no semantic change)        │   │
│    └────────────────────────────────┴───────────────────────────────────┘   │
│                                                                              │
│  RATIONALE:                                                                  │
│    • Session commit = MAJOR: A conversation sequence is complete. This      │
│      represents a significant knowledge milestone (new research, features,  │
│      or refactoring). Major version makes it easy to identify session       │
│      boundaries in the global version history.                              │
│    • Session is independent: Each session has its own v1.0.0 → v1.N.P      │
│      progression. Sessions don't share version numbers.                     │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  EXAMPLE VERSION TIMELINE:                                                   │
│                                                                              │
│    Global KG v0.0.0 (empty project, initial state)                          │
│                                                                              │
│    Session 1: v1.0.0 → v1.0.3 → v1.1.0 (commit, minor pre-commit)           │
│    Global KG: v0.0.0 → v1.0.0                                               │
│                                                                              │
│    Session 2: v1.0.0 → v1.0.1 → v1.0.5 → v1.1.0 (commit)                    │
│    Global KG: v1.0.0 → v2.0.0                                               │
│                                                                              │
│    Session 3: v1.0.0 → v1.0.2 → v1.2.0 → v1.3.0 (commit)                    │
│    Global KG: v2.0.0 → v3.0.0                                               │
│                                                                              │
│    Result: Global is at v3.0.0, each major = one committed session          │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Version Operations

```go
// SemanticVersion represents a version using semantic versioning (Major.Minor.Patch)
type SemanticVersion struct {
    Major uint16 `json:"major"`
    Minor uint16 `json:"minor"`
    Patch uint16 `json:"patch"`
}

func (v SemanticVersion) String() string {
    return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v SemanticVersion) DirName() string {
    return v.String() // e.g., "v1.0.0"
}

func (v SemanticVersion) IsZero() bool {
    return v.Major == 0 && v.Minor == 0 && v.Patch == 0
}

func (v SemanticVersion) Equal(other SemanticVersion) bool {
    return v.Major == other.Major && v.Minor == other.Minor && v.Patch == other.Patch
}

func (v SemanticVersion) BumpPatch() SemanticVersion {
    return SemanticVersion{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
}

func (v SemanticVersion) BumpMinor() SemanticVersion {
    return SemanticVersion{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
}

func (v SemanticVersion) BumpMajor() SemanticVersion {
    return SemanticVersion{Major: v.Major + 1, Minor: 0, Patch: 0}
}

// CheckpointType determines how the version number is bumped
type CheckpointType string

const (
    CheckpointMajor CheckpointType = "major" // Breaking changes: v1.0.0 → v2.0.0
    CheckpointMinor CheckpointType = "minor" // New features:     v1.0.0 → v1.1.0
    CheckpointPatch CheckpointType = "patch" // Bug fixes/auto:   v1.0.0 → v1.0.1
)

// Checkout: Move HEAD to a different version (O(1) pointer update)
func (s *Session) Checkout(version SemanticVersion) error {
    if !s.manifest.HasVersion(version) {
        return ErrVersionNotFound
    }
    s.manifest.Head = version
    return s.persistManifest()
}

// Checkpoint: Create new version from current HEAD with specified bump type
func (s *Session) Checkpoint(name string, checkpointType CheckpointType) (SemanticVersion, error) {
    var newVersion SemanticVersion
    switch checkpointType {
    case CheckpointMajor:
        newVersion = s.manifest.Head.BumpMajor()
    case CheckpointMinor:
        newVersion = s.manifest.Head.BumpMinor()
    case CheckpointPatch:
        newVersion = s.manifest.Head.BumpPatch()
    default:
        newVersion = s.manifest.Head.BumpPatch()
    }

    v := Version{
        ID:        newVersion,
        ParentID:  s.manifest.Head,
        Name:      name,
        CreatedAt: time.Now(),
        Trigger:   string(checkpointType),
        Stats:     s.deltaTracker.CurrentStats(),
    }

    // Create version folder and persist current delta
    if err := s.createVersionDirectory(newVersion); err != nil {
        return SemanticVersion{}, err
    }

    s.manifest.Versions = append(s.manifest.Versions, v)
    s.manifest.Head = newVersion
    s.deltaTracker.Reset()

    return newVersion, s.persistManifest()
}

// ListVersions: Return all versions in session (including orphaned branches)
func (s *Session) ListVersions() []Version {
    return s.manifest.Versions
}

// GetAncestorChain: Return versions from HEAD back to v1.0.0 (for layered reads)
func (s *Session) GetAncestorChain() []SemanticVersion {
    chain := []SemanticVersion{s.manifest.Head}
    versionMap := make(map[string]Version)
    for _, v := range s.manifest.Versions {
        versionMap[v.ID.String()] = v
    }

    current := s.manifest.Head
    for {
        v, ok := versionMap[current.String()]
        if !ok || v.ParentID.IsZero() {
            break
        }
        chain = append(chain, v.ParentID)
        current = v.ParentID
    }
    return chain
}
```

---

## Data Model

### Domains

The knowledge graph is partitioned into three domains, each with its own set of valid node types:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                               DOMAINS                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────────┐  │
│  │     DomainCode      │  │    DomainHistory    │  │   DomainAcademic    │  │
│  │                     │  │                     │  │                     │  │
│  │  Code entities,     │  │  Conversation and   │  │  External knowledge │  │
│  │  symbols, files,    │  │  session history,   │  │  papers, docs,      │  │
│  │  modules            │  │  agent actions      │  │  web content        │  │
│  │                     │  │                     │  │                     │  │
│  │  DERIVABLE from     │  │  PRIMARY DATA       │  │  EXTERNAL SOURCE    │  │
│  │  source files       │  │  (not recoverable)  │  │  (may be refetchable)│  │
│  └─────────────────────┘  └─────────────────────┘  └─────────────────────┘  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Node Types

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              NODE TYPES                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  DomainCode:                                                                 │
│  ┌─────────┬─────────┬──────────┬─────────┬───────────┬───────────────────┐ │
│  │  File   │ Package │ Function │ Method  │  Struct   │    Interface      │ │
│  └─────────┴─────────┴──────────┴─────────┴───────────┴───────────────────┘ │
│  ┌─────────┬─────────┬──────────┐                                           │
│  │Variable │Constant │  Import  │                                           │
│  └─────────┴─────────┴──────────┘                                           │
│                                                                              │
│  DomainHistory:                                                              │
│  ┌─────────────┬─────────┬──────────┬─────────┬───────────┐                 │
│  │HistoryEntry │ Session │ Workflow │ Outcome │ Decision  │                 │
│  └─────────────┴─────────┴──────────┴─────────┴───────────┘                 │
│                                                                              │
│  DomainAcademic:                                                             │
│  ┌─────────┬───────────────┬──────────────┬─────────┬───────────────┐       │
│  │  Paper  │ Documentation │ BestPractice │   RFC   │ StackOverflow │       │
│  └─────────┴───────────────┴──────────────┴─────────┴───────────────┘       │
│  ┌─────────┬──────────┐                                                     │
│  │BlogPost │ Tutorial │                                                     │
│  └─────────┴──────────┘                                                     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Edge Types

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              EDGE TYPES                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  STRUCTURAL (Code Topology):                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ Calls ↔ CalledBy    │ Imports ↔ ImportedBy  │ Implements ↔ ImplementedBy│ │
│  │ Embeds              │ HasField              │ HasMethod                 │ │
│  │ Defines ↔ DefinedIn │ Returns               │ Receives                  │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  TEMPORAL (History):                                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ ProducedBy          │ ResultedIn            │ SimilarTo                 │ │
│  │ FollowedBy          │ Supersedes            │                           │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  CROSS-DOMAIN (Linking):                                                     │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ Modified            │ Created               │ Deleted                   │ │
│  │ BasedOn             │ References            │ ValidatedBy               │ │
│  │ Documents           │ UsesLibrary           │ ImplementsPattern         │ │
│  │ Cites               │ RelatedTo             │                           │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  SEMANTIC (Agent-Derived):                                                   │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ SimilarTo           │ RelatedTo             │ DerivedFrom               │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Core Data Structures

### Node Structure

```go
type Node struct {
    // Identity
    ID              uint32            // Globally unique (from global atomic counter)
    CanonicalKey    string            // Unique logical identifier for deduplication

    // Classification
    Domain          Domain            // Code, History, or Academic
    Type            NodeType          // Specific type within domain

    // Content
    Name            string            // Human-readable name
    Path            string            // File path or URL (optional)
    Package         string            // Package/module (optional)
    Signature       string            // Function signature (optional)

    // Provenance
    CreatedAt       uint64            // Unix nano timestamp
    SessionID       uint32            // Session that created this node
    CreatedBy       uint16            // Agent ID that created this node

    // Document reference (many-to-one: multiple nodes → one document)
    DocRef          uint32            // Document ID from DocIDMap (0 = no document)

    // Supersession chain (immutable history, no deletes)
    SupersededBy    uint32            // ID of newer version (0 = current)
    Supersedes      uint32            // ID of older version (0 = original)
}

// Binary header layout (32 bytes, zero padding, cache-line aligned):
//
//   Offset  Size  Field
//   0       4     ID           uint32
//   4       1     Domain       uint8
//   5       1     NodeType     uint8
//   6       2     CreatedBy    uint16
//   8       8     CreatedAt    uint64 (Unix nano)
//   16      4     SessionID    uint32
//   20      4     DocRef       uint32 (0 = no document)
//   24      4     SupersededBy uint32 (0 = current)
//   28      4     Supersedes   uint32 (0 = original)
//
// Followed by variable-length strings (2-byte length prefix each):
//   CanonicalKey, Name, Path, Package, Signature

// CanonicalKey format examples:
//   "repo:path/to/file.go:FunctionName:func"
//   "repo:path/to/file.go:StructName:struct"
//   "doi:10.1234/paper-id"
//   "url:https://example.com/doc"
//   "session:ses_abc:decision:12345"

// IMPORTANT: Append-only with supersession
//   • Nodes are NEVER deleted
//   • Updates create NEW node that supersedes old
//   • Old node remains for history (SupersededBy points to new)
//   • Query filters by SupersededBy == 0 for current view
```

### Edge Structure

```go
type Edge struct {
    // Identity (edge key)
    SourceID        uint32            // Source node ID
    TargetID        uint32            // Target node ID
    Type            EdgeType          // Relationship type (uint8)

    // Payload
    Weight          float32           // Computed weight (last-writer-wins)

    // Provenance
    SessionID       uint32            // Session that created/last updated
    AgentID         uint16            // Agent that created/last updated
    CreatedAt       uint64            // Unix nano timestamp (immutable)
    UpdatedAt       uint64            // Unix nano timestamp (mutable)
}

// Edge key for deduplication:
type edgeKey struct {
    src  uint32
    dst  uint32
    typ  uint8   // EdgeType compressed
}
// Total: 9 bytes, fits in register
```

### Version Structure

```go
type Version struct {
    ID              SemanticVersion   // Semantic version: v1.0.0, v1.1.0, v2.0.0
    ParentID        SemanticVersion   // Parent version (forms DAG), zero for v1.0.0
    Name            string            // Optional: "before-refactor"
    CreatedAt       time.Time
    Trigger         string            // "major", "minor", "patch", "implicit"

    // Stats snapshot at this version
    Stats           VersionStats
}

type VersionStats struct {
    NodesCreated    uint32
    EdgesCreated    uint32
    VectorsCreated  uint32
    DocsBytes       uint64
}

type VersionManifest struct {
    SessionID       uint32            // Numeric session ID
    Head            SemanticVersion   // Current version pointer (e.g., v1.0.2)
    Versions        []Version         // All versions (forms DAG)
}
```

### Session Structure

```go
type Session struct {
    Meta             *SessionMeta       // ID, StringID, Status, timestamps
    BaseSnapshot     *BaseSnapshot      // Global state at session start
    Manifest         *VersionManifest   // Version DAG with HEAD pointer
    DeltaTracker     *DeltaTracker      // Mutation delta tracker
    CheckpointCtrl   *CheckpointController // Adaptive checkpoint controller

    // Shared data files (session-local, append-only)
    NodeDataFile     *SharedDataFile    // Session node records
    VectorDataFile   *SharedDataFile    // Session vector records
    DocDataFile      *SharedDataFile    // Session doc records

    // Document identity mapping (session-local)
    DocIDMap         *DocIDMap          // String → uint32 doc ID mapping

    // Version stores (registered, reference shared data files)
    BleveStore       *VersionBleveStore // Per-version Bleve
    WAL              *SessionWAL        // Session WAL (nil if committed)
}

type GlobalSnapshot struct {
    GlobalVersion     SemanticVersion // Global KG version at session start
    CommittedSessions []uint32        // Session IDs committed before this session
    SnapshotAt        time.Time
    NextNodeID        uint32          // Global node counter at snapshot time
}

type SessionStatus string

const (
    SessionActive    SessionStatus = "active"
    SessionCommitted SessionStatus = "committed"
)

// GlobalMeta tracks the global knowledge graph state and version history.
// Stored in .sylk/meta.json
type GlobalMeta struct {
    SchemaVersion             int                    `json:"schema_version"`
    Version                   SemanticVersion        `json:"version"`           // Current global KG version
    NextNodeID                uint32                 `json:"next_node_id"`
    NextSessionID             uint32                 `json:"next_session_id"`
    CommittedSessions         []CommittedSession     `json:"committed_sessions"`
    Manifest                  *GlobalVersionManifest // Global version DAG
    LastBleveIndexedVersion   *SemanticVersion       // Last Bleve-indexed version
}

// CommittedSession records a session's contribution to the global KG.
type CommittedSession struct {
    SessionID     uint32          `json:"session_id"`
    FinalVersion  SemanticVersion `json:"final_version"`  // Session's version at commit time
    GlobalVersion SemanticVersion `json:"global_version"` // Global version after this commit
    CommittedAt   time.Time       `json:"committed_at"`
}
```

### Delta Tracker

```go
type DeltaTracker struct {
    LastCheckpointAt  time.Time
    LastCheckpointVer SemanticVersion   // Last checkpoint version (e.g., v1.0.1)

    // Current deltas (reset after each checkpoint)
    NodesCreated      atomic.Uint32
    EdgesCreated      atomic.Uint32
    EdgesModified     atomic.Uint32
    VectorsCreated    atomic.Uint32
    DocsBytes         atomic.Uint64

    // Configurable thresholds
    Thresholds        DeltaThresholds
}

type DeltaThresholds struct {
    Nodes       uint32        // Default: 50
    Edges       uint32        // Default: 200
    Vectors     uint32        // Default: 50
    DocsBytes   uint64        // Default: 512KB
    MaxInterval time.Duration // Default: 10 minutes
}

func (d *DeltaTracker) ShouldCheckpoint() bool {
    return d.NodesCreated.Load() >= d.Thresholds.Nodes ||
           d.EdgesCreated.Load()+d.EdgesModified.Load() >= d.Thresholds.Edges ||
           d.VectorsCreated.Load() >= d.Thresholds.Vectors ||
           d.DocsBytes.Load() >= d.Thresholds.DocsBytes ||
           time.Since(d.LastCheckpointAt) >= d.Thresholds.MaxInterval
}

func (d *DeltaTracker) Reset(newVersion SemanticVersion) {
    d.LastCheckpointAt = time.Now()
    d.LastCheckpointVer = newVersion
    d.NodesCreated.Store(0)
    d.EdgesCreated.Store(0)
    d.EdgesModified.Store(0)
    d.VectorsCreated.Store(0)
    d.DocsBytes.Store(0)
}
```

---

## Storage Primitives

The storage layer is built on four core primitives that provide crash-safe, lock-free read paths for all data types.

### SharedDataFile

Append-only data file with lock-free concurrent reads. All record types (nodes, vectors, documents) are stored in shared data files, one per data type.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          SHARED DATA FILE                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Format: Raw append-only byte stream (no header, no framing)               │
│                                                                              │
│  Records are self-describing via per-type conventions:                      │
│                                                                              │
│    Nodes:   [size:4][binary_header:32][variable_strings]                    │
│    Vectors: [nodeID:4][dim:4][float32 x dim]                               │
│    Docs:    [size:4][json_data:size]                                        │
│                                                                              │
│  Concurrency:                                                               │
│    Reads:  Lock-free (POSIX guarantees safe reads on append-only files)    │
│    Writes: Serialized via mutex, returns starting offset                   │
│    Size:   Tracked via atomic.Int64 for concurrent read access             │
│                                                                              │
│  Writes return the byte offset of the appended record. This offset is      │
│  stored in the version's OffsetIndex for O(1) lookup.                      │
│                                                                              │
│  Data files are shared across all versions. Versions reference records     │
│  via their OffsetIndex. Dead records are reclaimed by CompactTo.           │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### OffsetIndex

Dense `uint32 → int64` mapping for O(1) record lookup into SharedDataFile. Each version maintains its own OffsetIndex per data type.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           OFFSET INDEX                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Binary format:                                                             │
│    Magic:4      0x5849444F ("OIDX")                                        │
│    Version:4    1                                                           │
│    Capacity:4   uint32 (array size)                                        │
│    Count:4      uint32 (populated entries)                                  │
│    Entries:     [Capacity x 8 bytes] (int64, -1 = absent)                  │
│                                                                              │
│  Semantics:                                                                 │
│    index[nodeID] = byte offset into SharedDataFile                         │
│    index[nodeID] = -1 means no record for this ID                          │
│                                                                              │
│  Concurrency:                                                               │
│    Reads:  Lock-free via atomic.Pointer[indexState]                         │
│    Writes: Serialized via writeMu                                          │
│    Grow:   Doubles capacity on demand                                      │
│                                                                              │
│  Operations:                                                                │
│    Set(id, offset)       Write entry                                       │
│    Get(id) → offset      Lock-free read                                    │
│    Delete(id)            Set entry to -1                                    │
│    Clone(path) → new     Copy for checkpoint                               │
│    ForEach(fn)           Iterate populated entries                          │
│    MergeFrom(other)      Copy entries from another index                   │
│    RemapOffsets(map)      Translate offsets after compaction                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### DocIDMap

Bidirectional `string ↔ uint32` mapping for document identity. Documents have string IDs (file paths, UUIDs) but are indexed by uint32 in OffsetIndex and referenced by uint32 DocRef in Node headers.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            DOC ID MAP                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Binary format:                                                             │
│    Magic:4      0x444D4150 ("DMAP")                                        │
│    Version:4    1                                                           │
│    Count:4      uint32 (number of entries)                                  │
│    Next:4       uint32 (next assignable ID, starts at 1; 0 = none)         │
│    Entries:     [Count x variable-size entry]                               │
│      keyLen:2   uint16                                                      │
│      key:Var    string (document string ID)                                 │
│      id:4       uint32 (assigned numeric ID)                                │
│                                                                              │
│  Operations:                                                                │
│    GetOrAssign(stringID) → uint32    Assign new ID or return existing      │
│    Get(stringID) → (uint32, bool)    Lookup without assignment             │
│    Reverse(id) → string              uint32 → string lookup                │
│    Save() / Load()                   Persist / restore from disk           │
│                                                                              │
│  Usage:                                                                     │
│    Session: maps doc string IDs to session-local uint32 IDs                │
│    Global:  maps doc string IDs to global uint32 IDs                       │
│    At commit: session DocRef remapped to global via string intermediary    │
│                                                                              │
│  Node.DocRef relationship (many-to-one):                                   │
│    Multiple nodes can share the same DocRef (e.g., function, class,        │
│    and import nodes all referencing the same source file document).         │
│    DocRef = 0 means no associated document.                                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### EdgeShardStore

Sharded edge storage with O(1) fanout/fan-in lookups and deduplication. Replaces flat-file edge scanning.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         EDGE SHARD STORE                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Sharding: sourceID / 65536 → shard number                                 │
│                                                                              │
│  Per-shard directory:                                                       │
│    shard_NNNN/                                                              │
│    ├── edges.bin       35-byte fixed-size edge records (append-only)       │
│    ├── outgoing.idx    sourceID → []offset (fanout index)                  │
│    └── incoming.idx    targetID → []offset (fan-in index)                  │
│                                                                              │
│  Indexes:                                                                   │
│    outgoingIndex:  sourceID → []globalOffset    O(1) fanout lookup         │
│    incomingIndex:  targetID → []globalOffset    O(1) fan-in lookup         │
│    edgeKeyIndex:   (src, dst, type) → offset    O(1) dedup                 │
│                                                                              │
│  Global offset encoding:                                                    │
│    (shardNum << 48) | localOffset                                          │
│    Allows cross-shard offset uniqueness in a single uint64.                │
│                                                                              │
│  Edge record (35 bytes):                                                    │
│    [SourceID:4][TargetID:4][Type:1][Weight:4][SessionID:4]                 │
│    [AgentID:2][CreatedAt:8][UpdatedAt:8]                                   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### TombstoneBitmap

Per-version bitset tracking dead (superseded) node IDs. Used to filter out dead nodes and their associated documents during reads and compaction.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        TOMBSTONE BITMAP                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  File: versions/vX.Y.Z/tombstones.bin                                      │
│                                                                              │
│  Format: Raw bitset (bit N set = node N is dead)                           │
│                                                                              │
│  Loaded on demand, cached in sync.Map per version.                         │
│                                                                              │
│  Usage:                                                                     │
│    • Node reads: skip nodes where IsDead(nodeID) = true                    │
│    • Doc filtering: collect DocRef from live nodes → exclude orphaned docs  │
│    • Compaction: remove dead entries from OffsetIndex                       │
│                                                                              │
│  Doc filtering via DocRef:                                                  │
│    1. Load version's node OffsetIndex                                      │
│    2. Iterate live nodes (exclude tombstoned IDs)                          │
│    3. Read DocRef (4 bytes at record offset + 24) from SharedDataFile      │
│    4. Collect set of live DocRef values                                     │
│    5. Filter doc OffsetIndex to only entries in the live set               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Data Architecture Summary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    SHARED DATA + PER-VERSION INDEX                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SHARED (one per data type, append-only):                                  │
│                                                                              │
│    data/nodes/data.bin    ←── all node records, all versions               │
│    data/vectors/data.bin  ←── all vector records, all versions             │
│    data/docs/data.bin     ←── all doc records, all versions                │
│    data/docs/id_map.bin   ←── DocIDMap (string ↔ uint32)                   │
│    data/edges/            ←── EdgeShardStore (self-indexed)                │
│                                                                              │
│  PER-VERSION (index only, references shared data):                         │
│                                                                              │
│    versions/v3.0.0/nodes/index.bin     ←── OffsetIndex into nodes data    │
│    versions/v3.0.0/vectors/index.bin   ←── OffsetIndex into vectors data  │
│    versions/v3.0.0/docs/index.bin      ←── OffsetIndex into docs data     │
│    versions/v3.0.0/tombstones.bin      ←── Dead node IDs                  │
│    versions/v3.0.0/bleve/              ←── Bleve snapshot                  │
│                                                                              │
│  CHECKPOINT creates a new version by cloning the current OffsetIndex.      │
│  No data is copied — only the index (mapping IDs → offsets) is cloned.     │
│                                                                              │
│  COMPACTION walks live offsets → writes a compacted data file →            │
│  remaps ALL version indexes to new offsets → replaces the data file.       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## File Layout

`.sylk/` is **project-local** (like `.git`). Each project has its own Knowledge Graph, session storage, and version history.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            FILE LAYOUT                                       │
└─────────────────────────────────────────────────────────────────────────────┘

  /path/to/project/                      # User's project root
  │
  ├── .sylk/                             # PROJECT-LOCAL (like .git)
  │   │
  │   ├── config.yaml                    # Project-level configuration
  │   ├── lock                           # Exclusive process lock
  │   ├── meta.json                      # Global metadata (mutable)
  │   │   {
  │   │     "schema_version": 2,
  │   │     "version": {"major": 3, "minor": 0, "patch": 0},
  │   │     "next_node_id": 15234,
  │   │     "next_session_id": 4,
  │   │     "committed_sessions": [...]
  │   │   }
  │   │
  │   │
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │  SHARED DATA FILES (append-only, all versions reference these)
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │
  │   ├── data/
  │   │   ├── nodes/
  │   │   │   └── data.bin               # All node records
  │   │   │       Format: [size:4][header:32][strings:var]...
  │   │   │
  │   │   ├── vectors/
  │   │   │   └── data.bin               # All vector records
  │   │   │       Format: [nodeID:4][dim:4][float32 x dim]...
  │   │   │
  │   │   ├── docs/
  │   │   │   ├── data.bin               # All document records
  │   │   │   │   Format: [size:4][json_data:size]...
  │   │   │   └── id_map.bin             # DocIDMap (string ↔ uint32)
  │   │   │
  │   │   └── edges/                     # EdgeShardStore
  │   │       └── shard_NNNN/
  │   │           ├── edges.bin          # 35-byte edge records
  │   │           ├── outgoing.idx       # sourceID → []offset
  │   │           └── incoming.idx       # targetID → []offset
  │   │
  │   │
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │  GLOBAL VERSIONS (per-version indexes into shared data)
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │
  │   ├── versions/
  │   │   ├── manifest.json              # Global version DAG
  │   │   │
  │   │   ├── v1.0.0/                    # First committed version
  │   │   │   ├── meta.json
  │   │   │   ├── tombstones.bin         # Dead node IDs (bitset)
  │   │   │   ├── nodes/
  │   │   │   │   └── index.bin          # OffsetIndex → data/nodes/data.bin
  │   │   │   ├── vectors/
  │   │   │   │   └── index.bin          # OffsetIndex → data/vectors/data.bin
  │   │   │   ├── docs/
  │   │   │   │   └── index.bin          # OffsetIndex → data/docs/data.bin
  │   │   │   └── bleve/                 # Bleve snapshot for this version
  │   │   │
  │   │   ├── v2.0.0/                    # Second committed version
  │   │   │   └── ...
  │   │   │
  │   │   └── v3.0.0/                    # Current HEAD
  │   │       └── ...
  │   │
  │   ├── wal/                           # Commit WAL directory
  │   │
  │   │
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │  SESSIONS (isolated per-session storage with versioning)
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │
  │   └── sessions/
  │       │
  │       ├── active -> ses_003          # Symlink to current session
  │       │
  │       ├── ses_001/                   # Session 1 (committed)
  │       │   ├── meta.json
  │       │   │   {
  │       │   │     "id": 1,
  │       │   │     "string_id": "ses_001",
  │       │   │     "created_at": "2025-01-25T10:00:00Z",
  │       │   │     "status": "committed",
  │       │   │     "committed_at": "2025-01-25T18:00:00Z"
  │       │   │   }
  │       │   │
  │       │   ├── base/
  │       │   │   └── snapshot.json      # Global state at session start
  │       │   │
  │       │   └── versions/
  │       │       ├── manifest.json      # Version DAG for this session
  │       │       ├── v1.0.0/            # Session start (always v1.0.0)
  │       │       ├── v1.0.1/            # First checkpoint (patch)
  │       │       ├── v1.1.0/            # Minor checkpoint
  │       │       └── ...
  │       │
  │       ├── ses_002/                   # Session 2 (committed)
  │       │   └── ...
  │       │
  │       └── ses_003/                   # Session 3 (active)
  │           ├── meta.json
  │           │   {
  │           │     "id": 3,
  │           │     "string_id": "ses_003",
  │           │     "created_at": "2025-01-26T10:00:00Z",
  │           │     "status": "active"
  │           │   }
  │           │
  │           ├── base/
  │           │   └── snapshot.json
  │           │       {
  │           │         "committed_sessions": [1, 2],
  │           │         "snapshot_at": "2025-01-26T10:00:00Z",
  │           │         "next_node_id": 15234
  │           │       }
  │           │
  │           ├── data/                  # Session-local shared data files
  │           │   ├── nodes/
  │           │   │   └── data.bin       # Session node records
  │           │   ├── vectors/
  │           │   │   └── data.bin       # Session vector records
  │           │   └── docs/
  │           │       ├── data.bin       # Session doc records
  │           │       └── id_map.bin     # Session DocIDMap
  │           │
  │           ├── versions/
  │           │   ├── manifest.json
  │           │   │   {
  │           │   │     "session_id": 3,
  │           │   │     "head": {"major": 1, "minor": 0, "patch": 2},
  │           │   │     "versions": [...]
  │           │   │   }
  │           │   │
  │           │   ├── v1.0.0/            # Initial version
  │           │   │   ├── meta.json
  │           │   │   ├── nodes/
  │           │   │   │   └── index.bin  # OffsetIndex → session data
  │           │   │   ├── edges/
  │           │   │   │   └── data.bin   # Per-version edge data
  │           │   │   ├── vectors/
  │           │   │   │   └── index.bin  # OffsetIndex → session data
  │           │   │   ├── docs/
  │           │   │   │   └── index.bin  # OffsetIndex → session data
  │           │   │   └── bleve/         # Bleve snapshot
  │           │   │
  │           │   ├── v1.0.1/
  │           │   │   └── ...
  │           │   │
  │           │   └── v1.0.2/            # HEAD
  │           │       └── ...
  │           │
  │           ├── delta/
  │           │   └── tracker.json       # Current delta for auto-checkpoint
  │           │
  │           ├── state/                 # Session state
  │           │   ├── dag.json           # Architect's task DAG
  │           │   └── orchestrator.json  # Pipeline scheduling state
  │           │
  │           ├── agents/                # Agent contexts
  │           │   ├── librarian.ctx
  │           │   ├── academic.ctx
  │           │   └── archivalist.ctx
  │           │
  │           ├── wal/                   # Session WAL
  │           │
  │           └── messages/              # Conversation history
  │               └── log.jsonl
  │
  ├── src/                               # Project source code
  ├── go.mod
  └── ...
```

---

## Query Resolution

### Visibility Rules

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         VISIBILITY RULES                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Query from Session S at HEAD version V:                                    │
│                                                                              │
│  STEP 1: Determine visible sources                                          │
│                                                                              │
│    Visible data comes from:                                                  │
│      a) Global committed state (sessions in S.BaseSnapshot)                 │
│      b) Session S's versions that are ancestors of V                        │
│                                                                              │
│    NOT visible:                                                              │
│      a) Other active sessions' uncommitted work                             │
│      b) Sessions committed AFTER S started                                  │
│      c) Session S's versions NOT in ancestor chain of V                     │
│                                                                              │
│  STEP 2: Resolve entity visibility                                          │
│                                                                              │
│    Node visible if:                                                          │
│      • Node exists in visible sources                                       │
│      • Node.SupersededBy == 0 (is current version)                         │
│        OR Node.SupersededBy not in visible sources (superseding node        │
│           was created in a session we can't see)                            │
│      • Node.ID not in deletions.json of any visible version                 │
│                                                                              │
│    Edge visible if:                                                          │
│      • Edge exists in visible sources                                       │
│      • Edge.SourceID and Edge.TargetID are both visible                    │
│      • Edge key not in deletions.json of any visible version               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Query Resolution Algorithm

```go
type QueryContext struct {
    SessionID         uint32
    HeadVersion       SemanticVersion       // Current HEAD (e.g., v1.0.2)
    AncestorVersions  []SemanticVersion     // Versions in HEAD's ancestor chain
    BaseSnapshot      *GlobalSnapshot
}

func (s *Session) BuildQueryContext() *QueryContext {
    return &QueryContext{
        SessionID:        s.ID,
        HeadVersion:      s.VersionManifest.Head,
        AncestorVersions: s.GetAncestorChain(),
        BaseSnapshot:     &s.BaseSnapshot,
    }
}

// GetNode with visibility filtering
func (store *VersionedStore) GetNode(ctx *QueryContext, id uint32) *Node {
    // 1. Check deletions in session versions
    if store.isDeletedInSession(ctx, id, "node") {
        return nil
    }

    // 2. Search session versions (newest first via ancestor chain)
    for _, version := range ctx.AncestorVersions {
        if node := store.loadNodeFromVersion(ctx.SessionID, version, id); node != nil {
            return node
        }
    }

    // 3. Search global committed state
    for _, committedSessionID := range ctx.BaseSnapshot.CommittedSessions {
        if node := store.loadNodeFromCommittedSession(committedSessionID, id); node != nil {
            // Check if superseded by something we can see
            if node.SupersededBy != 0 && store.canSee(ctx, node.SupersededBy) {
                continue // Skip, we have a newer version
            }
            return node
        }
    }

    // 4. Search global base (pre-session data)
    return store.loadNodeFromGlobalBase(id)
}

func (store *VersionedStore) canSee(ctx *QueryContext, nodeID uint32) bool {
    // Check if nodeID exists in any visible source
    // ... implementation
}
```

---

## Merge Process

### When Merge Happens

Merge to global occurs ONLY when:
1. Session ends (implicit commit)
2. User explicitly runs `/commit` command

### Node ID Allocation

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      GLOBAL NODE ID ALLOCATION                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  All node IDs come from a GLOBAL atomic counter, regardless of session.     │
│                                                                              │
│    Session A creates node → requests ID from global → gets 1001             │
│    Session B creates node → requests ID from global → gets 1002             │
│                                                                              │
│  IDs are globally unique. No remapping needed at merge time.                │
│                                                                              │
│  Storage: meta.json: { "next_node_id": 1003, ... }                │
│                                                                              │
│  Rolled-back session versions may "waste" IDs (acceptable tradeoff).        │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Canonical Key Deduplication

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                   CANONICAL KEY DEDUPLICATION                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  CanonicalKey uniquely identifies a logical entity:                         │
│    "repo:path/to/file.go:FunctionName:func"                                 │
│    "doi:10.1234/paper-id"                                                   │
│    "session:ses_abc:decision:12345"                                         │
│                                                                              │
│  WITHIN SESSION:                                                             │
│    Canonical key checked at node creation.                                  │
│    If exists in session's visible data → return existing ID (no duplicate) │
│                                                                              │
│  AT MERGE TIME:                                                              │
│    If session node's canonical key exists in global:                        │
│      → SUPERSESSION: new version of existing entity                         │
│      → Old node.SupersededBy = new node.ID                                  │
│      → New node.Supersedes = old node.ID                                    │
│      → BOTH nodes persist (no data loss)                                    │
│      → Canonical key index updated to point to new node                     │
│                                                                              │
│    If canonical key does NOT exist in global:                               │
│      → NEW entity, append to global                                         │
│      → Add to canonical key index                                           │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Supersession Model

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       SUPERSESSION MODEL                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Nodes are NEVER deleted. Updates create supersession chains.               │
│                                                                              │
│  Example: Function "ParseConfig" indexed across three sessions:             │
│                                                                              │
│    Node 100: {                                                              │
│      CanonicalKey: "repo:config.go:ParseConfig:func",                       │
│      SupersededBy: 500,                                                     │
│      Supersedes: 0                                                          │
│    } ← Original (Session 1)                                                 │
│                                                                              │
│    Node 500: {                                                              │
│      CanonicalKey: "repo:config.go:ParseConfig:func",                       │
│      SupersededBy: 1200,                                                    │
│      Supersedes: 100                                                        │
│    } ← Version 2 (Session 2)                                                │
│                                                                              │
│    Node 1200: {                                                             │
│      CanonicalKey: "repo:config.go:ParseConfig:func",                       │
│      SupersededBy: 0,                                                       │
│      Supersedes: 500                                                        │
│    } ← Current (Session 3)                                                  │
│                                                                              │
│  Query "ParseConfig":                                                        │
│    → Returns Node 1200 (SupersededBy == 0 means current)                   │
│                                                                              │
│  Query "history of ParseConfig":                                            │
│    → Walk Supersedes chain: 1200 → 500 → 100                               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Merge Algorithm

```go
func (s *Session) CommitToGlobal(kg *KnowledgeGraph) error {
    // 1. Collect entities from session's HEAD ancestor chain only
    ancestorChain := s.computeAncestorChain(s.VersionManifest.Head)
    nodes, edges, vectors, docs := s.collectEntitiesFromVersions(ancestorChain)

    // 2. Build supersession map for nodes
    supersessionMap := make(map[uint32]uint32) // oldGlobalID → newSessionID

    for _, node := range nodes {
        existingID, exists := kg.FindNodeByCanonicalKey(node.CanonicalKey)
        if exists {
            // Mark existing as superseded
            kg.SetSupersededBy(existingID, node.ID)
            node.Supersedes = existingID
            supersessionMap[existingID] = node.ID

            // Update canonical key index to point to new version
            kg.UpdateCanonicalKeyIndex(node.CanonicalKey, node.ID)
        } else {
            // New canonical key
            kg.AddCanonicalKeyIndex(node.CanonicalKey, node.ID)
        }

        // Append node to global
        kg.AppendNode(node)
    }

    // 3. Process edges
    for _, edge := range edges {
        // Resolve source/target to current versions
        srcID := resolveCurrentVersion(edge.SourceID, supersessionMap, kg)
        dstID := resolveCurrentVersion(edge.TargetID, supersessionMap, kg)

        // Append original edge (for history)
        kg.AppendEdge(edge)

        // If either endpoint was superseded, create edge with current versions
        if srcID != edge.SourceID || dstID != edge.TargetID {
            currentEdge := Edge{
                SourceID:  srcID,
                TargetID:  dstID,
                Type:      edge.Type,
                Weight:    edge.Weight,
                SessionID: edge.SessionID,
                AgentID:   edge.AgentID,
                CreatedAt: edge.CreatedAt,
                UpdatedAt: time.Now().UnixNano(),
            }
            kg.UpsertEdge(currentEdge) // Handles dedup by edge key
        }
    }

    // 4. Remap DocRef (session → global address space)
    //    Session and global have independent DocIDMaps.
    //    The same doc string ID maps to different uint32 values.
    //    Translation: sessDocIDMap.Reverse(node.DocRef) → string
    //                 → globalDocIDMap.GetOrAssign(string) → global uint32
    for _, node := range nodes {
        if node.DocRef != 0 {
            docStringID := sessDocIDMap.Reverse(node.DocRef)
            node.DocRef = globalDocIDMap.GetOrAssign(docStringID)
        }
    }

    // 5. Process vectors
    for _, vector := range vectors {
        kg.AppendVector(vector)
    }

    // 6. Index documents in global Bleve
    //    On supersession: resolve old node's DocRef → string ID for Bleve delete
    for _, doc := range docs {
        kg.IndexDocument(doc)
    }

    // 7. Apply deletions
    deletions := s.collectDeletionsFromVersions(ancestorChain)
    for _, nodeID := range deletions.Nodes {
        kg.MarkNodeDeleted(nodeID, s.ID)
    }
    for _, edgeKey := range deletions.Edges {
        kg.MarkEdgeDeleted(edgeKey, s.ID)
    }

    // 8. Update global metadata
    kg.RegisterCommittedSession(CommittedSession{
        SessionID:    s.ID,
        FinalVersion: s.VersionManifest.Head,
        CommittedAt:  time.Now(),
    })

    // 9. Mark session as committed
    s.Meta.Status = SessionCommitted
    s.Meta.CommittedAt = time.Now()

    return s.persistMeta()
}

func resolveCurrentVersion(id uint32, sessionMap map[uint32]uint32, kg *KnowledgeGraph) uint32 {
    // Check if superseded within this merge
    if newID, ok := sessionMap[id]; ok {
        return newID
    }
    // Check global supersession chain
    return kg.GetCurrentVersion(id)
}
```

---

## Write Operations

### Node Creation Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          NODE CREATION FLOW                                  │
└─────────────────────────────────────────────────────────────────────────────┘

  Agent calls: AddNode(canonicalKey, nodeData)
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Check session's visible data        │
  │     for existing canonical key          │
  └─────────────────────────────────────────┘
        │
        ├──── EXISTS ────▶ Return existing ID (no duplicate)
        │
        │ NOT EXISTS
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Allocate node ID from GLOBAL        │
  │     counter (atomic)                    │
  │     newID := globalNextNodeID.Add(1)-1  │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Assign DocRef via DocIDMap          │
  │     docRef := docIDMap.GetOrAssign(key) │
  │     node.DocRef = docRef                │
  │     (file nodes + symbol nodes inherit  │
  │      parent file's DocRef)              │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Append to shared data file          │
  │     data/nodes/data.bin → offset        │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  5. Update version's OffsetIndex        │
  │     versions/vX.Y.Z/nodes/index.bin     │
  │     index.Set(nodeID, offset)           │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  6. Update delta tracker                │
  │     deltaTracker.NodesCreated.Add(1)    │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  7. Check auto-checkpoint               │
  │     if deltaTracker.ShouldCheckpoint(): │
  │         session.Checkpoint("")          │
  └─────────────────────────────────────────┘
        │
        ▼
  Return (newID, created=true)
```

### Edge Upsert Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           EDGE UPSERT FLOW                                   │
└─────────────────────────────────────────────────────────────────────────────┘

  Agent calls: UpsertEdge(src, dst, type, weight, agentID)
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Validate source and target exist    │
  │     in session's visible data           │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Construct edge key                  │
  │     key = {src, dst, type}              │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Check if edge exists in visible     │
  │     data (session + global snapshot)    │
  └─────────────────────────────────────────┘
        │
        ├──── EXISTS ────▶ Update weight, timestamp in current version
        │
        │ NOT EXISTS
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Create new edge in current version  │
  │     versions/vNNNNNN/edges/data.bin     │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  5. Update delta tracker                │
  │     deltaTracker.EdgesCreated.Add(1)    │
  │     OR EdgesModified.Add(1)             │
  └─────────────────────────────────────────┘
        │
        ▼
  Return edge
```

### Concurrent Write Flow

When indexing content, three independent write paths execute in parallel:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    CONCURRENT WRITE FLOW                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Agent indexes entity (e.g., function "ParseConfig")                        │
│                                                                              │
│                              │                                               │
│                              ▼                                               │
│              ┌───────────────────────────────┐                              │
│              │  nodeID = globalCounter.Add(1) │  ← atomic allocation        │
│              │  docRef = docIDMap.GetOrAssign()│  ← doc identity             │
│              │  embedding = embed(content)    │                              │
│              └───────────────────────────────┘                              │
│                              │                                               │
│          ┌───────────────────┼───────────────────┐                          │
│          │                   │                   │                          │
│          ▼                   ▼                   ▼                          │
│  ┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐            │
│  │ NODE + VECTOR    │ │ BLEVE DOC DB     │ │ EDGES            │            │
│  │ (goroutine 1)    │ │ (goroutine 2)    │ │ (goroutine 3)    │            │
│  │                  │ │                  │ │                  │            │
│  │ Write to version │ │ Queue for async  │ │ Create edges to  │            │
│  │ folder           │ │ indexing         │ │ related nodes    │            │
│  └────────┬─────────┘ └────────┬─────────┘ └────────┬─────────┘            │
│           │                    │                    │                       │
│           └────────────────────┴────────────────────┘                       │
│                              │                                               │
│                              ▼                                               │
│                    sync.WaitGroup.Wait()                                    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## WAL Record Format

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          WAL RECORD FORMAT                                   │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                         RECORD HEADER (20 bytes)                          │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │  Length    │   Type     │  Checksum  │ Timestamp  │     SessionID        │
  │  (4 bytes) │  (1 byte)  │  (4 bytes) │  (6 bytes) │     (4 bytes)        │
  ├────────────┴────────────┴────────────┴────────────┴──────────────────────┤
  │  VersionID │  Reserved                                                    │
  │  (4 bytes) │  (1 byte)                                                    │
  └──────────────────────────────────────────────────────────────────────────┘

  Record Types:
    0x01 = NodeInsert
    0x02 = NodeDelete (soft delete)
    0x03 = EdgeInsert
    0x04 = EdgeUpdate
    0x05 = EdgeDelete (soft delete)
    0x06 = VectorInsert
    0x10 = VersionCheckpoint
    0x11 = SessionCommit
    0x20 = ShardSeal

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                      NODE INSERT RECORD                                   │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │  NodeID    │  Domain    │   Type     │ CanonKeyLen│   CanonicalKey       │
  │  (4 bytes) │  (1 byte)  │  (1 byte)  │  (2 bytes) │     (variable)       │
  ├────────────┴────────────┴────────────┴────────────┴──────────────────────┤
  │  NameLen  │  Name  │  PathLen  │   Path   │  Supersedes  │  ... more     │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                      EDGE INSERT/UPDATE RECORD                            │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │  SourceID  │  TargetID  │   Type     │   Weight   │      AgentID         │
  │  (4 bytes) │  (4 bytes) │  (1 byte)  │  (4 bytes) │      (2 bytes)       │
  └────────────┴────────────┴────────────┴────────────┴──────────────────────┘

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                    VERSION CHECKPOINT RECORD                              │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │ VersionID  │ ParentID   │ NameLen    │   Name     │      Trigger         │
  │  (4 bytes) │  (4 bytes) │  (2 bytes) │ (variable) │      (1 byte)        │
  ├────────────┴────────────┴────────────┴────────────┴──────────────────────┤
  │  NodesCreated  │  EdgesCreated  │  VectorsCreated  │  DocsBytes          │
  │   (4 bytes)    │   (4 bytes)    │    (4 bytes)     │   (8 bytes)         │
  └──────────────────────────────────────────────────────────────────────────┘
```

---

## Recovery Process

### Startup Recovery Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         STARTUP RECOVERY FLOW                                │
└─────────────────────────────────────────────────────────────────────────────┘

  Application starts
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Load global metadata                │
  │     - Read meta.json                    │
  │     - Verify schema version             │
  │     - Load committed_sessions list      │
  │     - Load next_node_id counter         │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Load global node/edge/vector data   │
  │     (PARALLEL)                          │
  │                                         │
  │     FOR each shard:                     │
  │       - mmap shard files                │
  │       - Verify checksums                │
  │       - Build in-memory indices         │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Load canonical key index            │
  │     data/nodes/ canonical key lookup    │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Check for active session            │
  │     sessions/active symlink             │
  └─────────────────────────────────────────┘
        │
        ├──── NO ACTIVE SESSION ────▶ Ready (clean start)
        │
        │ ACTIVE SESSION EXISTS
        ▼
  ┌─────────────────────────────────────────┐
  │  5. Load active session state           │
  │     - Read sessions/{id}/meta.json      │
  │     - Load versions/manifest.json       │
  │     - Load delta/tracker.json           │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  6. Recover session version data        │
  │     (PARALLEL per version folder)       │
  │                                         │
  │     FOR each version in manifest:       │
  │       - Load version meta.json          │
  │       - Load nodes/edges/vectors        │
  │       - Build session-local indices     │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  7. Validate HEAD version               │
  │     - Ensure HEAD points to valid ver   │
  │     - Rebuild ancestor chain            │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  8. Resume delta tracking               │
  │     - Restore tracker state             │
  │     - Ready for new writes              │
  └─────────────────────────────────────────┘
        │
        ▼
  Ready for operations
```

### Crash Recovery Guarantees

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CRASH RECOVERY GUARANTEES                               │
└─────────────────────────────────────────────────────────────────────────────┘

  DURABILITY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Every checkpoint persists version folder to disk                    │
  │  • Version folders are immutable once created                          │
  │  • Manifest updates are atomic (write-rename)                          │
  │  • Global commits are atomic (update meta.json last)                   │
  │                                                                         │
  │  GUARANTEE: No committed data is ever lost                             │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  CONSISTENCY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Node IDs are globally unique (atomic counter)                       │
  │  • Canonical keys are unique within visible scope                      │
  │  • Version parent pointers form a valid DAG                            │
  │  • Supersession chains are consistent                                  │
  │                                                                         │
  │  GUARANTEE: Graph is always in consistent state after recovery         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  ATOMICITY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Checkpoint: version folder created atomically                       │
  │  • Commit: global merge is all-or-nothing                              │
  │  • Crash during checkpoint: incomplete version discarded               │
  │  • Crash during commit: session stays uncommitted, can retry           │
  │                                                                         │
  │  GUARANTEE: Each operation fully completes or fully rolls back         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  NO DATA LOSS:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Nodes are never deleted (supersession model)                        │
  │  • Edges are soft-deleted (recorded in deletions.json)                 │
  │  • All versions persist (including orphaned branches)                  │
  │  • Committed sessions folders are preserved for audit                  │
  │                                                                         │
  │  GUARANTEE: Historical data is always recoverable                      │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Concurrency Model

### Lock Hierarchy

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          LOCK HIERARCHY                                      │
└─────────────────────────────────────────────────────────────────────────────┘

  Level 0 (Global - rare):
    └── globalNodeIDMu: Only when persisting next_node_id counter
    └── committedSessionsMu: Only when adding committed session

  Level 1 (Session - per session):
    └── session.manifestMu: When updating version manifest
    └── session.deltaMu: When updating delta tracker

  Level 2 (Version - per version folder):
    └── version.writeMu: Serializes writes to version folder

  LOCK-FREE OPERATIONS:
    ├── Node reads: Walk version chain, no locks
    ├── Edge reads: Walk version chain, no locks
    ├── Version checkout: Atomic pointer update
    └── Query context build: Read-only snapshot

  RULES:
    1. Sessions don't share locks (fully isolated)
    2. Version folders are immutable once checkpoint completes
    3. Only active version accepts writes
    4. Global state updated only at commit time
```

### Session Concurrency Model

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      SESSION CONCURRENCY MODEL                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SESSION A                              SESSION B                            │
│  ┌────────────────────────────┐        ┌────────────────────────────┐       │
│  │ ┌──────────┐ ┌──────────┐  │        │ ┌──────────┐ ┌──────────┐  │       │
│  │ │Librarian │ │ Academic │  │        │ │Librarian │ │ Academic │  │       │
│  │ │(instance)│ │(instance)│  │        │ │(instance)│ │(instance)│  │       │
│  │ └────┬─────┘ └────┬─────┘  │        │ └────┬─────┘ └────┬─────┘  │       │
│  │      │            │        │        │      │            │        │       │
│  │      └────────────┴────────┤        │      └────────────┴────────┤       │
│  │                            │        │                            │       │
│  │  Writes to:                │        │  Writes to:                │       │
│  │  sessions/A/versions/      │        │  sessions/B/versions/      │       │
│  │                            │        │                            │       │
│  │  Reads from:               │        │  Reads from:               │       │
│  │  • Session A versions      │        │  • Session B versions      │       │
│  │  • Global snapshot at A    │        │  • Global snapshot at B    │       │
│  │    start                   │        │    start                   │       │
│  │                            │        │                            │       │
│  │  CANNOT see Session B      │        │  CANNOT see Session A      │       │
│  └────────────────────────────┘        └────────────────────────────┘       │
│               │                                      │                       │
│               │                                      │                       │
│               │         /commit                      │                       │
│               └──────────────┬───────────────────────┘                       │
│                              │                                               │
│                              ▼                                               │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      GLOBAL KNOWLEDGE GRAPH                          │    │
│  │                                                                      │    │
│  │   Receives merged data only when sessions explicitly commit.        │    │
│  │   Concurrent commits serialized via committedSessionsMu.            │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Why This Works

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    ISOLATION GUARANTEES                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. NO CROSS-SESSION POLLUTION                                               │
│     • Sessions write to isolated folder structures                          │
│     • Sessions read from their own data + global snapshot at start          │
│     • Concurrent sessions cannot see each other's uncommitted work          │
│                                                                              │
│  2. DETERMINISTIC SESSION BEHAVIOR                                           │
│     • Session's visible data is fixed at session start                      │
│     • No "phantom reads" from concurrent session commits                    │
│     • Same operations produce same results within session                   │
│                                                                              │
│  3. EXPLICIT GLOBAL ACCESS                                                   │
│     • /query-global explicitly accesses current global state                │
│     • User controls when to see new committed data                          │
│     • Import is explicit, not automatic                                     │
│                                                                              │
│  4. SAFE CONCURRENT COMMITS                                                  │
│     • Commits are serialized at global level                                │
│     • Supersession handles same canonical key from different sessions       │
│     • No data loss, both versions preserved                                 │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Performance Targets

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         PERFORMANCE TARGETS                                  │
└─────────────────────────────────────────────────────────────────────────────┘

  LATENCY (p99):
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Operation              │  Target      │  Notes                        │
  ├─────────────────────────┼──────────────┼───────────────────────────────┤
  │  GetNode (session)      │  < 500ns     │  Walk version chain           │
  │  GetNode (global)       │  < 100ns     │  Direct lookup                │
  │  FindNode (canonical)   │  < 1μs       │  Index lookup                 │
  │  AddNode                │  < 10μs      │  Version folder + index       │
  │  UpsertEdge             │  < 5μs       │  Version folder               │
  │  Checkpoint (explicit)  │  < 50ms      │  Persist version folder       │
  │  Checkout               │  < 1μs       │  Update HEAD pointer          │
  │  Traverse (depth=3)     │  < 100μs     │  Depends on branching         │
  │  VectorSearch (k=20)    │  < 50ms      │  IVF + reranking              │
  └────────────────────────────────────────────────────────────────────────┘

  THROUGHPUT (per session):
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Operation              │  Target         │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  Node creation          │  > 10K ops/sec  │  Version folder bound      │
  │  Edge upserts           │  > 50K ops/sec  │  In-memory + batch write   │
  │  Vector inserts         │  > 5K ops/sec   │  IVF partitioning bound    │
  │  Checkpoints            │  > 10/sec       │  Folder creation bound     │
  └────────────────────────────────────────────────────────────────────────┘

  COMMIT PERFORMANCE:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Session Size           │  Commit Time    │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  1K nodes, 5K edges     │  < 100ms        │  Small session             │
  │  10K nodes, 50K edges   │  < 1 second     │  Medium session            │
  │  100K nodes, 500K edges │  < 10 seconds   │  Large session             │
  └────────────────────────────────────────────────────────────────────────┘

  RECOVERY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  State                  │  Recovery Time  │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  Clean start            │  < 500ms        │  Load global only          │
  │  Active session (small) │  < 1 second     │  + load session versions   │
  │  Active session (large) │  < 5 seconds    │  + rebuild indices         │
  └────────────────────────────────────────────────────────────────────────┘

  MEMORY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Component              │  Size per unit  │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  Node (in-memory)       │  ~250 bytes     │  Includes supersession     │
  │  Edge (in-memory)       │  ~32 bytes      │  Fixed size                │
  │  Version metadata       │  ~200 bytes     │  Per version               │
  │  Vector (768-dim)       │  ~3KB raw       │  96 bytes BBQ compressed   │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Boot Pipeline

The boot pipeline initializes the Knowledge Graph on first run and performs incremental updates on subsequent runs.

### Boot Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           BOOT PIPELINE                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  sylk init / sylk (first run in project)                                    │
│         │                                                                    │
│         ▼                                                                    │
│  ┌───────────────────────────────────────┐                                  │
│  │  1. DETECT GIT ROOT                   │                                  │
│  │     Walk up from cwd looking for .git │                                  │
│  │     If not found: use cwd as root     │                                  │
│  └───────────────────────────────────────┘                                  │
│         │                                                                    │
│         ▼                                                                    │
│  ┌───────────────────────────────────────┐                                  │
│  │  2. CHECK .sylk/ EXISTS               │                                  │
│  │     EXISTS?  → Load existing state    │                                  │
│  │     MISSING? → Create .sylk/          │                                  │
│  └───────────────────────────────────────┘                                  │
│         │                                                                    │
│         ▼                                                                    │
│  ┌───────────────────────────────────────┐                                  │
│  │  3. CREATE NEW SESSION                │                                  │
│  │     • Generate session ID             │                                  │
│  │     • Snapshot current global state   │                                  │
│  │     • Initialize version manifest     │                                  │
│  │     • Create v000001 (session_start)  │                                  │
│  └───────────────────────────────────────┘                                  │
│         │                                                                    │
│         ▼                                                                    │
│  ┌───────────────────────────────────────┐                                  │
│  │  4. INDEX CODEBASE (if needed)        │                                  │
│  │     • Discover source files           │                                  │
│  │     • Parse with tree-sitter          │                                  │
│  │     • Embed symbols                   │                                  │
│  │     • Write to session versions       │                                  │
│  │     • Auto-checkpoint on thresholds   │                                  │
│  └───────────────────────────────────────┘                                  │
│         │                                                                    │
│         ▼                                                                    │
│  ┌───────────────────────────────────────┐                                  │
│  │  5. READY FOR INTERACTION             │                                  │
│  │     • Session active                  │                                  │
│  │     • Agents can query/write          │                                  │
│  │     • User can checkpoint/checkout    │                                  │
│  └───────────────────────────────────────┘                                  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Terminal Commands

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         TERMINAL COMMANDS                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  VERSION MANAGEMENT:                                                         │
│                                                                              │
│    /checkpoint [name]       Create named checkpoint at current state        │
│                             Example: /checkpoint "before-refactor"          │
│                                                                              │
│    /checkout <version>      Switch HEAD to specified version                │
│                             Example: /checkout v000003                      │
│                             Example: /checkout "before-refactor"            │
│                                                                              │
│    /versions                List all versions in current session            │
│                             Shows: ID, name, timestamp, parent              │
│                                                                              │
│    /diff <v1> [v2]          Show changes between versions                   │
│                             If v2 omitted, diff against HEAD                │
│                                                                              │
│  SESSION MANAGEMENT:                                                         │
│                                                                              │
│    /commit                  Merge current session to global                 │
│                             Final, cannot be undone                         │
│                                                                              │
│    /status                  Show session status, HEAD, delta stats          │
│                                                                              │
│    /sessions                List all sessions (active and committed)        │
│                                                                              │
│  GLOBAL QUERIES:                                                             │
│                                                                              │
│    /query-global <search>   Query current global state                      │
│                             (bypasses session isolation)                    │
│                                                                              │
│    /import <node-id>        Import node from global into session            │
│                             (creates reference, not copy)                   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Configuration

### .sylk/config.yaml

```yaml
# Sylk project configuration

# Schema version (do not modify)
schema_version: 2

# Checkpoint thresholds for auto-checkpoint
checkpoint:
  nodes_threshold: 50
  edges_threshold: 200
  docs_bytes_threshold: 524288  # 512KB
  max_interval: 10m

# Embedding configuration
embedder:
  source: "hybrid-local"  # or "voyage-api"
  model: "voyage-3-lite"
  batch_size: 128

# Session configuration
session:
  auto_commit_on_exit: false
  preserve_orphan_versions: true

# Performance tuning
performance:
  parse_workers: 0  # 0 = auto (NumCPU)
  embed_workers: 4
  index_workers: 2
```

---

## References

- Vamana: DiskANN paper for graph-based ANN
- IVF: Inverted file indexing for vector search
- BBQ: Binary Quantization for compressed vectors
- MVCC: Multi-Version Concurrency Control (inspiration for versioning)
- Git: Object model and branching concepts

---

*Last updated: 2026-01-31*
