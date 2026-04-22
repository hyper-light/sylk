package orchestrator

import (
	"log/slog"
	"sync/atomic"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/buslog"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

// installedBusLogger retains the per-session BusLogger so the
// orchestrator's Stop path can drain it. Swap semantics mirror
// installedFabricLogger.
var installedBusLogger atomic.Pointer[buslog.BusLogger]

// installBusObservability opens a session-global bus log and
// installs a BusEventHook on the ChannelBus so every publish,
// delivery, subscribe, unsubscribe, overflow, and expired event is
// captured to disk. Idempotent — safe to call across session
// switches; a later call drains the prior logger before replacing.
//
// No-op when any required argument is nil so tests and minimal
// bootstraps skip observability without errors.
func installBusObservability(bus guide.EventBus, sd *sylkdir.SylkDir, sessionID string) {
	if bus == nil || sd == nil || sessionID == "" {
		return
	}
	channelBus, ok := bus.(*guide.ChannelBus)
	if !ok {
		// Only ChannelBus implements SetEventHook. Non-concrete
		// buses (test fakes) are fine to skip.
		slog.Debug("bus observability: skipped — bus is not *guide.ChannelBus", "session_id", sessionID)
		return
	}
	logger, err := buslog.New(buslog.Config{
		BaseDir:   sd.SessionBusPath(sessionID),
		SessionID: sessionID,
		Redactor:  agentlog.NewRedactor(agentlog.RedactorConfig{}, nil),
	})
	if err != nil {
		slog.Warn("bus observability: failed to construct bus logger",
			"session_id", sessionID, "error", err)
		return
	}

	// Drain the previous logger (if any) before replacing so the
	// prior session's buffered records land on disk.
	if prev := installedBusLogger.Swap(logger); prev != nil {
		if err := prev.Close(); err != nil {
			slog.Warn("bus observability: close previous logger",
				"session_id", sessionID, "error", err)
		}
	}
	buslog.SetDefault(logger)

	hook := buslog.NewBusEventHook(logger, nil)
	channelBus.SetEventHook(hook)

	slog.Info("bus observability: installed",
		"session_id", sessionID,
		"path", sd.SessionBusPath(sessionID),
	)
}

// uninstallBusObservability drains the current bus logger and
// clears the ChannelBus hook. Called from the orchestrator's Stop
// path so buffered records are flushed before exit.
func uninstallBusObservability(bus guide.EventBus) {
	if channelBus, ok := bus.(*guide.ChannelBus); ok {
		channelBus.SetEventHook(nil)
	}
	if prev := installedBusLogger.Swap(nil); prev != nil {
		buslog.SetDefault(nil)
		if err := prev.Close(); err != nil {
			slog.Warn("bus observability: close on shutdown", "error", err)
		}
	}
}
