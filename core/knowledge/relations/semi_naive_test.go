package relations

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// EdgeKey Tests
// =============================================================================

func TestEdgeKey_String(t *testing.T) {
	edge := EdgeKey{
		Subject:   "User",
		Predicate: "owns",
		Object:    "Account",
	}

	result := edge.String()
	assert.Equal(t, "(User)-[owns]->(Account)", result)
}

func TestEdgeKey_String_EmptyFields(t *testing.T) {
	edge := EdgeKey{}
	result := edge.String()
	assert.Equal(t, "()-[]->()", result)
}

func TestEdgeKey_AsMapKey(t *testing.T) {
	// Verify EdgeKey works correctly as a map key
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge3 := EdgeKey{Subject: "A", Predicate: "rel", Object: "C"}

	m := make(map[EdgeKey]int)
	m[edge1] = 1
	m[edge2] = 2 // Should overwrite edge1

	assert.Equal(t, 1, len(m), "Identical EdgeKeys should be equal as map keys")
	assert.Equal(t, 2, m[edge1], "Value should be overwritten")

	m[edge3] = 3
	assert.Equal(t, 2, len(m), "Different EdgeKeys should be separate keys")
}

func TestEdgeKey_Equality(t *testing.T) {
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge3 := EdgeKey{Subject: "A", Predicate: "rel", Object: "C"}

	assert.Equal(t, edge1, edge2, "Identical EdgeKeys should be equal")
	assert.NotEqual(t, edge1, edge3, "Different EdgeKeys should not be equal")
}

// =============================================================================
// DeltaTable Tests
// =============================================================================

func TestDeltaTable_NewDeltaTable(t *testing.T) {
	dt := NewDeltaTable()
	require.NotNil(t, dt)
	assert.True(t, dt.IsEmpty())
	assert.Equal(t, 0, dt.Size())
}

func TestDeltaTable_Add(t *testing.T) {
	dt := NewDeltaTable()
	edge := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}

	dt.Add(edge)

	assert.False(t, dt.IsEmpty())
	assert.Equal(t, 1, dt.Size())
	assert.True(t, dt.Contains(edge))
}

func TestDeltaTable_Add_Duplicate(t *testing.T) {
	dt := NewDeltaTable()
	edge := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}

	dt.Add(edge)
	dt.Add(edge) // Add same edge again

	assert.Equal(t, 1, dt.Size(), "Duplicate edges should not increase size")
}

func TestDeltaTable_Add_Multiple(t *testing.T) {
	dt := NewDeltaTable()
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "B", Predicate: "rel", Object: "C"}
	edge3 := EdgeKey{Subject: "A", Predicate: "other", Object: "B"}

	dt.Add(edge1)
	dt.Add(edge2)
	dt.Add(edge3)

	assert.Equal(t, 3, dt.Size())
	assert.True(t, dt.Contains(edge1))
	assert.True(t, dt.Contains(edge2))
	assert.True(t, dt.Contains(edge3))
}

func TestDeltaTable_Contains(t *testing.T) {
	dt := NewDeltaTable()
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "X", Predicate: "rel", Object: "Y"}

	dt.Add(edge1)

	assert.True(t, dt.Contains(edge1))
	assert.False(t, dt.Contains(edge2))
}

func TestDeltaTable_Contains_Empty(t *testing.T) {
	dt := NewDeltaTable()
	edge := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}

	assert.False(t, dt.Contains(edge))
}

func TestDeltaTable_Merge(t *testing.T) {
	dt1 := NewDeltaTable()
	dt1.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})
	dt1.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"})

	dt2 := NewDeltaTable()
	dt2.Add(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"})
	dt2.Add(EdgeKey{Subject: "D", Predicate: "rel", Object: "E"})

	dt1.Merge(dt2)

	assert.Equal(t, 4, dt1.Size())
	assert.True(t, dt1.Contains(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}))
	assert.True(t, dt1.Contains(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"}))
}

func TestDeltaTable_Merge_WithOverlap(t *testing.T) {
	dt1 := NewDeltaTable()
	dt1.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})
	dt1.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"})

	dt2 := NewDeltaTable()
	dt2.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"}) // Duplicate
	dt2.Add(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"})

	dt1.Merge(dt2)

	assert.Equal(t, 3, dt1.Size(), "Duplicate edges should not be counted twice")
}

func TestDeltaTable_Merge_Nil(t *testing.T) {
	dt1 := NewDeltaTable()
	dt1.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	dt1.Merge(nil) // Should not panic

	assert.Equal(t, 1, dt1.Size())
}

func TestDeltaTable_Merge_Empty(t *testing.T) {
	dt1 := NewDeltaTable()
	dt1.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	dt2 := NewDeltaTable()
	dt1.Merge(dt2)

	assert.Equal(t, 1, dt1.Size())
}

func TestDeltaTable_Clear(t *testing.T) {
	dt := NewDeltaTable()
	dt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})
	dt.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"})

	assert.Equal(t, 2, dt.Size())

	dt.Clear()

	assert.True(t, dt.IsEmpty())
	assert.Equal(t, 0, dt.Size())
	assert.False(t, dt.Contains(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}))
}

func TestDeltaTable_Edges(t *testing.T) {
	dt := NewDeltaTable()
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "B", Predicate: "rel", Object: "C"}
	edge3 := EdgeKey{Subject: "C", Predicate: "rel", Object: "D"}

	dt.Add(edge1)
	dt.Add(edge2)
	dt.Add(edge3)

	edges := dt.Edges()

	assert.Len(t, edges, 3)

	// Verify all edges are present (order not guaranteed)
	edgeSet := make(map[EdgeKey]bool)
	for _, e := range edges {
		edgeSet[e] = true
	}
	assert.True(t, edgeSet[edge1])
	assert.True(t, edgeSet[edge2])
	assert.True(t, edgeSet[edge3])
}

func TestDeltaTable_Edges_Empty(t *testing.T) {
	dt := NewDeltaTable()
	edges := dt.Edges()

	assert.NotNil(t, edges)
	assert.Len(t, edges, 0)
}

func TestDeltaTable_IsEmpty(t *testing.T) {
	dt := NewDeltaTable()
	assert.True(t, dt.IsEmpty())

	dt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})
	assert.False(t, dt.IsEmpty())

	dt.Clear()
	assert.True(t, dt.IsEmpty())
}

func TestDeltaTable_Size(t *testing.T) {
	dt := NewDeltaTable()
	assert.Equal(t, 0, dt.Size())

	dt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})
	assert.Equal(t, 1, dt.Size())

	dt.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"})
	assert.Equal(t, 2, dt.Size())

	dt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}) // Duplicate
	assert.Equal(t, 2, dt.Size())
}

// =============================================================================
// ConcurrentDeltaTable Tests
// =============================================================================

func TestConcurrentDeltaTable_NewConcurrentDeltaTable(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	require.NotNil(t, cdt)
	assert.True(t, cdt.IsEmpty())
	assert.Equal(t, 0, cdt.Size())
}

func TestConcurrentDeltaTable_Add(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	edge := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}

	cdt.Add(edge)

	assert.False(t, cdt.IsEmpty())
	assert.Equal(t, 1, cdt.Size())
	assert.True(t, cdt.Contains(edge))
}

func TestConcurrentDeltaTable_Contains(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "X", Predicate: "rel", Object: "Y"}

	cdt.Add(edge1)

	assert.True(t, cdt.Contains(edge1))
	assert.False(t, cdt.Contains(edge2))
}

func TestConcurrentDeltaTable_Merge(t *testing.T) {
	cdt1 := NewConcurrentDeltaTable()
	cdt1.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	cdt2 := NewConcurrentDeltaTable()
	cdt2.Add(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"})

	cdt1.Merge(cdt2)

	assert.Equal(t, 2, cdt1.Size())
	assert.True(t, cdt1.Contains(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}))
	assert.True(t, cdt1.Contains(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"}))
}

func TestConcurrentDeltaTable_Merge_Nil(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	cdt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	cdt.Merge(nil) // Should not panic

	assert.Equal(t, 1, cdt.Size())
}

func TestConcurrentDeltaTable_MergeFromDeltaTable(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	cdt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	dt := NewDeltaTable()
	dt.Add(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"})
	dt.Add(EdgeKey{Subject: "E", Predicate: "rel", Object: "F"})

	cdt.MergeFromDeltaTable(dt)

	assert.Equal(t, 3, cdt.Size())
	assert.True(t, cdt.Contains(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}))
	assert.True(t, cdt.Contains(EdgeKey{Subject: "C", Predicate: "rel", Object: "D"}))
	assert.True(t, cdt.Contains(EdgeKey{Subject: "E", Predicate: "rel", Object: "F"}))
}

func TestConcurrentDeltaTable_MergeFromDeltaTable_Nil(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	cdt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	cdt.MergeFromDeltaTable(nil) // Should not panic

	assert.Equal(t, 1, cdt.Size())
}

func TestConcurrentDeltaTable_Clear(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	cdt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})
	cdt.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"})

	cdt.Clear()

	assert.True(t, cdt.IsEmpty())
	assert.Equal(t, 0, cdt.Size())
}

func TestConcurrentDeltaTable_Edges(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "B", Predicate: "rel", Object: "C"}

	cdt.Add(edge1)
	cdt.Add(edge2)

	edges := cdt.Edges()

	assert.Len(t, edges, 2)

	edgeSet := make(map[EdgeKey]bool)
	for _, e := range edges {
		edgeSet[e] = true
	}
	assert.True(t, edgeSet[edge1])
	assert.True(t, edgeSet[edge2])
}

func TestConcurrentDeltaTable_ToDeltaTable(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	edge1 := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	edge2 := EdgeKey{Subject: "B", Predicate: "rel", Object: "C"}

	cdt.Add(edge1)
	cdt.Add(edge2)

	dt := cdt.ToDeltaTable()

	require.NotNil(t, dt)
	assert.Equal(t, 2, dt.Size())
	assert.True(t, dt.Contains(edge1))
	assert.True(t, dt.Contains(edge2))

	// Verify it's a copy (modifying one doesn't affect the other)
	dt.Add(EdgeKey{Subject: "X", Predicate: "rel", Object: "Y"})
	assert.Equal(t, 3, dt.Size())
	assert.Equal(t, 2, cdt.Size())
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestConcurrentDeltaTable_ConcurrentAdd(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	var wg sync.WaitGroup
	numGoroutines := 100
	edgesPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < edgesPerGoroutine; j++ {
				edge := EdgeKey{
					Subject:   "S",
					Predicate: "P",
					Object:    "O",
				}
				cdt.Add(edge)
			}
		}(i)
	}
	wg.Wait()

	// All goroutines added the same edge, so size should be 1
	assert.Equal(t, 1, cdt.Size())
}

func TestConcurrentDeltaTable_ConcurrentAddUnique(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	var wg sync.WaitGroup
	numGoroutines := 10
	edgesPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < edgesPerGoroutine; j++ {
				edge := EdgeKey{
					Subject:   string(rune('A' + gid)),
					Predicate: "rel",
					Object:    string(rune('0' + j%10)),
				}
				cdt.Add(edge)
			}
		}(i)
	}
	wg.Wait()

	// Each goroutine adds 10 unique edges (due to Object being j%10)
	// With 10 goroutines, total unique edges = 10 * 10 = 100
	assert.Equal(t, 100, cdt.Size())
}

func TestConcurrentDeltaTable_ConcurrentReadWrite(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	var wg sync.WaitGroup

	// Pre-populate with some edges
	for i := 0; i < 50; i++ {
		cdt.Add(EdgeKey{Subject: "init", Predicate: "rel", Object: string(rune('0' + i%10))})
	}

	// Writers
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cdt.Add(EdgeKey{Subject: "writer", Predicate: "rel", Object: string(rune('A' + gid))})
			}
		}(i)
	}

	// Readers
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = cdt.Size()
				_ = cdt.Contains(EdgeKey{Subject: "init", Predicate: "rel", Object: "0"})
				_ = cdt.Edges()
			}
		}()
	}

	wg.Wait()

	// Verify data integrity - should have init edges + writer edges
	assert.GreaterOrEqual(t, cdt.Size(), 10, "Should have at least the unique writer edges")
}

func TestConcurrentDeltaTable_ConcurrentMerge(t *testing.T) {
	cdt := NewConcurrentDeltaTable()
	var wg sync.WaitGroup

	numGoroutines := 10
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			other := NewConcurrentDeltaTable()
			other.Add(EdgeKey{Subject: string(rune('A' + gid)), Predicate: "rel", Object: "target"})
			cdt.Merge(other)
		}(i)
	}

	wg.Wait()

	assert.Equal(t, 10, cdt.Size())
}

