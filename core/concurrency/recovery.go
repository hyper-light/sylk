package concurrency

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrRecoveryFailed    = errors.New("recovery failed")
	ErrNoValidCheckpoint = errors.New("no valid checkpoint found")
)

type RecoveryConfig struct {
	DataDir            string        `yaml:"data_dir"`
	ShutdownMarkerFile string        `yaml:"shutdown_marker_file"`
	MaxCheckpointAge   time.Duration `yaml:"max_checkpoint_age"`
}

func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		DataDir:            ".sylk",
		ShutdownMarkerFile: ".shutdown_marker",
		MaxCheckpointAge:   24 * time.Hour,
	}
}

type RecoveredPipeline struct {
	ID     string
	Status string
	Phase  string
	Error  error
}

type RecoveryResult struct {
	RecoveryNeeded     bool
	Checkpoint         *Checkpoint
	RecoveredPipelines []RecoveredPipeline
	FailedRecoveries   []RecoveredPipeline
	WALEntriesReplayed int
	StartedFresh       bool
}

type RecoveryManager struct {
	config       RecoveryConfig
	wal          *WriteAheadLog
	checkpointer *Checkpointer
}

func NewRecoveryManager(config RecoveryConfig, wal *WriteAheadLog, checkpointer *Checkpointer) *RecoveryManager {
	return &RecoveryManager{
		config:       config,
		wal:          wal,
		checkpointer: checkpointer,
	}
}

func (r *RecoveryManager) Recover(ctx context.Context) (*RecoveryResult, error) {
	result := &RecoveryResult{}

	if !r.needsRecovery() {
		result.RecoveryNeeded = false
		return result, r.writeShutdownMarker()
	}

	result.RecoveryNeeded = true
	return r.performRecovery(ctx, result)
}

func (r *RecoveryManager) performRecovery(ctx context.Context, result *RecoveryResult) (*RecoveryResult, error) {
	checkpoint, err := r.loadCheckpoint(ctx)
	if err != nil {
		return r.handleNoCheckpoint(result)
	}

	result.Checkpoint = checkpoint
	return r.recoverFromCheckpoint(ctx, result, checkpoint)
}

func (r *RecoveryManager) handleNoCheckpoint(result *RecoveryResult) (*RecoveryResult, error) {
	result.StartedFresh = true
	return result, r.writeShutdownMarker()
}

func (r *RecoveryManager) recoverFromCheckpoint(ctx context.Context, result *RecoveryResult, cp *Checkpoint) (*RecoveryResult, error) {
	replayed, err := r.replayWAL(ctx, cp.WALSequence+1)
	if err != nil {
		return result, err
	}
	result.WALEntriesReplayed = replayed

	recovered, failed, err := r.recoverPipelines(ctx, cp)
	if err != nil {
		return result, err
	}

	result.RecoveredPipelines = recovered
	result.FailedRecoveries = failed

	return result, r.writeShutdownMarker()
}

func (r *RecoveryManager) needsRecovery() bool {
	markerPath := r.shutdownMarkerPath()
	_, err := os.Stat(markerPath)
	return err == nil
}

func (r *RecoveryManager) shutdownMarkerPath() string {
	return filepath.Join(r.config.DataDir, r.config.ShutdownMarkerFile)
}

func (r *RecoveryManager) loadCheckpoint(ctx context.Context) (*Checkpoint, error) {
	path, err := r.findLatestCheckpoint()
	if err != nil {
		return nil, err
	}

	cp, err := r.validateCheckpoint(path)
	if err != nil {
		return r.tryPreviousCheckpoint()
	}

	return r.checkCheckpointAge(cp)
}

func (r *RecoveryManager) checkCheckpointAge(cp *Checkpoint) (*Checkpoint, error) {
	if time.Since(cp.CreatedAt) > r.config.MaxCheckpointAge {
		return nil, ErrNoValidCheckpoint
	}
	return cp, nil
}

func (r *RecoveryManager) findLatestCheckpoint() (string, error) {
	files, err := r.listCheckpointFiles()
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "", ErrNoValidCheckpoint
	}

	return files[len(files)-1], nil
}

func (r *RecoveryManager) listCheckpointFiles() ([]string, error) {
	dir := r.checkpointDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	return r.filterAndSortCheckpoints(entries, dir), nil
}

func (r *RecoveryManager) checkpointDir() string {
	return filepath.Join(r.config.DataDir, "checkpoints")
}

