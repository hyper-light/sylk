package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adalundhe/sylk/core/detect"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/editor"
	"github.com/adalundhe/sylk/ui/filetree"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/planview"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *AppModel) handleFocusPanel(fp msg.FocusPanelMsg) tea.Cmd {
	m.focus.SetFocus(fp.Target)
	m.syncFocusState()
	return nil
}

func (m *AppModel) handlePlanUpdate(update msg.PlanUpdateMsg) tea.Cmd {
	// Guard interrupted correlations — drop plan updates for dead requests.
	if update.CorrelationID != "" {
		if _, interrupted := m.interruptedCorrelations[update.CorrelationID]; interrupted {
			return nil
		}
	}
	m.chat.HandlePlanUpdate(update)
	if m.planView != nil {
		comp, cmd := m.planView.Update(update)
		m.planView = comp.(*planview.Model)
		if cmd != nil {
			m.markSlotDirty(compositor.SlotCenter)
			m.viewDirty = true
			return cmd
		}
	}
	m.markSlotDirty(compositor.SlotCenter)
	m.viewDirty = true
	return nil
}

func (m *AppModel) handlePlanViewToggle() tea.Cmd {
	if m.planView == nil {
		return nil
	}
	comp, cmd := m.planView.Update(msg.PlanViewToggleMsg{})
	m.planView = comp.(*planview.Model)
	m.markSlotDirty(compositor.SlotRight)
	m.viewDirty = true
	return cmd
}

func (m *AppModel) handleOpenEditor(o msg.OpenEditorMsg) tea.Cmd {
	comp, cmd := m.editorOverlay.Update(o)
	m.editorOverlay = comp.(*editor.Model)
	m.editorOverlay.SetSize(m.width, m.height)
	m.overlay = overlayEditor

	// Notify LSP that the document was opened (fire-and-forget).
	if o.FilePath != "" {
		lang := detectEditorLanguage(o.FilePath)
		lspCmd := m.lspDidOpenCmd(o.FilePath, lang, o.Content)
		return tea.Batch(cmd, lspCmd)
	}
	return cmd
}

func (m *AppModel) handleLSPDiagnostic(d msg.LSPDiagnosticMsg) tea.Cmd {
	// Forward to all views that render diagnostics.
	m.editorOverlay.Update(d)
	m.focusedEditor().Update(d)
	m.codePanel.SetDiagnostics(d.FilePath, d.Diagnostics)
	return nil
}

// handleLSPServerMissing triggers on-demand installation when a language
// server binary is missing. De-duplicates by server ID.
func (m *AppModel) handleLSPServerMissing(d msg.LSPServerMissingMsg) tea.Cmd {
	sid := lsp.ServerID(d.ServerID)
	if m.lspInstalling[sid] {
		return nil // install already in progress
	}
	m.lspInstalling[sid] = true
	m.statusBar.SetFlash("Installing " + d.ServerName + "…")
	return m.installLSPServerCmd(d)
}

// handleLSPServerInstalled handles the result of an on-demand server install.
// On success, clears the detect cache and re-sends didOpen for the file that
// triggered the install.
func (m *AppModel) handleLSPServerInstalled(d msg.LSPServerInstalledMsg) tea.Cmd {
	delete(m.lspInstalling, lsp.ServerID(d.ServerID))
	if d.Err != nil {
		m.statusBar.SetFlash(d.ServerName + " install failed: " + d.Err.Error())
		return nil
	}
	detect.ClearCache()
	m.statusBar.SetFlash(d.ServerName + " installed")
	return m.lspDidOpenCmd(d.FilePath, d.LanguageID, d.Content)
}

