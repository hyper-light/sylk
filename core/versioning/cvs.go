package versioning

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCVSClosed            = errors.New("cvs is closed")
	ErrCVSPipelineNotFound  = errors.New("cvs pipeline not found")
	ErrCVSFileLocked        = errors.New("cvs file is locked by another session")
	ErrCVSLockNotHeld       = errors.New("cvs lock not held by session")
	ErrCVSLockNotFound      = errors.New("cvs file lock not found")
	ErrCVSMergeConflict     = errors.New("cvs merge conflict")
	ErrCVSSubscriptionExist = errors.New("cvs subscription already exists")
)

type SubscriptionID string

type FileChangeType int

const (
	FileChangeCreated FileChangeType = iota
	FileChangeModified
	FileChangeDeleted
)

var fileChangeTypeNames = map[FileChangeType]string{
	FileChangeCreated:  "created",
	FileChangeModified: "modified",
	FileChangeDeleted:  "deleted",
}

func (t FileChangeType) String() string {
	if name, ok := fileChangeTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

type FileChangeEvent struct {
	FilePath   string
	NewVersion VersionID
	OldVersion VersionID
	ChangeType FileChangeType
	SessionID  SessionID
	PipelineID string
	Timestamp  time.Time
}

type FileChangeCallback func(event FileChangeEvent)

type FileLock struct {
	ID         string
	FilePath   string
	SessionID  SessionID
	AcquiredAt time.Time
	ExpiresAt  time.Time
}

func (fl *FileLock) IsExpired() bool {
	return time.Now().After(fl.ExpiresAt)
}

func (fl *FileLock) Clone() *FileLock {
	return &FileLock{
		ID:         fl.ID,
		FilePath:   fl.FilePath,
		SessionID:  fl.SessionID,
		AcquiredAt: fl.AcquiredAt,
		ExpiresAt:  fl.ExpiresAt,
	}
}

type WriteMetadata struct {
	PipelineID string
	SessionID  SessionID
	AgentID    string
	AgentRole  string
	Message    string
}

type HistoryOptions struct {
	Limit      int
	Since      *time.Time
	Until      *time.Time
	SessionID  *SessionID
	PipelineID *string
}

type CVSStats struct {
	TotalFiles        int64
	TotalVersions     int64
	TotalOperations   int64
	ActivePipelines   int64
	ActiveVariants    int64
	ActiveLocks       int64
	ActiveSubscribers int64
}

type CVS interface {
	Read(ctx context.Context, filePath string) ([]byte, error)
	Write(ctx context.Context, filePath string, content []byte, meta WriteMetadata) (VersionID, error)
	Delete(ctx context.Context, filePath string, meta WriteMetadata) (VersionID, error)

	GetVersion(versionID VersionID) (*FileVersion, error)
	GetHead(filePath string) (*FileVersion, error)
	GetHistory(filePath string, opts HistoryOptions) ([]*FileVersion, error)

	BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error)
	CommitPipeline(pipelineID string) ([]VersionID, error)
	RollbackPipeline(pipelineID string) error

	Merge(ctx context.Context, v1, v2 VersionID, resolver ConflictResolver) (VersionID, error)
	ThreeWayMerge(ctx context.Context, base, ours, theirs VersionID, resolver ConflictResolver) (VersionID, error)

	AcquireFileLock(filePath string, sessionID SessionID, ttl time.Duration) (*FileLock, error)
	ReleaseFileLock(lockID string) error
	RefreshFileLock(lockID string, ttl time.Duration) error

	Subscribe(filePath string, sessionID SessionID, cb FileChangeCallback) (SubscriptionID, error)
	Unsubscribe(subID SubscriptionID) error

	Stats() CVSStats
	Close() error
}

type BeginPipelineConfig struct {
	PipelineID  string
	SessionID   SessionID
	BaseVersion VersionID
	AgentID     string
	AgentRole   string
	WorkingDir  string
	Files       []string
}

type CVSConfig struct {
	VFSManager VFSManager
	BlobStore  BlobStore
	OpLog      OperationLog
	DAGStore   DAGStore
	WAL        WriteAheadLog
	OTEngine   OTEngine
	LockTTL    time.Duration
}

