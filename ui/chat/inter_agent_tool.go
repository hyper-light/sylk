package chat

import (
	"encoding/json"
	"strings"

	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	globalReviewThreadPrefix = "global_review:"
	pipelineThreadPrefix     = "pipeline:"
)

func toolCallHasActiveVisual(tc ToolCallRecord) bool {
	if tc.InterAgent != nil {
		return interAgentToolHasActiveVisual(tc.InterAgent)
	}
	return !tc.Completed
}

func interAgentToolHasActiveVisual(row *InterAgentTool) bool {
	if row == nil {
		return false
	}
	if row.Status == InterAgentToolPending {
		return true
	}
	for i := range row.Children {
		if interAgentChildHasActiveVisual(&row.Children[i]) {
			return true
		}
	}
	return false
}

func entryHasPendingInterAgentToolCalls(entry *ChatEntry) bool {
	if entry == nil {
		return false
	}
	for _, call := range entry.ToolCalls {
		if call.InterAgent == nil {
			continue
		}
		if interAgentToolHasActiveVisual(call.InterAgent) {
			return true
		}
	}
	return false
}

// settleChallengeRowsForChildCID flips Pending challenge rows in entry whose
// nested child activity (recursively) includes childCID. success=true marks
// them Done; success=false marks them Failed. Used when the response stream
// for that child has materialized — either as a top-level transfer back to
// the parent or as a nested completion. Only Pending rows are touched; Done
// and Failed rows are not re-touched.
func settleChallengeRowsForChildCID(entry *ChatEntry, childCID string, success bool) bool {
	if entry == nil {
		return false
	}
	childCID = strings.TrimSpace(childCID)
	if childCID == "" {
		return false
	}
	changed := false
	for i := range entry.ToolCalls {
		record := &entry.ToolCalls[i]
		if record.InterAgent == nil || record.InterAgent.Kind != InterAgentToolChallenge {
			continue
		}
		if record.InterAgent.Status != InterAgentToolPending {
			continue
		}
		if !interAgentToolHasChildCID(record.InterAgent, childCID) {
			continue
		}
		if success {
			record.InterAgent.Status = InterAgentToolDone
		} else {
			record.InterAgent.Status = InterAgentToolFailed
		}
		record.Completed = true
		record.Success = success
		changed = true
	}
	return changed
}

// interAgentToolHasChildCID reports whether childCID appears anywhere in the
// row's nested child activity tree.
func interAgentToolHasChildCID(row *InterAgentTool, childCID string) bool {
	if row == nil {
		return false
	}
	childCID = strings.TrimSpace(childCID)
	if childCID == "" {
		return false
	}
	var visit func([]InterAgentChildActivity) bool
	visit = func(children []InterAgentChildActivity) bool {
		for i := range children {
			if strings.TrimSpace(children[i].CorrelationID) == childCID {
				return true
			}
			for _, tc := range children[i].ToolCalls {
				if tc.InterAgent != nil && visit(tc.InterAgent.Children) {
					return true
				}
			}
		}
		return false
	}
	return visit(row.Children)
}

// lastInterAgentTemplateMatch scans calls backwards (skipping non-inter-agent
// rows) looking for the most recent inter-agent row and reports whether the
// incoming record has the same template (kind, normalized target set, and
// normalized summary). It returns the matched row index and true when the
// incoming record is an immediate same-template repeat — the caller collapses
// the two into a single row with an incremented RepeatCount instead of
// appending a visual duplicate.
//
// Only the IMMEDIATE previous inter-agent row is considered. An intervening
// different-template row breaks the run (those two rows are unrelated from the
// user's perspective and should render separately).
func lastInterAgentTemplateMatch(calls []ToolCallRecord, incoming *ToolCallRecord) (int, bool) {
	if incoming == nil || incoming.InterAgent == nil {
		return -1, false
	}
	for i := len(calls) - 1; i >= 0; i-- {
		prev := calls[i]
		if prev.InterAgent == nil {
			continue
		}
		return i, interAgentTemplatesMatch(prev.InterAgent, incoming.InterAgent)
	}
	return -1, false
}

