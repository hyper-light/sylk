package mitigations

import (
	"os"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*vectorgraphdb.VectorGraphDB, func()) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	require.NoError(t, err)
	tmpFile.Close()

	db, err := vectorgraphdb.Open(tmpFile.Name())
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

func makeTestEmbedding() []float32 {
	embedding := make([]float32, 768)
	for i := range embedding {
		embedding[i] = 0.1
	}
	return embedding
}
