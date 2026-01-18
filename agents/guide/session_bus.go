package guide

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// =============================================================================
// Session Bus
// =============================================================================
//
// SessionBus wraps an EventBus with session context, providing:
// - Automatic topic prefixing with session ID
// - Session-scoped subscriptions (auto-cleanup on session close)
// - Wildcard topic support via TopicRouter
// - Message filtering by session, type, and priority
//
// Topic format: session.{session_id}.{agent}.{channel}
// Global topics (no session prefix) are still accessible.

var (
	ErrSessionClosed       = errors.New("session bus is closed")
	ErrInvalidSessionID    = errors.New("session ID is required")
	ErrSubscriptionInvalid = errors.New("subscription is invalid")
)

// SessionBus wraps an EventBus with session context
type SessionBus struct {
	mu sync.RWMutex

	// Underlying event bus
	bus EventBus

	// Session identifier
	sessionID string

	// Topic router for wildcard matching
	router *TopicRouter

	// Session-scoped subscriptions (for cleanup)
	subscriptions []*sessionSubscription

	// Subscription ID counter
	nextSubID int64

	// State
	closed atomic.Bool

	wildcardSem chan struct{}
	scope       *concurrency.GoroutineScope
	wildcardTTL time.Duration

	// Statistics
	stats sessionBusStats
}

// sessionBusStats holds atomic counters
type sessionBusStats struct {
	messagesPublished   int64
	messagesReceived    int64
	subscriptionsActive int64
	wildcardMatches     int64
}

// SessionBusStats contains session bus statistics
type SessionBusStats struct {
	SessionID           string `json:"session_id"`
	MessagesPublished   int64  `json:"messages_published"`
	MessagesReceived    int64  `json:"messages_received"`
	SubscriptionsActive int64  `json:"subscriptions_active"`
	WildcardMatches     int64  `json:"wildcard_matches"`
}

// SessionBusConfig configures a session bus
type SessionBusConfig struct {
	// SessionID is required - identifies this session
	SessionID string

	// EnableWildcards enables wildcard topic matching
	EnableWildcards bool
	Scope           *concurrency.GoroutineScope
	WildcardTimeout time.Duration
	NotifyTimeout   time.Duration
}

// sessionSubscription tracks a session-scoped subscription
type sessionSubscription struct {
	id           int64
	topic        string
	pattern      string // Original pattern (may be wildcard)
	isWildcard   bool
	subscription Subscription       // From underlying bus
	topicSub     *topicSubscription // From router (for wildcards)
	active       atomic.Bool
}

// NewSessionBus creates a new session-scoped bus
func NewSessionBus(bus EventBus, cfg SessionBusConfig) (*SessionBus, error) {
	if cfg.SessionID == "" {
		return nil, ErrInvalidSessionID
	}
	if bus == nil {
		return nil, errors.New("event bus is required")
	}

	sb := &SessionBus{
		bus:           bus,
		sessionID:     cfg.SessionID,
		subscriptions: make([]*sessionSubscription, 0),
		wildcardSem:   make(chan struct{}, 32),
		scope:         cfg.Scope,
		wildcardTTL:   cfg.WildcardTimeout,
	}

	if cfg.EnableWildcards {
		sb.router = NewTopicRouter()
	}

	return sb, nil
}

// =============================================================================
// EventBus Implementation
// =============================================================================

// Publish sends a message to a topic (auto-prefixed with session if not global)
func (sb *SessionBus) Publish(topic string, msg *Message) error {
	if sb.closed.Load() {
		return ErrSessionClosed
	}

	fullTopic := sb.resolveTopic(topic)
	sb.addSessionMetadata(msg)

	if err := sb.bus.Publish(fullTopic, msg); err != nil {
		return err
	}

	sb.recordPublish(fullTopic, msg)
	return nil
}

func (sb *SessionBus) recordPublish(topic string, msg *Message) {
	atomic.AddInt64(&sb.stats.messagesPublished, 1)
	sb.routeToWildcardsIfEnabled(topic, msg)
}

func (sb *SessionBus) addSessionMetadata(msg *Message) {
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]any)
	}
	msg.Metadata["session_id"] = sb.sessionID
}

func (sb *SessionBus) routeToWildcardsIfEnabled(topic string, msg *Message) {
	if sb.router != nil {
		sb.routeToWildcardSubscribers(topic, msg)
	}
}

