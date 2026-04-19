package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

// ErrDurableProtocolDuplicate signals an Append call whose (kind, correlation_id)
// pair was already recorded by a previous Append in this log. Callers that
// observe this error should skip downstream projection / mailbox work — the
// state for that event is already applied. Idempotency protects against:
//
//   - Retries after a partial crash (the caller restarted before persistProjection
//     completed, replayed the WAL, and is now trying to re-apply the same event).
//   - At-least-once bus delivery causing the same protocol step to fire twice.
//   - Cross-turn recovery where the old turn's Append landed but the caller's
//     in-memory state says it didn't.
//
// The error wraps a nil event and the existing seq, so callers wanting the
// original sequence number can inspect durableProtocolLog.SeqForCorrelation.
var ErrDurableProtocolDuplicate = errors.New("durable protocol: duplicate correlation id")

type durableProtocolEvent struct {
	EventID             string          `json:"event_id"`
	Namespace           string          `json:"namespace"`
	ScopeID             string          `json:"scope_id"`
	Kind                string          `json:"kind"`
	AgentType           string          `json:"agent_type,omitempty"`
	CorrelationID       string          `json:"correlation_id,omitempty"`
	ParentCorrelationID string          `json:"parent_correlation_id,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	Payload             json.RawMessage `json:"payload,omitempty"`
}

type durableProtocolSnapshotFile struct {
	Seq       uint64          `json:"seq"`
	UpdatedAt time.Time       `json:"updated_at"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

type durableProtocolLog struct {
	namespace string
	scopeID   string
	dir       string
	journal   *agentlog.AgentJournal

	// seen tracks (kind, correlation_id) pairs already persisted. Populated
	// from the WAL at open time, extended on every successful Append.
	seen *dedupeSet
}

func openDurableProtocolLog(sessionDir, namespace, scopeID string) (*durableProtocolLog, error) {
	sessionDir = strings.TrimSpace(sessionDir)
	namespace = strings.TrimSpace(namespace)
	scopeID = strings.TrimSpace(scopeID)
	switch {
	case sessionDir == "":
		return nil, fmt.Errorf("durable protocol log requires session dir")
	case namespace == "":
		return nil, fmt.Errorf("durable protocol log requires namespace")
	case scopeID == "":
		return nil, fmt.Errorf("durable protocol log requires scope id")
	}
	dir := filepath.Join(sessionDir, "protocols", namespace, scopeID, "wal")
	journal, err := agentlog.OpenJournalDirect(dir, agentlog.JournalConfig{WALName: "events"})
	if err != nil {
		return nil, err
	}
	log := &durableProtocolLog{
		namespace: namespace,
		scopeID:   scopeID,
		dir:       dir,
		journal:   journal,
		seen:      newDedupeSet(),
	}
	if err := log.warmDedupeFromWAL(); err != nil {
		_ = journal.Close()
		return nil, fmt.Errorf("populate durable protocol dedupe set: %w", err)
	}
	return log, nil
}

// Append records a protocol event. If (kind, correlationID) was already
// appended in a prior call (or in a prior process, via WAL warm-up),
// returns ErrDurableProtocolDuplicate with the original sequence number —
// the caller treats this as a successful no-op.
func (l *durableProtocolLog) Append(kind, agentType, correlationID, parentCorrelationID string, payload any) (uint64, *durableProtocolEvent, error) {
	if l == nil || l.journal == nil {
		return 0, nil, fmt.Errorf("durable protocol log is not initialized")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return 0, nil, fmt.Errorf("durable protocol event kind is required")
	}
	key := dedupeKey(kind, correlationID)
	if existingSeq, dup := l.seen.lookup(key); dup {
		return existingSeq, nil, ErrDurableProtocolDuplicate
	}
	encoded, err := encodeProtocolPayload(payload)
	if err != nil {
		return 0, nil, err
	}
	event := l.newEvent(kind, agentType, correlationID, parentCorrelationID, encoded)
	seq, err := l.journal.AppendJSON(agentlog.EventProtocolEventAppended, event)
	if err != nil {
		return 0, nil, err
	}
	if seq == 0 {
		return 0, nil, fmt.Errorf("durable protocol append returned zero sequence")
	}
	// Record the dedupe key AFTER the WAL write succeeds so a failed Append
	// does not poison the in-memory set. If the process crashes between
	// AppendJSON and this line, warmDedupeFromWAL rebuilds the set from the
	// WAL on next open — the on-disk truth wins.
	l.seen.record(key, seq)
	return seq, event, nil
}

// Replay applies fn to every persisted event whose seq > afterSeq, in order.
// Duplicate (kind, correlationID) entries from pre-DUR-03 WALs are filtered
// so the caller's projection sees first-writer-wins semantics. Post-DUR-03
// logs contain no duplicates by construction, so this guard is free for new
// sessions.
func (l *durableProtocolLog) Replay(afterSeq uint64, fn func(uint64, *durableProtocolEvent) error) error {
	if l == nil || l.journal == nil {
		return fmt.Errorf("durable protocol log is not initialized")
	}
	if fn == nil {
		return nil
	}
	seen := newDedupeSet()
	return l.journal.ReplayAll(afterSeq, func(entry agentlog.Entry) error {
		event, ok, err := decodeProtocolEvent(entry)
		switch {
		case err != nil:
			return err
		case !ok:
			return nil
		case seen.observeFirst(event.Kind, event.CorrelationID, entry.Seq):
			return nil
		}
		return fn(entry.Seq, event)
	})
}

// SeqForCorrelation returns the sequence number of the first Append that
// recorded the given (kind, correlationID). Returns 0, false when no entry
// matches.
func (l *durableProtocolLog) SeqForCorrelation(kind, correlationID string) (uint64, bool) {
	if l == nil {
		return 0, false
	}
	return l.seen.lookup(dedupeKey(kind, correlationID))
}

// warmDedupeFromWAL rebuilds the dedupe set from the on-disk WAL at open
// time. First-writer-wins: duplicate entries in pre-DUR-03 logs are ignored
// after the first sighting. Corrupt entries are skipped — the dedupe set
// is best-effort and a bad record should not block startup.
func (l *durableProtocolLog) warmDedupeFromWAL() error {
	return l.journal.ReplayAll(0, func(entry agentlog.Entry) error {
		event, ok, err := decodeProtocolEvent(entry)
		if err != nil || !ok {
			return nil
		}
		l.seen.observeFirst(event.Kind, event.CorrelationID, entry.Seq)
		return nil
	})
}

func (l *durableProtocolLog) newEvent(kind, agentType, correlationID, parentCorrelationID string, payload json.RawMessage) *durableProtocolEvent {
	return &durableProtocolEvent{
		EventID:             uuid.NewString(),
		Namespace:           l.namespace,
		ScopeID:             l.scopeID,
		Kind:                kind,
		AgentType:           strings.TrimSpace(agentType),
		CorrelationID:       strings.TrimSpace(correlationID),
		ParentCorrelationID: strings.TrimSpace(parentCorrelationID),
		CreatedAt:           time.Now().UTC(),
		Payload:             payload,
	}
}

func (l *durableProtocolLog) LoadSnapshot(out any) (uint64, bool, error) {
	if l == nil {
		return 0, false, fmt.Errorf("durable protocol log is not initialized")
	}
	data, err := os.ReadFile(l.snapshotPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	var snapshot durableProtocolSnapshotFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return 0, false, err
	}
	if out != nil && len(snapshot.Snapshot) > 0 {
		if err := json.Unmarshal(snapshot.Snapshot, out); err != nil {
			return 0, false, err
		}
	}
	return snapshot.Seq, true, nil
}

func (l *durableProtocolLog) SaveSnapshot(seq uint64, snapshot any) error {
	if l == nil {
		return fmt.Errorf("durable protocol log is not initialized")
	}
	encoded, err := encodeSnapshot(seq, snapshot)
	if err != nil {
		return err
	}
	if err := writeSnapshotAtomically(l.snapshotPath(), encoded); err != nil {
		return err
	}
	_, err = l.journal.AppendJSON(agentlog.EventProtocolSnapshotSaved, &agentlog.ProtocolSnapshotPayload{
		Namespace: l.namespace,
		ScopeID:   l.scopeID,
		Seq:       seq,
	})
	return err
}

func (l *durableProtocolLog) Close() error {
	if l == nil || l.journal == nil {
		return nil
	}
	return l.journal.Close()
}

func (l *durableProtocolLog) snapshotPath() string {
	return filepath.Join(filepath.Dir(l.dir), "projection.snapshot.json")
}

// =============================================================================
// Helpers
// =============================================================================

// dedupeSet is the in-memory first-writer-wins set that backs Append's
// idempotency guarantee. Keys without a correlation id are rejected at
// insertion so empty keys can never collide.
type dedupeSet struct {
	mu   sync.Mutex
	keys map[string]uint64
}

func newDedupeSet() *dedupeSet {
	return &dedupeSet{keys: make(map[string]uint64)}
}

// lookup returns (seq, true) if key is present. An empty key always returns
// (0, false) — callers pass an empty key for events without a correlation id,
// and those events bypass dedupe entirely.
func (s *dedupeSet) lookup(key string) (uint64, bool) {
	if s == nil || key == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.keys[key]
	return seq, ok
}

// record stores (key, seq). A subsequent record on the same key overwrites
// the prior seq — by contract this only happens when callers violate the
// first-writer-wins protocol, so we keep the last value for observability
// without silently dropping it.
func (s *dedupeSet) record(key string, seq uint64) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = seq
}

