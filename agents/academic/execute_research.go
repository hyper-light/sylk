package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

var errAcademicResearchPaperMissingGroundedSources = errors.New("the academic research run gathered no grounded sources for research paper synthesis")

type architectResearchProtocolParams struct {
	Query              string
	Domain             string
	SessionID          string
	Context            string
	Constraints        []string
	Invariants         []string
	OpenQuestions      []string
	RelatedTopics      []string
	StoreInArchivalist bool
}

type academicResearchExecutionState struct {
	mu                 sync.RWMutex
	sessionID          string
	consultAttempts    map[string]struct{}
	searchAttempts     map[string]researchExecutionSearch
	observedSearches   map[string]struct{}
	discoveredResults  map[string]researchExecutionDiscoveredResult
	searchEpisodes     []researchExecutionSearchEpisode
	searchEpisodeByID  map[string]int
	searchEpisodeURLs  map[string][]int
	authorityDomains   map[string]struct{}
	sources            []researchExecutionSource
	sourceIDsByURL     map[string]string
	librarianEvidence  *shared.ConsultationEvidence
	archivalEvidence   *shared.ConsultationEvidence
	paperOutput        map[string]any
	sawNativeSearch    bool
	sawDiscoverySearch bool
	repeatedSearch     *researchExecutionSearch
	requirePaper       bool
}

type academicResearchExecutionStateKey struct{}

type researchExecutionSource struct {
	ID          string
	URL         string
	Title       string
	Summary     string
	ContentHash string
	WordCount   int
	Ingested    bool
	Grounded    bool
	Persisted   bool
	Persistence string
	JobID       string
	Type        SourceType
	Quality     float64
}

type researchExecutionSearch struct {
	Fingerprint           string
	Query                 string
	URL                   string
	Count                 int
	GroundedSourceCount   int
	RepeatedWithoutGround bool
}

type researchExecutionDiscoveredResult struct {
	URL      string
	Title    string
	Query    string
	Provider string
	Source   string
	PageAge  string
}

type researchExecutionSearchEpisode struct {
	Ordinal          int
	SearchID         string
	Fingerprint      string
	Query            string
	Action           string
	URL              string
	Repeated         bool
	NovelResultCount int
	NovelDomainCount int
	GroundedCount    int
}

func WithAcademicResearchExecutionState(ctx context.Context, state *academicResearchExecutionState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, academicResearchExecutionStateKey{}, state)
}

func academicResearchExecutionStateFromContext(ctx context.Context) *academicResearchExecutionState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(academicResearchExecutionStateKey{}).(*academicResearchExecutionState)
	return state
}

func newAcademicResearchExecutionState(sessionID string) *academicResearchExecutionState {
	return &academicResearchExecutionState{
		sessionID:         strings.TrimSpace(sessionID),
		consultAttempts:   make(map[string]struct{}),
		searchAttempts:    make(map[string]researchExecutionSearch),
		observedSearches:  make(map[string]struct{}),
		discoveredResults: make(map[string]researchExecutionDiscoveredResult),
		searchEpisodeByID: make(map[string]int),
		searchEpisodeURLs: make(map[string][]int),
		authorityDomains:  make(map[string]struct{}),
		sourceIDsByURL:    make(map[string]string),
	}
}

func (s *academicResearchExecutionState) setResearchPaperRequired(required bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requirePaper = required
}

func (s *academicResearchExecutionState) recordConsultAttempt(target, query, scope string) error {
	if s == nil {
		return nil
	}
	fingerprint := normalizedAcademicConsultFingerprint(target, query, scope)
	if fingerprint == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.consultAttempts[fingerprint]; exists {
		return fmt.Errorf("this academic research run forbids repeating the same consultation question to %s; gather different evidence or synthesize what you already have", strings.TrimSpace(target))
	}
	s.consultAttempts[fingerprint] = struct{}{}
	return nil
}

func normalizedAcademicConsultFingerprint(target, query, scope string) string {
	fingerprint, _ := normalizeSearchQuery(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(target)),
		strings.TrimSpace(query),
		strings.TrimSpace(scope),
	}, " "))
	return fingerprint
}

func (s *academicResearchExecutionState) observeToolResult(ctx context.Context, a *Academic, call providers.ToolCall, result string, isError bool) {
	if s == nil || a == nil || isError {
		return
	}
	switch strings.TrimSpace(call.Name) {
	case "ground_source", "web_fetch", "fetch_document", "crawl_links":
		s.recordFetchResult(ctx, a, result)
	case "consult", "clone_via_librarian":
		s.recordConsultationResult(ctx, a, call, result)
	case "author_research_paper":
		s.recordPaperOutput(ctx, result)
	default:
		if strings.HasPrefix(strings.TrimSpace(call.Name), "consult_") {
			s.recordConsultationResult(ctx, a, call, result)
		}
	}
}

func (s *academicResearchExecutionState) observeProviderResponse(ctx context.Context, a *Academic, resp *providers.Response) {
	if s == nil || resp == nil {
		return
	}
	for _, call := range providers.DecodeNativeWebSearchCalls(resp.ProviderMetadata) {
		s.observeNativeSearchCall(ctx, a, call)
	}
	for _, result := range providers.DecodeNativeWebSearchResults(resp.ProviderMetadata) {
		s.observeNativeSearchResult(ctx, result)
	}
}