// PublishGlobal publishes to a global topic (not session-prefixed)
func (sb *SessionBus) PublishGlobal(topic string, msg *Message) error {
	if sb.closed.Load() {
		return ErrSessionClosed
	}

	// Add session ID to message metadata but don't prefix topic
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]any)
	}
	msg.Metadata["session_id"] = sb.sessionID

	atomic.AddInt64(&sb.stats.messagesPublished, 1)
	return sb.bus.Publish(topic, msg)
}

// Subscribe registers a handler for a topic (auto-prefixed with session)
func (sb *SessionBus) Subscribe(topic string, handler MessageHandler) (Subscription, error) {
	return sb.subscribe(topic, handler, false)
}

// SubscribeAsync registers an async handler (auto-prefixed with session)
func (sb *SessionBus) SubscribeAsync(topic string, handler MessageHandler) (Subscription, error) {
	return sb.subscribe(topic, handler, true)
}

// SubscribeGlobal subscribes to a global topic (not session-prefixed)
func (sb *SessionBus) SubscribeGlobal(topic string, handler MessageHandler) (Subscription, error) {
	if sb.closed.Load() {
		return nil, ErrSessionClosed
	}
	if handler == nil {
		return nil, ErrInvalidHandler
	}

	// Wrap handler with session filter
	filteredHandler := sb.wrapHandler(handler)

	sub, err := sb.bus.Subscribe(topic, filteredHandler)
	if err != nil {
		return nil, err
	}

	sessSub := sb.trackSubscription(topic, topic, false, sub, nil)
	return sessSub, nil
}

// SubscribePattern subscribes to a wildcard pattern
func (sb *SessionBus) SubscribePattern(pattern string, handler MessageHandler) (Subscription, error) {
	if sb.closed.Load() {
		return nil, ErrSessionClosed
	}
	if handler == nil {
		return nil, ErrInvalidHandler
	}
	if sb.router == nil {
		return nil, errors.New("wildcards not enabled for this session bus")
	}

	// Wrap handler with session filter
	filteredHandler := sb.wrapHandler(handler)

	// Register with topic router
	topicSub := sb.router.Subscribe(pattern, filteredHandler)

	sessSub := sb.trackSubscription("", pattern, true, nil, topicSub)
	return sessSub, nil
}

// SubscribeFiltered subscribes with a message filter
func (sb *SessionBus) SubscribeFiltered(topic string, filter *MessageFilter, handler MessageHandler) (Subscription, error) {
	if sb.closed.Load() {
		return nil, ErrSessionClosed
	}
	if handler == nil {
		return nil, ErrInvalidHandler
	}

	// Apply filter to handler
	filteredHandler := FilteredHandler(filter, handler)

	return sb.Subscribe(topic, filteredHandler)
}

func (sb *SessionBus) subscribe(topic string, handler MessageHandler, async bool) (Subscription, error) {
	if err := sb.validateSubscribe(handler); err != nil {
		return nil, err
	}

	fullTopic := sb.resolveTopic(topic)
	filteredHandler := sb.wrapHandler(handler)

	sub, err := sb.createBusSubscription(fullTopic, filteredHandler, async)
	if err != nil {
		return nil, err
	}

	sessSub := sb.trackSubscription(fullTopic, topic, false, sub, nil)
	return sessSub, nil
}

func (sb *SessionBus) validateSubscribe(handler MessageHandler) error {
	if sb.closed.Load() {
		return ErrSessionClosed
	}
	if handler == nil {
		return ErrInvalidHandler
	}
	return nil
}

func (sb *SessionBus) createBusSubscription(topic string, handler MessageHandler, async bool) (Subscription, error) {
	if async {
		return sb.bus.SubscribeAsync(topic, handler)
	}
	return sb.bus.Subscribe(topic, handler)
}

func (sb *SessionBus) trackSubscription(topic, pattern string, isWildcard bool, sub Subscription, topicSub *topicSubscription) *sessionSubscription {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	sessSub := &sessionSubscription{
		id:           atomic.AddInt64(&sb.nextSubID, 1),
		topic:        topic,
		pattern:      pattern,
		isWildcard:   isWildcard,
		subscription: sub,
		topicSub:     topicSub,
	}
	sessSub.active.Store(true)

	sb.subscriptions = append(sb.subscriptions, sessSub)
	atomic.AddInt64(&sb.stats.subscriptionsActive, 1)

	return sessSub
}

func (sb *SessionBus) wrapHandler(handler MessageHandler) MessageHandler {
	return func(msg *Message) error {
		atomic.AddInt64(&sb.stats.messagesReceived, 1)
		return handler(msg)
	}
}