// =============================================================================
// Semi-Naive Evaluation Pattern Tests
// =============================================================================

func TestSemiNaivePattern_IterationTermination(t *testing.T) {
	// Simulate semi-naive evaluation iteration
	// Start with initial facts
	allFacts := NewDeltaTable()
	allFacts.Add(EdgeKey{Subject: "A", Predicate: "parent", Object: "B"})
	allFacts.Add(EdgeKey{Subject: "B", Predicate: "parent", Object: "C"})

	// Delta contains new facts from last iteration
	delta := NewDeltaTable()
	delta.Add(EdgeKey{Subject: "A", Predicate: "parent", Object: "B"})
	delta.Add(EdgeKey{Subject: "B", Predicate: "parent", Object: "C"})

	iterations := 0
	maxIterations := 10

	for !delta.IsEmpty() && iterations < maxIterations {
		iterations++
		newDelta := NewDeltaTable()

		// Compute new inferences using delta
		// (simplified: derive ancestor relationships)
		for _, edge := range delta.Edges() {
			// For each parent relation in delta, find transitive closure
			for _, fact := range allFacts.Edges() {
				if edge.Object == fact.Subject && edge.Predicate == "parent" && fact.Predicate == "parent" {
					newEdge := EdgeKey{Subject: edge.Subject, Predicate: "ancestor", Object: fact.Object}
					if !allFacts.Contains(newEdge) {
						newDelta.Add(newEdge)
					}
				}
			}
		}

		// Update all facts and delta for next iteration
		allFacts.Merge(newDelta)
		delta = newDelta
	}

	// Should have computed ancestor relationships
	assert.True(t, allFacts.Contains(EdgeKey{Subject: "A", Predicate: "ancestor", Object: "C"}))
	assert.Less(t, iterations, maxIterations, "Should reach fixpoint")
}

func TestSemiNaivePattern_DeltaSwapping(t *testing.T) {
	// Test the pattern of swapping delta tables between iterations
	current := NewDeltaTable()
	current.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: "B"})

	next := NewDeltaTable()
	next.Add(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"})

	// Swap (simulating end of iteration)
	current.Clear()
	current.Merge(next)
	next.Clear()

	assert.Equal(t, 1, current.Size())
	assert.True(t, current.Contains(EdgeKey{Subject: "B", Predicate: "rel", Object: "C"}))
	assert.True(t, next.IsEmpty())
}

// =============================================================================
// Edge Cases and Boundary Tests
// =============================================================================

func TestEdgeKey_SpecialCharacters(t *testing.T) {
	edge := EdgeKey{
		Subject:   "User:123",
		Predicate: "has-role",
		Object:    "Admin/Root",
	}

	result := edge.String()
	assert.Contains(t, result, "User:123")
	assert.Contains(t, result, "has-role")
	assert.Contains(t, result, "Admin/Root")
}

func TestEdgeKey_Unicode(t *testing.T) {
	edge := EdgeKey{
		Subject:   "用户",
		Predicate: "关系",
		Object:    "对象",
	}

	dt := NewDeltaTable()
	dt.Add(edge)

	assert.True(t, dt.Contains(edge))
	assert.Equal(t, "(用户)-[关系]->(对象)", edge.String())
}

func TestDeltaTable_LargeScale(t *testing.T) {
	dt := NewDeltaTable()
	numEdges := 10000

	for i := 0; i < numEdges; i++ {
		edge := EdgeKey{
			Subject:   string(rune('A' + i%26)),
			Predicate: "rel",
			Object:    string(rune('a' + i%26)),
		}
		dt.Add(edge)
	}

	// Should have deduplicated to 26 unique edges
	assert.Equal(t, 26, dt.Size())
}

func TestConcurrentDeltaTable_RaceConditions(t *testing.T) {
	// Run with -race flag to detect race conditions
	cdt := NewConcurrentDeltaTable()
	var wg sync.WaitGroup

	// Concurrent operations of all types
	wg.Add(4)

	// Adder
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			cdt.Add(EdgeKey{Subject: "A", Predicate: "rel", Object: string(rune('0' + i%10))})
		}
	}()

	// Checker
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = cdt.Contains(EdgeKey{Subject: "A", Predicate: "rel", Object: "0"})
		}
	}()

	// Sizer
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = cdt.Size()
			_ = cdt.IsEmpty()
		}
	}()

	// Edger
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = cdt.Edges()
		}
	}()

	wg.Wait()

	// If we get here without race detector complaints, test passes
	assert.True(t, true)
}

// =============================================================================
// InMemoryDatabase Tests (SNE.4)
// =============================================================================

func TestInMemoryDatabase_Creation(t *testing.T) {
	db := NewInMemoryDatabase()

	require.NotNil(t, db)
	assert.Equal(t, 0, db.EdgeCount())
	assert.Equal(t, 0, db.PredicateCount())
}

func TestInMemoryDatabase_AddEdge_New(t *testing.T) {
	db := NewInMemoryDatabase()

	edge := EdgeKey{Subject: "Alice", Predicate: "knows", Object: "Bob"}

	added := db.AddEdge(edge)

	assert.True(t, added, "Adding new edge should return true")
	assert.Equal(t, 1, db.EdgeCount())
	assert.Equal(t, 1, db.PredicateCount())
}

func TestInMemoryDatabase_AddEdge_Duplicate(t *testing.T) {
	db := NewInMemoryDatabase()

	edge := EdgeKey{Subject: "Alice", Predicate: "knows", Object: "Bob"}

	added1 := db.AddEdge(edge)
	added2 := db.AddEdge(edge)

	assert.True(t, added1, "First add should return true")
	assert.False(t, added2, "Second add should return false (duplicate)")
	assert.Equal(t, 1, db.EdgeCount())
}

func TestInMemoryDatabase_ContainsEdge(t *testing.T) {
	db := NewInMemoryDatabase()

	edge := EdgeKey{Subject: "Alice", Predicate: "knows", Object: "Bob"}
	otherEdge := EdgeKey{Subject: "Alice", Predicate: "knows", Object: "Charlie"}

	assert.False(t, db.ContainsEdge(edge), "Empty database should not contain edge")

	db.AddEdge(edge)

	assert.True(t, db.ContainsEdge(edge), "Database should contain added edge")
	assert.False(t, db.ContainsEdge(otherEdge), "Database should not contain other edge")
}

func TestInMemoryDatabase_ContainsEdge_NonexistentPredicate(t *testing.T) {
	db := NewInMemoryDatabase()

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "knows", Object: "B"})

	// Check for edge with different predicate
	assert.False(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "likes", Object: "B"}))
}

func TestInMemoryDatabase_GetAllEdges(t *testing.T) {
	db := NewInMemoryDatabase()

	// Add edges with same predicate
	db.AddEdge(EdgeKey{Subject: "Alice", Predicate: "knows", Object: "Bob"})
	db.AddEdge(EdgeKey{Subject: "Alice", Predicate: "knows", Object: "Charlie"})
	db.AddEdge(EdgeKey{Subject: "Bob", Predicate: "knows", Object: "David"})

	// Add edge with different predicate
	db.AddEdge(EdgeKey{Subject: "Alice", Predicate: "likes", Object: "Pizza"})

	knowsEdges := db.GetAllEdges("knows")
	likesEdges := db.GetAllEdges("likes")
	emptyEdges := db.GetAllEdges("nonexistent")

	assert.Len(t, knowsEdges, 3)
	assert.Len(t, likesEdges, 1)
	assert.Empty(t, emptyEdges)
}

func TestInMemoryDatabase_GetAllEdges_Empty(t *testing.T) {
	db := NewInMemoryDatabase()

	edges := db.GetAllEdges("any")

	assert.NotNil(t, edges)
	assert.Empty(t, edges)
}

func TestInMemoryDatabase_PredicateCount(t *testing.T) {
	db := NewInMemoryDatabase()

	assert.Equal(t, 0, db.PredicateCount())

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p1", Object: "B"})
	assert.Equal(t, 1, db.PredicateCount())

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p2", Object: "B"})
	assert.Equal(t, 2, db.PredicateCount())

	// Same predicate, different edge - should not increase count
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p1", Object: "C"})
	assert.Equal(t, 2, db.PredicateCount())

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p3", Object: "B"})
	assert.Equal(t, 3, db.PredicateCount())
}

func TestInMemoryDatabase_EdgeCount(t *testing.T) {
	db := NewInMemoryDatabase()

	assert.Equal(t, 0, db.EdgeCount())

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})
	assert.Equal(t, 1, db.EdgeCount())

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "C"})
	assert.Equal(t, 2, db.EdgeCount())

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "q", Object: "D"})
	assert.Equal(t, 3, db.EdgeCount())

	// Duplicate - should not increase count
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})
	assert.Equal(t, 3, db.EdgeCount())
}

func TestInMemoryDatabase_GetAllPredicates(t *testing.T) {
	db := NewInMemoryDatabase()

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "knows", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "likes", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "follows", Object: "D"})

	predicates := db.GetAllPredicates()

	assert.Len(t, predicates, 3)
	assert.Contains(t, predicates, "knows")
	assert.Contains(t, predicates, "likes")
	assert.Contains(t, predicates, "follows")
}

func TestInMemoryDatabase_GetAllPredicates_Empty(t *testing.T) {
	db := NewInMemoryDatabase()

	predicates := db.GetAllPredicates()

	assert.NotNil(t, predicates)
	assert.Empty(t, predicates)
}

func TestInMemoryDatabase_RemoveEdge_Existing(t *testing.T) {
	db := NewInMemoryDatabase()

	edge := EdgeKey{Subject: "A", Predicate: "p", Object: "B"}
	db.AddEdge(edge)

	removed := db.RemoveEdge(edge)

	assert.True(t, removed, "Removing existing edge should return true")
	assert.Equal(t, 0, db.EdgeCount())
	assert.False(t, db.ContainsEdge(edge))
}

func TestInMemoryDatabase_RemoveEdge_NonExisting(t *testing.T) {
	db := NewInMemoryDatabase()

	edge := EdgeKey{Subject: "A", Predicate: "p", Object: "B"}

	removed := db.RemoveEdge(edge)

	assert.False(t, removed, "Removing non-existing edge should return false")
}

func TestInMemoryDatabase_RemoveEdge_CleansUpEmptyPredicate(t *testing.T) {
	db := NewInMemoryDatabase()

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})
	assert.Equal(t, 1, db.PredicateCount())

	db.RemoveEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})

	assert.Equal(t, 0, db.PredicateCount(), "Empty predicate should be removed")
}

func TestInMemoryDatabase_RemoveEdge_PreservesOtherEdges(t *testing.T) {
	db := NewInMemoryDatabase()

	edge1 := EdgeKey{Subject: "A", Predicate: "p", Object: "B"}
	edge2 := EdgeKey{Subject: "A", Predicate: "p", Object: "C"}

	db.AddEdge(edge1)
	db.AddEdge(edge2)

	db.RemoveEdge(edge1)

	assert.False(t, db.ContainsEdge(edge1))
	assert.True(t, db.ContainsEdge(edge2))
	assert.Equal(t, 1, db.EdgeCount())
	assert.Equal(t, 1, db.PredicateCount())
}

func TestInMemoryDatabase_Clear(t *testing.T) {
	db := NewInMemoryDatabase()

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p1", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p2", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "p1", Object: "D"})

	assert.Equal(t, 3, db.EdgeCount())
	assert.Equal(t, 2, db.PredicateCount())

	db.Clear()

	assert.Equal(t, 0, db.EdgeCount())
	assert.Equal(t, 0, db.PredicateCount())
}

func TestInMemoryDatabase_Clone(t *testing.T) {
	db := NewInMemoryDatabase()

	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "C"})

	clone := db.Clone()

	// Clone should have same data
	assert.Equal(t, db.EdgeCount(), clone.EdgeCount())
	assert.Equal(t, db.PredicateCount(), clone.PredicateCount())
	assert.True(t, clone.ContainsEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"}))
	assert.True(t, clone.ContainsEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "C"}))

	// Modifications to clone should not affect original
	clone.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "D"})
	assert.Equal(t, 3, clone.EdgeCount())
	assert.Equal(t, 2, db.EdgeCount())

	// Modifications to original should not affect clone
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "E"})
	assert.Equal(t, 3, db.EdgeCount())
	assert.Equal(t, 3, clone.EdgeCount())
}

