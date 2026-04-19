package providers

import (
	"strings"

	"github.com/google/uuid"
)

// EnsureToolCallID returns the given ID after trimming whitespace, or mints a
// synthetic UUID-based ID when the input is empty. Provider adapters must call
// this at every tool-call construction site so that downstream code (tool
// tracking, UI event pairing) can rely on tool calls always carrying a
// non-empty, lifecycle-stable ID.
//
// Some providers (notably Google Gemini) do not consistently assign IDs to
// function calls. Synthesizing here at the adapter boundary keeps that
// provider detail from leaking into the rest of the system.
func EnsureToolCallID(id string) string {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	return "tc_" + uuid.NewString()
}
