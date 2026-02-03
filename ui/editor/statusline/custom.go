package statusline

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/editor/mode"
	"github.com/adalundhe/sylk/ui/theme"
)

// ---------------------------------------------------------------------------
// Section alignment
// ---------------------------------------------------------------------------

// SectionAlignment determines where a section is placed in the status line.
type SectionAlignment int

const (
	AlignLeft   SectionAlignment = iota
	AlignCenter
	AlignRight
)

// ---------------------------------------------------------------------------
// StatusInfo
// ---------------------------------------------------------------------------

// StatusInfo carries the editor state snapshot consumed by section renderers.
type StatusInfo struct {
	Mode       mode.Mode
	Filename   string
	Modified   bool
	Line       int // zero-indexed
	Col        int // zero-indexed
	Total      int // total line count
	FileType   string
	Encoding   string
}

// ---------------------------------------------------------------------------
// Section
// ---------------------------------------------------------------------------

// Section defines one segment of the custom status line.
type Section struct {
	Name      string
	Content   func(StatusInfo) string
	Priority  int // lower numeric value = higher importance (dropped last)
	Alignment SectionAlignment
}

// ---------------------------------------------------------------------------
// Default sections (table-driven)
// ---------------------------------------------------------------------------

// defaultSections defines the out-of-the-box status line layout.
var defaultSections = []Section{
	{Name: "mode", Alignment: AlignLeft, Priority: 1, Content: renderMode},
	{Name: "filename", Alignment: AlignLeft, Priority: 2, Content: renderFilename},
	{Name: "modified", Alignment: AlignLeft, Priority: 3, Content: renderModified},
	{Name: "encoding", Alignment: AlignRight, Priority: 4, Content: renderEncoding},
	{Name: "filetype", Alignment: AlignRight, Priority: 3, Content: renderFileType},
	{Name: "position", Alignment: AlignRight, Priority: 1, Content: renderPosition},
	{Name: "progress", Alignment: AlignRight, Priority: 2, Content: renderProgress},
}

func renderMode(info StatusInfo) string {
	return fmt.Sprintf(" %s ", mode.ModeName(info.Mode))
}

func renderFilename(info StatusInfo) string {
	name := info.Filename
	if name == "" {
		name = "[No Name]"
	}
	return fmt.Sprintf(" %s ", name)
}

func renderModified(info StatusInfo) string {
	if !info.Modified {
		return ""
	}
	return " [+] "
}

func renderEncoding(info StatusInfo) string {
	enc := info.Encoding
	if enc == "" {
		enc = "utf-8"
	}
	return fmt.Sprintf(" %s ", enc)
}

func renderFileType(info StatusInfo) string {
	ft := info.FileType
	if ft == "" {
		ft = "plain"
	}
	return fmt.Sprintf(" %s ", ft)
}

func renderPosition(info StatusInfo) string {
	return fmt.Sprintf(" %d:%d ", info.Line+1, info.Col+1)
}

func renderProgress(info StatusInfo) string {
	total := max(info.Total, 1)
	pct := ((info.Line + 1) * 100) / total
	return renderProgressLabel(pct)
}

// progressThresholdTable maps percentage thresholds to their display
// labels. Entries are checked in order; the first match wins.
var progressThresholdTable = []struct {
	maxPct int
	label  string
}{
	{0, " Top "},
	{99, ""},   // placeholder for numeric display
	{100, " Bot "},
}

func renderProgressLabel(pct int) string {
	for _, entry := range progressThresholdTable {
		if pct <= entry.maxPct {
			if entry.label != "" {
				return entry.label
			}
			return fmt.Sprintf(" %d%% ", pct)
		}
	}
	return fmt.Sprintf(" %d%% ", pct)
}

// ---------------------------------------------------------------------------
// Component registry (Lua integration point)
// ---------------------------------------------------------------------------

// maxRegisteredComponents caps the number of plugin-provided components.
const maxRegisteredComponents = 32

// ComponentRegistry maps component names to render functions. It is
// prepared for Lua runtime integration in Phase 5d.
type ComponentRegistry struct {
	mu         sync.RWMutex
	components map[string]func(StatusInfo) string
}

// NewComponentRegistry creates an empty component registry.
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		components: make(map[string]func(StatusInfo) string, maxRegisteredComponents),
	}
}

// Register adds a named render function to the registry. If the registry
// is at capacity, the registration is silently dropped.
func (cr *ComponentRegistry) Register(name string, fn func(StatusInfo) string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	_, exists := cr.components[name]
	if !exists && len(cr.components) >= maxRegisteredComponents {
		return
	}
	cr.components[name] = fn
}

// Unregister removes a named component from the registry.
func (cr *ComponentRegistry) Unregister(name string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	delete(cr.components, name)
}

// Lookup retrieves a render function by name. Returns nil when not found.
func (cr *ComponentRegistry) Lookup(name string) func(StatusInfo) string {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return cr.components[name]
}

// ---------------------------------------------------------------------------
// CustomStatusline
// ---------------------------------------------------------------------------

// maxSections caps the number of configurable sections.
const maxSections = 16

// sectionSeparator is the Unicode separator between adjacent sections.
const sectionSeparator = " | "

// CustomStatusline extends the base status line with configurable,
// prioritized sections and a component registry for Lua plugins.
type CustomStatusline struct {
	mu         sync.RWMutex
	theme      *theme.Theme
	info       StatusInfo
	width      int
	sections   []Section
	components *ComponentRegistry
}

