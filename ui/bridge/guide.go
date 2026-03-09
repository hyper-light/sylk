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
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/redact"
)

const (
	guideBridgeName = "bridge.guide"
	guideBufferSize = 256
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
	buffer       chan *guide.Message
	dropped      atomic.Int64
	done         chan struct{}
	subscription guide.Subscription
	sessionID    string
	stopOnce     sync.Once
}

// NewGuideBridge creates a bridge that converts Guide bus response messages
// into Bubble Tea messages.
func NewGuideBridge(bus guide.EventBus, scope *concurrency.GoroutineScope, sessionID string) *GuideBridge {
	return &GuideBridge{
		bus:       bus,
		scope:     scope,
		buffer:    make(chan *guide.Message, guideBufferSize),
		done:      make(chan struct{}),
		sessionID: sessionID,
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
	return b.scope.Go(guideBridgeName, guideDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the bus and signals the drain goroutine to exit.
func (b *GuideBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.subscription != nil {
			_ = b.subscription.Unsubscribe()
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
	select {
	case b.buffer <- busMsg:
	default:
		b.dropped.Add(1)
		if stream, ok := busMsg.GetStreamResponse(); ok && stream.Event != nil && stream.Event.Type == guide.StreamEventComplete {
			bridgeEventDebugLog().Warn("GuideBridge: STREAM_COMPLETE_DROPPED",
				"correlation_id", busMsg.CorrelationID,
				"buffer_cap", cap(b.buffer),
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
			select {
			case busMsg := <-b.buffer:
				b.dispatch(busMsg, program)
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// dispatch converts a bus message into the appropriate Bubble Tea message(s).
func (b *GuideBridge) dispatch(busMsg *guide.Message, program TeaProgram) {
	if busMsg == nil {
		return
	}
	if resp, ok := busMsg.GetRouteResponse(); ok {
		program.Send(toGuideMsg(resp))
		return
	}
	if stream, ok := busMsg.GetStreamResponse(); ok {
		b.dispatchStream(stream, program)
		return
	}
	if errText, ok := busMsg.GetError(); ok {
		program.Send(msg.StreamErrorMsg{
			SessionID:     b.sessionID,
			CorrelationID: busMsg.CorrelationID,
			Err:           guideError(redact.Text(errText)),
		})
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
		program.Send(parseStreamStartMsg(sid, cid, stream))
	case guide.StreamEventData:
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
		program.Send(toStreamProgressMsg(sid, cid, stream))
	case guide.StreamEventComplete:
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
		bridgeEventDebugLog().Info("GuideBridge: STREAM_COMPLETE_SENT",
			"correlation_id", cid)
	case guide.StreamEventError:
		program.Send(msg.StreamErrorMsg{SessionID: sid, CorrelationID: cid, Err: redact.Error(extractStreamError(stream.Event))})
	case guide.StreamEventRetry:
		status, _ := stream.Event.Data.(guide.RetryStatus)
		errText := ""
		if status.Err != nil {
			errText = summarizeRetryError(status.Err.Error())
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
		program.Send(parseToolCallEventMsg(sid, cid, stream.RespondingAgentID, stream.Event))
	}
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
	result.AgentName = strings.TrimSpace(stream.RespondingAgentName)
	result.AgentType = streamMetadataString(stream, "agent_type")
	result.PipelineID = streamMetadataString(stream, "pipeline_id")
	result.TaskID = streamMetadataString(stream, "task_id")
	result.TaskSlug = streamMetadataString(stream, "task_slug")
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

func parseToolCallEventMsg(sessionID, correlationID, agentID string, event *guide.StreamEvent) msg.ToolCallEventMsg {
	result := msg.ToolCallEventMsg{
		SessionID:     sessionID,
		CorrelationID: correlationID,
		AgentID:       agentID,
	}
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
		agentName = strings.TrimSpace(stream.RespondingAgentName)
	}
	progress := parseProgressData(event)
	m := msg.StreamProgressMsg{
		SessionID:     sessionID,
		CorrelationID: correlationID,
		AgentID:       agentID,
		AgentName:     agentName,
		AgentType:     streamMetadataString(stream, "agent_type"),
		PipelineID:    streamMetadataString(stream, "pipeline_id"),
		TaskID:        streamMetadataString(stream, "task_id"),
		TaskSlug:      streamMetadataString(stream, "task_slug"),
		Current:       progress.Current,
		Total:         progress.Total,
		Message:       redact.Text(strings.TrimSpace(progress.Message)),
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
	result.AgentName = strings.TrimSpace(stream.RespondingAgentName)
	result.AgentType = streamMetadataString(stream, "agent_type")
	result.PipelineID = streamMetadataString(stream, "pipeline_id")
	result.TaskID = streamMetadataString(stream, "task_id")
	result.TaskSlug = streamMetadataString(stream, "task_slug")
	if stream.Event != nil {
		result.Result = stream.Event.Data
	}
	return result
}

func streamMetadataString(stream *guide.StreamResponse, key string) string {
	if stream == nil || len(stream.Metadata) == 0 {
		return ""
	}
	value, ok := stream.Metadata[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
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
			Current: parseProgressInt(data["current"]),
			Total:   parseProgressInt(data["total"]),
			Message: parseProgressString(data["message"]),
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
	data := unwrapResponseEnvelope(payload)
	if text, ok := formatStructuredPayload(agentID, data); ok {
		return text
	}
	switch typed := data.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprint(data)
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

// summarizeRetryError extracts a human-readable one-liner from a raw provider
// error. For JSON error bodies (common with Google/OpenAI APIs), it extracts
// the nested "message" field. Falls back to the prefix before any JSON body.
func summarizeRetryError(raw string) string {
	if idx := strings.Index(raw, "{"); idx >= 0 {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(raw[idx:]), &envelope) == nil && envelope.Error.Message != "" {
			return envelope.Error.Message
		}
	}
	// Strip JSON body if present but unparseable.
	if idx := strings.Index(raw, "{"); idx > 0 {
		return strings.TrimSpace(raw[:idx])
	}
	return raw
}

// toGuideMsg converts a RouteResponse into a GuideResponseMsg.
func toGuideMsg(resp *guide.RouteResponse) msg.GuideResponseMsg {
	m := msg.GuideResponseMsg{
		CorrelationID: resp.CorrelationID,
		AgentID:       resp.RespondingAgentID,
		AgentName:     resp.RespondingAgentName,
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
	data := unwrapResponseEnvelope(resp.Data)
	if text, ok := formatStructuredPayload(resp.RespondingAgentID, data); ok {
		return text
	}
	switch typed := data.(type) {
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
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprint(data)
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
