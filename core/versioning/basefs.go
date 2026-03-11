package versioning

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type vfsBaseFS interface {
	ReadFile(path string) ([]byte, error)
	Exists(path string) (bool, error)
	ListDir(dir string) ([]string, error)
	Stat(path string) (fs.FileInfo, error)
}

type vfsMutableBaseFS interface {
	vfsBaseFS
	ApplyModifications(mods []FileModification) error
}

type pipelineOverlayBaseFS struct {
	vfs *PipelineVFS
}

func (b pipelineOverlayBaseFS) ReadFile(path string) ([]byte, error) {
	if b.vfs == nil {
		return nil, ErrFileNotFound
	}
	return b.vfs.Read(context.Background(), path)
}

func (b pipelineOverlayBaseFS) Exists(path string) (bool, error) {
	if b.vfs == nil {
		return false, nil
	}
	return b.vfs.Exists(context.Background(), path)
}

func (b pipelineOverlayBaseFS) ListDir(dir string) ([]string, error) {
	if b.vfs == nil {
		return nil, ErrFileNotFound
	}
	return b.vfs.List(context.Background(), dir)
}

func (b pipelineOverlayBaseFS) Stat(path string) (fs.FileInfo, error) {
	if b.vfs == nil {
		return nil, ErrFileNotFound
	}
	return b.vfs.Stat(context.Background(), path)
}

type memorySnapshotFS struct {
	mu       sync.RWMutex
	root     string
	files    map[string]*snapshotFile
	dirs     map[string]*snapshotDir
	children map[string]map[string]struct{}
}

type snapshotFile struct {
	content []byte
	mode    fs.FileMode
	modTime time.Time
}

type snapshotDir struct {
	mode    fs.FileMode
	modTime time.Time
}

func loadMemorySnapshotFS(root string) (*memorySnapshotFS, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	snapshot := &memorySnapshotFS{
		root:     filepath.Clean(absRoot),
		files:    make(map[string]*snapshotFile),
		dirs:     make(map[string]*snapshotDir),
		children: make(map[string]map[string]struct{}),
	}

	rootInfo, err := os.Stat(snapshot.root)
	if err != nil {
		return nil, err
	}
	snapshot.ensureDirLocked(snapshot.root, rootInfo.Mode(), rootInfo.ModTime())

	walkErr := filepath.WalkDir(snapshot.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == snapshot.root {
			return nil
		}

		cleaned := filepath.Clean(path)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}

		if d.IsDir() {
			snapshot.ensureDirLocked(cleaned, info.Mode(), info.ModTime())
			return nil
		}

		content, readErr := os.ReadFile(cleaned)
		if readErr != nil {
			return readErr
		}
		snapshot.ensureParentDirsLocked(cleaned)
		snapshot.files[cleaned] = &snapshotFile{
			content: cloneBytes(content),
			mode:    info.Mode(),
			modTime: info.ModTime(),
		}
		snapshot.addChildLocked(filepath.Dir(cleaned), filepath.Base(cleaned))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return snapshot, nil
}

func (s *memorySnapshotFS) ReadFile(path string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resolved := s.resolve(path)
	file, ok := s.files[resolved]
	if !ok {
		return nil, ErrFileNotFound
	}
	return cloneBytes(file.content), nil
}

func (s *memorySnapshotFS) Exists(path string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resolved := s.resolve(path)
	if _, ok := s.files[resolved]; ok {
		return true, nil
	}
	if _, ok := s.dirs[resolved]; ok {
		return true, nil
	}
	return false, nil
}

