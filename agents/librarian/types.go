package librarian

import (
	"time"
)

type SymbolKind string

const (
	SymbolKindFunction SymbolKind = "function"
	SymbolKindType     SymbolKind = "type"
	SymbolKindConst    SymbolKind = "const"
	SymbolKindVar      SymbolKind = "var"
	SymbolKindMethod   SymbolKind = "method"
	SymbolKindField    SymbolKind = "field"
	SymbolKindPackage  SymbolKind = "package"
	SymbolKindAny      SymbolKind = "any"
)

type Symbol struct {
	Name      string            `json:"name"`
	Kind      SymbolKind        `json:"kind"`
	Package   string            `json:"package"`
	FilePath  string            `json:"file_path"`
	Line      int               `json:"line"`
	Column    int               `json:"column"`
	EndLine   int               `json:"end_line"`
	EndColumn int               `json:"end_column"`
	Signature string            `json:"signature,omitempty"`
	Doc       string            `json:"doc,omitempty"`
	Exported  bool              `json:"exported"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type FileInfo struct {
	Path        string    `json:"path"`
	Package     string    `json:"package"`
	Imports     []string  `json:"imports"`
	SymbolCount int       `json:"symbol_count"`
	LineCount   int       `json:"line_count"`
	ModifiedAt  time.Time `json:"modified_at"`
	IndexedAt   time.Time `json:"indexed_at"`
	ContentHash string    `json:"content_hash"`
}

type ImportInfo struct {
	Path    string `json:"path"`
	Alias   string `json:"alias,omitempty"`
	Line    int    `json:"line"`
	IsLocal bool   `json:"is_local"`
}

type PackageInfo struct {
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Dir         string       `json:"dir"`
	Files       []string     `json:"files"`
	Imports     []ImportInfo `json:"imports"`
	Dependents  []string     `json:"dependents"`
	SymbolCount int          `json:"symbol_count"`
	Doc         string       `json:"doc,omitempty"`
}

type IndexEntry struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Line        int       `json:"line"`
	Content     string    `json:"content"`
	Embedding   []float32 `json:"embedding,omitempty"`
	IndexedAt   time.Time `json:"indexed_at"`
	ContentHash string    `json:"content_hash"`
}

type IndexStats struct {
	FileCount    int       `json:"file_count"`
	SymbolCount  int       `json:"symbol_count"`
	PackageCount int       `json:"package_count"`
	LastIndexed  time.Time `json:"last_indexed"`
	IndexSize    int64     `json:"index_size_bytes"`
	InProgress   bool      `json:"in_progress"`
	Progress     float64   `json:"progress"`
}

type QueryRequest struct {
	Query       string            `json:"query"`
	Kind        SymbolKind        `json:"kind,omitempty"`
	FilePattern string            `json:"file_pattern,omitempty"`
	Package     string            `json:"package,omitempty"`
	Limit       int               `json:"limit,omitempty"`
	Offset      int               `json:"offset,omitempty"`
	Filters     map[string]string `json:"filters,omitempty"`
}

type QueryResult struct {
	Entries    []IndexEntry  `json:"entries"`
	TotalCount int           `json:"total_count"`
	Took       time.Duration `json:"took"`
	Query      string        `json:"query"`
}

type SearchMatch struct {
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	Content    string `json:"content"`
	MatchStart int    `json:"match_start"`
	MatchEnd   int    `json:"match_end"`
}

type SearchResult struct {
	Matches    []SearchMatch `json:"matches"`
	TotalCount int           `json:"total_count"`
	Took       time.Duration `json:"took"`
	Pattern    string        `json:"pattern"`
}

type ToolConfig struct {
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	ConfigFile string            `json:"config_file,omitempty"`
	Available  bool              `json:"available"`
	Version    string            `json:"version,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type ToolRegistry struct {
	Linters      map[string][]ToolConfig `json:"linters"`
	Formatters   map[string][]ToolConfig `json:"formatters"`
	LSPs         map[string][]ToolConfig `json:"lsps"`
	TypeCheckers map[string][]ToolConfig `json:"type_checkers"`
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		Linters:      make(map[string][]ToolConfig),
		Formatters:   make(map[string][]ToolConfig),
		LSPs:         make(map[string][]ToolConfig),
		TypeCheckers: make(map[string][]ToolConfig),
	}
}

