package versioning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

var (
	ErrVFSClosed          = errors.New("VFS is closed")
	ErrVFSInTransaction   = errors.New("VFS already in transaction")
	ErrVFSNotInTx         = errors.New("VFS not in transaction")
	ErrFileNotFound       = errors.New("file not found")
	ErrFileExists         = errors.New("file already exists")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrPathOutsideBounds  = errors.New("path outside allowed bounds")
	ErrInvalidVersion     = errors.New("invalid version")
	ErrTransactionAborted = errors.New("transaction aborted")
)

type FileModification struct {
	OriginalPath string
	StagingPath  string
	Operation    FileOp
	Timestamp    time.Time
	ContentHash  ContentHash
	BaseVersion  VersionID
	NewContent   []byte
	OldContent   []byte
}

type FileOp int

const (
	FileOpCreate FileOp = iota
	FileOpModify
	FileOpDelete
)

func (op FileOp) String() string {
	switch op {
	case FileOpCreate:
		return "create"
	case FileOpModify:
		return "modify"
	case FileOpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

type FileDiff struct {
	FilePath    string
	BaseVersion VersionID
	NewVersion  VersionID
	Hunks       []DiffHunk
	Stats       DiffStats
}

type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

type DiffLine struct {
	Type    DiffLineType
	Content string
	OldLine int
	NewLine int
}

type DiffLineType int

const (
	DiffLineContext DiffLineType = iota
	DiffLineAdd
	DiffLineDelete
)

type DiffStats struct {
	Additions int
	Deletions int
	Changes   int
}

type VFSConfig struct {
	PipelineID      string
	SessionID       SessionID
	WorkingDir      string
	StagingDir      string
	AllowedPaths    []string
	ReadOnly        bool
	AgentID         string
	AgentRole       string
	PermChecker     PermissionChecker
	SecretSanitizer SecretSanitizerInterface
}

type PermissionChecker interface {
	CheckFileRead(ctx context.Context, agentID, agentRole, path string) error
	CheckFileWrite(ctx context.Context, agentID, agentRole, path string) error
	CheckFileDelete(ctx context.Context, agentID, agentRole, path string) error
}

type SecretSanitizerInterface interface {
	SanitizeForIndex(path string, content []byte) ([]byte, bool)
}

type VFS interface {
	Read(ctx context.Context, path string) ([]byte, error)
	Write(ctx context.Context, path string, content []byte) error
	Delete(ctx context.Context, path string) error
	Exists(ctx context.Context, path string) (bool, error)
	List(ctx context.Context, dir string) ([]string, error)

	ReadAt(ctx context.Context, path string, version VersionID) ([]byte, error)
	History(ctx context.Context, path string, limit int) ([]FileVersion, error)
	Diff(ctx context.Context, path string, base, target VersionID) (*FileDiff, error)

	BeginTransaction(ctx context.Context) (Transaction, error)

	GetModifications() []FileModification
	GetPipelineID() string
	GetSessionID() SessionID

	Close() error
}

type Transaction interface {
	Read(ctx context.Context, path string) ([]byte, error)
	Write(ctx context.Context, path string, content []byte) error
	Delete(ctx context.Context, path string) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type PipelineVFS struct {
	mu sync.RWMutex

	config         VFSConfig
	closed         bool
	inTransaction  bool
	currentTx      *pipelineTx
	modifications  map[string]*FileModification
	stagedContent  map[string][]byte
	deletedPaths   map[string]bool
	versionStore   VersionStore
	blobStore      BlobStore
	baseVersions   map[string]VersionID
	workingContent map[string][]byte
}

type VersionStore interface {
	GetHead(filePath string) (*FileVersion, error)
	GetVersion(id VersionID) (*FileVersion, error)
	GetHistory(filePath string, limit int) ([]FileVersion, error)
	AddVersion(version FileVersion) error
}

func NewPipelineVFS(cfg VFSConfig, versionStore VersionStore, blobStore BlobStore) *PipelineVFS {
	return &PipelineVFS{
		config:         cfg,
		modifications:  make(map[string]*FileModification),
		stagedContent:  make(map[string][]byte),
		deletedPaths:   make(map[string]bool),
		versionStore:   versionStore,
		blobStore:      blobStore,
		baseVersions:   make(map[string]VersionID),
		workingContent: make(map[string][]byte),
	}
}

func (v *PipelineVFS) Read(ctx context.Context, path string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil, ErrVFSClosed
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return nil, err
	}

	if err := v.checkReadPermission(ctx, absPath); err != nil {
		return nil, err
	}

	return v.readContent(absPath)
}

func (v *PipelineVFS) readContent(absPath string) ([]byte, error) {
	if v.deletedPaths[absPath] {
		return nil, ErrFileNotFound
	}

	if content, ok := v.stagedContent[absPath]; ok {
		return cloneBytes(content), nil
	}

	return v.readFromDisk(absPath)
}

func (v *PipelineVFS) checkReadPermission(ctx context.Context, path string) error {
	if v.config.PermChecker == nil {
		return nil
	}
	return v.config.PermChecker.CheckFileRead(ctx, v.config.AgentID, v.config.AgentRole, path)
}

func (v *PipelineVFS) readFromDisk(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return content, nil
}

func (v *PipelineVFS) Write(ctx context.Context, path string, content []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.checkWritePrereqs(); err != nil {
		return err
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return err
	}

	if err := v.checkWritePermission(ctx, absPath); err != nil {
		return err
	}

	return v.stageWrite(absPath, content)
}

func (v *PipelineVFS) checkWritePrereqs() error {
	if v.closed {
		return ErrVFSClosed
	}
	if v.config.ReadOnly {
		return ErrPermissionDenied
	}
	return nil
}

func (v *PipelineVFS) checkWritePermission(ctx context.Context, path string) error {
	if v.config.PermChecker == nil {
		return nil
	}
	return v.config.PermChecker.CheckFileWrite(ctx, v.config.AgentID, v.config.AgentRole, path)
}

func (v *PipelineVFS) stageWrite(absPath string, content []byte) error {
	op, oldContent := v.determineWriteOp(absPath)
	baseVersion := v.getBaseVersion(absPath)

	contentHash := ComputeContentHash(content)
	sanitizedContent := v.sanitizeContent(absPath, content)

	v.stagedContent[absPath] = cloneBytes(sanitizedContent)
	v.modifications[absPath] = &FileModification{
		OriginalPath: absPath,
		StagingPath:  v.getStagingPath(absPath),
		Operation:    op,
		Timestamp:    time.Now(),
		ContentHash:  contentHash,
		BaseVersion:  baseVersion,
		NewContent:   cloneBytes(content),
		OldContent:   oldContent,
	}
	v.baseVersions[absPath] = baseVersion

	return nil
}

func (v *PipelineVFS) determineWriteOp(absPath string) (FileOp, []byte) {
	if v.deletedPaths[absPath] {
		delete(v.deletedPaths, absPath)
		return FileOpCreate, nil
	}

	if _, exists := v.stagedContent[absPath]; exists {
		return FileOpModify, nil
	}

	existing, err := v.readFromDisk(absPath)
	if err == ErrFileNotFound {
		return FileOpCreate, nil
	}
	return FileOpModify, existing
}

func (v *PipelineVFS) getBaseVersion(absPath string) VersionID {
	if v.versionStore == nil {
		return VersionID{}
	}
	head, err := v.versionStore.GetHead(absPath)
	if err != nil || head == nil {
		return VersionID{}
	}
	return head.ID
}

func (v *PipelineVFS) sanitizeContent(path string, content []byte) []byte {
	if v.config.SecretSanitizer == nil {
		return content
	}
	sanitized, _ := v.config.SecretSanitizer.SanitizeForIndex(path, content)
	return sanitized
}

func (v *PipelineVFS) getStagingPath(absPath string) string {
	if v.config.StagingDir == "" {
		return ""
	}
	rel, err := filepath.Rel(v.config.WorkingDir, absPath)
	if err != nil {
		rel = filepath.Base(absPath)
	}
	return filepath.Join(v.config.StagingDir, rel)
}

func (v *PipelineVFS) Delete(ctx context.Context, path string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.checkWritePrereqs(); err != nil {
		return err
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return err
	}

	if err := v.checkDeletePermission(ctx, absPath); err != nil {
		return err
	}

	return v.stageDelete(absPath)
}

func (v *PipelineVFS) checkDeletePermission(ctx context.Context, path string) error {
	if v.config.PermChecker == nil {
		return nil
	}
	return v.config.PermChecker.CheckFileDelete(ctx, v.config.AgentID, v.config.AgentRole, path)
}

func (v *PipelineVFS) stageDelete(absPath string) error {
	oldContent, err := v.getDeleteContent(absPath)
	if err != nil {
		return err
	}

	baseVersion := v.getBaseVersion(absPath)

	v.deletedPaths[absPath] = true
	v.modifications[absPath] = &FileModification{
		OriginalPath: absPath,
		Operation:    FileOpDelete,
		Timestamp:    time.Now(),
		BaseVersion:  baseVersion,
		OldContent:   oldContent,
	}
	v.baseVersions[absPath] = baseVersion

	return nil
}

func (v *PipelineVFS) getDeleteContent(absPath string) ([]byte, error) {
	if content, ok := v.stagedContent[absPath]; ok {
		delete(v.stagedContent, absPath)
		return content, nil
	}

	existing, err := v.readFromDisk(absPath)
	if err != nil {
		return nil, err
	}
	return existing, nil
}

func (v *PipelineVFS) Exists(ctx context.Context, path string) (bool, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return false, ErrVFSClosed
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return false, err
	}

	return v.fileExists(absPath)
}

func (v *PipelineVFS) fileExists(absPath string) (bool, error) {
	if v.deletedPaths[absPath] {
		return false, nil
	}

	if _, ok := v.stagedContent[absPath]; ok {
		return true, nil
	}

	return v.existsOnDisk(absPath)
}

func (v *PipelineVFS) existsOnDisk(absPath string) (bool, error) {
	_, err := os.Stat(absPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (v *PipelineVFS) List(ctx context.Context, dir string) ([]string, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil, ErrVFSClosed
	}

	absDir, err := v.resolvePath(dir)
	if err != nil {
		return nil, err
	}

	files, err := v.listDiskFiles(absDir)
	if err != nil {
		return nil, err
	}

	files = v.addStagedFiles(files, absDir)
	return files, nil
}

func (v *PipelineVFS) listDiskFiles(absDir string) ([]string, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, v.mapReadDirErr(err)
	}
	return v.filterDirEntries(entries, absDir), nil
}

func (v *PipelineVFS) mapReadDirErr(err error) error {
	if os.IsNotExist(err) {
		return ErrFileNotFound
	}
	return err
}

func (v *PipelineVFS) filterDirEntries(entries []os.DirEntry, absDir string) []string {
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		fullPath := filepath.Join(absDir, entry.Name())
		if !v.deletedPaths[fullPath] {
			files = append(files, entry.Name())
		}
	}
	return files
}

