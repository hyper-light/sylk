package theme

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	borderPhaseBucket   = 100 * time.Millisecond
	borderFrameCacheCap = 64
	ansiReset           = "\x1b[0m"
)

type borderFrameKey struct {
	gradient    *Gradient
	innerW      int
	innerH      int
	phaseBucket int64
}

type borderFrame struct {
	top    string
	bottom string
	left   []string
	right  []string
}

var (
	borderFrameCacheMu sync.Mutex
	borderFrameCache   = make(map[borderFrameKey]borderFrame)
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

	lines := normalizeBorderLines(content, innerW, innerH)
	frame := cachedBorderFrame(g, elapsed, innerW, len(lines))

	// Estimate: border chars ~20 bytes each (ANSI), content ~innerW,
	// newlines, plus reset sequences.
	var b strings.Builder
	b.Grow((innerW + 40) * (len(lines) + 2))
	b.WriteString(frame.top)
	b.WriteByte('\n')
	for r, line := range lines {
		b.WriteString(frame.left[r])
		b.WriteString(line)
		b.WriteString(ansiReset)
		b.WriteString(frame.right[r])
		b.WriteByte('\n')
	}
	b.WriteString(frame.bottom)

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

func normalizeBorderLines(content string, innerW, innerH int) []string {
	if innerW < 0 {
		innerW = 0
	}
	if innerH < 0 {
		innerH = 0
	}

	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}

	pad := strings.Repeat(" ", innerW)
	for len(lines) < innerH {
		lines = append(lines, pad)
	}

	for i, line := range lines {
		w := ansi.StringWidth(line)
		switch {
		case w < innerW:
			lines[i] = line + pad[:innerW-w]
		case w > innerW:
			lines[i] = ansi.Truncate(line, innerW, "")
		}
	}

	return lines
}

func cachedBorderFrame(g *Gradient, elapsed time.Duration, innerW, innerH int) borderFrame {
	normalized := normalizeGradientElapsed(g, elapsed)
	key := borderFrameKey{
		gradient:    g,
		innerW:      innerW,
		innerH:      innerH,
		phaseBucket: int64(normalized / borderPhaseBucket),
	}

	borderFrameCacheMu.Lock()
	frame, ok := borderFrameCache[key]
	borderFrameCacheMu.Unlock()
	if ok {
		return frame
	}

	frame = buildBorderFrame(g, time.Duration(key.phaseBucket)*borderPhaseBucket, innerW, innerH)

	borderFrameCacheMu.Lock()
	if len(borderFrameCache) >= borderFrameCacheCap {
		clear(borderFrameCache)
	}
	borderFrameCache[key] = frame
	borderFrameCacheMu.Unlock()

	return frame
}

func buildBorderFrame(g *Gradient, elapsed time.Duration, innerW, innerH int) borderFrame {
	borderW := innerW + 2
	perim := 2 * (borderW + innerH)

	totalSpread := g.Duration()
	if totalSpread <= 0 {
		totalSpread = borderPhaseBucket
	}
	phasePerChar := time.Duration(0)
	if perim > 0 {
		phasePerChar = totalSpread / time.Duration(perim)
	}

	bd := lipgloss.RoundedBorder()
	frame := borderFrame{
		left:  make([]string, innerH),
		right: make([]string, innerH),
	}

	var top strings.Builder
	top.Grow(borderW * 20)
	for i := 0; i < borderW; i++ {
		ch := bd.Top
		switch i {
		case 0:
			ch = bd.TopLeft
		case borderW - 1:
			ch = bd.TopRight
		}
		writeColoredChar(&top, g, elapsed, i, phasePerChar, ch)
	}
	top.WriteString(ansiReset)
	frame.top = top.String()

	for r := 0; r < innerH; r++ {
		leftPos := perim - 1 - r
		rightPos := borderW + r

		var left, right strings.Builder
		left.Grow(24)
		right.Grow(24)
		writeColoredChar(&left, g, elapsed, leftPos, phasePerChar, bd.Left)
		left.WriteString(ansiReset)
		writeColoredChar(&right, g, elapsed, rightPos, phasePerChar, bd.Right)
		right.WriteString(ansiReset)
		frame.left[r] = left.String()
		frame.right[r] = right.String()
	}

	var bottom strings.Builder
	bottom.Grow(borderW * 20)
	for i := 0; i < borderW; i++ {
		ch := bd.Bottom
		switch i {
		case 0:
			ch = bd.BottomLeft
		case borderW - 1:
			ch = bd.BottomRight
		}
		pos := borderW + innerH + (borderW - 1 - i)
		writeColoredChar(&bottom, g, elapsed, pos, phasePerChar, ch)
	}
	bottom.WriteString(ansiReset)
	frame.bottom = bottom.String()

	return frame
}

func normalizeGradientElapsed(g *Gradient, elapsed time.Duration) time.Duration {
	if g == nil {
		return elapsed
	}
	duration := g.Duration()
	if duration <= 0 {
		return elapsed
	}
	normalized := elapsed % duration
	if normalized < 0 {
		normalized += duration
	}
	return normalized
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
