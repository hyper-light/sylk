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
	if err := e.writeHeader(encoder); err != nil {
		return err
	}
	if err := e.writeSessions(encoder, archive); err != nil {
		return err
	}
	if err := e.writeEntries(encoder, store); err != nil {
		return err
	}
	if err := e.writeFacts(encoder, archive); err != nil {
		return err
	}
	return e.writeSummaries(encoder, archive)
}

func (e *Exporter) writeHeader(encoder *json.Encoder) error {
	return encoder.Encode(ExportItem{Type: "header", Data: map[string]any{
		"version":     "1.0",
		"exported_at": time.Now(),
		"format":      "jsonl",
	}})
}

func (e *Exporter) writeSessions(encoder *json.Encoder, archive *Archive) error {
	if archive == nil {
		return nil
	}
	sessions, _ := archive.GetRecentSessions(0)
	return e.writeSessionItems(encoder, sessions)
}

func (e *Exporter) writeSessionItems(encoder *json.Encoder, sessions []*Session) error {
	for _, session := range sessions {
		item := ExportItem{Type: "session", Data: session}
		if err := encoder.Encode(item); err != nil {
			atomic.AddInt64(&e.stats.exportErrors, 1)
			continue
		}
	}
	return nil
}

func (e *Exporter) writeEntries(encoder *json.Encoder, store *Store) error {
	if store == nil {
		return nil
	}
	entries, _ := store.Query(ArchiveQuery{IncludeArchived: e.config.IncludeArchived})
	return e.writeEntryItems(encoder, entries)
}

func (e *Exporter) writeEntryItems(encoder *json.Encoder, entries []*Entry) error {
	for _, entry := range entries {
		item := ExportItem{Type: "entry", Data: entry}
		if err := encoder.Encode(item); err != nil {
			atomic.AddInt64(&e.stats.exportErrors, 1)
			continue
		}
		atomic.AddInt64(&e.stats.entriesExported, 1)
	}
	return nil
}

func (e *Exporter) writeFacts(encoder *json.Encoder, archive *Archive) error {
	if !e.config.IncludeFacts || archive == nil {
		return nil
	}

	e.writeFactDecisionItems(encoder, archive)
	e.writeFactPatternItems(encoder, archive)
	e.writeFactFailureItems(encoder, archive)
	e.writeFactFileChangeItems(encoder, archive)
	return nil
}

func (e *Exporter) writeFactDecisionItems(encoder *json.Encoder, archive *Archive) {
	decisions, _ := archive.QueryFactDecisions("", 0)
	e.writeDecisionItems(encoder, "decision", decisions)
}

func (e *Exporter) writeFactPatternItems(encoder *json.Encoder, archive *Archive) {
	patterns, _ := archive.QueryFactPatterns("", 0)
	e.writePatternItems(encoder, "pattern", patterns)
}

func (e *Exporter) writeFactFailureItems(encoder *json.Encoder, archive *Archive) {
	failures, _ := archive.QueryFactFailures("", 0)
	e.writeFailureItems(encoder, "failure", failures)
}

func (e *Exporter) writeFactFileChangeItems(encoder *json.Encoder, archive *Archive) {
	fileChanges, _ := archive.QueryFactFileChanges("", 0)
	e.writeFileChangeItems(encoder, "file_change", fileChanges)
}

func (e *Exporter) writeDecisionItems(encoder *json.Encoder, itemType string, items []*FactDecision) error {
	for _, item := range items {
		encoder.Encode(ExportItem{Type: itemType, Data: item})
	}
	return nil
}

func (e *Exporter) writePatternItems(encoder *json.Encoder, itemType string, items []*FactPattern) error {
	for _, item := range items {
		encoder.Encode(ExportItem{Type: itemType, Data: item})
	}
	return nil
}

func (e *Exporter) writeFailureItems(encoder *json.Encoder, itemType string, items []*FactFailure) error {
	for _, item := range items {
		encoder.Encode(ExportItem{Type: itemType, Data: item})
	}
	return nil
}

func (e *Exporter) writeFileChangeItems(encoder *json.Encoder, itemType string, items []*FactFileChange) error {
	for _, item := range items {
		encoder.Encode(ExportItem{Type: itemType, Data: item})
	}
	return nil
}

