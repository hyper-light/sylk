package shared

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
)

func TestWithToolCallEmitter_RoundTrip(t *testing.T) {
	var received []ToolCallEvent
	emitter := func(ev ToolCallEvent) error { received = append(received, ev); return nil }

	ctx := WithToolCallEmitter(context.Background(), emitter)
	if err := EmitToolCall(ctx, ToolCallEvent{ToolCallKey: "tc-1", ToolName: "read_file", Phase: ToolCallStart}); err != nil {
		t.Fatalf("EmitToolCall: %v", err)
	}

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].ToolName != "read_file" {
		t.Errorf("expected tool_name=read_file, got %s", received[0].ToolName)
	}
	if received[0].ToolCallKey != "tc-1" {
		t.Errorf("expected tool_call_key=tc-1, got %s", received[0].ToolCallKey)
	}
}

func TestEmitToolCall_NilEmitter(t *testing.T) {
	// Must not fail with a bare context (assuming event carries the required ID).
	if err := EmitToolCall(context.Background(), ToolCallEvent{ToolCallKey: "tc-nil", ToolName: "test"}); err != nil {
		t.Fatalf("EmitToolCall with no emitter: %v", err)
	}
}

func TestEmitToolCall_ReturnsErrorOnEmptyToolCallKey(t *testing.T) {
	var received []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { received = append(received, ev); return nil })

	err := EmitToolCall(ctx, ToolCallEvent{ToolName: "read_file", Phase: ToolCallStart})
	if err == nil {
		t.Fatal("expected error on empty ToolCallKey; got nil")
	}
	if !errors.Is(err, ErrMissingToolCallID) {
		t.Fatalf("expected errors.Is(%v, ErrMissingToolCallID) to be true", err)
	}
	if len(received) != 0 {
		t.Fatalf("expected event with empty ToolCallKey to be dropped, got %d: %#v", len(received), received)
	}
}

func TestToolCallEventKey_ReturnsErrorOnEmptyID(t *testing.T) {
	key, err := toolCallEventKey(providers.ToolCall{Name: "read_file"})
	if err == nil {
		t.Fatal("expected error on empty call.ID; got nil")
	}
	if !errors.Is(err, ErrMissingToolCallID) {
		t.Fatalf("expected errors.Is(%v, ErrMissingToolCallID) to be true", err)
	}
	if key != "" {
		t.Fatalf("expected empty key on error, got %q", key)
	}
}

func TestToolCallEventKey_ReturnsIDOnSuccess(t *testing.T) {
	key, err := toolCallEventKey(providers.ToolCall{ID: "tc_abc", Name: "read_file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "tc_abc" {
		t.Fatalf("key = %q, want %q", key, "tc_abc")
	}
}

func TestEmitToolCall_AttachesStreamMetadata(t *testing.T) {
	var received []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-tool-meta", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		"agent_type":    "engineer",
		"pipeline_task": true,
		"task_id":       "task-1",
	})
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error { received = append(received, ev); return nil })

	EmitToolCall(ctx, ToolCallEvent{ToolCallKey: "tc-meta", ToolName: "read_file", Phase: ToolCallStart})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if got, ok := received[0].StreamMetadata["task_id"].(string); !ok || got != "task-1" {
		t.Fatalf("stream metadata task_id = %#v, want task-1", received[0].StreamMetadata["task_id"])
	}
	if got, ok := received[0].StreamMetadata["agent_type"].(string); !ok || got != "engineer" {
		t.Fatalf("stream metadata agent_type = %#v, want engineer", received[0].StreamMetadata["agent_type"])
	}
}

func TestEmitToolCall_NormalizesPartialInterAgentConsultStartMetadata(t *testing.T) {
	var received []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { received = append(received, ev); return nil })

	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-consult",
		ToolName:    "consult",
		Phase:       ToolCallStart,
		FullArgs:    `{"mode":"single","target":"academic","query":"Assess the approach."}`,
		InterAgent: &InterAgentToolEvent{
			Kind:   InterAgentToolEventKindConsult,
			Status: InterAgentToolEventStatusPending,
		},
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].InterAgent == nil {
		t.Fatal("expected normalized inter-agent metadata")
	}
	if got := received[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "academic" {
		t.Fatalf("agent_types = %#v, want [academic]", got)
	}
	if got := received[0].InterAgent.Summary; got != "Assess the approach." {
		t.Fatalf("summary = %q, want %q", got, "Assess the approach.")
	}
}

