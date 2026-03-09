package global

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	agentShared "github.com/adalundhe/sylk/agents/shared"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
)

// AuditLayer performs a comprehensive audit on a completed DAG layer.
func (gi *GlobalInspector) AuditLayer(ctx context.Context, req *shared.LayerAuditRequest) (*shared.AuditResult, error) {
	startTime := time.Now()

	auditCtx, cancel := context.WithTimeout(ctx, gi.config.AuditTimeout)
	defer cancel()

	lm := agentShared.LogMetaFromContext(ctx)
	if lm.EventLogger != nil {
		agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventAuditStarted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.AuditPayload{AuditID: req.DAGID, Phase: "started"})
	} else if gi.steering != nil {
		agentShared.LogAgentEvent(gi.steering.EventLogger(), agentlog.EventAuditStarted,
			gi.id, "", "", "info",
			&agentlog.AuditPayload{AuditID: req.DAGID, Phase: "started"})
	}

	result := &shared.AuditResult{
		DAGID:     req.DAGID,
		LayerIdx:  req.LayerIdx,
		StartedAt: startTime,
	}
	gi.applyDeterministicAuditPrepass(req, result)

	// Build audit prompt from diffs and plan
	auditPrompt := gi.buildAuditPrompt(req)
	if summary := gi.buildWorkspaceAuditContext(auditCtx, req); summary != "" {
		auditPrompt += "\n\n" + summary
	}

	// Run LLM tool loop for analysis
	gi.prepareSkillsForInput(auditPrompt)
	llmReq := &providers.Request{
		SystemPrompt: shared.GlobalInspectorSystemPrompt(),
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: auditPrompt},
		},
		Model:     gi.config.Model,
		MaxTokens: gi.config.MaxTokens,
		Tools:     gi.buildToolDefinitions(),
	}
	gi.applyLLMRuntimeProfile(llmReq, "audit")

	ledger := agentShared.SteeringLedgerFromContext(auditCtx)
	response, err := agentShared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return gi.executeToolLoop(auditCtx, llmReq, ledger)
	})
	if err != nil {
		if lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("audit tool loop: %v", err)})
		}
		return nil, fmt.Errorf("audit layer %d of DAG %s: %w", req.LayerIdx, req.DAGID, err)
	}

	// Parse LLM response into structured result
	gi.parseAuditResponse(response, result)

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)
	result.Passed = !result.HasBlockingIssues()

	// Log per-finding events for blocking issues.
	if lm.EventLogger != nil {
		for _, issue := range result.Issues {
			if issue.Severity == shared.Critical || issue.Severity == shared.High {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventAuditFinding,
					lm.AgentID, lm.SessionID, lm.CorrID, "warn",
					&agentlog.AuditPayload{AuditID: req.DAGID, Phase: "finding", Finding: issue.Message})
			}
		}
	}

	if lm.EventLogger != nil {
		phase := "completed"
		if !result.Passed {
			phase = "completed_with_findings"
		}
		agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventAuditCompleted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.AuditPayload{AuditID: req.DAGID, Phase: phase, DurNs: result.Duration.Nanoseconds()})
	} else if gi.steering != nil {
		agentShared.LogAgentEvent(gi.steering.EventLogger(), agentlog.EventAuditCompleted,
			gi.id, "", "", "info",
			&agentlog.AuditPayload{AuditID: req.DAGID, Phase: "completed", DurNs: result.Duration.Nanoseconds()})
	}

	return result, nil
}

func (gi *GlobalInspector) buildAuditPrompt(req *shared.LayerAuditRequest) string {
	prompt := fmt.Sprintf(
		"Audit DAG %s, layer %d.\n\n",
		req.DAGID, req.LayerIdx,
	)

	if req.PlanSnapshot != "" {
		prompt += fmt.Sprintf("## Architect Plan\n\n%s\n\n", req.PlanSnapshot)
	}

	if len(req.NodeDiffs) > 0 {
		prompt += "## Node Diffs\n\n"
		for nodeID, diff := range req.NodeDiffs {
			prompt += fmt.Sprintf("### Node: %s\n", nodeID)
			for i := range diff.ModifiedFiles {
				prompt += fmt.Sprintf("- %s\n", diff.ModifiedFiles[i].Path)
				fileDiff := diff.ModifiedFiles[i].Diff()
				if fileDiff != "" {
					prompt += fmt.Sprintf("```diff\n%s\n```\n", fileDiff)
				}
			}
			prompt += "\n"
		}
	}

	if len(req.NodeResults) > 0 {
		prompt += "## Node Results\n\n"
		for _, nr := range req.NodeResults {
			status := "success"
			if !nr.Success {
				status = fmt.Sprintf("failed: %s", nr.Error)
			}
			prompt += fmt.Sprintf("- %s: %s\n", nr.NodeID, status)
		}
	}

	prompt += "\nRun all critical analysis tools on the modified files. " +
		"Check cross-file coherence. Validate plan adherence. " +
		"Grade the layer quality. Escalate any blocking findings."

	return prompt
}

