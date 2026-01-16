package guide_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Capability Index Tests
// =============================================================================

func TestCapabilityIndex_Index(t *testing.T) {
	ci := guide.NewCapabilityIndex()

	agent := &guide.AgentRegistration{
		ID:   "test-agent",
		Name: "test",
		Capabilities: guide.AgentCapabilities{
			Intents:  []guide.Intent{guide.IntentRecall, guide.IntentStore},
			Domains:  []guide.Domain{guide.DomainPatterns, guide.DomainFailures},
			Tags:     []string{"history", "memory"},
			Keywords: []string{"remember", "recall", "store"},
		},
	}

	ci.Index(agent)

	stats := ci.Stats()
	assert.Equal(t, 1, stats.TotalAgents)
	assert.Equal(t, 2, stats.IntentCount)
	assert.Equal(t, 2, stats.DomainCount)
	assert.Equal(t, 2, stats.TagCount)
	assert.Equal(t, 3, stats.KeywordCount)
}

func TestCapabilityIndex_FindByIntent(t *testing.T) {
	ci := guide.NewCapabilityIndex()

	agent1 := &guide.AgentRegistration{
		ID:   "agent1",
		Name: "agent1",
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall},
		},
	}
	agent2 := &guide.AgentRegistration{
		ID:   "agent2",
		Name: "agent2",
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentStore},
		},
	}

	ci.Index(agent1)
	ci.Index(agent2)

	recallAgents := ci.FindByIntent(guide.IntentRecall)
	require.Len(t, recallAgents, 1)
	assert.Equal(t, "agent1", recallAgents[0].ID)

	storeAgents := ci.FindByIntent(guide.IntentStore)
	require.Len(t, storeAgents, 1)
	assert.Equal(t, "agent2", storeAgents[0].ID)
}

func TestCapabilityIndex_FindByKeyword(t *testing.T) {
	ci := guide.NewCapabilityIndex()

	agent := &guide.AgentRegistration{
		ID:   "memory-agent",
		Name: "memory",
		Capabilities: guide.AgentCapabilities{
			Keywords: []string{"remember", "forget", "MEMORY"},
		},
	}

	ci.Index(agent)

	// Case-insensitive lookup
	agents := ci.FindByKeyword("Remember")
	require.Len(t, agents, 1)
	assert.Equal(t, "memory-agent", agents[0].ID)

	agents = ci.FindByKeyword("memory")
	require.Len(t, agents, 1)
}

func TestCapabilityIndex_Remove(t *testing.T) {
	ci := guide.NewCapabilityIndex()

	agent := &guide.AgentRegistration{
		ID:   "test-agent",
		Name: "test",
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall},
		},
	}

	ci.Index(agent)
	assert.Equal(t, 1, ci.Count())

	ci.Remove("test-agent")
	assert.Equal(t, 0, ci.Count())

	agents := ci.FindByIntent(guide.IntentRecall)
	assert.Empty(t, agents)
}

// =============================================================================
// Session Routing Tests
// =============================================================================

func TestSessionRouter_GetOrCreateSession(t *testing.T) {
	// Create a mock guide (we only need the router field)
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	sr := guide.NewSessionRouter(g)

	// Create session
	cache1 := sr.GetOrCreateSession("session1")
	require.NotNil(t, cache1)

	// Get same session
	cache2 := sr.GetOrCreateSession("session1")
	assert.Equal(t, cache1, cache2)

	// Stats
	stats := sr.Stats()
	assert.Equal(t, 1, stats.TotalSessions)
	assert.Equal(t, 1, stats.ActiveSessions)
}

func TestSessionRouter_SetPreferredAgent(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	sr := guide.NewSessionRouter(g)
	sr.GetOrCreateSession("session1")

	sr.SetPreferredAgent("session1", "archivalist", 10)

	prefs := sr.GetSessionPrefs("session1")
	assert.Equal(t, 10, prefs.PreferredAgents["archivalist"])
}

