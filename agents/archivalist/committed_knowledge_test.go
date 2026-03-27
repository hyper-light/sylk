package archivalist

import (
	"context"
	"strings"
	"testing"

	knowledgequery "github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/search"
	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
)

type fakeCommittedArchivalBackend struct {
	result  *knowledgeruntime.CommittedSearchResult
	err     error
	lastReq *search.SearchRequest
}

func (f *fakeCommittedArchivalBackend) Search(_ context.Context, req *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error) {
	if req != nil {
		copied := *req
		f.lastReq = &copied
	}
	if f.result == nil {
		return &knowledgeruntime.CommittedSearchResult{}, f.err
	}
	return f.result, f.err
}

type mockRecallBleveIndex struct {
	results []knowledgequery.TextResult
}

func (m *mockRecallBleveIndex) SearchInContext(_ context.Context, _ *bleve.SearchRequest) (*bleve.SearchResult, error) {
	hits := make([]*blevesearch.DocumentMatch, len(m.results))
	for i, result := range m.results {
		hits[i] = &blevesearch.DocumentMatch{
			ID:    result.ID,
			Score: result.Score,
			Fields: map[string]any{
				"content": result.Content,
			},
		}
	}
	return &bleve.SearchResult{Hits: hits, Total: uint64(len(hits))}, nil
}

func TestArchivalistMergeCommittedKnowledgeRecall_AddsCommittedEntries(t *testing.T) {
	a := newTestArchivalist(t)
	a.knowledgeBackend = &fakeCommittedArchivalBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			HeadVersion: "v3.2",
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-1",
							Path:    "https://example.com/guardian-approval",
							Type:    search.DocTypeWebFetch,
							Content: "Guardian approval flows should pause the parent agent until consent is granted.",
						},
						Score: 0.94,
					},
					PrimaryNodeID:   42,
					PrimaryNodeType: "document",
					Domain:          "research",
					Symbols:         []string{"Guardian"},
				},
			},
		},
	}

	archiveEntries := []*Entry{
		{
			ID:       "archive-1",
			Category: CategoryInsight,
			Content:  "Older archive insight",
			Source:   SourceModelArchivalist,
		},
	}
	result := a.mergeCommittedKnowledgeRecall(context.Background(), ArchiveQuery{
		SearchText: "guardian approval",
		Limit:      4,
	}, archiveEntries)

	if len(result) != 2 {
		t.Fatalf("merged entry count = %d, want 2", len(result))
	}
	var knowledgeEntry *Entry
	for _, entry := range result {
		if strings.HasPrefix(entry.ID, "knowledge:") {
			knowledgeEntry = entry
			break
		}
	}
	if knowledgeEntry == nil {
		t.Fatal("expected committed knowledge entry to be interleaved into recall results")
	}
	if knowledgeEntry.Category != CategoryInsight {
		t.Fatalf("knowledge entry category = %v, want %v", knowledgeEntry.Category, CategoryInsight)
	}
	if knowledgeEntry.Metadata["search_source"] != "committed_knowledge" {
		t.Fatalf("search source = %v, want committed_knowledge", knowledgeEntry.Metadata["search_source"])
	}
	if knowledgeEntry.Metadata["head_version"] != "v3.2" {
		t.Fatalf("head version = %v, want v3.2", knowledgeEntry.Metadata["head_version"])
	}
}

func TestArchivalistKnowledgeSearch_UsesCommittedBackendWithoutCoordinator(t *testing.T) {
	a := newTestArchivalist(t)
	backend := &fakeCommittedArchivalBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			HeadVersion: "v1.7",
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-2",
							Path:    "docs/approval.md",
							Type:    search.DocTypeMarkdown,
							Content: "Approval dialogue requirements",
						},
						Score: 0.88,
					},
					RelatedPaths: []string{"ui/app.go"},
				},
			},
		},
	}
	a.knowledgeBackend = backend
	a.queryCoordinator = nil

	result, err := a.knowledgeSearch(context.Background(), "approval dialogue", "", 6, false, nil, false)
	if err != nil {
		t.Fatalf("knowledgeSearch returned error: %v", err)
	}
	if backend.lastReq == nil {
		t.Fatal("expected committed backend request to be recorded")
	}
	if backend.lastReq.Query != "approval dialogue" {
		t.Fatalf("backend query = %q, want approval dialogue", backend.lastReq.Query)
	}
	if backend.lastReq.Limit != 6 {
		t.Fatalf("backend limit = %d, want 6", backend.lastReq.Limit)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("knowledgeSearch payload type = %T, want map[string]any", result)
	}
	results, ok := payload["results"].([]map[string]any)
	if !ok {
		t.Fatalf("knowledgeSearch results type = %T, want []map[string]any", payload["results"])
	}
	if len(results) != 1 {
		t.Fatalf("knowledgeSearch result count = %d, want 1", len(results))
	}
	if results[0]["source"] != "committed_knowledge" {
		t.Fatalf("knowledgeSearch source = %v, want committed_knowledge", results[0]["source"])
	}
	metrics, ok := payload["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("knowledgeSearch metrics type = %T, want map[string]any", payload["metrics"])
	}
	if metrics["head_version"] != "v1.7" {
		t.Fatalf("head version = %v, want v1.7", metrics["head_version"])
	}
}

