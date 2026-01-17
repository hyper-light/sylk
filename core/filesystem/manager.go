package filesystem

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrPathTraversal     = errors.New("path traversal detected")
	ErrSymlinkNotAllowed = errors.New("symlink target outside boundary")
	ErrOutsideBoundary   = errors.New("path outside allowed boundary")
	ErrOperationDenied   = errors.New("operation denied by policy")
)

type OperationType string

const (
	OpRead   OperationType = "read"
	OpWrite  OperationType = "write"
	OpDelete OperationType = "delete"
	OpCreate OperationType = "create"
	OpList   OperationType = "list"
)

type AuditEntry struct {
	Timestamp    time.Time
	PipelineID   string
	Operation    OperationType
	Path         string
	ResolvedPath string
	Success      bool
	Error        string
}

type AuditLogger interface {
	Log(entry AuditEntry)
}

type NoOpAuditLogger struct{}

func (n *NoOpAuditLogger) Log(_ AuditEntry) {}

type FilesystemConfig struct {
	AllowedRoots  []string
	AllowSymlinks bool
	AllowHidden   bool
	MaxFileSize   int64
	AuditLogger   AuditLogger
}

func DefaultFilesystemConfig(roots ...string) FilesystemConfig {
	return FilesystemConfig{
		AllowedRoots:  roots,
		AllowSymlinks: false,
		AllowHidden:   false,
		MaxFileSize:   100 * 1024 * 1024,
		AuditLogger:   &NoOpAuditLogger{},
	}
}

type FilesystemManager struct {
	mu            sync.RWMutex
	config        FilesystemConfig
	resolvedRoots []string
}

func NewFilesystemManager(config FilesystemConfig) (*FilesystemManager, error) {
	resolved, err := resolveRoots(config.AllowedRoots)
	if err != nil {
		return nil, err
	}

	return &FilesystemManager{
		config:        config,
		resolvedRoots: resolved,
	}, nil
}

func resolveRoots(roots []string) ([]string, error) {
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		clean := filepath.Clean(abs)
		resolved = append(resolved, clean)
	}
	return resolved, nil
}

func (fm *FilesystemManager) ValidatePath(pipelineID, path string) (string, error) {
	resolved, err := fm.resolvePath(path)
	if err != nil {
		fm.audit(pipelineID, OpRead, path, "", false, err.Error())
		return "", err
	}

	if err := fm.checkBoundary(resolved); err != nil {
		fm.audit(pipelineID, OpRead, path, resolved, false, err.Error())
		return "", err
	}

	if err := fm.checkSymlink(resolved); err != nil {
		fm.audit(pipelineID, OpRead, path, resolved, false, err.Error())
		return "", err
	}

	if err := fm.checkHidden(resolved); err != nil {
		fm.audit(pipelineID, OpRead, path, resolved, false, err.Error())
		return "", err
	}

	return resolved, nil
}

func (fm *FilesystemManager) resolvePath(path string) (string, error) {
	if containsTraversal(path) {
		return "", ErrPathTraversal
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	return filepath.Clean(abs), nil
}

func containsTraversal(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if part == ".." {
			return true
		}
	}
	return false
}

func (fm *FilesystemManager) checkBoundary(resolved string) error {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	for _, root := range fm.resolvedRoots {
		if isWithinRoot(resolved, root) {
			return nil
		}
	}
	return ErrOutsideBoundary
}

func isWithinRoot(path, root string) bool {
	return strings.HasPrefix(path, root+string(filepath.Separator)) || path == root
}

func (fm *FilesystemManager) checkSymlink(path string) error {
	if fm.config.AllowSymlinks {
		return nil
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fm.validateSymlinkTarget(path)
	}

	return nil
}

func (fm *FilesystemManager) validateSymlinkTarget(path string) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}

	if err := fm.checkBoundary(target); err != nil {
		return ErrSymlinkNotAllowed
	}

	return nil
}

func (fm *FilesystemManager) checkHidden(path string) error {
	if fm.config.AllowHidden {
		return nil
	}

	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return ErrOperationDenied
	}

	return nil
}

func (fm *FilesystemManager) Read(pipelineID, path string) ([]byte, error) {
	resolved, err := fm.ValidatePath(pipelineID, path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		fm.audit(pipelineID, OpRead, path, resolved, false, err.Error())
		return nil, err
	}

	fm.audit(pipelineID, OpRead, path, resolved, true, "")
	return data, nil
}

