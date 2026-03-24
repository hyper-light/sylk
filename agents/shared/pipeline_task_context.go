package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

type pipelineTaskContextKey struct{}

// PipelineTaskInput is the shared wire shape used by orchestrator pipeline
// dispatches. It is intentionally duplicated here so pipeline workers can
// decode task payloads without importing the orchestrator package.
type PipelineTaskInput struct {
	NodeID        string         `json:"node_id"`
	DAGID         string         `json:"dag_id"`
	TaskID        string         `json:"task_id"`
	AgentType     string         `json:"agent_type"`
	TargetAgentID string         `json:"target_agent_id,omitempty"`
	Prompt        string         `json:"prompt"`
	Context       map[string]any `json:"context,omitempty"`
	ParentResults map[string]any `json:"parent_results,omitempty"`
	SessionID     string         `json:"session_id"`
}

func WithPipelineTask(ctx context.Context, task *PipelineTaskInput) context.Context {
	if ctx == nil || task == nil {
		return ctx
	}
	return context.WithValue(ctx, pipelineTaskContextKey{}, task)
}

func PipelineTaskFromContext(ctx context.Context) *PipelineTaskInput {
	if ctx == nil {
		return nil
	}
	task, _ := ctx.Value(pipelineTaskContextKey{}).(*PipelineTaskInput)
	return task
}

// DecodePipelineTaskInput parses a JSON-encoded orchestrator pipeline task.
// Returns nil when input is not a structured pipeline task payload.
func DecodePipelineTaskInput(input string) *PipelineTaskInput {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var task PipelineTaskInput
	if err := json.Unmarshal([]byte(trimmed), &task); err != nil {
		return nil
	}
	if strings.TrimSpace(task.NodeID) == "" || strings.TrimSpace(task.AgentType) == "" {
		return nil
	}
	return &task
}

// AppendPipelineSystemContext appends bounded big-picture and task-intent
// context to an agent's base system prompt. The task contract still governs
// scope; this section is for better local decisions, not scope expansion.
func AppendPipelineSystemContext(base string, task *PipelineTaskInput) string {
	if task == nil {
		return base
	}
	section := BuildPipelineSystemContext(task)
	if section == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return section
	}
	return base + "\n\n---\n\n" + section
}

