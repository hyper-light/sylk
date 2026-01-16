package dag

import "context"

//go:generate mockery --name=NodeDispatcherService --output=./mocks --outpkg=mocks
//go:generate mockery --name=SchedulerService --output=./mocks --outpkg=mocks
//go:generate mockery --name=ExecutorService --output=./mocks --outpkg=mocks

type NodeDispatcherService interface {
	Dispatch(ctx context.Context, node *Node, parentResults map[string]*NodeResult) (*NodeResult, error)
}

type SchedulerService interface {
	Submit(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (string, error)
	SubmitAndWait(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error)
	Cancel(id string) error
	Status(id string) (*DAGStatus, error)
	List() []*DAGStatus
	Stats() SchedulerStats
	Subscribe(handler EventHandler) func()
	Close() error
}

type ExecutorService interface {
	Execute(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error)
	Cancel() error
	Status() *DAGStatus
	Subscribe(handler EventHandler) func()
}
