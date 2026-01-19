package session_test

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Recent Searches Tests
// =============================================================================

func TestContext_AddRecentSearch(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentSearch("test query", 5)

	searches := ctx.GetRecentSearches(10)
	require.Len(t, searches, 1)
	assert.Equal(t, "test query", searches[0].Query)
	assert.Equal(t, 5, searches[0].ResultCount)
	assert.False(t, searches[0].Timestamp.IsZero())
}

func TestContext_GetRecentSearches_ReturnsNewestFirst(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentSearch("first", 1)
	time.Sleep(time.Millisecond)
	ctx.AddRecentSearch("second", 2)
	time.Sleep(time.Millisecond)
	ctx.AddRecentSearch("third", 3)

	searches := ctx.GetRecentSearches(3)
	require.Len(t, searches, 3)
	assert.Equal(t, "third", searches[0].Query)
	assert.Equal(t, "second", searches[1].Query)
	assert.Equal(t, "first", searches[2].Query)
}

func TestContext_GetRecentSearches_LimitRespected(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	for i := 0; i < 10; i++ {
		ctx.AddRecentSearch("query", i)
	}

	searches := ctx.GetRecentSearches(3)
	assert.Len(t, searches, 3)
}

func TestContext_GetRecentSearches_InvalidLimit(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentSearch("query", 1)
	ctx.AddRecentSearch("query2", 2)

	// Zero limit returns all
	searches := ctx.GetRecentSearches(0)
	assert.Len(t, searches, 2)

	// Negative limit returns all
	searches = ctx.GetRecentSearches(-5)
	assert.Len(t, searches, 2)

	// Limit greater than count returns all
	searches = ctx.GetRecentSearches(100)
	assert.Len(t, searches, 2)
}

func TestContext_AddRecentSearch_FIFOEviction(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	// Add 55 searches (max is 50)
	for i := 0; i < 55; i++ {
		ctx.AddRecentSearch("query"+string(rune('A'+i%26)), i)
	}

	searches := ctx.GetRecentSearches(100)
	assert.Len(t, searches, 50)

	// Oldest entries (0-4) should be evicted
	// Newest entry should be at index 0 (result count 54)
	assert.Equal(t, 54, searches[0].ResultCount)
	// Oldest remaining entry should be at index 49 (result count 5)
	assert.Equal(t, 5, searches[49].ResultCount)
}

// =============================================================================
// Recent Files Tests
// =============================================================================

func TestContext_AddRecentFile(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentFile("/path/to/file.go", "search")

	files := ctx.GetRecentFiles(10)
	require.Len(t, files, 1)
	assert.Equal(t, "/path/to/file.go", files[0].Path)
	assert.Equal(t, "search", files[0].Source)
	assert.False(t, files[0].AccessedAt.IsZero())
}

func TestContext_GetRecentFiles_ReturnsNewestFirst(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentFile("/first.go", "read")
	time.Sleep(time.Millisecond)
	ctx.AddRecentFile("/second.go", "edit")
	time.Sleep(time.Millisecond)
	ctx.AddRecentFile("/third.go", "search")

	files := ctx.GetRecentFiles(3)
	require.Len(t, files, 3)
	assert.Equal(t, "/third.go", files[0].Path)
	assert.Equal(t, "/second.go", files[1].Path)
	assert.Equal(t, "/first.go", files[2].Path)
}

func TestContext_GetRecentFiles_LimitRespected(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	for i := 0; i < 10; i++ {
		ctx.AddRecentFile("/file", "read")
	}

	files := ctx.GetRecentFiles(3)
	assert.Len(t, files, 3)
}

func TestContext_GetRecentFiles_InvalidLimit(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentFile("/file1.go", "read")
	ctx.AddRecentFile("/file2.go", "edit")

	// Zero limit returns all
	files := ctx.GetRecentFiles(0)
	assert.Len(t, files, 2)

	// Negative limit returns all
	files = ctx.GetRecentFiles(-5)
	assert.Len(t, files, 2)

	// Limit greater than count returns all
	files = ctx.GetRecentFiles(100)
	assert.Len(t, files, 2)
}

func TestContext_AddRecentFile_FIFOEviction(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	// Add 105 files (max is 100)
	for i := 0; i < 105; i++ {
		ctx.AddRecentFile("/file"+string(rune('A'+i%26)), "read")
	}

	files := ctx.GetRecentFiles(200)
	assert.Len(t, files, 100)
}

