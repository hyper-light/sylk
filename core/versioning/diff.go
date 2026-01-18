package versioning

import (
	"bytes"
	"strings"
)

type Differ interface {
	DiffBytes(base, target []byte) *FileDiff
	DiffLines(baseLines, targetLines []string) *FileDiff
	ToOperations(diff *FileDiff, filePath string, baseVersion VersionID) []Operation
	FromOperations(ops []Operation) *FileDiff
}

type ASTChange struct {
	Type     ASTChangeType
	NodePath []string
	NodeType string
	OldText  string
	NewText  string
	OldRange Target
	NewRange Target
}

type ASTChangeType int

const (
	ASTChangeAdded ASTChangeType = iota
	ASTChangeRemoved
	ASTChangeModified
	ASTChangeMoved
)

var astChangeTypeNames = map[ASTChangeType]string{
	ASTChangeAdded:    "added",
	ASTChangeRemoved:  "removed",
	ASTChangeModified: "modified",
	ASTChangeMoved:    "moved",
}

func (t ASTChangeType) String() string {
	if name, ok := astChangeTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

type ASTDiff struct {
	FilePath string
	Language string
	Changes  []ASTChange
}

type MyersDiffer struct {
	ContextLines int
}

func NewMyersDiffer(contextLines int) *MyersDiffer {
	return &MyersDiffer{ContextLines: contextLines}
}

func (d *MyersDiffer) DiffBytes(base, target []byte) *FileDiff {
	baseLines := splitLines(base)
	targetLines := splitLines(target)
	return d.DiffLines(baseLines, targetLines)
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return []string{}
	}
	lines := strings.Split(string(data), "\n")
	return lines
}

func (d *MyersDiffer) DiffLines(baseLines, targetLines []string) *FileDiff {
	editScript := d.computeEditScript(baseLines, targetLines)
	hunks := d.buildHunks(editScript, baseLines, targetLines)

	stats := d.computeStats(hunks)

	return &FileDiff{
		Hunks: hunks,
		Stats: stats,
	}
}

type editOp struct {
	opType   DiffLineType
	oldIndex int
	newIndex int
}

func (d *MyersDiffer) computeEditScript(base, target []string) []editOp {
	n, m := len(base), len(target)

	if n == 0 {
		return d.handleEmptyBase(m)
	}
	if m == 0 {
		return d.allDeletes(n)
	}
	return d.myersAlgorithm(base, target)
}

func (d *MyersDiffer) handleEmptyBase(m int) []editOp {
	if m == 0 {
		return nil
	}
	return d.allInserts(m)
}

func (d *MyersDiffer) allInserts(m int) []editOp {
	ops := make([]editOp, m)
	for i := range m {
		ops[i] = editOp{opType: DiffLineAdd, newIndex: i}
	}
	return ops
}

func (d *MyersDiffer) allDeletes(n int) []editOp {
	ops := make([]editOp, n)
	for i := range n {
		ops[i] = editOp{opType: DiffLineDelete, oldIndex: i}
	}
	return ops
}

func (d *MyersDiffer) myersAlgorithm(base, target []string) []editOp {
	n, m := len(base), len(target)
	max := n + m

	if max == 0 {
		return nil
	}

	v, trace, offset := d.initMyersState(max)
	return d.runMyersLoop(base, target, v, trace, offset, n, m, max)
}

func (d *MyersDiffer) initMyersState(max int) ([]int, [][]int, int) {
	v := make([]int, 2*max+1)
	offset := max
	v[offset+1] = 0
	return v, nil, offset
}

func (d *MyersDiffer) runMyersLoop(base, target []string, v []int, trace [][]int, offset, n, m, max int) []editOp {
	for depth := 0; depth <= max; depth++ {
		trace = d.captureTrace(v, trace)
		if result := d.processMyersDepth(base, target, v, trace, offset, n, m, depth); result != nil {
			return result
		}
	}
	return nil
}

func (d *MyersDiffer) captureTrace(v []int, trace [][]int) [][]int {
	traceCopy := make([]int, len(v))
	copy(traceCopy, v)
	return append(trace, traceCopy)
}

