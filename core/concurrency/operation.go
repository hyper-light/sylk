package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type OperationType int

const (
	OpTypeLLMCall OperationType = iota
	OpTypeToolExecution
	OpTypeFileIO
	OpTypeNetworkIO
)

var operationTypeNames = map[OperationType]string{
	OpTypeLLMCall:       "llm_call",
	OpTypeToolExecution: "tool_execution",
	OpTypeFileIO:        "file_io",
	OpTypeNetworkIO:     "network_io",
}

func (t OperationType) String() string {
	if name, ok := operationTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

type Operation struct {
	ID          string
	Type        OperationType
	AgentID     string
	Description string
	StartedAt   time.Time
	Deadline    time.Time

	ctx    context.Context
	cancel context.CancelFunc

	resourcesMu sync.Mutex
	resources   []TrackedResource

	result atomic.Pointer[any]
	err    atomic.Pointer[error]

	done     chan struct{}
	doneOnce sync.Once
}

func NewOperation(
	ctx context.Context,
	opType OperationType,
	agentID string,
	description string,
	timeout time.Duration,
) *Operation {
	opCtx, cancel := context.WithTimeout(ctx, timeout)

	return &Operation{
		ID:          uuid.NewString(),
		Type:        opType,
		AgentID:     agentID,
		Description: description,
		StartedAt:   time.Now(),
		Deadline:    time.Now().Add(timeout),
		ctx:         opCtx,
		cancel:      cancel,
		resources:   make([]TrackedResource, 0),
		done:        make(chan struct{}),
	}
}

func (o *Operation) Context() context.Context {
	return o.ctx
}

func (o *Operation) Cancel() {
	o.cancel()
}

func (o *Operation) AddResource(res TrackedResource) {
	o.resourcesMu.Lock()
	defer o.resourcesMu.Unlock()
	o.resources = append(o.resources, res)
}

func (o *Operation) ReleaseResources(tracker *ResourceTracker) {
	o.resourcesMu.Lock()
	defer o.resourcesMu.Unlock()

	for _, res := range o.resources {
		tracker.Release(res)
	}
	o.resources = nil
}

func (o *Operation) Resources() []TrackedResource {
	o.resourcesMu.Lock()
	defer o.resourcesMu.Unlock()

	result := make([]TrackedResource, len(o.resources))
	copy(result, o.resources)
	return result
}

func (o *Operation) SetResult(result any, err error) {
	o.result.Store(&result)
	if err != nil {
		o.err.Store(&err)
	}
}

func (o *Operation) Result() (any, error) {
	resultPtr := o.result.Load()
	errPtr := o.err.Load()

	var result any
	var err error

	if resultPtr != nil {
		result = *resultPtr
	}
	if errPtr != nil {
		err = *errPtr
	}

	return result, err
}

func (o *Operation) MarkDone() {
	o.doneOnce.Do(func() {
		close(o.done)
	})
}

func (o *Operation) Done() <-chan struct{} {
	return o.done
}

func (o *Operation) IsDone() bool {
	select {
	case <-o.done:
		return true
	default:
		return false
	}
}

type OrphanedOperation struct {
	ID          string
	Type        OperationType
	AgentID     string
	Description string
	StartedAt   time.Time
	Deadline    time.Time
	Resources   []string
}

func (o *Operation) ToOrphaned() OrphanedOperation {
	o.resourcesMu.Lock()
	resourceIDs := make([]string, len(o.resources))
	for i, res := range o.resources {
		resourceIDs[i] = res.ID()
	}
	o.resourcesMu.Unlock()

	return OrphanedOperation{
		ID:          o.ID,
		Type:        o.Type,
		AgentID:     o.AgentID,
		Description: o.Description,
		StartedAt:   o.StartedAt,
		Deadline:    o.Deadline,
		Resources:   resourceIDs,
	}
}
