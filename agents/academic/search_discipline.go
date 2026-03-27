package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

const (
	searchPlateauWindow               = 5
	searchPlateauVoteThreshold        = 3
	searchPlateauAverageDeltaMax      = 0.022
	searchPlateauAverageConfidenceMax = 0.018
	searchPlateauAverageReadinessMax  = 0.018
	searchPlateauMaxDelta             = 0.05
	searchPlateauVolatilityMax        = 0.028
	searchPlateauMaterialGrowth       = 0.10
	searchPlateauMaterialConfidence   = 0.08
	searchPlateauMaterialReadiness    = 0.08
	searchPlateauSpikeCooldown        = 3
	searchPlateauMinimumSignals       = 5
	searchPlateauMinReadiness         = 0.56
	searchPlateauMinConfidence        = 0.44
	searchPlateauMinBreadth           = 0.28
	searchPlateauMinDepth             = 0.26
	searchPlateauMinGrounding         = 0.22
	searchPlateauMinCorroboration     = 0.20
	searchPlateauMinSurfaceModes      = 2
	searchPlateauMinSurfaceCount      = 3
	searchPlateauMinimumEmergency     = 24
	searchPlateauEmergencyMultiplier  = 6
	searchGroundingTransitionCalls    = 4
	searchGroundingTransitionQueries  = 3
	searchSearchOnlyConfidenceCap     = 0.34
	searchSearchOnlyReadinessCap      = 0.38
	searchUngroundedConfidenceCap     = 0.46
	searchUngroundedReadinessCap      = 0.50
)

type researchPhase string

const (
	researchPhaseDiscover    researchPhase = "discover"
	researchPhaseGround      researchPhase = "ground"
	researchPhaseCorroborate researchPhase = "corroborate"
	researchPhaseSynthesize  researchPhase = "synthesize"
)

type researchProgressScores struct {
	Breadth       float64
	Depth         float64
	Grounding     float64
	Corroboration float64
	Confidence    float64
	Readiness     float64
	Total         float64
}

type researchObservation struct {
	Breadth         float64
	Depth           float64
	Grounding       float64
	Corroboration   float64
	Confidence      float64
	Readiness       float64
	Total           float64
	DeltaTotal      float64
	DeltaConfidence float64
	DeltaReadiness  float64
}

type searchDiscipline struct {
	RequireWebSearch            bool
	MaxAttempts                 int
	MaxNativeSearchCallsPerTurn int
}

type searchEvidenceTracker struct {
	nativeSearchCalls   map[string]providers.NativeWebSearchCall
	completedCallKeys   map[string]struct{}
	seenCallKeys        map[string]struct{}
	scoredCallKeys      map[string]struct{}
	queryFingerprints   map[string]map[string]struct{}
	consultFingerprints map[string]map[string]struct{}
	fetchedURLs         map[string]struct{}
	fetchedDomains      map[string]struct{}
	fetchedDocuments    map[string]struct{}
	consultTargets      map[string]struct{}
	sawSearch           bool
	maxCallsPerTurn     int
	hardMaxCalls        int
	currentTurn         int
	overBudget          bool
	runawayErr          error
	crawlRuns           int
	searchNoveltyMass   float64
	lowNoveltyStreak    int
	lowYieldStreak      int
	progressDeltas      []float64
	plateauVotes        int
	plateauCooldown     int
	lastTotalScore      float64
	lastScores          researchProgressScores
	observations        []researchObservation
	synthesizeNow       bool
	consultNoveltyMass  float64
	phase               researchPhase
	turnStartPhase      researchPhase
	promptQueued        bool
	evidenceClasses     map[academicEvidenceClass]int
	completionContract  *academicCompletionContract
}

type searchEvidenceTrackerKey struct{}

func withSearchEvidenceTracker(ctx context.Context, tracker *searchEvidenceTracker) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, searchEvidenceTrackerKey{}, tracker)
}

func searchEvidenceTrackerFromContext(ctx context.Context) *searchEvidenceTracker {
	if ctx == nil {
		return nil
	}
	tracker, _ := ctx.Value(searchEvidenceTrackerKey{}).(*searchEvidenceTracker)
	return tracker
}

func newSearchEvidenceTracker() *searchEvidenceTracker {
	return &searchEvidenceTracker{
		nativeSearchCalls:   make(map[string]providers.NativeWebSearchCall),
		completedCallKeys:   make(map[string]struct{}),
		seenCallKeys:        make(map[string]struct{}),
		scoredCallKeys:      make(map[string]struct{}),
		queryFingerprints:   make(map[string]map[string]struct{}),
		consultFingerprints: make(map[string]map[string]struct{}),
		fetchedURLs:         make(map[string]struct{}),
		fetchedDomains:      make(map[string]struct{}),
		fetchedDocuments:    make(map[string]struct{}),
		consultTargets:      make(map[string]struct{}),
		evidenceClasses:     make(map[academicEvidenceClass]int),
		phase:               researchPhaseDiscover,
	}
}

func (t *searchEvidenceTracker) setCompletionContract(contract *academicCompletionContract) {
	if t == nil {
		return
	}
	t.completionContract = contract
}

func (t *searchEvidenceTracker) beginTurn(turn int) {
	if t == nil {
		return
	}
	t.currentTurn = turn
	t.turnStartPhase = t.currentPhase()
}

func (t *searchEvidenceTracker) turnScopedKey(key string) string {
	if t == nil {
		return key
	}
	return fmt.Sprintf("%d:%s", t.currentTurn, key)
}

