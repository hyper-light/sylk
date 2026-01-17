package security

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrStagingNotInitialized = errors.New("staging directory not initialized")
	ErrMergeConflict         = errors.New("file modified in both staging and original")
)

// StagedFile tracks a file that was copied to staging for modification.
type StagedFile struct {
	OriginalPath string
	StagedPath   string
	OriginalHash string
	IsNew        bool
	IsDeleted    bool
}

type VirtualFilesystem struct {
	mu            sync.RWMutex
	workingDir    string
	stagingDir    string
	allowedPaths  map[string]bool
	approvedPaths map[string]bool
	stagedFiles   map[string]*StagedFile
	stagingActive bool
}

func NewVirtualFilesystem(workingDir string, allowedPaths []string) *VirtualFilesystem {
	resolvedWorkDir := workingDir
	if workingDir != "" {
		if resolved, err := filepath.EvalSymlinks(workingDir); err == nil {
			resolvedWorkDir = resolved
		}
	}

	vfs := &VirtualFilesystem{
		workingDir:    resolvedWorkDir,
		allowedPaths:  make(map[string]bool),
		approvedPaths: make(map[string]bool),
		stagedFiles:   make(map[string]*StagedFile),
	}

	for _, p := range allowedPaths {
		resolved := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			resolved = r
		}
		vfs.allowedPaths[resolved] = true
	}

	if resolvedWorkDir != "" {
		vfs.allowedPaths[resolvedWorkDir] = true
	}

	return vfs
}

func (v *VirtualFilesystem) PrepareForCommand(cmd *exec.Cmd) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if cmd.Dir == "" {
		cmd.Dir = v.workingDir
	}

	if err := v.initStaging(); err != nil {
		return err
	}

	return nil
}

func (v *VirtualFilesystem) initStaging() error {
	if v.stagingActive {
		return nil
	}

	stagingDir, err := os.MkdirTemp("", "sylk-vfs-staging-*")
	if err != nil {
		return err
	}

	v.stagingDir = stagingDir
	v.stagingActive = true
	v.stagedFiles = make(map[string]*StagedFile)

	return nil
}

func (v *VirtualFilesystem) GetStagingDir() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.stagingDir
}

func (v *VirtualFilesystem) IsStagingActive() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.stagingActive
}

func (v *VirtualFilesystem) ValidatePath(path string) error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	absPath, err := v.resolvePath(path)
	if err != nil {
		return err
	}

	if v.isAllowed(absPath) {
		return nil
	}

	return ErrPathOutsideSandbox
}

func (v *VirtualFilesystem) resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.workingDir, path)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			resolved = v.resolveNonExistentPath(path)
		} else {
			return "", err
		}
	}

	return resolved, nil
}

func (v *VirtualFilesystem) resolveNonExistentPath(path string) string {
	path = filepath.Clean(path)
	parts := strings.Split(path, string(filepath.Separator))

	for i := len(parts); i > 0; i-- {
		candidate := string(filepath.Separator) + filepath.Join(parts[1:i]...)
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
			remaining := filepath.Join(parts[i:]...)
			return filepath.Join(resolved, remaining)
		}
	}

	return path
}

func (v *VirtualFilesystem) isAllowed(absPath string) bool {
	if v.allowedPaths[absPath] {
		return true
	}

	if v.approvedPaths[absPath] {
		return true
	}

	for allowed := range v.allowedPaths {
		resolvedAllowed := v.resolveForComparison(allowed)
		if v.isUnderPath(absPath, resolvedAllowed) {
			return true
		}
	}

	for approved := range v.approvedPaths {
		resolvedApproved := v.resolveForComparison(approved)
		if v.isUnderPath(absPath, resolvedApproved) {
			return true
		}
	}

	return false
}

func (v *VirtualFilesystem) resolveForComparison(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func (v *VirtualFilesystem) isUnderPath(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

func (v *VirtualFilesystem) AddAllowedPath(path string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	v.allowedPaths[absPath] = true
}

func (v *VirtualFilesystem) ApprovePath(path string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	v.approvedPaths[absPath] = true
}

func (v *VirtualFilesystem) RemoveApprovedPath(path string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	delete(v.approvedPaths, absPath)
}

func (v *VirtualFilesystem) ListAllowedPaths() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	paths := make([]string, 0, len(v.allowedPaths))
	for p := range v.allowedPaths {
		paths = append(paths, p)
	}
	return paths
}

func (v *VirtualFilesystem) ListApprovedPaths() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	paths := make([]string, 0, len(v.approvedPaths))
	for p := range v.approvedPaths {
		paths = append(paths, p)
	}
	return paths
}

func (v *VirtualFilesystem) SetWorkingDir(dir string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.workingDir = dir
	v.allowedPaths[dir] = true
}

func (v *VirtualFilesystem) GetWorkingDir() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.workingDir
}

func (v *VirtualFilesystem) CopyOnWrite(originalPath string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.stagingActive {
		if err := v.initStaging(); err != nil {
			return "", err
		}
	}

	absPath, err := v.resolvePath(originalPath)
	if err != nil {
		return "", err
	}

	if staged, exists := v.stagedFiles[absPath]; exists {
		return staged.StagedPath, nil
	}

	return v.createStagedCopy(absPath)
}

