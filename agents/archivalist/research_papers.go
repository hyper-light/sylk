package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

const (
	researchPaperKind           = "academic_research_paper"
	archivalistStorePaperAction = "store_research_paper"
)

type ResearchPaperRecord struct {
	EntryID             string    `json:"entry_id"`
	SessionID           string    `json:"session_id"`
	ResearchSlug        string    `json:"research_slug"`
	Version             int       `json:"version"`
	Title               string    `json:"title"`
	Abstract            string    `json:"abstract"`
	ProblemFraming      string    `json:"problem_framing,omitempty"`
	PaperPath           string    `json:"paper_path,omitempty"`
	Topics              []string  `json:"topics,omitempty"`
	Recommendations     []string  `json:"recommendations,omitempty"`
	OpenQuestions       []string  `json:"open_questions,omitempty"`
	RelatedTopics       []string  `json:"related_topics,omitempty"`
	ArchitectSummary    string    `json:"architect_summary,omitempty"`
	RecommendedOptionID string    `json:"recommended_option_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type ResearchLinkedEntry struct {
	ID       string   `json:"id"`
	Category Category `json:"category"`
	Title    string   `json:"title,omitempty"`
	Content  string   `json:"content"`
}

type ResearchPlanningBrief struct {
	Paper            *ResearchPaperRecord  `json:"paper,omitempty"`
	RelatedDecisions []ResearchLinkedEntry `json:"related_decisions,omitempty"`
	RelatedIssues    []ResearchLinkedEntry `json:"related_issues,omitempty"`
	RelatedInsights  []ResearchLinkedEntry `json:"related_insights,omitempty"`
}

type storeResearchPaperParams struct {
	ID                  string   `json:"id,omitempty"`
	SessionID           string   `json:"session_id,omitempty"`
	ResearchSlug        string   `json:"research_slug"`
	Version             int      `json:"version,omitempty"`
	Title               string   `json:"title"`
	Abstract            string   `json:"abstract"`
	ProblemFraming      string   `json:"problem_framing,omitempty"`
	PaperPath           string   `json:"paper_path,omitempty"`
	TopicsResearched    []string `json:"topics_researched,omitempty"`
	Recommendations     []string `json:"recommendations,omitempty"`
	OpenQuestions       []string `json:"open_questions,omitempty"`
	RelatedTopics       []string `json:"related_topics,omitempty"`
	ArchitectSummary    string   `json:"architect_summary,omitempty"`
	RecommendedOptionID string   `json:"recommended_option_id,omitempty"`
}

type queryResearchPaperParams struct {
	ResearchSlug       string `json:"research_slug,omitempty"`
	Version            int    `json:"version,omitempty"`
	SessionID          string `json:"session_id,omitempty"`
	Search             string `json:"search,omitempty"`
	Limit              int    `json:"limit,omitempty"`
	IncludeAllSessions bool   `json:"include_all_sessions,omitempty"`
}

type planningBriefParams struct {
	ResearchSlug       string `json:"research_slug,omitempty"`
	Version            int    `json:"version,omitempty"`
	SessionID          string `json:"session_id,omitempty"`
	Search             string `json:"search,omitempty"`
	IncludeAllSessions bool   `json:"include_all_sessions,omitempty"`
}

func storeResearchPaperSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("store_research_paper").
		Description("Persist an Academic research paper artifact as a first-class Archivalist entry.").
		Domain("chronicle").
		Keywords("research paper", "proposal", "academic", "store", "architect").
		Priority(75).
		StringParam("research_slug", "Stable research identifier", true).
		StringParam("title", "Paper title", true).
		StringParam("abstract", "Paper abstract", true).
		StringParam("session_id", "Session identifier", false).
		IntParam("version", "Paper version", false).
		StringParam("problem_framing", "Problem framing", false).
		StringParam("paper_path", "On-disk markdown path", false).
		ArrayParam("topics_researched", "Topics covered by the paper", "string", false).
		ArrayParam("recommendations", "Actionable recommendations", "string", false).
		ArrayParam("open_questions", "Questions to carry forward", "string", false).
		ArrayParam("related_topics", "Related or adjacent topics", "string", false).
		StringParam("architect_summary", "Planning-oriented summary", false).
		StringParam("recommended_option_id", "Preferred architecture option id", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params storeResearchPaperParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			result := a.StoreResearchPaper(ctx, &params)
			return result, result.Error
		}).
		Build()
}

func researchPapersSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("research_papers").
		Description("Query stored Academic research papers and proposals.").
		Domain("memory").
		Keywords("research paper", "proposal", "academic", "planning", "architect").
		Priority(70).
		StringParam("research_slug", "Stable research identifier", false).
		IntParam("version", "Specific paper version", false).
		StringParam("session_id", "Session identifier", false).
		StringParam("search", "Free-text search across stored papers", false).
		IntParam("limit", "Maximum number of results", false).
		BoolParam("include_all_sessions", "Search across all sessions", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params queryResearchPaperParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.QueryResearchPapers(ctx, params)
		}).
		Build()
}

func researchPlanningBriefSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("research_planning_brief").
		Description("Build a planning-oriented brief from a stored Academic research paper plus related archival context.").
		Domain("memory").
		Keywords("planning brief", "research paper", "proposal", "architect", "brief").
		Priority(72).
		StringParam("research_slug", "Stable research identifier", false).
		IntParam("version", "Specific paper version", false).
		StringParam("session_id", "Session identifier", false).
		StringParam("search", "Fallback search text when slug is unknown", false).
		BoolParam("include_all_sessions", "Search across all sessions", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planningBriefParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.BuildResearchPlanningBrief(ctx, params)
		}).
		Build()
}

func (a *Archivalist) StoreResearchPaper(ctx context.Context, params *storeResearchPaperParams) SubmissionResult {
	if params == nil {
		return SubmissionResult{Success: false, Error: fmt.Errorf("research paper payload is required")}
	}
	if strings.TrimSpace(params.ResearchSlug) == "" {
		return SubmissionResult{Success: false, Error: fmt.Errorf("research_slug is required")}
	}
	if strings.TrimSpace(params.Title) == "" {
		return SubmissionResult{Success: false, Error: fmt.Errorf("title is required")}
	}
	if strings.TrimSpace(params.Abstract) == "" {
		return SubmissionResult{Success: false, Error: fmt.Errorf("abstract is required")}
	}

	entry := &Entry{
		Category: CategoryInsight,
		Title:    strings.TrimSpace(params.Title),
		Content:  buildResearchPaperEntryContent(params),
		Source:   SourceModelArchivalist,
		Metadata: normalizeCrossAgentMetadata(map[string]any{
			"kind":                  researchPaperKind,
			"research_slug":         strings.TrimSpace(params.ResearchSlug),
			"version":               normalizeResearchPaperVersion(params.Version),
			"paper_path":            strings.TrimSpace(params.PaperPath),
			"abstract":              strings.TrimSpace(params.Abstract),
			"problem_framing":       strings.TrimSpace(params.ProblemFraming),
			"topics_researched":     filterResearchStrings(params.TopicsResearched),
			"recommendations":       filterResearchStrings(params.Recommendations),
			"open_questions":        filterResearchStrings(params.OpenQuestions),
			"related_topics":        filterResearchStrings(params.RelatedTopics),
			"architect_summary":     strings.TrimSpace(params.ArchitectSummary),
			"recommended_option_id": strings.TrimSpace(params.RecommendedOptionID),
			"origin_agent":          "academic",
		}, "", "academic"),
	}

	result := a.StoreEntry(ctx, entry)
	if result.Success && result.ID != "" && strings.TrimSpace(params.ID) != "" {
		_ = a.UpdateEntry(ctx, result.ID, func(existing *Entry) {
			if existing.Metadata == nil {
				existing.Metadata = map[string]any{}
			}
			existing.Metadata["paper_id"] = strings.TrimSpace(params.ID)
		})
	}
	return result
}

func (a *Archivalist) QueryResearchPapers(ctx context.Context, params queryResearchPaperParams) ([]ResearchPaperRecord, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	entries := a.QueryByCategory(ctx, CategoryInsight, researchMax(limit*4, 50))
	records := make([]ResearchPaperRecord, 0, len(entries))
	for _, entry := range entries {
		record, ok := researchPaperRecordFromEntry(entry)
		if !ok {
			continue
		}
		if !matchesResearchPaperQuery(record, params, a.defaultSessionID) {
			continue
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].ResearchSlug == records[j].ResearchSlug {
			return records[i].Version > records[j].Version
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (a *Archivalist) BuildResearchPlanningBrief(ctx context.Context, params planningBriefParams) (*ResearchPlanningBrief, error) {
	records, err := a.QueryResearchPapers(ctx, queryResearchPaperParams{
		ResearchSlug:       params.ResearchSlug,
		Version:            params.Version,
		SessionID:          params.SessionID,
		Search:             params.Search,
		Limit:              1,
		IncludeAllSessions: params.IncludeAllSessions,
	})
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no matching research papers found")
	}

	record := records[0]
	terms := buildResearchSearchTerms(record)

	return &ResearchPlanningBrief{
		Paper:            &record,
		RelatedDecisions: a.queryRelatedResearchEntries(ctx, record.SessionID, terms, CategoryDecision, 5, params.IncludeAllSessions),
		RelatedIssues:    a.queryRelatedResearchEntries(ctx, record.SessionID, terms, CategoryIssue, 5, params.IncludeAllSessions),
		RelatedInsights:  a.queryRelatedResearchEntries(ctx, record.SessionID, terms, CategoryInsight, 5, params.IncludeAllSessions),
	}, nil
}

func (a *Archivalist) handleStoreResearchPaperAction(req *guide.ActionRequest) error {
	if req == nil {
		return nil
	}
	payload, err := decodeStoreResearchPaperParams(req.Data)
	if err != nil {
		return a.publishActionFailure(req, err)
	}
	result := a.StoreResearchPaper(context.Background(), payload)
	if !result.Success {
		return a.publishActionFailure(req, result.Error)
	}
	if req.FireAndForget {
		return nil
	}
	return a.publishActionSuccess(req, map[string]any{
		"stored":        true,
		"entry_id":      result.ID,
		"research_slug": payload.ResearchSlug,
		"version":       normalizeResearchPaperVersion(payload.Version),
	})
}

func decodeStoreResearchPaperParams(data any) (*storeResearchPaperParams, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode research paper payload: %w", err)
	}
	var params storeResearchPaperParams
	if err := json.Unmarshal(encoded, &params); err != nil {
		return nil, fmt.Errorf("decode research paper payload: %w", err)
	}
	return &params, nil
}

func (a *Archivalist) publishActionSuccess(req *guide.ActionRequest, data any) error {
	if req == nil || req.FireAndForget {
		return nil
	}
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             true,
		Data:                data,
		RespondingAgentID:   a.id,
		RespondingAgentName: "archivalist",
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(guide.TopicResponses(req.SourceAgentID, req.SourceAgentID), msg)
}

func (a *Archivalist) publishActionFailure(req *guide.ActionRequest, err error) error {
	if req == nil || req.FireAndForget || err == nil {
		return nil
	}
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             false,
		Error:               err.Error(),
		RespondingAgentID:   a.id,
		RespondingAgentName: "archivalist",
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(guide.TopicResponses(req.SourceAgentID, req.SourceAgentID), msg)
}

func researchPaperRecordFromEntry(entry *Entry) (ResearchPaperRecord, bool) {
	if entry == nil || entry.Metadata == nil {
		return ResearchPaperRecord{}, false
	}
	if kind, _ := entry.Metadata["kind"].(string); kind != researchPaperKind {
		return ResearchPaperRecord{}, false
	}
	record := ResearchPaperRecord{
		EntryID:             entry.ID,
		SessionID:           entry.SessionID,
		ResearchSlug:        researchMetadataString(entry.Metadata, "research_slug"),
		Version:             metadataInt(entry.Metadata, "version"),
		Title:               firstNonEmpty(entry.Title, researchMetadataString(entry.Metadata, "title")),
		Abstract:            researchMetadataString(entry.Metadata, "abstract"),
		ProblemFraming:      researchMetadataString(entry.Metadata, "problem_framing"),
		PaperPath:           researchMetadataString(entry.Metadata, "paper_path"),
		Topics:              metadataStrings(entry.Metadata, "topics_researched"),
		Recommendations:     metadataStrings(entry.Metadata, "recommendations"),
		OpenQuestions:       metadataStrings(entry.Metadata, "open_questions"),
		RelatedTopics:       metadataStrings(entry.Metadata, "related_topics"),
		ArchitectSummary:    researchMetadataString(entry.Metadata, "architect_summary"),
		RecommendedOptionID: researchMetadataString(entry.Metadata, "recommended_option_id"),
		CreatedAt:           entry.CreatedAt,
	}
	if record.Title == "" {
		record.Title = entry.Title
	}
	if record.Abstract == "" {
		record.Abstract = entry.Content
	}
	return record, true
}

func matchesResearchPaperQuery(record ResearchPaperRecord, params queryResearchPaperParams, defaultSessionID string) bool {
	if strings.TrimSpace(params.ResearchSlug) != "" && !strings.EqualFold(record.ResearchSlug, strings.TrimSpace(params.ResearchSlug)) {
		return false
	}
	if params.Version > 0 && record.Version != params.Version {
		return false
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" && !params.IncludeAllSessions {
		sessionID = defaultSessionID
	}
	if sessionID != "" && record.SessionID != sessionID {
		return false
	}
	search := strings.ToLower(strings.TrimSpace(params.Search))
	if search == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		record.ResearchSlug,
		record.Title,
		record.Abstract,
		record.ProblemFraming,
		record.ArchitectSummary,
		strings.Join(record.Topics, " "),
		strings.Join(record.Recommendations, " "),
	}, " "))
	return strings.Contains(haystack, search)
}

func buildResearchPaperEntryContent(params *storeResearchPaperParams) string {
	parts := []string{
		strings.TrimSpace(params.Abstract),
		strings.TrimSpace(params.ArchitectSummary),
		strings.TrimSpace(params.ProblemFraming),
		strings.Join(filterResearchStrings(params.Recommendations), "\n"),
		strings.Join(filterResearchStrings(params.OpenQuestions), "\n"),
	}
	return strings.TrimSpace(strings.Join(filterResearchStrings(parts), "\n\n"))
}

func buildResearchSearchTerms(record ResearchPaperRecord) []string {
	return filterResearchStrings(append([]string{
		record.ResearchSlug,
		record.Title,
		record.Abstract,
		record.ArchitectSummary,
	}, append(record.Topics, record.RelatedTopics...)...))
}

func (a *Archivalist) queryRelatedResearchEntries(
	ctx context.Context,
	sessionID string,
	terms []string,
	category Category,
	limit int,
	includeAllSessions bool,
) []ResearchLinkedEntry {
	searchText := strings.Join(terms[:researchMin(len(terms), 4)], " ")
	query := ArchiveQuery{
		Categories:       []Category{category},
		SearchText:       searchText,
		Limit:            researchMax(limit*2, limit),
		CrossAgentPolicy: CrossAgentPolicyInclude,
	}
	if !includeAllSessions && strings.TrimSpace(sessionID) != "" {
		query.SessionIDs = []string{sessionID}
	}
	entries, err := a.Query(ctx, query)
	if err != nil {
		return nil
	}
	result := make([]ResearchLinkedEntry, 0, limit)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.Metadata != nil {
			if kind, _ := entry.Metadata["kind"].(string); kind == researchPaperKind {
				continue
			}
		}
		result = append(result, ResearchLinkedEntry{
			ID:       entry.ID,
			Category: entry.Category,
			Title:    entry.Title,
			Content:  entry.Content,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func normalizeResearchPaperVersion(version int) int {
	if version <= 0 {
		return 1
	}
	return version
}

func filterResearchStrings(items []string) []string {
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func researchMetadataString(metadata map[string]any, key string) string {
	value, ok := metadataString(metadata, key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func metadataStrings(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return filterResearchStrings(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
		return filterResearchStrings(values)
	default:
		return nil
	}
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func researchMin(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func researchMax(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
