// Package highlight provides syntax highlighting for the editor.
// When a tree-sitter grammar is available for the file's language, full
// AST-based highlighting is used. Otherwise, a keyword-based fallback
// provides basic highlighting.
package highlight

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/ui/theme"
)

// HighlightRegion marks a styled span within a single line.
type HighlightRegion struct {
	StartCol int
	EndCol   int // exclusive
	Category theme.SyntaxCategory
}

// Highlighter performs syntax highlighting using tree-sitter grammars with
// a keyword-based fallback for languages without available grammars.
type Highlighter struct {
	theme      *theme.Theme
	parser     *treesitter.Parser
	tree       *treesitter.Tree
	language   string
	grammarOK  bool
	grammarErr bool

	cachedContent string
	cachedRegions [][]HighlightRegion
	cacheValid    bool
}

// NewHighlighter creates a Highlighter with the given theme.
func NewHighlighter(th *theme.Theme) *Highlighter {
	return &Highlighter{theme: th}
}

// Close releases tree-sitter resources held by the highlighter.
func (h *Highlighter) Close() {
	if h.tree != nil {
		h.tree.Close()
		h.tree = nil
	}
	if h.parser != nil {
		h.parser.Close()
		h.parser = nil
	}
	h.clearCache()
}

// Tree returns the current parse tree, or nil if no grammar is loaded or
// no content has been parsed yet.
func (h *Highlighter) Tree() *treesitter.Tree { return h.tree }

// HasParseErrors reports whether the current parse tree contains any ERROR
// nodes. Returns false if no tree is available.
func (h *Highlighter) HasParseErrors() bool {
	return h.tree != nil && h.tree.RootNode().HasError()
}

// Highlight returns highlight regions indexed by line number. When a
// tree-sitter grammar is available for the language, full AST-based
// highlighting is used. Otherwise, keyword-based highlighting is applied.
func (h *Highlighter) Highlight(content string, language string) [][]HighlightRegion {
	if language != h.language {
		h.setLanguage(language)
	}
	if h.cacheValid && content == h.cachedContent {
		return h.cachedRegions
	}

	var regions [][]HighlightRegion
	if h.grammarOK {
		if tsRegions := h.highlightTreeSitter(content); tsRegions != nil {
			regions = tsRegions
		}
	}
	if regions == nil {
		regions = highlightKeyword(content, language)
	}
	h.storeCache(content, regions)
	return regions
}

// ---------------------------------------------------------------------------
// Tree-sitter path
// ---------------------------------------------------------------------------

func (h *Highlighter) setLanguage(language string) {
	h.language = language
	h.grammarOK = false
	h.grammarErr = false
	h.clearCache()
	if h.tree != nil {
		h.tree.Close()
		h.tree = nil
	}
	if language == "" {
		return
	}
	if !treesitter.IsGrammarAvailable(language) {
		h.grammarErr = true
		return
	}
	if h.parser == nil {
		h.parser = treesitter.NewParser()
	}
	if err := h.parser.SetLanguageByName(language); err != nil {
		h.grammarErr = true
		return
	}
	h.grammarOK = true
}

func (h *Highlighter) clearCache() {
	h.cachedContent = ""
	h.cachedRegions = nil
	h.cacheValid = false
}

func (h *Highlighter) storeCache(content string, regions [][]HighlightRegion) {
	h.cachedContent = content
	h.cachedRegions = regions
	h.cacheValid = true
}

func (h *Highlighter) highlightTreeSitter(content string) [][]HighlightRegion {
	source := []byte(content)
	// Full reparse: we don't track byte-level edits, so passing the old tree
	// without ts_tree_edit info would produce misaligned regions.
	tree, err := h.parser.Parse(source, nil)
	if err != nil {
		return nil
	}
	if h.tree != nil {
		h.tree.Close()
	}
	h.tree = tree
	return walkTreeRegions(tree, source)
}