func TestTimedToolCall_GlobalReviewChallengeEmitsTargetSpecificToolName(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-global-review-challenge", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		"agent_type": "inspector",
	})
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	call := providers.ToolCall{
		ID:        "call-global-review-challenge",
		Name:      "challenge_agent",
		Arguments: `{"target_agents":["tester"],"reason":"Need merged-state validation.","request":"Audit the merged state.","protocol_scope":"global_review"}`,
	}
	result, err := TimedToolCall(ctx, "inspector", call, func() (string, error) {
		return `{"selected":true,"target_agents":["tester"],"challenge_id":"global-review-123","protocol_scope":"global_review","thread_key":"global_review:global-review-123"}`, nil
	})
	if err != nil {
		t.Fatalf("TimedToolCall: %v", err)
	}
	if result == "" {
		t.Fatal("expected tool result")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}
	if got := events[0].ToolName; got != "challenge_global_tester" {
		t.Fatalf("start tool name = %q, want %q", got, "challenge_global_tester")
	}
	if got := events[1].ToolName; got != "challenge_global_tester" {
		t.Fatalf("complete tool name = %q, want %q", got, "challenge_global_tester")
	}
	if events[1].InterAgent == nil {
		t.Fatal("expected inter-agent metadata on completion")
	}
	if got := events[1].InterAgent.ThreadKey; got != "global_review:global-review-123" {
		t.Fatalf("completion thread key = %q, want %q", got, "global_review:global-review-123")
	}
}

func TestObserveProviderToolCallChunk_PreAnnouncesWithoutDuplicateTimedStart(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-provider-tool", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		"agent_type": "engineer",
		"task_id":    "task-1",
	})
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call-1",
			Name: "read_file",
		},
	})
	if len(events) != 1 {
		t.Fatalf("expected immediate pre-announced start on tool_start, got %d events", len(events))
	}
	if events[0].Phase != ToolCallStart {
		t.Fatalf("pre-announced phase = %d, want start", events[0].Phase)
	}
	if events[0].ArgsSummary != "" {
		t.Fatalf("initial args summary = %q, want empty before args delta arrives", events[0].ArgsSummary)
	}
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-1",
			ArgumentsDelta: `{"path":"README.md"`,
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-1",
			ArgumentsDelta: `}`,
		},
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 pre-announced event, got %d", len(events))
	}

	call := providers.ToolCall{
		ID:        "call-1",
		Name:      "read_file",
		Arguments: `{"path":"README.md"}`,
	}
	result, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("TimedToolCall: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q, want ok", result)
	}
	if len(events) != 2 {
		t.Fatalf("expected start + complete with no duplicate start, got %d events", len(events))
	}
	if events[1].Phase != ToolCallComplete {
		t.Fatalf("completion phase = %d, want complete", events[1].Phase)
	}
	if !events[1].Success {
		t.Fatal("expected completion success=true")
	}
	if events[1].StartedAt != events[0].StartedAt {
		t.Fatalf("completion started_at = %v, want preannounced %v", events[1].StartedAt, events[0].StartedAt)
	}
	if events[1].Duration < 0 {
		t.Fatalf("completion duration = %v, want non-negative", events[1].Duration)
	}
	if got, ok := events[1].StreamMetadata["task_id"].(string); !ok || got != "task-1" {
		t.Fatalf("completion task_id = %#v, want task-1", events[1].StreamMetadata["task_id"])
	}
}

func TestObserveProviderToolCallChunk_AnnouncesStartOnToolStartWhenIDIsKnown(t *testing.T) {
	var events []ToolCallEvent
	startedAt := time.Now().Add(-150 * time.Millisecond)
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type:      providers.ChunkTypeToolStart,
		Timestamp: startedAt,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call-immediate",
			Name: "web_fetch",
		},
	})

	if len(events) != 1 {
		t.Fatalf("expected immediate start event on tool_start, got %d events", len(events))
	}
	if events[0].Phase != ToolCallStart {
		t.Fatalf("phase = %d, want start", events[0].Phase)
	}
	if got, want := events[0].ToolCallKey, "call-immediate"; got != want {
		t.Fatalf("tool_call_key = %q, want %q", got, want)
	}
	if got, want := events[0].StartedAt, startedAt; !got.Equal(want) {
		t.Fatalf("started_at = %v, want %v", got, want)
	}

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID: "call-immediate",
		},
	})

	if len(events) != 1 {
		t.Fatalf("tool_end should not duplicate start event, got %d events", len(events))
	}
}

