package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// Pipeline Stage Enum
// =============================================================================

// PipelineStage represents a stage in the extraction pipeline.
type PipelineStage int

const (
	// StageEntityExtraction extracts entities from source files.
	StageEntityExtraction PipelineStage = 0

	// StageRelationExtraction extracts relations between entities.
	StageRelationExtraction PipelineStage = 1

	// StageEntityLinking links entity references to definitions.
	StageEntityLinking PipelineStage = 2

	// StageRelationValidation validates extracted relations.
	StageRelationValidation PipelineStage = 3
)

// String returns the string representation of the pipeline stage.
func (ps PipelineStage) String() string {
	switch ps {
	case StageEntityExtraction:
		return "entity_extraction"
	case StageRelationExtraction:
		return "relation_extraction"
	case StageEntityLinking:
		return "entity_linking"
	case StageRelationValidation:
		return "relation_validation"
	default:
		return "unknown"
	}
}

// ParsePipelineStage parses a string into a PipelineStage.
func ParsePipelineStage(s string) (PipelineStage, bool) {
	switch s {
	case "entity_extraction":
		return StageEntityExtraction, true
	case "relation_extraction":
		return StageRelationExtraction, true
	case "entity_linking":
		return StageEntityLinking, true
	case "relation_validation":
		return StageRelationValidation, true
	default:
		return StageEntityExtraction, false
	}
}

// =============================================================================
// Pipeline Configuration
// =============================================================================

// PipelineConfig contains configuration options for the extraction pipeline.
type PipelineConfig struct {
	// EnabledStages specifies which pipeline stages are enabled.
	// All stages are enabled by default.
	EnabledStages map[PipelineStage]bool

	// LanguageFilters specifies which languages to process.
	// If empty, all detected languages are processed.
	LanguageFilters []string

	// ParallelExtraction enables parallel entity extraction per language.
	ParallelExtraction bool

	// MaxWorkers is the maximum number of concurrent workers for parallel extraction.
	MaxWorkers int

	// MinConfidence is the minimum confidence threshold for relations.
	MinConfidence float64

	// DeduplicateEntities enables entity deduplication.
	DeduplicateEntities bool

	// DeduplicateRelations enables relation deduplication.
	DeduplicateRelations bool

	// ContinueOnError allows the pipeline to continue even if individual files fail.
	ContinueOnError bool

	// LinkerConfig is the configuration for the entity linker.
	LinkerConfig EntityLinkerConfig

	// ValidatorConfig is the configuration for the relation validator.
	ValidatorConfig RelationValidatorConfig
}

// DefaultPipelineConfig returns the default configuration for the extraction pipeline.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		EnabledStages: map[PipelineStage]bool{
			StageEntityExtraction:   true,
			StageRelationExtraction: true,
			StageEntityLinking:      true,
			StageRelationValidation: true,
		},
		LanguageFilters:      nil, // Process all languages
		ParallelExtraction:   true,
		MaxWorkers:           4,
		MinConfidence:        0.3,
		DeduplicateEntities:  true,
		DeduplicateRelations: true,
		ContinueOnError:      true,
		LinkerConfig:         DefaultEntityLinkerConfig(),
		ValidatorConfig:      DefaultRelationValidatorConfig(),
	}
}

// =============================================================================
// Progress Callback
// =============================================================================

// ProgressInfo contains information about the current pipeline progress.
type ProgressInfo struct {
	// Stage is the current pipeline stage.
	Stage PipelineStage

	// TotalFiles is the total number of files to process.
	TotalFiles int

	// ProcessedFiles is the number of files processed so far.
	ProcessedFiles int

	// CurrentFile is the file currently being processed.
	CurrentFile string

	// Language is the language of the current file.
	Language string

	// EntitiesExtracted is the total number of entities extracted so far.
	EntitiesExtracted int

	// RelationsExtracted is the total number of relations extracted so far.
	RelationsExtracted int

	// Errors contains any errors encountered during processing.
	Errors []error
}

// ProgressCallback is called to report progress during pipeline execution.
type ProgressCallback func(info ProgressInfo)

// =============================================================================
// File Metrics
// =============================================================================

// FileMetrics contains metrics for a single file extraction.
type FileMetrics struct {
	FilePath       string        `json:"file_path"`
	Language       string        `json:"language"`
	EntitiesCount  int           `json:"entities_count"`
	RelationsCount int           `json:"relations_count"`
	Duration       time.Duration `json:"duration"`
	Error          error         `json:"error,omitempty"`
}

// =============================================================================
// Extraction Result
// =============================================================================

// ExtractionResult contains the results of the extraction pipeline.
type ExtractionResult struct {
	// Entities contains all extracted entities.
	Entities []ExtractedEntity `json:"entities"`

	// Relations contains all extracted relations.
	Relations []ExtractedRelation `json:"relations"`

	// Links contains all entity links.
	Links []EntityLink `json:"links"`

	// ValidationResults contains validation results for each relation.
	ValidationResults []ValidationResult `json:"validation_results"`

	// FileMetrics contains metrics for each processed file.
	FileMetrics []FileMetrics `json:"file_metrics"`

	// Errors contains any errors encountered during extraction.
	Errors []ExtractionError `json:"errors,omitempty"`

	// TotalDuration is the total time taken for extraction.
	TotalDuration time.Duration `json:"total_duration"`

	// StartTime is when the extraction started.
	StartTime time.Time `json:"start_time"`

	// EndTime is when the extraction completed.
	EndTime time.Time `json:"end_time"`
}

// ExtractionError represents an error that occurred during extraction.
type ExtractionError struct {
	FilePath string `json:"file_path"`
	Stage    string `json:"stage"`
	Message  string `json:"message"`
	Error    error  `json:"-"`
}

// =============================================================================
// Entity Extractor Interface
// =============================================================================

// PipelineEntityExtractor is the interface for language-specific entity extractors.
// This is defined locally to avoid import cycles with the extractors package.
type PipelineEntityExtractor interface {
	// ExtractEntities extracts entities from source code.
	ExtractEntities(filePath string, content []byte) ([]ExtractedEntity, error)
}

// =============================================================================
// Relation Extractor Interface
// =============================================================================

