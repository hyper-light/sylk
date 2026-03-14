package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// FileAccess is satisfied by versioning.FileAccess implementations (VFSFileAccess,
// DiskFileAccess, GlobalVFSFileAccess). Pipeline agents pass their per-pipeline
// VFS so that read operations see staged content.
type FileAccess = versioning.FileAccess

// FileAccessFunc returns the current FileAccess for an agent. Skills capture
// this function at registration time and call it at invocation time, so the
// returned value reflects whatever was injected via SetFileAccess().
type FileAccessFunc func() FileAccess

// RunLinterSkill returns a skill that runs a language-appropriate linter.
func RunLinterSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_linter").
		Description("Run a language-appropriate linter to detect code issues, style violations, and likely bugs.").
		Domain("analysis").
		Keywords("lint", "check", "style", "analysis").
		Priority(95).
		Usage("Use during implementation-validation mode after you understand the target files and criteria. Run it on the concrete scope under review, not the entire repo by default.").
		Requirement("Requires implementation evidence and the relevant target paths.").
		Satisfies("Produces code-quality findings that support criteria evaluation and final grading.").
		Avoid("Do not use during pre-implementation contract synthesis when there is no implementation evidence to validate.").
		ArrayParam("paths", "Paths to lint (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := runLinterAnalysis(ctx, runner, paths)
			logAnalysisFindings(ctx, "run_linter", issues)
			return analysisResult("run_linter", issues), nil
		}).
		Build()
}

// RunTypeCheckerSkill returns a skill that runs a language-appropriate type checker.
func RunTypeCheckerSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_type_checker").
		Description("Run a language-appropriate type checker or static analyzer for the requested files.").
		Domain("analysis").
		Keywords("type", "check", "static", "analysis").
		Priority(95).
		Usage("Use early in implementation-validation mode to detect structural correctness issues in the actual changed scope.").
		Requirement("Requires implementation evidence and the relevant target paths.").
		Satisfies("Produces type/static-analysis evidence for criteria evaluation and validation reporting.").
		Avoid("Do not use during pre-implementation contract synthesis.").
		ArrayParam("paths", "Paths to check (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := runTypeCheckerAnalysis(ctx, runner, paths)
			logAnalysisFindings(ctx, "run_type_checker", issues)
			return analysisResult("run_type_checker", issues), nil
		}).
		Build()
}

// RunFormatterCheckSkill returns a skill that checks formatting using the target language rules.
func RunFormatterCheckSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_formatter_check").
		Description("Check code formatting without modifying files, using the requested language's formatting rules.").
		Domain("analysis").
		Keywords("format", "style", "whitespace").
		Priority(90).
		Usage("Use in implementation-validation mode when formatting compliance is part of the quality bar or when findings need to distinguish formatting drift from real logic defects.").
		Satisfies("Produces formatting findings that contribute to quality judgment without mutating the workspace.").
		Avoid("Do not treat formatting-only findings as a substitute for type, security, or criteria validation.").
		ArrayParam("paths", "Paths to check (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := runFormatterAnalysis(ctx, runner, paths)
			logAnalysisFindings(ctx, "run_formatter_check", issues)
			return analysisResult("run_formatter_check", issues), nil
		}).
		Build()
}

// RunSecurityScanSkill returns a skill that runs a language-appropriate security analysis pass.
func RunSecurityScanSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_security_scan").
		Description("Run a language-appropriate security analysis pass to detect likely vulnerabilities.").
		Domain("analysis").
		Keywords("security", "vulnerability", "audit").
		Priority(95).
		Usage("Use during implementation-validation mode when security-sensitive behavior exists or when the quality bar requires security validation.").
		Requirement("Requires implementation evidence and the relevant target paths.").
		Satisfies("Produces security findings that directly affect validation outcome and severity reporting.").
		Avoid("Do not use during pre-implementation contract synthesis.").
		ArrayParam("paths", "Paths to scan (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := runSecurityAnalysis(ctx, runner, paths)
			criticalCount := countBySeverity(issues, Critical)
			highCount := countBySeverity(issues, High)
			result := analysisResult("run_security_scan", issues)
			result["critical_count"] = criticalCount
			result["high_count"] = highCount
			logAnalysisFindings(ctx, "run_security_scan", issues)
			return result, nil
		}).
		Build()
}

// CheckCoverageSkill returns a skill that analyzes test coverage.
func CheckCoverageSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("check_coverage").
		Description("Analyze test coverage and flag functions below the requested threshold when a compatible coverage runner exists.").
		Domain("analysis").
		Keywords("coverage", "test", "threshold").
		Priority(85).
		Usage("Use in implementation-validation mode when the task or quality gates require coverage evidence.").
		Satisfies("Produces coverage evidence for quality gates and final grading.").
		Avoid("Do not manufacture blocking findings when the project lacks a compatible coverage runner; report tool availability honestly.").
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
			issues := runCoverageAnalysis(ctx, runner, params.Paths, params.Threshold)
			logAnalysisFindings(ctx, "check_coverage", issues)
			return analysisResult("check_coverage", issues), nil
		}).
		Build()
}

