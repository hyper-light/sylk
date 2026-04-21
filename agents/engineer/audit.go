package engineer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/adalundhe/sylk/core/format"
	"github.com/adalundhe/sylk/core/providers"
)

// AuditConfig configures the bounded self-audit loop.
type AuditConfig struct {
	// MaxAuditIterations bounds re-audit cycles (default 3).
	MaxAuditIterations int `json:"max_audit_iterations"`
	// MinQualityScore is the threshold for passing audit (default 0.7).
	MinQualityScore float64 `json:"min_quality_score"`
}

// DefaultAuditConfig returns the default audit configuration.
func DefaultAuditConfig() AuditConfig {
	return AuditConfig{
		MaxAuditIterations: 3,
		MinQualityScore:    0.7,
	}
}

// AuditCategory classifies the type of audit issue.
type AuditCategory string

const (
	AuditReadability     AuditCategory = "readability"
	AuditCorrectness     AuditCategory = "correctness"
	AuditPerformance     AuditCategory = "performance"
	AuditMaintainability AuditCategory = "maintainability"
	AuditFormat          AuditCategory = "format"
	AuditLint            AuditCategory = "lint"
)

// AuditIssue describes a single quality issue found during self-audit.
// Issues from deterministic tools (format, lint) carry Source="tool"
// and Tool=<tool-id>; reflective issues carry Source="reflection".
type AuditIssue struct {
	Category    AuditCategory `json:"category"`
	Severity    string        `json:"severity"`
	Description string        `json:"description"`
	File        string        `json:"file,omitempty"`
	Suggestion  string        `json:"suggestion,omitempty"`
	Source      string        `json:"source,omitempty"`
	Tool        string        `json:"tool,omitempty"`
}

