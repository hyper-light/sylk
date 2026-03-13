package shared

import (
	"context"

	"github.com/adalundhe/sylk/core/versioning"
)

func EnrichTaskExecutionContractWithWorkspaceEvidence(
	ctx context.Context,
	contract *TaskExecutionContract,
	views versioning.WorkspaceViewAccess,
	task *PipelineTaskInput,
) *TaskExecutionContract {
	if contract == nil || views == nil || task == nil {
		return contract
	}
	if workspaceShowsImplementationEvidence(ctx, views, task) {
		contract.HasImplementationEvidence = true
	}
	return RebuildTaskExecutionContract(task, contract)
}

func workspaceShowsImplementationEvidence(
	ctx context.Context,
	views versioning.WorkspaceViewAccess,
	task *PipelineTaskInput,
) bool {
	if views == nil || task == nil {
		return false
	}
	for _, path := range collectTaskWorkspacePaths(task) {
		state, err := views.InspectPath(ctx, path, task.TaskID)
		if err != nil {
			continue
		}
		if workspacePathShowsImplementationEvidence(state) {
			return true
		}
	}
	return false
}

func workspacePathShowsImplementationEvidence(state *versioning.WorkspacePathState) bool {
	if state == nil {
		return false
	}
	if workspaceLayerValueShowsImplementationEvidence(state.Disk) {
		return true
	}
	if workspaceLayerShowsImplementationEvidence(state.Global) {
		return true
	}
	return workspaceLayerShowsImplementationEvidence(state.Pipeline)
}

func workspaceLayerValueShowsImplementationEvidence(state versioning.WorkspaceLayerState) bool {
	return state.Available && state.Exists
}

func workspaceLayerShowsImplementationEvidence(state *versioning.WorkspaceLayerState) bool {
	if state == nil {
		return false
	}
	return workspaceLayerValueShowsImplementationEvidence(*state)
}
