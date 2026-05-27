package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

// InterAgentBranchSpec describes a direct inter-agent exchange that should be
// visible in the parent agent's chat tree even when no real tool call is
// currently active.
type InterAgentBranchSpec struct {
	Kind          string
	ToolName      string
	AgentTypes    []string
	Summary       string
	ThreadKey     string
	SuccessStatus string
	Args          map[string]any
}

// InterAgentBranchHandle tracks either a reused active tool-call branch or a
// synthetic branch emitted for direct inter-agent exchanges.
//
// `completed` is a pointer shared across copies of the handle so Complete is
// idempotent no matter how many times a caller (or a deferred safety-net
// caller) invokes it. Auto-reused handles do not allocate `completed` — their
// Complete is already a no-op because `synthetic` is false.
type InterAgentBranchHandle struct {
	branch        InterAgentBranchMetadata
	synthetic     bool
	toolCallKey   string
	toolName      string
	fullArgs      string
	argsSummary   string
	startedAt     time.Time
	interAgent    *InterAgentToolEvent
	successStatus string
	completed     *atomic.Bool
}

// BeginInterAgentBranch ensures ctx carries branch identity for an inter-agent
// child route. When a real inter-agent tool call is already active it is
// reused; otherwise a synthetic branch row is emitted so the child stream has
// an anchor before its first StreamStart reaches the UI.
func BeginInterAgentBranch(ctx context.Context, spec InterAgentBranchSpec) (context.Context, InterAgentBranchHandle) {
	kind := normalizeInterAgentBranchKind(spec.Kind)
	if kind == "" {
		return ctx, InterAgentBranchHandle{}
	}
	if claimsBackedInterAgentBranchKind(kind) {
		return ctx, InterAgentBranchHandle{}
	}

	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" {
		return ctx, InterAgentBranchHandle{}
	}

	targets := normalizeAgentTypeList(spec.AgentTypes)
	summary := firstNonEmptyInline(spec.Summary)
	threadKey := strings.TrimSpace(spec.ThreadKey)
	successStatus := normalizeInterAgentSuccessStatus(kind, spec.SuccessStatus)

	if active, ok := ActiveToolCallFromContext(ctx); ok && active.InterAgent != nil && strings.TrimSpace(active.ToolCallKey) != "" {
		return ctx, InterAgentBranchHandle{
			branch: InterAgentBranchMetadata{
				ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
				ParentToolCallKey:   strings.TrimSpace(active.ToolCallKey),
				ThreadKey:           firstNonEmptyInline(threadKey, active.InterAgent.ThreadKey),
				Kind:                firstNonEmptyInline(kind, active.InterAgent.Kind),
			},
			toolCallKey:   strings.TrimSpace(active.ToolCallKey),
			toolName:      strings.TrimSpace(active.ToolName),
			fullArgs:      strings.TrimSpace(active.FullArgs),
			interAgent:    active.InterAgent,
			successStatus: successStatus,
		}
	}

	toolName := strings.TrimSpace(spec.ToolName)
	if toolName == "" {
		toolName = syntheticInterAgentToolName(kind, targets)
	}
	fullArgs, compactArgs := interAgentBranchArgs(spec.Args)
	argsSummary := firstNonEmptyInline(SummarizeToolArgs(toolName, compactArgs), summary)
	toolCallKey := fmt.Sprintf("%s_%s", kind, uuid.NewString()[:12])
	startedAt := time.Now()
	interAgent := &InterAgentToolEvent{
		Kind:       kind,
		AgentTypes: targets,
		Summary:    summary,
		ThreadKey:  threadKey,
		Status:     InterAgentToolEventStatusPending,
	}

	ctx = context.WithValue(ctx, activeToolCallContextKey{}, ActiveToolCallContext{
		ToolCallKey: toolCallKey,
		ToolName:    toolName,
		FullArgs:    fullArgs,
		InterAgent:  interAgent,
	})
	if err := EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: toolCallKey,
		ToolName:    toolName,
		ArgsSummary: argsSummary,
		FullArgs:    fullArgs,
		Phase:       ToolCallStart,
		StartedAt:   startedAt,
		InterAgent:  interAgent,
	}); err != nil {
		logMissingToolCallID(ctx, "inter_agent_branch_start", toolName, err)
	}
	logInterAgentDispatchStart(ctx, toolCallKey, kind, targets, threadKey, summary, startedAt)

	return ctx, InterAgentBranchHandle{
		branch: InterAgentBranchMetadata{
			ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
			ParentToolCallKey:   toolCallKey,
			ThreadKey:           threadKey,
			Kind:                kind,
		},
		synthetic:     true,
		toolCallKey:   toolCallKey,
		toolName:      toolName,
		fullArgs:      fullArgs,
		argsSummary:   argsSummary,
		startedAt:     startedAt,
		interAgent:    interAgent,
		successStatus: successStatus,
		completed:     new(atomic.Bool),
	}
}

