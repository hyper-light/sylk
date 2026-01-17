package security

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrRoleInsufficient = errors.New("role does not permit this action")
	ErrPermissionDenied = errors.New("permission denied")
	ErrApprovalRequired = errors.New("user approval required")
	ErrInvalidAction    = errors.New("invalid permission action")
)

type PermissionAction struct {
	Type     ActionType
	Target   string
	PathPerm PathPerm
}

type PermissionResult struct {
	Allowed          bool
	Source           string
	Reason           string
	RequiresApproval bool
	ApprovalPrompt   string
}

type PermissionManagerConfig struct {
	ProjectPath    string
	CustomSafeList string
	AuditLogger    AuditLoggerInterface
}

type AuditLoggerInterface interface {
	LogPermissionGranted(agentID string, action PermissionAction, source string)
	LogPermissionDenied(agentID string, action PermissionAction, reason string)
}

type PermissionManager struct {
	mu sync.RWMutex

	projectPath string
	projectID   string
	allowlist   *Allowlist

	safeCommands     map[string]bool
	safeDomains      map[string]bool
	safePathPatterns []string

	auditLog AuditLoggerInterface
}

func NewPermissionManager(cfg PermissionManagerConfig) *PermissionManager {
	pm := &PermissionManager{
		projectPath:      cfg.ProjectPath,
		projectID:        filepath.Base(cfg.ProjectPath),
		allowlist:        NewAllowlist(cfg.ProjectPath),
		safeCommands:     DefaultSafeCommands(),
		safeDomains:      DefaultSafeDomains(),
		safePathPatterns: DefaultSafePathPatterns(),
		auditLog:         cfg.AuditLogger,
	}
	return pm
}

func (pm *PermissionManager) Load() error {
	return pm.allowlist.Load()
}

func (pm *PermissionManager) CheckPermission(
	ctx context.Context,
	agentID string,
	agentRole AgentRole,
	action PermissionAction,
) (PermissionResult, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if !RoleHasCapability(agentRole, action.Type) {
		pm.logDenied(agentID, action, "role_insufficient")
		return PermissionResult{
			Allowed: false,
			Reason:  ErrRoleInsufficient.Error(),
		}, nil
	}

	if pm.isInSafeList(action) {
		pm.logGranted(agentID, action, "safe_list")
		return PermissionResult{Allowed: true, Source: "safe_list"}, nil
	}

	if pm.isInProjectAllowlist(action) {
		pm.logGranted(agentID, action, "project_allowlist")
		return PermissionResult{Allowed: true, Source: "project_allowlist"}, nil
	}

	return PermissionResult{
		Allowed:          false,
		RequiresApproval: true,
		ApprovalPrompt:   pm.formatApprovalPrompt(action),
	}, nil
}

func (pm *PermissionManager) isInSafeList(action PermissionAction) bool {
	switch action.Type {
	case ActionTypeCommand:
		return pm.isCommandSafe(action.Target)
	case ActionTypeNetwork:
		return pm.isDomainSafe(action.Target)
	case ActionTypePath:
		return pm.isPathSafe(action.Target)
	default:
		return false
	}
}

func (pm *PermissionManager) isCommandSafe(cmd string) bool {
	baseCmd := extractBaseCommand(cmd)
	return pm.safeCommands[baseCmd]
}

func (pm *PermissionManager) isDomainSafe(domain string) bool {
	return pm.safeDomains[domain]
}

func (pm *PermissionManager) isPathSafe(path string) bool {
	for _, pattern := range pm.safePathPatterns {
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}

func (pm *PermissionManager) isInProjectAllowlist(action PermissionAction) bool {
	switch action.Type {
	case ActionTypeCommand:
		baseCmd := extractBaseCommand(action.Target)
		return pm.allowlist.IsCommandAllowed(baseCmd)
	case ActionTypeNetwork:
		return pm.allowlist.IsDomainAllowed(action.Target)
	case ActionTypePath:
		return pm.checkPathAllowlist(action.Target, action.PathPerm)
	default:
		return false
	}
}

func (pm *PermissionManager) checkPathAllowlist(path string, required PathPerm) bool {
	perm, ok := pm.allowlist.GetPathPerm(path)
	if !ok {
		return false
	}
	return pathPermSatisfies(perm, required)
}

func pathPermSatisfies(have, need PathPerm) bool {
	return pathPermLevel(have) >= pathPermLevel(need)
}

func pathPermLevel(p PathPerm) int {
	if p.Delete {
		return 3
	}
	if p.Write {
		return 2
	}
	if p.Read {
		return 1
	}
	return 0
}

func (pm *PermissionManager) formatApprovalPrompt(action PermissionAction) string {
	switch action.Type {
	case ActionTypeCommand:
		return fmt.Sprintf("Allow command: %s", action.Target)
	case ActionTypeNetwork:
		return fmt.Sprintf("Allow network access to: %s", action.Target)
	case ActionTypePath:
		return fmt.Sprintf("Allow path access: %s (%s)", action.Target, serializePathPerm(action.PathPerm))
	default:
		return fmt.Sprintf("Allow action: %s on %s", action.Type, action.Target)
	}
}

func (pm *PermissionManager) ApproveAndPersist(action PermissionAction) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	switch action.Type {
	case ActionTypeCommand:
		baseCmd := extractBaseCommand(action.Target)
		pm.allowlist.AllowCommand(baseCmd)
	case ActionTypeNetwork:
		pm.allowlist.AllowDomain(action.Target)
	case ActionTypePath:
		pm.allowlist.AllowPath(action.Target, action.PathPerm)
	default:
		return ErrInvalidAction
	}

	return pm.allowlist.Save()
}

func (pm *PermissionManager) logGranted(agentID string, action PermissionAction, source string) {
	if pm.auditLog != nil {
		pm.auditLog.LogPermissionGranted(agentID, action, source)
	}
}

func (pm *PermissionManager) logDenied(agentID string, action PermissionAction, reason string) {
	if pm.auditLog != nil {
		pm.auditLog.LogPermissionDenied(agentID, action, reason)
	}
}

func (pm *PermissionManager) GetAllowlist() *Allowlist {
	return pm.allowlist
}

func extractBaseCommand(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
