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

	// Tool call visualization
	IconToolCall      = "⚙"
	IconToolCallError = "⚠"

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

	// Steering
	IconSteer = "⎈"
)

// DotAnimFrames are origami-bloom glyphs for active status dot animation.
// Cycle: seed dot → hollow star → four-point → six-point → eight-point →
// teardrop bloom → collapse back. The shape unfolds like paper being folded
// into progressively more intricate geometric forms.
var DotAnimFrames = [...]string{"·", "✧", "✦", "✶", "✸", "❋", "✸", "✶"}

// DotAnimFrameCount is derived from the frame array length.
const DotAnimFrameCount = len(DotAnimFrames)
