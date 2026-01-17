package treesitter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewParserPool(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	require.NotNil(t, pool)
	assert.NotNil(t, pool.parsers)
	assert.Equal(t, registry, pool.registry)
	assert.Equal(t, 4, pool.maxIdle)
}

func TestNewParserPoolWithConfig(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	cfg := ParserPoolConfig{MaxIdleParsersPerLanguage: 10}
	pool := NewParserPoolWithConfig(registry, cfg)
	require.NotNil(t, pool)
	assert.Equal(t, 10, pool.maxIdle)
}

func TestParserPoolGetUnknownLanguage(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	_, err := pool.Get("unknown_language")
	assert.Error(t, err)
}

func TestParserPoolPutNil(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	pool.Put(nil)
}

func TestParserPoolClose(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	err := pool.Close()
	assert.NoError(t, err)
	assert.Empty(t, pool.parsers)
}

func TestParserPoolStats(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	stats := pool.Stats()
	assert.NotNil(t, stats)
	assert.Empty(t, stats)
}

func TestParserPoolStatsFields(t *testing.T) {
	stats := ParserPoolStats{Idle: 3, Active: 2}
	assert.Equal(t, 3, stats.Idle)
	assert.Equal(t, 2, stats.Active)
}

func TestParserPoolGetOrCreateEntry(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	pool.mu.Lock()
	entry1 := pool.getOrCreateEntry("go")
	entry2 := pool.getOrCreateEntry("go")
	pool.mu.Unlock()

	assert.Same(t, entry1, entry2)
}

func TestParserPoolPopIdle(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	entry := &parserPoolEntry{idle: make([]*Parser, 0)}
	result := pool.popIdle(entry)
	assert.Nil(t, result)
}

func TestParserPoolEntryZeroValue(t *testing.T) {
	var entry parserPoolEntry
	assert.Nil(t, entry.idle)
	assert.Equal(t, 0, entry.active)
	assert.Nil(t, entry.language)
}

func TestNewIncrementalParser(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	require.NotNil(t, ip)
	assert.Equal(t, pool, ip.pool)
	assert.NotNil(t, ip.trees)
	assert.Equal(t, 100, ip.maxCached)
}

func TestNewIncrementalParserWithConfig(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	cfg := IncrementalParserConfig{MaxCachedTrees: 50}
	ip := NewIncrementalParserWithConfig(pool, cfg)

	require.NotNil(t, ip)
	assert.Equal(t, 50, ip.maxCached)
}

func TestIncrementalParserParseUnknownExtension(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	_, err := ip.Parse(context.Background(), "test.unknown", []byte("content"))
	assert.Error(t, err)
}

func TestIncrementalParserInvalidate(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.trees["test.go"] = &cachedTree{
		tree:     &Tree{ptr: 0},
		language: "go",
		parsedAt: time.Now(),
	}

	ip.Invalidate("test.go")
	assert.NotContains(t, ip.trees, "test.go")
}

func TestIncrementalParserInvalidateNonexistent(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.Invalidate("nonexistent.go")
}

func TestIncrementalParserInvalidateAll(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.trees["test1.go"] = &cachedTree{tree: &Tree{ptr: 0}}
	ip.trees["test2.go"] = &cachedTree{tree: &Tree{ptr: 0}}

	ip.InvalidateAll()
	assert.Empty(t, ip.trees)
}

func TestIncrementalParserClose(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.trees["test.go"] = &cachedTree{tree: &Tree{ptr: 0}}

	err := ip.Close()
	assert.NoError(t, err)
	assert.Empty(t, ip.trees)
}

func TestIncrementalParserCachedCount(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	assert.Equal(t, 0, ip.CachedCount())

	ip.trees["test.go"] = &cachedTree{}
	assert.Equal(t, 1, ip.CachedCount())

	ip.trees["test2.go"] = &cachedTree{}
	assert.Equal(t, 2, ip.CachedCount())
}

func TestHashContent(t *testing.T) {
	t.Run("empty content", func(t *testing.T) {
		hash := hashContent([]byte{})
		assert.NotEqual(t, uint64(0), hash)
	})

	t.Run("same content same hash", func(t *testing.T) {
		content := []byte("hello world")
		hash1 := hashContent(content)
		hash2 := hashContent(content)
		assert.Equal(t, hash1, hash2)
	})

	t.Run("different content different hash", func(t *testing.T) {
		hash1 := hashContent([]byte("hello"))
		hash2 := hashContent([]byte("world"))
		assert.NotEqual(t, hash1, hash2)
	})
}

func TestCreateInputEdit(t *testing.T) {
	edit := CreateInputEdit(10, 20, 25, 0, 10, 0, 20, 0, 25)

	assert.Equal(t, uint32(10), edit.StartByte)
	assert.Equal(t, uint32(20), edit.OldEndByte)
	assert.Equal(t, uint32(25), edit.NewEndByte)
	assert.Equal(t, uint32(0), edit.StartPoint.Row)
	assert.Equal(t, uint32(10), edit.StartPoint.Column)
	assert.Equal(t, uint32(0), edit.OldEndPoint.Row)
	assert.Equal(t, uint32(20), edit.OldEndPoint.Column)
	assert.Equal(t, uint32(0), edit.NewEndPoint.Row)
	assert.Equal(t, uint32(25), edit.NewEndPoint.Column)
}

