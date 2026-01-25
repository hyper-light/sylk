package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestUnifiedIndexer_Sylk(t *testing.T) {
	projectRoot := findProjectRoot(t)
	tmpDir := t.TempDir()
	vectorDir := filepath.Join(tmpDir, "vectors")
	if err := os.MkdirAll(vectorDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	indexer := NewUnifiedIndexer(UnifiedIndexerConfig{
		RootPath:    projectRoot,
		VectorDir:   vectorDir,
		SkipPersist: true,
		SkipVector:  false,
		EmbedderConfig: embedder.FactoryConfig{
			ForceLocal:    true,
			SkipModelLoad: true,
		},
	})

	result, err := indexer.Index(context.Background())
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	if result.CodeResult == nil {
		t.Fatal("CodeResult is nil")
	}

	if result.VectorResult == nil {
		t.Fatal("VectorResult is nil")
	}

	t.Logf("=== Unified Indexer Results ===")
	t.Logf("Code: %d files, %d symbols in %v",
		result.CodeResult.TotalFiles,
		result.CodeResult.TotalSymbols,
		result.CodeResult.TotalDuration)
	t.Logf("Vector: %d symbols, source=%s in %v",
		result.VectorResult.SymbolCount,
		result.VectorResult.EmbedderSource,
		result.VectorResult.Duration)
	t.Logf("Total time: %v", result.TotalTime)

	vectorStore, err := storage.OpenVectorStore(filepath.Join(vectorDir, "vectors.bin"))
	if err != nil {
		t.Fatalf("OpenVectorStore: %v", err)
	}
	defer vectorStore.Close()

	if vectorStore.Dimension() != 1024 {
		t.Errorf("Expected 1024d vectors, got %d", vectorStore.Dimension())
	}

	if int(vectorStore.Count()) != result.VectorResult.SymbolCount {
		t.Errorf("Vector count mismatch: store=%d, result=%d",
			vectorStore.Count(), result.VectorResult.SymbolCount)
	}

	t.Logf("Verified: %d vectors at %dd", vectorStore.Count(), vectorStore.Dimension())
}

func TestUnifiedIndexer_SkipVector(t *testing.T) {
	projectRoot := findProjectRoot(t)
	tmpDir := t.TempDir()

	indexer := NewUnifiedIndexer(UnifiedIndexerConfig{
		RootPath:    projectRoot,
		SkipPersist: true,
		SkipVector:  true,
	})

	result, err := indexer.Index(context.Background())
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	if result.CodeResult == nil {
		t.Fatal("CodeResult should not be nil")
	}

	if result.VectorResult != nil {
		t.Error("VectorResult should be nil when SkipVector=true")
	}

	t.Logf("Indexed %d symbols (vector skipped)", result.CodeResult.TotalSymbols)
	_ = tmpDir
}
