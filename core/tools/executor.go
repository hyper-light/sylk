package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func getStdinReader(stdin interface{}) io.Reader {
	if stdin == nil {
		return nil
	}
	if r, ok := stdin.(io.Reader); ok {
		return r
	}
	return nil
}

var (
	ErrExecutorClosed        = errors.New("executor is closed")
	ErrInvalidWorkingDir     = errors.New("invalid working directory")
	ErrWorkingDirNotAbsolute = errors.New("working directory must be absolute")
	ErrWorkingDirOutside     = errors.New("working directory outside allowed boundaries")
)

var DefaultShellPatterns = []string{"|", "&&", "||", ";", "*", "?", "$", "`", "(", ")", "<", ">"}

var DefaultEnvBlocklist = []string{
	"*_API_KEY",
	"*_SECRET",
	"*_TOKEN",
	"*_PASSWORD",
	"*_CREDENTIAL",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"GITHUB_TOKEN",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
}

type ToolExecutorConfig struct {
	ShellPatterns  []string
	EnvBlocklist   []string
	AllowedDirs    []string
	DefaultTimeout time.Duration
	CheckInterval  time.Duration
}

func DefaultToolExecutorConfig() ToolExecutorConfig {
	return ToolExecutorConfig{
		ShellPatterns:  DefaultShellPatterns,
		EnvBlocklist:   DefaultEnvBlocklist,
		AllowedDirs:    []string{},
		DefaultTimeout: 60 * time.Second,
		CheckInterval:  1 * time.Second,
	}
}

type ToolExecutor struct {
	config       ToolExecutorConfig
	timeoutMgr   *ToolTimeoutManager
	mu           sync.RWMutex
	activeGroups map[int]*ProcessGroup
	closed       bool
	groupCounter int
}

func NewToolExecutor(cfg ToolExecutorConfig) *ToolExecutor {
	cfg = normalizeExecutorConfig(cfg)

	return &ToolExecutor{
		config:       cfg,
		timeoutMgr:   NewToolTimeoutManager(DefaultAdaptiveTimeoutConfig()),
		activeGroups: make(map[int]*ProcessGroup),
	}
}

func normalizeExecutorConfig(cfg ToolExecutorConfig) ToolExecutorConfig {
	cfg.ShellPatterns = defaultSlice(cfg.ShellPatterns, DefaultShellPatterns)
	cfg.EnvBlocklist = defaultSlice(cfg.EnvBlocklist, DefaultEnvBlocklist)
	cfg.DefaultTimeout = defaultExecDuration(cfg.DefaultTimeout, 60*time.Second)
	cfg.CheckInterval = defaultExecDuration(cfg.CheckInterval, 1*time.Second)
	return cfg
}

func defaultSlice(val, def []string) []string {
	if len(val) == 0 {
		return def
	}
	return val
}

func defaultExecDuration(val, def time.Duration) time.Duration {
	if val == 0 {
		return def
	}
	return val
}

func (e *ToolExecutor) Execute(ctx context.Context, inv ToolInvocation) (*ToolResult, error) {
	if err := e.checkClosed(); err != nil {
		return nil, err
	}

	if inv.WorkingDir != "" {
		if err := e.validateWorkingDir(inv.WorkingDir); err != nil {
			return nil, err
		}
	}

	cmd := e.buildCommand(ctx, inv)
	pg := e.setupProcessGroup(cmd)

	return e.executeWithTracking(ctx, cmd, pg, inv)
}

func (e *ToolExecutor) checkClosed() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return ErrExecutorClosed
	}
	return nil
}

func (e *ToolExecutor) buildCommand(ctx context.Context, inv ToolInvocation) *exec.Cmd {
	var cmd *exec.Cmd

	if e.needsShell(inv.Command) {
		cmd = exec.CommandContext(ctx, "sh", "-c", inv.Command)
	} else {
		cmd = e.buildDirectCommand(ctx, inv)
	}

	e.configureCommand(cmd, inv)
	return cmd
}

func (e *ToolExecutor) buildDirectCommand(ctx context.Context, inv ToolInvocation) *exec.Cmd {
	if len(inv.Args) > 0 {
		return exec.CommandContext(ctx, inv.Command, inv.Args...)
	}
	return exec.CommandContext(ctx, inv.Command)
}

func (e *ToolExecutor) configureCommand(cmd *exec.Cmd, inv ToolInvocation) {
	cmd.Dir = inv.WorkingDir
	cmd.Env = e.buildEnvironment(inv.Env)
	cmd.Stdin = getStdinReader(inv.Stdin)
}

func (e *ToolExecutor) needsShell(command string) bool {
	for _, pattern := range e.config.ShellPatterns {
		if strings.Contains(command, pattern) {
			return true
		}
	}
	return false
}

func (e *ToolExecutor) buildEnvironment(extra map[string]string) []string {
	env := os.Environ()
	filtered := e.filterBlocklist(env)
	return e.mergeExtraEnv(filtered, extra)
}

func (e *ToolExecutor) filterBlocklist(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key := extractEnvKey(entry)
		if !e.matchesBlocklist(key) {
			result = append(result, entry)
		}
	}
	return result
}