func (t *searchEvidenceTracker) observeResponse(resp *providers.Response) {
	if t == nil || resp == nil {
		return
	}
	for _, call := range providers.DecodeNativeWebSearchCalls(resp.ProviderMetadata) {
		key := strings.TrimSpace(call.ID)
		if key == "" {
			key = call.ArgumentsJSON()
		}
		t.observeNativeSearchCall(key, call)
	}
}

func (t *searchEvidenceTracker) observeStreamChunk(chunk *providers.StreamChunk) {
	if t == nil || chunk == nil || t.runawayErr != nil {
		return
	}
	if chunk.ToolCall == nil {
		return
	}
	switch chunk.Type {
	case providers.ChunkTypeToolStart, providers.ChunkTypeToolDelta:
	default:
		return
	}
	if !isNativeWebSearchChunk(chunk.ToolCall) {
		return
	}
	key := strings.TrimSpace(chunk.ToolCall.ID)
	if key == "" {
		key = strings.TrimSpace(chunk.ToolCall.ArgumentsDelta)
	}
	if key == "" {
		key = "native_web_search_unknown"
	}
	t.observeNativeSearchCall(key, nativeWebSearchCallFromToolChunk(chunk.ToolCall))
}

func (t *searchEvidenceTracker) suppressStreamChunk(chunk *providers.StreamChunk) bool {
	if t == nil || chunk == nil || chunk.ToolCall == nil {
		return false
	}
	if !isNativeWebSearchChunk(chunk.ToolCall) {
		return false
	}
	return t.overBudget || t.synthesizeNow || t.runawayErr != nil
}

func (t *searchEvidenceTracker) emitNativeSearchCompletions(ctx context.Context, agentID string) {
	if t == nil {
		return
	}
	keys := make([]string, 0, len(t.nativeSearchCalls))
	for key := range t.nativeSearchCalls {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		call := t.nativeSearchCalls[key]
		if _, ok := t.completedCallKeys[key]; ok {
			continue
		}
		toolCall := call.ToolCall()
		shared.CompleteProviderNativeToolCall(ctx, agentID, toolCall, "")
		t.completedCallKeys[key] = struct{}{}
	}
}

func (t *searchEvidenceTracker) enforceSearchBudget() {
	if t == nil || t.maxCallsPerTurn <= 0 {
		return
	}
	if len(t.seenCallKeys) > t.maxCallsPerTurn {
		t.overBudget = true
	}
	if t.runawayErr != nil {
		return
	}
	if t.hardMaxCalls > 0 && len(t.seenCallKeys) > t.hardMaxCalls {
		t.runawayErr = excessiveNativeWebSearchError(len(t.seenCallKeys), t.hardMaxCalls)
		t.synthesizeNow = true
	}
}

func (t *searchEvidenceTracker) runawayError() error {
	if t == nil {
		return nil
	}
	return t.runawayErr
}

func (t *searchEvidenceTracker) clearRunawayError() {
	if t == nil {
		return
	}
	t.runawayErr = nil
}

func (t *searchEvidenceTracker) synthesisRequested() bool {
	if t == nil {
		return false
	}
	return t.synthesizeNow
}

func (t *searchEvidenceTracker) clearSynthesisRequested() {
	if t == nil {
		return
	}
	t.synthesizeNow = false
	t.plateauVotes = 0
	t.lowNoveltyStreak = 0
	t.lowYieldStreak = 0
	t.plateauCooldown = 0
	t.advancePhase(t.currentScores())
}

func (t *searchEvidenceTracker) searchBudgetExceeded() bool {
	if t == nil {
		return false
	}
	return t.overBudget
}

func (t *searchEvidenceTracker) currentScores() researchProgressScores {
	if t == nil {
		return researchProgressScores{}
	}
	searchBreadth := clamp01(t.searchNoveltyMass / 3.5)
	fetchDomainBreadth := clamp01(float64(len(t.fetchedDomains)) / 3.0)
	consultBreadth := clamp01(float64(len(t.consultTargets)) / 3.0)
	breadth := clamp01(0.50*searchBreadth + 0.25*fetchDomainBreadth + 0.25*consultBreadth)

	fetchDepth := clamp01(float64(len(t.fetchedURLs)) / 4.0)
	documentDepth := clamp01(float64(len(t.fetchedDocuments)) / 2.0)
	crawlDepth := clamp01(float64(t.crawlRuns) / 2.0)
	consultDepth := clamp01(t.consultNoveltyMass / 2.5)
	depth := clamp01(0.35*fetchDepth + 0.25*documentDepth + 0.10*crawlDepth + 0.30*consultDepth)

	searchConfidence := 0.0
	if t.sawSearch {
		searchConfidence = 0.12
	}
	modeDiversity := clamp01(float64(t.evidenceModeCount()-1) / 3.0)
	grounding := clamp01(0.40*fetchDepth + 0.25*documentDepth + 0.20*consultDepth + 0.15*fetchDomainBreadth)
	corroboration := clamp01(
		0.35*modeDiversity +
			0.25*math.Min(searchBreadth, fetchDomainBreadth) +
			0.20*math.Min(fetchDepth+0.5*documentDepth, consultBreadth+0.5*consultDepth) +
			0.20*math.Min(searchBreadth, consultBreadth+0.5*consultDepth),
	)
	confidence := clamp01(0.10*searchConfidence + 0.18*breadth + 0.18*depth + 0.27*grounding + 0.27*corroboration)
	readiness := clamp01(0.22*breadth + 0.18*depth + 0.30*grounding + 0.30*corroboration)
	switch {
	case !t.hasExternalEvidenceBeyondSearch():
		confidence = math.Min(confidence, searchSearchOnlyConfidenceCap)
		readiness = math.Min(readiness, searchSearchOnlyReadinessCap)
	case !t.hasGroundedEvidence():
		confidence = math.Min(confidence, searchUngroundedConfidenceCap)
		readiness = math.Min(readiness, searchUngroundedReadinessCap)
	}
	total := clamp01(0.20*breadth + 0.18*depth + 0.22*confidence + 0.20*grounding + 0.20*corroboration)
	return researchProgressScores{
		Breadth:       breadth,
		Depth:         depth,
		Grounding:     grounding,
		Corroboration: corroboration,
		Confidence:    confidence,
		Readiness:     readiness,
		Total:         total,
	}
}

