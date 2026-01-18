# Document Search System

**Local full-text search with Bleve, hybrid retrieval with Vector DB**

---

## Design Principles

1. **Local-first** - All data stays on disk, no external services, portable with the project
2. **Code-aware** - Tokenization understands camelCase, snake_case, and language constructs
3. **Hybrid retrieval** - Lexical precision (Bleve) + semantic understanding (Vector DB)
4. **Standalone usable** - Terminal users get powerful search without agents
5. **Agent-enhanced** - Agents leverage advanced queries, facets, and custom scoring

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         DOCUMENT SEARCH SYSTEM                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  STORAGE                                                                        │
│  ───────────────────────────────────────────────────────────────────────────── │
│  ~/.sylk/projects/<project-hash>/                                              │
│  ├── vector.db              # SQLite - vector embeddings (existing)            │
│  ├── documents.bleve/       # Bleve index directory                            │
│  │   ├── index_meta.json    # Index metadata                                   │
│  │   └── store/             # Scorch segment files                             │
│  ├── manifest.cmt           # Cartesian Merkle Tree manifest                   │
│  └── manifest.cmt.wal       # WAL for crash recovery                           │
│                                                                                 │
│  DOCUMENT TYPES                                                                 │
│  ───────────────────────────────────────────────────────────────────────────── │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐          │
│  │ Source Code  │ │  Markdown    │ │   Config     │ │  LLM Comms   │          │
│  │  .go .ts .py │ │  .md README  │ │ .yaml .json  │ │ prompts/resp │          │
│  └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘          │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐                           │
│  │ Web Fetches  │ │   Notes      │ │  Git Commits │                           │
│  │ cached pages │ │ user created │ │ messages     │                           │
│  └──────────────┘ └──────────────┘ └──────────────┘                           │
│                                                                                 │
│  COMPONENTS                                                                     │
│  ───────────────────────────────────────────────────────────────────────────── │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │                         INDEXING PIPELINE                                │  │
│  │  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐          │  │
│  │  │ Scanner  │───▶│ Parser   │───▶│ Analyzer │───▶│ Indexer  │          │  │
│  │  │          │    │          │    │          │    │          │          │  │
│  │  │ Walk tree│    │ Language │    │ Code-    │    │ Bleve    │          │  │
│  │  │ Filter   │    │ specific │    │ aware    │    │ batch    │          │  │
│  │  │ Hash     │    │ extract  │    │ tokenize │    │ insert   │          │  │
│  │  └──────────┘    └──────────┘    └──────────┘    └──────────┘          │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │                       CHANGE DETECTION                                   │  │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐   │  │
│  │  │  fsnotify    │ │  Git Hooks   │ │  Checksum    │ │  Periodic    │   │  │
│  │  │  (realtime)  │ │  (commits)   │ │  (on-access) │ │  (scheduled) │   │  │
│  │  └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘   │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │                        SEARCH LAYER                                      │  │
│  │                                                                          │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐   │  │
│  │  │                    SEARCH COORDINATOR                            │   │  │
│  │  │         Orchestrates Bleve + Vector DB + Rank Fusion            │   │  │
│  │  └─────────────────────────────────────────────────────────────────┘   │  │
│  │           │                                           │                 │  │
│  │           ▼                                           ▼                 │  │
│  │  ┌─────────────────────┐                 ┌─────────────────────┐       │  │
│  │  │      BLEVE          │                 │     VECTOR DB       │       │  │
│  │  │                     │                 │                     │       │  │
│  │  │  • Full-text search │                 │  • Semantic search  │       │  │
│  │  │  • Fuzzy matching   │                 │  • k-NN similarity  │       │  │
│  │  │  • Faceted search   │                 │  • Conceptual match │       │  │
│  │  │  • Boolean queries  │                 │                     │       │  │
│  │  │  • Range queries    │                 │                     │       │  │
│  │  └─────────────────────┘                 └─────────────────────┘       │  │
│  │                                                                          │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │                        ACCESS LAYER                                      │  │
│  │  ┌──────────────────────────┐    ┌──────────────────────────┐          │  │
│  │  │    TERMINAL CLI          │    │    AGENT API             │          │  │
│  │  │                          │    │                          │          │  │
│  │  │  sylk search <query>     │    │  SearchRequest/Response  │          │  │
│  │  │  --fuzzy --facets        │    │  Query expansion         │          │  │
│  │  │  --type=go --path=core/* │    │  Result synthesis        │          │  │
│  │  │  --interactive           │    │  Context building        │          │  │
│  │  └──────────────────────────┘    └──────────────────────────┘          │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## Document Model

### Core Document Structure

```go
// core/search/document.go

package search

import "time"

// DocumentType categorizes indexed content
type DocumentType string

const (
    DocTypeSourceCode   DocumentType = "source_code"
    DocTypeMarkdown     DocumentType = "markdown"
    DocTypeConfig       DocumentType = "config"
    DocTypeLLMPrompt    DocumentType = "llm_prompt"
    DocTypeLLMResponse  DocumentType = "llm_response"
    DocTypeWebFetch     DocumentType = "web_fetch"
    DocTypeNote         DocumentType = "note"
    DocTypeGitCommit    DocumentType = "git_commit"
)

// Document represents an indexed document
type Document struct {
    // Identity
    ID       string `json:"id"`        // Content hash (SHA-256)
    Path     string `json:"path"`      // File path or virtual path

    // Classification
    Type     DocumentType `json:"type"`
    Language string       `json:"language,omitempty"` // go, typescript, python, etc.
    Package  string       `json:"package,omitempty"`  // Package/module name

    // Searchable Content
    Content  string   `json:"content"`            // Full text content
    Symbols  []string `json:"symbols,omitempty"`  // Functions, types, variables
    Comments string   `json:"comments,omitempty"` // Extracted comments
    Imports  []string `json:"imports,omitempty"`  // Dependencies

    // Metadata
    Title       string            `json:"title,omitempty"`       // For markdown, web fetches
    Description string            `json:"description,omitempty"` // Summary
    Tags        []string          `json:"tags,omitempty"`        // User or auto-generated tags
    Metadata    map[string]string `json:"metadata,omitempty"`    // Extensible metadata

    // Metrics
    Lines     int   `json:"lines"`
    SizeBytes int64 `json:"size_bytes"`

    // Temporal
    CreatedAt  time.Time `json:"created_at"`
    ModifiedAt time.Time `json:"modified_at"`
    IndexedAt  time.Time `json:"indexed_at"`

    // Git Integration
    GitCommit string `json:"git_commit,omitempty"` // Last commit touching this file
    GitAuthor string `json:"git_author,omitempty"` // Author of last commit
    GitBranch string `json:"git_branch,omitempty"` // Current branch

    // Change Detection
    Checksum string `json:"checksum"` // For detecting modifications
}

// SymbolInfo provides detailed symbol information
type SymbolInfo struct {
    Name      string `json:"name"`
    Kind      string `json:"kind"`       // function, type, variable, const, interface
    Signature string `json:"signature"`  // Full signature for functions
    Line      int    `json:"line"`       // Line number
    Exported  bool   `json:"exported"`   // Public/private
}

// DocumentWithSymbols includes parsed symbol details
type DocumentWithSymbols struct {
    Document
    SymbolDetails []SymbolInfo `json:"symbol_details,omitempty"`
}
```

### LLM Communication Documents

```go
// core/search/llm_document.go

package search

import "time"

// LLMPromptDocument represents an indexed prompt
type LLMPromptDocument struct {
    Document

    // LLM-specific fields
    SessionID   string    `json:"session_id"`
    AgentID     string    `json:"agent_id"`
    AgentType   string    `json:"agent_type"`   // Engineer, Designer, Reviewer, etc.
    Model       string    `json:"model"`        // claude-3-opus, etc.
    TokenCount  int       `json:"token_count"`
    TurnNumber  int       `json:"turn_number"`

    // Context
    PrecedingFiles []string `json:"preceding_files,omitempty"` // Files referenced before
    FollowingFiles []string `json:"following_files,omitempty"` // Files referenced after
}

// LLMResponseDocument represents an indexed LLM response
type LLMResponseDocument struct {
    Document

    // Response-specific fields
    PromptID       string        `json:"prompt_id"`   // Links to prompt
    SessionID      string        `json:"session_id"`
    AgentID        string        `json:"agent_id"`
    AgentType      string        `json:"agent_type"`
    Model          string        `json:"model"`
    TokenCount     int           `json:"token_count"`
    LatencyMs      int64         `json:"latency_ms"`
    StopReason     string        `json:"stop_reason"` // end_turn, max_tokens, etc.

    // Extracted elements
    CodeBlocks     []CodeBlock   `json:"code_blocks,omitempty"`
    FilesModified  []string      `json:"files_modified,omitempty"`
    ToolsUsed      []string      `json:"tools_used,omitempty"`
}

// CodeBlock represents extracted code from a response
type CodeBlock struct {
    Language string `json:"language"`
    Content  string `json:"content"`
    LineNum  int    `json:"line_num"` // Line in response where block starts
}

// WebFetchDocument represents a cached web page
type WebFetchDocument struct {
    Document

    // Web-specific fields
    URL           string    `json:"url"`
    FetchedAt     time.Time `json:"fetched_at"`
    StatusCode    int       `json:"status_code"`
    ContentType   string    `json:"content_type"`

    // Extraction
    Links         []string  `json:"links,omitempty"`          // Outbound links
    Headings      []string  `json:"headings,omitempty"`       // H1, H2, etc.
    CodeSnippets  []string  `json:"code_snippets,omitempty"`  // Extracted code
}
```

---

## Bleve Index Configuration

### Custom Code Analyzer

```go
// core/search/analyzer.go

package search

import (
    "regexp"
    "strings"
    "unicode"

    "github.com/blevesearch/bleve/v2"
    "github.com/blevesearch/bleve/v2/analysis"
    "github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
    "github.com/blevesearch/bleve/v2/analysis/token/lowercase"
    "github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
    "github.com/blevesearch/bleve/v2/registry"
)

const (
    CodeAnalyzerName       = "code"
    CamelCaseFilterName    = "camel_case"
    SnakeCaseFilterName    = "snake_case"
    CodeTokenizerName      = "code_tokenizer"
)

func init() {
    // Register custom token filters
    registry.RegisterTokenFilter(CamelCaseFilterName, NewCamelCaseFilter)
    registry.RegisterTokenFilter(SnakeCaseFilterName, NewSnakeCaseFilter)
    registry.RegisterTokenizer(CodeTokenizerName, NewCodeTokenizer)
}

// CamelCaseFilter splits camelCase and PascalCase identifiers
// "handleHTTPError" → ["handle", "HTTP", "Error", "handleHTTPError"]
type CamelCaseFilter struct{}

func NewCamelCaseFilter(config map[string]interface{}, cache *registry.Cache) (analysis.TokenFilter, error) {
    return &CamelCaseFilter{}, nil
}

func (f *CamelCaseFilter) Filter(input analysis.TokenStream) analysis.TokenStream {
    var output analysis.TokenStream

    for _, token := range input {
        // Keep original token
        output = append(output, token)

        // Split camelCase
        parts := splitCamelCase(string(token.Term))
        if len(parts) > 1 {
            for _, part := range parts {
                if len(part) > 1 { // Skip single chars
                    newToken := &analysis.Token{
                        Term:     []byte(strings.ToLower(part)),
                        Position: token.Position,
                        Start:    token.Start,
                        End:      token.End,
                        Type:     token.Type,
                    }
                    output = append(output, newToken)
                }
            }
        }
    }

    return output
}

var camelCaseRegex = regexp.MustCompile(`([a-z])([A-Z])|([A-Z]+)([A-Z][a-z])`)

func splitCamelCase(s string) []string {
    // Insert spaces before uppercase letters
    result := camelCaseRegex.ReplaceAllString(s, "${1}${3} ${2}${4}")
    parts := strings.Fields(result)
    return parts
}

// SnakeCaseFilter splits snake_case identifiers
// "get_user_by_id" → ["get", "user", "by", "id", "get_user_by_id"]
type SnakeCaseFilter struct{}

func NewSnakeCaseFilter(config map[string]interface{}, cache *registry.Cache) (analysis.TokenFilter, error) {
    return &SnakeCaseFilter{}, nil
}

func (f *SnakeCaseFilter) Filter(input analysis.TokenStream) analysis.TokenStream {
    var output analysis.TokenStream

    for _, token := range input {
        term := string(token.Term)

        // Keep original token
        output = append(output, token)

        // Split on underscores
        if strings.Contains(term, "_") {
            parts := strings.Split(term, "_")
            for _, part := range parts {
                if len(part) > 1 { // Skip single chars
                    newToken := &analysis.Token{
                        Term:     []byte(strings.ToLower(part)),
                        Position: token.Position,
                        Start:    token.Start,
                        End:      token.End,
                        Type:     token.Type,
                    }
                    output = append(output, newToken)
                }
            }
        }
    }

    return output
}

// CodeTokenizer handles code-specific tokenization
// Preserves dots for package paths, handles operators, etc.
type CodeTokenizer struct {
    inner *unicode.Tokenizer
}

func NewCodeTokenizer(config map[string]interface{}, cache *registry.Cache) (analysis.Tokenizer, error) {
    return &CodeTokenizer{
        inner: unicode.NewUnicodeTokenizer(),
    }, nil
}

func (t *CodeTokenizer) Tokenize(input []byte) analysis.TokenStream {
    // Use unicode tokenizer as base
    tokens := t.inner.Tokenize(input)

    // Post-process to handle code-specific patterns
    var output analysis.TokenStream
    for _, token := range tokens {
        term := string(token.Term)

        // Skip pure punctuation/operators
        if isPureOperator(term) {
            continue
        }

        // Keep meaningful tokens
        output = append(output, token)
    }

    return output
}

func isPureOperator(s string) bool {
    for _, r := range s {
        if unicode.IsLetter(r) || unicode.IsDigit(r) {
            return false
        }
    }
    return true
}
```

### Index Mapping Configuration

```go
// core/search/index.go

package search

import (
    "github.com/blevesearch/bleve/v2"
    "github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
    "github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
    "github.com/blevesearch/bleve/v2/mapping"
)

// IndexConfig holds index configuration
type IndexConfig struct {
    Path             string
    BatchSize        int
    MaxConcurrent    int
}

// DefaultIndexConfig returns sensible defaults
func DefaultIndexConfig(basePath string) IndexConfig {
    return IndexConfig{
        Path:          basePath + "/documents.bleve",
        BatchSize:     100,
        MaxConcurrent: 4,
    }
}

// BuildIndexMapping creates the Bleve index mapping
func BuildIndexMapping() (mapping.IndexMapping, error) {
    indexMapping := bleve.NewIndexMapping()

    // Register custom analyzers
    err := indexMapping.AddCustomAnalyzer(CodeAnalyzerName, map[string]interface{}{
        "type":      custom.Name,
        "tokenizer": CodeTokenizerName,
        "token_filters": []string{
            lowercase.Name,
            CamelCaseFilterName,
            SnakeCaseFilterName,
            "porter", // Stemming
        },
    })
    if err != nil {
        return nil, err
    }

    // Default analyzer for general text
    indexMapping.DefaultAnalyzer = standard.Name

    // Document type mapping
    docMapping := bleve.NewDocumentMapping()

    // === Identity Fields ===

    // ID: stored but not indexed (lookup by ID is direct)
    idField := bleve.NewTextFieldMapping()
    idField.Index = false
    idField.Store = true
    docMapping.AddFieldMappingsAt("id", idField)

    // Path: keyword analyzer for exact matching + prefix queries
    pathField := bleve.NewTextFieldMapping()
    pathField.Analyzer = keyword.Name
    pathField.Store = true
    docMapping.AddFieldMappingsAt("path", pathField)

    // === Classification Fields ===

    // Type: keyword for faceting
    typeField := bleve.NewTextFieldMapping()
    typeField.Analyzer = keyword.Name
    typeField.Store = true
    docMapping.AddFieldMappingsAt("type", typeField)

    // Language: keyword for faceting
    langField := bleve.NewTextFieldMapping()
    langField.Analyzer = keyword.Name
    langField.Store = true
    docMapping.AddFieldMappingsAt("language", langField)

    // Package: keyword for faceting
    pkgField := bleve.NewTextFieldMapping()
    pkgField.Analyzer = keyword.Name
    pkgField.Store = true
    docMapping.AddFieldMappingsAt("package", pkgField)

    // === Searchable Content Fields ===

    // Content: code analyzer, not stored (too large)
    contentField := bleve.NewTextFieldMapping()
    contentField.Analyzer = CodeAnalyzerName
    contentField.Store = false
    contentField.IncludeTermVectors = true // For highlighting
    docMapping.AddFieldMappingsAt("content", contentField)

    // Symbols: code analyzer, stored, high boost
    symbolsField := bleve.NewTextFieldMapping()
    symbolsField.Analyzer = CodeAnalyzerName
    symbolsField.Store = true
    docMapping.AddFieldMappingsAt("symbols", symbolsField)

    // Comments: standard analyzer, not stored
    commentsField := bleve.NewTextFieldMapping()
    commentsField.Analyzer = standard.Name
    commentsField.Store = false
    docMapping.AddFieldMappingsAt("comments", commentsField)

    // Imports: keyword analyzer for exact matching
    importsField := bleve.NewTextFieldMapping()
    importsField.Analyzer = keyword.Name
    importsField.Store = true
    docMapping.AddFieldMappingsAt("imports", importsField)

    // === Metadata Fields ===

    // Title: standard analyzer, stored
    titleField := bleve.NewTextFieldMapping()
    titleField.Analyzer = standard.Name
    titleField.Store = true
    docMapping.AddFieldMappingsAt("title", titleField)

    // Description: standard analyzer, stored
    descField := bleve.NewTextFieldMapping()
    descField.Analyzer = standard.Name
    descField.Store = true
    docMapping.AddFieldMappingsAt("description", descField)

    // Tags: keyword for exact matching + faceting
    tagsField := bleve.NewTextFieldMapping()
    tagsField.Analyzer = keyword.Name
    tagsField.Store = true
    docMapping.AddFieldMappingsAt("tags", tagsField)

    // === Numeric Fields ===

    // Lines: numeric for range queries
    linesField := bleve.NewNumericFieldMapping()
    linesField.Store = true
    docMapping.AddFieldMappingsAt("lines", linesField)

    // SizeBytes: numeric for range queries
    sizeField := bleve.NewNumericFieldMapping()
    sizeField.Store = true
    docMapping.AddFieldMappingsAt("size_bytes", sizeField)

    // === Temporal Fields ===

    // ModifiedAt: datetime for recency queries
    modifiedField := bleve.NewDateTimeFieldMapping()
    modifiedField.Store = true
    docMapping.AddFieldMappingsAt("modified_at", modifiedField)

    // CreatedAt: datetime
    createdField := bleve.NewDateTimeFieldMapping()
    createdField.Store = true
    docMapping.AddFieldMappingsAt("created_at", createdField)

    // IndexedAt: datetime
    indexedField := bleve.NewDateTimeFieldMapping()
    indexedField.Store = true
    docMapping.AddFieldMappingsAt("indexed_at", indexedField)

    // === Git Fields ===

    // GitCommit: keyword
    commitField := bleve.NewTextFieldMapping()
    commitField.Analyzer = keyword.Name
    commitField.Store = true
    docMapping.AddFieldMappingsAt("git_commit", commitField)

    // GitAuthor: keyword for faceting
    authorField := bleve.NewTextFieldMapping()
    authorField.Analyzer = keyword.Name
    authorField.Store = true
    docMapping.AddFieldMappingsAt("git_author", authorField)

    // GitBranch: keyword
    branchField := bleve.NewTextFieldMapping()
    branchField.Analyzer = keyword.Name
    branchField.Store = true
    docMapping.AddFieldMappingsAt("git_branch", branchField)

    // Register document mapping
    indexMapping.AddDocumentMapping("document", docMapping)
    indexMapping.DefaultMapping = docMapping

    return indexMapping, nil
}
```

### Index Manager

```go
// core/search/index_manager.go

package search

import (
    "context"
    "fmt"
    "os"
    "sync"

    "github.com/blevesearch/bleve/v2"
)

// IndexManager manages the Bleve index lifecycle
type IndexManager struct {
    index   bleve.Index
    config  IndexConfig
    mu      sync.RWMutex
    closed  bool
}

// NewIndexManager creates or opens an index
func NewIndexManager(config IndexConfig) (*IndexManager, error) {
    var index bleve.Index
    var err error

    // Try to open existing index
    index, err = bleve.Open(config.Path)
    if err == bleve.ErrorIndexPathDoesNotExist {
        // Create new index
        mapping, err := BuildIndexMapping()
        if err != nil {
            return nil, fmt.Errorf("build mapping: %w", err)
        }

        index, err = bleve.New(config.Path, mapping)
        if err != nil {
            return nil, fmt.Errorf("create index: %w", err)
        }
    } else if err != nil {
        return nil, fmt.Errorf("open index: %w", err)
    }

    return &IndexManager{
        index:  index,
        config: config,
    }, nil
}

// Index adds or updates a document
func (m *IndexManager) Index(ctx context.Context, doc *Document) error {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.closed {
        return fmt.Errorf("index is closed")
    }

    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
    }

    return m.index.Index(doc.ID, doc)
}

// IndexBatch adds or updates multiple documents
func (m *IndexManager) IndexBatch(ctx context.Context, docs []*Document) error {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.closed {
        return fmt.Errorf("index is closed")
    }

    batch := m.index.NewBatch()

    for _, doc := range docs {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        if err := batch.Index(doc.ID, doc); err != nil {
            return fmt.Errorf("batch index %s: %w", doc.Path, err)
        }

        // Commit batch at configured size
        if batch.Size() >= m.config.BatchSize {
            if err := m.index.Batch(batch); err != nil {
                return fmt.Errorf("commit batch: %w", err)
            }
            batch = m.index.NewBatch()
        }
    }

    // Commit remaining
    if batch.Size() > 0 {
        if err := m.index.Batch(batch); err != nil {
            return fmt.Errorf("commit final batch: %w", err)
        }
    }

    return nil
}

// Delete removes a document by ID
func (m *IndexManager) Delete(ctx context.Context, id string) error {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.closed {
        return fmt.Errorf("index is closed")
    }

    return m.index.Delete(id)
}

// DeleteByPath removes a document by path
func (m *IndexManager) DeleteByPath(ctx context.Context, path string) error {
    // First find the document ID by path
    query := bleve.NewTermQuery(path)
    query.SetField("path")

    search := bleve.NewSearchRequest(query)
    search.Size = 1
    search.Fields = []string{"id"}

    result, err := m.index.Search(search)
    if err != nil {
        return fmt.Errorf("search by path: %w", err)
    }

    if len(result.Hits) == 0 {
        return nil // Document not found, nothing to delete
    }

    return m.Delete(ctx, result.Hits[0].ID)
}

// Search executes a search request
func (m *IndexManager) Search(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.closed {
        return nil, fmt.Errorf("index is closed")
    }

    return m.index.SearchInContext(ctx, req)
}

// DocumentCount returns the number of indexed documents
func (m *IndexManager) DocumentCount() (uint64, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.closed {
        return 0, fmt.Errorf("index is closed")
    }

    return m.index.DocCount()
}

// Close closes the index
func (m *IndexManager) Close() error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.closed {
        return nil
    }

    m.closed = true
    return m.index.Close()
}

// Destroy closes and removes the index
func (m *IndexManager) Destroy() error {
    if err := m.Close(); err != nil {
        return err
    }
    return os.RemoveAll(m.config.Path)
}
```

---

## Indexing Pipeline

### Scanner

```go
// core/search/scanner.go

package search

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "io/fs"
    "os"
    "path/filepath"
    "strings"

    gitignore "github.com/sabhiram/go-gitignore"
)

// ScanConfig configures the scanner
type ScanConfig struct {
    RootPath       string
    IncludeHidden  bool
    MaxFileSize    int64    // Skip files larger than this (bytes)
    IncludeExts    []string // Only include these extensions (empty = all)
    ExcludeExts    []string // Exclude these extensions
    ExcludePaths   []string // Glob patterns to exclude
    UseGitignore   bool     // Respect .gitignore
}

// DefaultScanConfig returns sensible defaults
func DefaultScanConfig(rootPath string) ScanConfig {
    return ScanConfig{
        RootPath:      rootPath,
        IncludeHidden: false,
        MaxFileSize:   1024 * 1024, // 1MB
        ExcludeExts: []string{
            ".exe", ".dll", ".so", ".dylib",           // Binaries
            ".zip", ".tar", ".gz", ".rar",              // Archives
            ".png", ".jpg", ".jpeg", ".gif", ".ico",   // Images
            ".pdf", ".doc", ".docx",                    // Documents
            ".mp3", ".mp4", ".wav", ".avi",            // Media
            ".lock", ".sum",                            // Lock files
        },
        ExcludePaths: []string{
            "**/node_modules/**",
            "**/.git/**",
            "**/vendor/**",
            "**/__pycache__/**",
            "**/dist/**",
            "**/build/**",
            "**/.next/**",
            "**/target/**",
        },
        UseGitignore: true,
    }
}

// ScannedFile represents a file found by the scanner
type ScannedFile struct {
    Path         string
    RelativePath string
    Size         int64
    ModTime      int64 // Unix timestamp
    Checksum     string
    Content      []byte
}

// Scanner walks a directory tree
type Scanner struct {
    config     ScanConfig
    gitIgnore  *gitignore.GitIgnore
    excludeSet map[string]bool
}

// NewScanner creates a scanner
func NewScanner(config ScanConfig) (*Scanner, error) {
    s := &Scanner{
        config:     config,
        excludeSet: make(map[string]bool),
    }

    // Build exclude extension set
    for _, ext := range config.ExcludeExts {
        s.excludeSet[strings.ToLower(ext)] = true
    }

    // Load .gitignore if configured
    if config.UseGitignore {
        gitignorePath := filepath.Join(config.RootPath, ".gitignore")
        if gi, err := gitignore.CompileIgnoreFile(gitignorePath); err == nil {
            s.gitIgnore = gi
        }
    }

    return s, nil
}

// Scan walks the directory and emits files to the channel
func (s *Scanner) Scan(ctx context.Context) (<-chan *ScannedFile, <-chan error) {
    files := make(chan *ScannedFile, 100)
    errs := make(chan error, 1)

    go func() {
        defer close(files)
        defer close(errs)

        err := filepath.WalkDir(s.config.RootPath, func(path string, d fs.DirEntry, err error) error {
            if err != nil {
                return err
            }

            select {
            case <-ctx.Done():
                return ctx.Err()
            default:
            }

            // Get relative path
            relPath, _ := filepath.Rel(s.config.RootPath, path)

            // Skip hidden files/directories
            if !s.config.IncludeHidden && strings.HasPrefix(d.Name(), ".") {
                if d.IsDir() {
                    return filepath.SkipDir
                }
                return nil
            }

            // Check gitignore
            if s.gitIgnore != nil && s.gitIgnore.MatchesPath(relPath) {
                if d.IsDir() {
                    return filepath.SkipDir
                }
                return nil
            }

            // Check exclude patterns
            for _, pattern := range s.config.ExcludePaths {
                if matched, _ := filepath.Match(pattern, relPath); matched {
                    if d.IsDir() {
                        return filepath.SkipDir
                    }
                    return nil
                }
                // Also check with ** prefix handling
                if strings.Contains(pattern, "**") {
                    // Simplified ** handling
                    simplified := strings.ReplaceAll(pattern, "**", "*")
                    if matched, _ := filepath.Match(simplified, relPath); matched {
                        if d.IsDir() {
                            return filepath.SkipDir
                        }
                        return nil
                    }
                }
            }

            // Skip directories themselves (we process their contents)
            if d.IsDir() {
                return nil
            }

            // Check extension
            ext := strings.ToLower(filepath.Ext(path))
            if s.excludeSet[ext] {
                return nil
            }

            // Check include extensions (if specified)
            if len(s.config.IncludeExts) > 0 {
                found := false
                for _, incExt := range s.config.IncludeExts {
                    if strings.ToLower(incExt) == ext {
                        found = true
                        break
                    }
                }
                if !found {
                    return nil
                }
            }

            // Get file info
            info, err := d.Info()
            if err != nil {
                return nil // Skip files we can't stat
            }

            // Check size
            if info.Size() > s.config.MaxFileSize {
                return nil
            }

            // Read content and compute checksum
            content, err := os.ReadFile(path)
            if err != nil {
                return nil // Skip files we can't read
            }

            checksum := computeChecksum(content)

            file := &ScannedFile{
                Path:         path,
                RelativePath: relPath,
                Size:         info.Size(),
                ModTime:      info.ModTime().Unix(),
                Checksum:     checksum,
                Content:      content,
            }

            select {
            case files <- file:
            case <-ctx.Done():
                return ctx.Err()
            }

            return nil
        })

        if err != nil && err != context.Canceled {
            errs <- err
        }
    }()

    return files, errs
}

func computeChecksum(content []byte) string {
    hash := sha256.Sum256(content)
    return hex.EncodeToString(hash[:])
}
```

### Parser

```go
// core/search/parser.go

package search

import (
    "bufio"
    "bytes"
    "path/filepath"
    "regexp"
    "strings"
    "time"
)

// Parser extracts structured information from files
type Parser struct {
    languageParsers map[string]LanguageParser
}

// LanguageParser handles language-specific parsing
type LanguageParser interface {
    ParseSymbols(content []byte) []SymbolInfo
    ExtractComments(content []byte) string
    ExtractImports(content []byte) []string
}

// NewParser creates a parser with registered language parsers
func NewParser() *Parser {
    p := &Parser{
        languageParsers: make(map[string]LanguageParser),
    }

    // Register language parsers
    p.languageParsers["go"] = &GoParser{}
    p.languageParsers["typescript"] = &TypeScriptParser{}
    p.languageParsers["javascript"] = &TypeScriptParser{} // Similar enough
    p.languageParsers["python"] = &PythonParser{}

    return p
}

// Parse converts a scanned file to a document
func (p *Parser) Parse(file *ScannedFile) *Document {
    ext := strings.ToLower(filepath.Ext(file.Path))
    lang := extensionToLanguage(ext)
    docType := extensionToDocType(ext)

    doc := &Document{
        ID:         file.Checksum, // Use checksum as ID
        Path:       file.RelativePath,
        Type:       docType,
        Language:   lang,
        Content:    string(file.Content),
        Lines:      countLines(file.Content),
        SizeBytes:  file.Size,
        ModifiedAt: time.Unix(file.ModTime, 0),
        IndexedAt:  time.Now(),
        Checksum:   file.Checksum,
    }

    // Language-specific parsing
    if parser, ok := p.languageParsers[lang]; ok {
        symbols := parser.ParseSymbols(file.Content)
        doc.Symbols = make([]string, len(symbols))
        for i, s := range symbols {
            doc.Symbols[i] = s.Name
        }
        doc.Comments = parser.ExtractComments(file.Content)
        doc.Imports = parser.ExtractImports(file.Content)
    }

    // Extract package name
    doc.Package = extractPackage(file.Content, lang)

    return doc
}

func countLines(content []byte) int {
    return bytes.Count(content, []byte{'\n'}) + 1
}

func extensionToLanguage(ext string) string {
    switch ext {
    case ".go":
        return "go"
    case ".ts", ".tsx":
        return "typescript"
    case ".js", ".jsx":
        return "javascript"
    case ".py":
        return "python"
    case ".rs":
        return "rust"
    case ".java":
        return "java"
    case ".rb":
        return "ruby"
    case ".md", ".markdown":
        return "markdown"
    case ".yaml", ".yml":
        return "yaml"
    case ".json":
        return "json"
    case ".toml":
        return "toml"
    default:
        return ""
    }
}

func extensionToDocType(ext string) DocumentType {
    switch ext {
    case ".md", ".markdown":
        return DocTypeMarkdown
    case ".yaml", ".yml", ".json", ".toml":
        return DocTypeConfig
    default:
        return DocTypeSourceCode
    }
}

func extractPackage(content []byte, lang string) string {
    switch lang {
    case "go":
        re := regexp.MustCompile(`package\s+(\w+)`)
        if match := re.FindSubmatch(content); len(match) > 1 {
            return string(match[1])
        }
    case "python":
        // Python doesn't have explicit packages in file, use directory
        return ""
    case "typescript", "javascript":
        // Could extract from package.json name
        return ""
    }
    return ""
}

// GoParser handles Go source files
type GoParser struct{}

func (p *GoParser) ParseSymbols(content []byte) []SymbolInfo {
    var symbols []SymbolInfo

    // Function pattern
    funcRe := regexp.MustCompile(`func\s+(?:\([^)]+\)\s+)?(\w+)\s*\(`)
    for _, match := range funcRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "function",
            })
        }
    }

    // Type pattern
    typeRe := regexp.MustCompile(`type\s+(\w+)\s+(?:struct|interface)`)
    for _, match := range typeRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "type",
            })
        }
    }

    // Const/Var pattern
    constRe := regexp.MustCompile(`(?:const|var)\s+(\w+)\s+`)
    for _, match := range constRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "variable",
            })
        }
    }

    return symbols
}

func (p *GoParser) ExtractComments(content []byte) string {
    var comments []string

    scanner := bufio.NewScanner(bytes.NewReader(content))
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if strings.HasPrefix(line, "//") {
            comments = append(comments, strings.TrimPrefix(line, "//"))
        }
    }

    // Also extract block comments
    blockRe := regexp.MustCompile(`/\*([^*]|\*[^/])*\*/`)
    for _, match := range blockRe.FindAll(content, -1) {
        comment := string(match)
        comment = strings.TrimPrefix(comment, "/*")
        comment = strings.TrimSuffix(comment, "*/")
        comments = append(comments, comment)
    }

    return strings.Join(comments, " ")
}

func (p *GoParser) ExtractImports(content []byte) []string {
    var imports []string

    // Single import
    singleRe := regexp.MustCompile(`import\s+"([^"]+)"`)
    for _, match := range singleRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            imports = append(imports, string(match[1]))
        }
    }

    // Import block
    blockRe := regexp.MustCompile(`import\s+\(([^)]+)\)`)
    if match := blockRe.FindSubmatch(content); len(match) > 1 {
        importRe := regexp.MustCompile(`"([^"]+)"`)
        for _, m := range importRe.FindAllSubmatch(match[1], -1) {
            if len(m) > 1 {
                imports = append(imports, string(m[1]))
            }
        }
    }

    return imports
}