// observeFirst is the replay-time entry point. Returns true iff the key was
// already seen (caller should skip); returns false and records the seq on
// first sighting.
func (s *dedupeSet) observeFirst(kind, correlationID string, seq uint64) bool {
	if s == nil {
		return false
	}
	key := dedupeKey(kind, correlationID)
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, already := s.keys[key]; already {
		return true
	}
	s.keys[key] = seq
	return false
}

// dedupeKey composes the in-memory dedupe key. Empty correlationIDs yield an
// empty key so they are ignored by lookup — events without correlation never
// match or collide.
func dedupeKey(kind, correlationID string) string {
	cid := strings.TrimSpace(correlationID)
	if cid == "" {
		return ""
	}
	return strings.TrimSpace(kind) + "\x1f" + cid
}

// encodeProtocolPayload marshals an arbitrary payload to JSON, returning nil
// for nil inputs so the caller can emit events without a body.
func encodeProtocolPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode durable protocol payload: %w", err)
	}
	return data, nil
}

// decodeProtocolEvent extracts a protocol event from a journal entry. Returns
// (nil, false, nil) for entries of a different event type and (nil, false, err)
// for malformed entries during live replay. warmDedupeFromWAL tolerates the
// error via a local no-op; Replay propagates it to the caller.
func decodeProtocolEvent(entry agentlog.Entry) (*durableProtocolEvent, bool, error) {
	if entry.EventType != agentlog.EventProtocolEventAppended {
		return nil, false, nil
	}
	var event durableProtocolEvent
	if err := json.Unmarshal(entry.Data, &event); err != nil {
		return nil, false, fmt.Errorf("decode durable protocol event seq %d: %w", entry.Seq, err)
	}
	return &event, true, nil
}

func encodeSnapshot(seq uint64, snapshot any) ([]byte, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	file := durableProtocolSnapshotFile{
		Seq:       seq,
		UpdatedAt: time.Now().UTC(),
		Snapshot:  data,
	}
	return json.MarshalIndent(file, "", "  ")
}

func writeSnapshotAtomically(path string, encoded []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
