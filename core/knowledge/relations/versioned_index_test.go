package relations

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// IndexSnapshot Tests
// =============================================================================

func TestIndexSnapshot_Creation(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	snapshot := NewIndexSnapshot(1, data)

	assert.Equal(t, uint64(1), snapshot.Version())
	assert.WithinDuration(t, time.Now(), snapshot.CreatedAt(), time.Second)
}

func TestIndexSnapshot_Immutability(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true},
	}

	snapshot := NewIndexSnapshot(1, data)

	// Modify original data
	data["A"]["B"] = false
	data["A"]["C"] = true
	data["D"] = map[string]bool{"E": true}

	// Snapshot should be unaffected
	assert.True(t, snapshot.Lookup("A", "B"), "Original data modification should not affect snapshot")
	assert.False(t, snapshot.Lookup("A", "C"), "New additions to original should not affect snapshot")
	assert.False(t, snapshot.Lookup("D", "E"), "New subjects in original should not affect snapshot")
}

func TestIndexSnapshot_Lookup(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": false},
		"B": {"C": true},
	}

	snapshot := NewIndexSnapshot(1, data)

	// Existing reachable pairs
	assert.True(t, snapshot.Lookup("A", "B"))
	assert.True(t, snapshot.Lookup("A", "C"))
	assert.True(t, snapshot.Lookup("B", "C"))

	// Explicitly not reachable
	assert.False(t, snapshot.Lookup("A", "D"))

	// Non-existent pairs
	assert.False(t, snapshot.Lookup("C", "A"))
	assert.False(t, snapshot.Lookup("X", "Y"))
	assert.False(t, snapshot.Lookup("A", "X"))
}

func TestIndexSnapshot_Lookup_NilData(t *testing.T) {
	snapshot := &IndexSnapshot{
		version:   1,
		data:      nil,
		createdAt: time.Now(),
	}

	assert.False(t, snapshot.Lookup("A", "B"))
}

func TestIndexSnapshot_GetReachable(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true},
		"C": {},
	}

	snapshot := NewIndexSnapshot(1, data)

	// Test reachable from A
	reachableFromA := snapshot.GetReachable("A")
	assert.Equal(t, []string{"B", "C", "D"}, reachableFromA)

	// Test reachable from B
	reachableFromB := snapshot.GetReachable("B")
	assert.Equal(t, []string{"C"}, reachableFromB)

	// Test reachable from C (empty)
	reachableFromC := snapshot.GetReachable("C")
	assert.Empty(t, reachableFromC)

	// Test non-existent subject
	reachableFromX := snapshot.GetReachable("X")
	assert.Nil(t, reachableFromX)
}

func TestIndexSnapshot_GetReachable_ExcludesFalse(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true, "C": false, "D": true},
	}

	snapshot := NewIndexSnapshot(1, data)
	reachable := snapshot.GetReachable("A")

	assert.Equal(t, []string{"B", "D"}, reachable)
	assert.NotContains(t, reachable, "C")
}

func TestIndexSnapshot_GetSubjects(t *testing.T) {
	data := map[string]map[string]bool{
		"C": {"D": true},
		"A": {"B": true},
		"B": {"C": true},
	}

	snapshot := NewIndexSnapshot(1, data)
	subjects := snapshot.GetSubjects()

	// Should be sorted
	assert.Equal(t, []string{"A", "B", "C"}, subjects)
}

func TestIndexSnapshot_Size(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true, "D": false}, // D is false, shouldn't count
	}

	snapshot := NewIndexSnapshot(1, data)

	// Should count only true values: A->B, A->C, B->C = 3
	assert.Equal(t, 3, snapshot.Size())
}

func TestIndexSnapshot_Size_Empty(t *testing.T) {
	snapshot := NewIndexSnapshot(1, nil)
	assert.Equal(t, 0, snapshot.Size())

	snapshot2 := NewIndexSnapshot(1, map[string]map[string]bool{})
	assert.Equal(t, 0, snapshot2.Size())
}

func TestIndexSnapshot_SubjectCount(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true},
		"B": {"C": true},
		"C": {},
	}

	snapshot := NewIndexSnapshot(1, data)
	assert.Equal(t, 3, snapshot.SubjectCount())
}

func TestIndexSnapshot_VersionIncrement(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true},
	}

	snapshot1 := NewIndexSnapshot(1, data)
	snapshot2 := NewIndexSnapshot(2, data)
	snapshot3 := NewIndexSnapshot(3, data)

	assert.Equal(t, uint64(1), snapshot1.Version())
	assert.Equal(t, uint64(2), snapshot2.Version())
	assert.Equal(t, uint64(3), snapshot3.Version())
}

// =============================================================================
// IntervalLabel Tests
// =============================================================================

func TestIntervalLabel_Contains(t *testing.T) {
	// Parent: [1, 10], Child: [3, 8]
	parent := IntervalLabel{Pre: 1, Post: 10, Level: 0}
	child := IntervalLabel{Pre: 3, Post: 8, Level: 1}
	sibling := IntervalLabel{Pre: 12, Post: 15, Level: 1}

	assert.True(t, parent.Contains(child), "Parent should contain child")
	assert.False(t, child.Contains(parent), "Child should not contain parent")
	assert.False(t, parent.Contains(sibling), "Parent should not contain sibling")
	assert.True(t, parent.Contains(parent), "Node should contain itself")
}

func TestIntervalLabel_IsAncestorOf(t *testing.T) {
	ancestor := IntervalLabel{Pre: 1, Post: 20, Level: 0}
	descendant := IntervalLabel{Pre: 5, Post: 15, Level: 2}

	assert.True(t, ancestor.IsAncestorOf(descendant))
	assert.False(t, descendant.IsAncestorOf(ancestor))
}

func TestIntervalLabel_IsDescendantOf(t *testing.T) {
	ancestor := IntervalLabel{Pre: 1, Post: 20, Level: 0}
	descendant := IntervalLabel{Pre: 5, Post: 15, Level: 2}

	assert.True(t, descendant.IsDescendantOf(ancestor))
	assert.False(t, ancestor.IsDescendantOf(descendant))
}

func TestIntervalLabel_IsValid(t *testing.T) {
	valid := IntervalLabel{Pre: 1, Post: 10, Level: 0}
	assert.True(t, valid.IsValid())

	invalid1 := IntervalLabel{Pre: 10, Post: 5, Level: 0} // Pre > Post
	assert.False(t, invalid1.IsValid())

	invalid2 := IntervalLabel{Pre: 5, Post: 5, Level: 0} // Pre == Post
	assert.False(t, invalid2.IsValid())

	invalid3 := IntervalLabel{Pre: -1, Post: 5, Level: 0} // Negative Pre
	assert.False(t, invalid3.IsValid())
}