// walkTreeRegions performs a depth-first walk of the parse tree, collecting
// highlight regions. Non-leaf span nodes (strings, comments) are highlighted
// as a single region covering the entire node; their children are skipped.
func walkTreeRegions(tree *treesitter.Tree, source []byte) [][]HighlightRegion {
	root := tree.RootNode()
	imports := collectGoImports(root)
	cursor := root.Walk()
	defer cursor.Close()

	sourceLines := bytes.Split(source, []byte("\n"))
	regions := make([][]HighlightRegion, len(sourceLines))

	reachedRoot := false
	for !reachedRoot {
		node := cursor.Node()
		skipChildren := false

		if node.ChildCount() > 0 {
			if cat := spanCategory(node); cat != "" {
				addNodeRegions(regions, node, cat, sourceLines)
				skipChildren = true
			}
		}

		if !skipChildren && node.ChildCount() == 0 {
			cat := categorizeNode(node, cursor.FieldName(), imports)
			if cat != "" {
				addNodeRegions(regions, node, cat, sourceLines)
			}
		}

		if !skipChildren && cursor.GotoFirstChild() {
			continue
		}
		if cursor.GotoNextSibling() {
			continue
		}
		for {
			if !cursor.GotoParent() {
				reachedRoot = true
				break
			}
			if cursor.GotoNextSibling() {
				break
			}
		}
	}
	return regions
}

// spanCategory returns the syntax category for non-leaf nodes that should be
// highlighted as a single span covering all children (e.g. string literals
// and comments whose children are delimiters and content fragments).
func spanCategory(node *treesitter.Node) theme.SyntaxCategory {
	cat, ok := theme.NodeTypeToCategory[node.Type()]
	if !ok {
		return ""
	}
	if cat == theme.CatString || cat == theme.CatComment {
		return cat
	}
	return ""
}

// categorizeNode determines the syntax category for a leaf node using
// the NodeTypeToCategory map and context-aware overrides for identifiers
// and field/property access nodes.
func categorizeNode(node *treesitter.Node, fieldName string, imports map[string]bool) theme.SyntaxCategory {
	nodeType := node.Type()
	cat, ok := theme.NodeTypeToCategory[nodeType]
	if !ok {
		return ""
	}
	switch nodeType {
	case "identifier":
		return identifierCategory(node, fieldName, imports)
	case "field_identifier", "property_identifier":
		return fieldIdentifierCategory(node)
	}
	return cat
}

// identifierCategory refines the category for identifier nodes based on
// their field name within the parent and the parent node type.
func identifierCategory(node *treesitter.Node, fieldName string, imports map[string]bool) theme.SyntaxCategory {
	switch fieldName {
	case "function":
		return theme.CatFunction
	case "type":
		return theme.CatType
	case "name":
		return identifierNameCategory(node)
	case "operand":
		return identifierOperandCategory(node, imports)
	}
	return theme.CatVariable
}

// identifierNameCategory handles identifiers with field name "name".
func identifierNameCategory(node *treesitter.Node) theme.SyntaxCategory {
	parent := node.Parent()
	if parent == nil {
		return theme.CatVariable
	}
	switch parent.Type() {
	case "function_declaration", "method_declaration",
		"function_definition", "function_item":
		return theme.CatFunction
	case "type_spec", "type_alias_declaration":
		return theme.CatType
	case "package_clause":
		return theme.CatNamespace
	}
	return theme.CatVariable
}

// identifierOperandCategory handles identifiers with field name "operand"
// (the left side of selector_expression, e.g. "fmt" in "fmt.Println()" or
// "time" in "time.Hour"). The imports set, collected from the file's import
// declarations, distinguishes package qualifiers from struct variables.
func identifierOperandCategory(node *treesitter.Node, imports map[string]bool) theme.SyntaxCategory {
	parent := node.Parent()
	if parent == nil || parent.Type() != "selector_expression" {
		return theme.CatVariable
	}
	if len(imports) > 0 && imports[node.Content()] {
		return theme.CatNamespace
	}
	return theme.CatVariable
}

// fieldIdentifierCategory refines field_identifier and property_identifier
// nodes. When the field is part of a method/function call (selector inside
// call_expression), it is categorized as CatFunction. Otherwise CatProperty.
func fieldIdentifierCategory(node *treesitter.Node) theme.SyntaxCategory {
	parent := node.Parent()
	if parent == nil {
		return theme.CatProperty
	}
	switch parent.Type() {
	case "selector_expression":
		grandparent := parent.Parent()
		if grandparent != nil && grandparent.Type() == "call_expression" {
			return theme.CatFunction
		}
	case "method_declaration":
		return theme.CatFunction
	}
	return theme.CatProperty
}

