package quantization

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type CodebookSwapper struct {
	codebooks     atomic.Pointer[map[PartitionID]*LocalCodebook]
	versions      map[PartitionID]uint64
	swapCallbacks []func(PartitionID, *LocalCodebook)
	mu            sync.RWMutex
}

func NewCodebookSwapper() *CodebookSwapper {
	cs := &CodebookSwapper{
		versions: make(map[PartitionID]uint64),
	}
	initialMap := make(map[PartitionID]*LocalCodebook)
	cs.codebooks.Store(&initialMap)
	return cs
}

func (cs *CodebookSwapper) Get(partition PartitionID) *LocalCodebook {
	codebooks := cs.codebooks.Load()
	if codebooks == nil {
		return nil
	}
	return (*codebooks)[partition]
}

func (cs *CodebookSwapper) Swap(codebook *LocalCodebook) bool {
	if codebook == nil {
		return false
	}

	cs.mu.Lock()
	currentVersion := cs.versions[codebook.PartitionID]
	if codebook.Version <= currentVersion {
		cs.mu.Unlock()
		return false
	}

	for {
		oldMap := cs.codebooks.Load()
		newMap := make(map[PartitionID]*LocalCodebook, len(*oldMap)+1)
		for k, v := range *oldMap {
			newMap[k] = v
		}
		newMap[codebook.PartitionID] = codebook

		if cs.codebooks.CompareAndSwap(oldMap, &newMap) {
			cs.versions[codebook.PartitionID] = codebook.Version
			break
		}
	}
	cs.mu.Unlock()

	cs.notifySwap(codebook.PartitionID, codebook)
	return true
}

func (cs *CodebookSwapper) OnSwap(callback func(PartitionID, *LocalCodebook)) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.swapCallbacks = append(cs.swapCallbacks, callback)
}

func (cs *CodebookSwapper) notifySwap(partition PartitionID, codebook *LocalCodebook) {
	cs.mu.RLock()
	callbacks := make([]func(PartitionID, *LocalCodebook), len(cs.swapCallbacks))
	copy(callbacks, cs.swapCallbacks)
	cs.mu.RUnlock()

	for _, cb := range callbacks {
		cb(partition, codebook)
	}
}

func (cs *CodebookSwapper) GetVersion(partition PartitionID) uint64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.versions[partition]
}

func (cs *CodebookSwapper) GetAll() map[PartitionID]*LocalCodebook {
	codebooks := cs.codebooks.Load()
	if codebooks == nil {
		return make(map[PartitionID]*LocalCodebook)
	}

	result := make(map[PartitionID]*LocalCodebook, len(*codebooks))
	for k, v := range *codebooks {
		result[k] = v
	}
	return result
}

func (cs *CodebookSwapper) Count() int {
	codebooks := cs.codebooks.Load()
	if codebooks == nil {
		return 0
	}
	return len(*codebooks)
}

type VectorReencoder struct {
	swapper      *CodebookSwapper
	coarse       *CoarseQuantizer
	pending      map[PartitionID][]string
	reencodeFunc func(context.Context, PartitionID, []string) error
	banditConfig *BanditConfig
	wg           sync.WaitGroup
	mu           sync.Mutex
}

func NewVectorReencoder(swapper *CodebookSwapper, coarse *CoarseQuantizer) *VectorReencoder {
	re := &VectorReencoder{
		swapper: swapper,
		coarse:  coarse,
		pending: make(map[PartitionID][]string),
	}

	swapper.OnSwap(func(partition PartitionID, _ *LocalCodebook) {
		re.scheduleReencode(partition)
	})

	return re
}

func (re *VectorReencoder) scheduleReencode(partition PartitionID) {
	re.mu.Lock()
	vectorIDs := re.pending[partition]
	delete(re.pending, partition)
	reencodeFunc := re.reencodeFunc
	timeout := re.getReencodeTimeoutLocked()
	re.mu.Unlock()

	if len(vectorIDs) == 0 || reencodeFunc == nil {
		return
	}

	re.wg.Add(1)
	go func() {
		defer re.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		reencodeFunc(ctx, partition, vectorIDs)
	}()
}

func (re *VectorReencoder) Wait() {
	re.wg.Wait()
}

func (re *VectorReencoder) SetReencodeFunc(fn func(context.Context, PartitionID, []string) error) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.reencodeFunc = fn
}

func (re *VectorReencoder) RegisterVector(vectorID string, partition PartitionID) {
	re.mu.Lock()
	defer re.mu.Unlock()

	if re.swapper.Get(partition) != nil {
		return
	}
	re.pending[partition] = append(re.pending[partition], vectorID)
}

func (re *VectorReencoder) getReencodeTimeoutLocked() time.Duration {
	if re.banditConfig != nil {
		return re.banditConfig.ReencodeTimeout
	}
	return DefaultBanditConfig().ReencodeTimeout
}

func (re *VectorReencoder) SetBanditConfig(config *BanditConfig) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.banditConfig = config
}

func (re *VectorReencoder) GetPendingCount(partition PartitionID) int {
	re.mu.Lock()
	defer re.mu.Unlock()
	return len(re.pending[partition])
}

func (re *VectorReencoder) GetTotalPending() int {
	re.mu.Lock()
	defer re.mu.Unlock()

	total := 0
	for _, ids := range re.pending {
		total += len(ids)
	}
	return total
}
