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

// Phase 2.K / GI-C refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.A +
// §10.K.1 GI-4): run_linter + run_type_checker + run_formatter_check +
// run_security_scan + check_coverage + analyze_complexity +
// detect_race_conditions + detect_deadlocks + detect_memory_leaks all
// collapsed into run_analyzer(kind=…). One primitive, same underlying
// dispatchers, one enum parameter tells it which analysis to run.
const (
	analyzerKindLint        = "lint"
	analyzerKindTypecheck   = "typecheck"
	analyzerKindFormatCheck = "format_check"
	analyzerKindSecurity    = "security"
	analyzerKindCoverage    = "coverage"
	analyzerKindComplexity  = "complexity"
	analyzerKindRace        = "race"
	analyzerKindDeadlock    = "deadlock"
	analyzerKindMemoryLeak  = "memory_leak"
)

func analyzerKinds() []string {
	return []string{
		analyzerKindLint,
		analyzerKindTypecheck,
		analyzerKindFormatCheck,
		analyzerKindSecurity,
		analyzerKindCoverage,
		analyzerKindComplexity,
		analyzerKindRace,
		analyzerKindDeadlock,
		analyzerKindMemoryLeak,
	}
}

// RunAnalyzerSkill returns a single primitive that dispatches to the
// language-appropriate linter, type checker, formatter check, security
// scan, coverage runner, complexity analyzer, race detector, deadlock
// analyzer, or memory-leak analyzer based on the kind parameter.
func RunAnalyzerSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("run_analyzer").
		Description("Run a language-appropriate static analysis pass selected by kind. Collapses run_linter, run_type_checker, run_formatter_check, run_security_scan, check_coverage, analyze_complexity, detect_race_conditions, detect_deadlocks, and detect_memory_leaks into one primitive dispatched by the kind enum.").
		Domain("analysis").
		Keywords("lint", "type", "format", "security", "coverage", "complexity", "race", "deadlock", "memory", "analysis").
		Priority(95).
		Usage("Set kind=lint|typecheck|format_check|security|coverage|complexity|race|deadlock|memory_leak. Run on the concrete scope under review — not the whole repo by default. Use during implementation-validation mode after you understand the target files and criteria.").
		Requirement("Requires implementation evidence and the relevant target paths.").
		Satisfies("Produces analysis findings (type, security, coverage, complexity, concurrency, memory) for criteria evaluation and final grading.").
		Avoid("Do not use during pre-implementation contract synthesis when there is no implementation evidence to validate.").
		EnumParam("kind", "Analyzer kind", analyzerKinds(), true).
		ArrayParam("paths", "Paths to analyze (default: ./...)", "string", false).
		IntParam("threshold", "Coverage threshold percentage (kind=coverage; default: 80)", false).
		IntParam("max_cyclomatic", "Max cyclomatic complexity (kind=complexity; default: 4 per CLAUDE.md)", false).
		IntParam("max_cognitive", "Max cognitive complexity (kind=complexity; default: 8)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Kind          string   `json:"kind"`
				Paths         []string `json:"paths"`
				Threshold     float64  `json:"threshold"`
				MaxCyclomatic int      `json:"max_cyclomatic"`
				MaxCognitive  int      `json:"max_cognitive"`
			}
			_ = json.Unmarshal(input, &params)
			kind := strings.ToLower(strings.TrimSpace(params.Kind))
			if kind == "" {
				return nil, fmt.Errorf("kind is required (expected one of: lint, typecheck, format_check, security, coverage, complexity, race, deadlock, memory_leak)")
			}
			paths := params.Paths
			if len(paths) == 0 {
				paths = []string{"./..."}
			}

			var (
				issues []ValidationIssue
				label  string
			)
			switch kind {
			case analyzerKindLint:
				label = "run_linter"
				issues = runLinterAnalysis(ctx, runner, paths)
			case analyzerKindTypecheck:
				label = "run_type_checker"
				issues = runTypeCheckerAnalysis(ctx, runner, paths)
			case analyzerKindFormatCheck:
				label = "run_formatter_check"
				issues = runFormatterAnalysis(ctx, runner, paths)
			case analyzerKindSecurity:
				label = "run_security_scan"
				issues = runSecurityAnalysis(ctx, runner, paths)
				result := analysisResult(label, issues)
				result["kind"] = kind
				result["critical_count"] = countBySeverity(issues, Critical)
				result["high_count"] = countBySeverity(issues, High)
				logAnalysisFindings(ctx, label, issues)
				return result, nil
			case analyzerKindCoverage:
				label = "check_coverage"
				issues = runCoverageAnalysis(ctx, runner, paths, params.Threshold)
			case analyzerKindComplexity:
				label = "analyze_complexity"
				issues = runComplexityAnalysis(ctx, runner, paths, params.MaxCyclomatic, params.MaxCognitive)
			case analyzerKindRace:
				label = "detect_race_conditions"
				issues = RunRaceDetector(ctx, runner, paths)
			case analyzerKindDeadlock:
				label = "detect_deadlocks"
				issues = RunDeadlockDetector(ctx, runner, paths)
			case analyzerKindMemoryLeak:
				label = "detect_memory_leaks"
				issues = RunMemoryAnalyzer(ctx, runner, paths)
			default:
				return nil, fmt.Errorf("unknown kind: %q (expected one of: lint, typecheck, format_check, security, coverage, complexity, race, deadlock, memory_leak)", params.Kind)
			}
			logAnalysisFindings(ctx, label, issues)
			result := analysisResult(label, issues)
			result["kind"] = kind
			return result, nil
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

			var fa versioning.FileAccess
			if resolved := getFA(); resolved != nil {
				fa = resolved
			} else {
				fa = versioning.NewDiskFileAccess("", true)
			}
			result, err := versioning.ReadFileToolResult(ctx, fa, params.Path, 0, 0)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", params.Path, err)
			}
			return result, nil
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