func TestObserveProviderToolCallChunk_GenericConsultWaitsForResolvableArgsBeforePreannounce(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call-consult",
			Name: "consult",
		},
	})
	if len(events) != 0 {
		t.Fatalf("expected generic consult tool_start without args to stay hidden, got %d events", len(events))
	}

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-consult",
			ArgumentsDelta: `{"target":"librarian","query":"Find relevant patterns."}`,
		},
	})
	if len(events) != 1 {
		t.Fatalf("expected consult start once args resolved, got %d events", len(events))
	}
	if events[0].Phase != ToolCallStart {
		t.Fatalf("phase = %d, want start", events[0].Phase)
	}
	if events[0].InterAgent == nil {
		t.Fatal("expected inter-agent consult metadata on resolved start event")
	}
	if got := events[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "librarian" {
		t.Fatalf("agent_types = %#v, want [librarian]", got)
	}
	if got := events[0].InterAgent.Summary; got != "Find relevant patterns." {
		t.Fatalf("summary = %q, want Find relevant patterns.", got)
	}

	call := providers.ToolCall{
		ID:        "call-consult",
		Name:      "consult",
		Arguments: `{"target":"librarian","query":"Find relevant patterns."}`,
	}
	if _, err := TimedToolCall(ctx, "academic", call, func() (string, error) {
		return `{"success":true}`, nil
	}); err != nil {
		t.Fatalf("TimedToolCall: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected start + completion, got %d events", len(events))
	}
	if events[1].Phase != ToolCallComplete {
		t.Fatalf("completion phase = %d, want complete", events[1].Phase)
	}
	if events[1].InterAgent == nil {
		t.Fatal("expected inter-agent consult metadata on completion event")
	}
}

func TestObserveProviderToolCallChunk_GenericConsultCanPreannounceAtToolEndWhenArgsArriveLate(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call-consult-end",
			Name: "consult",
		},
	})
	if len(events) != 0 {
		t.Fatalf("expected generic consult tool_start without args to stay hidden, got %d events", len(events))
	}

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-consult-end",
			ArgumentsDelta: `{"target":"librarian","query":"Inspect repo conventions."}`,
		},
	})
	if len(events) != 1 {
		t.Fatalf("expected consult start at tool_end once args resolved, got %d events", len(events))
	}
	if events[0].Phase != ToolCallStart {
		t.Fatalf("phase = %d, want start", events[0].Phase)
	}
	if events[0].InterAgent == nil {
		t.Fatal("expected inter-agent consult metadata on tool_end start event")
	}
	if got := events[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "librarian" {
		t.Fatalf("agent_types = %#v, want [librarian]", got)
	}
	if got := events[0].InterAgent.Summary; got != "Inspect repo conventions." {
		t.Fatalf("summary = %q, want Inspect repo conventions.", got)
	}
}

func TestObserveProviderToolCallChunk_StreamedChunksShareIDKeyAcrossPhases(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call-stable",
			Name: "read_file",
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-stable",
			ArgumentsDelta: `{"path":"README.md","line":1`,
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-stable",
			ArgumentsDelta: `}`,
		},
	})

	call := providers.ToolCall{
		ID:        "call-stable",
		Name:      "read_file",
		Arguments: "{\n  \"line\": 1,\n  \"path\": \"README.md\"\n}",
	}
	if _, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("TimedToolCall: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected pre-announced start plus completion, got %d events", len(events))
	}
	if events[0].Phase != ToolCallStart || events[1].Phase != ToolCallComplete {
		t.Fatalf("unexpected phases: %#v", events)
	}
	if events[0].ToolCallKey != "call-stable" || events[1].ToolCallKey != "call-stable" {
		t.Fatalf("tool call keys not equal to ID: start=%q complete=%q", events[0].ToolCallKey, events[1].ToolCallKey)
	}
}

func TestTimedToolCall_PreannouncedCompletionPreservesStartTime(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call-pre",
			Name: "consult_academic_approach",
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call-pre",
			ArgumentsDelta: `{"question":"Is there a cleaner approach?"}`,
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID: "call-pre",
		},
	})
	if len(events) != 1 {
		t.Fatalf("expected one preannounced event, got %d", len(events))
	}

	time.Sleep(15 * time.Millisecond)

	call := providers.ToolCall{
		ID:        "call-pre",
		Name:      "consult_academic_approach",
		Arguments: `{"question":"Is there a cleaner approach?"}`,
	}
	if _, err := TimedToolCall(ctx, "architect", call, func() (string, error) {
		time.Sleep(10 * time.Millisecond)
		return `{"consulted":true}`, nil
	}); err != nil {
		t.Fatalf("TimedToolCall: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected start + completion, got %d events", len(events))
	}
	if got, want := events[1].StartedAt, events[0].StartedAt; !got.Equal(want) {
		t.Fatalf("completion started_at = %v, want %v", got, want)
	}
	if events[1].Duration < 20*time.Millisecond {
		t.Fatalf("completion duration = %v, want >= 20ms from preannounced start", events[1].Duration)
	}
}

