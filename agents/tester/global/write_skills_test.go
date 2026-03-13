package global

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestGlobalTesterWriteSkillsUseLeasedGlobalWrites(t *testing.T) {
	gt, fa, ctx := newGlobalTesterWriteHarness(t)

	cases := []struct {
		skillName string
		output    string
	}{
		{skillName: "write_test", output: "pkg/service/service_test.go"},
		{skillName: "write_integration_test", output: "pkg/service/service_integration_test.go"},
		{skillName: "write_e2e_test", output: "pkg/service/service_e2e_test.go"},
	}

	for _, tc := range cases {
		t.Run(tc.skillName, func(t *testing.T) {
			basis := prepareGlobalWriteBasis(t, gt, ctx, tc.output)
			input, err := json.Marshal(map[string]any{
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

			result := gt.skills.Invoke(ctx, tc.skillName, input)
			if !result.Success {
				t.Fatalf("%s failed: %s", tc.skillName, result.Error)
			}

			data, ok := result.Data.(map[string]any)
			if !ok {
				t.Fatalf("result type = %T, want map[string]any", result.Data)
			}
			nextBasisRaw, ok := data["next_basis"]
			if !ok {
				t.Fatalf("%s result missing next_basis", tc.skillName)
			}
			nextBasis, ok := nextBasisRaw.(*versioning.WorkspaceWriteBasis)
			if !ok {
				t.Fatalf("next_basis type = %T, want *WorkspaceWriteBasis", nextBasisRaw)
			}
			if nextBasis.Scope != versioning.WorkspaceWriteScopeGlobal {
				t.Fatalf("next_basis scope = %q, want %q", nextBasis.Scope, versioning.WorkspaceWriteScopeGlobal)
			}

			content, err := fa.ReadFile(ctx, tc.output)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(content) == "" {
				t.Fatalf("%s wrote empty content", tc.skillName)
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

	gt, err := New(testershared.GlobalTesterConfig{}, nil)
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

	input, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result := gt.skills.Invoke(ctx, "prepare_global_write_context", input)
	if !result.Success {
		t.Fatalf("prepare_global_write_context failed: %s", result.Error)
	}
	prepared, ok := result.Data.(versioning.PreparedWorkspaceWriteContext)
	if !ok {
		t.Fatalf("prepare result type = %T, want PreparedWorkspaceWriteContext", result.Data)
	}
	return prepared.Basis
}