func (v *PipelineVFS) addStagedFiles(files []string, absDir string) []string {
	for stagedPath := range v.stagedContent {
		if filepath.Dir(stagedPath) != absDir {
			continue
		}
		name := filepath.Base(stagedPath)
		if !containsString(files, name) {
			files = append(files, name)
		}
	}
	return files
}

func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

func (v *PipelineVFS) ReadAt(ctx context.Context, path string, version VersionID) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil, ErrVFSClosed
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return nil, err
	}

	if err := v.checkReadPermission(ctx, absPath); err != nil {
		return nil, err
	}

	return v.readVersionContent(version)
}

func (v *PipelineVFS) readVersionContent(version VersionID) ([]byte, error) {
	if v.versionStore == nil || v.blobStore == nil {
		return nil, ErrVersionNotFound
	}

	fileVersion, err := v.getFileVersion(version)
	if err != nil {
		return nil, err
	}

	return v.blobStore.Get(fileVersion.ContentHash)
}

func (v *PipelineVFS) getFileVersion(version VersionID) (*FileVersion, error) {
	fileVersion, err := v.versionStore.GetVersion(version)
	if err != nil {
		return nil, err
	}
	if fileVersion == nil {
		return nil, ErrVersionNotFound
	}
	return fileVersion, nil
}

