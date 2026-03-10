package versioning

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

func preciseOperationsForModification(m FileModification) []*Operation {
	return preciseOperationsForChange(m.OriginalPath, m.OldContent, m.NewContent, "")
}

func preciseOperationsForDelta(d WALFileDelta, pipelineID string) []*Operation {
	return preciseOperationsForChange(d.Path, d.OldContent, d.NewContent, pipelineID)
}

func preciseOperationsForChange(path string, oldContent, newContent []byte, pipelineID string) []*Operation {
	if bytes.Equal(oldContent, newContent) {
		return nil
	}

	engine := dmp.New()
	diffs := engine.DiffMain(string(oldContent), string(newContent), false)
	engine.DiffCleanupEfficiency(diffs)

	ops := make([]*Operation, 0, len(diffs))
	oldOffset := 0
	for _, diff := range diffs {
		text := []byte(diff.Text)
		switch diff.Type {
		case dmp.DiffEqual:
			oldOffset += len(text)
		case dmp.DiffDelete:
			op := NewOperation(
				VersionID{},
				path,
				NewOffsetTarget(oldOffset, oldOffset+len(text)),
				OpDelete,
				nil,
				text,
				pipelineID,
				"",
				"",
				NewVectorClock(),
			)
			ops = append(ops, cloneOperationPtr(op))
			oldOffset += len(text)
		case dmp.DiffInsert:
			op := NewOperation(
				VersionID{},
				path,
				NewOffsetTarget(oldOffset, oldOffset),
				OpInsert,
				text,
				nil,
				pipelineID,
				"",
				"",
				NewVectorClock(),
			)
			ops = append(ops, cloneOperationPtr(op))
		}
	}
	return ops
}

func cloneOperationPtr(op Operation) *Operation {
	cloned := op.Clone()
	return &cloned
}

func isNoOpOperation(op *Operation) bool {
	if op == nil {
		return true
	}
	switch op.Type {
	case OpInsert:
		return len(op.Content) == 0
	case OpDelete:
		return op.Target.StartOffset == op.Target.EndOffset
	case OpReplace:
		return op.Target.StartOffset == op.Target.EndOffset && len(op.Content) == 0
	default:
		return false
	}
}

func (mp *MergePipe) transformOperation(ctx context.Context, op *Operation, remote []*Operation, prior []*Operation) (*Operation, error) {
	current := cloneOperationPtr(op.Clone())
	var err error
	for _, against := range remote {
		if against == nil || against.FilePath != current.FilePath {
			continue
		}
		current, err = mp.transformSingle(ctx, current, against)
		if err != nil {
			return nil, err
		}
	}
	for _, applied := range prior {
		if applied == nil || applied.FilePath != current.FilePath {
			continue
		}
		current, err = mp.transformSingle(ctx, current, applied)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

func (mp *MergePipe) transformSingle(ctx context.Context, op *Operation, against *Operation) (*Operation, error) {
	transformed, err := mp.ot.Transform(op, against)
	if err == nil {
		return transformed, nil
	}

	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) || mp.resolver == nil {
		return nil, err
	}

	resolution, resolveErr := mp.resolver.ResolveConflict(ctx, conflictErr.Conflict)
	if resolveErr != nil {
		return nil, resolveErr
	}
	if resolution == nil || resolution.ResultOp == nil {
		return nil, err
	}
	return cloneOperationPtr(resolution.ResultOp.Clone()), nil
}

func (mp *MergePipe) applyTransformedModToGlobal(ctx context.Context, tm transformedFileMod) (*WALFileDelta, error) {
	current, exists, err := mp.readGlobalDraftContent(ctx, tm.mod.OriginalPath)
	if err != nil {
		return nil, err
	}

	switch tm.mod.Operation {
	case FileOpDelete:
		if !exists {
			return nil, nil
		}
		if err := mp.globalVFS.Delete(ctx, tm.mod.OriginalPath); err != nil && !errors.Is(err, ErrFileNotFound) {
			return nil, err
		}
		return &WALFileDelta{
			Path:       tm.mod.OriginalPath,
			Op:         WALDeltaOpDelete,
			OldContent: current,
		}, nil
	default:
		target := cloneBytes(current)
		if len(tm.ops) == 0 {
			target = cloneBytes(tm.mod.NewContent)
		} else {
			target, err = applyOperationsToContent(current, tm.ops)
			if err != nil {
				return nil, err
			}
		}
		if exists && bytes.Equal(current, target) {
			return nil, nil
		}
		if !exists && bytes.Equal(target, nil) && tm.mod.Operation != FileOpCreate {
			return nil, nil
		}
		if err := mp.globalVFS.Write(ctx, tm.mod.OriginalPath, target); err != nil {
			return nil, err
		}
		return &WALFileDelta{
			Path:       tm.mod.OriginalPath,
			Op:         walDeltaOpForGlobalWrite(exists),
			NewContent: target,
			OldContent: current,
		}, nil
	}
}

func walDeltaOpForGlobalWrite(exists bool) WALDeltaOp {
	if exists {
		return WALDeltaOpModify
	}
	return WALDeltaOpCreate
}

func (mp *MergePipe) readGlobalDraftContent(ctx context.Context, path string) ([]byte, bool, error) {
	content, err := mp.globalVFS.Read(ctx, path)
	if err == nil {
		return content, true, nil
	}
	if errors.Is(err, ErrFileNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func applyOperationsToContent(current []byte, ops []*Operation) ([]byte, error) {
	result := cloneBytes(current)
	for _, op := range ops {
		if op == nil {
			continue
		}
		start := op.Target.StartOffset
		end := op.Target.EndOffset
		if start < 0 || end < start || end > len(result) {
			return nil, fmt.Errorf("apply transformed operation: %w", ErrInvalidOperation)
		}
		switch op.Type {
		case OpInsert:
			result = applyByteInsert(result, start, op.Content)
		case OpDelete:
			result = applyByteDelete(result, start, end)
		case OpReplace:
			result = applyByteReplace(result, start, end, op.Content)
		default:
			return nil, fmt.Errorf("apply transformed operation: unsupported op %s", op.Type.String())
		}
	}
	return result, nil
}

func applyByteInsert(content []byte, at int, insert []byte) []byte {
	result := make([]byte, 0, len(content)+len(insert))
	result = append(result, content[:at]...)
	result = append(result, insert...)
	result = append(result, content[at:]...)
	return result
}

func applyByteDelete(content []byte, start, end int) []byte {
	result := make([]byte, 0, len(content)-(end-start))
	result = append(result, content[:start]...)
	result = append(result, content[end:]...)
	return result
}

func applyByteReplace(content []byte, start, end int, replacement []byte) []byte {
	result := make([]byte, 0, len(content)-(end-start)+len(replacement))
	result = append(result, content[:start]...)
	result = append(result, replacement...)
	result = append(result, content[end:]...)
	return result
}
