// Package context provides CV.4 ReferenceGenerator for the Context Virtualization System.
// ReferenceGenerator creates compact ContextReference objects from evicted ContentEntry items.
package context

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReferenceGenerator generates compact ContextReference objects from evicted content.
type ReferenceGenerator struct {
	contentStore *UniversalContentStore
}

// NewReferenceGenerator creates a new ReferenceGenerator with the given content store.
func NewReferenceGenerator(store *UniversalContentStore) *ReferenceGenerator {
	return &ReferenceGenerator{contentStore: store}
}

// ErrNoEntries is returned when GenerateReference is called with no entries.
var ErrNoEntries = errors.New("no entries to reference")

// GenerateReference creates a ContextReference from a set of ContentEntry items.
func (g *ReferenceGenerator) GenerateReference(entries []*ContentEntry) (*ContextReference, error) {
	if len(entries) == 0 {
		return nil, ErrNoEntries
	}

	contentIDs := g.collectContentIDs(entries)
	tokensSaved := g.calculateTokensSaved(entries)
	turnRange := g.determineTurnRange(entries)
	topics := g.extractTopics(entries)
	entities := g.extractEntities(entries)
	queryHints := g.generateQueryHints(topics, entities)
	refType := g.determineType(entries)
	summary := g.generateSummary(entries, topics)
	timestamp := g.determineTimestamp(entries)

	return g.buildReference(contentIDs, tokensSaved, turnRange, topics, entities, queryHints, refType, summary, timestamp), nil
}

func (g *ReferenceGenerator) collectContentIDs(entries []*ContentEntry) []string {
	contentIDs := make([]string, len(entries))
	for i, e := range entries {
		contentIDs[i] = e.ID
	}
	return contentIDs
}

func (g *ReferenceGenerator) calculateTokensSaved(entries []*ContentEntry) int {
	var tokensSaved int
	for _, e := range entries {
		tokensSaved += e.TokenCount
	}
	return tokensSaved
}

func (g *ReferenceGenerator) determineTurnRange(entries []*ContentEntry) [2]int {
	minTurn, maxTurn := entries[0].TurnNumber, entries[0].TurnNumber
	for _, e := range entries {
		minTurn, maxTurn = updateTurnBounds(minTurn, maxTurn, e.TurnNumber)
	}
	return [2]int{minTurn, maxTurn}
}

func updateTurnBounds(minTurn, maxTurn, turn int) (int, int) {
	if turn < minTurn {
		minTurn = turn
	}
	if turn > maxTurn {
		maxTurn = turn
	}
	return minTurn, maxTurn
}

func (g *ReferenceGenerator) determineTimestamp(entries []*ContentEntry) time.Time {
	earliest := entries[0].Timestamp
	for _, e := range entries {
		if e.Timestamp.Before(earliest) {
			earliest = e.Timestamp
		}
	}
	return earliest
}

func (g *ReferenceGenerator) buildReference(
	contentIDs []string,
	tokensSaved int,
	turnRange [2]int,
	topics, entities, queryHints []string,
	refType ReferenceType,
	summary string,
	timestamp time.Time,
) *ContextReference {
	return &ContextReference{
		ID:          uuid.New().String(),
		Type:        refType,
		ContentIDs:  contentIDs,
		Summary:     summary,
		TokensSaved: tokensSaved,
		TurnRange:   turnRange,
		Timestamp:   timestamp,
		Topics:      topics,
		Entities:    entities,
		QueryHints:  queryHints,
	}
}

// extractTopics aggregates keywords from all entries and returns top topics by frequency.
func (g *ReferenceGenerator) extractTopics(entries []*ContentEntry) []string {
	keywordCounts := g.aggregateKeywords(entries)
	sorted := g.sortKeywordsByFrequency(keywordCounts)
	return g.selectTopN(sorted, 5)
}

func (g *ReferenceGenerator) aggregateKeywords(entries []*ContentEntry) map[string]int {
	keywordCounts := make(map[string]int)
	for _, e := range entries {
		for _, kw := range e.Keywords {
			keywordCounts[strings.ToLower(kw)]++
		}
	}
	return keywordCounts
}

type keywordCount struct {
	keyword string
	count   int
}