func TestContext_AddRecentFile_SourceTypes(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddRecentFile("/search.go", "search")
	ctx.AddRecentFile("/edit.go", "edit")
	ctx.AddRecentFile("/read.go", "read")

	files := ctx.GetRecentFiles(3)
	assert.Equal(t, "read", files[0].Source)
	assert.Equal(t, "edit", files[1].Source)
	assert.Equal(t, "search", files[2].Source)
}

// =============================================================================
// Search Preferences Tests
// =============================================================================

func TestContext_SearchPreferences_Default(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	prefs := ctx.GetSearchPreferences()
	assert.Equal(t, 10, prefs.DefaultLimit)
	assert.False(t, prefs.FuzzyDefault)
	assert.Nil(t, prefs.PreferredTypes)
	assert.Nil(t, prefs.ExcludePaths)
}

func TestContext_SetSearchPreferences(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	newPrefs := session.SearchPreferences{
		DefaultLimit:   25,
		PreferredTypes: []string{"go", "rs", "py"},
		ExcludePaths:   []string{"vendor/", "node_modules/"},
		FuzzyDefault:   true,
	}

	ctx.SetSearchPreferences(newPrefs)

	prefs := ctx.GetSearchPreferences()
	assert.Equal(t, 25, prefs.DefaultLimit)
	assert.True(t, prefs.FuzzyDefault)
	assert.Equal(t, []string{"go", "rs", "py"}, prefs.PreferredTypes)
	assert.Equal(t, []string{"vendor/", "node_modules/"}, prefs.ExcludePaths)
}

func TestContext_SearchPreferences_Override(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.SetSearchPreferences(session.SearchPreferences{
		DefaultLimit: 50,
		FuzzyDefault: true,
	})

	// Override with new preferences
	ctx.SetSearchPreferences(session.SearchPreferences{
		DefaultLimit: 100,
		FuzzyDefault: false,
	})

	prefs := ctx.GetSearchPreferences()
	assert.Equal(t, 100, prefs.DefaultLimit)
	assert.False(t, prefs.FuzzyDefault)
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestContext_ConcurrentSearchAccess(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					ctx.AddRecentSearch("query", id)
				}
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = ctx.GetRecentSearches(10)
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Should not panic and should have valid state
	searches := ctx.GetRecentSearches(100)
	assert.LessOrEqual(t, len(searches), 50)
}

func TestContext_ConcurrentFileAccess(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					ctx.AddRecentFile("/path", "read")
				}
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = ctx.GetRecentFiles(10)
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Should not panic and should have valid state
	files := ctx.GetRecentFiles(200)
	assert.LessOrEqual(t, len(files), 100)
}

func TestContext_ConcurrentPreferencesAccess(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					ctx.SetSearchPreferences(session.SearchPreferences{
						DefaultLimit: id * 10,
						FuzzyDefault: id%2 == 0,
					})
				}
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = ctx.GetSearchPreferences()
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Should not panic and should have valid state
	prefs := ctx.GetSearchPreferences()
	assert.GreaterOrEqual(t, prefs.DefaultLimit, 0)
}

func TestContext_ConcurrentMixedOperations(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Search operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				ctx.AddRecentSearch("query", 1)
				_ = ctx.GetRecentSearches(5)
			}
		}
	}()

	// File operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				ctx.AddRecentFile("/path", "read")
				_ = ctx.GetRecentFiles(5)
			}
		}
	}()

	// Preferences operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				ctx.SetSearchPreferences(session.SearchPreferences{DefaultLimit: 20})
				_ = ctx.GetSearchPreferences()
			}
		}
	}()

	// Other context operations (existing functionality)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				ctx.SetService("svc", "value")
				_, _ = ctx.GetService("svc")
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// All operations should complete without data races
	assert.NotNil(t, ctx.GetSearchPreferences())
}

// =============================================================================
// Empty State Tests
// =============================================================================

func TestContext_GetRecentSearches_Empty(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	searches := ctx.GetRecentSearches(10)
	assert.Empty(t, searches)
}

func TestContext_GetRecentFiles_Empty(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	files := ctx.GetRecentFiles(10)
	assert.Empty(t, files)
}
