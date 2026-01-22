package treesitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

var validGrammarName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type GrammarHandle struct {
	libHandle uintptr
	langPtr   unsafe.Pointer
	name      string
	checksum  string
}

type GrammarLoader struct {
	grammars       map[string]*GrammarHandle
	failedGrammars map[string]struct{}
	trustedDirs    []string
	checksums      map[string]string
	requireVerify  bool
	autoDownload   bool
	downloader     *GrammarDownloader
	mu             sync.RWMutex
}

type LoaderOption func(*GrammarLoader)

func WithTrustedDir(dir string) LoaderOption {
	return func(gl *GrammarLoader) {
		if abs, err := filepath.Abs(dir); err == nil {
			gl.trustedDirs = append(gl.trustedDirs, abs)
		}
	}
}

func WithChecksum(name, sha256sum string) LoaderOption {
	return func(gl *GrammarLoader) {
		gl.checksums[name] = sha256sum
	}
}

func WithRequireVerification(require bool) LoaderOption {
	return func(gl *GrammarLoader) {
		gl.requireVerify = require
	}
}

func WithAutoDownload(enable bool) LoaderOption {
	return func(gl *GrammarLoader) {
		gl.autoDownload = enable
	}
}

func WithDownloader(d *GrammarDownloader) LoaderOption {
	return func(gl *GrammarLoader) {
		gl.downloader = d
		if d != nil {
			gl.trustedDirs = append([]string{d.CacheDir()}, gl.trustedDirs...)
		}
	}
}

func NewGrammarLoader(opts ...LoaderOption) *GrammarLoader {
	gl := &GrammarLoader{
		grammars:       make(map[string]*GrammarHandle),
		failedGrammars: make(map[string]struct{}),
		trustedDirs:    defaultTrustedDirs(),
		checksums:      make(map[string]string),
		requireVerify:  false,
	}
	for _, opt := range opts {
		opt(gl)
	}
	return gl
}

var globalLoader = NewGrammarLoader()

func defaultTrustedDirs() []string {
	dirs := []string{}

	if dataDir := sylkDataDir(); dataDir != "" {
		grammarDir := filepath.Join(dataDir, "grammars")
		if abs, err := filepath.Abs(grammarDir); err == nil {
			dirs = append(dirs, abs)
		}
	}

	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/lib", "/usr/local/lib")
	case "linux":
		dirs = append(dirs, "/usr/lib", "/usr/local/lib")
	}

	return dirs
}

func sylkDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "sylk")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "sylk")
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "sylk")
		}
		return filepath.Join(home, "AppData", "Roaming", "sylk")
	default:
		return filepath.Join(home, ".local", "share", "sylk")
	}
}

func (gl *GrammarLoader) Load(name string) (*sitter.Language, error) {
	return gl.LoadContext(context.Background(), name)
}

func (gl *GrammarLoader) LoadContext(ctx context.Context, name string) (*sitter.Language, error) {
	if err := validateGrammarName(name); err != nil {
		return nil, err
	}

	gl.mu.RLock()
	if h, ok := gl.grammars[name]; ok {
		gl.mu.RUnlock()
		return sitter.NewLanguage(h.langPtr), nil
	}
	if _, failed := gl.failedGrammars[name]; failed {
		gl.mu.RUnlock()
		return nil, fmt.Errorf("grammar %q previously failed to load", name)
	}
	gl.mu.RUnlock()

	gl.mu.Lock()
	defer gl.mu.Unlock()

	if h, ok := gl.grammars[name]; ok {
		return sitter.NewLanguage(h.langPtr), nil
	}
	if _, failed := gl.failedGrammars[name]; failed {
		return nil, fmt.Errorf("grammar %q previously failed to load", name)
	}

	handle, err := gl.loadLibrarySafe(name)
	if err != nil {
		if gl.autoDownload && gl.downloader != nil {
			if downloadErr := gl.downloadAndRetry(ctx, name); downloadErr == nil {
				handle, err = gl.loadLibrarySafe(name)
			}
		}
		if err != nil {
			gl.failedGrammars[name] = struct{}{}
			return nil, err
		}
	}

	gl.grammars[name] = handle
	return sitter.NewLanguage(handle.langPtr), nil
}

func (gl *GrammarLoader) downloadAndRetry(ctx context.Context, name string) error {
	libPath, err := gl.downloader.EnsureGrammar(ctx, name)
	if err != nil {
		return fmt.Errorf("download grammar %s: %w", name, err)
	}

	if err := gl.downloader.VerifyChecksum(name, libPath); err != nil {
		os.Remove(libPath)
		return fmt.Errorf("verify downloaded grammar: %w", err)
	}

	return nil
}

func validateGrammarName(name string) error {
	if !validGrammarName.MatchString(name) {
		return fmt.Errorf("invalid grammar name %q: must be 1-64 lowercase alphanumeric chars", name)
	}
	return nil
}

