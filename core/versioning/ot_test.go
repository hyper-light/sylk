package versioning

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOTEngine(t *testing.T) {
	engine := NewOTEngine()
	assert.NotNil(t, engine)
}

func TestOTEngine_Transform_NilOperations(t *testing.T) {
	engine := NewOTEngine()

	_, err := engine.Transform(nil, nil)
	assert.ErrorIs(t, err, ErrNilOperation)

	op := createTestOp(OpInsert, 0, 0, "hello")
	_, err = engine.Transform(op, nil)
	assert.ErrorIs(t, err, ErrNilOperation)

	_, err = engine.Transform(nil, op)
	assert.ErrorIs(t, err, ErrNilOperation)
}

func TestOTEngine_Transform_NonOverlapping(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 0, "hello")
	op2 := createTestOp(OpInsert, 100, 100, "world")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, op1.Target.StartOffset, result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertInsert_SamePosition(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOpWithPipeline(OpInsert, 10, 10, "aaa", "pipeline-1")
	op2 := createTestOpWithPipeline(OpInsert, 10, 10, "bbb", "pipeline-2")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	// op1 has lower pipelineID so it stays at position 10
	assert.Equal(t, 10, result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertInsert_SamePosition_HigherPipeline(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOpWithPipeline(OpInsert, 10, 10, "aaa", "pipeline-2")
	op2 := createTestOpWithPipeline(OpInsert, 10, 10, "bbb", "pipeline-1")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	// op1 has higher pipelineID so it moves after op2's content
	assert.Equal(t, 10+len(op2.Content), result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertInsert_Op1First(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 5, 5, "aaa")
	op2 := createTestOp(OpInsert, 10, 10, "bbb")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 5, result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertInsert_Op2First(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOpWithPipeline(OpInsert, 15, 15, "aaa", "pipeline-b")
	op2 := createTestOpWithPipeline(OpInsert, 10, 10, "bbb", "pipeline-a")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 15+len(op2.Content), result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertDelete_InsertBefore(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 5, 5, "new")
	op2 := createTestOp(OpDelete, 10, 20, "")
	op2.OldContent = []byte("deleted")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 5, result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertDelete_InsertAfter(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 25, 25, "new")
	op2 := createTestOp(OpDelete, 10, 20, "")
	op2.OldContent = []byte("deleted")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 15, result.Target.StartOffset)
}

func TestOTEngine_Transform_InsertDelete_InsertInside(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 15, 15, "new")
	op2 := createTestOp(OpDelete, 10, 20, "")
	op2.OldContent = []byte("deleted")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 10, result.Target.StartOffset)
}

func TestOTEngine_Transform_DeleteInsert_InsertBefore(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 20, 30, "")
	op1.OldContent = []byte("deleted")
	op2 := createTestOp(OpInsert, 5, 5, "inserted")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 20+len(op2.Content), result.Target.StartOffset)
	assert.Equal(t, 30+len(op2.Content), result.Target.EndOffset)
}

func TestOTEngine_Transform_DeleteInsert_InsertAfter(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 10, 20, "")
	op1.OldContent = []byte("deleted")
	op2 := createTestOp(OpInsert, 25, 25, "inserted")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 10, result.Target.StartOffset)
	assert.Equal(t, 20, result.Target.EndOffset)
}

func TestOTEngine_Transform_DeleteInsert_InsertInside(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 10, 30, "")
	op1.OldContent = []byte("deleted content here")
	op2 := createTestOp(OpInsert, 15, 15, "inserted")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 10, result.Target.StartOffset)
	assert.Equal(t, 30+len(op2.Content), result.Target.EndOffset)
}

func TestOTEngine_Transform_DeleteDelete_NoOverlap(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 0, 10, "")
	op2 := createTestOp(OpDelete, 20, 30, "")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Target.StartOffset)
	assert.Equal(t, 10, result.Target.EndOffset)
}

func TestOTEngine_Transform_DeleteDelete_Op1BeforeOp2(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 30, 40, "")
	op2 := createTestOp(OpDelete, 10, 20, "")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 20, result.Target.StartOffset)
	assert.Equal(t, 30, result.Target.EndOffset)
}

func TestOTEngine_Transform_DeleteDelete_FullyContained(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 15, 25, "")
	op2 := createTestOp(OpDelete, 10, 30, "")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 10, result.Target.StartOffset)
	assert.Equal(t, 10, result.Target.EndOffset)
}

