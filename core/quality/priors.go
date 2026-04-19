package quality

// Domain identifies the agent family whose quality priors apply to a given
// query. Priors diverge sharply across domains — Librarian weights
// staleness heavily; Academic weights authority; Archivalist weights
// prior usage feedback. SCORING.md §2 lists the full matrix.
//
// This type is intentionally duplicated from the broader agent taxonomy in
// core/agents/identity so the quality package stays free of that dep —
// quality only cares about the four retrieval-relevant domain buckets.
type Domain uint8

const (
	// DomainUnknown is used when the caller hasn't classified the query;
	// the priors fall back to balanced high-uncertainty values.
	DomainUnknown Domain = iota
	// DomainCode corresponds to Librarian — local code is ground truth,
	// staleness is the dominant signal.
	DomainCode
	// DomainAcademic corresponds to Academic — authority and citations
	// matter; staleness is gentle.
	DomainAcademic
	// DomainHistory corresponds to Archivalist — past usage feedback is
	// the strongest signal; temporal decay is mild.
	DomainHistory
)

// String returns the stable domain label. Used in WAL records and
// diagnostics; changing a returned string breaks replay of older logs.
func (d Domain) String() string {
	switch d {
	case DomainCode:
		return "code"
	case DomainAcademic:
		return "academic"
	case DomainHistory:
		return "history"
	default:
		return "unknown"
	}
}

// DomainFromString is the inverse of String(). Returns (DomainUnknown, false)
// for unrecognized labels so WAL replay can tolerate future domain values
// rather than panic.
func DomainFromString(s string) (Domain, bool) {
	switch s {
	case "code":
		return DomainCode, true
	case "academic":
		return DomainAcademic, true
	case "history":
		return DomainHistory, true
	case "unknown":
		return DomainUnknown, true
	default:
		return DomainUnknown, false
	}
}

// PriorQualityWeights returns the weakly-informative Beta priors for a
// domain. These are the starting posteriors before any observations arrive;
// session overlays fork from these and accumulate domain-specific drift.
//
// Every posterior is initialized with PriorAlpha/PriorBeta set to its own
// alpha/beta so Decay() can blend back to the prior without a separate
// table lookup.
//
// The numbers here reproduce SCORING.md §2 "Prior Weights by Agent Domain"
// verbatim — changing them changes agent retrieval behavior and must be
// coordinated with the docs. Prior means appear as trailing comments for
// quick grep (`grep "Prior mean" core/quality/priors.go`).
func PriorQualityWeights(domain Domain) *QualityWeights {
	switch domain {
	case DomainCode:
		return codePriors()
	case DomainAcademic:
		return academicPriors()
	case DomainHistory:
		return historyPriors()
	default:
		return balancedPriors()
	}
}

// codePriors: Librarian — staleness matters more, authority N/A for local code.
func codePriors() *QualityWeights {
	return &QualityWeights{
		VectorWeight:         NewLearnedWeight(7, 13),  // Prior mean 0.35
		GraphWeight:          NewLearnedWeight(3, 17),  // Prior mean 0.15
		BleveWeight:          NewLearnedWeight(5, 15),  // Prior mean 0.25
		TrustLevelWeight:     NewLearnedWeight(1, 19),  // Prior mean 0.05
		StalenessWeight:      NewLearnedWeight(5, 15),  // Prior mean 0.25 — high for code
		UsageFeedbackWeight:  NewLearnedWeight(3, 17),  // Prior mean 0.15
		AuthorityWeight:      NewLearnedWeight(1, 99),  // Prior mean ~0 — N/A for local code
		CrossValidationBonus: NewLearnedWeight(1, 19),  // Prior mean 0.05
	}
}

// academicPriors: Academic — authority and citations matter.
func academicPriors() *QualityWeights {
	return &QualityWeights{
		VectorWeight:         NewLearnedWeight(6, 14),  // Prior mean 0.30
		GraphWeight:          NewLearnedWeight(2, 18),  // Prior mean 0.10
		BleveWeight:          NewLearnedWeight(5, 15),  // Prior mean 0.25
		TrustLevelWeight:     NewLearnedWeight(3, 17),  // Prior mean 0.15
		StalenessWeight:      NewLearnedWeight(1, 19),  // Prior mean 0.05 — low, old papers valuable
		UsageFeedbackWeight:  NewLearnedWeight(3, 17),  // Prior mean 0.15
		AuthorityWeight:      NewLearnedWeight(4, 16),  // Prior mean 0.20 — high for academic
		CrossValidationBonus: NewLearnedWeight(1, 19),  // Prior mean 0.05
	}
}

// historyPriors: Archivalist — past usage feedback matters most.
func historyPriors() *QualityWeights {
	return &QualityWeights{
		VectorWeight:         NewLearnedWeight(6, 14),  // Prior mean 0.30
		GraphWeight:          NewLearnedWeight(3, 17),  // Prior mean 0.15
		BleveWeight:          NewLearnedWeight(4, 16),  // Prior mean 0.20
		TrustLevelWeight:     NewLearnedWeight(2, 18),  // Prior mean 0.10
		StalenessWeight:      NewLearnedWeight(2, 18),  // Prior mean 0.10
		UsageFeedbackWeight:  NewLearnedWeight(5, 15),  // Prior mean 0.25 — high, "what helped before"
		AuthorityWeight:      NewLearnedWeight(1, 19),  // Prior mean 0.05
		CrossValidationBonus: NewLearnedWeight(1, 19),  // Prior mean 0.05
	}
}

// balancedPriors: Unknown domain — balanced, high-uncertainty priors.
// Effective-sample totals are lower (≈10) so observations move the
// posterior faster than in domain-specialized priors (≈20).
func balancedPriors() *QualityWeights {
	return &QualityWeights{
		VectorWeight:         NewLearnedWeight(3, 7),      // Prior mean 0.30
		GraphWeight:          NewLearnedWeight(1.5, 8.5),  // Prior mean 0.15
		BleveWeight:          NewLearnedWeight(2.5, 7.5),  // Prior mean 0.25
		TrustLevelWeight:     NewLearnedWeight(1, 9),      // Prior mean 0.10
		StalenessWeight:      NewLearnedWeight(1.5, 8.5),  // Prior mean 0.15
		UsageFeedbackWeight:  NewLearnedWeight(1.5, 8.5),  // Prior mean 0.15
		AuthorityWeight:      NewLearnedWeight(1, 9),      // Prior mean 0.10
		CrossValidationBonus: NewLearnedWeight(0.5, 9.5),  // Prior mean 0.05
	}
}

// PriorDecayRate returns the domain-specific Gamma prior for staleness
// decay. Code decays fast (short half-life); academic decays slowly.
// Shape-scale pairs are chosen so E[λ] matches SCORING.md §2 "Staleness
// Decay (Learned, Not Hardcoded)" expectations while keeping variance
// weakly-informative.
func PriorDecayRate(domain Domain) *LearnedDecayRate {
	switch domain {
	case DomainCode:
		// High decay rate: mean 0.05 events/unit, moderate variance.
		return NewLearnedDecayRate(1, 20)
	case DomainAcademic:
		// Low decay rate: mean ~0.001 events/unit, low variance.
		return NewLearnedDecayRate(1, 1000)
	case DomainHistory:
		// Mild decay: mean 0.02 events/unit.
		return NewLearnedDecayRate(1, 50)
	default:
		// Balanced uncertainty: mean 0.1, wide spread.
		return NewLearnedDecayRate(1, 10)
	}
}
