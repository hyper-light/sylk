package engineer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
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
)

// AuditIssue describes a single quality issue found during self-audit.
type AuditIssue struct {
	Category    AuditCategory `json:"category"`
	Severity    string        `json:"severity"`
	Description string        `json:"description"`
	File        string        `json:"file,omitempty"`
	Suggestion  string        `json:"suggestion,omitempty"`
}

// AuditVerdict is the structured result of a self-audit pass.
type AuditVerdict struct {
	QualityScore float64      `json:"quality_score"`
	Pass         bool         `json:"pass"`
	Issues       []AuditIssue `json:"issues,omitempty"`
	Iteration    int          `json:"iteration"`
}

// selfAudit runs a single audit pass against the implementation result.
// It builds an audit prompt, calls the LLM, and parses the structured verdict.
func (e *Engineer) selfAudit(ctx context.Context, result, criteria string) (*AuditVerdict, error) {
	p := e.getProvider()
	if p == nil {
		return &AuditVerdict{QualityScore: 1.0, Pass: true}, nil
	}

	auditPrompt := buildAuditPrompt(result, criteria)
	req := &providers.Request{
		SystemPrompt: "You are a code quality auditor. Evaluate the implementation and return a JSON verdict.",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: auditPrompt},
		},
		Model:     e.config.EngineerConfig.Model,
		MaxTokens: 4096,
	}
	llmruntime.Apply(req, e.llmRuntimeProfile())

	resp, err := p.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("audit llm: %w", err)
	}

	return parseAuditVerdict(resp.Content)
}

// shouldReimplement determines whether the engineer should re-implement based
// on the audit verdict. Pure function — no side effects.
func shouldReimplement(verdict *AuditVerdict, iteration int, config AuditConfig) bool {
	if verdict == nil {
		return false
	}
	return !verdict.Pass && iteration < config.MaxAuditIterations
}

func buildAuditPrompt(result, criteria string) string {
	var b strings.Builder
	b.WriteString("Audit the following implementation for quality.\n\n")
	if criteria != "" {
		b.WriteString("Criteria:\n")
		b.WriteString(criteria)
		b.WriteString("\n\n")
	}
	b.WriteString("Implementation:\n")
	b.WriteString(result)
	b.WriteString("\n\nRespond with a JSON object:\n")
	b.WriteString(`{"quality_score": 0.0-1.0, "pass": true/false, "issues": [{"category": "readability|correctness|performance|maintainability", "severity": "low|medium|high", "description": "...", "file": "...", "suggestion": "..."}]}`)
	return b.String()
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
