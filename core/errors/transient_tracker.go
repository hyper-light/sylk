package errors

import (
	"sync/atomic"
	"time"
	"unsafe"
)

type TransientTrackerConfig struct {
	FrequencyCount       int           `yaml:"frequency_count"`
	FrequencyWindow      time.Duration `yaml:"frequency_window"`
	NotificationCooldown time.Duration `yaml:"notification_cooldown"`
}

func DefaultTransientTrackerConfig() TransientTrackerConfig {
	return TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	}
}

const cacheLineSize = 64

type paddedTimestamp struct {
	value int64
	_     [cacheLineSize - 8]byte
}

type TransientTracker struct {
	config           TransientTrackerConfig
	timestamps       []paddedTimestamp
	head             atomic.Uint64
	lastNotification atomic.Int64
	windowNano       int64
	cooldownNano     int64
	mask             uint64
}

func NewTransientTracker(config TransientTrackerConfig) *TransientTracker {
	size := config.FrequencyCount * 2
	if size < 8 {
		size = 8
	}
	size = nextPowerOfTwo(size)

	return &TransientTracker{
		config:       config,
		timestamps:   make([]paddedTimestamp, size),
		windowNano:   config.FrequencyWindow.Nanoseconds(),
		cooldownNano: config.NotificationCooldown.Nanoseconds(),
		mask:         uint64(size - 1),
	}
}

func nextPowerOfTwo(n int) int {
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n++
	return n
}

func (t *TransientTracker) Record(_ error) bool {
	nowNano := time.Now().UnixNano()
	cutoff := nowNano - t.windowNano

	idx := t.head.Add(1) & t.mask
	atomic.StoreInt64(&t.timestamps[idx].value, nowNano)

	count := t.countInWindow(cutoff)
	return t.checkNotificationThreshold(count, nowNano)
}

func (t *TransientTracker) countInWindow(cutoff int64) int {
	count := 0
	for i := range t.timestamps {
		ts := atomic.LoadInt64(&t.timestamps[i].value)
		if ts > cutoff {
			count++
		}
	}
	return count
}

func (t *TransientTracker) checkNotificationThreshold(count int, nowNano int64) bool {
	if count < t.config.FrequencyCount {
		return false
	}

	for {
		lastNotif := t.lastNotification.Load()
		if nowNano-lastNotif < t.cooldownNano {
			return false
		}
		if t.lastNotification.CompareAndSwap(lastNotif, nowNano) {
			return true
		}
	}
}

func init() {
	_ = unsafe.Sizeof(paddedTimestamp{})
}
