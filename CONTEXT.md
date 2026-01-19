# Lossless Context Virtualization Architecture

## Overview

Sylk implements a **lossless context virtualization** system that eliminates information loss from traditional compaction. Instead of summarizing and discarding context, all content is stored in a Universal Content Store backed by Bleve (full-text search) and VectorGraphDB (SQLite-based semantic search) and can be retrieved on demand.

**Storage Architecture Note**: VectorGraphDB uses SQLite internally (`vector.db`) for embeddings, graph relationships, AND content metadata. Bleve is file-based (`documents.bleve/`) for full-text search. There is no separate content database - all relational data is stored in VectorGraphDB.

**Core Principle**: The context window is treated like a CPU's L1 cache backed by infinite storage. Content can be "evicted" from active context but remains fully retrievable.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    CONTEXT VIRTUALIZATION vs TRADITIONAL COMPACTION                  │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TRADITIONAL COMPACTION (Lossy)          CONTEXT VIRTUALIZATION (Lossless)         │
│  ════════════════════════════           ═══════════════════════════════════        │
│                                                                                     │
│  [Full Context] ──► [Summary] ──► [Summary²]    [Full Context] ──► [References]    │
│        │                  │              │            │                  │          │
│     10,000            2,000            400        10,000            ~200           │
│     tokens            tokens          tokens       tokens           tokens          │
│        │                  │              │            │                  │          │
│        ▼                  ▼              ▼            ▼                  ▼          │
│   Information         ~80% loss      ~96% loss   Information      0% loss          │
│                                                   in DB/VDB       (retrievable)    │
│                                                                                     │
│  After 3 compactions: unrecoverable    After any eviction: fully recoverable       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Design Principles

1. **Universal Indexing** - All prompts, responses, tool results, fetched content, and code analysis stored immediately
2. **Immediate Repo Scan** - On startup (with user permission), parallel scan indexes entire project
3. **No Compaction** - Knowledge agents use eviction + retrieval; pipeline agents use handoff
4. **Lossless Access** - Evicted content replaced with references that enable full retrieval
5. **Smart Eviction** - Per-agent strategies based on role and access patterns

---

## Memory Management: GP-Based Dynamic Triggers

### Agent Classification

Memory management triggers are **not hardcoded percentages**. Each agent instance learns its own degradation curve via Gaussian Process observations. When degradation is detected, the action depends on agent category.

| Agent | Model | Context Window | Category | Strategy |
|-------|-------|----------------|----------|----------|
| **Librarian** | Sonnet 4.5 | **1M tokens** | Knowledge | Tiered Eviction |
| **Archivalist** | Sonnet 4.5 | **1M tokens** | Knowledge | Tiered Eviction |
| **Academic** | Opus 4.5 | 200K tokens | Knowledge | Tiered Eviction |
| **Architect** | Opus 4.5 | 200K tokens | Knowledge | Tiered Eviction |
| **Engineer** | Opus 4.5 | 200K tokens | Pipeline | **Same-Type Handoff** |
| **Designer** | Sonnet 4.5 | 200K tokens | Pipeline | **Same-Type Handoff** |
| **Inspector** (standalone) | Sonnet 4.5 | 200K tokens | Standalone | **Same-Type Handoff** |
| **Inspector** (pipeline) | Haiku 4.5 | 200K tokens | Pipeline | **Same-Type Handoff** |
| **Tester** (standalone) | Sonnet 4.5 | 200K tokens | Standalone | **Same-Type Handoff** |
| **Tester** (pipeline) | Haiku 4.5 | 200K tokens | Pipeline | **Same-Type Handoff** |
| **Orchestrator** | Haiku 4.5 | 200K tokens | Standalone | **Same-Type Handoff** |
| **Guide** | Haiku 4.5 | 200K tokens | Standalone | **Same-Type Handoff** |

### Trigger Mechanism: GP-Based Degradation Detection

**No fixed percentage thresholds.** Each agent instance learns its own performance curve:

1. **Quality Observations**: After each turn, quality is assessed and recorded
2. **GP Prediction**: Gaussian Process predicts quality at current + lookahead context size
3. **Degradation Detection**: Action triggered when:
   - Predicted quality drops below learned peak quality threshold
   - Quality trend shows significant decline
   - High uncertainty + past burn-in phase
4. **Action Type**: Knowledge agents → eviction; Others → same-type handoff

See **HANDOFF.md** for full GP architecture and learned parameter specifications.

### Visual Overview

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    GP-BASED DYNAMIC MEMORY MANAGEMENT                                │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TRIGGER: GP-detected performance degradation (NOT fixed % threshold)               │
│                                                                                     │
│  KNOWLEDGE AGENTS → GRADUAL TIERED EVICTION                                         │
│  ═══════════════════════════════════════════                                        │
│  Librarian (Sonnet-1M), Archivalist (Sonnet-1M), Academic (Opus), Architect (Opus) │
│                                                                                     │
│  • Own goroutine (long-lived, standalone)                                           │
│  • GP detects degradation → tiered eviction (HOT → WARM → COLD → [CTX-REF-xxx])    │
│  • Agent CONTINUES (same instance, reduced context)                                 │
│  • Evicted content retrievable on demand via tiered retrieval system               │
│  • Librarian/Archivalist: 1M context means MUCH longer before eviction needed      │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────    │
│                                                                                     │
│  STANDALONE AGENTS → SAME-TYPE HANDOFF                                              │
│  ════════════════════════════════════════                                           │
│  Guide (Haiku), Orchestrator (Haiku), Inspector-Standalone (Sonnet),               │
│  Tester-Standalone (Sonnet)                                                         │
│                                                                                     │
│  • Own goroutine (long-lived, standalone)                                           │
│  • GP detects degradation → handoff to new instance of SAME (AgentType, Model)     │
│  • PreparedContext trimmed to OptimalPreparedSize (learned for this agent+model)   │
│  • Old instance TERMINATES, new instance continues                                  │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────    │
│                                                                                     │
│  PIPELINE AGENTS → SAME-TYPE HANDOFF (within pipeline)                              │
│  ═════════════════════════════════════════════════════                              │
│  Engineer (Opus), Designer (Sonnet), Inspector-Pipeline (Haiku),                   │
│  Tester-Pipeline (Haiku)                                                            │
│                                                                                     │
│  • Execute sequentially within pipeline goroutine                                   │
│  • GP detects degradation → handoff to new instance of SAME (AgentType, Model)     │
│  • Pipeline continues with swapped agent reference                                  │
│  • PreparedContext trimmed to OptimalPreparedSize (learned for this agent+model)   │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────    │
│                                                                                     │
│  PREPARED CONTEXT: Maintained continuously during normal operation                  │
│  HANDOFF LATENCY: ~1-2ms (pointer swap + trim)                                      │
│  ALL PARAMETERS: Learned distributions with hierarchical Bayesian pooling           │
│                                                                                     │
│  All checkpoints → Archivalist (persistent storage)                                │
│  All content → Universal Content Store (lossless retrieval)                        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Universal Content Store

Everything that enters any agent's context is immediately indexed for later retrieval.

### Content Types

```go
// core/context/content_types.go

type ContentType string

const (
    ContentTypeUserPrompt      ContentType = "user_prompt"
    ContentTypeAgentResponse   ContentType = "agent_response"
    ContentTypeToolCall        ContentType = "tool_call"
    ContentTypeToolResult      ContentType = "tool_result"
    ContentTypeCodeFile        ContentType = "code_file"
    ContentTypeWebFetch        ContentType = "web_fetch"
    ContentTypeResearchPaper   ContentType = "research_paper"
    ContentTypeAgentMessage    ContentType = "agent_message"
    ContentTypePlanWorkflow    ContentType = "plan_workflow"
    ContentTypeTestResult      ContentType = "test_result"
    ContentTypeInspectorFinding ContentType = "inspector_finding"
)
```

### Content Entry

```go
// core/context/content_entry.go

type ContentEntry struct {
    ID            string            `json:"id"`             // SHA-256 of content
    SessionID     string            `json:"session_id"`
    AgentID       string            `json:"agent_id"`
    AgentType     string            `json:"agent_type"`
    ContentType   ContentType       `json:"content_type"`
    Content       string            `json:"content"`        // Full original content
    TokenCount    int               `json:"token_count"`
    Timestamp     time.Time         `json:"timestamp"`
    TurnNumber    int               `json:"turn_number"`

    // For retrieval
    Embedding     []float32         `json:"-"`              // Vector embedding
    Keywords      []string          `json:"keywords"`       // Extracted keywords
    Entities      []string          `json:"entities"`       // Named entities

    // Relationships
    ParentID      string            `json:"parent_id,omitempty"`      // What prompted this
    ChildIDs      []string          `json:"child_ids,omitempty"`      // What this generated
    RelatedFiles  []string          `json:"related_files,omitempty"`  // Files referenced

    // Metadata
    Metadata      map[string]any    `json:"metadata,omitempty"`
}

// Generate deterministic ID from content
func GenerateContentID(content []byte) string {
    hash := sha256.Sum256(content)
    return hex.EncodeToString(hash[:])
}
```

### Universal Content Store

```go
// core/context/content_store.go

type UniversalContentStore struct {
    bleveIndex   bleve.Index           // Full-text search (file-based: documents.bleve/)
    vectorDB     *vectorgraphdb.DB     // Semantic search + content metadata (SQLite-based: vector.db)

    // Real-time indexing
    indexQueue   chan *ContentEntry
    workers      int

    // Resource management
    goroutineBudget  *concurrency.GoroutineBudget
    fileHandleBudget *resources.FileHandleBudget
}

// NOTE: VectorGraphDB uses SQLite internally (vector.db) for ALL storage:
// - Vector embeddings and HNSW index for semantic search
// - Graph edges for relationships
// - Content entry metadata (extends existing node schema)
// There is no separate content database - VectorGraphDB handles all relational queries.

func NewUniversalContentStore(config *ContentStoreConfig) (*UniversalContentStore, error) {
    store := &UniversalContentStore{
        indexQueue: make(chan *ContentEntry, 1000),
        workers:    config.IndexWorkers,
    }

    // Initialize Bleve index
    bleveIndex, err := bleve.Open(config.BlevePath)
    if err == bleve.ErrorIndexPathDoesNotExist {
        mapping := buildContentMapping()
        bleveIndex, err = bleve.New(config.BlevePath, mapping)
    }
    if err != nil {
        return nil, fmt.Errorf("open bleve index: %w", err)
    }
    store.bleveIndex = bleveIndex

    // Initialize VectorDB connection
    store.vectorDB = config.VectorDB

    // VectorDB handles both semantic search and content metadata storage
    // No separate content database needed - VectorGraphDB is SQLite-based
    if err := store.initSchema(); err != nil {
        return nil, fmt.Errorf("init schema: %w", err)
    }

    // Start index workers
    for i := 0; i < store.workers; i++ {
        go store.indexWorker()
    }

    return store, nil
}

// Index content - called on every message through Guide
func (s *UniversalContentStore) IndexContent(entry *ContentEntry) error {
    // Generate ID if not set
    if entry.ID == "" {
        entry.ID = GenerateContentID([]byte(entry.Content))
    }

    // Queue for async embedding generation
    select {
    case s.indexQueue <- entry:
    default:
        // Queue full - index synchronously
        return s.indexSync(entry)
    }

    // Index in Bleve synchronously for consistency
    if err := s.bleveIndex.Index(entry.ID, entry); err != nil {
        return fmt.Errorf("bleve index: %w", err)
    }

    // Store content metadata in VectorGraphDB (extends node schema)
    return s.storeContentEntry(entry)
}

func (s *UniversalContentStore) indexWorker() {
    for entry := range s.indexQueue {
        // Generate embedding
        embedding, err := s.vectorDB.GenerateEmbedding(entry.Content)
        if err != nil {
            log.Warn("Failed to generate embedding", "id", entry.ID, "error", err)
            continue
        }

        entry.Embedding = embedding

        // Store in VectorDB
        if err := s.vectorDB.Store(entry.ID, embedding, entry.Metadata); err != nil {
            log.Warn("Failed to store in VectorDB", "id", entry.ID, "error", err)
        }
    }
}

// Hybrid search combining full-text and semantic
func (s *UniversalContentStore) Search(query string, filters *SearchFilters, limit int) ([]*ContentEntry, error) {
    // Parallel search
    var wg sync.WaitGroup
    var bleveResults, vectorResults []*ContentEntry
    var bleveErr, vectorErr error

    wg.Add(2)
    go func() {
        defer wg.Done()
        bleveResults, bleveErr = s.searchBleve(query, filters, limit*2)
    }()
    go func() {
        defer wg.Done()
        vectorResults, vectorErr = s.searchVector(query, filters, limit*2)
    }()
    wg.Wait()

    if bleveErr != nil && vectorErr != nil {
        return nil, fmt.Errorf("both searches failed: bleve=%v, vector=%v", bleveErr, vectorErr)
    }

    // RRF fusion
    return s.fuseResults(bleveResults, vectorResults, limit), nil
}

// Get specific entries by ID
func (s *UniversalContentStore) GetByIDs(ids []string) ([]*ContentEntry, error) {
    entries := make([]*ContentEntry, 0, len(ids))

    for _, id := range ids {
        entry, err := s.getContentEntry(id)
        if err != nil {
            return nil, fmt.Errorf("get entry %s: %w", id, err)
        }
        if entry != nil {
            entries = append(entries, entry)
        }
    }

    return entries, nil
}

// Get entries by session and turn range
func (s *UniversalContentStore) GetByTurnRange(sessionID string, fromTurn, toTurn int) ([]*ContentEntry, error) {
    query := `
        SELECT id, session_id, agent_id, agent_type, content_type, content,
               token_count, timestamp, turn_number, keywords, entities,
               parent_id, related_files, metadata
        FROM content_entries
        WHERE session_id = ? AND turn_number >= ? AND turn_number <= ?
        ORDER BY turn_number ASC
    `
    return s.queryEntries(query, sessionID, fromTurn, toTurn)
}
```

### SQLite Schema

```sql
-- core/context/schema.sql

CREATE TABLE IF NOT EXISTS content_entries (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    content_type TEXT NOT NULL,
    content TEXT NOT NULL,
    token_count INTEGER NOT NULL,
    timestamp INTEGER NOT NULL,
    turn_number INTEGER NOT NULL,
    keywords TEXT,           -- JSON array
    entities TEXT,           -- JSON array
    parent_id TEXT,
    child_ids TEXT,          -- JSON array
    related_files TEXT,      -- JSON array
    metadata TEXT,           -- JSON object
    created_at INTEGER DEFAULT (unixepoch())
);

CREATE INDEX idx_content_session ON content_entries(session_id);
CREATE INDEX idx_content_agent ON content_entries(agent_id);
CREATE INDEX idx_content_type ON content_entries(content_type);
CREATE INDEX idx_content_turn ON content_entries(session_id, turn_number);
CREATE INDEX idx_content_timestamp ON content_entries(timestamp);
CREATE INDEX idx_content_parent ON content_entries(parent_id);

-- Context references (evicted content pointers)
CREATE TABLE IF NOT EXISTS context_references (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    content_ids TEXT NOT NULL,    -- JSON array of evicted content IDs
    summary TEXT NOT NULL,
    tokens_saved INTEGER NOT NULL,
    turn_range_start INTEGER NOT NULL,
    turn_range_end INTEGER NOT NULL,
    topics TEXT,                  -- JSON array
    entities TEXT,                -- JSON array
    query_hints TEXT,             -- JSON array
    created_at INTEGER DEFAULT (unixepoch())
);

CREATE INDEX idx_ref_session ON context_references(session_id);
CREATE INDEX idx_ref_agent ON context_references(agent_id);
```

---

## Startup Indexer

On terminal app start, with user permission, immediately scan and index the entire project.

### Startup Flow

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                              STARTUP INDEXING FLOW                                   │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  1. Terminal Starts                                                                │
│     │                                                                              │
│     ▼                                                                              │
│  2. Check for existing index                                                       │
│     │                                                                              │
│     ├──[Index exists + fresh]──► Skip scan, ready immediately                      │
│     │                                                                              │
│     ├──[Index exists + stale]──► Incremental update (changed files only)           │
│     │                                                                              │
│     └──[No index]──► Prompt user for permission                                    │
│                       │                                                            │
│                       ▼                                                            │
│  3. User grants permission                                                         │
│     │                                                                              │
│     ▼                                                                              │
│  4. Parallel scan begins                                                           │
│     ┌─────────────────────────────────────────────────────────────────┐           │
│     │  [Scanner] ──► [Parser] ──► [Indexer] ──► [VectorGraphDB]      │           │
│     │      │             │            │              │                │           │
│     │      ▼             ▼            ▼              ▼                │           │
│     │   Files        Symbols      Bleve         Embeddings           │           │
│     │   found        extracted   indexed       (SQLite-based)        │           │
│     └─────────────────────────────────────────────────────────────────┘           │
│     │                                                                              │
│     ▼                                                                              │
│  5. Progress displayed in UI                                                       │
│     [████████████████░░░░] 80% - Indexed 1,234 / 1,542 files                      │
│     │                                                                              │
│     ▼                                                                              │
│  6. Index complete - agents have instant codebase knowledge                        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Startup Indexer Implementation

```go
// core/context/startup_indexer.go

type StartupIndexer struct {
    contentStore     *UniversalContentStore
    goroutineBudget  *concurrency.GoroutineBudget
    fileHandleBudget *resources.FileHandleBudget
    progressCallback func(phase string, indexed, total int)
}

type StartupIndexConfig struct {
    MaxConcurrency   int           // Default: NumCPU * 2
    BatchSize        int           // Default: 100
    PriorityPaths    []string      // Index these first (e.g., src/, lib/, cmd/)
    ExcludePaths     []string      // Skip these (.git, node_modules, vendor, etc.)
    ExcludePatterns  []string      // Glob patterns to skip (*.min.js, etc.)
    MaxFileSize      int64         // Skip files larger than this (default: 1MB)
}

var DefaultStartupConfig = &StartupIndexConfig{
    MaxConcurrency: runtime.NumCPU() * 2,
    BatchSize:      100,
    PriorityPaths: []string{
        "cmd/", "src/", "lib/", "pkg/", "internal/", "core/",
        "app/", "components/", "services/", "handlers/",
    },
    ExcludePaths: []string{
        ".git", "node_modules", "vendor", "__pycache__", ".next",
        "dist", "build", "target", ".cache", "coverage",
    },
    ExcludePatterns: []string{
        "*.min.js", "*.min.css", "*.map", "*.lock", "*.sum",
        "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
    },
    MaxFileSize: 1 << 20, // 1MB
}

func (s *StartupIndexer) IndexProject(ctx context.Context, rootPath string, config *StartupIndexConfig) error {
    if config == nil {
        config = DefaultStartupConfig
    }

    // Phase 1: Quick scan to count files and build priority queue
    s.progressCallback("scanning", 0, 0)
    files, err := s.scanAndPrioritize(rootPath, config)
    if err != nil {
        return fmt.Errorf("scan: %w", err)
    }

    total := len(files)
    s.progressCallback("indexing", 0, total)

    // Phase 2: Parallel indexing with worker pool
    sem := make(chan struct{}, config.MaxConcurrency)
    var wg sync.WaitGroup
    indexed := atomic.Int32{}
    errors := make(chan error, total)

    for _, file := range files {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case sem <- struct{}{}:
        }

        wg.Add(1)
        go func(f *FileInfo) {
            defer wg.Done()
            defer func() { <-sem }()

            if err := s.indexFile(ctx, f); err != nil {
                select {
                case errors <- fmt.Errorf("%s: %w", f.Path, err):
                default:
                }
            }

            current := indexed.Add(1)
            s.progressCallback("indexing", int(current), total)
        }(file)
    }

    wg.Wait()
    close(errors)

    // Collect errors (don't fail entire scan for individual file failures)
    var errs []error
    for err := range errors {
        errs = append(errs, err)
    }

    if len(errs) > 0 {
        log.Warn("Some files failed to index", "count", len(errs), "total", total)
    }

    s.progressCallback("complete", total, total)
    return nil
}

// Priority ordering: recently modified > core paths > alphabetical
func (s *StartupIndexer) scanAndPrioritize(root string, config *StartupIndexConfig) ([]*FileInfo, error) {
    var files []*FileInfo

    err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return nil // Skip inaccessible
        }

        relPath, _ := filepath.Rel(root, path)

        // Skip excluded directories entirely
        if d.IsDir() {
            for _, exclude := range config.ExcludePaths {
                if relPath == exclude || strings.HasPrefix(relPath, exclude+string(filepath.Separator)) {
                    return filepath.SkipDir
                }
            }
            return nil
        }

        // Skip by pattern
        for _, pattern := range config.ExcludePatterns {
            if matched, _ := filepath.Match(pattern, d.Name()); matched {
                return nil
            }
        }

        info, err := d.Info()
        if err != nil {
            return nil
        }

        // Skip large files
        if info.Size() > config.MaxFileSize {
            return nil
        }

        files = append(files, &FileInfo{
            Path:     path,
            RelPath:  relPath,
            Size:     info.Size(),
            ModTime:  info.ModTime(),
            Priority: s.calculatePriority(relPath, info, config.PriorityPaths),
        })

        return nil
    })

    if err != nil {
        return nil, err
    }

    // Sort by priority (higher first)
    sort.Slice(files, func(i, j int) bool {
        return files[i].Priority > files[j].Priority
    })

    return files, nil
}

func (s *StartupIndexer) calculatePriority(relPath string, info fs.FileInfo, priorityPaths []string) int {
    priority := 0

    // Boost recently modified files
    age := time.Since(info.ModTime())
    if age < 24*time.Hour {
        priority += 100
    } else if age < 7*24*time.Hour {
        priority += 50
    } else if age < 30*24*time.Hour {
        priority += 25
    }

    // Boost priority paths
    for i, pp := range priorityPaths {
        if strings.HasPrefix(relPath, pp) {
            priority += 50 - i // Earlier in list = higher priority
            break
        }
    }

    // Boost certain file types
    ext := strings.ToLower(filepath.Ext(relPath))
    switch ext {
    case ".go", ".ts", ".tsx", ".py", ".rs":
        priority += 30
    case ".js", ".jsx", ".java", ".cpp", ".c":
        priority += 20
    case ".md", ".yaml", ".yml", ".json", ".toml":
        priority += 10
    }

    return priority
}

func (s *StartupIndexer) indexFile(ctx context.Context, file *FileInfo) error {
    // Acquire file handle budget
    release, err := s.fileHandleBudget.Acquire(ctx)
    if err != nil {
        return err
    }
    defer release()

    // Read file content
    content, err := os.ReadFile(file.Path)
    if err != nil {
        return err
    }

    // Detect language
    lang := detectLanguage(file.Path)

    // Parse for symbols if applicable
    var symbols []string
    var comments string
    if parser := getParser(lang); parser != nil {
        result, _ := parser.Parse(content, file.Path)
        if result != nil {
            for _, sym := range result.Symbols {
                symbols = append(symbols, sym.Name)
            }
            comments = result.Comments
        }
    }

    // Create content entry
    entry := &ContentEntry{
        ID:          GenerateContentID(content),
        ContentType: ContentTypeCodeFile,
        Content:     string(content),
        TokenCount:  estimateTokens(content),
        Timestamp:   file.ModTime,
        Keywords:    symbols,
        RelatedFiles: []string{file.RelPath},
        Metadata: map[string]any{
            "path":     file.RelPath,
            "language": lang,
            "size":     file.Size,
            "comments": comments,
        },
    }

    return s.contentStore.IndexContent(entry)
}

type FileInfo struct {
    Path     string
    RelPath  string
    Size     int64
    ModTime  time.Time
    Priority int
}
```

---

## Context Reference System

When content is evicted from active context, it's replaced with a compact reference marker.

### Reference Types

```go
// core/context/reference.go

type ReferenceType string

const (
    RefTypeConversation  ReferenceType = "conversation"   // User/agent exchanges
    RefTypeResearch      ReferenceType = "research"       // Academic findings
    RefTypeCodeAnalysis  ReferenceType = "code_analysis"  // Librarian discoveries
    RefTypeToolResults   ReferenceType = "tool_results"   // Tool call results
    RefTypePlanDiscussion ReferenceType = "plan_discussion" // Architect planning
)

type ContextReference struct {
    ID          string            `json:"id"`
    Type        ReferenceType     `json:"type"`
    ContentIDs  []string          `json:"content_ids"`   // What was evicted
    Summary     string            `json:"summary"`       // One-line description
    TokensSaved int               `json:"tokens_saved"`  // How much we saved
    TurnRange   [2]int            `json:"turn_range"`    // Turns [from, to]
    Timestamp   time.Time         `json:"timestamp"`

    // For smart retrieval
    Topics      []string          `json:"topics"`        // Main topics covered
    Entities    []string          `json:"entities"`      // Key entities mentioned
    QueryHints  []string          `json:"query_hints"`   // Good retrieval queries
}

// Render as compact marker in context
func (r *ContextReference) Render() string {
    topicsStr := strings.Join(r.Topics[:min(3, len(r.Topics))], ", ")
    return fmt.Sprintf(
        "[CTX-REF:%s | %d turns (%d tokens) @ %s | Topics: %s | retrieve_context(ref_id=\"%s\")]",
        r.Type,
        r.TurnRange[1]-r.TurnRange[0]+1,
        r.TokensSaved,
        r.Timestamp.Format("15:04"),
        topicsStr,
        r.ID,
    )
}

// Example rendered output:
// [CTX-REF:conversation | 15 turns (4,200 tokens) @ 14:23 | Topics: auth flow, JWT, middleware | retrieve_context(ref_id="abc123")]
```

