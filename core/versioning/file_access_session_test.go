package versioning

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionRoutingFileAccess_UsesSessionGlobalOverlay(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
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