// PipelineRelationExtractor is the interface for relation extractors.
type PipelineRelationExtractor interface {
	// ExtractRelations extracts relations from entities and content.
	ExtractRelations(entities []ExtractedEntity, content map[string][]byte) ([]ExtractedRelation, error)
}

// =============================================================================
// Extraction Pipeline
// =============================================================================

// ExtractionPipeline orchestrates the full extraction workflow.
type ExtractionPipeline struct {
	mu sync.RWMutex

	// config holds the pipeline configuration.
	config PipelineConfig

	// entityExtractors maps language names to their extractors.
	entityExtractors map[string]PipelineEntityExtractor

	// relationExtractors contains the relation extractors.
	relationExtractors []PipelineRelationExtractor

	// linker links entity references to definitions.
	linker *EntityLinker

	// validator validates extracted relations.
	validator *RelationValidator

	// symbolTable manages symbols for cross-reference resolution.
	symbolTable *SymbolTable

	// progressCallback is called to report progress.
	progressCallback ProgressCallback
}

// NewExtractionPipeline creates a new extraction pipeline with default configuration.
func NewExtractionPipeline() *ExtractionPipeline {
	return NewExtractionPipelineWithConfig(DefaultPipelineConfig())
}

// NewExtractionPipelineWithConfig creates a new extraction pipeline with custom configuration.
func NewExtractionPipelineWithConfig(config PipelineConfig) *ExtractionPipeline {
	symbolTable := NewSymbolTable()

	pipeline := &ExtractionPipeline{
		config:             config,
		entityExtractors:   make(map[string]PipelineEntityExtractor),
		relationExtractors: make([]PipelineRelationExtractor, 0),
		linker:             NewEntityLinkerWithConfig(symbolTable, config.LinkerConfig),
		validator:          NewRelationValidatorWithConfig(config.ValidatorConfig),
		symbolTable:        symbolTable,
	}

	// Register default extractors
	pipeline.RegisterExtractor("go", newGoEntityExtractor())
	pipeline.RegisterExtractor("typescript", newTypeScriptEntityExtractor())
	pipeline.RegisterExtractor("javascript", newTypeScriptEntityExtractor())
	pipeline.RegisterExtractor("python", newPythonEntityExtractor())

	// Register default relation extractors
	pipeline.RegisterRelationExtractor(newImportRelationExtractor())
	pipeline.RegisterRelationExtractor(newCallRelationExtractor())

	return pipeline
}

// RegisterExtractor registers an entity extractor for a specific language.
func (p *ExtractionPipeline) RegisterExtractor(lang string, extractor PipelineEntityExtractor) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.entityExtractors[strings.ToLower(lang)] = extractor
}

// RegisterRelationExtractor registers a relation extractor.
func (p *ExtractionPipeline) RegisterRelationExtractor(extractor PipelineRelationExtractor) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.relationExtractors = append(p.relationExtractors, extractor)
}

// SetProgressCallback sets the progress callback function.
func (p *ExtractionPipeline) SetProgressCallback(callback ProgressCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.progressCallback = callback
}

// SetConfig updates the pipeline configuration.
func (p *ExtractionPipeline) SetConfig(config PipelineConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.config = config
	p.linker = NewEntityLinkerWithConfig(p.symbolTable, config.LinkerConfig)
	p.validator = NewRelationValidatorWithConfig(config.ValidatorConfig)
}

// GetConfig returns the current pipeline configuration.
func (p *ExtractionPipeline) GetConfig() PipelineConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.config
}

// Extract performs the full extraction pipeline on the provided files.
func (p *ExtractionPipeline) Extract(files map[string][]byte) ExtractionResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	startTime := time.Now()
	result := ExtractionResult{
		StartTime:   startTime,
		FileMetrics: make([]FileMetrics, 0, len(files)),
		Errors:      make([]ExtractionError, 0),
	}

	// Group files by language
	filesByLang := p.groupFilesByLanguage(files)

	// Stage 1: Entity Extraction
	if p.config.EnabledStages[StageEntityExtraction] {
		entities, metrics, errors := p.extractEntities(filesByLang, files)
		result.Entities = entities
		result.FileMetrics = append(result.FileMetrics, metrics...)
		result.Errors = append(result.Errors, errors...)
	}

	// Stage 2: Relation Extraction
	if p.config.EnabledStages[StageRelationExtraction] {
		relations, errors := p.extractRelations(result.Entities, files)
		result.Relations = relations
		result.Errors = append(result.Errors, errors...)
	}

	// Stage 3: Entity Linking
	if p.config.EnabledStages[StageEntityLinking] {
		links := p.linkEntities(result.Entities, files)
		result.Links = links
	}

	// Stage 4: Relation Validation
	if p.config.EnabledStages[StageRelationValidation] {
		validationResults := p.validateRelations(result.Relations, result.Entities)
		result.ValidationResults = validationResults
	}

	result.EndTime = time.Now()
	result.TotalDuration = result.EndTime.Sub(startTime)

	return result
}

// ExtractIncremental performs incremental extraction on changed files.
// It uses existing entities to avoid re-extracting unchanged files.
func (p *ExtractionPipeline) ExtractIncremental(
	changedFiles map[string][]byte,
	existingEntities []ExtractedEntity,
) ExtractionResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	startTime := time.Now()
	result := ExtractionResult{
		StartTime:   startTime,
		FileMetrics: make([]FileMetrics, 0, len(changedFiles)),
		Errors:      make([]ExtractionError, 0),
	}

	// Build a set of changed file paths
	changedFilePaths := make(map[string]bool)
	for filePath := range changedFiles {
		changedFilePaths[filePath] = true
	}

	// Filter existing entities to remove those from changed files
	var unchangedEntities []ExtractedEntity
	for _, entity := range existingEntities {
		if !changedFilePaths[entity.FilePath] {
			unchangedEntities = append(unchangedEntities, entity)
		}
	}

	// Group changed files by language
	filesByLang := p.groupFilesByLanguage(changedFiles)

	// Stage 1: Entity Extraction (only changed files)
	if p.config.EnabledStages[StageEntityExtraction] {
		newEntities, metrics, errors := p.extractEntities(filesByLang, changedFiles)
		// Combine unchanged and new entities
		result.Entities = append(unchangedEntities, newEntities...)
		result.FileMetrics = append(result.FileMetrics, metrics...)
		result.Errors = append(result.Errors, errors...)
	} else {
		result.Entities = existingEntities
	}

	// Stage 2: Relation Extraction (rebuild all relations with combined entities)
	if p.config.EnabledStages[StageRelationExtraction] {
		// For incremental, we need the full file content map
		// We'll extract relations only from changed files but use all entities for resolution
		relations, errors := p.extractRelationsIncremental(result.Entities, changedFiles)
		result.Relations = relations
		result.Errors = append(result.Errors, errors...)
	}

	// Stage 3: Entity Linking
	if p.config.EnabledStages[StageEntityLinking] {
		links := p.linkEntities(result.Entities, changedFiles)
		result.Links = links
	}

	// Stage 4: Relation Validation
	if p.config.EnabledStages[StageRelationValidation] {
		validationResults := p.validateRelations(result.Relations, result.Entities)
		result.ValidationResults = validationResults
	}

	result.EndTime = time.Now()
	result.TotalDuration = result.EndTime.Sub(startTime)

	return result
}