### Reference Generation

```go
// core/context/reference_generator.go

type ReferenceGenerator struct {
    contentStore *UniversalContentStore
}

func (g *ReferenceGenerator) GenerateReference(entries []*ContentEntry) (*ContextReference, error) {
    if len(entries) == 0 {
        return nil, fmt.Errorf("no entries to reference")
    }

    // Collect content IDs
    contentIDs := make([]string, len(entries))
    for i, e := range entries {
        contentIDs[i] = e.ID
    }

    // Calculate tokens saved
    var tokensSaved int
    for _, e := range entries {
        tokensSaved += e.TokenCount
    }

    // Determine turn range
    minTurn, maxTurn := entries[0].TurnNumber, entries[0].TurnNumber
    for _, e := range entries {
        if e.TurnNumber < minTurn {
            minTurn = e.TurnNumber
        }
        if e.TurnNumber > maxTurn {
            maxTurn = e.TurnNumber
        }
    }

    // Extract topics via simple keyword clustering
    topics := g.extractTopics(entries)

    // Extract entities
    entities := g.extractEntities(entries)

    // Generate query hints
    queryHints := g.generateQueryHints(entries, topics, entities)

    // Determine reference type
    refType := g.determineType(entries)

    // Generate one-line summary
    summary := g.generateSummary(entries, topics)

    return &ContextReference{
        ID:          uuid.New().String(),
        Type:        refType,
        ContentIDs:  contentIDs,
        Summary:     summary,
        TokensSaved: tokensSaved,
        TurnRange:   [2]int{minTurn, maxTurn},
        Timestamp:   entries[0].Timestamp,
        Topics:      topics,
        Entities:    entities,
        QueryHints:  queryHints,
    }, nil
}

func (g *ReferenceGenerator) extractTopics(entries []*ContentEntry) []string {
    // Aggregate keywords from all entries
    keywordCounts := make(map[string]int)
    for _, e := range entries {
        for _, kw := range e.Keywords {
            keywordCounts[strings.ToLower(kw)]++
        }
    }

    // Sort by frequency
    type kwCount struct {
        keyword string
        count   int
    }
    var sorted []kwCount
    for kw, count := range keywordCounts {
        sorted = append(sorted, kwCount{kw, count})
    }
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i].count > sorted[j].count
    })

    // Take top N
    topics := make([]string, 0, 5)
    for i := 0; i < len(sorted) && i < 5; i++ {
        topics = append(topics, sorted[i].keyword)
    }

    return topics
}

func (g *ReferenceGenerator) determineType(entries []*ContentEntry) ReferenceType {
    typeCounts := make(map[ContentType]int)
    for _, e := range entries {
        typeCounts[e.ContentType]++
    }

    // Find dominant type
    var maxType ContentType
    var maxCount int
    for t, c := range typeCounts {
        if c > maxCount {
            maxType = t
            maxCount = c
        }
    }

    switch maxType {
    case ContentTypeResearchPaper, ContentTypeWebFetch:
        return RefTypeResearch
    case ContentTypeCodeFile:
        return RefTypeCodeAnalysis
    case ContentTypeToolCall, ContentTypeToolResult:
        return RefTypeToolResults
    case ContentTypePlanWorkflow:
        return RefTypePlanDiscussion
    default:
        return RefTypeConversation
    }
}

func (g *ReferenceGenerator) generateSummary(entries []*ContentEntry, topics []string) string {
    // Simple summary: "Discussion about X, Y, Z"
    if len(topics) == 0 {
        return fmt.Sprintf("%d turns of conversation", len(entries))
    }
    return fmt.Sprintf("Discussion about %s", strings.Join(topics[:min(3, len(topics))], ", "))
}

func (g *ReferenceGenerator) generateQueryHints(entries []*ContentEntry, topics, entities []string) []string {
    hints := make([]string, 0, 5)

    // Add topic-based hints
    for _, t := range topics[:min(2, len(topics))] {
        hints = append(hints, fmt.Sprintf("discussion about %s", t))
    }

    // Add entity-based hints
    for _, e := range entities[:min(2, len(entities))] {
        hints = append(hints, fmt.Sprintf("mentions of %s", e))
    }

    return hints
}
```

---

## Eviction Strategies

Different agents have different access patterns and need different eviction strategies.

### Eviction Interface

**Note**: Eviction triggers are **not hardcoded percentages**. Each agent learns its own degradation curve via Gaussian Process observations (see HANDOFF.md). The `EvictionStrategy` interface below operates on learned parameters from `AgentHandoffProfile`.

```go
// core/context/eviction.go

type EvictionStrategy interface {
    // Select entries for eviction based on GP-detected degradation
    // targetTokens is learned per-agent from AgentHandoffProfile
    SelectForEviction(ctx *AgentContext, targetTokens int) ([]*ContentEntry, error)
}

// AgentEvictionConfig uses learned parameters from AgentHandoffProfile
// NO hardcoded thresholds - all values are distributions
type AgentEvictionConfig struct {
    AgentType     AgentType
    Strategy      EvictionStrategy
    PreserveTypes []ContentType  // Never evict these types
    // All other parameters come from AgentHandoffProfile:
    // - PreserveRecent → profile.GetEffectiveRecentTurnsNeeded(explore)
    // - Trigger → GP-detected degradation via profile.QualityGP
    // - EvictionAmount → calculated to return to learned OptimalPreparedSize
}

// GetEvictionConfig returns config with learned parameters for knowledge agents
// Only Librarian, Archivalist, Academic, and Architect use eviction
// All other agents use same-type handoff (see HandoffController)
func GetEvictionConfig(profile *AgentHandoffProfile) *AgentEvictionConfig {
    if !profile.IsKnowledgeAgent() {
        return nil // Non-knowledge agents use handoff, not eviction
    }

    switch profile.AgentType {
    case AgentTypeLibrarian, AgentTypeArchivalist:
        return &AgentEvictionConfig{
            AgentType:     profile.AgentType,
            Strategy:      &RecencyBasedEviction{profile: profile},
            PreserveTypes: []ContentType{ContentTypeUserPrompt},
        }
    case AgentTypeAcademic:
        return &AgentEvictionConfig{
            AgentType:     profile.AgentType,
            Strategy:      &TopicClusterEviction{profile: profile},
            PreserveTypes: []ContentType{ContentTypeResearchPaper},
        }
    case AgentTypeArchitect:
        return &AgentEvictionConfig{
            AgentType:     profile.AgentType,
            Strategy:      &TaskCompletionEviction{profile: profile},
            PreserveTypes: []ContentType{ContentTypePlanWorkflow},
        }
    default:
        return nil
    }
}

// NOTE: Guide, Orchestrator, Engineer, Designer, Inspector, and Tester
// use Same-Type Handoff instead of eviction - see HandoffController in HANDOFF.md
```

### Recency-Based Eviction

Simple strategy: evict oldest content first. Used by Librarian and Archivalist (1M context knowledge agents).

```go
// core/context/eviction_recency.go

type RecencyBasedEviction struct {
    profile *AgentHandoffProfile  // Provides learned parameters
}

func (e *RecencyBasedEviction) SelectForEviction(ctx *AgentContext, targetTokens int) ([]*ContentEntry, error) {
    entries := ctx.GetEntriesByAge() // Oldest first, excluding preserved

    // Get learned preserve-recent count via hierarchical blending
    // explore=false for exploitation (use mean)
    preserveRecent := e.profile.GetEffectiveRecentTurnsNeeded(false)

    var selected []*ContentEntry
    var tokensSelected int

    for _, entry := range entries {
        if tokensSelected >= targetTokens {
            break
        }

        // Skip preserved types (configured per-agent)
        if ctx.IsPreservedType(entry.ContentType) {
            continue
        }

        // Skip recent turns (learned per agent instance)
        if entry.TurnNumber > ctx.CurrentTurn()-preserveRecent {
            continue
        }

        selected = append(selected, entry)
        tokensSelected += entry.TokenCount
    }

    return selected, nil
}
```

### Topic Cluster Eviction

For Academic: evict complete research topics together. Scoring weights are learned distributions.

```go
// core/context/eviction_topic.go

type TopicClusterEviction struct {
    embedder EmbeddingGenerator
    profile  *AgentHandoffProfile  // Provides learned parameters
}

type TopicCluster struct {
    Entries     []*ContentEntry
    TotalTokens int
    Centroid    []float32
    IsComplete  bool    // Research on this topic concluded?
    AvgAge      float64 // Average age of entries
    Coherence   float64 // How related are entries?
}

func (e *TopicClusterEviction) SelectForEviction(ctx *AgentContext, targetTokens int) ([]*ContentEntry, error) {
    entries := ctx.GetEntries()

    // Get learned preserve-recent count
    preserveRecent := e.profile.GetEffectiveRecentTurnsNeeded(false)

    // Cluster entries by topic using embeddings
    clusters := e.clusterByTopic(entries, preserveRecent)

    // Score clusters for eviction using learned weights
    for _, cluster := range clusters {
        cluster.IsComplete = e.isTopicComplete(cluster)
        cluster.AvgAge = e.calculateAvgAge(cluster.Entries)
        cluster.Coherence = e.calculateCoherence(cluster)
    }

    // Sort by eviction priority: complete + old + coherent first
    sort.Slice(clusters, func(i, j int) bool {
        scoreI := e.evictionScore(clusters[i])
        scoreJ := e.evictionScore(clusters[j])
        return scoreI > scoreJ
    })

    // Select clusters until we hit target
    var selected []*ContentEntry
    var tokensSelected int

    for _, cluster := range clusters {
        if tokensSelected >= targetTokens {
            break
        }

        // Evict entire cluster together (maintains coherence of reference)
        selected = append(selected, cluster.Entries...)
        tokensSelected += cluster.TotalTokens
    }

    return selected, nil
}

func (e *TopicClusterEviction) evictionScore(c *TopicCluster) float64 {
    // All scoring weights could be learned distributions
    // For now, use reasonable defaults that could be promoted to LearnedWeight
    completionWeight := 50.0
    ageWeight := 10.0        // points per day
    coherenceWeight := 20.0

    score := 0.0

    // Completed topics are safe to evict
    if c.IsComplete {
        score += completionWeight
    }

    // Older clusters preferred
    score += c.AvgAge / (24 * float64(time.Hour)) * ageWeight

    // Coherent clusters make better references
    score += c.Coherence * coherenceWeight

    return score
}

func (e *TopicClusterEviction) isTopicComplete(c *TopicCluster) bool {
    // Check if last entry in cluster looks like a conclusion
    if len(c.Entries) == 0 {
        return false
    }

    last := c.Entries[len(c.Entries)-1]

    // Heuristics for completion
    conclusionKeywords := []string{
        "in conclusion", "to summarize", "therefore", "thus",
        "recommendation is", "final answer", "in summary",
    }

    content := strings.ToLower(last.Content)
    for _, kw := range conclusionKeywords {
        if strings.Contains(content, kw) {
            return true
        }
    }

    return false
}
```

### Task Completion Eviction

For Architect: evict completed task discussions. Uses learned parameters from profile.

```go
// core/context/eviction_task.go

type TaskCompletionEviction struct {
    profile *AgentHandoffProfile  // Provides learned parameters
}

func (e *TaskCompletionEviction) SelectForEviction(ctx *AgentContext, targetTokens int) ([]*ContentEntry, error) {
    entries := ctx.GetEntries()

    // Group entries by task (using metadata or heuristics)
    taskGroups := e.groupByTask(entries)

    // Identify completed tasks
    var completedTasks []*TaskGroup
    for _, tg := range taskGroups {
        if e.isTaskComplete(tg) {
            completedTasks = append(completedTasks, tg)
        }
    }

    // Sort by age (oldest completed first)
    sort.Slice(completedTasks, func(i, j int) bool {
        return completedTasks[i].LastActivity.Before(completedTasks[j].LastActivity)
    })

    // Select until target reached
    var selected []*ContentEntry
    var tokensSelected int

    for _, task := range completedTasks {
        if tokensSelected >= targetTokens {
            break
        }
        selected = append(selected, task.Entries...)
        tokensSelected += task.TotalTokens
    }

    // If not enough from completed tasks, fall back to recency with learned params
    if tokensSelected < targetTokens {
        recency := &RecencyBasedEviction{profile: e.profile}
        remaining := targetTokens - tokensSelected
        additional, _ := recency.SelectForEviction(ctx, remaining)
        selected = append(selected, additional...)
    }

    return selected, nil
}

type TaskGroup struct {
    TaskID       string
    Entries      []*ContentEntry
    TotalTokens  int
    LastActivity time.Time
    Status       string // "planning", "in_progress", "completed", "abandoned"
}

func (e *TaskCompletionEviction) isTaskComplete(tg *TaskGroup) bool {
    // Check for completion signals in entries
    for _, entry := range tg.Entries {
        content := strings.ToLower(entry.Content)
        if strings.Contains(content, "task complete") ||
            strings.Contains(content, "workflow complete") ||
            strings.Contains(content, "plan approved") {
            return true
        }
    }
    return tg.Status == "completed"
}
```

---

## Context Retrieval

Agents can retrieve evicted content on demand using retrieval skills.

### Retrieval Implementation

```go
// core/context/retrieval.go

type ContextRetriever struct {
    contentStore *UniversalContentStore
    maxRetrieve  int // Max entries to retrieve at once (default: 50)
    maxTokens    int // Max tokens per retrieval (default: 10000)
}

type RetrievalRequest struct {
    Query       string            `json:"query"`            // Natural language query
    RefID       string            `json:"ref_id,omitempty"` // Specific reference to expand
    ContentIDs  []string          `json:"content_ids,omitempty"` // Specific IDs
    Filters     *RetrievalFilters `json:"filters,omitempty"`
    MaxTokens   int               `json:"max_tokens"`       // Budget for retrieval
}

type RetrievalFilters struct {
    ContentTypes []ContentType `json:"content_types,omitempty"`
    TurnRange    [2]int        `json:"turn_range,omitempty"`
    TimeRange    [2]time.Time  `json:"time_range,omitempty"`
    AgentTypes   []string      `json:"agent_types,omitempty"`
    SessionID    string        `json:"session_id,omitempty"`
}

type RetrievalResult struct {
    Entries     []*ContentEntry `json:"entries"`
    TotalTokens int             `json:"total_tokens"`
    Truncated   bool            `json:"truncated"`
    Query       string          `json:"query"`
    Source      string          `json:"source"` // "reference", "search", "direct"
}

func NewContextRetriever(store *UniversalContentStore) *ContextRetriever {
    return &ContextRetriever{
        contentStore: store,
        maxRetrieve:  50,
        maxTokens:    10000,
    }
}

func (r *ContextRetriever) Retrieve(ctx context.Context, req *RetrievalRequest) (*RetrievalResult, error) {
    if req.MaxTokens == 0 {
        req.MaxTokens = r.maxTokens
    }

    var entries []*ContentEntry
    var source string
    var err error

    switch {
    case req.RefID != "":
        // Expand a specific reference
        entries, err = r.expandReference(req.RefID)
        source = "reference"

    case len(req.ContentIDs) > 0:
        // Get specific content by ID
        entries, err = r.contentStore.GetByIDs(req.ContentIDs)
        source = "direct"

    default:
        // Hybrid search
        entries, err = r.contentStore.Search(req.Query, req.Filters, r.maxRetrieve)
        source = "search"
    }

    if err != nil {
        return nil, err
    }

    // Respect token budget
    result := &RetrievalResult{
        Query:  req.Query,
        Source: source,
    }

    for _, entry := range entries {
        if result.TotalTokens+entry.TokenCount > req.MaxTokens {
            result.Truncated = true
            break
        }
        result.Entries = append(result.Entries, entry)
        result.TotalTokens += entry.TokenCount
    }

    return result, nil
}

func (r *ContextRetriever) expandReference(refID string) ([]*ContentEntry, error) {
    // Get reference from store
    ref, err := r.contentStore.GetReference(refID)
    if err != nil {
        return nil, fmt.Errorf("get reference: %w", err)
    }

    // Get all content entries
    return r.contentStore.GetByIDs(ref.ContentIDs)
}
```

### Context Skills

```go
// skills/context_skills.go

var ContextSkills = []skills.SkillDefinition{
    {
        Name:        "retrieve_context",
        Description: "Retrieve evicted context by query, reference ID, or content ID. Use when you see a [CTX-REF:...] marker and need the full content.",
        Agents:      []string{"librarian", "academic", "architect", "guide", "archivalist"},
        Params: []skills.Param{
            {Name: "query", Type: "string", Description: "Natural language query for what to retrieve", Optional: true},
            {Name: "ref_id", Type: "string", Description: "Specific reference ID to expand (from CTX-REF marker)", Optional: true},
            {Name: "max_tokens", Type: "int", Description: "Maximum tokens to retrieve", Default: 2000},
        },
        Handler: handleRetrieveContext,
    },
    {
        Name:        "search_history",
        Description: "Search all historical context across current and past sessions",
        Agents:      []string{"librarian", "academic", "archivalist"},
        Params: []skills.Param{
            {Name: "query", Type: "string", Description: "Search query", Required: true},
            {Name: "content_type", Type: "string", Description: "Filter by type (user_prompt, agent_response, code_file, etc.)", Optional: true},
            {Name: "session_id", Type: "string", Description: "Limit to specific session", Optional: true},
            {Name: "max_results", Type: "int", Description: "Maximum results to return", Default: 10},
        },
        Handler: handleSearchHistory,
    },
    {
        Name:        "get_turn_range",
        Description: "Get full content for a specific range of conversation turns",
        Agents:      []string{"librarian", "academic", "architect", "guide", "archivalist"},
        Params: []skills.Param{
            {Name: "from_turn", Type: "int", Description: "Starting turn number", Required: true},
            {Name: "to_turn", Type: "int", Description: "Ending turn number", Required: true},
            {Name: "max_tokens", Type: "int", Description: "Maximum tokens to retrieve", Default: 5000},
        },
        Handler: handleGetTurnRange,
    },
}

func handleRetrieveContext(ctx context.Context, params map[string]any) (any, error) {
    retriever := ctx.Value(contextRetrieverKey).(*ContextRetriever)

    req := &RetrievalRequest{
        MaxTokens: getInt(params, "max_tokens", 2000),
    }

    if refID, ok := params["ref_id"].(string); ok && refID != "" {
        req.RefID = refID
    } else if query, ok := params["query"].(string); ok && query != "" {
        req.Query = query
    } else {
        return nil, fmt.Errorf("either query or ref_id required")
    }

    return retriever.Retrieve(ctx, req)
}
```

---

## Hybrid Retrieval Architecture

The context system uses a **hybrid retrieval approach** combining automatic pre-fetch with on-demand tool calls. This maximizes token efficiency while ensuring accuracy.

### Core Principle

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    HYBRID RETRIEVAL: PRE-FETCH + ON-DEMAND                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  LAYER 1: AUTOMATIC PRE-FETCH (before LLM sees query)                              │
│  ═══════════════════════════════════════════════════                               │
│  • System searches Bleve + VectorDB on query arrival                               │
│  • High-confidence results injected WITH query                                      │
│  • LLM sees relevant context immediately                                           │
│  • Zero LLM effort - happens transparently                                         │
│                                                                                     │
│  LAYER 2: ON-DEMAND TOOLS (LLM-initiated)                                          │
│  ═════════════════════════════════════════                                         │
│  • LLM can search for specific additional content                                  │
│  • Handles edge cases pre-fetch missed                                             │
│  • Retrieved content promoted to HOT tier                                          │
│                                                                                     │
│  RESULT: Most needs handled automatically, LLM searches only when necessary        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Pre-fetch Constraints

Pre-fetch is **surgical, not exhaustive**. Strict constraints prevent context bloat:

```go
// core/context/prefetch.go

// NOTE: All thresholds and weights in this system are ADAPTIVE, not hardcoded.
// The values shown are Bayesian priors - starting points that converge based on
// observed outcomes. See "Self-Tuning Adaptive System" section below.

type PrefetchConfig struct {
    // Budget constraints (adaptive via AdaptiveThresholds)
    MaxBudgetPercent   float64  // Max % of remaining context (prior: 10%)
    ConfidenceThreshold float64 // Min relevance score to inject (prior: 0.85)
    ExcerptThreshold   float64  // Min score for full excerpt (prior: 0.90)
    MaxExcerpts        int      // Max full excerpts (prior: 2)
    MaxSummaries       int      // Max summary hints (prior: 3)
    MaxExcerptLines    int      // Lines per excerpt (prior: 50)

    // Quality controls
    RequireMultiSignal bool     // Must match BOTH Bleve AND Vector (prior: true)
    RecencyBoost       float64  // Multiplier for recent content (prior: 0.1)
    SessionPreference  float64  // Boost for current session (prior: 0.15)

    // Adaptive system reference
    Adaptive           *AdaptiveRewardSystem  // Learns optimal thresholds + weights
}

// InitialPriors - starting beliefs, NOT fixed defaults
// These converge to user-optimal values via Bayesian updates
var InitialPriors = &PrefetchConfig{
    MaxBudgetPercent:    0.10,   // Prior: 10% of remaining context
    ConfidenceThreshold: 0.85,   // Prior: converges based on hit rate
    ExcerptThreshold:    0.90,   // Prior: converges based on usage
    MaxExcerpts:         2,
    MaxSummaries:        3,
    MaxExcerptLines:     50,
    RequireMultiSignal:  true,
    RecencyBoost:        0.1,
    SessionPreference:   0.15,
}
```

### Tiered Injection

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         TIERED PRE-FETCH INJECTION                                  │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TIER A: EXCERPT (confidence ≥ 0.90) - 60% of budget                               │
│  ═══════════════════════════════════════════════════                               │
│  • Actual code/content excerpt (20-50 lines max)                                   │
│  • Location: file:line-range                                                       │
│  • Max 2 excerpts                                                                  │
│                                                                                     │
│  Format:                                                                           │
│  [AUTO-RETRIEVED: jwt.go:45-67 | confidence: 0.92]                                 │
│  ```go                                                                             │
│  func ValidateToken(tokenString string) (*Claims, error) {                         │
│      // ... relevant code ...                                                      │
│  }                                                                                 │
│  ```                                                                               │
│  [Full file: search("jwt.go") or get_file("jwt.go")]                               │
│                                                                                     │
│  ─────────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  TIER B: SUMMARY (confidence 0.85-0.90) - 30% of budget                            │
│  ══════════════════════════════════════════════════════                            │
│  • One-line description, no content                                                │
│  • Awareness hint only                                                             │
│  • Max 3 summaries                                                                 │
│                                                                                     │
│  Format:                                                                           │
│  [RELATED: auth_middleware.go - JWT validation middleware, 89 lines]               │
│  [RELATED: token_refresh.go - refresh token logic, 156 lines]                      │
│                                                                                     │
│  ─────────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  TIER C: NOTHING (confidence < 0.85)                                               │
│  ═══════════════════════════════════                                               │
│  • Not mentioned in context                                                        │
│  • Available via search tool if LLM needs it                                       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Multi-Signal Validation

To maximize accuracy, content must score high on **both** search systems:

```go
// Scoring for pre-fetch decisions
func (p *Prefetcher) shouldInject(bleveScore, vectorScore float64, recency int) (Tier, bool) {
    // Must have signal from both systems for high confidence
    if p.config.RequireMultiSignal {
        if bleveScore < 0.5 || vectorScore < 0.5 {
            return TierNone, false
        }
    }

    // Combined score with recency boost
    combined := (bleveScore + vectorScore) / 2
    recencyFactor := 1.0 + (p.config.RecencyBoost * float64(maxRecency-recency) / float64(maxRecency))
    finalScore := combined * recencyFactor

    switch {
    case finalScore >= p.config.ExcerptThreshold:
        return TierExcerpt, true
    case finalScore >= p.config.ConfidenceThreshold:
        return TierSummary, true
    default:
        return TierNone, false
    }
}
```

### Query Augmenter

