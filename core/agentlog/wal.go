package agentlog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/logging"
	"github.com/adalundhe/sylk/core/storage"
)

const (
	defaultAgentLogFile = "events.wal"
)

// NewWALLogger creates a per-agent append-only logger backed by a file.
// The returned closer must be closed by the caller to flush and release fd.
func NewWALLogger(agentID string) (*slog.Logger, io.Closer, error) {
	path, err := ResolveWALPath(agentID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureWALPath(path); err != nil {
		fallbackPath, fallbackErr := resolveLocalWALPath(agentID)
		if fallbackErr != nil {
			return nil, nil, fmt.Errorf("create agent wal directory: %w", err)
		}
		path = fallbackPath
		if err := ensureWALPath(path); err != nil {
			return nil, nil, fmt.Errorf("create fallback agent wal directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fallbackPath, fallbackErr := resolveLocalWALPath(agentID)
		if fallbackErr != nil {
			return nil, nil, fmt.Errorf("open agent wal: %w", err)
		}
		path = fallbackPath
		if ensureErr := ensureWALPath(path); ensureErr != nil {
			return nil, nil, fmt.Errorf("open fallback agent wal: %w", err)
		}
		file, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, nil, fmt.Errorf("open fallback agent wal: %w", err)
		}
	}
	handler := slog.NewJSONHandler(file, logging.HandlerOptions(slog.LevelInfo))
	logger := slog.New(newSequenceHandler(handler)).With("agent_id", normalizeAgentID(agentID))
	return logger, file, nil
}

// ResolveWALPath resolves the on-disk WAL path for a specific agent.
func ResolveWALPath(agentID string) (string, error) {
	safeAgentID := normalizeAgentID(agentID)
	dirs, err := storage.ResolveDirs()
	if err == nil && dirs != nil {
		if ensureErr := dirs.EnsureAll(); ensureErr == nil {
			return filepath.Join(dirs.StateDir("wal", "agents", safeAgentID), defaultAgentLogFile), nil
		}
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		if err != nil {
			return "", fmt.Errorf("resolve wal path: %w", err)
		}
		return "", fmt.Errorf("resolve wal path: %w", cwdErr)
	}
	return filepath.Join(cwd, ".sylk", "local", "wal", "agents", safeAgentID, defaultAgentLogFile), nil
}

func resolveLocalWALPath(agentID string) (string, error) {
	safeAgentID := normalizeAgentID(agentID)
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".sylk", "local", "wal", "agents", safeAgentID, defaultAgentLogFile), nil
}

func ensureWALPath(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0700)
}

func normalizeAgentID(agentID string) string {
	trimmed := strings.ToLower(strings.TrimSpace(agentID))
	if trimmed == "" {
		return "agent"
	}
	return trimmed
}

type sequenceHandler struct {
	next *atomic.Uint64
	base slog.Handler
}

func newSequenceHandler(base slog.Handler) slog.Handler {
	return &sequenceHandler{
		next: &atomic.Uint64{},
		base: base,
	}
}

func (h *sequenceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *sequenceHandler) Handle(ctx context.Context, record slog.Record) error {
	withSeq := copyRecord(record)
	withSeq.AddAttrs(slog.Uint64("wal_seq", h.next.Add(1)))
	return h.base.Handle(ctx, withSeq)
}

func (h *sequenceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sequenceHandler{
		next: h.next,
		base: h.base.WithAttrs(attrs),
	}
}

func (h *sequenceHandler) WithGroup(name string) slog.Handler {
	return &sequenceHandler{
		next: h.next,
		base: h.base.WithGroup(name),
	}
}

func copyRecord(src slog.Record) slog.Record {
	dst := slog.NewRecord(src.Time, src.Level, src.Message, src.PC)
	src.Attrs(func(attr slog.Attr) bool {
		dst.AddAttrs(attr)
		return true
	})
	return dst
}

// ResolveSessionWALDir returns the per-agent WAL directory within a session.
// Layout: <sessionDir>/agents/<agentName>/wal/
func ResolveSessionWALDir(sessionDir, agentName string) string {
	return filepath.Join(sessionDir, "agents", normalizeAgentID(agentName), "wal")
}
