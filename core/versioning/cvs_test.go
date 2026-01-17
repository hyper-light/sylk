package versioning

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewCVS(t *testing.T) {
	cfg := createTestCVSConfig()
	cvs := NewCVS(cfg)

	if cvs == nil {
		t.Fatal("expected non-nil CVS")
	}

	if cvs.defaultLockTTL != 5*time.Minute {
		t.Errorf("expected default lock TTL 5m, got %v", cvs.defaultLockTTL)
	}
}

func TestNewCVS_CustomLockTTL(t *testing.T) {
	cfg := createTestCVSConfig()
	cfg.LockTTL = 10 * time.Second

	cvs := NewCVS(cfg)

	if cvs.defaultLockTTL != 10*time.Second {
		t.Errorf("expected lock TTL 10s, got %v", cvs.defaultLockTTL)
	}
}

func TestCVS_ReadWrite(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	content := []byte("hello world")
	meta := WriteMetadata{SessionID: "session1", PipelineID: "pipe1"}

	versionID, err := cvs.Write(ctx, path, content, meta)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if versionID.IsZero() {
		t.Error("expected non-zero version ID")
	}

	readContent, err := cvs.Read(ctx, path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	if string(readContent) != string(content) {
		t.Errorf("expected %q, got %q", content, readContent)
	}
}

func TestCVS_GetHead(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	cvs.Write(ctx, path, []byte("v1"), meta)
	cvs.Write(ctx, path, []byte("v2"), meta)

	head, err := cvs.GetHead(path)
	if err != nil {
		t.Fatalf("get head failed: %v", err)
	}

	if head == nil {
		t.Fatal("expected non-nil head")
	}
}

func TestCVS_GetHistory(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	cvs.Write(ctx, path, []byte("v1"), meta)
	cvs.Write(ctx, path, []byte("v2"), meta)
	cvs.Write(ctx, path, []byte("v3"), meta)

	history, err := cvs.GetHistory(path, HistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("get history failed: %v", err)
	}

	if len(history) != 3 {
		t.Errorf("expected 3 versions, got %d", len(history))
	}
}

func TestCVS_GetHistory_WithLimit(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	cvs.Write(ctx, path, []byte("v1"), meta)
	cvs.Write(ctx, path, []byte("v2"), meta)
	cvs.Write(ctx, path, []byte("v3"), meta)

	history, err := cvs.GetHistory(path, HistoryOptions{Limit: 2})
	if err != nil {
		t.Fatalf("get history failed: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("expected 2 versions, got %d", len(history))
	}
}

func TestCVS_Delete(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	cvs.Write(ctx, path, []byte("content"), meta)

	_, err := cvs.Delete(ctx, path, meta)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
}

func TestCVS_Delete_NonExistent(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	meta := WriteMetadata{SessionID: "session1"}

	_, err := cvs.Delete(ctx, "/nonexistent", meta)
	if err != ErrFileNotFound && err != ErrNoVersionsForFile {
		t.Errorf("expected file not found error, got %v", err)
	}
}

func TestCVS_GetVersion(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	v1, _ := cvs.Write(ctx, path, []byte("v1"), meta)
	cvs.Write(ctx, path, []byte("v2"), meta)

	version, err := cvs.GetVersion(v1)
	if err != nil {
		t.Fatalf("get version failed: %v", err)
	}

	if version.ID != v1 {
		t.Error("wrong version returned")
	}
}

func TestCVS_BeginPipeline(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	cfg := BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "session1",
		WorkingDir: "/tmp",
	}

	vfs, err := cvs.BeginPipeline(cfg)
	if err != nil {
		t.Fatalf("begin pipeline failed: %v", err)
	}

	if vfs == nil {
		t.Error("expected non-nil VFS")
	}

	stats := cvs.Stats()
	if stats.ActivePipelines != 1 {
		t.Errorf("expected 1 active pipeline, got %d", stats.ActivePipelines)
	}
}

func TestCVS_CommitPipeline(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	cfg := BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "session1",
		WorkingDir: "/tmp",
	}

	cvs.BeginPipeline(cfg)

	versions, err := cvs.CommitPipeline("pipe1")
	if err != nil {
		t.Fatalf("commit pipeline failed: %v", err)
	}

	if versions == nil {
		versions = []VersionID{}
	}

	stats := cvs.Stats()
	if stats.ActivePipelines != 0 {
		t.Errorf("expected 0 active pipelines, got %d", stats.ActivePipelines)
	}
}

func TestCVS_CommitPipeline_NotFound(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	_, err := cvs.CommitPipeline("nonexistent")
	if err != ErrCVSPipelineNotFound {
		t.Errorf("expected pipeline not found error, got %v", err)
	}
}

