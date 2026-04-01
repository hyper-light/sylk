package academic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	knowledgequery "github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/search"
)

type fakeAcademicKnowledgeBackend struct {
	result *knowledgeruntime.CommittedSearchResult
	err    error
}

func (f *fakeAcademicKnowledgeBackend) Search(_ context.Context, _ *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAcademicKnowledgeQuery_NilGuards(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	for _, payload := range []map[string]any{
		{"action": "search", "text": "dependency injection"},
		{"action": "readiness"},
	} {
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatalf("marshal payload: %v", marshalErr)
		}
		result := a.skills.Invoke(context.Background(), "knowledge_query", raw)
		if !errors.Is(result.Err, errKnowledgeQueryUnavailable) {
			t.Fatalf("knowledge_query error = %v, want %v", result.Err, errKnowledgeQueryUnavailable)
		}
	}
}

func TestAcademicKnowledgeQueryReadiness_WithCoordinator(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	a.knowledgeMu.Lock()
	a.queryCoordinator = knowledgequery.NewHybridQueryCoordinator(nil, nil, nil)
	a.knowledgeMu.Unlock()

	raw, marshalErr := json.Marshal(map[string]any{"action": "readiness"})
	if marshalErr != nil {
		t.Fatalf("marshal payload: %v", marshalErr)
	}
	result := a.skills.Invoke(context.Background(), "knowledge_query", raw)
	if result.Err != nil {
		t.Fatalf("knowledge_query readiness: %v", result.Err)
	}

	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("knowledge_query readiness type = %T, want map[string]any", result.Data)
	}
	if got, want := data["ready"], false; got != want {
		t.Fatalf("knowledge_query readiness ready = %v, want %v", got, want)
	}
}

func TestAcademicKnowledgeQuerySearch_UsesCommittedBackend(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	a.SetKnowledgeBackend(&fakeAcademicKnowledgeBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			Query:       "dependency injection",
			HeadVersion: "head-1",
			TotalHits:   1,
			SearchTime:  5 * time.Millisecond,
			Hits: []knowledgeruntime.CommittedSearchHit{{
				ScoredDocument: search.ScoredDocument{
					Document: search.Document{
						ID:      "doc-1",
						Path:    "research/di.md",
						Content: "Prefer explicit constructor wiring when interfaces stabilize behavior boundaries.",
						Type:    search.DocTypeMarkdown,
					},
					Score: 0.91,
				},
				PrimaryNodeType: "document",
				Domain:          "academic",
				RelatedPaths:    []string{"pkg/service/container.go"},
			}},
		},
	})

	execResult, execErr := a.executeToolCall(context.Background(), providers.ToolCall{
		ID:        "call-knowledge-query",
		Name:      "knowledge_query",
		Arguments: `{"action":"search","text":"dependency injection","limit":5}`,
	})
	if execErr != nil {
		t.Fatalf("execute knowledge_query: %v", execErr)
	}

	var payload struct {
		Results []map[string]any `json:"results"`
		Metrics map[string]any   `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(execResult.Output), &payload); err != nil {
		t.Fatalf("unmarshal knowledge_query output: %v", err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("knowledge_query results len = %d, want 1", len(payload.Results))
	}
	if got, want := payload.Results[0]["path"], "research/di.md"; got != want {
		t.Fatalf("knowledge_query path = %v, want %v", got, want)
	}
	if got, want := payload.Metrics["head_version"], "head-1"; got != want {
		t.Fatalf("knowledge_query head_version = %v, want %v", got, want)
	}
}
