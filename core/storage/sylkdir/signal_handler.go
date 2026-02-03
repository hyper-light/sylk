package sylkdir

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Syncable is the interface for WAL types that support fsync.
type Syncable interface {
	Sync() error
}

// SignalHandler fsyncs all registered WALs on SIGINT/SIGTERM.
// No compaction runs under signal pressure — only fsync for durability.
type SignalHandler struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wals   []Syncable
	done   chan struct{}
}

// NewSignalHandler creates a new signal handler.
func NewSignalHandler() *SignalHandler {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return &SignalHandler{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// Register adds a WAL to the set that will be fsynced on signal.
func (sh *SignalHandler) Register(w Syncable) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.wals = append(sh.wals, w)
}

// Start launches the signal-watching goroutine. The goroutine is tracked
// via the done channel and exits when the context is cancelled or a signal
// is received.
func (sh *SignalHandler) Start(ctx context.Context) {
	go sh.run(ctx)
}

// run is the signal-watching goroutine.
func (sh *SignalHandler) run(parentCtx context.Context) {
	defer close(sh.done)

	select {
	case <-sh.ctx.Done():
		sh.syncAll()
	case <-parentCtx.Done():
		// Parent cancelled — clean shutdown without signal.
	}
}

// syncAll fsyncs all registered WALs. Errors are silently ignored
// since we're in a signal handler context.
func (sh *SignalHandler) syncAll() {
	sh.mu.Lock()
	defer sh.mu.Unlock()

	for _, w := range sh.wals {
		_ = w.Sync()
	}
}

// Context returns the signal handler's context that is cancelled on signal.
func (sh *SignalHandler) Context() context.Context {
	return sh.ctx
}

// Wait blocks until the signal handler goroutine exits.
func (sh *SignalHandler) Wait() {
	<-sh.done
}

// Stop cancels the signal handler and waits for the goroutine to exit.
func (sh *SignalHandler) Stop() {
	sh.cancel()
	<-sh.done
}
