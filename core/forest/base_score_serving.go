package forest

import (
	"context"
	"sync/atomic"
)

// ────────────────────────────────────────────────────────────────────
// Issue #8 — base score serving cache + scoring resolution
// ────────────────────────────────────────────────────────────────────
//
// Hot read path. Every Retrieve resolves which model produces the
// base score for that retrieval — champion (default) or challenger
// (A/B sample). Reads use atomic.Pointer for lock-free access; writes
// are rare (only on promotion / training cycles).
//
// No code path panics. Nil champion / nil challenger fall back to
// hardcoded defaults; nil cache returns the hardcoded vector. The
// hardcoded fallback is the SAME vector that Layer 1 of Issue #8
// wraps via a learned model — cold-start preserves prior behavior.

// baseScoreCache holds the active champion + challenger models with
// atomic-pointer swap so the hot read path is lock-free. Nil pointers
// indicate "no model in this role" — the resolver falls through to
// hardcoded weights.
type baseScoreCache struct {
	champion   atomic.Pointer[BaseScoreModel]
	challenger atomic.Pointer[BaseScoreModel]
}

func newBaseScoreCache() *baseScoreCache {
	return &baseScoreCache{}
}

// Champion returns the current champion (may be nil).
func (c *baseScoreCache) Champion() *BaseScoreModel {
	if c == nil {
		return nil
	}
	return c.champion.Load()
}

// Challenger returns the current challenger (may be nil).
func (c *baseScoreCache) Challenger() *BaseScoreModel {
	if c == nil {
		return nil
	}
	return c.challenger.Load()
}

// SetChampion atomically replaces the champion pointer. Passing nil
// clears the slot — used during shutdown or when no champion is
// available yet.
func (c *baseScoreCache) SetChampion(model *BaseScoreModel) {
	if c == nil {
		return
	}
	c.champion.Store(model)
}

// SetChallenger atomically replaces the challenger pointer.
func (c *baseScoreCache) SetChallenger(model *BaseScoreModel) {
	if c == nil {
		return
	}
	c.challenger.Store(model)
}

// hydrateBaseScoreCache reads the active models from the store and
// installs them into the cache. Returns the loaded champion (or nil)
// so the caller can log / probe the result. A non-nil error means
// the store query failed — the trainer/init path treats this as a
// degraded-load and falls back to hardcoded defaults.
func (m *MemoryForest) hydrateBaseScoreCache(ctx context.Context) (*BaseScoreModel, error) {
	if m == nil || m.baseScoreStore == nil || m.baseScoreCache == nil {
		return nil, nil
	}
	champion, challenger, err := m.baseScoreStore.LoadActive(ctx)
	if err != nil {
		return nil, err
	}
	m.baseScoreCache.SetChampion(champion)
	m.baseScoreCache.SetChallenger(challenger)
	return champion, nil
}

// resolveRetrieveBaseScoreVariant picks the model + variant tag for
// one retrieval. A/B sampling: with probability baseScoreABRate, we
// route the retrieval through the challenger (when one exists);
// otherwise the champion. Cold-start (no champion): variant is
// "hardcoded" and the returned model is nil.
//
// Mutex-guarded RNG read because Retrieve runs concurrently across
// goroutines.
func (m *MemoryForest) resolveRetrieveBaseScoreVariant() (BaseScoreVariant, *BaseScoreModel) {
	if m == nil || m.baseScoreCache == nil {
		return BaseScoreVariantHardcoded, nil
	}
	champion := m.baseScoreCache.Champion()
	if champion == nil {
		return BaseScoreVariantHardcoded, nil
	}
	challenger := m.baseScoreCache.Challenger()
	if challenger == nil || m.baseScoreABRate <= 0 || m.baseScoreModeRng == nil {
		return BaseScoreVariantChampion, champion
	}
	m.baseScoreModeRngMu.Lock()
	roll := m.baseScoreModeRng.Float64()
	m.baseScoreModeRngMu.Unlock()
	if roll < m.baseScoreABRate {
		return BaseScoreVariantChallenger, challenger
	}
	return BaseScoreVariantChampion, champion
}

// baseScoreWeightsForModel returns the (weights, bias) pair to feed
// into scoreBatch. Nil model → hardcoded defaults. The returned
// weights slice is freshly allocated so callers can safely mutate
// (though scoreBatch never does).
func baseScoreWeightsForModel(model *BaseScoreModel) ([]float32, float64) {
	if model == nil {
		out := make([]float32, len(defaultScoreWeights))
		copy(out, defaultScoreWeights)
		return out, 0
	}
	out := make([]float32, BaseScoreComponentCount)
	for i := 0; i < BaseScoreComponentCount; i++ {
		out[i] = float32(model.Weights[i])
	}
	return out, model.Bias
}
