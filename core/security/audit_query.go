package security

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

type QueryFilter struct {
	StartTime  time.Time
	EndTime    time.Time
	Categories []AuditCategory
	Severities []AuditSeverity
	SessionID  string
	AgentID    string
	WorkflowID string
	Action     string
	Outcome    string
	Target     *regexp.Regexp
	Limit      int
	Offset     int
}

type OutputFormat string

const (
	OutputFormatTable OutputFormat = "table"
	OutputFormatJSON  OutputFormat = "json"
	OutputFormatCSV   OutputFormat = "csv"
)

type AuditQuerier struct {
	logDir  string
	logPath string
}

func NewAuditQuerier(logPath string) *AuditQuerier {
	return &AuditQuerier{
		logPath: logPath,
		logDir:  filepath.Dir(logPath),
	}
}

func (q *AuditQuerier) Query(filter QueryFilter) ([]AuditEntry, error) {
	files, err := q.getLogFiles()
	if err != nil {
		return nil, err
	}

	var entries []AuditEntry
	for _, f := range files {
		fileEntries, err := q.queryFile(f, filter)
		if err != nil {
			continue
		}
		entries = append(entries, fileEntries...)
	}

	return q.applyPagination(entries, filter), nil
}

func (q *AuditQuerier) getLogFiles() ([]string, error) {
	if q.logPath == "" {
		return nil, nil
	}

	baseName := filepath.Base(q.logPath)
	var files []string

	entries, err := os.ReadDir(q.logDir)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), baseName) {
			files = append(files, filepath.Join(q.logDir, e.Name()))
		}
	}

	sort.Strings(files)
	return files, nil
}

func (q *AuditQuerier) queryFile(path string, filter QueryFilter) ([]AuditEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return q.scanEntries(f, filter)
}

func (q *AuditQuerier) scanEntries(r io.Reader, filter QueryFilter) ([]AuditEntry, error) {
	var entries []AuditEntry
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "SIG:") {
			continue
		}

		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if q.matchesFilter(entry, filter) {
			entries = append(entries, entry)
		}
	}

	return entries, scanner.Err()
}

func (q *AuditQuerier) matchesFilter(entry AuditEntry, filter QueryFilter) bool {
	if !q.matchesTimeRange(entry, filter) {
		return false
	}
	if !q.matchesCategory(entry, filter) {
		return false
	}
	if !q.matchesSeverity(entry, filter) {
		return false
	}
	return q.matchesIdentifiers(entry, filter)
}

func (q *AuditQuerier) matchesTimeRange(entry AuditEntry, filter QueryFilter) bool {
	if !filter.StartTime.IsZero() && entry.Timestamp.Before(filter.StartTime) {
		return false
	}
	if !filter.EndTime.IsZero() && entry.Timestamp.After(filter.EndTime) {
		return false
	}
	return true
}

func (q *AuditQuerier) matchesCategory(entry AuditEntry, filter QueryFilter) bool {
	if len(filter.Categories) == 0 {
		return true
	}
	return slices.Contains(filter.Categories, entry.Category)
}

func (q *AuditQuerier) matchesSeverity(entry AuditEntry, filter QueryFilter) bool {
	if len(filter.Severities) == 0 {
		return true
	}
	return slices.Contains(filter.Severities, entry.Severity)
}

func (q *AuditQuerier) matchesIdentifiers(entry AuditEntry, filter QueryFilter) bool {
	if filter.SessionID != "" && entry.SessionID != filter.SessionID {
		return false
	}
	if filter.AgentID != "" && entry.AgentID != filter.AgentID {
		return false
	}
	if filter.WorkflowID != "" && entry.WorkflowID != filter.WorkflowID {
		return false
	}
	return q.matchesActionAndTarget(entry, filter)
}