```go
// core/context/query_augmenter.go

type QueryAugmenter struct {
    bleve        *BleveIndex
    vectorDB     *vectorgraphdb.VectorGraphDB
    hotTracker   *AccessTracker
    config       *PrefetchConfig
}

type AugmentedQuery struct {
    OriginalQuery string
    Excerpts      []Excerpt     // Tier A: full content excerpts
    Summaries     []Summary     // Tier B: one-line hints
    TokensUsed    int
    BudgetMax     int
}

type Excerpt struct {
    Source      string   // file path or content ID
    LineRange   [2]int   // start, end lines
    Content     string   // actual content
    Confidence  float64
    Tokens      int
}

type Summary struct {
    Source      string
    Description string
    Lines       int
    Confidence  float64
}

func (qa *QueryAugmenter) Augment(ctx context.Context, query string, remainingTokens int) (*AugmentedQuery, error) {
    budget := int(float64(remainingTokens) * qa.config.MaxBudgetPercent)

    // Parallel search
    var wg sync.WaitGroup
    var bleveResults []SearchResult
    var vectorResults []SearchResult

    wg.Add(2)
    go func() {
        defer wg.Done()
        bleveResults, _ = qa.bleve.Search(query, 10)
    }()
    go func() {
        defer wg.Done()
        embedding, _ := qa.vectorDB.Embed(query)
        vectorResults, _ = qa.vectorDB.Search(embedding, 10)
    }()
    wg.Wait()

    // Fuse and rank
    ranked := qa.fuseResults(bleveResults, vectorResults)

    // Filter already-hot content
    ranked = qa.filterHot(ranked)

    // Build augmentation within budget
    result := &AugmentedQuery{
        OriginalQuery: query,
        BudgetMax:     budget,
    }

    excerptBudget := int(float64(budget) * 0.6)
    summaryBudget := int(float64(budget) * 0.3)

    for _, r := range ranked {
        tier, ok := qa.shouldInject(r.BleveScore, r.VectorScore, r.Recency)
        if !ok {
            continue
        }

        switch tier {
        case TierExcerpt:
            if len(result.Excerpts) >= qa.config.MaxExcerpts {
                continue
            }
            excerpt := qa.extractExcerpt(r, excerptBudget-result.excerptTokens())
            if excerpt != nil {
                result.Excerpts = append(result.Excerpts, *excerpt)
            }

        case TierSummary:
            if len(result.Summaries) >= qa.config.MaxSummaries {
                continue
            }
            if result.summaryTokens() < summaryBudget {
                result.Summaries = append(result.Summaries, Summary{
                    Source:      r.Source,
                    Description: r.Summary,
                    Lines:       r.LineCount,
                    Confidence:  r.CombinedScore,
                })
            }
        }
    }

    result.TokensUsed = result.excerptTokens() + result.summaryTokens()
    return result, nil
}
```

### Access Tracking for Hot Tier

```go
// core/context/access_tracker.go

type AccessTracker struct {
    mu           sync.RWMutex
    accessCounts map[string]int       // content ID → access count
    lastAccess   map[string]int       // content ID → turn number
    accessLog    []AccessEvent        // chronological log

    hotThreshold int                  // accesses to become "hot" (default: 3)
    hotWindow    int                  // turns to stay hot after access (default: 5)
}

type AccessEvent struct {
    ContentID   string
    TurnNumber  int
    AccessType  string  // "prefetch_used", "tool_retrieved", "in_response"
    Timestamp   time.Time
}

func (t *AccessTracker) RecordAccess(contentID string, turnNumber int, accessType string) {
    t.mu.Lock()
    defer t.mu.Unlock()

    t.accessCounts[contentID]++
    t.lastAccess[contentID] = turnNumber
    t.accessLog = append(t.accessLog, AccessEvent{
        ContentID:  contentID,
        TurnNumber: turnNumber,
        AccessType: accessType,
        Timestamp:  time.Now(),
    })
}

func (t *AccessTracker) IsHot(contentID string, currentTurn int) bool {
    t.mu.RLock()
    defer t.mu.RUnlock()

    count := t.accessCounts[contentID]
    lastAccess := t.lastAccess[contentID]
    turnsSince := currentTurn - lastAccess

    return count >= t.hotThreshold || turnsSince <= t.hotWindow
}

func (t *AccessTracker) GetHotContent() []string {
    t.mu.RLock()
    defer t.mu.RUnlock()

    var hot []string
    for id := range t.accessCounts {
        if t.IsHot(id, t.currentTurn) {
            hot = append(hot, id)
        }
    }
    return hot
}
```

### Complete Flow

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    HYBRID RETRIEVAL COMPLETE FLOW                                   │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  1. QUERY ARRIVES: "Fix the JWT expiration bug"                                    │
│     │                                                                              │
│     ▼                                                                              │
│  2. CALCULATE BUDGET                                                               │
│     • Remaining context: 55k tokens                                                │
│     • Pre-fetch budget: 5.5k tokens (10%)                                          │
│     │                                                                              │
│     ▼                                                                              │
│  3. PARALLEL SEARCH                                                                │
│     • Bleve: "JWT expiration bug" → [jwt.go:0.94, auth.go:0.81]                    │
│     • VectorDB: [embedding] → [jwt.go:0.91, middleware.go:0.84]                    │
│     │                                                                              │
│     ▼                                                                              │
│  4. FUSE + FILTER                                                                  │
│     • jwt.go: 0.94 + 0.91 = TIER A (both signals, high confidence)                 │
│     • middleware.go: 0.84 vector only = TIER B (summary)                           │
│     • auth.go: 0.81 bleve only = TIER B (summary)                                  │
│     • Others below 0.85 = skip                                                     │
│     │                                                                              │
│     ▼                                                                              │
│  5. BUILD INJECTION (~400 tokens, 0.7% of context)                                 │
│     ┌───────────────────────────────────────────────────────────────────────────┐  │
│     │ [AUTO-RETRIEVED: jwt.go:38-67 | confidence: 0.92]                         │  │
│     │ ```go                                                                      │  │
│     │ func ValidateToken(tokenString string) (*Claims, error) { ... }           │  │
│     │ ```                                                                        │  │
│     │ [RELATED: auth_middleware.go - JWT validation, 89 lines]                  │  │
│     │ [RELATED: middleware.go - HTTP middleware chain, 156 lines]               │  │
│     └───────────────────────────────────────────────────────────────────────────┘  │
│     │                                                                              │
│     ▼                                                                              │
│  6. LLM RECEIVES: [system] + [hot] + [prefetch] + [query]                          │
│     │                                                                              │
│     ├── Has enough → Responds directly (80% of cases)                              │
│     │                                                                              │
│     └── Needs more → Tool call: search("auth middleware JWT")                      │
│                      │                                                             │
│                      ▼                                                             │
│  7. TOOL RETRIEVAL (on-demand)                                                     │
│     • Returns auth_middleware.go excerpt                                           │
│     • Promotes to HOT tier                                                         │
│     │                                                                              │
│     ▼                                                                              │
│  8. LLM COMPLETES RESPONSE                                                         │
│     │                                                                              │
│     ▼                                                                              │
│  9. ACCESS TRACKING UPDATED                                                        │
│     • jwt.go excerpt was used → stays HOT                                          │
│     • auth_middleware.go retrieved → now HOT                                       │
│     • middleware.go not used → remains COLD                                        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Feedback Loop for Continuous Improvement

```go
// Track pre-fetch effectiveness
type PrefetchMetrics struct {
    TotalPrefetches    int64
    ExcerptsInjected   int64
    ExcerptsUsed       int64   // Referenced in LLM response
    SummariesInjected  int64
    SummariesExpanded  int64   // LLM requested full content
    TokensSaved        int64   // Estimated tokens saved vs LLM searching

    // Per-pattern tracking
    PatternHitRate     map[string]float64  // query pattern → hit rate
}

func (m *PrefetchMetrics) RecordOutcome(prefetch *AugmentedQuery, response string, toolCalls []ToolCall) {
    m.TotalPrefetches++

    for _, excerpt := range prefetch.Excerpts {
        m.ExcerptsInjected++
        if strings.Contains(response, excerpt.Source) || referencesContent(response, excerpt) {
            m.ExcerptsUsed++
        }
    }

    for _, summary := range prefetch.Summaries {
        m.SummariesInjected++
        for _, tc := range toolCalls {
            if tc.Target == summary.Source {
                m.SummariesExpanded++
                break
            }
        }
    }

    // Estimate tokens saved: if excerpt was used, LLM didn't need to search
    // Average search round-trip costs ~500 tokens (query + results + processing)
    m.TokensSaved += int64(m.ExcerptsUsed) * 500
}
```

### Key Benefits

| Metric | Without Hybrid | With Hybrid |
|--------|---------------|-------------|
| LLM search calls per query | 2-5 | 0-1 |
| Context used for retrieval | Variable, often wasteful | ≤10% budget, surgical |
| Time to first response | Delayed by searches | Immediate (prefetch parallel) |
| Accuracy | LLM may search wrong things | Multi-signal validation |
| Hot content after handoff | Lost | Preserved via tracking |

### Self-Tuning Adaptive System

The retrieval system uses **fully adaptive learning** optimized for maximal performance, robustness, and correctness. All parameters are Bayesian distributions that converge based on observed outcomes.

#### Core Principle: Maximal Performance + Robustness

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    MAXIMAL ADAPTIVE RETRIEVAL ARCHITECTURE                           │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  PERFORMANCE OPTIMIZATIONS:                                                         │
│  ══════════════════════════                                                         │
│  1. SPECULATIVE PRE-FETCH - Start immediately, race against need                   │
│  2. TIERED SEARCH - Hot cache (<1ms) → Warm index (<10ms) → Full (<200ms)          │
│  3. SUFFICIENT STATISTICS - No raw history, distributions ARE the state            │
│                                                                                     │
│  ROBUSTNESS GUARANTEES:                                                             │
│  ══════════════════════                                                             │
│  1. WRITE-AHEAD LOG - Observations survive crashes                                  │
│  2. CIRCUIT BREAKERS - Graceful degradation on backend failure                     │
│  3. ROBUST BAYESIAN - Outlier rejection, decay, drift, cold start protection       │
│                                                                                     │
│  CORRECTNESS PROPERTIES:                                                            │
│  ═══════════════════════                                                            │
│  1. CONTEXT DISCOVERY - Embedding-based clustering, contexts emerge                │
│  2. QUALITY-FIRST - Task success primary, efficiency secondary                     │
│  3. NON-STATIONARITY - Prior drift handles changing preferences                    │
│                                                                                     │
│  WHAT IS NOT ADAPTIVE:                                                              │
│  ═════════════════════                                                              │
│  • The STRUCTURE of what signals to observe (definitional)                          │
│  • The PRIORS (initial beliefs with high uncertainty)                               │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Speculative Parallel Pre-fetch

Don't decide "pre-fetch or not" - **always start pre-fetch, race against need**.

> **⚠️ WAVE 4 INTEGRATION REQUIRED**: The simplified examples below show the concept.
> See "Integration with WAVE 4 Robustness Systems" section for the production implementation
> using `GoroutineScope.Go()`, `ResourceTracker`, and proper lifecycle management.

```go
// core/context/speculative_prefetch.go
// CONCEPTUAL - see WAVE 4 Integration section for production implementation

// SpeculativePrefetcher runs pre-fetch in parallel with tool execution
type SpeculativePrefetcher struct {
    searcher  *TieredSearcher
    hotCache  *HotCache

    // In-flight speculative fetches
    inflight  sync.Map  // queryHash → *PrefetchFuture

    // REQUIRED (see WAVE 4 Integration):
    // scope     *concurrency.GoroutineScope
    // budget    *concurrency.GoroutineBudget
    // tracker   *concurrency.ResourceTracker
}

type PrefetchFuture struct {
    result   atomic.Pointer[AugmentedQuery]
    done     chan struct{}
    started  time.Time

    // REQUIRED (see WAVE 4 Integration):
    // operation *concurrency.Operation
}

// StartSpeculative begins pre-fetch immediately, returns future
// NOTE: Production code MUST use scope.Go(), not raw go - see WAVE 4 Integration
func (p *SpeculativePrefetcher) StartSpeculative(query string) *PrefetchFuture {
    future := &PrefetchFuture{
        done:    make(chan struct{}),
        started: time.Now(),
    }

    // CONCEPTUAL ONLY - production uses scope.Go()
    go func() {
        defer close(future.done)
        result := p.searcher.SearchWithBudget(context.Background(), query, 200*time.Millisecond)
        future.result.Store(result.ToAugmentedQuery())
    }()

    return future
}

// GetIfReady returns result if available within budget, nil otherwise
func (f *PrefetchFuture) GetIfReady(budget time.Duration) *AugmentedQuery {
    select {
    case <-f.done:
        return f.result.Load()
    case <-time.After(budget):
        return nil  // Don't wait, proceed without
    }
}

// Usage in agent loop
func (a *Agent) ExecuteTool(tool ToolCall) ToolResult {
    // Start speculative pre-fetch based on predicted next query
    predictedQuery := a.predictNextQuery(tool)
    future := a.prefetcher.StartSpeculative(predictedQuery)

    // Execute tool (this is the slow part)
    result := tool.Execute()

    // Check if pre-fetch is ready (usually yes, tool is slower)
    prefetch := future.GetIfReady(10 * time.Millisecond)

    return ToolResult{
        Data:     result,
        Prefetch: prefetch,  // Attach if available
    }
}
```

#### Tiered Search with Latency Budgets

Not binary "search or don't" - **tiered search that respects time constraints**.

> **⚠️ WAVE 4 INTEGRATION REQUIRED**: Circuit breakers must use `GlobalCircuitBreakerRegistry`.
> File handles must be acquired through `FileHandleBudget`. Max tier reduced under memory pressure.
> See "Integration with WAVE 4 Robustness Systems" section for production implementation.

```go
// core/context/tiered_search.go
// CONCEPTUAL - see WAVE 4 Integration section for production implementation

type SearchTier int

const (
    TierHotCache  SearchTier = iota  // < 1ms - in-memory hot content
    TierWarmIndex                     // < 10ms - in-memory Bleve subset
    TierFullSearch                    // < 200ms - full Bleve + VectorDB
)

type TieredSearcher struct {
    hotCache   *HotCache           // Recently accessed, in-memory
    warmIndex  *BleveIndex         // Subset index, memory-mapped
    fullBleve  *BleveIndex         // Full index
    vectorDB   *vectorgraphdb.VectorGraphDB

    // CONCEPTUAL - production uses GlobalCircuitBreakerRegistry
    bleveCB    *CircuitBreaker
    vectorCB   *CircuitBreaker

    // REQUIRED (see WAVE 4 Integration):
    // cbRegistry *llm.GlobalCircuitBreakerRegistry
    // pressure   *resources.PressureController
}

func (t *TieredSearcher) SearchWithBudget(ctx context.Context, query string, budget time.Duration) *SearchResults {
    results := &SearchResults{}
    deadline := time.Now().Add(budget)

    // Tier 0: Always check hot cache (< 1ms)
    results.Merge(t.hotCache.Search(query))

    if time.Now().After(deadline) || results.SufficientConfidence() {
        return results
    }

    // Tier 1: Warm index if time permits (< 10ms)
    warmCtx, cancel := context.WithDeadline(ctx, deadline)
    defer cancel()

    if warmResults, err := t.warmIndex.SearchWithContext(warmCtx, query, 5); err == nil {
        results.Merge(warmResults)
    }

    if time.Now().After(deadline) || results.SufficientConfidence() {
        return results
    }

    // Tier 2: Full search with remaining budget (parallel Bleve + VectorDB)
    remaining := time.Until(deadline)
    t.executeFullSearch(ctx, query, remaining, results)

    return results
}

func (t *TieredSearcher) executeFullSearch(ctx context.Context, query string, budget time.Duration, results *SearchResults) {
    fullCtx, cancel := context.WithTimeout(ctx, budget)
    defer cancel()

    var wg sync.WaitGroup
    var mu sync.Mutex

    // Try Bleve if circuit is closed
    if t.bleveCB.Allow() {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if r, err := t.fullBleve.SearchWithContext(fullCtx, query, 10); err == nil {
                t.bleveCB.RecordSuccess()
                mu.Lock()
                results.Merge(r)
                mu.Unlock()
            } else {
                t.bleveCB.RecordFailure()
            }
        }()
    }

    // Try VectorDB if circuit is closed
    if t.vectorCB.Allow() {
        wg.Add(1)
        go func() {
            defer wg.Done()
            embedding, _ := t.vectorDB.Embed(query)
            if r, err := t.vectorDB.SearchWithContext(fullCtx, embedding, 10); err == nil {
                t.vectorCB.RecordSuccess()
                mu.Lock()
                results.Merge(r)
                mu.Unlock()
            } else {
                t.vectorCB.RecordFailure()
            }
        }()
    }

    wg.Wait()

    // Degrade confidence requirements if only single signal
    if results.SingleSignalOnly() {
        results.RequireMultiSignal = false
        results.ConfidenceThreshold *= 1.1  // Raise threshold to compensate
    }
}
```

#### Circuit Breakers for Graceful Degradation

```go
// core/context/circuit_breaker.go

// CircuitBreaker prevents hammering failed services
type CircuitBreaker struct {
    failures    int64
    successes   int64
    lastFailure time.Time
    threshold   int64         // Open after N consecutive failures
    resetAfter  time.Duration // Try again after this duration
    mu          sync.RWMutex
}

func NewCircuitBreaker(threshold int64, resetAfter time.Duration) *CircuitBreaker {
    return &CircuitBreaker{
        threshold:  threshold,
        resetAfter: resetAfter,
    }
}

func (cb *CircuitBreaker) Allow() bool {
    cb.mu.RLock()
    defer cb.mu.RUnlock()

    if cb.failures >= cb.threshold {
        // Circuit is open - check if reset period elapsed
        if time.Since(cb.lastFailure) > cb.resetAfter {
            return true  // Allow one probe request
        }
        return false  // Still open
    }
    return true  // Circuit closed
}

func (cb *CircuitBreaker) RecordSuccess() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.failures = 0
    cb.successes++
}

func (cb *CircuitBreaker) RecordFailure() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.failures++
    cb.lastFailure = time.Now()
}
```

#### Robust Weight Distribution Learning

```go
// core/context/adaptive_reward.go

// RobustWeightDistribution handles outliers, non-stationarity, and cold start
type RobustWeightDistribution struct {
    Alpha float64
    Beta  float64

    // Robustness tracking
    effectiveSamples float64
    priorAlpha       float64  // Original prior for drift
    priorBeta        float64
}

func (w *RobustWeightDistribution) Mean() float64 {
    return w.Alpha / (w.Alpha + w.Beta)
}

func (w *RobustWeightDistribution) Variance() float64 {
    sum := w.Alpha + w.Beta
    return (w.Alpha * w.Beta) / (sum * sum * (sum + 1))
}

func (w *RobustWeightDistribution) Sample() float64 {
    return betaSample(w.Alpha, w.Beta)
}

func (w *RobustWeightDistribution) Update(observation float64, satisfaction float64, config *UpdateConfig) {
    // 1. Outlier detection - reject observations far from current belief
    mean := w.Mean()
    stddev := math.Sqrt(w.Variance())

    if stddev > 0 {
        zScore := math.Abs(observation-mean) / stddev
        if zScore > config.OutlierThreshold {  // e.g., 3.0
            return  // Reject outlier
        }
    }

    // 2. Exponential decay on existing evidence (recency weighting)
    w.Alpha *= config.DecayFactor  // e.g., 0.999
    w.Beta *= config.DecayFactor
    w.effectiveSamples *= config.DecayFactor

    // 3. Add new observation
    weight := math.Abs(satisfaction)
    if satisfaction > 0 {
        w.Alpha += weight * observation
        w.Beta += weight * (1 - observation)
    } else {
        w.Alpha += weight * (1 - observation)
        w.Beta += weight * observation
    }
    w.effectiveSamples += weight

    // 4. Minimum effective sample size (cold start protection)
    if w.effectiveSamples < config.MinEffectiveSamples {
        scale := config.MinEffectiveSamples / w.effectiveSamples
        w.Alpha = w.priorAlpha + (w.Alpha-w.priorAlpha)/scale
        w.Beta = w.priorBeta + (w.Beta-w.priorBeta)/scale
    }

    // 5. Prior drift (handles non-stationarity)
    w.Alpha = w.Alpha*(1-config.DriftRate) + w.priorAlpha*config.DriftRate
    w.Beta = w.Beta*(1-config.DriftRate) + w.priorBeta*config.DriftRate
}

type UpdateConfig struct {
    DecayFactor         float64  // 0.999 - recent observations matter more
    OutlierThreshold    float64  // 3.0 - reject > 3 sigma
    MinEffectiveSamples float64  // 10.0 - cold start protection
    DriftRate           float64  // 0.001 - slow return to prior
}

var DefaultUpdateConfig = &UpdateConfig{
    DecayFactor:         0.999,
    OutlierThreshold:    3.0,
    MinEffectiveSamples: 10.0,
    DriftRate:           0.001,
}
```

#### Write-Ahead Log for Observations

Observations must survive crashes. Use append-only log with background processing.

> **⚠️ WAVE 4 INTEGRATION REQUIRED**: Channels must use `SafeChan` for context-aware operations.
> File handles must be acquired through `FileHandleBudget`. Background processor must use `scope.Go()`.
> See "Integration with WAVE 4 Robustness Systems" section for production implementation.

```go
// core/context/observation_log.go
// CONCEPTUAL - see WAVE 4 Integration section for production implementation

type ObservationLog struct {
    file      *os.File
    encoder   *json.Encoder
    mu        sync.Mutex

    // CONCEPTUAL - production uses SafeChan
    updateChan chan EpisodeObservation
    adaptive   *AdaptiveState

    // REQUIRED (see WAVE 4 Integration):
    // updateChan *safechan.SafeChan[EpisodeObservation]
    // scope      *concurrency.GoroutineScope
}

func NewObservationLog(path string, adaptive *AdaptiveState) (*ObservationLog, error) {
    file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }

    log := &ObservationLog{
        file:       file,
        encoder:    json.NewEncoder(file),
        updateChan: make(chan EpisodeObservation, 100),  // Production: safechan.New[...]
        adaptive:   adaptive,
    }

    // CONCEPTUAL - production uses scope.Go()
    go log.processLoop()
    return log, nil
}

func (l *ObservationLog) Record(obs EpisodeObservation) error {
    l.mu.Lock()
    defer l.mu.Unlock()

    // Append to WAL (fast, sequential write)
    obs.Timestamp = time.Now()
    if err := l.encoder.Encode(obs); err != nil {
        return err
    }

    // Queue for async processing (non-blocking)
    select {
    case l.updateChan <- obs:
    default:
        // Queue full, observation still logged but not immediately processed
    }

    return l.file.Sync()
}

func (l *ObservationLog) processLoop() {
    for obs := range l.updateChan {
        l.adaptive.UpdateFromOutcome(obs)
    }
}

// RecoverFromLog replays observations after crash
func (l *ObservationLog) RecoverFromLog(logPath string, checkpoint int64) error {
    file, err := os.Open(logPath)
    if err != nil {
        return err
    }
    defer file.Close()

    file.Seek(checkpoint, 0)
    decoder := json.NewDecoder(file)

    for {
        var obs EpisodeObservation
        if err := decoder.Decode(&obs); err != nil {
            break
        }
        l.adaptive.UpdateFromOutcome(obs)
    }

    return nil
}
```

#### Embedding-Based Context Discovery

Contexts emerge from query patterns, not predefined categories.

```go
// core/context/context_discovery.go

type ContextDiscovery struct {
    embedder    Embedder
    centroids   []ContextCentroid
    maxContexts int

    // Fast path: keyword cache
    keywordCache sync.Map  // normalized query → TaskContext
}

type ContextCentroid struct {
    ID        TaskContext
    Embedding []float32
    Count     int64

    // Learned bias for this context
    Bias      *ContextWeightBias
}

func (c *ContextDiscovery) ClassifyQuery(query string, embedding []float32) TaskContext {
    // Fast path: check keyword cache first (< 1μs)
    normalized := normalizeForCache(query)
    if ctx, ok := c.keywordCache.Load(normalized); ok {
        return ctx.(TaskContext)
    }

    // Find nearest centroid using provided embedding
    bestCtx := TaskContext("general")
    bestSim := float32(0)

    for _, centroid := range c.centroids {
        sim := cosineSimilarity(embedding, centroid.Embedding)
        if sim > bestSim {
            bestSim = sim
            bestCtx = centroid.ID
        }
    }

    // If no good match and room for more contexts, create new one
    if bestSim < 0.7 && len(c.centroids) < c.maxContexts {
        newCtx := c.createContext(embedding, query)
        c.keywordCache.Store(normalized, newCtx.ID)
        return newCtx.ID
    }

    // Update centroid with this query (online learning)
    c.updateCentroid(bestCtx, embedding)
    c.keywordCache.Store(normalized, bestCtx)

    return bestCtx
}

func (c *ContextDiscovery) createContext(embedding []float32, seedQuery string) *ContextCentroid {
    ctx := &ContextCentroid{
        ID:        TaskContext(fmt.Sprintf("ctx_%d", len(c.centroids))),
        Embedding: embedding,
        Count:     1,
        Bias:      &ContextWeightBias{RelevanceMult: 1.0, StruggleMult: 1.0, WasteMult: 1.0},
    }
    c.centroids = append(c.centroids, *ctx)
    return ctx
}

func (c *ContextDiscovery) updateCentroid(ctxID TaskContext, embedding []float32) {
    for i := range c.centroids {
        if c.centroids[i].ID == ctxID {
            // Exponential moving average of embedding
            alpha := float32(0.1)
            for j := range c.centroids[i].Embedding {
                c.centroids[i].Embedding[j] = (1-alpha)*c.centroids[i].Embedding[j] + alpha*embedding[j]
            }
            c.centroids[i].Count++
            break
        }
    }
}

func normalizeForCache(query string) string {
    return strings.ToLower(strings.TrimSpace(query))
}
```

#### Satisfaction Inference (No Explicit Ratings)

