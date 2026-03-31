package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
}

// BeginAutoInterAgentRouteBranch only preserves an already-established nested
// child branch. Generic route helpers must not invent new consult/challenge
// rows on their own; explicit consult/challenge/store entry points own that.
func BeginAutoInterAgentRouteBranch(
	ctx context.Context,
	_ string,
	_ any,
	metadata map[string]any,
) (context.Context, InterAgentBranchHandle) {
	if branch, ok := interAgentBranchMetadataFromMetadata(metadata); ok {
		return ctx, InterAgentBranchHandle{
			branch:        branch,
			successStatus: normalizeInterAgentSuccessStatus(branch.Kind, ""),
		}
	}
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		if branch, ok := interAgentBranchMetadataFromMetadata(stream.Metadata); ok {
			return ctx, InterAgentBranchHandle{
				branch:        branch,
				successStatus: normalizeInterAgentSuccessStatus(branch.Kind, ""),
			}
		}
	}
	return ctx, InterAgentBranchHandle{}
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
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: toolCallKey,
		ToolName:    toolName,
		ArgsSummary: argsSummary,
		FullArgs:    fullArgs,
		Phase:       ToolCallStart,
		StartedAt:   startedAt,
		InterAgent:  interAgent,
	})

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
	}
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
func (h InterAgentBranchHandle) Complete(ctx context.Context, summary, output string, err error) {
	if !h.synthetic || strings.TrimSpace(h.toolCallKey) == "" {
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
	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: h.toolCallKey,
		ToolName:    h.toolName,
		ArgsSummary: h.argsSummary,
		FullArgs:    h.fullArgs,
		Output:      strings.TrimSpace(output),
		ErrorMsg:    errorMsg,
		Phase:       ToolCallComplete,
		StartedAt:   h.startedAt,
		Duration:    time.Since(h.startedAt),
		Success:     success,
		InterAgent:  interAgent,
	})
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
