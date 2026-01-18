package recovery

import (
	"hash/fnv"
	"time"
)

type SignalEmitter struct {
	collector     *ProgressCollector
	repetitionDet *RepetitionDetector
}

func NewSignalEmitter(collector *ProgressCollector, repetitionDet *RepetitionDetector) *SignalEmitter {
	return &SignalEmitter{
		collector:     collector,
		repetitionDet: repetitionDet,
	}
}

func (e *SignalEmitter) EmitToolCompleted(agentID, sessionID, toolName, target string) {
	hash := HashOperation(toolName, target)

	e.collector.Signal(ProgressSignal{
		AgentID:    agentID,
		SessionID:  sessionID,
		SignalType: SignalToolCompleted,
		Operation:  toolName + ":" + target,
		Timestamp:  time.Now(),
		Hash:       hash,
	})

	e.repetitionDet.Record(agentID, Operation{
		Type:      "tool",
		Action:    toolName,
		Target:    target,
		Timestamp: time.Now(),
		Hash:      hash,
	})
}

func (e *SignalEmitter) EmitLLMResponse(agentID, sessionID string) {
	e.collector.Signal(ProgressSignal{
		AgentID:    agentID,
		SessionID:  sessionID,
		SignalType: SignalLLMResponse,
		Operation:  "llm:complete",
		Timestamp:  time.Now(),
	})
}

func (e *SignalEmitter) EmitFileModified(agentID, sessionID, filePath string) {
	hash := HashOperation("file", filePath)

	e.collector.Signal(ProgressSignal{
		AgentID:    agentID,
		SessionID:  sessionID,
		SignalType: SignalFileModified,
		Operation:  "file:" + filePath,
		Timestamp:  time.Now(),
		Hash:       hash,
	})

	e.repetitionDet.Record(agentID, Operation{
		Type:      "file",
		Action:    "modify",
		Target:    filePath,
		Timestamp: time.Now(),
		Hash:      hash,
	})
}

func (e *SignalEmitter) EmitStateTransition(agentID, sessionID, fromState, toState string) {
	e.collector.Signal(ProgressSignal{
		AgentID:    agentID,
		SessionID:  sessionID,
		SignalType: SignalStateTransition,
		Operation:  fromState + "->" + toState,
		Timestamp:  time.Now(),
		Details: map[string]any{
			"from": fromState,
			"to":   toState,
		},
	})
}

func (e *SignalEmitter) EmitAgentRequest(fromAgentID, sessionID, toAgentID string) {
	e.collector.Signal(ProgressSignal{
		AgentID:    fromAgentID,
		SessionID:  sessionID,
		SignalType: SignalAgentRequest,
		Operation:  "agent:" + toAgentID,
		Timestamp:  time.Now(),
	})

	e.repetitionDet.Record(fromAgentID, Operation{
		Type:      "agent_request",
		Action:    "dispatch",
		Target:    toAgentID,
		Timestamp: time.Now(),
		Hash:      HashOperation("agent", toAgentID),
	})
}

func HashOperation(action, target string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(action))
	h.Write([]byte(":"))
	h.Write([]byte(target))
	return h.Sum64()
}