func (gl *GrammarLoader) loadLibrarySafe(name string) (*GrammarHandle, error) {
	libPath, err := gl.findAndValidateLibrary(name)
	if err != nil {
		return nil, err
	}

	checksum, err := gl.verifyLibrary(name, libPath)
	if err != nil {
		return nil, err
	}

	lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("dlopen %s: %w", libPath, err)
	}

	var langFunc func() unsafe.Pointer
	purego.RegisterLibFunc(&langFunc, lib, "tree_sitter_"+name)

	ptr := langFunc()
	if ptr == nil {
		purego.Dlclose(lib)
		return nil, fmt.Errorf("tree_sitter_%s returned null", name)
	}

	return &GrammarHandle{
		libHandle: lib,
		langPtr:   ptr,
		name:      name,
		checksum:  checksum,
	}, nil
}

func (gl *GrammarLoader) findAndValidateLibrary(name string) (string, error) {
	libName := grammarLibName(name)

	for _, dir := range gl.trustedDirs {
		if err := validateDirectory(dir); err != nil {
			continue
		}

		path := filepath.Join(dir, libName)
		if err := validateLibraryFile(path, dir); err != nil {
			continue
		}

		return path, nil
	}

	return "", fmt.Errorf("grammar %q not found in trusted directories", name)
}

func validateDirectory(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return err
	}

	info, err := os.Stat(realDir)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}

	if isWorldWritable(info) {
		return fmt.Errorf("world-writable directory rejected: %s", dir)
	}

	return nil
}

func validateLibraryFile(path, trustedDir string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return err
	}

	absTrusted, _ := filepath.Abs(trustedDir)
	realTrusted, _ := filepath.EvalSymlinks(absTrusted)

	if !isSubpath(realPath, realTrusted) {
		return fmt.Errorf("path escapes trusted directory: %s", path)
	}

	info, err := os.Stat(realPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fmt.Errorf("expected file, got directory: %s", path)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unexpected symlink after resolution: %s", path)
	}

	if isWorldWritable(info) {
		return fmt.Errorf("world-writable file rejected: %s", path)
	}

	return nil
}

func isSubpath(child, parent string) bool {
	absChild, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return false
	}

	absChild = filepath.Clean(absChild)
	absParent = filepath.Clean(absParent)

	if absChild == absParent {
		return true
	}

	parentWithSep := absParent
	if !strings.HasSuffix(parentWithSep, string(filepath.Separator)) {
		parentWithSep += string(filepath.Separator)
	}

	return strings.HasPrefix(absChild, parentWithSep)
}

func isWorldWritable(info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return stat.Mode&0002 != 0
}

func (gl *GrammarLoader) verifyLibrary(name, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read library: %w", err)
	}

	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])

	if expected, ok := gl.checksums[name]; ok {
		if checksum != expected {
			return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, expected, checksum)
		}
	} else if gl.requireVerify {
		return "", fmt.Errorf("no checksum registered for %s (verification required)", name)
	}

	return checksum, nil
}

func grammarLibName(name string) string {
	switch runtime.GOOS {
	case "darwin":
		return "libtree-sitter-" + name + ".dylib"
	case "windows":
		return "tree-sitter-" + name + ".dll"
	default:
		return "libtree-sitter-" + name + ".so"
	}
}

func (gl *GrammarLoader) Unload(name string) error {
	gl.mu.Lock()
	defer gl.mu.Unlock()

	h, ok := gl.grammars[name]
	if !ok {
		return nil
	}

	if h.libHandle != 0 {
		purego.Dlclose(h.libHandle)
	}
	delete(gl.grammars, name)
	return nil
}

func (gl *GrammarLoader) RegisterChecksum(name, sha256sum string) {
	gl.mu.Lock()
	defer gl.mu.Unlock()
	gl.checksums[name] = sha256sum
}

func (gl *GrammarLoader) AddTrustedDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	if err := validateDirectory(abs); err != nil {
		return fmt.Errorf("directory validation failed: %w", err)
	}

	gl.mu.Lock()
	defer gl.mu.Unlock()
	gl.trustedDirs = append([]string{abs}, gl.trustedDirs...)
	return nil
}

func (gl *GrammarLoader) SetRequireVerification(require bool) {
	gl.mu.Lock()
	defer gl.mu.Unlock()
	gl.requireVerify = require
}

func (gl *GrammarLoader) LoadedGrammars() map[string]string {
	gl.mu.RLock()
	defer gl.mu.RUnlock()
	result := make(map[string]string, len(gl.grammars))
	for name, h := range gl.grammars {
		result[name] = h.checksum
	}
	return result
}

func LoadGrammar(name string) (*sitter.Language, error) {
	return globalLoader.Load(name)
}

func UnloadGrammar(name string) error {
	return globalLoader.Unload(name)
}

func RegisterGrammarChecksum(name, sha256sum string) {
	globalLoader.RegisterChecksum(name, sha256sum)
}

func AddTrustedGrammarDir(dir string) error {
	return globalLoader.AddTrustedDir(dir)
}

func SetRequireGrammarVerification(require bool) {
	globalLoader.SetRequireVerification(require)
}

func IsGrammarAvailable(name string) bool {
	if err := validateGrammarName(name); err != nil {
		return false
	}
	_, err := globalLoader.findAndValidateLibrary(name)
	return err == nil
}
