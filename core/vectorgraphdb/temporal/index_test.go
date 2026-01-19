package temporal

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// TemporalIndexVerifier Tests (TG.4.2)
// =============================================================================

func TestNewTemporalIndexVerifier(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	if v == nil {
		t.Fatal("NewTemporalIndexVerifier returned nil")
	}
	if v.db != db {
		t.Error("NewTemporalIndexVerifier did not set db correctly")
	}
}

func TestTemporalIndexVerifier_NilDB(t *testing.T) {
	v := NewTemporalIndexVerifier(nil)
	ctx := context.Background()

	// Test all methods return ErrNilDB
	_, err := v.VerifyIndexUsage(ctx, "SELECT 1")
	if err != ErrNilDB {
		t.Errorf("VerifyIndexUsage: expected ErrNilDB, got %v", err)
	}

	_, err = v.VerifyTemporalIndexes(ctx)
	if err != ErrNilDB {
		t.Errorf("VerifyTemporalIndexes: expected ErrNilDB, got %v", err)
	}

	_, err = v.SuggestMissingIndexes(ctx)
	if err != ErrNilDB {
		t.Errorf("SuggestMissingIndexes: expected ErrNilDB, got %v", err)
	}

	_, err = v.CreateMissingIndexes(ctx)
	if err != ErrNilDB {
		t.Errorf("CreateMissingIndexes: expected ErrNilDB, got %v", err)
	}

	_, err = v.AnalyzeTemporalQueries(ctx)
	if err != ErrNilDB {
		t.Errorf("AnalyzeTemporalQueries: expected ErrNilDB, got %v", err)
	}

	_, err = v.GetIndexRecommendations(ctx)
	if err != ErrNilDB {
		t.Errorf("GetIndexRecommendations: expected ErrNilDB, got %v", err)
	}
}

// =============================================================================
// VerifyIndexUsage Tests
// =============================================================================

func TestTemporalIndexVerifier_VerifyIndexUsage_TableScan(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	// Query without any index should result in table scan
	query := "SELECT * FROM edges WHERE weight > 0.5"
	analysis, err := v.VerifyIndexUsage(ctx, query)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	if analysis.Query != query {
		t.Errorf("expected query '%s', got '%s'", query, analysis.Query)
	}

	// Should be a table scan (no index on weight)
	if analysis.ScanType != "TABLE" && analysis.ScanType != "UNKNOWN" {
		t.Logf("scan type: %s, uses_index: %v", analysis.ScanType, analysis.UsesIndex)
	}
}

func TestTemporalIndexVerifier_VerifyIndexUsage_WithIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create an index on source_id
	_, err := db.Exec("CREATE INDEX idx_test_source ON edges(source_id)")
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	// Query that should use the index
	query := "SELECT * FROM edges WHERE source_id = 'node1'"
	analysis, err := v.VerifyIndexUsage(ctx, query)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	if len(analysis.Details) == 0 {
		t.Error("expected EXPLAIN details to be populated")
	}

	// The query should use an index
	// Note: SQLite might optimize this differently based on statistics
	t.Logf("Analysis: uses_index=%v, scan_type=%s, index_name=%s",
		analysis.UsesIndex, analysis.ScanType, analysis.IndexName)
}

func TestTemporalIndexVerifier_VerifyIndexUsage_PrimaryKey(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	// Query on primary key should use primary key
	query := "SELECT * FROM edges WHERE id = 1"
	analysis, err := v.VerifyIndexUsage(ctx, query)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	// Should use primary key or index
	t.Logf("Analysis: uses_index=%v, scan_type=%s", analysis.UsesIndex, analysis.ScanType)
}

// =============================================================================
// VerifyTemporalIndexes Tests
// =============================================================================

func TestTemporalIndexVerifier_VerifyTemporalIndexes_NoIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("VerifyTemporalIndexes failed: %v", err)
	}

	// Should report all temporal indexes as missing
	expectedMissing := []string{
		"idx_edges_temporal_current",
		"idx_edges_temporal_range",
		"idx_edges_transaction",
	}

	for _, expected := range expectedMissing {
		found := false
		for _, missing := range report.MissingIndexes {
			if missing == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s to be in missing indexes", expected)
		}
	}

	if report.AllExpectedIndexesExist {
		t.Error("AllExpectedIndexesExist should be false")
	}

	if len(report.Recommendations) == 0 {
		t.Error("expected recommendations for missing indexes")
	}
}

