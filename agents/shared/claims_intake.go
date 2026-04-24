package shared

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
)

// ClaimsIntakeConfig bundles everything needed to wire event-driven
// claims intake on an agent.
type ClaimsIntakeConfig struct {
	// AgentID is the agent's unique identifier.
	AgentID string

	// SessionID scopes the inbox to one session's board.
	SessionID string

	// Bus is the agent's event bus. Used to subscribe to claims
	// delta topics via EventBusDeltaSubscriber.
	Bus guide.EventBus

	// Board is the session's claims board. Used for graph resolution
	// when a delta matches. Nil-safe.
	Board *claims.ClaimsBoard

	// Scope is the agent's goroutine scope. OnResolved dispatches
	// into this scope for tracked, async execution.
	Scope *concurrency.GoroutineScope

	// ProcessEntry is the agent's claims entry processing function.
	// Called under scope.Go for each matched delta. The agent
	// implements this to run its tool loop or handle the entry.
	ProcessEntry func(ctx context.Context, entry *claims.GraphEntryPoint) error
}

// WireClaimsIntake creates a ClaimsInbox with event-driven dispatch.
// When a delta matches an expectation or standing subscription, the
// inbox resolves it into a GraphEntryPoint and dispatches it to
// ProcessEntry via scope.Go. Returns the inbox (caller must Start
// and Close it) or nil if the config is insufficient.
func WireClaimsIntake(cfg ClaimsIntakeConfig) *claims.ClaimsInbox {
	if cfg.AgentID == "" || cfg.SessionID == "" || cfg.Bus == nil {
		return nil
	}

	subscriber := &EventBusDeltaSubscriber{bus: cfg.Bus}

	inbox, err := claims.NewClaimsInbox(claims.InboxConfig{
		AgentID:    cfg.AgentID,
		SessionID:  cfg.SessionID,
		Subscriber: subscriber,
		Board:      cfg.Board,
		OnResolved: func(entry *claims.GraphEntryPoint) {
			if entry == nil {
				return
			}
			if cfg.Scope != nil && cfg.ProcessEntry != nil {
				if err := cfg.Scope.Go("process_claim", 0, func(ctx context.Context) error {
					return cfg.ProcessEntry(ctx, entry)
				}); err != nil {
					slog.Error("claims_intake_dispatch_failed",
						"agent_id", cfg.AgentID,
						"error", err.Error(),
					)
				}
				return
			}
			if cfg.ProcessEntry != nil {
				if err := cfg.ProcessEntry(context.Background(), entry); err != nil {
					slog.Error("claims_intake_process_failed",
						"agent_id", cfg.AgentID,
						"error", err.Error(),
					)
				}
			}
		},
	})
	if err != nil {
		slog.Error("claims_intake_create_failed",
			"agent_id", cfg.AgentID,
			"error", err.Error(),
		)
		return nil
	}
	return inbox
}

