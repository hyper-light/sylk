package archivalist

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowStore_StoreDAGAndHistory(t *testing.T) {
	archivalist, err := New(Config{})
	require.NoError(t, err)

	workflowStore := NewWorkflowStore(archivalist)
	workflow := dag.NewDAG("test", dag.DefaultExecutionPolicy())

	stored, err := workflowStore.StoreDAG(context.Background(), "session-1", workflow)
	require.NoError(t, err)
	assert.Equal(t, workflow.ID(), stored.ID)

	run := DAGRun{
		ID:        "run-1",
		DAGID:     workflow.ID(),
		SessionID: "session-1",
		Status:    "success",
	}
	err = workflowStore.RecordRun(context.Background(), run)
	require.NoError(t, err)

	history, err := workflowStore.GetSessionHistory(context.Background(), WorkflowQuery{SessionID: "session-1"})
	require.NoError(t, err)
	assert.NotEmpty(t, history)
}
