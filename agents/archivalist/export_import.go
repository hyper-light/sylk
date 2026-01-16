package archivalist

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type ExportFormat string

const (
	ExportFormatJSON    ExportFormat = "json"
	ExportFormatJSONL   ExportFormat = "jsonl"
	ExportFormatCompact ExportFormat = "compact"
)

type Exporter struct {
	mu sync.Mutex

	config     ExportConfig
	compressor *Compressor
	stats      exporterStatsInternal
}

type ExportConfig struct {
	Format            ExportFormat    `json:"format"`
	IncludeArchived   bool            `json:"include_archived"`
	IncludeEmbeddings bool            `json:"include_embeddings"`
	IncludeFacts      bool            `json:"include_facts"`
	IncludeSummaries  bool            `json:"include_summaries"`
	BatchSize         int             `json:"batch_size"`
	Compression       CompressionType `json:"compression"`
}

func DefaultExportConfig() ExportConfig {
	return ExportConfig{
		Format:            ExportFormatJSONL,
		IncludeArchived:   true,
		IncludeEmbeddings: false,
		IncludeFacts:      true,
		IncludeSummaries:  true,
		BatchSize:         1000,
		Compression:       CompressionGzip,
	}
}

type exporterStatsInternal struct {
	totalExports    int64
	totalImports    int64
	entriesExported int64
	entriesImported int64
	bytesExported   int64
	bytesImported   int64
	exportErrors    int64
	importErrors    int64
}

type ExporterStats struct {
	TotalExports    int64 `json:"total_exports"`
	TotalImports    int64 `json:"total_imports"`
	EntriesExported int64 `json:"entries_exported"`
	EntriesImported int64 `json:"entries_imported"`
	BytesExported   int64 `json:"bytes_exported"`
	BytesImported   int64 `json:"bytes_imported"`
	ExportErrors    int64 `json:"export_errors"`
	ImportErrors    int64 `json:"import_errors"`
}

func NewExporter(config ExportConfig) *Exporter {
	if config.BatchSize == 0 {
		config = DefaultExportConfig()
	}

	return &Exporter{
		config: config,
		compressor: NewCompressor(CompressorConfig{
			Type:              config.Compression,
			Level:             CompressionDefault,
			MinSizeToCompress: 256,
		}),
	}
}