// installLSPServerCmd returns a Cmd that installs a single language server
// binary in the background and reports the result.
func (m *AppModel) installLSPServerCmd(d msg.LSPServerMissingMsg) tea.Cmd {
	appCtx := m.ctx
	return func() tea.Msg {
		installer, err := lsp.NewInstaller()
		if err != nil {
			return msg.LSPServerInstalledMsg{
				ServerID:   d.ServerID,
				ServerName: d.ServerName,
				FilePath:   d.FilePath,
				LanguageID: d.LanguageID,
				Content:    d.Content,
				Err:        err,
			}
		}
		ctx, cancel := context.WithTimeout(appCtx, lsp.ProvisionTimeout)
		defer cancel()
		err = installer.EnsureServer(ctx, lsp.ServerID(d.ServerID))
		return msg.LSPServerInstalledMsg{
			ServerID:   d.ServerID,
			ServerName: d.ServerName,
			FilePath:   d.FilePath,
			LanguageID: d.LanguageID,
			Content:    d.Content,
			Err:        err,
		}
	}
}

// provisionLSPServers kicks off background LSP server detection and installation.
func (m *AppModel) provisionLSPServers() tea.Cmd {
	root := m.config.ProjectRoot
	scope := m.deps.Scope
	return func() tea.Msg {
		installer, err := lsp.NewInstaller()
		if err != nil {
			return msg.LSPProvisionDoneMsg{Err: err}
		}
		_ = scope.Go("lsp.provision", lsp.ProvisionTimeout, func(ctx context.Context) error {
			return installer.Provision(ctx, root)
		})
		return msg.LSPProvisionDoneMsg{}
	}
}

// lspDidOpenCmd returns a Cmd that notifies the LSP manager about a file open.
// If no server is available but one could be installed, returns an
// LSPServerMissingMsg to trigger on-demand installation.
func (m *AppModel) lspDidOpenCmd(filePath, languageID, content string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		err := mgr.NotifyDidOpen(ctx, root, filePath, languageID, content)
		if err != nil {
			return nil
		}
		// SuggestServer returns a disabled-but-installable server for this
		// file type. If NotifyDidOpen started a client successfully the
		// server is enabled and SuggestServer returns nil.
		suggested := mgr.SuggestServer(root, filePath)
		if suggested != nil {
			return msg.LSPServerMissingMsg{
				ServerID:   string(suggested.ID),
				ServerName: suggested.Name,
				FilePath:   filePath,
				LanguageID: languageID,
				Content:    content,
			}
		}
		return nil
	}
}

// lspDidChangeCmd returns a Cmd that sends a didChange notification.
func (m *AppModel) lspDidChangeCmd(filePath, content string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		_ = mgr.NotifyDidChange(ctx, root, filePath, content)
		return nil
	}
}

// lspDidSaveAsync sends a didSave notification as a tracked background goroutine.
func (m *AppModel) lspDidSaveAsync(filePath, content string) {
	if filePath == "" {
		return
	}
	mgr := m.lspManager
	root := m.config.ProjectRoot
	_ = m.deps.Scope.Go("lsp.didSave", lspNotifyTimeout, func(ctx context.Context) error {
		return mgr.NotifyDidSave(ctx, root, filePath, content)
	})
}

// lspDidCloseAsync sends a didClose notification as a tracked background goroutine.
func (m *AppModel) lspDidCloseAsync(filePath string) {
	if filePath == "" {
		return
	}
	mgr := m.lspManager
	root := m.config.ProjectRoot
	_ = m.deps.Scope.Go("lsp.didClose", lspNotifyTimeout, func(ctx context.Context) error {
		return mgr.NotifyDidClose(ctx, root, filePath)
	})
}

// lspReopenCmd returns a Cmd that atomically closes then re-opens a document
// in the LSP client. This avoids the race between async didClose (from
// exitEditMode) and async didOpen (from enterEditMode) where didOpen could
// run first, be silently rejected because the document is still tracked,
// and then didClose removes it — leaving the document permanently untracked.
func (m *AppModel) lspReopenCmd(filePath, languageID, content string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		// Close first so the document tracker drops the stale entry.
		_ = mgr.NotifyDidClose(ctx, root, filePath)

		// Now open — guaranteed to succeed because we just closed.
		err := mgr.NotifyDidOpen(ctx, root, filePath, languageID, content)
		if err != nil {
			return nil
		}

		suggested := mgr.SuggestServer(root, filePath)
		if suggested != nil {
			return msg.LSPServerMissingMsg{
				ServerID:   string(suggested.ID),
				ServerName: suggested.Name,
				FilePath:   filePath,
				LanguageID: languageID,
				Content:    content,
			}
		}
		return nil
	}
}

