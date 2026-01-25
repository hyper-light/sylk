package graph

import (
	"sync/atomic"
)

// atomicEdgeList provides lock-free reads with copy-on-write appends.
// Reads: atomic pointer load (always lock-free).
// Writes: CAS retry loop, allocates new slice on success.
// Returned slices are immutable - callers must not modify them.
type atomicEdgeList struct {
	list atomic.Pointer[[]Edge]
}

func newAtomicEdgeList() *atomicEdgeList {
	return &atomicEdgeList{}
}

func (a *atomicEdgeList) Append(e Edge) {
	for {
		old := a.list.Load()
		var newList []Edge
		if old != nil {
			newList = make([]Edge, len(*old)+1)
			copy(newList, *old)
			newList[len(*old)] = e
		} else {
			newList = []Edge{e}
		}
		if a.list.CompareAndSwap(old, &newList) {
			return
		}
	}
}

func (a *atomicEdgeList) AppendBatch(edges []Edge) {
	if len(edges) == 0 {
		return
	}

	for {
		old := a.list.Load()
		var newList []Edge
		if old != nil {
			newList = make([]Edge, len(*old)+len(edges))
			copy(newList, *old)
			copy(newList[len(*old):], edges)
		} else {
			newList = make([]Edge, len(edges))
			copy(newList, edges)
		}
		if a.list.CompareAndSwap(old, &newList) {
			return
		}
	}
}

// Read returns an immutable snapshot. Do not modify the returned slice.
func (a *atomicEdgeList) Read() []Edge {
	ptr := a.list.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

func (a *atomicEdgeList) Len() int {
	ptr := a.list.Load()
	if ptr == nil {
		return 0
	}
	return len(*ptr)
}

func (a *atomicEdgeList) ReadCopy() []Edge {
	ptr := a.list.Load()
	if ptr == nil {
		return nil
	}
	result := make([]Edge, len(*ptr))
	copy(result, *ptr)
	return result
}
