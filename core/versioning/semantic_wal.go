package versioning

// SemanticWAL is the live version log contract used by SessionVFS, MergePipe,
// DiskFlusher, and versioning skills. Session-scoped VFS uses an in-memory
// implementation; durable stores may provide their own implementation.
type SemanticWAL interface {
	AppendDelta(pipelineID string, deltas []WALFileDelta) (SemanticVersion, error)
	AppendCheckpoint(deltas []WALFileDelta) (SemanticVersion, error)
	GetEntry(ver SemanticVersion) (*VersionedWALEntry, error)
	GetDeltasSince(ver SemanticVersion) ([]VersionedWALEntry, error)
	GetDeltasInRange(from, to SemanticVersion) ([]VersionedWALEntry, error)
	CurrentVersion() SemanticVersion
	LatestCheckpoint() (SemanticVersion, bool)
	Compact() error
	IndexLen() int
	Close() error
}
