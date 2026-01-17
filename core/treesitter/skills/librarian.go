package skills

import (
	"context"
	"os"

	"github.com/adalundhe/sylk/core/treesitter"
)

type LibrarianSkills struct {
	tool *treesitter.TreeSitterTool
}

func NewLibrarianSkills(tool *treesitter.TreeSitterTool) *LibrarianSkills {
	return &LibrarianSkills{tool: tool}
}

type ParseOptions struct {
	IncludeChildren bool
}

type ParseOutput struct {
	Language  string                    `json:"language"`
	FilePath  string                    `json:"file_path"`
	RootNode  *treesitter.NodeInfo      `json:"root_node"`
	Functions []treesitter.FunctionInfo `json:"functions,omitempty"`
	Types     []treesitter.TypeInfo     `json:"types,omitempty"`
	Imports   []treesitter.ImportInfo   `json:"imports,omitempty"`
	Errors    []treesitter.ParseError   `json:"errors,omitempty"`
}

func (l *LibrarianSkills) TsParse(ctx context.Context, filePath string, opts ParseOptions) (*ParseOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	result, err := l.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	return &ParseOutput{
		Language:  result.Language,
		FilePath:  result.FilePath,
		RootNode:  result.RootNode,
		Functions: result.Functions,
		Types:     result.Types,
		Imports:   result.Imports,
		Errors:    result.Errors,
	}, nil
}

type QueryOptions struct {
	CaptureName string
}

type QueryOutput struct {
	Matches []QueryMatchOutput `json:"matches"`
}

type QueryMatchOutput struct {
	PatternIndex uint                 `json:"pattern_index"`
	Captures     []QueryCaptureOutput `json:"captures"`
}

type QueryCaptureOutput struct {
	Name    string               `json:"name"`
	Node    *treesitter.NodeInfo `json:"node"`
	Content string               `json:"content"`
}

func (l *LibrarianSkills) TsQuery(ctx context.Context, filePath, query string, opts QueryOptions) (*QueryOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	matches, err := l.tool.Query(ctx, filePath, content, query)
	if err != nil {
		return nil, err
	}

	return buildQueryOutput(matches, opts.CaptureName), nil
}

func buildQueryOutput(matches []treesitter.ToolQueryMatch, captureName string) *QueryOutput {
	output := &QueryOutput{
		Matches: make([]QueryMatchOutput, 0, len(matches)),
	}

	for _, m := range matches {
		appendFilteredMatch(output, m, captureName)
	}

	return output
}

func appendFilteredMatch(output *QueryOutput, m treesitter.ToolQueryMatch, captureName string) {
	captures := filterCaptures(m.Captures, captureName)
	if len(captures) > 0 {
		output.Matches = append(output.Matches, QueryMatchOutput{
			PatternIndex: m.PatternIndex,
			Captures:     captures,
		})
	}
}

func filterCaptures(captures []treesitter.ToolCapture, name string) []QueryCaptureOutput {
	result := make([]QueryCaptureOutput, 0, len(captures))
	for _, c := range captures {
		if name != "" && c.Name != name {
			continue
		}
		result = append(result, QueryCaptureOutput{
			Name:    c.Name,
			Node:    c.Node,
			Content: c.Content,
		})
	}
	return result
}

type FunctionOptions struct {
	IncludeBody bool
}

type FunctionOutput struct {
	Functions []FunctionEntry `json:"functions"`
}

type FunctionEntry struct {
	Name       string `json:"name"`
	StartLine  uint32 `json:"start_line"`
	EndLine    uint32 `json:"end_line"`
	Parameters string `json:"parameters,omitempty"`
	ReturnType string `json:"return_type,omitempty"`
	IsMethod   bool   `json:"is_method"`
	Receiver   string `json:"receiver,omitempty"`
	Body       string `json:"body,omitempty"`
}

func (l *LibrarianSkills) TsFindFunctions(ctx context.Context, filePath string, opts FunctionOptions) (*FunctionOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	result, err := l.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	output := &FunctionOutput{
		Functions: make([]FunctionEntry, len(result.Functions)),
	}

	for i, f := range result.Functions {
		output.Functions[i] = FunctionEntry{
			Name:       f.Name,
			StartLine:  f.StartLine,
			EndLine:    f.EndLine,
			Parameters: f.Parameters,
			ReturnType: f.ReturnType,
			IsMethod:   f.IsMethod,
			Receiver:   f.Receiver,
		}
	}

	return output, nil
}

type TypeOptions struct {
	IncludeFields bool
}

type TypeOutput struct {
	Types []TypeEntry `json:"types"`
}

type TypeEntry struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	StartLine uint32   `json:"start_line"`
	EndLine   uint32   `json:"end_line"`
	Fields    []string `json:"fields,omitempty"`
}

func (l *LibrarianSkills) TsFindTypes(ctx context.Context, filePath string, opts TypeOptions) (*TypeOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	result, err := l.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	return buildTypeOutput(result.Types, opts.IncludeFields), nil
}

