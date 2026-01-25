package graph

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func TestAtomicEdgeList_AppendAndRead(t *testing.T) {
	list := newAtomicEdgeList()

	if list.Len() != 0 {
		t.Errorf("expected empty list, got len=%d", list.Len())
	}

	e1 := Edge{SourceID: 1, TargetID: 2, Type: vectorgraphdb.EdgeTypeCalls}
	list.Append(e1)

	if list.Len() != 1 {
		t.Errorf("expected len=1, got %d", list.Len())
	}

	edges := list.Read()
	if len(edges) != 1 || edges[0].SourceID != 1 || edges[0].TargetID != 2 {
		t.Errorf("unexpected edge: %+v", edges)
	}
}

func TestAtomicEdgeList_AppendBatch(t *testing.T) {
	list := newAtomicEdgeList()

	batch := []Edge{
		{SourceID: 1, TargetID: 2, Type: vectorgraphdb.EdgeTypeCalls},
		{SourceID: 3, TargetID: 4, Type: vectorgraphdb.EdgeTypeImports},
		{SourceID: 5, TargetID: 6, Type: vectorgraphdb.EdgeTypeDefines},
	}
	list.AppendBatch(batch)

	if list.Len() != 3 {
		t.Errorf("expected len=3, got %d", list.Len())
	}

	edges := list.Read()
	for i, e := range batch {
		if edges[i].SourceID != e.SourceID || edges[i].TargetID != e.TargetID {
			t.Errorf("edge %d mismatch: got %+v, want %+v", i, edges[i], e)
		}
	}
}

func TestAtomicEdgeList_AppendBatchEmpty(t *testing.T) {
	list := newAtomicEdgeList()
	list.AppendBatch(nil)
	list.AppendBatch([]Edge{})

	if list.Len() != 0 {
		t.Errorf("expected len=0, got %d", list.Len())
	}
}

func TestAtomicEdgeList_ReadCopy(t *testing.T) {
	list := newAtomicEdgeList()
	list.Append(Edge{SourceID: 1, TargetID: 2})

	copy1 := list.ReadCopy()
	copy1[0].SourceID = 999

	edges := list.Read()
	if edges[0].SourceID == 999 {
		t.Error("ReadCopy should return independent copy")
	}
}

func TestAtomicEdgeList_ConcurrentAppend(t *testing.T) {
	list := newAtomicEdgeList()
	const numGoroutines = 100
	const edgesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < edgesPerGoroutine; i++ {
				list.Append(Edge{
					SourceID: uint32(gid*edgesPerGoroutine + i),
					TargetID: uint32(i),
				})
			}
		}(g)
	}

	wg.Wait()

	expected := numGoroutines * edgesPerGoroutine
	if list.Len() != expected {
		t.Errorf("expected len=%d, got %d", expected, list.Len())
	}
}

func TestAtomicEdgeList_ConcurrentAppendBatch(t *testing.T) {
	list := newAtomicEdgeList()
	const numGoroutines = 50
	const batchSize = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			batch := make([]Edge, batchSize)
			for i := range batch {
				batch[i] = Edge{
					SourceID: uint32(gid*batchSize + i),
					TargetID: uint32(i),
				}
			}
			list.AppendBatch(batch)
		}(g)
	}

	wg.Wait()

	expected := numGoroutines * batchSize
	if list.Len() != expected {
		t.Errorf("expected len=%d, got %d", expected, list.Len())
	}
}

func TestAtomicEdgeList_ConcurrentReadWrite(t *testing.T) {
	list := newAtomicEdgeList()
	const numWriters = 10
	const numReaders = 20
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(numWriters + numReaders)

	for w := 0; w < numWriters; w++ {
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				list.Append(Edge{SourceID: uint32(wid), TargetID: uint32(i)})
			}
		}(w)
	}

	for r := 0; r < numReaders; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				edges := list.Read()
				_ = len(edges)
			}
		}()
	}

	wg.Wait()

	expected := numWriters * iterations
	if list.Len() != expected {
		t.Errorf("expected len=%d, got %d", expected, list.Len())
	}
}

func BenchmarkAtomicEdgeList_Append(b *testing.B) {
	list := newAtomicEdgeList()
	e := Edge{SourceID: 1, TargetID: 2, Type: vectorgraphdb.EdgeTypeCalls}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list.Append(e)
	}
}

func BenchmarkAtomicEdgeList_AppendParallel(b *testing.B) {
	list := newAtomicEdgeList()
	e := Edge{SourceID: 1, TargetID: 2, Type: vectorgraphdb.EdgeTypeCalls}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			list.Append(e)
		}
	})
}

func BenchmarkAtomicEdgeList_Read(b *testing.B) {
	list := newAtomicEdgeList()
	for i := 0; i < 1000; i++ {
		list.Append(Edge{SourceID: uint32(i), TargetID: uint32(i + 1)})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = list.Read()
	}
}

func BenchmarkAtomicEdgeList_ReadParallel(b *testing.B) {
	list := newAtomicEdgeList()
	for i := 0; i < 1000; i++ {
		list.Append(Edge{SourceID: uint32(i), TargetID: uint32(i + 1)})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = list.Read()
		}
	})
}
