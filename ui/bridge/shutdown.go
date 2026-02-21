package bridge

import "context"

// shouldStop reports whether a bridge drain loop should exit immediately.
// It prioritizes shutdown signals before processing buffered events.
func shouldStop(done <-chan struct{}, ctx context.Context) (bool, error) {
	select {
	case <-done:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	default:
		return false, nil
	}
}
