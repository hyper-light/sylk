package tools

import (
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

var (
	ErrExecutorClosed        = errors.New("executor is closed")
	ErrInvalidWorkingDir     = errors.New("invalid working directory")
	ErrWorkingDirNotAbsolute = errors.New("working directory must be absolute")
	ErrWorkingDirOutside     = errors.New("working directory outside allowed boundaries")
	ErrPoolExhausted         = errors.New("subprocess pool exhausted")
)

type ResourcePool interface {
	Acquire(ctx context.Context, sessionID string, priority int) (ResourceHandle, error)
}

type ResourceHandle interface {
	Release()
}

type OutputStreamer interface {
	CreateStreams(streamTo io.Writer) (stdout, stderr *StreamWriter)
	ProcessOutput(tool string, stdout, stderr []byte) *ProcessedOutput
}

type TimeoutChecker interface {
	Start()
	OnOutput(line string)
	ShouldTimeout() bool
	Reset()
}

type TimeoutFactory interface {
	GetTimeoutForTool(toolName string) (TimeoutChecker, error)
}

type KillExecutor interface {
	Execute(pg *ProcessGroup, waitDone <-chan struct{}) KillResult
}

var DefaultShellPatterns = []string{"|", "&&", "||", ";", "*", "?", "$", "`", "(", ")", "<", ">"}

var DefaultEnvBlocklist = []string{
	"*_API_KEY", "*_SECRET", "*_TOKEN", "*_PASSWORD", "*_CREDENTIAL",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	"GITHUB_TOKEN", "OPENAI_API_KEY", "ANTHROPIC_API_KEY",
}

type ToolExecutorConfig struct {
	ShellPatterns  []string
	EnvBlocklist   []string
	ProjectRoot    string
	StagingRoot    string
	TempRoot       string
	AllowedDirs    []string
	DefaultTimeout time.Duration
	CheckInterval  time.Duration
	ToolTimeouts   map[string]time.Duration
	SIGINTGrace    time.Duration
	SIGTERMGrace   time.Duration
	SessionID      string
}

func DefaultToolExecutorConfig() ToolExecutorConfig {
	return ToolExecutorConfig{
		ShellPatterns:  DefaultShellPatterns,
		EnvBlocklist:   DefaultEnvBlocklist,
		DefaultTimeout: 60 * time.Second,
		CheckInterval:  time.Second,
		ToolTimeouts:   make(map[string]time.Duration),
		SIGINTGrace:    5 * time.Second,
		SIGTERMGrace:   3 * time.Second,
	}
}

type ToolExecutor struct {
	config         ToolExecutorConfig
	resourcePool   ResourcePool
	outputHandler  OutputStreamer
	timeoutFactory TimeoutFactory
	killManager    KillExecutor

	mu           sync.RWMutex
	activeGroups map[int]*ProcessGroup
	closed       bool
	groupCounter int
}

type ToolExecutorDeps struct {
	ResourcePool   ResourcePool
	OutputHandler  OutputStreamer
	TimeoutFactory TimeoutFactory
	KillManager    KillExecutor
}

func NewToolExecutor(cfg ToolExecutorConfig, deps *ToolExecutorDeps) *ToolExecutor {
	cfg = normalizeExecutorConfig(cfg)

	executor := &ToolExecutor{
		config:       cfg,
		activeGroups: make(map[int]*ProcessGroup),
	}

	if deps != nil {
		executor.resourcePool = deps.ResourcePool
		executor.outputHandler = deps.OutputHandler
		executor.timeoutFactory = deps.TimeoutFactory
		executor.killManager = deps.KillManager
	}

	return executor
}

func normalizeExecutorConfig(cfg ToolExecutorConfig) ToolExecutorConfig {
	cfg = normalizePatterns(cfg)
	cfg = normalizeTimeouts(cfg)
	return cfg
}

func normalizePatterns(cfg ToolExecutorConfig) ToolExecutorConfig {
	if len(cfg.ShellPatterns) == 0 {
		cfg.ShellPatterns = DefaultShellPatterns
	}
	if len(cfg.EnvBlocklist) == 0 {
		cfg.EnvBlocklist = DefaultEnvBlocklist
	}
	if cfg.ToolTimeouts == nil {
		cfg.ToolTimeouts = make(map[string]time.Duration)
	}
	return cfg
}

func normalizeTimeouts(cfg ToolExecutorConfig) ToolExecutorConfig {
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 60 * time.Second
	}
	if cfg.SIGINTGrace == 0 {
		cfg.SIGINTGrace = 5 * time.Second
	}
	if cfg.SIGTERMGrace == 0 {
		cfg.SIGTERMGrace = 3 * time.Second
	}
	return cfg
}

func (e *ToolExecutor) Execute(ctx context.Context, inv ToolInvocation) (*ToolResult, error) {
	if err := e.checkClosed(); err != nil {
		return nil, err
	}

	if err := e.validateInvocation(inv); err != nil {
		return nil, err
	}

	slot, err := e.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer e.releaseSlot(slot)

	return e.executeWithSlot(ctx, inv)
}

