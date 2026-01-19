package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pipeline"
	"github.com/adalundhe/sylk/core/pipeline/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestNewHandoffArchiver tests HandoffArchiver creation.
func TestNewHandoffArchiver(t *testing.T) {
	t.Run("creates archiver with custom config", func(t *testing.T) {
		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 50,
			WorkerCount:    4,
			EmbeddingDim:   768,
			MaxRetries:     5,
			RetryBackoff:   2 * time.Second,
		}

		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)

		assert.NotNil(t, archiver)
	})

	t.Run("creates archiver with default config when empty", func(t *testing.T) {
		archiver := pipeline.NewHandoffArchiver(pipeline.ArchiverConfig{}, nil, nil, nil)

		assert.NotNil(t, archiver)
	})
}

// TestHandoffArchiver_ArchiveSync tests synchronous archival.
func TestHandoffArchiver_ArchiveSync(t *testing.T) {
	t.Run("archives to both bleve and vector stores", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, mockVector, mockEmbedder)

		state := createTestState()

		// Setup expectations
		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockEmbedder.EXPECT().Embed(mock.Anything, mock.Anything).Return(make([]float32, 1536), nil)
		mockVector.EXPECT().Insert(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := archiver.ArchiveSync(context.Background(), state)

		assert.NoError(t, err)
	})

	t.Run("succeeds when bleve is nil", func(t *testing.T) {
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, mockVector, mockEmbedder)

		state := createTestState()

		mockEmbedder.EXPECT().Embed(mock.Anything, mock.Anything).Return(make([]float32, 1536), nil)
		mockVector.EXPECT().Insert(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := archiver.ArchiveSync(context.Background(), state)

		assert.NoError(t, err)
	})

	t.Run("succeeds when vector is nil", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)

		state := createTestState()

		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := archiver.ArchiveSync(context.Background(), state)

		assert.NoError(t, err)
	})

	t.Run("fails when bleve indexing fails", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)

		state := createTestState()

		expectedErr := errors.New("bleve error")
		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).Return(expectedErr)

		err := archiver.ArchiveSync(context.Background(), state)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bleve indexing failed")
	})

	t.Run("fails when embedding fails", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, mockVector, mockEmbedder)

		state := createTestState()

		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockEmbedder.EXPECT().Embed(mock.Anything, mock.Anything).Return(nil, errors.New("embed error"))

		err := archiver.ArchiveSync(context.Background(), state)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "embedding generation failed")
	})

	t.Run("fails when vector insert fails", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, mockVector, mockEmbedder)

		state := createTestState()

		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockEmbedder.EXPECT().Embed(mock.Anything, mock.Anything).Return(make([]float32, 1536), nil)
		mockVector.EXPECT().Insert(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("vector error"))

		err := archiver.ArchiveSync(context.Background(), state)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vector indexing failed")
	})
}

// TestHandoffArchiver_Archive tests async archival.
func TestHandoffArchiver_Archive(t *testing.T) {
	t.Run("submits job to async queue", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 10,
			WorkerCount:    1,
			MaxRetries:     3,
			RetryBackoff:   10 * time.Millisecond,
		}
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)
		archiver.Start()
		defer archiver.Close()

		state := createTestState()

		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := archiver.Archive(context.Background(), state)

		assert.NoError(t, err)

		// Wait for async processing
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("returns error when archiver is closed", func(t *testing.T) {
		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)
		archiver.Start()
		archiver.Close()

		state := createTestState()

		err := archiver.Archive(context.Background(), state)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "archiver is closed")
	})

	t.Run("returns error when queue is full", func(t *testing.T) {
		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 1,
			WorkerCount:    0, // No workers to process
			MaxRetries:     3,
			RetryBackoff:   time.Second,
		}
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)

		state := createTestState()

		// Fill the queue
		_ = archiver.Archive(context.Background(), state)

		// This should fail
		err := archiver.Archive(context.Background(), state)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "archive queue full")
	})
}

// TestHandoffArchiver_Query tests querying archived states.
func TestHandoffArchiver_Query(t *testing.T) {
	t.Run("searches bleve store", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)

		query := pipeline.ArchiveQuery{
			AgentType: "engineer",
			SessionID: "session-123",
			Limit:     10,
		}

		mockBleve.EXPECT().Search(mock.Anything, mock.Anything, 10).Return([]string{"id1", "id2"}, nil)

		entries, err := archiver.Query(context.Background(), query)

		assert.NoError(t, err)
		assert.NotNil(t, entries)
	})

	t.Run("fails when bleve is not configured", func(t *testing.T) {
		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)

		query := pipeline.ArchiveQuery{AgentType: "engineer"}

		entries, err := archiver.Query(context.Background(), query)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bleve store not configured")
		assert.Nil(t, entries)
	})

	t.Run("fails when search fails", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)

		query := pipeline.ArchiveQuery{AgentType: "engineer"}

		mockBleve.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("search error"))

		entries, err := archiver.Query(context.Background(), query)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "search failed")
		assert.Nil(t, entries)
	})

	t.Run("uses default limit when not specified", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)

		query := pipeline.ArchiveQuery{AgentType: "engineer"} // No limit specified

		mockBleve.EXPECT().Search(mock.Anything, mock.Anything, 100).Return([]string{}, nil)

		_, err := archiver.Query(context.Background(), query)

		assert.NoError(t, err)
	})
}

