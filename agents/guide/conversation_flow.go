package guide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	defaultConversationSessionTTL        = 20 * time.Minute
	defaultConversationReleaseConfidence = 0.9
	defaultConversationACTRHalfLife      = 10 * time.Minute
	defaultConversationACTRFrequencyK    = 0.35
	defaultConversationACTRThreshold     = 0.45

	// maxConversationTurns bounds the per-session history ring.
	// Derived from ACT-R half-life (10min) / typical interaction rate (~1.25/min).
	maxConversationTurns = 8

	// maxConversationTurnContentLen bounds each UserInput/AgentReply field.
	// At ~4 chars/token, 8000 chars ≈ 2000 tokens; 8 turns × 2 fields × 2000 = 32000 tokens max.
	// 32K tokens is ~16% of a 200K context window — acceptable for rich plan discussions.
	maxConversationTurnContentLen = 8000
)

type ConversationFlowConfig struct {
	SessionTTL        time.Duration `json:"session_ttl"`
	ReleaseConfidence float64       `json:"release_confidence"`
	ACTRHalfLife      time.Duration `json:"actr_half_life"`
	ACTRFrequencyK    float64       `json:"actr_frequency_k"`
	ACTRThreshold     float64       `json:"actr_threshold"`
}

type ConversationFlowManager struct {
	mu       sync.RWMutex
	config   ConversationFlowConfig
	sessions map[string]conversationFlowState
}

type conversationFlowState struct {
	ActiveAgentID string
	UpdatedAt     time.Time
	agents        map[string]conversationAgentState
	phase         *sessionPhase // Active conversation phase (e.g. plan approval).
}

// sessionPhase tracks an active conversation phase set by a ResponseDirective.
// The phase is consumed (one-shot) on the next user message classification.
type sessionPhase struct {
	Phase     ConversationPhase
	AgentID   string
	Metadata  map[string]any
	SetAt     time.Time
	ExpiresAt time.Time
}

type conversationAgentState struct {
	UpdatedAt time.Time
	Turns     int

	history      []ConversationTurn // ring buffer, cap = maxConversationTurns
	historyHead  int                // next write position
	historyCount int                // valid entries (≤ maxConversationTurns)
}

// ConversationSessionSnapshot captures active session conversation state.
type ConversationSessionSnapshot struct {
	SessionID      string
	ActiveAgentID  string
	UpdatedAt      time.Time
	Age            time.Duration
	Turns          int
	ActivationHint float64
}

func newGuideConversationFlow(cfg *ConversationFlowConfig) *ConversationFlowManager {
	if cfg == nil {
		return NewConversationFlowManager(ConversationFlowConfig{})
	}
	return NewConversationFlowManager(*cfg)
}

func NewConversationFlowManager(cfg ConversationFlowConfig) *ConversationFlowManager {
	return &ConversationFlowManager{
		config:   normalizeConversationFlowConfig(cfg),
		sessions: map[string]conversationFlowState{},
	}
}

func normalizeConversationFlowConfig(cfg ConversationFlowConfig) ConversationFlowConfig {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultConversationSessionTTL
	}
	if cfg.ReleaseConfidence <= 0 || cfg.ReleaseConfidence > 1 {
		cfg.ReleaseConfidence = defaultConversationReleaseConfidence
	}
	if cfg.ACTRHalfLife <= 0 {
		cfg.ACTRHalfLife = defaultConversationACTRHalfLife
	}
	if cfg.ACTRFrequencyK <= 0 {
		cfg.ACTRFrequencyK = defaultConversationACTRFrequencyK
	}
	if cfg.ACTRThreshold <= 0 || cfg.ACTRThreshold >= 1 {
		cfg.ACTRThreshold = defaultConversationACTRThreshold
	}
	return cfg
}

func (m *ConversationFlowManager) ObserveUserInput(sessionID string, input string) {
	if m == nil {
		return
	}
	if !isUserConversationDoneSignal(input) {
		return
	}
	m.Clear(sessionID)
}

