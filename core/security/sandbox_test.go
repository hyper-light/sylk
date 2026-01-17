package security

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSandbox_DisabledByDefault(t *testing.T) {
	cfg := DefaultSandboxConfig()
	if cfg.Enabled {
		t.Error("sandbox should be disabled by default")
	}
}

func TestSandbox_ExecuteWithoutSandbox(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Enabled: false,
	}, nil)

	cmd := exec.Command("echo", "test")
	wrapped, err := sandbox.Execute(context.Background(), cmd)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrapped != cmd {
		t.Error("disabled sandbox should return same command")
	}
}

func TestSandbox_EnableDisable(t *testing.T) {
	sandbox := NewSandbox(DefaultSandboxConfig(), nil)

	if sandbox.Status().Enabled {
		t.Error("should start disabled")
	}

	sandbox.Enable()
	if !sandbox.Status().Enabled {
		t.Error("should be enabled after Enable()")
	}

	sandbox.Disable()
	if sandbox.Status().Enabled {
		t.Error("should be disabled after Disable()")
	}
}

func TestSandbox_EnableLayers(t *testing.T) {
	sandbox := NewSandbox(DefaultSandboxConfig(), nil)

	sandbox.EnableLayer(LayerVFS)
	status := sandbox.Status()
	if !status.VFSActive {
		t.Error("VFS layer should be active")
	}

	sandbox.EnableLayer(LayerNetwork)
	status = sandbox.Status()
	if !status.NetworkActive {
		t.Error("Network layer should be active")
	}

	sandbox.DisableLayer(LayerVFS)
	status = sandbox.Status()
	if status.VFSActive {
		t.Error("VFS layer should be inactive after disable")
	}
}

func TestSandbox_AllowPath(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "sandbox-test-*")
	defer os.RemoveAll(tmpDir)

	sandbox := NewSandbox(SandboxConfig{
		Enabled:    true,
		VFSLayer:   true,
		WorkingDir: tmpDir,
	}, nil)

	testPath := filepath.Join(tmpDir, "allowed", "file.txt")
	sandbox.AllowPath(filepath.Join(tmpDir, "allowed"))

	err := sandbox.ValidatePath(testPath)
	if err != nil {
		t.Errorf("allowed path should be valid: %v", err)
	}
}

func TestSandbox_AllowDomain(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Enabled:      true,
		NetworkProxy: true,
	}, nil)

	sandbox.AllowDomain("example.com")

	domains := sandbox.networkProxy.ListAllowedDomains()
	found := false
	for _, d := range domains {
		if d == "example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Error("example.com should be in allowed domains")
	}
}

func TestSandbox_Status(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Enabled:      true,
		OSIsolation:  true,
		VFSLayer:     true,
		NetworkProxy: true,
		WorkingDir:   "/tmp",
	}, nil)

	status := sandbox.Status()

	if !status.Enabled {
		t.Error("status should show enabled")
	}
	if status.Platform == "" {
		t.Error("platform should be set")
	}
}

func TestSandbox_GracefulDegrade(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Enabled:         true,
		OSIsolation:     true,
		GracefulDegrade: true,
	}, nil)

	cmd := exec.Command("echo", "test")
	wrapped, err := sandbox.Execute(context.Background(), cmd)

	if err != nil {
		t.Errorf("graceful degrade should not error: %v", err)
	}
	if wrapped == nil {
		t.Error("should return a command even when degraded")
	}
}

func TestVirtualFilesystem_ValidatePath(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vfs-test-*")
	defer os.RemoveAll(tmpDir)

	vfs := NewVirtualFilesystem(tmpDir, nil)

	validPath := filepath.Join(tmpDir, "subdir", "file.txt")
	if err := vfs.ValidatePath(validPath); err != nil {
		t.Errorf("path under working dir should be valid: %v", err)
	}

	invalidPath := "/etc/passwd"
	if err := vfs.ValidatePath(invalidPath); err == nil {
		t.Error("path outside working dir should be invalid")
	}
}

func TestVirtualFilesystem_AllowedPaths(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vfs-test-*")
	defer os.RemoveAll(tmpDir)

	otherDir, _ := os.MkdirTemp("", "vfs-other-*")
	defer os.RemoveAll(otherDir)

	vfs := NewVirtualFilesystem(tmpDir, []string{otherDir})

	if err := vfs.ValidatePath(filepath.Join(otherDir, "file.txt")); err != nil {
		t.Errorf("allowed path should be valid: %v", err)
	}
}

