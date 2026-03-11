package logging

import (
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/config"
	"github.com/adalundhe/sylk/core/storage"
)

const LevelTrace = slog.Level(-8)

var (
	levelOnce       sync.Once
	overrideLevel   slog.Level
	overrideLevelOK bool
)

// HandlerOptions returns shared handler options for process/file loggers.
// When logging.level or SYLK_LOG_LEVEL is set, that override applies globally.
// Otherwise, the provided default level is preserved.
func HandlerOptions(defaultLevel slog.Level) *slog.HandlerOptions {
	if level, ok := configuredLevel(); ok {
		defaultLevel = level
	}
	return &slog.HandlerOptions{Level: defaultLevel}
}

// ParseLevel parses the user-facing logging level string.
func ParseLevel(raw string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "trace":
		return LevelTrace, true
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

func configuredLevel() (slog.Level, bool) {
	levelOnce.Do(loadConfiguredLevel)
	return overrideLevel, overrideLevelOK
}

func loadConfiguredLevel() {
	dirs, err := storage.ResolveDirs()
	if err == nil && dirs != nil {
		manager := config.NewManager(dirs)
		if loadErr := manager.Load(); loadErr == nil {
			if level, ok := ParseLevel(manager.Get().Logging.Level); ok {
				overrideLevel = level
				overrideLevelOK = true
				return
			}
		}
	}

	if level, ok := ParseLevel(os.Getenv("SYLK_LOG_LEVEL")); ok {
		overrideLevel = level
		overrideLevelOK = true
	}
}