// =============================================================================
// ComputeIntervalLabels Tests
// =============================================================================

func TestComputeIntervalLabels_SimpleChain(t *testing.T) {
	// A -> B -> C
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	labels := ComputeIntervalLabels(graph)

	require.Len(t, labels, 3)

	// A should be ancestor of B and C
	assert.True(t, labels["A"].IsAncestorOf(labels["B"]))
	assert.True(t, labels["A"].IsAncestorOf(labels["C"]))
	assert.True(t, labels["B"].IsAncestorOf(labels["C"]))

	// Verify levels
	assert.Equal(t, 0, labels["A"].Level)
	assert.Equal(t, 1, labels["B"].Level)
	assert.Equal(t, 2, labels["C"].Level)
}

func TestComputeIntervalLabels_BinaryTree(t *testing.T) {
	//       A
	//      / \
	//     B   C
	//    / \
	//   D   E
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D", "E"},
		"C": {},
		"D": {},
		"E": {},
	}

	labels := ComputeIntervalLabels(graph)

	require.Len(t, labels, 5)

	// A is ancestor of all
	assert.True(t, labels["A"].IsAncestorOf(labels["B"]))
	assert.True(t, labels["A"].IsAncestorOf(labels["C"]))
	assert.True(t, labels["A"].IsAncestorOf(labels["D"]))
	assert.True(t, labels["A"].IsAncestorOf(labels["E"]))

	// B is ancestor of D and E
	assert.True(t, labels["B"].IsAncestorOf(labels["D"]))
	assert.True(t, labels["B"].IsAncestorOf(labels["E"]))

	// C is not ancestor of D or E
	assert.False(t, labels["C"].IsAncestorOf(labels["D"]))
	assert.False(t, labels["C"].IsAncestorOf(labels["E"]))
}

func TestComputeIntervalLabels_Diamond(t *testing.T) {
	// Diamond DAG:
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {},
	}

	labels := ComputeIntervalLabels(graph)

	require.Len(t, labels, 4)

	// A should be ancestor of all
	assert.True(t, labels["A"].IsAncestorOf(labels["B"]))
	assert.True(t, labels["A"].IsAncestorOf(labels["C"]))
	assert.True(t, labels["A"].IsAncestorOf(labels["D"]))

	// Note: In a DAG, interval containment provides a conservative approximation.
	// D will be a descendant of whichever path DFS takes first (B or C in sorted order).
	// B comes before C alphabetically, so B->D is the tree edge.
	assert.True(t, labels["B"].IsAncestorOf(labels["D"]))
}

func TestComputeIntervalLabels_Cycle(t *testing.T) {
	// Cycle: A -> B -> C -> A
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}

	labels := ComputeIntervalLabels(graph)

	// All nodes should have labels
	require.Len(t, labels, 3)

	// All labels should be valid
	for node, label := range labels {
		assert.True(t, label.IsValid(), "Label for %s should be valid", node)
	}

	// In a cycle, DFS will visit each node once
	// A (first alphabetically) starts the DFS
	assert.Equal(t, 0, labels["A"].Level)
	assert.Equal(t, 1, labels["B"].Level)
	assert.Equal(t, 2, labels["C"].Level)
}

func TestComputeIntervalLabels_DisconnectedComponents(t *testing.T) {
	// Two disconnected components:
	// Component 1: A -> B
	// Component 2: C -> D
	graph := map[string][]string{
		"A": {"B"},
		"B": {},
		"C": {"D"},
		"D": {},
	}

	labels := ComputeIntervalLabels(graph)

	require.Len(t, labels, 4)

	// Each component has its own tree
	assert.True(t, labels["A"].IsAncestorOf(labels["B"]))
	assert.True(t, labels["C"].IsAncestorOf(labels["D"]))

	// Cross-component: no ancestor relationship
	assert.False(t, labels["A"].IsAncestorOf(labels["C"]))
	assert.False(t, labels["A"].IsAncestorOf(labels["D"]))
	assert.False(t, labels["C"].IsAncestorOf(labels["A"]))
	assert.False(t, labels["C"].IsAncestorOf(labels["B"]))
}

func TestComputeIntervalLabels_SingleNode(t *testing.T) {
	graph := map[string][]string{
		"A": {},
	}

	labels := ComputeIntervalLabels(graph)

	require.Len(t, labels, 1)
	assert.True(t, labels["A"].IsValid())
	assert.Equal(t, 0, labels["A"].Level)
}

func TestComputeIntervalLabels_EmptyGraph(t *testing.T) {
	graph := map[string][]string{}

	labels := ComputeIntervalLabels(graph)

	assert.Empty(t, labels)
}

func TestComputeIntervalLabels_NodeOnlyInTargets(t *testing.T) {
	// Node C only appears as a target
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {},
	}

	labels := ComputeIntervalLabels(graph)

	// C should still be included
	require.Len(t, labels, 3)
	_, exists := labels["C"]
	assert.True(t, exists, "C should have a label even though it only appears as a target")
}

// =============================================================================
// IntervalIndex Tests
// =============================================================================

func TestIntervalIndex_Creation(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	index := NewIntervalIndex(graph)

	assert.Equal(t, 3, index.Size())
	assert.WithinDuration(t, time.Now(), index.CreatedAt(), time.Second)
}

func TestIntervalIndex_IsReachable(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	index := NewIntervalIndex(graph)

	assert.True(t, index.IsReachable("A", "B"))
	assert.True(t, index.IsReachable("A", "C"))
	assert.True(t, index.IsReachable("B", "C"))

	assert.False(t, index.IsReachable("B", "A"))
	assert.False(t, index.IsReachable("C", "A"))
	assert.False(t, index.IsReachable("X", "Y"))
}

func TestIntervalIndex_GetLabel(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
	}

	index := NewIntervalIndex(graph)

	label, exists := index.GetLabel("A")
	assert.True(t, exists)
	assert.True(t, label.IsValid())

	_, exists = index.GetLabel("X")
	assert.False(t, exists)
}

