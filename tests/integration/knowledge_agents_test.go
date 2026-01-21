package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/archivalist"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLibrarianSearchFunctionality(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	searchSystem := &mockSearchSystemWithData{
		documents: []librarian.ScoredDocument{
			{ID: "doc1", Path: "pkg/service/handler.go", Type: "function", Content: "func HandleRequest", Score: 0.95},
			{ID: "doc2", Path: "pkg/service/types.go", Type: "struct", Content: "type Request struct", Score: 0.85},
			{ID: "doc3", Path: "pkg/utils/helpers.go", Type: "function", Content: "func FormatRequest", Score: 0.75},
		},
	}

	lib, err := librarian.New(librarian.Config{
		SearchSystem: searchSystem,
	})
	require.NoError(t, err)

	err = lib.Start(bus)
	require.NoError(t, err)
	defer lib.Stop()

	assert.True(t, lib.IsRunning())
	assert.NotNil(t, lib.Bus())
	assert.NotNil(t, lib.Channels())
	assert.Equal(t, "librarian.requests", lib.Channels().Requests)
	assert.Equal(t, "librarian.responses", lib.Channels().Responses)
}

type mockSearchSystemWithData struct {
	documents []librarian.ScoredDocument
	mu        sync.Mutex
}

func (m *mockSearchSystemWithData) Search(ctx context.Context, query string, opts librarian.SearchOptions) (*librarian.CodeSearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := opts.Limit
	if limit == 0 || limit > len(m.documents) {
		limit = len(m.documents)
	}

	return &librarian.CodeSearchResult{
		Documents: m.documents[:limit],
		TotalHits: len(m.documents),
		Took:      10 * time.Millisecond,
	}, nil
}

func TestLibrarianStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	lib, err := librarian.New(librarian.Config{
		SearchSystem: &mockSearchSystem{},
	})
	require.NoError(t, err)

	assert.False(t, lib.IsRunning())

	err = lib.Start(bus)
	require.NoError(t, err)
	assert.True(t, lib.IsRunning())

	err = lib.Start(bus)
	assert.Error(t, err, "should error when starting already running librarian")

	err = lib.Stop()
	require.NoError(t, err)
	assert.False(t, lib.IsRunning())

	err = lib.Stop()
	assert.NoError(t, err, "stopping already stopped should be idempotent")
}

func TestAcademicRegistration(t *testing.T) {
	acad, err := academic.New(academic.Config{})
	require.NoError(t, err)

	reg := acad.Registration()
	require.NotNil(t, reg)

	assert.Equal(t, "academic", reg.ID)
	assert.Equal(t, "academic", reg.Name)
	assert.Contains(t, reg.Aliases, "research")
	assert.Contains(t, reg.Aliases, "scholar")

	caps := reg.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentRecall)
	assert.Contains(t, caps.Intents, guide.IntentCheck)
	assert.Contains(t, caps.Domains, guide.DomainPatterns)
	assert.Contains(t, caps.Domains, guide.DomainDecisions)
	assert.Contains(t, caps.Domains, guide.DomainLearnings)
	assert.Contains(t, caps.Tags, "research")
	assert.Contains(t, caps.Tags, "best-practices")
	assert.Equal(t, 50, caps.Priority)
}

func TestAcademicStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	acad, err := academic.New(academic.Config{})
	require.NoError(t, err)

	assert.False(t, acad.IsRunning())

	err = acad.Start(bus)
	require.NoError(t, err)
	assert.True(t, acad.IsRunning())

	err = acad.Start(bus)
	assert.Error(t, err, "should error when starting already running academic")

	err = acad.Stop()
	require.NoError(t, err)
	assert.False(t, acad.IsRunning())
}

func TestArchivalistStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	arch, err := archivalist.New(archivalist.Config{})
	require.NoError(t, err)

	assert.False(t, arch.IsRunning())

	err = arch.Start(bus)
	require.NoError(t, err)
	assert.True(t, arch.IsRunning())

	err = arch.Start(bus)
	assert.Error(t, err, "should error when starting already running archivalist")

	err = arch.Stop()
	require.NoError(t, err)
	assert.False(t, arch.IsRunning())
}

func TestArchivalistRoutingInfo(t *testing.T) {
	arch, err := archivalist.New(archivalist.Config{})
	require.NoError(t, err)

	routingInfo := arch.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "archivalist", routingInfo.ID)
	assert.Equal(t, "archivalist", routingInfo.Name)
	assert.Contains(t, routingInfo.Aliases, "arch")

	require.NotNil(t, routingInfo.Registration)
	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentRecall)
	assert.Contains(t, caps.Intents, guide.IntentStore)
	assert.Contains(t, caps.Intents, guide.IntentCheck)
	assert.Contains(t, caps.Domains, guide.DomainPatterns)
	assert.Contains(t, caps.Domains, guide.DomainFailures)
}

