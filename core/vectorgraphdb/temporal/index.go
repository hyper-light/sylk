// Package temporal provides temporal query capabilities for VectorGraphDB.
// This file implements TG.4.2 - Temporal Index Optimization with EXPLAIN QUERY PLAN
// analysis for verifying and optimizing temporal query index usage.
package temporal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// =============================================================================
// Index Analysis Types (TG.4.2)
// =============================================================================

// IndexAnalysis contains the result of analyzing a query's index usage
// using EXPLAIN QUERY PLAN.
type IndexAnalysis struct {
	// Query is the SQL query that was analyzed.
	Query string `json:"query"`

	// UsesIndex indicates whether the query uses any index.
	UsesIndex bool `json:"uses_index"`

	// IndexName is the name of the index used, if any.
	IndexName string `json:"index_name,omitempty"`

	// ScanType indicates the type of scan performed (INDEX, TABLE, COVERING INDEX, etc.).
	ScanType string `json:"scan_type"`

	// EstimatedRows is the estimated number of rows scanned (if available).
	EstimatedRows int `json:"estimated_rows,omitempty"`

	// Details contains the raw EXPLAIN QUERY PLAN output for debugging.
	Details []string `json:"details,omitempty"`
}

// IndexInfo contains information about a database index.
type IndexInfo struct {
	// Name is the index name.
	Name string `json:"name"`

	// TableName is the table the index belongs to.
	TableName string `json:"table_name"`

	// Columns is the list of columns in the index.
	Columns []string `json:"columns"`

	// IsUnique indicates if this is a unique index.
	IsUnique bool `json:"is_unique"`

	// IsPartial indicates if this is a partial index (has WHERE clause).
	IsPartial bool `json:"is_partial"`

	// WhereClause is the WHERE clause for partial indexes, if any.
	WhereClause string `json:"where_clause,omitempty"`

	// SQL is the original CREATE INDEX statement.
	SQL string `json:"sql,omitempty"`
}

// IndexReport contains the results of verifying temporal indexes.
type IndexReport struct {
	// Indexes contains information about all found temporal indexes.
	Indexes []IndexInfo `json:"indexes"`

	// MissingIndexes contains names of expected temporal indexes that are missing.
	MissingIndexes []string `json:"missing_indexes,omitempty"`

	// Recommendations contains suggestions for index optimization.
	Recommendations []string `json:"recommendations,omitempty"`

	// AllExpectedIndexesExist indicates whether all expected temporal indexes exist.
	AllExpectedIndexesExist bool `json:"all_expected_indexes_exist"`
}

// ExpectedTemporalIndex defines an expected temporal index.
type ExpectedTemporalIndex struct {
	// Name is the expected index name.
	Name string

	// Table is the table the index should be on.
	Table string

	// Columns are the expected columns (in order).
	Columns []string

	// WhereClause is the expected WHERE clause for partial indexes.
	WhereClause string

	// Description explains the purpose of this index.
	Description string
}

// =============================================================================
// Expected Temporal Indexes
// =============================================================================

// GetExpectedTemporalIndexes returns the list of expected temporal indexes
// for optimal performance of temporal queries.
func GetExpectedTemporalIndexes() []ExpectedTemporalIndex {
	return []ExpectedTemporalIndex{
		{
			Name:        "idx_edges_temporal_current",
			Table:       "edges",
			Columns:     []string{"source_id"},
			WhereClause: "valid_to IS NULL",
			Description: "Optimizes queries for currently valid edges (no end time)",
		},
		{
			Name:        "idx_edges_temporal_range",
			Table:       "edges",
			Columns:     []string{"source_id", "valid_from", "valid_to"},
			WhereClause: "",
			Description: "Optimizes range-based temporal queries",
		},
		{
			Name:        "idx_edges_transaction",
			Table:       "edges",
			Columns:     []string{"tx_start", "tx_end"},
			WhereClause: "",
			Description: "Optimizes transaction time queries",
		},
	}
}

// =============================================================================
// TemporalIndexVerifier (TG.4.2)
// =============================================================================

// TemporalIndexVerifier provides methods for verifying and analyzing
// temporal index usage in SQLite queries.
type TemporalIndexVerifier struct {
	db *sql.DB
}

// NewTemporalIndexVerifier creates a new TemporalIndexVerifier with the given
// database connection.
func NewTemporalIndexVerifier(db *sql.DB) *TemporalIndexVerifier {
	return &TemporalIndexVerifier{db: db}
}

