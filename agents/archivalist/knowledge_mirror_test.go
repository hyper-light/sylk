package archivalist

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

type fakeCommittedKnowledgeWriter struct {
	requests []*knowledgeruntime.TextDocumentIngestRequest
	err      error
}

func (f *fakeCommittedKnowledgeWriter) UpsertTextDocument(_ context.Context, req *knowledgeruntime.TextDocumentIngestRequest) error {
	f.requests = append(f.requests, req)
	return f.err
}

func TestArchivalistStoreEntry_MirrorsScribeEntriesToKnowledge(t *testing.T) {
	a := newTestArchivalist(t)
	writer := &fakeCommittedKnowledgeWriter{}
	a.knowledgeWriter = writer

	entry := makeEntry(CategoryInsight, "Engineer implemented retry backoff and activation guards.", SourceModelArchivalist)
	entry.Metadata = normalizeCrossAgentMetadata(map[string]any{
		"source_type":  "scribe",
		"parent_agent": "engineer",
	}, "scribe-engineer-1", "scribe-engineer")

	result := a.StoreEntry(context.Background(), entry)
	if !result.Success {
		t.Fatalf("store entry failed: %v", result.Error)
	}
	if len(writer.requests) != 1 {
		t.Fatalf("knowledge mirror requests = %d, want 1", len(writer.requests))
	}

	req := writer.requests[0]
	if req.Domain != sylkdir.DomainDoc {
		t.Fatalf("knowledge mirror domain = %v, want DomainDoc", req.Domain)
	}
	if req.Path == "" || req.DocumentID == "" {
		t.Fatalf("knowledge mirror request missing path/document id: %#v", req)
	}
	if req.Metadata["origin_source_type"] != "scribe" {
		t.Fatalf("knowledge mirror origin_source_type = %q, want scribe", req.Metadata["origin_source_type"])
	}
	if req.Metadata["ingest_agent_name"] != "scribe-engineer" {
		t.Fatalf("knowledge mirror ingest_agent_name = %q, want scribe-engineer", req.Metadata["ingest_agent_name"])
	}
}

func TestArchivalistStoreEntry_DoesNotMirrorNonScribeEntries(t *testing.T) {
	a := newTestArchivalist(t)
	writer := &fakeCommittedKnowledgeWriter{}
	a.knowledgeWriter = writer

	entry := makeEntry(CategoryGeneral, "Plain archival note.", SourceModelArchivalist)
	result := a.StoreEntry(context.Background(), entry)
	if !result.Success {
		t.Fatalf("store entry failed: %v", result.Error)
	}
	if len(writer.requests) != 0 {
		t.Fatalf("knowledge mirror requests = %d, want 0", len(writer.requests))
	}
}
