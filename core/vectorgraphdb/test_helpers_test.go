package vectorgraphdb

import "sync"

// testSnapshot implements Snapshot for testing.
type testSnapshot struct {
	id      uint64
	results []SnapshotSearchResult
}

func (s *testSnapshot) Search(query []float32, k int, filter *SnapshotSearchFilter) []SnapshotSearchResult {
	return s.results
}

func (s *testSnapshot) ID() uint64 {
	return s.id
}

// testSnapshotManager implements SnapshotManager for testing.
type testSnapshotManager struct {
	snapshots     map[uint64]*testSnapshot
	nextID        uint64
	snapshotCount int
	onCreateFunc  func() Snapshot
	mu            sync.Mutex
}

func newTestSnapshotManager() *testSnapshotManager {
	return &testSnapshotManager{
		snapshots: make(map[uint64]*testSnapshot),
		nextID:    1,
	}
}

func (m *testSnapshotManager) CreateSnapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.onCreateFunc != nil {
		return m.onCreateFunc()
	}
	snap := &testSnapshot{id: m.nextID}
	m.snapshots[m.nextID] = snap
	m.nextID++
	m.snapshotCount++
	return snap
}

func (m *testSnapshotManager) ReleaseSnapshot(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.snapshots[id]; exists {
		delete(m.snapshots, id)
		m.snapshotCount--
	}
}

func (m *testSnapshotManager) GCLoop(ctx interface{}) {
	// no-op for testing
}

func (m *testSnapshotManager) SnapshotCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotCount
}

// testVectorIndex implements VectorIndex for testing.
type testVectorIndex struct {
	vectors   map[string][]float32
	domains   map[string]Domain
	nodeTypes map[string]NodeType
	inserted  map[string]bool
	deleted   map[string]bool
	mu        sync.RWMutex
}

func newTestVectorIndex() *testVectorIndex {
	return &testVectorIndex{
		vectors:   make(map[string][]float32),
		domains:   make(map[string]Domain),
		nodeTypes: make(map[string]NodeType),
		inserted:  make(map[string]bool),
		deleted:   make(map[string]bool),
	}
}

func (m *testVectorIndex) Search(query []float32, k int, filter *SnapshotSearchFilter) []SnapshotSearchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []SnapshotSearchResult
	for id := range m.vectors {
		results = append(results, SnapshotSearchResult{
			ID:         id,
			Similarity: 0.9,
			Domain:     m.domains[id],
			NodeType:   m.nodeTypes[id],
		})
		if len(results) >= k {
			break
		}
	}
	return results
}

func (m *testVectorIndex) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vectors[id] = vector
	m.domains[id] = domain
	m.nodeTypes[id] = nodeType
	m.inserted[id] = true
	return nil
}

func (m *testVectorIndex) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vectors, id)
	delete(m.domains, id)
	delete(m.nodeTypes, id)
	m.deleted[id] = true
	return nil
}

func (m *testVectorIndex) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.vectors)
}