func TestSessionRouter_BlockAgent(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	sr := guide.NewSessionRouter(g)
	sr.GetOrCreateSession("session1")

	sr.BlockAgent("session1", "blocked-agent")

	prefs := sr.GetSessionPrefs("session1")
	assert.True(t, prefs.BlockedAgents["blocked-agent"])

	sr.UnblockAgent("session1", "blocked-agent")
	prefs = sr.GetSessionPrefs("session1")
	assert.False(t, prefs.BlockedAgents["blocked-agent"])
}

func TestSessionRouter_RemoveSession(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	sr := guide.NewSessionRouter(g)
	sr.GetOrCreateSession("session1")
	sr.GetOrCreateSession("session2")

	stats := sr.Stats()
	assert.Equal(t, 2, stats.ActiveSessions)

	sr.RemoveSession("session1")

	stats = sr.Stats()
	assert.Equal(t, 1, stats.ActiveSessions)
}

// =============================================================================
// Route Versioning Tests
// =============================================================================

func TestRouteVersionStore_CreateVersion(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())
	store := guide.NewRouteVersionStore(cache)

	// Initial version is 1
	assert.Equal(t, 1, store.GetCurrentVersion())

	// Add a route
	err := store.AddRoute(&guide.VersionedRoute{
		Input:         "test query",
		TargetAgentID: "archivalist",
		Intent:        guide.IntentRecall,
		Domain:        guide.DomainPatterns,
		Source:        guide.RouteSourceManual,
	})
	require.NoError(t, err)

	// Create new version
	v2 := store.CreateVersion("test", "Added test route")
	assert.Equal(t, 2, v2)

	// List versions
	versions := store.ListVersions()
	assert.Len(t, versions, 2)
}

func TestRouteVersionStore_AddRemoveRoute(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())
	store := guide.NewRouteVersionStore(cache)

	// Add route
	err := store.AddRoute(&guide.VersionedRoute{
		ID:            "route1",
		Input:         "test query",
		TargetAgentID: "archivalist",
		Intent:        guide.IntentRecall,
		Domain:        guide.DomainPatterns,
	})
	require.NoError(t, err)

	// Verify route exists
	route := store.GetRoute("route1")
	require.NotNil(t, route)
	assert.Equal(t, "test query", route.Input)

	// Remove route
	err = store.RemoveRoute("route1")
	require.NoError(t, err)

	route = store.GetRoute("route1")
	assert.Nil(t, route)
}

func TestRouteVersionStore_DeprecateRoute(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())
	store := guide.NewRouteVersionStore(cache)

	// Add route
	err := store.AddRoute(&guide.VersionedRoute{
		ID:            "old-route",
		Input:         "old query",
		TargetAgentID: "archivalist",
	})
	require.NoError(t, err)

	// Deprecate
	err = store.DeprecateRoute("old-route", "replaced with better version", "new-route")
	require.NoError(t, err)

	route := store.GetRoute("old-route")
	assert.True(t, route.Deprecated)
	assert.Equal(t, "replaced with better version", route.DeprecatedReason)
	assert.Equal(t, "new-route", route.ReplacedBy)
}

func TestRouteVersionStore_Rollback(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())
	store := guide.NewRouteVersionStore(cache)

	// Add route in v1
	err := store.AddRoute(&guide.VersionedRoute{
		ID:            "route1",
		Input:         "query1",
		TargetAgentID: "agent1",
	})
	require.NoError(t, err)

	// Create v2 to capture v1 state
	v2 := store.CreateVersion("test", "v2")
	assert.Equal(t, 2, v2)

	// Add route in v2
	err = store.AddRoute(&guide.VersionedRoute{
		ID:            "route2",
		Input:         "query2",
		TargetAgentID: "agent2",
	})
	require.NoError(t, err)

	// Create v3 to capture v2 state
	store.CreateVersion("test", "v3")

	// Verify both routes exist
	assert.NotNil(t, store.GetRoute("route1"))
	assert.NotNil(t, store.GetRoute("route2"))

	// Rollback to v2 (which has route1 but not route2)
	err = store.Rollback(2)
	require.NoError(t, err)

	// Only route1 should exist
	assert.NotNil(t, store.GetRoute("route1"))
	assert.Nil(t, store.GetRoute("route2"))
}