func (e *Exporter) writeSummaries(encoder *json.Encoder, archive *Archive) error {
	if !e.config.IncludeSummaries || archive == nil {
		return nil
	}
	summaries, _ := archive.QuerySummaries(SummaryQuery{})
	return e.writeSummaryItems(encoder, summaries)
}

func (e *Exporter) writeSummaryItems(encoder *json.Encoder, summaries []*CompactedSummary) error {
	for _, summary := range summaries {
		encoder.Encode(ExportItem{Type: "summary", Data: summary})
	}
	return nil
}

func (e *Exporter) buildExportData(store *Store, archive *Archive, format string) *ExportData {
	data := &ExportData{
		Version:    "1.0",
		ExportedAt: time.Now(),
		Format:     format,
	}

	e.addSessions(data, archive)
	e.addEntries(data, store)
	e.addFacts(data, archive)
	e.addSummaries(data, archive)
	e.computeTotalItems(data)

	return data
}

func (e *Exporter) addSessions(data *ExportData, archive *Archive) {
	if archive == nil {
		return
	}
	sessions, err := archive.GetRecentSessions(0)
	if err == nil {
		data.Sessions = sessions
	}
}

func (e *Exporter) addEntries(data *ExportData, store *Store) {
	if store == nil {
		return
	}
	entries, err := store.Query(ArchiveQuery{IncludeArchived: e.config.IncludeArchived})
	if err == nil {
		data.Entries = entries
		atomic.AddInt64(&e.stats.entriesExported, int64(len(entries)))
	}
}

func (e *Exporter) addFacts(data *ExportData, archive *Archive) {
	if !e.config.IncludeFacts || archive == nil {
		return
	}
	decisions, _ := archive.QueryFactDecisions("", 0)
	patterns, _ := archive.QueryFactPatterns("", 0)
	failures, _ := archive.QueryFactFailures("", 0)
	fileChanges, _ := archive.QueryFactFileChanges("", 0)

	data.Decisions = decisions
	data.Patterns = patterns
	data.Failures = failures
	data.FileChanges = fileChanges
}

func (e *Exporter) addSummaries(data *ExportData, archive *Archive) {
	if !e.config.IncludeSummaries || archive == nil {
		return
	}
	summaries, _ := archive.QuerySummaries(SummaryQuery{})
	data.Summaries = summaries
}

func (e *Exporter) computeTotalItems(data *ExportData) {
	total := len(data.Entries) + len(data.Sessions)
	total += len(data.Decisions) + len(data.Patterns)
	total += len(data.Failures) + len(data.FileChanges)
	total += len(data.Summaries)
	data.TotalItems = total
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

	trimmed, err := readImportBytes(r)
	if err != nil {
		return nil, err
	}
	if len(trimmed) == 0 {
		return result, nil
	}

	first, secondErr, err := decodeImportHeaders(trimmed)
	if err != nil {
		return nil, err
	}

	if err := i.dispatchImportPayload(first, secondErr, trimmed, store, archive, result); err != nil {
		return nil, err
	}

	finalizeImportResult(result)
	return result, nil
}

func readImportBytes(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read import data: %w", err)
	}
	return bytes.TrimSpace(data), nil
}

func decodeImportHeaders(trimmed []byte) (json.RawMessage, error, error) {
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return nil, nil, fmt.Errorf("failed to parse import data: %w", err)
	}
	var second json.RawMessage
	secondErr := decoder.Decode(&second)
	return first, secondErr, nil
}

func (i *Importer) dispatchImportPayload(first json.RawMessage, secondErr error, trimmed []byte, store *Store, archive *Archive, result *ImportResult) error {
	if secondErr == nil {
		return i.importJSONL(bytes.NewReader(trimmed), store, archive, result)
	}
	if secondErr != io.EOF {
		return fmt.Errorf("failed to parse import data: %w", secondErr)
	}
	return i.importSingleJSON(first, store, archive, result)
}

func (i *Importer) importSingleJSON(first json.RawMessage, store *Store, archive *Archive, result *ImportResult) error {
	if len(first) == 0 {
		return nil
	}
	if first[0] == '[' {
		return fmt.Errorf("JSON array format not supported")
	}
	return i.importJSON(bytes.NewReader(first), store, archive, result)
}