// lspHoverCmd returns a Cmd that requests hover information from the LSP.
func (m *AppModel) lspHoverCmd(filePath string, line, col int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		result, err := mgr.Hover(ctx, root, filePath, line, col)
		return msg.LSPHoverMsg{
			FilePath: filePath,
			Line:     line,
			Col:      col,
			Result:   result,
			Err:      err,
		}
	}
}

// lspDocumentHighlightCmd returns a Cmd that requests document highlights from the LSP.
func (m *AppModel) lspDocumentHighlightCmd(filePath string, line, col int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		highlights, err := mgr.DocumentHighlight(ctx, root, filePath, line, col)
		return msg.LSPDocumentHighlightMsg{
			FilePath:   filePath,
			Line:       line,
			Col:        col,
			Highlights: highlights,
			Err:        err,
		}
	}
}

// lspDefinitionCmd returns a Cmd that requests definition locations from the LSP.
// When forHover is true, the result decorates the hover tooltip instead of navigating.
func (m *AppModel) lspDefinitionCmd(filePath string, line, col int, forHover bool) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		locs, err := mgr.Definition(ctx, root, filePath, line, col)
		return msg.LSPDefinitionMsg{
			FilePath:  filePath,
			Locations: locs,
			Err:       err,
			ForHover:  forHover,
		}
	}
}

// lspReferencesCmd returns a Cmd that requests all references of the symbol
// at the given position. Results include the declaration and are enriched
// with line preview text by reading from disk.
func (m *AppModel) lspReferencesCmd(filePath string, line, col int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		locs, err := mgr.References(ctx, root, filePath, line, col, true)
		return msg.LSPReferencesMsg{
			FilePath:  filePath,
			Line:      line,
			Col:       col,
			Locations: locs,
			Err:       err,
		}
	}
}

// handleLSPReferences builds reference entries and displays them in the file
// tree panel's references mode.
func (m *AppModel) handleLSPReferences(r msg.LSPReferencesMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("References: " + r.Err.Error())
		return nil
	}
	if len(r.Locations) == 0 {
		m.statusBar.SetFlash("No references found")
		return nil
	}

	symbol := m.focusedEditor().WordAt(r.Line, r.Col)
	entries := m.buildReferenceEntries(r.Locations)
	m.fileTree.SetReferences(symbol, entries)

	m.focus.SetFocus(component.FocusFileTree)
	m.syncFocusState()
	return nil
}

// buildReferenceEntries converts LSP locations to display entries, reading
// line preview text from disk and making paths relative to the project root.
func (m *AppModel) buildReferenceEntries(locs []lsp.Location) []filetree.ReferenceEntry {
	entries := make([]filetree.ReferenceEntry, 0, len(locs))
	for _, loc := range locs {
		absPath := lsp.FileURIToPath(loc.URI)
		relPath, err := filepath.Rel(m.config.ProjectRoot, absPath)
		if err != nil {
			relPath = absPath
		}
		preview := readLineFromFile(absPath, loc.Range.Start.Line)
		entries = append(entries, filetree.ReferenceEntry{
			FilePath: relPath,
			AbsPath:  absPath,
			Line:     loc.Range.Start.Line,
			Col:      loc.Range.Start.Character,
			Preview:  preview,
		})
	}
	return entries
}

// readLineFromFile reads a single line (0-indexed) from a file on disk.
// Returns the trimmed line content, or "" on error.
func readLineFromFile(path string, lineNum int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lineNum < 0 || lineNum >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[lineNum])
}

