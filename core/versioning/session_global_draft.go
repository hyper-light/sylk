package versioning

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

func (s *SessionVFS) ApplyGlobalWrite(ctx context.Context, path string, content []byte) error {
	mod, ok, err := s.buildGlobalWriteModification(ctx, path, content)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	_, err = s.mergeGlobalDraftModifications(ctx, path, []FileModification{mod})
	return err
}

func (s *SessionVFS) ApplyGlobalDelete(ctx context.Context, path string) error {
	mod, ok, err := s.buildGlobalDeleteModification(ctx, path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	_, err = s.mergeGlobalDraftModifications(ctx, path, []FileModification{mod})
	return err
}

func (s *SessionVFS) mergeGlobalDraftModifications(ctx context.Context, path string, mods []FileModification) (SemanticVersion, error) {
	if len(mods) == 0 {
		return s.CurrentVersion(), nil
	}
	pipelineID := fmt.Sprintf("global:%s:%d", path, time.Now().UnixNano())
	if err := s.mergePipe.RegisterPipelineAt(pipelineID, s.wal.CurrentVersion()); err != nil {
		return SemanticVersion{}, fmt.Errorf("session vfs: register global draft merge: %w", err)
	}
	ver, err := s.mergePipe.Merge(contextWithoutCancel(ctx), pipelineID, mods)
	if err != nil {
		return SemanticVersion{}, fmt.Errorf("session vfs: merge global draft: %w", err)
	}
	return ver, nil
}

func (s *SessionVFS) buildGlobalWriteModification(ctx context.Context, path string, content []byte) (FileModification, bool, error) {
	current, exists, resolved, err := s.readGlobalDraftFile(ctx, path)
	if err != nil {
		return FileModification{}, false, err
	}
	if exists && bytes.Equal(current, content) {
		return FileModification{}, false, nil
	}
	mod := FileModification{
		OriginalPath: resolved,
		Timestamp:    time.Now(),
		NewContent:   cloneBytes(content),
	}
	if exists {
		mod.Operation = FileOpModify
		mod.OldContent = cloneBytes(current)
	} else {
		mod.Operation = FileOpCreate
	}
	return mod, true, nil
}

func (s *SessionVFS) buildGlobalDeleteModification(ctx context.Context, path string) (FileModification, bool, error) {
	current, exists, resolved, err := s.readGlobalDraftFile(ctx, path)
	if err != nil {
		return FileModification{}, false, err
	}
	if !exists {
		return FileModification{}, false, nil
	}
	return FileModification{
		OriginalPath: resolved,
		Operation:    FileOpDelete,
		Timestamp:    time.Now(),
		OldContent:   cloneBytes(current),
	}, true, nil
}

func (s *SessionVFS) readGlobalDraftFile(ctx context.Context, path string) ([]byte, bool, string, error) {
	fa := NewVFSFileAccess(s.globalVFS, s.workingDir)
	resolved := fa.resolve(path)
	content, err := s.globalVFS.Read(ctx, resolved)
	if err == nil {
		return content, true, resolved, nil
	}
	if err == ErrFileNotFound {
		return nil, false, resolved, nil
	}
	return nil, false, resolved, err
}
