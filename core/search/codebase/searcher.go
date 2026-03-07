package codebase

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/treesitter"
)

// Match represents a single line match in a file.
type Match struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
	// SymbolContext is the enclosing symbol name (function/type/method) when
	// tree-sitter can determine it. Empty when unavailable.
	SymbolContext string `json:"symbol_context,omitempty"`
}

// SearchConfig configures a content search operation.
type SearchConfig struct {
	Root            string
	Pattern         *regexp.Regexp
	IncludePatterns []string // gobwas/glob file filters
	ExcludePatterns []string
	MaxMatches      int
	CaseInsensitive bool
	ContextLines    int  // lines of context around each match (0 = exact line only)
	WithSymbols     bool // enrich matches with tree-sitter symbol context
}

// GlobConfig configures a file glob operation.
type GlobConfig struct {
	Root            string
	Pattern         string   // gobwas/glob pattern
	ExcludePatterns []string // additional exclusions
}

// Search performs a parallel, gitignore-aware regex search across files.
// Files are discovered via Walker, then searched concurrently with bounded
// parallelism. Stops early once maxMatches is reached.
func Search(ctx context.Context, cfg SearchConfig) ([]Match, error) {
	if cfg.Pattern == nil {
		return nil, fmt.Errorf("pattern is required")
	}
	if cfg.MaxMatches <= 0 {
		cfg.MaxMatches = 200
	}

	walker := NewWalker(cfg.Root)
	if err := walker.SetInclude(cfg.IncludePatterns); err != nil {
		return nil, fmt.Errorf("include pattern: %w", err)
	}
	if err := walker.SetExclude(cfg.ExcludePatterns); err != nil {
		return nil, fmt.Errorf("exclude pattern: %w", err)
	}

	files := walker.Collect()
	if len(files) == 0 {
		return []Match{}, nil
	}

	// Parallel search with bounded concurrency.
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}

	var (
		mu      sync.Mutex
		results []Match
		found   atomic.Int32
		wg      sync.WaitGroup
		fileCh  = make(chan string, workers*2)
	)

	// Feed files.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(fileCh)
		for _, f := range files {
			if ctx.Err() != nil || int(found.Load()) >= cfg.MaxMatches {
				return
			}
			fileCh <- f
		}
	}()

	// Search workers.
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for relPath := range fileCh {
				if ctx.Err() != nil || int(found.Load()) >= cfg.MaxMatches {
					return
				}
				matches := searchFile(cfg.Root, relPath, cfg.Pattern, cfg.MaxMatches-int(found.Load()))
				if len(matches) == 0 {
					continue
				}

				if cfg.WithSymbols {
					enrichWithSymbols(cfg.Root, relPath, matches)
				}

				mu.Lock()
				results = append(results, matches...)
				mu.Unlock()
				found.Add(int32(len(matches)))
			}
		}()
	}

	wg.Wait()

	if len(results) > cfg.MaxMatches {
		results = results[:cfg.MaxMatches]
	}
	return results, nil
}

// searchFile scans a single file line-by-line for regex matches.
// Reads via bufio.Scanner to bound memory to one line at a time.
func searchFile(root, relPath string, pattern *regexp.Regexp, remaining int) []Match {
	absPath := filepath.Join(root, relPath)
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Skip files larger than 2MB to avoid stalling on generated files.
	if info, err := f.Stat(); err != nil || info.Size() > 2*1024*1024 {
		return nil
	}

	var matches []Match
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if pattern.MatchString(line) {
			matches = append(matches, Match{
				File:    relPath,
				Line:    lineNum,
				Content: truncateLine(line, 500),
			})
			if len(matches) >= remaining {
				break
			}
		}
	}
	return matches
}

// truncateLine caps a line to maxLen runes.
func truncateLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// enrichWithSymbols uses tree-sitter to annotate each match with its
// enclosing symbol (function, type, method). Best-effort: silently skips
// files where tree-sitter has no grammar or parsing fails.
func enrichWithSymbols(root, relPath string, matches []Match) {
	if len(matches) == 0 {
		return
	}
	lang := languageForFile(relPath)
	if lang == "" {
		return
	}

	absPath := filepath.Join(root, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return
	}

	parser := treesitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguageByName(lang); err != nil {
		return
	}

	tree, err := parser.Parse(content, nil)
	if err != nil {
		return
	}
	defer tree.Close()

	// Build a line→byte-offset index for efficient lookup.
	lineOffsets := buildLineOffsets(content)

	for i := range matches {
		lineIdx := matches[i].Line - 1
		if lineIdx < 0 || lineIdx >= len(lineOffsets) {
			continue
		}
		byteOffset := lineOffsets[lineIdx]
		node := tree.RootNode().NamedDescendantForByteRange(uint(byteOffset), uint(byteOffset))
		if node == nil {
			continue
		}
		matches[i].SymbolContext = findEnclosingSymbol(node)
	}
}

