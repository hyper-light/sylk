package guide

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/genai"
)

const (
	guideOnlineReply = "Guide is online. Ask for help, status, or agents."
	guideHelpReply   = "Guide handles routing. Try: '@guide status', '@guide agents', or send without @guide for automatic routing."
)

// GuideSelfResponseRequest is the runtime context sent to Guide self-responders.
type GuideSelfResponseRequest struct {
	Input              string
	AgentID            string
	PendingRequests    int
	RegisteredAgentIDs []string
}

// GuideSelfResponder handles requests explicitly targeted at the guide agent.
type GuideSelfResponder interface {
	Respond(ctx context.Context, request GuideSelfResponseRequest) (string, error)
}

func resolveGuideSelfResponder(cfg Config, geminiClient *genai.Client) GuideSelfResponder {
	if cfg.SelfResponder != nil {
		return cfg.SelfResponder
	}
	if geminiClient == nil {
		return NewStaticGuideResponder()
	}
	return NewFallbackGuideResponder(
		NewGeminiGuideResponder(geminiClient, cfg.RouterConfig),
		NewStaticGuideResponder(),
	)
}

// NewFallbackGuideResponder returns a responder that falls back on error/empty output.
func NewFallbackGuideResponder(primary GuideSelfResponder, fallback GuideSelfResponder) GuideSelfResponder {
	return &fallbackGuideResponder{
		primary:  primary,
		fallback: fallback,
	}
}

type fallbackGuideResponder struct {
	primary  GuideSelfResponder
	fallback GuideSelfResponder
}

func (r *fallbackGuideResponder) Respond(ctx context.Context, request GuideSelfResponseRequest) (string, error) {
	reply, err := respondIfPresent(ctx, r.primary, request)
	if isUsableGuideReply(reply, err) {
		return strings.TrimSpace(reply), nil
	}
	fallbackReply, fallbackErr := respondIfPresent(ctx, r.fallback, request)
	if isUsableGuideReply(fallbackReply, fallbackErr) {
		return strings.TrimSpace(fallbackReply), nil
	}
	return "", errors.Join(err, fallbackErr)
}

func respondIfPresent(ctx context.Context, responder GuideSelfResponder, request GuideSelfResponseRequest) (string, error) {
	if responder == nil {
		return "", fmt.Errorf("guide responder is not configured")
	}
	return responder.Respond(ctx, request)
}

func isUsableGuideReply(reply string, err error) bool {
	return err == nil && strings.TrimSpace(reply) != ""
}

// NewStaticGuideResponder returns the deterministic built-in guide responder.
func NewStaticGuideResponder() GuideSelfResponder {
	return &staticGuideResponder{}
}

type staticGuideResponder struct{}

func (r *staticGuideResponder) Respond(_ context.Context, request GuideSelfResponseRequest) (string, error) {
	query := normalizeGuideQuery(request.Input)
	if query == "" {
		return guideOnlineReply, nil
	}
	if isGuideStatusQuery(query) {
		return staticGuideStatusReply(request), nil
	}
	if isGuideAgentsQuery(query) {
		return staticGuideAgentsReply(request.RegisteredAgentIDs), nil
	}
	return guideHelpReply, nil
}

func normalizeGuideQuery(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func isGuideStatusQuery(query string) bool {
	return strings.Contains(query, "status") || strings.Contains(query, "health")
}

func isGuideAgentsQuery(query string) bool {
	return strings.Contains(query, "agent") || strings.Contains(query, "registry")
}

func staticGuideStatusReply(request GuideSelfResponseRequest) string {
	return fmt.Sprintf(
		"Guide is running. Pending requests: %d. Registered agents: %d.",
		request.PendingRequests,
		len(sanitizedGuideAgentIDs(request.RegisteredAgentIDs)),
	)
}

func staticGuideAgentsReply(agentIDs []string) string {
	ids := sanitizedGuideAgentIDs(agentIDs)
	if len(ids) == 0 {
		return "No agents are currently registered."
	}
	return "Registered agents: " + strings.Join(ids, ", ")
}

func sanitizedGuideAgentIDs(agentIDs []string) []string {
	ids := make([]string, 0, len(agentIDs))
	for _, id := range agentIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		ids = append(ids, trimmed)
	}
	sort.Strings(ids)
	return ids
}
