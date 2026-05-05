package transport

import "sync"

// boundedRing is a fixed-capacity ring buffer used to retain the tail of a
// noisy stream (e.g. stderr) for diagnostics. Bounded by construction so a
// chatty server cannot drive memory growth.
type boundedRing struct {
	mu     sync.Mutex
	buf    []string
	cap    int
	cursor int
	full   bool
}

func newBoundedRing(capacity int) *boundedRing {
	if capacity <= 0 {
		capacity = 1
	}
	return &boundedRing{buf: make([]string, capacity), cap: capacity}
}

func (r *boundedRing) push(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.cursor] = line
	r.cursor++
	if r.cursor >= r.cap {
		r.cursor = 0
		r.full = true
	}
}

func (r *boundedRing) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]string, r.cursor)
		copy(out, r.buf[:r.cursor])
		return out
	}
	out := make([]string, 0, r.cap)
	out = append(out, r.buf[r.cursor:]...)
	out = append(out, r.buf[:r.cursor]...)
	return out
}
