package manifest

import (
	"testing"
	"time"
)

func TestScopeMatchesQuery_LanguageMonorepo(t *testing.T) {
	// Decision says "for Python tests anywhere": {language: python}.
	// Query from a tester for /services/api/tests/test_login.py with
	// language=python should match.
	decision := Scope{DimensionLanguage: "python"}
	query := Scope{
		DimensionLanguage: "python",
		DimensionPath:     "services/api/tests/test_login.py",
		DimensionSurface:  "unit",
	}
	if !ScopeMatchesQuery(decision, query) {
		t.Fatalf("python decision should match python query in any path")
	}

	// Same decision should NOT match a Rust query in the same monorepo.
	rustQuery := Scope{
		DimensionLanguage: "rust",
		DimensionPath:     "crates/auth/tests/login.rs",
	}
	if ScopeMatchesQuery(decision, rustQuery) {
		t.Fatalf("python decision should not match rust query")
	}
}

func TestScopeMatchesQuery_PathPrefix(t *testing.T) {
	// Decision scoped to services/api/ subtree.
	decision := Scope{DimensionPath: "services/api/"}
	cases := map[string]bool{
		"services/api/main.py":             true,
		"services/api/tests/test_login.py": true,
		"services/api":                     true, // exact (sans slash) matches
		"services/billing/main.py":         false,
		"services/api_legacy/main.py":      false, // critical: must not glob
		"":                                 false,
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			query := Scope{DimensionPath: path}
			if got := ScopeMatchesQuery(decision, query); got != want {
				t.Fatalf("ScopeMatchesQuery(decision, {path: %q}) = %v, want %v", path, got, want)
			}
		})
	}
}

func TestScopeMatchesQuery_AmbientMatchesAnything(t *testing.T) {
	// Empty decision scope = ambient — matches every query in its domain.
	decision := Scope{}
	cases := []Scope{
		{},
		{DimensionLanguage: "python"},
		{DimensionPath: "anywhere/at/all.py"},
		{DimensionLanguage: "rust", DimensionPath: "crates/x/", DimensionSurface: "e2e"},
	}
	for i, q := range cases {
		if !ScopeMatchesQuery(decision, q) {
			t.Fatalf("ambient decision should match query case %d (%v)", i, q)
		}
	}
}

func TestScopesOverlap_DisjointNonOverlapping(t *testing.T) {
	cases := []struct {
		name    string
		a, b    Scope
		overlap bool
	}{
		{
			name:    "disjoint languages",
			a:       Scope{DimensionLanguage: "python"},
			b:       Scope{DimensionLanguage: "rust"},
			overlap: false,
		},
		{
			name:    "disjoint paths",
			a:       Scope{DimensionPath: "services/api/"},
			b:       Scope{DimensionPath: "services/billing/"},
			overlap: false,
		},
		{
			name:    "nested paths overlap",
			a:       Scope{DimensionPath: "services/"},
			b:       Scope{DimensionPath: "services/api/"},
			overlap: true,
		},
		{
			name:    "shared language, disjoint path",
			a:       Scope{DimensionLanguage: "python", DimensionPath: "src/"},
			b:       Scope{DimensionLanguage: "python", DimensionPath: "tests/"},
			overlap: false,
		},
		{
			name:    "shared language, no other dimension specified",
			a:       Scope{DimensionLanguage: "python"},
			b:       Scope{DimensionLanguage: "python", DimensionPath: "tests/"},
			overlap: true,
		},
		{
			name:    "fully ambient overlaps anything",
			a:       Scope{},
			b:       Scope{DimensionLanguage: "lua", DimensionPath: "scripts/"},
			overlap: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScopesOverlap(tc.a, tc.b); got != tc.overlap {
				t.Fatalf("ScopesOverlap = %v, want %v", got, tc.overlap)
			}
			if got := ScopesOverlap(tc.b, tc.a); got != tc.overlap {
				t.Fatalf("ScopesOverlap reversed = %v, want %v", got, tc.overlap)
			}
		})
	}
}

