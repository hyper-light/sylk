package guide

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
)

type responseTestProvider struct{}

func (responseTestProvider) Name() string { return "response-test" }

func (responseTestProvider) SupportedModels() []providers.ModelInfo {
	return []providers.ModelInfo{{ID: "response-test-model", Name: "response-test-model", MaxContext: 8192}}
}

func (responseTestProvider) Complete(context.Context, *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	return &providers.CompletionResponse{}, nil
}

func (responseTestProvider) Stream(context.Context, *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk)
	close(ch)
	return ch, nil
}

func (responseTestProvider) CountTokens([]providers.Message) (int, error) { return 0, nil }

func (responseTestProvider) MaxContextTokens(string) int { return 8192 }

func (responseTestProvider) HealthCheck(context.Context) error { return nil }

func newResponseTestGuide(t *testing.T) (*Guide, chan *Message) {
	t.Helper()

	bus := NewChannelBus(DefaultChannelBusConfig())
	t.Cleanup(func() {
		_ = bus.Close()
	})

	out := make(chan *Message, 8)
	sub, err := bus.Subscribe(TopicResponses("tui", "tui"), func(msg *Message) error {
		out <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})

	return &Guide{
		bus:                  bus,
		pending:              NewPendingStore(DefaultPendingStoreConfig()),
		streams:              newGuideStreamManager(nil),
		agentChannels:        NewStringMap[*AgentChannels](DefaultShardCount),
		agentID:              "guide",
		sessionID:            "test-session",
		responseMessagesSeen: make(map[string]time.Time),
		completedPendings:    make(map[string]completedPendingRoute),
	}, out
}

func setPendingRoute(g *Guide, correlationID string) {
	setPendingRouteWithSourceTargetAndMetadata(g, correlationID, "tui", "academic", "", nil)
}

func setPendingRouteWithMetadata(g *Guide, correlationID string, metadata map[string]any) {
	setPendingRouteWithSourceTargetAndMetadata(g, correlationID, "tui", "academic", "", metadata)
}

func setPendingRouteWithTargetAndMetadata(g *Guide, correlationID, targetAgentID string, metadata map[string]any) {
	setPendingRouteWithSourceTargetAndMetadata(g, correlationID, "tui", targetAgentID, "", metadata)
}

