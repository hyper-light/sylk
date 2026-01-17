package versioning

import (
	"errors"
	"sync"
)

var (
	ErrOperationNotFound = errors.New("operation not found")
)

type OperationLog interface {
	Append(op Operation) error
	Get(id OperationID) (*Operation, error)
	GetByVersion(versionID VersionID) ([]*Operation, error)
	GetByFile(filePath string, limit int) ([]*Operation, error)
	GetSince(versionID VersionID, filePath string) ([]*Operation, error)
	Count() int
}

type MemoryOperationLog struct {
	mu         sync.RWMutex
	operations []Operation
	byID       map[OperationID]int
	byVersion  map[VersionID][]int
	byFile     map[string][]int
}

func NewMemoryOperationLog() *MemoryOperationLog {
	return &MemoryOperationLog{
		operations: make([]Operation, 0),
		byID:       make(map[OperationID]int),
		byVersion:  make(map[VersionID][]int),
		byFile:     make(map[string][]int),
	}
}

func (l *MemoryOperationLog) Append(op Operation) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	idx := len(l.operations)
	l.operations = append(l.operations, op.Clone())
	l.indexOperation(op, idx)
	return nil
}

func (l *MemoryOperationLog) indexOperation(op Operation, idx int) {
	l.byID[op.ID] = idx
	l.byVersion[op.BaseVersion] = append(l.byVersion[op.BaseVersion], idx)
	l.byFile[op.FilePath] = append(l.byFile[op.FilePath], idx)
}

func (l *MemoryOperationLog) Get(id OperationID) (*Operation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	idx, ok := l.byID[id]
	if !ok {
		return nil, ErrOperationNotFound
	}

	op := l.operations[idx].Clone()
	return &op, nil
}

func (l *MemoryOperationLog) GetByVersion(versionID VersionID) ([]*Operation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	indices := l.byVersion[versionID]
	return l.collectOperations(indices), nil
}

func (l *MemoryOperationLog) collectOperations(indices []int) []*Operation {
	ops := make([]*Operation, 0, len(indices))
	for _, idx := range indices {
		op := l.operations[idx].Clone()
		ops = append(ops, &op)
	}
	return ops
}

func (l *MemoryOperationLog) GetByFile(filePath string, limit int) ([]*Operation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	indices := l.byFile[filePath]
	return l.collectOperationsWithLimit(indices, limit), nil
}

func (l *MemoryOperationLog) collectOperationsWithLimit(indices []int, limit int) []*Operation {
	count := effectiveOpLimit(len(indices), limit)
	ops := make([]*Operation, 0, count)

	for i := len(indices) - 1; i >= 0 && len(ops) < count; i-- {
		op := l.operations[indices[i]].Clone()
		ops = append(ops, &op)
	}
	return ops
}

func effectiveOpLimit(total, limit int) int {
	if limit > 0 && limit < total {
		return limit
	}
	return total
}

func (l *MemoryOperationLog) GetSince(versionID VersionID, filePath string) ([]*Operation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	startIdx := l.findVersionIndex(versionID, filePath)
	return l.collectOpsAfterIndex(filePath, startIdx), nil
}

func (l *MemoryOperationLog) findVersionIndex(versionID VersionID, filePath string) int {
	indices := l.byFile[filePath]
	for _, idx := range indices {
		if l.operations[idx].BaseVersion == versionID {
			return idx
		}
	}
	return -1
}

func (l *MemoryOperationLog) collectOpsAfterIndex(filePath string, startIdx int) []*Operation {
	indices := l.byFile[filePath]
	ops := make([]*Operation, 0)
	for _, idx := range indices {
		if idx > startIdx {
			op := l.operations[idx].Clone()
			ops = append(ops, &op)
		}
	}
	return ops
}

func (l *MemoryOperationLog) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.operations)
}

func (l *MemoryOperationLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.operations = make([]Operation, 0)
	l.byID = make(map[OperationID]int)
	l.byVersion = make(map[VersionID][]int)
	l.byFile = make(map[string][]int)
}
