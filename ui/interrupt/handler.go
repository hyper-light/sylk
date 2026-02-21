package interrupt

import (
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/msg"
)

// defaultThreshold is the maximum duration between two Ctrl+C presses
// for the second press to be treated as a quit confirmation.
// Derived from typical human double-press timing studies (~300-700ms).
const defaultThreshold = 500 * time.Millisecond

// Handler implements two-stage Ctrl+C logic.
// First press: emit InterruptMsg (cancel the current operation or clear input).
// Second press within threshold: emit QuitConfirmMsg (confirm quit).
type Handler struct {
	lastInterrupt atomic.Value // stores time.Time
	threshold     time.Duration
}

// NewHandler creates a Handler with the default 500ms threshold.
func NewHandler() *Handler {
	return NewHandlerWithThreshold(defaultThreshold)
}

// NewHandlerWithThreshold creates a handler with a custom double-press window.
// If threshold <= 0, the default threshold is used.
func NewHandlerWithThreshold(threshold time.Duration) *Handler {
	if threshold <= 0 {
		threshold = defaultThreshold
	}
	h := &Handler{
		threshold: threshold,
	}
	// Store the zero time so the first Load always returns a valid time.Time.
	h.lastInterrupt.Store(time.Time{})
	return h
}

// HandleCtrlC returns the appropriate tea.Msg based on the timing
// of consecutive Ctrl+C presses.
func (h *Handler) HandleCtrlC() tea.Msg {
	now := time.Now()
	prev := h.lastInterrupt.Swap(now).(time.Time)

	if !prev.IsZero() && now.Sub(prev) <= h.threshold {
		return msg.QuitConfirmMsg{}
	}

	return msg.InterruptMsg{}
}
