package agentlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamWriter_DailyDirectory(t *testing.T) {
	baseDir := t.TempDir()
	fixedTime := time.Date(2026, 4, 21, 10, 15, 0, 0, time.UTC)
	w, err := NewStreamWriter(baseDir, "events", StreamWriterConfig{
		Location: time.UTC,
		Now:      func() time.Time { return fixedTime },
	}, nil)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if err := w.Write(map[string]any{"hello": "world"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	expected := filepath.Join(baseDir, "2026-04-21", "events.000.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected file %s: %v", expected, err)
	}
}

func TestStreamWriter_DailyRollover(t *testing.T) {
	baseDir := t.TempDir()
	day1 := time.Date(2026, 4, 21, 23, 59, 0, 0, time.UTC)
	day2 := time.Date(2026, 4, 22, 0, 1, 0, 0, time.UTC)
	current := day1
	w, err := NewStreamWriter(baseDir, "events", StreamWriterConfig{
		Location: time.UTC,
		Now:      func() time.Time { return current },
	}, nil)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if err := w.Write(map[string]any{"day": 1}); err != nil {
		t.Fatalf("day1 write: %v", err)
	}
	current = day2
	if err := w.Write(map[string]any{"day": 2}); err != nil {
		t.Fatalf("day2 write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	entries := mustReadJSONL(t, filepath.Join(baseDir, "2026-04-21", "events.000.jsonl"))
	if len(entries) != 1 {
		t.Fatalf("day1 expected 1 record, got %d", len(entries))
	}
	if int(entries[0]["day"].(float64)) != 1 {
		t.Fatalf("day1 wrong record: %#v", entries[0])
	}
	entries = mustReadJSONL(t, filepath.Join(baseDir, "2026-04-22", "events.000.jsonl"))
	if len(entries) != 1 {
		t.Fatalf("day2 expected 1 record, got %d", len(entries))
	}
}

func TestStreamWriter_SizeRollover(t *testing.T) {
	baseDir := t.TempDir()
	fixedTime := time.Date(2026, 4, 21, 10, 15, 0, 0, time.UTC)
	w, err := NewStreamWriter(baseDir, "events", StreamWriterConfig{
		Location:        time.UTC,
		Now:             func() time.Time { return fixedTime },
		MaxBytesPerFile: 200, // tiny cap to force rollover
	}, nil)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	payload := strings.Repeat("x", 80)
	for i := 0; i < 10; i++ {
		if err := w.Write(map[string]any{"i": i, "pad": payload}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dayDir := filepath.Join(baseDir, "2026-04-21")
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		t.Fatalf("read day dir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected size rollover to produce multiple files, got %d", len(entries))
	}
	// Every file should contain at least one record.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		recs := mustReadJSONL(t, filepath.Join(dayDir, e.Name()))
		if len(recs) == 0 {
			t.Fatalf("file %s empty", e.Name())
		}
	}
}

func TestStreamWriter_ResumesLatestPart(t *testing.T) {
	baseDir := t.TempDir()
	fixedTime := time.Date(2026, 4, 21, 10, 15, 0, 0, time.UTC)
	cfg := StreamWriterConfig{
		Location:        time.UTC,
		Now:             func() time.Time { return fixedTime },
		MaxBytesPerFile: 120,
	}

	// First writer: generate two continuation files, then close.
	w1, err := NewStreamWriter(baseDir, "events", cfg, nil)
	if err != nil {
		t.Fatalf("new w1: %v", err)
	}
	for i := 0; i < 4; i++ {
		_ = w1.Write(map[string]any{"i": i, "pad": strings.Repeat("x", 40)})
	}
	_ = w1.Close()

	// Second writer resumes the same day; it should pick up the
	// highest existing part rather than clobber events.000.jsonl.
	w2, err := NewStreamWriter(baseDir, "events", cfg, nil)
	if err != nil {
		t.Fatalf("new w2: %v", err)
	}
	_ = w2.Write(map[string]any{"resumed": true})
	_ = w2.Close()

	// The resumed record should be in the HIGHEST-numbered file, not
	// overwrite the 000 file.
	dayDir := filepath.Join(baseDir, "2026-04-21")
	entries, _ := os.ReadDir(dayDir)
	if len(entries) < 2 {
		t.Fatalf("expected multiple part files: %v", entries)
	}
	first := mustReadJSONL(t, filepath.Join(dayDir, "events.000.jsonl"))
	for _, rec := range first {
		if rec["resumed"] == true {
			t.Fatalf("resumed record unexpectedly in events.000.jsonl")
		}
	}
}

// mustReadJSONL parses every line of a JSONL file into a map. Fails
// the test on read or parse errors.
func mustReadJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	var out []map[string]any
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("parse %s: %v (line=%q)", path, err, string(line))
		}
		out = append(out, m)
	}
	return out
}