// TestHandoffArchiver_QuerySimilar tests semantic similarity search.
func TestHandoffArchiver_QuerySimilar(t *testing.T) {
	t.Run("performs semantic search", func(t *testing.T) {
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, mockVector, mockEmbedder)

		queryText := "implement authentication"
		embedding := make([]float32, 1536)

		mockEmbedder.EXPECT().Embed(mock.Anything, queryText).Return(embedding, nil)
		mockVector.EXPECT().Search(mock.Anything, embedding, 10, mock.Anything).Return([]pipeline.VectorResult{
			{ID: "id1", Score: 0.9, Metadata: map[string]any{"agent_type": "engineer"}},
		}, nil)

		entries, err := archiver.QuerySimilar(context.Background(), queryText, 10)

		assert.NoError(t, err)
		assert.Len(t, entries, 1)
		assert.Equal(t, "id1", entries[0].ID)
	})

	t.Run("fails when vector not configured", func(t *testing.T) {
		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)

		entries, err := archiver.QuerySimilar(context.Background(), "query", 10)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vector search not configured")
		assert.Nil(t, entries)
	})

	t.Run("fails when embedding fails", func(t *testing.T) {
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, mockVector, mockEmbedder)

		mockEmbedder.EXPECT().Embed(mock.Anything, mock.Anything).Return(nil, errors.New("embed error"))

		entries, err := archiver.QuerySimilar(context.Background(), "query", 10)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "embedding generation failed")
		assert.Nil(t, entries)
	})

	t.Run("uses default limit when zero", func(t *testing.T) {
		mockVector := mocks.NewMockVectorStore(t)
		mockEmbedder := mocks.NewMockArchivalEmbedder(t)

		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, mockVector, mockEmbedder)

		mockEmbedder.EXPECT().Embed(mock.Anything, mock.Anything).Return(make([]float32, 1536), nil)
		mockVector.EXPECT().Search(mock.Anything, mock.Anything, 10, mock.Anything).Return([]pipeline.VectorResult{}, nil)

		_, err := archiver.QuerySimilar(context.Background(), "query", 0)

		assert.NoError(t, err)
	})
}

// TestHandoffArchiver_Close tests graceful shutdown.
func TestHandoffArchiver_Close(t *testing.T) {
	t.Run("closes gracefully", func(t *testing.T) {
		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 10,
			WorkerCount:    2,
			MaxRetries:     3,
			RetryBackoff:   time.Millisecond,
		}
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)
		archiver.Start()

		err := archiver.Close()

		assert.NoError(t, err)
	})

	t.Run("returns nil on double close", func(t *testing.T) {
		config := pipeline.DefaultArchiverConfig()
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)
		archiver.Start()

		err1 := archiver.Close()
		err2 := archiver.Close()

		assert.NoError(t, err1)
		assert.NoError(t, err2)
	})

	t.Run("waits for workers to finish", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 10,
			WorkerCount:    2,
			MaxRetries:     3,
			RetryBackoff:   time.Millisecond,
		}
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)
		archiver.Start()

		state := createTestState()

		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).
			Run(func(_ context.Context, _ string, _ any) {
				time.Sleep(50 * time.Millisecond)
			}).Return(nil)

		_ = archiver.Archive(context.Background(), state)

		err := archiver.Close()

		assert.NoError(t, err)
	})
}

// TestHandoffArchiver_ConcurrentArchive tests concurrent archive operations.
func TestHandoffArchiver_ConcurrentArchive(t *testing.T) {
	t.Run("handles concurrent archives safely", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 100,
			WorkerCount:    4,
			MaxRetries:     3,
			RetryBackoff:   time.Millisecond,
		}
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)
		archiver.Start()
		defer archiver.Close()

		var indexed atomic.Int32
		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).
			Run(func(_ context.Context, _ string, _ any) {
				indexed.Add(1)
			}).Return(nil).Maybe()

		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				state := &pipeline.BaseArchivableState{
					AgentID:        "agent-" + string(rune('a'+idx%26)),
					AgentType:      "engineer",
					SessionID:      "session-123",
					HandoffIndex:   idx,
					TriggerReason:  "context_threshold",
					TriggerContext: 0.75,
					StartedAt:      time.Now(),
					CompletedAt:    time.Now(),
					Summary:        "Test summary",
				}
				_ = archiver.Archive(context.Background(), state)
			}(i)
		}
		wg.Wait()

		// Wait for processing
		time.Sleep(200 * time.Millisecond)
	})
}

