package shared

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var pythonFileExtensions = map[string]struct{}{
	".py":  {},
	".pyi": {},
}

var pythonSecurityRules = []pythonPatternRule{
	{
		pattern:  regexp.MustCompile(`\beval\s*\(`),
		severity: Critical,
		ruleID:   "python-security-eval",
		message:  "Avoid eval on untrusted input",
	},
	{
		pattern:  regexp.MustCompile(`\bexec\s*\(`),
		severity: Critical,
		ruleID:   "python-security-exec",
		message:  "Avoid exec on untrusted input",
	},
	{
		pattern:  regexp.MustCompile(`\bos\.system\s*\(`),
		severity: High,
		ruleID:   "python-security-os-system",
		message:  "Avoid os.system; prefer subprocess.run with explicit arguments",
	},
	{
		pattern:  regexp.MustCompile(`\bpickle\.(load|loads)\s*\(`),
		severity: Critical,
		ruleID:   "python-security-pickle",
		message:  "Avoid pickle deserialization for untrusted data",
	},
	{
		pattern:  regexp.MustCompile(`\bsubprocess\.[A-Za-z_]+\s*\(.*shell\s*=\s*True`),
		severity: Critical,
		ruleID:   "python-security-subprocess-shell",
		message:  "Avoid subprocess calls with shell=True",
	},
	{
		pattern:  regexp.MustCompile(`\bhashlib\.(md5|sha1)\s*\(`),
		severity: Medium,
		ruleID:   "python-security-weak-hash",
		message:  "Weak hash function detected; prefer a stronger digest",
	},
}

type pythonPatternRule struct {
	pattern  *regexp.Regexp
	severity Severity
	ruleID   string
	message  string
}

type pythonFunctionMetrics struct {
	Name          string
	File          string
	Line          int
	Column        int
	BaseIndent    int
	Cyclomatic    int
	Cognitive     int
	LastLineIndex int
}

func RunPythonLinter(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	files, issues := pythonAnalysisInputs(ctx, runner, "python_lint", paths)
	for _, file := range files {
		content, err := readAnalysisFile(ctx, runner, file)
		if err != nil {
			issues = append(issues, pythonReadFailureIssue("python-lint", file, err))
			continue
		}
		issues = append(issues, pythonWhitespaceIssues(file, string(content), "python-lint")...)
		issues = append(issues, pythonBareExceptIssues(file, string(content))...)
	}
	return DeduplicateIssues(issues)
}

func RunPythonTypeChecker(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	files, issues := pythonAnalysisInputs(ctx, runner, "python_type_checker", paths)
	if len(files) > 0 {
		issues = append(issues, informationalToolIssues("python_type_checker", "No dedicated Python static type checker is configured; blocking type-check failures are not inferred from heuristics alone")...)
	}
	return DeduplicateIssues(issues)
}

func RunPythonFormatterCheck(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	files, issues := pythonAnalysisInputs(ctx, runner, "python_formatter", paths)
	for _, file := range files {
		content, err := readAnalysisFile(ctx, runner, file)
		if err != nil {
			issues = append(issues, pythonReadFailureIssue("python-formatter", file, err))
			continue
		}
		issues = append(issues, pythonWhitespaceIssues(file, string(content), "python-format")...)
	}
	return DeduplicateIssues(issues)
}

func RunPythonSecurityScan(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	files, issues := pythonAnalysisInputs(ctx, runner, "python_security", paths)
	for _, file := range files {
		content, err := readAnalysisFile(ctx, runner, file)
		if err != nil {
			issues = append(issues, pythonReadFailureIssue("python-security", file, err))
			continue
		}
		issues = append(issues, pythonPatternIssues(file, string(content), pythonSecurityRules)...)
	}
	return DeduplicateIssues(issues)
}

func RunPythonCoverage(_ context.Context, _ *ToolRunner, _ []string, _ float64) []ValidationIssue {
	return informationalToolIssues("python_coverage", "No Python coverage runner is configured in the current execution environment")
}

func RunPythonComplexity(
	ctx context.Context,
	runner *ToolRunner,
	paths []string,
	maxCyclomatic int,
	maxCognitive int,
) []ValidationIssue {
	files, issues := pythonAnalysisInputs(ctx, runner, "python_complexity", paths)
	for _, file := range files {
		content, err := readAnalysisFile(ctx, runner, file)
		if err != nil {
			issues = append(issues, pythonReadFailureIssue("python-complexity", file, err))
			continue
		}
		issues = append(issues, pythonComplexityIssues(file, string(content), maxCyclomatic, maxCognitive)...)
	}
	return DeduplicateIssues(issues)
}

