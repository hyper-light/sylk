package concurrency

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type CheckpointerConfig struct {
	Dir            string
	Interval       time.Duration
	MaxCheckpoints int
	WAL            *WriteAheadLog
	StateProvider  CheckpointStateProvider
}

type CheckpointStateProvider interface {
	CollectState(cp *Checkpoint) error
}

func DefaultCheckpointerConfig() CheckpointerConfig {
	return CheckpointerConfig{
		Dir:            ".sylk/checkpoints",
		Interval:       5 * time.Second,
		MaxCheckpoints: 10,
	}
}

type Checkpointer struct {
	config         CheckpointerConfig
	mu             sync.RWMutex
	running        atomic.Bool
	closed         atomic.Bool
	stopCh         chan struct{}
	doneCh         chan struct{}
	triggerCh      chan struct{}
	lastCheckpoint *Checkpoint
}

func NewCheckpointer(config CheckpointerConfig) (*Checkpointer, error) {
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	return &Checkpointer{
		config:    config,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		triggerCh: make(chan struct{}, 1),
	}, nil
}

func (c *Checkpointer) Start() {
	if c.running.Swap(true) {
		return
	}
	go c.run()
}

func (c *Checkpointer) run() {
	defer close(c.doneCh)

	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	for {
		if c.waitForEvent(ticker.C) {
			return
		}
	}
}

func (c *Checkpointer) waitForEvent(tick <-chan time.Time) bool {
	select {
	case <-c.stopCh:
		c.createCheckpointSafe()
		return true
	case <-tick:
		c.createCheckpointSafe()
	case <-c.triggerCh:
		c.createCheckpointSafe()
	}
	return false
}

func (c *Checkpointer) createCheckpointSafe() {
	if err := c.CreateCheckpoint(); err != nil {
		return
	}
}

func (c *Checkpointer) TriggerCheckpoint() {
	select {
	case c.triggerCh <- struct{}{}:
	default:
	}
}

func (c *Checkpointer) CreateCheckpoint() error {
	if c.closed.Load() {
		return ErrCheckpointerClosed
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cp := c.buildCheckpoint()

	if err := c.collectState(cp); err != nil {
		return err
	}

	cp.Finalize()

	if err := c.writeCheckpoint(cp); err != nil {
		return err
	}

	c.lastCheckpoint = cp
	return c.cleanupOldCheckpoints()
}

func (c *Checkpointer) buildCheckpoint() *Checkpoint {
	var walSeq uint64
	if c.config.WAL != nil {
		walSeq = c.config.WAL.CurrentSequence()
	}

	return NewCheckpoint(uuid.New().String(), walSeq)
}

func (c *Checkpointer) collectState(cp *Checkpoint) error {
	if c.config.StateProvider == nil {
		return nil
	}
	return c.config.StateProvider.CollectState(cp)
}

func (c *Checkpointer) writeCheckpoint(cp *Checkpoint) error {
	data, err := cp.Serialize()
	if err != nil {
		return err
	}

	path, tmpPath := c.checkpointPaths(cp.CreatedAt)

	if err := c.writeTempCheckpoint(tmpPath, data); err != nil {
		return err
	}

	return c.finalizeCheckpoint(tmpPath, path)
}

func (c *Checkpointer) checkpointPaths(t time.Time) (string, string) {
	filename := c.checkpointFilename(t)
	path := filepath.Join(c.config.Dir, filename)
	return path, path + ".tmp"
}

func (c *Checkpointer) writeTempCheckpoint(tmpPath string, data []byte) error {
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write checkpoint: %w", err)
	}

	if _, err := DeserializeCheckpoint(data); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("checkpoint validation failed: %w", err)
	}

	return nil
}

func (c *Checkpointer) finalizeCheckpoint(tmpPath, path string) error {
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to finalize checkpoint: %w", err)
	}
	return nil
}

func (c *Checkpointer) checkpointFilename(t time.Time) string {
	return fmt.Sprintf("checkpoint-%s.json", t.Format("20060102-150405"))
}

func (c *Checkpointer) cleanupOldCheckpoints() error {
	files, err := c.listCheckpointFiles()
	if err != nil {
		return err
	}

	if len(files) <= c.config.MaxCheckpoints {
		return nil
	}

	toRemove := files[:len(files)-c.config.MaxCheckpoints]
	for _, file := range toRemove {
		path := filepath.Join(c.config.Dir, file)
		_ = os.Remove(path)
	}

	return nil
}

func (c *Checkpointer) listCheckpointFiles() ([]string, error) {
	entries, err := os.ReadDir(c.config.Dir)
	if err != nil {
		return nil, err
	}

	files := filterCheckpointFiles(entries)
	sort.Strings(files)
	return files, nil
}

func filterCheckpointFiles(entries []os.DirEntry) []string {
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && isCheckpointFile(entry.Name()) {
			files = append(files, entry.Name())
		}
	}
	return files
}

func isCheckpointFile(name string) bool {
	return strings.HasPrefix(name, "checkpoint-") && strings.HasSuffix(name, ".json")
}

func (c *Checkpointer) LoadLatest() (*Checkpoint, error) {
	if c.closed.Load() {
		return nil, ErrCheckpointerClosed
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	files, err := c.listCheckpointFiles()
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, ErrCheckpointNotFound
	}

	return c.loadCheckpointFile(files[len(files)-1])
}

func (c *Checkpointer) loadCheckpointFile(filename string) (*Checkpoint, error) {
	path := filepath.Join(c.config.Dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cp, err := DeserializeCheckpoint(data)
	if err != nil {
		return nil, err
	}

	if err := cp.Validate(); err != nil {
		return nil, err
	}

	return cp, nil
}

func (c *Checkpointer) LoadByID(id string) (*Checkpoint, error) {
	if c.closed.Load() {
		return nil, ErrCheckpointerClosed
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	files, err := c.listCheckpointFiles()
	if err != nil {
		return nil, err
	}

	return c.findCheckpointByID(files, id)
}

func (c *Checkpointer) findCheckpointByID(files []string, id string) (*Checkpoint, error) {
	for _, file := range files {
		cp, err := c.loadCheckpointFile(file)
		if err != nil {
			continue
		}
		if cp.ID == id {
			return cp, nil
		}
	}
	return nil, ErrCheckpointNotFound
}

func (c *Checkpointer) LastCheckpoint() *Checkpoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastCheckpoint
}

func (c *Checkpointer) Stop() error {
	if c.closed.Swap(true) {
		return ErrCheckpointerClosed
	}

	if c.running.Load() {
		close(c.stopCh)
		<-c.doneCh
	}

	return nil
}

type NoOpStateProvider struct{}

func (p *NoOpStateProvider) CollectState(_ *Checkpoint) error {
	return nil
}
