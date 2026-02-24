package global

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/agents/inspector/shared"
)

// LayerGateFunc is the function signature for DAG layer gates.
type LayerGateFunc func(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]any) error

// NewInspectorLayerGate creates a LayerGateFunc that audits completed layers.
func NewInspectorLayerGate(inspector *GlobalInspector, store *AuditDiffStore) LayerGateFunc {
	return func(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]any) error {
		key := shared.AuditKey{DAGID: dagID, LayerIdx: layerIdx}
		diffs := store.Get(key)

		// No modifications captured -- pass through.
		if diffs == nil || len(diffs.NodeDiffs) == 0 {
			return nil
		}

		req := &shared.LayerAuditRequest{
			DAGID:     dagID,
			LayerIdx:  layerIdx,
			NodeDiffs: diffs.NodeDiffs,
		}

		result, err := inspector.AuditLayer(ctx, req)
		if err != nil {
			return fmt.Errorf("layer gate audit failed: %w", err)
		}

		// Clean up consumed diffs.
		store.Delete(key)

		if result.HasBlockingIssues() {
			return fmt.Errorf(
				"layer %d blocked: %d critical, %d high issues",
				layerIdx, result.CriticalCount, result.HighCount,
			)
		}

		return nil
	}
}
