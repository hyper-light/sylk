package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	inspectorpipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/adalundhe/sylk/core/pipeline/tdd"
)

const managedPipelinePollInterval = 15 * time.Second

func managedPipelineEligible(dispatch *taskDispatchContext) bool {
	if dispatch == nil {
		return false
	}
	switch strings.TrimSpace(dispatch.agentType) {
	case "engineer", "designer":
	default:
		return false
	}
	switch strings.TrimSpace(dispatch.pipelineStage) {
	case "", string(StageExecute):
		return true
	default:
		return false
	}
}

func (o *Orchestrator) routeManagedPipelineDispatch(dispatch *taskDispatchContext, done <-chan struct{}) error {
	o.mu.RLock()
	manager := o.pipelineMgr
	o.mu.RUnlock()
	if manager == nil {
		return fmt.Errorf("pipeline manager not configured")
	}
	if o.scope == nil {
		return fmt.Errorf("orchestrator scope not configured")
	}

	return o.scope.Go("route-managed-"+dispatch.nodeID, 0, func(routeCtx context.Context) error {
		cfg, err := o.buildManagedPipelineConfig(dispatch)
		if err != nil {
			o.publishManagedPipelineFailure(dispatch, err)
			return nil
		}

		pipelineID, err := manager.Create(routeCtx, cfg)
		if err != nil {
			o.publishManagedPipelineFailure(dispatch, fmt.Errorf("create pipeline: %w", err))
			return nil
		}
		defer manager.Release(pipelineID)
		if err := manager.Start(routeCtx, pipelineID); err != nil {
			o.publishManagedPipelineFailure(dispatch, fmt.Errorf("start pipeline: %w", err))
			return nil
		}
		for _, agentType := range PipelinePanelAgentTypes {
			o.publishPipelineAgentRegistration(agentType, dispatch.pipelineTaskID, dispatch.pipelineTaskSlug, "")
		}

		ticker := time.NewTicker(managedPipelinePollInterval)
		defer ticker.Stop()

		for {
			result, err := manager.GetResult(pipelineID)
			if err != nil {
				o.publishManagedPipelineFailure(dispatch, fmt.Errorf("query pipeline result: %w", err))
				return nil
			}
			if result != nil && tdd.IsTerminalStatus(result.Status) {
				if update := managedPipelineUpdateFromResult(dispatch, result); update != nil {
					publishPipelineUpdateMessage(o.bus, o.config.AgentID, update)
				}
				return nil
			}

			select {
			case <-routeCtx.Done():
				_ = manager.Cancel(context.Background(), pipelineID)
				return nil
			case <-done:
				_ = manager.Cancel(context.Background(), pipelineID)
				return nil
			case <-ticker.C:
				o.recordDispatchActivity(dispatch.dagID, dispatch.nodeID)
			}
		}
	})
}

func (o *Orchestrator) buildManagedPipelineConfig(dispatch *taskDispatchContext) (tdd.PipelineConfig, error) {
	task := dispatch.pipelineTask(o.config.SessionID)
	decodedTask := &agentshared.PipelineTaskInput{
		NodeID:        task.NodeID,
		DAGID:         task.DAGID,
		TaskID:        task.TaskID,
		AgentType:     task.AgentType,
		TargetAgentID: task.TargetAgentID,
		Prompt:        task.Prompt,
		Context:       task.Context,
		ParentResults: task.ParentResults,
		SessionID:     task.SessionID,
	}

	workerType, ok := managedWorkerType(dispatch.agentType)
	if !ok {
		return tdd.PipelineConfig{}, fmt.Errorf("unsupported managed worker type %q", dispatch.agentType)
	}

	var (
		svfs       = o.GetSessionVFS(task.SessionID)
		workingDir string
	)
	if svfs != nil {
		workingDir = svfs.WorkingDir()
	}

	return tdd.PipelineConfig{
		TaskID:          dispatch.pipelineTaskID,
		TaskSlug:        dispatch.pipelineTaskSlug,
		SessionID:       task.SessionID,
		DAGID:           dispatch.dagID,
		DAGNodeID:       dispatch.nodeID,
		WorkerType:      workerType,
		InitialCriteria: inspectorpipeline.CompileCriteriaFromTask(decodedTask),
		TaskPrompt:      task.Prompt,
		CoWorkerTypes:   managedCoWorkerTypes(dispatch.coAgents),
		AgentPrompts:    managedAgentPrompts(dispatch.nodeCtx),
		SessionVFS:      svfs,
		WorkingDir:      workingDir,
		Files:           extractAffectedFiles(dispatch.nodeCtx),
	}, nil
}

func managedWorkerType(agentType string) (tdd.WorkerType, bool) {
	switch strings.TrimSpace(agentType) {
	case "engineer":
		return tdd.WorkerEngineer, true
	case "designer":
		return tdd.WorkerDesigner, true
	default:
		return "", false
	}
}

