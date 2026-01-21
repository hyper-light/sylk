package variants

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type MergeStrategy int

const (
	MergeStrategyAuto MergeStrategy = iota
	MergeStrategyManual
	MergeStrategyOurs
	MergeStrategyTheirs
	MergeStrategyThreeWay
)

var mergeStrategyNames = map[MergeStrategy]string{
	MergeStrategyAuto:     "auto",
	MergeStrategyManual:   "manual",
	MergeStrategyOurs:     "ours",
	MergeStrategyTheirs:   "theirs",
	MergeStrategyThreeWay: "three_way",
}

func (s MergeStrategy) String() string {
	if name, ok := mergeStrategyNames[s]; ok {
		return name
	}
	return "unknown"
}

type ConflictType int

const (
	ConflictTypeContent ConflictType = iota
	ConflictTypeDelete
	ConflictTypeRename
	ConflictTypePermission
	ConflictTypeDirectory
)

var conflictTypeNames = map[ConflictType]string{
	ConflictTypeContent:    "content",
	ConflictTypeDelete:     "delete",
	ConflictTypeRename:     "rename",
	ConflictTypePermission: "permission",
	ConflictTypeDirectory:  "directory",
}

func (t ConflictType) String() string {
	if name, ok := conflictTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

type MergeConflict struct {
	ID           string
	Type         ConflictType
	FilePath     string
	BaseContent  []byte
	OursContent  []byte
	TheirContent []byte
	Description  string
	Resolved     bool
	Resolution   *ConflictResolution
}

type ConflictResolution struct {
	Strategy        ResolutionStrategy
	ResolvedContent []byte
	ResolvedBy      string
	ResolvedAt      time.Time
	Comment         string
}

type ResolutionStrategy int

const (
	ResolutionStrategyAcceptOurs ResolutionStrategy = iota
	ResolutionStrategyAcceptTheirs
	ResolutionStrategyMerge
	ResolutionStrategyCustom
)

var resolutionStrategyNames = map[ResolutionStrategy]string{
	ResolutionStrategyAcceptOurs:   "accept_ours",
	ResolutionStrategyAcceptTheirs: "accept_theirs",
	ResolutionStrategyMerge:        "merge",
	ResolutionStrategyCustom:       "custom",
}

func (s ResolutionStrategy) String() string {
	if name, ok := resolutionStrategyNames[s]; ok {
		return name
	}
	return "unknown"
}

type MergeResult struct {
	Success       bool
	VariantID     VariantID
	MergedFiles   []string
	Conflicts     []*MergeConflict
	UnresolvedCnt int
	StartedAt     time.Time
	CompletedAt   time.Time
	Duration      time.Duration
	Error         error
	VersionIDs    []string
}

type MergeRequest struct {
	VariantID   VariantID
	Strategy    MergeStrategy
	DryRun      bool
	AutoResolve bool
	Resolver    ConflictResolver
	Timeout     time.Duration
}

type ConflictResolver interface {
	CanResolve(conflict *MergeConflict) bool
	Resolve(ctx context.Context, conflict *MergeConflict) (*ConflictResolution, error)
}

type Merger interface {
	MergeToMain(ctx context.Context, variantID VariantID, strategy MergeStrategy) (*MergeResult, error)
	MergeWithRequest(ctx context.Context, req MergeRequest) (*MergeResult, error)
	DetectConflicts(ctx context.Context, variantID VariantID) ([]*MergeConflict, error)
	ResolveConflict(ctx context.Context, conflictID string, resolution *ConflictResolution) error
	AbortMerge(ctx context.Context, variantID VariantID) error
	GetMergeStatus(variantID VariantID) (*MergeStatus, error)
}

type MergeStatus struct {
	VariantID      VariantID
	InProgress     bool
	StartedAt      time.Time
	TotalFiles     int
	ProcessedFiles int
	ConflictCount  int
	ResolvedCount  int
	CurrentFile    string
	Strategy       MergeStrategy
}

type MergerConfig struct {
	MaxConcurrentMerges int
	DefaultTimeout      time.Duration
	AutoResolveEnabled  bool
	ConflictBufferSize  int
}

func DefaultMergerConfig() MergerConfig {
	return MergerConfig{
		MaxConcurrentMerges: 5,
		DefaultTimeout:      10 * time.Minute,
		AutoResolveEnabled:  true,
		ConflictBufferSize:  100,
	}
}

type VFSMerger interface {
	GetModifications(vfs any) ([]FileModification, error)
	ReadFile(vfs any, path string) ([]byte, error)
	WriteFile(vfs any, path string, content []byte) error
	DeleteFile(vfs any, path string) error
	GetMainContent(pipelineID string, path string) ([]byte, error)
	CommitToMain(pipelineID string, modifications []FileModification) ([]string, error)
}

type FileModification struct {
	Path       string
	Operation  FileOperation
	OldContent []byte
	NewContent []byte
	Timestamp  time.Time
}

type FileOperation int

const (
	FileOpCreate FileOperation = iota
	FileOpModify
	FileOpDelete
)

type VariantMerger struct {
	mu            sync.RWMutex
	config        MergerConfig
	registry      Registry
	vfsMerger     VFSMerger
	activeMerges  map[VariantID]*activeMerge
	conflicts     map[string]*MergeConflict
	conflictMu    sync.RWMutex
	resolvers     []ConflictResolver
	mergeCount    atomic.Int64
	conflictCount atomic.Int64
	closed        atomic.Bool
}

type activeMerge struct {
	variantID  VariantID
	status     *MergeStatus
	cancelFunc context.CancelFunc
	startedAt  time.Time
}

func NewMerger(cfg MergerConfig, registry Registry, vfsMerger VFSMerger) *VariantMerger {
	if cfg.MaxConcurrentMerges == 0 {
		cfg = DefaultMergerConfig()
	}

	return &VariantMerger{
		config:       cfg,
		registry:     registry,
		vfsMerger:    vfsMerger,
		activeMerges: make(map[VariantID]*activeMerge),
		conflicts:    make(map[string]*MergeConflict),
		resolvers:    make([]ConflictResolver, 0),
	}
}

func (m *VariantMerger) RegisterResolver(resolver ConflictResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolvers = append(m.resolvers, resolver)
}

func (m *VariantMerger) MergeToMain(ctx context.Context, variantID VariantID, strategy MergeStrategy) (*MergeResult, error) {
	return m.MergeWithRequest(ctx, MergeRequest{
		VariantID:   variantID,
		Strategy:    strategy,
		AutoResolve: m.config.AutoResolveEnabled,
		Timeout:     m.config.DefaultTimeout,
	})
}

func (m *VariantMerger) MergeWithRequest(ctx context.Context, req MergeRequest) (*MergeResult, error) {
	if m.closed.Load() {
		return nil, ErrVariantClosed
	}

	if err := m.checkMergePrereqs(req.VariantID); err != nil {
		return nil, err
	}

	ctx, cancel := m.setupMergeContext(ctx, req)
	defer cancel()

	if err := m.startMerge(req.VariantID, req.Strategy, cancel); err != nil {
		return nil, err
	}
	defer m.finishMerge(req.VariantID)

	return m.executeMerge(ctx, req)
}

func (m *VariantMerger) checkMergePrereqs(variantID VariantID) error {
	variant, err := m.registry.Get(variantID)
	if err != nil {
		return err
	}

	if variant.State != VariantStateComplete {
		return ErrVariantInvalidState
	}

	m.mu.RLock()
	_, inProgress := m.activeMerges[variantID]
	m.mu.RUnlock()

	if inProgress {
		return ErrVariantMergeInProcess
	}

	return nil
}

func (m *VariantMerger) setupMergeContext(ctx context.Context, req MergeRequest) (context.Context, context.CancelFunc) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = m.config.DefaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (m *VariantMerger) startMerge(variantID VariantID, strategy MergeStrategy, cancel context.CancelFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.activeMerges) >= m.config.MaxConcurrentMerges {
		return ErrVariantResourceLimit
	}

	m.activeMerges[variantID] = &activeMerge{
		variantID:  variantID,
		status:     &MergeStatus{VariantID: variantID, InProgress: true, StartedAt: time.Now(), Strategy: strategy},
		cancelFunc: cancel,
		startedAt:  time.Now(),
	}

	m.registry.UpdateState(variantID, VariantStateMerging)
	return nil
}

