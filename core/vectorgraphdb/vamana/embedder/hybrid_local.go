package embedder

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"unicode"
)

type HybridLocalEmbedder struct {
	dimension int
	idf       *idfTable
	mu        sync.RWMutex
}

func NewHybridLocalEmbedder() *HybridLocalEmbedder {
	return &HybridLocalEmbedder{
		dimension: EmbeddingDimension,
		idf:       newIdfTable(),
	}
}

func (h *HybridLocalEmbedder) Dimension() int {
	return h.dimension
}

func (h *HybridLocalEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return h.embedHybrid(text), nil
}

func (h *HybridLocalEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		results[i] = h.embedHybrid(text)
	}
	return results, nil
}

func (h *HybridLocalEmbedder) embedHybrid(text string) []float32 {
	vec := make([]float32, h.dimension)

	tokens := tokenize(text)
	trigrams := extractNgrams(text, 3)
	bigrams := extractNgrams(text, 2)

	ngramWeight := 0.4
	tokenWeight := 0.35
	simhashWeight := 0.25

	h.addNgramFeatures(vec, trigrams, ngramWeight*0.6)
	h.addNgramFeatures(vec, bigrams, ngramWeight*0.4)
	h.addTokenFeatures(vec, tokens, tokenWeight)
	h.addSimhashFeatures(vec, text, simhashWeight)

	normalizeVec(vec)
	return vec
}

func (h *HybridLocalEmbedder) addNgramFeatures(vec []float32, ngrams []string, weight float64) {
	if len(ngrams) == 0 {
		return
	}

	w := float32(weight / math.Sqrt(float64(len(ngrams))))

	for _, ng := range ngrams {
		hash := fnvHash64(ng)
		indices := multiHash(hash, h.dimension, 4)
		signs := hashSigns(hash, 4)

		for i, idx := range indices {
			vec[idx] += w * signs[i]
		}
	}
}

func (h *HybridLocalEmbedder) addTokenFeatures(vec []float32, tokens []string, weight float64) {
	if len(tokens) == 0 {
		return
	}

	tf := make(map[string]int)
	for _, tok := range tokens {
		tf[tok]++
	}

	var norm float64
	for tok, count := range tf {
		idf := h.idf.get(tok)
		tfidf := float64(count) * idf
		norm += tfidf * tfidf
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)

	for tok, count := range tf {
		idf := h.idf.get(tok)
		tfidf := float64(count) * idf / norm

		hash := fnvHash64(tok)
		indices := multiHash(hash, h.dimension, 8)
		signs := hashSigns(hash, 8)

		w := float32(weight * tfidf)
		for i, idx := range indices {
			vec[idx] += w * signs[i]
		}
	}
}

func (h *HybridLocalEmbedder) addSimhashFeatures(vec []float32, text string, weight float64) {
	simhash := computeSimhash(text, 64)

	w := float32(weight / 8)
	for i := range 64 {
		bit := (simhash >> i) & 1
		val := float32(-1)
		if bit == 1 {
			val = 1
		}

		startIdx := (i * h.dimension) / 64
		for j := range 16 {
			idx := (startIdx + j) % h.dimension
			vec[idx] += w * val
		}
	}
}

func computeSimhash(text string, bits int) uint64 {
	weights := make([]int, bits)
	shingles := extractNgrams(strings.ToLower(text), 3)

	for _, sh := range shingles {
		hash := fnvHash64(sh)
		for i := range bits {
			if (hash>>i)&1 == 1 {
				weights[i]++
			} else {
				weights[i]--
			}
		}
	}

	var result uint64
	for i := range bits {
		if weights[i] > 0 {
			result |= 1 << i
		}
	}
	return result
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			current.WriteRune(r)
		} else if current.Len() > 0 {
			tok := current.String()
			if len(tok) >= 2 {
				tokens = append(tokens, tok)
			}
			current.Reset()
		}
	}

	if current.Len() >= 2 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

func extractNgrams(text string, n int) []string {
	text = strings.ToLower(text)
	if len(text) < n {
		return nil
	}

	ngrams := make([]string, 0, len(text)-n+1)
	for i := 0; i <= len(text)-n; i++ {
		ngrams = append(ngrams, text[i:i+n])
	}
	return ngrams
}

func fnvHash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func multiHash(seed uint64, dim int, count int) []int {
	indices := make([]int, count)
	state := seed
	for i := range count {
		state = state*6364136223846793005 + 1442695040888963407
		indices[i] = int(state % uint64(dim))
	}
	return indices
}

func hashSigns(seed uint64, count int) []float32 {
	signs := make([]float32, count)
	for i := range count {
		if (seed>>i)&1 == 1 {
			signs[i] = 1
		} else {
			signs[i] = -1
		}
	}
	return signs
}

func normalizeVec(vec []float32) {
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

type idfTable struct {
	mu     sync.RWMutex
	counts map[string]int
	total  int
}

func newIdfTable() *idfTable {
	return &idfTable{
		counts: make(map[string]int),
	}
}

func (t *idfTable) get(token string) float64 {
	t.mu.RLock()
	count := t.counts[token]
	total := t.total
	t.mu.RUnlock()

	if total == 0 || count == 0 {
		return 1.0
	}

	return math.Log(float64(total)/float64(count)) + 1.0
}

func (t *idfTable) update(tokens []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	seen := make(map[string]bool)
	for _, tok := range tokens {
		if !seen[tok] {
			t.counts[tok]++
			seen[tok] = true
		}
	}
	t.total++
}
