package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	lspBridgeName = "bridge.lsp"
	// Zero uses the scope's max lifetime; LSP bridge is long-lived for the UI session.
	lspDrainTimeout = 0
)

// LSPBridge drains LSP diagnostic notifications from the Manager and
// forwards them as msg.LSPDiagnosticMsg to the Bubble Tea program.
type LSPBridge struct {
	manager  *lsp.Manager
	scope    *concurrency.GoroutineScope
	dropped  atomic.Int64
	done     chan struct{}
	stopOnce sync.Once
}

// NewLSPBridge creates a bridge that converts LSP diagnostics into
// Bubble Tea messages.
func NewLSPBridge(manager *lsp.Manager, scope *concurrency.GoroutineScope) *LSPBridge {
	return &LSPBridge{
		manager: manager,
		scope:   scope,
		done:    make(chan struct{}),
	}
}

// -- Bridge implementation --

// Start launches the drain goroutine that reads from the manager's
// aggregated diagnostics channel.
func (b *LSPBridge) Start(program TeaProgram) error {
	return b.scope.Go(lspBridgeName, lspDrainTimeout, b.drainFunc(program))
}

// Stop signals the drain goroutine to exit.
func (b *LSPBridge) Stop() {
	b.stopOnce.Do(func() {
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *LSPBridge) Name() string { return lspBridgeName }

// DroppedCount returns the total number of diagnostics dropped.
func (b *LSPBridge) DroppedCount() int64 { return b.dropped.Load() }

// drainFunc returns the WorkFunc that reads diagnostic results and
// sends them to the Bubble Tea program.
func (b *LSPBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		ch := b.manager.Diagnostics()
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case result, ok := <-ch:
				if !ok {
					return nil
				}
				program.Send(msg.LSPDiagnosticMsg{
					ServerID:    string(result.ServerID),
					FilePath:    result.FilePath,
					Diagnostics: result.Diagnostics,
				})
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
