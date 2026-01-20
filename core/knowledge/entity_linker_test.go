package knowledge

import (
	"sync"
	"testing"
)

// =============================================================================
// PF.4.3: Normalization Cache Tests
// =============================================================================

func TestNormalizationCache_GetSet(t *testing.T) {
	cache := newNormalizationCache(100)

	// Test set and get
	cache.set("Hello", "hello")
	result, found := cache.get("Hello")

	if !found {
		t.Error("Expected to find cached value")
	}
	if result != "hello" {
		t.Errorf("Expected 'hello', got '%s'", result)
	}
}

func TestNormalizationCache_MissReturnsEmpty(t *testing.T) {
	cache := newNormalizationCache(100)

	result, found := cache.get("NotCached")

	if found {
		t.Error("Expected not to find uncached value")
	}
	if result != "" {
		t.Errorf("Expected empty string for miss, got '%s'", result)
	}
}

func TestNormalizationCache_LRUEviction(t *testing.T) {
	// Create a small cache to test eviction
	cache := newNormalizationCache(3)

	// Fill the cache
	cache.set("A", "a")
	cache.set("B", "b")
	cache.set("C", "c")

	// Access A to make it most recently used
	cache.get("A")

	// Add a new entry, should evict B (least recently used)
	cache.set("D", "d")

	// B should be evicted
	_, found := cache.get("B")
	if found {
		t.Error("Expected B to be evicted")
	}

	// A, C, D should still be present
	_, foundA := cache.get("A")
	_, foundC := cache.get("C")
	_, foundD := cache.get("D")

	if !foundA {
		t.Error("Expected A to still be cached")
	}
	if !foundC {
		t.Error("Expected C to still be cached")
	}
	if !foundD {
		t.Error("Expected D to still be cached")
	}
}

func TestNormalizationCache_Clear(t *testing.T) {
	cache := newNormalizationCache(100)

	cache.set("A", "a")
	cache.set("B", "b")

	cache.clear()

	if cache.size() != 0 {
		t.Errorf("Expected empty cache after clear, got size %d", cache.size())
	}

	_, found := cache.get("A")
	if found {
		t.Error("Expected not to find value after clear")
	}
}

func TestNormalizationCache_Size(t *testing.T) {
	cache := newNormalizationCache(100)

	if cache.size() != 0 {
		t.Errorf("Expected size 0, got %d", cache.size())
	}

	cache.set("A", "a")
	if cache.size() != 1 {
		t.Errorf("Expected size 1, got %d", cache.size())
	}

	cache.set("B", "b")
	if cache.size() != 2 {
		t.Errorf("Expected size 2, got %d", cache.size())
	}

	// Update existing entry shouldn't increase size
	cache.set("A", "aa")
	if cache.size() != 2 {
		t.Errorf("Expected size 2 after update, got %d", cache.size())
	}
}

func TestNormalizationCache_ConcurrentAccess(t *testing.T) {
	cache := newNormalizationCache(1000)
	var wg sync.WaitGroup

	// Spawn multiple goroutines doing concurrent reads and writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := string(rune('A' + (id*100+j)%26))
				cache.set(key, key)
				cache.get(key)
			}
		}(i)
	}

	wg.Wait()

	// If we get here without deadlock or panic, the test passes
	if cache.size() == 0 {
		t.Error("Expected some entries in cache after concurrent access")
	}
}

func TestEntityLinker_Normalize_CaseSensitive(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = true
	el := NewEntityLinkerWithConfig(nil, config)

	// In case-sensitive mode, normalize should return the original string
	result := el.normalize("HelloWorld")
	if result != "HelloWorld" {
		t.Errorf("Expected 'HelloWorld', got '%s'", result)
	}
}

func TestEntityLinker_Normalize_CaseInsensitive(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// First call should compute and cache
	result1 := el.normalize("HelloWorld")
	if result1 != "helloworld" {
		t.Errorf("Expected 'helloworld', got '%s'", result1)
	}

	// Second call should use cache
	result2 := el.normalize("HelloWorld")
	if result2 != "helloworld" {
		t.Errorf("Expected 'helloworld', got '%s'", result2)
	}

	// Cache should have the entry
	if el.normCache.size() != 1 {
		t.Errorf("Expected 1 cache entry, got %d", el.normCache.size())
	}
}

// =============================================================================
// PF.4.4: Levenshtein Distance with Threshold Tests
// =============================================================================

