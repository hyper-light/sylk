package quantization

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type RemoveBirthAdapter struct {
	config          AdaptationConfig
	tracker         AdaptationTracker
	codebookGetter  func(string) *ProductQuantizer
	codebookUpdater func(string, *ProductQuantizer) error
	running         atomic.Bool
	stopCh          chan struct{}
	doneCh          chan struct{}
	onDeath         func(DeathEvent)
	onBirth         func(BirthEvent)
	banditConfig    *BanditConfig
	mu              sync.RWMutex
}

func NewRemoveBirthAdapter(config AdaptationConfig, tracker AdaptationTracker) (*RemoveBirthAdapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &RemoveBirthAdapter{
		config:  config,
		tracker: tracker,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}, nil
}

func (a *RemoveBirthAdapter) SetCodebookGetter(fn func(string) *ProductQuantizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codebookGetter = fn
}

func (a *RemoveBirthAdapter) SetCodebookUpdater(fn func(string, *ProductQuantizer) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codebookUpdater = fn
}

func (a *RemoveBirthAdapter) OnDeath(fn func(DeathEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onDeath = fn
}

func (a *RemoveBirthAdapter) OnBirth(fn func(BirthEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onBirth = fn
}

func (a *RemoveBirthAdapter) Start(ctx context.Context) error {
	if !a.config.Enabled {
		return ErrAdaptationDisabled
	}
	if !a.running.CompareAndSwap(false, true) {
		return nil
	}

	a.stopCh = make(chan struct{})
	a.doneCh = make(chan struct{})

	go a.runLoop(ctx)
	return nil
}

func (a *RemoveBirthAdapter) Stop() {
	if !a.running.CompareAndSwap(true, false) {
		return
	}
	close(a.stopCh)
	<-a.doneCh
}

func (a *RemoveBirthAdapter) IsRunning() bool {
	return a.running.Load()
}

func (a *RemoveBirthAdapter) runLoop(ctx context.Context) {
	defer close(a.doneCh)

	ticker := time.NewTicker(a.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.checkAndAdapt(ctx)
		}
	}
}

func (a *RemoveBirthAdapter) checkAndAdapt(ctx context.Context) {
	a.mu.RLock()
	getter := a.codebookGetter
	a.mu.RUnlock()

	if getter == nil {
		return
	}

	codebookTypes := []string{"global"}
	for _, cbType := range codebookTypes {
		select {
		case <-ctx.Done():
			return
		default:
		}

		a.processDeath(ctx, cbType)
		a.processBirth(ctx, cbType)
	}
}

func (a *RemoveBirthAdapter) processDeath(ctx context.Context, codebookType string) {
	deadCentroids, err := a.tracker.GetDeadCentroids(codebookType)
	if err != nil || len(deadCentroids) == 0 {
		return
	}

	getter, updater, onDeath := a.getCallbacks()
	if getter == nil {
		return
	}

	pq := getter(codebookType)
	if pq == nil || !pq.IsTrained() {
		return
	}

	for _, centroidID := range deadCentroids {
		if ctx.Err() != nil {
			return
		}
		a.processDeathSingle(pq, centroidID, codebookType, updater, onDeath)
	}
}

func (a *RemoveBirthAdapter) getCallbacks() (func(string) *ProductQuantizer, func(string, *ProductQuantizer) error, func(DeathEvent)) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.codebookGetter, a.codebookUpdater, a.onDeath
}

func (a *RemoveBirthAdapter) processDeathSingle(pq *ProductQuantizer, centroidID CentroidID, codebookType string, updater func(string, *ProductQuantizer) error, onDeath func(DeathEvent)) {
	if pq.CentroidsPerSubspace() <= a.config.MinCentroidsPerSubspace {
		return
	}

	util, err := a.tracker.GetUtilization(centroidID)
	if err != nil {
		return
	}

	replacement := a.findNearestCentroid(pq, centroidID)
	event := DeathEvent{
		Centroid:            centroidID,
		FinalCount:          util.Count,
		Threshold:           a.config.DeathThreshold,
		OccurredAt:          time.Now(),
		ReplacementCentroid: replacement,
	}

	if updater != nil {
		a.removeCentroid(pq, centroidID, replacement)
		updater(codebookType, pq)
	}

	a.tracker.RecordDeath(event)

	if onDeath != nil {
		onDeath(event)
	}
}

func (a *RemoveBirthAdapter) processBirth(ctx context.Context, codebookType string) {
	overloaded, err := a.tracker.GetOverloadedCentroids(codebookType)
	if err != nil || len(overloaded) == 0 {
		return
	}

	getter, updater, onBirth := a.getBirthCallbacks()
	if getter == nil {
		return
	}

	pq := getter(codebookType)
	if pq == nil || !pq.IsTrained() {
		return
	}

	for _, centroidID := range overloaded {
		if ctx.Err() != nil {
			return
		}
		a.processBirthSingle(pq, centroidID, codebookType, updater, onBirth)
	}
}