// BuildPipelineSystemContext formats bounded execution context for pipeline
// worker system prompts.
func BuildPipelineSystemContext(task *PipelineTaskInput) string {
	if task == nil || len(task.Context) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Execution Context\n\n")
	b.WriteString("Use the big picture to make better local decisions, but do not expand scope beyond this task, its contract, and its allowed files.\n")
	b.WriteString("\n")
	b.WriteString(BuildWorkspaceViewContext(WorkspacePromptOptions{
		DefaultView:     versioning.WorkspaceViewPipeline,
		IncludePipeline: true,
	}))

	if bigPicture := decodeMap(task.Context, "big_picture"); len(bigPicture) > 0 {
		b.WriteString("\n## Big Picture\n")
		writeLabeledScalar(&b, "Goal", bigPicture["goal"])
		writeLabeledScalar(&b, "Architecture", bigPicture["architecture_summary"])
		writeListSection(&b, "Requirements", decodeAnyStringList(bigPicture["requirements"]))
		writeListSection(&b, "Constraints", decodeAnyStringList(bigPicture["constraints"]))
		writeListSection(&b, "Risk Summary", decodeAnyStringList(bigPicture["risk_summary"]))
		writeListSection(&b, "Assumptions", decodeAnyStringList(bigPicture["assumptions"]))
		writeListSection(&b, "Critical Path", decodeAnyStringList(bigPicture["critical_path"]))
	}

	if taskIntent := decodeMap(task.Context, "task_intent"); len(taskIntent) > 0 {
		b.WriteString("\n## Task Intent\n")
		writeLabeledScalar(&b, "Task", taskIntent["task_name"])
		writeLabeledScalar(&b, "Why This Exists", taskIntent["why_this_task_exists"])
		writeLabeledScalar(&b, "User Outcome", taskIntent["user_visible_outcome"])
		writeLabeledScalar(&b, "Architectural Role", taskIntent["architectural_role"])
		writeListSection(&b, "Upstream Inputs", decodeAnyStringList(taskIntent["upstream_inputs"]))
		writeListSection(&b, "Downstream Dependents", decodeAnyStringList(taskIntent["downstream_dependents"]))
	}

	writeListSection(&b, "Affected Files", extractAffectedPaths(task.Context))
	writeListSection(&b, "Workspace Read Set", decodeWorkspacePaths(task.Context, "read_set"))
	writeListSection(&b, "Workspace Write Set", decodeWorkspacePaths(task.Context, "write_set"))
	writeListSection(&b, "Workspace Test Surface", decodeWorkspacePaths(task.Context, "test_surface"))
	writeListSection(&b, "Workspace Prefetch Paths", decodeWorkspacePaths(task.Context, "prefetch_paths"))

	if packet := decodeWorkerPacket(task.Context, PipelineWorkerType(task)); len(packet) > 0 {
		b.WriteString("\n## Worker Packet\n")
		writeLabeledScalar(&b, "Objective", packet["objective"])
		writeListSection(&b, "Responsibilities", decodeAnyStringList(packet["responsibilities"]))
		writeListSection(&b, "Worker Read Set", decodeAnyStringList(packet["read_set"]))
		writeListSection(&b, "Worker Write Set", decodeAnyStringList(packet["write_set"]))
		writeListSection(&b, "Worker Guidelines", decodeAnyStringList(packet["guidelines"]))
		writeListSection(&b, "Worker Test Requirements", decodeAnyStringList(packet["test_requirements"]))
	}
	if coordPacket := decodeMap(task.Context, "coordination_packet"); len(coordPacket) > 0 {
		b.WriteString("\n## Coordination State\n")
		writeLabeledScalar(&b, "Summary", coordPacket["summary"])
		writeListSection(&b, "Required Coordination Protocol", summarizeCoordinationContract(coordPacket["contract"]))
		writeListSection(&b, "Active Claims You Already Hold", summarizeCoordinationClaims(coordPacket["my_claims"]))
		writeListSection(&b, "Peer Claims To Avoid Duplicating", summarizeCoordinationClaims(coordPacket["peer_claims"]))
		writeListSection(&b, "Relevant Existing Artifacts", summarizeCoordinationArtifacts(coordPacket["relevant_artifacts"]))
		writeListSection(&b, "Pending Review Requests", summarizeCoordinationReviews(coordPacket["pending_reviews"]))
		writeListSection(&b, "Historical Coordination Precedents", summarizeCoordinationPrecedents(coordPacket["historical_precedents"]))
		b.WriteString("\nCoordination Rules:\n")
		b.WriteString("- Query and reuse the current coordination state before rediscovering work.\n")
		b.WriteString("- Claim scope before starting duplicative work.\n")
		b.WriteString("- Publish artifacts for decisions, findings, or plans that peers will need.\n")
		b.WriteString("- Request review against a specific artifact when you need another worker's input.\n")
		b.WriteString("- Use coordination watch updates when waiting on peer movement instead of blind polling.\n")
		b.WriteString("- Release or resolve claims when you finish or hand work off.\n")
	}
	if protocol := decodeMap(task.Context, "pipeline_protocol"); len(protocol) > 0 {
		b.WriteString("\n## Pipeline Protocol\n")
		writeListSection(&b, "Pipeline Members", summarizePipelineProtocolMembers(protocol["roster"]))
		writeListSection(&b, "Active Agents", decodeAnyStringList(protocol["active_agents"]))
		writeLabeledScalar(&b, "Requested By", protocol["requested_by"])
		writeLabeledScalar(&b, "Turn Mode", protocol["mode"])
		writeLabeledScalar(&b, "Current Request", protocol["current_request"])
		writeListSection(&b, "Audit Lock", summarizePipelineAuditLock(protocol["audit_lock"]))
		writeListSection(&b, "Pending Challenge", summarizePipelineChallenge(protocol["pending_challenge"]))
		writeListSection(&b, "Pending Validation", summarizePipelineValidation(protocol["pending_validation"]))
		writeListSection(&b, "Recent Protocol Events", summarizePipelineProtocolEvents(protocol["recent_events"]))
		b.WriteString("\nProtocol Rules:\n")
		b.WriteString("- Inspector is the only pipeline entrypoint and the ultimate pipeline exit point; only Inspector may accept work and hand off to OT.\n")
		b.WriteString("- The authoritative loop is inspector -> tester -> engineer/designer -> inspector unless the active challenge explicitly says otherwise.\n")
		b.WriteString("- A first challenge on a directed pipeline-agent pair is allowed.\n")
		b.WriteString("- Repeated challenges require new pipeline VFS evidence on the directed pair: Inspector may re-challenge Tester, Engineer, or Designer only after that target changed VFS since Inspector's previous challenge to that target; Tester, Engineer, and Designer may re-challenge each other only after the target changed VFS since the challenger's previous challenge; Tester, Engineer, and Designer may re-challenge Inspector only after the challenger changed VFS in response to Inspector's previous answer.\n")
		b.WriteString("- End each turn with challenge_agent, handoff_next, validate_work, finalize_pipeline, or handoff_to_ot (inspector only).\n")
		b.WriteString("- Process another agent's validation response before deciding the next handoff.\n")
		b.WriteString("- Engineer and Designer should hand completed implementation turns back to Inspector instead of routing directly to Tester by default.\n")
		b.WriteString("- Engineer and Designer should treat tests as executable specification and challenge unclear criteria or coverage.\n")
		b.WriteString("- Each time Engineer or Designer hands work back, Inspector should invoke finalize_pipeline to run the audit cycle.\n")
		b.WriteString("- If the finalize_pipeline audit passes and tester evidence confirms the required tests are implemented and passing, Inspector must immediately invoke handoff_to_ot instead of starting another audit loop.\n")
		b.WriteString("- Once a pipeline is ready for OT, that completed pipeline takes priority: Inspector must invoke handoff_to_ot before continuing with any other queued work, other pipelines, or other reviews.\n")
		b.WriteString("- While Inspector audit lock is active, challenges to Inspector are refused. If refused, stop work until Inspector challenges you directly or your preceding agent hands off to you.\n")
	}
	return strings.TrimSpace(b.String())
}

