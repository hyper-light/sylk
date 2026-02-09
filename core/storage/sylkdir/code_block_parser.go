package sylkdir

import (
	"context"
	"path/filepath"

	"github.com/adalundhe/sylk/core/treesitter"
)

// treeSitterBlockParser implements CodeBlockParser using tree-sitter.
type treeSitterBlockParser struct {
	tool *treesitter.TreeSitterTool
}

// NewTreeSitterBlockParser creates a CodeBlockParser backed by tree-sitter.
// The tool should have auto-download enabled for grammar availability.
func NewTreeSitterBlockParser(tool *treesitter.TreeSitterTool) CodeBlockParser {
	return &treeSitterBlockParser{tool: tool}
}

// ParseBlock parses a code block body and returns symbol boundaries.
// Returns nil if grammar unavailable or parsing fails (graceful degradation).
func (p *treeSitterBlockParser) ParseBlock(ctx context.Context, language string, content []byte) []SymbolBound {
	if p.tool == nil || language == "" || len(content) == 0 {
		return nil
	}

	// Use a fake file path with appropriate extension for language detection.
	ext := languageToExtension(language)
	if ext == "" {
		return nil
	}
	fakePath := "block" + ext

	result, err := p.tool.Parse(ctx, fakePath, content)
	if err != nil || result == nil {
		return nil
	}

	return parseResultToSymbolBounds(result)
}

// parseResultToSymbolBounds converts a tree-sitter ParseResult to SymbolBound slice.
func parseResultToSymbolBounds(result *treesitter.ParseResult) []SymbolBound {
	var bounds []SymbolBound

	for _, fn := range result.Functions {
		strength := uint32(2) // Function/Method = strength 2.
		bounds = append(bounds, SymbolBound{
			StartLine: fn.StartLine,
			EndLine:   fn.EndLine,
			Strength:  strength,
		})
	}

	for _, tp := range result.Types {
		strength := typeKindStrength(tp.Kind)
		bounds = append(bounds, SymbolBound{
			StartLine: tp.StartLine,
			EndLine:   tp.EndLine,
			Strength:  strength,
		})
	}

	return bounds
}

// typeKindStrength maps tree-sitter TypeInfo.Kind to boundary strength.
func typeKindStrength(kind string) uint32 {
	switch kind {
	case "interface":
		return 3
	default: // struct, class, enum, etc.
		return 3
	}
}

// languageToExtension maps common tree-sitter grammar names to file extensions.
// Used to create fake file paths for language detection in tree-sitter.
func languageToExtension(language string) string {
	ext, ok := langExtMap[language]
	if ok {
		return ext
	}
	// Try with a dot prefix as a fallback.
	return filepath.Ext("file." + language)
}

// langExtMap maps tree-sitter grammar names to canonical file extensions.
var langExtMap = map[string]string{
	"go":         ".go",
	"python":     ".py",
	"javascript": ".js",
	"typescript": ".ts",
	"rust":       ".rs",
	"java":       ".java",
	"c":          ".c",
	"cpp":        ".cpp",
	"c_sharp":    ".cs",
	"ruby":       ".rb",
	"php":        ".php",
	"swift":      ".swift",
	"kotlin":     ".kt",
	"scala":      ".scala",
	"lua":        ".lua",
	"elixir":     ".ex",
	"haskell":    ".hs",
	"zig":        ".zig",
	"bash":       ".sh",
	"html":       ".html",
	"css":        ".css",
	"json":       ".json",
	"yaml":       ".yaml",
	"toml":       ".toml",
	"markdown":   ".md",
	"vue":        ".vue",
	"svelte":     ".svelte",
	"sql":        ".sql",
	"r":          ".r",
	"julia":      ".jl",
	"dart":       ".dart",
}
