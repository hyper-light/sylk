package safelock

import "context"

// Mutex is a context-aware mutex
type Mutex struct {
	ch chan struct{}
}

func NewMutex() *Mutex {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	return &Mutex{ch: ch}
}

func (m *Mutex) Lock(ctx context.Context) error {
	select {
	case <-m.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Mutex) Unlock() {
	m.ch <- struct{}{}
}

// TryLock attempts to acquire the lock without blocking
func (m *Mutex) TryLock() bool {
	select {
	case <-m.ch:
		return true
	default:
		return false
	}
}
