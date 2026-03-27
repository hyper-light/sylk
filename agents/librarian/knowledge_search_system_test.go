package librarian

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/search"
)

type fakeCommittedKnowledgeBackend struct {
	result  *knowledgeruntime.CommittedSearchResult
	err     error
	lastReq *search.SearchRequest
}

func (f *fakeCommittedKnowledgeBackend) Search(_ context.Context, req *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error) {
	if req != nil {
		copied := *req
		f.lastReq = &copied
	}
	if f.result == nil {
		return &knowledgeruntime.CommittedSearchResult{}, f.err
	}
	return f.result, f.err
}

func TestCommittedKnowledgeSearchSystem_SearchUsesCommittedBackend(t *testing.T) {
	backend := &fakeCommittedKnowledgeBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			Query:      "needle",
			SearchTime: 25 * time.Millisecond,
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-1",
							Path:    "ui/theme/palette.go",
							Type:    search.DocTypeSourceCode,
							Content: "package theme\n\nfunc palette() {\n\tneedle()\n}\n",
						},
						Score: 0.91,
					},
					NodeKinds: []string{"file", "function"},
				},
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-2",
							Path:    "docs/theme.md",
							Type:    search.DocTypeMarkdown,
							Content: "needle",
						},
						Score: 0.75,
					},
					NodeKinds: []string{"document"},
				},
			},
		},
	}

	system := NewCommittedKnowledgeSearchSystem(backend)
	result, err := system.Search(context.Background(), "needle", SearchOptions{
		Limit:      7,
		PathPrefix: "ui/theme",
		Fuzzy:      true,
		Types:      []string{"function"},
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if backend.lastReq == nil {
		t.Fatal("expected committed backend request to be recorded")
	}
	if backend.lastReq.Query != "needle" {
		t.Fatalf("backend query = %q, want needle", backend.lastReq.Query)
	}
	if backend.lastReq.PathFilter != "ui/theme" {
		t.Fatalf("backend path filter = %q, want ui/theme", backend.lastReq.PathFilter)
	}
	if backend.lastReq.Limit != 7 {
		t.Fatalf("backend limit = %d, want 7", backend.lastReq.Limit)
	}
	if backend.lastReq.FuzzyLevel != 1 {
		t.Fatalf("backend fuzzy level = %d, want 1", backend.lastReq.FuzzyLevel)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("document count = %d, want 1", len(result.Documents))
	}
	if result.Documents[0].Path != "ui/theme/palette.go" {
		t.Fatalf("document path = %q, want ui/theme/palette.go", result.Documents[0].Path)
	}
	if result.Documents[0].Line != 4 {
		t.Fatalf("document line = %d, want 4", result.Documents[0].Line)
	}
	if result.Took != 25*time.Millisecond {
		t.Fatalf("search took = %v, want 25ms", result.Took)
	}
}

func TestCommittedKnowledgeSearchSystem_SearchRequiresBackend(t *testing.T) {
	system := NewCommittedKnowledgeSearchSystem(nil)
	_, err := system.Search(context.Background(), "needle", SearchOptions{})
	if err != knowledgeruntime.ErrCommittedBackendUnavailable {
		t.Fatalf("Search error = %v, want %v", err, knowledgeruntime.ErrCommittedBackendUnavailable)
	}
}
