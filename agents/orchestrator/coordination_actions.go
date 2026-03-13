package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

func (o *Orchestrator) handleCoordinationAction(ctx context.Context, req *guide.ActionRequest) (bool, error) {
	if req == nil || o.coordination == nil {
		return false, nil
	}

	actor := coordination.Actor{
		AgentID:   req.SourceAgentID,
		AgentType: req.SourceAgentName,
		SessionID: o.config.SessionID,
	}
	if actor.AgentType == "" {
		actor.AgentType = req.SourceAgentID
	}

	var (
		result any
		err    error
	)

	switch req.Action {
	case coordination.ActionClaimScope:
		var input coordination.ClaimScopeInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			result, err = o.coordination.ClaimScope(ctx, actor, input)
		}
	case coordination.ActionReleaseScope:
		var input coordination.ReleaseScopeInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			result, err = o.coordination.ReleaseScope(ctx, actor, input)
		}
	case coordination.ActionPublishArtifact:
		var input coordination.PublishArtifactInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			result, err = o.coordination.PublishArtifact(ctx, actor, input)
		}
	case coordination.ActionRequestReview:
		var input coordination.RequestReviewInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			review, reviewErr := o.coordination.RequestReview(ctx, actor, input)
			if reviewErr == nil {
				o.publishReviewWakeBestEffort(ctx, review)
			}
			result, err = review, reviewErr
		}
	case coordination.ActionResolveArtifact:
		var input coordination.ResolveArtifactInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			result, err = o.coordination.ResolveArtifact(ctx, actor, input)
		}
	case coordination.ActionQueryView:
		var input coordination.QueryViewInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			result, err = o.coordination.QueryView(ctx, input)
		}
	case coordination.ActionWatchUpdates:
		var input coordination.WatchUpdatesInput
		err = decodeCoordinationData(req.Data, &input)
		if err == nil {
			result, err = o.coordination.WatchUpdates(ctx, input)
		}
	default:
		return false, nil
	}

	if req.FireAndForget {
		return true, err
	}
	if err != nil {
		return true, o.publishCoordinationFailure(req, err)
	}
	return true, o.publishCoordinationSuccess(req, result)
}

func decodeCoordinationData(data any, dst any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode coordination payload: %w", err)
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		return fmt.Errorf("decode coordination payload: %w", err)
	}
	return nil
}

func (o *Orchestrator) publishCoordinationSuccess(req *guide.ActionRequest, data any) error {
	if req == nil || req.FireAndForget {
		return nil
	}
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             true,
		Data:                data,
		RespondingAgentID:   o.config.AgentID,
		RespondingAgentName: "orchestrator",
		ProcessingTime:      0,
	}
	msg := guide.NewResponseMessage(generateMessageID(), resp)
	return o.bus.Publish(o.actionResponseTopic(req), msg)
}

func (o *Orchestrator) publishCoordinationFailure(req *guide.ActionRequest, err error) error {
	if req == nil || req.FireAndForget || err == nil {
		return nil
	}
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             false,
		Error:               err.Error(),
		RespondingAgentID:   o.config.AgentID,
		RespondingAgentName: "orchestrator",
	}
	msg := guide.NewResponseMessage(generateMessageID(), resp)
	return o.bus.Publish(o.actionResponseTopic(req), msg)
}

func (o *Orchestrator) actionResponseTopic(req *guide.ActionRequest) string {
	if req == nil {
		return guide.TopicResponses("", "")
	}
	sourceType := strings.TrimSpace(req.SourceAgentName)
	if o != nil {
		if ann, ok := o.knownAgents[req.SourceAgentID]; ok && ann != nil {
			if trimmed := strings.TrimSpace(ann.AgentType); trimmed != "" {
				sourceType = trimmed
			}
		}
	}
	if sourceType == "" {
		sourceType = req.SourceAgentID
	}
	return guide.TopicResponses(sourceType, req.SourceAgentID)
}

const submitCoordinationEventTimeout = 15 * time.Second

func (o *Orchestrator) submitCoordinationEventAsync(event *coordination.ArchivalEvent) {
	if o == nil || o.scope == nil || event == nil || !o.config.ArchivalistEnabled {
		return
	}
	o.scope.Go("submit-coordination-event", submitCoordinationEventTimeout, func(ctx context.Context) error {
		return o.submitCoordinationEvent(ctx, event)
	})
}

func (o *Orchestrator) submitCoordinationEvent(ctx context.Context, event *coordination.ArchivalEvent) error {
	if o == nil || event == nil {
		return nil
	}
	taskEvent := &TaskEvent{
		ID:          generateMessageID(),
		Type:        event.Type,
		Timestamp:   event.Timestamp,
		TaskID:      event.TaskID,
		TaskName:    event.TaskName,
		Status:      TaskStatusCompleted,
		AgentID:     event.Actor.AgentID,
		Result:      event.Metadata,
		CompletedAt: event.Timestamp,
		SessionID:   o.config.SessionID,
		Metadata: map[string]any{
			"coordination_summary": event.Summary,
			"coordination_actor":   event.Actor,
		},
	}
	return o.SubmitEventToArchivalist(ctx, taskEvent)
}
