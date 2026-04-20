package lock

import (
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/fabric"
)

func TestMemoryWAL_AppendAndReplay(t *testing.T) {
	w := NewMemoryWAL()
	a := Event{Op: OpPinCreate, Payload: PinCreatePayload{Request: samplePinRequest("python", "pytest", "8.3.2", "p1")}}
	b := Event{Op: OpAddWitness, Payload: AddWitnessPayload{
		Coordinate: Coordinate{Ecosystem: "python", Name: "pytest"},
		Version:    "8.3.2",
		Witness:    Witness{PipelineID: "p2"},
	}}

	if err := w.Append(a); err != nil {
		t.Fatalf("Append a: %v", err)
	}
	if err := w.Append(b); err != nil {
		t.Fatalf("Append b: %v", err)
	}

	collected := []Event{}
	err := w.Replay(func(e Event) error {
		collected = append(collected, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(collected) != 2 {
		t.Fatalf("collected %d events, want 2", len(collected))
	}
	if collected[0].Op != OpPinCreate {
		t.Errorf("collected[0].Op = %v, want OpPinCreate", collected[0].Op)
	}
	if collected[1].Op != OpAddWitness {
		t.Errorf("collected[1].Op = %v, want OpAddWitness", collected[1].Op)
	}
}

func TestMemoryWAL_HandlerErrorHaltsReplay(t *testing.T) {
	w := NewMemoryWAL()
	_ = w.Append(Event{Op: OpPinCreate})
	_ = w.Append(Event{Op: OpAddWitness})

	stop := errors.New("stop")
	err := w.Replay(func(_ Event) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("expected handler error to propagate, got %v", err)
	}
}

func TestRestore_RebuildsLockfileFromWAL(t *testing.T) {
	// Run a sequence of operations against lockfile A; capture its WAL;
	// replay into a fresh lockfile B; assert state convergence.
	wal := NewMemoryWAL()
	a, err := New(Config{
		SessionID: "session-restore",
		Emitter:   fabric.NoOpEmitter{},
		WAL:       wal,
	})
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	_, _ = a.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-1"))
	_, _ = a.Pin(samplePinRequest("python", "pytest", "8.3.2", "pipe-2"))
	_, _ = a.Pin(samplePinRequest("rust", "ripgrep", "14.1.0", "pipe-1"))
	_, _ = a.RemoveWitness(Coordinate{Ecosystem: "python", Name: "pytest"}, "8.3.2", "pipe-1")

	// Fresh lockfile, separate WAL — replay events from A's WAL.
	b, err := New(Config{
		SessionID: "session-restore",
		Emitter:   fabric.NoOpEmitter{},
		WAL:       NewMemoryWAL(),
	})
	if err != nil {
		t.Fatalf("New B: %v", err)
	}
	if err := b.Restore(wal); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Both lockfiles should report the same Stat counts.
	statA := a.Stat()
	statB := b.Stat()
	if statA != statB {
		t.Errorf("Stat divergence after restore: A=%+v B=%+v", statA, statB)
	}
	// And the same pin set.
	snapA := a.Snapshot()
	snapB := b.Snapshot()
	if len(snapA.Pins) != len(snapB.Pins) {
		t.Fatalf("Snapshot pin count divergence: A=%d B=%d", len(snapA.Pins), len(snapB.Pins))
	}
	for i := range snapA.Pins {
		assertPinEquivalent(t, &snapA.Pins[i], &snapB.Pins[i])
	}
}

func TestRestore_HandlesEmptyWAL(t *testing.T) {
	l, err := New(Config{SessionID: "empty"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Restore(NewMemoryWAL()); err != nil {
		t.Errorf("Restore on empty WAL should be no-op, got %v", err)
	}
	if l.Stat().PinCount != 0 {
		t.Error("Restore on empty WAL should leave lockfile empty")
	}
}

func assertPinEquivalent(t *testing.T, a, b *Pin) {
	t.Helper()
	if a.Coordinate != b.Coordinate {
		t.Errorf("Coordinate divergence: A=%s B=%s", a.Coordinate, b.Coordinate)
	}
	if a.Version != b.Version {
		t.Errorf("Version divergence: A=%s B=%s", a.Version, b.Version)
	}
	if a.ManifestID != b.ManifestID {
		t.Errorf("ManifestID divergence: A=%s B=%s", a.ManifestID, b.ManifestID)
	}
	if len(a.Witnesses) != len(b.Witnesses) {
		t.Errorf("Witness count divergence: A=%d B=%d", len(a.Witnesses), len(b.Witnesses))
	}
}