func TestResolveWinner_SpecificityFirstMoverPicksMostSpecific(t *testing.T) {
	t0 := time.Now()
	d1 := &Decision{ID: "ambient", Scope: Scope{}, Value: "pytest", DeclaredAt: t0}
	d2 := &Decision{ID: "py", Scope: Scope{DimensionLanguage: "python"}, Value: "pytest", DeclaredAt: t0.Add(10 * time.Second)}
	d3 := &Decision{ID: "py-api", Scope: Scope{DimensionLanguage: "python", DimensionPath: "services/api/"}, Value: "pytest-asyncio", DeclaredAt: t0.Add(20 * time.Second)}

	winner := ResolveWinner(ResolvePolicySpecificityFirstMover, []*Decision{d1, d2, d3})
	if winner == nil || winner.ID != "py-api" {
		t.Fatalf("winner = %+v, want py-api (most specific)", winner)
	}
}

func TestResolveWinner_FirstMoverBreaksTie(t *testing.T) {
	t0 := time.Now()
	d1 := &Decision{ID: "early", Scope: Scope{DimensionLanguage: "python"}, Value: "pytest", DeclaredAt: t0}
	d2 := &Decision{ID: "later", Scope: Scope{DimensionLanguage: "python"}, Value: "unittest", DeclaredAt: t0.Add(time.Second)}

	winner := ResolveWinner(ResolvePolicySpecificityFirstMover, []*Decision{d2, d1})
	if winner == nil || winner.ID != "early" {
		t.Fatalf("winner = %+v, want early (first-mover tiebreak)", winner)
	}
}

func TestResolveWinner_AuthorityFirstPrefersHigherConfidence(t *testing.T) {
	t0 := time.Now()
	tentative := &Decision{ID: "tent", Scope: Scope{DimensionLanguage: "python"}, Value: "unittest", DeclaredAt: t0, Confidence: ConfidenceTentative}
	consensus := &Decision{ID: "consensus", Scope: Scope{}, Value: "pytest", DeclaredAt: t0.Add(time.Hour), Confidence: ConfidenceConsensus}

	winner := ResolveWinner(ResolvePolicyAuthorityFirst, []*Decision{tentative, consensus})
	if winner == nil || winner.ID != "consensus" {
		t.Fatalf("winner = %+v, want consensus (authority outranks specificity)", winner)
	}
}

func TestResolveWinner_ExplicitConsensusReturnsNilWithoutConsensus(t *testing.T) {
	t0 := time.Now()
	candidates := []*Decision{
		{ID: "a", Scope: Scope{DimensionLanguage: "python"}, Value: "pytest", DeclaredAt: t0, Confidence: ConfidenceTentative},
		{ID: "b", Scope: Scope{DimensionLanguage: "python"}, Value: "unittest", DeclaredAt: t0.Add(time.Second), Confidence: ConfidenceCommitted},
	}
	if winner := ResolveWinner(ResolvePolicyExplicitConsensusRequired, candidates); winner != nil {
		t.Fatalf("winner = %+v, want nil (explicit consensus required)", winner)
	}

	candidates = append(candidates, &Decision{ID: "c", Scope: Scope{DimensionLanguage: "python"}, Value: "pytest", DeclaredAt: t0.Add(2 * time.Second), Confidence: ConfidenceConsensus})
	if winner := ResolveWinner(ResolvePolicyExplicitConsensusRequired, candidates); winner == nil || winner.ID != "c" {
		t.Fatalf("winner = %+v, want c (the only consensus)", winner)
	}
}

func TestResolveWinner_LatestWins(t *testing.T) {
	t0 := time.Now()
	candidates := []*Decision{
		{ID: "old", Value: "v1", DeclaredAt: t0},
		{ID: "mid", Value: "v2", DeclaredAt: t0.Add(time.Second)},
		{ID: "new", Value: "v3", DeclaredAt: t0.Add(2 * time.Second)},
	}
	if winner := ResolveWinner(ResolvePolicyLatestWins, candidates); winner == nil || winner.ID != "new" {
		t.Fatalf("winner = %+v, want new", winner)
	}
}

