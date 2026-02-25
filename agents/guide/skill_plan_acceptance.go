package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"

	promptskills "github.com/adalundhe/sylk/core/promptskills"
	diskskills "github.com/adalundhe/sylk/skills"
)

// planAcceptanceInput is the four-field payload the architect sends.
type planAcceptanceInput struct {
	Plan         string `json:"plan"`
	PlanID       string `json:"plan_id"`
	PlanName     string `json:"plan_name"`
	UserResponse string `json:"user_response"`
}

// skillInvocationPrefixes maps input prefixes to registered skill names.
// Table-driven so future skills can be added without code changes.
var skillInvocationPrefixes = []skillInvocationEntry{
	{Prefix: "evaluate-plan-acceptance: ", SkillName: "evaluate_plan_acceptance"},
}

type skillInvocationEntry struct {
	Prefix    string
	SkillName string
}

// tryDirectSkillInvocation checks whether the input matches a skill-invocation
// prefix. On match it strips the prefix, validates the JSON payload, invokes
// the registered skill, and returns the skill's data. Returns (nil, false) when
// the input does not match any prefix or when invocation fails.
func (g *Guide) tryDirectSkillInvocation(ctx context.Context, input string) (any, bool) {
	for _, entry := range skillInvocationPrefixes {
		if !strings.HasPrefix(input, entry.Prefix) {
			continue
		}
		rawJSON := json.RawMessage(input[len(entry.Prefix):])
		if !json.Valid(rawJSON) {
			return nil, false
		}
		result := g.skills.Invoke(ctx, entry.SkillName, rawJSON)
		if !result.Success {
			return nil, false
		}
		return result.Data, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// SKILL.md prompt loader
// ---------------------------------------------------------------------------

var (
	planAcceptancePromptOnce sync.Once
	planAcceptancePromptBody string
)

func loadPlanAcceptancePrompt() string {
	planAcceptancePromptOnce.Do(func() {
		root := promptskills.RepoRoot()
		if root == "" {
			return
		}
		skillDir := filepath.Join(root, "agents", "guide", "skillfiles", "evaluate-plan-acceptance")
		_, body, err := diskskills.ReadFull(skillDir)
		if err != nil {
			return
		}
		planAcceptancePromptBody = strings.TrimSpace(body)
	})
	return planAcceptancePromptBody
}

// resetPlanAcceptancePrompt resets the sync.Once for testing.
func resetPlanAcceptancePrompt() {
	planAcceptancePromptOnce = sync.Once{}
	planAcceptancePromptBody = ""
}

// ---------------------------------------------------------------------------
// LLM classification
// ---------------------------------------------------------------------------

const (
	planAcceptanceTemperature = 0.2
	planAcceptanceMaxTokens   = 1024
)

// evaluatePlanAcceptance calls the LLM with structured output to classify the
// user's response as accept / modify / reject.
func (g *Guide) evaluatePlanAcceptance(ctx context.Context, input planAcceptanceInput) (map[string]any, error) {
	systemPrompt := loadPlanAcceptancePrompt()
	if systemPrompt == "" {
		systemPrompt = defaultPlanAcceptanceSystemPrompt
	}

	g.providerMu.RLock()
	provider := g.googleProvider
	g.providerMu.RUnlock()

	if provider == nil {
		return nil, fmt.Errorf("google provider not configured for plan acceptance evaluation")
	}

	temp := float64(planAcceptanceTemperature)
	req := &providers.Request{
		SystemPrompt:     systemPrompt,
		Messages:         []providers.Message{{Role: providers.RoleUser, Content: buildPlanAcceptanceUserPrompt(input)}},
		ResponseMIMEType: "application/json",
		ResponseSchema:   planAcceptanceResponseSchema(),
		Temperature:      &temp,
		MaxTokens:        planAcceptanceMaxTokens,
		Model:            resolveGuideResponseModel(g.config.RouterConfig.Model),
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("plan acceptance classification: %w", err)
	}

	return parsePlanAcceptanceResponse(resp.Content, input)
}

// defaultPlanAcceptanceSystemPrompt is used when the SKILL.md file is
// unavailable (e.g. binary builds without embedded assets).
const defaultPlanAcceptanceSystemPrompt = `Evaluate whether a user approved or rejected a plan proposed by the architect.

Classify the user's response as:
- accept: pure positive intent, NO requests to change ANY items in the plan
- modify: requests to change items OR acceptance with suggestions OR conditional acceptance
- reject: negative intent indicating the plan should not proceed (rejections may also contain modifications)

Return JSON with "result" (accept/modify/reject) and "modifications" (array of adjustment notes, empty for pure accept).`

// ---------------------------------------------------------------------------
// Prompt construction
// ---------------------------------------------------------------------------

func buildPlanAcceptanceUserPrompt(input planAcceptanceInput) string {
	var b strings.Builder
	b.WriteString("Plan ID: ")
	b.WriteString(strings.TrimSpace(input.PlanID))
	b.WriteString("\nPlan Name: ")
	b.WriteString(strings.TrimSpace(input.PlanName))
	b.WriteString("\n\n--- Plan ---\n")
	b.WriteString(strings.TrimSpace(input.Plan))
	b.WriteString("\n\n--- User Response ---\n")
	b.WriteString(strings.TrimSpace(input.UserResponse))
	return b.String()
}

// ---------------------------------------------------------------------------
// Response schema (Gemini structured output)
// ---------------------------------------------------------------------------

func planAcceptanceResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{
				"type":        "string",
				"enum":        []string{"accept", "modify", "reject"},
				"description": "Classification of the user's response.",
			},
			"modifications": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Adjustment notes for the architect. Empty for pure accept.",
			},
		},
		"required": []string{"result", "modifications"},
	}
}

