package lock

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/substrate/fabric"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Lockfile is the substrate's per-session pin store.
//
// All operations are safe for concurrent use. The CRDT property
// (witness append is idempotent and order-independent) is preserved
// under concurrent Pin/AddWitness/RemoveWitness calls.
type Lockfile struct {
	sessionID string
	emitter   fabric.Emitter
	wal       WAL

	mu   sync.RWMutex
	pins map[Coordinate]*coordinateBucket
}

// coordinateBucket holds all currently-pinned versions of one
// Coordinate. Versions are stored in stable insertion order so
// snapshots are reproducible.
type coordinateBucket struct {
	pinsByVersion map[string]*Pin
	versionOrder  []string
}

// Config wires the lockfile's collaborators.
type Config struct {
	// SessionID identifies the session this lockfile belongs to.
	// Required.
	SessionID string

	// Emitter receives Activity Fabric events on every state change.
	// Optional; defaults to fabric.NoOpEmitter.
	Emitter fabric.Emitter

	// WAL persists state changes for replay across process restarts.
	// Optional; defaults to NewMemoryWAL (in-memory, lost on restart).
	WAL WAL
}

// New constructs a Lockfile.
func New(cfg Config) (*Lockfile, error) {
	if cfg.SessionID == "" {
		return nil, ErrSessionIDRequired
	}
	return &Lockfile{
		sessionID: cfg.SessionID,
		emitter:   defaultEmitter(cfg.Emitter),
		wal:       defaultWAL(cfg.WAL),
		pins:      make(map[Coordinate]*coordinateBucket),
	}, nil
}

func defaultEmitter(e fabric.Emitter) fabric.Emitter {
	if e == nil {
		return fabric.NoOpEmitter{}
	}
	return e
}

func defaultWAL(w WAL) WAL {
	if w == nil {
		return NewMemoryWAL()
	}
	return w
}

// SessionID returns the session this lockfile belongs to.
func (l *Lockfile) SessionID() string { return l.sessionID }

// Pin records that pipelineID has pinned manifestID at (coord, version)
// under the given constraint.
//
// If no Pin exists for (coord, version), one is created with this as
// its first witness. Otherwise, the witness is appended to the existing
// Pin (idempotent: re-adding the same (PipelineID, Constraint) is a
// no-op rather than a duplicate).
//
// Returns the resulting Pin and what changed.
func (l *Lockfile) Pin(req PinRequest) (PinOutcome, error) {
	if err := validatePinRequest(req); err != nil {
		return PinOutcome{}, err
	}
	outcome := l.applyPin(req)
	l.recordPin(req, outcome)
	l.emitPin(req, outcome)
	return outcome, nil
}

func validatePinRequest(req PinRequest) error {
	if req.Coordinate.IsZero() {
		return ErrEmptyCoordinate
	}
	if req.Version == "" {
		return ErrEmptyVersion
	}
	if req.ManifestID.IsZero() {
		return ErrEmptyManifestID
	}
	if req.Witness.PipelineID == "" {
		return ErrEmptyWitnessPipeline
	}
	return nil
}

func (l *Lockfile) applyPin(req PinRequest) PinOutcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.bucketFor(req.Coordinate)
	pin, exists := bucket.pinsByVersion[req.Version]
	if !exists {
		newPin := buildPinFromRequest(req)
		bucket.pinsByVersion[req.Version] = newPin
		bucket.versionOrder = append(bucket.versionOrder, req.Version)
		return PinOutcome{Pin: newPin, Created: true, WitnessAdded: true}
	}
	added := appendWitnessIfNew(pin, req.Witness)
	return PinOutcome{Pin: pin, Created: false, WitnessAdded: added}
}

func (l *Lockfile) bucketFor(coord Coordinate) *coordinateBucket {
	bucket, ok := l.pins[coord]
	if !ok {
		bucket = &coordinateBucket{pinsByVersion: make(map[string]*Pin)}
		l.pins[coord] = bucket
	}
	return bucket
}

