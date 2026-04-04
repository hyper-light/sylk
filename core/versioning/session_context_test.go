package versioning

import (
	"context"
	"testing"
)

func TestTaskIDContextHelpers(t *testing.T) {
	t.Parallel()

	ctx := WithTaskID(WithSessionID(context.Background(), "sess-1"), "task-1")
	if got := SessionIDFromContext(ctx); got != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", got)
	}
	if got := TaskIDFromContext(ctx); got != "task-1" {
		t.Fatalf("task_id = %q, want task-1", got)
	}
}
