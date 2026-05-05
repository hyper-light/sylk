package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	// killWatchdogGrace is the delay between the second SIGINT/SIGTERM
	// arriving and os.Exit(130) firing. Sized to allow a tiny tail
	// window for terminal-restoration writes (alt-screen exit, cursor
	// restore) to flush — a typical terminal escape-sequence write
	// completes in single-digit milliseconds; 200ms gives ~25× headroom
	// without making the user wait noticeably for the kill.
	killWatchdogGrace = 200 * time.Millisecond

	// killWatchdogChanBuffer is the SIGINT/SIGTERM channel's buffer.
	// Sized so a user mashing Ctrl+C several times in rapid succession
	// doesn't overflow before the handler can drain the channel. The
	// watchdog only needs the first two; additional buffer guards
	// against mash bursts so subsequent signals don't get dropped at
	// the OS layer (which Go's signal.Notify silently does on full
	// channels).
	killWatchdogChanBuffer = 4
)

// killWatchdog is a SIGINT/SIGTERM watcher that guarantees the user
// can always force-quit by pressing Ctrl+C a second time, even when
// graceful shutdown is wedged in a blocking subsystem close. Operates
// independently of signal.NotifyContext — both subscribers receive
// each signal.
//
// The single listener goroutine is tracked via wg (defer wg.Done in
// run, wg.Wait in stop). On the second signal the listener itself
// performs the grace-period sleep and the os.Exit call — there is
// no spawned helper goroutine, so the watchdog satisfies the
// project's "no untracked goroutines" invariant.
type killWatchdog struct {
	ch   chan os.Signal
	done chan struct{}
	wg   sync.WaitGroup
}

// installKillWatchdog spawns the watchdog and returns a stop function
// the caller invokes once normal shutdown completes. Stop is
// idempotent and synchronizes on the listener's exit so callers can
// rely on no watchdog goroutine surviving the returned call.
//
// First SIGINT/SIGTERM: counted but no action — the
// signal.NotifyContext path receives the same signal and triggers
// cooperative shutdown.
//
// Second signal: schedules os.Exit(130) after killWatchdogGrace.
// The brief delay lets terminal-restoration writes flush so the
// user's terminal isn't left in alt-screen / raw mode.
func installKillWatchdog() func() {
	w := &killWatchdog{
		ch:   make(chan os.Signal, killWatchdogChanBuffer),
		done: make(chan struct{}),
	}
	signal.Notify(w.ch, syscall.SIGINT, syscall.SIGTERM)
	w.wg.Add(1)
	go w.run()
	return w.stop
}

// stop unsubscribes the signal channel and waits for the listener to
// exit. Safe to call multiple times — the done channel close is
// guarded by a select so duplicate calls are no-ops.
func (w *killWatchdog) stop() {
	select {
	case <-w.done:
		// Already closed via prior stop call — nothing to do.
	default:
		close(w.done)
	}
	w.wg.Wait()
	signal.Stop(w.ch)
}

// run is the listener loop. Exits cleanly on done close (normal
// shutdown completed) or after firing os.Exit on second signal
// (force-exit path, never returns from os.Exit).
func (w *killWatchdog) run() {
	defer w.wg.Done()
	count := 0
	for {
		select {
		case <-w.done:
			return
		case sig, ok := <-w.ch:
			if exit := w.handleSignal(sig, ok, &count); exit {
				return
			}
		}
	}
}

// handleSignal updates the press counter and decides whether the
// listener should exit. Returns true on the second press (force-exit
// scheduled and either fired or interrupted by stop) or when the
// channel closed; false on the first press (graceful shutdown
// remains in charge).
func (w *killWatchdog) handleSignal(sig os.Signal, ok bool, count *int) bool {
	if !ok {
		return true
	}
	*count++
	if *count == 1 {
		slog.Info("kill_watchdog: first signal received; cooperative shutdown in progress",
			"signal", sig.String(),
		)
		return false
	}
	slog.Warn("kill_watchdog: second signal received; force exit scheduled",
		"signal", sig.String(),
		"grace", killWatchdogGrace,
	)
	w.waitGraceOrExit()
	return true
}

// waitGraceOrExit sleeps for killWatchdogGrace then calls os.Exit(130),
// or returns early if normal shutdown completed during the grace
// window (done close beats the timer). The 130 exit code is the
// standard SIGINT-terminated convention (128 + signal number).
//
// The sleep happens on the listener goroutine itself — no spawning —
// so the watchdog has exactly one tracked goroutine for its entire
// lifetime.
func (w *killWatchdog) waitGraceOrExit() {
	timer := time.NewTimer(killWatchdogGrace)
	select {
	case <-timer.C:
		os.Exit(130)
	case <-w.done:
		timer.Stop()
	}
}
