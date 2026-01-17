package concurrency_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestRecoveryManager(t *testing.T) (*concurrency.RecoveryManager, string) {
	dir := t.TempDir()
	config := concurrency.RecoveryConfig{
		DataDir:            dir,
		ShutdownMarkerFile: ".shutdown_marker",
		MaxCheckpointAge:   24 * time.Hour,
	}
	rm := concurrency.NewRecoveryManager(config, nil, nil)
	return rm, dir
}

func createTestRecoveryManagerWithWAL(t *testing.T) (*concurrency.RecoveryManager, *concurrency.WriteAheadLog, string) {
	dir := t.TempDir()

	walConfig := concurrency.WALConfig{
		Dir:            filepath.Join(dir, "wal"),
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(walConfig)
	require.NoError(t, err)

	config := concurrency.RecoveryConfig{
		DataDir:            dir,
		ShutdownMarkerFile: ".shutdown_marker",
		MaxCheckpointAge:   24 * time.Hour,
	}

	rm := concurrency.NewRecoveryManager(config, wal, nil)
	return rm, wal, dir
}

func createTestCheckpointFile(t *testing.T, dir string, cp *concurrency.Checkpoint) string {
	cpDir := filepath.Join(dir, "checkpoints")
	require.NoError(t, os.MkdirAll(cpDir, 0755))

	cp.Finalize()
	data, err := cp.Serialize()
	require.NoError(t, err)

	filename := "checkpoint-" + cp.CreatedAt.Format("20060102-150405") + ".json"
	path := filepath.Join(cpDir, filename)
	require.NoError(t, os.WriteFile(path, data, 0644))

	return path
}

func createShutdownMarker(t *testing.T, dir string) {
	markerPath := filepath.Join(dir, ".shutdown_marker")
	require.NoError(t, os.MkdirAll(filepath.Dir(markerPath), 0755))
	require.NoError(t, os.WriteFile(markerPath, []byte(time.Now().Format(time.RFC3339)), 0644))
}

func shutdownMarkerExists(dir string) bool {
	markerPath := filepath.Join(dir, ".shutdown_marker")
	_, err := os.Stat(markerPath)
	return err == nil
}

func TestRecoveryManager_CleanShutdown_NoRecoveryNeeded(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.False(t, result.RecoveryNeeded)
	assert.Nil(t, result.Checkpoint)
	assert.Empty(t, result.RecoveredPipelines)
	assert.Empty(t, result.FailedRecoveries)
	assert.Zero(t, result.WALEntriesReplayed)
	assert.False(t, result.StartedFresh)
	assert.True(t, shutdownMarkerExists(dir))
}

func TestRecoveryManager_Recover_WithValidCheckpoint(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("pipe-1", concurrency.PipelineReady, "completed")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.NotNil(t, result.Checkpoint)
	assert.Equal(t, "test-cp", result.Checkpoint.ID)
	assert.Len(t, result.RecoveredPipelines, 1)
	assert.Equal(t, "completed", result.RecoveredPipelines[0].Status)
}

func TestRecoveryManager_Recover_WALReplay(t *testing.T) {
	rm, wal, dir := createTestRecoveryManagerWithWAL(t)
	defer wal.Close()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test payload"),
		})
		require.NoError(t, err)
	}

	cp := concurrency.NewCheckpoint("test-cp", 2)
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.NotNil(t, result.Checkpoint)
	assert.Equal(t, 3, result.WALEntriesReplayed)
}

func TestRecoveryManager_MarkStartup_CreatesMarker(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)

	assert.False(t, shutdownMarkerExists(dir))

	err := rm.MarkStartup()
	require.NoError(t, err)

	assert.True(t, shutdownMarkerExists(dir))
}

func TestRecoveryManager_MarkCleanShutdown_RemovesMarker(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)

	createShutdownMarker(t, dir)
	assert.True(t, shutdownMarkerExists(dir))

	err := rm.MarkCleanShutdown()
	require.NoError(t, err)

	assert.False(t, shutdownMarkerExists(dir))
}

func TestRecoveryManager_Recover_NoCheckpoint_StartsFresh(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.Nil(t, result.Checkpoint)
	assert.True(t, result.StartedFresh)
	assert.Empty(t, result.RecoveredPipelines)
	assert.Zero(t, result.WALEntriesReplayed)
}

func TestRecoveryManager_Recover_CorruptCheckpoint_UsesPrevious(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cpDir := filepath.Join(dir, "checkpoints")
	require.NoError(t, os.MkdirAll(cpDir, 0755))

	olderCp := concurrency.NewCheckpoint("older-cp", 5)
	olderCp.CreatedAt = time.Now().Add(-time.Hour)
	olderCp.Finalize()
	olderData, err := olderCp.Serialize()
	require.NoError(t, err)
	olderPath := filepath.Join(cpDir, "checkpoint-20060102-140000.json")
	require.NoError(t, os.WriteFile(olderPath, olderData, 0644))

	newerPath := filepath.Join(cpDir, "checkpoint-20060102-150000.json")
	require.NoError(t, os.WriteFile(newerPath, []byte("corrupted data"), 0644))

	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.NotNil(t, result.Checkpoint)
	assert.Equal(t, "older-cp", result.Checkpoint.ID)
}