func extractEnvKey(entry string) string {
	idx := strings.Index(entry, "=")
	if idx == -1 {
		return entry
	}
	return entry[:idx]
}

func (e *ToolExecutor) matchesBlocklist(key string) bool {
	for _, pattern := range e.config.EnvBlocklist {
		if matchEnvPattern(pattern, key) {
			return true
		}
	}
	return false
}

func matchEnvPattern(pattern, key string) bool {
	if strings.HasPrefix(pattern, "*") {
		suffix := pattern[1:]
		return strings.HasSuffix(key, suffix)
	}
	return pattern == key
}

func (e *ToolExecutor) mergeExtraEnv(env []string, extra map[string]string) []string {
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func (e *ToolExecutor) validateWorkingDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return ErrWorkingDirNotAbsolute
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ErrInvalidWorkingDir
	}

	if len(e.config.AllowedDirs) == 0 {
		return nil
	}

	return e.checkAllowedBoundaries(resolved)
}

func (e *ToolExecutor) checkAllowedBoundaries(resolved string) error {
	for _, allowed := range e.config.AllowedDirs {
		if strings.HasPrefix(resolved, allowed) {
			return nil
		}
	}
	return ErrWorkingDirOutside
}

func (e *ToolExecutor) setupProcessGroup(cmd *exec.Cmd) *ProcessGroup {
	pg := NewProcessGroup()
	pg.Setup(cmd)
	return pg
}

func (e *ToolExecutor) executeWithTracking(ctx context.Context, cmd *exec.Cmd, pg *ProcessGroup, inv ToolInvocation) (*ToolResult, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()

	if err := pg.Start(); err != nil {
		return nil, err
	}

	e.trackProcessGroup(pg)
	defer e.untrackProcessGroup(pg)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- pg.Wait()
	}()

	result := e.waitForCompletion(ctx, pg, waitDone, inv.Timeout)
	result.Duration = time.Since(startTime)
	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()

	return result, nil
}

func (e *ToolExecutor) trackProcessGroup(pg *ProcessGroup) {
	e.mu.Lock()
	e.groupCounter++
	e.activeGroups[e.groupCounter] = pg
	e.mu.Unlock()
}

func (e *ToolExecutor) untrackProcessGroup(pg *ProcessGroup) {
	e.mu.Lock()
	for id, group := range e.activeGroups {
		if group == pg {
			delete(e.activeGroups, id)
			break
		}
	}
	e.mu.Unlock()
}

func (e *ToolExecutor) waitForCompletion(ctx context.Context, pg *ProcessGroup, waitDone <-chan error, timeout time.Duration) *ToolResult {
	timeout = e.effectiveTimeout(timeout)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	return e.selectWaitResult(ctx, pg, waitDone, timer.C)
}

func (e *ToolExecutor) effectiveTimeout(timeout time.Duration) time.Duration {
	if timeout == 0 {
		return e.config.DefaultTimeout
	}
	return timeout
}

func (e *ToolExecutor) selectWaitResult(ctx context.Context, pg *ProcessGroup, waitDone <-chan error, timerC <-chan time.Time) *ToolResult {
	select {
	case err := <-waitDone:
		return e.buildResult(err, pg)
	case <-ctx.Done():
		return e.handleCancellation(pg, waitDone)
	case <-timerC:
		return e.handleTimeout(pg, waitDone)
	}
}

func (e *ToolExecutor) buildResult(err error, pg *ProcessGroup) *ToolResult {
	result := &ToolResult{
		Killed: pg.IsKilled(),
	}

	if err != nil {
		result.ExitCode = extractExitCode(err)
	}

	return result
}

func extractExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func (e *ToolExecutor) handleCancellation(pg *ProcessGroup, waitDone <-chan error) *ToolResult {
	pg.Kill()
	<-waitDone

	return &ToolResult{
		ExitCode:   -1,
		Killed:     true,
		KillSignal: "context",
		Partial:    true,
	}
}

func (e *ToolExecutor) handleTimeout(pg *ProcessGroup, waitDone <-chan error) *ToolResult {
	pg.Kill()
	<-waitDone

	return &ToolResult{
		ExitCode:   -1,
		Killed:     true,
		KillSignal: "timeout",
		Partial:    true,
	}
}

func (e *ToolExecutor) NeedsShell(command string) bool {
	return e.needsShell(command)
}

func (e *ToolExecutor) KillAll() {
	e.mu.Lock()
	groups := make([]*ProcessGroup, 0, len(e.activeGroups))
	for _, pg := range e.activeGroups {
		groups = append(groups, pg)
	}
	e.mu.Unlock()

	for _, pg := range groups {
		pg.Kill()
	}
}

func (e *ToolExecutor) ActiveCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.activeGroups)
}

func (e *ToolExecutor) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrExecutorClosed
	}
	e.closed = true
	groups := make([]*ProcessGroup, 0, len(e.activeGroups))
	for _, pg := range e.activeGroups {
		groups = append(groups, pg)
	}
	e.mu.Unlock()

	for _, pg := range groups {
		pg.Kill()
	}

	return nil
}