// ---------------------------------------------------------------------------
// LSP Document Symbols
// ---------------------------------------------------------------------------

// lspDocumentSymbolCmd returns a Cmd that requests document symbols for the
// given file. Results arrive as msg.LSPDocumentSymbolMsg.
func (m *AppModel) lspDocumentSymbolCmd(filePath string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		syms, err := mgr.DocumentSymbol(ctx, root, filePath)
		return msg.LSPDocumentSymbolMsg{
			FilePath: filePath,
			Symbols:  syms,
			Err:      err,
		}
	}
}

// lspFormatCmd sends a textDocument/formatting request to the language server.
// If flushContent is non-empty, a didChange notification is sent first so the
// server formats the latest buffer content.
func (m *AppModel) lspFormatCmd(filePath, flushContent string, editGen int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	tabSize := m.focusedEditor().TabWidth()
	return func() tea.Msg {
		if flushContent != "" {
			_ = mgr.NotifyDidChange(ctx, root, filePath, flushContent)
		}
		edits, err := mgr.Format(ctx, root, filePath, tabSize, false)
		return msg.LSPFormatMsg{
			FilePath:       filePath,
			Edits:          edits,
			Err:            err,
			EditGeneration: editGen,
		}
	}
}

// handleLSPFormat applies formatting edits from the language server.
func (m *AppModel) handleLSPFormat(r msg.LSPFormatMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Format: " + r.Err.Error())
		return nil
	}
	m.statusBar.SetFlash(m.applyFormatEdits(r))
	return nil
}

// applyFormatEdits validates and applies formatting edits, returning a
// status message. Separated from handleLSPFormat to keep CC < 4.
func (m *AppModel) applyFormatEdits(r msg.LSPFormatMsg) string {
	if r.EditGeneration != m.focusedEditor().EditGeneration() {
		return "Format: buffer changed, discarding"
	}
	if len(r.Edits) == 0 {
		return "Already formatted"
	}
	return fmt.Sprintf("Formatted (%d edits)", m.focusedEditor().ApplyTextEdits(r.Edits))
}

// handleLSPDocumentSymbol displays document symbols in the file tree panel.
func (m *AppModel) handleLSPDocumentSymbol(r msg.LSPDocumentSymbolMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Symbols: " + r.Err.Error())
		return nil
	}
	if len(r.Symbols) == 0 {
		m.statusBar.SetFlash("No symbols found")
		return nil
	}
	title := filepath.Base(r.FilePath)
	entries := flattenSymbols(r.Symbols, 0)
	m.fileTree.SetDocumentSymbols(title, r.FilePath, entries)
	m.focus.SetFocus(component.FocusFileTree)
	m.syncFocusState()
	return nil
}

// flattenSymbols recursively converts hierarchical DocumentSymbol trees into a
// flat, depth-annotated SymbolEntry list for display in the file tree panel.
func flattenSymbols(syms []lsp.DocumentSymbol, depth int) []filetree.SymbolEntry {
	var result []filetree.SymbolEntry
	for _, s := range syms {
		result = append(result, filetree.SymbolEntry{
			Name:   s.Name,
			Kind:   s.Kind,
			Line:   s.SelectionRange.Start.Line,
			Col:    s.SelectionRange.Start.Character,
			Detail: s.Detail,
			Depth:  depth,
		})
		result = append(result, flattenSymbols(s.Children, depth+1)...)
	}
	return result
}

// ---------------------------------------------------------------------------
// LSP Rename
// ---------------------------------------------------------------------------

// lspPrepareRenameCmd sends a textDocument/prepareRename request.
func (m *AppModel) lspPrepareRenameCmd(filePath string, line, col int, newName string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		result, err := mgr.PrepareRename(ctx, root, filePath, line, col)
		return msg.LSPPrepareRenameMsg{
			FilePath: filePath,
			Line:     line,
			Col:      col,
			NewName:  newName,
			Result:   result,
			Err:      err,
		}
	}
}

