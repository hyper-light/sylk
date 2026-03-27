package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

const coordinationPrecedentLimit = 4
const coordinationPrecedentTimeout = 5 * time.Second

func (o *Orchestrator) queryCoordinationPrecedents(
	ctx context.Context,
	taskName string,
	taskSlug string,
	workerType string,
) ([]coordination.PrecedentSummary, error) {
	if o == nil || o.coordination == nil || !o.config.ArchivalistEnabled {
		return nil, nil
	}
	taskName = strings.TrimSpace(taskName)
	taskSlug = strings.TrimSpace(taskSlug)
	workerType = strings.TrimSpace(workerType)
	if taskName == "" && taskSlug == "" {
		return nil, nil
	}
	if cached, ok := o.coordination.GetCachedPrecedents(taskName, taskSlug, workerType); ok {
		return cached, nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, coordinationPrecedentTimeout)
	defer cancel()

	query := fmt.Sprintf("Find historical coordination precedents for %s", firstNonEmpty(taskName, taskSlug))
	branchCtx, branch := shared.BeginInterAgentBranch(queryCtx, shared.InterAgentBranchSpec{
		Kind:       shared.InterAgentToolEventKindConsult,
		ToolName:   "consult_archivalist",
		AgentTypes: []string{"archivalist"},
		Summary:    query,
		Args: map[string]any{
			"target":      "archivalist",
			"query":       query,
			"task_name":   taskName,
			"task_slug":   taskSlug,
			"worker_type": workerType,
			"limit":       coordinationPrecedentLimit,
		},
	})
	respMsg, err := o.requestRouteSync(branchCtx, "archivalist",
		query,
		branch.ApplyMetadata(branchCtx, map[string]any{
			"coordination_precedent_query": true,
			"task_name":                    taskName,
			"task_slug":                    taskSlug,
			"worker_type":                  workerType,
			"limit":                        coordinationPrecedentLimit,
		}),
	)
	branch.CompleteFromMessage(branchCtx, respMsg, err)
	if err != nil {
		return nil, err
	}
	if respMsg == nil {
		return nil, nil
	}
	resp, ok := respMsg.GetRouteResponse()
	if !ok || resp == nil {
		if errText, hasErr := respMsg.GetError(); hasErr && strings.TrimSpace(errText) != "" {
			return nil, fmt.Errorf("coordination precedent query failed: %s", errText)
		}
		return nil, fmt.Errorf("invalid coordination precedent response")
	}
	if !resp.Success {
		return nil, fmt.Errorf("coordination precedent query failed: %s", strings.TrimSpace(resp.Error))
	}

	var decoded struct {
		Precedents []coordination.PrecedentSummary `json:"precedents"`
	}
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("encode coordination precedent response: %w", err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode coordination precedent response: %w", err)
	}
	if len(decoded.Precedents) > coordinationPrecedentLimit {
		decoded.Precedents = decoded.Precedents[:coordinationPrecedentLimit]
	}
	o.coordination.StoreCachedPrecedents(taskName, taskSlug, workerType, decoded.Precedents)
	return decoded.Precedents, nil
}
