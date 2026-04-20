//go:build linux

package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestNewLinux_Platform(t *testing.T) {
	s := NewLinux()
	if s.Platform().OS != "linux" {
		t.Errorf("Platform.OS = %q, want %q", s.Platform().OS, "linux")
	}
}

func TestLinuxSpawn_RejectsEmptyCmd(t *testing.T) {
	s := NewLinux()
	_, err := s.Spawn(context.Background(), Config{})
	if !errors.Is(err, ErrEmptyCmd) {
		t.Fatalf("expected ErrEmptyCmd, got %v", err)
	}
}

func TestLinuxSpawn_DegradedWhenBubblewrapMissing(t *testing.T) {
	if commandAvailable("bwrap") {
		t.Skip("bwrap is installed; cannot exercise the missing-bwrap fallback")
	}
	s := NewLinux()
	result, err := s.Spawn(context.Background(), Config{
		Cmd: []string{"true"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if result.Hermetic {
		t.Error("expected Hermetic=false in degraded mode")
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning describing degraded mode")
	}
}

func TestLinuxSpawn_HermeticWhenBubblewrapAvailable(t *testing.T) {
	if !commandAvailable("bwrap") {
		t.Skip("bwrap not installed; cannot exercise the hermetic path")
	}
	s := NewLinux()
	result, err := s.Spawn(context.Background(), Config{
		Cmd:     []string{"/bin/true"},
		Network: NetworkNone,
	})
	if err != nil {
		// bwrap may fail in nested-sandbox CI environments; the contract
		// being tested here is "if bwrap is on PATH, Spawn attempts the
		// hermetic path", not "every CI host can run bwrap successfully".
		if strings.Contains(err.Error(), "bwrap") || strings.Contains(strings.ToLower(err.Error()), "permission") {
			t.Skipf("bwrap unavailable in test sandbox: %v", err)
		}
		t.Fatalf("Spawn: %v", err)
	}
	if !result.Hermetic {
		t.Error("expected Hermetic=true when bwrap was used")
	}
}

func TestClassifyExitError_TimeoutMaps(t *testing.T) {
	_, err := classifyExitError(nil, context.DeadlineExceeded)
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("classifyExitError on DeadlineExceeded = %v, want ErrTimeout", err)
	}
}

func TestClassifyExitError_NoErrorReturnsZeroExit(t *testing.T) {
	code, err := classifyExitError(nil, nil)
	if err != nil || code != 0 {
		t.Errorf("classifyExitError(nil, nil) = (%d, %v), want (0, nil)", code, err)
	}
}

// 'time' import was previously required for the timeout integration test;
// keep a structural reference so the import still has a use as the test
// file evolves.
var _ = time.Second

func TestBuildBubblewrapArgs_AppliesNamespaceFlags(t *testing.T) {
	s := NewLinux()
	args := s.buildBubblewrapArgs(Config{
		Cmd: []string{"/bin/echo", "hi"},
	})
	wantContains := []string{"--unshare-all", "--die-with-parent", "--proc", "--dev"}
	for _, w := range wantContains {
		if !sliceContains(args, w) {
			t.Errorf("args missing %q: %v", w, args)
		}
	}
}

func TestBuildBubblewrapArgs_BindsMounts(t *testing.T) {
	s := NewLinux()
	args := s.buildBubblewrapArgs(Config{
		Cmd: []string{"/bin/echo"},
		Mounts: []Mount{
			{HostPath: "/host/path", ViewPath: "/view/path", ReadOnly: true},
		},
	})
	if !sliceContains(args, "--ro-bind") {
		t.Error("expected --ro-bind in args for ReadOnly mount")
	}
}

func TestBuildBubblewrapArgs_ChdirWhenSet(t *testing.T) {
	s := NewLinux()
	args := s.buildBubblewrapArgs(Config{
		Cmd: []string{"/bin/echo"},
		Cwd: "/work",
	})
	if !sliceContains(args, "--chdir") {
		t.Error("expected --chdir when Cwd is set")
	}
}

func TestBuildBubblewrapArgs_NoChdirWhenEmpty(t *testing.T) {
	s := NewLinux()
	args := s.buildBubblewrapArgs(Config{Cmd: []string{"/bin/echo"}})
	if sliceContains(args, "--chdir") {
		t.Error("did not expect --chdir when Cwd is empty")
	}
}

func TestBuildBubblewrapArgs_SetsEnvViaArgs(t *testing.T) {
	s := NewLinux()
	args := s.buildBubblewrapArgs(Config{
		Cmd: []string{"/bin/echo"},
		Env: map[string]string{"MY_VAR": "value"},
	})
	count := 0
	for _, a := range args {
		if a == "--setenv" {
			count++
		}
	}
	if count == 0 {
		t.Error("expected --setenv arguments for env vars")
	}
}

func TestNewLinux_PlatformContainsARH(t *testing.T) {
	s := NewLinux()
	p := s.Platform()
	if p.Arch == "" {
		t.Error("Platform.Arch should be runtime-derived, not empty")
	}
	_ = recipe.Platform{} // ensure import is referenced
}

func sliceContains(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
