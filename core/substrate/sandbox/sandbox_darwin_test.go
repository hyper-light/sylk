//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestGenerateDarwinSandboxProfile_Header(t *testing.T) {
	profile := generateDarwinSandboxProfile(Config{Cmd: []string{"echo", "hi"}})
	if !strings.Contains(profile, "(version 1)") {
		t.Error("profile missing version header")
	}
	if !strings.Contains(profile, "(deny default)") {
		t.Error("profile missing deny-default baseline")
	}
}

func TestGenerateDarwinSandboxProfile_NetworkDeniedByDefault(t *testing.T) {
	profile := generateDarwinSandboxProfile(Config{
		Cmd:     []string{"echo"},
		Network: NetworkNone,
	})
	if !strings.Contains(profile, "(deny network*)") {
		t.Error("profile missing explicit network deny")
	}
}

func TestGenerateDarwinSandboxProfile_AllowsSystemFrameworkReads(t *testing.T) {
	profile := generateDarwinSandboxProfile(Config{Cmd: []string{"echo"}})
	required := []string{
		"(allow file-read* (subpath \"/System\"))",
		"(allow file-read* (subpath \"/usr/lib\"))",
		"(allow file-read-metadata)",
	}
	for _, want := range required {
		if !strings.Contains(profile, want) {
			t.Errorf("profile missing required system rule: %s", want)
		}
	}
}

func TestGenerateDarwinSandboxProfile_ExposesMounts(t *testing.T) {
	profile := generateDarwinSandboxProfile(Config{
		Cmd: []string{"echo"},
		Mounts: []Mount{
			{HostPath: "/sylk/views/pipe-7", ReadOnly: true},
			{HostPath: "/sylk/views/pipe-7/scratch", ReadOnly: false},
		},
	})
	if !strings.Contains(profile, "(allow file-read* (subpath \"/sylk/views/pipe-7\"))") {
		t.Error("profile missing read rule for read-only mount")
	}
	if !strings.Contains(profile, "(allow file-write* (subpath \"/sylk/views/pipe-7/scratch\"))") {
		t.Error("profile missing write rule for writable mount")
	}
}

func TestGenerateDarwinSandboxProfile_AllowsProcessForkExec(t *testing.T) {
	profile := generateDarwinSandboxProfile(Config{Cmd: []string{"echo"}})
	for _, rule := range []string{"(allow process-fork)", "(allow process-exec)"} {
		if !strings.Contains(profile, rule) {
			t.Errorf("profile missing %s", rule)
		}
	}
}

func TestBuildDarwinSandboxArgs_Format(t *testing.T) {
	args := buildDarwinSandboxArgs("(some profile)", Config{Cmd: []string{"echo", "hi"}})
	if args[0] != "-p" {
		t.Errorf("args[0] = %q, want -p", args[0])
	}
	if args[1] != "(some profile)" {
		t.Errorf("args[1] = %q, want the profile", args[1])
	}
	if args[2] != "--" {
		t.Errorf("args[2] = %q, want --", args[2])
	}
	if args[3] != "echo" || args[4] != "hi" {
		t.Errorf("args trail = %v, want [echo hi]", args[3:])
	}
}

func TestNewDarwin_Platform(t *testing.T) {
	s := NewDarwin()
	if s.Platform().OS != "darwin" {
		t.Errorf("Platform.OS = %q", s.Platform().OS)
	}
	if s.Platform().Libc != "system" {
		t.Errorf("Platform.Libc = %q, want system", s.Platform().Libc)
	}
}
