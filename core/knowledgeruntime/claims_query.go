package knowledgeruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/search"
)

type ClaimsKnowledgeSearchBackend interface {
	Search(ctx context.Context, req *search.SearchRequest) (*CommittedSearchResult, error)
}

type ClaimsKnowledgeQueryIndex struct {
	backend ClaimsKnowledgeSearchBackend
}

func NewClaimsKnowledgeQueryIndex(backend ClaimsKnowledgeSearchBackend) *ClaimsKnowledgeQueryIndex {
	return &ClaimsKnowledgeQueryIndex{backend: backend}
}

func (q *ClaimsKnowledgeQueryIndex) LookupCarryForwardEnrichment(ctx context.Context, query claims.RecallForwardEnrichmentQuery) ([]claims.RecallForwardEnrichment, error) {
	if q == nil || q.backend == nil {
		return nil, nil
	}
	limit := query.MaxItems
	if limit <= 0 {
		limit = 8
	}
	scopes := []struct {
		query      string
		pathFilter string
	}{
		{query: claimsKnowledgeSearchText(query), pathFilter: claimsKnowledgePathFilter(query)},
		{query: legacyKnowledgeSearchText(query), pathFilter: "archivalist/"},
	}
	out := make([]claims.RecallForwardEnrichment, 0, limit)
	seen := make(map[string]struct{})
	for i, scope := range scopes {
		if len(out) >= limit {
			break
		}
		if i > 0 && len(out) > 0 {
			break
		}
		req := &search.SearchRequest{
			Query:      scope.query,
			PathFilter: scope.pathFilter,
			Limit:      limit * 4,
		}
		if req.Limit < limit {
			req.Limit = limit
		}
		result, err := q.backend.Search(ctx, req)
		if err != nil {
			return nil, err
		}
		if result == nil || len(result.Hits) == 0 {
			continue
		}
		for _, hit := range result.Hits {
			if !claimsKnowledgeHitMatches(hit, query) {
				continue
			}
			key := hit.Document.ID + "\x1f" + hit.Document.Path
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			enrichment := claimsKnowledgeEnrichment(hit, query)
			out = append(out, enrichment)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func claimsKnowledgeSearchText(query claims.RecallForwardEnrichmentQuery) string {
	terms := []string{"claims_board"}
	for _, value := range []string{
		query.Topic,
		query.AgentID,
		query.ClaimID,
		query.TestamentID,
		query.EntityType,
		query.RelationType,
		query.RelationRelationship,
		query.RelatedID,
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			terms = append(terms, value)
		}
	}
	return strings.Join(terms, " ")
}

func legacyKnowledgeSearchText(query claims.RecallForwardEnrichmentQuery) string {
	terms := []string{"continuity", "scribe", "archivalist"}
	for _, value := range []string{
		query.Topic,
		query.AgentID,
		query.SessionID,
		query.BoardID,
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			terms = append(terms, value)
		}
	}
	return strings.Join(terms, " ")
}

func claimsKnowledgePathFilter(query claims.RecallForwardEnrichmentQuery) string {
	if strings.TrimSpace(query.SessionID) == "" {
		return "claims/"
	}
	if strings.TrimSpace(query.BoardID) == "" {
		return "claims/" + sanitizeClaimsKnowledgeSegment(query.SessionID)
	}
	return "claims/" + sanitizeClaimsKnowledgeSegment(query.SessionID) + "/" + sanitizeClaimsKnowledgeSegment(query.BoardID)
}

func claimsKnowledgeHitMatches(hit CommittedSearchHit, query claims.RecallForwardEnrichmentQuery) bool {
	content := strings.ToLower(hit.Document.Content)
	path := strings.ToLower(hit.Document.Path)
	meta := parseClaimsKnowledgeContentFields(hit.Document.Content)
	if value := strings.TrimSpace(query.SessionID); value != "" && !fieldOrContentMatches(meta, content, path, "session_id", value) {
		return false
	}
	if value := strings.TrimSpace(query.BoardID); value != "" && !fieldOrContentMatches(meta, content, path, "board_id", value) {
		return false
	}
	if value := strings.TrimSpace(query.ClaimID); value != "" && !fieldOrContentMatches(meta, content, path, "claim_id", value) {
		return false
	}
	if value := strings.TrimSpace(query.TestamentID); value != "" && !fieldOrContentMatches(meta, content, path, "testament_id", value) {
		return false
	}
	if value := strings.TrimSpace(query.EntityType); value != "" && strings.ToLower(strings.TrimSpace(meta["entity_type"])) != strings.ToLower(value) {
		return false
	}
	if value := strings.TrimSpace(query.AgentID); value != "" && !fieldOrContentMatches(meta, content, path, "agent_id", value) && !fieldOrContentMatches(meta, content, path, "continuity_agent_id", value) && !fieldOrContentMatches(meta, content, path, "parent_agent", value) {
		return false
	}
	if value := strings.TrimSpace(query.Topic); value != "" && !strings.Contains(content, strings.ToLower(value)) {
		return false
	}
	if !claimsKnowledgeRelationMatches(meta, query) {
		return false
	}
	return true
}

func fieldOrContentMatches(meta map[string]string, content, path, key, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return true
	}
	if strings.ToLower(strings.TrimSpace(meta[key])) == value {
		return true
	}
	return strings.Contains(content, value) || strings.Contains(path, sanitizeClaimsKnowledgeSegment(value))
}