```go
// EpisodeObservation captures full episode for learning
type EpisodeObservation struct {
    // Metadata
    Timestamp        time.Time
    Position         int64  // Log position for recovery

    // What weights were used
    SampledWeights   RewardWeights
    SampledThresholds ThresholdConfig

    // What context (discovered, not predefined)
    TaskContext      TaskContext
    QueryEmbedding   []float32

    // What happened (behavioral signals)
    TaskCompleted    bool
    FollowUpCount    int
    ToolCallCount    int
    UserEdits        int
    HedgingDetected  bool
    SessionDuration  time.Duration
    ExplicitSignals  []string

    // What was prefetched vs used
    PrefetchedIDs    []string
    UsedIDs          []string
    SearchedAfter    []string  // What LLM searched for after prefetch
}

func (e *EpisodeObservation) InferSatisfaction() float64 {
    var score float64

    // Positive signals
    if e.TaskCompleted && e.FollowUpCount == 0 {
        score += 0.5  // Clean completion
    }
    if e.TaskCompleted && e.FollowUpCount > 0 {
        score += 0.2  // Completed but needed help
    }

    // Negative signals
    score -= float64(e.FollowUpCount) * 0.1        // Each follow-up hurts
    score -= float64(len(e.SearchedAfter)) * 0.15  // Had to search = starvation
    if e.HedgingDetected {
        score -= 0.2  // LLM was uncertain
    }
    if !e.TaskCompleted {
        score -= 0.4  // Failed
    }

    // Prefetch efficiency (but don't optimize purely for this)
    unusedPrefetch := len(e.PrefetchedIDs) - len(e.UsedIDs)
    score -= float64(unusedPrefetch) * 0.02  // Minor penalty for waste

    return score  // Can be negative
}
```

#### Adaptive State (Sufficient Statistics Only)

No raw history needed - distributions ARE the learned state.

```go
// core/context/adaptive_state.go

// AdaptiveState stores only sufficient statistics - no raw history
type AdaptiveState struct {
    // Weight distributions (robust)
    Weights struct {
        TaskSuccess     RobustWeightDistribution
        RelevanceBonus  RobustWeightDistribution
        StrugglePenalty RobustWeightDistribution
        WastePenalty    RobustWeightDistribution
    }

    // Threshold distributions
    Thresholds struct {
        Confidence RobustWeightDistribution
        Excerpt    RobustWeightDistribution
        Budget     RobustWeightDistribution
    }

    // Context discovery (contexts emerge, not predefined)
    ContextDiscovery *ContextDiscovery

    // Single user profile (for terminal app - one user)
    UserProfile UserWeightProfile

    // Metadata
    TotalObservations int64
    LastUpdated       time.Time
    Version           int

    // Update configuration
    config *UpdateConfig
}

// NewAdaptiveState creates system with initial priors
func NewAdaptiveState() *AdaptiveState {
    return &AdaptiveState{
        Weights: struct {
            TaskSuccess     RobustWeightDistribution
            RelevanceBonus  RobustWeightDistribution
            StrugglePenalty RobustWeightDistribution
            WastePenalty    RobustWeightDistribution
        }{
            TaskSuccess:     RobustWeightDistribution{Alpha: 8, Beta: 2, priorAlpha: 8, priorBeta: 2},
            RelevanceBonus:  RobustWeightDistribution{Alpha: 3, Beta: 7, priorAlpha: 3, priorBeta: 7},
            StrugglePenalty: RobustWeightDistribution{Alpha: 4, Beta: 6, priorAlpha: 4, priorBeta: 6},
            WastePenalty:    RobustWeightDistribution{Alpha: 1, Beta: 9, priorAlpha: 1, priorBeta: 9},
        },
        Thresholds: struct {
            Confidence RobustWeightDistribution
            Excerpt    RobustWeightDistribution
            Budget     RobustWeightDistribution
        }{
            Confidence: RobustWeightDistribution{Alpha: 8.5, Beta: 1.5, priorAlpha: 8.5, priorBeta: 1.5},
            Excerpt:    RobustWeightDistribution{Alpha: 9, Beta: 1, priorAlpha: 9, priorBeta: 1},
            Budget:     RobustWeightDistribution{Alpha: 1, Beta: 9, priorAlpha: 1, priorBeta: 9},
        },
        ContextDiscovery: &ContextDiscovery{maxContexts: 10},
        UserProfile:      UserWeightProfile{WastePenaltyMult: 1.0, StrugglePenaltyMult: 1.0},
        config:           DefaultUpdateConfig,
    }
}

// SampleWeights draws from current weight distributions (Thompson sampling)
func (a *AdaptiveState) SampleWeights(ctx TaskContext) RewardWeights {
    weights := RewardWeights{
        TaskSuccess:     a.Weights.TaskSuccess.Sample(),
        RelevanceBonus:  a.Weights.RelevanceBonus.Sample(),
        StrugglePenalty: a.Weights.StrugglePenalty.Sample(),
        WastePenalty:    a.Weights.WastePenalty.Sample(),
    }

    // Apply user profile adjustments
    if a.UserProfile.ObservationCount >= 5 {
        weights = a.UserProfile.Adjust(weights)
    }

    // Apply context-specific bias if learned
    if bias := a.ContextDiscovery.GetBias(ctx); bias != nil {
        weights = bias.Adjust(weights)
    }

    return weights
}

// UpdateFromOutcome performs robust Bayesian update
func (a *AdaptiveState) UpdateFromOutcome(obs EpisodeObservation) {
    satisfaction := obs.InferSatisfaction()

    // Update weight distributions with robust updates
    a.Weights.TaskSuccess.Update(obs.SampledWeights.TaskSuccess, satisfaction, a.config)
    a.Weights.RelevanceBonus.Update(obs.SampledWeights.RelevanceBonus, satisfaction, a.config)
    a.Weights.StrugglePenalty.Update(obs.SampledWeights.StrugglePenalty, satisfaction, a.config)
    a.Weights.WastePenalty.Update(obs.SampledWeights.WastePenalty, satisfaction, a.config)

    // Update thresholds
    a.Thresholds.Confidence.Update(obs.SampledThresholds.Confidence, satisfaction, a.config)
    a.Thresholds.Excerpt.Update(obs.SampledThresholds.Excerpt, satisfaction, a.config)
    a.Thresholds.Budget.Update(obs.SampledThresholds.Budget, satisfaction, a.config)

    // Update context bias
    a.ContextDiscovery.UpdateBias(obs.TaskContext, satisfaction, obs)

    // Update user profile
    a.updateUserProfile(obs, satisfaction)

    a.TotalObservations++
    a.LastUpdated = time.Now()
}

// Persistence - serialize only sufficient statistics (< 1KB typically)
func (a *AdaptiveState) MarshalBinary() ([]byte, error) {
    return json.Marshal(a)
}

func (a *AdaptiveState) UnmarshalBinary(data []byte) error {
    return json.Unmarshal(data, a)
}

// LoadOrInit loads persisted state or initializes with priors
func LoadOrInit(path string) (*AdaptiveState, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        // First run - use priors
        return NewAdaptiveState(), nil
    }

    state := &AdaptiveState{}
    if err := state.UnmarshalBinary(data); err != nil {
        return NewAdaptiveState(), nil
    }

    return state, nil
}

// SavePeriodically persists state in background
func (a *AdaptiveState) SavePeriodically(path string, interval time.Duration) {
    ticker := time.NewTicker(interval)
    go func() {
        for range ticker.C {
            data, _ := a.MarshalBinary()
            os.WriteFile(path, data, 0644)
        }
    }()
}
```

#### User Profile (Single User for Terminal App)

```go
// UserWeightProfile learns individual preferences from behavior
// Simplified for terminal app (single user)
type UserWeightProfile struct {
    // Learned biases (scale: -1 to +1)
    PrefersThorough   float64  // -1 = wants concise, +1 = wants thorough
    ToleratesSearches float64  // -1 = hates tool calls, +1 = fine with them

    // Adjustment multipliers (learned)
    WastePenaltyMult    float64  // Lower for verbose-preferring users
    StrugglePenaltyMult float64  // Higher for search-averse users

    // Confidence tracking
    ObservationCount int
    LastUpdated      time.Time
}

func (p *UserWeightProfile) Adjust(base RewardWeights) RewardWeights {
    if p.ObservationCount < 5 {
        return base  // Not enough data, use base weights
    }

    return RewardWeights{
        TaskSuccess:     base.TaskSuccess,  // Never adjust task success
        RelevanceBonus:  base.RelevanceBonus,
        StrugglePenalty: base.StrugglePenalty * p.StrugglePenaltyMult,
        WastePenalty:    base.WastePenalty * p.WastePenaltyMult,
    }
}

func (a *AdaptiveState) updateUserProfile(obs EpisodeObservation, satisfaction float64) {
    a.UserProfile.ObservationCount++
    a.UserProfile.LastUpdated = time.Now()

    // Learn preferences from behavior patterns
    if satisfaction > 0 && len(obs.PrefetchedIDs) > len(obs.UsedIDs)+2 {
        // Success despite lots of unused prefetch → user tolerates verbosity
        a.UserProfile.PrefersThorough += 0.1
        a.UserProfile.WastePenaltyMult *= 0.95  // Reduce waste penalty
    }

    if satisfaction < 0 && len(obs.SearchedAfter) > 2 {
        // Failure with many searches → user dislikes searching
        a.UserProfile.ToleratesSearches -= 0.1
        a.UserProfile.StrugglePenaltyMult *= 1.05  // Increase struggle penalty
    }

    // Clamp values
    a.UserProfile.PrefersThorough = clamp(a.UserProfile.PrefersThorough, -1, 1)
    a.UserProfile.ToleratesSearches = clamp(a.UserProfile.ToleratesSearches, -1, 1)
    a.UserProfile.WastePenaltyMult = clamp(a.UserProfile.WastePenaltyMult, 0.5, 2.0)
    a.UserProfile.StrugglePenaltyMult = clamp(a.UserProfile.StrugglePenaltyMult, 0.5, 2.0)
}

func clamp(v, min, max float64) float64 {
    if v < min { return min }
    if v > max { return max }
    return v
}
```

#### Context Weight Biases (Discovered, Not Predefined)

```go
// ContextWeightBias learned from embedding-based context clusters
type ContextWeightBias struct {
    // Learned adjustment multipliers
    RelevanceMult float64
    StruggleMult  float64
    WasteMult     float64

    ObservationCount int
}

func (b *ContextWeightBias) Adjust(base RewardWeights) RewardWeights {
    return RewardWeights{
        TaskSuccess:     base.TaskSuccess,
        RelevanceBonus:  base.RelevanceBonus * b.RelevanceMult,
        StrugglePenalty: base.StrugglePenalty * b.StruggleMult,
        WastePenalty:    base.WastePenalty * b.WasteMult,
    }
}

func (c *ContextDiscovery) GetBias(ctx TaskContext) *ContextWeightBias {
    for i := range c.centroids {
        if c.centroids[i].ID == ctx {
            return c.centroids[i].Bias
        }
    }
    return nil
}

func (c *ContextDiscovery) UpdateBias(ctx TaskContext, satisfaction float64, obs EpisodeObservation) {
    for i := range c.centroids {
        if c.centroids[i].ID == ctx {
            bias := c.centroids[i].Bias
            bias.ObservationCount++

            // Learn context-specific adjustments
            learningRate := 0.05
            if satisfaction > 0 {
                // Good outcome - current biases worked
                // Small reinforcement toward current values
            } else {
                // Bad outcome - adjust biases
                if len(obs.SearchedAfter) > 2 {
                    // Context needs more prefetch
                    bias.StruggleMult += learningRate
                }
                if len(obs.PrefetchedIDs) > len(obs.UsedIDs)+3 {
                    // Context gets too much noise
                    bias.WasteMult += learningRate
                }
            }

            // Clamp
            bias.RelevanceMult = clamp(bias.RelevanceMult, 0.5, 2.0)
            bias.StruggleMult = clamp(bias.StruggleMult, 0.5, 2.0)
            bias.WasteMult = clamp(bias.WasteMult, 0.5, 2.0)
            break
        }
    }
}
```

#### Complete Maximal Flow

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    MAXIMAL ADAPTIVE RETRIEVAL FLOW                                   │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  1. QUERY/TOOL-CALL ARRIVES                                                         │
│     │                                                                              │
│     ├──────────────────────────────────────────────────────────┐                   │
│     │                                                          │                   │
│     ▼                                                          ▼                   │
│  2a. START SPECULATIVE PRE-FETCH (parallel)          2b. COMPUTE EMBEDDING         │
│      • Returns PrefetchFuture immediately                 • Needed for VectorDB    │
│      • Races against need                                 • Used for context       │
│     │                                                          │                   │
│     └──────────────────────────┬───────────────────────────────┘                   │
│                                │                                                    │
│                                ▼                                                    │
│  3. CLASSIFY CONTEXT (embedding-based)                                              │
│     • Check keyword cache first (< 1μs)                                            │
│     • Find nearest centroid if cache miss                                          │
│     • Create new context cluster if novel                                          │
│     │                                                                              │
│     ▼                                                                              │
│  4. SAMPLE PARAMETERS (Thompson Sampling)                                          │
│     • Draw weights from robust distributions                                       │
│     • Draw thresholds from robust distributions                                    │
│     • Apply user profile adjustments                                               │
│     • Apply discovered context bias                                                │
│     │                                                                              │
│     ▼                                                                              │
│  5. TIERED SEARCH (within latency budget)                                          │
│     • Tier 0: Hot cache (< 1ms) - always                                           │
│     • Tier 1: Warm index (< 10ms) - if time permits                                │
│     • Tier 2: Full Bleve + VectorDB (< 200ms) - if time permits                   │
│     • Circuit breakers prevent hammering failed backends                           │
│     • Graceful degradation if backends fail                                        │
│     │                                                                              │
│     ▼                                                                              │
│  6. GET PRE-FETCH RESULT                                                           │
│     • future.GetIfReady(10ms) - don't block                                        │
│     • Use if ready, proceed without if not                                         │
│     │                                                                              │
│     ▼                                                                              │
│  7. EXECUTE (LLM call or tool execution)                                           │
│     │                                                                              │
│     ▼                                                                              │
│  8. OBSERVE OUTCOME (implicit signals)                                             │
│     • Task completed?                                                              │
│     • Follow-ups, edits, hedging?                                                  │
│     • Searches after prefetch?                                                     │
│     │                                                                              │
│     ▼                                                                              │
│  9. WRITE-AHEAD LOG (durable, non-blocking)                                        │
│     • Append observation to log (fast sequential write)                            │
│     • Queue for async processing                                                   │
│     │                                                                              │
│     ▼                                                                              │
│  10. ASYNC: ROBUST BAYESIAN UPDATE                                                  │
│      • Outlier rejection (> 3σ ignored)                                            │
│      • Exponential decay (recent observations weighted more)                       │
│      • Cold start protection (minimum effective samples)                           │
│      • Prior drift (adapts to changing preferences)                                │
│      │                                                                              │
│      ▼                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  OVER TIME: Parameters converge to user-optimal configuration               │   │
│  │  • Contexts discovered from query patterns                                  │   │
│  │  • User preferences learned from behavior                                   │   │
│  │  • System adapts to non-stationarity via prior drift                        │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  ON STARTUP: Load persisted state (< 1KB, instant)                                 │
│  ON CRASH: Recover from WAL (replay unprocessed observations)                      │
│  PERIODICALLY: Persist state to disk (background)                                   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Key Properties

| Property | Description |
|----------|-------------|
| **Speculative Pre-fetch** | Always start, race against need - 0ms added latency |
| **Tiered Search** | Hot (<1ms) → Warm (<10ms) → Full (<200ms) with budget |
| **Circuit Breakers** | Graceful degradation when backends fail |
| **Robust Bayesian** | Outlier rejection, decay, drift, cold start protection |
| **Context Discovery** | Embedding-based clustering - contexts emerge, not predefined |
| **Write-Ahead Log** | Observations survive crashes, async processing |
| **Sufficient Statistics** | O(contexts) memory, not O(observations) |
| **Instant Startup** | Load ~1KB state file, no history replay |
| **Quality-First** | Task success primary signal; efficiency secondary |
| **Non-Stationary** | Prior drift handles changing user preferences |

### Integration with WAVE 4 Robustness Systems

The adaptive retrieval system MUST integrate with the existing robustness architecture (WAVE 4) to prevent goroutine leaks, memory leaks, and ensure proper lifecycle management.

#### 1. Goroutine Budget Integration

All speculative prefetch goroutines must use `GoroutineScope.Go()`, not raw `go` statements.

```go
// core/context/speculative_prefetch.go

type SpeculativePrefetcher struct {
    searcher  *TieredSearcher
    hotCache  *HotCache

    // REQUIRED: Integration with goroutine management
    scope     *concurrency.GoroutineScope  // Per-agent scope
    budget    *concurrency.GoroutineBudget
    tracker   *concurrency.ResourceTracker

    inflight  sync.Map  // queryHash → *TrackedPrefetchFuture
}

type TrackedPrefetchFuture struct {
    result    atomic.Pointer[AugmentedQuery]
    done      chan struct{}
    started   time.Time

    // REQUIRED: Operation tracking
    operation *concurrency.Operation
}

// StartSpeculative uses GoroutineScope, not raw go
func (p *SpeculativePrefetcher) StartSpeculative(ctx context.Context, query string) (*TrackedPrefetchFuture, error) {
    // Create tracked operation
    op := concurrency.NewOperation(
        ctx,
        concurrency.OpTypeSearch,      // New operation type for prefetch
        p.scope.AgentID(),
        fmt.Sprintf("prefetch:%s", query[:min(50, len(query))]),
        200*time.Millisecond,          // Timeout matches search budget
    )

    future := &TrackedPrefetchFuture{
        done:      make(chan struct{}),
        started:   time.Now(),
        operation: op,
    }

    // Use scope.Go() - tracked, budget-aware, cancellable
    err := p.scope.Go("speculative-prefetch", 200*time.Millisecond, func(ctx context.Context) error {
        defer close(future.done)
        defer op.MarkDone()

        result := p.searcher.SearchWithBudget(ctx, query, 200*time.Millisecond)
        future.result.Store(result.ToAugmentedQuery())

        op.SetResult(result, nil)
        return nil
    })

    if err != nil {
        // Budget exhausted or context cancelled - proceed without prefetch
        return nil, err
    }

    // Track the operation
    p.tracker.Track(op, op.ID)

    return future, nil
}
```

#### 2. SafeChan for Async Updates

The observation log must use `SafeChan` for context-aware, leak-proof channel operations.

```go
// core/context/observation_log.go

import "core/concurrency/safechan"

type ObservationLog struct {
    file      *os.File
    encoder   *json.Encoder
    mu        sync.Mutex

    // REQUIRED: Use SafeChan instead of raw channel
    updateChan *safechan.SafeChan[EpisodeObservation]
    adaptive   *AdaptiveState

    // REQUIRED: Goroutine scope for background processor
    scope      *concurrency.GoroutineScope
}

func NewObservationLog(ctx context.Context, path string, adaptive *AdaptiveState, scope *concurrency.GoroutineScope) (*ObservationLog, error) {
    file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }

    log := &ObservationLog{
        file:       file,
        encoder:    json.NewEncoder(file),
        updateChan: safechan.New[EpisodeObservation](100),  // SafeChan with buffer
        adaptive:   adaptive,
        scope:      scope,
    }

    // Start background processor via scope.Go()
    err = scope.Go("observation-processor", 0, log.processLoop)
    if err != nil {
        file.Close()
        return nil, err
    }

    return log, nil
}

func (l *ObservationLog) Record(ctx context.Context, obs EpisodeObservation) error {
    l.mu.Lock()
    defer l.mu.Unlock()

    obs.Timestamp = time.Now()
    if err := l.encoder.Encode(obs); err != nil {
        return err
    }

    // Non-blocking send via SafeChan (respects context cancellation)
    l.updateChan.TrySend(ctx, obs)  // Fire-and-forget

    return l.file.Sync()
}

func (l *ObservationLog) processLoop(ctx context.Context) error {
    for {
        obs, ok := l.updateChan.Receive(ctx)
        if !ok {
            return nil  // Channel closed or context cancelled
        }
        l.adaptive.UpdateFromOutcome(obs)
    }
}

func (l *ObservationLog) Close() error {
    l.updateChan.Close()  // SafeChan handles pending messages gracefully
    return l.file.Close()
}
```

#### 3. Global Circuit Breaker Registry Integration

Use the existing `GlobalCircuitBreakerRegistry` instead of creating new circuit breakers.

```go
// core/context/tiered_search.go

type TieredSearcher struct {
    hotCache   *HotCache
    warmIndex  *BleveIndex
    fullBleve  *BleveIndex
    vectorDB   *vectorgraphdb.VectorGraphDB

    // REQUIRED: Use global registry, not local circuit breakers
    cbRegistry *llm.GlobalCircuitBreakerRegistry
}

func NewTieredSearcher(
    hotCache *HotCache,
    warmIndex, fullBleve *BleveIndex,
    vectorDB *vectorgraphdb.VectorGraphDB,
    cbRegistry *llm.GlobalCircuitBreakerRegistry,
) *TieredSearcher {
    // Register circuit breakers for search backends
    cbRegistry.Register("bleve-search", &llm.CircuitBreakerConfig{
        FailureThreshold: 5,
        ResetTimeout:     30 * time.Second,
        HalfOpenMax:      2,
    })
    cbRegistry.Register("vector-search", &llm.CircuitBreakerConfig{
        FailureThreshold: 5,
        ResetTimeout:     30 * time.Second,
        HalfOpenMax:      2,
    })

    return &TieredSearcher{
        hotCache:   hotCache,
        warmIndex:  warmIndex,
        fullBleve:  fullBleve,
        vectorDB:   vectorDB,
        cbRegistry: cbRegistry,
    }
}

func (t *TieredSearcher) executeFullSearch(ctx context.Context, query string, budget time.Duration, results *SearchResults) {
    fullCtx, cancel := context.WithTimeout(ctx, budget)
    defer cancel()

    var wg sync.WaitGroup
    var mu sync.Mutex

    // Use global circuit breaker registry
    if t.cbRegistry.Allow("bleve-search") {
        wg.Add(1)
        go func() {  // NOTE: This go should also use scope.Go in full implementation
            defer wg.Done()
            if r, err := t.fullBleve.SearchWithContext(fullCtx, query, 10); err == nil {
                t.cbRegistry.RecordSuccess("bleve-search")
                mu.Lock()
                results.Merge(r)
                mu.Unlock()
            } else {
                t.cbRegistry.RecordFailure("bleve-search")
            }
        }()
    }

    if t.cbRegistry.Allow("vector-search") {
        wg.Add(1)
        go func() {
            defer wg.Done()
            embedding, _ := t.vectorDB.Embed(query)
            if r, err := t.vectorDB.SearchWithContext(fullCtx, embedding, 10); err == nil {
                t.cbRegistry.RecordSuccess("vector-search")
                mu.Lock()
                results.Merge(r)
                mu.Unlock()
            } else {
                t.cbRegistry.RecordFailure("vector-search")
            }
        }()
    }

    wg.Wait()
}
```

#### 4. Memory Pressure Response

The adaptive system must respond to memory pressure levels from `PressureController`.

```go
// core/context/pressure_aware_retrieval.go

type PressureAwareRetrieval struct {
    prefetcher *SpeculativePrefetcher
    searcher   *TieredSearcher
    adaptive   *AdaptiveState
    hotCache   *HotCache

    // REQUIRED: Pressure controller integration
    pressure   *resources.PressureController
}

// OnPressureChange is called by PressureController when state changes
func (p *PressureAwareRetrieval) OnPressureChange(level resources.PressureLevel) {
    switch level {
    case resources.PressureNormal:
        // Full capacity
        p.prefetcher.SetEnabled(true)
        p.searcher.SetMaxTier(TierFullSearch)
        p.hotCache.SetMaxSize(p.hotCache.DefaultMaxSize())

    case resources.PressureElevated:
        // Reduce prefetch aggressiveness
        p.prefetcher.SetEnabled(true)
        p.searcher.SetMaxTier(TierWarmIndex)  // Skip full search
        p.hotCache.SetMaxSize(p.hotCache.DefaultMaxSize() * 75 / 100)

    case resources.PressureHigh:
        // Disable speculative prefetch, hot cache only
        p.prefetcher.SetEnabled(false)
        p.searcher.SetMaxTier(TierHotCache)
        p.hotCache.SetMaxSize(p.hotCache.DefaultMaxSize() * 50 / 100)

    case resources.PressureCritical:
        // Emergency: disable all prefetch, minimal hot cache
        p.prefetcher.SetEnabled(false)
        p.searcher.SetMaxTier(TierHotCache)
        p.hotCache.SetMaxSize(p.hotCache.DefaultMaxSize() * 25 / 100)
        p.hotCache.EvictOldest(50)  // Evict 50% immediately
    }
}

// Register with PressureController
func (p *PressureAwareRetrieval) Register(pc *resources.PressureController) {
    pc.RegisterCallback(p.OnPressureChange)

    // Register hot cache as evictable
    pc.RegisterEvictableCache("adaptive-hot-cache", p.hotCache)
}
```

#### 5. File Handle Budget Integration