// buildLineOffsets returns the byte offset of each line start.
func buildLineOffsets(content []byte) []int {
	offsets := []int{0}
	for i, b := range content {
		if b == '\n' && i+1 < len(content) {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// findEnclosingSymbol walks up from a node to find the nearest function,
// method, type, or class declaration and returns its name.
func findEnclosingSymbol(node *treesitter.Node) string {
	for n := node; n != nil; n = n.Parent() {
		kind := n.Type()
		switch kind {
		case "function_declaration", "method_declaration",
			"function_definition", "method_definition",
			"function_item", "impl_item":
			if name := n.ChildByFieldName("name"); name != nil {
				return name.Content()
			}
		case "type_declaration", "type_spec",
			"struct_item", "class_declaration",
			"class_definition", "interface_declaration":
			if name := n.ChildByFieldName("name"); name != nil {
				return name.Content()
			}
		}
	}
	return ""
}

// GlobFiles returns files matching a glob pattern, respecting .gitignore.
func GlobFiles(cfg GlobConfig) ([]string, error) {
	walker := NewWalker(cfg.Root)
	if err := walker.SetInclude([]string{cfg.Pattern}); err != nil {
		return nil, fmt.Errorf("glob pattern: %w", err)
	}
	if err := walker.SetExclude(cfg.ExcludePatterns); err != nil {
		return nil, fmt.Errorf("exclude pattern: %w", err)
	}
	return walker.Collect(), nil
}

// languageForFile returns the tree-sitter language name for a file extension.
// Returns "" if the extension is not recognized.
func languageForFile(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".sh", ".bash":
		return "bash"
	case ".css":
		return "css"
	case ".html", ".htm":
		return "html"
	case ".md":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	}
	return ""
}

// SymbolMatch represents a symbol found via tree-sitter structural search.
type SymbolMatch struct {
	File      string `json:"file"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // function, method, type, import
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Receiver  string `json:"receiver,omitempty"`
}

// SearchSymbols finds functions, types, and methods matching a name pattern
// across the codebase using tree-sitter.
func SearchSymbols(ctx context.Context, root string, namePattern *regexp.Regexp, includePatterns []string, maxResults int) ([]SymbolMatch, error) {
	if maxResults <= 0 {
		maxResults = 100
	}

	walker := NewWalker(root)
	if err := walker.SetInclude(includePatterns); err != nil {
		return nil, err
	}
	files := walker.Collect()

	queryCache := treesitter.NewEmbeddedQueryCache()
	defer queryCache.Close()

	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}

	var (
		mu      sync.Mutex
		results []SymbolMatch
		found   atomic.Int32
		wg      sync.WaitGroup
		fileCh  = make(chan string, workers*2)
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(fileCh)
		for _, f := range files {
			if ctx.Err() != nil || int(found.Load()) >= maxResults {
				return
			}
			lang := languageForFile(f)
			if lang == "" || !treesitter.HasQuery(lang) {
				continue
			}
			fileCh <- f
		}
	}()

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parser := treesitter.NewParser()
			defer parser.Close()

			for relPath := range fileCh {
				if ctx.Err() != nil || int(found.Load()) >= maxResults {
					return
				}
				matches := extractSymbolMatches(parser, queryCache, root, relPath, namePattern)
				if len(matches) == 0 {
					continue
				}
				mu.Lock()
				results = append(results, matches...)
				mu.Unlock()
				found.Add(int32(len(matches)))
			}
		}()
	}

	wg.Wait()
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
}

func extractSymbolMatches(parser *treesitter.Parser, queryCache *treesitter.EmbeddedQueryCache, root, relPath string, namePattern *regexp.Regexp) []SymbolMatch {
	lang := languageForFile(relPath)
	if lang == "" {
		return nil
	}

	absPath := filepath.Join(root, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}

	if err := parser.SetLanguageByName(lang); err != nil {
		return nil
	}

	grammar, err := treesitter.LoadGrammar(lang)
	if err != nil {
		return nil
	}

	tree, err := parser.Parse(content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	symbols, err := treesitter.ExtractSymbolsWithQuery(queryCache, grammar, lang, tree)
	if err != nil {
		return nil
	}

	var matches []SymbolMatch

	for _, fn := range symbols.Functions {
		if namePattern.MatchString(fn.Name) {
			kind := "function"
			if fn.IsMethod {
				kind = "method"
			}
			matches = append(matches, SymbolMatch{
				File:      relPath,
				Name:      fn.Name,
				Kind:      kind,
				StartLine: int(fn.StartLine),
				EndLine:   int(fn.EndLine),
				Receiver:  fn.Receiver,
			})
		}
	}

	for _, t := range symbols.Types {
		if namePattern.MatchString(t.Name) {
			matches = append(matches, SymbolMatch{
				File:      relPath,
				Name:      t.Name,
				Kind:      t.Kind,
				StartLine: int(t.StartLine),
				EndLine:   int(t.EndLine),
			})
		}
	}

	return matches
}
