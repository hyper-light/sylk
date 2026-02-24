package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// RunLinterSkill returns a skill that runs golangci-lint.
func RunLinterSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_linter").
		Description("Run golangci-lint to detect code issues, style violations, and bugs.").
		Domain("analysis").
		Keywords("lint", "golangci", "check", "style").
		Priority(95).
		ArrayParam("paths", "Paths to lint (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunGolangCILint(ctx, runner, paths)
			return analysisResult("run_linter", issues), nil
		}).
		Build()
}

// RunTypeCheckerSkill returns a skill that runs go vet and staticcheck.
func RunTypeCheckerSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_type_checker").
		Description("Run go vet and staticcheck for type checking and static analysis.").
		Domain("analysis").
		Keywords("type", "check", "vet", "staticcheck").
		Priority(95).
		ArrayParam("paths", "Paths to check (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			var issues []ValidationIssue
			issues = append(issues, RunGoVet(ctx, runner, paths)...)
			issues = append(issues, RunStaticcheck(ctx, runner, paths)...)
			issues = DeduplicateIssues(issues)
			return analysisResult("run_type_checker", issues), nil
		}).
		Build()
}

// RunFormatterCheckSkill returns a skill that checks gofmt/goimports formatting.
func RunFormatterCheckSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_formatter_check").
		Description("Check code formatting with gofmt and goimports without modifying files.").
		Domain("analysis").
		Keywords("format", "gofmt", "goimports").
		Priority(90).
		ArrayParam("paths", "Paths to check (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunGoFmt(ctx, runner, paths)
			return analysisResult("run_formatter_check", issues), nil
		}).
		Build()
}

// RunSecurityScanSkill returns a skill that runs gosec security analysis.
func RunSecurityScanSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_security_scan").
		Description("Run gosec to detect security vulnerabilities in Go code.").
		Domain("analysis").
		Keywords("security", "gosec", "vulnerability", "audit").
		Priority(95).
		ArrayParam("paths", "Paths to scan (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunGoSec(ctx, runner, paths)
			criticalCount := countBySeverity(issues, Critical)
			highCount := countBySeverity(issues, High)
			result := analysisResult("run_security_scan", issues)
			result["critical_count"] = criticalCount
			result["high_count"] = highCount
			return result, nil
		}).
		Build()
}

// CheckCoverageSkill returns a skill that analyzes test coverage.
func CheckCoverageSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("check_coverage").
		Description("Analyze test coverage and flag functions below the threshold.").
		Domain("analysis").
		Keywords("coverage", "test", "threshold").
		Priority(85).
		ArrayParam("paths", "Paths to check coverage for (default: ./...)", "string", false).
		IntParam("threshold", "Minimum coverage percentage (default: 80)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Paths     []string `json:"paths"`
				Threshold float64  `json:"threshold"`
			}
			_ = json.Unmarshal(input, &params)
			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}
			issues := RunCoverage(ctx, runner, params.Paths, params.Threshold)
			return analysisResult("check_coverage", issues), nil
		}).
		Build()
}

// AnalyzeComplexitySkill returns a skill that checks cyclomatic/cognitive complexity.
func AnalyzeComplexitySkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("analyze_complexity").
		Description("Analyze cyclomatic and cognitive complexity of functions.").
		Domain("analysis").
		Keywords("complexity", "cyclomatic", "cognitive").
		Priority(85).
		ArrayParam("paths", "Paths to analyze (default: ./...)", "string", false).
		IntParam("max_cyclomatic", "Max cyclomatic complexity (default: 4 per CLAUDE.md)", false).
		IntParam("max_cognitive", "Max cognitive complexity (default: 8)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Paths         []string `json:"paths"`
				MaxCyclomatic int      `json:"max_cyclomatic"`
				MaxCognitive  int      `json:"max_cognitive"`
			}
			_ = json.Unmarshal(input, &params)
			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}
			issues := RunComplexity(ctx, runner, params.Paths, params.MaxCyclomatic, params.MaxCognitive)
			return analysisResult("analyze_complexity", issues), nil
		}).
		Build()
}

// DetectRaceConditionsSkill returns a skill that runs go test with race detection.
func DetectRaceConditionsSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("detect_race_conditions").
		Description("Run go test with race detector to find data races.").
		Domain("analysis").
		Keywords("race", "concurrency", "data race").
		Priority(95).
		ArrayParam("paths", "Paths to test (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunRaceDetector(ctx, runner, paths)
			return analysisResult("detect_race_conditions", issues), nil
		}).
		Build()
}