// ---------------------------------------------------------------------------
// Import collection (Go-specific)
// ---------------------------------------------------------------------------

// collectGoImports scans top-level import declarations and returns the set of
// local package names (aliases or last path segment). For non-Go files the
// result is empty, which causes all selector operands to fall back to
// CatVariable.
func collectGoImports(root *treesitter.Node) map[string]bool {
	imports := make(map[string]bool)
	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type() == "import_declaration" {
			extractImportNames(child, imports)
		}
	}
	return imports
}

func extractImportNames(node *treesitter.Node, imports map[string]bool) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "import_spec":
			addImportSpec(child, imports)
		case "import_spec_list":
			extractImportNames(child, imports)
		}
	}
}

func addImportSpec(spec *treesitter.Node, imports map[string]bool) {
	nameNode := spec.ChildByFieldName("name")
	if nameNode != nil {
		name := nameNode.Content()
		if name != "." && name != "_" {
			imports[name] = true
		}
		return
	}
	pathNode := spec.ChildByFieldName("path")
	if pathNode == nil {
		return
	}
	path := pathNode.Content()
	path = strings.Trim(path, "\"")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	if path != "" {
		imports[path] = true
	}
}

// addNodeRegions converts a tree-sitter node's byte-based position into
// rune-based HighlightRegions and appends them to the appropriate lines.
func addNodeRegions(regions [][]HighlightRegion, node *treesitter.Node, cat theme.SyntaxCategory, sourceLines [][]byte) {
	start := node.StartPosition()
	end := node.EndPosition()
	startRow := int(start.Row)
	endRow := int(end.Row)

	if startRow == endRow {
		if startRow >= len(regions) {
			return
		}
		line := sourceLines[startRow]
		sc := byteColToRuneCol(line, start.Column)
		ec := byteColToRuneCol(line, end.Column)
		if sc < ec {
			regions[startRow] = append(regions[startRow], HighlightRegion{
				StartCol: sc, EndCol: ec, Category: cat,
			})
		}
		return
	}

	for row := startRow; row <= endRow && row < len(regions); row++ {
		line := sourceLines[row]
		lineRunes := utf8.RuneCount(line)
		var sc, ec int
		switch {
		case row == startRow:
			sc = byteColToRuneCol(line, start.Column)
			ec = lineRunes
		case row == endRow:
			sc = 0
			ec = byteColToRuneCol(line, end.Column)
		default:
			sc = 0
			ec = lineRunes
		}
		if sc < ec {
			regions[row] = append(regions[row], HighlightRegion{
				StartCol: sc, EndCol: ec, Category: cat,
			})
		}
	}
}

// byteColToRuneCol converts a byte-based column offset to a rune-based
// column for a given line of source bytes.
func byteColToRuneCol(line []byte, byteCol uint32) int {
	col := 0
	i := 0
	for i < len(line) && i < int(byteCol) {
		_, size := utf8.DecodeRune(line[i:])
		i += size
		col++
	}
	return col
}

// ---------------------------------------------------------------------------
// Keyword-based fallback
// ---------------------------------------------------------------------------

func highlightKeyword(content, language string) [][]HighlightRegion {
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
		if region, ok := tryLineComment(runes, i, length); ok {
			regions = append(regions, region)
			break
		}
		if region, advance := tryString(runes, i, length); advance > 0 {
			regions = append(regions, region)
			i += advance
			continue
		}
		if region, advance := tryNumber(runes, i, length); advance > 0 {
			regions = append(regions, region)
			i += advance
			continue
		}
		if region, advance := tryIdentifier(runes, i, length, keywords); advance > 0 {
			if region.EndCol > region.StartCol {
				regions = append(regions, region)
			}
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
			j += 2
			continue
		}
		if runes[j] == quote {
			j++
			return HighlightRegion{StartCol: i, EndCol: j, Category: theme.CatString}, j - i
		}
		j++
	}
	return HighlightRegion{StartCol: i, EndCol: length, Category: theme.CatString}, length - i
}

func tryNumber(runes []rune, i, length int) (HighlightRegion, int) {
	if !unicode.IsDigit(runes[i]) {
		return HighlightRegion{}, 0
	}
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
	"typescript": tsKeywords,
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

func toSet(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