func TestIntervalIndex_GetDescendants(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {},
		"D": {},
	}

	index := NewIntervalIndex(graph)

	descendants := index.GetDescendants("A")
	assert.Len(t, descendants, 3)
	assert.Contains(t, descendants, "B")
	assert.Contains(t, descendants, "C")
	assert.Contains(t, descendants, "D")

	descendantsOfB := index.GetDescendants("B")
	assert.Contains(t, descendantsOfB, "D")

	descendantsOfD := index.GetDescendants("D")
	assert.Empty(t, descendantsOfD)
}

func TestIntervalIndex_GetAncestors(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	index := NewIntervalIndex(graph)

	ancestorsOfC := index.GetAncestors("C")
	assert.Len(t, ancestorsOfC, 2)
	assert.Contains(t, ancestorsOfC, "A")
	assert.Contains(t, ancestorsOfC, "B")

	ancestorsOfA := index.GetAncestors("A")
	assert.Empty(t, ancestorsOfA)
}

func TestIntervalIndex_FromLabels(t *testing.T) {
	labels := map[string]IntervalLabel{
		"A": {Pre: 1, Post: 6, Level: 0},
		"B": {Pre: 2, Post: 5, Level: 1},
		"C": {Pre: 3, Post: 4, Level: 2},
	}

	index := NewIntervalIndexFromLabels(labels)

	assert.Equal(t, 3, index.Size())
	assert.True(t, index.IsReachable("A", "B"))
	assert.True(t, index.IsReachable("A", "C"))
}

func TestIntervalIndex_Labels(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
	}

	index := NewIntervalIndex(graph)
	labels := index.Labels()

	// Should be a copy
	labels["A"] = IntervalLabel{Pre: 999, Post: 1000, Level: 99}

	// Original should be unchanged
	originalLabel, _ := index.GetLabel("A")
	assert.NotEqual(t, 999, originalLabel.Pre)
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestIntervalLabel_BoundaryConditions(t *testing.T) {
	// Test exact boundary containment
	outer := IntervalLabel{Pre: 1, Post: 10, Level: 0}
	exactMatch := IntervalLabel{Pre: 1, Post: 10, Level: 1}

	// A node's interval contains itself (boundary case)
	assert.True(t, outer.Contains(exactMatch))
}

func TestComputeIntervalLabels_SelfLoop(t *testing.T) {
	// Self-loop: A -> A
	graph := map[string][]string{
		"A": {"A"},
	}

	labels := ComputeIntervalLabels(graph)

	require.Len(t, labels, 1)
	assert.True(t, labels["A"].IsValid())
}

func TestComputeIntervalLabels_LargeGraph(t *testing.T) {
	// Create a larger linear graph: 0 -> 1 -> 2 -> ... -> 99
	graph := make(map[string][]string)
	for i := 0; i < 100; i++ {
		node := string(rune('A' + i%26)) + string(rune('0'+i/26))
		if i < 99 {
			nextNode := string(rune('A'+(i+1)%26)) + string(rune('0'+(i+1)/26))
			graph[node] = []string{nextNode}
		} else {
			graph[node] = []string{}
		}
	}

	labels := ComputeIntervalLabels(graph)

	assert.Len(t, labels, 100)

	// All labels should be valid
	for node, label := range labels {
		assert.True(t, label.IsValid(), "Label for %s should be valid", node)
	}
}

func TestIndexSnapshot_ConcurrentReadSafety(t *testing.T) {
	data := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	snapshot := NewIndexSnapshot(1, data)

	// Simulate concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				snapshot.Lookup("A", "B")
				snapshot.GetReachable("A")
				snapshot.Size()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestIndexSnapshot_WithIntervalIndex_Consistency(t *testing.T) {
	// Create a graph and compute both TC and interval labels
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {},
	}

	// Compute transitive closure manually
	tc := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"D": true},
		"C": {"D": true},
		"D": {},
	}

	snapshot := NewIndexSnapshot(1, tc)
	index := NewIntervalIndex(graph)

	// For tree edges, interval index should agree with TC
	// A -> B (tree edge)
	assert.True(t, snapshot.Lookup("A", "B"))
	assert.True(t, index.IsReachable("A", "B"))

	// A -> D (through tree path)
	assert.True(t, snapshot.Lookup("A", "D"))
	assert.True(t, index.IsReachable("A", "D"))
}

// =============================================================================
// VersionedIntervalIndex Tests
// =============================================================================

func TestVersionedIntervalIndex_Creation(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	assert.NotNil(t, vi)
	assert.Equal(t, uint64(1), vi.Version())
}

func TestVersionedIntervalIndex_GetSnapshot(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	snapshot := vi.GetSnapshot()
	require.NotNil(t, snapshot)

	assert.True(t, snapshot.Lookup("A", "B"))
	assert.False(t, snapshot.Lookup("B", "A"))
}

func TestVersionedIntervalIndex_GetIntervalIndex(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	intervalIndex := vi.GetIntervalIndex()
	require.NotNil(t, intervalIndex)

	assert.True(t, intervalIndex.IsReachable("A", "B"))
	assert.True(t, intervalIndex.IsReachable("A", "C"))
}

func TestVersionedIntervalIndex_GetState(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	snapshot, intervalIndex, version := vi.GetState()

	assert.NotNil(t, snapshot)
	assert.NotNil(t, intervalIndex)
	assert.Equal(t, uint64(1), version)

	// Both should be consistent from the same version
	assert.True(t, snapshot.Lookup("A", "B"))
	assert.True(t, intervalIndex.IsReachable("A", "B"))
}

func TestVersionedIntervalIndex_AtomicSwap(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)
	assert.Equal(t, uint64(1), vi.Version())

	// Create new state
	newGraph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}

	newTcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	newSnapshot := NewIndexSnapshot(2, newTcData)
	newIndex := NewIntervalIndex(newGraph)

	// Perform atomic swap
	newVersion := vi.AtomicSwap(newSnapshot, newIndex)

	assert.Equal(t, uint64(2), newVersion)
	assert.Equal(t, uint64(2), vi.Version())

	// Verify new state is visible
	snapshot := vi.GetSnapshot()
	assert.True(t, snapshot.Lookup("A", "C"))

	intervalIndex := vi.GetIntervalIndex()
	assert.True(t, intervalIndex.IsReachable("A", "C"))
}

func TestVersionedIntervalIndex_AtomicSwap_MultipleSwaps(t *testing.T) {
	graph := map[string][]string{"A": {}}
	tcData := map[string]map[string]bool{}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Perform multiple swaps
	for i := uint64(2); i <= 10; i++ {
		newSnapshot := NewIndexSnapshot(i, tcData)
		newIndex := NewIntervalIndex(graph)

		newVersion := vi.AtomicSwap(newSnapshot, newIndex)
		assert.Equal(t, i, newVersion)
		assert.Equal(t, i, vi.Version())
	}
}

