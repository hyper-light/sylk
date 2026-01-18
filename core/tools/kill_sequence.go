package tools

import (
	"fmt"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultSIGINTGrace  = 5 * time.Second
	DefaultSIGTERMGrace = 3 * time.Second
)

type KillPhase int

const (
	KillPhaseSIGINT KillPhase = iota
	KillPhaseSIGTERM
	KillPhaseSIGKILL
	KillPhaseForceCloseResources
	KillPhaseOrphanTracking
)

type KillSequenceConfig struct {
	SIGINTGrace  time.Duration
	SIGTERMGrace time.Duration
}

func DefaultKillSequenceConfig() KillSequenceConfig {
	return KillSequenceConfig{
		SIGINTGrace:  DefaultSIGINTGrace,
		SIGTERMGrace: DefaultSIGTERMGrace,
	}
}

type KillResult struct {
	SentSIGINT  bool
	SentSIGTERM bool
	SentSIGKILL bool
	ExitedAfter string
	Duration    time.Duration
}

type KillSequenceManager struct {
	config KillSequenceConfig
}

func NewKillSequenceManager(cfg KillSequenceConfig) *KillSequenceManager {
	cfg = normalizeKillSequenceConfig(cfg)
	return &KillSequenceManager{config: cfg}
}

func normalizeKillSequenceConfig(cfg KillSequenceConfig) KillSequenceConfig {
	if cfg.SIGINTGrace == 0 {
		cfg.SIGINTGrace = DefaultSIGINTGrace
	}
	if cfg.SIGTERMGrace == 0 {
		cfg.SIGTERMGrace = DefaultSIGTERMGrace
	}
	return cfg
}

func (m *KillSequenceManager) Execute(pg *ProcessGroup, waitDone <-chan struct{}) KillResult {
	startTime := time.Now()
	result := KillResult{}

	if m.trySIGINT(pg, waitDone, &result) {
		return m.finishResult(result, startTime)
	}

	if m.trySIGTERM(pg, waitDone, &result) {
		return m.finishResult(result, startTime)
	}

	m.forceSIGKILL(pg, &result)
	return m.finishResult(result, startTime)
}

func (m *KillSequenceManager) trySIGINT(pg *ProcessGroup, waitDone <-chan struct{}, result *KillResult) bool {
	if pg != nil {
		pg.Signal(syscall.SIGINT)
	}
	result.SentSIGINT = true

	return m.waitForExit(waitDone, m.config.SIGINTGrace, "SIGINT", result)
}

func (m *KillSequenceManager) trySIGTERM(pg *ProcessGroup, waitDone <-chan struct{}, result *KillResult) bool {
	if pg != nil {
		pg.Signal(syscall.SIGTERM)
	}
	result.SentSIGTERM = true

	return m.waitForExit(waitDone, m.config.SIGTERMGrace, "SIGTERM", result)
}

func (m *KillSequenceManager) waitForExit(waitDone <-chan struct{}, grace time.Duration, signal string, result *KillResult) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-waitDone:
		result.ExitedAfter = signal
		return true
	case <-timer.C:
		return false
	}
}

func (m *KillSequenceManager) forceSIGKILL(pg *ProcessGroup, result *KillResult) {
	if pg != nil {
		pg.Kill()
	}
	result.SentSIGKILL = true
	result.ExitedAfter = "SIGKILL"
}

func (m *KillSequenceManager) ExecuteAsync(pg *ProcessGroup, done chan<- KillResult) {
	waitDone := make(chan struct{})
	go func() {
		pg.Wait()
		close(waitDone)
	}()

	result := m.Execute(pg, waitDone)
	done <- result
}

func (m *KillSequenceManager) finishResult(result KillResult, startTime time.Time) KillResult {
	result.Duration = time.Since(startTime)
	return result
}

type ProgressCallback func(stage string)

func (m *KillSequenceManager) ExecuteWithProgress(pg *ProcessGroup, waitDone <-chan struct{}, onProgress ProgressCallback) KillResult {
	startTime := time.Now()
	result := KillResult{}

	onProgress("stopping")
	if m.trySIGINT(pg, waitDone, &result) {
		return m.finishResult(result, startTime)
	}

	onProgress("force_stopping")
	if m.trySIGTERM(pg, waitDone, &result) {
		return m.finishResult(result, startTime)
	}

	onProgress("killed")
	m.forceSIGKILL(pg, &result)
	return m.finishResult(result, startTime)
}

func (m *KillSequenceManager) SIGINTGrace() time.Duration {
	return m.config.SIGINTGrace
}

func (m *KillSequenceManager) SIGTERMGrace() time.Duration {
	return m.config.SIGTERMGrace
}

func (m *KillSequenceManager) TotalGrace() time.Duration {
	return m.config.SIGINTGrace + m.config.SIGTERMGrace
}

type OrphanedProcessError struct {
	PID         int
	Description string
}

func (e *OrphanedProcessError) Error() string {
	return fmt.Sprintf("orphaned process: pid=%d desc=%s", e.PID, e.Description)
}