// WithInterAgentBranch opens a synthetic inter-agent branch (if ctx has the
// stream metadata required), runs fn under the branch context, and guarantees
// exactly one Complete emission against the returned error — including when
// fn unwinds abnormally. The deferred Complete fires during panic propagation
// and the row is finalized as failed via the fnReturned flag, so the UI
// reflects the panic instead of a misleading success. We do NOT recover the
// panic; invariant violations must surface to the goroutine boundary.
//
// This is the canonical entry point for consultations, challenges, approvals,
// and other agent-to-agent exchanges that should render as a synthetic row.
// It replaces the manual BeginInterAgentBranch / branch.Complete pattern that
// had to be carefully threaded through every return path; that pattern is a
// documented footgun — every past "stuck row at N seconds" class of bug has
// been a missing Complete. Scope-based emission makes that class impossible
// by construction.
//
// Returns fn's error unchanged. When ctx is missing the stream metadata
// required by BeginInterAgentBranch, the branch is a no-op, fn still runs,
// and fn's error still propagates.
func WithInterAgentBranch(
	ctx context.Context,
	spec InterAgentBranchSpec,
	fn func(ctx context.Context, handle InterAgentBranchHandle) error,
) (err error) {
	branchCtx, branch := BeginInterAgentBranch(ctx, spec)
	fnReturned := false
	defer func() {
		if !fnReturned && err == nil {
			err = fmt.Errorf("inter-agent %s branch unwound abnormally without returning", spec.Kind)
		}
		branch.Complete(branchCtx, "", "", err)
	}()
	err = fn(branchCtx, branch)
	fnReturned = true
	return err
}

// WithInterAgentBranchMessage is WithInterAgentBranch with CompleteFromMessage
// semantics. It's the common shape for consult/route-sync flows that finalize
// the row using the terminal response payload rather than a plain error.
//
// As with WithInterAgentBranch, the deferred CompleteFromMessage fires during
// panic propagation and stamps a failed status via the fnReturned flag. We do
// not recover; invariant violations must surface.
func WithInterAgentBranchMessage(
	ctx context.Context,
	spec InterAgentBranchSpec,
	fn func(ctx context.Context, handle InterAgentBranchHandle) (*guide.Message, error),
) (response *guide.Message, err error) {
	branchCtx, branch := BeginInterAgentBranch(ctx, spec)
	fnReturned := false
	defer func() {
		if !fnReturned && err == nil {
			err = fmt.Errorf("inter-agent %s branch unwound abnormally without returning", spec.Kind)
		}
		branch.CompleteFromMessage(branchCtx, response, err)
	}()
	response, err = fn(branchCtx, branch)
	fnReturned = true
	return response, err
}

// InheritedBranchMetadata returns request metadata stamped with any
// inter-agent branch identity present on ctx (or inside the provided
// metadata map itself). It replaces the lifecycle-less half of
// BeginAutoInterAgentRouteBranch for callers that just need to propagate an
// existing branch's identity to an outbound route request without creating
// or completing a branch of their own.
//
// When ctx carries no branch metadata and the input metadata has none either,
// the original map is returned unchanged.
func InheritedBranchMetadata(ctx context.Context, metadata map[string]any) map[string]any {
	if branch, ok := interAgentBranchMetadataFromMetadata(metadata); ok {
		return RouteMetadataWithExplicitInterAgentBranch(ctx, metadata, branch)
	}
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		if branch, ok := interAgentBranchMetadataFromMetadata(stream.Metadata); ok {
			return RouteMetadataWithExplicitInterAgentBranch(ctx, metadata, branch)
		}
	}
	return metadata
}

