package embedder

import (
	"context"
	"math"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/viterin/vek/vek32"
)

// Pre-computed MinHash seeds — constant across all calls.
// Derived from: index × Weyl constant + golden ratio constant.
var minhashSeeds [minhashNumFuncs]uint64

func init() {
	for i := range minhashSeeds {
		minhashSeeds[i] = uint64(i)*0x517cc1b727220a95 + 0x9e3779b97f4a7c15
	}
}

// embedCtx holds reusable buffers for a single embedEnhanced call.
// Pooled via sync.Pool to avoid per-call map and slice allocations.
type embedCtx struct {
	tokens     []string
	stems      []string
	codeTokens []string
	phonetic   []string
	tfMap      map[string]int
	scoreMap   map[string]float64
}

// reset clears all slices and maps for reuse, preserving allocated capacity.
func (c *embedCtx) reset() {
	c.tokens = c.tokens[:0]
	c.stems = c.stems[:0]
	c.codeTokens = c.codeTokens[:0]
	c.phonetic = c.phonetic[:0]
	clear(c.tfMap)
	clear(c.scoreMap)
}

// ensureStemsLen sets stems length, growing capacity if needed.
func (c *embedCtx) ensureStemsLen(n int) {
	if cap(c.stems) < n {
		c.stems = make([]string, n)
	} else {
		c.stems = c.stems[:n]
	}
}

// EnhancedHybridEmbedder implements a sophisticated non-neural embedder targeting 50-52 MTEB.
// It combines multiple signal types for maximum semantic capture without learned representations.
//
// Signal composition:
//   - BM25-weighted stems:       25% (semantic units with morphological normalization)
//   - Word n-grams:              15% (phrase-level semantics)
//   - Character n-grams:         15% (spelling/morphology)
//   - Skip-grams:                10% (non-adjacent relationships)
//   - Co-occurrence features:    10% (distributional semantics approximation)
//   - Code-aware tokens:         10% (camelCase/snake_case splitting)
//   - Phonetic features:          5% (spelling variation tolerance)
//   - MinHash signatures:        10% (robust set similarity)
type EnhancedHybridEmbedder struct {
	dimension int
	ctxPool   sync.Pool
	workers   int
	stemCache sync.Map // Cache for stemmed words
}

// NewEnhancedHybridEmbedder creates an enhanced hybrid embedder.
func NewEnhancedHybridEmbedder() *EnhancedHybridEmbedder {
	dim := EmbeddingDimension
	return &EnhancedHybridEmbedder{
		dimension: dim,
		workers:   runtime.NumCPU(),
		ctxPool: sync.Pool{
			New: func() any {
				return &embedCtx{
					tfMap:    make(map[string]int),
					scoreMap: make(map[string]float64),
				}
			},
		},
	}
}

func (e *EnhancedHybridEmbedder) Dimension() int {
	return e.dimension
}

func (e *EnhancedHybridEmbedder) MaxInputBytes() int {
	// Non-neural: no tokenizer or context window.
	// Derived from: dimension (1024) × signal pipeline count (8) × avg bytes per feature (5).
	// Pipeline count is counted from the 8 signal types in embedEnhanced.
	// Avg bytes per feature is measurable across the signal types (stems, n-grams, etc.).
	const signalPipelineCount = 8
	const avgBytesPerFeature = 5
	return e.dimension * signalPipelineCount * avgBytesPerFeature
}

func (e *EnhancedHybridEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return e.embedEnhanced(text), nil
}

func (e *EnhancedHybridEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	n := len(texts)
	if n == 0 {
		return nil, nil
	}

	results := make([][]float32, n)

	if n < e.workers*2 {
		for i, text := range texts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			results[i] = e.embedEnhanced(text)
		}
		return results, nil
	}

	var wg sync.WaitGroup
	chunkSize := (n + e.workers - 1) / e.workers
	errCh := make(chan error, 1)

	for w := range e.workers {
		start := w * chunkSize
		if start >= n {
			break
		}
		end := min(start+chunkSize, n)

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				select {
				case <-ctx.Done():
					select {
					case errCh <- ctx.Err():
					default:
					}
					return
				default:
					results[i] = e.embedEnhanced(texts[i])
				}
			}
		}(start, end)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return nil, err
	default:
		return results, nil
	}
}

