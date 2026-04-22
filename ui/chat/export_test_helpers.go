package chat

// HistoryEntriesForTest returns a snapshot of the chat model's
// history entries for assertion from cross-package integration
// tests (e.g. ui/bridge's end-to-end harness). Only exported for
// tests — production code reads history via the model's public
// rendering API.
func HistoryEntriesForTest(m *Model) []ChatEntry {
	if m == nil || m.history == nil {
		return nil
	}
	out := make([]ChatEntry, 0, m.history.Len())
	for i := range m.history.Len() {
		if entry := m.history.Get(i); entry != nil {
			out = append(out, *entry)
		}
	}
	return out
}