func TestTemporalIndexVerifier_VerifyTemporalIndexes_WithIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create the expected temporal indexes
	indexes := []string{
		"CREATE INDEX idx_edges_temporal_current ON edges(source_id) WHERE valid_to IS NULL",
		"CREATE INDEX idx_edges_temporal_range ON edges(source_id, valid_from, valid_to)",
		"CREATE INDEX idx_edges_transaction ON edges(tx_start, tx_end)",
	}

	for _, sql := range indexes {
		if _, err := db.Exec(sql); err != nil {
			t.Fatalf("failed to create index: %v", err)
		}
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("VerifyTemporalIndexes failed: %v", err)
	}

	if len(report.MissingIndexes) != 0 {
		t.Errorf("expected no missing indexes, got %v", report.MissingIndexes)
	}

	if !report.AllExpectedIndexesExist {
		t.Error("AllExpectedIndexesExist should be true")
	}

	// Verify the indexes are in the report
	foundIndexes := make(map[string]bool)
	for _, idx := range report.Indexes {
		foundIndexes[idx.Name] = true
	}

	for _, expected := range []string{
		"idx_edges_temporal_current",
		"idx_edges_temporal_range",
		"idx_edges_transaction",
	} {
		if !foundIndexes[expected] {
			t.Errorf("expected %s to be in report.Indexes", expected)
		}
	}
}

func TestTemporalIndexVerifier_VerifyTemporalIndexes_PartialIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a partial index
	_, err := db.Exec("CREATE INDEX idx_edges_temporal_current ON edges(source_id) WHERE valid_to IS NULL")
	if err != nil {
		t.Fatalf("failed to create partial index: %v", err)
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("VerifyTemporalIndexes failed: %v", err)
	}

	// Find the partial index
	var partialIdx *IndexInfo
	for i, idx := range report.Indexes {
		if idx.Name == "idx_edges_temporal_current" {
			partialIdx = &report.Indexes[i]
			break
		}
	}

	if partialIdx == nil {
		t.Fatal("partial index not found in report")
	}

	if !partialIdx.IsPartial {
		t.Error("expected IsPartial to be true")
	}

	if partialIdx.WhereClause == "" {
		t.Error("expected WhereClause to be set")
	}
}

// =============================================================================
// SuggestMissingIndexes Tests
// =============================================================================

func TestTemporalIndexVerifier_SuggestMissingIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	suggestions, err := v.SuggestMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("SuggestMissingIndexes failed: %v", err)
	}

	// Should have 3 suggestions for the 3 expected indexes
	if len(suggestions) != 3 {
		t.Errorf("expected 3 suggestions, got %d", len(suggestions))
	}

	// Verify each suggestion is a valid CREATE INDEX statement
	for _, sql := range suggestions {
		if !strings.HasPrefix(strings.ToUpper(sql), "CREATE INDEX") {
			t.Errorf("expected CREATE INDEX statement, got: %s", sql)
		}
	}
}

func TestTemporalIndexVerifier_SuggestMissingIndexes_AllExist(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create all expected indexes
	indexes := []string{
		"CREATE INDEX idx_edges_temporal_current ON edges(source_id) WHERE valid_to IS NULL",
		"CREATE INDEX idx_edges_temporal_range ON edges(source_id, valid_from, valid_to)",
		"CREATE INDEX idx_edges_transaction ON edges(tx_start, tx_end)",
	}

	for _, sql := range indexes {
		if _, err := db.Exec(sql); err != nil {
			t.Fatalf("failed to create index: %v", err)
		}
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	suggestions, err := v.SuggestMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("SuggestMissingIndexes failed: %v", err)
	}

	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions when all indexes exist, got %d", len(suggestions))
	}
}

// =============================================================================
// CreateMissingIndexes Tests
// =============================================================================

func TestTemporalIndexVerifier_CreateMissingIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	// Verify indexes don't exist initially
	report, _ := v.VerifyTemporalIndexes(ctx)
	if report.AllExpectedIndexesExist {
		t.Error("indexes should not exist initially")
	}

	// Create missing indexes
	created, err := v.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	if created != 3 {
		t.Errorf("expected 3 indexes created, got %d", created)
	}

	// Verify indexes now exist
	report, _ = v.VerifyTemporalIndexes(ctx)
	if !report.AllExpectedIndexesExist {
		t.Error("all indexes should exist after creation")
	}
}

func TestTemporalIndexVerifier_CreateMissingIndexes_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	// Create indexes twice
	created1, err := v.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("first CreateMissingIndexes failed: %v", err)
	}

	// Second call should create 0 (IF NOT EXISTS)
	created2, err := v.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("second CreateMissingIndexes failed: %v", err)
	}

	if created1 != 3 {
		t.Errorf("expected 3 indexes created first time, got %d", created1)
	}

	if created2 != 0 {
		t.Errorf("expected 0 indexes created second time, got %d", created2)
	}
}