func (e *EnhancedHybridEmbedder) embedEnhanced(text string) []float32 {
	vec := make([]float32, e.dimension)

	ectx := e.ctxPool.Get().(*embedCtx)
	ectx.reset()

	// Inline text analysis using pooled buffers.
	lower := toLowerASCII(text)
	tokenizeEnhancedInto(lower, &ectx.tokens)

	ectx.ensureStemsLen(len(ectx.tokens))
	for i, tok := range ectx.tokens {
		ectx.stems[i] = e.stem(tok)
	}

	tokenizeCodeInto(text, &ectx.codeTokens)

	for _, tok := range ectx.tokens {
		if ph := metaphone(tok); ph != "" {
			ectx.phonetic = append(ectx.phonetic, ph)
		}
	}

	// Weight constants — empirically tuned.
	const (
		stemWeight      = 0.25
		wordNgramWeight = 0.15
		charNgramWeight = 0.15
		skipWeight      = 0.10
		cooccurWeight   = 0.10
		codeWeight      = 0.10
		phoneticWeight  = 0.05
		minhashWeight   = 0.10
	)

	e.addBM25StemFeatures(vec, ectx.stems, stemWeight, ectx.tfMap, ectx.scoreMap)
	e.addWordNgramFeatures(vec, ectx.stems, wordNgramWeight)
	e.addCharNgramFeatures(vec, lower, charNgramWeight)
	e.addSkipGramFeatures(vec, ectx.stems, skipWeight)
	e.addCooccurrenceFeatures(vec, ectx.stems, cooccurWeight)
	e.addCodeTokenFeatures(vec, ectx.codeTokens, codeWeight, ectx.tfMap)
	e.addPhoneticFeatures(vec, ectx.phonetic, phoneticWeight, ectx.tfMap)
	e.addMinHashFeatures(vec, ectx.stems, minhashWeight)

	normalizeVecFast(vec)
	e.ctxPool.Put(ectx)

	return vec
}

// BM25 parameters
const (
	bm25K1    = 1.2
	bm25B     = 0.75
	avgDocLen = 256.0
)

// addBM25StemFeatures adds BM25-weighted stemmed token features.
// tf and scores are pre-allocated maps cleared before use.
func (e *EnhancedHybridEmbedder) addBM25StemFeatures(vec []float32, stems []string, weight float64, tf map[string]int, scores map[string]float64) {
	if len(stems) == 0 {
		return
	}

	clear(tf)
	for _, stem := range stems {
		tf[stem]++
	}

	lenRatio := float64(len(stems)) / avgDocLen
	var normSq float64

	clear(scores)
	for stem, count := range tf {
		// Zipf-based IDF estimation: longer words are rarer
		// log(1 + len) approximates IDF without corpus
		idf := math.Log(1.0 + float64(len(stem)))

		// BM25 saturation
		tfSat := (float64(count) * (bm25K1 + 1)) /
			(float64(count) + bm25K1*(1-bm25B+bm25B*lenRatio))

		score := idf * tfSat
		scores[stem] = score
		normSq += score * score
	}

	if normSq == 0 {
		return
	}
	invNorm := 1.0 / math.Sqrt(normSq)

	for stem, score := range scores {
		w := float32(weight * score * invNorm)
		hash := fnvHash64Inline(stem)
		e.projectWithSign(vec, hash, w, 8)
	}
}