func TestObserveProviderToolCallChunk_NativeWebSearchCompletesAtToolEndWithoutDuplicateFallback(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })
	startedAt := time.Now().Add(-250 * time.Millisecond)
	completedAt := startedAt.Add(250 * time.Millisecond)

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type:      providers.ChunkTypeToolStart,
		Timestamp: startedAt,
		ToolCall: &providers.ToolCallChunk{
			ID:   "native-1",
			Name: "web_search",
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type:      providers.ChunkTypeToolDelta,
		Timestamp: startedAt.Add(100 * time.Millisecond),
		ToolCall: &providers.ToolCallChunk{
			ID:             "native-1",
			ArgumentsDelta: `{"query":"python packaging pep 621","action":"search"}`,
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type:      providers.ChunkTypeToolEnd,
		Timestamp: completedAt,
		ToolCall: &providers.ToolCallChunk{
			ID: "native-1",
		},
	})
	if len(events) != 2 {
		t.Fatalf("expected start + completion at tool end, got %d events", len(events))
	}
	if events[0].Phase != ToolCallStart || events[1].Phase != ToolCallComplete {
		t.Fatalf("unexpected phases: %#v", events)
	}
	if got, want := events[1].StartedAt, events[0].StartedAt; !got.Equal(want) {
		t.Fatalf("completion started_at = %v, want %v", got, want)
	}
	if events[0].StartedAt != startedAt {
		t.Fatalf("start started_at = %v, want %v", events[0].StartedAt, startedAt)
	}
	if events[1].Duration != 250*time.Millisecond {
		t.Fatalf("completion duration = %v, want 250ms", events[1].Duration)
	}

	time.Sleep(15 * time.Millisecond)

	CompleteProviderNativeToolCall(ctx, "academic", providers.ToolCall{
		ID:        "native-1",
		Name:      "web_search",
		Arguments: `{"query":"python packaging pep 621","action":"search"}`,
	}, "search complete")

	if len(events) != 2 {
		t.Fatalf("expected fallback completion to be suppressed after streamed completion, got %d events", len(events))
	}
}

// TestEmitToolCall_DeliversLateEventsAfterStreamComplete inverts the historical
// "suppresses late events" expectation: the stream-content terminal lifecycle
// must NOT gate tool-call events. Tool calls have their own out-of-band
// lifecycle (toolCallEventKey-paired Start/Complete) and a slow handler or
// callback-driven synthetic branch can legitimately deliver one after the
// agent's StreamComplete fires. Suppressing them here was the largest single
// source of "tool completed but row stayed pending" reports we ever hunted —
// no producer-side or matcher-side patch could close those rows because the
// bytes never reached the bus to begin with.
func TestEmitToolCall_DeliversLateEventsAfterStreamComplete(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-complete", "tui")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	if err := publishWithStreamLifecycle(ctx, guide.StreamEventComplete, func() error { return nil }); err != nil {
		t.Fatalf("expected stream complete lifecycle transition to succeed: %v", err)
	}

	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "ws_late",
		Phase:       ToolCallStart,
		ToolName:    "web_search",
		FullArgs:    `{"query":"late"}`,
	})

	if len(events) != 1 {
		t.Fatalf("expected late tool-call event to reach the emitter, got %d events", len(events))
	}
	if events[0].ToolCallKey != "ws_late" {
		t.Fatalf("late event ToolCallKey = %q, want ws_late", events[0].ToolCallKey)
	}
}

func TestTimedToolCall_Success(t *testing.T) {
	var events []ToolCallEvent
	emitter := func(ev ToolCallEvent) error { events = append(events, ev); return nil }
	ctx := WithToolCallEmitter(context.Background(), emitter)

	call := providers.ToolCall{ID: "1", Name: "grep", Arguments: `{"pattern":"foo"}`}
	result, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		time.Sleep(5 * time.Millisecond)
		return "found 3 matches", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "found 3 matches" {
		t.Errorf("expected result 'found 3 matches', got %q", result)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (start+complete), got %d", len(events))
	}

	start := events[0]
	if start.Phase != ToolCallStart {
		t.Errorf("expected phase=start, got %d", start.Phase)
	}
	if start.ToolName != "grep" {
		t.Errorf("expected tool_name=grep, got %s", start.ToolName)
	}
	if start.AgentID != "engineer" {
		t.Errorf("expected agent_id=engineer, got %s", start.AgentID)
	}

	complete := events[1]
	if complete.Phase != ToolCallComplete {
		t.Errorf("expected phase=complete, got %d", complete.Phase)
	}
	if !complete.Success {
		t.Error("expected success=true")
	}
	if complete.Duration < 5*time.Millisecond {
		t.Errorf("expected duration >= 5ms, got %v", complete.Duration)
	}
	if complete.Output != "found 3 matches" {
		t.Errorf("expected output 'found 3 matches', got %q", complete.Output)
	}
}