// ComposePipelineTaskUserPrompt formats a pipeline task into a readable user
// prompt so workers do not reason over raw JSON.
func ComposePipelineTaskUserPrompt(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}

	var b strings.Builder
	currentRequest := PipelineCurrentRequest(task)
	originalPrompt := strings.TrimSpace(task.Prompt)
	b.WriteString("## Pipeline Task\n\n")
	fmt.Fprintf(&b, "Task ID: %s\n", task.TaskID)
	if slug, _ := task.Context["task_slug"].(string); slug != "" {
		fmt.Fprintf(&b, "Task Slug: %s\n", slug)
	}
	fmt.Fprintf(&b, "Node ID: %s\n", task.NodeID)
	if stage, _ := task.Context["pipeline_stage"].(string); stage != "" {
		fmt.Fprintf(&b, "Stage: %s\n", stage)
	}
	if currentRequest != "" {
		b.WriteString("\n### Current Turn Request\n")
		b.WriteString(currentRequest)
		b.WriteString("\n")
	}
	if originalPrompt != "" && originalPrompt != currentRequest {
		b.WriteString("\n### Original Task Objective\n")
		b.WriteString(originalPrompt)
		b.WriteString("\n")
	}

	writeListSection(&b, "Affected Files", extractAffectedPaths(task.Context))
	writeListSection(&b, "Workspace Read Set", decodeWorkspacePaths(task.Context, "read_set"))
	writeListSection(&b, "Workspace Write Set", decodeWorkspacePaths(task.Context, "write_set"))
	writeListSection(&b, "Workspace Test Surface", decodeWorkspacePaths(task.Context, "test_surface"))
	if packet := decodeWorkerPacket(task.Context, PipelineWorkerType(task)); len(packet) > 0 {
		writeLabeledScalar(&b, "Worker Objective", packet["objective"])
		writeListSection(&b, "Worker Responsibilities", decodeAnyStringList(packet["responsibilities"]))
		writeListSection(&b, "Worker Guidelines", decodeAnyStringList(packet["guidelines"]))
		writeListSection(&b, "Worker Test Requirements", decodeAnyStringList(packet["test_requirements"]))
	}
	writeListSection(&b, "Acceptance Criteria", decodeAcceptanceCriteria(task.Context))
	writeListSection(&b, "Success Criteria", decodeStringList(task.Context, "success_criteria"))
	writeListSection(&b, "Test Requirements", decodeStringList(task.Context, "test_requirements"))
	writeListSection(&b, "Guidelines", decodeStringList(task.Context, "guidelines"))
	writeListSection(&b, "Risk Factors", decodeStringList(task.Context, "risk_factors"))
	writeLabeledScalar(&b, "Implementation Guide", task.Context["implementation_guide"])
	if protocol := decodeMap(task.Context, "pipeline_protocol"); len(protocol) > 0 {
		b.WriteString("\n### Pipeline Protocol\n")
		writeListSection(&b, "Pipeline Members", summarizePipelineProtocolMembers(protocol["roster"]))
		writeListSection(&b, "Active Agents", decodeAnyStringList(protocol["active_agents"]))
		writeLabeledScalar(&b, "Requested By", protocol["requested_by"])
		writeLabeledScalar(&b, "Turn Mode", protocol["mode"])
		writeLabeledScalar(&b, "Current Request", protocol["current_request"])
		writeListSection(&b, "Audit Lock", summarizePipelineAuditLock(protocol["audit_lock"]))
		writeListSection(&b, "Pending Challenge", summarizePipelineChallenge(protocol["pending_challenge"]))
		writeListSection(&b, "Pending Validation", summarizePipelineValidation(protocol["pending_validation"]))
		writeListSection(&b, "Recent Protocol Events", summarizePipelineProtocolEvents(protocol["recent_events"]))
	}

	if len(task.ParentResults) > 0 {
		b.WriteString("\n### Parent Results\n")
		for nodeID, result := range task.ParentResults {
			fmt.Fprintf(&b, "- %s: %v\n", nodeID, result)
		}
	}

	return strings.TrimSpace(b.String())
}

