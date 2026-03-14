package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
)

type TaskStageResult struct {
	Plan         *testershared.TestPlan
	CreatedFiles []string
	SuiteResult  *tester.TestSuiteResult
}

// TestTask runs the tester against a structured pipeline task and returns the
// stage snapshot the executor should use to decide whether execute work is
// still needed.
func (pt *PipelineTester) TestTask(ctx context.Context, task *agentshared.PipelineTaskInput) (*TaskStageResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task is required")
	}

	if pt.getProvider() == nil {
		return pt.testTaskWithoutProvider(ctx, task)
	}

	payload, err := json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("encode pipeline task: %w", err)
	}

	sessionID := strings.TrimSpace(task.SessionID)
	if pt.bus != nil && pt.running {
		msg, err := agentshared.RequestGuideRouteSync(ctx, agentshared.GuideRouteSyncRequest{
			Bus:           pt.bus,
			ResponseTopic: guide.TopicResponses("tui", "tui"),
			Request: &guide.RouteRequest{
				Input:           string(payload),
				TargetAgentID:   pt.id,
				ExplicitTarget:  true,
				SourceAgentID:   "tui",
				SourceAgentName: "pipeline",
				SessionID:       sessionID,
				Metadata: map[string]any{
					"task_id":   strings.TrimSpace(task.TaskID),
					"task_slug": taskContextString(task.Context, "task_slug"),
					"task_name": taskContextString(task.Context, "task_name"),
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
		_, err = pt.Handle(ctx, &guide.ForwardedRequest{
			Input:     string(payload),
			SessionID: sessionID,
			Metadata: map[string]any{
				"task_id":   strings.TrimSpace(task.TaskID),
				"task_slug": taskContextString(task.Context, "task_slug"),
			},
		})
		if err != nil {
			return nil, err
		}
	}

	return &TaskStageResult{
		Plan:         pt.planSnapshot(),
		CreatedFiles: pt.createdArtifacts(),
		SuiteResult:  pt.lastSuiteSnapshot(),
	}, nil
}

func (pt *PipelineTester) testTaskWithoutProvider(ctx context.Context, task *agentshared.PipelineTaskInput) (*TaskStageResult, error) {
	contract := agentshared.BuildTaskExecutionContract(task)
	req := &tester.TesterRequest{
		TaskID:     strings.TrimSpace(task.TaskID),
		TaskPrompt: strings.TrimSpace(task.Prompt),
		Files:      taskContextFiles(task),
		WorkerType: agentshared.PipelineWorkerType(task),
	}
	if contract != nil && contract.HasImplementationEvidence {
		req.Intent = tester.IntentRunTests
	} else {
		req.Intent = tester.IntentCreateTests
	}

	resp, err := pt.HandleRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	result := &TaskStageResult{
		Plan:         pt.planSnapshot(),
		CreatedFiles: pt.createdArtifacts(),
		SuiteResult:  pt.lastSuiteSnapshot(),
	}
	if result.SuiteResult == nil && resp != nil {
		result.SuiteResult = resp.SuiteResult
	}
	return result, nil
}

func taskContextString(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}