func TestVersionedIntervalIndex_VersionMonotonicity(t *testing.T) {
	graph := map[string][]string{"A": {}}
	tcData := map[string]map[string]bool{}

	vi := NewVersionedIntervalIndex(graph, tcData)

	var lastVersion uint64 = 0
	for i := 0; i < 100; i++ {
		currentVersion := vi.Version()
		assert.GreaterOrEqual(t, currentVersion, lastVersion, "Version must be monotonically increasing")
		lastVersion = currentVersion

		newSnapshot := NewIndexSnapshot(uint64(i+2), tcData)
		newIndex := NewIntervalIndex(graph)
		vi.AtomicSwap(newSnapshot, newIndex)
	}
}

func TestVersionedIntervalIndex_ConcurrentReaders_NoBlocking(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 50
	const readsPerReader = 1000

	var wg sync.WaitGroup
	wg.Add(numReaders)

	startTime := time.Now()

	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				// These should all be lock-free operations
				snapshot := vi.GetSnapshot()
				assert.NotNil(t, snapshot)

				intervalIndex := vi.GetIntervalIndex()
				assert.NotNil(t, intervalIndex)

				_ = vi.Version()

				// Perform actual lookups
				snapshot.Lookup("A", "B")
				intervalIndex.IsReachable("A", "C")
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	// Concurrent readers should complete quickly (lock-free)
	// This is a basic sanity check - should complete in under 5 seconds
	assert.Less(t, elapsed, 5*time.Second, "Concurrent readers took too long, possible blocking")
}

func TestVersionedIntervalIndex_ConcurrentReadersWithSwap(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 20
	const readsPerReader = 500
	const numSwaps = 50

	var wg sync.WaitGroup
	var readErrors atomic.Int32
	var swapsDone atomic.Int32

	// Start readers
	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				// Get state atomically
				snapshot, intervalIndex, version := vi.GetState()

				if snapshot == nil || intervalIndex == nil {
					readErrors.Add(1)
					continue
				}

				// Version should be at least 1
				if version < 1 {
					readErrors.Add(1)
				}

				// Perform reads - these should never fail
				_ = snapshot.Lookup("A", "B")
				_ = intervalIndex.IsReachable("A", "B")
			}
		}()
	}

	// Start writer (performs swaps)
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < numSwaps; i++ {
			newSnapshot := NewIndexSnapshot(uint64(i+2), tcData)
			newIndex := NewIntervalIndex(graph)
			vi.AtomicSwap(newSnapshot, newIndex)
			swapsDone.Add(1)

			// Small delay to interleave with readers
			time.Sleep(time.Microsecond * 10)
		}
	}()

	wg.Wait()

	assert.Equal(t, int32(0), readErrors.Load(), "No read errors should occur during concurrent reads and swaps")
	assert.Equal(t, int32(numSwaps), swapsDone.Load(), "All swaps should complete")
}

func TestVersionedIntervalIndex_SnapshotIsolation(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Get snapshot before swap
	snapshotBefore := vi.GetSnapshot()
	versionBefore := vi.Version()

	// Verify initial state
	assert.True(t, snapshotBefore.Lookup("A", "B"))
	assert.False(t, snapshotBefore.Lookup("A", "C"))

	// Perform swap with new data
	newGraph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}

	newTcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	newSnapshot := NewIndexSnapshot(2, newTcData)
	newIndex := NewIntervalIndex(newGraph)
	vi.AtomicSwap(newSnapshot, newIndex)

	// Get snapshot after swap
	snapshotAfter := vi.GetSnapshot()
	versionAfter := vi.Version()

	// Old snapshot should still show old state (isolation)
	assert.True(t, snapshotBefore.Lookup("A", "B"))
	assert.False(t, snapshotBefore.Lookup("A", "C"), "Old snapshot should not see new data")
	assert.Equal(t, versionBefore, snapshotBefore.Version())

	// New snapshot should show new state
	assert.True(t, snapshotAfter.Lookup("A", "B"))
	assert.True(t, snapshotAfter.Lookup("A", "C"), "New snapshot should see new data")
	assert.Greater(t, versionAfter, versionBefore)
}

func TestVersionedIntervalIndex_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C", "D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 100
	const numWriters = 10
	const duration = 2 * time.Second

	var wg sync.WaitGroup
	stop := make(chan struct{})

	var totalReads atomic.Uint64
	var totalWrites atomic.Uint64
	var readErrors atomic.Uint64

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					snapshot, intervalIndex, version := vi.GetState()

					if snapshot == nil || intervalIndex == nil || version == 0 {
						readErrors.Add(1)
						continue
					}

					// Perform various reads
					_ = snapshot.Lookup("A", "B")
					_ = snapshot.Lookup("A", "D")
					_ = snapshot.GetReachable("A")
					_ = intervalIndex.IsReachable("A", "B")
					_ = intervalIndex.GetDescendants("A")

					totalReads.Add(1)
				}
			}
		}()
	}

	// Start writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					newSnapshot := NewIndexSnapshot(vi.Version()+1, tcData)
					newIndex := NewIntervalIndex(graph)
					vi.AtomicSwap(newSnapshot, newIndex)
					totalWrites.Add(1)

					// Small delay between writes
					time.Sleep(time.Millisecond * time.Duration(writerID+1))
				}
			}
		}(i)
	}

	// Run for specified duration
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	t.Logf("Stress test completed: %d reads, %d writes, %d errors",
		totalReads.Load(), totalWrites.Load(), readErrors.Load())

	assert.Equal(t, uint64(0), readErrors.Load(), "No read errors should occur")
	assert.Greater(t, totalReads.Load(), uint64(0), "Should have performed reads")
	assert.Greater(t, totalWrites.Load(), uint64(0), "Should have performed writes")
}

func TestVersionedIntervalIndex_EmptyInitialization(t *testing.T) {
	graph := map[string][]string{}
	tcData := map[string]map[string]bool{}

	vi := NewVersionedIntervalIndex(graph, tcData)

	assert.NotNil(t, vi)
	assert.Equal(t, uint64(1), vi.Version())

	snapshot := vi.GetSnapshot()
	assert.NotNil(t, snapshot)
	assert.Equal(t, 0, snapshot.Size())

	intervalIndex := vi.GetIntervalIndex()
	assert.NotNil(t, intervalIndex)
	assert.Equal(t, 0, intervalIndex.Size())
}

