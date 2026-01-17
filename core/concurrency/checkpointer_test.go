package concurrency_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpoint_NewAndSerialize(t *testing.T) {
	cp := concurrency.NewCheckpoint("test-id", 42)

	assert.Equal(t, "test-id", cp.ID)
	assert.Equal(t, uint64(42), cp.WALSequence)
	assert.NotZero(t, cp.CreatedAt)
	assert.NotNil(t, cp.Pipelines)
	assert.NotNil(t, cp.Agents)

	data, err := cp.Serialize()
	require.NoError(t, err)
	assert.Contains(t, string(data), "test-id")
}

func TestCheckpoint_DeserializeRoundtrip(t *testing.T) {
	cp := concurrency.NewCheckpoint("roundtrip-id", 100)
	cp.AddPipeline("pipe-1", concurrency.PipelineActive, "phase-2")
	cp.AddAgent("agent-1", "engineer", "task-1", "context summary")
	cp.SetLLMQueues(5, 10)
	cp.AddStagingFile("/path/file.go", "abc123", time.Now())
	cp.Finalize()

	data, err := cp.Serialize()
	require.NoError(t, err)

	restored, err := concurrency.DeserializeCheckpoint(data)
	require.NoError(t, err)

	assert.Equal(t, cp.ID, restored.ID)
	assert.Equal(t, cp.WALSequence, restored.WALSequence)
	assert.Equal(t, cp.ContentHash, restored.ContentHash)
	assert.Len(t, restored.Pipelines, 1)
	assert.Len(t, restored.Agents, 1)
	assert.Len(t, restored.StagingDir.Files, 1)
}

func TestCheckpoint_Validate(t *testing.T) {
	cp := concurrency.NewCheckpoint("valid-id", 50)
	cp.AddPipeline("pipe-1", concurrency.PipelineWaiting, "phase-1")
	cp.Finalize()

	err := cp.Validate()
	assert.NoError(t, err)

	cp.ContentHash = "invalid-hash"
	err = cp.Validate()
	assert.ErrorIs(t, err, concurrency.ErrCorruptedCheckpoint)
}

func TestCheckpoint_ComputeHash(t *testing.T) {
	cp1 := concurrency.NewCheckpoint("id-1", 10)
	cp2 := concurrency.NewCheckpoint("id-2", 10)

	hash1 := cp1.ComputeHash()
	hash2 := cp2.ComputeHash()
	assert.Equal(t, hash1, hash2)

	cp1.AddPipeline("pipe", concurrency.PipelineActive, "phase")
	hash3 := cp1.ComputeHash()
	assert.NotEqual(t, hash1, hash3)
}

func TestCheckpointer_CreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)
	defer cp.Stop()

	err = cp.CreateCheckpoint()
	require.NoError(t, err)

	loaded, err := cp.LoadLatest()
	require.NoError(t, err)
	assert.NotEmpty(t, loaded.ID)
	assert.NoError(t, loaded.Validate())
}

func TestCheckpointer_WithWAL(t *testing.T) {
	dir := t.TempDir()

	walConfig := concurrency.WALConfig{
		Dir:            filepath.Join(dir, "wal"),
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(walConfig)
	require.NoError(t, err)
	defer wal.Close()

	for i := 0; i < 5; i++ {
		_, err := wal.Append(&concurrency.WALEntry{Type: concurrency.EntryStateChange})
		require.NoError(t, err)
	}

	cpConfig := concurrency.CheckpointerConfig{
		Dir:            filepath.Join(dir, "checkpoints"),
		Interval:       time.Hour,
		MaxCheckpoints: 10,
		WAL:            wal,
	}

	cp, err := concurrency.NewCheckpointer(cpConfig)
	require.NoError(t, err)
	defer cp.Stop()

	err = cp.CreateCheckpoint()
	require.NoError(t, err)

	loaded, err := cp.LoadLatest()
	require.NoError(t, err)
	assert.Equal(t, uint64(5), loaded.WALSequence)
}

func TestCheckpointer_PeriodicCheckpoints(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       30 * time.Millisecond,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)

	cp.Start()
	time.Sleep(250 * time.Millisecond)
	cp.Stop()

	files, err := os.ReadDir(dir)
	require.NoError(t, err)

	checkpointCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			checkpointCount++
		}
	}

	assert.GreaterOrEqual(t, checkpointCount, 1)
}

func TestCheckpointer_TriggerCheckpoint(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)

	cp.Start()
	defer cp.Stop()

	cp.TriggerCheckpoint()
	time.Sleep(50 * time.Millisecond)

	loaded, err := cp.LoadLatest()
	require.NoError(t, err)
	assert.NotNil(t, loaded)
}

func TestCheckpointer_MaxCheckpoints(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 3,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)
	defer cp.Stop()

	for i := 0; i < 10; i++ {
		err = cp.CreateCheckpoint()
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
	}

	files, err := os.ReadDir(dir)
	require.NoError(t, err)

	checkpointCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			checkpointCount++
		}
	}

	assert.LessOrEqual(t, checkpointCount, 3)
}