func (gi *GlobalInspector) buildWorkspaceAuditContext(ctx context.Context, req *shared.LayerAuditRequest) string {
	if gi == nil || gi.workspaceViews == nil || req == nil {
		return ""
	}
	paths := make([]string, 0)
	seen := make(map[string]struct{})
	for _, diff := range req.NodeDiffs {
		if diff == nil {
			continue
		}
		for _, modified := range diff.ModifiedFiles {
			path := strings.TrimSpace(modified.Path)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	summary, err := gi.workspaceViews.SummarizePaths(ctx, paths, "")
	if err != nil {
		return "## Workspace Snapshot\n\n- unavailable: " + strings.TrimSpace(err.Error())
	}
	return agentShared.FormatWorkspaceSummary(summary)
}

func (gi *GlobalInspector) parseAuditResponse(response string, result *shared.AuditResult) {
	if strings.TrimSpace(response) == "" || result == nil {
		return
	}
	// Deterministic prepass owns the structured result today. The LLM response
	// is preserved for future enrichment but must not overwrite precomputed
	// adherence and finding state with placeholder values.
}

func (gi *GlobalInspector) applyDeterministicAuditPrepass(
	req *shared.LayerAuditRequest,
	result *shared.AuditResult,
) {
	if req == nil || result == nil {
		return
	}

	fileOwners := make(map[string][]string)
	tasksCovered := make([]string, 0, len(req.NodeResults))
	tasksMissing := make([]string, 0, len(req.NodeResults))
	deviations := make([]string, 0)

	for nodeID, nodeResult := range req.NodeResults {
		if nodeResult == nil {
			tasksMissing = append(tasksMissing, nodeID)
			deviations = append(deviations, fmt.Sprintf("node %s has no execution result", nodeID))
			continue
		}
		if nodeResult.Success {
			tasksCovered = append(tasksCovered, nodeID)
		} else {
			tasksMissing = append(tasksMissing, nodeID)
			issue := shared.ValidationIssue{
				ID:       "node_failed_" + nodeID,
				Severity: shared.High,
				File:     "",
				Message:  fmt.Sprintf("Node %s failed before layer completion: %s", nodeID, strings.TrimSpace(nodeResult.Error)),
				RuleID:   "global/node-failure",
				Domain:   shared.DomainCode,
			}
			result.Issues = append(result.Issues, issue)
			result.HighCount++
			deviations = append(deviations, fmt.Sprintf("node %s failed: %s", nodeID, strings.TrimSpace(nodeResult.Error)))
			result.Recommendations = append(result.Recommendations, shared.Recommendation{
				Type:        shared.RecommendEngineerRework,
				Description: fmt.Sprintf("Rework failed node %s before the next layer executes.", nodeID),
				Priority:    shared.High,
				TargetAgent: "engineer",
			})
		}
	}

	for nodeID, diff := range req.NodeDiffs {
		if diff == nil || len(diff.ModifiedFiles) == 0 {
			if _, ok := req.NodeResults[nodeID]; ok {
				deviations = append(deviations, fmt.Sprintf("node %s completed without any captured file modifications", nodeID))
			}
			continue
		}
		for _, modified := range diff.ModifiedFiles {
			path := strings.TrimSpace(modified.Path)
			if path == "" {
				continue
			}
			fileOwners[path] = append(fileOwners[path], nodeID)
		}
	}

	for path, owners := range fileOwners {
		if len(owners) < 2 {
			continue
		}
		sort.Strings(owners)
		result.CrossFileIssues = append(result.CrossFileIssues, shared.CrossFileIssue{
			Files:       []string{path},
			Description: fmt.Sprintf("Multiple nodes modified %s in the same layer: %s", path, strings.Join(owners, ", ")),
			Severity:    shared.High,
			Category:    shared.CrossFileTypeInconsistency,
		})
		result.Issues = append(result.Issues, shared.ValidationIssue{
			ID:       "shared_file_" + sanitizeAuditID(path),
			Severity: shared.High,
			File:     path,
			Message:  fmt.Sprintf("Multiple nodes modified %s in the same layer.", path),
			RuleID:   "global/shared-file-touch",
			Domain:   shared.DomainCode,
		})
		result.HighCount++
		deviations = append(deviations, fmt.Sprintf("shared file touched by multiple nodes: %s", path))
		result.Recommendations = append(result.Recommendations, shared.Recommendation{
			Type:        shared.RecommendPlanRevision,
			Description: fmt.Sprintf("Revisit task boundaries for %s; multiple nodes touched the same file in one layer.", path),
			Priority:    shared.High,
			TargetAgent: "architect",
		})
	}

	result.PlanAdherence = shared.PlanAdherenceScore{
		Score:        adherenceScore(len(tasksMissing), result.HighCount, result.CriticalCount, len(deviations)),
		TasksCovered: uniqueSortedStrings(tasksCovered),
		TasksMissing: uniqueSortedStrings(tasksMissing),
		Deviations:   uniqueSortedStrings(deviations),
	}
}

func adherenceScore(taskMisses, highCount, criticalCount, deviations int) float64 {
	score := 1.0
	score -= float64(taskMisses) * 0.20
	score -= float64(highCount) * 0.10
	score -= float64(criticalCount) * 0.20
	score -= float64(deviations) * 0.03
	if score < 0 {
		return 0
	}
	return score
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sanitizeAuditID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("/", "_", ".", "_", "-", "_", " ", "_")
	value = replacer.Replace(value)
	if value == "" {
		return "audit"
	}
	return value
}