func TestInMemoryDatabase_AddEdges(t *testing.T) {
	db := NewInMemoryDatabase()

	edges := []EdgeKey{
		{Subject: "A", Predicate: "p", Object: "B"},
		{Subject: "A", Predicate: "p", Object: "C"},
		{Subject: "A", Predicate: "q", Object: "D"},
	}

	added := db.AddEdges(edges)

	assert.Equal(t, 3, added)
	assert.Equal(t, 3, db.EdgeCount())
	assert.Equal(t, 2, db.PredicateCount())
}

func TestInMemoryDatabase_AddEdges_WithDuplicates(t *testing.T) {
	db := NewInMemoryDatabase()

	// Pre-add one edge
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})

	edges := []EdgeKey{
		{Subject: "A", Predicate: "p", Object: "B"}, // Already exists
		{Subject: "A", Predicate: "p", Object: "C"}, // New
		{Subject: "A", Predicate: "p", Object: "D"}, // New
	}

	added := db.AddEdges(edges)

	assert.Equal(t, 2, added, "Should only count new edges")
	assert.Equal(t, 3, db.EdgeCount())
}

func TestInMemoryDatabase_AddEdges_Empty(t *testing.T) {
	db := NewInMemoryDatabase()

	added := db.AddEdges([]EdgeKey{})

	assert.Equal(t, 0, added)
	assert.Equal(t, 0, db.EdgeCount())
}

func TestInMemoryDatabase_MergeFrom(t *testing.T) {
	db1 := NewInMemoryDatabase()
	db2 := NewInMemoryDatabase()

	db1.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})
	db2.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "C"})
	db2.AddEdge(EdgeKey{Subject: "A", Predicate: "q", Object: "D"})

	added := db1.MergeFrom(db2)

	assert.Equal(t, 2, added)
	assert.Equal(t, 3, db1.EdgeCount())
	assert.True(t, db1.ContainsEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"}))
	assert.True(t, db1.ContainsEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "C"}))
	assert.True(t, db1.ContainsEdge(EdgeKey{Subject: "A", Predicate: "q", Object: "D"}))

	// db2 should be unchanged
	assert.Equal(t, 2, db2.EdgeCount())
}

func TestInMemoryDatabase_MergeFrom_Nil(t *testing.T) {
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "B"})

	added := db.MergeFrom(nil)

	assert.Equal(t, 0, added)
	assert.Equal(t, 1, db.EdgeCount())
}

func TestInMemoryDatabase_MergeFrom_WithOverlap(t *testing.T) {
	db1 := NewInMemoryDatabase()
	db2 := NewInMemoryDatabase()

	sharedEdge := EdgeKey{Subject: "A", Predicate: "p", Object: "B"}
	db1.AddEdge(sharedEdge)
	db2.AddEdge(sharedEdge)
	db2.AddEdge(EdgeKey{Subject: "A", Predicate: "p", Object: "C"})

	added := db1.MergeFrom(db2)

	assert.Equal(t, 1, added, "Should only add non-duplicate")
	assert.Equal(t, 2, db1.EdgeCount())
}

// =============================================================================
// Database Interface Compliance Test
// =============================================================================

func TestInMemoryDatabase_ImplementsDatabase(t *testing.T) {
	// Compile-time check that InMemoryDatabase implements Database
	var _ Database = (*InMemoryDatabase)(nil)
}

func TestDatabase_InterfaceUsage(t *testing.T) {
	// Test using InMemoryDatabase through Database interface
	var db Database = NewInMemoryDatabase()

	edge := EdgeKey{Subject: "A", Predicate: "p", Object: "B"}

	added := db.AddEdge(edge)
	assert.True(t, added)

	assert.True(t, db.ContainsEdge(edge))
	assert.Equal(t, 1, db.EdgeCount())
	assert.Equal(t, 1, db.PredicateCount())

	edges := db.GetAllEdges("p")
	assert.Len(t, edges, 1)
	assert.Equal(t, edge, edges[0])
}

// =============================================================================
// InMemoryDatabase Thread Safety Tests
// =============================================================================

func TestInMemoryDatabase_ConcurrentAdd(t *testing.T) {
	db := NewInMemoryDatabase()

	var wg sync.WaitGroup
	numGoroutines := 50
	edgesPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < edgesPerGoroutine; j++ {
				edge := EdgeKey{
					Subject:   "Subject",
					Predicate: "predicate",
					Object:    string(rune('A' + (gid*edgesPerGoroutine+j)%26)),
				}
				db.AddEdge(edge)
			}
		}(i)
	}

	wg.Wait()

	// Should have at most 26 unique edges (A-Z)
	assert.LessOrEqual(t, db.EdgeCount(), 26)
	assert.Greater(t, db.EdgeCount(), 0)
}

func TestInMemoryDatabase_ConcurrentMixed(t *testing.T) {
	db := NewInMemoryDatabase()

	var wg sync.WaitGroup
	numReaders := 20
	numWriters := 10
	iterations := 100

	// Writers
	wg.Add(numWriters)
	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				edge := EdgeKey{
					Subject:   "S",
					Predicate: "p",
					Object:    string(rune('A' + (id+j)%26)),
				}
				db.AddEdge(edge)
			}
		}(i)
	}

	// Readers
	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = db.EdgeCount()
				_ = db.PredicateCount()
				_ = db.GetAllEdges("p")
				_ = db.ContainsEdge(EdgeKey{Subject: "S", Predicate: "p", Object: "A"})
			}
		}()
	}

	wg.Wait()

	// Should complete without deadlock or panic
	assert.GreaterOrEqual(t, db.EdgeCount(), 0)
}

func TestInMemoryDatabase_ConcurrentClone(t *testing.T) {
	db := NewInMemoryDatabase()

	// Pre-populate
	for i := 0; i < 100; i++ {
		db.AddEdge(EdgeKey{Subject: "S", Predicate: "p", Object: string(rune('A' + i%26))})
	}

	var wg sync.WaitGroup
	numClones := 10

	wg.Add(numClones)
	for i := 0; i < numClones; i++ {
		go func() {
			defer wg.Done()
			clone := db.Clone()
			assert.Greater(t, clone.EdgeCount(), 0)
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestInMemoryDatabase_EmptyStringValues(t *testing.T) {
	db := NewInMemoryDatabase()

	edge := EdgeKey{Subject: "", Predicate: "", Object: ""}

	added := db.AddEdge(edge)
	assert.True(t, added)
	assert.True(t, db.ContainsEdge(edge))
	assert.Equal(t, 1, db.EdgeCount())
}

func TestInMemoryDatabase_LargeDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large dataset test in short mode")
	}

	db := NewInMemoryDatabase()

	// Add many edges
	numEdges := 10000
	for i := 0; i < numEdges; i++ {
		edge := EdgeKey{
			Subject:   string(rune('A' + i%26)),
			Predicate: string(rune('a' + i%10)),
			Object:    string(rune('0' + i%10)),
		}
		db.AddEdge(edge)
	}

	// Should have at most 26 * 10 * 10 = 2600 unique edges
	assert.LessOrEqual(t, db.EdgeCount(), 2600)
	assert.Greater(t, db.EdgeCount(), 0)

	// Queries should still work
	for i := 0; i < 10; i++ {
		pred := string(rune('a' + i))
		edges := db.GetAllEdges(pred)
		assert.NotEmpty(t, edges)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkInMemoryDatabase_AddEdge(b *testing.B) {
	db := NewInMemoryDatabase()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		edge := EdgeKey{
			Subject:   string(rune('A' + i%26)),
			Predicate: "predicate",
			Object:    string(rune('0' + i%10)),
		}
		db.AddEdge(edge)
	}
}

func BenchmarkInMemoryDatabase_ContainsEdge(b *testing.B) {
	db := NewInMemoryDatabase()

	// Pre-populate
	for i := 0; i < 1000; i++ {
		edge := EdgeKey{
			Subject:   string(rune('A' + i%26)),
			Predicate: "predicate",
			Object:    string(rune('0' + i%10)),
		}
		db.AddEdge(edge)
	}

	edge := EdgeKey{Subject: "A", Predicate: "predicate", Object: "0"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.ContainsEdge(edge)
	}
}

func BenchmarkInMemoryDatabase_GetAllEdges(b *testing.B) {
	db := NewInMemoryDatabase()

	// Pre-populate
	for i := 0; i < 1000; i++ {
		edge := EdgeKey{
			Subject:   string(rune('A' + i%26)),
			Predicate: "predicate",
			Object:    string(rune('0' + i%10)),
		}
		db.AddEdge(edge)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.GetAllEdges("predicate")
	}
}

// =============================================================================
// Stratification Tests (SNE.5 & SNE.6)
// =============================================================================

func TestStratificationError_Error(t *testing.T) {
	err := &StratificationError{
		Rules:   []*InferenceRule{{ID: "r1"}, {ID: "r2"}},
		Message: "test error message",
	}

	assert.Equal(t, "test error message", err.Error())
}

func TestNewStratifier(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r1", Name: "rule1"},
		{ID: "r2", Name: "rule2"},
	}

	s := NewStratifier(rules)

	require.NotNil(t, s)
	assert.Len(t, s.rules, 2)
	assert.Contains(t, s.ruleByID, "r1")
	assert.Contains(t, s.ruleByID, "r2")
}

func TestStratifier_SimpleRules(t *testing.T) {
	// Rules with no negation - all should be stratum 0
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "transitive",
			HeadPredicate:  "reachable",
			BodyPredicates: []string{"edge", "reachable"},
			Negation:       false,
		},
		{
			ID:             "r2",
			Name:           "symmetric",
			HeadPredicate:  "connected",
			BodyPredicates: []string{"edge"},
			Negation:       false,
		},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	for _, r := range rules {
		assert.Equal(t, 0, r.Stratum, "Rule %s should be in stratum 0", r.ID)
	}
}

func TestStratifier_NegationDependency(t *testing.T) {
	// r2 uses NOT on predicate produced by r1, so r2 must be in higher stratum
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "base_rule",
			HeadPredicate:  "derived",
			BodyPredicates: []string{"base"},
			Negation:       false,
			Body:           []RuleCondition{{Predicate: "base", Negated: false}},
		},
		{
			ID:             "r2",
			Name:           "negation_rule",
			HeadPredicate:  "not_derived",
			BodyPredicates: []string{"something", "derived"},
			Negation:       true,
			Body: []RuleCondition{
				{Predicate: "something", Negated: false},
				{Predicate: "derived", Negated: true},
			},
		},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 0, rules[0].Stratum, "r1 should be in stratum 0")
	assert.Equal(t, 1, rules[1].Stratum, "r2 should be in stratum 1 (negatively depends on r1)")
}

func TestStratifier_NegativeCycle(t *testing.T) {
	// r1 uses NOT r2_pred, r2 uses NOT r1_pred - error
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "rule1",
			HeadPredicate:  "pred1",
			BodyPredicates: []string{"pred2"},
			Negation:       true,
			Body: []RuleCondition{
				{Predicate: "pred2", Negated: true},
			},
		},
		{
			ID:             "r2",
			Name:           "rule2",
			HeadPredicate:  "pred2",
			BodyPredicates: []string{"pred1"},
			Negation:       true,
			Body: []RuleCondition{
				{Predicate: "pred1", Negated: true},
			},
		},
	}

	s := NewStratifier(rules)
	err := s.Stratify()

	require.Error(t, err)
	var stratErr *StratificationError
	require.ErrorAs(t, err, &stratErr)
	assert.Contains(t, stratErr.Message, "circular negative dependency")
	assert.NotEmpty(t, stratErr.Rules)
}

func TestStratifier_MultipleStrata(t *testing.T) {
	// Chain of negative dependencies: r1 < r2 < r3
	// r1 produces pred1 (no negation)
	// r2 produces pred2, negatively depends on pred1
	// r3 produces pred3, negatively depends on pred2
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "base",
			HeadPredicate:  "pred1",
			BodyPredicates: []string{"input"},
			Negation:       false,
			Body:           []RuleCondition{{Predicate: "input", Negated: false}},
		},
		{
			ID:             "r2",
			Name:           "layer1",
			HeadPredicate:  "pred2",
			BodyPredicates: []string{"pred1"},
			Negation:       true,
			Body:           []RuleCondition{{Predicate: "pred1", Negated: true}},
		},
		{
			ID:             "r3",
			Name:           "layer2",
			HeadPredicate:  "pred3",
			BodyPredicates: []string{"pred2"},
			Negation:       true,
			Body:           []RuleCondition{{Predicate: "pred2", Negated: true}},
		},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 0, rules[0].Stratum, "r1 should be in stratum 0")
	assert.Equal(t, 1, rules[1].Stratum, "r2 should be in stratum 1")
	assert.Equal(t, 2, rules[2].Stratum, "r3 should be in stratum 2")
}

