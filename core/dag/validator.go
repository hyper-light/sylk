package dag

import (
	"sort"
)

// =============================================================================
// Validator
// =============================================================================

// Validator validates DAG structure and computes execution order
type Validator struct{}

// NewValidator creates a new DAG validator
func NewValidator() *Validator {
	return &Validator{}
}

// Validate validates a DAG and computes execution order
func (v *Validator) Validate(d *DAG) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check for empty DAG
	if len(d.nodes) == 0 {
		return ErrEmptyDAG
	}

	// Validate all dependencies exist
	if err := v.validateDependencies(d); err != nil {
		return err
	}

	// Build dependency graph and compute reverse dependencies
	v.buildDependencyGraph(d)

	// Check for cycles and compute topological order
	order, err := v.topologicalSort(d)
	if err != nil {
		return err
	}

	// Compute execution layers
	layers := v.computeLayers(d, order)

	// Sort nodes within each layer by priority
	v.sortLayersByPriority(d, layers)

	// Store execution order
	d.executionOrder = layers
	d.validated = true

	return nil
}

// validateDependencies checks that all dependencies exist
func (v *Validator) validateDependencies(d *DAG) error {
	for _, node := range d.nodes {
		for _, depID := range node.dependencies {
			if _, exists := d.nodes[depID]; !exists {
				return ErrMissingDependency
			}
		}
	}
	return nil
}

// buildDependencyGraph builds reverse dependency mappings
func (v *Validator) buildDependencyGraph(d *DAG) {
	// Clear existing dependents
	for _, node := range d.nodes {
		node.dependents = []string{}
	}

	// Build reverse mappings
	for id, node := range d.nodes {
		for _, depID := range node.dependencies {
			if depNode, exists := d.nodes[depID]; exists {
				depNode.addDependent(id)
			}
		}
	}
}

// topologicalSort performs Kahn's algorithm to detect cycles and compute order
func (v *Validator) topologicalSort(d *DAG) ([]string, error) {
	inDegree := v.buildInDegreeMap(d)
	queue := v.collectZeroInDegree(inDegree)
	result := v.processTopoQueue(d, inDegree, queue)

	if v.hasCycle(result, d.nodes) {
		return nil, ErrCyclicDependency
	}

	return result, nil
}

func (v *Validator) buildInDegreeMap(d *DAG) map[string]int {
	inDegree := make(map[string]int)
	for id, node := range d.nodes {
		inDegree[id] = len(node.dependencies)
	}
	return inDegree
}

func (v *Validator) collectZeroInDegree(inDegree map[string]int) []string {
	queue := make([]string, 0)
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	return queue
}

func (v *Validator) processTopoQueue(d *DAG, inDegree map[string]int, queue []string) []string {
	result := make([]string, 0, len(d.nodes))
	for len(queue) > 0 {
		var nodeID string
		nodeID, queue = popQueue(queue)
		result = append(result, nodeID)
		queue = v.enqueueDependents(d, nodeID, inDegree, queue)
	}
	return result
}

func (v *Validator) enqueueDependents(d *DAG, nodeID string, inDegree map[string]int, queue []string) []string {
	node := d.nodes[nodeID]
	for _, depID := range node.dependents {
		inDegree[depID]--
		if inDegree[depID] == 0 {
			queue = append(queue, depID)
		}
	}
	return queue
}

func (v *Validator) hasCycle(order []string, nodes map[string]*Node) bool {
	return len(order) != len(nodes)
}

func popQueue(queue []string) (string, []string) {
	nodeID := queue[0]
	return nodeID, queue[1:]
}

// computeLayers groups nodes into execution layers
func (v *Validator) computeLayers(d *DAG, order []string) [][]string {
	layerMap := v.buildLayerMap(d, order)
	layers := v.allocateLayers(layerMap)
	v.fillLayers(layers, layerMap)
	return layers
}

func (v *Validator) buildLayerMap(d *DAG, order []string) map[string]int {
	layerMap := make(map[string]int)
	for _, nodeID := range order {
		layer := v.resolveLayer(d.nodes[nodeID], layerMap)
		layerMap[nodeID] = layer
		d.nodes[nodeID].setLayer(layer)
	}
	return layerMap
}

func (v *Validator) resolveLayer(node *Node, layerMap map[string]int) int {
	maxDepLayer := -1
	for _, depID := range node.dependencies {
		if layer, ok := layerMap[depID]; ok && layer > maxDepLayer {
			maxDepLayer = layer
		}
	}
	return maxDepLayer + 1
}

func (v *Validator) allocateLayers(layerMap map[string]int) [][]string {
	maxLayer := 0
	for _, layer := range layerMap {
		if layer > maxLayer {
			maxLayer = layer
		}
	}

	layers := make([][]string, maxLayer+1)
	for i := range layers {
		layers[i] = make([]string, 0)
	}
	return layers
}

func (v *Validator) fillLayers(layers [][]string, layerMap map[string]int) {
	for nodeID, layer := range layerMap {
		layers[layer] = append(layers[layer], nodeID)
	}
}

// sortLayersByPriority sorts nodes within each layer by priority (descending)
func (v *Validator) sortLayersByPriority(d *DAG, layers [][]string) {
	for i := range layers {
		sort.Slice(layers[i], func(a, b int) bool {
			nodeA := d.nodes[layers[i][a]]
			nodeB := d.nodes[layers[i][b]]
			return nodeA.priority > nodeB.priority
		})
	}
}

// =============================================================================
// Validation Helpers
// =============================================================================

// ValidateDAG validates a DAG and returns any errors
func ValidateDAG(d *DAG) error {
	v := NewValidator()
	return v.Validate(d)
}

// IsValid checks if a DAG is valid without modifying it
func IsValid(d *DAG) bool {
	d.mu.RLock()
	nodeCount := len(d.nodes)
	d.mu.RUnlock()

	if nodeCount == 0 {
		return false
	}

	// Create a temporary copy for validation
	temp := NewDAG("temp", d.policy)
	for _, node := range d.Nodes() {
		temp.AddNode(node.ToConfig())
	}

	v := NewValidator()
	return v.Validate(temp) == nil
}
