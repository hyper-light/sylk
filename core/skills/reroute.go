package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrRerouteRequested is a sentinel error returned by the reroute_request skill
// handler. Agent tool loops should check for this with errors.Is and abort
// processing gracefully, allowing the Guide to re-classify the request.
var ErrRerouteRequested = errors.New("reroute requested")

// ReroutePublisher publishes a reroute message on the Guide request bus.
type ReroutePublisher func(reason, originalInput, suggestedTarget string) error

// RerouteConfig provides the agent-specific hooks needed by the reroute skill.
type RerouteConfig struct {
	AgentID   string
	SessionID func() string
	Publish   ReroutePublisher
}

type rerouteParams struct {
	Reason          string `json:"reason"`
	OriginalInput   string `json:"original_input"`
	SuggestedTarget string `json:"suggested_target"`
}

// NewRerouteSkill creates a reroute_request skill bound to a specific agent.
// The skill is always loaded (Priority 95) and available across all domains.
func NewRerouteSkill(cfg RerouteConfig) *Skill {
	return NewSkill("reroute_request").
		Description(
			"Request that this conversation be rerouted to a different agent. "+
				"Use this when you determine the user's request is outside your "+
				"capabilities or domain and another agent would handle it better.",
		).
		Domain("routing").
		Keywords("reroute", "redirect", "wrong agent", "can't help", "not my domain").
		Priority(95).
		StringParam("reason", "Why you cannot handle this request", true).
		StringParam("original_input", "The user's original message to re-route", true).
		StringParam("suggested_target", "Suggested agent ID if known (e.g. 'tester', 'engineer')", false).
		Usage("Invoke when the current request is clearly outside your domain.").
		Handler(rerouteHandler(cfg)).
		Build()
}

func rerouteHandler(cfg RerouteConfig) Handler {
	return func(_ context.Context, input json.RawMessage) (any, error) {
		var params rerouteParams
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid reroute parameters: %w", err)
		}
		reason := strings.TrimSpace(params.Reason)
		if reason == "" {
			return nil, fmt.Errorf("reason is required for reroute_request")
		}
		originalInput := strings.TrimSpace(params.OriginalInput)
		if originalInput == "" {
			return nil, fmt.Errorf("original_input is required for reroute_request")
		}
		suggestedTarget := strings.TrimSpace(params.SuggestedTarget)

		if cfg.Publish != nil {
			if err := cfg.Publish(reason, originalInput, suggestedTarget); err != nil {
				return nil, fmt.Errorf("failed to publish reroute: %w", err)
			}
		}

		return map[string]any{
			"rerouted":         true,
			"source_agent":     cfg.AgentID,
			"reason":           reason,
			"suggested_target": suggestedTarget,
		}, ErrRerouteRequested
	}
}