func (m *ConversationFlowManager) ObserveRoutedRequest(sessionID string, targetAgentID string) {
	if m == nil || !isConversationAgent(targetAgentID) {
		return
	}
	trimmedSession := strings.TrimSpace(sessionID)
	if trimmedSession == "" {
		return
	}
	normalizedTarget := strings.ToLower(strings.TrimSpace(targetAgentID))
	now := time.Now()
	m.mu.Lock()
	state, ok := m.sessions[trimmedSession]
	if !ok {
		state = conversationFlowState{
			agents: map[string]conversationAgentState{},
		}
	} else if state.agents == nil {
		state.agents = map[string]conversationAgentState{}
	}
	agentState := state.agents[normalizedTarget]
	agentState.Turns++
	agentState.UpdatedAt = now
	state.agents[normalizedTarget] = agentState
	state.ActiveAgentID = normalizedTarget
	state.UpdatedAt = now
	m.sessions[trimmedSession] = state
	m.mu.Unlock()
}

func (m *ConversationFlowManager) ObserveResponse(sessionID string, agentID string, content string) {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if !isConversationAgent(agentID) {
		return
	}
	if isAgentConversationDoneSignal(content) {
		m.Clear(sessionID)
		return
	}
	m.ObserveRoutedRequest(sessionID, agentID)
}

func (m *ConversationFlowManager) SuggestedTarget(sessionID string, targetAgentID string, result *RouteResult) (string, bool) {
	if m == nil {
		return "", false
	}
	state, ok := m.sessionState(sessionID)
	if !ok {
		return "", false
	}
	activeAgentID := strings.ToLower(strings.TrimSpace(state.ActiveAgentID))
	if !isConversationAgent(activeAgentID) {
		return "", false
	}
	if !shouldPreferConversationAgent(activeAgentID, targetAgentID, result, m.config.ReleaseConfidence) {
		return m.suggestTargetByACTR(state, targetAgentID, result)
	}
	return activeAgentID, true
}

func (m *ConversationFlowManager) suggestTargetByACTR(
	state conversationFlowState,
	targetAgentID string,
	result *RouteResult,
) (string, bool) {
	if !isFollowUpIntent(routeResultIntent(result)) {
		return "", false
	}
	activeState, ok := state.activeAgentState()
	if !ok {
		return "", false
	}
	score := actrConversationScore(activeState, targetAgentID, result, time.Now(), m.config)
	if score < m.config.ACTRThreshold {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(state.ActiveAgentID)), true
}

func (m *ConversationFlowManager) sessionState(sessionID string) (conversationFlowState, bool) {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return conversationFlowState{}, false
	}
	m.mu.RLock()
	state, ok := m.sessions[trimmed]
	m.mu.RUnlock()
	if !ok {
		return conversationFlowState{}, false
	}
	if time.Since(state.UpdatedAt) <= m.config.SessionTTL {
		return state, true
	}
	m.Clear(trimmed)
	return conversationFlowState{}, false
}

func (m *ConversationFlowManager) Clear(sessionID string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, trimmed)
	m.mu.Unlock()
}

// RecordUserInput pushes a new turn with the user's input into the session history.
func (m *ConversationFlowManager) RecordUserInput(sessionID, agentID, input string) {
	if m == nil {
		return
	}
	trimmedSession := strings.TrimSpace(sessionID)
	trimmedAgent := strings.ToLower(strings.TrimSpace(agentID))
	if trimmedSession == "" || trimmedAgent == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.sessions[trimmedSession]
	if !ok {
		return
	}
	if state.agents == nil {
		state.agents = map[string]conversationAgentState{}
	}
	agentState := state.agents[trimmedAgent]
	if agentState.history == nil {
		agentState.history = make([]ConversationTurn, maxConversationTurns)
	}
	now := time.Now()
	agentState.history[agentState.historyHead] = ConversationTurn{
		UserInput: truncateConversationContent(input),
		AgentID:   trimmedAgent,
		Timestamp: now,
	}
	agentState.historyHead = (agentState.historyHead + 1) % maxConversationTurns
	if agentState.historyCount < maxConversationTurns {
		agentState.historyCount++
	}
	agentState.UpdatedAt = now
	state.agents[trimmedAgent] = agentState
	state.ActiveAgentID = trimmedAgent
	state.UpdatedAt = now
	m.sessions[trimmedSession] = state
}

