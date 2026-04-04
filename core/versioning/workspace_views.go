package versioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// WorkspaceView identifies one logical filesystem layer.
type WorkspaceView string

const (
	WorkspaceViewDisk     WorkspaceView = "disk"
	WorkspaceViewGlobal   WorkspaceView = "global"
	WorkspaceViewPipeline WorkspaceView = "pipeline"
)

// WorkspaceViewAccess provides explicit read-only access to the committed disk
// state, the session-global merged overlay, and an optional task-local
// pipeline overlay.
type WorkspaceViewAccess interface {
	ReadFile(ctx context.Context, view WorkspaceView, path string, pipelineID string) ([]byte, error)
	Glob(ctx context.Context, view WorkspaceView, root, pattern string, exclude []string, pipelineID string) ([]string, error)
	Grep(ctx context.Context, view WorkspaceView, root, pattern, include string, contextLines, maxMatches int, pipelineID string) ([]GrepMatch, error)
	InspectPath(ctx context.Context, path string, pipelineID string) (*WorkspacePathState, error)
	SummarizePaths(ctx context.Context, paths []string, pipelineID string) (*WorkspaceSummary, error)
	DefaultView() WorkspaceView
}

type sessionWorkspaceViewResolver interface {
	ResolveSession(ctx context.Context) *SessionVFS
}

type workspaceViewDelegate interface {
	WorkspaceViewsDelegate() WorkspaceViewAccess
}

// WorkspacePathState summarizes a single path across the available workspace
// layers so agents can reason about committed disk state versus in-flight
// overlay state explicitly.
type WorkspacePathState struct {
	Path                        string               `json:"path"`
	PipelineID                  string               `json:"pipeline_id,omitempty"`
	DefaultView                 WorkspaceView        `json:"default_view"`
	SourceOfTruth               WorkspaceView        `json:"source_of_truth"`
	ViewsAvailable              []WorkspaceView      `json:"views_available"`
	ViewsUnavailable            []WorkspaceView      `json:"views_unavailable,omitempty"`
	Disk                        WorkspaceLayerState  `json:"disk"`
	Global                      *WorkspaceLayerState `json:"global,omitempty"`
	Pipeline                    *WorkspaceLayerState `json:"pipeline,omitempty"`
	GlobalDiffersFromDisk       bool                 `json:"global_differs_from_disk"`
	GlobalDiffKnown             bool                 `json:"global_diff_known"`
	PipelineDiffersFromDisk     bool                 `json:"pipeline_differs_from_disk"`
	PipelineDiffFromDiskKnown   bool                 `json:"pipeline_diff_from_disk_known"`
	PipelineDiffersFromGlobal   bool                 `json:"pipeline_differs_from_global"`
	PipelineDiffFromGlobalKnown bool                 `json:"pipeline_diff_from_global_known"`
}

