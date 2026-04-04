package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

func (o *Orchestrator) commitTaskDraft(ctx context.Context, task *TaskRecord) (versioning.SemanticVersion, bool, error) {
	if task == nil || strings.TrimSpace(task.SessionID) == "" {
		return versioning.SemanticVersion{}, false, nil
	}
	svfs := o.GetSessionVFS(task.SessionID)
	if svfs == nil || !svfs.HasPipeline(task.ID) {
		return versioning.SemanticVersion{}, false, nil
	}
	ver, err := svfs.CommitPipeline(ctx, task.ID)
	if err != nil {
		_, _ = svfs.RollbackPipelineIfTracked(task.ID)
		return versioning.SemanticVersion{}, true, fmt.Errorf("commit task draft %s: %w", task.ID, err)
	}
	return ver, true, nil
}

func (o *Orchestrator) rollbackTaskDraft(task *TaskRecord) error {
	if task == nil || strings.TrimSpace(task.SessionID) == "" {
		return nil
	}
	svfs := o.GetSessionVFS(task.SessionID)
	if svfs == nil {
		return nil
	}
	_, err := svfs.RollbackPipelineIfTracked(task.ID)
	return err
}

func (o *Orchestrator) extractTaskReviewCandidate(ctx context.Context, task *TaskRecord) (versioning.SemanticVersion, string, bool, error) {
	if task == nil || strings.TrimSpace(task.SessionID) == "" {
		return versioning.SemanticVersion{}, "", false, nil
	}
	svfs := o.GetSessionVFS(task.SessionID)
	if svfs == nil {
		return versioning.SemanticVersion{}, "", false, nil
	}
	if !svfs.HasPipeline(task.ID) {
		return svfs.CurrentVersion(), "", false, nil
	}
	candidate, err := svfs.ExtractReviewCandidate(strings.TrimSpace(task.ID))
	if err != nil {
		return versioning.SemanticVersion{}, "", true, fmt.Errorf("extract review candidate %s: %w", task.ID, err)
	}
	candidateID := ""
	if candidate != nil {
		candidateID = strings.TrimSpace(candidate.ID)
	}
	return svfs.CurrentVersion(), candidateID, candidate != nil, nil
}
