package versioning

import (
	"errors"
)

var (
	ErrConflict            = errors.New("conflict detected")
	ErrInvalidOperation    = errors.New("invalid operation")
	ErrIncompatibleOps     = errors.New("incompatible operations")
	ErrTransformFailed     = errors.New("transform failed")
	ErrNilOperation        = errors.New("nil operation")
	ErrEmptyBatch          = errors.New("empty operation batch")
	ErrCompositionFailed   = errors.New("composition failed")
	ErrIncompatibleTargets = errors.New("incompatible target types")
)

type ConflictType int

const (
	ConflictTypeOverlappingEdit ConflictType = iota
	ConflictTypeDeleteEdit
	ConflictTypeMoveEdit
	ConflictTypeSemanticConflict
)

var conflictTypeNames = map[ConflictType]string{
	ConflictTypeOverlappingEdit:  "overlapping_edit",
	ConflictTypeDeleteEdit:       "delete_edit",
	ConflictTypeMoveEdit:         "move_edit",
	ConflictTypeSemanticConflict: "semantic_conflict",
}

func (ct ConflictType) String() string {
	if name, ok := conflictTypeNames[ct]; ok {
		return name
	}
	return "unknown"
}

type Conflict struct {
	Op1         *Operation
	Op2         *Operation
	Type        ConflictType
	Description string
	Resolutions []Resolution
}

type Resolution struct {
	Label       string
	Description string
	ResultOp    *Operation
}

type OTEngine interface {
	Transform(op1, op2 *Operation) (*Operation, error)
	TransformBatch(ops1, ops2 []*Operation) ([]*Operation, error)
	Compose(op1, op2 *Operation) (*Operation, error)
	CanMergeAutomatically(op1, op2 *Operation) bool
	DetectConflict(op1, op2 *Operation) *Conflict
}

type DefaultOTEngine struct{}

func NewOTEngine() *DefaultOTEngine {
	return &DefaultOTEngine{}
}

func (e *DefaultOTEngine) Transform(op1, op2 *Operation) (*Operation, error) {
	if err := e.validateOperations(op1, op2); err != nil {
		return nil, err
	}

	if !e.targetsOverlap(op1, op2) {
		return e.transformNonOverlapping(op1, op2)
	}

	return e.transformOverlapping(op1, op2)
}

func (e *DefaultOTEngine) validateOperations(op1, op2 *Operation) error {
	if op1 == nil || op2 == nil {
		return ErrNilOperation
	}
	return nil
}

func (e *DefaultOTEngine) targetsOverlap(op1, op2 *Operation) bool {
	if op1.Target.Overlaps(op2.Target) {
		return true
	}
	return e.insertsAtSamePoint(op1, op2)
}

func (e *DefaultOTEngine) insertsAtSamePoint(op1, op2 *Operation) bool {
	bothInserts := op1.Type == OpInsert && op2.Type == OpInsert
	sameOffset := op1.Target.StartOffset == op2.Target.StartOffset
	return bothInserts && sameOffset
}

func (e *DefaultOTEngine) transformNonOverlapping(op1, op2 *Operation) (*Operation, error) {
	result := op1.Clone()
	result.Target = e.adjustTarget(op1.Target, op2)
	return &result, nil
}

func (e *DefaultOTEngine) adjustTarget(target Target, against *Operation) Target {
	if !target.IsOffset() || !against.Target.IsOffset() {
		return target
	}
	return e.adjustOffsetTarget(target, against)
}

func (e *DefaultOTEngine) adjustOffsetTarget(target Target, against *Operation) Target {
	delta := e.computeDelta(against)
	if target.StartOffset >= against.Target.EndOffset {
		return target.WithOffsets(target.StartOffset+delta, target.EndOffset+delta)
	}
	return target
}

func (e *DefaultOTEngine) computeDelta(op *Operation) int {
	switch op.Type {
	case OpInsert:
		return len(op.Content)
	case OpDelete:
		return -(op.Target.EndOffset - op.Target.StartOffset)
	default:
		return len(op.Content) - len(op.OldContent)
	}
}

