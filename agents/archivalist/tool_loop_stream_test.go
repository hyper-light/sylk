package archivalist

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type blockingStreamingArchivalistProvider struct {
	release chan struct{}
}

func (p *blockingStreamingArchivalistProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{Content: "unexpected complete fallback"}, nil
}

func (p *blockingStreamingArchivalistProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk, 8)
	go func() {
		defer close(ch)
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
		ch <- &providers.StreamChunk{
			Type: providers.ChunkTypeToolStart,
			ToolCall: &providers.ToolCallChunk{
				ID:   "tool-1",
				Name: "unknown_archivalist_tool",
			},
		}
		ch <- &providers.StreamChunk{
			Type: providers.ChunkTypeToolDelta,
			ToolCall: &providers.ToolCallChunk{
				ID:             "tool-1",
				ArgumentsDelta: `{"query":"prior incident"}`,
			},
		}
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeToolEnd, ToolCall: &providers.ToolCallChunk{ID: "tool-1"}}
		<-p.release
		ch <- &providers.StreamChunk{
			Type:       providers.ChunkTypeEnd,
			StopReason: providers.StopReasonToolUse,
		}
	}()
	return ch, nil
}

func TestArchivalistExecuteToolLoop_StreamsToolCallsForNestedConsults(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a := newTestArchivalist(t)
	a.provider = &blockingStreamingArchivalistProvider{release: make(chan struct{})}

	if err := a.Start(bus); err != nil {
		t.Fatalf("start archivalist: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 16)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Recall the relevant prior incident."}},
		Tools:    a.buildToolDefinitions(),
		Model:    "gpt-5.4-pro",
	}
	ctx := shared.WithStreamContext(context.Background(), "corr-archivalist-nested", "architect")
	ctx = shared.WithStreamContextMetadata(ctx, map[string]any{"agent_type": "archivalist"})
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus:           bus,
		Channels:      a.channels,
		AgentID:       a.id,
		CorrelationID: "corr-archivalist-nested",
		SourceAgentID: "architect",
	})
	ctx = shared.WithToolCallEmitter(ctx, shared.NewToolCallEmitter(bus, a.channels, a.id, "corr-archivalist-nested", "architect"))

	done := make(chan error, 1)
	go func() {
		_, err := a.executeToolLoop(ctx, req, nil)
		done <- err
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-archivalist-nested" || stream.Event == nil {
				continue
			}
			if stream.Event.Type != guide.StreamEventToolCall {
				continue
			}
			data, ok := stream.Event.Data.(map[string]any)
			if !ok {
				continue
			}
			if got, _ := data["tool_name"].(string); got != "unknown_archivalist_tool" {
				continue
			}
			phase, ok := data["phase"].(int)
			if !ok {
				if typed, ok := data["phase"].(float64); ok {
					phase = int(typed)
				}
			}
			if phase != 0 {
				continue
			}
			close(a.provider.(*blockingStreamingArchivalistProvider).release)
			if err := <-done; err == nil {
				t.Fatal("expected executeToolLoop to fail validation for unknown tool")
			}
			return
		case <-deadline:
			close(a.provider.(*blockingStreamingArchivalistProvider).release)
			<-done
			t.Fatal("timed out waiting for streamed archivalist tool-call start")
		}
	}
}