func (a *RemoveBirthAdapter) getBirthCallbacks() (func(string) *ProductQuantizer, func(string, *ProductQuantizer) error, func(BirthEvent)) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.codebookGetter, a.codebookUpdater, a.onBirth
}

func (a *RemoveBirthAdapter) processBirthSingle(pq *ProductQuantizer, centroidID CentroidID, codebookType string, updater func(string, *ProductQuantizer) error, onBirth func(BirthEvent)) {
	if pq.CentroidsPerSubspace() >= a.config.MaxCentroidsPerSubspace {
		return
	}

	util, err := a.tracker.GetUtilization(centroidID)
	if err != nil {
		return
	}

	childID := a.splitCentroid(pq, centroidID)
	if childID.CentroidIndex < 0 {
		return
	}

	event := BirthEvent{
		ParentCentroid:    centroidID,
		ChildCentroid:     childID,
		ParentCountBefore: util.Count,
		Threshold:         a.config.BirthThreshold,
		OccurredAt:        time.Now(),
	}

	if updater != nil {
		updater(codebookType, pq)
	}

	a.tracker.RecordBirth(event)

	if onBirth != nil {
		onBirth(event)
	}
}

func (a *RemoveBirthAdapter) findNearestCentroid(pq *ProductQuantizer, dead CentroidID) CentroidID {
	centroids := pq.Centroids()
	if centroids == nil || dead.SubspaceIndex >= len(centroids) {
		return CentroidID{}
	}

	subCentroids := centroids[dead.SubspaceIndex]
	if dead.CentroidIndex >= len(subCentroids) {
		return CentroidID{}
	}

	deadVec := subCentroids[dead.CentroidIndex]
	minDist := float32(1e30)
	minIdx := -1

	for i, c := range subCentroids {
		if i == dead.CentroidIndex {
			continue
		}
		dist := subspaceDistance(deadVec, c)
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}

	if minIdx < 0 {
		return CentroidID{}
	}

	return CentroidID{
		CodebookType:  dead.CodebookType,
		SubspaceIndex: dead.SubspaceIndex,
		CentroidIndex: minIdx,
	}
}

func (a *RemoveBirthAdapter) removeCentroid(pq *ProductQuantizer, dead CentroidID, replacement CentroidID) {
	centroids := pq.Centroids()
	if centroids == nil {
		return
	}
	if dead.SubspaceIndex >= len(centroids) {
		return
	}
	if dead.CentroidIndex >= len(centroids[dead.SubspaceIndex]) {
		return
	}
	if replacement.CentroidIndex < 0 || replacement.CentroidIndex >= len(centroids[dead.SubspaceIndex]) {
		return
	}

	copy(centroids[dead.SubspaceIndex][dead.CentroidIndex],
		centroids[dead.SubspaceIndex][replacement.CentroidIndex])
	pq.buildEncodingCache()
}

func (a *RemoveBirthAdapter) splitCentroid(pq *ProductQuantizer, parent CentroidID) CentroidID {
	centroids := pq.Centroids()
	if centroids == nil {
		return CentroidID{CentroidIndex: -1}
	}
	if parent.SubspaceIndex >= len(centroids) {
		return CentroidID{CentroidIndex: -1}
	}

	subCentroids := centroids[parent.SubspaceIndex]
	if parent.CentroidIndex >= len(subCentroids) {
		return CentroidID{CentroidIndex: -1}
	}

	parentVec := subCentroids[parent.CentroidIndex]
	perturbation := a.getSplitPerturbation()

	childVec := make([]float32, len(parentVec))
	for i := range parentVec {
		if i%2 == 0 {
			childVec[i] = parentVec[i] * (1.0 + perturbation)
			parentVec[i] *= (1.0 - perturbation)
		} else {
			childVec[i] = parentVec[i] * (1.0 - perturbation)
			parentVec[i] *= (1.0 + perturbation)
		}
	}

	newIndex := len(subCentroids)
	centroids[parent.SubspaceIndex] = append(subCentroids, childVec)
	pq.buildEncodingCache()

	return CentroidID{
		CodebookType:  parent.CodebookType,
		SubspaceIndex: parent.SubspaceIndex,
		CentroidIndex: newIndex,
	}
}

func (a *RemoveBirthAdapter) getSplitPerturbation() float32 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.banditConfig != nil {
		return a.banditConfig.SplitPerturbation
	}
	return DefaultBanditConfig().SplitPerturbation
}

func (a *RemoveBirthAdapter) SetBanditConfig(config *BanditConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.banditConfig = config
}

func (a *RemoveBirthAdapter) IncrementUtilization(id CentroidID) error {
	return a.tracker.Increment(id)
}

func (a *RemoveBirthAdapter) DecrementUtilization(id CentroidID) error {
	return a.tracker.Decrement(id)
}

func (a *RemoveBirthAdapter) GetStats() AdaptationStats {
	return a.tracker.GetStats()
}

func (a *RemoveBirthAdapter) ForceCheck(ctx context.Context) {
	a.checkAndAdapt(ctx)
}