func TestVersionedIntervalIndex_NilReturns(t *testing.T) {
	// Create an uninitialized VersionedIntervalIndex
	vi := &VersionedIntervalIndex{}

	// All getters should handle nil state gracefully
	assert.Nil(t, vi.GetSnapshot())
	assert.Nil(t, vi.GetIntervalIndex())
	assert.Equal(t, uint64(0), vi.Version())

	snapshot, intervalIndex, version := vi.GetState()
	assert.Nil(t, snapshot)
	assert.Nil(t, intervalIndex)
	assert.Equal(t, uint64(0), version)
}

func TestVersionedIntervalIndex_AtomicSwap_FromNil(t *testing.T) {
	vi := &VersionedIntervalIndex{}

	graph := map[string][]string{"A": {"B"}}
	tcData := map[string]map[string]bool{"A": {"B": true}}

	newSnapshot := NewIndexSnapshot(1, tcData)
	newIndex := NewIntervalIndex(graph)

	newVersion := vi.AtomicSwap(newSnapshot, newIndex)

	assert.Equal(t, uint64(1), newVersion)
	assert.NotNil(t, vi.GetSnapshot())
	assert.NotNil(t, vi.GetIntervalIndex())
}

func TestVersionedIntervalIndex_ConsistentState(t *testing.T) {
	// Test that GetState returns consistent snapshot and interval index
	// from the same version
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const iterations = 100
	const numGoroutines = 10

	var wg sync.WaitGroup
	errors := make(chan string, numGoroutines*iterations)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				// Get state atomically
				snapshot, intervalIndex, version := vi.GetState()

				if snapshot == nil || intervalIndex == nil {
					continue
				}

				// Check that snapshot and interval index are consistent
				// Both should see "A" as a valid node
				snapshotHasA := len(snapshot.GetReachable("A")) > 0
				_, intervalHasA := intervalIndex.GetLabel("A")

				if snapshotHasA != intervalHasA {
					errors <- "Inconsistent state detected"
				}

				// Version should match snapshot version
				if version != snapshot.Version() {
					errors <- "Version mismatch"
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	var errorCount int
	for err := range errors {
		t.Errorf("Consistency error: %s", err)
		errorCount++
	}

	assert.Equal(t, 0, errorCount, "No consistency errors should occur")
}

// =============================================================================
// VTC.5 - Lock-Free Read Operations Tests
// =============================================================================

func TestVersionedIntervalIndex_IsReachable(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Test reachable pairs (uses interval index)
	assert.True(t, vi.IsReachable("A", "B"))
	assert.True(t, vi.IsReachable("A", "C"))
	assert.True(t, vi.IsReachable("A", "D"))
	assert.True(t, vi.IsReachable("B", "D"))

	// Test non-reachable pairs
	assert.False(t, vi.IsReachable("B", "A"))
	assert.False(t, vi.IsReachable("D", "A"))
	assert.False(t, vi.IsReachable("X", "Y"))
}

func TestVersionedIntervalIndex_IsReachable_NilState(t *testing.T) {
	vi := &VersionedIntervalIndex{}

	// Should handle nil state gracefully
	assert.False(t, vi.IsReachable("A", "B"))
}

func TestVersionedIntervalIndex_GetAllReachable(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Get all reachable from A
	reachableFromA := vi.GetAllReachable("A")
	assert.Len(t, reachableFromA, 3)
	assert.Contains(t, reachableFromA, "B")
	assert.Contains(t, reachableFromA, "C")
	assert.Contains(t, reachableFromA, "D")

	// Get all reachable from B
	reachableFromB := vi.GetAllReachable("B")
	assert.Len(t, reachableFromB, 1)
	assert.Contains(t, reachableFromB, "D")

	// Get all reachable from non-existent node
	reachableFromX := vi.GetAllReachable("X")
	assert.Nil(t, reachableFromX)
}

func TestVersionedIntervalIndex_GetAllReachable_NilState(t *testing.T) {
	vi := &VersionedIntervalIndex{}

	// Should handle nil state gracefully
	result := vi.GetAllReachable("A")
	assert.Nil(t, result)
}

func TestVersionedIntervalIndex_LookupInSnapshot(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"D": true},
		"C": {"D": false}, // Explicitly false
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Test using snapshot lookup (TC data)
	assert.True(t, vi.LookupInSnapshot("A", "B"))
	assert.True(t, vi.LookupInSnapshot("A", "C"))
	assert.True(t, vi.LookupInSnapshot("A", "D"))
	assert.True(t, vi.LookupInSnapshot("B", "D"))

	// Test explicitly false
	assert.False(t, vi.LookupInSnapshot("C", "D"))

	// Test non-existent pairs
	assert.False(t, vi.LookupInSnapshot("X", "Y"))
	assert.False(t, vi.LookupInSnapshot("D", "A"))
}

func TestVersionedIntervalIndex_LookupInSnapshot_NilState(t *testing.T) {
	vi := &VersionedIntervalIndex{}

	// Should handle nil state gracefully
	assert.False(t, vi.LookupInSnapshot("A", "B"))
}

func TestVersionedIntervalIndex_LockFreeOperations_Concurrent(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 50
	const readsPerReader = 1000

	var wg sync.WaitGroup
	var errors atomic.Int32

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				// All these operations should be lock-free
				if !vi.IsReachable("A", "B") {
					errors.Add(1)
				}

				reachable := vi.GetAllReachable("A")
				if len(reachable) != 3 {
					errors.Add(1)
				}

				if !vi.LookupInSnapshot("B", "D") {
					errors.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int32(0), errors.Load(), "No errors should occur during concurrent lock-free reads")
}

// =============================================================================
// VTC.6 - Rebuild Operations Tests
// =============================================================================

func TestVersionedIntervalIndex_Rebuild(t *testing.T) {
	// Initial state
	initialGraph := map[string][]string{
		"A": {"B"},
		"B": {},
	}
	initialTcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(initialGraph, initialTcData)
	assert.Equal(t, uint64(1), vi.Version())

	// Rebuild with new data
	newGraph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}
	newTcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	err := vi.Rebuild(newGraph, newTcData)
	assert.NoError(t, err)

	// Version should be incremented
	assert.Equal(t, uint64(2), vi.Version())

	// New data should be visible
	assert.True(t, vi.LookupInSnapshot("A", "C"))
	assert.True(t, vi.LookupInSnapshot("B", "C"))
	assert.True(t, vi.IsReachable("A", "C"))
}

