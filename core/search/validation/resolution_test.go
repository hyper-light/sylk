package validation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockResolutionHandler is a mock implementation of ResolutionHandler.
type mockResolutionHandler struct {
	mu            sync.Mutex
	reindexCalls  []string
	removeCalls   []string
	metadataCalls []string
	reindexErr    error
	removeErr     error
	metadataErr   error
}

func newMockHandler() *mockResolutionHandler {
	return &mockResolutionHandler{
		reindexCalls:  make([]string, 0),
		removeCalls:   make([]string, 0),
		metadataCalls: make([]string, 0),
	}
}

func (m *mockResolutionHandler) Reindex(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reindexCalls = append(m.reindexCalls, path)
	return m.reindexErr
}

func (m *mockResolutionHandler) RemoveFromIndex(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeCalls = append(m.removeCalls, path)
	return m.removeErr
}

func (m *mockResolutionHandler) UpdateMetadata(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metadataCalls = append(m.metadataCalls, path)
	return m.metadataErr
}

func (m *mockResolutionHandler) ReindexCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reindexCalls)
}

func (m *mockResolutionHandler) RemoveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.removeCalls)
}

func (m *mockResolutionHandler) MetadataCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.metadataCalls)
}

// makeDiscrepancy creates a test discrepancy.
func makeDiscrepancy(path string, discType DiscrepancyType, sources ...ValidationSource) *Discrepancy {
	d := &Discrepancy{
		Path:    path,
		Type:    discType,
		Sources: sources,
		States:  make([]*FileState, 0),
	}
	for _, s := range sources {
		d.States = append(d.States, &FileState{
			Path:   path,
			Source: s,
			Exists: true,
			Size:   100,
		})
	}
	return d
}

// makeDiscrepancyWithStates creates a discrepancy with specific states.
func makeDiscrepancyWithStates(path string, discType DiscrepancyType, states []*FileState) *Discrepancy {
	sources := make([]ValidationSource, len(states))
	for i, s := range states {
		sources[i] = s.Source
	}
	return &Discrepancy{
		Path:    path,
		Type:    discType,
		Sources: sources,
		States:  states,
	}
}

// =============================================================================
// ResolutionAction Tests
// =============================================================================

