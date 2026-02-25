package theme

import "github.com/charmbracelet/lipgloss"

// Palette defines a color scheme for the TUI.
type Palette struct {
	// Base
	Background lipgloss.Color
	Foreground lipgloss.Color
	Subtext    lipgloss.Color // Between Foreground and Muted; secondary content.
	Muted      lipgloss.Color
	Subtle     lipgloss.Color

	// Accent
	Primary   lipgloss.Color
	Secondary lipgloss.Color
	Accent    lipgloss.Color

	// Extended accent (syntax highlighting differentiation)
	Peach     lipgloss.Color
	Teal      lipgloss.Color
	Lavender  lipgloss.Color
	Flamingo  lipgloss.Color
	Sapphire  lipgloss.Color
	Maroon    lipgloss.Color
	Rosewater lipgloss.Color

	// Complementary spectrum fills (bridge the largest Catppuccin hue gaps).
	Wisteria lipgloss.Color // ~280° — bridges mauve → pink
	Iris     lipgloss.Color // ~290° — bridges wisteria → pink
	Rose     lipgloss.Color // ~10°  — bridges flamingo → peach
	Citron   lipgloss.Color // ~70°  — bridges yellow → green
	Lime     lipgloss.Color // ~80°  — bridges citron → green

	// Semantic
	Success lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color
	Info    lipgloss.Color

	// Interaction
	HoverAccent    lipgloss.Color // Neon accent for hovered/focused interactive elements.
	HoverAccentDim lipgloss.Color // Darker shade of HoverAccent for hover feedback.

	// UI elements
	GroupActive  lipgloss.Color // Active agent group frame (header, tree, footer).
	FocusIce     lipgloss.Color // Icy near-white for focus ring shimmer peak.
	Border       lipgloss.Color
	BorderActive lipgloss.Color
	Highlight    lipgloss.Color // Subtle background for transient highlights.
	Selection    lipgloss.Color // Background for persistent entry selection.
	PopupBg      lipgloss.Color // Background for floating popups (darker than base).
	WarpBg       lipgloss.Color // Subtle background for warp-pointed lines.

	// Diff backgrounds
	DiffAddBg   lipgloss.Color // Subtle green tint for addition lines.
	DiffDelBg   lipgloss.Color // Subtle red tint for deletion lines.
	DiffAddChar lipgloss.Color // Stronger green for char-level additions.
	DiffDelChar lipgloss.Color // Stronger red for char-level deletions.

	// Agent-specific (indexed by type hash for consistency)
	AgentColors []lipgloss.Color
}