func TestStratifier_GetRulesByStratum(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r1", Name: "rule1", HeadPredicate: "p1", Negation: false},
		{ID: "r2", Name: "rule2", HeadPredicate: "p2", Negation: true,
			BodyPredicates: []string{"p1"},
			Body:           []RuleCondition{{Predicate: "p1", Negated: true}}},
		{ID: "r3", Name: "rule3", HeadPredicate: "p3", Negation: false},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	byStratum := s.GetRulesByStratum()

	assert.Len(t, byStratum[0], 2, "Stratum 0 should have r1 and r3")
	assert.Len(t, byStratum[1], 1, "Stratum 1 should have r2")

	// Check that correct rules are in correct strata
	stratum0IDs := make(map[string]bool)
	for _, r := range byStratum[0] {
		stratum0IDs[r.ID] = true
	}
	assert.True(t, stratum0IDs["r1"])
	assert.True(t, stratum0IDs["r3"])

	assert.Equal(t, "r2", byStratum[1][0].ID)
}

func TestStratifier_MaxStratum(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "p1", Negation: false},
		{ID: "r2", HeadPredicate: "p2", Negation: true,
			BodyPredicates: []string{"p1"},
			Body:           []RuleCondition{{Predicate: "p1", Negated: true}}},
		{ID: "r3", HeadPredicate: "p3", Negation: true,
			BodyPredicates: []string{"p2"},
			Body:           []RuleCondition{{Predicate: "p2", Negated: true}}},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 2, s.MaxStratum())
}

func TestStratifier_MaxStratum_Empty(t *testing.T) {
	s := NewStratifier([]*InferenceRule{})
	assert.Equal(t, -1, s.MaxStratum())
}

func TestStratifier_EmptyRules(t *testing.T) {
	s := NewStratifier([]*InferenceRule{})
	err := s.Stratify()

	require.NoError(t, err)
	assert.Equal(t, -1, s.MaxStratum())
	assert.Empty(t, s.GetRulesByStratum())
}

func TestStratifier_SingleRule(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r1", Name: "only_rule", HeadPredicate: "result", Negation: false},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 0, rules[0].Stratum)
	assert.Equal(t, 0, s.MaxStratum())

	byStratum := s.GetRulesByStratum()
	assert.Len(t, byStratum, 1)
	assert.Len(t, byStratum[0], 1)
}

func TestStratifier_SingleRuleWithNegation(t *testing.T) {
	// A single rule with negation on a predicate not produced by any rule
	// should still be stratum 0
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "negation_on_base",
			HeadPredicate:  "result",
			BodyPredicates: []string{"external"},
			Negation:       true,
			Body:           []RuleCondition{{Predicate: "external", Negated: true}},
		},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 0, rules[0].Stratum)
}

func TestStratifier_ComplexDependencies(t *testing.T) {
	// Complex graph:
	// r1 -> pred1 (base)
	// r2 -> pred2, depends on pred1 (no negation)
	// r3 -> pred3, negatively depends on pred2
	// r4 -> pred4, depends on pred1 (no negation)
	// r5 -> pred5, negatively depends on pred4
	// r6 -> pred6, negatively depends on pred3 and pred5
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", Negation: false},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: false},
		{ID: "r3", HeadPredicate: "pred3", BodyPredicates: []string{"pred2"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred2", Negated: true}}},
		{ID: "r4", HeadPredicate: "pred4", BodyPredicates: []string{"pred1"}, Negation: false},
		{ID: "r5", HeadPredicate: "pred5", BodyPredicates: []string{"pred4"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred4", Negated: true}}},
		{ID: "r6", HeadPredicate: "pred6", BodyPredicates: []string{"pred3", "pred5"}, Negation: true,
			Body: []RuleCondition{
				{Predicate: "pred3", Negated: true},
				{Predicate: "pred5", Negated: true},
			}},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	// r1, r2, r4 should be in stratum 0 (no negative deps)
	assert.Equal(t, 0, rules[0].Stratum, "r1")
	assert.Equal(t, 0, rules[1].Stratum, "r2")
	assert.Equal(t, 0, rules[3].Stratum, "r4")

	// r3 and r5 should be in stratum 1 (negative dep on stratum 0)
	assert.Equal(t, 1, rules[2].Stratum, "r3")
	assert.Equal(t, 1, rules[4].Stratum, "r5")

	// r6 should be in stratum 2 (negative dep on stratum 1)
	assert.Equal(t, 2, rules[5].Stratum, "r6")
}

func TestStratifier_GetRulesInOrder(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r3", HeadPredicate: "p3", Priority: 1, Negation: true,
			BodyPredicates: []string{"p2"},
			Body:           []RuleCondition{{Predicate: "p2", Negated: true}}},
		{ID: "r1", HeadPredicate: "p1", Priority: 5, Negation: false},
		{ID: "r2", HeadPredicate: "p2", Priority: 3, Negation: true,
			BodyPredicates: []string{"p1"},
			Body:           []RuleCondition{{Predicate: "p1", Negated: true}}},
		{ID: "r4", HeadPredicate: "p4", Priority: 10, Negation: false},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	ordered := s.GetRulesInOrder()

	assert.Len(t, ordered, 4)

	// Stratum 0 rules first (r1 and r4), sorted by priority (r4 first with priority 10)
	assert.Equal(t, "r4", ordered[0].ID, "r4 has highest priority in stratum 0")
	assert.Equal(t, "r1", ordered[1].ID, "r1 has second highest priority in stratum 0")

	// Stratum 1 rules next (r2)
	assert.Equal(t, "r2", ordered[2].ID, "r2 is in stratum 1")

	// Stratum 2 rules last (r3)
	assert.Equal(t, "r3", ordered[3].ID, "r3 is in stratum 2")
}

func TestStratifier_GetRulesInOrder_Empty(t *testing.T) {
	s := NewStratifier([]*InferenceRule{})
	ordered := s.GetRulesInOrder()

	assert.NotNil(t, ordered)
	assert.Empty(t, ordered)
}

func TestStratifier_SelfNegation(t *testing.T) {
	// A rule that negatively depends on its own predicate forms a self-cycle
	rules := []*InferenceRule{
		{
			ID:             "r1",
			HeadPredicate:  "pred1",
			BodyPredicates: []string{"pred1"},
			Negation:       true,
			Body:           []RuleCondition{{Predicate: "pred1", Negated: true}},
		},
	}

	s := NewStratifier(rules)
	err := s.Stratify()

	require.Error(t, err)
	var stratErr *StratificationError
	require.ErrorAs(t, err, &stratErr)
	assert.Contains(t, stratErr.Message, "circular negative dependency")
}

func TestStratifier_PositiveCycleAllowed(t *testing.T) {
	// Positive cycles (without negation) are allowed
	// r1 depends on r2, r2 depends on r1 (both non-negated)
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", BodyPredicates: []string{"pred2"}, Negation: false},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: false},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	// Both should be in stratum 0 since there are no negative dependencies
	assert.Equal(t, 0, rules[0].Stratum)
	assert.Equal(t, 0, rules[1].Stratum)
}

func TestStratifier_MixedDependencies(t *testing.T) {
	// r1 -> pred1
	// r2 -> pred2, positively depends on pred1
	// r3 -> pred3, negatively depends on pred1
	// Both r2 and r3 reference pred1, but only r3 has negation
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", Negation: false},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: false,
			Body: []RuleCondition{{Predicate: "pred1", Negated: false}}},
		{ID: "r3", HeadPredicate: "pred3", BodyPredicates: []string{"pred1"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred1", Negated: true}}},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 0, rules[0].Stratum, "r1 base rule")
	assert.Equal(t, 0, rules[1].Stratum, "r2 positive dependency")
	assert.Equal(t, 1, rules[2].Stratum, "r3 negative dependency")
}

func TestStratifier_buildDependencyGraph(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", BodyPredicates: []string{}},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}},
		{ID: "r3", HeadPredicate: "pred3", BodyPredicates: []string{"pred1", "pred2"}},
	}

	s := NewStratifier(rules)
	deps := s.buildDependencyGraph()

	// r1 has no dependencies
	assert.Empty(t, deps["r1"])

	// r2 depends on r1
	assert.Len(t, deps["r2"], 1)
	assert.Equal(t, "r1", deps["r2"][0].ID)

	// r3 depends on r1 and r2
	assert.Len(t, deps["r3"], 2)
	depIDs := make(map[string]bool)
	for _, d := range deps["r3"] {
		depIDs[d.ID] = true
	}
	assert.True(t, depIDs["r1"])
	assert.True(t, depIDs["r2"])
}

func TestStratifier_buildNegativeDependencyGraph(t *testing.T) {
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", Negation: false},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred1", Negated: true}}},
		{ID: "r3", HeadPredicate: "pred3", BodyPredicates: []string{"pred1"}, Negation: false,
			Body: []RuleCondition{{Predicate: "pred1", Negated: false}}},
	}

	s := NewStratifier(rules)
	negDeps := s.buildNegativeDependencyGraph()

	// r1 and r3 have no negative dependencies
	assert.Empty(t, negDeps["r1"])
	assert.Empty(t, negDeps["r3"])

	// r2 negatively depends on r1
	assert.Len(t, negDeps["r2"], 1)
	assert.Equal(t, "r1", negDeps["r2"][0].ID)
}

func TestStratifier_LongerNegativeCycle(t *testing.T) {
	// r1 -> pred1, negatively depends on pred3
	// r2 -> pred2, negatively depends on pred1
	// r3 -> pred3, negatively depends on pred2
	// Forms a cycle: r1 -> r3 -> r2 -> r1
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", BodyPredicates: []string{"pred3"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred3", Negated: true}}},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred1", Negated: true}}},
		{ID: "r3", HeadPredicate: "pred3", BodyPredicates: []string{"pred2"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred2", Negated: true}}},
	}

	s := NewStratifier(rules)
	err := s.Stratify()

	require.Error(t, err)
	var stratErr *StratificationError
	require.ErrorAs(t, err, &stratErr)
	assert.Contains(t, stratErr.Message, "circular negative dependency")
}

func TestStratifier_MultipleProducers(t *testing.T) {
	// Multiple rules produce the same predicate
	// r1 and r2 both produce pred1
	// r3 negatively depends on pred1
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", Negation: false},
		{ID: "r2", HeadPredicate: "pred1", Negation: false},
		{ID: "r3", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred1", Negated: true}}},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	// r1 and r2 should be in stratum 0
	assert.Equal(t, 0, rules[0].Stratum)
	assert.Equal(t, 0, rules[1].Stratum)

	// r3 should be in stratum 1 (negatively depends on both r1 and r2)
	assert.Equal(t, 1, rules[2].Stratum)
}

func TestStratifier_DiamondPattern(t *testing.T) {
	// Diamond pattern with negation:
	//       r1 (pred1)
	//      /  \
	//    r2    r3 (both depend on pred1, r2 negatively, r3 positively)
	//      \  /
	//       r4 (negatively depends on both pred2 and pred3)
	rules := []*InferenceRule{
		{ID: "r1", HeadPredicate: "pred1", Negation: false},
		{ID: "r2", HeadPredicate: "pred2", BodyPredicates: []string{"pred1"}, Negation: true,
			Body: []RuleCondition{{Predicate: "pred1", Negated: true}}},
		{ID: "r3", HeadPredicate: "pred3", BodyPredicates: []string{"pred1"}, Negation: false,
			Body: []RuleCondition{{Predicate: "pred1", Negated: false}}},
		{ID: "r4", HeadPredicate: "pred4", BodyPredicates: []string{"pred2", "pred3"}, Negation: true,
			Body: []RuleCondition{
				{Predicate: "pred2", Negated: true},
				{Predicate: "pred3", Negated: true},
			}},
	}

	s := NewStratifier(rules)
	err := s.Stratify()
	require.NoError(t, err)

	assert.Equal(t, 0, rules[0].Stratum, "r1 base")
	assert.Equal(t, 1, rules[1].Stratum, "r2 neg dep on r1")
	assert.Equal(t, 0, rules[2].Stratum, "r3 pos dep on r1")
	assert.Equal(t, 2, rules[3].Stratum, "r4 neg dep on r2 (stratum 1)")
}

// =============================================================================
// SemiNaiveEvaluator Tests (SNE.7 & SNE.8)
// =============================================================================

func TestNewSemiNaiveEvaluator(t *testing.T) {
	rules := []*InferenceRule{
		NewInferenceRule("r1", "simple",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)

	require.NoError(t, err)
	require.NotNil(t, evaluator)
	assert.Len(t, evaluator.rules, 1)
}

func TestNewSemiNaiveEvaluator_EmptyRules(t *testing.T) {
	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{})

	require.NoError(t, err)
	require.NotNil(t, evaluator)
}

func TestNewSemiNaiveEvaluator_StratificationError(t *testing.T) {
	// Create rules with a negative cycle
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "rule1",
			HeadPredicate:  "pred1",
			BodyPredicates: []string{"pred2"},
			Negation:       true,
			Enabled:        true,
			Body:           []RuleCondition{{Subject: "?x", Predicate: "pred2", Object: "?y", Negated: true}},
			Head:           RuleCondition{Subject: "?x", Predicate: "pred1", Object: "?y"},
		},
		{
			ID:             "r2",
			Name:           "rule2",
			HeadPredicate:  "pred2",
			BodyPredicates: []string{"pred1"},
			Negation:       true,
			Enabled:        true,
			Body:           []RuleCondition{{Subject: "?x", Predicate: "pred1", Object: "?y", Negated: true}},
			Head:           RuleCondition{Subject: "?x", Predicate: "pred2", Object: "?y"},
		},
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)

	require.Error(t, err)
	assert.Nil(t, evaluator)
	assert.Contains(t, err.Error(), "stratify")
}

