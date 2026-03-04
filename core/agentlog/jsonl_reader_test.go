package agentlog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSONLFile(t *testing.T, path string, entries []JSONLEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadLogs_BasicScan(t *testing.T) {
	dir := t.TempDir()

	now := time.Now().UTC().Truncate(time.Millisecond)
	entries := []JSONLEntry{
		{Timestamp: now.Add(-2 * time.Second), Level: "info", Agent: "test", Event: "A", EventCode: EventActivated},
		{Timestamp: now.Add(-1 * time.Second), Level: "error", Agent: "test", Event: "B", EventCode: EventError, Error: "boom"},
		{Timestamp: now, Level: "info", Agent: "test", Event: "C", EventCode: EventReady},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	got, err := ReadLogs(context.Background(), LogQuery{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	// Newest first (reverse order).
	if got[0].Event != "C" || got[2].Event != "A" {
		t.Errorf("expected newest first: got[0]=%s got[2]=%s", got[0].Event, got[2].Event)
	}
}

func TestReadLogs_LevelFilter(t *testing.T) {
	dir := t.TempDir()

	entries := []JSONLEntry{
		{Timestamp: time.Now(), Level: "info", Event: "one"},
		{Timestamp: time.Now(), Level: "error", Event: "two", Error: "fail"},
		{Timestamp: time.Now(), Level: "warn", Event: "three"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	got, err := ReadLogs(context.Background(), LogQuery{
		Dir:    dir,
		Levels: []string{"error"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event != "two" {
		t.Fatalf("expected 1 error entry, got %d", len(got))
	}
}

func TestReadLogs_HasError(t *testing.T) {
	dir := t.TempDir()

	entries := []JSONLEntry{
		{Timestamp: time.Now(), Level: "info", Event: "ok"},
		{Timestamp: time.Now(), Level: "error", Event: "fail", Error: "something broke"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	got, err := ReadLogs(context.Background(), LogQuery{Dir: dir, HasError: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Error != "something broke" {
		t.Fatalf("expected 1 error entry, got %d", len(got))
	}
}

func TestReadLogs_SinceFilter(t *testing.T) {
	dir := t.TempDir()

	now := time.Now().UTC().Truncate(time.Millisecond)
	entries := []JSONLEntry{
		{Timestamp: now.Add(-10 * time.Minute), Level: "info", Event: "old"},
		{Timestamp: now.Add(-1 * time.Minute), Level: "info", Event: "recent"},
		{Timestamp: now, Level: "info", Event: "now"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	got, err := ReadLogs(context.Background(), LogQuery{
		Dir:   dir,
		Since: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries since 2min ago, got %d", len(got))
	}
}

func TestReadLogs_MaxEntries(t *testing.T) {
	dir := t.TempDir()

	var entries []JSONLEntry
	for i := range 100 {
		entries = append(entries, JSONLEntry{
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Level:     "info",
			Event:     "e",
		})
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	got, err := ReadLogs(context.Background(), LogQuery{Dir: dir, MaxEntries: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("expected 10 capped entries, got %d", len(got))
	}
}

func TestReadLogs_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	old := []JSONLEntry{
		{Timestamp: time.Now().Add(-2 * time.Hour), Level: "info", Event: "old_file"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity-20260303-12.jsonl"), old)

	current := []JSONLEntry{
		{Timestamp: time.Now(), Level: "info", Event: "current_file"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), current)

	got, err := ReadLogs(context.Background(), LogQuery{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries from 2 files, got %d", len(got))
	}
	// Current file scanned first — newest entry from current file is first.
	if got[0].Event != "current_file" {
		t.Errorf("expected current file entry first, got %s", got[0].Event)
	}
}

func TestReadLogs_CorrIDFilter(t *testing.T) {
	dir := t.TempDir()

	entries := []JSONLEntry{
		{Timestamp: time.Now(), Level: "info", Event: "a", CorrID: "abc"},
		{Timestamp: time.Now(), Level: "info", Event: "b", CorrID: "def"},
		{Timestamp: time.Now(), Level: "info", Event: "c", CorrID: "abc"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	got, err := ReadLogs(context.Background(), LogQuery{Dir: dir, CorrID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries with corr_id=abc, got %d", len(got))
	}
}

func TestReadLogs_ContextCancelled(t *testing.T) {
	dir := t.TempDir()

	entries := []JSONLEntry{
		{Timestamp: time.Now(), Level: "info", Event: "a"},
	}
	writeJSONLFile(t, filepath.Join(dir, "activity.jsonl"), entries)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := ReadLogs(ctx, LogQuery{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	// Context cancelled before scan — returns empty.
	if len(got) != 0 {
		t.Fatalf("expected 0 entries on cancelled ctx, got %d", len(got))
	}
}

func TestReadLogs_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	got, err := ReadLogs(context.Background(), LogQuery{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}

func TestSummarize(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	entries := []JSONLEntry{
		{Timestamp: now.Add(-2 * time.Second), Level: "info", EventCode: EventActivated},
		{Timestamp: now.Add(-1 * time.Second), Level: "error", EventCode: EventError, Error: "boom"},
		{Timestamp: now, Level: "info", EventCode: EventReady},
	}

	s := Summarize(entries)

	if s.TotalScanned != 3 || s.TotalMatched != 3 {
		t.Fatalf("expected 3/3, got %d/%d", s.TotalScanned, s.TotalMatched)
	}
	if s.LevelCounts["info"] != 2 || s.LevelCounts["error"] != 1 {
		t.Errorf("level counts: %v", s.LevelCounts)
	}
	if len(s.RecentErrors) != 1 || s.RecentErrors[0].Error != "boom" {
		t.Errorf("recent errors: %v", s.RecentErrors)
	}
	if len(s.RecentEntries) != 3 {
		t.Errorf("recent entries: expected 3, got %d", len(s.RecentEntries))
	}
	if !s.TimeRange[0].Equal(now.Add(-2 * time.Second)) {
		t.Errorf("time range start: %v", s.TimeRange[0])
	}
	if !s.TimeRange[1].Equal(now) {
		t.Errorf("time range end: %v", s.TimeRange[1])
	}
}

func TestListLogFiles_Order(t *testing.T) {
	dir := t.TempDir()

	// Create files in arbitrary order.
	for _, name := range []string{
		"activity-20260303-10.jsonl",
		"activity.jsonl",
		"activity-20260303-14.jsonl",
		"activity-20260303-12.jsonl",
		"unrelated.txt",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0644)
	}

	files, err := listLogFiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 4 {
		t.Fatalf("expected 4 files, got %d", len(files))
	}

	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(f)
	}

	expected := []string{
		"activity.jsonl",          // current first
		"activity-20260303-14.jsonl",
		"activity-20260303-12.jsonl",
		"activity-20260303-10.jsonl",
	}
	for i := range expected {
		if names[i] != expected[i] {
			t.Errorf("index %d: expected %s, got %s", i, expected[i], names[i])
		}
	}
}

func TestReverseScanFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	var content []byte
	for i := range 5 {
		line, _ := json.Marshal(map[string]int{"i": i})
		content = append(content, line...)
		content = append(content, '\n')
	}
	os.WriteFile(path, content, 0644)

	lines, err := reverseScanFile(path, 4<<20)
	if err != nil {
		t.Fatal(err)
	}

	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}

	// First line in result should be the last line in the file (i=4).
	var first map[string]int
	json.Unmarshal(lines[0], &first)
	if first["i"] != 4 {
		t.Errorf("expected first result to be i=4, got %d", first["i"])
	}
}
