package session

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrFileLockTimeout    = errors.New("file lock acquisition timed out")
	ErrFileLocked         = errors.New("file is locked by another session")
	ErrNoFilesSpecified   = errors.New("no files specified")
	ErrLockNotHeld        = errors.New("lock not held by this session")
	ErrOperationAborted   = errors.New("coordinated operation was aborted")
	ErrOperationCompleted = errors.New("coordinated operation already completed")
)

type FileAccessMode int

const (
	FileAccessRead FileAccessMode = iota
	FileAccessWrite
)

const (
	DefaultFileLockTimeout = 30 * time.Second
)

type FileLock struct {
	FilePath   string
	SessionID  string
	Mode       FileAccessMode
	AcquiredAt time.Time
}

type CrossSessionPoolVFSConfig struct {
	BasePool        *CrossSessionPool
	Dispatcher      *SignalDispatcherVFS
	FileLockTimeout time.Duration
}

type fileOwnership struct {
	sessionID  string
	mode       FileAccessMode
	acquiredAt time.Time
	readers    map[string]time.Time
}

type CrossSessionPoolVFS struct {
	base            *CrossSessionPool
	dispatcher      *SignalDispatcherVFS
	fileLockTimeout time.Duration

	mu        sync.RWMutex
	fileLocks map[string]*fileOwnership
	waiters   map[string][]chan struct{}
}

func NewCrossSessionPoolVFS(cfg CrossSessionPoolVFSConfig) *CrossSessionPoolVFS {
	timeout := cfg.FileLockTimeout
	if timeout <= 0 {
		timeout = DefaultFileLockTimeout
	}

	return &CrossSessionPoolVFS{
		base:            cfg.BasePool,
		dispatcher:      cfg.Dispatcher,
		fileLockTimeout: timeout,
		fileLocks:       make(map[string]*fileOwnership),
		waiters:         make(map[string][]chan struct{}),
	}
}

func (p *CrossSessionPoolVFS) AcquireFileAccess(
	ctx context.Context,
	sessionID string,
	files []string,
	mode FileAccessMode,
) ([]FileLock, error) {
	if err := p.validateAcquireInput(sessionID, files); err != nil {
		return nil, err
	}

	locks := make([]FileLock, 0, len(files))
	for _, file := range files {
		lock, err := p.acquireSingleFile(ctx, sessionID, file, mode)
		if err != nil {
			p.releaseLocksOnError(locks)
			return nil, err
		}
		locks = append(locks, lock)
	}

	p.broadcastLockSignals(locks)
	return locks, nil
}

func (p *CrossSessionPoolVFS) validateAcquireInput(sessionID string, files []string) error {
	if sessionID == "" {
		return ErrEmptySessionID
	}
	if len(files) == 0 {
		return ErrNoFilesSpecified
	}
	return nil
}

func (p *CrossSessionPoolVFS) acquireSingleFile(
	ctx context.Context,
	sessionID string,
	filePath string,
	mode FileAccessMode,
) (FileLock, error) {
	deadline := time.Now().Add(p.fileLockTimeout)

	for {
		if p.tryAcquireFile(sessionID, filePath, mode) {
			return FileLock{
				FilePath:   filePath,
				SessionID:  sessionID,
				Mode:       mode,
				AcquiredAt: time.Now(),
			}, nil
		}

		if err := p.waitForFile(ctx, filePath, deadline); err != nil {
			return FileLock{}, err
		}
	}
}

func (p *CrossSessionPoolVFS) tryAcquireFile(sessionID, filePath string, mode FileAccessMode) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	ownership, exists := p.fileLocks[filePath]
	if !exists {
		return p.createOwnership(sessionID, filePath, mode)
	}

	return p.tryJoinOwnership(ownership, sessionID, mode)
}

func (p *CrossSessionPoolVFS) createOwnership(sessionID, filePath string, mode FileAccessMode) bool {
	ownership := &fileOwnership{
		mode:       mode,
		acquiredAt: time.Now(),
		readers:    make(map[string]time.Time),
	}

	if mode == FileAccessRead {
		ownership.readers[sessionID] = time.Now()
	} else {
		ownership.sessionID = sessionID
	}

	p.fileLocks[filePath] = ownership
	return true
}

func (p *CrossSessionPoolVFS) tryJoinOwnership(ownership *fileOwnership, sessionID string, mode FileAccessMode) bool {
	if mode == FileAccessRead && ownership.mode == FileAccessRead {
		ownership.readers[sessionID] = time.Now()
		return true
	}
	return false
}

func (p *CrossSessionPoolVFS) waitForFile(ctx context.Context, filePath string, deadline time.Time) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return ErrFileLockTimeout
	}

	waitCh := p.addWaiter(filePath)
	defer p.removeWaiter(filePath, waitCh)

	return p.waitWithTimeout(ctx, waitCh, remaining)
}

func (p *CrossSessionPoolVFS) waitWithTimeout(ctx context.Context, waitCh chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrFileLockTimeout
	case <-waitCh:
		return nil
	}
}

func (p *CrossSessionPoolVFS) addWaiter(filePath string) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch := make(chan struct{}, 1)
	p.waiters[filePath] = append(p.waiters[filePath], ch)
	return ch
}