func (s *memorySnapshotFS) ListDir(dir string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resolved := s.resolve(dir)
	if _, ok := s.dirs[resolved]; !ok {
		return nil, ErrFileNotFound
	}
	children := s.children[resolved]
	if len(children) == 0 {
		return []string{}, nil
	}
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (s *memorySnapshotFS) Stat(path string) (fs.FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resolved := s.resolve(path)
	if file, ok := s.files[resolved]; ok {
		return snapshotFileInfo{
			name:    filepath.Base(resolved),
			size:    int64(len(file.content)),
			mode:    file.mode,
			modTime: file.modTime,
		}, nil
	}
	if dir, ok := s.dirs[resolved]; ok {
		return snapshotFileInfo{
			name:    filepath.Base(resolved),
			size:    0,
			mode:    dir.mode,
			modTime: dir.modTime,
		}, nil
	}
	return nil, ErrFileNotFound
}

func (s *memorySnapshotFS) ApplyModifications(mods []FileModification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, mod := range mods {
		resolved := s.resolve(mod.OriginalPath)
		switch mod.Operation {
		case FileOpMkdir:
			s.ensureDirLocked(resolved, fs.ModeDir|0o755, mod.Timestamp)
		case FileOpCreate, FileOpModify:
			mode := fs.FileMode(0o644)
			modTime := mod.Timestamp
			if existing, ok := s.files[resolved]; ok {
				mode = existing.mode
				if modTime.IsZero() {
					modTime = existing.modTime
				}
			}
			s.ensureParentDirsLocked(resolved)
			s.files[resolved] = &snapshotFile{
				content: cloneBytes(mod.NewContent),
				mode:    mode,
				modTime: modTime,
			}
			s.addChildLocked(filepath.Dir(resolved), filepath.Base(resolved))
		case FileOpDelete:
			delete(s.files, resolved)
			s.removeChildLocked(filepath.Dir(resolved), filepath.Base(resolved))
		}
	}
	return nil
}

func (s *memorySnapshotFS) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(s.root, path)
}

func (s *memorySnapshotFS) ensureParentDirsLocked(path string) {
	parent := filepath.Dir(path)
	for current := parent; current != "" && isUnderPath(current, s.root); current = filepath.Dir(current) {
		s.ensureDirLocked(current, fs.ModeDir|0o755, time.Time{})
		if current == s.root {
			break
		}
	}
}

func (s *memorySnapshotFS) ensureDirLocked(path string, mode fs.FileMode, modTime time.Time) {
	path = filepath.Clean(path)
	if !pathExistsWithinRoot(path, s.root) {
		return
	}
	if modTime.IsZero() {
		modTime = time.Now().UTC()
	}
	if mode == 0 {
		mode = fs.ModeDir | 0o755
	}
	mode |= fs.ModeDir
	if _, ok := s.dirs[path]; !ok {
		s.dirs[path] = &snapshotDir{mode: mode, modTime: modTime}
	}
	if path != s.root {
		s.ensureParentDirsLocked(filepath.Dir(path))
		s.addChildLocked(filepath.Dir(path), filepath.Base(path))
	}
	if _, ok := s.children[path]; !ok {
		s.children[path] = make(map[string]struct{})
	}
}

func (s *memorySnapshotFS) addChildLocked(parent, name string) {
	parent = filepath.Clean(parent)
	if _, ok := s.children[parent]; !ok {
		s.children[parent] = make(map[string]struct{})
	}
	s.children[parent][name] = struct{}{}
}

func (s *memorySnapshotFS) removeChildLocked(parent, name string) {
	parent = filepath.Clean(parent)
	children := s.children[parent]
	if len(children) == 0 {
		return
	}
	delete(children, name)
	if len(children) == 0 {
		delete(s.children, parent)
	}
}

func pathExistsWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

type snapshotFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (i snapshotFileInfo) Name() string       { return i.name }
func (i snapshotFileInfo) Size() int64        { return i.size }
func (i snapshotFileInfo) Mode() fs.FileMode  { return i.mode }
func (i snapshotFileInfo) ModTime() time.Time { return i.modTime }
func (i snapshotFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i snapshotFileInfo) Sys() any           { return nil }

type overlayDirEntry struct {
	name string
	info fs.FileInfo
}

func (e overlayDirEntry) Name() string { return e.name }
func (e overlayDirEntry) IsDir() bool  { return e.info != nil && e.info.IsDir() }
func (e overlayDirEntry) Type() fs.FileMode {
	if e.info == nil {
		return 0
	}
	return e.info.Mode().Type()
}
func (e overlayDirEntry) Info() (fs.FileInfo, error) {
	if e.info == nil {
		return nil, errors.New("missing file info")
	}
	return e.info, nil
}