// lspRenameCmd sends a textDocument/rename request.
func (m *AppModel) lspRenameCmd(filePath string, line, col int, newName string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		edit, err := mgr.Rename(ctx, root, filePath, line, col, newName)
		return msg.LSPRenameMsg{
			FilePath: filePath,
			NewName:  newName,
			Edit:     edit,
			Err:      err,
		}
	}
}

// handleLSPPrepareRename processes the prepareRename response and either
// proceeds with the rename or shows an error.
func (m *AppModel) handleLSPPrepareRename(r msg.LSPPrepareRenameMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Rename: " + r.Err.Error())
		return nil
	}
	if r.Result == nil {
		m.statusBar.SetFlash("Cannot rename symbol at cursor")
		return nil
	}
	return m.lspRenameCmd(r.FilePath, r.Line, r.Col, r.NewName)
}

// handleLSPRename applies the workspace edit from a rename response.
func (m *AppModel) handleLSPRename(r msg.LSPRenameMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Rename: " + r.Err.Error())
		return nil
	}
	if r.Edit == nil || len(r.Edit.FileEdits) == 0 {
		m.statusBar.SetFlash("Rename: no changes")
		return nil
	}
	n := m.applyWorkspaceEdit(r.Edit)
	m.statusBar.SetFlash(fmt.Sprintf("Renamed to %s in %d file(s)", r.NewName, n))
	return nil
}

// applyWorkspaceEdit applies a workspace edit across files. Open editor
// buffers are edited in-place (with undo support); other files are
// modified on disk. Returns the number of files changed.
func (m *AppModel) applyWorkspaceEdit(edit *lsp.WorkspaceEdit) int {
	editorPath := m.focusedEditor().FilePath()
	changed := 0
	for path, edits := range edit.FileEdits {
		if path == editorPath {
			m.focusedEditor().ApplyTextEdits(edits)
			changed++
			continue
		}
		if applyEditsToFile(path, edits) {
			changed++
		}
	}
	return changed
}

// applyEditsToFile reads a file, applies text edits, and writes it back.
// Edits are applied in reverse order to preserve earlier offsets.
func applyEditsToFile(path string, edits []lsp.TextEdit) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")

	// Sort edits reverse by position so earlier offsets remain valid.
	sorted := make([]lsp.TextEdit, len(edits))
	copy(sorted, edits)
	slices.SortFunc(sorted, func(a, b lsp.TextEdit) int {
		if a.Range.Start.Line != b.Range.Start.Line {
			return b.Range.Start.Line - a.Range.Start.Line
		}
		return b.Range.Start.Character - a.Range.Start.Character
	})

	for _, edit := range sorted {
		lines = spliceLines(lines, edit)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644) == nil
}

