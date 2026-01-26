package embedder

import (
	"context"
	"math"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

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

// textAnalysis holds all preprocessed features for embedding.
type textAnalysis struct {
	original      string
	lower         string
	tokens        []string   // Raw tokens
	stems         []string   // Porter-stemmed tokens
	codeTokens    []string   // Code-aware split tokens
	phonetic      []string   // Phonetic encodings
	wordBigrams   []string   // Word-level bigrams
	wordTrigrams  []string   // Word-level trigrams
	cooccurrences []string   // Co-occurrence pairs within window
}

func (e *EnhancedHybridEmbedder) analyzeText(text string) textAnalysis {
	lower := toLowerASCII(text)
	tokens := tokenizeEnhanced(lower)

	// Stem all tokens
	stems := make([]string, len(tokens))
	for i, tok := range tokens {
		stems[i] = e.stem(tok)
	}

	// Code-aware tokenization (split camelCase, snake_case)
	codeTokens := e.tokenizeCode(text)

	// Phonetic encoding
	phonetic := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if ph := metaphone(tok); ph != "" {
			phonetic = append(phonetic, ph)
		}
	}

	// Word-level n-grams
	wordBigrams := extractWordNgrams(stems, 2)
	wordTrigrams := extractWordNgrams(stems, 3)

	// Co-occurrence pairs within window
	cooccurrences := extractCooccurrences(stems, 5)

	return textAnalysis{
		original:      text,
		lower:         lower,
		tokens:        tokens,
		stems:         stems,
		codeTokens:    codeTokens,
		phonetic:      phonetic,
		wordBigrams:   wordBigrams,
		wordTrigrams:  wordTrigrams,
		cooccurrences: cooccurrences,
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

	// Add all feature types
	e.addBM25StemFeatures(vec, a.stems, stemWeight)
	e.addWordNgramFeatures(vec, a.wordBigrams, a.wordTrigrams, wordNgramWeight)
	e.addCharNgramFeatures(vec, a.lower, charNgramWeight)
	e.addSkipGramFeatures(vec, a.stems, skipWeight)
	e.addCooccurrenceFeatures(vec, a.cooccurrences, cooccurWeight)
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

// addWordNgramFeatures adds word-level bigrams and trigrams.
func (e *EnhancedHybridEmbedder) addWordNgramFeatures(vec []float32, bigrams, trigrams []string, weight float64) {
	bigramWeight := weight * 0.6
	trigramWeight := weight * 0.4

	if len(bigrams) > 0 {
		w := float32(bigramWeight / math.Sqrt(float64(len(bigrams))))
		for _, bg := range bigrams {
			hash := fnvHash64Inline("wb:" + bg)
			e.projectWithSign(vec, hash, w, 4)
		}
	}

	if len(trigrams) > 0 {
		w := float32(trigramWeight / math.Sqrt(float64(len(trigrams))))
		for _, tg := range trigrams {
			hash := fnvHash64Inline("wt:" + tg)
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
			pair := tokens[i] + "\x00" + tokens[i+skip]
			hash := fnvHash64Inline(pair)
			e.projectWithSign(vec, hash, skipWeight, 4)
		}
	}
}

// addCooccurrenceFeatures adds co-occurrence window features.
// This approximates distributional semantics without training.
func (e *EnhancedHybridEmbedder) addCooccurrenceFeatures(vec []float32, cooccurrences []string, weight float64) {
	if len(cooccurrences) == 0 {
		return
	}

	w := float32(weight / math.Sqrt(float64(len(cooccurrences))))
	for _, pair := range cooccurrences {
		hash := fnvHash64Inline("co:" + pair)
		e.projectWithSign(vec, hash, w, 4)
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
		hash := fnvHash64Inline("code:" + tok)
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
		hash := fnvHash64Inline("ph:" + ph)
		e.projectWithSign(vec, hash, w, 4)
	}
}

// addMinHashFeatures adds MinHash signatures for Jaccard similarity.
func (e *EnhancedHybridEmbedder) addMinHashFeatures(vec []float32, tokens []string, weight float64) {
	if len(tokens) == 0 {
		return
	}

	// Use multiple hash functions for MinHash
	numHashes := 64
	seeds := make([]uint64, numHashes)
	for i := range seeds {
		seeds[i] = uint64(i)*0x517cc1b727220a95 + 0x9e3779b97f4a7c15
	}

	// Compute MinHash signature
	signature := make([]uint64, numHashes)
	for i := range signature {
		signature[i] = ^uint64(0) // Max value
	}

	for _, tok := range tokens {
		baseHash := fnvHash64Inline(tok)
		for i, seed := range seeds {
			h := baseHash ^ seed
			h = h*6364136223846793005 + 1442695040888963407
			if h < signature[i] {
				signature[i] = h
			}
		}
	}

	// Project signature into vector
	w := float32(weight / float64(numHashes))
	for i, minH := range signature {
		// Use signature value to determine position and sign
		idx := int(minH % uint64(e.dimension))
		sign := float32(1)
		if (minH>>32)&1 == 0 {
			sign = -1
		}
		vec[idx] += w * sign

		// Also spread to nearby positions for smoothness
		idx2 := (idx + int(minH>>16)%16) % e.dimension
		vec[idx2] += w * sign * 0.5
		_ = i // Silence unused warning
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
func (e *EnhancedHybridEmbedder) tokenizeCode(text string) []string {
	var tokens []string
	var current strings.Builder

	flushToken := func() {
		if current.Len() >= 2 {
			tok := strings.ToLower(current.String())
			tokens = append(tokens, tok)
		}
		current.Reset()
	}

	runes := []rune(text)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.':
			flushToken()
		case unicode.IsUpper(r):
			// Check for camelCase boundary
			if current.Len() > 0 {
				// Check if this is start of new word (lowercase followed by uppercase)
				// or end of acronym (uppercase followed by uppercase then lowercase)
				if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					flushToken()
				}
			}
			current.WriteRune(unicode.ToLower(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)
		default:
			flushToken()
		}
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

// extractWordNgrams extracts word-level n-grams.
func extractWordNgrams(words []string, n int) []string {
	if len(words) < n {
		return nil
	}

	ngrams := make([]string, 0, len(words)-n+1)
	for i := 0; i <= len(words)-n; i++ {
		ngram := strings.Join(words[i:i+n], " ")
		ngrams = append(ngrams, ngram)
	}
	return ngrams
}

// extractCooccurrences extracts all word pairs within a window.
func extractCooccurrences(words []string, windowSize int) []string {
	if len(words) < 2 {
		return nil
	}

	var pairs []string
	for i, w1 := range words {
		end := min(i+windowSize, len(words))
		for j := i + 1; j < end; j++ {
			// Sort pair for consistency (order-independent)
			w2 := words[j]
			if w1 > w2 {
				w1, w2 = w2, w1
			}
			pairs = append(pairs, w1+"\x00"+w2)
		}
	}
	return pairs
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

// fnvHash64Inline computes FNV-1a 64-bit hash inline for performance.
func fnvHash64Inline(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
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
