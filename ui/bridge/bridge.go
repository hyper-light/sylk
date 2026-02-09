package bridge

// TeaProgram is the subset of the Bubble Tea program interface used by bridges.
// Accepting this narrow interface avoids importing bubbletea into the bridge
// package, keeping the dependency graph clean.
type TeaProgram interface {
	Send(msg any)
}

// Bridge converts core system events into Bubble Tea messages.
// All bridges are tracked via GoroutineScope so no goroutine is untracked.
type Bridge interface {
	// Start begins forwarding events to the given Bubble Tea program.
	Start(program TeaProgram) error

	// Stop gracefully shuts down the bridge and releases resources.
	Stop()

	// Name returns a human-readable identifier for this bridge.
	Name() string

	// DroppedCount returns the number of events dropped due to backpressure.
	DroppedCount() int64
}
