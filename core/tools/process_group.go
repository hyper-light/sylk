//go:build !windows
// +build !windows

package tools

import (
	"os/exec"
	"sync"
	"syscall"
)

type ProcessGroup struct {
	pgid   int
	cmd    *exec.Cmd
	mu     sync.Mutex
	killed bool
}

func NewProcessGroup() *ProcessGroup {
	return &ProcessGroup{}
}

func (pg *ProcessGroup) Setup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	pg.cmd = cmd
}

func (pg *ProcessGroup) Start() error {
	if err := pg.cmd.Start(); err != nil {
		return err
	}

	pg.mu.Lock()
	pg.pgid = pg.cmd.Process.Pid
	pg.mu.Unlock()

	return nil
}

func (pg *ProcessGroup) Signal(sig syscall.Signal) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if pg.killed || pg.pgid == 0 {
		return nil
	}

	return syscall.Kill(-pg.pgid, sig)
}

func (pg *ProcessGroup) Kill() error {
	pg.mu.Lock()
	pg.killed = true
	pgid := pg.pgid
	pg.mu.Unlock()

	if pgid == 0 {
		return nil
	}

	return syscall.Kill(-pgid, syscall.SIGKILL)
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
	return pg.pgid
}

func (pg *ProcessGroup) IsKilled() bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.killed
}
