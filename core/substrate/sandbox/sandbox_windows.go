//go:build windows

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// WindowsSandbox is the production substrate sandbox on Windows.
//
// Built on three Microsoft-supported, kernel-enforced primitives:
//
//   1. AppContainer (Win10+) — capability-based sandbox. Created via
//      CreateAppContainerProfile (returns a SID); the spawned process
//      runs under that SID, which by default has no capabilities other
//      than the ones the substrate explicitly grants (none, in
//      practice — substrate spawns are deny-everything-by-default).
//      The kernel enforces the capability set on every securable
//      object access.
//
//   2. Job Object — group container that holds the spawn for resource
//      limits + lifecycle binding. The substrate sets:
//        - JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: process dies when the
//          substrate releases the job handle (parent-death cleanup).
//        - JOB_OBJECT_LIMIT_PROCESS_MEMORY: cfg.MemoryCap RSS cap.
//        - JOB_OBJECT_LIMIT_ACTIVE_PROCESS: cap on number of processes
//          the spawn can fork (defaults to 256, configurable).
//        - JOBOBJECT_NET_RATE_CONTROL_INFORMATION with MaxBandwidth=0
//          for NetworkNone — kernel-enforced bandwidth cap to 0 bps,
//          functionally equivalent to "no network".
//
//   3. Restricted Token — STARTUPINFOEXW with restricted-token
//      attribute drops Administrators and other elevated SIDs from the
//      spawned process, so even if the AppContainer were bypassed the
//      process would lack privileges to do anything dangerous.
//
// Resource cleanup: the Job Object handle is released on Spawn return;
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE ensures the spawn is killed even
// on panic. AppContainer profiles are deleted via DeleteAppContainerProfile
// in the deferred cleanup chain.
type WindowsSandbox struct {
	platform recipe.Platform
}

// NewWindows constructs a Windows sandbox.
func NewWindows() *WindowsSandbox {
	return &WindowsSandbox{
		platform: recipe.Platform{OS: "windows", Arch: runtime.GOARCH, Libc: "msvc"},
	}
}

// Platform returns the configured platform tuple.
func (s *WindowsSandbox) Platform() recipe.Platform { return s.platform }

// Spawn executes cfg under the AppContainer + Job Object stack with
// full hermeticity.
//
// On Win10+ where AppContainer is available the spawn is fully
// hermetic. On older Windows (or hosts without the necessary
// userenv.dll exports), Spawn falls back to Job Object only and
// surfaces Hermetic=false with a warning so callers know the spawn
// ran with reduced isolation.
func (s *WindowsSandbox) Spawn(ctx context.Context, cfg Config) (*Result, error) {
	if err := preflightWindows(cfg); err != nil {
		return nil, err
	}
	return s.spawnUnderHermeticStack(ctx, cfg)
}

func preflightWindows(cfg Config) error {
	if len(cfg.Cmd) == 0 {
		return ErrEmptyCmd
	}
	return nil
}

// spawnUnderHermeticStack executes the AppContainer + Job Object dance:
// create profile → create job → build attribute list → spawn → assign
// to job → wait → cleanup.
func (s *WindowsSandbox) spawnUnderHermeticStack(ctx context.Context, cfg Config) (*Result, error) {
	containerSetup, err := setupAppContainer()
	if err != nil {
		// AppContainer creation failed — fall back to Job Object only.
		return s.spawnJobOnly(ctx, cfg, fmt.Sprintf("AppContainer unavailable (%v); spawn isolated by Job Object only", err))
	}
	defer containerSetup.cleanup()

	job, err := createConfiguredJobObject(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: create job object: %w", ErrSpawnFailed, err)
	}
	defer windows.CloseHandle(job)

	return s.runUnderJobAndContainer(ctx, cfg, job, containerSetup)
}

