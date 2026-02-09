package sylkdir

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func BenchmarkCanonicalKeyIndexSet(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(key, uint32(i))
	}
}

func BenchmarkCanonicalKeyIndexLookup(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Pre-populate
	numKeys := 100000
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(keys[i], uint32(i))
	}

	// Pre-generate random indices
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	lookupIndices := make([]int, b.N)
	for i := range lookupIndices {
		lookupIndices[i] = rng.Intn(numKeys)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = idx.Lookup(keys[lookupIndices[i]])
	}
}

func BenchmarkCanonicalKeyIndexHas(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Pre-populate
	numKeys := 100000
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(keys[i], uint32(i))
	}

	// Pre-generate random indices
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	lookupIndices := make([]int, b.N)
	for i := range lookupIndices {
		lookupIndices[i] = rng.Intn(numKeys)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = idx.Has(keys[lookupIndices[i]])
	}
}

func BenchmarkCanonicalKeyIndexSave(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Pre-populate with 10K keys
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(key, uint32(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Save()
	}
}

func BenchmarkCanonicalKeyIndexLoad(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Pre-populate and save
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(key, uint32(i))
	}
	idx.Save()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newIdx := NewCanonicalKeyIndexFromSylkDir(sd)
		newIdx.Init()
	}
}

func BenchmarkCanonicalKeyIndexSetIfNotExists(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Pre-populate half the keys
	numKeys := 100000
	for i := 0; i < numKeys/2; i++ {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(key, uint32(i))
	}

	// Benchmark will test both existing and new keys
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	lookupIndices := make([]int, b.N)
	for i := range lookupIndices {
		lookupIndices[i] = rng.Intn(numKeys)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", lookupIndices[i], lookupIndices[i])
		_, _ = idx.SetIfNotExists(key, uint32(lookupIndices[i]))
	}
}

func BenchmarkCanonicalKeyIndexLookupPrefix(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Pre-populate with keys of different prefixes
	for i := 0; i < 10000; i++ {
		idx.Set(fmt.Sprintf("repo:file%d.go:Func:func", i), uint32(i))
	}
	for i := 0; i < 1000; i++ {
		idx.Set(fmt.Sprintf("doi:10.%d/paper", i), uint32(10000+i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = idx.LookupPrefix("repo:")
	}
}
