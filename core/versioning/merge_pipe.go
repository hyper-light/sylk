package versioning

import (
	"bytes"
	"context"
	"errors"
	"sync"
)

const DefaultMergeBuffer = 64

var (
	ErrMergePipeClosed       = errors.New("merge pipe is closed")
	ErrMergePipelineNotFound = errors.New("pipeline not registered with merge pipe")
	ErrMergePipelineExists   = errors.New("pipeline already registered")
)

// MergePipeConfig configures a MergePipe.
type MergePipeConfig struct {
	GlobalVFS   *PipelineVFS
	WAL         SemanticWAL
	OTEngine    OTEngine
	Resolver    ConflictResolver
	DraftMu     *sync.Mutex
	MergeBuffer int // channel capacity, default DefaultMergeBuffer
}

// MergePipe serializes pipeline→global merges through a single goroutine.
// Each merge uses OT to transform the pipeline's changes against any
// concurrent changes that have accumulated since the pipeline was registered.
type MergePipe struct {
	mu        sync.Mutex
	globalVFS *PipelineVFS
	wal       SemanticWAL
	ot        OTEngine
	resolver  ConflictResolver
	draftMu   *sync.Mutex
	pipelines map[string]pipelineReg
	mergeCh   chan mergeRequest
	stopCh    chan struct{}
	doneCh    chan struct{}
	closed    bool
}

type pipelineReg struct {
	baseVersion SemanticVersion
}

type mergeRequest struct {
	ctx        context.Context
	pipelineID string
	mods       []FileModification
	resultCh   chan mergeResult
}

type mergeResult struct {
	version SemanticVersion
	err     error
}

type transformedFileMod struct {
	mod FileModification
	ops []*Operation
}

