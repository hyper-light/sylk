package quality

import (
	"sync"
	"testing"
)

// TestSessionWeightManager_RejectsMissingSessionID locks the
// no-silent-defaults invariant: an unnamed session would collide on
// WAL attribution and three-way merge.
func TestSessionWeightManager_RejectsMissingSessionID(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	if _, err := NewSessionWeightManager("", base); err == nil {
		t.Fatal("expected error for empty session id")
	}
	if _, err := NewSessionWeightManager("  ", base); err == nil {
		t.Fatal("expected error for whitespace-only session id")
	}
}

// TestSessionWeightManager_RejectsNilBase verifies the manager refuses to
// start without a concrete prior. Forking from nil would produce
// Mean()=0 scoring that silently misranks every retrieval.
func TestSessionWeightManager_RejectsNilBase(t *testing.T) {
	if _, err := NewSessionWeightManager("session-1", nil); err == nil {
		t.Fatal("expected error for nil base")
	}
}

// TestSessionWeightManager_ReadsFallThroughToBase verifies the read path:
// when the session has observed nothing, WeightFor returns the base.
func TestSessionWeightManager_ReadsFallThroughToBase(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, err := NewSessionWeightManager("session-1", base)
	if err != nil {
		t.Fatalf("NewSessionWeightManager: %v", err)
	}
	w := m.WeightFor(DomainCode, SignalStaleness)
	if w == nil {
		t.Fatal("WeightFor returned nil — fall-through to base failed")
	}
	if w.Mean() != base.StalenessWeight.Mean() {
		t.Errorf("fall-through mean = %v, want %v", w.Mean(), base.StalenessWeight.Mean())
	}
}

// TestSessionWeightManager_ObserveCopiesOnFirstWrite verifies the
// copy-on-write semantic: the first observation on a signal forks the
// base weight rather than mutating it. The base must remain
// byte-identical after the observation.
func TestSessionWeightManager_ObserveCopiesOnFirstWrite(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	baseStalenessMean := base.StalenessWeight.Mean()

	m, _ := NewSessionWeightManager("session-1", base)
	m.Observe(SignalObservation{Kind: SignalStaleness, Success: true, EffectiveWeight: 1}, DomainCode)

	// The session's effective mean moved.
	if m.WeightFor(DomainCode, SignalStaleness).Mean() == baseStalenessMean {
		t.Error("session overlay did not record observation")
	}
	// The base mean is unchanged.
	if m.BaseWeights().StalenessWeight.Mean() != baseStalenessMean {
		t.Error("base was mutated — copy-on-write broken")
	}
}

// TestSessionWeightManager_SessionsAreIsolated is the SCORE-05 isolation
// regression. Two sessions forked from the same base: observations in A
// must be invisible to B's reads, even under concurrent access.
func TestSessionWeightManager_SessionsAreIsolated(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	sessionA, _ := NewSessionWeightManager("session-a", base)
	sessionB, _ := NewSessionWeightManager("session-b", base)

	// A records 10 positive observations on Staleness.
	for i := 0; i < 10; i++ {
		sessionA.Observe(SignalObservation{Kind: SignalStaleness, Success: true, EffectiveWeight: 1}, DomainCode)
	}

	// A's staleness mean must have moved.
	aMean := sessionA.WeightFor(DomainCode, SignalStaleness).Mean()
	baseMean := base.StalenessWeight.Mean()
	if aMean <= baseMean {
		t.Fatalf("session A's mean (%v) should exceed base (%v)", aMean, baseMean)
	}

	// B's staleness mean must be unchanged.
	bMean := sessionB.WeightFor(DomainCode, SignalStaleness).Mean()
	if bMean != baseMean {
		t.Errorf("session B sees A's mutations: bMean=%v baseMean=%v", bMean, baseMean)
	}
}

// TestSessionWeightManager_ConcurrentObservationsAreSafe stress-tests the
// overlay mutex. 100 concurrent observers each apply 50 observations —
// the resulting posterior must reflect exactly 5000 updates with no
// data races (run with -race).
func TestSessionWeightManager_ConcurrentObservationsAreSafe(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-concurrent", base)

	const workers = 100
	const perWorker = 50

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				m.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)
			}
		}()
	}
	wg.Wait()

	w := m.WeightFor(DomainCode, SignalVector)
	want := float64(workers * perWorker)
	if w.EffectiveSamples != want {
		t.Errorf("EffectiveSamples = %v, want %v (lost observations under concurrency)", w.EffectiveSamples, want)
	}
}

