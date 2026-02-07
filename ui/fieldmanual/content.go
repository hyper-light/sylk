package fieldmanual

// Binding describes a single keyboard shortcut or command.
type Binding struct {
	Key           string // Display string for the key/chord.
	Description   string // What the command does.
	Prerequisites string // Required context ("" if always available).
}

// Section groups related bindings under a heading.
type Section struct {
	Title    string
	Bindings []Binding
}

// allSections returns the complete field manual content organized by section.
// This is static, compile-time data.
func allSections() []Section {
	return []Section{
		sectionGlobalShortcuts(),
		sectionChords(),
		sectionEditMode(),
		sectionVimNormal(),
		sectionVimInsert(),
		sectionVimVisual(),
		sectionCommandLine(),
		sectionExCommands(),
		sectionFileTree(),
		sectionChatPanel(),
		sectionCodeViewer(),
		sectionInputPanel(),
		sectionCommandPalette(),
		sectionFindBar(),
		sectionReplaceBar(),
		sectionFieldManual(),
	}
}

func sectionGlobalShortcuts() Section {
	return Section{
		Title: "Global Shortcuts",
		Bindings: []Binding{
			{"Alt+h", "Toggle Field Manual", ""},
			{"Alt+e", "Toggle edit mode", ""},
			{"Ctrl+c / Ctrl+Shift+c", "Copy selection", "Edit panel visible and Edit mode active or Code panel "},
			{"Ctrl+x", "Cut selection to clipboard", "Edit panel visible and Edit mode active"},
			{"Ctrl+Shift+v", "Paste selection", "Edit mode active + Explorer panel selected w/ find or Edit panel selected + insert mode, Message panel visible and active"},
			{"Alt+Shift+a", "Select all", "Edit panel selected and Edit mode active"},
			{"Esc", "Close overlay / cancel / exit mode", ""},
			{"Esc (x2)", "Interrupt running agent", "Agent streaming"},
			{"Alt+Shift+Right", "Move focus to right panel", ""},
			{"Alt+Shift+Left", "Move focus to left panel", ""},
			{"Alt+Shift+Up", "Move focus to up one row to leftmost panel", ""},
			{"Alt+Shift+Down", "Move focus to down one row  to leftmost panel", ""},
		},
	}
}

func sectionChords() Section {
	return Section{
		Title: "Chord Shortcuts (two-key sequences)",
		Bindings: []Binding{
			{"Alt+s, Left/Right", "Cycle sessions", "Not in edit mode"},
			{"Alt+a, Left/Right", "Cycle agents", "Not in edit mode"},
			{"Alt+v, Left/Right", "Cycle views", "Not in edit mode"},
		},
	}
}

func sectionEditMode() Section {
	return Section{
		Title: "Edit Mode (Alt+E to activate)",
		Bindings: []Binding{
			{"Alt+t", "Toggle Tab/Explorer panel", "Edit mode active"},
			{"Alt+Enter", "Close tab (Tab panel or Tab bar)", "Edit mode active and Edit panel focused"},
			{"Shift+Tab", "Cycle to next tab", "Edit mode active, Edit panel focused and in insert/normal mode"},
			{":", "Enter command input", "Edit mode active and Edit panel in normal mode"},
			{"Alt+f", "Toggle find bar", "Edit mode and Edit panel panel focused"},
			{"Alt+Shift+r", "Toggle find-and-replace bar", "Edit mode active and Edit panel focused"},
			{"Alt+Shift+f", "LSP format document", "Edit mode active and Edit panel focused"},
			{"Alt+Shift+s", "Save buffer", "Edit mode active and Edit panel focused"},
			{"Alt+z", "Undo", "Edit mode active and Edit panel focused"},
			{"Alt+Shift+z", "Redo", "Edit mode active and Edit panel focused"},
			{"Alt+r / :references", "Find references (LSP)", "Edit mode active and Edit panel focused"},
			{"Alt+Shift+. / :symbols", "Toggle document symbols", "Edit mode active, Edit panel selected, cursor on symbol in document"},
		},
	}
}