// TypeScriptParser handles TypeScript/JavaScript files
type TypeScriptParser struct{}

func (p *TypeScriptParser) ParseSymbols(content []byte) []SymbolInfo {
    var symbols []SymbolInfo

    // Function declarations
    funcRe := regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+(\w+)`)
    for _, match := range funcRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "function",
            })
        }
    }

    // Arrow functions assigned to const/let
    arrowRe := regexp.MustCompile(`(?:export\s+)?(?:const|let)\s+(\w+)\s*=\s*(?:async\s+)?(?:\([^)]*\)|[^=])\s*=>`)
    for _, match := range arrowRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "function",
            })
        }
    }

    // Classes
    classRe := regexp.MustCompile(`(?:export\s+)?class\s+(\w+)`)
    for _, match := range classRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "class",
            })
        }
    }

    // Interfaces
    interfaceRe := regexp.MustCompile(`(?:export\s+)?interface\s+(\w+)`)
    for _, match := range interfaceRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "interface",
            })
        }
    }

    // Types
    typeRe := regexp.MustCompile(`(?:export\s+)?type\s+(\w+)\s*=`)
    for _, match := range typeRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "type",
            })
        }
    }

    return symbols
}

func (p *TypeScriptParser) ExtractComments(content []byte) string {
    var comments []string

    scanner := bufio.NewScanner(bytes.NewReader(content))
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if strings.HasPrefix(line, "//") {
            comments = append(comments, strings.TrimPrefix(line, "//"))
        }
    }

    // JSDoc comments
    jsdocRe := regexp.MustCompile(`/\*\*([^*]|\*[^/])*\*/`)
    for _, match := range jsdocRe.FindAll(content, -1) {
        comment := string(match)
        comment = strings.TrimPrefix(comment, "/**")
        comment = strings.TrimSuffix(comment, "*/")
        comments = append(comments, comment)
    }

    return strings.Join(comments, " ")
}

func (p *TypeScriptParser) ExtractImports(content []byte) []string {
    var imports []string

    importRe := regexp.MustCompile(`import\s+(?:.*?\s+from\s+)?['"]([^'"]+)['"]`)
    for _, match := range importRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            imports = append(imports, string(match[1]))
        }
    }

    return imports
}

// PythonParser handles Python files
type PythonParser struct{}

func (p *PythonParser) ParseSymbols(content []byte) []SymbolInfo {
    var symbols []SymbolInfo

    // Function definitions
    funcRe := regexp.MustCompile(`def\s+(\w+)\s*\(`)
    for _, match := range funcRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "function",
            })
        }
    }

    // Class definitions
    classRe := regexp.MustCompile(`class\s+(\w+)`)
    for _, match := range classRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            symbols = append(symbols, SymbolInfo{
                Name: string(match[1]),
                Kind: "class",
            })
        }
    }

    return symbols
}

func (p *PythonParser) ExtractComments(content []byte) string {
    var comments []string

    // Line comments
    scanner := bufio.NewScanner(bytes.NewReader(content))
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if strings.HasPrefix(line, "#") {
            comments = append(comments, strings.TrimPrefix(line, "#"))
        }
    }

    // Docstrings
    docstringRe := regexp.MustCompile(`"""([^"]|"[^"]|""[^"])*"""`)
    for _, match := range docstringRe.FindAll(content, -1) {
        comment := string(match)
        comment = strings.Trim(comment, `"`)
        comments = append(comments, comment)
    }

    return strings.Join(comments, " ")
}

func (p *PythonParser) ExtractImports(content []byte) []string {
    var imports []string

    // import x
    importRe := regexp.MustCompile(`^import\s+(\w+)`)
    for _, match := range importRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            imports = append(imports, string(match[1]))
        }
    }

    // from x import y
    fromRe := regexp.MustCompile(`from\s+(\w+(?:\.\w+)*)\s+import`)
    for _, match := range fromRe.FindAllSubmatch(content, -1) {
        if len(match) > 1 {
            imports = append(imports, string(match[1]))
        }
    }

    return imports
}
```

### Indexing Coordinator

```go
// core/search/indexer.go

package search

import (
    "context"
    "fmt"
    "sync"
    "sync/atomic"
    "time"
)

