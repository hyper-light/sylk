package resources

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type FileMode int

const (
	ModeRead FileMode = iota
	ModeWrite
	ModeReadWrite
	ModeAppend
)

func (m FileMode) String() string {
	switch m {
	case ModeRead:
		return "read"
	case ModeWrite:
		return "write"
	case ModeReadWrite:
		return "read_write"
	case ModeAppend:
		return "append"
	default:
		return "unknown"
	}
}

func (m FileMode) ToOSFlags() int {
	switch m {
	case ModeRead:
		return os.O_RDONLY
	case ModeWrite:
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case ModeReadWrite:
		return os.O_RDWR | os.O_CREATE
	case ModeAppend:
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND
	default:
		return os.O_RDONLY
	}
}

type TrackedResource interface {
	Type() string
	ID() string
	ForceClose() error
}

type TrackedFile struct {
	file       *os.File
	path       string
	fd         int
	mode       FileMode
	openedAt   time.Time
	lastAccess atomic.Value
	sessionID  string
	agentID    string
	budget     *AgentFileBudget
	mu         sync.Mutex
	closed     bool
}

func NewTrackedFile(
	file *os.File,
	path string,
	mode FileMode,
	sessionID, agentID string,
	budget *AgentFileBudget,
) *TrackedFile {
	tf := &TrackedFile{
		file:      file,
		path:      path,
		fd:        int(file.Fd()),
		mode:      mode,
		openedAt:  time.Now(),
		sessionID: sessionID,
		agentID:   agentID,
		budget:    budget,
	}
	tf.lastAccess.Store(tf.openedAt)
	return tf
}

func (f *TrackedFile) Type() string {
	return "file"
}

func (f *TrackedFile) ID() string {
	return fmt.Sprintf("file:%s:%d", f.path, f.fd)
}

func (f *TrackedFile) ForceClose() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil
	}
	f.closed = true

	if f.budget != nil {
		f.budget.Release()
	}
	return f.file.Close()
}

func (f *TrackedFile) Read(p []byte) (int, error) {
	f.lastAccess.Store(time.Now())
	return f.file.Read(p)
}

func (f *TrackedFile) Write(p []byte) (int, error) {
	f.lastAccess.Store(time.Now())
	return f.file.Write(p)
}

func (f *TrackedFile) Close() error {
	return f.ForceClose()
}

func (f *TrackedFile) Path() string {
	return f.path
}

func (f *TrackedFile) Fd() int {
	return f.fd
}

func (f *TrackedFile) Mode() FileMode {
	return f.mode
}

func (f *TrackedFile) SessionID() string {
	return f.sessionID
}

func (f *TrackedFile) AgentID() string {
	return f.agentID
}

func (f *TrackedFile) OpenDuration() time.Duration {
	return time.Since(f.openedAt)
}

func (f *TrackedFile) IdleDuration() time.Duration {
	lastAccess := f.lastAccess.Load().(time.Time)
	return time.Since(lastAccess)
}

func (f *TrackedFile) IsClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *TrackedFile) File() *os.File {
	return f.file
}

var _ TrackedResource = (*TrackedFile)(nil)
