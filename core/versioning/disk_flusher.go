package versioning

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// FlushResult summarizes the outcome of flushing the global VFS to disk.
type FlushResult struct {
	Version      SemanticVersion
	FilesWritten int
	FilesDeleted int
	BytesWritten int64
}

// DiskFlusherConfig configures a DiskFlusher.
type DiskFlusherConfig struct {
	GlobalVFS  *PipelineVFS
	WAL        SemanticWAL
	WorkingDir string
}

// DiskFlusher writes global VFS overlay changes to disk and creates
// WAL checkpoint entries (major version bumps).
type DiskFlusher struct {
	globalVFS  *PipelineVFS
	wal        SemanticWAL
	workingDir string
}

// NewDiskFlusher creates a new DiskFlusher.
func NewDiskFlusher(cfg DiskFlusherConfig) *DiskFlusher {
	return &DiskFlusher{
		globalVFS:  cfg.GlobalVFS,
		wal:        cfg.WAL,
		workingDir: cfg.WorkingDir,
	}
}

// PendingChanges returns the current set of modifications in the global VFS overlay.
func (df *DiskFlusher) PendingChanges() []FileModification {
	return df.globalVFS.GetModifications()
}

// Flush writes all pending changes from the global VFS to disk,
// creates a WAL checkpoint (major version bump), and clears the overlay.
func (df *DiskFlusher) Flush(ctx context.Context) (*FlushResult, error) {
	mods := df.globalVFS.GetModifications()
	if len(mods) == 0 {
		return &FlushResult{Version: df.wal.CurrentVersion()}, nil
	}

	result := &FlushResult{}
	deltas := make([]WALFileDelta, 0, len(mods))

	for _, mod := range mods {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		delta, err := df.flushMod(mod, result)
		if err != nil {
			return result, err
		}
		deltas = append(deltas, delta)
	}

	ver, err := df.wal.AppendCheckpoint(deltas)
	if err != nil {
		return result, fmt.Errorf("disk flusher: checkpoint: %w", err)
	}
	result.Version = ver

	df.globalVFS.ResetOverlay()
	return result, nil
}

func (df *DiskFlusher) flushMod(mod FileModification, result *FlushResult) (WALFileDelta, error) {
	resolved := df.resolve(mod.OriginalPath)
	oldContent := df.readDiskContent(resolved)

	delta := WALFileDelta{
		Path:       mod.OriginalPath,
		Op:         WALDeltaOpFromFileOp(mod.Operation),
		NewContent: mod.NewContent,
		OldContent: oldContent,
	}

	switch mod.Operation {
	case FileOpDelete:
		if err := df.deleteFromDisk(resolved); err != nil {
			return delta, err
		}
		result.FilesDeleted++
	default:
		if err := df.writeToDisk(resolved, mod.NewContent); err != nil {
			return delta, err
		}
		result.FilesWritten++
		result.BytesWritten += int64(len(mod.NewContent))
	}

	return delta, nil
}

// RollbackToVersion restores state to a target version.
// Major version targets: reverse deltas in both disk and global VFS.
// Minor version targets: reverse deltas in global VFS overlay only.
func (df *DiskFlusher) RollbackToVersion(ctx context.Context, target SemanticVersion) error {
	current := df.wal.CurrentVersion()
	if target.Compare(current) >= 0 {
		return nil
	}

	entries, err := df.wal.GetDeltasInRange(
		target.BumpMinor(), // one above target (inclusive)
		current,
	)
	if err != nil {
		return fmt.Errorf("disk flusher: get deltas for rollback: %w", err)
	}

	isMajor := target.Minor == 0 && target.Major > 0

	// Apply inverse deltas in reverse order.
	for i := len(entries) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := df.applyInverseDeltas(ctx, entries[i].Deltas, isMajor); err != nil {
			return err
		}
	}

	return nil
}

func (df *DiskFlusher) applyInverseDeltas(ctx context.Context, deltas []WALFileDelta, toDisk bool) error {
	for _, d := range deltas {
		if err := df.applyInverseDelta(ctx, d, toDisk); err != nil {
			return err
		}
	}
	return nil
}

func (df *DiskFlusher) applyInverseDelta(ctx context.Context, d WALFileDelta, toDisk bool) error {
	switch d.Op {
	case WALDeltaOpCreate:
		// Inverse of create = delete.
		if err := df.globalVFS.Delete(ctx, d.Path); err != nil && err != ErrFileNotFound {
			return err
		}
		if toDisk {
			return df.deleteFromDisk(df.resolve(d.Path))
		}
	case WALDeltaOpDelete:
		// Inverse of delete = restore old content.
		if err := df.globalVFS.Write(ctx, d.Path, d.OldContent); err != nil {
			return err
		}
		if toDisk {
			return df.writeToDisk(df.resolve(d.Path), d.OldContent)
		}
	case WALDeltaOpModify:
		// Inverse of modify = restore old content.
		if err := df.globalVFS.Write(ctx, d.Path, d.OldContent); err != nil {
			return err
		}
		if toDisk {
			return df.writeToDisk(df.resolve(d.Path), d.OldContent)
		}
	}
	return nil
}

func (df *DiskFlusher) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(df.workingDir, path)
}

func (df *DiskFlusher) readDiskContent(resolved string) []byte {
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil
	}
	return content
}

func (df *DiskFlusher) writeToDisk(resolved string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return fmt.Errorf("disk flusher: mkdir: %w", err)
	}
	return os.WriteFile(resolved, content, 0644)
}

func (df *DiskFlusher) deleteFromDisk(resolved string) error {
	err := os.Remove(resolved)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disk flusher: remove: %w", err)
	}
	return nil
}