// IndexerConfig configures the indexer
type IndexerConfig struct {
    Workers    int
    BatchSize  int
    RootPath   string
}

// DefaultIndexerConfig returns sensible defaults
func DefaultIndexerConfig(rootPath string) IndexerConfig {
    return IndexerConfig{
        Workers:   4,
        BatchSize: 50,
        RootPath:  rootPath,
    }
}

// IndexProgress reports indexing progress
type IndexProgress struct {
    TotalFiles     int64
    ProcessedFiles int64
    IndexedFiles   int64
    SkippedFiles   int64
    ErrorCount     int64
    Duration       time.Duration
    CurrentFile    string
}

// Indexer coordinates the indexing pipeline
type Indexer struct {
    config       IndexerConfig
    indexManager *IndexManager
    scanner      *Scanner
    parser       *Parser

    // Progress tracking
    progress atomic.Value // *IndexProgress
}

// NewIndexer creates an indexer
func NewIndexer(config IndexerConfig, indexManager *IndexManager) (*Indexer, error) {
    scanConfig := DefaultScanConfig(config.RootPath)
    scanner, err := NewScanner(scanConfig)
    if err != nil {
        return nil, fmt.Errorf("create scanner: %w", err)
    }

    return &Indexer{
        config:       config,
        indexManager: indexManager,
        scanner:      scanner,
        parser:       NewParser(),
    }, nil
}

// IndexAll performs a full index of the configured path
func (idx *Indexer) IndexAll(ctx context.Context, progressCh chan<- *IndexProgress) error {
    startTime := time.Now()

    // Initialize progress
    progress := &IndexProgress{}
    idx.progress.Store(progress)

    // Start scanner
    fileCh, errCh := idx.scanner.Scan(ctx)

    // Worker pool for parsing and indexing
    var wg sync.WaitGroup
    docCh := make(chan *Document, idx.config.BatchSize)

    // Start parser workers
    for i := 0; i < idx.config.Workers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for file := range fileCh {
                atomic.AddInt64(&progress.ProcessedFiles, 1)
                progress.CurrentFile = file.RelativePath

                doc := idx.parser.Parse(file)

                select {
                case docCh <- doc:
                case <-ctx.Done():
                    return
                }
            }
        }()
    }

    // Close docCh when all parsers are done
    go func() {
        wg.Wait()
        close(docCh)
    }()

    // Batch indexer
    var batch []*Document
    for doc := range docCh {
        batch = append(batch, doc)

        if len(batch) >= idx.config.BatchSize {
            if err := idx.indexManager.IndexBatch(ctx, batch); err != nil {
                atomic.AddInt64(&progress.ErrorCount, 1)
            } else {
                atomic.AddInt64(&progress.IndexedFiles, int64(len(batch)))
            }
            batch = batch[:0]

            // Report progress
            if progressCh != nil {
                progress.Duration = time.Since(startTime)
                select {
                case progressCh <- progress:
                default:
                }
            }
        }
    }

    // Index remaining batch
    if len(batch) > 0 {
        if err := idx.indexManager.IndexBatch(ctx, batch); err != nil {
            atomic.AddInt64(&progress.ErrorCount, 1)
        } else {
            atomic.AddInt64(&progress.IndexedFiles, int64(len(batch)))
        }
    }

    // Check for scanner errors
    select {
    case err := <-errCh:
        if err != nil {
            return fmt.Errorf("scan error: %w", err)
        }
    default:
    }

    // Final progress report
    progress.Duration = time.Since(startTime)
    if progressCh != nil {
        progressCh <- progress
    }

    return nil
}

// Progress returns the current indexing progress
func (idx *Indexer) Progress() *IndexProgress {
    if p := idx.progress.Load(); p != nil {
        return p.(*IndexProgress)
    }
    return &IndexProgress{}
}
```

---

## Change Detection

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           CHANGE DETECTION LAYERS                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  LAYER 1: FILE SYSTEM WATCHER (Real-time)                                       │
│  ─────────────────────────────────────────                                      │
│  • fsnotify for CREATE/MODIFY/DELETE/RENAME events                             │
│  • Debounce rapid changes (100ms window)                                       │
│  • Queue for batch processing                                                  │
│                                                                                 │
│  LAYER 2: GIT INTEGRATION (Commit-based)                                        │
│  ─────────────────────────────────────────                                      │
│  • Post-commit hook triggers re-index of changed files                         │
│  • `git diff --name-only HEAD~1` for affected files                            │
│  • Branch switch detection triggers relevant file re-index                     │
│                                                                                 │
│  LAYER 3: CHECKSUM VALIDATION (On-access)                                       │
│  ─────────────────────────────────────────                                      │
│  • When document retrieved, verify checksum against current file               │
│  • Stale? Re-index immediately, return fresh result                            │
│  • Lazy validation - only when accessed                                        │
│                                                                                 │
│  LAYER 4: PERIODIC RECONCILIATION (Scheduled)                                   │
│  ─────────────────────────────────────────────                                  │
│  • Every 5 minutes: sample random files, verify checksums                      │
│  • If >10% stale: trigger incremental re-index                                 │
│  • Nightly: full reconciliation scan                                           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### File Watcher

```go
// core/search/watcher.go

package search

import (
    "context"
    "path/filepath"
    "strings"
    "sync"
    "time"

    "github.com/fsnotify/fsnotify"
)

// WatcherConfig configures the file watcher
type WatcherConfig struct {
    RootPath       string
    DebounceDelay  time.Duration
    BatchSize      int
    ExcludePaths   []string
}

// DefaultWatcherConfig returns sensible defaults
func DefaultWatcherConfig(rootPath string) WatcherConfig {
    return WatcherConfig{
        RootPath:      rootPath,
        DebounceDelay: 100 * time.Millisecond,
        BatchSize:     20,
        ExcludePaths: []string{
            ".git",
            "node_modules",
            "vendor",
            "__pycache__",
            ".bleve",
        },
    }
}

// FileChange represents a detected change
type FileChange struct {
    Path      string
    Operation ChangeOperation
    Time      time.Time
}

// ChangeOperation represents the type of change
type ChangeOperation int

const (
    OpCreate ChangeOperation = iota
    OpModify
    OpDelete
    OpRename
)

// Watcher monitors file system changes
type Watcher struct {
    config   WatcherConfig
    watcher  *fsnotify.Watcher
    indexer  *Indexer

    // Debouncing
    pending  map[string]*FileChange
    mu       sync.Mutex
    timer    *time.Timer

    // Control
    done     chan struct{}
    running  bool
}

// NewWatcher creates a file watcher
func NewWatcher(config WatcherConfig, indexer *Indexer) (*Watcher, error) {
    fsWatcher, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }

    return &Watcher{
        config:  config,
        watcher: fsWatcher,
        indexer: indexer,
        pending: make(map[string]*FileChange),
        done:    make(chan struct{}),
    }, nil
}

// Start begins watching for changes
func (w *Watcher) Start(ctx context.Context) error {
    if w.running {
        return nil
    }
    w.running = true

    // Add root path recursively
    if err := w.addRecursive(w.config.RootPath); err != nil {
        return err
    }

    go w.eventLoop(ctx)

    return nil
}

// Stop stops watching
func (w *Watcher) Stop() error {
    if !w.running {
        return nil
    }
    close(w.done)
    w.running = false
    return w.watcher.Close()
}

func (w *Watcher) addRecursive(path string) error {
    return filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
        if err != nil {
            return nil // Skip errors
        }

        // Skip excluded paths
        for _, exclude := range w.config.ExcludePaths {
            if strings.Contains(p, exclude) {
                if info.IsDir() {
                    return filepath.SkipDir
                }
                return nil
            }
        }

        if info.IsDir() {
            return w.watcher.Add(p)
        }
        return nil
    })
}

func (w *Watcher) eventLoop(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-w.done:
            return

        case event, ok := <-w.watcher.Events:
            if !ok {
                return
            }
            w.handleEvent(event)

        case err, ok := <-w.watcher.Errors:
            if !ok {
                return
            }
            // Log error but continue
            _ = err
        }
    }
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
    // Skip excluded paths
    for _, exclude := range w.config.ExcludePaths {
        if strings.Contains(event.Name, exclude) {
            return
        }
    }

    // Map fsnotify operation to our operation
    var op ChangeOperation
    switch {
    case event.Op&fsnotify.Create != 0:
        op = OpCreate
        // If directory created, watch it
        if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
            w.watcher.Add(event.Name)
        }
    case event.Op&fsnotify.Write != 0:
        op = OpModify
    case event.Op&fsnotify.Remove != 0:
        op = OpDelete
    case event.Op&fsnotify.Rename != 0:
        op = OpRename
    default:
        return
    }

    // Add to pending with debounce
    w.mu.Lock()
    w.pending[event.Name] = &FileChange{
        Path:      event.Name,
        Operation: op,
        Time:      time.Now(),
    }

    // Reset debounce timer
    if w.timer != nil {
        w.timer.Stop()
    }
    w.timer = time.AfterFunc(w.config.DebounceDelay, w.processPending)
    w.mu.Unlock()
}

func (w *Watcher) processPending() {
    w.mu.Lock()
    changes := make([]*FileChange, 0, len(w.pending))
    for _, change := range w.pending {
        changes = append(changes, change)
    }
    w.pending = make(map[string]*FileChange)
    w.mu.Unlock()

    if len(changes) == 0 {
        return
    }

    // Process changes
    ctx := context.Background()
    for _, change := range changes {
        switch change.Operation {
        case OpCreate, OpModify:
            // Re-index the file
            w.reindexFile(ctx, change.Path)
        case OpDelete:
            // Remove from index
            w.indexer.indexManager.DeleteByPath(ctx, change.Path)
        case OpRename:
            // Handle as delete + create
            w.indexer.indexManager.DeleteByPath(ctx, change.Path)
        }
    }
}

func (w *Watcher) reindexFile(ctx context.Context, path string) {
    // Read file
    content, err := os.ReadFile(path)
    if err != nil {
        return
    }

    info, err := os.Stat(path)
    if err != nil {
        return
    }

    relPath, _ := filepath.Rel(w.config.RootPath, path)

    file := &ScannedFile{
        Path:         path,
        RelativePath: relPath,
        Size:         info.Size(),
        ModTime:      info.ModTime().Unix(),
        Checksum:     computeChecksum(content),
        Content:      content,
    }

    doc := w.indexer.parser.Parse(file)
    w.indexer.indexManager.Index(ctx, doc)
}
```

---

## Search Layer

### Search Coordinator (Hybrid Search)

```go
// core/search/coordinator.go

package search

import (
    "context"
    "sort"
    "sync"

    "github.com/blevesearch/bleve/v2"
)

// SearchMode determines search strategy
type SearchMode string

const (
    SearchModeHybrid   SearchMode = "hybrid"   // Bleve + Vector (default)
    SearchModeLexical  SearchMode = "lexical"  // Bleve only
    SearchModeSemantic SearchMode = "semantic" // Vector only
)

// SearchRequest represents a search query
type SearchRequest struct {
    Query      string
    Mode       SearchMode

    // Filters
    Types      []DocumentType // Filter by document type
    Languages  []string       // Filter by language
    Packages   []string       // Filter by package
    Paths      []string       // Glob patterns for path filtering
    Authors    []string       // Git authors

    // Time filters
    ModifiedAfter  *time.Time
    ModifiedBefore *time.Time

    // Numeric filters
    MinLines int
    MaxLines int

    // Options
    Fuzzy           bool    // Enable fuzzy matching
    FuzzyDistance   int     // Levenshtein distance (default 2)
    IncludeFacets   bool    // Return facet counts
    HighlightFields []string

    // Pagination
    Offset int
    Limit  int

    // Hybrid options
    LexicalWeight  float64 // Weight for Bleve results (default 0.4)
    SemanticWeight float64 // Weight for Vector results (default 0.6)
}

// DefaultSearchRequest returns a search request with sensible defaults
func DefaultSearchRequest(query string) *SearchRequest {
    return &SearchRequest{
        Query:          query,
        Mode:           SearchModeHybrid,
        Fuzzy:          true,
        FuzzyDistance:  2,
        Limit:          20,
        LexicalWeight:  0.4,
        SemanticWeight: 0.6,
    }
}

// SearchResult represents search results
type SearchResult struct {
    Hits       []*SearchHit
    TotalHits  int64
    Duration   time.Duration
    Facets     map[string][]FacetCount
    Query      string
}

// SearchHit represents a single search result
type SearchHit struct {
    Document   *Document
    Score      float64
    Highlights map[string][]string // Field -> highlighted fragments

    // Source tracking for hybrid search
    LexicalRank  int
    SemanticRank int
    LexicalScore float64
    VectorScore  float64
}

// FacetCount represents a facet value and its count
type FacetCount struct {
    Value string
    Count int
}

// SearchCoordinator orchestrates hybrid search
type SearchCoordinator struct {
    bleveIndex  *IndexManager
    vectorDB    VectorSearcher // Interface to existing vector DB

    // Configuration
    rrf_k int // RRF constant (default 60)
}

// VectorSearcher interface for vector DB integration
type VectorSearcher interface {
    Search(ctx context.Context, query string, limit int) ([]VectorResult, error)
}

// VectorResult from vector DB
type VectorResult struct {
    DocumentID string
    Score      float64 // Cosine similarity
}

// NewSearchCoordinator creates a search coordinator
func NewSearchCoordinator(bleveIndex *IndexManager, vectorDB VectorSearcher) *SearchCoordinator {
    return &SearchCoordinator{
        bleveIndex: bleveIndex,
        vectorDB:   vectorDB,
        rrf_k:      60,
    }
}

// Search performs a search based on the request
func (c *SearchCoordinator) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
    startTime := time.Now()

    switch req.Mode {
    case SearchModeLexical:
        return c.searchLexical(ctx, req, startTime)
    case SearchModeSemantic:
        return c.searchSemantic(ctx, req, startTime)
    default: // Hybrid
        return c.searchHybrid(ctx, req, startTime)
    }
}

func (c *SearchCoordinator) searchLexical(ctx context.Context, req *SearchRequest, startTime time.Time) (*SearchResult, error) {
    bleveReq := c.buildBleveRequest(req)

    bleveResult, err := c.bleveIndex.Search(ctx, bleveReq)
    if err != nil {
        return nil, err
    }

    hits := make([]*SearchHit, len(bleveResult.Hits))
    for i, hit := range bleveResult.Hits {
        hits[i] = &SearchHit{
            Document: &Document{
                ID:   hit.ID,
                Path: hit.Fields["path"].(string),
            },
            Score:        hit.Score,
            Highlights:   hit.Fragments,
            LexicalRank:  i + 1,
            LexicalScore: hit.Score,
        }
    }

    result := &SearchResult{
        Hits:      hits,
        TotalHits: int64(bleveResult.Total),
        Duration:  time.Since(startTime),
        Query:     req.Query,
    }

    // Add facets if requested
    if req.IncludeFacets {
        result.Facets = c.extractFacets(bleveResult)
    }

    return result, nil
}

func (c *SearchCoordinator) searchSemantic(ctx context.Context, req *SearchRequest, startTime time.Time) (*SearchResult, error) {
    vectorResults, err := c.vectorDB.Search(ctx, req.Query, req.Limit*2)
    if err != nil {
        return nil, err
    }

    hits := make([]*SearchHit, len(vectorResults))
    for i, vr := range vectorResults {
        hits[i] = &SearchHit{
            Document: &Document{
                ID: vr.DocumentID,
            },
            Score:        vr.Score,
            SemanticRank: i + 1,
            VectorScore:  vr.Score,
        }
    }

    // Limit results
    if len(hits) > req.Limit {
        hits = hits[:req.Limit]
    }

    return &SearchResult{
        Hits:      hits,
        TotalHits: int64(len(hits)),
        Duration:  time.Since(startTime),
        Query:     req.Query,
    }, nil
}

func (c *SearchCoordinator) searchHybrid(ctx context.Context, req *SearchRequest, startTime time.Time) (*SearchResult, error) {
    // Execute both searches in parallel
    var wg sync.WaitGroup
    var lexicalResults *bleve.SearchResult
    var vectorResults []VectorResult
    var lexicalErr, vectorErr error

    wg.Add(2)

    go func() {
        defer wg.Done()
        bleveReq := c.buildBleveRequest(req)
        bleveReq.Size = req.Limit * 2 // Get more for fusion
        lexicalResults, lexicalErr = c.bleveIndex.Search(ctx, bleveReq)
    }()

    go func() {
        defer wg.Done()
        vectorResults, vectorErr = c.vectorDB.Search(ctx, req.Query, req.Limit*2)
    }()

    wg.Wait()

    // Handle errors (graceful degradation)
    if lexicalErr != nil && vectorErr != nil {
        return nil, fmt.Errorf("both searches failed: lexical=%v, semantic=%v", lexicalErr, vectorErr)
    }

    // Fuse results using Reciprocal Rank Fusion
    hits := c.fuseResults(lexicalResults, vectorResults, req)

    // Limit final results
    if len(hits) > req.Limit {
        hits = hits[:req.Limit]
    }

    result := &SearchResult{
        Hits:      hits,
        TotalHits: int64(len(hits)),
        Duration:  time.Since(startTime),
        Query:     req.Query,
    }

    // Add facets from lexical results
    if req.IncludeFacets && lexicalResults != nil {
        result.Facets = c.extractFacets(lexicalResults)
    }

    return result, nil
}

// fuseResults implements Reciprocal Rank Fusion (RRF)
func (c *SearchCoordinator) fuseResults(lexical *bleve.SearchResult, vector []VectorResult, req *SearchRequest) []*SearchHit {
    // Build maps for rank lookup
    lexicalRanks := make(map[string]int)
    vectorRanks := make(map[string]int)

    if lexical != nil {
        for i, hit := range lexical.Hits {
            lexicalRanks[hit.ID] = i + 1
        }
    }

    for i, vr := range vector {
        vectorRanks[vr.DocumentID] = i + 1
    }

    // Collect all unique document IDs
    docIDs := make(map[string]bool)
    if lexical != nil {
        for _, hit := range lexical.Hits {
            docIDs[hit.ID] = true
        }
    }
    for _, vr := range vector {
        docIDs[vr.DocumentID] = true
    }

    // Calculate RRF scores
    type scoredDoc struct {
        id           string
        rrfScore     float64
        lexicalRank  int
        semanticRank int
    }

    var scored []scoredDoc
    for id := range docIDs {
        var score float64
        lexRank := lexicalRanks[id]
        vecRank := vectorRanks[id]

        // RRF formula: sum of 1/(k + rank) for each ranking
        if lexRank > 0 {
            score += req.LexicalWeight * (1.0 / float64(c.rrf_k+lexRank))
        }
        if vecRank > 0 {
            score += req.SemanticWeight * (1.0 / float64(c.rrf_k+vecRank))
        }

        scored = append(scored, scoredDoc{
            id:           id,
            rrfScore:     score,
            lexicalRank:  lexRank,
            semanticRank: vecRank,
        })
    }

    // Sort by RRF score
    sort.Slice(scored, func(i, j int) bool {
        return scored[i].rrfScore > scored[j].rrfScore
    })

    // Convert to SearchHits
    hits := make([]*SearchHit, len(scored))
    for i, s := range scored {
        hits[i] = &SearchHit{
            Document: &Document{
                ID: s.id,
            },
            Score:        s.rrfScore,
            LexicalRank:  s.lexicalRank,
            SemanticRank: s.semanticRank,
        }
    }

    return hits
}