func pythonAnalysisInputs(
	ctx context.Context,
	runner *ToolRunner,
	tool string,
	paths []string,
) ([]string, []ValidationIssue) {
	files, err := collectAnalysisFiles(ctx, runner, paths, pythonFileExtensions)
	if err != nil {
		return nil, executionFailureIssues(tool, err)
	}
	if len(files) == 0 {
		return nil, informationalToolIssues(tool, "No Python files were found in the requested paths")
	}
	return files, nil
}

func readAnalysisFile(ctx context.Context, runner *ToolRunner, path string) ([]byte, error) {
	if fa := runner.currentFileAccess(); fa != nil {
		return fa.ReadFile(ctx, path)
	}
	if filepath.IsAbs(path) {
		return os.ReadFile(path)
	}
	return os.ReadFile(filepath.Join(runner.currentWorkingDir(), path))
}

func pythonReadFailureIssue(tool string, file string, err error) ValidationIssue {
	return ValidationIssue{
		ID:       strings.ReplaceAll(tool+"_"+file+"_read", string(filepath.Separator), "_"),
		Severity: Medium,
		File:     file,
		Line:     1,
		Column:   1,
		Message:  fmt.Sprintf("%s could not read %s: %v", tool, file, err),
		RuleID:   tool + "-read",
		Domain:   DomainCode,
	}
}

func pythonWhitespaceIssues(file string, content string, rulePrefix string) []ValidationIssue {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 {
		lines = []string{content}
	}
	issues := make([]ValidationIssue, 0, 4)
	for index, rawLine := range lines {
		lineNumber := index + 1
		line := strings.TrimRight(rawLine, "\r\n")
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			issues = append(issues, ValidationIssue{
				ID:       fmt.Sprintf("%s_trailing_whitespace_%s_%d", rulePrefix, sanitizeRuleSegment(file), lineNumber),
				Severity: Low,
				File:     file,
				Line:     lineNumber,
				Column:   len(line),
				Message:  "Trailing whitespace detected",
				RuleID:   rulePrefix + "-trailing-whitespace",
				Domain:   DomainCode,
			})
		}
		indentation := rawLine[:len(rawLine)-len(strings.TrimLeft(rawLine, " \t"))]
		if strings.Contains(indentation, "\t") {
			issues = append(issues, ValidationIssue{
				ID:       fmt.Sprintf("%s_tab_indentation_%s_%d", rulePrefix, sanitizeRuleSegment(file), lineNumber),
				Severity: Medium,
				File:     file,
				Line:     lineNumber,
				Column:   1,
				Message:  "Tab indentation detected; use spaces for Python indentation",
				RuleID:   rulePrefix + "-tab-indentation",
				Domain:   DomainCode,
			})
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("%s_missing_newline_%s", rulePrefix, sanitizeRuleSegment(file)),
			Severity: Low,
			File:     file,
			Line:     maxLineNumber(content),
			Column:   1,
			Message:  "File is missing a trailing newline",
			RuleID:   rulePrefix + "-final-newline",
			Domain:   DomainCode,
		})
	}
	return issues
}

func pythonBareExceptIssues(file string, content string) []ValidationIssue {
	lines := strings.Split(content, "\n")
	issues := make([]ValidationIssue, 0, 1)
	for index, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, "except:") || trimmed == "except" {
			issues = append(issues, ValidationIssue{
				ID:       fmt.Sprintf("python_lint_bare_except_%s_%d", sanitizeRuleSegment(file), index+1),
				Severity: High,
				File:     file,
				Line:     index + 1,
				Column:   1,
				Message:  "Bare except catches BaseException and hides unexpected failures",
				RuleID:   "python-lint-bare-except",
				Domain:   DomainCode,
			})
		}
	}
	return issues
}

func pythonPatternIssues(file string, content string, rules []pythonPatternRule) []ValidationIssue {
	lines := strings.Split(content, "\n")
	issues := make([]ValidationIssue, 0, len(rules))
	for lineIndex, rawLine := range lines {
		for _, rule := range rules {
			column := rule.pattern.FindStringIndex(rawLine)
			if column == nil {
				continue
			}
			issues = append(issues, ValidationIssue{
				ID:       fmt.Sprintf("%s_%s_%d", sanitizeRuleSegment(rule.ruleID), sanitizeRuleSegment(file), lineIndex+1),
				Severity: rule.severity,
				File:     file,
				Line:     lineIndex + 1,
				Column:   column[0] + 1,
				Message:  rule.message,
				RuleID:   rule.ruleID,
				Domain:   DomainCode,
			})
		}
	}
	return issues
}

