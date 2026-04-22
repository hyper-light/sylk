package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

func TestStreamUsageAccumulatorTotalsAllUsageFields(t *testing.T) {
	ctx, acc := WithUsageAccumulator(context.Background())
	AccumulateUsage(ctx, &providers.Usage{
		InputTokens:      11,
		OutputTokens:     7,
		ReasoningTokens:  3,
		CacheReadTokens:  5,
		CacheWriteTokens: 2,
	})
	AccumulateUsage(ctx, &providers.Usage{
		InputTokens:      13,
		OutputTokens:     17,
		ReasoningTokens:  19,
		CacheReadTokens:  23,
		CacheWriteTokens: 29,
	})

	total := acc.Total()
	if total == nil {
		t.Fatal("expected total usage")
	}
	if total.InputTokens != 24 || total.OutputTokens != 24 {
		t.Fatalf("unexpected input/output totals: %+v", total)
	}
	if total.ReasoningTokens != 22 || total.CacheReadTokens != 28 || total.CacheWriteTokens != 31 {
		t.Fatalf("unexpected extended totals: %+v", total)
	}
}

// TestPublishStreamChunkDeliversTextToBus pins the agent-side
// emission contract: when a tool loop calls PublishStreamChunk, the
// bus delivers a StreamResponse carrying the chunk text, the
// caller's correlation + target identity, and a StreamEventData
// event. This is the invariant the bridge and chat model downstream
// rely on for streamed text to reach the terminal.
func TestPublishStreamChunkDeliversTextToBus(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("librarian", "librarian-1")
	streamCh := make(chan *guide.StreamResponse, 4)
	sub, err := bus.SubscribeAsync(channels.Responses, func(m *guide.Message) error {
		stream, ok := m.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithStreamContext(context.Background(), "corr-chunk", "tui")
	// Start is required by publishWithStreamLifecycle's gate — data
	// chunks emitted before Start are suppressed as a correctness
	// feature, not a regression. Match the real tool-loop ordering.
	if err := PublishStreamStart(bus, channels, ctx, "librarian-1"); err != nil {
		t.Fatalf("PublishStreamStart: %v", err)
	}
	if err := PublishStreamChunk(bus, channels, ctx, "librarian-1", "searching the index"); err != nil {
		t.Fatalf("PublishStreamChunk: %v", err)
	}

	var data *guide.StreamResponse
	deadline := time.After(2 * time.Second)
	for data == nil {
		select {
		case stream := <-streamCh:
			if stream.Event != nil && stream.Event.Type == guide.StreamEventData {
				data = stream
			}
		case <-deadline:
			t.Fatal("timed out waiting for StreamEventData delivery")
		}
	}
	if data.Event.Text != "searching the index" {
		t.Fatalf("chunk text = %q, want %q", data.Event.Text, "searching the index")
	}
	if data.CorrelationID != "corr-chunk" {
		t.Fatalf("correlation = %q, want corr-chunk", data.CorrelationID)
	}
	if data.RespondingAgentID != "librarian-1" {
		t.Fatalf("responding agent = %q, want librarian-1", data.RespondingAgentID)
	}
	if data.TargetAgentID != "tui" {
		t.Fatalf("target agent = %q, want tui", data.TargetAgentID)
	}
}

// TestPublishStreamChunkPropagatesReplicaIdentity pins the
// multi-replica contract: a replica whose context metadata carries
// runtime_agent_id (via AttachReplicaHandoffBridge) emits chunks
// whose StreamResponse.Metadata includes that runtime ID, so the
// bridge can stamp RuntimeAgentID on the downstream message and the
// chat model can render distinct per-replica rows.
func TestPublishStreamChunkPropagatesReplicaIdentity(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("librarian", "librarian-parent")
	streamCh := make(chan *guide.StreamResponse, 4)
	sub, err := bus.SubscribeAsync(channels.Responses, func(m *guide.Message) error {
		stream, ok := m.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithStreamContext(context.Background(), "corr-replica", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		"runtime_agent_id":   "librarian#replica-corr-replica",
		"handoff_replica_id": "librarian#replica-corr-replica",
		"agent_type":         "librarian",
		"agent_name":         "Librarian",
	})
	if err := PublishStreamStart(bus, channels, ctx, "librarian-parent"); err != nil {
		t.Fatalf("PublishStreamStart: %v", err)
	}
	if err := PublishStreamChunk(bus, channels, ctx, "librarian-parent", "replica text"); err != nil {
		t.Fatalf("PublishStreamChunk: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var data *guide.StreamResponse
	for data == nil {
		select {
		case stream := <-streamCh:
			if stream.Event != nil && stream.Event.Type == guide.StreamEventData {
				data = stream
			}
		case <-deadline:
			t.Fatal("timed out waiting for replica chunk")
		}
	}
	runtime, _ := data.Metadata["runtime_agent_id"].(string)
	if runtime != "librarian#replica-corr-replica" {
		t.Fatalf("metadata.runtime_agent_id = %q, want librarian#replica-corr-replica", runtime)
	}
	if data.Event.Text != "replica text" {
		t.Fatalf("chunk text = %q", data.Event.Text)
	}
}

func TestPublishStreamLifecycleUsesContextMetadata(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("tester", "agent-1")
	streamCh := make(chan *guide.StreamResponse, 4)
	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe stream: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithStreamContext(context.Background(), "corr-1", "tui")
	ctx, acc := WithUsageAccumulator(ctx)
	AccumulateUsage(ctx, &providers.Usage{InputTokens: 5, OutputTokens: 8})

	PublishStreamStart(bus, channels, ctx, "agent-1")
	PublishStreamComplete(bus, channels, ctx, "agent-1", "done", acc.Total())

	var start, complete *guide.StreamResponse
	deadline := time.After(2 * time.Second)
	for start == nil || complete == nil {
		select {
		case stream := <-streamCh:
			switch stream.Event.Type {
			case guide.StreamEventStart:
				start = stream
			case guide.StreamEventComplete:
				complete = stream
			}
		case <-deadline:
			t.Fatal("timed out waiting for stream lifecycle events")
		}
	}

	if start.CorrelationID != "corr-1" || start.RespondingAgentID != "agent-1" || start.TargetAgentID != "tui" {
		t.Fatalf("unexpected start stream: %+v", start)
	}
	if complete.CorrelationID != "corr-1" || complete.RespondingAgentID != "agent-1" || complete.TargetAgentID != "tui" {
		t.Fatalf("unexpected complete stream: %+v", complete)
	}
	if complete.Event.Text != "done" {
		t.Fatalf("complete text = %q, want done", complete.Event.Text)
	}
	if complete.Event.Usage == nil || complete.Event.Usage.InputTokens != 5 || complete.Event.Usage.OutputTokens != 8 {
		t.Fatalf("unexpected complete usage: %+v", complete.Event.Usage)
	}
}

func TestPublishIntermediateToolTurnPublishesToolTurnProgress(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("tester", "agent-1")
	streamCh := make(chan *guide.StreamResponse, 4)
	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe stream: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithStreamContext(context.Background(), "corr-2", "tui")
	PublishIntermediateToolTurn(bus, channels, ctx, "agent-1", &providers.Response{
		Content: "Inspecting the repository before I answer.",
		ToolCalls: []providers.ToolCall{{
			ID:        "tool-1",
			Name:      "search_skills",
			Arguments: `{"query":"repo"}`,
		}},
	})

	select {
	case stream := <-streamCh:
		if stream.Event == nil || stream.Event.Type != guide.StreamEventProgress {
			t.Fatalf("unexpected stream event: %+v", stream.Event)
		}
		progress, ok := stream.Event.Data.(*guide.ProgressData)
		if !ok || progress == nil {
			t.Fatalf("expected progress payload, got %+v", stream.Event.Data)
		}
		if got := progress.Message; got != "Inspecting the repository before I answer." {
			t.Fatalf("progress message = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for intermediate tool turn progress")
	}
}

func TestWithForwardedStreamContext_MarksTopLevelTransferWhenParentHasNoNestedBranch(t *testing.T) {
	ctx := WithForwardedStreamContext(context.Background(), "corr-child", "tui", "corr-parent", nil)

	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		t.Fatal("expected stream context")
	}
	if got, _ := stream.Metadata[streamMetadataParentCorrelation].(string); got != "corr-parent" {
		t.Fatalf("parent correlation = %q, want corr-parent", got)
	}
	if got, _ := stream.Metadata[streamMetadataTopLevelTransfer].(bool); !got {
		t.Fatalf("top-level transfer marker = %#v, want true", stream.Metadata[streamMetadataTopLevelTransfer])
	}
}

func TestWithForwardedStreamContext_DoesNotMarkTopLevelTransferForNestedBranch(t *testing.T) {
	ctx := WithForwardedStreamContext(context.Background(), "corr-child", "tui", "corr-parent", map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-parent",
		streamMetadataParentToolCallKey: "challenge-1",
		streamMetadataInterAgentKind:    "challenge",
	})

	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		t.Fatal("expected stream context")
	}
	if _, ok := stream.Metadata[streamMetadataTopLevelTransfer]; ok {
		t.Fatalf("top-level transfer marker = %#v, want absent for nested branch", stream.Metadata[streamMetadataTopLevelTransfer])
	}
}

func TestIntermediateToolTurnText_FallsBackToThinking(t *testing.T) {
	got := IntermediateToolTurnTextWithContext(context.Background(), &providers.Response{
		Thinking: "Checking packaging guidance and reconciling it with current Python recommendations.",
		ToolCalls: []providers.ToolCall{{
			ID:        "tool-1",
			Name:      "research_topic",
			Arguments: `{"topic":"python packaging"}`,
		}},
	})

	want := "Checking packaging guidance and reconciling it with current Python recommendations.\n\n"
	if got != want {
		t.Fatalf("IntermediateToolTurnText thinking fallback = %q, want %q", got, want)
	}
}

func TestIntermediateToolTurnText_FallsBackToToolSummary(t *testing.T) {
	got := IntermediateToolTurnTextWithContext(context.Background(), &providers.Response{
		ToolCalls: []providers.ToolCall{
			{ID: "tool-1", Name: "research_topic", Arguments: `{"topic":"python packaging"}`},
			{ID: "tool-2", Name: "search_skills", Arguments: `{"query":"packaging"}`},
		},
	})

	want := "Researching the topic and checking the available skills.\n\n"
	if got != want {
		t.Fatalf("IntermediateToolTurnText tool summary fallback = %q, want %q", got, want)
	}
}

func TestIntermediateToolTurnText_UsesAgentAwareNarrationForTester(t *testing.T) {
	// Phase 1/2 refactor: coord_query_view removed (use
	// query_peer_activity), coord_claim_scope folded into manage_claim.
	ctx := WithStreamContext(context.Background(), "corr-3", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{"agent_type": "tester-pipeline"})

	got := IntermediateToolTurnTextWithContext(ctx, &providers.Response{
		ToolCalls: []providers.ToolCall{
			{ID: "tool-1", Name: "check_inspector_gate", Arguments: `{}`},
			{ID: "tool-2", Name: "query_peer_activity", Arguments: `{}`},
			{ID: "tool-3", Name: "manage_claim", Arguments: `{"action":"acquire"}`},
		},
	})

	want := "Checking the inspector gate before test work begins, reviewing peer activity and prior task artifacts, and managing the scope claim for the test surface for this task.\n\n"
	if got != want {
		t.Fatalf("IntermediateToolTurnText tester narration = %q, want %q", got, want)
	}
}

func TestIntermediateToolTurnText_UsesAgentAwareNarrationForInspector(t *testing.T) {
	// Phase 1/2 refactor: coord_publish_artifact folded into
	// publish_work_event.
	ctx := WithStreamContext(context.Background(), "corr-4", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{"agent_type": "inspector-pipeline"})

	got := IntermediateToolTurnTextWithContext(ctx, &providers.Response{
		ToolCalls: []providers.ToolCall{
			{ID: "tool-1", Name: "query_peer_activity", Arguments: `{}`},
			{ID: "tool-2", Name: "read_workspace_file", Arguments: `{"path":"src/hello/cli.py"}`},
			{ID: "tool-3", Name: "publish_work_event", Arguments: `{"kind":"artifact"}`},
		},
	})

	want := "Reviewing peer activity and prior task artifacts, reading the relevant workspace files to compare the requested contract with the current implementation, and publishing the handoff artifact for downstream implementation.\n\n"
	if got != want {
		t.Fatalf("IntermediateToolTurnText inspector narration = %q, want %q", got, want)
	}
}

// TestPublishWithLifecycleState_ToolCallBypassesTerminalGate pins the (A) fix:
// once StreamComplete fires, text chunks are correctly suppressed but
// StreamEventToolCall events MUST still reach the publisher. Dropping them
// here was the largest single source of "tool completed but row stayed
// pending" reports — synthetic inter-agent branches (guardian approval
// callback, archivalist consult response) and slow handlers can both
// legitimately deliver a Complete after the parent stream's terminal lifecycle
// ends, and the UI cannot recover the row from bytes that never reached the
// bus.
func TestPublishWithLifecycleState_ToolCallBypassesTerminalGate(t *testing.T) {
	lifecycle := &streamLifecycleState{}

	// Drive the stream to its terminal state.
	chunkPublished := false
	if delivered, _, _ := publishWithLifecycleState(lifecycle, guide.StreamEventData, func() error { chunkPublished = true; return nil }); !delivered {
		t.Fatal("pre-terminal chunk must be delivered")
	}
	if !chunkPublished {
		t.Fatal("expected chunk publish to fire")
	}
	if delivered, _, _ := publishWithLifecycleState(lifecycle, guide.StreamEventComplete, func() error { return nil }); !delivered {
		t.Fatal("StreamComplete must be delivered")
	}

	// After terminal: text chunks are correctly dropped, tool-call events MUST
	// still pass through and the lateBypass signal MUST fire so callers can
	// log telemetry.
	lateChunkPublished := false
	if delivered, late, _ := publishWithLifecycleState(lifecycle, guide.StreamEventData, func() error { lateChunkPublished = true; return nil }); delivered {
		t.Fatal("post-terminal chunk must be suppressed")
	} else if late {
		t.Fatal("post-terminal chunk must not flag lateBypass")
	}
	if lateChunkPublished {
		t.Fatal("late chunk publish must not fire")
	}

	for _, eventType := range []guide.StreamEventType{guide.StreamEventToolCall} {
		published := false
		delivered, late, _ := publishWithLifecycleState(lifecycle, eventType, func() error { published = true; return nil })
		if !delivered {
			t.Fatalf("%s must bypass the terminal gate", eventType)
		}
		if !late {
			t.Fatalf("%s after terminal must report lateBypass=true for telemetry", eventType)
		}
		if !published {
			t.Fatalf("%s publish callback must fire", eventType)
		}
	}
}

// TestPublishWithLifecycleState_ToolCallBeforeTerminalDoesNotFlagBypass
// confirms the lateBypass signal only fires when the bypass actually rescued
// an event — pre-terminal tool-call events flow through the normal path with
// no telemetry noise.
func TestPublishWithLifecycleState_ToolCallBeforeTerminalDoesNotFlagBypass(t *testing.T) {
	lifecycle := &streamLifecycleState{}
	delivered, late, _ := publishWithLifecycleState(lifecycle, guide.StreamEventToolCall, func() error { return nil })
	if !delivered {
		t.Fatal("pre-terminal tool-call must be delivered")
	}
	if late {
		t.Fatal("pre-terminal tool-call must not report lateBypass")
	}
}

// TestPublishWithLifecycleState_NilLifecycleAlwaysPublishes covers the
// no-context path used by direct test fixtures; nil lifecycle delivers
// without flagging bypass.
func TestPublishWithLifecycleState_NilLifecycleAlwaysPublishes(t *testing.T) {
	published := false
	delivered, late, _ := publishWithLifecycleState(nil, guide.StreamEventToolCall, func() error { published = true; return nil })
	if !delivered || !published {
		t.Fatal("nil lifecycle must deliver the publish")
	}
	if late {
		t.Fatal("nil lifecycle never has a terminal to bypass")
	}
}
