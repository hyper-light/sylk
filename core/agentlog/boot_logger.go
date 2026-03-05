// Package agentlog provides the BootEventLogger — a three-tier structured
// logger for the boot and ingestion pipelines.
//
// Lifecycle:
//
//	NewBootEventLogger → Staging state (events written to .sylk/boot/staging.jsonl)
//	BindSession        → Bound state  (orphans + staged events drained to session log)
//	Close              → Closed state  (flush + release; staging retains if never bound)
//
// All methods are nil-safe: calling any method on a nil *BootEventLogger is a no-op.
package agentlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// bootLoggerState represents the current lifecycle phase.
type bootLoggerState uint8

const (
	bootStateUninitialized bootLoggerState = iota
	bootStateStaging
	bootStateBound
	bootStateClosed
)

// bootRingSize is the ring buffer capacity for zero-I/O diagnostics.
const bootRingSize = 256

// BootEventLogger is a three-tier structured logger for boot/ingestion events.
//
// Tier 1 (Staging): Events are written immediately to .sylk/boot/staging.jsonl.
// Tier 2 (Drain):   On BindSession, orphaned + staged entries are drained to the session log.
// Tier 3 (Session): All subsequent events go directly to .sylk/sessions/{sid}/agents/boot/logs/.
type BootEventLogger struct {
	mu       sync.Mutex
	state    bootLoggerState
	sylkRoot string

	staging  *JSONLWriter // .sylk/boot/staging.jsonl
	session  *JSONLWriter // .sylk/sessions/{sid}/agents/boot/logs/
	orphans  []JSONLEntry // recovered from prior staging file
	stageBuf []JSONLEntry // current-boot staged entries (for drain replay)

	// Ring buffer: lock-free via atomic position.
	ring    [bootRingSize]JSONLEntry
	ringPos atomic.Uint32
	ringLen atomic.Uint32

	// Running stats: updated via atomics.
	total     atomic.Int64
	errCount  atomic.Int64
	warnCount atomic.Int64
	lastErr   atomic.Int64
}

// NewBootEventLogger creates a BootEventLogger in Staging state.
// Opens .sylk/boot/staging.jsonl and recovers orphaned entries from any prior
// staging file (crashed or skipped boots).
func NewBootEventLogger(sylkRoot string) (*BootEventLogger, error) {
	bootDir := filepath.Join(sylkRoot, "boot")
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		return nil, err
	}

	b := &BootEventLogger{
		sylkRoot: sylkRoot,
	}

	// Recover orphaned entries from prior staging file before opening fresh writer.
	b.orphans = recoverOrphans(filepath.Join(bootDir, "staging.jsonl"))

	staging, err := NewJSONLWriter(bootDir, "staging", noRotation(), defaultSyncInterval)
	if err != nil {
		return nil, err
	}

	b.staging = staging
	b.state = bootStateStaging
	return b, nil
}

// BindSession drains orphaned and staged entries into the session-scoped
// log directory, then switches all future writes to the session log.
// The staging file is truncated after drain.
//
// sessionDir is the session root (e.g., .sylk/sessions/ses_003).
// sessionID is the string session ID (e.g., "ses_003").
func (b *BootEventLogger) BindSession(sessionDir, sessionID string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != bootStateStaging {
		return nil // idempotent: already bound or closed
	}

	logsDir := filepath.Join(sessionDir, "agents", "boot", "logs")
	sessWriter, err := NewJSONLWriter(logsDir, "activity", DefaultRotationPolicy(), defaultSyncInterval)
	if err != nil {
		return err
	}

	// Drain orphans (prior crashed/skipped boots) with recovery marker.
	for i := range b.orphans {
		entry := b.orphans[i]
		if entry.Data == nil {
			entry.Data = map[string]any{"recovered": true}
		}
		_ = sessWriter.Write(entry)
	}
	b.orphans = nil

	// Drain current-boot staged entries.
	for i := range b.stageBuf {
		_ = sessWriter.Write(b.stageBuf[i])
	}
	b.stageBuf = nil

	// Close and truncate the staging writer.
	b.staging.Close()
	truncateStaging(filepath.Join(b.sylkRoot, "boot", "staging.jsonl"))
	b.staging = nil

	b.session = sessWriter
	b.state = bootStateBound
	return nil
}

// LogEvent writes a structured entry to the active writer and updates the
// ring buffer + atomic stats. Always records to ring/stats even if the
// JSONL write fails (zero-I/O diagnostics always available).
func (b *BootEventLogger) LogEvent(entry JSONLEntry) {
	if b == nil {
		return
	}

	// Ring buffer write (lock-free via atomic position).
	pos := b.ringPos.Add(1) - 1
	b.ring[pos%bootRingSize] = entry
	if cur := b.ringLen.Load(); cur < bootRingSize {
		b.ringLen.CompareAndSwap(cur, min(cur+1, bootRingSize))
	}

	// Running stats.
	b.total.Add(1)
	switch entry.Level {
	case "error":
		b.errCount.Add(1)
		b.lastErr.Store(entry.Timestamp.UnixNano())
	case "warn":
		b.warnCount.Add(1)
	}

	// JSONL write to active writer.
	b.mu.Lock()
	switch b.state {
	case bootStateStaging:
		if b.staging != nil {
			_ = b.staging.Write(entry)
		}
		b.stageBuf = append(b.stageBuf, entry)
	case bootStateBound:
		if b.session != nil {
			_ = b.session.Write(entry)
		}
	}
	b.mu.Unlock()
}

// RecentEntries returns the last n entries from the ring buffer (newest first).
// Zero I/O. Nil-safe.
func (b *BootEventLogger) RecentEntries(n int) []JSONLEntry {
	if b == nil {
		return nil
	}
	length := int(b.ringLen.Load())
	if length == 0 {
		return nil
	}
	if n > length {
		n = length
	}
	pos := b.ringPos.Load()
	result := make([]JSONLEntry, n)
	for i := range n {
		idx := (pos - 1 - uint32(i)) % bootRingSize
		result[i] = b.ring[idx]
	}
	return result
}

// Stats returns an instant health pulse from running atomic counters.
// Zero I/O. Nil-safe.
func (b *BootEventLogger) Stats() LoggerStats {
	if b == nil {
		return LoggerStats{}
	}
	stats := LoggerStats{
		TotalCount: b.total.Load(),
		ErrorCount: b.errCount.Load(),
		WarnCount:  b.warnCount.Load(),
	}
	if nano := b.lastErr.Load(); nano > 0 {
		stats.LastError = time.Unix(0, nano)
	}
	return stats
}

// Close flushes and closes the active writer. If never bound (skipped boot),
// the staging file retains events for the next boot's drain. Idempotent. Nil-safe.
func (b *BootEventLogger) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == bootStateClosed {
		return nil
	}

	if b.staging != nil {
		b.staging.Close()
		b.staging = nil
	}
	if b.session != nil {
		b.session.Close()
		b.session = nil
	}
	b.state = bootStateClosed
	return nil
}

// noRotation returns a rotation policy that never rotates (boot is short-lived).
func noRotation() RotationPolicy {
	return RotationPolicy{Interval: 24 * time.Hour}
}

// recoverOrphans reads all valid JSONLEntry records from an existing staging file.
// Returns nil if the file doesn't exist, is empty, or contains no valid entries.
func recoverOrphans(path string) []JSONLEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []JSONLEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 8*1024), 128*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry JSONLEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// truncateStaging empties the staging file. Best-effort; errors are ignored
// since the file will be re-drained on next boot if truncation fails.
func truncateStaging(path string) {
	_ = os.Truncate(path, 0)
}
