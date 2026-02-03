package theme

import "github.com/charmbracelet/lipgloss"

// Theme holds all styled components for the TUI.
type Theme struct {
	Palette Palette

	// Panel styles
	ActiveBorder   lipgloss.Style
	InactiveBorder lipgloss.Style

	// Chat message styles
	UserMessage   lipgloss.Style
	AgentMessage  lipgloss.Style
	SystemMessage lipgloss.Style
	ErrorMessage  lipgloss.Style
	ToolMessage   lipgloss.Style

	// Input
	InputBorder  lipgloss.Style
	InputFocused lipgloss.Style
	Placeholder  lipgloss.Style

	// Status bar
	StatusBar     lipgloss.Style
	StatusNormal  lipgloss.Style
	StatusWarning lipgloss.Style
	StatusError   lipgloss.Style

	// Session panel
	SessionActive   lipgloss.Style
	SessionInactive lipgloss.Style

	// Agent panel
	AgentActive   lipgloss.Style
	AgentInactive lipgloss.Style
	AgentBadge    func(agentType string) lipgloss.Style

	// Syntax highlighting
	Syntax map[SyntaxCategory]lipgloss.Style
}

// New creates a Theme from a Palette.
func New(p Palette) *Theme {
	t := &Theme{
		Palette: p,

		ActiveBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.BorderActive),

		InactiveBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border),

		UserMessage: lipgloss.NewStyle().
			Foreground(p.Foreground),

		AgentMessage: lipgloss.NewStyle().
			Foreground(p.Foreground),

		SystemMessage: lipgloss.NewStyle().
			Foreground(p.Muted).
			Italic(true),

		ErrorMessage: lipgloss.NewStyle().
			Foreground(p.Error),

		ToolMessage: lipgloss.NewStyle().
			Foreground(p.Info),

		InputBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border),

		InputFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Primary),

		Placeholder: lipgloss.NewStyle().
			Foreground(p.Muted),

		StatusBar: lipgloss.NewStyle().
			Foreground(p.Muted),

		StatusNormal: lipgloss.NewStyle().
			Foreground(p.Success).
			Bold(true),

		StatusWarning: lipgloss.NewStyle().
			Foreground(p.Warning).
			Bold(true),

		StatusError: lipgloss.NewStyle().
			Foreground(p.Error).
			Bold(true),

		SessionActive: lipgloss.NewStyle().
			Foreground(p.Primary).
			Bold(true),

		SessionInactive: lipgloss.NewStyle().
			Foreground(p.Muted),

		AgentActive: lipgloss.NewStyle().
			Foreground(p.Success).
			Bold(true),

		AgentInactive: lipgloss.NewStyle().
			Foreground(p.Muted),

		Syntax: SyntaxStyles(p),
	}

	t.AgentBadge = func(agentType string) lipgloss.Style {
		return lipgloss.NewStyle().
			Foreground(p.AgentColor(agentType)).
			Bold(true)
	}

	return t
}

// DefaultDark returns a dark theme.
func DefaultDark() *Theme {
	return New(DarkPalette)
}

// DefaultLight returns a light theme.
func DefaultLight() *Theme {
	return New(LightPalette)
}
