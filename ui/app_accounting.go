package ui

// updateTokenDisplay pushes cumulative token counts to the status bar.
//
// The accountant is the canonical session-wide source whenever it is live. The
// bus counters are a cold-start fallback for tests and for the short window
// before the accountant bridge publishes its first non-zero snapshot.
func (m *AppModel) updateTokenDisplay() {
	if m.statusBar == nil {
		return
	}

	prompt := m.busInputTokens
	completion := m.busOutputTokens
	cacheRead := m.busCacheReadTokens
	reasoning := m.busReasoningTokens

	if m.accountantTotalInput != 0 || m.accountantTotalOutput != 0 || m.accountantTotalReasoning != 0 {
		prompt = int(m.accountantTotalInput)
		completion = int(m.accountantTotalOutput)
		reasoning = int(m.accountantTotalReasoning)
	}

	m.statusBar.SetTokens(prompt, completion, cacheRead, reasoning)
}
