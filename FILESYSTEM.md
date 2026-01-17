# Filesystem Architecture: MVCC + Operational Transformation with AST-Aware Targeting

This document describes the architecture for a robust, correct, performant, and parallelizable multi-session agentic code editing system.

## Overview

The system combines three proven paradigms:
- **Git's content model**: Content-addressable storage, DAG history, merge commits
- **Google Docs' OT engine**: Real-time operational transformation, fine-grained operations, automatic merge
- **Database MVCC**: Readers never block, writers only conflict on actual overlap

## Architecture Diagram

```
┌────────────────────────────────────────────────────────────────────────────┐
│                        Central Version Store (CVS)                         │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐ │
│  │ File: handler.go                                                      │ │
│  │                                                                       │ │
│  │ v1 ───► v2 ───► v3 ───┬───► v5 (auto-merged)                         │ │
│  │                       │                                               │ │
│  │                       └───► v4 (Pipeline B, concurrent)               │ │
│  └──────────────────────────────────────────────────────────────────────┘ │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐ │
│  │ Operations Log (append-only, content-addressable)                     │ │
│  │                                                                       │ │
│  │ op1: v1→v2  Pipeline A  Insert(func:Foo, body, <content>)            │ │
│  │ op2: v2→v3  Pipeline A  Replace(func:Bar, params, <content>)         │ │
│  │ op3: v2→v4  Pipeline B  Replace(func:Baz, body, <content>)           │ │
│  │ op4: merge  v3+v4→v5   OT(op3, against=[op2]) → op3'                 │ │
│  └──────────────────────────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────────────────────────┘
                    ▲              ▲              ▲
                    │              │              │
              ┌─────┴─────┐  ┌─────┴─────┐  ┌─────┴─────┐
              │ Session A │  │ Session B │  │ Session C │
              │  Pipes    │  │  Pipes    │  │  Pipes    │
              └───────────┘  └───────────┘  └───────────┘
```

## Core Concepts

### 1. Dual VFS Approach

Each pipeline maintains two Virtual File Systems:
- **Pre-change VFS**: Frozen snapshot at task start (actual content, not references)
- **Change VFS**: Working copy where modifications happen

Benefits:
- **Temporal isolation**: Immune to external changes during work
- **Instant operations**: Memory-to-memory diffs, reverts, forks
- **Consistent comparison**: Stable baseline for showing changes
- **Simple semantics**: No conditional logic for git vs uncommitted states

### 2. Central Version Store (CVS)

The CVS is the single source of truth for all file versions across all sessions.

```go
type CentralVersionStore struct {
    Versions    map[FilePath][]FileVersion    // Version DAG per file
    Operations  []Operation                    // Append-only operation log
    BlobStore   ContentAddressableStore        // Deduplicated content
    WAL         WriteAheadLog                  // Crash recovery
    Clock       LogicalClock                   // Global ordering

    mu          sync.RWMutex                   // For version graph updates
    fileLocks   map[FilePath]*sync.Mutex       // Per-file commit locks
}
```

### 3. Version DAG

Each file has a directed acyclic graph of versions:

```go
type VersionID [32]byte // SHA-256 of content + metadata

type FileVersion struct {
    ID          VersionID
    Parents     []VersionID           // DAG - can have multiple (merge commits)
    Operations  []OperationID         // Operations that produced this version
    ContentHash [32]byte              // Reference to blob store
    AST         *SyntaxTree           // Parsed structure (cached, lazy)
    Clock       VectorClock           // Causal ordering
    Timestamp   time.Time
    Pipeline    PipelineID
    Session     SessionID
}
```

### 4. Operations (Not Snapshots)

Changes are recorded as operations, not full file rewrites:

```go
type OperationID [32]byte

type Operation struct {
    ID          OperationID
    BaseVersion VersionID             // "I was looking at this version"
    Target      Target                // WHAT I'm modifying (AST node or range)
    Type        OpType                // Insert | Delete | Replace | Move
    Content     []byte                // The new content
    Pipeline    PipelineID
    Session     SessionID
    Clock       VectorClock
    Timestamp   time.Time
}

type OpType int

const (
    OpInsert OpType = iota
    OpDelete
    OpReplace
    OpMove
)
```

### 5. AST-Aware Targeting

Operations target AST nodes, not line numbers. This provides stability across refactors.

**Parser Implementation: Tree-Sitter via purego (no cgo)**

AST parsing is powered by tree-sitter, loaded dynamically via purego for a cgo-free build. See `ARCHITECTURE.md#Tree-Sitter Parsing Infrastructure` for full details. Key integration points:

