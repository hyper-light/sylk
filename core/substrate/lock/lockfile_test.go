package lock

import (
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/fabric"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestNew_RequiresSessionID(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, ErrSessionIDRequired) {
		t.Fatalf("expected ErrSessionIDRequired, got %v", err)
	}
}

func TestPin_FirstWitnessCreatesPin(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")

	out, err := l.Pin(req)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if !out.Created {
		t.Error("expected Created == true on first pin")
	}
	if !out.WitnessAdded {
		t.Error("expected WitnessAdded == true on first pin")
	}
	if got := out.Pin.WitnessCount(); got != 1 {
		t.Errorf("witness count = %d, want 1", got)
	}
}

func TestPin_SecondWitnessAppends(t *testing.T) {
	l := newTestLockfile(t)
	first := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	second := samplePinRequest("python", "pytest", "8.3.2", "pipe-12")

	_, _ = l.Pin(first)
	out, err := l.Pin(second)
	if err != nil {
		t.Fatalf("second Pin: %v", err)
	}
	if out.Created {
		t.Error("expected Created == false on second pin (pin already exists)")
	}
	if !out.WitnessAdded {
		t.Error("expected WitnessAdded == true for new witness")
	}
	if got := out.Pin.WitnessCount(); got != 2 {
		t.Errorf("witness count after 2 distinct witnesses = %d, want 2", got)
	}
}

func TestPin_SameWitnessTwiceIsIdempotent(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")

	_, _ = l.Pin(req)
	out, err := l.Pin(req)
	if err != nil {
		t.Fatalf("repeat Pin: %v", err)
	}
	if out.WitnessAdded {
		t.Error("expected WitnessAdded == false for repeat of same witness")
	}
	if got := out.Pin.WitnessCount(); got != 1 {
		t.Errorf("witness count after 2 identical pins = %d, want 1 (idempotent)", got)
	}
}

func TestPin_ConcurrentSameWitness_Idempotent(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = l.Pin(req)
		}()
	}
	wg.Wait()

	pin, ok := l.LookupVersion(req.Coordinate, req.Version)
	if !ok {
		t.Fatal("pin not found")
	}
	if pin.WitnessCount() != 1 {
		t.Errorf("after %d concurrent identical pins, witness count = %d, want 1", goroutines, pin.WitnessCount())
	}
}

func TestPin_ConcurrentDistinctWitnesses_AllAppended(t *testing.T) {
	l := newTestLockfile(t)
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			req := samplePinRequest("python", "pytest", "8.3.2", pipeName(idx))
			_, _ = l.Pin(req)
		}(i)
	}
	wg.Wait()

	pin, ok := l.LookupVersion(Coordinate{Ecosystem: "python", Name: "pytest"}, "8.3.2")
	if !ok {
		t.Fatal("pin not found")
	}
	if pin.WitnessCount() != goroutines {
		t.Errorf("witness count after %d distinct witnesses = %d, want %d", goroutines, pin.WitnessCount(), goroutines)
	}
}

func TestPin_DifferentVersionsCoexist(t *testing.T) {
	l := newTestLockfile(t)
	a := samplePinRequest("python", "pytest", "8.3.1", "pipe-A")
	b := samplePinRequest("python", "pytest", "8.3.2", "pipe-B")

	_, _ = l.Pin(a)
	_, _ = l.Pin(b)

	coord := Coordinate{Ecosystem: "python", Name: "pytest"}
	pins := l.PinsFor(coord)
	if len(pins) != 2 {
		t.Errorf("expected 2 side-by-side versions, got %d", len(pins))
	}
}

func TestPin_RejectsEmptyCoordinate(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	req.Coordinate = Coordinate{}
	_, err := l.Pin(req)
	if !errors.Is(err, ErrEmptyCoordinate) {
		t.Fatalf("expected ErrEmptyCoordinate, got %v", err)
	}
}

func TestPin_RejectsEmptyVersion(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	req.Version = ""
	_, err := l.Pin(req)
	if !errors.Is(err, ErrEmptyVersion) {
		t.Fatalf("expected ErrEmptyVersion, got %v", err)
	}
}

func TestPin_RejectsEmptyManifestID(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	req.ManifestID = manifest.ID{}
	_, err := l.Pin(req)
	if !errors.Is(err, ErrEmptyManifestID) {
		t.Fatalf("expected ErrEmptyManifestID, got %v", err)
	}
}

func TestPin_RejectsEmptyWitnessPipeline(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	req.Witness.PipelineID = ""
	_, err := l.Pin(req)
	if !errors.Is(err, ErrEmptyWitnessPipeline) {
		t.Fatalf("expected ErrEmptyWitnessPipeline, got %v", err)
	}
}

