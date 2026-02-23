package guide

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/core/providers"
)

const (
	guideOnlineReply = "Guide is online. Ask for help, status, or agents."
	guideHelpReply   = "I can help with routing, Sylk status, and agent registry questions. Try: '@guide status', '@guide agents', or ask me directly."
	guideChatReply   = "Hi. I can help with general questions and route work across Sylk agents."
)

// GuideSelfResponseRequest is the runtime context sent to Guide self-responders.
type GuideSelfResponseRequest struct {
	Input                   string
	AgentID                 string
	SessionID               string
	PendingRequests         int
	RegisteredAgentIDs      []string
	LoadedSkillNames        []string
	ActiveConversationAgent string
	ActiveConversationTurns int
	ActiveConversationAge   int
	ActiveConversationScore float64
}

// GuideSelfResponder handles requests explicitly targeted at the guide agent.
type GuideSelfResponder interface {
	Respond(ctx context.Context, request GuideSelfResponseRequest) (string, error)
}

// GuideSelfStreamResponder emits incremental chunks while generating a guide reply.
type GuideSelfStreamResponder interface {
	GuideSelfResponder
	RespondStream(ctx context.Context, request GuideSelfResponseRequest, onChunk func(string) error) (string, *providers.Usage, error)
}

// RespondGuideSelf streams when available and always returns the final reply text.
func RespondGuideSelf(
	ctx context.Context,
	responder GuideSelfResponder,
	request GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	streamer, ok := responder.(GuideSelfStreamResponder)
	if ok {
		return streamer.RespondStream(ctx, request, onChunk)
	}
	reply, err := respondIfPresent(ctx, responder, request)
	if err != nil {
		return "", nil, err
	}
	if err := emitGuideChunk(onChunk, reply); err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(reply), nil, nil
}

func resolveGuideSelfResponder(cfg Config, provider *providers.GoogleProvider, owner *Guide) GuideSelfResponder {
	var responder GuideSelfResponder
	if cfg.SelfResponder != nil {
		responder = cfg.SelfResponder
	} else if provider == nil {
		responder = NewStaticGuideResponder()
	} else {
		primary := NewGeminiGuideResponder(provider, cfg.RouterConfig)
		if owner != nil {
			primary = NewGeminiGuideResponderWithTools(
				provider,
				cfg.RouterConfig,
				owner.prepareGuideSelfResponseTools,
				owner.executeGuideSelfResponseToolCall,
			)
		}
		responder = NewFallbackGuideResponder(
			primary,
			NewStaticGuideResponder(),
		)
	}
	return responder
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
	primaryErr := primaryGuideResponderError(reply, err)
	geminiTrace("self_response", "primary_fallback", map[string]any{
		"input_preview": tracePreview(request.Input, 180),
		"primary_error": traceGuideResponderError(primaryErr),
		"primary_reply": tracePreview(reply, 180),
	})
	if !allowStaticGuideFallback(request) {
		geminiTrace("self_response", "fallback_skipped_non_meta", map[string]any{
			"input_preview": tracePreview(request.Input, 180),
			"primary_error": traceGuideResponderError(primaryErr),
		})
		return "", primaryErr
	}
	fallbackReply, fallbackErr := respondIfPresent(ctx, r.fallback, request)
	if isUsableGuideReply(fallbackReply, fallbackErr) {
		geminiTrace("self_response", "fallback_success", map[string]any{
			"input_preview":   tracePreview(request.Input, 180),
			"fallback_reply":  tracePreview(fallbackReply, 180),
			"fallback_source": "static",
		})
		return strings.TrimSpace(fallbackReply), nil
	}
	geminiTrace("self_response", "fallback_failed", map[string]any{
		"input_preview":   tracePreview(request.Input, 180),
		"primary_error":   traceGuideResponderError(primaryErr),
		"fallback_error":  traceGuideResponderError(fallbackErr),
		"fallback_source": "static",
	})
	return "", errors.Join(primaryErr, fallbackErr)
}