// groupFilesByLanguage groups files by their detected language.
func (p *ExtractionPipeline) groupFilesByLanguage(files map[string][]byte) map[string][]string {
	filesByLang := make(map[string][]string)

	for filePath := range files {
		lang := p.detectLanguage(filePath)
		if lang == "" {
			continue
		}

		// Apply language filter if set
		if len(p.config.LanguageFilters) > 0 {
			found := false
			for _, filter := range p.config.LanguageFilters {
				if strings.EqualFold(filter, lang) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		filesByLang[lang] = append(filesByLang[lang], filePath)
	}

	return filesByLang
}

// detectLanguage detects the programming language from a file path.
func (p *ExtractionPipeline) detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py", ".pyw":
		return "python"
	default:
		return ""
	}
}

// extractEntities extracts entities from files grouped by language.
func (p *ExtractionPipeline) extractEntities(
	filesByLang map[string][]string,
	files map[string][]byte,
) ([]ExtractedEntity, []FileMetrics, []ExtractionError) {
	var allEntities []ExtractedEntity
	var allMetrics []FileMetrics
	var allErrors []ExtractionError

	// Count total files for progress
	totalFiles := 0
	for _, langFiles := range filesByLang {
		totalFiles += len(langFiles)
	}
	processedFiles := 0

	if p.config.ParallelExtraction {
		// Parallel extraction per language
		var wg sync.WaitGroup
		var mu sync.Mutex

		// Create worker pool
		type workItem struct {
			lang     string
			filePath string
		}

		workChan := make(chan workItem, totalFiles)
		resultChan := make(chan struct {
			entities []ExtractedEntity
			metric   FileMetrics
			errors   []ExtractionError
		}, totalFiles)

		// Start workers
		numWorkers := p.config.MaxWorkers
		if numWorkers <= 0 {
			numWorkers = 1
		}

		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for work := range workChan {
					entities, metric, errors := p.extractFileEntities(work.lang, work.filePath, files[work.filePath])
					resultChan <- struct {
						entities []ExtractedEntity
						metric   FileMetrics
						errors   []ExtractionError
					}{entities, metric, errors}
				}
			}()
		}

		// Send work items
		go func() {
			for lang, langFiles := range filesByLang {
				for _, filePath := range langFiles {
					workChan <- workItem{lang, filePath}
				}
			}
			close(workChan)
		}()

		// Wait for workers and close result channel
		go func() {
			wg.Wait()
			close(resultChan)
		}()

		// Collect results
		for result := range resultChan {
			mu.Lock()
			allEntities = append(allEntities, result.entities...)
			allMetrics = append(allMetrics, result.metric)
			allErrors = append(allErrors, result.errors...)
			processedFiles++

			// Report progress
			if p.progressCallback != nil {
				p.progressCallback(ProgressInfo{
					Stage:             StageEntityExtraction,
					TotalFiles:        totalFiles,
					ProcessedFiles:    processedFiles,
					CurrentFile:       result.metric.FilePath,
					Language:          result.metric.Language,
					EntitiesExtracted: len(allEntities),
				})
			}
			mu.Unlock()
		}
	} else {
		// Sequential extraction
		for lang, langFiles := range filesByLang {
			for _, filePath := range langFiles {
				entities, metric, errors := p.extractFileEntities(lang, filePath, files[filePath])
				allEntities = append(allEntities, entities...)
				allMetrics = append(allMetrics, metric)
				allErrors = append(allErrors, errors...)
				processedFiles++

				// Report progress
				if p.progressCallback != nil {
					p.progressCallback(ProgressInfo{
						Stage:             StageEntityExtraction,
						TotalFiles:        totalFiles,
						ProcessedFiles:    processedFiles,
						CurrentFile:       filePath,
						Language:          lang,
						EntitiesExtracted: len(allEntities),
					})
				}
			}
		}
	}

	// Deduplicate entities if enabled
	if p.config.DeduplicateEntities {
		allEntities = p.deduplicateEntities(allEntities)
	}

	return allEntities, allMetrics, allErrors
}

// extractFileEntities extracts entities from a single file.
func (p *ExtractionPipeline) extractFileEntities(
	lang string,
	filePath string,
	content []byte,
) ([]ExtractedEntity, FileMetrics, []ExtractionError) {
	startTime := time.Now()

	metric := FileMetrics{
		FilePath: filePath,
		Language: lang,
	}

	var extractedEntities []ExtractedEntity
	var errors []ExtractionError

	extractor, hasExtractor := p.entityExtractors[lang]
	if !hasExtractor {
		if !p.config.ContinueOnError {
			errors = append(errors, ExtractionError{
				FilePath: filePath,
				Stage:    StageEntityExtraction.String(),
				Message:  "no extractor registered for language: " + lang,
			})
		}
		metric.Duration = time.Since(startTime)
		return extractedEntities, metric, errors
	}

	entities, err := extractor.ExtractEntities(filePath, content)
	if err != nil {
		metric.Error = err
		errors = append(errors, ExtractionError{
			FilePath: filePath,
			Stage:    StageEntityExtraction.String(),
			Message:  err.Error(),
			Error:    err,
		})
	} else {
		extractedEntities = entities
		metric.EntitiesCount = len(extractedEntities)
	}

	metric.Duration = time.Since(startTime)
	return extractedEntities, metric, errors
}