func (s *academicResearchExecutionState) observeNativeSearchCall(ctx context.Context, a *Academic, call providers.NativeWebSearchCall) {
	if s == nil {
		return
	}
	query := strings.TrimSpace(call.Query)
	action := strings.TrimSpace(call.Action)
	rawURL := strings.TrimSpace(call.URL)
	if query == "" && action == "" && rawURL == "" {
		return
	}

	fingerprintInput := firstNonEmpty(query, action+" "+rawURL)
	if nativeWebSearchActionResultSource(action) != "" && rawURL != "" {
		fingerprintInput = action + " " + rawURL
	}
	fingerprint, _ := normalizeSearchQuery(fingerprintInput)
	if fingerprint == "" {
		fingerprint = strings.ToLower(strings.TrimSpace(action + " " + rawURL))
	}
	if fingerprint == "" {
		return
	}

	s.mu.Lock()
	searchKey := strings.TrimSpace(call.ID)
	if searchKey == "" {
		searchKey = call.ArgumentsJSON()
	}
	if _, seen := s.observedSearches[searchKey]; seen {
		s.mu.Unlock()
		return
	}
	s.observedSearches[searchKey] = struct{}{}
	groundedCount := s.groundedSourceCountLocked()
	attempt := s.searchAttempts[fingerprint]
	attempt.Fingerprint = fingerprint
	if attempt.Query == "" {
		attempt.Query = query
	}
	if attempt.URL == "" {
		attempt.URL = rawURL
	}
	if attempt.Count > 0 && groundedCount <= attempt.GroundedSourceCount {
		attempt.RepeatedWithoutGround = true
		copyAttempt := attempt
		copyAttempt.Count++
		copyAttempt.GroundedSourceCount = groundedCount
		s.repeatedSearch = &copyAttempt
	}
	attempt.Count++
	attempt.GroundedSourceCount = groundedCount
	s.searchAttempts[fingerprint] = attempt
	episode := researchExecutionSearchEpisode{
		Ordinal:     len(s.searchEpisodes) + 1,
		SearchID:    searchKey,
		Fingerprint: fingerprint,
		Query:       query,
		Action:      action,
		URL:         rawURL,
		Repeated:    attempt.RepeatedWithoutGround,
	}
	s.searchEpisodes = append(s.searchEpisodes, episode)
	s.searchEpisodeByID[searchKey] = len(s.searchEpisodes) - 1
	if rawURL != "" {
		s.searchEpisodeURLs[rawURL] = appendUniqueInt(s.searchEpisodeURLs[rawURL], len(s.searchEpisodes)-1)
	}
	s.sawNativeSearch = true
	if strings.EqualFold(action, "search") || (query != "" && rawURL == "") {
		s.sawDiscoverySearch = true
	}
	s.mu.Unlock()

	academicLogResearchStateEvent(ctx, "native_search_observed", map[string]any{
		"query":                   query,
		"action":                  action,
		"url":                     rawURL,
		"search_fingerprint":      fingerprint,
		"search_count":            attempt.Count,
		"grounded_source_count":   groundedCount,
		"repeated_without_ground": attempt.RepeatedWithoutGround,
	})

	if source := nativeWebSearchActionResultSource(action); source != "" && rawURL != "" {
		s.observeNativeSearchResult(ctx, providers.NativeWebSearchResult{
			SearchID: searchKey,
			Provider: strings.TrimSpace(call.Provider),
			Source:   source,
			Query:    query,
			URL:      rawURL,
		})
	}
}

func (s *academicResearchExecutionState) observeNativeSearchResult(ctx context.Context, result providers.NativeWebSearchResult) {
	if s == nil {
		return
	}
	rawURL := strings.TrimSpace(result.URL)
	if rawURL == "" {
		return
	}

	discovered := researchExecutionDiscoveredResult{
		URL:      rawURL,
		Title:    strings.TrimSpace(result.Title),
		Query:    strings.TrimSpace(result.Query),
		Provider: strings.TrimSpace(result.Provider),
		Source:   strings.TrimSpace(result.Source),
		PageAge:  strings.TrimSpace(result.PageAge),
	}

	s.mu.Lock()
	idx := -1
	idx, ok := s.searchEpisodeByID[strings.TrimSpace(result.SearchID)]
	if !ok && len(s.searchEpisodes) > 0 {
		idx = len(s.searchEpisodes) - 1
	}
	existing, alreadyKnown := s.discoveredResults[rawURL]
	s.discoveredResults[rawURL] = mergeResearchExecutionDiscoveredResult(existing, discovered)
	s.reserveSourceIDForURLLocked(rawURL)
	grounded := s.hasGroundedURLLocked(rawURL)
	if idx >= 0 && idx < len(s.searchEpisodes) {
		if !alreadyKnown {
			s.searchEpisodes[idx].NovelResultCount++
		}
		s.searchEpisodeURLs[rawURL] = appendUniqueInt(s.searchEpisodeURLs[rawURL], idx)
	}
	if domain := authorityDomainFromURL(rawURL); domain != "" {
		if _, seen := s.authorityDomains[domain]; !seen {
			s.authorityDomains[domain] = struct{}{}
			if idx >= 0 && idx < len(s.searchEpisodes) {
				s.searchEpisodes[idx].NovelDomainCount++
			}
		}
	}
	s.sawNativeSearch = true
	s.sawDiscoverySearch = true
	discoveredCount := len(s.discoveredResults)
	s.mu.Unlock()

	academicLogResearchStateEvent(ctx, "native_search_result_observed", map[string]any{
		"url":                     rawURL,
		"title":                   discovered.Title,
		"query":                   discovered.Query,
		"provider":                discovered.Provider,
		"source":                  discovered.Source,
		"page_age":                discovered.PageAge,
		"already_grounded":        grounded,
		"discovered_result_count": discoveredCount,
	})
}

func mergeResearchExecutionDiscoveredResult(existing, incoming researchExecutionDiscoveredResult) researchExecutionDiscoveredResult {
	existing.URL = firstNonEmpty(existing.URL, incoming.URL)
	existing.Title = firstNonEmpty(existing.Title, incoming.Title)
	existing.Query = firstNonEmpty(existing.Query, incoming.Query)
	existing.Provider = firstNonEmpty(existing.Provider, incoming.Provider)
	existing.PageAge = firstNonEmpty(existing.PageAge, incoming.PageAge)
	if strings.TrimSpace(existing.Source) == "" || strings.EqualFold(strings.TrimSpace(existing.Source), "url_citation") {
		existing.Source = firstNonEmpty(incoming.Source, existing.Source)
	}
	return existing
}

func nativeWebSearchActionResultSource(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "open_page":
		return "open_page"
	case "find_in_page":
		return "find_in_page"
	default:
		return ""
	}
}

