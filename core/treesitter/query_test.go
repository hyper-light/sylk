package treesitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewQueryEngine(t *testing.T) {
	engine := NewQueryEngine(100)
	require.NotNil(t, engine)
	assert.Equal(t, 0, engine.CacheSize())
}

func TestQueryEngineClose(t *testing.T) {
	engine := NewQueryEngine(10)
	engine.Close()
	assert.Equal(t, 0, engine.CacheSize())
}

func TestQueryBuilder(t *testing.T) {
	t.Run("empty builder", func(t *testing.T) {
		b := NewQueryBuilder()
		assert.Equal(t, "", b.Build())
	})

	t.Run("single pattern", func(t *testing.T) {
		b := NewQueryBuilder().Add("(function_declaration)")
		assert.Equal(t, "(function_declaration)", b.Build())
	})

	t.Run("multiple patterns", func(t *testing.T) {
		b := NewQueryBuilder().
			Add("(function_declaration)").
			Add("(method_declaration)")
		result := b.Build()
		assert.Contains(t, result, "(function_declaration)")
		assert.Contains(t, result, "(method_declaration)")
	})
}

func TestQueryCacheKey(t *testing.T) {
	key1 := queryCacheKey{language: "go", pattern: "(func)"}
	key2 := queryCacheKey{language: "go", pattern: "(func)"}
	key3 := queryCacheKey{language: "rust", pattern: "(func)"}

	assert.Equal(t, key1, key2)
	assert.NotEqual(t, key1, key3)
}

func TestQueryResultZeroValue(t *testing.T) {
	var result QueryResult
	assert.Nil(t, result.Matches)
	assert.Equal(t, uint32(0), result.PatternCount)
	assert.Equal(t, uint32(0), result.CaptureCount)
}

func TestGetCommonQuery(t *testing.T) {
	t.Run("go functions", func(t *testing.T) {
		query, ok := GetCommonQuery("go", "functions")
		assert.True(t, ok)
		assert.Contains(t, query, "function_declaration")
	})

	t.Run("go methods", func(t *testing.T) {
		query, ok := GetCommonQuery("go", "methods")
		assert.True(t, ok)
		assert.Contains(t, query, "method_declaration")
	})

	t.Run("python functions", func(t *testing.T) {
		query, ok := GetCommonQuery("python", "functions")
		assert.True(t, ok)
		assert.Contains(t, query, "function_definition")
	})

	t.Run("unknown language", func(t *testing.T) {
		_, ok := GetCommonQuery("unknown", "functions")
		assert.False(t, ok)
	})

	t.Run("unknown query type", func(t *testing.T) {
		_, ok := GetCommonQuery("go", "unknown")
		assert.False(t, ok)
	})
}

func TestListCommonQueries(t *testing.T) {
	t.Run("go queries", func(t *testing.T) {
		names := ListCommonQueries("go")
		assert.Contains(t, names, "functions")
		assert.Contains(t, names, "methods")
		assert.Contains(t, names, "types")
	})

	t.Run("rust queries", func(t *testing.T) {
		names := ListCommonQueries("rust")
		assert.Contains(t, names, "functions")
		assert.Contains(t, names, "structs")
		assert.Contains(t, names, "traits")
	})

	t.Run("unknown language", func(t *testing.T) {
		names := ListCommonQueries("unknown")
		assert.Nil(t, names)
	})
}

func TestCommonQueriesContent(t *testing.T) {
	for lang, queries := range CommonQueries {
		for name, pattern := range queries {
			t.Run(lang+"_"+name, func(t *testing.T) {
				assert.NotEmpty(t, pattern)
				assert.Contains(t, pattern, "(")
				assert.Contains(t, pattern, ")")
			})
		}
	}
}

func TestJoinPatterns(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		result := joinPatterns(nil)
		assert.Equal(t, "", result)
	})

	t.Run("single", func(t *testing.T) {
		result := joinPatterns([]string{"(a)"})
		assert.Equal(t, "(a)", result)
	})

	t.Run("multiple", func(t *testing.T) {
		result := joinPatterns([]string{"(a)", "(b)", "(c)"})
		assert.Equal(t, "(a)\n(b)\n(c)", result)
	})
}

func TestCalculateTotalLength(t *testing.T) {
	assert.Equal(t, 0, calculateTotalLength(nil))
	assert.Equal(t, 2, calculateTotalLength([]string{"a"}))
	assert.Equal(t, 4, calculateTotalLength([]string{"a", "b"}))
}

func TestExtractQueryNames(t *testing.T) {
	queries := map[string]string{
		"a": "pattern_a",
		"b": "pattern_b",
	}
	names := extractQueryNames(queries)
	assert.Len(t, names, 2)
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
}

func TestQueryPatternMethod(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	lang := &Language{name: "go"}
	pattern := "(function_declaration)"
	query := &Query{pattern: pattern, language: lang}

	assert.Equal(t, pattern, query.Pattern())
}

func TestQueryEngineEviction(t *testing.T) {
	engine := NewQueryEngine(2)
	defer engine.Close()

	engine.queryCache[queryCacheKey{language: "a", pattern: "1"}] = &Query{}
	engine.queryCache[queryCacheKey{language: "b", pattern: "2"}] = &Query{}

	assert.Equal(t, 2, engine.CacheSize())

	engine.evictIfNeeded()
	assert.Equal(t, 1, engine.CacheSize())
}

func TestCollectMatchesEmpty(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	cursor := &QueryCursor{ptr: 0}
	query := &Query{ptr: 0}
	node := &Node{}

	matches := collectMatches(cursor, query, node)
	assert.Empty(t, matches)
}

func TestExtractCapturesEmpty(t *testing.T) {
	raw := &TSQueryMatch{CaptureCount: 0, Captures: 0}
	query := &Query{}
	node := &Node{}

	captures := extractCaptures(raw, query, node)
	assert.Nil(t, captures)
}

func TestWrapCapturedNode(t *testing.T) {
	source := []byte("test content")
	tree := &Tree{source: source}
	parent := &Node{tree: tree, source: source}

	raw := TSNode{}
	wrapped := wrapCapturedNode(raw, parent)

	assert.Same(t, tree, wrapped.tree)
	assert.Equal(t, source, wrapped.source)
}

func BenchmarkQueryBuilder(b *testing.B) {
	patterns := []string{
		"(function_declaration)",
		"(method_declaration)",
		"(type_declaration)",
		"(import_declaration)",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		builder := NewQueryBuilder()
		for _, p := range patterns {
			builder.Add(p)
		}
		_ = builder.Build()
	}
}

func BenchmarkGetCommonQuery(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetCommonQuery("go", "functions")
	}
}
