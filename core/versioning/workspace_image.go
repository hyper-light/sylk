package versioning

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/memorybudget"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	defaultWorkspaceImageMaxBytes = 1 << 30
	workspaceImageChunkSize       = 64 << 10
	workspaceImageLimitEnv        = "SYLK_WORKSPACE_IMAGE_MAX_BYTES"
)

var (
	ErrWorkspaceImageTooLarge      = errors.New("session vfs: workspace image exceeds memory limit")
	ErrWorkspaceImagePathOutside   = errors.New("session vfs: workspace image path outside root")
	ErrWorkspaceImageUnsupportedFS = errors.New("session vfs: workspace image cannot import special filesystem entries")
	defaultImageRegistry           = newWorkspaceImageRegistry()
	workspaceImageBranchCounter    uint64
)

var workspaceImageIgnoredRootNames = map[string]struct{}{
	".git":  {},
	".hg":   {},
	".svn":  {},
	".sylk": {},
}

type workspaceImageManifest struct {
	root       string
	signature  string
	totalBytes int64
	entries    []workspaceImageEntry
}

type workspaceImageEntry struct {
	absPath    string
	relPath    string
	mode       fs.FileMode
	size       int64
	isDir      bool
	isSymlink  bool
	linkTarget string
}

type workspaceImage struct {
	root       string
	signature  string
	totalBytes int64
	workspace  *purevfs.Workspace
	snapshot   purevfs.SnapshotID
	memory     *memorybudget.Reservation
}

type workspaceImageHandle struct {
	image *workspaceImage
	key   string
	once  sync.Once
}

type workspaceImageRegistry struct {
	mu     sync.Mutex
	images map[string]*workspaceImageRef
}

type workspaceImageRef struct {
	image *workspaceImage
	refs  int
}

func newWorkspaceImageRegistry() *workspaceImageRegistry {
	return &workspaceImageRegistry{images: make(map[string]*workspaceImageRef)}
}

func acquireWorkspaceImage(cfg SessionVFSConfig) (*workspaceImageHandle, error) {
	return defaultImageRegistry.Acquire(cfg)
}

func (r *workspaceImageRegistry) Acquire(cfg SessionVFSConfig) (*workspaceImageHandle, error) {
	manifest, err := scanWorkspaceImage(cfg.WorkingDir, workspaceImageLimit(cfg))
	if err != nil {
		return nil, err
	}
	key := workspaceImageKey(manifest.root, manifest.signature)
	r.mu.Lock()
	if cached := r.images[key]; cached != nil {
		cached.refs++
		r.mu.Unlock()
		return &workspaceImageHandle{image: cached.image, key: key}, nil
	}
	r.mu.Unlock()

	image, err := importWorkspaceImage(manifest, workspaceImageLimit(cfg))
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if cached := r.images[key]; cached != nil {
		cached.refs++
		r.mu.Unlock()
		return &workspaceImageHandle{image: cached.image, key: key}, nil
	}
	r.images[key] = &workspaceImageRef{image: image, refs: 1}
	r.mu.Unlock()
	return &workspaceImageHandle{image: image, key: key}, nil
}

func (h *workspaceImageHandle) Release() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		defaultImageRegistry.Release(h.key)
	})
}

func (r *workspaceImageRegistry) Release(key string) {
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ref := r.images[key]
	if ref == nil {
		return
	}
	ref.refs--
	if ref.refs > 0 {
		return
	}
	if ref.image != nil && ref.image.memory != nil {
		ref.image.memory.Release()
	}
	delete(r.images, key)
}

func workspaceImageLimit(cfg SessionVFSConfig) int64 {
	if cfg.WorkspaceImageMaxBytes > 0 {
		return cfg.WorkspaceImageMaxBytes
	}
	raw := strings.TrimSpace(os.Getenv(workspaceImageLimitEnv))
	if raw == "" {
		return defaultWorkspaceImageMaxBytes
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		return defaultWorkspaceImageMaxBytes
	}
	return limit
}

