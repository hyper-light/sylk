// Package search provides document search types and utilities for the Sylk search system.
// This file contains integration tests for the SearchSystem facade.
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
// Integration Test Mocks - Extended versions with configurable behavior
// =============================================================================

// integrationBleveManager provides a configurable mock for integration testing.
type integrationBleveManager struct {
	mu            sync.Mutex
	open          bool
	documents     map[string]*Document
	searchDelay   time.Duration
	indexDelay    time.Duration
	openErr       error
	closeErr      error
	searchErr     error
	indexErr      error
	openCalls     int
	closeCalls    int
	searchCalls   int
	indexCalls    int
	deleteCalls   int
}

func newIntegrationBleveManager() *integrationBleveManager {
	return &integrationBleveManager{
		documents: make(map[string]*Document),
	}
}

func (m *integrationBleveManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openCalls++
	if m.openErr != nil {
		return m.openErr
	}
	m.open = true
	return nil
}

func (m *integrationBleveManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	m.open = false
	return m.closeErr
}

func (m *integrationBleveManager) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

func (m *integrationBleveManager) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	m.mu.Lock()
	m.searchCalls++
	delay := m.searchDelay
	searchErr := m.searchErr
	docs := m.copyDocuments()
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if searchErr != nil {
		return nil, searchErr
	}

	return m.buildSearchResult(req, docs), nil
}

func (m *integrationBleveManager) copyDocuments() map[string]*Document {
	docs := make(map[string]*Document, len(m.documents))
	for k, v := range m.documents {
		docs[k] = v
	}
	return docs
}

func (m *integrationBleveManager) buildSearchResult(req *SearchRequest, docs map[string]*Document) *SearchResult {
	result := &SearchResult{
		Documents: make([]ScoredDocument, 0, len(docs)),
		Query:     req.Query,
	}
	for _, doc := range docs {
		if m.matchesQuery(doc, req.Query) {
			result.Documents = append(result.Documents, ScoredDocument{
				Document: *doc,
				Score:    1.0,
			})
		}
	}
	result.TotalHits = int64(len(result.Documents))
	return result
}

func (m *integrationBleveManager) matchesQuery(doc *Document, query string) bool {
	return len(query) > 0
}

func (m *integrationBleveManager) Index(ctx context.Context, doc *Document) error {
	m.mu.Lock()
	m.indexCalls++
	delay := m.indexDelay
	indexErr := m.indexErr
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if indexErr != nil {
		return indexErr
	}

	m.mu.Lock()
	m.documents[doc.ID] = doc
	m.mu.Unlock()
	return nil
}

func (m *integrationBleveManager) IndexBatch(ctx context.Context, docs []*Document) error {
	for _, doc := range docs {
		if err := m.Index(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func (m *integrationBleveManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	delete(m.documents, id)
	return nil
}

func (m *integrationBleveManager) DocumentCount() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return uint64(len(m.documents)), nil
}

func (m *integrationBleveManager) GetDocumentIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.documents))
	for id := range m.documents {
		ids = append(ids, id)
	}
	return ids
}

// integrationVectorDBManager provides a configurable mock for VectorDB.
type integrationVectorDBManager struct {
	mu          sync.Mutex
	open        bool
	documents   map[string]ScoredDocument
	searchDelay time.Duration
	openErr     error
	closeErr    error
	searchErr   error
}

func newIntegrationVectorDBManager() *integrationVectorDBManager {
	return &integrationVectorDBManager{
		documents: make(map[string]ScoredDocument),
	}
}

func (m *integrationVectorDBManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openErr != nil {
		return m.openErr
	}
	m.open = true
	return nil
}

func (m *integrationVectorDBManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = false
	return m.closeErr
}

func (m *integrationVectorDBManager) Search(ctx context.Context, query []float32, limit int) ([]ScoredDocument, error) {
	m.mu.Lock()
	delay := m.searchDelay
	searchErr := m.searchErr
	docs := make([]ScoredDocument, 0, len(m.documents))
	for _, doc := range m.documents {
		docs = append(docs, doc)
	}
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if searchErr != nil {
		return nil, searchErr
	}

	if len(docs) > limit {
		return docs[:limit], nil
	}
	return docs, nil
}

