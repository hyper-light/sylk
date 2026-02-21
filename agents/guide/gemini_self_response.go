package guide

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/prompts"
	"google.golang.org/genai"
)

const (
	defaultGuideResponseModel   = "gemini-3.1-pro-preview"
	defaultGuideResponseTimeout = 120 * time.Second
)

var guideResponseSystemPrompt = prompts.MustLoad("guide", "self_response")

// GeminiGuideResponder provides model-backed responses for guide-targeted prompts.
type GeminiGuideResponder struct {
	client  *genai.Client
	model   string
	timeout time.Duration
}

// NewGeminiGuideResponder creates a Gemini-backed self-responder for Guide.
func NewGeminiGuideResponder(client *genai.Client, cfg RouterConfig) GuideSelfResponder {
	return &GeminiGuideResponder{
		client:  client,
		model:   resolveGuideResponseModel(cfg.Model),
		timeout: defaultGuideResponseTimeout,
	}
}

func resolveGuideResponseModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if strings.Contains(strings.ToLower(trimmed), "gemini") {
		return trimmed
	}
	return defaultGuideResponseModel
}

func (r *GeminiGuideResponder) Respond(ctx context.Context, request GuideSelfResponseRequest) (string, error) {
	if r == nil || r.client == nil {
		return "", fmt.Errorf("gemini guide responder is not configured")
	}
	guardedCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	resp, err := r.generateWithModel(guardedCtx, r.model, request)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", fmt.Errorf("gemini model %s returned empty response", r.model)
	}
	return text, nil
}

func (r *GeminiGuideResponder) generateWithModel(
	ctx context.Context,
	model string,
	request GuideSelfResponseRequest,
) (*genai.GenerateContentResponse, error) {
	resp, err := r.client.Models.GenerateContent(
		ctx,
		model,
		genai.Text(buildGuideResponsePrompt(request)),
		&genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role: "system",
				Parts: []*genai.Part{
					{Text: guideResponseSystemPrompt},
				},
			},
			Temperature: guideResponseTemperature(),
			ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelHigh,
			},
		},
	)
	if err != nil {
		return nil, formatGeminiError("gemini model "+model, err)
	}
	return resp, nil
}

func buildGuideResponsePrompt(request GuideSelfResponseRequest) string {
	return fmt.Sprintf(
		"Runtime context:\n- guide_agent_id: %s\n- pending_requests: %d\n- registered_agents: %s\n\nUser request:\n%s",
		strings.TrimSpace(request.AgentID),
		request.PendingRequests,
		formatGuideAgentsForPrompt(request.RegisteredAgentIDs),
		strings.TrimSpace(request.Input),
	)
}

func formatGuideAgentsForPrompt(agentIDs []string) string {
	ids := sanitizedGuideAgentIDs(agentIDs)
	if len(ids) == 0 {
		return "(none)"
	}
	return strings.Join(ids, ", ")
}

func guideResponseTemperature() *float32 {
	value := float32(0.2)
	return &value
}
