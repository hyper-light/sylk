package shared

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// EmitAdvisory publishes an advisory_emitted activity (Medium
// resolution) for a knowledge-agent consult response. Peer pipelines
// tackling adjacent work see the advisory in their next ambient
// context envelope — they didn't have to consult, the librarian's
// wisdom is now ambient.
//
// See docs/FABRIC.md §"Librarian / Academic / Archivalist (knowledge
// agents)" — emit advisory projections.
func EmitAdvisory(ctx context.Context, in AdvisoryInput) activity.ActivityID {
	advice := strings.TrimSpace(in.Advice)
	if advice == "" {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"advice":           advice,
		"requesting_agent": in.RequestingAgentID,
		"requesting_type":  in.RequestingAgentType,
		"original_query":   in.OriginalQuery,
		"references":       in.References,
	})
	subject := activity.Subject{
		PathPrefix:  in.Scope,
		TargetAgent: in.RequestingAgentID,
		Domain:      in.Domain,
	}
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionAdvisoryEmitted),
		Action:     activity.ActionAdvisoryEmitted,
		Actor: activity.Actor{
			AgentID:   in.AdvisorAgentID,
			AgentType: in.AdvisorAgentType,
		},
		Subject: subject,
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
	return act.ID
}

// AdvisoryInput is the typed input to EmitAdvisory. AdvisorAgentID
// and AdvisorAgentType identify the knowledge agent emitting the
// advisory; RequestingAgentID + RequestingAgentType identify the
// pipeline agent who consulted (so the addressee filter on ambient
// context picks it up).
type AdvisoryInput struct {
	SessionID           string
	AdvisorAgentID      string
	AdvisorAgentType    string
	RequestingAgentID   string
	RequestingAgentType string
	Scope               string
	Domain              string
	OriginalQuery       string
	Advice              string
	References          []string
}

// EmitProactiveAdvisory publishes a proactive_advisory activity
// (Coarse resolution) when a knowledge agent observing the fabric
// notices a pattern match or anti-pattern in a peer's scope.
// Surfaces in the target agent's next ambient context envelope.
func EmitProactiveAdvisory(ctx context.Context, in ProactiveAdvisoryInput) activity.ActivityID {
	advice := strings.TrimSpace(in.Advice)
	if advice == "" {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"advice":          advice,
		"target_agent_id": in.TargetAgentID,
		"target_type":     in.TargetAgentType,
		"trigger":         in.Trigger,
		"references":      in.References,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionProactiveAdvisory),
		Action:     activity.ActionProactiveAdvisory,
		Actor: activity.Actor{
			AgentID:   in.AdvisorAgentID,
			AgentType: in.AdvisorAgentType,
		},
		Subject: activity.Subject{
			PathPrefix:  in.Scope,
			TargetAgent: in.TargetAgentID,
			Domain:      in.Domain,
		},
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
	return act.ID
}

// ProactiveAdvisoryInput is the typed input to EmitProactiveAdvisory.
type ProactiveAdvisoryInput struct {
	SessionID        string
	AdvisorAgentID   string
	AdvisorAgentType string
	TargetAgentID    string
	TargetAgentType  string
	Scope            string
	Domain           string
	Trigger          string // Why the advisor is reaching out
	Advice           string
	References       []string
}

// EmitNotification publishes a notification_emitted activity targeted
// at a specific peer + scope. Used by inspector and knowledge agents
// for proactive heads-ups that don't fit the consult/advisory shape.
func EmitNotification(ctx context.Context, in NotificationInput) activity.ActivityID {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"body":           body,
		"target_agent":   in.TargetAgentID,
		"target_type":    in.TargetAgentType,
		"category":       in.Category,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionNotificationEmitted),
		Action:     activity.ActionNotificationEmitted,
		Actor: activity.Actor{
			AgentID:   in.SenderAgentID,
			AgentType: in.SenderAgentType,
		},
		Subject: activity.Subject{
			PathPrefix:  in.Scope,
			TargetAgent: in.TargetAgentID,
		},
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
	return act.ID
}

// NotificationInput is the typed input to EmitNotification.
type NotificationInput struct {
	SessionID         string
	SenderAgentID     string
	SenderAgentType   string
	TargetAgentID     string
	TargetAgentType   string
	Scope             string
	Category          string
	Body              string
}