func scanWorkspaceImage(root string, limit int64) (*workspaceImageManifest, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entries := make([]workspaceImageEntry, 0, 256)
	hasher := sha256.New()
	var total int64
	basePatterns := workspaceImageRootPatterns(absRoot)
	if err := scanWorkspaceDir(absRoot, absRoot, nil, basePatterns, hasher, &total, limit, &entries); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})
	return &workspaceImageManifest{
		root:       filepath.Clean(absRoot),
		signature:  hex.EncodeToString(hasher.Sum(nil)),
		totalBytes: total,
		entries:    entries,
	}, nil
}

func workspaceImageShouldSkip(rel string) bool {
	cleaned := strings.TrimSpace(filepath.ToSlash(rel))
	if cleaned == "" || cleaned == "." {
		return false
	}
	root := cleaned
	if cut := strings.IndexByte(cleaned, '/'); cut >= 0 {
		root = cleaned[:cut]
	}
	_, skip := workspaceImageIgnoredRootNames[root]
	return skip
}

func scanWorkspaceDir(
	root, dir string,
	relParts []string,
	parentPatterns []gitignore.Pattern,
	hasher io.Writer,
	total *int64,
	limit int64,
	entries *[]workspaceImageEntry,
) error {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	localPatterns := workspaceImageLocalPatterns(dir, relParts)
	allPatterns := workspaceImageExtendPatterns(parentPatterns, localPatterns)
	matcher := gitignore.NewMatcher(allPatterns)

	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		childRelParts := append(relParts[:len(relParts):len(relParts)], name)
		childRel := strings.Join(childRelParts, "/")
		if workspaceImageShouldSkip(childRel) || matcher.Match(childRelParts, dirEntry.IsDir()) {
			continue
		}

		childAbs := filepath.Join(dir, name)
		entry, err := scanWorkspaceEntry(root, childAbs, dirEntry, hasher, total, limit)
		if err != nil {
			return err
		}
		*entries = append(*entries, *entry)

		if dirEntry.IsDir() {
			if err := scanWorkspaceDir(root, childAbs, childRelParts, allPatterns, hasher, total, limit, entries); err != nil {
				return err
			}
		}
	}
	return nil
}

func workspaceImageRootPatterns(root string) []gitignore.Pattern {
	patterns := workspaceImageReadPatterns(filepath.Join(root, ".gitignore"), nil)
	return append(patterns, workspaceImageReadPatterns(filepath.Join(root, ".git", "info", "exclude"), nil)...)
}

func workspaceImageLocalPatterns(dir string, relParts []string) []gitignore.Pattern {
	return workspaceImageReadPatterns(filepath.Join(dir, ".gitignore"), relParts)
}

func workspaceImageReadPatterns(path string, domain []string) []gitignore.Pattern {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

func workspaceImageExtendPatterns(parent, local []gitignore.Pattern) []gitignore.Pattern {
	if len(local) == 0 {
		return parent
	}
	combined := make([]gitignore.Pattern, len(parent)+len(local))
	copy(combined, parent)
	copy(combined[len(parent):], local)
	return combined
}

func scanWorkspaceEntry(root, fullPath string, d fs.DirEntry, hasher io.Writer, total *int64, limit int64) (*workspaceImageEntry, error) {
	info, err := d.Info()
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return nil, err
	}
	entry := &workspaceImageEntry{
		absPath:   filepath.Clean(fullPath),
		relPath:   filepath.ToSlash(rel),
		mode:      info.Mode(),
		size:      info.Size(),
		isDir:     d.IsDir(),
		isSymlink: info.Mode()&fs.ModeSymlink != 0,
	}
	if err := writeWorkspaceSignatureHeader(hasher, entry); err != nil {
		return nil, err
	}
	if entry.isDir {
		return entry, nil
	}
	if entry.isSymlink {
		target, err := os.Readlink(entry.absPath)
		if err != nil {
			return nil, err
		}
		entry.linkTarget = target
		_, err = hasher.Write([]byte(target))
		return entry, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrWorkspaceImageUnsupportedFS
	}
	*total += info.Size()
	if limit > 0 && *total > limit {
		return nil, fmt.Errorf("%w: %s requires %d bytes (limit %d)", ErrWorkspaceImageTooLarge, root, *total, limit)
	}
	return entry, hashWorkspaceFile(entry.absPath, hasher)
}

