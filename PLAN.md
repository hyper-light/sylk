# LSP Tier 3 Feature Plan

Six features, implemented in order. Each follows the established 4-layer pattern:
`protocol.go` → `client.go` → `manager.go` → `app.go` / editor UI.

---

## Item 1: Document Highlight

**What:** When the cursor rests on a symbol, all other occurrences of that symbol in the visible file are highlighted with a subtle background. Clears when the cursor moves to a non-symbol position.

**LSP method:** `textDocument/documentHighlight`

### 1A. Protocol + Types (`core/lsp/`)

- `protocol.go`: Add `MethodDocumentHighlight = "textDocument/documentHighlight"`
- `types.go`: Add `DocumentHighlightKind` (Text=1, Read=2, Write=3) and `DocumentHighlight` struct (`Range`, `Kind`)
- `protocol.go`: Add `ProtocolDocumentHighlight` wire type and `ToDocumentHighlights()` converter
- `client.go` `initialize()`: Declare `documentHighlight: {dynamicRegistration: false}` in client capabilities

### 1B. Client + Manager

- `client.go`: Add `DocumentHighlight(ctx, filePath, line, col) ([]DocumentHighlight, error)` — same pattern as `Hover()` but parses `[]DocumentHighlight`
- `manager.go`: Add `DocumentHighlight(ctx, projectRoot, filePath, line, col) ([]DocumentHighlight, error)` wrapper

### 1C. Messages (`ui/msg/`)

- `msg.go`: Add `LSPDocumentHighlightMsg { FilePath string; Line, Col int; Highlights []lsp.DocumentHighlight; Err error }`

### 1D. Editor integration (`ui/editor/`)

- `model.go`: Add `highlightRanges []lsp.DocumentHighlight` field; `SetHighlightRanges(ranges)` setter; `ClearHighlightRanges()` method
- Rendering: In `renderVisibleLines`, convert `highlightRanges` to display-column spans per line and render with a subtle background style (`theme.Palette.Selection`)
- Clear on cursor move: In `handleKeyMsg`, after cursor movement, if cursor position changed, clear highlights

### 1E. App wiring (`ui/app.go`)

- Add `lspDocumentHighlightCmd(filePath, line, col) tea.Cmd` — calls `m.lspManager.DocumentHighlight()`
- On cursor movement in normal mode (after `propagateToFocused`): fire debounced highlight request (reuse hover debounce pattern, ~250ms)
- On `LSPDocumentHighlightMsg`: if filePath matches, call `m.inlineEditor.SetHighlightRanges()`

### Verification

1. Open a Go file in edit mode
2. Place cursor on any variable/function name
3. After ~250ms, all occurrences of that symbol in the visible area should get a subtle background highlight
4. Move cursor to whitespace → highlights clear
5. Move cursor to a different symbol → highlights update to new symbol

---

## Item 2: Find References

**What:** `gr` in normal mode (or ex command `:references`) shows all locations in the project that reference the symbol under the cursor. Results appear in a references panel (reuse search overlay or a quickfix-style list).

**LSP method:** `textDocument/references`

### 2A. Protocol + Types

- `protocol.go`: Add `MethodReferences = "textDocument/references"`
- `types.go`: Add `ReferenceParams` (extends `TextDocumentPositionParams` with `ReferenceContext { IncludeDeclaration bool }`)
- Reuses existing `Location` type for results

### 2B. Client + Manager

- `client.go`: Add `References(ctx, filePath, line, col, includeDeclaration) ([]Location, error)` — same pattern as `Definition()`
- `manager.go`: Add `References(ctx, projectRoot, filePath, line, col, includeDeclaration) ([]Location, error)` wrapper

### 2C. Messages

- `msg.go`: Add `LSPReferencesRequestMsg { FilePath string; Line, Col int }`
- `msg.go`: Add `LSPReferencesMsg { FilePath string; Line, Col int; Locations []lsp.Location; Err error }`

### 2D. References panel (`ui/references/`)

- New package `ui/references/` with a `Model` that displays a scrollable list of locations
- Each entry: `filepath:line: <line content preview>`
- Enter on an entry → opens file at that location (emits `OpenEditorMsg` or navigates)
- Esc → closes the panel
- Render as an overlay or in the search overlay slot

### 2E. Editor + App wiring

- `mode/normal.go`: Add `OpGotoReferences` to `OperatorType`; map `gr` sequence to it
- `model.go`: Handle `StandaloneResult` with `OpGotoReferences` → emit `LSPReferencesRequestMsg`
- `app.go`: Add `lspReferencesCmd` → route response to references panel
- `app.go`: On `LSPReferencesMsg` → populate and show references panel
- On entry selection → navigate to file:line (same as go-to-definition flow)

### Verification

1. Open a Go file, place cursor on a function name
2. Press `gr` → references panel opens showing all call sites
3. Navigate with j/k, press Enter → jumps to that reference location
4. Press Esc → panel closes, returns to previous position
5. Works across files (references in other files show full paths)

---

## Item 3: Document Symbols

