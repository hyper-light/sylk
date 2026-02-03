package embedder

import (
	"context"
	"math"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Pre-computed MinHash seeds — constant across all calls.
// Derived from: index × Weyl constant + golden ratio constant.
var minhashSeeds [minhashNumFuncs]uint64

func init() {
	for i := range minhashSeeds {
		minhashSeeds[i] = uint64(i)*0x517cc1b727220a95 + 0x9e3779b97f4a7c15
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
	vecPool   sync.Pool
	workers   int
	stemCache sync.Map // Cache for stemmed words
}

// NewEnhancedHybridEmbedder creates an enhanced hybrid embedder.
func NewEnhancedHybridEmbedder() *EnhancedHybridEmbedder {
	dim := EmbeddingDimension
	return &EnhancedHybridEmbedder{
		dimension: dim,
		workers:   runtime.NumCPU(),
		vecPool: sync.Pool{
			New: func() any {
				return make([]float32, dim)
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

// textAnalysis holds preprocessed features for embedding.
// N-grams and co-occurrence pairs are computed inline during feature
// extraction to avoid intermediate slice allocations.
type textAnalysis struct {
	lower      string
	tokens     []string // Raw tokens
	stems      []string // Porter-stemmed tokens
	codeTokens []string // Code-aware split tokens
	phonetic   []string // Phonetic encodings
}

func (e *EnhancedHybridEmbedder) analyzeText(text string) textAnalysis {
	lower := toLowerASCII(text)
	tokens := tokenizeEnhanced(lower)

	stems := make([]string, len(tokens))
	for i, tok := range tokens {
		stems[i] = e.stem(tok)
	}

	codeTokens := tokenizeCode(text)

	phonetic := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if ph := metaphone(tok); ph != "" {
			phonetic = append(phonetic, ph)
		}
	}

	return textAnalysis{
		lower:      lower,
		tokens:     tokens,
		stems:      stems,
		codeTokens: codeTokens,
		phonetic:   phonetic,
	}
}

func (e *EnhancedHybridEmbedder) embedEnhanced(text string) []float32 {
	vec := e.vecPool.Get().([]float32)
	clear(vec)

	a := e.analyzeText(text)

	// Weight constants - empirically tuned
	const (
		stemWeight       = 0.25
		wordNgramWeight  = 0.15
		charNgramWeight  = 0.15
		skipWeight       = 0.10
		cooccurWeight    = 0.10
		codeWeight       = 0.10
		phoneticWeight   = 0.05
		minhashWeight    = 0.10
	)

	e.addBM25StemFeatures(vec, a.stems, stemWeight)
	e.addWordNgramFeatures(vec, a.stems, wordNgramWeight)
	e.addCharNgramFeatures(vec, a.lower, charNgramWeight)
	e.addSkipGramFeatures(vec, a.stems, skipWeight)
	e.addCooccurrenceFeatures(vec, a.stems, cooccurWeight)
	e.addCodeTokenFeatures(vec, a.codeTokens, codeWeight)
	e.addPhoneticFeatures(vec, a.phonetic, phoneticWeight)
	e.addMinHashFeatures(vec, a.stems, minhashWeight)

	normalizeVecFast(vec)

	result := make([]float32, e.dimension)
	copy(result, vec)
	e.vecPool.Put(vec)

	return result
}

// BM25 parameters
const (
	bm25K1    = 1.2
	bm25B     = 0.75
	avgDocLen = 256.0
)

// addBM25StemFeatures adds BM25-weighted stemmed token features.
func (e *EnhancedHybridEmbedder) addBM25StemFeatures(vec []float32, stems []string, weight float64) {
	if len(stems) == 0 {
		return
	}

	tf := make(map[string]int, len(stems))
	for _, stem := range stems {
		tf[stem]++
	}

	lenRatio := float64(len(stems)) / avgDocLen
	var normSq float64

	scores := make(map[string]float64, len(tf))
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
func (e *EnhancedHybridEmbedder) addCodeTokenFeatures(vec []float32, codeTokens []string, weight float64) {
	if len(codeTokens) == 0 {
		return
	}

	tf := make(map[string]int, len(codeTokens))
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
func (e *EnhancedHybridEmbedder) addPhoneticFeatures(vec []float32, phonetic []string, weight float64) {
	if len(phonetic) == 0 {
		return
	}

	tf := make(map[string]int, len(phonetic))
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

// tokenizeCode splits identifiers by camelCase and snake_case.
// Uses byte-level processing for ASCII (fast path) with UTF-8 fallback.
func tokenizeCode(text string) []string {
	var tokens []string
	var current strings.Builder

	flushToken := func() {
		if current.Len() >= 2 {
			tokens = append(tokens, toLowerASCII(current.String()))
		}
		current.Reset()
	}

	for i := 0; i < len(text); {
		c := text[i]

		// ASCII fast path — handles >95% of source code characters.
		if c < 0x80 {
			switch {
			case c == '_' || c == '-' || c == '.':
				flushToken()
			case c >= 'A' && c <= 'Z':
				if current.Len() > 0 && i+1 < len(text) &&
					text[i+1] >= 'a' && text[i+1] <= 'z' {
					flushToken()
				}
				current.WriteByte(c + 32)
			case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
				current.WriteByte(c)
			default:
				flushToken()
			}
			i++
			continue
		}

		// UTF-8 multibyte fallback.
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case unicode.IsUpper(r):
			if current.Len() > 0 {
				if i+size < len(text) {
					nextR, _ := utf8.DecodeRuneInString(text[i+size:])
					if unicode.IsLower(nextR) {
						flushToken()
					}
				}
			}
			current.WriteRune(unicode.ToLower(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)
		default:
			flushToken()
		}
		i += size
	}
	flushToken()

	return tokens
}

// metaphone implements a simplified Double Metaphone algorithm.
func metaphone(word string) string {
	if len(word) == 0 {
		return ""
	}

	word = strings.ToUpper(word)
	var result strings.Builder

	// Skip initial silent letters
	start := 0
	if len(word) >= 2 {
		switch word[:2] {
		case "GN", "KN", "PN", "WR", "PS":
			start = 1
		}
	}

	for i := start; i < len(word) && result.Len() < 4; i++ {
		c := word[i]

		switch c {
		case 'A', 'E', 'I', 'O', 'U':
			if i == start {
				result.WriteByte('A')
			}
		case 'B':
			if i == 0 || word[i-1] != 'M' || i == len(word)-1 {
				result.WriteByte('P')
			}
		case 'C':
			if i+1 < len(word) {
				switch word[i+1] {
				case 'H':
					result.WriteByte('X')
					i++
				case 'I', 'E', 'Y':
					result.WriteByte('S')
				default:
					result.WriteByte('K')
				}
			} else {
				result.WriteByte('K')
			}
		case 'D':
			if i+1 < len(word) && word[i+1] == 'G' {
				next2 := byte(0)
				if i+2 < len(word) {
					next2 = word[i+2]
				}
				if next2 == 'E' || next2 == 'I' || next2 == 'Y' {
					result.WriteByte('J')
					i++
				} else {
					result.WriteByte('T')
				}
			} else {
				result.WriteByte('T')
			}
		case 'F':
			result.WriteByte('F')
		case 'G':
			if i+1 < len(word) {
				switch word[i+1] {
				case 'H':
					if i+2 < len(word) && !isVowel(word[i+2]) {
						i++
					} else {
						result.WriteByte('K')
					}
				case 'N':
					// Silent
				case 'I', 'E', 'Y':
					result.WriteByte('J')
				default:
					result.WriteByte('K')
				}
			} else {
				result.WriteByte('K')
			}
		case 'H':
			if i > 0 && isVowel(word[i-1]) {
				// Silent after vowel
			} else if i+1 < len(word) && isVowel(word[i+1]) {
				result.WriteByte('H')
			}
		case 'J':
			result.WriteByte('J')
		case 'K':
			if i == 0 || word[i-1] != 'C' {
				result.WriteByte('K')
			}
		case 'L':
			result.WriteByte('L')
		case 'M':
			result.WriteByte('M')
		case 'N':
			result.WriteByte('N')
		case 'P':
			if i+1 < len(word) && word[i+1] == 'H' {
				result.WriteByte('F')
				i++
			} else {
				result.WriteByte('P')
			}
		case 'Q':
			result.WriteByte('K')
		case 'R':
			result.WriteByte('R')
		case 'S':
			if i+1 < len(word) && word[i+1] == 'H' {
				result.WriteByte('X')
				i++
			} else {
				result.WriteByte('S')
			}
		case 'T':
			if i+1 < len(word) {
				switch word[i+1] {
				case 'H':
					result.WriteByte('0') // TH sound
					i++
				case 'I':
					if i+2 < len(word) && (word[i+2] == 'O' || word[i+2] == 'A') {
						result.WriteByte('X')
					} else {
						result.WriteByte('T')
					}
				default:
					result.WriteByte('T')
				}
			} else {
				result.WriteByte('T')
			}
		case 'V':
			result.WriteByte('F')
		case 'W':
			if i+1 < len(word) && isVowel(word[i+1]) {
				result.WriteByte('W')
			}
		case 'X':
			result.WriteByte('K')
			result.WriteByte('S')
		case 'Y':
			if i+1 < len(word) && isVowel(word[i+1]) {
				result.WriteByte('Y')
			}
		case 'Z':
			result.WriteByte('S')
		}
	}

	return result.String()
}

func isVowel(c byte) bool {
	switch c {
	case 'A', 'E', 'I', 'O', 'U':
		return true
	}
	return false
}

// tokenizeEnhanced extracts tokens with improved handling.
func tokenizeEnhanced(text string) []string {
	estimated := len(text) / 5
	tokens := make([]string, 0, estimated)

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
				tokens = append(tokens, text[start:i])
			}
			inToken = false
		}
	}

	if inToken && len(text)-start >= 2 {
		tokens = append(tokens, text[start:])
	}

	return tokens
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
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return
	}
	invNorm := float32(1.0 / math.Sqrt(sumSq))
	for i := range vec {
		vec[i] *= invNorm
	}
}
