package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

// ErrDurableProtocolDuplicate signals an Append call whose semantic
// fingerprint matches an event already recorded by a previous Append in this
// log. Callers that observe this error should skip downstream projection /
// mailbox work — the state for that event is already applied. Idempotency
// protects against:
//
//   - Retries after a partial crash (the caller restarted before
//     persistProjection completed, replayed the WAL, and is now trying to
//     re-apply the same event).
//   - At-least-once bus delivery causing the same protocol step to fire twice.
//   - Cross-turn recovery where the old turn's Append landed but the caller's
//     in-memory state says it didn't.
//
// The error wraps the existing seq; callers wanting to investigate can use
// durableProtocolLog.SeqForFingerprint to retrieve it.
var ErrDurableProtocolDuplicate = errors.New("durable protocol: duplicate event fingerprint")

type durableProtocolEvent struct {
	EventID             string          `json:"event_id"`
	Namespace           string          `json:"namespace"`
	ScopeID             string          `json:"scope_id"`
	Kind                string          `json:"kind"`
	AgentType           string          `json:"agent_type,omitempty"`
	CorrelationID       string          `json:"correlation_id,omitempty"`
	ParentCorrelationID string          `json:"parent_correlation_id,omitempty"`
	IdempotencyKey      string          `json:"idempotency_key,omitempty"`
	PayloadFingerprint  string          `json:"payload_fingerprint,omitempty"`
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

	// seen tracks the semantic fingerprints already persisted. Populated
	// from the WAL at open time, extended on every successful Append.
	// The fingerprint is the caller-supplied idempotency_key when set,
	// otherwise a hash composed of (kind, correlation_id, canonical
	// payload).
	seen *dedupeSet
}

// AppendRequest carries the full write intent for a single durable-protocol
// event. Using a struct (rather than a growing list of positional parameters)
// keeps the three-layer defense visible at each call site: callers can
// explicitly set IdempotencyKey when they know something the payload-hash
// doesn't, or leave it empty to let the log compute the semantic fingerprint.
type AppendRequest struct {
	Kind                string
	AgentType           string
	CorrelationID       string
	ParentCorrelationID string
	// IdempotencyKey, when set, is used verbatim as the dedupe
	// discriminator. Leave empty to let the log compute a semantic
	// fingerprint from (kind, correlation_id, canonical_payload). Use
	// an explicit key for distinctness the payload hash can't see
	// (e.g. two logically-different events that happen to carry equal
	// payloads). When set, callers are responsible for choosing a key
	// that is stable across retries of the same logical intent and
	// distinct across different logical intents.
	IdempotencyKey string
	Payload        any
}

