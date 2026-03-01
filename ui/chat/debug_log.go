package chat

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var (
	chatDebugLogger     *slog.Logger
	chatDebugLoggerOnce sync.Once
)

func chatDebugLog() *slog.Logger {
	chatDebugLoggerOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			chatDebugLogger = slog.Default()
			return
		}
		chatDebugLogger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	return chatDebugLogger
}