func sectionVimNormal() Section {
	return Section{
		Title: "Vim Normal Mode",
		Bindings: []Binding{
			{"h / Left", "Move left", "Normal mode"},
			{"l / Right", "Move right", "Normal mode"},
			{"j / Down", "Move down", "Normal mode"},
			{"k / Up", "Move up", "Normal mode"},
			{"w", "Next word start", "Normal mode"},
			{"b", "Previous word start", "Normal mode"},
			{"e", "End of word", "Normal mode"},
			{"W / B / E", "WORD motion (whitespace-delimited)", "Normal mode"},
			{"0", "Line start", "Normal mode"},
			{"$", "Line end", "Normal mode"},
			{"^", "First non-blank character", "Normal mode"},
			{"G", "Document end (or [count]G for line)", "Normal mode"},
			{"f<char>", "Find character forward", "Normal mode"},
			{"F<char>", "Find character backward", "Normal mode"},
			{"t<char>", "Till character forward", "Normal mode"},
			{"T<char>", "Till character backward", "Normal mode"},
			{"%", "Jump to matching bracket/paren", "Normal mode"},
			{"{ / }", "Paragraph backward / forward", "Normal mode"},
			{"d[motion]", "Delete (e.g., dw, dd, d$)", "Normal mode"},
			{"c[motion]", "Change (delete + insert mode)", "Normal mode"},
			{"y[motion]", "Yank (copy)", "Normal mode"},
			{"> / <", "Indent / dedent", "Normal mode"},
			{"gq", "Format text", "Normal mode"},
			{"gu / gU", "Lowercase / uppercase", "Normal mode"},
			{"g~", "Toggle case", "Normal mode"},
			{"gd", "Go to definition (LSP)", "Normal mode"},
			{"gr", "Go to references (LSP)", "Normal mode"},
			{"gt / gT", "Next / previous tab", "Normal mode"},
			{"i", "Insert before cursor", "Normal mode"},
			{"I", "Insert at first non-blank", "Normal mode"},
			{"a", "Insert after cursor", "Normal mode"},
			{"A", "Insert at line end", "Normal mode"},
			{"o", "New line below + insert", "Normal mode"},
			{"O", "New line above + insert", "Normal mode"},
			{"v", "Enter visual (character) mode", "Normal mode"},
			{"V", "Enter visual line mode", "Normal mode"},
			{"u", "Undo", "Normal mode"},
			{"Ctrl+R", "Redo", "Normal mode"},
		},
	}
}

func sectionVimInsert() Section {
	return Section{
		Title: "Vim Insert Mode",
		Bindings: []Binding{
			{"Esc", "Exit to normal mode", "Insert mode"},
			{"Backspace", "Delete previous character", "Insert mode"},
			{"Enter", "New line with auto-indent", "Insert mode"},
			{"Tab", "Insert tab character", "Insert mode"},
			{"Arrow keys", "Move cursor", "Insert mode"},
			{"( / { / [", "Auto-close bracket pair", "Insert mode"},
			{") / } / ]", "Smart skip-over closing bracket", "Insert mode"},
		},
	}
}

func sectionVimVisual() Section {
	return Section{
		Title: "Vim Visual Mode",
		Bindings: []Binding{
			{"d / x", "Delete selection", "Visual mode"},
			{"y", "Yank (copy) selection", "Visual mode"},
			{"c", "Change selection (delete + insert)", "Visual mode"},
			{"U", "Uppercase selection", "Visual mode"},
			{"u", "Lowercase selection", "Visual mode"},
			{"~", "Toggle case", "Visual mode"},
			{"> / <", "Indent / dedent selection", "Visual mode"},
			{"J", "Join lines", "Visual mode"},
			{"r", "Replace character in selection", "Visual mode"},
			{"p", "Put (paste) over selection", "Visual mode"},
			{"o", "Swap anchor (reverse selection)", "Visual mode"},
			{"v / V / Ctrl+V", "Toggle between visual modes", "Visual mode"},
			{"Esc", "Exit visual mode", "Visual mode"},
		},
	}
}

func sectionCommandLine() Section {
	return Section{
		Title: "Command-Line Mode",
		Bindings: []Binding{
			{"Esc / Ctrl+C", "Cancel and return to normal", "Command-line mode"},
			{"Enter", "Execute command", "Command-line mode"},
			{"Backspace", "Delete character before cursor", "Command-line mode"},
			{"Left / Ctrl+B", "Move cursor left", "Command-line mode"},
			{"Right / Ctrl+F", "Move cursor right", "Command-line mode"},
			{"Home / Ctrl+A", "Move to line start", "Command-line mode"},
			{"End / Ctrl+E", "Move to line end", "Command-line mode"},
			{"Up / Down", "Navigate command history", "Command-line mode"},
			{"Tab", "Tab completion", "Command-line mode"},
			{"Ctrl+W", "Delete word before cursor", "Command-line mode"},
			{"Ctrl+U", "Clear entire line", "Command-line mode"},
		},
	}
}

func sectionExCommands() Section {
	return Section{
		Title: ":Commands (Ex Commands)",
		Bindings: []Binding{
			{":w", "Write (save) buffer to file", "Command-line mode"},
			{":q", "Quit (close window), :q! to force", "Command-line mode"},
			{":wq", "Write and quit", "Command-line mode"},
			{":e <path>", "Open file for editing", "Command-line mode"},
			{":split", "Horizontal split", "Command-line mode"},
			{":vsplit", "Vertical split", "Command-line mode"},
			{":bnext", "Switch to next buffer", "Command-line mode"},
			{":bprev", "Switch to previous buffer", "Command-line mode"},
			{":bd", "Delete buffer, :bd! to force", "Command-line mode"},
			{":ls", "List open buffers", "Command-line mode"},
			{":set <opt>", "Set editor option (e.g., :set nu)", "Command-line mode"},
			{":map <l> <r>", "Create key mapping", "Command-line mode"},
			{":source <f>", "Source (execute) a file", "Command-line mode"},
			{":lua <code>", "Execute Lua code", "Command-line mode"},
			{":!<cmd>", "Run shell command", "Command-line mode"},
			{":s/p/r/flags", "Substitute text (% for all lines)", "Command-line mode"},
			{":nohlsearch", "Clear search highlights", "Command-line mode"},
		},
	}
}

