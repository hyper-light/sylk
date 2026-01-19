package search

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Mock Implementations
// =============================================================================

// mockBleveManager implements the BleveManager interface for testing.
type mockBleveManager struct {
	mu            sync.Mutex
	open          bool
	openErr       error
	closeErr      error
	searchResult  *SearchResult
	searchErr     error
	indexErr      error
	documentCount uint64
	countErr      error
	openCalls     int
	closeCalls    int
	searchCalls   int
	indexCalls    int
}

func newMockBleveManager() *mockBleveManager {
	return &mockBleveManager{
		searchResult: &SearchResult{
			Documents: []ScoredDocument{
				{Document: Document{ID: "1", Path: "/test.go"}},
			},
			TotalHits: 1,
		},
		documentCount: 100,
	}
}

func (m *mockBleveManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openCalls++
	if m.openErr != nil {
		return m.openErr
	}
	m.open = true
	return nil
}

func (m *mockBleveManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	m.open = false
	return m.closeErr
}

func (m *mockBleveManager) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

func (m *mockBleveManager) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchCalls++
	return m.searchResult, m.searchErr
}

func (m *mockBleveManager) Index(ctx context.Context, doc *Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexCalls++
	return m.indexErr
}

func (m *mockBleveManager) IndexBatch(ctx context.Context, docs []*Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for range docs {
		m.indexCalls++
	}
	return m.indexErr
}

func (m *mockBleveManager) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockBleveManager) DocumentCount() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.documentCount, m.countErr
}

func (m *mockBleveManager) GetOpenCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openCalls
}

func (m *mockBleveManager) GetCloseCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCalls
}

func (m *mockBleveManager) GetSearchCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.searchCalls
}

func (m *mockBleveManager) GetIndexCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.indexCalls
}

// mockVectorDBManager implements the VectorDBManager interface for testing.
type mockVectorDBManager struct {
	mu          sync.Mutex
	open        bool
	openErr     error
	closeErr    error
	searchErr   error
	openCalls   int
	closeCalls  int
	searchCalls int
}

func newMockVectorDBManager() *mockVectorDBManager {
	return &mockVectorDBManager{}
}

func (m *mockVectorDBManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openCalls++
	if m.openErr != nil {
		return m.openErr
	}
	m.open = true
	return nil
}

func (m *mockVectorDBManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	m.open = false
	return m.closeErr
}

func (m *mockVectorDBManager) Search(ctx context.Context, query []float32, limit int) ([]ScoredDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchCalls++
	return nil, m.searchErr
}

func (m *mockVectorDBManager) GetOpenCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openCalls
}

func (m *mockVectorDBManager) GetCloseCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCalls
}

// mockWatcher implements the ChangeWatcher interface for testing.
type mockWatcher struct {
	mu        sync.Mutex
	started   bool
	startErr  error
	stopErr   error
	events    chan ChangeEvent
	startChan chan struct{}
}

func newMockWatcher() *mockWatcher {
	return &mockWatcher{
		events:    make(chan ChangeEvent, 10),
		startChan: make(chan struct{}),
	}
}

func (m *mockWatcher) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.startErr != nil {
		m.mu.Unlock()
		return m.startErr
	}
	m.started = true
	m.mu.Unlock()

	close(m.startChan)

	<-ctx.Done()
	return nil
}

func (m *mockWatcher) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false
	return m.stopErr
}

func (m *mockWatcher) Events() <-chan ChangeEvent {
	return m.events
}

func (m *mockWatcher) WaitForStart() {
	<-m.startChan
}

// mockManifest implements the ManifestTracker interface for testing.
type mockManifest struct {
	mu       sync.Mutex
	store    map[string]string
	closeErr error
}

func newMockManifest() *mockManifest {
	return &mockManifest{
		store: make(map[string]string),
	}
}

func (m *mockManifest) Get(path string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.store[path]
	return v, ok
}

func (m *mockManifest) Set(path string, checksum string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[path] = checksum
	return nil
}

func (m *mockManifest) Delete(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, path)
	return nil
}

func (m *mockManifest) Size() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.store))
}

func (m *mockManifest) Close() error {
	return m.closeErr
}

// =============================================================================
// SystemState Tests
// =============================================================================