type DefaultCVS struct {
	vfsManager VFSManager
	blobStore  BlobStore
	opLog      OperationLog
	dagStore   DAGStore
	wal        WriteAheadLog
	otEngine   OTEngine

	defaultLockTTL time.Duration

	fileLocks     map[string]*FileLock
	lockMu        sync.RWMutex
	subscriptions map[string]map[SubscriptionID]FileChangeCallback
	subMu         sync.RWMutex
	subCounter    atomic.Uint64

	pipelines   map[string]*pipelineEntry
	pipelineMu  sync.RWMutex
	fileTracker map[string]int64

	stats   CVSStats
	statsMu sync.RWMutex

	closed   bool
	closedMu sync.RWMutex
}

type pipelineEntry struct {
	vfs       *PipelineVFS
	sessionID SessionID
	createdAt time.Time
}

func NewCVS(cfg CVSConfig) *DefaultCVS {
	lockTTL := cfg.LockTTL
	if lockTTL == 0 {
		lockTTL = 5 * time.Minute
	}

	return &DefaultCVS{
		vfsManager:     cfg.VFSManager,
		blobStore:      cfg.BlobStore,
		opLog:          cfg.OpLog,
		dagStore:       cfg.DAGStore,
		wal:            cfg.WAL,
		otEngine:       cfg.OTEngine,
		defaultLockTTL: lockTTL,
		fileLocks:      make(map[string]*FileLock),
		subscriptions:  make(map[string]map[SubscriptionID]FileChangeCallback),
		pipelines:      make(map[string]*pipelineEntry),
		fileTracker:    make(map[string]int64),
	}
}

func (c *DefaultCVS) Read(ctx context.Context, filePath string) ([]byte, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}

	head, err := c.dagStore.GetHead(filePath)
	if err != nil {
		return nil, err
	}

	return c.blobStore.Get(head.ContentHash)
}

func (c *DefaultCVS) Write(ctx context.Context, filePath string, content []byte, meta WriteMetadata) (VersionID, error) {
	if err := c.checkClosed(); err != nil {
		return VersionID{}, err
	}

	if err := c.checkFileLock(filePath, meta.SessionID); err != nil {
		return VersionID{}, err
	}

	return c.writeVersion(ctx, filePath, content, meta)
}

func (c *DefaultCVS) writeVersion(_ context.Context, filePath string, content []byte, meta WriteMetadata) (VersionID, error) {
	if err := c.logToWAL(WALEntryOperation, meta.PipelineID, meta.SessionID, content); err != nil {
		return VersionID{}, err
	}

	parents := c.getParentVersions(filePath)
	version, err := c.createFileVersion(filePath, content, parents, meta)
	if err != nil {
		return VersionID{}, err
	}

	c.postWriteHooks(filePath, version, parents, meta)
	return version.ID, nil
}

func (c *DefaultCVS) createFileVersion(filePath string, content []byte, parents []VersionID, meta WriteMetadata) (FileVersion, error) {
	contentHash, err := c.blobStore.Put(content)
	if err != nil {
		return FileVersion{}, err
	}

	clock := c.buildVectorClock(meta.SessionID)
	op := c.buildWriteOperation(filePath, content, parents, meta, clock)

	if err := c.opLog.Append(op); err != nil {
		return FileVersion{}, err
	}

	version := NewFileVersion(filePath, content, parents, []OperationID{op.ID}, meta.PipelineID, meta.SessionID, clock)
	version.ContentHash = contentHash

	if err := c.dagStore.Add(version); err != nil {
		return FileVersion{}, err
	}

	return version, nil
}

func (c *DefaultCVS) postWriteHooks(filePath string, version FileVersion, parents []VersionID, meta WriteMetadata) {
	oldVersion := c.extractOldVersion(parents)
	changeType := c.determineChangeType(parents)
	c.notifySubscribers(filePath, version.ID, oldVersion, changeType, meta)
	c.incrementStats(func(s *CVSStats) { s.TotalVersions++ })
	c.trackFile(filePath)
}

func (c *DefaultCVS) logToWAL(entryType WALEntryType, pipelineID string, sessionID SessionID, data []byte) error {
	if c.wal == nil {
		return nil
	}
	_, err := c.wal.Append(WALEntry{
		Type:       entryType,
		PipelineID: pipelineID,
		SessionID:  sessionID,
		Data:       data,
	})
	return err
}