// NewCustom creates a CustomStatusline with default sections and an empty
// component registry.
func NewCustom(th *theme.Theme) *CustomStatusline {
	sections := make([]Section, len(defaultSections))
	copy(sections, defaultSections)
	return &CustomStatusline{
		theme:      th,
		sections:   sections,
		components: NewComponentRegistry(),
		info:       StatusInfo{Encoding: "utf-8"},
	}
}

// SetInfo updates the editor state snapshot.
func (cs *CustomStatusline) SetInfo(info StatusInfo) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.info = info
}

// SetWidth updates the available rendering width.
func (cs *CustomStatusline) SetWidth(w int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.width = max(w, 0)
}

// SetSections replaces the current section configuration. This is the
// entry point for Lua plugin configuration. The list is capped at
// maxSections.
func (cs *CustomStatusline) SetSections(sections []Section) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(sections) > maxSections {
		sections = sections[:maxSections]
	}
	cs.sections = make([]Section, len(sections))
	copy(cs.sections, sections)
}

// RegisterComponent delegates to the underlying ComponentRegistry.
func (cs *CustomStatusline) RegisterComponent(name string, fn func(StatusInfo) string) {
	cs.components.Register(name, fn)
}

// Components returns the component registry for external use.
func (cs *CustomStatusline) Components() *ComponentRegistry {
	return cs.components
}

// View renders the full status line, distributing sections by alignment
// and dropping lower-priority sections when width is insufficient.
func (cs *CustomStatusline) View() string {
	cs.mu.RLock()
	info := cs.info
	width := cs.width
	sections := make([]Section, len(cs.sections))
	copy(sections, cs.sections)
	th := cs.theme
	cs.mu.RUnlock()

	rendered := renderSections(sections, info)
	fitted := fitToWidth(rendered, width)

	left := joinRendered(fitted, AlignLeft)
	center := joinRendered(fitted, AlignCenter)
	right := joinRendered(fitted, AlignRight)

	return composeLine(left, center, right, width, th)
}

// ---------------------------------------------------------------------------
// Rendering pipeline
// ---------------------------------------------------------------------------

// renderedSection holds the output of a section's content function along
// with its metadata for layout.
type renderedSection struct {
	name      string
	text      string
	width     int
	priority  int
	alignment SectionAlignment
}

// renderSections evaluates every section's content function and measures
// the output width.
func renderSections(sections []Section, info StatusInfo) []renderedSection {
	out := make([]renderedSection, 0, len(sections))
	for _, sec := range sections {
		text := sec.Content(info)
		if text == "" {
			continue
		}
		out = append(out, renderedSection{
			name:      sec.Name,
			text:      text,
			width:     lipgloss.Width(text),
			priority:  sec.Priority,
			alignment: sec.Alignment,
		})
	}
	return out
}

// fitToWidth drops the lowest-priority sections (highest numeric priority
// value) until the total width fits.
func fitToWidth(sections []renderedSection, maxWidth int) []renderedSection {
	total := totalWidth(sections, len(sectionSeparator))
	for total > maxWidth && len(sections) > 0 {
		dropIdx := lowestPriorityIndex(sections)
		sections = append(sections[:dropIdx], sections[dropIdx+1:]...)
		total = totalWidth(sections, len(sectionSeparator))
	}
	return sections
}

// totalWidth computes the combined width of all sections including
// separators between adjacent sections of the same alignment.
func totalWidth(sections []renderedSection, sepWidth int) int {
	if len(sections) == 0 {
		return 0
	}
	total := 0
	alignCounts := [3]int{} // indexed by SectionAlignment
	for _, s := range sections {
		total += s.width
		alignCounts[s.alignment]++
	}
	// Add separator widths between adjacent sections of same alignment.
	for _, count := range alignCounts {
		if count > 1 {
			total += (count - 1) * sepWidth
		}
	}
	return total
}

// lowestPriorityIndex returns the index of the section with the highest
// numeric priority value (= least important). Ties are broken by choosing
// the last occurrence.
func lowestPriorityIndex(sections []renderedSection) int {
	idx := 0
	for i, s := range sections {
		if s.priority >= sections[idx].priority {
			idx = i
		}
	}
	return idx
}

// joinRendered concatenates sections belonging to a given alignment with
// separators between them.
func joinRendered(sections []renderedSection, align SectionAlignment) string {
	var parts []string
	for _, s := range sections {
		if s.alignment != align {
			continue
		}
		parts = append(parts, s.text)
	}
	return strings.Join(parts, sectionSeparator)
}

// composeLine places left, center, and right groups into a single line
// padded to width and styled with the theme's status bar style.
func composeLine(left, center, right string, width int, th *theme.Theme) string {
	leftW := lipgloss.Width(left)
	centerW := lipgloss.Width(center)
	rightW := lipgloss.Width(right)

	// When there is no center content, just pad between left and right.
	if centerW == 0 {
		pad := max(width-leftW-rightW, 0)
		content := left + strings.Repeat(" ", pad) + right
		return th.StatusBar.Width(width).Render(content)
	}

	// Center the center section between left and right.
	halfGap := max((width-centerW)/2, 0)
	leftPad := max(halfGap-leftW, 0)
	rightPad := max(width-leftW-leftPad-centerW-rightW, 0)

	var b strings.Builder
	b.Grow(width)
	b.WriteString(left)
	b.WriteString(strings.Repeat(" ", leftPad))
	b.WriteString(center)
	b.WriteString(strings.Repeat(" ", max(rightPad, 0)))
	b.WriteString(right)

	return th.StatusBar.Width(width).Render(b.String())
}