// =============================================================================
// Query Analysis
// =============================================================================

// VerifyIndexUsage analyzes a query using EXPLAIN QUERY PLAN to determine
// if indexes are being used effectively.
//
// Returns an IndexAnalysis containing information about index usage.
func (v *TemporalIndexVerifier) VerifyIndexUsage(ctx context.Context, query string) (*IndexAnalysis, error) {
	if v.db == nil {
		return nil, ErrNilDB
	}

	analysis := &IndexAnalysis{
		Query:     query,
		UsesIndex: false,
		ScanType:  "UNKNOWN",
		Details:   make([]string, 0),
	}

	// Run EXPLAIN QUERY PLAN
	explainQuery := "EXPLAIN QUERY PLAN " + query
	rows, err := v.db.QueryContext(ctx, explainQuery)
	if err != nil {
		return nil, fmt.Errorf("explain query plan: %w", err)
	}
	defer rows.Close()

	// Parse the EXPLAIN QUERY PLAN output
	// SQLite returns: id, parent, notused, detail
	for rows.Next() {
		var id, parent, notused int
		var detail string

		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return nil, fmt.Errorf("scan explain result: %w", err)
		}

		analysis.Details = append(analysis.Details, detail)

		// Parse the detail string to extract index information
		v.parseExplainDetail(detail, analysis)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate explain results: %w", err)
	}

	return analysis, nil
}

// parseExplainDetail extracts index usage information from EXPLAIN QUERY PLAN detail.
func (v *TemporalIndexVerifier) parseExplainDetail(detail string, analysis *IndexAnalysis) {
	upperDetail := strings.ToUpper(detail)

	// Check for different scan types
	switch {
	case strings.Contains(upperDetail, "USING COVERING INDEX"):
		analysis.UsesIndex = true
		analysis.ScanType = "COVERING INDEX"
		analysis.IndexName = v.extractIndexName(detail)

	case strings.Contains(upperDetail, "USING INDEX"):
		analysis.UsesIndex = true
		analysis.ScanType = "INDEX"
		analysis.IndexName = v.extractIndexName(detail)

	case strings.Contains(upperDetail, "USING INTEGER PRIMARY KEY"):
		analysis.UsesIndex = true
		analysis.ScanType = "PRIMARY KEY"

	case strings.Contains(upperDetail, "SCAN TABLE") || strings.Contains(upperDetail, "SCAN"):
		// Only set to TABLE if we haven't found an index yet
		if !analysis.UsesIndex {
			analysis.ScanType = "TABLE"
		}

	case strings.Contains(upperDetail, "SEARCH"):
		// SEARCH typically uses an index
		if strings.Contains(upperDetail, "INDEX") {
			analysis.UsesIndex = true
			analysis.ScanType = "INDEX"
			analysis.IndexName = v.extractIndexName(detail)
		}
	}
}

// extractIndexName extracts the index name from an EXPLAIN QUERY PLAN detail string.
func (v *TemporalIndexVerifier) extractIndexName(detail string) string {
	// Look for patterns like "USING INDEX idx_name" or "USING COVERING INDEX idx_name"
	patterns := []string{
		"USING COVERING INDEX ",
		"USING INDEX ",
		"INDEX ",
	}

	upperDetail := strings.ToUpper(detail)
	for _, pattern := range patterns {
		if idx := strings.Index(upperDetail, pattern); idx != -1 {
			// Extract the name starting after the pattern
			start := idx + len(pattern)
			remaining := detail[start:]

			// Find the end of the index name (space, paren, or end of string)
			end := len(remaining)
			for i, c := range remaining {
				if c == ' ' || c == '(' || c == ')' {
					end = i
					break
				}
			}

			if end > 0 {
				return strings.TrimSpace(remaining[:end])
			}
		}
	}

	return ""
}

// =============================================================================
// Index Verification
// =============================================================================

