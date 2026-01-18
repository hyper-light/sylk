package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/security"
)

var (
	ErrPermissionDenied  = errors.New("permission denied")
	ErrTooManyOperations = errors.New("too many concurrent operations")
)

type OperationType int

const (
	OpTypeLLMCall OperationType = iota
	OpTypeToolExecution
	OpTypeFileIO
	OpTypeNetworkIO
)

type Operation struct {
	ID          string
	Type        OperationType
	AgentID     string
	Description string
	StartedAt   time.Time
	Deadline    time.Time

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (o *Operation) Context() context.Context {
	return o.ctx
}

type OperationSupervisor interface {
	AgentID() string
	BeginOperation(opType OperationType, description string, timeout time.Duration) (*Operation, error)
	EndOperation(op *Operation, result any, err error)
	TrackResource(op *Operation, res concurrency.TrackedResource)
}

type Tool interface {
	Name() string
	Command() string
	Args(params map[string]any) []string
	Timeout() time.Duration
	ParseOutput(output []byte) (any, error)
}

type TrackedToolExecutorConfig struct {
	MaxTimeout time.Duration
}

func DefaultTrackedToolExecutorConfig() TrackedToolExecutorConfig {
	return TrackedToolExecutorConfig{
		MaxTimeout: 5 * time.Minute,
	}
}

type TrackedToolExecutor struct {
	permissionMgr *security.PermissionManager
	sandbox       *security.Sandbox
	auditLogger   *security.AuditLogger
	maxTimeout    time.Duration
}

func NewTrackedToolExecutor(
	permMgr *security.PermissionManager,
	sandbox *security.Sandbox,
	auditLogger *security.AuditLogger,
	cfg TrackedToolExecutorConfig,
) *TrackedToolExecutor {
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = 5 * time.Minute
	}
	return &TrackedToolExecutor{
		permissionMgr: permMgr,
		sandbox:       sandbox,
		auditLogger:   auditLogger,
		maxTimeout:    cfg.MaxTimeout,
	}
}

func (e *TrackedToolExecutor) Execute(
	ctx context.Context,
	supervisor OperationSupervisor,
	tool Tool,
	args map[string]any,
) (any, error) {
	timeout := e.normalizeTimeout(tool.Timeout())

	op, err := supervisor.BeginOperation(OpTypeToolExecution, e.buildDescription(tool), timeout)
	if err != nil {
		return nil, err
	}

	result, execErr := e.executeWithTracking(ctx, supervisor, op, tool, args)
	supervisor.EndOperation(op, result, execErr)
	return result, execErr
}

func (e *TrackedToolExecutor) normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > e.maxTimeout {
		return e.maxTimeout
	}
	return timeout
}

func (e *TrackedToolExecutor) buildDescription(tool Tool) string {
	return fmt.Sprintf("tool=%s cmd=%s", tool.Name(), tool.Command())
}

func (e *TrackedToolExecutor) executeWithTracking(
	ctx context.Context,
	supervisor OperationSupervisor,
	op *Operation,
	tool Tool,
	args map[string]any,
) (any, error) {
	if err := e.checkPermission(ctx, supervisor.AgentID(), tool); err != nil {
		return nil, err
	}

	e.logExecution(supervisor.AgentID(), tool)

	proc, err := e.createProcess(op.Context(), tool, args)
	if err != nil {
		return nil, err
	}

	supervisor.TrackResource(op, proc)

	return e.runAndParse(proc, tool)
}

func (e *TrackedToolExecutor) checkPermission(ctx context.Context, agentID string, tool Tool) error {
	if e.permissionMgr == nil {
		return nil
	}

	action := security.PermissionAction{
		Type:   security.ActionTypeCommand,
		Target: tool.Command(),
	}

	result, err := e.permissionMgr.CheckPermission(ctx, agentID, security.RoleWorker, action)
	if err != nil {
		return fmt.Errorf("permission check: %w", err)
	}

	if !result.Allowed {
		return fmt.Errorf("%w: %s", ErrPermissionDenied, result.Reason)
	}

	return nil
}

func (e *TrackedToolExecutor) logExecution(agentID string, tool Tool) {
	if e.auditLogger == nil {
		return
	}

	entry := security.NewAuditEntry(
		security.AuditCategoryProcess,
		"tool_execute",
		"execute",
	)
	entry.AgentID = agentID
	entry.Target = tool.Command()
	entry.Outcome = "started"
	entry.Details = map[string]any{
		"tool": tool.Name(),
	}
	_ = e.auditLogger.Log(entry)
}

func (e *TrackedToolExecutor) createProcess(
	ctx context.Context,
	tool Tool,
	args map[string]any,
) (*TrackedProcess, error) {
	return NewTrackedProcess(ctx, tool.Command(), tool.Args(args), e.sandbox)
}

func (e *TrackedToolExecutor) runAndParse(proc *TrackedProcess, tool Tool) (any, error) {
	output, err := proc.Run()
	if err != nil {
		return nil, fmt.Errorf("process execution: %w", err)
	}
	return tool.ParseOutput(output)
}

func (e *TrackedToolExecutor) MaxTimeout() time.Duration {
	return e.maxTimeout
}
