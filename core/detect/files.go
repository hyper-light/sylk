// Package detect provides utilities for detecting files, binaries, and dependencies
// in project directories. These utilities support the Librarian's tool discovery system.
package detect

import (
	"errors"
	"os"
	"path/filepath"
)

var (
	ErrInvalidPath      = errors.New("invalid path provided")
	ErrFileNotFound     = errors.New("file not found")
	ErrNoFilesSpecified = errors.New("no files specified")
)

// FileExists checks if any of the specified files exist under the root directory.
func FileExists(root string, files ...string) bool {
	if root == "" || len(files) == 0 {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	return anyFileExists(absRoot, files)
}

func anyFileExists(absRoot string, files []string) bool {
	for _, file := range files {
		if fileExistsAt(absRoot, file) {
			return true
		}
	}
	return false
}

func fileExistsAt(absRoot, file string) bool {
	fullPath := resolvePath(absRoot, file)
	info, err := os.Stat(fullPath)
	return err == nil && !info.IsDir()
}

func resolvePath(absRoot, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(absRoot, file)
}

// FindUp searches upward from startDir for a file with the given filename.
func FindUp(startDir string, filename string) (string, error) {
	if startDir == "" {
		return "", ErrInvalidPath
	}
	if filename == "" {
		return "", ErrNoFilesSpecified
	}

	currentDir, err := resolveStartDir(startDir)
	if err != nil {
		return "", err
	}

	return searchUpward(currentDir, []string{filename})
}

// FindUpAny searches upward from startDir for any of the specified filenames.
func FindUpAny(startDir string, filenames ...string) (string, string, error) {
	if err := validateFindUpAnyArgs(startDir, filenames); err != nil {
		return "", "", err
	}

	currentDir, err := resolveStartDir(startDir)
	if err != nil {
		return "", "", err
	}

	return findUpAnyFromDir(currentDir, filenames)
}

func validateFindUpAnyArgs(startDir string, filenames []string) error {
	if startDir == "" {
		return ErrInvalidPath
	}
	if len(filenames) == 0 {
		return ErrNoFilesSpecified
	}
	return nil
}

func findUpAnyFromDir(currentDir string, filenames []string) (string, string, error) {
	path, err := searchUpward(currentDir, filenames)
	if err != nil {
		return "", "", err
	}
	return path, filepath.Base(path), nil
}

func resolveStartDir(startDir string) (string, error) {
	absDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", ErrInvalidPath
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return "", ErrInvalidPath
	}
	if !info.IsDir() {
		absDir = filepath.Dir(absDir)
	}
	return absDir, nil
}

func searchUpward(currentDir string, filenames []string) (string, error) {
	for {
		if path := findFileInDir(currentDir, filenames); path != "" {
			return path, nil
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", ErrFileNotFound
		}
		currentDir = parentDir
	}
}

func findFileInDir(dir string, filenames []string) string {
	for _, filename := range filenames {
		candidate := filepath.Join(dir, filename)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
