package skills

import (
	"context"
	"os"

	"github.com/adalundhe/sylk/core/treesitter"
)

type InspectorSkills struct {
	tool *treesitter.TreeSitterTool
}

func NewInspectorSkills(tool *treesitter.TreeSitterTool) *InspectorSkills {
	return &InspectorSkills{tool: tool}
}

type ComplexityResult struct {
	Files []FileComplexity `json:"files"`
}

type FileComplexity struct {
	FilePath   string               `json:"file_path"`
	Functions  []FunctionComplexity `json:"functions"`
	TotalScore int                  `json:"total_score"`
}

type FunctionComplexity struct {
	Name       string `json:"name"`
	Cyclomatic int    `json:"cyclomatic"`
	Cognitive  int    `json:"cognitive"`
	Nesting    int    `json:"max_nesting"`
	StartLine  uint32 `json:"start_line"`
}

type ComplexityThresholds struct {
	Cyclomatic int
	Cognitive  int
	Nesting    int
}

func (i *InspectorSkills) TsComplexityAnalysis(ctx context.Context, files []string, thresholds ComplexityThresholds) (*ComplexityResult, error) {
	result := &ComplexityResult{
		Files: make([]FileComplexity, 0, len(files)),
	}

	for _, filePath := range files {
		fc, err := i.analyzeFileComplexity(ctx, filePath)
		if err != nil {
			continue
		}
		result.Files = append(result.Files, fc)
	}

	return result, nil
}

func (i *InspectorSkills) analyzeFileComplexity(ctx context.Context, filePath string) (FileComplexity, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileComplexity{}, err
	}

	parseResult, err := i.tool.Parse(ctx, filePath, content)
	if err != nil {
		return FileComplexity{}, err
	}

	fc := FileComplexity{
		FilePath:  filePath,
		Functions: make([]FunctionComplexity, len(parseResult.Functions)),
	}

	for j, f := range parseResult.Functions {
		fc.Functions[j] = computeFunctionComplexity(f)
		fc.TotalScore += fc.Functions[j].Cyclomatic
	}

	return fc, nil
}

func computeFunctionComplexity(f treesitter.FunctionInfo) FunctionComplexity {
	return FunctionComplexity{
		Name:       f.Name,
		Cyclomatic: 1,
		Cognitive:  0,
		Nesting:    0,
		StartLine:  f.StartLine,
	}
}

type CodeSmellResult struct {
	Smells []CodeSmell `json:"smells"`
}

type CodeSmell struct {
	Type      string `json:"type"`
	FilePath  string `json:"file_path"`
	StartLine uint32 `json:"start_line"`
	EndLine   uint32 `json:"end_line"`
	Message   string `json:"message"`
}

func (i *InspectorSkills) TsFindCodeSmells(ctx context.Context, files []string, smellTypes []string) (*CodeSmellResult, error) {
	result := &CodeSmellResult{
		Smells: make([]CodeSmell, 0),
	}

	smellSet := makeSmellSet(smellTypes)

	for _, filePath := range files {
		smells, err := i.findFileSmells(ctx, filePath, smellSet)
		if err != nil {
			continue
		}
		result.Smells = append(result.Smells, smells...)
	}

	return result, nil
}

func makeSmellSet(types []string) map[string]bool {
	set := make(map[string]bool)
	for _, t := range types {
		set[t] = true
	}
	return set
}

func (i *InspectorSkills) findFileSmells(ctx context.Context, filePath string, smellSet map[string]bool) ([]CodeSmell, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	parseResult, err := i.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	var smells []CodeSmell

	if smellSet["long_method"] {
		smells = append(smells, findLongMethods(parseResult.Functions, filePath)...)
	}

	return smells, nil
}

func findLongMethods(functions []treesitter.FunctionInfo, filePath string) []CodeSmell {
	var smells []CodeSmell
	for _, f := range functions {
		lineCount := f.EndLine - f.StartLine
		if lineCount > 50 {
			smells = append(smells, CodeSmell{
				Type:      "long_method",
				FilePath:  filePath,
				StartLine: f.StartLine,
				EndLine:   f.EndLine,
				Message:   "function exceeds 50 lines",
			})
		}
	}
	return smells
}

type ValidateResult struct {
	Valid    bool             `json:"valid"`
	Matches  []PatternMatch   `json:"matches"`
	Failures []PatternFailure `json:"failures"`
}

type PatternMatch struct {
	Pattern  string `json:"pattern"`
	FilePath string `json:"file_path"`
	Count    int    `json:"count"`
}

