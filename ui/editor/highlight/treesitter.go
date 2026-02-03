// Package highlight provides keyword-based syntax highlighting for the
// editor. This is the Phase 5a implementation; tree-sitter integration
// replaces it in a later phase.
package highlight

import (
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/ui/theme"
)

// HighlightRegion marks a styled span within a single line.
type HighlightRegion struct {
	StartCol int
	EndCol   int // exclusive
	Category theme.SyntaxCategory
}

// Highlighter performs simple keyword-based syntax highlighting.
type Highlighter struct {
	theme *theme.Theme
}

// NewHighlighter creates a Highlighter with the given theme.
func NewHighlighter(th *theme.Theme) *Highlighter {
	return &Highlighter{theme: th}
}

// Highlight scans content line-by-line and returns highlight regions indexed
// by line number. The language parameter selects the keyword table.
func (h *Highlighter) Highlight(content string, language string) [][]HighlightRegion {
	lines := strings.Split(content, "\n")
	keywords := languageKeywords(language)
	result := make([][]HighlightRegion, len(lines))
	for i, line := range lines {
		result[i] = highlightLine(line, keywords)
	}
	return result
}

// highlightLine scans a single line for keywords, strings, numbers, and
// comments, producing a non-overlapping region list.
func highlightLine(line string, keywords map[string]bool) []HighlightRegion {
	runes := []rune(line)
	length := len(runes)
	var regions []HighlightRegion
	i := 0
	for i < length {
		// Line comment: // or #
		if region, ok := tryLineComment(runes, i, length); ok {
			regions = append(regions, region)
			break // comment extends to end of line
		}
		// String literal: " or ' or `
		if region, advance := tryString(runes, i, length); advance > 0 {
			regions = append(regions, region)
			i += advance
			continue
		}
		// Number literal
		if region, advance := tryNumber(runes, i, length); advance > 0 {
			regions = append(regions, region)
			i += advance
			continue
		}
		// Identifier / keyword
		if region, advance := tryIdentifier(runes, i, length, keywords); advance > 0 {
			regions = append(regions, region)
			i += advance
			continue
		}
		i++
	}
	return regions
}

// ---------------------------------------------------------------------------
// Token scanners
// ---------------------------------------------------------------------------

func tryLineComment(runes []rune, i, length int) (HighlightRegion, bool) {
	if i+1 < length && runes[i] == '/' && runes[i+1] == '/' {
		return HighlightRegion{StartCol: i, EndCol: length, Category: theme.CatComment}, true
	}
	if runes[i] == '#' {
		return HighlightRegion{StartCol: i, EndCol: length, Category: theme.CatComment}, true
	}
	return HighlightRegion{}, false
}

func tryString(runes []rune, i, length int) (HighlightRegion, int) {
	quote := runes[i]
	if quote != '"' && quote != '\'' && quote != '`' {
		return HighlightRegion{}, 0
	}
	j := i + 1
	for j < length {
		if runes[j] == '\\' && j+1 < length {
			j += 2 // skip escaped character
			continue
		}
		if runes[j] == quote {
			j++
			return HighlightRegion{StartCol: i, EndCol: j, Category: theme.CatString}, j - i
		}
		j++
	}
	// Unterminated string: highlight to end of line.
	return HighlightRegion{StartCol: i, EndCol: length, Category: theme.CatString}, length - i
}

func tryNumber(runes []rune, i, length int) (HighlightRegion, int) {
	if !unicode.IsDigit(runes[i]) {
		return HighlightRegion{}, 0
	}
	// Do not match digits that are part of an identifier.
	if i > 0 && (unicode.IsLetter(runes[i-1]) || runes[i-1] == '_') {
		return HighlightRegion{}, 0
	}
	j := i + 1
	for j < length && isNumberContinuation(runes[j]) {
		j++
	}
	return HighlightRegion{StartCol: i, EndCol: j, Category: theme.CatNumber}, j - i
}