func TestResolutionAction_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action ResolutionAction
		want   string
	}{
		{ActionNone, "none"},
		{ActionReindex, "reindex"},
		{ActionRemoveFromIndex, "remove_from_index"},
		{ActionUpdateMetadata, "update_metadata"},
		{ResolutionAction(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.action.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Resolution Clone Tests
// =============================================================================

func TestResolution_Clone(t *testing.T) {
	t.Parallel()

	original := &Resolution{
		Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
		Action:      ActionReindex,
		TruthSource: SourceFilesystem,
		Description: "test resolution",
		Applied:     true,
		AppliedAt:   time.Now(),
	}

	cloned := original.Clone()

	if cloned == nil {
		t.Fatal("Clone() returned nil")
	}
	if cloned.Action != original.Action {
		t.Errorf("Action = %v, want %v", cloned.Action, original.Action)
	}
	if cloned.TruthSource != original.TruthSource {
		t.Errorf("TruthSource = %v, want %v", cloned.TruthSource, original.TruthSource)
	}
	if cloned.Description != original.Description {
		t.Errorf("Description = %q, want %q", cloned.Description, original.Description)
	}
}

func TestResolution_Clone_Nil(t *testing.T) {
	t.Parallel()

	var r *Resolution
	if r.Clone() != nil {
		t.Error("Clone() of nil should return nil")
	}
}

// =============================================================================
// DiscrepancyResolver Creation Tests
// =============================================================================

func TestNewDiscrepancyResolver(t *testing.T) {
	t.Parallel()

	config := DefaultResolverConfig()
	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(config, handler)

	if resolver == nil {
		t.Fatal("expected non-nil resolver")
	}
}

func TestNewDiscrepancyResolver_NilHandler(t *testing.T) {
	t.Parallel()

	config := DefaultResolverConfig()
	resolver := NewDiscrepancyResolver(config, nil)

	if resolver == nil {
		t.Fatal("expected non-nil resolver even with nil handler")
	}
}

// =============================================================================
// Resolve Tests
// =============================================================================

func TestDiscrepancyResolver_Resolve_Empty(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	_, err := resolver.Resolve(ctx, nil)
	if err != ErrNoDiscrepancies {
		t.Errorf("expected ErrNoDiscrepancies, got %v", err)
	}
}

func TestDiscrepancyResolver_Resolve_ContentMismatch(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(resolutions) != 1 {
		t.Fatalf("expected 1 resolution, got %d", len(resolutions))
	}

	res := resolutions[0]
	if res.Action != ActionReindex {
		t.Errorf("Action = %v, want %v", res.Action, ActionReindex)
	}
	if res.TruthSource != SourceFilesystem {
		t.Errorf("TruthSource = %v, want %v", res.TruthSource, SourceFilesystem)
	}
}

func TestDiscrepancyResolver_Resolve_MissingInCMT(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	states := []*FileState{
		{Path: "/test/new.go", Source: SourceFilesystem, Exists: true, Size: 100},
		{Path: "/test/new.go", Source: SourceCMT, Exists: false},
	}
	discrepancies := []*Discrepancy{
		makeDiscrepancyWithStates("/test/new.go", DiscrepancyMissing, states),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(resolutions) != 1 {
		t.Fatalf("expected 1 resolution, got %d", len(resolutions))
	}

	res := resolutions[0]
	if res.Action != ActionReindex {
		t.Errorf("Action = %v, want %v", res.Action, ActionReindex)
	}
}

func TestDiscrepancyResolver_Resolve_DeletedFromFilesystem(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	states := []*FileState{
		{Path: "/test/deleted.go", Source: SourceFilesystem, Exists: false},
		{Path: "/test/deleted.go", Source: SourceCMT, Exists: true, Size: 100},
	}
	discrepancies := []*Discrepancy{
		makeDiscrepancyWithStates("/test/deleted.go", DiscrepancyMissing, states),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	res := resolutions[0]
	if res.Action != ActionRemoveFromIndex {
		t.Errorf("Action = %v, want %v", res.Action, ActionRemoveFromIndex)
	}
}

func TestDiscrepancyResolver_Resolve_ModTimeMismatch(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	res := resolutions[0]
	if res.Action != ActionUpdateMetadata {
		t.Errorf("Action = %v, want %v", res.Action, ActionUpdateMetadata)
	}
}

// =============================================================================
// Priority Tests
// =============================================================================

func TestDiscrepancyResolver_Priority_FilesystemFirst(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceGit, SourceCMT),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	res := resolutions[0]
	if res.TruthSource != SourceFilesystem {
		t.Errorf("TruthSource = %v, want %v", res.TruthSource, SourceFilesystem)
	}
}

func TestDiscrepancyResolver_Priority_GitSecond(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	// No filesystem source, only git and CMT
	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceGit, SourceCMT),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	res := resolutions[0]
	if res.TruthSource != SourceGit {
		t.Errorf("TruthSource = %v, want %v", res.TruthSource, SourceGit)
	}
}

func TestDiscrepancyResolver_Priority_CMTLast(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	// Only CMT source
	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceCMT),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	res := resolutions[0]
	if res.TruthSource != SourceCMT {
		t.Errorf("TruthSource = %v, want %v", res.TruthSource, SourceCMT)
	}
}

// =============================================================================
// Apply Tests
// =============================================================================

func TestDiscrepancyResolver_Apply_Reindex(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
			Action:      ActionReindex,
			TruthSource: SourceFilesystem,
		},
	}

	results, err := resolver.Apply(ctx, resolutions)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Applied {
		t.Error("expected Applied = true")
	}
	if handler.ReindexCount() != 1 {
		t.Errorf("expected 1 reindex call, got %d", handler.ReindexCount())
	}
}