func (q *AuditQuerier) matchesActionAndTarget(entry AuditEntry, filter QueryFilter) bool {
	if filter.Action != "" && entry.Action != filter.Action {
		return false
	}
	if filter.Outcome != "" && entry.Outcome != filter.Outcome {
		return false
	}
	if filter.Target != nil && !filter.Target.MatchString(entry.Target) {
		return false
	}
	return true
}

func (q *AuditQuerier) applyPagination(entries []AuditEntry, filter QueryFilter) []AuditEntry {
	if filter.Offset >= len(entries) {
		return nil
	}
	entries = entries[filter.Offset:]

	if filter.Limit > 0 && filter.Limit < len(entries) {
		entries = entries[:filter.Limit]
	}
	return entries
}

func FormatEntries(entries []AuditEntry, format OutputFormat, w io.Writer) error {
	switch format {
	case OutputFormatJSON:
		return formatJSON(entries, w)
	case OutputFormatCSV:
		return formatCSV(entries, w)
	default:
		return formatTable(entries, w)
	}
}

func formatJSON(entries []AuditEntry, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

func formatCSV(entries []AuditEntry, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{"id", "timestamp", "sequence", "category", "severity", "action", "target", "outcome", "session_id", "agent_id"}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, e := range entries {
		row := []string{
			e.ID,
			e.Timestamp.Format(time.RFC3339),
			fmt.Sprintf("%d", e.Sequence),
			string(e.Category),
			string(e.Severity),
			e.Action,
			e.Target,
			e.Outcome,
			e.SessionID,
			e.AgentID,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func formatTable(entries []AuditEntry, w io.Writer) error {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No entries found.")
		return nil
	}

	header := "%-36s  %-19s  %-10s  %-8s  %-15s  %-20s  %-10s\n"
	fmt.Fprintf(w, header, "ID", "TIMESTAMP", "CATEGORY", "SEVERITY", "ACTION", "TARGET", "OUTCOME")
	fmt.Fprintln(w, strings.Repeat("-", 130))

	for _, e := range entries {
		target := truncateString(e.Target, 20)
		ts := e.Timestamp.Format("2006-01-02 15:04:05")
		fmt.Fprintf(w, header, e.ID, ts, e.Category, e.Severity, e.Action, target, e.Outcome)
	}
	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

type PurgeOptions struct {
	Before       time.Time
	CreateBackup bool
	BackupPath   string
}

func (q *AuditQuerier) Purge(opts PurgeOptions, logger *AuditLogger) error {
	if opts.CreateBackup {
		if err := q.createBackup(opts.BackupPath); err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}
	}

	return q.executePurge(opts, logger)
}

func (q *AuditQuerier) createBackup(backupPath string) error {
	if backupPath == "" {
		backupPath = q.logPath + ".backup." + time.Now().Format("20060102150405")
	}

	src, err := os.Open(q.logPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func (q *AuditQuerier) executePurge(opts PurgeOptions, logger *AuditLogger) error {
	filter := QueryFilter{StartTime: opts.Before}
	entries, err := q.Query(filter)
	if err != nil {
		return err
	}

	tempPath := q.logPath + ".purging"
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return err
	}

	for _, e := range entries {
		data, _ := json.Marshal(e)
		tempFile.Write(append(data, '\n'))
	}
	tempFile.Close()

	q.logPurgeOperation(logger, opts.Before, len(entries))

	return os.Rename(tempPath, q.logPath)
}

func (q *AuditQuerier) logPurgeOperation(logger *AuditLogger, before time.Time, retained int) {
	if logger == nil {
		return
	}
	entry := NewAuditEntry(AuditCategoryConfig, "audit_purge", "purge")
	entry.Details = map[string]any{
		"purged_before":    before.Format(time.RFC3339),
		"entries_retained": retained,
	}
	_ = logger.Log(entry)
}

func (q *AuditQuerier) Export(filter QueryFilter, format OutputFormat, w io.Writer) error {
	entries, err := q.Query(filter)
	if err != nil {
		return err
	}
	return FormatEntries(entries, format, w)
}