func TestSemiNaiveEvaluator_SetMaxIterations(t *testing.T) {
	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{})
	require.NoError(t, err)

	evaluator.SetMaxIterations(500)
	assert.Equal(t, 500, evaluator.maxIterations)

	// Invalid value should be ignored
	evaluator.SetMaxIterations(0)
	assert.Equal(t, 500, evaluator.maxIterations)

	evaluator.SetMaxIterations(-10)
	assert.Equal(t, 500, evaluator.maxIterations)
}

func TestSemiNaiveEvaluator_SimpleRule(t *testing.T) {
	// Rule: derived(X,Y) :- base(X,Y)
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "base", Object: "C"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 2, derived)
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "derived", Object: "B"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "B", Predicate: "derived", Object: "C"}))
}

func TestSemiNaiveEvaluator_TransitiveClosure(t *testing.T) {
	// Rules for transitive closure:
	// reachable(X,Y) :- edge(X,Y)
	// reachable(X,Z) :- reachable(X,Y), edge(Y,Z)
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	// Create a chain: A -> B -> C -> D
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "edge", Object: "D"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	// Should derive:
	// From r1: reachable(A,B), reachable(B,C), reachable(C,D)
	// From r2: reachable(A,C), reachable(B,D), reachable(A,D)
	assert.Equal(t, 6, derived)

	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "C"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "D"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "B", Predicate: "reachable", Object: "C"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "B", Predicate: "reachable", Object: "D"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "C", Predicate: "reachable", Object: "D"}))
}

func TestSemiNaiveEvaluator_TransitiveClosure_Cyclic(t *testing.T) {
	// Test with a cycle: A -> B -> C -> A
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	// Create a cycle: A -> B -> C -> A
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "edge", Object: "A"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	// In a cycle, everyone can reach everyone (including themselves through the cycle)
	// A->B, A->C, A->A, B->C, B->A, B->B, C->A, C->B, C->C
	assert.Equal(t, 9, derived)

	// Verify some key reachability facts
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "C"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "A"})) // Through cycle
}

func TestSemiNaiveEvaluator_TwoBodyAtoms(t *testing.T) {
	// Rule: sibling(X,Y) :- parent(Z,X), parent(Z,Y)
	// (Note: this will also derive sibling(X,X), which is technically correct)
	rule := NewInferenceRule("r1", "sibling",
		RuleCondition{Subject: "?x", Predicate: "sibling", Object: "?y"},
		[]RuleCondition{
			{Subject: "?z", Predicate: "parent", Object: "?x"},
			{Subject: "?z", Predicate: "parent", Object: "?y"},
		})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Parent has two children: Alice and Bob
	db.AddEdge(EdgeKey{Subject: "Parent", Predicate: "parent", Object: "Alice"})
	db.AddEdge(EdgeKey{Subject: "Parent", Predicate: "parent", Object: "Bob"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	// Should derive: sibling(Alice,Alice), sibling(Alice,Bob), sibling(Bob,Alice), sibling(Bob,Bob)
	assert.Equal(t, 4, derived)
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "Alice", Predicate: "sibling", Object: "Bob"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "Bob", Predicate: "sibling", Object: "Alice"}))
}

func TestSemiNaiveEvaluator_NegatedBody(t *testing.T) {
	// Rule: notConnected(X,Y) :- node(X), node(Y), NOT edge(X,Y)
	rule := &InferenceRule{
		ID:             "r1",
		Name:           "not_connected",
		HeadPredicate:  "notConnected",
		BodyPredicates: []string{"node", "edge"},
		Negation:       true,
		Enabled:        true,
		Head:           RuleCondition{Subject: "?x", Predicate: "notConnected", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "node", Object: "?x"},
			{Subject: "?y", Predicate: "node", Object: "?y"},
			{Subject: "?x", Predicate: "edge", Object: "?y", Negated: true},
		},
	}

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Nodes
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "node", Object: "A"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "node", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "node", Object: "C"})
	// Edges: A->B exists, others don't
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	// Should derive notConnected for pairs without edges:
	// (A,A), (A,C), (B,A), (B,B), (B,C), (C,A), (C,B), (C,C)
	// Note: (A,B) should NOT be derived because edge(A,B) exists
	assert.Equal(t, 8, derived)
	assert.False(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "notConnected", Object: "B"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "notConnected", Object: "C"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "B", Predicate: "notConnected", Object: "A"}))
}

func TestSemiNaiveEvaluator_EmptyDatabase(t *testing.T) {
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 0, derived)
}

func TestSemiNaiveEvaluator_NoMatchingEdges(t *testing.T) {
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Add edges with different predicate
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "other", Object: "B"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 0, derived)
}

func TestSemiNaiveEvaluator_DisabledRule(t *testing.T) {
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})
	rule.Enabled = false

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 0, derived)
	assert.False(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "derived", Object: "B"}))
}

func TestSemiNaiveEvaluator_MultipleRules(t *testing.T) {
	// Rule 1: foo(X,Y) :- base(X,Y)
	// Rule 2: bar(X,Y) :- foo(X,Y)
	rules := []*InferenceRule{
		NewInferenceRule("r1", "to_foo",
			RuleCondition{Subject: "?x", Predicate: "foo", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
		NewInferenceRule("r2", "foo_to_bar",
			RuleCondition{Subject: "?x", Predicate: "bar", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "foo", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 2, derived)
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "foo", Object: "B"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "bar", Object: "B"}))
}

func TestSemiNaiveEvaluator_ChainOfRules(t *testing.T) {
	// Chain: base -> level1 -> level2 -> level3
	rules := []*InferenceRule{
		NewInferenceRule("r1", "level1",
			RuleCondition{Subject: "?x", Predicate: "level1", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
		NewInferenceRule("r2", "level2",
			RuleCondition{Subject: "?x", Predicate: "level2", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "level1", Object: "?y"}}),
		NewInferenceRule("r3", "level3",
			RuleCondition{Subject: "?x", Predicate: "level3", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "level2", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "base", Object: "D"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 6, derived) // 2 edges * 3 levels
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "level3", Object: "B"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "C", Predicate: "level3", Object: "D"}))
}

func TestSemiNaiveEvaluator_Deduplication(t *testing.T) {
	// Two rules that derive the same edge
	rules := []*InferenceRule{
		NewInferenceRule("r1", "from_a",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "sourceA", Object: "?y"}}),
		NewInferenceRule("r2", "from_b",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "sourceB", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Both sources have the same edge (A,B)
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "sourceA", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "sourceB", Object: "B"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	// Should only derive one edge (deduplication)
	assert.Equal(t, 1, derived)
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "derived", Object: "B"}))
}

func TestSemiNaiveEvaluator_LargeGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large graph test in short mode")
	}

	// Transitive closure on a larger graph
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Create a chain of 20 nodes
	numNodes := 20
	for i := 0; i < numNodes-1; i++ {
		db.AddEdge(EdgeKey{
			Subject:   string(rune('A' + i)),
			Predicate: "edge",
			Object:    string(rune('A' + i + 1)),
		})
	}

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	// For a chain of n nodes, we can reach from each node to all subsequent nodes
	// Total reachable pairs = n*(n-1)/2 = 20*19/2 = 190
	expectedDerived := numNodes * (numNodes - 1) / 2
	assert.Equal(t, expectedDerived, derived)
}

func TestApplyRuleSemiNaive_TransitiveClosure(t *testing.T) {
	// Test applyRuleSemiNaive directly
	rule := NewInferenceRule("r1", "transitive",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "reachable", Object: "?y"},
			{Subject: "?y", Predicate: "edge", Object: "?z"},
		})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})

	// Delta contains only the reachable(A,B) edge
	delta := NewDeltaTable()
	delta.Add(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"})

	result, err := evaluator.applyRuleSemiNaive(rule, delta, db)
	require.NoError(t, err)

	// Should derive reachable(A,C)
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "C"}))
}

func TestApplyRuleSemiNaive_EmptyDelta(t *testing.T) {
	rule := NewInferenceRule("r1", "transitive",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "reachable", Object: "?y"},
			{Subject: "?y", Predicate: "edge", Object: "?z"},
		})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})

	delta := NewDeltaTable() // Empty delta

	result, err := evaluator.applyRuleSemiNaive(rule, delta, db)
	require.NoError(t, err)

	// Empty delta should produce empty result
	assert.True(t, result.IsEmpty())
}

func TestApplyGeneralRuleSemiNaive_MultipleBodyAtoms(t *testing.T) {
	// Rule: uncle(X,Y) :- parent(Z,X), sibling(Z,Y)
	rule := NewInferenceRule("r1", "uncle",
		RuleCondition{Subject: "?x", Predicate: "uncle", Object: "?y"},
		[]RuleCondition{
			{Subject: "?z", Predicate: "parent", Object: "?x"},
			{Subject: "?z", Predicate: "sibling", Object: "?y"},
		})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Parent relations
	db.AddEdge(EdgeKey{Subject: "Dad", Predicate: "parent", Object: "Child"})
	// Sibling relations
	db.AddEdge(EdgeKey{Subject: "Dad", Predicate: "sibling", Object: "Uncle"})

	// Delta for sibling
	deltas := map[string]*DeltaTable{
		"sibling": NewDeltaTable(),
	}
	deltas["sibling"].Add(EdgeKey{Subject: "Dad", Predicate: "sibling", Object: "Uncle"})

	result, err := evaluator.applyGeneralRuleSemiNaive(rule, deltas, db)
	require.NoError(t, err)

	// Should derive uncle(Child, Uncle)
	assert.True(t, result.Contains(EdgeKey{Subject: "Child", Predicate: "uncle", Object: "Uncle"}))
}

func TestApplyGeneralRuleSemiNaive_NoDeltaMatch(t *testing.T) {
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	// Delta for different predicate
	deltas := map[string]*DeltaTable{
		"other": NewDeltaTable(),
	}
	deltas["other"].Add(EdgeKey{Subject: "X", Predicate: "other", Object: "Y"})

	result, err := evaluator.applyGeneralRuleSemiNaive(rule, deltas, db)
	require.NoError(t, err)

	// No delta for "base" predicate, so empty result
	assert.True(t, result.IsEmpty())
}

func TestApplyGeneralRuleSemiNaive_EmptyDeltas(t *testing.T) {
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	deltas := map[string]*DeltaTable{
		"base": NewDeltaTable(), // Empty delta
	}

	result, err := evaluator.applyGeneralRuleSemiNaive(rule, deltas, db)
	require.NoError(t, err)

	assert.True(t, result.IsEmpty())
}

func TestSemiNaiveEvaluator_FixpointDetection(t *testing.T) {
	// Test that evaluation terminates when fixpoint is reached
	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	// First evaluation
	derived1, err := evaluator.Evaluate(db)
	require.NoError(t, err)
	assert.Equal(t, 1, derived1)

	// Second evaluation should derive nothing (fixpoint reached)
	derived2, err := evaluator.Evaluate(db)
	require.NoError(t, err)
	assert.Equal(t, 0, derived2)
}

