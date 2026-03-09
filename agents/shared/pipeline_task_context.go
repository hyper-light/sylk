package shared

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

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
	return strings.TrimSpace(b.String())
}

// ComposePipelineTaskUserPrompt formats a pipeline task into a readable user
// prompt so workers do not reason over raw JSON.
func ComposePipelineTaskUserPrompt(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Pipeline Task\n\n")
	fmt.Fprintf(&b, "Task ID: %s\n", task.TaskID)
	if slug, _ := task.Context["task_slug"].(string); slug != "" {
		fmt.Fprintf(&b, "Task Slug: %s\n", slug)
	}
	fmt.Fprintf(&b, "Node ID: %s\n", task.NodeID)
	if stage, _ := task.Context["pipeline_stage"].(string); stage != "" {
		fmt.Fprintf(&b, "Stage: %s\n", stage)
	}
	b.WriteString("\n### Assignment\n")
	b.WriteString(strings.TrimSpace(task.Prompt))
	b.WriteString("\n")

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

	if len(task.ParentResults) > 0 {
		b.WriteString("\n### Parent Results\n")
		for nodeID, result := range task.ParentResults {
			fmt.Fprintf(&b, "- %s: %v\n", nodeID, result)
		}
	}

	return strings.TrimSpace(b.String())
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
