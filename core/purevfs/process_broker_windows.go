//go:build windows

package purevfs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsDisableMaxPrivilege = 0x1
	windowsLuaToken            = 0x4
	windowsWriteRestricted     = 0x8
)

var createRestrictedTokenProc = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

func isWindowsPlatform() bool {
	return true
}

func platformExecutionCapabilities() ExecutionCapabilities {
	if strictExecutionProbe() != nil {
		return ExecutionCapabilities{}
	}
	return StrictBrokerCapabilities()
}

func strictExecutionProbe() error {
	switch {
	case !windowsProjectionAvailable():
		return fmt.Errorf("%w: WinFsp is not installed", ErrStrictExecutionUnavailable)
	case createRestrictedTokenProc.Find() != nil:
		return fmt.Errorf("%w: CreateRestrictedToken unavailable", ErrStrictExecutionUnavailable)
	default:
		return nil
	}
}

func windowsProjectionAvailable() bool {
	for _, dir := range windowsProjectionDirs() {
		matches, _ := filepath.Glob(filepath.Join(dir, "winfsp-*.dll"))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

func windowsProjectionDirs() []string {
	seen := make(map[string]struct{})
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	add(filepath.Join(os.Getenv("ProgramFiles"), "WinFsp", "bin"))
	add(filepath.Join(os.Getenv("ProgramFiles(x86)"), "WinFsp", "bin"))
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		add(dir)
	}
	return dirs
}

func mountExecutionRoot(_ context.Context, _ BrokerRunRequest, root *projectedRoot) (mountedExecutionRoot, error) {
	mountpoint, err := newWindowsMountpoint()
	if err != nil {
		return nil, err
	}
	return mountCGOFuseExecutionRoot(mountpoint, nil, root)
}

func newWindowsMountpoint() (string, error) {
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		return "", err
	}
	for _, letter := range []byte("ZYXWVUTSRQPONMLKJIHGFED") {
		bit := uint32(1 << (letter - 'A'))
		if drives&bit == 0 {
			return string(letter) + ":", nil
		}
	}
	return "", ErrStrictExecutionUnavailable
}

func runMountedExecution(ctx context.Context, req BrokerRunRequest, mountRoot string, guard *brokerMemoryGuard) (*BrokerRunResult, error) {
	token, err := newWindowsExecutionToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()

	job, err := newWindowsExecutionJob(guard.runLimit)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(job)

	argv, err := hostProjectedExecutionArgv(req.Plan, req.Env, mountRoot, req.Argv)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = translatedWorkingDir(req.Plan, mountRoot)
	cmd.Env = translatedExecutionEnv(req.Plan, req.Env, mountRoot)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:       true,
		Token:            syscall.Token(token),
		NoInheritHandles: true,
	}

	stdout := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	stderr := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := assignWindowsProcessJob(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	err = cmd.Wait()
	exitCode, exitErr := captureExitCode(err)
	if exitErr != nil {
		return nil, exitErr
	}
	return &BrokerRunResult{
		ExitCode:        exitCode,
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}, nil
}

func newWindowsExecutionToken() (windows.Token, error) {
	var current windows.Token
	access := uint32(windows.TOKEN_ASSIGN_PRIMARY |
		windows.TOKEN_DUPLICATE |
		windows.TOKEN_QUERY |
		windows.TOKEN_ADJUST_DEFAULT |
		windows.TOKEN_ADJUST_SESSIONID)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access, &current); err != nil {
		return 0, err
	}
	defer current.Close()

	flags := uint32(windowsDisableMaxPrivilege | windowsLuaToken | windowsWriteRestricted)
	restricted, err := windowsCreateRestrictedToken(current, flags)
	if err != nil {
		return 0, err
	}
	if err := setWindowsLowIntegrity(restricted); err != nil {
		restricted.Close()
		return 0, err
	}
	return restricted, nil
}

func windowsCreateRestrictedToken(token windows.Token, flags uint32) (windows.Token, error) {
	var out windows.Token
	r1, _, callErr := createRestrictedTokenProc.Call(
		uintptr(token),
		uintptr(flags),
		0,
		0,
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 != 0 {
		return out, nil
	}
	if callErr != syscall.Errno(0) {
		return 0, callErr
	}
	return 0, ErrStrictExecutionUnavailable
}

func setWindowsLowIntegrity(token windows.Token) error {
	sid, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		return err
	}
	label := windows.Tokenmandatorylabel{
		Label: windows.SIDAndAttributes{
			Sid:        sid,
			Attributes: windows.SE_GROUP_INTEGRITY,
		},
	}
	return windows.SetTokenInformation(
		token,
		windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&label)),
		label.Size(),
	)
}

func newWindowsExecutionJob(memoryLimit int64) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	info.BasicLimitInformation.ActiveProcessLimit = 1
	if memoryLimit > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = uintptr(memoryLimit)
		info.JobMemoryLimit = uintptr(memoryLimit)
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignWindowsProcessJob(job windows.Handle, pid int) error {
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|
			windows.PROCESS_SET_INFORMATION|
			windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(job, process)
}
