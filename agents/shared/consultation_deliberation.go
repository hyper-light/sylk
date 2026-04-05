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
	similarity := ledger.maxSimilarity(target, fingerprint, tokens)
	novelty := clamp01(1 - similarity)
	targetCount := ledger.targetCount(target) + 1
	consultCount := ledger.count() + 1
	nextDepth := snapshot.CurrentDepth + 1

	priorCredit := consultationDepthCredit(researchDepth)
	globalReward := ledger.reward()
	targetReward := ledger.targetReward(target)
	distinctTargets := ledger.distinctTargetCount()

	depthBudget := 1 + priorCredit + globalReward
	volumeBudget := 1 + priorCredit + float64(distinctTargets) + globalReward
	repeatBudget := 1 + (priorCredit / 2) + targetReward

	depthExcess := positivePart(float64(nextDepth) - depthBudget)
	volumeExcess := positivePart(float64(consultCount) - volumeBudget)
	repeatExcess := positivePart(float64(targetCount) - repeatBudget)
	questionPenalty := similarity * float64(targetCount)
	penalty := (depthExcess * depthExcess) + (volumeExcess * volumeExcess) + (repeatExcess * repeatExcess) + questionPenalty
	expectedGain := novelty * (1 + priorCredit + globalReward + targetReward)
	allowed := penalty <= expectedGain

	attemptID := newDeliberationAttemptID()
	ledger.recordAttempt(consultationObservation{
		AttemptID:   attemptID,
		Target:      target,
		Fingerprint: fingerprint,
		Novelty:     novelty,
		Allowed:     allowed,
	})

	admission := ConsultationAdmission{
		AttemptID:       attemptID,
		Allowed:         allowed,
		RootCorrelation: snapshot.RootCorrelationID,
		ResearchDepth:   researchDepth,
		Depth:           nextDepth,
		ConsultCount:    consultCount,
		TargetCount:     targetCount,
		Similarity:      similarity,
		Novelty:         novelty,
		Penalty:         penalty,
		ExpectedGain:    expectedGain,
	}
	if !allowed {
		admission.Guidance = fmt.Sprintf(
			"Consultation pressure is too high for this branch. Expected gain %.2f is below penalty %.2f at depth %d with %d total consults. Synthesize current evidence, materially change the question, or switch to a more informative target before consulting again.",
			expectedGain,
			penalty,
			nextDepth,
			consultCount,
		)
		return admission
	}

	childSnapshot := snapshot
	childSnapshot.CurrentDepth = nextDepth
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