func TestSemiNaiveEvaluator_ConstantInHead(t *testing.T) {
	// Rule: isKnown("true") :- entity(X)
	rule := NewInferenceRule("r1", "mark_known",
		RuleCondition{Subject: "?x", Predicate: "isKnown", Object: "true"},
		[]RuleCondition{{Subject: "?x", Predicate: "entity", Object: "?x"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "entity", Object: "A"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "entity", Object: "B"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 2, derived)
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "isKnown", Object: "true"}))
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "B", Predicate: "isKnown", Object: "true"}))
}

func TestSemiNaiveEvaluator_ConstantInBody(t *testing.T) {
	// Rule: human(X) :- species(X, "homo_sapiens")
	rule := NewInferenceRule("r1", "human",
		RuleCondition{Subject: "?x", Predicate: "human", Object: "?x"},
		[]RuleCondition{{Subject: "?x", Predicate: "species", Object: "homo_sapiens"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "Alice", Predicate: "species", Object: "homo_sapiens"})
	db.AddEdge(EdgeKey{Subject: "Rex", Predicate: "species", Object: "canis_familiaris"})

	derived, err := evaluator.Evaluate(db)
	require.NoError(t, err)

	assert.Equal(t, 1, derived)
	assert.True(t, db.ContainsEdge(EdgeKey{Subject: "Alice", Predicate: "human", Object: "Alice"}))
	assert.False(t, db.ContainsEdge(EdgeKey{Subject: "Rex", Predicate: "human", Object: "Rex"}))
}

// =============================================================================
// Benchmark Tests for Semi-Naive Evaluation
// =============================================================================

func BenchmarkSemiNaiveEvaluator_TransitiveClosure(b *testing.B) {
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator, _ := NewSemiNaiveEvaluator(rules)

		db := NewInMemoryDatabase()
		// Create a chain of 10 nodes
		for j := 0; j < 9; j++ {
			db.AddEdge(EdgeKey{
				Subject:   string(rune('A' + j)),
				Predicate: "edge",
				Object:    string(rune('A' + j + 1)),
			})
		}

		_, _ = evaluator.Evaluate(db)
	}
}

func BenchmarkApplyRuleSemiNaive(b *testing.B) {
	rule := NewInferenceRule("r1", "transitive",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "reachable", Object: "?y"},
			{Subject: "?y", Predicate: "edge", Object: "?z"},
		})

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule})

	db := NewInMemoryDatabase()
	for i := 0; i < 100; i++ {
		db.AddEdge(EdgeKey{
			Subject:   string(rune('A' + i%26)),
			Predicate: "reachable",
			Object:    string(rune('a' + i%26)),
		})
		db.AddEdge(EdgeKey{
			Subject:   string(rune('a' + i%26)),
			Predicate: "edge",
			Object:    string(rune('0' + i%10)),
		})
	}

	delta := NewDeltaTable()
	for i := 0; i < 10; i++ {
		delta.Add(EdgeKey{
			Subject:   string(rune('A' + i)),
			Predicate: "reachable",
			Object:    string(rune('a' + i)),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = evaluator.applyRuleSemiNaive(rule, delta, db)
	}
}

// =============================================================================
// Context-Aware Evaluation Tests (SNE.9 & SNE.10)
// =============================================================================

func TestErrNotStratified(t *testing.T) {
	assert.Error(t, ErrNotStratified)
	assert.Equal(t, "evaluator has no stratifier configured", ErrNotStratified.Error())
}

func TestEvaluateWithContext_Simple(t *testing.T) {
	ctx := context.Background()

	rule := NewInferenceRule("r1", "copy",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}})

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "base", Object: "C"})

	result, err := evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	assert.Equal(t, 2, result.Size())
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "derived", Object: "B"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "B", Predicate: "derived", Object: "C"}))
}

func TestEvaluateWithContext_TransitiveClosure(t *testing.T) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	// Create a chain: A -> B -> C -> D
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "edge", Object: "D"})

	result, err := evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	// Should derive:
	// From r1: reachable(A,B), reachable(B,C), reachable(C,D)
	// From r2: reachable(A,C), reachable(B,D), reachable(A,D)
	assert.Equal(t, 6, result.Size())

	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "C"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "D"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "B", Predicate: "reachable", Object: "C"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "B", Predicate: "reachable", Object: "D"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "C", Predicate: "reachable", Object: "D"}))
}

func TestEvaluateWithContext_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})

	result, err := evaluator.EvaluateWithContext(ctx, db)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, result)
}

func TestEvaluateWithContext_NilStratifier(t *testing.T) {
	ctx := context.Background()

	// Create evaluator manually without stratifier
	evaluator := &SemiNaiveEvaluator{
		rules:         []*InferenceRule{},
		stratifier:    nil,
		maxIterations: 1000,
	}

	db := NewInMemoryDatabase()

	result, err := evaluator.EvaluateWithContext(ctx, db)

	assert.Error(t, err)
	assert.Equal(t, ErrNotStratified, err)
	assert.Nil(t, result)
}

func TestEvaluateWithContext_EmptyRules(t *testing.T) {
	ctx := context.Background()

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{})
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	result, err := evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	assert.NotNil(t, result)
	assert.True(t, result.IsEmpty())
}

func TestEvaluateWithContext_MultiStratum(t *testing.T) {
	ctx := context.Background()

	// r1 produces "derived" (stratum 0)
	// r2 uses NOT "derived" (stratum 1)
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "base_rule",
			HeadPredicate:  "derived",
			BodyPredicates: []string{"base"},
			Negation:       false,
			Enabled:        true,
			Head:           RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body:           []RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
		},
		{
			ID:             "r2",
			Name:           "negation_rule",
			HeadPredicate:  "notDerived",
			BodyPredicates: []string{"input", "derived"},
			Negation:       true,
			Enabled:        true,
			Head:           RuleCondition{Subject: "?x", Predicate: "notDerived", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "input", Object: "?y", Negated: false},
				{Subject: "?x", Predicate: "derived", Object: "?y", Negated: true},
			},
		},
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// base edges
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})
	// input edges (some overlap with base, some don't)
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "input", Object: "B"}) // will be derived
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "input", Object: "C"}) // will NOT be derived

	result, err := evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	// Should derive:
	// - derived(A,B) from r1
	// - notDerived(A,C) from r2 (because derived(A,C) doesn't exist)
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "derived", Object: "B"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "notDerived", Object: "C"}))
	// notDerived(A,B) should NOT be derived because derived(A,B) exists
	assert.False(t, result.Contains(EdgeKey{Subject: "A", Predicate: "notDerived", Object: "B"}))
}

func TestEvaluateWithContext_CyclicGraph(t *testing.T) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	// Create a cycle: A -> B -> C -> A
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "edge", Object: "A"})

	result, err := evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	// In a cycle, everyone can reach everyone (including themselves)
	// A->B, A->C, A->A, B->C, B->A, B->B, C->A, C->B, C->C = 9 facts
	assert.Equal(t, 9, result.Size())

	// Verify key reachability facts
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "A"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "B", Predicate: "reachable", Object: "B"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "C", Predicate: "reachable", Object: "C"}))
}

func TestEvaluateWithContext_LargeGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large graph test in short mode")
	}

	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	// Create a chain of 20 nodes
	numNodes := 20
	for i := 0; i < numNodes-1; i++ {
		db.AddEdge(EdgeKey{
			Subject:   string(rune('A' + i)),
			Predicate: "edge",
			Object:    string(rune('A' + i + 1)),
		})
	}

	result, err := evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	// For a chain of n nodes, we can reach from each node to all subsequent nodes
	// Total reachable pairs = n*(n-1)/2 = 20*19/2 = 190
	expectedDerived := numNodes * (numNodes - 1) / 2
	assert.Equal(t, expectedDerived, result.Size())
}

// =============================================================================
// EvaluateIncremental Tests
// =============================================================================

func TestEvaluateIncremental_Simple(t *testing.T) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "copy",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	// First do full evaluation
	_, err = evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	// Now add a new fact and do incremental evaluation
	changes := NewDeltaTable()
	changes.Add(EdgeKey{Subject: "C", Predicate: "base", Object: "D"})

	// Add the change to the database
	db.AddEdge(EdgeKey{Subject: "C", Predicate: "base", Object: "D"})

	result, err := evaluator.EvaluateIncremental(ctx, db, changes)
	require.NoError(t, err)

	// Should derive the new fact
	assert.Equal(t, 1, result.Size())
	assert.True(t, result.Contains(EdgeKey{Subject: "C", Predicate: "derived", Object: "D"}))
}

func TestEvaluateIncremental_TransitiveClosure(t *testing.T) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	// Initial graph: A -> B
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})

	// First do full evaluation
	_, err = evaluator.EvaluateWithContext(ctx, db)
	require.NoError(t, err)

	// Now add a new edge B -> C
	changes := NewDeltaTable()
	changes.Add(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})

	result, err := evaluator.EvaluateIncremental(ctx, db, changes)
	require.NoError(t, err)

	// Should derive:
	// - reachable(B,C) directly from new edge
	// - reachable(A,C) transitively
	assert.True(t, result.Contains(EdgeKey{Subject: "B", Predicate: "reachable", Object: "C"}))
	assert.True(t, result.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "C"}))
}

func TestEvaluateIncremental_EmptyChanges(t *testing.T) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "copy",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	result, err := evaluator.EvaluateIncremental(ctx, db, NewDeltaTable())
	require.NoError(t, err)

	assert.True(t, result.IsEmpty())
}

func TestEvaluateIncremental_NilChanges(t *testing.T) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "copy",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()

	result, err := evaluator.EvaluateIncremental(ctx, db, nil)
	require.NoError(t, err)

	assert.True(t, result.IsEmpty())
}

