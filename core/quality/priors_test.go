package quality

import (
	"math"
	"testing"
)

// TestPriorQualityWeights_CodeMeansMatchSpec verifies the SCORING.md §2
// "Prior Weights by Agent Domain" table verbatim for DomainCode. Any
// divergence changes Librarian retrieval behavior silently — this lock-in
// test ensures edits to priors.go are coordinated with the doc.
func TestPriorQualityWeights_CodeMeansMatchSpec(t *testing.T) {
	q := PriorQualityWeights(DomainCode)
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"vector", q.VectorWeight.Mean(), 0.35},
		{"graph", q.GraphWeight.Mean(), 0.15},
		{"bleve", q.BleveWeight.Mean(), 0.25},
		{"trust_level", q.TrustLevelWeight.Mean(), 0.05},
		{"staleness", q.StalenessWeight.Mean(), 0.25},
		{"usage_feedback", q.UsageFeedbackWeight.Mean(), 0.15},
		// Authority prior (1, 99) yields ≈0.01, not exactly 0. Test against
		// the spec's stated "~0".
		{"authority", q.AuthorityWeight.Mean(), 0.01},
		{"cross_validation", q.CrossValidationBonus.Mean(), 0.05},
	}
	for _, tc := range cases {
		if math.Abs(tc.got-tc.want) > 0.005 {
			t.Errorf("DomainCode.%s: mean = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// TestPriorQualityWeights_AcademicAuthorityExceedsCodeAuthority locks the
// domain-divergence invariant: Academic weights authority signal more
// heavily than Code does. If this relationship ever inverts, prior
// tuning has regressed.
func TestPriorQualityWeights_AcademicAuthorityExceedsCodeAuthority(t *testing.T) {
	code := PriorQualityWeights(DomainCode).AuthorityWeight.Mean()
	acad := PriorQualityWeights(DomainAcademic).AuthorityWeight.Mean()
	if acad <= code {
		t.Fatalf("Academic authority (%v) should exceed Code authority (%v)", acad, code)
	}
}

// TestPriorQualityWeights_CodeStalenessExceedsAcademicStaleness locks the
// inverse domain invariant: code staleness penalty is heavier than
// academic's.
func TestPriorQualityWeights_CodeStalenessExceedsAcademicStaleness(t *testing.T) {
	code := PriorQualityWeights(DomainCode).StalenessWeight.Mean()
	acad := PriorQualityWeights(DomainAcademic).StalenessWeight.Mean()
	if code <= acad {
		t.Fatalf("Code staleness (%v) should exceed Academic staleness (%v)", code, acad)
	}
}

// TestPriorQualityWeights_HistoryUsageFeedbackIsPrimary locks the
// Archivalist domain invariant: usage feedback is the strongest prior
// signal in DomainHistory.
func TestPriorQualityWeights_HistoryUsageFeedbackIsPrimary(t *testing.T) {
	q := PriorQualityWeights(DomainHistory)
	feedback := q.UsageFeedbackWeight.Mean()
	for _, other := range []float64{
		q.GraphWeight.Mean(),
		q.BleveWeight.Mean(),
		q.TrustLevelWeight.Mean(),
		q.StalenessWeight.Mean(),
		q.AuthorityWeight.Mean(),
		q.CrossValidationBonus.Mean(),
	} {
		if feedback <= other {
			t.Errorf("UsageFeedback (%v) should exceed other signals; competitor = %v", feedback, other)
		}
	}
	// VectorWeight is allowed to match or exceed feedback (retrieval core)
	// — the invariant is that feedback beats every OTHER quality signal.
}

// TestPriorQualityWeights_UnknownDomainIsBalanced verifies the default
// balanced prior: no single signal dominates. Useful when a query's
// domain cannot be classified.
func TestPriorQualityWeights_UnknownDomainIsBalanced(t *testing.T) {
	q := PriorQualityWeights(DomainUnknown)
	means := q.MeanVector()
	for _, m := range means {
		if m <= 0 || m >= 0.5 {
			t.Errorf("unknown domain mean %v out of balanced range (0, 0.5)", m)
		}
	}
}

// TestDomain_StringRoundTrip verifies String ↔ DomainFromString is a
// bijection for known domains. WAL replay relies on this — a mismatch
// would break cross-version resume.
func TestDomain_StringRoundTrip(t *testing.T) {
	for _, d := range []Domain{DomainUnknown, DomainCode, DomainAcademic, DomainHistory} {
		s := d.String()
		parsed, ok := DomainFromString(s)
		if !ok {
			t.Errorf("DomainFromString(%q) failed for Domain %d", s, d)
		}
		if parsed != d {
			t.Errorf("round trip %d → %q → %d", d, s, parsed)
		}
	}
}

// TestSignalKind_StringRoundTrip verifies String ↔ SignalKindFromString.
// WAL replay of Bayesian observations (SCORE-04) will serialize signals
// by name — a rename without updating both sides silently drops the
// observation on replay.
func TestSignalKind_StringRoundTrip(t *testing.T) {
	for k := SignalKind(0); k < signalCount; k++ {
		s := k.String()
		parsed, ok := SignalKindFromString(s)
		if !ok {
			t.Errorf("SignalKindFromString(%q) failed for signal %d", s, k)
		}
		if parsed != k {
			t.Errorf("round trip %d → %q → %d", k, s, parsed)
		}
	}
}
