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

func CoordinationSkills(cfg CoordinationSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		coordQueryViewSkill(cfg),
		coordWatchUpdatesSkill(cfg),
		coordClaimScopeSkill(cfg),
		coordReleaseScopeSkill(cfg),
		coordPublishArtifactSkill(cfg),
		coordRequestReviewSkill(cfg),
		coordResolveArtifactSkill(cfg),
	}
}

func coordQueryViewSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_query_view").
		Description("Query the current task coordination ledger: active claims, published artifacts, and pending reviews for this task. Use before duplicating peer work.").
		Domain("coordination").
		Keywords("coordination", "claims", "artifacts", "reviews", "task state", "peer work").
		Priority(90).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("task_name", "Task name. Defaults to the current pipeline task name when available.", false).
		BoolParam("include_drafts", "Include draft artifacts in the returned view.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID        string `json:"task_id"`
				TaskName      string `json:"task_name"`
				IncludeDrafts bool   `json:"include_drafts"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			taskName := defaultTaskName(params.TaskName, cfg.CurrentTaskName)
			workerType := defaultWorkerType(cfg.WorkerType)
			return cfg.Client.QueryView(ctx, coordination.QueryViewInput{
				TaskID:        taskID,
				TaskName:      taskName,
				WorkerType:    workerType,
				IncludeDrafts: params.IncludeDrafts,
			})
		}).
		Build()
}

func coordClaimScopeSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_claim_scope").
		Description("Claim a concrete task scope before doing work so peer workers do not duplicate it. Claims can be exclusive or shared.").
		Domain("coordination").
		Keywords("claim", "scope", "coordination", "lock", "ownership", "avoid duplication").
		Priority(95).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("task_name", "Task name. Defaults to the current pipeline task name when available.", false).
		StringParam("scope_kind", "Scope type: file, symbol, api, test_surface, ux_surface, invariant, investigation, implementation, component.", true).
		StringParam("scope_key", "Concrete scope key, such as a file path, symbol, or invariant identifier.", true).
		StringParam("purpose", "Why this scope is being claimed.", false).
		StringParam("mode", "Claim mode: exclusive, shared, or review. Defaults to exclusive.", false).
		IntParam("lease_seconds", "Lease duration in seconds before the claim expires. Defaults to 600.", false).
		ArrayObjectParam("evidence", "Optional evidence references supporting the claim.", map[string]*skills.Property{
			"kind":  {Type: "string", Description: "Evidence kind, such as file, symbol, test, or artifact"},
			"value": {Type: "string", Description: "Evidence value"},
		}, []string{"value"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID       string                     `json:"task_id"`
				TaskName     string                     `json:"task_name"`
				ScopeKind    coordination.ScopeKind     `json:"scope_kind"`
				ScopeKey     string                     `json:"scope_key"`
				Purpose      string                     `json:"purpose"`
				Mode         coordination.ClaimMode     `json:"mode"`
				LeaseSeconds int                        `json:"lease_seconds"`
				Evidence     []coordination.EvidenceRef `json:"evidence"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return cfg.Client.ClaimScope(ctx, coordination.ClaimScopeInput{
				TaskID:       taskID,
				TaskName:     defaultTaskName(params.TaskName, cfg.CurrentTaskName),
				ScopeKind:    params.ScopeKind,
				ScopeKey:     strings.TrimSpace(params.ScopeKey),
				Purpose:      strings.TrimSpace(params.Purpose),
				Mode:         params.Mode,
				LeaseSeconds: params.LeaseSeconds,
				Evidence:     params.Evidence,
			})
		}).
		Build()
}

func coordWatchUpdatesSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_watch_updates").
		Description("Wait for coordination changes for this task and return a fresh task view/worker packet when peers publish new claims, artifacts, or review updates. Use when you are blocked on peer feedback.").
		Domain("coordination").
		Keywords("watch", "coordination", "updates", "reviews", "artifacts", "peer progress", "wait").
		Priority(88).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("task_name", "Task name. Defaults to the current pipeline task name when available.", false).
		IntParam("after_version", "Only return after the coordination view advances beyond this version.", false).
		IntParam("wait_seconds", "Maximum time to wait before returning the current view. Defaults to 20.", false).
		BoolParam("include_drafts", "Include draft artifacts in the watched view.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID        string `json:"task_id"`
				TaskName      string `json:"task_name"`
				AfterVersion  int64  `json:"after_version"`
				WaitSeconds   int    `json:"wait_seconds"`
				IncludeDrafts bool   `json:"include_drafts"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return cfg.Client.WatchUpdates(ctx, coordination.WatchUpdatesInput{
				TaskID:        taskID,
				TaskName:      defaultTaskName(params.TaskName, cfg.CurrentTaskName),
				WorkerType:    defaultWorkerType(cfg.WorkerType),
				AfterVersion:  params.AfterVersion,
				WaitSeconds:   params.WaitSeconds,
				IncludeDrafts: params.IncludeDrafts,
			})
		}).
		Build()
}

func coordReleaseScopeSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_release_scope").
		Description("Release a previously claimed scope when the work is done, handed off, or intentionally abandoned.").
		Domain("coordination").
		Keywords("release", "claim", "handoff", "done", "coordination").
		Priority(85).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("claim_id", "Claim identifier to release.", false).
		StringParam("scope_kind", "Scope kind when releasing by scope.", false).
		StringParam("scope_key", "Scope key when releasing by scope.", false).
		StringParam("resolution", "Short explanation of why the claim is being released.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID     string                 `json:"task_id"`
				ClaimID    string                 `json:"claim_id"`
				ScopeKind  coordination.ScopeKind `json:"scope_kind"`
				ScopeKey   string                 `json:"scope_key"`
				Resolution string                 `json:"resolution"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return cfg.Client.ReleaseScope(ctx, coordination.ReleaseScopeInput{
				TaskID:     taskID,
				ClaimID:    strings.TrimSpace(params.ClaimID),
				ScopeKind:  params.ScopeKind,
				ScopeKey:   strings.TrimSpace(params.ScopeKey),
				Resolution: strings.TrimSpace(params.Resolution),
			})
		}).
		Build()
}

func coordPublishArtifactSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_publish_artifact").
		Description("Publish a structured artifact to the task coordination ledger so peers can reuse your findings instead of rediscovering them.").
		Domain("coordination").
		Keywords("artifact", "publish", "findings", "risk map", "test plan", "ux contract", "implementation plan").
		Priority(95).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("task_name", "Task name. Defaults to the current pipeline task name when available.", false).
		StringParam("kind", "Artifact kind, such as risk_map, invariant_set, test_plan, verification_result, implementation_plan, change_set, ux_contract, or component_contract.", true).
		StringParam("title", "Optional artifact title.", false).
		StringParam("summary", "Human-readable summary of the artifact.", true).
		StringParam("scope_kind", "Optional scope kind tied to this artifact.", false).
		StringParam("scope_key", "Optional scope key tied to this artifact.", false).
		StringParam("status", "Artifact status: draft, published, accepted, superseded, resolved, rejected. Defaults to published.", false).
		ArrayObjectParam("evidence", "Evidence supporting the artifact.", map[string]*skills.Property{
			"kind":  {Type: "string", Description: "Evidence kind"},
			"value": {Type: "string", Description: "Evidence value"},
		}, []string{"value"}, false).
		ArrayParam("upstream_artifact_ids", "Artifact IDs this artifact depends on.", "string", false).
		StringParam("supersedes_artifact_id", "Artifact ID superseded by this artifact.", false).
		ObjectParam("payload", "Structured artifact payload.", map[string]*skills.Property{}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID               string                      `json:"task_id"`
				TaskName             string                      `json:"task_name"`
				Kind                 string                      `json:"kind"`
				Title                string                      `json:"title"`
				Summary              string                      `json:"summary"`
				ScopeKind            coordination.ScopeKind      `json:"scope_kind"`
				ScopeKey             string                      `json:"scope_key"`
				Status               coordination.ArtifactStatus `json:"status"`
				Payload              map[string]any              `json:"payload"`
				Evidence             []coordination.EvidenceRef  `json:"evidence"`
				UpstreamArtifactIDs  []string                    `json:"upstream_artifact_ids"`
				SupersedesArtifactID string                      `json:"supersedes_artifact_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return cfg.Client.PublishArtifact(ctx, coordination.PublishArtifactInput{
				TaskID:               taskID,
				TaskName:             defaultTaskName(params.TaskName, cfg.CurrentTaskName),
				Kind:                 strings.TrimSpace(params.Kind),
				Title:                strings.TrimSpace(params.Title),
				Summary:              strings.TrimSpace(params.Summary),
				ScopeKind:            params.ScopeKind,
				ScopeKey:             strings.TrimSpace(params.ScopeKey),
				Status:               params.Status,
				Payload:              params.Payload,
				Evidence:             params.Evidence,
				UpstreamArtifactIDs:  append([]string(nil), params.UpstreamArtifactIDs...),
				SupersedesArtifactID: strings.TrimSpace(params.SupersedesArtifactID),
			})
		}).
		Build()
}

func coordRequestReviewSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_request_review").
		Description("Request another worker type to review a specific coordination artifact instead of freeform rework.").
		Domain("coordination").
		Keywords("review", "artifact", "request", "peer review", "handoff").
		Priority(88).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("artifact_id", "Artifact ID to review.", true).
		StringParam("reviewer_type", "Reviewer worker type, such as engineer, designer, inspector-pipeline, or tester-pipeline.", true).
		StringParam("summary", "What the reviewer should evaluate.", true).
		ArrayParam("criteria", "Specific review criteria.", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID       string   `json:"task_id"`
				ArtifactID   string   `json:"artifact_id"`
				ReviewerType string   `json:"reviewer_type"`
				Summary      string   `json:"summary"`
				Criteria     []string `json:"criteria"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return cfg.Client.RequestReview(ctx, coordination.RequestReviewInput{
				TaskID:       taskID,
				ArtifactID:   strings.TrimSpace(params.ArtifactID),
				ReviewerType: strings.TrimSpace(params.ReviewerType),
				Summary:      strings.TrimSpace(params.Summary),
				Criteria:     append([]string(nil), params.Criteria...),
			})
		}).
		Build()
}

func coordResolveArtifactSkill(cfg CoordinationSkillConfig) *skills.Skill {
	return skills.NewSkill("coord_resolve_artifact").
		Description("Resolve a coordination artifact or review after applying the feedback or finalizing the work.").
		Domain("coordination").
		Keywords("resolve", "artifact", "review", "accepted", "changes requested", "done").
		Priority(82).
		StringParam("task_id", "Task identifier. Defaults to the current pipeline task.", false).
		StringParam("artifact_id", "Artifact ID to resolve.", false).
		StringParam("review_id", "Review ID to resolve.", false).
		StringParam("status", "Artifact status to set.", false).
		StringParam("review_status", "Review status to set.", false).
		StringParam("resolution_summary", "Short explanation of the resolution.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskID            string                      `json:"task_id"`
				ArtifactID        string                      `json:"artifact_id"`
				ReviewID          string                      `json:"review_id"`
				Status            coordination.ArtifactStatus `json:"status"`
				ReviewStatus      coordination.ReviewStatus   `json:"review_status"`
				ResolutionSummary string                      `json:"resolution_summary"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			taskID := defaultTaskID(params.TaskID, cfg.CurrentTaskID)
			if taskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return cfg.Client.ResolveArtifact(ctx, coordination.ResolveArtifactInput{
				TaskID:            taskID,
				ArtifactID:        strings.TrimSpace(params.ArtifactID),
				ReviewID:          strings.TrimSpace(params.ReviewID),
				Status:            params.Status,
				ReviewStatus:      params.ReviewStatus,
				ResolutionSummary: strings.TrimSpace(params.ResolutionSummary),
			})
		}).
		Build()
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
