package archivalist

import (
	"math"
	"sort"
	"strings"
)

const (
	crossAgentIngestedKey = "cross_agent_ingested"
	originAgentIDKey      = "origin_agent_id"
	originAgentTypeKey    = "origin_agent_type"
	originSourceTypeKey   = "origin_source_type"
	ingestAgentIDKey      = "ingest_agent_id"
	ingestAgentNameKey    = "ingest_agent_name"
)

type crossAgentScope string

const (
	crossAgentScopeCode     crossAgentScope = "code"
	crossAgentScopeHistory  crossAgentScope = "history"
	crossAgentScopeAcademic crossAgentScope = "academic"
)

type boundedInfluencePolicy struct {
	scope       crossAgentScope
	trustWeight float64
	maxShare    float64
}

type prioritizedCrossAgentEntry struct {
	entry     *Entry
	keepScore float64
}

func (a *Archivalist) expandQueryForBoundedInfluence(query ArchiveQuery) ArchiveQuery {
	if query.CrossAgentPolicy == CrossAgentPolicyInclude || query.Limit <= 0 {
		return query
	}

	expanded := query
	expanded.Limit = expandedQueryLimit(query.Limit)
	return expanded
}

func (a *Archivalist) applyBoundedCrossAgentInfluence(query ArchiveQuery, entries []*Entry) []*Entry {
	if len(entries) == 0 || query.CrossAgentPolicy == CrossAgentPolicyInclude {
		return entries
	}

	policy := boundedPolicyForQuery(query)
	if policy.maxShare <= 0 {
		return entries
	}

	primaryCount := 0
	crossAgent := make([]prioritizedCrossAgentEntry, 0, len(entries))
	for idx, entry := range entries {
		if !isCrossAgentEntry(entry) {
			primaryCount++
			continue
		}
		crossAgent = append(crossAgent, prioritizedCrossAgentEntry{
			entry:     entry,
			keepScore: computeCrossAgentKeepScore(entry, idx, len(entries), policy, a.crossAgentWeights),
		})
	}

	if len(crossAgent) == 0 || primaryCount == 0 {
		return applyResultLimit(entries, query.Limit)
	}

	limit := query.Limit
	if limit <= 0 || limit > len(entries) {
		limit = len(entries)
	}

	maxCrossAgent := maxAllowedCrossAgentResults(limit, policy.maxShare)
	if maxCrossAgent >= len(crossAgent) {
		return applyResultLimit(entries, query.Limit)
	}

	sort.Slice(crossAgent, func(i, j int) bool {
		if crossAgent[i].keepScore == crossAgent[j].keepScore {
			return crossAgent[i].entry.CreatedAt.After(crossAgent[j].entry.CreatedAt)
		}
		return crossAgent[i].keepScore > crossAgent[j].keepScore
	})

	allowed := make(map[string]struct{}, maxCrossAgent)
	for i := 0; i < maxCrossAgent; i++ {
		allowed[crossAgent[i].entry.ID] = struct{}{}
	}

	balanced := make([]*Entry, 0, minInt(limit, len(entries)))
	for _, entry := range entries {
		if !isCrossAgentEntry(entry) {
			balanced = append(balanced, entry)
		} else if _, ok := allowed[entry.ID]; ok {
			balanced = append(balanced, entry)
		}

		if limit > 0 && len(balanced) >= limit {
			break
		}
	}

	return balanced
}

func boundedPolicyForQuery(query ArchiveQuery) boundedInfluencePolicy {
	scope := crossAgentScopeHistory
	trustWeight := 0.10

	switch {
	case isCodeWeightedQuery(query):
		scope = crossAgentScopeCode
		trustWeight = 0.05
	case isAcademicWeightedQuery(query):
		scope = crossAgentScopeAcademic
		trustWeight = 0.15
	}

	return boundedInfluencePolicy{
		scope:       scope,
		trustWeight: trustWeight,
		maxShare:    math.Min(0.35, trustWeight*2.0),
	}
}

func isCodeWeightedQuery(query ArchiveQuery) bool {
	if len(query.Categories) == 0 {
		return false
	}
	for _, category := range query.Categories {
		if category != CategoryCodeStyle && category != CategoryCodebaseMap {
			return false
		}
	}
	return true
}

func isAcademicWeightedQuery(query ArchiveQuery) bool {
	search := strings.ToLower(strings.TrimSpace(query.SearchText))
	if search == "" {
		return false
	}
	return strings.Contains(search, "research paper") ||
		strings.Contains(search, "proposal") ||
		strings.Contains(search, "architecture option")
}

