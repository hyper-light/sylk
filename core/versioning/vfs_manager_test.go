package versioning

import (
	"sync"
	"testing"
)

func TestMemoryVFSManager_CreateAndGet(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	cfg := VFSConfig{
		PipelineID: "pipeline-1",
		SessionID:  "session-1",
		WorkingDir: tmpDir,
	}

	vfs, err := manager.CreatePipelineVFS(cfg)
	if err != nil {
		t.Fatalf("CreatePipelineVFS failed: %v", err)
	}

	if vfs.GetPipelineID() != "pipeline-1" {
		t.Errorf("PipelineID mismatch: got %q", vfs.GetPipelineID())
	}

	retrieved, err := manager.GetPipelineVFS("pipeline-1")
	if err != nil {
		t.Fatalf("GetPipelineVFS failed: %v", err)
	}

	if retrieved != vfs {
		t.Error("Retrieved VFS should be the same instance")
	}
}

func TestMemoryVFSManager_GetNotFound(t *testing.T) {
	t.Parallel()

	manager := NewMemoryVFSManager(VFSManagerConfig{})
	defer manager.Close()

	_, err := manager.GetPipelineVFS("nonexistent")
	if err != ErrVFSNotFound {
		t.Errorf("Expected ErrVFSNotFound, got %v", err)
	}
}

func TestMemoryVFSManager_Close(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})

	cfg := VFSConfig{
		PipelineID: "pipeline-1",
		SessionID:  "session-1",
		WorkingDir: tmpDir,
	}

	manager.CreatePipelineVFS(cfg)

	if err := manager.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err := manager.CreatePipelineVFS(cfg)
	if err != ErrVFSClosed {
		t.Errorf("Expected ErrVFSClosed after close, got %v", err)
	}
}

func TestMemoryVFSManager_ClosePipelineVFS(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	cfg := VFSConfig{
		PipelineID: "pipeline-1",
		SessionID:  "session-1",
		WorkingDir: tmpDir,
	}

	manager.CreatePipelineVFS(cfg)

	if err := manager.ClosePipelineVFS("pipeline-1"); err != nil {
		t.Fatalf("ClosePipelineVFS failed: %v", err)
	}

	_, err := manager.GetPipelineVFS("pipeline-1")
	if err != ErrVFSNotFound {
		t.Errorf("Expected ErrVFSNotFound after close, got %v", err)
	}
}

func TestMemoryVFSManager_SessionIndex(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	for i := 1; i <= 3; i++ {
		cfg := VFSConfig{
			PipelineID: "pipeline-" + string(rune('0'+i)),
			SessionID:  "session-1",
			WorkingDir: tmpDir,
		}
		manager.CreatePipelineVFS(cfg)
	}

	vfses := manager.GetSessionVFSes("session-1")
	if len(vfses) != 3 {
		t.Errorf("Expected 3 VFSes for session, got %d", len(vfses))
	}

	vfses = manager.GetSessionVFSes("session-2")
	if len(vfses) != 0 {
		t.Errorf("Expected 0 VFSes for nonexistent session, got %d", len(vfses))
	}
}

func TestMemoryVFSManager_CleanupSession(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	for i := 1; i <= 3; i++ {
		cfg := VFSConfig{
			PipelineID: "pipeline-" + string(rune('0'+i)),
			SessionID:  "session-1",
			WorkingDir: tmpDir,
		}
		manager.CreatePipelineVFS(cfg)
	}

	if err := manager.CleanupSession("session-1"); err != nil {
		t.Fatalf("CleanupSession failed: %v", err)
	}

	vfses := manager.GetSessionVFSes("session-1")
	if len(vfses) != 0 {
		t.Errorf("Expected 0 VFSes after cleanup, got %d", len(vfses))
	}
}

func TestMemoryVFSManager_VariantGroup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:     "group-1",
		SessionID:   "session-1",
		BasePath:    tmpDir,
		Description: "Test variant group",
	}

	group, err := manager.CreateVariantGroup(groupCfg)
	if err != nil {
		t.Fatalf("CreateVariantGroup failed: %v", err)
	}

	if group.ID != "group-1" {
		t.Errorf("GroupID mismatch: got %q", group.ID)
	}

	retrieved, err := manager.GetVariantGroup("group-1")
	if err != nil {
		t.Fatalf("GetVariantGroup failed: %v", err)
	}

	if retrieved != group {
		t.Error("Retrieved group should be the same instance")
	}
}

func TestMemoryVFSManager_VariantGroupExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}

	manager.CreateVariantGroup(groupCfg)

	_, err := manager.CreateVariantGroup(groupCfg)
	if err != ErrVariantGroupExists {
		t.Errorf("Expected ErrVariantGroupExists, got %v", err)
	}
}