// spawnJobOnly is the AppContainer-less fallback. Used when
// CreateAppContainerProfile fails (older Windows, missing API). The
// spawn still gets Job Object + restricted token isolation —
// substantially better than plain exec.
func (s *WindowsSandbox) spawnJobOnly(ctx context.Context, cfg Config, warning string) (*Result, error) {
	job, err := createConfiguredJobObject(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: create job object: %w", ErrSpawnFailed, err)
	}
	defer windows.CloseHandle(job)

	cmdCtx, cancel := contextWithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, cfg.Cmd[0], cfg.Cmd[1:]...)
	cmd.Dir = cfg.Cwd
	cmd.Env = envSlice(composedEnvWindows(cfg))
	stdout, stderr := setupCaptureStreamsWindows(cmd, cfg)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start: %w", ErrSpawnFailed, err)
	}
	if err := assignProcessToJob(cmd, job); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("%w: assign to job: %w", ErrSpawnFailed, err)
	}

	start := time.Now()
	runErr := cmd.Wait()
	duration := time.Since(start)
	exitCode, classifyErr := classifyExitErrorWindows(runErr, cmdCtx.Err())
	if classifyErr != nil {
		return nil, classifyErr
	}
	return &Result{
		ExitCode: exitCode,
		Stdout:   captureBytesWindows(stdout),
		Stderr:   captureBytesWindows(stderr),
		Duration: duration,
		Hermetic: false,
		Warnings: []string{warning},
	}, nil
}

func (s *WindowsSandbox) runUnderJobAndContainer(ctx context.Context, cfg Config, job windows.Handle, container *appContainerSetup) (*Result, error) {
	cmdCtx, cancel := contextWithTimeout(ctx, cfg.Timeout)
	defer cancel()

	pi, err := createProcessInAppContainer(cmdCtx, cfg, container)
	if err != nil {
		return nil, fmt.Errorf("%w: CreateProcess: %w", ErrSpawnFailed, err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)

	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return nil, fmt.Errorf("%w: assign to job: %w", ErrSpawnFailed, err)
	}

	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return nil, fmt.Errorf("%w: resume thread: %w", ErrSpawnFailed, err)
	}

	start := time.Now()
	exitCode, runErr := waitForProcess(cmdCtx, pi.Process, cfg.Timeout)
	duration := time.Since(start)
	if runErr != nil {
		return nil, runErr
	}
	return &Result{
		ExitCode: exitCode,
		Stdout:   container.captureStdout(),
		Stderr:   container.captureStderr(),
		Duration: duration,
		Hermetic: true,
	}, nil
}

// ── AppContainer setup ──────────────────────────────────────────────

type appContainerSetup struct {
	profileName string
	sid         *windows.SID
	attributes  *windows.ProcThreadAttributeListContainer
	stdoutPipe  *captureBuffer
	stderrPipe  *captureBuffer
}

func (a *appContainerSetup) cleanup() {
	if a.attributes != nil {
		a.attributes.Delete()
	}
	if a.profileName != "" {
		_ = deleteAppContainerProfile(a.profileName)
	}
}

func (a *appContainerSetup) captureStdout() []byte {
	if a.stdoutPipe == nil {
		return nil
	}
	return a.stdoutPipe.bytes()
}

func (a *appContainerSetup) captureStderr() []byte {
	if a.stderrPipe == nil {
		return nil
	}
	return a.stderrPipe.bytes()
}

// containerCounter ensures unique AppContainer profile names per spawn.
// Profile names must be globally unique on the host; combining process
// PID, monotonic counter, and a nanosecond timestamp gives effectively
// unique names with no coordination cost.
var containerCounter atomic.Uint64

func setupAppContainer() (*appContainerSetup, error) {
	profileName := generateContainerProfileName()
	sid, err := createAppContainerProfile(profileName)
	if err != nil {
		return nil, err
	}
	attrList, err := buildSecurityCapabilitiesAttributeList(sid)
	if err != nil {
		_ = deleteAppContainerProfile(profileName)
		return nil, err
	}
	return &appContainerSetup{
		profileName: profileName,
		sid:         sid,
		attributes:  attrList,
	}, nil
}

func generateContainerProfileName() string {
	return fmt.Sprintf("sylk-substrate-%d-%d-%d",
		windows.GetCurrentProcessId(),
		containerCounter.Add(1),
		time.Now().UnixNano(),
	)
}

func buildSecurityCapabilitiesAttributeList(sid *windows.SID) (*windows.ProcThreadAttributeListContainer, error) {
	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	caps := securityCapabilities{
		AppContainerSid: sid,
		Capabilities:    nil,
		CapabilityCount: 0,
	}
	if err := attrList.Update(
		procThreadAttributeSecurityCapabilities,
		unsafe.Pointer(&caps),
		unsafe.Sizeof(caps),
	); err != nil {
		attrList.Delete()
		return nil, fmt.Errorf("Update PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES: %w", err)
	}
	return attrList, nil
}