func (v *PipelineVFS) History(ctx context.Context, path string, limit int) ([]FileVersion, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil, ErrVFSClosed
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return nil, err
	}

	if err := v.checkReadPermission(ctx, absPath); err != nil {
		return nil, err
	}

	return v.getFileHistory(absPath, limit)
}

func (v *PipelineVFS) getFileHistory(absPath string, limit int) ([]FileVersion, error) {
	if v.versionStore == nil {
		return nil, nil
	}
	return v.versionStore.GetHistory(absPath, limit)
}

func (v *PipelineVFS) Diff(ctx context.Context, path string, base, target VersionID) (*FileDiff, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return nil, ErrVFSClosed
	}

	absPath, err := v.resolvePath(path)
	if err != nil {
		return nil, err
	}

	if err := v.checkReadPermission(ctx, absPath); err != nil {
		return nil, err
	}

	return &FileDiff{
		FilePath:    absPath,
		BaseVersion: base,
		NewVersion:  target,
		Hunks:       []DiffHunk{},
		Stats:       DiffStats{},
	}, nil
}

func (v *PipelineVFS) BeginTransaction(ctx context.Context) (Transaction, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return nil, ErrVFSClosed
	}

	if v.inTransaction {
		return nil, ErrVFSInTransaction
	}

	tx := &pipelineTx{
		vfs:       v,
		staged:    make(map[string][]byte),
		deleted:   make(map[string]bool),
		committed: false,
	}
	v.inTransaction = true
	v.currentTx = tx

	return tx, nil
}

func (v *PipelineVFS) GetModifications() []FileModification {
	v.mu.RLock()
	defer v.mu.RUnlock()

	mods := make([]FileModification, 0, len(v.modifications))
	for _, mod := range v.modifications {
		mods = append(mods, *mod)
	}
	return mods
}