// AppendResult describes the outcome of a successful Append call.
type AppendResult struct {
	Seq                uint64
	Event              *durableProtocolEvent
	PayloadFingerprint string
	DedupeKey          string
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

// Append records a protocol event. The dedupe discriminator is a three-layer
// composite designed to capture "same logical intent retried" while never
// silently dropping "semantically distinct event that happens to share a
// correlation_id":
//
//   1. Every persisted row carries a fresh EventID (UUID) — the physical
//      primary key. No two rows can ever share one.
//   2. The caller may supply an IdempotencyKey when it knows logical
//      distinctness the payload hash can't see. That key, when non-empty,
//      is used verbatim as the dedupe discriminator.
//   3. When IdempotencyKey is empty, the log computes a semantic
//      fingerprint: sha256(kind || "\x1f" || correlation_id || "\x1f" ||
//      canonical_payload). Two writes under the same (kind, correlation_id)
//      with different payloads produce different fingerprints and BOTH
//      persist. Two writes with identical (kind, correlation_id, payload)
//      produce the same fingerprint and the second is deduped.
//
// When a duplicate is detected, ErrDurableProtocolDuplicate is returned with
// the prior seq. A structured log entry (pipeline_protocol.dedupe_hit) is
// emitted so observability distinguishes legitimate crash-recovery
// idempotency (expected) from silent-drop bugs (unexpected).
func (l *durableProtocolLog) Append(req AppendRequest) (*AppendResult, error) {
	if l == nil || l.journal == nil {
		return nil, fmt.Errorf("durable protocol log is not initialized")
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		return nil, fmt.Errorf("durable protocol event kind is required")
	}

	encoded, err := encodeProtocolPayload(req.Payload)
	if err != nil {
		return nil, err
	}
	payloadFingerprint := computePayloadFingerprint(encoded)
	dedupeKey := composeDedupeKey(kind, req.CorrelationID, req.IdempotencyKey, payloadFingerprint)

	if existingSeq, dup := l.seen.lookup(dedupeKey); dup {
		l.logDedupeHit(kind, req, existingSeq, dedupeKey)
		return &AppendResult{
			Seq:                existingSeq,
			PayloadFingerprint: payloadFingerprint,
			DedupeKey:          dedupeKey,
		}, ErrDurableProtocolDuplicate
	}

	event := l.newEvent(kind, req.AgentType, req.CorrelationID, req.ParentCorrelationID, req.IdempotencyKey, payloadFingerprint, encoded)
	seq, err := l.journal.AppendJSON(agentlog.EventProtocolEventAppended, event)
	if err != nil {
		return nil, err
	}
	if seq == 0 {
		return nil, fmt.Errorf("durable protocol append returned zero sequence")
	}
	// Record AFTER the WAL write succeeds so a failed Append does not
	// poison the in-memory set. If the process crashes between
	// AppendJSON and this line, warmDedupeFromWAL rebuilds the set from
	// the WAL on next open — the on-disk truth wins.
	l.seen.record(dedupeKey, seq)
	return &AppendResult{
		Seq:                seq,
		Event:              event,
		PayloadFingerprint: payloadFingerprint,
		DedupeKey:          dedupeKey,
	}, nil
}

// logDedupeHit emits a structured log record every time dedupe fires. This
// is the observability signal that distinguishes legitimate idempotency (a
// crash-recovery replay re-applying the same event) from caller bugs (two
// distinct intents sharing a correlation_id and identical payload, which
// would be wrong if it happened in steady-state operation).
func (l *durableProtocolLog) logDedupeHit(kind string, req AppendRequest, priorSeq uint64, key string) {
	slog.Info("pipeline_protocol.dedupe_hit",
		"namespace", l.namespace,
		"scope_id", l.scopeID,
		"kind", kind,
		"correlation_id", strings.TrimSpace(req.CorrelationID),
		"agent_type", strings.TrimSpace(req.AgentType),
		"idempotency_key", strings.TrimSpace(req.IdempotencyKey),
		"prior_seq", priorSeq,
		"dedupe_key", key,
	)
}

// Replay applies fn to every persisted event whose seq > afterSeq, in order.
// Duplicate fingerprints from pre-fix WALs are filtered so the caller's
// projection sees first-writer-wins semantics. Post-fix logs contain no
// fingerprint collisions by construction, so this guard is free for new
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
		case seen.observeFirstEvent(event, entry.Seq):
			return nil
		}
		return fn(entry.Seq, event)
	})
}

// SeqForFingerprint returns the sequence number of the first Append that
// recorded the given dedupe fingerprint. Returns 0, false when no entry
// matches. Used by tests + observability to confirm a specific logical
// event is (or is not) persisted.
func (l *durableProtocolLog) SeqForFingerprint(dedupeKey string) (uint64, bool) {
	if l == nil {
		return 0, false
	}
	return l.seen.lookup(dedupeKey)
}

// warmDedupeFromWAL rebuilds the dedupe set from the on-disk WAL at open
// time. First-writer-wins: duplicate entries in pre-fix logs are ignored
// after the first sighting. Corrupt entries are skipped — the dedupe set
// is best-effort and a bad record should not block startup.
func (l *durableProtocolLog) warmDedupeFromWAL() error {
	return l.journal.ReplayAll(0, func(entry agentlog.Entry) error {
		event, ok, err := decodeProtocolEvent(entry)
		if err != nil || !ok {
			return nil
		}
		l.seen.observeFirstEvent(event, entry.Seq)
		return nil
	})
}

