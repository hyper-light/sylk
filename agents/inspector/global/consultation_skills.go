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

// Phase 2.K / GI-A refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.K.1):
// consult_librarian_style + consult_academic_approach +
// consult_archivalist_context + request_architect_research folded into
// shared consult_peer(target_agent_type=…). The consultAgent helper
// below stays because the request_architect_research programmatic
// surface and existing tests exercise its sync bus path; knowledge-
// agent routing now flows through the fabric-backed consult_peer
// primitive registered via CrossPipelineSkills.

func (gi *GlobalInspector) consultAgent(ctx context.Context, target, prompt string, metadata map[string]any) (string, error) {
	prompt = strings.TrimSpace(prompt)
	admission := agentShared.AdmitConsultation(ctx, target, prompt, metadata)
	if !admission.Allowed {
		return "", agentShared.ConsultationAdmissionError(admission)
	}
	spec := agentShared.InterAgentBranchSpec{
		Kind:       agentShared.InterAgentToolEventKindConsult,
		ToolName:   "consult_" + strings.ReplaceAll(strings.TrimSpace(target), "-", "_"),
		AgentTypes: []string{target},
		Summary:    prompt,
		Args: map[string]any{
			"target": target,
			"query":  prompt,
		},
	}
	msg, err := agentShared.WithInterAgentBranchMessage(ctx, spec, func(branchCtx context.Context, branch agentShared.InterAgentBranchHandle) (*guide.Message, error) {
		branchMetadata := branch.ApplyMetadata(branchCtx, admission.Metadata)
		resp, routeErr := gi.requestRouteSync(branchCtx, target, prompt, branchMetadata)
		if routeErr != nil {
			agentShared.RecordConsultationOutcome(branchCtx, admission.AttemptID, false, nil, routeErr)
			return resp, routeErr
		}
		content, extractErr := extractConsultationResponse(resp)
		agentShared.RecordConsultationOutcome(branchCtx, admission.AttemptID, extractErr == nil, content, extractErr)
		return resp, extractErr
	})
	if err != nil {
		return "", err
	}
	return extractConsultationResponse(msg)
}

func consultationBlockedResult(target string, err *agentShared.ConsultationPressureError) map[string]any {
	result := map[string]any{
		"consulted": false,
		"blocked":   true,
		"target":    strings.TrimSpace(target),
		"status":    "deferred",
	}
	if err == nil {
		return result
	}
	if message := strings.TrimSpace(err.Error()); message != "" {
		result["guidance"] = message
		result["error"] = message
	}
	if recovery := agentShared.ConsultationPressureRecovery(err); len(recovery) > 0 {
		result["recovery"] = recovery
	}
	return result
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