// DetectDeadlocksSkill returns a skill that performs AST-based deadlock analysis.
func DetectDeadlocksSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("detect_deadlocks").
		Description("Analyze Go source for potential deadlocks via lock ordering analysis.").
		Domain("analysis").
		Keywords("deadlock", "lock", "mutex").
		Priority(90).
		ArrayParam("paths", "Paths to analyze (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunDeadlockDetector(ctx, runner, paths)
			return analysisResult("detect_deadlocks", issues), nil
		}).
		Build()
}

// DetectMemoryLeaksSkill returns a skill that runs escape analysis.
func DetectMemoryLeaksSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("detect_memory_leaks").
		Description("Run escape analysis to detect memory leaks and unnecessary heap allocations.").
		Domain("analysis").
		Keywords("memory", "leak", "escape", "heap").
		Priority(90).
		ArrayParam("paths", "Paths to analyze (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunMemoryAnalyzer(ctx, runner, paths)
			return analysisResult("detect_memory_leaks", issues), nil
		}).
		Build()
}

// ReadFileSkill returns a read-only file reading skill.
func ReadFileSkill() *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read the contents of a file. Read-only — never modifies files.").
		Domain("filesystem").
		Keywords("read", "file", "content").
		Priority(90).
		StringParam("path", "Absolute or relative path to the file", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			data, err := os.ReadFile(params.Path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", params.Path, err)
			}

			return map[string]any{
				"path":    params.Path,
				"content": string(data),
				"size":    len(data),
			}, nil
		}).
		Build()
}

// GlobSkill returns a read-only file glob skill.
func GlobSkill() *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files matching a glob pattern. Read-only.").
		Domain("filesystem").
		Keywords("glob", "find", "files", "pattern").
		Priority(85).
		StringParam("pattern", "Glob pattern (e.g. **/*.go)", true).
		StringParam("dir", "Base directory to search from", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Pattern string `json:"pattern"`
				Dir     string `json:"dir"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}
			if params.Dir == "" {
				params.Dir = "."
			}

			var matches []string
			err := filepath.WalkDir(params.Dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				matched, matchErr := filepath.Match(params.Pattern, filepath.Base(path))
				if matchErr != nil {
					return nil
				}
				if matched {
					matches = append(matches, path)
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("walk %s: %w", params.Dir, err)
			}

			return map[string]any{
				"pattern": params.Pattern,
				"matches": matches,
				"count":   len(matches),
			}, nil
		}).
		Build()
}

// GrepSkill returns a read-only text search skill.
func GrepSkill() *skills.Skill {
	return skills.NewSkill("grep").
		Description("Search file contents for a pattern. Read-only.").
		Domain("filesystem").
		Keywords("grep", "search", "find", "pattern").
		Priority(85).
		StringParam("pattern", "Search pattern (substring match)", true).
		StringParam("path", "File or directory to search", true).
		StringParam("glob", "File glob filter (e.g. *.go)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
				Glob    string `json:"glob"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Pattern == "" || params.Path == "" {
				return nil, fmt.Errorf("pattern and path are required")
			}

			type match struct {
				File string `json:"file"`
				Line int    `json:"line"`
				Text string `json:"text"`
			}
			var results []match

			const maxMatches = 100
			err := filepath.WalkDir(params.Path, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if len(results) >= maxMatches {
					return filepath.SkipAll
				}
				if params.Glob != "" {
					matched, _ := filepath.Match(params.Glob, filepath.Base(path))
					if !matched {
						return nil
					}
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return nil
				}
				for i, line := range strings.Split(string(data), "\n") {
					if strings.Contains(line, params.Pattern) {
						results = append(results, match{
							File: path,
							Line: i + 1,
							Text: line,
						})
						if len(results) >= maxMatches {
							return filepath.SkipAll
						}
					}
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("search %s: %w", params.Path, err)
			}

			return map[string]any{
				"pattern": params.Pattern,
				"matches": results,
				"count":   len(results),
			}, nil
		}).
		Build()
}

// --- helpers ---

func extractPaths(input json.RawMessage) []string {
	var params struct {
		Paths []string `json:"paths"`
	}
	_ = json.Unmarshal(input, &params)
	if len(params.Paths) == 0 {
		return []string{"./..."}
	}
	return params.Paths
}

func analysisResult(tool string, issues []ValidationIssue) map[string]any {
	return map[string]any{
		"tool":        tool,
		"passed":      !HasBlockingIssues(issues),
		"issue_count": len(issues),
		"issues":      issues,
	}
}

func countBySeverity(issues []ValidationIssue, sev Severity) int {
	count := 0
	for _, issue := range issues {
		if issue.Severity == sev {
			count++
		}
	}
	return count
}