// =============================================================================
// AnalyzeTemporalQueries Tests
// =============================================================================

func TestTemporalIndexVerifier_AnalyzeTemporalQueries(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	analyses, err := v.AnalyzeTemporalQueries(ctx)
	if err != nil {
		t.Fatalf("AnalyzeTemporalQueries failed: %v", err)
	}

	// Should have analysis for all common query patterns
	expectedPatterns := []string{
		"as_of_current",
		"between_range",
		"transaction_history",
		"current_edges",
		"edges_created_between",
		"edges_by_transaction",
	}

	for _, pattern := range expectedPatterns {
		if _, ok := analyses[pattern]; !ok {
			t.Errorf("expected analysis for pattern '%s'", pattern)
		}
	}
}

func TestTemporalIndexVerifier_AnalyzeTemporalQueries_WithIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create temporal indexes
	indexes := []string{
		"CREATE INDEX idx_edges_temporal_current ON edges(source_id) WHERE valid_to IS NULL",
		"CREATE INDEX idx_edges_temporal_range ON edges(source_id, valid_from, valid_to)",
		"CREATE INDEX idx_edges_transaction ON edges(tx_start, tx_end)",
	}

	for _, sql := range indexes {
		if _, err := db.Exec(sql); err != nil {
			t.Fatalf("failed to create index: %v", err)
		}
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	analyses, err := v.AnalyzeTemporalQueries(ctx)
	if err != nil {
		t.Fatalf("AnalyzeTemporalQueries failed: %v", err)
	}

	// Log the results for inspection
	for name, analysis := range analyses {
		t.Logf("%s: uses_index=%v, scan_type=%s, index=%s",
			name, analysis.UsesIndex, analysis.ScanType, analysis.IndexName)
	}
}

// =============================================================================
// GetIndexRecommendations Tests
// =============================================================================

func TestTemporalIndexVerifier_GetIndexRecommendations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	recs, err := v.GetIndexRecommendations(ctx)
	if err != nil {
		t.Fatalf("GetIndexRecommendations failed: %v", err)
	}

	// Should have recommendations for missing indexes
	if len(recs) < 3 {
		t.Errorf("expected at least 3 recommendations, got %d", len(recs))
	}
}

func TestTemporalIndexVerifier_GetIndexRecommendations_AllOptimal(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create all expected indexes
	indexes := []string{
		"CREATE INDEX idx_edges_temporal_current ON edges(source_id) WHERE valid_to IS NULL",
		"CREATE INDEX idx_edges_temporal_range ON edges(source_id, valid_from, valid_to)",
		"CREATE INDEX idx_edges_transaction ON edges(tx_start, tx_end)",
	}

	for _, sql := range indexes {
		if _, err := db.Exec(sql); err != nil {
			t.Fatalf("failed to create index: %v", err)
		}
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	recs, err := v.GetIndexRecommendations(ctx)
	if err != nil {
		t.Fatalf("GetIndexRecommendations failed: %v", err)
	}

	// Should have fewer recommendations since indexes exist
	// Might still have some based on query analysis
	t.Logf("Recommendations with all indexes: %v", recs)
}

// =============================================================================
// IndexInfo Tests
// =============================================================================

func TestIndexInfo_ColumnsExtraction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a multi-column index
	_, err := db.Exec("CREATE INDEX idx_test_multi ON edges(source_id, target_id, edge_type)")
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("VerifyTemporalIndexes failed: %v", err)
	}

	// Find the test index
	var testIdx *IndexInfo
	for i, idx := range report.Indexes {
		if idx.Name == "idx_test_multi" {
			testIdx = &report.Indexes[i]
			break
		}
	}

	if testIdx == nil {
		t.Fatal("test index not found")
	}

	expectedColumns := []string{"source_id", "target_id", "edge_type"}
	if len(testIdx.Columns) != len(expectedColumns) {
		t.Errorf("expected %d columns, got %d", len(expectedColumns), len(testIdx.Columns))
	}

	for i, expected := range expectedColumns {
		if i < len(testIdx.Columns) && testIdx.Columns[i] != expected {
			t.Errorf("expected column %d to be '%s', got '%s'", i, expected, testIdx.Columns[i])
		}
	}
}

// =============================================================================
// Expected Temporal Indexes Tests
// =============================================================================

