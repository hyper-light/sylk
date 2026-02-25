package guide

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// RuleClassifierClient provides deterministic local classification as an
// offline fallback when no LLM API key is available. It uses keyword-based
// heuristics and should not be used as the primary classifier.
type RuleClassifierClient struct{}

// NewRuleClassifierClient returns a keyword-based classifier for offline use
// when no LLM API key is configured. The LLM classifier is the primary path;
// this exists only as a degraded-mode fallback.
func NewRuleClassifierClient() *RuleClassifierClient {
	return &RuleClassifierClient{}
}

type ruleClassification struct {
	IsRetrospective bool    `json:"is_retrospective"`
	Intent          string  `json:"intent"`
	Domain          string  `json:"domain"`
	TargetAgent     string  `json:"target_agent"`
	Confidence      float64 `json:"confidence"`
}

func (c *RuleClassifierClient) New(_ context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	query := extractClassifierInput(params.Messages)
	intent, domain, target, confidence := classifyByRules(query)
	payload, _ := json.Marshal(ruleClassification{
		IsRetrospective: false,
		Intent:          string(intent),
		Domain:          string(domain),
		TargetAgent:     target,
		Confidence:      confidence,
	})
	return &anthropic.Message{
		Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: string(payload)}},
		Role:       "assistant",
		StopReason: "end_turn",
	}, nil
}

func extractClassifierInput(messages []anthropic.MessageParam) string {
	if len(messages) == 0 {
		return ""
	}
	for idx := len(messages) - 1; idx >= 0; idx-- {
		msg := messages[idx]
		if msg.Role != anthropic.MessageParamRoleUser {
			continue
		}
		text := extractMessageText(msg)
		if text != "" {
			return text
		}
	}
	return extractMessageText(messages[len(messages)-1])
}

func extractMessageText(msg anthropic.MessageParam) string {
	var parts []string
	for _, block := range msg.Content {
		if block.OfText == nil {
			continue
		}
		text := strings.TrimSpace(block.OfText.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func classifyByRules(input string) (Intent, Domain, string, float64) {
	query := strings.ToLower(strings.TrimSpace(input))
	if query == "" {
		return IntentHelp, DomainGeneral, "guide", 0.7
	}
	if containsClassifierKeyword(query, "hello", "hi", "hey", "how are you", "thanks") {
		return IntentChat, DomainGeneral, "guide", 0.92
	}
	if containsClassifierKeyword(query, "agent", "registry", "status", "health", "guide", "sylk") {
		return IntentStatus, DomainSystem, "guide", 0.9
	}
	if containsClassifierKeyword(query, "go ahead", "execute", "hand off", "handoff",
		"ship it", "run the plan", "proceed with", "kick it off") {
		return IntentExecute, DomainPlanning, "architect", 0.90
	}
	if containsClassifierKeyword(query, "plan", "architecture", "break down", "decompose",
		"strategy", "workflow", "coordinate", "orchestrate") {
		return IntentPlan, DomainPlanning, "architect", 0.88
	}
	if containsClassifierKeyword(query, "design", "ui", "ux", "layout", "component",
		"visual", "style", "css", "mockup", "wireframe") {
		return IntentDesign, DomainDesign, "designer", 0.85
	}
	if containsClassifierKeyword(query, "implement", "build", "write", "refactor",
		"function", "class", "method", "fix bug", "add feature", "engineer") {
		return IntentDeclare, DomainLocal, "engineer", 0.85
	}
	if containsClassifierKeyword(query, "find", "search", "locate", "where", "file", "code") {
		return IntentFind, DomainLocal, "librarian", 0.85
	}
	if containsClassifierKeyword(query, "test", "qa", "coverage") {
		return IntentCheck, DomainTesting, "tester", 0.82
	}
	if containsClassifierKeyword(query, "review", "compliance", "requirement", "lint", "inspect") {
		return IntentCheck, DomainCompliance, "inspector", 0.82
	}
	if containsClassifierKeyword(query, "history", "pattern", "failure", "learning",
		"lesson", "what happened", "last time") {
		return IntentRecall, DomainHistory, "archivalist", 0.82
	}
	if containsClassifierKeyword(query, "research", "paper", "best practice", "documentation") {
		return IntentRecall, DomainResearch, "academic", 0.80
	}
	return IntentHelp, DomainGeneral, "guide", 0.75
}

func containsClassifierKeyword(query string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}
