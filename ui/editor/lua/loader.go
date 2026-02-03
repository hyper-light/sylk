package lua

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxPluginFiles caps the total number of Lua plugin files that will be
// loaded from disk.  Prevents unbounded scanning of huge directories.
const maxPluginFiles = 100

// defaultConfigDir is the conventional location for user configuration.
const defaultConfigDir = ".config/sylk"

// ---------------------------------------------------------------------------
// Load phases (table-driven ordering)
// ---------------------------------------------------------------------------

// loadPhase describes one phase of the plugin loading sequence.
type loadPhase struct {
	// Relative path under the config directory.
	RelPath string
	// IsGlob indicates whether RelPath should be globbed (true) or treated
	// as a single file (false).
	IsGlob bool
}

// loadPhases defines the canonical ordering:
//  1. init.lua (single file)
//  2. plugin/*.lua (alphabetical)
//  3. after/plugin/*.lua (alphabetical)
var loadPhases = []loadPhase{
	{RelPath: "init.lua", IsGlob: false},
	{RelPath: filepath.Join("plugin", "*.lua"), IsGlob: true},
	{RelPath: filepath.Join("after", "plugin", "*.lua"), IsGlob: true},
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

// Loader discovers and executes Lua plugin files in the conventional
// directory layout.
type Loader struct {
	configDir string
}

// NewLoader creates a loader targeting the given config directory.
// An empty dir falls back to ~/.config/sylk/.
func NewLoader(dir string) *Loader {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, defaultConfigDir)
	}
	return &Loader{configDir: dir}
}

// Load executes every plugin file according to the phase ordering.
// Individual file errors are collected; loading continues on failure.
// Returns a joined error if any file failed, nil otherwise.
func (ld *Loader) Load(rt *Runtime) error {
	var errs []error
	loaded := 0

	for _, phase := range loadPhases {
		if loaded >= maxPluginFiles {
			break
		}
		paths := ld.resolvePaths(phase)
		for _, p := range paths {
			if loaded >= maxPluginFiles {
				break
			}
			if err := LoadFile(rt, p); err != nil {
				errs = append(errs, err)
			}
			loaded++
		}
	}
	return errors.Join(errs...)
}

// resolvePaths returns the list of concrete file paths for a phase.
func (ld *Loader) resolvePaths(phase loadPhase) []string {
	full := filepath.Join(ld.configDir, phase.RelPath)
	if !phase.IsGlob {
		return resolveSingleFile(full)
	}
	return resolveGlob(full)
}

// resolveSingleFile returns a single-element slice if the file exists and
// has a .lua extension, or nil otherwise.
func resolveSingleFile(path string) []string {
	if !isLuaFile(path) {
		return nil
	}
	if !fileExists(path) {
		return nil
	}
	return []string{path}
}

// resolveGlob expands a glob pattern and returns matching .lua files in
// sorted order, bounded by maxPluginFiles.
func resolveGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	slices.Sort(matches)
	out := make([]string, 0, min(len(matches), maxPluginFiles))
	for _, m := range matches {
		if !isLuaFile(m) {
			continue
		}
		out = append(out, m)
		if len(out) >= maxPluginFiles {
			break
		}
	}
	return out
}

// LoadFile validates and executes a single Lua file.
func LoadFile(rt *Runtime, path string) error {
	if !isLuaFile(path) {
		return errors.New("not a .lua file: " + path)
	}
	if !fileExists(path) {
		return errors.New("file not found: " + path)
	}
	return rt.DoFile(path)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isLuaFile reports whether path has a .lua extension.
func isLuaFile(path string) bool {
	return strings.HasSuffix(path, ".lua")
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
