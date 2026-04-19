package knowledge

import (
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
)

// TestResultsFromConsultResponse_BuildsSummaryPlusCitations is the
// UI-05 shape contract: one payload produces 1 summary entry + N
// citation entries in a rankable, stable order. A regression that
// dropped the summary or reordered citations would break the panel's
// expected top-result semantics.
func TestResultsFromConsultResponse_BuildsSummaryPlusCitations(t *testing.T) {
	payload := &shared.ConsultResponsePayload{
		Summary: "the user wants to refactor the retry middleware",
		Citations: []shared.ConsultCitation{
			{Title: "retry.go", URL: "file://retry.go"},
			{Title: "client.go", URL: "file://client.go"},
		},
	}
	got := ResultsFromConsultResponse("librarian", payload)

	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3 (summary + 2 citations)", len(got))
	}
	if got[0].Title != payload.Summary {
		t.Errorf("entry[0].Title = %q, want summary text", got[0].Title)
	}
	if got[0].Score <= got[1].Score {
		t.Errorf("summary score %v should exceed first citation %v", got[0].Score, got[1].Score)
	}
	if got[1].Score <= got[2].Score {
		t.Errorf("citation score must be monotonically decreasing: [1]=%v [2]=%v", got[1].Score, got[2].Score)
	}
	if got[1].Title != "retry.go" {
		t.Errorf("citation order not preserved: got[1].Title = %q", got[1].Title)
	}
}

// TestResultsFromConsultResponse_NilAndEmptyReturnNil covers the
// skip-panel-update signal. A nil payload or an empty-summary-with-
// no-citations payload must produce nil so the AppModel can
// distinguish "no new results" from "clear the panel".
func TestResultsFromConsultResponse_NilAndEmptyReturnNil(t *testing.T) {
	if ResultsFromConsultResponse("librarian", nil) != nil {
		t.Error("nil payload should produce nil")
	}
	empty := &shared.ConsultResponsePayload{}
	if ResultsFromConsultResponse("librarian", empty) != nil {
		t.Error("empty payload should produce nil")
	}
}

// TestResultsFromConsultResponse_CitationsOnlyIsValid covers the
// rare but legal case where an agent answers purely through
// citations without a summary (e.g. "here are the three papers you
// asked for, no further commentary"). The adapter must still
// produce entries.
func TestResultsFromConsultResponse_CitationsOnlyIsValid(t *testing.T) {
	payload := &shared.ConsultResponsePayload{
		Citations: []shared.ConsultCitation{
			{Title: "paper.pdf", URL: "https://example.com/paper.pdf"},
		},
	}
	got := ResultsFromConsultResponse("academic", payload)
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if got[0].Title != "paper.pdf" {
		t.Errorf("entry title = %q, want paper.pdf", got[0].Title)
	}
}

// TestResultsFromConsultResponse_SourceTypeMapping ensures each
// known agent gets a distinct source-type badge so the UI
// differentiates librarian/academic/archivalist at a glance. A
// regression that mapped all to the same badge would defeat the
// visual distinction that drove the SourceType field in the first
// place.
func TestResultsFromConsultResponse_SourceTypeMapping(t *testing.T) {
	cases := map[string]string{
		"librarian":   "bleve",
		"academic":    "vector",
		"archivalist": "graph",
		"unknown":     "combined",
	}
	for source, want := range cases {
		t.Run(source, func(t *testing.T) {
			got := ResultsFromConsultResponse(source, &shared.ConsultResponsePayload{
				Summary: "x",
			})
			if len(got) != 1 {
				t.Fatalf("entries = %d, want 1", len(got))
			}
			if got[0].SourceType != want {
				t.Errorf("source %q → SourceType %q, want %q", source, got[0].SourceType, want)
			}
		})
	}
}

// TestResultsFromConsultResponse_CitationFallbackTitle covers the
// partial-citation case where Title is empty but URL or Ref is
// present. The adapter must pick the best available string rather
// than emitting a blank-titled row.
func TestResultsFromConsultResponse_CitationFallbackTitle(t *testing.T) {
	payload := &shared.ConsultResponsePayload{
		Summary: "summary",
		Citations: []shared.ConsultCitation{
			{URL: "https://example.com/no-title"},
			{Ref: "RFC-1234"},
			{}, // fully empty — should be skipped
		},
	}
	got := ResultsFromConsultResponse("academic", payload)
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3 (summary + 2 populated citations)", len(got))
	}
	if got[1].Title != "https://example.com/no-title" {
		t.Errorf("got[1].Title = %q, want URL fallback", got[1].Title)
	}
	if got[2].Title != "RFC-1234" {
		t.Errorf("got[2].Title = %q, want Ref fallback", got[2].Title)
	}
}

// TestCitationScoreAt_FloorsNegativeScores guards the large-citation-
// list edge case. Scores must never go negative because the panel's
// relevance bar rendering assumes non-negative floats.
func TestCitationScoreAt_FloorsNegativeScores(t *testing.T) {
	// With start=0.9 and step=0.05, index 20 would be -0.1 without the floor.
	if got := citationScoreAt(20); got < 0 {
		t.Errorf("citationScoreAt(20) = %v, must be >= 0", got)
	}
	if got := citationScoreAt(20); got != citationScoreFloor {
		t.Errorf("citationScoreAt(20) = %v, want floor %v", got, citationScoreFloor)
	}
}