func TestCheckpointer_LoadByID(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)
	defer cp.Stop()

	err = cp.CreateCheckpoint()
	require.NoError(t, err)

	latest, err := cp.LoadLatest()
	require.NoError(t, err)

	byID, err := cp.LoadByID(latest.ID)
	require.NoError(t, err)
	assert.Equal(t, latest.ID, byID.ID)

	_, err = cp.LoadByID("non-existent-id")
	assert.ErrorIs(t, err, concurrency.ErrCheckpointNotFound)
}

func TestCheckpointer_LastCheckpoint(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)
	defer cp.Stop()

	assert.Nil(t, cp.LastCheckpoint())

	err = cp.CreateCheckpoint()
	require.NoError(t, err)

	last := cp.LastCheckpoint()
	assert.NotNil(t, last)
}

func TestCheckpointer_ClosedOperations(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)

	err = cp.Stop()
	require.NoError(t, err)

	err = cp.CreateCheckpoint()
	assert.ErrorIs(t, err, concurrency.ErrCheckpointerClosed)

	_, err = cp.LoadLatest()
	assert.ErrorIs(t, err, concurrency.ErrCheckpointerClosed)

	_, err = cp.LoadByID("any")
	assert.ErrorIs(t, err, concurrency.ErrCheckpointerClosed)

	err = cp.Stop()
	assert.ErrorIs(t, err, concurrency.ErrCheckpointerClosed)
}

func TestCheckpointer_WithStateProvider(t *testing.T) {
	dir := t.TempDir()

	provider := &testStateProvider{
		pipelines: map[string]concurrency.PipelineSnapshot{
			"pipe-1": {ID: "pipe-1", State: concurrency.PipelineActive, Phase: "build"},
		},
		agents: map[string]concurrency.AgentSnapshot{
			"agent-1": {ID: "agent-1", Type: "engineer", CurrentTask: "implement feature"},
		},
	}

	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 10,
		StateProvider:  provider,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)
	defer cp.Stop()

	err = cp.CreateCheckpoint()
	require.NoError(t, err)

	loaded, err := cp.LoadLatest()
	require.NoError(t, err)

	assert.Len(t, loaded.Pipelines, 1)
	assert.Equal(t, "pipe-1", loaded.Pipelines["pipe-1"].ID)
	assert.Len(t, loaded.Agents, 1)
	assert.Equal(t, "engineer", loaded.Agents["agent-1"].Type)
}

type testStateProvider struct {
	pipelines map[string]concurrency.PipelineSnapshot
	agents    map[string]concurrency.AgentSnapshot
}

func (p *testStateProvider) CollectState(cp *concurrency.Checkpoint) error {
	for id, pipe := range p.pipelines {
		cp.Pipelines[id] = pipe
	}
	for id, agent := range p.agents {
		cp.Agents[id] = agent
	}
	return nil
}

func TestCheckpointer_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       time.Hour,
		MaxCheckpoints: 100,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)
	defer cp.Stop()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_ = cp.CreateCheckpoint()
			}
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = cp.LoadLatest()
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	_, err = cp.LoadLatest()
	assert.NoError(t, err)
}

func TestCheckpointer_GracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.CheckpointerConfig{
		Dir:            dir,
		Interval:       50 * time.Millisecond,
		MaxCheckpoints: 10,
	}

	cp, err := concurrency.NewCheckpointer(config)
	require.NoError(t, err)

	cp.Start()
	time.Sleep(100 * time.Millisecond)

	err = cp.Stop()
	require.NoError(t, err)

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Greater(t, len(files), 0)
}

func TestCheckpoint_PipelineStates(t *testing.T) {
	states := []concurrency.PipelineState{
		concurrency.PipelineActive,
		concurrency.PipelineWaiting,
		concurrency.PipelineReady,
	}

	for _, state := range states {
		cp := concurrency.NewCheckpoint("test", 1)
		cp.AddPipeline("pipe", state, "phase")
		assert.Equal(t, state, cp.Pipelines["pipe"].State)
	}
}

func TestDefaultCheckpointerConfig(t *testing.T) {
	config := concurrency.DefaultCheckpointerConfig()

	assert.Equal(t, ".sylk/checkpoints", config.Dir)
	assert.Equal(t, 5*time.Second, config.Interval)
	assert.Equal(t, 10, config.MaxCheckpoints)
}

func TestNoOpStateProvider(t *testing.T) {
	provider := &concurrency.NoOpStateProvider{}
	cp := concurrency.NewCheckpoint("test", 1)

	err := provider.CollectState(cp)
	assert.NoError(t, err)
	assert.Empty(t, cp.Pipelines)
	assert.Empty(t, cp.Agents)
}
