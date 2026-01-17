package skills

import (
	"context"
	"os"
	"strings"

	"github.com/adalundhe/sylk/core/treesitter"
)

type TesterSkills struct {
	tool *treesitter.TreeSitterTool
}

func NewTesterSkills(tool *treesitter.TreeSitterTool) *TesterSkills {
	return &TesterSkills{tool: tool}
}

type DiscoverTestsResult struct {
	Files []FileTests `json:"files"`
	Total int         `json:"total"`
}

type FileTests struct {
	FilePath string     `json:"file_path"`
	Tests    []TestInfo `json:"tests"`
}

type TestInfo struct {
	Name      string   `json:"name"`
	StartLine uint32   `json:"start_line"`
	EndLine   uint32   `json:"end_line"`
	SubTests  []string `json:"sub_tests,omitempty"`
}

type DiscoverTestsOptions struct {
	IncludeSubtests bool
}

func (t *TesterSkills) TsDiscoverTests(ctx context.Context, files []string, opts DiscoverTestsOptions) (*DiscoverTestsResult, error) {
	result := &DiscoverTestsResult{
		Files: make([]FileTests, 0, len(files)),
	}

	for _, filePath := range files {
		ft, err := t.discoverFileTests(ctx, filePath, opts)
		if err != nil {
			continue
		}
		if len(ft.Tests) > 0 {
			result.Files = append(result.Files, ft)
			result.Total += len(ft.Tests)
		}
	}

	return result, nil
}

func (t *TesterSkills) discoverFileTests(ctx context.Context, filePath string, opts DiscoverTestsOptions) (FileTests, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileTests{}, err
	}

	parseResult, err := t.tool.Parse(ctx, filePath, content)
	if err != nil {
		return FileTests{}, err
	}

	return FileTests{
		FilePath: filePath,
		Tests:    filterTestFunctions(parseResult.Functions),
	}, nil
}

func filterTestFunctions(functions []treesitter.FunctionInfo) []TestInfo {
	tests := make([]TestInfo, 0)
	for _, f := range functions {
		if isTestFunction(f.Name) {
			tests = append(tests, TestInfo{
				Name:      f.Name,
				StartLine: f.StartLine,
				EndLine:   f.EndLine,
			})
		}
	}
	return tests
}

func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "test_")
}

type TestableFunctionsResult struct {
	Files []FileTestableFunctions `json:"files"`
}

type FileTestableFunctions struct {
	FilePath  string             `json:"file_path"`
	Functions []TestableFunction `json:"functions"`
}

type TestableFunction struct {
	Name       string `json:"name"`
	StartLine  uint32 `json:"start_line"`
	Complexity int    `json:"complexity"`
	IsPublic   bool   `json:"is_public"`
}

type TestableFunctionsOptions struct {
	ExcludePrivate bool
	MinComplexity  int
}

func (t *TesterSkills) TsFindTestableFunctions(ctx context.Context, files []string, opts TestableFunctionsOptions) (*TestableFunctionsResult, error) {
	result := &TestableFunctionsResult{
		Files: make([]FileTestableFunctions, 0, len(files)),
	}

	for _, filePath := range files {
		t.appendTestableFunctions(ctx, filePath, opts, &result.Files)
	}

	return result, nil
}

func (t *TesterSkills) appendTestableFunctions(ctx context.Context, filePath string, opts TestableFunctionsOptions, files *[]FileTestableFunctions) {
	if strings.Contains(filePath, "_test.") {
		return
	}

	ftf, err := t.findFileTestableFunctions(ctx, filePath, opts)
	if err != nil || len(ftf.Functions) == 0 {
		return
	}
	*files = append(*files, ftf)
}

func (t *TesterSkills) findFileTestableFunctions(ctx context.Context, filePath string, opts TestableFunctionsOptions) (FileTestableFunctions, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileTestableFunctions{}, err
	}

	parseResult, err := t.tool.Parse(ctx, filePath, content)
	if err != nil {
		return FileTestableFunctions{}, err
	}

	return FileTestableFunctions{
		FilePath:  filePath,
		Functions: buildTestableFunctions(parseResult.Functions, opts.ExcludePrivate),
	}, nil
}

