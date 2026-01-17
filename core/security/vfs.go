package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type VirtualFilesystem struct {
	mu            sync.RWMutex
	workingDir    string
	stagingDir    string
	allowedPaths  map[string]bool
	approvedPaths map[string]bool
}

func NewVirtualFilesystem(workingDir string, allowedPaths []string) *VirtualFilesystem {
	vfs := &VirtualFilesystem{
		workingDir:    workingDir,
		allowedPaths:  make(map[string]bool),
		approvedPaths: make(map[string]bool),
	}

	for _, p := range allowedPaths {
		vfs.allowedPaths[p] = true
	}

	if workingDir != "" {
		vfs.allowedPaths[workingDir] = true
	}

	return vfs
}

func (v *VirtualFilesystem) PrepareForCommand(cmd *exec.Cmd) error {
	if cmd.Dir == "" {
		cmd.Dir = v.workingDir
	}

	return nil
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
			resolved = filepath.Clean(path)
		} else {
			return "", err
		}
	}

	return resolved, nil
}

func (v *VirtualFilesystem) isAllowed(absPath string) bool {
	if v.allowedPaths[absPath] {
		return true
	}

	if v.approvedPaths[absPath] {
		return true
	}

	for allowed := range v.allowedPaths {
		if v.isUnderPath(absPath, allowed) {
			return true
		}
	}

	for approved := range v.approvedPaths {
		if v.isUnderPath(absPath, approved) {
			return true
		}
	}

	return false
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
