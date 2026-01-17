package skills

import (
	"context"
	"os"
	"regexp"

	"github.com/adalundhe/sylk/core/treesitter"
)

type EngineerSkills struct {
	tool *treesitter.TreeSitterTool
}

func NewEngineerSkills(tool *treesitter.TreeSitterTool) *EngineerSkills {
	return &EngineerSkills{tool: tool}
}

type RenameResult struct {
	OldName     string           `json:"old_name"`
	NewName     string           `json:"new_name"`
	Occurrences []RenameLocation `json:"occurrences"`
	DryRun      bool             `json:"dry_run"`
}

type RenameLocation struct {
	Line   uint32 `json:"line"`
	Column uint32 `json:"column"`
}

type RenameOptions struct {
	Scope  string
	DryRun bool
}

func (e *EngineerSkills) TsRenameSymbol(ctx context.Context, filePath, oldName, newName string, opts RenameOptions) (*RenameResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	matches, err := e.tool.Query(ctx, filePath, content, `(identifier) @id`)
	if err != nil {
		return nil, err
	}

	result := &RenameResult{
		OldName:     oldName,
		NewName:     newName,
		Occurrences: make([]RenameLocation, 0),
		DryRun:      opts.DryRun,
	}

	for _, m := range matches {
		collectRenameLocations(m.Captures, oldName, &result.Occurrences)
	}

	return result, nil
}

func collectRenameLocations(captures []treesitter.ToolCapture, name string, locs *[]RenameLocation) {
	for _, c := range captures {
		if c.Content == name {
			*locs = append(*locs, RenameLocation{
				Line:   c.Node.StartLine,
				Column: c.Node.StartCol,
			})
		}
	}
}

type ExtractFunctionResult struct {
	FunctionName string   `json:"function_name"`
	StartLine    int      `json:"start_line"`
	EndLine      int      `json:"end_line"`
	Variables    []string `json:"variables"`
	DryRun       bool     `json:"dry_run"`
}

type ExtractFunctionOptions struct {
	DryRun bool
}

func (e *EngineerSkills) TsExtractFunction(ctx context.Context, filePath string, startLine, endLine int, funcName string, opts ExtractFunctionOptions) (*ExtractFunctionResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	matches, err := e.tool.Query(ctx, filePath, content, `(identifier) @id`)
	if err != nil {
		return nil, err
	}

	variables := extractVariablesInRange(matches, startLine, endLine)

	return &ExtractFunctionResult{
		FunctionName: funcName,
		StartLine:    startLine,
		EndLine:      endLine,
		Variables:    variables,
		DryRun:       opts.DryRun,
	}, nil
}

func extractVariablesInRange(matches []treesitter.ToolQueryMatch, startLine, endLine int) []string {
	seen := make(map[string]bool)
	var variables []string

	for _, m := range matches {
		for _, c := range m.Captures {
			if isInRange(c.Node, startLine, endLine) && !seen[c.Content] {
				seen[c.Content] = true
				variables = append(variables, c.Content)
			}
		}
	}

	return variables
}

func isInRange(node *treesitter.NodeInfo, startLine, endLine int) bool {
	return int(node.StartLine) >= startLine && int(node.EndLine) <= endLine
}

type EditTarget struct {
	NodeType  string `json:"node_type"`
	Name      string `json:"name,omitempty"`
	StartLine uint32 `json:"start_line"`
	EndLine   uint32 `json:"end_line"`
	Content   string `json:"content"`
}

type FindEditTargetsResult struct {
	Targets []EditTarget `json:"targets"`
}

type FindEditTargetsOptions struct {
	NamePattern string
}

func (e *EngineerSkills) TsFindEditTargets(ctx context.Context, filePath, nodeType string, opts FindEditTargetsOptions) (*FindEditTargetsResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	query := "(" + nodeType + ") @target"
	matches, err := e.tool.Query(ctx, filePath, content, query)
	if err != nil {
		return nil, err
	}

	var nameRegex *regexp.Regexp
	if opts.NamePattern != "" {
		nameRegex, _ = regexp.Compile(opts.NamePattern)
	}

	result := &FindEditTargetsResult{
		Targets: make([]EditTarget, 0),
	}

	for _, m := range matches {
		appendMatchingTargets(m.Captures, nodeType, nameRegex, &result.Targets)
	}

	return result, nil
}

func appendMatchingTargets(captures []treesitter.ToolCapture, nodeType string, nameRegex *regexp.Regexp, targets *[]EditTarget) {
	for _, c := range captures {
		if nameRegex != nil && !nameRegex.MatchString(c.Content) {
			continue
		}
		*targets = append(*targets, EditTarget{
			NodeType:  nodeType,
			StartLine: c.Node.StartLine,
			EndLine:   c.Node.EndLine,
			Content:   c.Content,
		})
	}
}

type NodeAtResult struct {
	Node    *treesitter.NodeInfo `json:"node"`
	Parents []ParentNode         `json:"parents,omitempty"`
}

type ParentNode struct {
	Type      string `json:"type"`
	StartLine uint32 `json:"start_line"`
	EndLine   uint32 `json:"end_line"`
}

type GetNodeAtOptions struct {
	IncludeParents bool
}

func (e *EngineerSkills) TsGetNodeAt(ctx context.Context, filePath string, line, column int, opts GetNodeAtOptions) (*NodeAtResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	result, err := e.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	return &NodeAtResult{
		Node:    result.RootNode,
		Parents: nil,
	}, nil
}

func (e *EngineerSkills) Close() {
}
