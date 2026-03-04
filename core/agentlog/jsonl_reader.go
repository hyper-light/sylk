package agentlog

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	defaultMaxEntries = 500
	defaultMaxBytes   = 4 << 20 // 4 MB
	maxRecentErrors   = 10
	maxRecentEntries  = 20
)

// LogQuery describes a bounded, filtered scan of JSONL activity log files.
type LogQuery struct {
	Dir        string        // directory containing activity*.jsonl files
	MaxEntries int           // cap on returned entries (default 500)
	MaxBytes   int64         // cap on bytes scanned per file (default 4 MB)
	Since      time.Time     // include entries at or after this time
	Until      time.Time     // include entries at or before this time
	Levels     []string      // filter by log level ("info","warn","error")
	EventCodes []EventType   // filter by event code
	CorrID     string        // filter by correlation ID
	HasError   bool          // only entries with non-empty Error field
}

// LogSummary aggregates statistics from a set of log entries.
type LogSummary struct {
	TotalScanned  int                `json:"total_scanned"`
	TotalMatched  int                `json:"total_matched"`
	TimeRange     [2]time.Time       `json:"time_range"`
	ErrorCounts   map[string]int     `json:"error_counts"`
	EventCounts   map[EventType]int  `json:"event_counts"`
	LevelCounts   map[string]int     `json:"level_counts"`
	RecentErrors  []JSONLEntry       `json:"recent_errors"`
	RecentEntries []JSONLEntry       `json:"recent_entries"`
}

// ReadLogs scans JSONL activity files in the query directory newest-first.
// It reads from the tail of each file (reverse chronological) and stops at
// MaxEntries or context cancellation. Returns entries in newest-first order.
func ReadLogs(ctx context.Context, q LogQuery) ([]JSONLEntry, error) {
	if q.MaxEntries <= 0 {
		q.MaxEntries = defaultMaxEntries
	}
	if q.MaxBytes <= 0 {
		q.MaxBytes = defaultMaxBytes
	}

	files, err := listLogFiles(q.Dir)
	if err != nil {
		return nil, err
	}

	result := make([]JSONLEntry, 0, min(q.MaxEntries, 64))
	for _, path := range files {
		if ctx.Err() != nil {
			break
		}
		if len(result) >= q.MaxEntries {
			break
		}

		lines, scanErr := reverseScanFile(path, q.MaxBytes)
		if scanErr != nil {
			continue // skip unreadable files
		}

		for _, line := range lines {
			if ctx.Err() != nil {
				break
			}
			if len(result) >= q.MaxEntries {
				break
			}

			var entry JSONLEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}
			if matchEntry(entry, q) {
				result = append(result, entry)
			}
		}
	}

	return result, nil
}

// Summarize produces a single-pass aggregation over the provided entries.
func Summarize(entries []JSONLEntry) LogSummary {
	s := LogSummary{
		ErrorCounts: make(map[string]int),
		EventCounts: make(map[EventType]int),
		LevelCounts: make(map[string]int),
	}

	for i, e := range entries {
		s.TotalScanned++
		s.TotalMatched++
		s.EventCounts[e.EventCode]++
		s.LevelCounts[e.Level]++

		if i == 0 {
			s.TimeRange = [2]time.Time{e.Timestamp, e.Timestamp}
		} else {
			if e.Timestamp.Before(s.TimeRange[0]) {
				s.TimeRange[0] = e.Timestamp
			}
			if e.Timestamp.After(s.TimeRange[1]) {
				s.TimeRange[1] = e.Timestamp
			}
		}

		if e.Error != "" {
			s.ErrorCounts[e.Error]++
			if len(s.RecentErrors) < maxRecentErrors {
				s.RecentErrors = append(s.RecentErrors, e)
			}
		}
		if len(s.RecentEntries) < maxRecentEntries {
			s.RecentEntries = append(s.RecentEntries, e)
		}
	}

	return s
}

// listLogFiles returns JSONL activity files sorted newest-first by name.
// Files follow the naming convention: activity.jsonl (current) and
// activity-YYYYMMDD-HH.jsonl (rotated).
func listLogFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "activity") && strings.HasSuffix(name, ".jsonl") {
			files = append(files, filepath.Join(dir, name))
		}
	}

	// Sort by name descending — rotated files (activity-YYYYMMDD-HH.jsonl)
	// sort lexicographically later than the current file (activity.jsonl),
	// so reverse gives newest rotated files first, current file last.
	// We swap: put "activity.jsonl" (current, newest writes) first,
	// then rotated files newest-first.
	sort.Slice(files, func(i, j int) bool {
		ni, nj := filepath.Base(files[i]), filepath.Base(files[j])
		iCurrent := ni == "activity.jsonl"
		jCurrent := nj == "activity.jsonl"
		if iCurrent != jCurrent {
			return iCurrent // current file sorts first
		}
		return ni > nj // rotated files: descending (newest first)
	})

	return files, nil
}

// reverseScanFile reads up to maxBytes from the tail of a file and returns
// lines in reverse order (newest first). Each line is raw JSON bytes.
func reverseScanFile(path string, maxBytes int64) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	readStart := max(int64(0), size-maxBytes)
	readSize := size - readStart

	if _, err := f.Seek(readStart, io.SeekStart); err != nil {
		return nil, err
	}

	reader := bufio.NewReaderSize(f, min(int(readSize), 64*1024))

	// If we seeked past the beginning, discard the partial first line.
	if readStart > 0 {
		if _, _, err := reader.ReadLine(); err != nil {
			return nil, err
		}
	}

	var lines [][]byte
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
	}

	// Reverse in-place: newest lines (end of file) first.
	slices.Reverse(lines)
	return lines, scanner.Err()
}

// matchEntry tests whether a log entry satisfies all query filters.
func matchEntry(e JSONLEntry, q LogQuery) bool {
	if !q.Since.IsZero() && e.Timestamp.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && e.Timestamp.After(q.Until) {
		return false
	}
	if len(q.Levels) > 0 && !slices.Contains(q.Levels, e.Level) {
		return false
	}
	if len(q.EventCodes) > 0 && !slices.Contains(q.EventCodes, e.EventCode) {
		return false
	}
	if q.CorrID != "" && e.CorrID != q.CorrID {
		return false
	}
	if q.HasError && e.Error == "" {
		return false
	}
	return true
}
