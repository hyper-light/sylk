// Package events provides event publishing components for the activity bus.
// Publishers are typed components that emit events for specific subsystems:
// - GuidePublisher: user prompts, routing decisions, clarification requests
// - ToolPublisher: tool calls, results, and timeouts
// - AgentPublisher: agent actions, decisions, errors, and outcomes
// - LLMPublisher: LLM requests, responses, and errors
package events

import (
	"errors"
	"fmt"
	"time"
)

// ErrNilBus is returned when a publisher attempts to publish an event but the bus is nil.
// This error indicates the publisher was created without a valid ActivityEventBus reference.
var ErrNilBus = errors.New("event bus is nil")

// checkBus returns ErrNilBus when bus is nil. Every typed publisher in this
// package routes its nil guard through this helper so the behavior is
// consistent and can be changed in one place.
func checkBus(bus ActivityPublisher) error {
	if bus == nil {
		return ErrNilBus
	}
	return nil
}

// =============================================================================
// GuidePublisher - Publishes guide/router related events
// =============================================================================

// GuidePublisher publishes events related to the guide/router component.
// It handles user prompts, routing decisions, and clarification requests.
type GuidePublisher struct {
	bus       ActivityPublisher
	sessionID string
}

// NewGuidePublisher creates a new GuidePublisher.
func NewGuidePublisher(bus ActivityPublisher, sessionID string) *GuidePublisher {
	return &GuidePublisher{
		bus:       bus,
		sessionID: sessionID,
	}
}

// PublishUserPrompt publishes a user prompt event.
func (p *GuidePublisher) PublishUserPrompt(prompt string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	event := NewActivityEvent(EventTypeUserPrompt, p.sessionID, prompt)
	event.Category = "user"
	event.Keywords = []string{"prompt", "user", "input"}

	event.Data["prompt"] = prompt

	return p.bus.PublishActivity(event)
}

// PublishRoutingDecision publishes a routing decision event.
func (p *GuidePublisher) PublishRoutingDecision(fromAgent, toAgent, reason string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("Routing from %s to %s: %s", fromAgent, toAgent, reason)

	event := NewActivityEvent(EventTypeAgentDecision, p.sessionID, content)
	event.Category = "routing"
	event.Keywords = []string{"routing", "decision", fromAgent, toAgent}

	event.Data["fromAgent"] = fromAgent
	event.Data["toAgent"] = toAgent
	event.Data["reason"] = reason

	return p.bus.PublishActivity(event)
}

// PublishClarificationRequest publishes a clarification request event.
func (p *GuidePublisher) PublishClarificationRequest(question string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	event := NewActivityEvent(EventTypeUserClarification, p.sessionID, question)
	event.Category = "clarification"
	event.Keywords = []string{"clarification", "question", "user"}

	event.Data["question"] = question

	return p.bus.PublishActivity(event)
}

// =============================================================================
// ToolPublisher - Publishes tool execution events
// =============================================================================

// ToolPublisher publishes events related to tool execution.
// It handles tool calls, results, and timeouts.
type ToolPublisher struct {
	bus       ActivityPublisher
	sessionID string
	agentID   string
}

// NewToolPublisher creates a new ToolPublisher.
func NewToolPublisher(bus ActivityPublisher, sessionID, agentID string) *ToolPublisher {
	return &ToolPublisher{
		bus:       bus,
		sessionID: sessionID,
		agentID:   agentID,
	}
}

// PublishToolCall publishes a tool call event.
func (p *ToolPublisher) PublishToolCall(toolName string, params map[string]any) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("Tool call: %s", toolName)

	event := NewActivityEvent(EventTypeToolCall, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "tool"
	event.Keywords = []string{"tool", "call", toolName}

	event.Data["tool_name"] = toolName
	event.Data["params"] = params

	return p.bus.PublishActivity(event)
}

// PublishToolResult publishes a tool result event.
func (p *ToolPublisher) PublishToolResult(toolName string, result any, success bool) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("Tool result: %s", toolName)

	event := NewActivityEvent(EventTypeToolResult, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "tool"
	event.Keywords = []string{"tool", "result", toolName}

	if success {
		event.Outcome = OutcomeSuccess
	} else {
		event.Outcome = OutcomeFailure
	}

	event.Data["tool_name"] = toolName
	event.Data["result"] = result
	event.Data["outcome"] = event.Outcome.String()

	return p.bus.PublishActivity(event)
}

// PublishToolTimeout publishes a tool timeout event.
func (p *ToolPublisher) PublishToolTimeout(toolName string, timeout time.Duration) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("Tool timeout: %s after %v", toolName, timeout)

	event := NewActivityEvent(EventTypeToolTimeout, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "tool"
	event.Keywords = []string{"tool", "timeout", toolName}
	event.Outcome = OutcomeFailure

	event.Data["tool_name"] = toolName
	event.Data["timeout_duration"] = timeout.String()
	event.Data["timeout_ms"] = timeout.Milliseconds()

	return p.bus.PublishActivity(event)
}

// =============================================================================
// AgentPublisher - Publishes agent activity events
// =============================================================================

// AgentPublisher publishes events related to agent activities.
// It handles agent actions, decisions, errors, and outcomes.
type AgentPublisher struct {
	bus       ActivityPublisher
	sessionID string
	agentID   string
}

// NewAgentPublisher creates a new AgentPublisher.
func NewAgentPublisher(bus ActivityPublisher, sessionID, agentID string) *AgentPublisher {
	return &AgentPublisher{
		bus:       bus,
		sessionID: sessionID,
		agentID:   agentID,
	}
}

