package embedder

import (
	"context"
	"hash/fnv"
	"math"
	"runtime"
	"strings"
	"sync"
)

// ClusteredMockEmbedder produces embeddings with semantic locality.
// Symbols in the same file/directory cluster together, same kind clusters together.
// This simulates the manifold structure of real embeddings.
type ClusteredMockEmbedder struct {
	dimension int

	// Cached base vectors for directories and kinds
	mu          sync.RWMutex
	dirVectors  map[string][]float32
	kindVectors map[string][]float32
}

// NewClusteredMockEmbedder creates an embedder that produces clustered embeddings.
func NewClusteredMockEmbedder(dimension int) *ClusteredMockEmbedder {
	return &ClusteredMockEmbedder{
		dimension:   dimension,
		dirVectors:  make(map[string][]float32),
		kindVectors: make(map[string][]float32),
	}
}

func (c *ClusteredMockEmbedder) Dimension() int {
	return c.dimension
}

func (c *ClusteredMockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return c.embedClustered(text), nil
}

func (c *ClusteredMockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	n := len(texts)
	results := make([][]float32, n)

	numWorkers := runtime.NumCPU()
	chunkSize := (n + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := range numWorkers {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				results[i] = c.embedClustered(texts[i])
			}
		}(start, end)
	}
	wg.Wait()

	return results, nil
}

// embedClustered produces a vector that clusters similar symbols together.
// Uses weighted combination of components with overlap for natural similarity gradients.
func (c *ClusteredMockEmbedder) embedClustered(text string) []float32 {
	dir, kind, name, sig := parseSymbolText(text)

	vec := make([]float32, c.dimension)

	// Weighted combination: name is most important (semantics), then dir, then kind, then sig.
	// Components OVERLAP in the embedding space to create smooth similarity gradients.
	dirVec := c.getOrCreateDirVector(dir)
	kindVec := c.getOrCreateKindVector(kind)
	nameVec := c.computeNameVector(name, c.dimension)
	sigVec := c.computeSignatureVector(sig, c.dimension)

	// Combine with decreasing weights: name 0.5, dir 0.25, kind 0.15, sig 0.10
	for i := range c.dimension {
		vec[i] = 0.50*nameVec[i] + 0.25*dirVec[i] + 0.15*kindVec[i] + 0.10*sigVec[i]
	}

	normalize(vec)
	return vec
}

// parseSymbolText extracts components from "path kind name signature" format.
func parseSymbolText(text string) (dir, kind, name, sig string) {
	parts := strings.SplitN(text, " ", 4)
	if len(parts) >= 1 {
		// First part is path - extract directory
		dir = extractDir(parts[0])
	}
	if len(parts) >= 2 {
		kind = parts[1]
	}
	if len(parts) >= 3 {
		name = parts[2]
	}
	if len(parts) >= 4 {
		sig = parts[3]
	}
	return
}

// extractDir gets the directory from a file path.
func extractDir(path string) string {
	// Handle both "/" and "\" separators
	lastSlash := strings.LastIndexAny(path, "/\\")
	if lastSlash >= 0 {
		return path[:lastSlash]
	}
	return path
}

// getOrCreateDirVector returns a cached or new base vector for a directory.
func (c *ClusteredMockEmbedder) getOrCreateDirVector(dir string) []float32 {
	c.mu.RLock()
	if vec, ok := c.dirVectors[dir]; ok {
		c.mu.RUnlock()
		return vec
	}
	c.mu.RUnlock()

	// Create new vector with hierarchical structure
	// Similar directories (shared prefix) get similar vectors
	vec := c.computeHierarchicalVector(dir, c.dimension)

	c.mu.Lock()
	c.dirVectors[dir] = vec
	c.mu.Unlock()

	return vec
}

// computeHierarchicalVector creates a vector where paths with shared prefixes are similar.
func (c *ClusteredMockEmbedder) computeHierarchicalVector(path string, dim int) []float32 {
	vec := make([]float32, dim)

	// Split path into components
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	// Each path component contributes to the vector
	// Earlier components have more influence (shared ancestry)
	for level, part := range parts {
		partHash := hashString(part)
		weight := 1.0 / float64(level+1) // Decreasing weight for deeper levels

		// Add contribution from this level
		for i := range dim {
			// Use different seeds for each dimension
			seed := partHash ^ uint64(i*7919)
			val := pcgFloat(seed)
			vec[i] += float32(val * weight)
		}
	}

	normalize(vec)
	return vec
}

