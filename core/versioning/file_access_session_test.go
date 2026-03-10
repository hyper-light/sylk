package versioning

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionRoutingFileAccess_UsesSessionGlobalOverlay(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := WithSessionID(context.Background(), "sess-1")
	target := filepath.Join(dir, "hello.txt")
	if err := svfs.GlobalVFS().Write(context.Background(), target, []byte("overlay")); err != nil {
		t.Fatalf("global write: %v", err)
	}

	router := NewSessionRoutingFileAccess(true, func(sessionID string) *SessionVFS {
		if sessionID == "sess-1" {
			return svfs
		}
		return nil
	}, NewDiskFileAccess(dir, true))

	content, err := router.ReadFile(ctx, target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "overlay" {
		t.Fatalf("content = %q, want overlay content", string(content))
	}
}

func TestSessionRoutingFileAccess_DeniesWritableFallbackWithoutSession(t *testing.T) {
	dir := t.TempDir()
	router := NewSessionRoutingFileAccess(false, func(string) *SessionVFS { return nil }, NewDiskFileAccess(dir, false))

	target := filepath.Join(dir, "hello.txt")
	err := router.WriteFile(context.Background(), target, []byte("disk-bypass"))
	if err != ErrNoActiveSessionVFS {
		t.Fatalf("WriteFile error = %v, want %v", err, ErrNoActiveSessionVFS)
	}

	exists, statErr := NewDiskFileAccess(dir, true).Exists(context.Background(), target)
	if statErr != nil {
		t.Fatalf("Exists: %v", statErr)
	}
	if exists {
		t.Fatal("expected writable fallback to be denied without touching disk")
	}
}