// spliceLines applies a single text edit to a line slice.
func spliceLines(lines []string, edit lsp.TextEdit) []string {
	startLine := edit.Range.Start.Line
	endLine := edit.Range.End.Line
	if startLine >= len(lines) {
		return lines
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	startCol := clampCol(lines[startLine], edit.Range.Start.Character)
	endCol := clampCol(lines[endLine], edit.Range.End.Character)

	prefix := lines[startLine][:startCol]
	suffix := lines[endLine][endCol:]
	replacement := prefix + edit.NewText + suffix

	newLines := strings.Split(replacement, "\n")

	// Splice: replace lines[startLine..endLine] with newLines.
	result := make([]string, 0, len(lines)-((endLine-startLine)+1)+len(newLines))
	result = append(result, lines[:startLine]...)
	result = append(result, newLines...)
	result = append(result, lines[endLine+1:]...)
	return result
}

// clampCol clamps a column index to the valid range for a line.
func clampCol(line string, col int) int {
	if col < 0 {
		return 0
	}
	if col > len(line) {
		return len(line)
	}
	return col
}

// handleHoverDefinition stores the definition symbol and package path on the
// active hover tooltip. If the hover content hasn't arrived yet, stashes
// the info so it can be applied when the hover activates.
func (m *AppModel) handleHoverDefinition(d msg.LSPDefinitionMsg) tea.Cmd {
	if d.Err != nil || len(d.Locations) == 0 {
		return nil
	}
	loc := d.Locations[0]
	filePath := lsp.FileURIToPath(loc.URI)

	// When hover is for preview, use the preview panel for word lookup.
	var word string
	if m.hoverForPreview {
		word = m.previewPanel.WordAt(m.hoverMouseLine, m.hoverMouseCol)
	} else {
		word = m.focusedEditor().WordAt(m.hoverMouseLine, m.hoverMouseCol)
	}

	pkgName, pkgPath := defPackageInfo(filePath, m.config.ProjectRoot)
	symbol := word
	if pkgName != "" && word != "" {
		symbol = pkgName + "." + word
	}

	if m.hoverForPreview {
		if m.previewPanel.HoverActive() {
			m.previewPanel.SetHoverDefinition(symbol, pkgPath)
		} else {
			m.pendingHoverSymbol = symbol
			m.pendingHoverPkgPath = pkgPath
		}
		return nil
	}

	if m.focusedEditor().HoverActive() {
		m.focusedEditor().SetHoverDefinition(symbol, pkgPath)
	} else {
		// Hover content hasn't arrived yet; stash for when it does.
		m.pendingHoverSymbol = symbol
		m.pendingHoverPkgPath = pkgPath
	}
	return nil
}

// handlePreviewHoverMsg processes an LSP hover response for the preview pane.
func (m *AppModel) handlePreviewHoverMsg(h msg.LSPHoverMsg) tea.Cmd {
	// Staleness check using preview panel methods.
	wordStart, _ := m.previewPanel.WordBoundsAt(h.Line, h.Col)
	if h.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
		return nil
	}
	if h.Err != nil || h.Result == nil || h.FilePath != m.previewPanel.FilePath() {
		return nil
	}
	m.previewPanel.ShowHover(h.Result.Contents, h.Line, h.Col)
	// Apply definition info that arrived before the hover content.
	if m.previewPanel.HoverActive() && m.pendingHoverSymbol != "" {
		m.previewPanel.SetHoverDefinition(m.pendingHoverSymbol, m.pendingHoverPkgPath)
		m.pendingHoverSymbol = ""
		m.pendingHoverPkgPath = ""
	}
	return nil
}

// defPackageInfo extracts a Go package name and clean display path from a
// definition file path. Strips module versions and GOROOT/GOMODCACHE prefixes.
func defPackageInfo(filePath, projectRoot string) (pkgName, displayPath string) {
	// Local project file.
	if rel, err := filepath.Rel(projectRoot, filePath); err == nil && !strings.HasPrefix(rel, "..") {
		dir := filepath.Dir(rel)
		if dir == "." {
			dir = filepath.Base(projectRoot)
		}
		return filepath.Base(dir), dir
	}

	// Go module cache: .../pkg/mod/github.com/foo/bar@v1.0.0/pkg/file.go
	if idx := strings.Index(filePath, "/pkg/mod/"); idx >= 0 {
		modRel := filePath[idx+len("/pkg/mod/"):]
		dir := filepath.Dir(modRel)
		dir = stripModVersion(dir)
		return filepath.Base(dir), dir
	}

	// Go standard library: .../go/src/context/context.go
	if idx := strings.Index(filePath, "/src/"); idx >= 0 {
		srcRel := filePath[idx+len("/src/"):]
		dir := filepath.Dir(srcRel)
		return filepath.Base(dir), dir
	}

	// Fallback.
	dir := filepath.Dir(filePath)
	return filepath.Base(dir), dir
}

// stripModVersion removes @version suffixes from Go module path components.
func stripModVersion(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if at := strings.Index(p, "@"); at >= 0 {
			parts[i] = p[:at]
		}
	}
	return strings.Join(parts, "/")
}