func TestDiscrepancyResolver_Apply_RemoveFromIndex(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/deleted.go", DiscrepancyMissing, SourceFilesystem, SourceCMT),
			Action:      ActionRemoveFromIndex,
			TruthSource: SourceFilesystem,
		},
	}

	results, err := resolver.Apply(ctx, resolutions)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if !results[0].Applied {
		t.Error("expected Applied = true")
	}
	if handler.RemoveCount() != 1 {
		t.Errorf("expected 1 remove call, got %d", handler.RemoveCount())
	}
}

func TestDiscrepancyResolver_Apply_UpdateMetadata(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
			Action:      ActionUpdateMetadata,
			TruthSource: SourceFilesystem,
		},
	}

	results, err := resolver.Apply(ctx, resolutions)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if !results[0].Applied {
		t.Error("expected Applied = true")
	}
	if handler.MetadataCount() != 1 {
		t.Errorf("expected 1 metadata call, got %d", handler.MetadataCount())
	}
}

func TestDiscrepancyResolver_Apply_Error(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	handler.reindexErr = errors.New("reindex failed")
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
			Action:      ActionReindex,
			TruthSource: SourceFilesystem,
		},
	}

	results, err := resolver.Apply(ctx, resolutions)
	if err != nil {
		t.Fatalf("Apply() should not return error, got %v", err)
	}

	if results[0].Applied {
		t.Error("expected Applied = false when handler returns error")
	}
	if results[0].Error == nil {
		t.Error("expected Error to be set")
	}
}

// =============================================================================
// Dry-Run Tests
// =============================================================================

func TestDiscrepancyResolver_DryRun_NoChanges(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	config := ResolverConfig{DryRun: true}
	resolver := NewDiscrepancyResolver(config, handler)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
			Action:      ActionReindex,
			TruthSource: SourceFilesystem,
			Description: "reindex file",
		},
	}

	results, err := resolver.Apply(ctx, resolutions)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Handler should not be called in dry-run mode
	if handler.ReindexCount() != 0 {
		t.Errorf("expected 0 reindex calls in dry-run, got %d", handler.ReindexCount())
	}

	// Description should indicate dry-run
	if len(results) > 0 && results[0].Description[:9] != "[DRY-RUN]" {
		t.Errorf("expected dry-run prefix in description, got %q", results[0].Description)
	}
}

func TestDiscrepancyResolver_IsDryRun(t *testing.T) {
	t.Parallel()

	config := ResolverConfig{DryRun: true}
	resolver := NewDiscrepancyResolver(config, nil)

	if !resolver.IsDryRun() {
		t.Error("expected IsDryRun() = true")
	}

	resolver.SetDryRun(false)
	if resolver.IsDryRun() {
		t.Error("expected IsDryRun() = false after SetDryRun(false)")
	}
}

// =============================================================================
// ResolveAndApply Tests
// =============================================================================

func TestDiscrepancyResolver_ResolveAndApply(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
	}

	results, err := resolver.ResolveAndApply(ctx, discrepancies)
	if err != nil {
		t.Fatalf("ResolveAndApply() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Applied {
		t.Error("expected Applied = true")
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestDiscrepancyResolver_Close(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())

	if err := resolver.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	ctx := context.Background()
	_, err := resolver.Resolve(ctx, []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
	})

	if err != ErrResolverClosed {
		t.Errorf("expected ErrResolverClosed, got %v", err)
	}
}

// =============================================================================
// IsMinorDiscrepancy Tests
// =============================================================================

