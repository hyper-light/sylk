// Package detect provides utilities for detecting binaries and tools in the system.
package detect

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// pathCache stores cached binary lookups to avoid repeated filesystem operations.
type pathCache struct {
	mu          sync.RWMutex
	cache       map[string]string   // binary -> full path (single result)
	cacheAll    map[string][]string // binary -> all paths
	lastPathEnv string              // PATH value when cache was populated
}

var (
	// globalCache is the package-level cache for binary lookups.
	globalCache = &pathCache{
		cache:    make(map[string]string),
		cacheAll: make(map[string][]string),
	}
)

// Which finds a binary in PATH and returns its full path.
// Returns an empty string if the binary is not found.
// Results are cached for performance; cache is automatically invalidated
// when the PATH environment variable changes.
func Which(binary string) string {
	if binary == "" {
		return ""
	}

	if path, ok := tryReadCache(binary); ok {
		return path
	}

	return lookupAndCacheBinary(binary)
}

func tryReadCache(binary string) (string, bool) {
	globalCache.mu.RLock()
	defer globalCache.mu.RUnlock()

	if globalCache.lastPathEnv != os.Getenv("PATH") {
		return "", false
	}
	path, ok := globalCache.cache[binary]
	return path, ok
}

func lookupAndCacheBinary(binary string) string {
	globalCache.mu.Lock()
	defer globalCache.mu.Unlock()

	refreshCacheIfPathChanged()

	if path, ok := globalCache.cache[binary]; ok {
		return path
	}

	return performLookup(binary)
}

func refreshCacheIfPathChanged() {
	currentPath := os.Getenv("PATH")
	if globalCache.lastPathEnv != currentPath {
		globalCache.cache = make(map[string]string)
		globalCache.cacheAll = make(map[string][]string)
		globalCache.lastPathEnv = currentPath
	}
}

func performLookup(binary string) string {
	path, err := exec.LookPath(binary)
	if err != nil {
		globalCache.cache[binary] = ""
		return ""
	}
	globalCache.cache[binary] = path
	return path
}

// WhichAll finds all occurrences of a binary in PATH and returns their full paths.
// Returns an empty slice if the binary is not found anywhere.
// Results are cached for performance; cache is automatically invalidated
// when the PATH environment variable changes.
func WhichAll(binary string) []string {
	if binary == "" {
		return nil
	}

	if paths, ok := tryReadCacheAll(binary); ok {
		return paths
	}

	return lookupAndCacheAllBinaries(binary)
}

func tryReadCacheAll(binary string) ([]string, bool) {
	globalCache.mu.RLock()
	defer globalCache.mu.RUnlock()

	if globalCache.lastPathEnv != os.Getenv("PATH") {
		return nil, false
	}
	paths, ok := globalCache.cacheAll[binary]
	if !ok {
		return nil, false
	}
	return copyPaths(paths), true
}

func lookupAndCacheAllBinaries(binary string) []string {
	globalCache.mu.Lock()
	defer globalCache.mu.Unlock()

	refreshCacheIfPathChanged()

	if paths, ok := globalCache.cacheAll[binary]; ok {
		return copyPaths(paths)
	}

	return performAllLookup(binary)
}

func performAllLookup(binary string) []string {
	paths := findAllInPath(binary, os.Getenv("PATH"))
	globalCache.cacheAll[binary] = paths
	cacheFirstPath(binary, paths)
	return copyPaths(paths)
}

func cacheFirstPath(binary string, paths []string) {
	if len(paths) > 0 {
		globalCache.cache[binary] = paths[0]
	} else {
		globalCache.cache[binary] = ""
	}
}

func copyPaths(paths []string) []string {
	result := make([]string, len(paths))
	copy(result, paths)
	return result
}

// ClearCache manually invalidates the binary lookup cache.
// This can be useful when you know the PATH has changed or binaries have been
// installed/removed, and you want to force fresh lookups.
func ClearCache() {
	globalCache.mu.Lock()
	defer globalCache.mu.Unlock()

	globalCache.cache = make(map[string]string)
	globalCache.cacheAll = make(map[string][]string)
	globalCache.lastPathEnv = ""
}

// findAllInPath searches for all occurrences of a binary across all PATH directories.
func findAllInPath(binary, pathEnv string) []string {
	if pathEnv == "" {
		return nil
	}

	dirs := strings.Split(pathEnv, getPathSeparator())
	return collectExecutables(binary, dirs)
}

func getPathSeparator() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

func collectExecutables(binary string, dirs []string) []string {
	var results []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		results = appendNewExecutables(results, seen, getCandidates(binary, dir))
	}
	return results
}

func appendNewExecutables(results []string, seen map[string]bool, candidates []string) []string {
	for _, candidate := range candidates {
		if seen[candidate] || !isExecutable(candidate) {
			continue
		}
		seen[candidate] = true
		results = append(results, candidate)
	}
	return results
}

// getCandidates returns the list of potential executable paths for a binary in a directory.
// On Windows, this includes extensions from PATHEXT; on Unix, just the binary name.
func getCandidates(binary, dir string) []string {
	basePath := buildBasePath(dir, binary)

	if runtime.GOOS != "windows" {
		return []string{basePath}
	}

	return getWindowsCandidates(basePath)
}

func buildBasePath(dir, binary string) string {
	separator := "/"
	if runtime.GOOS == "windows" {
		separator = "\\"
	}
	return dir + separator + binary
}

func getWindowsCandidates(basePath string) []string {
	candidates := []string{basePath}
	extensions := getWindowsExtensions()

	for _, ext := range extensions {
		if ext != "" {
			candidates = append(candidates, basePath+ext)
		}
	}
	return candidates
}

func getWindowsExtensions() []string {
	pathExt := os.Getenv("PATHEXT")
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD;.VBS;.VBE;.JS;.JSE;.WSF;.WSH;.MSC"
	}
	return strings.Split(strings.ToLower(pathExt), ";")
}

// isExecutable checks if a path exists and is executable.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	if info.IsDir() {
		return false
	}

	if runtime.GOOS == "windows" {
		// On Windows, if the file exists it's considered executable
		// (actual execution depends on extension which we handle in getCandidates)
		return true
	}

	// On Unix, check if any execute bit is set
	mode := info.Mode()
	return mode&0111 != 0
}
