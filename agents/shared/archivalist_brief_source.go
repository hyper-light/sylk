package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/versioning"
)

const archivalistBriefToolName = "archivalist_get_briefing"

// ArchivalistBriefSource requests handoff briefs from the Archivalist via
// bus. The Archivalist has accumulated Scribe commentary and can produce
// targeted briefs richer than direct LLM summarization.
type ArchivalistBriefSource struct {
	bus     guide.EventBus
	timeout time.Duration
}

// NewArchivalistBriefSource creates a BriefSource that routes requests
// to the Archivalist via the event bus.
func NewArchivalistBriefSource(bus guide.EventBus) *ArchivalistBriefSource {
	return &ArchivalistBriefSource{
		bus:     bus,
		timeout: 10 * time.Second,
	}
}

// RequestBrief sends a briefing request to the Archivalist and waits for
// the response within the configured timeout.
func (a *ArchivalistBriefSource) RequestBrief(ctx context.Context, agentType string, contextSize, turnNumber int) (*handoff.ContextBrief, error) {
	req := &guide.RouteRequest{
		SourceAgentID:   "brief-requester",
		SourceAgentName: "brief-requester",
		TargetAgentID:   "archivalist",
		ExplicitTarget:  true,
		Input:           fmt.Sprintf(`%s {"tier":"standard"}`, archivalistBriefToolName),
		Metadata: map[string]any{
			"request_type": "briefing",
			"tool_name":    archivalistBriefToolName,
			"brief_tier":   "standard",
			"brief_format": "context_brief",
			"agent_type":   agentType,
			"context_size": contextSize,
			"turn_number":  turnNumber,
		},
	}
	if sessionID := versioning.SessionIDFromContext(ctx); sessionID != "" {
		req.SessionID = sessionID
	}

	// Issuing-side claim: brief requester posts consultation claim against archivalist.
	if sessionID := versioning.SessionIDFromContext(ctx); sessionID != "" {
		if board := claims.DefaultSessionBoardRegistry().Lookup(sessionID); board != nil {
			if err := board.PostAction(ctx, claims.Action{AgentID: "brief-requester", Type: claims.ActionTypeConsultation}, []claims.Claim{{
				Title:       "Request brief from archivalist for " + agentType,
				Description: "Handoff briefing request to archivalist",
				Scope:       []claims.ClaimScopeEntry{{Kind: "consultation", Key: "archivalist"}},
				ActionType:  claims.ActionTypeConsultation,
				Relations: []claims.Relation{
					{Related: "brief-requester", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
					{Related: "archivalist", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				},
				Validations: []*claims.Validation{{
					Type: claims.ValidationTypeReceipt, Required: false,
					Description: "Archivalist returns context brief", QualityBar: "brief != nil",
					Status: claims.ValidationStatusPending,
				}},
			}}); err != nil {
				// Best-effort: brief request proceeds even if claim fails.
				_ = err
			}
		}
	}
	msg, err := RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
		Bus:               a.bus,
		ResponseTopic:     guide.TopicResponses("brief-requester", "brief-requester"),
		Request:           req,
		InactivityTimeout: a.timeout,
	})
	if err != nil {
		return nil, err
	}
	if brief := a.parseBriefResponse(msg); brief != nil {
		return brief, nil
	}
	return nil, fmt.Errorf("brief request returned empty response")
}

// parseBriefResponse extracts a ContextBrief from the bus response message.
func (a *ArchivalistBriefSource) parseBriefResponse(msg *guide.Message) *handoff.ContextBrief {
	if msg == nil || msg.Payload == nil {
		return nil
	}

	// Try to extract from RouteResponse.Data.
	resp, ok := msg.Payload.(*guide.RouteResponse)
	if !ok {
		return nil
	}
	if !resp.Success {
		return nil
	}

	switch data := resp.Data.(type) {
	case *handoff.ContextBrief:
		return data
	case handoff.ContextBrief:
		brief := data
		return &brief
	case map[string]any:
		return mapToContextBrief(data)
	case string:
		var brief handoff.ContextBrief
		if err := json.Unmarshal([]byte(data), &brief); err != nil {
			brief.TaskSummary = data
			brief.GeneratedAt = time.Now()
		}
		return &brief
	}
	return nil
}

func mapToContextBrief(data map[string]any) *handoff.ContextBrief {
	if len(data) == 0 {
		return nil
	}
	brief := &handoff.ContextBrief{
		TaskSummary:  stringFromMap(data, "task_summary"),
		KeyDecisions: stringFromMap(data, "key_decisions"),
		ActiveState:  stringFromMap(data, "active_state"),
		NextSteps:    stringFromMap(data, "next_steps"),
		Blockers:     stringFromMap(data, "blockers"),
		ContextSize:  intFromMap(data, "context_size"),
		TurnNumber:   intFromMap(data, "turn_number"),
	}
	if generatedAt := timeFromMap(data, "generated_at"); !generatedAt.IsZero() {
		brief.GeneratedAt = generatedAt
	}
	if brief.GeneratedAt.IsZero() {
		brief.GeneratedAt = time.Now()
	}
	if brief.TaskSummary == "" && brief.KeyDecisions == "" && brief.ActiveState == "" && brief.NextSteps == "" && brief.Blockers == "" {
		return nil
	}
	return brief
}

func stringFromMap(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func intFromMap(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func timeFromMap(data map[string]any, key string) time.Time {
	switch value := data[key].(type) {
	case time.Time:
		return value
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}
