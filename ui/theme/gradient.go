package theme

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// gradientCycleDuration is the time for one full pass through all gradient colors.
// Derived from gemini-cli's 4-second cycle (smooth at 10fps DecorTick).
const gradientCycleDuration = 4 * time.Second

// holographicCycleDuration is a slower cycle for the prismatic group shimmer.
// 6 seconds produces a mesmerizing iridescent drift at 10fps.
const holographicCycleDuration = 6 * time.Second

// focusRingCycleDuration is the cycle for the focus ring border shimmer.
// 5 seconds avoids locking with the 6-second holographic cycle, producing
// organic drift between the two animations.
const focusRingCycleDuration = 5 * time.Second

// queueCycleDuration is a fast cycle for the prompt queue strip shimmer.
// 3 seconds produces visible motion across the compact 1-3 line strip.
const queueCycleDuration = 3 * time.Second

// FocusRingFlowStep is the phase offset between adjacent border sides.
// 200ms at 5s/cycle (14 colors, 357ms/segment) keeps adjacent sides
// close (~56% of a segment apart) with visible directional flow.
const FocusRingFlowStep = 200 * time.Millisecond

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

// PipelineGradient returns a gradient for the pipeline progress shimmer,
// cycling through spectral accent colors in the same order as ThinkingGradient.
func (p Palette) PipelineGradient() *Gradient {
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

// Duration returns the cycle duration of the gradient.
func (g *Gradient) Duration() time.Duration {
	return g.duration
}

// hexDigits is the lookup table for nibble-to-ASCII hex encoding.
const hexDigits = "0123456789abcdef"

// rgbToColor encodes RGB bytes as a lipgloss "#rrggbb" color with no
// fmt.Sprintf overhead. The 7-byte stack array avoids heap allocation
// in most cases (small string optimization).
func rgbToColor(r, g, b uint8) lipgloss.Color {
	var buf [7]byte
	buf[0] = '#'
	buf[1] = hexDigits[r>>4]
	buf[2] = hexDigits[r&0x0f]
	buf[3] = hexDigits[g>>4]
	buf[4] = hexDigits[g&0x0f]
	buf[5] = hexDigits[b>>4]
	buf[6] = hexDigits[b&0x0f]
	return lipgloss.Color(string(buf[:]))
}

// SampleRGB returns the interpolated RGB components at the given elapsed
// duration. Avoids the hex-encode round-trip when callers need raw bytes
// (e.g. for direct ANSI output in per-character gradient borders).
func (g *Gradient) SampleRGB(elapsed time.Duration) (r, green, b uint8) {
	n := len(g.colors)
	if n == 0 {
		return 0, 0, 0
	}
	if n == 1 {
		return g.colors[0].R, g.colors[0].G, g.colors[0].B
	}

	t := cyclePosition(elapsed, g.duration)
	i, j, frac := gradientSegment(t, n)

	return lerpByte(g.colors[i].R, g.colors[j].R, frac),
		lerpByte(g.colors[i].G, g.colors[j].G, frac),
		lerpByte(g.colors[i].B, g.colors[j].B, frac)
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
		return rgbToColor(c.R, c.G, c.B)
	}

	t := cyclePosition(elapsed, g.duration)
	i, j, frac := gradientSegment(t, n)

	r := lerpByte(g.colors[i].R, g.colors[j].R, frac)
	green := lerpByte(g.colors[i].G, g.colors[j].G, frac)
	b := lerpByte(g.colors[i].B, g.colors[j].B, frac)

	return rgbToColor(r, green, b)
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

// midColor returns the color halfway between a and b in RGB space.
// Used to compute smooth gradient intermediates from palette colors.
func midColor(a, b lipgloss.Color) lipgloss.Color {
	ca := parseHexColor(string(a))
	cb := parseHexColor(string(b))
	return rgbToColor((ca.R+cb.R)/2, (ca.G+cb.G)/2, (ca.B+cb.B)/2)
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

// RipplePhaseStep is the gradient phase offset between adjacent characters
// in the ripple text animation. 80ms per character across a 4-second gradient
// cycle produces a visible wave across ~20 characters.
const RipplePhaseStep = 80 * time.Millisecond

// RenderRippleText renders each character with an individually sampled gradient
// color, producing a flowing wave effect. charOffset shifts the phase so the
// wave can continue across adjacent text segments (e.g. name → summary).
func RenderRippleText(text string, elapsed time.Duration, grad *Gradient, charOffset int) string {
	var b strings.Builder
	b.Grow(len(text) * 20) // ANSI overhead per character
	for i, ch := range text {
		pos := charOffset + i
		phase := elapsed - time.Duration(pos)*RipplePhaseStep
		r, g, bl := grad.SampleRGB(phase)
		writeRippleRune(&b, r, g, bl, ch)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// writeRippleRune appends a single rune colored with raw ANSI truecolor.
func writeRippleRune(b *strings.Builder, r, green, bl uint8, ch rune) {
	var buf [3]byte
	b.WriteString("\x1b[38;2;")
	b.Write(strconv.AppendUint(buf[:0], uint64(r), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(buf[:0], uint64(green), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(buf[:0], uint64(bl), 10))
	b.WriteByte('m')
	b.WriteRune(ch)
}

// RenderGradientGlyph renders a single glyph string colored by the gradient
// at the given elapsed time using raw ANSI truecolor. Shared by agent and
// session panels for the origami-bloom dot animation.
func RenderGradientGlyph(icon string, g *Gradient, elapsed time.Duration) string {
	r, gr, bl := g.SampleRGB(elapsed)
	var b strings.Builder
	var buf [3]byte
	b.WriteString("\x1b[38;2;")
	b.Write(strconv.AppendUint(buf[:0], uint64(r), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(buf[:0], uint64(gr), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(buf[:0], uint64(bl), 10))
	b.WriteString("m")
	b.WriteString(icon)
	b.WriteString("\x1b[0m")
	return b.String()
}