func (e *ToolExecutor) checkClosed() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return ErrExecutorClosed
	}
	return nil
}

func (e *ToolExecutor) validateInvocation(inv ToolInvocation) error {
	if inv.WorkingDir == "" {
		return nil
	}
	return e.validateWorkingDir(inv.WorkingDir)
}

func (e *ToolExecutor) acquireSlot(ctx context.Context) (ResourceHandle, error) {
	if e.resourcePool == nil {
		return nil, nil
	}
	handle, err := e.resourcePool.Acquire(ctx, e.config.SessionID, 1)
	if err != nil {
		return nil, ErrPoolExhausted
	}
	return handle, nil
}

func (e *ToolExecutor) releaseSlot(slot ResourceHandle) {
	if slot != nil {
		slot.Release()
	}
}

func (e *ToolExecutor) executeWithSlot(ctx context.Context, inv ToolInvocation) (*ToolResult, error) {
	cmd := e.buildCommand(ctx, inv)
	pg := e.setupProcessGroup(cmd)

	stdout, stderr := e.setupOutputStreams(inv)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return e.runAndWait(ctx, pg, inv, stdout, stderr)
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

func (e *ToolExecutor) NeedsShell(command string) bool {
	return e.needsShell(command)
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
	key, _, found := strings.Cut(entry, "=")
	if !found {
		return entry
	}
	return key
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

	return e.checkBoundaries(resolved)
}

func (e *ToolExecutor) checkBoundaries(resolved string) error {
	boundaries := e.getAllowedBoundaries()
	if len(boundaries) == 0 {
		return nil
	}

	for _, boundary := range boundaries {
		if strings.HasPrefix(resolved, boundary) {
			return nil
		}
	}
	return ErrWorkingDirOutside
}

func (e *ToolExecutor) getAllowedBoundaries() []string {
	var boundaries []string
	if e.config.ProjectRoot != "" {
		boundaries = append(boundaries, e.config.ProjectRoot)
	}
	if e.config.StagingRoot != "" {
		boundaries = append(boundaries, e.config.StagingRoot)
	}
	if e.config.TempRoot != "" {
		boundaries = append(boundaries, e.config.TempRoot)
	}
	boundaries = append(boundaries, e.config.AllowedDirs...)
	return boundaries
}

func (e *ToolExecutor) setupProcessGroup(cmd *exec.Cmd) *ProcessGroup {
	pg := NewProcessGroup()
	pg.Setup(cmd)
	return pg
}

func (e *ToolExecutor) setupOutputStreams(inv ToolInvocation) (io.Writer, io.Writer) {
	streamTo := getStreamWriter(inv.StreamTo)

	if e.outputHandler != nil {
		return e.outputHandler.CreateStreams(streamTo)
	}

	return NewStreamWriter(streamTo, 1024*1024), NewStreamWriter(streamTo, 1024*1024)
}

func getStreamWriter(streamTo any) io.Writer {
	if streamTo == nil {
		return nil
	}
	if w, ok := streamTo.(io.Writer); ok {
		return w
	}
	return nil
}

func getStdinReader(stdin any) io.Reader {
	if stdin == nil {
		return nil
	}
	if r, ok := stdin.(io.Reader); ok {
		return r
	}
	return nil
}

func (e *ToolExecutor) runAndWait(ctx context.Context, pg *ProcessGroup, inv ToolInvocation, stdout, stderr io.Writer) (*ToolResult, error) {
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

	result := e.waitWithAdaptiveTimeout(ctx, pg, waitDone, inv)
	result.Duration = time.Since(startTime)

	e.collectOutput(result, stdout, stderr)
	e.parseOutput(result, inv.Tool)

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

func (e *ToolExecutor) waitWithAdaptiveTimeout(ctx context.Context, pg *ProcessGroup, waitDone <-chan error, inv ToolInvocation) *ToolResult {
	timeout := e.getTimeout(inv)
	checker := e.getTimeoutChecker(inv.Tool)

	if checker != nil {
		checker.Start()
		return e.waitWithChecker(ctx, pg, waitDone, timeout, checker)
	}

	return e.waitWithFixedTimeout(ctx, pg, waitDone, timeout)
}

func (e *ToolExecutor) getTimeout(inv ToolInvocation) time.Duration {
	if inv.Timeout > 0 {
		return inv.Timeout
	}
	if toolTimeout, ok := e.config.ToolTimeouts[inv.Tool]; ok {
		return toolTimeout
	}
	return e.config.DefaultTimeout
}

func (e *ToolExecutor) getTimeoutChecker(tool string) TimeoutChecker {
	if e.timeoutFactory == nil {
		return nil
	}
	checker, err := e.timeoutFactory.GetTimeoutForTool(tool)
	if err != nil {
		return nil
	}
	return checker
}

func (e *ToolExecutor) waitWithChecker(ctx context.Context, pg *ProcessGroup, waitDone <-chan error, maxTimeout time.Duration, checker TimeoutChecker) *ToolResult {
	timers := newWaitTimers(maxTimeout)
	defer timers.stop()

	for {
		result := e.selectWaitEvent(ctx, pg, waitDone, timers, checker)
		if result != nil {
			return result
		}
	}
}

type waitTimers struct {
	ticker   *time.Ticker
	maxTimer *time.Timer
}

func newWaitTimers(maxTimeout time.Duration) *waitTimers {
	return &waitTimers{
		ticker:   time.NewTicker(time.Second),
		maxTimer: time.NewTimer(maxTimeout),
	}
}

func (t *waitTimers) stop() {
	t.ticker.Stop()
	t.maxTimer.Stop()
}

func (e *ToolExecutor) selectWaitEvent(ctx context.Context, pg *ProcessGroup, waitDone <-chan error, timers *waitTimers, checker TimeoutChecker) *ToolResult {
	if result := e.checkImmediateEvents(pg, waitDone, ctx.Done()); result != nil {
		return result
	}
	return e.checkTimerEvents(pg, waitDone, timers, checker)
}

func (e *ToolExecutor) checkImmediateEvents(pg *ProcessGroup, waitDone <-chan error, ctxDone <-chan struct{}) *ToolResult {
	select {
	case err := <-waitDone:
		return e.buildNormalResult(err, pg)
	case <-ctxDone:
		return e.killAndBuildResult(pg, waitDone, "context")
	default:
		return nil
	}
}

func (e *ToolExecutor) checkTimerEvents(pg *ProcessGroup, waitDone <-chan error, timers *waitTimers, checker TimeoutChecker) *ToolResult {
	select {
	case err := <-waitDone:
		return e.buildNormalResult(err, pg)
	case <-timers.maxTimer.C:
		return e.killAndBuildResult(pg, waitDone, "timeout")
	case <-timers.ticker.C:
		return e.checkAdaptiveTimeout(pg, waitDone, checker)
	}
}

func (e *ToolExecutor) checkAdaptiveTimeout(pg *ProcessGroup, waitDone <-chan error, checker TimeoutChecker) *ToolResult {
	if checker.ShouldTimeout() {
		return e.killAndBuildResult(pg, waitDone, "adaptive_timeout")
	}
	return nil
}

func (e *ToolExecutor) waitWithFixedTimeout(ctx context.Context, pg *ProcessGroup, waitDone <-chan error, timeout time.Duration) *ToolResult {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-waitDone:
		return e.buildNormalResult(err, pg)
	case <-ctx.Done():
		return e.killAndBuildResult(pg, waitDone, "context")
	case <-timer.C:
		return e.killAndBuildResult(pg, waitDone, "timeout")
	}
}

func (e *ToolExecutor) buildNormalResult(err error, pg *ProcessGroup) *ToolResult {
	result := &ToolResult{
		Killed: pg.IsKilled(),
	}
	if err != nil {
		result.ExitCode = extractExitCode(err)
	}
	return result
}

func (e *ToolExecutor) killAndBuildResult(pg *ProcessGroup, waitDone <-chan error, signal string) *ToolResult {
	e.executeKillSequence(pg, waitDone)
	return &ToolResult{
		ExitCode:   -1,
		Killed:     true,
		KillSignal: signal,
		Partial:    true,
	}
}

func (e *ToolExecutor) executeKillSequence(pg *ProcessGroup, waitDone <-chan error) {
	if e.killManager != nil {
		structDone := e.convertToStructChan(waitDone)
		e.killManager.Execute(pg, structDone)
		return
	}
	pg.Kill()
	<-waitDone
}

func (e *ToolExecutor) convertToStructChan(errChan <-chan error) <-chan struct{} {
	structChan := make(chan struct{})
	go func() {
		<-errChan
		close(structChan)
	}()
	return structChan
}

func extractExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func (e *ToolExecutor) collectOutput(result *ToolResult, stdout, stderr io.Writer) {
	if sw, ok := stdout.(*StreamWriter); ok {
		result.Stdout = sw.Bytes()
	}
	if sw, ok := stderr.(*StreamWriter); ok {
		result.Stderr = sw.Bytes()
	}
}

func (e *ToolExecutor) parseOutput(result *ToolResult, tool string) {
	if !e.shouldParseOutput(tool) {
		return
	}
	processed := e.outputHandler.ProcessOutput(tool, result.Stdout, result.Stderr)
	e.applyParsedOutput(result, processed)
}

func (e *ToolExecutor) shouldParseOutput(tool string) bool {
	return e.outputHandler != nil && tool != ""
}

func (e *ToolExecutor) applyParsedOutput(result *ToolResult, processed *ProcessedOutput) {
	if processed != nil && processed.Type == OutputTypeParsed {
		result.ParsedOutput = processed.Parsed
	}
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
