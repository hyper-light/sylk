package security

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAuditLogger struct {
	mu      sync.Mutex
	granted []string
	denied  []string
}

func (m *mockAuditLogger) LogPermissionGranted(agentID string, action PermissionAction, source string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.granted = append(m.granted, agentID+":"+action.Target+":"+source)
}

func (m *mockAuditLogger) LogPermissionDenied(agentID string, action PermissionAction, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.denied = append(m.denied, agentID+":"+action.Target+":"+reason)
}

func TestRoleHasCapability(t *testing.T) {
	tests := []struct {
		name     string
		role     AgentRole
		action   ActionType
		expected bool
	}{
		{"observer can read paths", RoleObserver, ActionTypePath, true},
		{"observer cannot execute commands", RoleObserver, ActionTypeCommand, false},
		{"worker can execute commands", RoleWorker, ActionTypeCommand, true},
		{"worker can access network", RoleWorker, ActionTypeNetwork, true},
		{"admin can access credentials", RoleAdmin, ActionTypeCredential, true},
		{"worker cannot access credentials", RoleWorker, ActionTypeCredential, false},
		{"supervisor can modify config", RoleSupervisor, ActionTypeConfig, true},
		{"orchestrator can only read paths", RoleOrchestrator, ActionTypePath, true},
		{"orchestrator cannot execute commands", RoleOrchestrator, ActionTypeCommand, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := RoleHasCapability(tc.role, tc.action)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestGetRoleForAgent(t *testing.T) {
	tests := []struct {
		agentType    string
		expectedRole AgentRole
	}{
		{"guide", RoleSupervisor},
		{"engineer", RoleWorker},
		{"librarian", RoleObserverKnowledge},
		{"unknown", RoleObserver},
	}

	for _, tc := range tests {
		t.Run(tc.agentType, func(t *testing.T) {
			role := GetRoleForAgent(tc.agentType)
			assert.Equal(t, tc.expectedRole, role)
		})
	}
}

func TestIsHigherRole(t *testing.T) {
	tests := []struct {
		name     string
		role1    AgentRole
		role2    AgentRole
		expected bool
	}{
		{"admin higher than worker", RoleAdmin, RoleWorker, true},
		{"worker not higher than supervisor", RoleWorker, RoleSupervisor, false},
		{"same role is equal", RoleWorker, RoleWorker, true},
		{"observer lowest", RoleObserver, RoleAdmin, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsHigherRole(tc.role1, tc.role2)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPermissionManager_CheckPermission_RoleCheck(t *testing.T) {
	tmpDir := t.TempDir()
	audit := &mockAuditLogger{}
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
		AuditLogger: audit,
	})

	ctx := context.Background()

	result, err := pm.CheckPermission(ctx, "agent1", RoleObserver, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "ls",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.Contains(t, result.Reason, "role does not permit")
}

func TestPermissionManager_CheckPermission_SafeList(t *testing.T) {
	tmpDir := t.TempDir()
	audit := &mockAuditLogger{}
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
		AuditLogger: audit,
	})

	ctx := context.Background()

	result, err := pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "ls -la",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, "safe_list", result.Source)

	result, err = pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeNetwork,
		Target: "github.com",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, "safe_list", result.Source)
}

func TestPermissionManager_CheckPermission_RequiresApproval(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})

	ctx := context.Background()

	result, err := pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "rm -rf /",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.True(t, result.RequiresApproval)
	assert.Contains(t, result.ApprovalPrompt, "rm")
}

func TestPermissionManager_ApproveAndPersist(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})

	err := pm.ApproveAndPersist(PermissionAction{
		Type:   ActionTypeCommand,
		Target: "custom-cmd --arg",
	})
	require.NoError(t, err)

	permPath := filepath.Join(tmpDir, ".sylk", "local", "permissions.yaml")
	_, err = os.Stat(permPath)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "custom-cmd --other-arg",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, "project_allowlist", result.Source)
}