func (c *SearchCoordinator) buildBleveRequest(req *SearchRequest) *bleve.SearchRequest {
    var query bleve.Query

    if req.Fuzzy {
        fuzzyQuery := bleve.NewFuzzyQuery(req.Query)
        fuzzyQuery.Fuzziness = req.FuzzyDistance
        query = fuzzyQuery
    } else {
        query = bleve.NewMatchQuery(req.Query)
    }

    // Apply filters using boolean query
    if len(req.Types) > 0 || len(req.Languages) > 0 || len(req.Packages) > 0 {
        boolQuery := bleve.NewBooleanQuery()
        boolQuery.AddMust(query)

        // Type filter
        if len(req.Types) > 0 {
            typeQueries := make([]bleve.Query, len(req.Types))
            for i, t := range req.Types {
                tq := bleve.NewTermQuery(string(t))
                tq.SetField("type")
                typeQueries[i] = tq
            }
            boolQuery.AddMust(bleve.NewDisjunctionQuery(typeQueries...))
        }

        // Language filter
        if len(req.Languages) > 0 {
            langQueries := make([]bleve.Query, len(req.Languages))
            for i, l := range req.Languages {
                lq := bleve.NewTermQuery(l)
                lq.SetField("language")
                langQueries[i] = lq
            }
            boolQuery.AddMust(bleve.NewDisjunctionQuery(langQueries...))
        }

        // Package filter
        if len(req.Packages) > 0 {
            pkgQueries := make([]bleve.Query, len(req.Packages))
            for i, p := range req.Packages {
                pq := bleve.NewTermQuery(p)
                pq.SetField("package")
                pkgQueries[i] = pq
            }
            boolQuery.AddMust(bleve.NewDisjunctionQuery(pkgQueries...))
        }

        query = boolQuery
    }

    searchReq := bleve.NewSearchRequest(query)
    searchReq.From = req.Offset
    searchReq.Size = req.Limit

    // Add highlighting
    if len(req.HighlightFields) > 0 {
        searchReq.Highlight = bleve.NewHighlight()
        for _, field := range req.HighlightFields {
            searchReq.Highlight.AddField(field)
        }
    }

    // Add facets
    if req.IncludeFacets {
        searchReq.AddFacet("languages", bleve.NewFacetRequest("language", 10))
        searchReq.AddFacet("types", bleve.NewFacetRequest("type", 10))
        searchReq.AddFacet("packages", bleve.NewFacetRequest("package", 20))
        searchReq.AddFacet("authors", bleve.NewFacetRequest("git_author", 10))
    }

    // Store fields we need
    searchReq.Fields = []string{"path", "type", "language", "package", "title", "symbols"}

    return searchReq
}

func (c *SearchCoordinator) extractFacets(result *bleve.SearchResult) map[string][]FacetCount {
    facets := make(map[string][]FacetCount)

    for name, facetResult := range result.Facets {
        counts := make([]FacetCount, len(facetResult.Terms.Terms()))
        for i, term := range facetResult.Terms.Terms() {
            counts[i] = FacetCount{
                Value: term.Term,
                Count: term.Count,
            }
        }
        facets[name] = counts
    }

    return facets
}
```

---

## Terminal CLI Interface

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         TERMINAL SEARCH INTERFACE                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  COMMAND STRUCTURE                                                              │
│  ─────────────────                                                              │
│  sylk search [flags] <query>                                                   │
│  sylk index [flags]                                                            │
│  sylk index status                                                             │
│                                                                                 │
│  SEARCH FLAGS                                                                   │
│  ────────────                                                                   │
│  --mode=hybrid|lexical|semantic    Search mode (default: hybrid)               │
│  --fuzzy                           Enable fuzzy matching (default: true)       │
│  --type=go,ts,md                   Filter by file type                         │
│  --lang=go,typescript              Filter by language                          │
│  --path="core/**"                  Filter by path glob                         │
│  --package=llm,session             Filter by package                           │
│  --author=ada                      Filter by git author                        │
│  --modified=7d                     Modified within duration                    │
│  --min-lines=50                    Minimum line count                          │
│  --max-lines=500                   Maximum line count                          │
│  --limit=20                        Maximum results (default: 20)               │
│  --offset=0                        Pagination offset                           │
│  --facets                          Show facet counts                           │
│                                                                                 │
│  OUTPUT FLAGS                                                                   │
│  ────────────                                                                   │
│  --format=default|paths|json|interactive                                       │
│  --no-highlight                    Disable match highlighting                  │
│  --no-color                        Disable color output                        │
│                                                                                 │
│  INDEX FLAGS                                                                    │
│  ───────────                                                                    │
│  sylk index                        Full re-index of project                    │
│  sylk index --incremental          Incremental update only                     │
│  sylk index status                 Show index statistics                       │
│  sylk index clear                  Clear the index                             │
│                                                                                 │
│  EXAMPLES                                                                       │
│  ────────                                                                       │
│  $ sylk search "circuit breaker"                                               │
│  $ sylk search "error handling" --type=go --path="core/**"                     │
│  $ sylk search "TODO" --modified=7d --facets                                   │
│  $ sylk search "handleError" --mode=lexical --fuzzy=false                      │
│  $ sylk search "code that retries failures" --mode=semantic                    │
│  $ sylk search "auth" --format=paths | xargs vim                               │
│  $ sylk search "config" --format=interactive                                   │
│                                                                                 │
│  OUTPUT FORMATS                                                                 │
│  ──────────────                                                                 │
│                                                                                 │
│  DEFAULT:                                                                       │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ $ sylk search "circuit breaker" --type=go                               │   │
│  │                                                                          │   │
│  │ core/llm/circuit_breaker.go:45  [go] [package: llm]                     │   │
│  │   │ 43│ // CircuitBreaker implements the circuit breaker pattern        │   │
│  │   │ 44│ // for protecting against cascading failures.                   │   │
│  │ ▶ │ 45│ type «CircuitBreaker» struct {                                  │   │
│  │   │ 46│     state      atomic.Value                                     │   │
│  │                                                                          │   │
│  │ core/llm/client.go:156  [go] [package: llm]                             │   │
│  │   │154│ func (c *Client) executeWithBreaker(ctx context.Context,        │   │
│  │ ▶ │156│     if !c.«circuitBreaker».Allow() {                            │   │
│  │   │157│         return nil, ErrCircuitOpen                              │   │
│  │                                                                          │   │
│  │ Found 12 results in 0.023s (hybrid: 40% lexical, 60% semantic)          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  PATHS (for piping):                                                           │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ $ sylk search "TODO" --format=paths                                     │   │
│  │ core/llm/client.go:45                                                   │   │
│  │ core/session/manager.go:123                                             │   │
│  │ core/tools/executor.go:89                                               │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  JSON (for tooling):                                                           │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ $ sylk search "error" --format=json --limit=2                           │   │
│  │ {                                                                        │   │
│  │   "query": "error",                                                     │   │
│  │   "total_hits": 145,                                                    │   │
│  │   "duration_ms": 23,                                                    │   │
│  │   "hits": [                                                             │   │
│  │     {                                                                   │   │
│  │       "path": "core/llm/client.go",                                     │   │
│  │       "score": 0.892,                                                   │   │
│  │       "language": "go",                                                 │   │
│  │       "symbols": ["handleError", "wrapError"],                          │   │
│  │       "lexical_rank": 1,                                                │   │
│  │       "semantic_rank": 3                                                │   │
│  │     },                                                                  │   │
│  │     ...                                                                 │   │
│  │   ]                                                                     │   │
│  │ }                                                                        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  INTERACTIVE (TUI picker):                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ Search: circuit breaker█                                      [hybrid] │   │
│  ├─────────────────────────────────────────────────────────────────────────┤   │
│  │ > core/llm/circuit_breaker.go:45    type CircuitBreaker struct         │   │
│  │   core/llm/client.go:156            circuitBreaker.Allow()              │   │
│  │   core/llm/health.go:89             breaker.RecordSuccess()             │   │
│  │   tests/circuit_test.go:23          TestCircuitBreakerTrip              │   │
│  ├─────────────────────────────────────────────────────────────────────────┤   │
│  │ Facets: go(4) typescript(0) | llm(3) health(1) | source(3) test(1)     │   │
│  ├─────────────────────────────────────────────────────────────────────────┤   │
│  │ [Enter] Open  [Tab] Preview  [Ctrl+Y] Copy  [/] Filter  [Esc] Cancel   │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  WITH FACETS:                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ $ sylk search "error" --facets                                          │   │
│  │                                                                          │   │
│  │ ─── Facets ───────────────────────────────────────────────────────────  │   │
│  │ Language:    go (89)  typescript (34)  python (12)  markdown (10)       │   │
│  │ Package:     llm (23)  core (18)  tools (15)  session (12)  ui (8)     │   │
│  │ Type:        source (112)  test (28)  docs (5)                          │   │
│  │ Author:      ada (98)  bob (32)  charlie (15)                           │   │
│  │ ─────────────────────────────────────────────────────────────────────── │   │
│  │                                                                          │   │
│  │ [Results follow...]                                                     │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## Agent Search API

```go
// core/search/agent_api.go

package search

import (
    "context"
)

// AgentSearchIntent describes what the agent is trying to accomplish
type AgentSearchIntent string

const (
    IntentUnderstand  AgentSearchIntent = "understand"  // Understand how something works
    IntentFind        AgentSearchIntent = "find"        // Find specific code/files
    IntentCompare     AgentSearchIntent = "compare"     // Compare implementations
    IntentTrace       AgentSearchIntent = "trace"       // Trace execution flow
    IntentDiscover    AgentSearchIntent = "discover"    // Discover related code
)

// AgentSearchRequest extends SearchRequest with agent-specific options
type AgentSearchRequest struct {
    SearchRequest

    // Agent context
    Intent           AgentSearchIntent
    AgentType        string            // Academic, Archivist, Librarian
    SessionContext   []string          // Recent files/topics in session

    // Enhanced options
    ExpandQuery      bool              // Let agent expand query terms
    FollowReferences bool              // Follow imports/calls
    MaxDepth         int               // Reference follow depth
    IncludeContext   bool              // Fetch surrounding code
    ContextLines     int               // Lines of context (default 5)

    // Synthesis options
    Summarize        bool              // Generate summary of results
    GroupBy          string            // Group results (package, type, author)
    SuggestFollowUp  bool              // Suggest follow-up queries
}

// AgentSearchResult extends SearchResult with agent-specific data
type AgentSearchResult struct {
    SearchResult

    // Enhanced results
    RelatedFiles     []string          // Files to also consider
    CallGraph        *CallGraphSnippet // If reference following enabled

    // Synthesis
    Summary          string            // Agent-generated summary
    Groupings        map[string][]*SearchHit // Grouped results
    FollowUpQueries  []string          // Suggested refinements

    // Query expansion info
    ExpandedTerms    []string          // Terms added by expansion
    OriginalQuery    string            // Pre-expansion query
}

// CallGraphSnippet represents a portion of the call graph
type CallGraphSnippet struct {
    Root      string              // Starting point
    Callers   []CallReference     // Who calls this
    Callees   []CallReference     // What this calls
}

// CallReference represents a call relationship
type CallReference struct {
    From     string // Caller
    To       string // Callee
    File     string
    Line     int
    Context  string // Surrounding code
}

// AgentSearchAPI provides agent-specific search capabilities
type AgentSearchAPI struct {
    coordinator *SearchCoordinator
    expander    *QueryExpander
    synthesizer *ResultSynthesizer
}

// NewAgentSearchAPI creates an agent search API
func NewAgentSearchAPI(coordinator *SearchCoordinator) *AgentSearchAPI {
    return &AgentSearchAPI{
        coordinator: coordinator,
        expander:    NewQueryExpander(),
        synthesizer: NewResultSynthesizer(),
    }
}

// Search performs an agent-enhanced search
func (api *AgentSearchAPI) Search(ctx context.Context, req *AgentSearchRequest) (*AgentSearchResult, error) {
    result := &AgentSearchResult{
        OriginalQuery: req.Query,
    }

    // 1. Query expansion (if enabled)
    if req.ExpandQuery {
        expanded := api.expander.Expand(req.Query, req.Intent, req.SessionContext)
        req.Query = expanded.Query
        result.ExpandedTerms = expanded.AddedTerms
    }

    // 2. Execute base search
    baseResult, err := api.coordinator.Search(ctx, &req.SearchRequest)
    if err != nil {
        return nil, err
    }
    result.SearchResult = *baseResult

    // 3. Follow references (if enabled)
    if req.FollowReferences && len(result.Hits) > 0 {
        result.RelatedFiles = api.findRelatedFiles(ctx, result.Hits, req.MaxDepth)
        result.CallGraph = api.buildCallGraph(ctx, result.Hits[0], req.MaxDepth)
    }

    // 4. Group results (if requested)
    if req.GroupBy != "" {
        result.Groupings = api.groupResults(result.Hits, req.GroupBy)
    }

    // 5. Generate summary (if requested)
    if req.Summarize {
        result.Summary = api.synthesizer.Summarize(result.Hits, req.Intent)
    }

    // 6. Suggest follow-ups (if requested)
    if req.SuggestFollowUp {
        result.FollowUpQueries = api.synthesizer.SuggestFollowUps(req.Query, result.Hits)
    }

    return result, nil
}

// QueryExpander expands queries based on intent and context
type QueryExpander struct {
    synonyms map[string][]string
}

// NewQueryExpander creates a query expander
func NewQueryExpander() *QueryExpander {
    return &QueryExpander{
        synonyms: map[string][]string{
            "error":   {"err", "exception", "failure", "fault"},
            "auth":    {"authentication", "authorization", "login", "session"},
            "config":  {"configuration", "settings", "options", "preferences"},
            "db":      {"database", "storage", "persistence", "repository"},
            "api":     {"endpoint", "handler", "route", "service"},
            "test":    {"spec", "unit", "integration", "mock"},
            "async":   {"concurrent", "parallel", "goroutine", "channel"},
            "cache":   {"memoize", "store", "buffer"},
        },
    }
}

// ExpandedQuery represents an expanded query
type ExpandedQuery struct {
    Query      string
    AddedTerms []string
}

// Expand expands a query based on intent and context
func (e *QueryExpander) Expand(query string, intent AgentSearchIntent, context []string) *ExpandedQuery {
    result := &ExpandedQuery{
        Query: query,
    }

    // Add synonyms
    for term, syns := range e.synonyms {
        if strings.Contains(strings.ToLower(query), term) {
            for _, syn := range syns {
                if !strings.Contains(strings.ToLower(query), syn) {
                    result.AddedTerms = append(result.AddedTerms, syn)
                }
            }
        }
    }

    // Build expanded query
    if len(result.AddedTerms) > 0 {
        // Original query with OR'd synonyms
        parts := []string{query}
        parts = append(parts, result.AddedTerms...)
        result.Query = strings.Join(parts, " OR ")
    }

    return result
}

// ResultSynthesizer generates summaries and suggestions
type ResultSynthesizer struct{}

// NewResultSynthesizer creates a result synthesizer
func NewResultSynthesizer() *ResultSynthesizer {
    return &ResultSynthesizer{}
}

// Summarize generates a summary of search results
func (s *ResultSynthesizer) Summarize(hits []*SearchHit, intent AgentSearchIntent) string {
    if len(hits) == 0 {
        return "No results found."
    }

    // Group by package
    packages := make(map[string]int)
    for _, hit := range hits {
        if hit.Document != nil && hit.Document.Package != "" {
            packages[hit.Document.Package]++
        }
    }

    // Build summary
    var summary strings.Builder
    summary.WriteString(fmt.Sprintf("Found %d results. ", len(hits)))

    if len(packages) > 0 {
        summary.WriteString("Distributed across packages: ")
        for pkg, count := range packages {
            summary.WriteString(fmt.Sprintf("%s(%d) ", pkg, count))
        }
    }

    return summary.String()
}

// SuggestFollowUps suggests follow-up queries
func (s *ResultSynthesizer) SuggestFollowUps(query string, hits []*SearchHit) []string {
    var suggestions []string

    // Suggest narrowing by top packages
    packages := make(map[string]int)
    for _, hit := range hits {
        if hit.Document != nil && hit.Document.Package != "" {
            packages[hit.Document.Package]++
        }
    }

    for pkg := range packages {
        suggestions = append(suggestions, fmt.Sprintf("%s package:%s", query, pkg))
    }

    // Suggest related searches based on common symbols
    symbols := make(map[string]int)
    for _, hit := range hits {
        if hit.Document != nil {
            for _, sym := range hit.Document.Symbols {
                symbols[sym]++
            }
        }
    }

    // Find most common symbols (not in original query)
    for sym, count := range symbols {
        if count > 2 && !strings.Contains(strings.ToLower(query), strings.ToLower(sym)) {
            suggestions = append(suggestions, sym)
        }
    }

    return suggestions
}

func (api *AgentSearchAPI) findRelatedFiles(ctx context.Context, hits []*SearchHit, maxDepth int) []string {
    related := make(map[string]bool)

    for _, hit := range hits {
        if hit.Document == nil {
            continue
        }

        // Add imported files
        for _, imp := range hit.Document.Imports {
            related[imp] = true
        }
    }

    var result []string
    for file := range related {
        result = append(result, file)
    }

    return result
}

func (api *AgentSearchAPI) buildCallGraph(ctx context.Context, hit *SearchHit, maxDepth int) *CallGraphSnippet {
    // Placeholder - would need actual code analysis
    return &CallGraphSnippet{
        Root: hit.Document.Path,
    }
}