func TestCVS_RollbackPipeline(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	cfg := BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "session1",
		WorkingDir: "/tmp",
	}

	cvs.BeginPipeline(cfg)

	err := cvs.RollbackPipeline("pipe1")
	if err != nil {
		t.Fatalf("rollback pipeline failed: %v", err)
	}

	stats := cvs.Stats()
	if stats.ActivePipelines != 0 {
		t.Errorf("expected 0 active pipelines, got %d", stats.ActivePipelines)
	}
}

func TestCVS_RollbackPipeline_NotFound(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	err := cvs.RollbackPipeline("nonexistent")
	if err != ErrCVSPipelineNotFound {
		t.Errorf("expected pipeline not found error, got %v", err)
	}
}

func TestCVS_AcquireFileLock(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	lock, err := cvs.AcquireFileLock("/test/file.txt", "session1", time.Minute)
	if err != nil {
		t.Fatalf("acquire lock failed: %v", err)
	}

	if lock == nil {
		t.Fatal("expected non-nil lock")
	}

	if lock.FilePath != "/test/file.txt" {
		t.Errorf("wrong file path: %s", lock.FilePath)
	}

	if lock.SessionID != "session1" {
		t.Errorf("wrong session ID: %s", lock.SessionID)
	}

	stats := cvs.Stats()
	if stats.ActiveLocks != 1 {
		t.Errorf("expected 1 active lock, got %d", stats.ActiveLocks)
	}
}

func TestCVS_AcquireFileLock_AlreadyLocked(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	cvs.AcquireFileLock("/test/file.txt", "session1", time.Minute)

	_, err := cvs.AcquireFileLock("/test/file.txt", "session2", time.Minute)
	if err != ErrCVSFileLocked {
		t.Errorf("expected file locked error, got %v", err)
	}
}

func TestCVS_AcquireFileLock_SameSession(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	lock1, _ := cvs.AcquireFileLock("/test/file.txt", "session1", time.Minute)
	lock2, err := cvs.AcquireFileLock("/test/file.txt", "session1", time.Minute)

	if err != nil {
		t.Fatalf("reacquire lock failed: %v", err)
	}

	if lock2.ID != lock1.ID {
		t.Error("expected same lock to be returned")
	}
}

func TestCVS_ReleaseFileLock(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	lock, _ := cvs.AcquireFileLock("/test/file.txt", "session1", time.Minute)

	err := cvs.ReleaseFileLock(lock.ID)
	if err != nil {
		t.Fatalf("release lock failed: %v", err)
	}

	stats := cvs.Stats()
	if stats.ActiveLocks != 0 {
		t.Errorf("expected 0 active locks, got %d", stats.ActiveLocks)
	}
}

func TestCVS_ReleaseFileLock_NotFound(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	err := cvs.ReleaseFileLock("nonexistent")
	if err != ErrCVSLockNotFound {
		t.Errorf("expected lock not found error, got %v", err)
	}
}

func TestCVS_RefreshFileLock(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	lock, _ := cvs.AcquireFileLock("/test/file.txt", "session1", time.Second)
	originalExpiry := lock.ExpiresAt

	time.Sleep(10 * time.Millisecond)

	err := cvs.RefreshFileLock(lock.ID, time.Hour)
	if err != nil {
		t.Fatalf("refresh lock failed: %v", err)
	}

	cvs.lockMu.RLock()
	refreshedLock := cvs.fileLocks["/test/file.txt"]
	cvs.lockMu.RUnlock()

	if !refreshedLock.ExpiresAt.After(originalExpiry) {
		t.Error("expected expiry to be extended")
	}
}

func TestCVS_Subscribe(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	called := make(chan bool, 1)
	cb := func(event FileChangeEvent) {
		called <- true
	}

	subID, err := cvs.Subscribe("/test/file.txt", "session1", cb)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if subID == "" {
		t.Error("expected non-empty subscription ID")
	}

	stats := cvs.Stats()
	if stats.ActiveSubscribers != 1 {
		t.Errorf("expected 1 active subscriber, got %d", stats.ActiveSubscribers)
	}
}

func TestCVS_Subscribe_Notification(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	var receivedEvent FileChangeEvent
	var wg sync.WaitGroup
	wg.Add(1)

	cb := func(event FileChangeEvent) {
		receivedEvent = event
		wg.Done()
	}

	cvs.Subscribe("/test/file.txt", "session1", cb)

	ctx := context.Background()
	meta := WriteMetadata{SessionID: "session1", PipelineID: "pipe1"}
	cvs.Write(ctx, "/test/file.txt", []byte("content"), meta)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for notification")
	}

	if receivedEvent.FilePath != "/test/file.txt" {
		t.Errorf("wrong file path: %s", receivedEvent.FilePath)
	}

	if receivedEvent.ChangeType != FileChangeCreated {
		t.Errorf("expected created change type, got %v", receivedEvent.ChangeType)
	}
}