Bleve indexes and WAL files must register with `FileHandleBudget`.

```go
// core/context/retrieval_resources.go

type RetrievalResources struct {
    bleveIndex   *BleveIndex
    vectorDB     *vectorgraphdb.VectorGraphDB
    walFile      *os.File
    stateFile    *os.File

    // REQUIRED: File handle tracking
    fileBudget   *resources.FileHandleBudget
    agentBudget  *resources.AgentFileBudget
    handles      []resources.TrackedFileHandle
}

func NewRetrievalResources(
    sessionID, agentID string,
    fileBudget *resources.FileHandleBudget,
    paths RetrievalPaths,
) (*RetrievalResources, error) {
    // Get agent-level file budget
    agentBudget, err := fileBudget.GetAgentBudget(sessionID, agentID)
    if err != nil {
        return nil, err
    }

    r := &RetrievalResources{
        fileBudget:  fileBudget,
        agentBudget: agentBudget,
    }

    // Acquire file handles through budget (blocks if exhausted, never errors)

    // WAL file handle
    walHandle, err := agentBudget.Acquire("wal", paths.WAL)
    if err != nil {
        return nil, err
    }
    r.handles = append(r.handles, walHandle)
    r.walFile = walHandle.File()

    // State file handle
    stateHandle, err := agentBudget.Acquire("state", paths.State)
    if err != nil {
        r.Close()
        return nil, err
    }
    r.handles = append(r.handles, stateHandle)
    r.stateFile = stateHandle.File()

    // Bleve index (multiple internal handles)
    bleveHandles, err := agentBudget.AcquireMultiple("bleve", 5)  // Bleve uses ~5 FDs
    if err != nil {
        r.Close()
        return nil, err
    }
    r.handles = append(r.handles, bleveHandles...)

    // VectorDB handles
    vectorHandles, err := agentBudget.AcquireMultiple("vectordb", 3)
    if err != nil {
        r.Close()
        return nil, err
    }
    r.handles = append(r.handles, vectorHandles...)

    return r, nil
}

func (r *RetrievalResources) Close() error {
    var errs []error

    // Release all tracked handles
    for _, h := range r.handles {
        if err := r.agentBudget.Release(h); err != nil {
            errs = append(errs, err)
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("errors releasing handles: %v", errs)
    }
    return nil
}
```

#### 6. HotCache as EvictableCache

The hot cache must implement `EvictableCache` interface for pressure-driven eviction.

```go
// core/context/hot_cache.go

// HotCache implements resources.EvictableCache
type HotCache struct {
    entries     sync.Map  // contentID → *HotEntry
    maxSize     atomic.Int64
    currentSize atomic.Int64
    defaultMax  int64

    // LRU tracking
    accessOrder *list.List
    accessMap   map[string]*list.Element
    mu          sync.Mutex
}

// Implement EvictableCache interface
func (c *HotCache) Name() string {
    return "adaptive-hot-cache"
}

func (c *HotCache) CurrentSize() int64 {
    return c.currentSize.Load()
}

func (c *HotCache) Evict(percent float64) int64 {
    c.mu.Lock()
    defer c.mu.Unlock()

    target := int64(float64(c.currentSize.Load()) * percent)
    evicted := int64(0)

    // Evict from oldest (LRU)
    for evicted < target && c.accessOrder.Len() > 0 {
        oldest := c.accessOrder.Back()
        if oldest == nil {
            break
        }

        entry := oldest.Value.(*HotEntry)
        evicted += entry.Size

        c.entries.Delete(entry.ID)
        c.accessOrder.Remove(oldest)
        delete(c.accessMap, entry.ID)
        c.currentSize.Add(-entry.Size)
    }

    return evicted
}

func (c *HotCache) SetMaxSize(size int64) {
    c.maxSize.Store(size)

    // Evict if over new limit
    if c.currentSize.Load() > size {
        excess := float64(c.currentSize.Load()-size) / float64(c.currentSize.Load())
        c.Evict(excess)
    }
}

func (c *HotCache) DefaultMaxSize() int64 {
    return c.defaultMax
}
```

#### 7. Complete Integration Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    ADAPTIVE RETRIEVAL + WAVE 4 INTEGRATION                           │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                         EXISTING WAVE 4 SYSTEMS                              │   │
│  ├─────────────────────────────────────────────────────────────────────────────┤   │
│  │  GoroutineBudget ◄──── Speculative prefetch uses scope.Go()                 │   │
│  │  GoroutineScope  ◄──── All goroutines tracked, cancellable                  │   │
│  │  ResourceTracker ◄──── PrefetchFuture operations tracked                    │   │
│  │  SafeChan        ◄──── ObservationLog update channel                        │   │
│  │  FileHandleBudget◄──── Bleve, VectorDB, WAL file handles                    │   │
│  │  GlobalCBRegistry◄──── bleve-search, vector-search circuit breakers         │   │
│  │  PressureController◄── Pressure callbacks adjust prefetch behavior          │   │
│  │  EvictableCache  ◄──── HotCache registered for pressure-driven eviction     │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                      │                                              │
│                                      ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                         ADAPTIVE RETRIEVAL COMPONENTS                        │   │
│  ├─────────────────────────────────────────────────────────────────────────────┤   │
│  │                                                                             │   │
│  │  SpeculativePrefetcher                                                      │   │
│  │  ├── Uses GoroutineScope.Go() for all goroutines                           │   │
│  │  ├── Creates tracked Operations with timeout                                │   │
│  │  ├── Disabled at PressureHigh+                                              │   │
│  │  └── Resources tracked in ResourceTracker                                   │   │
│  │                                                                             │   │
│  │  TieredSearcher                                                             │   │
│  │  ├── Uses GlobalCircuitBreakerRegistry                                      │   │
│  │  ├── Max tier reduced under pressure                                        │   │
│  │  └── File handles from FileHandleBudget                                     │   │
│  │                                                                             │   │
│  │  HotCache                                                                   │   │
│  │  ├── Implements EvictableCache interface                                    │   │
│  │  ├── Registered with PressureController                                     │   │
│  │  └── Size reduced under pressure                                            │   │
│  │                                                                             │   │
│  │  ObservationLog                                                             │   │
│  │  ├── Uses SafeChan for update channel                                       │   │
│  │  ├── Background processor via scope.Go()                                    │   │
│  │  └── File handle from FileHandleBudget                                      │   │
│  │                                                                             │   │
│  │  AdaptiveState                                                              │   │
│  │  └── Persisted via existing WAL patterns                                    │   │
│  │                                                                             │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  PRESSURE RESPONSE MATRIX:                                                          │
│  ─────────────────────────                                                          │
│  NORMAL:    Full prefetch, all tiers, 100% hot cache                               │
│  ELEVATED:  Prefetch enabled, skip full search tier, 75% hot cache                 │
│  HIGH:      Prefetch DISABLED, hot cache only, 50% cache size                      │
│  CRITICAL:  Prefetch DISABLED, hot cache only, 25% cache + immediate eviction      │
│                                                                                     │
│  LEAK PREVENTION:                                                                   │
│  ────────────────                                                                   │
│  ✓ No raw `go` statements (linter enforced)                                        │
│  ✓ All channels via SafeChan (context-aware close)                                 │
│  ✓ All file handles via FileHandleBudget (tracked, auto-cleanup)                   │
│  ✓ All operations via ResourceTracker (orphan tracking)                            │
│  ✓ Goroutine budget enforced (soft/hard limits per agent)                          │
│  ✓ Circuit breakers prevent runaway failures                                       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Pipeline Agent Handoff

Pipeline agents (Engineer, Designer, Inspector, Tester) use **agent handoff** instead of eviction. When an agent hits 75% context, a new instance of that agent is created within the same pipeline.

### Handoff Architecture

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    PIPELINE AGENT HANDOFF (Agent @ 75%)                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  PIPELINE (persists throughout)                                                    │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                             │   │
│  │   ┌──────────┐      ┌──────────┐      ┌──────────┐                         │   │
│  │   │ Engineer │      │ Inspector│      │  Tester  │                         │   │
│  │   │  @ 75%   │      │          │      │          │                         │   │
│  │   └────┬─────┘      └──────────┘      └──────────┘                         │   │
│  │        │                                                                    │   │
│  │        │ HANDOFF SEQUENCE:                                                  │   │
│  │        │ 1. Build EngineerHandoffState                                      │   │
│  │        │ 2. Store state in ContentStore (for audit/recovery)                │   │
│  │        │ 3. Pipeline spawns new Engineer instance                           │   │
│  │        │ 4. Inject handoff state as initial context                         │   │
│  │        │ 5. Old Engineer instance terminates                                │   │
│  │        ▼                                                                    │   │
│  │   ┌──────────┐      ┌──────────┐      ┌──────────┐                         │   │
│  │   │ Engineer │      │ Inspector│      │  Tester  │                         │   │
│  │   │  (new)   │      │ (same)   │      │ (same)   │                         │   │
│  │   │  ~20%    │      │          │      │          │                         │   │
│  │   └──────────┘      └──────────┘      └──────────┘                         │   │
│  │                                                                             │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  KEY POINTS:                                                                       │
│  • Pipeline ID unchanged - same pipeline continues                                 │
│  • Other agents unaffected - only the agent at 75% gets replaced                   │
│  • Handoff state is agent-specific (not bundled with other agents)                 │
│  • New agent starts with full task context via handoff state                       │
│  • Handoff can chain (HandoffIndex tracks: 1, 2, 3...)                            │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Orchestrator Handoff

