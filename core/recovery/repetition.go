package recovery

import (
	"math"
	"sync"
	"time"
)

type RepetitionConfig struct {
	WindowSize     int
	MaxCycleLength int
	MinRepetitions int
}

func DefaultRepetitionConfig() RepetitionConfig {
	return RepetitionConfig{
		WindowSize:     50,
		MaxCycleLength: 5,
		MinRepetitions: 3,
	}
}

type Operation struct {
	Type      string
	Action    string
	Target    string
	Timestamp time.Time
	Hash      uint64
}

type OperationLog struct {
	agentID    string
	operations *RingBuffer[Operation]
	mu         sync.RWMutex
}

func (ol *OperationLog) Recent(n int) []Operation {
	ol.mu.RLock()
	defer ol.mu.RUnlock()
	return ol.operations.Last(n)
}

type RepetitionDetector struct {
	operationLogs sync.Map
	config        RepetitionConfig
}

func NewRepetitionDetector(config RepetitionConfig) *RepetitionDetector {
	return &RepetitionDetector{config: config}
}

func (r *RepetitionDetector) Record(agentID string, op Operation) {
	log := r.getOrCreateLog(agentID)
	log.mu.Lock()
	log.operations.Push(op)
	log.mu.Unlock()
}

func (r *RepetitionDetector) getOrCreateLog(agentID string) *OperationLog {
	if existing, ok := r.operationLogs.Load(agentID); ok {
		return existing.(*OperationLog)
	}

	log := &OperationLog{
		agentID:    agentID,
		operations: NewRingBuffer[Operation](r.config.WindowSize),
	}

	actual, _ := r.operationLogs.LoadOrStore(agentID, log)
	return actual.(*OperationLog)
}

func (r *RepetitionDetector) Score(agentID string) float64 {
	log, ok := r.operationLogs.Load(agentID)
	if !ok {
		return 1.0
	}

	ops := log.(*OperationLog).Recent(r.config.WindowSize)
	minOps := r.config.MaxCycleLength * r.config.MinRepetitions
	if len(ops) < minOps {
		return 1.0
	}

	return r.detectCycles(ops)
}

func (r *RepetitionDetector) detectCycles(ops []Operation) float64 {
	for cycleLen := 1; cycleLen <= r.config.MaxCycleLength; cycleLen++ {
		reps := r.countRepetitions(ops, cycleLen)
		if reps >= r.config.MinRepetitions {
			confidence := math.Min(float64(reps)/float64(r.config.MinRepetitions+3), 1.0)
			return 1.0 - confidence
		}
	}
	return 1.0
}

func (r *RepetitionDetector) countRepetitions(ops []Operation, cycleLen int) int {
	if len(ops) < cycleLen*2 {
		return 0
	}

	pattern := r.extractPattern(ops, cycleLen)
	return r.matchPatternBackward(ops, pattern, cycleLen)
}

func (r *RepetitionDetector) extractPattern(ops []Operation, cycleLen int) []uint64 {
	pattern := make([]uint64, cycleLen)
	start := len(ops) - cycleLen
	for i := 0; i < cycleLen; i++ {
		pattern[i] = ops[start+i].Hash
	}
	return pattern
}

func (r *RepetitionDetector) matchPatternBackward(ops []Operation, pattern []uint64, cycleLen int) int {
	repetitions := 1

	for i := len(ops) - cycleLen*2; i >= 0; i -= cycleLen {
		if !r.matchesPattern(ops, i, pattern, cycleLen) {
			break
		}
		repetitions++
	}
	return repetitions
}

func (r *RepetitionDetector) matchesPattern(ops []Operation, start int, pattern []uint64, cycleLen int) bool {
	for j := 0; j < cycleLen && start+j < len(ops); j++ {
		if ops[start+j].Hash != pattern[j] {
			return false
		}
	}
	return true
}

func (r *RepetitionDetector) RemoveAgent(agentID string) {
	r.operationLogs.Delete(agentID)
}
