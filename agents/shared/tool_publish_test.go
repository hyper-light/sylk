package shared

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestPublishToolCallStreamEvent_PreservesStreamMetadata(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("task-1:engineer", "task-1:engineer")
	streams := make(chan *guide.StreamResponse, 1)
	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streams <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	PublishToolCallStreamEvent(
		bus,
		channels,
		"task-1:engineer",
		"corr-tool-publish",
		"orchestrator",
		ToolCallEvent{
			ToolCallKey: "tc-readme",
			Phase:       ToolCallStart,
			ToolName:    "read_file",
			ArgsSummary: "path=README.md",
			StartedAt:   time.Now(),
			StreamMetadata: map[string]any{
				"agent_type":    "engineer",
				"pipeline_task": true,
				"task_id":       "task-1",
			},
		},
	)

	select {
	case stream := <-streams:
		if stream.Event == nil {
			t.Fatal("expected stream event")
		}
		if stream.Event.Type != guide.StreamEventToolCall {
			t.Fatalf("event type = %q, want %q", stream.Event.Type, guide.StreamEventToolCall)
		}
		if got, ok := stream.Metadata["task_id"].(string); !ok || got != "task-1" {
			t.Fatalf("stream metadata task_id = %#v, want task-1", stream.Metadata["task_id"])
		}
		if got, ok := stream.Metadata["agent_type"].(string); !ok || got != "engineer" {
			t.Fatalf("stream metadata agent_type = %#v, want engineer", stream.Metadata["agent_type"])
		}
		data, ok := stream.Event.Data.(map[string]any)
		if !ok {
			t.Fatalf("tool event data = %#v, want map", stream.Event.Data)
		}
		if got, ok := data["tool_call_key"].(string); !ok || got != "tc-readme" {
			t.Fatalf("tool_call_key = %#v, want tc-readme", data["tool_call_key"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tool call stream event")
	}
}

func TestPublishToolCallStreamEvent_NormalizesPartialInterAgentConsultStart(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("task-1:architect", "task-1:architect")
	streams := make(chan *guide.StreamResponse, 1)
	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streams <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	PublishToolCallStreamEvent(
		bus,
		channels,
		"task-1:architect",
		"corr-tool-publish",
		"tui",
		ToolCallEvent{
			ToolCallKey: "tc-consult",
			Phase:       ToolCallStart,
			ToolName:    "consult",
			FullArgs:    `{"mode":"single","target":"librarian","query":"Find relevant patterns."}`,
			StartedAt:   time.Now(),
			InterAgent: &InterAgentToolEvent{
				Kind:   InterAgentToolEventKindConsult,
				Status: InterAgentToolEventStatusPending,
			},
		},
	)

	select {
	case stream := <-streams:
		data, ok := stream.Event.Data.(map[string]any)
		if !ok {
			t.Fatalf("tool event data = %#v, want map", stream.Event.Data)
		}
		raw, ok := data["inter_agent"].(map[string]any)
		if !ok {
			t.Fatalf("inter_agent = %#v, want map", data["inter_agent"])
		}
		if got, _ := raw["kind"].(string); got != InterAgentToolEventKindConsult {
			t.Fatalf("kind = %q, want %q", got, InterAgentToolEventKindConsult)
		}
		targets, ok := raw["agent_types"].([]string)
		if !ok {
			t.Fatalf("agent_types = %#v, want []string", raw["agent_types"])
		}
		if len(targets) != 1 || targets[0] != "librarian" {
			t.Fatalf("agent_types = %#v, want [librarian]", targets)
		}
		if got, _ := raw["summary"].(string); got != "Find relevant patterns." {
			t.Fatalf("summary = %q, want %q", got, "Find relevant patterns.")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tool call stream event")
	}
}

func TestPublishToolCallStreamEvent_DropsUnderspecifiedInterAgentStart(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("task-1:architect", "task-1:architect")
	streams := make(chan *guide.StreamResponse, 1)
	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streams <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	PublishToolCallStreamEvent(
		bus,
		channels,
		"task-1:architect",
		"corr-tool-publish",
		"tui",
		ToolCallEvent{
			ToolCallKey: "tc-consult",
			Phase:       ToolCallStart,
			ToolName:    "consult",
			FullArgs:    `{"mode":"single","query":"Find relevant patterns."}`,
			StartedAt:   time.Now(),
			InterAgent: &InterAgentToolEvent{
				Kind:   InterAgentToolEventKindConsult,
				Status: InterAgentToolEventStatusPending,
			},
		},
	)

	select {
	case stream := <-streams:
		data, ok := stream.Event.Data.(map[string]any)
		if !ok {
			t.Fatalf("tool event data = %#v, want map", stream.Event.Data)
		}
		if _, exists := data["inter_agent"]; exists {
			t.Fatalf("inter_agent = %#v, want omitted", data["inter_agent"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tool call stream event")
	}
}