// deduplicateEntities removes duplicate entities based on file path, name, and kind.
func (p *ExtractionPipeline) deduplicateEntities(entities []ExtractedEntity) []ExtractedEntity {
	seen := make(map[string]bool)
	var unique []ExtractedEntity

	for _, entity := range entities {
		key := generateEntityKey(entity)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, entity)
		}
	}

	return unique
}

// generateEntityKey generates a unique key for an entity.
func generateEntityKey(entity ExtractedEntity) string {
	key := entity.FilePath + ":" + entity.Kind.String() + ":" + entity.Name + ":" + string(rune(entity.StartLine))
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:16])
}

// extractRelations extracts relations from the extracted entities.
func (p *ExtractionPipeline) extractRelations(
	entities []ExtractedEntity,
	files map[string][]byte,
) ([]ExtractedRelation, []ExtractionError) {
	var allRelations []ExtractedRelation
	var allErrors []ExtractionError

	// Run each relation extractor
	for _, extractor := range p.relationExtractors {
		relations, err := extractor.ExtractRelations(entities, files)
		if err != nil {
			allErrors = append(allErrors, ExtractionError{
				Stage:   StageRelationExtraction.String(),
				Message: "relation extraction failed: " + err.Error(),
				Error:   err,
			})
		} else {
			allRelations = append(allRelations, relations...)
		}
	}

	// Report progress
	if p.progressCallback != nil {
		p.progressCallback(ProgressInfo{
			Stage:              StageRelationExtraction,
			RelationsExtracted: len(allRelations),
		})
	}

	// Deduplicate relations if enabled
	if p.config.DeduplicateRelations {
		allRelations = p.deduplicateRelations(allRelations)
	}

	return allRelations, allErrors
}

// extractRelationsIncremental extracts relations for incremental updates.
func (p *ExtractionPipeline) extractRelationsIncremental(
	allEntities []ExtractedEntity,
	changedFiles map[string][]byte,
) ([]ExtractedRelation, []ExtractionError) {
	// For incremental extraction, we use all entities for resolution
	// but only extract from changed files
	return p.extractRelations(allEntities, changedFiles)
}

// deduplicateRelations removes duplicate relations.
func (p *ExtractionPipeline) deduplicateRelations(relations []ExtractedRelation) []ExtractedRelation {
	seen := make(map[string]bool)
	var unique []ExtractedRelation

	for _, relation := range relations {
		key := p.generateRelationKey(relation)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, relation)
		}
	}

	return unique
}

// generateRelationKey generates a unique key for a relation.
func (p *ExtractionPipeline) generateRelationKey(relation ExtractedRelation) string {
	sourceKey := ""
	targetKey := ""

	if relation.SourceEntity != nil {
		sourceKey = relation.SourceEntity.FilePath + ":" + relation.SourceEntity.Name
	}
	if relation.TargetEntity != nil {
		targetKey = relation.TargetEntity.FilePath + ":" + relation.TargetEntity.Name
	}

	key := sourceKey + "->" + relation.RelationType.String() + "->" + targetKey
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:16])
}

// linkEntities links entity references to their definitions.
func (p *ExtractionPipeline) linkEntities(
	entities []ExtractedEntity,
	files map[string][]byte,
) []EntityLink {
	// Clear and re-index entities
	p.linker.Clear()
	p.linker.IndexEntities(entities)

	// Link entities with content
	links := p.linker.LinkEntitiesWithContent(entities, files)

	// Report progress
	if p.progressCallback != nil {
		p.progressCallback(ProgressInfo{
			Stage:             StageEntityLinking,
			EntitiesExtracted: len(entities),
		})
	}

	return links
}

// validateRelations validates the extracted relations.
func (p *ExtractionPipeline) validateRelations(
	relations []ExtractedRelation,
	entities []ExtractedEntity,
) []ValidationResult {
	// Clear and re-index entities
	p.validator.Clear()
	p.validator.IndexEntities(entities)

	// Validate all relations
	results := p.validator.ValidateAll(relations, entities)

	// Report progress
	if p.progressCallback != nil {
		p.progressCallback(ProgressInfo{
			Stage:              StageRelationValidation,
			RelationsExtracted: len(relations),
		})
	}

	return results
}

// =============================================================================
// Pipeline Summary
// =============================================================================

// ExtractionSummary provides a summary of the extraction results.
type ExtractionSummary struct {
	TotalFiles         int               `json:"total_files"`
	TotalEntities      int               `json:"total_entities"`
	TotalRelations     int               `json:"total_relations"`
	TotalLinks         int               `json:"total_links"`
	ValidRelations     int               `json:"valid_relations"`
	InvalidRelations   int               `json:"invalid_relations"`
	ErrorCount         int               `json:"error_count"`
	EntitiesByKind     map[string]int    `json:"entities_by_kind"`
	EntitiesByLanguage map[string]int    `json:"entities_by_language"`
	RelationsByType    map[string]int    `json:"relations_by_type"`
	Duration           time.Duration     `json:"duration"`
	ValidationSummary  ValidationSummary `json:"validation_summary"`
}

// Summarize creates a summary of the extraction results.
func (p *ExtractionPipeline) Summarize(result ExtractionResult) ExtractionSummary {
	summary := ExtractionSummary{
		TotalFiles:         len(result.FileMetrics),
		TotalEntities:      len(result.Entities),
		TotalRelations:     len(result.Relations),
		TotalLinks:         len(result.Links),
		ErrorCount:         len(result.Errors),
		EntitiesByKind:     make(map[string]int),
		EntitiesByLanguage: make(map[string]int),
		RelationsByType:    make(map[string]int),
		Duration:           result.TotalDuration,
	}

	// Count entities by kind
	for _, entity := range result.Entities {
		summary.EntitiesByKind[entity.Kind.String()]++
	}

	// Count entities by language (based on file extension)
	for _, entity := range result.Entities {
		lang := p.detectLanguage(entity.FilePath)
		if lang != "" {
			summary.EntitiesByLanguage[lang]++
		}
	}

	// Count relations by type
	for _, relation := range result.Relations {
		summary.RelationsByType[relation.RelationType.String()]++
	}

	// Count valid/invalid relations
	for _, vr := range result.ValidationResults {
		if vr.Valid {
			summary.ValidRelations++
		} else {
			summary.InvalidRelations++
		}
	}

	// Get validation summary
	summary.ValidationSummary = p.validator.Summarize(result.ValidationResults)

	return summary
}

