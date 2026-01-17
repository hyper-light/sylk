package parsers

import (
	"encoding/json"
	"regexp"
	"sync"
	"time"
)

// ParseTemplate represents an LLM-learned parsing template
type ParseTemplate struct {
	ToolPattern  string           `json:"tool_pattern"`
	Fields       []TemplateField  `json:"fields"`
	Example      *TemplateExample `json:"example,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	LastUsed     time.Time        `json:"last_used"`
	SuccessCount int              `json:"success_count"`
	FailureCount int              `json:"failure_count"`
}

// TemplateField defines an extraction rule for a field
type TemplateField struct {
	Name     string `json:"name"`
	Pattern  string `json:"pattern"`
	Required bool   `json:"required"`
	compiled *regexp.Regexp
}

// TemplateExample provides validation data
type TemplateExample struct {
	Input    string         `json:"input"`
	Expected map[string]any `json:"expected"`
}

// TemplateResult holds parsed field values
type TemplateResult struct {
	Fields   map[string][]string
	Warnings []string
}

// TemplateStore defines persistence interface
type TemplateStore interface {
	Load(toolPattern string) (*ParseTemplate, error)
	Save(template *ParseTemplate) error
	List() ([]*ParseTemplate, error)
	Delete(toolPattern string) error
}

// TemplateCache provides fast template lookup with persistence
type TemplateCache struct {
	mu       sync.RWMutex
	cache    map[string]*ParseTemplate
	store    TemplateStore
	maxSize  int
	compiled map[string]*regexp.Regexp
}

// TemplateCacheConfig configures the template cache
type TemplateCacheConfig struct {
	MaxSize int
	Store   TemplateStore // Optional persistence
}

// NewTemplateCache creates a new template cache
func NewTemplateCache(cfg TemplateCacheConfig) *TemplateCache {
	maxSize := cfg.MaxSize
	if maxSize == 0 {
		maxSize = 100
	}

	tc := &TemplateCache{
		cache:    make(map[string]*ParseTemplate),
		store:    cfg.Store,
		maxSize:  maxSize,
		compiled: make(map[string]*regexp.Regexp),
	}

	tc.loadFromStore()
	return tc
}

func (tc *TemplateCache) loadFromStore() {
	if tc.store == nil {
		return
	}

	templates, err := tc.store.List()
	if err != nil {
		return
	}

	for _, t := range templates {
		tc.cache[t.ToolPattern] = t
	}
}

// Get retrieves a template for a tool pattern
func (tc *TemplateCache) Get(toolPattern string) *ParseTemplate {
	tc.mu.RLock()
	t, ok := tc.cache[toolPattern]
	tc.mu.RUnlock()

	if !ok {
		return tc.loadFromStoreIfMissing(toolPattern)
	}

	tc.updateLastUsed(t)
	return t
}

func (tc *TemplateCache) loadFromStoreIfMissing(toolPattern string) *ParseTemplate {
	if tc.store == nil {
		return nil
	}

	t, err := tc.store.Load(toolPattern)
	if err != nil || t == nil {
		return nil
	}

	tc.mu.Lock()
	tc.cache[toolPattern] = t
	tc.mu.Unlock()

	return t
}

func (tc *TemplateCache) updateLastUsed(t *ParseTemplate) {
	tc.mu.Lock()
	t.LastUsed = time.Now()
	tc.mu.Unlock()
}

// Store adds or updates a template
func (tc *TemplateCache) Store(template *ParseTemplate) error {
	if err := tc.validateTemplate(template); err != nil {
		return err
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.enforceMaxSize()
	tc.cache[template.ToolPattern] = template
	tc.compilePatterns(template)

	return tc.persistTemplate(template)
}

func (tc *TemplateCache) validateTemplate(t *ParseTemplate) error {
	if t.ToolPattern == "" {
		return ErrEmptyToolPattern
	}

	for _, f := range t.Fields {
		if _, err := regexp.Compile(f.Pattern); err != nil {
			return &InvalidPatternError{Field: f.Name, Err: err}
		}
	}

	return tc.validateAgainstExample(t)
}

func (tc *TemplateCache) validateAgainstExample(t *ParseTemplate) error {
	if t.Example == nil {
		return nil
	}

	result := tc.applyTemplate(t, []byte(t.Example.Input), nil)
	return tc.checkExpectedFields(t, result)
}

func (tc *TemplateCache) checkExpectedFields(t *ParseTemplate, result *TemplateResult) error {
	for key, expected := range t.Example.Expected {
		actual, ok := result.Fields[key]
		if !ok || len(actual) == 0 {
			return &ValidationError{Field: key, Expected: expected}
		}
	}
	return nil
}

func (tc *TemplateCache) enforceMaxSize() {
	if len(tc.cache) < tc.maxSize {
		return
	}

	oldest := tc.findOldest()
	if oldest != "" {
		delete(tc.cache, oldest)
		delete(tc.compiled, oldest)
	}
}

func (tc *TemplateCache) findOldest() string {
	var oldest string
	var oldestTime time.Time

	for pattern, t := range tc.cache {
		if oldest == "" || t.LastUsed.Before(oldestTime) {
			oldest = pattern
			oldestTime = t.LastUsed
		}
	}

	return oldest
}

func (tc *TemplateCache) compilePatterns(t *ParseTemplate) {
	for i := range t.Fields {
		re, err := regexp.Compile(t.Fields[i].Pattern)
		if err == nil {
			t.Fields[i].compiled = re
		}
	}
}

func (tc *TemplateCache) persistTemplate(t *ParseTemplate) error {
	if tc.store == nil {
		return nil
	}
	return tc.store.Save(t)
}

// Apply applies a template to parse output
func (tc *TemplateCache) Apply(template *ParseTemplate, stdout, stderr []byte) (*TemplateResult, error) {
	if template == nil {
		return nil, ErrNilTemplate
	}

	result := tc.applyTemplate(template, stdout, stderr)
	tc.recordUsage(template, result)

	return result, nil
}

func (tc *TemplateCache) applyTemplate(t *ParseTemplate, stdout, stderr []byte) *TemplateResult {
	result := &TemplateResult{
		Fields:   make(map[string][]string),
		Warnings: make([]string, 0),
	}

	input := string(stdout)
	if len(stderr) > 0 {
		input += "\n" + string(stderr)
	}

	for _, field := range t.Fields {
		tc.extractField(input, field, result)
	}

	return result
}

func (tc *TemplateCache) extractField(input string, field TemplateField, result *TemplateResult) {
	re := tc.getCompiledPattern(field)
	if re == nil {
		tc.addWarning(result, field)
		return
	}

	matches := re.FindAllStringSubmatch(input, -1)
	tc.collectMatches(field.Name, matches, result)
}

func (tc *TemplateCache) getCompiledPattern(field TemplateField) *regexp.Regexp {
	if field.compiled != nil {
		return field.compiled
	}

	re, err := regexp.Compile(field.Pattern)
	if err != nil {
		return nil
	}
	return re
}

func (tc *TemplateCache) addWarning(result *TemplateResult, field TemplateField) {
	if field.Required {
		result.Warnings = append(result.Warnings, "failed to compile pattern for "+field.Name)
	}
}

func (tc *TemplateCache) collectMatches(name string, matches [][]string, result *TemplateResult) {
	for _, match := range matches {
		if len(match) > 1 {
			result.Fields[name] = append(result.Fields[name], match[1])
		} else if len(match) == 1 {
			result.Fields[name] = append(result.Fields[name], match[0])
		}
	}
}

func (tc *TemplateCache) recordUsage(t *ParseTemplate, result *TemplateResult) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if len(result.Warnings) == 0 {
		t.SuccessCount++
	} else {
		t.FailureCount++
	}
}

// Delete removes a template
func (tc *TemplateCache) Delete(toolPattern string) error {
	tc.mu.Lock()
	delete(tc.cache, toolPattern)
	delete(tc.compiled, toolPattern)
	tc.mu.Unlock()

	if tc.store == nil {
		return nil
	}
	return tc.store.Delete(toolPattern)
}

// List returns all cached templates
func (tc *TemplateCache) List() []*ParseTemplate {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	templates := make([]*ParseTemplate, 0, len(tc.cache))
	for _, t := range tc.cache {
		templates = append(templates, t)
	}
	return templates
}

// Stats returns cache statistics
func (tc *TemplateCache) Stats() TemplateCacheStats {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	var total, success, failure int
	for _, t := range tc.cache {
		total++
		success += t.SuccessCount
		failure += t.FailureCount
	}

	return TemplateCacheStats{
		TemplateCount: total,
		TotalSuccess:  success,
		TotalFailure:  failure,
		CacheSize:     len(tc.cache),
		MaxSize:       tc.maxSize,
	}
}

// TemplateCacheStats holds cache statistics
type TemplateCacheStats struct {
	TemplateCount int
	TotalSuccess  int
	TotalFailure  int
	CacheSize     int
	MaxSize       int
}

// Export exports all templates as JSON
func (tc *TemplateCache) Export() ([]byte, error) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	templates := make([]*ParseTemplate, 0, len(tc.cache))
	for _, t := range tc.cache {
		templates = append(templates, t)
	}

	return json.Marshal(templates)
}

// Import imports templates from JSON
func (tc *TemplateCache) Import(data []byte) error {
	var templates []*ParseTemplate
	if err := json.Unmarshal(data, &templates); err != nil {
		return err
	}

	for _, t := range templates {
		if err := tc.Store(t); err != nil {
			return err
		}
	}

	return nil
}