func interAgentTemplatesMatch(a, b *InterAgentTool) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Kind != b.Kind {
		return false
	}
	if !interAgentStringSetsEqual(a.AgentTypes, b.AgentTypes) {
		return false
	}
	return normalizeInterAgentTemplateText(a.Summary) == normalizeInterAgentTemplateText(b.Summary)
}

func interAgentStringSetsEqual(a, b []string) bool {
	normA := normalizeAgentTypes(a)
	normB := normalizeAgentTypes(b)
	if len(normA) != len(normB) {
		return false
	}
	for i := range normA {
		if strings.TrimSpace(normA[i]) != strings.TrimSpace(normB[i]) {
			return false
		}
	}
	return true
}

func normalizeInterAgentTemplateText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func interAgentChildHasActiveVisual(child *InterAgentChildActivity) bool {
	if child == nil {
		return false
	}
	if !child.Completed {
		return true
	}
	if strings.TrimSpace(child.ThinkingText) != "" || strings.TrimSpace(child.ThinkingStatus) != "" {
		return true
	}
	for _, call := range child.ToolCalls {
		if toolCallHasActiveVisual(call) {
			return true
		}
	}
	return false
}

func buildInterAgentStartRecord(ev msg.ToolCallEventMsg) (ToolCallRecord, bool) {
	args := parseJSONMap(ev.FullArgs)
	fallbackRecord, fallbackOK := buildInterAgentStartRecordFallback(ev, args)
	if record, ok := interAgentRecordFromMetadata(ev); ok {
		mergeInterAgentStartRecord(&record, fallbackRecord, fallbackOK)
		return record, true
	}
	if fallbackOK {
		return fallbackRecord, true
	}
	return ToolCallRecord{}, false
}

func buildInterAgentStartRecordFallback(ev msg.ToolCallEventMsg, args map[string]any) (ToolCallRecord, bool) {
	switch {
	case isConsultationTool(ev.ToolName, args):
		targets := consultationTargets(ev.ToolName, args)
		if len(targets) == 0 {
			return ToolCallRecord{}, false
		}
		summary := firstNonEmptyString(
			stringFromMap(args, "question"),
			stringFromMap(args, "query"),
			stringFromMap(args, "description"),
			ev.ArgsSummary,
		)
		return ToolCallRecord{
			ToolCallKey: ev.ToolCallKey,
			ToolName:    ev.ToolName,
			ArgsSummary: ev.ArgsSummary,
			FullArgs:    ev.FullArgs,
			StartedAt:   ev.StartedAt,
			InterAgent: &InterAgentTool{
				Kind:       InterAgentToolConsult,
				AgentTypes: normalizeAgentTypes(targets),
				Summary:    normalizeInlineText(summary),
				Status:     InterAgentToolPending,
			},
		}, true
	case isChallengeTool(ev.ToolName):
		targets := challengeTargets(ev.ToolName, args, nil, ev.AgentType, ev.PipelineID)
		if len(targets) == 0 {
			return ToolCallRecord{}, false
		}
		return ToolCallRecord{
			ToolCallKey: ev.ToolCallKey,
			ToolName:    ev.ToolName,
			ArgsSummary: ev.ArgsSummary,
			FullArgs:    ev.FullArgs,
			StartedAt:   ev.StartedAt,
			InterAgent: &InterAgentTool{
				Kind:       InterAgentToolChallenge,
				AgentTypes: normalizeAgentTypes(targets),
				Summary: normalizeInlineText(firstNonEmptyString(
					stringFromMap(args, "request"),
					stringFromMap(args, "reason"),
					ev.ArgsSummary,
				)),
				Status: InterAgentToolPending,
			},
		}, true
	default:
		return ToolCallRecord{}, false
	}
}