func (v *PipelineVFS) GetPipelineID() string {
	return v.config.PipelineID
}

func (v *PipelineVFS) GetSessionID() SessionID {
	return v.config.SessionID
}

func (v *PipelineVFS) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return nil
	}

	v.closed = true
	v.stagedContent = nil
	v.deletedPaths = nil
	v.modifications = nil
	v.workingContent = nil
	v.baseVersions = nil

	return nil
}

func (v *PipelineVFS) resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.config.WorkingDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	if !v.isPathAllowed(absPath) {
		return "", ErrPathOutsideBounds
	}

	return absPath, nil
}

func (v *PipelineVFS) isPathAllowed(absPath string) bool {
	if len(v.config.AllowedPaths) == 0 {
		return isUnderPath(absPath, v.config.WorkingDir)
	}

	for _, allowed := range v.config.AllowedPaths {
		if isUnderPath(absPath, allowed) {
			return true
		}
	}
	return false
}

func isUnderPath(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return isValidRelPath(rel)
}

func isValidRelPath(rel string) bool {
	if rel == "." {
		return true
	}
	if len(rel) == 0 || filepath.IsAbs(rel) {
		return false
	}
	return !isParentRef(rel)
}

func isParentRef(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.'
}

type pipelineTx struct {
	vfs       *PipelineVFS
	staged    map[string][]byte
	deleted   map[string]bool
	committed bool
}

func (tx *pipelineTx) Read(ctx context.Context, path string) ([]byte, error) {
	tx.vfs.mu.RLock()
	defer tx.vfs.mu.RUnlock()

	absPath, err := tx.vfs.resolvePath(path)
	if err != nil {
		return nil, err
	}

	return tx.readContent(absPath)
}

func (tx *pipelineTx) readContent(absPath string) ([]byte, error) {
	if tx.deleted[absPath] {
		return nil, ErrFileNotFound
	}

	if content, ok := tx.staged[absPath]; ok {
		return cloneBytes(content), nil
	}

	if content, ok := tx.vfs.stagedContent[absPath]; ok {
		return cloneBytes(content), nil
	}

	return tx.vfs.readFromDisk(absPath)
}

func (tx *pipelineTx) Write(ctx context.Context, path string, content []byte) error {
	tx.vfs.mu.Lock()
	defer tx.vfs.mu.Unlock()

	if tx.vfs.config.ReadOnly {
		return ErrPermissionDenied
	}

	absPath, err := tx.vfs.resolvePath(path)
	if err != nil {
		return err
	}

	if err := tx.vfs.checkWritePermission(ctx, absPath); err != nil {
		return err
	}

	delete(tx.deleted, absPath)
	tx.staged[absPath] = cloneBytes(content)
	return nil
}

func (tx *pipelineTx) Delete(ctx context.Context, path string) error {
	tx.vfs.mu.Lock()
	defer tx.vfs.mu.Unlock()

	if tx.vfs.config.ReadOnly {
		return ErrPermissionDenied
	}

	absPath, err := tx.vfs.resolvePath(path)
	if err != nil {
		return err
	}

	if err := tx.vfs.checkDeletePermission(ctx, absPath); err != nil {
		return err
	}

	delete(tx.staged, absPath)
	tx.deleted[absPath] = true
	return nil
}

func (tx *pipelineTx) Commit(ctx context.Context) error {
	tx.vfs.mu.Lock()
	defer tx.vfs.mu.Unlock()

	if tx.committed {
		return ErrTransactionAborted
	}

	if err := tx.commitWrites(); err != nil {
		return err
	}

	if err := tx.commitDeletes(); err != nil {
		return err
	}

	tx.finalize()
	return nil
}

func (tx *pipelineTx) commitWrites() error {
	for path, content := range tx.staged {
		if err := tx.vfs.stageWrite(path, content); err != nil {
			return err
		}
	}
	return nil
}

func (tx *pipelineTx) commitDeletes() error {
	for path := range tx.deleted {
		err := tx.vfs.stageDelete(path)
		if err != nil && err != ErrFileNotFound {
			return err
		}
	}
	return nil
}

func (tx *pipelineTx) finalize() {
	tx.committed = true
	tx.vfs.inTransaction = false
	tx.vfs.currentTx = nil
}

func (tx *pipelineTx) Rollback(ctx context.Context) error {
	tx.vfs.mu.Lock()
	defer tx.vfs.mu.Unlock()

	tx.staged = nil
	tx.deleted = nil
	tx.committed = true
	tx.vfs.inTransaction = false
	tx.vfs.currentTx = nil

	return nil
}
