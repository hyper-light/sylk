package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Journal is the per-session append-only event log for MCP operations.
//
// One Journal serves all operations within a single session. Each Append
// fsyncs the file before returning so a crash-after-append is impossible.
// The reducer reads back from the same file to rebuild Operation projections
// on recovery.
type Journal struct {
	dir     string
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
	seq     atomic.Uint64

	closed atomic.Bool
}

// JournalConfig configures a Journal.
type JournalConfig struct {
	// SessionDir is the session-scoped directory under which the
	// MCP subtree (mcp/ops, mcp/blobs, mcp/servers) is created.
	SessionDir string
}

// Open opens (or creates) the per-session journal file. Subsequent appends
// are fsynced. Recovery is via Replay() on the same Journal.
func Open(cfg JournalConfig) (*Journal, error) {
	if cfg.SessionDir == "" {
		return nil, errors.New("mcp durable: session dir is required")
	}
	mcpDir := filepath.Join(cfg.SessionDir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o700); err != nil {
		return nil, fmt.Errorf("mcp durable: ensure dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(mcpDir, "ops"), 0o700); err != nil {
		return nil, fmt.Errorf("mcp durable: ensure ops dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(mcpDir, "blobs"), 0o700); err != nil {
		return nil, fmt.Errorf("mcp durable: ensure blobs dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(mcpDir, "servers"), 0o700); err != nil {
		return nil, fmt.Errorf("mcp durable: ensure servers dir: %w", err)
	}
	walPath := filepath.Join(mcpDir, "events.wal")
	file, err := os.OpenFile(walPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mcp durable: open wal: %w", err)
	}
	j := &Journal{dir: mcpDir, file: file, encoder: json.NewEncoder(file)}
	j.encoder.SetEscapeHTML(false)
	if err := j.recoverSequence(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return j, nil
}

// recoverSequence scans the existing WAL and seeds the next sequence number.
// O(N) over event count but the cost only lands on session start; the WAL
// is bounded by session lifetime, not application lifetime.
func (j *Journal) recoverSequence() error {
	if _, err := j.file.Seek(0, 0); err != nil {
		return fmt.Errorf("mcp durable: seek for recover: %w", err)
	}
	defer j.file.Seek(0, 2)
	dec := json.NewDecoder(j.file)
	var maxSeq uint64
	for dec.More() {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			return fmt.Errorf("mcp durable: replay parse: %w", err)
		}
		if ev.Sequence > maxSeq {
			maxSeq = ev.Sequence
		}
	}
	j.seq.Store(maxSeq)
	return nil
}

// Close flushes and closes the journal.
func (j *Journal) Close() error {
	if !j.closed.CompareAndSwap(false, true) {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Sync()
	if cerr := j.file.Close(); err == nil {
		err = cerr
	}
	j.file = nil
	return err
}

// Append serializes and durably writes one event. Returns once the data has
// hit the disk (post-fsync). Caller-side ordering is enforced: the call MUST
// complete before any live transport write that depends on the event.
func (j *Journal) Append(ev Event) error {
	if j.closed.Load() {
		return errors.New("mcp durable: journal closed")
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	ev.Sequence = j.seq.Add(1)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return errors.New("mcp durable: journal closed")
	}
	if err := j.encoder.Encode(ev); err != nil {
		return fmt.Errorf("mcp durable: encode event: %w", err)
	}
	return j.file.Sync()
}

// AppendCtx is a context-respecting wrapper around Append. Useful when the
// caller wants to cancel a long fsync — though in practice fsync cannot be
// interrupted; we honour ctx by short-circuiting before the syscall.
func (j *Journal) AppendCtx(ctx context.Context, ev Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return j.Append(ev)
}

// Replay invokes fn on every event currently in the journal, in order. Used
// during session boot to rebuild projections.
func (j *Journal) Replay(fn func(Event) error) error {
	if j.closed.Load() {
		return errors.New("mcp durable: journal closed")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return errors.New("mcp durable: journal closed")
	}
	if _, err := j.file.Seek(0, 0); err != nil {
		return fmt.Errorf("mcp durable: seek for replay: %w", err)
	}
	defer j.file.Seek(0, 2)
	dec := json.NewDecoder(j.file)
	for dec.More() {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			return fmt.Errorf("mcp durable: replay decode: %w", err)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return nil
}

// Dir returns the absolute path of the mcp subtree for callers that need
// to write blobs or projections beside the WAL.
func (j *Journal) Dir() string { return j.dir }