func TestAddWitness_FailsForUnknownPin(t *testing.T) {
	l := newTestLockfile(t)
	_, err := l.AddWitness(
		Coordinate{Ecosystem: "python", Name: "ghost"},
		"1.0.0",
		Witness{PipelineID: "pipe-7", Constraint: "*"},
	)
	if !errors.Is(err, ErrPinNotFound) {
		t.Fatalf("expected ErrPinNotFound, got %v", err)
	}
}

func TestRemoveWitness_KeepsPinWhenWitnessesRemain(t *testing.T) {
	l := newTestLockfile(t)
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-7"))
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-12"))

	out, err := l.RemoveWitness(Coordinate{Ecosystem: "python", Name: "pytest"}, "8.3.2", "pipe-7")
	if err != nil {
		t.Fatalf("RemoveWitness: %v", err)
	}
	if out.PinRemoved {
		t.Error("expected PinRemoved == false (other witnesses remain)")
	}
	if out.RemainingWitnesses != 1 {
		t.Errorf("RemainingWitnesses = %d, want 1", out.RemainingWitnesses)
	}
}

func TestRemoveWitness_RemovesPinWhenLastWitness(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	_, _ = l.Pin(req)

	out, err := l.RemoveWitness(req.Coordinate, req.Version, "pipe-7")
	if err != nil {
		t.Fatalf("RemoveWitness: %v", err)
	}
	if !out.PinRemoved {
		t.Error("expected PinRemoved == true (last witness)")
	}
	if out.ManifestID != req.ManifestID {
		t.Errorf("outcome ManifestID = %s, want %s", out.ManifestID, req.ManifestID)
	}
	if _, ok := l.LookupVersion(req.Coordinate, req.Version); ok {
		t.Error("pin should be gone after last witness removal")
	}
}

func TestRemoveWitness_FailsForUnknownPin(t *testing.T) {
	l := newTestLockfile(t)
	_, err := l.RemoveWitness(Coordinate{Ecosystem: "x", Name: "y"}, "1", "p")
	if !errors.Is(err, ErrPinNotFound) {
		t.Fatalf("expected ErrPinNotFound, got %v", err)
	}
}

func TestRemoveWitness_FailsForUnknownWitness(t *testing.T) {
	l := newTestLockfile(t)
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	_, _ = l.Pin(req)

	_, err := l.RemoveWitness(req.Coordinate, req.Version, "different-pipeline")
	if !errors.Is(err, ErrWitnessNotFound) {
		t.Fatalf("expected ErrWitnessNotFound, got %v", err)
	}
}

func TestSnapshot_Sorted(t *testing.T) {
	l := newTestLockfile(t)
	_, _ = l.Pin(samplePinRequest("rust", "ripgrep", "14.1.0", "pipe-A"))
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-A"))
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.1", "pipe-B"))

	snap := l.Snapshot()
	if len(snap.Pins) != 3 {
		t.Fatalf("snapshot has %d pins, want 3", len(snap.Pins))
	}
	// Snapshot pins should be ordered: ecosystem/name asc, then version asc.
	want := []string{
		"python/pytest@8.3.1",
		"python/pytest@8.3.2",
		"rust/ripgrep@14.1.0",
	}
	for i := range want {
		got := snap.Pins[i].Coordinate.String() + "@" + snap.Pins[i].Version
		if got != want[i] {
			t.Errorf("snapshot[%d] = %s, want %s", i, got, want[i])
		}
	}
}

func TestStat(t *testing.T) {
	l := newTestLockfile(t)
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-A"))
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-B"))
	_, _ = l.Pin(samplePinRequest("rust", "ripgrep", "14.1.0", "pipe-A"))

	stats := l.Stat()
	if stats.CoordinateCount != 2 {
		t.Errorf("CoordinateCount = %d, want 2", stats.CoordinateCount)
	}
	if stats.PinCount != 2 {
		t.Errorf("PinCount = %d, want 2", stats.PinCount)
	}
	if stats.WitnessCount != 3 {
		t.Errorf("WitnessCount = %d, want 3", stats.WitnessCount)
	}
}

func TestEmitter_FiresOnPinCreate(t *testing.T) {
	em := fabric.NewCapturingEmitter()
	l, err := New(Config{SessionID: "test-session", Emitter: em})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-7"))

	if em.CountByKind(fabric.ToolPinned) != 1 {
		t.Errorf("expected 1 ToolPinned activity, got %d", em.CountByKind(fabric.ToolPinned))
	}
}

