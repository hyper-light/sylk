package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/dag"
)

type ClaimsDAGBackendConfig struct {
	Bridge *DAGBridge
	PlanID string
}

type ClaimsDAGBackend struct {
	bridge *DAGBridge
	planID string
}

func NewClaimsDAGBackend(cfg ClaimsDAGBackendConfig) *ClaimsDAGBackend {
	return &ClaimsDAGBackend{bridge: cfg.Bridge, planID: strings.TrimSpace(cfg.PlanID)}
}

func (b *ClaimsDAGBackend) HandleDAGOperation(ctx context.Context, req claims.DAGOperationRequest) (claims.DAGOperationArtifactData, error) {
	data := req.Requested
	if b == nil || b.bridge == nil {
		data.FailureReason = "DAG bridge not configured"
		return data, nil
	}
	switch data.Operation {
	case claims.DAGProcessorToolAllocatePipeline:
		return b.allocate(ctx, req, data), nil
	case claims.DAGProcessorToolDeallocatePipeline:
		return b.deallocate(data), nil
	case claims.DAGProcessorToolQueryState:
		return b.query(data), nil
	case claims.DAGProcessorToolRepair:
		return b.repair(data), nil
	default:
		return b.health(data), nil
	}
}

func (b *ClaimsDAGBackend) allocate(ctx context.Context, req claims.DAGOperationRequest, data claims.DAGOperationArtifactData) claims.DAGOperationArtifactData {
	graph, err := claimsDAG(data)
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	if err := b.bridge.Prepare(ctx, graph, firstClaimsDAGString(b.planID, req.Claim.TaskID), req.Claim.SessionID); err != nil {
		data.FailureReason = err.Error()
	}
	return data
}

func (b *ClaimsDAGBackend) deallocate(data claims.DAGOperationArtifactData) claims.DAGOperationArtifactData {
	if err := b.bridge.Cancel(data.PipelineID, "claims DAG deallocation"); err != nil {
		data.FailureReason = err.Error()
	}
	return data
}

func (b *ClaimsDAGBackend) query(data claims.DAGOperationArtifactData) claims.DAGOperationArtifactData {
	status, err := b.bridge.Status(data.PipelineID)
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	return applyClaimsDAGStatus(data, status)
}

func (b *ClaimsDAGBackend) repair(data claims.DAGOperationArtifactData) claims.DAGOperationArtifactData {
	mod := &DAGModification{Reason: "claims DAG repair"}
	if err := b.bridge.Modify(data.PipelineID, mod); err != nil {
		data.FailureReason = err.Error()
	}
	return data
}

func (b *ClaimsDAGBackend) health(data claims.DAGOperationArtifactData) claims.DAGOperationArtifactData {
	statuses := b.bridge.List()
	data.HealthSummary = fmt.Sprintf("active_dags=%d", len(statuses))
	return data
}

func claimsDAG(data claims.DAGOperationArtifactData) (*dag.DAG, error) {
	graph := dag.NewDAGWithID(data.PipelineID, data.PipelineID, dag.DefaultExecutionPolicy())
	deps := claimsDAGDependencies(data)
	for _, node := range data.Nodes {
		if err := graph.AddNode(dag.NodeConfig{ID: node, AgentType: "agent", Dependencies: deps[node]}); err != nil {
			return nil, err
		}
	}
	return graph, nil
}

func claimsDAGDependencies(data claims.DAGOperationArtifactData) map[string][]string {
	deps := make(map[string][]string, len(data.Nodes))
	for _, edge := range data.Edges {
		deps[edge.To] = append(deps[edge.To], edge.From)
	}
	return deps
}

func applyClaimsDAGStatus(data claims.DAGOperationArtifactData, status *dag.DAGStatus) claims.DAGOperationArtifactData {
	if status == nil {
		data.FailureReason = "DAG status unavailable"
		return data
	}
	data.NodeCount = status.NodesTotal
	data.PlannedPods = status.NodesTotal
	data.HealthSummary = status.State.String()
	return data
}

func firstClaimsDAGString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
