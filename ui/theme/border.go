package theme

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// RenderGradientBorder wraps content in a rounded border where each border
// character is individually colored from the gradient based on its clockwise
// perimeter position. This produces smooth flowing color with no visible
// jumps at corners.
//
// Perimeter positions (clockwise from top-left):
//
//	Top:    0..borderW-1          (left → right)
//	Right:  borderW..borderW+H-1  (top → bottom)
//	Bottom: borderW+H..2·borderW+H-1  (right → left)
//	Left:   2·borderW+H..P-1      (bottom → top)
func RenderGradientBorder(content string, g *Gradient, elapsed time.Duration, innerW, innerH, maxH int) string {
	if g == nil {
		return content
	}

	// Split content into lines and pad/truncate to exact inner dimensions.
	// Manual processing avoids lipgloss re-wrapping ANSI-dense content
	// (e.g. per-character selection styling) which can produce extra lines.
	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	pad := strings.Repeat(" ", innerW)
	for len(lines) < innerH {
		lines = append(lines, pad)
	}
	for i, line := range lines {
		w := lipgloss.Width(line)
		switch {
		case w < innerW:
			lines[i] = line + strings.Repeat(" ", innerW-w)
		case w > innerW:
			// Truncate oversized lines (e.g. cursor trailing space
			// extending past contentWidth). MaxWidth may wrap instead
			// of truncate, so take only the first line to prevent
			// embedded newlines from corrupting the border structure.
			rendered := lipgloss.NewStyle().MaxWidth(innerW).Render(line)
			if idx := strings.Index(rendered, "\n"); idx >= 0 {
				rendered = rendered[:idx]
			}
			lines[i] = rendered
		}
	}

	nRows := len(lines)
	borderW := innerW + 2
	perim := 2 * (borderW + nRows)

	// Phase spread: total gradient time visible around the border at any
	// instant. 3×FocusRingFlowStep keeps the same visual spread as the
	// old 4-side approach but distributed smoothly per character.
	totalSpread := 3 * FocusRingFlowStep
	phasePerChar := totalSpread / time.Duration(perim)

	bd := lipgloss.RoundedBorder()

	// Estimate: border chars ~20 bytes each (ANSI), content ~innerW,
	// newlines, plus reset sequences.
	var b strings.Builder
	b.Grow((borderW + 40) * (nRows + 2))

	// Top border: ╭─...─╮ (positions 0..borderW-1, left to right).
	for i := range borderW {
		ch := bd.Top
		switch i {
		case 0:
			ch = bd.TopLeft
		case borderW - 1:
			ch = bd.TopRight
		}
		writeColoredChar(&b, g, elapsed, i, phasePerChar, ch)
	}
	b.WriteString("\x1b[0m\n")

	// Content rows with │ borders.
	for r, line := range lines {
		// Left │ — perimeter goes bottom-to-top on left side.
		leftPos := perim - 1 - r
		writeColoredChar(&b, g, elapsed, leftPos, phasePerChar, bd.Left)
		b.WriteString("\x1b[0m")

		b.WriteString(line)

		// Reset content styling before right border.
		b.WriteString("\x1b[0m")

		// Right │ — perimeter goes top-to-bottom on right side.
		rightPos := borderW + r
		writeColoredChar(&b, g, elapsed, rightPos, phasePerChar, bd.Right)
		b.WriteString("\x1b[0m\n")
	}

	// Bottom border: ╰─...─╯ (right-to-left in perimeter order).
	for i := range borderW {
		ch := bd.Bottom
		switch i {
		case 0:
			ch = bd.BottomLeft
		case borderW - 1:
			ch = bd.BottomRight
		}
		// Column i maps to perimeter position right-to-left.
		pos := borderW + nRows + (borderW - 1 - i)
		writeColoredChar(&b, g, elapsed, pos, phasePerChar, ch)
	}
	b.WriteString("\x1b[0m")

	result := b.String()

	// MaxHeight trimming (total = top + content + bottom).
	if maxH > 0 {
		resultLines := strings.Split(result, "\n")
		if len(resultLines) > maxH {
			result = strings.Join(resultLines[:maxH], "\n")
		}
	}

	return result
}

// writeColoredChar appends a border character colored by the gradient at the
// given perimeter position. Uses raw ANSI truecolor escape codes to avoid
// lipgloss style-creation overhead on the per-character hot path.
func writeColoredChar(b *strings.Builder, g *Gradient, elapsed time.Duration, pos int, phasePerChar time.Duration, ch string) {
	phase := elapsed - time.Duration(pos)*phasePerChar
	r, green, bl := g.SampleRGB(phase)

	var buf [3]byte
	b.WriteString("\x1b[38;2;")
	b.Write(strconv.AppendUint(buf[:0], uint64(r), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(buf[:0], uint64(green), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(buf[:0], uint64(bl), 10))
	b.WriteByte('m')
	b.WriteString(ch)
}