func (api *AgentSearchAPI) groupResults(hits []*SearchHit, groupBy string) map[string][]*SearchHit {
    groups := make(map[string][]*SearchHit)

    for _, hit := range hits {
        if hit.Document == nil {
            continue
        }

        var key string
        switch groupBy {
        case "package":
            key = hit.Document.Package
        case "type":
            key = string(hit.Document.Type)
        case "language":
            key = hit.Document.Language
        case "author":
            key = hit.Document.GitAuthor
        default:
            key = "other"
        }

        if key == "" {
            key = "(none)"
        }

        groups[key] = append(groups[key], hit)
    }

    return groups
}
```

---

## Agent-Specific Search Strategies

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                      AGENT-SPECIFIC SEARCH STRATEGIES                            │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ACADEMIC AGENT                                                                 │
│  ──────────────                                                                 │
│  Focus: Research, citations, cross-referencing, chronological understanding    │
│                                                                                 │
│  Strategies:                                                                    │
│  • Citation chaining: Find all files that reference a given symbol/file        │
│  • Chronological ordering: Sort by git commit date to show evolution           │
│  • Cross-reference validation: Verify references are still valid               │
│  • Bibliography building: Collect all related documents on a topic             │
│                                                                                 │
│  Example queries:                                                               │
│  • "Who uses the CircuitBreaker type?" → citation chain                        │
│  • "How has error handling evolved?" → chronological + semantic                │
│  • "What papers/docs mention rate limiting?" → web fetch + markdown search     │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ func (a *AcademicAgent) Search(query string) *AgentSearchResult {       │   │
│  │     req := &AgentSearchRequest{                                         │   │
│  │         SearchRequest: SearchRequest{Query: query, Mode: SearchModeHybrid},│ │
│  │         Intent:          IntentUnderstand,                              │   │
│  │         ExpandQuery:     true,                                          │   │
│  │         FollowReferences: true,                                         │   │
│  │         MaxDepth:        3,                                             │   │
│  │         Summarize:       true,                                          │   │
│  │         GroupBy:         "package",                                     │   │
│  │     }                                                                   │   │
│  │     return a.searchAPI.Search(ctx, req)                                 │   │
│  │ }                                                                        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ARCHIVIST AGENT                                                                │
│  ───────────────                                                                │
│  Focus: Organization, preservation, metadata, provenance                       │
│                                                                                 │
│  Strategies:                                                                    │
│  • Provenance tracking: Where did this code come from? (git blame integration) │
│  • Version comparison: How has this file changed over time?                    │
│  • Metadata enrichment: Add tags, descriptions to search results               │
│  • Archive management: Index historical versions, old branches                 │
│                                                                                 │
│  Example queries:                                                               │
│  • "Show me all config files modified this week" → metadata + date filter      │
│  • "What changed in the auth module?" → git diff integration                   │
│  • "Categorize all error handling code" → faceted search + tagging             │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ func (a *ArchivistAgent) Search(query string) *AgentSearchResult {      │   │
│  │     req := &AgentSearchRequest{                                         │   │
│  │         SearchRequest: SearchRequest{                                   │   │
│  │             Query:         query,                                       │   │
│  │             Mode:          SearchModeLexical, // Precise matching       │   │
│  │             IncludeFacets: true,                                        │   │
│  │         },                                                              │   │
│  │         Intent:        IntentDiscover,                                  │   │
│  │         GroupBy:       "author",                                        │   │
│  │         IncludeContext: true,                                           │   │
│  │         ContextLines:  10,                                              │   │
│  │     }                                                                   │   │
│  │     return a.searchAPI.Search(ctx, req)                                 │   │
│  │ }                                                                        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  LIBRARIAN AGENT                                                                │
│  ───────────────                                                                │
│  Focus: Query understanding, result organization, recommendations              │
│                                                                                 │
│  Strategies:                                                                    │
│  • Query understanding: Parse natural language into structured query           │
│  • Result curation: Filter noise, highlight most relevant                      │
│  • Recommendation: "You might also want to look at..."                         │
│  • Facet navigation: Guide user through faceted browsing                       │
│                                                                                 │
│  Example queries:                                                               │
│  • "How do I add a new tool?" → query expansion + semantic + follow-up         │
│  • "Find examples of streaming" → example detection + ranking                  │
│  • "What's related to the session manager?" → graph traversal                  │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ func (l *LibrarianAgent) Search(query string) *AgentSearchResult {      │   │
│  │     req := &AgentSearchRequest{                                         │   │
│  │         SearchRequest: SearchRequest{                                   │   │
│  │             Query:         query,                                       │   │
│  │             Mode:          SearchModeHybrid,                            │   │
│  │             Fuzzy:         true,                                        │   │
│  │             IncludeFacets: true,                                        │   │
│  │         },                                                              │   │
│  │         Intent:          IntentFind,                                    │   │
│  │         ExpandQuery:     true,                                          │   │
│  │         Summarize:       true,                                          │   │
│  │         SuggestFollowUp: true,                                          │   │
│  │     }                                                                   │   │
│  │     return l.searchAPI.Search(ctx, req)                                 │   │
│  │ }                                                                        │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## Integration Points

### With Vector DB

```go
// core/search/vector_adapter.go

package search

import (
    "context"

    "sylk/core/vectordb" // Existing vector DB package
)

// VectorDBAdapter adapts the existing vector DB to VectorSearcher interface
type VectorDBAdapter struct {
    db *vectordb.VectorGraphDB
}

// NewVectorDBAdapter creates an adapter
func NewVectorDBAdapter(db *vectordb.VectorGraphDB) *VectorDBAdapter {
    return &VectorDBAdapter{db: db}
}

// Search implements VectorSearcher
func (a *VectorDBAdapter) Search(ctx context.Context, query string, limit int) ([]VectorResult, error) {
    // Use existing vector DB search
    results, err := a.db.Search(ctx, query, limit)
    if err != nil {
        return nil, err
    }

    // Convert to our result type
    var vResults []VectorResult
    for _, r := range results {
        vResults = append(vResults, VectorResult{
            DocumentID: r.ID,
            Score:      r.Similarity,
        })
    }

    return vResults, nil
}
```

### With Session Manager

```go
// Integration with session for context-aware search

// SessionSearchContext provides session context for search
type SessionSearchContext struct {
    SessionID      string
    RecentFiles    []string  // Files recently accessed
    RecentQueries  []string  // Previous search queries
    ActiveAgent    string    // Current agent type
    CurrentPackage string    // Package user is working in
}

// EnhanceRequest adds session context to a search request
func EnhanceRequest(req *AgentSearchRequest, ctx *SessionSearchContext) {
    req.SessionContext = ctx.RecentFiles

    // Boost results in current package
    if ctx.CurrentPackage != "" {
        req.Packages = append(req.Packages, ctx.CurrentPackage)
    }
}
```

---

## Performance Considerations

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         PERFORMANCE OPTIMIZATION                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  INDEXING                                                                       │
│  ────────                                                                       │
│  • Batch inserts (50-100 documents per batch)                                  │
│  • Parallel parsing (4 workers default, configurable)                          │
│  • Incremental updates (only re-index changed files)                           │
│  • Background indexing (non-blocking for user)                                 │
│                                                                                 │
│  SEARCH                                                                         │
│  ──────                                                                         │
│  • Parallel Bleve + Vector search                                              │
│  • Early termination for high-confidence results                               │
│  • Result caching (LRU, 5-minute TTL)                                          │
│  • Facet caching (reuse across similar queries)                                │
│                                                                                 │
│  STORAGE                                                                        │
│  ───────                                                                        │
│  • Bleve scorch segment compaction (automatic)                                 │
│  • Content not stored (only indexed) for large files                           │
│  • Checksum-based deduplication                                                │
│                                                                                 │
│  MEMORY                                                                         │
│  ──────                                                                         │
│  • Streaming file reads (not loading entire files)                             │
│  • Bounded channel buffers                                                     │
│  • Document pooling for batch operations                                       │
│                                                                                 │
│  EXPECTED PERFORMANCE                                                           │
│  ────────────────────                                                           │
│  • Initial index: ~1000 files/second                                           │
│  • Incremental update: <100ms per file                                         │
│  • Search latency: <50ms for most queries                                      │
│  • Hybrid search: <100ms (parallel execution)                                  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## Cartesian Merkle Tree Manifest

The index manifest uses a Cartesian Merkle Tree (CMT) for corruption-resistant, verifiable state tracking.

### Why CMT

| Property | Benefit |
|----------|---------|
| **Deterministic structure** | Same files → same tree shape → same root hash |
| **O(log n) operations** | Efficient lookup, insert, delete |
| **Merkle integrity** | Any tampering changes root hash |
| **Partial verification** | Can verify subtrees independently |
| **Membership proofs** | O(log n) hashes to prove file is indexed |

### CMT Structure

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                      CARTESIAN MERKLE TREE MANIFEST                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  CARTESIAN TREE PROPERTIES                                                      │
│  ─────────────────────────                                                      │
│  • Key ordering (BST): left.key < node.key < right.key                         │
│  • Priority ordering (Heap): node.priority > children.priority                 │
│  • Priority derived from content hash → deterministic structure                │
│                                                                                 │
│  MERKLE PROPERTIES                                                              │
│  ────────────────                                                               │
│  • Each node contains hash(self + children)                                    │
│  • Root hash = cryptographic commitment to entire tree                         │
│  • Any modification propagates to root                                         │
│                                                                                 │
│  NODE STRUCTURE                                                                 │
│  ──────────────                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  {                                                                       │   │
│  │    key:      string,                 // File path (BST ordering)        │   │
│  │    priority: uint64,                 // hash(content_hash)[:8]          │   │
│  │    value: {                                                             │   │
│  │      path:         "core/llm/client.go",                                │   │
│  │      content_hash: [32]byte,         // SHA-256 of file content         │   │
│  │      mtime:        int64,            // Modification timestamp          │   │
│  │      size:         int64,            // File size in bytes              │   │
│  │      indexed_at:   int64,            // When we indexed this            │   │
│  │    },                                                                   │   │
│  │    left_hash:  [32]byte,             // Hash of left subtree            │   │
│  │    right_hash: [32]byte,             // Hash of right subtree           │   │
│  │    node_hash:  [32]byte,             // hash(key+priority+value+children)│  │
│  │  }                                                                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  TREE EXAMPLE                                                                   │
│  ────────────                                                                   │
│                                                                                 │
│                         ┌─────────────────────────────┐                        │
│                         │  core/session/manager.go    │                        │
│                         │  priority: 0x9f3a (highest) │                        │
│                         │  node_hash: 0x7d2e...       │  ◄── ROOT HASH        │
│                         └──────────────┬──────────────┘                        │
│                                        │                                        │
│                    ┌───────────────────┴───────────────────┐                   │
│                    ▼                                       ▼                    │
│         ┌─────────────────────┐                 ┌─────────────────────┐        │
│         │  core/llm/client.go │                 │  tools/executor.go  │        │
│         │  priority: 0x8c1b   │                 │  priority: 0x7e4d   │        │
│         │  node_hash: 0x3f1a  │                 │  node_hash: 0x9b2c  │        │
│         └──────────┬──────────┘                 └──────────┬──────────┘        │
│                    │                                       │                    │
│              ┌─────┴─────┐                           ┌─────┴─────┐             │
│              ▼           ▼                           ▼           ▼              │
│         ┌────────┐  ┌────────┐                  ┌────────┐  ┌────────┐         │
│         │agents/ │  │core/   │                  │session/│  │ui/     │         │
│         │guide.go│  │dag/    │                  │bus.go  │  │render  │         │
│         │pri:0x7a│  │pri:0x6b│                  │pri:0x5c│  │pri:0x4d│         │
│         └────────┘  └────────┘                  └────────┘  └────────┘         │
│                                                                                 │
│  OPERATIONS                                                                     │
│  ──────────                                                                     │
│  • Lookup(path): O(log n) - BST search by path                                 │
│  • Insert(file): O(log n) - insert + rotate + rehash path to root             │
│  • Delete(path): O(log n) - remove + rebalance + rehash                        │
│  • Verify():     O(n) - full tree verification                                 │
│  • VerifyPath(): O(log n) - verify single path to root                         │
│  • RootHash():   O(1) - return root commitment                                 │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### CMT Implementation

```go
// core/search/cmt/tree.go

package cmt

import (
    "crypto/sha256"
    "encoding/binary"
    "fmt"
)

// Node represents a CMT node
type Node struct {
    Key       string     // File path (BST ordering)
    Priority  uint64     // Derived from content hash (heap ordering)
    Value     FileEntry  // File metadata

    Left      *Node
    Right     *Node

    // Merkle commitments
    LeftHash  [32]byte   // Hash of left subtree (zero if nil)
    RightHash [32]byte   // Hash of right subtree (zero if nil)
    NodeHash  [32]byte   // Hash of this node (includes children)
}

// FileEntry stores file metadata
type FileEntry struct {
    Path        string
    ContentHash [32]byte
    Mtime       int64
    Size        int64
    IndexedAt   int64
}

// CMT is a Cartesian Merkle Tree
type CMT struct {
    root     *Node
    count    int
    metadata CMTMetadata
}

// CMTMetadata stores tree-level metadata
type CMTMetadata struct {
    Version     int      // Schema version
    CreatedAt   int64
    LastGitHead string   // For go-git integration
    FileCount   int
}

// ComputePriority derives deterministic priority from content
// Same content = same priority = same tree structure
func ComputePriority(contentHash [32]byte) uint64 {
    return binary.BigEndian.Uint64(contentHash[:8])
}

// ComputeNodeHash computes the Merkle hash for a node
func (n *Node) ComputeNodeHash() [32]byte {
    h := sha256.New()

    // Include key
    h.Write([]byte(n.Key))

    // Include priority
    var priBuf [8]byte
    binary.BigEndian.PutUint64(priBuf[:], n.Priority)
    h.Write(priBuf[:])

    // Include value
    h.Write(n.Value.ContentHash[:])
    binary.Write(h, binary.BigEndian, n.Value.Mtime)
    binary.Write(h, binary.BigEndian, n.Value.Size)

    // Include children hashes
    h.Write(n.LeftHash[:])
    h.Write(n.RightHash[:])

    var result [32]byte
    copy(result[:], h.Sum(nil))
    return result
}

// Insert adds or updates a file in the CMT
func (t *CMT) Insert(entry FileEntry) error {
    priority := ComputePriority(entry.ContentHash)
    t.root = t.insert(t.root, entry.Path, priority, entry)
    t.count++
    return nil
}

func (t *CMT) insert(node *Node, key string, priority uint64, value FileEntry) *Node {
    if node == nil {
        newNode := &Node{
            Key:      key,
            Priority: priority,
            Value:    value,
        }
        newNode.NodeHash = newNode.ComputeNodeHash()
        return newNode
    }

    if key < node.Key {
        node.Left = t.insert(node.Left, key, priority, value)
        if node.Left != nil {
            node.LeftHash = node.Left.NodeHash
        }
        // Maintain heap property
        if node.Left.Priority > node.Priority {
            node = t.rotateRight(node)
        }
    } else if key > node.Key {
        node.Right = t.insert(node.Right, key, priority, value)
        if node.Right != nil {
            node.RightHash = node.Right.NodeHash
        }
        // Maintain heap property
        if node.Right.Priority > node.Priority {
            node = t.rotateLeft(node)
        }
    } else {
        // Update existing
        node.Value = value
        node.Priority = priority
    }

    // Recompute hash
    node.NodeHash = node.ComputeNodeHash()
    return node
}

func (t *CMT) rotateRight(node *Node) *Node {
    left := node.Left
    node.Left = left.Right
    if node.Left != nil {
        node.LeftHash = node.Left.NodeHash
    } else {
        node.LeftHash = [32]byte{}
    }
    left.Right = node
    left.RightHash = node.ComputeNodeHash()
    node.NodeHash = node.ComputeNodeHash()
    left.NodeHash = left.ComputeNodeHash()
    return left
}

func (t *CMT) rotateLeft(node *Node) *Node {
    right := node.Right
    node.Right = right.Left
    if node.Right != nil {
        node.RightHash = node.Right.NodeHash
    } else {
        node.RightHash = [32]byte{}
    }
    right.Left = node
    right.LeftHash = node.ComputeNodeHash()
    node.NodeHash = node.ComputeNodeHash()
    right.NodeHash = right.ComputeNodeHash()
    return right
}

// Lookup finds a file by path
func (t *CMT) Lookup(path string) (*FileEntry, bool) {
    node := t.lookup(t.root, path)
    if node == nil {
        return nil, false
    }
    return &node.Value, true
}

func (t *CMT) lookup(node *Node, key string) *Node {
    if node == nil {
        return nil
    }
    if key < node.Key {
        return t.lookup(node.Left, key)
    } else if key > node.Key {
        return t.lookup(node.Right, key)
    }
    return node
}

// Verify checks internal consistency of the entire CMT
func (t *CMT) Verify() error {
    return t.verify(t.root, "", "")
}

func (t *CMT) verify(node *Node, minKey, maxKey string) error {
    if node == nil {
        return nil
    }

    // Verify hash
    computed := node.ComputeNodeHash()
    if computed != node.NodeHash {
        return fmt.Errorf("hash mismatch at %s", node.Key)
    }

    // Verify BST property
    if minKey != "" && node.Key <= minKey {
        return fmt.Errorf("BST violation: %s <= %s", node.Key, minKey)
    }
    if maxKey != "" && node.Key >= maxKey {
        return fmt.Errorf("BST violation: %s >= %s", node.Key, maxKey)
    }

    // Verify heap property
    if node.Left != nil && node.Left.Priority > node.Priority {
        return fmt.Errorf("heap violation at %s", node.Key)
    }
    if node.Right != nil && node.Right.Priority > node.Priority {
        return fmt.Errorf("heap violation at %s", node.Key)
    }

    // Verify child hashes
    if node.Left != nil && node.LeftHash != node.Left.NodeHash {
        return fmt.Errorf("left hash mismatch at %s", node.Key)
    }
    if node.Right != nil && node.RightHash != node.Right.NodeHash {
        return fmt.Errorf("right hash mismatch at %s", node.Key)
    }

    // Recurse
    if err := t.verify(node.Left, minKey, node.Key); err != nil {
        return err
    }
    return t.verify(node.Right, node.Key, maxKey)
}

// RootHash returns the root commitment
func (t *CMT) RootHash() [32]byte {
    if t.root == nil {
        return [32]byte{}
    }
    return t.root.NodeHash
}

// GenerateProof creates a membership proof for a path
func (t *CMT) GenerateProof(path string) (*MembershipProof, error) {
    var proof MembershipProof
    found := t.generateProof(t.root, path, &proof)
    if !found {
        return nil, fmt.Errorf("path not found: %s", path)
    }
    return &proof, nil
}

// MembershipProof contains the hashes needed to verify membership
type MembershipProof struct {
    Path   string
    Entry  FileEntry
    Hashes [][32]byte  // Sibling hashes from leaf to root
    Sides  []bool      // true = left sibling, false = right sibling
}

func (t *CMT) generateProof(node *Node, path string, proof *MembershipProof) bool {
    if node == nil {
        return false
    }

    if path < node.Key {
        if t.generateProof(node.Left, path, proof) {
            proof.Hashes = append(proof.Hashes, node.RightHash)
            proof.Sides = append(proof.Sides, false)
            return true
        }
    } else if path > node.Key {
        if t.generateProof(node.Right, path, proof) {
            proof.Hashes = append(proof.Hashes, node.LeftHash)
            proof.Sides = append(proof.Sides, true)
            return true
        }
    } else {
        proof.Path = path
        proof.Entry = node.Value
        return true
    }
    return false
}

// VerifyProof checks a membership proof against a root hash
func VerifyProof(rootHash [32]byte, proof *MembershipProof) bool {
    // Reconstruct hash from proof
    currentHash := computeLeafHash(proof.Path, proof.Entry)

    for i, siblingHash := range proof.Hashes {
        h := sha256.New()
        if proof.Sides[i] {
            h.Write(siblingHash[:])
            h.Write(currentHash[:])
        } else {
            h.Write(currentHash[:])
            h.Write(siblingHash[:])
        }
        copy(currentHash[:], h.Sum(nil))
    }

    return currentHash == rootHash
}

func computeLeafHash(path string, entry FileEntry) [32]byte {
    h := sha256.New()
    h.Write([]byte(path))
    h.Write(entry.ContentHash[:])
    binary.Write(h, binary.BigEndian, entry.Mtime)
    binary.Write(h, binary.BigEndian, entry.Size)
    var result [32]byte
    copy(result[:], h.Sum(nil))
    return result
}
```

### CMT Persistence

```go
// core/search/cmt/persistence.go

package cmt

import (
    "encoding/gob"
    "os"
)

// Save persists the CMT to disk
func (t *CMT) Save(path string) error {
    f, err := os.Create(path + ".tmp")
    if err != nil {
        return err
    }
    defer f.Close()

    enc := gob.NewEncoder(f)
    if err := enc.Encode(t.metadata); err != nil {
        return err
    }
    if err := t.saveNode(enc, t.root); err != nil {
        return err
    }

    // Atomic rename
    return os.Rename(path+".tmp", path)
}