func pythonComplexityIssues(
	file string,
	content string,
	maxCyclomatic int,
	maxCognitive int,
) []ValidationIssue {
	if maxCyclomatic <= 0 {
		maxCyclomatic = 4
	}
	if maxCognitive <= 0 {
		maxCognitive = 8
	}
	lines := strings.Split(content, "\n")
	issues := make([]ValidationIssue, 0, 2)
	var current *pythonFunctionMetrics
	for index, rawLine := range lines {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(rawLine)
		indent := indentationWidth(rawLine)
		if startsPythonFunction(trimmed) {
			issues = append(issues, finalizePythonComplexity(current, maxCyclomatic, maxCognitive)...)
			current = newPythonFunctionMetrics(file, trimmed, lineNumber, indent)
			continue
		}
		if current == nil || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indent <= current.BaseIndent && !strings.HasPrefix(trimmed, "@") {
			issues = append(issues, finalizePythonComplexity(current, maxCyclomatic, maxCognitive)...)
			current = nil
			if startsPythonFunction(trimmed) {
				current = newPythonFunctionMetrics(file, trimmed, lineNumber, indent)
			}
			continue
		}
		current.LastLineIndex = lineNumber
		current.Cyclomatic += branchComplexity(trimmed)
		current.Cognitive += branchCognitive(trimmed, indent-current.BaseIndent)
	}
	issues = append(issues, finalizePythonComplexity(current, maxCyclomatic, maxCognitive)...)
	return issues
}

func newPythonFunctionMetrics(file string, line string, lineNumber int, indent int) *pythonFunctionMetrics {
	return &pythonFunctionMetrics{
		Name:          pythonFunctionName(line),
		File:          file,
		Line:          lineNumber,
		Column:        indent + 1,
		BaseIndent:    indent,
		Cyclomatic:    1,
		Cognitive:     0,
		LastLineIndex: lineNumber,
	}
}

func finalizePythonComplexity(
	metrics *pythonFunctionMetrics,
	maxCyclomatic int,
	maxCognitive int,
) []ValidationIssue {
	if metrics == nil {
		return nil
	}
	issues := make([]ValidationIssue, 0, 2)
	if metrics.Cyclomatic > maxCyclomatic {
		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("python_complexity_cyclomatic_%s_%s_%d", sanitizeRuleSegment(metrics.File), sanitizeRuleSegment(metrics.Name), metrics.Line),
			Severity: High,
			File:     metrics.File,
			Line:     metrics.Line,
			Column:   metrics.Column,
			Message:  fmt.Sprintf("Function %s has cyclomatic complexity %d (max %d)", metrics.Name, metrics.Cyclomatic, maxCyclomatic),
			RuleID:   "python-complexity-cyclomatic",
			Domain:   DomainCode,
		})
	}
	if metrics.Cognitive > maxCognitive {
		issues = append(issues, ValidationIssue{
			ID:       fmt.Sprintf("python_complexity_cognitive_%s_%s_%d", sanitizeRuleSegment(metrics.File), sanitizeRuleSegment(metrics.Name), metrics.Line),
			Severity: Medium,
			File:     metrics.File,
			Line:     metrics.Line,
			Column:   metrics.Column,
			Message:  fmt.Sprintf("Function %s has cognitive complexity %d (max %d)", metrics.Name, metrics.Cognitive, maxCognitive),
			RuleID:   "python-complexity-cognitive",
			Domain:   DomainCode,
		})
	}
	return issues
}

func startsPythonFunction(trimmed string) bool {
	return strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ")
}

func pythonFunctionName(line string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "async "))
	trimmed = strings.TrimPrefix(trimmed, "def ")
	name, _, _ := strings.Cut(trimmed, "(")
	name = strings.TrimSpace(name)
	if name == "" {
		return "anonymous"
	}
	return name
}

func indentationWidth(line string) int {
	width := 0
	for _, ch := range line {
		switch ch {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

func branchComplexity(trimmed string) int {
	complexity := 0
	for _, prefix := range []string{"if ", "elif ", "for ", "async for ", "while ", "except", "case ", "with ", "async with "} {
		if strings.HasPrefix(trimmed, prefix) {
			complexity++
		}
	}
	complexity += strings.Count(trimmed, " and ")
	complexity += strings.Count(trimmed, " or ")
	return complexity
}

func branchCognitive(trimmed string, relativeIndent int) int {
	complexity := branchComplexity(trimmed)
	if complexity == 0 {
		return 0
	}
	nesting := 0
	if relativeIndent > 0 {
		nesting = relativeIndent / 4
	}
	return complexity * (1 + nesting)
}

func sanitizeRuleSegment(value string) string {
	replaced := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", "-", "_", ".", "_", ":", "_").Replace(strings.TrimSpace(value))
	if replaced == "" {
		return "value"
	}
	return replaced
}

func maxLineNumber(content string) int {
	count := strings.Count(content, "\n")
	if count == 0 {
		return 1
	}
	return count + 1
}
