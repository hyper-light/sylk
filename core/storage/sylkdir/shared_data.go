package sylkdir

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// SharedDataFile is an append-only data file shared across versions.
// Writes append to the end and return the starting offset.
// Reads use ReadAt for lock-free concurrent access (POSIX guarantees
// safety for reads on append-only files where the region was fully written).
//
// Concurrency model: single-writer-multiple-reader.
// Append is mutex-protected for write serialization.
// ReadAt and Size are completely lock-free.
type SharedDataFile struct {
	path string
	mu   sync.Mutex
	file *os.File
	size atomic.Int64
}

// OpenSharedDataFile opens or creates an append-only shared data file.
// The file is opened for both reading and appending.
func OpenSharedDataFile(path string) (*SharedDataFile, error) {
	if err := os.MkdirAll(dirOfPath(path), 0755); err != nil {
		return nil, fmt.Errorf("create shared data dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open shared data file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat shared data file: %w", err)
	}

	sdf := &SharedDataFile{
		path: path,
		file: f,
	}
	sdf.size.Store(info.Size())
	return sdf, nil
}

// Append writes data to the end of the file and returns the starting offset.
func (sdf *SharedDataFile) Append(data []byte) (int64, error) {
	sdf.mu.Lock()
	defer sdf.mu.Unlock()

	offset := sdf.size.Load()
	n, err := sdf.file.WriteAt(data, offset)
	sdf.size.Add(int64(n))
	if err != nil {
		return offset, fmt.Errorf("append shared data: %w", err)
	}
	return offset, nil
}

// ReadAt reads len(buf) bytes from the file starting at the given offset.
// Lock-free: safe for concurrent reads on append-only files.
func (sdf *SharedDataFile) ReadAt(buf []byte, offset int64) (int, error) {
	return sdf.file.ReadAt(buf, offset)
}

// Size returns the current file size. Lock-free.
func (sdf *SharedDataFile) Size() int64 {
	return sdf.size.Load()
}

// Sync flushes the file to durable storage.
func (sdf *SharedDataFile) Sync() error {
	return sdf.file.Sync()
}

// AppendBatch writes multiple records in a single I/O operation.
// Returns the starting offset of each record. Single mutex acquisition.
func (sdf *SharedDataFile) AppendBatch(records [][]byte) ([]int64, error) {
	totalSize := 0
	for _, r := range records {
		totalSize += len(r)
	}

	buf := make([]byte, totalSize)
	offsets := make([]int64, len(records))

	sdf.mu.Lock()
	baseOffset := sdf.size.Load()

	pos := 0
	for i, r := range records {
		offsets[i] = baseOffset + int64(pos)
		copy(buf[pos:], r)
		pos += len(r)
	}

	n, err := sdf.file.WriteAt(buf, baseOffset)
	sdf.size.Add(int64(n))
	sdf.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("append batch: %w", err)
	}
	return offsets, nil
}

// AppendRaw writes a pre-assembled buffer in a single I/O operation.
// Returns the starting offset. Single mutex acquisition.
// Use this when the caller has already marshaled all records into a contiguous
// buffer (e.g., fixed-size records marshaled with MarshalTo pattern).
func (sdf *SharedDataFile) AppendRaw(buf []byte) (int64, error) {
	sdf.mu.Lock()
	baseOffset := sdf.size.Load()
	n, err := sdf.file.WriteAt(buf, baseOffset)
	sdf.size.Add(int64(n))
	sdf.mu.Unlock()

	if err != nil {
		return 0, fmt.Errorf("append raw: %w", err)
	}
	return baseOffset, nil
}

// Close closes the underlying file.
func (sdf *SharedDataFile) Close() error {
	if sdf.file == nil {
		return nil
	}
	return sdf.file.Close()
}

// Path returns the file path.
func (sdf *SharedDataFile) Path() string {
	return sdf.path
}

// CompactTo copies live records from this file to destPath, returning old→new offset mapping.
// recordSizeFn reads a record at the given offset and returns its total size in bytes.
// The caller provides liveOffsets (sorted ascending for sequential I/O).
func (sdf *SharedDataFile) CompactTo(destPath string, liveOffsets []int64,
	recordSizeFn func(f *SharedDataFile, offset int64) (int, error)) (map[int64]int64, error) {
	sdf.mu.Lock()
	defer sdf.mu.Unlock()

	dest, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("compact: create dest: %w", err)
	}

	remap := make(map[int64]int64, len(liveOffsets))
	var destOffset int64

	for _, oldOffset := range liveOffsets {
		recSize, err := recordSizeFn(sdf, oldOffset)
		if err != nil {
			dest.Close()
			os.Remove(destPath)
			return nil, fmt.Errorf("compact: read record size at %d: %w", oldOffset, err)
		}

		buf := make([]byte, recSize)
		if _, err := sdf.file.ReadAt(buf, oldOffset); err != nil {
			dest.Close()
			os.Remove(destPath)
			return nil, fmt.Errorf("compact: read record at %d: %w", oldOffset, err)
		}

		if _, err := dest.Write(buf); err != nil {
			dest.Close()
			os.Remove(destPath)
			return nil, fmt.Errorf("compact: write record: %w", err)
		}

		remap[oldOffset] = destOffset
		destOffset += int64(recSize)
	}

	if err := dest.Sync(); err != nil {
		dest.Close()
		os.Remove(destPath)
		return nil, fmt.Errorf("compact: fsync: %w", err)
	}
	if err := dest.Close(); err != nil {
		os.Remove(destPath)
		return nil, fmt.Errorf("compact: close: %w", err)
	}

	return remap, nil
}

// ReplaceFile atomically replaces this file with newPath.
// Closes the current file, renames newPath to this file's path, then reopens.
func (sdf *SharedDataFile) ReplaceFile(newPath string) error {
	sdf.mu.Lock()
	defer sdf.mu.Unlock()

	if err := sdf.file.Close(); err != nil {
		return fmt.Errorf("replace: close old: %w", err)
	}

	if err := os.Rename(newPath, sdf.path); err != nil {
		return fmt.Errorf("replace: rename: %w", err)
	}
	if err := syncPathDir(sdf.path); err != nil {
		return fmt.Errorf("replace: sync dir: %w", err)
	}

	f, err := os.OpenFile(sdf.path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("replace: reopen: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("replace: stat: %w", err)
	}

	sdf.file = f
	sdf.size.Store(info.Size())
	return nil
}

// dirOfPath returns the directory portion of a file path.
func dirOfPath(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
