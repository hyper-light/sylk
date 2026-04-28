package forest

// newForestWithSnapshot constructs a bare *MemoryForest with the given
// snapshot pre-installed. Used by unit tests that exercise specific
// scoring or scheduling behaviors without spinning up the full tuner +
// store + DB stack.
//
// The returned forest has no DB, no maintenance loops, no projector —
// only the snapshot. Tests that need DB-bound paths must construct
// via newAsyncTestForest / newTestForestWithConfig instead.
func newForestWithSnapshot(hp *HyperParameters) *MemoryForest {
	if hp == nil {
		hp = PlaceholderHyperParameters()
	}
	if hp.Provenance == nil {
		hp.Provenance = allFieldsAs(SourcePlaceholder)
	}
	m := &MemoryForest{}
	m.snapshot.Store(hp)
	return m
}
