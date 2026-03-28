package forest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

var whitespaceRE = regexp.MustCompile(`\s+`)

func stableID(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeText(input string) string {
	normalized := strings.TrimSpace(strings.ToLower(input))
	if normalized == "" {
		return ""
	}
	return whitespaceRE.ReplaceAllString(normalized, " ")
}

func summarizeText(input string, maxLen int) string {
	text := strings.TrimSpace(input)
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	text = text[:maxLen]
	if idx := strings.LastIndexAny(text, ". \n"); idx > maxLen/2 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}

func defaultConfidence(value float64) float64 {
	if value > 0 {
		return clamp01(value)
	}
	return 0.65
}

func defaultSalience(value float64) float64 {
	if value > 0 {
		return clamp01(value)
	}
	return 0.5
}

func clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func sigmoid(value float64) float64 {
	if value >= 8 {
		return 1
	}
	if value <= -8 {
		return 0
	}
	return 1 / (1 + math.Exp(-value))
}

func recencyScore(last time.Time, now time.Time, halfLife time.Duration) float64 {
	if last.IsZero() {
		return 0
	}
	if halfLife <= 0 {
		halfLife = 72 * time.Hour
	}
	age := now.Sub(last)
	if age <= 0 {
		return 1
	}
	return math.Exp(-math.Ln2 * age.Seconds() / halfLife.Seconds())
}

func marshalJSON(value any) string {
	if value == nil {
		return ""
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(payload)
}

func unmarshalJSON[T any](raw string, dst *T) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), dst)
}

func tokenSet(text string) map[string]struct{} {
	normalized := normalizeText(text)
	if normalized == "" {
		return nil
	}
	parts := strings.Fields(normalized)
	set := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		set[part] = struct{}{}
	}
	return set
}

func lexicalScore(query, text string) float64 {
	querySet := tokenSet(query)
	textSet := tokenSet(text)
	if len(querySet) == 0 || len(textSet) == 0 {
		return 0
	}

	var overlap float64
	for token := range querySet {
		if _, ok := textSet[token]; ok {
			overlap++
		}
	}
	denominator := float64(len(querySet) + len(textSet))
	if denominator == 0 {
		return 0
	}
	return (2 * overlap) / denominator
}

func dedupeStrings(values []string) []string {
	if len(values) <= 1 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sortPackets(packets []*BranchPacket) {
	sort.SliceStable(packets, func(i, j int) bool {
		return packets[i].Score.Total > packets[j].Score.Total
	})
}

func canopyKey(horizon CanopyHorizon, sessionID, intentID string) string {
	return fmt.Sprintf("%s:%s:%s", horizon, sessionID, intentID)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func metadataFloat(metadata map[string]any, key string) float64 {
	if metadata == nil {
		return 0
	}
	raw, ok := metadata[key]
	if !ok {
		return 0
	}
	switch value := raw.(type) {
	case float64:
		return clamp01(value)
	case float32:
		return clamp01(float64(value))
	case int:
		return clamp01(float64(value))
	case int64:
		return clamp01(float64(value))
	default:
		return 0
	}
}