func buildTypeOutput(types []treesitter.TypeInfo, includeFields bool) *TypeOutput {
	output := &TypeOutput{
		Types: make([]TypeEntry, len(types)),
	}

	for i, t := range types {
		output.Types[i] = typeInfoToEntry(t, includeFields)
	}

	return output
}

func typeInfoToEntry(t treesitter.TypeInfo, includeFields bool) TypeEntry {
	entry := TypeEntry{
		Name:      t.Name,
		Kind:      t.Kind,
		StartLine: t.StartLine,
		EndLine:   t.EndLine,
	}
	if includeFields {
		entry.Fields = t.Fields
	}
	return entry
}

type ImportOutput struct {
	Imports []ImportEntry `json:"imports"`
}

type ImportEntry struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
	Line  uint32 `json:"line"`
}

func (l *LibrarianSkills) TsFindImports(ctx context.Context, filePath string) (*ImportOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	result, err := l.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	output := &ImportOutput{
		Imports: make([]ImportEntry, len(result.Imports)),
	}

	for i, imp := range result.Imports {
		output.Imports[i] = ImportEntry{
			Path:  imp.Path,
			Alias: imp.Alias,
			Line:  imp.Line,
		}
	}

	return output, nil
}

type ReferenceOptions struct {
	IncludeDefinitions bool
}

type ReferenceOutput struct {
	Symbol     string           `json:"symbol"`
	References []ReferenceEntry `json:"references"`
}

type ReferenceEntry struct {
	Line    uint32 `json:"line"`
	Column  uint32 `json:"column"`
	Content string `json:"content"`
}

func (l *LibrarianSkills) TsFindReferences(ctx context.Context, filePath, symbol string, opts ReferenceOptions) (*ReferenceOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	matches, err := l.tool.Query(ctx, filePath, content, `(identifier) @id`)
	if err != nil {
		return nil, err
	}

	return buildReferenceOutput(matches, symbol), nil
}

func buildReferenceOutput(matches []treesitter.ToolQueryMatch, symbol string) *ReferenceOutput {
	output := &ReferenceOutput{
		Symbol:     symbol,
		References: make([]ReferenceEntry, 0),
	}

	for _, m := range matches {
		collectSymbolReferences(m.Captures, symbol, &output.References)
	}

	return output
}

func collectSymbolReferences(captures []treesitter.ToolCapture, symbol string, refs *[]ReferenceEntry) {
	for _, c := range captures {
		if c.Content == symbol {
			*refs = append(*refs, ReferenceEntry{
				Line:    c.Node.StartLine,
				Column:  c.Node.StartCol,
				Content: c.Content,
			})
		}
	}
}

type ExtractSymbolsOptions struct {
	Types []string
}

type SymbolOutput struct {
	Symbols []SymbolEntry `json:"symbols"`
}

type SymbolEntry struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	StartLine uint32 `json:"start_line"`
	EndLine   uint32 `json:"end_line"`
}

func (l *LibrarianSkills) TsExtractSymbols(ctx context.Context, filePath string, opts ExtractSymbolsOptions) (*SymbolOutput, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	result, err := l.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	typeSet := makeTypeSet(opts.Types)
	return buildSymbolOutput(result, typeSet), nil
}

func buildSymbolOutput(result *treesitter.ParseResult, typeSet map[string]bool) *SymbolOutput {
	output := &SymbolOutput{
		Symbols: make([]SymbolEntry, 0),
	}

	appendFunctionSymbols(output, result.Functions, typeSet)
	appendTypeSymbols(output, result.Types, typeSet)

	return output
}

func appendFunctionSymbols(output *SymbolOutput, funcs []treesitter.FunctionInfo, typeSet map[string]bool) {
	if !shouldInclude(typeSet, "function") {
		return
	}
	for _, f := range funcs {
		output.Symbols = append(output.Symbols, SymbolEntry{
			Name:      f.Name,
			Kind:      "function",
			StartLine: f.StartLine,
			EndLine:   f.EndLine,
		})
	}
}

func appendTypeSymbols(output *SymbolOutput, types []treesitter.TypeInfo, typeSet map[string]bool) {
	if !shouldInclude(typeSet, "type") {
		return
	}
	for _, t := range types {
		output.Symbols = append(output.Symbols, SymbolEntry{
			Name:      t.Name,
			Kind:      t.Kind,
			StartLine: t.StartLine,
			EndLine:   t.EndLine,
		})
	}
}

func makeTypeSet(types []string) map[string]bool {
	if len(types) == 0 {
		return nil
	}
	set := make(map[string]bool)
	for _, t := range types {
		set[t] = true
	}
	return set
}

func shouldInclude(typeSet map[string]bool, kind string) bool {
	if typeSet == nil {
		return true
	}
	return typeSet[kind]
}

func (l *LibrarianSkills) Close() {
}