func TestSystemState_String(t *testing.T) {
	tests := []struct {
		state    SystemState
		expected string
	}{
		{StateStopped, "stopped"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateError, "error"},
		{SystemState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.state.String()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// =============================================================================
// Config Tests
// =============================================================================

func TestDefaultSearchSystemConfig(t *testing.T) {
	config := DefaultSearchSystemConfig()

	if config.StartTimeout != DefaultStartTimeout {
		t.Errorf("expected StartTimeout %v, got %v",
			DefaultStartTimeout, config.StartTimeout)
	}
	if config.StopTimeout != DefaultStopTimeout {
		t.Errorf("expected StopTimeout %v, got %v",
			DefaultStopTimeout, config.StopTimeout)
	}
	if config.IndexTimeout != DefaultIndexTimeout {
		t.Errorf("expected IndexTimeout %v, got %v",
			DefaultIndexTimeout, config.IndexTimeout)
	}
	if config.MaxConcurrentSearches != 10 {
		t.Errorf("expected MaxConcurrentSearches 10, got %d",
			config.MaxConcurrentSearches)
	}
	if !config.EnableWatcher {
		t.Error("expected EnableWatcher to be true")
	}
}

func TestSearchSystemConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    SearchSystemConfig
		expectErr bool
	}{
		{
			name: "valid config",
			config: SearchSystemConfig{
				BlevePath: "/path/to/bleve",
			},
			expectErr: false,
		},
		{
			name:      "missing bleve path",
			config:    SearchSystemConfig{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewSearchSystem_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}

	ss, err := NewSearchSystem(config)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ss == nil {
		t.Fatal("expected non-nil SearchSystem")
	}
	if ss.State() != StateStopped {
		t.Errorf("expected StateStopped, got %v", ss.State())
	}
}

func TestNewSearchSystem_InvalidConfig(t *testing.T) {
	config := SearchSystemConfig{} // Missing BlevePath

	ss, err := NewSearchSystem(config)

	if err == nil {
		t.Error("expected error for invalid config")
	}
	if ss != nil {
		t.Error("expected nil SearchSystem")
	}
}

func TestNewSearchSystem_DefaultsApplied(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
		// All other fields are zero values
	}

	ss, err := NewSearchSystem(config)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that defaults were applied to internal config
	if ss.config.MaxConcurrentSearches != 10 {
		t.Errorf("expected MaxConcurrentSearches 10, got %d",
			ss.config.MaxConcurrentSearches)
	}
}

func TestNewSearchSystemWithComponents_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}
	bleve := newMockBleveManager()
	vectorDB := newMockVectorDBManager()
	watcher := newMockWatcher()
	manifest := newMockManifest()

	ss, err := NewSearchSystemWithComponents(config, bleve, vectorDB, watcher, manifest)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ss == nil {
		t.Fatal("expected non-nil SearchSystem")
	}
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestStart_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()
	vectorDB := newMockVectorDBManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, vectorDB, nil, nil)

	err := ss.Start(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ss.State() != StateRunning {
		t.Errorf("expected StateRunning, got %v", ss.State())
	}
	if !ss.IsRunning() {
		t.Error("expected IsRunning to return true")
	}
	if bleve.GetOpenCalls() != 1 {
		t.Errorf("expected 1 bleve open call, got %d", bleve.GetOpenCalls())
	}
	if vectorDB.GetOpenCalls() != 1 {
		t.Errorf("expected 1 vectorDB open call, got %d", vectorDB.GetOpenCalls())
	}

	// Cleanup
	ss.Stop()
}

func TestStart_AlreadyRunning(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	err := ss.Start(context.Background())

	if err != ErrSystemAlreadyRunning {
		t.Errorf("expected ErrSystemAlreadyRunning, got %v", err)
	}
}

func TestStart_BleveOpenFails(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()
	bleve.openErr = errors.New("bleve open failed")

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)

	err := ss.Start(context.Background())

	if !errors.Is(err, ErrStartFailed) {
		t.Errorf("expected ErrStartFailed, got %v", err)
	}
	if ss.State() != StateError {
		t.Errorf("expected StateError, got %v", ss.State())
	}
}

func TestStart_VectorDBOpenFails(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()
	vectorDB := newMockVectorDBManager()
	vectorDB.openErr = errors.New("vectordb open failed")

	ss, _ := NewSearchSystemWithComponents(config, bleve, vectorDB, nil, nil)

	err := ss.Start(context.Background())

	if !errors.Is(err, ErrStartFailed) {
		t.Errorf("expected ErrStartFailed, got %v", err)
	}
}

func TestStart_WithWatcher(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: true,
	}
	bleve := newMockBleveManager()
	watcher := newMockWatcher()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, watcher, nil)

	err := ss.Start(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for watcher to start
	watcher.WaitForStart()

	status := ss.GetStatus()
	if !status.WatcherActive {
		t.Error("expected watcher to be active")
	}

	ss.Stop()
}

