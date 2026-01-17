//go:build windows
// +build windows

package tools

import (
	"os/exec"
	"sync"
	"syscall"
)

type ProcessGroup struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	killed bool
}

func NewProcessGroup() *ProcessGroup {
	return &ProcessGroup{}
}

func (pg *ProcessGroup) Setup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	pg.cmd = cmd
}

func (pg *ProcessGroup) Start() error {
	return pg.cmd.Start()
}

func (pg *ProcessGroup) Signal(sig syscall.Signal) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if pg.killed || pg.cmd == nil || pg.cmd.Process == nil {
		return nil
	}

	return pg.cmd.Process.Signal(sig)
}

func (pg *ProcessGroup) Kill() error {
	pg.mu.Lock()
	pg.killed = true
	cmd := pg.cmd
	pg.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	return cmd.Process.Kill()
}

func (pg *ProcessGroup) Wait() error {
	if pg.cmd == nil || pg.cmd.Process == nil {
		return nil
	}
	return pg.cmd.Wait()
}

func (pg *ProcessGroup) Pid() int {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if pg.cmd == nil || pg.cmd.Process == nil {
		return 0
	}
	return pg.cmd.Process.Pid
}

func (pg *ProcessGroup) IsKilled() bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.killed
}