// VerifyTemporalIndexes checks that all expected temporal indexes exist
// and returns a report of the findings.
func (v *TemporalIndexVerifier) VerifyTemporalIndexes(ctx context.Context) (*IndexReport, error) {
	if v.db == nil {
		return nil, ErrNilDB
	}

	report := &IndexReport{
		Indexes:                 make([]IndexInfo, 0),
		MissingIndexes:          make([]string, 0),
		Recommendations:         make([]string, 0),
		AllExpectedIndexesExist: true,
	}

	// Get all existing indexes
	existingIndexes, err := v.getExistingIndexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("get existing indexes: %w", err)
	}

	// Build a map for quick lookup
	existingMap := make(map[string]IndexInfo)
	for _, idx := range existingIndexes {
		existingMap[idx.Name] = idx
	}

	// Check for expected temporal indexes
	expectedIndexes := GetExpectedTemporalIndexes()
	for _, expected := range expectedIndexes {
		if existing, ok := existingMap[expected.Name]; ok {
			report.Indexes = append(report.Indexes, existing)
		} else {
			report.MissingIndexes = append(report.MissingIndexes, expected.Name)
			report.AllExpectedIndexesExist = false
			report.Recommendations = append(report.Recommendations,
				fmt.Sprintf("Create index %s for %s", expected.Name, expected.Description))
		}
	}

	// Add any other edges-related indexes to the report
	for _, idx := range existingIndexes {
		if idx.TableName == "edges" {
			found := false
			for _, reportIdx := range report.Indexes {
				if reportIdx.Name == idx.Name {
					found = true
					break
				}
			}
			if !found {
				report.Indexes = append(report.Indexes, idx)
			}
		}
	}

	return report, nil
}

// getExistingIndexes retrieves all indexes from the database.
func (v *TemporalIndexVerifier) getExistingIndexes(ctx context.Context) ([]IndexInfo, error) {
	query := `
		SELECT name, tbl_name, sql
		FROM sqlite_master
		WHERE type = 'index'
		AND name NOT LIKE 'sqlite_%'
		ORDER BY tbl_name, name
	`

	rows, err := v.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query indexes: %w", err)
	}
	defer rows.Close()

	var indexes []IndexInfo
	for rows.Next() {
		var name, tableName string
		var sqlStmt sql.NullString

		if err := rows.Scan(&name, &tableName, &sqlStmt); err != nil {
			return nil, fmt.Errorf("scan index row: %w", err)
		}

		idx := IndexInfo{
			Name:      name,
			TableName: tableName,
		}

		if sqlStmt.Valid {
			idx.SQL = sqlStmt.String
			idx.Columns = v.extractColumnsFromSQL(sqlStmt.String)
			idx.IsUnique = strings.Contains(strings.ToUpper(sqlStmt.String), "UNIQUE")
			idx.IsPartial = strings.Contains(strings.ToUpper(sqlStmt.String), "WHERE")
			if idx.IsPartial {
				idx.WhereClause = v.extractWhereClause(sqlStmt.String)
			}
		}

		indexes = append(indexes, idx)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate index rows: %w", err)
	}

	return indexes, nil
}

// extractColumnsFromSQL extracts column names from a CREATE INDEX statement.
func (v *TemporalIndexVerifier) extractColumnsFromSQL(sql string) []string {
	// Look for content between parentheses
	start := strings.Index(sql, "(")
	end := strings.LastIndex(sql, ")")

	if start == -1 || end == -1 || start >= end {
		return nil
	}

	// Extract the column list
	columnPart := sql[start+1 : end]

	// Handle WHERE clause
	if whereIdx := strings.Index(strings.ToUpper(columnPart), " WHERE"); whereIdx != -1 {
		columnPart = columnPart[:whereIdx]
	}

	// Split by comma and clean up
	parts := strings.Split(columnPart, ",")
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		col := strings.TrimSpace(part)
		// Remove any ordering (ASC, DESC)
		col = strings.TrimSuffix(col, " ASC")
		col = strings.TrimSuffix(col, " DESC")
		col = strings.TrimSuffix(col, " asc")
		col = strings.TrimSuffix(col, " desc")
		if col != "" {
			columns = append(columns, col)
		}
	}

	return columns
}

// extractWhereClause extracts the WHERE clause from a partial index definition.
func (v *TemporalIndexVerifier) extractWhereClause(sql string) string {
	upperSQL := strings.ToUpper(sql)
	whereIdx := strings.Index(upperSQL, "WHERE")
	if whereIdx == -1 {
		return ""
	}

	// Return everything after WHERE
	return strings.TrimSpace(sql[whereIdx+5:])
}

// =============================================================================
// Index Suggestions
// =============================================================================

// SuggestMissingIndexes returns SQL statements to create missing temporal indexes.
func (v *TemporalIndexVerifier) SuggestMissingIndexes(ctx context.Context) ([]string, error) {
	if v.db == nil {
		return nil, ErrNilDB
	}

	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		return nil, err
	}

	if len(report.MissingIndexes) == 0 {
		return []string{}, nil
	}

	suggestions := make([]string, 0, len(report.MissingIndexes))
	expectedIndexes := GetExpectedTemporalIndexes()

	for _, missingName := range report.MissingIndexes {
		for _, expected := range expectedIndexes {
			if expected.Name == missingName {
				sql := v.generateCreateIndexSQL(expected)
				suggestions = append(suggestions, sql)
				break
			}
		}
	}

	return suggestions, nil
}

