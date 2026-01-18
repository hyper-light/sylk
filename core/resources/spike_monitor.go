package resources

import (
	"context"
	"runtime"
	"sync"
	"time"
)

type Sample struct {
	Timestamp time.Time
	HeapAlloc uint64
	Usage     float64
	Trend     float64
}

type SpikeMonitorConfig struct {
	Interval       time.Duration
	SpikeThreshold float64
	SampleCount    int
	MemoryLimit    int64
}

func DefaultSpikeMonitorConfig() SpikeMonitorConfig {
	return SpikeMonitorConfig{
		Interval:       200 * time.Millisecond,
		SpikeThreshold: 0.15,
		SampleCount:    30,
		MemoryLimit:    0,
	}
}

type SampleCallback func(Sample)
type SpikeCallback func(Sample)

type SpikeMonitor struct {
	config    SpikeMonitorConfig
	limit     int64
	mu        sync.RWMutex
	samples   []Sample
	sampleIdx int
	onSample  SampleCallback
	onSpike   SpikeCallback
}

func NewSpikeMonitor(config SpikeMonitorConfig) *SpikeMonitor {
	config = applyConfigDefaults(config)
	limit := resolveMemoryLimit(config.MemoryLimit)

	return &SpikeMonitor{
		config:  config,
		limit:   limit,
		samples: make([]Sample, config.SampleCount),
	}
}

func applyConfigDefaults(config SpikeMonitorConfig) SpikeMonitorConfig {
	if config.SampleCount <= 0 {
		config.SampleCount = 30
	}
	if config.Interval <= 0 {
		config.Interval = 200 * time.Millisecond
	}
	if config.SpikeThreshold <= 0 {
		config.SpikeThreshold = 0.15
	}
	return config
}

func resolveMemoryLimit(limit int64) int64 {
	if limit > 0 {
		return limit
	}
	return calculateDefaultLimit()
}

func calculateDefaultLimit() int64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return int64(float64(stats.Sys) * 0.75)
}

func (m *SpikeMonitor) SetOnSample(cb SampleCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSample = cb
}

func (m *SpikeMonitor) SetOnSpike(cb SpikeCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSpike = cb
}

func (m *SpikeMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick()
		}
	}
}

func (m *SpikeMonitor) tick() {
	sample := m.takeSample()
	m.invokeOnSample(sample)

	if m.isSpike(sample) {
		m.invokeOnSpike(sample)
	}
}

func (m *SpikeMonitor) takeSample() Sample {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	m.mu.Lock()
	defer m.mu.Unlock()

	sample := m.buildSample(stats.HeapAlloc)
	m.storeSample(sample)
	return sample
}

func (m *SpikeMonitor) buildSample(heapAlloc uint64) Sample {
	sample := Sample{
		Timestamp: time.Now(),
		HeapAlloc: heapAlloc,
		Usage:     float64(heapAlloc) / float64(m.limit),
	}

	prevIdx := (m.sampleIdx - 1 + len(m.samples)) % len(m.samples)
	prev := m.samples[prevIdx]
	if prev.HeapAlloc > 0 {
		sample.Trend = m.calculateTrend(heapAlloc, prev.HeapAlloc)
	}

	return sample
}

func (m *SpikeMonitor) calculateTrend(current, previous uint64) float64 {
	return float64(int64(current)-int64(previous)) / float64(previous)
}

func (m *SpikeMonitor) storeSample(sample Sample) {
	m.samples[m.sampleIdx] = sample
	m.sampleIdx = (m.sampleIdx + 1) % len(m.samples)
}

func (m *SpikeMonitor) invokeOnSample(sample Sample) {
	m.mu.RLock()
	cb := m.onSample
	m.mu.RUnlock()

	if cb != nil {
		cb(sample)
	}
}

func (m *SpikeMonitor) invokeOnSpike(sample Sample) {
	m.mu.RLock()
	cb := m.onSpike
	m.mu.RUnlock()

	if cb != nil {
		cb(sample)
	}
}

func (m *SpikeMonitor) isSpike(s Sample) bool {
	return s.Trend > m.config.SpikeThreshold
}

func (m *SpikeMonitor) Limit() int64 {
	return m.limit
}

func (m *SpikeMonitor) LastSample() Sample {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prevIdx := (m.sampleIdx - 1 + len(m.samples)) % len(m.samples)
	return m.samples[prevIdx]
}

func (m *SpikeMonitor) RecentSamples(n int) []Sample {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if n > len(m.samples) {
		n = len(m.samples)
	}

	result := make([]Sample, n)
	for i := 0; i < n; i++ {
		idx := (m.sampleIdx - n + i + len(m.samples)) % len(m.samples)
		result[i] = m.samples[idx]
	}
	return result
}

func (m *SpikeMonitor) AverageUsage(n int) float64 {
	samples := m.RecentSamples(n)
	total, count := sumValidUsage(samples)
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func sumValidUsage(samples []Sample) (total float64, count int) {
	for _, s := range samples {
		if s.HeapAlloc > 0 {
			total += s.Usage
			count++
		}
	}
	return total, count
}