// RecordAgentReply attaches the agent's reply to the most recent turn.
// Drops the reply silently if the turn's agent doesn't match.
func (m *ConversationFlowManager) RecordAgentReply(sessionID, agentID, reply string) {
	if m == nil {
		return
	}
	trimmedSession := strings.TrimSpace(sessionID)
	trimmedAgent := strings.ToLower(strings.TrimSpace(agentID))
	if trimmedSession == "" || trimmedAgent == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.sessions[trimmedSession]
	if !ok || state.agents == nil {
		return
	}
	agentState, ok := state.agents[trimmedAgent]
	if !ok || agentState.historyCount == 0 {
		return
	}
	lastIdx := (agentState.historyHead - 1 + maxConversationTurns) % maxConversationTurns
	if !strings.EqualFold(agentState.history[lastIdx].AgentID, trimmedAgent) {
		return
	}
	agentState.history[lastIdx].AgentReply = truncateConversationContent(reply)
	agentState.UpdatedAt = time.Now()
	state.agents[trimmedAgent] = agentState
	if strings.EqualFold(state.ActiveAgentID, trimmedAgent) {
		state.UpdatedAt = agentState.UpdatedAt
	}
	m.sessions[trimmedSession] = state
}

// HistoryForSession returns a chronological copy of turns (oldest→newest).
func (m *ConversationFlowManager) HistoryForSession(sessionID string) []ConversationTurn {
	return m.HistoryForSessionAgent(sessionID, "")
}

// HistoryForSessionAgent returns a chronological copy of turns for the specified
// agent within a session. If agentID is empty or non-conversational, the active
// session agent is used.
func (m *ConversationFlowManager) HistoryForSessionAgent(sessionID, agentID string) []ConversationTurn {
	if m == nil {
		return nil
	}
	trimmedSession := strings.TrimSpace(sessionID)
	if trimmedSession == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.sessions[trimmedSession]
	if !ok || state.agents == nil {
		return nil
	}
	normalizedAgent := strings.ToLower(strings.TrimSpace(agentID))
	if !isConversationAgent(normalizedAgent) {
		normalizedAgent = strings.ToLower(strings.TrimSpace(state.ActiveAgentID))
	}
	agentState, ok := state.agents[normalizedAgent]
	if !ok || agentState.historyCount == 0 {
		return nil
	}
	result := make([]ConversationTurn, agentState.historyCount)
	start := (agentState.historyHead - agentState.historyCount + maxConversationTurns) % maxConversationTurns
	for i := range agentState.historyCount {
		result[i] = agentState.history[(start+i)%maxConversationTurns]
	}
	return result
}

// truncationMarker is appended when content is shortened so the LLM
// understands it is seeing a summary, not a broken/incomplete response.
const truncationMarker = "\n[…]"

func truncateConversationContent(s string) string {
	if len(s) <= maxConversationTurnContentLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxConversationTurnContentLen {
		return s
	}

	// Reserve room for the marker.
	markerRunes := []rune(truncationMarker)
	limit := maxConversationTurnContentLen - len(markerRunes)
	if limit < 1 {
		limit = 1
	}

	clipped := string(runes[:limit])

	// Prefer truncating at a paragraph boundary (last "\n\n").
	if idx := strings.LastIndex(clipped, "\n\n"); idx > limit/2 {
		clipped = clipped[:idx]
	} else if idx := strings.LastIndex(clipped, "\n"); idx > limit/2 {
		// Fall back to line boundary.
		clipped = clipped[:idx]
	}

	return clipped + truncationMarker
}

// Snapshot returns the active conversation state for a session when available.
func (m *ConversationFlowManager) Snapshot(sessionID string) (ConversationSessionSnapshot, bool) {
	if m == nil {
		return ConversationSessionSnapshot{}, false
	}
	state, ok := m.sessionState(sessionID)
	if !ok {
		return ConversationSessionSnapshot{}, false
	}
	activeState, ok := state.activeAgentState()
	if !ok {
		return ConversationSessionSnapshot{}, false
	}
	now := time.Now()
	return ConversationSessionSnapshot{
		SessionID:      strings.TrimSpace(sessionID),
		ActiveAgentID:  strings.ToLower(strings.TrimSpace(state.ActiveAgentID)),
		UpdatedAt:      activeState.UpdatedAt,
		Age:            now.Sub(activeState.UpdatedAt),
		Turns:          activeState.Turns,
		ActivationHint: actrConversationStateScore(activeState, now, m.config),
	}, true
}