func (a *Academic) supportsSearchDiscipline(p academicProvider) bool {
	if a == nil || a.config.DisableSearchDiscipline || p == nil {
		return false
	}
	capable, ok := p.(providers.NativeWebSearchEvidenceProvider)
	if ok && capable.SupportsNativeWebSearchEvidence() {
		return true
	}
	_, streaming := p.(academicStreamingProvider)
	return streaming
}

func (a *Academic) effectiveSearchDiscipline(p academicProvider, d searchDiscipline) searchDiscipline {
	if !a.supportsSearchDiscipline(p) {
		d.RequireWebSearch = false
		d.MaxAttempts = 0
		d.MaxNativeSearchCallsPerTurn = 0
		return d
	}
	if d.MaxNativeSearchCallsPerTurn <= 0 {
		d.MaxNativeSearchCallsPerTurn = a.config.MaxNativeWebSearchCalls
	}
	if !d.RequireWebSearch {
		return d
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = a.config.SearchDisciplineMaxAttempts
	}
	return d
}

func hardNativeSearchBudget(softLimit int) int {
	if softLimit <= 0 {
		return 0
	}
	budget := softLimit * searchPlateauEmergencyMultiplier
	if budget < searchPlateauMinimumEmergency {
		budget = searchPlateauMinimumEmergency
	}
	return budget
}

func requiredSearchDiscipline(maxAttempts int) searchDiscipline {
	return searchDiscipline{
		RequireWebSearch:            true,
		MaxAttempts:                 maxAttempts,
		MaxNativeSearchCallsPerTurn: 0,
	}
}

func searchReminderMessage() string {
	return "Do not answer yet. First use `web_search` to discover current authoritative external sources for this request. Base your answer on that search-backed evidence rather than memory alone."
}

func pendingResearchActionReminderMessage() string {
	return "Do not stop after narrating pending research work. If you still need evidence, emit the actual tool call now (`web_fetch`, `fetch_document`, `crawl_links`, `consult`, or another justified step). Otherwise, answer from the evidence you already gathered. Do not reply with text like 'I'm fetching' or 'I'm about to consult' unless you also make the tool call."
}

func searchDisciplineFailure() error {
	return fmt.Errorf("academic required web_search before answering, but the model never performed one")
}

func pendingResearchActionFailure() error {
	return fmt.Errorf("academic described pending research work without emitting the corresponding tool call")
}

func excessiveNativeWebSearchError(observed, maxAllowed int) error {
	return fmt.Errorf("academic failed to converge after %d native web_search calls in a single research turn (emergency stop at %d)", observed, maxAllowed)
}

func searchRunawayReminderMessage(scores researchProgressScores) string {
	return fmt.Sprintf(
		"Stop using `web_search` for now. Research growth has plateaued (breadth %.2f, depth %.2f, grounding %.2f, corroboration %.2f, confidence %.2f). Synthesize from the search-backed evidence you already gathered. If you already identified an authoritative URL, use `web_fetch` or `fetch_document` instead of another search, then answer.",
		scores.Breadth,
		scores.Depth,
		scores.Grounding,
		scores.Corroboration,
		scores.Confidence,
	)
}

func searchProgressReminderMessage(scores researchProgressScores) string {
	return fmt.Sprintf(
		"Do not reply with a status update like 'still searching'. Continue the research turn productively: either do one more targeted search/fetch if it will materially improve breadth, depth, grounding, corroboration, or confidence (currently breadth %.2f, depth %.2f, grounding %.2f, corroboration %.2f, confidence %.2f), or synthesize the search-backed evidence you already have into a concrete answer now.",
		scores.Breadth,
		scores.Depth,
		scores.Grounding,
		scores.Corroboration,
		scores.Confidence,
	)
}

func groundingPhaseReminderMessage(scores researchProgressScores) string {
	return fmt.Sprintf(
		"Pause broad `web_search`. Discovery is sufficient for now. Pick the strongest authoritative URL you already surfaced and ground the answer by ingesting a primary source: use `web_fetch`, `fetch_document`, or one bounded `crawl_links` follow-up against the authoritative page you already found. Do not do another broad search until you have ingested at least one primary source. Current research status: breadth %.2f, grounding %.2f, corroboration %.2f, confidence %.2f.",
		scores.Breadth,
		scores.Grounding,
		scores.Corroboration,
		scores.Confidence,
	)
}

