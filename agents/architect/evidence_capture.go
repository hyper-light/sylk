package architect

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
)

const (
	consultPeerToolName          = "consult_peer"
	defaultEvidenceSummaryMaxLen = 600
)

func registerArchitectEvidenceCaptureHook(a *Architect) {
	if a == nil || a.hooks == nil {
		return
	}
	a.hooks.RegisterPostToolCallHook(
		"architect_consult_peer_evidence_capture",
		skills.HookPriorityLow,
		func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
			if data == nil || strings.TrimSpace(data.ToolName) != consultPeerToolName {
				return skills.HookResult{Continue: true}
			}
			if err := a.captureConsultPeerEvidence(ctx, data); err != nil {
				a.logWarn("consult_peer evidence capture skipped", "error", err)
			}
			return skills.HookResult{Continue: true}
		},
	)
}

func (a *Architect) runContinuationPostToolHooks(ctx context.Context, snapshot *shared.TurnSnapshot, output string) {
	if a == nil || a.hooks == nil || snapshot == nil {
		return
	}
	toolName, toolID, rawArgs := continuationToolCall(snapshot)
	if strings.TrimSpace(toolName) == "" {
		toolName = strings.TrimSpace(snapshot.AwaitToolName)
	}
	if strings.TrimSpace(toolName) == "" {
		return
	}
	input := map[string]any{}
	if strings.TrimSpace(rawArgs) != "" {
		_ = json.Unmarshal([]byte(rawArgs), &input)
	}
	if input == nil {
		input = map[string]any{}
	}
	_, _, _ = a.hooks.ExecutePostToolCallHooks(ctx, &skills.ToolCallHookData{
		ToolName: strings.TrimSpace(toolName),
		ToolID:   strings.TrimSpace(toolID),
		AgentID:  a.id,
		Input:    input,
		Output:   output,
		Metadata: map[string]any{
			"raw_json":       rawArgs,
			"correlation_id": snapshot.CorrelationID,
			"session_id":     snapshot.SessionID,
		},
	})
}

func continuationToolCall(snapshot *shared.TurnSnapshot) (toolName, toolID, rawArgs string) {
	if snapshot == nil || snapshot.Request == nil {
		return "", "", ""
	}
	want := strings.TrimSpace(snapshot.AwaitToolCallID)
	for i := len(snapshot.Request.Messages) - 1; i >= 0; i-- {
		msg := snapshot.Request.Messages[i]
		for j := range msg.ToolCalls {
			call := msg.ToolCalls[j]
			if strings.TrimSpace(call.ID) == want {
				return call.Name, call.ID, call.Arguments
			}
		}
	}
	return strings.TrimSpace(snapshot.AwaitToolName), want, ""
}

func (a *Architect) captureConsultPeerEvidence(ctx context.Context, data *skills.ToolCallHookData) error {
	target := strings.ToLower(strings.TrimSpace(stringFromAny(data.Input["target_agent_type"])))
	query := strings.TrimSpace(stringFromAny(data.Input["query"]))
	if target == "" || query == "" {
		return fmt.Errorf("consult_peer evidence missing target or query")
	}
	scope := strings.TrimSpace(stringFromAny(data.Input["scope"]))
	planID := strings.TrimSpace(stringFromAny(data.Input["plan_id"]))
	now := time.Now()

	outputs := consultOutputsFromToolOutput(data.Output)
	if len(outputs) == 0 {
		outputs = []capturedConsultOutput{{
			Status:   "completed",
			Response: data.Output,
		}}
	}
	for _, out := range outputs {
		success := data.Error == nil && !strings.EqualFold(out.Status, "error") &&
			!strings.EqualFold(out.Status, "timeout") &&
			!strings.EqualFold(out.Status, "cancelled")
		errText := strings.TrimSpace(out.Error)
		if data.Error != nil && errText == "" {
			errText = data.Error.Error()
		}
		summary := firstNonEmptyString(
			out.Summary,
			consultSummaryFromResponse(out.Response),
			consultSummaryFromArtifacts(out.Artifacts),
		)
		ev := &PlanEvidence{
			Kind:             EvidenceKindConsult,
			PlanID:           planID,
			Target:           target,
			Query:            query,
			Scope:            scope,
			Correlation:      firstNonEmptyString(out.ConsultID, correlationIDFromHookData(data), originalCIDFromContext(ctx), shared.LogMetaFromContext(ctx).CorrID),
			Success:          success,
			Data:             normalizeConsultEvidenceData(target, summary, out.Response, out.Artifacts),
			Summary:          truncatePlannerEvidenceString(summary, defaultEvidenceSummaryMaxLen),
			Error:            errText,
			RequestedAt:      now,
			ReceivedAt:       now,
			SourceTool:       consultPeerToolName,
			SourceArtifactID: strings.TrimSpace(out.ConsultID),
		}
		ev.ID = planEvidenceID(ev)
		a.attachPlanEvidence(ctx, ev)
	}
	return nil
}

