package agentlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWALPath_UsesStateDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	path, err := ResolveWALPath("architect")
	if err != nil {
		t.Fatalf("ResolveWALPath() error: %v", err)
	}
	if !strings.Contains(path, filepath.Join("sylk", "wal", "agents", "architect")) {
		t.Fatalf("unexpected wal path: %s", path)
	}
}

func TestNewWALLogger_WritesJSONLineWithSequence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)

	logger, closer, err := NewWALLogger("architect")
	if err != nil {
		t.Fatalf("NewWALLogger() error: %v", err)
	}
	logger.Info("architect started", "phase", "startup")
	if closeErr := closer.Close(); closeErr != nil {
		t.Fatalf("close wal logger: %v", closeErr)
	}

	path, err := ResolveWALPath("architect")
	if err != nil {
		t.Fatalf("ResolveWALPath() error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wal file: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "\"msg\":\"architect started\"") {
		t.Fatalf("expected message in wal entry, got %q", text)
	}
	if !strings.Contains(text, "\"wal_seq\":1") {
		t.Fatalf("expected wal_seq in wal entry, got %q", text)
	}
}
