# FILESYSTEM Implementation Tasks

This document contains discrete, actionable, atomic tasks for implementing the MVCC + Operational Transformation + AST-aware VFS system described in `FILESYSTEM.md`, fully integrated with Sylk's architecture.

Each task includes:
- **Objective**: What needs to be built
- **Acceptance Criteria**: Specific, testable requirements
- **Interface/Signature**: Exact Go types and methods to implement
- **Examples**: Concrete usage examples
- **Integration Points**: How this connects to existing architecture
- **References**: Related files and dependencies
- **Tests**: Required test cases

---

## Phase 1: Foundation Data Structures

### Task 1.1: Implement VersionID and Content-Addressable Hashing

**Objective**: Create the core versioning primitives for content-addressable storage.

**File**: `core/versioning/version_id.go`

**Interface/Signature**:
```go
package versioning

import (
    "crypto/sha256"
    "encoding/hex"
)

// VersionID is a content-addressable identifier (SHA-256 hash)
type VersionID [32]byte

// NilVersion represents no version (zero value)
var NilVersion VersionID

// NewVersionID creates a VersionID from content bytes
func NewVersionID(content []byte) VersionID

// NewVersionIDFromMetadata creates a VersionID from content + metadata
// This ensures versions with same content but different metadata are distinct
func NewVersionIDFromMetadata(content []byte, parentIDs []VersionID, timestamp int64) VersionID

// String returns hex-encoded representation
func (v VersionID) String() string

// Short returns first 8 characters of hex string (for display)
func (v VersionID) Short() string

// IsNil returns true if this is the zero version
func (v VersionID) IsNil() bool

// MarshalJSON implements json.Marshaler
func (v VersionID) MarshalJSON() ([]byte, error)

// UnmarshalJSON implements json.Unmarshaler
func (v *VersionID) UnmarshalJSON(data []byte) error

// ParseVersionID parses a hex string into VersionID
func ParseVersionID(s string) (VersionID, error)
```

**Acceptance Criteria**:
1. `NewVersionID(content)` returns deterministic hash for same content
2. `NewVersionIDFromMetadata` incorporates parents and timestamp into hash
3. `String()` returns 64-character lowercase hex string
4. `Short()` returns first 8 characters for display purposes
5. `ParseVersionID(v.String())` round-trips correctly
6. JSON marshaling/unmarshaling works correctly
7. `NilVersion.IsNil()` returns true
8. Non-nil version's `IsNil()` returns false

**Examples**:
```go
content := []byte("func main() {}")
v1 := NewVersionID(content)
v2 := NewVersionID(content)
assert(v1 == v2) // Same content = same ID

v3 := NewVersionIDFromMetadata(content, []VersionID{v1}, time.Now().UnixNano())
assert(v3 != v1) // Different metadata = different ID

assert(v1.String() == "a1b2c3...") // 64 hex chars
assert(v1.Short() == "a1b2c3d4")   // 8 hex chars
parsed, _ := ParseVersionID(v1.String())
assert(parsed == v1)
```

**Integration Points**:
- Used by `core/versioning/file_version.go` for version identification
- Used by `core/versioning/cvs.go` for DAG node identity
- Stored in Archivalist entries for file change tracking

**Tests Required**:
- `TestVersionID_Deterministic`
- `TestVersionID_MetadataChangesHash`
- `TestVersionID_StringRoundTrip`
- `TestVersionID_ShortString`
- `TestVersionID_JSONRoundTrip`
- `TestVersionID_NilCheck`

**References**:
- Similar pattern: git object IDs
- crypto/sha256 stdlib

---

### Task 1.2: Implement Vector Clock for Causality Tracking

**Objective**: Create vector clock implementation for tracking causal ordering of operations across sessions.

**File**: `core/versioning/vector_clock.go`

**Interface/Signature**:
```go
package versioning

import "core/session"

// VectorClock tracks causal ordering across sessions
// Maps session.SessionID -> logical timestamp
type VectorClock map[session.SessionID]uint64

// NewVectorClock creates an empty vector clock
func NewVectorClock() VectorClock

// Increment returns a new clock with the given session's count incremented
func (vc VectorClock) Increment(sessionID session.SessionID) VectorClock

// Merge returns a new clock that is the pointwise maximum of both clocks
func (vc VectorClock) Merge(other VectorClock) VectorClock

// HappensBefore returns true if vc causally precedes other
// (all counts in vc <= other, and at least one strictly less)
func (vc VectorClock) HappensBefore(other VectorClock) bool

// HappensAfter returns true if vc causally follows other
func (vc VectorClock) HappensAfter(other VectorClock) bool

// Concurrent returns true if neither clock happens before the other
func (vc VectorClock) Concurrent(other VectorClock) bool

// Clone returns a deep copy of the clock
func (vc VectorClock) Clone() VectorClock

// Compare returns -1 (before), 0 (concurrent), or 1 (after)
func (vc VectorClock) Compare(other VectorClock) int

// Sessions returns all session IDs in the clock
func (vc VectorClock) Sessions() []session.SessionID

// Get returns the timestamp for a session (0 if not present)
func (vc VectorClock) Get(sessionID session.SessionID) uint64

// MarshalJSON implements json.Marshaler
func (vc VectorClock) MarshalJSON() ([]byte, error)

// UnmarshalJSON implements json.Unmarshaler
func (vc *VectorClock) UnmarshalJSON(data []byte) error
```

