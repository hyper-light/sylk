package theme

import "github.com/charmbracelet/lipgloss"

// Palette defines a color scheme for the TUI.
type Palette struct {
	// Base
	Background lipgloss.Color
	Foreground lipgloss.Color
	Muted      lipgloss.Color
	Subtle     lipgloss.Color

	// Accent
	Primary   lipgloss.Color
	Secondary lipgloss.Color
	Accent    lipgloss.Color

	// Semantic
	Success lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color
	Info    lipgloss.Color

	// UI elements
	Border       lipgloss.Color
	BorderActive lipgloss.Color
	Highlight    lipgloss.Color // Subtle background for transient highlights.
	Selection    lipgloss.Color // Background for persistent entry selection.

	// Agent-specific (indexed by type hash for consistency)
	AgentColors []lipgloss.Color
}

// DarkPalette is the default dark color scheme.
var DarkPalette = Palette{
	Background: lipgloss.Color("#1e1e2e"),
	Foreground: lipgloss.Color("#cdd6f4"),
	Muted:      lipgloss.Color("#6c7086"),
	Subtle:     lipgloss.Color("#45475a"),

	Primary:   lipgloss.Color("#89b4fa"),
	Secondary: lipgloss.Color("#cba6f7"),
	Accent:    lipgloss.Color("#f5c2e7"),

	Success: lipgloss.Color("#a6e3a1"),
	Warning: lipgloss.Color("#f9e2af"),
	Error:   lipgloss.Color("#f38ba8"),
	Info:    lipgloss.Color("#89dceb"),

	Border:       lipgloss.Color("#45475a"),
	BorderActive: lipgloss.Color("#89b4fa"),
	Highlight:    lipgloss.Color("#45475a"), // surface1 – prominent transient copy feedback
	Selection:    lipgloss.Color("#313244"), // surface0 – subtle persistent selection

	AgentColors: []lipgloss.Color{
		lipgloss.Color("#89b4fa"), // blue - engineer
		lipgloss.Color("#cba6f7"), // mauve - architect
		lipgloss.Color("#a6e3a1"), // green - guide
		lipgloss.Color("#f9e2af"), // yellow - inspector
		lipgloss.Color("#fab387"), // peach - tester
		lipgloss.Color("#89dceb"), // sky - librarian
		lipgloss.Color("#f5c2e7"), // pink - designer
		lipgloss.Color("#94e2d5"), // teal - academic
		lipgloss.Color("#f38ba8"), // red - archivalist
		lipgloss.Color("#eba0ac"), // maroon - orchestrator
	},
}

// LightPalette is the light color scheme.
var LightPalette = Palette{
	Background: lipgloss.Color("#eff1f5"),
	Foreground: lipgloss.Color("#4c4f69"),
	Muted:      lipgloss.Color("#9ca0b0"),
	Subtle:     lipgloss.Color("#bcc0cc"),

	Primary:   lipgloss.Color("#1e66f5"),
	Secondary: lipgloss.Color("#8839ef"),
	Accent:    lipgloss.Color("#ea76cb"),

	Success: lipgloss.Color("#40a02b"),
	Warning: lipgloss.Color("#df8e1d"),
	Error:   lipgloss.Color("#d20f39"),
	Info:    lipgloss.Color("#04a5e5"),

	Border:       lipgloss.Color("#bcc0cc"),
	BorderActive: lipgloss.Color("#1e66f5"),
	Highlight:    lipgloss.Color("#dce0e8"), // crust – prominent transient copy feedback
	Selection:    lipgloss.Color("#e6e9ef"), // mantle – subtle persistent selection

	AgentColors: []lipgloss.Color{
		lipgloss.Color("#1e66f5"), // blue
		lipgloss.Color("#8839ef"), // mauve
		lipgloss.Color("#40a02b"), // green
		lipgloss.Color("#df8e1d"), // yellow
		lipgloss.Color("#fe640b"), // peach
		lipgloss.Color("#04a5e5"), // sky
		lipgloss.Color("#ea76cb"), // pink
		lipgloss.Color("#179299"), // teal
		lipgloss.Color("#d20f39"), // red
		lipgloss.Color("#e64553"), // maroon
	},
}

// agentTypeIndex maps agent type names to consistent color indices.
var agentTypeIndex = map[string]int{
	"engineer":    0,
	"architect":   1,
	"guide":       2,
	"inspector":   3,
	"tester":      4,
	"librarian":   5,
	"designer":    6,
	"academic":    7,
	"archivalist": 8,
	"orchestrator": 9,
}

// AgentColor returns the palette color for a given agent type.
// Unknown agent types are assigned a color derived from the name hash.
func (p Palette) AgentColor(agentType string) lipgloss.Color {
	if idx, ok := agentTypeIndex[agentType]; ok && idx < len(p.AgentColors) {
		return p.AgentColors[idx]
	}
	// Derive from name hash for unknown types
	h := uint(0)
	for _, r := range agentType {
		h = h*31 + uint(r)
	}
	return p.AgentColors[h%uint(len(p.AgentColors))]
}
