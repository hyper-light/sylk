package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const consultationDeliberationMetadataKey = "consultation_deliberation"

var consultationDeliberationRegistry sync.Map

type consultationSnapshot struct {
	Version           int    `json:"version"`
	RootCorrelationID string `json:"root_correlation_id,omitempty"`
	RootAgentID       string `json:"root_agent_id,omitempty"`
	RootAgentType     string `json:"root_agent_type,omitempty"`
	CurrentDepth      int    `json:"current_depth,omitempty"`
	ResearchDepth     string `json:"research_depth,omitempty"`
}

type consultationObservation struct {
	AttemptID    string  `json:"attempt_id,omitempty"`
	Target       string  `json:"target,omitempty"`
	Fingerprint  string  `json:"fingerprint,omitempty"`
	Novelty      float64 `json:"novelty,omitempty"`
	Reward       float64 `json:"reward,omitempty"`
	Allowed      bool    `json:"allowed,omitempty"`
	OutcomeKnown bool    `json:"outcome_known,omitempty"`

	// SessionID enables cross-turn cache lookup via
	// SessionConsultationCache. Carried from the request that
	// produced the attempt so the cache can scope hits to the
	// correct session without scraping context.
	SessionID string `json:"session_id,omitempty"`

	// Query is the verbatim user-facing query string. The fingerprint
	// is derived from it but we keep the original for telemetry, debug,
	// and reconstruction of cached evidence at serve-time.
	Query string `json:"query,omitempty"`

	// Scope is the consult's local surface area. Cache lookups require
	// the requested scope to match the stored scope so a similar query
	// for a different package, file, or subsystem does not reuse stale
	// evidence from the wrong part of the repository.
	Scope string `json:"scope,omitempty"`

	// Response payload from a successful consultation. Stored at
	// outcome-record time so the session cache can serve the answer
	// back without a fresh bus round-trip when the existing pressure
	// math says re-asking would be denied. Nil when the consultation
	// failed or the cache layer was disabled.
	Response any `json:"response,omitempty"`

	// FreshnessHorizon is the agent-self-reported duration over
	// which this answer remains valid. Cache lookups treat entries
	// older than (StoredAt + FreshnessHorizon) as stale and force a
	// re-ask. A zero or unset horizon is treated as "expires
	// immediately" — the cache will not serve it. This makes the
	// freshness contract explicit: agents either commit to a
	// horizon or their answers are not eligible for cached re-use.
	FreshnessHorizon time.Duration `json:"freshness_horizon,omitempty"`

	// StoredAt is the wall-clock time the response was recorded
	// (post-RecordConsultationOutcome). Used together with
	// FreshnessHorizon for staleness checks.
	StoredAt time.Time `json:"stored_at,omitempty"`
}

type consultationTargetStats struct {
	Count  int     `json:"count,omitempty"`
	Reward float64 `json:"reward,omitempty"`
}

type consultationLedger struct {
	mu              sync.Mutex
	rootCorrelation string
	consultCount    int
	totalReward     float64
	targets         map[string]*consultationTargetStats
	recent          []consultationObservation
}

type ConsultationAdmission struct {
	AttemptID       string
	Allowed         bool
	Guidance        string
	Metadata        map[string]any
	RootCorrelation string
	ResearchDepth   ResearchDepth
	Depth           int
	ConsultCount    int
	TargetCount     int
	Similarity      float64
	Novelty         float64
	Penalty         float64
	ExpectedGain    float64
}

type ConsultationPressureError struct {
	Message  string
	Recovery []string
}

func (e *ConsultationPressureError) Error() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Message)
}

func ConsultationPressureRecovery(err error) []string {
	var pressureErr *ConsultationPressureError
	if !errorsAs(err, &pressureErr) || pressureErr == nil {
		return nil
	}
	return append([]string(nil), pressureErr.Recovery...)
}

// admissionInputs captures the per-attempt quantities the pressure
// math operates over. Extracted as a value type so the cache layer
// can synthesize a virtual ledger view of "what would admission say
// if the cached observations were the only history?" without
// duplicating the math.
type admissionInputs struct {
	Target          string
	Fingerprint     string
	Tokens          map[string]struct{}
	NextDepth       int
	ResearchDepth   ResearchDepth
	Similarity      float64
	TargetCount     int
	ConsultCount    int
	DistinctTargets int
	GlobalReward    float64
	TargetReward    float64
}

