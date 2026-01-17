package versioning

import (
	"errors"
	"sync"
)

var (
	ErrVersionNotFound   = errors.New("version not found")
	ErrVersionExists     = errors.New("version already exists")
	ErrInvalidParent     = errors.New("invalid parent version")
	ErrCycleDetected     = errors.New("cycle detected in version DAG")
	ErrNoVersionsForFile = errors.New("no versions for file")
)

type DAGStore interface {
	Add(version FileVersion) error
	Get(id VersionID) (*FileVersion, error)
	GetHead(filePath string) (*FileVersion, error)
	GetHistory(filePath string, limit int) ([]*FileVersion, error)
	GetChildren(id VersionID) ([]*FileVersion, error)
	GetAncestors(id VersionID, depth int) ([]*FileVersion, error)
	Has(id VersionID) bool
	Count() int
}

type MemoryDAGStore struct {
	mu        sync.RWMutex
	versions  map[VersionID]*FileVersion
	heads     map[string]VersionID
	children  map[VersionID][]VersionID
	fileIndex map[string][]VersionID
}

func NewMemoryDAGStore() *MemoryDAGStore {
	return &MemoryDAGStore{
		versions:  make(map[VersionID]*FileVersion),
		heads:     make(map[string]VersionID),
		children:  make(map[VersionID][]VersionID),
		fileIndex: make(map[string][]VersionID),
	}
}

func (s *MemoryDAGStore) Add(version FileVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.versions[version.ID]; exists {
		return ErrVersionExists
	}

	if err := s.validateParents(version); err != nil {
		return err
	}

	s.storeVersion(version)
	s.updateIndices(version)
	return nil
}

func (s *MemoryDAGStore) validateParents(version FileVersion) error {
	for _, parentID := range version.Parents {
		if _, exists := s.versions[parentID]; !exists {
			return ErrInvalidParent
		}
	}
	return nil
}

func (s *MemoryDAGStore) storeVersion(version FileVersion) {
	clone := version.Clone()
	s.versions[version.ID] = &clone
}

func (s *MemoryDAGStore) updateIndices(version FileVersion) {
	s.heads[version.FilePath] = version.ID
	s.fileIndex[version.FilePath] = append(s.fileIndex[version.FilePath], version.ID)
	s.updateChildrenIndex(version)
}

func (s *MemoryDAGStore) updateChildrenIndex(version FileVersion) {
	for _, parentID := range version.Parents {
		s.children[parentID] = append(s.children[parentID], version.ID)
	}
}

func (s *MemoryDAGStore) Get(id VersionID) (*FileVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	version, ok := s.versions[id]
	if !ok {
		return nil, ErrVersionNotFound
	}

	clone := version.Clone()
	return &clone, nil
}

func (s *MemoryDAGStore) GetHead(filePath string) (*FileVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	headID, ok := s.heads[filePath]
	if !ok {
		return nil, ErrNoVersionsForFile
	}

	version := s.versions[headID]
	clone := version.Clone()
	return &clone, nil
}

func (s *MemoryDAGStore) GetHistory(filePath string, limit int) ([]*FileVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.fileIndex[filePath]
	if len(ids) == 0 {
		return nil, nil
	}

	return s.collectVersionsReverse(ids, limit), nil
}

func (s *MemoryDAGStore) collectVersionsReverse(ids []VersionID, limit int) []*FileVersion {
	count := len(ids)
	if limit > 0 && limit < count {
		count = limit
	}

	versions := make([]*FileVersion, 0, count)
	for i := len(ids) - 1; i >= 0 && len(versions) < count; i-- {
		clone := s.versions[ids[i]].Clone()
		versions = append(versions, &clone)
	}
	return versions
}

func (s *MemoryDAGStore) GetChildren(id VersionID) ([]*FileVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	childIDs := s.children[id]
	return s.collectVersions(childIDs), nil
}

func (s *MemoryDAGStore) collectVersions(ids []VersionID) []*FileVersion {
	versions := make([]*FileVersion, 0, len(ids))
	for _, id := range ids {
		if version, ok := s.versions[id]; ok {
			clone := version.Clone()
			versions = append(versions, &clone)
		}
	}
	return versions
}

func (s *MemoryDAGStore) GetAncestors(id VersionID, depth int) ([]*FileVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	version, ok := s.versions[id]
	if !ok {
		return nil, ErrVersionNotFound
	}

	return s.traverseAncestors(version, depth), nil
}

func (s *MemoryDAGStore) traverseAncestors(start *FileVersion, depth int) []*FileVersion {
	if depth == 0 {
		return nil
	}

	ancestors := make([]*FileVersion, 0)
	visited := make(map[VersionID]bool)
	queue := start.Parents

	for len(queue) > 0 && (depth < 0 || len(ancestors) < depth) {
		parentID := queue[0]
		queue = queue[1:]

		if visited[parentID] {
			continue
		}
		visited[parentID] = true

		parent, ok := s.versions[parentID]
		if !ok {
			continue
		}

		clone := parent.Clone()
		ancestors = append(ancestors, &clone)
		queue = append(queue, parent.Parents...)
	}

	return ancestors
}

func (s *MemoryDAGStore) Has(id VersionID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.versions[id]
	return ok
}

func (s *MemoryDAGStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.versions)
}

func (s *MemoryDAGStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions = make(map[VersionID]*FileVersion)
	s.heads = make(map[string]VersionID)
	s.children = make(map[VersionID][]VersionID)
	s.fileIndex = make(map[string][]VersionID)
}

func (s *MemoryDAGStore) Files() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	files := make([]string, 0, len(s.heads))
	for file := range s.heads {
		files = append(files, file)
	}
	return files
}