// =============================================================================
// Language Extensions Map
// =============================================================================

// SupportedLanguages returns a list of supported programming languages.
func (p *ExtractionPipeline) SupportedLanguages() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	languages := make([]string, 0, len(p.entityExtractors))
	for lang := range p.entityExtractors {
		languages = append(languages, lang)
	}
	return languages
}

// HasExtractor returns true if an extractor is registered for the given language.
func (p *ExtractionPipeline) HasExtractor(lang string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	_, exists := p.entityExtractors[strings.ToLower(lang)]
	return exists
}

// =============================================================================
// Built-in Entity Extractors
// =============================================================================

// goEntityExtractor extracts entities from Go source files.
type goEntityExtractor struct{}

func newGoEntityExtractor() *goEntityExtractor {
	return &goEntityExtractor{}
}

func (e *goEntityExtractor) ExtractEntities(filePath string, content []byte) ([]ExtractedEntity, error) {
	if len(content) == 0 {
		return []ExtractedEntity{}, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		// Return empty slice on parse errors (syntax errors)
		return []ExtractedEntity{}, nil
	}

	var entities []ExtractedEntity

	// Extract package name
	if file.Name != nil {
		entities = append(entities, ExtractedEntity{
			Name:       file.Name.Name,
			Kind:       EntityKindPackage,
			FilePath:   filePath,
			StartLine:  fset.Position(file.Package).Line,
			EndLine:    fset.Position(file.Package).Line,
			Signature:  "package " + file.Name.Name,
			Scope:      ScopeModule,
			Visibility: VisibilityPublic,
		})
	}

	// Walk through all declarations
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			entities = append(entities, e.extractFunc(fset, filePath, d)...)
		case *ast.GenDecl:
			entities = append(entities, e.extractGenDecl(fset, filePath, d)...)
		}
	}

	return entities, nil
}

func (e *goEntityExtractor) extractFunc(fset *token.FileSet, filePath string, fn *ast.FuncDecl) []ExtractedEntity {
	var entities []ExtractedEntity

	startLine := fset.Position(fn.Pos()).Line
	endLine := fset.Position(fn.End()).Line

	var sig strings.Builder
	sig.WriteString("func ")

	var kind EntityKind
	visibility := VisibilityPrivate

	// Check first character for visibility
	if len(fn.Name.Name) > 0 && fn.Name.Name[0] >= 'A' && fn.Name.Name[0] <= 'Z' {
		visibility = VisibilityPublic
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		// It's a method
		kind = EntityKindMethod
		recvType := e.getReceiverType(fn.Recv.List[0].Type)
		sig.WriteString("(")
		if len(fn.Recv.List[0].Names) > 0 {
			sig.WriteString(fn.Recv.List[0].Names[0].Name)
			sig.WriteString(" ")
		}
		sig.WriteString(recvType)
		sig.WriteString(") ")
	} else {
		kind = EntityKindFunction
	}

	sig.WriteString(fn.Name.Name)
	sig.WriteString(e.formatFuncParams(fn.Type))

	entity := ExtractedEntity{
		Name:       fn.Name.Name,
		Kind:       kind,
		FilePath:   filePath,
		StartLine:  startLine,
		EndLine:    endLine,
		Signature:  sig.String(),
		Scope:      ScopeModule,
		Visibility: visibility,
	}

	entities = append(entities, entity)
	return entities
}

func (e *goEntityExtractor) extractGenDecl(fset *token.FileSet, filePath string, decl *ast.GenDecl) []ExtractedEntity {
	var entities []ExtractedEntity

	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			entities = append(entities, e.extractTypeSpec(fset, filePath, s)...)
		}
	}

	return entities
}

func (e *goEntityExtractor) extractTypeSpec(fset *token.FileSet, filePath string, spec *ast.TypeSpec) []ExtractedEntity {
	var entities []ExtractedEntity

	startLine := fset.Position(spec.Pos()).Line
	endLine := fset.Position(spec.End()).Line

	var kind EntityKind
	var sig strings.Builder

	visibility := VisibilityPrivate
	if len(spec.Name.Name) > 0 && spec.Name.Name[0] >= 'A' && spec.Name.Name[0] <= 'Z' {
		visibility = VisibilityPublic
	}

	switch spec.Type.(type) {
	case *ast.StructType:
		kind = EntityKindStruct
		sig.WriteString("type ")
		sig.WriteString(spec.Name.Name)
		sig.WriteString(" struct")
	case *ast.InterfaceType:
		kind = EntityKindInterface
		sig.WriteString("type ")
		sig.WriteString(spec.Name.Name)
		sig.WriteString(" interface")
	default:
		kind = EntityKindType
		sig.WriteString("type ")
		sig.WriteString(spec.Name.Name)
	}

	entity := ExtractedEntity{
		Name:       spec.Name.Name,
		Kind:       kind,
		FilePath:   filePath,
		StartLine:  startLine,
		EndLine:    endLine,
		Signature:  sig.String(),
		Scope:      ScopeModule,
		Visibility: visibility,
	}

	entities = append(entities, entity)
	return entities
}

func (e *goEntityExtractor) getReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return "*" + ident.Name
		}
		return "*unknown"
	default:
		return "unknown"
	}
}