// addWordNgramFeatures computes and hashes word bigrams/trigrams inline
// from stems, avoiding intermediate slice and string allocations.
func (e *EnhancedHybridEmbedder) addWordNgramFeatures(vec []float32, stems []string, weight float64) {
	bigramWeight := weight * 0.6
	trigramWeight := weight * 0.4

	if len(stems) >= 2 {
		count := len(stems) - 1
		w := float32(bigramWeight / math.Sqrt(float64(count)))
		for i := range count {
			hash := fnvHash64JoinedInline("wb:", stems[i:i+2], ' ')
			e.projectWithSign(vec, hash, w, 4)
		}
	}

	if len(stems) >= 3 {
		count := len(stems) - 2
		w := float32(trigramWeight / math.Sqrt(float64(count)))
		for i := range count {
			hash := fnvHash64JoinedInline("wt:", stems[i:i+3], ' ')
			e.projectWithSign(vec, hash, w, 4)
		}
	}
}

// addCharNgramFeatures adds multi-scale character n-grams.
func (e *EnhancedHybridEmbedder) addCharNgramFeatures(vec []float32, text string, weight float64) {
	textLen := len(text)

	scales := []struct {
		n      int
		weight float64
	}{
		{3, 0.50},
		{2, 0.30},
		{4, 0.20},
	}

	for _, scale := range scales {
		if textLen < scale.n {
			continue
		}
		count := textLen - scale.n + 1
		w := float32(weight * scale.weight / math.Sqrt(float64(count)))

		for i := 0; i <= textLen-scale.n; i++ {
			ng := text[i : i+scale.n]
			hash := fnvHash64Inline(ng)
			e.projectWithSign(vec, hash, w, 4)
		}
	}
}

// addSkipGramFeatures adds skip-grams (non-adjacent word pairs).
func (e *EnhancedHybridEmbedder) addSkipGramFeatures(vec []float32, tokens []string, weight float64) {
	if len(tokens) < 2 {
		return
	}

	maxSkip := min(3, len(tokens)-1)
	totalPairs := 0
	for skip := 1; skip <= maxSkip; skip++ {
		totalPairs += len(tokens) - skip
	}

	if totalPairs == 0 {
		return
	}

	baseWeight := weight / math.Sqrt(float64(totalPairs))

	for skip := 1; skip <= maxSkip; skip++ {
		skipWeight := float32(baseWeight / float64(skip))
		bound := len(tokens) - skip
		for i := range bound {
			hash := fnvHash64PairInline("", tokens[i], tokens[i+skip], '\x00')
			e.projectWithSign(vec, hash, skipWeight, 4)
		}
	}
}

// cooccurrenceWindow is the token distance for co-occurrence features.
const cooccurrenceWindow = 5

// addCooccurrenceFeatures computes co-occurrence pairs inline within a
// fixed window, hashing directly without materializing pair strings.
func (e *EnhancedHybridEmbedder) addCooccurrenceFeatures(vec []float32, stems []string, weight float64) {
	if len(stems) < 2 {
		return
	}

	// Count total pairs for normalization.
	totalPairs := 0
	for i := range stems {
		end := min(i+cooccurrenceWindow, len(stems))
		totalPairs += end - i - 1
	}
	if totalPairs == 0 {
		return
	}

	w := float32(weight / math.Sqrt(float64(totalPairs)))
	for i, s1 := range stems {
		end := min(i+cooccurrenceWindow, len(stems))
		for j := i + 1; j < end; j++ {
			s2 := stems[j]
			// Sort for order-independent consistency.
			if s1 > s2 {
				s1, s2 = s2, s1
			}
			hash := fnvHash64PairInline("co:", s1, s2, '\x00')
			e.projectWithSign(vec, hash, w, 4)
		}
	}
}

// addCodeTokenFeatures adds code-aware token features.
// tf is a pre-allocated map cleared before use.
func (e *EnhancedHybridEmbedder) addCodeTokenFeatures(vec []float32, codeTokens []string, weight float64, tf map[string]int) {
	if len(codeTokens) == 0 {
		return
	}

	clear(tf)
	for _, tok := range codeTokens {
		tf[tok]++
	}

	var normSq float64
	for _, count := range tf {
		normSq += float64(count * count)
	}
	if normSq == 0 {
		return
	}
	invNorm := 1.0 / math.Sqrt(normSq)

	for tok, count := range tf {
		w := float32(weight * float64(count) * invNorm)
		hash := fnvHash64WithPrefix("code:", tok)
		e.projectWithSign(vec, hash, w, 6)
	}
}

