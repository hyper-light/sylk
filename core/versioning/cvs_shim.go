package versioning

import (
	"context"
	"errors"
	"time"
)

var ErrNotImplemented = errors.New("not implemented in new architecture")

// CVSShim implements the CVS interface by delegating to SessionVFS's new
// subsystems. This is a temporary bridge that allows existing consumers
// (VFSVolume.Mount, guardian skills, tui.go) to keep working during the
// transition from DefaultCVS to the new pipeline→MergePipe→GlobalVFS→Disk flow.
type CVSShim struct {
	session *SessionVFS
}

// NewCVSShim creates a CVS shim backed by the given SessionVFS.
func NewCVSShim(session *SessionVFS) *CVSShim {
	return &CVSShim{session: session}
}

func (c *CVSShim) Read(ctx context.Context, filePath string) ([]byte, error) {
	return c.session.globalVFS.Read(ctx, filePath)
}

func (c *CVSShim) Write(ctx context.Context, filePath string, content []byte, _ WriteMetadata) (VersionID, error) {
	if err := c.session.globalVFS.Write(ctx, filePath, content); err != nil {
		return VersionID{}, err
	}
	return VersionID{}, nil
}

func (c *CVSShim) Delete(ctx context.Context, filePath string, _ WriteMetadata) (VersionID, error) {
	if err := c.session.globalVFS.Delete(ctx, filePath); err != nil {
		return VersionID{}, err
	}
	return VersionID{}, nil
}

func (c *CVSShim) GetVersion(_ VersionID) (*FileVersion, error) {
	return nil, ErrNotImplemented
}

func (c *CVSShim) GetHead(_ string) (*FileVersion, error) {
	return nil, ErrNotImplemented
}

func (c *CVSShim) GetHistory(_ string, _ HistoryOptions) ([]*FileVersion, error) {
	return nil, ErrNotImplemented
}

func (c *CVSShim) BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error) {
	return c.session.BeginPipeline(cfg)
}

func (c *CVSShim) CommitPipeline(pipelineID string) ([]VersionID, error) {
	ver, err := c.session.CommitPipeline(pipelineID)
	if err != nil {
		return nil, err
	}
	// Return a synthetic VersionID (unused by callers that go through new path).
	_ = ver
	return nil, nil
}

func (c *CVSShim) RollbackPipeline(pipelineID string) error {
	return c.session.RollbackPipeline(pipelineID)
}

func (c *CVSShim) Merge(_ context.Context, _, _ VersionID, _ ConflictResolver) (VersionID, error) {
	return VersionID{}, ErrNotImplemented
}

func (c *CVSShim) ThreeWayMerge(_ context.Context, _, _, _ VersionID, _ ConflictResolver) (VersionID, error) {
	return VersionID{}, ErrNotImplemented
}

func (c *CVSShim) AcquireFileLock(_ string, _ SessionID, _ time.Duration) (*FileLock, error) {
	return nil, ErrNotImplemented
}

func (c *CVSShim) ReleaseFileLock(_ string) error {
	return ErrNotImplemented
}

func (c *CVSShim) RefreshFileLock(_ string, _ time.Duration) error {
	return ErrNotImplemented
}

func (c *CVSShim) Subscribe(_ string, _ SessionID, _ FileChangeCallback) (SubscriptionID, error) {
	return "", ErrNotImplemented
}

func (c *CVSShim) Unsubscribe(_ SubscriptionID) error {
	return ErrNotImplemented
}

func (c *CVSShim) Stats() CVSStats {
	ver := c.session.wal.CurrentVersion()
	mods := c.session.globalVFS.GetModifications()

	return CVSStats{
		TotalFiles:    int64(len(mods)),
		TotalVersions: int64(ver.Major)*1000 + int64(ver.Minor),
		ActivePipelines: func() int64 {
			stats := c.session.vfsManager.Stats()
			return int64(stats.ActiveVFSes)
		}(),
	}
}

func (c *CVSShim) Close() error {
	// SessionVFS.Close handles everything.
	_ = c
	return nil
}
