package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/redact"
)

const (
	guideBridgeName         = "bridge.guide"
	guideBufferSize         = 256
	guidePriorityBufferSize = 256
	// Zero uses the scope's max lifetime; guide bridge is long-lived for the UI session.
	guideDrainTimeout = 0

	// tuiAgentType and tuiAgentID identify the TUI as a source agent.
	// The Guide routes responses back to TopicResponses(tuiAgentType, tuiAgentID).
	tuiAgentType = "tui"
	tuiAgentID   = "tui"
)

// GuideBridge subscribes to the TUI's response topic on the Guide EventBus
// and forwards both RouteResponse and StreamResponse messages as Bubble Tea
// messages to the program.
type GuideBridge struct {
	bus          guide.EventBus
	scope        *concurrency.GoroutineScope
	priority     chan *guide.Message
	buffer       chan *guide.Message
	started      map[string]struct{}
	metadata     map[string]map[string]any
	completed    map[string]struct{}
	dropped      atomic.Int64
	done         chan struct{}
	subscription guide.Subscription
	decisionSub  guide.Subscription
	sessionID    string
	stopOnce     sync.Once

	stateMu        sync.Mutex
	pendingRegular map[string]int
	heldTerminals  map[string][]*guide.Message
	heldOrder      []string
}

// NewGuideBridge creates a bridge that converts Guide bus response messages
// into Bubble Tea messages.
func NewGuideBridge(bus guide.EventBus, scope *concurrency.GoroutineScope, sessionID string) *GuideBridge {
	return &GuideBridge{
		bus:            bus,
		scope:          scope,
		priority:       make(chan *guide.Message, guidePriorityBufferSize),
		buffer:         make(chan *guide.Message, guideBufferSize),
		started:        make(map[string]struct{}),
		metadata:       make(map[string]map[string]any),
		completed:      make(map[string]struct{}),
		done:           make(chan struct{}),
		sessionID:      sessionID,
		pendingRegular: make(map[string]int),
		heldTerminals:  make(map[string][]*guide.Message),
	}
}

// -- Bridge implementation --

// Start subscribes to the TUI response topic and launches the drain goroutine.
func (b *GuideBridge) Start(program TeaProgram) error {
	topic := guide.TopicResponses(tuiAgentType, tuiAgentID)
	sub, err := b.bus.Subscribe(topic, b.onMessage)
	if err != nil {
		return err
	}
	b.subscription = sub
	decisionSub, err := b.bus.Subscribe("dag.decision", b.onMessage)
	if err != nil {
		_ = b.subscription.Unsubscribe()
		b.subscription = nil
		return err
	}
	b.decisionSub = decisionSub
	return b.scope.Go(guideBridgeName, guideDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the bus and signals the drain goroutine to exit.
func (b *GuideBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.subscription != nil {
			_ = b.subscription.Unsubscribe()
		}
		if b.decisionSub != nil {
			_ = b.decisionSub.Unsubscribe()
		}
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *GuideBridge) Name() string { return guideBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *GuideBridge) DroppedCount() int64 { return b.dropped.Load() }

// onMessage is the guide.MessageHandler called by the EventBus.
// It enqueues the raw message into the bounded buffer for type dispatch.
func (b *GuideBridge) onMessage(busMsg *guide.Message) error {
	if b.holdTerminalUntilRegularsDrain(busMsg) {
		return nil
	}
	target := b.buffer
	if isPriorityGuideMessage(busMsg) {
		target = b.priority
	}
	select {
	case target <- busMsg:
		if target == b.buffer {
			b.noteRegularQueued(busMsg)
		}
	default:
		b.dropped.Add(1)
		if stream, ok := busMsg.GetStreamResponse(); ok && stream.Event != nil && stream.Event.Type == guide.StreamEventComplete {
			bridgeEventDebugLog().Warn("GuideBridge: STREAM_COMPLETE_DROPPED",
				"correlation_id", busMsg.CorrelationID,
				"buffer_cap", cap(target),
				"total_dropped", b.dropped.Load())
		} else if busMsg != nil && busMsg.Type == guide.MessageTypeProposal {
			bridgeEventDebugLog().Warn("GuideBridge: APPROVAL_PROPOSAL_DROPPED",
				"correlation_id", busMsg.CorrelationID,
				"buffer_cap", cap(target),
				"total_dropped", b.dropped.Load())
		}
	}
	return nil
}

// drainFunc returns the WorkFunc that drains the buffer and sends tea messages.
func (b *GuideBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			if busMsg, ok := b.nextMessage(ctx); ok {
				b.dispatch(busMsg, program)
				continue
			}
			return nil
		}
	}
}