func tryIdentifier(runes []rune, i, length int, keywords map[string]bool) (HighlightRegion, int) {
	if !unicode.IsLetter(runes[i]) && runes[i] != '_' {
		return HighlightRegion{}, 0
	}
	j := i + 1
	for j < length && isIdentChar(runes[j]) {
		j++
	}
	word := string(runes[i:j])
	if keywords[word] {
		return HighlightRegion{StartCol: i, EndCol: j, Category: theme.CatKeyword}, j - i
	}
	if isConstantLike(word) {
		return HighlightRegion{StartCol: i, EndCol: j, Category: theme.CatConstant}, j - i
	}
	// Not a highlighted identifier.
	return HighlightRegion{}, j - i
}

// ---------------------------------------------------------------------------
// Character classifiers
// ---------------------------------------------------------------------------

func isIdentChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isNumberContinuation(r rune) bool {
	return unicode.IsDigit(r) || r == '.' || r == 'x' || r == 'X' ||
		r == 'o' || r == 'O' || r == 'b' || r == 'B' ||
		(r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '_'
}

// isConstantLike detects well-known constant identifiers.
var constantIdentifiers = map[string]bool{
	"true": true, "false": true, "nil": true, "null": true,
	"None": true, "True": true, "False": true,
	"undefined": true, "iota": true,
}

func isConstantLike(word string) bool {
	return constantIdentifiers[word]
}

// ---------------------------------------------------------------------------
// Language keyword tables
// ---------------------------------------------------------------------------

// languageKeywords returns the keyword set for a language identifier.
func languageKeywords(language string) map[string]bool {
	if kw, ok := keywordTables[strings.ToLower(language)]; ok {
		return kw
	}
	return genericKeywords
}

var keywordTables = map[string]map[string]bool{
	"go":         goKeywords,
	"python":     pythonKeywords,
	"javascript": jsKeywords,
	"typescript":  tsKeywords,
	"rust":       rustKeywords,
}

var goKeywords = toSet([]string{
	"break", "case", "chan", "const", "continue", "default", "defer",
	"else", "fallthrough", "for", "func", "go", "goto", "if",
	"import", "interface", "map", "package", "range", "return",
	"select", "struct", "switch", "type", "var",
})

var pythonKeywords = toSet([]string{
	"and", "as", "assert", "async", "await", "break", "class",
	"continue", "def", "del", "elif", "else", "except", "finally",
	"for", "from", "global", "if", "import", "in", "is", "lambda",
	"nonlocal", "not", "or", "pass", "raise", "return", "try",
	"while", "with", "yield",
})

var jsKeywords = toSet([]string{
	"async", "await", "break", "case", "catch", "class", "const",
	"continue", "debugger", "default", "delete", "do", "else",
	"export", "extends", "finally", "for", "function", "if",
	"import", "in", "instanceof", "let", "new", "of", "return",
	"super", "switch", "this", "throw", "try", "typeof", "var",
	"void", "while", "with", "yield",
})

var tsKeywords = toSet([]string{
	"abstract", "any", "as", "async", "await", "boolean", "break",
	"case", "catch", "class", "const", "continue", "debugger",
	"declare", "default", "delete", "do", "else", "enum", "export",
	"extends", "finally", "for", "from", "function", "get", "if",
	"implements", "import", "in", "instanceof", "interface", "is",
	"keyof", "let", "module", "namespace", "new", "never", "number",
	"of", "private", "protected", "public", "readonly", "return",
	"set", "static", "string", "super", "switch", "symbol", "this",
	"throw", "try", "type", "typeof", "undefined", "unknown", "var",
	"void", "while", "with", "yield",
})

var rustKeywords = toSet([]string{
	"as", "async", "await", "break", "const", "continue", "crate",
	"dyn", "else", "enum", "extern", "fn", "for", "if", "impl",
	"in", "let", "loop", "match", "mod", "move", "mut", "pub",
	"ref", "return", "self", "static", "struct", "super", "trait",
	"type", "unsafe", "use", "where", "while",
})

var genericKeywords = toSet([]string{
	"if", "else", "for", "while", "return", "break", "continue",
	"switch", "case", "default", "class", "function", "import",
	"export", "const", "let", "var", "type", "struct", "enum",
})

// toSet converts a string slice to a lookup map.
func toSet(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