func (r *fallbackGuideResponder) RespondStream(
	ctx context.Context,
	request GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	reply, usage, err := respondGuideViaResponder(ctx, r.primary, request, onChunk)
	if isUsableGuideReply(reply, err) {
		return strings.TrimSpace(reply), usage, nil
	}
	primaryErr := primaryGuideResponderError(reply, err)
	geminiTrace("self_response", "primary_stream_fallback", map[string]any{
		"input_preview": tracePreview(request.Input, 180),
		"primary_error": traceGuideResponderError(primaryErr),
		"primary_reply": tracePreview(reply, 180),
	})
	if !allowStaticGuideFallback(request) {
		geminiTrace("self_response", "fallback_stream_skipped_non_meta", map[string]any{
			"input_preview": tracePreview(request.Input, 180),
			"primary_error": traceGuideResponderError(primaryErr),
		})
		return "", nil, primaryErr
	}
	fallbackReply, fallbackUsage, fallbackErr := respondGuideViaResponder(ctx, r.fallback, request, onChunk)
	if isUsableGuideReply(fallbackReply, fallbackErr) {
		geminiTrace("self_response", "fallback_stream_success", map[string]any{
			"input_preview":   tracePreview(request.Input, 180),
			"fallback_reply":  tracePreview(fallbackReply, 180),
			"fallback_source": "static",
		})
		return strings.TrimSpace(fallbackReply), fallbackUsage, nil
	}
	geminiTrace("self_response", "fallback_stream_failed", map[string]any{
		"input_preview":   tracePreview(request.Input, 180),
		"primary_error":   traceGuideResponderError(primaryErr),
		"fallback_error":  traceGuideResponderError(fallbackErr),
		"fallback_source": "static",
	})
	return "", nil, errors.Join(primaryErr, fallbackErr)
}

func respondGuideViaResponder(
	ctx context.Context,
	responder GuideSelfResponder,
	request GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	if responder == nil {
		return "", nil, fmt.Errorf("guide responder is not configured")
	}
	streamer, ok := responder.(GuideSelfStreamResponder)
	if ok {
		return streamer.RespondStream(ctx, request, onChunk)
	}
	reply, err := responder.Respond(ctx, request)
	if err != nil {
		return "", nil, err
	}
	if err := emitGuideChunk(onChunk, reply); err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(reply), nil, nil
}

func emitGuideChunk(onChunk func(string) error, reply string) error {
	if onChunk == nil {
		return nil
	}
	if strings.TrimSpace(reply) == "" {
		return nil
	}
	return onChunk(reply)
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
	if isGuideWellbeingQuery(query) {
		return staticGuideWellbeingReply(), nil
	}
	if isGuideGreetingQuery(query) {
		return guideChatReply, nil
	}
	if isGuideStatusQuery(query) {
		return staticGuideStatusReply(request), nil
	}
	if isGuideAgentCountQuery(query) {
		return staticGuideAgentCountReply(request.RegisteredAgentIDs), nil
	}
	if isGuideAgentsQuery(query) {
		return staticGuideAgentsReply(request.RegisteredAgentIDs), nil
	}
	if isGuideCapabilitiesQuery(query) {
		return staticGuideCapabilitiesReply(request), nil
	}
	return staticGuideHelpReply(request), nil
}

func (r *staticGuideResponder) RespondStream(
	ctx context.Context,
	request GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	reply, err := r.Respond(ctx, request)
	if err != nil {
		return "", nil, err
	}
	if err := emitGuideChunk(onChunk, reply); err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(reply), nil, nil
}