func (fm *FilesystemManager) Write(pipelineID, path string, data []byte) error {
	if int64(len(data)) > fm.config.MaxFileSize {
		fm.audit(pipelineID, OpWrite, path, "", false, "file size exceeds limit")
		return ErrOperationDenied
	}

	resolved, err := fm.ValidatePath(pipelineID, path)
	if err != nil {
		return err
	}

	if err := os.WriteFile(resolved, data, 0644); err != nil {
		fm.audit(pipelineID, OpWrite, path, resolved, false, err.Error())
		return err
	}

	fm.audit(pipelineID, OpWrite, path, resolved, true, "")
	return nil
}

func (fm *FilesystemManager) Delete(pipelineID, path string) error {
	resolved, err := fm.ValidatePath(pipelineID, path)
	if err != nil {
		return err
	}

	if err := os.Remove(resolved); err != nil {
		fm.audit(pipelineID, OpDelete, path, resolved, false, err.Error())
		return err
	}

	fm.audit(pipelineID, OpDelete, path, resolved, true, "")
	return nil
}

func (fm *FilesystemManager) CreateDir(pipelineID, path string) error {
	resolved, err := fm.resolvePath(path)
	if err != nil {
		fm.audit(pipelineID, OpCreate, path, "", false, err.Error())
		return err
	}

	if err := fm.checkBoundary(resolved); err != nil {
		fm.audit(pipelineID, OpCreate, path, resolved, false, err.Error())
		return err
	}

	if err := os.MkdirAll(resolved, 0755); err != nil {
		fm.audit(pipelineID, OpCreate, path, resolved, false, err.Error())
		return err
	}

	fm.audit(pipelineID, OpCreate, path, resolved, true, "")
	return nil
}

func (fm *FilesystemManager) List(pipelineID, path string) ([]FileInfo, error) {
	resolved, err := fm.ValidatePath(pipelineID, path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		fm.audit(pipelineID, OpList, path, resolved, false, err.Error())
		return nil, err
	}

	infos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entryToFileInfo(entry, resolved)
		if err != nil {
			continue
		}

		if !fm.config.AllowHidden && strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		infos = append(infos, info)
	}

	fm.audit(pipelineID, OpList, path, resolved, true, "")
	return infos, nil
}

func entryToFileInfo(entry os.DirEntry, parent string) (FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		Name:    entry.Name(),
		Path:    filepath.Join(parent, entry.Name()),
		Size:    info.Size(),
		IsDir:   entry.IsDir(),
		ModTime: info.ModTime(),
	}, nil
}

func (fm *FilesystemManager) Copy(pipelineID, src, dst string) error {
	srcResolved, err := fm.ValidatePath(pipelineID, src)
	if err != nil {
		return err
	}

	dstResolved, err := fm.resolvePath(dst)
	if err != nil {
		fm.audit(pipelineID, OpCreate, dst, "", false, err.Error())
		return err
	}

	if err := fm.checkBoundary(dstResolved); err != nil {
		fm.audit(pipelineID, OpCreate, dst, dstResolved, false, err.Error())
		return err
	}

	if err := copyFile(srcResolved, dstResolved); err != nil {
		fm.audit(pipelineID, OpCreate, dst, dstResolved, false, err.Error())
		return err
	}

	fm.audit(pipelineID, OpCreate, dst, dstResolved, true, "")
	return nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (fm *FilesystemManager) Exists(pipelineID, path string) (bool, error) {
	resolved, err := fm.ValidatePath(pipelineID, path)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(resolved)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (fm *FilesystemManager) audit(pipelineID string, op OperationType, path, resolved string, success bool, errMsg string) {
	fm.mu.RLock()
	logger := fm.config.AuditLogger
	fm.mu.RUnlock()

	if logger == nil {
		return
	}

	logger.Log(AuditEntry{
		Timestamp:    time.Now(),
		PipelineID:   pipelineID,
		Operation:    op,
		Path:         path,
		ResolvedPath: resolved,
		Success:      success,
		Error:        errMsg,
	})
}

func (fm *FilesystemManager) AddRoot(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	fm.mu.Lock()
	defer fm.mu.Unlock()

	fm.resolvedRoots = append(fm.resolvedRoots, filepath.Clean(abs))
	fm.config.AllowedRoots = append(fm.config.AllowedRoots, root)
	return nil
}

func (fm *FilesystemManager) Roots() []string {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	roots := make([]string, len(fm.resolvedRoots))
	copy(roots, fm.resolvedRoots)
	return roots
}

type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}