func mergeInterAgentStartRecord(record *ToolCallRecord, fallback ToolCallRecord, fallbackOK bool) {
	if record == nil || !fallbackOK {
		return
	}
	if strings.TrimSpace(record.ToolCallKey) == "" {
		record.ToolCallKey = strings.TrimSpace(fallback.ToolCallKey)
	}
	if strings.TrimSpace(record.ToolName) == "" {
		record.ToolName = strings.TrimSpace(fallback.ToolName)
	}
	if strings.TrimSpace(record.ArgsSummary) == "" {
		record.ArgsSummary = strings.TrimSpace(fallback.ArgsSummary)
	}
	if strings.TrimSpace(record.FullArgs) == "" {
		record.FullArgs = strings.TrimSpace(fallback.FullArgs)
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = fallback.StartedAt
	}
	if record.InterAgent == nil {
		if fallback.InterAgent != nil {
			row := *fallback.InterAgent
			row.AgentTypes = append([]string(nil), fallback.InterAgent.AgentTypes...)
			record.InterAgent = &row
		}
		return
	}
	if len(record.InterAgent.AgentTypes) == 0 && fallback.InterAgent != nil {
		record.InterAgent.AgentTypes = append([]string(nil), fallback.InterAgent.AgentTypes...)
	}
	if strings.TrimSpace(record.InterAgent.Summary) == "" && fallback.InterAgent != nil {
		record.InterAgent.Summary = strings.TrimSpace(fallback.InterAgent.Summary)
	}
	if strings.TrimSpace(record.InterAgent.ThreadKey) == "" && fallback.InterAgent != nil {
		record.InterAgent.ThreadKey = strings.TrimSpace(fallback.InterAgent.ThreadKey)
	}
}

func updateInterAgentCompletion(record *ToolCallRecord, ev msg.ToolCallEventMsg) bool {
	if record == nil {
		return false
	}

	// Promotion path: the Start event could not resolve inter-agent
	// metadata (args lacked a target identifier the derivation could
	// read), so a regular tool-call row was created. The Complete event
	// for tools like challenge_peer carries resolved metadata in the
	// output (the handler does an activity lookup and stamps
	// target_agent_type into its result map). Attach that metadata to
	// the matching record instead of refusing the update — otherwise
	// the caller falls through to buildInterAgentCompletionFallback,
	// which appends a *second* row with the same ToolCallKey while
	// leaving the original row incomplete forever.
	if record.InterAgent == nil {
		if row, ok := interAgentRowFromMetadata(ev.InterAgent); ok && !ev.InterAgent.UpdateOrigin {
			record.InterAgent = row
		} else {
			return false
		}
	}

	if row, ok := interAgentRowFromMetadata(ev.InterAgent); ok && !ev.InterAgent.UpdateOrigin {
		if record.StartedAt.IsZero() {
			record.StartedAt = ev.StartedAt
		}
		record.Duration = ev.Duration
		record.Success = row.Status != InterAgentToolFailed
		record.Completed = true
		record.SyntheticCompletion = false
		record.Output = ev.Output
		record.ErrorMsg = ev.ErrorMsg
		record.InterAgent.Kind = row.Kind
		record.InterAgent.AgentTypes = append([]string(nil), row.AgentTypes...)
		record.InterAgent.Summary = row.Summary
		record.InterAgent.ThreadKey = row.ThreadKey
		record.InterAgent.Status = row.Status
		ensureInterAgentTerminalStatus(record, ev)
		return true
	}

	if record.StartedAt.IsZero() {
		record.StartedAt = ev.StartedAt
	}
	record.Duration = ev.Duration
	record.Success = ev.Success
	record.Completed = true
	record.SyntheticCompletion = false
	record.Output = ev.Output
	record.ErrorMsg = ev.ErrorMsg
	if strings.TrimSpace(record.ToolCallKey) == "" {
		record.ToolCallKey = strings.TrimSpace(ev.ToolCallKey)
	}

	args := parseJSONMap(ev.FullArgs)
	output := parseJSONMap(ev.Output)

	switch record.InterAgent.Kind {
	case InterAgentToolConsult:
		labels := consultationTargets(ev.ToolName, args)
		if outTarget := stringFromMap(output, "target"); outTarget != "" {
			labels = []string{outTarget}
		}
		if len(labels) > 0 {
			record.InterAgent.AgentTypes = normalizeAgentTypes(labels)
		}
		record.InterAgent.Summary = normalizeInlineText(firstNonEmptyString(
			consultationResponseSummary(output),
			ev.ErrorMsg,
			record.InterAgent.Summary,
		))
		if interAgentConsultationFailed(ev.Success, output, ev.ErrorMsg) {
			record.InterAgent.Status = InterAgentToolFailed
			record.Success = false
			return true
		}
		record.InterAgent.Status = InterAgentToolDone
		return true
	case InterAgentToolChallenge:
		if !ev.Success {
			record.InterAgent.Summary = normalizeInlineText(firstNonEmptyString(ev.ErrorMsg, record.InterAgent.Summary))
			record.InterAgent.Status = InterAgentToolFailed
			record.Success = false
			return true
		}
		if threadKey := challengeThreadKey(ev.ToolName, args, output); threadKey != "" {
			record.InterAgent.ThreadKey = threadKey
		}
		if labels := challengeTargets(ev.ToolName, args, output, ev.AgentType, ev.PipelineID); len(labels) > 0 {
			record.InterAgent.AgentTypes = normalizeAgentTypes(labels)
		}
		if summary := normalizeInlineText(firstNonEmptyString(stringFromMap(args, "request"), record.InterAgent.Summary)); summary != "" {
			record.InterAgent.Summary = summary
		}
		// Challenge dispatch completed, but the exchange remains pending until
		// a response/validation arrives on the shared challenge thread.
		record.InterAgent.Status = InterAgentToolPending
		return true
	case InterAgentToolApproval:
		if !ev.Success {
			record.InterAgent.Summary = normalizeInlineText(firstNonEmptyString(ev.ErrorMsg, record.InterAgent.Summary))
			record.InterAgent.Status = InterAgentToolFailed
			record.Success = false
			return true
		}
		record.InterAgent.Summary = normalizeInlineText(firstNonEmptyString(
			ev.ErrorMsg,
			consultationResponseSummary(output),
			record.InterAgent.Summary,
		))
		record.InterAgent.Status = InterAgentToolDone
		return true
	default:
		return false
	}
}