// generateCreateIndexSQL generates a CREATE INDEX statement for an expected index.
func (v *TemporalIndexVerifier) generateCreateIndexSQL(expected ExpectedTemporalIndex) string {
	var sb strings.Builder

	sb.WriteString("CREATE INDEX IF NOT EXISTS ")
	sb.WriteString(expected.Name)
	sb.WriteString(" ON ")
	sb.WriteString(expected.Table)
	sb.WriteString(" (")
	sb.WriteString(strings.Join(expected.Columns, ", "))
	sb.WriteString(")")

	if expected.WhereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(expected.WhereClause)
	}

	return sb.String()
}

// CreateMissingIndexes creates all missing temporal indexes in the database.
// Returns the number of indexes created.
func (v *TemporalIndexVerifier) CreateMissingIndexes(ctx context.Context) (int, error) {
	if v.db == nil {
		return 0, ErrNilDB
	}

	suggestions, err := v.SuggestMissingIndexes(ctx)
	if err != nil {
		return 0, err
	}

	created := 0
	for _, sql := range suggestions {
		if _, err := v.db.ExecContext(ctx, sql); err != nil {
			return created, fmt.Errorf("create index: %w", err)
		}
		created++
	}

	return created, nil
}

// =============================================================================
// Query Performance Analysis
// =============================================================================

// AnalyzeTemporalQueries runs EXPLAIN QUERY PLAN on common temporal query patterns
// and returns an analysis of index usage for each.
func (v *TemporalIndexVerifier) AnalyzeTemporalQueries(ctx context.Context) (map[string]*IndexAnalysis, error) {
	if v.db == nil {
		return nil, ErrNilDB
	}

	// Common temporal query patterns to analyze
	queries := map[string]string{
		"as_of_current": `
			SELECT * FROM edges
			WHERE source_id = ?
			AND (valid_from IS NULL OR valid_from <= ?)
			AND (valid_to IS NULL OR valid_to > ?)
		`,
		"between_range": `
			SELECT * FROM edges
			WHERE source_id = ?
			AND (valid_from IS NULL OR valid_from < ?)
			AND (valid_to IS NULL OR valid_to > ?)
		`,
		"transaction_history": `
			SELECT * FROM edges
			WHERE id = ?
			ORDER BY tx_start DESC
		`,
		"current_edges": `
			SELECT * FROM edges
			WHERE source_id = ?
			AND valid_to IS NULL
		`,
		"edges_created_between": `
			SELECT * FROM edges
			WHERE valid_from >= ?
			AND valid_from < ?
		`,
		"edges_by_transaction": `
			SELECT * FROM edges
			WHERE tx_start >= ?
			AND tx_start < ?
			AND tx_end IS NOT NULL
		`,
	}

	results := make(map[string]*IndexAnalysis)

	for name, query := range queries {
		analysis, err := v.VerifyIndexUsage(ctx, query)
		if err != nil {
			// Log the error but continue with other queries
			analysis = &IndexAnalysis{
				Query:     query,
				UsesIndex: false,
				ScanType:  "ERROR",
				Details:   []string{err.Error()},
			}
		}
		results[name] = analysis
	}

	return results, nil
}

// GetIndexRecommendations analyzes temporal query patterns and returns
// recommendations for improving index coverage.
func (v *TemporalIndexVerifier) GetIndexRecommendations(ctx context.Context) ([]string, error) {
	if v.db == nil {
		return nil, ErrNilDB
	}

	var recommendations []string

	// Check temporal index report
	report, err := v.VerifyTemporalIndexes(ctx)
	if err != nil {
		return nil, err
	}

	// Add recommendations from the report
	recommendations = append(recommendations, report.Recommendations...)

	// Analyze query patterns
	analyses, err := v.AnalyzeTemporalQueries(ctx)
	if err != nil {
		return recommendations, nil // Return what we have so far
	}

	// Add recommendations based on query analysis
	for name, analysis := range analyses {
		if !analysis.UsesIndex && analysis.ScanType == "TABLE" {
			recommendations = append(recommendations,
				fmt.Sprintf("Query '%s' performs full table scan - consider adding appropriate index", name))
		}
	}

	return recommendations, nil
}