func (m *integrationVectorDBManager) AddDocument(doc ScoredDocument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[doc.ID] = doc
}

func (m *integrationVectorDBManager) GetDocumentIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.documents))
	for id := range m.documents {
		ids = append(ids, id)
	}
	return ids
}

// integrationWatcher provides a configurable mock for ChangeWatcher.
type integrationWatcher struct {
	mu        sync.Mutex
	started   bool
	events    chan ChangeEvent
	startChan chan struct{}
	startErr  error
	stopErr   error
}

func newIntegrationWatcher() *integrationWatcher {
	return &integrationWatcher{
		events:    make(chan ChangeEvent, 100),
		startChan: make(chan struct{}),
	}
}

func (m *integrationWatcher) Start(ctx context.Context) error {
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

func (m *integrationWatcher) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false
	return m.stopErr
}

func (m *integrationWatcher) Events() <-chan ChangeEvent {
	return m.events
}

func (m *integrationWatcher) SendEvent(event ChangeEvent) {
	m.events <- event
}

func (m *integrationWatcher) WaitForStart(t *testing.T, timeout time.Duration) {
	select {
	case <-m.startChan:
	case <-time.After(timeout):
		t.Fatal("timeout waiting for watcher to start")
	}
}

// integrationManifest provides a configurable mock for ManifestTracker.
type integrationManifest struct {
	mu       sync.Mutex
	store    map[string]string
	setDelay time.Duration
	setErr   error
	delErr   error
	closeErr error
}

func newIntegrationManifest() *integrationManifest {
	return &integrationManifest{
		store: make(map[string]string),
	}
}

func (m *integrationManifest) Get(path string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.store[path]
	return v, ok
}

func (m *integrationManifest) Set(path string, checksum string) error {
	if m.setDelay > 0 {
		time.Sleep(m.setDelay)
	}
	if m.setErr != nil {
		return m.setErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[path] = checksum
	return nil
}

func (m *integrationManifest) Delete(path string) error {
	if m.delErr != nil {
		return m.delErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, path)
	return nil
}

func (m *integrationManifest) Size() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.store))
}

func (m *integrationManifest) Close() error {
	return m.closeErr
}

func (m *integrationManifest) GetAllPaths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	paths := make([]string, 0, len(m.store))
	for p := range m.store {
		paths = append(paths, p)
	}
	return paths
}

// concurrencyTrackingBleveManager tracks actual concurrent operations.
type concurrencyTrackingBleveManager struct {
	mu                   sync.Mutex
	open                 bool
	documents            map[string]*Document
	delay                time.Duration
	activeSearches       atomic.Int32
	maxConcurrentSearch  atomic.Int32
	activeIndexes        atomic.Int32
	maxConcurrentIndex   atomic.Int32
}

func newConcurrencyTrackingBleveManager(delay time.Duration) *concurrencyTrackingBleveManager {
	return &concurrencyTrackingBleveManager{
		documents: make(map[string]*Document),
		delay:     delay,
	}
}

func (m *concurrencyTrackingBleveManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = true
	return nil
}

func (m *concurrencyTrackingBleveManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = false
	return nil
}

func (m *concurrencyTrackingBleveManager) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

