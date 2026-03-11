package purevfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const executionToolchainRoot = "/.sylk/toolchain/bin"

type mountedExecutionRoot interface {
	Root() string
	Close() error
}

type nativeExecutionBroker struct{}

type readOnlyExecutionFS struct {
	base ExecutionFS
}

func DefaultExecutionBroker() ExecutionBroker {
	return &nativeExecutionBroker{}
}

func ReadOnlyExecutionFS(base ExecutionFS) ExecutionFS {
	if base == nil || base.IsReadOnly() {
		return base
	}
	return readOnlyExecutionFS{base: base}
}

func (b *nativeExecutionBroker) Capabilities(_ context.Context) (ExecutionCapabilities, error) {
	return platformExecutionCapabilities(), nil
}

func (b *nativeExecutionBroker) Run(ctx context.Context, req BrokerRunRequest) (*BrokerRunResult, error) {
	if err := validateBrokerRunRequest(req); err != nil {
		return nil, err
	}
	if err := strictExecutionProbe(); err != nil {
		return nil, err
	}
	guard, err := currentExecutionGovernor().Admit()
	if err != nil {
		return nil, err
	}
	defer guard.Release()
	root, err := newProjectedRoot(req, guard.budget)
	if err != nil {
		return nil, err
	}
	mount, err := mountExecutionRoot(ctx, req, root)
	if err != nil {
		return nil, err
	}
	defer mount.Close()
	return runMountedExecution(ctx, req, mount.Root(), guard)
}

func validateBrokerRunRequest(req BrokerRunRequest) error {
	if len(req.Argv) == 0 {
		return fmt.Errorf("purevfs: missing execution argv")
	}
	if req.Workspace == nil {
		return fmt.Errorf("purevfs: missing execution workspace")
	}
	return nil
}

func ShellCommandArgv(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	if isWindowsPlatform() {
		return []string{"cmd.exe", "/d", "/c", command}
	}
	return []string{"/bin/sh", "-c", command}
}

func mountedExecPath(mountRoot, virtualPath string) string {
	mountRoot = normalizedMountedRoot(mountRoot)
	cleaned := normalizeExecPath(virtualPath)
	if cleaned == "/" {
		return mountRoot
	}
	rel := strings.TrimPrefix(cleaned, "/")
	return filepath.Join(mountRoot, filepath.FromSlash(rel))
}

func translatedWorkingDir(plan ExecutionPlan, mountRoot string) string {
	dir := strings.TrimSpace(plan.WorkingDir)
	if dir == "" {
		return normalizedMountedRoot(mountRoot)
	}
	return mountedExecPath(mountRoot, dir)
}

func translatedExecutionEnv(plan ExecutionPlan, extra map[string]string, mountRoot string) []string {
	env := translatedExecutionEnvMap(plan, extra, mountRoot)
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func translatedExecutionEnvMap(plan ExecutionPlan, extra map[string]string, mountRoot string) map[string]string {
	env := baseExecutionEnv()
	mergeExecutionEnv(env, plan.Env, plan, mountRoot)
	mergeExecutionEnv(env, extra, plan, mountRoot)
	return env
}

func baseExecutionEnv() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !keepExecutionEnvKey(key) {
			continue
		}
		env[key] = value
	}
	return env
}

func keepExecutionEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	switch {
	case key == "PATH":
		return true
	case key == "LANG":
		return true
	case key == "TERM":
		return true
	case key == "TZ":
		return true
	case strings.HasPrefix(key, "LC_"):
		return true
	case key == "SYSTEMROOT":
		return true
	case key == "COMSPEC":
		return true
	case key == "PATHEXT":
		return true
	default:
		return false
	}
}

func mergeExecutionEnv(dst map[string]string, values map[string]string, plan ExecutionPlan, mountRoot string) {
	for key, value := range values {
		translated := translateExecutionEnvValue(plan, mountRoot, value)
		if isPathListKey(key) {
			dst[key] = mergeExecutionPathList(dst[key], translated)
			continue
		}
		dst[key] = translated
	}
}

func translateExecutionEnvValue(plan ExecutionPlan, mountRoot, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	if strings.Contains(value, string(os.PathListSeparator)) {
		return translateExecutionPathList(plan, mountRoot, value)
	}
	if !looksLikeExecPath(value) || !matchesMountedNamespace(plan, value) {
		return value
	}
	return mountedExecPath(mountRoot, value)
}

func translateExecutionPathList(plan ExecutionPlan, mountRoot, value string) string {
	parts := filepath.SplitList(value)
	if len(parts) == 0 {
		return value
	}
	for i, part := range parts {
		parts[i] = translateExecutionEnvValue(plan, mountRoot, part)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func looksLikeExecPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, string(os.PathListSeparator)) {
		return false
	}
	if filepath.IsAbs(trimmed) {
		return true
	}
	if len(trimmed) > 2 && trimmed[1] == ':' {
		return true
	}
	return strings.HasPrefix(trimmed, "/")
}

func matchesMountedNamespace(plan ExecutionPlan, value string) bool {
	cleaned := normalizeExecPath(value)
	for _, mount := range plan.Mounts {
		root := normalizeExecPath(mount.VirtualPath)
		if cleaned == root || hasExecPrefix(cleaned, root) || hasExecPrefix(root, cleaned) {
			return true
		}
	}
	return false
}

func isPathListKey(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "PATH")
}

