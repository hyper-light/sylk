package versioning

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ApplyResult summarizes the outcome of writing versioned changes to disk.
type ApplyResult struct {
	FilesWritten  int
	FilesDeleted  int
	BytesWritten  int64
	Errors        []ApplyError
}

// ApplyError records a single file-level error during disk write.
type ApplyError struct {
	FilePath string
	Err      error
}

// DiskWriter applies committed CVS versions to the physical working directory.
// Used after user/auto approval to make pipeline changes permanent.
type DiskWriter struct {
	blobStore  BlobStore
	workingDir string
}

// NewDiskWriter creates a writer that resolves blob content and writes to disk.
func NewDiskWriter(blobStore BlobStore, workingDir string) *DiskWriter {
	return &DiskWriter{
		blobStore:  blobStore,
		workingDir: workingDir,
	}
}

// ApplyVersions writes the specified versions to the working directory.
// Each version's content is retrieved from the BlobStore and written to
// the file path recorded in the version. Deleted files are removed.
func (dw *DiskWriter) ApplyVersions(ctx context.Context, cvs CVS, versionIDs []VersionID) (*ApplyResult, error) {
	result := &ApplyResult{}

	for _, vid := range versionIDs {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		version, err := cvs.GetVersion(vid)
		if err != nil {
			result.Errors = append(result.Errors, ApplyError{
				Err: fmt.Errorf("get version %s: %w", vid, err),
			})
			continue
		}

		fullPath := dw.resolve(version.FilePath)

		if version.ContentSize == 0 && version.ContentHash == (ContentHash{}) {
			if removeErr := os.Remove(fullPath); removeErr != nil && !os.IsNotExist(removeErr) {
				result.Errors = append(result.Errors, ApplyError{
					FilePath: version.FilePath,
					Err:      removeErr,
				})
				continue
			}
			result.FilesDeleted++
			continue
		}

		content, blobErr := dw.blobStore.Get(version.ContentHash)
		if blobErr != nil {
			result.Errors = append(result.Errors, ApplyError{
				FilePath: version.FilePath,
				Err:      fmt.Errorf("blob get: %w", blobErr),
			})
			continue
		}

		dir := filepath.Dir(fullPath)
		if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
			result.Errors = append(result.Errors, ApplyError{
				FilePath: version.FilePath,
				Err:      mkErr,
			})
			continue
		}

		if writeErr := os.WriteFile(fullPath, content, 0644); writeErr != nil {
			result.Errors = append(result.Errors, ApplyError{
				FilePath: version.FilePath,
				Err:      writeErr,
			})
			continue
		}

		result.FilesWritten++
		result.BytesWritten += int64(len(content))
	}

	return result, nil
}

func (dw *DiskWriter) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dw.workingDir, path)
}