func (s *academicResearchExecutionState) recordFetchResult(ctx context.Context, a *Academic, result string) {
	payload := parseResearchJSONPayload(result)
	if !payloadSuccess(payload) {
		return
	}
	rawURL := strings.TrimSpace(stringValue(payload["url"]))
	if rawURL == "" {
		return
	}
	title := firstNonEmpty(
		stringValue(payload["title"]),
		stringValue(payload["site"]),
		rawURL,
	)
	summary := firstNonEmpty(
		stringValue(payload["text_preview"]),
		stringValue(payload["content"]),
		stringValue(payload["description"]),
		title,
	)
	now := time.Now().UTC()
	sourceType := sourceTypeFromURL(rawURL)
	sourceQuality := sourceQualityFromPayload(rawURL, payload)
	sourceMetadata := map[string]any{
		"grounded":           boolValue(payload["grounded"]),
		"ingested":           boolValue(payload["ingested"]),
		"content_hash":       stringValue(payload["content_hash"]),
		"persistence_status": stringValue(payload["persistence_status"]),
		"persistence_job_id": stringValue(payload["persistence_job_id"]),
	}
	sourceRecord := researchExecutionSource{
		URL:         rawURL,
		Title:       title,
		Summary:     truncateStr(strings.TrimSpace(summary), 500),
		ContentHash: stringValue(payload["content_hash"]),
		WordCount:   intValue(payload["word_count"]),
		Ingested:    boolValue(payload["ingested"]),
		Grounded:    boolValue(payload["grounded"]),
		Persisted:   boolValue(payload["ingested"]),
		Persistence: stringValue(payload["persistence_status"]),
		JobID:       stringValue(payload["persistence_job_id"]),
		Type:        sourceType,
		Quality:     sourceQuality,
	}
	source := &Source{
		Type:        sourceType,
		URL:         rawURL,
		Title:       title,
		Description: truncateStr(strings.TrimSpace(summary), 280),
		UpdatedAt:   now,
		TokenCount:  intValue(payload["word_count"]),
		Quality:     sourceQuality,
		Metadata:    sourceMetadata,
	}
	if boolValue(payload["ingested"]) {
		source.IngestedAt = now
	}

	s.mu.Lock()
	effectiveID := s.reserveSourceIDForURLLocked(rawURL)
	sourceRecord.ID = effectiveID
	source.ID = effectiveID

	updated := false
	for i := range s.sources {
		if s.sources[i].ID != effectiveID {
			continue
		}
		s.sources[i] = sourceRecord
		updated = true
		break
	}
	if !updated {
		s.sources = append(s.sources, sourceRecord)
	}
	if boolValue(payload["grounded"]) {
		for _, idx := range s.searchEpisodeURLs[rawURL] {
			if idx < 0 || idx >= len(s.searchEpisodes) {
				continue
			}
			if s.searchEpisodes[idx].GroundedCount == 0 {
				s.searchEpisodes[idx].GroundedCount++
			}
		}
	}
	s.mu.Unlock()

	if a != nil {
		a.upsertResearchSource(source)
	}

	eventName := "grounded_source_recorded"
	if updated {
		eventName = "grounded_source_updated"
	}
	academicLogResearchStateEvent(ctx, eventName, map[string]any{
		"url":                rawURL,
		"title":              title,
		"word_count":         intValue(payload["word_count"]),
		"ingested":           boolValue(payload["ingested"]),
		"persistence_status": stringValue(payload["persistence_status"]),
		"persistence_job_id": stringValue(payload["persistence_job_id"]),
		"source_type":        source.Type,
	})

	// Source grounding testament.
	if a != nil {
		a.academicSubmitTestament(ctx, a.academicTestament(
			"Source grounded: "+truncateAcademic(title, 80),
			"committed",
			[]*claims.Artifact{
				a.academicArtifact("url", rawURL),
				a.academicArtifact("title", title),
				a.academicArtifact("word_count", fmt.Sprintf("%d", intValue(payload["word_count"]))),
				a.academicArtifact("source_type", string(source.Type)),
				a.academicArtifact("grounded", fmt.Sprintf("%t", boolValue(payload["grounded"]))),
			},
		))
	}
}

func (s *academicResearchExecutionState) recordConsultationResult(ctx context.Context, a *Academic, call providers.ToolCall, result string) {
	payload := parseResearchJSONPayload(result)
	if !payloadSuccess(payload) {
		return
	}
	target := consultationTarget(call, strings.TrimSpace(call.Name), result)
	if target == "" {
		return
	}
	scope := extractStringField(call.Arguments, "scope")
	query := extractStringField(call.Arguments, "query")
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Success:     true,
		RequestedAt: time.Now().UTC(),
		ReceivedAt:  time.Now().UTC(),
		Data:        payload["data"],
		Error:       stringValue(payload["error"]),
	}
	s.mu.Lock()
	switch target {
	case "librarian":
		s.librarianEvidence = evidence
	case "archivalist":
		s.archivalEvidence = evidence
	}
	s.mu.Unlock()

	academicLogResearchStateEvent(ctx, "consultation_recorded", map[string]any{
		"target": target,
		"query":  query,
		"scope":  scope,
	})

	// Peer consultation testament.
	if a != nil {
		a.academicSubmitTestament(ctx, a.academicTestamentWithSubject(
			"Consulted "+target+": "+truncateAcademic(query, 80),
			"committed", target,
			[]*claims.Artifact{
				a.academicArtifact("target", target),
				a.academicArtifact("query", truncateAcademic(query, 200)),
				a.academicArtifact("success", "true"),
			},
		))
	}
}

func (s *academicResearchExecutionState) recordPaperOutput(ctx context.Context, result string) {
	payload := parseResearchJSONPayload(result)
	if len(payload) == 0 {
		return
	}
	if strings.TrimSpace(stringValue(payload["paper_id"])) == "" &&
		strings.TrimSpace(stringValue(payload["paper_path"])) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paperOutput = cloneStringAnyMap(payload)
	academicLogResearchStateEvent(ctx, "research_paper_recorded", map[string]any{
		"paper_id":   stringValue(payload["paper_id"]),
		"paper_path": stringValue(payload["paper_path"]),
		"title":      stringValue(payload["title"]),
	})
}

func (s *academicResearchExecutionState) builtPaperOutput() map[string]any {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStringAnyMap(s.paperOutput)
}

func (s *academicResearchExecutionState) sourceIDs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.sources))
	for _, source := range s.sources {
		if source.Grounded && strings.TrimSpace(source.ID) != "" {
			out = append(out, source.ID)
		}
	}
	return out
}

func (s *academicResearchExecutionState) sourceURLs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.sources))
	for _, source := range s.sources {
		if source.Grounded && strings.TrimSpace(source.URL) != "" {
			out = append(out, source.URL)
		}
	}
	return out
}

