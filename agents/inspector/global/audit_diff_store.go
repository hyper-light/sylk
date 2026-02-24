package global

import (
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/inspector/shared"
)

// gcDivisor derives the GC interval from MaxAge (MaxAge / gcDivisor).
const gcDivisor = 6

// AuditDiffStore holds pre-OT diff snapshots for layer auditing.
// Entries are bounded by the number of active DAGs and GC'd by age.
type AuditDiffStore struct {
	mu      sync.RWMutex
	entries map[shared.AuditKey]*shared.LayerAuditDiffs
	maxAge  time.Duration
	gcTick  *time.Ticker
	done    chan struct{}
}

// NewAuditDiffStore creates a diff store with age-based GC.
func NewAuditDiffStore(maxAge time.Duration) *AuditDiffStore {
	gcInterval := maxAge / gcDivisor

	ds := &AuditDiffStore{
		entries: make(map[shared.AuditKey]*shared.LayerAuditDiffs),
		maxAge:  maxAge,
		gcTick:  time.NewTicker(gcInterval),
		done:    make(chan struct{}),
	}

	go ds.gcLoop()
	return ds
}

// Store adds a node diff snapshot to the layer's entry.
func (ds *AuditDiffStore) Store(key shared.AuditKey, nodeID string, snap *shared.NodeDiffSnapshot) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	entry, ok := ds.entries[key]
	if !ok {
		entry = &shared.LayerAuditDiffs{
			Key:        key,
			NodeDiffs:  make(map[string]*shared.NodeDiffSnapshot),
			CapturedAt: time.Now(),
		}
		ds.entries[key] = entry
	}

	entry.NodeDiffs[nodeID] = snap
}

// Get returns the layer audit diffs for a key, or nil if not found.
func (ds *AuditDiffStore) Get(key shared.AuditKey) *shared.LayerAuditDiffs {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	return ds.entries[key]
}

// Delete removes an entry from the store.
func (ds *AuditDiffStore) Delete(key shared.AuditKey) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	delete(ds.entries, key)
}

// GC removes entries older than maxAge.
func (ds *AuditDiffStore) GC() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	cutoff := time.Now().Add(-ds.maxAge)
	for key, entry := range ds.entries {
		if entry.CapturedAt.Before(cutoff) {
			delete(ds.entries, key)
		}
	}
}

// Close stops the GC ticker.
func (ds *AuditDiffStore) Close() {
	ds.gcTick.Stop()
	close(ds.done)
}

func (ds *AuditDiffStore) gcLoop() {
	for {
		select {
		case <-ds.gcTick.C:
			ds.GC()
		case <-ds.done:
			return
		}
	}
}