// TestSessionWeightManager_MergeToProjectAppliesDirtyKeys verifies
// MergeToProject commits the session overlay into the base and clears
// the overlay. After merge, a fresh WeightFor read must see the merged
// posterior coming from the base, not the overlay.
func TestSessionWeightManager_MergeToProjectAppliesDirtyKeys(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)

	for i := 0; i < 5; i++ {
		m.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)
	}
	preMergeMean := m.WeightFor(DomainCode, SignalVector).Mean()

	merged := m.MergeToProject()
	if merged == 0 {
		t.Fatal("MergeToProject returned 0 — dirty keys were not committed")
	}
	if len(m.DirtyKeys()) != 0 {
		t.Errorf("dirty keys after merge = %d, want 0", len(m.DirtyKeys()))
	}
	postMergeMean := m.WeightFor(DomainCode, SignalVector).Mean()
	if postMergeMean != preMergeMean {
		t.Errorf("effective mean changed across merge: pre=%v post=%v — session's view should be stable", preMergeMean, postMergeMean)
	}
	// Base has absorbed the evidence.
	if m.BaseWeights().VectorWeight.EffectiveSamples < 5 {
		t.Errorf("base EffectiveSamples after merge = %v, want ≥5", m.BaseWeights().VectorWeight.EffectiveSamples)
	}
}

// TestSessionWeightManager_MergeWithNoDirtyIsNoop verifies MergeToProject
// on a session that observed nothing is a no-op (returns 0) and leaves
// the base untouched.
func TestSessionWeightManager_MergeWithNoDirtyIsNoop(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)

	baseAlphaBefore := m.BaseWeights().VectorWeight.Alpha
	if n := m.MergeToProject(); n != 0 {
		t.Fatalf("empty-overlay merge returned %d, want 0", n)
	}
	if m.BaseWeights().VectorWeight.Alpha != baseAlphaBefore {
		t.Error("base changed despite empty overlay")
	}
}

// TestSessionWeightManager_ObservationHookFires verifies the hook path:
// every Observe call produces exactly one ObservationRecord with correct
// before/after means.
func TestSessionWeightManager_ObservationHookFires(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)

	var records []ObservationRecord
	var hookMu sync.Mutex
	m.SetObservationHook(func(rec ObservationRecord) {
		hookMu.Lock()
		defer hookMu.Unlock()
		records = append(records, rec)
	})

	m.Observe(SignalObservation{Kind: SignalBleve, Success: false, EffectiveWeight: 1}, DomainCode)
	m.Observe(SignalObservation{Kind: SignalBleve, Success: true, EffectiveWeight: 1}, DomainCode)

	hookMu.Lock()
	defer hookMu.Unlock()
	if len(records) != 2 {
		t.Fatalf("hook fired %d times, want 2", len(records))
	}
	if records[0].SessionID != "session-1" {
		t.Errorf("SessionID = %q, want session-1", records[0].SessionID)
	}
	if records[1].PriorMean >= records[1].PosteriorMean {
		// Second Observe is Success=true, so posterior mean must rise.
		t.Errorf("success observation did not raise mean: prior=%v post=%v", records[1].PriorMean, records[1].PosteriorMean)
	}
}

// TestSessionWeightManager_ConcurrentSessionsDoNotInterfere combines
// isolation and concurrency: two sessions each run 1000 observations on
// the same base in parallel; after completion, neither session has seen
// the other's evidence. This is the heavyweight SCORE-05 locked invariant.
func TestSessionWeightManager_ConcurrentSessionsDoNotInterfere(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	sessionA, _ := NewSessionWeightManager("session-a", base)
	sessionB, _ := NewSessionWeightManager("session-b", base)

	const perSession = 1000

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < perSession; i++ {
			sessionA.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < perSession; i++ {
			sessionB.Observe(SignalObservation{Kind: SignalVector, Success: false, EffectiveWeight: 1}, DomainCode)
		}
	}()
	wg.Wait()

	aSamples := sessionA.WeightFor(DomainCode, SignalVector).EffectiveSamples
	bSamples := sessionB.WeightFor(DomainCode, SignalVector).EffectiveSamples
	if aSamples != perSession {
		t.Errorf("session A EffectiveSamples = %v, want %v", aSamples, perSession)
	}
	if bSamples != perSession {
		t.Errorf("session B EffectiveSamples = %v, want %v", bSamples, perSession)
	}
	// A's mean trended upward (all successes); B's trended downward.
	aMean := sessionA.WeightFor(DomainCode, SignalVector).Mean()
	bMean := sessionB.WeightFor(DomainCode, SignalVector).Mean()
	if aMean <= bMean {
		t.Errorf("A's mean (%v) should exceed B's (%v); isolation broken", aMean, bMean)
	}
}

// TestSessionWeightManager_EffectiveWeightsReturnsCopy verifies the
// caller cannot corrupt manager state by mutating EffectiveWeights output.
func TestSessionWeightManager_EffectiveWeightsReturnsCopy(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	m.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)

	view := m.EffectiveWeights(DomainCode)
	view.VectorWeight.Alpha = 9999
	if m.WeightFor(DomainCode, SignalVector).Alpha == 9999 {
		t.Error("mutating EffectiveWeights poisoned the manager — not a deep copy")
	}
}