func (s conversationFlowState) activeAgentState() (conversationAgentState, bool) {
	agentID := strings.ToLower(strings.TrimSpace(s.ActiveAgentID))
	if agentID == "" || s.agents == nil {
		return conversationAgentState{}, false
	}
	agentState, ok := s.agents[agentID]
	if !ok {
		return conversationAgentState{}, false
	}
	return agentState, true
}

func shouldPreferConversationAgent(activeAgentID string, classifiedTarget string, result *RouteResult, releaseConfidence float64) bool {
	if !isConversationAgent(activeAgentID) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(classifiedTarget), activeAgentID) {
		return false
	}
	if isUnknownOrEmptyTarget(classifiedTarget) {
		return true
	}
	if isGuideTargetID(classifiedTarget) && isFollowUpIntent(routeResultIntent(result)) {
		return true
	}
	return routeResultConfidence(result) < releaseConfidence
}

func actrConversationScore(
	state conversationAgentState,
	classifiedTarget string,
	result *RouteResult,
	now time.Time,
	cfg ConversationFlowConfig,
) float64 {
	base := actrConversationStateScore(state, now, cfg)
	boost := actrFollowUpBoost(classifiedTarget, result)
	penalty := routeResultConfidence(result) * 0.25
	return clampUnit(base + boost - penalty)
}

func actrConversationStateScore(state conversationAgentState, now time.Time, cfg ConversationFlowConfig) float64 {
	recency := actrRecency(now.Sub(state.UpdatedAt), cfg.ACTRHalfLife)
	frequency := actrFrequency(state.Turns, cfg.ACTRFrequencyK)
	return clampUnit(recency*0.5 + frequency*0.5)
}

func actrRecency(age time.Duration, halfLife time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Exp(-math.Ln2 * age.Seconds() / halfLife.Seconds())
}

func actrFrequency(turns int, k float64) float64 {
	if turns <= 0 {
		return 0
	}
	return 1 - math.Exp(-k*float64(turns))
}

func actrFollowUpBoost(classifiedTarget string, result *RouteResult) float64 {
	if isUnknownOrEmptyTarget(classifiedTarget) {
		return 0.20
	}
	if isGuideTargetID(classifiedTarget) && isFollowUpIntent(routeResultIntent(result)) {
		return 0.15
	}
	return 0
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func isUnknownOrEmptyTarget(target string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(target))
	return trimmed == "" || trimmed == "unknown"
}

func isGuideTargetID(target string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(target))
	return trimmed == "guide" || trimmed == "g"
}

func isConversationAgent(agentID string) bool {
	if isGuideTargetID(agentID) {
		return false
	}
	return !isUnknownOrEmptyTarget(agentID)
}

func routeResultIntent(result *RouteResult) Intent {
	if result == nil {
		return IntentUnknown
	}
	return result.Intent
}

func routeResultConfidence(result *RouteResult) float64 {
	if result == nil {
		return 0
	}
	return result.Confidence
}

func isFollowUpIntent(intent Intent) bool {
	switch intent {
	case IntentChat, IntentHelp, IntentStatus, IntentCheck, IntentUnknown:
		return true
	default:
		return false
	}
}

func isUserConversationDoneSignal(input string) bool {
	query := normalizeGuideQuery(input)
	return containsAny(
		query,
		"that's all",
		"thats all",
		"we're done",
		"we are done",
		"done for now",
		"no further changes",
		"no further action",
		"stop here",
		"end this",
		"all set",
	)
}

func isAgentConversationDoneSignal(input string) bool {
	query := normalizeGuideQuery(input)
	return containsAny(
		query,
		"completed successfully",
		"task complete",
		"no further action needed",
		"work is complete",
		"all tasks completed",
	)
}