func (b *GuideBridge) nextMessage(ctx context.Context) (*guide.Message, bool) {
	if busMsg, ok := b.nextReadyHeldTerminal(); ok {
		return busMsg, true
	}

	select {
	case busMsg := <-b.priority:
		return busMsg, true
	default:
	}

	select {
	case busMsg := <-b.priority:
		return busMsg, true
	case busMsg := <-b.buffer:
		b.noteRegularDequeued(busMsg)
		return busMsg, true
	case <-b.done:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func (b *GuideBridge) holdTerminalUntilRegularsDrain(busMsg *guide.Message) bool {
	key := streamPhaseKeyForMessage(busMsg)
	if key == "" || !isTerminalStreamMessage(busMsg) {
		return false
	}
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if b.pendingRegular[key] == 0 {
		return false
	}
	if _, exists := b.heldTerminals[key]; !exists {
		b.heldOrder = append(b.heldOrder, key)
	}
	b.heldTerminals[key] = append(b.heldTerminals[key], busMsg)
	bridgeEventDebugLog().Info("GuideBridge: HOLD_TERMINAL_BEHIND_REGULAR",
		"correlation_id", busMsg.CorrelationID,
		"phase_key", key,
		"pending_regular", b.pendingRegular[key])
	return true
}

func (b *GuideBridge) noteRegularQueued(busMsg *guide.Message) {
	key := streamPhaseKeyForMessage(busMsg)
	if key == "" {
		return
	}
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.pendingRegular[key]++
}

func (b *GuideBridge) noteRegularDequeued(busMsg *guide.Message) {
	key := streamPhaseKeyForMessage(busMsg)
	if key == "" {
		return
	}
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	remaining := b.pendingRegular[key] - 1
	if remaining <= 0 {
		delete(b.pendingRegular, key)
		return
	}
	b.pendingRegular[key] = remaining
}

func (b *GuideBridge) nextReadyHeldTerminal() (*guide.Message, bool) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	for idx := 0; idx < len(b.heldOrder); idx++ {
		key := b.heldOrder[idx]
		if b.pendingRegular[key] > 0 {
			continue
		}
		queue := b.heldTerminals[key]
		if len(queue) == 0 {
			delete(b.heldTerminals, key)
			b.heldOrder = append(b.heldOrder[:idx], b.heldOrder[idx+1:]...)
			idx--
			continue
		}
		busMsg := queue[0]
		if len(queue) == 1 {
			delete(b.heldTerminals, key)
			b.heldOrder = append(b.heldOrder[:idx], b.heldOrder[idx+1:]...)
		} else {
			b.heldTerminals[key] = queue[1:]
		}
		bridgeEventDebugLog().Info("GuideBridge: RELEASE_HELD_TERMINAL",
			"correlation_id", strings.TrimSpace(busMsg.CorrelationID),
			"phase_key", key)
		return busMsg, true
	}
	return nil, false
}

func isPriorityGuideMessage(busMsg *guide.Message) bool {
	if busMsg == nil {
		return false
	}
	switch busMsg.Type {
	case guide.MessageTypeProposal, guide.MessageTypeLayerDecision, guide.MessageTypeResponse, guide.MessageTypeError:
		return true
	case guide.MessageTypeStream:
		stream, ok := busMsg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil {
			return false
		}
		switch stream.Event.Type {
		case guide.StreamEventComplete, guide.StreamEventError:
			return true
		case guide.StreamEventStart:
			return true
		case guide.StreamEventProgress:
			return streamHasPriorityInterAgentBranch(stream)
		case guide.StreamEventToolCall:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func streamPhaseKeyForMessage(busMsg *guide.Message) string {
	if busMsg == nil {
		return ""
	}
	stream, ok := busMsg.GetStreamResponse()
	if !ok || stream == nil {
		return ""
	}
	return streamPhaseKey(busMsg.CorrelationID, stream)
}

func isTerminalStreamMessage(busMsg *guide.Message) bool {
	if busMsg == nil {
		return false
	}
	stream, ok := busMsg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return false
	}
	switch stream.Event.Type {
	case guide.StreamEventComplete, guide.StreamEventError:
		return true
	default:
		return false
	}
}

func streamHasPriorityInterAgentBranch(stream *guide.StreamResponse) bool {
	return parseInterAgentBranchRef(stream) != nil
}

func streamHasPriorityInterAgentToolCall(stream *guide.StreamResponse) bool {
	if stream == nil || stream.Event == nil {
		return false
	}
	if streamHasPriorityInterAgentBranch(stream) {
		return true
	}
	data, ok := stream.Event.Data.(map[string]any)
	if !ok || len(data) == 0 {
		return false
	}
	meta, ok := data["inter_agent"].(map[string]any)
	if !ok || len(meta) == 0 {
		return false
	}
	kind, _ := meta["kind"].(string)
	switch strings.TrimSpace(kind) {
	case "consult", "challenge", "approval", "store":
		return true
	default:
		return false
	}
}

// dispatch converts a bus message into the appropriate Bubble Tea message(s).
func (b *GuideBridge) dispatch(busMsg *guide.Message, program TeaProgram) {
	if busMsg == nil {
		return
	}
	if proposal, ok := decodeCommandApprovalProposal(busMsg); ok {
		program.Send(msg.CommandApprovalRequestMsg{Proposal: proposal})
		return
	}
	if decision, ok := decodeLayerDecisionRequest(busMsg); ok {
		program.Send(*decision)
		return
	}
	if resp, ok := busMsg.GetRouteResponse(); ok {
		program.Send(toGuideMsg(resp, busMsg.Metadata))
		return
	}
	if stream, ok := busMsg.GetStreamResponse(); ok {
		stream = streamWithEnvelopeMetadata(stream, busMsg.Metadata)
		stream = b.streamWithRememberedMetadata(busMsg.CorrelationID, stream)
		b.rememberStreamMetadata(busMsg.CorrelationID, stream)
		b.dispatchStream(stream, program)
		return
	}
	if errText, ok := busMsg.GetError(); ok {
		program.Send(msg.StreamErrorMsg{
			SessionID:     b.sessionID,
			CorrelationID: busMsg.CorrelationID,
			Err:           guideError(redact.Text(errText)),
			BranchRef:     parseInterAgentBranchRefFromMetadata(busMsg.Metadata),
		})
	}
}

func decodeCommandApprovalProposal(busMsg *guide.Message) (*commandapproval.Proposal, bool) {
	if busMsg == nil || busMsg.Type != guide.MessageTypeProposal || busMsg.Payload == nil {
		return nil, false
	}
	if typed, ok := busMsg.Payload.(*commandapproval.Proposal); ok && typed != nil {
		return normalizeDecodedCommandApprovalProposal(*typed, nil, busMsg)
	}
	if typed, ok := busMsg.Payload.(commandapproval.Proposal); ok {
		return normalizeDecodedCommandApprovalProposal(typed, nil, busMsg)
	}
	if typed, ok := busMsg.Payload.(*fetch.FetchProposal); ok && typed != nil {
		return proposalFromFetchApproval(*typed, busMsg)
	}
	if typed, ok := busMsg.Payload.(fetch.FetchProposal); ok {
		return proposalFromFetchApproval(typed, busMsg)
	}
	raw, err := json.Marshal(busMsg.Payload)
	if err != nil {
		return nil, false
	}
	var proposal commandapproval.Proposal
	_ = json.Unmarshal(raw, &proposal)
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if normalized, ok := normalizeDecodedCommandApprovalProposal(proposal, payload, busMsg); ok {
		return normalized, true
	}
	var fetchProposal fetch.FetchProposal
	if err := json.Unmarshal(raw, &fetchProposal); err == nil {
		return proposalFromFetchApproval(fetchProposal, busMsg)
	}
	return nil, false
}

func normalizeDecodedCommandApprovalProposal(
	proposal commandapproval.Proposal,
	payload map[string]any,
	busMsg *guide.Message,
) (*commandapproval.Proposal, bool) {
	if payload != nil {
		hasFetchURL := strings.TrimSpace(stringMapValue(payload, "url")) != ""
		proposal.Command = firstNonEmptyBridgeValue(proposal.Command, stringMapValue(payload, "command"), stringMapValue(payload, "url"))
		proposal.ToolName = firstNonEmptyBridgeValue(proposal.ToolName, stringMapValue(payload, "tool_name"))
		if hasFetchURL && strings.TrimSpace(proposal.ToolName) == "" {
			proposal.ToolName = "web_fetch"
		}
		proposal.Domain = firstNonEmptyBridgeValue(proposal.Domain, stringMapValue(payload, "domain"))
		proposal.Justification = firstNonEmptyBridgeValue(proposal.Justification, stringMapValue(payload, "justification"), stringMapValue(payload, "reason"))
		proposal.AgentID = firstNonEmptyBridgeValue(proposal.AgentID, stringMapValue(payload, "agent_id"))
		proposal.AgentType = firstNonEmptyBridgeValue(proposal.AgentType, stringMapValue(payload, "agent_type"), stringMapValue(payload, "source_agent"))
		proposal.Summary = firstNonEmptyBridgeValue(proposal.Summary, stringMapValue(payload, "summary"))
		proposal.Risk = firstNonEmptyBridgeValue(proposal.Risk, stringMapValue(payload, "risk"), stringMapValue(payload, "risk_assessment"))
	}
	proposal.CorrelationID = firstNonEmptyBridgeValue(proposal.CorrelationID, correlationIDFromBusMessage(busMsg))
	proposal.TargetAgentID = firstNonEmptyBridgeValue(proposal.TargetAgentID, sourceAgentIDFromBusMessage(busMsg))
	if proposal.Timestamp.IsZero() && busMsg != nil && !busMsg.Timestamp.IsZero() {
		proposal.Timestamp = busMsg.Timestamp
	}
	if proposal.IsFetchApproval() && proposal.ApprovalPolicy == "" {
		proposal.ApprovalPolicy = commandapproval.ApprovalPolicyExact
	}
	if strings.TrimSpace(proposal.CorrelationID) == "" || strings.TrimSpace(proposal.Command) == "" || strings.TrimSpace(proposal.TargetAgentID) == "" {
		return nil, false
	}
	return &proposal, true
}

func proposalFromFetchApproval(fetchProposal fetch.FetchProposal, busMsg *guide.Message) (*commandapproval.Proposal, bool) {
	proposal := commandapproval.Proposal{
		CorrelationID:  firstNonEmptyBridgeValue(fetchProposal.CorrelationID, correlationIDFromBusMessage(busMsg)),
		TargetAgentID:  sourceAgentIDFromBusMessage(busMsg),
		AgentType:      strings.TrimSpace(fetchProposal.SourceAgent),
		ToolName:       firstNonEmptyBridgeValue(fetchProposal.ToolName, "web_fetch"),
		Command:        strings.TrimSpace(fetchProposal.URL),
		Domain:         strings.TrimSpace(fetchProposal.Domain),
		Justification:  strings.TrimSpace(fetchProposal.Reason),
		Summary:        strings.TrimSpace(fetchProposal.Reason),
		Risk:           strings.TrimSpace(fetchProposal.RiskAssessment),
		Timestamp:      fetchProposal.Timestamp,
		ApprovalPolicy: commandapproval.ApprovalPolicyExact,
	}
	if proposal.Timestamp.IsZero() && busMsg != nil && !busMsg.Timestamp.IsZero() {
		proposal.Timestamp = busMsg.Timestamp
	}
	if strings.TrimSpace(proposal.CorrelationID) == "" || strings.TrimSpace(proposal.Command) == "" || strings.TrimSpace(proposal.TargetAgentID) == "" {
		return nil, false
	}
	return &proposal, true
}

func stringMapValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func correlationIDFromBusMessage(busMsg *guide.Message) string {
	if busMsg == nil {
		return ""
	}
	return strings.TrimSpace(busMsg.CorrelationID)
}

func sourceAgentIDFromBusMessage(busMsg *guide.Message) string {
	if busMsg == nil {
		return ""
	}
	return strings.TrimSpace(busMsg.SourceAgentID)
}

func firstNonEmptyBridgeValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func decodeLayerDecisionRequest(busMsg *guide.Message) (*msg.LayerDecisionMsg, bool) {
	if busMsg == nil || busMsg.Type != guide.MessageTypeLayerDecision || busMsg.Payload == nil {
		return nil, false
	}
	data, ok := busMsg.Payload.(map[string]any)
	if !ok {
		raw, err := json.Marshal(busMsg.Payload)
		if err != nil {
			return nil, false
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, false
		}
	}
	dagID, _ := data["dag_id"].(string)
	layerIdx, _ := toInt(data["layer_idx"])
	if strings.TrimSpace(dagID) == "" {
		return nil, false
	}
	result := &msg.LayerDecisionMsg{
		DAGID:    dagID,
		LayerIdx: layerIdx,
	}
	if rawNodes, ok := data["failed_nodes"].([]any); ok {
		result.FailedNodes = append(result.FailedNodes, decodeLayerFailedNodes(rawNodes)...)
	}
	return result, true
}

func decodeLayerFailedNodes(raw []any) []msg.LayerFailedNode {
	nodes := make([]msg.LayerFailedNode, 0, len(raw))
	for _, entry := range raw {
		nodeMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		node := msg.LayerFailedNode{}
		node.NodeID, _ = nodeMap["NodeID"].(string)
		if node.NodeID == "" {
			node.NodeID, _ = nodeMap["node_id"].(string)
		}
		node.NodeName, _ = nodeMap["NodeName"].(string)
		if node.NodeName == "" {
			node.NodeName, _ = nodeMap["node_name"].(string)
		}
		node.AgentType, _ = nodeMap["AgentType"].(string)
		if node.AgentType == "" {
			node.AgentType, _ = nodeMap["agent_type"].(string)
		}
		node.Error, _ = nodeMap["Error"].(string)
		if node.Error == "" {
			node.Error, _ = nodeMap["error"].(string)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func toInt(v any) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

// dispatchStream converts a StreamResponse into the matching stream tea message.
func (b *GuideBridge) dispatchStream(stream *guide.StreamResponse, program TeaProgram) {
	if stream.Event == nil {
		return
	}
	sid := b.sessionID
	cid := stream.CorrelationID

	switch stream.Event.Type {
	case guide.StreamEventStart:
		b.clearStreamCompleted(cid, stream)
		if !b.markStreamStarted(cid, stream) {
			bridgeEventDebugLog().Info("GuideBridge: STREAM_START_SUPPRESSED",
				"correlation_id", cid,
				"agent_id", stream.RespondingAgentID)
			return
		}
		program.Send(parseStreamStartMsg(sid, cid, stream))
	case guide.StreamEventData:
		if !b.ensureStreamStarted(sid, cid, stream, program) {
			return
		}
		if planMsg, ok := tryPlanUpdateMsg(stream.Event); ok {
			planMsg.CorrelationID = cid
			program.Send(planMsg)
			return
		}
		text := streamDataText(stream.Event)
		earlyUsage := streamEventEarlyInputTokens(stream.Event)
		if text != "" || earlyUsage > 0 {
			chunk := msg.StreamChunkMsg{SessionID: sid, CorrelationID: cid, Text: redact.Text(text), InputTokens: earlyUsage}
			program.Send(chunk)
		}
	case guide.StreamEventProgress:
		if !b.ensureStreamStarted(sid, cid, stream, program) {
			return
		}
		program.Send(toStreamProgressMsg(sid, cid, stream))
	case guide.StreamEventComplete:
		if !b.ensureStreamStarted(sid, cid, stream, program) {
			return
		}
		bridgeEventDebugLog().Info("GuideBridge: STREAM_COMPLETE_DISPATCH",
			"correlation_id", cid,
			"agent_id", stream.RespondingAgentID,
			"has_directive", stream.Event.Directive != nil,
			"text_len", len(stream.Event.Text))
		complete := parseStreamCompleteMsg(sid, cid, stream)
		if text := streamCompleteText(stream.RespondingAgentID, stream.Event); text != "" {
			complete.AuthoritativeText = redact.Text(text)
		}
		if stream.Event.Usage != nil {
			complete.InputTokens = stream.Event.Usage.InputTokens
			complete.OutputTokens = stream.Event.Usage.OutputTokens
		}
		program.Send(complete)
		b.clearStreamStarted(cid, stream)
		b.markStreamCompleted(cid, stream)
		bridgeEventDebugLog().Info("GuideBridge: STREAM_COMPLETE_SENT",
			"correlation_id", cid)
	case guide.StreamEventError:
		if !b.ensureStreamStarted(sid, cid, stream, program) {
			return
		}
		program.Send(msg.StreamErrorMsg{
			SessionID:     sid,
			CorrelationID: cid,
			Err:           redact.Error(extractStreamError(stream.Event)),
			BranchRef:     parseInterAgentBranchRef(stream),
		})
		b.clearStreamStarted(cid, stream)
		b.markStreamCompleted(cid, stream)
	case guide.StreamEventRetry:
		if !b.ensureStreamStarted(sid, cid, stream, program) {
			return
		}
		status, _ := stream.Event.Data.(guide.RetryStatus)
		errText := ""
		if status.Err != nil {
			errText = providers.FriendlyErrorMessage(status.Err)
		}
		program.Send(msg.RetryStatusMsg{
			SessionID:     sid,
			CorrelationID: cid,
			Attempt:       status.Attempt,
			MaxAttempts:   status.MaxAttempts,
			Delay:         status.Delay,
			Error:         errText,
		})
	case guide.StreamEventReroute:
		program.Send(parseStreamRerouteMsg(sid, cid, stream.Event))
	case guide.StreamEventToolCall:
		if !b.ensureStreamStarted(sid, cid, stream, program) {
			return
		}
		program.Send(parseToolCallEventMsg(sid, cid, stream))
	}
}

func streamPhaseAgentID(stream *guide.StreamResponse) string {
	if stream == nil {
		return ""
	}
	if responderID := strings.TrimSpace(stream.RespondingAgentID); responderID != "" {
		return responderID
	}
	if runtimeID := streamRuntimeAgentID(stream); runtimeID != "" {
		return runtimeID
	}
	return streamMetadataString(stream, "agent_type")
}

func streamPhaseKey(correlationID string, stream *guide.StreamResponse) string {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return ""
	}
	phaseAgentID := strings.TrimSpace(streamPhaseAgentID(stream))
	if phaseAgentID == "" {
		return correlationID
	}
	return correlationID + "\x00" + phaseAgentID
}

func (b *GuideBridge) markStreamStarted(correlationID string, stream *guide.StreamResponse) bool {
	key := streamPhaseKey(correlationID, stream)
	if key == "" {
		return false
	}
	if _, exists := b.started[key]; exists {
		return false
	}
	b.started[key] = struct{}{}
	return true
}

func (b *GuideBridge) clearStreamStarted(correlationID string, stream *guide.StreamResponse) {
	key := streamPhaseKey(correlationID, stream)
	if key == "" {
		return
	}
	delete(b.started, key)
	delete(b.metadata, key)
}

func (b *GuideBridge) markStreamCompleted(correlationID string, stream *guide.StreamResponse) {
	key := streamPhaseKey(correlationID, stream)
	if key == "" {
		return
	}
	b.completed[key] = struct{}{}
}

func (b *GuideBridge) clearStreamCompleted(correlationID string, stream *guide.StreamResponse) {
	key := streamPhaseKey(correlationID, stream)
	if key == "" {
		return
	}
	delete(b.completed, key)
}

func (b *GuideBridge) isStreamCompleted(correlationID string, stream *guide.StreamResponse) bool {
	key := streamPhaseKey(correlationID, stream)
	if key == "" {
		return false
	}
	_, completed := b.completed[key]
	return completed
}

func (b *GuideBridge) ensureStreamStarted(sessionID, correlationID string, stream *guide.StreamResponse, program TeaProgram) bool {
	if b.isStreamCompleted(correlationID, stream) {
		bridgeEventDebugLog().Info("GuideBridge: STALE_STREAM_EVENT_DROPPED",
			"correlation_id", correlationID,
			"event_type", stream.Event.Type,
			"agent_id", stream.RespondingAgentID)
		return false
	}
	if !b.markStreamStarted(correlationID, stream) {
		return true
	}
	bridgeEventDebugLog().Info("GuideBridge: SYNTHETIC_STREAM_START",
		"correlation_id", correlationID,
		"event_type", stream.Event.Type,
		"agent_id", stream.RespondingAgentID,
		"parent_correlation_id", streamParentCorrelationID(stream),
		"task_id", streamMetadataString(stream, "task_id"))
	program.Send(parseStreamStartMsg(sessionID, correlationID, stream))
	return true
}

func parseStreamStartMsg(sessionID, correlationID string, stream *guide.StreamResponse) msg.StreamStartMsg {
	result := msg.StreamStartMsg{
		SessionID:     sessionID,
		CorrelationID: correlationID,
	}
	if stream == nil {
		return result
	}
	result.AgentID = strings.TrimSpace(stream.RespondingAgentID)
	result.RuntimeAgentID = streamRuntimeAgentID(stream)
	result.ParentCorrelationID = streamParentCorrelationID(stream)
	result.TopLevelTransfer = streamTopLevelTransfer(stream)
	result.AgentName = streamAgentName(stream)
	result.AgentType = streamMetadataString(stream, "agent_type")
	result.PipelineID = streamMetadataString(stream, "pipeline_id")
	result.TaskID = streamMetadataString(stream, "task_id")
	result.TaskName = streamMetadataString(stream, "task_name")
	result.TaskSlug = streamMetadataString(stream, "task_slug")
	result.BranchRef = parseInterAgentBranchRef(stream)
	if stream.Event != nil {
		result.Visibility = stream.Event.Visibility
	}
	return result
}

func parseStreamRerouteMsg(sessionID, correlationID string, event *guide.StreamEvent) msg.StreamRerouteMsg {
	result := msg.StreamRerouteMsg{SessionID: sessionID, CorrelationID: correlationID}
	if event == nil || event.Data == nil {
		return result
	}
	data, ok := event.Data.(map[string]string)
	if !ok {
		return result
	}
	result.FromAgentID = strings.TrimSpace(data["from_agent"])
	result.ToAgentID = strings.TrimSpace(data["to_agent"])
	result.Reason = strings.TrimSpace(data["reason"])
	result.OriginalCorrelationID = strings.TrimSpace(data["original_correlation_id"])
	if newCID := strings.TrimSpace(data["new_correlation_id"]); newCID != "" {
		result.CorrelationID = newCID
	}
	return result
}

func parseToolCallEventMsg(sessionID, correlationID string, stream *guide.StreamResponse) msg.ToolCallEventMsg {
	result := msg.ToolCallEventMsg{
		SessionID:     sessionID,
		CorrelationID: correlationID,
	}
	if stream == nil {
		return result
	}
	result.AgentID = strings.TrimSpace(stream.RespondingAgentID)
	result.ParentCorrelationID = streamParentCorrelationID(stream)
	result.TopLevelTransfer = streamTopLevelTransfer(stream)
	result.AgentName = streamAgentName(stream)
	result.AgentType = streamMetadataString(stream, "agent_type")
	result.PipelineID = streamMetadataString(stream, "pipeline_id")
	result.TaskID = streamMetadataString(stream, "task_id")
	result.TaskName = streamMetadataString(stream, "task_name")
	result.TaskSlug = streamMetadataString(stream, "task_slug")
	result.BranchRef = parseInterAgentBranchRef(stream)
	event := stream.Event
	if event == nil || event.Data == nil {
		return result
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return result
	}
	if v, ok := data["tool_name"].(string); ok {
		result.ToolName = v
	}
	if v, ok := data["tool_call_key"].(string); ok {
		result.ToolCallKey = strings.TrimSpace(v)
	}
	if v, ok := data["args_summary"].(string); ok {
		result.ArgsSummary = v
	}
	if v, ok := data["full_args"].(string); ok {
		result.FullArgs = v
	}
	if v, ok := data["output"].(string); ok {
		result.Output = v
	}
	if v, ok := data["error_msg"].(string); ok {
		result.ErrorMsg = v
	}
	if v, ok := data["phase"].(float64); ok {
		result.Phase = int(v)
	}
	if v, ok := data["phase"].(int); ok {
		result.Phase = v
	}
	if v, ok := data["success"].(bool); ok {
		result.Success = v
	}
	if v, ok := data["started_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			result.StartedAt = t
		}
	}
	if v, ok := data["duration"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			result.Duration = d
		}
	}
	if meta, ok := data["inter_agent"].(map[string]any); ok {
		parsed := &msg.InterAgentToolEventMsg{}
		if v, ok := meta["kind"].(string); ok {
			parsed.Kind = strings.TrimSpace(v)
		}
		if v, ok := meta["summary"].(string); ok {
			parsed.Summary = strings.TrimSpace(v)
		}
		if v, ok := meta["thread_key"].(string); ok {
			parsed.ThreadKey = strings.TrimSpace(v)
		}
		if v, ok := meta["status"].(string); ok {
			parsed.Status = strings.TrimSpace(v)
		}
		if v, ok := meta["update_origin"].(bool); ok {
			parsed.UpdateOrigin = v
		}
		switch typed := meta["agent_types"].(type) {
		case []string:
			parsed.AgentTypes = append(parsed.AgentTypes, typed...)
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					parsed.AgentTypes = append(parsed.AgentTypes, strings.TrimSpace(text))
				}
			}
		}
		if parsed.Kind != "" {
			result.InterAgent = parsed
		}
	}
	return result
}

func streamEventEarlyInputTokens(event *guide.StreamEvent) int {
	if event == nil || event.Usage == nil {
		return 0
	}
	return event.Usage.InputTokens
}

func streamDataText(event *guide.StreamEvent) string {
	if event == nil {
		return ""
	}
	if strings.TrimSpace(event.Text) != "" {
		return event.Text
	}
	switch typed := event.Data.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
		return ""
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			if strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func toStreamProgressMsg(sessionID, correlationID string, stream *guide.StreamResponse) msg.StreamProgressMsg {
	var event *guide.StreamEvent
	agentID := ""
	agentName := ""
	if stream != nil {
		event = stream.Event
		agentID = strings.TrimSpace(stream.RespondingAgentID)
		agentName = streamAgentName(stream)
	}
	progress := parseProgressData(event)
	m := msg.StreamProgressMsg{
		SessionID:           sessionID,
		CorrelationID:       correlationID,
		ParentCorrelationID: streamParentCorrelationID(stream),
		TopLevelTransfer:    streamTopLevelTransfer(stream),
		AgentID:             agentID,
		RuntimeAgentID:      streamRuntimeAgentID(stream),
		AgentName:           agentName,
		AgentType:           streamMetadataString(stream, "agent_type"),
		PipelineID:          streamMetadataString(stream, "pipeline_id"),
		TaskID:              streamMetadataString(stream, "task_id"),
		TaskName:            streamMetadataString(stream, "task_name"),
		TaskSlug:            streamMetadataString(stream, "task_slug"),
		Current:             progress.Current,
		Total:               progress.Total,
		Message:             redact.Text(strings.TrimSpace(progress.Message)),
		ToolDerived:         progress.ToolDerived,
		UIState:             events.NormalizeAgentUIState(progress.UIState),
		BranchRef:           parseInterAgentBranchRef(stream),
	}
	if event != nil {
		m.Visibility = event.Visibility
	}
	return m
}

func parseStreamCompleteMsg(sessionID, correlationID string, stream *guide.StreamResponse) msg.StreamCompleteMsg {
	result := msg.StreamCompleteMsg{
		SessionID:     sessionID,
		CorrelationID: correlationID,
	}
	if stream == nil {
		return result
	}
	result.AgentID = strings.TrimSpace(stream.RespondingAgentID)
	result.RuntimeAgentID = streamRuntimeAgentID(stream)
	result.ParentCorrelationID = streamParentCorrelationID(stream)
	result.TopLevelTransfer = streamTopLevelTransfer(stream)
	result.AgentName = streamAgentName(stream)
	result.AgentType = streamMetadataString(stream, "agent_type")
	result.PipelineID = streamMetadataString(stream, "pipeline_id")
	result.TaskID = streamMetadataString(stream, "task_id")
	result.TaskName = streamMetadataString(stream, "task_name")
	result.TaskSlug = streamMetadataString(stream, "task_slug")
	result.BranchRef = parseInterAgentBranchRef(stream)
	if stream.Event != nil {
		result.Result = stream.Event.Data
		result.Visibility = stream.Event.Visibility
	}
	return result
}

const (
	streamMetadataNestedBranch      = "chat_nested_branch"
	streamMetadataTopLevelTransfer  = "chat_top_level_transfer"
	streamMetadataParentCorrelation = "chat_parent_correlation_id"
	streamMetadataParentToolCallKey = "chat_parent_tool_call_key"
	streamMetadataInterAgentThread  = "chat_inter_agent_thread_key"
	streamMetadataInterAgentKind    = "chat_inter_agent_kind"
)

func streamMetadataString(stream *guide.StreamResponse, key string) string {
	metadata := effectiveStreamMetadata(stream)
	if len(metadata) == 0 {
		return ""
	}
	return metadataString(metadata, key)
}

func streamAgentName(stream *guide.StreamResponse) string {
	if stream == nil {
		return ""
	}
	return firstNonEmpty(
		strings.TrimSpace(stream.RespondingAgentName),
		streamMetadataString(stream, "agent_name"),
	)
}

func streamParentCorrelationID(stream *guide.StreamResponse) string {
	return streamMetadataString(stream, streamMetadataParentCorrelation)
}

func streamTopLevelTransfer(stream *guide.StreamResponse) bool {
	return metadataBool(effectiveStreamMetadata(stream), streamMetadataTopLevelTransfer)
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func parseInterAgentBranchRef(stream *guide.StreamResponse) *msg.InterAgentBranchRefMsg {
	return parseInterAgentBranchRefFromMetadata(effectiveStreamMetadata(stream))
}

func streamWithEnvelopeMetadata(stream *guide.StreamResponse, envelope map[string]any) *guide.StreamResponse {
	if stream == nil || len(envelope) == 0 {
		return stream
	}
	merged := mergeStreamMetadata(envelope, stream.Metadata)
	cloned := *stream
	cloned.Metadata = merged
	return &cloned
}

func effectiveStreamMetadata(stream *guide.StreamResponse) map[string]any {
	if stream == nil {
		return nil
	}
	return mergeStreamMetadata(stream.Metadata, streamEventMetadata(stream.Event))
}

func streamEventMetadata(event *guide.StreamEvent) map[string]any {
	if event == nil || event.Data == nil {
		return nil
	}
	data, ok := event.Data.(map[string]any)
	if !ok || len(data) == 0 {
		return nil
	}
	for _, key := range []string{"stream_metadata", "metadata"} {
		raw, ok := data[key]
		if !ok {
			continue
		}
		if nested, ok := raw.(map[string]any); ok && len(nested) > 0 {
			return nested
		}
	}
	return nil
}

func cloneMetadataMap(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func (b *GuideBridge) streamWithRememberedMetadata(correlationID string, stream *guide.StreamResponse) *guide.StreamResponse {
	key := streamPhaseKey(correlationID, stream)
	if key == "" || stream == nil {
		return stream
	}
	merged := mergeStreamMetadata(b.metadata[key], effectiveStreamMetadata(stream))
	if len(merged) == 0 {
		return stream
	}
	cloned := *stream
	cloned.Metadata = merged
	return &cloned
}

func (b *GuideBridge) rememberStreamMetadata(correlationID string, stream *guide.StreamResponse) {
	key := streamPhaseKey(correlationID, stream)
	if key == "" || stream == nil {
		return
	}
	metadata := effectiveStreamMetadata(stream)
	if len(metadata) == 0 {
		return
	}
	b.metadata[key] = mergeStreamMetadata(b.metadata[key], metadata)
}

func mergeStreamMetadata(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := cloneMetadataMap(base)
	if merged == nil {
		merged = make(map[string]any, len(overlay))
	}
	for key, value := range overlay {
		if !streamMetadataValueCarriesIdentity(value) {
			if _, exists := merged[key]; exists {
				continue
			}
			continue
		}
		merged[key] = value
	}
	merged = normalizeExclusiveChatTransferMetadata(merged)
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func normalizeExclusiveChatTransferMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return metadata
	}
	if metadataBool(metadata, streamMetadataTopLevelTransfer) {
		delete(metadata, streamMetadataNestedBranch)
		delete(metadata, streamMetadataParentToolCallKey)
		delete(metadata, streamMetadataInterAgentThread)
		delete(metadata, streamMetadataInterAgentKind)
		return metadata
	}
	if metadataBool(metadata, streamMetadataNestedBranch) {
		delete(metadata, streamMetadataTopLevelTransfer)
	}
	return metadata
}

func streamMetadataValueCarriesIdentity(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case bool:
		return typed
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func parseInterAgentBranchRefFromMetadata(metadata map[string]any) *msg.InterAgentBranchRefMsg {
	if len(metadata) == 0 {
		return nil
	}
	if metadataBool(metadata, streamMetadataTopLevelTransfer) {
		return nil
	}
	nested := metadataBool(metadata, streamMetadataNestedBranch)
	if !nested {
		return nil
	}
	parentCorrelationID := metadataString(metadata, streamMetadataParentCorrelation)
	if strings.TrimSpace(parentCorrelationID) == "" {
		return nil
	}
	parentToolCallKey := metadataString(metadata, streamMetadataParentToolCallKey)
	threadKey := metadataString(metadata, streamMetadataInterAgentThread)
	kind := metadataString(metadata, streamMetadataInterAgentKind)
	switch strings.TrimSpace(kind) {
	case "consult", "challenge", "approval", "store":
	default:
		return nil
	}
	return &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: strings.TrimSpace(parentCorrelationID),
		ParentToolCallKey:   strings.TrimSpace(parentToolCallKey),
		ThreadKey:           strings.TrimSpace(threadKey),
		Kind:                strings.TrimSpace(kind),
	}
}

func metadataBool(metadata map[string]any, key string) bool {
	if len(metadata) == 0 {
		return false
	}
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.TrimSpace(strings.ToLower(typed)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func parseProgressData(event *guide.StreamEvent) guide.ProgressData {
	if event == nil {
		return guide.ProgressData{}
	}
	if progress, ok := event.Data.(*guide.ProgressData); ok && progress != nil {
		return *progress
	}
	if progress, ok := event.Data.(guide.ProgressData); ok {
		return progress
	}
	if data, ok := event.Data.(map[string]any); ok {
		return guide.ProgressData{
			Current:     parseProgressInt(data["current"]),
			Total:       parseProgressInt(data["total"]),
			Message:     parseProgressString(data["message"]),
			UIState:     events.AgentUIStateFromData(data),
			ToolDerived: parseProgressBool(data["tool_derived"]),
		}
	}
	return guide.ProgressData{}
}

func parseProgressInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func parseProgressString(value any) string {
	text, _ := value.(string)
	return text
}

func parseProgressBool(value any) bool {
	flag, _ := value.(bool)
	return flag
}

// streamCompleteText extracts the user-facing text from a StreamEventComplete.
// Prefers the explicit Text field (set by the architect for plan readiness
// responses), falling back to structured payload extraction from Data.
func streamCompleteText(agentID string, event *guide.StreamEvent) string {
	if event == nil {
		return ""
	}
	if text := strings.TrimSpace(event.Text); text != "" {
		return text
	}
	return streamCompleteContent(agentID, event.Data)
}

func streamCompleteContent(agentID string, payload any) string {
	data := normalizeStructuredPayload(payload)
	renderable, controlOnly := extractTurnEnvelopeResult(data)
	if text, ok := formatStructuredPayload(agentID, renderable); ok {
		return text
	}
	if controlOnly {
		return ""
	}
	switch typed := renderable.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	}
	encoded, err := json.MarshalIndent(renderable, "", "  ")
	if err != nil {
		return fmt.Sprint(renderable)
	}
	return string(encoded)
}

// extractStreamError pulls an error from a StreamEvent.
func extractStreamError(event *guide.StreamEvent) error {
	if e, ok := event.Data.(error); ok {
		return e
	}
	return guideError("stream error")
}

// toGuideMsg converts a RouteResponse into a GuideResponseMsg.
func toGuideMsg(resp *guide.RouteResponse, metadata map[string]any) msg.GuideResponseMsg {
	m := msg.GuideResponseMsg{
		CorrelationID: resp.CorrelationID,
		AgentID:       resp.RespondingAgentID,
		AgentName:     firstNonEmpty(resp.RespondingAgentName, metadataString(metadata, "agent_name")),
		AgentType:     metadataString(metadata, "agent_type"),
		BranchRef:     parseInterAgentBranchRefFromMetadata(metadata),
	}
	if resp.Success {
		m.Content = redact.Text(routeResponseContent(resp))
		return m
	}
	if resp.Error != "" {
		m.Err = guideError(redact.Text(resp.Error))
	}
	return m
}

func routeResponseContent(resp *guide.RouteResponse) string {
	if resp == nil {
		return ""
	}
	data := normalizeStructuredPayload(resp.Data)
	renderable, controlOnly := extractTurnEnvelopeResult(data)
	if text, ok := formatStructuredPayload(resp.RespondingAgentID, renderable); ok {
		return text
	}
	if controlOnly {
		return ""
	}
	switch typed := renderable.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case []*guide.AgentRegistration:
		return formatAgentRegistryNames(typed)
	case []guide.AgentRegistration:
		return formatAgentRegistryValues(typed)
	case map[string]any:
		if text, ok := humanizeGuideMap(typed); ok {
			return text
		}
	}
	encoded, err := json.MarshalIndent(renderable, "", "  ")
	if err != nil {
		return fmt.Sprint(renderable)
	}
	return string(encoded)
}

func humanizeGuideMap(values map[string]any) (string, bool) {
	if pending, ok := values["pending"]; ok {
		return fmt.Sprintf("Guide pending requests: %v.", pending), true
	}
	if skills, ok := values["skills"]; ok {
		switch typed := skills.(type) {
		case []any:
			return fmt.Sprintf("Guide has %d available skills.", len(typed)), true
		default:
			return "Guide skills are available.", true
		}
	}
	return "", false
}

func formatAgentRegistryNames(agents []*guide.AgentRegistration) string {
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		if agent == nil || strings.TrimSpace(agent.ID) == "" {
			continue
		}
		names = append(names, strings.TrimSpace(agent.ID))
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "No agents are currently registered."
	}
	return fmt.Sprintf("Registered agents (%d): %s", len(names), strings.Join(names, ", "))
}

func formatAgentRegistryValues(agents []guide.AgentRegistration) string {
	names := make([]string, 0, len(agents))
	for idx := range agents {
		id := strings.TrimSpace(agents[idx].ID)
		if id == "" {
			continue
		}
		names = append(names, id)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "No agents are currently registered."
	}
	return fmt.Sprintf("Registered agents (%d): %s", len(names), strings.Join(names, ", "))
}

// tryPlanUpdateMsg checks if a StreamEvent contains a plan snapshot and
// converts it to a PlanUpdateMsg. Returns (msg, true) on success.
func tryPlanUpdateMsg(event *guide.StreamEvent) (msg.PlanUpdateMsg, bool) {
	if event == nil || event.Data == nil {
		return msg.PlanUpdateMsg{}, false
	}
	data, ok := toMap(event.Data)
	if !ok || !looksLikePlanSnapshot(data) {
		return msg.PlanUpdateMsg{}, false
	}
	return parsePlanUpdateMsg(data), true
}

func looksLikePlanSnapshot(values map[string]any) bool {
	_, hasPlanID := values["PlanID"]
	_, hasTasks := values["Tasks"]
	return hasPlanID && hasTasks
}

func parsePlanUpdateMsg(data map[string]any) msg.PlanUpdateMsg {
	result := msg.PlanUpdateMsg{
		PlanID:         stringFromKey(data, "PlanID"),
		Status:         stringFromKey(data, "Status"),
		TotalTokensIn:  intFromKey(data, "TotalTokensIn"),
		TotalTokensOut: intFromKey(data, "TotalTokensOut"),
	}

	if t, ok := data["StartTime"].(time.Time); ok {
		result.StartTime = t
	}

	// Parse tasks.
	rawTasks := sliceFromKey(data, "Tasks")
	result.Tasks = make([]msg.PlanTaskSnapshot, 0, len(rawTasks))
	for _, raw := range rawTasks {
		taskMap, ok := toMap(raw)
		if !ok {
			continue
		}
		result.Tasks = append(result.Tasks, parsePlanTaskSnapshot(taskMap))
	}

	// Parse execution layers.
	rawLayers := sliceFromKey(data, "ExecutionLayers")
	result.ExecutionLayers = make([][]string, 0, len(rawLayers))
	for _, rawLayer := range rawLayers {
		layerSlice, ok := rawLayer.([]any)
		if !ok {
			continue
		}
		ids := make([]string, 0, len(layerSlice))
		for _, item := range layerSlice {
			if s, ok := item.(string); ok {
				ids = append(ids, s)
			}
		}
		result.ExecutionLayers = append(result.ExecutionLayers, ids)
	}

	return result
}

func parsePlanTaskSnapshot(data map[string]any) msg.PlanTaskSnapshot {
	snap := msg.PlanTaskSnapshot{
		ID:                  stringFromKey(data, "ID"),
		Name:                stringFromKey(data, "Name"),
		Description:         stringFromKey(data, "Description"),
		AgentType:           stringFromKey(data, "AgentType"),
		Status:              stringFromKey(data, "Status"),
		TokensIn:            intFromKey(data, "TokensIn"),
		TokensOut:           intFromKey(data, "TokensOut"),
		StatusMessage:       stringFromKey(data, "StatusMessage"),
		ImplementationGuide: stringFromKey(data, "ImplementationGuide"),
		Guidelines:          stringSliceFromKey(data, "Guidelines"),
	}

	// Parse dependencies.
	rawDeps := sliceFromKey(data, "Dependencies")
	if len(rawDeps) > 0 {
		snap.Dependencies = make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok {
				snap.Dependencies = append(snap.Dependencies, s)
			}
		}
	}

	// Parse duration string if present.
	if durStr := stringFromKey(data, "Duration"); durStr != "" {
		if d, err := time.ParseDuration(durStr); err == nil {
			snap.Duration = d
		}
	}

	// Parse acceptance criteria.
	rawCriteria := sliceFromKey(data, "AcceptanceCriteria")
	if len(rawCriteria) > 0 {
		snap.AcceptanceCriteria = make([]msg.PlanAcceptanceCriterion, 0, len(rawCriteria))
		for _, raw := range rawCriteria {
			criterionMap, ok := toMap(raw)
			if !ok {
				continue
			}
			snap.AcceptanceCriteria = append(snap.AcceptanceCriteria, msg.PlanAcceptanceCriterion{
				Given:    stringFromKey(criterionMap, "Given"),
				When:     stringFromKey(criterionMap, "When"),
				Then:     stringFromKey(criterionMap, "Then"),
				Priority: stringFromKey(criterionMap, "Priority"),
			})
		}
	}

	// Parse examples.
	rawExamples := sliceFromKey(data, "Examples")
	if len(rawExamples) > 0 {
		snap.Examples = make([]msg.PlanTaskExample, 0, len(rawExamples))
		for _, raw := range rawExamples {
			exampleMap, ok := toMap(raw)
			if !ok {
				continue
			}
			snap.Examples = append(snap.Examples, msg.PlanTaskExample{
				Label:       stringFromKey(exampleMap, "Label"),
				Language:    stringFromKey(exampleMap, "Language"),
				Code:        stringFromKey(exampleMap, "Code"),
				Explanation: stringFromKey(exampleMap, "Explanation"),
			})
		}
	}

	// Parse affected files.
	rawFiles := sliceFromKey(data, "AffectedFiles")
	if len(rawFiles) > 0 {
		snap.AffectedFiles = make([]msg.PlanFileTarget, 0, len(rawFiles))
		for _, raw := range rawFiles {
			fileMap, ok := toMap(raw)
			if !ok {
				continue
			}
			snap.AffectedFiles = append(snap.AffectedFiles, msg.PlanFileTarget{
				Path:      stringFromKey(fileMap, "Path"),
				Operation: stringFromKey(fileMap, "Operation"),
				Reason:    stringFromKey(fileMap, "Reason"),
			})
		}
	}

	return snap
}

// guideError is a simple error type for guide response errors.
type guideError string

func (e guideError) Error() string { return string(e) }
