package treesitter

import (
	"context"
	"time"
)

type TreeSitterTool struct {
	manager *TreeSitterManager
	loader  *GrammarLoader
}

type ParseResult struct {
	Language    string         `json:"language"`
	FilePath    string         `json:"file_path"`
	RootNode    *NodeInfo      `json:"root_node"`
	Functions   []FunctionInfo `json:"functions,omitempty"`
	Types       []TypeInfo     `json:"types,omitempty"`
	Imports     []ImportInfo   `json:"imports,omitempty"`
	Errors      []ParseError   `json:"errors,omitempty"`
	ParseTimeMs int64          `json:"parse_time_ms"`
}

type NodeInfo struct {
	Type      string     `json:"type"`
	StartLine uint32     `json:"start_line"`
	EndLine   uint32     `json:"end_line"`
	StartCol  uint32     `json:"start_col"`
	EndCol    uint32     `json:"end_col"`
	Content   string     `json:"content,omitempty"`
	Children  []NodeInfo `json:"children,omitempty"`
}

type FunctionInfo struct {
	Name       string `json:"name"`
	StartLine  uint32 `json:"start_line"`
	EndLine    uint32 `json:"end_line"`
	Parameters string `json:"parameters"`
	ReturnType string `json:"return_type,omitempty"`
	IsMethod   bool   `json:"is_method"`
	Receiver   string `json:"receiver,omitempty"`
}

type TypeInfo struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	StartLine uint32   `json:"start_line"`
	EndLine   uint32   `json:"end_line"`
	Fields    []string `json:"fields,omitempty"`
}

type ImportInfo struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
	Line  uint32 `json:"line"`
}

type ParseError struct {
	Line    uint32 `json:"line"`
	Column  uint32 `json:"column"`
	Message string `json:"message"`
}

type ToolQueryMatch struct {
	PatternIndex uint          `json:"pattern_index"`
	Captures     []ToolCapture `json:"captures"`
}

type ToolCapture struct {
	Name    string    `json:"name"`
	Node    *NodeInfo `json:"node"`
	Content string    `json:"content"`
}

func NewTreeSitterTool(opts ...LoaderOption) *TreeSitterTool {
	loader := NewGrammarLoader(opts...)
	manager := NewTreeSitterManager(loader, 100, 50)
	return &TreeSitterTool{
		manager: manager,
		loader:  loader,
	}
}

func (t *TreeSitterTool) Parse(ctx context.Context, filePath string, content []byte) (*ParseResult, error) {
	start := time.Now()

	langName := detectLanguage(filePath)
	if langName == "" {
		return nil, ErrGrammarNotFound
	}

	tree, err := t.parseContent(ctx, langName, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	return t.buildParseResult(tree, filePath, langName, start), nil
}

func (t *TreeSitterTool) parseContent(ctx context.Context, langName string, content []byte) (*Tree, error) {
	lang, err := t.loader.LoadContext(ctx, langName)
	if err != nil {
		return nil, err
	}

	parser := NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(lang); err != nil {
		return nil, err
	}

	return parser.Parse(content, nil)
}

func (t *TreeSitterTool) buildParseResult(tree *Tree, filePath, langName string, start time.Time) *ParseResult {
	result := &ParseResult{
		Language:    langName,
		FilePath:    filePath,
		ParseTimeMs: time.Since(start).Milliseconds(),
	}

	root := tree.RootNode()
	result.RootNode = nodeToInfo(root, 0)
	result.Functions = extractFunctions(root, langName)
	result.Types = extractTypes(root, langName)
	result.Imports = extractImports(root, langName)
	result.Errors = collectParseErrors(root)

	return result
}

func nodeToInfo(node *Node, maxDepth int) *NodeInfo {
	if node == nil || node.IsNull() {
		return nil
	}

	info := &NodeInfo{
		Type:      node.Type(),
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		StartCol:  node.StartPosition().Column,
		EndCol:    node.EndPosition().Column,
	}

	if maxDepth > 0 {
		info.Children = collectChildNodes(node, maxDepth-1)
	}

	return info
}

func collectChildNodes(node *Node, maxDepth int) []NodeInfo {
	count := node.NamedChildCount()
	if count == 0 {
		return nil
	}

	children := make([]NodeInfo, 0, count)
	for i := uint(0); i < count; i++ {
		child := node.NamedChild(i)
		if childInfo := nodeToInfo(child, maxDepth); childInfo != nil {
			children = append(children, *childInfo)
		}
	}
	return children
}

func extractFunctions(root *Node, langName string) []FunctionInfo {
	switch langName {
	case "go":
		return extractGoFunctions(root)
	default:
		return nil
	}
}

func extractGoFunctions(root *Node) []FunctionInfo {
	var functions []FunctionInfo
	walkNamedChildren(root, func(node *Node) {
		if isFunctionDeclaration(node) {
			functions = append(functions, parseFunctionInfo(node))
		}
	})
	return functions
}

func isFunctionDeclaration(node *Node) bool {
	t := node.Type()
	return t == "function_declaration" || t == "method_declaration"
}

func parseFunctionInfo(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  node.Type() == "method_declaration",
	}

	info.Name = getFieldContent(node, "name")
	info.Parameters = getFieldContent(node, "parameters")

	if info.IsMethod {
		info.Receiver = getFieldContent(node, "receiver")
	}

	return info
}