func TestTimedToolCall_WaitsWhilePaused(t *testing.T) {
	ledger := steering.NewSteeringLedger("corr-paused-tool", "engineer", "sess", nil, nil)
	ledger.SetPace(steering.PacePaused)
	ctx := WithSteeringLedger(context.Background(), ledger)

	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	call := providers.ToolCall{ID: "paused-1", Name: "read_file", Arguments: `{"path":"README.md"}`}

	go func() {
		_, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
			started <- struct{}{}
			return "ok", nil
		})
		done <- err
	}()

	select {
	case <-started:
		t.Fatal("tool call started while paused")
	case <-time.After(100 * time.Millisecond):
	}

	ledger.SetPace(steering.PaceAuto)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tool call did not start after resume")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TimedToolCall returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TimedToolCall did not finish after resume")
	}
}

func TestTimedToolCall_Error(t *testing.T) {
	var events []ToolCallEvent
	emitter := func(ev ToolCallEvent) error { events = append(events, ev); return nil }
	ctx := WithToolCallEmitter(context.Background(), emitter)

	call := providers.ToolCall{ID: "2", Name: "run_command", Arguments: `{"command":"make"}`}
	_, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		return "", errTestToolFailed
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	complete := events[1]
	if complete.Success {
		t.Error("expected success=false on error")
	}
	if complete.ErrorMsg == "" {
		t.Error("expected non-empty error_msg")
	}
}

