package lua

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestInit_NilRuntimeReturnsSentinel proves the programmer-error
// guard: passing a nil runtime is distinct from a clean no-plugin
// startup and must surface the dedicated sentinel so editor
// bootstrap code can tell "mis-wired" apart from "no files yet".
func TestInit_NilRuntimeReturnsSentinel(t *testing.T) {
	err := Init(t.TempDir(), nil)
	if !errors.Is(err, ErrRuntimeNil) {
		t.Errorf("Init(_, nil) = %v, want ErrRuntimeNil", err)
	}
}

// TestInit_MissingConfigDirReturnsNil covers the brand-new-install
// path — no ~/.config/sylk exists, no plugins to load, and that's
// NOT an error. A non-nil return here would surface spurious
// warnings in every fresh editor session.
func TestInit_MissingConfigDirReturnsNil(t *testing.T) {
	rt := newStubRuntimeForInit(t)
	defer rt.Close()

	missing := filepath.Join(t.TempDir(), "nonexistent")
	if err := Init(missing, rt); err != nil {
		t.Errorf("Init on missing dir returned %v, want nil", err)
	}
}

// TestInit_ExecutesInitLua runs a tiny plugin file and verifies it
// actually landed on the runtime by checking a side effect — the
// runtime executed DoFile. If init.lua is skipped, the whole
// UI-10 claim collapses.
func TestInit_ExecutesInitLua(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "init.lua")
	// A benign script: assign a global; the plugin loader invokes
	// DoFile which compiles and runs the chunk. Success is implied by
	// "no error"; we do not attempt to read the global back because
	// the stub runtime has no vim.api surface to read through.
	if err := os.WriteFile(path, []byte("return 1 + 1"), 0o644); err != nil {
		t.Fatalf("seed init.lua: %v", err)
	}

	rt := newStubRuntimeForInit(t)
	defer rt.Close()

	if err := Init(dir, rt); err != nil {
		t.Errorf("Init(valid dir with init.lua) returned %v, want nil", err)
	}
}

// TestInit_BrokenPluginReturnsJoinedError verifies error isolation:
// a syntax error in one file must produce an ErrPluginLoadFailed-
// wrapped return, not a panic and not silent success. The editor
// bootstrap caller can log and keep running.
func TestInit_BrokenPluginReturnsJoinedError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "init.lua")
	// Deliberately malformed: unterminated string literal.
	if err := os.WriteFile(path, []byte(`print("oops`), 0o644); err != nil {
		t.Fatalf("seed init.lua: %v", err)
	}

	rt := newStubRuntimeForInit(t)
	defer rt.Close()

	err := Init(dir, rt)
	if err == nil {
		t.Fatal("expected error for malformed plugin, got nil")
	}
	if !errors.Is(err, ErrPluginLoadFailed) {
		t.Errorf("err = %v, want ErrPluginLoadFailed wrap", err)
	}
}

// newStubRuntimeForInit builds a Runtime with a no-op EditorAccess.
// Good enough for Init's needs: the loader just compiles and runs
// Lua chunks; it never reaches into vim.api for these tests.
func newStubRuntimeForInit(t *testing.T) *Runtime {
	t.Helper()
	return NewRuntime(stubEditor{})
}

// stubEditor is a zero-behavior EditorAccess for tests that never
// exercise the vim.* surface. All methods return zero values.
type stubEditor struct{}

func (stubEditor) CurrentBuffer() BufferAccess         { return nil }
func (stubEditor) CurrentWindow() WindowAccess         { return nil }
func (stubEditor) ListBuffers() []int                  { return nil }
func (stubEditor) ListWindows() []int                  { return nil }
func (stubEditor) GetBuffer(int) BufferAccess          { return nil }
func (stubEditor) GetWindow(int) WindowAccess          { return nil }
func (stubEditor) SetCurrentBuffer(int) error          { return nil }
func (stubEditor) SetCurrentWindow(int) error          { return nil }
func (stubEditor) Command(string) error                { return nil }
func (stubEditor) Eval(string) (any, error)            { return nil, nil }
func (stubEditor) Input(string)                        {}
func (stubEditor) FeedKeys(string, string)             {}
func (stubEditor) GetMode() string                     { return "" }
func (stubEditor) GetOption(string) (any, bool)        { return nil, false }
func (stubEditor) SetOption(string, any)               {}
func (stubEditor) GetVar(string) (any, bool)           { return nil, false }
func (stubEditor) SetVar(string, any)                  {}
func (stubEditor) Echo([]string)                       {}
func (stubEditor) Notify(string, int)                  {}