func (e *goEntityExtractor) formatFuncParams(ft *ast.FuncType) string {
	var sb strings.Builder
	sb.WriteString("(")

	if ft.Params != nil {
		var params []string
		for _, param := range ft.Params.List {
			paramType := e.formatType(param.Type)
			if len(param.Names) > 0 {
				for _, name := range param.Names {
					params = append(params, name.Name+" "+paramType)
				}
			} else {
				params = append(params, paramType)
			}
		}
		sb.WriteString(strings.Join(params, ", "))
	}
	sb.WriteString(")")

	if ft.Results != nil && len(ft.Results.List) > 0 {
		sb.WriteString(" ")
		if len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) == 0 {
			sb.WriteString(e.formatType(ft.Results.List[0].Type))
		} else {
			sb.WriteString("(")
			var results []string
			for _, result := range ft.Results.List {
				resultType := e.formatType(result.Type)
				if len(result.Names) > 0 {
					for _, name := range result.Names {
						results = append(results, name.Name+" "+resultType)
					}
				} else {
					results = append(results, resultType)
				}
			}
			sb.WriteString(strings.Join(results, ", "))
			sb.WriteString(")")
		}
	}

	return sb.String()
}

func (e *goEntityExtractor) formatType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + e.formatType(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + e.formatType(t.Elt)
		}
		return "[...]" + e.formatType(t.Elt)
	case *ast.MapType:
		return "map[" + e.formatType(t.Key) + "]" + e.formatType(t.Value)
	case *ast.SelectorExpr:
		return e.formatType(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return "unknown"
	}
}

// =============================================================================
// TypeScript Entity Extractor
// =============================================================================

type typeScriptEntityExtractor struct {
	functionPattern  *regexp.Regexp
	classPattern     *regexp.Regexp
	interfacePattern *regexp.Regexp
	typeAliasPattern *regexp.Regexp
}

func newTypeScriptEntityExtractor() *typeScriptEntityExtractor {
	return &typeScriptEntityExtractor{
		functionPattern:  regexp.MustCompile(`(?m)^[\t ]*(export\s+)?(async\s+)?function\s+(\w+)\s*(<[^>]*>)?\s*\(([^)]*)\)(\s*:\s*[^{;]+)?`),
		classPattern:     regexp.MustCompile(`(?m)^[\t ]*(export\s+)?(abstract\s+)?class\s+(\w+)(\s+extends\s+\w+)?(\s+implements\s+[\w,\s]+)?\s*\{`),
		interfacePattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?interface\s+(\w+)(<[^>]*>)?(\s+extends\s+[\w,\s<>]+)?\s*\{`),
		typeAliasPattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?type\s+(\w+)(<[^>]*>)?\s*=`),
	}
}

func (e *typeScriptEntityExtractor) ExtractEntities(filePath string, content []byte) ([]ExtractedEntity, error) {
	if len(content) == 0 {
		return []ExtractedEntity{}, nil
	}

	source := string(content)
	lines := strings.Split(source, "\n")
	var entities []ExtractedEntity

	// Extract functions
	entities = append(entities, e.extractFunctions(filePath, source, lines)...)

	// Extract classes
	entities = append(entities, e.extractClasses(filePath, source, lines)...)

	// Extract interfaces
	entities = append(entities, e.extractInterfaces(filePath, source, lines)...)

	// Extract type aliases
	entities = append(entities, e.extractTypeAliases(filePath, source, lines)...)

	return entities, nil
}

