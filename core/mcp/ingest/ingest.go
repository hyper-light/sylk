// Package ingest implements the runtime.ContentIngester and
// runtime.OutcomeListener interfaces that route MCP-derived content into
// Sylk's knowledge and forest planes.
//
// Two backends are provided here:
//
//   - FileIngester writes resources, prompts, and tool result text into
//     a session-scoped content directory along with a JSON sidecar
//     describing provenance. Sylk's knowledge boot path indexes the
//     directory using its existing extraction pipeline — no separate
//     ingestion plumbing required.
//
//   - ForestRecorder wraps a callback that records outcome signals (server
//     reliability, schema mismatches, repeated approval requests) into
//     the Memory Forest's existing outcome ingestion path. The forest
//     package owns the actual record format; we adapt the runtime's
//     event vocabulary into a struct the forest understands.
//
// All writes are async — the ingester runs inside the runtime's tracked
// goroutine scope. Errors are logged but never propagate back to the
// MCP hot path.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/adalundhe/sylk/core/mcp/durable"
	"github.com/adalundhe/sylk/core/mcp/runtime"
)

// FileIngesterConfig configures a FileIngester.
type FileIngesterConfig struct {
	// SessionDir is the session root; ingested content lives under
	// <session>/mcp/content/<sha>.{md,json}.
	SessionDir string
	// Logger is called with non-nil errors (test-friendly).
	Logger func(format string, args ...any)
}

// FileIngester is a runtime.ContentIngester that persists content to disk.
type FileIngester struct {
	dir    string
	logger func(format string, args ...any)
	mu     sync.Mutex
}

// NewFileIngester opens the per-session content directory.
func NewFileIngester(cfg FileIngesterConfig) (*FileIngester, error) {
	if cfg.SessionDir == "" {
		return nil, errors.New("mcp ingest: session dir is required")
	}
	dir := filepath.Join(cfg.SessionDir, "mcp", "content")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mcp ingest: ensure dir: %w", err)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &FileIngester{dir: dir, logger: logger}, nil
}

// Ingest implements runtime.ContentIngester. Each payload becomes one .md
// (the text body) and one .json (provenance) keyed by sha256 of the body.
// Idempotent: identical content is written once.
func (f *FileIngester) Ingest(_ context.Context, payload runtime.IngestPayload) {
	if f == nil {
		return
	}
	if payload.Text == "" {
		return
	}
	digest := f.write(payload)
	if digest == "" {
		return
	}
}

func (f *FileIngester) write(payload runtime.IngestPayload) string {
	sum := sha256.Sum256([]byte(payload.Text))
	digest := hex.EncodeToString(sum[:])
	textPath := filepath.Join(f.dir, digest+".md")
	jsonPath := filepath.Join(f.dir, digest+".json")
	if _, err := os.Stat(textPath); err == nil {
		return digest
	}
	if err := f.writeText(textPath, payload.Text); err != nil {
		f.logger("mcp ingest: write text %s: %v", digest, err)
		return ""
	}
	if err := f.writeProvenance(jsonPath, digest, payload); err != nil {
		f.logger("mcp ingest: write provenance %s: %v", digest, err)
		return ""
	}
	return digest
}

func (f *FileIngester) writeText(path, body string) error {
	tmp, err := os.CreateTemp(f.dir, "mcp-content-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (f *FileIngester) writeProvenance(path, digest string, payload runtime.IngestPayload) error {
	body := struct {
		Digest         string         `json:"digest"`
		Source         string         `json:"source"`
		ServerID       string         `json:"server_id,omitempty"`
		OperationID    string         `json:"operation_id,omitempty"`
		SessionID      string         `json:"session_id,omitempty"`
		CapabilityKind string         `json:"capability_kind,omitempty"`
		MIMEType       string         `json:"mime_type,omitempty"`
		Title          string         `json:"title,omitempty"`
		Metadata       map[string]any `json:"metadata,omitempty"`
	}{
		Digest:         digest,
		Source:         payload.Source,
		ServerID:       payload.ServerID,
		OperationID:    payload.OperationID,
		SessionID:      payload.SessionID,
		CapabilityKind: payload.CapabilityKind,
		MIMEType:       payload.MIMEType,
		Title:          payload.Title,
		Metadata:       payload.Metadata,
	}
	tmp, err := os.CreateTemp(f.dir, "mcp-provenance-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// OutcomeRecorder is the abstract sink an outcome listener writes to.
// The forest package implements one in production; tests inject fakes.
type OutcomeRecorder interface {
	Record(outcome Outcome)
}

// Outcome is the aggregated form of one MCP operation suitable for forest
// learning: server, tool, status, latency, error class. The forest's family
// canonicalization picks up these fields and folds them into reliability
// signals.
type Outcome struct {
	ServerID       string
	CapabilityKind string
	LocalName      string
	RemoteName     string
	Status         string
	ErrorClass     string
	DurationMs     int64
	AgentID        string
	SessionID      string
	CorrelationID  string
}

// ForestRecorder is a runtime.OutcomeListener that translates each finalized
// MCP operation into a forest Outcome and forwards it.
type ForestRecorder struct {
	sink OutcomeRecorder
}

// NewForestRecorder constructs a ForestRecorder.
func NewForestRecorder(sink OutcomeRecorder) *ForestRecorder {
	return &ForestRecorder{sink: sink}
}

// OnOperation implements runtime.OutcomeListener.
func (r *ForestRecorder) OnOperation(op durable.Operation, finalEvent durable.Event) {
	if r == nil || r.sink == nil {
		return
	}
	r.sink.Record(buildOutcome(op, finalEvent))
}

func buildOutcome(op durable.Operation, finalEvent durable.Event) Outcome {
	out := Outcome{
		ServerID:       op.ServerID,
		CapabilityKind: op.CapabilityKind,
		LocalName:      op.LocalName,
		RemoteName:     op.RemoteName,
		Status:         string(op.Status),
		AgentID:        op.AgentID,
		SessionID:      op.SessionID,
		CorrelationID:  op.CorrelationID,
	}
	if op.CompletedAt != nil {
		out.DurationMs = op.CompletedAt.Sub(op.StartedAt).Milliseconds()
	}
	out.ErrorClass = classifyError(finalEvent.ErrorMessage, op.Error)
	return out
}

func classifyError(eventErr, projectionErr string) string {
	src := eventErr
	if src == "" {
		src = projectionErr
	}
	if src == "" {
		return ""
	}
	return classifyMessage(src)
}

func classifyMessage(msg string) string {
	switch {
	case containsAny(msg, "denied", "forbidden", "unauthorized", "guardian"):
		return "auth"
	case containsAny(msg, "schema", "invalid", "validation"):
		return "schema"
	case containsAny(msg, "timeout", "deadline"):
		return "timeout"
	case containsAny(msg, "connection", "transport", "EOF"):
		return "transport"
	}
	return "other"
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if indexOf(haystack, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(haystack, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	if len(haystack) < len(needle) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a := haystack[i+j]
			b := needle[j]
			if a >= 'A' && a <= 'Z' {
				a = a - 'A' + 'a'
			}
			if b >= 'A' && b <= 'Z' {
				b = b - 'A' + 'a'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
