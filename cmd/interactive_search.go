// Package cmd provides CLI commands for the Sylk application.
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// =============================================================================
// Interactive Search Constants
// =============================================================================

const (
	// ANSI escape codes for terminal control
	escClearLine    = "\033[2K"
	escClearScreen  = "\033[2J"
	escMoveCursor   = "\033[%d;%dH"
	escMoveUp       = "\033[%dA"
	escMoveDown     = "\033[%dB"
	escHideCursor   = "\033[?25l"
	escShowCursor   = "\033[?25h"
	escBold         = "\033[1m"
	escDim          = "\033[2m"
	escReset        = "\033[0m"
	escReverse      = "\033[7m"
	escCyan         = "\033[36m"
	escYellow       = "\033[33m"
	escGreen        = "\033[32m"
	escMagenta      = "\033[35m"

	// Key codes
	keyEnter     = 13
	keyEscape    = 27
	keyBackspace = 127
	keyCtrlC     = 3
	keyCtrlD     = 4
	keyTab       = 9

	// Display settings
	maxResults       = 10
	maxPreviewLines  = 5
	maxPreviewWidth  = 80
	searchDebounceMs = 150
)

// =============================================================================
// Interactive Search Types
// =============================================================================

// InteractiveSearcher provides a terminal-based interactive search interface.
type InteractiveSearcher struct {
	searcher    Searcher
	results     []search.ScoredDocument
	selected    int
	query       string
	running     bool
	termWidth   int
	termHeight  int
	stdin       io.Reader
	stdout      io.Writer
	oldState    *term.State
	lastSearch  time.Time
	showPreview bool
}

// Searcher is the interface for performing searches.
// This allows for dependency injection and testing.
type Searcher interface {
	Search(ctx context.Context, req search.SearchRequest) (*search.SearchResult, error)
}

// MockSearcher is a simple mock implementation for testing.
type MockSearcher struct {
	Results []search.ScoredDocument
	Err     error
}

// Search implements the Searcher interface.
func (m *MockSearcher) Search(ctx context.Context, req search.SearchRequest) (*search.SearchResult, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	filtered := make([]search.ScoredDocument, 0)
	for _, doc := range m.Results {
		if strings.Contains(strings.ToLower(doc.Path), strings.ToLower(req.Query)) ||
			strings.Contains(strings.ToLower(doc.Content), strings.ToLower(req.Query)) {
			filtered = append(filtered, doc)
		}
	}
	return &search.SearchResult{
		Documents:  filtered,
		TotalHits:  int64(len(filtered)),
		SearchTime: time.Millisecond * 10,
		Query:      req.Query,
	}, nil
}

// =============================================================================
// Interactive Search Command
// =============================================================================

var interactiveSearchCmd = &cobra.Command{
	Use:   "isearch",
	Short: "Interactive search mode",
	Long: `Launch an interactive terminal-based search interface.

Features:
  - Real-time search as you type
  - Arrow keys for navigation
  - Enter to select a result
  - Tab to toggle file preview
  - Escape or 'q' to quit

Controls:
  Up/Down arrows   Navigate results
  Enter            Select result (print path)
  Tab              Toggle file preview
  Escape/q         Quit
  Ctrl+C/Ctrl+D    Force quit`,
	RunE: runInteractiveSearch,
}

var isearchPreview bool

func init() {
	rootCmd.AddCommand(interactiveSearchCmd)
	interactiveSearchCmd.Flags().BoolVarP(&isearchPreview, "preview", "p", true, "Show file preview")
}

func runInteractiveSearch(cmd *cobra.Command, args []string) error {
	// For now, use a mock searcher since we don't have a real search service yet
	// In production, this would be replaced with the actual search service
	mockSearcher := &MockSearcher{
		Results: createMockResults(),
	}

	searcher := NewInteractiveSearcher(mockSearcher, os.Stdin, os.Stdout)
	searcher.showPreview = isearchPreview
	return searcher.Run()
}

func createMockResults() []search.ScoredDocument {
	// Create sample mock results for demonstration
	return []search.ScoredDocument{
		{
			Document: search.Document{
				ID:       "1",
				Path:     "core/search/document.go",
				Type:     search.DocTypeSourceCode,
				Language: "go",
				Content:  "// Package search provides document types...\ntype Document struct {\n    ID string\n    Path string\n}",
			},
			Score: 0.95,
		},
		{
			Document: search.Document{
				ID:       "2",
				Path:     "core/search/result.go",
				Type:     search.DocTypeSourceCode,
				Language: "go",
				Content:  "// SearchResult contains the results of a search operation.\ntype SearchResult struct {\n    Documents []ScoredDocument\n}",
			},
			Score: 0.87,
		},
		{
			Document: search.Document{
				ID:       "3",
				Path:     "cmd/root.go",
				Type:     search.DocTypeSourceCode,
				Language: "go",
				Content:  "package cmd\n\nimport \"github.com/spf13/cobra\"\n\nvar rootCmd = &cobra.Command{}",
			},
			Score: 0.75,
		},
	}
}

