package concurrency

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type StagingManager struct {
	sessionID   string
	workDir     string
	stagingRoot string
	mu          sync.RWMutex
	stages      map[string]*PipelineStaging
}

type PipelineStaging struct {
	pipelineID string
	stagingDir string
	fileHashes map[string]string
	deleted    map[string]bool
	manager    *StagingManager
	mu         sync.RWMutex
}

type Conflict struct {
	Path     string
	Ours     []byte
	Theirs   []byte
	Original string
}

type MergeResult struct {
	Applied   []string
	Conflicts []Conflict
}

func NewStagingManager(sessionID, workDir, stagingRoot string) *StagingManager {
	return &StagingManager{
		sessionID:   sessionID,
		workDir:     workDir,
		stagingRoot: stagingRoot,
		stages:      make(map[string]*PipelineStaging),
	}
}

func (m *StagingManager) CreateStaging(pipelineID string) (*PipelineStaging, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stagingDir := filepath.Join(m.stagingRoot, pipelineID)
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}

	staging := &PipelineStaging{
		pipelineID: pipelineID,
		stagingDir: stagingDir,
		fileHashes: make(map[string]string),
		deleted:    make(map[string]bool),
		manager:    m,
	}

	m.stages[pipelineID] = staging
	return staging, nil
}

func (m *StagingManager) GetStaging(pipelineID string) (*PipelineStaging, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	staging, ok := m.stages[pipelineID]
	return staging, ok
}

func (m *StagingManager) CloseStaging(pipelineID string) error {
	m.mu.Lock()
	staging, ok := m.stages[pipelineID]
	if ok {
		delete(m.stages, pipelineID)
	}
	m.mu.Unlock()

	if ok {
		return staging.Cleanup()
	}
	return nil
}

func (m *StagingManager) CloseAll() error {
	m.mu.Lock()
	stages := make([]*PipelineStaging, 0, len(m.stages))
	for _, s := range m.stages {
		stages = append(stages, s)
	}
	m.stages = make(map[string]*PipelineStaging)
	m.mu.Unlock()

	var firstErr error
	for _, s := range stages {
		if err := s.Cleanup(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (ps *PipelineStaging) ReadFile(relativePath string) ([]byte, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.deleted[relativePath] {
		return nil, os.ErrNotExist
	}

	stagingPath := filepath.Join(ps.stagingDir, relativePath)
	if content, err := os.ReadFile(stagingPath); err == nil {
		return content, nil
	}

	workPath := filepath.Join(ps.manager.workDir, relativePath)
	content, err := os.ReadFile(workPath)
	if err != nil {
		return nil, err
	}

	ps.fileHashes[relativePath] = hashContent(content)

	if err := os.MkdirAll(filepath.Dir(stagingPath), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(stagingPath, content, 0644); err != nil {
		return nil, err
	}

	return content, nil
}

func (ps *PipelineStaging) WriteFile(relativePath string, content []byte) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	delete(ps.deleted, relativePath)

	stagingPath := filepath.Join(ps.stagingDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(stagingPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(stagingPath, content, 0644)
}

func (ps *PipelineStaging) DeleteFile(relativePath string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.deleted[relativePath] = true

	stagingPath := filepath.Join(ps.stagingDir, relativePath)
	if err := os.Remove(stagingPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (ps *PipelineStaging) ListModified() []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var modified []string

	_ = filepath.Walk(ps.stagingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(ps.stagingDir, path)
		modified = append(modified, rel)
		return nil
	})

	for path := range ps.deleted {
		modified = append(modified, path)
	}

	return modified
}

func (ps *PipelineStaging) CheckConflicts() ([]Conflict, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var conflicts []Conflict

	for relativePath, originalHash := range ps.fileHashes {
		workPath := filepath.Join(ps.manager.workDir, relativePath)
		currentContent, err := os.ReadFile(workPath)
		if err != nil {
			continue
		}

		currentHash := hashContent(currentContent)
		if currentHash != originalHash {
			stagingPath := filepath.Join(ps.stagingDir, relativePath)
			ourContent, _ := os.ReadFile(stagingPath)

			conflicts = append(conflicts, Conflict{
				Path:     relativePath,
				Ours:     ourContent,
				Theirs:   currentContent,
				Original: originalHash,
			})
		}
	}

	return conflicts, nil
}

func (ps *PipelineStaging) Merge() (*MergeResult, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	result := &MergeResult{
		Applied:   []string{},
		Conflicts: []Conflict{},
	}

	for relativePath, originalHash := range ps.fileHashes {
		workPath := filepath.Join(ps.manager.workDir, relativePath)

		if ps.deleted[relativePath] {
			continue
		}

		currentContent, err := os.ReadFile(workPath)
		if err == nil {
			currentHash := hashContent(currentContent)
			if currentHash != originalHash {
				stagingPath := filepath.Join(ps.stagingDir, relativePath)
				ourContent, _ := os.ReadFile(stagingPath)

				result.Conflicts = append(result.Conflicts, Conflict{
					Path:     relativePath,
					Ours:     ourContent,
					Theirs:   currentContent,
					Original: originalHash,
				})
				continue
			}
		}
	}

	if len(result.Conflicts) > 0 {
		return result, fmt.Errorf("%d conflicts detected", len(result.Conflicts))
	}

	err := filepath.Walk(ps.stagingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		relativePath, _ := filepath.Rel(ps.stagingDir, path)
		workPath := filepath.Join(ps.manager.workDir, relativePath)

		stagedContent, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(workPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(workPath, stagedContent, 0644); err != nil {
			return err
		}

		result.Applied = append(result.Applied, relativePath)
		return nil
	})

	if err != nil {
		return result, err
	}

	for relativePath := range ps.deleted {
		workPath := filepath.Join(ps.manager.workDir, relativePath)
		if err := os.Remove(workPath); err != nil && !os.IsNotExist(err) {
			continue
		}
		result.Applied = append(result.Applied, relativePath)
	}

	return result, nil
}

func (ps *PipelineStaging) Abort() error {
	return ps.Cleanup()
}

func (ps *PipelineStaging) Cleanup() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	return os.RemoveAll(ps.stagingDir)
}

func (ps *PipelineStaging) PipelineID() string {
	return ps.pipelineID
}

func (ps *PipelineStaging) StagingDir() string {
	return ps.stagingDir
}

func hashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}