func finalizeImportResult(result *ImportResult) {
	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(result.StartedAt)
}

func (i *Importer) importJSON(r io.Reader, store *Store, archive *Archive, result *ImportResult) error {
	data, err := i.decodeExportData(r)
	if err != nil {
		return err
	}

	i.importSessions(data, archive, result)
	i.importEntries(data, store, archive, result)
	i.importDecisions(data, store, archive, result)
	i.importPatterns(data, store, archive, result)
	i.importFailures(data, store, archive, result)
	i.importFileChanges(data, store, archive, result)
	i.importSummaries(data, store, archive, result)

	return nil
}

func (i *Importer) decodeExportData(r io.Reader) (*ExportData, error) {
	var data ExportData
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON import: %w", err)
	}
	return &data, nil
}

func (i *Importer) importSessions(data *ExportData, archive *Archive, result *ImportResult) {
	if archive == nil {
		return
	}
	for _, session := range data.Sessions {
		i.importSessionItem(session, archive, result)
	}
}

func (i *Importer) importSessionItem(session *Session, archive *Archive, result *ImportResult) {
	if session == nil {
		return
	}
	if err := archive.SaveSession(session); err != nil {
		result.Errors = append(result.Errors, err.Error())
		atomic.AddInt64(&i.stats.importErrors, 1)
		return
	}
	result.SessionsImported++
}

func (i *Importer) importEntries(data *ExportData, store *Store, archive *Archive, result *ImportResult) {
	if store == nil {
		return
	}
	for _, entry := range data.Entries {
		i.importEntryItem(entry, store, archive, result)
	}
}