func corroborationPhaseReminderMessage(scores researchProgressScores) string {
	return fmt.Sprintf(
		"You already have grounded material. Before answering, strengthen it: fetch one more authoritative source, follow a bounded official link with `crawl_links`, or consult Librarian/Archivalist if codebase fit or historical precedent matters. Prefer corroborating or resolving ambiguity over another broad `web_search`. Current research status: breadth %.2f, grounding %.2f, corroboration %.2f, confidence %.2f.",
		scores.Breadth,
		scores.Grounding,
		scores.Corroboration,
		scores.Confidence,
	)
}

func synthesisPhaseReminderMessage(scores researchProgressScores) string {
	return fmt.Sprintf(
		"Stop searching and synthesize now. More retrieval is unlikely to change the conclusion materially. Base the answer on the grounded evidence you already gathered, and only do another fetch or consult if it will directly resolve a concrete ambiguity. Current research status: breadth %.2f, depth %.2f, grounding %.2f, corroboration %.2f, confidence %.2f.",
		scores.Breadth,
		scores.Depth,
		scores.Grounding,
		scores.Corroboration,
		scores.Confidence,
	)
}

func researchPhaseReminderMessage(phase researchPhase, scores researchProgressScores) string {
	switch phase {
	case researchPhaseGround:
		return groundingPhaseReminderMessage(scores)
	case researchPhaseCorroborate:
		return corroborationPhaseReminderMessage(scores)
	case researchPhaseSynthesize:
		return synthesisPhaseReminderMessage(scores)
	default:
		return searchProgressReminderMessage(scores)
	}
}

func isNativeWebSearchChunk(call *providers.ToolCallChunk) bool {
	if call == nil {
		return false
	}
	if call.Kind == providers.ToolKindNativeWebSearch {
		return true
	}
	return strings.TrimSpace(call.Name) == "web_search"
}

func fetchNeedsSearch(input string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(input))
	return !strings.Contains(trimmed, "http://") && !strings.Contains(trimmed, "https://")
}

func shouldRetryResearchTurn(resp *providers.Response, tracker *searchEvidenceTracker) bool {
	if resp == nil || tracker == nil || !tracker.sawSearch || len(resp.ToolCalls) != 0 {
		return false
	}
	switch tracker.currentPhase() {
	case researchPhaseGround:
		return !tracker.hasGroundedEvidence()
	case researchPhaseCorroborate, researchPhaseSynthesize:
		return responseLooksLikeResearchStatus(resp.Content)
	}
	return responseLooksLikeResearchStatus(resp.Content)
}

func responseLooksLikeResearchStatus(content string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(content))
	if trimmed == "" {
		return true
	}
	trimmed = strings.NewReplacer(
		"`", "",
		"’", "'",
		"“", "\"",
		"”", "\"",
	).Replace(trimmed)
	phrases := []string{
		"still searching",
		"still researching",
		"searching again",
		"looking for more",
		"need to search",
		"let me search",
		"gathering more sources",
		"researching further",
		"still fetching",
		"fetching now",
		"let me fetch",
		"i'm fetching",
		"i am fetching",
		"i'll fetch",
		"i will fetch",
		"about to fetch",
		"executing web_fetch",
		"executing fetch_document",
		"executing crawl_links",
		"executing a web fetch",
		"running web_fetch",
		"running fetch_document",
		"running crawl_links",
		"opening the page",
		"opening this page",
		"let me open the page",
		"consulting librarian",
		"consulting the librarian",
		"consulting archivalist",
		"consulting the archivalist",
		"i'll consult",
		"i will consult",
		"cloning the repository",
		"cloning the repo",
	}
	for _, phrase := range phrases {
		if strings.Contains(trimmed, phrase) {
			return true
		}
	}
	return false
}

func (t *searchEvidenceTracker) observeNativeSearchCall(key string, call providers.NativeWebSearchCall) {
	if t == nil {
		return
	}
	if t.turnStartPhase != researchPhaseDiscover && t.currentPhase() != researchPhaseDiscover && strings.EqualFold(strings.TrimSpace(call.Action), "search") {
		t.runawayErr = fmt.Errorf("academic phase policy rejected provider-native web_search action %q after discovery; use web_fetch, fetch_document, crawl_links, or a direct page-open grounding step instead", call.Action)
		return
	}
	key = t.turnScopedKey(strings.TrimSpace(key))
	if key == "" {
		key = t.turnScopedKey("native_web_search_unknown")
	}

	existing := t.nativeSearchCalls[key]
	merged := mergeNativeSearchCall(existing, call)
	t.nativeSearchCalls[key] = merged
	t.sawSearch = true

	if _, ok := t.seenCallKeys[key]; !ok {
		t.seenCallKeys[key] = struct{}{}
	}

	if _, ok := t.scoredCallKeys[key]; !ok {
		if t.recordSearchNovelty(merged.Query) {
			t.scoredCallKeys[key] = struct{}{}
		}
	}
	if t.observeNativeSearchGrounding(merged) {
		t.plateauCooldown = searchPlateauSpikeCooldown
		t.recordProgressObservation()
	}

	t.enforceSearchBudget()
	t.advancePhase(t.currentScores())
}

func mergeNativeSearchCall(existing, incoming providers.NativeWebSearchCall) providers.NativeWebSearchCall {
	merged := existing
	if strings.TrimSpace(incoming.ID) != "" {
		merged.ID = incoming.ID
	}
	if strings.TrimSpace(incoming.Provider) != "" {
		merged.Provider = incoming.Provider
	}
	if strings.TrimSpace(incoming.Status) != "" {
		merged.Status = incoming.Status
	}
	if strings.TrimSpace(incoming.Action) != "" {
		merged.Action = incoming.Action
	}
	if strings.TrimSpace(incoming.Query) != "" {
		merged.Query = incoming.Query
	}
	if strings.TrimSpace(incoming.URL) != "" {
		merged.URL = incoming.URL
	}
	if strings.TrimSpace(incoming.Pattern) != "" {
		merged.Pattern = incoming.Pattern
	}
	return merged
}