```
CVS (Central Version Store)
    │
    ├── FileVersion
    │   ├── Content
    │   ├── AST (cached tree-sitter parse)  ◄── TreeSitterManager
    │   └── GrammarVersion (ABI compatibility)
    │
    └── On Edit:
        1. Get cached tree from CVS
        2. Apply TSInputEdit (incremental)
        3. Re-parse ONLY changed region (O(edit_size))
        4. Cache new tree
```

```go
type Target struct {
    // Primary: AST-based (stable across refactors)
    // Generated via TreeSitterManager.ComputeNodePath()
    NodePath    []string              // e.g., ["func", "HandleRequest", "body", "if[0]"]
    NodeType    string                // e.g., "function_declaration", "if_statement"
    NodeID      string                // Unique identifier within file

    // Fallback: character offsets (for non-parseable files)
    StartOffset int
    EndOffset   int

    // Cached: line numbers (for display, not identity)
    StartLine   int
    EndLine     int
}

// TreeSitterManager integration for Target resolution
type TreeSitterManager interface {
    // ComputeNodePath generates stable AST path for a position
    ComputeNodePath(tree *Tree, offset uint32) []string

    // ResolveNodePath finds node from path (reverse operation)
    ResolveNodePath(tree *Tree, path []string) (Node, error)

    // ParseIncremental re-parses after edit using cached tree
    ParseIncremental(oldVersion *FileVersion, newContent []byte, edit *InputEdit) (*Tree, error)
}
```

**Why AST targeting is critical:**

```
Line-based (BREAKS):
  Pipeline A: "Modify lines 50-60"
  Pipeline B: "Insert 20 lines at line 10"
  → Pipeline A's target is now wrong (should be 70-80)

AST-based (STABLE):
  Pipeline A: "Modify func:HandleRequest.body.if[0]"
  Pipeline B: "Insert func:NewHelper before func:HandleRequest"
  → Pipeline A's target unchanged - still func:HandleRequest.body.if[0]
```

## Operational Transformation Engine

### Transform Algorithm

When two operations conflict (overlapping targets on same base version), OT transforms one against the other:

```go
func Transform(op Operation, against []Operation) (Operation, error) {
    result := op

    for _, concurrent := range against {
        if !Overlaps(result.Target, concurrent.Target) {
            // No conflict - but may need to adjust offsets
            result.Target = AdjustTarget(result.Target, concurrent)
            continue
        }

        // Overlapping targets - need semantic transform
        switch {
        case BothInsertAtSamePoint(result, concurrent):
            // Tie-breaker: lower PipelineID goes first
            result = InsertAfter(result, concurrent)

        case OneDeletesOthersContext(result, concurrent):
            // Rebase onto new structure
            result = RebaseOnto(result, concurrent)

        case BothModifySameNode(result, concurrent):
            // True conflict - attempt line-level merge within node
            merged, ok := MergeNodeContent(result, concurrent)
            if !ok {
                return Operation{}, ConflictError{result, concurrent}
            }
            result = merged
        }
    }

    return result, nil
}
```

### Conflict Detection

```go
func Overlaps(a, b Target) bool {
    // AST-based: check node path overlap
    if a.NodePath != nil && b.NodePath != nil {
        return NodePathOverlaps(a.NodePath, b.NodePath)
    }

    // Offset-based: check range overlap
    return a.StartOffset < b.EndOffset && b.StartOffset < a.EndOffset
}

func NodePathOverlaps(a, b []string) bool {
    // Paths overlap if one is a prefix of the other
    // or if they target the same node
    minLen := min(len(a), len(b))
    for i := 0; i < minLen; i++ {
        if a[i] != b[i] {
            return false
        }
    }
    return true // One is prefix of other, or they're equal
}
```

### Conflict Resolution Strategies

```go
type ConflictStrategy int

const (
    // Surface to user - they decide
    StrategyManual ConflictStrategy = iota

    // Last writer wins (by vector clock)
    StrategyLastWriterWins

    // Merge at line level within conflicting AST node
    StrategyLineMerge

    // Both changes kept, marked with conflict markers
    StrategyKeepBoth

    // Per-session priority (Session A always wins over B)
    StrategySessionPriority
)
```

## Pipeline Lifecycle

### 1. Start

```go
func (p *Pipeline) Start(files []string) error {
    p.BaseVersions = make(map[string]VersionID)
    p.PreChangeVFS = NewVFS()
    p.ChangeVFS = NewVFS()

    for _, file := range files {
        version := p.CVS.CurrentVersion(file)
        p.BaseVersions[file] = version.ID

        content := p.CVS.GetContent(version.ContentHash)
        p.PreChangeVFS.Write(file, content)  // Freeze original
        p.ChangeVFS.Write(file, content)     // Working copy
    }

    return nil
}
```

