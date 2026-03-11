package versioning

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var ErrDiskExportDisabled = errors.New("disk export is disabled for this session")

// FlushResult summarizes the outcome of flushing the global VFS to disk.
type FlushResult struct {
	Version      SemanticVersion
	FilesWritten int
	FilesDeleted int
	BytesWritten int64
}

// DiskFlusherConfig configures a DiskFlusher.
type DiskFlusherConfig struct {
	GlobalVFS       *PipelineVFS
	WAL             SemanticWAL
	SnapshotFS      vfsMutableBaseFS
	WorkingDir      string
	DraftMu         *sync.Mutex
	AllowDiskExport bool
}

// DiskFlusher writes global VFS overlay changes to disk and creates
// WAL checkpoint entries (major version bumps).
type DiskFlusher struct {
	globalVFS       *PipelineVFS
	wal             SemanticWAL
	snapshotFS      vfsMutableBaseFS
	workingDir      string
	draftMu         *sync.Mutex
	allowDiskExport bool
}

// NewDiskFlusher creates a new DiskFlusher.
func NewDiskFlusher(cfg DiskFlusherConfig) *DiskFlusher {
	return &DiskFlusher{
		globalVFS:       cfg.GlobalVFS,
		wal:             cfg.WAL,
		snapshotFS:      cfg.SnapshotFS,
		workingDir:      cfg.WorkingDir,
		draftMu:         cfg.DraftMu,
		allowDiskExport: cfg.AllowDiskExport,
	}
}

// PendingChanges returns the current set of modifications in the global VFS overlay.
func (df *DiskFlusher) PendingChanges() []FileModification {
	return df.globalVFS.GetModifications()
}

// Flush writes all pending changes from the global VFS to disk,
// creates a WAL checkpoint (major version bump), and clears the overlay.
func (df *DiskFlusher) Flush(ctx context.Context) (*FlushResult, error) {
	if !df.allowDiskExport {
		return nil, ErrDiskExportDisabled
	}
	if df.draftMu != nil {
		df.draftMu.Lock()
		defer df.draftMu.Unlock()
	}

	mods := df.globalVFS.GetModifications()
	if len(mods) == 0 {
		return &FlushResult{Version: df.wal.CurrentVersion()}, nil
	}

	result := &FlushResult{}
	plans, deltas, err := df.buildFlushPlans(ctx, mods)
	if err != nil {
		return result, err
	}
	defer cleanupFlushPlans(plans)

	if err := df.applyFlushPlans(ctx, plans, result); err != nil {
		if rollbackErr := df.rollbackFlushPlans(plans); rollbackErr != nil {
			return result, fmt.Errorf("disk flusher: apply flush plans: %w (rollback failed: %v)", err, rollbackErr)
		}
		return result, err
	}

	ver, err := df.wal.AppendCheckpoint(deltas)
	if err != nil {
		if rollbackErr := df.rollbackFlushPlans(plans); rollbackErr != nil {
			return result, fmt.Errorf("disk flusher: checkpoint: %w (rollback failed: %v)", err, rollbackErr)
		}
		return result, fmt.Errorf("disk flusher: checkpoint: %w", err)
	}
	result.Version = ver
	if df.snapshotFS != nil {
		if err := df.snapshotFS.ApplyModifications(mods); err != nil {
			return result, fmt.Errorf("disk flusher: advance RAM base: %w", err)
		}
	}

	df.globalVFS.ResetOverlay()
	return result, nil
}

type flushPlan struct {
	mod        FileModification
	resolved   string
	oldContent []byte
	existed    bool
	tempPath   string
	applied    bool
}

func (df *DiskFlusher) buildFlushPlans(ctx context.Context, mods []FileModification) ([]flushPlan, []WALFileDelta, error) {
	plans := make([]flushPlan, 0, len(mods))
	deltas := make([]WALFileDelta, 0, len(mods))

	for _, mod := range mods {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}

		resolved := df.resolve(mod.OriginalPath)
		oldContent, existed := df.readDiskContent(resolved)
		plan := flushPlan{
			mod:        mod,
			resolved:   resolved,
			oldContent: oldContent,
			existed:    existed,
		}

		if mod.Operation != FileOpDelete && mod.Operation != FileOpMkdir {
			tempPath, err := df.stageTempFile(resolved, mod.NewContent)
			if err != nil {
				return nil, nil, err
			}
			plan.tempPath = tempPath
		}

		deltas = append(deltas, WALFileDelta{
			Path:       mod.OriginalPath,
			Op:         WALDeltaOpFromFileOp(mod.Operation),
			NewContent: cloneBytes(mod.NewContent),
			OldContent: cloneBytes(oldContent),
		})
		plans = append(plans, plan)
	}

	return plans, deltas, nil
}