func getFieldContent(node *Node, fieldName string) string {
	if fieldNode := node.ChildByFieldName(fieldName); fieldNode != nil {
		return fieldNode.Content()
	}
	return ""
}

func extractTypes(root *Node, langName string) []TypeInfo {
	switch langName {
	case "go":
		return extractGoTypes(root)
	default:
		return nil
	}
}

func extractGoTypes(root *Node) []TypeInfo {
	var types []TypeInfo
	walkNamedChildren(root, func(node *Node) {
		if node.Type() == "type_declaration" {
			types = append(types, parseTypeDecl(node)...)
		}
	})
	return types
}

func parseTypeDecl(node *Node) []TypeInfo {
	var types []TypeInfo
	walkNamedChildren(node, func(child *Node) {
		if child.Type() == "type_spec" {
			types = append(types, parseTypeSpec(child))
		}
	})
	return types
}

func parseTypeSpec(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}

	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		info.Name = nameNode.Content()
	}

	if typeNode := node.ChildByFieldName("type"); typeNode != nil {
		info.Kind = typeNode.Type()
	}

	return info
}

func extractImports(root *Node, langName string) []ImportInfo {
	switch langName {
	case "go":
		return extractGoImports(root)
	default:
		return nil
	}
}

func extractGoImports(root *Node) []ImportInfo {
	var imports []ImportInfo
	walkNamedChildren(root, func(node *Node) {
		if node.Type() == "import_declaration" {
			imports = append(imports, parseImportDecl(node)...)
		}
	})
	return imports
}

func parseImportDecl(node *Node) []ImportInfo {
	var imports []ImportInfo
	walkAllNamedChildren(node, func(child *Node) {
		if child.Type() == "import_spec" {
			imports = append(imports, parseImportSpec(child))
		}
	})
	return imports
}

func walkAllNamedChildren(node *Node, fn func(*Node)) {
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := node.NamedChild(i)
		fn(child)
		walkAllNamedChildren(child, fn)
	}
}

func parseImportSpec(node *Node) ImportInfo {
	info := ImportInfo{
		Line: node.StartPosition().Row + 1,
	}

	if pathNode := node.ChildByFieldName("path"); pathNode != nil {
		info.Path = pathNode.Content()
	}

	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		info.Alias = nameNode.Content()
	}

	return info
}

func collectParseErrors(root *Node) []ParseError {
	var errors []ParseError
	walkAllChildren(root, func(node *Node) {
		if node.HasError() && node.Type() == "ERROR" {
			errors = append(errors, ParseError{
				Line:    node.StartPosition().Row + 1,
				Column:  node.StartPosition().Column,
				Message: "syntax error",
			})
		}
	})
	return errors
}

func walkNamedChildren(node *Node, fn func(*Node)) {
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		fn(node.NamedChild(i))
	}
}

func walkAllChildren(node *Node, fn func(*Node)) {
	count := node.ChildCount()
	for i := uint(0); i < count; i++ {
		child := node.Child(i)
		fn(child)
		walkAllChildren(child, fn)
	}
}

func (t *TreeSitterTool) Query(ctx context.Context, filePath string, content []byte, queryStr string) ([]ToolQueryMatch, error) {
	langName := detectLanguage(filePath)
	if langName == "" {
		return nil, ErrGrammarNotFound
	}

	tree, err := t.parseContent(ctx, langName, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	return t.executeQuery(ctx, tree, langName, queryStr)
}

func (t *TreeSitterTool) executeQuery(ctx context.Context, tree *Tree, langName, queryStr string) ([]ToolQueryMatch, error) {
	lang, err := t.loader.LoadContext(ctx, langName)
	if err != nil {
		return nil, err
	}

	query, err := NewQuery(lang, queryStr)
	if err != nil {
		return nil, err
	}
	defer query.Close()

	cursor := NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), tree.Source())
	return convertMatches(matches), nil
}

func convertMatches(matches []QueryMatch) []ToolQueryMatch {
	result := make([]ToolQueryMatch, len(matches))
	for i, m := range matches {
		result[i] = ToolQueryMatch{
			PatternIndex: m.PatternIndex,
			Captures:     convertCaptures(m.Captures),
		}
	}
	return result
}

func convertCaptures(captures []QueryCapture) []ToolCapture {
	result := make([]ToolCapture, len(captures))
	for i, c := range captures {
		result[i] = ToolCapture{
			Name:    c.Name,
			Node:    nodeToInfo(c.Node, 0),
			Content: c.Node.Content(),
		}
	}
	return result
}

func (t *TreeSitterTool) DetectLanguage(filePath string) (string, bool) {
	lang := detectLanguage(filePath)
	return lang, lang != ""
}

func (t *TreeSitterTool) ListAvailableGrammars() []string {
	var names []string
	for ext := range extToLang {
		names = append(names, extToLang[ext])
	}
	return deduplicateStrings(names)
}

func deduplicateStrings(strs []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func (t *TreeSitterTool) Close() {
	t.manager.Close()
}