func TestVersionedIntervalIndex_Rebuild_FromNil(t *testing.T) {
	vi := &VersionedIntervalIndex{}

	graph := map[string][]string{
		"A": {"B"},
		"B": {},
	}
	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	err := vi.Rebuild(graph, tcData)
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), vi.Version())
	assert.True(t, vi.LookupInSnapshot("A", "B"))
}

func TestVersionedIntervalIndex_Rebuild_MultipleRebuilds(t *testing.T) {
	graph := map[string][]string{"A": {}}
	tcData := map[string]map[string]bool{}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Perform multiple rebuilds
	for i := uint64(2); i <= 10; i++ {
		err := vi.Rebuild(graph, tcData)
		assert.NoError(t, err)
		assert.Equal(t, i, vi.Version())
	}
}

func TestVersionedIntervalIndex_RebuildWithMerge(t *testing.T) {
	// Initial state
	initialGraph := map[string][]string{
		"A": {"B"},
		"B": {},
	}
	initialTcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(initialGraph, initialTcData)

	// Merge new edges
	newEdges := map[string][]string{
		"A": {"C"},
		"C": {"D"},
		"D": {},
	}

	err := vi.RebuildWithMerge(newEdges)
	assert.NoError(t, err)

	// Version should be incremented
	assert.Equal(t, uint64(2), vi.Version())

	// New reachability should be computed
	snapshot := vi.GetSnapshot()
	assert.NotNil(t, snapshot)

	// Check that new edges are reflected in TC
	reachableFromA := vi.GetAllReachable("A")
	assert.NotNil(t, reachableFromA)
}

func TestVersionedIntervalIndex_RebuildWithMerge_FromNil(t *testing.T) {
	vi := &VersionedIntervalIndex{}

	newEdges := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	err := vi.RebuildWithMerge(newEdges)
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), vi.Version())

	// TC should be computed
	assert.True(t, vi.LookupInSnapshot("A", "B"))
	assert.True(t, vi.LookupInSnapshot("A", "C"))
	assert.True(t, vi.LookupInSnapshot("B", "C"))
}

func TestVersionedIntervalIndex_Rebuild_ConcurrentWithReads(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}
	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 20
	const readsPerReader = 500
	const numRebuilds = 50

	var wg sync.WaitGroup
	var readErrors atomic.Int32
	var rebuildsDone atomic.Int32

	// Start readers
	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				// Use lock-free read operations
				_ = vi.IsReachable("A", "B")
				_ = vi.GetAllReachable("A")
				_ = vi.LookupInSnapshot("B", "C")

				// Also test GetState
				snapshot, intervalIndex, version := vi.GetState()
				if snapshot == nil || intervalIndex == nil || version == 0 {
					readErrors.Add(1)
				}
			}
		}()
	}

	// Start rebuilder
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < numRebuilds; i++ {
			err := vi.Rebuild(graph, tcData)
			if err != nil {
				readErrors.Add(1)
			}
			rebuildsDone.Add(1)
			time.Sleep(time.Microsecond * 10)
		}
	}()

	wg.Wait()

	assert.Equal(t, int32(0), readErrors.Load(), "No errors during concurrent reads and rebuilds")
	assert.Equal(t, int32(numRebuilds), rebuildsDone.Load(), "All rebuilds should complete")
}

func TestVersionedIntervalIndex_Rebuild_SnapshotIsolation(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {},
	}
	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Get snapshot before rebuild
	snapshotBefore := vi.GetSnapshot()
	assert.True(t, snapshotBefore.Lookup("A", "B"))
	assert.False(t, snapshotBefore.Lookup("A", "C"))

	// Rebuild with new data
	newGraph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}
	newTcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	err := vi.Rebuild(newGraph, newTcData)
	assert.NoError(t, err)

	// Get snapshot after rebuild
	snapshotAfter := vi.GetSnapshot()

	// Old snapshot should still show old data (isolation)
	assert.True(t, snapshotBefore.Lookup("A", "B"))
	assert.False(t, snapshotBefore.Lookup("A", "C"), "Old snapshot should not see new data")

	// New snapshot should show new data
	assert.True(t, snapshotAfter.Lookup("A", "B"))
	assert.True(t, snapshotAfter.Lookup("A", "C"), "New snapshot should see new data")
}

func TestComputeTransitiveClosure(t *testing.T) {
	// Simple chain: A -> B -> C
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	tc := computeTransitiveClosure(graph)

	// A should reach B and C
	assert.True(t, tc["A"]["B"])
	assert.True(t, tc["A"]["C"])

	// B should reach C
	assert.True(t, tc["B"]["C"])

	// C has no outgoing edges, so it's not in TC or has empty map
	if cReachable, exists := tc["C"]; exists {
		assert.Empty(t, cReachable)
	}
}

func TestComputeTransitiveClosure_Diamond(t *testing.T) {
	// Diamond DAG:
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {},
	}

	tc := computeTransitiveClosure(graph)

	// A should reach B, C, D
	assert.True(t, tc["A"]["B"])
	assert.True(t, tc["A"]["C"])
	assert.True(t, tc["A"]["D"])

	// B should reach D
	assert.True(t, tc["B"]["D"])

	// C should reach D
	assert.True(t, tc["C"]["D"])
}

func TestComputeTransitiveClosure_Cycle(t *testing.T) {
	// Cycle: A -> B -> C -> A
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}

	tc := computeTransitiveClosure(graph)

	// In a cycle, each node can reach all others
	assert.True(t, tc["A"]["B"])
	assert.True(t, tc["A"]["C"])
	assert.True(t, tc["B"]["C"])
	assert.True(t, tc["B"]["A"])
	assert.True(t, tc["C"]["A"])
	assert.True(t, tc["C"]["B"])
}

func TestComputeTransitiveClosure_Empty(t *testing.T) {
	graph := map[string][]string{}

	tc := computeTransitiveClosure(graph)
	assert.Empty(t, tc)
}

func TestComputeTransitiveClosure_SingleNode(t *testing.T) {
	graph := map[string][]string{
		"A": {},
	}

	tc := computeTransitiveClosure(graph)

	// Single node with no edges should have empty or non-existent TC entry
	if aReachable, exists := tc["A"]; exists {
		assert.Empty(t, aReachable)
	}
}