func TestLevenshteinDistanceWithThreshold_ExactMatch(t *testing.T) {
	el := NewEntityLinker(nil)

	distance := el.levenshteinDistanceWithThreshold("hello", "hello", 3)
	if distance != 0 {
		t.Errorf("Expected distance 0 for exact match, got %d", distance)
	}
}

func TestLevenshteinDistanceWithThreshold_EmptyStrings(t *testing.T) {
	el := NewEntityLinker(nil)

	// Empty vs non-empty
	distance1 := el.levenshteinDistanceWithThreshold("", "hello", 10)
	if distance1 != 5 {
		t.Errorf("Expected distance 5, got %d", distance1)
	}

	// Non-empty vs empty
	distance2 := el.levenshteinDistanceWithThreshold("hello", "", 10)
	if distance2 != 5 {
		t.Errorf("Expected distance 5, got %d", distance2)
	}

	// Both empty
	distance3 := el.levenshteinDistanceWithThreshold("", "", 10)
	if distance3 != 0 {
		t.Errorf("Expected distance 0 for both empty, got %d", distance3)
	}
}

func TestLevenshteinDistanceWithThreshold_SingleEdit(t *testing.T) {
	el := NewEntityLinker(nil)

	// Substitution
	distance1 := el.levenshteinDistanceWithThreshold("cat", "bat", 3)
	if distance1 != 1 {
		t.Errorf("Expected distance 1 for substitution, got %d", distance1)
	}

	// Insertion
	distance2 := el.levenshteinDistanceWithThreshold("cat", "cats", 3)
	if distance2 != 1 {
		t.Errorf("Expected distance 1 for insertion, got %d", distance2)
	}

	// Deletion
	distance3 := el.levenshteinDistanceWithThreshold("cats", "cat", 3)
	if distance3 != 1 {
		t.Errorf("Expected distance 1 for deletion, got %d", distance3)
	}
}

func TestLevenshteinDistanceWithThreshold_EarlyExitLengthDiff(t *testing.T) {
	el := NewEntityLinker(nil)

	// Length difference > threshold should return threshold+1 immediately
	distance := el.levenshteinDistanceWithThreshold("a", "abcdef", 2)
	if distance != 3 { // threshold + 1
		t.Errorf("Expected early exit with distance 3, got %d", distance)
	}
}

func TestLevenshteinDistanceWithThreshold_EarlyExitDuringComputation(t *testing.T) {
	el := NewEntityLinker(nil)

	// Completely different strings should trigger early exit
	distance := el.levenshteinDistanceWithThreshold("aaaa", "zzzz", 1)
	if distance != 2 { // threshold + 1
		t.Errorf("Expected early exit with distance 2, got %d", distance)
	}
}

func TestLevenshteinDistanceWithThreshold_WithinThreshold(t *testing.T) {
	el := NewEntityLinker(nil)

	// "kitten" to "sitting" has distance 3
	distance := el.levenshteinDistanceWithThreshold("kitten", "sitting", 3)
	if distance != 3 {
		t.Errorf("Expected distance 3, got %d", distance)
	}
}

func TestLevenshteinDistanceWithThreshold_ExceedsThreshold(t *testing.T) {
	el := NewEntityLinker(nil)

	// "kitten" to "sitting" has distance 3, threshold 2 should return 3
	distance := el.levenshteinDistanceWithThreshold("kitten", "sitting", 2)
	if distance != 3 { // threshold + 1
		t.Errorf("Expected distance 3 (threshold+1), got %d", distance)
	}
}

func TestLevenshteinDistanceWithThreshold_BackwardCompatibility(t *testing.T) {
	el := NewEntityLinker(nil)

	// Test that levenshteinDistance (the old method) still works
	// It should delegate to the threshold version with a high threshold
	testCases := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"cat", "cat", 0},
		{"cat", "bat", 1},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
	}

	for _, tc := range testCases {
		distance := el.levenshteinDistance(tc.a, tc.b)
		if distance != tc.expected {
			t.Errorf("levenshteinDistance(%q, %q) = %d, expected %d", tc.a, tc.b, distance, tc.expected)
		}
	}
}