func (t *searchEvidenceTracker) observeNativeSearchGrounding(call providers.NativeWebSearchCall) bool {
	if t == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(call.Action)) {
	case "open_page", "find_in_page":
	default:
		return false
	}
	rawURL := strings.TrimSpace(call.URL)
	if rawURL == "" {
		return false
	}
	changed := false
	if _, ok := t.fetchedURLs[rawURL]; !ok {
		t.fetchedURLs[rawURL] = struct{}{}
		changed = true
	}
	t.recordEvidenceClassesForURL(rawURL)
	if domain := domainFromURL(rawURL); domain != "" {
		if _, ok := t.fetchedDomains[domain]; !ok {
			t.fetchedDomains[domain] = struct{}{}
			changed = true
		}
	}
	return changed
}

func nativeWebSearchCallFromToolChunk(chunk *providers.ToolCallChunk) providers.NativeWebSearchCall {
	call := providers.NativeWebSearchCall{}
	if chunk == nil {
		return call
	}
	call.ID = strings.TrimSpace(chunk.ID)
	args := strings.TrimSpace(chunk.ArgumentsDelta)
	if args == "" || args == "{}" {
		return call
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return call
	}
	if query, ok := payload["query"].(string); ok {
		call.Query = strings.TrimSpace(query)
	}
	if action, ok := payload["action"].(string); ok {
		call.Action = strings.TrimSpace(action)
	}
	if rawURL, ok := payload["url"].(string); ok {
		call.URL = strings.TrimSpace(rawURL)
	}
	if pattern, ok := payload["pattern"].(string); ok {
		call.Pattern = strings.TrimSpace(pattern)
	}
	if status, ok := payload["status"].(string); ok {
		call.Status = strings.TrimSpace(status)
	}
	return call
}

func (t *searchEvidenceTracker) recordSearchNovelty(query string) bool {
	if t == nil {
		return false
	}
	fingerprint, tokens := normalizeSearchQuery(query)
	if fingerprint == "" || len(tokens) == 0 {
		return false
	}
	novelty := 0.0
	if len(t.queryFingerprints) == 0 {
		novelty = 1.0
	} else if _, exists := t.queryFingerprints[fingerprint]; !exists {
		novelty = 1 - t.maxQuerySimilarity(tokens)
	}
	if novelty < 0 {
		novelty = 0
	}
	t.queryFingerprints[fingerprint] = tokens
	t.searchNoveltyMass += novelty
	t.recordProgressObservation()
	return true
}

func (t *searchEvidenceTracker) maxQuerySimilarity(tokens map[string]struct{}) float64 {
	maxSimilarity := 0.0
	for _, existing := range t.queryFingerprints {
		if sim := jaccardSimilarity(tokens, existing); sim > maxSimilarity {
			maxSimilarity = sim
		}
	}
	return maxSimilarity
}

func normalizeSearchQuery(query string) (string, map[string]struct{}) {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return unicode.ToLower(r)
		case unicode.IsSpace(r):
			return ' '
		default:
			return ' '
		}
	}, query)
	fields := strings.Fields(normalized)
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		tokens[field] = struct{}{}
	}
	if len(tokens) == 0 {
		return "", nil
	}
	ordered := make([]string, 0, len(tokens))
	for token := range tokens {
		ordered = append(ordered, token)
	}
	sort.Strings(ordered)
	fingerprint := strings.Join(ordered, " ")
	return fingerprint, tokens
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	union := len(a)
	for token := range b {
		if _, ok := a[token]; ok {
			intersection++
			continue
		}
		union++
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func (t *searchEvidenceTracker) observeToolResult(call providers.ToolCall, result string, isError bool) {
	if t == nil || isError {
		return
	}
	name := strings.TrimSpace(call.Name)
	observed := false
	switch name {
	case "web_fetch", "fetch_document", "crawl_links":
		observed = true
	case "consult", "clone_via_librarian":
		observed = true
	default:
		if strings.HasPrefix(name, "consult_") {
			observed = true
		} else {
			return
		}
	}
	if !toolResultSucceeded(result) || !observed {
		return
	}
	changed := false
	switch name {
	case "web_fetch", "fetch_document", "crawl_links":
		if rawURL := extractToolURL(call.Arguments, result); rawURL != "" {
			if _, ok := t.fetchedURLs[rawURL]; !ok {
				t.fetchedURLs[rawURL] = struct{}{}
				changed = true
			}
			t.recordEvidenceClassesForURL(rawURL)
			if domain := domainFromURL(rawURL); domain != "" {
				if _, ok := t.fetchedDomains[domain]; !ok {
					t.fetchedDomains[domain] = struct{}{}
					changed = true
				}
			}
			if name == "fetch_document" {
				if _, ok := t.fetchedDocuments[rawURL]; !ok {
					t.fetchedDocuments[rawURL] = struct{}{}
					changed = true
				}
			}
		}
		if name == "crawl_links" {
			t.crawlRuns++
			changed = true
		}
	default:
		changed = t.observeConsultEvidence(call, name, result)
	}
	if changed {
		t.plateauCooldown = searchPlateauSpikeCooldown
	}
	t.recordProgressObservation()
}

func toolResultSucceeded(result string) bool {
	raw := strings.TrimSpace(result)
	if raw == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return true
	}
	if success, ok := payload["success"].(bool); ok {
		return success
	}
	return true
}

