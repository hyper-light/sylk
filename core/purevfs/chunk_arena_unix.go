//go:build !windows

package purevfs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// allocSlabMemory returns bytes backed by anonymous mmap (RAM-only,
// invisible to the Go GC). Honors the VFS no-disk-write rule: anonymous
// mmap with MAP_PRIVATE | MAP_ANON allocates virtual memory pages that
// the kernel populates lazily and never writes to a file. The kernel
// may swap dirty pages under memory pressure if swap is configured, but
// sylk itself issues no write syscall.
//
// Returns (bytes, mapped=true, err) on success. If the mmap call fails
// the function falls back to a Go-heap allocation so the arena stays
// functional under unusual sandbox restrictions; the mapped flag is
// false in that case so releaseSlabMemory knows to skip munmap.
func allocSlabMemory(size int) ([]byte, bool, error) {
	if size <= 0 {
		return nil, false, fmt.Errorf("purevfs: arena slab size must be positive (got %d)", size)
	}
	bytes, err := unix.Mmap(-1, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		// Fallback: heap-backed slab keeps the arena functional even if
		// the platform refuses anonymous mappings (e.g., a constrained
		// sandbox). Functionally identical; loses the off-heap GC win.
		return make([]byte, size), false, nil
	}
	return bytes, true, nil
}

// releaseSlabMemory unmaps the slab if it was anonymous-mapped, or lets
// the Go GC reclaim it if it came from the heap fallback.
func releaseSlabMemory(bytes []byte, mapped bool) {
	if !mapped || len(bytes) == 0 {
		return
	}
	_ = unix.Munmap(bytes)
}