func (e *DefaultOTEngine) transformOverlapping(op1, op2 *Operation) (*Operation, error) {
	switch {
	case op1.Type == OpInsert && op2.Type == OpInsert:
		return e.transformInsertInsert(op1, op2)
	case op1.Type == OpInsert && op2.Type == OpDelete:
		return e.transformInsertDelete(op1, op2)
	case op1.Type == OpDelete && op2.Type == OpInsert:
		return e.transformDeleteInsert(op1, op2)
	case op1.Type == OpDelete && op2.Type == OpDelete:
		return e.transformDeleteDelete(op1, op2)
	default:
		return e.transformReplaceReplace(op1, op2)
	}
}

func (e *DefaultOTEngine) transformInsertInsert(op1, op2 *Operation) (*Operation, error) {
	result := op1.Clone()

	if op1.Target.StartOffset < op2.Target.StartOffset {
		return &result, nil
	}

	if op1.Target.StartOffset == op2.Target.StartOffset {
		if op1.PipelineID < op2.PipelineID {
			return &result, nil
		}
	}

	result.Target = result.Target.WithOffsets(
		op1.Target.StartOffset+len(op2.Content),
		op1.Target.EndOffset+len(op2.Content),
	)
	return &result, nil
}

func (e *DefaultOTEngine) transformInsertDelete(op1, op2 *Operation) (*Operation, error) {
	result := op1.Clone()
	deleteStart := op2.Target.StartOffset
	deleteEnd := op2.Target.EndOffset
	insertPos := op1.Target.StartOffset

	if insertPos <= deleteStart {
		return &result, nil
	}

	if insertPos >= deleteEnd {
		delta := deleteEnd - deleteStart
		result.Target = result.Target.WithOffsets(insertPos-delta, insertPos-delta)
		return &result, nil
	}

	result.Target = result.Target.WithOffsets(deleteStart, deleteStart)
	return &result, nil
}

func (e *DefaultOTEngine) transformDeleteInsert(op1, op2 *Operation) (*Operation, error) {
	result := op1.Clone()
	insertPos := op2.Target.StartOffset
	insertLen := len(op2.Content)
	deleteStart := op1.Target.StartOffset
	deleteEnd := op1.Target.EndOffset

	if insertPos >= deleteEnd {
		return &result, nil
	}

	if insertPos <= deleteStart {
		result.Target = result.Target.WithOffsets(deleteStart+insertLen, deleteEnd+insertLen)
		return &result, nil
	}

	result.Target = result.Target.WithOffsets(deleteStart, deleteEnd+insertLen)
	return &result, nil
}

func (e *DefaultOTEngine) transformDeleteDelete(op1, op2 *Operation) (*Operation, error) {
	result := op1.Clone()
	s1, e1 := op1.Target.StartOffset, op1.Target.EndOffset
	s2, e2 := op2.Target.StartOffset, op2.Target.EndOffset

	if e1 <= s2 {
		return &result, nil
	}

	if s1 >= e2 {
		delta := e2 - s2
		result.Target = result.Target.WithOffsets(s1-delta, e1-delta)
		return &result, nil
	}

	return e.handleOverlappingDeletes(op1, s1, e1, s2, e2)
}

func (e *DefaultOTEngine) handleOverlappingDeletes(op1 *Operation, s1, e1, s2, e2 int) (*Operation, error) {
	result := op1.Clone()

	if s1 >= s2 && e1 <= e2 {
		result.Target = result.Target.WithOffsets(s2, s2)
		result.Content = nil
		result.OldContent = nil
		return &result, nil
	}

	newStart, newEnd := e.computeRemainingRange(s1, e1, s2, e2)
	result.Target = result.Target.WithOffsets(newStart, newEnd)
	return &result, nil
}