func (m *VariantMerger) finishMerge(variantID VariantID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeMerges, variantID)
}

func (m *VariantMerger) executeMerge(ctx context.Context, req MergeRequest) (*MergeResult, error) {
	startTime := time.Now()
	result := &MergeResult{
		VariantID: req.VariantID,
		StartedAt: startTime,
		Conflicts: make([]*MergeConflict, 0),
	}

	variant, err := m.registry.Get(req.VariantID)
	if err != nil {
		return m.failMerge(result, err)
	}

	vfs := variant.GetVFS()
	if vfs == nil {
		return m.failMerge(result, fmt.Errorf("variant has no VFS"))
	}

	modifications, err := m.vfsMerger.GetModifications(vfs)
	if err != nil {
		return m.failMerge(result, fmt.Errorf("failed to get modifications: %w", err))
	}

	conflicts, mergeableMods := m.detectAndClassify(ctx, variant.BasePipelineID, modifications)
	result.Conflicts = conflicts

	if len(conflicts) > 0 {
		resolvedConflicts, unresolvedCount := m.attemptResolve(ctx, req, conflicts)
		result.Conflicts = resolvedConflicts
		result.UnresolvedCnt = unresolvedCount

		if unresolvedCount > 0 && req.Strategy != MergeStrategyManual {
			return m.failMerge(result, ErrVariantConflict)
		}
	}

	if !req.DryRun {
		versionIDs, err := m.commitMerge(ctx, variant.BasePipelineID, mergeableMods, result.Conflicts)
		if err != nil {
			return m.failMerge(result, err)
		}
		result.VersionIDs = versionIDs
	}

	result.Success = true
	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)
	result.MergedFiles = extractFilePaths(mergeableMods)

	m.registry.UpdateState(req.VariantID, VariantStateMerged)
	m.mergeCount.Add(1)

	return result, nil
}

