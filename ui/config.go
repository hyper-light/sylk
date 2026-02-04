package ui

import "github.com/adalundhe/sylk/ui/theme"

// Config holds TUI configuration.
type Config struct {
	// Theme selection
	ThemeMode ThemeMode

	// Layout
	MinPanelWidth int // Minimum panel width for layout breakpoints (default 40)

	// Chat
	ChatHistoryCapacity int // Max chat entries in ring buffer (default 10000)

	// Input
	InputHistoryCapacity int  // Max input history entries (default 500)
	InputMaxHeight       int  // Max visible input lines before scroll (default 10)

	// Bridge
	BridgeBufferSize int // Bounded channel size for event bridges (default 256)

	// Interrupt
	InterruptThresholdMs int // Double Ctrl+C window in ms (default 500)

	// Project root (git root, user-specified, or CWD). Set automatically if empty.
	ProjectRoot string

	// Mock mode
	MockMode bool // Run with mock backend for testing (no real agents)
}

// ThemeMode selects the color scheme.
type ThemeMode int

const (
	ThemeDark ThemeMode = iota
	ThemeLight
)

// DefaultConfig returns the default TUI configuration.
func DefaultConfig() Config {
	return Config{
		ThemeMode:            ThemeDark,
		MinPanelWidth:        40,
		ChatHistoryCapacity:  10000,
		InputHistoryCapacity: 500,
		InputMaxHeight:       10,
		BridgeBufferSize:     256,
		InterruptThresholdMs: 500,
	}
}

// Theme returns the theme for the configured mode.
func (c Config) Theme() *theme.Theme {
	if c.ThemeMode == ThemeLight {
		return theme.DefaultLight()
	}
	return theme.DefaultDark()
}