func (e *DefaultOTEngine) computeRemainingRange(s1, e1, s2, e2 int) (int, int) {
	if s1 < s2 && e1 > e2 {
		return s1, e1 - (e2 - s2)
	}
	if s1 < s2 {
		return s1, s2
	}
	return s2, e1 - (e2 - s1)
}

func (e *DefaultOTEngine) transformReplaceReplace(op1, op2 *Operation) (*Operation, error) {
	conflict := e.DetectConflict(op1, op2)
	if conflict != nil {
		return nil, &ConflictError{Conflict: conflict}
	}

	result := op1.Clone()
	delta := len(op2.Content) - len(op2.OldContent)
	if op1.Target.StartOffset >= op2.Target.EndOffset {
		result.Target = result.Target.WithOffsets(
			op1.Target.StartOffset+delta,
			op1.Target.EndOffset+delta,
		)
	}
	return &result, nil
}

func (e *DefaultOTEngine) TransformBatch(ops1, ops2 []*Operation) ([]*Operation, error) {
	if len(ops1) == 0 {
		return []*Operation{}, nil
	}

	result := make([]*Operation, 0, len(ops1))
	for _, op1 := range ops1 {
		transformed, err := e.transformAgainstBatch(op1, ops2)
		if err != nil {
			return nil, err
		}
		result = append(result, transformed)
	}
	return result, nil
}

func (e *DefaultOTEngine) transformAgainstBatch(op *Operation, against []*Operation) (*Operation, error) {
	current := op
	for _, op2 := range against {
		transformed, err := e.Transform(current, op2)
		if err != nil {
			return nil, err
		}
		current = transformed
	}
	return current, nil
}

func (e *DefaultOTEngine) Compose(op1, op2 *Operation) (*Operation, error) {
	if err := e.validateOperations(op1, op2); err != nil {
		return nil, err
	}

	if op1.FilePath != op2.FilePath {
		return nil, ErrIncompatibleOps
	}

	return e.composeOperations(op1, op2)
}

func (e *DefaultOTEngine) composeOperations(op1, op2 *Operation) (*Operation, error) {
	if op1.Type == OpInsert && op2.Type == OpInsert {
		return e.composeInserts(op1, op2)
	}
	if op1.Type == OpDelete && op2.Type == OpDelete {
		return e.composeDeletes(op1, op2)
	}
	return nil, ErrCompositionFailed
}

func (e *DefaultOTEngine) composeInserts(op1, op2 *Operation) (*Operation, error) {
	if !e.areAdjacent(op1.Target, op2.Target) {
		return nil, ErrCompositionFailed
	}

	result := op1.Clone()
	result.Content = append(result.Content, op2.Content...)
	result.Target = result.Target.WithOffsets(
		op1.Target.StartOffset,
		op1.Target.EndOffset+len(op2.Content),
	)
	return &result, nil
}

func (e *DefaultOTEngine) composeDeletes(op1, op2 *Operation) (*Operation, error) {
	if !e.areAdjacent(op1.Target, op2.Target) {
		return nil, ErrCompositionFailed
	}

	result := op1.Clone()
	result.OldContent = append(result.OldContent, op2.OldContent...)
	newEnd := max(op1.Target.EndOffset, op2.Target.EndOffset)
	newStart := min(op1.Target.StartOffset, op2.Target.StartOffset)
	result.Target = result.Target.WithOffsets(newStart, newEnd)
	return &result, nil
}

func (e *DefaultOTEngine) areAdjacent(t1, t2 Target) bool {
	return t1.EndOffset == t2.StartOffset || t2.EndOffset == t1.StartOffset
}

func (e *DefaultOTEngine) CanMergeAutomatically(op1, op2 *Operation) bool {
	if op1 == nil || op2 == nil {
		return false
	}

	if !e.targetsOverlap(op1, op2) {
		return true
	}

	return e.canAutoMergeOverlapping(op1, op2)
}

