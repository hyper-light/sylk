package git

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
	"github.com/go-git/go-billy/v5"
)

// IngestConfig configures how in-memory cloned files are ingested into a VFS.
type IngestConfig struct {
	// MemFS is the in-memory billy filesystem from the clone result.
	MemFS billy.Filesystem

	// VFSPrefix is the path prefix under which files are stored in the VFS.
	// For example: ".sylk/packages/owner/repo"
	VFSPrefix string

	// VFS is the target VFS to write files into.
	VFS versioning.VFS

	// MaxFileSize skips individual files larger than this threshold.
	// Default: 1 MiB.
	MaxFileSize int64
}

// IngestResult reports the outcome of a VFS ingestion.
type IngestResult struct {
	FilesWritten int
	FilesSkipped int
	BytesWritten int64
	Duration     time.Duration
}

// IngestIntoVFS walks an in-memory billy filesystem and writes each eligible
// file into the target VFS under the configured prefix. Skips .git directories,
// oversized files, and binary files.
func IngestIntoVFS(ctx context.Context, cfg IngestConfig) (*IngestResult, error) {
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = 1024 * 1024 // 1 MiB
	}

	start := time.Now()
	result := &IngestResult{}

	if err := ingestDir(ctx, cfg, "/", result); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	result.Duration = time.Since(start)
	return result, nil
}

// ingestDir recursively walks a directory in the billy filesystem.
func ingestDir(ctx context.Context, cfg IngestConfig, dir string, result *IngestResult) error {
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
			if err := ingestDir(ctx, cfg, fullPath, result); err != nil {
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

		// Build VFS path: prefix + relative path from root.
		relPath := strings.TrimPrefix(fullPath, "/")
		vfsPath := cfg.VFSPrefix + "/" + relPath

		if err := cfg.VFS.Write(ctx, vfsPath, content); err != nil {
			return fmt.Errorf("vfs write %s: %w", vfsPath, err)
		}

		result.FilesWritten++
		result.BytesWritten += int64(len(content))
	}

	return nil
}

func joinBillyPath(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

func isSkippedDir(name string) bool {
	return name == ".git" || name == "node_modules" || name == ".svn"
}

func readBillyFile(fs billy.Filesystem, path string) ([]byte, error) {
	f, err := fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// isBinary detects whether content is likely binary by scanning
// the first 8 KiB for NUL bytes.
func isBinary(content []byte) bool {
	limit := len(content)
	if limit > 8192 {
		limit = 8192
	}
	return strings.ContainsRune(string(content[:limit]), '\x00')
}