func mergeExecutionPathList(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return extra + string(os.PathListSeparator) + base
}

func normalizedMountedRoot(mountRoot string) string {
	if len(mountRoot) == 2 && mountRoot[1] == ':' {
		return mountRoot + string(os.PathSeparator)
	}
	return mountRoot
}

func hostProjectedExecutionArgv(plan ExecutionPlan, extra map[string]string, mountRoot string, argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("purevfs: missing execution argv")
	}
	env := translatedExecutionEnvMap(plan, extra, mountRoot)
	out := append([]string(nil), argv...)
	executable, err := resolveHostExecutionExecutable(plan, env, mountRoot, out[0])
	if err != nil {
		return nil, err
	}
	out[0] = executable
	if idx := shellCommandIndex(out); idx >= 0 {
		out[idx] = translateShellCommand(plan, mountRoot, out[idx])
		return out, nil
	}
	for i := 1; i < len(out); i++ {
		out[i] = translateExecutionArgument(plan, mountRoot, out[i])
	}
	return out, nil
}

func resolveHostExecutionExecutable(plan ExecutionPlan, env map[string]string, mountRoot, executable string) (string, error) {
	trimmed := strings.TrimSpace(executable)
	if trimmed == "" {
		return "", fmt.Errorf("purevfs: missing executable")
	}
	if containsExecPathSeparator(trimmed) {
		return translateExecutionArgument(plan, mountRoot, trimmed), nil
	}
	if looksLikeExecPath(trimmed) {
		return translateExecutionArgument(plan, mountRoot, trimmed), nil
	}
	if resolved, ok := lookupExecutionPath(trimmed, env); ok {
		return resolved, nil
	}
	return "", fs.ErrNotExist
}

func translateExecutionArgument(plan ExecutionPlan, mountRoot, value string) string {
	if !looksLikeExecPath(value) || !matchesMountedNamespace(plan, value) {
		return value
	}
	return mountedExecPath(mountRoot, value)
}

func translateShellCommand(plan ExecutionPlan, mountRoot, command string) string {
	translated := command
	for _, mount := range plan.Mounts {
		virtualRoot := normalizeExecPath(mount.VirtualPath)
		translated = strings.ReplaceAll(translated, virtualRoot, mountedExecPath(mountRoot, virtualRoot))
	}
	return translated
}

func shellCommandIndex(argv []string) int {
	if len(argv) >= 3 && shellUsesLastArgument(argv[0], argv[1:]) {
		return len(argv) - 1
	}
	return -1
}

func shellUsesLastArgument(executable string, args []string) bool {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	switch {
	case strings.EqualFold(name, "cmd.exe"):
		return len(args) >= 2 && strings.EqualFold(args[len(args)-2], "/c")
	case strings.HasSuffix(name, "sh"):
		return len(args) >= 2 && args[len(args)-2] == "-c"
	default:
		return false
	}
}

func containsExecPathSeparator(value string) bool {
	return strings.ContainsRune(value, '/') || strings.ContainsRune(value, '\\')
}

func lookupExecutionPath(name string, env map[string]string) (string, bool) {
	for _, dir := range filepath.SplitList(env["PATH"]) {
		if resolved, ok := lookupExecutionPathInDir(dir, name, env["PATHEXT"]); ok {
			return resolved, true
		}
	}
	return "", false
}

func lookupExecutionPathInDir(dir, name, pathext string) (string, bool) {
	for _, candidate := range executionPathCandidates(name, pathext) {
		full := filepath.Join(dir, candidate)
		if executionPathExists(full) {
			return full, true
		}
	}
	return "", false
}

func executionPathCandidates(name, pathext string) []string {
	if isWindowsPlatform() {
		return windowsExecutionPathCandidates(name, pathext)
	}
	return []string{name}
}

func windowsExecutionPathCandidates(name, pathext string) []string {
	if filepath.Ext(name) != "" {
		return []string{name}
	}
	parts := filepath.SplitList(strings.ReplaceAll(pathext, ";", string(os.PathListSeparator)))
	if len(parts) == 0 {
		return []string{name + ".exe", name + ".cmd", name + ".bat", name}
	}
	out := make([]string, 0, len(parts)+1)
	for _, ext := range parts {
		trimmed := strings.TrimSpace(ext)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, ".") {
			trimmed = "." + trimmed
		}
		out = append(out, name+trimmed)
	}
	return append(out, name)
}

func executionPathExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func captureExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func (r readOnlyExecutionFS) ReadFile(ctx context.Context, name string) ([]byte, error) {
	return r.base.ReadFile(ctx, name)
}

func (r readOnlyExecutionFS) MkdirAll(context.Context, string) error {
	return fs.ErrPermission
}

func (r readOnlyExecutionFS) WriteFile(context.Context, string, []byte) error {
	return fs.ErrPermission
}

func (r readOnlyExecutionFS) DeleteFile(context.Context, string) error {
	return fs.ErrPermission
}

func (r readOnlyExecutionFS) Exists(ctx context.Context, name string) (bool, error) {
	return r.base.Exists(ctx, name)
}

func (r readOnlyExecutionFS) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	return r.base.ListDir(ctx, dir)
}

func (r readOnlyExecutionFS) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	return r.base.Stat(ctx, name)
}

func (r readOnlyExecutionFS) WorkingDir() string {
	return r.base.WorkingDir()
}

func (r readOnlyExecutionFS) IsReadOnly() bool {
	return true
}
