package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoundToPage(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected int64
	}{
		{"zero returns page size", 0, PageSize},
		{"negative returns page size", -100, PageSize},
		{"one byte rounds up", 1, PageSize},
		{"exactly page size", PageSize, PageSize},
		{"one over page size", PageSize + 1, 2 * PageSize},
		{"two pages minus one", 2*PageSize - 1, 2 * PageSize},
		{"exactly two pages", 2 * PageSize, 2 * PageSize},
		{"large value", 10*PageSize + 500, 11 * PageSize},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RoundToPage(tc.input)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestMapFile_ReadWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.dat")

	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)
	require.NotNil(t, region)
	require.NotNil(t, region.Data())
	require.Equal(t, PageSize, region.Size())
	require.False(t, region.Readonly())

	// Write data to the mapped region
	data := region.Data()
	copy(data[:5], []byte("hello"))

	// Sync to disk
	err = region.Sync()
	require.NoError(t, err)

	// Close the region
	err = region.Close()
	require.NoError(t, err)

	// Verify data is nil after close
	require.Nil(t, region.Data())

	// Reopen and verify data persisted
	region2, err := MapFile(path, PageSize, true)
	require.NoError(t, err)
	require.NotNil(t, region2)
	require.True(t, region2.Readonly())

	data2 := region2.Data()
	require.Equal(t, []byte("hello"), data2[:5])

	err = region2.Close()
	require.NoError(t, err)
}

func TestMapFile_Readonly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.dat")

	// Create the file first
	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)
	copy(region.Data()[:4], []byte("test"))
	require.NoError(t, region.Sync())
	require.NoError(t, region.Close())

	// Open as readonly
	region, err = MapFile(path, PageSize, true)
	require.NoError(t, err)
	require.True(t, region.Readonly())

	// Sync should fail on readonly
	err = region.Sync()
	require.ErrorIs(t, err, ErrMmapReadonly)

	require.NoError(t, region.Close())
}

func TestMapFile_NonExistentReadonly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.dat")

	_, err := MapFile(path, PageSize, true)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err) || err != nil)
}

func TestMapFile_InvalidSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.dat")

	_, err := MapFile(path, 0, false)
	require.ErrorIs(t, err, ErrInvalidSize)

	_, err = MapFile(path, -100, false)
	require.ErrorIs(t, err, ErrInvalidSize)
}

func TestMapFile_PageAlignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "align.dat")

	// Request non-aligned size
	region, err := MapFile(path, 1000, false)
	require.NoError(t, err)

	// Should be rounded up to page size
	require.Equal(t, PageSize, region.Size())
	require.NoError(t, region.Close())

	// Request size larger than one page
	region2, err := MapFile(path, PageSize+1, false)
	require.NoError(t, err)
	require.Equal(t, 2*PageSize, region2.Size())
	require.NoError(t, region2.Close())
}

func TestGrowFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grow.dat")

	// Create initial file
	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)
	copy(region.Data()[:4], []byte("data"))
	require.NoError(t, region.Sync())
	require.NoError(t, region.Close())

	// Grow the file
	err = GrowFile(path, 3*PageSize)
	require.NoError(t, err)

	// Verify new size
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, 3*PageSize, info.Size())

	// Verify original data intact
	region2, err := MapFile(path, 3*PageSize, true)
	require.NoError(t, err)
	require.Equal(t, []byte("data"), region2.Data()[:4])
	require.NoError(t, region2.Close())
}

func TestGrowFile_InvalidSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grow_invalid.dat")

	// Create file first
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	err = GrowFile(path, 0)
	require.ErrorIs(t, err, ErrInvalidSize)

	err = GrowFile(path, -100)
	require.ErrorIs(t, err, ErrInvalidSize)
}

func TestGrowFile_NonExistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.dat")

	err := GrowFile(path, PageSize)
	require.Error(t, err)
}

func TestGrowFile_SmallerSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shrink.dat")

	// Create file with 2 pages
	region, err := MapFile(path, 2*PageSize, false)
	require.NoError(t, err)
	require.NoError(t, region.Close())

	// Try to "grow" to 1 page - should be no-op
	err = GrowFile(path, PageSize)
	require.NoError(t, err)

	// Size should remain 2 pages
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, 2*PageSize, info.Size())
}

func TestMmapRegion_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "double_close.dat")

	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)

	// First close should succeed
	err = region.Close()
	require.NoError(t, err)

	// Second close should be no-op
	err = region.Close()
	require.NoError(t, err)
}

func TestMmapRegion_SyncAfterClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync_closed.dat")

	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)

	require.NoError(t, region.Close())

	err = region.Sync()
	require.ErrorIs(t, err, ErrMmapClosed)
}

func TestMmapRegion_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.dat")

	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)
	defer region.Close()

	var wg sync.WaitGroup

	// Multiple concurrent readers
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for range 100 {
				data := region.Data()
				if data != nil {
					_ = data[0]
				}
				_ = region.Size()
				_ = region.Readonly()
			}
		}(i)
	}

	// Concurrent sync calls
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_ = region.Sync()
			}
		}()
	}

	wg.Wait()
}

func TestMmapRegion_ConcurrentClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent_close.dat")

	region, err := MapFile(path, PageSize, false)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var closeErrors []error
	var mu sync.Mutex

	// Multiple goroutines trying to close
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := region.Close()
			mu.Lock()
			closeErrors = append(closeErrors, err)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// All closes should succeed (no-op after first)
	for _, err := range closeErrors {
		require.NoError(t, err)
	}
}

func TestMmapRegion_LargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.dat")

	// Map 10MB file
	size := int64(10 * 1024 * 1024)
	region, err := MapFile(path, size, false)
	require.NoError(t, err)
	require.GreaterOrEqual(t, region.Size(), size)

	// Write at various positions
	data := region.Data()
	copy(data[:5], []byte("start"))
	copy(data[size-5:size], []byte("enddd"))

	require.NoError(t, region.Sync())
	require.NoError(t, region.Close())

	// Verify
	region2, err := MapFile(path, size, true)
	require.NoError(t, err)
	data2 := region2.Data()
	require.Equal(t, []byte("start"), data2[:5])
	require.Equal(t, []byte("enddd"), data2[size-5:size])
	require.NoError(t, region2.Close())
}

func TestPageSizeConstant(t *testing.T) {
	require.Equal(t, int64(4096), PageSize)
}
