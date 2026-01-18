package resources

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
)

type PipelineScheduler interface {
	Schedule(ctx context.Context, sessionID string, p *concurrency.SchedulablePipeline) (bool, error)
}

type AdmissionController struct {
	scheduler PipelineScheduler
	admitting atomic.Bool

	mu     sync.Mutex
	queued []*queuedPipeline
}

type queuedPipeline struct {
	ctx       context.Context
	sessionID string
	pipeline  *concurrency.SchedulablePipeline
}

func NewAdmissionController(scheduler PipelineScheduler) *AdmissionController {
	ac := &AdmissionController{
		scheduler: scheduler,
		queued:    make([]*queuedPipeline, 0),
	}
	ac.admitting.Store(true)
	return ac
}

func (ac *AdmissionController) TryAdmit(ctx context.Context, sessionID string, p *concurrency.SchedulablePipeline) bool {
	if ac.admitting.Load() {
		_, err := ac.scheduler.Schedule(ctx, sessionID, p)
		return err == nil
	}

	ac.addToQueue(ctx, sessionID, p)
	return false
}

func (ac *AdmissionController) addToQueue(ctx context.Context, sessionID string, p *concurrency.SchedulablePipeline) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	ac.queued = append(ac.queued, &queuedPipeline{
		ctx:       ctx,
		sessionID: sessionID,
		pipeline:  p,
	})

	sortQueueByPriority(ac.queued)
}

func sortQueueByPriority(queue []*queuedPipeline) {
	for i := 1; i < len(queue); i++ {
		for j := i; j > 0 && queue[j].pipeline.Priority > queue[j-1].pipeline.Priority; j-- {
			queue[j], queue[j-1] = queue[j-1], queue[j]
		}
	}
}

func (ac *AdmissionController) Stop() {
	ac.admitting.Store(false)
}

func (ac *AdmissionController) Resume() {
	ac.admitting.Store(true)
	ac.drainQueue()
}

func (ac *AdmissionController) drainQueue() {
	ac.mu.Lock()
	toSchedule := ac.queued
	ac.queued = make([]*queuedPipeline, 0)
	ac.mu.Unlock()

	for _, qp := range toSchedule {
		_, _ = ac.scheduler.Schedule(qp.ctx, qp.sessionID, qp.pipeline)
	}
}

func (ac *AdmissionController) QueueLength() int {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return len(ac.queued)
}

func (ac *AdmissionController) IsAdmitting() bool {
	return ac.admitting.Load()
}