func (e *typeScriptEntityExtractor) extractFunctions(filePath, source string, lines []string) []ExtractedEntity {
	var entities []ExtractedEntity
	matches := e.functionPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 8 {
			startPos := match[0]
			startLine := strings.Count(source[:startPos], "\n") + 1
			endLine := e.findBlockEnd(lines, startLine)

			name := source[match[6]:match[7]]

			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			if match[4] != -1 && match[5] != -1 {
				sig.WriteString("async ")
			}
			sig.WriteString("function ")
			sig.WriteString(name)
			sig.WriteString("()")

			entity := ExtractedEntity{
				Name:       name,
				Kind:       EntityKindFunction,
				FilePath:   filePath,
				StartLine:  startLine,
				EndLine:    endLine,
				Signature:  sig.String(),
				Scope:      ScopeModule,
				Visibility: VisibilityPublic,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

func (e *typeScriptEntityExtractor) extractClasses(filePath, source string, lines []string) []ExtractedEntity {
	var entities []ExtractedEntity
	matches := e.classPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 8 {
			startPos := match[0]
			startLine := strings.Count(source[:startPos], "\n") + 1
			endLine := e.findBlockEnd(lines, startLine)

			name := source[match[6]:match[7]]

			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			if match[4] != -1 && match[5] != -1 {
				sig.WriteString("abstract ")
			}
			sig.WriteString("class ")
			sig.WriteString(name)

			entity := ExtractedEntity{
				Name:       name,
				Kind:       EntityKindType,
				FilePath:   filePath,
				StartLine:  startLine,
				EndLine:    endLine,
				Signature:  sig.String(),
				Scope:      ScopeModule,
				Visibility: VisibilityPublic,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

func (e *typeScriptEntityExtractor) extractInterfaces(filePath, source string, lines []string) []ExtractedEntity {
	var entities []ExtractedEntity
	matches := e.interfacePattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := strings.Count(source[:startPos], "\n") + 1
			endLine := e.findBlockEnd(lines, startLine)

			name := source[match[4]:match[5]]

			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			sig.WriteString("interface ")
			sig.WriteString(name)

			entity := ExtractedEntity{
				Name:       name,
				Kind:       EntityKindInterface,
				FilePath:   filePath,
				StartLine:  startLine,
				EndLine:    endLine,
				Signature:  sig.String(),
				Scope:      ScopeModule,
				Visibility: VisibilityPublic,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

func (e *typeScriptEntityExtractor) extractTypeAliases(filePath, source string, lines []string) []ExtractedEntity {
	var entities []ExtractedEntity
	matches := e.typeAliasPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := strings.Count(source[:startPos], "\n") + 1
			endLine := startLine // Type aliases typically end on same line

			name := source[match[4]:match[5]]

			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			sig.WriteString("type ")
			sig.WriteString(name)
			sig.WriteString(" = ...")

			entity := ExtractedEntity{
				Name:       name,
				Kind:       EntityKindType,
				FilePath:   filePath,
				StartLine:  startLine,
				EndLine:    endLine,
				Signature:  sig.String(),
				Scope:      ScopeModule,
				Visibility: VisibilityPublic,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

func (e *typeScriptEntityExtractor) findBlockEnd(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}

	braceCount := 0
	started := false

	for i := startLine - 1; i < len(lines); i++ {
		line := lines[i]
		for _, ch := range line {
			if ch == '{' {
				braceCount++
				started = true
			} else if ch == '}' {
				braceCount--
				if started && braceCount == 0 {
					return i + 1
				}
			}
		}
	}

	return startLine
}

// =============================================================================
// Python Entity Extractor
// =============================================================================

type pythonEntityExtractor struct {
	functionPattern *regexp.Regexp
	asyncFuncPattern *regexp.Regexp
	classPattern    *regexp.Regexp
}

func newPythonEntityExtractor() *pythonEntityExtractor {
	return &pythonEntityExtractor{
		functionPattern:  regexp.MustCompile(`(?m)^([\t ]*)def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),
		asyncFuncPattern: regexp.MustCompile(`(?m)^([\t ]*)async\s+def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),
		classPattern:     regexp.MustCompile(`(?m)^([\t ]*)class\s+(\w+)(\s*\([^)]*\))?\s*:`),
	}
}

func (e *pythonEntityExtractor) ExtractEntities(filePath string, content []byte) ([]ExtractedEntity, error) {
	if len(content) == 0 {
		return []ExtractedEntity{}, nil
	}

	source := string(content)
	lines := strings.Split(source, "\n")
	var entities []ExtractedEntity

	// Extract classes first
	classEntities := e.extractClasses(filePath, source, lines)
	entities = append(entities, classEntities...)

	// Build class range map
	classRanges := make(map[string]struct {
		startLine int
		endLine   int
		indent    int
	})

	for _, ce := range classEntities {
		indent := e.getIndentLevel(lines[ce.StartLine-1])
		classRanges[ce.Name] = struct {
			startLine int
			endLine   int
			indent    int
		}{
			startLine: ce.StartLine,
			endLine:   ce.EndLine,
			indent:    indent,
		}
	}

	// Extract functions
	entities = append(entities, e.extractFunctions(filePath, source, lines, classRanges, e.functionPattern, false)...)
	entities = append(entities, e.extractFunctions(filePath, source, lines, classRanges, e.asyncFuncPattern, true)...)

	return entities, nil
}

func (e *pythonEntityExtractor) extractClasses(filePath, source string, lines []string) []ExtractedEntity {
	var entities []ExtractedEntity
	matches := e.classPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := strings.Count(source[:startPos], "\n") + 1

			indent := ""
			if match[2] != -1 && match[3] != -1 {
				indent = source[match[2]:match[3]]
			}

			name := source[match[4]:match[5]]
			endLine := e.findPythonBlockEnd(lines, startLine, len(indent))

			var sig strings.Builder
			sig.WriteString("class ")
			sig.WriteString(name)
			if match[6] != -1 && match[7] != -1 {
				sig.WriteString(source[match[6]:match[7]])
			}

			entity := ExtractedEntity{
				Name:       name,
				Kind:       EntityKindType,
				FilePath:   filePath,
				StartLine:  startLine,
				EndLine:    endLine,
				Signature:  sig.String(),
				Scope:      ScopeModule,
				Visibility: VisibilityPublic,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

func (e *pythonEntityExtractor) extractFunctions(filePath, source string, lines []string, classRanges map[string]struct {
	startLine int
	endLine   int
	indent    int
}, pattern *regexp.Regexp, isAsync bool) []ExtractedEntity {
	var entities []ExtractedEntity
	matches := pattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := strings.Count(source[:startPos], "\n") + 1

			indent := ""
			if match[2] != -1 && match[3] != -1 {
				indent = source[match[2]:match[3]]
			}
			indentLevel := len(indent)

			name := source[match[4]:match[5]]
			endLine := e.findPythonBlockEnd(lines, startLine, indentLevel)

			var sig strings.Builder
			if isAsync {
				sig.WriteString("async ")
			}
			sig.WriteString("def ")
			sig.WriteString(name)
			sig.WriteString("(")
			if match[6] != -1 && match[7] != -1 {
				params := strings.TrimSpace(source[match[6]:match[7]])
				sig.WriteString(params)
			}
			sig.WriteString(")")
			if match[8] != -1 && match[9] != -1 {
				sig.WriteString(source[match[8]:match[9]])
			}

			// Determine if method or function
			var kind EntityKind
			visibility := e.determineVisibility(name)

			isMethod := false
			for _, classInfo := range classRanges {
				if startLine > classInfo.startLine && startLine <= classInfo.endLine && indentLevel > classInfo.indent {
					isMethod = true
					break
				}
			}

			if isMethod {
				kind = EntityKindMethod
			} else {
				kind = EntityKindFunction
			}

			entity := ExtractedEntity{
				Name:       name,
				Kind:       kind,
				FilePath:   filePath,
				StartLine:  startLine,
				EndLine:    endLine,
				Signature:  sig.String(),
				Scope:      ScopeModule,
				Visibility: visibility,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

func (e *pythonEntityExtractor) determineVisibility(name string) EntityVisibility {
	if strings.HasPrefix(name, "__") && !strings.HasSuffix(name, "__") {
		return VisibilityPrivate
	}
	if strings.HasPrefix(name, "_") {
		return VisibilityInternal
	}
	return VisibilityPublic
}

func (e *pythonEntityExtractor) getIndentLevel(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}

func (e *pythonEntityExtractor) findPythonBlockEnd(lines []string, startLine int, baseIndent int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}

	lastNonEmptyLine := startLine
	inBlock := false

	for i := startLine; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		currentIndent := e.getIndentLevel(line)

		if !inBlock {
			if currentIndent > baseIndent {
				inBlock = true
				lastNonEmptyLine = i + 1
			}
			continue
		}

		if currentIndent > baseIndent {
			lastNonEmptyLine = i + 1
		} else {
			break
		}
	}

	if !inBlock {
		return startLine
	}

	return lastNonEmptyLine
}

// =============================================================================
// Built-in Relation Extractors
// =============================================================================

// importRelationExtractor extracts import relations from source files.
type importRelationExtractor struct {
	goImportPattern *regexp.Regexp
	tsImportPattern *regexp.Regexp
	pyImportPattern *regexp.Regexp
}

func newImportRelationExtractor() *importRelationExtractor {
	return &importRelationExtractor{
		goImportPattern: regexp.MustCompile(`(?m)^[\t ]*import\s+(?:(\w+)\s+)?["']([^"']+)["']`),
		tsImportPattern: regexp.MustCompile(`(?m)^[\t ]*import\s+.*from\s*["']([^"']+)["']`),
		pyImportPattern: regexp.MustCompile(`(?m)^[\t ]*(?:import\s+([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)*)|from\s+([a-zA-Z_\.][a-zA-Z0-9_\.]*)\s+import)`),
	}
}

func (e *importRelationExtractor) ExtractRelations(entities []ExtractedEntity, content map[string][]byte) ([]ExtractedRelation, error) {
	var relations []ExtractedRelation

	for filePath, fileContent := range content {
		fileRelations := e.extractImportsFromFile(filePath, fileContent)
		relations = append(relations, fileRelations...)
	}

	return relations, nil
}

func (e *importRelationExtractor) extractImportsFromFile(filePath string, content []byte) []ExtractedRelation {
	var relations []ExtractedRelation

	ext := strings.ToLower(filepath.Ext(filePath))
	source := string(content)
	lines := strings.Split(source, "\n")

	var pattern *regexp.Regexp
	switch ext {
	case ".go":
		pattern = e.goImportPattern
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		pattern = e.tsImportPattern
	case ".py", ".pyw":
		pattern = e.pyImportPattern
	default:
		return relations
	}

	matches := pattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range matches {
		if len(match) >= 4 {
			line := strings.Count(source[:match[0]], "\n") + 1

			// Extract import path from the match
			var importPath string
			for i := 2; i < len(match); i += 2 {
				if match[i] != -1 && match[i+1] != -1 {
					importPath = source[match[i]:match[i+1]]
					break
				}
			}

			if importPath == "" {
				continue
			}

			// Create source entity (the file doing the import)
			sourceEntity := &ExtractedEntity{
				Name:     filepath.Base(filePath),
				Kind:     EntityKindFile,
				FilePath: filePath,
			}

			// Create target entity (the imported module)
			targetEntity := &ExtractedEntity{
				Name:     filepath.Base(importPath),
				Kind:     EntityKindImport,
				FilePath: importPath,
			}

			// Extract evidence snippet
			snippet := ""
			if line > 0 && line <= len(lines) {
				snippet = strings.TrimSpace(lines[line-1])
			}

			evidence := []EvidenceSpan{
				{
					FilePath:  filePath,
					StartLine: line,
					EndLine:   line,
					Snippet:   snippet,
				},
			}

			relations = append(relations, ExtractedRelation{
				SourceEntity: sourceEntity,
				TargetEntity: targetEntity,
				RelationType: RelImports,
				Evidence:     evidence,
				Confidence:   0.95,
				ExtractedAt:  time.Now(),
			})
		}
	}

	return relations
}

// callRelationExtractor extracts call relations from source files.
type callRelationExtractor struct {
	callPattern *regexp.Regexp
}

func newCallRelationExtractor() *callRelationExtractor {
	return &callRelationExtractor{
		callPattern: regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`),
	}
}

func (e *callRelationExtractor) ExtractRelations(entities []ExtractedEntity, content map[string][]byte) ([]ExtractedRelation, error) {
	var relations []ExtractedRelation

	// Build entity lookup by name
	entityByName := make(map[string][]ExtractedEntity)
	funcEntities := make(map[string][]ExtractedEntity) // file -> functions in file

	for _, entity := range entities {
		entityByName[entity.Name] = append(entityByName[entity.Name], entity)

		if entity.Kind == EntityKindFunction || entity.Kind == EntityKindMethod {
			funcEntities[entity.FilePath] = append(funcEntities[entity.FilePath], entity)
		}
	}

	// Keywords to exclude
	keywords := map[string]bool{
		"if": true, "else": true, "for": true, "while": true, "switch": true,
		"case": true, "return": true, "class": true, "function": true, "def": true,
		"import": true, "from": true, "export": true, "const": true, "let": true,
		"var": true, "type": true, "interface": true, "struct": true, "package": true,
		"async": true, "await": true, "try": true, "catch": true, "finally": true,
		"throw": true, "new": true, "typeof": true, "print": true, "println": true,
	}

	for filePath, fileContent := range content {
		funcsInFile := funcEntities[filePath]
		source := string(fileContent)
		lines := strings.Split(source, "\n")

		for _, caller := range funcsInFile {
			startIdx := caller.StartLine - 1
			endIdx := caller.EndLine
			if startIdx < 0 {
				startIdx = 0
			}
			if endIdx > len(lines) {
				endIdx = len(lines)
			}

			for lineIdx := startIdx; lineIdx < endIdx; lineIdx++ {
				line := lines[lineIdx]
				matches := e.callPattern.FindAllStringSubmatch(line, -1)

				for _, match := range matches {
					if len(match) >= 2 {
						calleeName := match[1]

						// Skip keywords
						if keywords[calleeName] {
							continue
						}

						// Find matching callee entities
						callees, found := entityByName[calleeName]
						if !found {
							continue
						}

						for _, callee := range callees {
							if callee.Kind != EntityKindFunction && callee.Kind != EntityKindMethod {
								continue
							}

							// Skip self-reference
							if callee.FilePath == caller.FilePath &&
								callee.StartLine == caller.StartLine &&
								callee.Name == caller.Name {
								continue
							}

							evidence := []EvidenceSpan{
								{
									FilePath:  filePath,
									StartLine: lineIdx + 1,
									EndLine:   lineIdx + 1,
									Snippet:   strings.TrimSpace(line),
								},
							}

							callerCopy := caller
							calleeCopy := callee

							relations = append(relations, ExtractedRelation{
								SourceEntity: &callerCopy,
								TargetEntity: &calleeCopy,
								RelationType: RelCalls,
								Evidence:     evidence,
								Confidence:   0.8,
								ExtractedAt:  time.Now(),
							})
						}
					}
				}
			}
		}
	}

	return relations, nil
}