func TestFilterMatching_ExcludesNonMatchingDecisions(t *testing.T) {
	decisions := []*Decision{
		{ID: "py", Scope: Scope{DimensionLanguage: "python"}, Value: "pytest"},
		{ID: "rust", Scope: Scope{DimensionLanguage: "rust"}, Value: "cargo-test"},
		{ID: "ambient", Scope: Scope{}, Value: "pytest"},
		{ID: "api-py", Scope: Scope{DimensionLanguage: "python", DimensionPath: "services/api/"}, Value: "pytest-asyncio"},
		{ID: "billing-py", Scope: Scope{DimensionLanguage: "python", DimensionPath: "services/billing/"}, Value: "pytest"},
	}
	query := Scope{DimensionLanguage: "python", DimensionPath: "services/api/tests/test_login.py"}
	matches := FilterMatching(decisions, query)

	// Should match: py, ambient, api-py. Should NOT match: rust, billing-py.
	gotIDs := map[string]bool{}
	for _, m := range matches {
		gotIDs[m.ID] = true
	}
	want := []string{"py", "ambient", "api-py"}
	for _, id := range want {
		if !gotIDs[id] {
			t.Errorf("expected %s in matches, missing from %v", id, matches)
		}
	}
	if gotIDs["rust"] || gotIDs["billing-py"] {
		t.Errorf("matches contained excluded decision: %v", matches)
	}
}

func TestSortBySpecificity_OrdersMostSpecificFirst(t *testing.T) {
	t0 := time.Now()
	in := []*Decision{
		{ID: "ambient", Scope: Scope{}, DeclaredAt: t0},
		{ID: "lang-only", Scope: Scope{DimensionLanguage: "python"}, DeclaredAt: t0.Add(time.Second)},
		{ID: "lang+path", Scope: Scope{DimensionLanguage: "python", DimensionPath: "services/api/"}, DeclaredAt: t0.Add(2 * time.Second)},
	}
	SortBySpecificity(in)
	wantOrder := []string{"lang+path", "lang-only", "ambient"}
	for i, want := range wantOrder {
		if in[i].ID != want {
			t.Fatalf("position %d = %s, want %s; full order = %+v", i, in[i].ID, want, in)
		}
	}
}

func TestCompatibilityFor_EqualValuesEquivalentEvenWithoutDomain(t *testing.T) {
	if got := CompatibilityFor("nonexistent_domain", "pytest", "pytest"); got != CompatibilityEquivalent {
		t.Fatalf("CompatibilityFor equal values = %v, want Equivalent", got)
	}
	if got := CompatibilityFor("nonexistent_domain", "pytest", "unittest"); got != CompatibilityIncompatible {
		t.Fatalf("CompatibilityFor unequal unregistered = %v, want Incompatible (conservative default)", got)
	}
}

func TestValidateDeclareInput_RejectsEmptyAndHintConfidence(t *testing.T) {
	cases := []struct {
		name    string
		input   *DeclareDecisionInput
		wantErr bool
	}{
		{"nil", nil, true},
		{"empty domain", &DeclareDecisionInput{Value: "pytest"}, true},
		{"empty value", &DeclareDecisionInput{Domain: "test_framework"}, true},
		{"hint not allowed for agents", &DeclareDecisionInput{Domain: "test_framework", Value: "pytest", Confidence: ConfidenceHint}, true},
		{"defaults to tentative", &DeclareDecisionInput{Domain: "test_framework", Value: "pytest"}, false},
		{"committed ok", &DeclareDecisionInput{Domain: "test_framework", Value: "pytest", Confidence: ConfidenceCommitted}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeclareInput(tc.input)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr && tc.input.Confidence == "" {
				t.Fatalf("ValidateDeclareInput should default empty confidence")
			}
		})
	}
}
