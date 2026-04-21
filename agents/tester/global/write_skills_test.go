package global

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestGlobalTesterWriteSkillsUseLeasedGlobalWrites(t *testing.T) {
	gt, fa, ctx := newGlobalTesterWriteHarness(t)

	// Phase 2.K / GT-2 refactor: write_integration_test + write_e2e_test
	// collapsed into write_test(level=…).
	cases := []struct {
		name   string
		level  string
		output string
	}{
		{name: "write_test_unit", level: "unit", output: "pkg/service/service_test.go"},
		{name: "write_test_integration", level: "integration", output: "pkg/service/service_integration_test.go"},
		{name: "write_test_e2e", level: "e2e", output: "pkg/service/service_e2e_test.go"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			basis := prepareGlobalWriteBasis(t, gt, ctx, tc.output)
			input, err := json.Marshal(map[string]any{
				"level": tc.level,
				"test_case": map[string]any{
					"name":        "TestService",
					"target_file": "pkg/service/service.go",
				},
				"target_file": "pkg/service/service.go",
				"output_file": tc.output,
				"content":     "package service\n\nimport \"testing\"\n\nfunc TestService(t *testing.T) {}\n",
				"basis":       basis,
			})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			result := gt.skills.Invoke(ctx, "write_test", input)
			if !result.Success {
				t.Fatalf("write_test(level=%s) failed: %s", tc.level, result.Error)
			}

			data, ok := result.Data.(map[string]any)
			if !ok {
				t.Fatalf("result type = %T, want map[string]any", result.Data)
			}
			nextBasisRaw, ok := data["next_basis"]
			if !ok {
				t.Fatalf("write_test(level=%s) result missing next_basis", tc.level)
			}
			nextBasis, ok := nextBasisRaw.(*versioning.WorkspaceWriteBasis)
			if !ok {
				t.Fatalf("next_basis type = %T, want *WorkspaceWriteBasis", nextBasisRaw)
			}
			if nextBasis.Scope != versioning.WorkspaceWriteScopeGlobal {
				t.Fatalf("next_basis scope = %q, want %q", nextBasis.Scope, versioning.WorkspaceWriteScopeGlobal)
			}
			if gotLevel, _ := data["level"].(string); gotLevel != tc.level {
				t.Fatalf("result level = %q, want %q", gotLevel, tc.level)
			}

			content, err := fa.ReadFile(ctx, tc.output)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(content) == "" {
				t.Fatalf("write_test(level=%s) wrote empty content", tc.level)
			}
		})
	}
}

func TestGlobalTesterWriteTestAutoRenewsExpiredLease(t *testing.T) {
	gt, fa, ctx := newGlobalTesterWriteHarness(t)

	basis := prepareGlobalWriteBasis(t, gt, ctx, "pkg/service/service_test.go")
	basis.LeaseExpiresAt = time.Now().UTC().Add(-time.Second)

	input, err := json.Marshal(map[string]any{
		"test_case": map[string]any{
			"name":        "TestServiceLease",
			"target_file": "pkg/service/service.go",
		},
		"target_file": "pkg/service/service.go",
		"output_file": "pkg/service/service_test.go",
		"content":     "package service\n\nimport \"testing\"\n\nfunc TestServiceLease(t *testing.T) {}\n",
		"basis":       basis,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result := gt.skills.Invoke(ctx, "write_test", input)
	if !result.Success {
		t.Fatalf("write_test failed: %s", result.Error)
	}

	data := result.Data.(map[string]any)
	nextBasis, ok := data["next_basis"].(*versioning.WorkspaceWriteBasis)
	if !ok {
		t.Fatalf("next_basis type = %T, want *WorkspaceWriteBasis", data["next_basis"])
	}
	if !nextBasis.LeaseExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("lease_expires_at = %s, want future time", nextBasis.LeaseExpiresAt)
	}

	content, err := fa.ReadFile(ctx, "pkg/service/service_test.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected written test content")
	}
}

