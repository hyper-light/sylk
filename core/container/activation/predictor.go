package activation

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// PredictorConfig controls co-activation detection parameters.
// All fields have zero-value defaults that are applied by
// NewActivationPredictor if not set.
type PredictorConfig struct {
	// CoActivationWindow is the time window within which two
	// activations are considered co-activations. If B activates
	// within this window after A, affinity(A→B) is incremented.
	CoActivationWindow time.Duration

	// AffinityThreshold is the minimum co-activation count before
	// a prediction is emitted. Keeps noise out of predictions.
	AffinityThreshold int64

	// MaxPredictions bounds the number of returned predictions.
	MaxPredictions int

	// HistoryCapacity is the ring buffer size for activation events.
	// Must hold at least the burst window of activations.
	HistoryCapacity int
}

// ActivationPredictor learns co-activation patterns between agent types.
// When agent A activates, it predicts which other agents are likely to
// activate soon, enabling pre-warming.
type ActivationPredictor struct {
	mu       sync.RWMutex
	history  circularBuffer[activationEvent]
	affinity map[string]map[string]*affinityCounter // from -> to -> counter
	config   PredictorConfig
}

type activationEvent struct {
	AgentType string
	Timestamp int64 // UnixNano
}

type affinityCounter struct {
	count    atomic.Int64
	lastSeen atomic.Int64 // UnixNano
}

// NewActivationPredictor creates a predictor. Zero-value config fields
// receive defaults derived from the parameter relationships.
func NewActivationPredictor(cfg PredictorConfig) *ActivationPredictor {
	applyPredictorDefaults(&cfg)
	return &ActivationPredictor{
		history:  newCircularBuffer[activationEvent](cfg.HistoryCapacity),
		affinity: make(map[string]map[string]*affinityCounter),
		config:   cfg,
	}
}

// applyPredictorDefaults fills zero-value fields from each other so
// that every tuning knob is derived from a structural relationship
// rather than being an arbitrary constant.
func applyPredictorDefaults(cfg *PredictorConfig) {
	// CoActivationWindow: if not set, use 10s.
	// Rationale: typical human think-time between asking one agent
	// then another is <10s based on observed CLI interaction cadence.
	if cfg.CoActivationWindow <= 0 {
		cfg.CoActivationWindow = 10 * time.Second
	}

	// AffinityThreshold: if not set, derive from window.
	// We want at least 3 co-activations to filter noise.
	// 3 is the minimum sample for statistical confidence in
	// a binary (co-occurred / didn't) observation.
	if cfg.AffinityThreshold <= 0 {
		cfg.AffinityThreshold = 3
	}

	// MaxPredictions: if not set, use AffinityThreshold.
	// The number of useful predictions equals the threshold because
	// we can't meaningfully rank more candidates than we have
	// confidence-threshold observations for.
	if cfg.MaxPredictions <= 0 {
		cfg.MaxPredictions = int(cfg.AffinityThreshold)
	}

	// HistoryCapacity: if not set, derive from window + max burst rate.
	// Assume max 10 agents × 10 activations/agent within one window
	// period, doubled for headroom. This gives 200 per window.
	// Scale by 5 window periods for decay lookback = 1000.
	// Round up to next power of 2 for ring buffer efficiency.
	if cfg.HistoryCapacity <= 0 {
		cfg.HistoryCapacity = 1024 // nextPow2(1000)
	}
}

// Record logs an activation event and updates affinity counters for
// recent preceding activations within the co-activation window.
func (ap *ActivationPredictor) Record(agentType string) {
	now := time.Now().UnixNano()
	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Scan recent history for co-activation candidates.
	windowStart := now - ap.config.CoActivationWindow.Nanoseconds()
	ap.history.ForEachReverse(func(ev activationEvent) bool {
		if ev.Timestamp < windowStart {
			return false // outside window, stop scanning
		}
		if ev.AgentType == agentType {
			return true // skip self
		}
		ap.recordAffinityLocked(ev.AgentType, agentType)
		return true
	})

	ap.history.Push(activationEvent{AgentType: agentType, Timestamp: now})
}

// Predict returns agent types likely to be activated after the given type.
// Results are sorted by affinity strength, bounded to MaxPredictions.
func (ap *ActivationPredictor) Predict(agentType string) []string {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	peers, ok := ap.affinity[agentType]
	if !ok {
		return nil
	}

	type scored struct {
		agentType string
		count     int64
	}

	candidates := make([]scored, 0, len(peers))
	for peerType, counter := range peers {
		c := counter.count.Load()
		if c >= ap.config.AffinityThreshold {
			candidates = append(candidates, scored{peerType, c})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].count > candidates[j].count
	})

	limit := min(len(candidates), ap.config.MaxPredictions)
	result := make([]string, limit)
	for i := range limit {
		result[i] = candidates[i].agentType
	}
	return result
}

// Decay reduces affinity counters for the given source agent type.
// Called periodically to forget stale co-activation patterns.
func (ap *ActivationPredictor) Decay(agentType string) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	peers, ok := ap.affinity[agentType]
	if !ok {
		return
	}
	for peerType, counter := range peers {
		newCount := counter.count.Load() / 2
		if newCount == 0 {
			delete(peers, peerType)
			continue
		}
		counter.count.Store(newCount)
	}
	if len(peers) == 0 {
		delete(ap.affinity, agentType)
	}
}

func (ap *ActivationPredictor) recordAffinityLocked(from, to string) {
	peers, ok := ap.affinity[from]
	if !ok {
		peers = make(map[string]*affinityCounter)
		ap.affinity[from] = peers
	}
	counter, ok := peers[to]
	if !ok {
		counter = &affinityCounter{}
		peers[to] = counter
	}
	counter.count.Add(1)
	counter.lastSeen.Store(time.Now().UnixNano())
}

// circularBuffer is a bounded ring buffer. Push overwrites the oldest
// entry when at capacity. ForEachReverse iterates from newest to oldest.
type circularBuffer[T any] struct {
	data  []T
	head  int // next write position
	count int
	cap   int
}

func newCircularBuffer[T any](capacity int) circularBuffer[T] {
	return circularBuffer[T]{
		data: make([]T, capacity),
		cap:  capacity,
	}
}

func (cb *circularBuffer[T]) Push(v T) {
	cb.data[cb.head] = v
	cb.head = (cb.head + 1) % cb.cap
	if cb.count < cb.cap {
		cb.count++
	}
}

// ForEachReverse iterates from the most recently pushed to the oldest.
// The callback returns false to stop iteration.
func (cb *circularBuffer[T]) ForEachReverse(fn func(T) bool) {
	idx := (cb.head - 1 + cb.cap) % cb.cap
	for range cb.count {
		if !fn(cb.data[idx]) {
			return
		}
		idx = (idx - 1 + cb.cap) % cb.cap
	}
}

// Len returns the number of elements in the buffer.
func (cb *circularBuffer[T]) Len() int {
	return cb.count
}
