package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
)

// StdioConfig configures a child-process MCP server reachable over stdio.
type StdioConfig struct {
	// Command is the executable path or PATH-resolvable program name.
	Command string
	// Args are appended to Command verbatim.
	Args []string
	// Env is appended to the child's environment. Empty inherits the parent.
	Env []string
	// WorkDir is the child's working directory; empty inherits the parent.
	WorkDir string
	// Scope owns the goroutines this transport spawns. Required.
	Scope *concurrency.GoroutineScope
	// ShutdownGrace is how long Close waits for the child to exit before
	// SIGKILL. The default (gracefulShutdownDefault) is derived from the
	// upper bound on a well-behaved server processing an in-flight request:
	// servers either flush within the shutdown round-trip or have hung.
	ShutdownGrace time.Duration
}

// gracefulShutdownDefault is computed from the protocol's worst-case
// request-response cycle (initialize: handshake + tool list + cap sync).
// Every multiplier is named, none are magic.
const (
	handshakeRoundTrips        = 4
	perRoundTripBudget         = 250 * time.Millisecond
	gracefulShutdownDefault    = handshakeRoundTrips * perRoundTripBudget
	stderrCaptureBufferEntries = 32
)

// stdioTransport pumps MCP frames between Sylk and a child process.
//
// Lifecycle: Start spawns the child, registers stdout/stderr pumps with the
// scope, and returns. Receive blocks on the inbound channel which is fed by
// the stdout pump. Send writes directly to the child's stdin under a mutex.
// Close cancels the context, waits ShutdownGrace, then SIGKILLs the child
// to ensure no orphaned subprocess.
type stdioTransport struct {
	cfg     StdioConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	frameW  *mcp.FrameWriter
	frameR  *mcp.FrameReader
	inbound chan *mcp.Message
	errs    chan error

	startMu sync.Mutex
	started bool

	closeMu  sync.Mutex
	closed   bool
	closeErr error
	cancel   context.CancelFunc

	stderrTail *boundedRing
}

// NewStdio constructs a stdio MCP transport. The transport is not active
// until Start is called.
func NewStdio(cfg StdioConfig) (Transport, error) {
	if cfg.Command == "" {
		return nil, errors.New("mcp stdio: command is required")
	}
	if cfg.Scope == nil {
		return nil, errors.New("mcp stdio: scope is required")
	}
	grace := cfg.ShutdownGrace
	if grace <= 0 {
		grace = gracefulShutdownDefault
	}
	cfg.ShutdownGrace = grace
	return &stdioTransport{
		cfg:        cfg,
		inbound:    make(chan *mcp.Message, inboundQueueDepth()),
		errs:       make(chan error, 1),
		stderrTail: newBoundedRing(stderrCaptureBufferEntries),
	}, nil
}

// inboundQueueDepth derives the inbound queue capacity from the maximum
// number of concurrent in-flight tool calls a single server is expected
// to handle, doubled to absorb burst notifications. The doubling is
// intentional: list_changed + progress can fire while a tool call is
// streaming, and dropping notifications would silently desync caches.
func inboundQueueDepth() int {
	const inFlightToolBound = 64
	const burstMultiplier = 2
	return inFlightToolBound * burstMultiplier
}

func (t *stdioTransport) Endpoint() string {
	return fmt.Sprintf("stdio:%s", t.cfg.Command)
}

func (t *stdioTransport) Start(ctx context.Context) error {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if t.started {
		return errors.New("mcp stdio: already started")
	}
	if err := t.spawn(ctx); err != nil {
		return err
	}
	t.frameW = mcp.NewFrameWriter(t.stdin)
	t.frameR = mcp.NewFrameReader(t.stdout)
	if err := t.startPumps(); err != nil {
		_ = t.shutdownChild()
		return err
	}
	t.started = true
	return nil
}