func TestCVS_Unsubscribe(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	cb := func(event FileChangeEvent) {}
	subID, _ := cvs.Subscribe("/test/file.txt", "session1", cb)

	err := cvs.Unsubscribe(subID)
	if err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	stats := cvs.Stats()
	if stats.ActiveSubscribers != 0 {
		t.Errorf("expected 0 active subscribers, got %d", stats.ActiveSubscribers)
	}
}

func TestCVS_Merge(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	v1, _ := cvs.Write(ctx, path, []byte("base"), meta)
	v2, _ := cvs.Write(ctx, path, []byte("modified"), meta)

	resolver := NewNoOpConflictResolver()
	mergedID, err := cvs.Merge(ctx, v1, v2, resolver)

	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if mergedID.IsZero() {
		t.Error("expected non-zero merged version ID")
	}
}

func TestCVS_ThreeWayMerge(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"
	meta := WriteMetadata{SessionID: "session1"}

	base, _ := cvs.Write(ctx, path, []byte("base"), meta)
	ours, _ := cvs.Write(ctx, path, []byte("ours"), meta)
	theirs, _ := cvs.Write(ctx, path, []byte("theirs"), meta)

	resolver := NewNoOpConflictResolver()
	mergedID, err := cvs.ThreeWayMerge(ctx, base, ours, theirs, resolver)

	if err != nil {
		t.Fatalf("three-way merge failed: %v", err)
	}

	if mergedID.IsZero() {
		t.Error("expected non-zero merged version ID")
	}
}

func TestCVS_Close(t *testing.T) {
	cvs := createTestCVS(t)

	cfg := BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "session1",
		WorkingDir: "/tmp",
	}
	cvs.BeginPipeline(cfg)
	cvs.AcquireFileLock("/test/file.txt", "session1", time.Minute)
	cvs.Subscribe("/test/file.txt", "session1", func(e FileChangeEvent) {})

	err := cvs.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	err = cvs.checkClosed()
	if err != ErrCVSClosed {
		t.Errorf("expected closed error, got %v", err)
	}
}

func TestCVS_Close_Idempotent(t *testing.T) {
	cvs := createTestCVS(t)

	cvs.Close()
	err := cvs.Close()

	if err != nil {
		t.Errorf("second close should not error: %v", err)
	}
}

func TestCVS_OperationsAfterClose(t *testing.T) {
	cvs := createTestCVS(t)
	cvs.Close()

	ctx := context.Background()
	meta := WriteMetadata{SessionID: "session1"}

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Read", func() error { _, err := cvs.Read(ctx, "/test"); return err }},
		{"Write", func() error { _, err := cvs.Write(ctx, "/test", []byte("x"), meta); return err }},
		{"Delete", func() error { _, err := cvs.Delete(ctx, "/test", meta); return err }},
		{"GetHead", func() error { _, err := cvs.GetHead("/test"); return err }},
		{"GetHistory", func() error { _, err := cvs.GetHistory("/test", HistoryOptions{}); return err }},
		{"AcquireFileLock", func() error { _, err := cvs.AcquireFileLock("/test", "s", time.Minute); return err }},
		{"Subscribe", func() error { _, err := cvs.Subscribe("/test", "s", nil); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err != ErrCVSClosed {
				t.Errorf("expected CVS closed error, got %v", err)
			}
		})
	}
}

func TestCVS_ConcurrentOperations(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			path := "/test/file" + itoa(n) + ".txt"
			meta := WriteMetadata{SessionID: SessionID("session" + itoa(n))}
			cvs.Write(ctx, path, []byte("content"), meta)
			cvs.Read(ctx, path)
		}(i)
	}

	wg.Wait()

	stats := cvs.Stats()
	if stats.TotalFiles != 10 {
		t.Errorf("expected 10 files, got %d", stats.TotalFiles)
	}
}

func TestCVS_ConcurrentLockAcquisition(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := cvs.AcquireFileLock("/test/file.txt", SessionID("session"+itoa(n)), time.Minute)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful lock acquisition, got %d", successCount)
	}
}