type capturedConsultOutput struct {
	ConsultID string
	Status    string
	Response  any
	Summary   string
	Error     string
	Artifacts []map[string]any
}

func consultOutputsFromToolOutput(output any) []capturedConsultOutput {
	decoded := decodeConsultOutput(output)
	if decoded == nil {
		return nil
	}
	if m, ok := decoded.(map[string]any); ok {
		if results, ok := asMap(m["results"]); ok {
			keys := make([]string, 0, len(results))
			for key := range results {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			out := make([]capturedConsultOutput, 0, len(keys))
			for _, key := range keys {
				entry, _ := asMap(results[key])
				if entry == nil {
					out = append(out, capturedConsultOutput{ConsultID: key, Status: "error", Error: "malformed consult result"})
					continue
				}
				response := entry["response"]
				artifacts := artifactsFromAny(response)
				out = append(out, capturedConsultOutput{
					ConsultID: strings.TrimSpace(key),
					Status:    firstNonEmptyString(stringFromAny(entry["status"]), "completed"),
					Response:  response,
					Summary:   stringFromAny(entry["summary"]),
					Error:     stringFromAny(entry["error"]),
					Artifacts: artifacts,
				})
			}
			return out
		}
		response := m["response"]
		artifacts := artifactsFromAny(response)
		return []capturedConsultOutput{{
			ConsultID: stringFromAny(m["consult_id"]),
			Status:    firstNonEmptyString(stringFromAny(m["status"]), "completed"),
			Response:  response,
			Summary:   firstNonEmptyString(stringFromAny(m["summary"]), consultSummaryFromResponse(response)),
			Error:     stringFromAny(m["error"]),
			Artifacts: artifacts,
		}}
	}
	return []capturedConsultOutput{{Status: "completed", Response: decoded, Summary: consultSummaryFromResponse(decoded)}}
}

func decodeConsultOutput(output any) any {
	switch typed := output.(type) {
	case nil:
		return nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		var decoded any
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			return decoded
		}
		return trimmed
	case []byte:
		var decoded any
		if json.Unmarshal(typed, &decoded) == nil {
			return decoded
		}
		return string(typed)
	default:
		return typed
	}
}

func normalizeConsultEvidenceData(target, summary string, response any, artifacts []map[string]any) map[string]any {
	data := map[string]any{}
	if m, ok := asMap(response); ok {
		for key, value := range m {
			data[key] = value
		}
	}
	if response != nil {
		data["response"] = response
	}
	if summary = strings.TrimSpace(summary); summary != "" {
		data["summary"] = summary
	}
	if len(artifacts) > 0 {
		items := make([]any, 0, len(artifacts))
		for _, artifact := range artifacts {
			items = append(items, artifact)
		}
		data["artifacts"] = items
	}
	if strings.EqualFold(strings.TrimSpace(target), "librarian") {
		addLibrarianPatternPayload(data, summary, response, artifacts)
	}
	return data
}