func interAgentOriginUpdate(ev msg.ToolCallEventMsg, currentAgentType string) (*InterAgentTool, bool) {
	if ev.InterAgent != nil && ev.InterAgent.UpdateOrigin {
		return interAgentRowFromMetadata(ev.InterAgent)
	}
	args := parseJSONMap(ev.FullArgs)
	output := parseJSONMap(ev.Output)

	switch ev.ToolName {
	case "validate_global_review":
		challengeID := firstNonEmptyString(stringFromMap(output, "challenge_id"), stringFromMap(args, "challenge_id"))
		if challengeID == "" {
			return nil, false
		}
		status := stringFromMap(output, "status")
		return &InterAgentTool{
			Kind:      InterAgentToolChallenge,
			ThreadKey: globalReviewThreadPrefix + challengeID,
			AgentTypes: normalizeAgentTypes([]string{firstNonEmptyString(
				stringFromMap(output, "responding_agent"),
				currentAgentType,
			)}),
			Summary: normalizeInlineText(firstNonEmptyString(
				stringFromMap(args, "summary"),
				stringFromMap(output, "summary"),
				ev.ArgsSummary,
			)),
			Status: validationStatusToInterAgentStatus(status, ev.Success, ev.ErrorMsg),
		}, true
	case "process_global_validation":
		challengeID := firstNonEmptyString(stringFromMap(output, "challenge_id"), stringFromMap(args, "challenge_id"))
		if challengeID == "" {
			return nil, false
		}
		return &InterAgentTool{
			Kind:      InterAgentToolChallenge,
			ThreadKey: globalReviewThreadPrefix + challengeID,
			Summary: normalizeInlineText(firstNonEmptyString(
				stringFromMap(args, "summary"),
				stringFromMap(output, "decision"),
				ev.ArgsSummary,
			)),
			Status: validationDecisionToInterAgentStatus(firstNonEmptyString(
				stringFromMap(output, "decision"),
				stringFromMap(args, "decision"),
			), ev.Success, ev.ErrorMsg),
		}, true
	case "validate_work":
		challengeID := firstNonEmptyString(stringFromMap(output, "challenge_id"), stringFromMap(args, "challenge_id"))
		if challengeID == "" {
			return nil, false
		}
		status := stringFromMap(output, "status")
		return &InterAgentTool{
			Kind:      InterAgentToolChallenge,
			ThreadKey: responseThreadKey(ev.ToolName, args, output, challengeID),
			AgentTypes: normalizeAgentTypes([]string{firstNonEmptyString(
				stringFromMap(output, "responding_agent"),
				currentAgentType,
			)}),
			Summary: normalizeInlineText(firstNonEmptyString(
				stringFromMap(args, "summary"),
				stringFromMap(output, "summary"),
				ev.ArgsSummary,
			)),
			Status: validationStatusToInterAgentStatus(status, ev.Success, ev.ErrorMsg),
		}, true
	case "process_validation":
		challengeID := firstNonEmptyString(stringFromMap(output, "challenge_id"), stringFromMap(args, "challenge_id"))
		if challengeID == "" {
			return nil, false
		}
		return &InterAgentTool{
			Kind:      InterAgentToolChallenge,
			ThreadKey: responseThreadKey(ev.ToolName, args, output, challengeID),
			Summary: normalizeInlineText(firstNonEmptyString(
				stringFromMap(args, "summary"),
				stringFromMap(output, "decision"),
				ev.ArgsSummary,
			)),
			Status: validationDecisionToInterAgentStatus(firstNonEmptyString(
				stringFromMap(output, "decision"),
				stringFromMap(args, "decision"),
			), ev.Success, ev.ErrorMsg),
		}, true
	default:
		return nil, false
	}
}