// DarkPalette is the default dark color scheme.
var DarkPalette = Palette{
	Background: lipgloss.Color("#1e1e2e"),
	Foreground: lipgloss.Color("#cdd6f4"),
	Subtext:    lipgloss.Color("#a6adc8"),
	Muted:      lipgloss.Color("#6c7086"),
	Subtle:     lipgloss.Color("#45475a"),

	Primary:   lipgloss.Color("#89b4fa"),
	Secondary: lipgloss.Color("#cba6f7"),
	Accent:    lipgloss.Color("#f5c2e7"),

	Peach:     lipgloss.Color("#fab387"),
	Teal:      lipgloss.Color("#94e2d5"),
	Lavender:  lipgloss.Color("#b4befe"),
	Flamingo:  lipgloss.Color("#f0c6c6"),
	Sapphire:  lipgloss.Color("#74c7ec"),
	Maroon:    lipgloss.Color("#eba0ac"),
	Rosewater: lipgloss.Color("#89dceb"),

	Wisteria: lipgloss.Color("#b49fdc"), // dusty purple ~280°
	Iris:     lipgloss.Color("#c4a7e7"), // soft lilac ~290°
	Rose:     lipgloss.Color("#ebbcba"), // muted dusty pink ~10°
	Citron:   lipgloss.Color("#d4e77b"), // yellow-green ~70°
	Lime:     lipgloss.Color("#c3e88d"), // soft chartreuse ~80°

	Success: lipgloss.Color("#a6e3a1"),
	Warning: lipgloss.Color("#f9e2af"),
	Error:   lipgloss.Color("#f38ba8"),
	Info:    lipgloss.Color("#89dceb"),

	HoverAccent:    lipgloss.Color("#bf5fff"), // neon purple – hover/focus interactive elements
	HoverAccentDim: lipgloss.Color("#8839ef"), // dark purple – hover feedback on interactive elements

	GroupActive: lipgloss.Color("#6dd6b5"), // jade – active agent group frame
	FocusIce:   lipgloss.Color("#d0d8f0"), // icy pale blue – focus ring shimmer peak
	Border:       lipgloss.Color("#45475a"),
	BorderActive: lipgloss.Color("#89b4fa"),
	Highlight:    lipgloss.Color("#45475a"), // surface1 – prominent transient copy feedback
	Selection:    lipgloss.Color("#313244"), // surface0 – subtle persistent selection
	PopupBg:      lipgloss.Color("#181825"), // mantle – darker than base for floating popups
	WarpBg:       lipgloss.Color("#2a2040"), // dark purple tint – subtle warp line background

	DiffAddBg:   lipgloss.Color("#1e2b25"), // subtle green tint over base for addition lines
	DiffDelBg:   lipgloss.Color("#2b1e24"), // subtle red tint over base for deletion lines
	DiffAddChar: lipgloss.Color("#243d30"), // moderate green for char-level additions
	DiffDelChar: lipgloss.Color("#3d2430"), // moderate red for char-level deletions

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
	Subtext:    lipgloss.Color("#6c6f85"),
	Muted:      lipgloss.Color("#9ca0b0"),
	Subtle:     lipgloss.Color("#bcc0cc"),

	Primary:   lipgloss.Color("#1e66f5"),
	Secondary: lipgloss.Color("#8839ef"),
	Accent:    lipgloss.Color("#ea76cb"),

	Peach:     lipgloss.Color("#fe640b"),
	Teal:      lipgloss.Color("#179299"),
	Lavender:  lipgloss.Color("#7287fd"),
	Flamingo:  lipgloss.Color("#dd7878"),
	Sapphire:  lipgloss.Color("#209fb5"),
	Maroon:    lipgloss.Color("#e64553"),
	Rosewater: lipgloss.Color("#dc8a78"),

	Wisteria: lipgloss.Color("#7a5fa8"), // dusty purple ~280°
	Iris:     lipgloss.Color("#8b5fc7"), // soft lilac ~290°
	Rose:     lipgloss.Color("#b86b6b"), // muted dusty pink ~10°
	Citron:   lipgloss.Color("#8a9930"), // yellow-green ~70°
	Lime:     lipgloss.Color("#668b2b"), // soft chartreuse ~80°

	Success: lipgloss.Color("#40a02b"),
	Warning: lipgloss.Color("#df8e1d"),
	Error:   lipgloss.Color("#d20f39"),
	Info:    lipgloss.Color("#04a5e5"),

	HoverAccent:    lipgloss.Color("#7b2fbe"), // neon purple – hover/focus interactive elements
	HoverAccentDim: lipgloss.Color("#5c1f99"), // dark purple – hover feedback on interactive elements

	GroupActive: lipgloss.Color("#1a9a7a"), // jade – active agent group frame
	FocusIce:   lipgloss.Color("#e8ecf8"), // icy pale blue – focus ring shimmer peak
	Border:       lipgloss.Color("#bcc0cc"),
	BorderActive: lipgloss.Color("#1e66f5"),
	Highlight:    lipgloss.Color("#dce0e8"), // crust – prominent transient copy feedback
	Selection:    lipgloss.Color("#e6e9ef"), // mantle – subtle persistent selection
	PopupBg:      lipgloss.Color("#dce0e8"), // crust – lighter than base for floating popups
	WarpBg:       lipgloss.Color("#e8dff5"), // light purple tint – subtle warp line background

	DiffAddBg:   lipgloss.Color("#e4f0e4"), // subtle green tint over base for addition lines
	DiffDelBg:   lipgloss.Color("#f0e4e4"), // subtle red tint over base for deletion lines
	DiffAddChar: lipgloss.Color("#c0dcc0"), // moderate green for char-level additions
	DiffDelChar: lipgloss.Color("#dcc0c0"), // moderate red for char-level deletions

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

// prismaticColors returns 19 curated colors (14 Catppuccin accents + jade
// + 4 complementary spectrum fills) arranged in spectral hue order for
// smooth rainbow cycling. No computed midpoints — every color is hand-picked.
func (p Palette) prismaticColors() []lipgloss.Color {
	return []lipgloss.Color{
		p.Success,     //  1 green    ~115°
		p.GroupActive, //  2 jade     ~155°
		p.Teal,        //  3 teal     ~170°
		p.Info,        //  4 sky      ~195°
		p.Sapphire,    //  5 sapphire ~200°
		p.Primary,     //  6 blue     ~220°
		p.Lavender,    //  7 lavender ~240°
		p.Secondary,   //  8 mauve    ~265°
		p.Wisteria,    //  9 wisteria ~280°
		p.Iris,        // 10 iris     ~290°
		p.Accent,      // 11 pink     ~320°
		p.Error,       // 12 red      ~345°
		p.Maroon,      // 13 maroon   ~350°
		p.Flamingo,    // 14 flamingo ~0°
		p.Rose,        // 15 rose     ~10°
		p.Peach,       // 16 peach    ~25°
		p.Warning,     // 17 yellow   ~40°
		p.Citron,      // 18 citron   ~70°
		p.Lime,        // 19 lime     ~80°
		// wraps smoothly back to green
	}
}

// GroupGradient returns a holographic prismatic gradient for the active agent
// group frame (header, tree connectors, footer). 19 spectral keyframes over
// 6 seconds for smooth, gradual color flow.
func (p Palette) GroupGradient() *Gradient {
	return NewGradient(p.prismaticColors(), holographicCycleDuration)
}

// IdleGroupGradient returns a subdued gradient for the agent group frame
// when no agents are actively working. Green → jade → teal → blue → white
// produces a calm ambient shimmer without the full prismatic spectrum.
func (p Palette) IdleGroupGradient() *Gradient {
	return NewGradient([]lipgloss.Color{
		p.Success,     // green ~115°
		p.GroupActive, // jade  ~155°
		p.Teal,        // teal  ~170°
		p.Sapphire,    // sapphire ~200°
		p.Primary,     // blue  ~220°
		p.Lavender,    // lavender ~240°
		p.FocusIce,    // near-white
	}, holographicCycleDuration)
}

// FocusRingGradient returns a gradient for the focus ring border shimmer.
// Same 19 spectral keyframes, 5-second cycle to avoid phase-locking with
// the 6-second group gradient.
func (p Palette) FocusRingGradient() *Gradient {
	return NewGradient(p.prismaticColors(), focusRingCycleDuration)
}

// IdleFocusRingGradient returns a subdued gradient for the focus ring when
// no agents are actively working. Blue → white hues produce a subtle ambient
// shimmer that signals the app is alive without the full prismatic spectrum.
func (p Palette) IdleFocusRingGradient() *Gradient {
	return NewGradient([]lipgloss.Color{
		p.Primary,  // blue ~220°
		p.Sapphire, // sapphire ~200°
		p.Lavender, // lavender ~240°
		p.FocusIce, // near-white
	}, focusRingCycleDuration)
}

// RippleGradient returns a gradient for the per-character name/summary ripple
// on active agents. Same spectral keyframes as GroupGradient but on a 4-second
// cycle for faster visible flow across short text spans.
func (p Palette) RippleGradient() *Gradient {
	return NewGradient(p.prismaticColors(), gradientCycleDuration)
}

// QueueGradient returns a cool-spectrum gradient for the prompt queue strip.
// Teal → blue → lavender → mauve on a 3-second cycle, visually distinct from
// the warm prismatic agent animations.
func (p Palette) QueueGradient() *Gradient {
	return NewGradient([]lipgloss.Color{
		p.Teal,      // teal
		p.Primary,   // blue
		p.Lavender,  // lavender
		p.Secondary, // mauve
	}, queueCycleDuration)
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