func addLibrarianPatternPayload(data map[string]any, summary string, response any, artifacts []map[string]any) {
	files := extractEvidenceFileRefs(summary, response, artifacts)
	if len(files) > 0 {
		fileValues := make([]any, 0, len(files))
		for _, file := range files {
			fileValues = append(fileValues, file)
		}
		data["relevant_files"] = fileValues
	}
	if _, exists := data["patterns"]; exists {
		return
	}
	description := truncatePlannerEvidenceString(firstNonEmptyString(summary, consultSummaryFromResponse(response), "Librarian consultation evidence"), 240)
	pattern := map[string]any{
		"name":        "librarian consultation",
		"description": description,
		"category":    "consultation",
		"confidence":  1.0,
	}
	if len(files) > 0 {
		pattern["file_path"] = files[0]
	}
	data["patterns"] = []any{pattern}
}

func extractEvidenceFileRefs(summary string, response any, artifacts []map[string]any) []string {
	var chunks []string
	if summary != "" {
		chunks = append(chunks, summary)
	}
	if response != nil {
		if encoded, err := json.Marshal(response); err == nil {
			chunks = append(chunks, string(encoded))
		}
	}
	for _, artifact := range artifacts {
		chunks = append(chunks, stringFromAny(artifact["reference"]))
		if md, ok := asMap(artifact["metadata"]); ok {
			for _, value := range md {
				if s := stringFromAny(value); s != "" {
					chunks = append(chunks, s)
				}
			}
		}
	}
	seen := map[string]struct{}{}
	var files []string
	for _, chunk := range chunks {
		for _, token := range strings.Fields(chunk) {
			candidate := normalizeEvidenceFileToken(token)
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			files = append(files, candidate)
		}
	}
	sort.Strings(files)
	return files
}

func normalizeEvidenceFileToken(token string) string {
	token = strings.Trim(token, "`'\".,;:()[]{}<>")
	if idx := strings.Index(token, ":"); idx > 0 {
		token = token[:idx]
	}
	token = strings.TrimPrefix(token, "./")
	if token == "" || strings.HasPrefix(token, "/") || strings.Contains(token, "://") {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(token))
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".md", ".json", ".yaml", ".yml", ".sql", ".css", ".scss", ".html", ".sh":
	default:
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(token))
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return ""
	}
	return cleaned
}

func (a *Architect) attachPlanEvidence(ctx context.Context, evidence *PlanEvidence) {
	if a == nil || evidence == nil {
		return
	}
	plan := a.resolvePlanForEvidence(ctx, evidence)
	if plan == nil {
		a.stagePlanEvidence(ctx, evidence)
		return
	}
	a.evidenceMu.Lock()
	changed := appendPlanEvidence(plan, evidence)
	if changed {
		plan.CodebasePatterns = extractLibrarianPatterns(plan)
		plan.UpdatedAt = time.Now()
	}
	a.evidenceMu.Unlock()
	if changed {
		if err := a.persistPlanState(plan); err != nil {
			a.logWarn("persist consultation evidence failed", "plan_id", plan.ID, "error", err)
		}
	}
}

func (a *Architect) attachPlanInputEvidence(ctx context.Context, plan *DesignPlan, evidence []*PlanEvidence) {
	if a == nil || len(evidence) == 0 {
		return
	}
	if plan == nil {
		for _, ev := range evidence {
			a.attachPlanEvidence(ctx, ev)
		}
		return
	}
	a.evidenceMu.Lock()
	changed := false
	for _, ev := range evidence {
		if ev == nil {
			continue
		}
		if ev.Kind == "" {
			ev.Kind = EvidenceKindConsult
		}
		if appendPlanEvidence(plan, ev) {
			changed = true
		}
	}
	if changed {
		plan.CodebasePatterns = extractLibrarianPatterns(plan)
		plan.UpdatedAt = time.Now()
	}
	a.evidenceMu.Unlock()
	if changed {
		if err := a.persistPlanState(plan); err != nil {
			a.logWarn("persist input evidence failed", "plan_id", plan.ID, "error", err)
		}
	}
}