func computeCrossAgentKeepScore(
	entry *Entry,
	index int,
	total int,
	policy boundedInfluencePolicy,
	weights *CrossAgentWeightManager,
) float64 {
	if entry == nil {
		return 0
	}

	rankComponent := 1.0
	if total > 0 {
		rankComponent = 1.0 - (float64(index) / float64(total))
	}

	trustComponent := 0.5
	if weights != nil {
		if agentType := originAgentType(entry); agentType != "" {
			trustComponent = weights.WeighResult(agentType)
		}
	}

	return (rankComponent * (1.0 - policy.trustWeight)) + (trustComponent * policy.trustWeight)
}

func maxAllowedCrossAgentResults(limit int, maxShare float64) int {
	if limit <= 0 {
		return 0
	}
	allowed := int(math.Ceil(float64(limit) * maxShare))
	if allowed < 1 {
		return 1
	}
	return allowed
}

func isCrossAgentEntry(entry *Entry) bool {
	if entry == nil || len(entry.Metadata) == 0 {
		return false
	}
	if value, ok := entry.Metadata[crossAgentIngestedKey].(bool); ok {
		return value
	}
	return originAgentType(entry) != ""
}

func originAgentType(entry *Entry) string {
	if entry == nil || len(entry.Metadata) == 0 {
		return ""
	}
	for _, key := range []string{originAgentTypeKey, "origin_agent", "parent_agent"} {
		if value, ok := metadataString(entry.Metadata, key); ok {
			value = strings.TrimSpace(strings.ToLower(value))
			if value != "" && value != "archivalist" {
				return value
			}
		}
	}
	return ""
}

func normalizeCrossAgentMetadata(
	metadata map[string]any,
	sourceAgentID string,
	sourceAgentName string,
) map[string]any {
	normalized := cloneEntryMetadata(metadata)
	if normalized == nil {
		normalized = make(map[string]any)
	}

	if strings.TrimSpace(sourceAgentID) != "" {
		normalized[ingestAgentIDKey] = strings.TrimSpace(sourceAgentID)
	}
	if strings.TrimSpace(sourceAgentName) != "" {
		normalized[ingestAgentNameKey] = strings.TrimSpace(sourceAgentName)
	}

	if origin, ok := metadataString(normalized, originAgentTypeKey); ok && strings.TrimSpace(origin) != "" {
		normalized[originAgentTypeKey] = strings.TrimSpace(strings.ToLower(origin))
	} else if origin, ok := metadataString(normalized, "origin_agent"); ok && strings.TrimSpace(origin) != "" {
		normalized[originAgentTypeKey] = strings.TrimSpace(strings.ToLower(origin))
	} else if parent, ok := metadataString(normalized, "parent_agent"); ok && strings.TrimSpace(parent) != "" {
		normalized[originAgentTypeKey] = strings.TrimSpace(strings.ToLower(parent))
	} else if strings.TrimSpace(sourceAgentName) != "" {
		normalized[originAgentTypeKey] = strings.TrimSpace(strings.ToLower(sourceAgentName))
	}

	if originID, ok := metadataString(normalized, originAgentIDKey); !ok || strings.TrimSpace(originID) == "" {
		if strings.TrimSpace(sourceAgentID) != "" {
			normalized[originAgentIDKey] = strings.TrimSpace(sourceAgentID)
		}
	}

	if sourceType, ok := metadataString(normalized, originSourceTypeKey); ok && strings.TrimSpace(sourceType) != "" {
		normalized[originSourceTypeKey] = strings.TrimSpace(sourceType)
	} else if sourceType, ok := metadataString(normalized, "source_type"); ok && strings.TrimSpace(sourceType) != "" {
		normalized[originSourceTypeKey] = strings.TrimSpace(sourceType)
	}

	if originType, ok := metadataString(normalized, originAgentTypeKey); ok && strings.TrimSpace(originType) != "" {
		if strings.TrimSpace(strings.ToLower(originType)) != "archivalist" {
			normalized[crossAgentIngestedKey] = true
		}
	}

	return normalized
}

func cloneEntryMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	clone := make(map[string]any, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

func metadataString(metadata map[string]any, key string) (string, bool) {
	if len(metadata) == 0 {
		return "", false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	default:
		return "", false
	}
}

func extractEntrySourceModel(metadata map[string]any) SourceModel {
	for _, key := range []string{"source_model", "model", "provider_model"} {
		value, ok := metadataString(metadata, key)
		if !ok {
			continue
		}
		source := SourceModel(strings.TrimSpace(value))
		if IsValidSource(source) || source == SourceModelUser || source == SourceModelArchivalist {
			return source
		}
	}
	return SourceModelClaudeOpus
}

func applyResultLimit(entries []*Entry, limit int) []*Entry {
	if limit > 0 && len(entries) > limit {
		return entries[:limit]
	}
	return entries
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func expandedQueryLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	expanded := limit * 3
	if expanded < limit+10 {
		expanded = limit + 10
	}
	if expanded > 300 {
		return 300
	}
	return expanded
}
