package ui

import (
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/msg"
)

// UI-05 dispatch stitch: route knowledge-agent consult responses
// into the knowledge panel as ranked ResultEntry rows.
//
// handleGuideResponse already fans every agent response out to
// chat, telemetry, and streams. This helper is the single additional
// sink for the knowledge panel: it decodes the response content as
// a ConsultResponsePayload (best-effort — the decoder has a prose
// fallback for pre-typed-payload agents), runs the adapter from
// UI-05, and pushes non-nil results into the panel.
//
// The panel update is always side-effect-only — it never returns a
// tea.Cmd because the knowledge panel renders synchronously from
// its internal state. Callers invoke this as a fire-and-forget
// step inside handleGuideResponse; the existing response pipeline
// keeps running unchanged.

// knowledgeAgentTypes lists the agent types whose responses feed
// the knowledge panel. Librarian surfaces codebase search results;
// Academic surfaces external research citations. Archivalist
// consults generally answer "have we seen this before" in a form
// that doesn't map to the relevance-list panel, so it's intentionally
// omitted here — if that changes, add it.
var knowledgeAgentTypes = map[string]struct{}{
	"librarian": {},
	"academic":  {},
}

// isKnowledgeAgentType reports whether responses from agentType
// should populate the knowledge panel.
func isKnowledgeAgentType(agentType string) bool {
	_, ok := knowledgeAgentTypes[strings.ToLower(strings.TrimSpace(agentType))]
	return ok
}

// pushKnowledgeResponseToPanel decodes r.Content as a
// ConsultResponsePayload and pushes the adapted entries into the
// knowledge panel. A response with no extractable content (prose-
// fallback with an empty summary, or malformed JSON without citations)
// returns without touching the panel — the previous result set stays
// visible. This preserves the "stale but readable" invariant when an
// agent returns garbage; clearing on every failed response would
// cause the panel to flicker empty.
//
// Guards:
//   - No-op on errored responses (r.Err != nil) — error text is not
//     a knowledge result.
//   - No-op for non-knowledge agent types.
//   - No-op when the knowledge panel is unbound (test fixtures,
//     bootstrap paths that omit it).
func (m *AppModel) pushKnowledgeResponseToPanel(r msg.GuideResponseMsg) {
	if r.Err != nil || !isKnowledgeAgentType(r.AgentType) {
		return
	}
	if m.knowledgePanel == nil {
		return
	}
	payload := shared.DecodeConsultResponsePayloadFromLLM(r.Content)
	entries := knowledge.ResultsFromConsultResponse(r.AgentType, payload)
	if entries == nil {
		return
	}
	m.knowledgePanel.SetResults(entries)
}