func (i *Importer) importEntryItem(entry *Entry, store *Store, archive *Archive, result *ImportResult) {
	if entry == nil {
		return
	}
	item := ExportItem{Type: "entry", Data: entry}
	if err := i.importItem(&item, store, archive, result); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func (i *Importer) importDecisions(data *ExportData, store *Store, archive *Archive, result *ImportResult) {
	if archive == nil {
		return
	}
	for _, decision := range data.Decisions {
		i.importDecisionItem(decision, store, archive, result)
	}
}

func (i *Importer) importDecisionItem(decision *FactDecision, store *Store, archive *Archive, result *ImportResult) {
	if decision == nil {
		return
	}
	item := ExportItem{Type: "decision", Data: decision}
	if err := i.importItem(&item, store, archive, result); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func (i *Importer) importPatterns(data *ExportData, store *Store, archive *Archive, result *ImportResult) {
	if archive == nil {
		return
	}
	for _, pattern := range data.Patterns {
		i.importPatternItem(pattern, store, archive, result)
	}
}

func (i *Importer) importPatternItem(pattern *FactPattern, store *Store, archive *Archive, result *ImportResult) {
	if pattern == nil {
		return
	}
	item := ExportItem{Type: "pattern", Data: pattern}
	if err := i.importItem(&item, store, archive, result); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func (i *Importer) importFailures(data *ExportData, store *Store, archive *Archive, result *ImportResult) {
	if archive == nil {
		return
	}
	for _, failure := range data.Failures {
		i.importFailureItem(failure, store, archive, result)
	}
}

func (i *Importer) importFailureItem(failure *FactFailure, store *Store, archive *Archive, result *ImportResult) {
	if failure == nil {
		return
	}
	item := ExportItem{Type: "failure", Data: failure}
	if err := i.importItem(&item, store, archive, result); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func (i *Importer) importFileChanges(data *ExportData, store *Store, archive *Archive, result *ImportResult) {
	if archive == nil {
		return
	}
	for _, fileChange := range data.FileChanges {
		i.importFileChangeItem(fileChange, store, archive, result)
	}
}

func (i *Importer) importFileChangeItem(fileChange *FactFileChange, store *Store, archive *Archive, result *ImportResult) {
	if fileChange == nil {
		return
	}
	item := ExportItem{Type: "file_change", Data: fileChange}
	if err := i.importItem(&item, store, archive, result); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func (i *Importer) importSummaries(data *ExportData, store *Store, archive *Archive, result *ImportResult) {
	if archive == nil {
		return
	}
	for _, summary := range data.Summaries {
		i.importSummaryItem(summary, store, archive, result)
	}
}

func (i *Importer) importSummaryItem(summary *CompactedSummary, store *Store, archive *Archive, result *ImportResult) {
	if summary == nil {
		return
	}
	item := ExportItem{Type: "summary", Data: summary}
	if err := i.importItem(&item, store, archive, result); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func (i *Importer) importJSONL(r io.Reader, store *Store, archive *Archive, result *ImportResult) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		item, ok := i.parseJSONLLine(scanner.Bytes(), result)
		if !ok {
			continue
		}
		if err := i.importItem(item, store, archive, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}

	return scanner.Err()
}

func (i *Importer) parseJSONLLine(line []byte, result *ImportResult) (*ExportItem, bool) {
	if len(line) == 0 {
		return nil, false
	}

	var item ExportItem
	if err := json.Unmarshal(line, &item); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("failed to parse line: %v", err))
		atomic.AddInt64(&i.stats.importErrors, 1)
		return nil, false
	}

	return &item, true
}

func (i *Importer) importItem(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
	if i.config.DryRun {
		result.DryRunCount++
		return nil
	}

	handler := i.itemHandler(item.Type)
	if handler == nil {
		return fmt.Errorf("unknown item type: %s", item.Type)
	}
	return handler(item, store, archive, result)
}

func (i *Importer) itemHandler(itemType string) func(*ExportItem, *Store, *Archive, *ImportResult) error {
	handlers := map[string]func(*ExportItem, *Store, *Archive, *ImportResult) error{
		"header":      i.handleHeader,
		"session":     i.handleSession,
		"entry":       i.handleEntry,
		"decision":    i.handleDecision,
		"pattern":     i.handlePattern,
		"failure":     i.handleFailure,
		"file_change": i.handleFileChange,
		"summary":     i.handleSummary,
	}
	return handlers[itemType]
}

func (i *Importer) handleHeader(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
	return nil
}

func (i *Importer) handleSession(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
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
	return nil
}

func (i *Importer) handleEntry(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
	if store == nil {
		return nil
	}
	data, _ := json.Marshal(item.Data)
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return fmt.Errorf("failed to parse entry: %w", err)
	}
	if i.shouldSkipEntry(store, &entry, result) {
		return nil
	}
	if _, err := store.InsertEntry(&entry); err != nil {
		return fmt.Errorf("failed to insert entry: %w", err)
	}
	atomic.AddInt64(&i.stats.entriesImported, 1)
	result.EntriesImported++
	return nil
}

func (i *Importer) shouldSkipEntry(store *Store, entry *Entry, result *ImportResult) bool {
	if existing, found := store.GetEntry(entry.ID); found {
		return i.mergeSkip(existing, entry, result)
	}
	return false
}

func (i *Importer) mergeSkip(existing *Entry, entry *Entry, result *ImportResult) bool {
	if i.config.MergeStrategy == MergeStrategySkip {
		return i.skipEntry(result)
	}
	if i.config.MergeStrategy == MergeStrategyOverwrite {
		return false
	}
	if i.config.MergeStrategy == MergeStrategyNewest {
		return i.skipIfExistingNewer(existing, entry, result)
	}
	return false
}

func (i *Importer) skipEntry(result *ImportResult) bool {
	atomic.AddInt64(&i.stats.entriesSkipped, 1)
	result.EntriesSkipped++
	return true
}

func (i *Importer) skipIfExistingNewer(existing *Entry, entry *Entry, result *ImportResult) bool {
	if !existing.UpdatedAt.After(entry.UpdatedAt) {
		return false
	}
	return i.skipEntry(result)
}

func (i *Importer) handleDecision(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
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
	return nil
}

func (i *Importer) handlePattern(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
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
	return nil
}

func (i *Importer) handleFailure(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
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
	return nil
}

func (i *Importer) handleFileChange(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
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
	return nil
}

func (i *Importer) handleSummary(item *ExportItem, store *Store, archive *Archive, result *ImportResult) error {
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