// admissionVerdict is the pure-math output of evaluateAdmission. It
// mirrors the subset of ConsultationAdmission that doesn't require
// ledger mutation, so callers can compare "would this be admitted?"
// without recording an attempt.
type admissionVerdict struct {
	Allowed      bool
	Novelty      float64
	Penalty      float64
	ExpectedGain float64
}

// evaluateAdmission is the pure pressure decision: takes the
// per-attempt inputs and returns the verdict. Both AdmitConsultation
// (live throttling, mutating the per-request ledger) and the cache
// layer (non-mutating dedup against the per-session cache) call this
// so the "should we consult?" decision uses exactly one math path.
//
// Why pure: cache lookup needs to ask "if these cached entries WERE
// the ledger, would re-asking be admitted?" without recording a new
// attempt against the live ledger. Returning a verdict struct (not
// taking the ledger as a side-effect parameter) keeps the dedup
// query free of throttling state mutation.
func evaluateAdmission(in admissionInputs) admissionVerdict {
	priorCredit := consultationDepthCredit(in.ResearchDepth)
	novelty := clamp01(1 - in.Similarity)
	depthBudget := 1 + priorCredit + in.GlobalReward
	volumeBudget := 1 + priorCredit + float64(in.DistinctTargets) + in.GlobalReward
	repeatBudget := 1 + (priorCredit / 2) + in.TargetReward
	depthExcess := positivePart(float64(in.NextDepth) - depthBudget)
	volumeExcess := positivePart(float64(in.ConsultCount) - volumeBudget)
	repeatExcess := positivePart(float64(in.TargetCount) - repeatBudget)
	questionPenalty := in.Similarity * float64(in.TargetCount)
	penalty := (depthExcess * depthExcess) + (volumeExcess * volumeExcess) + (repeatExcess * repeatExcess) + questionPenalty
	expectedGain := novelty * (1 + priorCredit + in.GlobalReward + in.TargetReward)
	return admissionVerdict{
		Allowed:      penalty <= expectedGain,
		Novelty:      novelty,
		Penalty:      penalty,
		ExpectedGain: expectedGain,
	}
}

func AdmitConsultation(ctx context.Context, target, query string, metadata map[string]any) ConsultationAdmission {
	target = strings.ToLower(strings.TrimSpace(target))
	query = strings.TrimSpace(query)

	snapshot := consultationSnapshotFromMetadata(metadata)
	if snapshot.RootCorrelationID == "" {
		if stream, ok := StreamMetadataFromContext(ctx); ok {
			if inherited := consultationSnapshotFromMetadata(stream.Metadata); inherited.RootCorrelationID != "" {
				snapshot = inherited
			}
		}
	}
	if snapshot.RootCorrelationID == "" {
		snapshot.RootCorrelationID = firstNonEmptyInline(
			consultationStreamCorrelationID(ctx),
			consultationLogCorrelationID(ctx),
		)
	}
	if snapshot.RootAgentID == "" {
		snapshot.RootAgentID = firstNonEmptyInline(consultationLogAgentID(ctx), consultationStreamAgentID(ctx))
	}
	if snapshot.RootAgentType == "" {
		snapshot.RootAgentType = firstNonEmptyInline(
			consultationStreamMetadataStringFromContext(ctx, "agent_type"),
			consultationStreamMetadataStringFromContext(ctx, "source_agent_type"),
		)
	}
	if snapshot.ResearchDepth == "" {
		snapshot.ResearchDepth = string(ConsultationResearchDepth(metadata))
	}
	researchDepth := EffectiveResearchDepth(snapshot.ResearchDepth)
	ledger := consultationLedgerForRoot(snapshot.RootCorrelationID)

	fingerprint, tokens := normalizeConsultationQuery(query)
	inputs := admissionInputs{
		Target:          target,
		Fingerprint:     fingerprint,
		Tokens:          tokens,
		NextDepth:       snapshot.CurrentDepth + 1,
		ResearchDepth:   researchDepth,
		Similarity:      ledger.maxSimilarity(target, fingerprint, tokens),
		TargetCount:     ledger.targetCount(target) + 1,
		ConsultCount:    ledger.count() + 1,
		DistinctTargets: ledger.distinctTargetCount(),
		GlobalReward:    ledger.reward(),
		TargetReward:    ledger.targetReward(target),
	}
	verdict := evaluateAdmission(inputs)

	attemptID := newDeliberationAttemptID()
	ledger.recordAttempt(consultationObservation{
		AttemptID:   attemptID,
		Target:      target,
		Fingerprint: fingerprint,
		Novelty:     verdict.Novelty,
		Allowed:     verdict.Allowed,
	})

	admission := ConsultationAdmission{
		AttemptID:       attemptID,
		Allowed:         verdict.Allowed,
		RootCorrelation: snapshot.RootCorrelationID,
		ResearchDepth:   researchDepth,
		Depth:           inputs.NextDepth,
		ConsultCount:    inputs.ConsultCount,
		TargetCount:     inputs.TargetCount,
		Similarity:      inputs.Similarity,
		Novelty:         verdict.Novelty,
		Penalty:         verdict.Penalty,
		ExpectedGain:    verdict.ExpectedGain,
	}
	if !verdict.Allowed {
		admission.Guidance = fmt.Sprintf(
			"Consultation pressure is too high for this branch. Expected gain %.2f is below penalty %.2f at depth %d with %d total consults. Synthesize current evidence, materially change the question, or switch to a more informative target before consulting again.",
			verdict.ExpectedGain,
			verdict.Penalty,
			inputs.NextDepth,
			inputs.ConsultCount,
		)
		return admission
	}

	childSnapshot := snapshot
	childSnapshot.CurrentDepth = inputs.NextDepth
	childSnapshot.ResearchDepth = string(researchDepth)
	admission.Metadata = consultationSnapshotIntoMetadata(metadata, childSnapshot)
	return admission
}