**Acceptance Criteria**:
1. `Increment` returns new clock without mutating original
2. `Merge` returns pointwise maximum
3. `HappensBefore` correctly identifies causal ordering
4. `Concurrent` returns true only when neither precedes the other
5. `Clone` creates independent copy (mutations don't affect original)
6. Empty clocks are concurrent with each other
7. JSON serialization preserves all session counts
8. Uses `session.SessionID` type from existing architecture

**Examples**:
```go
vc1 := NewVectorClock().Increment(session.SessionID("sess-A")) // {sess-A: 1}
vc2 := vc1.Increment(session.SessionID("sess-A"))              // {sess-A: 2}
vc3 := vc1.Increment(session.SessionID("sess-B"))              // {sess-A: 1, sess-B: 1}

assert(vc1.HappensBefore(vc2)) // true: {A:1} < {A:2}
assert(vc2.Concurrent(vc3))    // true: {A:2} || {A:1,B:1}

merged := vc2.Merge(vc3) // {A: 2, B: 1}
assert(vc2.HappensBefore(merged))
assert(vc3.HappensBefore(merged))
```

**Integration Points**:
- Uses `session.SessionID` from `core/session/session.go`
- Used by Operations to track causality
- Used by Cross-Session Pool for ordering decisions

**Tests Required**:
- `TestVectorClock_Increment`
- `TestVectorClock_Merge`
- `TestVectorClock_HappensBefore`
- `TestVectorClock_Concurrent`
- `TestVectorClock_Clone`
- `TestVectorClock_Compare`
- `TestVectorClock_JSONRoundTrip`
- `TestVectorClock_EmptyClocks`

**References**:
- Lamport, "Time, Clocks, and the Ordering of Events"
- Fidge/Mattern vector clocks
- `core/session/session.go` for SessionID type

---

### Task 1.3: Implement OperationID and Operation Types

**Objective**: Define the operation data structures for tracking file changes.

**File**: `core/versioning/operation.go`

**Interface/Signature**:
```go
package versioning

import (
    "time"
    "core/session"
    "core/pipeline"
)

// OperationID uniquely identifies an operation (content-addressable)
type OperationID [32]byte

// NewOperationID creates an ID from operation content
func NewOperationID(op *Operation) OperationID

// OpType defines the type of operation
type OpType int

const (
    OpInsert  OpType = iota // Insert new content
    OpDelete                // Delete content
    OpReplace               // Replace content (delete + insert)
    OpMove                  // Move content from one location to another
)

// String returns human-readable operation type
func (t OpType) String() string

// Operation represents a single atomic change to a file
type Operation struct {
    ID          OperationID          `json:"id"`
    BaseVersion VersionID            `json:"base_version"`    // Version this op was based on
    FilePath    string               `json:"file_path"`       // Target file
    Target      Target               `json:"target"`          // Where in the file
    Type        OpType               `json:"type"`            // Insert/Delete/Replace/Move
    Content     []byte               `json:"content"`         // New content (for Insert/Replace)
    OldContent  []byte               `json:"old_content"`     // Previous content (for Replace/Delete)
    PipelineID  pipeline.PipelineID  `json:"pipeline_id"`
    SessionID   session.SessionID    `json:"session_id"`
    AgentID     string               `json:"agent_id"`        // Which agent performed this
    AgentRole   security.AgentRole   `json:"agent_role"`      // Role of the agent
    Clock       VectorClock          `json:"clock"`
    Timestamp   time.Time            `json:"timestamp"`
}

// NewOperation creates a new operation with generated ID
func NewOperation(
    baseVersion VersionID,
    filePath string,
    target Target,
    opType OpType,
    content, oldContent []byte,
    pipelineID pipeline.PipelineID,
    sessionID session.SessionID,
    agentID string,
    agentRole security.AgentRole,
    clock VectorClock,
) *Operation

// Invert returns the inverse operation (for rollback)
func (op *Operation) Invert() *Operation

// Clone returns a deep copy of the operation
func (op *Operation) Clone() *Operation

// Validate checks operation integrity
func (op *Operation) Validate() error

// RequiresRole returns minimum role required to perform this operation
func (op *Operation) RequiresRole() security.AgentRole

// Sanitize removes sensitive content from the operation for logging
func (op *Operation) Sanitize(sanitizer *security.SecretSanitizer) *Operation
```

**Acceptance Criteria**:
1. `NewOperationID` produces deterministic ID from operation content
2. `Invert()` correctly reverses each operation type:
   - Insert → Delete (same target, content becomes oldContent)
   - Delete → Insert (same target, oldContent becomes content)
   - Replace → Replace (swap content and oldContent)
   - Move → Move (swap source and destination)
3. `Clone()` creates independent copy
4. `Validate()` returns error for invalid operations (e.g., Insert without content)
5. Operations are JSON serializable
6. Operations track agent role for permission auditing
7. `Sanitize()` redacts secrets before logging

**Examples**:
```go
op := NewOperation(
    baseVersion,
    "handler.go",
    Target{NodePath: []string{"func", "HandleRequest", "body"}},
    OpReplace,
    []byte("new body"),
    []byte("old body"),
    pipeline.PipelineID("pipeline-1"),
    session.SessionID("session-A"),
    "engineer-1",
    security.RoleWorker,
    clock,
)

inverse := op.Invert()
assert(inverse.Type == OpReplace)
assert(bytes.Equal(inverse.Content, op.OldContent))
assert(bytes.Equal(inverse.OldContent, op.Content))

// Role check
assert(op.RequiresRole() == security.RoleWorker)
```

**Integration Points**:
- Uses `pipeline.PipelineID` from `core/pipeline/pipeline.go`
- Uses `session.SessionID` from `core/session/session.go`
- Uses `security.AgentRole` from `core/security/security.go`
- Uses `security.SecretSanitizer` for safe logging

**Tests Required**:
- `TestOperation_NewOperation`
- `TestOperation_Invert_Insert`
- `TestOperation_Invert_Delete`
- `TestOperation_Invert_Replace`
- `TestOperation_Invert_Move`
- `TestOperation_Clone`
- `TestOperation_Validate`
- `TestOperation_RequiresRole`
- `TestOperation_Sanitize`
- `TestOperation_JSONRoundTrip`

**References**:
- `FILESYSTEM.md` Operation definition
- `core/security/security.go` AgentRole types
- `core/pipeline/pipeline.go` PipelineID type

---

### Task 1.4: Implement AST-Aware Target

**Objective**: Create the targeting system that identifies locations within files using AST nodes.

**File**: `core/versioning/target.go`

**Interface/Signature**:
```go
package versioning

// Target identifies a location within a file
// Primary identification is via AST node path (stable across refactors)
// Falls back to character offsets for non-parseable files
type Target struct {
    // AST-based targeting (primary, stable)
    NodePath []string `json:"node_path,omitempty"` // e.g., ["func", "HandleRequest", "body", "if[0]"]
    NodeType string   `json:"node_type,omitempty"` // e.g., "FunctionDecl", "IfStmt"
    NodeID   string   `json:"node_id,omitempty"`   // Unique ID within file (hash of path + type)

    // Offset-based targeting (fallback for non-parseable files)
    StartOffset int `json:"start_offset"`
    EndOffset   int `json:"end_offset"`

    // Line-based (for display, not identity)
    StartLine int `json:"start_line"`
    EndLine   int `json:"end_line"`

    // Language hint for parser selection
    Language string `json:"language,omitempty"` // "go", "typescript", "python", etc.
}

// NewASTTarget creates a target from AST node information
func NewASTTarget(nodePath []string, nodeType string, startLine, endLine int) Target

// NewOffsetTarget creates a target from character offsets
func NewOffsetTarget(startOffset, endOffset, startLine, endLine int) Target

// IsASTBased returns true if this target uses AST identification
func (t Target) IsASTBased() bool

// Overlaps returns true if this target overlaps with another
func (t Target) Overlaps(other Target) bool

// Contains returns true if this target fully contains another
func (t Target) Contains(other Target) bool

// IsAncestorOf returns true if this target is an AST ancestor of another
func (t Target) IsAncestorOf(other Target) bool

// IsDescendantOf returns true if this target is an AST descendant of another
func (t Target) IsDescendantOf(other Target) bool

// Shift returns a new target with offsets adjusted by delta
// Used when operations before this target change line counts
func (t Target) Shift(lineDelta, offsetDelta int) Target

// String returns human-readable target description
func (t Target) String() string

// NodePathString returns the node path as a dotted string
// e.g., "func.HandleRequest.body.if[0]"
func (t Target) NodePathString() string

// Validate checks target integrity
func (t Target) Validate() error
```

**Acceptance Criteria**:
1. AST targets with same NodePath are equal regardless of offsets
2. `Overlaps()` correctly identifies:
   - Same AST node → overlaps
   - Parent/child AST nodes → overlaps
   - Sibling AST nodes → no overlap
   - Overlapping offset ranges (for non-AST) → overlaps
3. `Shift()` correctly adjusts offsets without mutating original
4. `Contains()` returns true only for strict containment
5. `IsAncestorOf`/`IsDescendantOf` correctly traverses AST relationships
6. Targets are JSON serializable
7. Language field enables correct parser selection

**Examples**:
```go
t1 := NewASTTarget(
    []string{"func", "HandleRequest", "body"},
    "BlockStmt",
    10, 50,
)

t2 := NewASTTarget(
    []string{"func", "HandleRequest", "body", "if[0]"},
    "IfStmt",
    15, 30,
)

t3 := NewASTTarget(
    []string{"func", "OtherFunc", "body"},
    "BlockStmt",
    60, 80,
)

assert(t1.Overlaps(t2))        // true: t1 is parent of t2
assert(t1.Contains(t2))        // true: t1 fully contains t2
assert(t1.IsAncestorOf(t2))    // true
assert(t2.IsDescendantOf(t1))  // true
assert(!t1.Overlaps(t3))       // false: different functions

assert(t1.NodePathString() == "func.HandleRequest.body")
```

**Integration Points**:
- Used by OT engine for conflict detection
- Used by AST parsers in Phase 8
- Provides stable references for Inspector/Tester feedback

**Tests Required**:
- `TestTarget_NewASTTarget`
- `TestTarget_NewOffsetTarget`
- `TestTarget_Overlaps_SameNode`
- `TestTarget_Overlaps_ParentChild`
- `TestTarget_Overlaps_Siblings`
- `TestTarget_Overlaps_OffsetBased`
- `TestTarget_Contains`
- `TestTarget_IsAncestorOf`
- `TestTarget_IsDescendantOf`
- `TestTarget_Shift`
- `TestTarget_Validate`
- `TestTarget_JSONRoundTrip`

**References**:
- `FILESYSTEM.md` AST targeting section
- Go AST package patterns

---

### Task 1.5: Implement FileVersion

**Objective**: Create the file version structure that forms the version DAG.

**File**: `core/versioning/file_version.go`

**Interface/Signature**:
```go
package versioning

import (
    "time"
    "core/session"
    "core/pipeline"
)

// FileVersion represents a single version of a file in the version DAG
type FileVersion struct {
    ID          VersionID            `json:"id"`
    FilePath    string               `json:"file_path"`
    Parents     []VersionID          `json:"parents"`       // DAG: can have multiple (merge commits)
    Operations  []OperationID        `json:"operations"`    // Operations that produced this version
    ContentHash [32]byte             `json:"content_hash"`  // Reference to blob store
    ContentSize int64                `json:"content_size"`  // Size in bytes
    Clock       VectorClock          `json:"clock"`
    Timestamp   time.Time            `json:"timestamp"`
    PipelineID  pipeline.PipelineID  `json:"pipeline_id"`
    SessionID   session.SessionID    `json:"session_id"`
    IsMerge     bool                 `json:"is_merge"`      // True if this is a merge commit

    // Variant tracking
    VariantGroupID *string `json:"variant_group_id,omitempty"` // Set if part of variant execution
    VariantLabel   string  `json:"variant_label,omitempty"`    // "original", "variant-1", etc.
}

// NewFileVersion creates a new file version
func NewFileVersion(
    filePath string,
    parents []VersionID,
    operations []OperationID,
    contentHash [32]byte,
    contentSize int64,
    clock VectorClock,
    pipelineID pipeline.PipelineID,
    sessionID session.SessionID,
) *FileVersion

// NewMergeVersion creates a merge version with multiple parents
func NewMergeVersion(
    filePath string,
    parents []VersionID,
    operations []OperationID,
    contentHash [32]byte,
    contentSize int64,
    clock VectorClock,
    sessionID session.SessionID,
) *FileVersion

// NewVariantVersion creates a version within a variant group
func NewVariantVersion(
    filePath string,
    parents []VersionID,
    operations []OperationID,
    contentHash [32]byte,
    contentSize int64,
    clock VectorClock,
    pipelineID pipeline.PipelineID,
    sessionID session.SessionID,
    variantGroupID string,
    variantLabel string,
) *FileVersion

// IsRoot returns true if this version has no parents
func (v *FileVersion) IsRoot() bool

// HasParent returns true if parentID is one of this version's parents
func (v *FileVersion) HasParent(parentID VersionID) bool

// IsVariant returns true if this version is part of a variant group
func (v *FileVersion) IsVariant() bool

// Clone returns a deep copy
func (v *FileVersion) Clone() *FileVersion

// Validate checks version integrity
func (v *FileVersion) Validate() error
```

**Acceptance Criteria**:
1. Version ID is computed from content + parents + timestamp (deterministic)
2. Root versions have empty Parents slice
3. Merge versions have `IsMerge = true` and multiple parents
4. Variant versions have `VariantGroupID` set
5. `HasParent()` correctly checks parent membership
6. Versions are JSON serializable
7. `Validate()` catches integrity issues (nil content hash, etc.)

**Examples**:
```go
// Initial version (root)
v1 := NewFileVersion(
    "handler.go",
    nil, // no parents - root
    nil,
    contentHash,
    1024,
    clock,
    pipeline.PipelineID("pipeline-1"),
    session.SessionID("session-A"),
)
assert(v1.IsRoot())

// Variant version
v2 := NewVariantVersion(
    "handler.go",
    []VersionID{v1.ID},
    []OperationID{op1.ID},
    newContentHash,
    1100,
    clock.Increment(session.SessionID("session-A")),
    pipeline.PipelineID("pipeline-1"),
    session.SessionID("session-A"),
    "variant-group-123",
    "variant-1",
)
assert(v2.IsVariant())
assert(v2.VariantLabel == "variant-1")
```

**Integration Points**:
- References `pipeline.PipelineID` from existing Pipeline architecture
- References `session.SessionID` from Session Management
- Integrates with VariantGroup from Pipeline Variants
- Stored in Version DAG Store (Task 2.3)

**Tests Required**:
- `TestFileVersion_NewFileVersion`
- `TestFileVersion_NewMergeVersion`
- `TestFileVersion_NewVariantVersion`
- `TestFileVersion_IsRoot`
- `TestFileVersion_HasParent`
- `TestFileVersion_IsVariant`
- `TestFileVersion_Clone`
- `TestFileVersion_Validate`
- `TestFileVersion_JSONRoundTrip`

**References**:
- `FILESYSTEM.md` FileVersion definition
- `core/pipeline/pipeline.go` Pipeline Variants section
- Git commit object model

---

## Phase 2: Storage Layer

### Task 2.1: Implement Content-Addressable Blob Store

**Objective**: Create storage for file contents indexed by content hash, integrating with existing FilesystemManager patterns.

**File**: `core/versioning/blob_store.go`

**Interface/Signature**:
```go
package versioning

import (
    "io"
    "sync"
)

// ContentHash is a SHA-256 hash of file contents
type ContentHash [32]byte

// ComputeContentHash computes hash from content bytes
func ComputeContentHash(content []byte) ContentHash

// BlobStore stores file contents indexed by content hash
// Automatically deduplicates identical content
type BlobStore interface {
    // Put stores content and returns its hash
    // If content already exists, returns existing hash (no-op)
    Put(content []byte) (ContentHash, error)

    // PutStream stores content from a reader (for large files)
    PutStream(r io.Reader) (ContentHash, error)

    // Get retrieves content by hash
    // Returns ErrBlobNotFound if hash doesn't exist
    Get(hash ContentHash) ([]byte, error)

    // GetReader returns a reader for streaming content
    GetReader(hash ContentHash) (io.ReadCloser, error)

    // Has checks if a blob exists
    Has(hash ContentHash) bool

    // Delete removes a blob (use carefully - check references first)
    Delete(hash ContentHash) error

    // Size returns the size of a blob without loading it
    Size(hash ContentHash) (int64, error)

    // Stats returns storage statistics
    Stats() BlobStoreStats

    // GC removes unreferenced blobs (requires reference checker)
    GC(refChecker func(ContentHash) bool) (int64, error)
}

// BlobStoreStats contains storage statistics
type BlobStoreStats struct {
    TotalBlobs     int64
    TotalBytes     int64
    UniqueBytes    int64 // After deduplication
    DedupeRatio    float64
}

// MemoryBlobStore is an in-memory implementation
type MemoryBlobStore struct {
    blobs map[ContentHash][]byte
    mu    sync.RWMutex
}

func NewMemoryBlobStore() *MemoryBlobStore

// FileBlobStore stores blobs on disk in a content-addressable structure
// Path structure: <base>/<first 2 chars>/<next 2 chars>/<full hash>
// Follows existing FilesystemManager patterns for path handling
type FileBlobStore struct {
    baseDir    string
    config     FilesystemConfig
    mu         sync.RWMutex
    auditLog   *AuditLog
}

func NewFileBlobStore(baseDir string, config FilesystemConfig) (*FileBlobStore, error)

// ErrBlobNotFound is returned when a blob doesn't exist
var ErrBlobNotFound = errors.New("blob not found")
```

**Acceptance Criteria**:
1. Same content always produces same hash (deterministic)
2. Duplicate content is not stored twice (deduplication)
3. `Get(Put(content))` returns original content
4. `Has()` returns true only for existing blobs
5. `Delete()` removes blob and subsequent `Get()` fails
6. `FileBlobStore` survives process restart
7. Thread-safe for concurrent access
8. Follows existing `FilesystemConfig` patterns from ARCHITECTURE.md
9. Integrates with audit logging

**Examples**:
```go
config := FilesystemConfig{
    ProjectRoot: "/path/to/project",
    StagingRoot: "~/.sylk/staging",
    TempRoot:    "~/.sylk/tmp",
}
store, _ := NewFileBlobStore("~/.sylk/blobs", config)

content1 := []byte("func main() {}")
content2 := []byte("func main() {}")  // Same as content1
content3 := []byte("func other() {}")

hash1, _ := store.Put(content1)
hash2, _ := store.Put(content2)
hash3, _ := store.Put(content3)

assert(hash1 == hash2)  // Same content = same hash
assert(hash1 != hash3)  // Different content = different hash

retrieved, _ := store.Get(hash1)
assert(bytes.Equal(retrieved, content1))

stats := store.Stats()
assert(stats.TotalBlobs == 2)  // Only 2 unique blobs stored
```

**Integration Points**:
- Uses `FilesystemConfig` from `core/filesystem/config.go`
- Integrates with `AuditLog` for tracking blob operations
- Respects path boundaries from FilesystemManager

**Tests Required**:
- `TestBlobStore_Put_Deterministic`
- `TestBlobStore_Put_Deduplication`
- `TestBlobStore_PutStream`
- `TestBlobStore_Get`
- `TestBlobStore_GetReader`
- `TestBlobStore_Get_NotFound`
- `TestBlobStore_Has`
- `TestBlobStore_Delete`
- `TestBlobStore_Stats`
- `TestBlobStore_GC`
- `TestBlobStore_Concurrent`
- `TestFileBlobStore_Persistence`
- `TestFileBlobStore_BoundaryCheck`

**References**:
- Git object storage model
- `ARCHITECTURE.md` FilesystemManager section
- Content-addressable storage patterns

---

### Task 2.2: Implement Operation Log

**Objective**: Create append-only log for storing operations with WAL integration.

**File**: `core/versioning/operation_log.go`

**Interface/Signature**:
```go
package versioning

import (
    "sync"
    "core/session"
)

// OperationLog is an append-only log of operations
type OperationLog interface {
    // Append adds an operation to the log
    // Returns the operation's position in the log
    Append(op *Operation) (uint64, error)

    // Get retrieves an operation by ID
    Get(id OperationID) (*Operation, error)

    // GetByPosition retrieves an operation by log position
    GetByPosition(pos uint64) (*Operation, error)

    // Range iterates operations in a position range
    // Calls fn for each operation; stops if fn returns false
    Range(startPos, endPos uint64, fn func(*Operation) bool) error

    // OperationsSince returns all operations for a file since a given version
    OperationsSince(filePath string, sinceVersion VersionID) ([]*Operation, error)

    // OperationsForSession returns operations from a specific session
    OperationsForSession(sessionID session.SessionID) ([]*Operation, error)

    // OperationsForPipeline returns operations from a specific pipeline
    OperationsForPipeline(pipelineID pipeline.PipelineID) ([]*Operation, error)

    // Len returns total operations in log
    Len() uint64

    // LastPosition returns the position of the last operation
    LastPosition() uint64

    // Sync forces durability (for WAL integration)
    Sync() error

    // Close closes the log
    Close() error
}

// MemoryOperationLog is an in-memory implementation
type MemoryOperationLog struct {
    operations []*Operation
    byID       map[OperationID]int
    byFile     map[string][]int
    bySession  map[session.SessionID][]int
    byPipeline map[pipeline.PipelineID][]int
    mu         sync.RWMutex
}

func NewMemoryOperationLog() *MemoryOperationLog

// FileOperationLog stores operations on disk with WAL
type FileOperationLog struct {
    path     string
    wal      *WAL
    index    *OperationIndex
    mu       sync.RWMutex
}

func NewFileOperationLog(path string) (*FileOperationLog, error)

// OperationIndex provides fast lookups
type OperationIndex struct {
    byID       map[OperationID]uint64     // ID -> log position
    byFile     map[string][]uint64        // file path -> positions
    bySession  map[session.SessionID][]uint64
    byPipeline map[pipeline.PipelineID][]uint64
}
```

**Acceptance Criteria**:
1. Operations are stored durably with WAL
2. Operations can be queried by ID, position, file, session, or pipeline
3. `Range()` iterates in order
4. Thread-safe for concurrent append and read
5. `Sync()` ensures durability
6. File-based log survives process restart
7. Indexes enable efficient queries by session/pipeline

**Examples**:
```go
log := NewMemoryOperationLog()

op1 := NewOperation(/*...*/)
pos1, _ := log.Append(op1)

op2 := NewOperation(/*...*/)
pos2, _ := log.Append(op2)

assert(pos1 == 0)
assert(pos2 == 1)

retrieved, _ := log.Get(op1.ID)
assert(retrieved.ID == op1.ID)

// Query by session
ops, _ := log.OperationsForSession(session.SessionID("session-A"))
assert(len(ops) > 0)
```

**Integration Points**:
- Uses WAL from Task 2.4
- Indexes by `session.SessionID` and `pipeline.PipelineID`
- Queryable for Archivalist file change tracking

**Tests Required**:
- `TestOperationLog_Append`
- `TestOperationLog_Get`
- `TestOperationLog_GetByPosition`
- `TestOperationLog_Range`
- `TestOperationLog_OperationsSince`
- `TestOperationLog_OperationsForSession`
- `TestOperationLog_OperationsForPipeline`
- `TestOperationLog_Concurrent`
- `TestFileOperationLog_Persistence`
- `TestFileOperationLog_WALRecovery`

**References**:
- Append-only log patterns
- LSM tree indexing
- `core/session/session.go` for session tracking

---

### Task 2.3: Implement Version DAG Store

**Objective**: Create storage for the version DAG with efficient traversal operations.

**File**: `core/versioning/dag_store.go`

**Interface/Signature**:
```go
package versioning

import (
    "sync"
    "core/session"
    "core/pipeline"
)

// DAGStore manages the version DAG for all files
type DAGStore interface {
    // PutVersion adds a new version to the DAG
    PutVersion(version *FileVersion) error

    // GetVersion retrieves a version by ID
    GetVersion(id VersionID) (*FileVersion, error)

    // GetHead returns the current HEAD version for a file
    GetHead(filePath string) (*FileVersion, error)

    // SetHead updates the HEAD for a file
    SetHead(filePath string, versionID VersionID) error

    // GetHistory returns version history for a file (newest first)
    GetHistory(filePath string, limit int) ([]*FileVersion, error)

    // GetAncestors returns all ancestors of a version
    GetAncestors(versionID VersionID) ([]*FileVersion, error)

    // GetCommonAncestor finds the common ancestor of two versions
    GetCommonAncestor(v1, v2 VersionID) (*FileVersion, error)

    // GetChildren returns immediate children of a version
    GetChildren(versionID VersionID) ([]*FileVersion, error)

    // ListFiles returns all tracked files
    ListFiles() []string

    // GetSessionVersions returns versions created in a session
    GetSessionVersions(sessionID session.SessionID) ([]*FileVersion, error)

    // GetPipelineVersions returns versions created by a pipeline
    GetPipelineVersions(pipelineID pipeline.PipelineID) ([]*FileVersion, error)

    // GetVariantGroupVersions returns versions in a variant group
    GetVariantGroupVersions(variantGroupID string) ([]*FileVersion, error)

    // Stats returns DAG statistics
    Stats() DAGStoreStats
}

// DAGStoreStats contains DAG statistics
type DAGStoreStats struct {
    TotalVersions   int64
    TotalFiles      int64
    MaxDepth        int64
    MergeCommits    int64
    VariantVersions int64
}

// MemoryDAGStore is an in-memory implementation
type MemoryDAGStore struct {
    versions map[VersionID]*FileVersion
    heads    map[string]VersionID         // filePath -> head versionID
    children map[VersionID][]VersionID    // parent -> children
    byFile   map[string][]VersionID       // filePath -> all versions
    bySession  map[session.SessionID][]VersionID
    byPipeline map[pipeline.PipelineID][]VersionID
    byVariantGroup map[string][]VersionID
    mu       sync.RWMutex
}

func NewMemoryDAGStore() *MemoryDAGStore

// PersistentDAGStore stores DAG on disk
type PersistentDAGStore struct {
    path     string
    memory   *MemoryDAGStore  // hot cache
    wal      *WAL
    mu       sync.RWMutex
}

func NewPersistentDAGStore(path string) (*PersistentDAGStore, error)
```

**Acceptance Criteria**:
1. Versions can be stored and retrieved by ID
2. HEAD pointers are maintained per file
3. History traversal returns versions in chronological order
4. Common ancestor detection works for merge commits
5. Indexes by session, pipeline, and variant group enable efficient queries
6. Thread-safe for concurrent access
7. Persistent store survives restarts

**Examples**:
```go
store := NewMemoryDAGStore()

// Create version chain
v1 := NewFileVersion("main.go", nil, /*...*/)
store.PutVersion(v1)
store.SetHead("main.go", v1.ID)

v2 := NewFileVersion("main.go", []VersionID{v1.ID}, /*...*/)
store.PutVersion(v2)
store.SetHead("main.go", v2.ID)

// Query
head, _ := store.GetHead("main.go")
assert(head.ID == v2.ID)

history, _ := store.GetHistory("main.go", 10)
assert(len(history) == 2)

ancestor, _ := store.GetCommonAncestor(v2.ID, branchV.ID)
assert(ancestor.ID == v1.ID)
```

**Integration Points**:
- Supports variant group queries for Pipeline Variants
- Indexes by session for Session Management integration
- Used by CVS (Task 5.1) for version coordination

**Tests Required**:
- `TestDAGStore_PutVersion`
- `TestDAGStore_GetVersion`
- `TestDAGStore_GetHead`
- `TestDAGStore_SetHead`
- `TestDAGStore_GetHistory`
- `TestDAGStore_GetAncestors`
- `TestDAGStore_GetCommonAncestor`
- `TestDAGStore_GetChildren`
- `TestDAGStore_GetSessionVersions`
- `TestDAGStore_GetPipelineVersions`
- `TestDAGStore_GetVariantGroupVersions`
- `TestDAGStore_Concurrent`
- `TestPersistentDAGStore_Persistence`

**References**:
- Git object graph
- `core/pipeline/pipeline.go` Pipeline Variants
- `core/session/session.go` Session Management

---

### Task 2.4: Implement Write-Ahead Log for Crash Recovery

**Objective**: Create WAL for crash recovery of versioning operations.

**File**: `core/versioning/wal.go`

**Interface/Signature**:
```go
package versioning

import (
    "io"
    "sync"
)

// WALEntryType identifies the type of WAL entry
type WALEntryType uint8

const (
    WALEntryOperation WALEntryType = iota
    WALEntryVersion
    WALEntryHeadUpdate
    WALEntryBlob
    WALEntryCheckpoint
)

// WALEntry represents a single entry in the WAL
type WALEntry struct {
    Type      WALEntryType `json:"type"`
    Timestamp int64        `json:"timestamp"`
    SeqNum    uint64       `json:"seq_num"`
    SessionID string       `json:"session_id"`
    Data      []byte       `json:"data"`
    Checksum  uint32       `json:"checksum"` // CRC32
}

// WAL provides write-ahead logging for crash recovery
type WAL interface {
    // Append adds an entry to the WAL
    Append(entry *WALEntry) error

    // Sync forces all pending writes to disk
    Sync() error

    // Replay replays all entries since last checkpoint
    // Calls fn for each entry; stops if fn returns error
    Replay(fn func(*WALEntry) error) error

    // Checkpoint creates a checkpoint and truncates old entries
    Checkpoint() error

    // Close closes the WAL
    Close() error
}

// FileWAL is a file-based WAL implementation
type FileWAL struct {
    path          string
    file          *os.File
    mu            sync.Mutex
    seqNum        uint64
    lastCheckpoint uint64

    // Segment management
    segmentSize   int64         // Max segment size before rotation
    segments      []string      // Active segment files
}

func NewFileWAL(path string, opts WALOptions) (*FileWAL, error)

// WALOptions configures the WAL
type WALOptions struct {
    SegmentSize     int64         // Default: 64MB
    SyncMode        WALSyncMode   // Sync strategy
    CheckpointFreq  time.Duration // Auto-checkpoint frequency
}

type WALSyncMode int

const (
    WALSyncNone      WALSyncMode = iota // No sync (fastest, least durable)
    WALSyncPerEntry                      // Sync after each entry (slowest, most durable)
    WALSyncPeriodic                      // Sync periodically (balanced)
)

// WALRecovery handles crash recovery using WAL
type WALRecovery struct {
    wal        *FileWAL
    dagStore   DAGStore
    blobStore  BlobStore
    opLog      OperationLog
}

func NewWALRecovery(wal *FileWAL, dag DAGStore, blob BlobStore, opLog OperationLog) *WALRecovery

// Recover replays WAL to restore state after crash
func (r *WALRecovery) Recover() error
```

**Acceptance Criteria**:
1. All state changes are logged before being applied
2. `Sync()` guarantees durability
3. `Replay()` can recover all state after crash
4. Checkpointing truncates old entries
5. CRC32 checksums detect corruption
6. Segment rotation prevents unbounded growth
7. Thread-safe for concurrent access

**Examples**:
```go
wal, _ := NewFileWAL("/var/sylk/wal", WALOptions{
    SegmentSize:    64 * 1024 * 1024, // 64MB
    SyncMode:       WALSyncPeriodic,
    CheckpointFreq: 5 * time.Minute,
})

// Log operation
entry := &WALEntry{
    Type:      WALEntryOperation,
    Timestamp: time.Now().UnixNano(),
    SessionID: "session-A",
    Data:      marshalOperation(op),
}
wal.Append(entry)
wal.Sync()

// After crash: recover
recovery := NewWALRecovery(wal, dagStore, blobStore, opLog)
recovery.Recover()
```

**Integration Points**:
- Used by BlobStore, OperationLog, DAGStore for durability
- Session-aware for per-session recovery
- Integrates with existing session WAL pattern from ARCHITECTURE.md

**Tests Required**:
- `TestWAL_Append`
- `TestWAL_Sync`
- `TestWAL_Replay`
- `TestWAL_Checkpoint`
- `TestWAL_SegmentRotation`
- `TestWAL_ChecksumValidation`
- `TestWAL_CrashRecovery`
- `TestWAL_ConcurrentAppend`

**References**:
- Database WAL patterns
- `ARCHITECTURE.md` session WAL references
- SQLite WAL implementation

---

## Phase 3: Virtual File System

### Task 3.1: Implement VFS (Virtual File System)

**Objective**: Create VFS implementation that extends existing VirtualFilesystem with MVCC support.

**File**: `core/versioning/vfs.go`

**Interface/Signature**:
```go
package versioning

import (
    "io"
    "sync"
    "core/session"
    "core/pipeline"
    "core/security"
)

// VFS provides versioned file system operations
// Extends existing VirtualFilesystem patterns with MVCC support
type VFS interface {
    // Core file operations
    Read(path string) ([]byte, error)
    Write(path string, content []byte) error
    Delete(path string) error
    Exists(path string) bool
    List(dir string) ([]string, error)

    // Versioned operations
    ReadAt(path string, version VersionID) ([]byte, error)
    History(path string, limit int) ([]*FileVersion, error)
    Diff(path string, v1, v2 VersionID) (*FileDiff, error)

    // Transaction support
    BeginTransaction() (*VFSTransaction, error)

    // Version management
    GetCurrentVersion(path string) VersionID
    GetBaseVersion() VersionID  // Starting point for this VFS

    // Staging (for variant isolation)
    ChangedFiles() []string
    StagingDir() string

    // Integration with existing patterns
    ValidatePath(path string) error      // From existing VirtualFilesystem
    PrepareForCommand(cmd *exec.Cmd) error
    MergeChanges(cmd *exec.Cmd) error
}

// PipelineVFS is a VFS scoped to a specific pipeline
type PipelineVFS struct {
    base          VFS
    pipelineID    pipeline.PipelineID
    sessionID     session.SessionID
    variantGroupID *string

    // Security integration
    permMgr       *security.PermissionManager
    sanitizer     *security.SecretSanitizer
    agentRole     security.AgentRole

    // Staging
    stagingDir    string
    modifications map[string]*FileModification
    baseVersion   VersionID

    cvs           *CVS  // Central Version Store reference
    clock         VectorClock
    mu            sync.RWMutex
}

// NewPipelineVFS creates a VFS for a pipeline
func NewPipelineVFS(
    pipelineID pipeline.PipelineID,
    sessionID session.SessionID,
    baseVersion VersionID,
    cvs *CVS,
    permMgr *security.PermissionManager,
    agentRole security.AgentRole,
) (*PipelineVFS, error)

// VFSTransaction represents an atomic set of file operations
type VFSTransaction struct {
    vfs        *PipelineVFS
    operations []*Operation
    committed  bool
}

// Commit commits all operations atomically
func (tx *VFSTransaction) Commit() error

// Rollback discards all operations
func (tx *VFSTransaction) Rollback() error

// FileModification tracks a single file modification (from existing VirtualFilesystem)
type FileModification struct {
    OriginalPath string
    StagingPath  string
    Operation    string // "create", "modify", "delete"
    Timestamp    time.Time
    ContentHash  ContentHash  // For MVCC
    BaseVersion  VersionID    // Version this modification is based on
}

// FileDiff represents differences between two versions
type FileDiff struct {
    FilePath string
    OldVersion VersionID
    NewVersion VersionID
    Hunks    []DiffHunk
}

type DiffHunk struct {
    OldStart  int
    OldCount  int
    NewStart  int
    NewCount  int
    Lines     []DiffLine
}

type DiffLine struct {
    Type    DiffLineType // Added, Removed, Context
    Content string
}

type DiffLineType int

const (
    DiffLineContext DiffLineType = iota
    DiffLineAdded
    DiffLineRemoved
)
```

**Acceptance Criteria**:
1. Extends existing VirtualFilesystem pattern with MVCC support
2. Security integration: validates agent role before write operations
3. Secret sanitization: redacts secrets before storing content
4. Path validation: uses existing ValidatePath from VirtualFilesystem
5. Staging directory isolation for pipeline work
6. Transaction support for atomic multi-file operations
7. Version history accessible per file
8. Diff generation between versions
9. Thread-safe for concurrent access

**Examples**:
```go
// Create pipeline-scoped VFS
vfs, _ := NewPipelineVFS(
    pipeline.PipelineID("pipeline-1"),
    session.SessionID("session-A"),
    baseVersion,
    cvs,
    permMgr,
    security.RoleWorker,
)

// Write (security checked)
err := vfs.Write("handler.go", []byte("new content"))
if errors.Is(err, security.ErrInsufficientRole) {
    // Agent doesn't have permission
}

// Transaction
tx, _ := vfs.BeginTransaction()
tx.Write("file1.go", content1)
tx.Write("file2.go", content2)
tx.Commit() // Atomic

// Version history
history, _ := vfs.History("handler.go", 10)

// Diff
diff, _ := vfs.Diff("handler.go", v1, v2)
```

**Integration Points**:
- Extends `core/security/vfs.go` VirtualFilesystem patterns
- Uses `security.PermissionManager` for access control
- Uses `security.SecretSanitizer` before storing
- Uses `security.AgentRole` for permission checks
- Integrates with CVS for version coordination

**Tests Required**:
- `TestPipelineVFS_Read`
- `TestPipelineVFS_Write`
- `TestPipelineVFS_Write_PermissionDenied`
- `TestPipelineVFS_Write_SecretSanitization`
- `TestPipelineVFS_Delete`
- `TestPipelineVFS_Transaction_Commit`
- `TestPipelineVFS_Transaction_Rollback`
- `TestPipelineVFS_History`
- `TestPipelineVFS_Diff`
- `TestPipelineVFS_ReadAt`
- `TestPipelineVFS_Concurrent`
- `TestPipelineVFS_PathValidation`

**References**:
- `ARCHITECTURE.md` VirtualFilesystem section
- `ARCHITECTURE.md` Security Model section
- `FILESYSTEM.md` VFS specification

---

### Task 3.2: Implement Pipeline VFS Manager

**Objective**: Create manager for VFS instances across pipelines and variant groups.

**File**: `core/versioning/vfs_manager.go`

**Interface/Signature**:
```go
package versioning

import (
    "sync"
    "core/session"
    "core/pipeline"
    "core/security"
)

// VFSManager manages VFS instances for pipelines and variant groups
type VFSManager interface {
    // Pipeline VFS
    CreatePipelineVFS(cfg CreatePipelineVFSConfig) (*PipelineVFS, error)
    GetPipelineVFS(pipelineID pipeline.PipelineID) (*PipelineVFS, error)
    ClosePipelineVFS(pipelineID pipeline.PipelineID) error

    // Variant group VFS management (for Pipeline Variants)
    CreateVariantGroup(cfg CreateVariantGroupConfig) (*VariantVFSGroup, error)
    GetVariantGroup(groupID string) (*VariantVFSGroup, error)
    AddVariantToGroup(groupID string, variantID string, cfg AddVariantConfig) (*PipelineVFS, error)
    SelectVariant(groupID string, variantID string) error
    CancelVariantGroup(groupID string) error

    // Session queries
    GetSessionVFSes(sessionID session.SessionID) []*PipelineVFS

    // Cleanup
    CleanupSession(sessionID session.SessionID) error
    CleanupStaging() error  // Remove orphaned staging dirs
}

// CreatePipelineVFSConfig configures a new pipeline VFS
type CreatePipelineVFSConfig struct {
    PipelineID  pipeline.PipelineID
    SessionID   session.SessionID
    BaseVersion VersionID  // nil for HEAD
    AgentRole   security.AgentRole
    StagingDir  string     // Optional override
}

// CreateVariantGroupConfig configures a variant group
type CreateVariantGroupConfig struct {
    GroupID       string
    SessionID     session.SessionID
    OriginalPipelineID pipeline.PipelineID
    StartingPoint VersionID  // Git HEAD when original started
}

// AddVariantConfig configures adding a variant to a group
type AddVariantConfig struct {
    PipelineID pipeline.PipelineID
    Label      string  // "original", "variant-1", etc.
    AgentRole  security.AgentRole
}

// VariantVFSGroup manages VFS instances for a variant group
type VariantVFSGroup struct {
    ID            string
    SessionID     session.SessionID
    StartingPoint VersionID
    OriginalVFS   *PipelineVFS
    VariantVFSes  map[string]*PipelineVFS  // variantID -> VFS
    Status        VariantGroupStatus       // From pipeline.VariantGroupStatus
    SelectedID    *string
    mu            sync.RWMutex
}

// DefaultVFSManager is the standard implementation
type DefaultVFSManager struct {
    cvs           *CVS
    permMgr       *security.PermissionManager
    sanitizer     *security.SecretSanitizer
    auditLog      *AuditLog

    pipelineVFSes map[pipeline.PipelineID]*PipelineVFS
    variantGroups map[string]*VariantVFSGroup
    bySession     map[session.SessionID][]pipeline.PipelineID

    stagingRoot   string
    mu            sync.RWMutex
}

func NewVFSManager(cvs *CVS, permMgr *security.PermissionManager, stagingRoot string) *DefaultVFSManager
```

**Acceptance Criteria**:
1. Creates and manages VFS instances for pipelines
2. Supports variant group isolation (from Pipeline Variants architecture)
3. Variant selection commits chosen variant's changes
4. Cleanup removes staging directories when done
5. Session-scoped tracking for multi-session coordination
6. Thread-safe for concurrent access

**Examples**:
```go
mgr := NewVFSManager(cvs, permMgr, "~/.sylk/staging")

// Create pipeline VFS
vfs, _ := mgr.CreatePipelineVFS(CreatePipelineVFSConfig{
    PipelineID: pipeline.PipelineID("pipeline-1"),
    SessionID:  session.SessionID("session-A"),
    AgentRole:  security.RoleWorker,
})

// Create variant group (when user requests variant)
group, _ := mgr.CreateVariantGroup(CreateVariantGroupConfig{
    GroupID:            "variant-group-123",
    SessionID:          session.SessionID("session-A"),
    OriginalPipelineID: pipeline.PipelineID("pipeline-1"),
    StartingPoint:      headVersion,
})

// Add variant
variantVFS, _ := mgr.AddVariantToGroup("variant-group-123", "variant-1", AddVariantConfig{
    PipelineID: pipeline.PipelineID("pipeline-2"),
    Label:      "variant-1",
    AgentRole:  security.RoleWorker,
})

// User selects variant
mgr.SelectVariant("variant-group-123", "variant-1")
```

**Integration Points**:
- Integrates with `pipeline.VariantGroup` from Pipeline Variants
- Uses `security.PermissionManager` for access control
- Uses CVS for version coordination
- Manages staging directories following FilesystemManager patterns

**Tests Required**:
- `TestVFSManager_CreatePipelineVFS`
- `TestVFSManager_GetPipelineVFS`
- `TestVFSManager_ClosePipelineVFS`
- `TestVFSManager_CreateVariantGroup`
- `TestVFSManager_AddVariantToGroup`
- `TestVFSManager_SelectVariant`
- `TestVFSManager_CancelVariantGroup`
- `TestVFSManager_GetSessionVFSes`
- `TestVFSManager_CleanupSession`
- `TestVFSManager_CleanupStaging`
- `TestVFSManager_Concurrent`

**References**:
- `ARCHITECTURE.md` Pipeline Variants section
- `ARCHITECTURE.md` FilesystemManager section
- `FILESYSTEM.md` VFS Manager specification

---

## Phase 4: Operational Transformation Engine

### Task 4.1: Implement Diff Algorithm for Operations

**Objective**: Create algorithms for computing differences between file versions.

**File**: `core/versioning/diff.go`

**Interface/Signature**:
```go
package versioning

// Differ computes differences between file versions
type Differ interface {
    // DiffBytes computes diff between two byte slices
    DiffBytes(old, new []byte) (*FileDiff, error)

    // DiffVersions computes diff between two versions
    DiffVersions(v1, v2 VersionID, cvs *CVS) (*FileDiff, error)

    // ToOperations converts a diff to operations
    ToOperations(diff *FileDiff, baseVersion VersionID, filePath string) ([]*Operation, error)

    // FromOperations reconstructs content by applying operations
    FromOperations(base []byte, ops []*Operation) ([]byte, error)
}

// MyersDiffer implements Myers diff algorithm
type MyersDiffer struct {
    // Options
    contextLines int  // Lines of context around changes
}

func NewMyersDiffer(contextLines int) *MyersDiffer

// ASTDiffer computes AST-aware diffs
type ASTDiffer struct {
    parser ASTParser
}

func NewASTDiffer(parser ASTParser) *ASTDiffer

// DiffAST computes diff at AST node level
func (d *ASTDiffer) DiffAST(oldContent, newContent []byte) (*ASTDiff, error)

// ASTDiff represents AST-level differences
type ASTDiff struct {
    FilePath   string
    Changes    []ASTChange
}

type ASTChange struct {
    Type       ASTChangeType  // Added, Removed, Modified, Moved
    Target     Target         // AST target
    OldContent []byte
    NewContent []byte
}

type ASTChangeType int

const (
    ASTChangeAdded ASTChangeType = iota
    ASTChangeRemoved
    ASTChangeModified
    ASTChangeMoved
)
```

**Acceptance Criteria**:
1. Myers diff algorithm correctly computes minimal edit distance
2. AST-aware diff identifies node-level changes
3. Diff to operations conversion is reversible
4. Operations can reconstruct target content from base
5. Context lines configurable
6. Handles binary files gracefully (no diff, just replacement)

**Examples**:
```go
differ := NewMyersDiffer(3) // 3 lines context

old := []byte("line1\nline2\nline3")
new := []byte("line1\nmodified\nline3")

diff, _ := differ.DiffBytes(old, new)
assert(len(diff.Hunks) == 1)
assert(diff.Hunks[0].Lines[1].Type == DiffLineRemoved)
assert(diff.Hunks[0].Lines[2].Type == DiffLineAdded)

ops, _ := differ.ToOperations(diff, baseVersion, "file.go")
reconstructed, _ := differ.FromOperations(old, ops)
assert(bytes.Equal(reconstructed, new))
```

**Integration Points**:
- Used by OT engine for conflict detection
- Used by CVS for three-way merge
- AST differ uses parsers from Phase 8

**Tests Required**:
- `TestMyersDiffer_DiffBytes`
- `TestMyersDiffer_DiffVersions`
- `TestMyersDiffer_ToOperations`
- `TestMyersDiffer_FromOperations`
- `TestMyersDiffer_RoundTrip`
- `TestMyersDiffer_BinaryFiles`
- `TestASTDiffer_DiffAST`
- `TestASTDiffer_NodeChanges`

**References**:
- Myers, "An O(ND) Difference Algorithm"
- Git diff implementation
- `FILESYSTEM.md` diff algorithms

---

### Task 4.2: Implement Operational Transformation

**Objective**: Create OT engine for transforming concurrent operations.

**File**: `core/versioning/ot.go`

**Interface/Signature**:
```go
package versioning

// OTEngine handles operational transformation for concurrent edits
type OTEngine interface {
    // Transform transforms op1 against op2 that was applied concurrently
    // Returns transformed op1 that achieves same intent when applied after op2
    Transform(op1, op2 *Operation) (*Operation, error)

    // TransformBatch transforms a batch of operations against another batch
    TransformBatch(ops1, ops2 []*Operation) ([]*Operation, error)

    // Compose combines sequential operations into equivalent single operation
    Compose(op1, op2 *Operation) (*Operation, error)

    // CanMergeAutomatically returns true if concurrent ops can be auto-merged
    CanMergeAutomatically(op1, op2 *Operation) bool

    // DetectConflict identifies conflicts between operations
    DetectConflict(op1, op2 *Operation) *Conflict
}

// Conflict represents a conflict between two operations
type Conflict struct {
    Op1         *Operation
    Op2         *Operation
    Type        ConflictType
    Description string
    Resolutions []Resolution
}

type ConflictType int

const (
    ConflictTypeOverlappingEdit ConflictType = iota  // Both edit same region
    ConflictTypeDeleteEdit                            // One deletes, other edits
    ConflictTypeMoveEdit                              // One moves, other edits
    ConflictTypeSemanticConflict                      // AST-level conflict
)

// Resolution represents a possible conflict resolution
type Resolution struct {
    Label       string       // "Keep op1", "Keep op2", "Keep both", etc.
    Description string
    ResultOp    *Operation   // Resulting operation if this resolution is chosen
}

// DefaultOTEngine is the standard OT implementation
type DefaultOTEngine struct {
    astDiffer *ASTDiffer
}

func NewOTEngine(astDiffer *ASTDiffer) *DefaultOTEngine

// Transform implementation for different operation types
func (e *DefaultOTEngine) transformInsertInsert(op1, op2 *Operation) (*Operation, error)
func (e *DefaultOTEngine) transformInsertDelete(op1, op2 *Operation) (*Operation, error)
func (e *DefaultOTEngine) transformDeleteInsert(op1, op2 *Operation) (*Operation, error)
func (e *DefaultOTEngine) transformDeleteDelete(op1, op2 *Operation) (*Operation, error)
func (e *DefaultOTEngine) transformReplaceReplace(op1, op2 *Operation) (*Operation, error)
```

**Acceptance Criteria**:
1. Transform is symmetric: transform(a, b) + transform(b, a) converge
2. Insert-Insert: correct position adjustment
3. Insert-Delete: handles edge cases at boundaries
4. Delete-Delete: handles overlapping deletions
5. Replace-Replace: detects conflicts correctly
6. AST-aware: uses node paths for stable targeting
7. Conflict detection identifies all conflict types
8. Resolution suggestions provided for conflicts

**Examples**:
```go
engine := NewOTEngine(astDiffer)

// Concurrent inserts at same position
op1 := NewOperation(/*...*/) // Insert "A" at line 5
op2 := NewOperation(/*...*/) // Insert "B" at line 5

transformed1, _ := engine.Transform(op1, op2)
// transformed1 now inserts "A" at line 6 (after op2's "B")

// Conflict detection
op3 := NewOperation(/*...*/) // Replace lines 5-10
op4 := NewOperation(/*...*/) // Replace lines 8-15

conflict := engine.DetectConflict(op3, op4)
assert(conflict.Type == ConflictTypeOverlappingEdit)
assert(len(conflict.Resolutions) >= 2) // Keep one, keep other, etc.
```

**Integration Points**:
- Uses AST differ for semantic conflict detection
- Used by CVS for merge operations
- Conflict UI integrations in Task 4.3

**Tests Required**:
- `TestOTEngine_Transform_InsertInsert`
- `TestOTEngine_Transform_InsertDelete`
- `TestOTEngine_Transform_DeleteInsert`
- `TestOTEngine_Transform_DeleteDelete`
- `TestOTEngine_Transform_ReplaceReplace`
- `TestOTEngine_TransformBatch`
- `TestOTEngine_Compose`
- `TestOTEngine_DetectConflict`
- `TestOTEngine_CanMergeAutomatically`
- `TestOTEngine_Symmetry`
- `TestOTEngine_Convergence`

**References**:
- OT theory papers (Sun & Ellis)
- Google Wave OT implementation
- `FILESYSTEM.md` OT specification

---

### Task 4.3: Implement Conflict Resolution UI Integration

**Objective**: Create hooks for presenting conflicts to users via Guide.

**File**: `core/versioning/conflict_ui.go`

**Interface/Signature**:
```go
package versioning

import (
    "context"
    "core/guide"
)

// ConflictResolver handles conflict resolution UI
type ConflictResolver interface {
    // ResolveConflict presents conflict to user and returns chosen resolution
    ResolveConflict(ctx context.Context, conflict *Conflict) (*Resolution, error)

    // ResolveConflictBatch presents multiple conflicts
    ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error)

    // AutoResolve attempts automatic resolution based on policy
    AutoResolve(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool)
}

// AutoResolvePolicy defines automatic resolution behavior
type AutoResolvePolicy int

const (
    AutoResolvePolicyNone        AutoResolvePolicy = iota // Always prompt user
    AutoResolvePolicyKeepNewest                            // Keep most recent
    AutoResolvePolicyKeepOldest                            // Keep oldest
    AutoResolvePolicyKeepBoth                              // Merge both changes
)

// GuideConflictResolver routes conflicts through Guide to user
type GuideConflictResolver struct {
    guide guide.Guide
    policy AutoResolvePolicy
}

func NewGuideConflictResolver(guide guide.Guide, policy AutoResolvePolicy) *GuideConflictResolver

// ConflictMessage is sent to user via Guide
type ConflictMessage struct {
    Conflict    *Conflict
    FilePath    string
    SessionID   string
    PipelineID  string
    Options     []ConflictOption
}

type ConflictOption struct {
    ID          string
    Label       string
    Description string
    Preview     string  // Code preview of result
}

// ConflictResponse from user
type ConflictResponse struct {
    ChosenOptionID string
    CustomMerge    []byte  // If user provides custom resolution
}

// UserConflictHandler is a Guide skill for handling conflicts
type UserConflictHandler struct {
    resolver ConflictResolver
}

// HandleConflictSignal processes CONFLICT_DETECTED signals
func (h *UserConflictHandler) HandleConflictSignal(signal *Signal) error
```

**Acceptance Criteria**:
1. Conflicts are routed through Guide to user
2. User sees diff preview for each resolution option
3. Auto-resolve policy can handle simple conflicts automatically
4. Custom merge option allows user to provide resolution
5. Batch resolution for multiple conflicts
6. Timeout handling for unresponsive users

**Examples**:
```go
resolver := NewGuideConflictResolver(guide, AutoResolvePolicyNone)

conflict := &Conflict{
    Op1: op1,
    Op2: op2,
    Type: ConflictTypeOverlappingEdit,
    Resolutions: []Resolution{
        {Label: "Keep op1", Description: "Use first change"},
        {Label: "Keep op2", Description: "Use second change"},
        {Label: "Keep both", Description: "Merge both changes"},
    },
}

// Routes to user via Guide
resolution, _ := resolver.ResolveConflict(ctx, conflict)

// Apply resolution
mergedOp := resolution.ResultOp
```

**Integration Points**:
- Uses Guide for user communication (from ARCHITECTURE.md)
- Creates CONFLICT_DETECTED signals
- Integrates with Pipeline for mid-execution conflict handling

**Tests Required**:
- `TestGuideConflictResolver_ResolveConflict`
- `TestGuideConflictResolver_ResolveConflictBatch`
- `TestGuideConflictResolver_AutoResolve`
- `TestGuideConflictResolver_CustomMerge`
- `TestGuideConflictResolver_Timeout`
- `TestUserConflictHandler_HandleSignal`

**References**:
- `ARCHITECTURE.md` Guide section
- Git merge conflict UI patterns
- `FILESYSTEM.md` conflict resolution

---

## Phase 5: Central Version Store

### Task 5.1: Implement Central Version Store (CVS)

**Objective**: Create the central coordinator for all version operations.

**File**: `core/versioning/cvs.go`

**Interface/Signature**:
```go
package versioning

import (
    "context"
    "sync"
    "core/session"
    "core/pipeline"
    "core/security"
)

// CVS is the Central Version Store - the main coordinator
type CVS interface {
    // File operations (through VFS)
    Read(ctx context.Context, filePath string) ([]byte, error)
    Write(ctx context.Context, filePath string, content []byte, meta WriteMetadata) (VersionID, error)
    Delete(ctx context.Context, filePath string, meta WriteMetadata) (VersionID, error)

    // Version queries
    GetVersion(versionID VersionID) (*FileVersion, error)
    GetHead(filePath string) (*FileVersion, error)
    GetHistory(filePath string, opts HistoryOptions) ([]*FileVersion, error)

    // Pipeline operations
    BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error)
    CommitPipeline(pipelineID pipeline.PipelineID) ([]VersionID, error)
    RollbackPipeline(pipelineID pipeline.PipelineID) error

    // Merge operations
    Merge(ctx context.Context, v1, v2 VersionID, resolver ConflictResolver) (VersionID, error)
    ThreeWayMerge(ctx context.Context, base, ours, theirs VersionID, resolver ConflictResolver) (VersionID, error)

    // Cross-session coordination
    AcquireFileLock(filePath string, sessionID session.SessionID) (FileLock, error)
    ReleaseFileLock(lock FileLock) error

    // Variant operations (from Pipeline Variants)
    BeginVariantGroup(cfg BeginVariantGroupConfig) (*VariantVFSGroup, error)
    AddVariant(groupID string, cfg AddVariantConfig) (*PipelineVFS, error)
    SelectVariant(groupID string, variantID string) ([]VersionID, error)

    // Subscriptions (for cross-session notifications)
    Subscribe(filePath string, sessionID session.SessionID, cb FileChangeCallback) (SubscriptionID, error)
    Unsubscribe(subID SubscriptionID) error

    // Admin
    Stats() CVSStats
    GC() error
}

// WriteMetadata provides context for write operations
type WriteMetadata struct {
    PipelineID pipeline.PipelineID
    SessionID  session.SessionID
    AgentID    string
    AgentRole  security.AgentRole
    Message    string  // Commit message equivalent
}

// BeginPipelineConfig configures a new pipeline
type BeginPipelineConfig struct {
    PipelineID  pipeline.PipelineID
    SessionID   session.SessionID
    BaseVersion VersionID  // nil = HEAD
    AgentRole   security.AgentRole
    Files       []string   // Files this pipeline will touch (optional hint)
}

// BeginVariantGroupConfig configures a variant group
type BeginVariantGroupConfig struct {
    GroupID            string
    SessionID          session.SessionID
    OriginalPipelineID pipeline.PipelineID
}

// HistoryOptions configures history queries
type HistoryOptions struct {
    Limit     int
    Since     *time.Time
    Until     *time.Time
    SessionID *session.SessionID   // Filter by session
    PipelineID *pipeline.PipelineID // Filter by pipeline
}

// FileLock represents an advisory lock on a file
type FileLock struct {
    ID        string
    FilePath  string
    SessionID session.SessionID
    AcquiredAt time.Time
    ExpiresAt  time.Time
}

// FileChangeCallback is called when a file changes
type FileChangeCallback func(event FileChangeEvent)

type FileChangeEvent struct {
    FilePath    string
    NewVersion  VersionID
    OldVersion  VersionID
    ChangeType  FileChangeType
    SessionID   session.SessionID
    PipelineID  pipeline.PipelineID
}

type FileChangeType int

const (
    FileChangeCreated FileChangeType = iota
    FileChangeModified
    FileChangeDeleted
)

// SubscriptionID identifies a subscription
type SubscriptionID string

// CVSStats contains CVS statistics
type CVSStats struct {
    TotalFiles      int64
    TotalVersions   int64
    TotalOperations int64
    ActivePipelines int64
    ActiveVariants  int64
    ActiveLocks     int64
}

// DefaultCVS is the standard CVS implementation
type DefaultCVS struct {
    blobStore    BlobStore
    opLog        OperationLog
    dagStore     DAGStore
    vfsManager   *DefaultVFSManager
    otEngine     OTEngine
    wal          *FileWAL

    // Security
    permMgr      *security.PermissionManager
    sanitizer    *security.SecretSanitizer

    // Locks
    fileLocks    map[string]*FileLock

    // Subscriptions
    subscriptions map[string]map[SubscriptionID]FileChangeCallback

    mu           sync.RWMutex
}

func NewCVS(cfg CVSConfig) (*DefaultCVS, error)

type CVSConfig struct {
    StoragePath string
    WALPath     string
    PermMgr     *security.PermissionManager
    Sanitizer   *security.SecretSanitizer
}
```

**Acceptance Criteria**:
1. All file operations are versioned and atomic
2. Pipeline isolation: changes are staged until commit
3. Merge operations use OT for conflict resolution
4. File locks prevent concurrent writes from different sessions
5. Subscriptions notify of cross-session changes
6. Variant operations support Pipeline Variants workflow
7. Security checks enforced for all operations
8. WAL ensures crash recovery

**Examples**:
```go
cvs, _ := NewCVS(CVSConfig{
    StoragePath: "~/.sylk/versions",
    WALPath:     "~/.sylk/wal",
    PermMgr:     permMgr,
    Sanitizer:   sanitizer,
})

// Begin pipeline
vfs, _ := cvs.BeginPipeline(BeginPipelineConfig{
    PipelineID: pipeline.PipelineID("pipeline-1"),
    SessionID:  session.SessionID("session-A"),
    AgentRole:  security.RoleWorker,
})

// Write through VFS
vfs.Write("handler.go", []byte("new content"))

// Commit pipeline
newVersions, _ := cvs.CommitPipeline(pipeline.PipelineID("pipeline-1"))

// Cross-session subscription
cvs.Subscribe("handler.go", session.SessionID("session-B"), func(event FileChangeEvent) {
    fmt.Printf("File changed: %s\n", event.FilePath)
})
```

**Integration Points**:
- Coordinates all storage components (BlobStore, OpLog, DAGStore)
- Uses VFSManager for pipeline/variant VFS management
- Integrates with security for permission checking
- Provides file change notifications for cross-session coordination

**Tests Required**:
- `TestCVS_Read`
- `TestCVS_Write`
- `TestCVS_Delete`
- `TestCVS_BeginPipeline`
- `TestCVS_CommitPipeline`
- `TestCVS_RollbackPipeline`
- `TestCVS_Merge`
- `TestCVS_ThreeWayMerge`
- `TestCVS_FileLock`
- `TestCVS_Subscribe`
- `TestCVS_BeginVariantGroup`
- `TestCVS_SelectVariant`
- `TestCVS_Concurrent`
- `TestCVS_CrashRecovery`

**References**:
- `FILESYSTEM.md` CVS specification
- `ARCHITECTURE.md` Pipeline Variants
- `ARCHITECTURE.md` Security Model

---

## Phase 6: Pipeline Integration

### Task 6.1: Integrate VFS with Pipeline Runner

**Objective**: Integrate versioned VFS with existing Pipeline execution.

**File**: `core/pipeline/vfs_integration.go`

**Interface/Signature**:
```go
package pipeline

import (
    "core/versioning"
    "core/session"
    "core/security"
)

// PipelineVFSIntegration provides VFS access to pipelines
type PipelineVFSIntegration struct {
    cvs        *versioning.CVS
    vfsManager *versioning.DefaultVFSManager
}

func NewPipelineVFSIntegration(cvs *versioning.CVS) *PipelineVFSIntegration

// SetupPipeline creates VFS for a pipeline
func (i *PipelineVFSIntegration) SetupPipeline(p *Pipeline) error

// TeardownPipeline commits or rolls back VFS
func (i *PipelineVFSIntegration) TeardownPipeline(p *Pipeline, success bool) error

// GetVFS returns the VFS for a pipeline's engineer
func (i *PipelineVFSIntegration) GetVFS(pipelineID PipelineID) (*versioning.PipelineVFS, error)

// PipelineWithVFS extends Pipeline with VFS support
type PipelineWithVFS struct {
    *Pipeline
    vfs         *versioning.PipelineVFS
    baseVersion versioning.VersionID
}

// OnFileWrite hook for tracking writes
func (p *PipelineWithVFS) OnFileWrite(path string, version versioning.VersionID)

// OnFileRead hook for tracking reads
func (p *PipelineWithVFS) OnFileRead(path string)

// ModifiedFiles returns files modified by this pipeline
func (p *PipelineWithVFS) ModifiedFiles() []string

// UpdatedEngineerSkills adds VFS-aware file skills
func UpdatedEngineerSkills(vfs *versioning.PipelineVFS) []SkillDefinition
```

**Acceptance Criteria**:
1. Pipelines automatically get VFS on creation
2. Engineer file operations route through VFS
3. Pipeline commit creates new versions
4. Pipeline failure rolls back changes
5. File tracking updated in PipelineContext
6. Engineer skills updated to use VFS

**Examples**:
```go
integration := NewPipelineVFSIntegration(cvs)

// Setup VFS when pipeline created
pipeline, _ := pipelineManager.Create(ctx, cfg)
integration.SetupPipeline(pipeline)

// Engineer uses VFS through skills
vfs, _ := integration.GetVFS(pipeline.ID)
// vfs is passed to engineer_read_file, engineer_write_file skills

// On completion
integration.TeardownPipeline(pipeline, true) // commits
// OR
integration.TeardownPipeline(pipeline, false) // rolls back
```

**Integration Points**:
- Modifies existing Pipeline struct from `core/pipeline/pipeline.go`
- Updates engineer skills to use VFS
- Integrates with PipelineContext for file tracking

**Tests Required**:
- `TestPipelineVFSIntegration_SetupPipeline`
- `TestPipelineVFSIntegration_TeardownPipeline_Success`
- `TestPipelineVFSIntegration_TeardownPipeline_Failure`
- `TestPipelineVFSIntegration_GetVFS`
- `TestPipelineWithVFS_ModifiedFiles`
- `TestUpdatedEngineerSkills`

**References**:
- `ARCHITECTURE.md` Pipeline Architecture
- `core/pipeline/pipeline.go` existing implementation

---

### Task 6.2: Integrate with Pipeline Scheduler

**Objective**: Update Pipeline Scheduler for VFS coordination.

**File**: `core/session/pipeline_scheduler_vfs.go`

**Interface/Signature**:
```go
package session

import (
    "core/versioning"
    "core/pipeline"
)

// PipelineSchedulerVFS extends PipelineScheduler with VFS coordination
type PipelineSchedulerVFS struct {
    *PipelineScheduler
    cvs        *versioning.CVS
    vfsManager *versioning.DefaultVFSManager
}

func NewPipelineSchedulerVFS(base *PipelineScheduler, cvs *versioning.CVS) *PipelineSchedulerVFS

// ScheduleWithVFS schedules a task with VFS setup
func (s *PipelineSchedulerVFS) ScheduleWithVFS(task *ScheduledTask) error

// OnTaskComplete handles VFS commit on success
func (s *PipelineSchedulerVFS) OnTaskComplete(taskID string, success bool) error

// GetFileDependencies returns files a task depends on
func (s *PipelineSchedulerVFS) GetFileDependencies(taskID string) []string

// DetectFileConflicts checks for file conflicts between tasks
func (s *PipelineSchedulerVFS) DetectFileConflicts(tasks []*ScheduledTask) []FileConflict

// FileConflict represents a file access conflict
type FileConflict struct {
    FilePath string
    Tasks    []string
    Type     FileConflictType  // ReadWrite, WriteWrite
}

type FileConflictType int

const (
    FileConflictReadWrite FileConflictType = iota
    FileConflictWriteWrite
)
```

**Acceptance Criteria**:
1. Scheduler tracks file dependencies per task
2. Write-write conflicts detected before scheduling
3. Tasks with file conflicts serialized (not parallel)
4. VFS setup/teardown integrated with task lifecycle
5. Fair-share allocation respects VFS resource usage

**Examples**:
```go
scheduler := NewPipelineSchedulerVFS(baseScheduler, cvs)

// Schedule tasks
task1 := &ScheduledTask{Files: []string{"handler.go"}}
task2 := &ScheduledTask{Files: []string{"handler.go"}} // conflict!

conflicts := scheduler.DetectFileConflicts([]*ScheduledTask{task1, task2})
assert(len(conflicts) == 1)
assert(conflicts[0].Type == FileConflictWriteWrite)

// Scheduler serializes conflicting tasks
scheduler.ScheduleWithVFS(task1)
scheduler.ScheduleWithVFS(task2) // waits for task1 to complete
```

**Integration Points**:
- Extends `core/session/pipeline_scheduler.go`
- Uses CVS file locks for coordination
- Integrates with existing fair-share allocation

**Tests Required**:
- `TestPipelineSchedulerVFS_ScheduleWithVFS`
- `TestPipelineSchedulerVFS_OnTaskComplete`
- `TestPipelineSchedulerVFS_DetectFileConflicts`
- `TestPipelineSchedulerVFS_SerializeConflicts`
- `TestPipelineSchedulerVFS_FairShare`

**References**:
- `core/session/pipeline_scheduler.go` existing implementation
- `ARCHITECTURE.md` Pipeline Scheduler section

---

## Phase 7: Cross-Session Coordination

### Task 7.1: Extend Signal Dispatcher for File Changes

**Objective**: Add file change signals to cross-session communication.

**File**: `core/session/signal_dispatcher_vfs.go`

**Interface/Signature**:
```go
package session

import (
    "core/versioning"
)

// FileChangeSignalType identifies file change signals
const (
    SignalFileCreated  = "FILE_CREATED"
    SignalFileModified = "FILE_MODIFIED"
    SignalFileDeleted  = "FILE_DELETED"
    SignalFileLocked   = "FILE_LOCKED"
    SignalFileUnlocked = "FILE_UNLOCKED"
    SignalMergeConflict = "MERGE_CONFLICT"
)

// FileChangeSignal carries file change information
type FileChangeSignal struct {
    Signal
    FilePath    string               `json:"file_path"`
    OldVersion  versioning.VersionID `json:"old_version,omitempty"`
    NewVersion  versioning.VersionID `json:"new_version,omitempty"`
    ChangeType  FileChangeSignalType `json:"change_type"`
    ChangedBy   SessionID            `json:"changed_by"`
    PipelineID  string               `json:"pipeline_id,omitempty"`
}

type FileChangeSignalType string

const (
    FileChangeSignalCreated  FileChangeSignalType = "created"
    FileChangeSignalModified FileChangeSignalType = "modified"
    FileChangeSignalDeleted  FileChangeSignalType = "deleted"
)

// SignalDispatcherVFS extends SignalDispatcher with file change handling
type SignalDispatcherVFS struct {
    *SignalDispatcher
    cvs *versioning.CVS
}

func NewSignalDispatcherVFS(base *SignalDispatcher, cvs *versioning.CVS) *SignalDispatcherVFS

// BroadcastFileChange sends file change to all interested sessions
func (d *SignalDispatcherVFS) BroadcastFileChange(signal *FileChangeSignal) error

// SubscribeFileChanges registers for file change notifications
func (d *SignalDispatcherVFS) SubscribeFileChanges(sessionID SessionID, patterns []string) error

// UnsubscribeFileChanges removes file change subscription
func (d *SignalDispatcherVFS) UnsubscribeFileChanges(sessionID SessionID) error
```

**Acceptance Criteria**:
1. File changes broadcast to interested sessions
2. Sessions can subscribe to specific file patterns
3. Lock/unlock signals enable coordination
4. Merge conflict signals route to Guide
5. Signals include version information for sync

**Examples**:
```go
dispatcher := NewSignalDispatcherVFS(baseDispatcher, cvs)

// Session B subscribes to changes in handler.go
dispatcher.SubscribeFileChanges(
    SessionID("session-B"),
    []string{"handler.go", "*.proto"},
)

// Session A modifies file
signal := &FileChangeSignal{
    Signal:     Signal{Type: SignalFileModified},
    FilePath:   "handler.go",
    OldVersion: v1,
    NewVersion: v2,
    ChangeType: FileChangeSignalModified,
    ChangedBy:  SessionID("session-A"),
}
dispatcher.BroadcastFileChange(signal) // Session B receives
```

**Integration Points**:
- Extends `core/session/signal_dispatcher.go`
- Uses CVS subscriptions for change detection
- Routes conflict signals to Guide

**Tests Required**:
- `TestSignalDispatcherVFS_BroadcastFileChange`
- `TestSignalDispatcherVFS_SubscribeFileChanges`
- `TestSignalDispatcherVFS_PatternMatching`
- `TestSignalDispatcherVFS_UnsubscribeFileChanges`
- `TestSignalDispatcherVFS_MergeConflictSignal`

**References**:
- `core/session/signal_dispatcher.go` existing implementation
- `ARCHITECTURE.md` Cross-Session Communication

---

### Task 7.2: Extend Cross-Session Pool for File Coordination

**Objective**: Add file-level coordination to cross-session pool.

**File**: `core/session/cross_session_pool_vfs.go`

**Interface/Signature**:
```go
package session

import (
    "core/versioning"
)

// CrossSessionPoolVFS extends CrossSessionPool with file coordination
type CrossSessionPoolVFS struct {
    *CrossSessionPool
    cvs *versioning.CVS
}

func NewCrossSessionPoolVFS(base *CrossSessionPool, cvs *versioning.CVS) *CrossSessionPoolVFS

// AcquireFileAccess requests file access for a session
func (p *CrossSessionPoolVFS) AcquireFileAccess(sessionID SessionID, files []string, mode FileAccessMode) ([]versioning.FileLock, error)

// ReleaseFileAccess releases file access
func (p *CrossSessionPoolVFS) ReleaseFileAccess(locks []versioning.FileLock) error

// GetFileOwners returns sessions that have locks on files
func (p *CrossSessionPoolVFS) GetFileOwners(files []string) map[string]SessionID

// FileAccessMode defines read vs write access
type FileAccessMode int

const (
    FileAccessRead FileAccessMode = iota
    FileAccessWrite
)

// CoordinatedFileOperation wraps file operations with coordination
type CoordinatedFileOperation struct {
    pool      *CrossSessionPoolVFS
    sessionID SessionID
    locks     []versioning.FileLock
}

func (p *CrossSessionPoolVFS) BeginCoordinatedOp(sessionID SessionID, files []string, mode FileAccessMode) (*CoordinatedFileOperation, error)

func (op *CoordinatedFileOperation) Complete() error
func (op *CoordinatedFileOperation) Abort() error
```

**Acceptance Criteria**:
1. File locks coordinated across sessions
2. Read access allows concurrent reads
3. Write access exclusive per file
4. Lock timeout prevents deadlocks
5. Coordinated operations are atomic

**Examples**:
```go
pool := NewCrossSessionPoolVFS(basePool, cvs)

// Begin coordinated operation
op, _ := pool.BeginCoordinatedOp(
    SessionID("session-A"),
    []string{"handler.go", "config.go"},
    FileAccessWrite,
)
defer op.Complete()

// Do file operations...
// Other sessions trying to write these files will wait

// Check who owns files
owners := pool.GetFileOwners([]string{"handler.go"})
assert(owners["handler.go"] == SessionID("session-A"))
```

**Integration Points**:
- Extends `core/session/cross_session_pool.go`
- Uses CVS file locks
- Integrates with Pipeline Scheduler for task coordination

**Tests Required**:
- `TestCrossSessionPoolVFS_AcquireFileAccess`
- `TestCrossSessionPoolVFS_ReleaseFileAccess`
- `TestCrossSessionPoolVFS_ConcurrentReads`
- `TestCrossSessionPoolVFS_ExclusiveWrites`
- `TestCrossSessionPoolVFS_LockTimeout`
- `TestCrossSessionPoolVFS_CoordinatedOperation`

**References**:
- `core/session/cross_session_pool.go` existing implementation
- `ARCHITECTURE.md` Multi-Session Architecture

---

## Phase 8: AST Parsers

### Task 8.1: Implement Go AST Parser

**Objective**: Create Go-specific AST parser for stable targeting.

**File**: `core/versioning/parser_go.go`

**Interface/Signature**:
```go
package versioning

import (
    "go/ast"
    "go/parser"
    "go/token"
)

// ASTParser interface for language-specific parsers
type ASTParser interface {
    // Parse parses content into AST
    Parse(content []byte) (*AST, error)

    // FindNode finds a node by path
    FindNode(ast *AST, path []string) (*ASTNode, error)

    // GetTarget creates a Target from AST node
    GetTarget(node *ASTNode) Target

    // NodePath returns the path for a position
    NodePath(ast *AST, line, col int) ([]string, error)

    // Supports returns true if parser supports this file type
    Supports(filePath string) bool

    // Language returns the language name
    Language() string
}

// AST represents a parsed abstract syntax tree
type AST struct {
    Language string
    Root     *ASTNode
    Source   []byte
}

// ASTNode represents a node in the AST
type ASTNode struct {
    Type     string      // "FunctionDecl", "IfStmt", etc.
    Name     string      // Identifier name if applicable
    Path     []string    // Full path from root
    Start    Position
    End      Position
    Children []*ASTNode
}

// Position in source
type Position struct {
    Line   int
    Column int
    Offset int
}

// GoParser parses Go source code
type GoParser struct {
    fset *token.FileSet
}

func NewGoParser() *GoParser

// Parse implementation for Go
func (p *GoParser) Parse(content []byte) (*AST, error)

// FindNode implementation for Go
func (p *GoParser) FindNode(ast *AST, path []string) (*ASTNode, error)

// GetTarget implementation for Go
func (p *GoParser) GetTarget(node *ASTNode) Target

// NodePath implementation for Go
func (p *GoParser) NodePath(ast *AST, line, col int) ([]string, error)

// Supports returns true for .go files
func (p *GoParser) Supports(filePath string) bool

// Language returns "go"
func (p *GoParser) Language() string
```

**Acceptance Criteria**:
1. Parses valid Go source code
2. Creates stable node paths (func.FunctionName.body.stmt[0])
3. Handles nested structures correctly
4. Position mappings are accurate
5. Error recovery for partial parses
6. Supports Go modules syntax

**Examples**:
```go
parser := NewGoParser()

content := []byte(`
package main

func HandleRequest(w http.ResponseWriter, r *http.Request) {
    if r.Method == "POST" {
        // handle POST
    }
}
`)

ast, _ := parser.Parse(content)

// Find the if statement
path := []string{"func", "HandleRequest", "body", "if[0]"}
node, _ := parser.FindNode(ast, path)

assert(node.Type == "IfStmt")
assert(node.Start.Line == 5)

target := parser.GetTarget(node)
assert(target.NodePath[0] == "func")
assert(target.NodePath[1] == "HandleRequest")
```

**Integration Points**:
- Used by ASTDiffer for semantic diffs
- Used by OT engine for AST-aware transforms
- Supports Target creation for operations

**Tests Required**:
- `TestGoParser_Parse`
- `TestGoParser_FindNode`
- `TestGoParser_GetTarget`
- `TestGoParser_NodePath`
- `TestGoParser_NestedStructures`
- `TestGoParser_ErrorRecovery`
- `TestGoParser_Modules`

**References**:
- go/ast, go/parser stdlib
- `FILESYSTEM.md` AST targeting

---

### Task 8.2: Implement TypeScript/JavaScript Parser

**Objective**: Create TypeScript/JavaScript AST parser.

**File**: `core/versioning/parser_typescript.go`

**Interface/Signature**:
```go
package versioning

// TypeScriptParser parses TypeScript and JavaScript
type TypeScriptParser struct {
    // Uses tree-sitter or similar for parsing
}

func NewTypeScriptParser() *TypeScriptParser

// Implements ASTParser interface
func (p *TypeScriptParser) Parse(content []byte) (*AST, error)
func (p *TypeScriptParser) FindNode(ast *AST, path []string) (*ASTNode, error)
func (p *TypeScriptParser) GetTarget(node *ASTNode) Target
func (p *TypeScriptParser) NodePath(ast *AST, line, col int) ([]string, error)
func (p *TypeScriptParser) Supports(filePath string) bool // .ts, .tsx, .js, .jsx
func (p *TypeScriptParser) Language() string // "typescript"
```

**Acceptance Criteria**:
1. Parses TypeScript, JavaScript, TSX, JSX
2. Handles modern ES syntax (async/await, destructuring)
3. Type annotations parsed correctly
4. JSX elements tracked as nodes
5. Compatible node path format with Go parser

**Tests Required**:
- `TestTypeScriptParser_Parse`
- `TestTypeScriptParser_JSX`
- `TestTypeScriptParser_Async`
- `TestTypeScriptParser_Types`

**References**:
- tree-sitter-typescript
- `FILESYSTEM.md` AST targeting

---

### Task 8.3: Implement Python Parser

**Objective**: Create Python AST parser.

**File**: `core/versioning/parser_python.go`

**Interface/Signature**:
```go
package versioning

// PythonParser parses Python source code
type PythonParser struct {
    // Uses tree-sitter or similar
}

func NewPythonParser() *PythonParser

// Implements ASTParser interface
func (p *PythonParser) Parse(content []byte) (*AST, error)
func (p *PythonParser) FindNode(ast *AST, path []string) (*ASTNode, error)
func (p *PythonParser) GetTarget(node *ASTNode) Target
func (p *PythonParser) NodePath(ast *AST, line, col int) ([]string, error)
func (p *PythonParser) Supports(filePath string) bool // .py, .pyw
func (p *PythonParser) Language() string // "python"
```

**Acceptance Criteria**:
1. Parses Python 3 syntax
2. Handles decorators, async def, type hints
3. Class and function hierarchies tracked
4. Indentation-based scoping handled correctly

**Tests Required**:
- `TestPythonParser_Parse`
- `TestPythonParser_Classes`
- `TestPythonParser_Decorators`
- `TestPythonParser_AsyncDef`

**References**:
- tree-sitter-python
- `FILESYSTEM.md` AST targeting

---

### Task 8.4: Implement Parser Registry

**Objective**: Create registry for managing multiple parsers.

**File**: `core/versioning/parser_registry.go`

**Interface/Signature**:
```go
package versioning

import "sync"

// ParserRegistry manages language parsers
type ParserRegistry struct {
    parsers map[string]ASTParser  // extension -> parser
    mu      sync.RWMutex
}

var DefaultRegistry = NewParserRegistry()

func NewParserRegistry() *ParserRegistry

// Register adds a parser for file extensions
func (r *ParserRegistry) Register(parser ASTParser, extensions ...string)

// GetParser returns parser for a file path
func (r *ParserRegistry) GetParser(filePath string) (ASTParser, bool)

// SupportedExtensions returns all supported extensions
func (r *ParserRegistry) SupportedExtensions() []string

// Initialize registers default parsers
func (r *ParserRegistry) Initialize()
```

**Acceptance Criteria**:
1. Parsers registered by file extension
2. Fallback for unsupported extensions (offset-based targeting)
3. Thread-safe for concurrent access
4. Default parsers initialized on startup

**Tests Required**:
- `TestParserRegistry_Register`
- `TestParserRegistry_GetParser`
- `TestParserRegistry_Fallback`
- `TestParserRegistry_Initialize`

**References**:
- `FILESYSTEM.md` multi-language support

---

## Phase 9: Agent Skills and Hooks

### Task 9.1: Implement Versioning Skills for Engineers

**Objective**: Create skills for engineers to interact with versioned files.

**File**: `core/versioning/skills.go`

**Interface/Signature**:
```go
package versioning

import "core/skills"

// VersioningSkills returns skills for file versioning
func VersioningSkills(vfs *PipelineVFS) []skills.SkillDefinition {
    return []skills.SkillDefinition{
        {
            Name:        "versioned_read_file",
            Description: "Read a file at a specific version or HEAD",
            InputSchema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "path":    {"type": "string", "description": "File path"},
                    "version": {"type": "string", "description": "Version ID (optional, defaults to HEAD)"},
                },
                "required": []string{"path"},
            },
        },
        {
            Name:        "versioned_write_file",
            Description: "Write content to a file (creates new version)",
            InputSchema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "path":    {"type": "string"},
                    "content": {"type": "string"},
                    "message": {"type": "string", "description": "Change description"},
                },
                "required": []string{"path", "content"},
            },
        },
        {
            Name:        "versioned_file_history",
            Description: "Get version history for a file",
            InputSchema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "path":  {"type": "string"},
                    "limit": {"type": "integer", "default": 10},
                },
                "required": []string{"path"},
            },
        },
        {
            Name:        "versioned_file_diff",
            Description: "Get diff between two versions",
            InputSchema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "path":         {"type": "string"},
                    "old_version":  {"type": "string"},
                    "new_version":  {"type": "string", "description": "Optional, defaults to HEAD"},
                },
                "required": []string{"path", "old_version"},
            },
        },
        {
            Name:        "versioned_rollback",
            Description: "Rollback file to a previous version",
            InputSchema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "path":    {"type": "string"},
                    "version": {"type": "string"},
                },
                "required": []string{"path", "version"},
            },
        },
    }
}

// VersioningSkillHandler handles skill invocations
type VersioningSkillHandler struct {
    vfs *PipelineVFS
    cvs *CVS
}

func NewVersioningSkillHandler(vfs *PipelineVFS, cvs *CVS) *VersioningSkillHandler

func (h *VersioningSkillHandler) Handle(skillName string, params map[string]any) (any, error)
```

**Acceptance Criteria**:
1. Engineers can read files at specific versions
2. Write operations create new versions with messages
3. History queries return version information
4. Diff shows changes between versions
5. Rollback creates new version with old content
6. Security checks enforced for all operations

**Examples**:
```go
skills := VersioningSkills(vfs)
handler := NewVersioningSkillHandler(vfs, cvs)

// Engineer reads file history
result, _ := handler.Handle("versioned_file_history", map[string]any{
    "path":  "handler.go",
    "limit": 5,
})
// Returns: [{version: "abc123", timestamp: "...", message: "..."}, ...]

// Engineer rolls back
result, _ = handler.Handle("versioned_rollback", map[string]any{
    "path":    "handler.go",
    "version": "abc123",
})
```

**Integration Points**:
- Replaces/extends engineer_read_file, engineer_write_file skills
- Integrates with VFS for version tracking
- Respects agent role for permission checks

**Tests Required**:
- `TestVersioningSkills_Read`
- `TestVersioningSkills_ReadAtVersion`
- `TestVersioningSkills_Write`
- `TestVersioningSkills_History`
- `TestVersioningSkills_Diff`
- `TestVersioningSkills_Rollback`
- `TestVersioningSkills_PermissionCheck`

**References**:
- `ARCHITECTURE.md` Engineer Skill Definitions
- `core/skills/skills.go` existing skill system

---

### Task 9.2: Implement File Operation Hooks

**Objective**: Create hooks for file operations to integrate with Archivalist and Audit.

**File**: `core/versioning/hooks.go`

**Interface/Signature**:
```go
package versioning

import (
    "context"
    "core/hooks"
    "core/archivalist"
)

// FileOperationHooks provides hooks for file operations
type FileOperationHooks struct {
    archivalist *archivalist.Archivalist
    auditLog    *AuditLog
}

func NewFileOperationHooks(arch *archivalist.Archivalist, audit *AuditLog) *FileOperationHooks

// PreWriteHook validates and sanitizes before write
func (h *FileOperationHooks) PreWriteHook() hooks.Hook {
    return hooks.Hook{
        Name:     "file_pre_write",
        Type:     hooks.PreTool,
        Priority: hooks.HookPriorityFirst,
        Handler: func(ctx context.Context, data *hooks.ToolCallHookData) (*hooks.ToolCallHookData, error) {
            // Validate permissions
            // Sanitize secrets
            // Check file locks
            return data, nil
        },
    }
}

// PostWriteHook records file changes
func (h *FileOperationHooks) PostWriteHook() hooks.Hook {
    return hooks.Hook{
        Name:     "file_post_write",
        Type:     hooks.PostTool,
        Priority: hooks.HookPriorityNormal,
        Handler: func(ctx context.Context, data *hooks.ToolCallHookData) (*hooks.ToolCallHookData, error) {
            // Record to Archivalist
            // Update session context
            // Emit file change signal
            return data, nil
        },
    }
}

// ConflictDetectionHook checks for concurrent modification
func (h *FileOperationHooks) ConflictDetectionHook() hooks.Hook {
    return hooks.Hook{
        Name:     "file_conflict_detection",
        Type:     hooks.PreTool,
        Priority: hooks.HookPriorityEarly,
        Handler: func(ctx context.Context, data *hooks.ToolCallHookData) (*hooks.ToolCallHookData, error) {
            // Check if file modified since last read
            // Trigger conflict resolution if needed
            return data, nil
        },
    }
}

// RegisterHooks registers all file operation hooks
func (h *FileOperationHooks) RegisterHooks(registry *hooks.HookRegistry) {
    registry.Register(h.PreWriteHook())
    registry.Register(h.PostWriteHook())
    registry.Register(h.ConflictDetectionHook())
}
```

**Acceptance Criteria**:
1. Pre-write hook validates permissions and sanitizes secrets
2. Post-write hook records changes to Archivalist
3. Conflict detection warns of concurrent modifications
4. Hooks integrate with existing hook system
5. Session context updated with file changes

**Examples**:
```go
hooks := NewFileOperationHooks(archivalist, auditLog)
hooks.RegisterHooks(hookRegistry)

// Now file operations automatically:
// 1. Validate permissions
// 2. Sanitize secrets
// 3. Check for conflicts
// 4. Record changes to Archivalist
// 5. Emit change signals
```

**Integration Points**:
- Uses hook system from `ARCHITECTURE.md` LLM Hooks
- Records to Archivalist for history tracking
- Updates SessionContext.ModifiedFiles

**Tests Required**:
- `TestFileOperationHooks_PreWrite`
- `TestFileOperationHooks_PostWrite`
- `TestFileOperationHooks_ConflictDetection`
- `TestFileOperationHooks_Integration`

**References**:
- `ARCHITECTURE.md` LLM Hooks section
- `core/archivalist/archivalist.go`

---

## Phase 10: Testing Infrastructure

### Task 10.1: Create Integration Test Framework

**Objective**: Create comprehensive integration tests for the versioning system.

**File**: `core/versioning/integration_test.go`

**Interface/Signature**:
```go
package versioning_test

import (
    "testing"
    "core/versioning"
    "core/session"
    "core/pipeline"
)

// TestHarness provides test utilities
type TestHarness struct {
    CVS        *versioning.CVS
    VFSManager *versioning.DefaultVFSManager
    TempDir    string
}

func NewTestHarness(t *testing.T) *TestHarness

func (h *TestHarness) CreateTestSession() session.SessionID
func (h *TestHarness) CreateTestPipeline(sessionID session.SessionID) pipeline.PipelineID
func (h *TestHarness) WriteTestFile(path string, content []byte) versioning.VersionID
func (h *TestHarness) Cleanup()

// Integration test scenarios
func TestIntegration_SinglePipelineWorkflow(t *testing.T)
func TestIntegration_ConcurrentPipelines(t *testing.T)
func TestIntegration_VariantWorkflow(t *testing.T)
func TestIntegration_CrossSessionCoordination(t *testing.T)
func TestIntegration_ConflictResolution(t *testing.T)
func TestIntegration_CrashRecovery(t *testing.T)
```

**Acceptance Criteria**:
1. Test harness sets up complete versioning system
2. Single pipeline workflow end-to-end
3. Concurrent pipelines with file conflicts
4. Variant creation and selection
5. Cross-session file changes
6. Conflict resolution flow
7. Crash recovery with WAL

**Tests Required**:
- All scenarios listed above
- At least 90% code coverage for versioning package

**References**:
- `FILESYSTEM.md` integration scenarios
- Go testing best practices

---

### Task 10.2: Create Stress Tests

**Objective**: Create stress tests for performance and concurrency.

**File**: `core/versioning/stress_test.go`

**Interface/Signature**:
```go
package versioning_test

import "testing"

// Stress test configurations
type StressConfig struct {
    NumSessions     int
    NumPipelines    int
    NumFiles        int
    NumOperations   int
    ConcurrentOps   int
}

func TestStress_HighConcurrency(t *testing.T)
func TestStress_LargeFiles(t *testing.T)
func TestStress_ManyVersions(t *testing.T)
func TestStress_RapidConflicts(t *testing.T)

// Benchmarks
func BenchmarkCVS_Write(b *testing.B)
func BenchmarkCVS_Read(b *testing.B)
func BenchmarkOT_Transform(b *testing.B)
func BenchmarkDAG_GetHistory(b *testing.B)
```

**Acceptance Criteria**:
1. System handles 10+ concurrent sessions
2. System handles 100+ concurrent pipelines
3. Large files (10MB+) don't cause OOM
4. 1000+ versions per file doesn't degrade performance
5. Benchmarks establish performance baselines

**Tests Required**:
- All stress tests pass without errors
- Benchmarks documented

**References**:
- `FILESYSTEM.md` performance requirements
- Go benchmark practices

---

## Phase 11: Documentation and Examples

### Task 11.1: Create Usage Documentation

**Objective**: Document how to use the versioning system.

**File**: `docs/versioning.md`

**Content Requirements**:
1. Architecture overview
2. Getting started guide
3. API reference
4. Configuration options
5. Troubleshooting guide
6. Performance tuning

**Acceptance Criteria**:
1. All public APIs documented
2. Code examples for common use cases
3. Configuration reference complete
4. Troubleshooting covers common issues

---

### Task 11.2: Create Example Implementations

**Objective**: Provide example code for common patterns.

**File**: `examples/versioning/`

**Examples Required**:
1. `basic_pipeline.go` - Simple pipeline with VFS
2. `variant_workflow.go` - Variant creation and selection
3. `cross_session.go` - Cross-session coordination
4. `conflict_resolution.go` - Handling merge conflicts
5. `custom_parser.go` - Adding a new language parser

**Acceptance Criteria**:
1. Examples compile and run
2. Comments explain key concepts
3. Demonstrates best practices

---

## Phase 12: Future Work

### Task 12.1: Git Integration Layer

**Objective**: Sync version history with git for persistence and collaboration.

**File**: `core/versioning/git_sync.go`

**Scope**:
- Periodic checkpoints to git
- Branch-per-session support
- Merge with external changes
- Push/pull coordination

**Status**: Future work - not in initial implementation

---

### Task 12.2: Remote Version Store

**Objective**: Distributed CVS for multi-machine collaboration.

**File**: `core/versioning/remote_cvs.go`

**Scope**:
- gRPC API for remote access
- Replication protocol
- Conflict resolution across machines
- Offline support

**Status**: Future work - not in initial implementation

---

### Task 12.3: Advanced Conflict Resolution UI

**Objective**: Visual diff/merge interface.

**Scope**:
- Side-by-side diff view
- Three-way merge visualization
- Syntax-highlighted diffs
- Semantic conflict explanation

**Status**: Future work - not in initial implementation

---

### Task 12.4: Language Server Protocol Integration

**Objective**: Integrate with LSP for semantic awareness.

**Scope**:
- Use LSP for AST information
- Symbol-aware targeting
- Refactoring support
- Cross-file reference tracking

**Status**: Future work - not in initial implementation

---

## Task Dependency Graph

```
Phase 1: Foundation
├── 1.1 VersionID
├── 1.2 VectorClock (depends: session.SessionID)
├── 1.3 Operation (depends: 1.1, 1.2, 1.4)
├── 1.4 Target
└── 1.5 FileVersion (depends: 1.1, 1.2, 1.3)

Phase 2: Storage
├── 2.1 BlobStore (depends: 1.1)
├── 2.2 OperationLog (depends: 1.3)
├── 2.3 DAGStore (depends: 1.5)
└── 2.4 WAL (depends: 2.1, 2.2, 2.3)

Phase 3: VFS
├── 3.1 VFS (depends: 2.1, 2.2, 2.3, security.PermissionManager)
└── 3.2 VFSManager (depends: 3.1, pipeline.VariantGroup)

Phase 4: OT Engine
├── 4.1 Diff (depends: 1.4)
├── 4.2 OT (depends: 1.3, 4.1)
└── 4.3 ConflictUI (depends: 4.2, guide.Guide)

Phase 5: CVS
└── 5.1 CVS (depends: 2.*, 3.*, 4.*)

Phase 6: Pipeline Integration
├── 6.1 PipelineRunner (depends: 5.1, pipeline.Pipeline)
└── 6.2 PipelineScheduler (depends: 6.1, session.PipelineScheduler)

Phase 7: Cross-Session
├── 7.1 SignalDispatcher (depends: 5.1, session.SignalDispatcher)
└── 7.2 CrossSessionPool (depends: 5.1, session.CrossSessionPool)

Phase 8: Parsers
├── 8.1 GoParser
├── 8.2 TypeScriptParser
├── 8.3 PythonParser
└── 8.4 ParserRegistry (depends: 8.1, 8.2, 8.3)

Phase 9: Skills and Hooks
├── 9.1 VersioningSkills (depends: 5.1)
└── 9.2 FileOperationHooks (depends: 5.1, hooks.*)

Phase 10: Testing
├── 10.1 IntegrationTests (depends: all above)
└── 10.2 StressTests (depends: all above)

Phase 11: Documentation
├── 11.1 UsageDocs (depends: all above)
└── 11.2 Examples (depends: all above)

Phase 12: Future
├── 12.1 GitSync
├── 12.2 RemoteCVS
├── 12.3 AdvancedConflictUI
└── 12.4 LSPIntegration
```

---

## Implementation Order Recommendation

1. **Week 1-2**: Phase 1 (Foundation) - All tasks
2. **Week 3-4**: Phase 2 (Storage) - All tasks
3. **Week 5-6**: Phase 3 (VFS) - All tasks
4. **Week 7-8**: Phase 4 (OT Engine) - All tasks
5. **Week 9-10**: Phase 5 (CVS) - All tasks
6. **Week 11**: Phase 6 (Pipeline Integration) - All tasks
7. **Week 12**: Phase 7 (Cross-Session) - All tasks
8. **Week 13**: Phase 8 (Parsers) - Go parser + registry
9. **Week 14**: Phase 9 (Skills/Hooks) - All tasks
10. **Week 15**: Phase 10 (Testing) - All tasks
11. **Week 16**: Phase 11 (Documentation) - All tasks
12. **Future**: Phase 12 tasks as needed

---

## Security Compliance Checklist

For each task involving file operations:

- [ ] Uses `security.PermissionManager` for access control
- [ ] Uses `security.AgentRole` for role-based permissions
- [ ] Uses `security.SecretSanitizer` before logging/storing
- [ ] Validates paths with `VirtualFilesystem.ValidatePath`
- [ ] Respects `FilesystemConfig` boundaries
- [ ] Records operations in audit log
- [ ] Handles secrets in content appropriately

---

## Architecture Compliance Checklist

For each task:

- [ ] Uses existing types (`session.SessionID`, `pipeline.PipelineID`, etc.)
- [ ] Follows existing patterns from ARCHITECTURE.md
- [ ] Integrates with existing hooks system
- [ ] Routes through Guide for user communication
- [ ] Records significant changes to Archivalist
- [ ] Emits appropriate signals for cross-session coordination
- [ ] Thread-safe for concurrent access
- [ ] Includes comprehensive tests
