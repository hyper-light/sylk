package academic

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/versioning"
)

func (a *Academic) applyLLMRuntimeProfile(ctx context.Context, req *providers.Request, stage string) {
	llmruntime.ApplyStage(req, a.llmStageProfile(stage), llmruntime.ApplyOptions{
		Model:     academicRuntimeModel(req, a.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   strings.TrimSpace(a.id),
		SessionID: academicRuntimeSessionID(ctx, req, a.config.SessionID),
	})
}

func (a *Academic) llmStageProfile(stage string) llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("academic", stage)
}

func academicRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("academic")
}

func academicDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("academic")
}

func academicRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}

func academicRuntimeSessionID(ctx context.Context, req *providers.Request, fallback string) string {
	if req != nil && req.Metadata != nil {
		if trimmed, _ := req.Metadata["session_id"].(string); strings.TrimSpace(trimmed) != "" {
			return strings.TrimSpace(trimmed)
		}
	}
	if trimmed := versioning.SessionIDFromContext(ctx); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}