// ApplyMetadata stamps this branch identity onto request metadata.
func (h InterAgentBranchHandle) ApplyMetadata(ctx context.Context, metadata map[string]any) map[string]any {
	if strings.TrimSpace(h.branch.ParentCorrelationID) == "" {
		return metadata
	}
	return RouteMetadataWithExplicitInterAgentBranch(ctx, metadata, h.branch)
}

// CompleteFromMessage finalizes a synthetic branch row using the terminal
// response message that satisfied the direct inter-agent request.
func (h InterAgentBranchHandle) CompleteFromMessage(ctx context.Context, msg *guide.Message, err error) {
	summary, output := interAgentMessageSummaryAndOutput(msg)
	if err == nil {
		err = interAgentMessageTerminalError(msg)
	}
	h.Complete(ctx, summary, output, err)
}

// Complete finalizes a synthetic branch row. Reused real tool-call branches are
// left alone because their owning tool loop will emit the authoritative
// completion event.
//
// Idempotent across calls via the handle's shared completed flag: a caller
// that explicitly completes on the success path and a deferred safety-net
// call that fires later both execute without producing a second UI row.
func (h InterAgentBranchHandle) Complete(ctx context.Context, summary, output string, err error) {
	if !h.synthetic || strings.TrimSpace(h.toolCallKey) == "" {
		return
	}
	if h.completed != nil && !h.completed.CompareAndSwap(false, true) {
		return
	}

	success := err == nil
	errorMsg := ""
	if err != nil {
		errorMsg = strings.TrimSpace(err.Error())
	}
	status := h.successStatus
	if !success {
		status = InterAgentToolEventStatusFailed
	}
	summary = firstNonEmptyInline(summary, errorMsg, output, h.summary())

	interAgent := &InterAgentToolEvent{
		Kind:       strings.TrimSpace(h.branch.Kind),
		AgentTypes: append([]string(nil), h.targets()...),
		Summary:    summary,
		ThreadKey:  strings.TrimSpace(h.branch.ThreadKey),
		Status:     status,
	}
	duration := time.Since(h.startedAt)
	if emitErr := EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: h.toolCallKey,
		ToolName:    h.toolName,
		ArgsSummary: h.argsSummary,
		FullArgs:    h.fullArgs,
		Output:      strings.TrimSpace(output),
		ErrorMsg:    errorMsg,
		Phase:       ToolCallComplete,
		StartedAt:   h.startedAt,
		Duration:    duration,
		Success:     success,
		InterAgent:  interAgent,
	}); emitErr != nil {
		logMissingToolCallID(ctx, "inter_agent_branch_complete", h.toolName, emitErr)
	}
	logInterAgentDispatchComplete(ctx, h.toolCallKey, interAgent.Kind, interAgent.AgentTypes, output, duration, success, errorMsg)
}

// logInterAgentDispatchStart writes an inter_agent_dispatch_started event
// to the parent agent's WAL/JSONL log, paired with the completion event
// emitted from InterAgentBranchHandle.Complete. The pre-existing
// EventConsultationSent path covers architect/engineer consultations
// only; this hook generalizes the dispatch lifecycle to every kind
// (consult, challenge, store, approval) routed through the canonical
// branch helper. No-op when ctx lacks a SessionEventLogger.
func logInterAgentDispatchStart(ctx context.Context, branchKey, kind string, targets []string, threadKey, summary string, startedAt time.Time) {
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	data := map[string]any{
		"branch_key": branchKey,
		"kind":       kind,
		"targets":    append([]string(nil), targets...),
		"started_at": startedAt.UnixNano(),
	}
	if threadKey != "" {
		data["thread_key"] = threadKey
	}
	if summary != "" {
		data["summary"] = summary
	}
	LogAgentEvent(lm.EventLogger, agentlog.EventInterAgentDispatchStarted,
		lm.AgentID, lm.SessionID, lm.CorrID, "info", data)
}