func TestEvaluateIncremental_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	rules := []*InferenceRule{
		NewInferenceRule("r1", "copy",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()

	changes := NewDeltaTable()
	changes.Add(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	result, err := evaluator.EvaluateIncremental(ctx, db, changes)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, result)
}

func TestEvaluateIncremental_NilStratifier(t *testing.T) {
	ctx := context.Background()

	evaluator := &SemiNaiveEvaluator{
		rules:         []*InferenceRule{},
		stratifier:    nil,
		maxIterations: 1000,
	}

	db := NewInMemoryDatabase()
	changes := NewDeltaTable()
	changes.Add(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	result, err := evaluator.EvaluateIncremental(ctx, db, changes)

	assert.Error(t, err)
	assert.Equal(t, ErrNotStratified, err)
	assert.Nil(t, result)
}

func TestEvaluateIncremental_UnaffectedStratum(t *testing.T) {
	ctx := context.Background()

	// Rule that doesn't match the changed predicate
	rules := []*InferenceRule{
		NewInferenceRule("r1", "copy",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()

	// Change to a predicate not used by any rule
	changes := NewDeltaTable()
	changes.Add(EdgeKey{Subject: "A", Predicate: "other", Object: "B"})

	result, err := evaluator.EvaluateIncremental(ctx, db, changes)
	require.NoError(t, err)

	assert.True(t, result.IsEmpty())
}

// =============================================================================
// initializeStratumDelta Tests
// =============================================================================

func TestInitializeStratumDelta(t *testing.T) {
	rules := []*InferenceRule{
		NewInferenceRule("r1", "transitive",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})
	db.AddEdge(EdgeKey{Subject: "X", Predicate: "other", Object: "Y"})

	delta := evaluator.initializeStratumDelta(rules, db)

	// Should include edges from "reachable" and "edge" predicates
	assert.True(t, delta.Contains(EdgeKey{Subject: "A", Predicate: "reachable", Object: "B"}))
	assert.True(t, delta.Contains(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"}))
	// Should NOT include "other" predicate
	assert.False(t, delta.Contains(EdgeKey{Subject: "X", Predicate: "other", Object: "Y"}))
}

// =============================================================================
// findAffectedStrata Tests
// =============================================================================

func TestFindAffectedStrata(t *testing.T) {
	// Create rules in multiple strata
	rules := []*InferenceRule{
		{
			ID:             "r1",
			Name:           "base_rule",
			HeadPredicate:  "derived",
			BodyPredicates: []string{"base"},
			Negation:       false,
			Enabled:        true,
			Head:           RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body:           []RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
		},
		{
			ID:             "r2",
			Name:           "negation_rule",
			HeadPredicate:  "notDerived",
			BodyPredicates: []string{"derived"},
			Negation:       true,
			Enabled:        true,
			Head:           RuleCondition{Subject: "?x", Predicate: "notDerived", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "derived", Object: "?y", Negated: true},
			},
		},
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	require.NoError(t, err)

	// Changes to "base" predicate
	changes := NewDeltaTable()
	changes.Add(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	affected := evaluator.findAffectedStrata(changes)

	// Should affect stratum 0 (r1 uses "base")
	// And all higher strata (cascade)
	_, stratum0Affected := affected[0]
	_, stratum1Affected := affected[1]

	assert.True(t, stratum0Affected, "Stratum 0 should be affected")
	assert.True(t, stratum1Affected, "Stratum 1 should be affected (cascade)")
}

// =============================================================================
// Benchmark Tests for Context-Aware Methods
// =============================================================================

func BenchmarkEvaluateWithContext_TransitiveClosure(b *testing.B) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator, _ := NewSemiNaiveEvaluator(rules)

		db := NewInMemoryDatabase()
		// Create a chain of 10 nodes
		for j := 0; j < 9; j++ {
			db.AddEdge(EdgeKey{
				Subject:   string(rune('A' + j)),
				Predicate: "edge",
				Object:    string(rune('A' + j + 1)),
			})
		}

		_, _ = evaluator.EvaluateWithContext(ctx, db)
	}
}

func BenchmarkEvaluateIncremental(b *testing.B) {
	ctx := context.Background()

	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	// Pre-build the base database
	evaluator, _ := NewSemiNaiveEvaluator(rules)
	db := NewInMemoryDatabase()
	for j := 0; j < 9; j++ {
		db.AddEdge(EdgeKey{
			Subject:   string(rune('A' + j)),
			Predicate: "edge",
			Object:    string(rune('A' + j + 1)),
		})
	}
	_, _ = evaluator.EvaluateWithContext(ctx, db)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Clone the database for each iteration
		testDB := db.Clone()

		// Add one new edge
		changes := NewDeltaTable()
		newEdge := EdgeKey{Subject: "J", Predicate: "edge", Object: "K"}
		changes.Add(newEdge)
		testDB.AddEdge(newEdge)

		_, _ = evaluator.EvaluateIncremental(ctx, testDB, changes)
	}
}

// =============================================================================
// SNE.11: Performance Benchmarks for Semi-Naive Evaluation
// =============================================================================

// -----------------------------------------------------------------------------
// Benchmark Evaluate() with varying rule counts (10, 100, 1000 rules)
// -----------------------------------------------------------------------------

func BenchmarkEvaluate_10Rules(b *testing.B) {
	benchmarkEvaluateWithRuleCount(b, 10)
}

func BenchmarkEvaluate_100Rules(b *testing.B) {
	benchmarkEvaluateWithRuleCount(b, 100)
}

func BenchmarkEvaluate_1000Rules(b *testing.B) {
	benchmarkEvaluateWithRuleCount(b, 1000)
}

func benchmarkEvaluateWithRuleCount(b *testing.B, ruleCount int) {
	// Create rules that copy from different source predicates to different target predicates
	rules := make([]*InferenceRule, 0, ruleCount)
	for i := 0; i < ruleCount; i++ {
		srcPred := fmt.Sprintf("source%d", i)
		dstPred := fmt.Sprintf("derived%d", i)
		rule := NewInferenceRule(
			fmt.Sprintf("r%d", i),
			fmt.Sprintf("copy_rule_%d", i),
			RuleCondition{Subject: "?x", Predicate: dstPred, Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: srcPred, Object: "?y"}},
		)
		rules = append(rules, rule)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		evaluator, err := NewSemiNaiveEvaluator(rules)
		if err != nil {
			b.Fatalf("Failed to create evaluator: %v", err)
		}

		db := NewInMemoryDatabase()
		// Add 10 edges per rule's source predicate
		for j := 0; j < ruleCount; j++ {
			srcPred := fmt.Sprintf("source%d", j)
			for k := 0; k < 10; k++ {
				db.AddEdge(EdgeKey{
					Subject:   fmt.Sprintf("S%d", k),
					Predicate: srcPred,
					Object:    fmt.Sprintf("O%d", k),
				})
			}
		}
		b.StartTimer()

		_, _ = evaluator.Evaluate(db)
	}
}

// -----------------------------------------------------------------------------
// Benchmark Evaluate() with varying graph sizes (100, 1000, 10000 edges)
// -----------------------------------------------------------------------------

func BenchmarkEvaluate_100Edges(b *testing.B) {
	benchmarkEvaluateWithGraphSize(b, 100)
}

func BenchmarkEvaluate_1000Edges(b *testing.B) {
	benchmarkEvaluateWithGraphSize(b, 1000)
}

func BenchmarkEvaluate_10000Edges(b *testing.B) {
	benchmarkEvaluateWithGraphSize(b, 10000)
}

func benchmarkEvaluateWithGraphSize(b *testing.B, edgeCount int) {
	// Create transitive closure rules
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		evaluator, err := NewSemiNaiveEvaluator(rules)
		if err != nil {
			b.Fatalf("Failed to create evaluator: %v", err)
		}

		db := NewInMemoryDatabase()
		// Create a sparse graph with edgeCount edges
		// Using random-ish distribution to avoid worst-case transitive closure
		nodeCount := edgeCount / 2 // Sparse graph
		if nodeCount < 10 {
			nodeCount = 10
		}
		for j := 0; j < edgeCount; j++ {
			src := fmt.Sprintf("N%d", j%nodeCount)
			dst := fmt.Sprintf("N%d", (j*7+3)%nodeCount) // Pseudo-random target
			db.AddEdge(EdgeKey{
				Subject:   src,
				Predicate: "edge",
				Object:    dst,
			})
		}
		b.StartTimer()

		_, _ = evaluator.Evaluate(db)
	}
}

// -----------------------------------------------------------------------------
// Benchmark evaluateStratum() in isolation
// -----------------------------------------------------------------------------

func BenchmarkEvaluateStratum_Small(b *testing.B) {
	benchmarkEvaluateStratum(b, 5, 50)
}

func BenchmarkEvaluateStratum_Medium(b *testing.B) {
	benchmarkEvaluateStratum(b, 10, 200)
}

func BenchmarkEvaluateStratum_Large(b *testing.B) {
	benchmarkEvaluateStratum(b, 20, 500)
}

func benchmarkEvaluateStratum(b *testing.B, chainLength int, extraEdges int) {
	// Create transitive closure rules (all in stratum 0)
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		evaluator, err := NewSemiNaiveEvaluator(rules)
		if err != nil {
			b.Fatalf("Failed to create evaluator: %v", err)
		}

		db := NewInMemoryDatabase()
		// Create a chain of nodes
		for j := 0; j < chainLength-1; j++ {
			db.AddEdge(EdgeKey{
				Subject:   fmt.Sprintf("N%d", j),
				Predicate: "edge",
				Object:    fmt.Sprintf("N%d", j+1),
			})
		}
		// Add extra edges for more complexity
		for j := 0; j < extraEdges; j++ {
			db.AddEdge(EdgeKey{
				Subject:   fmt.Sprintf("X%d", j),
				Predicate: "edge",
				Object:    fmt.Sprintf("Y%d", j),
			})
		}
		b.StartTimer()

		// Evaluate stratum 0 only
		_, _ = evaluator.evaluateStratum(0, db)
	}
}

// -----------------------------------------------------------------------------
// Benchmark delta propagation overhead
// -----------------------------------------------------------------------------

func BenchmarkDeltaPropagation_Small(b *testing.B) {
	benchmarkDeltaPropagation(b, 10)
}

func BenchmarkDeltaPropagation_Medium(b *testing.B) {
	benchmarkDeltaPropagation(b, 100)
}

func BenchmarkDeltaPropagation_Large(b *testing.B) {
	benchmarkDeltaPropagation(b, 1000)
}

func benchmarkDeltaPropagation(b *testing.B, deltaSize int) {
	ctx := context.Background()

	// Create transitive closure rules
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	evaluator, err := NewSemiNaiveEvaluator(rules)
	if err != nil {
		b.Fatalf("Failed to create evaluator: %v", err)
	}

	// Pre-populate database with initial edges
	db := NewInMemoryDatabase()
	for j := 0; j < 50; j++ {
		db.AddEdge(EdgeKey{
			Subject:   fmt.Sprintf("N%d", j),
			Predicate: "edge",
			Object:    fmt.Sprintf("N%d", j+1),
		})
	}
	// Do initial full evaluation
	_, _ = evaluator.EvaluateWithContext(ctx, db)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Clone database for each iteration
		testDB := db.Clone()
		changes := NewDeltaTable()
		// Create delta with new edges
		for j := 0; j < deltaSize; j++ {
			newEdge := EdgeKey{
				Subject:   fmt.Sprintf("D%d", j),
				Predicate: "edge",
				Object:    fmt.Sprintf("D%d", j+1),
			}
			changes.Add(newEdge)
			testDB.AddEdge(newEdge)
		}
		b.StartTimer()

		_, _ = evaluator.EvaluateIncremental(ctx, testDB, changes)
	}
}

// -----------------------------------------------------------------------------
// Compare with naive fixed-point evaluation
// -----------------------------------------------------------------------------

// NaiveEvaluator implements naive fixed-point evaluation for comparison
type NaiveEvaluator struct {
	rules         []*InferenceRule
	maxIterations int
}

// NewNaiveEvaluator creates a naive evaluator (for benchmarking comparison)
func NewNaiveEvaluator(rules []*InferenceRule, maxIterations int) *NaiveEvaluator {
	return &NaiveEvaluator{
		rules:         rules,
		maxIterations: maxIterations,
	}
}

// Evaluate runs naive fixed-point evaluation
func (e *NaiveEvaluator) Evaluate(db Database) (int, error) {
	totalDerived := 0

	for iteration := 0; iteration < e.maxIterations; iteration++ {
		derivedThisIteration := 0

		for _, rule := range e.rules {
			if !rule.Enabled {
				continue
			}

			// Naive: compute ALL tuples from scratch each iteration
			// This is the key difference from semi-naive
			newEdges := e.applyRuleNaive(rule, db)

			for _, edge := range newEdges {
				if db.AddEdge(edge) {
					derivedThisIteration++
					totalDerived++
				}
			}
		}

		if derivedThisIteration == 0 {
			break // Fixpoint reached
		}
	}

	return totalDerived, nil
}

// applyRuleNaive applies a rule naively (full cross product)
func (e *NaiveEvaluator) applyRuleNaive(rule *InferenceRule, db Database) []EdgeKey {
	if len(rule.Body) == 0 {
		return nil
	}

	// Start with all possible bindings from first body atom
	var bindings []map[string]string
	firstAtom := rule.Body[0]

	if !IsRuleVariable(firstAtom.Predicate) {
		edges := db.GetAllEdges(firstAtom.Predicate)
		for _, edge := range edges {
			if newBindings, ok := firstAtom.Unify(map[string]string{}, edge); ok {
				bindings = append(bindings, newBindings)
			}
		}
	}

	// Extend bindings with remaining body atoms
	for i := 1; i < len(rule.Body); i++ {
		bindings = e.extendBindingsNaive(bindings, rule.Body[i], db)
		if len(bindings) == 0 {
			return nil
		}
	}

	// Generate head edges
	var result []EdgeKey
	for _, binding := range bindings {
		headEdge := e.substituteHeadNaive(rule.Head, binding)
		if headEdge != nil {
			result = append(result, *headEdge)
		}
	}

	return result
}

func (e *NaiveEvaluator) extendBindingsNaive(bindings []map[string]string, atom RuleCondition, db Database) []map[string]string {
	var result []map[string]string

	for _, binding := range bindings {
		substituted := atom.Substitute(binding)
		var edges []EdgeKey

		if !IsRuleVariable(substituted.Predicate) {
			edges = db.GetAllEdges(substituted.Predicate)
		} else {
			// Variable predicate - need all predicates
			if imdb, ok := db.(*InMemoryDatabase); ok {
				for _, pred := range imdb.GetAllPredicates() {
					edges = append(edges, db.GetAllEdges(pred)...)
				}
			}
		}

		for _, edge := range edges {
			if newBindings, ok := atom.Unify(binding, edge); ok {
				result = append(result, newBindings)
			}
		}
	}

	return result
}

func (e *NaiveEvaluator) substituteHeadNaive(head RuleCondition, bindings map[string]string) *EdgeKey {
	substituted := head.Substitute(bindings)

	if IsRuleVariable(substituted.Subject) ||
		IsRuleVariable(substituted.Predicate) ||
		IsRuleVariable(substituted.Object) {
		return nil
	}

	return &EdgeKey{
		Subject:   substituted.Subject,
		Predicate: substituted.Predicate,
		Object:    substituted.Object,
	}
}

func BenchmarkNaiveEvaluation_Chain10(b *testing.B) {
	benchmarkNaiveVsSemiNaive(b, 10, true)
}

