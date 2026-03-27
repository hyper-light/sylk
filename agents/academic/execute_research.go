package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

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
		sessionID:        strings.TrimSpace(sessionID),
		consultAttempts:  make(map[string]struct{}),
		searchAttempts:   make(map[string]researchExecutionSearch),
		observedSearches: make(map[string]struct{}),
		sourceIDsByURL:   make(map[string]string),
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
		s.recordConsultationResult(ctx, call, result)
	case "author_research_paper":
		s.recordPaperOutput(ctx, result)
	default:
		if strings.HasPrefix(strings.TrimSpace(call.Name), "consult_") {
			s.recordConsultationResult(ctx, call, result)
		}
	}
}

func (s *academicResearchExecutionState) observeProviderResponse(ctx context.Context, resp *providers.Response) {
	if s == nil || resp == nil {
		return
	}
	for _, call := range providers.DecodeNativeWebSearchCalls(resp.ProviderMetadata) {
		s.observeNativeSearchCall(ctx, call)
	}
}

func (s *academicResearchExecutionState) observeNativeSearchCall(ctx context.Context, call providers.NativeWebSearchCall) {
	if s == nil {
		return
	}
	query := strings.TrimSpace(call.Query)
	action := strings.TrimSpace(call.Action)
	rawURL := strings.TrimSpace(call.URL)
	if query == "" && action == "" && rawURL == "" {
		return
	}

	fingerprint, _ := normalizeSearchQuery(firstNonEmpty(query, action+" "+rawURL))
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
	groundedCount := len(s.sources)
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
	srcID := "src_" + uuid.NewString()
	source := &Source{
		ID:          srcID,
		Type:        sourceTypeFromURL(rawURL),
		URL:         rawURL,
		Title:       title,
		Description: truncateStr(strings.TrimSpace(summary), 280),
		UpdatedAt:   time.Now().UTC(),
		TokenCount:  intValue(payload["word_count"]),
		Quality:     sourceQualityFromPayload(rawURL, payload),
		Metadata: map[string]any{
			"grounded":           boolValue(payload["grounded"]),
			"ingested":           boolValue(payload["ingested"]),
			"content_hash":       stringValue(payload["content_hash"]),
			"persistence_status": stringValue(payload["persistence_status"]),
			"persistence_job_id": stringValue(payload["persistence_job_id"]),
		},
	}
	if boolValue(payload["ingested"]) {
		source.IngestedAt = time.Now().UTC()
	}
	a.upsertResearchSource(source)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existingID, exists := s.sourceIDsByURL[rawURL]; exists {
		for i := range s.sources {
			if s.sources[i].ID == existingID {
				s.sources[i].Title = title
				s.sources[i].Summary = truncateStr(strings.TrimSpace(summary), 500)
				s.sources[i].ContentHash = stringValue(payload["content_hash"])
				s.sources[i].WordCount = intValue(payload["word_count"])
				s.sources[i].Ingested = boolValue(payload["ingested"])
				s.sources[i].Grounded = boolValue(payload["grounded"])
				s.sources[i].Persisted = boolValue(payload["ingested"])
				s.sources[i].Persistence = stringValue(payload["persistence_status"])
				s.sources[i].JobID = stringValue(payload["persistence_job_id"])
				s.sources[i].Quality = source.Quality
				academicLogResearchStateEvent(ctx, "grounded_source_updated", map[string]any{
					"url":                rawURL,
					"title":              title,
					"ingested":           boolValue(payload["ingested"]),
					"persistence_status": stringValue(payload["persistence_status"]),
					"persistence_job_id": stringValue(payload["persistence_job_id"]),
				})
				return
			}
		}
	}
	s.sourceIDsByURL[rawURL] = srcID
	s.sources = append(s.sources, researchExecutionSource{
		ID:          srcID,
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
		Type:        source.Type,
		Quality:     source.Quality,
	})
	academicLogResearchStateEvent(ctx, "grounded_source_recorded", map[string]any{
		"url":                rawURL,
		"title":              title,
		"word_count":         intValue(payload["word_count"]),
		"ingested":           boolValue(payload["ingested"]),
		"persistence_status": stringValue(payload["persistence_status"]),
		"persistence_job_id": stringValue(payload["persistence_job_id"]),
		"source_type":        source.Type,
	})
}

func (s *academicResearchExecutionState) recordConsultationResult(ctx context.Context, call providers.ToolCall, result string) {
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
	defer s.mu.Unlock()
	switch target {
	case "librarian":
		s.librarianEvidence = evidence
	case "archivalist":
		s.archivalEvidence = evidence
	}
	academicLogResearchStateEvent(ctx, "consultation_recorded", map[string]any{
		"target": target,
		"query":  query,
		"scope":  scope,
	})
}

func (s *academicResearchExecutionState) recordPaperOutput(ctx context.Context, result string) {
	payload := parseResearchJSONPayload(result)
	if len(payload) == 0 {
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
		if strings.TrimSpace(source.ID) != "" {
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
		if strings.TrimSpace(source.URL) != "" {
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

func (s *academicResearchExecutionState) finalizationBlock() (string, map[string]any) {
	if s == nil {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	reasons := make([]string, 0, 2)
	fields := map[string]any{
		"saw_native_search":    s.sawNativeSearch,
		"saw_discovery_search": s.sawDiscoverySearch,
		"grounded_sources":     len(s.sources),
		"paper_required":       s.requirePaper,
		"paper_recorded":       len(s.paperOutput) > 0,
	}

	if s.sawDiscoverySearch && len(s.sources) == 0 {
		reasons = append(reasons, "You already used `web_search`, but you have not grounded any promising source yet. Pick the strongest candidate URL and call `ground_source`, `web_fetch`, `fetch_document`, or one bounded `crawl_links` follow-up before finalizing.")
	}
	if s.repeatedSearch != nil && s.repeatedSearch.RepeatedWithoutGround {
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

func (s *academicResearchExecutionState) buildResearchResult(params *authorResearchPaperParams) (*ResearchResult, error) {
	if s == nil {
		return nil, fmt.Errorf("research execution state is required")
	}
	sourceIDs := s.sourceIDs()
	if len(sourceIDs) == 0 {
		return nil, fmt.Errorf("the academic research run gathered no grounded sources for research paper synthesis")
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
		Confidence:       academicExecutionConfidenceLevel(s),
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
	if state == nil {
		return ConfidenceLevelMedium
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
	a.sourceIndex[source.ID] = source
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
		return a.executeToolLoopWithDiscipline(
			ctx,
			llmReq,
			ledger,
			surface,
			requiredSearchDiscipline(a.config.SearchDisciplineMaxAttempts),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("architect research protocol: %w", err)
	}
	return finalizeArchitectResearchProtocolResult(ctx, state, text)
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
	b.WriteString("1. Gather research. If current public information, official docs, standards, research papers, expert articles, or precise source URLs matter, use `web_search` first. Keep searching only while you are still surfacing materially better, more relevant, well-sourced candidates.\n")
	b.WriteString("2. Ground and ingest evidence. If a result looks promising, you must inspect the source itself before relying on it: use `web_fetch`, `fetch_document`, or one bounded `crawl_links` follow-up. For a high-relevance source you expect to cite heavily, prefer `fetch_document` so it is ingested into the knowledge graph and document store when available.\n")
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
