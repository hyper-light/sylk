package shared

import (
	"encoding/json"
	"strings"
)

const (
	InterAgentToolEventKindConsult   = "consult"
	InterAgentToolEventKindChallenge = "challenge"
	InterAgentToolEventKindApproval  = "approval"
	InterAgentToolEventKindStore     = "store"

	InterAgentToolEventStatusPending = "pending"
	InterAgentToolEventStatusDone    = "done"
	InterAgentToolEventStatusFailed  = "failed"
)

const (
	globalReviewThreadPrefix = "global_review:"
	pipelineThreadPrefix     = "pipeline:"
)

// InterAgentToolEvent carries UI-facing metadata for tool calls that represent
// consultations, challenges, responses, or validation processing between
// agents. The chat layer consumes this directly instead of inferring meaning
// from tool names.
type InterAgentToolEvent struct {
	Kind         string   `json:"kind,omitempty"`
	AgentTypes   []string `json:"agent_types,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	ThreadKey    string   `json:"thread_key,omitempty"`
	Status       string   `json:"status,omitempty"`
	UpdateOrigin bool     `json:"update_origin,omitempty"`
}

// NormalizeInterAgentToolEventForEmit canonicalizes any caller-provided
// inter-agent metadata against the shared derivation rules. This prevents
// partial consult/challenge metadata from leaking into the UI stream when a
// caller supplies a non-nil but incomplete InterAgent payload.
func NormalizeInterAgentToolEventForEmit(
	toolName string,
	fullArgs string,
	output string,
	phase ToolCallPhase,
	success bool,
	errorMsg string,
	existing *InterAgentToolEvent,
	streamMetadata map[string]any,
) *InterAgentToolEvent {
	derived := DeriveInterAgentToolEvent(toolName, fullArgs, output, phase, success, errorMsg)
	if existing == nil {
		return normalizeInterAgentToolEventShape(derived, phase, toolName, fullArgs, output, streamMetadata)
	}

	normalized := &InterAgentToolEvent{
		Kind:         normalizeInterAgentBranchKind(existing.Kind),
		AgentTypes:   normalizeAgentTypeList(existing.AgentTypes),
		Summary:      normalizeInlineString(existing.Summary),
		ThreadKey:    normalizeInlineString(existing.ThreadKey),
		Status:       normalizeInlineString(existing.Status),
		UpdateOrigin: existing.UpdateOrigin,
	}

	if derived != nil {
		if normalized.Kind == "" {
			normalized.Kind = normalizeInterAgentBranchKind(derived.Kind)
		}
		if len(normalized.AgentTypes) == 0 {
			normalized.AgentTypes = append([]string(nil), normalizeAgentTypeList(derived.AgentTypes)...)
		}
		if normalized.Summary == "" {
			normalized.Summary = normalizeInlineString(derived.Summary)
		}
		if normalized.ThreadKey == "" {
			normalized.ThreadKey = normalizeInlineString(derived.ThreadKey)
		}
		if normalized.Status == "" {
			normalized.Status = normalizeInlineString(derived.Status)
		}
		normalized.UpdateOrigin = normalized.UpdateOrigin || derived.UpdateOrigin
	}

	return normalizeInterAgentToolEventShape(normalized, phase, toolName, fullArgs, output, streamMetadata)
}

func normalizeInterAgentToolEventShape(
	meta *InterAgentToolEvent,
	phase ToolCallPhase,
	toolName string,
	fullArgs string,
	output string,
	streamMetadata map[string]any,
) *InterAgentToolEvent {
	if meta == nil {
		return nil
	}
	args := parseInterAgentJSONMap(fullArgs)
	out := parseInterAgentJSONMap(output)
	normalized := &InterAgentToolEvent{
		Kind:         normalizeInterAgentBranchKind(meta.Kind),
		AgentTypes:   normalizeInterAgentAgentTypes(meta.Kind, meta.AgentTypes, toolName, args, out, streamMetadata),
		Summary:      normalizeInlineString(meta.Summary),
		ThreadKey:    normalizeInlineString(meta.ThreadKey),
		Status:       normalizeInlineString(meta.Status),
		UpdateOrigin: meta.UpdateOrigin,
	}
	if !interAgentToolEventPublishable(normalized, phase) {
		return nil
	}
	return normalized
}

func interAgentToolEventPublishable(meta *InterAgentToolEvent, phase ToolCallPhase) bool {
	if meta == nil {
		return false
	}
	if !isNestedInterAgentKind(meta.Kind) {
		return false
	}
	switch phase {
	case ToolCallStart:
		switch meta.Kind {
		case InterAgentToolEventKindConsult, InterAgentToolEventKindChallenge, InterAgentToolEventKindApproval:
			return len(meta.AgentTypes) > 0
		case InterAgentToolEventKindStore:
			return len(meta.AgentTypes) > 0
		default:
			return false
		}
	case ToolCallComplete:
		switch meta.Kind {
		case InterAgentToolEventKindConsult, InterAgentToolEventKindChallenge, InterAgentToolEventKindApproval, InterAgentToolEventKindStore:
			return len(meta.AgentTypes) > 0
		default:
			return false
		}
	default:
		return false
	}
}

func DeriveInterAgentToolEvent(
	toolName string,
	fullArgs string,
	output string,
	phase ToolCallPhase,
	success bool,
	errorMsg string,
) *InterAgentToolEvent {
	args := parseInterAgentJSONMap(fullArgs)
	out := parseInterAgentJSONMap(output)
	toolName = strings.TrimSpace(toolName)

	switch {
	case isInterAgentResponseToolName(toolName):
		if phase != ToolCallComplete {
			return nil
		}
		return deriveInterAgentOriginUpdate(toolName, args, out, output, success, errorMsg)
	case isInterAgentConsultToolName(toolName, args):
		if phase == ToolCallStart {
			return deriveInterAgentConsultStart(toolName, args)
		}
		return deriveInterAgentConsultCompletion(toolName, args, out, output, success, errorMsg)
	case isInterAgentChallengeToolName(toolName):
		if phase == ToolCallStart {
			return deriveInterAgentChallengeStart(toolName, args)
		}
		return deriveInterAgentChallengeCompletion(toolName, args, out, success, errorMsg)
	default:
		return nil
	}
}

func deriveInterAgentConsultStart(toolName string, args map[string]any) *InterAgentToolEvent {
	targets := interAgentConsultationTargets(toolName, args)
	if len(targets) == 0 {
		return nil
	}
	summary := firstNonEmptyInline(
		stringFromAnyMap(args, "question"),
		stringFromAnyMap(args, "query"),
		stringFromAnyMap(args, "description"),
		stringFromAnyMap(args, "approach"),
	)
	if summary == "" && toolName == "consult" && strings.TrimSpace(stringFromAnyMap(args, "mode")) == "pre_planning" {
		summary = "pre-planning consultation gate"
	}
	return &InterAgentToolEvent{
		Kind:       InterAgentToolEventKindConsult,
		AgentTypes: normalizeAgentTypeList(targets),
		Summary:    summary,
		Status:     InterAgentToolEventStatusPending,
	}
}

func deriveInterAgentConsultCompletion(toolName string, args, output map[string]any, rawOutput string, success bool, errorMsg string) *InterAgentToolEvent {
	targets := interAgentConsultationTargets(toolName, args)
	if outTarget := stringFromAnyMap(output, "target"); outTarget != "" {
		targets = []string{outTarget}
	}
	if len(targets) == 0 {
		return nil
	}
	summary := firstNonEmptyInline(
		consultationResponseSummary(output, rawOutput),
		errorMsg,
		stringFromAnyMap(args, "description"),
		stringFromAnyMap(args, "query"),
		stringFromAnyMap(args, "approach"),
	)
	status := InterAgentToolEventStatusDone
	if interAgentConsultationFailed(success, output, errorMsg) {
		status = InterAgentToolEventStatusFailed
	}
	return &InterAgentToolEvent{
		Kind:       InterAgentToolEventKindConsult,
		AgentTypes: normalizeAgentTypeList(targets),
		Summary:    summary,
		Status:     status,
	}
}

func deriveInterAgentChallengeStart(toolName string, args map[string]any) *InterAgentToolEvent {
	targets := interAgentChallengeTargets(toolName, args, nil)
	if len(targets) == 0 {
		return nil
	}
	return &InterAgentToolEvent{
		Kind:       InterAgentToolEventKindChallenge,
		AgentTypes: normalizeAgentTypeList(targets),
		Summary: firstNonEmptyInline(
			stringFromAnyMap(args, "request"),
			stringFromAnyMap(args, "reason"),
		),
		Status: InterAgentToolEventStatusPending,
	}
}

func deriveInterAgentChallengeCompletion(toolName string, args, output map[string]any, success bool, errorMsg string) *InterAgentToolEvent {
	targets := interAgentChallengeTargets(toolName, args, output)
	if len(targets) == 0 {
		return nil
	}
	status := InterAgentToolEventStatusPending
	summary := firstNonEmptyInline(
		stringFromAnyMap(args, "request"),
		stringFromAnyMap(args, "reason"),
	)
	if !success || strings.TrimSpace(errorMsg) != "" {
		status = InterAgentToolEventStatusFailed
		summary = firstNonEmptyInline(errorMsg, summary)
	}
	return &InterAgentToolEvent{
		Kind:       InterAgentToolEventKindChallenge,
		AgentTypes: normalizeAgentTypeList(targets),
		Summary:    summary,
		ThreadKey:  interAgentChallengeThreadKey(toolName, args, output),
		Status:     status,
	}
}

func deriveInterAgentOriginUpdate(toolName string, args, output map[string]any, rawOutput string, success bool, errorMsg string) *InterAgentToolEvent {
	challengeID := firstNonEmptyInline(
		stringFromAnyMap(output, "challenge_id"),
		stringFromAnyMap(args, "challenge_id"),
	)
	if challengeID == "" {
		return nil
	}

	agentTypes := []string{firstNonEmptyInline(
		stringFromAnyMap(output, "responding_agent"),
	)}
	if toolName == "process_global_validation" || toolName == "process_validation" {
		if agent := firstNonEmptyInline(
			stringFromAnyMap(output, "agent_type"),
			stringFromAnyMap(args, "agent_type"),
		); agent != "" {
			agentTypes = []string{agent}
		}
	}

	summary := firstNonEmptyInline(
		stringFromAnyMap(args, "summary"),
		stringFromAnyMap(output, "summary"),
		stringFromAnyMap(output, "decision"),
		stringFromAnyMap(args, "decision"),
		normalizeInlineString(rawOutput),
	)

	var status string
	switch toolName {
	case "validate_global_review", "validate_work":
		status = validationStatusToInterAgentEventStatus(
			firstNonEmptyInline(stringFromAnyMap(output, "status"), stringFromAnyMap(args, "status")),
			success,
			errorMsg,
		)
	default:
		status = validationDecisionToInterAgentEventStatus(
			firstNonEmptyInline(stringFromAnyMap(output, "decision"), stringFromAnyMap(args, "decision")),
			success,
			errorMsg,
		)
	}

	threadKey := interAgentResponseThreadKey(toolName, args, output, challengeID)
	return &InterAgentToolEvent{
		Kind:         InterAgentToolEventKindChallenge,
		AgentTypes:   normalizeAgentTypeList(agentTypes),
		Summary:      summary,
		ThreadKey:    threadKey,
		Status:       status,
		UpdateOrigin: true,
	}
}

func interAgentResponseThreadKey(toolName string, args, output map[string]any, challengeID string) string {
	if explicit := firstNonEmptyInline(
		stringFromAnyMap(output, "thread_key"),
		stringFromAnyMap(args, "thread_key"),
	); explicit != "" {
		return explicit
	}
	scope := firstNonEmptyInline(
		stringFromAnyMap(output, "protocol_scope"),
		stringFromAnyMap(args, "protocol_scope"),
	)
	switch strings.TrimSpace(scope) {
	case globalReviewNamespace:
		return globalReviewThreadPrefix + challengeID
	case pipelineProtocolNamespace:
		return pipelineThreadPrefix + challengeID
	}
	if toolName == "validate_work" || toolName == "process_validation" {
		return pipelineThreadPrefix + challengeID
	}
	return globalReviewThreadPrefix + challengeID
}

func isInterAgentConsultToolName(toolName string, args map[string]any) bool {
	if strings.HasPrefix(toolName, "consult_") {
		return true
	}
	switch toolName {
	case "consult", "request_architect_research", "validate_approach":
		return true
	default:
		return false
	}
}

func isInterAgentChallengeToolName(toolName string) bool {
	return toolName == "challenge_agent" || strings.HasPrefix(toolName, "challenge_")
}

func isNestedInterAgentKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case InterAgentToolEventKindConsult, InterAgentToolEventKindChallenge, InterAgentToolEventKindApproval, InterAgentToolEventKindStore:
		return true
	default:
		return false
	}
}

func isInterAgentResponseToolName(toolName string) bool {
	switch toolName {
	case "validate_global_review", "process_global_validation", "validate_work", "process_validation":
		return true
	default:
		return false
	}
}

func interAgentConsultationTargets(toolName string, args map[string]any) []string {
	if target := stringFromAnyMap(args, "target"); target != "" {
		return []string{target}
	}
	switch toolName {
	case "request_architect_research":
		return []string{"architect"}
	case "validate_approach":
		return []string{"librarian"}
	case "consult":
		if strings.TrimSpace(stringFromAnyMap(args, "mode")) != "pre_planning" {
			return nil
		}
		targets := []string{"librarian", "archivalist"}
		if boolFromAnyMap(args, "include_academic") {
			targets = append(targets, "academic")
		}
		return targets
	default:
		if target := firstKnownAgentInName(strings.TrimPrefix(toolName, "consult_")); target != "" {
			return []string{target}
		}
		return nil
	}
}

func interAgentChallengeTargets(toolName string, args, output map[string]any) []string {
	if targets := stringSliceFromAnyMap(args, "target_agents"); len(targets) > 0 {
		return targets
	}
	if targets := stringSliceFromAnyMap(output, "target_agents"); len(targets) > 0 {
		return targets
	}
	if target := firstNonEmptyInline(
		stringFromAnyMap(args, "target_agent"),
		stringFromAnyMap(output, "target_agent"),
	); target != "" {
		return []string{target}
	}
	if toolName == "challenge_agent" {
		return nil
	}
	target := strings.TrimPrefix(toolName, "challenge_")
	target = strings.TrimPrefix(target, "global_")
	if resolved := firstKnownAgentInName(target); resolved != "" {
		return []string{resolved}
	}
	return nil
}

func normalizeInterAgentAgentTypes(
	kind string,
	values []string,
	toolName string,
	args, output, streamMetadata map[string]any,
) []string {
	normalized := normalizeAgentTypeList(values)
	if strings.TrimSpace(kind) != InterAgentToolEventKindChallenge || len(normalized) == 0 {
		return normalized
	}
	if interAgentProtocolScope(toolName, args, output, streamMetadata) != pipelineProtocolNamespace {
		return normalized
	}
	out := make([]string, 0, len(normalized))
	for _, value := range normalized {
		out = append(out, normalizePipelineChallengeAgentType(value))
	}
	return normalizeAgentTypeList(out)
}

func interAgentProtocolScope(toolName string, args, output, streamMetadata map[string]any) string {
	if scope := firstNonEmptyInline(
		stringFromAnyMap(output, "protocol_scope"),
		stringFromAnyMap(args, "protocol_scope"),
		stringFromAnyMap(streamMetadata, "protocol_scope"),
	); scope != "" {
		return scope
	}
	if strings.TrimSpace(stringFromAnyMap(streamMetadata, "pipeline_id")) != "" {
		return pipelineProtocolNamespace
	}
	switch normalizeAgentType(stringFromAnyMap(streamMetadata, "agent_type")) {
	case "inspector-pipeline", "tester-pipeline", "engineer", "designer":
		return pipelineProtocolNamespace
	case "inspector", "tester":
		return globalReviewNamespace
	}
	if strings.TrimSpace(toolName) == "challenge_agent" {
		if strings.TrimSpace(stringFromAnyMap(output, "challenge_id")) != "" {
			return pipelineProtocolNamespace
		}
	}
	return ""
}

func normalizePipelineChallengeAgentType(value string) string {
	switch normalizeAgentType(value) {
	case "inspector":
		return "inspector-pipeline"
	case "tester":
		return "tester-pipeline"
	default:
		return normalizeAgentType(value)
	}
}

func interAgentChallengeThreadKey(toolName string, args, output map[string]any) string {
	challengeID := firstNonEmptyInline(
		stringFromAnyMap(output, "challenge_id"),
		stringFromAnyMap(args, "challenge_id"),
	)
	if challengeID == "" {
		return ""
	}
	if toolName == "challenge_agent" {
		return pipelineThreadPrefix + challengeID
	}
	return globalReviewThreadPrefix + challengeID
}

func interAgentConsultationFailed(success bool, output map[string]any, errorMsg string) bool {
	if !success || strings.TrimSpace(errorMsg) != "" {
		return true
	}
	switch strings.TrimSpace(stringFromAnyMap(output, "status")) {
	case "", "ok", "success", "ready":
		return false
	default:
		return false
	}
}

func consultationResponseSummary(output map[string]any, rawOutput string) string {
	if output == nil {
		return SummarizeInterAgentPayload(rawOutput)
	}
	if ready, ok := output["ready"].(bool); ok && ready {
		if reused, _ := output["reused"].(bool); reused {
			return "consultation gate reused"
		}
		return "consultation gate satisfied"
	}
	if summary := normalizeInlineString(SummarizeInterAgentPayload(output)); summary != "" {
		return summary
	}
	if requested, ok := output["requested"].(bool); ok && requested {
		if value := normalizeInlineString(stringFromAnyMap(output, "description")); value != "" {
			return value
		}
	}
	return normalizeInlineString(rawOutput)
}

func validationStatusToInterAgentEventStatus(status string, success bool, errorMsg string) string {
	if !success || strings.TrimSpace(errorMsg) != "" {
		return InterAgentToolEventStatusFailed
	}
	switch strings.TrimSpace(status) {
	case "passed", "partial":
		return InterAgentToolEventStatusDone
	default:
		return InterAgentToolEventStatusFailed
	}
}

func validationDecisionToInterAgentEventStatus(decision string, success bool, errorMsg string) string {
	if !success || strings.TrimSpace(errorMsg) != "" {
		return InterAgentToolEventStatusFailed
	}
	switch strings.TrimSpace(decision) {
	case "accept", "handoff":
		return InterAgentToolEventStatusDone
	default:
		return InterAgentToolEventStatusFailed
	}
}

func parseInterAgentJSONMap(raw string) map[string]any {
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

func stringFromAnyMap(m map[string]any, key string) string {
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
		return strings.TrimSpace(anyValueSummary(typed))
	}
}

func boolFromAnyMap(m map[string]any, key string) bool {
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

func stringSliceFromAnyMap(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	value, ok := m[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return normalizeAgentTypeList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return normalizeAgentTypeList(out)
	default:
		return nil
	}
}

func anyValueSummary(value any) string {
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

func normalizeAgentTypeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeAgentType(value)
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

func normalizeAgentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "global_")
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "inspector":
		return "inspector"
	case "tester":
		return "tester"
	case "architect":
		return "architect"
	case "orchestrator":
		return "orchestrator"
	case "librarian":
		return "librarian"
	case "archivalist":
		return "archivalist"
	case "academic":
		return "academic"
	case "engineer":
		return "engineer"
	case "designer":
		return "designer"
	case "inspector-pipeline":
		return "inspector-pipeline"
	case "tester-pipeline":
		return "tester-pipeline"
	case "guide":
		return "guide"
	default:
		return value
	}
}

func firstKnownAgentInName(name string) string {
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

func normalizeInlineString(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

func firstNonEmptyInline(values ...string) string {
	for _, value := range values {
		if trimmed := normalizeInlineString(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
