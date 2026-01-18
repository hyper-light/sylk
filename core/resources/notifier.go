package resources

import (
	"log/slog"
	"sync"
)

// ResourceNotifier defines the interface for notifying about resource waiting events.
// Used to signal the UI when an agent is waiting for a resource to become available.
type ResourceNotifier interface {
	// AgentWaiting is called when an agent starts waiting for a resource.
	AgentWaiting(sessionID, agentID, resource string)
	// AgentProceeding is called when an agent has acquired the resource and can proceed.
	AgentProceeding(sessionID, agentID, resource string)
}

// NoOpNotifier is a ResourceNotifier that does nothing.
// Useful for testing and when notifications are not needed.
type NoOpNotifier struct{}

// AgentWaiting does nothing.
func (n *NoOpNotifier) AgentWaiting(sessionID, agentID, resource string) {}

// AgentProceeding does nothing.
func (n *NoOpNotifier) AgentProceeding(sessionID, agentID, resource string) {}

// LoggingNotifier logs resource waiting events using slog.
type LoggingNotifier struct {
	logger *slog.Logger
}

// NewLoggingNotifier creates a new LoggingNotifier with the given logger.
func NewLoggingNotifier(logger *slog.Logger) *LoggingNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggingNotifier{logger: logger}
}

// AgentWaiting logs that an agent is waiting for a resource.
func (n *LoggingNotifier) AgentWaiting(sessionID, agentID, resource string) {
	n.logger.Info("agent waiting for resource",
		"session_id", sessionID,
		"agent_id", agentID,
		"resource", resource,
	)
}

// AgentProceeding logs that an agent has acquired the resource.
func (n *LoggingNotifier) AgentProceeding(sessionID, agentID, resource string) {
	n.logger.Info("agent proceeding with resource",
		"session_id", sessionID,
		"agent_id", agentID,
		"resource", resource,
	)
}

// CompositeNotifier fans out notifications to multiple notifiers.
type CompositeNotifier struct {
	mu        sync.RWMutex
	notifiers []ResourceNotifier
}

// NewCompositeNotifier creates a new CompositeNotifier with the given notifiers.
func NewCompositeNotifier(notifiers ...ResourceNotifier) *CompositeNotifier {
	filtered := make([]ResourceNotifier, 0, len(notifiers))
	for _, n := range notifiers {
		if n != nil {
			filtered = append(filtered, n)
		}
	}
	return &CompositeNotifier{notifiers: filtered}
}

// Add adds a notifier to the composite.
func (c *CompositeNotifier) Add(notifier ResourceNotifier) {
	if notifier == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifiers = append(c.notifiers, notifier)
}

// AgentWaiting notifies all child notifiers that an agent is waiting.
func (c *CompositeNotifier) AgentWaiting(sessionID, agentID, resource string) {
	c.mu.RLock()
	notifiers := c.notifiers
	c.mu.RUnlock()

	for _, n := range notifiers {
		n.AgentWaiting(sessionID, agentID, resource)
	}
}

// AgentProceeding notifies all child notifiers that an agent is proceeding.
func (c *CompositeNotifier) AgentProceeding(sessionID, agentID, resource string) {
	c.mu.RLock()
	notifiers := c.notifiers
	c.mu.RUnlock()

	for _, n := range notifiers {
		n.AgentProceeding(sessionID, agentID, resource)
	}
}

// Compile-time interface checks.
var (
	_ ResourceNotifier = (*NoOpNotifier)(nil)
	_ ResourceNotifier = (*LoggingNotifier)(nil)
	_ ResourceNotifier = (*CompositeNotifier)(nil)
)