// PublishAgentAction publishes an agent action event.
func (p *AgentPublisher) PublishAgentAction(action, details string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("Agent action: %s - %s", action, details)

	event := NewActivityEvent(EventTypeAgentAction, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "agent"
	event.Keywords = []string{"agent", "action", action}

	event.Data["action"] = action
	event.Data["details"] = details

	return p.bus.PublishActivity(event)
}

// PublishAgentDecision publishes an agent decision event.
func (p *AgentPublisher) PublishAgentDecision(decision, rationale string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("Agent decision: %s - %s", decision, rationale)

	event := NewActivityEvent(EventTypeAgentDecision, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "agent"
	event.Keywords = []string{"agent", "decision"}

	event.Data["decision"] = decision
	event.Data["rationale"] = rationale

	return p.bus.PublishActivity(event)
}

// PublishAgentError publishes an agent error event.
// The err parameter can be nil, in which case an empty error message is recorded.
// The context parameter provides additional context about where/why the error occurred.
func (p *AgentPublisher) PublishAgentError(err error, context string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}

	content := fmt.Sprintf("Agent error: %s - %s", errMsg, context)

	event := NewActivityEvent(EventTypeAgentError, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "agent"
	event.Keywords = []string{"agent", "error"}
	event.Outcome = OutcomeFailure

	event.Data["error_message"] = errMsg
	event.Data["context"] = context

	return p.bus.PublishActivity(event)
}

// PublishSuccess publishes a success outcome event.
func (p *AgentPublisher) PublishSuccess(summary string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	event := NewActivityEvent(EventTypeSuccess, p.sessionID, summary)
	event.AgentID = p.agentID
	event.Category = "outcome"
	event.Keywords = []string{"success", "outcome"}
	event.Outcome = OutcomeSuccess

	event.Data["summary"] = summary

	return p.bus.PublishActivity(event)
}

// PublishFailure publishes a failure outcome event.
func (p *AgentPublisher) PublishFailure(err error, summary string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}

	content := fmt.Sprintf("Failure: %s - %s", errMsg, summary)

	event := NewActivityEvent(EventTypeFailure, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "outcome"
	event.Keywords = []string{"failure", "outcome", "error"}
	event.Outcome = OutcomeFailure

	event.Data["error"] = errMsg
	event.Data["summary"] = summary

	return p.bus.PublishActivity(event)
}

// =============================================================================
// LLMPublisher - Publishes LLM interaction events
// =============================================================================

// LLMPublisher publishes events related to LLM interactions.
// It handles LLM requests, responses, and errors.
type LLMPublisher struct {
	bus       ActivityPublisher
	sessionID string
	agentID   string
}

// NewLLMPublisher creates a new LLMPublisher.
func NewLLMPublisher(bus ActivityPublisher, sessionID, agentID string) *LLMPublisher {
	return &LLMPublisher{
		bus:       bus,
		sessionID: sessionID,
		agentID:   agentID,
	}
}

// PublishLLMRequest publishes an LLM request event.
func (p *LLMPublisher) PublishLLMRequest(model string, inputTokens int) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	content := fmt.Sprintf("LLM request: model=%s, input_tokens=%d", model, inputTokens)

	event := NewActivityEvent(EventTypeLLMRequest, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "llm"
	event.Keywords = []string{"llm", "request", model}

	event.Data["model"] = model
	event.Data["input_tokens"] = inputTokens

	return p.bus.PublishActivity(event)
}

// PublishLLMResponse publishes an LLM response event.
func (p *LLMPublisher) PublishLLMResponse(model string, inputTokens, outputTokens int, duration time.Duration) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	var tokensPerSec float64
	if duration > 0 {
		tokensPerSec = float64(outputTokens) / duration.Seconds()
	}

	content := fmt.Sprintf("LLM response: model=%s, input=%d, output=%d, duration=%v, tokens/sec=%.2f",
		model, inputTokens, outputTokens, duration, tokensPerSec)

	event := NewActivityEvent(EventTypeLLMResponse, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "llm"
	event.Keywords = []string{"llm", "response", model}
	event.Outcome = OutcomeSuccess

	event.Data["model"] = model
	event.Data["input_tokens"] = inputTokens
	event.Data["output_tokens"] = outputTokens
	event.Data["duration_ms"] = duration.Milliseconds()
	event.Data["tokens_per_sec"] = tokensPerSec

	return p.bus.PublishActivity(event)
}

// PublishLLMError publishes an LLM error event.
// Note: Uses EventTypeLLMResponse with OutcomeFailure to indicate an error.
// This design allows subscribers filtering by EventTypeLLMResponse to capture
// both successful responses and errors, while the Outcome field distinguishes them.
// The err parameter can be nil, in which case "unknown error" is recorded.
// The errContext parameter provides additional context about where/why the error occurred.
func (p *LLMPublisher) PublishLLMError(model string, err error, errContext string) error {
	if err := checkBus(p.bus); err != nil {
		return err
	}

	var errMsg string
	if err != nil {
		errMsg = err.Error()
	} else {
		errMsg = "unknown error"
	}

	content := fmt.Sprintf("LLM error: model=%s, error=%s, context=%s", model, errMsg, errContext)

	event := NewActivityEvent(EventTypeLLMResponse, p.sessionID, content)
	event.AgentID = p.agentID
	event.Category = "llm"
	event.Keywords = []string{"llm", "error", model}
	event.Outcome = OutcomeFailure

	event.Data["model"] = model
	event.Data["error"] = errMsg
	event.Data["error_context"] = errContext

	return p.bus.PublishActivity(event)
}
