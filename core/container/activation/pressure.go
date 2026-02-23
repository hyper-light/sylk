package activation

import (
	"math"
	"sort"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
)

// PressureConfig controls resource pressure evaluation parameters.
type PressureConfig struct {
	// ThresholdFraction is the utilization fraction (0.0–1.0) above
	// which the system is considered under resource pressure.
	// Derived default: 0.80, from the standard headroom principle —
	// systems need ~20% free capacity for burst absorption.
	ThresholdFraction float64

	// RecencyHalfLife controls the exponential decay of the recency
	// weight in eviction scoring. More recently active entries are
	// harder to evict. Derived from the idle-to-warm threshold:
	// half-life = IdleToWarm / 2, so entries idle for one full
	// IdleToWarm period have ~25% of their initial recency weight.
	RecencyHalfLife time.Duration
}

// PressureEvaluator determines eviction candidates when resource
// utilization exceeds budget thresholds.
type PressureEvaluator struct {
	budget *concurrency.GoroutineBudget
	quota  *container.ResourceQuota
	config PressureConfig
}

// EvictionCandidate pairs an activation entry with a priority score.
// Lower score = evict first.
type EvictionCandidate struct {
	Entry *ActivationEntry
	Score float64
}

// NewPressureEvaluator creates an evaluator. Either budget or quota
// may be nil — pressure is evaluated against whichever is provided.
// Zero-value config fields receive defaults.
func NewPressureEvaluator(budget *concurrency.GoroutineBudget, quota *container.ResourceQuota, cfg PressureConfig) *PressureEvaluator {
	applyPressureDefaults(&cfg)
	return &PressureEvaluator{
		budget: budget,
		quota:  quota,
		config: cfg,
	}
}

// applyPressureDefaults fills zero-value fields.
func applyPressureDefaults(cfg *PressureConfig) {
	if cfg.ThresholdFraction <= 0 || cfg.ThresholdFraction > 1.0 {
		cfg.ThresholdFraction = 0.80
	}
	if cfg.RecencyHalfLife <= 0 {
		cfg.RecencyHalfLife = 30 * time.Second
	}
}

// IsUnderPressure returns true if goroutine or container utilization
// exceeds the pressure threshold.
func (pe *PressureEvaluator) IsUnderPressure() bool {
	if pe.budget != nil {
		utilization := pe.goroutineUtilization()
		if utilization >= pe.config.ThresholdFraction {
			return true
		}
	}
	if pe.quota != nil {
		usage := pe.quota.Usage()
		if usage.ContainerLimit > 0 {
			containerUtil := float64(usage.ContainerCount) / float64(usage.ContainerLimit)
			if containerUtil >= pe.config.ThresholdFraction {
				return true
			}
		}
	}
	return false
}

// Evaluate returns eviction candidates ordered by score (lowest first).
// Daemon entries and entries at or below their MinTier are excluded.
func (pe *PressureEvaluator) Evaluate(entries []*ActivationEntry) []EvictionCandidate {
	candidates := make([]EvictionCandidate, 0, len(entries))
	now := time.Now().UnixNano()

	for _, entry := range entries {
		if entry.Policy.IsDaemon {
			continue
		}
		tier := entry.LoadTier()
		if tier <= entry.Policy.MinTier {
			continue
		}
		score := pe.computeScore(entry, now)
		candidates = append(candidates, EvictionCandidate{
			Entry: entry,
			Score: score,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score < candidates[j].Score
	})
	return candidates
}

// computeScore produces a combined priority + recency score.
// Higher priority and more recent activity produce higher scores
// (harder to evict).
func (pe *PressureEvaluator) computeScore(entry *ActivationEntry, nowNano int64) float64 {
	priorityWeight := float64(entry.Policy.Priority)

	lastActive := entry.LastActive.Load()
	idleNanos := float64(nowNano - lastActive)

	// Recency weight: exponential decay with configurable half-life.
	// ln(2) ≈ 0.693
	halfLifeNanos := float64(pe.config.RecencyHalfLife)
	recencyWeight := math.Exp(-0.693 * idleNanos / halfLifeNanos)

	return priorityWeight + recencyWeight
}

func (pe *PressureEvaluator) goroutineUtilization() float64 {
	total := pe.budget.TotalActive()
	limit := pe.budget.SystemLimit()
	if limit == 0 {
		return 0
	}
	return float64(total) / float64(limit)
}