func normalizeGuideQuery(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func isGuideStatusQuery(query string) bool {
	return strings.Contains(query, "status") || strings.Contains(query, "health")
}

func isGuideAgentCountQuery(query string) bool {
	return containsAll(query, "agent", "how many") || containsAll(query, "agent", "number of")
}

func isGuideAgentsQuery(query string) bool {
	return containsAny(query, "agents", "what agents", "which agents", "registered agents", "available agents", "list agents", "agent registry")
}

func isGuideCapabilitiesQuery(query string) bool {
	return containsAny(
		query,
		"what can you do",
		"what can sylk do",
		"capabilities",
		"how does sylk",
		"help",
		"commands",
		"dsl",
	)
}

func isGuideGreetingQuery(query string) bool {
	tokens := tokenizeGuideQuery(query)
	if len(tokens) == 0 {
		return false
	}

	for _, token := range tokens {
		if token == "hello" || token == "hi" || token == "hey" {
			return true
		}
	}

	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] != "good" {
			continue
		}
		switch tokens[i+1] {
		case "morning", "afternoon", "evening":
			return true
		}
	}

	return false
}

func isGuideWellbeingQuery(query string) bool {
	return containsAny(query, "how are you", "how's it going", "hows it going")
}

func staticGuideStatusReply(request GuideSelfResponseRequest) string {
	base := fmt.Sprintf(
		"Guide is running. Pending requests: %d. Registered agents: %d.",
		request.PendingRequests,
		len(sanitizedGuideAgentIDs(request.RegisteredAgentIDs)),
	)
	conversation := staticGuideConversationReply(request)
	if conversation == "" {
		return base
	}
	return base + " " + conversation
}

func staticGuideAgentsReply(agentIDs []string) string {
	ids := sanitizedGuideAgentIDs(agentIDs)
	if len(ids) == 0 {
		return "No agents are currently registered."
	}
	return fmt.Sprintf("Registered agents (%d): %s", len(ids), strings.Join(ids, ", "))
}

func staticGuideAgentCountReply(agentIDs []string) string {
	ids := sanitizedGuideAgentIDs(agentIDs)
	if len(ids) == 0 {
		return "There are currently no registered agents."
	}
	return fmt.Sprintf("There are %d registered agents right now.", len(ids))
}

func staticGuideCapabilitiesReply(request GuideSelfResponseRequest) string {
	agentCount := len(sanitizedGuideAgentIDs(request.RegisteredAgentIDs))
	return fmt.Sprintf(
		"Sylk can route requests across specialized agents, answer system/meta questions, and guide workflows. Registered agents right now: %d.",
		agentCount,
	)
}

func staticGuideWellbeingReply() string {
	return "I’m doing well and ready to help. Ask anything about Sylk, or I can route work to the right agent."
}

func staticGuideHelpReply(request GuideSelfResponseRequest) string {
	conversation := staticGuideConversationReply(request)
	if conversation == "" {
		return guideHelpReply
	}
	return guideHelpReply + " " + conversation
}

func staticGuideConversationReply(request GuideSelfResponseRequest) string {
	agentID := strings.TrimSpace(request.ActiveConversationAgent)
	if agentID == "" {
		return ""
	}
	turns := request.ActiveConversationTurns
	if turns <= 0 {
		turns = 1
	}
	age := request.ActiveConversationAge
	if age > 0 {
		return fmt.Sprintf(
			"Active conversation is with %s (%d turns, updated %ds ago).",
			agentID,
			turns,
			age,
		)
	}
	return fmt.Sprintf("Active conversation is with %s (%d turns).", agentID, turns)
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

func containsAny(query string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(query, fragment) {
			return true
		}
	}
	return false
}

func containsAll(query string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(query, fragment) {
			return false
		}
	}
	return true
}

func tokenizeGuideQuery(query string) []string {
	return strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func traceGuideResponderError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func primaryGuideResponderError(reply string, err error) error {
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		return nil
	}
	return fmt.Errorf("guide responder returned empty response")
}

func allowStaticGuideFallback(request GuideSelfResponseRequest) bool {
	query := normalizeGuideQuery(request.Input)
	if query == "" {
		return true
	}
	return isGuideWellbeingQuery(query) ||
		isGuideGreetingQuery(query) ||
		isGuideStatusQuery(query) ||
		isGuideAgentCountQuery(query) ||
		isGuideAgentsQuery(query) ||
		isGuideCapabilitiesQuery(query)
}