// logInterAgentDispatchComplete pairs with logInterAgentDispatchStart.
// Captures duration, success, output size, and error so the WAL has
// closed-loop dispatch records (vs. the prior open-loop signal where
// only the start half was visible until the response arrived).
func logInterAgentDispatchComplete(ctx context.Context, branchKey, kind string, targets []string, output string, duration time.Duration, success bool, errorMsg string) {
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	data := map[string]any{
		"branch_key":  branchKey,
		"kind":        kind,
		"targets":     append([]string(nil), targets...),
		"duration_ms": duration.Milliseconds(),
		"output_size": len(output),
		"success":     success,
	}
	if errorMsg != "" {
		data["error"] = errorMsg
	}
	level := "info"
	if !success {
		level = "warn"
	}
	LogAgentEvent(lm.EventLogger, agentlog.EventInterAgentDispatchCompleted,
		lm.AgentID, lm.SessionID, lm.CorrID, level, data)
}

func (h InterAgentBranchHandle) summary() string {
	if h.interAgent == nil {
		return ""
	}
	return strings.TrimSpace(h.interAgent.Summary)
}

func (h InterAgentBranchHandle) targets() []string {
	if h.interAgent == nil {
		return nil
	}
	return append([]string(nil), h.interAgent.AgentTypes...)
}

func claimsBackedInterAgentBranchKind(kind string) bool {
	switch normalizeInterAgentBranchKind(kind) {
	case InterAgentToolEventKindConsult, InterAgentToolEventKindChallenge:
		return true
	default:
		return false
	}
}

func normalizeInterAgentBranchKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case InterAgentToolEventKindConsult:
		return InterAgentToolEventKindConsult
	case InterAgentToolEventKindChallenge:
		return InterAgentToolEventKindChallenge
	case InterAgentToolEventKindApproval:
		return InterAgentToolEventKindApproval
	case InterAgentToolEventKindStore:
		return InterAgentToolEventKindStore
	default:
		return strings.TrimSpace(kind)
	}
}

func normalizeInterAgentSuccessStatus(kind, status string) string {
	status = strings.TrimSpace(status)
	if status != "" {
		return status
	}
	switch normalizeInterAgentBranchKind(kind) {
	case InterAgentToolEventKindChallenge:
		return InterAgentToolEventStatusPending
	default:
		return InterAgentToolEventStatusDone
	}
}

func syntheticInterAgentToolName(kind string, targets []string) string {
	kind = normalizeInterAgentBranchKind(kind)
	if len(targets) == 1 && strings.TrimSpace(targets[0]) != "" {
		return fmt.Sprintf("%s_%s", kind, strings.ReplaceAll(strings.TrimSpace(targets[0]), "-", "_"))
	}
	if kind == "" {
		return "inter_agent_route"
	}
	return "inter_agent_" + kind
}

func interAgentBranchArgs(args map[string]any) (pretty string, compact string) {
	if len(args) == 0 {
		return "", ""
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", ""
	}
	compact = string(encoded)
	return PrettyPrintArgs(compact), compact
}

func interAgentMessageSummaryAndOutput(msg *guide.Message) (string, string) {
	if msg == nil {
		return "", ""
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		if !resp.Success {
			errText := normalizeInlineString(resp.Error)
			return errText, errText
		}
		output := truncateInterAgentOutput(firstNonEmptyInline(routeResponseSummary(resp.Data), resp.Error))
		return output, output
	}
	if errText, ok := msg.GetError(); ok {
		errText = normalizeInlineString(errText)
		return errText, errText
	}
	return "", ""
}