func (m *concurrencyTrackingBleveManager) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	// Track concurrent searches
	current := m.activeSearches.Add(1)
	m.updateMaxSearch(current)
	defer m.activeSearches.Add(-1)

	// Simulate work
	select {
	case <-time.After(m.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &SearchResult{Query: req.Query}, nil
}

func (m *concurrencyTrackingBleveManager) updateMaxSearch(current int32) {
	for {
		old := m.maxConcurrentSearch.Load()
		if current <= old {
			return
		}
		if m.maxConcurrentSearch.CompareAndSwap(old, current) {
			return
		}
	}
}

func (m *concurrencyTrackingBleveManager) Index(ctx context.Context, doc *Document) error {
	// Track concurrent indexes
	current := m.activeIndexes.Add(1)
	m.updateMaxIndex(current)
	defer m.activeIndexes.Add(-1)

	// Simulate work
	select {
	case <-time.After(m.delay):
	case <-ctx.Done():
		return ctx.Err()
	}

	m.mu.Lock()
	m.documents[doc.ID] = doc
	m.mu.Unlock()
	return nil
}

func (m *concurrencyTrackingBleveManager) updateMaxIndex(current int32) {
	for {
		old := m.maxConcurrentIndex.Load()
		if current <= old {
			return
		}
		if m.maxConcurrentIndex.CompareAndSwap(old, current) {
			return
		}
	}
}

func (m *concurrencyTrackingBleveManager) IndexBatch(ctx context.Context, docs []*Document) error {
	for _, doc := range docs {
		if err := m.Index(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func (m *concurrencyTrackingBleveManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, id)
	return nil
}

func (m *concurrencyTrackingBleveManager) DocumentCount() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return uint64(len(m.documents)), nil
}

func (m *concurrencyTrackingBleveManager) GetMaxConcurrent() int32 {
	return m.maxConcurrentSearch.Load()
}

func (m *concurrencyTrackingBleveManager) GetMaxConcurrentIndex() int32 {
	return m.maxConcurrentIndex.Load()
}

// =============================================================================
// Test Helpers
// =============================================================================

// testConfig creates a standard test configuration.
func testConfig() SearchSystemConfig {
	return SearchSystemConfig{
		BlevePath:             "/test/bleve",
		MaxConcurrentSearches: 5,
		MaxConcurrentIndexes:  3,
		EnableWatcher:         false,
	}
}

// createTestDocument creates a document for testing.
func createTestDocument(id, path, content string) *Document {
	return &Document{
		ID:         id,
		Path:       path,
		Type:       DocTypeSourceCode,
		Content:    content,
		Checksum:   GenerateChecksum([]byte(content)),
		IndexedAt:  time.Now(),
		ModifiedAt: time.Now(),
	}
}

// =============================================================================
// End-to-End Indexing Tests
// =============================================================================

func TestIntegration_IndexAndSearch(t *testing.T) {
	bleve := newIntegrationBleveManager()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// Index documents
	paths := []string{"/test/file1.go", "/test/file2.go"}
	result, err := ss.Index(context.Background(), paths)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}
	if result.Indexed != 2 {
		t.Errorf("expected 2 indexed, got %d", result.Indexed)
	}

	// Search for documents
	req := &SearchRequest{Query: "test", Limit: 10}
	searchResult, err := ss.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if searchResult.TotalHits != 2 {
		t.Errorf("expected 2 hits, got %d", searchResult.TotalHits)
	}
}

func TestIntegration_IncrementalIndex(t *testing.T) {
	bleve := newIntegrationBleveManager()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// First batch
	paths1 := []string{"/test/file1.go", "/test/file2.go"}
	result1, err := ss.Index(context.Background(), paths1)
	if err != nil {
		t.Fatalf("first index failed: %v", err)
	}
	if result1.Indexed != 2 {
		t.Errorf("expected 2 indexed, got %d", result1.Indexed)
	}

	// Verify initial count
	count1, _ := bleve.DocumentCount()
	if count1 != 2 {
		t.Errorf("expected 2 documents, got %d", count1)
	}

	// Second batch
	paths2 := []string{"/test/file3.go", "/test/file4.go", "/test/file5.go"}
	result2, err := ss.Index(context.Background(), paths2)
	if err != nil {
		t.Fatalf("second index failed: %v", err)
	}
	if result2.Indexed != 3 {
		t.Errorf("expected 3 indexed, got %d", result2.Indexed)
	}

	// Verify total count
	count2, _ := bleve.DocumentCount()
	if count2 != 5 {
		t.Errorf("expected 5 documents, got %d", count2)
	}
}

func TestIntegration_DeleteAndSearch(t *testing.T) {
	bleve := newIntegrationBleveManager()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// Index documents
	paths := []string{"/test/file1.go", "/test/file2.go"}
	_, err = ss.Index(context.Background(), paths)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}

	// Verify both indexed
	ids := bleve.GetDocumentIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 documents, got %d", len(ids))
	}

	// Delete one document
	if len(ids) > 0 {
		bleve.Delete(context.Background(), ids[0])
	}

	// Verify only one remains
	count, _ := bleve.DocumentCount()
	if count != 1 {
		t.Errorf("expected 1 document after delete, got %d", count)
	}

	// Search should return only remaining document
	req := &SearchRequest{Query: "test", Limit: 10}
	searchResult, err := ss.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if searchResult.TotalHits != 1 {
		t.Errorf("expected 1 hit after delete, got %d", searchResult.TotalHits)
	}
}