**What:** Ex command `:symbols` (or keybind) opens a filterable outline of the current file — functions, types, variables, constants — with fuzzy search. Selecting an entry jumps to that symbol.

**LSP method:** `textDocument/documentSymbol`

### 3A. Protocol + Types

- `protocol.go`: Add `MethodDocumentSymbol = "textDocument/documentSymbol"`
- `types.go`: Add `SymbolKind` constants (File=1, Module=2, ..., Function=12, Variable=13, etc.)
- `types.go`: Add `DocumentSymbol` struct (`Name`, `Detail`, `Kind`, `Range`, `SelectionRange`, `Children []DocumentSymbol`)
- `types.go`: Add `SymbolInformation` struct (flat variant: `Name`, `Kind`, `Location`) for servers that return the flat format
- `protocol.go`: Add converter that normalizes both response formats to `[]DocumentSymbol`

### 3B. Client + Manager

- `client.go`: Add `DocumentSymbol(ctx, filePath) ([]DocumentSymbol, error)`
- `manager.go`: Add `DocumentSymbol(ctx, projectRoot, filePath) ([]DocumentSymbol, error)` wrapper

### 3C. Messages

- `msg.go`: Add `LSPDocumentSymbolMsg { FilePath string; Symbols []lsp.DocumentSymbol; Err error }`

### 3D. Symbols overlay (`ui/symbols/`)

- New package `ui/symbols/` with a `Model` — filterable list overlay
- Input field at top for fuzzy filtering
- Each entry: icon (based on SymbolKind) + symbol name + detail + line number
- Flat list with indentation for children (structs show fields indented)
- Enter → jump to symbol's `SelectionRange.Start`
- Esc → close overlay
- Render in the overlay slot (like search overlay)

### 3E. App wiring

- `app.go`: Add `:symbols` to ex command handler
- `app.go`: Add `lspDocumentSymbolCmd` → route to symbols overlay
- On entry selection → navigate inline editor to that line

### Verification

1. Open a Go file in edit mode
2. Type `:symbols` → symbols overlay opens showing all functions, types, etc.
3. Type to filter (e.g., "Handle" → shows only matching symbols)
4. Navigate with j/k or arrow keys, press Enter → jumps to that symbol
5. Press Esc → overlay closes

---

## Item 4: Signature Help

**What:** When typing inside function call parentheses, a small popup shows the function signature with the current parameter highlighted. Updates as the cursor moves between parameters.

**LSP method:** `textDocument/signatureHelp`

### 4A. Protocol + Types

- `protocol.go`: Add `MethodSignatureHelp = "textDocument/signatureHelp"`
- `types.go`: Add `SignatureHelpOptions { TriggerCharacters []string; RetriggerCharacters []string }` — parsed from server capabilities
- `types.go`: Add `SignatureHelp { Signatures []SignatureInformation; ActiveSignature int; ActiveParameter int }`
- `types.go`: Add `SignatureInformation { Label string; Documentation string; Parameters []ParameterInformation }`
- `types.go`: Add `ParameterInformation { Label [2]int or string; Documentation string }`
- `protocol.go`: Parse `signatureHelpProvider` from ServerCaps; store trigger chars on client
- `client.go` `initialize()`: Declare `signatureHelp` in client capabilities

### 4B. Client + Manager

- `client.go`: Add `SignatureHelp(ctx, filePath, line, col) (*SignatureHelp, error)`
- `manager.go`: Add `SignatureHelp(ctx, projectRoot, filePath, line, col) (*SignatureHelp, error)` wrapper
- `manager.go`: Add `SignatureHelpTriggers(projectRoot, filePath) []string` — returns trigger chars

### 4C. Messages

- `msg.go`: Add `LSPSignatureHelpMsg { FilePath string; Line, Col int; Result *lsp.SignatureHelp; Err error }`

### 4D. Signature popup (`ui/editor/signature/`)

- New package or add to existing hover — a one-line popup rendered above the cursor line
- Shows: `funcName(param1 Type, **param2 Type**, param3 Type)` with active param bold/highlighted
- Positioned directly above the cursor, width-capped to editor panel
- Auto-dismiss when cursor leaves the call expression (no `(` on current line before cursor)

### 4E. App wiring

- In insert mode, after each character typed: if the character is a trigger char (`(`, `,`), fire `lspSignatureHelpCmd`
- On `LSPSignatureHelpMsg`: show/update signature popup in editor
- Dismiss when: Esc pressed, `)` typed, mode changes to normal, cursor moves outside call

### Verification

1. Open a Go file, enter insert mode
2. Type `fmt.Println(` → signature popup appears showing `func Println(a ...any) (n int, err error)`
3. Type a string, then `,` → active parameter highlights the next param
4. Type `)` → popup dismisses
5. Works for multi-param functions: `strings.Replace(` shows all 4 params, highlights advance with each `,`

---

## Item 5: Formatting

**What:** `:format` ex command (or `gq` on entire file) formats the current document using the LSP server's formatter. Buffer content is replaced in-place with undo support.

**LSP method:** `textDocument/formatting`

