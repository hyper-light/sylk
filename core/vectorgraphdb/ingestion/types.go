package ingestion

import (
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Core Types
// =============================================================================

// FileInfo holds metadata about a discovered file.
type FileInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
}

type MappedFile struct {
	Path string
	Data []byte
	Lang string
	Size int64
}

// ParsedFile holds the result of parsing a single file.
type ParsedFile struct {
	Path    string
	Lang    string
	Symbols []Symbol
	Imports []Import
	Lines   int
	Errors  []ParseError
}

// Symbol represents an extracted symbol (function, type, etc.).
type Symbol struct {
	Name      string
	Kind      SymbolKind
	StartLine uint32
	EndLine   uint32
	Signature string
}

// SymbolKind represents the type of symbol.
type SymbolKind uint8

const (
	SymbolKindUnknown SymbolKind = iota
	SymbolKindFunction
	SymbolKindMethod
	SymbolKindType
	SymbolKindInterface
	SymbolKindConst
	SymbolKindVar
)

// String returns the string representation of SymbolKind.
func (k SymbolKind) String() string {
	switch k {
	case SymbolKindFunction:
		return "function"
	case SymbolKindMethod:
		return "method"
	case SymbolKindType:
		return "type"
	case SymbolKindInterface:
		return "interface"
	case SymbolKindConst:
		return "const"
	case SymbolKindVar:
		return "var"
	default:
		return "unknown"
	}
}

// Import represents an extracted import statement.
type Import struct {
	Path  string
	Alias string
	Line  uint32
}

// ParseError represents a parsing error.
type ParseError struct {
	Line    uint32
	Column  uint32
	Message string
}

// =============================================================================
// CodeGraph - In-Memory Representation
// =============================================================================

// CodeGraph holds the complete code structure in memory.
// Pre-allocated slices are used for performance.
type CodeGraph struct {
	// Nodes
	Files   []FileNode
	Symbols []SymbolNode

	// Edges
	ImportEdges   []Edge
	ContainsEdges []Edge

	// Indexes (built during aggregation)
	PathIndex   map[string]uint32   // path -> file index
	SymbolIndex map[string][]uint32 // symbol name -> symbol indices

	// Metadata
	RootPath   string
	TotalLines int
	TotalBytes int64
	CreatedAt  time.Time
}

// FileNode represents a file in the code graph.
type FileNode struct {
	ID        uint32
	Path      string
	Lang      string
	LineCount int
	ByteCount int64
}

// SymbolNode represents a symbol in the code graph.
type SymbolNode struct {
	ID        uint32
	FileID    uint32
	Name      string
	Kind      SymbolKind
	StartLine uint32
	EndLine   uint32
	Signature string
}

// Edge represents a relationship between two nodes.
type Edge struct {
	SourceID uint32
	TargetID uint32
	Kind     EdgeKind
}

// EdgeKind represents the type of edge.
type EdgeKind uint8

const (
	EdgeKindImports EdgeKind = iota
	EdgeKindContains
	EdgeKindCalls
	EdgeKindReferences
)

// String returns the string representation of EdgeKind.
func (k EdgeKind) String() string {
	switch k {
	case EdgeKindImports:
		return "imports"
	case EdgeKindContains:
		return "contains"
	case EdgeKindCalls:
		return "calls"
	case EdgeKindReferences:
		return "references"
	default:
		return "unknown"
	}
}

// =============================================================================
// Result Types
// =============================================================================

type IngestionResult struct {
	Graph          *CodeGraph
	TotalFiles     int
	TotalSymbols   int
	TotalLines     int
	TotalBytes     int64
	TotalDuration  time.Duration
	PhaseDurations PhaseDurations
	VectorResult   *VectorResult
	ParseErrors    []FileParseError
}

type PhaseDurations struct {
	Discovery time.Duration
	Mmap      time.Duration
	Parse     time.Duration
	Aggregate time.Duration
	Persist   time.Duration
	Index     time.Duration
	Vector    time.Duration
}

type VectorResult struct {
	SymbolCount    int
	EmbedderSource string
	DiskSizeBytes  int64
}

// FileParseError represents a file that failed to parse.
type FileParseError struct {
	Path  string
	Error string
}

// =============================================================================
// Configuration
// =============================================================================

type Config struct {
	RootPath       string
	IgnorePatterns []string
	SQLitePath     string
	BlevePath      string
	Workers        int
	SkipPersist    bool
	SkipBleve      bool
	VectorConfig   *VectorConfig
}

type VectorConfig struct {
	StorageDir    string
	ForceLocal    bool
	SkipModelLoad bool
	BatchSize     int
}

// WithDefaults applies default values to the config.
func (c *Config) WithDefaults() *Config {
	if c.Workers <= 0 {
		c.Workers = WorkerCount()
	}
	return c
}

// =============================================================================
// Thread-Safe Counters
// =============================================================================

// Counter provides thread-safe counting.
type Counter struct {
	value atomic.Uint64
}

// Inc increments and returns the new value.
func (c *Counter) Inc() uint64 {
	return c.value.Add(1)
}

// Add adds n and returns the new value.
func (c *Counter) Add(n uint64) uint64 {
	return c.value.Add(n)
}

// Load returns the current value.
func (c *Counter) Load() uint64 {
	return c.value.Load()
}

// =============================================================================
// Accumulator for Thread-Safe Collection
// =============================================================================

// Accumulator collects items from multiple goroutines.
type Accumulator[T any] struct {
	items []T
	mu    sync.Mutex
}

// NewAccumulator creates an accumulator with pre-allocated capacity.
func NewAccumulator[T any](capacity int) *Accumulator[T] {
	return &Accumulator[T]{
		items: make([]T, 0, capacity),
	}
}

// Append adds an item thread-safely.
func (a *Accumulator[T]) Append(item T) {
	a.mu.Lock()
	a.items = append(a.items, item)
	a.mu.Unlock()
}

// AppendAll adds multiple items thread-safely.
func (a *Accumulator[T]) AppendAll(items []T) {
	a.mu.Lock()
	a.items = append(a.items, items...)
	a.mu.Unlock()
}

// Items returns all collected items.
func (a *Accumulator[T]) Items() []T {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]T, len(a.items))
	copy(result, a.items)
	return result
}

// Len returns the number of items.
func (a *Accumulator[T]) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.items)
}