func (s *academicResearchExecutionState) consultedAgents() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, 2)
	if s.librarianEvidence != nil {
		out = append(out, "librarian")
	}
	if s.archivalEvidence != nil {
		out = append(out, "archivalist")
	}
	return out
}

func (s *academicResearchExecutionState) hasGroundedSources() bool {
	return len(s.sourceIDs()) > 0
}

func (s *academicResearchExecutionState) candidateGroundingURLs(limit int) []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.candidateGroundingURLsLocked(limit)
}

func (s *academicResearchExecutionState) candidateGroundingURLsLocked(limit int) []string {
	seen := make(map[string]struct{}, len(s.discoveredResults))
	urls := make([]string, 0, len(s.discoveredResults))
	for _, discovered := range s.discoveredResults {
		rawURL := strings.TrimSpace(discovered.URL)
		if rawURL == "" {
			continue
		}
		if s.hasGroundedURLLocked(rawURL) {
			continue
		}
		if _, exists := seen[rawURL]; exists {
			continue
		}
		seen[rawURL] = struct{}{}
		urls = append(urls, rawURL)
	}
	sort.Strings(urls)
	if limit > 0 && len(urls) > limit {
		urls = urls[:limit]
	}
	return urls
}

func (s *academicResearchExecutionState) bestGroundingCandidate(excluded map[string]struct{}) (researchExecutionDiscoveredResult, bool) {
	if s == nil {
		return researchExecutionDiscoveredResult{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bestGroundingCandidateLocked(excluded)
}

func (s *academicResearchExecutionState) bestGroundingCandidateLocked(excluded map[string]struct{}) (researchExecutionDiscoveredResult, bool) {
	candidates := make([]researchExecutionDiscoveredResult, 0, len(s.discoveredResults))
	for _, discovered := range s.discoveredResults {
		rawURL := strings.TrimSpace(discovered.URL)
		if rawURL == "" {
			continue
		}
		if s.hasGroundedURLLocked(rawURL) {
			continue
		}
		if excluded != nil {
			if _, skip := excluded[rawURL]; skip {
				continue
			}
		}
		candidates = append(candidates, discovered)
	}
	if len(candidates) == 0 {
		return researchExecutionDiscoveredResult{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftScore := discoveredGroundingCandidateScore(candidates[i])
		rightScore := discoveredGroundingCandidateScore(candidates[j])
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return strings.TrimSpace(candidates[i].URL) < strings.TrimSpace(candidates[j].URL)
	})
	return candidates[0], true
}

func (s *academicResearchExecutionState) bestAutoFetchCandidate(excluded map[string]struct{}) (researchExecutionDiscoveredResult, bool) {
	if s == nil {
		return researchExecutionDiscoveredResult{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	candidates := make([]researchExecutionDiscoveredResult, 0, len(s.discoveredResults))
	for _, discovered := range s.discoveredResults {
		candidates = append(candidates, discovered)
	}
	return s.bestAutoFetchCandidateLocked(candidates, excluded)
}

func (s *academicResearchExecutionState) bestAutoFetchCandidateForResponse(
	resp *providers.Response,
	excluded map[string]struct{},
) (researchExecutionDiscoveredResult, bool) {
	if s == nil || resp == nil {
		return researchExecutionDiscoveredResult{}, false
	}
	results := providers.DecodeNativeWebSearchResults(resp.ProviderMetadata)
	candidates := make([]researchExecutionDiscoveredResult, 0, len(results)+2)
	for _, result := range results {
		candidates = append(candidates, researchExecutionDiscoveredResult{
			URL:      strings.TrimSpace(result.URL),
			Title:    strings.TrimSpace(result.Title),
			Query:    strings.TrimSpace(result.Query),
			Provider: strings.TrimSpace(result.Provider),
			Source:   strings.TrimSpace(result.Source),
			PageAge:  strings.TrimSpace(result.PageAge),
		})
	}
	for _, call := range providers.DecodeNativeWebSearchCalls(resp.ProviderMetadata) {
		source := nativeWebSearchActionResultSource(call.Action)
		rawURL := strings.TrimSpace(call.URL)
		if source == "" || rawURL == "" {
			continue
		}
		candidates = append(candidates, researchExecutionDiscoveredResult{
			URL:      rawURL,
			Query:    strings.TrimSpace(call.Query),
			Provider: strings.TrimSpace(call.Provider),
			Source:   source,
		})
	}
	if len(candidates) == 0 {
		return researchExecutionDiscoveredResult{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bestAutoFetchCandidateLocked(candidates, excluded)
}

func (s *academicResearchExecutionState) bestAutoFetchCandidateLocked(
	candidates []researchExecutionDiscoveredResult,
	excluded map[string]struct{},
) (researchExecutionDiscoveredResult, bool) {
	eligible := make([]researchExecutionDiscoveredResult, 0, len(candidates))
	for _, candidate := range candidates {
		rawURL := strings.TrimSpace(candidate.URL)
		if rawURL == "" {
			continue
		}
		if s.hasGroundedURLLocked(rawURL) {
			continue
		}
		if excluded != nil {
			if _, skip := excluded[rawURL]; skip {
				continue
			}
		}
		if !discoveredResultEligibleForImmediateFetch(candidate) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return researchExecutionDiscoveredResult{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		leftScore := discoveredGroundingCandidateScore(eligible[i])
		rightScore := discoveredGroundingCandidateScore(eligible[j])
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return strings.TrimSpace(eligible[i].URL) < strings.TrimSpace(eligible[j].URL)
	})
	return eligible[0], true
}

func discoveredResultEligibleForImmediateFetch(result researchExecutionDiscoveredResult) bool {
	rawURL := strings.TrimSpace(result.URL)
	if rawURL == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(result.Source)) {
	case "open_page", "find_in_page":
		return true
	}
	if strings.TrimSpace(result.Title) == "" {
		return false
	}
	if looksLikeDocumentURL(rawURL) {
		return true
	}
	switch sourceTypeFromURL(rawURL) {
	case SourceTypeDocumentation, SourceTypePaper, SourceTypeRFC:
		return true
	default:
		return false
	}
}

func discoveredGroundingCandidateScore(result researchExecutionDiscoveredResult) int {
	score := 0
	switch strings.ToLower(strings.TrimSpace(result.Source)) {
	case "open_page":
		score += 50
	case "find_in_page":
		score += 45
	case "search_result":
		score += 35
	case "url_citation":
		score += 25
	default:
		score += 10
	}
	switch sourceTypeFromURL(result.URL) {
	case SourceTypePaper, SourceTypeRFC, SourceTypeDocumentation:
		score += 20
	case SourceTypeGitHub:
		score += 8
	default:
		score += 4
	}
	if looksLikeDocumentURL(result.URL) {
		score += 5
	}
	if strings.TrimSpace(result.Title) != "" {
		score += 2
	}
	if strings.TrimSpace(result.PageAge) != "" {
		score++
	}
	return score
}

func (s *academicResearchExecutionState) finalizationBlock() (string, map[string]any) {
	if s == nil {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	reasons := make([]string, 0, 2)
	groundedCount := s.groundedSourceCountLocked()
	fields := map[string]any{
		"saw_native_search":    s.sawNativeSearch,
		"saw_discovery_search": s.sawDiscoverySearch,
		"grounded_sources":     groundedCount,
		"paper_required":       s.requirePaper,
		"paper_recorded":       len(s.paperOutput) > 0,
	}
	candidateURLs := s.candidateGroundingURLsLocked(3)
	autoFetchCandidate, hasAutoFetchCandidate := s.bestAutoFetchCandidateLocked(s.discoveredResultsSliceLocked(), nil)
	if candidateCount := len(s.candidateGroundingURLsLocked(0)); candidateCount > 0 {
		fields["candidate_grounding_url_count"] = candidateCount
		fields["candidate_grounding_urls"] = candidateURLs
	}
	if hasAutoFetchCandidate {
		fields["auto_fetch_candidate_url"] = strings.TrimSpace(autoFetchCandidate.URL)
		fields["auto_fetch_candidate_source"] = strings.TrimSpace(autoFetchCandidate.Source)
	}

	if hasAutoFetchCandidate && groundedCount == 0 {
		reason := "You already used `web_search`, but you have not grounded any promising source yet. Pick the strongest candidate URL and call `ground_source`, `web_fetch`, `fetch_document`, or one bounded `crawl_links` follow-up before finalizing."
		if len(candidateURLs) > 0 {
			reason = fmt.Sprintf("%s Candidate URLs already surfaced in this run: %s.", reason, strings.Join(candidateURLs, ", "))
		}
		reasons = append(reasons, reason)
	}
	if s.repeatedSearch != nil && s.repeatedSearch.RepeatedWithoutGround && groundedCount <= s.repeatedSearch.GroundedSourceCount {
		fields["repeated_search_query"] = s.repeatedSearch.Query
		fields["repeated_search_count"] = s.repeatedSearch.Count
		reasons = append(reasons, fmt.Sprintf("Stop repeating the same search path for %q without grounding a source. Ground one promising result before searching again.", strings.TrimSpace(s.repeatedSearch.Query)))
	}
	if s.requirePaper && len(s.paperOutput) == 0 {
		reasons = append(reasons, "This Architect-facing research consult cannot end yet. You must invoke `author_research_paper` once the evidence is ready, then return.")
	}
	if len(reasons) == 0 {
		return "", nil
	}
	return strings.Join(reasons, " "), fields
}

func (s *academicResearchExecutionState) discoveredResultsSliceLocked() []researchExecutionDiscoveredResult {
	if s == nil {
		return nil
	}
	results := make([]researchExecutionDiscoveredResult, 0, len(s.discoveredResults))
	for _, discovered := range s.discoveredResults {
		results = append(results, discovered)
	}
	return results
}

func (s *academicResearchExecutionState) webSearchFrontier() academicWebSearchFrontier {
	if s == nil {
		return academicWebSearchFrontier{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.searchEpisodes)
	if total == 0 {
		return academicWebSearchFrontier{}
	}
	repeated := 0
	rewardSum := 0.0
	for _, episode := range s.searchEpisodes {
		if episode.Repeated {
			repeated++
		}
		rewardSum += episode.reward()
	}
	return academicWebSearchFrontier{
		TotalSearches:    total,
		RepeatedSearches: repeated,
		UniqueResults:    len(s.discoveredResults),
		UniqueDomains:    len(s.authorityDomains),
		GroundedSources:  s.groundedSourceCountLocked(),
		LocalRewardMean:  rewardSum / float64(total),
	}
}

func (s *academicResearchExecutionState) webSearchEpisodeRewards() []searchEpisodeReward {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rewards := make([]searchEpisodeReward, 0, len(s.searchEpisodes))
	for _, episode := range s.searchEpisodes {
		rewards = append(rewards, searchEpisodeReward{
			Ordinal: episode.Ordinal,
			Reward:  episode.reward(),
		})
	}
	return rewards
}

func (e researchExecutionSearchEpisode) reward() float64 {
	switch {
	case e.GroundedCount > 0:
		return 1
	case e.NovelDomainCount > 0:
		return 1
	case e.NovelResultCount > 0:
		return 1
	default:
		return 0
	}
}

type researchResultBuildOptions struct {
	AllowUngrounded bool
}

func (s *academicResearchExecutionState) buildResearchResult(params *authorResearchPaperParams) (*ResearchResult, error) {
	return s.buildResearchResultWithOptions(params, researchResultBuildOptions{})
}

func (s *academicResearchExecutionState) buildResearchResultWithOptions(
	params *authorResearchPaperParams,
	opts researchResultBuildOptions,
) (*ResearchResult, error) {
	if s == nil {
		return nil, fmt.Errorf("research execution state is required")
	}
	sourceIDs := s.sourceIDs()
	if len(sourceIDs) == 0 && !opts.AllowUngrounded {
		return nil, errAcademicResearchPaperMissingGroundedSources
	}
	findings := make([]Finding, 0)
	for _, item := range filterNonEmpty(params.KeyFindings) {
		findings = append(findings, Finding{
			ID:         uuid.NewString(),
			Topic:      firstNonEmpty(strings.TrimSpace(params.Topic), "research finding"),
			Summary:    item,
			Details:    strings.TrimSpace(params.ResearchSummary),
			Confidence: academicExecutionConfidenceLevel(s),
			SourceIDs:  append([]string(nil), sourceIDs...),
			Citations:  append([]string(nil), s.sourceURLs()...),
		})
	}
	if len(findings) == 0 {
		summary := firstNonEmpty(strings.TrimSpace(params.ResearchSummary), strings.TrimSpace(params.Context), strings.TrimSpace(params.Topic))
		if summary == "" {
			summary = "Grounded research was gathered and synthesized for the requested topic."
		}
		findings = append(findings, Finding{
			ID:         uuid.NewString(),
			Topic:      firstNonEmpty(strings.TrimSpace(params.Topic), "research finding"),
			Summary:    summary,
			Confidence: academicExecutionConfidenceLevel(s),
			SourceIDs:  append([]string(nil), sourceIDs...),
			Citations:  append([]string(nil), s.sourceURLs()...),
		})
	}
	recommendations := make([]Recommendation, 0, len(params.Recommendations))
	for i, rec := range filterNonEmpty(params.Recommendations) {
		recommendations = append(recommendations, Recommendation{
			ID:            fmt.Sprintf("recommendation_%d", i+1),
			Title:         rec,
			Description:   rec,
			Rationale:     strings.TrimSpace(params.ResearchSummary),
			Applicability: strings.TrimSpace(params.ArchitectSummary),
			Confidence:    academicExecutionConfidenceLevel(s),
			SourceIDs:     append([]string(nil), sourceIDs...),
		})
	}
	return &ResearchResult{
		QueryID:          uuid.NewString(),
		Findings:         findings,
		Recommendations:  recommendations,
		SourcesConsulted: append([]string(nil), sourceIDs...),
		Confidence:       academicExecutionConfidenceLevelWithOptions(s, opts),
		GeneratedAt:      time.Now().UTC(),
	}, nil
}

func (s *academicResearchExecutionState) consultationEvidence(target string) *shared.ConsultationEvidence {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "librarian":
		return s.librarianEvidence
	case "archivalist":
		return s.archivalEvidence
	default:
		return nil
	}
}

func academicExecutionConfidenceLevel(state *academicResearchExecutionState) ConfidenceLevel {
	return academicExecutionConfidenceLevelWithOptions(state, researchResultBuildOptions{})
}

func academicExecutionConfidenceLevelWithOptions(state *academicResearchExecutionState, opts researchResultBuildOptions) ConfidenceLevel {
	if state == nil {
		if opts.AllowUngrounded {
			return ConfidenceLevelLow
		}
		return ConfidenceLevelMedium
	}
	if opts.AllowUngrounded && len(state.sourceIDs()) == 0 {
		return ConfidenceLevelLow
	}
	score := len(state.sourceIDs())
	if state.consultationEvidence("librarian") != nil {
		score++
	}
	if state.consultationEvidence("archivalist") != nil {
		score++
	}
	switch {
	case score >= 4:
		return ConfidenceLevelHigh
	case score >= 2:
		return ConfidenceLevelMedium
	default:
		return ConfidenceLevelLow
	}
}

func parseResearchJSONPayload(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload
}

func payloadSuccess(payload map[string]any) bool {
	if len(payload) == 0 {
		return false
	}
	if success, ok := payload["success"].(bool); ok {
		return success
	}
	return true
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func boolValue(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

func sourceTypeFromURL(rawURL string) SourceType {
	lowered := strings.ToLower(strings.TrimSpace(rawURL))
	switch {
	case strings.HasSuffix(lowered, ".pdf"):
		return SourceTypePaper
	case strings.Contains(lowered, "/docs/"), strings.Contains(lowered, "readthedocs"), strings.Contains(lowered, "docs."):
		return SourceTypeDocumentation
	case strings.Contains(lowered, "rfc"), strings.Contains(lowered, "ietf.org"), strings.Contains(lowered, "w3.org"), strings.Contains(lowered, "spec"):
		return SourceTypeRFC
	case strings.Contains(lowered, "github.com"), strings.Contains(lowered, "gitlab.com"):
		return SourceTypeGitHub
	default:
		return SourceTypeArticle
	}
}

func (s *academicResearchExecutionState) hasGroundedURL(rawURL string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasGroundedURLLocked(rawURL)
}

func (s *academicResearchExecutionState) hasGroundedURLLocked(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	for _, source := range s.sources {
		if source.Grounded && strings.TrimSpace(source.URL) == rawURL {
			return true
		}
	}
	return false
}

func (s *academicResearchExecutionState) groundedSourceCountLocked() int {
	count := 0
	for _, source := range s.sources {
		if source.Grounded {
			count++
		}
	}
	return count
}

func (s *academicResearchExecutionState) reserveSourceIDForURLLocked(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if existing := strings.TrimSpace(s.sourceIDsByURL[rawURL]); existing != "" {
		return existing
	}
	effectiveID := "src_" + uuid.NewString()
	s.sourceIDsByURL[rawURL] = effectiveID
	return effectiveID
}

func sourceQualityFromPayload(rawURL string, payload map[string]any) float64 {
	quality := 0.7
	if boolValue(payload["ingested"]) {
		quality += 0.1
	}
	switch sourceTypeFromURL(rawURL) {
	case SourceTypeDocumentation, SourceTypeRFC, SourceTypePaper:
		quality += 0.1
	}
	if quality > 1 {
		return 1
	}
	return quality
}

func (a *Academic) upsertResearchSource(source *Source) {
	if a == nil || source == nil || strings.TrimSpace(source.ID) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if source.UpdatedAt.IsZero() {
		source.UpdatedAt = time.Now().UTC()
	}
	a.sourceIndex[source.ID] = source
	a.pruneSourceIndexLocked(time.Now())
}

func academicShouldUseArchitectResearchProtocol(fwd *guide.ForwardedRequest) bool {
	if fwd == nil {
		return false
	}
	if academicConversationHandoffFromForwarded(fwd) != nil {
		return false
	}
	if !academicForwardedSourceMatches(fwd, "architect") {
		return false
	}
	switch fwd.Intent {
	case guide.IntentRecall, guide.IntentCheck, guide.IntentFetch:
		return true
	default:
		return false
	}
}

func (a *Academic) handleArchitectResearchProtocol(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	params := academicArchitectResearchParamsFromForwardedRequest(fwd)
	academicLogArchitectResearchRouting(ctx, fwd, params)
	result, err := a.runArchitectResearchProtocol(ctx, fwd, params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func academicArchitectResearchParamsFromForwardedRequest(fwd *guide.ForwardedRequest) *architectResearchProtocolParams {
	params := &architectResearchProtocolParams{
		Query:              strings.TrimSpace(fwd.Input),
		Domain:             strings.TrimSpace(string(fwd.Domain)),
		SessionID:          strings.TrimSpace(fwd.SessionID),
		StoreInArchivalist: true,
	}
	contextParts := make([]string, 0, 4)
	switch fwd.Intent {
	case guide.IntentCheck:
		contextParts = append(contextParts, "The Architect needs this framed as a verification or challenge assessment, not just a generic overview.")
	case guide.IntentFetch:
		contextParts = append(contextParts, "The Architect needs concrete source retrieval and evidence-backed feasibility details.")
	}
	if fwd.Domain != "" && fwd.Domain != guide.DomainUnknown {
		contextParts = append(contextParts, "Guide domain: "+string(fwd.Domain))
	}
	if len(fwd.ConversationHistory) > 0 {
		last := fwd.ConversationHistory[len(fwd.ConversationHistory)-1]
		if strings.TrimSpace(last.UserInput) != "" {
			contextParts = append(contextParts, "Recent user context: "+strings.TrimSpace(last.UserInput))
		}
	}
	if len(contextParts) > 0 {
		params.Context = strings.Join(contextParts, "\n")
	}
	return params
}

func (a *Academic) architectResearchWorkflowSurface() toolruntime.Surface {
	base := a.toolRuntime()
	if base == nil {
		return nil
	}
	view, err := base.RequestView("author_research_paper", "clone_via_librarian", "crawl_links")
	if err != nil {
		return base
	}
	return view
}

func (a *Academic) runArchitectResearchProtocol(
	ctx context.Context,
	fwd *guide.ForwardedRequest,
	params *architectResearchProtocolParams,
) (map[string]any, error) {
	if params == nil || strings.TrimSpace(params.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	// Architect research protocol claim.
	queryText := strings.TrimSpace(params.Query)
	completeFn := a.academicStageClaim(ctx,
		"Architect research protocol: "+truncateAcademic(queryText, 60),
		"4-phase research (Gather → Ground → Consult → Synthesize) with paper output",
	)
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		sessionID = versioningSessionIDOrDefault(ctx, a.config.SessionID)
	}
	state := newAcademicResearchExecutionState(sessionID)
	ctx = WithAcademicResearchExecutionState(ctx, state)
	contract := academicCompletionContractForArchitectResearch(params)
	ctx = WithAcademicCompletionContract(ctx, contract)
	ctx = WithAcademicTurnState(ctx, newAcademicTurnState(
		academicTurnActionResearchPaper,
		"This Architect consultation must terminate by invoking `author_research_paper` before the Academic returns.",
	))
	academicLogArchitectResearchStart(ctx, params)

	prompt := buildArchitectResearchProtocolPrompt(params, contract)
	a.prepareSkillsForInput(prompt)
	surface := a.architectResearchWorkflowSurface()
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: prompt}},
		Tools:        a.buildToolDefinitionsWithSurface(surface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "research")
	if fwd != nil {
		shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)
	}

	ledger := shared.SteeringLedgerFromContext(ctx)
	text, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, surface)
	})
	if err != nil {
		protocolErr := fmt.Errorf("architect research protocol: %w", err)
		completeFn("Protocol failed", nil, protocolErr)
		return nil, protocolErr
	}
	result, finalizeErr := finalizeArchitectResearchProtocolResult(ctx, state, text)

	// Protocol completion testament.
	state.mu.RLock()
	sourceCount := len(state.sources)
	hasPaper := len(state.paperOutput) > 0
	state.mu.RUnlock()
	completeFn(
		fmt.Sprintf("Protocol complete: %d sources, paper=%t", sourceCount, hasPaper),
		[]*claims.Artifact{
			a.academicArtifact("source_count", fmt.Sprintf("%d", sourceCount)),
			a.academicArtifact("paper_generated", fmt.Sprintf("%t", hasPaper)),
			a.academicArtifact("query", truncateAcademic(queryText, 200)),
		},
		finalizeErr,
	)
	return result, finalizeErr
}

func finalizeArchitectResearchProtocolResult(
	ctx context.Context,
	state *academicResearchExecutionState,
	text string,
) (map[string]any, error) {
	paperOutput := state.builtPaperOutput()
	if len(paperOutput) == 0 {
		return nil, fmt.Errorf("architect research protocol completed without invoking author_research_paper")
	}
	if summary := strings.TrimSpace(text); summary != "" && !responseLooksLikeResearchStatus(summary) {
		if _, exists := paperOutput["summary"]; !exists {
			paperOutput["summary"] = summary
		}
	}
	paperOutput["source_urls"] = state.sourceURLs()
	paperOutput["consulted_agents"] = state.consultedAgents()
	paperOutput["protocol"] = "architect_research"
	if _, ok := paperOutput["type"]; !ok {
		paperOutput["type"] = "research_paper"
	}
	academicLogArchitectResearchCompletion(ctx, state, paperOutput)
	return paperOutput, nil
}

func academicCompletionContractForArchitectResearch(params *architectResearchProtocolParams) *academicCompletionContract {
	query := ""
	if params != nil {
		query = strings.TrimSpace(strings.Join([]string{
			params.Query,
			params.Context,
			strings.Join(params.RelatedTopics, " "),
			strings.Join(params.OpenQuestions, " "),
		}, "\n"))
	}
	contract := &academicCompletionContract{
		Objective: academicObjectiveProduceResearchArtifact,
		RequiredEvidence: []academicEvidenceClass{
			academicEvidenceExternalGrounding,
			academicEvidenceCodebaseFit,
		},
		PreferredEvidence:  inferPreferredEvidenceClasses(query),
		AllowInconclusive:  true,
		RequirePolishedOut: true,
	}
	contract.PreferredEvidence = appendUniqueEvidenceClasses(
		contract.PreferredEvidence,
		academicEvidenceOfficialDocs,
		academicEvidenceExpertArticles,
		academicEvidenceResearchPapers,
	)
	if academicArchitectResearchNeedsHistoricalPrecedent(query) {
		contract.RequiredEvidence = appendUniqueEvidenceClasses(contract.RequiredEvidence, academicEvidenceHistoricalPrecedent)
	} else {
		contract.PreferredEvidence = appendUniqueEvidenceClasses(contract.PreferredEvidence, academicEvidenceHistoricalPrecedent)
	}
	return contract
}

func academicArchitectResearchNeedsHistoricalPrecedent(query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return false
	}
	return containsAny(
		query,
		"historical",
		"history",
		"prior decision",
		"previous decision",
		"precedent",
		"legacy",
		"migration",
		"regression",
		"past incident",
		"existing failure mode",
	)
}

func buildArchitectResearchProtocolPrompt(params *architectResearchProtocolParams, contract *academicCompletionContract) string {
	var b strings.Builder
	b.WriteString("Handle this Architect research consultation end-to-end.\n\n")
	b.WriteString("You own the research workflow. Do not return transient prose, a status note, or a partial answer. Only finish this consultation after invoking `author_research_paper` exactly once.\n\n")
	if contract != nil {
		b.WriteString(contract.guidancePrompt())
		b.WriteString("\n\n")
	}
	b.WriteString("Protocol phases:\n")
	b.WriteString("1. Gather research. If current public information, official docs, standards, research papers, expert articles, or precise source URLs matter, use `web_search` first. Keep searching only while you are still surfacing materially better, more relevant, well-sourced candidates. Provider-native `web_search` may already inspect pages in-context.\n")
	b.WriteString("2. Ground and ingest evidence. If a result looks promising, make sure the source is actually examined before relying on it. Sylk may automatically secure-fetch clearly relevant, high-quality exact URLs surfaced by native `web_search`. Use `ground_source`, `web_fetch`, `fetch_document`, or one bounded `crawl_links` follow-up when you need exact-URL control, a specific fetch mode, or a secured fetch path that has not happened yet. For a high-relevance source you expect to cite heavily, prefer `fetch_document` so it is ingested into the knowledge graph and document store when available.\n")
	b.WriteString("3. Consult supporting agents only when the evidence genuinely needs them. Consult the Librarian when codebase fit matters. Consult the Archivalist when historical precedent materially affects the answer. You may not repeat the same consultation question to the same agent in this run.\n")
	b.WriteString("4. Synthesize. Once the evidence is sufficient, invoke `author_research_paper` and pass the research summary, key findings, recommendations, open questions, and any architect-facing planning notes needed by the Architect.\n\n")
	b.WriteString("When you call `author_research_paper`:\n")
	b.WriteString("- set `topic` to the main research topic\n")
	b.WriteString("- include `context` if it materially affects the recommendation\n")
	b.WriteString("- include `architect_summary`, `research_summary`, `key_findings`, `recommendations`, and `open_questions` when available\n")
	b.WriteString("- set `store_in_archivalist=true`\n")
	b.WriteString("- do not set `handoff_to_architect`; this consult returns directly to Architect\n\n")
	b.WriteString("Research request:\n")
	b.WriteString(strings.TrimSpace(params.Query))
	if contextText := strings.TrimSpace(params.Context); contextText != "" {
		b.WriteString("\n\nAdditional context:\n")
		b.WriteString(contextText)
	}
	if domain := strings.TrimSpace(params.Domain); domain != "" {
		b.WriteString("\n\nDomain:\n")
		b.WriteString(domain)
	}
	return strings.TrimSpace(b.String())
}

func academicLogArchitectResearchCompletion(ctx context.Context, state *academicResearchExecutionState, output map[string]any) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil {
		return
	}
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchQuery,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"info",
		map[string]any{
			"decision":         "architect_research_protocol_complete",
			"source_urls":      state.sourceURLs(),
			"consulted_agents": state.consultedAgents(),
			"paper_id":         stringValue(output["paper_id"]),
			"paper_path":       stringValue(output["paper_path"]),
			"title":            stringValue(output["title"]),
		},
	)
}

func academicLogArchitectResearchStart(ctx context.Context, params *architectResearchProtocolParams) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil || params == nil {
		return
	}
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchQuery,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"info",
		map[string]any{
			"decision":          "architect_research_protocol_start",
			"query":             strings.TrimSpace(params.Query),
			"domain":            strings.TrimSpace(params.Domain),
			"session_id":        strings.TrimSpace(params.SessionID),
			"constraints":       append([]string(nil), params.Constraints...),
			"invariants":        append([]string(nil), params.Invariants...),
			"open_questions":    append([]string(nil), params.OpenQuestions...),
			"related_topics":    append([]string(nil), params.RelatedTopics...),
			"store_archivalist": params.StoreInArchivalist,
		},
	)
}

