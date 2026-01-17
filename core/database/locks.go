package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type AdvisoryLock struct {
	path string
	file *os.File
}

func NewAdvisoryLock(lockDir, name string) (*AdvisoryLock, error) {
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, err
	}

	return &AdvisoryLock{
		path: filepath.Join(lockDir, name+".lock"),
	}, nil
}

func (l *AdvisoryLock) Acquire(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("lock acquisition timeout: %s", l.path)
		}

		file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}

		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			l.file = file
			return nil
		}

		file.Close()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (l *AdvisoryLock) Release() error {
	if l.file == nil {
		return nil
	}

	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil

	if err != nil {
		return err
	}
	return closeErr
}

func (l *AdvisoryLock) TryAcquire() (bool, error) {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, err
	}

	l.file = file
	return true, nil
}

func (l *AdvisoryLock) IsHeld() bool {
	return l.file != nil
}

type LockManager struct {
	lockDir string
	locks   map[string]*AdvisoryLock
}

func NewLockManager(lockDir string) *LockManager {
	return &LockManager{
		lockDir: lockDir,
		locks:   make(map[string]*AdvisoryLock),
	}
}

func (lm *LockManager) Acquire(ctx context.Context, name string, timeout time.Duration) error {
	lock, err := NewAdvisoryLock(lm.lockDir, name)
	if err != nil {
		return err
	}

	if err := lock.Acquire(ctx, timeout); err != nil {
		return err
	}

	lm.locks[name] = lock
	return nil
}

func (lm *LockManager) Release(name string) error {
	lock, ok := lm.locks[name]
	if !ok {
		return nil
	}

	delete(lm.locks, name)
	return lock.Release()
}

func (lm *LockManager) ReleaseAll() error {
	var firstErr error
	for name, lock := range lm.locks {
		if err := lock.Release(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(lm.locks, name)
	}
	return firstErr
}
