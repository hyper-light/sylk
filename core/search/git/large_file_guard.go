package git

import (
	"path/filepath"
	"strings"
)

// LargeFileEntry describes a staged file that exceeds the size threshold
// or is a known binary format.
type LargeFileEntry struct {
	Path   string
	Size   int64
	Binary bool
}

// binaryExtensions is the set of file extensions considered binary.
// Derived from common binary formats that should not be committed.
var binaryExtensions = map[string]struct{}{
	".exe": {}, ".dll": {}, ".so": {}, ".dylib": {},
	".zip": {}, ".tar.gz": {}, ".jar": {}, ".bin": {},
	".o": {}, ".a": {}, ".wasm": {},
	".pdf": {}, ".png": {}, ".jpg": {}, ".jpeg": {},
	".gif": {}, ".mp4": {}, ".mp3": {},
}

// ScanStagedLargeFiles checks staged files against a size threshold and
// binary extension list. Only files in the provided paths set are checked.
// Reads index entries directly — no worktree or blob access needed.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) ScanStagedLargeFiles(paths []string, sizeThreshold int64) ([]LargeFileEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if len(paths) == 0 {
		return nil, nil
	}

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return nil, err
	}

	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	var findings []LargeFileEntry
	for _, entry := range idx.Entries {
		if _, ok := pathSet[entry.Name]; !ok {
			continue
		}
		isBin := isBinaryExtension(entry.Name)
		isLarge := int64(entry.Size) >= sizeThreshold
		if isBin || isLarge {
			findings = append(findings, LargeFileEntry{
				Path:   entry.Name,
				Size:   int64(entry.Size),
				Binary: isBin,
			})
		}
	}

	return findings, nil
}

// isBinaryExtension reports whether the file path has a known binary extension.
func isBinaryExtension(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := binaryExtensions[ext]; ok {
		return true
	}
	// Handle compound extensions like .tar.gz.
	if ext == ".gz" {
		base := strings.TrimSuffix(path, ext)
		ext2 := strings.ToLower(filepath.Ext(base))
		compound := ext2 + ext
		_, ok := binaryExtensions[compound]
		return ok
	}
	return false
}