func TestStop_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()
	vectorDB := newMockVectorDBManager()
	manifest := newMockManifest()

	ss, _ := NewSearchSystemWithComponents(config, bleve, vectorDB, nil, manifest)
	ss.Start(context.Background())

	err := ss.Stop()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ss.State() != StateStopped {
		t.Errorf("expected StateStopped, got %v", ss.State())
	}
	if bleve.GetCloseCalls() != 1 {
		t.Errorf("expected 1 bleve close call, got %d", bleve.GetCloseCalls())
	}
	if vectorDB.GetCloseCalls() != 1 {
		t.Errorf("expected 1 vectorDB close call, got %d", vectorDB.GetCloseCalls())
	}
}

func TestStop_NotRunning(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}
	ss, _ := NewSearchSystem(config)

	err := ss.Stop()

	if err != ErrSystemNotRunning {
		t.Errorf("expected ErrSystemNotRunning, got %v", err)
	}
}

func TestStop_WithWatcher(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: true,
	}
	bleve := newMockBleveManager()
	watcher := newMockWatcher()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, watcher, nil)
	ss.Start(context.Background())

	err := ss.Stop()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ss.State() != StateStopped {
		t.Errorf("expected StateStopped, got %v", ss.State())
	}
}

// =============================================================================
// Search Tests
// =============================================================================

func TestSearch_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	req := &SearchRequest{Query: "test query", Limit: 10}
	result, err := ss.Search(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Documents) != 1 {
		t.Errorf("expected 1 document, got %d", len(result.Documents))
	}
	if bleve.GetSearchCalls() != 1 {
		t.Errorf("expected 1 search call, got %d", bleve.GetSearchCalls())
	}
}

func TestSearch_NotRunning(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)

	req := &SearchRequest{Query: "test"}
	_, err := ss.Search(context.Background(), req)

	if err != ErrSystemNotRunning {
		t.Errorf("expected ErrSystemNotRunning, got %v", err)
	}
}

func TestSearch_DuringShutdown(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())

	// Force state to stopping
	ss.state.Store(int32(StateStopping))

	req := &SearchRequest{Query: "test"}
	_, err := ss.Search(context.Background(), req)

	if err != ErrSystemShuttingDown {
		t.Errorf("expected ErrSystemShuttingDown, got %v", err)
	}
}

func TestSearch_NilBleveManager(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}

	ss, _ := NewSearchSystemWithComponents(config, nil, nil, nil, nil)
	ss.state.Store(int32(StateRunning)) // Force running state

	req := &SearchRequest{Query: "test"}
	_, err := ss.Search(context.Background(), req)

	if err != ErrNilBleveManager {
		t.Errorf("expected ErrNilBleveManager, got %v", err)
	}
}

func TestSearch_UpdatesStats(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	beforeCount := ss.searchCount.Load()

	req := &SearchRequest{Query: "test", Limit: 10}
	ss.Search(context.Background(), req)

	afterCount := ss.searchCount.Load()
	if afterCount != beforeCount+1 {
		t.Errorf("expected search count to increment, got %d", afterCount)
	}

	status := ss.GetStatus()
	if status.SearchCount != afterCount {
		t.Errorf("expected status.SearchCount %d, got %d", afterCount, status.SearchCount)
	}
}

// =============================================================================
// Index Tests
// =============================================================================

func TestIndex_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	paths := []string{"/path/to/file1.go", "/path/to/file2.go"}
	result, err := ss.Index(context.Background(), paths)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Indexed != 2 {
		t.Errorf("expected 2 indexed, got %d", result.Indexed)
	}
	if bleve.GetIndexCalls() != 2 {
		t.Errorf("expected 2 index calls, got %d", bleve.GetIndexCalls())
	}
}

func TestIndex_NotRunning(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}

	ss, _ := NewSearchSystem(config)

	paths := []string{"/path/to/file.go"}
	_, err := ss.Index(context.Background(), paths)

	if err != ErrSystemNotRunning {
		t.Errorf("expected ErrSystemNotRunning, got %v", err)
	}
}

func TestIndex_SetsIndexingFlag(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	// Start indexing in background
	var indexingDuringOp atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		paths := []string{"/path/to/file.go"}
		ss.Index(context.Background(), paths)
	}()

	// Check indexing flag
	time.Sleep(10 * time.Millisecond)
	indexingDuringOp.Store(ss.isIndexing())

	wg.Wait()

	// After indexing completes, flag should be false
	if ss.isIndexing() {
		t.Error("expected indexing flag to be false after completion")
	}
}

