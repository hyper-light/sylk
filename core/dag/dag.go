package dag

import (
	"encoding/json"
	"maps"
	"sync"

	"github.com/google/uuid"
)

// =============================================================================
// DAG
// =============================================================================

// DAG represents a directed acyclic graph of tasks
type DAG struct {
	mu sync.RWMutex

	// Identity
	id          string
	name        string
	description string

	// Nodes
	nodes map[string]*Node

	// Execution order (computed during validation)
	executionOrder [][]string // Each inner slice is a layer of parallel nodes

	// Policy
	policy ExecutionPolicy

	// Metadata
	metadata map[string]any

	// State
	validated bool
}

// dagSnapshot is used for serialization
type dagSnapshot struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Nodes          []NodeConfig    `json:"nodes"`
	Policy         ExecutionPolicy `json:"policy"`
	ExecutionOrder [][]string      `json:"execution_order"`
	Metadata       map[string]any  `json:"metadata"`
}

// NewDAG creates a new DAG
func NewDAG(name string, policy ExecutionPolicy) *DAG {
	return &DAG{
		id:       uuid.New().String(),
		name:     name,
		nodes:    make(map[string]*Node),
		policy:   policy,
		metadata: make(map[string]any),
	}
}

// NewDAGWithID creates a new DAG with a specific ID
func NewDAGWithID(id, name string, policy ExecutionPolicy) *DAG {
	return &DAG{
		id:       id,
		name:     name,
		nodes:    make(map[string]*Node),
		policy:   policy,
		metadata: make(map[string]any),
	}
}

// =============================================================================
// Identity
// =============================================================================

// ID returns the DAG ID
func (d *DAG) ID() string {
	return d.id
}

// Name returns the DAG name
func (d *DAG) Name() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.name
}

// Description returns the DAG description
func (d *DAG) Description() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.description
}

// SetDescription sets the DAG description
func (d *DAG) SetDescription(desc string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.description = desc
}

// =============================================================================
// Node Management
// =============================================================================

// AddNode adds a node to the DAG
func (d *DAG) AddNode(cfg NodeConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if cfg.ID == "" {
		cfg.ID = uuid.New().String()
	}

	if _, exists := d.nodes[cfg.ID]; exists {
		return ErrDuplicateNode
	}

	d.nodes[cfg.ID] = NewNode(cfg)
	d.validated = false
	d.executionOrder = nil

	return nil
}

// GetNode returns a node by ID
func (d *DAG) GetNode(id string) (*Node, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	node, ok := d.nodes[id]
	return node, ok
}

// RemoveNode removes a node from the DAG
func (d *DAG) RemoveNode(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.nodes[id]; !exists {
		return ErrNodeNotFound
	}

	delete(d.nodes, id)
	d.validated = false
	d.executionOrder = nil

	return nil
}

// Nodes returns all nodes
func (d *DAG) Nodes() []*Node {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*Node, 0, len(d.nodes))
	for _, node := range d.nodes {
		result = append(result, node)
	}
	return result
}

// NodeCount returns the number of nodes
func (d *DAG) NodeCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.nodes)
}

// =============================================================================
// Execution Order
// =============================================================================

// ExecutionOrder returns the computed execution order
func (d *DAG) ExecutionOrder() [][]string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.executionOrder == nil {
		return nil
	}

	// Return a copy
	result := make([][]string, len(d.executionOrder))
	for i, layer := range d.executionOrder {
		result[i] = make([]string, len(layer))
		copy(result[i], layer)
	}
	return result
}

// LayerCount returns the number of execution layers
func (d *DAG) LayerCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.executionOrder)
}

// NodesInLayer returns the node IDs in a specific layer
func (d *DAG) NodesInLayer(layer int) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if layer < 0 || layer >= len(d.executionOrder) {
		return nil
	}

	result := make([]string, len(d.executionOrder[layer]))
	copy(result, d.executionOrder[layer])
	return result
}

// =============================================================================
// Policy
// =============================================================================

// Policy returns the execution policy
func (d *DAG) Policy() ExecutionPolicy {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.policy
}

// SetPolicy sets the execution policy
func (d *DAG) SetPolicy(policy ExecutionPolicy) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policy = policy
}