// =============================================================================
// InteractiveSearcher Constructor
// =============================================================================

// NewInteractiveSearcher creates a new interactive searcher.
func NewInteractiveSearcher(searcher Searcher, stdin io.Reader, stdout io.Writer) *InteractiveSearcher {
	return &InteractiveSearcher{
		searcher:    searcher,
		results:     make([]search.ScoredDocument, 0),
		selected:    0,
		query:       "",
		running:     false,
		stdin:       stdin,
		stdout:      stdout,
		showPreview: true,
	}
}

// =============================================================================
// Main Run Loop
// =============================================================================

// Run starts the interactive search mode.
func (s *InteractiveSearcher) Run() error {
	// Check if stdin is a terminal
	stdinFd, ok := s.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(stdinFd.Fd())) {
		return fmt.Errorf("interactive search requires a terminal")
	}

	// Get terminal dimensions
	width, height, err := term.GetSize(int(stdinFd.Fd()))
	if err != nil {
		s.termWidth = 80
		s.termHeight = 24
	} else {
		s.termWidth = width
		s.termHeight = height
	}

	// Set terminal to raw mode
	oldState, err := term.MakeRaw(int(stdinFd.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	s.oldState = oldState
	defer s.restore()

	s.running = true
	s.hideCursor()
	s.clearScreen()
	s.render()

	return s.readInput()
}

// restore restores terminal state.
func (s *InteractiveSearcher) restore() {
	s.showCursor()
	if s.oldState != nil {
		if stdin, ok := s.stdin.(*os.File); ok {
			term.Restore(int(stdin.Fd()), s.oldState)
		}
	}
}

// =============================================================================
// Input Handling
// =============================================================================

// readInput reads and processes keyboard input.
func (s *InteractiveSearcher) readInput() error {
	reader := bufio.NewReader(s.stdin)

	for s.running {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				s.running = false
				break
			}
			return err
		}

		if err := s.handleKey(b, reader); err != nil {
			return err
		}
	}

	return nil
}

// handleKey processes a single key press.
func (s *InteractiveSearcher) handleKey(b byte, reader *bufio.Reader) error {
	switch b {
	case keyCtrlC, keyCtrlD:
		s.running = false
		return nil

	case keyEscape:
		return s.handleEscapeSequence(reader)

	case keyEnter:
		s.selectResult()
		return nil

	case keyBackspace:
		s.handleBackspace()
		s.performSearch()
		s.render()
		return nil

	case keyTab:
		s.showPreview = !s.showPreview
		s.render()
		return nil

	case 'q':
		if s.query == "" {
			s.running = false
			return nil
		}
		s.handleCharacter(b)
		return nil

	default:
		if b >= 32 && b < 127 {
			s.handleCharacter(b)
		}
		return nil
	}
}

// handleEscapeSequence processes escape sequences (arrow keys, etc).
func (s *InteractiveSearcher) handleEscapeSequence(reader *bufio.Reader) error {
	// Check for escape sequence
	b1, err := reader.ReadByte()
	if err != nil {
		s.running = false
		return nil
	}

	if b1 != '[' {
		// Just escape key pressed
		s.running = false
		return nil
	}

	b2, err := reader.ReadByte()
	if err != nil {
		return nil
	}

	switch b2 {
	case 'A': // Up arrow
		s.moveUp()
		s.render()
	case 'B': // Down arrow
		s.moveDown()
		s.render()
	case 'C': // Right arrow - could be used for preview
	case 'D': // Left arrow - could be used for preview
	}

	return nil
}

// handleCharacter processes a printable character.
func (s *InteractiveSearcher) handleCharacter(b byte) {
	s.query += string(b)
	s.performSearch()
	s.render()
}

// handleBackspace handles backspace key.
func (s *InteractiveSearcher) handleBackspace() {
	if len(s.query) > 0 {
		s.query = s.query[:len(s.query)-1]
	}
}

// =============================================================================
// Navigation
// =============================================================================

// moveUp moves selection up.
func (s *InteractiveSearcher) moveUp() {
	if s.selected > 0 {
		s.selected--
	}
}

// moveDown moves selection down.
func (s *InteractiveSearcher) moveDown() {
	if s.selected < len(s.results)-1 && s.selected < maxResults-1 {
		s.selected++
	}
}

// selectResult selects the current result and exits.
func (s *InteractiveSearcher) selectResult() {
	s.running = false
	if len(s.results) > 0 && s.selected < len(s.results) {
		s.clearScreen()
		s.showCursor()
		fmt.Fprintln(s.stdout, s.results[s.selected].Path)
	}
}

// =============================================================================
// Search
// =============================================================================