// ---------------------------------------------------------------------------
// Response parsing
// ---------------------------------------------------------------------------

var validPlanAcceptanceResults = map[string]struct{}{
	"accept": {}, "modify": {}, "reject": {},
}

func parsePlanAcceptanceResponse(content string, input planAcceptanceInput) (map[string]any, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("empty response from plan acceptance classification")
	}

	var raw struct {
		Result        string   `json:"result"`
		Modifications []string `json:"modifications"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("parse plan acceptance response: %w", err)
	}

	result := strings.TrimSpace(strings.ToLower(raw.Result))
	if _, ok := validPlanAcceptanceResults[result]; !ok {
		return nil, fmt.Errorf("invalid plan acceptance result %q: must be accept, modify, or reject", raw.Result)
	}

	mods := raw.Modifications
	if mods == nil {
		mods = []string{}
	}

	return map[string]any{
		"plan":          input.Plan,
		"user_response": input.UserResponse,
		"plan_id":       input.PlanID,
		"plan_name":     input.PlanName,
		"result":        result,
		"modifications": mods,
	}, nil
}

// ---------------------------------------------------------------------------
// Skill registration
// ---------------------------------------------------------------------------

// evaluatePlanAcceptanceSkill returns the Go skill definition for plan
// acceptance evaluation. When g is non-nil, the handler calls the LLM for
// classification. When g is nil the skill is used for metadata/prompt
// discovery only (the handler returns an error on invocation).
func evaluatePlanAcceptanceSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("evaluate_plan_acceptance").
		Description("Evaluate whether a user approved or rejected a plan proposed by the architect and any modifications requested if so.").
		Domain("coordination").
		Keywords("plan", "accept", "reject", "modify", "evaluate").
		Priority(95).
		Usage("Use when the Architect sends a plan and user response that needs to be evaluated as accept/modify/reject. "+
			"Classify the user response as: accept (pure positive intent, NO requests to change ANY items), "+
			"modify (requests to change items OR acceptance with suggestions OR conditional acceptance), "+
			"or reject (negative intent indicating the plan should not proceed). "+
			"Rejections may ALSO contain modifications.").
		StringParam("plan", "The plan text to be accepted/rejected/modified.", true).
		StringParam("user_response", "The user response indicating acceptance, rejection, or partial acceptance with change notes.", true).
		StringParam("plan_id", "The ID of the plan.", true).
		StringParam("plan_name", "The name of the plan.", true).
		Example(`{
  "plan": "1. Create a file called SKILL.md. 2. Add frontmatter at the top",
  "plan_id": "1dfud9-39fes0-fhsoihf8-8hf94",
  "plan_name": "Create a Skill",
  "user_response": "Make it so! But don't forget the Output section!"
}`).
		BestPractice("Result is the pure accept/modify/reject classification. Modifications is a list of things to adjust for the architect.").
		BestPractice("A pure 'accept' should have an empty modifications array. A 'modify' or 'reject' should explain what the user wants changed.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planAcceptanceInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			if params.Plan == "" {
				return nil, fmt.Errorf("missing required parameter: plan")
			}
			if params.UserResponse == "" {
				return nil, fmt.Errorf("missing required parameter: user_response")
			}
			if params.PlanID == "" {
				return nil, fmt.Errorf("missing required parameter: plan_id")
			}
			if params.PlanName == "" {
				return nil, fmt.Errorf("missing required parameter: plan_name")
			}
			if g == nil {
				return nil, fmt.Errorf("guide instance required for plan acceptance evaluation")
			}
			return g.evaluatePlanAcceptance(ctx, params)
		}).
		Build()
}

// ---------------------------------------------------------------------------
// SKILL.md file path helper (used by tests)
// ---------------------------------------------------------------------------

func planAcceptanceSkillDir() string {
	root := promptskills.RepoRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "agents", "guide", "skillfiles", "evaluate-plan-acceptance")
}

func planAcceptanceSkillFileExists() bool {
	dir := planAcceptanceSkillDir()
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil
}