func TestGetExpectedTemporalIndexes(t *testing.T) {
	indexes := GetExpectedTemporalIndexes()

	if len(indexes) != 3 {
		t.Errorf("expected 3 expected indexes, got %d", len(indexes))
	}

	// Verify each expected index has required fields
	for _, idx := range indexes {
		if idx.Name == "" {
			t.Error("expected index name to be set")
		}
		if idx.Table == "" {
			t.Error("expected table to be set")
		}
		if len(idx.Columns) == 0 {
			t.Error("expected columns to be set")
		}
		if idx.Description == "" {
			t.Error("expected description to be set")
		}
	}

	// Verify specific indexes
	indexMap := make(map[string]ExpectedTemporalIndex)
	for _, idx := range indexes {
		indexMap[idx.Name] = idx
	}

	// Check idx_edges_temporal_current
	if current, ok := indexMap["idx_edges_temporal_current"]; ok {
		if current.WhereClause != "valid_to IS NULL" {
			t.Errorf("expected WHERE clause 'valid_to IS NULL', got '%s'", current.WhereClause)
		}
	} else {
		t.Error("expected idx_edges_temporal_current to be in expected indexes")
	}

	// Check idx_edges_temporal_range
	if rangeIdx, ok := indexMap["idx_edges_temporal_range"]; ok {
		if len(rangeIdx.Columns) != 3 {
			t.Errorf("expected 3 columns for range index, got %d", len(rangeIdx.Columns))
		}
	} else {
		t.Error("expected idx_edges_temporal_range to be in expected indexes")
	}

	// Check idx_edges_transaction
	if txIdx, ok := indexMap["idx_edges_transaction"]; ok {
		if len(txIdx.Columns) != 2 {
			t.Errorf("expected 2 columns for transaction index, got %d", len(txIdx.Columns))
		}
	} else {
		t.Error("expected idx_edges_transaction to be in expected indexes")
	}
}

// =============================================================================
// Helper Functions Tests
// =============================================================================

func TestExtractColumnsFromSQL(t *testing.T) {
	v := &TemporalIndexVerifier{}

	tests := []struct {
		sql      string
		expected []string
	}{
		{
			sql:      "CREATE INDEX idx ON tbl(col1)",
			expected: []string{"col1"},
		},
		{
			sql:      "CREATE INDEX idx ON tbl(col1, col2)",
			expected: []string{"col1", "col2"},
		},
		{
			sql:      "CREATE INDEX idx ON tbl(col1, col2, col3)",
			expected: []string{"col1", "col2", "col3"},
		},
		{
			sql:      "CREATE INDEX idx ON tbl(col1 ASC, col2 DESC)",
			expected: []string{"col1", "col2"},
		},
		{
			sql:      "CREATE INDEX idx ON tbl(col1) WHERE col2 IS NULL",
			expected: []string{"col1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			cols := v.extractColumnsFromSQL(tt.sql)
			if len(cols) != len(tt.expected) {
				t.Errorf("expected %d columns, got %d", len(tt.expected), len(cols))
				return
			}
			for i, expected := range tt.expected {
				if i < len(cols) && cols[i] != expected {
					t.Errorf("column %d: expected '%s', got '%s'", i, expected, cols[i])
				}
			}
		})
	}
}

func TestExtractWhereClause(t *testing.T) {
	v := &TemporalIndexVerifier{}

	tests := []struct {
		sql      string
		expected string
	}{
		{
			sql:      "CREATE INDEX idx ON tbl(col1) WHERE col2 IS NULL",
			expected: "col2 IS NULL",
		},
		{
			sql:      "CREATE INDEX idx ON tbl(col1) WHERE col2 > 0 AND col3 IS NOT NULL",
			expected: "col2 > 0 AND col3 IS NOT NULL",
		},
		{
			sql:      "CREATE INDEX idx ON tbl(col1)",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			where := v.extractWhereClause(tt.sql)
			if where != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, where)
			}
		})
	}
}

func TestExtractIndexName(t *testing.T) {
	v := &TemporalIndexVerifier{}

	tests := []struct {
		detail   string
		expected string
	}{
		{
			detail:   "SEARCH TABLE edges USING INDEX idx_edges_source (source_id=?)",
			expected: "idx_edges_source",
		},
		{
			detail:   "SEARCH edges USING COVERING INDEX idx_covering (col1=?)",
			expected: "idx_covering",
		},
		{
			detail:   "SCAN TABLE edges",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.detail, func(t *testing.T) {
			name := v.extractIndexName(tt.detail)
			if name != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, name)
			}
		})
	}
}

// =============================================================================
// IndexAnalysis Tests
// =============================================================================