func TestArchivalistMergeCommittedKnowledgeRecall_CorroboratesCommittedHitWithHybridKnowledge(t *testing.T) {
	a := newTestArchivalist(t)
	a.knowledgeBackend = &fakeCommittedArchivalBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			HeadVersion: "v9.4",
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-1",
							Path:    "https://example.com/research",
							Type:    search.DocTypeWebFetch,
							Content: "Guardian approval requires a visible user consent checkpoint.",
						},
						Score: 0.71,
					},
					PrimaryNodeID:   5,
					PrimaryNodeType: "document",
				},
			},
		},
	}
	a.queryCoordinator = knowledgequery.NewHybridQueryCoordinator(
		knowledgequery.NewBleveSearcher(&mockRecallBleveIndex{
			results: []knowledgequery.TextResult{
				{ID: "doc-1", Score: 0.95, Content: "Guardian approval requires a visible user consent checkpoint."},
			},
		}),
		nil,
		nil,
	)

	result := a.mergeCommittedKnowledgeRecall(context.Background(), ArchiveQuery{
		SearchText: "guardian approval",
		Limit:      4,
	}, []*Entry{{ID: "archive-1", Category: CategoryInsight, Content: "Older archive note", Source: SourceModelArchivalist}})

	if len(result) != 2 {
		t.Fatalf("merged entry count = %d, want 2", len(result))
	}
	if result[0].Metadata["search_source"] != "committed_knowledge+hybrid" {
		t.Fatalf("search source = %v, want committed_knowledge+hybrid", result[0].Metadata["search_source"])
	}
	if result[0].Metadata["hybrid_source"] != "bleve" {
		t.Fatalf("hybrid source = %v, want bleve", result[0].Metadata["hybrid_source"])
	}
	if metadataFloat(result[0].Metadata, "combined_score") <= metadataFloat(result[1].Metadata, "combined_score") {
		t.Fatalf("expected corroborated knowledge entry to outrank archive entry")
	}
}

func TestArchivalistKnowledgeSearch_MergesCommittedAndHybridResults(t *testing.T) {
	a := newTestArchivalist(t)
	a.knowledgeBackend = &fakeCommittedArchivalBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			HeadVersion: "v2.8",
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-1",
							Path:    "docs/guardian.md",
							Type:    search.DocTypeMarkdown,
							Content: "Guardian approval notes",
						},
						Score: 0.72,
					},
				},
			},
		},
	}
	a.queryCoordinator = knowledgequery.NewHybridQueryCoordinator(
		knowledgequery.NewBleveSearcher(&mockRecallBleveIndex{
			results: []knowledgequery.TextResult{
				{ID: "doc-1", Score: 0.91, Content: "Guardian approval notes"},
				{ID: "doc-2", Score: 0.88, Content: "Architect review coordination"},
			},
		}),
		nil,
		nil,
	)

	payload, err := a.knowledgeSearch(context.Background(), "guardian approval", "", 5, false, nil, false)
	if err != nil {
		t.Fatalf("knowledgeSearch returned error: %v", err)
	}
	data, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("knowledgeSearch payload type = %T, want map[string]any", payload)
	}
	results, ok := data["results"].([]map[string]any)
	if !ok {
		t.Fatalf("knowledgeSearch results type = %T, want []map[string]any", data["results"])
	}
	if len(results) != 2 {
		t.Fatalf("knowledgeSearch result count = %d, want 2", len(results))
	}
	if results[0]["source"] != "committed_knowledge+hybrid" {
		t.Fatalf("first result source = %v, want committed_knowledge+hybrid", results[0]["source"])
	}
	if results[1]["id"] != "doc-2" {
		t.Fatalf("second result id = %v, want doc-2", results[1]["id"])
	}
}
