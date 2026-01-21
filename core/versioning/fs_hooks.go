package versioning

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Filesystem Hooks (Wave 9 - FS.9)
// =============================================================================
//
// Hooks for file system lifecycle events:
// - PreFileWriteHook: Before file write (validation)
// - PostFileWriteHook: After file write (indexing)
// - PreFileDeleteHook: Before delete (backup)
// - PostFileDeleteHook: After delete (cleanup)
// - ConflictDetectionHook: Detect conflicts
// - VersionCreatedHook: When new version created

var (
	ErrHookValidationFailed = errors.New("hook validation failed")
	ErrHookBackupFailed     = errors.New("hook backup failed")
	ErrHookIndexingFailed   = errors.New("hook indexing failed")
	ErrHookConflictDetected = errors.New("conflict detected by hook")
	ErrHookCleanupFailed    = errors.New("hook cleanup failed")
)

type FSHookPhase string

const (
	FSHookPhasePreFileWrite   FSHookPhase = "pre_file_write"
	FSHookPhasePostFileWrite  FSHookPhase = "post_file_write"
	FSHookPhasePreFileDelete  FSHookPhase = "pre_file_delete"
	FSHookPhasePostFileDelete FSHookPhase = "post_file_delete"
	FSHookPhaseConflictDetect FSHookPhase = "conflict_detect"
	FSHookPhaseVersionCreated FSHookPhase = "version_created"
)

type FileWriteHookData struct {
	FilePath    string
	Content     []byte
	OldContent  []byte
	SessionID   SessionID
	PipelineID  string
	AgentID     string
	VersionID   VersionID
	OldVersion  VersionID
	ContentHash ContentHash
	Timestamp   time.Time
	Metadata    map[string]any
}

type FileDeleteHookData struct {
	FilePath   string
	Content    []byte
	SessionID  SessionID
	PipelineID string
	AgentID    string
	VersionID  VersionID
	Timestamp  time.Time
	BackupPath string
	Metadata   map[string]any
}

type ConflictDetectionData struct {
	FilePath      string
	LocalVersion  VersionID
	RemoteVersion VersionID
	BaseVersion   VersionID
	LocalContent  []byte
	RemoteContent []byte
	BaseContent   []byte
	SessionID     SessionID
	Conflicts     []*Conflict
	Metadata      map[string]any
}

type VersionCreatedData struct {
	FilePath    string
	VersionID   VersionID
	ParentIDs   []VersionID
	Content     []byte
	ContentHash ContentHash
	SessionID   SessionID
	PipelineID  string
	IsMerge     bool
	Timestamp   time.Time
	Metadata    map[string]any
}

type FSHookResult struct {
	Continue        bool
	ModifiedContent []byte
	Error           error
	SkipExecution   bool
	SkipResponse    string
	BackupCreated   bool
	BackupPath      string
	IndexUpdated    bool
	ConflictsFound  []*Conflict
}

type PreFileWriteHook interface {
	Name() string
	Priority() skills.HookPriority
	Execute(ctx context.Context, data *FileWriteHookData) FSHookResult
}

type PostFileWriteHook interface {
	Name() string
	Priority() skills.HookPriority
	Execute(ctx context.Context, data *FileWriteHookData) FSHookResult
}

type PreFileDeleteHook interface {
	Name() string
	Priority() skills.HookPriority
	Execute(ctx context.Context, data *FileDeleteHookData) FSHookResult
}

type PostFileDeleteHook interface {
	Name() string
	Priority() skills.HookPriority
	Execute(ctx context.Context, data *FileDeleteHookData) FSHookResult
}

type ConflictDetectionHook interface {
	Name() string
	Priority() skills.HookPriority
	Execute(ctx context.Context, data *ConflictDetectionData) FSHookResult
}

type VersionCreatedHook interface {
	Name() string
	Priority() skills.HookPriority
	Execute(ctx context.Context, data *VersionCreatedData) FSHookResult
}

type PreFileWriteHookFunc func(ctx context.Context, data *FileWriteHookData) FSHookResult
type PostFileWriteHookFunc func(ctx context.Context, data *FileWriteHookData) FSHookResult
type PreFileDeleteHookFunc func(ctx context.Context, data *FileDeleteHookData) FSHookResult
type PostFileDeleteHookFunc func(ctx context.Context, data *FileDeleteHookData) FSHookResult
type ConflictDetectionHookFunc func(ctx context.Context, data *ConflictDetectionData) FSHookResult
type VersionCreatedHookFunc func(ctx context.Context, data *VersionCreatedData) FSHookResult

