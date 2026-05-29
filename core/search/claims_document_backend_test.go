package search

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestClaimsDocumentBackendIndexesThroughBleve(t *testing.T) {
	bleve := &claimsDocumentBleve{}
	backend := NewClaimsDocumentBackend(ClaimsDocumentBackendConfig{Bleve: bleve, IndexPath: "bleve:test"})
	data, err := backend.HandleDocumentOperation(context.Background(), claims.DocumentOperationRequest{
		Call: claims.ExpectedToolCall{Tool: claims.DocumentToolIngest, Arguments: map[string]any{
			"document_id": "doc-1",
			"path":        "docs/test.md",
			"content":     "claims evidence",
		}},
		Requested: claims.DocumentOperationArtifactData{Operation: claims.DocumentToolIngest, DocumentID: "doc-1"},
	})
	if err != nil {
		t.Fatalf("HandleDocumentOperation: %v", err)
	}
	if !data.Indexed || data.DocumentID != "doc-1" || data.IndexPath != "bleve:test" || bleve.indexed == nil {
		t.Fatalf("document data = %+v indexed=%+v, want indexed evidence", data, bleve.indexed)
	}
}

func TestClaimsDocumentBackendSearchUsesBleve(t *testing.T) {
	bleve := &claimsDocumentBleve{result: &SearchResult{Documents: []ScoredDocument{{Document: Document{ID: "doc-1"}, Score: 1}}}}
	backend := NewClaimsDocumentBackend(ClaimsDocumentBackendConfig{Bleve: bleve})
	data, err := backend.HandleDocumentOperation(context.Background(), claims.DocumentOperationRequest{
		Call: claims.ExpectedToolCall{Tool: claims.DocumentToolSearchBleve, Arguments: map[string]any{
			"query": "claims",
			"limit": float64(1),
		}},
		Requested: claims.DocumentOperationArtifactData{Operation: claims.DocumentToolSearchBleve, DocumentID: "doc-1"},
	})
	if err != nil {
		t.Fatalf("HandleDocumentOperation: %v", err)
	}
	if !data.Indexed || data.ResultCount != 1 || bleve.query != "claims" {
		t.Fatalf("document data = %+v query=%q, want search evidence", data, bleve.query)
	}
}

type claimsDocumentBleve struct {
	indexed *Document
	query   string
	result  *SearchResult
}

func (b *claimsDocumentBleve) Open() error { return nil }

func (b *claimsDocumentBleve) Close() error { return nil }

func (b *claimsDocumentBleve) IsOpen() bool { return true }

func (b *claimsDocumentBleve) Search(_ context.Context, req *SearchRequest) (*SearchResult, error) {
	b.query = req.Query
	return b.result, nil
}

func (b *claimsDocumentBleve) Index(_ context.Context, doc *Document) error {
	b.indexed = doc
	return nil
}

func (b *claimsDocumentBleve) IndexBatch(_ context.Context, docs []*Document) error {
	if len(docs) != 0 {
		b.indexed = docs[0]
	}
	return nil
}

func (b *claimsDocumentBleve) Delete(_ context.Context, _ string) error { return nil }

func (b *claimsDocumentBleve) DocumentCount() (uint64, error) { return 0, nil }
