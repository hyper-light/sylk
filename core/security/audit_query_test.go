package security

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestQueryFilter_TimeRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	logEntriesWithTimestamps(logger)
	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{
		StartTime: time.Now().Add(-2 * time.Hour),
	}
	entries, err := querier.Query(filter)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(entries) == 0 {
		t.Error("expected entries within time range")
	}
}

func TestQueryFilter_Category(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	logMixedCategories(logger)
	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{
		Categories: []AuditCategory{AuditCategoryPermission},
	}
	entries, err := querier.Query(filter)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	for _, e := range entries {
		if e.Category != AuditCategoryPermission {
			t.Errorf("got category %s, want permission", e.Category)
		}
	}
}

func TestQueryFilter_Severity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	logMixedSeverities(logger)
	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{
		Severities: []AuditSeverity{AuditSeveritySecurity},
	}
	entries, err := querier.Query(filter)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	for _, e := range entries {
		if e.Severity != AuditSeveritySecurity {
			t.Errorf("got severity %s, want security", e.Severity)
		}
	}
}

func TestQueryFilter_SessionID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	logMixedSessions(logger)
	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{
		SessionID: "session-1",
	}
	entries, err := querier.Query(filter)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	for _, e := range entries {
		if e.SessionID != "session-1" {
			t.Errorf("got session %s, want session-1", e.SessionID)
		}
	}
}

func TestQueryFilter_TargetRegex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	logMixedTargets(logger)
	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{
		Target: regexp.MustCompile(`\.go$`),
	}
	entries, err := querier.Query(filter)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	for _, e := range entries {
		if !strings.HasSuffix(e.Target, ".go") {
			t.Errorf("target %s should end with .go", e.Target)
		}
	}
}

func TestQueryFilter_Pagination(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	for i := 0; i < 20; i++ {
		entry := NewAuditEntry(AuditCategoryFile, "test", "read")
		logger.Log(entry)
	}
	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{Limit: 5, Offset: 0}
	entries, _ := querier.Query(filter)
	if len(entries) != 5 {
		t.Errorf("got %d entries, want 5", len(entries))
	}

	filter = QueryFilter{Limit: 5, Offset: 5}
	entries, _ = querier.Query(filter)
	if len(entries) != 5 {
		t.Errorf("got %d entries, want 5", len(entries))
	}

	filter = QueryFilter{Limit: 5, Offset: 18}
	entries, _ = querier.Query(filter)
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestFormatEntries_JSON(t *testing.T) {
	t.Parallel()

	entries := []AuditEntry{
		{ID: "1", Category: AuditCategoryFile, Action: "read"},
		{ID: "2", Category: AuditCategoryFile, Action: "write"},
	}

	var buf bytes.Buffer
	err := FormatEntries(entries, OutputFormatJSON, &buf)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	var result []AuditEntry
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("got %d entries, want 2", len(result))
	}
}

func TestFormatEntries_CSV(t *testing.T) {
	t.Parallel()

	entries := []AuditEntry{
		{ID: "1", Category: AuditCategoryFile, Action: "read"},
	}

	var buf bytes.Buffer
	err := FormatEntries(entries, OutputFormatCSV, &buf)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "id,timestamp") {
		t.Error("CSV should have header")
	}
	if !strings.Contains(output, "read") {
		t.Error("CSV should contain entry data")
	}
}

func TestFormatEntries_Table(t *testing.T) {
	t.Parallel()

	entries := []AuditEntry{
		{ID: "test-id", Category: AuditCategoryFile, Action: "read"},
	}

	var buf bytes.Buffer
	err := FormatEntries(entries, OutputFormatTable, &buf)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ID") {
		t.Error("table should have ID header")
	}
	if !strings.Contains(output, "test-id") {
		t.Error("table should contain entry ID")
	}
}

func TestFormatEntries_EmptyTable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := FormatEntries([]AuditEntry{}, OutputFormatTable, &buf)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	if !strings.Contains(buf.String(), "No entries found") {
		t.Error("empty table should show 'No entries found'")
	}
}