type funcPreFileWriteHook struct {
	name     string
	priority skills.HookPriority
	fn       PreFileWriteHookFunc
}

func (h *funcPreFileWriteHook) Name() string                  { return h.name }
func (h *funcPreFileWriteHook) Priority() skills.HookPriority { return h.priority }
func (h *funcPreFileWriteHook) Execute(ctx context.Context, data *FileWriteHookData) FSHookResult {
	return h.fn(ctx, data)
}

type funcPostFileWriteHook struct {
	name     string
	priority skills.HookPriority
	fn       PostFileWriteHookFunc
}

func (h *funcPostFileWriteHook) Name() string                  { return h.name }
func (h *funcPostFileWriteHook) Priority() skills.HookPriority { return h.priority }
func (h *funcPostFileWriteHook) Execute(ctx context.Context, data *FileWriteHookData) FSHookResult {
	return h.fn(ctx, data)
}

type funcPreFileDeleteHook struct {
	name     string
	priority skills.HookPriority
	fn       PreFileDeleteHookFunc
}

func (h *funcPreFileDeleteHook) Name() string                  { return h.name }
func (h *funcPreFileDeleteHook) Priority() skills.HookPriority { return h.priority }
func (h *funcPreFileDeleteHook) Execute(ctx context.Context, data *FileDeleteHookData) FSHookResult {
	return h.fn(ctx, data)
}

type funcPostFileDeleteHook struct {
	name     string
	priority skills.HookPriority
	fn       PostFileDeleteHookFunc
}

func (h *funcPostFileDeleteHook) Name() string                  { return h.name }
func (h *funcPostFileDeleteHook) Priority() skills.HookPriority { return h.priority }
func (h *funcPostFileDeleteHook) Execute(ctx context.Context, data *FileDeleteHookData) FSHookResult {
	return h.fn(ctx, data)
}

type funcConflictDetectionHook struct {
	name     string
	priority skills.HookPriority
	fn       ConflictDetectionHookFunc
}

func (h *funcConflictDetectionHook) Name() string                  { return h.name }
func (h *funcConflictDetectionHook) Priority() skills.HookPriority { return h.priority }
func (h *funcConflictDetectionHook) Execute(ctx context.Context, data *ConflictDetectionData) FSHookResult {
	return h.fn(ctx, data)
}

type funcVersionCreatedHook struct {
	name     string
	priority skills.HookPriority
	fn       VersionCreatedHookFunc
}

func (h *funcVersionCreatedHook) Name() string                  { return h.name }
func (h *funcVersionCreatedHook) Priority() skills.HookPriority { return h.priority }
func (h *funcVersionCreatedHook) Execute(ctx context.Context, data *VersionCreatedData) FSHookResult {
	return h.fn(ctx, data)
}

type FSHookRegistry struct {
	mu sync.RWMutex

	preFileWriteHooks   []PreFileWriteHook
	postFileWriteHooks  []PostFileWriteHook
	preFileDeleteHooks  []PreFileDeleteHook
	postFileDeleteHooks []PostFileDeleteHook
	conflictDetectHooks []ConflictDetectionHook
	versionCreatedHooks []VersionCreatedHook

	hooksByName map[string]any
}

func NewFSHookRegistry() *FSHookRegistry {
	return &FSHookRegistry{
		preFileWriteHooks:   make([]PreFileWriteHook, 0),
		postFileWriteHooks:  make([]PostFileWriteHook, 0),
		preFileDeleteHooks:  make([]PreFileDeleteHook, 0),
		postFileDeleteHooks: make([]PostFileDeleteHook, 0),
		conflictDetectHooks: make([]ConflictDetectionHook, 0),
		versionCreatedHooks: make([]VersionCreatedHook, 0),
		hooksByName:         make(map[string]any),
	}
}

func (r *FSHookRegistry) RegisterPreFileWriteHook(hook PreFileWriteHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooksByName[hook.Name()] = hook
	r.preFileWriteHooks = insertPreFileWriteHookSorted(r.preFileWriteHooks, hook)
}

func (r *FSHookRegistry) RegisterPostFileWriteHook(hook PostFileWriteHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooksByName[hook.Name()] = hook
	r.postFileWriteHooks = insertPostFileWriteHookSorted(r.postFileWriteHooks, hook)
}

func (r *FSHookRegistry) RegisterPreFileDeleteHook(hook PreFileDeleteHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooksByName[hook.Name()] = hook
	r.preFileDeleteHooks = insertPreFileDeleteHookSorted(r.preFileDeleteHooks, hook)
}

