package versioning

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/memorybudget"
)

var (
	ErrVFSClosed            = errors.New("VFS is closed")
	ErrVFSInTransaction     = errors.New("VFS already in transaction")
	ErrVFSNotInTx           = errors.New("VFS not in transaction")
	ErrFileNotFound         = fmt.Errorf("file not found: %w", fs.ErrNotExist)
	ErrFileExists           = fmt.Errorf("file already exists: %w", fs.ErrExist)
	ErrPermissionDenied     = fmt.Errorf("permission denied: %w", fs.ErrPermission)
	ErrPathOutsideBounds    = errors.New("path outside allowed bounds")
	ErrInvalidVersion       = errors.New("invalid version")
	ErrTransactionAborted   = errors.New("transaction aborted")
	ErrMemoryBudgetExceeded = errors.New("VFS memory budget exceeded")
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
	FileOpMkdir
)

func (op FileOp) String() string {
	switch op {
	case FileOpCreate:
		return "create"
	case FileOpModify:
		return "modify"
	case FileOpDelete:
		return "delete"
	case FileOpMkdir:
		return "mkdir"
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
	MkdirAll(ctx context.Context, path string) error
	Stat(ctx context.Context, path string) (fs.FileInfo, error)

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
	visiblePaths   map[string]struct{}
	createdDirs    map[string]fs.FileMode
	baseFS         vfsBaseFS
	baseReader     func(string) ([]byte, error)
	memory         *memorybudget.Reservation
}

type VersionStore interface {
	GetHead(filePath string) (*FileVersion, error)
	GetVersion(id VersionID) (*FileVersion, error)
	GetHistory(filePath string, limit int) ([]FileVersion, error)
	AddVersion(version FileVersion) error
}

func NewPipelineVFS(cfg VFSConfig, versionStore VersionStore, blobStore BlobStore) *PipelineVFS {
	reservation, _ := memorybudget.Current().Reserve(memorybudget.ScopeOverlay, 0)
	return &PipelineVFS{
		config:         cfg,
		modifications:  make(map[string]*FileModification),
		stagedContent:  make(map[string][]byte),
		deletedPaths:   make(map[string]bool),
		versionStore:   versionStore,
		blobStore:      blobStore,
		baseVersions:   make(map[string]VersionID),
		workingContent: make(map[string][]byte),
		visiblePaths:   make(map[string]struct{}),
		createdDirs:    make(map[string]fs.FileMode),
		memory:         reservation,
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

	if content, ok := v.workingContent[absPath]; ok {
		return cloneBytes(content), nil
	}

	if v.baseFS != nil {
		content, err := v.baseFS.ReadFile(absPath)
		if err == nil {
			return cloneBytes(content), nil
		}
		if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}

	if v.baseReader != nil {
		content, err := v.baseReader(absPath)
		if err == nil {
			return cloneBytes(content), nil
		}
		if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
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

func (v *PipelineVFS) MkdirAll(ctx context.Context, path string) error {
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
	return v.stageMkdirAll(absPath)
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
	prev := v.captureOverlayState(absPath)
	op, oldContent := v.determineWriteOp(absPath)
	baseVersion := v.getBaseVersion(absPath)

	contentHash := ComputeContentHash(content)
	v.stagedContent[absPath] = cloneBytes(content)
	delete(v.deletedPaths, absPath)
	v.visiblePaths[absPath] = struct{}{}
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
	return v.commitOverlayState(absPath, prev)
}

func (v *PipelineVFS) determineWriteOp(absPath string) (FileOp, []byte) {
	if v.deletedPaths[absPath] {
		delete(v.deletedPaths, absPath)
		return FileOpCreate, nil
	}

	if _, exists := v.stagedContent[absPath]; exists {
		return FileOpModify, nil
	}

	existing, err := v.readExistingContent(absPath)
	if err == ErrFileNotFound {
		return FileOpCreate, nil
	}
	return FileOpModify, existing
}

func (v *PipelineVFS) readExistingContent(absPath string) ([]byte, error) {
	if content, ok := v.workingContent[absPath]; ok {
		return cloneBytes(content), nil
	}
	if v.baseFS != nil {
		content, err := v.baseFS.ReadFile(absPath)
		if err == nil {
			return cloneBytes(content), nil
		}
		if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	if v.baseReader != nil {
		content, err := v.baseReader(absPath)
		if err == nil {
			return cloneBytes(content), nil
		}
		if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return v.readFromDisk(absPath)
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

func (v *PipelineVFS) stageMkdirAll(absPath string) error {
	segments := make([]string, 0, 8)
	for current := absPath; current != "" && isUnderPath(current, v.config.WorkingDir); current = filepath.Dir(current) {
		segments = append(segments, current)
		if current == v.config.WorkingDir {
			break
		}
	}
	slices.Reverse(segments)

	for _, dirPath := range segments {
		if dirPath == v.config.WorkingDir {
			continue
		}
		exists, isDir, err := v.pathState(dirPath)
		if err != nil {
			return err
		}
		if exists {
			if !isDir {
				return ErrFileExists
			}
			continue
		}
		v.createdDirs[dirPath] = fs.ModeDir | 0o755
		v.visiblePaths[dirPath] = struct{}{}
		v.modifications[dirPath] = &FileModification{
			OriginalPath: dirPath,
			Operation:    FileOpMkdir,
			Timestamp:    time.Now(),
		}
	}
	return nil
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
	prev := v.captureOverlayState(absPath)
	oldContent, err := v.getDeleteContent(absPath)
	if err != nil {
		return err
	}

	baseVersion := v.getBaseVersion(absPath)

	v.deletedPaths[absPath] = true
	v.visiblePaths[absPath] = struct{}{}
	v.modifications[absPath] = &FileModification{
		OriginalPath: absPath,
		Operation:    FileOpDelete,
		Timestamp:    time.Now(),
		BaseVersion:  baseVersion,
		OldContent:   oldContent,
	}
	v.baseVersions[absPath] = baseVersion

	return v.commitOverlayState(absPath, prev)
}

func (v *PipelineVFS) getDeleteContent(absPath string) ([]byte, error) {
	if content, ok := v.stagedContent[absPath]; ok {
		delete(v.stagedContent, absPath)
		return content, nil
	}

	existing, err := v.readExistingContent(absPath)
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

	return v.pathExists(absPath)
}

func (v *PipelineVFS) pathExists(absPath string) (bool, error) {
	if v.deletedPaths[absPath] {
		return false, nil
	}

	if _, ok := v.createdDirs[absPath]; ok {
		return true, nil
	}

	if _, ok := v.stagedContent[absPath]; ok {
		return true, nil
	}

	if _, ok := v.workingContent[absPath]; ok {
		return true, nil
	}

	if v.hasVisibleDescendantLocked(absPath) {
		return true, nil
	}

	if v.baseFS != nil {
		return v.baseFS.Exists(absPath)
	}

	if _, ok := v.visiblePaths[absPath]; ok && v.baseReader != nil {
		_, err := v.baseReader(absPath)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
			return false, nil
		}
		return false, err
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

	if v.hasSparseWorkspace() {
		return v.listVisibleEntries(absDir), nil
	}

	files, err := v.listBaseFiles(absDir)
	if err != nil {
		exists, isDir, stateErr := v.pathState(absDir)
		if stateErr != nil {
			return nil, stateErr
		}
		if !exists || !isDir {
			return nil, err
		}
		files = nil
	}

	files = v.addStagedFiles(files, absDir)
	files = v.addCreatedDirs(files, absDir)
	slices.Sort(files)
	return files, nil
}

func (v *PipelineVFS) listBaseFiles(absDir string) ([]string, error) {
	if v.baseFS != nil {
		entries, err := v.baseFS.ListDir(absDir)
		if err != nil {
			return nil, v.mapReadDirErr(err)
		}
		return v.filterListedNames(entries, absDir), nil
	}
	return v.listDiskFiles(absDir)
}

func (v *PipelineVFS) listDiskFiles(absDir string) ([]string, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, v.mapReadDirErr(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return v.filterListedNames(names, absDir), nil
}

func (v *PipelineVFS) mapReadDirErr(err error) error {
	if os.IsNotExist(err) {
		return ErrFileNotFound
	}
	return err
}

func (v *PipelineVFS) filterListedNames(entries []string, absDir string) []string {
	files := make([]string, 0, len(entries))
	for _, name := range entries {
		fullPath := filepath.Join(absDir, name)
		if !v.deletedPaths[fullPath] {
			files = append(files, name)
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

func (v *PipelineVFS) addCreatedDirs(files []string, absDir string) []string {
	for created := range v.createdDirs {
		if filepath.Dir(created) != absDir {
			continue
		}
		name := filepath.Base(created)
		if !containsString(files, name) {
			files = append(files, name)
		}
	}
	return files
}

func (v *PipelineVFS) hasSparseWorkspace() bool {
	return len(v.config.AllowedPaths) > 0
}

func (v *PipelineVFS) listVisibleEntries(absDir string) []string {
	seen := make(map[string]struct{})
	files := make([]string, 0, len(v.visiblePaths)+len(v.stagedContent))
	for _, absPath := range v.visibleCandidatePathsLocked() {
		if v.deletedPaths[absPath] {
			continue
		}
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		name := rel
		if idx := strings.IndexRune(rel, filepath.Separator); idx >= 0 {
			name = rel[:idx]
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		files = append(files, name)
	}
	slices.Sort(files)
	return files
}

func (v *PipelineVFS) visibleCandidatePathsLocked() []string {
	paths := make([]string, 0, len(v.visiblePaths)+len(v.stagedContent)+len(v.createdDirs))
	seen := make(map[string]struct{}, len(v.visiblePaths)+len(v.stagedContent)+len(v.createdDirs))
	for path := range v.visiblePaths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range v.stagedContent {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range v.createdDirs {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
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

// AllowedPaths returns the normalized sparse workspace bounds for this VFS.
func (v *PipelineVFS) AllowedPaths() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.config.AllowedPaths) == 0 {
		return nil
	}
	out := make([]string, len(v.config.AllowedPaths))
	copy(out, v.config.AllowedPaths)
	return out
}

// VisiblePaths returns tracked visible file paths under root. Paths are
// absolute and reflect seeded workspace files plus staged additions.
func (v *PipelineVFS) VisiblePaths(root string) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if root == "" {
		root = v.config.WorkingDir
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(v.config.WorkingDir, root)
	}
	root = filepath.Clean(root)

	paths := make([]string, 0, len(v.visiblePaths)+len(v.stagedContent))
	for _, absPath := range v.visibleCandidatePathsLocked() {
		if v.deletedPaths[absPath] {
			continue
		}
		if !isUnderPath(absPath, root) {
			continue
		}
		paths = append(paths, absPath)
	}
	slices.Sort(paths)
	return paths
}

// RegisterVisiblePath records a path as part of this pipeline's sparse
// workspace even if the file does not yet exist.
func (v *PipelineVFS) RegisterVisiblePath(path string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.config.WorkingDir, path)
	}
	v.visiblePaths[filepath.Clean(path)] = struct{}{}
}

// SeedFile primes this pipeline with a base snapshot from the session-global
// overlay so reads stay inside in-memory session truth.
func (v *PipelineVFS) SeedFile(path string, content []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.config.WorkingDir, path)
	}
	path = filepath.Clean(path)
	prev := v.captureOverlayState(path)
	v.visiblePaths[path] = struct{}{}
	v.workingContent[path] = cloneBytes(content)
	if err := v.commitOverlayState(path, prev); err != nil {
		return
	}
}

// SetBaseReader installs the authoritative base reader used for sparse
// workspace reads when a tracked file is not locally staged.
func (v *PipelineVFS) SetBaseReader(reader func(string) ([]byte, error)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.baseReader = reader
}

func (v *PipelineVFS) SetBaseFS(base vfsBaseFS) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.baseFS = base
}

// KnownFileCount returns the number of tracked visible paths in this VFS.
func (v *PipelineVFS) KnownFileCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.visibleCandidatePathsLocked())
}

// HasVisibleDescendant reports whether path has any tracked visible children.
func (v *PipelineVFS) HasVisibleDescendant(path string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.hasVisibleDescendantLocked(path)
}

func (v *PipelineVFS) hasVisibleDescendantLocked(path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.config.WorkingDir, path)
	}
	path = filepath.Clean(path)
	prefix := path + string(filepath.Separator)
	for _, candidate := range v.visibleCandidatePathsLocked() {
		if v.deletedPaths[candidate] {
			continue
		}
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

// ResetOverlay clears all staged content, deleted paths, modifications,
// base versions, and working content. Used after a disk flush when the
// overlay is no longer needed (disk is source of truth).
func (v *PipelineVFS) ResetOverlay() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.stagedContent = make(map[string][]byte)
	v.deletedPaths = make(map[string]bool)
	v.modifications = make(map[string]*FileModification)
	v.baseVersions = make(map[string]VersionID)
	v.workingContent = make(map[string][]byte)
	v.visiblePaths = make(map[string]struct{})
	v.createdDirs = make(map[string]fs.FileMode)
	v.releaseOverlayMemoryLocked()
}

type overlayStateSnapshot struct {
	path         string
	staged       []byte
	hasStaged    bool
	deleted      bool
	visible      bool
	modification *FileModification
	hasMod       bool
	baseVersion  VersionID
	hasBase      bool
	working      []byte
	hasWorking   bool
}

func (v *PipelineVFS) captureOverlayState(path string) overlayStateSnapshot {
	state := overlayStateSnapshot{
		path:        path,
		deleted:     v.deletedPaths[path],
		visible:     v.hasVisiblePathLocked(path),
		baseVersion: v.baseVersions[path],
	}
	state.staged, state.hasStaged = cloneOptionalBytes(v.stagedContent[path])
	state.working, state.hasWorking = cloneOptionalBytes(v.workingContent[path])
	state.modification, state.hasMod = cloneOptionalModification(v.modifications[path])
	_, state.hasBase = v.baseVersions[path]
	return state
}

func (v *PipelineVFS) commitOverlayState(path string, previous overlayStateSnapshot) error {
	if err := v.reconcileOverlayMemoryLocked(); err == nil {
		return nil
	}
	v.restoreOverlayState(previous)
	_ = v.reconcileOverlayMemoryLocked()
	return ErrMemoryBudgetExceeded
}

func (v *PipelineVFS) restoreOverlayState(previous overlayStateSnapshot) {
	restoreBytesEntry(v.stagedContent, previous.path, previous.staged, previous.hasStaged)
	restoreBytesEntry(v.workingContent, previous.path, previous.working, previous.hasWorking)
	restoreBoolEntry(v.deletedPaths, previous.path, previous.deleted)
	restoreVisibleEntry(v.visiblePaths, previous.path, previous.visible)
	restoreModificationEntry(v.modifications, previous.path, previous.modification, previous.hasMod)
	restoreVersionEntry(v.baseVersions, previous.path, previous.baseVersion, previous.hasBase)
}

func (v *PipelineVFS) reconcileOverlayMemoryLocked() error {
	if v.memory == nil {
		return nil
	}
	if err := v.memory.Resize(v.overlayMemoryBytesLocked()); err != nil {
		return ErrMemoryBudgetExceeded
	}
	return nil
}

func (v *PipelineVFS) overlayMemoryBytesLocked() int64 {
	var total int64
	for _, content := range v.stagedContent {
		total += int64(len(content))
	}
	for _, content := range v.workingContent {
		total += int64(len(content))
	}
	for _, mod := range v.modifications {
		if mod == nil {
			continue
		}
		total += int64(len(mod.NewContent) + len(mod.OldContent))
	}
	return total
}

func (v *PipelineVFS) releaseOverlayMemoryLocked() {
	if v.memory != nil {
		v.memory.Release()
	}
}

func (v *PipelineVFS) hasVisiblePathLocked(path string) bool {
	_, ok := v.visiblePaths[path]
	return ok
}

func cloneOptionalBytes(content []byte) ([]byte, bool) {
	if content == nil {
		return nil, false
	}
	return cloneBytes(content), true
}

func cloneOptionalModification(mod *FileModification) (*FileModification, bool) {
	if mod == nil {
		return nil, false
	}
	cloned := *mod
	cloned.NewContent = cloneBytes(mod.NewContent)
	cloned.OldContent = cloneBytes(mod.OldContent)
	return &cloned, true
}

func restoreBytesEntry(dst map[string][]byte, path string, content []byte, present bool) {
	if present {
		dst[path] = cloneBytes(content)
		return
	}
	delete(dst, path)
}

func restoreBoolEntry(dst map[string]bool, path string, value bool) {
	if value {
		dst[path] = true
		return
	}
	delete(dst, path)
}

func restoreVisibleEntry(dst map[string]struct{}, path string, present bool) {
	if present {
		dst[path] = struct{}{}
		return
	}
	delete(dst, path)
}

func restoreModificationEntry(dst map[string]*FileModification, path string, mod *FileModification, present bool) {
	if present {
		cloned, _ := cloneOptionalModification(mod)
		dst[path] = cloned
		return
	}
	delete(dst, path)
}

func restoreVersionEntry(dst map[string]VersionID, path string, value VersionID, present bool) {
	if present {
		dst[path] = value
		return
	}
	delete(dst, path)
}

// NewGlobalVFS creates a PipelineVFS for use as the session-scoped global overlay.
// It passes nil for versionStore/blobStore since global VFS operates purely as
// an overlay on top of disk, not through the old CVS version DAG.
func NewGlobalVFS(cfg VFSConfig) *PipelineVFS {
	return NewPipelineVFS(cfg, nil, nil)
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
	v.visiblePaths = nil
	v.createdDirs = nil
	v.releaseOverlayMemoryLocked()

	return nil
}

func (v *PipelineVFS) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
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

	if mode, ok := v.createdDirs[absPath]; ok {
		return snapshotFileInfo{name: filepath.Base(absPath), mode: mode, modTime: time.Now().UTC()}, nil
	}
	if content, ok := v.stagedContent[absPath]; ok {
		return snapshotFileInfo{name: filepath.Base(absPath), size: int64(len(content)), mode: 0o644, modTime: time.Now().UTC()}, nil
	}
	if content, ok := v.workingContent[absPath]; ok {
		return snapshotFileInfo{name: filepath.Base(absPath), size: int64(len(content)), mode: 0o644, modTime: time.Now().UTC()}, nil
	}
	if v.hasVisibleDescendantLocked(absPath) {
		return snapshotFileInfo{name: filepath.Base(absPath), mode: fs.ModeDir | 0o755, modTime: time.Now().UTC()}, nil
	}
	if v.baseFS != nil {
		return v.baseFS.Stat(absPath)
	}
	return os.Stat(absPath)
}

func (v *PipelineVFS) pathState(absPath string) (exists bool, isDir bool, err error) {
	if v.deletedPaths[absPath] {
		return false, false, nil
	}
	if _, ok := v.createdDirs[absPath]; ok {
		return true, true, nil
	}
	if _, ok := v.stagedContent[absPath]; ok {
		return true, false, nil
	}
	if _, ok := v.workingContent[absPath]; ok {
		return true, false, nil
	}
	if v.hasVisibleDescendantLocked(absPath) {
		return true, true, nil
	}
	if v.baseFS != nil {
		info, statErr := v.baseFS.Stat(absPath)
		if statErr == nil {
			return true, info.IsDir(), nil
		}
		if errors.Is(statErr, ErrFileNotFound) || os.IsNotExist(statErr) {
			return false, false, nil
		}
		return false, false, statErr
	}
	info, statErr := os.Stat(absPath)
	if statErr == nil {
		return true, info.IsDir(), nil
	}
	if os.IsNotExist(statErr) {
		return false, false, nil
	}
	return false, false, statErr
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
		return isUnderPath(absPath, v.config.WorkingDir) || v.isVisiblePathAllowed(absPath)
	}

	for _, allowed := range v.config.AllowedPaths {
		if isUnderPath(absPath, allowed) {
			return true
		}
	}
	return v.isVisiblePathAllowed(absPath)
}

func (v *PipelineVFS) isVisiblePathAllowed(absPath string) bool {
	if len(v.visiblePaths) == 0 {
		return false
	}
	for visible := range v.visiblePaths {
		if isUnderPath(absPath, visible) {
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

	if content, ok := tx.vfs.workingContent[absPath]; ok {
		return cloneBytes(content), nil
	}

	if tx.vfs.baseReader != nil {
		content, err := tx.vfs.baseReader(absPath)
		if err == nil {
			return cloneBytes(content), nil
		}
		if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
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
	tx.vfs.visiblePaths[absPath] = struct{}{}
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
	tx.vfs.visiblePaths[absPath] = struct{}{}
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