// lspCompletionCmd returns a Cmd that requests completion items from the LSP.
// If flushContent is non-empty, a didChange notification is sent first so
// the server sees the latest buffer content before the request arrives.
func (m *AppModel) lspCompletionCmd(filePath string, line, col int, flushContent string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		if flushContent != "" {
			_ = mgr.NotifyDidChange(ctx, root, filePath, flushContent)
		}
		items, err := mgr.Completion(ctx, root, filePath, line, col)
		return msg.LSPCompletionMsg{
			FilePath: filePath,
			Items:    items,
			Err:      err,
		}
	}
}

// lspSignatureHelpCmd returns a Cmd that requests signature help from the LSP.
// If flushContent is non-empty, a didChange notification is sent first so
// the server sees the trigger character before the request arrives.
func (m *AppModel) lspSignatureHelpCmd(filePath string, line, col int, flushContent string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		if flushContent != "" {
			_ = mgr.NotifyDidChange(ctx, root, filePath, flushContent)
		}
		result, err := mgr.SignatureHelp(ctx, root, filePath, line, col)
		return msg.LSPSignatureHelpMsg{
			FilePath: filePath,
			Line:     line,
			Col:      col,
			Result:   result,
			Err:      err,
		}
	}
}

// extToLSPLanguage maps file extensions to LSP language identifiers.
var extToLSPLanguage = map[string]string{
	".go":   "go",
	".ts":   "typescript",
	".tsx":  "typescriptreact",
	".js":   "javascript",
	".jsx":  "javascriptreact",
	".py":   "python",
	".rs":   "rust",
	".c":    "c",
	".h":    "c",
	".cpp":  "cpp",
	".hpp":  "cpp",
	".rb":   "ruby",
	".java": "java",
	".yaml": "yaml",
	".yml":  "yaml",
	".tf":   "terraform",
	".lua":  "lua",
	".zig":  "zig",
	".ml":   "ocaml",
}

// isMarkdownFile reports whether path has a markdown extension.
func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// detectEditorLanguage returns the LSP language ID for a file path.
func detectEditorLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := extToLSPLanguage[ext]; ok {
		return lang
	}
	return ""
}

// handleFileOpen reads a file from disk and displays it in the code viewer.
// Always marks the file as active in the tree regardless of read success so
// the explorer highlights it immediately.
func (m *AppModel) handleFileOpen(o msg.FileOpenMsg) tea.Cmd {
	content, ok := m.readOpenedFile(o)
	if !ok {
		return nil
	}
	lspContent := content
	if m.viewMode == ViewEdit {
		if m.focusedEditor().FilePath() == o.Path {
			m.focusOpenedFile(o)
			m.moveToFileOpenLocation(o)
			return nil
		}
		if m.focusOpenFileInExistingPane(o) {
			m.moveToFileOpenLocation(o)
			return nil
		}
		lspContent = m.openFileInFocusedEditor(o, content)
	}
	m.moveToFileOpenLocation(o)
	m.syncEditorWarpLines()
	return m.lspDidOpenCmd(o.Path, detectEditorLanguage(o.Path), lspContent)
}

func (m *AppModel) readOpenedFile(o msg.FileOpenMsg) (string, bool) {
	m.fileTree.SetActiveFile(o.Path)
	m.fileTree.RevealPath(o.Path)
	data, err := os.ReadFile(o.Path)
	if err != nil {
		m.statusBar.SetFlash("Cannot open: " + o.Name)
		return "", false
	}
	content := string(data)
	m.codePanel.SetContent(content, o.Path, o.Language)
	return content, true
}

func (m *AppModel) focusOpenedFile(o msg.FileOpenMsg) {
	m.appendTab(o.Path)
	m.focusCodePanel()
	m.syncFocusState()
}