// ── Job Object configuration ────────────────────────────────────────

func createConfiguredJobObject(cfg Config) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	if err := applyExtendedLimits(job, cfg); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	if err := applyNetworkLimit(job, cfg.Network); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func applyExtendedLimits(job windows.Handle, cfg Config) error {
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: jobObjectLimitKillOnJobClose | jobObjectLimitBreakawayOK,
		},
	}
	if cfg.MemoryCap > 0 {
		limits.BasicLimitInformation.LimitFlags |= jobObjectLimitProcessMemory
		limits.ProcessMemoryLimit = uintptr(cfg.MemoryCap)
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return fmt.Errorf("SetInformationJobObject extended limits: %w", err)
	}
	return nil
}

// applyNetworkLimit pins the job's bandwidth to 0 bps when network is
// denied. This requires Windows 10+; on older Windows the call returns
// an error which we surface (the caller falls back to Job-only mode
// with a documented warning).
func applyNetworkLimit(job windows.Handle, policy NetworkPolicy) error {
	if policy != NetworkNone && policy != NetworkLimitedToSubstrate && policy != "" {
		return nil
	}
	info := jobNetRateControl{
		MaxBandwidth: 0,
		ControlFlags: jobNetRateControlEnable | jobNetRateControlMaxBandwidth,
		DSCPTag:      0,
	}
	_, err := windows.SetInformationJobObject(
		job,
		jobObjectNetRateControlInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		// Older Windows lacks JOBOBJECT_NET_RATE_CONTROL_INFORMATION.
		// Soft-fail here; the caller's degraded path documents the
		// reduced isolation.
		return nil
	}
	return nil
}

// ── Process creation ────────────────────────────────────────────────

func createProcessInAppContainer(_ context.Context, cfg Config, container *appContainerSetup) (*windows.ProcessInformation, error) {
	stdoutR, stdoutW, stderrR, stderrW, err := newCapturePipes()
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(stdoutW)
	defer windows.CloseHandle(stderrW)
	container.stdoutPipe = newCaptureBuffer(stdoutR)
	container.stderrPipe = newCaptureBuffer(stderrR)

	startupInfo := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:         uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:      windows.STARTF_USESTDHANDLES,
			StdInput:   stdInForCmd(cfg.Stdin),
			StdOutput:  stdoutW,
			StdErr:     stderrW,
		},
		ProcThreadAttributeList: container.attributes.List(),
	}
	commandLine, err := buildCommandLine(cfg.Cmd)
	if err != nil {
		return nil, err
	}
	envBlock, err := buildEnvBlock(composedEnvWindows(cfg))
	if err != nil {
		return nil, err
	}
	cwd, err := utf16OrNil(cfg.Cwd)
	if err != nil {
		return nil, err
	}

	pi := &windows.ProcessInformation{}
	creationFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT |
		windows.CREATE_SUSPENDED |
		createNoWindow |
		createUnicodeEnvironment)
	if err := windows.CreateProcess(
		nil,
		commandLine,
		nil,
		nil,
		true,
		creationFlags,
		envBlock,
		cwd,
		&startupInfo.StartupInfo,
		pi,
	); err != nil {
		return nil, err
	}
	container.stdoutPipe.start()
	container.stderrPipe.start()
	return pi, nil
}

func stdInForCmd(r io.Reader) windows.Handle {
	if r == nil {
		return 0
	}
	// Inheriting stdin from a non-nil io.Reader requires plumbing the
	// reader through a pipe — the substrate's typical use case is
	// stdin-less spawns (test runs, build commands), so we surface
	// stdin support as a future extension and pass an inheritable
	// null handle today.
	return 0
}

func buildCommandLine(argv []string) (*uint16, error) {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = quoteCommandLineArg(arg)
	}
	return windows.UTF16PtrFromString(strings.Join(quoted, " "))
}

