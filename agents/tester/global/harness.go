package global

import (
	"fmt"
	"sync"
)

// TestHarness manages test infrastructure for integration/e2e testing.
// Built during Phase 4 (constructHarness) by the LLM through the
// build_harness skill.
type TestHarness struct {
	Fixtures     []TestFixture   `json:"fixtures"`
	MockServers  []MockServer    `json:"mock_servers"`
	TestDBs      []TestDB        `json:"test_dbs"`
	CreatedFiles []string        `json:"created_files"`
	mu           sync.RWMutex
}

// TestFixture describes a test fixture created by the harness.
type TestFixture struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	File        string `json:"file"`
	Description string `json:"description"`
}

// MockServer describes a mock server for integration testing.
type MockServer struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Port     int    `json:"port"`
	File     string `json:"file"`
}

// TestDB describes a test database for integration testing.
type TestDB struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	DSN    string `json:"dsn"`
	File   string `json:"file"`
}

// NewTestHarness creates an empty test harness.
func NewTestHarness() *TestHarness {
	return &TestHarness{
		Fixtures:     make([]TestFixture, 0),
		MockServers:  make([]MockServer, 0),
		TestDBs:      make([]TestDB, 0),
		CreatedFiles: make([]string, 0),
	}
}

// AddFixture records a fixture created by the harness builder.
func (h *TestHarness) AddFixture(f TestFixture) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Fixtures = append(h.Fixtures, f)
}

// AddMockServer records a mock server created by the harness builder.
func (h *TestHarness) AddMockServer(m MockServer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.MockServers = append(h.MockServers, m)
}

// AddTestDB records a test database created by the harness builder.
func (h *TestHarness) AddTestDB(db TestDB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.TestDBs = append(h.TestDBs, db)
}

// TrackFile records a file created as part of the harness.
func (h *TestHarness) TrackFile(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.CreatedFiles = append(h.CreatedFiles, path)
}

// Summary returns a human-readable summary of the harness state.
func (h *TestHarness) Summary() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return fmt.Sprintf(
		"TestHarness: %d fixtures, %d mock servers, %d test DBs, %d created files",
		len(h.Fixtures), len(h.MockServers), len(h.TestDBs), len(h.CreatedFiles),
	)
}