// AuditToolFinding captures the raw per-tool outcome so the caller
// can see which tools ran, which file each targeted, and the full
// output. Separate from AuditIssue so the pass/fail signal stays
// atomic while the underlying evidence remains inspectable.
type AuditToolFinding struct {
	Tool    string `json:"tool"`    // "format" | "lint"
	File    string `json:"file,omitempty"`
	Backend string `json:"backend,omitempty"` // concrete formatter/linter id
	Clean   bool   `json:"clean"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// AuditVerdict is the structured result of a self-audit pass.
// The pass signal combines deterministic tool evidence (format-check,
// lint) with the LLM's reflective analysis so a green audit requires
// both "tools see no issues" and "reasoning sees no issues." Callers
// can inspect ToolFindings to see the exact evidence.
type AuditVerdict struct {
	QualityScore float64            `json:"quality_score"`
	Pass         bool               `json:"pass"`
	Issues       []AuditIssue       `json:"issues,omitempty"`
	ToolFindings []AuditToolFinding `json:"tool_findings,omitempty"`
	Reflection   string             `json:"reflection,omitempty"`
	Iteration    int                `json:"iteration"`
}

// selfAudit runs the full grounded audit: format check + lint on each
// file, followed by a reflective LLM pass that interprets the tool
// output against the provided criteria. The tool pass is authoritative
// for deterministic issues (style violations, lint warnings, type
// errors); the reflective pass catches things the tools cannot see
// (missed edge cases, hidden invariants, scope creep).
//
// Either layer failing contributes to Pass=false. A deterministic
// failure reduces QualityScore proportionally; a reflective
// low-confidence score dominates only when the tools are clean.
func (e *Engineer) selfAudit(ctx context.Context, files []string, implementation, criteria string) (*AuditVerdict, error) {
	verdict := &AuditVerdict{
		QualityScore: 1.0,
		Pass:         true,
	}

	// --- Tool layer -------------------------------------------------

	files = normalizeAuditFiles(files)
	toolFindings, toolIssues := e.runAuditTools(ctx, files)
	verdict.ToolFindings = toolFindings
	verdict.Issues = append(verdict.Issues, toolIssues...)

	// --- Reflective layer ------------------------------------------

	p := e.getProvider()
	if p != nil {
		reflection, err := e.runReflectiveAudit(ctx, p, files, implementation, criteria, toolFindings)
		if err != nil {
			// Reflection failure is non-fatal — we still have tool
			// signal. Record the error on the verdict so the caller
			// knows reflection was skipped.
			verdict.Reflection = fmt.Sprintf("reflection unavailable: %s", err.Error())
		} else if reflection != nil {
			verdict.Reflection = reflection.Reflection
			verdict.Issues = append(verdict.Issues, reflection.Issues...)
			// Reflection's quality score is folded in multiplicatively
			// so either layer can fail the audit.
			verdict.QualityScore *= reflection.QualityScore
			if !reflection.Pass {
				verdict.Pass = false
			}
		}
	}

	// --- Tool-grounded score adjustment ----------------------------

	// Every failed tool finding drops QualityScore. A single high-
	// severity issue (e.g., lint error) is enough to fail the audit
	// even if reflection is clean. This is the "grounded" half of
	// Option B — we don't trust self-reported quality alone.
	for _, finding := range toolFindings {
		if finding.Clean {
			continue
		}
		verdict.Pass = false
		verdict.QualityScore *= 0.7
	}

	// Clamp and return.
	if verdict.QualityScore < 0 {
		verdict.QualityScore = 0
	}
	if verdict.QualityScore > 1 {
		verdict.QualityScore = 1
	}
	return verdict, nil
}

// runAuditTools runs format-check + lint on each file and returns
// the raw findings plus a flattened issue list. Errors surface as
// non-clean findings rather than returning early — a broken linter
// shouldn't halt the audit.
func (e *Engineer) runAuditTools(ctx context.Context, files []string) ([]AuditToolFinding, []AuditIssue) {
	if len(files) == 0 {
		return nil, nil
	}
	findings := make([]AuditToolFinding, 0, 2*len(files))
	issues := make([]AuditIssue, 0, 2*len(files))

	formatSelector := format.NewFormatterSelector()
	root := e.effectiveWorkingDirectory()

	for _, file := range files {
		finding, issue := e.runAuditFormatCheck(ctx, formatSelector, root, file)
		findings = append(findings, finding)
		if issue != nil {
			issues = append(issues, *issue)
		}
	}

	for _, file := range files {
		finding, issue := e.runAuditLintCheck(ctx, root, file)
		findings = append(findings, finding)
		if issue != nil {
			issues = append(issues, *issue)
		}
	}

	return findings, issues
}

// runAuditFormatCheck runs a format check on one file using the
// project's detected formatter. A missing formatter is not a
// failure (Clean=true, Backend="none").
func (e *Engineer) runAuditFormatCheck(ctx context.Context, selector *format.FormatterSelector, root, file string) (AuditToolFinding, *AuditIssue) {
	f := selector.SelectFormatter(root, file)
	if f == nil {
		return AuditToolFinding{Tool: "format", File: file, Backend: "none", Clean: true}, nil
	}
	checkArgs, ok := formatterCheckArgs[f.ID]
	if !ok {
		return AuditToolFinding{Tool: "format", File: file, Backend: string(f.ID), Clean: true}, nil
	}
	fullPath := resolvePath(root, file)
	args := substituteFileArg(checkArgs, fullPath)
	output, err := e.runToolInDir(ctx, root, false, f.Command, args...)
	if err != nil {
		return AuditToolFinding{
				Tool: "format", File: file, Backend: string(f.ID),
				Clean: false, Error: err.Error(),
			},
			&AuditIssue{
				Category:    AuditFormat,
				Severity:    "medium",
				Description: fmt.Sprintf("format check failed: %s", err.Error()),
				File:        file,
				Source:      "tool",
				Tool:        string(f.ID),
				Suggestion:  fmt.Sprintf("run `format` with action=apply on %s", file),
			}
	}
	clean := strings.TrimSpace(output) == ""
	finding := AuditToolFinding{
		Tool:    "format",
		File:    file,
		Backend: string(f.ID),
		Clean:   clean,
		Output:  output,
	}
	if clean {
		return finding, nil
	}
	return finding, &AuditIssue{
		Category:    AuditFormat,
		Severity:    "low",
		Description: fmt.Sprintf("formatting differs from project style (%s)", f.ID),
		File:        file,
		Source:      "tool",
		Tool:        string(f.ID),
		Suggestion:  fmt.Sprintf("run `format` with action=apply on %s", file),
	}
}

// runAuditLintCheck runs the detected linter on one file. No fix
// mode here — the audit observes, it does not mutate.
func (e *Engineer) runAuditLintCheck(ctx context.Context, root, file string) (AuditToolFinding, *AuditIssue) {
	linter := selectLinter(root, []string{file})
	if linter == nil {
		return AuditToolFinding{Tool: "lint", File: file, Backend: "none", Clean: true}, nil
	}
	args := append([]string(nil), linter.args...)
	args = append(args, file)
	output, err := e.runToolInDir(ctx, root, false, linter.command, args...)
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			// Non-zero exit from a linter is the normal "issues found"
			// path — extract the output and treat as a lint issue.
			parts := strings.SplitN(err.Error(), "\n", 2)
			if len(parts) > 1 {
				output = parts[1]
			}
			return AuditToolFinding{
					Tool: "lint", File: file, Backend: linter.id,
					Clean: false, Output: output,
				},
				&AuditIssue{
					Category:    AuditLint,
					Severity:    "medium",
					Description: fmt.Sprintf("lint issues in %s", file),
					File:        file,
					Source:      "tool",
					Tool:        linter.id,
					Suggestion:  "address the lint output; re-run `audit` to re-check",
				}
		}
		return AuditToolFinding{
				Tool: "lint", File: file, Backend: linter.id,
				Clean: false, Error: err.Error(),
			},
			&AuditIssue{
				Category:    AuditLint,
				Severity:    "medium",
				Description: fmt.Sprintf("lint failed to run: %s", err.Error()),
				File:        file,
				Source:      "tool",
				Tool:        linter.id,
			}
	}
	clean := strings.TrimSpace(output) == ""
	finding := AuditToolFinding{
		Tool:    "lint",
		File:    file,
		Backend: linter.id,
		Clean:   clean,
		Output:  output,
	}
	if clean {
		return finding, nil
	}
	return finding, &AuditIssue{
		Category:    AuditLint,
		Severity:    "low",
		Description: fmt.Sprintf("lint findings in %s", file),
		File:        file,
		Source:      "tool",
		Tool:        linter.id,
		Suggestion:  "address the lint output; re-run `audit` to re-check",
	}
}

// runReflectiveAudit runs the LLM pass that interprets the
// implementation + tool findings against criteria. The tool findings
// are part of the prompt so the model doesn't duplicate issues the
// deterministic tools already caught; its job is to surface things
// the tools cannot see — missed edge cases, unhandled invariants,
// scope creep, subtle correctness defects.
func (e *Engineer) runReflectiveAudit(ctx context.Context, p engineerProvider, files []string, implementation, criteria string, toolFindings []AuditToolFinding) (*AuditVerdict, error) {
	prompt := buildAuditPrompt(files, implementation, criteria, toolFindings)
	req := &providers.Request{
		SystemPrompt: auditSystemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
		Model:     e.config.EngineerConfig.Model,
		MaxTokens: 4096,
	}
	e.applyLLMRuntimeProfile(req, "audit")

	resp, err := p.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("audit llm: %w", err)
	}
	v, err := parseAuditVerdict(resp.Content)
	if err != nil {
		return nil, err
	}
	// Mark reflective issues so callers can tell them apart from tool
	// issues when the two lists are merged.
	for i := range v.Issues {
		if v.Issues[i].Source == "" {
			v.Issues[i].Source = "reflection"
		}
	}
	return v, nil
}

// shouldReimplement determines whether the engineer should re-implement based
// on the audit verdict. Pure function — no side effects.
func shouldReimplement(verdict *AuditVerdict, iteration int, config AuditConfig) bool {
	if verdict == nil {
		return false
	}
	return !verdict.Pass && iteration < config.MaxAuditIterations
}

// normalizeAuditFiles trims whitespace, filters empties, and de-dups
// the input list so the caller can pass loose input without
// duplicating tool work.
func normalizeAuditFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

const auditSystemPrompt = `You are a code quality auditor. The user will provide the implementation under review, the acceptance criteria, and the results from deterministic tools (formatter, linter) already run against the files. Your job is to catch issues the deterministic tools cannot see — missed edge cases, unhandled invariants, scope creep, subtle correctness defects — and to interpret the tool output in context. Do not re-report issues the tools already surfaced. Return a JSON verdict.`

func buildAuditPrompt(files []string, implementation, criteria string, toolFindings []AuditToolFinding) string {
	var b strings.Builder
	b.WriteString("Audit the following implementation for quality.\n\n")
	if len(files) > 0 {
		b.WriteString("Files under audit:\n")
		for _, f := range files {
			b.WriteString("- ")
			b.WriteString(f)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if criteria != "" {
		b.WriteString("Criteria:\n")
		b.WriteString(criteria)
		b.WriteString("\n\n")
	}
	if len(toolFindings) > 0 {
		b.WriteString("Deterministic tool findings (already run — do not re-report):\n")
		for _, f := range toolFindings {
			status := "clean"
			if !f.Clean {
				status = "issues"
			}
			fmt.Fprintf(&b, "- %s[%s] %s: %s", f.Tool, f.Backend, f.File, status)
			if trimmed := strings.TrimSpace(f.Output); trimmed != "" {
				fmt.Fprintf(&b, "\n  output: %s", truncateForPrompt(trimmed, 800))
			}
			if trimmed := strings.TrimSpace(f.Error); trimmed != "" {
				fmt.Fprintf(&b, "\n  error: %s", truncateForPrompt(trimmed, 400))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if implementation != "" {
		b.WriteString("Implementation:\n")
		b.WriteString(implementation)
		b.WriteString("\n\n")
	}
	b.WriteString("Respond with a JSON object. Focus your reflection on issues the tools could not catch:\n")
	b.WriteString(`{"quality_score": 0.0-1.0, "pass": true/false, "reflection": "short narrative — what the tool findings mean in context and what the tools could not see", "issues": [{"category": "readability|correctness|performance|maintainability", "severity": "low|medium|high", "description": "...", "file": "...", "suggestion": "..."}]}`)
	return b.String()
}

func truncateForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 8 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func parseAuditVerdict(content string) (*AuditVerdict, error) {
	content = strings.TrimSpace(content)

	// Try to extract JSON from markdown code blocks if present
	if idx := strings.Index(content, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(content[start:], "```"); end >= 0 {
			content = strings.TrimSpace(content[start : start+end])
		}
	} else if idx := strings.Index(content, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(content[start:], "```"); end >= 0 {
			content = strings.TrimSpace(content[start : start+end])
		}
	}

	// Find the first '{' to handle any leading text
	if idx := strings.Index(content, "{"); idx > 0 {
		content = content[idx:]
	}

	var verdict AuditVerdict
	if err := json.Unmarshal([]byte(content), &verdict); err != nil {
		return nil, fmt.Errorf("parse audit verdict: %w", err)
	}
	return &verdict, nil
}