func TestCVS_Stats(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	meta := WriteMetadata{SessionID: "session1"}

	cvs.Write(ctx, "/test/file1.txt", []byte("content1"), meta)
	cvs.Write(ctx, "/test/file2.txt", []byte("content2"), meta)
	cvs.AcquireFileLock("/test/file1.txt", "session1", time.Minute)
	cvs.Subscribe("/test/file1.txt", "session1", func(e FileChangeEvent) {})
	cvs.BeginPipeline(BeginPipelineConfig{PipelineID: "pipe1", SessionID: "session1", WorkingDir: "/tmp"})

	stats := cvs.Stats()

	if stats.TotalFiles != 2 {
		t.Errorf("expected 2 files, got %d", stats.TotalFiles)
	}
	if stats.TotalVersions != 2 {
		t.Errorf("expected 2 versions, got %d", stats.TotalVersions)
	}
	if stats.ActiveLocks != 1 {
		t.Errorf("expected 1 lock, got %d", stats.ActiveLocks)
	}
	if stats.ActiveSubscribers != 1 {
		t.Errorf("expected 1 subscriber, got %d", stats.ActiveSubscribers)
	}
	if stats.ActivePipelines != 1 {
		t.Errorf("expected 1 pipeline, got %d", stats.ActivePipelines)
	}
}

func TestCVS_WriteWithLock(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"

	cvs.AcquireFileLock(path, "session1", time.Minute)

	meta := WriteMetadata{SessionID: "session2"}
	_, err := cvs.Write(ctx, path, []byte("content"), meta)
	if err != ErrCVSFileLocked {
		t.Errorf("expected file locked error, got %v", err)
	}

	meta.SessionID = "session1"
	_, err = cvs.Write(ctx, path, []byte("content"), meta)
	if err != nil {
		t.Fatalf("write with lock holder session should succeed: %v", err)
	}
}

func TestCVS_ExpiredLockAllowsWrite(t *testing.T) {
	cvs := createTestCVS(t)
	defer cvs.Close()

	ctx := context.Background()
	path := "/test/file.txt"

	cvs.AcquireFileLock(path, "session1", 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	meta := WriteMetadata{SessionID: "session2"}
	_, err := cvs.Write(ctx, path, []byte("content"), meta)
	if err != nil {
		t.Fatalf("write should succeed after lock expires: %v", err)
	}
}

func TestFileChangeType_String(t *testing.T) {
	tests := []struct {
		ct   FileChangeType
		want string
	}{
		{FileChangeCreated, "created"},
		{FileChangeModified, "modified"},
		{FileChangeDeleted, "deleted"},
		{FileChangeType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.ct.String(); got != tt.want {
			t.Errorf("FileChangeType(%d).String() = %q, want %q", tt.ct, got, tt.want)
		}
	}
}

func TestFileLock_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"not expired", time.Now().Add(time.Hour), false},
		{"expired", time.Now().Add(-time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := &FileLock{ExpiresAt: tt.expiresAt}
			if got := lock.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFileLock_Clone(t *testing.T) {
	lock := &FileLock{
		ID:         "lock1",
		FilePath:   "/test/file.txt",
		SessionID:  "session1",
		AcquiredAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	}

	clone := lock.Clone()

	if clone == lock {
		t.Error("clone should be a different pointer")
	}

	if clone.ID != lock.ID || clone.FilePath != lock.FilePath || clone.SessionID != lock.SessionID {
		t.Error("clone should have same values")
	}
}

func createTestCVSConfig() CVSConfig {
	blobStore := NewMemoryBlobStore()
	dagStore := NewMemoryDAGStore()
	opLog := NewMemoryOperationLog()
	wal := NewMemoryWAL()
	otEngine := NewOTEngine()

	vfsManagerCfg := VFSManagerConfig{
		BaseDir:      "/tmp/test",
		VersionStore: &dagStoreAdapter{dagStore},
		BlobStore:    blobStore,
	}
	vfsManager := NewMemoryVFSManager(vfsManagerCfg)

	return CVSConfig{
		VFSManager: vfsManager,
		BlobStore:  blobStore,
		OpLog:      opLog,
		DAGStore:   dagStore,
		WAL:        wal,
		OTEngine:   otEngine,
	}
}

func createTestCVS(t *testing.T) *DefaultCVS {
	t.Helper()
	cfg := createTestCVSConfig()
	return NewCVS(cfg)
}

type dagStoreAdapter struct {
	dag DAGStore
}

func (a *dagStoreAdapter) GetHead(filePath string) (*FileVersion, error) {
	return a.dag.GetHead(filePath)
}

func (a *dagStoreAdapter) GetVersion(id VersionID) (*FileVersion, error) {
	return a.dag.Get(id)
}

func (a *dagStoreAdapter) GetHistory(filePath string, limit int) ([]FileVersion, error) {
	versions, err := a.dag.GetHistory(filePath, limit)
	if err != nil {
		return nil, err
	}
	result := make([]FileVersion, len(versions))
	for i, v := range versions {
		result[i] = *v
	}
	return result, nil
}

func (a *dagStoreAdapter) AddVersion(version FileVersion) error {
	return a.dag.Add(version)
}