type LanguageInfo struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
	FileCount  int      `json:"file_count"`
	LineCount  int      `json:"line_count"`
	Percentage float64  `json:"percentage"`
}

type RepositoryInfo struct {
	Path       string         `json:"path"`
	Name       string         `json:"name"`
	Languages  []LanguageInfo `json:"languages"`
	ToolConfig *ToolRegistry  `json:"tool_config,omitempty"`
	IndexStats *IndexStats    `json:"index_stats,omitempty"`
}

type FileRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type FileContent struct {
	Path     string     `json:"path"`
	Content  string     `json:"content"`
	Range    *FileRange `json:"range,omitempty"`
	Language string     `json:"language"`
	Encoding string     `json:"encoding"`
}

type UsageInfo struct {
	Symbol    string `json:"symbol"`
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Context   string `json:"context"`
	UsageKind string `json:"usage_kind"`
}

type UsageResult struct {
	Symbol     string      `json:"symbol"`
	Definition *Symbol     `json:"definition,omitempty"`
	Usages     []UsageInfo `json:"usages"`
	TotalCount int         `json:"total_count"`
}

type LibrarianIntent string

const (
	IntentRecall LibrarianIntent = "recall"
	IntentCheck  LibrarianIntent = "check"
)

type LibrarianDomain string

const (
	DomainFiles    LibrarianDomain = "files"
	DomainPatterns LibrarianDomain = "patterns"
	DomainCode     LibrarianDomain = "code"
	DomainTooling  LibrarianDomain = "tooling"
	DomainRemote   LibrarianDomain = "remote"
)

type LibrarianRequest struct {
	ID        string          `json:"id"`
	Intent    LibrarianIntent `json:"intent"`
	Domain    LibrarianDomain `json:"domain"`
	Query     string          `json:"query"`
	Params    map[string]any  `json:"params,omitempty"`
	SessionID string          `json:"session_id"`
	Timestamp time.Time       `json:"timestamp"`
}

type LibrarianResponse struct {
	ID        string        `json:"id"`
	RequestID string        `json:"request_id"`
	Success   bool          `json:"success"`
	Data      any           `json:"data,omitempty"`
	Error     string        `json:"error,omitempty"`
	Took      time.Duration `json:"took"`
	Timestamp time.Time     `json:"timestamp"`
}

type CodebaseSummary struct {
	DirectoryStructure string            `json:"directory_structure"`
	KeyPaths           []string          `json:"key_paths"`
	CodeStyle          CodeStyleInfo     `json:"code_style"`
	Languages          []LanguageInfo    `json:"languages"`
	Dependencies       []string          `json:"dependencies"`
	EntryPoints        []string          `json:"entry_points"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

type CodeStyleInfo struct {
	IndentStyle string `json:"indent_style"`
	IndentSize  int    `json:"indent_size"`
	LineLimit   int    `json:"line_limit"`
	NamingConv  string `json:"naming_convention"`
	TestPattern string `json:"test_pattern"`
	DocStyle    string `json:"doc_style"`
}

type ContextCheckpoint struct {
	ID             string           `json:"id"`
	SessionID      string           `json:"session_id"`
	Timestamp      time.Time        `json:"timestamp"`
	Summary        *CodebaseSummary `json:"summary"`
	TokensUsed     int              `json:"tokens_used"`
	ThresholdLevel int              `json:"threshold_level"`
}

type IndexProgress struct {
	Phase        string        `json:"phase"`
	Current      int           `json:"current"`
	Total        int           `json:"total"`
	CurrentFile  string        `json:"current_file,omitempty"`
	StartedAt    time.Time     `json:"started_at"`
	EstimatedETA time.Duration `json:"estimated_eta,omitempty"`
}

type AgentRoutingInfo struct {
	AgentID   string            `json:"agent_id"`
	AgentType string            `json:"agent_type"`
	Intents   []LibrarianIntent `json:"intents"`
	Domains   []LibrarianDomain `json:"domains"`
	Keywords  []string          `json:"keywords"`
	Priority  int               `json:"priority"`
}
