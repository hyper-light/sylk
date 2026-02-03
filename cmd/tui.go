package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui"
	"github.com/spf13/cobra"
)

var (
	tuiTheme string
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive terminal UI",
	Long:  `Launch Sylk's terminal UI with multi-agent chat, session management, and code viewing.`,
	RunE:  runTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	tuiCmd.Flags().StringVar(&tuiTheme, "theme", "dark", "Color theme (dark or light)")
}

func runTUI(_ *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps, cleanup, err := bootstrapDeps(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	defer cleanup()

	cfg := ui.DefaultConfig()
	cfg.ThemeMode = parseThemeMode(tuiTheme)

	return ui.Run(ctx, cfg, deps)
}

// activityBusBuffer is the channel size for the activity event bus.
const activityBusBuffer = 1000

// bootstrapDeps initializes the core systems needed by the TUI.
// Returns a Deps struct and a cleanup function.
func bootstrapDeps(ctx context.Context) (ui.Deps, func(), error) {
	// Goroutine budget with no memory pressure (level 0).
	pressureLevel := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressureLevel)
	scope := concurrency.NewGoroutineScope(ctx, "tui", budget)

	activityBus := events.NewActivityEventBus(activityBusBuffer)
	activityBus.Start()

	sessionMgr := session.NewManager(session.ManagerConfig{
		Scope: scope,
	})

	guideBus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	streamMgr := guide.NewStreamManager(guide.DefaultStreamConfig())

	cleanup := func() {
		_ = guideBus.Close()
		activityBus.Close()
	}

	deps := ui.Deps{
		ActivityBus:    activityBus,
		SessionManager: sessionMgr,
		GuideBus:       guideBus,
		StreamManager:  streamMgr,
		Scope:          scope,
	}

	return deps, cleanup, nil
}

// parseThemeMode converts a theme flag string to a ThemeMode.
func parseThemeMode(s string) ui.ThemeMode {
	if s == "light" {
		return ui.ThemeLight
	}
	return ui.ThemeDark
}
