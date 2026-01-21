// Package storage provides mmap-based storage types for the Vamana index.
package storage

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// PageSize is the standard memory page size for alignment (4KB).
const PageSize int64 = 4096

// Errors for mmap operations.
var (
	ErrMmapClosed       = errors.New("mmap region is closed")
	ErrMmapReadonly     = errors.New("cannot sync readonly mmap region")
	ErrInvalidSize      = errors.New("size must be positive")
	ErrInsufficientDisk = errors.New("insufficient disk space")
)

// RoundToPage rounds a size up to the nearest page boundary.
// Returns at least PageSize for any positive input.
func RoundToPage(size int64) int64 {
	if size <= 0 {
		return PageSize
	}
	return ((size + PageSize - 1) / PageSize) * PageSize
}

// MmapRegion represents a memory-mapped file region.
type MmapRegion struct {
	data     []byte
	size     int64
	readonly bool
	file     *os.File
	closed   atomic.Bool
}

// Data returns the mapped byte slice.
// Returns nil if the region is closed.
func (r *MmapRegion) Data() []byte {
	if r.closed.Load() {
		return nil
	}
	return r.data
}

// Size returns the size of the mapped region.
func (r *MmapRegion) Size() int64 {
	return r.size
}

// Readonly returns whether the region is readonly.
func (r *MmapRegion) Readonly() bool {
	return r.readonly
}

// MapFile creates a memory-mapped region for the specified file.
// If readonly is false and the file doesn't exist, it will be created.
// The file is extended/truncated to the requested size before mapping.
func MapFile(path string, size int64, readonly bool) (*MmapRegion, error) {
	if size <= 0 {
		return nil, fmt.Errorf("mmap: %w: got %d", ErrInvalidSize, size)
	}

	alignedSize := RoundToPage(size)

	var file *os.File
	var err error

	if readonly {
		file, err = os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("mmap: open file for reading: %w", err)
		}
	} else {
		file, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return nil, fmt.Errorf("mmap: open file for writing: %w", err)
		}

		if err := truncateFile(file, alignedSize); err != nil {
			file.Close()
			return nil, err
		}
	}

	prot := unix.PROT_READ
	if !readonly {
		prot |= unix.PROT_WRITE
	}

	data, err := unix.Mmap(int(file.Fd()), 0, int(alignedSize), prot, unix.MAP_SHARED)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("mmap: map file: %w", err)
	}

	return &MmapRegion{
		data:     data,
		size:     alignedSize,
		readonly: readonly,
		file:     file,
	}, nil
}

// truncateFile extends or truncates a file to the specified size.
// It checks for disk space errors and returns a descriptive error.
func truncateFile(file *os.File, size int64) error {
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("mmap: stat file: %w", err)
	}

	if stat.Size() >= size {
		return nil
	}

	if err := file.Truncate(size); err != nil {
		if errors.Is(err, unix.ENOSPC) {
			return fmt.Errorf("mmap: %w: need %d bytes", ErrInsufficientDisk, size)
		}
		return fmt.Errorf("mmap: truncate file to %d bytes: %w", size, err)
	}

	return nil
}

// GrowFile extends a file to a new size without remapping.
// This is useful for preparing a file for a subsequent MapFile call.
// The file must exist and the new size must be larger than the current size.
func GrowFile(path string, newSize int64) error {
	if newSize <= 0 {
		return fmt.Errorf("mmap: %w: got %d", ErrInvalidSize, newSize)
	}

	alignedSize := RoundToPage(newSize)

	file, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("mmap: open file for growth: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("mmap: stat file: %w", err)
	}

	if stat.Size() >= alignedSize {
		return nil
	}

	if err := file.Truncate(alignedSize); err != nil {
		if errors.Is(err, unix.ENOSPC) {
			return fmt.Errorf("mmap: %w: need %d bytes, have %d", ErrInsufficientDisk, alignedSize, stat.Size())
		}
		return fmt.Errorf("mmap: grow file to %d bytes: %w", alignedSize, err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("mmap: sync after growth: %w", err)
	}

	return nil
}

// Sync flushes the mapped region to disk using msync.
// Returns an error if the region is readonly or closed.
func (r *MmapRegion) Sync() error {
	if r.closed.Load() {
		return ErrMmapClosed
	}

	if r.readonly {
		return ErrMmapReadonly
	}

	if err := unix.Msync(r.data, unix.MS_SYNC); err != nil {
		return fmt.Errorf("mmap: msync: %w", err)
	}

	return nil
}

// Close releases the memory mapping and closes the underlying file.
// It is safe to call Close multiple times; subsequent calls are no-ops.
func (r *MmapRegion) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}

	var errs []error

	if r.data != nil {
		if err := unix.Munmap(r.data); err != nil {
			errs = append(errs, fmt.Errorf("mmap: munmap: %w", err))
		}
		r.data = nil
	}

	if r.file != nil {
		if err := r.file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mmap: close file: %w", err))
		}
		r.file = nil
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