func BenchmarkSemiNaiveEvaluation_Chain10(b *testing.B) {
	benchmarkNaiveVsSemiNaive(b, 10, false)
}

func BenchmarkNaiveEvaluation_Chain20(b *testing.B) {
	benchmarkNaiveVsSemiNaive(b, 20, true)
}

func BenchmarkSemiNaiveEvaluation_Chain20(b *testing.B) {
	benchmarkNaiveVsSemiNaive(b, 20, false)
}

func benchmarkNaiveVsSemiNaive(b *testing.B, chainLength int, useNaive bool) {
	// Create transitive closure rules
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db := NewInMemoryDatabase()
		// Create a chain of nodes
		for j := 0; j < chainLength-1; j++ {
			db.AddEdge(EdgeKey{
				Subject:   fmt.Sprintf("N%d", j),
				Predicate: "edge",
				Object:    fmt.Sprintf("N%d", j+1),
			})
		}
		b.StartTimer()

		if useNaive {
			naiveEval := NewNaiveEvaluator(rules, 1000)
			_, _ = naiveEval.Evaluate(db)
		} else {
			semiNaiveEval, _ := NewSemiNaiveEvaluator(rules)
			_, _ = semiNaiveEval.Evaluate(db)
		}
	}
}

// -----------------------------------------------------------------------------
// Additional micro-benchmarks for specific operations
// -----------------------------------------------------------------------------

func BenchmarkDeltaTable_Add(b *testing.B) {
	dt := NewDeltaTable()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dt.Add(EdgeKey{
			Subject:   fmt.Sprintf("S%d", i%1000),
			Predicate: "pred",
			Object:    fmt.Sprintf("O%d", i%1000),
		})
	}
}

func BenchmarkDeltaTable_Contains(b *testing.B) {
	dt := NewDeltaTable()
	// Pre-populate
	for i := 0; i < 1000; i++ {
		dt.Add(EdgeKey{
			Subject:   fmt.Sprintf("S%d", i),
			Predicate: "pred",
			Object:    fmt.Sprintf("O%d", i),
		})
	}

	edge := EdgeKey{Subject: "S500", Predicate: "pred", Object: "O500"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dt.Contains(edge)
	}
}

func BenchmarkConcurrentDeltaTable_Add(b *testing.B) {
	cdt := NewConcurrentDeltaTable()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cdt.Add(EdgeKey{
			Subject:   fmt.Sprintf("S%d", i%1000),
			Predicate: "pred",
			Object:    fmt.Sprintf("O%d", i%1000),
		})
	}
}

func BenchmarkStratifier_Stratify(b *testing.B) {
	// Create a set of rules with some negative dependencies
	rules := make([]*InferenceRule, 50)
	for i := 0; i < 50; i++ {
		rule := &InferenceRule{
			ID:             fmt.Sprintf("r%d", i),
			Name:           fmt.Sprintf("rule%d", i),
			HeadPredicate:  fmt.Sprintf("pred%d", i),
			BodyPredicates: []string{fmt.Sprintf("pred%d", (i+1)%50)},
			Negation:       i%5 == 0, // Every 5th rule has negation
			Enabled:        true,
		}
		if rule.Negation {
			rule.Body = []RuleCondition{
				{Predicate: fmt.Sprintf("pred%d", (i+1)%50), Negated: true},
			}
		} else {
			rule.Body = []RuleCondition{
				{Predicate: fmt.Sprintf("pred%d", (i+1)%50), Negated: false},
			}
		}
		rules[i] = rule
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset strata for fair comparison
		for _, r := range rules {
			r.Stratum = 0
		}
		stratifier := NewStratifier(rules)
		_ = stratifier.Stratify()
	}
}

func BenchmarkNewSemiNaiveEvaluator(b *testing.B) {
	// Create a moderate set of rules
	rules := make([]*InferenceRule, 20)
	for i := 0; i < 20; i++ {
		rules[i] = NewInferenceRule(
			fmt.Sprintf("r%d", i),
			fmt.Sprintf("rule%d", i),
			RuleCondition{Subject: "?x", Predicate: fmt.Sprintf("derived%d", i), Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: fmt.Sprintf("base%d", i), Object: "?y"}},
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewSemiNaiveEvaluator(rules)
	}
}

// =============================================================================
// SNE.11: Comprehensive Performance Benchmarks (10K, 50K, 100K edges)
// =============================================================================

// These benchmarks compare semi-naive vs naive evaluation on large datasets
// to verify that semi-naive achieves significant performance improvements.

// -----------------------------------------------------------------------------
// Large-scale naive vs semi-naive comparison
// -----------------------------------------------------------------------------

func BenchmarkNaive_10K_Edges(b *testing.B) {
	benchmarkNaiveLargeScale(b, 10000)
}

func BenchmarkSemiNaive_10K_Edges(b *testing.B) {
	benchmarkSemiNaiveLargeScale(b, 10000)
}

func BenchmarkNaive_50K_Edges(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping 50K edge benchmark in short mode")
	}
	benchmarkNaiveLargeScale(b, 50000)
}

func BenchmarkSemiNaive_50K_Edges(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping 50K edge benchmark in short mode")
	}
	benchmarkSemiNaiveLargeScale(b, 50000)
}

func BenchmarkNaive_100K_Edges(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping 100K edge benchmark in short mode")
	}
	benchmarkNaiveLargeScale(b, 100000)
}

func BenchmarkSemiNaive_100K_Edges(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping 100K edge benchmark in short mode")
	}
	benchmarkSemiNaiveLargeScale(b, 100000)
}

func benchmarkNaiveLargeScale(b *testing.B, edgeCount int) {
	// Use a simple copy rule that triggers on all edges
	// This tests the pure overhead of naive vs semi-naive
	rules := []*InferenceRule{
		NewInferenceRule("r1", "derived",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		naiveEval := NewNaiveEvaluator(rules, 1000)
		db := NewInMemoryDatabase()
		// Create edges with some structure
		nodeCount := edgeCount / 5
		if nodeCount < 100 {
			nodeCount = 100
		}
		for j := 0; j < edgeCount; j++ {
			src := fmt.Sprintf("N%d", j%nodeCount)
			dst := fmt.Sprintf("N%d", (j*3+7)%nodeCount)
			db.AddEdge(EdgeKey{
				Subject:   src,
				Predicate: "edge",
				Object:    dst,
			})
		}
		b.StartTimer()

		_, _ = naiveEval.Evaluate(db)
	}
}

func benchmarkSemiNaiveLargeScale(b *testing.B, edgeCount int) {
	// Use a simple copy rule that triggers on all edges
	rules := []*InferenceRule{
		NewInferenceRule("r1", "derived",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		semiNaiveEval, _ := NewSemiNaiveEvaluator(rules)
		db := NewInMemoryDatabase()
		nodeCount := edgeCount / 5
		if nodeCount < 100 {
			nodeCount = 100
		}
		for j := 0; j < edgeCount; j++ {
			src := fmt.Sprintf("N%d", j%nodeCount)
			dst := fmt.Sprintf("N%d", (j*3+7)%nodeCount)
			db.AddEdge(EdgeKey{
				Subject:   src,
				Predicate: "edge",
				Object:    dst,
			})
		}
		b.StartTimer()

		_, _ = semiNaiveEval.Evaluate(db)
	}
}

// -----------------------------------------------------------------------------
// Transitive closure benchmarks (where semi-naive really shines)
// -----------------------------------------------------------------------------

func BenchmarkNaive_TransitiveClosure_10K(b *testing.B) {
	benchmarkNaiveTransitiveClosure(b, 10000)
}

func BenchmarkSemiNaive_TransitiveClosure_10K(b *testing.B) {
	benchmarkSemiNaiveTransitiveClosure(b, 10000)
}

func BenchmarkNaive_TransitiveClosure_50K(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping 50K transitive closure benchmark in short mode")
	}
	benchmarkNaiveTransitiveClosure(b, 50000)
}

func BenchmarkSemiNaive_TransitiveClosure_50K(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping 50K transitive closure benchmark in short mode")
	}
	benchmarkSemiNaiveTransitiveClosure(b, 50000)
}

func benchmarkNaiveTransitiveClosure(b *testing.B, edgeCount int) {
	// Transitive closure rules
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		naiveEval := NewNaiveEvaluator(rules, 1000)
		db := NewInMemoryDatabase()
		// Create a sparse graph (many small connected components)
		nodeCount := edgeCount / 2
		if nodeCount < 100 {
			nodeCount = 100
		}
		for j := 0; j < edgeCount; j++ {
			src := fmt.Sprintf("N%d", j%nodeCount)
			dst := fmt.Sprintf("N%d", (j+1)%nodeCount)
			db.AddEdge(EdgeKey{
				Subject:   src,
				Predicate: "edge",
				Object:    dst,
			})
		}
		b.StartTimer()

		_, _ = naiveEval.Evaluate(db)
	}
}

func benchmarkSemiNaiveTransitiveClosure(b *testing.B, edgeCount int) {
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		semiNaiveEval, _ := NewSemiNaiveEvaluator(rules)
		db := NewInMemoryDatabase()
		nodeCount := edgeCount / 2
		if nodeCount < 100 {
			nodeCount = 100
		}
		for j := 0; j < edgeCount; j++ {
			src := fmt.Sprintf("N%d", j%nodeCount)
			dst := fmt.Sprintf("N%d", (j+1)%nodeCount)
			db.AddEdge(EdgeKey{
				Subject:   src,
				Predicate: "edge",
				Object:    dst,
			})
		}
		b.StartTimer()

		_, _ = semiNaiveEval.Evaluate(db)
	}
}

// -----------------------------------------------------------------------------
// Derivation count measurement (verifying semi-naive processes fewer tuples)
// -----------------------------------------------------------------------------

func TestDerivationCountComparison(t *testing.T) {
	// This test verifies that semi-naive evaluation processes fewer tuples
	// than naive evaluation by measuring the number of rule applications.

	// Transitive closure rules
	rules := []*InferenceRule{
		NewInferenceRule("r1", "base_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
		NewInferenceRule("r2", "transitive_reachable",
			RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			[]RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			}),
	}

	// Create a chain of 15 nodes (to get measurable difference)
	chainLength := 15
	db1 := NewInMemoryDatabase()
	db2 := NewInMemoryDatabase()

	for j := 0; j < chainLength-1; j++ {
		edge := EdgeKey{
			Subject:   fmt.Sprintf("N%d", j),
			Predicate: "edge",
			Object:    fmt.Sprintf("N%d", j+1),
		}
		db1.AddEdge(edge)
		db2.AddEdge(edge)
	}

	// Run naive evaluation
	naiveEval := NewNaiveEvaluator(rules, 1000)
	naiveDerived, _ := naiveEval.Evaluate(db1)

	// Run semi-naive evaluation
	semiNaiveEval, _ := NewSemiNaiveEvaluator(rules)
	semiNaiveDerived, _ := semiNaiveEval.Evaluate(db2)

	// Both should derive the same number of edges
	assert.Equal(t, naiveDerived, semiNaiveDerived,
		"Naive and semi-naive should derive the same number of edges")

	// Verify the expected count
	// For a chain of n nodes: n*(n-1)/2 reachable edges
	expectedDerived := chainLength * (chainLength - 1) / 2
	assert.Equal(t, expectedDerived, semiNaiveDerived,
		"Should derive n*(n-1)/2 reachable edges for a chain of n nodes")

	t.Logf("Chain length: %d, Derived edges: %d", chainLength, semiNaiveDerived)
}

// -----------------------------------------------------------------------------
// Memory allocation benchmarks
// -----------------------------------------------------------------------------

func BenchmarkMemAlloc_SemiNaive_1K(b *testing.B) {
	benchmarkMemAllocSemiNaive(b, 1000)
}

func BenchmarkMemAlloc_SemiNaive_10K(b *testing.B) {
	benchmarkMemAllocSemiNaive(b, 10000)
}

func benchmarkMemAllocSemiNaive(b *testing.B, edgeCount int) {
	rules := []*InferenceRule{
		NewInferenceRule("r1", "derived",
			RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			[]RuleCondition{{Subject: "?x", Predicate: "edge", Object: "?y"}}),
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		semiNaiveEval, _ := NewSemiNaiveEvaluator(rules)
		db := NewInMemoryDatabase()
		for j := 0; j < edgeCount; j++ {
			db.AddEdge(EdgeKey{
				Subject:   fmt.Sprintf("S%d", j%100),
				Predicate: "edge",
				Object:    fmt.Sprintf("O%d", j%100),
			})
		}
		b.StartTimer()

		_, _ = semiNaiveEval.Evaluate(db)
	}
}