func (sb *SessionBus) routeToWildcardSubscribers(topic string, msg *Message) {
	handlers := sb.router.MatchHandlers(topic)
	for _, handler := range handlers {
		sb.runWildcardHandler(handler, msg)
	}
}

func (sb *SessionBus) runWildcardHandler(handler MessageHandler, msg *Message) {
	atomic.AddInt64(&sb.stats.wildcardMatches, 1)
	if sb.scope == nil {
		sb.runWildcardHandlerDirect(handler, msg)
		return
	}
	sb.runWildcardHandlerWithScope(handler, msg)
}

func (sb *SessionBus) runWildcardHandlerDirect(handler MessageHandler, msg *Message) {
	go func() {
		defer sb.recoverWildcardPanic()
		_ = handler(msg)
	}()
}

func (sb *SessionBus) runWildcardHandlerWithScope(handler MessageHandler, msg *Message) {
	select {
	case sb.wildcardSem <- struct{}{}:
		_ = sb.scope.Go("guide.sessionbus.wildcard_handler", sb.wildcardTimeout(), func(ctx context.Context) error {
			defer func() {
				<-sb.wildcardSem
				sb.recoverWildcardPanic()
			}()
			_ = handler(msg)
			return nil
		})
	default:
	}
}

func (sb *SessionBus) recoverWildcardPanic() {
	if r := recover(); r != nil {
		// Handler panicked - ignore
	}
}

func (sb *SessionBus) wildcardTimeout() time.Duration {
	if sb.scope == nil {
		return 0
	}
	if sb.wildcardTTL != 0 {
		return sb.wildcardTTL
	}
	return 5 * time.Second
}

// Close closes the session bus and all session-scoped subscriptions
func (sb *SessionBus) Close() error {
	if sb.closed.Swap(true) {
		return ErrSessionClosed
	}

	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Unsubscribe all session subscriptions
	for _, sub := range sb.subscriptions {
		sb.unsubscribeLocked(sub)
	}
	sb.subscriptions = nil

	return nil
}

func (sb *SessionBus) unsubscribeLocked(sub *sessionSubscription) {
	if !sub.active.Swap(false) {
		return
	}

	sb.closeUnderlyingSubscription(sub)
	sb.closeRouterSubscription(sub)
	atomic.AddInt64(&sb.stats.subscriptionsActive, -1)
}

func (sb *SessionBus) closeUnderlyingSubscription(sub *sessionSubscription) {
	if sub.subscription != nil {
		_ = sub.subscription.Unsubscribe()
	}
}

func (sb *SessionBus) closeRouterSubscription(sub *sessionSubscription) {
	if sub.topicSub != nil && sb.router != nil {
		sb.router.Unsubscribe(sub.topicSub)
	}
}

// =============================================================================
// Topic Resolution
// =============================================================================

// resolveTopic adds session prefix to local topics
func (sb *SessionBus) resolveTopic(topic string) string {
	// If already session-prefixed or is a global topic, use as-is
	if IsSessionTopic(topic) || sb.isGlobalTopic(topic) {
		return topic
	}

	// Add session prefix
	return BuildSessionTopic(sb.sessionID, extractAgentFromTopic(topic), extractChannelFromTopic(topic))
}

// isGlobalTopic checks if topic should not be session-prefixed
func (sb *SessionBus) isGlobalTopic(topic string) bool {
	return sb.isKnownGlobalTopic(topic) || sb.hasGlobalPrefix(topic)
}

func (sb *SessionBus) isKnownGlobalTopic(topic string) bool {
	return topic == TopicAgentRegistry || topic == TopicRoutesLearned
}

func (sb *SessionBus) hasGlobalPrefix(topic string) bool {
	globalPrefixes := []string{"agents.", "routes.", "system.", "guide."}
	for _, prefix := range globalPrefixes {
		if topicHasPrefix(topic, prefix) {
			return true
		}
	}
	return false
}

func topicHasPrefix(topic, prefix string) bool {
	return len(topic) >= len(prefix) && topic[:len(prefix)] == prefix
}

func extractAgentFromTopic(topic string) string {
	segments := splitTopic(topic)
	if len(segments) > 0 {
		return segments[0]
	}
	return ""
}

func extractChannelFromTopic(topic string) ChannelType {
	segments := splitTopic(topic)
	if len(segments) >= 2 {
		return ChannelType(segments[len(segments)-1])
	}
	return ChannelTypeRequests
}

// =============================================================================
// Session Topic Helpers
// =============================================================================

