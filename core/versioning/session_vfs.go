package versioning

import (
	"os"
	"path/filepath"
	"sync"
)

// SessionVFS owns the per-session CVS infrastructure: VFSManager, BlobStore,
// DAGStore, OperationLog, OTEngine, and WriteAheadLog. Each session gets its
// own isolated set of stores. Within a session, each pipeline receives its
// own PipelineVFS allocated via the CVS.
type SessionVFS struct {
	mu sync.Mutex

	sessionID  SessionID
	workingDir string
	cvs        *DefaultCVS
	vfsManager VFSManager
	blobStore  BlobStore
	dagStore   DAGStore
	opLog      OperationLog
	otEngine   OTEngine
	wal        WriteAheadLog
	closed     bool
}

// SessionVFSConfig configures the per-session VFS infrastructure.
type SessionVFSConfig struct {
	SessionID  SessionID
	WorkingDir string
}

// NewSessionVFS creates an isolated set of versioning stores for a session.
func NewSessionVFS(cfg SessionVFSConfig) *SessionVFS {
	blobStore := NewMemoryBlobStore()
	dagStore := NewMemoryDAGStore()
	opLog := NewMemoryOperationLog()
	otEngine := NewOTEngine()
	wal := NewMemoryWAL()

	stagingDir := filepath.Join(os.TempDir(), "sylk", "sessions", string(cfg.SessionID))

	vfsMgr := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir:      stagingDir,
		VersionStore: &dagVersionStoreAdapter{dag: dagStore},
		BlobStore:    blobStore,
	})

	cvs := NewCVS(CVSConfig{
		VFSManager: vfsMgr,
		BlobStore:  blobStore,
		OpLog:      opLog,
		DAGStore:   dagStore,
		WAL:        wal,
		OTEngine:   otEngine,
	})

	return &SessionVFS{
		sessionID:  cfg.SessionID,
		workingDir: cfg.WorkingDir,
		cvs:        cvs,
		vfsManager: vfsMgr,
		blobStore:  blobStore,
		dagStore:   dagStore,
		opLog:      opLog,
		otEngine:   otEngine,
		wal:        wal,
	}
}

// CVS returns the session's collaborative versioning system.
func (s *SessionVFS) CVS() CVS { return s.cvs }

// VFSManager returns the session's VFS manager.
func (s *SessionVFS) VFSManager() VFSManager { return s.vfsManager }

// SessionID returns the session identifier.
func (s *SessionVFS) SessionID() SessionID { return s.sessionID }

// NewDiskFileAccess creates a DiskFileAccess for agents that bypass VFS.
func (s *SessionVFS) NewDiskFileAccess(readOnly bool) FileAccess {
	return NewDiskFileAccess(s.workingDir, readOnly)
}

// NewGlobalFileAccess creates a GlobalVFSFileAccess backed by this
// session's CVS for global agents (Global Inspector, Global Tester).
func (s *SessionVFS) NewGlobalFileAccess(readOnly bool) FileAccess {
	return NewGlobalVFSFileAccess(s.cvs, s.workingDir, readOnly)
}

// NewPipelineFileAccess creates a VFSFileAccess backed by a per-pipeline
// VFS for pipeline agents (Engineer, Designer, Pipeline Inspector/Tester).
func (s *SessionVFS) NewPipelineFileAccess(vfs *PipelineVFS) FileAccess {
	return NewVFSFileAccess(vfs, s.workingDir)
}

// Close shuts down the session's CVS infrastructure and cleans up.
func (s *SessionVFS) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	if err := s.cvs.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.vfsManager.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// dagVersionStoreAdapter adapts a DAGStore to the VersionStore interface
// required by VFSManager and PipelineVFS. DAGStore uses Get/Add while
// VersionStore uses GetVersion/AddVersion.
type dagVersionStoreAdapter struct {
	dag DAGStore
}

func (a *dagVersionStoreAdapter) GetHead(filePath string) (*FileVersion, error) {
	return a.dag.GetHead(filePath)
}

func (a *dagVersionStoreAdapter) GetVersion(id VersionID) (*FileVersion, error) {
	return a.dag.Get(id)
}

func (a *dagVersionStoreAdapter) GetHistory(filePath string, limit int) ([]FileVersion, error) {
	versions, err := a.dag.GetHistory(filePath, limit)
	if err != nil {
		return nil, err
	}
	result := make([]FileVersion, len(versions))
	for i, v := range versions {
		result[i] = *v
	}
	return result, nil
}

func (a *dagVersionStoreAdapter) AddVersion(version FileVersion) error {
	return a.dag.Add(version)
}
