package treesitter

import (
	"context"
	"fmt"
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

func (t *TreeSitterTool) ParseFast(ctx context.Context, filePath string, content []byte) (*ParseResult, error) {
	langName := detectLanguage(filePath)
	if langName == "" {
		return nil, ErrGrammarNotFound
	}

	tree, err := t.parseContent(ctx, langName, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	return t.buildParseResultFast(tree, filePath, langName), nil
}

func (t *TreeSitterTool) ParseWithParser(ctx context.Context, parser *Parser, filePath string, content []byte, currentLang *string) (*ParseResult, error) {
	langName := detectLanguage(filePath)
	if langName == "" {
		return nil, ErrGrammarNotFound
	}

	if *currentLang != langName {
		lang, err := t.loader.LoadContext(ctx, langName)
		if err != nil {
			return nil, err
		}
		if err := parser.SetLanguage(lang); err != nil {
			return nil, err
		}
		*currentLang = langName
	}

	tree, err := parser.Parse(content, nil)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	return t.buildParseResultFast(tree, filePath, langName), nil
}

func (t *TreeSitterTool) buildParseResultFast(tree *Tree, filePath, langName string) *ParseResult {
	result := &ParseResult{
		Language: langName,
		FilePath: filePath,
	}

	root := tree.RootNode()

	switch langName {
	case "go":
		result.Functions, result.Types, result.Imports = extractGoSymbolsSinglePass(root)
	case "python":
		result.Functions, result.Types, result.Imports = extractPythonSymbolsSinglePass(root)
	case "javascript", "typescript", "tsx":
		result.Functions, result.Types, result.Imports = extractJSSymbolsSinglePass(root)
	case "java":
		result.Functions, result.Types, result.Imports = extractJavaSymbolsSinglePass(root)
	case "rust":
		result.Functions, result.Types, result.Imports = extractRustSymbolsSinglePass(root)
	case "c", "cpp":
		result.Functions, result.Types, result.Imports = extractCSymbolsSinglePass(root)
	case "ruby":
		result.Functions, result.Types, result.Imports = extractRubySymbolsSinglePass(root)
	case "php":
		result.Functions, result.Types, result.Imports = extractPHPSymbolsSinglePass(root)
	case "elixir":
		result.Functions, result.Types, result.Imports = extractElixirSymbolsSinglePass(root)
	case "scala":
		result.Functions, result.Types, result.Imports = extractScalaSymbolsSinglePass(root)
	case "markdown":
		result.Types = extractMarkdownSections(root)
	case "json":
		result.Types = extractJSONKeys(root)
	case "yaml":
		result.Types = extractYAMLKeys(root)
	case "hcl":
		result.Types = extractHCLBlocks(root)
	case "lua":
		result.Functions, result.Types, result.Imports = extractLuaSymbolsSinglePass(root)
	case "toml":
		result.Types = extractTOMLKeys(root)
	case "scss", "css":
		result.Types = extractCSSSelectors(root)
	case "vue", "svelte":
		result.Functions, result.Types, result.Imports = extractJSSymbolsSinglePass(root)
	case "zig":
		result.Functions, result.Types, result.Imports = extractZigSymbolsSinglePass(root)
	case "cuda":
		result.Functions, result.Types, result.Imports = extractCSymbolsSinglePass(root)
	case "kotlin":
		result.Functions, result.Types, result.Imports = extractKotlinSymbolsSinglePass(root)
	case "make":
		result.Types = extractMakeTargets(root)
	case "bash", "zsh", "sh":
		result.Functions, result.Types, result.Imports = extractBashSymbolsSinglePass(root)
	case "gitattributes", "gitignore":
		result.Types = extractGitPatterns(root)
	case "haskell":
		result.Functions, result.Types, result.Imports = extractHaskellSymbolsSinglePass(root)
	case "diff":
		result.Types = extractDiffFiles(root)
	}

	result.Errors = collectParseErrors(root)

	return result
}

func extractGoSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	count := root.NamedChildCount()
	for i := range count {
		node := root.NamedChild(uint(i))
		switch node.Type() {
		case "function_declaration", "method_declaration":
			functions = append(functions, parseFunctionInfo(node))
		case "type_declaration":
			types = append(types, parseTypeDecl(node)...)
		case "const_declaration":
			types = append(types, parseGoConstDecl(node)...)
		case "var_declaration":
			types = append(types, parseGoVarDecl(node)...)
		case "import_declaration":
			imports = append(imports, parseImportDecl(node)...)
		}
	}

	return functions, types, imports
}

func parseGoConstDecl(node *Node) []TypeInfo {
	var types []TypeInfo
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "const_spec" {
			if nameNode := child.ChildByFieldName("name"); nameNode != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(nameNode),
					Kind:      "const",
					StartLine: child.StartPosition().Row + 1,
					EndLine:   child.EndPosition().Row + 1,
				})
			}
		}
	}
	return types
}

func parseGoVarDecl(node *Node) []TypeInfo {
	var types []TypeInfo
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "var_spec" {
			if nameNode := child.ChildByFieldName("name"); nameNode != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(nameNode),
					Kind:      "var",
					StartLine: child.StartPosition().Row + 1,
					EndLine:   child.EndPosition().Row + 1,
				})
			}
		}
	}
	return types
}

func extractPythonSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function_definition":
			functions = append(functions, parsePythonFunction(node, false))
		case "class_definition":
			types = append(types, parsePythonClass(node))
			extractPythonMethods(node, &functions)
		case "import_statement":
			imports = append(imports, parsePythonImport(node)...)
		case "import_from_statement", "future_import_statement":
			imports = append(imports, parsePythonFromImport(node)...)
		case "decorated_definition":
			if child := node.ChildByFieldName("definition"); child != nil {
				walk(child)
			}
			return
		case "assignment":
			types = append(types, parsePythonAssignments(node)...)
		case "type_alias_statement":
			if t := parsePythonTypeAlias(node); t.Name != "" {
				types = append(types, t)
			}
		case "global_statement", "nonlocal_statement":
			types = append(types, parsePythonGlobalNonlocal(node)...)
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parsePythonAssignments(node *Node) []TypeInfo {
	var types []TypeInfo
	if left := node.ChildByFieldName("left"); left != nil {
		switch left.Type() {
		case "identifier":
			name := nodeText(left)
			kind := "variable"
			if isAllCaps(name) {
				kind = "const"
			}
			types = append(types, TypeInfo{
				Name:      name,
				Kind:      kind,
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		case "pattern_list", "tuple_pattern":
			for i := uint(0); i < left.NamedChildCount(); i++ {
				child := left.NamedChild(i)
				if child.Type() == "identifier" {
					types = append(types, TypeInfo{
						Name:      nodeText(child),
						Kind:      "variable",
						StartLine: node.StartPosition().Row + 1,
						EndLine:   node.EndPosition().Row + 1,
					})
				}
			}
		}
	}
	return types
}

func isAllCaps(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return true
}

func parsePythonTypeAlias(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "type",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		info.Name = nodeText(nameNode)
	}
	return info
}

func parsePythonGlobalNonlocal(node *Node) []TypeInfo {
	var types []TypeInfo
	kind := "global"
	if node.Type() == "nonlocal_statement" {
		kind = "nonlocal"
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			types = append(types, TypeInfo{
				Name:      nodeText(child),
				Kind:      kind,
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}
	}
	return types
}

func parsePythonFunction(node *Node, isMethod bool) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  isMethod,
	}

	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		info.Name = nodeText(nameNode)
	}

	if params := node.ChildByFieldName("parameters"); params != nil {
		info.Parameters = nodeText(params)
	}

	if retType := node.ChildByFieldName("return_type"); retType != nil {
		info.ReturnType = nodeText(retType)
	}

	return info
}

func parsePythonClass(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "class",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}

	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		info.Name = nodeText(nameNode)
	}

	return info
}

func extractPythonMethods(classNode *Node, functions *[]FunctionInfo) {
	body := classNode.ChildByFieldName("body")
	if body == nil {
		return
	}

	className := ""
	if nameNode := classNode.ChildByFieldName("name"); nameNode != nil {
		className = nodeText(nameNode)
	}

	count := body.NamedChildCount()
	for i := range count {
		child := body.NamedChild(uint(i))
		switch child.Type() {
		case "function_definition":
			fn := parsePythonFunction(child, true)
			fn.Receiver = className
			*functions = append(*functions, fn)
		case "decorated_definition":
			if def := child.ChildByFieldName("definition"); def != nil && def.Type() == "function_definition" {
				fn := parsePythonFunction(def, true)
				fn.Receiver = className
				*functions = append(*functions, fn)
			}
		}
	}
}

func parsePythonImport(node *Node) []ImportInfo {
	var imports []ImportInfo

	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		switch child.Type() {
		case "dotted_name":
			imports = append(imports, ImportInfo{
				Path: nodeText(child),
				Line: node.StartPosition().Row + 1,
			})
		case "aliased_import":
			imp := ImportInfo{Line: node.StartPosition().Row + 1}
			if name := child.ChildByFieldName("name"); name != nil {
				imp.Path = nodeText(name)
			}
			if alias := child.ChildByFieldName("alias"); alias != nil {
				imp.Alias = nodeText(alias)
			}
			imports = append(imports, imp)
		}
	}

	return imports
}

func parsePythonFromImport(node *Node) []ImportInfo {
	var imports []ImportInfo

	moduleName := ""
	if mod := node.ChildByFieldName("module_name"); mod != nil {
		moduleName = nodeText(mod)
	}

	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		switch child.Type() {
		case "dotted_name", "identifier":
			imports = append(imports, ImportInfo{
				Path: moduleName + "." + nodeText(child),
				Line: node.StartPosition().Row + 1,
			})
		case "aliased_import":
			imp := ImportInfo{Line: node.StartPosition().Row + 1}
			if name := child.ChildByFieldName("name"); name != nil {
				imp.Path = moduleName + "." + nodeText(name)
			}
			if alias := child.ChildByFieldName("alias"); alias != nil {
				imp.Alias = nodeText(alias)
			}
			imports = append(imports, imp)
		}
	}

	return imports
}

func nodeText(node *Node) string {
	if node == nil || node.IsNull() {
		return ""
	}
	return node.Content()
}

func extractJSSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, className string)
	walk = func(node *Node, className string) {
		switch node.Type() {
		case "function_declaration", "generator_function_declaration":
			functions = append(functions, parseJSFunction(node, false, ""))
		case "function_expression":
			if fn := parseJSFunction(node, false, ""); fn.Name != "" {
				functions = append(functions, fn)
			}
		case "class_declaration", "abstract_class_declaration":
			types = append(types, parseJSClass(node))
			if body := node.ChildByFieldName("body"); body != nil {
				name := ""
				if n := node.ChildByFieldName("name"); n != nil {
					name = nodeText(n)
				}
				walkJSClassBody(body, name, &functions, &types)
			}
			return
		case "lexical_declaration", "variable_declaration":
			extractJSVarDeclarations(node, &functions, &types)
		case "import_statement":
			imports = append(imports, parseJSImport(node)...)
		case "export_statement":
			if decl := node.ChildByFieldName("declaration"); decl != nil {
				walk(decl, className)
			}
			if spec := node.ChildByFieldName("source"); spec != nil {
				imports = append(imports, ImportInfo{
					Path: nodeText(spec),
					Line: node.StartPosition().Row + 1,
				})
			}
			return
		case "interface_declaration", "type_alias_declaration":
			types = append(types, parseJSTypeDecl(node))
		case "enum_declaration":
			types = append(types, parseJSEnum(node))
		case "ambient_declaration":
			extractJSAmbient(node, &functions, &types)
		case "internal_module":
			types = append(types, parseJSModule(node))
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), className)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parseJSFunction(node *Node, isMethod bool, receiver string) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  isMethod,
		Receiver:  receiver,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseJSClass(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "class",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func walkJSClassBody(body *Node, className string, functions *[]FunctionInfo, types *[]TypeInfo) {
	count := body.NamedChildCount()
	for i := range count {
		child := body.NamedChild(uint(i))
		switch child.Type() {
		case "method_definition":
			fn := parseJSFunction(child, true, className)
			if n := child.ChildByFieldName("name"); n != nil {
				fn.Name = nodeText(n)
			}
			*functions = append(*functions, fn)
		case "public_field_definition", "field_definition":
			if n := child.ChildByFieldName("name"); n != nil {
				*types = append(*types, TypeInfo{
					Name:      nodeText(n),
					Kind:      "field",
					StartLine: child.StartPosition().Row + 1,
					EndLine:   child.EndPosition().Row + 1,
				})
			}
		}
	}
}

func extractJSVarDeclarations(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	count := node.NamedChildCount()
	for i := range count {
		decl := node.NamedChild(uint(i))
		if decl.Type() == "variable_declarator" {
			name := ""
			if n := decl.ChildByFieldName("name"); n != nil {
				name = nodeText(n)
			}
			value := decl.ChildByFieldName("value")
			if value != nil && (value.Type() == "arrow_function" || value.Type() == "function" || value.Type() == "function_expression") {
				fn := FunctionInfo{
					Name:      name,
					StartLine: decl.StartPosition().Row + 1,
					EndLine:   decl.EndPosition().Row + 1,
				}
				if p := value.ChildByFieldName("parameters"); p != nil {
					fn.Parameters = nodeText(p)
				}
				*functions = append(*functions, fn)
			} else if name != "" {
				*types = append(*types, TypeInfo{
					Name:      name,
					Kind:      "variable",
					StartLine: decl.StartPosition().Row + 1,
					EndLine:   decl.EndPosition().Row + 1,
				})
			}
		}
	}
}

func parseJSEnum(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "enum",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseJSModule(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "module",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func extractJSAmbient(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		switch child.Type() {
		case "function_signature":
			fn := FunctionInfo{
				StartLine: child.StartPosition().Row + 1,
				EndLine:   child.EndPosition().Row + 1,
			}
			if n := child.ChildByFieldName("name"); n != nil {
				fn.Name = nodeText(n)
			}
			if p := child.ChildByFieldName("parameters"); p != nil {
				fn.Parameters = nodeText(p)
			}
			*functions = append(*functions, fn)
		case "ambient_declaration":
			extractJSAmbient(child, functions, types)
		case "class_declaration", "abstract_class_declaration":
			*types = append(*types, parseJSClass(child))
		case "interface_declaration", "type_alias_declaration":
			*types = append(*types, parseJSTypeDecl(child))
		case "enum_declaration":
			*types = append(*types, parseJSEnum(child))
		case "module":
			*types = append(*types, parseJSModule(child))
		case "lexical_declaration", "variable_declaration":
			extractJSVarDeclarations(child, functions, types)
		}
	}
}

func parseJSImport(node *Node) []ImportInfo {
	var imports []ImportInfo
	if src := node.ChildByFieldName("source"); src != nil {
		imports = append(imports, ImportInfo{
			Path: nodeText(src),
			Line: node.StartPosition().Row + 1,
		})
	}
	return imports
}

func parseJSTypeDecl(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if node.Type() == "interface_declaration" {
		info.Kind = "interface"
	} else {
		info.Kind = "type"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func extractJavaSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, className string)
	walk = func(node *Node, className string) {
		switch node.Type() {
		case "class_declaration", "interface_declaration", "enum_declaration",
			"annotation_type_declaration", "record_declaration":
			types = append(types, parseJavaType(node))
			name := ""
			if n := node.ChildByFieldName("name"); n != nil {
				name = nodeText(n)
			}
			if body := node.ChildByFieldName("body"); body != nil {
				walkJavaClassBody(body, name, &functions, &types)
			}
			return
		case "import_declaration":
			imports = append(imports, parseJavaImport(node))
		case "package_declaration":
			types = append(types, TypeInfo{
				Name:      parseJavaPackage(node),
				Kind:      "package",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), className)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parseJavaType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "interface_declaration":
		info.Kind = "interface"
	case "enum_declaration":
		info.Kind = "enum"
	case "annotation_type_declaration":
		info.Kind = "annotation"
	case "record_declaration":
		info.Kind = "record"
	default:
		info.Kind = "class"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseJavaPackage(node *Node) string {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Type() == "scoped_identifier" || child.Type() == "identifier" {
			return nodeText(child)
		}
	}
	return ""
}

func walkJavaClassBody(body *Node, className string, functions *[]FunctionInfo, types *[]TypeInfo) {
	count := body.NamedChildCount()
	for i := range count {
		child := body.NamedChild(uint(i))
		switch child.Type() {
		case "method_declaration", "constructor_declaration":
			fn := FunctionInfo{
				StartLine: child.StartPosition().Row + 1,
				EndLine:   child.EndPosition().Row + 1,
				IsMethod:  true,
				Receiver:  className,
			}
			if n := child.ChildByFieldName("name"); n != nil {
				fn.Name = nodeText(n)
			}
			if p := child.ChildByFieldName("parameters"); p != nil {
				fn.Parameters = nodeText(p)
			}
			*functions = append(*functions, fn)
		case "field_declaration", "constant_declaration":
			*types = append(*types, parseJavaFields(child)...)
		case "class_declaration", "interface_declaration", "enum_declaration",
			"annotation_type_declaration", "record_declaration":
			*types = append(*types, parseJavaType(child))
			if innerBody := child.ChildByFieldName("body"); innerBody != nil {
				innerName := ""
				if n := child.ChildByFieldName("name"); n != nil {
					innerName = nodeText(n)
				}
				walkJavaClassBody(innerBody, innerName, functions, types)
			}
		case "enum_constant":
			if n := child.ChildByFieldName("name"); n != nil {
				*types = append(*types, TypeInfo{
					Name:      nodeText(n),
					Kind:      "enum_constant",
					StartLine: child.StartPosition().Row + 1,
					EndLine:   child.EndPosition().Row + 1,
				})
			}
		}
	}
}

func parseJavaFields(node *Node) []TypeInfo {
	var types []TypeInfo
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "variable_declarator" {
			if n := child.ChildByFieldName("name"); n != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(n),
					Kind:      "field",
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		}
	}
	return types
}

func parseJavaImport(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "scoped_identifier" || child.Type() == "identifier" {
			info.Path = nodeText(child)
			break
		}
	}
	return info
}

func extractRustSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function_item", "function_signature_item":
			functions = append(functions, parseRustFunction(node, ""))
		case "struct_item", "enum_item", "trait_item", "type_item", "mod_item",
			"const_item", "static_item", "macro_definition":
			types = append(types, parseRustType(node))
		case "impl_item":
			typeName := ""
			if t := node.ChildByFieldName("type"); t != nil {
				typeName = nodeText(t)
			}
			if body := node.ChildByFieldName("body"); body != nil {
				walkRustImplBody(body, typeName, &functions)
			}
			return
		case "use_declaration", "extern_crate_declaration":
			imports = append(imports, parseRustUse(node)...)
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parseRustFunction(node *Node, receiver string) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  receiver != "",
		Receiver:  receiver,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseRustType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "struct_item":
		info.Kind = "struct"
	case "enum_item":
		info.Kind = "enum"
	case "trait_item":
		info.Kind = "trait"
	case "mod_item":
		info.Kind = "module"
	case "const_item":
		info.Kind = "const"
	case "static_item":
		info.Kind = "static"
	case "macro_definition":
		info.Kind = "macro"
	default:
		info.Kind = "type"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func walkRustImplBody(body *Node, typeName string, functions *[]FunctionInfo) {
	count := body.NamedChildCount()
	for i := range count {
		child := body.NamedChild(uint(i))
		if child.Type() == "function_item" {
			*functions = append(*functions, parseRustFunction(child, typeName))
		}
	}
}

func parseRustUse(node *Node) []ImportInfo {
	var imports []ImportInfo
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "use_tree" || child.Type() == "scoped_identifier" || child.Type() == "identifier" {
			imports = append(imports, ImportInfo{
				Path: nodeText(child),
				Line: node.StartPosition().Row + 1,
			})
			break
		}
	}
	return imports
}

func extractCSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function_definition":
			functions = append(functions, parseCFunction(node))
		case "declaration":
			parseCDeclaration(node, &functions, &types)
		case "struct_specifier", "union_specifier", "enum_specifier", "class_specifier":
			if t := parseCType(node); t.Name != "" {
				types = append(types, t)
				walkCClassBody(node, t.Name, &functions, &types)
			}
		case "type_definition", "alias_declaration":
			types = append(types, parseCTypedef(node))
		case "preproc_include":
			imports = append(imports, parseCInclude(node))
		case "preproc_def", "preproc_function_def":
			if t := parseCMacro(node); t.Name != "" {
				types = append(types, t)
			}
		case "namespace_definition":
			types = append(types, parseCNamespace(node))
		case "using_declaration":
			imports = append(imports, parseCUsing(node))
		case "template_declaration":
			parseCTemplate(node, &functions, &types)
		case "concept_definition":
			if t := parseCConcept(node); t.Name != "" {
				types = append(types, t)
			}
		case "friend_declaration":
			parseCFriend(node, &functions, &types)
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parseCFunction(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if decl := node.ChildByFieldName("declarator"); decl != nil {
		if name := decl.ChildByFieldName("declarator"); name != nil {
			info.Name = nodeText(name)
		}
		if params := decl.ChildByFieldName("parameters"); params != nil {
			info.Parameters = nodeText(params)
		}
	}
	return info
}

func tryCFunctionDecl(node *Node) *FunctionInfo {
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "function_declarator" {
			info := &FunctionInfo{
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			}
			if name := child.ChildByFieldName("declarator"); name != nil {
				info.Name = nodeText(name)
			}
			if params := child.ChildByFieldName("parameters"); params != nil {
				info.Parameters = nodeText(params)
			}
			return info
		}
	}
	return nil
}