func TestTimedToolCall_PipelineHandoffIsControlOutcome(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	call := providers.ToolCall{ID: "4", Name: "handoff_next", Arguments: `{"target_agents":["tester"]}`}
	result, err := TimedToolCall(ctx, "inspector-pipeline", call, func() (string, error) {
		return `{"forwarded":true,"target_agent_id":"task_1-tester-pipeline"}`, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if result != `{"forwarded":true,"target_agent_id":"task_1-tester-pipeline"}` {
		t.Fatalf("result = %q", result)
	}

	complete := events[1]
	if !complete.Success {
		t.Fatal("expected handoff result to be marked successful")
	}
	if complete.ErrorMsg != "" {
		t.Fatalf("expected empty error_msg, got %q", complete.ErrorMsg)
	}
	if complete.Output != `{"forwarded":true,"target_agent_id":"task_1-tester-pipeline"}` {
		t.Fatalf("output = %q", complete.Output)
	}
}

func TestTimedToolCall_RerouteIsControlOutcome(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	call := providers.ToolCall{ID: "5", Name: "reroute_request", Arguments: `{"suggested_target":"guide"}`}
	_, err := TimedToolCall(ctx, "engineer", call, func() (string, error) {
		return "", skills.ErrRerouteRequested
	})

	if !errors.Is(err, skills.ErrRerouteRequested) {
		t.Fatalf("expected ErrRerouteRequested, got %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	complete := events[1]
	if !complete.Success {
		t.Fatal("expected reroute control outcome to be marked successful")
	}
	if complete.ErrorMsg != "" {
		t.Fatalf("expected empty error_msg, got %q", complete.ErrorMsg)
	}
	if complete.Output != `{"rerouted":true}` {
		t.Fatalf("output = %q, want rerouted payload", complete.Output)
	}
}

var errTestToolFailed = &testError{msg: "tool failed: exit code 1"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestTimedToolCall_NoEmitter(t *testing.T) {
	// TimedToolCall must work without panicking when no emitter is set.
	call := providers.ToolCall{ID: "3", Name: "read_file", Arguments: `{}`}
	result, err := TimedToolCall(context.Background(), "test", call, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestSummarizeToolArgs(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     string
		want     string
	}{
		{"path key", "read_file", `{"file_path":"core/main.go","offset":1}`, "file_path=core/main.go"},
		{"pattern key", "grep", `{"pattern":"StreamEvent"}`, "pattern=StreamEvent"},
		{"command key", "run_command", `{"command":"go build ./..."}`, "command=go build ./..."},
		{"script key", "run_shell_script", `{"script":"cd ui && pnpm test"}`, "script=cd ui && pnpm test"},
		{"empty args", "test", `{}`, ""},
		{"empty string", "test", "", ""},
		{"invalid json", "test", "not json", ""},
		{"no priority keys", "custom", `{"foo":"bar"}`, "foo=bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummarizeToolArgs(tt.toolName, tt.args)
			if got != tt.want {
				t.Errorf("SummarizeToolArgs(%q, %q) = %q, want %q", tt.toolName, tt.args, got, tt.want)
			}
		})
	}
}

func TestSummarizeToolArgs_Truncation(t *testing.T) {
	longPath := "/very/long/path/that/exceeds/sixty/characters/and/should/be/truncated/by/the/summarizer"
	got := SummarizeToolArgs("read_file", `{"path":"`+longPath+`"}`)
	if len([]rune(got)) > maxArgsSummaryLen {
		t.Errorf("summary exceeds %d chars: %d", maxArgsSummaryLen, len([]rune(got)))
	}
}

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		expected string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world", 8, "hello..."},
		{"tiny max", "abcd", 3, "..."},
		{"empty", "", 10, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateOutput(tt.input, tt.max)
			if got != tt.expected {
				t.Errorf("TruncateOutput(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
			}
		})
	}
}

func TestPrettyPrintArgs(t *testing.T) {
	got := PrettyPrintArgs(`{"a":1,"b":"two"}`)
	if got == `{"a":1,"b":"two"}` {
		t.Error("expected indented output, got raw")
	}
	// Should contain newlines and indentation.
	if len(got) <= len(`{"a":1,"b":"two"}`) {
		t.Errorf("expected longer formatted output, got %q", got)
	}
}

func TestPrettyPrintArgs_Invalid(t *testing.T) {
	got := PrettyPrintArgs("not json")
	if got != "not json" {
		t.Errorf("expected passthrough for invalid JSON, got %q", got)
	}
}

func TestPrettyPrintArgs_Empty(t *testing.T) {
	if got := PrettyPrintArgs(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := PrettyPrintArgs("{}"); got != "{}" {
		t.Errorf("expected '{}', got %q", got)
	}
}

// TestTimedToolCall_ReusesPinnedEventKeyFromPreannounce verifies that when the
// streaming observer preannounces a Start event with a particular set of
// accumulated arguments and the tool loop later assembles a slightly different
// final argument string, the Complete event still pairs with the Start by
// reusing the pinned EventKey. This is the generalization of the web_search
// fix to any tool that routes through TimedToolCall — notably challenge_agent
// and consult, whose `genericInterAgentToolRequiresResolvedArgs` gate delays
// the preannounce until target_agents becomes parseable, after which later
// argument deltas can still alter the args-derived key.
func TestTimedToolCall_ReusesPinnedEventKeyFromPreannounce(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-tool-call", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		"agent_type":  "inspector-pipeline",
		"pipeline_id": "task_1",
	})
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	preannounceArgs := `{"target_agents":["tester"],"reason":"need"}`
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "call_1",
			Name: "challenge_agent",
		},
	})
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "call_1",
			Name:           "challenge_agent",
			ArgumentsDelta: preannounceArgs,
		},
	})
	if len(events) != 1 || events[0].Phase != ToolCallStart {
		t.Fatalf("expected single ToolCallStart, got %d events", len(events))
	}
	startKey := events[0].ToolCallKey
	if strings.TrimSpace(startKey) == "" {
		t.Fatal("expected non-empty start ToolCallKey")
	}

	// The tool loop receives the assembled call with a longer argument payload
	// (extra request field). Without pinning, toolCallEventKey(call) would
	// derive a different signature than the preannounced Start.
	call := providers.ToolCall{
		ID:        "call_1",
		Name:      "challenge_agent",
		Arguments: `{"target_agents":["tester"],"reason":"need","request":"audit"}`,
	}
	_, _ = TimedToolCall(ctx, "inspector-pipeline", call, func() (string, error) {
		return "", errors.New("unknown pipeline target agent \"foo\"")
	})

	if len(events) != 2 {
		t.Fatalf("expected start + complete, got %d events", len(events))
	}
	if events[1].Phase != ToolCallComplete {
		t.Fatalf("phase = %d, want ToolCallComplete", events[1].Phase)
	}
	if events[1].ToolCallKey != startKey {
		t.Fatalf("complete ToolCallKey = %q, want pinned start key %q", events[1].ToolCallKey, startKey)
	}
	if events[1].Success {
		t.Fatal("expected complete success=false after handler error")
	}
}