func (r *FSHookRegistry) RegisterPostFileDeleteHook(hook PostFileDeleteHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooksByName[hook.Name()] = hook
	r.postFileDeleteHooks = insertPostFileDeleteHookSorted(r.postFileDeleteHooks, hook)
}

func (r *FSHookRegistry) RegisterConflictDetectionHook(hook ConflictDetectionHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooksByName[hook.Name()] = hook
	r.conflictDetectHooks = insertConflictDetectHookSorted(r.conflictDetectHooks, hook)
}

func (r *FSHookRegistry) RegisterVersionCreatedHook(hook VersionCreatedHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooksByName[hook.Name()] = hook
	r.versionCreatedHooks = insertVersionCreatedHookSorted(r.versionCreatedHooks, hook)
}

func (r *FSHookRegistry) RegisterPreFileWriteHookFunc(name string, priority skills.HookPriority, fn PreFileWriteHookFunc) {
	r.RegisterPreFileWriteHook(&funcPreFileWriteHook{
		name:     name,
		priority: priority,
		fn:       fn,
	})
}

func (r *FSHookRegistry) RegisterPostFileWriteHookFunc(name string, priority skills.HookPriority, fn PostFileWriteHookFunc) {
	r.RegisterPostFileWriteHook(&funcPostFileWriteHook{
		name:     name,
		priority: priority,
		fn:       fn,
	})
}

func (r *FSHookRegistry) RegisterPreFileDeleteHookFunc(name string, priority skills.HookPriority, fn PreFileDeleteHookFunc) {
	r.RegisterPreFileDeleteHook(&funcPreFileDeleteHook{
		name:     name,
		priority: priority,
		fn:       fn,
	})
}

func (r *FSHookRegistry) RegisterPostFileDeleteHookFunc(name string, priority skills.HookPriority, fn PostFileDeleteHookFunc) {
	r.RegisterPostFileDeleteHook(&funcPostFileDeleteHook{
		name:     name,
		priority: priority,
		fn:       fn,
	})
}

func (r *FSHookRegistry) RegisterConflictDetectionHookFunc(name string, priority skills.HookPriority, fn ConflictDetectionHookFunc) {
	r.RegisterConflictDetectionHook(&funcConflictDetectionHook{
		name:     name,
		priority: priority,
		fn:       fn,
	})
}

func (r *FSHookRegistry) RegisterVersionCreatedHookFunc(name string, priority skills.HookPriority, fn VersionCreatedHookFunc) {
	r.RegisterVersionCreatedHook(&funcVersionCreatedHook{
		name:     name,
		priority: priority,
		fn:       fn,
	})
}

func (r *FSHookRegistry) UnregisterHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.hooksByName[name]
	if !ok {
		return false
	}

	delete(r.hooksByName, name)

	switch h := hook.(type) {
	case PreFileWriteHook:
		r.preFileWriteHooks = removePreFileWriteHook(r.preFileWriteHooks, h.Name())
	case PostFileWriteHook:
		r.postFileWriteHooks = removePostFileWriteHook(r.postFileWriteHooks, h.Name())
	case PreFileDeleteHook:
		r.preFileDeleteHooks = removePreFileDeleteHook(r.preFileDeleteHooks, h.Name())
	case PostFileDeleteHook:
		r.postFileDeleteHooks = removePostFileDeleteHook(r.postFileDeleteHooks, h.Name())
	case ConflictDetectionHook:
		r.conflictDetectHooks = removeConflictDetectHook(r.conflictDetectHooks, h.Name())
	case VersionCreatedHook:
		r.versionCreatedHooks = removeVersionCreatedHook(r.versionCreatedHooks, h.Name())
	}

	return true
}

func (r *FSHookRegistry) ExecutePreFileWriteHooks(ctx context.Context, data *FileWriteHookData) (*FileWriteHookData, error) {
	r.mu.RLock()
	hooks := make([]PreFileWriteHook, len(r.preFileWriteHooks))
	copy(hooks, r.preFileWriteHooks)
	r.mu.RUnlock()

	current := data
	for _, hook := range hooks {
		if err := checkContext(ctx); err != nil {
			return current, err
		}

		result := hook.Execute(ctx, current)
		if result.Error != nil {
			return current, result.Error
		}

		if result.ModifiedContent != nil {
			current.Content = result.ModifiedContent
		}

		if result.SkipExecution {
			return current, nil
		}

		if !result.Continue {
			break
		}
	}

	return current, nil
}