func parseCType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "struct_specifier":
		info.Kind = "struct"
	case "union_specifier":
		info.Kind = "union"
	case "enum_specifier":
		info.Kind = "enum"
	case "class_specifier":
		info.Kind = "class"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseCTypedef(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "typedef",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "type_identifier" {
			info.Name = nodeText(child)
			break
		}
	}
	return info
}

func parseCInclude(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	if path := node.ChildByFieldName("path"); path != nil {
		info.Path = nodeText(path)
	}
	return info
}

func parseCDeclaration(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "function_declarator":
			info := FunctionInfo{
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			}
			if name := child.ChildByFieldName("declarator"); name != nil {
				info.Name = nodeText(name)
			}
			if params := child.ChildByFieldName("parameters"); params != nil {
				info.Parameters = nodeText(params)
			}
			*functions = append(*functions, info)
		case "init_declarator":
			if name := child.ChildByFieldName("declarator"); name != nil {
				*types = append(*types, TypeInfo{
					Name:      nodeText(name),
					Kind:      "variable",
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		case "identifier":
			*types = append(*types, TypeInfo{
				Name:      nodeText(child),
				Kind:      "variable",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}
	}
}

func walkCClassBody(node *Node, className string, functions *[]FunctionInfo, types *[]TypeInfo) {
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "function_definition":
			fn := parseCFunction(child)
			fn.IsMethod = true
			fn.Receiver = className
			*functions = append(*functions, fn)
		case "declaration":
			parseCClassMember(child, className, functions, types)
		case "field_declaration":
			*types = append(*types, parseCFieldDecl(child)...)
		case "access_specifier":
			continue
		}
	}
}

func parseCClassMember(node *Node, className string, functions *[]FunctionInfo, types *[]TypeInfo) {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Type() == "function_declarator" {
			fn := FunctionInfo{
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
				IsMethod:  true,
				Receiver:  className,
			}
			if name := child.ChildByFieldName("declarator"); name != nil {
				fn.Name = nodeText(name)
			}
			if params := child.ChildByFieldName("parameters"); params != nil {
				fn.Parameters = nodeText(params)
			}
			*functions = append(*functions, fn)
			return
		}
	}
}

