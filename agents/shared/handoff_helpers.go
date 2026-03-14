package shared

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// EstimateContextSize approximates total context tokens from accumulated messages.
// Uses ~4 characters per token plus overhead per message, matching core/llm/context.go.
func EstimateContextSize(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)/4 + 4
	}
	return total
}

// ContextSizeForTurn returns the best available request-wide context occupancy
// for a completed turn. Provider-reported input tokens are ground truth when
// available; otherwise we fall back to message estimation.
func ContextSizeForTurn(req *providers.Request, resp *providers.Response) int {
	if resp != nil && resp.Usage.InputTokens > 0 {
		return resp.Usage.InputTokens
	}
	if req == nil {
		return 0
	}
	return EstimateContextSize(req.Messages)
}

// BuildHandoffTurnRecord constructs a bridge TurnRecord from the completed LLM
// call, preferring provider-reported context occupancy and threading through
// per-request logging metadata for bridge-side trace emission.
func BuildHandoffTurnRecord(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) handoff.TurnRecord {
	meta := LogMetaFromContext(ctx)
	usage := providers.Usage{}
	stopReason := providers.StopReason("")
	if resp != nil {
		usage = resp.Usage
		stopReason = resp.StopReason
	}

	successes := toolCalls - errCount
	if successes < 0 {
		successes = 0
	}

	record := handoff.TurnRecord{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		ContextSize:      ContextSizeForTurn(req, resp),
		ToolCalls:        toolCalls,
		ToolSuccesses:    successes,
		TurnNumber:       turn + 1,
		Duration:         time.Since(turnStart),
		Timestamp:        time.Now(),
		StopReason:       stopReason,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		CorrelationID:    meta.CorrID,
		SessionID:        meta.SessionID,
		EventLogger:      meta.EventLogger,
	}
	if req != nil {
		record.Stage = llmruntime.StageFromRequest(req)
		record.RuntimeProfile = llmruntime.ProfileNameFromRequest(req)
	}
	return record
}