func TestReindex_Success(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	result, err := ss.Reindex(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestReindex_NotRunning(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}

	ss, _ := NewSearchSystem(config)

	_, err := ss.Reindex(context.Background())

	if err != ErrSystemNotRunning {
		t.Errorf("expected ErrSystemNotRunning, got %v", err)
	}
}

// =============================================================================
// Status Tests
// =============================================================================

func TestGetStatus_Stopped(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath: "/path/to/bleve",
	}
	ss, _ := NewSearchSystem(config)

	status := ss.GetStatus()

	if status.State != StateStopped {
		t.Errorf("expected StateStopped, got %v", status.State)
	}
	if status.Uptime != 0 {
		t.Errorf("expected 0 uptime, got %v", status.Uptime)
	}
}

func TestGetStatus_Running(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()
	bleve.documentCount = 500

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	time.Sleep(10 * time.Millisecond) // Let uptime accumulate

	status := ss.GetStatus()

	if status.State != StateRunning {
		t.Errorf("expected StateRunning, got %v", status.State)
	}
	if status.Uptime <= 0 {
		t.Error("expected positive uptime")
	}
	if status.DocumentCount != 500 {
		t.Errorf("expected document count 500, got %d", status.DocumentCount)
	}
	if status.StartedAt.IsZero() {
		t.Error("expected non-zero StartedAt")
	}
}

func TestGetStatus_WithErrors(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()
	bleve.searchErr = errors.New("search failed")

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	// Trigger an error
	req := &SearchRequest{Query: "test", Limit: 10}
	ss.Search(context.Background(), req)

	status := ss.GetStatus()

	if status.ErrorCount != 1 {
		t.Errorf("expected error count 1, got %d", status.ErrorCount)
	}
	if status.LastError != "search failed" {
		t.Errorf("expected 'search failed' error, got %q", status.LastError)
	}
}

func TestGetStatus_WatcherActive(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: true,
		WatchPaths:    []string{"/path/to/watch"},
	}
	bleve := newMockBleveManager()
	watcher := newMockWatcher()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, watcher, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	watcher.WaitForStart()

	status := ss.GetStatus()

	if !status.WatcherActive {
		t.Error("expected WatcherActive to be true")
	}
	if len(status.WatchedPaths) != 1 {
		t.Errorf("expected 1 watched path, got %d", len(status.WatchedPaths))
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestConcurrentSearches(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:             "/path/to/bleve",
		EnableWatcher:         false,
		MaxConcurrentSearches: 5,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	var wg sync.WaitGroup
	var completed atomic.Int32

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &SearchRequest{Query: "test", Limit: 10}
			_, err := ss.Search(context.Background(), req)
			if err == nil {
				completed.Add(1)
			}
		}()
	}

	wg.Wait()

	if completed.Load() != 20 {
		t.Errorf("expected 20 completed searches, got %d", completed.Load())
	}
}

func TestConcurrentSearchAndIndex(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:             "/path/to/bleve",
		EnableWatcher:         false,
		MaxConcurrentSearches: 3,
		MaxConcurrentIndexes:  2,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Concurrent searches
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					req := &SearchRequest{Query: "test", Limit: 10}
					ss.Search(context.Background(), req)
				}
			}
		}()
	}

	// Concurrent indexes
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					paths := []string{"/path/to/file.go"}
					ss.Index(context.Background(), paths)
				}
			}
		}()
	}

	wg.Wait()
	// Test passes if no panics or deadlocks occur
}

func TestConcurrentStartStop(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:     "/path/to/bleve",
		EnableWatcher: false,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)

	// Start the system
	ss.Start(context.Background())

	// Try concurrent stops
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ss.Stop()
		}()
	}

	wg.Wait()

	// System should be stopped
	if ss.State() != StateStopped {
		t.Errorf("expected StateStopped, got %v", ss.State())
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestSearch_ContextCancelled(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:             "/path/to/bleve",
		EnableWatcher:         false,
		MaxConcurrentSearches: 1,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	// Fill up the semaphore
	ss.searchSem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	req := &SearchRequest{Query: "test", Limit: 10}
	_, err := ss.Search(ctx, req)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline exceeded, got %v", err)
	}

	// Release semaphore
	<-ss.searchSem
}

func TestIndex_ContextCancelled(t *testing.T) {
	config := SearchSystemConfig{
		BlevePath:            "/path/to/bleve",
		EnableWatcher:        false,
		MaxConcurrentIndexes: 1,
	}
	bleve := newMockBleveManager()

	ss, _ := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	ss.Start(context.Background())
	defer ss.Stop()

	// Fill up the semaphore
	ss.indexSem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	paths := []string{"/path/to/file.go"}
	_, err := ss.Index(ctx, paths)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline exceeded, got %v", err)
	}

	// Release semaphore
	<-ss.indexSem
}