func TestVirtualFilesystem_ApprovedPaths(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vfs-test-*")
	defer os.RemoveAll(tmpDir)

	vfs := NewVirtualFilesystem(tmpDir, nil)

	outsidePath := "/opt/approved"
	if err := vfs.ValidatePath(outsidePath); err == nil {
		t.Error("unapproved path should be invalid")
	}

	vfs.ApprovePath(outsidePath)
	if err := vfs.ValidatePath(outsidePath); err != nil {
		t.Errorf("approved path should be valid: %v", err)
	}

	vfs.RemoveApprovedPath(outsidePath)
	if err := vfs.ValidatePath(outsidePath); err == nil {
		t.Error("removed approval should make path invalid again")
	}
}

func TestVirtualFilesystem_ListPaths(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vfs-test-*")
	defer os.RemoveAll(tmpDir)

	vfs := NewVirtualFilesystem(tmpDir, []string{"/path/a", "/path/b"})

	allowed := vfs.ListAllowedPaths()
	if len(allowed) < 3 {
		t.Errorf("expected at least 3 allowed paths, got %d", len(allowed))
	}

	vfs.ApprovePath("/approved/1")
	vfs.ApprovePath("/approved/2")

	approved := vfs.ListApprovedPaths()
	if len(approved) != 2 {
		t.Errorf("expected 2 approved paths, got %d", len(approved))
	}
}

func TestNetworkProxy_AllowedDomains(t *testing.T) {
	np := NewNetworkProxy([]string{"custom.example.com"})

	if !np.isDomainAllowed("custom.example.com") {
		t.Error("custom domain should be allowed")
	}

	if !np.isDomainAllowed("github.com") {
		t.Error("default domain github.com should be allowed")
	}

	if np.isDomainAllowed("blocked.example.com") {
		t.Error("non-allowed domain should be blocked")
	}
}

func TestNetworkProxy_AddRemoveDomain(t *testing.T) {
	np := NewNetworkProxy(nil)

	np.AddAllowedDomain("new.example.com")
	if !np.isDomainAllowed("new.example.com") {
		t.Error("added domain should be allowed")
	}

	np.RemoveAllowedDomain("new.example.com")
	if np.isDomainAllowed("new.example.com") {
		t.Error("removed domain should not be allowed")
	}
}

func TestNetworkProxy_ListDomains(t *testing.T) {
	np := NewNetworkProxy([]string{"example1.com", "example2.com"})

	domains := np.ListAllowedDomains()
	if len(domains) < 2 {
		t.Errorf("expected at least 2 domains, got %d", len(domains))
	}
}

func TestNetworkProxy_Stats(t *testing.T) {
	np := NewNetworkProxy(nil)

	stats := np.GetStats()
	if stats.TotalRequests != 0 {
		t.Error("initial stats should be zero")
	}

	np.ResetStats()
	stats = np.GetStats()
	if stats.TotalRequests != 0 {
		t.Error("reset should clear stats")
	}
}

func TestNetworkProxy_RouteCommand(t *testing.T) {
	np := NewNetworkProxy(nil)

	cmd := exec.Command("curl", "https://example.com")
	np.RouteCommand(cmd)

	if np.Port() != 0 {
		var hasProxy bool
		for _, env := range cmd.Env {
			if len(env) > 11 && env[:11] == "HTTP_PROXY=" {
				hasProxy = true
				break
			}
		}
		if !hasProxy {
			t.Error("command env should have HTTP_PROXY when proxy running")
		}
	}
}

func TestNetworkProxy_StartStop(t *testing.T) {
	np := NewNetworkProxy(nil)

	if np.IsRunning() {
		t.Error("proxy should not be running initially")
	}

	if err := np.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}

	if !np.IsRunning() {
		t.Error("proxy should be running after Start")
	}

	if np.Port() == 0 {
		t.Error("port should be assigned after Start")
	}

	np.Stop()

	if np.IsRunning() {
		t.Error("proxy should not be running after Stop")
	}
}

func TestSandbox_Stop(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Enabled:      true,
		NetworkProxy: true,
	}, nil)

	if sandbox.networkProxy != nil {
		sandbox.networkProxy.Start()
	}

	sandbox.Stop()
}