func RecordConsultationOutcome(ctx context.Context, attemptID string, success bool, data any, err error) {
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return
	}
	rootID := firstNonEmptyInline(
		consultationSnapshotFromContextOrMetadata(ctx).RootCorrelationID,
		consultationStreamCorrelationID(ctx),
		consultationLogCorrelationID(ctx),
	)
	ledger := consultationLedgerForRoot(rootID)
	reward := 0.0
	if success && err == nil && consultationOutcomeHasSignal(data) {
		reward = ledger.observationNovelty(attemptID)
	}
	ledger.recordOutcome(attemptID, reward)
}

func ConsultationAdmissionError(ad ConsultationAdmission) error {
	if ad.Allowed {
		return nil
	}
	return &ConsultationPressureError{
		Message: strings.TrimSpace(ad.Guidance),
		Recovery: []string{
			"Synthesize current evidence before consulting again",
			"Only re-consult after the question, target, or evidence frontier has changed materially",
			"Prefer a narrower, more novel question if another consult is truly necessary",
		},
	}
}

func consultationSnapshotFromContextOrMetadata(ctx context.Context) consultationSnapshot {
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		if snapshot := consultationSnapshotFromMetadata(stream.Metadata); snapshot.RootCorrelationID != "" {
			return snapshot
		}
	}
	return consultationSnapshot{}
}

func consultationSnapshotFromMetadata(metadata map[string]any) consultationSnapshot {
	if len(metadata) == 0 {
		return consultationSnapshot{}
	}
	raw, ok := metadata[consultationDeliberationMetadataKey]
	if !ok || raw == nil {
		return consultationSnapshot{}
	}
	switch typed := raw.(type) {
	case string:
		var snapshot consultationSnapshot
		if strings.TrimSpace(typed) == "" {
			return consultationSnapshot{}
		}
		if err := json.Unmarshal([]byte(typed), &snapshot); err != nil {
			return consultationSnapshot{}
		}
		return snapshot
	case map[string]any:
		payload, err := json.Marshal(typed)
		if err != nil {
			return consultationSnapshot{}
		}
		var snapshot consultationSnapshot
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return consultationSnapshot{}
		}
		return snapshot
	default:
		return consultationSnapshot{}
	}
}

func consultationSnapshotIntoMetadata(metadata map[string]any, snapshot consultationSnapshot) map[string]any {
	cloned := CloneMetadataMap(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 1)
	}
	snapshot.Version = 1
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return cloned
	}
	cloned[consultationDeliberationMetadataKey] = string(payload)
	return cloned
}

func consultationLedgerForRoot(rootID string) *consultationLedger {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return &consultationLedger{targets: make(map[string]*consultationTargetStats)}
	}
	if existing, ok := consultationDeliberationRegistry.Load(rootID); ok {
		if ledger, ok := existing.(*consultationLedger); ok && ledger != nil {
			return ledger
		}
	}
	ledger := &consultationLedger{
		rootCorrelation: rootID,
		targets:         make(map[string]*consultationTargetStats),
	}
	actual, _ := consultationDeliberationRegistry.LoadOrStore(rootID, ledger)
	if persisted, ok := actual.(*consultationLedger); ok && persisted != nil {
		return persisted
	}
	return ledger
}

