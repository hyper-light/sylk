package global

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

var architectSnapshotVersionPattern = regexp.MustCompile(`_v(\d+)\.(\d+)\.json$`)

func loadPlanContextSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("load_plan_context").
		Description("Load or recover the architect's full plan context from the published plan file or persisted architect snapshot on disk.").
		Domain("audit").
		Keywords("plan", "architect", "context", "recover", "disk").
		Priority(100).
		Usage("Use immediately when the full architect plan is missing, partial, or suspect. Prefer this before making any final global audit judgment without complete plan context.").
		Requirement("Provide the plan_file_path when available. If it is missing, provide plan_id and session_id so the persisted architect snapshot can be loaded from disk.").
		Satisfies("Recovers the exact architect plan context the global inspector should audit against.").
		Avoid("Do not guess missing plan details from task-local summaries when the plan can be loaded from disk.").
		StringParam("plan_snapshot", "Already-provided plan snapshot, if one is present", false).
		StringParam("plan_id", "Architect plan ID", false).
		StringParam("plan_file_path", "Architect plan file path", false).
		StringParam("session_id", "Session identifier", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				PlanSnapshot string `json:"plan_snapshot"`
				PlanID       string `json:"plan_id"`
				PlanFilePath string `json:"plan_file_path"`
				SessionID    string `json:"session_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			planSnapshot, source, planFilePath, err := gi.loadPlanContext(
				ctx,
				params.PlanSnapshot,
				params.PlanID,
				params.PlanFilePath,
				params.SessionID,
			)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"loaded":         true,
				"source":         source,
				"plan_file_path": planFilePath,
				"plan_snapshot":  planSnapshot,
			}, nil
		}).
		Build()
}

func consultLibrarianStyleSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("consult_librarian_style").
		Description("Consult the Librarian about established code style, local patterns, naming, layering, and codebase-specific conventions.").
		Domain("audit").
		Keywords("librarian", "style", "pattern", "convention", "codebase").
		Priority(95).
		Usage("Use proactively when judging whether the implementation matches the repo's style, naming, layout, and established local patterns.").
		Requirement("Provide the concrete style or pattern question, plus the relevant files or context.").
		Satisfies("Adds codebase-specific evidence about how work should look in this repository, not just in the abstract.").
		Avoid("Do not rely on your own generic style instincts when the codebase already establishes a stronger local pattern.").
		StringParam("question", "Style or pattern question for the Librarian", true).
		StringParam("context", "Optional surrounding context", false).
		ArrayParam("files", "Relevant files or packages", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Question string   `json:"question"`
				Context  string   `json:"context"`
				Files    []string `json:"files"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			content, err := gi.consultAgent(ctx, "librarian", buildLibrarianConsultationPrompt(params.Question, params.Context, params.Files), map[string]any{
				"consultation_kind": "librarian_style",
				"files":             params.Files,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"consulted": true,
				"target":    "librarian",
				"response":  content,
			}, nil
		}).
		Build()
}

func consultAcademicApproachSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("consult_academic_approach").
		Description("Consult the Academic about stronger alternative implementations, correctness tradeoffs, performance implications, or whether the architect's approach should be challenged.").
		Domain("audit").
		Keywords("academic", "alternative", "approach", "performance", "correctness", "tradeoff").
		Priority(95).
		Usage("Use proactively when a better implementation, cleaner design, or stronger overall approach may exist. This is the main tool for challenging the current solution or even the architect's plan.").
		Requirement("Provide the current approach, the concrete concern, and enough context for the Academic to compare alternatives.").
		Satisfies("Adds principled comparative evidence about whether the current implementation and overall approach are actually the best available fit.").
		Avoid("Do not accept an implementation as 'good enough' without comparison when a stronger alternative is plausible.").
		StringParam("question", "Alternative-implementation or tradeoff question", true).
		StringParam("current_approach", "Current implementation or plan approach being evaluated", false).
		StringParam("context", "Optional surrounding context", false).
		ArrayParam("files", "Relevant files or packages", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Question        string   `json:"question"`
				CurrentApproach string   `json:"current_approach"`
				Context         string   `json:"context"`
				Files           []string `json:"files"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			content, err := gi.consultAgent(ctx, "academic", buildAcademicConsultationPrompt(params.Question, params.CurrentApproach, params.Context, params.Files), map[string]any{
				"consultation_kind": "academic_approach",
				"files":             params.Files,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"consulted": true,
				"target":    "academic",
				"response":  content,
			}, nil
		}).
		Build()
}

func consultArchivalistContextSkill(gi *GlobalInspector) *skills.Skill {
	return skills.NewSkill("consult_archivalist_context").
		Description("Consult the Archivalist about past failure modes, historical decisions, user preferences, and prior remediation that should shape the audit.").
		Domain("audit").
		Keywords("archivalist", "history", "preferences", "failures", "precedent").
		Priority(95).
		Usage("Use proactively before sign-off when past failures, prior user preferences, or earlier remediation history might materially change the audit verdict.").
		Requirement("Provide the historical question plus any plan, DAG, task, or file context that will help the Archivalist find the right precedent.").
		Satisfies("Adds historical and preference-preservation evidence so the global inspector can stop repeating known failure modes.").
		Avoid("Do not assume the current implementation is acceptable if similar work failed before or if the user has already expressed contrary preferences.").
		StringParam("question", "Historical or precedent question", true).
		StringParam("context", "Optional surrounding context", false).
		StringParam("plan_id", "Plan identifier", false).
		StringParam("dag_id", "DAG identifier", false).
		StringParam("task_id", "Task identifier", false).
		ArrayParam("files", "Relevant files or packages", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Question string   `json:"question"`
				Context  string   `json:"context"`
				PlanID   string   `json:"plan_id"`
				DAGID    string   `json:"dag_id"`
				TaskID   string   `json:"task_id"`
				Files    []string `json:"files"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			content, err := gi.consultAgent(ctx, "archivalist", buildArchivalistConsultationPrompt(params.Question, params.Context, params.PlanID, params.DAGID, params.TaskID, params.Files), map[string]any{
				"consultation_kind": "archivalist_context",
				"plan_id":           strings.TrimSpace(params.PlanID),
				"dag_id":            strings.TrimSpace(params.DAGID),
				"task_id":           strings.TrimSpace(params.TaskID),
				"files":             params.Files,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"consulted": true,
				"target":    "archivalist",
				"response":  content,
			}, nil
		}).
		Build()
}

