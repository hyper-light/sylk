package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// StreamingTurnProvider is the narrow interface a provider must satisfy
// to drive a streaming tool-loop turn. Satisfied by the gateway and by
// *providers.AnthropicProvider / *providers.OpenAIProvider.
type StreamingTurnProvider interface {
	Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error)
}

// StreamingLLMTurnConfig configures a streamed tool-loop turn. Bus,
// Channels, and AgentID identify the publishing surface;
// AgentDisplayName is used by the thinking watchdog for
// human-readable status.
type StreamingLLMTurnConfig struct {
	Bus              guide.EventBus
	Channels         *guide.AgentChannels
	AgentID          string
	AgentDisplayName string
}

// StreamLLMTurn runs one streamed LLM turn and publishes thinking +
// text chunks to the chat panel as they arrive. It mirrors the
// pattern in academic/tool_loop.go.streamLLMTurn so librarian,
// global inspector, and global tester can use identical streaming
// semantics without each owning a copy.
//
// Behavior:
//   - Starts a thinking watchdog (cancelled on first visible chunk).
//   - Buffers text chunks into a single final `PublishStreamChunk`
//     when the turn produced no tool calls.
//   - Publishes thought-progress narrations via the progress publisher
//     already on ctx (no-op when absent).
//   - When tool calls are present, publishes an intermediate
//     `PublishIntermediateToolTurn` and returns — the caller's tool
//     loop executes them and invokes StreamLLMTurn again next turn.
//
// Caller retains agent-specific retry/deadline handling around this
// function; StreamLLMTurn itself makes a single Stream() call and
// returns whatever the provider yielded.
func StreamLLMTurn(
	ctx context.Context,
	cfg StreamingLLMTurnConfig,
	sp StreamingTurnProvider,
	req *providers.Request,
) (*providers.Response, error) {
	if sp == nil || req == nil {
		return nil, fmt.Errorf("streaming turn: provider and request are required")
	}
	if pp := ProgressPublisherFromContext(ctx); pp != nil {
		llmruntime.PromoteForUserFacingTurn(req, pp.SourceAgentID, llmruntime.ThoughtVisibilitySummary)
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	chunks, err := sp.Stream(streamCtx, req)
	if err != nil {
		return nil, err
	}

	displayName := strings.TrimSpace(cfg.AgentDisplayName)
	cancelWatchdog := StartThinkingWatchdog(ctx, req, displayName)
	defer cancelWatchdog()

	var (
		streamErr         error
		firstVisibleChunk bool
		bufferedText      strings.Builder
	)
	emitter := NewThoughtEmitter(llmruntime.EmitsThoughts(req))

	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		if chunk == nil {
			return
		}
		ObserveProviderToolCallChunk(ctx, chunk)
		switch chunk.Type {
		case providers.ChunkTypeStart:
			if chunk.RetryReset {
				bufferedText.Reset()
			}
		case providers.ChunkTypeText:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			bufferedText.WriteString(chunk.Text)
		case providers.ChunkTypeThought:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			if thought := emitter.AddDelta(chunk.Text); thought != "" {
				publishThoughtProgressShared(ctx, thought)
				LogThinkingChunk(ctx, thought)
			}
		}
	})

	for chunk := range chunks {
		if chunk != nil && chunk.Type == providers.ChunkTypeStart && chunk.RetryReset {
			streamErr = nil
		}
		collector.Add(chunk)
		if chunk != nil && chunk.Type == providers.ChunkTypeError {
			streamErr = fmt.Errorf("stream error: %s", chunk.Text)
		}
	}
	if thought := emitter.Flush(); thought != "" {
		publishThoughtProgressShared(ctx, thought)
		LogThinkingChunk(ctx, thought)
	}

	resp := collector.Response()
	if resp != nil && len(resp.ToolCalls) > 0 {
		PublishIntermediateToolTurn(cfg.Bus, cfg.Channels, ctx, cfg.AgentID, resp)
		return resp, nil
	}

	var streamedText bool
	if resp != nil {
		if text := bufferedText.String(); strings.TrimSpace(text) != "" {
			streamedText = true
			_ = PublishStreamChunk(cfg.Bus, cfg.Channels, ctx, cfg.AgentID, text)
		}
	}

	if streamErr != nil {
		return nil, streamErr
	}
	if resp != nil && streamedText {
		MarkResponseStreamedText(resp)
	}
	return resp, nil
}

// publishThoughtProgressShared forwards a thought narration through
// the context's progress publisher. Matches the per-agent helper
// (e.g. academic.publishThoughtProgress) — lifted here so the shared
// streaming turn doesn't require every caller to own one.
func publishThoughtProgressShared(ctx context.Context, thought string) {
	pp := ProgressPublisherFromContext(ctx)
	if pp == nil {
		return
	}
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return
	}
	pp.Publish(thought)
}