func (df *DiskFlusher) applyFlushPlans(ctx context.Context, plans []flushPlan, result *FlushResult) error {
	for i := range plans {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := df.applyFlushPlan(&plans[i], result); err != nil {
			return err
		}
	}
	return nil
}

func (df *DiskFlusher) applyFlushPlan(plan *flushPlan, result *FlushResult) error {
	switch plan.mod.Operation {
	case FileOpDelete:
		if err := df.deleteFromDisk(plan.resolved); err != nil {
			return err
		}
		result.FilesDeleted++
	case FileOpMkdir:
		if err := df.mkdirOnDisk(plan.resolved); err != nil {
			return err
		}
	default:
		if err := df.commitTempFile(plan.tempPath, plan.resolved); err != nil {
			return err
		}
		result.FilesWritten++
		result.BytesWritten += int64(len(plan.mod.NewContent))
	}
	plan.applied = true
	return nil
}

func (df *DiskFlusher) rollbackFlushPlans(plans []flushPlan) error {
	var firstErr error
	for i := len(plans) - 1; i >= 0; i-- {
		plan := plans[i]
		if !plan.applied {
			continue
		}
		var err error
		switch {
		case plan.mod.Operation == FileOpMkdir:
			if plan.existed {
				err = nil
			} else {
				err = df.deleteEmptyDir(plan.resolved)
			}
		case plan.existed:
			err = df.writeToDisk(plan.resolved, plan.oldContent)
		default:
			err = df.deleteFromDisk(plan.resolved)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func cleanupFlushPlans(plans []flushPlan) {
	for _, plan := range plans {
		if plan.tempPath == "" {
			continue
		}
		_ = os.Remove(plan.tempPath)
	}
}

func (df *DiskFlusher) stageTempFile(resolved string, content []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return "", fmt.Errorf("disk flusher: mkdir temp dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".sylk-flush-*")
	if err != nil {
		return "", fmt.Errorf("disk flusher: create temp file: %w", err)
	}
	defer tmp.Close()

	if _, err := tmp.Write(content); err != nil {
		return "", fmt.Errorf("disk flusher: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("disk flusher: sync temp file: %w", err)
	}
	return tmp.Name(), nil
}

func (df *DiskFlusher) commitTempFile(tempPath, resolved string) error {
	if tempPath == "" {
		return fmt.Errorf("disk flusher: missing staged file for %s", resolved)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return fmt.Errorf("disk flusher: mkdir: %w", err)
	}
	if err := os.Rename(tempPath, resolved); err != nil {
		return fmt.Errorf("disk flusher: rename temp file: %w", err)
	}
	return nil
}

// RollbackToVersion restores state to a target version.
// Major version targets: reverse deltas in both disk and global VFS.
// Minor version targets: reverse deltas in global VFS overlay only.
func (df *DiskFlusher) RollbackToVersion(ctx context.Context, target SemanticVersion) error {
	if !df.allowDiskExport {
		return ErrDiskExportDisabled
	}
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
	case WALDeltaOpMkdir:
		if toDisk {
			return df.deleteEmptyDir(df.resolve(d.Path))
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

func (df *DiskFlusher) readDiskContent(resolved string) ([]byte, bool) {
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, false
	}
	return content, true
}

func (df *DiskFlusher) writeToDisk(resolved string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return fmt.Errorf("disk flusher: mkdir: %w", err)
	}
	return os.WriteFile(resolved, content, 0644)
}

func (df *DiskFlusher) mkdirOnDisk(resolved string) error {
	if err := os.MkdirAll(resolved, 0755); err != nil {
		return fmt.Errorf("disk flusher: mkdir dir: %w", err)
	}
	return nil
}

func (df *DiskFlusher) deleteFromDisk(resolved string) error {
	err := os.Remove(resolved)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disk flusher: remove: %w", err)
	}
	return nil
}

func (df *DiskFlusher) deleteEmptyDir(resolved string) error {
	err := os.Remove(resolved)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disk flusher: remove dir: %w", err)
	}
	return nil
}