// Load reads a CMT from disk
func Load(path string) (*CMT, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    t := &CMT{}
    dec := gob.NewDecoder(f)
    if err := dec.Decode(&t.metadata); err != nil {
        return nil, err
    }
    t.root, err = t.loadNode(dec)
    if err != nil {
        return nil, err
    }

    // Verify integrity after load
    if err := t.Verify(); err != nil {
        return nil, fmt.Errorf("integrity check failed: %w", err)
    }

    return t, nil
}
```

---

## go-git Integration

Pure Go git implementation for fast change detection, metadata enrichment, and git-based skills.

### Why go-git

| Advantage | Description |
|-----------|-------------|
| **Pure Go** | No external git CLI dependency |
| **Embedded** | Ships with app, always available |
| **Programmatic** | Full access to git internals |
| **Skills-ready** | Build git-based agent tools |
| **Cross-platform** | Works identically everywhere |

### go-git Implementation

```go
// core/search/git/integration.go

package git

import (
    "context"
    "fmt"
    "sync/atomic"
    "time"

    "github.com/go-git/go-git/v5"
    "github.com/go-git/go-git/v5/plumbing"
    "github.com/go-git/go-git/v5/plumbing/object"
)

// Integration provides git operations via go-git
type Integration struct {
    repo     *git.Repository
    worktree *git.Worktree
    rootPath string
    healthy  atomic.Bool
}

// NewIntegration opens a git repository
func NewIntegration(rootPath string) (*Integration, error) {
    repo, err := git.PlainOpen(rootPath)
    if err != nil {
        return nil, fmt.Errorf("open git repo: %w", err)
    }

    wt, err := repo.Worktree()
    if err != nil {
        return nil, fmt.Errorf("get worktree: %w", err)
    }

    g := &Integration{
        repo:     repo,
        worktree: wt,
        rootPath: rootPath,
    }
    g.healthy.Store(true)

    return g, nil
}

// HEAD returns the current HEAD commit hash
func (g *Integration) HEAD() (plumbing.Hash, error) {
    ref, err := g.repo.Head()
    if err != nil {
        g.healthy.Store(false)
        return plumbing.ZeroHash, err
    }
    return ref.Hash(), nil
}

// CurrentBranch returns the current branch name
func (g *Integration) CurrentBranch() (string, error) {
    ref, err := g.repo.Head()
    if err != nil {
        return "", err
    }
    return ref.Name().Short(), nil
}

// Status returns the working tree status
func (g *Integration) Status() (git.Status, error) {
    return g.worktree.Status()
}

// ChangedFiles returns files changed between two commits
func (g *Integration) ChangedFiles(from, to plumbing.Hash) ([]string, error) {
    fromCommit, err := g.repo.CommitObject(from)
    if err != nil {
        return nil, err
    }

    toCommit, err := g.repo.CommitObject(to)
    if err != nil {
        return nil, err
    }

    fromTree, err := fromCommit.Tree()
    if err != nil {
        return nil, err
    }

    toTree, err := toCommit.Tree()
    if err != nil {
        return nil, err
    }

    changes, err := fromTree.Diff(toTree)
    if err != nil {
        return nil, err
    }

    var files []string
    for _, change := range changes {
        name := change.To.Name
        if name == "" {
            name = change.From.Name // Deleted file
        }
        files = append(files, name)
    }

    return files, nil
}

// FileInfo returns git metadata for a file
type FileInfo struct {
    LastCommit  plumbing.Hash
    Author      string
    AuthorEmail string
    CommitDate  time.Time
    Message     string
}

// GetFileInfo returns git info for a specific file
func (g *Integration) GetFileInfo(path string) (*FileInfo, error) {
    iter, err := g.repo.Log(&git.LogOptions{
        FileName: &path,
    })
    if err != nil {
        return nil, err
    }

    commit, err := iter.Next()
    if err != nil {
        return nil, err
    }

    return &FileInfo{
        LastCommit:  commit.Hash,
        Author:      commit.Author.Name,
        AuthorEmail: commit.Author.Email,
        CommitDate:  commit.Author.When,
        Message:     commit.Message,
    }, nil
}

// VerifyIntegrity checks git repository health
func (g *Integration) VerifyIntegrity() error {
    // 1. Verify HEAD is resolvable
    head, err := g.repo.Head()
    if err != nil {
        g.healthy.Store(false)
        return fmt.Errorf("cannot resolve HEAD: %w", err)
    }

    // 2. Verify HEAD commit exists
    commit, err := g.repo.CommitObject(head.Hash())
    if err != nil {
        g.healthy.Store(false)
        return fmt.Errorf("HEAD commit corrupted: %w", err)
    }

    // 3. Verify commit tree is accessible
    _, err = commit.Tree()
    if err != nil {
        g.healthy.Store(false)
        return fmt.Errorf("commit tree corrupted: %w", err)
    }

    // 4. Sample verification of random objects
    if err := g.verifySampleObjects(10); err != nil {
        g.healthy.Store(false)
        return fmt.Errorf("object store corrupted: %w", err)
    }

    g.healthy.Store(true)
    return nil
}

func (g *Integration) verifySampleObjects(count int) error {
    iter, err := g.repo.CommitObjects()
    if err != nil {
        return err
    }

    checked := 0
    return iter.ForEach(func(c *object.Commit) error {
        if checked >= count {
            return nil
        }
        // Verify commit is readable
        _, err := c.Tree()
        if err != nil {
            return err
        }
        checked++
        return nil
    })
}

// IsHealthy returns current health status
func (g *Integration) IsHealthy() bool {
    return g.healthy.Load()
}
```

### Git-Based Skills

```go
// core/search/git/skills.go

package git

import (
    "context"
    "time"
)

// Skills provides git operations as agent tools
type Skills struct {
    git *Integration
}

// NewSkills creates a git skills provider
func NewSkills(git *Integration) *Skills {
    return &Skills{git: git}
}

// DiffResult represents a diff between refs
type DiffResult struct {
    FromRef string
    ToRef   string
    Files   []DiffFile
}

type DiffFile struct {
    Path      string
    Status    string // Added, Modified, Deleted, Renamed
    Additions int
    Deletions int
}

// Diff shows changes between two refs
func (s *Skills) Diff(ctx context.Context, ref1, ref2 string) (*DiffResult, error) {
    // Implementation using go-git
    // ...
}

// LogResult represents commit history
type LogResult struct {
    Commits []CommitInfo
}

type CommitInfo struct {
    Hash      string
    Author    string
    Date      time.Time
    Message   string
    Files     []string
}

// Log shows commit history for a path
func (s *Skills) Log(ctx context.Context, path string, n int) (*LogResult, error) {
    // Implementation using go-git
    // ...
}

// BlameResult represents file blame info
type BlameResult struct {
    Lines []BlameLine
}

type BlameLine struct {
    LineNum int
    Commit  string
    Author  string
    Date    time.Time
    Content string
}

// Blame shows line-by-line authorship
func (s *Skills) Blame(ctx context.Context, path string) (*BlameResult, error) {
    // Implementation using go-git
    // ...
}

// StatusResult represents working tree status
type StatusResult struct {
    Branch   string
    Clean    bool
    Modified []string
    Added    []string
    Deleted  []string
    Untracked []string
}

// Status shows working tree status
func (s *Skills) Status(ctx context.Context) (*StatusResult, error) {
    // Implementation using go-git
    // ...
}
```

---

## Cross-Validation System

The cross-validation system compares three sources of truth to detect corruption and maintain consistency.

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    CROSS-VALIDATION ARCHITECTURE                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  THREE SOURCES OF TRUTH                                                         │
│  ─────────────────────                                                          │
│                                                                                 │
│  ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐          │
│  │   FILESYSTEM    │     │   CMT MANIFEST  │     │    GO-GIT       │          │
│  │                 │     │                 │     │                 │          │
│  │  • Actual files │     │  • Indexed state│     │  • Committed    │          │
│  │  • Ground truth │     │  • Authoritative│     │    state        │          │
│  │  • Cannot be    │     │    for search   │     │  • Optimization │          │
│  │    corrupted    │     │  • Self-healing │     │    layer        │          │
│  └────────┬────────┘     └────────┬────────┘     └────────┬────────┘          │
│           │                       │                       │                    │
│           └───────────────────────┼───────────────────────┘                    │
│                                   ▼                                            │
│                    ┌──────────────────────────────┐                            │
│                    │     CROSS-VALIDATOR          │                            │
│                    └──────────────────────────────┘                            │
│                                                                                 │
│  VALIDATION SCENARIOS                                                           │
│  ────────────────────                                                           │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐   │
│  │ Scenario          │ FS     │ CMT    │ Git    │ Action                  │   │
│  ├────────────────────────────────────────────────────────────────────────┤   │
│  │ Normal (all match)│ abc123 │ abc123 │ abc123 │ No action               │   │
│  │ File modified     │ def456 │ abc123 │ abc123 │ Re-index, update CMT    │   │
│  │ Uncommitted edit  │ def456 │ def456 │ abc123 │ Normal (dirty working)  │   │
│  │ CMT corrupted     │ abc123 │ xxxxxx │ abc123 │ Rebuild CMT from FS+Git │   │
│  │ Git corrupted     │ abc123 │ abc123 │ <error>│ Git-disabled mode       │   │
│  │ Both corrupted    │ abc123 │ <error>│ <error>│ Full rebuild from FS    │   │
│  └────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Cross-Validator Implementation

```go
// core/search/validator/cross_validator.go

package validator

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "sylk/core/search/cmt"
    "sylk/core/search/git"
)

// CrossValidator compares multiple sources for consistency
type CrossValidator struct {
    rootPath string
    manifest *cmt.CMT
    git      *git.Integration  // May be nil if git unavailable
}

// NewCrossValidator creates a cross-validator
func NewCrossValidator(rootPath string, manifest *cmt.CMT, git *git.Integration) *CrossValidator {
    return &CrossValidator{
        rootPath: rootPath,
        manifest: manifest,
        git:      git,
    }
}

// ValidationResult contains validation findings
type ValidationResult struct {
    CMTHealthy    bool
    GitHealthy    bool
    Inconsistencies []Inconsistency
    Actions       []RepairAction
}

// Inconsistency represents a detected issue
type Inconsistency struct {
    Path     string
    Type     InconsistencyType
    Expected string
    Actual   string
}

type InconsistencyType int

const (
    InconsistencyFileMissing InconsistencyType = iota
    InconsistencyFileModified
    InconsistencyHashMismatch
    InconsistencyOrphanedEntry
)

// RepairAction describes how to fix an inconsistency
type RepairAction struct {
    Type        RepairType
    Path        string
    Description string
}

type RepairType int

const (
    RepairReindex RepairType = iota
    RepairRemoveEntry
    RepairRebuildCMT
    RepairDisableGit
)

// Validate performs full cross-validation
func (v *CrossValidator) Validate(ctx context.Context) (*ValidationResult, error) {
    result := &ValidationResult{
        CMTHealthy: true,
        GitHealthy: v.git != nil,
    }

    // 1. Verify CMT internal consistency
    if err := v.manifest.Verify(); err != nil {
        result.CMTHealthy = false
        result.Actions = append(result.Actions, RepairAction{
            Type:        RepairRebuildCMT,
            Description: "CMT integrity check failed, rebuild required",
        })
        return result, nil
    }

    // 2. Verify git health (if available)
    if v.git != nil {
        if err := v.git.VerifyIntegrity(); err != nil {
            result.GitHealthy = false
            result.Actions = append(result.Actions, RepairAction{
                Type:        RepairDisableGit,
                Description: fmt.Sprintf("Git corrupted: %v", err),
            })
        }
    }

    // 3. Compare CMT with filesystem
    v.manifest.ForEach(func(path string, entry cmt.FileEntry) bool {
        select {
        case <-ctx.Done():
            return false
        default:
        }

        fullPath := filepath.Join(v.rootPath, path)
        stat, err := os.Stat(fullPath)

        if os.IsNotExist(err) {
            // File deleted but still in CMT
            result.Inconsistencies = append(result.Inconsistencies, Inconsistency{
                Path: path,
                Type: InconsistencyOrphanedEntry,
            })
            result.Actions = append(result.Actions, RepairAction{
                Type: RepairRemoveEntry,
                Path: path,
            })
            return true
        }

        // Check if file changed
        if stat.ModTime().Unix() != entry.Mtime || stat.Size() != entry.Size {
            result.Inconsistencies = append(result.Inconsistencies, Inconsistency{
                Path: path,
                Type: InconsistencyFileModified,
            })
            result.Actions = append(result.Actions, RepairAction{
                Type: RepairReindex,
                Path: path,
            })
        }

        return true
    })

    // 4. Find files not in CMT
    err := filepath.Walk(v.rootPath, func(path string, info os.FileInfo, err error) error {
        if err != nil || info.IsDir() {
            return nil
        }

        relPath, _ := filepath.Rel(v.rootPath, path)
        if _, found := v.manifest.Lookup(relPath); !found {
            result.Inconsistencies = append(result.Inconsistencies, Inconsistency{
                Path: relPath,
                Type: InconsistencyFileMissing,
            })
            result.Actions = append(result.Actions, RepairAction{
                Type: RepairReindex,
                Path: relPath,
            })
        }
        return nil
    })

    if err != nil {
        return nil, err
    }

    return result, nil
}

// Repair executes repair actions
func (v *CrossValidator) Repair(ctx context.Context, actions []RepairAction) error {
    for _, action := range actions {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        switch action.Type {
        case RepairReindex:
            // Re-index single file
            if err := v.reindexFile(action.Path); err != nil {
                return err
            }
        case RepairRemoveEntry:
            // Remove orphaned entry
            v.manifest.Delete(action.Path)
        case RepairRebuildCMT:
            // Full rebuild
            if err := v.rebuildCMT(ctx); err != nil {
                return err
            }
        case RepairDisableGit:
            // Mark git as unhealthy
            v.git = nil
        }
    }
    return nil
}
```

---

## Hybrid Staleness Detection

Combines CMT integrity checks with go-git acceleration for fast, robust change detection.

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    HYBRID STALENESS DETECTION FLOW                               │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ON STARTUP                                                                     │
│  ──────────                                                                     │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  PHASE 1: INTEGRITY CHECK (<20ms)                                       │   │
│  │                                                                          │   │
│  │  Parallel:                                                              │   │
│  │  ┌─────────────────┐     ┌─────────────────┐                           │   │
│  │  │ Verify CMT Root │     │ Verify Git HEAD │                           │   │
│  │  │ • Load manifest │     │ • Open repo     │                           │   │
│  │  │ • Check root    │     │ • Resolve HEAD  │                           │   │
│  │  │   hash          │     │ • Verify commit │                           │   │
│  │  └────────┬────────┘     └────────┬────────┘                           │   │
│  │           │                       │                                     │   │
│  │           ▼                       ▼                                     │   │
│  │      CMT healthy?            Git healthy?                               │   │
│  │      ┌────┴────┐             ┌────┴────┐                               │   │
│  │      Yes      No             Yes      No                                │   │
│  │      │        │              │        │                                 │   │
│  │      │        ▼              │        ▼                                 │   │
│  │      │   Rebuild from        │   Git-disabled                           │   │
│  │      │   filesystem          │   mode                                   │   │
│  │      └────────┬──────────────┴────────┘                                │   │
│  │               ▼                                                         │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  PHASE 2: FAST CHANGE DETECTION                                         │   │
│  │                                                                          │   │
│  │  If git healthy:                                                        │   │
│  │  ┌───────────────────────────────────────────────────────────────────┐ │   │
│  │  │  stored_head := CMT.Metadata.LastGitHead                          │ │   │
│  │  │  current_head := Git.HEAD()                                       │ │   │
│  │  │                                                                    │ │   │
│  │  │  if stored_head == current_head {                                 │ │   │
│  │  │      // Git HEAD unchanged, only check working tree               │ │   │
│  │  │      dirty_files := Git.Status().Modified                         │ │   │
│  │  │      return dirty_files  // O(dirty files)                        │ │   │
│  │  │  } else {                                                         │ │   │
│  │  │      // Git HEAD changed, get full diff                           │ │   │
│  │  │      changed := Git.ChangedFiles(stored_head, current_head)       │ │   │
│  │  │      dirty := Git.Status().Modified                               │ │   │
│  │  │      return union(changed, dirty)                                 │ │   │
│  │  │  }                                                                │ │   │
│  │  └───────────────────────────────────────────────────────────────────┘ │   │
│  │                                                                          │   │
│  │  If git unhealthy (fallback):                                           │   │
│  │  ┌───────────────────────────────────────────────────────────────────┐ │   │
│  │  │  // Must scan filesystem and compare with CMT                     │ │   │
│  │  │  changed := []string{}                                            │ │   │
│  │  │  CMT.ForEach(func(path, entry) {                                  │ │   │
│  │  │      stat := os.Stat(path)                                        │ │   │
│  │  │      if stat.Mtime != entry.Mtime || stat.Size != entry.Size {    │ │   │
│  │  │          changed = append(changed, path)                          │ │   │
│  │  │      }                                                            │ │   │
│  │  │  })                                                               │ │   │
│  │  │  return changed  // O(all files)                                  │ │   │
│  │  └───────────────────────────────────────────────────────────────────┘ │   │
│  │                                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  PHASE 3: CONTENT VERIFICATION (For changed files only)                 │   │
│  │                                                                          │   │
│  │  for path in changed_files {                                            │   │
│  │      current_hash := sha256(read(path))                                 │   │
│  │      cmt_hash := CMT.Lookup(path).ContentHash                           │   │
│  │                                                                          │   │
│  │      if current_hash != cmt_hash {                                      │   │
│  │          queue_for_reindex(path)  // Actually changed                   │   │
│  │      } else {                                                           │   │
│  │          CMT.UpdateMtime(path)    // Mtime changed, content same        │   │
│  │      }                                                                  │   │
│  │  }                                                                      │   │
│  │                                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  PHASE 4: CROSS-VALIDATION (Background, periodic)                       │   │
│  │                                                                          │   │
│  │  Every 5 minutes:                                                       │   │
│  │  1. Verify CMT root hash (internal consistency)                         │   │
│  │  2. If git healthy: compare git tree hash with expectation              │   │
│  │  3. Sample 5% of files: verify CMT hash matches filesystem              │   │
│  │  4. If mismatch found: trigger targeted repair                          │   │
│  │                                                                          │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  PERFORMANCE                                                                    │
│  ───────────                                                                    │
│                                                                                 │
│  Scenario                              Git Healthy       Git Unhealthy          │
│  ─────────────────────────────────────────────────────────────────────────     │
│  No changes (common)                   ~5ms (HEAD cmp)   ~100ms (stat all)     │
│  Few files changed                     ~10ms (diff)      ~100ms (stat all)     │
│  Many files changed                    ~50ms (diff)      ~100ms (stat all)     │
│  Git corrupted mid-session             Auto fallback     N/A                   │
│  CMT corrupted                         Rebuild ~2s       Rebuild ~2s           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Staleness Detector Implementation

```go
// core/search/staleness/detector.go

package staleness

import (
    "context"
    "os"
    "path/filepath"

    "sylk/core/search/cmt"
    "sylk/core/search/git"
)