// responseThreadKey computes a challenge-response ThreadKey from explicit
// values on the event payload. It requires either a pre-computed `thread_key`
// or an explicit `protocol_scope` — no tool-name-based guessing. Producers
// that emit validate_work / process_validation / validate_global_review /
// process_global_validation must include `protocol_scope` in their output
// map; the deriveInterAgentOriginUpdate producer path does this via
// interAgentResponseThreadKey and the skill handlers themselves now stamp
// protocol_scope on their outputs. Empty return means the caller cannot
// cross-reference via ThreadKey and falls back to the existing origin-update
// logic.
func responseThreadKey(_ string, args, output map[string]any, challengeID string) string {
	if explicit := firstNonEmptyString(stringFromMap(output, "thread_key"), stringFromMap(args, "thread_key")); explicit != "" {
		return explicit
	}
	scope := firstNonEmptyString(stringFromMap(output, "protocol_scope"), stringFromMap(args, "protocol_scope"))
	switch strings.TrimSpace(scope) {
	case "global_review":
		return globalReviewThreadPrefix + challengeID
	case "pipeline":
		return pipelineThreadPrefix + challengeID
	}
	return ""
}

func buildInterAgentCompletionFallback(ev msg.ToolCallEventMsg, currentAgentType string) (ToolCallRecord, bool) {
	if record, ok := interAgentRecordFromMetadata(ev); ok {
		record.Duration = ev.Duration
		record.Success = ev.Success
		record.Completed = true
		record.Output = ev.Output
		record.ErrorMsg = ev.ErrorMsg
		if row, ok := interAgentRowFromMetadata(ev.InterAgent); ok {
			record.Success = row.Status != InterAgentToolFailed
		}
		ensureInterAgentTerminalStatus(&record, ev)
		return record, true
	}
	if record, ok := buildInterAgentStartRecord(ev); ok {
		record.Duration = ev.Duration
		record.Success = ev.Success
		record.Completed = true
		record.Output = ev.Output
		record.ErrorMsg = ev.ErrorMsg
		if updateInterAgentCompletion(&record, ev) {
			ensureInterAgentTerminalStatus(&record, ev)
			return record, true
		}
		ensureInterAgentTerminalStatus(&record, ev)
		return record, true
	}
	if row, ok := interAgentOriginUpdate(ev, currentAgentType); ok {
		record := ToolCallRecord{
			ToolCallKey: ev.ToolCallKey,
			ToolName:    ev.ToolName,
			ArgsSummary: ev.ArgsSummary,
			FullArgs:    ev.FullArgs,
			Output:      ev.Output,
			ErrorMsg:    ev.ErrorMsg,
			StartedAt:   ev.StartedAt,
			Duration:    ev.Duration,
			Success:     row.Status != InterAgentToolFailed,
			Completed:   true,
			InterAgent:  row,
		}
		ensureInterAgentTerminalStatus(&record, ev)
		return record, true
	}
	return ToolCallRecord{}, false
}