func (d *MyersDiffer) processMyersDepth(base, target []string, v []int, trace [][]int, offset, n, m, depth int) []editOp {
	for k := -depth; k <= depth; k += 2 {
		x := d.computeXArray(v, offset, k, depth)
		y := x - k
		x, y = d.extendDiagonal(base, target, x, y, n, m)
		v[offset+k] = x
		if x >= n && y >= m {
			return d.backtrackArray(trace, n, m, offset)
		}
	}
	return nil
}

func (d *MyersDiffer) computeXArray(v []int, offset, k, depth int) int {
	if k == -depth || (k != depth && v[offset+k-1] < v[offset+k+1]) {
		return v[offset+k+1]
	}
	return v[offset+k-1] + 1
}

func (d *MyersDiffer) extendDiagonal(base, target []string, x, y, n, m int) (int, int) {
	for x < n && y < m && base[x] == target[y] {
		x++
		y++
	}
	return x, y
}

func (d *MyersDiffer) backtrackArray(trace [][]int, n, m, offset int) []editOp {
	ops := make([]editOp, 0)
	x, y := n, m

	for depth := len(trace) - 1; depth > 0; depth-- {
		k := x - y
		vPrev := trace[depth]

		prevK := d.getPrevK(vPrev, offset, k, depth)
		prevX := vPrev[offset+prevK]
		prevY := prevX - prevK

		afterX, afterY := d.afterEditPosition(prevK, k, prevX, prevY)
		ops = d.addSnakeOps(ops, x, y, afterX, afterY)
		x, y = afterX, afterY

		ops, x, y = d.appendEditOp(ops, prevK, k, x, y)
	}

	ops = d.addSnakeOps(ops, x, y, 0, 0)
	return reverseOps(ops)
}

func (d *MyersDiffer) afterEditPosition(prevK, k, prevX, prevY int) (int, int) {
	if prevK < k {
		return prevX + 1, prevY
	}
	return prevX, prevY + 1
}

func (d *MyersDiffer) getPrevK(v []int, offset, k, depth int) int {
	if k == -depth {
		return k + 1
	}
	if k == depth {
		return k - 1
	}
	if v[offset+k-1] < v[offset+k+1] {
		return k + 1
	}
	return k - 1
}

func (d *MyersDiffer) appendEditOp(ops []editOp, prevK, k, x, y int) ([]editOp, int, int) {
	if prevK < k {
		x--
		return append(ops, editOp{opType: DiffLineDelete, oldIndex: x}), x, y
	}
	y--
	return append(ops, editOp{opType: DiffLineAdd, newIndex: y}), x, y
}

func (d *MyersDiffer) addSnakeOps(ops []editOp, x, y, prevX, prevY int) []editOp {
	for x > prevX && y > prevY {
		x--
		y--
		ops = append(ops, editOp{opType: DiffLineContext, oldIndex: x, newIndex: y})
	}
	return ops
}