The Orchestrator is a special case - it coordinates all pipelines and agents within a session. When it hits 75% context, it performs a handoff to a new Orchestrator instance.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                       ORCHESTRATOR HANDOFF (@ 75%)                                   │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  SESSION (persists)                                                                │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                             │   │
│  │   ┌──────────────────────────────────────────────────────────────────┐     │   │
│  │   │                    ORCHESTRATOR @ 75%                             │     │   │
│  │   │                                                                   │     │   │
│  │   │   Current Workflow: "Implement user authentication"               │     │   │
│  │   │   Active Pipelines: [P1: Engineer, P2: Tester]                   │     │   │
│  │   │   Pending Tasks: ["Add password hashing", "Create tests"]        │     │   │
│  │   │   Completed: ["Set up routes", "Create user model"]              │     │   │
│  │   │                                                                   │     │   │
│  │   └────────────────────────────┬─────────────────────────────────────┘     │   │
│  │                                │                                            │   │
│  │                                │ HANDOFF:                                   │   │
│  │                                │ 1. Build OrchestratorHandoffState         │   │
│  │                                │    - Current workflow + phase              │   │
│  │                                │    - All active pipeline states            │   │
│  │                                │    - Pending & completed tasks             │   │
│  │                                │    - Key decisions & blockers              │   │
│  │                                │ 2. Store in ContentStore                   │   │
│  │                                │ 3. Spawn new Orchestrator                  │   │
│  │                                │ 4. Inject handoff state                    │   │
│  │                                │ 5. Old Orchestrator terminates             │   │
│  │                                ▼                                            │   │
│  │   ┌──────────────────────────────────────────────────────────────────┐     │   │
│  │   │                 ORCHESTRATOR (new) ~20%                           │     │   │
│  │   │                                                                   │     │   │
│  │   │   "Continuing workflow: Implement user authentication"            │     │   │
│  │   │   "Active pipelines: P1 (Engineer), P2 (Tester)"                 │     │   │
│  │   │   "Next: Add password hashing, Create tests"                      │     │   │
│  │   │                                                                   │     │   │
│  │   │   [Has full context of what was done and what remains]           │     │   │
│  │   │                                                                   │     │   │
│  │   └──────────────────────────────────────────────────────────────────┘     │   │
│  │                                                                             │   │
│  │   ┌────────────────┐   ┌────────────────┐                                  │   │
│  │   │   Pipeline 1   │   │   Pipeline 2   │     (unchanged, continue)       │   │
│  │   │   [Engineer]   │   │   [Tester]     │                                  │   │
│  │   └────────────────┘   └────────────────┘                                  │   │
│  │                                                                             │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  ORCHESTRATOR HANDOFF STATE INCLUDES:                                              │
│  • Original goal (user's initial request)                                          │
│  • Current workflow state (phase, progress)                                        │
│  • All active pipelines and their status                                           │
│  • All agents and their context usage                                              │
│  • Pending tasks still to dispatch                                                 │
│  • Completed tasks for progress tracking                                           │
│  • Key decisions made (so new orchestrator doesn't re-decide)                      │
│  • Current blockers and what we're waiting on                                      │
│  • Next planned actions                                                            │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Guide Handoff

The Guide is the user-facing router that handles all user interactions. When it hits 75% context, it hands off conversation state and routing context to a new Guide instance.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                           GUIDE HANDOFF (@ 75%)                                      │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  SESSION (persists)                                                                │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                             │   │
│  │   ┌──────────────────────────────────────────────────────────────────┐     │   │
│  │   │                       GUIDE @ 75%                                 │     │   │
│  │   │                                                                   │     │   │
│  │   │   Current Intent: "Implement authentication system"               │     │   │
│  │   │   Active Topics: [auth, JWT, middleware, security]               │     │   │
│  │   │   Recent Routings: Engineer(3), Academic(1), Librarian(2)        │     │   │
│  │   │   User Preferences: verbose, expert-level                         │     │   │
│  │   │                                                                   │     │   │
│  │   └────────────────────────────┬─────────────────────────────────────┘     │   │
│  │                                │                                            │   │
│  │                                │ HANDOFF:                                   │   │
│  │                                │ 1. Build GuideHandoffState                │   │
│  │                                │    - Conversation history summary          │   │
│  │                                │    - Current user intent                   │   │
│  │                                │    - Active topics                         │   │
│  │                                │    - Routing decisions & affinities        │   │
│  │                                │    - User preferences learned              │   │
│  │                                │ 2. Store in ContentStore                   │   │
│  │                                │ 3. Spawn new Guide                         │   │
│  │                                │ 4. Inject handoff state                    │   │
│  │                                │ 5. Old Guide terminates                    │   │
│  │                                ▼                                            │   │
│  │   ┌──────────────────────────────────────────────────────────────────┐     │   │
│  │   │                    GUIDE (new) ~20%                               │     │   │
│  │   │                                                                   │     │   │
│  │   │   "Continuing: User implementing authentication system"           │     │   │
│  │   │   "User prefers: verbose explanations, expert-level"              │     │   │
│  │   │   "Recent focus: Engineer for implementation tasks"               │     │   │
│  │   │                                                                   │     │   │
│  │   │   [Knows user's style, current task, routing history]            │     │   │
│  │   │                                                                   │     │   │
│  │   └──────────────────────────────────────────────────────────────────┘     │   │
│  │                                                                             │   │
│  │         USER ◄──────────────────────────────────► NEW GUIDE                │   │
│  │              (seamless continuation)                                        │   │
│  │                                                                             │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  GUIDE HANDOFF STATE INCLUDES:                                                     │
│  • Conversation history (summarized recent turns)                                  │
│  • Current user intent (what they're trying to accomplish)                         │
│  • Active topics being discussed                                                   │
│  • Recent routing decisions (which agents for which queries)                       │
│  • Agent affinities (learned: "user prefers Engineer for X")                       │
│  • User preferences (verbosity, code style, explanation level)                     │
│  • Pending follow-up questions                                                     │
│  • Key context and assumptions made                                                │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Handoff State Structures

Each pipeline agent has its own handoff state structure:

```go
// core/pipeline/handoff.go

// Engineer handoff state
type EngineerHandoffState struct {
    // Task context
    OriginalPrompt  string            `json:"original_prompt"`   // Verbatim original task
    TaskID          string            `json:"task_id"`
    PipelineID      string            `json:"pipeline_id"`

    // Progress
    Accomplished    []string          `json:"accomplished"`      // What was completed
    FilesChanged    []FileChange      `json:"files_changed"`     // Specific changes made
    Remaining       []string          `json:"remaining"`         // TODOs still to do

    // Context
    ContextNotes    string            `json:"context_notes"`     // Critical context to preserve
    KeyDecisions    []string          `json:"key_decisions"`     // Important decisions made
    BlockersHit     []string          `json:"blockers_hit"`      // Problems encountered

    // Handoff metadata
    HandoffIndex    int               `json:"handoff_index"`     // 1, 2, 3... for chaining
    HandoffReason   string            `json:"handoff_reason"`    // "context_75%"
    Timestamp       time.Time         `json:"timestamp"`
}

// Inspector handoff state
type InspectorHandoffState struct {
    // Task context
    TaskID          string            `json:"task_id"`
    PipelineID      string            `json:"pipeline_id"`

    // Validation state
    ChecksPerformed []string          `json:"checks_performed"`  // What was validated
    IssuesFound     []Issue           `json:"issues_found"`      // All issues discovered
    FixesApplied    []Fix             `json:"fixes_applied"`     // Issues that were fixed
    PendingIssues   []Issue           `json:"pending_issues"`    // Issues still open
    ValidationState map[string]bool   `json:"validation_state"`  // Category → pass/fail

    // Handoff metadata
    HandoffIndex    int               `json:"handoff_index"`
    HandoffReason   string            `json:"handoff_reason"`
    Timestamp       time.Time         `json:"timestamp"`
}

// Tester handoff state
type TesterHandoffState struct {
    // Task context
    TaskID          string            `json:"task_id"`
    PipelineID      string            `json:"pipeline_id"`

    // Test state
    TestsCreated    []TestInfo        `json:"tests_created"`     // Tests written
    TestResults     []TestResult      `json:"test_results"`      // Pass/fail results
    FailingTests    []FailureDesc     `json:"failing_tests"`     // Current failures
    CoverageGaps    []string          `json:"coverage_gaps"`     // What needs tests

    // Handoff metadata
    HandoffIndex    int               `json:"handoff_index"`
    HandoffReason   string            `json:"handoff_reason"`
    Timestamp       time.Time         `json:"timestamp"`
}

// Designer handoff state
type DesignerHandoffState struct {
    // Task context
    OriginalPrompt  string            `json:"original_prompt"`
    TaskID          string            `json:"task_id"`
    PipelineID      string            `json:"pipeline_id"`

    // Progress
    Accomplished    []string          `json:"accomplished"`
    ComponentsCreated []string        `json:"components_created"`
    StylesApplied   []string          `json:"styles_applied"`
    Remaining       []string          `json:"remaining"`

    // Context
    DesignDecisions []string          `json:"design_decisions"`
    TokensUsed      []string          `json:"tokens_used"`       // Design tokens referenced
    A11yConsiderations []string       `json:"a11y_considerations"`

    // Handoff metadata
    HandoffIndex    int               `json:"handoff_index"`
    HandoffReason   string            `json:"handoff_reason"`
    Timestamp       time.Time         `json:"timestamp"`
}

// Orchestrator handoff state
type OrchestratorHandoffState struct {
    // Session context
    SessionID       string            `json:"session_id"`
    OriginalGoal    string            `json:"original_goal"`     // User's original request

    // Current workflow state
    CurrentWorkflow *WorkflowState    `json:"current_workflow"`  // Active workflow being managed
    PendingTasks    []OrchestratorTask `json:"pending_tasks"`    // Tasks not yet dispatched
    ActivePipelines []PipelineInfo    `json:"active_pipelines"` // Currently running pipelines
    CompletedTasks  []OrchestratorTask `json:"completed_tasks"` // Finished work

    // Coordination state
    AgentStates     map[string]AgentStateSnapshot `json:"agent_states"` // State of each agent
    WaitingOn       []WaitCondition   `json:"waiting_on"`        // What we're blocked on
    NextActions     []PlannedAction   `json:"next_actions"`      // What to do next

    // Context notes
    KeyDecisions    []string          `json:"key_decisions"`     // Important decisions made
    Blockers        []string          `json:"blockers"`          // Current blockers
    ContextNotes    string            `json:"context_notes"`     // Critical context to preserve

    // Handoff metadata
    HandoffIndex    int               `json:"handoff_index"`
    HandoffReason   string            `json:"handoff_reason"`
    Timestamp       time.Time         `json:"timestamp"`
}

// Guide handoff state
type GuideHandoffState struct {
    // Session context
    SessionID       string            `json:"session_id"`
    UserID          string            `json:"user_id,omitempty"`

    // Conversation state
    ConversationHistory []ConversationTurn `json:"conversation_history"` // Recent turns summary
    CurrentIntent   string            `json:"current_intent"`    // What user is trying to accomplish
    ActiveTopics    []string          `json:"active_topics"`     // Topics being discussed

    // Routing state
    RecentRoutings  []RoutingDecision `json:"recent_routings"`   // Recent agent routing decisions
    AgentAffinities map[string]float64 `json:"agent_affinities"` // Which agents for which topics
    PendingFollowups []string         `json:"pending_followups"` // Questions to ask user

    // User preferences learned
    UserPreferences *UserPreferences  `json:"user_preferences,omitempty"`

    // Active context
    ActiveAgents    []string          `json:"active_agents"`     // Currently engaged agents
    WaitingFor      string            `json:"waiting_for,omitempty"` // What we're waiting on

    // Context notes
    KeyContext      string            `json:"key_context"`       // Critical context to preserve
    Assumptions     []string          `json:"assumptions"`       // Assumptions made about user intent

    // Handoff metadata
    HandoffIndex    int               `json:"handoff_index"`
    HandoffReason   string            `json:"handoff_reason"`
    Timestamp       time.Time         `json:"timestamp"`
}

type ConversationTurn struct {
    TurnNumber      int               `json:"turn_number"`
    Role            string            `json:"role"`              // "user" or "assistant"
    Summary         string            `json:"summary"`           // Brief summary of turn
    Intent          string            `json:"intent,omitempty"`  // Detected intent
    RoutedTo        []string          `json:"routed_to,omitempty"` // Agents involved
}

type RoutingDecision struct {
    TurnNumber      int               `json:"turn_number"`
    UserQuery       string            `json:"user_query"`        // Abbreviated query
    SelectedAgent   string            `json:"selected_agent"`
    Confidence      float64           `json:"confidence"`
    Reasoning       string            `json:"reasoning,omitempty"`
}

type UserPreferences struct {
    Verbosity       string            `json:"verbosity"`         // "concise", "detailed", "verbose"
    CodeStyle       string            `json:"code_style,omitempty"`
    ExplanationLevel string           `json:"explanation_level"` // "beginner", "intermediate", "expert"
    PreferredAgents []string          `json:"preferred_agents,omitempty"`
}

type WorkflowState struct {
    ID              string            `json:"id"`
    Name            string            `json:"name"`
    Phase           string            `json:"phase"`             // "planning", "executing", "validating", "completing"
    Progress        float64           `json:"progress"`          // 0.0 - 1.0
    StartTime       time.Time         `json:"start_time"`
}

type OrchestratorTask struct {
    ID              string            `json:"id"`
    Description     string            `json:"description"`
    AssignedTo      string            `json:"assigned_to,omitempty"` // Agent type
    PipelineID      string            `json:"pipeline_id,omitempty"`
    Status          string            `json:"status"`            // "pending", "in_progress", "completed", "blocked"
    Dependencies    []string          `json:"dependencies,omitempty"`
}

type PipelineInfo struct {
    ID              string            `json:"id"`
    TaskID          string            `json:"task_id"`
    Status          string            `json:"status"`
    ActiveAgents    []string          `json:"active_agents"`
}

type AgentStateSnapshot struct {
    AgentID         string            `json:"agent_id"`
    AgentType       string            `json:"agent_type"`
    ContextUsage    float64           `json:"context_usage"`
    LastActivity    time.Time         `json:"last_activity"`
    CurrentTask     string            `json:"current_task,omitempty"`
}

type WaitCondition struct {
    Type            string            `json:"type"`              // "pipeline_complete", "agent_response", "user_input"
    Target          string            `json:"target"`            // Pipeline ID, Agent ID, etc.
    Description     string            `json:"description"`
}

type PlannedAction struct {
    Action          string            `json:"action"`            // "dispatch_task", "spawn_pipeline", "request_info"
    Target          string            `json:"target"`
    Details         map[string]any    `json:"details,omitempty"`
}

type FileChange struct {
    Path        string `json:"path"`
    Action      string `json:"action"` // "create", "modify", "delete"
    Description string `json:"description"`
    LinesChanged int   `json:"lines_changed,omitempty"`
}

type Issue struct {
    ID          string `json:"id"`
    Severity    string `json:"severity"` // "critical", "major", "minor"
    Category    string `json:"category"` // "security", "performance", "style", etc.
    File        string `json:"file"`
    Line        int    `json:"line,omitempty"`
    Description string `json:"description"`
    Suggestion  string `json:"suggestion,omitempty"`
}

type Fix struct {
    IssueID     string `json:"issue_id"`
    Description string `json:"description"`
    Applied     bool   `json:"applied"`
}

type TestInfo struct {
    File     string `json:"file"`
    TestName string `json:"test_name"`
    Type     string `json:"type"` // "unit", "integration", "e2e"
    ForTask  string `json:"for_task,omitempty"`
}

type TestResult struct {
    TestName string `json:"test_name"`
    Status   string `json:"status"` // "pass", "fail", "skip"
    Duration string `json:"duration,omitempty"`
    Error    string `json:"error,omitempty"`
}

type FailureDesc struct {
    TestName   string `json:"test_name"`
    Error      string `json:"error"`
    Expected   string `json:"expected,omitempty"`
    Actual     string `json:"actual,omitempty"`
    Suggestion string `json:"suggestion,omitempty"`
}
```

### Handoff Manager

```go
// core/pipeline/handoff_manager.go

type HandoffManager struct {
    contentStore *UniversalContentStore
}

func NewHandoffManager(store *UniversalContentStore) *HandoffManager {
    return &HandoffManager{contentStore: store}
}

// Trigger handoff for a pipeline agent
func (m *HandoffManager) TriggerHandoff(agent PipelineAgent, pipeline *Pipeline) error {
    // 1. Build handoff state based on agent type
    var state any
    var stateJSON []byte
    var err error

    switch a := agent.(type) {
    case *Engineer:
        state = a.BuildHandoffState()
        stateJSON, err = json.Marshal(state)
    case *Inspector:
        state = a.BuildHandoffState()
        stateJSON, err = json.Marshal(state)
    case *Tester:
        state = a.BuildHandoffState()
        stateJSON, err = json.Marshal(state)
    case *Designer:
        state = a.BuildHandoffState()
        stateJSON, err = json.Marshal(state)
    case *Orchestrator:
        state = a.BuildHandoffState()
        stateJSON, err = json.Marshal(state)
    case *Guide:
        state = a.BuildHandoffState()
        stateJSON, err = json.Marshal(state)
    default:
        return fmt.Errorf("unknown agent type for handoff")
    }

    if err != nil {
        return fmt.Errorf("marshal handoff state: %w", err)
    }

    // 2. Store handoff state in content store (for audit/recovery)
    entry := &ContentEntry{
        ID:          GenerateContentID(stateJSON),
        SessionID:   pipeline.SessionID,
        AgentID:     agent.ID(),
        AgentType:   agent.Type(),
        ContentType: "agent_handoff",
        Content:     string(stateJSON),
        TokenCount:  estimateTokens(stateJSON),
        Timestamp:   time.Now(),
        Metadata: map[string]any{
            "pipeline_id":   pipeline.ID,
            "handoff_index": getHandoffIndex(state),
        },
    }

    if err := m.contentStore.IndexContent(entry); err != nil {
        log.Warn("Failed to store handoff state", "error", err)
        // Continue anyway - handoff should proceed
    }

    // 3. Create new agent instance
    newAgent, err := pipeline.CreateAgent(agent.Type())
    if err != nil {
        return fmt.Errorf("create new agent: %w", err)
    }

    // 4. Inject handoff state as initial context
    if err := newAgent.InjectHandoffState(state); err != nil {
        return fmt.Errorf("inject handoff state: %w", err)
    }

    // 5. Replace agent in pipeline
    pipeline.ReplaceAgent(agent.ID(), newAgent)

    // 6. Terminate old agent
    agent.Terminate()

    log.Info("Agent handoff complete",
        "agent_type", agent.Type(),
        "pipeline_id", pipeline.ID,
        "handoff_index", getHandoffIndex(state),
    )

    return nil
}

// Check if agent should trigger handoff
func (m *HandoffManager) ShouldHandoff(agent PipelineAgent) bool {
    usage := agent.ContextUsagePercent()
    return usage >= 75.0
}
```

### Agent Context Check Hook

```go
// core/pipeline/agent_base.go

type PipelineAgentBase struct {
    id              string
    agentType       string
    contextTokens   int
    maxContextTokens int
    handoffManager  *HandoffManager
    pipeline        *Pipeline
}

// Called after each turn
func (a *PipelineAgentBase) CheckContextAndHandoff() error {
    if a.handoffManager.ShouldHandoff(a) {
        return a.handoffManager.TriggerHandoff(a, a.pipeline)
    }
    return nil
}

func (a *PipelineAgentBase) ContextUsagePercent() float64 {
    return float64(a.contextTokens) / float64(a.maxContextTokens) * 100
}
```

---

## Virtual Context Manager

The central coordinator for context virtualization across all agents.

```go
// core/context/manager.go

type VirtualContextManager struct {
    contentStore      *UniversalContentStore
    retriever         *ContextRetriever
    refGenerator      *ReferenceGenerator
    evictionConfigs   map[string]*AgentEvictionConfig
    handoffManager    *HandoffManager

    // Per-agent context state
    agentContexts     map[string]*AgentContext
    mu                sync.RWMutex
}

type AgentContext struct {
    AgentID         string
    AgentType       string
    SessionID       string
    Entries         []*ContentEntry
    References      []*ContextReference
    TotalTokens     int
    MaxTokens       int
    CurrentTurn     int
    PreserveRecent  int
    PreserveTypes   []ContentType
}

func NewVirtualContextManager(config *VirtualContextConfig) (*VirtualContextManager, error) {
    store, err := NewUniversalContentStore(config.StoreConfig)
    if err != nil {
        return nil, err
    }

    return &VirtualContextManager{
        contentStore:    store,
        retriever:       NewContextRetriever(store),
        refGenerator:    &ReferenceGenerator{contentStore: store},
        evictionConfigs: EvictionConfigs,
        handoffManager:  NewHandoffManager(store),
        agentContexts:   make(map[string]*AgentContext),
    }, nil
}

// Called on every message through Guide
func (m *VirtualContextManager) OnMessage(agentID string, entry *ContentEntry) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    ctx, exists := m.agentContexts[agentID]
    if !exists {
        return fmt.Errorf("unknown agent: %s", agentID)
    }

    // 1. Store in universal content store
    if err := m.contentStore.IndexContent(entry); err != nil {
        return fmt.Errorf("index content: %w", err)
    }

    // 2. Add to agent's active context
    ctx.Entries = append(ctx.Entries, entry)
    ctx.TotalTokens += entry.TokenCount
    ctx.CurrentTurn++

    // 3. Check if eviction needed (knowledge agents only)
    config, isPipelineAgent := m.evictionConfigs[ctx.AgentType]
    if !isPipelineAgent {
        // Pipeline agents use handoff, not eviction
        return nil
    }

    if ctx.UsagePercent() >= config.ThresholdPercent {
        return m.evict(ctx, config)
    }

    return nil
}

func (m *VirtualContextManager) evict(ctx *AgentContext, config *AgentEvictionConfig) error {
    // Select entries for eviction
    entries, err := config.Strategy.SelectForEviction(ctx, config.EvictionPercent/100)
    if err != nil {
        return fmt.Errorf("select for eviction: %w", err)
    }

    if len(entries) == 0 {
        return nil
    }

    // Generate reference
    ref, err := m.refGenerator.GenerateReference(entries)
    if err != nil {
        return fmt.Errorf("generate reference: %w", err)
    }

    // Store reference
    if err := m.contentStore.StoreReference(ref); err != nil {
        return fmt.Errorf("store reference: %w", err)
    }

    // Replace entries with reference in agent's context
    ctx.ReplaceWithReference(entries, ref)

    log.Info("Context eviction complete",
        "agent_id", ctx.AgentID,
        "entries_evicted", len(entries),
        "tokens_saved", ref.TokensSaved,
        "new_usage", ctx.UsagePercent(),
    )

    return nil
}

func (ctx *AgentContext) UsagePercent() float64 {
    return float64(ctx.TotalTokens) / float64(ctx.MaxTokens) * 100
}

func (ctx *AgentContext) ReplaceWithReference(entries []*ContentEntry, ref *ContextReference) {
    // Build set of entry IDs to remove
    removeIDs := make(map[string]bool)
    for _, e := range entries {
        removeIDs[e.ID] = true
    }

    // Filter out evicted entries
    var newEntries []*ContentEntry
    var newTokens int
    for _, e := range ctx.Entries {
        if !removeIDs[e.ID] {
            newEntries = append(newEntries, e)
            newTokens += e.TokenCount
        }
    }

    // Add reference (as a pseudo-entry for rendering)
    refEntry := &ContentEntry{
        ID:          ref.ID,
        ContentType: "context_reference",
        Content:     ref.Render(),
        TokenCount:  estimateTokens([]byte(ref.Render())),
        TurnNumber:  ref.TurnRange[0], // Position at start of evicted range
    }

    // Insert reference at correct position
    newEntries = insertAtTurn(newEntries, refEntry, ref.TurnRange[0])
    newTokens += refEntry.TokenCount

    ctx.Entries = newEntries
    ctx.TotalTokens = newTokens
    ctx.References = append(ctx.References, ref)
}
```

---

## Complete Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    LOSSLESS CONTEXT VIRTUALIZATION ARCHITECTURE                      │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  APP STARTUP                                                                        │
│  ══════════                                                                         │
│  ┌─────────────┐                                                                   │
│  │  Terminal   │──[User Permission]──► StartupIndexer ──► ContentStore             │
│  │   Starts    │                            │                   │                  │
│  └─────────────┘                            │                   ▼                  │
│                                      ┌──────┴──────┐    ┌──────────────────┐      │
│                                      │ Parallel    │    │ Bleve (file-based)│      │
│                                      │ File Scan   │    │ VectorGraphDB     │      │
│                                      │ (all code)  │    │ (SQLite-based)    │      │
│                                      └─────────────┘    └──────────────────┘      │
│                                                                                     │
│  RUNTIME                                                                           │
│  ═══════                                                                           │
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                              GUIDE (Router)                                  │   │
│  │                                    │                                         │   │
│  │        ┌───────────────────────────┼───────────────────────────┐            │   │
│  │        │                           │                           │            │   │
│  │        ▼                           ▼                           ▼            │   │
│  │  ┌───────────┐              ┌───────────┐              ┌───────────┐        │   │
│  │  │ KNOWLEDGE │              │ KNOWLEDGE │              │  PIPELINE │        │   │
│  │  │  AGENTS   │              │  AGENTS   │              │  AGENTS   │        │   │
│  │  │           │              │           │              │           │        │   │
│  │  │ Librarian │              │ Academic  │              │ Engineer  │        │   │
│  │  │ Archivalist              │ Architect │              │ Designer  │        │   │
│  │  │           │              │           │              │ Inspector │        │   │
│  │  │           │              │           │              │ Tester    │        │   │
│  │  └─────┬─────┘              └─────┬─────┘              └─────┬─────┘        │   │
│  │        │                          │                          │              │   │
│  │        │   EVICTION @ 75-90%      │                          │              │   │
│  │        ▼                          ▼                          ▼              │   │
│  │  ┌───────────┐              ┌───────────┐              ┌───────────┐        │   │
│  │  │ Reference │              │ Reference │              │  AGENT    │        │   │
│  │  │ + Retrieve│              │ + Retrieve│              │  HANDOFF  │        │   │
│  │  │           │              │           │              │  @ 75%    │        │   │
│  │  └───────────┘              └───────────┘              └───────────┘        │   │
│  │        │                          │                          │              │   │
│  └────────┼──────────────────────────┼──────────────────────────┼──────────────┘   │
│           │                          │                          │                  │
│           └──────────────────────────┼──────────────────────────┘                  │
│                                      ▼                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                         UNIVERSAL CONTENT STORE                              │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐    │   │
│  │  │                         ALL CONTENT INDEXED                          │    │   │
│  │  │                                                                      │    │   │
│  │  │  • Every user prompt          • Every agent response                 │    │   │
│  │  │  • Every tool call/result     • Every file read/written              │    │   │
│  │  │  • Every web fetch            • Every research paper                 │    │   │
│  │  │  • Every code analysis        • Every routing decision               │    │   │
│  │  │  • Every handoff state        • Every checkpoint                     │    │   │
│  │  │                                                                      │    │   │
│  │  └─────────────────────────────────────────────────────────────────────┘    │   │
│  │                                                                              │   │
│  │  ┌────────────┐  ┌─────────────────────────┐  ┌────────────┐             │   │
│  │  │   BLEVE    │  │   VECTORGRAPHDB         │  │    CMT     │             │   │
│  │  │ (file dir) │  │   (SQLite: vector.db)   │  │  Manifest  │             │   │
│  │  │            │  │                         │  │            │             │   │
│  │  │ Full-text  │  │  • Embeddings (HNSW)    │  │            │             │   │
│  │  │  Search    │  │  • Graph relationships  │  │            │             │   │
│  │  │            │  │  • Content metadata     │  │            │             │   │
│  │  └────────────┘  └─────────────────────────┘  └────────────┘             │   │
│  │                                                                              │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  RETRIEVAL (on demand)                                                             │
│  ═════════════════════                                                             │
│  Agent sees [CTX-REF:...] → calls retrieve_context(ref_id="...") → full content   │
│  Agent needs old discussion → calls search_history(query="...") → matching content │
│  Agent needs specific file → already indexed → instant retrieval                   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Key Benefits

| Benefit | Description |
|---------|-------------|
| **Zero Information Loss** | Everything ever processed is retrievable |
| **Smarter Context** | Active context is working set, not random sample |
| **Cross-Session Memory** | Previous sessions' content fully searchable |
| **Fast Startup** | Repo pre-indexed, agents start with full knowledge |
| **Efficient Tokens** | Only active content uses context budget |
| **Pipeline Continuity** | Handoff preserves task state completely |
| **Debugging/Audit** | Full history for understanding agent behavior |
| **No Degradation** | Unlike compaction, quality doesn't decrease over time |

---

## Implementation Dependencies

### External Libraries

| Library | Purpose |
|---------|---------|
| `github.com/blevesearch/bleve/v2` | Full-text search indexing |
| `github.com/go-git/go-git/v5` | Pure-Go git integration |
| `github.com/fsnotify/fsnotify` | File system change notifications |
| `github.com/mattn/go-sqlite3` | SQLite for relational storage |

### Internal Dependencies

| Component | Depends On |
|-----------|------------|
| StartupIndexer | UniversalContentStore, GoroutineBudget, FileHandleBudget |
| VirtualContextManager | UniversalContentStore, Retriever, RefGenerator, HandoffManager |
| ContextRetriever | UniversalContentStore (Bleve + VectorDB) |
| HandoffManager | UniversalContentStore, Pipeline |
| Eviction Strategies | AgentContext, ContentEntry |

---

## Storage Layout

```
~/.sylk/projects/<project-hash>/
├── vector.db              # VectorGraphDB (SQLite) - embeddings, graph, content metadata
├── documents.bleve/       # Bleve full-text search index (file-based directory)
├── manifest.cmt           # Cartesian Merkle Tree manifest
└── manifest.cmt.wal       # WAL for crash recovery
```

**Storage Notes**:
- **vector.db**: VectorGraphDB uses SQLite internally for ALL storage - embeddings, graph edges, HNSW index, AND content entry metadata. No separate content database exists.
- **documents.bleve/**: Bleve index is file-based (not SQLite). Contains Scorch segments for full-text search.
- VectorGraphDB's schema can be extended to store ContentEntry records alongside existing node types.

---

## Security Considerations

1. **Content Filtering** - Never index files matching `.env`, `credentials`, `secrets`, etc.
2. **Access Control** - Respect existing PermissionManager for file access
3. **Audit Trail** - All retrievals logged for debugging
4. **Data Isolation** - Per-project storage, no cross-project leakage
5. **User Consent** - Startup indexing requires explicit user permission

---

## Architecture Gap Analysis

This section identifies issues with the current architecture and required changes for full integration with the Adaptive Retrieval System and WAVE 4 robustness systems.

### 1. Critical Issues in Current Architecture

#### 1.1 UniversalContentStore WAVE 4 Violations

The current `UniversalContentStore` (lines 177-322) has several WAVE 4 violations:

```go
// PROBLEM 1: Raw channel (must use SafeChan)
indexQueue   chan *ContentEntry  // ← VIOLATION

// PROBLEM 2: Raw goroutines in worker pool (must use GoroutineScope.Go())
for i := 0; i < store.workers; i++ {
    go store.indexWorker()  // ← VIOLATION
}

// PROBLEM 3: No file handle tracking for Bleve/SQLite
bleveIndex, err := bleve.Open(config.BlevePath)  // ← No FileHandleBudget
```

**REQUIRED CHANGES**:
```go
type UniversalContentStore struct {
    bleveIndex   bleve.Index            // Full-text search (file-based: documents.bleve/)
    vectorDB     *vectorgraphdb.DB      // Semantic search + content metadata (SQLite: vector.db)

    // WAVE 4 REQUIRED:
    indexQueue   *safechan.SafeChan[*ContentEntry]  // Context-aware channel
    scope        *concurrency.GoroutineScope        // Managed goroutines
    fileBudget   *resources.FileHandleBudget        // File handle tracking
    handles      []resources.TrackedFileHandle      // Acquired handles
}
// NOTE: VectorGraphDB (vector.db) is SQLite-based and stores ALL data:
// embeddings, graph relationships, AND content entry metadata.
// No separate content database - extend VectorGraphDB's schema for content entries.
```

#### 1.2 Duplicate Functionality with VectorGraphDB

The current architecture defines a **new** `UniversalContentStore` that duplicates existing `VectorGraphDB` functionality:

| UniversalContentStore | VectorGraphDB Equivalent |
|-----------------------|--------------------------|
| `searchVector()` | `VectorSearcher.Search()` |
| `searchBleve()` | N/A (Bleve integration needed) |
| `fuseResults()` | `QueryEngine.HybridQuery()` |
| `storeContent()` | `NodeStore.InsertNode()` (extend schema for content entries) |
| Session tracking | `SessionScopedView` |

**REQUIRED CHANGES**:
- `UniversalContentStore` should **wrap** `VectorGraphDB`, not duplicate it
- Add Bleve integration to `VectorGraphDB` rather than parallel implementation
- Use existing `QueryEngine.HybridQuery()` with RRF fusion

#### 1.3 StartupIndexer WAVE 4 Violations

```go
// PROBLEM: Raw goroutines in worker pool
go func(f *FileInfo) {
    defer wg.Done()  // ← Raw go, not tracked
    ...
}(file)
```

**REQUIRED**: Use `GoroutineBudget` with configurable limits for startup indexing.

#### 1.4 AccessTracker Unbounded Growth

```go
type AccessTracker struct {
    accessCounts map[string]int       // ← Unbounded, never cleaned
    lastAccess   map[string]int       // ← Unbounded, never cleaned
    accessLog    []AccessEvent        // ← Unbounded, grows forever
}
```

**REQUIRED CHANGES**:
- Implement sliding window or decay for `accessCounts`
- LRU eviction for `lastAccess` map
- Bounded ring buffer for `accessLog`
- Register with `PressureController` for emergency eviction

#### 1.5 Eviction Strategies Missing Pressure Response

Current eviction strategies are triggered **only** by context threshold (75-85%). They should **also** respond to memory pressure.

**REQUIRED**: Add `OnPressureChange()` callback to trigger proactive eviction at `PressureHigh`.

#### 1.6 Handoff State Not Indexed for Retrieval

Handoff states are stored in `ContentStore` but not properly indexed for semantic search. A new agent can't find relevant handoff history.

**REQUIRED**: Add `ContentTypeAgentHandoff` with proper embedding generation.

### 2. VectorGraphDB Changes Required

#### 2.1 Add Bleve Integration

The existing `VectorGraphDB` handles vector search but lacks full-text search. Rather than duplicating in `UniversalContentStore`, extend `VectorGraphDB`:

```go
// core/vectorgraphdb/bleve_integration.go

type BleveIntegratedDB struct {
    *VectorGraphDB
    bleveIndex   bleve.Index
    blevePath    string

    // WAVE 4 integration
    cbRegistry   *llm.GlobalCircuitBreakerRegistry
    fileBudget   *resources.FileHandleBudget
}

func (db *BleveIntegratedDB) HybridSearch(
    query string,
    embedding []float32,
    opts *HybridSearchOptions,
) ([]HybridSearchResult, error) {
    // Parallel search with circuit breakers
    // RRF fusion of Bleve + HNSW results
    // Respect latency budget
}
```

#### 2.2 Extend HybridResult for Adaptive Retrieval

```go
// core/vectorgraphdb/query.go

type HybridResult struct {
    Node            *GraphNode
    VectorScore     float64
    GraphScore      float64
    CombinedScore   float64
    ConnectionCount int

    // NEW: Adaptive retrieval fields
    RetrievalCost   float64    // Compute cost for this result
    BleveScore      float64    // Full-text score (NEW)
    TierSource      SearchTier // Which tier found this (hot/warm/full)
    AccessedAt      time.Time  // For cache prediction
}
```

#### 2.3 Add Pressure-Aware Search Methods

```go
// core/vectorgraphdb/pressure_search.go

type PressureAwareSearcher struct {
    qe         *QueryEngine
    pressure   *resources.PressureController
    maxTier    atomic.Int32  // Reduced under pressure
}

func (s *PressureAwareSearcher) Search(ctx context.Context, query string, opts *SearchOptions) ([]HybridResult, error) {
    maxTier := SearchTier(s.maxTier.Load())

    switch maxTier {
    case TierHotCache:
        return s.searchHotOnly(ctx, query)
    case TierWarmIndex:
        return s.searchUpToWarm(ctx, query, opts)
    case TierFullSearch:
        return s.searchFull(ctx, query, opts)
    }
    return nil, nil
}

func (s *PressureAwareSearcher) OnPressureChange(level resources.PressureLevel) {
    switch level {
    case resources.PressureNormal:
        s.maxTier.Store(int32(TierFullSearch))
    case resources.PressureElevated:
        s.maxTier.Store(int32(TierWarmIndex))
    case resources.PressureHigh, resources.PressureCritical:
        s.maxTier.Store(int32(TierHotCache))
    }
}
```

#### 2.4 Session Views Need Retrieval Budgets

```go
// core/vectorgraphdb/session_view.go

type SessionScopedView struct {
    // ... existing fields ...

    // NEW: Retrieval budget tracking
    retrievalBudget *RetrievalBudget
}

type RetrievalBudget struct {
    MaxTokensPerQuery   int
    MaxQueriesPerTurn   int
    MaxTotalTokens      int
    UsedTokens          atomic.Int64
    UsedQueries         atomic.Int64
}

func (v *SessionScopedView) Search(query string, opts *SearchOptions) ([]HybridResult, error) {
    if !v.retrievalBudget.CanQuery() {
        return nil, ErrBudgetExhausted
    }

    results, err := v.searcher.Search(query, opts)
    if err != nil {
        return nil, err
    }

    // Track budget
    totalTokens := 0
    for _, r := range results {
        totalTokens += r.Node.TokenCount
    }
    v.retrievalBudget.UsedTokens.Add(int64(totalTokens))
    v.retrievalBudget.UsedQueries.Add(1)

    return results, nil
}
```

### 3. Agent Skills, Hooks, Tools, and Protocols

Each agent needs specific capabilities to maximize the Adaptive Retrieval System.

#### 3.1 Skills by Agent Type

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    AGENT RETRIEVAL SKILLS MATRIX                                     │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  KNOWLEDGE AGENTS (use eviction + retrieval)                                        │
│  ════════════════════════════════════════════                                       │
│                                                                                     │
│  LIBRARIAN:                                                                         │
│  ├── retrieve_context(ref_id, query, max_tokens)  [CTX-REF expansion]              │
│  ├── search_codebase(query, filters)              [Code-specific search]           │
│  ├── get_symbol_context(symbol, depth)            [Symbol + related code]          │
│  ├── search_history(query, session_id)            [Historical content]             │
│  └── promote_to_hot(content_ids)                  [Mark content as frequently used]│
│                                                                                     │
│  ARCHIVALIST:                                                                       │
│  ├── retrieve_context(ref_id, query, max_tokens)                                   │
│  ├── search_history(query, session_id, category)  [Session history search]         │
│  ├── get_chronicle_entries(timerange, category)   [Temporal retrieval]             │
│  ├── search_decisions(query)                      [Decision history]               │
│  └── get_cross_session_patterns()                 [Cross-session learning]         │
│                                                                                     │
│  ACADEMIC:                                                                          │
│  ├── retrieve_context(ref_id, query, max_tokens)                                   │
│  ├── search_research(query, source_types)         [Papers, RFCs, docs]             │
│  ├── get_related_research(topic)                  [Semantic expansion]             │
│  ├── validate_sources(content_ids)                [Trust verification]             │
│  └── cite_sources(claim)                          [Find supporting sources]        │
│                                                                                     │
│  ARCHITECT:                                                                         │
│  ├── retrieve_context(ref_id, query, max_tokens)                                   │
│  ├── search_patterns(query)                       [Architecture patterns]          │
│  ├── get_system_context(component)                [Related components]             │
│  └── search_decisions(query)                      [Past architectural decisions]   │
│                                                                                     │
│  ─────────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  PIPELINE AGENTS (use handoff, limited retrieval)                                   │
│  ═════════════════════════════════════════════════                                  │
│                                                                                     │
│  ENGINEER:                                                                          │
│  ├── get_file_context(path, line_range)           [Focused file retrieval]         │
│  ├── search_codebase(query, max_results=5)        [Limited code search]            │
│  ├── get_recent_changes(path)                     [Recent modifications]           │
│  └── consult_*() → routes to knowledge agents     [Delegate complex retrieval]     │
│                                                                                     │
│  INSPECTOR:                                                                         │
│  ├── get_validation_context(issue_type)           [Past similar issues]            │
│  ├── search_issues(query)                         [Historical issues]              │
│  └── get_test_coverage(path)                      [Coverage context]               │
│                                                                                     │
│  TESTER:                                                                            │
│  ├── get_test_patterns(function_signature)        [Similar test examples]          │
│  ├── search_test_history(query)                   [Past test results]              │
│  └── get_coverage_gaps()                          [Uncovered code]                 │
│                                                                                     │
│  DESIGNER:                                                                          │
│  ├── get_component_patterns(component_type)       [UI pattern examples]            │
│  ├── search_styles(query)                         [Style definitions]              │
│  └── get_a11y_guidelines(component)               [Accessibility context]          │
│                                                                                     │
│  ─────────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  COORDINATION AGENTS (orchestration + routing)                                      │
│  ═════════════════════════════════════════════                                      │
│                                                                                     │
│  ORCHESTRATOR:                                                                      │
│  ├── get_workflow_context(workflow_id)            [Workflow state retrieval]       │
│  ├── get_agent_history(agent_id)                  [Agent performance history]      │
│  ├── search_similar_workflows(query)              [Past similar workflows]         │
│  └── get_pending_context()                        [Context for pending tasks]      │
│                                                                                     │
│  GUIDE:                                                                             │
│  ├── get_routing_history(query_pattern)           [Past routing decisions]         │
│  ├── get_user_preferences()                       [Learned user preferences]       │
│  ├── search_conversations(query)                  [Past conversations]             │
│  └── get_agent_capabilities()                     [Agent registry + capabilities]  │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### 3.2 Hook Definitions

```go
// core/context/retrieval_hooks.go

// PrePromptPrefetchHook - Inject prefetched content before LLM sees query
type PrePromptPrefetchHook struct {
    prefetcher *SpeculativePrefetcher
    augmenter  *QueryAugmenter
}

func (h *PrePromptPrefetchHook) Phase() HookPhase { return HookPhasePrePrompt }
func (h *PrePromptPrefetchHook) Priority() int    { return HookPriorityHigh }

func (h *PrePromptPrefetchHook) Execute(ctx context.Context, data *HookData) (*HookResult, error) {
    query := data.UserQuery
    remainingTokens := data.RemainingContext

    // Get speculative prefetch result (started earlier)
    future := h.prefetcher.GetOrStart(query)
    prefetch := future.GetIfReady(10 * time.Millisecond)

    if prefetch == nil {
        // Prefetch not ready - do quick synchronous augmentation
        prefetch, _ = h.augmenter.Augment(ctx, query, remainingTokens)
    }

    if prefetch != nil && len(prefetch.Excerpts) > 0 {
        data.InjectContext = prefetch.Render()
    }

    return &HookResult{Continue: true}, nil
}

// PostToolObservationHook - Record retrieval outcomes for adaptive learning
type PostToolObservationHook struct {
    observationLog *ObservationLog
    tracker        *EpisodeTracker
}

func (h *PostToolObservationHook) Phase() HookPhase { return HookPhasePostToolCall }
func (h *PostToolObservationHook) Priority() int    { return HookPriorityNormal }

func (h *PostToolObservationHook) Execute(ctx context.Context, data *HookData) (*HookResult, error) {
    if data.ToolName == "search" || data.ToolName == "retrieve_context" {
        // Record that content was actively retrieved (not from prefetch)
        h.tracker.RecordSearch(data.ToolInput, data.ToolOutput)
    }

    return &HookResult{Continue: true}, nil
}

// PostResponseCompletionHook - Finalize episode observation
type PostResponseCompletionHook struct {
    observationLog *ObservationLog
    tracker        *EpisodeTracker
    adaptive       *AdaptiveState
}

func (h *PostResponseCompletionHook) Phase() HookPhase { return HookPhasePostPrompt }
func (h *PostResponseCompletionHook) Priority() int    { return HookPriorityLow }

func (h *PostResponseCompletionHook) Execute(ctx context.Context, data *HookData) (*HookResult, error) {
    // Build episode observation
    obs := h.tracker.FinalizeEpisode(data.Response, data.ToolCalls)

    // Record to WAL (async processing)
    h.observationLog.Record(ctx, obs)

    return &HookResult{Continue: true}, nil
}

// EvictionPressureHook - Trigger proactive eviction under memory pressure
type EvictionPressureHook struct {
    contextManager *VirtualContextManager
    pressure       *resources.PressureController
}

func (h *EvictionPressureHook) Phase() HookPhase { return HookPhasePrePrompt }
func (h *EvictionPressureHook) Priority() int    { return HookPriorityHigh + 10 } // Before prefetch

func (h *EvictionPressureHook) Execute(ctx context.Context, data *HookData) (*HookResult, error) {
    level := h.pressure.CurrentLevel()

    if level >= resources.PressureHigh {
        // Force eviction to free memory before processing
        agentCtx := h.contextManager.GetAgentContext(data.AgentID)
        if agentCtx.UsagePercent() > 50 {
            h.contextManager.ForceEvict(agentCtx, 0.25) // Evict 25%
        }
    }

    return &HookResult{Continue: true}, nil
}
```

#### 3.3 Hook Registration by Agent

```go
// core/context/hook_registry.go

func RegisterAgentHooks(agentType string, registry *HookRegistry, deps *HookDependencies) {
    // Common hooks for all agents
    registry.Register(agentType, &PostResponseCompletionHook{
        observationLog: deps.ObservationLog,
        tracker:        deps.EpisodeTracker,
        adaptive:       deps.AdaptiveState,
    })

    registry.Register(agentType, &EvictionPressureHook{
        contextManager: deps.ContextManager,
        pressure:       deps.PressureController,
    })

    // Agent-specific hooks
    switch agentType {
    case "librarian", "archivalist", "academic", "architect":
        // Knowledge agents get full prefetch
        registry.Register(agentType, &PrePromptPrefetchHook{
            prefetcher: deps.Prefetcher,
            augmenter:  deps.Augmenter,
        })
        registry.Register(agentType, &PostToolObservationHook{
            observationLog: deps.ObservationLog,
            tracker:        deps.EpisodeTracker,
        })

    case "guide":
        // Guide gets prefetch for routing decisions
        registry.Register(agentType, &PrePromptPrefetchHook{
            prefetcher: deps.Prefetcher,
            augmenter:  deps.Augmenter,
        })
        registry.Register(agentType, &RoutingHistoryHook{
            routingCache: deps.RoutingCache,
        })

    case "engineer", "designer", "inspector", "tester":
        // Pipeline agents get limited prefetch (focused, not broad)
        registry.Register(agentType, &FocusedPrefetchHook{
            augmenter: deps.Augmenter,
            maxTokens: 1000, // Much smaller budget
        })

    case "orchestrator":
        // Orchestrator gets workflow-focused prefetch
        registry.Register(agentType, &WorkflowContextHook{
            workflowStore: deps.WorkflowStore,
        })
    }
}
```

#### 3.4 Inter-Agent Context Protocols

```go
// core/context/agent_protocols.go

// ContextShareRequest - Agent requests context from another agent
type ContextShareRequest struct {
    RequestingAgentID   string
    RequestingAgentType string
    TargetAgentType     string
    Query               string
    MaxTokens           int
    Priority            ContextPriority
    Filters             *ContextFilters
}

type ContextPriority int
const (
    ContextPriorityLow    ContextPriority = 0
    ContextPriorityNormal ContextPriority = 1
    ContextPriorityHigh   ContextPriority = 2
    ContextPriorityCritical ContextPriority = 3
)

// ContextShareResponse - Shared context from knowledge agent
type ContextShareResponse struct {
    SourceAgentID   string
    SourceAgentType string
    Entries         []*ContentEntry
    References      []*ContextReference
    TokenCount      int
    Truncated       bool
    CacheHint       string  // "hot", "warm", "cold" - for recipient's cache
}

// Protocol: Engineer → Librarian consultation
func (e *Engineer) ConsultLibrarian(query string) (*ConsultResponse, error) {
    req := &ContextShareRequest{
        RequestingAgentID:   e.ID(),
        RequestingAgentType: "engineer",
        TargetAgentType:     "librarian",
        Query:               query,
        MaxTokens:           2000,
        Priority:            ContextPriorityNormal,
        Filters: &ContextFilters{
            ContentTypes: []ContentType{ContentTypeCodeFile},
            Domains:      []Domain{DomainCode},
        },
    }

    resp := e.router.RequestContext(req)

    // Promote received content to engineer's hot cache
    for _, entry := range resp.Entries {
        e.hotCache.Add(entry.ID, entry)
    }

    return &ConsultResponse{
        RelevantFiles: extractFiles(resp.Entries),
        Suggestions:   extractSuggestions(resp.References),
    }, nil
}

// Protocol: Guide → Any agent routing
func (g *Guide) RouteWithContext(userQuery string, targetAgent string) error {
    // 1. Get prefetched context relevant to query
    prefetch := g.prefetcher.GetIfReady(userQuery)

    // 2. Include relevant context in routing payload
    routing := &ForwardedRequest{
        UserQuery:     userQuery,
        Classification: g.classify(userQuery),
        PrefetchedContext: prefetch, // Share prefetch with target
    }

    // 3. Target agent receives prefetch, promotes to hot cache
    return g.router.Forward(routing, targetAgent)
}

// Protocol: Orchestrator → Pipeline agents workflow context
func (o *Orchestrator) DispatchWithContext(task *OrchestratorTask, pipeline *Pipeline) error {
    // 1. Get workflow context
    workflowCtx := o.workflowStore.GetContext(o.currentWorkflow.ID)

    // 2. Get relevant historical context for this task type
    historical := o.contextManager.SearchSimilarTasks(task.Description, 3)

    // 3. Build dispatch context
    dispatch := &TaskDispatch{
        Task:              task,
        WorkflowContext:   workflowCtx,
        HistoricalContext: historical,
        Dependencies:      o.resolveDependencies(task),
    }

    // 4. Pipeline agents receive full context
    return pipeline.Execute(dispatch)
}
```

#### 3.5 Complete Skill Definitions (Anthropic Tool Format)

```go
// core/context/retrieval_skills.go

// ═══════════════════════════════════════════════════════════════════════════════
// UNIVERSAL RETRIEVAL SKILLS (Knowledge Agents: Librarian, Archivalist, Academic, Architect)
// ═══════════════════════════════════════════════════════════════════════════════

var UniversalRetrievalSkills = []SkillDefinition{
    {
        Name:        "retrieve_context",
        Description: "Retrieve full content for an evicted context reference. Use when you see [CTX-REF:...] markers and need the original content. Returns the complete content that was evicted.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "ref_id": {
                    "type":        "string",
                    "description": "Reference ID from CTX-REF marker (e.g., 'abc123' from [CTX-REF:conversation | ... | retrieve_context(ref_id=\"abc123\")])",
                },
                "query": {
                    "type":        "string",
                    "description": "Natural language query for semantic search if ref_id not available",
                },
                "max_tokens": {
                    "type":        "integer",
                    "description": "Maximum tokens to retrieve (default: 2000, max: 10000)",
                    "default":     2000,
                    "minimum":     100,
                    "maximum":     10000,
                },
                "include_related": {
                    "type":        "boolean",
                    "description": "Include semantically related content beyond the exact reference",
                    "default":     false,
                },
            },
            "oneOf": []map[string]any{
                {"required": []string{"ref_id"}},
                {"required": []string{"query"}},
            },
        },
        Handler: handleRetrieveContext,
    },
    {
        Name:        "search_history",
        Description: "Search all historical context across current and past sessions. Uses hybrid Bleve full-text + VectorDB semantic search with RRF fusion.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": {
                    "type":        "string",
                    "description": "Natural language search query",
                },
                "content_types": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"user_prompt", "agent_response", "tool_call", "tool_result", "code_file", "web_fetch", "research_paper", "agent_message", "plan_workflow", "test_result", "inspector_finding"},
                    },
                    "description": "Filter by content types",
                },
                "session_ids": {
                    "type":        "array",
                    "items":       {"type": "string"},
                    "description": "Limit to specific sessions (empty = current session only)",
                },
                "cross_session": {
                    "type":        "boolean",
                    "description": "Search across ALL sessions (overrides session_ids)",
                    "default":     false,
                },
                "time_range": {
                    "type": "object",
                    "properties": map[string]any{
                        "start": {"type": "string", "format": "date-time"},
                        "end":   {"type": "string", "format": "date-time"},
                    },
                    "description": "Filter by time range (ISO 8601 format)",
                },
                "max_results": {
                    "type":        "integer",
                    "description": "Maximum results to return",
                    "default":     10,
                    "minimum":     1,
                    "maximum":     50,
                },
            },
            "required": []string{"query"},
        },
        Handler: handleSearchHistory,
    },
    {
        Name:        "promote_to_hot",
        Description: "Mark content as frequently accessed to keep in hot cache. Use when you know content will be needed repeatedly.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "content_ids": {
                    "type":        "array",
                    "items":       {"type": "string"},
                    "description": "Content IDs to promote to hot cache",
                    "minItems":    1,
                    "maxItems":    20,
                },
                "ttl_turns": {
                    "type":        "integer",
                    "description": "Number of turns to keep hot (default: 10)",
                    "default":     10,
                    "minimum":     1,
                    "maximum":     50,
                },
            },
            "required": []string{"content_ids"},
        },
        Handler: handlePromoteToHot,
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// LIBRARIAN-SPECIFIC RETRIEVAL SKILLS
// ═══════════════════════════════════════════════════════════════════════════════

var LibrarianRetrievalSkills = []SkillDefinition{
    {
        Name:        "librarian_search_codebase",
        Description: "Search the indexed codebase using hybrid Bleve + VectorDB search. Returns code snippets with file paths and line numbers.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": {
                    "type":        "string",
                    "description": "Code search query (natural language or code pattern)",
                },
                "languages": {
                    "type":  "array",
                    "items": {"type": "string"},
                    "description": "Filter by programming languages (e.g., ['go', 'typescript'])",
                },
                "path_pattern": {
                    "type":        "string",
                    "description": "Glob pattern for file paths (e.g., 'core/**/*.go')",
                },
                "symbol_types": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"function", "method", "struct", "interface", "type", "const", "var", "class", "all"},
                    },
                    "description": "Filter by symbol types",
                },
                "include_tests": {
                    "type":        "boolean",
                    "description": "Include test files in results",
                    "default":     false,
                },
                "max_results": {
                    "type":    "integer",
                    "default": 10,
                    "minimum": 1,
                    "maximum": 50,
                },
                "context_lines": {
                    "type":        "integer",
                    "description": "Lines of context around matches",
                    "default":     3,
                    "minimum":     0,
                    "maximum":     20,
                },
            },
            "required": []string{"query"},
        },
        Handler: handleLibrarianSearchCodebase,
    },
    {
        Name:        "librarian_get_symbol_context",
        Description: "Get a symbol and its full context including callers, callees, type definitions, and usage examples.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "symbol": {
                    "type":        "string",
                    "description": "Symbol name to look up (e.g., 'HandleRequest', 'UserService')",
                },
                "file": {
                    "type":        "string",
                    "description": "File path containing the symbol (helps disambiguate)",
                },
                "depth": {
                    "type":        "integer",
                    "description": "Levels of related symbols to include (1=direct, 2=transitive)",
                    "default":     1,
                    "minimum":     1,
                    "maximum":     3,
                },
                "include": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"callers", "callees", "types", "implementations", "tests", "docs"},
                    },
                    "description": "What related context to include",
                    "default":     []string{"callers", "callees", "types"},
                },
            },
            "required": []string{"symbol"},
        },
        Handler: handleLibrarianGetSymbolContext,
    },
    {
        Name:        "librarian_get_pattern_examples",
        Description: "Get examples of a specific coding pattern from the codebase. Use to show how patterns are implemented in this project.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "pattern": {
                    "type":        "string",
                    "description": "Pattern name or description (e.g., 'error handling', 'dependency injection', 'repository pattern')",
                },
                "language": {
                    "type":        "string",
                    "description": "Programming language to search in",
                },
                "max_examples": {
                    "type":    "integer",
                    "default": 3,
                    "minimum": 1,
                    "maximum": 10,
                },
            },
            "required": []string{"pattern"},
        },
        Handler: handleLibrarianGetPatternExamples,
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// ARCHIVALIST-SPECIFIC RETRIEVAL SKILLS
// ═══════════════════════════════════════════════════════════════════════════════