### 2. During Work

```go
func (p *Pipeline) RecordChange(file string, newContent []byte) error {
    oldContent := p.ChangeVFS.Read(file)
    ast := p.ParseAST(oldContent)

    // Compute semantic diff - what AST nodes changed?
    ops := ComputeOperations(ast, oldContent, newContent)

    for _, op := range ops {
        op.BaseVersion = p.BaseVersions[file]
        op.Pipeline = p.ID
        op.Session = p.Session
        op.Clock = p.IncrementClock()

        p.PendingOps[file] = append(p.PendingOps[file], op)
    }

    p.ChangeVFS.Write(file, newContent) // Update local VFS
    return nil
}
```

### 3. Commit

```go
func (p *Pipeline) Commit() error {
    for file, ops := range p.PendingOps {
        baseVersion := p.BaseVersions[file]
        currentVersion := p.CVS.CurrentVersion(file)

        if currentVersion.ID == baseVersion {
            // Fast path: no concurrent modifications
            if err := p.CVS.Apply(file, ops); err != nil {
                return err
            }
            continue
        }

        // Concurrent modifications exist - need OT
        concurrentOps := p.CVS.OperationsSince(file, baseVersion)

        transformedOps := make([]Operation, len(ops))
        for i, op := range ops {
            transformed, err := Transform(op, concurrentOps)
            if err != nil {
                // Conflict - surface to user or apply strategy
                return p.HandleConflict(file, op, concurrentOps, err)
            }
            transformedOps[i] = transformed
        }

        if err := p.CVS.ApplyMerge(file, currentVersion.ID, transformedOps); err != nil {
            return err
        }
    }

    // Write to actual filesystem
    for file := range p.PendingOps {
        content := p.CVS.GetCurrentContent(file)
        if err := os.WriteFile(file, content, 0644); err != nil {
            return err
        }
    }

    p.PendingOps = nil
    p.CommittedOps = p.PendingOps // Track for potential revert
    return nil
}
```

### 4. Revert

```go
func (p *Pipeline) Revert() error {
    // Option 1: If not yet committed, just restore from PreChangeVFS
    if len(p.CommittedOps) == 0 {
        for file := range p.PendingOps {
            content := p.PreChangeVFS.Read(file)
            if err := os.WriteFile(file, content, 0644); err != nil {
                return err
            }
        }
        p.PendingOps = nil
        return nil
    }

    // Option 2: If committed, generate inverse operations
    for file, ops := range p.CommittedOps {
        inverseOps := make([]Operation, len(ops))

        for i, op := range ops {
            inverseOps[len(ops)-1-i] = Invert(op) // Reverse order
        }

        p.PendingOps[file] = inverseOps
    }

    return p.Commit() // Inverse ops go through normal OT
}
```

### 5. Fork Variant

```go
func (p *Pipeline) ForkVariant() *Pipeline {
    variant := &Pipeline{
        ID:           NewPipelineID(),
        Session:      p.Session,
        CVS:          p.CVS,                           // Shared
        BaseVersions: maps.Clone(p.BaseVersions),      // Same starting point
        PreChangeVFS: p.PreChangeVFS.Clone(),          // Copy frozen state
        ChangeVFS:    p.PreChangeVFS.Clone(),          // Start fresh from original
        PendingOps:   make(map[string][]Operation),
    }

    return variant
}
```

## Cross-Session Coordination

```go
type CrossSessionCoordinator struct {
    CVS           *CentralVersionStore
    Sessions      map[SessionID]*Session
    Subscriptions map[FilePath][]SessionID

    mu            sync.RWMutex
}

func (c *CrossSessionCoordinator) Subscribe(session SessionID, files []string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    for _, file := range files {
        c.Subscriptions[file] = append(c.Subscriptions[file], session)
    }
}

func (c *CrossSessionCoordinator) NotifyChange(file string, version FileVersion) {
    c.mu.RLock()
    subscribers := c.Subscriptions[file]
    c.mu.RUnlock()

    for _, sessionID := range subscribers {
        session := c.Sessions[sessionID]
        session.Notify(FileChangedEvent{
            File:       file,
            NewVersion: version.ID,
            By:         version.Session,
            Operations: version.Operations,
        })
    }
}
```

## Storage Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Persistent Storage                           │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                    Write-Ahead Log                          ││
│  │  (all operations written here first for crash recovery)     ││
│  └─────────────────────────────────────────────────────────────┘│
│         │                                                       │
│         ▼                                                       │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐ │
│  │   Version   │  │  Operation  │  │   Content-Addressable   │ │
│  │    Graph    │  │     Log     │  │        Blob Store       │ │
│  │   (DAG)     │  │  (append)   │  │                         │ │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                           ▲
                           │