func (v *VirtualFilesystem) createStagedCopy(absPath string) (string, error) {
	relPath, err := filepath.Rel(v.workingDir, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		relPath = filepath.Base(absPath)
	}

	stagedPath := filepath.Join(v.stagingDir, relPath)

	cleanStaged := filepath.Clean(stagedPath)
	if !strings.HasPrefix(cleanStaged, v.stagingDir) {
		stagedPath = filepath.Join(v.stagingDir, filepath.Base(absPath))
	}

	if err := os.MkdirAll(filepath.Dir(stagedPath), 0755); err != nil {
		return "", err
	}

	staged := &StagedFile{
		OriginalPath: absPath,
		StagedPath:   stagedPath,
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		staged.IsNew = true
	} else {
		hash, err := v.hashFile(absPath)
		if err != nil {
			return "", err
		}
		staged.OriginalHash = hash

		if err := v.copyFile(absPath, stagedPath); err != nil {
			return "", err
		}
	}

	v.stagedFiles[absPath] = staged
	return stagedPath, nil
}

func (v *VirtualFilesystem) StageNewFile(originalPath string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.stagingActive {
		if err := v.initStaging(); err != nil {
			return "", err
		}
	}

	absPath, err := v.resolvePath(originalPath)
	if err != nil {
		return "", err
	}

	if staged, exists := v.stagedFiles[absPath]; exists {
		return staged.StagedPath, nil
	}

	relPath, err := filepath.Rel(v.workingDir, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		relPath = filepath.Base(absPath)
	}

	stagedPath := filepath.Join(v.stagingDir, relPath)

	cleanStaged := filepath.Clean(stagedPath)
	if !strings.HasPrefix(cleanStaged, v.stagingDir) {
		stagedPath = filepath.Join(v.stagingDir, filepath.Base(absPath))
	}

	if err := os.MkdirAll(filepath.Dir(stagedPath), 0755); err != nil {
		return "", err
	}

	v.stagedFiles[absPath] = &StagedFile{
		OriginalPath: absPath,
		StagedPath:   stagedPath,
		IsNew:        true,
	}

	return stagedPath, nil
}

func (v *VirtualFilesystem) StageDelete(originalPath string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.stagingActive {
		if err := v.initStaging(); err != nil {
			return err
		}
	}

	absPath, err := v.resolvePath(originalPath)
	if err != nil {
		return err
	}

	hash, err := v.hashFile(absPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	v.stagedFiles[absPath] = &StagedFile{
		OriginalPath: absPath,
		OriginalHash: hash,
		IsDeleted:    true,
	}

	return nil
}

func (v *VirtualFilesystem) MergeChanges() (*MergeResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.stagingActive {
		return &MergeResult{}, nil
	}

	result := &MergeResult{
		Merged:    make([]string, 0),
		Conflicts: make([]string, 0),
		Created:   make([]string, 0),
		Deleted:   make([]string, 0),
	}

	for origPath, staged := range v.stagedFiles {
		if err := v.mergeFile(staged, result); err != nil {
			result.Conflicts = append(result.Conflicts, origPath)
		}
	}

	return result, v.cleanupStaging()
}

func (v *VirtualFilesystem) mergeFile(staged *StagedFile, result *MergeResult) error {
	if staged.IsDeleted {
		return v.mergeDelete(staged, result)
	}
	if staged.IsNew {
		return v.mergeNew(staged, result)
	}
	return v.mergeModified(staged, result)
}

func (v *VirtualFilesystem) mergeDelete(staged *StagedFile, result *MergeResult) error {
	if staged.OriginalHash != "" {
		currentHash, err := v.hashFile(staged.OriginalPath)
		if err == nil && currentHash != staged.OriginalHash {
			return ErrMergeConflict
		}
	}

	if err := os.Remove(staged.OriginalPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	result.Deleted = append(result.Deleted, staged.OriginalPath)
	return nil
}

func (v *VirtualFilesystem) mergeNew(staged *StagedFile, result *MergeResult) error {
	if _, err := os.Stat(staged.OriginalPath); err == nil {
		return ErrMergeConflict
	}

	if err := os.MkdirAll(filepath.Dir(staged.OriginalPath), 0755); err != nil {
		return err
	}

	if err := v.copyFile(staged.StagedPath, staged.OriginalPath); err != nil {
		return err
	}

	result.Created = append(result.Created, staged.OriginalPath)
	return nil
}

func (v *VirtualFilesystem) mergeModified(staged *StagedFile, result *MergeResult) error {
	currentHash, err := v.hashFile(staged.OriginalPath)
	if err != nil {
		return err
	}

	if currentHash != staged.OriginalHash {
		return ErrMergeConflict
	}

	if err := v.copyFile(staged.StagedPath, staged.OriginalPath); err != nil {
		return err
	}

	result.Merged = append(result.Merged, staged.OriginalPath)
	return nil
}

func (v *VirtualFilesystem) cleanupStaging() error {
	if v.stagingDir != "" {
		os.RemoveAll(v.stagingDir)
	}
	v.stagingDir = ""
	v.stagingActive = false
	v.stagedFiles = make(map[string]*StagedFile)
	return nil
}

func (v *VirtualFilesystem) DiscardChanges() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cleanupStaging()
}

func (v *VirtualFilesystem) ListStagedFiles() []StagedFile {
	v.mu.RLock()
	defer v.mu.RUnlock()

	files := make([]StagedFile, 0, len(v.stagedFiles))
	for _, sf := range v.stagedFiles {
		files = append(files, *sf)
	}
	return files
}

type MergeResult struct {
	Merged    []string
	Conflicts []string
	Created   []string
	Deleted   []string
}

func (r *MergeResult) HasConflicts() bool {
	return len(r.Conflicts) > 0
}

func (r *MergeResult) TotalChanges() int {
	return len(r.Merged) + len(r.Created) + len(r.Deleted)
}

func (v *VirtualFilesystem) hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func (v *VirtualFilesystem) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
