package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	defaultConversationSessionTTL        = 30 * time.Minute
	defaultConversationReleaseConfidence = 0.9
	defaultConversationACTRHalfLife      = 15 * time.Minute
	defaultConversationACTRFrequencyK    = 0.35
	defaultConversationACTRThreshold     = 0.45

	// maxConversationTurns bounds the per-session history ring.
	// Derived from ACT-R half-life (15min) / typical interaction rate (~1.25/min).
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
	pending       *pendingPlan // Architect plan awaiting user approval.
	work          sessionWorkState
}

// pendingPlan tracks an architect plan awaiting user approval.
// Consumed on execute/feedback; TTL-bounded to prevent stale plans.
type pendingPlan struct {
	PlanID    string
	AgentID   string
	Epoch     uint64
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

type sessionWorkState struct {
	Kind         string
	PrimaryAgent string
	PlanID       string
	DAGID        string
	TaskID       string
	ArtifactRefs []string
	UpdatedAt    time.Time
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
	// Only refresh UpdatedAt — do NOT increment Turns. ObserveRoutedRequest
	// is called on the routing path (when a *new* user message arrives).
	// Calling it here on every response would inflate the ACT-R score,
	// causing the conversation fast-path to fire indefinitely and preventing
	// the Guide from ever reclassifying subsequent user messages.
	m.refreshAgentTimestamp(sessionID, agentID)
}

// refreshAgentTimestamp updates only the UpdatedAt timestamp for an agent
// without incrementing Turns. Used by ObserveResponse to keep the session
// alive without inflating ACT-R frequency scores.
func (m *ConversationFlowManager) refreshAgentTimestamp(sessionID string, agentID string) {
	trimmedSession := strings.TrimSpace(sessionID)
	normalizedAgent := strings.ToLower(strings.TrimSpace(agentID))
	if trimmedSession == "" || normalizedAgent == "" {
		return
	}
	now := time.Now()
	m.mu.Lock()
	state, ok := m.sessions[trimmedSession]
	if !ok {
		m.mu.Unlock()
		return
	}
	agentState, ok := state.agents[normalizedAgent]
	if !ok {
		m.mu.Unlock()
		return
	}
	agentState.UpdatedAt = now
	state.agents[normalizedAgent] = agentState
	state.UpdatedAt = now
	m.sessions[trimmedSession] = state
	m.mu.Unlock()
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

func (m *ConversationFlowManager) ObserveWorkRoute(
	sessionID string,
	targetAgentID string,
	result *RouteResult,
	metadata map[string]any,
) {
	if m == nil {
		return
	}
	trimmedSession := strings.TrimSpace(sessionID)
	normalizedTarget := strings.ToLower(strings.TrimSpace(targetAgentID))
	if trimmedSession == "" || normalizedTarget == "" || isGuideTargetID(normalizedTarget) {
		return
	}

	now := time.Now()
	m.mu.Lock()
	state, ok := m.sessions[trimmedSession]
	if !ok {
		state = conversationFlowState{agents: map[string]conversationAgentState{}}
	} else if state.agents == nil {
		state.agents = map[string]conversationAgentState{}
	}
	work := state.work
	work.Kind = inferWorkKind(normalizedTarget, result, metadata)
	work.PrimaryAgent = normalizedTarget
	work.PlanID = firstNonEmptyString(metadataString(metadata, "plan_id"), work.PlanID)
	work.DAGID = firstNonEmptyString(metadataString(metadata, "dag_id"), work.DAGID)
	work.TaskID = firstNonEmptyString(metadataString(metadata, "task_id"), work.TaskID)
	work.ArtifactRefs = mergeArtifactRefs(work.ArtifactRefs,
		metadataString(metadata, "paper_path"),
		metadataString(metadata, "research_slug"),
		metadataString(metadata, "workflow_id"),
		metadataString(metadata, "dag_id"),
		metadataString(metadata, "plan_id"),
	)
	work.UpdatedAt = now
	state.work = work
	state.UpdatedAt = now
	m.sessions[trimmedSession] = state
	m.mu.Unlock()
}

func (m *ConversationFlowManager) WorkPreferredTarget(sessionID string) (string, bool) {
	state, ok := m.sessionState(sessionID)
	if !ok {
		return "", false
	}
	target := strings.ToLower(strings.TrimSpace(state.work.PrimaryAgent))
	if !isConversationAgent(target) {
		return "", false
	}
	return target, true
}

func (m *ConversationFlowManager) WorkMetadata(sessionID string) map[string]any {
	state, ok := m.sessionState(sessionID)
	if !ok {
		return nil
	}
	work := state.work
	if strings.TrimSpace(work.Kind) == "" && strings.TrimSpace(work.PrimaryAgent) == "" {
		return nil
	}
	meta := map[string]any{
		"work_kind":      work.Kind,
		"work_agent":     work.PrimaryAgent,
		"work_artifacts": append([]string(nil), work.ArtifactRefs...),
	}
	if work.PlanID != "" {
		meta["work_plan_id"] = work.PlanID
	}
	if work.DAGID != "" {
		meta["work_dag_id"] = work.DAGID
	}
	if work.TaskID != "" {
		meta["work_task_id"] = work.TaskID
	}
	return meta
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
	// Session expired — preserve the pendingPlan if it has its own live TTL.
	m.expireSessionPreservePlan(trimmed)
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

// ClearAllStickiness clears ActiveAgentID across every session so the next
// request goes through full LLM classification with the new provider.
// Conversation history (turns, ring buffer), pending plans, and ACT-R decay
// state are preserved so chat continuity is maintained after a model swap.
func (m *ConversationFlowManager) ClearAllStickiness() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, state := range m.sessions {
		state.ActiveAgentID = ""
		m.sessions[sid] = state
	}
}

// expireSessionPreservePlan resets the session's conversation state while
// keeping the pendingPlan when it still has a valid TTL. This prevents the
// session TTL from destroying plans that have their own longer TTL.
func (m *ConversationFlowManager) expireSessionPreservePlan(sessionID string) {
	m.mu.Lock()
	state, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if state.pending != nil && time.Now().Before(state.pending.ExpiresAt) {
		state.ActiveAgentID = ""
		state.agents = map[string]conversationAgentState{}
		state.UpdatedAt = time.Time{}
		m.sessions[sessionID] = state
		m.mu.Unlock()
		return
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

// ClearStickiness clears routing stickiness (ActiveAgentID) for the session
// but preserves the agents map containing conversation history. Use this on
// interrupt instead of Clear to avoid destroying the session's memory.
func (m *ConversationFlowManager) ClearStickiness(sessionID string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.sessions[trimmed]
	if !ok {
		return
	}
	state.ActiveAgentID = ""
	m.sessions[trimmed] = state
}

// RecordInterruption records an interruption marker as the agent's reply so
// the LLM knows the prior turn was cut short. Delegates to RecordAgentReply.
func (m *ConversationFlowManager) RecordInterruption(sessionID, agentID string) {
	m.RecordAgentReply(sessionID, agentID, "(interrupted by user)")
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
		"(interrupted)",
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

func (g *Guide) observeRoutedConversationTarget(request *RouteRequest, classification *RouteResult, targetAgentID string) {
	if g == nil || g.conversation == nil || request == nil {
		return
	}
	g.conversation.ObserveRoutedRequest(request.SessionID, targetAgentID)
	g.conversation.RecordUserInput(request.SessionID, targetAgentID, request.Input)
	g.conversation.ObserveWorkRoute(request.SessionID, targetAgentID, classification,
		mergeForwardMetadata(workMetadataFromClassification(classification), request.Metadata))
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
	g.observeResponseDirective(pending, response)
}

// observeResponseDirective extracts a ResponseDirective from the RouteResponse
// data as a fallback when the STREAM_COMPLETE event carrying the directive was
// lost. This is a safety net — the primary path is observeStreamDirective.
func (g *Guide) observeResponseDirective(pending *PendingRequest, response *RouteResponse) {
	if g == nil || g.conversation == nil || response == nil || response.Data == nil {
		return
	}
	if pending == nil || pending.Request == nil {
		return
	}
	// Skip if a pending plan is already set for this session (stream path succeeded).
	if g.conversation.PendingPlan(pending.Request.SessionID) != nil {
		return
	}
	carrier, ok := response.Data.(directiveCarrier)
	if !ok {
		return
	}
	directive := carrier.ResponseDirective()
	if directive == nil {
		return
	}
	DebugFileLog().Info("observeResponseDirective: FALLBACK_SET_PENDING_PLAN",
		"session_id", pending.Request.SessionID,
		"phase", string(directive.Phase),
		"agent_id", directive.AgentID,
		"correlation_id", response.CorrelationID)
	g.conversation.SetPendingPlan(pending.Request.SessionID, directive)
}

func (g *Guide) applyConversationFlow(
	ctx context.Context,
	request *RouteRequest,
	classification *RouteResult,
	targetAgentID string,
) (*RouteResult, string) {
	if g == nil || g.conversation == nil || request == nil {
		return classification, targetAgentID
	}
	if g.router != nil && g.router.IsDSL(request.Input) {
		g.observeRoutedConversationTarget(request, classification, targetAgentID)
		return classification, targetAgentID
	}
	if updated, updatedTarget, ok := g.applyWorkPreference(ctx, request, classification, targetAgentID); ok {
		g.observeRoutedConversationTarget(request, updated, updatedTarget)
		return updated, updatedTarget
	}
	desiredTarget, ok := g.conversation.SuggestedTarget(request.SessionID, targetAgentID, classification)
	if !ok {
		g.observeRoutedConversationTarget(request, classification, targetAgentID)
		return classification, targetAgentID
	}
	updated := remapClassificationTarget(classification, desiredTarget)
	updated, targetAgentID = g.ensureRoutableClassification(ctx, request.Input, updated, desiredTarget)
	g.observeRoutedConversationTarget(request, updated, targetAgentID)
	return updated, targetAgentID
}

func (g *Guide) applyWorkPreference(
	ctx context.Context,
	request *RouteRequest,
	classification *RouteResult,
	targetAgentID string,
) (*RouteResult, string, bool) {
	if g == nil || g.conversation == nil || request == nil || request.ExplicitTarget {
		return nil, "", false
	}
	if classification != nil && !g.shouldUseWorkPreference(classification, targetAgentID) {
		return nil, "", false
	}
	preferred, ok := g.conversation.WorkPreferredTarget(request.SessionID)
	if !ok || strings.EqualFold(preferred, targetAgentID) {
		return nil, "", false
	}
	updated := remapClassificationTarget(classification, preferred)
	updated.ClassificationMethod = appendClassificationMethod(updated.ClassificationMethod, "work_state")
	updated.Reason = "session work context routed to active specialist"
	updated, targetAgentID = g.ensureRoutableClassification(ctx, request.Input, updated, preferred)
	return updated, targetAgentID, true
}

func (g *Guide) shouldUseWorkPreference(classification *RouteResult, targetAgentID string) bool {
	if classification == nil {
		return true
	}
	if isGuideTargetID(targetAgentID) || isUnknownOrEmptyTarget(targetAgentID) {
		return true
	}
	return classification.Confidence < 0.55
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

func inferWorkKind(targetAgentID string, result *RouteResult, metadata map[string]any) string {
	target := strings.ToLower(strings.TrimSpace(targetAgentID))
	switch {
	case target == "academic":
		return "research"
	case target == "architect":
		return "planning"
	case target == "librarian":
		return "grounding"
	case target == "archivalist":
		return "memory"
	case target == "engineer" || target == "designer":
		return "implementation"
	case strings.HasPrefix(target, "inspector"):
		return "validation"
	case target == "guardian":
		return "safety"
	case target == "orchestrator":
		return "workflow"
	}
	if metadataString(metadata, "dag_id") != "" || metadataString(metadata, "task_id") != "" {
		return "implementation"
	}
	if metadataString(metadata, "plan_id") != "" {
		return "planning"
	}
	if result != nil {
		switch result.Intent {
		case IntentPlan, IntentDesign:
			return "planning"
		case IntentExecute, IntentComplete:
			return "implementation"
		case IntentFind, IntentSearch, IntentLocate:
			return "grounding"
		case IntentRecall, IntentCheck:
			return "memory"
		}
	}
	return "general"
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[key]
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && value != "<nil>" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mergeArtifactRefs(existing []string, refs ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(refs))
	out := make([]string, 0, len(existing)+len(refs))
	for _, ref := range existing {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || ref == "<nil>" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func workMetadataFromClassification(classification *RouteResult) map[string]any {
	if classification == nil {
		return nil
	}
	return classification.PhaseMetadata
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

// directiveCarrier is satisfied by agent response types that carry a
// ResponseDirective (e.g. DesignPlan when ready, ConversationResult with
// plan approval). Used as a fallback when the STREAM_COMPLETE event carrying
// the directive is lost due to bus delivery issues.
type directiveCarrier interface {
	ResponseDirective() *ResponseDirective
}

// =============================================================================
// Pending Plan Tracking
// =============================================================================

const defaultPendingPlanTTL = 30 * time.Minute

// SetPendingPlan records a pending plan from a ResponseDirective.
// Only stores when directive.Phase == PhasePlanApproval.
func (m *ConversationFlowManager) SetPendingPlan(sessionID string, directive *ResponseDirective) {
	if m == nil || directive == nil || directive.Phase != PhasePlanApproval {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	pp := pendingPlanFromDirective(directive)
	if pp == nil {
		return
	}
	m.mu.Lock()
	state, ok := m.sessions[trimmed]
	if !ok {
		state = conversationFlowState{
			agents: map[string]conversationAgentState{},
		}
	}
	state.pending = pp
	state.UpdatedAt = pp.SetAt
	m.sessions[trimmed] = state
	m.mu.Unlock()
	DebugFileLog().Info("SetPendingPlan: STORED",
		"session_id", trimmed,
		"plan_id", pp.PlanID,
		"agent_id", pp.AgentID,
		"epoch", pp.Epoch,
		"expires_at", pp.ExpiresAt.String())
}

// PendingPlan returns the active pending plan for the session, or nil if
// expired/absent. Clears expired entries on read.
func (m *ConversationFlowManager) PendingPlan(sessionID string) *pendingPlan {
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
	if !ok || state.pending == nil {
		return nil
	}
	if time.Now().After(state.pending.ExpiresAt) {
		m.ClearPendingPlan(trimmed)
		return nil
	}
	return state.pending
}

// ClearPendingPlan removes the pending plan for the session.
func (m *ConversationFlowManager) ClearPendingPlan(sessionID string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	m.mu.Lock()
	if state, ok := m.sessions[trimmed]; ok {
		state.pending = nil
		m.sessions[trimmed] = state
	}
	m.mu.Unlock()
}

// pendingPlanFromDirective extracts a pendingPlan from a ResponseDirective.
// Handles both uint64 and float64 epoch types (JSON round-trip yields float64).
func pendingPlanFromDirective(directive *ResponseDirective) *pendingPlan {
	if directive == nil {
		return nil
	}
	agentID := strings.TrimSpace(directive.AgentID)
	if agentID == "" {
		agentID = "architect"
	}
	planID, _ := directive.Metadata["plan_id"].(string)
	var epoch uint64
	switch v := directive.Metadata["epoch"].(type) {
	case uint64:
		epoch = v
	case float64:
		epoch = uint64(v)
	}
	now := time.Now()
	ttl := directive.TTL
	if ttl <= 0 {
		ttl = defaultPendingPlanTTL
	}
	return &pendingPlan{
		PlanID:    planID,
		AgentID:   agentID,
		Epoch:     epoch,
		SetAt:     now,
		ExpiresAt: now.Add(ttl),
	}
}

// observeStreamDirective checks an incoming stream event for a ResponseDirective
// and records the conversation phase for the session.
func (g *Guide) observeStreamDirective(streamResp *StreamResponse) {
	if g == nil || g.conversation == nil || streamResp == nil || streamResp.Event == nil {
		return
	}
	event := streamResp.Event
	if event.Type != StreamEventComplete {
		return
	}
	DebugFileLog().Info("observeStreamDirective: STREAM_COMPLETE",
		"correlation_id", streamResp.CorrelationID,
		"has_directive", event.Directive != nil)
	if event.Directive == nil {
		return
	}
	pending := g.pending.Get(streamResp.CorrelationID)
	if pending == nil || pending.Request == nil {
		DebugFileLog().Warn("observeStreamDirective: PENDING_MISSING",
			"correlation_id", streamResp.CorrelationID,
			"pending_nil", pending == nil)
		return
	}
	sessionID := pending.Request.SessionID
	DebugFileLog().Info("observeStreamDirective: SET_PENDING_PLAN",
		"correlation_id", streamResp.CorrelationID,
		"session_id", sessionID,
		"phase", string(event.Directive.Phase),
		"agent_id", event.Directive.AgentID)
	g.conversation.SetPendingPlan(sessionID, event.Directive)
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
	streamReroutes := g.streamReroutes
	if streamReroutes == nil {
		streamReroutes = make(map[string]string)
		g.streamReroutes = streamReroutes
	}
	streamReroutes[newCID] = originalSource
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
	streamReroutes := g.streamReroutes
	if streamReroutes == nil {
		g.streamReroutesMu.Unlock()
		return
	}
	target, ok := streamReroutes[pending.CorrelationID]
	if ok {
		delete(streamReroutes, pending.CorrelationID)
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
