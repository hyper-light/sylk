package ui

import "testing"

// TestShouldRouteHelpKey_OpensOutsideEditor is the UI-06 core case:
// `?` pressed from the chat/filetree/planview focus opens the Field
// Manual. Any regression here breaks the spec'd help accelerator.
func TestShouldRouteHelpKey_OpensOutsideEditor(t *testing.T) {
	cases := []struct {
		name       string
		viewIsEdit bool
		editorFoc  bool
	}{
		{"chat_focus_chat_mode", false, false},
		{"chat_focus_edit_mode", true, false},   // edit layout visible but editor not focused
		{"filetree_focus_edit_mode", true, false}, // same — non-editor pane active
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !shouldRouteHelpKey("?", tc.viewIsEdit, tc.editorFoc) {
				t.Errorf("shouldRouteHelpKey(?, viewEdit=%v, editorFoc=%v) = false, want true",
					tc.viewIsEdit, tc.editorFoc)
			}
		})
	}
}

// TestShouldRouteHelpKey_YieldsToFocusedEditor guards the vim-mode
// compat. In edit mode with the editor focused, `?` must NOT route
// to help — the editor will consume it for reverse search.
func TestShouldRouteHelpKey_YieldsToFocusedEditor(t *testing.T) {
	if shouldRouteHelpKey("?", true, true) {
		t.Error("? must yield to the editor when it is focused in edit mode")
	}
}

// TestShouldRouteHelpKey_IgnoresNonHelpKeys protects the hot-path
// cost: the predicate is called on every key press, so non-? keys
// must short-circuit cheaply without evaluating the focus check.
// The rejection path also matters for keybinding isolation — a
// regression that treated other keys as help would shadow every
// other binding that shares a condition branch.
func TestShouldRouteHelpKey_IgnoresNonHelpKeys(t *testing.T) {
	nonHelp := []string{"alt+h", "/", "esc", "enter", "j", "", "??"}
	for _, key := range nonHelp {
		if shouldRouteHelpKey(key, false, false) {
			t.Errorf("shouldRouteHelpKey(%q, ...) = true, want false", key)
		}
	}
}
