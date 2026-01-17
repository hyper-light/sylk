package backup

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/database"
)

type Scheduler struct {
	manager   *Manager
	interval  time.Duration
	pools     map[string]*database.Pool
	mu        sync.RWMutex
	stopCh    chan struct{}
	stoppedCh chan struct{}
	running   bool
}

func NewScheduler(manager *Manager, interval time.Duration) *Scheduler {
	return &Scheduler{
		manager:   manager,
		interval:  interval,
		pools:     make(map[string]*database.Pool),
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

func (s *Scheduler) Register(scope string, pool *database.Pool) {
	s.mu.Lock()
	s.pools[scope] = pool
	s.mu.Unlock()
}

func (s *Scheduler) Unregister(scope string) {
	s.mu.Lock()
	delete(s.pools, scope)
	s.mu.Unlock()
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go s.run()
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)
	<-s.stoppedCh
}

func (s *Scheduler) run() {
	defer close(s.stoppedCh)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.backupAll()
		}
	}
}

func (s *Scheduler) backupAll() {
	s.mu.RLock()
	pools := make(map[string]*database.Pool, len(s.pools))
	for k, v := range s.pools {
		pools[k] = v
	}
	s.mu.RUnlock()

	ctx := context.Background()
	for scope, pool := range pools {
		_, _ = s.manager.Backup(ctx, pool, scope)
	}
}

func (s *Scheduler) TriggerNow() {
	go s.backupAll()
}