// performSearch executes the search with the current query.
func (s *InteractiveSearcher) performSearch() {
	// Debounce search
	now := time.Now()
	if now.Sub(s.lastSearch) < time.Millisecond*searchDebounceMs {
		return
	}
	s.lastSearch = now

	if s.query == "" {
		s.results = nil
		s.selected = 0
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	req := search.SearchRequest{
		Query:             s.query,
		Limit:             maxResults,
		IncludeHighlights: true,
	}

	result, err := s.searcher.Search(ctx, req)
	if err != nil {
		s.results = nil
		return
	}

	s.results = result.Documents
	s.selected = 0
}

// =============================================================================
// Rendering
// =============================================================================

// render draws the current UI state.
func (s *InteractiveSearcher) render() {
	s.moveCursor(1, 1)
	s.clearScreen()

	// Render header
	s.renderHeader()

	// Render search input
	s.renderSearchInput()

	// Render results
	s.renderResults()

	// Render preview if enabled
	if s.showPreview && len(s.results) > 0 {
		s.renderPreview()
	}

	// Render footer
	s.renderFooter()
}

// renderHeader renders the header line.
func (s *InteractiveSearcher) renderHeader() {
	header := fmt.Sprintf("%s%s Sylk Interactive Search %s", escBold, escCyan, escReset)
	fmt.Fprintln(s.stdout, header)
	fmt.Fprintln(s.stdout, strings.Repeat("-", min(s.termWidth, 60)))
}

// renderSearchInput renders the search input line.
func (s *InteractiveSearcher) renderSearchInput() {
	fmt.Fprintf(s.stdout, "%s> %s%s%s\r\n", escYellow, escReset, s.query, escReset)
	fmt.Fprintln(s.stdout)
}

// renderResults renders the search results list.
func (s *InteractiveSearcher) renderResults() {
	if len(s.results) == 0 {
		if s.query != "" {
			fmt.Fprintf(s.stdout, "%sNo results found%s\r\n", escDim, escReset)
		} else {
			fmt.Fprintf(s.stdout, "%sType to search...%s\r\n", escDim, escReset)
		}
		return
	}

	displayCount := min(len(s.results), maxResults)
	for i := 0; i < displayCount; i++ {
		s.renderResultItem(i)
	}

	if len(s.results) > maxResults {
		fmt.Fprintf(s.stdout, "%s... and %d more%s\r\n", escDim, len(s.results)-maxResults, escReset)
	}
}

// renderResultItem renders a single result item.
func (s *InteractiveSearcher) renderResultItem(index int) {
	doc := s.results[index]
	prefix := "  "
	style := ""

	if index == s.selected {
		prefix = "> "
		style = escReverse
	}

	// Format the result line
	path := truncateString(doc.Path, s.termWidth-20)
	score := fmt.Sprintf("%.2f", doc.Score)
	docType := string(doc.Type)

	line := fmt.Sprintf("%s%s%s [%s] %s (score: %s)%s",
		prefix, style, path, docType[:min(len(docType), 6)], escDim, score, escReset)

	fmt.Fprintln(s.stdout, line+"\r")
}

// renderPreview renders a preview of the selected document.
func (s *InteractiveSearcher) renderPreview() {
	if s.selected >= len(s.results) {
		return
	}

	doc := s.results[s.selected]

	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "%s%s Preview: %s %s\r\n", escBold, escMagenta, doc.Path, escReset)
	fmt.Fprintln(s.stdout, strings.Repeat("-", min(s.termWidth, maxPreviewWidth))+"\r")

	// Show content preview
	lines := strings.Split(doc.Content, "\n")
	displayLines := min(len(lines), maxPreviewLines)

	for i := 0; i < displayLines; i++ {
		line := lines[i]
		if len(line) > maxPreviewWidth {
			line = line[:maxPreviewWidth-3] + "..."
		}
		fmt.Fprintf(s.stdout, "%s%d: %s%s\r\n", escGreen, i+1, escReset, line)
	}

	if len(lines) > maxPreviewLines {
		fmt.Fprintf(s.stdout, "%s... %d more lines%s\r\n", escDim, len(lines)-maxPreviewLines, escReset)
	}
}

// renderFooter renders the footer with help information.
func (s *InteractiveSearcher) renderFooter() {
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "%s[Up/Down: Navigate] [Enter: Select] [Tab: Toggle Preview] [Esc/q: Quit]%s\r\n",
		escDim, escReset)
}

// =============================================================================
// Terminal Control Helpers
// =============================================================================

func (s *InteractiveSearcher) clearScreen() {
	fmt.Fprint(s.stdout, escClearScreen)
}

func (s *InteractiveSearcher) clearLine() {
	fmt.Fprint(s.stdout, escClearLine)
}

func (s *InteractiveSearcher) moveCursor(row, col int) {
	fmt.Fprintf(s.stdout, escMoveCursor, row, col)
}

func (s *InteractiveSearcher) hideCursor() {
	fmt.Fprint(s.stdout, escHideCursor)
}

func (s *InteractiveSearcher) showCursor() {
	fmt.Fprint(s.stdout, escShowCursor)
}

// =============================================================================
// Utility Functions
// =============================================================================

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