func TestEmitter_FiresOnWitnessAdded(t *testing.T) {
	em := fabric.NewCapturingEmitter()
	l, err := New(Config{SessionID: "test-session", Emitter: em})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-7"))
	_, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-12"))

	if em.CountByKind(fabric.ToolWitnessAdded) != 1 {
		t.Errorf("expected 1 ToolWitnessAdded activity, got %d", em.CountByKind(fabric.ToolWitnessAdded))
	}
}

func TestEmitter_FiresOnPinDetached(t *testing.T) {
	em := fabric.NewCapturingEmitter()
	l, err := New(Config{SessionID: "test-session", Emitter: em})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	_, _ = l.Pin(req)
	_, _ = l.RemoveWitness(req.Coordinate, req.Version, "pipe-7")

	if em.CountByKind(fabric.ToolDetached) != 1 {
		t.Errorf("expected 1 ToolDetached activity (pin removed), got %d", em.CountByKind(fabric.ToolDetached))
	}
}

func TestEmitter_NoDetachWhenWitnessesRemain(t *testing.T) {
	em := fabric.NewCapturingEmitter()
	l, err := New(Config{SessionID: "test-session", Emitter: em})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req1 := samplePinRequest("python", "pytest", "8.3.2", "pipe-7")
	req2 := samplePinRequest("python", "pytest", "8.3.2", "pipe-12")
	_, _ = l.Pin(req1)
	_, _ = l.Pin(req2)
	_, _ = l.RemoveWitness(req1.Coordinate, req1.Version, "pipe-7")

	if em.CountByKind(fabric.ToolDetached) != 0 {
		t.Errorf("expected no ToolDetached when witnesses remain, got %d", em.CountByKind(fabric.ToolDetached))
	}
}

func TestPinAndRemove_Convergent(t *testing.T) {
	// CRDT property: applying the same operation set in different
	// orders yields the same final state.
	ops := []func(*Lockfile){
		func(l *Lockfile) { _, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "p1")) },
		func(l *Lockfile) { _, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "p2")) },
		func(l *Lockfile) { _, _ = l.Pin(samplePinRequest("python", "pytest", "8.3.2", "p3")) },
		func(l *Lockfile) {
			_, _ = l.RemoveWitness(Coordinate{Ecosystem: "python", Name: "pytest"}, "8.3.2", "p2")
		},
	}

	a := newTestLockfile(t)
	for _, op := range ops {
		op(a)
	}

	b := newTestLockfile(t)
	// Reverse the order — expect convergence on the same final state
	// because witness append/remove operations commute when their
	// (PipelineID) keys differ.
	reversed := []func(*Lockfile){ops[2], ops[0], ops[1], ops[3]}
	for _, op := range reversed {
		op(b)
	}

	assertSameWitnessSet(t, a, b, Coordinate{Ecosystem: "python", Name: "pytest"}, "8.3.2")
}

func assertSameWitnessSet(t *testing.T, a, b *Lockfile, coord Coordinate, version string) {
	t.Helper()
	pa, _ := a.LookupVersion(coord, version)
	pb, _ := b.LookupVersion(coord, version)
	if (pa == nil) != (pb == nil) {
		t.Fatalf("convergence broken: pin existence differs (a=%v, b=%v)", pa != nil, pb != nil)
	}
	if pa == nil {
		return
	}
	witnessesA := witnessSet(pa)
	witnessesB := witnessSet(pb)
	for k := range witnessesA {
		if !witnessesB[k] {
			t.Errorf("witness %q present in a but not b", k)
		}
	}
	for k := range witnessesB {
		if !witnessesA[k] {
			t.Errorf("witness %q present in b but not a", k)
		}
	}
}

func witnessSet(p *Pin) map[string]bool {
	out := make(map[string]bool, len(p.Witnesses))
	for i := range p.Witnesses {
		out[p.Witnesses[i].PipelineID] = true
	}
	return out
}

// Test helpers.

func newTestLockfile(t *testing.T) *Lockfile {
	t.Helper()
	l, err := New(Config{SessionID: "test-session"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func samplePinRequest(ecosystem, name, version, pipeline string) PinRequest {
	return PinRequest{
		Coordinate: Coordinate{Ecosystem: ecosystem, Name: name},
		Version:    version,
		ManifestID: manifest.ID{0xab, 0xcd, 0xef},
		RecipeID:   recipe.ID{0x12, 0x34},
		Source: manifest.SourceMetadata{
			RecipeID: "test-recipe",
			FetchURL: "https://example.com/" + name + "-" + version + ".tar.gz",
		},
		Witness: Witness{
			PipelineID: pipeline,
			Constraint: ">=" + version,
		},
	}
}

func pipeName(i int) string {
	return "pipe-" + intToString(i)
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