func TestIsMinorDiscrepancy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		disc  *Discrepancy
		minor bool
	}{
		{
			name:  "nil discrepancy",
			disc:  nil,
			minor: false,
		},
		{
			name:  "modtime mismatch is minor",
			disc:  makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
			minor: true,
		},
		{
			name:  "content mismatch is not minor",
			disc:  makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
			minor: false,
		},
		{
			name:  "missing is not minor",
			disc:  makeDiscrepancy("/test/file.go", DiscrepancyMissing, SourceFilesystem, SourceCMT),
			minor: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMinorDiscrepancy(tt.disc); got != tt.minor {
				t.Errorf("IsMinorDiscrepancy() = %v, want %v", got, tt.minor)
			}
		})
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestDiscrepancyResolver_ConcurrentResolve(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			discrepancies := []*Discrepancy{
				makeDiscrepancy("/test/file"+string(rune('0'+idx))+".go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
			}
			_, err := resolver.Resolve(ctx, discrepancies)
			if err == nil {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if successCount.Load() != 10 {
		t.Errorf("expected 10 successful resolves, got %d", successCount.Load())
	}
}

func TestDiscrepancyResolver_ConcurrentApply(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resolutions := []*Resolution{
				{
					Discrepancy: makeDiscrepancy("/test/file"+string(rune('0'+idx))+".go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
					Action:      ActionReindex,
					TruthSource: SourceFilesystem,
				},
			}
			resolver.Apply(ctx, resolutions)
		}(i)
	}

	wg.Wait()

	if handler.ReindexCount() != 10 {
		t.Errorf("expected 10 reindex calls, got %d", handler.ReindexCount())
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestDiscrepancyResolver_Apply_NilHandler(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), nil)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
			Action:      ActionReindex,
			TruthSource: SourceFilesystem,
		},
	}

	_, err := resolver.Apply(ctx, resolutions)
	if err != ErrApplyFailed {
		t.Errorf("expected ErrApplyFailed with nil handler, got %v", err)
	}
}

func TestDiscrepancyResolver_Apply_ActionNone(t *testing.T) {
	t.Parallel()

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)
	ctx := context.Background()

	resolutions := []*Resolution{
		{
			Discrepancy: makeDiscrepancy("/test/file.go", DiscrepancyUnknown, SourceFilesystem, SourceCMT),
			Action:      ActionNone,
			TruthSource: SourceFilesystem,
		},
	}

	results, err := resolver.Apply(ctx, resolutions)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// No handler calls for ActionNone
	if handler.ReindexCount() != 0 || handler.RemoveCount() != 0 || handler.MetadataCount() != 0 {
		t.Error("expected no handler calls for ActionNone")
	}

	// Should still mark as applied
	if !results[0].Applied {
		t.Error("expected Applied = true for ActionNone")
	}
}

func TestDiscrepancyResolver_Resolve_MultipleDiscrepancies(t *testing.T) {
	t.Parallel()

	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())
	ctx := context.Background()

	// Create a proper missing discrepancy where file exists in filesystem but not in CMT
	missingStates := []*FileState{
		{Path: "/test/file2.go", Source: SourceFilesystem, Exists: true, Size: 100},
		{Path: "/test/file2.go", Source: SourceCMT, Exists: false},
	}

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file1.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
		makeDiscrepancyWithStates("/test/file2.go", DiscrepancyMissing, missingStates),
		makeDiscrepancy("/test/file3.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
	}

	resolutions, err := resolver.Resolve(ctx, discrepancies)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(resolutions) != 3 {
		t.Fatalf("expected 3 resolutions, got %d", len(resolutions))
	}

	// Check each resolution has correct action
	if resolutions[0].Action != ActionReindex {
		t.Errorf("resolution[0].Action = %v, want %v", resolutions[0].Action, ActionReindex)
	}
	if resolutions[1].Action != ActionReindex {
		t.Errorf("resolution[1].Action = %v, want %v", resolutions[1].Action, ActionReindex)
	}
	if resolutions[2].Action != ActionUpdateMetadata {
		t.Errorf("resolution[2].Action = %v, want %v", resolutions[2].Action, ActionUpdateMetadata)
	}
}