// SessionID returns the session ID
func (sb *SessionBus) SessionID() string {
	return sb.sessionID
}

// RequestsTopic returns the session's requests topic for an agent
func (sb *SessionBus) RequestsTopic(agentID string) string {
	return BuildSessionTopic(sb.sessionID, agentID, ChannelTypeRequests)
}

// ResponsesTopic returns the session's responses topic for an agent
func (sb *SessionBus) ResponsesTopic(agentID string) string {
	return BuildSessionTopic(sb.sessionID, agentID, ChannelTypeResponses)
}

// ErrorsTopic returns the session's errors topic for an agent
func (sb *SessionBus) ErrorsTopic(agentID string) string {
	return BuildSessionTopic(sb.sessionID, agentID, ChannelTypeErrors)
}

// =============================================================================
// Session Subscription
// =============================================================================

// Topic returns the resolved topic
func (s *sessionSubscription) Topic() string {
	if s.topic != "" {
		return s.topic
	}
	return s.pattern
}

// IsActive returns true if subscription is active
func (s *sessionSubscription) IsActive() bool {
	return s.active.Load()
}

// Unsubscribe removes the subscription
func (s *sessionSubscription) Unsubscribe() error {
	if !s.active.Swap(false) {
		return ErrSubscriptionInvalid
	}

	if s.subscription != nil {
		return s.subscription.Unsubscribe()
	}

	return nil
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns session bus statistics
func (sb *SessionBus) Stats() SessionBusStats {
	return SessionBusStats{
		SessionID:           sb.sessionID,
		MessagesPublished:   atomic.LoadInt64(&sb.stats.messagesPublished),
		MessagesReceived:    atomic.LoadInt64(&sb.stats.messagesReceived),
		SubscriptionsActive: atomic.LoadInt64(&sb.stats.subscriptionsActive),
		WildcardMatches:     atomic.LoadInt64(&sb.stats.wildcardMatches),
	}
}

// SubscriptionCount returns the number of active subscriptions
func (sb *SessionBus) SubscriptionCount() int {
	return int(atomic.LoadInt64(&sb.stats.subscriptionsActive))
}

// =============================================================================
// Session Bus Manager
// =============================================================================

// SessionBusManager manages session buses
type SessionBusManager struct {
	mu sync.RWMutex

	// Session buses by session ID
	sessions map[string]*SessionBus

	// Underlying event bus
	bus EventBus

	// Configuration
	config SessionBusManagerConfig

	scope *concurrency.GoroutineScope

	// Statistics
	stats sessionBusManagerStats
}

type sessionBusManagerStats struct {
	sessionsCreated int64
	sessionsClosed  int64
}

// SessionBusManagerConfig configures the manager
type SessionBusManagerConfig struct {
	// EnableWildcards enables wildcard matching for all session buses
	EnableWildcards bool

	Scope         *concurrency.GoroutineScope
	NotifyTimeout time.Duration

	// OnSessionCreated is called when a session bus is created
	OnSessionCreated func(sessionID string, bus *SessionBus)

	// OnSessionClosed is called when a session bus is closed
	OnSessionClosed func(sessionID string)
}

// SessionBusManagerStats contains manager statistics
type SessionBusManagerStats struct {
	ActiveSessions  int   `json:"active_sessions"`
	SessionsCreated int64 `json:"sessions_created"`
	SessionsClosed  int64 `json:"sessions_closed"`
}

// NewSessionBusManager creates a new session bus manager
func NewSessionBusManager(bus EventBus, cfg SessionBusManagerConfig) *SessionBusManager {
	return &SessionBusManager{
		sessions: make(map[string]*SessionBus),
		bus:      bus,
		config:   cfg,
		scope:    cfg.Scope,
	}
}

// GetOrCreate returns an existing session bus or creates a new one
func (m *SessionBusManager) GetOrCreate(sessionID string) (*SessionBus, error) {
	if sessionID == "" {
		return nil, ErrInvalidSessionID
	}

	if sb := m.getExisting(sessionID); sb != nil {
		return sb, nil
	}

	return m.createNew(sessionID)
}

func (m *SessionBusManager) getExisting(sessionID string) *SessionBus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

func (m *SessionBusManager) createNew(sessionID string) (*SessionBus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if sb, ok := m.sessions[sessionID]; ok {
		return sb, nil
	}

	sb, err := m.buildSessionBus(sessionID)
	if err != nil {
		return nil, err
	}

	m.sessions[sessionID] = sb
	atomic.AddInt64(&m.stats.sessionsCreated, 1)
	m.notifySessionCreated(sessionID, sb)

	return sb, nil
}

func (m *SessionBusManager) buildSessionBus(sessionID string) (*SessionBus, error) {
	return NewSessionBus(m.bus, SessionBusConfig{
		SessionID:       sessionID,
		EnableWildcards: m.config.EnableWildcards,
		Scope:           m.scope,
		WildcardTimeout: m.config.NotifyTimeout,
	})
}

func (m *SessionBusManager) notifySessionCreated(sessionID string, sb *SessionBus) {
	if m.config.OnSessionCreated == nil {
		return
	}
	m.runNotify("guide.sessionbus.session_created", m.config.OnSessionCreated, sessionID, sb)
}

// Get returns an existing session bus or nil
func (m *SessionBusManager) Get(sessionID string) *SessionBus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

// Close closes and removes a session bus
func (m *SessionBusManager) Close(sessionID string) error {
	m.mu.Lock()
	sb, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	atomic.AddInt64(&m.stats.sessionsClosed, 1)

	if m.config.OnSessionClosed != nil {
		m.runNotify("guide.sessionbus.session_closed", func(id string, _ *SessionBus) {
			m.config.OnSessionClosed(id)
		}, sessionID, nil)
	}

	return sb.Close()
}

// CloseAll closes all session buses
func (m *SessionBusManager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*SessionBus, 0, len(m.sessions))
	for _, sb := range m.sessions {
		sessions = append(sessions, sb)
	}
	m.sessions = make(map[string]*SessionBus)
	m.mu.Unlock()

	for _, sb := range sessions {
		_ = sb.Close()
		atomic.AddInt64(&m.stats.sessionsClosed, 1)
	}
}

