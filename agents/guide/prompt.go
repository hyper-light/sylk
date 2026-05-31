package guide

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	ClassificationSystemPrompt     = prompts.MustLoad("guide", "classification")
	ClassificationExamplesTemplate = prompts.MustLoad("guide", "classification_examples")
	ClassificationDomainPrompt     = prompts.MustLoad("guide", "domain")
	GuideSystemBasePrompt          = prompts.MustLoad("guide", "system")
	GuideClaimsNativePrompt        = prompts.MustLoad("shared", "claims_native")
	GuideSystemPrompt              = strings.Join([]string{GuideSystemBasePrompt, GuideClaimsNativePrompt}, "\n\n---\n\n")
	HelpDSLSyntax                  = prompts.MustLoad("guide", "help_dsl")
	HelpAgents                     = prompts.MustLoad("guide", "help_agents")
)

// ClassificationPromptRuntime carries optional session-scoped context for LLM routing.
type ClassificationPromptRuntime struct {
	SessionID                string
	ActiveConversationAgent  string
	ActiveConversationTurns  int
	ActiveConversationAgeSec int
	ActiveConversationScore  float64
	PendingPlanID            string
	PendingPlanAgent         string
	AgentCapabilitySummary   string
}

// BuildClassificationPrompt composes prompt modules based on request content.
func BuildClassificationPrompt(input string) string {
	return BuildClassificationPromptWithRuntime(input, nil)
}

// BuildClassificationPromptWithRuntime composes prompt modules plus runtime context.
func BuildClassificationPromptWithRuntime(input string, runtime *ClassificationPromptRuntime) string {
	sections := []string{ClassificationSystemPrompt}
	if shouldIncludeClassificationDomainModule(input) {
		sections = append(sections, ClassificationDomainPrompt)
	}
	if runtimeSection := classificationRuntimeSection(runtime); runtimeSection != "" {
		sections = append(sections, runtimeSection)
	}
	return strings.Join(sections, "\n\n")
}

// FormatClassificationPrompt formats the classification prompt with optional corrections
func FormatClassificationPrompt(corrections string) string {
	return FormatClassificationPromptWithRuntime(corrections, nil)
}

// FormatClassificationPromptWithRuntime formats prompt with optional corrections/runtime context.
func FormatClassificationPromptWithRuntime(corrections string, runtime *ClassificationPromptRuntime) string {
	base := BuildClassificationPromptWithRuntime("", runtime)
	if corrections == "" {
		return base
	}
	return base + "\n" + fmt.Sprintf(ClassificationExamplesTemplate, corrections)
}

func shouldIncludeClassificationDomainModule(input string) bool {
	query := strings.ToLower(strings.TrimSpace(input))
	if query == "" {
		return true
	}
	keywords := []string{"domain", "route", "agent", "classify", "taxonomy"}
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}

func classificationRuntimeSection(runtime *ClassificationPromptRuntime) string {
	if runtime == nil {
		return ""
	}
	sessionID := strings.TrimSpace(runtime.SessionID)
	activeAgent := strings.TrimSpace(runtime.ActiveConversationAgent)
	capabilitySummary := strings.TrimSpace(runtime.AgentCapabilitySummary)
	hasConversationContext := sessionID != "" || activeAgent != ""
	if !hasConversationContext && capabilitySummary == "" && pendingPlanRuntimeSection(runtime) == "" {
		return ""
	}
	ageSec := runtime.ActiveConversationAgeSec
	if ageSec < 0 {
		ageSec = 0
	}
	turns := runtime.ActiveConversationTurns
	if turns < 0 {
		turns = 0
	}
	score := runtime.ActiveConversationScore
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	sections := make([]string, 0, 3)
	if hasConversationContext {
		lines := []string{
			"## Runtime Conversation Context",
			"Use this as a tie-breaker for ambiguous follow-up prompts only.",
		}
		if sessionID != "" {
			lines = append(lines, "- session_id: "+sessionID)
		}
		if activeAgent != "" {
			lines = append(lines, "- active_conversation_agent: "+activeAgent)
		}
		if turns > 0 {
			lines = append(lines, fmt.Sprintf("- active_conversation_turns: %d", turns))
		}
		if ageSec > 0 {
			lines = append(lines, fmt.Sprintf("- active_conversation_age_seconds: %d", ageSec))
		}
		if score > 0 {
			lines = append(lines, fmt.Sprintf("- active_conversation_score: %.2f", score))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if pendingSection := pendingPlanRuntimeSection(runtime); pendingSection != "" {
		sections = append(sections, pendingSection)
	}
	if capabilitySummary != "" {
		sections = append(sections, capabilitySummary)
	}
	return strings.Join(sections, "\n\n")
}

func pendingPlanRuntimeSection(runtime *ClassificationPromptRuntime) string {
	if runtime == nil {
		return ""
	}
	planID := strings.TrimSpace(runtime.PendingPlanID)
	planAgent := strings.TrimSpace(runtime.PendingPlanAgent)
	if planID == "" && planAgent == "" {
		return ""
	}
	lines := []string{
		"## Pending Plan Approval",
		"A plan from the architect is waiting for user approval.",
	}
	if planID != "" {
		lines = append(lines, "- pending_plan_id: "+planID)
	}
	if planAgent != "" {
		lines = append(lines, "- pending_plan_agent: "+planAgent)
	}
	lines = append(lines,
		`Any approval or execution phrase ("yes", "ok", "sure", "lgtm", "go ahead",`,
		`"hand it off", "ship it", "do it", "execute", "proceed", "approved", "let's go")`,
		`should be classified as intent=execute, domain=tasks, target_agent=architect`,
		`when a plan is pending. Do NOT classify these as intent=plan — they are approvals.`,
	)
	return strings.Join(lines, "\n")
}