func TestOTEngine_Transform_DeleteDelete_PartialOverlap(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 5, 20, "")
	op2 := createTestOp(OpDelete, 15, 25, "")

	result, err := engine.Transform(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, 5, result.Target.StartOffset)
}

func TestOTEngine_Transform_ReplaceReplace_Conflict(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpReplace, 10, 20, "new1")
	op1.OldContent = []byte("old")
	op2 := createTestOp(OpReplace, 15, 25, "new2")
	op2.OldContent = []byte("old")

	_, err := engine.Transform(op1, op2)
	assert.Error(t, err)

	var conflictErr *ConflictError
	assert.True(t, errors.As(err, &conflictErr))
}

func TestOTEngine_TransformBatch_Empty(t *testing.T) {
	engine := NewOTEngine()

	result, err := engine.TransformBatch([]*Operation{}, []*Operation{})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestOTEngine_TransformBatch_SingleOp(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 0, "hello")
	op2 := createTestOp(OpInsert, 100, 100, "world")

	result, err := engine.TransformBatch([]*Operation{op1}, []*Operation{op2})
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestOTEngine_TransformBatch_MultipleOps(t *testing.T) {
	engine := NewOTEngine()

	ops1 := []*Operation{
		createTestOp(OpInsert, 0, 0, "a"),
		createTestOp(OpInsert, 10, 10, "b"),
	}
	ops2 := []*Operation{
		createTestOp(OpInsert, 5, 5, "x"),
	}

	result, err := engine.TransformBatch(ops1, ops2)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestOTEngine_Compose_NilOperations(t *testing.T) {
	engine := NewOTEngine()

	_, err := engine.Compose(nil, nil)
	assert.ErrorIs(t, err, ErrNilOperation)
}

func TestOTEngine_Compose_DifferentFiles(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 0, "hello")
	op1.FilePath = "file1.go"
	op2 := createTestOp(OpInsert, 5, 5, "world")
	op2.FilePath = "file2.go"

	_, err := engine.Compose(op1, op2)
	assert.ErrorIs(t, err, ErrIncompatibleOps)
}

func TestOTEngine_Compose_AdjacentInserts(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 5, "hello")
	op2 := createTestOp(OpInsert, 5, 10, "world")

	result, err := engine.Compose(op1, op2)
	require.NoError(t, err)
	assert.Equal(t, []byte("helloworld"), result.Content)
}

func TestOTEngine_Compose_NonAdjacentInserts(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 5, "hello")
	op2 := createTestOp(OpInsert, 10, 15, "world")

	_, err := engine.Compose(op1, op2)
	assert.ErrorIs(t, err, ErrCompositionFailed)
}

func TestOTEngine_CanMergeAutomatically_NilOps(t *testing.T) {
	engine := NewOTEngine()

	assert.False(t, engine.CanMergeAutomatically(nil, nil))
}

func TestOTEngine_CanMergeAutomatically_NonOverlapping(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 0, "a")
	op2 := createTestOp(OpInsert, 100, 100, "b")

	assert.True(t, engine.CanMergeAutomatically(op1, op2))
}

func TestOTEngine_CanMergeAutomatically_OverlappingInserts(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 10, 10, "a")
	op2 := createTestOp(OpInsert, 10, 10, "b")

	assert.True(t, engine.CanMergeAutomatically(op1, op2))
}

func TestOTEngine_CanMergeAutomatically_OverlappingDeletes(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 10, 20, "")
	op2 := createTestOp(OpDelete, 15, 25, "")

	assert.True(t, engine.CanMergeAutomatically(op1, op2))
}

func TestOTEngine_CanMergeAutomatically_OverlappingReplaces(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpReplace, 10, 20, "a")
	op2 := createTestOp(OpReplace, 15, 25, "b")

	assert.False(t, engine.CanMergeAutomatically(op1, op2))
}

func TestOTEngine_DetectConflict_NilOps(t *testing.T) {
	engine := NewOTEngine()

	conflict := engine.DetectConflict(nil, nil)
	assert.Nil(t, conflict)
}

func TestOTEngine_DetectConflict_NonOverlapping(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 0, 0, "a")
	op2 := createTestOp(OpInsert, 100, 100, "b")

	conflict := engine.DetectConflict(op1, op2)
	assert.Nil(t, conflict)
}