// addPhoneticFeatures adds phonetic encoding features for spelling tolerance.
// tf is a pre-allocated map cleared before use.
func (e *EnhancedHybridEmbedder) addPhoneticFeatures(vec []float32, phonetic []string, weight float64, tf map[string]int) {
	if len(phonetic) == 0 {
		return
	}

	clear(tf)
	for _, ph := range phonetic {
		tf[ph]++
	}

	var normSq float64
	for _, count := range tf {
		normSq += float64(count * count)
	}
	if normSq == 0 {
		return
	}
	invNorm := 1.0 / math.Sqrt(normSq)

	for ph, count := range tf {
		w := float32(weight * float64(count) * invNorm)
		hash := fnvHash64WithPrefix("ph:", ph)
		e.projectWithSign(vec, hash, w, 4)
	}
}

// minhashNumFuncs is the number of independent hash functions for MinHash.
const minhashNumFuncs = 64

// addMinHashFeatures adds MinHash signatures for Jaccard similarity.
// Uses pre-computed seeds (package init) and a stack-allocated signature array.
func (e *EnhancedHybridEmbedder) addMinHashFeatures(vec []float32, tokens []string, weight float64) {
	if len(tokens) == 0 {
		return
	}

	var signature [minhashNumFuncs]uint64
	for i := range signature {
		signature[i] = ^uint64(0)
	}

	for _, tok := range tokens {
		baseHash := fnvHash64Inline(tok)
		for i := range minhashNumFuncs {
			h := baseHash ^ minhashSeeds[i]
			h = h*6364136223846793005 + 1442695040888963407
			if h < signature[i] {
				signature[i] = h
			}
		}
	}

	w := float32(weight / float64(minhashNumFuncs))
	for _, minH := range signature {
		idx := int(minH % uint64(e.dimension))
		sign := float32(1)
		if (minH>>32)&1 == 0 {
			sign = -1
		}
		vec[idx] += w * sign

		idx2 := (idx + int(minH>>16)%16) % e.dimension
		vec[idx2] += w * sign * 0.5
	}
}

// projectWithSign projects a hash value to multiple indices with signs.
func (e *EnhancedHybridEmbedder) projectWithSign(vec []float32, hash uint64, weight float32, projections int) {
	state := hash

	for j := range projections {
		state = state*6364136223846793005 + 1442695040888963407
		idx := int(state % uint64(e.dimension))
		sign := float32(1)
		if (hash>>j)&1 == 0 {
			sign = -1
		}
		vec[idx] += weight * sign
	}
}

// stem applies Porter stemmer with caching.
func (e *EnhancedHybridEmbedder) stem(word string) string {
	if cached, ok := e.stemCache.Load(word); ok {
		return cached.(string)
	}
	stemmed := porterStem(word)
	e.stemCache.Store(word, stemmed)
	return stemmed
}

// porterStem implements the Porter stemming algorithm.
func porterStem(word string) string {
	if len(word) <= 2 {
		return word
	}

	// Step 1a: plurals
	word = step1a(word)

	// Step 1b: -ed, -ing
	word = step1b(word)

	// Step 1c: y -> i
	word = step1c(word)

	// Step 2: derivational suffixes
	word = step2(word)

	// Step 3: derivational suffixes
	word = step3(word)

	// Step 4: derivational suffixes
	word = step4(word)

	// Step 5: final cleanup
	word = step5(word)

	return word
}

// isConsonant returns true if the character at position i is a consonant.
func isConsonant(word string, i int) bool {
	switch word[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		if i == 0 {
			return true
		}
		return !isConsonant(word, i-1)
	}
	return true
}

