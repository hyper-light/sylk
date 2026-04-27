package forest

// ────────────────────────────────────────────────────────────────────
// Issue #7 — Substrate mode selection + A/B sampling
// ────────────────────────────────────────────────────────────────────
//
// Per-retrieval mode resolution lives here so every Retrieve goes
// through one decision point. The dominant mode (`substrateMode`) is
// used unless `substrateABRate` fires the per-call dice — in which
// case we uniformly pick from the non-dominant modes.
//
// The choice is captured in the audit so SubstrateModeStatsSince can
// roll up per-mode outcomes weeks later. Pure measurement; the actual
// substrate computation dispatches off the chosen mode in
// loadSubstrateSignals.

// resolveRetrieveSubstrateMode returns the substrate mode for one
// Retrieve call. Mutex-guarded RNG read because Retrieve is
// concurrent.
func (m *MemoryForest) resolveRetrieveSubstrateMode() SubstrateMode {
	dominant := m.substrateMode
	if dominant == "" {
		dominant = defaultSubstrateMode
	}
	if m.substrateABRate <= 0 || m.substrateModeRng == nil {
		return dominant
	}
	m.substrateModeRngMu.Lock()
	defer m.substrateModeRngMu.Unlock()
	if m.substrateModeRng.Float64() >= m.substrateABRate {
		return dominant
	}
	// Fire the A/B swap. Pick uniformly from the modes != dominant.
	candidates := nonDominantSubstrateModes(dominant)
	if len(candidates) == 0 {
		return dominant
	}
	return candidates[m.substrateModeRng.Intn(len(candidates))]
}

// nonDominantSubstrateModes returns the canonical mode set with the
// dominant mode filtered out. The list is small + fixed; allocation
// per-call is cheap and avoids a global mutable.
func nonDominantSubstrateModes(dominant SubstrateMode) []SubstrateMode {
	all := []SubstrateMode{
		SubstrateModeFull,
		SubstrateModePageRank,
		SubstrateModeWarmthOnly,
	}
	out := make([]SubstrateMode, 0, len(all)-1)
	for _, mode := range all {
		if mode == dominant {
			continue
		}
		out = append(out, mode)
	}
	return out
}