// ComposeClaimsEntryPrompt builds a user-message prompt from a
// GraphEntryPoint. This is the prompt the agent's tool loop receives
// when a claims delta matches. The prompt includes the delta kind,
// the claim/testament/validation content, the node's edges for
// traversal context, and guidance on which skills to use.
func ComposeClaimsEntryPrompt(entry *claims.GraphEntryPoint) string {
	if entry == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Incoming Claims Event\n\n")
	b.WriteString("**Delta kind:** " + entry.Delta.DeltaKind() + "\n")
	if entry.Expectation != nil {
		b.WriteString("**Matches expectation:** claim " + entry.Expectation.ClaimID + " (action " + entry.Expectation.ActionID + ")\n")
	}
	b.WriteString("\n")

	node := entry.Node
	switch {
	case node.Claim != nil:
		c := node.Claim
		b.WriteString("### Claim: " + c.Title + "\n\n")
		if c.Description != "" {
			b.WriteString(c.Description + "\n\n")
		}
		b.WriteString("- **Status:** " + string(c.Status) + "\n")
		b.WriteString("- **Action type:** " + string(c.ActionType) + "\n")
		if issuer := claims.IssuerAgentID(c.Relations); issuer != "" {
			b.WriteString("- **Issuer:** " + issuer + "\n")
		}
		if subject := claims.SubjectAgentID(c.Relations); subject != "" {
			b.WriteString("- **Subject:** " + subject + "\n")
		}
		if len(c.Scope) > 0 {
			b.WriteString("- **Scope:** ")
			for i, s := range c.Scope {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(s.Kind + ":" + s.Key)
			}
			b.WriteString("\n")
		}
		if len(c.Validations) > 0 {
			b.WriteString("\n**Validations required:**\n")
			for _, v := range c.Validations {
				b.WriteString("- " + v.Description)
				if v.QualityBar != "" {
					b.WriteString(" (bar: " + v.QualityBar + ")")
				}
				b.WriteString("\n")
			}
		}

	case node.Testament != nil:
		t := node.Testament
		b.WriteString("### Testament Response\n\n")
		b.WriteString("**Summary:** " + t.Summary + "\n")
		b.WriteString("**Confidence:** " + t.Confidence + "\n")
		if len(t.Artifacts) > 0 {
			b.WriteString("\n**Artifacts:**\n")
			for _, a := range t.Artifacts {
				b.WriteString("- [" + a.Kind + "] " + truncatePromptString(a.Reference, 200) + "\n")
			}
		}

	case node.Validation != nil:
		v := node.Validation
		b.WriteString("### Validation Verdict\n\n")
		b.WriteString("**Status:** " + string(v.Status) + "\n")
		b.WriteString("**Description:** " + v.Description + "\n")
		if v.QualityBar != "" {
			b.WriteString("**Quality bar:** " + v.QualityBar + "\n")
		}

	default:
		b.WriteString("### Phase Transition or Board Event\n\n")
		b.WriteString("Use `traverse` to inspect the board state.\n")
	}

	if len(node.Edges) > 0 {
		b.WriteString("\n**Graph edges** (use `traverse` to explore):\n")
		limit := len(node.Edges)
		if limit > 10 {
			limit = 10
		}
		for _, e := range node.Edges[:limit] {
			b.WriteString("- " + e.Relationship + " → " + e.TargetType + ":" + e.TargetID + "\n")
		}
		if len(node.Edges) > 10 {
			b.WriteString("- ... and " + fmt.Sprintf("%d", len(node.Edges)-10) + " more\n")
		}
	}

	b.WriteString("\n---\n\n")
	b.WriteString("Process this event using your skills. Use `traverse` for more context, ")
	b.WriteString("`post_action` to issue sub-claims, `submit_testaments` to respond, ")
	b.WriteString("`evaluate_validation` to judge responses.\n")

	return b.String()
}

func truncatePromptString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// EventBusDeltaSubscriber adapts a guide.EventBus to the
// claims.DeltaSubscriber interface. Subscribes to claims delta topics
// via SubscribeAsync and extracts claims.Delta from the message
// payload.
type EventBusDeltaSubscriber struct {
	bus guide.EventBus
}

// SubscribeDelta registers a claims delta handler on the bus topic
// pattern. The handler is called for every MessageTypeClaimsDelta
// message matching the pattern.
func (s *EventBusDeltaSubscriber) SubscribeDelta(pattern string, handler claims.DeltaHandler) (claims.DeltaSubscription, error) {
	if s.bus == nil {
		return claims.NoopDeltaBus{}.SubscribeDelta(pattern, handler)
	}
	sub, err := s.bus.SubscribeAsync(pattern, func(msg *guide.Message) error {
		delta, extractErr := guide.ExtractClaimsDelta(msg)
		if extractErr != nil || delta == nil {
			return nil
		}
		handler(delta)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &eventBusDeltaSub{sub: sub}, nil
}

type eventBusDeltaSub struct {
	sub guide.Subscription
}

func (s *eventBusDeltaSub) Topic() string {
	if s.sub == nil {
		return ""
	}
	return s.sub.Topic()
}

func (s *eventBusDeltaSub) Unsubscribe() error {
	if s.sub == nil {
		return nil
	}
	return s.sub.Unsubscribe()
}
