package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-billy/v5"
)

type DiskIngestConfig struct {
	MemFS       billy.Filesystem
	DiskRoot    string
	MaxFileSize int64
}

func IngestIntoDisk(ctx context.Context, cfg DiskIngestConfig) (*IngestResult, error) {
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = 1024 * 1024
	}
	start := time.Now()
	result := &IngestResult{}
	if err := ingestDiskDir(ctx, cfg, "/", result); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}
	result.Duration = time.Since(start)
	return result, nil
}

func ingestDiskDir(ctx context.Context, cfg DiskIngestConfig, dir string, result *IngestResult) error {
	entries, err := cfg.MemFS.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", dir, err)
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fullPath := joinBillyPath(dir, entry.Name())
		if entry.IsDir() {
			if isSkippedDir(entry.Name()) {
				continue
			}
			if err := ingestDiskDir(ctx, cfg, fullPath, result); err != nil {
				return err
			}
			continue
		}
		if entry.Size() > cfg.MaxFileSize {
			result.FilesSkipped++
			continue
		}
		content, err := readBillyFile(cfg.MemFS, fullPath)
		if err != nil {
			result.FilesSkipped++
			continue
		}
		if isBinary(content) {
			result.FilesSkipped++
			continue
		}
		target := filepath.Join(cfg.DiskRoot, filepath.FromSlash(stringsTrimPrefix(fullPath, "/")))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		mode := entry.Mode()
		if mode == 0 {
			mode = 0644
		}
		if err := os.WriteFile(target, content, mode); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		result.FilesWritten++
		result.BytesWritten += int64(len(content))
	}
	return nil
}

func stringsTrimPrefix(value, prefix string) string {
	if len(prefix) == 0 {
		return value
	}
	if len(value) < len(prefix) {
		return value
	}
	if value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}
