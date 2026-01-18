package tools

import (
	"syscall"
	"time"
)

const (
	DefaultSIGINTGrace  = 5 * time.Second
	DefaultSIGTERMGrace = 3 * time.Second
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