type ExportData struct {
	Version    string    `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	Format     string    `json:"format"`
	TotalItems int       `json:"total_items"`

	Sessions []*Session `json:"sessions,omitempty"`
	Entries  []*Entry   `json:"entries,omitempty"`

	Decisions   []*FactDecision   `json:"decisions,omitempty"`
	Patterns    []*FactPattern    `json:"patterns,omitempty"`
	Failures    []*FactFailure    `json:"failures,omitempty"`
	FileChanges []*FactFileChange `json:"file_changes,omitempty"`

	Summaries []*CompactedSummary `json:"summaries,omitempty"`
}

type ExportItem struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (e *Exporter) ExportToFile(path string, store *Store, archive *Archive) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	atomic.AddInt64(&e.stats.totalExports, 1)

	file, err := os.Create(path)
	if err != nil {
		atomic.AddInt64(&e.stats.exportErrors, 1)
		return fmt.Errorf("failed to create export file: %w", err)
	}
	defer file.Close()

	switch e.config.Format {
	case ExportFormatJSON:
		return e.exportJSON(file, store, archive)
	case ExportFormatJSONL:
		return e.exportJSONL(file, store, archive)
	case ExportFormatCompact:
		return e.exportCompact(file, store, archive)
	default:
		return fmt.Errorf("unsupported export format: %s", e.config.Format)
	}
}

func (e *Exporter) ExportToWriter(w io.Writer, store *Store, archive *Archive) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	atomic.AddInt64(&e.stats.totalExports, 1)

	switch e.config.Format {
	case ExportFormatJSON:
		return e.exportJSON(w, store, archive)
	case ExportFormatJSONL:
		return e.exportJSONL(w, store, archive)
	default:
		return fmt.Errorf("unsupported export format: %s", e.config.Format)
	}
}

func (e *Exporter) exportJSON(w io.Writer, store *Store, archive *Archive) error {
	data := e.buildExportData(store, archive, "json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		atomic.AddInt64(&e.stats.exportErrors, 1)
		return fmt.Errorf("failed to encode export data: %w", err)
	}

	return nil
}

func (e *Exporter) exportJSONL(w io.Writer, store *Store, archive *Archive) error {
	encoder := json.NewEncoder(w)

	if err := encoder.Encode(ExportItem{Type: "header", Data: map[string]any{
		"version":     "1.0",
		"exported_at": time.Now(),
		"format":      "jsonl",
	}}); err != nil {
		return err
	}

	if archive != nil {
		sessions, _ := archive.GetRecentSessions(0)
		for _, session := range sessions {
			item := ExportItem{Type: "session", Data: session}
			if err := encoder.Encode(item); err != nil {
				atomic.AddInt64(&e.stats.exportErrors, 1)
				continue
			}
		}
	}

	if store != nil {
		entries, _ := store.Query(ArchiveQuery{IncludeArchived: e.config.IncludeArchived})
		for _, entry := range entries {
			item := ExportItem{Type: "entry", Data: entry}
			if err := encoder.Encode(item); err != nil {
				atomic.AddInt64(&e.stats.exportErrors, 1)
				continue
			}
			atomic.AddInt64(&e.stats.entriesExported, 1)
		}
	}

	if e.config.IncludeFacts && archive != nil {
		decisions, _ := archive.QueryFactDecisions("", 0)
		for _, d := range decisions {
			encoder.Encode(ExportItem{Type: "decision", Data: d})
		}

		patterns, _ := archive.QueryFactPatterns("", 0)
		for _, p := range patterns {
			encoder.Encode(ExportItem{Type: "pattern", Data: p})
		}

		failures, _ := archive.QueryFactFailures("", 0)
		for _, f := range failures {
			encoder.Encode(ExportItem{Type: "failure", Data: f})
		}

		fileChanges, _ := archive.QueryFactFileChanges("", 0)
		for _, fc := range fileChanges {
			encoder.Encode(ExportItem{Type: "file_change", Data: fc})
		}
	}

	if e.config.IncludeSummaries && archive != nil {
		summaries, _ := archive.QuerySummaries(SummaryQuery{})
		for _, s := range summaries {
			encoder.Encode(ExportItem{Type: "summary", Data: s})
		}
	}

	return nil
}

func (e *Exporter) buildExportData(store *Store, archive *Archive, format string) *ExportData {
	data := &ExportData{
		Version:    "1.0",
		ExportedAt: time.Now(),
		Format:     format,
	}

	if archive != nil {
		sessions, err := archive.GetRecentSessions(0)
		if err == nil {
			data.Sessions = sessions
		}
	}

	if store != nil {
		entries, err := store.Query(ArchiveQuery{IncludeArchived: e.config.IncludeArchived})
		if err == nil {
			data.Entries = entries
			atomic.AddInt64(&e.stats.entriesExported, int64(len(entries)))
		}
	}

	if e.config.IncludeFacts && archive != nil {
		decisions, _ := archive.QueryFactDecisions("", 0)
		patterns, _ := archive.QueryFactPatterns("", 0)
		failures, _ := archive.QueryFactFailures("", 0)
		fileChanges, _ := archive.QueryFactFileChanges("", 0)

		data.Decisions = decisions
		data.Patterns = patterns
		data.Failures = failures
		data.FileChanges = fileChanges
	}

	if e.config.IncludeSummaries && archive != nil {
		summaries, _ := archive.QuerySummaries(SummaryQuery{})
		data.Summaries = summaries
	}

	data.TotalItems = len(data.Entries) + len(data.Sessions) +
		len(data.Decisions) + len(data.Patterns) +
		len(data.Failures) + len(data.FileChanges) + len(data.Summaries)

	return data
}

func (e *Exporter) exportCompact(w io.Writer, store *Store, archive *Archive) error {
	var buf bufio.Writer
	buf.Reset(w)

	data := e.buildExportData(store, archive, "compact")

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to serialize export data: %w", err)
	}

	compressed, err := e.compressor.Compress(jsonData)
	if err != nil {
		return fmt.Errorf("failed to compress export data: %w", err)
	}

	atomic.AddInt64(&e.stats.bytesExported, int64(compressed.CompressedSize))

	wrapper := struct {
		Type       string          `json:"type"`
		Compressed *CompressedData `json:"compressed"`
	}{
		Type:       "compact_export",
		Compressed: compressed,
	}

	return json.NewEncoder(w).Encode(wrapper)
}

type Importer struct {
	mu sync.Mutex

	config     ImportConfig
	compressor *Compressor
	stats      importerStatsInternal
}

type ImportConfig struct {
	MergeStrategy  MergeStrategy `json:"merge_strategy"`
	BatchSize      int           `json:"batch_size"`
	SkipValidation bool          `json:"skip_validation"`
	DryRun         bool          `json:"dry_run"`
}

type MergeStrategy string

const (
	MergeStrategySkip      MergeStrategy = "skip"
	MergeStrategyOverwrite MergeStrategy = "overwrite"
	MergeStrategyNewest    MergeStrategy = "newest"
)

func DefaultImportConfig() ImportConfig {
	return ImportConfig{
		MergeStrategy:  MergeStrategySkip,
		BatchSize:      1000,
		SkipValidation: false,
		DryRun:         false,
	}
}

type importerStatsInternal struct {
	totalImports    int64
	entriesImported int64
	entriesSkipped  int64
	importErrors    int64
}

type ImporterStats struct {
	TotalImports    int64 `json:"total_imports"`
	EntriesImported int64 `json:"entries_imported"`
	EntriesSkipped  int64 `json:"entries_skipped"`
	ImportErrors    int64 `json:"import_errors"`
}

func NewImporter(config ImportConfig) *Importer {
	if config.BatchSize == 0 {
		config = DefaultImportConfig()
	}

	return &Importer{
		config: config,
		compressor: NewCompressor(CompressorConfig{
			Type:              CompressionGzip,
			Level:             CompressionDefault,
			MinSizeToCompress: 256,
		}),
	}
}

func (i *Importer) ImportFromFile(path string, store *Store, archive *Archive) (*ImportResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	atomic.AddInt64(&i.stats.totalImports, 1)

	file, err := os.Open(path)
	if err != nil {
		atomic.AddInt64(&i.stats.importErrors, 1)
		return nil, fmt.Errorf("failed to open import file: %w", err)
	}
	defer file.Close()

	return i.importFromReader(file, store, archive)
}

func (i *Importer) ImportFromReader(r io.Reader, store *Store, archive *Archive) (*ImportResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	atomic.AddInt64(&i.stats.totalImports, 1)

	return i.importFromReader(r, store, archive)
}

func (i *Importer) importFromReader(r io.Reader, store *Store, archive *Archive) (*ImportResult, error) {
	result := &ImportResult{
		StartedAt: time.Now(),
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read import data: %w", err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return result, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return nil, fmt.Errorf("failed to parse import data: %w", err)
	}
	var second json.RawMessage
	secondErr := decoder.Decode(&second)
	if secondErr == nil {
		if err := i.importJSONL(bytes.NewReader(trimmed), store, archive, result); err != nil {
			return nil, err
		}
	} else if secondErr != io.EOF {
		return nil, fmt.Errorf("failed to parse import data: %w", secondErr)
	} else {
		if len(first) == 0 {
			return result, nil
		}
		if first[0] == '[' {
			return nil, fmt.Errorf("JSON array format not supported")
		}
		if err := i.importJSON(bytes.NewReader(first), store, archive, result); err != nil {
			return nil, err
		}
	}

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(result.StartedAt)

	return result, nil
}

func (i *Importer) importJSON(r io.Reader, store *Store, archive *Archive, result *ImportResult) error {
	var data ExportData
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return fmt.Errorf("failed to parse JSON import: %w", err)
	}

	for _, session := range data.Sessions {
		if archive == nil {
			continue
		}
		if session != nil {
			if err := archive.SaveSession(session); err != nil {
				result.Errors = append(result.Errors, err.Error())
				atomic.AddInt64(&i.stats.importErrors, 1)
				continue
			}
			result.SessionsImported++
		}
	}

	for _, entry := range data.Entries {
		if store == nil {
			continue
		}
		if entry != nil {
			item := ExportItem{Type: "entry", Data: entry}
			if err := i.importItem(&item, store, archive, result); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
	}

	for _, decision := range data.Decisions {
		if archive == nil {
			continue
		}
		if decision != nil {
			item := ExportItem{Type: "decision", Data: decision}
			if err := i.importItem(&item, store, archive, result); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
	}

	for _, pattern := range data.Patterns {
		if archive == nil {
			continue
		}
		if pattern != nil {
			item := ExportItem{Type: "pattern", Data: pattern}
			if err := i.importItem(&item, store, archive, result); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
	}

	for _, failure := range data.Failures {
		if archive == nil {
			continue
		}
		if failure != nil {
			item := ExportItem{Type: "failure", Data: failure}
			if err := i.importItem(&item, store, archive, result); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
	}

	for _, fileChange := range data.FileChanges {
		if archive == nil {
			continue
		}
		if fileChange != nil {
			item := ExportItem{Type: "file_change", Data: fileChange}
			if err := i.importItem(&item, store, archive, result); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
	}

	for _, summary := range data.Summaries {
		if archive == nil {
			continue
		}
		if summary != nil {
			item := ExportItem{Type: "summary", Data: summary}
			if err := i.importItem(&item, store, archive, result); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
	}

	return nil
}

func (i *Importer) importJSONL(r io.Reader, store *Store, archive *Archive, result *ImportResult) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var item ExportItem
		if err := json.Unmarshal(line, &item); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to parse line: %v", err))
			atomic.AddInt64(&i.stats.importErrors, 1)
			continue
		}

		if err := i.importItem(&item, store, archive, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			continue
		}
	}

	return scanner.Err()
}

func (i *Importer) importItem(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
	if i.config.DryRun {
		result.DryRunCount++
		return nil
	}

	switch item.Type {
	case "header":
		return nil

	case "session":
		if archive == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			return fmt.Errorf("failed to parse session: %w", err)
		}
		if err := archive.SaveSession(&session); err != nil {
			return fmt.Errorf("failed to save session: %w", err)
		}
		result.SessionsImported++

	case "entry":
		if store == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("failed to parse entry: %w", err)
		}

		if existing, found := store.GetEntry(entry.ID); found {
			switch i.config.MergeStrategy {
			case MergeStrategySkip:
				atomic.AddInt64(&i.stats.entriesSkipped, 1)
				result.EntriesSkipped++
				return nil
			case MergeStrategyNewest:
				if existing.UpdatedAt.After(entry.UpdatedAt) {
					atomic.AddInt64(&i.stats.entriesSkipped, 1)
					result.EntriesSkipped++
					return nil
				}
			case MergeStrategyOverwrite:
			}
		}

		if _, err := store.InsertEntry(&entry); err != nil {
			return fmt.Errorf("failed to insert entry: %w", err)
		}
		atomic.AddInt64(&i.stats.entriesImported, 1)
		result.EntriesImported++

	case "decision":
		if archive == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var fact FactDecision
		if err := json.Unmarshal(data, &fact); err != nil {
			return fmt.Errorf("failed to parse decision: %w", err)
		}
		if err := archive.SaveFactDecision(&fact); err != nil {
			return fmt.Errorf("failed to save decision: %w", err)
		}
		result.FactsImported++

	case "pattern":
		if archive == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var fact FactPattern
		if err := json.Unmarshal(data, &fact); err != nil {
			return fmt.Errorf("failed to parse pattern: %w", err)
		}
		if err := archive.SaveFactPattern(&fact); err != nil {
			return fmt.Errorf("failed to save pattern: %w", err)
		}
		result.FactsImported++

	case "failure":
		if archive == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var fact FactFailure
		if err := json.Unmarshal(data, &fact); err != nil {
			return fmt.Errorf("failed to parse failure: %w", err)
		}
		if err := archive.SaveFactFailure(&fact); err != nil {
			return fmt.Errorf("failed to save failure: %w", err)
		}
		result.FactsImported++

	case "file_change":
		if archive == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var fact FactFileChange
		if err := json.Unmarshal(data, &fact); err != nil {
			return fmt.Errorf("failed to parse file_change: %w", err)
		}
		if err := archive.SaveFactFileChange(&fact); err != nil {
			return fmt.Errorf("failed to save file_change: %w", err)
		}
		result.FactsImported++

	case "summary":
		if archive == nil {
			return nil
		}
		data, _ := json.Marshal(item.Data)
		var summary CompactedSummary
		if err := json.Unmarshal(data, &summary); err != nil {
			return fmt.Errorf("failed to parse summary: %w", err)
		}
		if err := archive.SaveSummary(&summary); err != nil {
			return fmt.Errorf("failed to save summary: %w", err)
		}
		result.SummariesImported++

	default:
		return fmt.Errorf("unknown item type: %s", item.Type)
	}

	return nil
}

type ImportResult struct {
	StartedAt         time.Time     `json:"started_at"`
	CompletedAt       time.Time     `json:"completed_at"`
	Duration          time.Duration `json:"duration"`
	SessionsImported  int           `json:"sessions_imported"`
	EntriesImported   int           `json:"entries_imported"`
	EntriesSkipped    int           `json:"entries_skipped"`
	FactsImported     int           `json:"facts_imported"`
	SummariesImported int           `json:"summaries_imported"`
	DryRunCount       int           `json:"dry_run_count,omitempty"`
	Errors            []string      `json:"errors,omitempty"`
}

func (i *Importer) Stats() ImporterStats {
	return ImporterStats{
		TotalImports:    atomic.LoadInt64(&i.stats.totalImports),
		EntriesImported: atomic.LoadInt64(&i.stats.entriesImported),
		EntriesSkipped:  atomic.LoadInt64(&i.stats.entriesSkipped),
		ImportErrors:    atomic.LoadInt64(&i.stats.importErrors),
	}
}

func (e *Exporter) Stats() ExporterStats {
	return ExporterStats{
		TotalExports:    atomic.LoadInt64(&e.stats.totalExports),
		TotalImports:    atomic.LoadInt64(&e.stats.totalImports),
		EntriesExported: atomic.LoadInt64(&e.stats.entriesExported),
		EntriesImported: atomic.LoadInt64(&e.stats.entriesImported),
		BytesExported:   atomic.LoadInt64(&e.stats.bytesExported),
		BytesImported:   atomic.LoadInt64(&e.stats.bytesImported),
		ExportErrors:    atomic.LoadInt64(&e.stats.exportErrors),
		ImportErrors:    atomic.LoadInt64(&e.stats.importErrors),
	}
}