func PipelineCurrentRequest(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	if snapshot, err := PipelineProtocolSnapshotFromTask(task); err == nil && snapshot != nil {
		if current := strings.TrimSpace(snapshot.CurrentRequest); current != "" {
			return current
		}
	}
	return strings.TrimSpace(task.Prompt)
}

// PipelineWorkerType returns the task's primary implementation worker type.
// For expanded pipeline stages this comes from task context, not the sub-node
// agent type (which will be inspector-pipeline/tester-pipeline during red
// stages).
func PipelineWorkerType(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	if task.Context != nil {
		if workerType, _ := task.Context["agent_type"].(string); strings.TrimSpace(workerType) != "" {
			return strings.TrimSpace(workerType)
		}
	}
	return strings.TrimSpace(task.AgentType)
}

func decodeMap(ctx map[string]any, key string) map[string]any {
	if ctx == nil {
		return nil
	}
	value, ok := ctx[key]
	if !ok {
		return nil
	}
	typed, _ := value.(map[string]any)
	return typed
}

func decodeStringList(ctx map[string]any, key string) []string {
	if ctx == nil {
		return nil
	}
	return decodeAnyStringList(ctx[key])
}

func decodeAnyStringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, entry := range typed {
			if s, ok := entry.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, strings.TrimSpace(s))
			}
		}
		return result
	default:
		return nil
	}
}

func summarizePipelineProtocolMembers(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			item, _ := entry.(map[string]any)
			agentType, _ := item["agent_type"].(string)
			role, _ := item["role"].(string)
			agentType = strings.TrimSpace(agentType)
			role = strings.TrimSpace(role)
			if agentType == "" {
				continue
			}
			if role != "" {
				out = append(out, agentType+": "+role)
				continue
			}
			out = append(out, agentType)
		}
		return out
	default:
		return nil
	}
}

func summarizePipelineChallenge(value any) []string {
	item, _ := value.(map[string]any)
	if len(item) == 0 {
		return nil
	}
	out := make([]string, 0, 6)
	if challengeID, _ := item["id"].(string); strings.TrimSpace(challengeID) != "" {
		out = append(out, "Challenge ID: "+strings.TrimSpace(challengeID))
	}
	if requestingAgent, _ := item["requesting_agent"].(string); strings.TrimSpace(requestingAgent) != "" {
		out = append(out, "Requested By: "+strings.TrimSpace(requestingAgent))
	}
	if targets := decodeAnyStringList(item["target_agents"]); len(targets) > 0 {
		out = append(out, "Targets: "+strings.Join(targets, ", "))
	}
	if reason, _ := item["reason"].(string); strings.TrimSpace(reason) != "" {
		out = append(out, "Reason: "+strings.TrimSpace(reason))
	}
	if request, _ := item["request"].(string); strings.TrimSpace(request) != "" {
		out = append(out, "Request: "+strings.TrimSpace(request))
	}
	if required := decodeAnyStringList(item["required_output"]); len(required) > 0 {
		out = append(out, "Required Output: "+strings.Join(required, "; "))
	}
	return out
}

