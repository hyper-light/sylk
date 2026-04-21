package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

type CoordinationClient struct {
	Bus             guide.EventBus
	BusProvider     func() guide.EventBus
	SourceAgentID   func() string
	SourceAgentType func() string
	SessionID       func() string
	RegisterPending func(string) <-chan *guide.Message
	ClearPending    func(string)
	Timeout         time.Duration
}

func (c CoordinationClient) QueryView(ctx context.Context, input coordination.QueryViewInput) (*coordination.QueryViewResult, error) {
	var result coordination.QueryViewResult
	if err := c.request(ctx, coordination.ActionQueryView, input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c CoordinationClient) WatchUpdates(ctx context.Context, input coordination.WatchUpdatesInput) (*coordination.WatchUpdatesResult, error) {
	var result coordination.WatchUpdatesResult
	timeout := c.Timeout
	minimum := time.Duration(input.WaitSeconds+5) * time.Second
	if minimum > timeout {
		timeout = minimum
	}
	if err := c.requestWithTimeout(ctx, coordination.ActionWatchUpdates, input, &result, timeout); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c CoordinationClient) ClaimScope(ctx context.Context, input coordination.ClaimScopeInput) (*coordination.Claim, error) {
	var result coordination.Claim
	if err := c.request(ctx, coordination.ActionClaimScope, input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c CoordinationClient) ReleaseScope(ctx context.Context, input coordination.ReleaseScopeInput) (*coordination.Claim, error) {
	var result coordination.Claim
	if err := c.request(ctx, coordination.ActionReleaseScope, input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c CoordinationClient) PublishArtifact(ctx context.Context, input coordination.PublishArtifactInput) (*coordination.Artifact, error) {
	var result coordination.Artifact
	if err := c.request(ctx, coordination.ActionPublishArtifact, input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c CoordinationClient) RequestReview(ctx context.Context, input coordination.RequestReviewInput) (*coordination.Review, error) {
	var result coordination.Review
	if err := c.request(ctx, coordination.ActionRequestReview, input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c CoordinationClient) ResolveArtifact(ctx context.Context, input coordination.ResolveArtifactInput) (map[string]any, error) {
	if strings.TrimSpace(input.ReviewID) == "" {
		reviewID, err := c.resolvePendingReviewID(ctx, input.TaskID, input.ArtifactID)
		if err != nil {
			return nil, err
		}
		if reviewID != "" {
			input.ReviewID = reviewID
		}
	}
	var result map[string]any
	if err := c.request(ctx, coordination.ActionResolveArtifact, input, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c CoordinationClient) resolvePendingReviewID(ctx context.Context, taskID, artifactID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	artifactID = strings.TrimSpace(artifactID)
	if taskID == "" || artifactID == "" {
		return "", nil
	}
	view, err := c.QueryView(ctx, coordination.QueryViewInput{TaskID: taskID})
	if err != nil {
		return "", fmt.Errorf("query coordination view for pending review: %w", err)
	}
	if view == nil {
		return "", nil
	}
	matches := pendingReviewIDsForArtifact(view.View.Reviews, artifactID)
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple pending reviews exist for artifact %q; specify review_id explicitly", artifactID)
	}
}

func pendingReviewIDsForArtifact(reviews []coordination.Review, artifactID string) []string {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil
	}
	matches := make([]string, 0, 1)
	for _, review := range reviews {
		if strings.TrimSpace(review.ArtifactID) != artifactID {
			continue
		}
		if review.Status != coordination.ReviewStatusPending {
			continue
		}
		if reviewID := strings.TrimSpace(review.ID); reviewID != "" {
			matches = append(matches, reviewID)
		}
	}
	return matches
}

func (c CoordinationClient) request(ctx context.Context, action string, payload any, out any) error {
	return c.requestWithTimeout(ctx, action, payload, out, 0)
}

func (c CoordinationClient) requestWithTimeout(ctx context.Context, action string, payload any, out any, timeout time.Duration) error {
	bus := c.eventBus()
	if bus == nil || c.RegisterPending == nil || c.ClearPending == nil || c.SourceAgentID == nil || c.SourceAgentType == nil || c.SessionID == nil {
		return fmt.Errorf("coordination client is not configured")
	}
	if timeout <= 0 {
		timeout = c.Timeout
	}
	if timeout <= 0 {
		timeout = DefaultConsultationTimeout
	}
	correlationID := "coord_" + uuid.NewString()[:12]
	waitCh := c.RegisterPending(correlationID)
	defer c.ClearPending(correlationID)

	req := &guide.ActionRequest{
		CorrelationID:   correlationID,
		SourceAgentID:   strings.TrimSpace(c.SourceAgentID()),
		SourceAgentName: strings.TrimSpace(c.SourceAgentType()),
		TargetAgentID:   "orchestrator",
		Action:          action,
		Data:            payload,
		Timestamp:       time.Now(),
	}
	if err := bus.Publish(guide.TopicGuideRequests, guide.NewActionMessage("", req)); err != nil {
		return fmt.Errorf("publish coordination action %s: %w", action, err)
	}

	var msg *guide.Message
	err := RunWithContextLease(ctx, ContextLeaseConfig{
		AttemptTimeout: timeout,
		MaxRefreshes:   DefaultConsultationLeaseRefreshes,
	}, func(waitCtx context.Context) error {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case msg = <-waitCh:
			return nil
		}
	})
	if err != nil {
		return WrapLeaseTimeoutError(fmt.Sprintf("coordination action %s", action), timeout, err)
	}
	if msg == nil {
		return fmt.Errorf("coordination action %s returned empty response", action)
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		if errText, ok := msg.GetError(); ok && strings.TrimSpace(errText) != "" {
			return fmt.Errorf("coordination action %s failed: %s", action, errText)
		}
		return fmt.Errorf("coordination action %s returned invalid response", action)
	}
	if !resp.Success {
		return fmt.Errorf("coordination action %s failed: %s", action, resp.Error)
	}
	if out == nil {
		return nil
	}
	encoded, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("encode coordination action %s result: %w", action, err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decode coordination action %s result: %w", action, err)
	}
	return nil
}

func (c CoordinationClient) eventBus() guide.EventBus {
	if c.BusProvider != nil {
		if bus := c.BusProvider(); bus != nil {
			return bus
		}
	}
	return c.Bus
}

type CoordinationSkillConfig struct {
	Client          CoordinationClient
	CurrentTaskID   func() string
	CurrentTaskName func() string
	WorkerType      func() string
}

// CoordinationSkills previously returned manage_claim + publish_work_event.
// Both skills were removed: they were not used by any agent's LLM in practice
// and their use cases are covered by the Claims Board (per-task claim
// lifecycle) plus the Fabric's ambient_context and consult_peer/challenge_peer
// primitives. The function stays for backwards source-compatibility with any
// out-of-tree caller that still references it; it now returns nil.
//
// The underlying AutoPublish* emitters (fabric activities for
// ActionClaimAcquired, ActionArtifactPublished, etc.) stay in this package —
// programmatic callers such as tester/pipeline/testing_skills.go still emit
// those activities directly, and the CoordinationClient RPC surface is
// unchanged.
func CoordinationSkills(cfg CoordinationSkillConfig) []*skills.Skill {
	_ = cfg
	return nil
}

func defaultTaskID(explicit string, current func() string) string {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed
	}
	if current != nil {
		return strings.TrimSpace(current())
	}
	return ""
}

func defaultTaskName(explicit string, current func() string) string {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed
	}
	if current != nil {
		return strings.TrimSpace(current())
	}
	return ""
}

func defaultWorkerType(current func() string) string {
	if current == nil {
		return ""
	}
	return strings.TrimSpace(current())
}
