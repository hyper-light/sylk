package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// EventType categorizes the type of event
type EventType string

const (
	EventTypeAgentRegister   EventType = "agent_register"
	EventTypeAgentUnregister EventType = "agent_unregister"
	EventTypeFileRead        EventType = "file_read"
	EventTypeFileModify      EventType = "file_modify"
	EventTypeFileCreate      EventType = "file_create"
	EventTypePatternAdd      EventType = "pattern_add"
	EventTypePatternUpdate   EventType = "pattern_update"
	EventTypePatternRemove   EventType = "pattern_remove"
	EventTypeFailureRecord   EventType = "failure_record"
	EventTypeIntentAdd       EventType = "intent_add"
	EventTypeIntentRemove    EventType = "intent_remove"
	EventTypeResumeUpdate    EventType = "resume_update"
	EventTypeEntryStore      EventType = "entry_store"
	EventTypeEntryUpdate     EventType = "entry_update"
	EventTypeEntryArchive    EventType = "entry_archive"
	EventTypeConflictDetect  EventType = "conflict_detect"
	EventTypeConflictResolve EventType = "conflict_resolve"
	EventTypeSessionStart    EventType = "session_start"
	EventTypeSessionEnd      EventType = "session_end"
)

// Event represents an immutable event in the log
type Event struct {
	ID         string         `json:"id"`
	Type       EventType      `json:"type"`
	Version    string         `json:"version"`
	Clock      uint64         `json:"clock"`
	AgentID    string         `json:"agent_id"`
	SessionID  string         `json:"session_id"`
	Scope      Scope          `json:"scope,omitempty"`
	Key        string         `json:"key,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
	PreviousID string         `json:"previous_id,omitempty"`
}

// EventLog is a high-performance append-only log optimized for concurrent access
type EventLog struct {
	// Sharded locks for reduced contention
	eventsMu  sync.RWMutex // Protects main event slice
	indexesMu sync.RWMutex // Protects all indexes

	// Main event storage - ring buffer for bounded memory
	events    []*Event
	head      int // Write position in ring buffer
	tail      int // Read start position
	count     int // Current number of events
	maxEvents int

	// Indexes using sync.Map for better concurrent read performance
	byID      sync.Map // map[string]*Event
	byVersion sync.Map // map[string]*Event

	// Slice-based indexes (need mutex protection)
	byAgent   map[string][]*Event
	bySession map[string][]*Event
	byType    map[EventType][]*Event
	byScope   map[Scope][]*Event

	// Lock-free counters using atomics
	sequence  atomic.Uint64
	lastClock atomic.Uint64

	// Async write channel for non-blocking appends
	appendChan chan *Event
	doneChan   chan struct{}

	// Configuration
	archiveFunc   func([]*Event) error
	batchSize     int
	flushInterval time.Duration
}

// EventLogConfig configures the event log
type EventLogConfig struct {
	MaxEvents     int                  // Max events in memory (default: 10000)
	ArchiveFunc   func([]*Event) error // Called when trimming
	BatchSize     int                  // Events to batch before flush (default: 100)
	FlushInterval time.Duration        // Max time between flushes (default: 100ms)
	AsyncWrites   bool                 // Enable async channel-based writes
}

// NewEventLog creates a new optimized event log
func NewEventLog(cfg EventLogConfig) *EventLog {
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 10000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 100 * time.Millisecond
	}

	el := &EventLog{
		events:        make([]*Event, cfg.MaxEvents),
		maxEvents:     cfg.MaxEvents,
		byAgent:       make(map[string][]*Event),
		bySession:     make(map[string][]*Event),
		byType:        make(map[EventType][]*Event),
		byScope:       make(map[Scope][]*Event),
		archiveFunc:   cfg.ArchiveFunc,
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
	}

	if cfg.AsyncWrites {
		el.appendChan = make(chan *Event, cfg.BatchSize*2)
		el.doneChan = make(chan struct{})
		go el.asyncWriter()
	}

	return el
}

// asyncWriter processes events from the channel in batches
func (el *EventLog) asyncWriter() {
	batch := make([]*Event, 0, el.batchSize)
	ticker := time.NewTicker(el.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-el.appendChan:
			if !ok {
				// Channel closed, flush remaining
				if len(batch) > 0 {
					el.flushBatch(batch)
				}
				close(el.doneChan)
				return
			}
			batch = append(batch, event)
			if len(batch) >= el.batchSize {
				el.flushBatch(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				el.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// flushBatch writes a batch of events (called from asyncWriter)
func (el *EventLog) flushBatch(batch []*Event) {
	el.eventsMu.Lock()
	for _, event := range batch {
		el.appendEventLocked(event)
	}
	el.eventsMu.Unlock()

	// Update indexes in separate lock scope
	el.indexesMu.Lock()
	for _, event := range batch {
		el.updateIndexesLocked(event)
	}
	el.indexesMu.Unlock()
}

// Append adds an event to the log
func (el *EventLog) Append(event *Event) error {
	// Prepare event metadata
	if event.ID == "" {
		event.ID = fmt.Sprintf("e%d", el.sequence.Add(1))
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Update clock atomically
	for {
		current := el.lastClock.Load()
		newClock := current + 1
		if event.Clock > current {
			newClock = event.Clock + 1
		}
		if el.lastClock.CompareAndSwap(current, newClock) {
			event.Clock = newClock
			break
		}
	}

	// If async mode, send to channel
	if el.appendChan != nil {
		select {
		case el.appendChan <- event:
			return nil
		default:
			// Channel full, fall back to sync write
		}
	}

	// Synchronous write
	el.eventsMu.Lock()
	el.appendEventLocked(event)
	el.eventsMu.Unlock()

	el.indexesMu.Lock()
	el.updateIndexesLocked(event)
	el.indexesMu.Unlock()

	return nil
}

// appendEventLocked adds event to ring buffer (caller must hold eventsMu)
func (el *EventLog) appendEventLocked(event *Event) {
	// Link to previous
	if el.count > 0 {
		prevIdx := (el.head - 1 + el.maxEvents) % el.maxEvents
		if el.events[prevIdx] != nil {
			event.PreviousID = el.events[prevIdx].ID
		}
	}

	// Check if we need to archive old events
	if el.count >= el.maxEvents {
		el.archiveOldestLocked()
	}

	// Add to ring buffer
	el.events[el.head] = event
	el.head = (el.head + 1) % el.maxEvents
	if el.count < el.maxEvents {
		el.count++
	}
}

// archiveOldestLocked removes oldest events (caller must hold eventsMu)
func (el *EventLog) archiveOldestLocked() {
	archiveCount := el.archiveCount()
	if el.archiveFunc == nil {
		el.removeOldest(archiveCount)
		return
	}

	toArchive := el.collectOldestForArchive(archiveCount)
	go func(events []*Event) {
		_ = el.archiveFunc(events)
	}(toArchive)
}

func (el *EventLog) archiveCount() int {
	archiveCount := el.maxEvents / 10
	if archiveCount < 1 {
		return 1
	}
	return archiveCount
}

func (el *EventLog) collectOldestForArchive(archiveCount int) []*Event {
	toArchive := make([]*Event, 0, archiveCount)
	for i := 0; i < archiveCount && el.count > 0; i++ {
		event := el.events[el.tail]
		if event != nil {
			toArchive = append(toArchive, event)
			el.removeEventIndexes(event)
		}
		el.clearOldestEvent()
	}
	return toArchive
}

func (el *EventLog) removeOldest(archiveCount int) {
	for i := 0; i < archiveCount && el.count > 0; i++ {
		event := el.events[el.tail]
		if event != nil {
			el.removeEventIndexes(event)
		}
		el.clearOldestEvent()
	}
}

func (el *EventLog) removeEventIndexes(event *Event) {
	el.byID.Delete(event.ID)
	el.byVersion.Delete(event.Version)
}

func (el *EventLog) clearOldestEvent() {
	el.events[el.tail] = nil
	el.tail = (el.tail + 1) % el.maxEvents
	el.count--
}

// updateIndexesLocked updates all indexes (caller must hold indexesMu)
func (el *EventLog) updateIndexesLocked(event *Event) {
	// sync.Map indexes (lock-free)
	el.byID.Store(event.ID, event)
	if event.Version != "" {
		el.byVersion.Store(event.Version, event)
	}

	// Slice indexes
	el.byAgent[event.AgentID] = append(el.byAgent[event.AgentID], event)
	el.bySession[event.SessionID] = append(el.bySession[event.SessionID], event)
	el.byType[event.Type] = append(el.byType[event.Type], event)
	if event.Scope != "" {
		el.byScope[event.Scope] = append(el.byScope[event.Scope], event)
	}
}

// Close shuts down the event log gracefully
func (el *EventLog) Close() {
	if el.appendChan != nil {
		close(el.appendChan)
		<-el.doneChan // Wait for async writer to finish
	}
}

// Get retrieves an event by ID (lock-free via sync.Map)
func (el *EventLog) Get(id string) *Event {
	if v, ok := el.byID.Load(id); ok {
		return v.(*Event)
	}
	return nil
}

// GetByVersion retrieves event by version (lock-free via sync.Map)
func (el *EventLog) GetByVersion(version string) *Event {
	if v, ok := el.byVersion.Load(version); ok {
		return v.(*Event)
	}
	return nil
}

// GetByAgent retrieves events for an agent
func (el *EventLog) GetByAgent(agentID string, limit int) []*Event {
	el.indexesMu.RLock()
	events := el.byAgent[agentID]
	el.indexesMu.RUnlock()

	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:]
	}
	return events
}

// GetBySession retrieves events for a session
func (el *EventLog) GetBySession(sessionID string, limit int) []*Event {
	el.indexesMu.RLock()
	events := el.bySession[sessionID]
	el.indexesMu.RUnlock()

	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:]
	}
	return events
}

// GetByType retrieves events of a specific type
func (el *EventLog) GetByType(eventType EventType, limit int) []*Event {
	el.indexesMu.RLock()
	events := el.byType[eventType]
	el.indexesMu.RUnlock()

	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:]
	}
	return events
}

// GetByScope retrieves events for a scope
func (el *EventLog) GetByScope(scope Scope, limit int) []*Event {
	el.indexesMu.RLock()
	events := el.byScope[scope]
	el.indexesMu.RUnlock()

	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:]
	}
	return events
}

// GetSinceVersion retrieves all events after a given version
func (el *EventLog) GetSinceVersion(version string) []*Event {
	startEvent := el.GetByVersion(version)

	el.eventsMu.RLock()
	defer el.eventsMu.RUnlock()

	if startEvent == nil {
		// Version not found, return all events
		return el.getAllEventsLocked()
	}

	// Find start event and return everything after
	var results []*Event
	found := false
	for i := 0; i < el.count; i++ {
		idx := (el.tail + i) % el.maxEvents
		event := el.events[idx]
		if event == nil {
			continue
		}
		if found {
			results = append(results, event)
		} else if event.ID == startEvent.ID {
			found = true
		}
	}
	return results
}

// getAllEventsLocked returns all events (caller must hold eventsMu.RLock)
func (el *EventLog) getAllEventsLocked() []*Event {
	results := make([]*Event, 0, el.count)
	for i := 0; i < el.count; i++ {
		idx := (el.tail + i) % el.maxEvents
		if el.events[idx] != nil {
			results = append(results, el.events[idx])
		}
	}
	return results
}

// GetSinceClock retrieves all events after a given Lamport clock value
func (el *EventLog) GetSinceClock(clock uint64) []*Event {
	el.eventsMu.RLock()
	defer el.eventsMu.RUnlock()

	var results []*Event
	for i := 0; i < el.count; i++ {
		idx := (el.tail + i) % el.maxEvents
		event := el.events[idx]
		if event != nil && event.Clock > clock {
			results = append(results, event)
		}
	}
	return results
}

// GetRecent retrieves the most recent events
func (el *EventLog) GetRecent(n int) []*Event {
	el.eventsMu.RLock()
	defer el.eventsMu.RUnlock()

	if n <= 0 || n > el.count {
		n = el.count
	}

	results := make([]*Event, 0, n)
	startIdx := el.count - n
	for i := startIdx; i < el.count; i++ {
		idx := (el.tail + i) % el.maxEvents
		if el.events[idx] != nil {
			results = append(results, el.events[idx])
		}
	}
	return results
}

// GetDelta returns events as delta entries for efficient response
func (el *EventLog) GetDelta(sinceVersion string, limit int) []DeltaEntry {
	events := el.GetSinceVersion(sinceVersion)

	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}

	delta := make([]DeltaEntry, len(events))
	for i, e := range events {
		delta[i] = DeltaEntry{
			Version: e.Version,
			Type:    eventTypeToChangeType(e.Type),
			Scope:   string(e.Scope),
			Key:     e.Key,
			Data:    e.Data,
			AgentID: e.AgentID,
		}
	}
	return delta
}

func eventTypeToChangeType(et EventType) string {
	switch et {
	case EventTypeFileCreate, EventTypePatternAdd, EventTypeIntentAdd,
		EventTypeAgentRegister, EventTypeFailureRecord, EventTypeEntryStore:
		return "add"
	case EventTypeFileModify, EventTypePatternUpdate, EventTypeResumeUpdate, EventTypeEntryUpdate:
		return "update"
	case EventTypePatternRemove, EventTypeIntentRemove, EventTypeAgentUnregister, EventTypeEntryArchive:
		return "delete"
	default:
		return "update"
	}
}

// Len returns the number of events in the log
func (el *EventLog) Len() int {
	el.eventsMu.RLock()
	defer el.eventsMu.RUnlock()
	return el.count
}

// LastClock returns the last Lamport clock value (lock-free)
func (el *EventLog) LastClock() uint64 {
	return el.lastClock.Load()
}

// EventQuery specifies filters for querying events
type EventQuery struct {
	AgentID    string      `json:"agent_id,omitempty"`
	SessionID  string      `json:"session_id,omitempty"`
	Types      []EventType `json:"types,omitempty"`
	Scopes     []Scope     `json:"scopes,omitempty"`
	Since      *time.Time  `json:"since,omitempty"`
	Until      *time.Time  `json:"until,omitempty"`
	SinceClock uint64      `json:"since_clock,omitempty"`
	Limit      int         `json:"limit,omitempty"`
}

// Query executes a query against the event log
func (el *EventLog) Query(q EventQuery) []*Event {
	el.eventsMu.RLock()
	defer el.eventsMu.RUnlock()

	var results []*Event
	for i := 0; i < el.count; i++ {
		idx := (el.tail + i) % el.maxEvents
		e := el.events[idx]
		if e != nil && el.matchesQuery(e, q) {
			results = append(results, e)
		}
	}

	if q.Limit > 0 && len(results) > q.Limit {
		results = results[len(results)-q.Limit:]
	}
	return results
}

// QueryWithContext supports cancellation
func (el *EventLog) QueryWithContext(ctx context.Context, q EventQuery) ([]*Event, error) {
	el.eventsMu.RLock()
	defer el.eventsMu.RUnlock()

	var results []*Event
	for i := 0; i < el.count; i++ {
		if shouldCancelQuery(ctx, i) {
			return results, ctx.Err()
		}

		event := el.eventAtIndex(i)
		if event != nil && el.matchesQuery(event, q) {
			results = append(results, event)
		}
	}

	return applyQueryLimit(results, q.Limit), nil
}

func shouldCancelQuery(ctx context.Context, idx int) bool {
	if idx%1000 != 0 {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func (el *EventLog) eventAtIndex(i int) *Event {
	idx := (el.tail + i) % el.maxEvents
	return el.events[idx]
}

func applyQueryLimit(results []*Event, limit int) []*Event {
	if limit > 0 && len(results) > limit {
		return results[len(results)-limit:]
	}
	return results
}

func (el *EventLog) matchesQuery(e *Event, q EventQuery) bool {
	return el.matchesIDFilters(e, q) &&
		el.matchesTypeFilters(e, q) &&
		el.matchesTimeFilters(e, q)
}

func (el *EventLog) matchesIDFilters(e *Event, q EventQuery) bool {
	if q.AgentID != "" && e.AgentID != q.AgentID {
		return false
	}
	if q.SessionID != "" && e.SessionID != q.SessionID {
		return false
	}
	return true
}

func (el *EventLog) matchesTypeFilters(e *Event, q EventQuery) bool {
	if len(q.Types) > 0 && !containsEventType(q.Types, e.Type) {
		return false
	}
	if len(q.Scopes) > 0 && !containsScope(q.Scopes, e.Scope) {
		return false
	}
	return true
}

func (el *EventLog) matchesTimeFilters(e *Event, q EventQuery) bool {
	if q.Since != nil && e.Timestamp.Before(*q.Since) {
		return false
	}
	if q.Until != nil && e.Timestamp.After(*q.Until) {
		return false
	}
	if q.SinceClock > 0 && e.Clock <= q.SinceClock {
		return false
	}
	return true
}

func containsEventType(types []EventType, t EventType) bool {
	for _, et := range types {
		if et == t {
			return true
		}
	}
	return false
}

func containsScope(scopes []Scope, s Scope) bool {
	for _, sc := range scopes {
		if sc == s {
			return true
		}
	}
	return false
}

// EventStats contains statistics about the event log
type EventStats struct {
	TotalEvents     int               `json:"total_events"`
	MaxEvents       int               `json:"max_events"`
	EventsByType    map[EventType]int `json:"events_by_type"`
	EventsByAgent   map[string]int    `json:"events_by_agent"`
	EventsBySession map[string]int    `json:"events_by_session"`
	EventsByScope   map[Scope]int     `json:"events_by_scope"`
	LastClock       uint64            `json:"last_clock"`
	OldestTimestamp *time.Time        `json:"oldest_timestamp,omitempty"`
	NewestTimestamp *time.Time        `json:"newest_timestamp,omitempty"`
	AsyncEnabled    bool              `json:"async_enabled"`
}

// Stats returns statistics about the event log
func (el *EventLog) Stats() EventStats {
	stats := EventStats{
		MaxEvents:       el.maxEvents,
		LastClock:       el.lastClock.Load(),
		EventsByType:    make(map[EventType]int),
		EventsByAgent:   make(map[string]int),
		EventsBySession: make(map[string]int),
		EventsByScope:   make(map[Scope]int),
		AsyncEnabled:    el.appendChan != nil,
	}

	el.eventsMu.RLock()
	stats.TotalEvents = el.count
	if el.count > 0 {
		oldest := el.events[el.tail]
		if oldest != nil {
			stats.OldestTimestamp = &oldest.Timestamp
		}
		newestIdx := (el.head - 1 + el.maxEvents) % el.maxEvents
		newest := el.events[newestIdx]
		if newest != nil {
			stats.NewestTimestamp = &newest.Timestamp
		}
	}
	el.eventsMu.RUnlock()

	el.indexesMu.RLock()
	for eventType, events := range el.byType {
		stats.EventsByType[eventType] = len(events)
	}
	for agentID, events := range el.byAgent {
		stats.EventsByAgent[agentID] = len(events)
	}
	for sessionID, events := range el.bySession {
		stats.EventsBySession[sessionID] = len(events)
	}
	for scope, events := range el.byScope {
		stats.EventsByScope[scope] = len(events)
	}
	el.indexesMu.RUnlock()

	return stats
}

// ToJSON exports events to JSON
func (el *EventLog) ToJSON() ([]byte, error) {
	el.eventsMu.RLock()
	events := el.getAllEventsLocked()
	el.eventsMu.RUnlock()
	return json.Marshal(events)
}

// FromJSON imports events from JSON
func (el *EventLog) FromJSON(data []byte) error {
	var events []*Event
	if err := json.Unmarshal(data, &events); err != nil {
		return err
	}

	for _, event := range events {
		if err := el.Append(event); err != nil {
			return err
		}
	}
	return nil
}
