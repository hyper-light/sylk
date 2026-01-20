// Package concurrency provides health monitoring hooks for observability.
// This file implements W4N.18: HealthMonitor interface for callbacks/metrics.
package concurrency

import "time"

// HealthEventType categorizes different health events for monitoring.
type HealthEventType int

const (
	// EventMessageDropped indicates a message was dropped due to queue full.
	EventMessageDropped HealthEventType = iota
	// EventChannelResized indicates channel buffer was resized.
	EventChannelResized
	// EventQueueDepthHigh indicates queue depth exceeded threshold.
	EventQueueDepthHigh
	// EventQueueDepthNormal indicates queue depth returned to normal.
	EventQueueDepthNormal
	// EventShutdownStarted indicates graceful shutdown has begun.
	EventShutdownStarted
	// EventShutdownComplete indicates shutdown finished.
	EventShutdownComplete
	// EventWorkerStarted indicates a new worker goroutine started.
	EventWorkerStarted
	// EventWorkerStopped indicates a worker goroutine stopped.
	EventWorkerStopped
	// EventLeakDetected indicates potential goroutine leak.
	EventLeakDetected
	// EventSyncFailure indicates disk sync operation failed.
	EventSyncFailure
	// EventSyncRecovered indicates sync recovered after failures.
	EventSyncRecovered
)

// String returns human-readable name for the event type.
func (e HealthEventType) String() string {
	names := [...]string{
		"MessageDropped",
		"ChannelResized",
		"QueueDepthHigh",
		"QueueDepthNormal",
		"ShutdownStarted",
		"ShutdownComplete",
		"WorkerStarted",
		"WorkerStopped",
		"LeakDetected",
		"SyncFailure",
		"SyncRecovered",
	}
	if int(e) < len(names) {
		return names[e]
	}
	return "Unknown"
}

// HealthEvent contains details about a health-related event.
type HealthEvent struct {
	Type      HealthEventType
	Source    string    // Component name (e.g., "AdaptiveChannel", "DualQueueGate")
	Timestamp time.Time // When the event occurred

	// Optional fields depending on event type
	Count     int64  // For counters (dropped messages, queue depth)
	OldValue  int64  // For resize events (old size)
	NewValue  int64  // For resize events (new size)
	Message   string // Additional context
	Error     error  // For failure events
}

// HealthMonitor defines the interface for receiving health events.
// Implementations must be thread-safe as callbacks may be invoked concurrently.
type HealthMonitor interface {
	// OnHealthEvent is called when a health event occurs.
	// Implementations should not block as this may affect performance.
	// Errors returned are logged but do not affect the calling component.
	OnHealthEvent(event HealthEvent)
}

// HealthMonitorFunc allows using a function as a HealthMonitor.
type HealthMonitorFunc func(event HealthEvent)

// OnHealthEvent implements HealthMonitor by calling the function.
func (f HealthMonitorFunc) OnHealthEvent(event HealthEvent) {
	f(event)
}

// MultiHealthMonitor fans out events to multiple monitors.
type MultiHealthMonitor struct {
	monitors []HealthMonitor
}

// NewMultiHealthMonitor creates a monitor that broadcasts to all provided monitors.
func NewMultiHealthMonitor(monitors ...HealthMonitor) *MultiHealthMonitor {
	return &MultiHealthMonitor{monitors: monitors}
}

// OnHealthEvent broadcasts the event to all registered monitors.
func (m *MultiHealthMonitor) OnHealthEvent(event HealthEvent) {
	for _, monitor := range m.monitors {
		m.safeInvoke(monitor, event)
	}
}

// safeInvoke calls the monitor's OnHealthEvent, recovering from panics.
func (m *MultiHealthMonitor) safeInvoke(monitor HealthMonitor, event HealthEvent) {
	defer func() {
		_ = recover() // Ignore panics from misbehaving monitors
	}()
	monitor.OnHealthEvent(event)
}

// emitHealthEvent safely emits an event to a HealthMonitor if non-nil.
// This is a helper function used by components to emit events.
func emitHealthEvent(monitor HealthMonitor, event HealthEvent) {
	if monitor == nil {
		return
	}
	safeEmitEvent(monitor, event)
}

// safeEmitEvent calls the monitor, recovering from panics to prevent crashes.
func safeEmitEvent(monitor HealthMonitor, event HealthEvent) {
	defer func() {
		_ = recover() // Don't let monitor panics crash the system
	}()
	monitor.OnHealthEvent(event)
}

// newHealthEvent creates a new HealthEvent with common fields populated.
func newHealthEvent(eventType HealthEventType, source string) HealthEvent {
	return HealthEvent{
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now(),
	}
}
