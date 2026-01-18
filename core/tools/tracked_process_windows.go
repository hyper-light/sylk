//go:build windows
// +build windows

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
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
	killWait = 100 * time.Millisecond
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
		if current == ProcessStateTerminated || current == ProcessStateTerminating {
			return false
		}
		if p.state.CompareAndSwap(int32(current), int32(ProcessStateTerminating)) {
			return true
		}
	}
}

func (p *TrackedProcess) executeTermination() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil {
		return err
	}
	return p.waitTermination(killWait)
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
	return 0
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