func (e *DefaultOTEngine) canAutoMergeOverlapping(op1, op2 *Operation) bool {
	if op1.Type == OpInsert && op2.Type == OpInsert {
		return true
	}
	if op1.Type == OpDelete && op2.Type == OpDelete {
		return true
	}
	return false
}

func (e *DefaultOTEngine) DetectConflict(op1, op2 *Operation) *Conflict {
	if op1 == nil || op2 == nil {
		return nil
	}

	if !e.targetsOverlap(op1, op2) {
		return nil
	}

	return e.detectConflictType(op1, op2)
}

func (e *DefaultOTEngine) detectConflictType(op1, op2 *Operation) *Conflict {
	conflictType := e.classifyConflict(op1, op2)
	if conflictType == ConflictType(-1) {
		return nil
	}

	return e.buildConflict(op1, op2, conflictType)
}

func (e *DefaultOTEngine) classifyConflict(op1, op2 *Operation) ConflictType {
	if e.isDeleteEdit(op1, op2) {
		return ConflictTypeDeleteEdit
	}
	if e.isMoveEdit(op1, op2) {
		return ConflictTypeMoveEdit
	}
	if e.isSemanticConflict(op1, op2) {
		return ConflictTypeSemanticConflict
	}
	if e.isOverlappingEdit(op1, op2) {
		return ConflictTypeOverlappingEdit
	}
	return ConflictType(-1)
}

func (e *DefaultOTEngine) isDeleteEdit(op1, op2 *Operation) bool {
	return (op1.Type == OpDelete && op2.Type != OpDelete) ||
		(op2.Type == OpDelete && op1.Type != OpDelete)
}

func (e *DefaultOTEngine) isMoveEdit(op1, op2 *Operation) bool {
	return (op1.Type == OpMove && op2.Type != OpMove) ||
		(op2.Type == OpMove && op1.Type != OpMove)
}

func (e *DefaultOTEngine) isSemanticConflict(op1, op2 *Operation) bool {
	return op1.Target.IsAST() && op2.Target.IsAST() && e.sameASTNode(op1, op2)
}

func (e *DefaultOTEngine) sameASTNode(op1, op2 *Operation) bool {
	return op1.Target.NodeID == op2.Target.NodeID && op1.Target.NodeID != ""
}

func (e *DefaultOTEngine) isOverlappingEdit(op1, op2 *Operation) bool {
	return (op1.Type == OpReplace || op1.Type == OpInsert) &&
		(op2.Type == OpReplace || op2.Type == OpInsert)
}

func (e *DefaultOTEngine) buildConflict(op1, op2 *Operation, ctype ConflictType) *Conflict {
	return &Conflict{
		Op1:         op1,
		Op2:         op2,
		Type:        ctype,
		Description: e.buildDescription(op1, op2, ctype),
		Resolutions: e.buildResolutions(op1, op2),
	}
}

func (e *DefaultOTEngine) buildDescription(_, _ *Operation, ctype ConflictType) string {
	switch ctype {
	case ConflictTypeOverlappingEdit:
		return "Both operations edit overlapping regions"
	case ConflictTypeDeleteEdit:
		return "One operation deletes content that another edits"
	case ConflictTypeMoveEdit:
		return "One operation moves content that another edits"
	case ConflictTypeSemanticConflict:
		return "Operations conflict at AST level"
	default:
		return "Unknown conflict"
	}
}

func (e *DefaultOTEngine) buildResolutions(op1, op2 *Operation) []Resolution {
	return []Resolution{
		{Label: "Keep op1", Description: "Apply first operation only", ResultOp: op1},
		{Label: "Keep op2", Description: "Apply second operation only", ResultOp: op2},
		{Label: "Keep both", Description: "Apply both operations sequentially", ResultOp: nil},
	}
}

type ConflictError struct {
	Conflict *Conflict
}

func (e *ConflictError) Error() string {
	return "conflict: " + e.Conflict.Description
}

func (e *ConflictError) Unwrap() error {
	return ErrConflict
}
