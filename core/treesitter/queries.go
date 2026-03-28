package treesitter

import (
	"embed"
	"fmt"
	"sync"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

//go:embed queries/*.scm
var queryFS embed.FS

// EmbeddedQueryCache holds compiled queries per language for reuse.
// Uses embedded .scm query files for CGo-optimized symbol extraction.
type EmbeddedQueryCache struct {
	queries map[string]*Query
	mu      sync.RWMutex
}

// NewEmbeddedQueryCache creates a new embedded query cache.
func NewEmbeddedQueryCache() *EmbeddedQueryCache {
	return &EmbeddedQueryCache{
		queries: make(map[string]*Query),
	}
}

// GetQuery returns a compiled query for the given language, loading and compiling it if necessary.
func (qc *EmbeddedQueryCache) GetQuery(lang *sitter.Language, langName string) (*Query, error) {
	qc.mu.RLock()
	if q, ok := qc.queries[langName]; ok {
		qc.mu.RUnlock()
		return q, nil
	}
	qc.mu.RUnlock()

	qc.mu.Lock()
	defer qc.mu.Unlock()

	// Double-check after acquiring write lock
	if q, ok := qc.queries[langName]; ok {
		return q, nil
	}

	// Load query from embedded filesystem
	queryPath := fmt.Sprintf("queries/%s.scm", langName)
	queryBytes, err := queryFS.ReadFile(queryPath)
	if err != nil {
		return nil, fmt.Errorf("query file not found for %s: %w", langName, err)
	}

	// Compile the query
	q, err := NewQuery(lang, string(queryBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to compile query for %s: %w", langName, err)
	}

	qc.queries[langName] = q
	return q, nil
}

// Close releases all cached queries.
func (qc *EmbeddedQueryCache) Close() {
	qc.mu.Lock()
	defer qc.mu.Unlock()
	for _, q := range qc.queries {
		q.Close()
	}
	qc.queries = make(map[string]*Query)
}

// QuerySymbolResult holds symbols extracted via query.
type QuerySymbolResult struct {
	Functions []FunctionInfo
	Types     []TypeInfo
	Imports   []ImportInfo
}

// ExtractSymbolsWithQuery extracts symbols from a tree using the query-based approach.
// This executes the query entirely in C, only crossing CGo boundary for results.
func ExtractSymbolsWithQuery(qc *EmbeddedQueryCache, lang *sitter.Language, langName string, tree *Tree) (*QuerySymbolResult, error) {
	query, err := qc.GetQuery(lang, langName)
	if err != nil {
		return nil, err
	}

	cursor := NewQueryCursor()
	defer cursor.Close()

	source := tree.Source()
	matches := cursor.Matches(query, tree.RootNode(), source)

	result := &QuerySymbolResult{}

	for _, match := range matches {
		processMatch(match, langName, result)
	}

	return result, nil
}

func processMatch(match QueryMatch, langName string, result *QuerySymbolResult) {
	captures := make(map[string]*QueryCapture)
	for i := range match.Captures {
		captures[match.Captures[i].Name] = &match.Captures[i]
	}

	switch langName {
	case "go":
		processGoMatch(captures, result)
	case "python":
		processPythonMatch(captures, result)
	case "javascript", "typescript", "tsx":
		processJSMatch(captures, result)
	case "rust":
		processRustMatch(captures, result)
	case "java":
		processJavaMatch(captures, result)
	case "c", "cpp":
		processCMatch(captures, result)
	case "ruby":
		processRubyMatch(captures, result)
	case "kotlin":
		processKotlinMatch(captures, result)
	case "swift":
		processSwiftMatch(captures, result)
	case "markdown":
		processMarkdownMatch(captures, result)
	case "json":
		processJSONMatch(captures, result)
	case "yaml":
		processYAMLMatch(captures, result)
	case "toml":
		processTOMLMatch(captures, result)
	case "bash":
		processBashMatch(captures, result)
	case "css":
		processCSSMatch(captures, result)
	case "html":
		processHTMLMatch(captures, result)
	}
}

func processGoMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Function declarations
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Method declarations
	if name, ok := captures["method.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
			IsMethod:  true,
		}
		if recv, ok := captures["method.receiver"]; ok {
			fn.Receiver = recv.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Type declarations
	if name, ok := captures["type.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if kind, ok := captures["type.kind"]; ok {
			t.Kind = kind.Node.Type()
		}
		result.Types = append(result.Types, t)
		return
	}

	// Import declarations
	if path, ok := captures["import.path"]; ok {
		imp := ImportInfo{
			Path: path.Node.Content(),
			Line: path.Node.StartPosition().Row + 1,
		}
		if alias, ok := captures["import.alias"]; ok {
			imp.Alias = alias.Node.Content()
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processPythonMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Function definitions
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["func.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		if ret, ok := captures["func.return_type"]; ok {
			fn.ReturnType = ret.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Class definitions
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Methods inside classes
	if name, ok := captures["method.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
			IsMethod:  true,
		}
		if params, ok := captures["method.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Imports
	if mod, ok := captures["import.module"]; ok {
		imp := ImportInfo{
			Path: mod.Node.Content(),
			Line: mod.Node.StartPosition().Row + 1,
		}
		if alias, ok := captures["import.alias"]; ok {
			imp.Alias = alias.Node.Content()
		}
		result.Imports = append(result.Imports, imp)
		return
	}

	// From imports
	if mod, ok := captures["from_import.module"]; ok {
		imp := ImportInfo{
			Path: mod.Node.Content(),
			Line: mod.Node.StartPosition().Row + 1,
		}
		if name, ok := captures["from_import.name"]; ok {
			imp.Path = imp.Path + "." + name.Node.Content()
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processJSMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Function declarations
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["func.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Arrow functions
	if name, ok := captures["arrow.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["arrow.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Class declarations
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Methods
	if name, ok := captures["method.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
			IsMethod:  true,
		}
		if params, ok := captures["method.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Interfaces
	if name, ok := captures["interface.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "interface",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Type aliases
	if name, ok := captures["type_alias.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "type",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Imports
	if src, ok := captures["import.source"]; ok {
		imp := ImportInfo{
			Path: src.Node.Content(),
			Line: src.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processRustMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Functions
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["func.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Impl methods
	if name, ok := captures["impl_method.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
			IsMethod:  true,
		}
		if params, ok := captures["impl_method.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Structs
	if name, ok := captures["struct.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "struct",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Enums
	if name, ok := captures["enum.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "enum",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Traits
	if name, ok := captures["trait.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "trait",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Use declarations
	if path, ok := captures["use.path"]; ok {
		imp := ImportInfo{
			Path: path.Node.Content(),
			Line: path.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processJavaMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Classes
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Interfaces
	if name, ok := captures["interface.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "interface",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Methods
	if name, ok := captures["method.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
			IsMethod:  true,
		}
		if params, ok := captures["method.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		if ret, ok := captures["method.return_type"]; ok {
			fn.ReturnType = ret.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Imports
	if path, ok := captures["import.path"]; ok {
		imp := ImportInfo{
			Path: path.Node.Content(),
			Line: path.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processCMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Functions
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["func.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Function declarations
	if name, ok := captures["func_decl.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["func_decl.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Structs
	if name, ok := captures["struct.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "struct",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Typedefs
	if name, ok := captures["typedef.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "typedef",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Includes
	if path, ok := captures["include.path"]; ok {
		imp := ImportInfo{
			Path: path.Node.Content(),
			Line: path.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processRubyMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Methods
	if name, ok := captures["method.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["method.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Classes
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Modules
	if name, ok := captures["module.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "module",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Requires
	if path, ok := captures["require.path"]; ok {
		imp := ImportInfo{
			Path: path.Node.Content(),
			Line: path.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processKotlinMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Functions
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		if params, ok := captures["func.params"]; ok {
			fn.Parameters = params.Node.Content()
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Classes
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Imports
	if path, ok := captures["import.path"]; ok {
		imp := ImportInfo{
			Path: path.Node.Content(),
			Line: path.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processSwiftMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Functions
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Classes
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Structs
	if name, ok := captures["struct.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "struct",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Imports
	if mod, ok := captures["import.module"]; ok {
		imp := ImportInfo{
			Path: mod.Node.Content(),
			Line: mod.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processMarkdownMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// H1-H6 headings
	for level := 1; level <= 6; level++ {
		contentKey := fmt.Sprintf("h%d.content", level)
		if content, ok := captures[contentKey]; ok {
			t := TypeInfo{
				Name:      content.Node.Content(),
				Kind:      fmt.Sprintf("h%d", level),
				StartLine: content.Node.StartPosition().Row + 1,
				EndLine:   content.Node.EndPosition().Row + 1,
			}
			result.Types = append(result.Types, t)
			return
		}
	}

	// Code blocks
	if lang, ok := captures["code.language"]; ok {
		t := TypeInfo{
			Name:      lang.Node.Content(),
			Kind:      "code_block",
			StartLine: lang.Node.StartPosition().Row + 1,
			EndLine:   lang.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
	}
}

func processJSONMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Top-level keys
	if key, ok := captures["key.name"]; ok {
		t := TypeInfo{
			Name:      key.Node.Content(),
			Kind:      "key",
			StartLine: key.Node.StartPosition().Row + 1,
			EndLine:   key.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
	}
}

func processYAMLMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Keys
	if key, ok := captures["key.name"]; ok {
		t := TypeInfo{
			Name:      key.Node.Content(),
			Kind:      "key",
			StartLine: key.Node.StartPosition().Row + 1,
			EndLine:   key.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
	}
}

func processTOMLMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Tables
	if name, ok := captures["table.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "table",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Keys
	if key, ok := captures["pair.key"]; ok {
		t := TypeInfo{
			Name:      key.Node.Content(),
			Kind:      "key",
			StartLine: key.Node.StartPosition().Row + 1,
			EndLine:   key.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
	}
}

func processBashMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Functions
	if name, ok := captures["func.name"]; ok {
		fn := FunctionInfo{
			Name:      name.Node.Content(),
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Functions = append(result.Functions, fn)
		return
	}

	// Variables
	if name, ok := captures["var.name"]; ok {
		t := TypeInfo{
			Name:      name.Node.Content(),
			Kind:      "variable",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Source imports
	if file, ok := captures["source.file"]; ok {
		imp := ImportInfo{
			Path: file.Node.Content(),
			Line: file.Node.StartPosition().Row + 1,
		}
		result.Imports = append(result.Imports, imp)
	}
}

func processCSSMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Selectors
	if sel, ok := captures["rule.selectors"]; ok {
		t := TypeInfo{
			Name:      sel.Node.Content(),
			Kind:      "selector",
			StartLine: sel.Node.StartPosition().Row + 1,
			EndLine:   sel.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// Classes
	if name, ok := captures["class.name"]; ok {
		t := TypeInfo{
			Name:      "." + name.Node.Content(),
			Kind:      "class",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
		return
	}

	// IDs
	if name, ok := captures["id.name"]; ok {
		t := TypeInfo{
			Name:      "#" + name.Node.Content(),
			Kind:      "id",
			StartLine: name.Node.StartPosition().Row + 1,
			EndLine:   name.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
	}
}

func processHTMLMatch(captures map[string]*QueryCapture, result *QuerySymbolResult) {
	// Elements with IDs
	if tag, ok := captures["element.tag"]; ok {
		t := TypeInfo{
			Name:      tag.Node.Content(),
			Kind:      "element",
			StartLine: tag.Node.StartPosition().Row + 1,
			EndLine:   tag.Node.EndPosition().Row + 1,
		}
		result.Types = append(result.Types, t)
	}
}

// HasQuery returns true if a query file exists for the given language.
func HasQuery(langName string) bool {
	queryPath := fmt.Sprintf("queries/%s.scm", langName)
	_, err := queryFS.ReadFile(queryPath)
	return err == nil
}

// ListAvailableQueries returns a list of all languages with embedded queries.
func ListAvailableQueries() []string {
	entries, err := queryFS.ReadDir("queries")
	if err != nil {
		return nil
	}

	var langs []string
	for _, e := range entries {
		name := e.Name()
		if len(name) > 4 && name[len(name)-4:] == ".scm" {
			langs = append(langs, name[:len(name)-4])
		}
	}
	return langs
}
