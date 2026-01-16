package guide

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/messaging"
)

// =============================================================================
// Topic Router
// =============================================================================
//
// TopicRouter provides efficient topic matching with wildcard support.
// Uses a trie-based structure for O(n) pattern matching where n is the
// number of segments in the topic.
//
// Wildcard patterns:
// - "*" matches exactly one segment: "session.*.requests" matches "session.abc.requests"
// - "**" matches one or more segments: "session.**" matches "session.abc" and "session.abc.def"
//
// Topic format: segments separated by "."
// - session.{session_id}.{agent}.{channel}
// - {agent}.{channel}

// TopicRouter matches topics to subscriptions with wildcard support
type TopicRouter struct {
	mu sync.RWMutex

	// Trie root for pattern matching
	root *trieNode

	// Exact match lookup (optimization for non-wildcard topics)
	exact map[string][]*topicSubscription

	// Statistics
	stats topicRouterStats
}

// topicRouterStats holds atomic counters for statistics
type topicRouterStats struct {
	lookups        int64
	exactHits      int64
	wildcardHits   int64
	totalPatterns  int64
	wildcardRoutes int64
}

// TopicRouterStats contains router statistics
type TopicRouterStats struct {
	Lookups        int64 `json:"lookups"`
	ExactHits      int64 `json:"exact_hits"`
	WildcardHits   int64 `json:"wildcard_hits"`
	TotalPatterns  int64 `json:"total_patterns"`
	WildcardRoutes int64 `json:"wildcard_routes"`
}

// trieNode represents a node in the topic trie
type trieNode struct {
	// Children by segment name
	children map[string]*trieNode

	// Single segment wildcard "*"
	singleWildcard *trieNode

	// Multi-segment wildcard "**"
	multiWildcard *trieNode

	// Subscriptions at this node (only for terminal nodes)
	subscriptions []*topicSubscription

	// Whether this is a terminal node (pattern ends here)
	isTerminal bool
}

// topicSubscription represents a subscription to a topic pattern
type topicSubscription struct {
	id      int64
	pattern string
	handler MessageHandler
	active  atomic.Bool
}

// NewTopicRouter creates a new topic router
func NewTopicRouter() *TopicRouter {
	return &TopicRouter{
		root:  newTrieNode(),
		exact: make(map[string][]*topicSubscription),
	}
}

func newTrieNode() *trieNode {
	return &trieNode{
		children:      make(map[string]*trieNode),
		subscriptions: make([]*topicSubscription, 0),
	}
}

// =============================================================================
// Pattern Operations
// =============================================================================

// Subscribe adds a subscription for a topic pattern
func (r *TopicRouter) Subscribe(pattern string, handler MessageHandler) *topicSubscription {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := &topicSubscription{
		id:      atomic.AddInt64(&r.stats.totalPatterns, 1),
		pattern: pattern,
		handler: handler,
	}
	sub.active.Store(true)

	// Check if pattern has wildcards
	if !hasWildcard(pattern) {
		// Exact match - use fast path
		r.exact[pattern] = append(r.exact[pattern], sub)
	} else {
		// Wildcard pattern - insert into trie
		r.insertPattern(pattern, sub)
		atomic.AddInt64(&r.stats.wildcardRoutes, 1)
	}

	return sub
}

