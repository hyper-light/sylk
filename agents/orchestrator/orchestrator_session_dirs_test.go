package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

func TestEnsureSessionDataPlaneDirs_CreatesFreshRunSessionPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sd := sylkdir.New(root)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir.Init: %v", err)
	}

	sessionID := "run-session-123"
	if err := ensureSessionDataPlaneDirs(sd, sessionID); err != nil {
		t.Fatalf("ensureSessionDataPlaneDirs: %v", err)
	}

	for _, path := range []string{
		sd.SessionPath(sessionID),
		sd.SessionOrchestratorPath(sessionID),
		sd.SessionOrchestratorWALPath(sessionID),
		sd.SessionAgentWALPath(sessionID, "orchestrator"),
		sd.SessionFabricPath(sessionID),
		sd.SessionBusPath(sessionID),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", filepath.Base(path), err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
}

func TestOpenStore_CreatesParentDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing", "nested", "orchestrator.db")
	store, err := OpenStore(DefaultStoreConfig(path))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent directory missing: %v", err)
	}
}