func TestRecoveryManager_Recover_NoWAL(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("test-cp", 10)
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.Zero(t, result.WALEntriesReplayed)
}

func TestRecoveryManager_NeedsRecovery_MarkerExists(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
}

func TestRecoveryManager_NeedsRecovery_NoMarker(t *testing.T) {
	rm, _ := createTestRecoveryManager(t)
	ctx := context.Background()

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.False(t, result.RecoveryNeeded)
}

func TestRecoveryConfig_Defaults(t *testing.T) {
	config := concurrency.DefaultRecoveryConfig()

	assert.Equal(t, ".sylk", config.DataDir)
	assert.Equal(t, ".shutdown_marker", config.ShutdownMarkerFile)
	assert.Equal(t, 24*time.Hour, config.MaxCheckpointAge)
}

func TestRecoveryManager_MarkCleanShutdown_NoMarker(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)

	assert.False(t, shutdownMarkerExists(dir))

	err := rm.MarkCleanShutdown()
	require.NoError(t, err)
}

func TestRecoveryManager_Recover_ExpiredCheckpoint(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.RecoveryConfig{
		DataDir:            dir,
		ShutdownMarkerFile: ".shutdown_marker",
		MaxCheckpointAge:   1 * time.Nanosecond,
	}
	rm := concurrency.NewRecoveryManager(config, nil, nil)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("expired-cp", 5)
	cp.CreatedAt = time.Now().Add(-time.Hour)
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	time.Sleep(10 * time.Millisecond)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.True(t, result.StartedFresh)
}

func TestRecoveryManager_RecoverPipeline_Completed(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("completed-pipe", concurrency.PipelineReady, "final")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	require.Len(t, result.RecoveredPipelines, 1)
	assert.Equal(t, "completed-pipe", result.RecoveredPipelines[0].ID)
	assert.Equal(t, "completed", result.RecoveredPipelines[0].Status)
	assert.Equal(t, "final", result.RecoveredPipelines[0].Phase)
	assert.Empty(t, result.FailedRecoveries)
}

func TestRecoveryManager_RecoverPipeline_InProgress(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	stagingDir := filepath.Join(dir, "staging", "active-pipe")
	require.NoError(t, os.MkdirAll(stagingDir, 0755))

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("active-pipe", concurrency.PipelineActive, "processing")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	require.Len(t, result.RecoveredPipelines, 1)
	assert.Equal(t, "active-pipe", result.RecoveredPipelines[0].ID)
	assert.Equal(t, "restarted", result.RecoveredPipelines[0].Status)
	assert.Equal(t, "processing", result.RecoveredPipelines[0].Phase)
}

func TestRecoveryManager_RecoverPipeline_InProgress_NoStagingDir(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("active-pipe", concurrency.PipelineActive, "processing")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.Empty(t, result.RecoveredPipelines)
	require.Len(t, result.FailedRecoveries, 1)
	assert.Equal(t, "active-pipe", result.FailedRecoveries[0].ID)
	assert.Equal(t, "failed", result.FailedRecoveries[0].Status)
	assert.NotNil(t, result.FailedRecoveries[0].Error)
}

func TestRecoveryManager_RecoverPipeline_Waiting(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("waiting-pipe", concurrency.PipelineWaiting, "queued")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	require.Len(t, result.RecoveredPipelines, 1)
	assert.Equal(t, "waiting-pipe", result.RecoveredPipelines[0].ID)
	assert.Equal(t, "re-enqueued", result.RecoveredPipelines[0].Status)
	assert.Equal(t, "queued", result.RecoveredPipelines[0].Phase)
}

func TestRecoveryManager_RecoverPipeline_MultiplePipelines(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	stagingDir := filepath.Join(dir, "staging", "active-pipe")
	require.NoError(t, os.MkdirAll(stagingDir, 0755))

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("completed-pipe", concurrency.PipelineReady, "done")
	cp.AddPipeline("active-pipe", concurrency.PipelineActive, "building")
	cp.AddPipeline("waiting-pipe", concurrency.PipelineWaiting, "queued")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	result, err := rm.Recover(ctx)
	require.NoError(t, err)

	assert.Len(t, result.RecoveredPipelines, 3)
	assert.Empty(t, result.FailedRecoveries)

	statusMap := make(map[string]string)
	for _, rp := range result.RecoveredPipelines {
		statusMap[rp.ID] = rp.Status
	}

	assert.Equal(t, "completed", statusMap["completed-pipe"])
	assert.Equal(t, "restarted", statusMap["active-pipe"])
	assert.Equal(t, "re-enqueued", statusMap["waiting-pipe"])
}