func (c *DefaultCVS) getParentVersions(filePath string) []VersionID {
	head, err := c.dagStore.GetHead(filePath)
	if err != nil || head == nil {
		return nil
	}
	return []VersionID{head.ID}
}

func (c *DefaultCVS) buildVectorClock(sessionID SessionID) VectorClock {
	clock := NewVectorClock()
	clock = clock.Increment(sessionID)
	return clock
}

func (c *DefaultCVS) buildWriteOperation(filePath string, content []byte, parents []VersionID, meta WriteMetadata, clock VectorClock) Operation {
	baseVersion := VersionID{}
	if len(parents) > 0 {
		baseVersion = parents[0]
	}

	return NewOperation(
		baseVersion,
		filePath,
		NewOffsetTarget(0, len(content)),
		OpReplace,
		content,
		nil,
		meta.PipelineID,
		meta.SessionID,
		meta.AgentID,
		clock,
	)
}

func (c *DefaultCVS) extractOldVersion(parents []VersionID) VersionID {
	if len(parents) > 0 {
		return parents[0]
	}
	return VersionID{}
}

func (c *DefaultCVS) determineChangeType(parents []VersionID) FileChangeType {
	if len(parents) == 0 {
		return FileChangeCreated
	}
	return FileChangeModified
}

func (c *DefaultCVS) trackFile(filePath string) {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	if _, exists := c.fileTracker[filePath]; !exists {
		c.fileTracker[filePath] = 1
		c.stats.TotalFiles++
	}
}

func (c *DefaultCVS) Delete(ctx context.Context, filePath string, meta WriteMetadata) (VersionID, error) {
	if err := c.checkClosed(); err != nil {
		return VersionID{}, err
	}

	if err := c.checkFileLock(filePath, meta.SessionID); err != nil {
		return VersionID{}, err
	}

	return c.deleteVersion(ctx, filePath, meta)
}

func (c *DefaultCVS) deleteVersion(_ context.Context, filePath string, meta WriteMetadata) (VersionID, error) {
	parents := c.getParentVersions(filePath)
	if len(parents) == 0 {
		return VersionID{}, ErrFileNotFound
	}

	if err := c.logToWAL(WALEntryOperation, meta.PipelineID, meta.SessionID, nil); err != nil {
		return VersionID{}, err
	}

	version, err := c.createDeleteVersion(filePath, parents, meta)
	if err != nil {
		return VersionID{}, err
	}

	c.postDeleteHooks(filePath, version, parents[0], meta)
	return version.ID, nil
}

func (c *DefaultCVS) createDeleteVersion(filePath string, parents []VersionID, meta WriteMetadata) (FileVersion, error) {
	clock := c.buildVectorClock(meta.SessionID)
	op := c.buildDeleteOperation(filePath, parents[0], meta, clock)

	if err := c.opLog.Append(op); err != nil {
		return FileVersion{}, err
	}

	version := NewFileVersion(filePath, nil, parents, []OperationID{op.ID}, meta.PipelineID, meta.SessionID, clock)
	if err := c.dagStore.Add(version); err != nil {
		return FileVersion{}, err
	}

	return version, nil
}

func (c *DefaultCVS) postDeleteHooks(filePath string, version FileVersion, oldVersion VersionID, meta WriteMetadata) {
	c.notifySubscribers(filePath, version.ID, oldVersion, FileChangeDeleted, meta)
	c.incrementStats(func(s *CVSStats) { s.TotalVersions++ })
}

func (c *DefaultCVS) buildDeleteOperation(filePath string, baseVersion VersionID, meta WriteMetadata, clock VectorClock) Operation {
	return NewOperation(
		baseVersion,
		filePath,
		NewOffsetTarget(0, 0),
		OpDelete,
		nil,
		nil,
		meta.PipelineID,
		meta.SessionID,
		meta.AgentID,
		clock,
	)
}

func (c *DefaultCVS) GetVersion(versionID VersionID) (*FileVersion, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	return c.dagStore.Get(versionID)
}

func (c *DefaultCVS) GetHead(filePath string) (*FileVersion, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	return c.dagStore.GetHead(filePath)
}

func (c *DefaultCVS) GetHistory(filePath string, opts HistoryOptions) ([]*FileVersion, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	versions, err := c.dagStore.GetHistory(filePath, limit)
	if err != nil {
		return nil, err
	}

	return c.filterVersions(versions, opts), nil
}