// measure returns the "measure" of a word (number of VC sequences).
func measure(word string) int {
	n := len(word)
	if n == 0 {
		return 0
	}

	count := 0
	i := 0

	// Skip initial consonants
	for i < n && isConsonant(word, i) {
		i++
	}

	for i < n {
		// Count vowel sequence
		for i < n && !isConsonant(word, i) {
			i++
		}
		if i < n {
			count++
			// Skip consonant sequence
			for i < n && isConsonant(word, i) {
				i++
			}
		}
	}

	return count
}

// hasVowel returns true if the word contains a vowel.
func hasVowel(word string) bool {
	for i := range len(word) {
		if !isConsonant(word, i) {
			return true
		}
	}
	return false
}

// endsWithDouble returns true if word ends with a double consonant.
func endsWithDouble(word string) bool {
	n := len(word)
	if n < 2 {
		return false
	}
	return word[n-1] == word[n-2] && isConsonant(word, n-1)
}

// endsCVC returns true if word ends consonant-vowel-consonant (not w, x, y).
func endsCVC(word string) bool {
	n := len(word)
	if n < 3 {
		return false
	}
	if !isConsonant(word, n-1) || isConsonant(word, n-2) || !isConsonant(word, n-3) {
		return false
	}
	c := word[n-1]
	return c != 'w' && c != 'x' && c != 'y'
}

func step1a(word string) string {
	if strings.HasSuffix(word, "sses") {
		return word[:len(word)-2]
	}
	if strings.HasSuffix(word, "ies") {
		return word[:len(word)-2]
	}
	if strings.HasSuffix(word, "ss") {
		return word
	}
	if strings.HasSuffix(word, "s") {
		return word[:len(word)-1]
	}
	return word
}

func step1b(word string) string {
	if strings.HasSuffix(word, "eed") {
		stem := word[:len(word)-3]
		if measure(stem) > 0 {
			return word[:len(word)-1]
		}
		return word
	}

	var stem string
	changed := false

	if strings.HasSuffix(word, "ed") {
		stem = word[:len(word)-2]
		if hasVowel(stem) {
			word = stem
			changed = true
		}
	} else if strings.HasSuffix(word, "ing") {
		stem = word[:len(word)-3]
		if hasVowel(stem) {
			word = stem
			changed = true
		}
	}

	if changed {
		if strings.HasSuffix(word, "at") || strings.HasSuffix(word, "bl") || strings.HasSuffix(word, "iz") {
			return word + "e"
		}
		if endsWithDouble(word) {
			c := word[len(word)-1]
			if c != 'l' && c != 's' && c != 'z' {
				return word[:len(word)-1]
			}
		}
		if measure(word) == 1 && endsCVC(word) {
			return word + "e"
		}
	}

	return word
}

func step1c(word string) string {
	if strings.HasSuffix(word, "y") {
		stem := word[:len(word)-1]
		if hasVowel(stem) {
			return stem + "i"
		}
	}
	return word
}

func step2(word string) string {
	suffixes := []struct {
		suffix      string
		replacement string
	}{
		{"ational", "ate"},
		{"tional", "tion"},
		{"enci", "ence"},
		{"anci", "ance"},
		{"izer", "ize"},
		{"abli", "able"},
		{"alli", "al"},
		{"entli", "ent"},
		{"eli", "e"},
		{"ousli", "ous"},
		{"ization", "ize"},
		{"ation", "ate"},
		{"ator", "ate"},
		{"alism", "al"},
		{"iveness", "ive"},
		{"fulness", "ful"},
		{"ousness", "ous"},
		{"aliti", "al"},
		{"iviti", "ive"},
		{"biliti", "ble"},
	}

	for _, s := range suffixes {
		if strings.HasSuffix(word, s.suffix) {
			stem := word[:len(word)-len(s.suffix)]
			if measure(stem) > 0 {
				return stem + s.replacement
			}
			return word
		}
	}
	return word
}

