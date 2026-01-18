//go:build !windows
// +build !windows

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/security"
)

type ProcessState int32

const (
	ProcessStateCreated ProcessState = iota
	ProcessStateRunning
	ProcessStateTerminating
	ProcessStateTerminated
)

const (
	sigintWait  = 100 * time.Millisecond
	sigtermWait = 500 * time.Millisecond
	sigkillWait = 100 * time.Millisecond
)

var (
	ErrProcessAlreadyStarted = errors.New("process already started")
	ErrProcessNotStarted     = errors.New("process not started")
	ErrNoProcessGroup        = errors.New("no process group")
	ErrTerminationTimeout    = errors.New("termination timeout")
)

type TrackedProcess struct {
	cmd       *exec.Cmd
	pid       int
	pgid      int
	startedAt time.Time
	sandbox   *security.Sandbox

	mu      sync.Mutex
	state   atomic.Int32
	doneCh  chan struct{}
	exitErr error
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func NewTrackedProcess(
	ctx context.Context,
	command string,
	args []string,
	sandbox *security.Sandbox,
) (*TrackedProcess, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if sandbox != nil {
		wrappedCmd, err := sandbox.Execute(ctx, cmd)
		if err != nil {
			return nil, fmt.Errorf("sandbox configure: %w", err)
		}
		cmd = wrappedCmd
	}

	proc := &TrackedProcess{
		cmd:     cmd,
		sandbox: sandbox,
		doneCh:  make(chan struct{}),
	}
	proc.state.Store(int32(ProcessStateCreated))
	proc.cmd.Stdout = &proc.stdout
	proc.cmd.Stderr = &proc.stderr

	return proc, nil
}

func (p *TrackedProcess) Run() ([]byte, error) {
	if err := p.start(); err != nil {
		return nil, err
	}
	return p.waitForCompletion()
}

func (p *TrackedProcess) start() error {
	if !p.state.CompareAndSwap(int32(ProcessStateCreated), int32(ProcessStateRunning)) {
		return ErrProcessAlreadyStarted
	}

	p.mu.Lock()
	p.startedAt = time.Now()
	p.mu.Unlock()

	if err := p.cmd.Start(); err != nil {
		p.state.Store(int32(ProcessStateTerminated))
		return err
	}

	p.recordProcessIDs()
	go p.waitInBackground()

	return nil
}

func (p *TrackedProcess) recordProcessIDs() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.pid = p.cmd.Process.Pid
	pgid, err := syscall.Getpgid(p.pid)
	if err == nil {
		p.pgid = pgid
	} else {
		p.pgid = p.pid
	}
}

func (p *TrackedProcess) waitInBackground() {
	p.exitErr = p.cmd.Wait()
	p.state.Store(int32(ProcessStateTerminated))
	close(p.doneCh)
}

func (p *TrackedProcess) waitForCompletion() ([]byte, error) {
	<-p.doneCh
	if p.exitErr != nil {
		return nil, p.exitErr
	}
	return p.stdout.Bytes(), nil
}

func (p *TrackedProcess) Type() concurrency.ResourceType {
	return concurrency.ResourceTypeProcess
}

func (p *TrackedProcess) ID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("proc-%d", p.pid)
}

func (p *TrackedProcess) IsClosed() bool {
	return ProcessState(p.state.Load()) == ProcessStateTerminated
}

func (p *TrackedProcess) ForceClose() error {
	if !p.transitionToTerminating() {
		return nil
	}
	return p.executeTermination()
}

func (p *TrackedProcess) transitionToTerminating() bool {
	for {
		current := ProcessState(p.state.Load())
		if p.isAlreadyTerminatingOrTerminated(current) {
			return false
		}
		if p.state.CompareAndSwap(int32(current), int32(ProcessStateTerminating)) {
			return true
		}
	}
}

func (p *TrackedProcess) isAlreadyTerminatingOrTerminated(state ProcessState) bool {
	return state == ProcessStateTerminated || state == ProcessStateTerminating
}

func (p *TrackedProcess) executeTermination() error {
	if p.trySandboxKill() {
		return nil
	}
	return p.signalTerminationSequence()
}

func (p *TrackedProcess) trySandboxKill() bool {
	if p.sandbox == nil {
		return false
	}
	status := p.sandbox.Status()
	if !status.Enabled || !status.OSActive {
		return false
	}
	return p.waitTermination(sigkillWait) == nil
}

func (p *TrackedProcess) signalTerminationSequence() error {
	if p.trySigint() {
		return nil
	}
	if p.trySigterm() {
		return nil
	}
	return p.sendSigkill()
}

func (p *TrackedProcess) trySigint() bool {
	if err := p.signalGroup(syscall.SIGINT); err != nil {
		return true
	}
	return p.waitTermination(sigintWait) == nil
}

func (p *TrackedProcess) trySigterm() bool {
	if err := p.signalGroup(syscall.SIGTERM); err != nil {
		return true
	}
	return p.waitTermination(sigtermWait) == nil
}

func (p *TrackedProcess) sendSigkill() error {
	if err := p.signalGroup(syscall.SIGKILL); err != nil {
		return err
	}
	return p.waitTermination(sigkillWait)
}

func (p *TrackedProcess) signalGroup(sig syscall.Signal) error {
	p.mu.Lock()
	pgid := p.pgid
	p.mu.Unlock()

	if pgid == 0 {
		return ErrNoProcessGroup
	}
	return syscall.Kill(-pgid, sig)
}

func (p *TrackedProcess) waitTermination(timeout time.Duration) error {
	select {
	case <-p.doneCh:
		return nil
	case <-time.After(timeout):
		return ErrTerminationTimeout
	}
}

func (p *TrackedProcess) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func (p *TrackedProcess) Pgid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pgid
}

func (p *TrackedProcess) State() ProcessState {
	return ProcessState(p.state.Load())
}

func (p *TrackedProcess) StartedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedAt
}

func (p *TrackedProcess) Done() <-chan struct{} {
	return p.doneCh
}

func (p *TrackedProcess) Stderr() []byte {
	return p.stderr.Bytes()
}
