package session

import (
	"maps"
	"sync"
	"time"
)

const (
	DefaultUserReservedPercent = 0.20
	DefaultIdleBaseline        = 1
	DefaultRebalanceInterval   = 500 * time.Millisecond
	DefaultSignificantChange   = 0.10
)

type FairShareConfig struct {
	UserReservedPercent float64
	IdleBaseline        int
	RebalanceInterval   time.Duration
	SignificantChange   float64
}

func DefaultFairShareConfig() FairShareConfig {
	return FairShareConfig{
		UserReservedPercent: DefaultUserReservedPercent,
		IdleBaseline:        DefaultIdleBaseline,
		RebalanceInterval:   DefaultRebalanceInterval,
		SignificantChange:   DefaultSignificantChange,
	}
}

type SessionAllocation struct {
	SessionID       string
	SubprocessSlots int
	FileHandleSlots int
	NetworkSlots    int
	MemoryBudget    int64
}

type ResourceTotals struct {
	SubprocessSlots int
	FileHandleSlots int
	NetworkSlots    int
	MemoryBudget    int64
}

type allocationTotals struct {
	forActive ResourceTotals
}

type FairShareCalculator struct {
	registry *Registry
	config   FairShareConfig

	mu             sync.RWMutex
	lastAllocation map[string]SessionAllocation
}

func NewFairShareCalculator(registry *Registry, cfg FairShareConfig) *FairShareCalculator {
	cfg = normalizeFairShareConfig(cfg)
	return &FairShareCalculator{
		registry:       registry,
		config:         cfg,
		lastAllocation: make(map[string]SessionAllocation),
	}
}

func normalizeFairShareConfig(cfg FairShareConfig) FairShareConfig {
	cfg.UserReservedPercent = defaultFloat(cfg.UserReservedPercent, DefaultUserReservedPercent)
	cfg.IdleBaseline = defaultInt(cfg.IdleBaseline, DefaultIdleBaseline)
	cfg.RebalanceInterval = defaultIfZeroDuration(cfg.RebalanceInterval, DefaultRebalanceInterval)
	cfg.SignificantChange = defaultFloat(cfg.SignificantChange, DefaultSignificantChange)
	return cfg
}

func defaultFloat(val, def float64) float64 {
	if val <= 0 {
		return def
	}
	return val
}

func defaultInt(val, def int) int {
	if val <= 0 {
		return def
	}
	return val
}

func (f *FairShareCalculator) Calculate(totals ResourceTotals) (map[string]SessionAllocation, error) {
	sessions, err := f.registry.GetActiveSessions()
	if err != nil {
		return nil, err
	}

	if len(sessions) == 0 {
		return make(map[string]SessionAllocation), nil
	}

	activeSessions, idleSessions := f.classifySessions(sessions)
	return f.computeAllocations(totals, activeSessions, idleSessions), nil
}

func (f *FairShareCalculator) classifySessions(sessions []SessionRecord) ([]SessionRecord, []SessionRecord) {
	var active, idle []SessionRecord
	for _, s := range sessions {
		if s.ActivityScore > 0 {
			active = append(active, s)
		} else {
			idle = append(idle, s)
		}
	}
	return active, idle
}

func (f *FairShareCalculator) computeAllocations(totals ResourceTotals, active, idle []SessionRecord) map[string]SessionAllocation {
	allocations := make(map[string]SessionAllocation)
	allocationTotals := f.prepareAllocationTotals(totals, len(idle))

	totalScore := f.sumActivityScores(active)

	f.allocateActiveSessions(allocations, active, allocationTotals.forActive, totalScore)
	f.allocateIdleSessions(allocations, idle)

	return allocations
}

func (f *FairShareCalculator) prepareAllocationTotals(totals ResourceTotals, idleCount int) allocationTotals {
	userReserved := f.computeUserReserved(totals)
	remaining := f.subtractReserved(totals, userReserved)
	idleReserved := f.computeIdleReserved(remaining, idleCount)

	return allocationTotals{
		forActive: f.subtractReserved(remaining, idleReserved),
	}
}

func (f *FairShareCalculator) computeUserReserved(totals ResourceTotals) ResourceTotals {
	pct := f.config.UserReservedPercent
	return ResourceTotals{
		SubprocessSlots: int(float64(totals.SubprocessSlots) * pct),
		FileHandleSlots: int(float64(totals.FileHandleSlots) * pct),
		NetworkSlots:    int(float64(totals.NetworkSlots) * pct),
		MemoryBudget:    int64(float64(totals.MemoryBudget) * pct),
	}
}