func (c *DefaultCVS) filterVersions(versions []*FileVersion, opts HistoryOptions) []*FileVersion {
	filtered := make([]*FileVersion, 0, len(versions))
	for _, v := range versions {
		if c.versionMatchesOpts(v, opts) {
			filtered = append(filtered, v)
		}
	}
	return filtered
}

func (c *DefaultCVS) versionMatchesOpts(v *FileVersion, opts HistoryOptions) bool {
	return c.matchesTimeRange(v, opts) && c.matchesSessionPipeline(v, opts)
}

func (c *DefaultCVS) matchesTimeRange(v *FileVersion, opts HistoryOptions) bool {
	return c.matchesSince(v, opts) && c.matchesUntil(v, opts)
}

func (c *DefaultCVS) matchesSince(v *FileVersion, opts HistoryOptions) bool {
	return opts.Since == nil || !v.Timestamp.Before(*opts.Since)
}

func (c *DefaultCVS) matchesUntil(v *FileVersion, opts HistoryOptions) bool {
	return opts.Until == nil || !v.Timestamp.After(*opts.Until)
}

func (c *DefaultCVS) matchesSessionPipeline(v *FileVersion, opts HistoryOptions) bool {
	return c.matchesSession(v, opts) && c.matchesPipeline(v, opts)
}

func (c *DefaultCVS) matchesSession(v *FileVersion, opts HistoryOptions) bool {
	return opts.SessionID == nil || v.SessionID == *opts.SessionID
}

func (c *DefaultCVS) matchesPipeline(v *FileVersion, opts HistoryOptions) bool {
	return opts.PipelineID == nil || v.PipelineID == *opts.PipelineID
}

func (c *DefaultCVS) BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}

	vfsCfg := VFSConfig{
		PipelineID: cfg.PipelineID,
		SessionID:  cfg.SessionID,
		WorkingDir: cfg.WorkingDir,
		AgentID:    cfg.AgentID,
		AgentRole:  cfg.AgentRole,
	}

	pipelineVFS, err := c.vfsManager.CreatePipelineVFS(vfsCfg)
	if err != nil {
		return nil, err
	}

	c.pipelineMu.Lock()
	c.pipelines[cfg.PipelineID] = &pipelineEntry{
		vfs:       pipelineVFS,
		sessionID: cfg.SessionID,
		createdAt: time.Now(),
	}
	c.pipelineMu.Unlock()

	c.incrementStats(func(s *CVSStats) { s.ActivePipelines++ })

	return pipelineVFS, nil
}

func (c *DefaultCVS) CommitPipeline(pipelineID string) ([]VersionID, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}

	entry, err := c.removePipeline(pipelineID)
	if err != nil {
		return nil, err
	}

	versions, err := c.commitPipelineChanges(entry)
	if err != nil {
		return nil, err
	}

	return c.finalizePipelineCommit(pipelineID, entry, versions)
}

func (c *DefaultCVS) removePipeline(pipelineID string) (*pipelineEntry, error) {
	c.pipelineMu.Lock()
	defer c.pipelineMu.Unlock()

	entry, ok := c.pipelines[pipelineID]
	if !ok {
		return nil, ErrCVSPipelineNotFound
	}
	delete(c.pipelines, pipelineID)
	return entry, nil
}

func (c *DefaultCVS) finalizePipelineCommit(pipelineID string, entry *pipelineEntry, versions []VersionID) ([]VersionID, error) {
	c.logToWAL(WALEntryCommit, pipelineID, entry.sessionID, nil)
	c.vfsManager.ClosePipelineVFS(pipelineID)
	c.incrementStats(func(s *CVSStats) { s.ActivePipelines-- })
	return versions, nil
}

func (c *DefaultCVS) commitPipelineChanges(entry *pipelineEntry) ([]VersionID, error) {
	mods := entry.vfs.GetModifications()
	if len(mods) == 0 {
		return nil, nil
	}

	versions := make([]VersionID, 0, len(mods))
	for _, mod := range mods {
		version, err := c.commitModification(mod, entry)
		if err != nil {
			return versions, err
		}
		versions = append(versions, version)
	}

	return versions, nil
}