func buildTestableFunctions(functions []treesitter.FunctionInfo, excludePrivate bool) []TestableFunction {
	result := make([]TestableFunction, 0)
	for _, f := range functions {
		if tf, ok := makeTestableFunction(f, excludePrivate); ok {
			result = append(result, tf)
		}
	}
	return result
}

func makeTestableFunction(f treesitter.FunctionInfo, excludePrivate bool) (TestableFunction, bool) {
	isPublic := isPublicFunction(f.Name)
	if excludePrivate && !isPublic {
		return TestableFunction{}, false
	}
	return TestableFunction{
		Name:       f.Name,
		StartLine:  f.StartLine,
		Complexity: 1,
		IsPublic:   isPublic,
	}, true
}

func isPublicFunction(name string) bool {
	if len(name) == 0 {
		return false
	}
	first := name[0]
	return first >= 'A' && first <= 'Z'
}

type TestStructureResult struct {
	FilePath     string          `json:"file_path"`
	TestCount    int             `json:"test_count"`
	SubtestCount int             `json:"subtest_count"`
	Helpers      []string        `json:"helpers"`
	SetupFuncs   []string        `json:"setup_functions"`
	Tests        []TestStructure `json:"tests"`
}

type TestStructure struct {
	Name       string   `json:"name"`
	StartLine  uint32   `json:"start_line"`
	Assertions int      `json:"assertions"`
	SubTests   []string `json:"sub_tests,omitempty"`
}

func (t *TesterSkills) TsAnalyzeTestStructure(ctx context.Context, testFile string) (*TestStructureResult, error) {
	content, err := os.ReadFile(testFile)
	if err != nil {
		return nil, err
	}

	parseResult, err := t.tool.Parse(ctx, testFile, content)
	if err != nil {
		return nil, err
	}

	return buildTestStructureResult(testFile, parseResult.Functions), nil
}

func buildTestStructureResult(testFile string, functions []treesitter.FunctionInfo) *TestStructureResult {
	result := &TestStructureResult{
		FilePath: testFile,
		Helpers:  make([]string, 0),
		Tests:    make([]TestStructure, 0),
	}

	for _, f := range functions {
		classifyFunction(f, result)
	}

	return result
}

func classifyFunction(f treesitter.FunctionInfo, result *TestStructureResult) {
	switch {
	case isTestFunction(f.Name):
		result.TestCount++
		result.Tests = append(result.Tests, TestStructure{
			Name:      f.Name,
			StartLine: f.StartLine,
		})
	case isHelperFunction(f.Name):
		result.Helpers = append(result.Helpers, f.Name)
	case isSetupFunction(f.Name):
		result.SetupFuncs = append(result.SetupFuncs, f.Name)
	}
}

func isHelperFunction(name string) bool {
	return strings.HasSuffix(name, "Helper") || strings.HasPrefix(name, "helper")
}

func isSetupFunction(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "setup") || strings.Contains(lower, "teardown")
}

type AssertionsResult struct {
	Files []FileAssertions `json:"files"`
	Total int              `json:"total"`
}

type FileAssertions struct {
	FilePath   string      `json:"file_path"`
	Assertions []Assertion `json:"assertions"`
}

type Assertion struct {
	Type   string `json:"type"`
	Line   uint32 `json:"line"`
	InTest string `json:"in_test,omitempty"`
}

func (t *TesterSkills) TsFindAssertions(ctx context.Context, files []string) (*AssertionsResult, error) {
	result := &AssertionsResult{
		Files: make([]FileAssertions, 0, len(files)),
	}

	for _, filePath := range files {
		fa, err := t.findFileAssertions(ctx, filePath)
		if err != nil {
			continue
		}
		if len(fa.Assertions) > 0 {
			result.Files = append(result.Files, fa)
			result.Total += len(fa.Assertions)
		}
	}

	return result, nil
}

