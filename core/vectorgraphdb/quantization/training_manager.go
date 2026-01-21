package quantization

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type TrainingManager struct {
	trainer    *LocalCodebookTrainer
	queue      TrainingQueue
	config     TrainingQueueConfig
	running    atomic.Bool
	stopCh     chan struct{}
	doneCh     chan struct{}
	onComplete func(PartitionID, *LocalCodebook, error)
	mu         sync.RWMutex
}

func NewTrainingManager(
	trainer *LocalCodebookTrainer,
	queue TrainingQueue,
	config TrainingQueueConfig,
) (*TrainingManager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &TrainingManager{
		trainer: trainer,
		queue:   queue,
		config:  config,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}, nil
}

func (m *TrainingManager) SetCompletionCallback(fn func(PartitionID, *LocalCodebook, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onComplete = fn
}

func (m *TrainingManager) Start(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return nil
	}

	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})

	go m.runLoop(ctx)
	return nil
}

func (m *TrainingManager) Stop() {
	if !m.running.CompareAndSwap(true, false) {
		return
	}
	close(m.stopCh)
	<-m.doneCh
}

func (m *TrainingManager) IsRunning() bool {
	return m.running.Load()
}

func (m *TrainingManager) runLoop(ctx context.Context) {
	defer close(m.doneCh)

	ticker := time.NewTicker(m.config.PollInterval)
	defer ticker.Stop()

	sem := make(chan struct{}, m.config.MaxConcurrent)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-m.stopCh:
			wg.Wait()
			return
		case <-ticker.C:
			m.processJobs(ctx, sem, &wg)
		}
	}
}

func (m *TrainingManager) processJobs(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup) {
	for {
		select {
		case sem <- struct{}{}:
		default:
			return
		}

		job, err := m.queue.Dequeue(ctx)
		if err != nil || job == nil {
			<-sem
			return
		}

		wg.Add(1)
		go func(j *TrainingJob) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					m.queue.Fail(ctx, j.ID, fmt.Errorf("panic during training: %v", r))
				}
			}()
			m.executeJob(ctx, j)
		}(job)
	}
}

func (m *TrainingManager) executeJob(ctx context.Context, job *TrainingJob) {
	if job.Type != TrainingJobLOPQLocal {
		m.queue.Fail(ctx, job.ID, ErrTrainingQueueInvalidTransition)
		return
	}

	partition := PartitionID(job.PartitionID)
	codebook, err := m.trainer.TrainPartition(ctx, partition)

	if err != nil {
		m.queue.Fail(ctx, job.ID, err)
		m.notifyCompletion(partition, nil, err)
		return
	}

	m.queue.Complete(ctx, job.ID, codebook.VectorCount)
	m.notifyCompletion(partition, codebook, nil)
}

func (m *TrainingManager) notifyCompletion(partition PartitionID, codebook *LocalCodebook, err error) {
	m.mu.RLock()
	callback := m.onComplete
	m.mu.RUnlock()

	if callback != nil {
		callback(partition, codebook, err)
	}
}

func (m *TrainingManager) SchedulePartition(ctx context.Context, partition PartitionID, priority JobPriority) error {
	hasPending, err := m.queue.HasPendingForPartition(ctx, int(partition))
	if err != nil {
		return err
	}
	if hasPending {
		return nil
	}

	job := NewTrainingJob(TrainingJobLOPQLocal, int(partition), priority)
	_, err = m.queue.Enqueue(ctx, job)
	return err
}

func (m *TrainingManager) ScheduleReadyPartitions(ctx context.Context, priority JobPriority) (int, error) {
	ready := m.trainer.GetPartitionsReadyForTraining()
	scheduled := 0

	for _, partition := range ready {
		if err := m.SchedulePartition(ctx, partition, priority); err != nil {
			continue
		}
		scheduled++
	}

	return scheduled, nil
}

func (m *TrainingManager) GetQueueStats(ctx context.Context) (TrainingQueueStats, error) {
	return m.queue.GetStats(ctx)
}

func (m *TrainingManager) Cleanup(ctx context.Context) (int, error) {
	return m.queue.Cleanup(ctx, m.config.RetentionPeriod)
}