func (l *consultationLedger) count() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.consultCount
}

func (l *consultationLedger) reward() float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.totalReward
}

func (l *consultationLedger) targetCount(target string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if stats := l.targets[strings.TrimSpace(target)]; stats != nil {
		return stats.Count
	}
	return 0
}

func (l *consultationLedger) targetReward(target string) float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if stats := l.targets[strings.TrimSpace(target)]; stats != nil {
		return stats.Reward
	}
	return 0
}

func (l *consultationLedger) distinctTargetCount() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.targets)
}

func (l *consultationLedger) maxSimilarity(target, fingerprint string, tokens map[string]struct{}) float64 {
	if l == nil || len(tokens) == 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	maxSimilarity := 0.0
	target = strings.TrimSpace(target)
	for _, observation := range l.recent {
		if observation.Target != target && target != "" {
			continue
		}
		otherTokens := normalizedConsultationTokens(observation.Fingerprint)
		similarity := consultationJaccard(tokens, otherTokens)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
		}
	}
	return maxSimilarity
}

func (l *consultationLedger) recordAttempt(observation consultationObservation) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consultCount++
	target := strings.TrimSpace(observation.Target)
	stats := l.targets[target]
	if stats == nil {
		stats = &consultationTargetStats{}
		l.targets[target] = stats
	}
	stats.Count++
	l.recent = append(l.recent, observation)
	if len(l.recent) > 24 {
		l.recent = append([]consultationObservation(nil), l.recent[len(l.recent)-24:]...)
	}
}

func (l *consultationLedger) observationNovelty(attemptID string) float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.recent) - 1; i >= 0; i-- {
		if l.recent[i].AttemptID == attemptID {
			return l.recent[i].Novelty
		}
	}
	return 0
}

func (l *consultationLedger) recordOutcome(attemptID string, reward float64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.recent) - 1; i >= 0; i-- {
		if l.recent[i].AttemptID != attemptID {
			continue
		}
		if l.recent[i].OutcomeKnown {
			return
		}
		l.recent[i].OutcomeKnown = true
		l.recent[i].Reward = reward
		l.totalReward += reward
		target := strings.TrimSpace(l.recent[i].Target)
		if stats := l.targets[target]; stats != nil {
			stats.Reward += reward
		}
		return
	}
}

func consultationOutcomeHasSignal(data any) bool {
	switch typed := data.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []string:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func consultationDepthCredit(depth ResearchDepth) float64 {
	switch EffectiveResearchDepth(string(depth)) {
	case ResearchDepthMinimal:
		return 0
	case ResearchDepthQuick:
		return 1
	case ResearchDepthStandard:
		return math.Sqrt(2)
	case ResearchDepthDeep:
		return math.Sqrt(3)
	case ResearchDepthComprehensive:
		return 2
	default:
		return math.Sqrt(2)
	}
}

func normalizeConsultationQuery(query string) (string, map[string]struct{}) {
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
	if len(fields) == 0 {
		return "", nil
	}
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		tokens[field] = struct{}{}
	}
	ordered := make([]string, 0, len(tokens))
	for token := range tokens {
		ordered = append(ordered, token)
	}
	sortStrings(ordered)
	return strings.Join(ordered, " "), tokens
}

func normalizedConsultationTokens(fingerprint string) map[string]struct{} {
	if strings.TrimSpace(fingerprint) == "" {
		return nil
	}
	tokens := make(map[string]struct{})
	for _, field := range strings.Fields(strings.TrimSpace(fingerprint)) {
		tokens[field] = struct{}{}
	}
	return tokens
}

func consultationJaccard(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	union := len(left)
	for token := range right {
		if _, ok := left[token]; ok {
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

func newDeliberationAttemptID() string {
	return "consult_" + timeNowUTC().Format("150405.000000000")
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

func positivePart(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func consultationStreamCorrelationID(ctx context.Context) string {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stream.CorrelationID)
}

func consultationStreamAgentID(ctx context.Context) string {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stream.SourceAgentID)
}

func consultationStreamMetadataStringFromContext(ctx context.Context, key string) string {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	return streamMetadataString(stream.Metadata, key)
}

func consultationLogCorrelationID(ctx context.Context) string {
	return strings.TrimSpace(LogMetaFromContext(ctx).CorrID)
}

func consultationLogAgentID(ctx context.Context) string {
	return strings.TrimSpace(LogMetaFromContext(ctx).AgentID)
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}

func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
