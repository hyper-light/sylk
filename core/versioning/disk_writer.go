package versioning

// ApplyResult summarizes the outcome of writing versioned changes to disk.
// Kept for backward compatibility — superseded by FlushResult in disk_flusher.go.
type ApplyResult struct {
	FilesWritten int
	FilesDeleted int
	BytesWritten int64
	Errors       []ApplyError
}

// ApplyError records a single file-level error during disk write.
type ApplyError struct {
	FilePath string
	Err      error
}