// TestObserveProviderToolCallChunk_NativeWebSearchPairsStartAndEndDespiteArgDrift
// reproduces the historical "academic web_search status=in_progress 7m33s" stuck
// row: the provider streams the call's arguments with a lifecycle "status" field
// that flips from in_progress to completed between Start and End. Without a
// stable EventKey the ToolCallStart and ToolCallComplete events would carry
// different ToolCallKey signatures and the UI matcher would never pair them.
func TestObserveProviderToolCallChunk_NativeWebSearchPairsStartAndEndDespiteArgDrift(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-web-search", "tui")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:             "ws_call_1",
			Name:           "web_search",
			Kind:           providers.ToolKindNativeWebSearch,
			ArgumentsDelta: `{"action":"search","query":"sylk","status":"in_progress"}`,
		},
	})
	if len(events) != 1 || events[0].Phase != ToolCallStart {
		t.Fatalf("expected single ToolCallStart, got %d events", len(events))
	}
	if strings.TrimSpace(events[0].ToolCallKey) == "" {
		t.Fatal("expected non-empty start ToolCallKey")
	}
	startKey := events[0].ToolCallKey

	// Provider sends the End chunk with the lifecycle status flipped — the
	// accumulated state.Arguments will now contain the in_progress JSON
	// concatenated with the completed JSON. Without the EventKey pinning,
	// toolCallEventKey would derive a different signature here.
	ObserveProviderToolCallChunk(ctx, &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID:             "ws_call_1",
			Name:           "web_search",
			Kind:           providers.ToolKindNativeWebSearch,
			ArgumentsDelta: `{"action":"search","query":"sylk","status":"completed"}`,
		},
	})

	if len(events) != 2 {
		t.Fatalf("expected start + complete, got %d events", len(events))
	}
	if events[1].Phase != ToolCallComplete {
		t.Fatalf("phase = %d, want ToolCallComplete", events[1].Phase)
	}
	if events[1].ToolCallKey != startKey {
		t.Fatalf("complete ToolCallKey = %q, want pinned start key %q", events[1].ToolCallKey, startKey)
	}
	if !events[1].Success {
		t.Fatal("expected native web_search completion success=true")
	}
}

// TestToolCallTracker_InFlightSnapshotReflectsStartCompletePairs verifies the
// orphan-tracking surface used by (D). EmitToolCall records Start emissions
// in tracker.inFlight; Complete emissions clear them. snapshotInFlight returns
// only the unmatched Starts, sorted by StartedAt ascending so the oldest
// (most-likely-stuck) calls appear first.
func TestToolCallTracker_InFlightSnapshotReflectsStartCompletePairs(t *testing.T) {
	emitter := func(ev ToolCallEvent) error { return nil }
	ctx := WithToolCallEmitter(context.Background(), emitter)
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		t.Fatal("expected tracker on ctx")
	}

	now := time.Now()
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-old",
		ToolName:    "summarize_workspace_state",
		Phase:       ToolCallStart,
		StartedAt:   now.Add(-2 * time.Minute),
	})
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-new",
		ToolName:    "read_workspace_file",
		Phase:       ToolCallStart,
		StartedAt:   now.Add(-3 * time.Second),
	})
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-paired",
		ToolName:    "coord_query_view",
		Phase:       ToolCallStart,
		StartedAt:   now,
	})
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-paired",
		ToolName:    "coord_query_view",
		Phase:       ToolCallComplete,
		StartedAt:   now,
	})

	snapshot := tracker.snapshotInFlightToolCalls()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 in-flight calls (the paired one cleared), got %d: %#v", len(snapshot), snapshot)
	}
	if snapshot[0].ToolCallKey != "tc-old" {
		t.Fatalf("oldest first: snapshot[0].ToolCallKey = %q, want tc-old", snapshot[0].ToolCallKey)
	}
	if snapshot[1].ToolCallKey != "tc-new" {
		t.Fatalf("newer second: snapshot[1].ToolCallKey = %q, want tc-new", snapshot[1].ToolCallKey)
	}
}