func step3(word string) string {
	suffixes := []struct {
		suffix      string
		replacement string
	}{
		{"icate", "ic"},
		{"ative", ""},
		{"alize", "al"},
		{"iciti", "ic"},
		{"ical", "ic"},
		{"ful", ""},
		{"ness", ""},
	}

	for _, s := range suffixes {
		if strings.HasSuffix(word, s.suffix) {
			stem := word[:len(word)-len(s.suffix)]
			if measure(stem) > 0 {
				return stem + s.replacement
			}
			return word
		}
	}
	return word
}

func step4(word string) string {
	suffixes := []string{
		"al", "ance", "ence", "er", "ic", "able", "ible", "ant",
		"ement", "ment", "ent", "ion", "ou", "ism", "ate", "iti",
		"ous", "ive", "ize",
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(word, suffix) {
			stem := word[:len(word)-len(suffix)]
			if measure(stem) > 1 {
				if suffix == "ion" {
					if len(stem) > 0 && (stem[len(stem)-1] == 's' || stem[len(stem)-1] == 't') {
						return stem
					}
				} else {
					return stem
				}
			}
			return word
		}
	}
	return word
}

func step5(word string) string {
	if strings.HasSuffix(word, "e") {
		stem := word[:len(word)-1]
		m := measure(stem)
		if m > 1 || (m == 1 && !endsCVC(stem)) {
			return stem
		}
	}

	if strings.HasSuffix(word, "ll") && measure(word[:len(word)-1]) > 1 {
		return word[:len(word)-1]
	}

	return word
}

// tokenizeCodeInto splits identifiers by camelCase and snake_case, appending to out.
// Uses position tracking instead of strings.Builder to avoid per-token heap allocations.
func tokenizeCodeInto(text string, out *[]string) {
	start := -1
	hasUpper := false

	flush := func(end int) {
		if start >= 0 && end-start >= 2 {
			tok := text[start:end]
			if hasUpper {
				tok = toLowerASCII(tok)
			}
			*out = append(*out, tok)
		}
		start = -1
		hasUpper = false
	}

	for i := 0; i < len(text); {
		c := text[i]

		// ASCII fast path — handles >95% of source code characters.
		if c < 0x80 {
			tokenizeCodeASCII(c, i, text, &start, &hasUpper, flush, out)
			i++
			continue
		}

		// UTF-8 multibyte fallback.
		r, size := utf8.DecodeRuneInString(text[i:])
		tokenizeCodeUTF8(r, i, size, text, &start, &hasUpper, flush)
		i += size
	}
	flush(len(text))
}

func tokenizeCodeASCII(c byte, i int, text string, start *int, hasUpper *bool, flush func(int), out *[]string) {
	switch {
	case c == '_' || c == '-' || c == '.':
		flush(i)
	case c >= 'A' && c <= 'Z':
		if *start >= 0 && i+1 < len(text) && text[i+1] >= 'a' && text[i+1] <= 'z' {
			flush(i)
		}
		if *start < 0 {
			*start = i
		}
		*hasUpper = true
	case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
		if *start < 0 {
			*start = i
		}
	default:
		flush(i)
	}
}

func tokenizeCodeUTF8(r rune, i, size int, text string, start *int, hasUpper *bool, flush func(int)) {
	switch {
	case unicode.IsUpper(r):
		if *start >= 0 && i+size < len(text) {
			nextR, _ := utf8.DecodeRuneInString(text[i+size:])
			if unicode.IsLower(nextR) {
				flush(i)
			}
		}
		if *start < 0 {
			*start = i
		}
		*hasUpper = true
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		if *start < 0 {
			*start = i
		}
	default:
		flush(i)
	}
}

