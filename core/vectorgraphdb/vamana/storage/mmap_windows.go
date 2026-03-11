//go:build windows

package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

const PageSize int64 = 4096

var (
	ErrMmapClosed       = errors.New("mmap region is closed")
	ErrMmapReadonly     = errors.New("cannot sync readonly mmap region")
	ErrInvalidSize      = errors.New("size must be positive")
	ErrInsufficientDisk = errors.New("insufficient disk space")
)

func RoundToPage(size int64) int64 {
	if size <= 0 {
		return PageSize
	}
	return ((size + PageSize - 1) / PageSize) * PageSize
}

type MmapRegion struct {
	data     []byte
	size     int64
	readonly bool
	file     *os.File
	closed   atomic.Bool
}

func (r *MmapRegion) Data() []byte {
	if r.closed.Load() {
		return nil
	}
	return r.data
}

func (r *MmapRegion) Size() int64 {
	return r.size
}

func (r *MmapRegion) Readonly() bool {
	return r.readonly
}

func MapFile(path string, size int64, readonly bool) (*MmapRegion, error) {
	if size <= 0 {
		return nil, fmt.Errorf("mmap: %w: got %d", ErrInvalidSize, size)
	}
	alignedSize := RoundToPage(size)
	file, err := openMmapFile(path, alignedSize, readonly)
	if err != nil {
		return nil, err
	}
	data, err := loadMmapData(file, alignedSize)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &MmapRegion{
		data:     data,
		size:     alignedSize,
		readonly: readonly,
		file:     file,
	}, nil
}

func openMmapFile(path string, size int64, readonly bool) (*os.File, error) {
	if readonly {
		file, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("mmap: open file for reading: %w", err)
		}
		return file, nil
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("mmap: open file for writing: %w", err)
	}
	if err := truncateFile(file, size); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func loadMmapData(file *os.File, size int64) ([]byte, error) {
	data := make([]byte, size)
	_, err := file.ReadAt(data, 0)
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return data, nil
	}
	return nil, fmt.Errorf("mmap: read file: %w", err)
}

func truncateFile(file *os.File, size int64) error {
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("mmap: stat file: %w", err)
	}
	if stat.Size() >= size {
		return nil
	}
	if err := file.Truncate(size); err != nil {
		return fmt.Errorf("mmap: truncate file to %d bytes: %w", size, err)
	}
	return nil
}

func GrowFile(path string, newSize int64) error {
	if newSize <= 0 {
		return fmt.Errorf("mmap: %w: got %d", ErrInvalidSize, newSize)
	}
	alignedSize := RoundToPage(newSize)
	file, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("mmap: open file for growth: %w", err)
	}
	defer file.Close()
	if err := truncateFile(file, alignedSize); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("mmap: sync after growth: %w", err)
	}
	return nil
}

func (r *MmapRegion) Sync() error {
	if r.closed.Load() {
		return ErrMmapClosed
	}
	if r.readonly {
		return ErrMmapReadonly
	}
	if _, err := r.file.WriteAt(r.data, 0); err != nil {
		return fmt.Errorf("mmap: sync: %w", err)
	}
	if err := r.file.Sync(); err != nil {
		return fmt.Errorf("mmap: sync: %w", err)
	}
	return nil
}

func (r *MmapRegion) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	var errs []error
	if !r.readonly {
		if _, err := r.file.WriteAt(r.data, 0); err != nil {
			errs = append(errs, fmt.Errorf("mmap: sync: %w", err))
		} else if err := r.file.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("mmap: sync: %w", err))
		}
	}
	r.data = nil
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