func TestPermissionManager_CommandMatchingIgnoresArgs(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})

	ctx := context.Background()

	result1, _ := pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "git status",
	})
	result2, _ := pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "git push origin main",
	})

	assert.True(t, result1.Allowed)
	assert.True(t, result2.Allowed)
}

func TestPermissionManager_DomainMatchingExact(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})

	ctx := context.Background()

	result, _ := pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeNetwork,
		Target: "github.com",
	})
	assert.True(t, result.Allowed)

	result, _ = pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeNetwork,
		Target: "evil-github.com",
	})
	assert.False(t, result.Allowed)
	assert.True(t, result.RequiresApproval)
}

func TestAllowlist_LoadAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	al := NewAllowlist(tmpDir)

	al.AllowCommand("test-cmd")
	al.AllowDomain("example.com")
	al.AllowPath("/tmp/test", PathPerm{Read: true, Write: true})

	err := al.Save()
	require.NoError(t, err)

	al2 := NewAllowlist(tmpDir)
	err = al2.Load()
	require.NoError(t, err)

	assert.True(t, al2.IsCommandAllowed("test-cmd"))
	assert.True(t, al2.IsDomainAllowed("example.com"))
	perm, ok := al2.GetPathPerm("/tmp/test")
	assert.True(t, ok)
	assert.True(t, perm.Write)
}

func TestAllowlist_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	al := NewAllowlist(tmpDir)

	err := al.Load()
	require.NoError(t, err)
}

func TestAllowlist_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	al := NewAllowlist(tmpDir)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			al.AllowCommand("cmd-" + string(rune('a'+i%26)))
			al.IsCommandAllowed("cmd-a")
		}(i)
	}
	wg.Wait()
}

func TestDefaultSafeCommands(t *testing.T) {
	cmds := DefaultSafeCommands()

	assert.True(t, cmds["ls"])
	assert.True(t, cmds["git"])
	assert.True(t, cmds["go"])
	assert.False(t, cmds["rm"])
}

func TestDefaultSafeDomains(t *testing.T) {
	domains := DefaultSafeDomains()

	assert.True(t, domains["github.com"])
	assert.True(t, domains["pkg.go.dev"])
	assert.False(t, domains["malware.com"])
}

func TestPathPermLevel(t *testing.T) {
	tests := []struct {
		perm     PathPerm
		expected int
	}{
		{PathPerm{}, 0},
		{PathPerm{Read: true}, 1},
		{PathPerm{Read: true, Write: true}, 2},
		{PathPerm{Read: true, Write: true, Delete: true}, 3},
	}

	for _, tc := range tests {
		result := pathPermLevel(tc.perm)
		assert.Equal(t, tc.expected, result)
	}
}

func TestPathPermSatisfies(t *testing.T) {
	tests := []struct {
		have     PathPerm
		need     PathPerm
		expected bool
	}{
		{PathPerm{Read: true}, PathPerm{Read: true}, true},
		{PathPerm{Read: true}, PathPerm{Write: true}, false},
		{PathPerm{Read: true, Write: true}, PathPerm{Read: true}, true},
		{PathPerm{Read: true, Write: true, Delete: true}, PathPerm{Delete: true}, true},
	}

	for _, tc := range tests {
		result := pathPermSatisfies(tc.have, tc.need)
		assert.Equal(t, tc.expected, result)
	}
}

func TestExtractBaseCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ls", "ls"},
		{"ls -la", "ls"},
		{"git status --short", "git"},
		{"", ""},
		{"  spaces  ", "spaces"},
	}

	for _, tc := range tests {
		result := extractBaseCommand(tc.input)
		assert.Equal(t, tc.expected, result)
	}
}