// metaphone implements a simplified Double Metaphone algorithm.
// Uses a fixed [4]byte buffer and inline uppercase to avoid heap allocations
// from strings.ToUpper and strings.Builder.
func metaphone(word string) string {
	if len(word) == 0 {
		return ""
	}

	var buf [4]byte
	n := 0

	// Inline uppercase lookup — avoids strings.ToUpper allocation.
	upper := func(i int) byte {
		c := word[i]
		if c >= 'a' && c <= 'z' {
			return c - 32
		}
		return c
	}

	// upperIsVowel checks if the uppercase byte at position i is a vowel.
	upperIsVowel := func(i int) bool {
		c := upper(i)
		return c == 'A' || c == 'E' || c == 'I' || c == 'O' || c == 'U'
	}

	// emit appends a byte to the buffer if space remains.
	emit := func(b byte) {
		if n < 4 {
			buf[n] = b
			n++
		}
	}

	// Skip initial silent letters.
	start := 0
	if len(word) >= 2 {
		b0, b1 := upper(0), upper(1)
		if (b0 == 'G' && b1 == 'N') || (b0 == 'K' && b1 == 'N') ||
			(b0 == 'P' && b1 == 'N') || (b0 == 'W' && b1 == 'R') ||
			(b0 == 'P' && b1 == 'S') {
			start = 1
		}
	}

	for i := start; i < len(word) && n < 4; i++ {
		c := upper(i)
		metaphoneChar(c, i, word, start, upper, upperIsVowel, emit, &i)
	}

	if n == 0 {
		return ""
	}
	return string(buf[:n])
}

// metaphoneChar processes one character of the metaphone algorithm.
// Separated from the loop to keep cyclomatic complexity bounded.
func metaphoneChar(c byte, i int, word string, start int, upper func(int) byte, isV func(int) bool, emit func(byte), iPtr *int) {
	switch c {
	case 'A', 'E', 'I', 'O', 'U':
		if i == start {
			emit('A')
		}
	case 'B':
		if i == 0 || upper(i-1) != 'M' || i == len(word)-1 {
			emit('P')
		}
	case 'C':
		metaphoneC(i, word, upper, emit, iPtr)
	case 'D':
		metaphoneD(i, word, upper, emit, iPtr)
	case 'F':
		emit('F')
	case 'G':
		metaphoneG(i, word, upper, isV, emit, iPtr)
	case 'H':
		metaphoneH(i, word, isV, emit)
	case 'J':
		emit('J')
	case 'K':
		if i == 0 || upper(i-1) != 'C' {
			emit('K')
		}
	case 'L':
		emit('L')
	case 'M':
		emit('M')
	case 'N':
		emit('N')
	case 'P':
		if i+1 < len(word) && upper(i+1) == 'H' {
			emit('F')
			*iPtr = i + 1
		} else {
			emit('P')
		}
	case 'Q':
		emit('K')
	case 'R':
		emit('R')
	case 'S':
		if i+1 < len(word) && upper(i+1) == 'H' {
			emit('X')
			*iPtr = i + 1
		} else {
			emit('S')
		}
	case 'T':
		metaphoneT(i, word, upper, emit, iPtr)
	case 'V':
		emit('F')
	case 'W':
		if i+1 < len(word) && isV(i+1) {
			emit('W')
		}
	case 'X':
		emit('K')
		emit('S')
	case 'Y':
		if i+1 < len(word) && isV(i+1) {
			emit('Y')
		}
	case 'Z':
		emit('S')
	}
}

func metaphoneC(i int, word string, upper func(int) byte, emit func(byte), iPtr *int) {
	if i+1 >= len(word) {
		emit('K')
		return
	}
	switch upper(i + 1) {
	case 'H':
		emit('X')
		*iPtr = i + 1
	case 'I', 'E', 'Y':
		emit('S')
	default:
		emit('K')
	}
}

func metaphoneD(i int, word string, upper func(int) byte, emit func(byte), iPtr *int) {
	if i+1 < len(word) && upper(i+1) == 'G' {
		next2 := byte(0)
		if i+2 < len(word) {
			next2 = upper(i + 2)
		}
		if next2 == 'E' || next2 == 'I' || next2 == 'Y' {
			emit('J')
			*iPtr = i + 1
		} else {
			emit('T')
		}
	} else {
		emit('T')
	}
}

