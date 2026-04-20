package ui

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// TestIsKnowledgeAgentType_AllowsLibrarianAndAcademic locks the
// allowlist scope. Adding other types here would flood the
// knowledge panel with orchestrator / guardian / architect
// responses that don't map to ResultEntry semantics.
func TestIsKnowledgeAgentType_AllowsLibrarianAndAcademic(t *testing.T) {
	allow := []string{"librarian", "academic", "LIBRARIAN", "  academic  "}
	for _, a := range allow {
		if !isKnowledgeAgentType(a) {
			t.Errorf("isKnowledgeAgentType(%q) = false, want true", a)
		}
	}
	deny := []string{"architect", "guardian", "orchestrator", "archivalist", "engineer", ""}
	for _, a := range deny {
		if isKnowledgeAgentType(a) {
			t.Errorf("isKnowledgeAgentType(%q) = true, want false", a)
		}
	}
}

// TestPushKnowledgeResponseToPanel_PopulatesOnLibrarianResponse is
// the end-to-end dispatch-stitch check: a well-formed
// ConsultResponsePayload from a librarian response must land on the
// knowledge panel as ResultEntry rows.
func TestPushKnowledgeResponseToPanel_PopulatesOnLibrarianResponse(t *testing.T) {
	panel := knowledge.New(theme.DefaultDark())
	m := &AppModel{knowledgePanel: panel}

	payload := &shared.ConsultResponsePayload{
		Summary: "retry middleware sits at core/providers/retry.go:42",
		Citations: []shared.ConsultCitation{
			{Title: "retry.go", URL: "file://core/providers/retry.go"},
			{Title: "client.go", URL: "file://core/providers/client.go"},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	m.pushKnowledgeResponseToPanel(msg.GuideResponseMsg{
		AgentType:     "librarian",
		CorrelationID: "corr-1",
		Content:       string(raw),
	})

	// ResultsFromConsultResponse produces 3 entries (summary + 2
	// citations). ResultCount is the direct observable on the panel.
	if got := panel.ResultCount(); got != 3 {
		t.Errorf("panel ResultCount = %d, want 3 (summary + 2 citations)", got)
	}
}

// TestPushKnowledgeResponseToPanel_SkipsNonKnowledgeAgent verifies
// that non-knowledge-agent responses (architect, orchestrator, etc.)
// do not mutate the panel. The panel stays in its prior state so
// users don't see unrelated content pushed by, say, an orchestrator
// status reply.
func TestPushKnowledgeResponseToPanel_SkipsNonKnowledgeAgent(t *testing.T) {
	panel := knowledge.New(theme.DefaultDark())
	m := &AppModel{knowledgePanel: panel}

	m.pushKnowledgeResponseToPanel(msg.GuideResponseMsg{
		AgentType: "architect",
		Content:   `{"summary":"irrelevant to knowledge panel"}`,
	})
	if got := panel.ResultCount(); got != 0 {
		t.Errorf("architect response leaked %d entries into panel, want 0", got)
	}
}

// TestPushKnowledgeResponseToPanel_SkipsErroredResponse covers the
// failure case. An errored response carries error text in Content
// that must not be pushed as a knowledge result — doing so would
// render the error message as a relevance-ranked row.
func TestPushKnowledgeResponseToPanel_SkipsErroredResponse(t *testing.T) {
	panel := knowledge.New(theme.DefaultDark())
	m := &AppModel{knowledgePanel: panel}

	m.pushKnowledgeResponseToPanel(msg.GuideResponseMsg{
		AgentType: "librarian",
		Content:   "stream failed: context deadline exceeded",
		Err:       errors.New("stream failed"),
	})
	if got := panel.ResultCount(); got != 0 {
		t.Errorf("errored response leaked %d entries into panel, want 0", got)
	}
}

// TestPushKnowledgeResponseToPanel_NilPanelIsNoop protects the
// bootstrap / test path where the knowledge panel may not be
// constructed. The stitch must not panic when the field is nil.
func TestPushKnowledgeResponseToPanel_NilPanelIsNoop(t *testing.T) {
	m := &AppModel{knowledgePanel: nil}
	// Must not panic.
	m.pushKnowledgeResponseToPanel(msg.GuideResponseMsg{
		AgentType: "librarian",
		Content:   `{"summary":"text"}`,
	})
}

// TestPushKnowledgeResponseToPanel_ProseFallbackPopulatesPanel
// guards the prose-fallback decode path. DecodeConsultResponsePayload
// FromLLM accepts non-JSON text and treats the whole body as the
// summary. The stitch must honor that so agents that have not
// migrated to typed payloads yet still populate the panel.
func TestPushKnowledgeResponseToPanel_ProseFallbackPopulatesPanel(t *testing.T) {
	panel := knowledge.New(theme.DefaultDark())
	m := &AppModel{knowledgePanel: panel}

	m.pushKnowledgeResponseToPanel(msg.GuideResponseMsg{
		AgentType: "academic",
		Content:   "plain-prose answer about RFC 7230 conn reuse semantics",
	})
	if got := panel.ResultCount(); got == 0 {
		t.Error("prose fallback produced 0 entries — decoder returned empty summary")
	}
}