func buildPinFromRequest(req PinRequest) *Pin {
	witness := normalizeWitnessTimestamp(req.Witness)
	return &Pin{
		Coordinate: req.Coordinate,
		Version:    req.Version,
		ManifestID: req.ManifestID,
		RecipeID:   req.RecipeID,
		Source:     req.Source,
		PinnedAt:   witness.AddedAt,
		Witnesses:  []Witness{witness},
		Closure:    copyClosure(req.Closure),
	}
}

// copyClosure returns a defensive copy of the recipe ID slice so the
// caller's slice header changes do not leak into the lockfile's stored
// pin.
func copyClosure(in []recipe.ID) []recipe.ID {
	out := make([]recipe.ID, len(in))
	copy(out, in)
	return out
}

func normalizeWitnessTimestamp(w Witness) Witness {
	if w.AddedAt.IsZero() {
		w.AddedAt = time.Now()
	}
	return w
}

// appendWitnessIfNew appends w to pin.Witnesses unless an equivalent
// witness (same PipelineID and Constraint) is already present. Returns
// true when the witness set changed.
func appendWitnessIfNew(pin *Pin, w Witness) bool {
	if witnessExists(pin, w) {
		return false
	}
	pin.Witnesses = append(pin.Witnesses, normalizeWitnessTimestamp(w))
	return true
}

func witnessExists(pin *Pin, w Witness) bool {
	for i := range pin.Witnesses {
		if pin.Witnesses[i].PipelineID == w.PipelineID &&
			pin.Witnesses[i].Constraint == w.Constraint {
			return true
		}
	}
	return false
}

func (l *Lockfile) recordPin(req PinRequest, outcome PinOutcome) {
	if outcome.Created {
		_ = l.wal.Append(Event{
			Op:        OpPinCreate,
			Timestamp: outcome.Pin.PinnedAt,
			Payload:   PinCreatePayload{Request: req},
		})
		return
	}
	if outcome.WitnessAdded {
		_ = l.wal.Append(Event{
			Op:        OpAddWitness,
			Timestamp: time.Now(),
			Payload:   AddWitnessPayload{Coordinate: req.Coordinate, Version: req.Version, Witness: req.Witness},
		})
	}
}

func (l *Lockfile) emitPin(req PinRequest, outcome PinOutcome) {
	if outcome.Created {
		l.emitter.Emit(fabric.Activity{
			Kind:  fabric.ToolPinned,
			Scope: pinScope(req.Coordinate),
			Payload: map[string]any{
				"coordinate":  req.Coordinate.String(),
				"version":     req.Version,
				"manifest_id": req.ManifestID.String(),
				"recipe_id":   req.RecipeID.String(),
				"witness":     req.Witness.PipelineID,
				"constraint":  req.Witness.Constraint,
			},
		})
		return
	}
	if outcome.WitnessAdded {
		l.emitter.Emit(fabric.Activity{
			Kind:  fabric.ToolWitnessAdded,
			Scope: pinScope(req.Coordinate),
			Payload: map[string]any{
				"coordinate": req.Coordinate.String(),
				"version":    req.Version,
				"witness":    req.Witness.PipelineID,
				"constraint": req.Witness.Constraint,
			},
		})
	}
}

func pinScope(c Coordinate) string {
	return "tooling/" + c.Ecosystem + "/" + c.Name
}

// AddWitness is a thin wrapper around Pin for the case where the caller
// knows the pin already exists. Equivalent semantics to Pin (idempotent
// on duplicates), but signals intent — the caller is asserting they're
// not creating a new pin and won't be surprised by an outcome saying
// they did.
//
// Returns ErrPinNotFound if no Pin exists for (coord, version).
func (l *Lockfile) AddWitness(coord Coordinate, version string, w Witness) (PinOutcome, error) {
	if w.PipelineID == "" {
		return PinOutcome{}, ErrEmptyWitnessPipeline
	}
	outcome, err := l.appendWitnessOrFail(coord, version, w)
	if err != nil {
		return PinOutcome{}, err
	}
	if outcome.WitnessAdded {
		l.emitWitnessAdded(coord, version, w)
		_ = l.wal.Append(Event{
			Op:        OpAddWitness,
			Timestamp: time.Now(),
			Payload:   AddWitnessPayload{Coordinate: coord, Version: version, Witness: w},
		})
	}
	return outcome, nil
}

