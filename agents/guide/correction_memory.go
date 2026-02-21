package guide

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
)

const (
	defaultCorrectionStoreSize   = 512
	defaultMaxPromptCorrections  = 4
	defaultPromptSimilarityFloor = 0.28
)

type correctionMemory struct {
	mu     sync.RWMutex
	cache  *ristretto.Cache
	exact  map[string][]CorrectionRecord
	recent []CorrectionRecord
	limit  int
}

func newCorrectionMemory(maxCorrections int) *correctionMemory {
	limit := maxCorrections
	if limit <= 0 {
		limit = defaultCorrectionStoreSize
	}
	return &correctionMemory{
		cache:  newCorrectionCache(),
		exact:  map[string][]CorrectionRecord{},
		recent: make([]CorrectionRecord, 0, limit),
		limit:  limit,
	}
}

func newCorrectionCache() *ristretto.Cache {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e4,
		MaxCost:     1024,
		BufferItems: 64,
	})
	if err != nil {
		return nil
	}
	return cache
}

func (m *correctionMemory) add(correction CorrectionRecord) {
	if m == nil {
		return
	}
	key := correctionKey(correction.Input)
	if key == "" {
		return
	}
	m.mu.Lock()
	m.exact[key] = appendBounded(m.exact[key], correction, 8)
	m.recent = appendBounded(m.recent, correction, m.limit)
	m.mu.Unlock()
	m.cacheSet(promptCacheKey(key), m.selectExact(key, defaultMaxPromptCorrections))
}

func appendBounded[T any](values []T, value T, max int) []T {
	values = append(values, value)
	if max <= 0 || len(values) <= max {
		return values
	}
	return values[len(values)-max:]
}

func (m *correctionMemory) selectForPrompt(input string, max int) []CorrectionRecord {
	if m == nil {
		return nil
	}
	limit := normalizePromptLimit(max)
	key := correctionKey(input)
	if key == "" {
		return nil
	}
	if cached, ok := m.cachedPromptSelection(key); ok {
		return truncateCorrections(cached, limit)
	}
	exact := m.selectExact(key, limit)
	if len(exact) > 0 {
		m.cacheSet(promptCacheKey(key), exact)
		return exact
	}
	fuzzy := m.selectFuzzy(input, limit)
	if len(fuzzy) > 0 {
		m.cacheSet(promptCacheKey(key), fuzzy)
	}
	return fuzzy
}

func (m *correctionMemory) bestMatch(input string) *CorrectionRecord {
	if m == nil {
		return nil
	}
	key := correctionKey(input)
	if key == "" {
		return nil
	}
	exact := m.selectExact(key, 1)
	if len(exact) > 0 {
		return &exact[len(exact)-1]
	}
	fuzzy := m.selectFuzzy(input, 1)
	if len(fuzzy) == 0 {
		return nil
	}
	return &fuzzy[0]
}

func normalizePromptLimit(max int) int {
	if max <= 0 {
		return defaultMaxPromptCorrections
	}
	return max
}

func (m *correctionMemory) cachedPromptSelection(key string) ([]CorrectionRecord, bool) {
	value, ok := m.cacheGet(promptCacheKey(key))
	if !ok {
		return nil, false
	}
	records, ok := value.([]CorrectionRecord)
	if !ok || len(records) == 0 {
		return nil, false
	}
	return cloneCorrections(records), true
}

func (m *correctionMemory) selectExact(key string, max int) []CorrectionRecord {
	m.mu.RLock()
	records := cloneCorrections(m.exact[key])
	m.mu.RUnlock()
	return truncateCorrections(records, max)
}

func (m *correctionMemory) selectFuzzy(input string, max int) []CorrectionRecord {
	m.mu.RLock()
	records := cloneCorrections(m.recent)
	m.mu.RUnlock()
	scored := scoreCorrections(input, records)
	filtered := keepRelevantCorrections(scored, max)
	return extractCorrections(filtered)
}

func scoreCorrections(input string, records []CorrectionRecord) []scoredCorrection {
	candidates := make([]scoredCorrection, 0, len(records))
	for _, record := range records {
		score := correctionSimilarity(input, record.Input)
		if score < defaultPromptSimilarityFloor {
			continue
		}
		candidates = append(candidates, scoredCorrection{
			record: record,
			score:  score,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].record.CorrectedAt.After(candidates[j].record.CorrectedAt)
	})
	return candidates
}

func keepRelevantCorrections(scored []scoredCorrection, max int) []scoredCorrection {
	if len(scored) == 0 {
		return nil
	}
	limit := max
	if limit > len(scored) {
		limit = len(scored)
	}
	return scored[:limit]
}

func extractCorrections(scored []scoredCorrection) []CorrectionRecord {
	out := make([]CorrectionRecord, 0, len(scored))
	for _, candidate := range scored {
		out = append(out, candidate.record)
	}
	return out
}

type scoredCorrection struct {
	record CorrectionRecord
	score  float64
}

func correctionSimilarity(a string, b string) float64 {
	left := tokenSet(correctionKey(a))
	right := tokenSet(correctionKey(b))
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for token := range left {
		if right[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenSet(input string) map[string]bool {
	set := map[string]bool{}
	for _, token := range strings.Fields(strings.ToLower(input)) {
		if token == "" {
			continue
		}
		set[token] = true
	}
	return set
}

func truncateCorrections(records []CorrectionRecord, max int) []CorrectionRecord {
	if len(records) <= max {
		return records
	}
	return records[len(records)-max:]
}

func cloneCorrections(records []CorrectionRecord) []CorrectionRecord {
	if len(records) == 0 {
		return nil
	}
	out := make([]CorrectionRecord, len(records))
	copy(out, records)
	return out
}

func correctionKey(input string) string {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" {
		return ""
	}
	replacer := strings.NewReplacer("\n", " ", "\t", " ", ",", " ", ".", " ", ":", " ", ";", " ", "!", " ", "?", " ")
	return strings.Join(strings.Fields(replacer.Replace(normalized)), " ")
}

func promptCacheKey(input string) string {
	return "prompt:" + input
}

func (m *correctionMemory) cacheSet(key string, records []CorrectionRecord) {
	if m == nil || m.cache == nil || key == "" {
		return
	}
	_ = m.cache.Set(key, cloneCorrections(records), int64(len(records)+1))
	m.cache.Wait()
}

func (m *correctionMemory) cacheGet(key string) (any, bool) {
	if m == nil || m.cache == nil || key == "" {
		return nil, false
	}
	return m.cache.Get(key)
}

func formatCorrectionExamples(corrections []CorrectionRecord) string {
	if len(corrections) == 0 {
		return ""
	}
	var b strings.Builder
	for _, corr := range corrections {
		b.WriteString("Input: ")
		b.WriteString(strconvQuote(corr.Input))
		b.WriteString("\nWRONG: intent=")
		b.WriteString(string(corr.WrongIntent))
		b.WriteString(", domain=")
		b.WriteString(string(corr.WrongDomain))
		b.WriteString(", target=")
		b.WriteString(string(corr.WrongTarget))
		b.WriteString("\nCORRECT: intent=")
		b.WriteString(string(corr.CorrectIntent))
		b.WriteString(", domain=")
		b.WriteString(string(corr.CorrectDomain))
		b.WriteString(", target=")
		b.WriteString(string(corr.CorrectTarget))
		if strings.TrimSpace(corr.Reason) != "" {
			b.WriteString("\nReason: ")
			b.WriteString(corr.Reason)
		}
		if !corr.CorrectedAt.IsZero() {
			b.WriteString("\nCorrectedAt: ")
			b.WriteString(corr.CorrectedAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

func strconvQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