// getOrCreateKindVector returns a cached or new base vector for a symbol kind.
func (c *ClusteredMockEmbedder) getOrCreateKindVector(kind string) []float32 {
	c.mu.RLock()
	if vec, ok := c.kindVectors[kind]; ok {
		c.mu.RUnlock()
		return vec
	}
	c.mu.RUnlock()

	// Create deterministic vector for this kind
	vec := deterministicVector(kind, c.dimension)

	c.mu.Lock()
	c.kindVectors[kind] = vec
	c.mu.Unlock()

	return vec
}

// computeNameVector creates a vector where similar names have similar vectors.
// Uses character n-grams to capture name similarity.
func (c *ClusteredMockEmbedder) computeNameVector(name string, dim int) []float32 {
	vec := make([]float32, dim)

	// Use character trigrams for name similarity
	name = strings.ToLower(name)
	trigrams := extractTrigrams(name)

	for _, tri := range trigrams {
		triHash := hashString(tri)
		for i := 0; i < 8 && i < dim; i++ {
			idx := int((triHash + uint64(i)) % uint64(dim))
			vec[idx] += 0.1
		}
	}

	// Add base from full name hash
	nameHash := hashString(name)
	for i := range dim {
		seed := nameHash ^ uint64(i*13)
		vec[i] += float32(pcgFloat(seed) * 0.5)
	}

	normalize(vec)
	return vec
}

// extractTrigrams returns character trigrams from a string.
func extractTrigrams(s string) []string {
	if len(s) < 3 {
		return []string{s}
	}
	trigrams := make([]string, 0, len(s)-2)
	for i := 0; i <= len(s)-3; i++ {
		trigrams = append(trigrams, s[i:i+3])
	}
	return trigrams
}

// computeSignatureVector creates a vector from the signature.
func (c *ClusteredMockEmbedder) computeSignatureVector(sig string, dim int) []float32 {
	if sig == "" {
		// Return small random vector for symbols without signatures
		vec := make([]float32, dim)
		for i := range vec {
			vec[i] = float32(pcgFloat(uint64(i))) * 0.1
		}
		return vec
	}

	// Extract tokens from signature (types, parameter names)
	tokens := tokenizeSig(sig)

	vec := make([]float32, dim)
	for _, tok := range tokens {
		tokHash := hashString(tok)
		for i := 0; i < dim/16 && i < dim; i++ {
			idx := int((tokHash + uint64(i)) % uint64(dim))
			vec[idx] += 0.05
		}
	}

	// Add base variation
	sigHash := hashString(sig)
	for i := range dim {
		seed := sigHash ^ uint64(i*17)
		vec[i] += float32(pcgFloat(seed) * 0.3)
	}

	normalize(vec)
	return vec
}

// tokenizeSig extracts meaningful tokens from a signature.
func tokenizeSig(sig string) []string {
	// Simple tokenization: split on common delimiters
	tokens := strings.FieldsFunc(sig, func(r rune) bool {
		return r == '(' || r == ')' || r == ',' || r == ' ' || r == '*' || r == '[' || r == ']'
	})

	// Filter short tokens
	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len(t) >= 2 {
			result = append(result, strings.ToLower(t))
		}
	}
	return result
}

// deterministicVector creates a deterministic unit vector from a string.
func deterministicVector(s string, dim int) []float32 {
	vec := make([]float32, dim)
	h := hashString(s)

	for i := range dim {
		seed := h ^ uint64(i*31)
		vec[i] = float32(pcgFloat(seed)*2 - 1)
	}

	normalize(vec)
	return vec
}

// hashString returns a 64-bit hash of a string.
func hashString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// pcgFloat returns a float64 in [0, 1) from a seed.
func pcgFloat(state uint64) float64 {
	// PCG-style mixing
	state = state*6364136223846793005 + 1442695040888963407
	return float64(state>>32) / float64(1<<32)
}

// normalize normalizes a vector to unit length.
func normalize(vec []float32) {
	var mag float64
	for _, v := range vec {
		mag += float64(v * v)
	}
	if mag == 0 {
		return
	}
	invMag := float32(1.0 / math.Sqrt(mag))
	for i := range vec {
		vec[i] *= invMag
	}
}