// Detector handles staleness detection with CMT + go-git hybrid
type Detector struct {
    rootPath string
    manifest *cmt.CMT
    git      *git.Integration
}

// NewDetector creates a staleness detector
func NewDetector(rootPath string, manifest *cmt.CMT, git *git.Integration) *Detector {
    return &Detector{
        rootPath: rootPath,
        manifest: manifest,
        git:      git,
    }
}

// DetectionResult contains staleness detection findings
type DetectionResult struct {
    ChangedFiles []string
    NewFiles     []string
    DeletedFiles []string
    GitUsed      bool
    Duration     time.Duration
}

// Detect finds stale files efficiently
func (d *Detector) Detect(ctx context.Context) (*DetectionResult, error) {
    start := time.Now()
    result := &DetectionResult{}

    // Try git-accelerated detection first
    if d.git != nil && d.git.IsHealthy() {
        detected, err := d.detectWithGit(ctx)
        if err == nil {
            result = detected
            result.GitUsed = true
            result.Duration = time.Since(start)
            return result, nil
        }
        // Git failed, fall back to CMT-only
    }

    // CMT-only detection (slower but always works)
    detected, err := d.detectWithCMT(ctx)
    if err != nil {
        return nil, err
    }
    result = detected
    result.GitUsed = false
    result.Duration = time.Since(start)
    return result, nil
}

func (d *Detector) detectWithGit(ctx context.Context) (*DetectionResult, error) {
    result := &DetectionResult{}

    // Get stored HEAD from CMT metadata
    storedHead := d.manifest.Metadata().LastGitHead

    // Get current HEAD
    currentHead, err := d.git.HEAD()
    if err != nil {
        return nil, err
    }

    if storedHead == currentHead.String() {
        // HEAD unchanged, only check working tree
        status, err := d.git.Status()
        if err != nil {
            return nil, err
        }

        for path, s := range status {
            if s.Worktree != git.Unmodified {
                result.ChangedFiles = append(result.ChangedFiles, path)
            }
        }
    } else {
        // HEAD changed, get full diff
        storedHash := plumbing.NewHash(storedHead)
        changed, err := d.git.ChangedFiles(storedHash, currentHead)
        if err != nil {
            return nil, err
        }
        result.ChangedFiles = changed

        // Also check working tree
        status, err := d.git.Status()
        if err != nil {
            return nil, err
        }
        for path, s := range status {
            if s.Worktree != git.Unmodified {
                result.ChangedFiles = append(result.ChangedFiles, path)
            }
        }
    }

    return result, nil
}

func (d *Detector) detectWithCMT(ctx context.Context) (*DetectionResult, error) {
    result := &DetectionResult{}
    seen := make(map[string]bool)

    // Check all CMT entries against filesystem
    d.manifest.ForEach(func(path string, entry cmt.FileEntry) bool {
        select {
        case <-ctx.Done():
            return false
        default:
        }

        seen[path] = true
        fullPath := filepath.Join(d.rootPath, path)

        stat, err := os.Stat(fullPath)
        if os.IsNotExist(err) {
            result.DeletedFiles = append(result.DeletedFiles, path)
            return true
        }

        if stat.ModTime().Unix() != entry.Mtime || stat.Size() != entry.Size {
            result.ChangedFiles = append(result.ChangedFiles, path)
        }

        return true
    })

    // Find new files not in CMT
    filepath.Walk(d.rootPath, func(path string, info os.FileInfo, err error) error {
        if err != nil || info.IsDir() {
            return nil
        }
        relPath, _ := filepath.Rel(d.rootPath, path)
        if !seen[relPath] && d.shouldIndex(relPath) {
            result.NewFiles = append(result.NewFiles, relPath)
        }
        return nil
    })

    return result, nil
}
```

---

## Updated Storage Layout

```
~/.sylk/projects/<project-hash>/
├── vector.db              # SQLite - vector embeddings
├── documents.bleve/       # Bleve full-text index
│   ├── index_meta.json
│   └── store/
├── manifest.cmt           # Cartesian Merkle Tree manifest
└── manifest.cmt.wal       # Write-ahead log for crash recovery
```

---

## Implementation Phases

| Phase | Components | Dependencies |
|-------|------------|--------------|
| 1 | Document model, Bleve index setup, custom analyzers | None |
| 2 | Scanner, Parser (Go, TS, Python) | Phase 1 |
| 3 | **CMT implementation, persistence, verification** | None |
| 4 | **go-git integration, health checking** | None |
| 5 | **Cross-validator, repair actions** | Phase 3, 4 |
| 6 | **Hybrid staleness detector** | Phase 3, 4, 5 |
| 7 | Indexer, batch operations | Phase 2, 3 |
| 8 | File watcher, incremental updates | Phase 6, 7 |
| 9 | Search coordinator, Bleve queries | Phase 7 |
| 10 | Vector DB integration, hybrid search, RRF | Phase 9 |
| 11 | Terminal CLI interface | Phase 9 |
| 12 | Agent search API, query expansion | Phase 10 |
| 13 | Agent-specific strategies | Phase 12 |
| 14 | **Git-based skills for agents** | Phase 4 |
| 15 | LLM document indexing (prompts/responses) | Phase 7 |

---

## Full Integration with Sylk Codebase

This section details how the Document Search System integrates with the existing Sylk architecture.

### Package Structure

```
core/search/
├── search.go              # Main SearchSystem facade
├── config.go              # SearchConfig (mirrors core/config patterns)
├── types.go               # Document, SearchRequest, SearchResult
├── bleve/
│   ├── index.go           # IndexManager
│   ├── analyzer.go        # Code analyzers (CamelCase, SnakeCase)
│   └── mapping.go         # Index field mappings
├── cmt/
│   ├── tree.go            # Cartesian Merkle Tree
│   ├── persistence.go     # Save/Load with atomic writes
│   ├── proof.go           # Membership proofs
│   └── wal.go             # Write-ahead log (use existing core/concurrency/wal.go patterns)
├── git/
│   ├── integration.go     # go-git wrapper
│   ├── skills.go          # Git-based agent skills
│   └── health.go          # Integrity verification
├── pipeline/
│   ├── scanner.go         # File tree walker
│   ├── parser.go          # Language-specific extraction
│   ├── indexer.go         # Batch indexing coordinator
│   └── parsers/           # Per-language parsers (Go, TS, Python, etc.)
├── detection/
│   ├── watcher.go         # fsnotify wrapper
│   ├── staleness.go       # Hybrid CMT+git detector
│   └── validator.go       # Cross-validation
├── coordinator/
│   ├── coordinator.go     # Hybrid search orchestrator
│   ├── fusion.go          # RRF rank fusion
│   └── cache.go           # Result caching (LRU)
├── agent/
│   ├── api.go             # AgentSearchAPI
│   ├── expander.go        # Query expansion
│   ├── synthesizer.go     # Result synthesis
│   └── strategies.go      # Agent-specific strategies
└── adapter/
    └── vector.go          # VectorGraphDB adapter
```

---

### Integration with Resource Management

#### GoroutineBudget Integration

The search system must respect goroutine budgets. Add search as a new agent type:

```go
// In core/concurrency/goroutine_budget.go - add to agentWeights
var agentWeights = map[string]float64{
    "engineer":    1.0,
    "architect":   0.5,
    "librarian":   0.3,
    "archivalist": 0.3,
    "inspector":   0.5,
    "tester":      0.8,
    "guide":       0.2,
    "search":      0.4,  // NEW: Search indexing/querying
}
```

**SearchSystem must acquire budget before parallel operations:**

```go
// core/search/search.go

type SearchSystem struct {
    // Resource integration
    goroutineBudget    *concurrency.GoroutineBudget
    fileHandleBudget   *resources.FileHandleBudget
    pressureController *resources.PressureController

    // Components
    bleveIndex     *bleve.IndexManager
    manifest       *cmt.CMT
    git            *git.Integration
    coordinator    *coordinator.SearchCoordinator
    watcher        *detection.Watcher
    staleness      *detection.Detector

    // State
    agentID        string
    sessionID      string
}

func (s *SearchSystem) IndexBatch(ctx context.Context, files []*ScannedFile) error {
    // 1. Acquire goroutine budget for parallel parsing
    workers := s.calculateWorkers()
    if err := s.goroutineBudget.Acquire(s.agentID, workers); err != nil {
        return fmt.Errorf("acquire budget: %w", err)
    }
    defer s.goroutineBudget.Release(s.agentID, workers)

    // 2. Check pressure level - reduce parallelism under pressure
    pressure := s.pressureController.CurrentLevel()
    if pressure >= resources.PressureHigh {
        workers = max(1, workers/2)
    }

    // 3. Execute with bounded parallelism
    return s.indexer.IndexWithWorkers(ctx, files, workers)
}

func (s *SearchSystem) calculateWorkers() int {
    // Base: 4 workers
    // Adjusted by pressure: Normal=4, Elevated=3, High=2, Critical=1
    base := 4
    pressure := s.pressureController.CurrentLevel()
    multiplier := s.goroutineBudget.PressureMultiplier(pressure)
    return max(1, int(float64(base)*multiplier))
}
```

#### FileHandleBudget Integration

Scanner and parsers need file handles. Integrate with existing `core/resources/file_handle_budget.go`:

```go
// core/search/pipeline/scanner.go

type Scanner struct {
    config           ScanConfig
    fileHandleBudget *resources.FileHandleBudget
    agentID          string
}

func (s *Scanner) Scan(ctx context.Context) (<-chan *ScannedFile, <-chan error) {
    files := make(chan *ScannedFile, 100)
    errs := make(chan error, 1)

    go func() {
        defer close(files)
        defer close(errs)

        err := filepath.WalkDir(s.config.RootPath, func(path string, d fs.DirEntry, err error) error {
            if err != nil {
                return err
            }

            select {
            case <-ctx.Done():
                return ctx.Err()
            default:
            }

            if d.IsDir() {
                return nil
            }

            // Acquire file handle before reading
            if err := s.fileHandleBudget.Acquire(ctx, s.agentID, 1); err != nil {
                return nil // Skip file, don't fail entire scan
            }

            content, err := s.readFile(path)

            // Release immediately after read
            s.fileHandleBudget.Release(s.agentID, 1)

            if err != nil {
                return nil // Skip unreadable files
            }

            // ... process file
            return nil
        })

        if err != nil {
            errs <- err
        }
    }()

    return files, errs
}
```

#### PressureController Integration

Search operations must respond to memory pressure:

```go
// core/search/search.go

func (s *SearchSystem) Start(ctx context.Context) error {
    // Register pressure callback
    s.pressureController.RegisterCallback("search", s.handlePressure)

    // Start components
    if err := s.watcher.Start(ctx); err != nil {
        return err
    }

    return nil
}

func (s *SearchSystem) handlePressure(level resources.PressureLevel) {
    switch level {
    case resources.PressureNormal:
        // Full operation
        s.watcher.SetDebounce(100 * time.Millisecond)

    case resources.PressureElevated:
        // Reduce indexing frequency
        s.watcher.SetDebounce(500 * time.Millisecond)

    case resources.PressureHigh:
        // Pause background indexing
        s.watcher.Pause()
        // Evict search result cache
        s.coordinator.EvictCache(0.25)

    case resources.PressureCritical:
        // Stop all non-essential operations
        s.watcher.Pause()
        s.coordinator.EvictCache(0.50)
        // Only serve cached results
        s.coordinator.SetCacheOnlyMode(true)
    }
}
```

---

### Integration with Session Management

#### Per-Session Search State

Each session gets isolated search context:

```go
// core/session/session.go - ADD to existing Session struct

type Session struct {
    // ... existing fields

    // Search integration
    searchContext *search.SessionContext
}

// core/search/session_context.go

type SessionContext struct {
    SessionID      string
    RecentFiles    *ring.Ring  // Last 50 accessed files
    RecentQueries  *ring.Ring  // Last 20 queries
    CurrentPackage string
    QueryCache     *lru.Cache  // Session-local result cache
}

func NewSessionContext(sessionID string) *SessionContext {
    cache, _ := lru.New(100)
    return &SessionContext{
        SessionID:     sessionID,
        RecentFiles:   ring.New(50),
        RecentQueries: ring.New(20),
        QueryCache:    cache,
    }
}
```

#### Session Lifecycle Hooks

```go
// core/session/manager.go - ADD to existing Manager

func (m *Manager) Create(ctx context.Context, meta SessionMetadata) (*Session, error) {
    // ... existing creation logic

    // Initialize search context for session
    session.searchContext = search.NewSessionContext(session.ID)

    return session, nil
}

func (m *Manager) Activate(ctx context.Context, id string) error {
    // ... existing activation logic

    // Notify search system of session switch
    m.searchSystem.SetActiveSession(session.searchContext)

    return nil
}

func (m *Manager) Complete(ctx context.Context, id string) error {
    // ... existing completion logic

    // Cleanup session search state
    if session.searchContext != nil {
        session.searchContext.QueryCache.Purge()
    }

    return nil
}
```

---

### Integration with Agent System

#### Guide Router Integration

Add search message types to existing messaging:

```go
// core/messaging/message.go - ADD to MessageType enum

const (
    // ... existing types

    // Search messages
    SEARCH_REQUEST     MessageType = "search.request"
    SEARCH_RESPONSE    MessageType = "search.response"
    INDEX_REQUEST      MessageType = "index.request"
    INDEX_PROGRESS     MessageType = "index.progress"
    INDEX_COMPLETE     MessageType = "index.complete"
)
```

#### Search Handler in Guide

```go
// agents/guide/router.go - ADD to Route method

func (r *Router) Route(ctx context.Context, msg *messaging.Message[any]) error {
    switch msg.Type {
    // ... existing cases

    case messaging.SEARCH_REQUEST:
        return r.routeToSearch(ctx, msg)

    case messaging.INDEX_REQUEST:
        return r.routeToSearch(ctx, msg)
    }

    // ... existing routing logic
}

func (r *Router) routeToSearch(ctx context.Context, msg *messaging.Message[any]) error {
    // Extract search request
    req, ok := msg.Payload.(*search.AgentSearchRequest)
    if !ok {
        return fmt.Errorf("invalid search request payload")
    }

    // Set session context
    if session := r.sessionManager.Active(); session != nil {
        req.SessionContext = session.searchContext.RecentFiles
    }

    // Execute search
    result, err := r.searchSystem.AgentAPI().Search(ctx, req)
    if err != nil {
        return r.sendError(ctx, msg, err)
    }

    // Send response
    response := messaging.NewMessage[*search.AgentSearchResult](
        messaging.SEARCH_RESPONSE,
        msg.SessionID,
        result,
    )
    response.CorrelationID = msg.ID

    return r.eventBus.Publish(ctx, "agent.responses", response)
}
```

#### Librarian Agent Search Integration

```go
// agents/librarian/librarian.go

type Librarian struct {
    // ... existing fields

    searchAPI *search.AgentSearchAPI
}

func (l *Librarian) HandleContextRequest(ctx context.Context, msg *messaging.Message[ContextRequest]) error {
    req := msg.Payload

    // Build agent search request with Librarian strategy
    searchReq := &search.AgentSearchRequest{
        SearchRequest: search.SearchRequest{
            Query:         req.Query,
            Mode:          search.SearchModeHybrid,
            Fuzzy:         true,
            IncludeFacets: true,
            Limit:         20,
        },
        Intent:          search.IntentFind,
        AgentType:       "librarian",
        ExpandQuery:     true,
        Summarize:       true,
        SuggestFollowUp: true,
    }

    // Add session context
    if l.sessionContext != nil {
        searchReq.SessionContext = l.sessionContext.RecentFilePaths()
        if pkg := l.sessionContext.CurrentPackage; pkg != "" {
            searchReq.Packages = []string{pkg}
        }
    }

    result, err := l.searchAPI.Search(ctx, searchReq)
    if err != nil {
        return l.sendError(ctx, msg, err)
    }

    // Build context response from search results
    context := l.buildContextFromSearch(result)

    return l.sendResponse(ctx, msg, context)
}
```

---

### Integration with Vector DB

#### Vector DB Adapter

Connect to existing `core/vectorgraphdb/` for hybrid search:

```go
// core/search/adapter/vector.go

package adapter

import (
    "context"

    "sylk/core/vectorgraphdb"
    "sylk/core/search"
)

type VectorDBAdapter struct {
    db       *vectorgraphdb.VectorGraphDB
    embedder Embedder
}

type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}

func NewVectorDBAdapter(db *vectorgraphdb.VectorGraphDB, embedder Embedder) *VectorDBAdapter {
    return &VectorDBAdapter{
        db:       db,
        embedder: embedder,
    }
}

func (a *VectorDBAdapter) Search(ctx context.Context, query string, limit int) ([]search.VectorResult, error) {
    // 1. Embed query
    embedding, err := a.embedder.Embed(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("embed query: %w", err)
    }

    // 2. Search existing vector DB
    opts := vectorgraphdb.SearchOptions{
        Domains:       []string{"code", "docs"},
        MinSimilarity: 0.5,
        Limit:         limit,
    }

    results, err := a.db.Search(ctx, embedding, opts)
    if err != nil {
        return nil, err
    }

    // 3. Convert to search.VectorResult
    var vResults []search.VectorResult
    for _, r := range results {
        vResults = append(vResults, search.VectorResult{
            DocumentID: r.Node.ID,
            Score:      r.Similarity,
        })
    }

    return vResults, nil
}

// IndexDocument adds document to vector DB alongside Bleve
func (a *VectorDBAdapter) IndexDocument(ctx context.Context, doc *search.Document) error {
    // Embed content
    embedding, err := a.embedder.Embed(ctx, doc.Content)
    if err != nil {
        return err
    }

    // Add to vector graph
    node := &vectorgraphdb.GraphNode{
        ID:        doc.ID,
        Content:   doc.Content,
        Embedding: embedding,
        Metadata: map[string]string{
            "path":     doc.Path,
            "type":     string(doc.Type),
            "language": doc.Language,
        },
    }

    return a.db.AddNode(ctx, node)
}
```

#### Dual Indexing Pipeline

```go
// core/search/pipeline/indexer.go

type DualIndexer struct {
    bleveIndex   *bleve.IndexManager
    vectorDB     *adapter.VectorDBAdapter
    manifest     *cmt.CMT

    batchSize    int
    embedBatch   bool  // Batch embeddings for efficiency
}

func (idx *DualIndexer) IndexDocument(ctx context.Context, doc *search.Document) error {
    // 1. Index in Bleve (fast, local)
    if err := idx.bleveIndex.Index(ctx, doc); err != nil {
        return fmt.Errorf("bleve index: %w", err)
    }

    // 2. Index in Vector DB (slower, embedding required)
    if err := idx.vectorDB.IndexDocument(ctx, doc); err != nil {
        // Log but don't fail - Bleve is primary
        log.Printf("vector index warning: %v", err)
    }

    // 3. Update CMT manifest
    entry := cmt.FileEntry{
        Path:        doc.Path,
        ContentHash: sha256Hash([]byte(doc.Content)),
        Mtime:       doc.ModifiedAt.Unix(),
        Size:        doc.SizeBytes,
        IndexedAt:   time.Now().Unix(),
    }
    if err := idx.manifest.Insert(entry); err != nil {
        return fmt.Errorf("manifest update: %w", err)
    }

    return nil
}
```

---

### Integration with Skills System

```go
// core/skills/search_skills.go

package skills

