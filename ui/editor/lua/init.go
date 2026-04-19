package lua

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

// UI-10: editor-init entrypoint for user Lua scripts.
//
// The Runtime and Loader existed as a complete library but had no
// startup caller — user scripts in ~/.config/sylk/ were never
// discovered or executed. Init fixes that by wiring the two together
// with explicit error isolation: a single broken plugin file cannot
// fail the editor startup, it produces a logged warning and the
// other plugins continue to load.
//
// The function is deliberately narrow:
//
//   - It does NOT own the Runtime (the editor does — the runtime
//     lives for the full editor lifetime, Init just runs plugins
//     against the runtime once).
//   - It degrades silently when the config directory doesn't exist
//     (a brand-new install has no plugins; that's normal, not an
//     error).
//   - It returns a joined error so callers can surface per-file
//     failures without crashing the editor. The editor is expected
//     to log this and continue.

// Init runs user plugin scripts against the supplied Runtime.
// The search path defaults to ~/.config/sylk/ when configDir is
// empty; callers that want a specific directory (testing, enterprise
// overrides) pass it explicitly.
//
// Error semantics:
//
//   - Nil runtime → ErrRuntimeNil (editor configuration bug, not a
//     user-script problem).
//   - Missing config directory → nil (no plugins installed).
//   - Per-file Lua errors → collected and returned joined; the
//     Loader keeps running other files after each failure.
func Init(configDir string, rt *Runtime) error {
	if rt == nil {
		return ErrRuntimeNil
	}
	dir := resolveConfigDir(configDir)
	if !configDirExists(dir) {
		return nil
	}
	loader := NewLoader(dir)
	if err := loader.Load(rt); err != nil {
		// Wrap so callers can grep logs for "lua: init" without
		// pulling in the lua package for type matching.
		return errors.Join(ErrPluginLoadFailed, err)
	}
	return nil
}

// InitWithLogger runs Init and emits a structured slog.Warn for each
// plugin failure instead of returning them. Preferred by the editor
// bootstrap — plugin errors should not block startup, and the log
// already lives in the editor's sink.
func InitWithLogger(configDir string, rt *Runtime, logger *slog.Logger) {
	err := Init(configDir, rt)
	if err == nil || logger == nil {
		return
	}
	logger.Warn("lua: init failed", "dir", resolveConfigDir(configDir), "err", err)
}

// ErrRuntimeNil signals Init was called without a Runtime — a
// programmer error distinct from user-script failures.
var ErrRuntimeNil = errors.New("lua: Init requires a non-nil Runtime")

// ErrPluginLoadFailed wraps per-file load errors. Callers testing
// for plugin failures use errors.Is(err, ErrPluginLoadFailed).
var ErrPluginLoadFailed = errors.New("lua: plugin load failed")

// resolveConfigDir returns the effective config directory. Matches
// Loader.NewLoader's own fallback so tests can stub os.UserHomeDir
// and get the same path.
func resolveConfigDir(configDir string) string {
	if configDir != "" {
		return configDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, defaultConfigDir)
}

// configDirExists reports whether dir exists AND is a directory.
// A regular file at that path is treated as "missing" — a user who
// put a file where the dir should live has a misconfiguration Init
// should not try to paper over.
func configDirExists(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	return info.IsDir()
}