func TestPipelinePermissionManager_WorkflowPerms(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})
	ppm := NewPipelinePermissionManager(pm)
	defer ppm.Close()

	ppm.RegisterWorkflow("wf-1", &WorkflowPermissions{
		Commands: []string{"custom-build"},
		Domains:  []string{"internal.api.com"},
	})

	ctx := context.Background()
	result, err := ppm.CheckPipelinePermission(ctx, "wf-1", "agent1", PermissionAction{
		Type:   ActionTypeCommand,
		Target: "custom-build --prod",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, "workflow_declaration", result.Source)

	result, err = ppm.CheckPipelinePermission(ctx, "wf-1", "agent1", PermissionAction{
		Type:   ActionTypeNetwork,
		Target: "internal.api.com",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)

	result, err = ppm.CheckPipelinePermission(ctx, "wf-1", "agent1", PermissionAction{
		Type:   ActionTypeCommand,
		Target: "unknown-cmd",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.True(t, result.RequiresApproval)
}

func TestPipelinePermissionManager_UnregisterWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})
	ppm := NewPipelinePermissionManager(pm)
	defer ppm.Close()

	ppm.RegisterWorkflow("wf-1", &WorkflowPermissions{
		Commands: []string{"test-cmd"},
	})
	ppm.UnregisterWorkflow("wf-1")

	ctx := context.Background()
	result, err := ppm.CheckPipelinePermission(ctx, "wf-1", "agent1", PermissionAction{
		Type:   ActionTypeCommand,
		Target: "test-cmd",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
}

func TestPipelinePermissionManager_RuntimeEscalation(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})
	ppm := NewPipelinePermissionManager(pm)
	defer ppm.Close()

	ppm.RegisterWorkflow("wf-1", &WorkflowPermissions{
		AllowRuntimeEscalation: true,
	})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		req := <-ppm.PendingEscalations()
		req.Response <- EscalationResponse{Approved: true, Persist: false}
	}()

	result, err := ppm.CheckPipelinePermission(ctx, "wf-1", "agent1", PermissionAction{
		Type:   ActionTypeCommand,
		Target: "escalated-cmd",
	})
	cancel()

	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, "runtime_escalation", result.Source)
}

func TestPipelinePermissionManager_EscalationDenied(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})
	ppm := NewPipelinePermissionManager(pm)
	defer ppm.Close()

	ppm.RegisterWorkflow("wf-1", &WorkflowPermissions{
		AllowRuntimeEscalation: true,
	})

	ctx := context.Background()

	go func() {
		req := <-ppm.PendingEscalations()
		req.Response <- EscalationResponse{Approved: false}
	}()

	result, err := ppm.CheckPipelinePermission(ctx, "wf-1", "agent1", PermissionAction{
		Type:   ActionTypeCommand,
		Target: "denied-cmd",
	})

	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.Equal(t, "user denied", result.Reason)
}

func TestPipelinePermissionManager_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
	})
	ppm := NewPipelinePermissionManager(pm)
	defer ppm.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wfID := "wf-" + string(rune('a'+i%10))
			ppm.RegisterWorkflow(wfID, &WorkflowPermissions{
				Commands: []string{"cmd-" + string(rune('a'+i%26))},
			})
			ctx := context.Background()
			_, _ = ppm.CheckPipelinePermission(ctx, wfID, "agent1", PermissionAction{
				Type:   ActionTypeCommand,
				Target: "ls",
			})
		}(i)
	}
	wg.Wait()
}

func TestAuditLogging(t *testing.T) {
	tmpDir := t.TempDir()
	audit := &mockAuditLogger{}
	pm := NewPermissionManager(PermissionManagerConfig{
		ProjectPath: tmpDir,
		AuditLogger: audit,
	})

	ctx := context.Background()

	_, _ = pm.CheckPermission(ctx, "agent1", RoleWorker, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "ls",
	})

	audit.mu.Lock()
	assert.Len(t, audit.granted, 1)
	assert.Contains(t, audit.granted[0], "agent1:ls:safe_list")
	audit.mu.Unlock()

	_, _ = pm.CheckPermission(ctx, "agent2", RoleObserver, PermissionAction{
		Type:   ActionTypeCommand,
		Target: "any",
	})

	audit.mu.Lock()
	assert.Len(t, audit.denied, 1)
	assert.Contains(t, audit.denied[0], "agent2:any:role_insufficient")
	audit.mu.Unlock()
}
