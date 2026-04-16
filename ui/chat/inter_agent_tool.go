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
	if record == nil || record.InterAgent == nil {
		return false
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

func responseThreadKey(toolName string, args, output map[string]any, challengeID string) string {
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
	if toolName == "validate_work" || toolName == "process_validation" {
		return pipelineThreadPrefix + challengeID
	}
	return globalReviewThreadPrefix + challengeID
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
		return record, true
	}
	if record, ok := buildInterAgentStartRecord(ev); ok {
		record.Duration = ev.Duration
		record.Success = ev.Success
		record.Completed = true
		record.Output = ev.Output
		record.ErrorMsg = ev.ErrorMsg
		if updateInterAgentCompletion(&record, ev) {
			return record, true
		}
	}
	if row, ok := interAgentOriginUpdate(ev, currentAgentType); ok {
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
			Completed:   true,
			InterAgent:  row,
		}, true
	}
	return ToolCallRecord{}, false
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

func isInterAgentResponseTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "validate_global_review", "process_global_validation", "validate_work", "process_validation":
		return true
	default:
		return false
	}
}

func consultationTargets(toolName string, args map[string]any) []string {
	if target := stringFromMap(args, "target"); target != "" {
		return []string{target}
	}
	switch strings.TrimSpace(toolName) {
	case "consult_librarian_style":
		return []string{"librarian"}
	case "consult_academic_approach":
		return []string{"academic"}
	case "consult_archivalist_context":
		return []string{"archivalist"}
	default:
		return nil
	}
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

func challengeThreadKey(toolName string, args, output map[string]any) string {
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
	if toolName == "challenge_agent" {
		return pipelineThreadPrefix + challengeID
	}
	return globalReviewThreadPrefix + challengeID
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
