package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestDAGBridgeCompositeGate_DoesNotFlushGlobalDraftToDisk(t *testing.T) {
	dir := t.TempDir()
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:       "sess-1",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	target := filepath.Join(dir, "draft.txt")
	if err := svfs.NewGlobalFileAccess(false).WriteFile(context.Background(), target, []byte("draft")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bridge := &DAGBridge{
		activeDAGs: map[string]*ActiveDAGMeta{
			"dag-1": {SessionID: "sess-1"},
		},
		sessionVFS: func(sessionID string) *versioning.SessionVFS {
			if sessionID == "sess-1" {
				return svfs
			}
			return nil
		},
	}

	if err := bridge.compositeGate()(context.Background(), "dag-1", 0, nil); err != nil {
		t.Fatalf("compositeGate: %v", err)
	}

	if _, err := os.ReadFile(target); err == nil {
		t.Fatal("expected draft file to remain out of disk until explicit commit")
	} else if !os.IsNotExist(err) {
		t.Fatalf("ReadFile error = %v, want not exists", err)
	}
	if pending := svfs.DiskFlusher().PendingChanges(); len(pending) == 0 {
		t.Fatal("expected pending draft changes to remain until explicit commit")
	}
}
