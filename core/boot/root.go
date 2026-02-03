// Package boot provides the boot pipeline for initializing and updating the Knowledge Graph.
package boot

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

var (
	// ErrNotGitRepo indicates the directory is not within a git repository.
	ErrNotGitRepo = errors.New("not a git repository")
)

// FindGitRoot walks up from startDir looking for a .git directory.
// Returns the directory containing .git, or ErrNotGitRepo if not found.
func FindGitRoot(startDir string) (string, error) {
	absDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	dir := absDir
	for {
		gitDir := filepath.Join(dir, ".git")
		info, err := os.Stat(gitDir)
		if err == nil && info.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotGitRepo
		}
		dir = parent
	}
}

// SylkDir returns the .sylk directory path for a project root.
func SylkDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".sylk")
}

// SylkExists checks if the .sylk directory exists.
func SylkExists(projectRoot string) bool {
	info, err := os.Stat(SylkDir(projectRoot))
	return err == nil && info.IsDir()
}

// MetaPath returns the path to .sylk/boot_meta.json.
// Separate from GlobalMeta's meta.json to avoid path conflicts.
func MetaPath(projectRoot string) string {
	return filepath.Join(SylkDir(projectRoot), "boot_meta.json")
}

// MetaExists checks if .sylk/meta.json exists.
func MetaExists(projectRoot string) bool {
	info, err := os.Stat(MetaPath(projectRoot))
	return err == nil && !info.IsDir()
}

// KnowledgeDir returns the path to .sylk/knowledge/.
func KnowledgeDir(projectRoot string) string {
	return filepath.Join(SylkDir(projectRoot), "knowledge")
}

// SessionsDir returns the path to .sylk/sessions/.
func SessionsDir(projectRoot string) string {
	return filepath.Join(SylkDir(projectRoot), "sessions")
}

// EnsureSylkDirs creates the .sylk directory structure using sylkdir.SylkDir.Init().
// This creates the full versioned structure including global versions at .sylk/versions/.
func EnsureSylkDirs(projectRoot string) error {
	sd := sylkdir.New(projectRoot)
	return sd.Init()
}