func (r *FSHookRegistry) ExecutePostFileWriteHooks(ctx context.Context, data *FileWriteHookData) (*FileWriteHookData, error) {
	r.mu.RLock()
	hooks := make([]PostFileWriteHook, len(r.postFileWriteHooks))
	copy(hooks, r.postFileWriteHooks)
	r.mu.RUnlock()

	current := data
	for _, hook := range hooks {
		if err := checkContext(ctx); err != nil {
			return current, err
		}

		result := hook.Execute(ctx, current)
		if result.Error != nil {
			return current, result.Error
		}

		if !result.Continue {
			break
		}
	}

	return current, nil
}

func (r *FSHookRegistry) ExecutePreFileDeleteHooks(ctx context.Context, data *FileDeleteHookData) (*FileDeleteHookData, FSHookResult, error) {
	r.mu.RLock()
	hooks := make([]PreFileDeleteHook, len(r.preFileDeleteHooks))
	copy(hooks, r.preFileDeleteHooks)
	r.mu.RUnlock()

	current := data
	lastResult := FSHookResult{Continue: true}

	for _, hook := range hooks {
		if err := checkContext(ctx); err != nil {
			return current, lastResult, err
		}

		result := hook.Execute(ctx, current)
		lastResult = result

		if result.Error != nil {
			return current, result, result.Error
		}

		if result.BackupCreated {
			current.BackupPath = result.BackupPath
		}

		if result.SkipExecution {
			return current, result, nil
		}

		if !result.Continue {
			break
		}
	}

	return current, lastResult, nil
}

func (r *FSHookRegistry) ExecutePostFileDeleteHooks(ctx context.Context, data *FileDeleteHookData) (*FileDeleteHookData, error) {
	r.mu.RLock()
	hooks := make([]PostFileDeleteHook, len(r.postFileDeleteHooks))
	copy(hooks, r.postFileDeleteHooks)
	r.mu.RUnlock()

	current := data
	for _, hook := range hooks {
		if err := checkContext(ctx); err != nil {
			return current, err
		}

		result := hook.Execute(ctx, current)
		if result.Error != nil {
			return current, result.Error
		}

		if !result.Continue {
			break
		}
	}

	return current, nil
}

func (r *FSHookRegistry) ExecuteConflictDetectionHooks(ctx context.Context, data *ConflictDetectionData) ([]*Conflict, error) {
	r.mu.RLock()
	hooks := make([]ConflictDetectionHook, len(r.conflictDetectHooks))
	copy(hooks, r.conflictDetectHooks)
	r.mu.RUnlock()

	allConflicts := make([]*Conflict, 0)

	for _, hook := range hooks {
		if err := checkContext(ctx); err != nil {
			return allConflicts, err
		}

		result := hook.Execute(ctx, data)
		if result.Error != nil {
			return allConflicts, result.Error
		}

		if len(result.ConflictsFound) > 0 {
			allConflicts = append(allConflicts, result.ConflictsFound...)
		}

		if !result.Continue {
			break
		}
	}

	return allConflicts, nil
}

func (r *FSHookRegistry) ExecuteVersionCreatedHooks(ctx context.Context, data *VersionCreatedData) error {
	r.mu.RLock()
	hooks := make([]VersionCreatedHook, len(r.versionCreatedHooks))
	copy(hooks, r.versionCreatedHooks)
	r.mu.RUnlock()

	for _, hook := range hooks {
		if err := checkContext(ctx); err != nil {
			return err
		}

		result := hook.Execute(ctx, data)
		if result.Error != nil {
			return result.Error
		}

		if !result.Continue {
			break
		}
	}

	return nil
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func insertPreFileWriteHookSorted(hooks []PreFileWriteHook, hook PreFileWriteHook) []PreFileWriteHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertPostFileWriteHookSorted(hooks []PostFileWriteHook, hook PostFileWriteHook) []PostFileWriteHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertPreFileDeleteHookSorted(hooks []PreFileDeleteHook, hook PreFileDeleteHook) []PreFileDeleteHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertPostFileDeleteHookSorted(hooks []PostFileDeleteHook, hook PostFileDeleteHook) []PostFileDeleteHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertConflictDetectHookSorted(hooks []ConflictDetectionHook, hook ConflictDetectionHook) []ConflictDetectionHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertVersionCreatedHookSorted(hooks []VersionCreatedHook, hook VersionCreatedHook) []VersionCreatedHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func removePreFileWriteHook(hooks []PreFileWriteHook, name string) []PreFileWriteHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removePostFileWriteHook(hooks []PostFileWriteHook, name string) []PostFileWriteHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removePreFileDeleteHook(hooks []PreFileDeleteHook, name string) []PreFileDeleteHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removePostFileDeleteHook(hooks []PostFileDeleteHook, name string) []PostFileDeleteHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removeConflictDetectHook(hooks []ConflictDetectionHook, name string) []ConflictDetectionHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removeVersionCreatedHook(hooks []VersionCreatedHook, name string) []VersionCreatedHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

type FSHookStats struct {
	PreFileWriteHooks   int `json:"pre_file_write_hooks"`
	PostFileWriteHooks  int `json:"post_file_write_hooks"`
	PreFileDeleteHooks  int `json:"pre_file_delete_hooks"`
	PostFileDeleteHooks int `json:"post_file_delete_hooks"`
	ConflictDetectHooks int `json:"conflict_detect_hooks"`
	VersionCreatedHooks int `json:"version_created_hooks"`
	TotalHooks          int `json:"total_hooks"`
}

func (r *FSHookRegistry) Stats() FSHookStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return FSHookStats{
		PreFileWriteHooks:   len(r.preFileWriteHooks),
		PostFileWriteHooks:  len(r.postFileWriteHooks),
		PreFileDeleteHooks:  len(r.preFileDeleteHooks),
		PostFileDeleteHooks: len(r.postFileDeleteHooks),
		ConflictDetectHooks: len(r.conflictDetectHooks),
		VersionCreatedHooks: len(r.versionCreatedHooks),
		TotalHooks:          len(r.hooksByName),
	}
}