func (m *VariantMerger) failMerge(result *MergeResult, err error) (*MergeResult, error) {
	result.Success = false
	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(result.StartedAt)
	result.Error = err

	if result.VariantID != "" {
		m.registry.UpdateState(result.VariantID, VariantStateFailed)
	}

	return result, err
}

func (m *VariantMerger) detectAndClassify(ctx context.Context, basePipelineID string, mods []FileModification) ([]*MergeConflict, []FileModification) {
	conflicts := make([]*MergeConflict, 0)
	mergeable := make([]FileModification, 0, len(mods))

	for _, mod := range mods {
		conflict := m.checkForConflict(ctx, basePipelineID, mod)
		if conflict != nil {
			conflicts = append(conflicts, conflict)
			m.storeConflict(conflict)
		} else {
			mergeable = append(mergeable, mod)
		}
	}

	return conflicts, mergeable
}

func (m *VariantMerger) checkForConflict(ctx context.Context, basePipelineID string, mod FileModification) *MergeConflict {
	if m.vfsMerger == nil {
		return nil
	}

	mainContent, err := m.vfsMerger.GetMainContent(basePipelineID, mod.Path)
	if err != nil {
		return nil
	}

	if mod.OldContent != nil && !bytesEqual(mod.OldContent, mainContent) {
		conflictID := generateConflictID(basePipelineID, mod.Path)
		return &MergeConflict{
			ID:           conflictID,
			Type:         ConflictTypeContent,
			FilePath:     mod.Path,
			BaseContent:  mod.OldContent,
			OursContent:  mod.NewContent,
			TheirContent: mainContent,
			Description:  fmt.Sprintf("concurrent modification of %s", mod.Path),
		}
	}

	return nil
}

func (m *VariantMerger) storeConflict(conflict *MergeConflict) {
	m.conflictMu.Lock()
	defer m.conflictMu.Unlock()
	m.conflicts[conflict.ID] = conflict
	m.conflictCount.Add(1)
}

func (m *VariantMerger) attemptResolve(ctx context.Context, req MergeRequest, conflicts []*MergeConflict) ([]*MergeConflict, int) {
	if !req.AutoResolve {
		return conflicts, len(conflicts)
	}

	unresolvedCount := 0
	for _, conflict := range conflicts {
		if conflict.Resolved {
			continue
		}

		resolution := m.tryAutoResolve(ctx, req, conflict)
		if resolution != nil {
			conflict.Resolved = true
			conflict.Resolution = resolution
		} else {
			unresolvedCount++
		}
	}

	return conflicts, unresolvedCount
}

func (m *VariantMerger) tryAutoResolve(ctx context.Context, req MergeRequest, conflict *MergeConflict) *ConflictResolution {
	if req.Resolver != nil && req.Resolver.CanResolve(conflict) {
		resolution, err := req.Resolver.Resolve(ctx, conflict)
		if err == nil {
			return resolution
		}
	}

	m.mu.RLock()
	resolvers := m.resolvers
	m.mu.RUnlock()

	for _, resolver := range resolvers {
		if resolver.CanResolve(conflict) {
			resolution, err := resolver.Resolve(ctx, conflict)
			if err == nil {
				return resolution
			}
		}
	}

	return m.applyStrategyResolution(req.Strategy, conflict)
}

