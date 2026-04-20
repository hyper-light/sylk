package lock

import (
	"sync"
	"time"
)

// WAL records lockfile state changes for replay across process
// restarts. The substrate's broader WAL infrastructure (in
// core/purevfs) will eventually be the canonical persistent backing,
// per the design doc's "extend purevfs WAL, don't add #5" rule. For
// Phase 3, the WAL interface is defined and an in-memory implementation
// is provided so the lockfile's replay semantics can be unit-tested
// independently of disk persistence.
type WAL interface {
	// Append records one event. Implementations must serialize event
	// ordering — a successful Append guarantees the event is observed
	// in the same position by any subsequent Replay.
	Append(event Event) error

	// Replay invokes handler for each previously-appended event in
	// arrival order. handler returning an error halts replay and
	// surfaces the error.
	Replay(handler func(Event) error) error
}

// Op enumerates the lockfile operations the WAL persists.
type Op int

const (
	// OpPinCreate records the first witness for (Coordinate, Version),
	// which causes a new Pin to be created.
	OpPinCreate Op = iota + 1

	// OpAddWitness records a subsequent witness appended to an
	// existing Pin.
	OpAddWitness

	// OpRemoveWitness records a witness being removed. When the
	// removal causes the last witness to leave, the WAL entry's
	// PinRemoved flag is true so replay reproduces the pin removal.
	OpRemoveWitness
)

// Event is one WAL record.
type Event struct {
	Op        Op
	Timestamp time.Time
	Payload   any // one of: PinCreatePayload, AddWitnessPayload, RemoveWitnessPayload
}

// PinCreatePayload carries the full PinRequest for OpPinCreate.
type PinCreatePayload struct {
	Request PinRequest
}

// AddWitnessPayload carries the (coord, version, witness) for OpAddWitness.
type AddWitnessPayload struct {
	Coordinate Coordinate
	Version    string
	Witness    Witness
}

// RemoveWitnessPayload carries the (coord, version, pipeline) for
// OpRemoveWitness.
type RemoveWitnessPayload struct {
	Coordinate Coordinate
	Version    string
	PipelineID string
	PinRemoved bool
}

// MemoryWAL is an in-memory WAL implementation. Suitable for tests and
// for sessions that don't need cross-restart persistence.
//
// Safe for concurrent use.
type MemoryWAL struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryWAL constructs an empty in-memory WAL.
func NewMemoryWAL() *MemoryWAL {
	return &MemoryWAL{}
}

// Append records the event.
func (w *MemoryWAL) Append(event Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	return nil
}

// Replay invokes handler for each previously-recorded event in arrival
// order. The handler may freely mutate the lockfile; the WAL takes a
// snapshot of its event list before invoking the handler so concurrent
// Append calls during replay are not observed by this Replay.
func (w *MemoryWAL) Replay(handler func(Event) error) error {
	snapshot := w.snapshot()
	for i := range snapshot {
		if err := handler(snapshot[i]); err != nil {
			return err
		}
	}
	return nil
}

func (w *MemoryWAL) snapshot() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Event, len(w.events))
	copy(out, w.events)
	return out
}

// Len returns the number of recorded events. Useful for tests.
func (w *MemoryWAL) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.events)
}

// Restore replays a WAL into the lockfile, recreating state from
// recorded events. The lockfile must be freshly constructed; calling
// Restore on a populated lockfile produces undefined behavior.
//
// Restore re-emits the WAL events into a fresh handler that applies
// them via the lockfile's normal Pin/AddWitness/RemoveWitness methods.
// This means restored state passes through the same validation and
// fabric emission paths as live operations — at the cost of double
// fabric emission. Callers that don't want fabric noise during replay
// should temporarily install a NoOpEmitter on the lockfile before
// Restore.
func (l *Lockfile) Restore(wal WAL) error {
	return wal.Replay(func(event Event) error {
		return l.applyReplayedEvent(event)
	})
}

func (l *Lockfile) applyReplayedEvent(event Event) error {
	switch event.Op {
	case OpPinCreate:
		return l.replayPinCreate(event)
	case OpAddWitness:
		return l.replayAddWitness(event)
	case OpRemoveWitness:
		return l.replayRemoveWitness(event)
	}
	return nil
}

func (l *Lockfile) replayPinCreate(event Event) error {
	payload, ok := event.Payload.(PinCreatePayload)
	if !ok {
		return ErrInvalidWALPayload
	}
	_, err := l.Pin(payload.Request)
	return err
}

func (l *Lockfile) replayAddWitness(event Event) error {
	payload, ok := event.Payload.(AddWitnessPayload)
	if !ok {
		return ErrInvalidWALPayload
	}
	_, err := l.AddWitness(payload.Coordinate, payload.Version, payload.Witness)
	return err
}

func (l *Lockfile) replayRemoveWitness(event Event) error {
	payload, ok := event.Payload.(RemoveWitnessPayload)
	if !ok {
		return ErrInvalidWALPayload
	}
	_, err := l.RemoveWitness(payload.Coordinate, payload.Version, payload.PipelineID)
	return err
}

// ErrInvalidWALPayload is returned when a WAL event's payload type
// doesn't match its Op (corrupt/foreign WAL).
var ErrInvalidWALPayload = walError("lock: WAL event payload type does not match Op")

type walError string

func (e walError) Error() string { return string(e) }
