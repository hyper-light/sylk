package forest

import (
	"context"
	"sync/atomic"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #9 Phase B.4 — ModelCalibration cache
// ────────────────────────────────────────────────────────────────────
//
// Single value (1 - ECE) cached at runtime so the hot Retrieve path
// can stamp it onto every BranchPacket without recomputing the
// snapshot. Refreshed lazily on first read after model promotion or
// after the staleness window elapses.
//
// Atomic-pointer load/store so the read path is lock-free. No
// goroutine; refresh runs synchronously the first time stale data
// is observed in a Retrieve call.

const (
	// calibrationCacheStaleWindow is how long a cached value is
	// served before refresh. 1h amortizes the per-Retrieve cost
	// (one CalibrationSince call per hour, not per request).
	calibrationCacheStaleWindow = 1 * time.Hour
)

// calibrationCacheEntry holds the most recent ModelCalibration
// computation and the wall-clock time it was computed. atomic.Pointer
// swap is the write path; readers Load() lock-free.
type calibrationCacheEntry struct {
	value       float64
	computedAt  time.Time
	modelVersion int64
}

// calibrationCache wraps an atomic.Pointer so updates publish
// atomically and reads never see a torn value. Nil pointer = no
// data yet (cold-start); reader gets 0 and the next refresh attempt
// will populate.
type calibrationCache struct {
	entry atomic.Pointer[calibrationCacheEntry]
}

func newCalibrationCache() *calibrationCache {
	return &calibrationCache{}
}

// load returns the cached value + a fresh-flag. fresh=false means
// the cache is empty or older than calibrationCacheStaleWindow —
// callers may trigger a refresh.
func (c *calibrationCache) load(now time.Time) (float64, int64, bool) {
	if c == nil {
		return 0, 0, false
	}
	entry := c.entry.Load()
	if entry == nil {
		return 0, 0, false
	}
	if now.Sub(entry.computedAt) >= calibrationCacheStaleWindow {
		return entry.value, entry.modelVersion, false
	}
	return entry.value, entry.modelVersion, true
}

// store atomically replaces the cached entry.
func (c *calibrationCache) store(value float64, modelVersion int64, computedAt time.Time) {
	if c == nil {
		return
	}
	c.entry.Store(&calibrationCacheEntry{
		value:        value,
		computedAt:   computedAt,
		modelVersion: modelVersion,
	})
}

// activeModelCalibration returns the value to stamp on every
// BranchPacket.Score.ModelCalibration. Reads the cache; if stale,
// recomputes via BaseScoreCalibrationSince inline. Bounded cost:
// the recompute query is window-limited to defaultCalibrationWindow
// (7d) so it returns in milliseconds.
//
// On any error path (cache nil, recompute fails, no samples in
// window), returns 0 — meaning "no calibration data; treat
// predictions as uncalibrated." Operators see the actual ECE via
// BoosterCalibrationSince / BaseScoreCalibrationSince.
func (m *MemoryForest) activeModelCalibration(ctx context.Context) float64 {
	if m == nil || m.calibrationCache == nil {
		return 0
	}
	now := time.Now().UTC()
	value, _, fresh := m.calibrationCache.load(now)
	if fresh {
		return value
	}
	refreshed := m.refreshActiveModelCalibration(ctx, now)
	if refreshed != 0 {
		return refreshed
	}
	// Refresh failed or no samples. Fall back to the stale cached
	// value (better than zeroing on every retrieval if we have any
	// signal at all).
	return value
}

// refreshActiveModelCalibration recomputes the base-score
// calibration over the default window and stores the result. Returns
// the new value (or 0 if no samples / no champion). Errors are
// swallowed because the retrieval path doesn't surface them — the
// per-advisory annotation is best-effort.
func (m *MemoryForest) refreshActiveModelCalibration(ctx context.Context, now time.Time) float64 {
	since := now.Add(-defaultCalibrationWindow)
	snapshot, err := m.BaseScoreCalibrationSince(ctx, since)
	if err != nil || snapshot.SampleCount == 0 {
		return 0
	}
	calibration := 1.0 - snapshot.ECE
	if calibration < 0 {
		calibration = 0
	}
	if calibration > 1 {
		calibration = 1
	}
	m.calibrationCache.store(calibration, snapshot.ModelVersion, now)
	return calibration
}
