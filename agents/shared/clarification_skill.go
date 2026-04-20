package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

// AskUserClarificationConfig wires the shared user-clarification skill
// builder to a concrete agent. Callers supply the bus, agent identity,
// session id, and a message-id factory; the builder returns a skill
// any agent can register without re-implementing the publish path.
//
// Why this exists: prior to consolidation, only designer registered
// `ask_user_clarification`. Engineer and tester routed clarification
// asks through challenge_agent → inspector escalation, which delays
// the answer by one round trip and pushes the question out of the
// asking agent's tool-call context. Promoting the skill to a shared
// builder gives every implementer a direct path to the user with
// uniform shape (question, context, options) — matching the priority
// order documented in `prompts/shared/language_tooling_preference.md`,
// which names this skill as the trigger for resolving task ambiguity.
type AskUserClarificationConfig struct {
	// Bus is the guide bus used to publish the clarification request.
	Bus guide.EventBus
	// AgentID is the asking agent's runtime id; populates from_agent.
	AgentID string
	// AgentName is the asking agent's display name (e.g., "engineer",
	// "tester-pipeline"). Used in the request prose so the user sees
	// who is asking.
	AgentName string
	// SessionID is the guide session this clarification belongs to.
	SessionID string
	// NewMessageID returns a fresh message id for the published
	// request. Callers reuse their existing id factory so message ids
	// stay consistent with the rest of the agent's bus traffic.
	NewMessageID func() string
}

// BuildAskUserClarificationSkill returns the shared
// `ask_user_clarification` skill. The skill publishes a fire-and-forget
// clarification request to the orchestrator, which surfaces the
// question to the user and routes the answer back through normal
// inbound channels. Callers register the returned skill on their
// agent's skill set and add `ask_user_clarification` to their tool
// policy allow-list.
func BuildAskUserClarificationSkill(cfg AskUserClarificationConfig) *skills.Skill {
	type params struct {
		Question string   `json:"question"`
		Context  string   `json:"context"`
		Options  []string `json:"options,omitempty"`
	}
	return skills.NewSkill("ask_user_clarification").
		Description("Ask the user for clarification when requirements are ambiguous, multiple valid approaches exist, or the task is silent on a decision you cannot make from existing signals.").
		Domain("collaboration").
		Keywords("clarify", "question", "user", "ambiguous", "language", "tooling", "framework").
		Priority(70).
		StringParam("question", "Specific question for the user", true).
		StringParam("context", "Context explaining why clarification is needed", true).
		ArrayParam("options", "Concrete options for the user to choose from", "string", false).
		Usage("Use when the task spec, peer activity in the Activity Fabric, and the existing codebase do not converge on the decision you need to make — e.g., language or framework choice for a new component, library selection where multiple valid options exist, or interpretation of an ambiguous requirement. Prefer this over guessing or speculative writes.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if cfg.Bus == nil {
				return nil, fmt.Errorf("bus not available")
			}
			question := strings.TrimSpace(p.Question)
			if question == "" {
				return nil, fmt.Errorf("question is required")
			}
			messageID := ""
			if cfg.NewMessageID != nil {
				messageID = cfg.NewMessageID()
			}
			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("%s needs clarification: %s", strings.TrimSpace(cfg.AgentName), question),
				SourceAgentID:   strings.TrimSpace(cfg.AgentID),
				SourceAgentName: strings.TrimSpace(cfg.AgentName),
				TargetAgentID:   "orchestrator",
				FireAndForget:   true,
				SessionID:       strings.TrimSpace(cfg.SessionID),
				Timestamp:       time.Now(),
			}
			msg := guide.NewRequestMessage(messageID, req)
			msg.Metadata = map[string]any{
				"type":       "clarification_request",
				"question":   question,
				"context":    strings.TrimSpace(p.Context),
				"options":    p.Options,
				"from_agent": strings.TrimSpace(cfg.AgentID),
			}
			if err := cfg.Bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish clarification: %w", err)
			}
			return map[string]any{
				"asked":    true,
				"question": question,
			}, nil
		}).
		Build()
}