// Stats returns manager statistics
func (m *SessionBusManager) Stats() SessionBusManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return SessionBusManagerStats{
		ActiveSessions:  len(m.sessions),
		SessionsCreated: atomic.LoadInt64(&m.stats.sessionsCreated),
		SessionsClosed:  atomic.LoadInt64(&m.stats.sessionsClosed),
	}
}

// ActiveSessionIDs returns all active session IDs
func (m *SessionBusManager) ActiveSessionIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

func (m *SessionBusManager) runNotify(desc string, handler func(string, *SessionBus), sessionID string, sb *SessionBus) {
	if m.scope == nil {
		handler(sessionID, sb)
		return
	}
	_ = m.scope.Go(desc, m.notifyTimeout(), func(ctx context.Context) error {
		handler(sessionID, sb)
		return nil
	})
}

func (m *SessionBusManager) notifyTimeout() time.Duration {
	if m.config.NotifyTimeout != 0 {
		return m.config.NotifyTimeout
	}
	return 5 * time.Second
}

// =============================================================================
// Session Lifecycle Events
// =============================================================================

// SessionStartedPayload is the payload for session started events
type SessionStartedPayload struct {
	SessionID string            `json:"session_id"`
	StartedAt time.Time         `json:"started_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SessionClosedPayload is the payload for session closed events
type SessionClosedPayload struct {
	SessionID string          `json:"session_id"`
	ClosedAt  time.Time       `json:"closed_at"`
	Duration  time.Duration   `json:"duration"`
	Stats     SessionBusStats `json:"stats"`
}

// Session lifecycle topics
const (
	TopicSessionStarted = "sessions.started"
	TopicSessionClosed  = "sessions.closed"
)

// PublishSessionStarted publishes a session started event
func PublishSessionStarted(bus EventBus, sessionID string, metadata map[string]string) error {
	msg := &Message{
		ID:            sessionID + "-started",
		Type:          "session_started",
		SourceAgentID: "session_manager",
		Timestamp:     time.Now(),
		Payload: &SessionStartedPayload{
			SessionID: sessionID,
			StartedAt: time.Now(),
			Metadata:  metadata,
		},
	}
	return bus.Publish(TopicSessionStarted, msg)
}

// PublishSessionClosed publishes a session closed event
func PublishSessionClosed(bus EventBus, sessionID string, duration time.Duration, stats SessionBusStats) error {
	msg := &Message{
		ID:            sessionID + "-closed",
		Type:          "session_closed",
		SourceAgentID: "session_manager",
		Timestamp:     time.Now(),
		Payload: &SessionClosedPayload{
			SessionID: sessionID,
			ClosedAt:  time.Now(),
			Duration:  duration,
			Stats:     stats,
		},
	}
	return bus.Publish(TopicSessionClosed, msg)
}