func (p *CrossSessionPoolVFS) removeWaiter(filePath string, ch chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	waiters := p.waiters[filePath]
	for i, w := range waiters {
		if w == ch {
			p.waiters[filePath] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}

	if len(p.waiters[filePath]) == 0 {
		delete(p.waiters, filePath)
	}
}

func (p *CrossSessionPoolVFS) releaseLocksOnError(locks []FileLock) {
	for _, lock := range locks {
		p.releaseSingleFile(lock)
	}
}

func (p *CrossSessionPoolVFS) broadcastLockSignals(locks []FileLock) {
	if p.dispatcher == nil {
		return
	}
	for _, lock := range locks {
		p.dispatcher.BroadcastFileLocked(lock.FilePath, lock.SessionID)
	}
}

func (p *CrossSessionPoolVFS) ReleaseFileAccess(locks []FileLock) error {
	if len(locks) == 0 {
		return nil
	}

	for _, lock := range locks {
		p.releaseSingleFile(lock)
	}

	p.broadcastUnlockSignals(locks)
	return nil
}

func (p *CrossSessionPoolVFS) releaseSingleFile(lock FileLock) {
	p.mu.Lock()
	released := p.doRelease(lock)
	waitersToNotify := p.getWaitersToNotify(lock.FilePath, released)
	p.mu.Unlock()

	p.notifyWaiters(waitersToNotify)
}

func (p *CrossSessionPoolVFS) doRelease(lock FileLock) bool {
	ownership, exists := p.fileLocks[lock.FilePath]
	if !exists {
		return false
	}

	if lock.Mode == FileAccessRead {
		return p.releaseReadLock(lock.FilePath, lock.SessionID, ownership)
	}
	return p.releaseWriteLock(lock.FilePath, lock.SessionID, ownership)
}

func (p *CrossSessionPoolVFS) releaseReadLock(filePath, sessionID string, ownership *fileOwnership) bool {
	delete(ownership.readers, sessionID)
	if len(ownership.readers) == 0 && ownership.sessionID == "" {
		delete(p.fileLocks, filePath)
		return true
	}
	return false
}

func (p *CrossSessionPoolVFS) releaseWriteLock(filePath, sessionID string, ownership *fileOwnership) bool {
	if ownership.sessionID == sessionID {
		delete(p.fileLocks, filePath)
		return true
	}
	return false
}

func (p *CrossSessionPoolVFS) getWaitersToNotify(filePath string, released bool) []chan struct{} {
	if !released {
		return nil
	}
	waiters := p.waiters[filePath]
	if len(waiters) == 0 {
		return nil
	}
	result := make([]chan struct{}, len(waiters))
	copy(result, waiters)
	return result
}

func (p *CrossSessionPoolVFS) notifyWaiters(waiters []chan struct{}) {
	for _, ch := range waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (p *CrossSessionPoolVFS) broadcastUnlockSignals(locks []FileLock) {
	if p.dispatcher == nil {
		return
	}
	for _, lock := range locks {
		p.dispatcher.BroadcastFileUnlocked(lock.FilePath, lock.SessionID)
	}
}

func (p *CrossSessionPoolVFS) GetFileOwners(files []string) map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	owners := make(map[string]string, len(files))
	for _, file := range files {
		if ownership, exists := p.fileLocks[file]; exists {
			owners[file] = p.getOwnerFromOwnership(ownership)
		}
	}
	return owners
}

func (p *CrossSessionPoolVFS) getOwnerFromOwnership(ownership *fileOwnership) string {
	if ownership.sessionID != "" {
		return ownership.sessionID
	}
	for sessionID := range ownership.readers {
		return sessionID
	}
	return ""
}

func (p *CrossSessionPoolVFS) IsFileLocked(filePath string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, exists := p.fileLocks[filePath]
	return exists
}

func (p *CrossSessionPoolVFS) GetFileLockInfo(filePath string) (FileLock, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ownership, exists := p.fileLocks[filePath]
	if !exists {
		return FileLock{}, false
	}

	return FileLock{
		FilePath:   filePath,
		SessionID:  p.getOwnerFromOwnership(ownership),
		Mode:       ownership.mode,
		AcquiredAt: ownership.acquiredAt,
	}, true
}

type CoordinatedFileOperation struct {
	pool      *CrossSessionPoolVFS
	sessionID string
	locks     []FileLock
	completed bool
	aborted   bool
	mu        sync.Mutex
}

func (p *CrossSessionPoolVFS) BeginCoordinatedOp(
	ctx context.Context,
	sessionID string,
	files []string,
	mode FileAccessMode,
) (*CoordinatedFileOperation, error) {
	locks, err := p.AcquireFileAccess(ctx, sessionID, files, mode)
	if err != nil {
		return nil, err
	}

	return &CoordinatedFileOperation{
		pool:      p,
		sessionID: sessionID,
		locks:     locks,
	}, nil
}

func (op *CoordinatedFileOperation) Complete() error {
	op.mu.Lock()
	defer op.mu.Unlock()

	if op.completed {
		return ErrOperationCompleted
	}
	if op.aborted {
		return ErrOperationAborted
	}

	op.completed = true
	return op.pool.ReleaseFileAccess(op.locks)
}

func (op *CoordinatedFileOperation) Abort() error {
	op.mu.Lock()
	defer op.mu.Unlock()

	if op.completed {
		return ErrOperationCompleted
	}
	if op.aborted {
		return ErrOperationAborted
	}

	op.aborted = true
	return op.pool.ReleaseFileAccess(op.locks)
}

func (op *CoordinatedFileOperation) Locks() []FileLock {
	op.mu.Lock()
	defer op.mu.Unlock()

	result := make([]FileLock, len(op.locks))
	copy(result, op.locks)
	return result
}

func (op *CoordinatedFileOperation) SessionID() string {
	return op.sessionID
}

func (op *CoordinatedFileOperation) IsCompleted() bool {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.completed
}

func (op *CoordinatedFileOperation) IsAborted() bool {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.aborted
}

func (p *CrossSessionPoolVFS) Base() *CrossSessionPool {
	return p.base
}

func (p *CrossSessionPoolVFS) Dispatcher() *SignalDispatcherVFS {
	return p.dispatcher
}