func (l *Lockfile) appendWitnessOrFail(coord Coordinate, version string, w Witness) (PinOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pin, ok := l.lookupPinLocked(coord, version)
	if !ok {
		return PinOutcome{}, fmt.Errorf("%w: %s@%s", ErrPinNotFound, coord, version)
	}
	added := appendWitnessIfNew(pin, w)
	return PinOutcome{Pin: pin, WitnessAdded: added}, nil
}

func (l *Lockfile) emitWitnessAdded(coord Coordinate, version string, w Witness) {
	l.emitter.Emit(fabric.Activity{
		Kind:  fabric.ToolWitnessAdded,
		Scope: pinScope(coord),
		Payload: map[string]any{
			"coordinate": coord.String(),
			"version":    version,
			"witness":    w.PipelineID,
			"constraint": w.Constraint,
		},
	})
}

// RemoveWitness drops one witness from the pin at (coord, version).
//
// If the pin has multiple witnesses, the witness identified by
// pipelineID is removed and the pin survives. If the pin has only this
// witness, the pin itself is removed and the outcome reports
// ManifestID so the caller can call manifest.Store.Detach.
//
// Returns ErrPinNotFound when no Pin exists for (coord, version).
// Returns ErrWitnessNotFound when the pin exists but has no witness
// from pipelineID.
func (l *Lockfile) RemoveWitness(coord Coordinate, version, pipelineID string) (RemovalOutcome, error) {
	outcome, manifestID, err := l.applyWitnessRemoval(coord, version, pipelineID)
	if err != nil {
		return RemovalOutcome{}, err
	}
	l.recordWitnessRemoval(coord, version, pipelineID, outcome.PinRemoved)
	l.emitWitnessRemoval(coord, version, pipelineID, outcome.PinRemoved, manifestID)
	return outcome, nil
}

func (l *Lockfile) applyWitnessRemoval(coord Coordinate, version, pipelineID string) (RemovalOutcome, manifest.ID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	pin, ok := l.lookupPinLocked(coord, version)
	if !ok {
		return RemovalOutcome{}, manifest.ID{}, fmt.Errorf("%w: %s@%s", ErrPinNotFound, coord, version)
	}
	if !removeWitnessFromPin(pin, pipelineID) {
		return RemovalOutcome{}, manifest.ID{}, fmt.Errorf("%w: pipeline %q on %s@%s",
			ErrWitnessNotFound, pipelineID, coord, version)
	}
	if len(pin.Witnesses) > 0 {
		return RemovalOutcome{
			PinRemoved:         false,
			ManifestID:         pin.ManifestID,
			RemainingWitnesses: len(pin.Witnesses),
		}, pin.ManifestID, nil
	}
	manifestID := pin.ManifestID
	l.removePinLocked(coord, version)
	return RemovalOutcome{PinRemoved: true, ManifestID: manifestID, RemainingWitnesses: 0}, manifestID, nil
}

func (l *Lockfile) lookupPinLocked(coord Coordinate, version string) (*Pin, bool) {
	bucket, ok := l.pins[coord]
	if !ok {
		return nil, false
	}
	pin, ok := bucket.pinsByVersion[version]
	return pin, ok
}

func removeWitnessFromPin(pin *Pin, pipelineID string) bool {
	for i := range pin.Witnesses {
		if pin.Witnesses[i].PipelineID == pipelineID {
			pin.Witnesses = append(pin.Witnesses[:i], pin.Witnesses[i+1:]...)
			return true
		}
	}
	return false
}

func (l *Lockfile) removePinLocked(coord Coordinate, version string) {
	bucket := l.pins[coord]
	delete(bucket.pinsByVersion, version)
	bucket.versionOrder = removeStringFromSlice(bucket.versionOrder, version)
	if len(bucket.pinsByVersion) == 0 {
		delete(l.pins, coord)
	}
}