### 5A. Protocol + Types

- `protocol.go`: Add `MethodFormatting = "textDocument/formatting"`
- `types.go`: Add `DocumentFormattingParams { TextDocument TextDocumentIdentifier; Options FormattingOptions }`
- `types.go`: Add `FormattingOptions { TabSize int; InsertSpaces bool }`
- `types.go`: Add `ServerCapabilities.FormattingProvider bool`; parse from `ServerCaps`
- Response is `[]TextEdit` — reuse existing `TextEdit` type or add: `TextEdit { Range Range; NewText string }`

### 5B. Client + Manager

- `client.go`: Add `Format(ctx, filePath, tabSize int, insertSpaces bool) ([]TextEdit, error)`
- `manager.go`: Add `Format(ctx, projectRoot, filePath, tabSize int, insertSpaces bool) ([]TextEdit, error)` wrapper

### 5C. Messages

- `msg.go`: Add `LSPFormatMsg { FilePath string; Edits []lsp.TextEdit; Err error }`

### 5D. Editor integration

- `model.go`: Add `ApplyTextEdits(edits []lsp.TextEdit)` — applies edits in reverse order (bottom-up to preserve positions), records undo entries for each
- Must convert LSP ranges (line:character UTF-16) to buffer positions

### 5E. App wiring

- Add `:format` to ex command handler → fires `lspFormatCmd`
- On `LSPFormatMsg`: call `m.inlineEditor.ApplyTextEdits(edits)`; flash "Formatted" in status bar
- If no edits returned: flash "Already formatted"
- If error: flash error message

### Verification

1. Open a Go file with poor formatting (extra spaces, misaligned fields)
2. Type `:format` → file reformats (gopls applies gofmt + goimports)
3. Undo (`u`) → reverts to pre-format state
4. Works for other languages (e.g., TypeScript with prettier via tsserver)

---

## Item 6: Rename Symbol

**What:** `:rename <newname>` ex command (or `<leader>rn` keybind) renames the symbol under the cursor across all files. Shows a preview of affected files before applying.

**LSP method:** `textDocument/rename` + `textDocument/prepareRename`

### 6A. Protocol + Types

- `protocol.go`: Add `MethodRename = "textDocument/rename"`, `MethodPrepareRename = "textDocument/prepareRename"`
- `types.go`: Add `RenameParams { TextDocument TextDocumentIdentifier; Position ProtocolPosition; NewName string }`
- `types.go`: Add `WorkspaceEdit { Changes map[string][]TextEdit; DocumentChanges []TextDocumentEdit }`
- `types.go`: Add `TextDocumentEdit { TextDocument VersionedTextDocumentIdentifier; Edits []TextEdit }`
- `types.go`: Add `PrepareRenameResult { Range Range; Placeholder string }`
- `client.go` `initialize()`: Declare `rename: { prepareSupport: true }` in client capabilities

### 6B. Client + Manager

- `client.go`: Add `PrepareRename(ctx, filePath, line, col) (*PrepareRenameResult, error)` — checks if rename is valid at position
- `client.go`: Add `Rename(ctx, filePath, line, col, newName) (*WorkspaceEdit, error)`
- `manager.go`: Corresponding wrappers

### 6C. Messages

- `msg.go`: Add `LSPPrepareRenameMsg { FilePath string; Line, Col int; Result *lsp.PrepareRenameResult; Err error }`
- `msg.go`: Add `LSPRenameMsg { FilePath string; Edit *lsp.WorkspaceEdit; Err error }`

### 6D. Workspace edit application

- `app.go`: Add `applyWorkspaceEdit(edit *lsp.WorkspaceEdit) error` — for each affected file:
  1. If file is open in editor: apply edits in-place with undo
  2. If file is not open: read from disk, apply edits, write back
  3. Track modified files for didChange notifications
- Show confirmation dialog (modal overlay) before applying: "Rename X to Y across N files?"

### 6E. App wiring

- Add `:rename <newname>` to ex command handler
- First call `PrepareRename` to validate; if invalid, flash error
- Then call `Rename` with the new name
- On response: show confirmation modal → apply workspace edit → flash "Renamed X to Y in N files"
- Send `didChange` for all modified open documents

### Verification

1. Open a Go file, place cursor on a function name
2. Type `:rename NewFuncName` → confirmation dialog: "Rename OldFunc → NewFuncName in 5 files?"
3. Press Enter → rename applied across all files
4. Check other files — all references updated
5. Undo (`u`) in current file → reverts current file changes
6. Try renaming something that can't be renamed (keyword, etc.) → error flash

---

## Implementation Order

| # | Feature | Key Binding | Complexity |
|---|---------|-------------|------------|
| 1 | Document Highlight | automatic on cursor rest | Low |
| 2 | Find References | `gr` / `:references` | Medium |
| 3 | Document Symbols | `:symbols` | Medium |
| 4 | Signature Help | automatic in insert mode | Medium |
| 5 | Formatting | `:format` | Low |
| 6 | Rename Symbol | `:rename <name>` | High |