type OrphanTracker struct {
	mu       sync.RWMutex
	orphans  map[int]*OrphanEntry
	maxAge   time.Duration
	onOrphan func(entry *OrphanEntry)
}

type OrphanEntry struct {
	PID         int
	Description string
	TrackedAt   time.Time
	LastChecked time.Time
	StillAlive  bool
}

func NewOrphanTracker(maxAge time.Duration) *OrphanTracker {
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	return &OrphanTracker{
		orphans: make(map[int]*OrphanEntry),
		maxAge:  maxAge,
	}
}

func (t *OrphanTracker) Track(pid int, description string) *OrphanEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry := &OrphanEntry{
		PID:         pid,
		Description: description,
		TrackedAt:   time.Now(),
		LastChecked: time.Now(),
		StillAlive:  true,
	}
	t.orphans[pid] = entry

	if t.onOrphan != nil {
		t.onOrphan(entry)
	}

	return entry
}

func (t *OrphanTracker) Remove(pid int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.orphans, pid)
}

func (t *OrphanTracker) Get(pid int) (*OrphanEntry, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.orphans[pid]
	return entry, ok
}

func (t *OrphanTracker) List() []*OrphanEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	entries := make([]*OrphanEntry, 0, len(t.orphans))
	for _, e := range t.orphans {
		entries = append(entries, e)
	}
	return entries
}

func (t *OrphanTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.orphans)
}

func (t *OrphanTracker) OnOrphan(fn func(entry *OrphanEntry)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onOrphan = fn
}

type ExtendedKillResult struct {
	KillResult
	ForcedClose   bool
	OrphanTracked bool
	OrphanPID     int
	Phase         KillPhase
}

type ExtendedKillSequenceConfig struct {
	KillSequenceConfig
	ForceCloseWait time.Duration
	OrphanTracker  *OrphanTracker
}

func DefaultExtendedKillSequenceConfig() ExtendedKillSequenceConfig {
	return ExtendedKillSequenceConfig{
		KillSequenceConfig: DefaultKillSequenceConfig(),
		ForceCloseWait:     500 * time.Millisecond,
	}
}

type ExtendedKillSequenceManager struct {
	config        ExtendedKillSequenceConfig
	orphanTracker *OrphanTracker
}

func NewExtendedKillSequenceManager(cfg ExtendedKillSequenceConfig) *ExtendedKillSequenceManager {
	cfg.KillSequenceConfig = normalizeKillSequenceConfig(cfg.KillSequenceConfig)
	if cfg.ForceCloseWait <= 0 {
		cfg.ForceCloseWait = 500 * time.Millisecond
	}

	tracker := cfg.OrphanTracker
	if tracker == nil {
		tracker = NewOrphanTracker(30 * time.Minute)
	}

	return &ExtendedKillSequenceManager{
		config:        cfg,
		orphanTracker: tracker,
	}
}

func (m *ExtendedKillSequenceManager) ExecuteOnProcess(proc *TrackedProcess) (*ExtendedKillResult, error) {
	startTime := time.Now()
	result := &ExtendedKillResult{}

	if m.trySignalSequence(proc, result) {
		return m.finishExtendedResult(result, startTime), nil
	}

	if m.tryForceClose(proc, result) {
		return m.finishExtendedResult(result, startTime), nil
	}

	return m.handleOrphan(proc, result, startTime)
}

func (m *ExtendedKillSequenceManager) trySignalSequence(proc *TrackedProcess, result *ExtendedKillResult) bool {
	err := proc.ForceClose()
	if err == nil {
		result.Phase = KillPhaseSIGKILL
		result.SentSIGKILL = true
		result.ExitedAfter = "signal_sequence"
		return true
	}
	return false
}

func (m *ExtendedKillSequenceManager) tryForceClose(proc *TrackedProcess, result *ExtendedKillResult) bool {
	result.ForcedClose = true
	result.Phase = KillPhaseForceCloseResources

	select {
	case <-proc.Done():
		result.ExitedAfter = "force_close"
		return true
	case <-time.After(m.config.ForceCloseWait):
		return false
	}
}

func (m *ExtendedKillSequenceManager) handleOrphan(
	proc *TrackedProcess,
	result *ExtendedKillResult,
	startTime time.Time,
) (*ExtendedKillResult, error) {
	result.Phase = KillPhaseOrphanTracking
	result.OrphanTracked = true

	pid := proc.Pid()
	result.OrphanPID = pid

	m.orphanTracker.Track(pid, proc.ID())

	finalResult := m.finishExtendedResult(result, startTime)
	return finalResult, &OrphanedProcessError{PID: pid, Description: proc.ID()}
}

func (m *ExtendedKillSequenceManager) finishExtendedResult(result *ExtendedKillResult, startTime time.Time) *ExtendedKillResult {
	result.Duration = time.Since(startTime)
	return result
}

func (m *ExtendedKillSequenceManager) OrphanTracker() *OrphanTracker {
	return m.orphanTracker
}