func TestRecoveryManager_ConcurrentRecover(t *testing.T) {
	rm, dir := createTestRecoveryManager(t)
	ctx := context.Background()

	cp := concurrency.NewCheckpoint("test-cp", 10)
	cp.AddPipeline("pipe-1", concurrency.PipelineReady, "done")
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	var wg sync.WaitGroup
	results := make([]*concurrency.RecoveryResult, 10)
	errors := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = rm.Recover(ctx)
		}(i)
	}

	wg.Wait()

	for i, err := range errors {
		assert.NoError(t, err, "Recovery %d failed", i)
	}

	recoveryCount := 0
	for _, result := range results {
		if result.RecoveryNeeded {
			recoveryCount++
		}
	}

	assert.GreaterOrEqual(t, recoveryCount, 1)
}

func TestRecoveryManager_ConcurrentMarkStartupShutdown(t *testing.T) {
	rm, _ := createTestRecoveryManager(t)

	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < iterations; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			_ = rm.MarkStartup()
		}()

		go func() {
			defer wg.Done()
			_ = rm.MarkCleanShutdown()
		}()
	}

	wg.Wait()
}

func TestRecoveryManager_Recover_ContextCancelled(t *testing.T) {
	rm, wal, dir := createTestRecoveryManagerWithWAL(t)
	defer wal.Close()

	for i := 0; i < 100; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test payload"),
		})
		require.NoError(t, err)
	}

	cp := concurrency.NewCheckpoint("test-cp", 0)
	createTestCheckpointFile(t, dir, cp)
	createShutdownMarker(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := rm.Recover(ctx)

	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	} else {
		assert.NotNil(t, result)
	}
}

func TestRecoveryManager_FullRecoveryCycle(t *testing.T) {
	rm, wal, dir := createTestRecoveryManagerWithWAL(t)
	defer wal.Close()
	ctx := context.Background()

	err := rm.MarkStartup()
	require.NoError(t, err)
	assert.True(t, shutdownMarkerExists(dir))

	for i := 0; i < 10; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("operation"),
		})
		require.NoError(t, err)
	}

	err = rm.MarkCleanShutdown()
	require.NoError(t, err)
	assert.False(t, shutdownMarkerExists(dir))

	result, err := rm.Recover(ctx)
	require.NoError(t, err)
	assert.False(t, result.RecoveryNeeded)
}

func TestRecoveryManager_CrashRecoveryCycle(t *testing.T) {
	dir := t.TempDir()

	walConfig := concurrency.WALConfig{
		Dir:            filepath.Join(dir, "wal"),
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(walConfig)
	require.NoError(t, err)

	config := concurrency.RecoveryConfig{
		DataDir:            dir,
		ShutdownMarkerFile: ".shutdown_marker",
		MaxCheckpointAge:   24 * time.Hour,
	}

	rm1 := concurrency.NewRecoveryManager(config, wal, nil)
	ctx := context.Background()

	err = rm1.MarkStartup()
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("operation"),
		})
		require.NoError(t, err)
	}

	cp := concurrency.NewCheckpoint("crash-cp", 3)
	cp.AddPipeline("pipe-1", concurrency.PipelineActive, "building")
	createTestCheckpointFile(t, dir, cp)

	stagingDir := filepath.Join(dir, "staging", "pipe-1")
	require.NoError(t, os.MkdirAll(stagingDir, 0755))

	wal.Close()

	wal2, err := concurrency.NewWriteAheadLog(walConfig)
	require.NoError(t, err)
	defer wal2.Close()

	rm2 := concurrency.NewRecoveryManager(config, wal2, nil)

	result, err := rm2.Recover(ctx)
	require.NoError(t, err)

	assert.True(t, result.RecoveryNeeded)
	assert.NotNil(t, result.Checkpoint)
	assert.Equal(t, "crash-cp", result.Checkpoint.ID)
	assert.Equal(t, 2, result.WALEntriesReplayed)
	assert.Len(t, result.RecoveredPipelines, 1)
	assert.Equal(t, "restarted", result.RecoveredPipelines[0].Status)
}

func TestRecoveryResult_Initialization(t *testing.T) {
	result := &concurrency.RecoveryResult{}

	assert.False(t, result.RecoveryNeeded)
	assert.Nil(t, result.Checkpoint)
	assert.Nil(t, result.RecoveredPipelines)
	assert.Nil(t, result.FailedRecoveries)
	assert.Zero(t, result.WALEntriesReplayed)
	assert.False(t, result.StartedFresh)
}

func TestRecoveredPipeline_Fields(t *testing.T) {
	rp := concurrency.RecoveredPipeline{
		ID:     "test-pipe",
		Status: "restarted",
		Phase:  "building",
		Error:  nil,
	}

	assert.Equal(t, "test-pipe", rp.ID)
	assert.Equal(t, "restarted", rp.Status)
	assert.Equal(t, "building", rp.Phase)
	assert.Nil(t, rp.Error)
}