func parseCFieldDecl(node *Node) []TypeInfo {
	var types []TypeInfo
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Type() == "field_identifier" {
			types = append(types, TypeInfo{
				Name:      nodeText(child),
				Kind:      "field",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}
	}
	return types
}

func parseCMacro(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "macro",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if name := node.ChildByFieldName("name"); name != nil {
		info.Name = nodeText(name)
	}
	return info
}

func parseCNamespace(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "namespace",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if name := node.ChildByFieldName("name"); name != nil {
		info.Name = nodeText(name)
	}
	return info
}

func parseCUsing(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Type() == "scoped_identifier" || child.Type() == "identifier" || child.Type() == "qualified_identifier" {
			info.Path = nodeText(child)
			break
		}
	}
	return info
}

func parseCTemplate(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "function_definition":
			*functions = append(*functions, parseCFunction(child))
		case "declaration":
			parseCDeclaration(child, functions, types)
		case "class_specifier", "struct_specifier":
			if t := parseCType(child); t.Name != "" {
				*types = append(*types, t)
				walkCClassBody(child, t.Name, functions, types)
			}
		case "alias_declaration":
			*types = append(*types, parseCTypedef(child))
		}
	}
}

func parseCConcept(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "concept",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if name := node.ChildByFieldName("name"); name != nil {
		info.Name = nodeText(name)
	}
	return info
}

func parseCFriend(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "function_definition":
			*functions = append(*functions, parseCFunction(child))
		case "declaration":
			parseCDeclaration(child, functions, types)
		}
	}
}

func extractRubySymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, className string)
	walk = func(node *Node, className string) {
		switch node.Type() {
		case "method":
			functions = append(functions, parseRubyMethod(node, className))
		case "singleton_method":
			fn := parseRubyMethod(node, className)
			fn.Name = "self." + fn.Name
			functions = append(functions, fn)
		case "class", "singleton_class":
			t := parseRubyClass(node)
			types = append(types, t)
			if body := node.ChildByFieldName("body"); body != nil {
				walk(body, t.Name)
			}
			return
		case "module":
			types = append(types, parseRubyModule(node))
		case "call":
			if imp := tryRubyRequire(node); imp != nil {
				imports = append(imports, *imp)
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), className)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parseRubyMethod(node *Node, className string) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  className != "",
		Receiver:  className,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseRubyClass(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "class",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseRubyModule(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "module",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func tryRubyRequire(node *Node) *ImportInfo {
	if method := node.ChildByFieldName("method"); method != nil {
		name := nodeText(method)
		if name == "require" || name == "require_relative" {
			if args := node.ChildByFieldName("arguments"); args != nil {
				if args.NamedChildCount() > 0 {
					return &ImportInfo{
						Path: nodeText(args.NamedChild(0)),
						Line: node.StartPosition().Row + 1,
					}
				}
			}
		}
	}
	return nil
}

func extractPHPSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, className string)
	walk = func(node *Node, className string) {
		switch node.Type() {
		case "function_definition":
			functions = append(functions, parsePHPFunction(node, className))
		case "method_declaration":
			functions = append(functions, parsePHPFunction(node, className))
		case "class_declaration", "interface_declaration", "trait_declaration":
			t := parsePHPType(node)
			types = append(types, t)
			if body := node.ChildByFieldName("body"); body != nil {
				walkPHPClassBody(body, t.Name, &functions)
			}
			return
		case "namespace_use_declaration":
			imports = append(imports, parsePHPUse(node)...)
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), className)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parsePHPFunction(node *Node, className string) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  className != "",
		Receiver:  className,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parsePHPType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "interface_declaration":
		info.Kind = "interface"
	case "trait_declaration":
		info.Kind = "trait"
	default:
		info.Kind = "class"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func walkPHPClassBody(body *Node, className string, functions *[]FunctionInfo) {
	count := body.NamedChildCount()
	for i := range count {
		child := body.NamedChild(uint(i))
		if child.Type() == "method_declaration" {
			*functions = append(*functions, parsePHPFunction(child, className))
		}
	}
}

func parsePHPUse(node *Node) []ImportInfo {
	var imports []ImportInfo
	count := node.NamedChildCount()
	for i := range count {
		child := node.NamedChild(uint(i))
		if child.Type() == "namespace_use_clause" || child.Type() == "qualified_name" {
			imports = append(imports, ImportInfo{
				Path: nodeText(child),
				Line: node.StartPosition().Row + 1,
			})
		}
	}
	return imports
}

func extractElixirSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, moduleName string)
	walk = func(node *Node, moduleName string) {
		switch node.Type() {
		case "call":
			name := ""
			if target := node.ChildByFieldName("target"); target != nil {
				name = nodeText(target)
			}
			switch name {
			case "def", "defp":
				functions = append(functions, parseElixirFunction(node, moduleName, name == "defp"))
			case "defmodule":
				t := parseElixirModule(node)
				types = append(types, t)
				if args := node.ChildByFieldName("arguments"); args != nil {
					if args.NamedChildCount() > 1 {
						if body := args.NamedChild(1); body != nil {
							walk(body, t.Name)
						}
					}
				}
				return
			case "import", "alias", "require", "use":
				imports = append(imports, parseElixirImport(node))
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), moduleName)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parseElixirFunction(node *Node, moduleName string, isPrivate bool) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  moduleName != "",
		Receiver:  moduleName,
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		if args.NamedChildCount() > 0 {
			first := args.NamedChild(0)
			if first.Type() == "call" {
				if target := first.ChildByFieldName("target"); target != nil {
					info.Name = nodeText(target)
				}
				if params := first.ChildByFieldName("arguments"); params != nil {
					info.Parameters = nodeText(params)
				}
			} else {
				info.Name = nodeText(first)
			}
		}
	}
	return info
}