// AnalyzeComplexitySkill returns a skill that checks language-appropriate complexity limits.
func AnalyzeComplexitySkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("analyze_complexity").
		Description("Analyze cyclomatic and cognitive complexity of functions using the requested language rules.").
		Domain("analysis").
		Keywords("complexity", "cyclomatic", "cognitive").
		Priority(85).
		Usage("Use in implementation-validation mode when readability, maintainability, or explicit complexity gates matter for the requested scope.").
		Satisfies("Produces complexity findings that contribute to quality judgment and review artifacts.").
		Avoid("Do not use as the first validation step when correctness or security evidence is still missing.").
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
			issues := runComplexityAnalysis(ctx, runner, params.Paths, params.MaxCyclomatic, params.MaxCognitive)
			logAnalysisFindings(ctx, "analyze_complexity", issues)
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
		Usage("Use in implementation-validation mode when concurrency behavior is present or when a task explicitly risks races.").
		Requirement("Requires runnable implementation/test evidence for the target scope.").
		Satisfies("Produces concurrency-safety evidence for validation findings and severity reporting.").
		Avoid("Do not use during pre-implementation inspection or on obviously non-runnable placeholder code.").
		ArrayParam("paths", "Paths to test (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunRaceDetector(ctx, runner, paths)
			logAnalysisFindings(ctx, "detect_race_conditions", issues)
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
		Usage("Use in implementation-validation mode when the target code includes locks, shared state, or orchestration paths that could block.").
		Satisfies("Produces deadlock-risk findings for validation and grading.").
		Avoid("Do not use during pre-implementation synthesis or as a replacement for broader criteria validation.").
		ArrayParam("paths", "Paths to analyze (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunDeadlockDetector(ctx, runner, paths)
			logAnalysisFindings(ctx, "detect_deadlocks", issues)
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
		Usage("Use in implementation-validation mode when the task risks unbounded growth, retention bugs, or performance regressions.").
		Satisfies("Produces memory-behavior evidence for validation findings and quality grading.").
		Avoid("Do not use during pre-implementation synthesis or as a stand-in for correctness validation.").
		ArrayParam("paths", "Paths to analyze (default: ./...)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := RunMemoryAnalyzer(ctx, runner, paths)
			logAnalysisFindings(ctx, "detect_memory_leaks", issues)
			return analysisResult("detect_memory_leaks", issues), nil
		}).
		Build()
}

// ReadFileSkill returns a read-only file reading skill that routes through
// the FileAccess returned by getFA at invocation time. Pipeline agents pass
// a closure over their fileAccess field so reads see staged content; global
// agents may pass nil to fall back to direct disk I/O.
func ReadFileSkill(getFA FileAccessFunc) *skills.Skill {
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

			var data []byte
			var err error
			if fa := getFA(); fa != nil {
				data, err = fa.ReadFile(ctx, params.Path)
			} else {
				data, err = os.ReadFile(params.Path)
			}
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

// GlobSkill returns a read-only file glob skill that routes through the
// FileAccess returned by getFA at invocation time.
func GlobSkill(getFA FileAccessFunc) *skills.Skill {
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

			if fa := getFA(); fa != nil {
				matches, err := fa.Glob(ctx, params.Dir, params.Pattern, nil)
				if err != nil {
					return nil, fmt.Errorf("glob %s: %w", params.Dir, err)
				}
				return map[string]any{
					"pattern": params.Pattern,
					"matches": matches,
					"count":   len(matches),
				}, nil
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

// GrepSkill returns a read-only text search skill that routes through the
// FileAccess returned by getFA at invocation time.
func GrepSkill(getFA FileAccessFunc) *skills.Skill {
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

			const maxMatches = 100

			if fa := getFA(); fa != nil {
				faMatches, err := fa.Grep(ctx, params.Path, params.Pattern, params.Glob, 0, maxMatches)
				if err != nil {
					return nil, fmt.Errorf("search %s: %w", params.Path, err)
				}
				return map[string]any{
					"pattern": params.Pattern,
					"matches": faMatches,
					"count":   len(faMatches),
				}, nil
			}

			type match struct {
				File string `json:"file"`
				Line int    `json:"line"`
				Text string `json:"text"`
			}
			var results []match

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

func logAnalysisFindings(ctx context.Context, tool string, issues []ValidationIssue) {
	if !HasBlockingIssues(issues) {
		return
	}
	lm := agentShared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	blocking := 0
	for _, issue := range issues {
		if issue.Severity == Critical || issue.Severity == High {
			blocking++
		}
	}
	agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventAuditFinding,
		lm.AgentID, lm.SessionID, lm.CorrID, "warn",
		&agentlog.AuditPayload{Phase: tool, Finding: fmt.Sprintf("%d blocking issues", blocking)})
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