// Unsubscribe removes a subscription
func (r *TopicRouter) Unsubscribe(sub *topicSubscription) {
	if sub == nil {
		return
	}
	sub.active.Store(false)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if it's an exact match subscription
	if !hasWildcard(sub.pattern) {
		subs := r.exact[sub.pattern]
		for i, s := range subs {
			if s.id == sub.id {
				r.exact[sub.pattern] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(r.exact[sub.pattern]) == 0 {
			delete(r.exact, sub.pattern)
		}
	} else {
		// Remove from trie
		r.removePattern(sub.pattern, sub.id)
		atomic.AddInt64(&r.stats.wildcardRoutes, -1)
	}
}

// Match returns all subscriptions that match a topic
func (r *TopicRouter) Match(topic string) []*topicSubscription {
	atomic.AddInt64(&r.stats.lookups, 1)

	r.mu.RLock()
	defer r.mu.RUnlock()

	var matches []*topicSubscription

	// Check exact matches first (fast path)
	if subs := r.exact[topic]; len(subs) > 0 {
		atomic.AddInt64(&r.stats.exactHits, 1)
		for _, sub := range subs {
			if sub.active.Load() {
				matches = append(matches, sub)
			}
		}
	}

	// Check wildcard patterns
	segments := splitTopic(topic)
	wildcardMatches := r.matchWildcards(segments)
	if len(wildcardMatches) > 0 {
		atomic.AddInt64(&r.stats.wildcardHits, 1)
		matches = append(matches, wildcardMatches...)
	}

	return matches
}

// MatchHandlers returns all handlers that match a topic
func (r *TopicRouter) MatchHandlers(topic string) []MessageHandler {
	subs := r.Match(topic)
	handlers := make([]MessageHandler, 0, len(subs))
	for _, sub := range subs {
		handlers = append(handlers, sub.handler)
	}
	return handlers
}

// =============================================================================
// Trie Operations
// =============================================================================

func (r *TopicRouter) insertPattern(pattern string, sub *topicSubscription) {
	segments := splitTopic(pattern)
	node := r.root

	for _, seg := range segments {
		switch seg {
		case "*":
			if node.singleWildcard == nil {
				node.singleWildcard = newTrieNode()
			}
			node = node.singleWildcard
		case "**":
			if node.multiWildcard == nil {
				node.multiWildcard = newTrieNode()
			}
			node = node.multiWildcard
		default:
			child, ok := node.children[seg]
			if !ok {
				child = newTrieNode()
				node.children[seg] = child
			}
			node = child
		}
	}

	node.isTerminal = true
	node.subscriptions = append(node.subscriptions, sub)
}

func (r *TopicRouter) removePattern(pattern string, subID int64) {
	segments := splitTopic(pattern)
	node := r.root

	for _, seg := range segments {
		switch seg {
		case "*":
			if node.singleWildcard == nil {
				return
			}
			node = node.singleWildcard
		case "**":
			if node.multiWildcard == nil {
				return
			}
			node = node.multiWildcard
		default:
			child, ok := node.children[seg]
			if !ok {
				return
			}
			node = child
		}
	}

	// Remove subscription from terminal node
	for i, sub := range node.subscriptions {
		if sub.id == subID {
			node.subscriptions = append(node.subscriptions[:i], node.subscriptions[i+1:]...)
			break
		}
	}

	if len(node.subscriptions) == 0 {
		node.isTerminal = false
	}
}

func (r *TopicRouter) matchWildcards(segments []string) []*topicSubscription {
	// Use a map to deduplicate matches (subscription ID -> subscription)
	seen := make(map[int64]*topicSubscription)
	r.matchRecursive(r.root, segments, 0, seen)

	// Convert map to slice
	matches := make([]*topicSubscription, 0, len(seen))
	for _, sub := range seen {
		matches = append(matches, sub)
	}
	return matches
}

func (r *TopicRouter) matchRecursive(node *trieNode, segments []string, idx int, seen map[int64]*topicSubscription) {
	if node == nil {
		return
	}

	// Check if we've consumed all segments
	if idx >= len(segments) {
		if node.isTerminal {
			for _, sub := range node.subscriptions {
				if sub.active.Load() {
					seen[sub.id] = sub
				}
			}
		}
		return
	}

	segment := segments[idx]

	// Try exact match
	if child, ok := node.children[segment]; ok {
		r.matchRecursive(child, segments, idx+1, seen)
	}

	// Try single wildcard "*" - matches exactly one segment
	if node.singleWildcard != nil {
		r.matchRecursive(node.singleWildcard, segments, idx+1, seen)
	}

	// Try multi wildcard "**" - matches one or more segments
	if node.multiWildcard != nil {
		// ** at end of pattern matches all remaining segments
		if node.multiWildcard.isTerminal {
			for _, sub := range node.multiWildcard.subscriptions {
				if sub.active.Load() {
					seen[sub.id] = sub
				}
			}
		}

		// ** can consume current segment and continue matching
		// Try consuming 1 segment
		r.matchRecursive(node.multiWildcard, segments, idx+1, seen)

		// ** can also stay in place to match more segments
		// Try consuming 2+ segments by recursively consuming from multiWildcard
		for i := idx + 2; i <= len(segments); i++ {
			r.matchRecursive(node.multiWildcard, segments, i, seen)
		}
	}
}

// =============================================================================
// Topic Format Helpers
// =============================================================================

// SessionTopicPrefix returns the prefix for session-scoped topics
const SessionTopicPrefix = "session"

// BuildSessionTopic creates a session-scoped topic
func BuildSessionTopic(sessionID, agentID string, channelType ChannelType) string {
	return SessionTopicPrefix + "." + sessionID + "." + agentID + "." + string(channelType)
}

// BuildSessionTopicPattern creates a wildcard pattern for session topics
func BuildSessionTopicPattern(pattern string) string {
	return SessionTopicPrefix + "." + pattern
}

// ParseSessionTopic extracts components from a session-scoped topic
func ParseSessionTopic(topic string) (sessionID, agentID, channel string, ok bool) {
	segments := splitTopic(topic)
	if len(segments) != 4 || segments[0] != SessionTopicPrefix {
		return "", "", "", false
	}
	return segments[1], segments[2], segments[3], true
}

// IsSessionTopic checks if a topic is session-scoped
func IsSessionTopic(topic string) bool {
	return strings.HasPrefix(topic, SessionTopicPrefix+".")
}

// IsGlobalTopic checks if a topic is global (not session-scoped)
func IsGlobalTopic(topic string) bool {
	return !IsSessionTopic(topic)
}

// splitTopic splits a topic into segments
func splitTopic(topic string) []string {
	if topic == "" {
		return nil
	}
	return strings.Split(topic, ".")
}

// hasWildcard checks if a pattern contains wildcards
func hasWildcard(pattern string) bool {
	return strings.Contains(pattern, "*")
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns router statistics
func (r *TopicRouter) Stats() TopicRouterStats {
	return TopicRouterStats{
		Lookups:        atomic.LoadInt64(&r.stats.lookups),
		ExactHits:      atomic.LoadInt64(&r.stats.exactHits),
		WildcardHits:   atomic.LoadInt64(&r.stats.wildcardHits),
		TotalPatterns:  atomic.LoadInt64(&r.stats.totalPatterns),
		WildcardRoutes: atomic.LoadInt64(&r.stats.wildcardRoutes),
	}
}

// PatternCount returns the number of registered patterns
func (r *TopicRouter) PatternCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, subs := range r.exact {
		count += len(subs)
	}
	count += int(atomic.LoadInt64(&r.stats.wildcardRoutes))
	return count
}

// =============================================================================
// Message Filters
// =============================================================================

// MessageFilter filters messages based on criteria
type MessageFilter struct {
	// SessionID filters by session (exact match or empty for all)
	SessionID string

	// MessageTypes filters by message type (empty for all)
	MessageTypes []MessageType

	// MinPriority filters by minimum priority
	MinPriority *messaging.Priority

	// MaxPriority filters by maximum priority
	MaxPriority *messaging.Priority

	// SourceAgentID filters by source agent
	SourceAgentID string

	// TargetAgentID filters by target agent
	TargetAgentID string

	// Custom predicate for additional filtering
	Predicate func(*Message) bool
}

// Matches checks if a message matches the filter
func (f *MessageFilter) Matches(msg *Message) bool {
	if msg == nil {
		return false
	}

	// Filter by session ID (from metadata)
	if f.SessionID != "" {
		if msg.Metadata == nil {
			return false
		}
		if sessID, ok := msg.Metadata["session_id"].(string); !ok || sessID != f.SessionID {
			return false
		}
	}

	// Filter by message type
	if len(f.MessageTypes) > 0 {
		found := false
		for _, t := range f.MessageTypes {
			if msg.Type == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by priority range
	if f.MinPriority != nil && msg.Priority < *f.MinPriority {
		return false
	}
	if f.MaxPriority != nil && msg.Priority > *f.MaxPriority {
		return false
	}

	// Filter by source agent
	if f.SourceAgentID != "" && msg.SourceAgentID != f.SourceAgentID {
		return false
	}

	// Filter by target agent
	if f.TargetAgentID != "" && msg.TargetAgentID != f.TargetAgentID {
		return false
	}

	// Apply custom predicate
	if f.Predicate != nil && !f.Predicate(msg) {
		return false
	}

	return true
}

// FilteredHandler wraps a handler with a filter
func FilteredHandler(filter *MessageFilter, handler MessageHandler) MessageHandler {
	if filter == nil {
		return handler
	}
	return func(msg *Message) error {
		if filter.Matches(msg) {
			return handler(msg)
		}
		return nil // Filtered out, not an error
	}
}

// =============================================================================
// Topic Pattern Builder
// =============================================================================

// TopicPatternBuilder helps construct topic patterns
type TopicPatternBuilder struct {
	segments []string
}

// NewTopicPattern creates a new topic pattern builder
func NewTopicPattern() *TopicPatternBuilder {
	return &TopicPatternBuilder{
		segments: make([]string, 0),
	}
}

// Session adds the session prefix
func (b *TopicPatternBuilder) Session() *TopicPatternBuilder {
	b.segments = append(b.segments, SessionTopicPrefix)
	return b
}

// SessionID adds a session ID or wildcard
func (b *TopicPatternBuilder) SessionID(id string) *TopicPatternBuilder {
	b.segments = append(b.segments, id)
	return b
}

// AnySession adds a wildcard for any session
func (b *TopicPatternBuilder) AnySession() *TopicPatternBuilder {
	b.segments = append(b.segments, "*")
	return b
}

// Agent adds an agent ID
func (b *TopicPatternBuilder) Agent(id string) *TopicPatternBuilder {
	b.segments = append(b.segments, id)
	return b
}

// AnyAgent adds a wildcard for any agent
func (b *TopicPatternBuilder) AnyAgent() *TopicPatternBuilder {
	b.segments = append(b.segments, "*")
	return b
}

// Channel adds a channel type
func (b *TopicPatternBuilder) Channel(ch ChannelType) *TopicPatternBuilder {
	b.segments = append(b.segments, string(ch))
	return b
}

// AnyChannel adds a wildcard for any channel
func (b *TopicPatternBuilder) AnyChannel() *TopicPatternBuilder {
	b.segments = append(b.segments, "*")
	return b
}

// Any adds a single-segment wildcard
func (b *TopicPatternBuilder) Any() *TopicPatternBuilder {
	b.segments = append(b.segments, "*")
	return b
}

// AnyRemaining adds a multi-segment wildcard (matches rest)
func (b *TopicPatternBuilder) AnyRemaining() *TopicPatternBuilder {
	b.segments = append(b.segments, "**")
	return b
}

// Segment adds a literal segment
func (b *TopicPatternBuilder) Segment(s string) *TopicPatternBuilder {
	b.segments = append(b.segments, s)
	return b
}

// Build returns the constructed topic pattern
func (b *TopicPatternBuilder) Build() string {
	return strings.Join(b.segments, ".")
}

// Common topic patterns
var (
	// AllSessionRequests matches all session request topics: session.*.*.requests
	AllSessionRequests = NewTopicPattern().Session().AnySession().AnyAgent().Channel(ChannelTypeRequests).Build()

	// AllSessionResponses matches all session response topics: session.*.*.responses
	AllSessionResponses = NewTopicPattern().Session().AnySession().AnyAgent().Channel(ChannelTypeResponses).Build()

	// AllAgentRequests matches all agent request topics: *.requests
	AllAgentRequests = NewTopicPattern().AnyAgent().Channel(ChannelTypeRequests).Build()

	// AllAgentResponses matches all agent response topics: *.responses
	AllAgentResponses = NewTopicPattern().AnyAgent().Channel(ChannelTypeResponses).Build()
)

// SessionPattern creates a pattern for a specific session's topics
func SessionPattern(sessionID string, pattern string) string {
	return SessionTopicPrefix + "." + sessionID + "." + pattern
}

// AllSessionTopics returns a pattern matching all topics for a session
func AllSessionTopics(sessionID string) string {
	return SessionTopicPrefix + "." + sessionID + ".**"
}