import (
    "encoding/json"

    "sylk/core/search"
)

func RegisterSearchSkills(registry *Registry, searchSystem *search.SearchSystem) {
    // Search codebase skill
    registry.Register(&Skill{
        Name:        "search_code",
        Description: "Search the codebase for code, symbols, and documentation",
        Domain:      "search",
        Priority:    1,
        InputSchema: InputSchema{
            Properties: map[string]Property{
                "query": {Type: "string", Description: "Search query"},
                "type":  {Type: "string", Description: "Filter by type (go, ts, md)"},
                "path":  {Type: "string", Description: "Filter by path glob"},
                "fuzzy": {Type: "boolean", Description: "Enable fuzzy matching"},
            },
            Required: []string{"query"},
        },
        Handler: func(input json.RawMessage) (any, error) {
            var params struct {
                Query string `json:"query"`
                Type  string `json:"type"`
                Path  string `json:"path"`
                Fuzzy bool   `json:"fuzzy"`
            }
            if err := json.Unmarshal(input, &params); err != nil {
                return nil, err
            }

            req := search.DefaultSearchRequest(params.Query)
            req.Fuzzy = params.Fuzzy
            if params.Type != "" {
                req.Languages = []string{params.Type}
            }
            if params.Path != "" {
                req.Paths = []string{params.Path}
            }

            return searchSystem.Search(context.Background(), req)
        },
    })

    // Git-based skills
    gitSkills := searchSystem.GitSkills()

    registry.Register(&Skill{
        Name:        "git_diff",
        Description: "Show changes between commits or branches",
        Domain:      "git",
        Priority:    2,
        InputSchema: InputSchema{
            Properties: map[string]Property{
                "from": {Type: "string", Description: "From ref (commit/branch)"},
                "to":   {Type: "string", Description: "To ref (default: HEAD)"},
            },
            Required: []string{"from"},
        },
        Handler: func(input json.RawMessage) (any, error) {
            var params struct {
                From string `json:"from"`
                To   string `json:"to"`
            }
            if err := json.Unmarshal(input, &params); err != nil {
                return nil, err
            }
            if params.To == "" {
                params.To = "HEAD"
            }
            return gitSkills.Diff(context.Background(), params.From, params.To)
        },
    })

    registry.Register(&Skill{
        Name:        "git_log",
        Description: "Show commit history for a file or path",
        Domain:      "git",
        Priority:    2,
        InputSchema: InputSchema{
            Properties: map[string]Property{
                "path": {Type: "string", Description: "File or directory path"},
                "n":    {Type: "integer", Description: "Number of commits (default: 10)"},
            },
        },
        Handler: func(input json.RawMessage) (any, error) {
            var params struct {
                Path string `json:"path"`
                N    int    `json:"n"`
            }
            if err := json.Unmarshal(input, &params); err != nil {
                return nil, err
            }
            if params.N == 0 {
                params.N = 10
            }
            return gitSkills.Log(context.Background(), params.Path, params.N)
        },
    })

    registry.Register(&Skill{
        Name:        "git_blame",
        Description: "Show line-by-line authorship for a file",
        Domain:      "git",
        Priority:    3,
        InputSchema: InputSchema{
            Properties: map[string]Property{
                "path": {Type: "string", Description: "File path"},
            },
            Required: []string{"path"},
        },
        Handler: func(input json.RawMessage) (any, error) {
            var params struct {
                Path string `json:"path"`
            }
            if err := json.Unmarshal(input, &params); err != nil {
                return nil, err
            }
            return gitSkills.Blame(context.Background(), params.Path)
        },
    })
}
```

---

### Startup/Shutdown Lifecycle

#### SearchSystem Initialization

```go
// core/search/search.go

type SearchSystem struct {
    config             SearchConfig
    rootPath           string

    // Resources
    goroutineBudget    *concurrency.GoroutineBudget
    fileHandleBudget   *resources.FileHandleBudget
    pressureController *resources.PressureController

    // Core components
    bleveIndex         *bleve.IndexManager
    manifest           *cmt.CMT
    git                *git.Integration
    vectorAdapter      *adapter.VectorDBAdapter

    // Pipeline
    scanner            *pipeline.Scanner
    parser             *pipeline.Parser
    indexer            *pipeline.DualIndexer

    // Detection
    watcher            *detection.Watcher
    staleness          *detection.Detector
    validator          *detection.CrossValidator

    // Search
    coordinator        *coordinator.SearchCoordinator
    agentAPI           *agent.AgentSearchAPI
    gitSkills          *git.Skills

    // State
    mu                 sync.RWMutex
    started            bool
    healthy            atomic.Bool
}

type SearchConfig struct {
    RootPath           string
    StoragePath        string        // ~/.sylk/projects/<hash>/

    // Indexing
    Workers            int
    BatchSize          int
    MaxFileSize        int64

    // Search
    DefaultLimit       int
    CacheSize          int
    CacheTTL           time.Duration

    // Detection
    WatcherEnabled     bool
    ReconcileInterval  time.Duration
    ValidationInterval time.Duration
}

func DefaultSearchConfig(rootPath, storagePath string) SearchConfig {
    return SearchConfig{
        RootPath:           rootPath,
        StoragePath:        storagePath,
        Workers:            4,
        BatchSize:          100,
        MaxFileSize:        1024 * 1024, // 1MB
        DefaultLimit:       20,
        CacheSize:          1000,
        CacheTTL:           5 * time.Minute,
        WatcherEnabled:     true,
        ReconcileInterval:  5 * time.Minute,
        ValidationInterval: 5 * time.Minute,
    }
}

func NewSearchSystem(
    config SearchConfig,
    goroutineBudget *concurrency.GoroutineBudget,
    fileHandleBudget *resources.FileHandleBudget,
    pressureController *resources.PressureController,
    vectorDB *vectorgraphdb.VectorGraphDB,
    embedder adapter.Embedder,
) (*SearchSystem, error) {

    s := &SearchSystem{
        config:             config,
        rootPath:           config.RootPath,
        goroutineBudget:    goroutineBudget,
        fileHandleBudget:   fileHandleBudget,
        pressureController: pressureController,
    }

    // 1. Initialize Bleve index
    bleveConfig := bleve.IndexConfig{
        Path:          filepath.Join(config.StoragePath, "documents.bleve"),
        BatchSize:     config.BatchSize,
        MaxConcurrent: config.Workers,
    }
    var err error
    s.bleveIndex, err = bleve.NewIndexManager(bleveConfig)
    if err != nil {
        return nil, fmt.Errorf("bleve index: %w", err)
    }

    // 2. Load or create CMT manifest
    cmtPath := filepath.Join(config.StoragePath, "manifest.cmt")
    s.manifest, err = cmt.Load(cmtPath)
    if err != nil {
        // Create new manifest
        s.manifest = cmt.New()
    }

    // 3. Initialize go-git (optional - may not be a git repo)
    s.git, _ = git.NewIntegration(config.RootPath)
    // Ignore error - git is optional

    // 4. Create vector adapter
    s.vectorAdapter = adapter.NewVectorDBAdapter(vectorDB, embedder)

    // 5. Create pipeline components
    scanConfig := pipeline.DefaultScanConfig(config.RootPath)
    scanConfig.MaxFileSize = config.MaxFileSize
    s.scanner, err = pipeline.NewScanner(scanConfig, fileHandleBudget, "search")
    if err != nil {
        return nil, fmt.Errorf("scanner: %w", err)
    }
    s.parser = pipeline.NewParser()
    s.indexer = pipeline.NewDualIndexer(s.bleveIndex, s.vectorAdapter, s.manifest, config.BatchSize)

    // 6. Create detection components
    watchConfig := detection.DefaultWatcherConfig(config.RootPath)
    s.watcher, err = detection.NewWatcher(watchConfig, s.indexer)
    if err != nil {
        return nil, fmt.Errorf("watcher: %w", err)
    }
    s.staleness = detection.NewDetector(config.RootPath, s.manifest, s.git)
    s.validator = detection.NewCrossValidator(config.RootPath, s.manifest, s.git)

    // 7. Create search coordinator
    s.coordinator = coordinator.NewSearchCoordinator(
        s.bleveIndex,
        s.vectorAdapter,
        config.CacheSize,
        config.CacheTTL,
    )

    // 8. Create agent API
    s.agentAPI = agent.NewAgentSearchAPI(s.coordinator)

    // 9. Create git skills
    if s.git != nil {
        s.gitSkills = git.NewSkills(s.git)
    }

    return s, nil
}
```

#### Startup Sequence

```go
// core/search/search.go

func (s *SearchSystem) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.started {
        return nil
    }

    // 1. Register pressure callback
    s.pressureController.RegisterCallback("search", s.handlePressure)

    // 2. Verify integrity (parallel CMT + git check)
    if err := s.verifyIntegrity(ctx); err != nil {
        // Log but continue - will rebuild if needed
        log.Printf("integrity check warning: %v", err)
    }

    // 3. Detect stale files
    staleResult, err := s.staleness.Detect(ctx)
    if err != nil {
        return fmt.Errorf("staleness detection: %w", err)
    }

    // 4. Index stale files (priority-based)
    if len(staleResult.ChangedFiles) > 0 || len(staleResult.NewFiles) > 0 {
        if err := s.indexStale(ctx, staleResult); err != nil {
            return fmt.Errorf("index stale: %w", err)
        }
    }

    // 5. Update git HEAD in manifest
    if s.git != nil && s.git.IsHealthy() {
        head, _ := s.git.HEAD()
        s.manifest.SetMetadata(cmt.CMTMetadata{
            LastGitHead: head.String(),
        })
    }

    // 6. Persist manifest
    cmtPath := filepath.Join(s.config.StoragePath, "manifest.cmt")
    if err := s.manifest.Save(cmtPath); err != nil {
        return fmt.Errorf("save manifest: %w", err)
    }

    // 7. Start file watcher (if enabled)
    if s.config.WatcherEnabled {
        if err := s.watcher.Start(ctx); err != nil {
            return fmt.Errorf("start watcher: %w", err)
        }
    }

    // 8. Start background reconciliation
    go s.reconciliationLoop(ctx)

    s.started = true
    s.healthy.Store(true)

    return nil
}

func (s *SearchSystem) verifyIntegrity(ctx context.Context) error {
    var wg sync.WaitGroup
    var cmtErr, gitErr error

    wg.Add(2)

    go func() {
        defer wg.Done()
        cmtErr = s.manifest.Verify()
    }()

    go func() {
        defer wg.Done()
        if s.git != nil {
            gitErr = s.git.VerifyIntegrity()
        }
    }()

    wg.Wait()

    if cmtErr != nil {
        // CMT corrupted - need full rebuild
        return s.rebuildManifest(ctx)
    }

    if gitErr != nil {
        // Git corrupted - disable git integration
        s.git = nil
        log.Printf("git disabled due to corruption: %v", gitErr)
    }

    return nil
}

func (s *SearchSystem) indexStale(ctx context.Context, result *detection.DetectionResult) error {
    // Prioritize files
    allFiles := append(result.ChangedFiles, result.NewFiles...)
    prioritized := s.prioritizeFiles(allFiles)

    // Index P0 (critical) synchronously
    p0Files := prioritized[0]
    if len(p0Files) > 0 {
        if err := s.indexFiles(ctx, p0Files); err != nil {
            return err
        }
    }

    // Index P1 (important) synchronously
    p1Files := prioritized[1]
    if len(p1Files) > 0 {
        if err := s.indexFiles(ctx, p1Files); err != nil {
            return err
        }
    }

    // Queue P2+ for background indexing
    for i := 2; i < len(prioritized); i++ {
        go s.indexFiles(context.Background(), prioritized[i])
    }

    return nil
}

func (s *SearchSystem) prioritizeFiles(files []string) [][]string {
    // P0: go.mod, package.json, main entry points
    // P1: Core source files in working packages
    // P2: Other source files
    // P3: Tests, docs

    priorities := make([][]string, 4)

    for _, f := range files {
        switch {
        case isEntryPoint(f):
            priorities[0] = append(priorities[0], f)
        case isCoreSource(f):
            priorities[1] = append(priorities[1], f)
        case isSource(f):
            priorities[2] = append(priorities[2], f)
        default:
            priorities[3] = append(priorities[3], f)
        }
    }

    return priorities
}
```

#### Shutdown Sequence

```go
// core/search/search.go

func (s *SearchSystem) Stop(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if !s.started {
        return nil
    }

    // 1. Stop file watcher
    if err := s.watcher.Stop(); err != nil {
        log.Printf("watcher stop error: %v", err)
    }

    // 2. Unregister pressure callback
    s.pressureController.UnregisterCallback("search")

    // 3. Persist manifest with current state
    cmtPath := filepath.Join(s.config.StoragePath, "manifest.cmt")
    if err := s.manifest.Save(cmtPath); err != nil {
        log.Printf("manifest save error: %v", err)
    }

    // 4. Close Bleve index
    if err := s.bleveIndex.Close(); err != nil {
        log.Printf("bleve close error: %v", err)
    }

    s.started = false
    s.healthy.Store(false)

    return nil
}
```

---

### Application Bootstrap Integration

```go
// cmd/sylk/main.go (or wherever main bootstrap occurs)

func bootstrap(ctx context.Context, config *Config) (*App, error) {
    // ... existing initialization

    // Initialize resource managers
    goroutineBudget := concurrency.NewGoroutineBudget(...)
    fileHandleBudget := resources.NewFileHandleBudget(...)
    pressureController := resources.NewPressureController(...)

    // Initialize vector DB (existing)
    vectorDB, err := vectorgraphdb.Open(...)

    // Initialize search system
    searchConfig := search.DefaultSearchConfig(
        config.ProjectRoot,
        config.StoragePath,
    )
    searchSystem, err := search.NewSearchSystem(
        searchConfig,
        goroutineBudget,
        fileHandleBudget,
        pressureController,
        vectorDB,
        embedder,
    )
    if err != nil {
        return nil, fmt.Errorf("search system: %w", err)
    }

    // Start search system
    if err := searchSystem.Start(ctx); err != nil {
        return nil, fmt.Errorf("start search: %w", err)
    }

    // Register search skills
    skills.RegisterSearchSkills(skillRegistry, searchSystem)

    // Wire into session manager
    sessionManager.SetSearchSystem(searchSystem)

    // Wire into guide router
    guideRouter.SetSearchSystem(searchSystem)

    // ... rest of initialization

    return app, nil
}
```

---

### Signal Handler Integration

```go
// core/signal/handler.go - ADD to existing handler

func (h *Handler) registerHandlers() {
    // ... existing handlers

    // Register search system handler
    h.RegisterAgent("search", func(ctx context.Context) error {
        if h.searchSystem != nil {
            return h.searchSystem.Stop(ctx)
        }
        return nil
    })
}
```

---

### WAL Integration for Crash Recovery

Use existing `core/concurrency/wal.go` patterns:

```go
// core/search/cmt/wal.go

package cmt

import (
    "sylk/core/concurrency" // Use existing WAL
)

type WALManager struct {
    wal *concurrency.WAL
}

func NewWALManager(path string) (*WALManager, error) {
    wal, err := concurrency.NewWAL(path, concurrency.WALConfig{
        MaxSegmentSize: 10 * 1024 * 1024, // 10MB
        SyncMode:       concurrency.SyncFsync,
    })
    if err != nil {
        return nil, err
    }

    return &WALManager{wal: wal}, nil
}

type CMTOperation struct {
    Type  string      // "insert", "delete", "update"
    Entry FileEntry
}

func (w *WALManager) LogOperation(op CMTOperation) error {
    data, err := json.Marshal(op)
    if err != nil {
        return err
    }
    return w.wal.Append(data)
}

func (w *WALManager) Recover(manifest *CMT) error {
    return w.wal.Replay(func(data []byte) error {
        var op CMTOperation
        if err := json.Unmarshal(data, &op); err != nil {
            return err
        }

        switch op.Type {
        case "insert":
            manifest.Insert(op.Entry)
        case "delete":
            manifest.Delete(op.Entry.Path)
        }

        return nil
    })
}
```

---

### CLI Integration

```go
// cmd/sylk/search.go

package main

import (
    "github.com/spf13/cobra"

    "sylk/core/search"
)

func newSearchCmd(app *App) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "search [query]",
        Short: "Search the codebase",
        Args:  cobra.MinimumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            query := strings.Join(args, " ")

            // Parse flags
            mode, _ := cmd.Flags().GetString("mode")
            fuzzy, _ := cmd.Flags().GetBool("fuzzy")
            types, _ := cmd.Flags().GetStringSlice("type")
            paths, _ := cmd.Flags().GetStringSlice("path")
            limit, _ := cmd.Flags().GetInt("limit")
            format, _ := cmd.Flags().GetString("format")
            facets, _ := cmd.Flags().GetBool("facets")

            // Build request
            req := search.DefaultSearchRequest(query)
            req.Fuzzy = fuzzy
            req.Limit = limit
            req.IncludeFacets = facets

            if mode != "" {
                req.Mode = search.SearchMode(mode)
            }
            if len(types) > 0 {
                req.Languages = types
            }
            if len(paths) > 0 {
                req.Paths = paths
            }

            // Execute search
            result, err := app.SearchSystem.Search(cmd.Context(), req)
            if err != nil {
                return err
            }

            // Format output
            return formatSearchResult(cmd.OutOrStdout(), result, format)
        },
    }

    cmd.Flags().String("mode", "hybrid", "Search mode (hybrid|lexical|semantic)")
    cmd.Flags().Bool("fuzzy", true, "Enable fuzzy matching")
    cmd.Flags().StringSlice("type", nil, "Filter by file type")
    cmd.Flags().StringSlice("path", nil, "Filter by path glob")
    cmd.Flags().Int("limit", 20, "Maximum results")
    cmd.Flags().String("format", "default", "Output format (default|paths|json)")
    cmd.Flags().Bool("facets", false, "Show facet counts")

    return cmd
}

func newIndexCmd(app *App) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "index",
        Short: "Manage the search index",
    }

    cmd.AddCommand(&cobra.Command{
        Use:   "rebuild",
        Short: "Full rebuild of the index",
        RunE: func(cmd *cobra.Command, args []string) error {
            return app.SearchSystem.RebuildIndex(cmd.Context())
        },
    })

    cmd.AddCommand(&cobra.Command{
        Use:   "status",
        Short: "Show index status",
        RunE: func(cmd *cobra.Command, args []string) error {
            status := app.SearchSystem.Status()
            return formatIndexStatus(cmd.OutOrStdout(), status)
        },
    })

    return cmd
}
```

---

### Integration Points Summary

| Component | Integrates With | Integration Pattern |
|-----------|-----------------|---------------------|
| SearchSystem | GoroutineBudget | Acquire/Release for parallel ops |
| Scanner | FileHandleBudget | Acquire/Release for file reads |
| SearchSystem | PressureController | Callback for degradation |
| SearchSystem | Session Manager | Per-session context, lifecycle hooks |
| SearchSystem | Guide Router | Message routing for SEARCH_REQUEST |
| Librarian Agent | AgentSearchAPI | Direct API calls with strategies |
| SearchCoordinator | VectorGraphDB | VectorDBAdapter for hybrid search |
| Git Skills | Skills Registry | Registered as agent tools |
| SearchSystem | Signal Handler | Graceful shutdown |
| CMT | WAL | Crash recovery using existing patterns |
| CLI | Cobra Commands | search, index subcommands |