func TestRouteVersionStore_Diff(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())
	store := guide.NewRouteVersionStore(cache)

	// Add route in v1
	store.AddRoute(&guide.VersionedRoute{
		ID:            "route1",
		Input:         "query1",
		TargetAgentID: "agent1",
	})

	// Create v2
	store.CreateVersion("test", "v2")

	// Modify route in v2
	store.UpdateRoute("route1", func(r *guide.VersionedRoute) {
		r.TargetAgentID = "agent2"
	})

	// Add new route in v2
	store.AddRoute(&guide.VersionedRoute{
		ID:            "route2",
		Input:         "query2",
		TargetAgentID: "agent3",
	})

	// Create v3 to capture changes
	store.CreateVersion("test", "v3")

	// Diff v2 and v3
	diff, err := store.Diff(2, 3)
	require.NoError(t, err)

	assert.Len(t, diff.Added, 1)
	assert.Len(t, diff.Modified, 1)
	assert.Empty(t, diff.Removed)
}

// =============================================================================
// Batch Processor Tests
// =============================================================================

func TestBatchProcessor_Add(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	cfg := guide.DefaultBatchConfig()
	cfg.AutoFlush = false // Manual control for testing
	bp := guide.NewBatchProcessor(g, cfg)
	bp.Start()
	defer bp.Stop()

	// Create a request
	req := &guide.RouteRequest{
		Input:         "@archivalist:recall:patterns",
		SourceAgentID: "test-agent",
	}

	// Add to batch
	resultCh := bp.Add(context.Background(), req)
	require.NotNil(t, resultCh)

	// Get result
	select {
	case result := <-resultCh:
		require.NotNil(t, result)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestBatchProcessor_AddBatch(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	cfg := guide.DefaultBatchConfig()
	cfg.AutoFlush = true
	cfg.MaxWaitTime = 10 * time.Millisecond
	bp := guide.NewBatchProcessor(g, cfg)
	bp.Start()
	defer bp.Stop()

	// Create multiple DSL requests (these don't need LLM)
	requests := []*guide.RouteRequest{
		{Input: "@archivalist:recall:patterns", SourceAgentID: "test"},
		{Input: "@guide:status:system", SourceAgentID: "test"},
	}

	// Add batch
	channels := bp.AddBatch(context.Background(), requests)
	assert.Len(t, channels, 2)

	// Collect results with longer timeout
	for i, ch := range channels {
		select {
		case result := <-ch:
			require.NotNil(t, result, "result %d should not be nil", i)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for result %d", i)
		}
	}
}

func TestBatchProcessor_Stats(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	cfg := guide.DefaultBatchConfig()
	cfg.AutoFlush = true
	cfg.MaxWaitTime = 10 * time.Millisecond
	bp := guide.NewBatchProcessor(g, cfg)
	bp.Start()
	defer bp.Stop()

	// Add a single request and wait for it
	req := &guide.RouteRequest{
		Input:         "@archivalist:recall:patterns",
		SourceAgentID: "test",
	}
	ch := bp.Add(context.Background(), req)
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for request")
	}

	// Just verify stats method doesn't panic
	stats := bp.Stats()
	_ = stats
}

// =============================================================================
// Response Streaming Tests
// =============================================================================

func TestStreamManager_CreateStream(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream, err := sm.CreateStream("corr-123", "session-1")
	require.NoError(t, err)
	require.NotNil(t, stream)

	assert.Equal(t, "corr-123", stream.CorrelationID)
	assert.Equal(t, "session-1", stream.SessionID)
}

func TestStreamManager_GetStream(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	// Create stream
	stream1, _ := sm.CreateStream("corr-123", "session-1")

	// Get same stream
	stream2 := sm.GetStream("corr-123")
	assert.Equal(t, stream1, stream2)

	// Get non-existent stream
	stream3 := sm.GetStream("corr-999")
	assert.Nil(t, stream3)
}

func TestResponseStream_SendData(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, _ := sm.CreateStream("corr-123", "session-1")

	// Drain the start event
	<-stream.Events()

	// Send data
	ok := stream.SendData(map[string]string{"key": "value"})
	assert.True(t, ok)

	// Read event
	event := <-stream.Events()
	assert.Equal(t, guide.StreamEventData, event.Type)
	assert.NotNil(t, event.Data)
}

func TestResponseStream_SendText(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, _ := sm.CreateStream("corr-123", "session-1")

	// Drain start event
	<-stream.Events()

	// Send text
	ok := stream.SendText("Hello, ")
	assert.True(t, ok)
	ok = stream.SendText("World!")
	assert.True(t, ok)

	// Read events
	event1 := <-stream.Events()
	assert.Equal(t, "Hello, ", event1.Text)

	event2 := <-stream.Events()
	assert.Equal(t, "World!", event2.Text)
}

func TestResponseStream_SendProgress(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, _ := sm.CreateStream("corr-123", "session-1")

	// Drain start event
	<-stream.Events()

	// Send progress
	ok := stream.SendProgress(50, 100, "Half done")
	assert.True(t, ok)

	// Read event
	event := <-stream.Events()
	assert.Equal(t, guide.StreamEventProgress, event.Type)

	progress := event.Data.(*guide.ProgressData)
	assert.Equal(t, 50, progress.Current)
	assert.Equal(t, 100, progress.Total)
	assert.Equal(t, 50.0, progress.Percent)
}

func TestResponseStream_Close(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, _ := sm.CreateStream("corr-123", "session-1")

	// Close stream
	stream.Close()

	// Should be closed
	assert.True(t, stream.IsClosed())

	// Should not be able to send
	ok := stream.SendData("test")
	assert.False(t, ok)
}

func TestStreamConsumer_CollectText(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, _ := sm.CreateStream("corr-123", "session-1")

	// Send text in goroutine
	go func() {
		// Drain start event first
		time.Sleep(10 * time.Millisecond)
		stream.SendText("Hello, ")
		stream.SendText("World!")
		stream.Close()
	}()

	// Create consumer
	ctx := context.Background()
	consumer := guide.NewStreamConsumer(ctx, stream)

	// Collect all text
	text := consumer.CollectText()
	assert.Equal(t, "Hello, World!", text)
}

func TestStreamManager_Stats(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	// Create some streams
	sm.CreateStream("corr-1", "session-1")
	sm.CreateStream("corr-2", "session-1")
	sm.CreateStream("corr-3", "session-2")

	stats := sm.Stats()
	assert.Equal(t, 3, stats.ActiveStreams)
	assert.Equal(t, int64(3), stats.TotalStreams)
}

func TestStreamManager_CloseStream(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream, _ := sm.CreateStream("corr-123", "session-1")
	assert.False(t, stream.IsClosed())

	sm.CloseStream("corr-123")

	assert.True(t, stream.IsClosed())
	assert.Nil(t, sm.GetStream("corr-123"))
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestCapabilityIndex_ConcurrentAccess(t *testing.T) {
	ci := guide.NewCapabilityIndex()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			agent := &guide.AgentRegistration{
				ID:   "agent-" + string(rune('a'+id%26)),
				Name: "agent",
				Capabilities: guide.AgentCapabilities{
					Intents: []guide.Intent{guide.IntentRecall},
				},
			}
			ci.Index(agent)
			ci.FindByIntent(guide.IntentRecall)
			ci.Remove(agent.ID)
		}(i)
	}
	wg.Wait()
}

func TestSessionRouter_ConcurrentSessions(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	g, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	sr := guide.NewSessionRouter(g)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			sessionID := "session-" + string(rune('a'+id%26))
			sr.GetOrCreateSession(sessionID)
			sr.SetPreferredAgent(sessionID, "agent", 10)
			_ = sr.GetSessionPrefs(sessionID)
		}(i)
	}
	wg.Wait()
}

func TestStreamManager_ConcurrentStreams(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	// Create streams concurrently
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			corrID := "corr-" + string(rune('a'+id%26))
			stream, err := sm.CreateStream(corrID, "session-1")
			if err != nil {
				return
			}

			// Just do operations on the stream
			stream.SendData("test")
			stream.SendText("text")
			_ = stream.Stats()
		}(i)
	}
	wg.Wait()

	// Clean up sequentially after all operations complete
	for i := 0; i < 26; i++ {
		corrID := "corr-" + string(rune('a'+i))
		sm.CloseStream(corrID)
	}

	stats := sm.Stats()
	assert.Equal(t, 0, stats.ActiveStreams)
}