func (gi *GlobalInspector) consultAgent(ctx context.Context, target, prompt string, metadata map[string]any) (string, error) {
	prompt = strings.TrimSpace(prompt)
	branchCtx, branch := agentShared.BeginInterAgentBranch(ctx, agentShared.InterAgentBranchSpec{
		Kind:       agentShared.InterAgentToolEventKindConsult,
		ToolName:   "consult_" + strings.ReplaceAll(strings.TrimSpace(target), "-", "_"),
		AgentTypes: []string{target},
		Summary:    prompt,
		Args: map[string]any{
			"target": target,
			"query":  prompt,
		},
	})
	msg, err := gi.requestRouteSync(branchCtx, target, prompt, branch.ApplyMetadata(branchCtx, metadata))
	branch.CompleteFromMessage(branchCtx, msg, err)
	if err != nil {
		return "", err
	}
	return extractConsultationResponse(msg)
}

func buildLibrarianConsultationPrompt(question, context string, files []string) string {
	var b strings.Builder
	b.WriteString("Global inspector style consultation.\n")
	b.WriteString("Focus on repository-specific code style, naming, layering, and established implementation patterns.\n")
	fmt.Fprintf(&b, "Question: %s\n", strings.TrimSpace(question))
	if trimmed := strings.TrimSpace(context); trimmed != "" {
		fmt.Fprintf(&b, "Context: %s\n", trimmed)
	}
	if len(files) > 0 {
		b.WriteString("Relevant files:\n")
		for _, file := range files {
			if trimmed := strings.TrimSpace(file); trimmed != "" {
				fmt.Fprintf(&b, "- %s\n", trimmed)
			}
		}
	}
	b.WriteString("Return the concrete local conventions, precedents, and any style or pattern mismatches the global inspector should enforce.")
	return b.String()
}

func buildAcademicConsultationPrompt(question, currentApproach, context string, files []string) string {
	var b strings.Builder
	b.WriteString("Global inspector approach consultation.\n")
	b.WriteString("Challenge the current implementation and overall plan approach. Compare it against stronger alternatives with respect to correctness, robustness, elegance, and performance.\n")
	fmt.Fprintf(&b, "Question: %s\n", strings.TrimSpace(question))
	if trimmed := strings.TrimSpace(currentApproach); trimmed != "" {
		fmt.Fprintf(&b, "Current approach: %s\n", trimmed)
	}
	if trimmed := strings.TrimSpace(context); trimmed != "" {
		fmt.Fprintf(&b, "Context: %s\n", trimmed)
	}
	if len(files) > 0 {
		b.WriteString("Relevant files:\n")
		for _, file := range files {
			if trimmed := strings.TrimSpace(file); trimmed != "" {
				fmt.Fprintf(&b, "- %s\n", trimmed)
			}
		}
	}
	b.WriteString("Return whether the current approach is sound, what the strongest alternative is, and what tradeoffs matter most.")
	return b.String()
}

func buildArchivalistConsultationPrompt(question, context, planID, dagID, taskID string, files []string) string {
	var b strings.Builder
	b.WriteString("Global inspector historical-context consultation.\n")
	b.WriteString("Look for prior failures, prior user preferences, earlier remediation, and relevant precedent that should shape this audit.\n")
	fmt.Fprintf(&b, "Question: %s\n", strings.TrimSpace(question))
	if trimmed := strings.TrimSpace(planID); trimmed != "" {
		fmt.Fprintf(&b, "Plan ID: %s\n", trimmed)
	}
	if trimmed := strings.TrimSpace(dagID); trimmed != "" {
		fmt.Fprintf(&b, "DAG ID: %s\n", trimmed)
	}
	if trimmed := strings.TrimSpace(taskID); trimmed != "" {
		fmt.Fprintf(&b, "Task ID: %s\n", trimmed)
	}
	if trimmed := strings.TrimSpace(context); trimmed != "" {
		fmt.Fprintf(&b, "Context: %s\n", trimmed)
	}
	if len(files) > 0 {
		b.WriteString("Relevant files:\n")
		for _, file := range files {
			if trimmed := strings.TrimSpace(file); trimmed != "" {
				fmt.Fprintf(&b, "- %s\n", trimmed)
			}
		}
	}
	b.WriteString("Return the most relevant prior failures, preserved user preferences, and historical constraints the global inspector should enforce.")
	return b.String()
}