func extractToolURL(arguments string, result string) string {
	if rawURL := extractStringField(arguments, "url"); rawURL != "" {
		return rawURL
	}
	return extractStringField(result, "url")
}

func extractStringField(raw string, field string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	value, _ := payload[field].(string)
	return strings.TrimSpace(value)
}

func (t *searchEvidenceTracker) observeConsultEvidence(call providers.ToolCall, name, result string) bool {
	if t == nil {
		return false
	}
	target := consultationTarget(call, name, result)
	changed := false
	if target != "" {
		if _, ok := t.consultTargets[target]; !ok {
			t.consultTargets[target] = struct{}{}
			changed = true
		}
		switch target {
		case "librarian":
			t.recordEvidenceClass(academicEvidenceCodebaseFit)
		case "archivalist":
			t.recordEvidenceClass(academicEvidenceHistoricalPrecedent)
		}
	}
	fingerprint, tokens := normalizeConsultationEvidence(result, target)
	if fingerprint == "" || len(tokens) == 0 {
		return changed
	}
	novelty := 0.0
	if len(t.consultFingerprints) == 0 {
		novelty = 1.0
	} else if _, exists := t.consultFingerprints[fingerprint]; !exists {
		novelty = 1 - t.maxConsultSimilarity(tokens)
	}
	if novelty < 0 {
		novelty = 0
	}
	if _, exists := t.consultFingerprints[fingerprint]; !exists {
		t.consultFingerprints[fingerprint] = tokens
	}
	if novelty > 0 {
		t.consultNoveltyMass += novelty
		changed = true
	}
	return changed
}

func (t *searchEvidenceTracker) maxConsultSimilarity(tokens map[string]struct{}) float64 {
	maxSimilarity := 0.0
	for _, existing := range t.consultFingerprints {
		if sim := jaccardSimilarity(tokens, existing); sim > maxSimilarity {
			maxSimilarity = sim
		}
	}
	return maxSimilarity
}

func consultationTarget(call providers.ToolCall, name, result string) string {
	if raw := extractStringField(call.Arguments, "target"); raw != "" {
		return strings.ToLower(raw)
	}
	if raw := extractStringField(result, "target"); raw != "" {
		return strings.ToLower(raw)
	}
	if name == "clone_via_librarian" {
		return "librarian"
	}
	if strings.HasPrefix(name, "consult_") {
		return strings.ToLower(strings.TrimPrefix(name, "consult_"))
	}
	return ""
}

func normalizeConsultationEvidence(result, target string) (string, map[string]struct{}) {
	raw := strings.TrimSpace(result)
	if raw == "" {
		return normalizeSearchQuery(target)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return normalizeSearchQuery(target + " " + raw)
	}
	if success, ok := payload["success"].(bool); ok && !success {
		return "", nil
	}
	parts := []string{}
	if trimmed := strings.TrimSpace(target); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if data, ok := payload["data"]; ok {
		parts = append(parts, collectEvidenceText(data)...)
	} else {
		parts = append(parts, collectEvidenceText(payload)...)
	}
	return normalizeSearchQuery(strings.Join(parts, " "))
}

func collectEvidenceText(value any) []string {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, collectEvidenceText(item)...)
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			switch key {
			case "success", "ok", "error", "target":
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]string, 0, len(keys))
		for _, key := range keys {
			out = append(out, collectEvidenceText(typed[key])...)
		}
		return out
	default:
		return nil
	}
}

func domainFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		if reparsed, retryErr := url.Parse("https://" + rawURL); retryErr == nil {
			parsed = reparsed
		}
	}
	host := strings.TrimSpace(parsed.Hostname())
	return strings.ToLower(host)
}

func (t *searchEvidenceTracker) recordProgressObservation() {
	if t == nil {
		return
	}
	scores := t.currentScores()
	delta := scores.Total - t.lastTotalScore
	if delta < 0 {
		delta = 0
	}
	t.lastTotalScore = scores.Total
	deltaConfidence := scores.Confidence - t.lastScores.Confidence
	if deltaConfidence < 0 {
		deltaConfidence = 0
	}
	deltaReadiness := scores.Readiness - t.lastScores.Readiness
	if deltaReadiness < 0 {
		deltaReadiness = 0
	}
	t.lastScores = scores
	t.observations = append(t.observations, researchObservation{
		Breadth:         scores.Breadth,
		Depth:           scores.Depth,
		Grounding:       scores.Grounding,
		Corroboration:   scores.Corroboration,
		Confidence:      scores.Confidence,
		Readiness:       scores.Readiness,
		Total:           scores.Total,
		DeltaTotal:      delta,
		DeltaConfidence: deltaConfidence,
		DeltaReadiness:  deltaReadiness,
	})
	if len(t.observations) > 12 {
		t.observations = append([]researchObservation(nil), t.observations[len(t.observations)-12:]...)
	}
	if delta >= searchPlateauMaterialGrowth || deltaConfidence >= searchPlateauMaterialConfidence || deltaReadiness >= searchPlateauMaterialReadiness {
		t.plateauVotes = 0
		t.plateauCooldown = searchPlateauSpikeCooldown
		t.synthesizeNow = false
	}
	t.evaluatePlateau(scores)
	t.advancePhase(scores)
}

