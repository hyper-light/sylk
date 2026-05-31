package knowledgeruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

func TestCommittedKnowledgeBackend_RefreshExistingFromDiskDoesNotCreateMissingBleve(t *testing.T) {
	projectRoot := t.TempDir()
	sd := sylkdir.New(projectRoot)
	if err := sd.Init(); err != nil {
		t.Fatalf("init sylkdir: %v", err)
	}
	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("load global meta: %v", err)
	}

	backend := NewCommittedKnowledgeBackend(projectRoot, nil)
	defer backend.Close()

	err := backend.RefreshExistingFromDisk(context.Background())
	if !errors.Is(err, sylkdir.ErrBleveHeadUnavailable) {
		t.Fatalf("RefreshExistingFromDisk error = %v, want ErrBleveHeadUnavailable", err)
	}
	blevePath := filepath.Join(sylkdir.NewGlobalVersionBleveStore(sd, gm.GetHead()).HeadBlevePath(), "documents.bleve")
	if _, statErr := os.Stat(blevePath); statErr == nil {
		t.Fatalf("RefreshExistingFromDisk created missing Bleve index at %s", blevePath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat missing Bleve index: %v", statErr)
	}
}

func TestCommittedKnowledgeBackend_UpsertTextDocument(t *testing.T) {
	projectRoot := t.TempDir()
	if err := sylkdir.New(projectRoot).Init(); err != nil {
		t.Fatalf("init sylkdir: %v", err)
	}

	backend := NewCommittedKnowledgeBackend(projectRoot, nil)
	defer backend.Close()

	ctx := context.Background()
	req := &TextDocumentIngestRequest{
		DocumentID: "scribe-doc-1",
		Path:       "archivalist/scribe/engineer/entry-1.md",
		Content:    "initial scribe note about retry handling",
		DocType:    search.DocTypeMarkdown,
		Language:   "markdown",
		Domain:     sylkdir.DomainDoc,
	}
	if err := backend.UpsertTextDocument(ctx, req); err != nil {
		t.Fatalf("upsert initial text document: %v", err)
	}

	req.Content = "updated scribe note about activation guards"
	if err := backend.UpsertTextDocument(ctx, req); err != nil {
		t.Fatalf("upsert replacement text document: %v", err)
	}

	result, err := backend.Search(ctx, &search.SearchRequest{
		Query: "activation guards",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("search updated text document: %v", err)
	}
	if len(result.Hits) == 0 {
		t.Fatalf("search hits = 0, want at least 1")
	}
	if got, want := result.Hits[0].Path, req.Path; got != want {
		t.Fatalf("hit path = %q, want %q", got, want)
	}
}