// ensureInterAgentTerminalStatus normalizes an inter-agent row's Status when a
// completion event arrives without a usable terminal status in its metadata.
// Without this guard, a Phase-Complete event whose InterAgent.Status was left
// at the default "pending" produced a Completed=true record that the renderer
// still showed as in-flight, because the spinner branch in
// formatToolCallDuration only consulted Status.
//
// Successful Challenge rows legitimately land in Completed+Pending after the
// local dispatch and stay there until the peer response arrives on the shared
// thread — skipping the success branch preserves that. A FAILED challenge
// (the handler rejected the call, or the tool loop returned an error) has no
// peer coming back, so it must finalize as Failed now or the row hangs
// forever. This is the "orchestrator challenge stuck at 486s" class of bug:
// the LLM hallucinated an invalid target, the handler rejected, but the
// Challenge exception kept the row rendering as pending.
func ensureInterAgentTerminalStatus(record *ToolCallRecord, ev msg.ToolCallEventMsg) {
	if record == nil || record.InterAgent == nil || !record.Completed {
		return
	}
	if record.InterAgent.Status != InterAgentToolPending {
		return
	}
	if !ev.Success {
		record.InterAgent.Status = InterAgentToolFailed
		return
	}
	if record.InterAgent.Kind == InterAgentToolChallenge {
		return
	}
	record.InterAgent.Status = InterAgentToolDone
}

func interAgentRecordFromMetadata(ev msg.ToolCallEventMsg) (ToolCallRecord, bool) {
	row, ok := interAgentRowFromMetadata(ev.InterAgent)
	if !ok {
		return ToolCallRecord{}, false
	}
	if ev.InterAgent != nil && ev.InterAgent.UpdateOrigin {
		return ToolCallRecord{}, false
	}
	return ToolCallRecord{
		ToolCallKey: ev.ToolCallKey,
		ToolName:    ev.ToolName,
		ArgsSummary: ev.ArgsSummary,
		FullArgs:    ev.FullArgs,
		Output:      ev.Output,
		ErrorMsg:    ev.ErrorMsg,
		StartedAt:   ev.StartedAt,
		Duration:    ev.Duration,
		Success:     row.Status != InterAgentToolFailed,
		Completed:   ev.Phase == 1,
		InterAgent:  row,
	}, true
}

func interAgentRowFromMetadata(meta *msg.InterAgentToolEventMsg) (*InterAgentTool, bool) {
	if meta == nil || strings.TrimSpace(meta.Kind) == "" {
		return nil, false
	}
	kind := InterAgentToolConsult
	switch strings.TrimSpace(meta.Kind) {
	case "challenge":
		kind = InterAgentToolChallenge
	case "approval":
		kind = InterAgentToolApproval
	case "store":
		kind = InterAgentToolStore
	}
	status := InterAgentToolPending
	switch strings.TrimSpace(meta.Status) {
	case "done":
		status = InterAgentToolDone
	case "failed":
		status = InterAgentToolFailed
	}
	return &InterAgentTool{
		Kind:       kind,
		AgentTypes: normalizeAgentTypes(meta.AgentTypes),
		Summary:    normalizeInlineText(meta.Summary),
		ThreadKey:  strings.TrimSpace(meta.ThreadKey),
		Status:     status,
	}, true
}

func isConsultationTool(toolName string, args map[string]any) bool {
	toolName = strings.TrimSpace(toolName)
	if strings.HasPrefix(toolName, "consult_") {
		return true
	}
	if toolName != "consult" {
		return false
	}
	target := stringFromMap(args, "target")
	if target == "" {
		return false
	}
	mode := strings.TrimSpace(stringFromMap(args, "mode"))
	return mode == "single" || mode == "knowledge" || mode == ""
}

func isChallengeTool(toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	return toolName == "challenge_agent" || strings.HasPrefix(toolName, "challenge_")
}

