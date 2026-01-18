package concurrency

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type ControllerState int32

const (
	ControllerStateRunning ControllerState = iota
	ControllerStateStopping
	ControllerStateStopped
	ControllerStatePausing
	ControllerStatePaused
	ControllerStateKilling
	ControllerStateKilled
)

func (s ControllerState) String() string {
	names := []string{"Running", "Stopping", "Stopped", "Pausing", "Paused", "Killing", "Killed"}
	if int(s) < len(names) {
		return names[s]
	}
	return "Unknown"
}

type PipelineControllerConfig struct {
	StopGracePeriod  time.Duration
	PauseTimeout     time.Duration
	KillGracePeriod  time.Duration
	KillHardDeadline time.Duration
}

func DefaultPipelineControllerConfig() PipelineControllerConfig {
	return PipelineControllerConfig{
		StopGracePeriod:  30 * time.Second,
		PauseTimeout:     5 * time.Second,
		KillGracePeriod:  2 * time.Second,
		KillHardDeadline: 5 * time.Second,
	}
}

type PipelineController struct {
	pipelineID    string
	supervisorsMu sync.RWMutex
	supervisors   map[string]*AgentSupervisor
	state         atomic.Int32
	config        PipelineControllerConfig
}

func NewPipelineController(pipelineID string, config PipelineControllerConfig) *PipelineController {
	config = normalizePipelineConfig(config)
	c := &PipelineController{
		pipelineID:  pipelineID,
		supervisors: make(map[string]*AgentSupervisor),
		config:      config,
	}
	c.state.Store(int32(ControllerStateRunning))
	return c
}

func normalizePipelineConfig(cfg PipelineControllerConfig) PipelineControllerConfig {
	defaults := DefaultPipelineControllerConfig()
	cfg.StopGracePeriod = normalizePipelineDuration(cfg.StopGracePeriod, defaults.StopGracePeriod)
	cfg.PauseTimeout = normalizePipelineDuration(cfg.PauseTimeout, defaults.PauseTimeout)
	cfg.KillGracePeriod = normalizePipelineDuration(cfg.KillGracePeriod, defaults.KillGracePeriod)
	cfg.KillHardDeadline = normalizePipelineDuration(cfg.KillHardDeadline, defaults.KillHardDeadline)
	return cfg
}

func normalizePipelineDuration(val, defaultVal time.Duration) time.Duration {
	if val <= 0 {
		return defaultVal
	}
	return val
}

func (c *PipelineController) RegisterSupervisor(agentID string, supervisor *AgentSupervisor) {
	c.supervisorsMu.Lock()
	defer c.supervisorsMu.Unlock()
	c.supervisors[agentID] = supervisor
}

func (c *PipelineController) UnregisterSupervisor(agentID string) {
	c.supervisorsMu.Lock()
	defer c.supervisorsMu.Unlock()
	delete(c.supervisors, agentID)
}

func (c *PipelineController) State() ControllerState {
	return ControllerState(c.state.Load())
}

func (c *PipelineController) PipelineID() string {
	return c.pipelineID
}

func (c *PipelineController) Stop(ctx context.Context) error {
	c.state.Store(int32(ControllerStateStopping))

	supervisors := c.getSupervisors()
	stopAcceptingWork(supervisors)

	deadline := time.Now().Add(c.config.StopGracePeriod)
	err := waitForAllCompletion(ctx, deadline, supervisors)

	c.state.Store(int32(ControllerStateStopped))
	return err
}

func (c *PipelineController) getSupervisors() []*AgentSupervisor {
	c.supervisorsMu.RLock()
	defer c.supervisorsMu.RUnlock()

	result := make([]*AgentSupervisor, 0, len(c.supervisors))
	for _, s := range c.supervisors {
		result = append(result, s)
	}
	return result
}

func stopAcceptingWork(supervisors []*AgentSupervisor) {
	for _, s := range supervisors {
		s.StopAcceptingWork()
	}
}