// =============================================================================
// VTC.7 - Concurrency Tests for VersionedIntervalIndex
// =============================================================================

// TestVersionedIntervalIndex_ConcurrentReads tests multiple goroutines reading
// simultaneously without data races or blocking.
func TestVersionedIntervalIndex_ConcurrentReads(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C", "D"},
		"C": {"D", "E"},
		"D": {"E"},
		"E": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true, "E": true},
		"B": {"C": true, "D": true, "E": true},
		"C": {"D": true, "E": true},
		"D": {"E": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 100
	const readsPerReader = 10000

	var wg sync.WaitGroup
	var errors atomic.Int64
	var totalReads atomic.Uint64

	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func(readerID int) {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				// Mix of different read operations
				switch j % 4 {
				case 0:
					if !vi.IsReachable("A", "E") {
						errors.Add(1)
					}
				case 1:
					reachable := vi.GetAllReachable("A")
					if len(reachable) != 4 {
						errors.Add(1)
					}
				case 2:
					if !vi.LookupInSnapshot("B", "D") {
						errors.Add(1)
					}
				case 3:
					snapshot, intervalIndex, version := vi.GetState()
					if snapshot == nil || intervalIndex == nil || version == 0 {
						errors.Add(1)
					}
				}
				totalReads.Add(1)
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(0), errors.Load(), "No read errors should occur during concurrent reads")
	assert.Equal(t, uint64(numReaders*readsPerReader), totalReads.Load(), "All reads should complete")
}

// TestVersionedIntervalIndex_ReadDuringRebuild tests that reads continue
// uninterrupted during atomic rebuild operations.
func TestVersionedIntervalIndex_ReadDuringRebuild(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 50
	const readsPerReader = 5000
	const numRebuilds = 100

	var wg sync.WaitGroup
	var readErrors atomic.Int64
	var rebuildCount atomic.Int64
	var successfulReads atomic.Uint64

	stopReaders := make(chan struct{})

	// Start readers
	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				select {
				case <-stopReaders:
					return
				default:
				}

				// Perform reads during rebuilds
				snapshot := vi.GetSnapshot()
				if snapshot == nil {
					readErrors.Add(1)
					continue
				}

				// Verify snapshot is valid and consistent
				if vi.Version() < 1 {
					readErrors.Add(1)
					continue
				}

				// Test reachability lookup
				_ = vi.IsReachable("A", "B")
				_ = vi.GetAllReachable("A")

				successfulReads.Add(1)
			}
		}()
	}

	// Perform rebuilds concurrently with reads
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < numRebuilds; i++ {
			// Create new graph with slight variations
			newGraph := map[string][]string{
				"A": {"B", "C"},
				"B": {"C"},
				"C": {"D"},
				"D": {},
			}

			newTcData := map[string]map[string]bool{
				"A": {"B": true, "C": true, "D": true},
				"B": {"C": true, "D": true},
				"C": {"D": true},
			}

			err := vi.Rebuild(newGraph, newTcData)
			if err != nil {
				readErrors.Add(1)
			}
			rebuildCount.Add(1)

			// Small delay to interleave with readers
			time.Sleep(time.Microsecond * 50)
		}
		close(stopReaders)
	}()

	wg.Wait()

	assert.Equal(t, int64(0), readErrors.Load(), "No errors should occur during reads while rebuilding")
	assert.Equal(t, int64(numRebuilds), rebuildCount.Load(), "All rebuilds should complete")
	assert.Greater(t, successfulReads.Load(), uint64(0), "Should have successful reads")
}

// TestVersionedIntervalIndex_StressTestConcurrency is a high-volume stress test
// for concurrent reads and rebuilds.
func TestVersionedIntervalIndex_StressTestConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Create a larger graph for stress testing
	graph := make(map[string][]string)
	tcData := make(map[string]map[string]bool)

	// Build a 100-node graph with various connections
	nodes := make([]string, 100)
	for i := 0; i < 100; i++ {
		nodes[i] = fmt.Sprintf("node_%03d", i)
		graph[nodes[i]] = []string{}
	}

	// Add edges and compute TC
	for i := 0; i < 99; i++ {
		graph[nodes[i]] = append(graph[nodes[i]], nodes[i+1])
		// Add some cross-edges for complexity
		if i+5 < 100 {
			graph[nodes[i]] = append(graph[nodes[i]], nodes[i+5])
		}
	}

	// Compute transitive closure
	tcData = computeTransitiveClosure(graph)

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 200
	const numWriters = 20
	const duration = 3 * time.Second

	var wg sync.WaitGroup
	stop := make(chan struct{})

	var totalReads atomic.Uint64
	var totalWrites atomic.Uint64
	var readErrors atomic.Uint64

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					// Perform various read operations
					snapshot, intervalIndex, version := vi.GetState()

					if snapshot == nil || intervalIndex == nil || version == 0 {
						readErrors.Add(1)
						continue
					}

					// Perform lookups
					_ = snapshot.Lookup("node_000", "node_050")
					_ = snapshot.Lookup("node_010", "node_090")
					_ = intervalIndex.IsReachable("node_000", "node_099")
					_ = intervalIndex.GetDescendants("node_000")

					// Use lock-free convenience methods
					_ = vi.IsReachable("node_000", "node_050")
					_ = vi.GetAllReachable("node_010")
					_ = vi.LookupInSnapshot("node_020", "node_030")

					totalReads.Add(1)
				}
			}
		}(i)
	}

	// Start writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					// Create slightly modified graph for rebuild
					newGraph := make(map[string][]string)
					for k, v := range graph {
						newGraph[k] = append([]string{}, v...)
					}

					newSnapshot := NewIndexSnapshot(vi.Version()+1, tcData)
					newIndex := NewIntervalIndex(newGraph)
					vi.AtomicSwap(newSnapshot, newIndex)
					totalWrites.Add(1)

					// Stagger writes
					time.Sleep(time.Millisecond * time.Duration(writerID+1))
				}
			}
		}(i)
	}

	// Run for specified duration
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	t.Logf("Stress test results: %d reads, %d writes, %d errors",
		totalReads.Load(), totalWrites.Load(), readErrors.Load())

	assert.Equal(t, uint64(0), readErrors.Load(), "No read errors should occur")
	assert.Greater(t, totalReads.Load(), uint64(100000), "Should achieve high read throughput")
	assert.Greater(t, totalWrites.Load(), uint64(10), "Should have performed writes")
}