func parseElixirModule(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "module",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		if args.NamedChildCount() > 0 {
			info.Name = nodeText(args.NamedChild(0))
		}
	}
	return info
}

func parseElixirImport(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	if args := node.ChildByFieldName("arguments"); args != nil {
		if args.NamedChildCount() > 0 {
			info.Path = nodeText(args.NamedChild(0))
		}
	}
	return info
}

func extractScalaSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, className string)
	walk = func(node *Node, className string) {
		switch node.Type() {
		case "function_definition":
			functions = append(functions, parseScalaFunction(node, className))
		case "class_definition", "trait_definition", "object_definition":
			t := parseScalaType(node)
			types = append(types, t)
			if body := node.ChildByFieldName("body"); body != nil {
				walk(body, t.Name)
			}
			return
		case "import_declaration":
			imports = append(imports, parseScalaImport(node))
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), className)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parseScalaFunction(node *Node, className string) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  className != "",
		Receiver:  className,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseScalaType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "trait_definition":
		info.Kind = "trait"
	case "object_definition":
		info.Kind = "object"
	default:
		info.Kind = "class"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseScalaImport(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	if path := node.ChildByFieldName("path"); path != nil {
		info.Path = nodeText(path)
	}
	return info
}

func extractMarkdownSections(root *Node) []TypeInfo {
	var types []TypeInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "atx_heading", "setext_heading":
			level := 0
			content := ""
			count := node.ChildCount()
			for i := range count {
				child := node.Child(uint(i))
				switch child.Type() {
				case "atx_h1_marker":
					level = 1
				case "atx_h2_marker":
					level = 2
				case "atx_h3_marker":
					level = 3
				case "atx_h4_marker":
					level = 4
				case "atx_h5_marker":
					level = 5
				case "atx_h6_marker":
					level = 6
				case "heading_content", "inline":
					content = nodeText(child)
				}
			}
			if content != "" {
				types = append(types, TypeInfo{
					Name:      content,
					Kind:      fmt.Sprintf("h%d", level),
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return types
}

func extractJSONKeys(root *Node) []TypeInfo {
	var types []TypeInfo

	if root.Type() != "document" && root.Type() != "object" {
		return types
	}

	var obj *Node
	if root.Type() == "document" && root.NamedChildCount() > 0 {
		obj = root.NamedChild(0)
	} else {
		obj = root
	}

	if obj == nil || obj.Type() != "object" {
		return types
	}

	count := obj.NamedChildCount()
	for i := range count {
		pair := obj.NamedChild(uint(i))
		if pair.Type() == "pair" {
			if key := pair.ChildByFieldName("key"); key != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(key),
					Kind:      "key",
					StartLine: pair.StartPosition().Row + 1,
					EndLine:   pair.EndPosition().Row + 1,
				})
			}
		}
	}

	return types
}

func extractYAMLKeys(root *Node) []TypeInfo {
	var types []TypeInfo

	var walk func(node *Node, path string)
	walk = func(node *Node, path string) {
		switch node.Type() {
		case "block_mapping_pair":
			if key := node.ChildByFieldName("key"); key != nil {
				keyText := nodeText(key)
				fullPath := keyText
				if path != "" {
					fullPath = path + "." + keyText
				}

				kind := "key"
				if isYAMLSpecialKey(keyText) {
					kind = keyText
				}

				types = append(types, TypeInfo{
					Name:      fullPath,
					Kind:      kind,
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})

				if value := node.ChildByFieldName("value"); value != nil {
					walk(value, fullPath)
				}
				return
			}
		case "block_sequence_item":
			count := node.NamedChildCount()
			for i := range count {
				walk(node.NamedChild(uint(i)), path)
			}
			return
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), path)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return types
}

func isYAMLSpecialKey(key string) bool {
	switch key {
	case "apiVersion", "kind", "metadata", "spec", "data", "stringData",
		"name", "namespace", "labels", "annotations", "selector",
		"template", "containers", "volumes", "env", "ports",
		"resources", "limits", "requests", "image", "command", "args",
		"replicas", "strategy", "type", "nodeSelector", "tolerations",
		"affinity", "serviceAccountName", "securityContext",
		"livenessProbe", "readinessProbe", "startupProbe":
		return true
	}
	return false
}

func extractHCLBlocks(root *Node) []TypeInfo {
	var types []TypeInfo

	var walk func(node *Node, inBlock bool)
	walk = func(node *Node, inBlock bool) {
		switch node.Type() {
		case "block":
			blockType := ""
			labels := ""

			count := node.ChildCount()
			for i := range count {
				child := node.Child(uint(i))
				switch child.Type() {
				case "identifier":
					if blockType == "" {
						blockType = nodeText(child)
					}
				case "string_lit":
					if labels != "" {
						labels += "."
					}
					labels += nodeText(child)
				}
			}

			name := blockType
			if labels != "" {
				name = blockType + " " + labels
			}

			kind := "block"
			switch blockType {
			case "resource":
				kind = "resource"
			case "data":
				kind = "data"
			case "variable":
				kind = "variable"
			case "output":
				kind = "output"
			case "locals":
				kind = "locals"
			case "module":
				kind = "module"
			case "provider":
				kind = "provider"
			case "terraform":
				kind = "terraform"
			}

			types = append(types, TypeInfo{
				Name:      name,
				Kind:      kind,
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})

			if body := node.ChildByFieldName("body"); body != nil {
				walk(body, true)
			}
			return

		case "attribute":
			if !inBlock {
				if name := node.ChildByFieldName("name"); name != nil {
					types = append(types, TypeInfo{
						Name:      nodeText(name),
						Kind:      "attribute",
						StartLine: node.StartPosition().Row + 1,
						EndLine:   node.EndPosition().Row + 1,
					})
				}
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), inBlock)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), false)
	}

	return types
}

func extractLuaSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function_declaration":
			functions = append(functions, parseLuaFunction(node))
		case "local_function":
			functions = append(functions, parseLuaLocalFunction(node))
		case "function_call":
			if imp := tryLuaRequire(node); imp != nil {
				imports = append(imports, *imp)
			}
		case "variable_declaration":
			parseLuaVarDecl(node, &functions, &types)
		case "assignment_statement":
			parseLuaAssignment(node, &functions, &types)
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parseLuaVarDecl(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	names := node.ChildByFieldName("names")
	values := node.ChildByFieldName("values")
	if names == nil {
		return
	}

	nameCount := names.NamedChildCount()
	for i := uint(0); i < nameCount; i++ {
		nameNode := names.NamedChild(i)
		name := nodeText(nameNode)

		var valueNode *Node
		if values != nil && i < values.NamedChildCount() {
			valueNode = values.NamedChild(i)
		}

		if valueNode != nil && valueNode.Type() == "function_definition" {
			fn := FunctionInfo{
				Name:      name,
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			}
			if p := valueNode.ChildByFieldName("parameters"); p != nil {
				fn.Parameters = nodeText(p)
			}
			*functions = append(*functions, fn)
		} else {
			*types = append(*types, TypeInfo{
				Name:      name,
				Kind:      "variable",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}
	}
}

func parseLuaAssignment(node *Node, functions *[]FunctionInfo, types *[]TypeInfo) {
	names := node.ChildByFieldName("variables")
	values := node.ChildByFieldName("values")
	if names == nil {
		return
	}

	nameCount := names.NamedChildCount()
	for i := uint(0); i < nameCount; i++ {
		nameNode := names.NamedChild(i)
		name := nodeText(nameNode)

		var valueNode *Node
		if values != nil && i < values.NamedChildCount() {
			valueNode = values.NamedChild(i)
		}

		if valueNode != nil && valueNode.Type() == "function_definition" {
			fn := FunctionInfo{
				Name:      name,
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			}
			if p := valueNode.ChildByFieldName("parameters"); p != nil {
				fn.Parameters = nodeText(p)
			}
			*functions = append(*functions, fn)
		} else if nameNode.Type() == "identifier" {
			*types = append(*types, TypeInfo{
				Name:      name,
				Kind:      "variable",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}
	}
}

func parseLuaFunction(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseLuaLocalFunction(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func tryLuaRequire(node *Node) *ImportInfo {
	if name := node.ChildByFieldName("name"); name != nil {
		if nodeText(name) == "require" {
			if args := node.ChildByFieldName("arguments"); args != nil {
				if args.NamedChildCount() > 0 {
					return &ImportInfo{
						Path: nodeText(args.NamedChild(0)),
						Line: node.StartPosition().Row + 1,
					}
				}
			}
		}
	}
	return nil
}

func extractTOMLKeys(root *Node) []TypeInfo {
	var types []TypeInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "table", "table_array_element":
			count := node.ChildCount()
			for i := range count {
				child := node.Child(uint(i))
				if child.Type() == "bare_key" || child.Type() == "dotted_key" || child.Type() == "quoted_key" {
					types = append(types, TypeInfo{
						Name:      nodeText(child),
						Kind:      "table",
						StartLine: node.StartPosition().Row + 1,
						EndLine:   node.EndPosition().Row + 1,
					})
					break
				}
			}
		case "pair":
			if key := node.ChildByFieldName("key"); key != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(key),
					Kind:      "key",
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return types
}

func extractCSSSelectors(root *Node) []TypeInfo {
	var types []TypeInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		if node.Type() == "rule_set" {
			if selectors := node.ChildByFieldName("selectors"); selectors != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(selectors),
					Kind:      "selector",
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return types
}

func extractZigSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function_declaration":
			functions = append(functions, parseZigFunction(node))
		case "variable_declaration":
			types = append(types, parseZigVarDecl(node)...)
		case "struct_declaration":
			if t := parseZigStruct(node); t.Name != "" {
				types = append(types, t)
			}
		case "enum_declaration":
			if t := parseZigEnum(node); t.Name != "" {
				types = append(types, t)
			}
		case "union_declaration":
			if t := parseZigUnion(node); t.Name != "" {
				types = append(types, t)
			}
		case "test_declaration":
			if fn := parseZigTest(node); fn.Name != "" {
				functions = append(functions, fn)
			}
		case "builtin_function":
			if imp := tryZigImport(node); imp != nil {
				imports = append(imports, *imp)
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parseZigFunction(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseZigVarDecl(node *Node) []TypeInfo {
	var types []TypeInfo
	if name := node.ChildByFieldName("name"); name != nil {
		kind := "const"
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if nodeText(child) == "var" {
				kind = "var"
				break
			}
		}
		types = append(types, TypeInfo{
			Name:      nodeText(name),
			Kind:      kind,
			StartLine: node.StartPosition().Row + 1,
			EndLine:   node.EndPosition().Row + 1,
		})
	}
	return types
}

func parseZigStruct(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "struct",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseZigEnum(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "enum",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseZigUnion(node *Node) TypeInfo {
	info := TypeInfo{
		Kind:      "union",
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseZigTest(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = "test " + nodeText(n)
	}
	return info
}

func tryZigImport(node *Node) *ImportInfo {
	text := nodeText(node)
	if len(text) > 7 && text[:7] == "@import" {
		return &ImportInfo{
			Path: text,
			Line: node.StartPosition().Row + 1,
		}
	}
	return nil
}

func extractKotlinSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node, className string)
	walk = func(node *Node, className string) {
		switch node.Type() {
		case "function_declaration":
			functions = append(functions, parseKotlinFunction(node, className))
		case "class_declaration", "interface_declaration", "object_declaration":
			t := parseKotlinType(node)
			types = append(types, t)
			if body := node.ChildByFieldName("body"); body != nil {
				walk(body, t.Name)
			}
			return
		case "import_header":
			imports = append(imports, parseKotlinImport(node))
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)), className)
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)), "")
	}

	return functions, types, imports
}

func parseKotlinFunction(node *Node, className string) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
		IsMethod:  className != "",
		Receiver:  className,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	if p := node.ChildByFieldName("parameters"); p != nil {
		info.Parameters = nodeText(p)
	}
	return info
}

func parseKotlinType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "interface_declaration":
		info.Kind = "interface"
	case "object_declaration":
		info.Kind = "object"
	default:
		info.Kind = "class"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseKotlinImport(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	if id := node.ChildByFieldName("identifier"); id != nil {
		info.Path = nodeText(id)
	}
	return info
}

func extractMakeTargets(root *Node) []TypeInfo {
	var types []TypeInfo

	count := root.NamedChildCount()
	for i := range count {
		node := root.NamedChild(uint(i))
		if node.Type() == "rule" {
			if targets := node.ChildByFieldName("targets"); targets != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(targets),
					Kind:      "target",
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		}
	}

	return types
}

func extractBashSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function_definition":
			functions = append(functions, parseBashFunction(node))
		case "command":
			if imp := tryBashSource(node); imp != nil {
				imports = append(imports, *imp)
			}
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parseBashFunction(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func tryBashSource(node *Node) *ImportInfo {
	if node.NamedChildCount() > 0 {
		first := node.NamedChild(0)
		name := nodeText(first)
		if name == "source" || name == "." {
			if node.NamedChildCount() > 1 {
				return &ImportInfo{
					Path: nodeText(node.NamedChild(1)),
					Line: node.StartPosition().Row + 1,
				}
			}
		}
	}
	return nil
}

func extractGitPatterns(root *Node) []TypeInfo {
	var types []TypeInfo

	count := root.NamedChildCount()
	for i := range count {
		node := root.NamedChild(uint(i))
		if node.Type() == "pattern" || node.Type() == "path" {
			types = append(types, TypeInfo{
				Name:      nodeText(node),
				Kind:      "pattern",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}
	}

	return types
}

func extractHaskellSymbolsSinglePass(root *Node) ([]FunctionInfo, []TypeInfo, []ImportInfo) {
	var functions []FunctionInfo
	var types []TypeInfo
	var imports []ImportInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "function":
			functions = append(functions, parseHaskellFunction(node))
		case "signature":
			functions = append(functions, parseHaskellSignature(node))
		case "adt", "newtype", "type_alias":
			types = append(types, parseHaskellType(node))
		case "import":
			imports = append(imports, parseHaskellImport(node))
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return functions, types, imports
}

func parseHaskellFunction(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseHaskellSignature(node *Node) FunctionInfo {
	info := FunctionInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseHaskellType(node *Node) TypeInfo {
	info := TypeInfo{
		StartLine: node.StartPosition().Row + 1,
		EndLine:   node.EndPosition().Row + 1,
	}
	switch node.Type() {
	case "adt":
		info.Kind = "data"
	case "newtype":
		info.Kind = "newtype"
	default:
		info.Kind = "type"
	}
	if n := node.ChildByFieldName("name"); n != nil {
		info.Name = nodeText(n)
	}
	return info
}

func parseHaskellImport(node *Node) ImportInfo {
	info := ImportInfo{Line: node.StartPosition().Row + 1}
	if mod := node.ChildByFieldName("module"); mod != nil {
		info.Path = nodeText(mod)
	}
	return info
}

func extractDiffFiles(root *Node) []TypeInfo {
	var types []TypeInfo

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Type() {
		case "file_change", "git_diff":
			if oldFile := node.ChildByFieldName("old"); oldFile != nil {
				types = append(types, TypeInfo{
					Name:      nodeText(oldFile),
					Kind:      "file",
					StartLine: node.StartPosition().Row + 1,
					EndLine:   node.EndPosition().Row + 1,
				})
			}
		case "filename":
			types = append(types, TypeInfo{
				Name:      nodeText(node),
				Kind:      "file",
				StartLine: node.StartPosition().Row + 1,
				EndLine:   node.EndPosition().Row + 1,
			})
		}

		count := node.NamedChildCount()
		for i := range count {
			walk(node.NamedChild(uint(i)))
		}
	}

	count := root.NamedChildCount()
	for i := range count {
		walk(root.NamedChild(uint(i)))
	}

	return types
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
	for i := range count {
		child := node.NamedChild(uint(i))
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

func walkAllNamedChildren(root *Node, fn func(*Node)) {
	stack := make([]*Node, 0, 32)

	count := root.NamedChildCount()
	for i := range count {
		stack = append(stack, root.NamedChild(uint(i)))
	}

	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		fn(node)

		count := node.NamedChildCount()
		for i := range count {
			stack = append(stack, node.NamedChild(uint(i)))
		}
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
	if !root.HasError() {
		return nil
	}

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
	for i := range count {
		fn(node.NamedChild(uint(i)))
	}
}

func walkAllChildren(root *Node, fn func(*Node)) {
	stack := make([]*Node, 0, 64)

	count := root.ChildCount()
	for i := range count {
		stack = append(stack, root.Child(uint(i)))
	}

	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		fn(node)

		count := node.ChildCount()
		for i := range count {
			stack = append(stack, node.Child(uint(i)))
		}
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