func TestOTEngine_DetectConflict_OverlappingEdit(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpReplace, 10, 20, "a")
	op1.OldContent = []byte("old")
	op2 := createTestOp(OpReplace, 15, 25, "b")
	op2.OldContent = []byte("old")

	conflict := engine.DetectConflict(op1, op2)
	require.NotNil(t, conflict)
	assert.Equal(t, ConflictTypeOverlappingEdit, conflict.Type)
	assert.NotEmpty(t, conflict.Description)
	assert.NotEmpty(t, conflict.Resolutions)
}

func TestOTEngine_DetectConflict_DeleteEdit(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpDelete, 10, 20, "")
	op2 := createTestOp(OpReplace, 15, 25, "new")

	conflict := engine.DetectConflict(op1, op2)
	require.NotNil(t, conflict)
	assert.Equal(t, ConflictTypeDeleteEdit, conflict.Type)
}

func TestOTEngine_DetectConflict_MoveEdit(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpMove, 10, 20, "")
	op2 := createTestOp(OpReplace, 15, 25, "new")

	conflict := engine.DetectConflict(op1, op2)
	require.NotNil(t, conflict)
	assert.Equal(t, ConflictTypeMoveEdit, conflict.Type)
}

func TestOTEngine_DetectConflict_SemanticConflict(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOpWithAST(OpReplace, "node-123")
	op2 := createTestOpWithAST(OpReplace, "node-123")

	conflict := engine.DetectConflict(op1, op2)
	require.NotNil(t, conflict)
	assert.Equal(t, ConflictTypeSemanticConflict, conflict.Type)
}

func TestOTEngine_Symmetry(t *testing.T) {
	engine := NewOTEngine()

	op1 := createTestOp(OpInsert, 10, 10, "aaa")
	op2 := createTestOp(OpInsert, 10, 10, "bbb")

	t1, err1 := engine.Transform(op1, op2)
	t2, err2 := engine.Transform(op2, op1)

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotNil(t, t1)
	assert.NotNil(t, t2)
}

func TestOTEngine_Concurrent(t *testing.T) {
	engine := NewOTEngine()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			op1 := createTestOp(OpInsert, idx*10, idx*10, "content")
			op2 := createTestOp(OpInsert, idx*10+5, idx*10+5, "other")
			_, _ = engine.Transform(op1, op2)
		}(i)
	}

	wg.Wait()
}

func TestConflictType_String(t *testing.T) {
	tests := []struct {
		ct       ConflictType
		expected string
	}{
		{ConflictTypeOverlappingEdit, "overlapping_edit"},
		{ConflictTypeDeleteEdit, "delete_edit"},
		{ConflictTypeMoveEdit, "move_edit"},
		{ConflictTypeSemanticConflict, "semantic_conflict"},
		{ConflictType(99), "unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.ct.String())
	}
}

func TestConflictError_Error(t *testing.T) {
	conflict := &Conflict{
		Description: "test conflict",
	}
	err := &ConflictError{Conflict: conflict}

	assert.Equal(t, "conflict: test conflict", err.Error())
}

func TestConflictError_Unwrap(t *testing.T) {
	err := &ConflictError{Conflict: &Conflict{}}
	assert.ErrorIs(t, err, ErrConflict)
}

func createTestOp(opType OpType, start, end int, content string) *Operation {
	return &Operation{
		Type:       opType,
		Target:     NewOffsetTarget(start, end),
		Content:    []byte(content),
		FilePath:   "test.go",
		PipelineID: "pipeline-1",
		SessionID:  "session-1",
		Timestamp:  time.Now(),
	}
}

func createTestOpWithPipeline(opType OpType, start, end int, content, pipelineID string) *Operation {
	op := createTestOp(opType, start, end, content)
	op.PipelineID = pipelineID
	return op
}

func createTestOpWithAST(opType OpType, nodeID string) *Operation {
	return &Operation{
		Type: opType,
		Target: Target{
			NodePath: []string{"root", "func"},
			NodeType: "FunctionDecl",
			NodeID:   nodeID,
		},
		Content:    []byte("content"),
		FilePath:   "test.go",
		PipelineID: "pipeline-1",
		SessionID:  "session-1",
		Timestamp:  time.Now(),
	}
}