// NewMergePipe creates a new MergePipe. Call Start() to launch the merge goroutine.
func NewMergePipe(cfg MergePipeConfig) *MergePipe {
	bufSize := cfg.MergeBuffer
	if bufSize <= 0 {
		bufSize = DefaultMergeBuffer
	}

	resolver := cfg.Resolver
	if resolver == nil {
		resolver = NewNoOpConflictResolver()
	}

	return &MergePipe{
		globalVFS: cfg.GlobalVFS,
		wal:       cfg.WAL,
		ot:        cfg.OTEngine,
		resolver:  resolver,
		draftMu:   cfg.DraftMu,
		pipelines: make(map[string]pipelineReg),
		mergeCh:   make(chan mergeRequest, bufSize),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start launches the single merge goroutine.
func (mp *MergePipe) Start() {
	go mp.mergeLoop()
}

// RegisterPipeline records the current WAL version as the pipeline's base.
func (mp *MergePipe) RegisterPipeline(pipelineID string) error {
	return mp.RegisterPipelineAt(pipelineID, mp.wal.CurrentVersion())
}

// RegisterPipelineAt records an explicit base version for a pipeline.
func (mp *MergePipe) RegisterPipelineAt(pipelineID string, base SemanticVersion) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if mp.closed {
		return ErrMergePipeClosed
	}

	if _, exists := mp.pipelines[pipelineID]; exists {
		return ErrMergePipelineExists
	}

	mp.pipelines[pipelineID] = pipelineReg{
		baseVersion: base,
	}
	return nil
}

// UnregisterPipeline removes a pipeline without merging.
func (mp *MergePipe) UnregisterPipeline(pipelineID string) {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	delete(mp.pipelines, pipelineID)
}

// Merge sends modifications to the merge goroutine and blocks until the
// merge completes or ctx is cancelled.
func (mp *MergePipe) Merge(ctx context.Context, pipelineID string, mods []FileModification) (SemanticVersion, error) {
	mp.mu.Lock()
	if mp.closed {
		mp.mu.Unlock()
		return SemanticVersion{}, ErrMergePipeClosed
	}
	mp.mu.Unlock()

	req := mergeRequest{
		ctx:        ctx,
		pipelineID: pipelineID,
		mods:       mods,
		resultCh:   make(chan mergeResult, 1),
	}

	select {
	case mp.mergeCh <- req:
	case <-ctx.Done():
		return SemanticVersion{}, ctx.Err()
	case <-mp.stopCh:
		return SemanticVersion{}, ErrMergePipeClosed
	}

	select {
	case res := <-req.resultCh:
		return res.version, res.err
	case <-ctx.Done():
		return SemanticVersion{}, ctx.Err()
	case <-mp.stopCh:
		return SemanticVersion{}, ErrMergePipeClosed
	}
}

// Stop closes the merge pipe and waits for the goroutine to finish.
func (mp *MergePipe) Stop() {
	mp.mu.Lock()
	if mp.closed {
		mp.mu.Unlock()
		return
	}
	mp.closed = true
	mp.mu.Unlock()

	close(mp.stopCh)
	<-mp.doneCh
}

// mergeLoop is the single merge goroutine.
func (mp *MergePipe) mergeLoop() {
	defer close(mp.doneCh)

	for {
		select {
		case req := <-mp.mergeCh:
			res := mp.executeMerge(req)
			req.resultCh <- res
		case <-mp.stopCh:
			mp.drainPending()
			return
		}
	}
}

func (mp *MergePipe) drainPending() {
	for {
		select {
		case req := <-mp.mergeCh:
			req.resultCh <- mergeResult{err: ErrMergePipeClosed}
		default:
			return
		}
	}
}

func (mp *MergePipe) executeMerge(req mergeRequest) mergeResult {
	mp.mu.Lock()
	reg, ok := mp.pipelines[req.pipelineID]
	if ok {
		delete(mp.pipelines, req.pipelineID)
	}
	mp.mu.Unlock()

	if !ok {
		return mergeResult{err: ErrMergePipelineNotFound}
	}

	if len(req.mods) == 0 {
		return mergeResult{version: mp.wal.CurrentVersion()}
	}

	if mp.draftMu != nil {
		mp.draftMu.Lock()
		defer mp.draftMu.Unlock()
	}

	transformed, err := mp.transformMods(req.ctx, req.mods, reg.baseVersion)
	if err != nil {
		return mergeResult{err: err}
	}

	deltas, err := mp.applyToGlobal(req.ctx, transformed)
	if err != nil {
		return mergeResult{err: err}
	}

	ver, err := mp.wal.AppendDelta(req.pipelineID, deltas)
	if err != nil {
		return mergeResult{err: err}
	}

	return mergeResult{version: ver}
}

// transformMods converts pipeline FileModifications into precise
// OT-transformed operations against any accepted global changes since the
// pipeline's base version.
func (mp *MergePipe) transformMods(ctx context.Context, mods []FileModification, base SemanticVersion) ([]transformedFileMod, error) {
	accumulated, err := mp.wal.GetDeltasSince(base)
	if err != nil {
		return nil, err
	}

	accOpsByPath := make(map[string][]*Operation, len(accumulated))
	for _, entry := range accumulated {
		for _, d := range entry.Deltas {
			accOpsByPath[d.Path] = append(accOpsByPath[d.Path], preciseOperationsForDelta(d, entry.PipelineID)...)
		}
	}

	result := make([]transformedFileMod, 0, len(mods))
	for _, m := range mods {
		sourceOps := preciseOperationsForModification(m)
		transformed := make([]*Operation, 0, len(sourceOps))
		for _, op := range sourceOps {
			next, transformErr := mp.transformOperation(ctx, op, accOpsByPath[m.OriginalPath], transformed)
			if transformErr != nil {
				return nil, transformErr
			}
			if isNoOpOperation(next) {
				continue
			}
			transformed = append(transformed, next)
		}
		result = append(result, transformedFileMod{mod: m, ops: transformed})
	}

	return result, nil
}

// applyToGlobal materializes transformed operations into the global draft VFS
// and emits one semantic WAL delta per resulting file change.
func (mp *MergePipe) applyToGlobal(ctx context.Context, mods []transformedFileMod) ([]WALFileDelta, error) {
	deltas := make([]WALFileDelta, 0, len(mods))

	for _, tm := range mods {
		delta, applyErr := mp.applyTransformedModToGlobal(ctx, tm)
		if applyErr != nil {
			return nil, applyErr
		}
		if delta == nil {
			continue
		}
		deltas = append(deltas, *delta)
	}

	return deltas, nil
}

func (mp *MergePipe) validateModAgainstGlobal(ctx context.Context, m FileModification) error {
	current, err := mp.globalVFS.Read(ctx, m.OriginalPath)
	currentExists := err == nil
	if err != nil && !errors.Is(err, ErrFileNotFound) {
		return err
	}

	switch m.Operation {
	case FileOpCreate:
		if currentExists {
			return &ConflictError{Conflict: buildMergeConflict(m, current)}
		}
	case FileOpDelete, FileOpModify:
		if !currentExists {
			return &ConflictError{Conflict: buildMergeConflict(m, nil)}
		}
		if !bytes.Equal(current, m.OldContent) {
			return &ConflictError{Conflict: buildMergeConflict(m, current)}
		}
	}
	return nil
}

func buildMergeConflict(m FileModification, current []byte) *Conflict {
	ours := modToOperation(m)
	currentOp := NewOperation(
		m.BaseVersion,
		m.OriginalPath,
		NewOffsetTarget(0, len(current)),
		OpReplace,
		current,
		m.OldContent,
		"",
		"",
		"",
		NewVectorClock(),
	)
	return &Conflict{
		Op1:         ours,
		Op2:         &currentOp,
		Type:        ConflictTypeOverlappingEdit,
		Description: "global file changed since pipeline base snapshot",
		Resolutions: []Resolution{
			{Label: "Keep pipeline change", Description: "Apply the pipeline modification", ResultOp: ours},
			{Label: "Keep current global", Description: "Preserve the newer global content", ResultOp: &currentOp},
		},
	}
}

func (mp *MergePipe) readOldContent(ctx context.Context, path string) []byte {
	content, err := mp.globalVFS.Read(ctx, path)
	if err != nil {
		return nil
	}
	return content
}

func (mp *MergePipe) applyModToGlobal(ctx context.Context, m FileModification) error {
	switch m.Operation {
	case FileOpDelete:
		return mp.globalVFS.Delete(ctx, m.OriginalPath)
	default:
		return mp.globalVFS.Write(ctx, m.OriginalPath, m.NewContent)
	}
}

// walDeltaToOperation converts a WALFileDelta to an OT Operation.
func walDeltaToOperation(d WALFileDelta, pipelineID string) *Operation {
	opType := deltaOpToOpType(d.Op)
	op := NewOperation(
		VersionID{},
		d.Path,
		NewOffsetTarget(0, len(d.OldContent)),
		opType,
		d.NewContent,
		d.OldContent,
		pipelineID,
		"",
		"",
		NewVectorClock(),
	)
	return &op
}

func deltaOpToOpType(op WALDeltaOp) OpType {
	switch op {
	case WALDeltaOpCreate:
		return OpInsert
	case WALDeltaOpDelete:
		return OpDelete
	default:
		return OpReplace
	}
}

// modToOperation converts a FileModification to an OT Operation.
func modToOperation(m FileModification) *Operation {
	opType := fileOpToOpType(m.Operation)
	op := NewOperation(
		m.BaseVersion,
		m.OriginalPath,
		NewOffsetTarget(0, len(m.OldContent)),
		opType,
		m.NewContent,
		m.OldContent,
		"",
		"",
		"",
		NewVectorClock(),
	)
	return &op
}

func fileOpToOpType(op FileOp) OpType {
	switch op {
	case FileOpCreate:
		return OpInsert
	case FileOpDelete:
		return OpDelete
	default:
		return OpReplace
	}
}

// opsToMods maps transformed ops back to FileModifications,
// preserving metadata from the originals.
func opsToMods(ops []*Operation, originals []FileModification) []FileModification {
	result := make([]FileModification, len(ops))
	for i, op := range ops {
		base := FileModification{}
		if i < len(originals) {
			base = originals[i]
		}
		base.NewContent = op.Content
		base.OriginalPath = op.FilePath
		result[i] = base
	}
	return result
}