func sectionFileTree() Section {
	return Section{
		Title: "File Tree",
		Bindings: []Binding{
			{"j / Down", "Move cursor down", "File tree focused"},
			{"k / Up", "Move cursor up", "File tree focused"},
			{"h / Left", "Collapse directory", "File tree focused"},
			{"l / Right", "Expand directory", "File tree focused"},
			{"Enter", "Open file / activate entry", "File tree focused"},
			{"g", "Go to first entry", "File tree focused"},
			{"G", "Go to last entry", "File tree focused"},
			{"Alt+f", "Toggle search/filter", "File tree focused"},
			{"Alt+Shift+r", "Toggle multi-file replace", "File tree focused"},
			{"/", "Enter tab filter (in tabs mode)", "Tabs panel active"},
			{"Esc / q", "Exit references / symbols / tabs", "Submode active"},
		},
	}
}

func sectionChatPanel() Section {
	return Section{
		Title: "Chat Panel",
		Bindings: []Binding{
			{"j / Down", "Scroll down", "Chat focused"},
			{"k / Up", "Scroll up", "Chat focused"},
			{"PgDown", "Page down", "Chat focused"},
			{"PgUp", "Page up", "Chat focused"},
			{"Home", "Scroll to top", "Chat focused"},
			{"End", "Scroll to bottom", "Chat focused"},
		},
	}
}

func sectionCodeViewer() Section {
	return Section{
		Title: "Code Viewer",
		Bindings: []Binding{
			{"j / Down", "Scroll down", "Code viewer focused"},
			{"k / Up", "Scroll up", "Code viewer focused"},
			{"g / Home", "Scroll to top", "Code viewer focused"},
			{"G / End", "Scroll to bottom", "Code viewer focused"},
			{"PgDown", "Page down", "Code viewer focused"},
			{"PgUp", "Page up", "Code viewer focused"},
			{"Ctrl+L", "Toggle line numbers", "Code viewer focused"},
			{"Ctrl+W", "Toggle word wrap", "Code viewer focused"},
		},
	}
}

func sectionInputPanel() Section {
	return Section{
		Title: "Input Panel",
		Bindings: []Binding{
			{"Enter", "Submit message", "Input focused"},
			{"Shift+Enter", "Insert new line", "Input focused"},
			{"Up / Down", "Navigate history", "Input focused"},
			{"Tab", "Tab-complete", "Input focused"},
			{"Ctrl+U", "Clear line", "Input focused"},
			{"Ctrl+K", "Kill to end of line", "Input focused"},
			{"Left / Ctrl+B", "Move cursor left", "Input focused"},
			{"Right / Ctrl+F", "Move cursor right", "Input focused"},
			{"Home / Ctrl+A", "Move to start", "Input focused"},
			{"End / Ctrl+E", "Move to end", "Input focused"},
		},
	}
}

func sectionCommandPalette() Section {
	return Section{
		Title: "Command Palette (Ctrl+P)",
		Bindings: []Binding{
			{"Esc", "Close palette", "Palette open"},
			{"Enter", "Select result", "Palette open"},
			{"Up / Ctrl+K / Ctrl+P", "Move selection up", "Palette open"},
			{"Down / Ctrl+J / Ctrl+N", "Move selection down", "Palette open"},
			{"Backspace", "Delete character", "Palette open"},
			{"Ctrl+U", "Clear query", "Palette open"},
		},
	}
}

func sectionFindBar() Section {
	return Section{
		Title: "Find Bar (Alt+f in editor)",
		Bindings: []Binding{
			{"Esc", "Close find bar", "Find bar open"},
			{"Enter", "Go to next match", "Find bar open"},
			{"Shift+Enter", "Go to previous match", "Find bar open"},
			{"Tab", "Next tab stop ([Aa] [ab] [.*] [Sel])", "Find bar open"},
			{"Shift+Tab", "Previous tab stop", "Find bar open"},
		},
	}
}

func sectionReplaceBar() Section {
	return Section{
		Title: "Replace Bar (Alt+Shift+r in editor)",
		Bindings: []Binding{
			{"Esc", "Close replace bar", "Replace bar open"},
			{"Enter", "Go to next match", "Replace bar open"},
			{"Shift+Enter", "Go to previous match", "Replace bar open"},
			{"Tab / Shift+Tab", "Cycle tab stops (find, options, replace)", "Replace bar open"},
		},
	}
}

func sectionFieldManual() Section {
	return Section{
		Title: "Field Manual (this overlay)",
		Bindings: []Binding{
			{"j / Down", "Scroll down", ""},
			{"k / Up", "Scroll up", ""},
			{"PgDown", "Page down", ""},
			{"PgUp", "Page up", ""},
			{"g / Home", "Scroll to top", ""},
			{"G / End", "Scroll to bottom", ""},
			{"Esc / q", "Close Field Manual", ""},
		},
	}
}
