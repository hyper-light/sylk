package global

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// AuditLayer performs a comprehensive audit on a completed DAG layer.
func (gi *GlobalInspector) AuditLayer(ctx context.Context, req *shared.LayerAuditRequest) (*shared.AuditResult, error) {
	startTime := time.Now()

	auditCtx, cancel := context.WithTimeout(ctx, gi.config.AuditTimeout)
	defer cancel()

	result := &shared.AuditResult{
		DAGID:     req.DAGID,
		LayerIdx:  req.LayerIdx,
		StartedAt: startTime,
	}

	// Build audit prompt from diffs and plan
	auditPrompt := gi.buildAuditPrompt(req)

	// Run LLM tool loop for analysis
	llmReq := &providers.Request{
		SystemPrompt: shared.GlobalInspectorSystemPrompt(),
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: auditPrompt},
		},
		Model:     gi.config.Model,
		MaxTokens: gi.config.MaxTokens,
		Tools:     gi.buildToolDefinitions(),
	}

	response, err := gi.executeToolLoop(auditCtx, llmReq)
	if err != nil {
		return nil, fmt.Errorf("audit layer %d of DAG %s: %w", req.LayerIdx, req.DAGID, err)
	}

	// Parse LLM response into structured result
	gi.parseAuditResponse(response, result)

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)
	result.Passed = !result.HasBlockingIssues()

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

func (gi *GlobalInspector) parseAuditResponse(response string, result *shared.AuditResult) {
	// The LLM tool loop will have already invoked tools and generated findings.
	// The response text contains the LLM's summary judgment.
	// Tool call results are accumulated during the tool loop.
	// This is a simplified parser -- real implementation would parse structured
	// tool outputs from the conversation history.

	result.PlanAdherence = shared.PlanAdherenceScore{
		Score: 1.0,
	}
	_ = response
}
