package dag

import (
	"sync"
	"time"
)

// =============================================================================
// Node
// =============================================================================

// Node represents a single node in a DAG
type Node struct {
	mu sync.RWMutex

	// Configuration
	id           string
	agentType    string
	prompt       string
	context      map[string]any
	dependencies []string
	timeout      time.Duration
	retries      int
	priority     int
	metadata     map[string]any

	// Runtime state
	state      NodeState
	result     *NodeResult
	startTime  time.Time
	retryCount int

	// Computed
	dependents []string // Nodes that depend on this node
	layer      int      // Execution layer (computed during topological sort)
}

// NewNode creates a new DAG node from configuration
func NewNode(cfg NodeConfig) *Node {
	ctx := cfg.Context
	if ctx == nil {
		ctx = make(map[string]any)
	}

	meta := cfg.Metadata
	if meta == nil {
		meta = make(map[string]any)
	}

	deps := cfg.Dependencies
	if deps == nil {
		deps = []string{}
	}

	return &Node{
		id:           cfg.ID,
		agentType:    cfg.AgentType,
		prompt:       cfg.Prompt,
		context:      ctx,
		dependencies: deps,
		timeout:      cfg.Timeout,
		retries:      cfg.Retries,
		priority:     cfg.Priority,
		metadata:     meta,
		state:        NodeStatePending,
		dependents:   []string{},
	}
}

// =============================================================================
// Getters
// =============================================================================

// ID returns the node ID
func (n *Node) ID() string {
	return n.id
}

// AgentType returns the agent type
func (n *Node) AgentType() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.agentType
}

// Prompt returns the task prompt
func (n *Node) Prompt() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.prompt
}

// Context returns the node context
func (n *Node) Context() map[string]any {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make(map[string]any, len(n.context))
	for k, v := range n.context {
		result[k] = v
	}
	return result
}

// Dependencies returns the dependency node IDs
func (n *Node) Dependencies() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make([]string, len(n.dependencies))
	copy(result, n.dependencies)
	return result
}

// Dependents returns the nodes that depend on this node
func (n *Node) Dependents() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make([]string, len(n.dependents))
	copy(result, n.dependents)
	return result
}

// Timeout returns the node timeout
func (n *Node) Timeout() time.Duration {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.timeout
}

// Retries returns the maximum retry count
func (n *Node) Retries() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.retries
}

// Priority returns the node priority
func (n *Node) Priority() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.priority
}

// Layer returns the execution layer
func (n *Node) Layer() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.layer
}

// Metadata returns a copy of the metadata
func (n *Node) Metadata() map[string]any {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make(map[string]any, len(n.metadata))
	for k, v := range n.metadata {
		result[k] = v
	}
	return result
}

// =============================================================================
// State Management
// =============================================================================

// State returns the current node state
func (n *Node) State() NodeState {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.state
}

// SetState sets the node state
func (n *Node) SetState(state NodeState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.state = state
}

// Result returns the node result
func (n *Node) Result() *NodeResult {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.result
}

// SetResult sets the node result
func (n *Node) SetResult(result *NodeResult) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.result = result
	if result != nil {
		n.state = result.State
	}
}

// StartTime returns when execution started
func (n *Node) StartTime() time.Time {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.startTime
}

// SetStartTime sets when execution started
func (n *Node) SetStartTime(t time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.startTime = t
}

// RetryCount returns the current retry count
func (n *Node) RetryCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.retryCount
}

// IncrementRetryCount increments the retry count
func (n *Node) IncrementRetryCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.retryCount++
	return n.retryCount
}

// =============================================================================
// Internal Setters
// =============================================================================

// setLayer sets the execution layer (called during topological sort)
func (n *Node) setLayer(layer int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.layer = layer
}

// addDependent adds a dependent node
func (n *Node) addDependent(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dependents = append(n.dependents, id)
}

// setContext sets the node context
func (n *Node) setContext(key string, value any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.context[key] = value
}

// =============================================================================
// Helper Methods
// =============================================================================

// HasDependencies returns true if the node has dependencies
func (n *Node) HasDependencies() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.dependencies) > 0
}

// HasDependents returns true if other nodes depend on this node
func (n *Node) HasDependents() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.dependents) > 0
}

// IsReady returns true if all dependencies have succeeded
func (n *Node) IsReady(nodeStates map[string]NodeState) bool {
	n.mu.RLock()
	deps := n.dependencies
	n.mu.RUnlock()

	for _, depID := range deps {
		state, ok := nodeStates[depID]
		if !ok || state != NodeStateSucceeded {
			return false
		}
	}
	return true
}

// IsBlocked returns true if any dependency failed
func (n *Node) IsBlocked(nodeStates map[string]NodeState) bool {
	n.mu.RLock()
	deps := n.dependencies
	n.mu.RUnlock()

	for _, depID := range deps {
		if n.isDependencyBlocked(nodeStates, depID) {
			return true
		}
	}
	return false
}

func (n *Node) isDependencyBlocked(nodeStates map[string]NodeState, depID string) bool {
	state, ok := nodeStates[depID]
	return ok && isBlockingState(state)
}

func isBlockingState(state NodeState) bool {
	return state == NodeStateFailed || state == NodeStateBlocked ||
		state == NodeStateCancelled || state == NodeStateSkipped
}

// CanRetry returns true if the node can be retried
func (n *Node) CanRetry() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.retryCount < n.retries
}

// Clone creates a shallow copy of the node (for status reporting)
func (n *Node) Clone() *Node {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return &Node{
		id:           n.id,
		agentType:    n.agentType,
		prompt:       n.prompt,
		context:      n.context,
		dependencies: n.dependencies,
		timeout:      n.timeout,
		retries:      n.retries,
		priority:     n.priority,
		metadata:     n.metadata,
		state:        n.state,
		result:       n.result,
		startTime:    n.startTime,
		retryCount:   n.retryCount,
		dependents:   n.dependents,
		layer:        n.layer,
	}
}

// ToConfig converts the node back to configuration
func (n *Node) ToConfig() NodeConfig {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return NodeConfig{
		ID:           n.id,
		AgentType:    n.agentType,
		Prompt:       n.prompt,
		Context:      n.context,
		Dependencies: n.dependencies,
		Timeout:      n.timeout,
		Retries:      n.retries,
		Priority:     n.priority,
		Metadata:     n.metadata,
	}
}