// eventIsInterAgentOriginUpdate reports whether an incoming tool-call event
// is an *origin-update* — a message that should patch an existing InterAgent
// row (the challenge the responder is answering) rather than create a new
// one. Origin-update Phase 1 events carry the UpdateOrigin flag in their
// serialized metadata; Phase 0 events for response tools carry no InterAgent
// metadata at all (emission-side suppresses it), so classification at Start
// still falls back on tool name. Both cases are handled here so callers have
// a single discriminator.
//
// Do NOT split this back into tool-name vs flag checks at callsites — that
// scatter was the root cause of the duplicate-row bug class where the
// nested-stream dispatcher and the top-level dispatcher classified events
// inconsistently.
func eventIsInterAgentOriginUpdate(ev msg.ToolCallEventMsg) bool {
	if ev.InterAgent != nil && ev.InterAgent.UpdateOrigin {
		return true
	}
	return isInterAgentResponseTool(ev.ToolName)
}

// isInterAgentResponseTool defers to the canonical classifier in
// agents/shared so the emission and UI sides share exactly one list. A new
// response tool added there is automatically picked up by the UI.
func isInterAgentResponseTool(toolName string) bool {
	return shared.IsInterAgentResponseToolName(toolName)
}

func consultationTargets(toolName string, args map[string]any) []string {
	if target := stringFromMap(args, "target"); target != "" {
		return []string{target}
	}
	// Mirror the emission-side classifier in
	// agents/shared/inter_agent_tool_event.go:interAgentConsultationTargets.
	// This path runs only as a fallback when the event arrives without
	// stamped inter-agent metadata, but if the two sides diverge the
	// fallback misclassifies silently — consult_peer was invisible in the
	// chat for exactly this reason.
	if target := stringFromMap(args, "target_agent_type"); target != "" {
		return []string{target}
	}
	switch strings.TrimSpace(toolName) {
	case "consult_librarian_style":
		return []string{"librarian"}
	case "consult_academic_approach":
		return []string{"academic"}
	case "consult_archivalist_context":
		return []string{"archivalist"}
	case "request_architect_research":
		return []string{"architect"}
	case "validate_approach":
		return []string{"librarian"}
	case "consult":
		if strings.TrimSpace(stringFromMap(args, "mode")) != "pre_planning" {
			return nil
		}
		targets := []string{"librarian", "archivalist"}
		if boolFromMap(args, "include_academic") {
			targets = append(targets, "academic")
		}
		return targets
	default:
		if target := firstKnownChallengeAgentInName(strings.TrimPrefix(toolName, "consult_")); target != "" {
			return []string{target}
		}
		return nil
	}
}

func boolFromMap(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	value, ok := m[key]
	if !ok || value == nil {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func challengeTargets(toolName string, args, output map[string]any, currentAgentType, pipelineID string) []string {
	scope := challengeScope(toolName, args, output, currentAgentType, pipelineID)
	if targets := stringSliceFromMap(args, "target_agents"); len(targets) > 0 {
		return normalizeChallengeTargetsForScope(targets, scope)
	}
	if targets := stringSliceFromMap(output, "target_agents"); len(targets) > 0 {
		return normalizeChallengeTargetsForScope(targets, scope)
	}
	if target := firstNonEmptyString(stringFromMap(args, "target_agent"), stringFromMap(output, "target_agent")); target != "" {
		return normalizeChallengeTargetsForScope([]string{target}, scope)
	}
	if strings.TrimSpace(toolName) == "challenge_agent" {
		return nil
	}
	target := strings.TrimPrefix(strings.TrimSpace(toolName), "challenge_")
	target = strings.TrimPrefix(target, "global_")
	if resolved := firstKnownChallengeAgentInName(target); resolved != "" {
		return normalizeChallengeTargetsForScope([]string{resolved}, scope)
	}
	return nil
}

func challengeScope(toolName string, args, output map[string]any, currentAgentType, pipelineID string) string {
	scope := firstNonEmptyString(stringFromMap(output, "protocol_scope"), stringFromMap(args, "protocol_scope"))
	if scope != "" {
		return scope
	}
	if strings.TrimSpace(pipelineID) != "" {
		return "pipeline"
	}
	switch normalizeAgentTypeLabel(currentAgentType) {
	case "inspector-pipeline", "tester-pipeline", "engineer", "designer":
		return "pipeline"
	}
	if strings.TrimSpace(toolName) == "challenge_agent" && firstNonEmptyString(stringFromMap(output, "challenge_id"), stringFromMap(args, "challenge_id")) != "" {
		return "pipeline"
	}
	return ""
}

func normalizeChallengeTargetsForScope(targets []string, scope string) []string {
	targets = normalizeAgentTypes(targets)
	if scope != "pipeline" {
		return targets
	}
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		switch normalizeAgentTypeLabel(target) {
		case "inspector":
			out = append(out, "inspector-pipeline")
		case "tester":
			out = append(out, "tester-pipeline")
		default:
			out = append(out, normalizeAgentTypeLabel(target))
		}
	}
	return normalizeAgentTypes(out)
}

// challengeThreadKey computes a challenge-dispatch ThreadKey from explicit
// values on the event payload. It requires either a pre-computed `thread_key`
// or an explicit `protocol_scope` — no tool-name-based guessing. Producers
// stamp protocol_scope onto challenge dispatch outputs (see
// pipelineTurnSelectionResult, global_review_protocol.go); empty return
// means the event is missing the scope and the UI falls back to
// non-ThreadKey-based consolidation.
func challengeThreadKey(_ string, args, output map[string]any) string {
	if explicit := firstNonEmptyString(stringFromMap(output, "thread_key"), stringFromMap(args, "thread_key")); explicit != "" {
		return explicit
	}
	challengeID := firstNonEmptyString(stringFromMap(output, "challenge_id"), stringFromMap(args, "challenge_id"))
	if challengeID == "" {
		return ""
	}
	switch strings.TrimSpace(firstNonEmptyString(stringFromMap(output, "protocol_scope"), stringFromMap(args, "protocol_scope"))) {
	case "global_review":
		return globalReviewThreadPrefix + challengeID
	case "pipeline":
		return pipelineThreadPrefix + challengeID
	}
	return ""
}

func firstKnownChallengeAgentInName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "_", "-"))
	if name == "" {
		return ""
	}
	for _, candidate := range []string{
		"inspector-pipeline",
		"tester-pipeline",
		"orchestrator",
		"architect",
		"inspector",
		"designer",
		"engineer",
		"academic",
		"librarian",
		"archivalist",
		"tester",
		"guide",
	} {
		if strings.Contains(name, candidate) {
			return candidate
		}
	}
	return ""
}

