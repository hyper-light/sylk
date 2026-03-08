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