func TestMemoryVFSManager_AddVariant(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}
	group, _ := manager.CreateVariantGroup(groupCfg)

	vfsCfg := VFSConfig{
		PipelineID: "variant-1",
		SessionID:  "session-1",
		WorkingDir: tmpDir,
	}

	if err := manager.AddVariantToGroup("group-1", "variant-1", vfsCfg); err != nil {
		t.Fatalf("AddVariantToGroup failed: %v", err)
	}

	if group.VariantCount() != 1 {
		t.Errorf("Expected 1 variant, got %d", group.VariantCount())
	}

	variants := group.ListVariants()
	if len(variants) != 1 || variants[0] != "variant-1" {
		t.Errorf("Unexpected variants: %v", variants)
	}
}

func TestMemoryVFSManager_SelectVariant(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}
	group, _ := manager.CreateVariantGroup(groupCfg)

	for i := 1; i <= 3; i++ {
		vfsCfg := VFSConfig{
			PipelineID: "variant-" + string(rune('0'+i)),
			SessionID:  "session-1",
			WorkingDir: tmpDir,
		}
		manager.AddVariantToGroup("group-1", "variant-"+string(rune('0'+i)), vfsCfg)
	}

	if err := manager.SelectVariant("group-1", "variant-2"); err != nil {
		t.Fatalf("SelectVariant failed: %v", err)
	}

	if group.GetSelected() != "variant-2" {
		t.Errorf("Expected selected variant-2, got %q", group.GetSelected())
	}

	if !group.IsCommitted() {
		t.Error("Group should be committed after selection")
	}
}

func TestMemoryVFSManager_SelectVariantNotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}
	manager.CreateVariantGroup(groupCfg)

	err := manager.SelectVariant("group-1", "nonexistent")
	if err != ErrVariantNotFound {
		t.Errorf("Expected ErrVariantNotFound, got %v", err)
	}
}

func TestMemoryVFSManager_CancelVariantGroup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}
	manager.CreateVariantGroup(groupCfg)

	vfsCfg := VFSConfig{
		PipelineID: "variant-1",
		SessionID:  "session-1",
		WorkingDir: tmpDir,
	}
	manager.AddVariantToGroup("group-1", "variant-1", vfsCfg)

	if err := manager.CancelVariantGroup("group-1"); err != nil {
		t.Fatalf("CancelVariantGroup failed: %v", err)
	}

	_, err := manager.GetVariantGroup("group-1")
	if err != ErrVariantGroupNotFound {
		t.Errorf("Expected ErrVariantGroupNotFound after cancel, got %v", err)
	}
}

func TestMemoryVFSManager_Stats(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	for i := 1; i <= 2; i++ {
		cfg := VFSConfig{
			PipelineID: "pipeline-" + string(rune('0'+i)),
			SessionID:  SessionID("session-" + string(rune('0'+i))),
			WorkingDir: tmpDir,
		}
		manager.CreatePipelineVFS(cfg)
	}

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}
	manager.CreateVariantGroup(groupCfg)

	stats := manager.Stats()

	if stats.ActiveVFSes != 2 {
		t.Errorf("Expected 2 active VFSes, got %d", stats.ActiveVFSes)
	}
	if stats.VariantGroups != 1 {
		t.Errorf("Expected 1 variant group, got %d", stats.VariantGroups)
	}
	if stats.ActiveSessions != 2 {
		t.Errorf("Expected 2 active sessions, got %d", stats.ActiveSessions)
	}
}

func TestMemoryVFSManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	iterations := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				cfg := VFSConfig{
					PipelineID: "pipeline-" + string(rune('a'+id)) + "-" + string(rune('0'+j%10)),
					SessionID:  SessionID("session-" + string(rune('0'+id%3))),
					WorkingDir: tmpDir,
				}

				if j%3 == 0 {
					manager.CreatePipelineVFS(cfg)
				} else if j%3 == 1 {
					manager.GetPipelineVFS(cfg.PipelineID)
				} else {
					manager.GetSessionVFSes(cfg.SessionID)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestVariantGroup_GetVariant(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manager := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: tmpDir,
	})
	defer manager.Close()

	groupCfg := VariantGroupConfig{
		GroupID:   "group-1",
		SessionID: "session-1",
	}
	group, _ := manager.CreateVariantGroup(groupCfg)

	vfsCfg := VFSConfig{
		PipelineID: "variant-1",
		SessionID:  "session-1",
		WorkingDir: tmpDir,
	}
	manager.AddVariantToGroup("group-1", "variant-1", vfsCfg)

	vfs, err := group.GetVariant("variant-1")
	if err != nil {
		t.Fatalf("GetVariant failed: %v", err)
	}
	if vfs == nil {
		t.Error("Expected non-nil VFS")
	}

	_, err = group.GetVariant("nonexistent")
	if err != ErrVariantNotFound {
		t.Errorf("Expected ErrVariantNotFound, got %v", err)
	}
}