func TestLevenshteinDistanceWithThreshold_SpaceEfficiency(t *testing.T) {
	el := NewEntityLinker(nil)

	// Test with longer strings to verify the algorithm handles them
	a := "thequickbrownfox"
	b := "thequickbrownfox"

	distance := el.levenshteinDistanceWithThreshold(a, b, 5)
	if distance != 0 {
		t.Errorf("Expected distance 0 for identical strings, got %d", distance)
	}

	// Different strings
	a = "thequickbrownfox"
	b = "thelazybrowndog"
	distance = el.levenshteinDistanceWithThreshold(a, b, 10)
	if distance < 1 {
		t.Errorf("Expected non-zero distance for different strings, got %d", distance)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestFuzzyMatchScore_UsesNormalizationCache(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Call fuzzyMatchScore multiple times with same strings
	_ = el.fuzzyMatchScore("HelloWorld", "helloworld")
	_ = el.fuzzyMatchScore("HelloWorld", "helloworld")
	_ = el.fuzzyMatchScore("HelloWorld", "helloworld")

	// Cache should have entries (exact count depends on internal normalization calls)
	if el.normCache.size() == 0 {
		t.Error("Expected normalization cache to have entries")
	}
}

func TestFuzzyMatchScore_UsesThresholdLevenshtein(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.MaxFuzzyDistance = 2
	el := NewEntityLinkerWithConfig(nil, config)

	// Strings with distance <= threshold should get a score
	score := el.fuzzyMatchScore("cat", "bat")
	if score <= 0 {
		t.Errorf("Expected positive score for strings within threshold, got %f", score)
	}

	// Strings with distance > threshold should get low/zero score from edit distance check
	// But may still match via other heuristics (substring, tokens, etc.)
	score = el.fuzzyMatchScore("abc", "xyz")
	// Just verify it doesn't panic and returns a valid score
	if score < 0 || score > 1 {
		t.Errorf("Expected score between 0 and 1, got %f", score)
	}
}

func TestEntityLinker_IndexAndResolve_UsesNormalizationCache(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{Name: "HelloWorld", FilePath: "/test/file.go", Kind: EntityKindFunction},
		{Name: "FooBar", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}

	el.IndexEntities(entities)

	// Resolve using different case
	context := ExtractedEntity{FilePath: "/test/file.go"}
	result := el.ResolveReference("helloworld", context)

	if result == nil {
		t.Error("Expected to resolve 'helloworld' to 'HelloWorld'")
	}
	if result != nil && result.Name != "HelloWorld" {
		t.Errorf("Expected 'HelloWorld', got '%s'", result.Name)
	}

	// Verify cache was used
	if el.normCache.size() == 0 {
		t.Error("Expected normalization cache to have entries after indexing and resolving")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkNormalizationCache_SetGet(b *testing.B) {
	cache := newNormalizationCache(10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "HelloWorld"
		cache.set(key, "helloworld")
		cache.get(key)
	}
}

func BenchmarkLevenshteinDistanceWithThreshold(b *testing.B) {
	el := NewEntityLinker(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		el.levenshteinDistanceWithThreshold("kitten", "sitting", 3)
	}
}

func BenchmarkLevenshteinDistanceWithThreshold_EarlyExit(b *testing.B) {
	el := NewEntityLinker(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This should trigger early exit due to length difference
		el.levenshteinDistanceWithThreshold("a", "abcdefghij", 2)
	}
}

func BenchmarkFuzzyMatchScore_WithCache(b *testing.B) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Pre-warm the cache
	el.normalize("HelloWorld")
	el.normalize("helloworld")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		el.fuzzyMatchScore("HelloWorld", "helloworld")
	}
}

// =============================================================================
// PF.4.2: TokenSet Integration Tests
// =============================================================================

func TestEntityLinker_IndexEntities_PrebuildsTokenSets(t *testing.T) {
	// PF.4.2: Test that TokenSets are pre-built during indexing
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{Name: "getUserName", FilePath: "/test/file.go", Kind: EntityKindFunction},
		{Name: "setUserAge", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}

	el.IndexEntities(entities)

	// PF.4.2: Verify TokenSets are pre-built
	if len(el.entityNameTokenSets) != 2 {
		t.Errorf("PF.4.2: expected 2 TokenSets, got %d", len(el.entityNameTokenSets))
	}

	// Verify TokenSet for normalized "getusername"
	tokenSet, exists := el.entityNameTokenSets["getusername"]
	if !exists {
		t.Fatal("PF.4.2: expected TokenSet for 'getusername'")
	}

	// The tokenizeName function should produce tokens: get, user, name
	expectedTokens := []string{"get", "user", "name"}
	for _, token := range expectedTokens {
		if !tokenSet.Contains(token) {
			t.Errorf("PF.4.2: expected TokenSet to contain %q, got tokens: %v", token, tokenSet.Tokens())
		}
	}
}

func TestEntityLinker_FuzzyMatchScore_UsesTokenSetIntersection(t *testing.T) {
	// PF.4.2: Test that fuzzyMatchScore uses TokenSet for O(k) token matching
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Index entities to pre-build TokenSets
	entities := []ExtractedEntity{
		{Name: "getUserDetails", FilePath: "/test/file.go", Kind: EntityKindFunction},
		{Name: "setUserProfile", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}
	el.IndexEntities(entities)

	// Test matching with reference that shares tokens with indexed entity
	// "fetchUserInfo" shares "user" with "getUserDetails"
	score := el.fuzzyMatchScore("fetchUserInfo", "getuserdetails")

	// Should get a positive score due to token overlap
	if score <= 0 {
		t.Errorf("PF.4.2: expected positive score for token overlap, got %f", score)
	}
}

func TestEntityLinker_FuzzyMatchScore_UsesCachedTokenSet(t *testing.T) {
	// PF.4.2: Verify cached TokenSets are used when available
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Index entities to pre-build TokenSets
	entities := []ExtractedEntity{
		{Name: "processUserData", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}
	el.IndexEntities(entities)

	// Verify TokenSet is cached
	if _, exists := el.entityNameTokenSets["processuserdata"]; !exists {
		t.Fatal("PF.4.2: expected TokenSet to be cached for 'processuserdata'")
	}

	// Now perform fuzzy matching - it should use the cached TokenSet
	// "validateUserData" shares "user" and "data" tokens
	score := el.fuzzyMatchScore("validateUserData", "processuserdata")

	if score <= 0 {
		t.Errorf("PF.4.2: expected positive score when using cached TokenSet, got %f", score)
	}
}

func TestEntityLinker_FuzzyMatchScore_TokenMatchWithNoOverlap(t *testing.T) {
	// PF.4.2: Test token matching with no overlap returns 0
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Completely different tokens, no substring/prefix match, beyond edit distance
	score := el.fuzzyMatchScore("abcXyzQrs", "defMnoPqr")

	// Should return 0 for no match
	if score != 0 {
		t.Errorf("PF.4.2: expected score 0 for no token overlap, got %f", score)
	}
}

func TestEntityLinker_Clear_ClearsTokenSetCache(t *testing.T) {
	// PF.4.2: Test that Clear() also clears the TokenSet cache
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{Name: "testFunction", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}
	el.IndexEntities(entities)

	// Verify TokenSet exists
	if len(el.entityNameTokenSets) == 0 {
		t.Fatal("expected TokenSet to exist before clear")
	}

	el.Clear()

	// Verify TokenSet cache is cleared
	if len(el.entityNameTokenSets) != 0 {
		t.Error("PF.4.2: expected entityNameTokenSets to be cleared")
	}
}

func TestEntityLinker_TokenSetPerformance(t *testing.T) {
	// PF.4.2: Test performance characteristics of TokenSet intersection
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Create entities with compound names
	entities := []ExtractedEntity{
		{Name: "processUserAuthenticationRequestHandler", FilePath: "/test/file.go", Kind: EntityKindFunction},
		{Name: "validateUserSessionTokenManager", FilePath: "/test/file.go", Kind: EntityKindFunction},
		{Name: "handleUserPermissionCheckService", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}
	el.IndexEntities(entities)

	// Perform many fuzzy match operations
	// With TokenSet O(k), this should be efficient
	refs := []string{
		"checkUserAuth",
		"verifyUserSession",
		"getUserPermission",
	}

	targets := []string{
		"processuserauthenticationrequesthandler",
		"validateusersessiontokenmanager",
		"handleuserpermissioncheckservice",
	}

	for i := 0; i < 100; i++ {
		for _, ref := range refs {
			for _, target := range targets {
				score := el.fuzzyMatchScore(ref, target)
				if score < 0 || score > 1 {
					t.Errorf("invalid score %f for %q vs %q", score, ref, target)
				}
			}
		}
	}
}

// =============================================================================
// PF.4.2: TokenSet Benchmark
// =============================================================================

func BenchmarkFuzzyMatchScore_WithTokenSet(b *testing.B) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Pre-build TokenSets
	entities := []ExtractedEntity{
		{Name: "processUserData", FilePath: "/test/file.go", Kind: EntityKindFunction},
	}
	el.IndexEntities(entities)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		el.fuzzyMatchScore("validateUserData", "processuserdata")
	}
}

func BenchmarkFuzzyMatchScore_WithoutCachedTokenSet(b *testing.B) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	el := NewEntityLinkerWithConfig(nil, config)

	// Don't pre-build TokenSets
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		el.fuzzyMatchScore("validateUserData", "processUserData")
	}
}