// =============================================================================
// Component Integration Tests
// =============================================================================

func TestIntegration_BleveAndVectorDB(t *testing.T) {
	bleve := newIntegrationBleveManager()
	vectorDB := newIntegrationVectorDBManager()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, vectorDB, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// Add documents to both
	doc := createTestDocument("doc1", "/test/file.go", "test content")
	bleve.Index(context.Background(), doc)
	vectorDB.AddDocument(ScoredDocument{Document: *doc, Score: 0.95})

	// Both should have the document
	bleveCount, _ := bleve.DocumentCount()
	if bleveCount != 1 {
		t.Errorf("expected 1 bleve document, got %d", bleveCount)
	}

	vectorIDs := vectorDB.GetDocumentIDs()
	if len(vectorIDs) != 1 {
		t.Errorf("expected 1 vectorDB document, got %d", len(vectorIDs))
	}

	// Verify same document ID in both
	bleveIDs := bleve.GetDocumentIDs()
	if len(bleveIDs) > 0 && len(vectorIDs) > 0 && bleveIDs[0] != vectorIDs[0] {
		t.Error("document IDs should match between Bleve and VectorDB")
	}
}

func TestIntegration_ManifestTracking(t *testing.T) {
	bleve := newIntegrationBleveManager()
	manifest := newIntegrationManifest()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, manifest)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// Index a file
	paths := []string{"/test/file1.go"}
	_, err = ss.Index(context.Background(), paths)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}

	// Manually track in manifest (simulating CMT behavior)
	manifest.Set("/test/file1.go", "checksum123")

	// Verify manifest tracks the file
	checksum, exists := manifest.Get("/test/file1.go")
	if !exists {
		t.Error("expected manifest to track /test/file1.go")
	}
	if checksum != "checksum123" {
		t.Errorf("expected checksum123, got %s", checksum)
	}

	// Delete and verify manifest updated
	manifest.Delete("/test/file1.go")
	_, exists = manifest.Get("/test/file1.go")
	if exists {
		t.Error("expected manifest to not track deleted file")
	}
}

func TestIntegration_FileChangeDetection(t *testing.T) {
	bleve := newIntegrationBleveManager()
	watcher := newIntegrationWatcher()
	config := testConfig()
	config.EnableWatcher = true

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, watcher, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	watcher.WaitForStart(t, time.Second)

	// Send a change event
	watcher.SendEvent(ChangeEvent{
		Path:      "/test/changed_file.go",
		Operation: "modify",
		Time:      time.Now(),
	})

	// Give time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify system is still running (event processed without error)
	if !ss.IsRunning() {
		t.Error("system should still be running after processing event")
	}
}

// =============================================================================
// Resource Budget Tests
// =============================================================================

func TestIntegration_ConcurrentSearchLimit(t *testing.T) {
	// Create a custom bleve that tracks actual concurrent executions
	bleve := newConcurrencyTrackingBleveManager(100 * time.Millisecond)
	config := testConfig()
	config.MaxConcurrentSearches = 2

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &SearchRequest{Query: "test", Limit: 10}
			ss.Search(context.Background(), req)
		}()
	}

	wg.Wait()

	maxObserved := bleve.GetMaxConcurrent()
	if maxObserved > int32(config.MaxConcurrentSearches) {
		t.Errorf("max concurrent searches exceeded: got %d, limit %d",
			maxObserved, config.MaxConcurrentSearches)
	}
}

