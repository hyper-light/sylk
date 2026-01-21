package quantization

import (
	"sync"
	"time"
)

// InMemoryUtilizationTracker implements AdaptationTracker with in-memory storage.
type InMemoryUtilizationTracker struct {
	config      AdaptationConfig
	utilization map[string]*CentroidUtilization
	deathEvents []DeathEvent
	birthEvents []BirthEvent
	mu          sync.RWMutex
	lastCheckAt time.Time
}

// NewInMemoryUtilizationTracker creates an in-memory utilization tracker.
func NewInMemoryUtilizationTracker(config AdaptationConfig) (*InMemoryUtilizationTracker, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &InMemoryUtilizationTracker{
		config:      config,
		utilization: make(map[string]*CentroidUtilization),
	}, nil
}

func (t *InMemoryUtilizationTracker) centroidKey(id CentroidID) string {
	return id.String()
}

func (t *InMemoryUtilizationTracker) Increment(id CentroidID) error {
	if !t.config.Enabled {
		return ErrAdaptationDisabled
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := t.centroidKey(id)
	util, ok := t.utilization[key]
	if !ok {
		util = &CentroidUtilization{ID: id}
		t.utilization[key] = util
	}

	util.Count++
	util.LastUpdated = time.Now()

	return nil
}

func (t *InMemoryUtilizationTracker) Decrement(id CentroidID) error {
	if !t.config.Enabled {
		return ErrAdaptationDisabled
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := t.centroidKey(id)
	util, ok := t.utilization[key]
	if !ok {
		return ErrAdaptationCentroidNotFound
	}

	if util.Count > 0 {
		util.Count--
	}
	util.LastUpdated = time.Now()

	return nil
}

func (t *InMemoryUtilizationTracker) GetUtilization(id CentroidID) (CentroidUtilization, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	key := t.centroidKey(id)
	util, ok := t.utilization[key]
	if !ok {
		return CentroidUtilization{}, ErrAdaptationCentroidNotFound
	}

	return *util, nil
}

func (t *InMemoryUtilizationTracker) GetDeadCentroids(codebookType string) ([]CentroidID, error) {
	if !t.config.Enabled {
		return nil, ErrAdaptationDisabled
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []CentroidID
	for _, util := range t.utilization {
		if util.ID.CodebookType == codebookType && util.IsDead(t.config.DeathThreshold) {
			result = append(result, util.ID)
		}
	}

	return result, nil
}

func (t *InMemoryUtilizationTracker) GetOverloadedCentroids(codebookType string) ([]CentroidID, error) {
	if !t.config.Enabled {
		return nil, ErrAdaptationDisabled
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []CentroidID
	for _, util := range t.utilization {
		if util.ID.CodebookType == codebookType && util.IsOverloaded(t.config.BirthThreshold) {
			result = append(result, util.ID)
		}
	}

	return result, nil
}

func (t *InMemoryUtilizationTracker) RecordDeath(event DeathEvent) error {
	if !t.config.Enabled {
		return ErrAdaptationDisabled
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.deathEvents = append(t.deathEvents, event)

	key := t.centroidKey(event.Centroid)
	delete(t.utilization, key)

	return nil
}

func (t *InMemoryUtilizationTracker) RecordBirth(event BirthEvent) error {
	if !t.config.Enabled {
		return ErrAdaptationDisabled
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.birthEvents = append(t.birthEvents, event)

	childKey := t.centroidKey(event.ChildCentroid)
	t.utilization[childKey] = &CentroidUtilization{
		ID:          event.ChildCentroid,
		Count:       0,
		LastUpdated: event.OccurredAt,
	}

	return nil
}

func (t *InMemoryUtilizationTracker) GetRecentEvents(since time.Time) ([]DeathEvent, []BirthEvent, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var deaths []DeathEvent
	for _, e := range t.deathEvents {
		if e.OccurredAt.After(since) {
			deaths = append(deaths, e)
		}
	}

	var births []BirthEvent
	for _, e := range t.birthEvents {
		if e.OccurredAt.After(since) {
			births = append(births, e)
		}
	}

	return deaths, births, nil
}

func (t *InMemoryUtilizationTracker) GetStats() AdaptationStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := AdaptationStats{
		TotalCentroids: len(t.utilization),
		TotalDeaths:    int64(len(t.deathEvents)),
		TotalBirths:    int64(len(t.birthEvents)),
		LastCheckAt:    t.lastCheckAt,
	}

	for _, util := range t.utilization {
		if util.IsDead(t.config.DeathThreshold) {
			stats.DeadCentroids++
		}
		if util.IsOverloaded(t.config.BirthThreshold) {
			stats.OverloadedCentroids++
		}
	}

	return stats
}

// SetUtilization directly sets the utilization count for a centroid.
func (t *InMemoryUtilizationTracker) SetUtilization(id CentroidID, count int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := t.centroidKey(id)
	t.utilization[key] = &CentroidUtilization{
		ID:          id,
		Count:       count,
		LastUpdated: time.Now(),
	}
}

// MarkChecked updates the last check timestamp.
func (t *InMemoryUtilizationTracker) MarkChecked() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastCheckAt = time.Now()
}

// GetAllUtilizations returns all tracked utilizations.
func (t *InMemoryUtilizationTracker) GetAllUtilizations() []CentroidUtilization {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]CentroidUtilization, 0, len(t.utilization))
	for _, util := range t.utilization {
		result = append(result, *util)
	}
	return result
}

// Clear removes all tracked data.
func (t *InMemoryUtilizationTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.utilization = make(map[string]*CentroidUtilization)
	t.deathEvents = nil
	t.birthEvents = nil
}

var _ AdaptationTracker = (*InMemoryUtilizationTracker)(nil)