func (t *searchEvidenceTracker) evaluatePlateau(scores researchProgressScores) {
	if t == nil || t.synthesizeNow || t.maxCallsPerTurn <= 0 {
		return
	}
	if t.plateauCooldown > 0 {
		t.plateauCooldown--
		return
	}
	if len(t.observations) < searchPlateauWindow {
		return
	}
	window := t.observations[len(t.observations)-searchPlateauWindow:]
	avgDelta, maxDelta, stddevDelta, avgConfidenceDelta, avgReadinessDelta := summarizePlateauWindow(window)
	flatWindow := avgDelta <= searchPlateauAverageDeltaMax &&
		maxDelta <= searchPlateauMaxDelta &&
		avgConfidenceDelta <= searchPlateauAverageConfidenceMax &&
		avgReadinessDelta <= searchPlateauAverageReadinessMax &&
		stddevDelta <= searchPlateauVolatilityMax
	if !flatWindow {
		if t.plateauVotes > 0 {
			t.plateauVotes--
		}
		return
	}
	readyEnough := t.readinessReached(scores)
	saturated := t.overBudget && len(t.seenCallKeys) >= max(t.maxCallsPerTurn+1, searchPlateauWindow)
	if readyEnough || saturated {
		t.plateauVotes++
	} else if t.plateauVotes > 0 {
		t.plateauVotes--
	}
	if t.plateauVotes >= searchPlateauVoteThreshold {
		switch {
		case t.discoverySufficientForGrounding():
			t.phase = researchPhaseGround
			t.promptQueued = true
		case t.shouldCorroborate(scores):
			t.phase = researchPhaseCorroborate
			t.promptQueued = true
		default:
			t.synthesizeNow = true
			t.phase = researchPhaseSynthesize
			t.promptQueued = true
		}
	}
}

func (t *searchEvidenceTracker) evidenceSurfaceCount() int {
	if t == nil {
		return 0
	}
	return len(t.queryFingerprints) + len(t.fetchedDomains) + len(t.fetchedDocuments) + len(t.consultTargets)
}

func (t *searchEvidenceTracker) hasGroundedEvidence() bool {
	if t == nil {
		return false
	}
	return len(t.fetchedURLs) > 0 || len(t.fetchedDocuments) > 0 || t.crawlRuns > 0
}

func (t *searchEvidenceTracker) hasExternalEvidenceBeyondSearch() bool {
	if t == nil {
		return false
	}
	return t.hasGroundedEvidence() || len(t.consultTargets) > 0
}

func (t *searchEvidenceTracker) hasEvidenceClass(class academicEvidenceClass) bool {
	if t == nil {
		return false
	}
	switch class {
	case academicEvidenceExternalGrounding:
		return t.hasGroundedEvidence() || t.evidenceClasses[class] > 0
	default:
		return t.evidenceClasses[class] > 0
	}
}

func (t *searchEvidenceTracker) evidenceModeCount() int {
	if t == nil {
		return 0
	}
	count := 0
	if t.sawSearch {
		count++
	}
	if len(t.fetchedDomains) > 0 || len(t.fetchedURLs) > 0 {
		count++
	}
	if len(t.fetchedDocuments) > 0 {
		count++
	}
	if len(t.consultTargets) > 0 {
		count++
	}
	return count
}

func (t *searchEvidenceTracker) discoverySufficientForGrounding() bool {
	if t == nil || !t.sawSearch || t.hasGroundedEvidence() {
		return false
	}
	if len(t.seenCallKeys) >= searchGroundingTransitionCalls {
		return true
	}
	return len(t.queryFingerprints) >= searchGroundingTransitionQueries
}

func (t *searchEvidenceTracker) shouldCorroborate(scores researchProgressScores) bool {
	if t == nil || !t.hasGroundedEvidence() || t.synthesizeNow {
		return false
	}
	if t.readinessReached(scores) {
		return false
	}
	if scores.Grounding < searchPlateauMinGrounding {
		return false
	}
	return scores.Corroboration < searchPlateauMinCorroboration || len(t.consultTargets) == 0
}

func (t *searchEvidenceTracker) currentPhase() researchPhase {
	if t == nil || t.phase == "" {
		return researchPhaseDiscover
	}
	return t.phase
}

func (t *searchEvidenceTracker) advancePhase(scores researchProgressScores) {
	if t == nil {
		return
	}
	next := researchPhaseDiscover
	switch {
	case t.synthesizeNow:
		next = researchPhaseSynthesize
	case t.discoverySufficientForGrounding():
		next = researchPhaseGround
	case t.shouldCorroborate(scores):
		next = researchPhaseCorroborate
	}
	if next == t.phase {
		return
	}
	t.phase = next
	if next == researchPhaseGround || next == researchPhaseSynthesize {
		t.promptQueued = true
	}
}

func (t *searchEvidenceTracker) consumeQueuedPrompt() (researchPhase, bool) {
	if t == nil || !t.promptQueued {
		return researchPhaseDiscover, false
	}
	t.promptQueued = false
	return t.currentPhase(), true
}

func (t *searchEvidenceTracker) queuePromptForCurrentPhase() {
	if t == nil {
		return
	}
	switch t.currentPhase() {
	case researchPhaseGround, researchPhaseCorroborate, researchPhaseSynthesize:
		t.promptQueued = true
	}
}