func managedCoWorkerTypes(agentTypes []string) []tdd.WorkerType {
	result := make([]tdd.WorkerType, 0, len(agentTypes))
	seen := make(map[tdd.WorkerType]struct{}, len(agentTypes))
	for _, agentType := range agentTypes {
		wt, ok := managedWorkerType(agentType)
		if !ok {
			continue
		}
		if _, exists := seen[wt]; exists {
			continue
		}
		seen[wt] = struct{}{}
		result = append(result, wt)
	}
	return result
}

func managedAgentPrompts(ctx map[string]any) map[tdd.WorkerType]string {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx["agent_prompts"]
	if !ok || raw == nil {
		return nil
	}
	result := map[tdd.WorkerType]string{}
	switch typed := raw.(type) {
	case map[string]string:
		for agentType, prompt := range typed {
			wt, ok := managedWorkerType(agentType)
			if !ok || strings.TrimSpace(prompt) == "" {
				continue
			}
			result[wt] = strings.TrimSpace(prompt)
		}
	case map[string]any:
		for agentType, value := range typed {
			prompt, _ := value.(string)
			wt, ok := managedWorkerType(agentType)
			if !ok || strings.TrimSpace(prompt) == "" {
				continue
			}
			result[wt] = strings.TrimSpace(prompt)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (o *Orchestrator) handleManagedPipelineEvent(evt tdd.PipelineEvent) {
	if o == nil || strings.TrimSpace(evt.DAGNodeID) == "" || tdd.IsTerminalStatus(evt.NewStatus) {
		return
	}
	update := managedPipelineUpdateFromEvent(evt)
	if update == nil {
		return
	}
	publishPipelineUpdateMessage(o.bus, o.config.AgentID, update)
	o.recordDispatchActivity(update.DAGID, update.NodeID)
}

func managedPipelineUpdateFromResult(dispatch *taskDispatchContext, result *tdd.PipelineResult) *PipelineUpdate {
	if dispatch == nil || result == nil {
		return nil
	}

	stage, progress, message := managedPipelineProgressForStatus(result.Status, result.Error)
	status := "running"
	errorText := ""
	output := any(nil)

	switch result.Status {
	case tdd.StatusCompleted:
		status = "succeeded"
		progress = 1
		output = managedPipelineOutput(result)
	case tdd.StatusFailed:
		status = "failed"
		progress = 1
		errorText = strings.TrimSpace(result.Error)
		message = firstNonEmpty(message, errorText, "pipeline failed")
	case tdd.StatusCancelled:
		status = "cancelled"
		progress = 1
		message = firstNonEmpty(message, "pipeline cancelled")
	}

	return &PipelineUpdate{
		DAGID:     dispatch.dagID,
		NodeID:    dispatch.nodeID,
		TaskID:    dispatch.pipelineTaskID,
		AgentID:   pipelineWorkerTargetAgentID(dispatch.pipelineTaskID, dispatch.agentType),
		AgentType: dispatch.agentType,
		Status:    status,
		Stage:     stage,
		Progress:  progress,
		Message:   message,
		Output:    output,
		Error:     errorText,
		Attempt:   result.LoopCount,
		Timestamp: time.Now(),
	}
}

func managedPipelineUpdateFromEvent(evt tdd.PipelineEvent) *PipelineUpdate {
	stage, progress, message := managedPipelineProgressForEvent(evt)
	if strings.TrimSpace(stage) == "" {
		return nil
	}
	return &PipelineUpdate{
		DAGID:     evt.DAGID,
		NodeID:    evt.DAGNodeID,
		TaskID:    evt.TaskID,
		AgentID:   pipelineWorkerTargetAgentID(evt.TaskID, string(evt.WorkerType)),
		AgentType: string(evt.WorkerType),
		Status:    "running",
		Stage:     stage,
		Progress:  progress,
		Message:   firstNonEmpty(strings.TrimSpace(evt.Message), message),
		Attempt:   evt.LoopCount,
		Timestamp: evt.Timestamp,
	}
}

func managedPipelineProgressForStatus(status tdd.PipelineStatus, errorText string) (string, float64, string) {
	switch status {
	case tdd.StatusPending:
		return "", 0, "pipeline queued"
	case tdd.StatusActive:
		return "", 0, ""
	case tdd.StatusCompleted:
		return string(StageExecute), 1, "pipeline completed"
	case tdd.StatusFailed:
		return string(StageExecute), 1, firstNonEmpty(strings.TrimSpace(errorText), "pipeline failed")
	case tdd.StatusCancelled:
		return string(StageExecute), 1, "pipeline cancelled"
	default:
		return "", 0, ""
	}
}

func managedPipelineProgressForEvent(evt tdd.PipelineEvent) (string, float64, string) {
	switch strings.TrimSpace(evt.Stage) {
	case string(StageInspect):
		return string(StageInspect), 0.15, "inspecting current state"
	case string(StageTest):
		return string(StageTest), 0.4, "testing current state"
	case string(StageExecute):
		return string(StageExecute), 0.7, "executing implementation"
	default:
		return "", 0, ""
	}
}

func managedPipelineOutput(result *tdd.PipelineResult) map[string]any {
	output := map[string]any{
		"loop_count": result.LoopCount,
	}
	if result.WorkerOutput != nil && result.WorkerOutput.TaskResult != nil {
		output["worker_output"] = result.WorkerOutput.TaskResult.Output
		output["files_changed"] = append([]string(nil), result.WorkerOutput.ChangedFiles...)
	}
	if result.InspectorResult != nil {
		output["inspector_passed"] = result.InspectorResult.Passed
		output["inspector_issues"] = len(result.InspectorResult.Issues)
	}
	if result.TesterResult != nil {
		output["tester_passed"] = result.TesterResult.Success
	}
	if len(result.CoWorkerOutputs) > 0 {
		coWorkerOutputs := make([]map[string]any, 0, len(result.CoWorkerOutputs))
		for _, co := range result.CoWorkerOutputs {
			if co == nil || co.TaskResult == nil {
				continue
			}
			coWorkerOutputs = append(coWorkerOutputs, map[string]any{
				"worker_type":   string(co.WorkerType),
				"output":        co.TaskResult.Output,
				"files_changed": append([]string(nil), co.ChangedFiles...),
			})
		}
		if len(coWorkerOutputs) > 0 {
			output["co_worker_outputs"] = coWorkerOutputs
		}
	}
	return output
}

func (o *Orchestrator) publishManagedPipelineFailure(dispatch *taskDispatchContext, err error) {
	if dispatch == nil || err == nil {
		return
	}
	publishPipelineUpdateMessage(o.bus, o.config.AgentID, &PipelineUpdate{
		DAGID:     dispatch.dagID,
		NodeID:    dispatch.nodeID,
		TaskID:    dispatch.pipelineTaskID,
		AgentID:   pipelineWorkerTargetAgentID(dispatch.pipelineTaskID, dispatch.agentType),
		AgentType: dispatch.agentType,
		Status:    "failed",
		Progress:  1,
		Message:   err.Error(),
		Error:     err.Error(),
		Timestamp: time.Now(),
	})
}

func publishPipelineUpdateMessage(bus guide.EventBus, sourceAgentID string, update *PipelineUpdate) {
	if bus == nil || update == nil || strings.TrimSpace(update.AgentType) == "" {
		return
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypePipelineUpdate,
		SourceAgentID: sourceAgentID,
		Payload:       update,
		Timestamp:     time.Now(),
	}
	_ = bus.Publish("pipeline.update."+update.AgentType, msg)
}

func (o *Orchestrator) recordDispatchActivity(dagID, nodeID string) {
	if o == nil || o.dagBridge == nil {
		return
	}
	o.dagBridge.RecordDispatchActivity(dagID, nodeID)
}

func (o *Orchestrator) recordPipelineDispatchActivity(update *PipelineUpdate) {
	if update == nil || isTerminalStatus(update.Status) {
		return
	}
	o.recordDispatchActivity(update.DAGID, update.NodeID)
}

func pipelineTaskStateForUpdate(status, stage string) taskstate.Status {
	switch strings.TrimSpace(status) {
	case "running":
		return pipelinePhaseStatus(stage)
	case "succeeded":
		return taskstate.StatusCompleted
	case "failed", "timed_out":
		return taskstate.StatusFailed
	case "cancelled":
		return taskstate.StatusCancelled
	default:
		return ""
	}
}

func (o *Orchestrator) finalizePipelineUpdate(update *PipelineUpdate) {
	if update == nil || strings.TrimSpace(update.TaskID) == "" || !isImplementationPipelineWorker(update.AgentType) {
		return
	}

	task := o.lookupTask(update.TaskID)
	if task == nil {
		return
	}

	switch update.Status {
	case "succeeded":
		if err := o.commitTaskDraft(context.Background(), task); err != nil {
			update.Status = "failed"
			update.Error = err.Error()
			update.Message = firstNonEmpty(update.Message, "draft merge failed")
			publishTaskPipelineState(o.bus, o.config.AgentID, update.TaskID, "", taskstate.StatusFailed, update.AgentType)
		}
	case "failed", "timed_out", "cancelled":
		_ = o.rollbackTaskDraft(task)
	}

	if o.coordination != nil {
		_ = o.coordination.ReleaseTaskClaims(context.Background(), update.TaskID)
	}
}

func isImplementationPipelineWorker(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case "engineer", "designer":
		return true
	default:
		return false
	}
}

func (o *Orchestrator) lookupTask(taskID string) *TaskRecord {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.state.Tasks[taskID]
}
