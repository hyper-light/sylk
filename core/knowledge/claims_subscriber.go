package knowledge

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
)

// ClaimsKnowledgeSubscriber indexes accepted claims and testaments as
// searchable knowledge documents. It implements the activity subscriber
// interface, detecting terminal claims events and extracting structured
// content for full-text indexing.
type ClaimsKnowledgeSubscriber struct {
	store *KnowledgeStore
}

// NewClaimsKnowledgeSubscriber creates a subscriber that indexes claims
// entities into the knowledge store. Nil-safe on store.
func NewClaimsKnowledgeSubscriber(store *KnowledgeStore) *ClaimsKnowledgeSubscriber {
	return &ClaimsKnowledgeSubscriber{store: store}
}

// Name returns the subscriber identifier.
func (s *ClaimsKnowledgeSubscriber) Name() string { return "knowledge.claims" }

// Receive processes a claims activity. Only terminal events
// (claim_accepted, board_complete) trigger extraction and logging.
// Full indexing into the HybridQueryCoordinator is deferred until
// the coordinator exposes a document-level ingest API.
func (s *ClaimsKnowledgeSubscriber) Receive(_ context.Context, a activity.AgentActivity) {
	if s.store == nil {
		return
	}
	if !isClaimsTerminalAction(a.Action) {
		return
	}

	payload := parseClaimsPayload(a.Payload)
	if payload == nil {
		return
	}

	title := claimsPayloadString(payload, "title")
	description := claimsPayloadString(payload, "description")
	summary := claimsPayloadString(payload, "summary")

	content := firstNonEmptyClaimsString(description, summary, title)
	if len(strings.TrimSpace(content)) < 50 {
		return
	}

	slog.Debug("knowledge_claims_index",
		"action", string(a.Action),
		"title", title,
		"content_len", len(content))
}

func isClaimsTerminalAction(kind activity.ActionKind) bool {
	switch kind {
	case activity.ActionClaimAccepted, activity.ActionBoardComplete,
		activity.ActionTestamentSubmitted:
		return true
	default:
		return false
	}
}

func parseClaimsPayload(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func claimsPayloadString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, isStr := v.(string)
	if !isStr {
		return ""
	}
	return s
}

func firstNonEmptyClaimsString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