func reverseOps(ops []editOp) []editOp {
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

func (d *MyersDiffer) buildHunks(editScript []editOp, base, target []string) []DiffHunk {
	if len(editScript) == 0 {
		return nil
	}

	changes := d.findChangeRanges(editScript)
	return d.createHunksFromRanges(changes, editScript, base, target)
}

func (d *MyersDiffer) findChangeRanges(editScript []editOp) [][2]int {
	var ranges [][2]int
	var start int
	inChange := false

	for i, op := range editScript {
		isChange := op.opType != DiffLineContext
		inChange, start, ranges = d.updateChangeState(isChange, inChange, i, start, ranges)
	}

	if inChange {
		ranges = append(ranges, [2]int{start, len(editScript)})
	}

	return ranges
}

func (d *MyersDiffer) updateChangeState(isChange, inChange bool, i, start int, ranges [][2]int) (bool, int, [][2]int) {
	if isChange {
		return d.handleChangeOp(inChange, i, start, ranges)
	}
	return d.handleContextOp(inChange, i, start, ranges)
}

func (d *MyersDiffer) handleChangeOp(inChange bool, i, start int, ranges [][2]int) (bool, int, [][2]int) {
	if !inChange {
		return true, i, ranges
	}
	return true, start, ranges
}

func (d *MyersDiffer) handleContextOp(inChange bool, i, start int, ranges [][2]int) (bool, int, [][2]int) {
	if inChange {
		return false, start, append(ranges, [2]int{start, i})
	}
	return false, start, ranges
}

func (d *MyersDiffer) createHunksFromRanges(ranges [][2]int, ops []editOp, base, target []string) []DiffHunk {
	hunks := make([]DiffHunk, 0, len(ranges))

	for _, r := range ranges {
		hunk := d.createHunk(r[0], r[1], ops, base, target)
		hunks = append(hunks, hunk)
	}

	return hunks
}

func (d *MyersDiffer) createHunk(start, end int, ops []editOp, base, target []string) DiffHunk {
	contextStart := d.clampStart(start)
	contextEnd := d.clampEnd(end, len(ops))

	oldStart, newStart := d.findStartPositions(ops, contextStart)
	oldCount, newCount := d.countLines(ops, contextStart, contextEnd)

	lines := d.extractHunkLines(ops, contextStart, contextEnd, base, target)

	return DiffHunk{
		OldStart: oldStart + 1,
		OldCount: oldCount,
		NewStart: newStart + 1,
		NewCount: newCount,
		Lines:    lines,
	}
}

func (d *MyersDiffer) clampStart(start int) int {
	contextStart := start - d.ContextLines
	if contextStart < 0 {
		return 0
	}
	return contextStart
}

func (d *MyersDiffer) clampEnd(end, total int) int {
	contextEnd := end + d.ContextLines
	if contextEnd > total {
		return total
	}
	return contextEnd
}

func (d *MyersDiffer) findStartPositions(ops []editOp, contextStart int) (int, int) {
	oldStart, newStart := 0, 0
	for i := range contextStart {
		op := ops[i]
		if op.opType != DiffLineAdd {
			oldStart++
		}
		if op.opType != DiffLineDelete {
			newStart++
		}
	}
	return oldStart, newStart
}

func (d *MyersDiffer) countLines(ops []editOp, start, end int) (int, int) {
	oldCount, newCount := 0, 0
	for i := start; i < end; i++ {
		op := ops[i]
		if op.opType != DiffLineAdd {
			oldCount++
		}
		if op.opType != DiffLineDelete {
			newCount++
		}
	}
	return oldCount, newCount
}

func (d *MyersDiffer) extractHunkLines(ops []editOp, start, end int, base, target []string) []DiffLine {
	lines := make([]DiffLine, 0, end-start)

	for i := start; i < end; i++ {
		line := d.createDiffLine(ops[i], base, target)
		lines = append(lines, line)
	}

	return lines
}

func (d *MyersDiffer) createDiffLine(op editOp, base, target []string) DiffLine {
	switch op.opType {
	case DiffLineContext:
		return DiffLine{
			Type:    DiffLineContext,
			Content: safeGetLine(base, op.oldIndex),
			OldLine: op.oldIndex + 1,
			NewLine: op.newIndex + 1,
		}
	case DiffLineAdd:
		return DiffLine{
			Type:    DiffLineAdd,
			Content: safeGetLine(target, op.newIndex),
			NewLine: op.newIndex + 1,
		}
	case DiffLineDelete:
		return DiffLine{
			Type:    DiffLineDelete,
			Content: safeGetLine(base, op.oldIndex),
			OldLine: op.oldIndex + 1,
		}
	default:
		return DiffLine{}
	}
}

func safeGetLine(lines []string, index int) string {
	if index < 0 || index >= len(lines) {
		return ""
	}
	return lines[index]
}

func (d *MyersDiffer) computeStats(hunks []DiffHunk) DiffStats {
	var stats DiffStats
	for _, hunk := range hunks {
		d.countHunkStats(&stats, hunk)
	}
	stats.Changes = stats.Additions + stats.Deletions
	return stats
}

func (d *MyersDiffer) countHunkStats(stats *DiffStats, hunk DiffHunk) {
	for _, line := range hunk.Lines {
		d.countLineType(stats, line.Type)
	}
}

func (d *MyersDiffer) countLineType(stats *DiffStats, lineType DiffLineType) {
	switch lineType {
	case DiffLineAdd:
		stats.Additions++
	case DiffLineDelete:
		stats.Deletions++
	}
}

func (d *MyersDiffer) ToOperations(diff *FileDiff, filePath string, baseVersion VersionID) []Operation {
	ops := make([]Operation, 0)

	for _, hunk := range diff.Hunks {
		hunkOps := d.hunkToOperations(hunk, filePath, baseVersion)
		ops = append(ops, hunkOps...)
	}

	return ops
}

func (d *MyersDiffer) hunkToOperations(hunk DiffHunk, filePath string, baseVersion VersionID) []Operation {
	ops := make([]Operation, 0)

	for _, line := range hunk.Lines {
		if line.Type == DiffLineContext {
			continue
		}

		op := d.lineToOperation(line, filePath, baseVersion)
		ops = append(ops, op)
	}

	return ops
}

func (d *MyersDiffer) lineToOperation(line DiffLine, filePath string, baseVersion VersionID) Operation {
	switch line.Type {
	case DiffLineAdd:
		return Operation{
			BaseVersion: baseVersion,
			FilePath:    filePath,
			Target:      NewLineTarget(line.NewLine, line.NewLine),
			Type:        OpInsert,
			Content:     []byte(line.Content + "\n"),
		}
	case DiffLineDelete:
		return Operation{
			BaseVersion: baseVersion,
			FilePath:    filePath,
			Target:      NewLineTarget(line.OldLine, line.OldLine),
			Type:        OpDelete,
			OldContent:  []byte(line.Content + "\n"),
		}
	default:
		return Operation{}
	}
}

func (d *MyersDiffer) FromOperations(ops []Operation) *FileDiff {
	hunks := make([]DiffHunk, 0)
	var stats DiffStats

	for _, op := range ops {
		line := d.operationToLine(op)
		if line.Type == DiffLineContext {
			continue
		}

		hunk := DiffHunk{
			OldStart: op.Target.StartLine,
			NewStart: op.Target.StartLine,
			Lines:    []DiffLine{line},
		}

		d.updateHunkCounts(&hunk, line.Type)
		d.updateStats(&stats, line.Type)

		hunks = append(hunks, hunk)
	}

	stats.Changes = stats.Additions + stats.Deletions

	return &FileDiff{
		Hunks: hunks,
		Stats: stats,
	}
}

func (d *MyersDiffer) operationToLine(op Operation) DiffLine {
	switch op.Type {
	case OpInsert:
		return DiffLine{
			Type:    DiffLineAdd,
			Content: string(bytes.TrimSuffix(op.Content, []byte("\n"))),
			NewLine: op.Target.StartLine,
		}
	case OpDelete:
		return DiffLine{
			Type:    DiffLineDelete,
			Content: string(bytes.TrimSuffix(op.OldContent, []byte("\n"))),
			OldLine: op.Target.StartLine,
		}
	default:
		return DiffLine{Type: DiffLineContext}
	}
}

func (d *MyersDiffer) updateHunkCounts(hunk *DiffHunk, lineType DiffLineType) {
	switch lineType {
	case DiffLineAdd:
		hunk.NewCount = 1
	case DiffLineDelete:
		hunk.OldCount = 1
	}
}

func (d *MyersDiffer) updateStats(stats *DiffStats, lineType DiffLineType) {
	switch lineType {
	case DiffLineAdd:
		stats.Additions++
	case DiffLineDelete:
		stats.Deletions++
	}
}

type ASTDiffer struct {
	fallbackDiffer *MyersDiffer
}

func NewASTDiffer() *ASTDiffer {
	return &ASTDiffer{
		fallbackDiffer: NewMyersDiffer(3),
	}
}

func (d *ASTDiffer) DiffBytes(base, target []byte) *FileDiff {
	return d.fallbackDiffer.DiffBytes(base, target)
}

func (d *ASTDiffer) DiffLines(baseLines, targetLines []string) *FileDiff {
	return d.fallbackDiffer.DiffLines(baseLines, targetLines)
}

func (d *ASTDiffer) ToOperations(diff *FileDiff, filePath string, baseVersion VersionID) []Operation {
	return d.fallbackDiffer.ToOperations(diff, filePath, baseVersion)
}

func (d *ASTDiffer) FromOperations(ops []Operation) *FileDiff {
	return d.fallbackDiffer.FromOperations(ops)
}

func (d *ASTDiffer) DiffAST(baseAST, targetAST any, language string) *ASTDiff {
	return &ASTDiff{
		Language: language,
		Changes:  []ASTChange{},
	}
}