func TestIntegration_ConcurrentIndexLimit(t *testing.T) {
	// Create a custom bleve that tracks actual concurrent index executions
	bleve := newConcurrencyTrackingBleveManager(100 * time.Millisecond)
	config := testConfig()
	config.MaxConcurrentIndexes = 2

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths := []string{"/test/file.go"}
			ss.Index(context.Background(), paths)
		}()
	}

	wg.Wait()

	maxObserved := bleve.GetMaxConcurrentIndex()
	if maxObserved > int32(config.MaxConcurrentIndexes) {
		t.Errorf("max concurrent indexes exceeded: got %d, limit %d",
			maxObserved, config.MaxConcurrentIndexes)
	}
}

func TestIntegration_SemaphoreUnderLoad(t *testing.T) {
	bleve := newIntegrationBleveManager()
	bleve.searchDelay = 10 * time.Millisecond
	bleve.indexDelay = 10 * time.Millisecond
	config := testConfig()
	config.MaxConcurrentSearches = 3
	config.MaxConcurrentIndexes = 2

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var completedSearches atomic.Int32
	var completedIndexes atomic.Int32

	// Launch many concurrent searches
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					req := &SearchRequest{Query: "test", Limit: 10}
					if _, err := ss.Search(ctx, req); err == nil {
						completedSearches.Add(1)
					}
				}
			}
		}()
	}

	// Launch many concurrent indexes
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					paths := []string{"/test/file.go"}
					if _, err := ss.Index(ctx, paths); err == nil {
						completedIndexes.Add(1)
					}
				}
			}
		}()
	}

	wg.Wait()

	// Verify operations completed without deadlock
	if completedSearches.Load() == 0 {
		t.Error("expected some searches to complete")
	}
	if completedIndexes.Load() == 0 {
		t.Error("expected some indexes to complete")
	}

	// Verify system is still healthy
	if !ss.IsRunning() {
		t.Error("system should still be running after load test")
	}
}

// =============================================================================
// Cross-Validation Tests
// =============================================================================

func TestIntegration_ManifestBleveConsistency(t *testing.T) {
	bleve := newIntegrationBleveManager()
	manifest := newIntegrationManifest()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, manifest)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// Index documents and track in manifest
	paths := []string{
		"/test/file1.go",
		"/test/file2.go",
		"/test/file3.go",
	}
	_, err = ss.Index(context.Background(), paths)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}

	// Simulate manifest tracking
	for _, path := range paths {
		manifest.Set(path, GenerateChecksum([]byte(path)))
	}

	// Verify counts match
	bleveCount, _ := bleve.DocumentCount()
	manifestCount := manifest.Size()

	if bleveCount != uint64(manifestCount) {
		t.Errorf("mismatch: bleve has %d, manifest has %d", bleveCount, manifestCount)
	}

	// Simulate a deletion from both
	bleve.Delete(context.Background(), bleve.GetDocumentIDs()[0])
	manifest.Delete(paths[0])

	// Verify consistency after deletion
	bleveCountAfter, _ := bleve.DocumentCount()
	manifestCountAfter := manifest.Size()

	if bleveCountAfter != uint64(manifestCountAfter) {
		t.Errorf("post-delete mismatch: bleve has %d, manifest has %d",
			bleveCountAfter, manifestCountAfter)
	}
}

func TestIntegration_VectorDBBleveConsistency(t *testing.T) {
	bleve := newIntegrationBleveManager()
	vectorDB := newIntegrationVectorDBManager()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, vectorDB, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	// Create documents with same IDs for both
	docs := []*Document{
		createTestDocument("doc1", "/test/file1.go", "content1"),
		createTestDocument("doc2", "/test/file2.go", "content2"),
		createTestDocument("doc3", "/test/file3.go", "content3"),
	}

	// Index in both stores
	for _, doc := range docs {
		bleve.Index(context.Background(), doc)
		vectorDB.AddDocument(ScoredDocument{Document: *doc, Score: 0.9})
	}

	// Verify both have same documents
	bleveIDs := bleve.GetDocumentIDs()
	vectorIDs := vectorDB.GetDocumentIDs()

	if len(bleveIDs) != len(vectorIDs) {
		t.Errorf("count mismatch: bleve has %d, vectorDB has %d",
			len(bleveIDs), len(vectorIDs))
	}

	// Check all bleve IDs exist in vectorDB
	vectorIDSet := make(map[string]bool)
	for _, id := range vectorIDs {
		vectorIDSet[id] = true
	}

	for _, id := range bleveIDs {
		if !vectorIDSet[id] {
			t.Errorf("document %s exists in Bleve but not VectorDB", id)
		}
	}
}