func TestGlobalTesterWriteTestAutoRenewsExpiredLeaseWithVisibleInternalToolSteps(t *testing.T) {
	gt, _, baseCtx := newGlobalTesterWriteHarness(t)

	var emitted []agentshared.ToolCallEvent
	ctx := agentshared.WithToolCallEmitter(baseCtx, func(event agentshared.ToolCallEvent) error {
		emitted = append(emitted, event)
		return nil
	})

	basis := prepareGlobalWriteBasis(t, gt, ctx, "pkg/service/service_test.go")
	basis.LeaseExpiresAt = time.Now().UTC().Add(-time.Second)
	emitted = nil

	input, err := json.Marshal(map[string]any{
		"test_case": map[string]any{
			"name":        "TestServiceLeaseVisible",
			"target_file": "pkg/service/service.go",
		},
		"target_file": "pkg/service/service.go",
		"output_file": "pkg/service/service_test.go",
		"content":     "package service\n\nimport \"testing\"\n\nfunc TestServiceLeaseVisible(t *testing.T) {}\n",
		"basis":       basis,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result := gt.skills.Invoke(ctx, "write_test", input)
	if !result.Success {
		t.Fatalf("write_test failed: %s", result.Error)
	}

	// prepare_write_context folded into workspace_read(op=prepare_write);
	// both the prep and the write now emit under workspace_read /
	// workspace_write tool names.
	if !hasGlobalToolEvent(emitted, "workspace_write", agentshared.ToolCallStart, false) {
		t.Fatalf("expected workspace_write start event, got %#v", emitted)
	}
	if !hasGlobalToolEvent(emitted, "workspace_read", agentshared.ToolCallStart, false) {
		t.Fatalf("expected workspace_read(op=prepare_write) start event, got %#v", emitted)
	}
	if !hasGlobalToolEvent(emitted, "workspace_read", agentshared.ToolCallComplete, true) {
		t.Fatalf("expected successful workspace_read(op=prepare_write) completion event, got %#v", emitted)
	}
	if !hasGlobalToolEvent(emitted, "workspace_write", agentshared.ToolCallComplete, true) {
		t.Fatalf("expected successful workspace_write completion after refresh, got %#v", emitted)
	}
}

func newGlobalTesterWriteHarness(t *testing.T) (*GlobalTester, versioning.FileAccess, context.Context) {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "service"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "service", "service.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	t.Cleanup(func() {
		_ = svfs.Close()
	})

	gt, err := New(testershared.GlobalTesterConfig{Factory: newTestFactory(t)}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	fa := svfs.NewGlobalFileAccess(false)
	gt.SetFileAccess(fa)
	gt.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:  versioning.WorkspaceViewGlobal,
		Session:      svfs,
		WorkingDir:   root,
		DiskFallback: versioning.NewDiskFileAccess(root, true),
	}))

	return gt, fa, versioning.WithSessionID(context.Background(), "sess-1")
}

func prepareGlobalWriteBasis(
	t *testing.T,
	gt *GlobalTester,
	ctx context.Context,
	path string,
) versioning.WorkspaceWriteBasis {
	t.Helper()

	// Phase 2.K / CR-2: route through workspace_read(op=prepare_write)
	// which carries the write preflight after the fold.
	input, err := json.Marshal(map[string]any{
		"op":    "prepare_write",
		"scope": string(versioning.WorkspaceWriteScopeGlobal),
		"path":  path,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result := gt.skills.Invoke(ctx, "workspace_read", input)
	if !result.Success {
		t.Fatalf("workspace_read(op=prepare_write) failed: %s", result.Error)
	}
	prepared, ok := result.Data.(versioning.PreparedWorkspaceWriteContext)
	if !ok {
		t.Fatalf("prepare result type = %T, want PreparedWorkspaceWriteContext", result.Data)
	}
	return prepared.Basis
}

func hasGlobalToolEvent(events []agentshared.ToolCallEvent, name string, phase agentshared.ToolCallPhase, success bool) bool {
	for _, event := range events {
		if event.ToolName != name || event.Phase != phase {
			continue
		}
		if phase == agentshared.ToolCallComplete && event.Success != success {
			continue
		}
		return true
	}
	return false
}