func (g *ReferenceGenerator) sortKeywordsByFrequency(keywordCounts map[string]int) []keywordCount {
	sorted := make([]keywordCount, 0, len(keywordCounts))
	for kw, count := range keywordCounts {
		sorted = append(sorted, keywordCount{kw, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	return sorted
}

func (g *ReferenceGenerator) selectTopN(sorted []keywordCount, n int) []string {
	topics := make([]string, 0, n)
	for i := 0; i < len(sorted) && i < n; i++ {
		topics = append(topics, sorted[i].keyword)
	}
	return topics
}

// extractEntities aggregates entities from all entries and returns unique ones by frequency.
func (g *ReferenceGenerator) extractEntities(entries []*ContentEntry) []string {
	entityCounts := g.aggregateEntities(entries)
	sorted := g.sortEntitiesByFrequency(entityCounts)
	return g.selectTopEntities(sorted, 5)
}

func (g *ReferenceGenerator) aggregateEntities(entries []*ContentEntry) map[string]int {
	entityCounts := make(map[string]int)
	for _, e := range entries {
		for _, entity := range e.Entities {
			entityCounts[entity]++
		}
	}
	return entityCounts
}

type entityCount struct {
	entity string
	count  int
}

func (g *ReferenceGenerator) sortEntitiesByFrequency(entityCounts map[string]int) []entityCount {
	sorted := make([]entityCount, 0, len(entityCounts))
	for entity, count := range entityCounts {
		sorted = append(sorted, entityCount{entity, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	return sorted
}

func (g *ReferenceGenerator) selectTopEntities(sorted []entityCount, n int) []string {
	entities := make([]string, 0, n)
	for i := 0; i < len(sorted) && i < n; i++ {
		entities = append(entities, sorted[i].entity)
	}
	return entities
}

// generateQueryHints creates hints for retrieval based on topics and entities.
func (g *ReferenceGenerator) generateQueryHints(topics, entities []string) []string {
	hints := make([]string, 0, 5)
	hints = g.addTopicHints(hints, topics)
	hints = g.addEntityHints(hints, entities)
	return hints
}

func (g *ReferenceGenerator) addTopicHints(hints []string, topics []string) []string {
	topicCount := minInt(2, len(topics))
	for i := 0; i < topicCount; i++ {
		hints = append(hints, fmt.Sprintf("discussion about %s", topics[i]))
	}
	return hints
}

func (g *ReferenceGenerator) addEntityHints(hints []string, entities []string) []string {
	entityCount := minInt(2, len(entities))
	for i := 0; i < entityCount; i++ {
		hints = append(hints, fmt.Sprintf("mentions of %s", entities[i]))
	}
	return hints
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// determineType determines the reference type based on the dominant content type.
func (g *ReferenceGenerator) determineType(entries []*ContentEntry) ReferenceType {
	typeCounts := g.aggregateContentTypes(entries)
	maxType := g.findDominantType(typeCounts)
	return g.mapContentTypeToRefType(maxType)
}

func (g *ReferenceGenerator) aggregateContentTypes(entries []*ContentEntry) map[ContentType]int {
	typeCounts := make(map[ContentType]int)
	for _, e := range entries {
		typeCounts[e.ContentType]++
	}
	return typeCounts
}

func (g *ReferenceGenerator) findDominantType(typeCounts map[ContentType]int) ContentType {
	var maxType ContentType
	var maxCount int
	for t, c := range typeCounts {
		if c > maxCount {
			maxType = t
			maxCount = c
		}
	}
	return maxType
}

// contentTypeToRefType maps ContentType to ReferenceType.
var contentTypeToRefType = map[ContentType]ReferenceType{
	ContentTypeResearchPaper: RefTypeResearch,
	ContentTypeWebFetch:      RefTypeResearch,
	ContentTypeCodeFile:      RefTypeCodeAnalysis,
	ContentTypeToolCall:      RefTypeToolResults,
	ContentTypeToolResult:    RefTypeToolResults,
	ContentTypePlanWorkflow:  RefTypePlanDiscussion,
}

func (g *ReferenceGenerator) mapContentTypeToRefType(ct ContentType) ReferenceType {
	if refType, ok := contentTypeToRefType[ct]; ok {
		return refType
	}
	return RefTypeConversation
}

// generateSummary creates a one-line summary based on entries and topics.
func (g *ReferenceGenerator) generateSummary(entries []*ContentEntry, topics []string) string {
	if len(topics) == 0 {
		return g.generateFallbackSummary(len(entries))
	}
	return g.generateTopicSummary(topics)
}

func (g *ReferenceGenerator) generateFallbackSummary(entryCount int) string {
	return fmt.Sprintf("%d turns of conversation", entryCount)
}

func (g *ReferenceGenerator) generateTopicSummary(topics []string) string {
	topicCount := minInt(3, len(topics))
	return fmt.Sprintf("Discussion about %s", strings.Join(topics[:topicCount], ", "))
}