func (c *DefaultCVS) commitModification(mod FileModification, entry *pipelineEntry) (VersionID, error) {
	meta := WriteMetadata{
		PipelineID: entry.vfs.GetPipelineID(),
		SessionID:  entry.sessionID,
	}

	switch mod.Operation {
	case FileOpDelete:
		return c.deleteVersion(context.Background(), mod.OriginalPath, meta)
	default:
		return c.writeVersion(context.Background(), mod.OriginalPath, mod.NewContent, meta)
	}
}

func (c *DefaultCVS) RollbackPipeline(pipelineID string) error {
	if err := c.checkClosed(); err != nil {
		return err
	}

	entry, err := c.removePipeline(pipelineID)
	if err != nil {
		return err
	}

	return c.finalizePipelineRollback(pipelineID, entry)
}

func (c *DefaultCVS) finalizePipelineRollback(pipelineID string, entry *pipelineEntry) error {
	c.logToWAL(WALEntryRollback, pipelineID, entry.sessionID, nil)
	c.vfsManager.ClosePipelineVFS(pipelineID)

	c.incrementStats(func(s *CVSStats) { s.ActivePipelines-- })

	return nil
}

func (c *DefaultCVS) Merge(ctx context.Context, v1, v2 VersionID, resolver ConflictResolver) (VersionID, error) {
	if err := c.checkClosed(); err != nil {
		return VersionID{}, err
	}

	ver1, err := c.dagStore.Get(v1)
	if err != nil {
		return VersionID{}, err
	}

	ver2, err := c.dagStore.Get(v2)
	if err != nil {
		return VersionID{}, err
	}

	return c.performMerge(ctx, ver1, ver2, resolver)
}

func (c *DefaultCVS) performMerge(ctx context.Context, v1, v2 *FileVersion, resolver ConflictResolver) (VersionID, error) {
	content1, content2, err := c.getMergeContents(v1, v2)
	if err != nil {
		return VersionID{}, err
	}

	if string(content1) == string(content2) {
		return v2.ID, nil
	}

	return c.mergeWithOps(ctx, v1, v2, content1, resolver)
}

func (c *DefaultCVS) getMergeContents(v1, v2 *FileVersion) ([]byte, []byte, error) {
	content1, err := c.blobStore.Get(v1.ContentHash)
	if err != nil {
		return nil, nil, err
	}

	content2, err := c.blobStore.Get(v2.ContentHash)
	if err != nil {
		return nil, nil, err
	}

	return content1, content2, nil
}

func (c *DefaultCVS) mergeWithOps(ctx context.Context, v1, v2 *FileVersion, content1 []byte, resolver ConflictResolver) (VersionID, error) {
	ops1, _ := c.getVersionOps(v1)
	ops2, _ := c.getVersionOps(v2)

	mergedContent, err := c.mergeOperations(ctx, content1, ops1, ops2, resolver)
	if err != nil {
		return VersionID{}, err
	}

	return c.createMergeVersion(v1, v2, mergedContent)
}

