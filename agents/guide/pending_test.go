package guide_test

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPendingStore_NewPendingStore tests creating pending store
func TestPendingStore_NewPendingStore(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())
	require.NotNil(t, store)

	assert.Equal(t, 0, store.Count())
}

// TestPendingStore_Add tests adding pending requests
func TestPendingStore_Add(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	req := &guide.RouteRequest{
		Input:           "test query",
		SourceAgentID:   "source-1",
		SourceAgentName: "Source Agent",
	}

	classification := &guide.RouteResult{
		Intent: guide.IntentRecall,
		Domain: guide.DomainPatterns,
	}

	// Add request
	corrID := store.Add(req, classification, "target-1")
	assert.NotEmpty(t, corrID)
	assert.Equal(t, 1, store.Count())
}

// TestPendingStore_AddWithCorrelationID tests adding with pre-set correlation ID
func TestPendingStore_AddWithCorrelationID(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	req := &guide.RouteRequest{
		Input:         "test query",
		SourceAgentID: "source-1",
		CorrelationID: "custom-corr-id",
	}

	corrID := store.Add(req, nil, "target-1")
	assert.Equal(t, "custom-corr-id", corrID)
}

// TestPendingStore_Get tests retrieving pending requests
func TestPendingStore_Get(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	req := &guide.RouteRequest{
		Input:         "test query",
		SourceAgentID: "source-1",
	}

	corrID := store.Add(req, nil, "target-1")

	// Get existing
	pending := store.Get(corrID)
	require.NotNil(t, pending)
	assert.Equal(t, corrID, pending.CorrelationID)
	assert.Equal(t, "source-1", pending.SourceAgentID)
	assert.Equal(t, "target-1", pending.TargetAgentID)

	// Get non-existent
	pending = store.Get("non-existent")
	assert.Nil(t, pending)
}

// TestPendingStore_Remove tests removing pending requests
func TestPendingStore_Remove(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	req := &guide.RouteRequest{
		Input:         "test query",
		SourceAgentID: "source-1",
	}

	corrID := store.Add(req, nil, "target-1")
	assert.Equal(t, 1, store.Count())

	// Remove
	pending := store.Remove(corrID)
	require.NotNil(t, pending)
	assert.Equal(t, corrID, pending.CorrelationID)
	assert.Equal(t, 0, store.Count())

	// Remove again should return nil
	pending = store.Remove(corrID)
	assert.Nil(t, pending)
}

// TestPendingStore_GetBySource tests getting requests by source
func TestPendingStore_GetBySource(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	// Add multiple requests from same source
	for i := 0; i < 3; i++ {
		store.Add(&guide.RouteRequest{
			Input:         "query",
			SourceAgentID: "source-1",
		}, nil, "target-1")
	}

	// Add request from different source
	store.Add(&guide.RouteRequest{
		Input:         "query",
		SourceAgentID: "source-2",
	}, nil, "target-1")

	// Get by source
	source1Requests := store.GetBySource("source-1")
	assert.Len(t, source1Requests, 3)

	source2Requests := store.GetBySource("source-2")
	assert.Len(t, source2Requests, 1)

	unknownRequests := store.GetBySource("unknown")
	assert.Nil(t, unknownRequests)
}

// TestPendingStore_GetByTarget tests getting requests by target
func TestPendingStore_GetByTarget(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	// Add requests to different targets
	store.Add(&guide.RouteRequest{Input: "q1", SourceAgentID: "s1"}, nil, "target-1")
	store.Add(&guide.RouteRequest{Input: "q2", SourceAgentID: "s2"}, nil, "target-1")
	store.Add(&guide.RouteRequest{Input: "q3", SourceAgentID: "s3"}, nil, "target-2")

	// Get by target
	target1Requests := store.GetByTarget("target-1")
	assert.Len(t, target1Requests, 2)

	target2Requests := store.GetByTarget("target-2")
	assert.Len(t, target2Requests, 1)
}

