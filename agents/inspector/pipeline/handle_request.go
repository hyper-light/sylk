package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
)

type InspectionStageResult struct {
	Criteria *shared.InspectorCriteria
	Result   *shared.InspectorResult
}

// InspectTask runs the inspector directly against a pipeline task and returns
// the current criteria snapshot for that task.
func (pi *PipelineInspector) InspectTask(ctx context.Context, task *agentShared.PipelineTaskInput) (*shared.InspectorCriteria, error) {
	stage, err := pi.RunTask(ctx, task)
	if err != nil {
		return nil, err
	}
	return stage.Criteria, nil
}

// RunTask drives the inspector through its normal task handler and returns the
// resulting criteria and validation snapshot for the task.
func (pi *PipelineInspector) RunTask(ctx context.Context, task *agentShared.PipelineTaskInput) (*InspectionStageResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task is required")
	}
	if pi.getProvider() == nil {
		pi.seedCriteriaFromTask(task)
		contract := agentShared.BuildTaskExecutionContract(task)
		if contract != nil && contract.HasImplementationEvidence {
			if _, err := pi.ValidateAgainstCriteria(
				ctx,
				strings.TrimSpace(task.TaskID),
				affectedPathsFromTask(task),
				agentShared.PipelineWorkerType(task),
			); err != nil {
				return nil, err
			}
		}
		return pi.stageSnapshot(task.TaskID)
	}

	payload, err := json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("encode pipeline task: %w", err)
	}

	sessionID := strings.TrimSpace(task.SessionID)
	if pi.bus != nil && pi.running {
		msg, err := agentShared.RequestGuideRouteSync(ctx, agentShared.GuideRouteSyncRequest{
			Bus:           pi.bus,
			ResponseTopic: guide.TopicResponses("tui", "tui"),
			Request: &guide.RouteRequest{
				Input:           string(payload),
				TargetAgentID:   pi.id,
				ExplicitTarget:  true,
				SourceAgentID:   "tui",
				SourceAgentName: "pipeline",
				SessionID:       sessionID,
				Metadata: map[string]any{
					"task_id":   strings.TrimSpace(task.TaskID),
					"task_slug": stringValue(task.Context, "task_slug"),
					"task_name": stringValue(task.Context, "task_name"),
				},
			},
		})
		if err != nil {
			return nil, err
		}
		if errText, ok := msg.GetError(); ok && strings.TrimSpace(errText) != "" {
			return nil, fmt.Errorf("%s", errText)
		}
		if resp, ok := msg.GetRouteResponse(); ok && resp != nil && !resp.Success {
			return nil, fmt.Errorf("%s", resp.Error)
		}
	} else {
		_, err = pi.Handle(ctx, &guide.ForwardedRequest{
			Input:     string(payload),
			SessionID: sessionID,
			Metadata: map[string]any{
				"task_id":   strings.TrimSpace(task.TaskID),
				"task_slug": stringValue(task.Context, "task_slug"),
			},
		})
		if err != nil {
			return nil, err
		}
	}

	return pi.stageSnapshot(task.TaskID)
}

func (pi *PipelineInspector) stageSnapshot(taskID string) (*InspectionStageResult, error) {
	taskID = strings.TrimSpace(taskID)
	criteria := pi.CriteriaForTask(taskID)
	if criteria == nil {
		return nil, fmt.Errorf("no criteria defined for task %s", taskID)
	}
	return &InspectionStageResult{
		Criteria: criteria,
		Result:   pi.ResultForTask(taskID),
	}, nil
}

func (pi *PipelineInspector) CriteriaForTask(taskID string) *shared.InspectorCriteria {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	pi.mu.RLock()
	criteria := pi.criteria[taskID]
	pi.mu.RUnlock()
	return cloneInspectorCriteria(criteria)
}

func (pi *PipelineInspector) ResultForTask(taskID string) *shared.InspectorResult {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	pi.mu.RLock()
	result := pi.results[taskID]
	pi.mu.RUnlock()
	return cloneInspectorResult(result)
}

func cloneInspectorCriteria(criteria *shared.InspectorCriteria) *shared.InspectorCriteria {
	if criteria == nil {
		return nil
	}
	cloned := *criteria
	cloned.SuccessCriteria = append([]shared.SuccessCriterion(nil), criteria.SuccessCriteria...)
	cloned.QualityGates = append([]shared.QualityGate(nil), criteria.QualityGates...)
	cloned.Constraints = append([]shared.Constraint(nil), criteria.Constraints...)
	return &cloned
}

func cloneInspectorResult(result *shared.InspectorResult) *shared.InspectorResult {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.Issues = append([]shared.ValidationIssue(nil), result.Issues...)
	cloned.CriteriaMet = append([]string(nil), result.CriteriaMet...)
	cloned.CriteriaFailed = append([]string(nil), result.CriteriaFailed...)
	if result.QualityGateResults != nil {
		cloned.QualityGateResults = make(map[string]bool, len(result.QualityGateResults))
		for key, value := range result.QualityGateResults {
			cloned.QualityGateResults[key] = value
		}
	}
	cloned.FeedbackHistory = append([]shared.InspectorFeedback(nil), result.FeedbackHistory...)
	return &cloned
}

func stringValue(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}