func (r *RecoveryManager) filterAndSortCheckpoints(entries []os.DirEntry, dir string) []string {
	var files []string
	for _, entry := range entries {
		if r.isCheckpointFile(entry) {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files
}

func (r *RecoveryManager) isCheckpointFile(entry os.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	name := entry.Name()
	return strings.HasPrefix(name, "checkpoint-") && strings.HasSuffix(name, ".json")
}

func (r *RecoveryManager) validateCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cp, err := DeserializeCheckpoint(data)
	if err != nil {
		return nil, err
	}

	return cp, cp.Validate()
}

func (r *RecoveryManager) tryPreviousCheckpoint() (*Checkpoint, error) {
	files, err := r.listCheckpointFiles()
	if err != nil || len(files) < 2 {
		return nil, ErrNoValidCheckpoint
	}

	return r.validateCheckpoint(files[len(files)-2])
}

func (r *RecoveryManager) replayWAL(ctx context.Context, fromSeq uint64) (int, error) {
	if r.wal == nil {
		return 0, nil
	}

	entries, err := r.wal.ReadFrom(fromSeq)
	if err != nil {
		return 0, err
	}

	return r.applyWALEntries(ctx, entries)
}

func (r *RecoveryManager) applyWALEntries(ctx context.Context, entries []*WALEntry) (int, error) {
	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		if err := r.applyWALEntry(entry); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (r *RecoveryManager) applyWALEntry(entry *WALEntry) error {
	switch entry.Type {
	case EntryStateChange:
		return nil
	case EntryLLMRequest, EntryLLMResponse:
		return nil
	case EntryFileChange:
		return nil
	}
	return nil
}

func (r *RecoveryManager) getReplayStartSequence(checkpoint *Checkpoint) uint64 {
	if checkpoint == nil {
		return 0
	}
	return checkpoint.WALSequence + 1
}

func (r *RecoveryManager) isAlreadyApplied(entry *WALEntry, checkpoint *Checkpoint) bool {
	if checkpoint == nil {
		return false
	}
	return entry.Sequence <= checkpoint.WALSequence
}

func (r *RecoveryManager) recoverPipelines(ctx context.Context, cp *Checkpoint) ([]RecoveredPipeline, []RecoveredPipeline, error) {
	var recovered []RecoveredPipeline
	var failed []RecoveredPipeline

	for _, pipeline := range cp.Pipelines {
		rp := r.recoverPipeline(ctx, &pipeline)
		r.categorizePipeline(rp, &recovered, &failed)
	}

	return recovered, failed, nil
}

func (r *RecoveryManager) categorizePipeline(rp *RecoveredPipeline, recovered, failed *[]RecoveredPipeline) {
	if rp.Status == "failed" {
		*failed = append(*failed, *rp)
	} else {
		*recovered = append(*recovered, *rp)
	}
}

func (r *RecoveryManager) recoverPipeline(ctx context.Context, p *PipelineSnapshot) *RecoveredPipeline {
	switch p.State {
	case PipelineReady:
		return r.recoverCompletedPipeline(p)
	case PipelineActive:
		return r.recoverInProgressPipeline(p)
	case PipelineWaiting:
		return r.recoverWaitingPipeline(p)
	}
	return r.recoverUnknownPipeline(p)
}

func (r *RecoveryManager) recoverCompletedPipeline(p *PipelineSnapshot) *RecoveredPipeline {
	return &RecoveredPipeline{
		ID:     p.ID,
		Status: "completed",
		Phase:  p.Phase,
	}
}

func (r *RecoveryManager) recoverInProgressPipeline(p *PipelineSnapshot) *RecoveredPipeline {
	if err := r.validateStagingDir(p.ID); err != nil {
		return r.failedPipelineRecovery(p, err)
	}

	return &RecoveredPipeline{
		ID:     p.ID,
		Status: "restarted",
		Phase:  p.Phase,
	}
}

func (r *RecoveryManager) failedPipelineRecovery(p *PipelineSnapshot, err error) *RecoveredPipeline {
	return &RecoveredPipeline{
		ID:     p.ID,
		Status: "failed",
		Phase:  p.Phase,
		Error:  err,
	}
}

func (r *RecoveryManager) recoverWaitingPipeline(p *PipelineSnapshot) *RecoveredPipeline {
	return &RecoveredPipeline{
		ID:     p.ID,
		Status: "re-enqueued",
		Phase:  p.Phase,
	}
}

func (r *RecoveryManager) recoverUnknownPipeline(p *PipelineSnapshot) *RecoveredPipeline {
	return &RecoveredPipeline{
		ID:     p.ID,
		Status: "re-enqueued",
		Phase:  p.Phase,
	}
}

func (r *RecoveryManager) validateStagingDir(pipelineID string) error {
	stagingPath := r.stagingDirPath(pipelineID)
	info, err := os.Stat(stagingPath)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return errors.New("staging path is not a directory")
	}

	return nil
}

func (r *RecoveryManager) stagingDirPath(pipelineID string) string {
	return filepath.Join(r.config.DataDir, "staging", pipelineID)
}

func (r *RecoveryManager) writeShutdownMarker() error {
	markerPath := r.shutdownMarkerPath()

	if err := os.MkdirAll(filepath.Dir(markerPath), 0755); err != nil {
		return err
	}

	return os.WriteFile(markerPath, []byte(time.Now().Format(time.RFC3339)), 0644)
}

func (r *RecoveryManager) removeShutdownMarker() error {
	markerPath := r.shutdownMarkerPath()
	err := os.Remove(markerPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (r *RecoveryManager) MarkStartup() error {
	return r.writeShutdownMarker()
}

func (r *RecoveryManager) MarkCleanShutdown() error {
	return r.removeShutdownMarker()
}
