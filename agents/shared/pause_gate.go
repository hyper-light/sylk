package shared

import (
	"context"

	"github.com/adalundhe/sylk/core/steering"
)

// WaitWhileExecutionPaused blocks before starting a new unit of work when the
// current request ledger is explicitly paused. Step mode is intentionally not
// handled here; step continues to pause only at normal turn boundaries.
func WaitWhileExecutionPaused(ctx context.Context) error {
	ledger := SteeringLedgerFromContext(ctx)
	if ledger == nil {
		return nil
	}
	for ledger.Pace() == steering.PacePaused {
		if err := ledger.WaitForResume(ctx); err != nil {
			return err
		}
	}
	return nil
}
