package knowledgeruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/search"
)

type fakeClaimsSearchBackend struct {
	requests []*search.SearchRequest
	result   *CommittedSearchResult
	err      error
}

func (f *fakeClaimsSearchBackend) Search(_ context.Context, req *search.SearchRequest) (*CommittedSearchResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil && strings.TrimSpace(req.PathFilter) != "" {
		filtered := &CommittedSearchResult{}
		for _, hit := range f.result.Hits {
			if strings.HasPrefix(hit.Document.Path, req.PathFilter) {
				filtered.Hits = append(filtered.Hits, hit)
			}
		}
		return filtered, nil
	}
	return f.result, nil
}

func TestArchivalistClaimsQuery_ByTopic(t *testing.T) {
	backend := &fakeClaimsSearchBackend{
		result: &CommittedSearchResult{Hits: []CommittedSearchHit{
			claimsQueryHit("doc-1", "claims/session-1/board-1/testament/t-1.md", `
# Testament t-1
entity_type: testament
entity_id: t-1
claim_id: c-1
testament_id: t-1
agent_id: architect
session_id: session-1
board_id: board-1
continuity_topic: python_cli_planning
topic: python_cli_planning

## Summary

Python CLI planning digest.
`, 0.9),
			claimsQueryHit("doc-2", "claims/session-1/board-1/testament/t-2.md", `
# Testament t-2
entity_type: testament
entity_id: t-2
agent_id: architect
session_id: session-1
board_id: board-1
topic: unrelated_topic

## Summary

Unrelated digest.
`, 0.8),
		}},
	}
	index := NewClaimsKnowledgeQueryIndex(backend)

	hits, err := index.LookupCarryForwardEnrichment(context.Background(), claims.RecallForwardEnrichmentQuery{
		AgentID:   "architect",
		Topic:     "python_cli_planning",
		SessionID: "session-1",
		BoardID:   "board-1",
		MaxItems:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1: %#v", len(hits), hits)
	}
	if hits[0].DocumentID != "doc-1" || hits[0].Topic != "python_cli_planning" {
		t.Fatalf("unexpected hit: %#v", hits[0])
	}
	if len(backend.requests) != 1 {
		t.Fatalf("search requests = %d, want 1", len(backend.requests))
	}
	if got := backend.requests[0].PathFilter; got != "claims/session-1/board-1" {
		t.Fatalf("path filter = %q, want claims/session-1/board-1", got)
	}
	if !strings.Contains(backend.requests[0].Query, "python_cli_planning") {
		t.Fatalf("query %q did not include topic", backend.requests[0].Query)
	}
}

func TestArchivalistClaimsQuery_ByAgent(t *testing.T) {
	backend := &fakeClaimsSearchBackend{
		result: &CommittedSearchResult{Hits: []CommittedSearchHit{
			claimsQueryHit("doc-architect", "claims/session-1/board-1/testament/t-1.md", `
entity_type: testament
entity_id: t-1
agent_id: architect
session_id: session-1
board_id: board-1
topic: planning

Architect continuity.
`, 0.9),
			claimsQueryHit("doc-librarian", "claims/session-1/board-1/testament/t-2.md", `
entity_type: testament
entity_id: t-2
agent_id: librarian
session_id: session-1
board_id: board-1
topic: planning

Librarian continuity.
`, 0.8),
		}},
	}
	index := NewClaimsKnowledgeQueryIndex(backend)

	hits, err := index.LookupCarryForwardEnrichment(context.Background(), claims.RecallForwardEnrichmentQuery{
		AgentID:   "architect",
		Topic:     "planning",
		SessionID: "session-1",
		BoardID:   "board-1",
		MaxItems:  8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1: %#v", len(hits), hits)
	}
	if hits[0].AgentID != "architect" {
		t.Fatalf("agent = %q, want architect", hits[0].AgentID)
	}
}

func TestArchivalistClaimsQuery_ByRelation(t *testing.T) {
	backend := &fakeClaimsSearchBackend{
		result: &CommittedSearchResult{Hits: []CommittedSearchHit{
			claimsQueryHit("doc-related", "claims/session-1/board-1/testament/t-1.md", `
entity_type: testament
entity_id: t-1
claim_id: claim-1
agent_id: architect
session_id: session-1
board_id: board-1
topic: planning
relation_claim_claim: claim-1
relations_index: claim:claim:claim-1,testament:derived_from:t-source

Related response.
`, 0.9),
			claimsQueryHit("doc-unrelated", "claims/session-1/board-1/testament/t-2.md", `
entity_type: testament
entity_id: t-2
claim_id: claim-2
agent_id: architect
session_id: session-1
board_id: board-1
topic: planning
relation_claim_claim: claim-2
relations_index: claim:claim:claim-2

Unrelated response.
`, 0.8),
		}},
	}
	index := NewClaimsKnowledgeQueryIndex(backend)

	hits, err := index.LookupCarryForwardEnrichment(context.Background(), claims.RecallForwardEnrichmentQuery{
		AgentID:              "architect",
		Topic:                "planning",
		SessionID:            "session-1",
		BoardID:              "board-1",
		EntityType:           "testament",
		RelationType:         string(claims.RelatedTypeClaim),
		RelationRelationship: string(claims.RelationshipClaim),
		RelatedID:            "claim-1",
		MaxItems:             8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1: %#v", len(hits), hits)
	}
	if hits[0].DocumentID != "doc-related" {
		t.Fatalf("document = %q, want doc-related", hits[0].DocumentID)
	}
}

func TestArchivalistClaimsQuery_NoLLMForMetadataLookup(t *testing.T) {
	backend := &fakeClaimsSearchBackend{result: &CommittedSearchResult{}}
	index := NewClaimsKnowledgeQueryIndex(backend)

	hits, err := index.LookupCarryForwardEnrichment(context.Background(), claims.RecallForwardEnrichmentQuery{
		AgentID:  "architect",
		Topic:    "planning",
		MaxItems: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %d, want 0", len(hits))
	}
	if len(backend.requests) != 2 {
		t.Fatalf("search requests = %d, want claims + legacy deterministic search calls", len(backend.requests))
	}
	if got := backend.requests[0].PathFilter; got != "claims/" {
		t.Fatalf("path filter = %q, want claims/", got)
	}
	if got := backend.requests[1].PathFilter; got != "archivalist/" {
		t.Fatalf("legacy path filter = %q, want archivalist/", got)
	}
}

func TestArchivalistClaimsQuery_DistinguishesAgenticNarrative(t *testing.T) {
	backend := &fakeClaimsSearchBackend{
		result: &CommittedSearchResult{Hits: []CommittedSearchHit{
			claimsQueryHit("doc-scribe", "claims/session-1/board-1/artifact/a-1.md", `
entity_type: artifact
entity_id: a-1
artifact_id: a-1
agent_id: scribe-architect
session_id: session-1
board_id: board-1
topic: planning
source_type: scribe
narration_type: continuity
archivalist_entry_id: entry-1

Scribe narrative digest.
`, 0.9),
		}},
	}
	index := NewClaimsKnowledgeQueryIndex(backend)

	hits, err := index.LookupCarryForwardEnrichment(context.Background(), claims.RecallForwardEnrichmentQuery{
		AgentID:   "scribe-architect",
		Topic:     "planning",
		SessionID: "session-1",
		BoardID:   "board-1",
		MaxItems:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if !hits[0].AgenticNarrative || hits[0].ArchivalistEntryID != "entry-1" {
		t.Fatalf("narrative metadata not surfaced: %+v", hits[0])
	}
}

func TestArchivalistClaimsQuery_LegacyArchivalistFallback(t *testing.T) {
	backend := &fakeClaimsSearchBackend{
		result: &CommittedSearchResult{Hits: []CommittedSearchHit{
			claimsQueryHit("legacy-entry-1", "archivalist/scribe/architect/entry-1.md", `
source_type: scribe
narration_type: continuity
parent_agent: architect
session_id: legacy-session
topic: python cli plan
archivalist_entry_id: entry-1

Previous planning selected Click and a pyproject console script.
`, 0.8),
		}},
	}
	index := NewClaimsKnowledgeQueryIndex(backend)

	hits, err := index.LookupCarryForwardEnrichment(context.Background(), claims.RecallForwardEnrichmentQuery{
		AgentID:   "architect",
		Topic:     "python cli plan",
		SessionID: "legacy-session",
		MaxItems:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1: %+v", len(hits), hits)
	}
	if !hits[0].Legacy || hits[0].Source != "archivalist_legacy_entry" || hits[0].ExactClaimSources {
		t.Fatalf("legacy enrichment metadata wrong: %+v", hits[0])
	}
	if len(backend.requests) != 2 || backend.requests[1].PathFilter != "archivalist/" {
		t.Fatalf("legacy fallback search requests = %+v", backend.requests)
	}
}

func claimsQueryHit(id, path, content string, score float64) CommittedSearchHit {
	return CommittedSearchHit{
		ScoredDocument: search.ScoredDocument{
			Document: search.Document{
				ID:      id,
				Path:    path,
				Type:    search.DocTypeMarkdown,
				Content: strings.TrimSpace(content),
			},
			Score: score,
		},
		PrimaryNodeID:   42,
		PrimaryNodeType: "claims_testament",
	}
}