// TestPendingStore_CleanupExpired tests expiration cleanup
func TestPendingStore_CleanupExpired(t *testing.T) {
	// Use very short timeout for testing
	store := guide.NewPendingStore(guide.PendingStoreConfig{
		DefaultTimeout: 50 * time.Millisecond,
		MaxPerAgent:    1000,
	})

	// Add requests
	store.Add(&guide.RouteRequest{Input: "q1", SourceAgentID: "s1"}, nil, "t1")
	store.Add(&guide.RouteRequest{Input: "q2", SourceAgentID: "s2"}, nil, "t2")
	assert.Equal(t, 2, store.Count())

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Cleanup
	expired := store.CleanupExpired()
	assert.Len(t, expired, 2)
	assert.Equal(t, 0, store.Count())
}

// TestPendingStore_Stats tests statistics
func TestPendingStore_Stats(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	store.Add(&guide.RouteRequest{Input: "q1", SourceAgentID: "s1"}, nil, "t1")
	store.Add(&guide.RouteRequest{Input: "q2", SourceAgentID: "s1"}, nil, "t2")
	store.Add(&guide.RouteRequest{Input: "q3", SourceAgentID: "s2"}, nil, "t1")

	stats := store.Stats()

	assert.Equal(t, 3, stats.TotalPending)
	assert.Equal(t, 2, stats.BySource["s1"])
	assert.Equal(t, 1, stats.BySource["s2"])
	assert.Equal(t, 2, stats.ByTarget["t1"])
	assert.Equal(t, 1, stats.ByTarget["t2"])
	assert.False(t, stats.OldestPending.IsZero())
	assert.False(t, stats.NextExpiration.IsZero())
}

// TestPendingStore_CountBySource tests count by source
func TestPendingStore_CountBySource(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	store.Add(&guide.RouteRequest{Input: "q1", SourceAgentID: "s1"}, nil, "t1")
	store.Add(&guide.RouteRequest{Input: "q2", SourceAgentID: "s1"}, nil, "t2")

	assert.Equal(t, 2, store.CountBySource("s1"))
	assert.Equal(t, 0, store.CountBySource("unknown"))
}

// TestPendingStore_CountByTarget tests count by target
func TestPendingStore_CountByTarget(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	store.Add(&guide.RouteRequest{Input: "q1", SourceAgentID: "s1"}, nil, "t1")
	store.Add(&guide.RouteRequest{Input: "q2", SourceAgentID: "s2"}, nil, "t1")

	assert.Equal(t, 2, store.CountByTarget("t1"))
	assert.Equal(t, 0, store.CountByTarget("unknown"))
}

// TestPendingStore_RemoveUpdatesIndexes tests that remove properly updates indexes
func TestPendingStore_RemoveUpdatesIndexes(t *testing.T) {
	store := guide.NewPendingStore(guide.DefaultPendingStoreConfig())

	corrID := store.Add(&guide.RouteRequest{Input: "q1", SourceAgentID: "s1"}, nil, "t1")

	assert.Equal(t, 1, store.CountBySource("s1"))
	assert.Equal(t, 1, store.CountByTarget("t1"))

	store.Remove(corrID)

	assert.Equal(t, 0, store.CountBySource("s1"))
	assert.Equal(t, 0, store.CountByTarget("t1"))
}

// TestPendingStore_DefaultConfig tests default config values
func TestPendingStore_DefaultConfig(t *testing.T) {
	cfg := guide.DefaultPendingStoreConfig()
	assert.Equal(t, 5*time.Minute, cfg.DefaultTimeout)
	assert.Equal(t, 1000, cfg.MaxPerAgent)
}

// TestPendingStore_ZeroConfig tests handling of zero config values
func TestPendingStore_ZeroConfig(t *testing.T) {
	// Should use defaults for zero values
	store := guide.NewPendingStore(guide.PendingStoreConfig{})
	require.NotNil(t, store)
}