func removeStringFromSlice(s []string, target string) []string {
	for i := range s {
		if s[i] == target {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func (l *Lockfile) recordWitnessRemoval(coord Coordinate, version, pipelineID string, pinRemoved bool) {
	_ = l.wal.Append(Event{
		Op:        OpRemoveWitness,
		Timestamp: time.Now(),
		Payload: RemoveWitnessPayload{
			Coordinate: coord, Version: version, PipelineID: pipelineID, PinRemoved: pinRemoved,
		},
	})
}

func (l *Lockfile) emitWitnessRemoval(coord Coordinate, version, pipelineID string, pinRemoved bool, manifestID manifest.ID) {
	if !pinRemoved {
		return
	}
	l.emitter.Emit(fabric.Activity{
		Kind:  fabric.ToolDetached,
		Scope: pinScope(coord),
		Payload: map[string]any{
			"coordinate":      coord.String(),
			"version":         version,
			"pipeline":        pipelineID,
			"manifest_id":     manifestID.String(),
			"final_witness":   true,
		},
	})
}

// PinsFor returns all currently-pinned versions for the coordinate, in
// stable insertion order. Returns nil if no pins exist for coord.
func (l *Lockfile) PinsFor(coord Coordinate) []*Pin {
	l.mu.RLock()
	defer l.mu.RUnlock()
	bucket, ok := l.pins[coord]
	if !ok {
		return nil
	}
	out := make([]*Pin, 0, len(bucket.versionOrder))
	for _, v := range bucket.versionOrder {
		out = append(out, bucket.pinsByVersion[v])
	}
	return out
}

// LookupVersion returns the pin for (coord, version) if present.
func (l *Lockfile) LookupVersion(coord Coordinate, version string) (*Pin, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.lookupPinLocked(coord, version)
}

// Snapshot takes a point-in-time copy of the lockfile.
//
// The returned Snapshot is safe for the caller to mutate; the lockfile
// retains no references to it. Coordinates and versions are sorted in
// the snapshot for reproducible serialization.
func (l *Lockfile) Snapshot() Snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	pins := collectPinsSorted(l.pins)
	return Snapshot{
		SessionID: l.sessionID,
		CreatedAt: time.Now(),
		Pins:      pins,
	}
}

func collectPinsSorted(byCoord map[Coordinate]*coordinateBucket) []Pin {
	coords := sortedCoords(byCoord)
	out := []Pin{}
	for _, c := range coords {
		out = appendVersionsSorted(out, byCoord[c])
	}
	return out
}

func sortedCoords(m map[Coordinate]*coordinateBucket) []Coordinate {
	coords := make([]Coordinate, 0, len(m))
	for c := range m {
		coords = append(coords, c)
	}
	sort.Slice(coords, func(i, j int) bool {
		return coords[i].String() < coords[j].String()
	})
	return coords
}

func appendVersionsSorted(out []Pin, bucket *coordinateBucket) []Pin {
	versions := append([]string(nil), bucket.versionOrder...)
	sort.Strings(versions)
	for _, v := range versions {
		out = append(out, copyPin(bucket.pinsByVersion[v]))
	}
	return out
}

func copyPin(p *Pin) Pin {
	out := *p
	out.Witnesses = append([]Witness(nil), p.Witnesses...)
	out.Closure = copyClosure(p.Closure)
	return out
}

// Stat returns counts useful for diagnostics.
func (l *Lockfile) Stat() Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := Stats{}
	for _, bucket := range l.pins {
		out.CoordinateCount++
		for _, p := range bucket.pinsByVersion {
			out.PinCount++
			out.WitnessCount += len(p.Witnesses)
		}
	}
	return out
}

// Stats summarizes the lockfile's contents.
type Stats struct {
	CoordinateCount int
	PinCount        int
	WitnessCount    int
}

// Errors.
var (
	ErrSessionIDRequired    = errors.New("lock: session ID required")
	ErrEmptyCoordinate      = errors.New("lock: coordinate is empty")
	ErrEmptyVersion         = errors.New("lock: version is empty")
	ErrEmptyManifestID      = errors.New("lock: manifest ID is empty")
	ErrEmptyWitnessPipeline = errors.New("lock: witness pipeline ID is empty")
	ErrPinNotFound          = errors.New("lock: pin not found")
	ErrWitnessNotFound      = errors.New("lock: witness not found")
)