func writeWorkspaceSignatureHeader(dst io.Writer, entry *workspaceImageEntry) error {
	_, err := fmt.Fprintf(
		dst,
		"%s\x00%d\x00%d\x00%d\x00",
		entry.relPath,
		uint32(entry.mode),
		entry.size,
		entry.mode&fs.ModeType,
	)
	return err
}

func hashWorkspaceFile(fullPath string, dst io.Writer) error {
	f, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}

func importWorkspaceImage(manifest *workspaceImageManifest, limit int64) (*workspaceImage, error) {
	reservation, err := memorybudget.Current().Reserve(memorybudget.ScopeWorkspaceImage, manifest.totalBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %s requires %d bytes (%v)", ErrWorkspaceImageTooLarge, manifest.root, manifest.totalBytes, err)
	}
	store := purevfs.NewChunkStore(limit)
	workspace := purevfs.NewWorkspace(store, nil)
	for _, entry := range manifest.entries {
		if err := importWorkspaceEntry(workspace, entry); err != nil {
			reservation.Release()
			return nil, err
		}
	}
	snapshot, err := workspace.Head("main")
	if err != nil {
		reservation.Release()
		return nil, err
	}
	return &workspaceImage{
		root:       manifest.root,
		signature:  manifest.signature,
		totalBytes: manifest.totalBytes,
		workspace:  workspace,
		snapshot:   snapshot,
		memory:     reservation,
	}, nil
}

func importWorkspaceEntry(workspace *purevfs.Workspace, entry workspaceImageEntry) error {
	vfsPath := workspaceImageVFSPath(entry.relPath)
	if entry.isDir {
		_, err := workspace.Mkdir("main", vfsPath, entry.mode)
		if err == nil || errors.Is(err, purevfs.ErrAlreadyExists) {
			return nil
		}
		return err
	}
	if entry.isSymlink {
		_, err := workspace.Symlink("main", vfsPath, entry.linkTarget, entry.mode)
		return err
	}
	content, err := os.ReadFile(entry.absPath)
	if err != nil {
		return err
	}
	body, err := purevfs.NewChunkedFileBody(content, workspace.ChunkStore(), workspaceImageChunkSize)
	if err != nil {
		return err
	}
	_, err = workspace.WriteFileBody("main", vfsPath, body, entry.mode)
	return err
}

func workspaceImageKey(root, signature string) string {
	return root + "\x00" + signature
}

func workspaceImageVFSPath(rel string) string {
	if strings.TrimSpace(rel) == "" || rel == "." {
		return "/"
	}
	return "/" + strings.TrimPrefix(filepath.ToSlash(rel), "/")
}

type workspaceImageFS struct {
	mu       sync.RWMutex
	handle   *workspaceImageHandle
	snapshot purevfs.SnapshotID
	branch   string
}

func newWorkspaceImageFS(handle *workspaceImageHandle) *workspaceImageFS {
	return &workspaceImageFS{
		handle:   handle,
		snapshot: handle.image.snapshot,
	}
}

func (w *workspaceImageFS) ReadFile(path string) ([]byte, error) {
	resolved, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	content, err := w.handle.image.workspace.ReadFile(w.currentSnapshot(), resolved)
	if err != nil {
		return nil, w.mapError(err)
	}
	return content, nil
}

func (w *workspaceImageFS) Exists(path string) (bool, error) {
	resolved, err := w.resolve(path)
	if err != nil {
		return false, err
	}
	return w.handle.image.workspace.Exists(w.currentSnapshot(), resolved), nil
}

