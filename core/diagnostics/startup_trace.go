package diagnostics

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/logging"
)

const startupTraceDefaultPath = "/tmp/sylk-startup-debug.log"

var (
	startupTraceOnce   sync.Once
	startupTraceLogger *slog.Logger
)

// StartupTracePath returns the file used for temporary startup diagnostics.
// Set SYLK_STARTUP_DEBUG_LOG to redirect the trace for a single run.
func StartupTracePath() string {
	if path := strings.TrimSpace(os.Getenv("SYLK_STARTUP_DEBUG_LOG")); path != "" {
		return path
	}
	return startupTraceDefaultPath
}

// StartupTrace returns a process-wide file logger for boot/UI diagnostics.
// The file is truncated on first use so one failing startup produces one
// readable timeline without carrying stale events from earlier runs.
func StartupTrace() *slog.Logger {
	startupTraceOnce.Do(func() {
		path := StartupTracePath()
		if dir := filepath.Dir(path); dir != "." {
			_ = os.MkdirAll(dir, 0o700)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			startupTraceLogger = slog.New(slog.NewTextHandler(io.Discard, logging.HandlerOptions(slog.LevelDebug)))
			return
		}
		startupTraceLogger = slog.New(slog.NewTextHandler(f, logging.HandlerOptions(slog.LevelDebug)))
		startupTraceLogger.Info("startup_trace_opened", "path", path, "pid", os.Getpid())
	})
	return startupTraceLogger
}

// LogStartup writes a structured startup diagnostic event.
func LogStartup(event string, fields ...any) {
	StartupTrace().Info("STARTUP_TRACE: "+event, fields...)
}