var ArchivalistRetrievalSkills = []SkillDefinition{
    {
        Name:        "archivalist_search_decisions",
        Description: "Search past decisions, their rationale, and outcomes. Critical for avoiding repeated mistakes.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": {
                    "type":        "string",
                    "description": "Search query for decisions",
                },
                "categories": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"architecture", "implementation", "tooling", "testing", "security", "performance", "ux", "all"},
                    },
                    "description": "Decision categories to search",
                },
                "outcome_filter": {
                    "type": "string",
                    "enum": []string{"successful", "failed", "pending", "all"},
                    "description": "Filter by decision outcome",
                    "default":     "all",
                },
                "time_range": {
                    "type":        "string",
                    "description": "Time range (e.g., '7d', '30d', '90d', 'all')",
                    "default":     "all",
                },
                "cross_session": {
                    "type":        "boolean",
                    "description": "Include decisions from other sessions",
                    "default":     true,
                },
            },
            "required": []string{"query"},
        },
        Handler: handleArchivalistSearchDecisions,
    },
    {
        Name:        "archivalist_get_failure_patterns",
        Description: "Get failure patterns similar to current approach. CRITICAL: Call before attempting approaches that have failed before.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "approach_description": {
                    "type":        "string",
                    "description": "Description of the approach being considered",
                },
                "context": {
                    "type":        "string",
                    "description": "Current context (files, task, constraints)",
                },
                "similarity_threshold": {
                    "type":        "number",
                    "description": "Minimum similarity score (0.0-1.0) to consider a match",
                    "default":     0.7,
                    "minimum":     0.0,
                    "maximum":     1.0,
                },
            },
            "required": []string{"approach_description"},
        },
        Handler: handleArchivalistGetFailurePatterns,
    },
    {
        Name:        "archivalist_get_cross_session_learnings",
        Description: "Get learnings from other sessions that apply to current context. Surfaces patterns across the user's history.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "context_query": {
                    "type":        "string",
                    "description": "Current context to find relevant learnings for",
                },
                "learning_types": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"pattern", "failure", "decision", "insight", "all"},
                    },
                    "default": []string{"all"},
                },
                "min_recurrence": {
                    "type":        "integer",
                    "description": "Minimum times pattern must have occurred",
                    "default":     2,
                },
            },
            "required": []string{"context_query"},
        },
        Handler: handleArchivalistGetCrossSessionLearnings,
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// ACADEMIC-SPECIFIC RETRIEVAL SKILLS
// ═══════════════════════════════════════════════════════════════════════════════

var AcademicRetrievalSkills = []SkillDefinition{
    {
        Name:        "academic_search_research",
        Description: "Search indexed research papers, RFCs, documentation, and best practices. Results include applicability assessment.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": {
                    "type":        "string",
                    "description": "Research query",
                },
                "source_types": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"paper", "rfc", "documentation", "blog", "stackoverflow", "tutorial", "book", "all"},
                    },
                    "default": []string{"all"},
                },
                "min_trust_level": {
                    "type": "string",
                    "enum": []string{"verified", "high", "medium", "low"},
                    "description": "Minimum trust level for sources",
                    "default":     "medium",
                },
                "recency": {
                    "type": "string",
                    "enum": []string{"latest", "last_year", "last_3_years", "all"},
                    "description": "How recent sources should be",
                    "default":     "all",
                },
                "include_applicability": {
                    "type":        "boolean",
                    "description": "Include applicability analysis for current codebase",
                    "default":     true,
                },
            },
            "required": []string{"query"},
        },
        Handler: handleAcademicSearchResearch,
    },
    {
        Name:        "academic_cite_sources",
        Description: "Find sources that support or refute a technical claim. Use to validate recommendations.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "claim": {
                    "type":        "string",
                    "description": "Technical claim to find sources for",
                },
                "stance": {
                    "type": "string",
                    "enum": []string{"support", "refute", "both"},
                    "description": "Whether to find supporting, refuting, or both types of sources",
                    "default":     "both",
                },
                "min_sources": {
                    "type":        "integer",
                    "description": "Minimum number of sources to find",
                    "default":     2,
                    "minimum":     1,
                    "maximum":     10,
                },
            },
            "required": []string{"claim"},
        },
        Handler: handleAcademicCiteSources,
    },
    {
        Name:        "academic_get_best_practices",
        Description: "Get best practices for a specific technology or pattern, tailored to codebase maturity level.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "topic": {
                    "type":        "string",
                    "description": "Topic to get best practices for (e.g., 'error handling in Go', 'React state management')",
                },
                "codebase_maturity": {
                    "type": "string",
                    "enum": []string{"greenfield", "legacy", "transitional", "disciplined", "auto"},
                    "description": "Codebase maturity level (auto = ask Librarian)",
                    "default":     "auto",
                },
                "constraints": {
                    "type":        "array",
                    "items":       {"type": "string"},
                    "description": "Constraints to consider (e.g., ['no external dependencies', 'must support Go 1.18'])",
                },
            },
            "required": []string{"topic"},
        },
        Handler: handleAcademicGetBestPractices,
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// GUIDE-SPECIFIC RETRIEVAL SKILLS
// ═══════════════════════════════════════════════════════════════════════════════

var GuideRetrievalSkills = []SkillDefinition{
    {
        Name:        "guide_get_routing_history",
        Description: "Get past routing decisions for similar queries. Use to maintain consistency in routing.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query_pattern": {
                    "type":        "string",
                    "description": "Current query or pattern to match against",
                },
                "include_outcomes": {
                    "type":        "boolean",
                    "description": "Include success/failure outcomes of past routings",
                    "default":     true,
                },
                "limit": {
                    "type":    "integer",
                    "default": 5,
                    "minimum": 1,
                    "maximum": 20,
                },
            },
            "required": []string{"query_pattern"},
        },
        Handler: handleGuideGetRoutingHistory,
    },
    {
        Name:        "guide_get_user_preferences",
        Description: "Get learned user preferences for routing and response style.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "preference_types": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"verbosity", "agent_affinity", "topic_routing", "response_style", "all"},
                    },
                    "default": []string{"all"},
                },
            },
        },
        Handler: handleGuideGetUserPreferences,
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// ORCHESTRATOR-SPECIFIC RETRIEVAL SKILLS
// ═══════════════════════════════════════════════════════════════════════════════

var OrchestratorRetrievalSkills = []SkillDefinition{
    {
        Name:        "orchestrator_get_workflow_context",
        Description: "Get full context for a workflow including history and current state.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "workflow_id": {
                    "type":        "string",
                    "description": "Workflow ID (omit for current workflow)",
                },
                "include": {
                    "type": "array",
                    "items": {
                        "type": "string",
                        "enum": []string{"history", "decisions", "blockers", "artifacts", "agent_states", "all"},
                    },
                    "default": []string{"all"},
                },
            },
        },
        Handler: handleOrchestratorGetWorkflowContext,
    },
    {
        Name:        "orchestrator_search_similar_workflows",
        Description: "Find past workflows similar to current task. Use to learn from previous approaches.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "description": {
                    "type":        "string",
                    "description": "Current task or workflow description",
                },
                "outcome_filter": {
                    "type": "string",
                    "enum": []string{"successful", "failed", "all"},
                    "description": "Filter by workflow outcome",
                    "default":     "successful",
                },
                "limit": {
                    "type":    "integer",
                    "default": 3,
                    "minimum": 1,
                    "maximum": 10,
                },
            },
            "required": []string{"description"},
        },
        Handler: handleOrchestratorSearchSimilarWorkflows,
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// PIPELINE AGENT RETRIEVAL SKILLS (Engineer, Designer, Inspector, Tester)
// Limited retrieval - prefer consultation with knowledge agents
// ═══════════════════════════════════════════════════════════════════════════════

var PipelineAgentRetrievalSkills = []SkillDefinition{
    {
        Name:        "get_file_context",
        Description: "Get focused context for a specific file. Use for quick lookups; consult Librarian for broader searches.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path": {
                    "type":        "string",
                    "description": "File path to get context for",
                },
                "start_line": {
                    "type":        "integer",
                    "description": "Start line (optional, defaults to beginning)",
                },
                "end_line": {
                    "type":        "integer",
                    "description": "End line (optional, defaults to end)",
                },
                "include_imports": {
                    "type":        "boolean",
                    "description": "Include imported/required files",
                    "default":     false,
                },
                "include_symbols": {
                    "type":        "boolean",
                    "description": "Include symbol definitions in file",
                    "default":     true,
                },
            },
            "required": []string{"path"},
        },
        Handler: handleGetFileContext,
    },
    {
        Name:        "get_recent_changes",
        Description: "Get recent changes to a file or directory. Useful for understanding current state.",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "path": {
                    "type":        "string",
                    "description": "File or directory path",
                },
                "limit": {
                    "type":        "integer",
                    "description": "Number of recent changes",
                    "default":     5,
                    "minimum":     1,
                    "maximum":     20,
                },
                "include_diffs": {
                    "type":        "boolean",
                    "description": "Include actual diff content",
                    "default":     false,
                },
            },
            "required": []string{"path"},
        },
        Handler: handleGetRecentChanges,
    },
}
```

#### 3.6 Complete Hook Definitions

```go
// core/context/retrieval_hooks.go

// ═══════════════════════════════════════════════════════════════════════════════
// HOOK DATA STRUCTURES
// ═══════════════════════════════════════════════════════════════════════════════