func academicLogArchitectResearchRouting(ctx context.Context, fwd *guide.ForwardedRequest, params *architectResearchProtocolParams) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil || fwd == nil || params == nil {
		return
	}
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchQuery,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"info",
		map[string]any{
			"decision":          "route_architect_consult_to_research_protocol",
			"source_agent_id":   strings.TrimSpace(fwd.SourceAgentID),
			"source_agent_name": strings.TrimSpace(fwd.SourceAgentName),
			"intent":            string(fwd.Intent),
			"query":             strings.TrimSpace(params.Query),
		},
	)
}

func academicLogResearchStateEvent(ctx context.Context, decision string, fields map[string]any) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil {
		return
	}
	payload := map[string]any{
		"decision": strings.TrimSpace(decision),
	}
	for key, value := range fields {
		payload[key] = value
	}
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchQuery,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"debug",
		payload,
	)
}

func academicLogDuplicateConsultationBlocked(ctx context.Context, target, query, scope string) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil {
		return
	}
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchQuery,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"warn",
		map[string]any{
			"decision": "duplicate_consultation_blocked",
			"target":   strings.TrimSpace(target),
			"query":    strings.TrimSpace(query),
			"scope":    strings.TrimSpace(scope),
		},
	)
}

func versioningSessionIDOrDefault(ctx context.Context, fallback string) string {
	if sessionID := versioning.SessionIDFromContext(ctx); strings.TrimSpace(sessionID) != "" {
		return strings.TrimSpace(sessionID)
	}
	return strings.TrimSpace(fallback)
}

func appendUniqueInt(values []int, value int) []int {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func authorityDomainFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}
