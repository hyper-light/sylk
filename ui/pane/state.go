package pane

// PaneKind identifies the type of content a leaf pane displays.
type PaneKind int

const (
	// KindEditor is a pane containing an editor with tabs.
	KindEditor PaneKind = iota
	// KindPreview is a pane containing the read-only file preview.
	KindPreview
)