func claimsKnowledgeEnrichment(hit CommittedSearchHit, query claims.RecallForwardEnrichmentQuery) claims.RecallForwardEnrichment {
	meta := parseClaimsKnowledgeContentFields(hit.Document.Content)
	entityType := firstNonEmptyString(meta["entity_type"], entityTypeFromClaimsPath(hit.Document.Path))
	entityID := firstNonEmptyString(meta["entity_id"], meta[entityType+"_id"])
	legacy := strings.HasPrefix(strings.ToLower(strings.TrimSpace(hit.Document.Path)), "archivalist/")
	exact := firstNonEmptyString(meta["claim_id"], query.ClaimID) != "" || firstNonEmptyString(meta["testament_id"], query.TestamentID) != "" || meta["artifact_id"] != ""
	source := "archivalist_claims_knowledge"
	if legacy {
		source = "archivalist_legacy_entry"
	}
	enrichment := claims.RecallForwardEnrichment{
		Source:             source,
		DocumentID:         hit.Document.ID,
		Path:               hit.Document.Path,
		EntityType:         entityType,
		EntityID:           entityID,
		ClaimID:            firstNonEmptyString(meta["claim_id"], query.ClaimID),
		TestamentID:        firstNonEmptyString(meta["testament_id"], query.TestamentID),
		ArtifactID:         meta["artifact_id"],
		SessionID:          firstNonEmptyString(meta["session_id"], query.SessionID),
		BoardID:            firstNonEmptyString(meta["board_id"], query.BoardID),
		AgentID:            firstNonEmptyString(meta["agent_id"], query.AgentID),
		Topic:              firstNonEmptyString(meta["continuity_topic"], meta["topic"], query.Topic),
		Score:              hit.Score,
		Summary:            summarizeClaimsKnowledgeContent(hit.Document.Content),
		EnrichedBy:         "claims_knowledge_query",
		GraphNodeID:        fmt.Sprint(hit.PrimaryNodeID),
		GraphNodeType:      hit.PrimaryNodeType,
		AgenticNarrative:   strings.EqualFold(meta["source_type"], "scribe") || strings.TrimSpace(meta["narration_type"]) != "",
		ArchivalistEntryID: meta["archivalist_entry_id"],
		Legacy:             legacy,
		ExactClaimSources:  exact,
	}
	if enrichment.GraphNodeID == "0" {
		enrichment.GraphNodeID = ""
	}
	return enrichment
}

func parseClaimsKnowledgeContentFields(content string) map[string]string {
	fields := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		before, after, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(before))
		value := strings.TrimSpace(after)
		if key == "" || value == "" {
			continue
		}
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return fields
}

func claimsKnowledgeRelationMatches(meta map[string]string, query claims.RecallForwardEnrichmentQuery) bool {
	relType := strings.ToLower(strings.TrimSpace(query.RelationType))
	relRelationship := strings.ToLower(strings.TrimSpace(query.RelationRelationship))
	relatedID := strings.ToLower(strings.TrimSpace(query.RelatedID))
	if relType == "" && relRelationship == "" && relatedID == "" {
		return true
	}
	for key, rawValue := range meta {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasPrefix(lowerKey, "relation_") && lowerKey != "relations_index" {
			continue
		}
		if lowerKey == "relations_index" {
			if relationIndexContains(rawValue, relType, relRelationship, relatedID) {
				return true
			}
			continue
		}
		if relType != "" && !strings.Contains(lowerKey, "relation_"+sanitizeClaimsKnowledgeSegment(relType)+"_") {
			continue
		}
		if relRelationship != "" && !strings.HasSuffix(lowerKey, "_"+sanitizeClaimsKnowledgeSegment(relRelationship)) {
			continue
		}
		if relatedID == "" || commaListContains(rawValue, relatedID) {
			return true
		}
	}
	return false
}

func relationIndexContains(rawValue, relType, relRelationship, relatedID string) bool {
	for _, part := range strings.Split(rawValue, ",") {
		pieces := strings.SplitN(strings.ToLower(strings.TrimSpace(part)), ":", 3)
		if len(pieces) != 3 {
			continue
		}
		if relType != "" && pieces[0] != relType {
			continue
		}
		if relRelationship != "" && pieces[1] != relRelationship {
			continue
		}
		if relatedID != "" && pieces[2] != relatedID {
			continue
		}
		return true
	}
	return false
}

func commaListContains(rawValue, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, part := range strings.Split(rawValue, ",") {
		if strings.ToLower(strings.TrimSpace(part)) == target {
			return true
		}
	}
	return false
}

func summarizeClaimsKnowledgeContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "entity_") || strings.Contains(line, ": ") {
			continue
		}
		if len(line) > 240 {
			return strings.TrimSpace(line[:237]) + "..."
		}
		return line
	}
	return ""
}

func entityTypeFromClaimsPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		if part == "claims" && i+3 < len(parts) {
			return parts[i+3]
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