func (c *DefaultCVS) getVersionOps(v *FileVersion) ([]*Operation, error) {
	ops := make([]*Operation, 0, len(v.Operations))
	for _, opID := range v.Operations {
		op, err := c.opLog.Get(opID)
		if err != nil {
			continue
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func (c *DefaultCVS) mergeOperations(ctx context.Context, base []byte, ops1, ops2 []*Operation, resolver ConflictResolver) ([]byte, error) {
	if c.otEngine == nil {
		return base, nil
	}

	resolved, err := c.resolveConflicts(ctx, ops1, ops2, resolver)
	if err != nil {
		return nil, err
	}
	if resolved != nil {
		return resolved, nil
	}

	return c.transformAndApply(base, ops1, ops2)
}

func (c *DefaultCVS) resolveConflicts(ctx context.Context, ops1, ops2 []*Operation, resolver ConflictResolver) ([]byte, error) {
	for _, op1 := range ops1 {
		result, err := c.resolveConflictsForOp(ctx, op1, ops2, resolver)
		if err != nil || result != nil {
			return result, err
		}
	}
	return nil, nil
}

func (c *DefaultCVS) resolveConflictsForOp(ctx context.Context, op1 *Operation, ops2 []*Operation, resolver ConflictResolver) ([]byte, error) {
	for _, op2 := range ops2 {
		result, err := c.tryResolveConflict(ctx, op1, op2, resolver)
		if err != nil || result != nil {
			return result, err
		}
	}
	return nil, nil
}

func (c *DefaultCVS) tryResolveConflict(ctx context.Context, op1, op2 *Operation, resolver ConflictResolver) ([]byte, error) {
	conflict := c.otEngine.DetectConflict(op1, op2)
	if conflict == nil {
		return nil, nil
	}

	return c.resolveDetectedConflict(ctx, conflict, resolver)
}

func (c *DefaultCVS) resolveDetectedConflict(ctx context.Context, conflict *Conflict, resolver ConflictResolver) ([]byte, error) {
	resolution, err := resolver.ResolveConflict(ctx, conflict)
	if err != nil {
		return nil, err
	}

	return extractResolutionContent(resolution), nil
}

func extractResolutionContent(resolution *Resolution) []byte {
	if resolution != nil && resolution.ResultOp != nil {
		return resolution.ResultOp.Content
	}
	return nil
}

func (c *DefaultCVS) transformAndApply(base []byte, ops1, ops2 []*Operation) ([]byte, error) {
	transformed, err := c.otEngine.TransformBatch(ops1, ops2)
	if err != nil {
		return base, err
	}

	result := base
	for _, op := range transformed {
		result = applyOp(result, op)
	}
	return result, nil
}

func applyOp(content []byte, op *Operation) []byte {
	if op == nil {
		return content
	}
	return applyOpByType(content, op)
}

func applyOpByType(content []byte, op *Operation) []byte {
	switch op.Type {
	case OpInsert:
		return insertAt(content, op.Target.StartOffset, op.Content)
	case OpDelete:
		return deleteRange(content, op.Target.StartOffset, op.Target.EndOffset)
	case OpReplace:
		return applyReplace(content, op)
	default:
		return content
	}
}

func applyReplace(content []byte, op *Operation) []byte {
	tmp := deleteRange(content, op.Target.StartOffset, op.Target.EndOffset)
	return insertAt(tmp, op.Target.StartOffset, op.Content)
}

func insertAt(content []byte, pos int, data []byte) []byte {
	if pos > len(content) {
		pos = len(content)
	}
	result := make([]byte, 0, len(content)+len(data))
	result = append(result, content[:pos]...)
	result = append(result, data...)
	result = append(result, content[pos:]...)
	return result
}

func deleteRange(content []byte, start, end int) []byte {
	if start > len(content) {
		return content
	}
	if end > len(content) {
		end = len(content)
	}
	result := make([]byte, 0, len(content)-(end-start))
	result = append(result, content[:start]...)
	result = append(result, content[end:]...)
	return result
}

func (c *DefaultCVS) createMergeVersion(v1, v2 *FileVersion, content []byte) (VersionID, error) {
	contentHash, err := c.blobStore.Put(content)
	if err != nil {
		return VersionID{}, err
	}

	mergedClock := v1.Clock.Merge(v2.Clock)
	version := NewFileVersion(v1.FilePath, content, []VersionID{v1.ID, v2.ID}, nil, "", "", mergedClock)
	version.ContentHash = contentHash
	version.IsMerge = true

	if err := c.dagStore.Add(version); err != nil {
		return VersionID{}, err
	}

	c.incrementStats(func(s *CVSStats) { s.TotalVersions++ })
	return version.ID, nil
}

func (c *DefaultCVS) ThreeWayMerge(ctx context.Context, base, ours, theirs VersionID, resolver ConflictResolver) (VersionID, error) {
	if err := c.checkClosed(); err != nil {
		return VersionID{}, err
	}

	versions, err := c.getThreeVersions(base, ours, theirs)
	if err != nil {
		return VersionID{}, err
	}

	return c.performThreeWayMerge(ctx, versions[0], versions[1], versions[2], resolver)
}

func (c *DefaultCVS) getThreeVersions(base, ours, theirs VersionID) ([]*FileVersion, error) {
	baseVer, err := c.dagStore.Get(base)
	if err != nil {
		return nil, err
	}

	oursVer, err := c.dagStore.Get(ours)
	if err != nil {
		return nil, err
	}

	theirsVer, err := c.dagStore.Get(theirs)
	if err != nil {
		return nil, err
	}

	return []*FileVersion{baseVer, oursVer, theirsVer}, nil
}

func (c *DefaultCVS) performThreeWayMerge(ctx context.Context, base, ours, theirs *FileVersion, resolver ConflictResolver) (VersionID, error) {
	baseContent, _ := c.blobStore.Get(base.ContentHash)
	oursContent, _ := c.blobStore.Get(ours.ContentHash)
	theirsContent, _ := c.blobStore.Get(theirs.ContentHash)

	ourOps, _ := c.opLog.GetSince(base.ID, ours.FilePath)
	theirOps, _ := c.opLog.GetSince(base.ID, theirs.FilePath)

	mergedContent, err := c.threeWayMergeContent(ctx, baseContent, oursContent, theirsContent, ourOps, theirOps, resolver)
	if err != nil {
		return VersionID{}, err
	}

	return c.createMergeVersion(ours, theirs, mergedContent)
}

func (c *DefaultCVS) threeWayMergeContent(ctx context.Context, base, ours, theirs []byte, ourOps, theirOps []*Operation, resolver ConflictResolver) ([]byte, error) {
	if string(ours) == string(theirs) {
		return ours, nil
	}

	if string(ours) == string(base) {
		return theirs, nil
	}

	if string(theirs) == string(base) {
		return ours, nil
	}

	return c.mergeOperations(ctx, base, ourOps, theirOps, resolver)
}

func (c *DefaultCVS) AcquireFileLock(filePath string, sessionID SessionID, ttl time.Duration) (*FileLock, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}

	ttl = c.effectiveLockTTL(ttl)

	c.lockMu.Lock()
	defer c.lockMu.Unlock()

	c.cleanExpiredLocks()

	if lock, err := c.tryRefreshExisting(filePath, sessionID, ttl); lock != nil || err != nil {
		return lock, err
	}

	return c.createNewLock(filePath, sessionID, ttl)
}

func (c *DefaultCVS) effectiveLockTTL(ttl time.Duration) time.Duration {
	if ttl == 0 {
		return c.defaultLockTTL
	}
	return ttl
}

func (c *DefaultCVS) tryRefreshExisting(filePath string, sessionID SessionID, ttl time.Duration) (*FileLock, error) {
	existing, ok := c.fileLocks[filePath]
	if !ok {
		return nil, nil
	}

	if existing.SessionID != sessionID {
		return nil, ErrCVSFileLocked
	}

	existing.ExpiresAt = time.Now().Add(ttl)
	return existing.Clone(), nil
}

func (c *DefaultCVS) createNewLock(filePath string, sessionID SessionID, ttl time.Duration) (*FileLock, error) {
	lock := &FileLock{
		ID:         generateFileLockID(filePath, sessionID),
		FilePath:   filePath,
		SessionID:  sessionID,
		AcquiredAt: time.Now(),
		ExpiresAt:  time.Now().Add(ttl),
	}

	c.fileLocks[filePath] = lock
	c.incrementStats(func(s *CVSStats) { s.ActiveLocks++ })

	return lock.Clone(), nil
}

func generateFileLockID(filePath string, sessionID SessionID) string {
	return string(sessionID) + ":" + filePath + ":" + itoa(int(time.Now().UnixNano()))
}

func (c *DefaultCVS) ReleaseFileLock(lockID string) error {
	if err := c.checkClosed(); err != nil {
		return err
	}

	c.lockMu.Lock()
	defer c.lockMu.Unlock()

	for path, lock := range c.fileLocks {
		if lock.ID == lockID {
			delete(c.fileLocks, path)
			c.incrementStats(func(s *CVSStats) { s.ActiveLocks-- })
			return nil
		}
	}

	return ErrCVSLockNotFound
}

func (c *DefaultCVS) RefreshFileLock(lockID string, ttl time.Duration) error {
	if err := c.checkClosed(); err != nil {
		return err
	}

	ttl = c.effectiveLockTTL(ttl)

	c.lockMu.Lock()
	defer c.lockMu.Unlock()

	return c.refreshLockByID(lockID, ttl)
}

func (c *DefaultCVS) refreshLockByID(lockID string, ttl time.Duration) error {
	for _, lock := range c.fileLocks {
		if lock.ID == lockID {
			lock.ExpiresAt = time.Now().Add(ttl)
			return nil
		}
	}
	return ErrCVSLockNotFound
}

func (c *DefaultCVS) checkFileLock(filePath string, sessionID SessionID) error {
	c.lockMu.RLock()
	defer c.lockMu.RUnlock()

	lock, ok := c.fileLocks[filePath]
	if !ok {
		return nil
	}

	if lock.IsExpired() {
		return nil
	}

	if lock.SessionID != sessionID {
		return ErrCVSFileLocked
	}

	return nil
}

func (c *DefaultCVS) cleanExpiredLocks() {
	now := time.Now()
	var expired []string

	for path, lock := range c.fileLocks {
		if lock.ExpiresAt.Before(now) {
			expired = append(expired, path)
		}
	}

	for _, path := range expired {
		delete(c.fileLocks, path)
		c.incrementStats(func(s *CVSStats) { s.ActiveLocks-- })
	}
}

func (c *DefaultCVS) Subscribe(filePath string, sessionID SessionID, cb FileChangeCallback) (SubscriptionID, error) {
	if err := c.checkClosed(); err != nil {
		return "", err
	}

	c.subMu.Lock()
	defer c.subMu.Unlock()

	subID := SubscriptionID(c.generateSubID())

	if c.subscriptions[filePath] == nil {
		c.subscriptions[filePath] = make(map[SubscriptionID]FileChangeCallback)
	}

	c.subscriptions[filePath][subID] = cb
	c.incrementStats(func(s *CVSStats) { s.ActiveSubscribers++ })

	return subID, nil
}

func (c *DefaultCVS) generateSubID() string {
	id := c.subCounter.Add(1)
	return "sub_" + itoa(int(id))
}

func (c *DefaultCVS) Unsubscribe(subID SubscriptionID) error {
	if err := c.checkClosed(); err != nil {
		return err
	}

	c.subMu.Lock()
	defer c.subMu.Unlock()

	return c.removeSubscription(subID)
}

func (c *DefaultCVS) removeSubscription(subID SubscriptionID) error {
	for path, subs := range c.subscriptions {
		if _, ok := subs[subID]; !ok {
			continue
		}
		c.deleteSubscription(path, subID, subs)
		return nil
	}
	return nil
}

func (c *DefaultCVS) deleteSubscription(path string, subID SubscriptionID, subs map[SubscriptionID]FileChangeCallback) {
	delete(subs, subID)
	if len(subs) == 0 {
		delete(c.subscriptions, path)
	}
	c.incrementStats(func(s *CVSStats) { s.ActiveSubscribers-- })
}

func (c *DefaultCVS) notifySubscribers(filePath string, newVer, oldVer VersionID, changeType FileChangeType, meta WriteMetadata) {
	c.subMu.RLock()
	subs := c.subscriptions[filePath]
	if len(subs) == 0 {
		c.subMu.RUnlock()
		return
	}

	callbacks := make([]FileChangeCallback, 0, len(subs))
	for _, cb := range subs {
		callbacks = append(callbacks, cb)
	}
	c.subMu.RUnlock()

	event := FileChangeEvent{
		FilePath:   filePath,
		NewVersion: newVer,
		OldVersion: oldVer,
		ChangeType: changeType,
		SessionID:  meta.SessionID,
		PipelineID: meta.PipelineID,
		Timestamp:  time.Now(),
	}

	for _, cb := range callbacks {
		go cb(event)
	}
}

func (c *DefaultCVS) Stats() CVSStats {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.stats
}

func (c *DefaultCVS) incrementStats(fn func(*CVSStats)) {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	fn(&c.stats)
}

func (c *DefaultCVS) Close() error {
	c.closedMu.Lock()
	defer c.closedMu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	c.pipelineMu.Lock()
	for id := range c.pipelines {
		c.vfsManager.ClosePipelineVFS(id)
	}
	c.pipelines = nil
	c.pipelineMu.Unlock()

	c.lockMu.Lock()
	c.fileLocks = nil
	c.lockMu.Unlock()

	c.subMu.Lock()
	c.subscriptions = nil
	c.subMu.Unlock()

	if c.wal != nil {
		c.wal.Close()
	}

	return c.vfsManager.Close()
}

func (c *DefaultCVS) checkClosed() error {
	c.closedMu.RLock()
	defer c.closedMu.RUnlock()
	if c.closed {
		return ErrCVSClosed
	}
	return nil
}
