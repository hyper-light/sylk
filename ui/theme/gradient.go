package theme

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// gradientCycleDuration is the time for one full pass through all gradient colors.
// Derived from gemini-cli's 4-second cycle (smooth at 10fps DecorTick).
const gradientCycleDuration = 4 * time.Second

// rgb holds decomposed color channels for interpolation.
type rgb struct {
	R, G, B uint8
}

// Gradient cycles smoothly through a sequence of colors over a fixed duration.
// Thread-safe (immutable after construction).
type Gradient struct {
	colors   []rgb
	duration time.Duration
}

// NewGradient creates a gradient that cycles through the given hex colors.
func NewGradient(colors []lipgloss.Color, duration time.Duration) *Gradient {
	parsed := make([]rgb, len(colors))
	for i, c := range colors {
		parsed[i] = parseHexColor(string(c))
	}
	return &Gradient{
		colors:   parsed,
		duration: duration,
	}
}

// ThinkingGradient returns a gradient for the thinking indicator,
// cycling through the palette's accent colors in spectral order.
func (p Palette) ThinkingGradient() *Gradient {
	return NewGradient([]lipgloss.Color{
		p.Primary,   // blue
		p.Teal,      // teal
		p.Success,   // green
		p.Warning,   // yellow
		p.Peach,     // peach
		p.Accent,    // pink
		p.Secondary, // mauve
	}, gradientCycleDuration)
}

// Sample returns the interpolated color at the given elapsed duration.
// The gradient cycles continuously — elapsed values beyond one cycle wrap.
func (g *Gradient) Sample(elapsed time.Duration) lipgloss.Color {
	n := len(g.colors)
	if n == 0 {
		return lipgloss.Color("")
	}
	if n == 1 {
		c := g.colors[0]
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
	}

	t := cyclePosition(elapsed, g.duration)
	i, j, frac := gradientSegment(t, n)

	r := lerpByte(g.colors[i].R, g.colors[j].R, frac)
	green := lerpByte(g.colors[i].G, g.colors[j].G, frac)
	b := lerpByte(g.colors[i].B, g.colors[j].B, frac)

	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, green, b))
}

// cyclePosition maps elapsed time to a normalized position [0, 1) in the cycle.
func cyclePosition(elapsed, duration time.Duration) float64 {
	t := elapsed.Seconds() / duration.Seconds()
	t -= float64(int(t))
	if t < 0 {
		t += 1.0
	}
	return t
}

// gradientSegment returns the two color indices and interpolation fraction
// for a normalized position t in [0, 1) across n colors.
func gradientSegment(t float64, n int) (int, int, float64) {
	segment := t * float64(n)
	i := int(segment) % n
	j := (i + 1) % n
	frac := segment - float64(int(segment))
	return i, j, frac
}

// lerpByte linearly interpolates between two bytes.
func lerpByte(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t + 0.5)
}

// parseHexColor parses a "#RRGGBB" string into RGB components.
func parseHexColor(hex string) rgb {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return rgb{}
	}
	return rgb{
		R: hexNibble(hex[0])<<4 | hexNibble(hex[1]),
		G: hexNibble(hex[2])<<4 | hexNibble(hex[3]),
		B: hexNibble(hex[4])<<4 | hexNibble(hex[5]),
	}
}

// hexNibble converts a single hex character to its 4-bit value.
func hexNibble(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0
	}
}