type PromptHookData struct {
    // Input
    AgentID       string
    AgentType     string
    SessionID     string
    UserQuery     string
    SystemPrompt  string
    Messages      []Message
    RemainingContext int  // Remaining tokens in context window

    // Modifiable by hooks
    InjectContext string   // Context to inject before user query
    SkipLLM       bool     // Skip LLM call (use cached result)
    CachedResult  any      // Cached result if SkipLLM=true

    // Metadata
    TurnNumber    int
    Timestamp     time.Time
}

type ToolCallHookData struct {
    // Input
    AgentID     string
    AgentType   string
    SessionID   string
    ToolName    string
    ToolInput   map[string]any
    TurnNumber  int

    // Output (set by tool execution)
    ToolOutput  any
    ToolError   error
    Duration    time.Duration

    // Modifiable
    SkipExecution bool  // Skip actual tool execution
    ModifiedInput map[string]any  // Modified parameters
}

// ═══════════════════════════════════════════════════════════════════════════════
// PRE-PROMPT HOOKS: Execute before LLM call
// ═══════════════════════════════════════════════════════════════════════════════

// Hook: Speculative Prefetch Injection
// Priority: HookPriorityFirst (0) - runs before all other hooks
// Agents: librarian, archivalist, academic, architect, guide
var SpeculativePrefetchHook = Hook{
    Name:     "speculative_prefetch_injection",
    Type:     PrePrompt,
    Priority: HookPriorityFirst,
    Agents:   []string{"librarian", "archivalist", "academic", "architect", "guide"},
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        prefetcher := ctx.Value(prefetcherKey).(*SpeculativePrefetcher)
        augmenter := ctx.Value(augmenterKey).(*QueryAugmenter)

        // Check if speculative prefetch was started earlier
        future := prefetcher.GetInflight(data.UserQuery)
        var prefetch *AugmentedQuery

        if future != nil {
            // Speculative prefetch was started - get result with short timeout
            prefetch = future.GetIfReady(10 * time.Millisecond)
        }

        if prefetch == nil {
            // No speculative prefetch available - do quick synchronous augmentation
            prefetch, _ = augmenter.Augment(ctx, data.UserQuery, data.RemainingContext)
        }

        if prefetch != nil && (len(prefetch.Excerpts) > 0 || len(prefetch.Summaries) > 0) {
            // Inject prefetched context before user query
            data.InjectContext = prefetch.Render()
        }

        return data, nil
    },
}

// Hook: Episode Tracker Initialization
// Priority: HookPriorityEarly (25)
// Agents: ALL (tracks observations for adaptive learning)
var EpisodeTrackerInitHook = Hook{
    Name:     "episode_tracker_init",
    Type:     PrePrompt,
    Priority: HookPriorityEarly,
    Agents:   []string{"*"}, // All agents
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        tracker := ctx.Value(episodeTrackerKey).(*EpisodeTracker)
        adaptive := ctx.Value(adaptiveStateKey).(*AdaptiveState)

        // Start new episode observation
        episode := tracker.StartEpisode(data.AgentID, data.AgentType, data.TurnNumber)

        // Sample weights for this episode (Thompson sampling)
        queryEmbedding := ctx.Value(queryEmbeddingKey).([]float32)
        taskContext := adaptive.ContextDiscovery.ClassifyQuery(data.UserQuery, queryEmbedding)
        episode.TaskContext = taskContext
        episode.QueryEmbedding = queryEmbedding
        episode.SampledWeights = adaptive.SampleWeights(taskContext)
        episode.SampledThresholds = adaptive.SampleThresholds()

        return data, nil
    },
}

// Hook: Pressure-Driven Preemptive Eviction
// Priority: HookPriorityFirst (0) - before prefetch
// Agents: Knowledge agents only
var PressureEvictionHook = Hook{
    Name:     "pressure_preemptive_eviction",
    Type:     PrePrompt,
    Priority: HookPriorityFirst,
    Agents:   []string{"librarian", "archivalist", "academic", "architect"},
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        pressure := ctx.Value(pressureControllerKey).(*resources.PressureController)
        contextMgr := ctx.Value(contextManagerKey).(*VirtualContextManager)

        level := pressure.CurrentLevel()
        if level >= resources.PressureHigh {
            // Force eviction to free memory before processing
            agentCtx := contextMgr.GetAgentContext(data.AgentID)
            if agentCtx != nil && agentCtx.UsagePercent() > 50 {
                evictPercent := 0.25 // 25% eviction
                if level == resources.PressureCritical {
                    evictPercent = 0.50 // 50% eviction at critical
                }
                contextMgr.ForceEvict(agentCtx, evictPercent)
            }
        }

        return data, nil
    },
}

// Hook: Failure Pattern Warning Injection
// Priority: HookPriorityNormal (50)
// Agents: engineer, designer, architect
var FailurePatternWarningHook = Hook{
    Name:     "failure_pattern_warning",
    Type:     PrePrompt,
    Priority: HookPriorityNormal,
    Agents:   []string{"engineer", "designer", "architect"},
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        archivalist := ctx.Value(archivalistClientKey).(*ArchivalistClient)

        // Check for similar failure patterns
        failures, err := archivalist.GetFailurePatterns(ctx, data.UserQuery, 0.7)
        if err != nil || len(failures) == 0 {
            return data, nil
        }

        // Inject warning into context
        var warning strings.Builder
        warning.WriteString("\n\n[FAILURE_PATTERN_WARNING]\n")
        warning.WriteString("Similar approaches have failed before:\n")
        for _, f := range failures {
            warning.WriteString(fmt.Sprintf("- %s (recurrence: %d): %s\n", f.Approach, f.RecurrenceCount, f.Reason))
            if f.Resolution != "" {
                warning.WriteString(fmt.Sprintf("  Resolution: %s\n", f.Resolution))
            }
        }
        warning.WriteString("[/FAILURE_PATTERN_WARNING]\n")

        data.SystemPrompt = appendSection(data.SystemPrompt, "FAILURE_WARNING", warning.String())
        return data, nil
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// POST-PROMPT HOOKS: Execute after LLM response
// ═══════════════════════════════════════════════════════════════════════════════

// Hook: Episode Observation Recording
// Priority: HookPriorityLate (75)
// Agents: ALL
var EpisodeObservationHook = Hook{
    Name:     "episode_observation_recording",
    Type:     PostPrompt,
    Priority: HookPriorityLate,
    Agents:   []string{"*"},
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        tracker := ctx.Value(episodeTrackerKey).(*EpisodeTracker)
        observationLog := ctx.Value(observationLogKey).(*ObservationLog)

        // Finalize episode with response data
        episode := tracker.FinalizeEpisode(data.Response, data.ToolCalls)

        // Infer behavioral signals
        episode.TaskCompleted = inferTaskCompletion(data.Response)
        episode.HedgingDetected = detectHedging(data.Response)
        episode.ToolCallCount = len(data.ToolCalls)

        // Record to WAL (async processing)
        observationLog.Record(ctx, episode)

        return data, nil
    },
}

// Hook: Access Tracking Update
// Priority: HookPriorityNormal (50)
// Agents: ALL
var AccessTrackingHook = Hook{
    Name:     "access_tracking_update",
    Type:     PostPrompt,
    Priority: HookPriorityNormal,
    Agents:   []string{"*"},
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        tracker := ctx.Value(accessTrackerKey).(*AccessTracker)

        // Extract content IDs referenced in response
        contentIDs := extractContentReferences(data.Response)

        for _, id := range contentIDs {
            tracker.RecordAccess(id, data.TurnNumber, "in_response")
        }

        return data, nil
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// POST-TOOL HOOKS: Execute after tool calls
// ═══════════════════════════════════════════════════════════════════════════════

// Hook: Search Tool Observation
// Priority: HookPriorityNormal (50)
// Agents: ALL
var SearchToolObservationHook = Hook{
    Name:     "search_tool_observation",
    Type:     PostTool,
    Priority: HookPriorityNormal,
    Agents:   []string{"*"},
    Handler: func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
        tracker := ctx.Value(episodeTrackerKey).(*EpisodeTracker)

        // Track searches performed after prefetch
        searchTools := []string{
            "retrieve_context", "search_history", "librarian_search_codebase",
            "academic_search_research", "archivalist_search_decisions",
        }

        for _, tool := range searchTools {
            if data.ToolName == tool {
                // This search was NOT from prefetch - LLM had to search
                query := data.ToolInput["query"].(string)
                tracker.RecordSearchAfterPrefetch(query)
                break
            }
        }

        return data, nil
    },
}

// Hook: Retrieved Content Promotion
// Priority: HookPriorityLate (75)
// Agents: ALL
var ContentPromotionHook = Hook{
    Name:     "retrieved_content_promotion",
    Type:     PostTool,
    Priority: HookPriorityLate,
    Agents:   []string{"*"},
    Handler: func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
        accessTracker := ctx.Value(accessTrackerKey).(*AccessTracker)
        hotCache := ctx.Value(hotCacheKey).(*HotCache)

        // Promote retrieved content to hot cache
        if results, ok := data.ToolOutput.(*RetrievalResult); ok {
            for _, entry := range results.Entries {
                accessTracker.RecordAccess(entry.ID, data.TurnNumber, "tool_retrieved")
                hotCache.Add(entry.ID, entry)
            }
        }

        return data, nil
    },
}

// ═══════════════════════════════════════════════════════════════════════════════
// HOOK REGISTRATION BY AGENT TYPE
// ═══════════════════════════════════════════════════════════════════════════════

func RegisterAdaptiveRetrievalHooks(registry *HookRegistry, deps *HookDependencies) {
    // Universal hooks (all agents)
    registry.RegisterGlobal(EpisodeTrackerInitHook)
    registry.RegisterGlobal(EpisodeObservationHook)
    registry.RegisterGlobal(AccessTrackingHook)
    registry.RegisterGlobal(SearchToolObservationHook)
    registry.RegisterGlobal(ContentPromotionHook)

    // Knowledge agent hooks
    knowledgeAgents := []string{"librarian", "archivalist", "academic", "architect"}
    for _, agent := range knowledgeAgents {
        registry.Register(agent, SpeculativePrefetchHook)
        registry.Register(agent, PressureEvictionHook)
    }

    // Guide hooks
    registry.Register("guide", SpeculativePrefetchHook)
    registry.Register("guide", GuideRoutingCacheHook)

    // Pipeline agent hooks
    pipelineAgents := []string{"engineer", "designer", "inspector", "tester"}
    for _, agent := range pipelineAgents {
        registry.Register(agent, FailurePatternWarningHook)
        registry.Register(agent, FocusedPrefetchHook) // Limited prefetch
    }

    // Orchestrator hooks
    registry.Register("orchestrator", WorkflowContextHook)
}
```

#### 3.7 System Prompt Additions

Each agent requires system prompt additions to leverage the Adaptive Retrieval System:

```go
// core/context/system_prompt_additions.go

// ═══════════════════════════════════════════════════════════════════════════════
// LIBRARIAN SYSTEM PROMPT ADDITION
// ═══════════════════════════════════════════════════════════════════════════════

const LibrarianRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

You have access to an adaptive retrieval system that learns from your behavior. The system
automatically prefetches relevant code before you see queries.

### Context Reference Markers
When you see [CTX-REF:...] markers, these represent evicted context. Example:
[CTX-REF:code_analysis | 15 turns (4,200 tokens) @ 14:23 | Topics: auth flow, JWT | retrieve_context(ref_id="abc123")]

To retrieve the full content: call retrieve_context(ref_id="abc123")

### Auto-Retrieved Content
Content marked [AUTO-RETRIEVED: file:lines | confidence: X.XX] was automatically fetched
based on the query. This content is highly relevant - use it directly.

Content marked [RELATED: file - description, N lines] indicates potentially relevant files.
Only retrieve if the auto-retrieved content is insufficient.

### Search Priority
1. Use prefetched [AUTO-RETRIEVED] content first
2. Use librarian_search_codebase for additional code searches
3. Use librarian_get_symbol_context for deep symbol exploration
4. Use search_history for historical context

### Hot Cache Promotion
When you repeatedly access the same files, call promote_to_hot(content_ids) to keep them
readily available. The system tracks access patterns and optimizes automatically.
`

// ═══════════════════════════════════════════════════════════════════════════════
// ARCHIVALIST SYSTEM PROMPT ADDITION
// ═══════════════════════════════════════════════════════════════════════════════

const ArchivalistRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Failure Pattern Memory
Before storing new failures, the system automatically checks for similar past failures.
You will see [FAILURE_PATTERN_WARNING] blocks when similar approaches have failed before.

CRITICAL: When you see recurrence_count >= 2, this is a RECURRING failure. You MUST:
1. Surface this warning to the requesting agent
2. Include the resolution if one exists
3. Track if the same approach is attempted anyway

### Cross-Session Learning
Use archivalist_get_cross_session_learnings to surface patterns across the user's history.
This is especially valuable for:
- Recurring mistakes (same error in different contexts)
- Successful patterns (approaches that worked before)
- User preferences (how they like things done)

### Decision Search
Use archivalist_search_decisions with outcome_filter to find:
- "successful": Decisions that worked well
- "failed": Decisions that caused problems
- "all": Both (for comprehensive analysis)

### Context Reference Markers
When you see [CTX-REF:...] markers in your context, retrieve the full content using
retrieve_context(ref_id="...") before responding to queries about that content.
`

// ═══════════════════════════════════════════════════════════════════════════════
// ACADEMIC SYSTEM PROMPT ADDITION
// ═══════════════════════════════════════════════════════════════════════════════

const AcademicRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Research Search
Use academic_search_research with appropriate filters:
- source_types: Filter by paper, rfc, documentation, etc.
- min_trust_level: Prefer "verified" or "high" for critical recommendations
- recency: Use "latest" for rapidly-evolving topics

### Applicability Analysis
When include_applicability=true, results include:
- DIRECT: Can be applied as-is to this codebase
- ADAPTABLE: Requires modifications for this codebase
- INCOMPATIBLE: Not suitable for this codebase

ALWAYS check applicability before recommending. Consult Librarian for codebase context.

### Source Citation
Use academic_cite_sources to validate claims before presenting them:
- Find at least 2 sources supporting critical claims
- Note any refuting sources
- Include trust levels in your response

### Best Practices Lookup
Use academic_get_best_practices with codebase_maturity:
- "auto": System asks Librarian for maturity assessment
- "greenfield": New project, flexible
- "legacy": Established patterns, careful changes
- "transitional": Moving between styles
- "disciplined": Strict standards
`

// ═══════════════════════════════════════════════════════════════════════════════
// GUIDE SYSTEM PROMPT ADDITION
// ═══════════════════════════════════════════════════════════════════════════════

const GuideRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Prefetch Sharing
When routing queries to other agents, prefetched context is automatically shared.
The target agent receives [AUTO-RETRIEVED] content without additional latency.

### Routing History
Use guide_get_routing_history to maintain consistency:
- Check how similar queries were routed before
- Note which routings were successful vs failed
- Learn from routing mistakes

### User Preferences
The system learns user preferences automatically. Use guide_get_user_preferences to:
- Check preferred verbosity level
- See agent affinities (which agents for which topics)
- Understand response style preferences

### Context References
When forwarding queries that contain [CTX-REF:...] markers, the full content
will be automatically retrieved and included in the forwarded context.
`

// ═══════════════════════════════════════════════════════════════════════════════
// ENGINEER SYSTEM PROMPT ADDITION
// ═══════════════════════════════════════════════════════════════════════════════

const EngineerRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Limited Direct Retrieval
As a pipeline agent, you have LIMITED retrieval capabilities:
- get_file_context: Quick file lookups
- get_recent_changes: Recent modifications

For broader searches, consult knowledge agents:
- consult_librarian: Codebase patterns, file locations, symbols
- consult_archivalist: Past decisions, failure patterns
- consult_academic: Best practices, research

### Failure Pattern Warnings
When you see [FAILURE_PATTERN_WARNING] blocks, STOP and:
1. Read the warning carefully
2. If recurrence_count >= 2, this approach has failed REPEATEDLY
3. Consider the suggested resolution
4. If you proceed with the same approach anyway, document why

### Auto-Retrieved Context
You may see [AUTO-RETRIEVED] content injected by the system. This is
highly relevant to your current task - use it directly.
`

// ═══════════════════════════════════════════════════════════════════════════════
// ORCHESTRATOR SYSTEM PROMPT ADDITION
// ═══════════════════════════════════════════════════════════════════════════════

const OrchestratorRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Workflow Context
Use orchestrator_get_workflow_context to retrieve:
- Current workflow state and progress
- Past decisions and blockers
- Agent states and artifacts

### Similar Workflow Learning
Use orchestrator_search_similar_workflows BEFORE starting new workflows:
- Find past workflows with similar goals
- Learn from successful approaches
- Avoid approaches that failed

### Context Handoff
When performing Orchestrator handoff at 75% context:
- Workflow context is automatically preserved
- Agent states are captured and transferred
- New Orchestrator receives full context via handoff state
`
```

#### 3.8 Inter-Agent Context Protocols

```go
// core/context/protocols.go

// ═══════════════════════════════════════════════════════════════════════════════
// CONTEXT SHARE PROTOCOL
// ═══════════════════════════════════════════════════════════════════════════════

// ContextShareRequest represents a request from one agent to share context
type ContextShareRequest struct {
    // Request metadata
    RequestID           string    `json:"request_id"`
    RequestingAgentID   string    `json:"requesting_agent_id"`
    RequestingAgentType string    `json:"requesting_agent_type"`
    TargetAgentType     string    `json:"target_agent_type"`
    Timestamp           time.Time `json:"timestamp"`

    // Query specification
    Query               string            `json:"query"`
    MaxTokens           int               `json:"max_tokens"`
    Priority            ContextPriority   `json:"priority"`

    // Filters
    ContentTypes        []ContentType     `json:"content_types,omitempty"`
    Domains             []Domain          `json:"domains,omitempty"`
    TimeRange           *TimeRange        `json:"time_range,omitempty"`
    SessionScope        string            `json:"session_scope"` // "current", "cross_session", "specific"
    SessionIDs          []string          `json:"session_ids,omitempty"`

    // Response preferences
    IncludeReferences   bool              `json:"include_references"`
    IncludeMetadata     bool              `json:"include_metadata"`
}

type ContextPriority int
const (
    ContextPriorityLow      ContextPriority = 0  // Background, can wait
    ContextPriorityNormal   ContextPriority = 1  // Standard request
    ContextPriorityHigh     ContextPriority = 2  // Time-sensitive
    ContextPriorityCritical ContextPriority = 3  // Blocking operation
)

// ContextShareResponse represents shared context from a knowledge agent
type ContextShareResponse struct {
    // Response metadata
    RequestID       string    `json:"request_id"`
    SourceAgentID   string    `json:"source_agent_id"`
    SourceAgentType string    `json:"source_agent_type"`
    Timestamp       time.Time `json:"timestamp"`
    Duration        time.Duration `json:"duration"`

    // Content
    Entries         []*ContentEntry     `json:"entries"`
    References      []*ContextReference `json:"references,omitempty"`
    TotalTokens     int                 `json:"total_tokens"`
    Truncated       bool                `json:"truncated"`

    // Cache hints for recipient
    CacheHint       string              `json:"cache_hint"` // "hot", "warm", "cold"
    SuggestedTTL    time.Duration       `json:"suggested_ttl"`

    // Quality indicators
    Confidence      float64             `json:"confidence"`
    Completeness    float64             `json:"completeness"` // 0-1, how complete the response is
}

// ═══════════════════════════════════════════════════════════════════════════════
// CONSULTATION PROTOCOL (Agent-to-Agent via Guide bypass)
// ═══════════════════════════════════════════════════════════════════════════════

type ConsultationRequest struct {
    // Routing
    SourceAgent     string    `json:"source_agent"`
    TargetAgent     string    `json:"target_agent"`
    CorrelationID   string    `json:"correlation_id"`

    // Query
    Question        string    `json:"question"`
    Intent          string    `json:"intent"` // e.g., "pattern_lookup", "failure_check", "research"
    Context         string    `json:"context"`

    // Constraints
    MaxTokens       int       `json:"max_tokens"`
    Timeout         time.Duration `json:"timeout"`
    Priority        ContextPriority `json:"priority"`

    // Adaptive retrieval hints
    IncludePrefetch bool      `json:"include_prefetch"` // Share prefetched content
    ShareHotCache   bool      `json:"share_hot_cache"`  // Share hot cache state
}

type ConsultationResponse struct {
    // Metadata
    CorrelationID   string    `json:"correlation_id"`
    RespondingAgent string    `json:"responding_agent"`
    Duration        time.Duration `json:"duration"`

    // Response content
    Answer          string    `json:"answer"`
    Confidence      float64   `json:"confidence"`
    Sources         []string  `json:"sources"` // Content IDs used

    // Shared context
    RelevantContent []*ContentEntry `json:"relevant_content,omitempty"`
    SharedPrefetch  *AugmentedQuery `json:"shared_prefetch,omitempty"`

    // Follow-up suggestions
    SuggestedActions []string `json:"suggested_actions,omitempty"`
}

// ═══════════════════════════════════════════════════════════════════════════════
// PREFETCH SHARING PROTOCOL (Guide → Target Agent)
// ═══════════════════════════════════════════════════════════════════════════════

type PrefetchSharePayload struct {
    // Source
    OriginatingQuery string          `json:"originating_query"`
    SourceAgent      string          `json:"source_agent"`

    // Prefetched content
    Excerpts         []Excerpt       `json:"excerpts"`
    Summaries        []Summary       `json:"summaries"`
    TotalTokens      int             `json:"total_tokens"`

    // Adaptive metadata
    SampledWeights   RewardWeights   `json:"sampled_weights"`
    TaskContext      TaskContext     `json:"task_context"`
    Confidence       float64         `json:"confidence"`

    // Cache state
    HotContentIDs    []string        `json:"hot_content_ids"`
}

// ═══════════════════════════════════════════════════════════════════════════════
// HANDOFF CONTEXT PROTOCOL
// ═══════════════════════════════════════════════════════════════════════════════

type HandoffContextPayload struct {
    // Handoff metadata
    HandoffType     string    `json:"handoff_type"` // "agent", "orchestrator", "guide"
    HandoffIndex    int       `json:"handoff_index"`
    HandoffReason   string    `json:"handoff_reason"`

    // Adaptive state snapshot
    AdaptiveStateSnapshot struct {
        WeightsMean     RewardWeights   `json:"weights_mean"`
        ThresholdsMean  ThresholdConfig `json:"thresholds_mean"`
        TotalObservations int64         `json:"total_observations"`
        DiscoveredContexts []TaskContext `json:"discovered_contexts"`
    } `json:"adaptive_state_snapshot"`

    // Hot cache state
    HotContentIDs   []string  `json:"hot_content_ids"`
    HotContentTTLs  map[string]int `json:"hot_content_ttls"` // ID → remaining turns

    // Episode in progress
    CurrentEpisode  *EpisodeObservation `json:"current_episode,omitempty"`
}
```

### 4. Required Architecture Updates Summary

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    REQUIRED ARCHITECTURE UPDATES                                     │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  1. UNIFY STORAGE LAYER                                                             │
│  ════════════════════════                                                           │
│  • UniversalContentStore wraps VectorGraphDB (not parallel implementation)          │
│  • Add Bleve integration to VectorGraphDB                                           │
│  • Single source of truth for all content                                           │
│                                                                                     │
│  2. WAVE 4 COMPLIANCE                                                               │
│  ═════════════════════                                                              │
│  • Replace raw channels with SafeChan                                               │
│  • Replace raw go with GoroutineScope.Go()                                          │
│  • Track file handles through FileHandleBudget                                      │
│  • Use GlobalCircuitBreakerRegistry                                                 │
│  • Register caches with PressureController                                          │
│                                                                                     │
│  3. VECTORGRAPHDB EXTENSIONS                                                        │
│  ═══════════════════════════                                                        │
│  • BleveIntegratedDB for hybrid search                                              │
│  • PressureAwareSearcher for adaptive tier selection                                │
│  • Extended HybridResult for adaptive retrieval fields                              │
│  • SessionScopedView retrieval budget tracking                                      │
│                                                                                     │
│  4. AGENT CAPABILITIES                                                              │
│  ═════════════════════                                                              │
│  • Agent-specific retrieval skills (see matrix above)                               │
│  • Hook registration by agent type                                                  │
│  • Inter-agent context protocols                                                    │
│  • Tool definitions for Anthropic API                                               │
│                                                                                     │
│  5. ADAPTIVE LEARNING INTEGRATION                                                   │
│  ═════════════════════════════════                                                  │
│  • PrePromptPrefetchHook for knowledge agents                                       │
│  • PostToolObservationHook for learning                                             │
│  • PostResponseCompletionHook for episode finalization                              │
│  • EvictionPressureHook for proactive eviction                                      │
│                                                                                     │
│  6. BOUNDED DATA STRUCTURES                                                         │
│  ══════════════════════════                                                         │
│  • AccessTracker with sliding window                                                │
│  • Bounded ring buffer for observation log                                          │
│  • LRU eviction for caches                                                          │
│  • Sufficient statistics only (no raw history)                                      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```