func (m *VariantMerger) applyStrategyResolution(strategy MergeStrategy, conflict *MergeConflict) *ConflictResolution {
	switch strategy {
	case MergeStrategyOurs:
		return &ConflictResolution{
			Strategy:        ResolutionStrategyAcceptOurs,
			ResolvedContent: conflict.OursContent,
			ResolvedBy:      "auto",
			ResolvedAt:      time.Now(),
			Comment:         "auto-resolved using ours strategy",
		}
	case MergeStrategyTheirs:
		return &ConflictResolution{
			Strategy:        ResolutionStrategyAcceptTheirs,
			ResolvedContent: conflict.TheirContent,
			ResolvedBy:      "auto",
			ResolvedAt:      time.Now(),
			Comment:         "auto-resolved using theirs strategy",
		}
	default:
		return nil
	}
}

func (m *VariantMerger) commitMerge(ctx context.Context, basePipelineID string, mods []FileModification, conflicts []*MergeConflict) ([]string, error) {
	if m.vfsMerger == nil {
		return nil, nil
	}

	allMods := make([]FileModification, 0, len(mods)+len(conflicts))
	allMods = append(allMods, mods...)

	for _, conflict := range conflicts {
		if conflict.Resolved && conflict.Resolution != nil {
			allMods = append(allMods, FileModification{
				Path:       conflict.FilePath,
				Operation:  FileOpModify,
				NewContent: conflict.Resolution.ResolvedContent,
			})
		}
	}

	return m.vfsMerger.CommitToMain(basePipelineID, allMods)
}

func (m *VariantMerger) DetectConflicts(ctx context.Context, variantID VariantID) ([]*MergeConflict, error) {
	if m.closed.Load() {
		return nil, ErrVariantClosed
	}

	variant, err := m.registry.Get(variantID)
	if err != nil {
		return nil, err
	}

	vfs := variant.GetVFS()
	if vfs == nil {
		return nil, fmt.Errorf("variant has no VFS")
	}

	modifications, err := m.vfsMerger.GetModifications(vfs)
	if err != nil {
		return nil, fmt.Errorf("failed to get modifications: %w", err)
	}

	conflicts, _ := m.detectAndClassify(ctx, variant.BasePipelineID, modifications)
	return conflicts, nil
}

func (m *VariantMerger) ResolveConflict(ctx context.Context, conflictID string, resolution *ConflictResolution) error {
	if m.closed.Load() {
		return ErrVariantClosed
	}

	m.conflictMu.Lock()
	defer m.conflictMu.Unlock()

	conflict, exists := m.conflicts[conflictID]
	if !exists {
		return fmt.Errorf("conflict not found: %s", conflictID)
	}

	if conflict.Resolved {
		return fmt.Errorf("conflict already resolved: %s", conflictID)
	}

	conflict.Resolved = true
	conflict.Resolution = resolution

	return nil
}

func (m *VariantMerger) AbortMerge(ctx context.Context, variantID VariantID) error {
	if m.closed.Load() {
		return ErrVariantClosed
	}

	m.mu.Lock()
	merge, exists := m.activeMerges[variantID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active merge for variant: %s", variantID)
	}

	merge.cancelFunc()
	m.finishMerge(variantID)

	m.registry.UpdateState(variantID, VariantStateComplete)

	return nil
}

