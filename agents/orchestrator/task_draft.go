package orchestrator

import (
	"context"
	"fmt"
	"strings"
)

func (o *Orchestrator) commitTaskDraft(ctx context.Context, task *TaskRecord) error {
	if task == nil || strings.TrimSpace(task.SessionID) == "" {
		return nil
	}
	svfs := o.GetSessionVFS(task.SessionID)
	if svfs == nil || !svfs.HasPipeline(task.ID) {
		return nil
	}
	if _, err := svfs.CommitPipeline(ctx, task.ID); err != nil {
		_, _ = svfs.RollbackPipelineIfTracked(task.ID)
		return fmt.Errorf("commit task draft %s: %w", task.ID, err)
	}
	return nil
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
