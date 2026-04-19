package quality

import (
	"fmt"

	"github.com/adalundhe/sylk/core/concurrency"
)

// SCORE-03 checkpoint integration. WeightStateProvider implements
// concurrency.CheckpointStateProvider: on every checkpoint tick the
// provider snapshots the session's weight state and writes it into the
// checkpoint's Extras map under weightStateExtrasKey. On restart,
// RestoreFromCheckpoint loads the snapshot so session weight learning
// survives suspend/resume and crash/recovery.
//
// The provider holds a pointer to the SessionWeightManager and writes its
// full effective state per domain. Sessions that only use one domain
// still pay the full snapshot cost — cheap at the scale of 8 LearnedWeight
// fields per domain, and saves an indirection to an opaque "dirty
// domains" list.

// weightStateExtrasKey is the checkpoint.Extras key this provider writes.
// Exported for symmetry with other providers (third parties can detect
// the key without importing this package).
const weightStateExtrasKey = "quality.weight_state.v1"

// WeightStateSnapshot is the checkpoint-durable shape of a session's
// weight state. Captures:
//   - The base QualityWeights at the moment of snapshot (so recovery can
//     reconstruct the overlay baseline).
//   - Every overlay entry keyed by (domain, signal).
//   - The fork version so a three-way merge with a concurrently-advanced
//     project base is possible on recovery.
type WeightStateSnapshot struct {
	SessionID    string                       `json:"session_id"`
	ForkVersion  uint64                       `json:"fork_version"`
	Base         *QualityWeights              `json:"base"`
	Overlay      []weightStateOverlayEntry    `json:"overlay,omitempty"`
	DirtyKeys    []weightStateOverlayEntryKey `json:"dirty_keys,omitempty"`
}

type weightStateOverlayEntry struct {
	Domain string         `json:"domain"`
	Signal string         `json:"signal"`
	Weight *LearnedWeight `json:"weight"`
}

type weightStateOverlayEntryKey struct {
	Domain string `json:"domain"`
	Signal string `json:"signal"`
}

// WeightStateProvider is the CheckpointStateProvider implementation for
// the quality layer. Construct with the session's manager; register with
// the Checkpointer alongside other providers.
type WeightStateProvider struct {
	manager *SessionWeightManager
}

// NewWeightStateProvider builds a provider around the given session
// manager. Rejects a nil manager: a provider without a manager has
// nothing to snapshot, and silent-no-op would hide configuration bugs.
func NewWeightStateProvider(manager *SessionWeightManager) (*WeightStateProvider, error) {
	if manager == nil {
		return nil, fmt.Errorf("quality: WeightStateProvider requires a session manager")
	}
	return &WeightStateProvider{manager: manager}, nil
}

// CollectState implements concurrency.CheckpointStateProvider. Called by
// the Checkpointer on each tick — the provider snapshots the session's
// weight state and writes it into cp.Extras.
func (p *WeightStateProvider) CollectState(cp *concurrency.Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("quality: nil checkpoint")
	}
	snapshot := p.Snapshot()
	return cp.SetExtra(weightStateExtrasKey, snapshot)
}

// Snapshot captures the current session state into a serializable
// WeightStateSnapshot. Exported separately so callers can snapshot
// out-of-band (e.g. for test observation or explicit suspend-to-disk).
func (p *WeightStateProvider) Snapshot() WeightStateSnapshot {
	p.manager.overlayMu.RLock()
	defer p.manager.overlayMu.RUnlock()

	snapshot := WeightStateSnapshot{
		SessionID:   p.manager.sessionID,
		ForkVersion: p.manager.forkVersion,
		Base:        p.manager.base.Load().Clone(),
	}
	if len(p.manager.overlay) > 0 {
		snapshot.Overlay = make([]weightStateOverlayEntry, 0, len(p.manager.overlay))
		for key, weight := range p.manager.overlay {
			snapshot.Overlay = append(snapshot.Overlay, weightStateOverlayEntry{
				Domain: key.Domain.String(),
				Signal: key.Signal.String(),
				Weight: weight.Clone(),
			})
		}
	}
	if len(p.manager.dirty) > 0 {
		snapshot.DirtyKeys = make([]weightStateOverlayEntryKey, 0, len(p.manager.dirty))
		for key := range p.manager.dirty {
			snapshot.DirtyKeys = append(snapshot.DirtyKeys, weightStateOverlayEntryKey{
				Domain: key.Domain.String(),
				Signal: key.Signal.String(),
			})
		}
	}
	return snapshot
}