// IsActiveForFastPath checks whether the session has an active conversation agent
// with sufficient ACT-R activation to skip LLM classification. Returns the
// active agent ID and true when the fast-path should fire.
func (m *ConversationFlowManager) IsActiveForFastPath(sessionID string) (string, bool) {
	if m == nil {
		return "", false
	}
	state, ok := m.sessionState(sessionID)
	if !ok {
		return "", false
	}
	activeAgentID := strings.ToLower(strings.TrimSpace(state.ActiveAgentID))
	if !isConversationAgent(activeAgentID) {
		return "", false
	}
	activeState, ok := state.activeAgentState()
	if !ok {
		return "", false
	}
	score := actrConversationStateScore(activeState, time.Now(), m.config)
	if score < m.config.ACTRThreshold {
		return "", false
	}
	return activeAgentID, true
}

func (g *Guide) observeUserConversationSignal(request *RouteRequest) {
	if g == nil || g.conversation == nil || request == nil {
		return
	}
	g.conversation.ObserveUserInput(request.SessionID, request.Input)
}

func (g *Guide) observeRoutedConversationTarget(request *RouteRequest, targetAgentID string) {
	if g == nil || g.conversation == nil || request == nil {
		return
	}
	g.conversation.ObserveRoutedRequest(request.SessionID, targetAgentID)
	g.conversation.RecordUserInput(request.SessionID, targetAgentID, request.Input)
	if g.sessionRouter != nil && request.SessionID != "" && isConversationAgent(targetAgentID) {
		g.sessionRouter.SetPreferredAgent(request.SessionID, targetAgentID, 8)
	}
}

func (g *Guide) observeConversationResponse(pending *PendingRequest, response *RouteResponse) {
	if g == nil || g.conversation == nil || pending == nil || pending.Request == nil {
		return
	}
	responseText := guideResponseText(response)
	g.conversation.ObserveResponse(
		pending.Request.SessionID,
		pending.TargetAgentID,
		responseText,
	)
	g.conversation.RecordAgentReply(
		pending.Request.SessionID,
		pending.TargetAgentID,
		responseText,
	)
}

func (g *Guide) applyConversationFlow(
	request *RouteRequest,
	classification *RouteResult,
	targetAgentID string,
) (*RouteResult, string) {
	if g == nil || g.conversation == nil || request == nil {
		return classification, targetAgentID
	}
	desiredTarget, ok := g.conversation.SuggestedTarget(request.SessionID, targetAgentID, classification)
	if !ok {
		g.observeRoutedConversationTarget(request, targetAgentID)
		return classification, targetAgentID
	}
	updated := remapClassificationTarget(classification, desiredTarget)
	updated.Intent = g.supportedIntentForTarget(desiredTarget, updated.Intent)
	updated.Domain = g.supportedDomainForTarget(desiredTarget, updated.Domain, updated.Intent)
	updated, targetAgentID = g.ensureRoutableClassification(updated, desiredTarget)
	g.observeRoutedConversationTarget(request, targetAgentID)
	return updated, targetAgentID
}

func remapClassificationTarget(classification *RouteResult, targetAgentID string) *RouteResult {
	if classification == nil {
		return &RouteResult{
			TargetAgent:          TargetAgent(targetAgentID),
			Intent:               IntentUnknown,
			Domain:               DomainGeneral,
			Confidence:           0.75,
			Action:               RouteActionExecute,
			ClassificationMethod: "session_flow",
			Reason:               "session follow-up routed to active agent",
		}
	}
	copyResult := *classification
	copyResult.TargetAgent = TargetAgent(targetAgentID)
	copyResult.Rejected = false
	copyResult.Action = RouteActionExecute
	copyResult.ClassificationMethod = appendClassificationMethod(classification.ClassificationMethod, "session_flow")
	copyResult.Reason = "session follow-up routed to active agent"
	if copyResult.Confidence < 0.75 {
		copyResult.Confidence = 0.75
	}
	return &copyResult
}

func appendClassificationMethod(base string, suffix string) string {
	trimmedBase := strings.TrimSpace(base)
	trimmedSuffix := strings.TrimSpace(suffix)
	if trimmedBase == "" {
		return trimmedSuffix
	}
	if trimmedSuffix == "" {
		return trimmedBase
	}
	return trimmedBase + "+" + trimmedSuffix
}