func summarizePipelineAuditLock(value any) []string {
	item, _ := value.(map[string]any)
	if len(item) == 0 {
		return nil
	}
	out := make([]string, 0, 4)
	if ownerAgent, _ := item["owner_agent"].(string); strings.TrimSpace(ownerAgent) != "" {
		out = append(out, "Owner: "+strings.TrimSpace(ownerAgent))
	}
	if phase, _ := item["phase"].(string); strings.TrimSpace(phase) != "" {
		out = append(out, "Phase: "+strings.TrimSpace(phase))
	}
	if reason, _ := item["reason"].(string); strings.TrimSpace(reason) != "" {
		out = append(out, "Reason: "+strings.TrimSpace(reason))
	}
	out = append(out, "Challenges to Inspector are refused while this lock is active.")
	return out
}

func summarizePipelineValidation(value any) []string {
	item, _ := value.(map[string]any)
	if len(item) == 0 {
		return nil
	}
	out := make([]string, 0, 6)
	if challengeID, _ := item["challenge_id"].(string); strings.TrimSpace(challengeID) != "" {
		out = append(out, "Challenge ID: "+strings.TrimSpace(challengeID))
	}
	if requestingAgent, _ := item["requesting_agent"].(string); strings.TrimSpace(requestingAgent) != "" {
		out = append(out, "Requester: "+strings.TrimSpace(requestingAgent))
	}
	if respondingAgent, _ := item["responding_agent"].(string); strings.TrimSpace(respondingAgent) != "" {
		out = append(out, "Responder: "+strings.TrimSpace(respondingAgent))
	}
	if status, _ := item["status"].(string); strings.TrimSpace(status) != "" {
		out = append(out, "Status: "+strings.TrimSpace(status))
	}
	if summary, _ := item["summary"].(string); strings.TrimSpace(summary) != "" {
		out = append(out, "Summary: "+strings.TrimSpace(summary))
	}
	if evidence := decodeAnyStringList(item["evidence_refs"]); len(evidence) > 0 {
		out = append(out, "Evidence: "+strings.Join(evidence, ", "))
	}
	return out
}

func summarizePipelineProtocolEvents(value any) []string {
	entries, _ := value.([]any)
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		if len(item) == 0 {
			continue
		}
		eventType, _ := item["type"].(string)
		agentType, _ := item["agent_type"].(string)
		summary, _ := item["summary"].(string)
		parts := make([]string, 0, 3)
		if strings.TrimSpace(eventType) != "" {
			parts = append(parts, strings.TrimSpace(eventType))
		}
		if strings.TrimSpace(agentType) != "" {
			parts = append(parts, strings.TrimSpace(agentType))
		}
		if strings.TrimSpace(summary) != "" {
			parts = append(parts, strings.TrimSpace(summary))
		}
		if len(parts) > 0 {
			out = append(out, strings.Join(parts, ": "))
		}
	}
	return out
}

func extractAffectedPaths(ctx map[string]any) []string {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx["affected_files"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, entry := range typed {
			switch value := entry.(type) {
			case string:
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					result = append(result, trimmed)
				}
			case map[string]any:
				if path, _ := value["path"].(string); strings.TrimSpace(path) != "" {
					result = append(result, strings.TrimSpace(path))
				}
			}
		}
		return result
	default:
		return nil
	}
}

func decodeWorkspacePaths(ctx map[string]any, key string) []string {
	workspace := decodeMap(ctx, "workspace")
	if len(workspace) == 0 {
		return nil
	}
	return decodeAnyStringList(workspace[key])
}

func decodeWorkerPacket(ctx map[string]any, agentType string) map[string]any {
	if ctx == nil || strings.TrimSpace(agentType) == "" {
		return nil
	}
	raw, ok := ctx["worker_packets"]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		for _, packet := range typed {
			if value, _ := packet["agent_type"].(string); strings.EqualFold(strings.TrimSpace(value), agentType) {
				return packet
			}
		}
	case []any:
		for _, entry := range typed {
			packet, _ := entry.(map[string]any)
			if value, _ := packet["agent_type"].(string); strings.EqualFold(strings.TrimSpace(value), agentType) {
				return packet
			}
		}
	}
	return nil
}