func setPendingRouteWithSourceTargetAndMetadata(g *Guide, correlationID, sourceAgentID, targetAgentID, parentCorrelationID string, metadata map[string]any) {
	g.pending.Set(correlationID, &PendingRequest{
		CorrelationID:        correlationID,
		SourceAgentID:        sourceAgentID,
		TargetAgentID:        targetAgentID,
		StreamTargetOverride: metadataVisibleTargetValue(metadata),
		Request: &RouteRequest{
			CorrelationID:       correlationID,
			ParentCorrelationID: parentCorrelationID,
			SessionID:           "test-session",
			SourceAgentID:       sourceAgentID,
			Metadata:            metadata,
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(DefaultPendingStoreConfig().DefaultTimeout),
	})
}

func TestGuideSelfStreamMetadataDoesNotLeakTargetIdentity(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithMetadata(g, "corr-guide-stream", map[string]any{
		"agent_type":                 "architect",
		"agent_name":                 "Architect",
		"runtime_agent_id":           "architect",
		"chat_nested_branch":         true,
		"chat_parent_correlation_id": "corr-parent",
		"chat_parent_tool_call_key":  "consult-1",
		"chat_inter_agent_kind":      "consult",
	})

	g.publishGuideStreamComplete("corr-guide-stream", "tui", nil)

	select {
	case msg := <-out:
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatal("expected relayed guide stream response")
		}
		if got := stream.RespondingAgentID; got != "guide" {
			t.Fatalf("responding agent = %q, want guide", got)
		}
		if got := stream.Metadata["agent_type"]; got != "guide" {
			t.Fatalf("stream metadata agent_type = %#v, want guide", got)
		}
		if got := stream.Metadata["agent_name"]; got != "Guide" {
			t.Fatalf("stream metadata agent_name = %#v, want Guide", got)
		}
		if _, ok := stream.Metadata["runtime_agent_id"]; ok {
			t.Fatalf("guide self-stream should not leak runtime_agent_id: %#v", stream.Metadata["runtime_agent_id"])
		}
		if got := stream.Metadata["chat_parent_correlation_id"]; got != "corr-parent" {
			t.Fatalf("stream metadata chat_parent_correlation_id = %#v, want corr-parent", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed guide stream response")
	}
}

func TestGuideSelfResponseMetadataDoesNotLeakTargetIdentity(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithMetadata(g, "corr-guide-response", map[string]any{
		"agent_type":       "architect",
		"agent_name":       "Architect",
		"runtime_agent_id": "architect",
		"task_id":          "task-123",
	})
	pending := g.pending.Get("corr-guide-response")
	if pending == nil {
		t.Fatal("expected pending request")
	}

	err := g.publishResponseToSource("tui", &RouteResponse{
		CorrelationID:       "corr-guide-response",
		RespondingAgentID:   "guide",
		RespondingAgentName: "Guide",
		Success:             true,
		Data:                "ok",
	}, pending)
	if err != nil {
		t.Fatalf("publish response to source: %v", err)
	}

	select {
	case msg := <-out:
		resp, ok := msg.GetRouteResponse()
		if !ok || resp == nil {
			t.Fatal("expected relayed guide route response")
		}
		if got := msg.Metadata["agent_type"]; got != "guide" {
			t.Fatalf("response metadata agent_type = %#v, want guide", got)
		}
		if got := msg.Metadata["agent_name"]; got != "Guide" {
			t.Fatalf("response metadata agent_name = %#v, want Guide", got)
		}
		if _, ok := msg.Metadata["runtime_agent_id"]; ok {
			t.Fatalf("guide self-response should not leak runtime_agent_id: %#v", msg.Metadata["runtime_agent_id"])
		}
		if got := msg.Metadata["task_id"]; got != "task-123" {
			t.Fatalf("response metadata task_id = %#v, want task-123", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed guide route response")
	}
}

func TestHandleResponseMessage_RelaysAncestorVisibleMetadataToTUI(t *testing.T) {
	g, tuiOut := newResponseTestGuide(t)
	rootCID := g.pending.Add(&RouteRequest{
		CorrelationID: "corr-visible-root",
		Input:         "audit root",
		SourceAgentID: "orchestrator",
		TargetAgentID: "inspector",
		SessionID:     "test-session",
		Metadata: map[string]any{
			metadataVisibleTarget: "tui",
			"task_id":             "task-123",
			"task_name":           "Hello CLI",
			"task_slug":           "hello-cli",
		},
	}, nil, "inspector")
	childCID := g.pending.Add(&RouteRequest{
		CorrelationID:       "corr-visible-child",
		ParentCorrelationID: rootCID,
		Input:               "consult academic",
		SourceAgentID:       "inspector",
		TargetAgentID:       "academic",
		SessionID:           "test-session",
		Metadata: map[string]any{
			"chat_nested_branch":         true,
			"chat_parent_correlation_id": rootCID,
			"chat_parent_tool_call_key":  "consult-academic-1",
			"chat_inter_agent_kind":      "consult",
		},
	}, nil, "academic")

	if err := g.handleResponseMessage(&Message{
		ID:            "msg-visible-child-progress",
		CorrelationID: childCID,
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     childCID,
			RespondingAgentID: "academic",
			Metadata: map[string]any{
				"agent_type": "academic",
			},
			Event: &StreamEvent{
				Type: StreamEventProgress,
				Data: &ProgressData{Message: "Gathering stronger alternatives"},
			},
		},
	}); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case msg := <-tuiOut:
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatal("expected relayed visible child stream")
		}
		if got := stream.TargetAgentID; got != "tui" {
			t.Fatalf("target_agent_id = %q, want tui", got)
		}
		if got := stream.Metadata["task_id"]; got != "task-123" {
			t.Fatalf("stream metadata task_id = %#v, want task-123", got)
		}
		if got := stream.Metadata["task_name"]; got != "Hello CLI" {
			t.Fatalf("stream metadata task_name = %#v, want Hello CLI", got)
		}
		if got := stream.Metadata["task_slug"]; got != "hello-cli" {
			t.Fatalf("stream metadata task_slug = %#v, want hello-cli", got)
		}
		if got := stream.Metadata["chat_parent_correlation_id"]; got != rootCID {
			t.Fatalf("stream metadata chat_parent_correlation_id = %#v, want %s", got, rootCID)
		}
		if got := stream.Metadata["chat_parent_tool_call_key"]; got != "consult-academic-1" {
			t.Fatalf("stream metadata chat_parent_tool_call_key = %#v, want consult-academic-1", got)
		}
		if got := stream.Metadata["chat_inter_agent_kind"]; got != "consult" {
			t.Fatalf("stream metadata chat_inter_agent_kind = %#v, want consult", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed visible child stream")
	}
}

func TestHandleResponseMessage_NestedBranchOverridesAncestorTopLevelTransfer(t *testing.T) {
	g, tuiOut := newResponseTestGuide(t)
	rootCID := g.pending.Add(&RouteRequest{
		CorrelationID: "corr-top-level-root",
		Input:         "review root",
		SourceAgentID: "orchestrator",
		TargetAgentID: "inspector",
		SessionID:     "test-session",
		Metadata: map[string]any{
			metadataVisibleTarget:        "tui",
			"chat_top_level_transfer":    true,
			"chat_parent_correlation_id": "corr-orchestrator-parent",
		},
	}, nil, "inspector")
	childCID := g.pending.Add(&RouteRequest{
		CorrelationID:       "corr-top-level-child",
		ParentCorrelationID: rootCID,
		Input:               "consult archivalist",
		SourceAgentID:       "inspector",
		TargetAgentID:       "archivalist",
		SessionID:           "test-session",
		Metadata: map[string]any{
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  rootCID,
			"chat_parent_tool_call_key":   "consult-archivalist-1",
			"chat_inter_agent_kind":       "consult",
			"chat_inter_agent_thread_key": "thread-archivalist-1",
		},
	}, nil, "archivalist")

	if err := g.handleResponseMessage(&Message{
		ID:            "msg-top-level-child-stream",
		CorrelationID: childCID,
		Type:          MessageTypeStream,
		SourceAgentID: "archivalist",
		Payload: &StreamResponse{
			CorrelationID:     childCID,
			RespondingAgentID: "archivalist",
			Event: &StreamEvent{
				Type:      StreamEventStart,
				Timestamp: time.Now(),
			},
		},
	}); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-tuiOut:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if got := stream.Metadata["chat_parent_correlation_id"]; got != rootCID {
			t.Fatalf("parent correlation metadata = %#v, want %s", got, rootCID)
		}
		if got := stream.Metadata["chat_parent_tool_call_key"]; got != "consult-archivalist-1" {
			t.Fatalf("parent tool call metadata = %#v, want consult-archivalist-1", got)
		}
		if got := stream.Metadata["chat_inter_agent_kind"]; got != "consult" {
			t.Fatalf("inter-agent kind metadata = %#v, want consult", got)
		}
		if got := stream.Metadata["chat_inter_agent_thread_key"]; got != "thread-archivalist-1" {
			t.Fatalf("inter-agent thread metadata = %#v, want thread-archivalist-1", got)
		}
		if got, _ := stream.Metadata["chat_nested_branch"].(bool); !got {
			t.Fatalf("chat_nested_branch = %#v, want true", stream.Metadata["chat_nested_branch"])
		}
		if got, _ := stream.Metadata["chat_top_level_transfer"].(bool); got {
			t.Fatalf("chat_top_level_transfer = %#v, want absent/false", stream.Metadata["chat_top_level_transfer"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed child stream start")
	}
}

func TestRelayedNonGuideStreamMetadataUsesResponderIdentity(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithTargetAndMetadata(g, "corr-architect-stream", "architect", map[string]any{
		"agent_type":       "guide",
		"agent_name":       "Guide",
		"runtime_agent_id": "guide",
		"task_id":          "task-123",
	})

	err := g.publishStreamToSource("tui", &StreamResponse{
		CorrelationID:       "corr-architect-stream",
		RespondingAgentID:   "architect",
		RespondingAgentName: "Architect",
		Event: &StreamEvent{
			Type: StreamEventStart,
		},
	})
	if err != nil {
		t.Fatalf("publish stream to source: %v", err)
	}

	select {
	case msg := <-out:
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatal("expected relayed architect stream response")
		}
		if got := stream.Metadata["agent_type"]; got != "architect" {
			t.Fatalf("stream metadata agent_type = %#v, want architect", got)
		}
		if got := stream.Metadata["agent_name"]; got != "Architect" {
			t.Fatalf("stream metadata agent_name = %#v, want Architect", got)
		}
		if got := stream.Metadata["task_id"]; got != "task-123" {
			t.Fatalf("stream metadata task_id = %#v, want task-123", got)
		}
		if got := stream.Metadata["runtime_agent_id"]; got != nil {
			t.Fatalf("stream metadata runtime_agent_id = %#v, want nil for canonical architect responder", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed architect stream response")
	}
}

func TestRelayedNonGuideResponseMetadataUsesResponderIdentity(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithTargetAndMetadata(g, "corr-architect-response", "architect", map[string]any{
		"agent_type":       "guide",
		"agent_name":       "Guide",
		"runtime_agent_id": "guide",
		"task_id":          "task-123",
	})
	pending := g.pending.Get("corr-architect-response")
	if pending == nil {
		t.Fatal("expected pending request")
	}

	err := g.publishResponseToSource("tui", &RouteResponse{
		CorrelationID:       "corr-architect-response",
		RespondingAgentID:   "architect",
		RespondingAgentName: "Architect",
		Success:             true,
		Data:                "ok",
	}, pending)
	if err != nil {
		t.Fatalf("publish response to source: %v", err)
	}

	select {
	case msg := <-out:
		resp, ok := msg.GetRouteResponse()
		if !ok || resp == nil {
			t.Fatal("expected relayed architect route response")
		}
		if got := msg.Metadata["agent_type"]; got != "architect" {
			t.Fatalf("response metadata agent_type = %#v, want architect", got)
		}
		if got := msg.Metadata["agent_name"]; got != "Architect" {
			t.Fatalf("response metadata agent_name = %#v, want Architect", got)
		}
		if got := msg.Metadata["task_id"]; got != "task-123" {
			t.Fatalf("response metadata task_id = %#v, want task-123", got)
		}
		if got := msg.Metadata["runtime_agent_id"]; got != nil {
			t.Fatalf("response metadata runtime_agent_id = %#v, want nil for canonical architect responder", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed architect route response")
	}
}

func setPendingRouteWithOverride(g *Guide, correlationID, sourceAgentID, targetAgentID, streamTarget string, metadata map[string]any) {
	g.pending.Set(correlationID, &PendingRequest{
		CorrelationID:        correlationID,
		SourceAgentID:        sourceAgentID,
		TargetAgentID:        targetAgentID,
		StreamTargetOverride: streamTarget,
		Request: &RouteRequest{
			CorrelationID: correlationID,
			SessionID:     "test-session",
			SourceAgentID: sourceAgentID,
			Metadata:      metadata,
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(DefaultPendingStoreConfig().DefaultTimeout),
	})
}

func TestHandleResponseMessage_DeduplicatesByBusMessageID(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRoute(g, "corr-start")

	msg := &Message{
		ID:            "msg-stream-start",
		CorrelationID: "corr-start",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-start",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventStart,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("first handleResponseMessage: %v", err)
	}
	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("second handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-out:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if stream.Event.Type != StreamEventStart {
			t.Fatalf("forwarded stream type = %q, want start", stream.Event.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded stream start")
	}

	select {
	case extra := <-out:
		t.Fatalf("unexpected duplicate forwarded message: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleResponseMessage_ForwardsLateStreamCompleteAfterRouteResponse(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRoute(g, "corr-late-complete")

	respMsg := &Message{
		ID:            "msg-route-response",
		CorrelationID: "corr-late-complete",
		Type:          MessageTypeResponse,
		SourceAgentID: "academic",
		Payload: &RouteResponse{
			CorrelationID:       "corr-late-complete",
			Success:             true,
			RespondingAgentID:   "academic",
			RespondingAgentName: "Academic",
			Data:                "final answer",
		},
	}
	if err := g.handleResponseMessage(respMsg); err != nil {
		t.Fatalf("handle route response: %v", err)
	}

	completeMsg := &Message{
		ID:            "msg-stream-complete",
		CorrelationID: "corr-late-complete",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-late-complete",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventComplete,
				Text:      "final answer",
				Timestamp: time.Now(),
			},
		},
	}
	if err := g.handleResponseMessage(completeMsg); err != nil {
		t.Fatalf("handle late stream complete: %v", err)
	}

	var sawResponse, sawComplete bool
	deadline := time.After(2 * time.Second)
	for !(sawResponse && sawComplete) {
		select {
		case forwarded := <-out:
			if resp, ok := forwarded.GetRouteResponse(); ok && resp != nil {
				sawResponse = true
			}
			if stream, ok := forwarded.GetStreamResponse(); ok && stream != nil && stream.Event != nil && stream.Event.Type == StreamEventComplete {
				sawComplete = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for forwarded late complete; sawResponse=%v sawComplete=%v", sawResponse, sawComplete)
		}
	}
}

func TestHandleResponseMessage_InitializesTrackingMapsWhenNil(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRoute(g, "corr-init-nil")
	g.responseMessagesSeen = nil
	g.completedPendings = nil

	respMsg := &Message{
		ID:            "msg-init-nil-response",
		CorrelationID: "corr-init-nil",
		Type:          MessageTypeResponse,
		SourceAgentID: "academic",
		Payload: &RouteResponse{
			CorrelationID:       "corr-init-nil",
			Success:             true,
			RespondingAgentID:   "academic",
			RespondingAgentName: "Academic",
			Data:                "ready",
		},
	}

	if err := g.handleResponseMessage(respMsg); err != nil {
		t.Fatalf("handle response with nil tracking maps: %v", err)
	}
	if g.responseMessagesSeen == nil {
		t.Fatal("responseMessagesSeen should be initialized on demand")
	}
	if g.completedPendings == nil {
		t.Fatal("completedPendings should be initialized on demand")
	}

	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded route response")
	}

	completeMsg := &Message{
		ID:            "msg-init-nil-complete",
		CorrelationID: "corr-init-nil",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-init-nil",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventComplete,
				Text:      "ready",
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(completeMsg); err != nil {
		t.Fatalf("handle late complete with nil-initialized tracking maps: %v", err)
	}
}

func TestHandleResponseMessage_IgnoresGuideRelayedRouteResponses(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithMetadata(g, "corr-relayed-response", map[string]any{
		"pipeline_task": true,
		"task_id":       "task-1",
		"dag_id":        "dag-1",
		"node_id":       "node-1",
		"agent_type":    "inspector-pipeline",
	})

	respMsg := &Message{
		ID:            "msg-original-route-response",
		CorrelationID: "corr-relayed-response",
		Type:          MessageTypeResponse,
		SourceAgentID: "guardian",
		Payload: &RouteResponse{
			CorrelationID:       "corr-relayed-response",
			Success:             true,
			RespondingAgentID:   "guardian",
			RespondingAgentName: "Guardian",
			Data:                "approved",
		},
	}

	if err := g.handleResponseMessage(respMsg); err != nil {
		t.Fatalf("handle original route response: %v", err)
	}

	var forwarded *Message
	select {
	case forwarded = <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded relayed route response")
	}
	if forwarded == nil {
		t.Fatal("forwarded relayed route response is nil")
	}
	if got := forwarded.TargetAgentID; got != "tui" {
		t.Fatalf("forwarded target_agent_id = %q, want tui", got)
	}
	if forwarded.Metadata == nil {
		t.Fatal("forwarded relayed route response should carry metadata")
	}
	if _, ok := forwarded.Metadata["_guide_relayed"]; !ok {
		t.Fatal("forwarded relayed route response should be marked as _guide_relayed")
	}
	if got := forwarded.Metadata["task_id"]; got != "task-1" {
		t.Fatalf("forwarded task_id metadata = %#v, want task-1", got)
	}
	if got := forwarded.Metadata["agent_type"]; got != "guardian" {
		t.Fatalf("forwarded agent_type metadata = %#v, want guardian", got)
	}

	if err := g.handleResponseMessage(forwarded); err != nil {
		t.Fatalf("handle relayed route response: %v", err)
	}

	select {
	case extra := <-out:
		t.Fatalf("unexpected duplicate forwarded route response: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleResponseMessage_RestoresPendingMetadataOnRelayedStream(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithMetadata(g, "corr-relayed-stream", map[string]any{
		"chat_nested_branch":          true,
		"chat_parent_correlation_id":  "corr-parent",
		"chat_parent_tool_call_key":   "consult-1",
		"chat_inter_agent_kind":       "consult",
		"chat_inter_agent_thread_key": "thread-1",
		"agent_type":                  "librarian",
		"task_id":                     "task-1",
	})

	msg := &Message{
		ID:            "msg-relayed-stream-start",
		CorrelationID: "corr-relayed-stream",
		Type:          MessageTypeStream,
		SourceAgentID: "librarian",
		Payload: &StreamResponse{
			CorrelationID:     "corr-relayed-stream",
			RespondingAgentID: "librarian",
			Event: &StreamEvent{
				Type:      StreamEventStart,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-out:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if got := stream.Metadata["chat_parent_correlation_id"]; got != "corr-parent" {
			t.Fatalf("parent correlation metadata = %#v, want corr-parent", got)
		}
		if got := stream.Metadata["chat_parent_tool_call_key"]; got != "consult-1" {
			t.Fatalf("parent tool call metadata = %#v, want consult-1", got)
		}
		if got := stream.Metadata["chat_inter_agent_kind"]; got != "consult" {
			t.Fatalf("inter-agent kind metadata = %#v, want consult", got)
		}
		if got := stream.Metadata["agent_type"]; got != "librarian" {
			t.Fatalf("agent_type metadata = %#v, want librarian", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed stream start")
	}
}

func TestHandleResponseMessage_SparseStreamMetadataDoesNotClobberPendingIdentity(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithMetadata(g, "corr-relayed-stream-sparse", map[string]any{
		"chat_nested_branch":          true,
		"chat_parent_correlation_id":  "corr-parent",
		"chat_parent_tool_call_key":   "challenge-1",
		"chat_inter_agent_kind":       "challenge",
		"chat_inter_agent_thread_key": "thread-1",
		"agent_type":                  "tester-pipeline",
		"task_id":                     "task-3",
		"task_name":                   "Create hello.py CLI entrypoint",
		"task_slug":                   "create-cli-entrypoint",
	})

	msg := &Message{
		ID:            "msg-relayed-stream-progress-sparse",
		CorrelationID: "corr-relayed-stream-sparse",
		Type:          MessageTypeStream,
		SourceAgentID: "runtime-tester",
		Payload: &StreamResponse{
			CorrelationID:     "corr-relayed-stream-sparse",
			RespondingAgentID: "runtime-tester",
			Metadata: map[string]any{
				"task_id":   "",
				"task_name": "",
				"task_slug": "",
			},
			Event: &StreamEvent{
				Type:      StreamEventProgress,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-out:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if got := stream.Metadata["task_id"]; got != "task-3" {
			t.Fatalf("task_id metadata = %#v, want task-3", got)
		}
		if got := stream.Metadata["task_name"]; got != "Create hello.py CLI entrypoint" {
			t.Fatalf("task_name metadata = %#v, want preserved task name", got)
		}
		if got := stream.Metadata["task_slug"]; got != "create-cli-entrypoint" {
			t.Fatalf("task_slug metadata = %#v, want preserved task slug", got)
		}
		if got := stream.Metadata["chat_parent_correlation_id"]; got != "corr-parent" {
			t.Fatalf("parent correlation metadata = %#v, want corr-parent", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed sparse stream progress")
	}
}

func TestPublishRouteHandoffProgress_AttachesPendingMetadata(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRouteWithMetadata(g, "corr-handoff", map[string]any{
		"chat_nested_branch":         true,
		"chat_parent_correlation_id": "corr-parent",
		"chat_parent_tool_call_key":  "consult-1",
		"chat_inter_agent_kind":      "consult",
	})

	g.publishRouteHandoffProgress("corr-handoff", "tui", "librarian", events.VisibilityAgent)

	select {
	case forwarded := <-out:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if stream.Event.Type != StreamEventProgress {
			t.Fatalf("forwarded event type = %q, want progress", stream.Event.Type)
		}
		if stream.RespondingAgentID != "librarian" {
			t.Fatalf("responding agent = %q, want librarian", stream.RespondingAgentID)
		}
		if got := stream.Metadata["chat_parent_correlation_id"]; got != "corr-parent" {
			t.Fatalf("parent correlation metadata = %#v, want corr-parent", got)
		}
		if got := stream.Metadata["chat_parent_tool_call_key"]; got != "consult-1" {
			t.Fatalf("parent tool call metadata = %#v, want consult-1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed handoff progress")
	}
}

func TestObserveStreamReroute_InitializesDeferredMapWhenNil(t *testing.T) {
	g, _ := newResponseTestGuide(t)
	setPendingRoute(g, "corr-reroute-old")
	g.streamReroutes = nil

	g.observeStreamReroute(&StreamResponse{
		CorrelationID: "corr-reroute-old",
		Event: &StreamEvent{
			Type: StreamEventReroute,
			Data: map[string]string{
				"new_correlation_id": "corr-reroute-new",
			},
		},
	})

	if g.streamReroutes == nil {
		t.Fatal("streamReroutes should be initialized on demand")
	}
	if got := g.streamReroutes["corr-reroute-new"]; got != "tui" {
		t.Fatalf("streamReroutes[new] = %q, want tui", got)
	}

	pending := &PendingRequest{CorrelationID: "corr-reroute-new"}
	g.applyDeferredStreamReroute(pending)
	if pending.StreamTargetOverride != "tui" {
		t.Fatalf("pending.StreamTargetOverride = %q, want tui", pending.StreamTargetOverride)
	}
	if _, ok := g.streamReroutes["corr-reroute-new"]; ok {
		t.Fatal("deferred stream reroute should be consumed after apply")
	}
}

func TestHandleResponseMessage_MirrorsRelayedStreamActivityToRequesterWhenReroutedToTUI(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	t.Cleanup(func() {
		_ = bus.Close()
	})

	tuiOut := make(chan *Message, 1)
	tuiSub, err := bus.Subscribe(TopicResponses("tui", "tui"), func(msg *Message) error {
		tuiOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	t.Cleanup(func() {
		_ = tuiSub.Unsubscribe()
	})

	academicOut := make(chan *Message, 1)
	academicSub, err := bus.Subscribe(TopicResponses("academic", "academic"), func(msg *Message) error {
		academicOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe academic responses: %v", err)
	}
	t.Cleanup(func() {
		_ = academicSub.Unsubscribe()
	})

	g := &Guide{
		bus:                  bus,
		pending:              NewPendingStore(DefaultPendingStoreConfig()),
		streams:              newGuideStreamManager(nil),
		agentChannels:        NewStringMap[*AgentChannels](DefaultShardCount),
		sessionID:            "test-session",
		responseMessagesSeen: make(map[string]time.Time),
		completedPendings:    make(map[string]completedPendingRoute),
	}
	setPendingRouteWithOverride(g, "corr-mirror", "academic", "librarian", "tui", map[string]any{
		"chat_nested_branch":         true,
		"chat_parent_correlation_id": "corr-parent",
		"chat_parent_tool_call_key":  "consult-1",
		"chat_inter_agent_kind":      "consult",
	})

	msg := &Message{
		ID:            "msg-mirror-progress",
		CorrelationID: "corr-mirror",
		Type:          MessageTypeStream,
		SourceAgentID: "librarian",
		Payload: &StreamResponse{
			CorrelationID:     "corr-mirror",
			RespondingAgentID: "librarian",
			Event: &StreamEvent{
				Type: StreamEventProgress,
				Data: &ProgressData{Message: "Searching codebase"},
			},
		},
	}
	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-tuiOut:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected tui message: %+v", forwarded)
		}
		if stream.TargetAgentID != "tui" {
			t.Fatalf("tui target agent = %q, want tui", stream.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tui relayed stream")
	}

	select {
	case mirrored := <-academicOut:
		stream, ok := mirrored.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected academic message: %+v", mirrored)
		}
		if stream.TargetAgentID != "academic" {
			t.Fatalf("academic target agent = %q, want academic", stream.TargetAgentID)
		}
		if stream.CorrelationID != "corr-mirror" {
			t.Fatalf("academic correlation_id = %q, want corr-mirror", stream.CorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for mirrored academic stream")
	}
}

func TestHandleResponseMessage_MirrorsNestedStreamToAncestorVisibleTarget(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	t.Cleanup(func() {
		_ = bus.Close()
	})

	tuiOut := make(chan *Message, 1)
	tuiSub, err := bus.Subscribe(TopicResponses("tui", "tui"), func(msg *Message) error {
		tuiOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	t.Cleanup(func() {
		_ = tuiSub.Unsubscribe()
	})

	orchestratorOut := make(chan *Message, 1)
	orchestratorSub, err := bus.Subscribe(TopicResponses("orchestrator", "orchestrator"), func(msg *Message) error {
		orchestratorOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe orchestrator responses: %v", err)
	}
	t.Cleanup(func() {
		_ = orchestratorSub.Unsubscribe()
	})

	inspectorOut := make(chan *Message, 1)
	inspectorSub, err := bus.Subscribe(TopicResponses("inspector", "inspector"), func(msg *Message) error {
		inspectorOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe inspector responses: %v", err)
	}
	t.Cleanup(func() {
		_ = inspectorSub.Unsubscribe()
	})

	g := &Guide{
		bus:                  bus,
		pending:              NewPendingStore(DefaultPendingStoreConfig()),
		streams:              newGuideStreamManager(nil),
		agentChannels:        NewStringMap[*AgentChannels](DefaultShardCount),
		sessionID:            "test-session",
		responseMessagesSeen: make(map[string]time.Time),
		completedPendings:    make(map[string]completedPendingRoute),
	}
	g.pending.Set("corr-root", &PendingRequest{
		CorrelationID: "corr-root",
		SourceAgentID: "tui",
		TargetAgentID: "orchestrator",
		Request: &RouteRequest{
			CorrelationID: "corr-root",
			SessionID:     "test-session",
			SourceAgentID: "tui",
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(DefaultPendingStoreConfig().DefaultTimeout),
	})
	g.pending.Set("corr-child", &PendingRequest{
		CorrelationID:        "corr-child",
		SourceAgentID:        "inspector",
		TargetAgentID:        "academic",
		StreamTargetOverride: "orchestrator",
		Request: &RouteRequest{
			CorrelationID:       "corr-child",
			SessionID:           "test-session",
			SourceAgentID:       "inspector",
			ParentCorrelationID: "corr-root",
			Metadata: map[string]any{
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": "corr-root",
				"chat_parent_tool_call_key":  "challenge-1",
				"chat_inter_agent_kind":      "challenge",
			},
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(DefaultPendingStoreConfig().DefaultTimeout),
	})

	msg := &Message{
		ID:            "msg-mirror-ancestor-progress",
		CorrelationID: "corr-child",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-child",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type: StreamEventProgress,
				Data: &ProgressData{Message: "Comparing the strongest alternatives"},
			},
		},
	}
	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-tuiOut:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected tui message: %+v", forwarded)
		}
		if stream.TargetAgentID != "tui" {
			t.Fatalf("tui target agent = %q, want tui", stream.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ancestor-visible tui stream")
	}

	select {
	case forwarded := <-orchestratorOut:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected orchestrator message: %+v", forwarded)
		}
		if stream.TargetAgentID != "orchestrator" {
			t.Fatalf("orchestrator target agent = %q, want orchestrator", stream.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for primary nested stream target")
	}

	select {
	case mirrored := <-inspectorOut:
		stream, ok := mirrored.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected inspector message: %+v", mirrored)
		}
		if stream.TargetAgentID != "inspector" {
			t.Fatalf("inspector target agent = %q, want inspector", stream.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for requester mirror")
	}
}

func TestHandleResponseMessage_MirrorsEngineerNestedConsultStreamToTUIImmediately(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	t.Cleanup(func() {
		_ = bus.Close()
	})

	tuiOut := make(chan *Message, 1)
	tuiSub, err := bus.Subscribe(TopicResponses("tui", "tui"), func(msg *Message) error {
		tuiOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	t.Cleanup(func() {
		_ = tuiSub.Unsubscribe()
	})

	engineerOut := make(chan *Message, 1)
	engineerSub, err := bus.Subscribe(TopicResponses("engineer-runtime", "engineer-runtime"), func(msg *Message) error {
		engineerOut <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe engineer responses: %v", err)
	}
	t.Cleanup(func() {
		_ = engineerSub.Unsubscribe()
	})

	g := &Guide{
		bus:                  bus,
		pending:              NewPendingStore(DefaultPendingStoreConfig()),
		streams:              newGuideStreamManager(nil),
		agentChannels:        NewStringMap[*AgentChannels](DefaultShardCount),
		sessionID:            "test-session",
		responseMessagesSeen: make(map[string]time.Time),
		completedPendings:    make(map[string]completedPendingRoute),
	}
	g.pending.Set("corr-root", &PendingRequest{
		CorrelationID: "corr-root",
		SourceAgentID: "tui",
		TargetAgentID: "orchestrator",
		Request: &RouteRequest{
			CorrelationID: "corr-root",
			SessionID:     "test-session",
			SourceAgentID: "tui",
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(DefaultPendingStoreConfig().DefaultTimeout),
	})

	engineerParentID := g.pending.Add(&RouteRequest{
		CorrelationID:       "corr-engineer",
		SessionID:           "test-session",
		SourceAgentID:       "orchestrator",
		ParentCorrelationID: "corr-root",
	}, nil, "engineer")
	if engineerParentID != "corr-engineer" {
		t.Fatalf("engineer parent correlation = %q, want corr-engineer", engineerParentID)
	}

	childID := g.pending.Add(&RouteRequest{
		CorrelationID:       "corr-librarian",
		SessionID:           "test-session",
		SourceAgentID:       "engineer-runtime",
		ParentCorrelationID: engineerParentID,
		Metadata: map[string]any{
			"chat_nested_branch":         true,
			"chat_parent_correlation_id": "corr-engineer",
			"chat_parent_tool_call_key":  "consult-1",
			"chat_inter_agent_kind":      "consult",
		},
	}, nil, "librarian")
	if childID != "corr-librarian" {
		t.Fatalf("child correlation = %q, want corr-librarian", childID)
	}

	msg := &Message{
		ID:            "msg-engineer-child-progress",
		CorrelationID: "corr-librarian",
		Type:          MessageTypeStream,
		SourceAgentID: "librarian",
		Payload: &StreamResponse{
			CorrelationID:     "corr-librarian",
			RespondingAgentID: "librarian",
			Event: &StreamEvent{
				Type: StreamEventProgress,
				Data: &ProgressData{Message: "Searching codebase"},
			},
		},
	}
	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-tuiOut:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected tui message: %+v", forwarded)
		}
		if stream.TargetAgentID != "tui" {
			t.Fatalf("tui target agent = %q, want tui", stream.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for immediate tui relayed stream")
	}

	select {
	case mirrored := <-engineerOut:
		stream, ok := mirrored.GetStreamResponse()
		if !ok || stream == nil {
			t.Fatalf("unexpected engineer message: %+v", mirrored)
		}
		if stream.TargetAgentID != "engineer-runtime" {
			t.Fatalf("engineer target agent = %q, want engineer-runtime", stream.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for mirrored engineer stream")
	}

}

func TestNewWithProvider_InitializesResponseTrackingForStreamRelay(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	t.Cleanup(func() {
		_ = bus.Close()
	})

	out := make(chan *Message, 8)
	sub, err := bus.Subscribe(TopicResponses("tui", "tui"), func(msg *Message) error {
		out <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})

	g, err := NewWithProvider(responseTestProvider{}, "response-test-model", Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("NewWithProvider: %v", err)
	}
	if g.responseMessagesSeen == nil {
		t.Fatal("responseMessagesSeen should be initialized")
	}
	if g.completedPendings == nil {
		t.Fatal("completedPendings should be initialized")
	}

	setPendingRoute(g, "corr-provider-stream")

	msg := &Message{
		ID:            "msg-provider-stream-start",
		CorrelationID: "corr-provider-stream",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-provider-stream",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventStart,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-out:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if stream.Event.Type != StreamEventStart {
			t.Fatalf("forwarded stream type = %q, want start", stream.Event.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded provider-backed stream")
	}
}

func TestHandleResponseMessage_ForwardsStreamLifecycleWithoutPendingWhenTargetKnown(t *testing.T) {
	g, out := newResponseTestGuide(t)

	cases := []struct {
		name      string
		eventType StreamEventType
		eventData any
	}{
		{name: "start", eventType: StreamEventStart},
		{
			name:      "reroute",
			eventType: StreamEventReroute,
			eventData: map[string]string{
				"from_agent":              "inspector",
				"to_agent":                "tester",
				"original_correlation_id": "corr-parent",
				"new_correlation_id":      "corr-child",
			},
		},
		{name: "complete", eventType: StreamEventComplete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &Message{
				ID:            "msg-" + tc.name,
				CorrelationID: "corr-missing-pending-" + tc.name,
				Type:          MessageTypeStream,
				SourceAgentID: "tester-pipeline",
				TargetAgentID: "tui",
				Payload: &StreamResponse{
					CorrelationID:     "corr-missing-pending-" + tc.name,
					RespondingAgentID: "tester-pipeline",
					TargetAgentID:     "tui",
					Event: &StreamEvent{
						Type: tc.eventType,
						Data: tc.eventData,
					},
				},
			}

			if err := g.handleResponseMessage(msg); err != nil {
				t.Fatalf("handleResponseMessage: %v", err)
			}

			select {
			case forwarded := <-out:
				stream, ok := forwarded.GetStreamResponse()
				if !ok || stream == nil || stream.Event == nil {
					t.Fatalf("unexpected forwarded message: %+v", forwarded)
				}
				if stream.TargetAgentID != "tui" {
					t.Fatalf("stream.TargetAgentID = %q, want tui", stream.TargetAgentID)
				}
				if stream.Event.Type != tc.eventType {
					t.Fatalf("stream.Event.Type = %q, want %q", stream.Event.Type, tc.eventType)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for forwarded %s stream", tc.name)
			}
		})
	}
}