func (w *workspaceImageFS) ListDir(dir string) ([]string, error) {
	resolved, err := w.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := w.handle.image.workspace.ListDir(w.currentSnapshot(), resolved)
	if err != nil {
		return nil, w.mapError(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names, nil
}

func (w *workspaceImageFS) Stat(path string) (fs.FileInfo, error) {
	resolved, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := w.handle.image.workspace.Stat(w.currentSnapshot(), resolved)
	if err != nil {
		return nil, w.mapError(err)
	}
	return snapshotFileInfo{
		name:    info.Name,
		size:    info.Size,
		mode:    info.Mode,
		modTime: info.ModTime,
	}, nil
}

func (w *workspaceImageFS) resolve(path string) (string, error) {
	root := w.handle.image.root
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(root, cleaned)
	}
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrWorkspaceImagePathOutside
	}
	return workspaceImageVFSPath(rel), nil
}

func (w *workspaceImageFS) mapError(err error) error {
	if errors.Is(err, purevfs.ErrPathNotFound) {
		return ErrFileNotFound
	}
	return err
}

func (w *workspaceImageFS) ApplyModifications(mods []FileModification) error {
	if len(mods) == 0 {
		return nil
	}
	if err := w.ensureBranch(); err != nil {
		return err
	}
	for _, mod := range mods {
		if err := w.applyModification(mod); err != nil {
			return err
		}
	}
	return w.refreshSnapshot()
}

func (w *workspaceImageFS) currentSnapshot() purevfs.SnapshotID {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot
}

func (w *workspaceImageFS) ensureBranch() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.branch != "" {
		return nil
	}
	branch := workspaceImageBranchName()
	if err := w.handle.image.workspace.CreateBranch(branch, w.snapshot); err != nil {
		return err
	}
	w.branch = branch
	return nil
}

func (w *workspaceImageFS) applyModification(mod FileModification) error {
	resolved, err := w.resolve(mod.OriginalPath)
	if err != nil {
		return err
	}
	switch mod.Operation {
	case FileOpDelete:
		return w.deleteFile(resolved)
	case FileOpMkdir:
		return w.mkdir(resolved)
	default:
		return w.writeFile(resolved, mod.NewContent)
	}
}

func (w *workspaceImageFS) deleteFile(path string) error {
	_, err := w.handle.image.workspace.Delete(w.branchName(), path)
	if errors.Is(err, purevfs.ErrPathNotFound) {
		return nil
	}
	return err
}

func (w *workspaceImageFS) mkdir(path string) error {
	_, err := w.handle.image.workspace.Mkdir(w.branchName(), path, fs.ModeDir|0o755)
	if errors.Is(err, purevfs.ErrAlreadyExists) {
		return nil
	}
	return err
}

func (w *workspaceImageFS) writeFile(path string, content []byte) error {
	if err := w.ensureParentDirs(path); err != nil {
		return err
	}
	body, err := purevfs.NewChunkedFileBody(content, w.handle.image.workspace.ChunkStore(), workspaceImageChunkSize)
	if err != nil {
		return err
	}
	_, err = w.handle.image.workspace.WriteFileBody(w.branchName(), path, body, w.fileMode(path))
	return err
}

func (w *workspaceImageFS) ensureParentDirs(filePath string) error {
	parent := path.Dir(filePath)
	if parent == "/" {
		return nil
	}
	current := ""
	for _, segment := range strings.Split(strings.TrimPrefix(parent, "/"), "/") {
		current = "/" + path.Join(strings.TrimPrefix(current, "/"), segment)
		if err := w.mkdir(current); err != nil {
			return err
		}
	}
	return nil
}

func (w *workspaceImageFS) fileMode(path string) fs.FileMode {
	info, err := w.handle.image.workspace.Stat(w.currentSnapshot(), path)
	if err != nil {
		return 0o644
	}
	return info.Mode
}

func (w *workspaceImageFS) refreshSnapshot() error {
	snapshot, err := w.handle.image.workspace.Head(w.branchName())
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.snapshot = snapshot
	w.mu.Unlock()
	return nil
}

func (w *workspaceImageFS) branchName() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.branch
}

func workspaceImageBranchName() string {
	id := atomic.AddUint64(&workspaceImageBranchCounter, 1)
	return fmt.Sprintf("session-%d", id)
}
