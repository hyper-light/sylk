package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
)

const (
	llmEventBufferSize        = 256
	llmBatchWindow            = 500 * time.Millisecond
	defaultBootstrapSafetyDur = 30 * time.Second
)


// eventSeverity classifies how urgent a bus event is.
type eventSeverity int

const (
	severityInfo     eventSeverity = iota
	severityWarning
	severityCritical
)

func (s eventSeverity) String() string {
	switch s {
	case severityWarning:
		return "WARNING"
	case severityCritical:
		return "CRITICAL"
	default:
		return "INFO"
	}
}

// busEvent wraps a bus event for the LLM processing pipeline.
type busEvent struct {
	Topic     string
	Timestamp time.Time
	Severity  eventSeverity
	Summary   string
	Data      map[string]any
}

// pushEvent sends a bus event to the LLM loop channel. Non-blocking: drops on
// full channel to maintain backpressure safety.
func (o *Orchestrator) pushEvent(evt *busEvent) {
	if o.eventCh == nil {
		return
	}
	select {
	case o.eventCh <- evt:
	default:
		// Channel full — drop oldest semantics via non-blocking send.
	}
}

// runLLMLoop is the main LLM event processing goroutine.
// It blocks on the bootstrap gate until the system signals readiness (or a
// safety deadline / critical event fires), then enters the steady-state
// event loop with classification, filtering, and deduplication.
//
// Health monitoring is handled deterministically by the HealthMonitor; this
// loop only reacts to bus events that require LLM judgment.
func (o *Orchestrator) runLLMLoop(ctx context.Context) {
	criticals := o.bootGate.Wait(ctx, o.eventCh)
	if ctx.Err() != nil {
		return
	}
	if len(criticals) > 0 {
		o.processBatch(ctx, criticals)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-o.eventCh:
			batch := o.collectBatch(ctx, evt)
			batch = filterBatch(batch)
			if len(batch) == 0 {
				continue
			}
			batch = deduplicateBatch(batch)
			o.processBatch(ctx, batch)
		}
	}
}

// collectBatch gathers events from the channel. If the initial event is
// critical, returns immediately. Otherwise waits up to llmBatchWindow for
// additional events.
func (o *Orchestrator) collectBatch(ctx context.Context, initial *busEvent) []*busEvent {
	batch := []*busEvent{initial}

	if initial.Severity == severityCritical {
		return o.drainPending(batch)
	}

	timer := time.NewTimer(llmBatchWindow)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return batch
		case evt := <-o.eventCh:
			batch = append(batch, evt)
			if evt.Severity == severityCritical {
				return o.drainPending(batch)
			}
		case <-timer.C:
			return o.drainPending(batch)
		}
	}
}

// drainPending pulls any remaining buffered events without blocking.
func (o *Orchestrator) drainPending(batch []*busEvent) []*busEvent {
	for {
		select {
		case evt := <-o.eventCh:
			batch = append(batch, evt)
		default:
			return batch
		}
	}
}

// processBatch sends the event batch to the LLM for analysis.
// For batches containing only info-severity events, LLM failures are silently
// ignored — the fallback path only matters for critical events.
func (o *Orchestrator) processBatch(ctx context.Context, batch []*busEvent) {
	if o.provider == nil {
		o.fallbackEscalateCritical(batch)
		return
	}

	o.publishActivity(events.EventTypeLLMResponse, fmt.Sprintf("Analyzing %d events", len(batch)))

	digest := buildDigestMessage(batch)

	o.mu.RLock()
	snapshot := o.buildStateSnapshot()
	o.mu.RUnlock()

	userMsg := digest + "\n\n## Current State Snapshot\n" + snapshot

	req := o.buildLLMRequest(userMsg)

	llmCtx, cancel := context.WithTimeout(ctx, o.config.LLMTimeout)
	defer cancel()
	llmCtx = providers.WithRetryObserver(llmCtx, o.retryObserver())

	_, err := o.executeToolLoop(llmCtx, req)
	if err != nil {
		if batchMaxSeverity(batch) >= severityCritical {
			o.publishActivity(events.EventTypeAgentError, "LLM analysis failed, using fallback")
		}
		o.fallbackEscalateCritical(batch)
		return
	}
	o.publishActivity(events.EventTypeLLMResponse, fmt.Sprintf("Processed %d events", len(batch)))
}

// buildLLMRequest constructs a providers.Request with system prompt, tools,
// and the given user message.
func (o *Orchestrator) buildLLMRequest(userMessage string) *providers.Request {
	temp := o.config.Temperature
	tools := o.buildToolDefinitions()
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		Model:           o.config.Model,
		SystemPrompt:    DefaultSystemPrompt,
		MaxTokens:       o.config.MaxOutputTokens,
		Temperature:     &temp,
		ReasoningEffort: "low",
		Tools:           tools,
	}
	if len(tools) > 0 {
		req.ToolChoice = "auto"
	}
	return req
}

// buildDigestMessage formats a batch of events into a markdown digest.
func buildDigestMessage(batch []*busEvent) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Event Batch (%d events)\n\n", len(batch)))

	for i, evt := range batch {
		b.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, evt.Severity, evt.Summary))
	}

	return b.String()
}

// batchMaxSeverity returns the highest severity in the batch.
func batchMaxSeverity(batch []*busEvent) eventSeverity {
	maxSev := severityInfo
	for _, evt := range batch {
		if evt.Severity > maxSev {
			maxSev = evt.Severity
		}
	}
	return maxSev
}

// fallbackEscalateCritical auto-escalates critical events when the LLM is
// unavailable. This is the deterministic fallback path.
func (o *Orchestrator) fallbackEscalateCritical(batch []*busEvent) {
	for _, evt := range batch {
		if evt.Severity != severityCritical {
			continue
		}
		o.doEscalateToArchitect(
			evt.Summary,
			"critical",
			fmt.Sprintf("Auto-escalated (LLM unavailable). Topic: %s", evt.Topic),
		)
	}
}