func (m *AppModel) focusOpenFileInExistingPane(o msg.FileOpenMsg) bool {
	pid, _ := m.findPaneWithTab(o.Path)
	if pid == 0 || pid == m.focusedPane {
		return false
	}
	m.focusedPane = pid
	m.focusCodePanel()
	m.syncFocusState()
	m.switchPaneToOpenFile(pid, o.Path)
	return true
}

func (m *AppModel) switchPaneToOpenFile(pid pane.PaneID, path string) {
	ps := m.paneEditors[pid]
	for i, tabPath := range ps.tabOrder {
		if tabPath == path {
			m.switchToTab(i)
			return
		}
	}
}

func (m *AppModel) openFileInFocusedEditor(o msg.FileOpenMsg, content string) string {
	if oldPath := m.focusedEditor().FilePath(); oldPath != "" {
		m.lspDidCloseAsync(oldPath)
	}
	m.detachCurrentEditor()
	m.appendTab(o.Path)
	m.resizeInlineEditor()
	if m.restoreFromCache(o.Path) {
		m.focusCodePanel()
		m.syncFocusState()
		return m.focusedEditor().Content()
	}
	m.focusedEditor().OpenFile(o.Path, content, o.Language)
	m.focusCodePanel()
	m.syncFocusState()
	return content
}

func (m *AppModel) moveToFileOpenLocation(o msg.FileOpenMsg) {
	if o.Line <= 0 {
		return
	}
	targetLine := o.Line - 1
	m.codePanel.ScrollToLine(targetLine)
	if m.viewMode != ViewEdit {
		return
	}
	if o.CursorCol > 0 {
		m.focusedEditor().GoToLineCol(targetLine, o.CursorCol)
	} else {
		m.focusedEditor().GoToLine(targetLine)
	}
	m.setJumpMarker(targetLine, o.Col, o.EndCol)
}

// setJumpMarker activates the visual jump marker on the inline editor when
// column info is available. When endCol is 0, derives bounds from the word
// at the given column.
func (m *AppModel) setJumpMarker(line, col, endCol int) {
	if col <= 0 && endCol <= 0 {
		return
	}
	startCol, ec := col, endCol
	if ec <= startCol {
		startCol, ec = m.focusedEditor().WordBoundsAt(line, col)
	}
	if ec > startCol {
		m.focusedEditor().SetJumpMarker(line, startCol, ec)
	}
}

func (m *AppModel) handleCloseEditor() tea.Cmd {
	comp, cmd := m.editorOverlay.Update(msg.CloseEditorMsg{})
	m.editorOverlay = comp.(*editor.Model)
	m.overlay = overlayNone
	return cmd
}

// handleFileReplaced reloads the editor buffer when a multi-file replace
// modified the currently open file on disk (only if there are no unsaved changes).
func (m *AppModel) handleFileReplaced(r msg.FileReplacedMsg) tea.Cmd {
	if m.viewMode != ViewEdit || m.focusedEditor().FilePath() != r.Path {
		return nil
	}
	if m.focusedEditor().Modified() {
		return nil
	}
	return m.reloadEditorFromDisk(r.Path)
}

// handleMultiFileReplaceDone flashes a status message after multi-file replace-all.
func (m *AppModel) handleMultiFileReplaceDone(r msg.MultiFileReplaceDoneMsg) tea.Cmd {
	status := fmt.Sprintf("Replaced %d occurrences in %d files", r.TotalReplaced, r.FilesChanged)
	if r.Skipped > 0 {
		status += fmt.Sprintf(" (%d skipped)", r.Skipped)
	}
	m.statusBar.SetFlash(status)
	return nil
}

// reloadEditorFromDisk reads the file from disk and reloads the editor buffer.
func (m *AppModel) reloadEditorFromDisk(path string) tea.Cmd {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	lang := detectEditorLanguage(path)
	m.focusedEditor().OpenFile(path, content, lang)
	m.codePanel.SetContent(content, path, lang)
	m.editorCache.Delete(path)
	return nil
}