func TestPurge_WithBackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	for i := 0; i < 10; i++ {
		entry := NewAuditEntry(AuditCategoryFile, "test", "read")
		logger.Log(entry)
	}
	logger.Close()

	querier := NewAuditQuerier(logPath)
	backupPath := filepath.Join(dir, "audit.backup")

	opts := PurgeOptions{
		Before:       time.Now().Add(-24 * time.Hour),
		CreateBackup: true,
		BackupPath:   backupPath,
	}

	err := querier.Purge(opts, nil)
	if err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("backup file should exist")
	}
}

func TestQuery_CombinedFilters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	entry1 := NewAuditEntry(AuditCategoryPermission, "grant", "allow")
	entry1.SessionID = "session-1"
	entry1.Severity = AuditSeveritySecurity
	logger.Log(entry1)

	entry2 := NewAuditEntry(AuditCategoryFile, "read", "read")
	entry2.SessionID = "session-1"
	logger.Log(entry2)

	entry3 := NewAuditEntry(AuditCategoryPermission, "deny", "deny")
	entry3.SessionID = "session-2"
	entry3.Severity = AuditSeveritySecurity
	logger.Log(entry3)

	logger.Close()

	querier := NewAuditQuerier(logPath)

	filter := QueryFilter{
		Categories: []AuditCategory{AuditCategoryPermission},
		Severities: []AuditSeverity{AuditSeveritySecurity},
		SessionID:  "session-1",
	}
	entries, err := querier.Query(filter)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func TestQuery_EmptyLogPath(t *testing.T) {
	t.Parallel()

	querier := NewAuditQuerier("")
	entries, err := querier.Query(QueryFilter{})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if entries != nil {
		t.Error("expected nil entries for empty log path")
	}
}

func TestExport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	logger := createTestLogger(t, logPath)

	entry := NewAuditEntry(AuditCategoryFile, "test", "read")
	logger.Log(entry)
	logger.Close()

	querier := NewAuditQuerier(logPath)

	var buf bytes.Buffer
	err := querier.Export(QueryFilter{}, OutputFormatJSON, &buf)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	var entries []AuditEntry
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("invalid export: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func createTestLogger(t *testing.T, logPath string) *AuditLogger {
	t.Helper()
	cfg := AuditLogConfig{LogPath: logPath, SignatureInterval: 100}
	logger, err := NewAuditLogger(cfg)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return logger
}

func logEntriesWithTimestamps(logger *AuditLogger) {
	for i := 0; i < 5; i++ {
		entry := NewAuditEntry(AuditCategoryFile, "test", "read")
		logger.Log(entry)
	}
}

func logMixedCategories(logger *AuditLogger) {
	logger.Log(NewAuditEntry(AuditCategoryPermission, "grant", "allow"))
	logger.Log(NewAuditEntry(AuditCategoryFile, "read", "read"))
	logger.Log(NewAuditEntry(AuditCategoryPermission, "deny", "deny"))
	logger.Log(NewAuditEntry(AuditCategoryNetwork, "connect", "allow"))
}

func logMixedSeverities(logger *AuditLogger) {
	e1 := NewAuditEntry(AuditCategoryFile, "read", "read")
	e1.Severity = AuditSeverityInfo
	logger.Log(e1)

	e2 := NewAuditEntry(AuditCategoryPermission, "deny", "deny")
	e2.Severity = AuditSeveritySecurity
	logger.Log(e2)

	e3 := NewAuditEntry(AuditCategoryFile, "write", "write")
	e3.Severity = AuditSeverityWarning
	logger.Log(e3)
}

func logMixedSessions(logger *AuditLogger) {
	e1 := NewAuditEntry(AuditCategoryFile, "read", "read")
	e1.SessionID = "session-1"
	logger.Log(e1)

	e2 := NewAuditEntry(AuditCategoryFile, "write", "write")
	e2.SessionID = "session-2"
	logger.Log(e2)

	e3 := NewAuditEntry(AuditCategoryFile, "delete", "delete")
	e3.SessionID = "session-1"
	logger.Log(e3)
}

func logMixedTargets(logger *AuditLogger) {
	e1 := NewAuditEntry(AuditCategoryFile, "read", "read")
	e1.Target = "main.go"
	logger.Log(e1)

	e2 := NewAuditEntry(AuditCategoryFile, "read", "read")
	e2.Target = "config.yaml"
	logger.Log(e2)

	e3 := NewAuditEntry(AuditCategoryFile, "read", "read")
	e3.Target = "utils.go"
	logger.Log(e3)
}