// WorkspaceLayerState captures existence and content identity for a single view.
type WorkspaceLayerState struct {
	View        WorkspaceView `json:"view"`
	Available   bool          `json:"available"`
	Exists      bool          `json:"exists"`
	IsDir       bool          `json:"is_dir,omitempty"`
	SizeBytes   int           `json:"size_bytes,omitempty"`
	ContentHash string        `json:"content_hash,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// WorkspaceSummary provides a bounded multi-path view of disk, session-global,
// and task-local state so agents can reason over the task surface without
// stitching together many single-path inspections.
type WorkspaceSummary struct {
	DefaultView               WorkspaceView        `json:"default_view"`
	SourceOfTruth             WorkspaceView        `json:"source_of_truth"`
	PipelineID                string               `json:"pipeline_id,omitempty"`
	Paths                     []string             `json:"paths"`
	ViewsAvailable            []WorkspaceView      `json:"views_available"`
	ViewsUnavailable          []WorkspaceView      `json:"views_unavailable,omitempty"`
	GlobalChangedPaths        []string             `json:"global_changed_paths,omitempty"`
	PipelineChangedPaths      []string             `json:"pipeline_changed_paths,omitempty"`
	PipelineChangedFromGlobal []string             `json:"pipeline_changed_from_global_paths,omitempty"`
	PathsWithUnavailableViews []string             `json:"paths_with_unavailable_views,omitempty"`
	PathsWithErrors           []string             `json:"paths_with_errors,omitempty"`
	Entries                   []WorkspacePathState `json:"entries,omitempty"`
}

// SessionWorkspaceViews resolves workspace layers against the active session.
// It keeps disk as an explicit committed source-of-truth view while still
// exposing the merged session overlay and task-local pipeline overlay.
type SessionWorkspaceViews struct {
	defaultView       WorkspaceView
	defaultPipelineID string
	defaultSessionID  string
	workingDir        string
	sessionLookup     func(sessionID string) *SessionVFS
	session           *SessionVFS
	diskFallback      FileAccess
}

// SessionWorkspaceViewsConfig configures a workspace-view resolver.
type SessionWorkspaceViewsConfig struct {
	DefaultView       WorkspaceView
	DefaultPipelineID string
	DefaultSessionID  string
	WorkingDir        string
	SessionLookup     func(sessionID string) *SessionVFS
	Session           *SessionVFS
	DiskFallback      FileAccess
}

// NewSessionWorkspaceViews creates a workspace-view resolver. Disk access
// always remains available even when no SessionVFS is active.
func NewSessionWorkspaceViews(cfg SessionWorkspaceViewsConfig) *SessionWorkspaceViews {
	defaultView := cfg.DefaultView
	if defaultView == "" {
		defaultView = WorkspaceViewDisk
	}
	diskFallback := cfg.DiskFallback
	if diskFallback == nil {
		diskFallback = NewDiskFileAccess(cfg.WorkingDir, true)
	}
	return &SessionWorkspaceViews{
		defaultView:       defaultView,
		defaultPipelineID: strings.TrimSpace(cfg.DefaultPipelineID),
		defaultSessionID:  strings.TrimSpace(cfg.DefaultSessionID),
		workingDir:        cfg.WorkingDir,
		sessionLookup:     cfg.SessionLookup,
		session:           cfg.Session,
		diskFallback:      diskFallback,
	}
}

func (s *SessionWorkspaceViews) DefaultView() WorkspaceView {
	if s == nil || s.defaultView == "" {
		return WorkspaceViewDisk
	}
	return s.defaultView
}

func (s *SessionWorkspaceViews) ReadFile(ctx context.Context, view WorkspaceView, path string, pipelineID string) ([]byte, error) {
	fa, err := s.fileAccessFor(ctx, view, pipelineID)
	if err != nil {
		return nil, err
	}
	return fa.ReadFile(ctx, path)
}

func (s *SessionWorkspaceViews) Glob(ctx context.Context, view WorkspaceView, root, pattern string, exclude []string, pipelineID string) ([]string, error) {
	fa, err := s.fileAccessFor(ctx, view, pipelineID)
	if err != nil {
		return nil, err
	}
	searchRoot := root
	if strings.TrimSpace(searchRoot) == "" {
		searchRoot = fa.WorkingDir()
	}
	if strings.TrimSpace(searchRoot) == "" {
		searchRoot = "."
	}
	return fa.Glob(ctx, searchRoot, pattern, exclude)
}

func (s *SessionWorkspaceViews) Grep(
	ctx context.Context,
	view WorkspaceView,
	root, pattern, include string,
	contextLines, maxMatches int,
	pipelineID string,
) ([]GrepMatch, error) {
	fa, err := s.fileAccessFor(ctx, view, pipelineID)
	if err != nil {
		return nil, err
	}
	searchRoot := root
	if strings.TrimSpace(searchRoot) == "" {
		searchRoot = fa.WorkingDir()
	}
	if strings.TrimSpace(searchRoot) == "" {
		searchRoot = "."
	}
	return fa.Grep(ctx, searchRoot, pattern, include, contextLines, maxMatches)
}

func (s *SessionWorkspaceViews) InspectPath(ctx context.Context, path string, pipelineID string) (*WorkspacePathState, error) {
	accesses, err := s.resolveAccesses(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	return inspectWorkspacePath(ctx, path, s.DefaultView(), accesses), nil
}

func (s *SessionWorkspaceViews) SummarizePaths(ctx context.Context, paths []string, pipelineID string) (*WorkspaceSummary, error) {
	accesses, err := s.resolveAccesses(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	normalized := normalizeWorkspacePaths(paths)
	summary := &WorkspaceSummary{
		DefaultView:               s.DefaultView(),
		SourceOfTruth:             WorkspaceViewDisk,
		PipelineID:                accesses.pipelineID,
		Paths:                     normalized,
		ViewsAvailable:            append([]WorkspaceView(nil), accesses.viewsAvailable...),
		ViewsUnavailable:          append([]WorkspaceView(nil), accesses.viewsUnavailable...),
		Entries:                   make([]WorkspacePathState, 0, len(normalized)),
		GlobalChangedPaths:        []string{},
		PipelineChangedPaths:      []string{},
		PipelineChangedFromGlobal: []string{},
	}
	for _, path := range normalized {
		state := inspectWorkspacePath(ctx, path, s.DefaultView(), accesses)
		summary.Entries = append(summary.Entries, *state)
		if len(state.ViewsUnavailable) > 0 {
			summary.PathsWithUnavailableViews = append(summary.PathsWithUnavailableViews, path)
		}
		if pathStateHasErrors(state) {
			summary.PathsWithErrors = append(summary.PathsWithErrors, path)
		}
		if state.GlobalDiffKnown && state.GlobalDiffersFromDisk {
			summary.GlobalChangedPaths = append(summary.GlobalChangedPaths, path)
		}
		if state.PipelineDiffFromDiskKnown && state.PipelineDiffersFromDisk {
			summary.PipelineChangedPaths = append(summary.PipelineChangedPaths, path)
		}
		if state.PipelineDiffFromGlobalKnown && state.PipelineDiffersFromGlobal {
			summary.PipelineChangedFromGlobal = append(summary.PipelineChangedFromGlobal, path)
		}
	}
	summary.PathsWithUnavailableViews = uniqueWorkspacePaths(summary.PathsWithUnavailableViews)
	summary.PathsWithErrors = uniqueWorkspacePaths(summary.PathsWithErrors)
	summary.GlobalChangedPaths = uniqueWorkspacePaths(summary.GlobalChangedPaths)
	summary.PipelineChangedPaths = uniqueWorkspacePaths(summary.PipelineChangedPaths)
	summary.PipelineChangedFromGlobal = uniqueWorkspacePaths(summary.PipelineChangedFromGlobal)
	return summary, nil
}

type resolvedWorkspaceAccesses struct {
	pipelineID       string
	diskFA           FileAccess
	globalFA         FileAccess
	pipelineFA       FileAccess
	globalErr        error
	pipelineErr      error
	viewsAvailable   []WorkspaceView
	viewsUnavailable []WorkspaceView
}

func (s *SessionWorkspaceViews) resolveAccesses(ctx context.Context, pipelineID string) (*resolvedWorkspaceAccesses, error) {
	diskFA, err := s.fileAccessFor(ctx, WorkspaceViewDisk, "")
	if err != nil {
		return nil, err
	}
	accesses := &resolvedWorkspaceAccesses{
		pipelineID:     s.resolvePipelineID(pipelineID),
		diskFA:         diskFA,
		viewsAvailable: []WorkspaceView{WorkspaceViewDisk},
	}
	if globalFA, globalErr := s.fileAccessFor(ctx, WorkspaceViewGlobal, ""); globalErr == nil {
		accesses.globalFA = globalFA
		accesses.viewsAvailable = append(accesses.viewsAvailable, WorkspaceViewGlobal)
	} else {
		accesses.globalErr = globalErr
		accesses.viewsUnavailable = append(accesses.viewsUnavailable, WorkspaceViewGlobal)
	}
	if accesses.pipelineID != "" {
		if pipelineFA, pipeErr := s.fileAccessFor(ctx, WorkspaceViewPipeline, accesses.pipelineID); pipeErr == nil {
			accesses.pipelineFA = pipelineFA
			accesses.viewsAvailable = append(accesses.viewsAvailable, WorkspaceViewPipeline)
		} else {
			accesses.pipelineErr = pipeErr
			accesses.viewsUnavailable = append(accesses.viewsUnavailable, WorkspaceViewPipeline)
		}
	}
	return accesses, nil
}

func inspectWorkspacePath(
	ctx context.Context,
	path string,
	defaultView WorkspaceView,
	accesses *resolvedWorkspaceAccesses,
) *WorkspacePathState {
	status := &WorkspacePathState{
		Path:             path,
		DefaultView:      defaultView,
		SourceOfTruth:    WorkspaceViewDisk,
		ViewsAvailable:   append([]WorkspaceView(nil), accesses.viewsAvailable...),
		ViewsUnavailable: append([]WorkspaceView(nil), accesses.viewsUnavailable...),
	}
	if accesses != nil {
		status.PipelineID = accesses.pipelineID
	}
	diskState := inspectWorkspaceLayer(ctx, accesses.diskFA, WorkspaceViewDisk, path)
	status.Disk = diskState
	if accesses.globalFA != nil {
		globalState := inspectWorkspaceLayer(ctx, accesses.globalFA, WorkspaceViewGlobal, path)
		status.Global = &globalState
		status.GlobalDiffersFromDisk, status.GlobalDiffKnown = compareWorkspaceLayers(diskState, globalState)
	} else if accesses.globalErr != nil {
		globalState := workspaceLayerErrorState(WorkspaceViewGlobal, accesses.globalErr)
		status.Global = &globalState
	}
	if accesses.pipelineID != "" {
		if accesses.pipelineFA != nil {
			pipelineState := inspectWorkspaceLayer(ctx, accesses.pipelineFA, WorkspaceViewPipeline, path)
			status.Pipeline = &pipelineState
			status.PipelineDiffersFromDisk, status.PipelineDiffFromDiskKnown = compareWorkspaceLayers(diskState, pipelineState)
			if status.Global != nil {
				status.PipelineDiffersFromGlobal, status.PipelineDiffFromGlobalKnown = compareWorkspaceLayers(*status.Global, pipelineState)
			}
		} else if accesses.pipelineErr != nil {
			pipelineState := workspaceLayerErrorState(WorkspaceViewPipeline, accesses.pipelineErr)
			status.Pipeline = &pipelineState
		}
	}
	return status
}

func (s *SessionWorkspaceViews) fileAccessFor(ctx context.Context, view WorkspaceView, pipelineID string) (FileAccess, error) {
	switch view {
	case WorkspaceViewDisk:
		if svfs := s.resolveSession(ctx); svfs != nil {
			return svfs.NewDiskFileAccess(true), nil
		}
		if s.diskFallback != nil {
			return s.diskFallback, nil
		}
		return NewDiskFileAccess(s.workingDir, true), nil
	case WorkspaceViewGlobal:
		svfs := s.resolveSession(ctx)
		if svfs == nil {
			return nil, fmt.Errorf("workspace view %q unavailable: no active session VFS", view)
		}
		return svfs.NewGlobalFileAccess(true), nil
	case WorkspaceViewPipeline:
		svfs := s.resolveSession(ctx)
		if svfs == nil {
			return nil, fmt.Errorf("workspace view %q unavailable: no active session VFS", view)
		}
		resolvedPipelineID := s.resolvePipelineID(pipelineID)
		if resolvedPipelineID == "" {
			return nil, fmt.Errorf("workspace view %q requires pipeline_id", view)
		}
		fa, err := svfs.ReadOnlyPipelineFileAccess(resolvedPipelineID)
		if err != nil {
			if err == ErrVFSNotFound {
				return nil, fmt.Errorf("workspace view %q unavailable: pipeline VFS %q not found: %w", view, resolvedPipelineID, err)
			}
			return nil, err
		}
		return fa, nil
	default:
		return nil, fmt.Errorf("unsupported workspace view %q", view)
	}
}

func (s *SessionWorkspaceViews) resolveSession(ctx context.Context) *SessionVFS {
	if s == nil {
		return nil
	}
	if s.session != nil {
		return s.session
	}
	if s.sessionLookup == nil {
		return nil
	}
	sessionID := strings.TrimSpace(SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID = s.defaultSessionID
	}
	if sessionID == "" {
		return nil
	}
	return s.sessionLookup(sessionID)
}

func (s *SessionWorkspaceViews) resolvePipelineID(pipelineID string) string {
	if trimmed := strings.TrimSpace(pipelineID); trimmed != "" {
		return trimmed
	}
	return s.defaultPipelineID
}

func inspectWorkspaceLayer(ctx context.Context, fa FileAccess, view WorkspaceView, path string) WorkspaceLayerState {
	state := WorkspaceLayerState{View: view, Available: fa != nil}
	if fa == nil {
		state.Error = "view unavailable"
		return state
	}
	exists, err := fa.Exists(ctx, path)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	state.Exists = exists
	if !exists {
		return state
	}
	info, err := fa.Stat(ctx, path)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	state.IsDir = info.IsDir()
	if state.IsDir {
		return state
	}
	content, err := fa.ReadFile(ctx, path)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	sum := sha256.Sum256(content)
	state.SizeBytes = len(content)
	state.ContentHash = hex.EncodeToString(sum[:])
	return state
}

func workspaceLayerErrorState(view WorkspaceView, err error) WorkspaceLayerState {
	state := WorkspaceLayerState{View: view}
	if err != nil {
		state.Error = err.Error()
	}
	return state
}

func compareWorkspaceLayers(a, b WorkspaceLayerState) (bool, bool) {
	if !a.Available || !b.Available || a.Error != "" || b.Error != "" {
		return false, false
	}
	if a.Exists != b.Exists {
		return true, true
	}
	if !a.Exists && !b.Exists {
		return false, true
	}
	if a.IsDir != b.IsDir {
		return true, true
	}
	if a.IsDir && b.IsDir {
		return false, true
	}
	return a.ContentHash != b.ContentHash, true
}

func pathStateHasErrors(state *WorkspacePathState) bool {
	if state == nil {
		return false
	}
	if state.Disk.Error != "" {
		return true
	}
	if state.Global != nil && state.Global.Error != "" {
		return true
	}
	if state.Pipeline != nil && state.Pipeline.Error != "" {
		return true
	}
	return false
}

func normalizeWorkspacePaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func uniqueWorkspacePaths(paths []string) []string {
	return normalizeWorkspacePaths(paths)
}

var _ WorkspaceViewAccess = (*SessionWorkspaceViews)(nil)

func (s *SessionWorkspaceViews) ResolveSession(ctx context.Context) *SessionVFS {
	return s.resolveSession(ctx)
}

// SessionForWorkspaceViews resolves the backing SessionVFS when the provided
// workspace view implementation is session-backed. Returns nil for non-session
// view providers.
func SessionForWorkspaceViews(ctx context.Context, views WorkspaceViewAccess) *SessionVFS {
	current := views
	for current != nil {
		if resolver, ok := current.(sessionWorkspaceViewResolver); ok {
			return resolver.ResolveSession(ctx)
		}
		delegate, ok := current.(workspaceViewDelegate)
		if !ok {
			return nil
		}
		next := delegate.WorkspaceViewsDelegate()
		if next == current {
			return nil
		}
		current = next
	}
	return nil
}
