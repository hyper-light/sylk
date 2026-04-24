package pipeline

import (
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
)

// wireClaimsBoardLifecycle subscribes to the claims board and handles
// pipeline lifecycle events when all claims are accepted.
//
// IMPORTANT: Per PARALLEL_GLOBAL_VFS.md, the VFS merge into green
// happens at pipeline-accept time (handoff_to_ot), NOT at
// board-complete time. The pipeline's work is already in green
// when the board completes. Board completion signals that all claims
// are accepted — the pipeline's obligation is fulfilled.
//
// The DAG node advancement is handled by the existing handoff_to_ot
// flow which publishes the pipeline success update. The global review
// (per-merge audit replicas) runs independently on the MergeDescriptor
// produced by the merge, and is a separate concern from the pipeline's
// claims board.
func (pi *PipelineInspector) wireClaimsBoardLifecycle() {
	if pi.claimsBoard == nil {
		return
	}

	pi.claimsBoard.SubscribeProjection(func(p *claims.ClaimsBoardProjection) error {
		if p == nil || p.Phase != claims.BoardPhaseComplete {
			return nil
		}

		pipelineID := strings.TrimSpace(pi.pipelineID)
		slog.Info("claims_board_complete",
			"pipeline_id", pipelineID,
			"task_id", p.TaskID,
			"board_id", p.BoardID,
			"total_claims", p.TotalClaims,
			"accepted_count", p.AcceptedCount,
			"iteration", p.Iteration,
		)

		// The VFS merge already happened at handoff_to_ot time.
		// Board complete = all claims accepted = pipeline work is done.
		// The handoff_to_ot flow handles DAG node advancement and
		// global review dispatch. Nothing else to do here.
		return nil
	})
}