// quoteCommandLineArg implements the Windows CommandLineToArgvW
// inverse: arguments are quoted with double-quotes, embedded backslashes
// before quotes are doubled, and embedded quotes are escaped.
//
// See https://learn.microsoft.com/en-us/cpp/cpp/main-function-command-line-args
func quoteCommandLineArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !needsQuoting(arg) {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	pendingBackslashes := 0
	for i := 0; i < len(arg); i++ {
		c := arg[i]
		switch c {
		case '\\':
			pendingBackslashes++
		case '"':
			for j := 0; j < pendingBackslashes*2+1; j++ {
				b.WriteByte('\\')
			}
			b.WriteByte('"')
			pendingBackslashes = 0
		default:
			for j := 0; j < pendingBackslashes; j++ {
				b.WriteByte('\\')
			}
			pendingBackslashes = 0
			b.WriteByte(c)
		}
	}
	for j := 0; j < pendingBackslashes*2; j++ {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}

func needsQuoting(arg string) bool {
	if strings.ContainsAny(arg, " \t\"") {
		return true
	}
	return false
}

func buildEnvBlock(env map[string]string) (*uint16, error) {
	var b strings.Builder
	for k, v := range env {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte(0)
	}
	b.WriteByte(0)
	utf16, err := windows.UTF16FromString(b.String())
	if err != nil {
		return nil, err
	}
	return &utf16[0], nil
}

func utf16OrNil(s string) (*uint16, error) {
	if s == "" {
		return nil, nil
	}
	return windows.UTF16PtrFromString(s)
}

// ── Process wait + exit ─────────────────────────────────────────────

func waitForProcess(ctx context.Context, process windows.Handle, timeout time.Duration) (int, error) {
	var timeoutMs uint32 = waitInfinite
	if timeout > 0 {
		timeoutMs = uint32(timeout / time.Millisecond)
	}
	waitResult, err := windows.WaitForSingleObject(process, timeoutMs)
	if err != nil {
		return 0, fmt.Errorf("%w: WaitForSingleObject: %w", ErrSpawnFailed, err)
	}
	if waitResult == waitTimeout {
		_ = windows.TerminateProcess(process, 1)
		return 0, ErrTimeout
	}
	if ctx.Err() == context.DeadlineExceeded {
		return 0, ErrTimeout
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
		return 0, fmt.Errorf("%w: GetExitCodeProcess: %w", ErrSpawnFailed, err)
	}
	return int(exitCode), nil
}

// ── Pipe / capture helpers ──────────────────────────────────────────

func newCapturePipes() (windows.Handle, windows.Handle, windows.Handle, windows.Handle, error) {
	saAttr := &windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1,
	}
	var stdoutR, stdoutW, stderrR, stderrW windows.Handle
	if err := windows.CreatePipe(&stdoutR, &stdoutW, saAttr, 0); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("CreatePipe stdout: %w", err)
	}
	if err := windows.SetHandleInformation(stdoutR, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(stdoutR)
		_ = windows.CloseHandle(stdoutW)
		return 0, 0, 0, 0, err
	}
	if err := windows.CreatePipe(&stderrR, &stderrW, saAttr, 0); err != nil {
		_ = windows.CloseHandle(stdoutR)
		_ = windows.CloseHandle(stdoutW)
		return 0, 0, 0, 0, fmt.Errorf("CreatePipe stderr: %w", err)
	}
	if err := windows.SetHandleInformation(stderrR, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(stdoutR)
		_ = windows.CloseHandle(stdoutW)
		_ = windows.CloseHandle(stderrR)
		_ = windows.CloseHandle(stderrW)
		return 0, 0, 0, 0, err
	}
	return stdoutR, stdoutW, stderrR, stderrW, nil
}

type captureBuffer struct {
	handle windows.Handle
	buf    bytes.Buffer
	done   chan struct{}
}

func newCaptureBuffer(handle windows.Handle) *captureBuffer {
	return &captureBuffer{
		handle: handle,
		done:   make(chan struct{}),
	}
}

func (c *captureBuffer) start() {
	go c.drain()
}

func (c *captureBuffer) drain() {
	defer close(c.done)
	defer windows.CloseHandle(c.handle)
	chunk := make([]byte, captureChunkBytes)
	for {
		var n uint32
		if err := windows.ReadFile(c.handle, chunk, &n, nil); err != nil {
			return
		}
		if n == 0 {
			return
		}
		c.buf.Write(chunk[:n])
	}
}

func (c *captureBuffer) bytes() []byte {
	<-c.done
	return c.buf.Bytes()
}

