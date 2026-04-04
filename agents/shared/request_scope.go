package shared

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

// WithForwardedTaskScope stamps task-scoped routing metadata into ctx so
// downstream shared infrastructure can resolve the active task horizon.
func WithForwardedTaskScope(ctx context.Context, metadata map[string]any) context.Context {
	if ctx == nil || len(metadata) == 0 {
		return ctx
	}
	for _, key := range []string{"task_id", "pipeline_id"} {
		value, _ := metadata[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return versioning.WithTaskID(ctx, value)
		}
	}
	return ctx
}

// RouteMetadataWithTaskScope stamps the current task scope into outgoing route
// metadata so child consult/challenge requests preserve the same task horizon
// as the parent request.
func RouteMetadataWithTaskScope(ctx context.Context, metadata map[string]any) map[string]any {
	if ctx == nil {
		return metadata
	}

	taskID := strings.TrimSpace(versioning.TaskIDFromContext(ctx))
	if taskID == "" {
		if stream, ok := StreamMetadataFromContext(ctx); ok {
			taskID = firstNonEmpty(
				streamMetadataString(stream.Metadata, "task_id"),
				streamMetadataString(stream.Metadata, "pipeline_id"),
			)
		}
	}
	if taskID == "" {
		return metadata
	}

	if existing, ok := metadata["task_id"].(string); ok && strings.TrimSpace(existing) != "" {
		return metadata
	}

	cloned := CloneMetadataMap(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 1)
	}
	cloned["task_id"] = taskID
	return cloned
}
