package lua

import (
	"errors"
	"sync"
	"time"

	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Capacity limits
// ---------------------------------------------------------------------------

// scheduleBufferSize is the channel capacity for immediate vim.schedule()
// callbacks.  Derived from practical plugin usage -- even heavy plugins
// rarely queue more than a few dozen per frame.
const scheduleBufferSize = 256

// maxDeferredCallbacks caps the number of pending vim.defer_fn entries.
const maxDeferredCallbacks = 256

// Sentinel errors.
var errScheduleFull = errors.New("schedule buffer full")

// ---------------------------------------------------------------------------
// deferEntry
// ---------------------------------------------------------------------------

// deferEntry pairs a Lua callback with the earliest time it should run.
type deferEntry struct {
	Fn       *glua.LFunction
	RunAfter time.Time
}

// ---------------------------------------------------------------------------
// Scheduler
// ---------------------------------------------------------------------------

// Scheduler manages vim.schedule() and vim.defer_fn() callbacks.
// Immediate callbacks are held in a bounded channel; deferred callbacks
// sit in a bounded slice guarded by a mutex.
type Scheduler struct {
	pending  chan *glua.LFunction
	mu       sync.Mutex
	deferred []deferEntry
}

// NewScheduler returns a ready-to-use scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		pending:  make(chan *glua.LFunction, scheduleBufferSize),
		deferred: make([]deferEntry, 0, maxDeferredCallbacks),
	}
}

// Schedule queues fn for execution on the next Drain cycle.
func (s *Scheduler) Schedule(fn *glua.LFunction) error {
	select {
	case s.pending <- fn:
		return nil
	default:
		return errScheduleFull
	}
}

// DeferFn queues fn for execution after delayMs milliseconds.
func (s *Scheduler) DeferFn(fn *glua.LFunction, delayMs int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.deferred) >= maxDeferredCallbacks {
		return errScheduleFull
	}
	s.deferred = append(s.deferred, deferEntry{
		Fn:       fn,
		RunAfter: time.Now().Add(time.Duration(delayMs) * time.Millisecond),
	})
	return nil
}

// Drain executes all pending and ready deferred callbacks.  It returns a
// slice of any errors encountered during callback execution.
func (s *Scheduler) Drain(rt *Runtime) []error {
	var errs []error
	errs = append(errs, s.drainImmediate(rt)...)
	errs = append(errs, s.drainDeferred(rt)...)
	return errs
}

// drainImmediate processes all entries currently in the pending channel.
func (s *Scheduler) drainImmediate(rt *Runtime) []error {
	var errs []error
	for {
		select {
		case fn := <-s.pending:
			if _, err := rt.CallFunction(fn); err != nil {
				errs = append(errs, err)
			}
		default:
			return errs
		}
	}
}

// drainDeferred fires any deferred entries whose RunAfter time has passed.
func (s *Scheduler) drainDeferred(rt *Runtime) []error {
	s.mu.Lock()
	now := time.Now()
	ready, remaining := partitionDeferred(s.deferred, now)
	s.deferred = remaining
	s.mu.Unlock()

	var errs []error
	for _, entry := range ready {
		if _, err := rt.CallFunction(entry.Fn); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// partitionDeferred splits entries into those ready to fire and those
// still pending, preserving relative order.
func partitionDeferred(entries []deferEntry, now time.Time) (ready, remaining []deferEntry) {
	ready = make([]deferEntry, 0, len(entries))
	remaining = make([]deferEntry, 0, len(entries))
	for _, e := range entries {
		if now.After(e.RunAfter) || now.Equal(e.RunAfter) {
			ready = append(ready, e)
		} else {
			remaining = append(remaining, e)
		}
	}
	return ready, remaining
}

// ---------------------------------------------------------------------------
// Lua registration
// ---------------------------------------------------------------------------

// registerScheduleFunctions attaches vim.schedule and vim.defer_fn.
func registerScheduleFunctions(L *glua.LState, vim *glua.LTable, rt *Runtime) {
	vim.RawSetString("schedule", L.NewFunction(func(ls *glua.LState) int {
		return luaSchedule(ls, rt)
	}))
	vim.RawSetString("defer_fn", L.NewFunction(func(ls *glua.LState) int {
		return luaDeferFn(ls, rt)
	}))
}

// luaSchedule implements vim.schedule(fn).
func luaSchedule(L *glua.LState, rt *Runtime) int {
	fn, ok := L.Get(1).(*glua.LFunction)
	if !ok {
		return 0
	}
	_ = rt.Scheduler.Schedule(fn)
	return 0
}

// luaDeferFn implements vim.defer_fn(fn, delay).
func luaDeferFn(L *glua.LState, rt *Runtime) int {
	fn, ok := L.Get(1).(*glua.LFunction)
	if !ok {
		return 0
	}
	delayMs := L.ToInt(2)
	_ = rt.Scheduler.DeferFn(fn, delayMs)
	return 0
}