func metaphoneG(i int, word string, upper func(int) byte, isV func(int) bool, emit func(byte), iPtr *int) {
	if i+1 >= len(word) {
		emit('K')
		return
	}
	switch upper(i + 1) {
	case 'H':
		if i+2 < len(word) && !isV(i+2) {
			*iPtr = i + 1
		} else {
			emit('K')
		}
	case 'N':
		// Silent
	case 'I', 'E', 'Y':
		emit('J')
	default:
		emit('K')
	}
}

func metaphoneH(i int, word string, isV func(int) bool, emit func(byte)) {
	if i > 0 && isV(i-1) {
		return // Silent after vowel.
	}
	if i+1 < len(word) && isV(i+1) {
		emit('H')
	}
}

func metaphoneT(i int, word string, upper func(int) byte, emit func(byte), iPtr *int) {
	if i+1 >= len(word) {
		emit('T')
		return
	}
	switch upper(i + 1) {
	case 'H':
		emit('0') // TH sound
		*iPtr = i + 1
	case 'I':
		if i+2 < len(word) && (upper(i+2) == 'O' || upper(i+2) == 'A') {
			emit('X')
		} else {
			emit('T')
		}
	default:
		emit('T')
	}
}

// tokenizeEnhancedInto extracts tokens into the provided slice, preserving capacity.
func tokenizeEnhancedInto(text string, out *[]string) {
	var start int
	inToken := false

	for i, r := range text {
		isTokenChar := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
		if isTokenChar {
			if !inToken {
				start = i
				inToken = true
			}
		} else if inToken {
			if i-start >= 2 {
				*out = append(*out, text[start:i])
			}
			inToken = false
		}
	}

	if inToken && len(text)-start >= 2 {
		*out = append(*out, text[start:])
	}
}

// toLowerASCII is an optimized lowercase for ASCII-heavy text.
func toLowerASCII(s string) string {
	needsConvert := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			needsConvert = true
			break
		}
	}
	if !needsConvert {
		return s
	}

	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// FNV-1a 64-bit constants.
const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// fnvHash64Inline computes FNV-1a 64-bit hash inline for performance.
func fnvHash64Inline(s string) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// fnvHash64WithPrefix computes FNV-1a hash of prefix+s without concatenation.
// Produces the same hash as fnvHash64Inline(prefix + s).
func fnvHash64WithPrefix(prefix, s string) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(prefix); i++ {
		h ^= uint64(prefix[i])
		h *= fnvPrime64
	}
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// fnvHash64PairInline computes FNV-1a hash of prefix+a+sep+b without concatenation.
// Produces the same hash as fnvHash64Inline(prefix + a + string(sep) + b).
func fnvHash64PairInline(prefix, a, b string, sep byte) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(prefix); i++ {
		h ^= uint64(prefix[i])
		h *= fnvPrime64
	}
	for i := 0; i < len(a); i++ {
		h ^= uint64(a[i])
		h *= fnvPrime64
	}
	h ^= uint64(sep)
	h *= fnvPrime64
	for i := 0; i < len(b); i++ {
		h ^= uint64(b[i])
		h *= fnvPrime64
	}
	return h
}

// fnvHash64JoinedInline computes FNV-1a hash of prefix + words joined by sep,
// without creating the joined string. Produces the same hash as
// fnvHash64Inline(prefix + strings.Join(words, string(sep))).
func fnvHash64JoinedInline(prefix string, words []string, sep byte) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(prefix); i++ {
		h ^= uint64(prefix[i])
		h *= fnvPrime64
	}
	for i, w := range words {
		if i > 0 {
			h ^= uint64(sep)
			h *= fnvPrime64
		}
		for j := 0; j < len(w); j++ {
			h ^= uint64(w[j])
			h *= fnvPrime64
		}
	}
	return h
}

// normalizeVecFast normalizes the vector in-place to unit length.
func normalizeVecFast(vec []float32) {
	normSq := vek32.Dot(vec, vec)
	if normSq == 0 {
		return
	}
	vek32.MulNumber_Inplace(vec, float32(1.0/math.Sqrt(float64(normSq))))
}
