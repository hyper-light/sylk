//go:build windows

package purevfs

import "fmt"

// allocSlabMemory returns a heap-backed slab on Windows. The arena and
// its dedup/free-list machinery still apply — only the off-heap GC win
// is lost (Windows VirtualAlloc would restore it but introduces
// cross-platform complexity that's not load-bearing for the wins
// targeted on Linux/Darwin where sylk's runtime concentrates).
func allocSlabMemory(size int) ([]byte, bool, error) {
	if size <= 0 {
		return nil, false, fmt.Errorf("purevfs: arena slab size must be positive (got %d)", size)
	}
	return make([]byte, size), false, nil
}

// releaseSlabMemory: no-op on Windows; Go GC reclaims heap-backed slabs.
func releaseSlabMemory(_ []byte, _ bool) {}