func (l *durableProtocolLog) newEvent(
	kind, agentType, correlationID, parentCorrelationID, idempotencyKey, payloadFingerprint string,
	payload json.RawMessage,
) *durableProtocolEvent {
	return &durableProtocolEvent{
		EventID:             uuid.NewString(),
		Namespace:           l.namespace,
		ScopeID:             l.scopeID,
		Kind:                kind,
		AgentType:           strings.TrimSpace(agentType),
		CorrelationID:       strings.TrimSpace(correlationID),
		ParentCorrelationID: strings.TrimSpace(parentCorrelationID),
		IdempotencyKey:      strings.TrimSpace(idempotencyKey),
		PayloadFingerprint:  payloadFingerprint,
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
// idempotency guarantee. Keys are the fingerprint strings composed by
// composeDedupeKey; empty keys are never stored.
type dedupeSet struct {
	mu   sync.Mutex
	keys map[string]uint64
}

func newDedupeSet() *dedupeSet {
	return &dedupeSet{keys: make(map[string]uint64)}
}

// lookup returns (seq, true) if key is present. An empty key always returns
// (0, false) — a caller passing an empty key is asking for no dedupe.
func (s *dedupeSet) lookup(key string) (uint64, bool) {
	if s == nil || key == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.keys[key]
	return seq, ok
}

// record stores (key, seq). Subsequent record calls on the same key keep the
// FIRST seq (first-writer-wins). Observers that need a later seq should treat
// this as a protocol violation and log.
func (s *dedupeSet) record(key string, seq uint64) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, already := s.keys[key]; already {
		return
	}
	s.keys[key] = seq
}

// observeFirstEvent is the replay-time entry point. Returns true iff the
// fingerprint was already seen (caller should skip). First-writer-wins.
//
// For events written before the fingerprint fix lands, PayloadFingerprint
// and IdempotencyKey are empty; we fall back to a legacy key constructed
// from (kind, correlation_id) alone. This preserves the pre-fix dedupe
// behavior for old logs while the post-fix code path uses the stronger
// discriminator for new writes.
func (s *dedupeSet) observeFirstEvent(event *durableProtocolEvent, seq uint64) bool {
	if s == nil || event == nil {
		return false
	}
	key := composeDedupeKey(event.Kind, event.CorrelationID, event.IdempotencyKey, event.PayloadFingerprint)
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

// composeDedupeKey assembles the dedupe key from the three-layer inputs.
// Priority: caller-supplied IdempotencyKey wins when set (caller knows more
// than the hash can). Otherwise the key is (kind, correlation_id,
// payload_fingerprint) — and the load-bearing invariant is that the
// fingerprint participates in the key so two distinct payloads under the
// same correlation_id ALWAYS produce different keys. The pre-Fix-A bug
// was that the dedupe key omitted the payload entirely; that is what
// this function must never regress to. Empty-payload retries under the
// same correlation_id legitimately dedupe (two nil-payload calls are
// logically equivalent), so (kind, correlation_id, "") is still a
// meaningful key. Only when the correlation_id and the fingerprint are
// BOTH empty does the key degenerate to "" (no dedupe — the caller had
// no discriminator at all).
func composeDedupeKey(kind, correlationID, idempotencyKey, payloadFingerprint string) string {
	key := strings.TrimSpace(idempotencyKey)
	if key != "" {
		return strings.TrimSpace(kind) + "\x1f:idemp:" + key
	}
	cid := strings.TrimSpace(correlationID)
	fp := strings.TrimSpace(payloadFingerprint)
	if cid == "" && fp == "" {
		return ""
	}
	return strings.TrimSpace(kind) + "\x1f" + cid + "\x1f" + fp
}

// computePayloadFingerprint returns a hex-encoded sha256 of the canonical
// JSON encoding of the payload. Empty payloads produce the empty string so
// composeDedupeKey can degenerate correctly when the caller has no
// distinguishing information.
func computePayloadFingerprint(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
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
	// Pre-fix records have no IdempotencyKey / PayloadFingerprint.
	// Backfill the fingerprint from the stored payload so the dedupe
	// set's composed key matches what a post-fix Append would produce
	// for the same event. This keeps pre-fix and post-fix logs
	// comparable and prevents accidental re-write of an already-
	// persisted logical event during mixed-version replay.
	if event.PayloadFingerprint == "" && event.IdempotencyKey == "" {
		event.PayloadFingerprint = computePayloadFingerprint(event.Payload)
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