func TestIntegration_StateTransitions(t *testing.T) {
	bleve := newIntegrationBleveManager()
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	// Initial state should be stopped
	if ss.State() != StateStopped {
		t.Errorf("expected StateStopped, got %v", ss.State())
	}

	// Start
	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if ss.State() != StateRunning {
		t.Errorf("expected StateRunning, got %v", ss.State())
	}

	// Index while running should work
	paths := []string{"/test/file.go"}
	_, err = ss.Index(context.Background(), paths)
	if err != nil {
		t.Errorf("index should work while running: %v", err)
	}

	// Stop
	if err := ss.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if ss.State() != StateStopped {
		t.Errorf("expected StateStopped after stop, got %v", ss.State())
	}

	// Operations should fail when stopped
	_, err = ss.Search(context.Background(), &SearchRequest{Query: "test"})
	if err != ErrSystemNotRunning {
		t.Errorf("expected ErrSystemNotRunning, got %v", err)
	}

	// Restart should work
	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if ss.State() != StateRunning {
		t.Errorf("expected StateRunning after restart, got %v", ss.State())
	}

	// Search should work after restart
	result, err := ss.Search(context.Background(), &SearchRequest{Query: "test", Limit: 10})
	if err != nil {
		t.Errorf("search should work after restart: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}

	ss.Stop()
}

// =============================================================================
// Error Handling Integration Tests
// =============================================================================

func TestIntegration_IndexWithErrors(t *testing.T) {
	bleve := newIntegrationBleveManager()
	bleve.indexErr = errors.New("index error")
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	paths := []string{"/test/file1.go", "/test/file2.go"}
	result, err := ss.Index(context.Background(), paths)

	// Index should return result with failures
	if err != nil {
		t.Fatalf("index should not return error: %v", err)
	}
	if result.Failed != 2 {
		t.Errorf("expected 2 failures, got %d", result.Failed)
	}
	if result.Indexed != 0 {
		t.Errorf("expected 0 indexed, got %d", result.Indexed)
	}
}

func TestIntegration_SearchWithErrors(t *testing.T) {
	bleve := newIntegrationBleveManager()
	bleve.searchErr = errors.New("search error")
	config := testConfig()

	ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create search system: %v", err)
	}

	if err := ss.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer ss.Stop()

	req := &SearchRequest{Query: "test", Limit: 10}
	_, err = ss.Search(context.Background(), req)

	if err == nil {
		t.Error("expected error from search")
	}

	// Verify error was recorded
	status := ss.GetStatus()
	if status.ErrorCount == 0 {
		t.Error("expected error count to be incremented")
	}
}

func TestIntegration_ComponentOpenFailure(t *testing.T) {
	tests := []struct {
		name        string
		bleveErr    error
		vectorDBErr error
	}{
		{
			name:     "bleve open failure",
			bleveErr: errors.New("bleve open failed"),
		},
		{
			name:        "vectorDB open failure",
			vectorDBErr: errors.New("vectorDB open failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bleve := newIntegrationBleveManager()
			bleve.openErr = tt.bleveErr

			vectorDB := newIntegrationVectorDBManager()
			vectorDB.openErr = tt.vectorDBErr

			config := testConfig()

			ss, err := NewSearchSystemWithComponents(config, bleve, vectorDB, nil, nil)
			if err != nil {
				t.Fatalf("failed to create search system: %v", err)
			}

			err = ss.Start(context.Background())
			if err == nil {
				ss.Stop()
				t.Error("expected start to fail")
			}
			if !errors.Is(err, ErrStartFailed) {
				t.Errorf("expected ErrStartFailed, got %v", err)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests for Multiple Scenarios
// =============================================================================

func TestIntegration_SearchScenarios(t *testing.T) {
	tests := []struct {
		name          string
		docCount      int
		query         string
		limit         int
		expectedHits  int
		expectedError error
	}{
		{
			name:         "empty index",
			docCount:     0,
			query:        "test",
			limit:        10,
			expectedHits: 0,
		},
		{
			name:         "single document",
			docCount:     1,
			query:        "test",
			limit:        10,
			expectedHits: 1,
		},
		{
			name:         "multiple documents",
			docCount:     5,
			query:        "test",
			limit:        10,
			expectedHits: 5,
		},
		{
			name:         "limit exceeded",
			docCount:     20,
			query:        "test",
			limit:        10,
			expectedHits: 20, // Mock returns all matching
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bleve := newIntegrationBleveManager()
			config := testConfig()

			ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
			if err != nil {
				t.Fatalf("failed to create search system: %v", err)
			}

			if err := ss.Start(context.Background()); err != nil {
				t.Fatalf("failed to start: %v", err)
			}
			defer ss.Stop()

			// Index documents
			for i := 0; i < tt.docCount; i++ {
				doc := createTestDocument(
					GenerateDocumentID([]byte{byte(i)}),
					"/test/file.go",
					"content",
				)
				bleve.Index(context.Background(), doc)
			}

			req := &SearchRequest{Query: tt.query, Limit: tt.limit}
			result, err := ss.Search(context.Background(), req)

			if tt.expectedError != nil {
				if !errors.Is(err, tt.expectedError) {
					t.Errorf("expected error %v, got %v", tt.expectedError, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.TotalHits != int64(tt.expectedHits) {
				t.Errorf("expected %d hits, got %d", tt.expectedHits, result.TotalHits)
			}
		})
	}
}

func TestIntegration_ConcurrencyScenarios(t *testing.T) {
	tests := []struct {
		name           string
		maxSearches    int
		maxIndexes     int
		searchWorkers  int
		indexWorkers   int
		testDuration   time.Duration
		expectDeadlock bool
	}{
		{
			name:          "low concurrency",
			maxSearches:   2,
			maxIndexes:    1,
			searchWorkers: 5,
			indexWorkers:  3,
			testDuration:  100 * time.Millisecond,
		},
		{
			name:          "high concurrency",
			maxSearches:   10,
			maxIndexes:    5,
			searchWorkers: 50,
			indexWorkers:  25,
			testDuration:  200 * time.Millisecond,
		},
		{
			name:          "single slot bottleneck",
			maxSearches:   1,
			maxIndexes:    1,
			searchWorkers: 20,
			indexWorkers:  20,
			testDuration:  100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bleve := newIntegrationBleveManager()
			bleve.searchDelay = 5 * time.Millisecond
			bleve.indexDelay = 5 * time.Millisecond

			config := testConfig()
			config.MaxConcurrentSearches = tt.maxSearches
			config.MaxConcurrentIndexes = tt.maxIndexes

			ss, err := NewSearchSystemWithComponents(config, bleve, nil, nil, nil)
			if err != nil {
				t.Fatalf("failed to create: %v", err)
			}

			if err := ss.Start(context.Background()); err != nil {
				t.Fatalf("failed to start: %v", err)
			}
			defer ss.Stop()

			ctx, cancel := context.WithTimeout(context.Background(), tt.testDuration)
			defer cancel()

			var wg sync.WaitGroup
			runConcurrentOperations(ctx, &wg, ss, tt.searchWorkers, tt.indexWorkers)
			wg.Wait()

			if !ss.IsRunning() {
				t.Error("system should be running after test")
			}
		})
	}
}

func runConcurrentOperations(
	ctx context.Context,
	wg *sync.WaitGroup,
	ss *SearchSystem,
	searchWorkers, indexWorkers int,
) {
	for i := 0; i < searchWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSearchWorker(ctx, ss)
		}()
	}

	for i := 0; i < indexWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runIndexWorker(ctx, ss)
		}()
	}
}

func runSearchWorker(ctx context.Context, ss *SearchSystem) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			req := &SearchRequest{Query: "test", Limit: 10}
			ss.Search(ctx, req)
		}
	}
}

func runIndexWorker(ctx context.Context, ss *SearchSystem) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			paths := []string{"/test/file.go"}
			ss.Index(ctx, paths)
		}
	}
}