// TestToolCallTracker_LateCompleteClearsInFlight covers the (A)+(D) interaction:
// a Complete event arriving after the parent stream's terminal lifecycle (which
// (A) now allows through) must still drain the in-flight tracker so the orphan
// log doesn't continue surfacing the same call after every subsequent
// terminal event.
func TestToolCallTracker_LateCompleteClearsInFlight(t *testing.T) {
	emitter := func(ev ToolCallEvent) error { return nil }
	ctx := WithStreamContext(context.Background(), "corr-late", "tui")
	ctx = WithToolCallEmitter(ctx, emitter)
	tracker := toolCallTrackerFromContext(ctx)

	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-late",
		ToolName:    "summarize_workspace_state",
		Phase:       ToolCallStart,
		StartedAt:   time.Now().Add(-30 * time.Second),
	})

	// Drive stream to terminal.
	if err := publishWithStreamLifecycle(ctx, guide.StreamEventComplete, func() error { return nil }); err != nil {
		t.Fatalf("publishWithStreamLifecycle: %v", err)
	}

	if got := len(tracker.snapshotInFlightToolCalls()); got != 1 {
		t.Fatalf("expected 1 in-flight call before late Complete, got %d", got)
	}

	// Late Complete arrives after terminalStarted; (A) lets it through.
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: "tc-late",
		ToolName:    "summarize_workspace_state",
		Phase:       ToolCallComplete,
		Success:     true,
	})

	if got := len(tracker.snapshotInFlightToolCalls()); got != 0 {
		t.Fatalf("late Complete must clear in-flight entry, got %d remaining", got)
	}
}

// TestToolCallSession_StartIsIdempotentAcrossAcquires verifies the (C) core
// invariant: two callers reaching for "the session for this call.ID" share
// one handle, and Start fires exactly once. This is what kills the
// "preannounce + TimedToolCall both emit Start → UI gets duplicate rows"
// failure mode at the source. Local patches at one emit site couldn't fix
// it because the next emit site reintroduced the same pattern; sessions
// make the invariant structural.
func TestToolCallSession_StartIsIdempotentAcrossAcquires(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	call := providers.ToolCall{ID: "shared-id", Name: "read_file", Arguments: `{"path":"a.go"}`}

	a := acquireToolCallSession(ctx, "agent", call)
	b := acquireToolCallSession(ctx, "agent", call)
	if a != b {
		t.Fatal("expected the same session pointer for matching call.ID")
	}

	if !a.Start(call, time.Now(), nil) {
		t.Fatal("first Start must report emitted=true")
	}
	if b.Start(call, time.Now(), nil) {
		t.Fatal("second Start (via the parallel acquire) must be a no-op")
	}
	if got := len(events); got != 1 {
		t.Fatalf("expected exactly 1 ToolCallStart event, got %d: %#v", got, events)
	}
}

// TestToolCallSession_CompleteIsIdempotentAcrossPaths confirms a Complete
// from one path (e.g. the web_search ToolEnd branch) prevents a later
// Complete from a different path (e.g. CompleteProviderNativeToolCall)
// from emitting a second terminal event. Without this, the UI sees two
// Completes for the same key and the second clobbers the first's state.
func TestToolCallSession_CompleteIsIdempotentAcrossPaths(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithToolCallEmitter(context.Background(), func(ev ToolCallEvent) error { events = append(events, ev); return nil })

	call := providers.ToolCall{ID: "complete-id", Name: "summarize_workspace_state", Arguments: `{"paths":["a"]}`}

	session := acquireToolCallSession(ctx, "tester-pipeline", call)
	session.Start(call, time.Now(), nil)
	if !session.Complete(call, `{"ok":true}`, nil) {
		t.Fatal("first Complete must report emitted=true")
	}
	if session.Complete(call, `{"ok":true}`, nil) {
		t.Fatal("second Complete must be a no-op")
	}
	completes := 0
	for _, e := range events {
		if e.Phase == ToolCallComplete {
			completes++
		}
	}
	if completes != 1 {
		t.Fatalf("expected exactly 1 ToolCallComplete event, got %d", completes)
	}
}

// TestToolCallSession_CompleteReleasesTrackerEntry confirms a Completed
// session is removed from tracker.sessions so a future call.ID reuse (e.g.
// agent reissuing a tool call) gets a fresh session, not a stuck "already
// completed" handle that would silently swallow the new emissions.
func TestToolCallSession_CompleteReleasesTrackerEntry(t *testing.T) {
	emitter := func(ev ToolCallEvent) error { return nil }
	ctx := WithToolCallEmitter(context.Background(), emitter)

	call := providers.ToolCall{ID: "reuse-id", Name: "read_file", Arguments: `{"path":"a.go"}`}

	first := acquireToolCallSession(ctx, "agent", call)
	first.Start(call, time.Now(), nil)
	first.Complete(call, "ok", nil)

	second := acquireToolCallSession(ctx, "agent", call)
	if first == second {
		t.Fatal("after Complete, a fresh acquire must return a new session pointer")
	}
	if !second.Start(call, time.Now(), nil) {
		t.Fatal("the fresh session's Start must be emittable")
	}
}