func validationStatusToInterAgentStatus(status string, success bool, errMsg string) InterAgentToolStatus {
	if !success || strings.TrimSpace(errMsg) != "" {
		return InterAgentToolFailed
	}
	switch strings.TrimSpace(status) {
	case "passed", "partial":
		return InterAgentToolDone
	default:
		return InterAgentToolFailed
	}
}

func validationDecisionToInterAgentStatus(decision string, success bool, errMsg string) InterAgentToolStatus {
	if !success || strings.TrimSpace(errMsg) != "" {
		return InterAgentToolFailed
	}
	switch strings.TrimSpace(decision) {
	case "accept", "handoff":
		return InterAgentToolDone
	default:
		return InterAgentToolFailed
	}
}

func interAgentConsultationFailed(success bool, output map[string]any, errMsg string) bool {
	if !success || strings.TrimSpace(errMsg) != "" {
		return true
	}
	switch strings.TrimSpace(stringFromMap(output, "status")) {
	case "", "ok", "success", "ready":
		return false
	default:
		return true
	}
}

func consultationResponseSummary(output map[string]any) string {
	if output == nil {
		return ""
	}
	if ready, ok := output["ready"].(bool); ok && ready {
		if reused, _ := output["reused"].(bool); reused {
			return "consultation gate reused"
		}
		return "consultation gate satisfied"
	}
	if summary := normalizeInlineText(shared.SummarizeInterAgentPayload(output)); summary != "" {
		return summary
	}
	if requested, ok := output["requested"].(bool); ok && requested {
		if value := normalizeInlineText(stringFromMap(output, "description")); value != "" {
			return value
		}
	}
	return ""
}

func parseJSONMap(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(anySummary(typed))
	}
}

func stringSliceFromMap(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	value, ok := m[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return normalizeAgentTypes(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return normalizeAgentTypes(out)
	default:
		return nil
	}
}

func anySummary(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func normalizeAgentTypes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeAgentTypeLabel(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeAgentTypeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, agentPart, ok := splitPipelineBadgeIdentity(value); ok {
		value = agentPart
	}
	return strings.TrimSpace(value)
}

func normalizeInlineText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := normalizeInlineText(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