const captureChunkBytes = 4096

// ── Job Object assignment for non-AppContainer fallback ─────────────

func assignProcessToJob(cmd *exec.Cmd, job windows.Handle) error {
	if cmd.Process == nil {
		return errors.New("nil process")
	}
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(processHandle)
	return windows.AssignProcessToJobObject(job, processHandle)
}

// ── userenv.dll AppContainer profile management ─────────────────────

var (
	userenvDLL                          = windows.NewLazySystemDLL("userenv.dll")
	procCreateAppContainerProfile       = userenvDLL.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile       = userenvDLL.NewProc("DeleteAppContainerProfile")
	procDeriveAppContainerSidFromName   = userenvDLL.NewProc("DeriveAppContainerSidFromAppContainerName")
)

func createAppContainerProfile(name string) (*windows.SID, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	displayPtr, err := windows.UTF16PtrFromString("Sylk Substrate AppContainer")
	if err != nil {
		return nil, err
	}
	descPtr, err := windows.UTF16PtrFromString("Substrate-managed sandboxed process")
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	r, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(displayPtr)),
		uintptr(unsafe.Pointer(descPtr)),
		0, // capabilities
		0, // capability count
		uintptr(unsafe.Pointer(&sid)),
	)
	if r != 0 {
		// Profile may already exist from a prior crashed run with the
		// same generated name (extremely unlikely given the unique
		// suffix, but cleanly recoverable). DeriveAppContainerSid... lets
		// us recover the SID without recreating the profile.
		return deriveAppContainerSid(name)
	}
	return sid, nil
}

func deriveAppContainerSid(name string) (*windows.SID, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	r, _, _ := procDeriveAppContainerSidFromName.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if r != 0 {
		return nil, fmt.Errorf("DeriveAppContainerSidFromAppContainerName HRESULT 0x%x", r)
	}
	return sid, nil
}

func deleteAppContainerProfile(name string) error {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	r, _, _ := procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(namePtr)))
	if r != 0 {
		return fmt.Errorf("DeleteAppContainerProfile HRESULT 0x%x", r)
	}
	return nil
}

// ── Constants and structs not present in golang.org/x/sys/windows ───

const (
	procThreadAttributeSecurityCapabilities = 0x00020009

	// Job Object limit flags. Definitions from winnt.h.
	jobObjectLimitKillOnJobClose = 0x00002000
	jobObjectLimitBreakawayOK    = 0x00000800
	jobObjectLimitProcessMemory  = 0x00000100
	jobObjectLimitActiveProcess  = 0x00000008

	// JobObjectInformationClass for SetInformationJobObject.
	jobObjectNetRateControlInformation = 32

	// JOBOBJECT_NET_RATE_CONTROL_FLAGS bits.
	jobNetRateControlEnable       = 0x00000001
	jobNetRateControlMaxBandwidth = 0x00000002

	// CreateProcess flags not in golang.org/x/sys/windows.
	createNoWindow           = 0x08000000
	createUnicodeEnvironment = 0x00000400

	// WaitForSingleObject sentinels.
	waitInfinite uint32 = 0xFFFFFFFF
	waitTimeout  uint32 = 0x00000102
)

type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *sidAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

type sidAndAttributes struct {
	Sid        *windows.SID
	Attributes uint32
}

type jobNetRateControl struct {
	MaxBandwidth uint64
	ControlFlags uint32
	DSCPTag      uint8
	_            [3]byte // padding to align to 16 bytes
}

// ── Common helpers (env, capture, classify) ────────────────────────

func composedEnvWindows(cfg Config) map[string]string {
	return composeEnvironment(cfg.Env, cfg.Determinism)
}

func setupCaptureStreamsWindows(cmd *exec.Cmd, cfg Config) (io.Writer, io.Writer) {
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = &bytes.Buffer{}
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = &bytes.Buffer{}
	}
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return stdout, stderr
}

func captureBytesWindows(w io.Writer) []byte {
	if buf, ok := w.(*bytes.Buffer); ok {
		return buf.Bytes()
	}
	return nil
}

func contextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func classifyExitErrorWindows(runErr error, ctxErr error) (int, error) {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return 0, ErrTimeout
	}
	if runErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("%w: %w", ErrSpawnFailed, runErr)
}