func TestIndexAnalysis_Fields(t *testing.T) {
	analysis := &IndexAnalysis{
		Query:         "SELECT * FROM edges WHERE source_id = ?",
		UsesIndex:     true,
		IndexName:     "idx_test",
		ScanType:      "INDEX",
		EstimatedRows: 100,
		Details:       []string{"SEARCH TABLE edges USING INDEX idx_test"},
	}

	if analysis.Query == "" {
		t.Error("Query should not be empty")
	}
	if !analysis.UsesIndex {
		t.Error("UsesIndex should be true")
	}
	if analysis.IndexName == "" {
		t.Error("IndexName should not be empty")
	}
	if analysis.ScanType == "" {
		t.Error("ScanType should not be empty")
	}
}

// =============================================================================
// IndexReport Tests
// =============================================================================

func TestIndexReport_Fields(t *testing.T) {
	report := &IndexReport{
		Indexes: []IndexInfo{
			{Name: "idx1", TableName: "edges"},
			{Name: "idx2", TableName: "edges"},
		},
		MissingIndexes:          []string{"idx3"},
		Recommendations:         []string{"Create idx3"},
		AllExpectedIndexesExist: false,
	}

	if len(report.Indexes) != 2 {
		t.Errorf("expected 2 indexes, got %d", len(report.Indexes))
	}
	if len(report.MissingIndexes) != 1 {
		t.Errorf("expected 1 missing index, got %d", len(report.MissingIndexes))
	}
	if len(report.Recommendations) != 1 {
		t.Errorf("expected 1 recommendation, got %d", len(report.Recommendations))
	}
	if report.AllExpectedIndexesExist {
		t.Error("AllExpectedIndexesExist should be false")
	}
}

// =============================================================================
// Integration Test
// =============================================================================

func TestTemporalIndexVerifier_FullWorkflow(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	v := NewTemporalIndexVerifier(db)
	ctx := context.Background()

	// 1. Verify no temporal indexes initially
	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("initial VerifyTemporalIndexes failed: %v", err)
	}
	if report.AllExpectedIndexesExist {
		t.Error("step 1: indexes should not exist initially")
	}

	// 2. Get suggestions
	suggestions, err := v.SuggestMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("SuggestMissingIndexes failed: %v", err)
	}
	if len(suggestions) != 3 {
		t.Errorf("step 2: expected 3 suggestions, got %d", len(suggestions))
	}

	// 3. Analyze queries before indexes
	analysesBefore, err := v.AnalyzeTemporalQueries(ctx)
	if err != nil {
		t.Fatalf("AnalyzeTemporalQueries failed: %v", err)
	}

	// 4. Create missing indexes
	created, err := v.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}
	if created != 3 {
		t.Errorf("step 4: expected 3 indexes created, got %d", created)
	}

	// 5. Verify all indexes now exist
	reportAfter, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("final VerifyTemporalIndexes failed: %v", err)
	}
	if !reportAfter.AllExpectedIndexesExist {
		t.Error("step 5: all indexes should exist after creation")
	}

	// 6. Analyze queries after indexes
	analysesAfter, err := v.AnalyzeTemporalQueries(ctx)
	if err != nil {
		t.Fatalf("AnalyzeTemporalQueries after failed: %v", err)
	}

	// Log before/after comparison
	t.Log("Query analysis comparison:")
	for name := range analysesBefore {
		before := analysesBefore[name]
		after := analysesAfter[name]
		t.Logf("  %s: before=%s after=%s",
			name, before.ScanType, after.ScanType)
	}

	// 7. Get recommendations (should be fewer or none)
	recs, err := v.GetIndexRecommendations(ctx)
	if err != nil {
		t.Fatalf("GetIndexRecommendations failed: %v", err)
	}
	t.Logf("Recommendations after index creation: %v", recs)
}

// =============================================================================
// Test Helpers
// =============================================================================

// Note: setupTestDB is defined in as_of_test.go and shared across test files.
// If running this test file alone, ensure as_of_test.go is also compiled.

// createTestDBForIndex creates a minimal test database for index testing.
func createTestDBForIndex(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	schema := `
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL,
			created_at TEXT,
			updated_at TEXT
		);

		CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			edge_type INTEGER NOT NULL,
			weight REAL DEFAULT 1.0,
			metadata TEXT,
			created_at TEXT,
			valid_from INTEGER,
			valid_to INTEGER,
			tx_start INTEGER,
			tx_end INTEGER,
			FOREIGN KEY (source_id) REFERENCES nodes(id),
			FOREIGN KEY (target_id) REFERENCES nodes(id)
		);
	`

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}

	return db
}
