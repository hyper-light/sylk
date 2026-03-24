package shared

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/commandapproval"
)

func PopulateCommandApprovalScope(ctx context.Context, req *commandapproval.Request) {
	if req == nil {
		return
	}
	if task := PipelineTaskFromContext(ctx); task != nil {
		req.SessionID = firstNonEmpty(strings.TrimSpace(req.SessionID), strings.TrimSpace(task.SessionID))
		req.DAGID = firstNonEmpty(strings.TrimSpace(req.DAGID), strings.TrimSpace(task.DAGID))
		req.NodeID = firstNonEmpty(strings.TrimSpace(req.NodeID), strings.TrimSpace(task.NodeID))
		req.TaskID = firstNonEmpty(strings.TrimSpace(req.TaskID), strings.TrimSpace(task.TaskID))
		req.PipelineID = firstNonEmpty(strings.TrimSpace(req.PipelineID), strings.TrimSpace(task.TaskID))
	}
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		req.DAGID = firstNonEmpty(strings.TrimSpace(req.DAGID), streamMetadataString(stream.Metadata, "dag_id"))
		req.NodeID = firstNonEmpty(strings.TrimSpace(req.NodeID), streamMetadataString(stream.Metadata, "node_id"))
		req.TaskID = firstNonEmpty(strings.TrimSpace(req.TaskID), streamMetadataString(stream.Metadata, "task_id"))
		req.PipelineID = firstNonEmpty(strings.TrimSpace(req.PipelineID), streamMetadataString(stream.Metadata, "pipeline_id"))
	}
	if strings.TrimSpace(req.PipelineID) == "" {
		req.PipelineID = strings.TrimSpace(req.TaskID)
	}
}

func streamMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}