func (t *searchEvidenceTracker) readinessReached(scores researchProgressScores) bool {
	if t == nil {
		return false
	}
	if t.completionContract != nil && !t.completionContract.requiredEvidenceSatisfied(t) {
		return false
	}
	if scores.Readiness < searchPlateauMinReadiness ||
		scores.Confidence < searchPlateauMinConfidence ||
		scores.Breadth < searchPlateauMinBreadth ||
		scores.Depth < searchPlateauMinDepth ||
		scores.Grounding < searchPlateauMinGrounding ||
		scores.Corroboration < searchPlateauMinCorroboration {
		return false
	}
	if t.evidenceModeCount() < searchPlateauMinSurfaceModes {
		return false
	}
	return t.evidenceSurfaceCount() >= searchPlateauMinSurfaceCount
}

func (t *searchEvidenceTracker) recordEvidenceClass(class academicEvidenceClass) {
	if t == nil || strings.TrimSpace(string(class)) == "" {
		return
	}
	t.evidenceClasses[class]++
}

func (t *searchEvidenceTracker) recordEvidenceClassesForURL(rawURL string) {
	if t == nil {
		return
	}
	t.recordEvidenceClass(academicEvidenceExternalGrounding)
	for _, class := range classifyEvidenceClassesForURL(rawURL) {
		t.recordEvidenceClass(class)
	}
}

func classifyEvidenceClassesForURL(rawURL string) []academicEvidenceClass {
	host := domainFromURL(rawURL)
	if host == "" {
		return []academicEvidenceClass{academicEvidenceExternalGrounding}
	}
	classes := []academicEvidenceClass{academicEvidenceExternalGrounding}
	switch {
	case isResearchPaperHost(host, rawURL):
		classes = append(classes, academicEvidenceResearchPapers)
	case isReferenceRepositoryHost(host):
		classes = append(classes, academicEvidenceReferenceRepos)
	case isStandardsHost(host, rawURL):
		classes = append(classes, academicEvidenceStandardsOrSpecs)
	case isExpertArticleHost(host):
		classes = append(classes, academicEvidenceExpertArticles)
	default:
		if isOfficialDocsHost(host, rawURL) {
			classes = append(classes, academicEvidenceOfficialDocs)
		}
	}
	return dedupeEvidenceClasses(classes)
}

func isResearchPaperHost(host, rawURL string) bool {
	switch {
	case strings.Contains(host, "arxiv.org"),
		strings.Contains(host, "doi.org"),
		strings.Contains(host, "dl.acm.org"),
		strings.Contains(host, "ieeexplore.ieee.org"),
		strings.Contains(host, "springer.com"),
		strings.Contains(host, "nature.com"),
		strings.Contains(host, "sciencedirect.com"),
		strings.Contains(host, "usenix.org"),
		strings.Contains(host, "aclanthology.org"),
		strings.Contains(host, "openreview.net"),
		strings.Contains(host, "proceedings.neurips.cc"),
		strings.Contains(host, "papers.nips.cc"):
		return true
	}
	lowered := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.HasSuffix(lowered, ".pdf") && containsAny(lowered, "paper", "proceedings", "arxiv", "doi")
}

func isReferenceRepositoryHost(host string) bool {
	return containsAny(host, "github.com", "gitlab.com", "bitbucket.org", "sr.ht", "sourcehut.org")
}

func isStandardsHost(host, rawURL string) bool {
	if containsAny(host, "ietf.org", "rfc-editor.org", "w3.org", "ecma-international.org", "unicode.org", "iso.org", "opengroup.org") {
		return true
	}
	lowered := strings.ToLower(strings.TrimSpace(rawURL))
	return containsAny(lowered, "/rfc", "/spec", "/specification", "/standard")
}

func isExpertArticleHost(host string) bool {
	return strings.HasPrefix(host, "blog.") ||
		strings.HasPrefix(host, "engineering.") ||
		containsAny(host, "medium.com", "substack.com", "dev.to", "hashnode.dev")
}

func isOfficialDocsHost(host, rawURL string) bool {
	if strings.HasPrefix(host, "docs.") || strings.HasPrefix(host, "developer.") || strings.HasPrefix(host, "pkg.go.dev") || strings.HasPrefix(host, "docs.rs") {
		return true
	}
	if containsAny(host, "go.dev", "python.org", "rust-lang.org", "nodejs.org", "kubernetes.io", "react.dev", "openai.com") {
		return true
	}
	lowered := strings.ToLower(strings.TrimSpace(rawURL))
	return containsAny(lowered, "/docs/", "/doc/", "/guide/", "/reference/")
}

func summarizePlateauWindow(window []researchObservation) (avgDelta, maxDelta, stddevDelta, avgConfidenceDelta, avgReadinessDelta float64) {
	if len(window) == 0 {
		return 0, 0, 0, 0, 0
	}
	sumDelta := 0.0
	sumConfidence := 0.0
	sumReadiness := 0.0
	for _, observation := range window {
		sumDelta += observation.DeltaTotal
		sumConfidence += observation.DeltaConfidence
		sumReadiness += observation.DeltaReadiness
		if observation.DeltaTotal > maxDelta {
			maxDelta = observation.DeltaTotal
		}
	}
	avgDelta = sumDelta / float64(len(window))
	avgConfidenceDelta = sumConfidence / float64(len(window))
	avgReadinessDelta = sumReadiness / float64(len(window))
	variance := 0.0
	for _, observation := range window {
		diff := observation.DeltaTotal - avgDelta
		variance += diff * diff
	}
	stddevDelta = math.Sqrt(variance / float64(len(window)))
	return avgDelta, maxDelta, stddevDelta, avgConfidenceDelta, avgReadinessDelta
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}
