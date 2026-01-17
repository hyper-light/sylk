package security

import (
	"context"
	"sync"
)

type WorkflowPermissions struct {
	Commands               []string `json:"commands"`
	Domains                []string `json:"domains"`
	Paths                  []string `json:"paths"`
	InheritFromParent      bool     `json:"inherit_from_parent"`
	AllowRuntimeEscalation bool     `json:"allow_runtime_escalation"`
}

type EscalationRequest struct {
	WorkflowID string
	Action     PermissionAction
	Response   chan EscalationResponse
}

type EscalationResponse struct {
	Approved bool
	Persist  bool
}

type PipelinePermissionManager struct {
	mu          sync.RWMutex
	baseManager *PermissionManager

	workflowPerms      map[string]*WorkflowPermissions
	pendingEscalations chan EscalationRequest
}

func NewPipelinePermissionManager(base *PermissionManager) *PipelinePermissionManager {
	return &PipelinePermissionManager{
		baseManager:        base,
		workflowPerms:      make(map[string]*WorkflowPermissions),
		pendingEscalations: make(chan EscalationRequest, 100),
	}
}

func (ppm *PipelinePermissionManager) RegisterWorkflow(workflowID string, perms *WorkflowPermissions) {
	ppm.mu.Lock()
	defer ppm.mu.Unlock()
	ppm.workflowPerms[workflowID] = perms
}

func (ppm *PipelinePermissionManager) UnregisterWorkflow(workflowID string) {
	ppm.mu.Lock()
	defer ppm.mu.Unlock()
	delete(ppm.workflowPerms, workflowID)
}

func (ppm *PipelinePermissionManager) CheckPipelinePermission(
	ctx context.Context,
	workflowID string,
	agentID string,
	action PermissionAction,
) (PermissionResult, error) {
	perms := ppm.getWorkflowPerms(workflowID)

	if perms != nil && ppm.actionInWorkflowPerms(action, perms) {
		return PermissionResult{Allowed: true, Source: "workflow_declaration"}, nil
	}

	result, err := ppm.baseManager.CheckPermission(ctx, agentID, RoleWorker, action)
	if err != nil {
		return result, err
	}

	return ppm.handleEscalationIfNeeded(ctx, workflowID, perms, action, result)
}

func (ppm *PipelinePermissionManager) getWorkflowPerms(workflowID string) *WorkflowPermissions {
	ppm.mu.RLock()
	defer ppm.mu.RUnlock()
	return ppm.workflowPerms[workflowID]
}

func (ppm *PipelinePermissionManager) handleEscalationIfNeeded(
	ctx context.Context,
	workflowID string,
	perms *WorkflowPermissions,
	action PermissionAction,
	result PermissionResult,
) (PermissionResult, error) {
	if !result.RequiresApproval {
		return result, nil
	}
	if perms == nil || !perms.AllowRuntimeEscalation {
		return result, nil
	}
	return ppm.queueEscalation(ctx, workflowID, action)
}

func (ppm *PipelinePermissionManager) actionInWorkflowPerms(action PermissionAction, perms *WorkflowPermissions) bool {
	switch action.Type {
	case ActionTypeCommand:
		return containsPrefix(perms.Commands, extractBaseCommand(action.Target))
	case ActionTypeNetwork:
		return contains(perms.Domains, action.Target)
	case ActionTypePath:
		return contains(perms.Paths, action.Target)
	default:
		return false
	}
}

func (ppm *PipelinePermissionManager) queueEscalation(
	ctx context.Context,
	workflowID string,
	action PermissionAction,
) (PermissionResult, error) {
	req := ppm.createEscalationRequest(workflowID, action)

	if err := ppm.sendEscalationRequest(ctx, req); err != nil {
		return PermissionResult{Allowed: false, Reason: "context cancelled"}, err
	}

	return ppm.waitForEscalationResponse(ctx, req.Response, action)
}

func (ppm *PipelinePermissionManager) createEscalationRequest(workflowID string, action PermissionAction) EscalationRequest {
	return EscalationRequest{
		WorkflowID: workflowID,
		Action:     action,
		Response:   make(chan EscalationResponse, 1),
	}
}

func (ppm *PipelinePermissionManager) sendEscalationRequest(ctx context.Context, req EscalationRequest) error {
	select {
	case ppm.pendingEscalations <- req:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ppm *PipelinePermissionManager) waitForEscalationResponse(
	ctx context.Context,
	respChan chan EscalationResponse,
	action PermissionAction,
) (PermissionResult, error) {
	select {
	case resp := <-respChan:
		return ppm.processEscalationResponse(resp, action), nil
	case <-ctx.Done():
		return PermissionResult{Allowed: false, Reason: "context cancelled"}, ctx.Err()
	}
}

func (ppm *PipelinePermissionManager) processEscalationResponse(resp EscalationResponse, action PermissionAction) PermissionResult {
	if !resp.Approved {
		return PermissionResult{Allowed: false, Reason: "user denied"}
	}
	if resp.Persist {
		_ = ppm.baseManager.ApproveAndPersist(action)
	}
	return PermissionResult{Allowed: true, Source: "runtime_escalation"}
}

func (ppm *PipelinePermissionManager) PendingEscalations() <-chan EscalationRequest {
	return ppm.pendingEscalations
}

func (ppm *PipelinePermissionManager) Close() {
	close(ppm.pendingEscalations)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsPrefix(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
