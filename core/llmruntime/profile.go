package llmruntime

import (
	"strings"

	"github.com/adalundhe/sylk/core/providers"
)

// Profile captures agent-selected runtime behavior for an LLM request.
// Providers map the subset they understand and ignore the rest safely.
type Profile struct {
	ReasoningEffort      string `json:"reasoning_effort,omitempty"`
	ReasoningSummary     string `json:"reasoning_summary,omitempty"`
	Verbosity            string `json:"verbosity,omitempty"`
	IncludeThoughts      *bool  `json:"include_thoughts,omitempty"`
	PromptCacheKey       string `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string `json:"prompt_cache_retention,omitempty"`
	UsePromptCache       *bool  `json:"use_prompt_cache,omitempty"`
	ParallelToolCalls    *bool  `json:"parallel_tool_calls,omitempty"`
	ThinkingBudget       *int   `json:"thinking_budget,omitempty"`
	DisableParallelTools *bool  `json:"disable_parallel_tools,omitempty"`
}

// Apply copies profile fields onto a provider request.
func Apply(req *providers.Request, profile Profile) {
	if req == nil {
		return
	}

	if value := strings.TrimSpace(profile.ReasoningEffort); value != "" {
		req.ReasoningEffort = value
	}
	if value := strings.TrimSpace(profile.ReasoningSummary); value != "" {
		req.ReasoningSummary = value
	}
	if value := strings.TrimSpace(profile.Verbosity); value != "" {
		req.Verbosity = value
	}
	if profile.IncludeThoughts != nil {
		req.IncludeThoughts = Bool(*profile.IncludeThoughts)
	}
	if value := strings.TrimSpace(profile.PromptCacheKey); value != "" {
		req.PromptCacheKey = value
	}
	if value := strings.TrimSpace(profile.PromptCacheRetention); value != "" {
		req.PromptCacheRetention = value
	}
	if profile.UsePromptCache != nil {
		req.UsePromptCache = Bool(*profile.UsePromptCache)
	}
	if profile.ParallelToolCalls != nil {
		req.ParallelToolCalls = Bool(*profile.ParallelToolCalls)
	}
	if profile.ThinkingBudget != nil {
		req.ThinkingBudget = *profile.ThinkingBudget
	}
	if profile.DisableParallelTools != nil {
		req.DisableParallelToolUse = *profile.DisableParallelTools
	}
}

func Bool(value bool) *bool {
	v := value
	return &v
}

func Int(value int) *int {
	v := value
	return &v
}

func SessionPromptCacheKey(agentID, sessionID string) string {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" {
		return ""
	}
	return agentID + ":" + sessionID
}
