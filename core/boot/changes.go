package boot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ChangeType uint8

const (
	ChangeAdded ChangeType = iota
	ChangeModified
	ChangeDeleted
)

type FileChange struct {
	Path string
	Type ChangeType
	Hash string
}

type ChangeDetector struct {
	projectRoot string
	meta        *Meta
	isGitRepo   bool
}

func NewChangeDetector(projectRoot string, meta *Meta, isGitRepo bool) *ChangeDetector {
	return &ChangeDetector{
		projectRoot: projectRoot,
		meta:        meta,
		isGitRepo:   isGitRepo,
	}
}

func (d *ChangeDetector) DetectChanges(ctx context.Context, currentFiles []string) ([]FileChange, error) {
	if d.isGitRepo && d.meta.LastIndexedCommit != "" {
		return d.detectGitChanges(ctx, currentFiles)
	}
	return d.detectHashChanges(currentFiles)
}

func (d *ChangeDetector) detectGitChanges(ctx context.Context, currentFiles []string) ([]FileChange, error) {
	var changes []FileChange
	changedSet := make(map[string]bool)

	committed, err := d.gitDiffCommitted(ctx)
	if err == nil {
		for _, path := range committed {
			changedSet[path] = true
		}
	}

	uncommitted, err := d.gitStatusChanges(ctx)
	if err == nil {
		for _, path := range uncommitted {
			changedSet[path] = true
		}
	}

	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
	}

	for _, path := range currentFiles {
		if changedSet[path] {
			hash, _ := d.hashFile(path)
			changeType := ChangeModified
			if _, existed := d.meta.FileHashes[path]; !existed {
				changeType = ChangeAdded
			}
			changes = append(changes, FileChange{
				Path: path,
				Type: changeType,
				Hash: hash,
			})
		}
	}

	for path := range d.meta.FileHashes {
		if !currentSet[path] {
			changes = append(changes, FileChange{
				Path: path,
				Type: ChangeDeleted,
			})
		}
	}

	return changes, nil
}

func (d *ChangeDetector) gitDiffCommitted(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", d.meta.LastIndexedCommit, "HEAD")
	cmd.Dir = d.projectRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return d.parseGitOutput(out), nil
}

func (d *ChangeDetector) gitStatusChanges(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = d.projectRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (d *ChangeDetector) parseGitOutput(out []byte) []string {
	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		path := strings.TrimSpace(scanner.Text())
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func (d *ChangeDetector) detectHashChanges(currentFiles []string) ([]FileChange, error) {
	var changes []FileChange

	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
	}

	for _, path := range currentFiles {
		hash, err := d.hashFile(path)
		if err != nil {
			continue
		}

		storedHash, existed := d.meta.FileHashes[path]
		if !existed {
			changes = append(changes, FileChange{
				Path: path,
				Type: ChangeAdded,
				Hash: hash,
			})
		} else if hash != storedHash {
			changes = append(changes, FileChange{
				Path: path,
				Type: ChangeModified,
				Hash: hash,
			})
		}
	}

	for path := range d.meta.FileHashes {
		if !currentSet[path] {
			changes = append(changes, FileChange{
				Path: path,
				Type: ChangeDeleted,
			})
		}
	}

	return changes, nil
}

func (d *ChangeDetector) hashFile(relPath string) (string, error) {
	fullPath := filepath.Join(d.projectRoot, relPath)
	f, err := os.Open(fullPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (d *ChangeDetector) GetCurrentCommit(ctx context.Context) (string, error) {
	if !d.isGitRepo {
		return "", ErrNotGitRepo
	}

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = d.projectRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}