// =============================================================================
// Metadata
// =============================================================================

// Metadata returns a copy of the metadata
func (d *DAG) Metadata() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]any, len(d.metadata))
	maps.Copy(result, d.metadata)
	return result
}

// GetMetadata returns a single metadata value
func (d *DAG) GetMetadata(key string) (any, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v, ok := d.metadata[key]
	return v, ok
}

// SetMetadata sets a metadata value
func (d *DAG) SetMetadata(key string, value any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metadata[key] = value
}

// =============================================================================
// Validation
// =============================================================================

// IsValidated returns true if the DAG has been validated
func (d *DAG) IsValidated() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.validated
}

// =============================================================================
// Node State Management
// =============================================================================

// GetNodeStates returns the current state of all nodes
func (d *DAG) GetNodeStates() map[string]NodeState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]NodeState, len(d.nodes))
	for id, node := range d.nodes {
		result[id] = node.State()
	}
	return result
}

// ResetNodeStates resets all nodes to pending state
func (d *DAG) ResetNodeStates() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, node := range d.nodes {
		node.SetState(NodeStatePending)
		node.SetResult(nil)
	}
}

// =============================================================================
// Serialization
// =============================================================================

// MarshalJSON implements json.Marshaler
func (d *DAG) MarshalJSON() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	nodeConfigs := make([]NodeConfig, 0, len(d.nodes))
	for _, node := range d.nodes {
		nodeConfigs = append(nodeConfigs, node.ToConfig())
	}

	snapshot := dagSnapshot{
		ID:             d.id,
		Name:           d.name,
		Description:    d.description,
		Nodes:          nodeConfigs,
		Policy:         d.policy,
		ExecutionOrder: d.executionOrder,
		Metadata:       d.metadata,
	}

	return json.Marshal(snapshot)
}

// UnmarshalJSON implements json.Unmarshaler
func (d *DAG) UnmarshalJSON(data []byte) error {
	var snapshot dagSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.id = snapshot.ID
	d.name = snapshot.Name
	d.description = snapshot.Description
	d.policy = snapshot.Policy
	d.executionOrder = snapshot.ExecutionOrder
	d.metadata = snapshot.Metadata
	d.validated = len(snapshot.ExecutionOrder) > 0

	d.nodes = make(map[string]*Node, len(snapshot.Nodes))
	for _, cfg := range snapshot.Nodes {
		d.nodes[cfg.ID] = NewNode(cfg)
	}

	return nil
}

// =============================================================================
// Builder Pattern
// =============================================================================

// Builder creates a DAG using a fluent API
type Builder struct {
	dag    *DAG
	errors []error
}

// NewBuilder creates a new DAG builder
func NewBuilder(name string) *Builder {
	return &Builder{
		dag:    NewDAG(name, DefaultExecutionPolicy()),
		errors: make([]error, 0),
	}
}

// WithID sets the DAG ID
func (b *Builder) WithID(id string) *Builder {
	b.dag.id = id
	return b
}

// WithDescription sets the DAG description
func (b *Builder) WithDescription(desc string) *Builder {
	b.dag.description = desc
	return b
}

// WithPolicy sets the execution policy
func (b *Builder) WithPolicy(policy ExecutionPolicy) *Builder {
	b.dag.policy = policy
	return b
}

// WithMetadata sets a metadata value
func (b *Builder) WithMetadata(key string, value any) *Builder {
	b.dag.metadata[key] = value
	return b
}

// AddNode adds a node to the DAG
func (b *Builder) AddNode(cfg NodeConfig) *Builder {
	if err := b.dag.AddNode(cfg); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// Build validates and returns the DAG
func (b *Builder) Build() (*DAG, error) {
	if len(b.errors) > 0 {
		return nil, b.errors[0]
	}

	// Validate the DAG
	validator := NewValidator()
	if err := validator.Validate(b.dag); err != nil {
		return nil, err
	}

	return b.dag, nil
}

// MustBuild builds the DAG and panics on error
func (b *Builder) MustBuild() *DAG {
	dag, err := b.Build()
	if err != nil {
		panic(err)
	}
	return dag
}