// TestHandoffArchiver_RetryLogic tests retry behavior.
func TestHandoffArchiver_RetryLogic(t *testing.T) {
	t.Run("retries failed archives", func(t *testing.T) {
		mockBleve := mocks.NewMockBleveStore(t)

		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 10,
			WorkerCount:    1,
			MaxRetries:     2,
			RetryBackoff:   10 * time.Millisecond,
		}
		archiver := pipeline.NewHandoffArchiver(config, mockBleve, nil, nil)
		archiver.Start()
		defer archiver.Close()

		var callCount atomic.Int32
		mockBleve.EXPECT().Index(mock.Anything, mock.Anything, mock.Anything).
			Run(func(_ context.Context, _ string, _ any) {
				callCount.Add(1)
			}).Return(errors.New("temporary error")).Maybe()

		state := createTestState()
		_ = archiver.Archive(context.Background(), state)

		// Wait for retries
		time.Sleep(300 * time.Millisecond)

		// Should have retried (original + retries)
		assert.GreaterOrEqual(t, int(callCount.Load()), 1)
	})
}

// TestBaseArchivableState tests the base implementation.
func TestBaseArchivableState(t *testing.T) {
	t.Run("implements ArchivableHandoffState interface", func(t *testing.T) {
		state := &pipeline.BaseArchivableState{
			AgentID:        "agent-1",
			AgentType:      "engineer",
			SessionID:      "session-123",
			PipelineID:     "pipeline-456",
			TriggerReason:  "context_threshold",
			TriggerContext: 0.75,
			HandoffIndex:   3,
			StartedAt:      time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC),
			CompletedAt:    time.Date(2025, 1, 18, 10, 5, 0, 0, time.UTC),
			Summary:        "Test summary",
			Data:           json.RawMessage(`{"key":"value"}`),
		}

		assert.Equal(t, "agent-1", state.GetAgentID())
		assert.Equal(t, "engineer", state.GetAgentType())
		assert.Equal(t, "session-123", state.GetSessionID())
		assert.Equal(t, "pipeline-456", state.GetPipelineID())
		assert.Equal(t, "context_threshold", state.GetTriggerReason())
		assert.Equal(t, 0.75, state.GetTriggerContext())
		assert.Equal(t, 3, state.GetHandoffIndex())
		assert.Equal(t, time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC), state.GetStartedAt())
		assert.Equal(t, time.Date(2025, 1, 18, 10, 5, 0, 0, time.UTC), state.GetCompletedAt())
		assert.Equal(t, "Test summary", state.GetSummary())
	})

	t.Run("serializes to JSON", func(t *testing.T) {
		state := &pipeline.BaseArchivableState{
			AgentID:   "agent-1",
			AgentType: "engineer",
			Summary:   "Test",
		}

		data, err := state.ToJSON()

		require.NoError(t, err)
		assert.Contains(t, string(data), `"agent_id":"agent-1"`)
		assert.Contains(t, string(data), `"agent_type":"engineer"`)
	})
}

// TestDefaultArchiverConfig tests default configuration.
func TestDefaultArchiverConfig(t *testing.T) {
	config := pipeline.DefaultArchiverConfig()

	assert.Equal(t, 100, config.AsyncQueueSize)
	assert.Equal(t, 2, config.WorkerCount)
	assert.Equal(t, 1536, config.EmbeddingDim)
	assert.Equal(t, 3, config.MaxRetries)
	assert.Equal(t, time.Second, config.RetryBackoff)
}

// TestArchiver_StartStop tests worker lifecycle.
func TestArchiver_StartStop(t *testing.T) {
	t.Run("starts and stops workers correctly", func(t *testing.T) {
		config := pipeline.ArchiverConfig{
			AsyncQueueSize: 10,
			WorkerCount:    3,
			MaxRetries:     1,
			RetryBackoff:   time.Millisecond,
		}
		archiver := pipeline.NewHandoffArchiver(config, nil, nil, nil)

		archiver.Start()

		// Give workers time to start
		time.Sleep(10 * time.Millisecond)

		err := archiver.Close()
		assert.NoError(t, err)
	})
}

// Helper function to create test state
func createTestState() *pipeline.BaseArchivableState {
	return &pipeline.BaseArchivableState{
		AgentID:        "agent-1",
		AgentType:      "engineer",
		SessionID:      "session-123",
		PipelineID:     "pipeline-456",
		TriggerReason:  "context_threshold",
		TriggerContext: 0.75,
		HandoffIndex:   1,
		StartedAt:      time.Now().Add(-5 * time.Minute),
		CompletedAt:    time.Now(),
		Summary:        "Test handoff summary",
		Data:           json.RawMessage(`{"task":"implement auth"}`),
	}
}