func interAgentMessageTerminalError(msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		if resp.Success {
			return nil
		}
		if errText := normalizeInlineString(resp.Error); errText != "" {
			return fmt.Errorf("%s", errText)
		}
		return fmt.Errorf("inter-agent route failed")
	}
	if errText, ok := msg.GetError(); ok {
		if errText = normalizeInlineString(errText); errText != "" {
			return fmt.Errorf("%s", errText)
		}
		return fmt.Errorf("inter-agent request failed")
	}
	return nil
}

func routeResponseSummary(data any) string {
	return normalizeInlineString(SummarizeInterAgentPayload(data))
}

func truncateInterAgentOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return TruncateOutput(text, 240)
}

func interAgentBranchMetadataFromMetadata(metadata map[string]any) (InterAgentBranchMetadata, bool) {
	if len(metadata) == 0 {
		return InterAgentBranchMetadata{}, false
	}
	parentCorrelation := normalizeInlineString(stringFromAnyMap(metadata, streamMetadataParentCorrelation))
	parentToolCallKey := normalizeInlineString(stringFromAnyMap(metadata, streamMetadataParentToolCallKey))
	threadKey := normalizeInlineString(stringFromAnyMap(metadata, streamMetadataInterAgentThread))
	kind := normalizeInterAgentBranchKind(stringFromAnyMap(metadata, streamMetadataInterAgentKind))
	if parentCorrelation == "" || !isNestedInterAgentKind(kind) {
		return InterAgentBranchMetadata{}, false
	}
	return InterAgentBranchMetadata{
		ParentCorrelationID: parentCorrelation,
		ParentToolCallKey:   parentToolCallKey,
		ThreadKey:           threadKey,
		Kind:                kind,
	}, true
}

func inferInterAgentRouteSummary(payload any, metadata map[string]any) string {
	if len(metadata) > 0 {
		if summary := anyMapInterAgentSummary(metadata); summary != "" {
			return summary
		}
	}
	switch typed := payload.(type) {
	case nil:
		return ""
	case string:
		return inferInterAgentRouteStringSummary(typed)
	case []byte:
		return inferInterAgentRouteStringSummary(string(typed))
	case map[string]any:
		if summary := anyMapInterAgentSummary(typed); summary != "" {
			return summary
		}
		return truncateInterAgentOutput(normalizeInlineString(anyValueSummary(typed)))
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return inferInterAgentRouteStringSummary(string(raw))
	}
}

// BeginArchivalistStoreBranch synthesizes a child row for archival store
// operations so they remain nested under the originating agent turn instead of
// leaking into top-level chat.
func BeginArchivalistStoreBranch(
	ctx context.Context,
	summary string,
	args map[string]any,
) (context.Context, InterAgentBranchHandle) {
	clonedArgs := make(map[string]any, len(args)+1)
	for key, value := range args {
		clonedArgs[key] = value
	}
	clonedArgs["target"] = "archivalist"
	return BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:          InterAgentToolEventKindStore,
		ToolName:      "store_archivalist",
		AgentTypes:    []string{"archivalist"},
		Summary:       summary,
		SuccessStatus: InterAgentToolEventStatusDone,
		Args:          clonedArgs,
	})
}

func inferInterAgentRouteStringSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		if summary := anyMapInterAgentSummary(decoded); summary != "" {
			return summary
		}
	}
	return truncateInterAgentOutput(normalizeInlineString(raw))
}

func anyMapInterAgentSummary(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	for _, key := range []string{"summary", "request", "query", "question", "description", "message", "reason", "prompt"} {
		if summary := normalizeInlineString(stringFromAnyMap(values, key)); summary != "" {
			return truncateInterAgentOutput(summary)
		}
	}
	action := normalizeInlineString(stringFromAnyMap(values, "action"))
	kind := normalizeInlineString(stringFromAnyMap(values, "type"))
	taskID := normalizeInlineString(stringFromAnyMap(values, "task_id"))
	switch {
	case action != "" && taskID != "":
		return truncateInterAgentOutput(action + " " + taskID)
	case kind != "" && taskID != "":
		return truncateInterAgentOutput(kind + " " + taskID)
	case action != "":
		return truncateInterAgentOutput(action)
	case kind != "":
		return truncateInterAgentOutput(kind)
	default:
		return ""
	}
}
