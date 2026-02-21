package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	gitBridgeName = "bridge.git"
	gitBufferSize = 64
	// Zero uses the scope's max lifetime; git bridge is long-lived for the UI session.
	gitDrainTimeout = 0
)

// GitBridge subscribes to mutation events on a GitBus and forwards them
// as msg.GitOpEventMsg to the Bubble Tea program.  Only mutating
// operations are forwarded — reads generate no UI messages.
type GitBridge struct {
	bus      *git.GitBus
	scope    *concurrency.GoroutineScope
	buffer   chan *git.GitEvent
	dropped  atomic.Int64
	unsub    func()
	done     chan struct{}
	stopOnce sync.Once
}

// NewGitBridge creates a bridge.  Call Start to begin forwarding events.
func NewGitBridge(bus *git.GitBus, scope *concurrency.GoroutineScope) *GitBridge {
	return &GitBridge{
		bus:    bus,
		scope:  scope,
		buffer: make(chan *git.GitEvent, gitBufferSize),
		done:   make(chan struct{}),
	}
}

// Start subscribes to post-mutation events and launches the drain goroutine.
func (b *GitBridge) Start(program TeaProgram) error {
	b.unsub = b.bus.Subscribe(nil, b.onEvent) // wildcard; filter in handler
	return b.scope.Go(gitBridgeName, gitDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the bus and signals the drain goroutine to exit.
func (b *GitBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.unsub != nil {
			b.unsub()
		}
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *GitBridge) Name() string { return gitBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *GitBridge) DroppedCount() int64 { return b.dropped.Load() }

// onEvent is the GitBus callback.  It filters to post-mutation events and
// enqueues them into the bounded buffer, dropping on backpressure.
func (b *GitBridge) onEvent(event *git.GitEvent) {
	if event.Phase != git.PhasePost || !event.IsMutation() {
		return
	}
	select {
	case b.buffer <- event:
	default:
		b.dropped.Add(1)
	}
}

// drainFunc returns the WorkFunc that drains the buffer and sends tea messages.
func (b *GitBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case event := <-b.buffer:
				program.Send(msg.GitOpEventMsg{
					Op:       event.Op.String(),
					Err:      event.Err,
					Duration: event.Duration,
					Params:   event.Params,
				})
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