type PatternFailure struct {
	Pattern  string `json:"pattern"`
	FilePath string `json:"file_path"`
	Reason   string `json:"reason"`
}

func (i *InspectorSkills) TsValidateStructure(ctx context.Context, files []string, expectedPatterns, forbiddenPatterns []string) (*ValidateResult, error) {
	result := &ValidateResult{
		Valid:    true,
		Matches:  make([]PatternMatch, 0),
		Failures: make([]PatternFailure, 0),
	}

	for _, filePath := range files {
		i.validateFile(ctx, filePath, expectedPatterns, forbiddenPatterns, result)
	}

	return result, nil
}

func (i *InspectorSkills) validateFile(ctx context.Context, filePath string, expected, forbidden []string, result *ValidateResult) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	i.checkExpectedPatterns(ctx, filePath, content, expected, result)
	i.checkForbiddenPatterns(ctx, filePath, content, forbidden, result)
}

func (i *InspectorSkills) checkExpectedPatterns(ctx context.Context, filePath string, content []byte, patterns []string, result *ValidateResult) {
	for _, pattern := range patterns {
		matches, err := i.tool.Query(ctx, filePath, content, pattern)
		if err != nil || len(matches) == 0 {
			result.Valid = false
			result.Failures = append(result.Failures, PatternFailure{
				Pattern:  pattern,
				FilePath: filePath,
				Reason:   "expected pattern not found",
			})
		} else {
			result.Matches = append(result.Matches, PatternMatch{
				Pattern:  pattern,
				FilePath: filePath,
				Count:    len(matches),
			})
		}
	}
}

func (i *InspectorSkills) checkForbiddenPatterns(ctx context.Context, filePath string, content []byte, patterns []string, result *ValidateResult) {
	for _, pattern := range patterns {
		matches, err := i.tool.Query(ctx, filePath, content, pattern)
		if err == nil && len(matches) > 0 {
			result.Valid = false
			result.Failures = append(result.Failures, PatternFailure{
				Pattern:  pattern,
				FilePath: filePath,
				Reason:   "forbidden pattern found",
			})
		}
	}
}

type NodeCountResult struct {
	Files []FileNodeCount `json:"files"`
	Total map[string]int  `json:"total"`
}

type FileNodeCount struct {
	FilePath string         `json:"file_path"`
	Counts   map[string]int `json:"counts"`
}

func (i *InspectorSkills) TsCountNodes(ctx context.Context, files []string, nodeTypes []string) (*NodeCountResult, error) {
	result := &NodeCountResult{
		Files: make([]FileNodeCount, 0, len(files)),
		Total: make(map[string]int),
	}

	for _, filePath := range files {
		fc, err := i.countFileNodes(ctx, filePath, nodeTypes)
		if err != nil {
			continue
		}
		result.Files = append(result.Files, fc)
		for k, v := range fc.Counts {
			result.Total[k] += v
		}
	}

	return result, nil
}

func (i *InspectorSkills) countFileNodes(ctx context.Context, filePath string, nodeTypes []string) (FileNodeCount, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileNodeCount{}, err
	}

	fc := FileNodeCount{
		FilePath: filePath,
		Counts:   make(map[string]int),
	}

	for _, nodeType := range nodeTypes {
		query := "(" + nodeType + ") @node"
		matches, err := i.tool.Query(ctx, filePath, content, query)
		if err == nil {
			fc.Counts[nodeType] = len(matches)
		}
	}

	return fc, nil
}

type ParseErrorsResult struct {
	Files []FileParseErrors `json:"files"`
}

type FileParseErrors struct {
	FilePath string                  `json:"file_path"`
	Errors   []treesitter.ParseError `json:"errors"`
}

func (i *InspectorSkills) TsParseErrors(ctx context.Context, files []string) (*ParseErrorsResult, error) {
	result := &ParseErrorsResult{
		Files: make([]FileParseErrors, 0, len(files)),
	}

	for _, filePath := range files {
		fe, err := i.findFileParseErrors(ctx, filePath)
		if err != nil {
			continue
		}
		if len(fe.Errors) > 0 {
			result.Files = append(result.Files, fe)
		}
	}

	return result, nil
}

func (i *InspectorSkills) findFileParseErrors(ctx context.Context, filePath string) (FileParseErrors, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileParseErrors{}, err
	}

	parseResult, err := i.tool.Parse(ctx, filePath, content)
	if err != nil {
		return FileParseErrors{}, err
	}

	return FileParseErrors{
		FilePath: filePath,
		Errors:   parseResult.Errors,
	}, nil
}

func (i *InspectorSkills) Close() {
}
