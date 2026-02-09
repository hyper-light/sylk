package bridge

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	lspBridgeName   = "bridge.lsp"
	lspDrainTimeout = 5 * time.Minute
)

// LSPBridge drains LSP diagnostic notifications from the Manager and
// forwards them as msg.LSPDiagnosticMsg to the Bubble Tea program.
type LSPBridge struct {
	manager *lsp.Manager
	scope   *concurrency.GoroutineScope
	dropped atomic.Int64
	done    chan struct{}
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
	close(b.done)
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