// RestoreFromCheckpoint rebuilds a SessionWeightManager from a checkpoint.
// Used by the recovery path on startup. Looks up weightStateExtrasKey;
// returns (nil, nil) when the checkpoint contains no weight state (e.g.
// a v1 checkpoint from before the provider was wired) so callers fall
// back to priors-only initialization.
func RestoreFromCheckpoint(cp *concurrency.Checkpoint) (*SessionWeightManager, error) {
	if cp == nil {
		return nil, nil
	}
	var snapshot WeightStateSnapshot
	ok, err := cp.GetExtra(weightStateExtrasKey, &snapshot)
	if err != nil {
		return nil, fmt.Errorf("decode quality weight state: %w", err)
	}
	if !ok {
		return nil, nil
	}
	return restoreSession(snapshot)
}

// RestoreFromSnapshot rebuilds a SessionWeightManager directly from a
// snapshot structure — useful for tests and for non-checkpoint state
// transports (e.g. direct suspend-file loads).
func RestoreFromSnapshot(snapshot WeightStateSnapshot) (*SessionWeightManager, error) {
	return restoreSession(snapshot)
}

func restoreSession(snapshot WeightStateSnapshot) (*SessionWeightManager, error) {
	manager, err := NewSessionWeightManager(snapshot.SessionID, snapshot.Base)
	if err != nil {
		return nil, err
	}
	manager.SetForkVersion(snapshot.ForkVersion)
	if err := restoreOverlay(manager, snapshot.Overlay); err != nil {
		return nil, err
	}
	if err := restoreDirty(manager, snapshot.DirtyKeys); err != nil {
		return nil, err
	}
	return manager, nil
}

func restoreOverlay(manager *SessionWeightManager, entries []weightStateOverlayEntry) error {
	for _, entry := range entries {
		key, err := decodeOverlayKey(entry.Domain, entry.Signal)
		if err != nil {
			return err
		}
		manager.overlayMu.Lock()
		manager.overlay[key] = entry.Weight.Clone()
		manager.overlayMu.Unlock()
	}
	return nil
}

func restoreDirty(manager *SessionWeightManager, keys []weightStateOverlayEntryKey) error {
	for _, key := range keys {
		decoded, err := decodeOverlayKey(key.Domain, key.Signal)
		if err != nil {
			return err
		}
		manager.overlayMu.Lock()
		manager.dirty[decoded] = struct{}{}
		manager.overlayMu.Unlock()
	}
	return nil
}

// decodeOverlayKey parses the string-form (domain, signal) pair emitted by
// Snapshot. Unrecognized values are an error because silently dropping
// them on restore would produce a SessionWeightManager whose reads
// disagree with the pre-crash state — the opposite of what checkpoints
// exist to provide.
func decodeOverlayKey(domainStr, signalStr string) (overlayKey, error) {
	domain, ok := DomainFromString(domainStr)
	if !ok {
		return overlayKey{}, fmt.Errorf("quality: unknown domain %q in checkpoint overlay", domainStr)
	}
	signal, ok := SignalKindFromString(signalStr)
	if !ok {
		return overlayKey{}, fmt.Errorf("quality: unknown signal %q in checkpoint overlay", signalStr)
	}
	return overlayKey{Domain: domain, Signal: signal}, nil
}