┌──────────────────────────┼──────────────────────────────────────┐
│                    In-Memory Cache                              │
│                                                                 │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐ │
│  │   Hot       │  │   Parsed    │  │   Materialized          │ │
│  │  Versions   │  │    ASTs     │  │     Content             │ │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

## VFS Implementation

```go
type VFS struct {
    files   map[string][]byte
    mu      sync.RWMutex
}

func NewVFS() *VFS {
    return &VFS{
        files: make(map[string][]byte),
    }
}

func (v *VFS) Read(path string) []byte {
    v.mu.RLock()
    defer v.mu.RUnlock()

    content, exists := v.files[path]
    if !exists {
        return nil
    }

    // Return copy to prevent mutation
    result := make([]byte, len(content))
    copy(result, content)
    return result
}

func (v *VFS) Write(path string, content []byte) {
    v.mu.Lock()
    defer v.mu.Unlock()

    // Store copy to prevent external mutation
    stored := make([]byte, len(content))
    copy(stored, content)
    v.files[path] = stored
}

func (v *VFS) Clone() *VFS {
    v.mu.RLock()
    defer v.mu.RUnlock()

    clone := NewVFS()
    for path, content := range v.files {
        clonedContent := make([]byte, len(content))
        copy(clonedContent, content)
        clone.files[path] = clonedContent
    }
    return clone
}

func (v *VFS) Diff(other *VFS) []FileDiff {
    v.mu.RLock()
    other.mu.RLock()
    defer v.mu.RUnlock()
    defer other.mu.RUnlock()

    var diffs []FileDiff

    // Files in v but not in other, or different
    for path, content := range v.files {
        otherContent, exists := other.files[path]
        if !exists {
            diffs = append(diffs, FileDiff{Path: path, Type: DiffDeleted})
        } else if !bytes.Equal(content, otherContent) {
            diffs = append(diffs, FileDiff{Path: path, Type: DiffModified})
        }
    }

    // Files in other but not in v
    for path := range other.files {
        if _, exists := v.files[path]; !exists {
            diffs = append(diffs, FileDiff{Path: path, Type: DiffAdded})
        }
    }

    return diffs
}
```

## Vector Clock Implementation

```go
type VectorClock map[SessionID]uint64

func (vc VectorClock) Increment(session SessionID) VectorClock {
    result := vc.Clone()
    result[session]++
    return result
}

func (vc VectorClock) Merge(other VectorClock) VectorClock {
    result := vc.Clone()
    for session, count := range other {
        if count > result[session] {
            result[session] = count
        }
    }
    return result
}

func (vc VectorClock) HappensBefore(other VectorClock) bool {
    // vc happens before other if all counts in vc <= other
    // and at least one is strictly less
    allLessOrEqual := true
    someStrictlyLess := false

    for session, count := range vc {
        otherCount := other[session]
        if count > otherCount {
            allLessOrEqual = false
            break
        }
        if count < otherCount {
            someStrictlyLess = true
        }
    }

    // Check sessions in other but not in vc
    for session, count := range other {
        if _, exists := vc[session]; !exists && count > 0 {
            someStrictlyLess = true
        }
    }

    return allLessOrEqual && someStrictlyLess
}

func (vc VectorClock) Concurrent(other VectorClock) bool {
    return !vc.HappensBefore(other) && !other.HappensBefore(vc)
}

func (vc VectorClock) Clone() VectorClock {
    result := make(VectorClock)
    for k, v := range vc {
        result[k] = v
    }
    return result
}
```

## Benefits Summary

| Dimension | How Achieved |
|-----------|--------------|
| **Robustness** | Immutable operations, WAL, version DAG, inverse operations for rollback |
| **Correctness** | OT guarantees convergence, precise conflict detection via AST targeting |
| **Performance** | Small operations, content deduplication, parallel reads (MVCC), minimal locking |
| **Parallelism** | Complete isolation until commit, auto-merge for non-conflicts, per-file lock granularity |
| **Multi-Session** | Shared CVS, real-time notifications, unified conflict resolution |

## Edge Cases Handled

| Scenario | Resolution |
|----------|------------|
| Same line modified by two pipelines | OT transforms one operation, or surfaces conflict |
| Pipeline A adds function, Pipeline B modifies shifted lines | AST targeting: B's target stable; Line targeting: conflict detected |
| Session disconnects mid-pipeline | Pending ops persisted, resume or rollback on reconnect |
| Process crash | WAL recovery, pending ops can be recovered or abandoned |
| External process modifies file | Detected at commit time via version mismatch, OT applied |