func waitForAllCompletion(
	ctx context.Context,
	deadline time.Time,
	supervisors []*AgentSupervisor,
) error {
	for _, s := range supervisors {
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		err := s.WaitForCompletion(waitCtx)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *PipelineController) Pause(ctx context.Context) error {
	c.state.Store(int32(ControllerStatePausing))

	supervisors := c.getSupervisors()
	signalPause(supervisors)

	deadline := time.Now().Add(c.config.PauseTimeout)
	waitForPauseAll(ctx, deadline, supervisors)

	c.state.Store(int32(ControllerStatePaused))
	return nil
}

func signalPause(supervisors []*AgentSupervisor) {
	for _, s := range supervisors {
		s.SignalPause()
	}
}

func waitForPauseAll(
	ctx context.Context,
	deadline time.Time,
	supervisors []*AgentSupervisor,
) {
	for _, s := range supervisors {
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		_ = s.WaitForPause(waitCtx)
		cancel()
	}
}

func (c *PipelineController) Resume(ctx context.Context) error {
	supervisors := c.getSupervisors()
	signalResume(supervisors)
	c.state.Store(int32(ControllerStateRunning))
	return nil
}

func signalResume(supervisors []*AgentSupervisor) {
	for _, s := range supervisors {
		s.SignalResume()
	}
}

func (c *PipelineController) Kill(ctx context.Context) error {
	c.state.Store(int32(ControllerStateKilling))

	supervisors := c.getSupervisors()
	cancelAll(supervisors)

	if c.waitForVoluntaryTermination(supervisors) {
		c.state.Store(int32(ControllerStateKilled))
		return nil
	}

	forceCloseResources(supervisors)

	if c.waitForHardDeadline(supervisors) {
		c.state.Store(int32(ControllerStateKilled))
		return nil
	}

	orphans := collectOrphans(supervisors)
	c.state.Store(int32(ControllerStateKilled))

	if len(orphans) > 0 {
		return &PipelineKillOrphansError{PipelineID: c.pipelineID, Orphans: orphans}
	}
	return nil
}

func cancelAll(supervisors []*AgentSupervisor) {
	for _, s := range supervisors {
		s.CancelAll()
	}
}

func (c *PipelineController) waitForVoluntaryTermination(
	supervisors []*AgentSupervisor,
) bool {
	allDone := waitForTerminationAsync(supervisors, c.config.KillGracePeriod)

	select {
	case allOk := <-allDone:
		return allOk
	case <-time.After(c.config.KillGracePeriod):
		return false
	}
}

func waitForTerminationAsync(
	supervisors []*AgentSupervisor,
	timeout time.Duration,
) <-chan bool {
	allDone := make(chan bool, 1)
	go func() {
		allOk := true
		for _, s := range supervisors {
			if !waitForTermination(s, timeout) {
				allOk = false
			}
		}
		allDone <- allOk
	}()
	return allDone
}

func waitForTermination(s *AgentSupervisor, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.WaitForCompletion(ctx) == nil
}

func forceCloseResources(supervisors []*AgentSupervisor) {
	for _, s := range supervisors {
		_ = s.ForceCloseResources()
	}
}

func (c *PipelineController) waitForHardDeadline(
	supervisors []*AgentSupervisor,
) bool {
	remaining := c.config.KillHardDeadline - c.config.KillGracePeriod
	if remaining <= 0 {
		return false
	}
	allDone := waitForTerminationAsync(supervisors, remaining)

	select {
	case allOk := <-allDone:
		return allOk
	case <-time.After(remaining):
		return false
	}
}

func collectOrphans(supervisors []*AgentSupervisor) []OrphanedOperation {
	var orphans []OrphanedOperation
	for _, s := range supervisors {
		orphans = append(orphans, s.MarkOrphansAndReport()...)
	}
	return orphans
}

func (c *PipelineController) SupervisorCount() int {
	c.supervisorsMu.RLock()
	defer c.supervisorsMu.RUnlock()
	return len(c.supervisors)
}

type PipelineKillOrphansError struct {
	PipelineID string
	Orphans    []OrphanedOperation
}

func (e *PipelineKillOrphansError) Error() string {
	return fmt.Sprintf(
		"pipeline %s killed with %d orphaned operations",
		e.PipelineID,
		len(e.Orphans),
	)
}
