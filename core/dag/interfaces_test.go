package dag_test

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/dag"
)

func TestInterfaceImplementations(t *testing.T) {
	var _ dag.NodeDispatcherService = (*testNodeDispatcher)(nil)
	var _ dag.SchedulerService = (*dag.Scheduler)(nil)
	var _ dag.ExecutorService = (*dag.Executor)(nil)
}

type testNodeDispatcher struct{}

func (testNodeDispatcher) Dispatch(_ context.Context, _ *dag.Node, _ map[string]*dag.NodeResult) (*dag.NodeResult, error) {
	return &dag.NodeResult{State: dag.NodeStateSucceeded}, nil
}