func TestKnowledgeAgentsConcurrentStart(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	lib, err := librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
	require.NoError(t, err)

	acad, err := academic.New(academic.Config{})
	require.NoError(t, err)

	arch, err := archivalist.New(archivalist.Config{})
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 3)

	wg.Add(3)
	go func() {
		defer wg.Done()
		if err := lib.Start(bus); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := acad.Start(bus); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := arch.Start(bus); err != nil {
			errs <- err
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent start error: %v", err)
	}

	assert.True(t, lib.IsRunning())
	assert.True(t, acad.IsRunning())
	assert.True(t, arch.IsRunning())

	lib.Stop()
	acad.Stop()
	arch.Stop()
}

func TestLibrarianReceivesForwardedRequest(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	searchSystem := &mockSearchSystemWithData{
		documents: []librarian.ScoredDocument{
			{ID: "doc1", Path: "main.go", Type: "function", Content: "func main()", Score: 0.9},
		},
	}

	lib, err := librarian.New(librarian.Config{
		SearchSystem: searchSystem,
	})
	require.NoError(t, err)

	err = lib.Start(bus)
	require.NoError(t, err)
	defer lib.Stop()

	var responseReceived bool
	var responseMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)

	sub, err := bus.SubscribeAsync(lib.Channels().Responses, func(msg *guide.Message) error {
		responseMu.Lock()
		responseReceived = true
		responseMu.Unlock()
		wg.Done()
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	fwdRequest := &guide.ForwardedRequest{
		CorrelationID: "test-123",
		SourceAgentID: "guide",
		Intent:        guide.IntentSearch,
		Domain:        guide.DomainCode,
		Input:         "find main function",
	}

	msg := guide.NewForwardMessage("guide", fwdRequest)
	err = bus.Publish(lib.Channels().Requests, msg)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		responseMu.Lock()
		assert.True(t, responseReceived)
		responseMu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for librarian response")
	}
}

func TestKnowledgeAgentsEventBusSubscriptions(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	tests := []struct {
		name    string
		agentID string
		start   func() error
		stop    func() error
	}{
		{
			name:    "librarian",
			agentID: "librarian",
			start: func() error {
				lib, err := librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
				if err != nil {
					return err
				}
				return lib.Start(bus)
			},
		},
		{
			name:    "academic",
			agentID: "academic",
			start: func() error {
				acad, err := academic.New(academic.Config{})
				if err != nil {
					return err
				}
				return acad.Start(bus)
			},
		},
		{
			name:    "archivalist",
			agentID: "archivalist",
			start: func() error {
				arch, err := archivalist.New(archivalist.Config{})
				if err != nil {
					return err
				}
				return arch.Start(bus)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.start()
			require.NoError(t, err)

			requestTopic := guide.AgentTopic(tc.agentID, guide.ChannelTypeRequests)
			responseTopic := guide.AgentTopic(tc.agentID, guide.ChannelTypeResponses)

			reqCount := bus.TopicSubscriberCount(requestTopic)
			assert.Greater(t, reqCount, 0, "should have request subscription")

			respCount := bus.TopicSubscriberCount(responseTopic)
			assert.Greater(t, respCount, 0, "should have response subscription")
		})
	}
}

func TestLibrarianRoutingInfo(t *testing.T) {
	lib, err := librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
	require.NoError(t, err)

	routingInfo := lib.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "librarian", routingInfo.ID)
	assert.Equal(t, "librarian", routingInfo.Name)

	require.NotNil(t, routingInfo.Registration)
	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentFind)
	assert.Contains(t, caps.Intents, guide.IntentSearch)
	assert.Contains(t, caps.Intents, guide.IntentLocate)
	assert.Contains(t, caps.Domains, guide.DomainCode)
}

func TestLibrarianTriggers(t *testing.T) {
	lib, err := librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
	require.NoError(t, err)

	routingInfo := lib.GetRoutingInfo()
	triggers := routingInfo.Triggers

	expectedStrong := []string{"find", "search", "locate", "where is"}
	for _, trig := range expectedStrong {
		assert.Contains(t, triggers.StrongTriggers, trig)
	}

	findTrigs, ok := triggers.IntentTriggers[guide.IntentFind]
	assert.True(t, ok)
	assert.Contains(t, findTrigs, "find")
	assert.Contains(t, findTrigs, "where is")
}