func decodeAcceptanceCriteria(ctx map[string]any) []string {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx["acceptance_criteria"]
	if !ok {
		return nil
	}
	criteria, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(criteria))
	for _, item := range criteria {
		criterion, ok := item.(map[string]any)
		if !ok {
			continue
		}
		given, _ := criterion["given"].(string)
		when, _ := criterion["when"].(string)
		thenText, _ := criterion["then"].(string)
		priority, _ := criterion["priority"].(string)
		text := strings.TrimSpace(fmt.Sprintf("[%s] Given %s, when %s, then %s", priority, given, when, thenText))
		if text != "[] Given , when , then" {
			result = append(result, text)
		}
	}
	return result
}

func writeLabeledScalar(b *strings.Builder, label string, value any) {
	if b == nil {
		return
	}
	text, _ := value.(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	fmt.Fprintf(b, "\n%s: %s\n", label, text)
}

func writeListSection(b *strings.Builder, title string, items []string) {
	if b == nil || len(items) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		fmt.Fprintf(b, "- %s\n", item)
	}
}

func summarizeCoordinationClaims(value any) []string {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		claim, _ := entry.(map[string]any)
		scopeKind, _ := claim["scope_kind"].(string)
		scopeKey, _ := claim["scope_key"].(string)
		purpose, _ := claim["purpose"].(string)
		ownerType, _ := claim["owner_type"].(string)
		line := strings.TrimSpace(fmt.Sprintf("%s %s", scopeKind, scopeKey))
		if ownerType != "" {
			line = strings.TrimSpace(fmt.Sprintf("%s (%s)", line, ownerType))
		}
		if purpose != "" {
			line = strings.TrimSpace(fmt.Sprintf("%s — %s", line, purpose))
		}
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}

func summarizeCoordinationArtifacts(value any) []string {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		artifact, _ := entry.(map[string]any)
		kind, _ := artifact["kind"].(string)
		summary, _ := artifact["summary"].(string)
		producerType, _ := artifact["producer_type"].(string)
		line := strings.TrimSpace(kind)
		if producerType != "" {
			line = strings.TrimSpace(fmt.Sprintf("%s from %s", line, producerType))
		}
		if summary != "" {
			line = strings.TrimSpace(fmt.Sprintf("%s — %s", line, summary))
		}
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}

func summarizeCoordinationReviews(value any) []string {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		review, _ := entry.(map[string]any)
		artifactID, _ := review["artifact_id"].(string)
		summary, _ := review["summary"].(string)
		line := strings.TrimSpace(artifactID)
		if summary != "" {
			line = strings.TrimSpace(fmt.Sprintf("%s — %s", line, summary))
		}
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}

func summarizeCoordinationContract(value any) []string {
	contract, ok := value.(map[string]any)
	if !ok || len(contract) == 0 {
		return nil
	}
	items := make([]string, 0, 4)
	if summary, _ := contract["summary"].(string); strings.TrimSpace(summary) != "" {
		items = append(items, strings.TrimSpace(summary))
	}
	if minClaims, ok := contract["minimum_claims"].(float64); ok && int(minClaims) > 0 {
		items = append(items, fmt.Sprintf("Minimum claims before completion: %d", int(minClaims)))
	}
	if minArtifacts, ok := contract["minimum_artifacts"].(float64); ok && int(minArtifacts) > 0 {
		items = append(items, fmt.Sprintf("Minimum artifacts before completion: %d", int(minArtifacts)))
	}
	if kinds := decodeAnyStringList(contract["preferred_artifact_kinds"]); len(kinds) > 0 {
		items = append(items, "Preferred artifact kinds: "+strings.Join(kinds, ", "))
	}
	return items
}

func summarizeCoordinationPrecedents(value any) []string {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		precedent, _ := entry.(map[string]any)
		category, _ := precedent["category"].(string)
		title, _ := precedent["title"].(string)
		summary, _ := precedent["summary"].(string)
		sessionID, _ := precedent["session_id"].(string)
		line := strings.TrimSpace(summary)
		if line == "" {
			line = strings.TrimSpace(title)
		}
		if category != "" {
			line = strings.TrimSpace(fmt.Sprintf("[%s] %s", category, line))
		}
		if sessionID != "" {
			line = strings.TrimSpace(fmt.Sprintf("%s (%s)", line, sessionID))
		}
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}
