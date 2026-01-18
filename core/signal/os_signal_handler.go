package signal

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/adalundhe/sylk/core/concurrency"
)

type OSSignalHandler struct {
	controller *concurrency.PipelineController

	mu                sync.Mutex
	running           bool
	interruptReceived atomic.Bool
	stopCh            chan struct{}
	sigCh             chan os.Signal
}

func NewOSSignalHandler(controller *concurrency.PipelineController) *OSSignalHandler {
	return &OSSignalHandler{
		controller: controller,
		stopCh:     make(chan struct{}),
		sigCh:      make(chan os.Signal, 1),
	}
}

func (h *OSSignalHandler) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return
	}

	h.running = true
	signal.Notify(h.sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGTSTP)
	go h.listen()
}

func (h *OSSignalHandler) listen() {
	for {
		select {
		case <-h.stopCh:
			return
		case sig := <-h.sigCh:
			h.handleSignal(sig)
		}
	}
}

func (h *OSSignalHandler) handleSignal(sig os.Signal) {
	ctx := context.Background()

	switch sig {
	case syscall.SIGINT:
		h.handleInterrupt(ctx)
	case syscall.SIGTERM:
		h.handleTerminate(ctx)
	case syscall.SIGTSTP:
		h.handleSuspend(ctx)
	}
}

func (h *OSSignalHandler) handleInterrupt(ctx context.Context) {
	if h.interruptReceived.Load() {
		_ = h.controller.Kill(ctx)
		return
	}

	h.interruptReceived.Store(true)
	go h.stopWithReset(ctx)
}

func (h *OSSignalHandler) stopWithReset(ctx context.Context) {
	err := h.controller.Stop(ctx)
	if err == nil {
		h.interruptReceived.Store(false)
	}
}

func (h *OSSignalHandler) handleTerminate(ctx context.Context) {
	_ = h.controller.Kill(ctx)
}

func (h *OSSignalHandler) handleSuspend(ctx context.Context) {
	_ = h.controller.Pause(ctx)
}

func (h *OSSignalHandler) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}

	signal.Stop(h.sigCh)
	close(h.stopCh)
	h.running = false
}

func (h *OSSignalHandler) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

func (h *OSSignalHandler) InterruptReceived() bool {
	return h.interruptReceived.Load()
}
