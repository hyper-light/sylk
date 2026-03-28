package academic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestConsultSkill_ExecuteResearchRejectsDuplicateQuestion(t *testing.T) {
	a, err := New(Config{ID: "academic"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	state := newAcademicResearchExecutionState("sess-exec")
	if err := state.recordConsultAttempt("librarian", "How does Sylk already structure auth?", ""); err != nil {
		t.Fatalf("recordConsultAttempt: %v", err)
	}
	ctx := WithAcademicResearchExecutionState(context.Background(), state)
	skill := a.skills.Get("consult")
	if skill == nil || skill.Handler == nil {
		t.Fatal("consult skill not registered")
	}

	_, err = skill.Handler(ctx, json.RawMessage(`{"target":"librarian","query":"How does Sylk already structure auth?","scope":""}`))
	if err == nil {
		t.Fatal("expected duplicate consultation to be rejected")
	}
	if !strings.Contains(err.Error(), "forbids repeating") {
		t.Fatalf("duplicate consult error = %v, want research-run duplicate guard", err)
	}
}

func TestAcademicResearchExecutionState_FinalizationBlockDoesNotBlockOnSearchWithoutSurfacedCandidate(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.observeNativeSearchCall(context.Background(), nil, providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli library recommendation",
	})

	reminder, _ := state.finalizationBlock()
	if reminder != "" {
		t.Fatalf("finalization reminder = %q, want empty without surfaced candidate URL", reminder)
	}
}

func TestAcademicResearchExecutionState_FinalizationBlockFlagsRepeatedSearchWithoutGrounding(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.observeNativeSearchCall(context.Background(), nil, providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli library recommendation",
	})
	state.observeNativeSearchCall(context.Background(), nil, providers.NativeWebSearchCall{
		ID:    "search-2",
		Query: "python cli library recommendation",
	})

	reminder, fields := state.finalizationBlock()
	if !strings.Contains(reminder, "Stop repeating the same search path") {
		t.Fatalf("finalization reminder = %q, want repeated search warning", reminder)
	}
	if got := fields["repeated_search_count"]; got == nil {
		t.Fatalf("expected repeated_search_count field, got %#v", fields)
	}
}

func TestAcademicResearchExecutionState_FinalizationBlockRequiresPaperWhenRequested(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.setResearchPaperRequired(true)

	reminder, _ := state.finalizationBlock()
	if !strings.Contains(reminder, "`author_research_paper`") {
		t.Fatalf("finalization reminder = %q, want author_research_paper guidance", reminder)
	}
}

func TestAcademicResearchExecutionState_FinalizationBlockRequiresSecuredFetchAfterNativeOpenPage(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.observeNativeSearchCall(context.Background(), nil, providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli library recommendation",
	})
	state.observeNativeSearchCall(context.Background(), nil, providers.NativeWebSearchCall{
		ID:     "open-1",
		Action: "open_page",
		Query:  "python cli library recommendation",
		URL:    "https://docs.example.com/cli",
	})

	reminder, fields := state.finalizationBlock()
	if !strings.Contains(reminder, "`ground_source`") {
		t.Fatalf("finalization reminder = %q, want secured fetch guidance", reminder)
	}
	if state.hasGroundedSources() {
		t.Fatal("expected native open_page to remain ungrounded until secured fetch succeeds")
	}
	if got := state.sourceURLs(); len(got) != 0 {
		t.Fatalf("sourceURLs = %#v, want no grounded URLs before secured fetch", got)
	}
	if got := fields["auto_fetch_candidate_url"]; got != "https://docs.example.com/cli" {
		t.Fatalf("auto_fetch_candidate_url = %#v, want surfaced open_page URL", got)
	}
}

func TestAcademicResearchExecutionState_RecordFetchResultReusesReservedSourceIDAndMarksGrounded(t *testing.T) {
	a, err := New(Config{ID: "academic"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	state := newAcademicResearchExecutionState("sess-exec")
	state.observeNativeSearchCall(context.Background(), a, providers.NativeWebSearchCall{
		ID:     "open-1",
		Action: "open_page",
		Query:  "oauth deployment guidance",
		URL:    "https://docs.example.com/oauth",
	})
	if state.hasGroundedSources() {
		t.Fatal("expected reserved source ID without grounded source before fetch result")
	}
	reservedID := state.sourceIDsByURL["https://docs.example.com/oauth"]
	if strings.TrimSpace(reservedID) == "" {
		t.Fatal("expected source ID to be reserved for surfaced native URL")
	}

	state.recordFetchResult(context.Background(), a, `{"success":true,"url":"https://docs.example.com/oauth","title":"OAuth Deployment Guide","content":"Use a dedicated token validation boundary.","word_count":1200,"grounded":true,"ingested":true}`)

	sourceIDs := state.sourceIDs()
	if len(sourceIDs) != 1 {
		t.Fatalf("sourceIDs = %#v, want one grounded source after fetch", sourceIDs)
	}
	updatedID := sourceIDs[0]
	if updatedID != reservedID {
		t.Fatalf("source ID changed after fetch enrichment: got %q want %q", updatedID, reservedID)
	}
	a.mu.RLock()
	source := a.sourceIndex[updatedID]
	a.mu.RUnlock()
	if source == nil {
		t.Fatalf("sourceIndex[%q] missing after fetch enrichment", updatedID)
	}
	if source.Title != "OAuth Deployment Guide" {
		t.Fatalf("source title = %q, want enriched fetch title", source.Title)
	}
}

func TestAcademicResearchExecutionState_ProviderSearchResultsBecomeCandidateGroundingURLs(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.observeProviderResponse(context.Background(), nil, &providers.Response{
		ProviderMetadata: map[string]any{
			providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
				{
					ID:       "search-1",
					Provider: "anthropic",
					Action:   "search",
					Query:    "oauth token validation boundary",
				},
			},
			providers.ProviderMetadataNativeWebSearchResultsKey: []providers.NativeWebSearchResult{
				{
					SearchID: "search-1",
					Provider: "anthropic",
					Source:   "search_result",
					Query:    "oauth token validation boundary",
					URL:      "https://docs.example.com/oauth",
					Title:    "OAuth Deployment Guide",
					PageAge:  "7d",
				},
				{
					Provider: "openai",
					Source:   "url_citation",
					URL:      "https://security.example.com/token-boundary",
					Title:    "Token Boundary Notes",
				},
			},
		},
	})

	candidates := state.candidateGroundingURLs(0)
	if len(candidates) != 2 {
		t.Fatalf("candidateGroundingURLs = %#v, want 2 URLs", candidates)
	}
	if candidates[0] != "https://docs.example.com/oauth" || candidates[1] != "https://security.example.com/token-boundary" {
		t.Fatalf("candidateGroundingURLs = %#v, want sorted surfaced URLs", candidates)
	}

	reminder, fields := state.finalizationBlock()
	if !strings.Contains(reminder, "Candidate URLs already surfaced in this run") {
		t.Fatalf("finalization reminder = %q, want surfaced candidate URL guidance", reminder)
	}
	if !strings.Contains(reminder, "https://docs.example.com/oauth") {
		t.Fatalf("finalization reminder = %q, want surfaced candidate URL", reminder)
	}
	if got := fields["candidate_grounding_url_count"]; got != 2 {
		t.Fatalf("candidate_grounding_url_count = %#v, want 2", got)
	}
}
