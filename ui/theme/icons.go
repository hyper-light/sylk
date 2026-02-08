package theme

// Unicode glyphs for status indicators and agent badges.
// These are safe for most modern terminals with Unicode support.

const (
	// Agent status
	IconIdle     = "○"
	IconThinking = "◉"
	IconActing   = "●"
	IconError    = "✕"
	IconSuccess  = "✓"
	IconHandoff  = "⇄"
	IconWaiting  = "◌"

	// Navigation
	IconArrowRight = "→"
	IconArrowLeft  = "←"
	IconArrowUp    = "↑"
	IconArrowDown  = "↓"
	IconExpand     = "▸"
	IconCollapse   = "▾"

	// Chat
	IconUser   = "❯"
	IconAgent  = "◆"
	IconSystem = "◇"
	IconTool   = "⚡"

	// Status bar
	IconSpinner  = "◐"
	IconTokens   = "τ"
	IconSession  = "◈"
	IconBranch   = "⎇"
	IconModified = "●"

	// Editor modes
	IconNormal  = "N"
	IconInsert  = "I"
	IconVisual  = "V"
	IconReplace = "R"
	IconCommand = ":"
	IconPreview = "P"

	// Search
	IconSearch = "/"
	IconFilter = "⊕"
)
