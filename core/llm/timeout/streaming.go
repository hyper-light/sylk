// Package timeout provides timeout management for LLM streaming responses.
package timeout

import (
	"context"
	"sync"
	"time"
)

// StreamingTimeoutMonitor monitors streaming response timeouts.
type StreamingTimeoutMonitor struct {
	config StreamingTimeoutConfig

	started    time.Time
	firstToken time.Time
	lastToken  time.Time

	totalCtx    context.Context
	totalCancel context.CancelFunc

	tokenCh chan struct{} // Signal when token received
	errorCh chan error    // Signal timeout errors

	mu sync.Mutex
}

// NewStreamingTimeoutMonitor creates monitor with TotalTimeout context.
func NewStreamingTimeoutMonitor(ctx context.Context, config StreamingTimeoutConfig) *StreamingTimeoutMonitor {
	totalCtx, totalCancel := context.WithTimeout(ctx, config.TotalTimeout)

	stm := &StreamingTimeoutMonitor{
		config:      config,
		started:     time.Now(),
		totalCtx:    totalCtx,
		totalCancel: totalCancel,
		tokenCh:     make(chan struct{}, 1),
		errorCh:     make(chan error, 1),
	}

	go stm.monitorTimeouts()

	return stm
}

// monitorTimeouts runs the timeout detection logic.
func (stm *StreamingTimeoutMonitor) monitorTimeouts() {
	if !stm.waitForFirstToken() {
		return
	}
	stm.monitorInterTokenGaps()
}

// waitForFirstToken waits for the first token or times out.
// Returns true if first token was received, false on timeout/cancellation.
func (stm *StreamingTimeoutMonitor) waitForFirstToken() bool {
	timer := time.NewTimer(stm.config.FirstTokenTimeout)
	defer timer.Stop()

	select {
	case <-stm.tokenCh:
		return true
	case <-timer.C:
		stm.sendError(ErrFirstTokenTimeout)
		return false
	case <-stm.totalCtx.Done():
		return false
	}
}

// monitorInterTokenGaps monitors gaps between tokens after first token received.
func (stm *StreamingTimeoutMonitor) monitorInterTokenGaps() {
	timer := time.NewTimer(stm.config.InterTokenTimeout)
	defer timer.Stop()

	for {
		if !stm.waitForTokenOrTimeout(timer) {
			return
		}
		resetTimer(timer, stm.config.InterTokenTimeout)
	}
}

// waitForTokenOrTimeout waits for a token, timeout, or cancellation.
// Returns true if a token was received, false if monitoring should stop.
func (stm *StreamingTimeoutMonitor) waitForTokenOrTimeout(timer *time.Timer) bool {
	select {
	case <-stm.tokenCh:
		return true
	case <-timer.C:
		stm.sendError(ErrInterTokenTimeout)
		return false
	case <-stm.totalCtx.Done():
		return false
	}
}

// resetTimer safely resets the timer, draining it if necessary.
func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

// sendError sends an error to the error channel (non-blocking).
func (stm *StreamingTimeoutMonitor) sendError(err error) {
	select {
	case stm.errorCh <- err:
	default:
	}
}

// RecordToken signals that a token was received (non-blocking send).
func (stm *StreamingTimeoutMonitor) RecordToken() {
	now := time.Now()

	stm.mu.Lock()
	if stm.firstToken.IsZero() {
		stm.firstToken = now
	}
	stm.lastToken = now
	stm.mu.Unlock()

	select {
	case stm.tokenCh <- struct{}{}:
	default:
	}
}

// Done signals the stream completed successfully.
func (stm *StreamingTimeoutMonitor) Done() {
	stm.totalCancel()
}

// Errors returns the error channel.
func (stm *StreamingTimeoutMonitor) Errors() <-chan error {
	return stm.errorCh
}

// Context returns the total timeout context.
func (stm *StreamingTimeoutMonitor) Context() context.Context {
	return stm.totalCtx
}

// Stats returns timing statistics.
func (stm *StreamingTimeoutMonitor) Stats() StreamingStats {
	stm.mu.Lock()
	defer stm.mu.Unlock()

	stats := StreamingStats{
		Started:      stm.started,
		FirstTokenAt: stm.firstToken,
		LastTokenAt:  stm.lastToken,
	}

	if !stm.firstToken.IsZero() {
		stats.TimeToFirstToken = stm.firstToken.Sub(stm.started)
	}

	if !stm.lastToken.IsZero() {
		stats.TotalDuration = stm.lastToken.Sub(stm.started)
	}

	return stats
}