func (m *VariantMerger) GetMergeStatus(variantID VariantID) (*MergeStatus, error) {
	if m.closed.Load() {
		return nil, ErrVariantClosed
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	merge, exists := m.activeMerges[variantID]
	if !exists {
		return &MergeStatus{VariantID: variantID, InProgress: false}, nil
	}

	return merge.status, nil
}

func (m *VariantMerger) GetConflict(conflictID string) (*MergeConflict, bool) {
	m.conflictMu.RLock()
	defer m.conflictMu.RUnlock()
	conflict, exists := m.conflicts[conflictID]
	return conflict, exists
}

func (m *VariantMerger) ListUnresolvedConflicts(variantID VariantID) []*MergeConflict {
	m.conflictMu.RLock()
	defer m.conflictMu.RUnlock()

	result := make([]*MergeConflict, 0)
	for _, conflict := range m.conflicts {
		if !conflict.Resolved {
			result = append(result, conflict)
		}
	}
	return result
}

func (m *VariantMerger) ClearConflicts(variantID VariantID) {
	m.conflictMu.Lock()
	defer m.conflictMu.Unlock()
	m.conflicts = make(map[string]*MergeConflict)
}

func (m *VariantMerger) Stats() MergerStats {
	m.mu.RLock()
	activeMergeCount := len(m.activeMerges)
	m.mu.RUnlock()

	m.conflictMu.RLock()
	conflictCount := len(m.conflicts)
	m.conflictMu.RUnlock()

	return MergerStats{
		TotalMerges:         m.mergeCount.Load(),
		ActiveMerges:        activeMergeCount,
		TotalConflicts:      m.conflictCount.Load(),
		PendingConflicts:    conflictCount,
		RegisteredResolvers: len(m.resolvers),
	}
}

func (m *VariantMerger) Close() error {
	if m.closed.Swap(true) {
		return nil
	}

	m.mu.Lock()
	for _, merge := range m.activeMerges {
		merge.cancelFunc()
	}
	m.activeMerges = nil
	m.mu.Unlock()

	m.conflictMu.Lock()
	m.conflicts = nil
	m.conflictMu.Unlock()

	return nil
}

type MergerStats struct {
	TotalMerges         int64
	ActiveMerges        int
	TotalConflicts      int64
	PendingConflicts    int
	RegisteredResolvers int
}

func generateConflictID(pipelineID, filePath string) string {
	return fmt.Sprintf("conflict_%s_%s_%d", pipelineID, filePath, time.Now().UnixNano())
}

func extractFilePaths(mods []FileModification) []string {
	paths := make([]string, len(mods))
	for i, mod := range mods {
		paths[i] = mod.Path
	}
	return paths
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type AutoResolver struct {
	strategies map[ConflictType]ResolutionStrategy
}

func NewAutoResolver() *AutoResolver {
	return &AutoResolver{
		strategies: map[ConflictType]ResolutionStrategy{
			ConflictTypePermission: ResolutionStrategyAcceptOurs,
		},
	}
}

func (r *AutoResolver) SetStrategy(conflictType ConflictType, strategy ResolutionStrategy) {
	r.strategies[conflictType] = strategy
}

func (r *AutoResolver) CanResolve(conflict *MergeConflict) bool {
	_, ok := r.strategies[conflict.Type]
	return ok
}

func (r *AutoResolver) Resolve(ctx context.Context, conflict *MergeConflict) (*ConflictResolution, error) {
	strategy, ok := r.strategies[conflict.Type]
	if !ok {
		return nil, fmt.Errorf("no strategy for conflict type: %s", conflict.Type)
	}

	var content []byte
	switch strategy {
	case ResolutionStrategyAcceptOurs:
		content = conflict.OursContent
	case ResolutionStrategyAcceptTheirs:
		content = conflict.TheirContent
	default:
		return nil, fmt.Errorf("unsupported resolution strategy: %s", strategy)
	}

	return &ConflictResolution{
		Strategy:        strategy,
		ResolvedContent: content,
		ResolvedBy:      "auto_resolver",
		ResolvedAt:      time.Now(),
		Comment:         fmt.Sprintf("auto-resolved %s conflict", conflict.Type),
	}, nil
}

type OursResolver struct{}

func NewOursResolver() *OursResolver {
	return &OursResolver{}
}

func (r *OursResolver) CanResolve(conflict *MergeConflict) bool {
	return true
}

func (r *OursResolver) Resolve(ctx context.Context, conflict *MergeConflict) (*ConflictResolution, error) {
	return &ConflictResolution{
		Strategy:        ResolutionStrategyAcceptOurs,
		ResolvedContent: conflict.OursContent,
		ResolvedBy:      "ours_resolver",
		ResolvedAt:      time.Now(),
		Comment:         "resolved by accepting our changes",
	}, nil
}

type TheirsResolver struct{}

func NewTheirsResolver() *TheirsResolver {
	return &TheirsResolver{}
}

func (r *TheirsResolver) CanResolve(conflict *MergeConflict) bool {
	return true
}

func (r *TheirsResolver) Resolve(ctx context.Context, conflict *MergeConflict) (*ConflictResolution, error) {
	return &ConflictResolution{
		Strategy:        ResolutionStrategyAcceptTheirs,
		ResolvedContent: conflict.TheirContent,
		ResolvedBy:      "theirs_resolver",
		ResolvedAt:      time.Now(),
		Comment:         "resolved by accepting their changes",
	}, nil
}