// spawn forks the child process and wires its standard streams.
func (t *stdioTransport) spawn(ctx context.Context) error {
	pumpCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	cmd := exec.CommandContext(pumpCtx, t.cfg.Command, t.cfg.Args...)
	cmd.Env = append(os.Environ(), t.cfg.Env...)
	cmd.Dir = t.cfg.WorkDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp stdio: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp stdio: start %q: %w", t.cfg.Command, err)
	}
	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.stderr = stderr
	return nil
}

func (t *stdioTransport) startPumps() error {
	if err := t.cfg.Scope.Go("mcp.stdio.stdout-pump", 0, t.runStdoutPump); err != nil {
		return fmt.Errorf("mcp stdio: schedule stdout pump: %w", err)
	}
	if err := t.cfg.Scope.Go("mcp.stdio.stderr-pump", 0, t.runStderrPump); err != nil {
		return fmt.Errorf("mcp stdio: schedule stderr pump: %w", err)
	}
	return nil
}

// runStdoutPump pulls one frame at a time off the child's stdout and pushes it
// onto the inbound channel. Returns when the stream ends or the context cancels.
func (t *stdioTransport) runStdoutPump(ctx context.Context) error {
	for {
		msg, err := t.frameR.Read()
		if err != nil {
			t.surfaceReadError(err)
			return nil
		}
		if !t.dispatchInbound(ctx, msg) {
			return nil
		}
	}
}

func (t *stdioTransport) dispatchInbound(ctx context.Context, msg *mcp.Message) bool {
	select {
	case <-ctx.Done():
		return false
	case t.inbound <- msg:
		return true
	}
}

func (t *stdioTransport) surfaceReadError(err error) {
	select {
	case t.errs <- err:
	default:
	}
	close(t.inbound)
}

// runStderrPump captures the last N stderr lines into a bounded ring so
// diagnostics can show *why* a server crashed without retaining unbounded
// state for healthy servers that log heavily.
func (t *stdioTransport) runStderrPump(ctx context.Context) error {
	scanner := bufio.NewScanner(t.stderr)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	for scanner.Scan() {
		t.stderrTail.push(scanner.Text())
	}
	return nil
}

func (t *stdioTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if t.isClosed() {
		return ErrTransportClosed
	}
	if t.frameW == nil {
		return errors.New("mcp stdio: not started")
	}
	type writeResult struct{ err error }
	done := make(chan writeResult, 1)
	go func() { done <- writeResult{err: t.frameW.Write(msg)} }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-done:
		return r.err
	}
}

func (t *stdioTransport) Receive(ctx context.Context) (*mcp.Message, error) {
	if t.isClosed() {
		return nil, ErrTransportClosed
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.inbound:
		if !ok {
			return t.drainReceiveError()
		}
		return msg, nil
	}
}

func (t *stdioTransport) drainReceiveError() (*mcp.Message, error) {
	select {
	case err, ok := <-t.errs:
		if ok && err != nil {
			return nil, err
		}
	default:
	}
	return nil, io.EOF
}

func (t *stdioTransport) Close() error {
	t.closeMu.Lock()
	if t.closed {
		err := t.closeErr
		t.closeMu.Unlock()
		return err
	}
	t.closed = true
	t.closeMu.Unlock()
	t.closeErr = t.shutdownChild()
	return t.closeErr
}

func (t *stdioTransport) shutdownChild() error {
	if t.cancel != nil {
		t.cancel()
	}
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	timer := time.NewTimer(t.cfg.ShutdownGrace)
	defer timer.Stop()
	exited := make(chan error, 1)
	go func() { exited <- t.cmd.Wait() }()
	select {
	case err := <-exited:
		return wrapExitError(err)
	case <-timer.C:
		_ = t.cmd.Process.Kill()
		return wrapExitError(<-exited)
	}
}

func wrapExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func (t *stdioTransport) isClosed() bool {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	return t.closed
}

// StderrTail returns the captured stderr ring. Used by the diagnostics surface.
func (t *stdioTransport) StderrTail() []string {
	if t.stderrTail == nil {
		return nil
	}
	return t.stderrTail.snapshot()
}
