package cmd

import (
	"log/slog"
	"os"
	"os/signal"
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

// installKillWatchdog spawns a parallel signal watcher that
// guarantees the user can always force-quit by pressing Ctrl+C a
// second time, even when graceful shutdown is wedged in a blocking
// subsystem close. Operates independently of signal.NotifyContext —
// both subscribers receive each signal.
//
// Behavior:
//   - First SIGINT/SIGTERM: counted but no action. The
//     signal.NotifyContext path receives the same signal and triggers
//     cooperative shutdown.
//   - Second signal: schedules os.Exit(130) after killWatchdogGrace.
//     The brief delay lets terminal-restoration writes flush so the
//     user's terminal isn't left in alt-screen / raw mode.
//
// Returns a stop function the caller invokes once normal shutdown
// completes; stop() unsubscribes the watchdog cleanly so it never
// fires after a successful exit.
//
// The watchdog's goroutines are intentionally untracked: they exist
// precisely to recover from situations where the normal goroutine-
// tracking infrastructure (GoroutineScope) is itself blocked in
// shutdown. Tracking the kill switch through the system it's meant
// to escape would defeat its purpose.
func installKillWatchdog() func() {
	ch := make(chan os.Signal, killWatchdogChanBuffer)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go runKillWatchdog(ch, done, stopped)
	return func() {
		select {
		case <-done:
			// Already stopped — idempotent.
		default:
			close(done)
		}
		<-stopped
		signal.Stop(ch)
	}
}

// runKillWatchdog drains the signal channel and counts presses until
// either (a) the second signal arrives — triggering force-exit — or
// (b) the done channel closes — indicating normal shutdown completed.
// Closes stopped before returning so the installer's stop function
// can synchronize on watchdog teardown.
func runKillWatchdog(ch <-chan os.Signal, done <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	count := 0
	for {
		select {
		case <-done:
			return
		case sig, ok := <-ch:
			if shouldStop := handleKillSignal(sig, ok, &count); shouldStop {
				return
			}
		}
	}
}

// handleKillSignal updates the press counter and decides whether the
// watchdog should exit. Returns true on the second press (force-exit
// scheduled) or when the channel closed; false on the first press
// (graceful shutdown remains in charge).
func handleKillSignal(sig os.Signal, ok bool, count *int) bool {
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
	go forceExitAfter(killWatchdogGrace)
	return true
}

// forceExitAfter sleeps for grace then calls os.Exit(130). The
// 130 exit code is the standard SIGINT-terminated convention
// (128 + signal number). Run on a dedicated goroutine so the
// watchdog's main loop returns first and the caller's stop()
// synchronizes cleanly.
func forceExitAfter(grace time.Duration) {
	time.Sleep(grace)
	os.Exit(130)
}