func (t *TesterSkills) findFileAssertions(ctx context.Context, filePath string) (FileAssertions, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileAssertions{}, err
	}

	matches, err := t.tool.Query(ctx, filePath, content, `(call_expression function: (selector_expression field: (field_identifier) @method)) @call`)
	if err != nil {
		return FileAssertions{}, err
	}

	fa := FileAssertions{
		FilePath:   filePath,
		Assertions: make([]Assertion, 0),
	}

	for _, m := range matches {
		collectAssertions(m.Captures, &fa.Assertions)
	}

	return fa, nil
}

func collectAssertions(captures []treesitter.ToolCapture, assertions *[]Assertion) {
	for _, c := range captures {
		if c.Name == "method" && isAssertionMethod(c.Content) {
			*assertions = append(*assertions, Assertion{
				Type: c.Content,
				Line: c.Node.StartLine,
			})
		}
	}
}

func isAssertionMethod(name string) bool {
	assertMethods := []string{
		"Error", "Errorf", "Fatal", "Fatalf",
		"Equal", "NotEqual", "True", "False",
		"Nil", "NotNil", "Contains", "Fail",
	}
	for _, m := range assertMethods {
		if name == m {
			return true
		}
	}
	return false
}

type TestMappingResult struct {
	SourceFile string        `json:"source_file"`
	TestFile   string        `json:"test_file"`
	Mappings   []TestMapping `json:"mappings"`
	Untested   []string      `json:"untested"`
}

type TestMapping struct {
	Function string   `json:"function"`
	Tests    []string `json:"tests"`
}

func (t *TesterSkills) TsMatchTestsToFunctions(ctx context.Context, sourceFile, testFile string) (*TestMappingResult, error) {
	sourceResult, testResult, err := t.parseSourceAndTest(ctx, sourceFile, testFile)
	if err != nil {
		return nil, err
	}

	tests := extractTestNames(testResult.Functions)
	return buildTestMappingResult(sourceFile, testFile, sourceResult.Functions, tests), nil
}

func (t *TesterSkills) parseSourceAndTest(ctx context.Context, sourceFile, testFile string) (*treesitter.ParseResult, *treesitter.ParseResult, error) {
	sourceResult, err := t.parseFile(ctx, sourceFile)
	if err != nil {
		return nil, nil, err
	}

	testResult, err := t.parseFile(ctx, testFile)
	if err != nil {
		return nil, nil, err
	}

	return sourceResult, testResult, nil
}

func (t *TesterSkills) parseFile(ctx context.Context, filePath string) (*treesitter.ParseResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return t.tool.Parse(ctx, filePath, content)
}

func buildTestMappingResult(sourceFile, testFile string, functions []treesitter.FunctionInfo, tests []string) *TestMappingResult {
	result := &TestMappingResult{
		SourceFile: sourceFile,
		TestFile:   testFile,
		Mappings:   make([]TestMapping, 0),
		Untested:   make([]string, 0),
	}

	for _, f := range functions {
		if !isPublicFunction(f.Name) {
			continue
		}
		addFunctionMapping(f.Name, tests, result)
	}

	return result
}

func addFunctionMapping(funcName string, tests []string, result *TestMappingResult) {
	matching := findMatchingTests(funcName, tests)
	if len(matching) > 0 {
		result.Mappings = append(result.Mappings, TestMapping{
			Function: funcName,
			Tests:    matching,
		})
	} else {
		result.Untested = append(result.Untested, funcName)
	}
}

func extractTestNames(functions []treesitter.FunctionInfo) []string {
	var tests []string
	for _, f := range functions {
		if isTestFunction(f.Name) {
			tests = append(tests, f.Name)
		}
	}
	return tests
}

func findMatchingTests(funcName string, tests []string) []string {
	var matching []string
	for _, t := range tests {
		if strings.Contains(t, funcName) {
			matching = append(matching, t)
		}
	}
	return matching
}

func (t *TesterSkills) Close() {
}