func (g *Guide) supportedIntentForTarget(targetAgentID string, current Intent) Intent {
	if g.agentSupportsIntent(targetAgentID, current) {
		return current
	}
	for _, candidate := range followupIntentCandidates() {
		if g.agentSupportsIntent(targetAgentID, candidate) {
			return candidate
		}
	}
	return current
}

func followupIntentCandidates() []Intent {
	return []Intent{
		IntentPlan,
		IntentDesign,
		IntentCheck,
		IntentHelp,
		IntentRecall,
		IntentSearch,
		IntentFind,
		IntentChat,
	}
}

// responseTexter is satisfied by agent response types (e.g. ConversationResult,
// DesignPlan) that carry human-readable text. Checked before falling back to
// JSON serialization so conversation history stores clean prose, not struct JSON.
type responseTexter interface {
	ResponseText() string
}

// =============================================================================
// Conversation Phase Tracking
// =============================================================================

// SetPhase records a conversation phase for the session from a ResponseDirective.
func (m *ConversationFlowManager) SetPhase(sessionID string, directive *ResponseDirective) {
	if m == nil || directive == nil || directive.Phase == PhaseNone {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	now := time.Now()
	ttl := directive.TTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	m.mu.Lock()
	state, ok := m.sessions[trimmed]
	if !ok {
		state = conversationFlowState{
			agents: map[string]conversationAgentState{},
		}
	}
	state.phase = &sessionPhase{
		Phase:     directive.Phase,
		AgentID:   strings.TrimSpace(directive.AgentID),
		Metadata:  directive.Metadata,
		SetAt:     now,
		ExpiresAt: now.Add(ttl),
	}
	state.UpdatedAt = now
	m.sessions[trimmed] = state
	m.mu.Unlock()
}

// CurrentPhase returns the active phase for the session, or nil if expired/absent.
func (m *ConversationFlowManager) CurrentPhase(sessionID string) *sessionPhase {
	if m == nil {
		return nil
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	m.mu.RLock()
	state, ok := m.sessions[trimmed]
	m.mu.RUnlock()
	if !ok || state.phase == nil {
		return nil
	}
	if time.Now().After(state.phase.ExpiresAt) {
		m.ClearPhase(trimmed)
		return nil
	}
	return state.phase
}

// ClearPhase removes the active phase for the session.
func (m *ConversationFlowManager) ClearPhase(sessionID string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	m.mu.Lock()
	if state, ok := m.sessions[trimmed]; ok {
		state.phase = nil
		m.sessions[trimmed] = state
	}
	m.mu.Unlock()
}

// =============================================================================
// Phase-Aware Classification
// =============================================================================

// tryPhaseClassification checks whether a conversation phase is active and
// classifies the input using phase-specific polarity instead of full intent
// detection. Returns (result, targetAgentID, true) when the phase gate fires.
func (g *Guide) tryPhaseClassification(request *RouteRequest) (*RouteResult, string, bool) {
	if g == nil || g.conversation == nil || request == nil {
		return nil, "", false
	}
	phase := g.conversation.CurrentPhase(request.SessionID)
	if phase == nil {
		return nil, "", false
	}

	// One-shot: consume the phase regardless of classification outcome.
	defer g.conversation.ClearPhase(request.SessionID)

	switch phase.Phase {
	case PhasePlanApproval:
		return g.classifyPlanApprovalPhase(request, phase)
	default:
		return nil, "", false
	}
}

// classifyPlanApprovalPhase handles the PhasePlanApproval phase. Checks for
// topic escape first, then classifies polarity (accept vs feedback).
func (g *Guide) classifyPlanApprovalPhase(request *RouteRequest, phase *sessionPhase) (*RouteResult, string, bool) {
	input := strings.TrimSpace(request.Input)
	if input == "" {
		return nil, "", false
	}

	// Topic escape: if the input references a different domain, fall through
	// to normal classification.
	if isPlanApprovalTopicEscape(input) {
		return nil, "", false
	}

	targetAgent := phase.AgentID
	if targetAgent == "" {
		targetAgent = "architect"
	}

	if classifyPlanApprovalPolarity(input) {
		result := &RouteResult{
			Intent:               IntentExecute,
			Domain:               DomainPlanning,
			TargetAgent:          TargetAgent(targetAgent),
			Confidence:           0.95,
			Action:               RouteActionExecute,
			ClassificationMethod: "phase_plan_approval",
			Reason:               "plan approval: positive polarity",
		}
		return result, targetAgent, true
	}

	result := &RouteResult{
		Intent:               IntentPlan,
		Domain:               DomainPlanning,
		TargetAgent:          TargetAgent(targetAgent),
		Confidence:           0.95,
		Action:               RouteActionExecute,
		ClassificationMethod: "phase_plan_feedback",
		Reason:               "plan approval: feedback/rejection",
	}
	return result, targetAgent, true
}

// planApprovalPositiveExact are short affirmatives that match only when the
// entire message (after trim + lower + strip punctuation) equals the phrase.
// Acceptance is a closed class — there are only so many ways to say "yes".
var planApprovalPositiveExact = map[string]struct{}{
	"yes": {}, "y": {}, "yep": {}, "yeah": {}, "yup": {},
	"ok": {}, "okay": {}, "sure": {}, "right": {},
	"great": {}, "perfect": {}, "awesome": {}, "absolutely": {},
	"affirmative": {}, "roger": {}, "aye": {}, "approved": {}, "lgtm": {},
}

// planApprovalPositiveContains are phrases that signal acceptance when found
// anywhere in the input.
var planApprovalPositiveContains = []string{
	"go ahead", "go for it", "do it", "make it so", "ship it",
	"let's go", "lets go", "let's do it", "lets do it",
	"sounds good", "looks good", "kick it off",
	"run it", "proceed", "execute", "hand off", "handoff",
}

// planApprovalNegationPrefixes are phrases that negate a subsequent positive
// signal. Checked before positive matching so "don't go ahead" and "no,
// don't proceed" are correctly classified as negative (feedback/rejection).
var planApprovalNegationPrefixes = []string{
	"don't", "don't", "do not", "dont",
	"not yet", "hold off", "hold on",
	"wait", "stop", "cancel", "pause",
	"i disagree", "i object", "i refuse",
	"no,", "no.", "no!", "nope",
}

// classifyPlanApprovalPolarity returns true for positive (accept), false for
// negative (feedback/reject). The positive list is intentionally small because
// acceptance is a closed class. Everything else is feedback — the correct
// default because unsolicited approval is rare.
//
// Negation is checked first: "don't go ahead" contains "go ahead" but is
// negative because the negation prefix overrides the positive signal.
func classifyPlanApprovalPolarity(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	if hasPlanApprovalNegation(lower) {
		return false
	}
	stripped := strings.TrimRight(lower, ".!?,;:")
	if _, ok := planApprovalPositiveExact[stripped]; ok {
		return true
	}
	for _, phrase := range planApprovalPositiveContains {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// hasPlanApprovalNegation returns true when the input contains a negation
// prefix that overrides any subsequent positive signal.
func hasPlanApprovalNegation(lower string) bool {
	for _, prefix := range planApprovalNegationPrefixes {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}

// isPlanApprovalTopicEscape returns true when the input clearly references a
// different domain, indicating the user has moved on from plan approval.
func isPlanApprovalTopicEscape(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	// File paths or code references.
	if strings.ContainsAny(lower, "/\\") {
		return true
	}
	// Explicit agent targeting.
	escapeKeywords := []string{
		"@librarian", "@tester", "@inspector", "@engineer", "@designer",
		"find the", "search for", "locate the", "show me the file",
	}
	for _, kw := range escapeKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// observeStreamDirective checks an incoming stream event for a ResponseDirective
// and records the conversation phase for the session.
func (g *Guide) observeStreamDirective(streamResp *StreamResponse) {
	if g == nil || g.conversation == nil || streamResp == nil || streamResp.Event == nil {
		return
	}
	event := streamResp.Event
	if event.Type != StreamEventComplete || event.Directive == nil {
		return
	}
	pending := g.pending.Get(streamResp.CorrelationID)
	if pending == nil || pending.Request == nil {
		return
	}
	g.conversation.SetPhase(pending.Request.SessionID, event.Directive)
}

// observeStreamReroute detects reroute events and records the stream target
// override so that stream events for the new CID reach the correct consumer.
//
// When the architect dispatches a plan to the orchestrator, it emits a reroute
// carrying both the original CID and the orchestrator's new CID. The pending
// for the new CID has SourceAgentID = "architect" (set by requestRouteSync),
// but stream events must reach the TUI. This method stores the mapping
// newCID → original source so that resolveStreamTarget can redirect streams
// while the MessageTypeResponse still reaches the architect for the sync wait.
func (g *Guide) observeStreamReroute(streamResp *StreamResponse) {
	if g == nil || streamResp == nil || streamResp.Event == nil {
		return
	}
	if streamResp.Event.Type != StreamEventReroute {
		return
	}
	data, ok := streamResp.Event.Data.(map[string]string)
	if !ok {
		return
	}
	newCID := strings.TrimSpace(data["new_correlation_id"])
	if newCID == "" {
		return
	}
	// The reroute arrives on the original CID's pending. The original
	// source (e.g. "tui") is who should receive the new CID's streams.
	pending := g.pending.Get(streamResp.CorrelationID)
	if pending == nil {
		return
	}
	originalSource := strings.TrimSpace(pending.SourceAgentID)
	if originalSource == "" {
		return
	}
	slog.Info("guide: observeStreamReroute: storing stream target override",
		"new_cid", newCID,
		"original_source", originalSource,
		"reroute_cid", streamResp.CorrelationID)

	// Store the mapping. The pending for newCID may not exist yet (the
	// route request hasn't arrived), so we store in the deferred map.
	g.streamReroutesMu.Lock()
	g.streamReroutes[newCID] = originalSource
	g.streamReroutesMu.Unlock()

	// If the pending already exists, apply immediately.
	if newPending := g.pending.Get(newCID); newPending != nil {
		newPending.StreamTargetOverride = originalSource
		slog.Info("guide: observeStreamReroute: applied immediately to existing pending",
			"new_cid", newCID)
	}
}

// applyDeferredStreamReroute checks the deferred reroute map and applies
// the stream target override to the pending if one was registered before
// the pending was created. Consumes the entry (one-shot application).
func (g *Guide) applyDeferredStreamReroute(pending *PendingRequest) {
	if pending == nil || pending.StreamTargetOverride != "" {
		return
	}
	g.streamReroutesMu.Lock()
	target, ok := g.streamReroutes[pending.CorrelationID]
	if ok {
		delete(g.streamReroutes, pending.CorrelationID)
	}
	g.streamReroutesMu.Unlock()
	if ok && target != "" {
		pending.StreamTargetOverride = target
		slog.Info("guide: applyDeferredStreamReroute: applied deferred override",
			"cid", pending.CorrelationID,
			"stream_target", target)
	}
}

// resolveStreamTarget returns the agent ID to which stream events should
// be published. Uses StreamTargetOverride when set (rerouted streams),
// otherwise falls back to the original SourceAgentID.
func (g *Guide) resolveStreamTarget(pending *PendingRequest) string {
	if pending.StreamTargetOverride != "" {
		return pending.StreamTargetOverride
	}
	return pending.SourceAgentID
}

func guideResponseText(response *RouteResponse) string {
	if response == nil || response.Data == nil {
		return ""
	}
	if text, ok := response.Data.(string); ok {
		return strings.TrimSpace(text)
	}
	if rt, ok := response.Data.(responseTexter); ok {
		if text := strings.TrimSpace(rt.ResponseText()); text != "" {
			return text
		}
	}
	if values, ok := response.Data.(map[string]any); ok {
		if answer, ok := values["answer"].(string); ok {
			return strings.TrimSpace(answer)
		}
	}
	encoded, err := json.Marshal(response.Data)
	if err != nil {
		return strings.TrimSpace(fmt.Sprint(response.Data))
	}
	return strings.TrimSpace(string(encoded))
}