func (a *Architect) resolvePlanForEvidence(ctx context.Context, evidence *PlanEvidence) *DesignPlan {
	if a == nil || a.planStore == nil || evidence == nil {
		return nil
	}
	if planID := strings.TrimSpace(evidence.PlanID); planID != "" {
		if plan := a.planStore.Get(planID); plan != nil {
			return plan
		}
	}
	sessionID := firstNonEmptyString(architectSessionIDFromContext(ctx), shared.LogMetaFromContext(ctx).SessionID, a.config.SessionID)
	correlationID := firstNonEmptyString(originalCIDFromContext(ctx), shared.LogMetaFromContext(ctx).CorrID, evidence.Correlation)
	if sessionID != "" && correlationID != "" {
		if plan := a.reusablePlanForRequest(sessionID, correlationID); plan != nil {
			return plan
		}
	}
	if sessionID != "" {
		return a.latestReusablePlanForSession(sessionID)
	}
	return nil
}

func (a *Architect) latestReusablePlanForSession(sessionID string) *DesignPlan {
	if a == nil || a.planStore == nil {
		return nil
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	var best *DesignPlan
	for _, plan := range a.planStore.Snapshot() {
		if plan == nil || !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if !isReusablePlanningState(plan.SM().State()) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

func (a *Architect) stagePlanEvidence(ctx context.Context, evidence *PlanEvidence) {
	if a == nil || evidence == nil {
		return
	}
	sessionID := firstNonEmptyString(architectSessionIDFromContext(ctx), shared.LogMetaFromContext(ctx).SessionID, a.config.SessionID)
	correlationID := firstNonEmptyString(originalCIDFromContext(ctx), shared.LogMetaFromContext(ctx).CorrID, evidence.Correlation)
	key := stagedEvidenceKey(sessionID, correlationID)
	if key == "" {
		return
	}
	a.evidenceMu.Lock()
	if a.stagedEvidence == nil {
		a.stagedEvidence = make(map[string][]*PlanEvidence)
	}
	a.stagedEvidence[key] = appendDedupedEvidence(a.stagedEvidence[key], evidence)
	a.evidenceMu.Unlock()
}

func (a *Architect) drainStagedEvidenceIntoPlan(plan *DesignPlan) {
	if a == nil || plan == nil {
		return
	}
	keys := []string{
		stagedEvidenceKey(plan.SessionID, plan.RequestCorrelationID),
		stagedEvidenceKey(plan.SessionID, ""),
	}
	var staged []*PlanEvidence
	a.evidenceMu.Lock()
	for _, key := range keys {
		if key == "" {
			continue
		}
		staged = append(staged, a.stagedEvidence[key]...)
		delete(a.stagedEvidence, key)
	}
	for _, evidence := range staged {
		appendPlanEvidence(plan, evidence)
	}
	if len(staged) > 0 {
		plan.CodebasePatterns = extractLibrarianPatterns(plan)
		plan.UpdatedAt = time.Now()
	}
	a.evidenceMu.Unlock()
}

func stagedEvidenceKey(sessionID, correlationID string) string {
	sessionID = strings.TrimSpace(sessionID)
	correlationID = strings.TrimSpace(correlationID)
	if sessionID == "" {
		return ""
	}
	if correlationID == "" {
		return sessionID + "\x00session"
	}
	return sessionID + "\x00" + correlationID
}

func appendPlanEvidence(plan *DesignPlan, evidence *PlanEvidence) bool {
	if plan == nil || evidence == nil {
		return false
	}
	normalized := clonePlanEvidence(evidence)
	if normalized.ID == "" {
		normalized.ID = planEvidenceID(normalized)
	}
	for _, existing := range plan.EvidenceTrail {
		if existing != nil && strings.TrimSpace(existing.ID) == normalized.ID {
			syncConsultationIndexFromTrail(plan)
			return false
		}
	}
	plan.EvidenceTrail = append(plan.EvidenceTrail, normalized)
	syncConsultationIndexFromTrail(plan)
	return true
}

func appendDedupedEvidence(list []*PlanEvidence, evidence *PlanEvidence) []*PlanEvidence {
	if evidence == nil {
		return list
	}
	id := evidence.ID
	if id == "" {
		id = planEvidenceID(evidence)
	}
	for _, existing := range list {
		if existing != nil && existing.ID == id {
			return list
		}
	}
	return append(list, clonePlanEvidence(evidence))
}

func syncConsultationIndexFromTrail(plan *DesignPlan) {
	if plan == nil {
		return
	}
	if len(plan.EvidenceTrail) == 0 {
		if plan.Consultations == nil {
			plan.Consultations = map[string]*ConsultationEvidence{}
		}
		return
	}
	index := map[string]*ConsultationEvidence{}
	for _, evidence := range plan.EvidenceTrail {
		if evidence == nil || evidence.Kind != EvidenceKindConsult {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(evidence.Target))
		if target == "" {
			continue
		}
		index[target] = consultationEvidenceFromPlanEvidence(evidence)
	}
	plan.Consultations = index
}

func consultationEvidenceFromPlanEvidence(evidence *PlanEvidence) *ConsultationEvidence {
	if evidence == nil {
		return nil
	}
	return &ConsultationEvidence{
		Target:      strings.ToLower(strings.TrimSpace(evidence.Target)),
		Query:       strings.TrimSpace(evidence.Query),
		Scope:       strings.TrimSpace(evidence.Scope),
		Correlation: strings.TrimSpace(evidence.Correlation),
		Success:     evidence.Success,
		Data:        evidence.Data,
		Error:       evidence.Error,
		RequestedAt: evidence.RequestedAt,
		ReceivedAt:  evidence.ReceivedAt,
	}
}

func clonePlanEvidence(evidence *PlanEvidence) *PlanEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	cloned.Target = strings.ToLower(strings.TrimSpace(cloned.Target))
	cloned.Query = strings.TrimSpace(cloned.Query)
	cloned.Scope = strings.TrimSpace(cloned.Scope)
	cloned.Correlation = strings.TrimSpace(cloned.Correlation)
	cloned.SourceTool = strings.TrimSpace(cloned.SourceTool)
	cloned.SourceArtifactID = strings.TrimSpace(cloned.SourceArtifactID)
	if cloned.RequestedAt.IsZero() {
		cloned.RequestedAt = time.Now()
	}
	if cloned.ReceivedAt.IsZero() {
		cloned.ReceivedAt = cloned.RequestedAt
	}
	return &cloned
}

func planEvidenceID(evidence *PlanEvidence) string {
	if evidence == nil {
		return ""
	}
	if id := strings.TrimSpace(evidence.ID); id != "" {
		return id
	}
	if source := strings.TrimSpace(evidence.SourceArtifactID); source != "" {
		return string(evidence.Kind) + ":" + source
	}
	sum := sha1.Sum([]byte(strings.Join([]string{
		string(evidence.Kind),
		strings.ToLower(strings.TrimSpace(evidence.Target)),
		strings.TrimSpace(evidence.Query),
		strings.TrimSpace(evidence.Scope),
		strings.TrimSpace(evidence.Summary),
	}, "\x00")))
	return string(evidence.Kind) + ":" + hex.EncodeToString(sum[:])[:16]
}

func correlationIDFromHookData(data *skills.ToolCallHookData) string {
	if data == nil || data.Metadata == nil {
		return ""
	}
	return stringFromAny(data.Metadata["correlation_id"])
}

func consultSummaryFromResponse(response any) string {
	switch typed := response.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"summary", "response", "answer", "recommendation"} {
			if text := stringFromAny(typed[key]); strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	if encoded, err := json.Marshal(response); err == nil {
		return truncatePlannerEvidenceString(string(encoded), defaultEvidenceSummaryMaxLen)
	}
	return ""
}

func consultSummaryFromArtifacts(artifacts []map[string]any) string {
	for _, artifact := range artifacts {
		if text := stringFromAny(artifact["reference"]); strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func artifactsFromAny(value any) []map[string]any {
	m, ok := asMap(value)
	if !ok {
		return nil
	}
	raw, ok := m["artifacts"]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if artifact, ok := asMap(item); ok {
			out = append(out, artifact)
		}
	}
	return out
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncatePlannerEvidenceString(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max-3] + "..."
}
