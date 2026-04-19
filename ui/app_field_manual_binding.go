package ui

// shouldRouteHelpKey decides whether a key press should open the
// Field Manual overlay. UI-06 semantics:
//
//   - The caller tests the key string against "?" — no other key
//     should trigger help through this route.
//   - Inside a focused editor, vim-mode uses `?` for reverse search;
//     yielding to the editor keeps that behavior intact.
//   - Outside edit mode, or with a non-editor focus (chat, filetree,
//     planview, etc.), `?` acts as the help accelerator the AUDIT
//     spec calls for. Alt+H remains available in both cases.
//
// Extracted from the inline predicate in app.go so the routing rule
// is unit-testable without building a full AppModel fixture.
func shouldRouteHelpKey(keyString string, viewModeIsEdit, editorFocused bool) bool {
	if keyString != "?" {
		return false
	}
	return !(viewModeIsEdit && editorFocused)
}