// TestVersionedIntervalIndex_SnapshotIsolation_Comprehensive tests that readers
// see consistent snapshots even when writers update the index.
func TestVersionedIntervalIndex_SnapshotIsolation_Comprehensive(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numReaders = 30
	const holdDuration = time.Millisecond * 10

	var wg sync.WaitGroup
	var inconsistencies atomic.Int64

	// Start readers that hold onto snapshots
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				// Get snapshot and record its version
				snapshot, intervalIndex, version := vi.GetState()
				if snapshot == nil || intervalIndex == nil {
					continue
				}

				// Capture state
				initialVersion := version
				canReachAtoB := snapshot.Lookup("A", "B")
				canReachAtoC := snapshot.Lookup("A", "C")
				intervalReachAtoB := intervalIndex.IsReachable("A", "B")

				// Hold the snapshot for a while (simulating work)
				time.Sleep(holdDuration)

				// Verify snapshot is still consistent (immutable)
				if snapshot.Version() != initialVersion {
					inconsistencies.Add(1)
				}
				if snapshot.Lookup("A", "B") != canReachAtoB {
					inconsistencies.Add(1)
				}
				if snapshot.Lookup("A", "C") != canReachAtoC {
					inconsistencies.Add(1)
				}
				if intervalIndex.IsReachable("A", "B") != intervalReachAtoB {
					inconsistencies.Add(1)
				}
			}
		}(i)
	}

	// Perform updates while readers are holding snapshots
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < 100; i++ {
			newGraph := map[string][]string{
				"A": {"B", "C", "D"},
				"B": {"C", "D"},
				"C": {"D"},
				"D": {},
			}

			newTcData := map[string]map[string]bool{
				"A": {"B": true, "C": true, "D": true},
				"B": {"C": true, "D": true},
				"C": {"D": true},
			}

			_ = vi.Rebuild(newGraph, newTcData)
			time.Sleep(time.Millisecond * 2)
		}
	}()

	wg.Wait()

	assert.Equal(t, int64(0), inconsistencies.Load(),
		"Snapshot isolation violated - readers should see consistent data")
}

// TestVersionedIntervalIndex_NoDataRaces is specifically designed to be run with
// -race flag to detect data races.
func TestVersionedIntervalIndex_NoDataRaces(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	const numGoroutines = 50
	const iterations = 1000

	var wg sync.WaitGroup

	// Mix of readers and writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				if id%5 == 0 {
					// Writer goroutine
					newSnapshot := NewIndexSnapshot(vi.Version()+1, tcData)
					newIndex := NewIntervalIndex(graph)
					vi.AtomicSwap(newSnapshot, newIndex)
				} else {
					// Reader goroutine - access all possible fields
					_ = vi.Version()
					snapshot := vi.GetSnapshot()
					if snapshot != nil {
						_ = snapshot.Version()
						_ = snapshot.CreatedAt()
						_ = snapshot.Lookup("A", "B")
						_ = snapshot.GetReachable("A")
						_ = snapshot.GetSubjects()
						_ = snapshot.Size()
						_ = snapshot.SubjectCount()
					}

					intervalIndex := vi.GetIntervalIndex()
					if intervalIndex != nil {
						_ = intervalIndex.IsReachable("A", "D")
						_ = intervalIndex.GetDescendants("A")
						_ = intervalIndex.GetAncestors("D")
						_ = intervalIndex.Size()
						_ = intervalIndex.CreatedAt()
						_ = intervalIndex.Labels()
					}

					_, _, _ = vi.GetState()
					_ = vi.IsReachable("A", "B")
					_ = vi.GetAllReachable("A")
					_ = vi.LookupInSnapshot("B", "D")
				}
			}
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// VTC.7 - Benchmark Tests for Lock-Free Read Performance
// =============================================================================

// BenchmarkVersionedIntervalIndex_IsReachable benchmarks O(1) reachability checks.
func BenchmarkVersionedIntervalIndex_IsReachable(b *testing.B) {
	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C", "D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = vi.IsReachable("A", "D")
	}
}

// BenchmarkVersionedIntervalIndex_GetAllReachable benchmarks getting all reachable nodes.
func BenchmarkVersionedIntervalIndex_GetAllReachable(b *testing.B) {
	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C", "D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = vi.GetAllReachable("A")
	}
}

// BenchmarkVersionedIntervalIndex_LookupInSnapshot benchmarks snapshot TC lookups.
func BenchmarkVersionedIntervalIndex_LookupInSnapshot(b *testing.B) {
	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C", "D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = vi.LookupInSnapshot("A", "D")
	}
}

// BenchmarkVersionedIntervalIndex_GetSnapshot benchmarks atomic snapshot retrieval.
func BenchmarkVersionedIntervalIndex_GetSnapshot(b *testing.B) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = vi.GetSnapshot()
	}
}

// BenchmarkVersionedIntervalIndex_ConcurrentReads benchmarks parallel read performance.
func BenchmarkVersionedIntervalIndex_ConcurrentReads(b *testing.B) {
	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C", "D"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = vi.IsReachable("A", "D")
		}
	})
}

// BenchmarkVersionedIntervalIndex_ReadDuringWrite benchmarks reads with concurrent writes.
func BenchmarkVersionedIntervalIndex_ReadDuringWrite(b *testing.B) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"C": true},
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	// Start a background writer
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				newSnapshot := NewIndexSnapshot(vi.Version()+1, tcData)
				newIndex := NewIntervalIndex(graph)
				vi.AtomicSwap(newSnapshot, newIndex)
				time.Sleep(time.Microsecond * 10)
			}
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = vi.IsReachable("A", "C")
		}
	})

	b.StopTimer()
	close(stop)
}

// BenchmarkVersionedIntervalIndex_LargeGraph benchmarks with a larger graph.
func BenchmarkVersionedIntervalIndex_LargeGraph(b *testing.B) {
	// Create a larger graph
	graph := make(map[string][]string)
	tcData := make(map[string]map[string]bool)

	for i := 0; i < 1000; i++ {
		node := fmt.Sprintf("node_%04d", i)
		graph[node] = []string{}
		tcData[node] = make(map[string]bool)

		if i > 0 {
			parent := fmt.Sprintf("node_%04d", i-1)
			graph[parent] = append(graph[parent], node)
			// Set TC for parent
			tcData[parent][node] = true
		}
	}

	vi := NewVersionedIntervalIndex(graph, tcData)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = vi.IsReachable("node_0000", "node_0999")
	}
}