func TestCachedTreeFields(t *testing.T) {
	now := time.Now()
	cached := &cachedTree{
		tree:       &Tree{ptr: 123},
		language:   "go",
		parsedAt:   now,
		sourceHash: 12345,
	}

	assert.Equal(t, TSTree(123), cached.tree.ptr)
	assert.Equal(t, "go", cached.language)
	assert.Equal(t, now, cached.parsedAt)
	assert.Equal(t, uint64(12345), cached.sourceHash)
}

func TestIncrementalParserEvictOldest(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	now := time.Now()
	ip.trees["old.go"] = &cachedTree{
		tree:     &Tree{ptr: 0},
		parsedAt: now.Add(-time.Hour),
	}
	ip.trees["new.go"] = &cachedTree{
		tree:     &Tree{ptr: 0},
		parsedAt: now,
	}

	ip.mu.Lock()
	ip.evictOldest()
	ip.mu.Unlock()

	assert.NotContains(t, ip.trees, "old.go")
	assert.Contains(t, ip.trees, "new.go")
}

func TestIncrementalParserEvictOldestEmpty(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.mu.Lock()
	ip.evictOldest()
	ip.mu.Unlock()
}

func TestIncrementalParserFindOldestEntry(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	t.Run("empty returns empty string", func(t *testing.T) {
		path := ip.findOldestEntry()
		assert.Empty(t, path)
	})

	t.Run("finds oldest", func(t *testing.T) {
		now := time.Now()
		ip.trees["oldest.go"] = &cachedTree{parsedAt: now.Add(-2 * time.Hour)}
		ip.trees["middle.go"] = &cachedTree{parsedAt: now.Add(-time.Hour)}
		ip.trees["newest.go"] = &cachedTree{parsedAt: now}

		path := ip.findOldestEntry()
		assert.Equal(t, "oldest.go", path)
	})
}

func TestIncrementalParserRemoveEntry(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.trees["test.go"] = &cachedTree{tree: &Tree{ptr: 0}}

	ip.removeEntry("test.go")
	assert.NotContains(t, ip.trees, "test.go")
}

func TestIncrementalParserRemoveEntryNilTree(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	ip.trees["test.go"] = &cachedTree{tree: nil}

	ip.removeEntry("test.go")
	assert.NotContains(t, ip.trees, "test.go")
}

func TestIncrementalParserCacheTree(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	cfg := IncrementalParserConfig{MaxCachedTrees: 2}
	ip := NewIncrementalParserWithConfig(pool, cfg)

	ip.cacheTree("file1.go", &Tree{ptr: 0}, "go", []byte("content1"))
	assert.Equal(t, 1, len(ip.trees))

	ip.cacheTree("file2.go", &Tree{ptr: 0}, "go", []byte("content2"))
	assert.Equal(t, 2, len(ip.trees))

	ip.cacheTree("file3.go", &Tree{ptr: 0}, "go", []byte("content3"))
	assert.Equal(t, 2, len(ip.trees))
}

func TestParserPoolThreadSafety(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	defer pool.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = pool.Get("unknown")
			pool.Stats()
		}()
	}
	wg.Wait()
}

func TestIncrementalParserThreadSafety(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)
	defer ip.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip.CachedCount()
			ip.Invalidate("test.go")
		}()
	}
	wg.Wait()
}

func TestIncrementalParserParseIncrementalNoCached(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	edit := CreateInputEdit(0, 0, 5, 0, 0, 0, 0, 0, 5)
	_, err := ip.ParseIncremental(context.Background(), "test.go", []byte("hello"), edit)
	assert.Error(t, err)
}

func TestIncrementalParserParseIncrementalUnknownExt(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)
	ip := NewIncrementalParser(pool)

	edit := CreateInputEdit(0, 0, 5, 0, 0, 0, 0, 0, 5)
	_, err := ip.ParseIncremental(context.Background(), "test.unknown", []byte("hello"), edit)
	assert.Error(t, err)
}

func TestParserPoolPutParserNoLanguage(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	parser := &Parser{ptr: 0, language: nil}
	pool.Put(parser)
}

func TestParserPoolPutParserUnknownLanguage(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	pool := NewParserPool(registry)

	lang := &Language{name: "unknown_lang"}
	parser := &Parser{ptr: 0, language: lang}
	pool.Put(parser)
}

func BenchmarkHashContent(b *testing.B) {
	content := []byte("func main() { fmt.Println(\"hello\") }")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hashContent(content)
	}
}

func BenchmarkHashContentLarge(b *testing.B) {
	content := make([]byte, 10000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hashContent(content)
	}
}

func BenchmarkCreateInputEdit(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CreateInputEdit(10, 20, 25, 0, 10, 0, 20, 0, 25)
	}
}