func NewValidationHook(name string, validateFn func(data *FileWriteHookData) error) PreFileWriteHook {
	return &funcPreFileWriteHook{
		name:     name,
		priority: skills.HookPriorityHigh,
		fn: func(ctx context.Context, data *FileWriteHookData) FSHookResult {
			if err := validateFn(data); err != nil {
				return FSHookResult{
					Continue: false,
					Error:    err,
				}
			}
			return FSHookResult{Continue: true}
		},
	}
}

func NewIndexingHook(name string, indexFn func(data *FileWriteHookData) error) PostFileWriteHook {
	return &funcPostFileWriteHook{
		name:     name,
		priority: skills.HookPriorityNormal,
		fn: func(ctx context.Context, data *FileWriteHookData) FSHookResult {
			if err := indexFn(data); err != nil {
				return FSHookResult{
					Continue:     true,
					Error:        nil,
					IndexUpdated: false,
				}
			}
			return FSHookResult{Continue: true, IndexUpdated: true}
		},
	}
}

func NewBackupHook(name string, backupFn func(data *FileDeleteHookData) (string, error)) PreFileDeleteHook {
	return &funcPreFileDeleteHook{
		name:     name,
		priority: skills.HookPriorityHigh,
		fn: func(ctx context.Context, data *FileDeleteHookData) FSHookResult {
			backupPath, err := backupFn(data)
			if err != nil {
				return FSHookResult{
					Continue:      false,
					Error:         err,
					BackupCreated: false,
				}
			}
			return FSHookResult{
				Continue:      true,
				BackupCreated: true,
				BackupPath:    backupPath,
			}
		},
	}
}

func NewCleanupHook(name string, cleanupFn func(data *FileDeleteHookData) error) PostFileDeleteHook {
	return &funcPostFileDeleteHook{
		name:     name,
		priority: skills.HookPriorityLow,
		fn: func(ctx context.Context, data *FileDeleteHookData) FSHookResult {
			if err := cleanupFn(data); err != nil {
				return FSHookResult{Continue: true}
			}
			return FSHookResult{Continue: true}
		},
	}
}

func NewConflictDetector(name string, otEngine OTEngine) ConflictDetectionHook {
	return &funcConflictDetectionHook{
		name:     name,
		priority: skills.HookPriorityHigh,
		fn: func(ctx context.Context, data *ConflictDetectionData) FSHookResult {
			if otEngine == nil {
				return FSHookResult{Continue: true}
			}

			conflicts := make([]*Conflict, 0)

			localOp := &Operation{
				FilePath: data.FilePath,
				Content:  data.LocalContent,
			}
			remoteOp := &Operation{
				FilePath: data.FilePath,
				Content:  data.RemoteContent,
			}

			if conflict := otEngine.DetectConflict(localOp, remoteOp); conflict != nil {
				conflicts = append(conflicts, conflict)
			}

			return FSHookResult{
				Continue:       true,
				ConflictsFound: conflicts,
			}
		},
	}
}

func NewVersionNotificationHook(name string, notifyFn func(data *VersionCreatedData)) VersionCreatedHook {
	return &funcVersionCreatedHook{
		name:     name,
		priority: skills.HookPriorityLow,
		fn: func(ctx context.Context, data *VersionCreatedData) FSHookResult {
			notifyFn(data)
			return FSHookResult{Continue: true}
		},
	}
}