func (f *FairShareCalculator) subtractReserved(total, reserved ResourceTotals) ResourceTotals {
	return ResourceTotals{
		SubprocessSlots: maxInt(0, total.SubprocessSlots-reserved.SubprocessSlots),
		FileHandleSlots: maxInt(0, total.FileHandleSlots-reserved.FileHandleSlots),
		NetworkSlots:    maxInt(0, total.NetworkSlots-reserved.NetworkSlots),
		MemoryBudget:    maxInt64(0, total.MemoryBudget-reserved.MemoryBudget),
	}
}

func (f *FairShareCalculator) computeIdleReserved(remaining ResourceTotals, idleCount int) ResourceTotals {
	baseline := f.config.IdleBaseline
	return ResourceTotals{
		SubprocessSlots: minInt(idleCount*baseline, remaining.SubprocessSlots),
		FileHandleSlots: minInt(idleCount*baseline, remaining.FileHandleSlots),
		NetworkSlots:    minInt(idleCount*baseline, remaining.NetworkSlots),
		MemoryBudget:    minInt64(int64(idleCount*baseline)*1024*1024, remaining.MemoryBudget),
	}
}

func (f *FairShareCalculator) sumActivityScores(sessions []SessionRecord) float64 {
	var total float64
	for _, s := range sessions {
		total += s.ActivityScore
	}
	return total
}

func (f *FairShareCalculator) allocateActiveSessions(allocs map[string]SessionAllocation, active []SessionRecord, available ResourceTotals, totalScore float64) {
	for _, s := range active {
		share := f.calculateShare(s.ActivityScore, totalScore)
		allocs[s.SessionID] = f.buildAllocation(s.SessionID, available, share)
	}
}

func (f *FairShareCalculator) calculateShare(score, totalScore float64) float64 {
	if totalScore <= 0 {
		return 1.0
	}
	return score / totalScore
}

func (f *FairShareCalculator) buildAllocation(sessionID string, available ResourceTotals, share float64) SessionAllocation {
	return SessionAllocation{
		SessionID:       sessionID,
		SubprocessSlots: maxInt(1, int(float64(available.SubprocessSlots)*share)),
		FileHandleSlots: maxInt(1, int(float64(available.FileHandleSlots)*share)),
		NetworkSlots:    maxInt(1, int(float64(available.NetworkSlots)*share)),
		MemoryBudget:    maxInt64(1024*1024, int64(float64(available.MemoryBudget)*share)),
	}
}

func (f *FairShareCalculator) allocateIdleSessions(allocs map[string]SessionAllocation, idle []SessionRecord) {
	baseline := f.config.IdleBaseline
	for _, s := range idle {
		allocs[s.SessionID] = SessionAllocation{
			SessionID:       s.SessionID,
			SubprocessSlots: baseline,
			FileHandleSlots: baseline,
			NetworkSlots:    baseline,
			MemoryBudget:    int64(baseline) * 1024 * 1024,
		}
	}
}

func (f *FairShareCalculator) HasSignificantChange(newAllocs map[string]SessionAllocation) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.lastAllocation) != len(newAllocs) {
		return true
	}

	return f.anyAllocationChanged(newAllocs)
}

func (f *FairShareCalculator) anyAllocationChanged(newAllocs map[string]SessionAllocation) bool {
	for id, newAlloc := range newAllocs {
		oldAlloc, exists := f.lastAllocation[id]
		if !exists || f.allocationChanged(oldAlloc, newAlloc) {
			return true
		}
	}
	return false
}

func (f *FairShareCalculator) allocationChanged(old, new SessionAllocation) bool {
	threshold := f.config.SignificantChange
	return f.percentChange(old.SubprocessSlots, new.SubprocessSlots) > threshold
}

func (f *FairShareCalculator) percentChange(old, new int) float64 {
	if old == 0 {
		if new == 0 {
			return 0
		}
		return 1.0
	}
	diff := float64(new - old)
	return abs(diff / float64(old))
}

func (f *FairShareCalculator) UpdateLastAllocation(allocs map[string]SessionAllocation) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastAllocation = make(map[string]SessionAllocation, len(allocs))
	maps.Copy(f.lastAllocation, allocs)
}

func (f *FairShareCalculator) GetLastAllocation() map[string]SessionAllocation {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make(map[string]SessionAllocation, len(f.lastAllocation))
	maps.Copy(result, f.lastAllocation)
	return result
}

func (f *FairShareCalculator) RebalanceInterval() time.Duration {
	return f.config.RebalanceInterval
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