func extractConsultationResponse(msg *guide.Message) (string, error) {
	if msg == nil {
		return "", fmt.Errorf("consultation response is missing")
	}
	if errText, ok := msg.GetError(); ok && strings.TrimSpace(errText) != "" {
		return "", fmt.Errorf("%s", strings.TrimSpace(errText))
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return "", fmt.Errorf("consultation response payload is unsupported")
	}
	if !resp.Success {
		return "", fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	switch typed := resp.Data.(type) {
	case string:
		return strings.TrimSpace(typed), nil
	case map[string]any:
		for _, key := range []string{"response", "content", "message", "result"} {
			if value, ok := typed[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value), nil
			}
		}
		data, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("marshal consultation response: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	default:
		data, err := json.Marshal(resp.Data)
		if err != nil {
			return "", fmt.Errorf("marshal consultation response: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
}

func (gi *GlobalInspector) loadPlanContext(
	ctx context.Context,
	providedSnapshot string,
	planID string,
	planFilePath string,
	sessionID string,
) (string, string, string, error) {
	if trimmed := strings.TrimSpace(providedSnapshot); trimmed != "" {
		return trimmed, "provided", strings.TrimSpace(planFilePath), nil
	}
	if content, resolvedPath, err := readPlanContextFile(planFilePath); err == nil {
		return content, "plan_file", resolvedPath, nil
	}
	normalizedSession := strings.TrimSpace(sessionID)
	if normalizedSession == "" {
		normalizedSession = strings.TrimSpace(versioning.SessionIDFromContext(ctx))
	}
	if normalizedSession == "" {
		normalizedSession = strings.TrimSpace(gi.config.SessionID)
	}
	jsonPath := architectSnapshotJSONPath(normalizedSession, planID)
	if content, resolvedPath, err := readPlanContextFile(jsonPath); err == nil {
		return content, "architect_snapshot_json", resolvedPath, nil
	}
	return "", "", "", fmt.Errorf("plan context is unavailable: provide plan_file_path or plan_id/session_id")
}

func readPlanContextFile(path string) (string, string, error) {
	resolved := strings.TrimSpace(path)
	if resolved == "" {
		return "", "", fmt.Errorf("plan context path is empty")
	}
	if !validPlanContextPath(resolved) {
		return "", "", fmt.Errorf("invalid plan context path: %s", resolved)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", "", fmt.Errorf("plan context file is empty")
	}
	if strings.HasSuffix(strings.ToLower(resolved), ".json") {
		content = "```json\n" + content + "\n```"
	}
	return content, resolved, nil
}

func architectSnapshotJSONPath(sessionID, planID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return ""
	}
	baseDir := filepath.Join(".sylk", "sessions", sessionID, "agents", "architect", "plans")
	matches, err := filepath.Glob(filepath.Join(baseDir, planID+"_v*.json"))
	if err == nil {
		var (
			bestPath    string
			bestVersion versioning.SemanticVersion
		)
		for _, match := range matches {
			ver, ok := parseArchitectSnapshotVersion(match)
			if !ok {
				continue
			}
			if bestPath == "" || ver.Compare(bestVersion) > 0 {
				bestPath = match
				bestVersion = ver
			}
		}
		if bestPath != "" {
			return bestPath
		}
	}
	return filepath.Join(baseDir, planID+".json")
}

func parseArchitectSnapshotVersion(path string) (versioning.SemanticVersion, bool) {
	match := architectSnapshotVersionPattern.FindStringSubmatch(strings.TrimSpace(path))
	if len(match) != 3 {
		return versioning.SemanticVersion{}, false
	}
	major, err := strconv.ParseUint(match[1], 10, 32)
	if err != nil {
		return versioning.SemanticVersion{}, false
	}
	minor, err := strconv.ParseUint(match[2], 10, 32)
	if err != nil {
		return versioning.SemanticVersion{}, false
	}
	return versioning.SemanticVersion{Major: uint32(major), Minor: uint32(minor)}, true
}

func validPlanContextPath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == "" {
		return false
	}
	lower := strings.ToLower(cleaned)
	if strings.Contains(cleaned, "..") {
		return false
	}
	if !(strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".json")) {
		return false
	}
	return strings.Contains(cleaned, filepath.Join(".sylk", "sessions")) &&
		(strings.Contains(cleaned, string(filepath.Separator)+"plans"+string(filepath.Separator)) ||
			strings.Contains(cleaned, filepath.Join(".sylk", "sessions")))
}
