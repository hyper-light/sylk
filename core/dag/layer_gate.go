package dag

import "context"

// LayerGate is a function invoked between DAG layers. It receives the completed
// layer index and node results, and returns an error to block the next layer.
// A nil return allows execution to proceed to the next layer.
type LayerGate func(ctx context.Context, dagID string, layerIdx int, nodeResults map[string]*NodeResult) error
