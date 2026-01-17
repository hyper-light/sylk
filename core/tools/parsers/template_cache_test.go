package parsers

import (
	"sync"
	"testing"
	"time"
)

func TestNewTemplateCache(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})
	if tc == nil {
		t.Fatal("expected non-nil cache")
	}
	if tc.maxSize != 100 {
		t.Errorf("expected default maxSize 100, got %d", tc.maxSize)
	}
}

func TestTemplateCache_StoreAndGet(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "go test *",
		Fields: []TemplateField{
			{Name: "status", Pattern: `(PASS|FAIL)`, Required: true},
		},
		CreatedAt: time.Now(),
	}

	if err := tc.Store(template); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	got := tc.Get("go test *")
	if got == nil {
		t.Fatal("expected template, got nil")
	}
	if got.ToolPattern != "go test *" {
		t.Errorf("expected 'go test *', got %q", got.ToolPattern)
	}
}

func TestTemplateCache_GetMissing(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	got := tc.Get("nonexistent")
	if got != nil {
		t.Error("expected nil for missing template")
	}
}

func TestTemplateCache_StoreEmptyPattern(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	err := tc.Store(&ParseTemplate{})
	if err != ErrEmptyToolPattern {
		t.Errorf("expected ErrEmptyToolPattern, got %v", err)
	}
}

func TestTemplateCache_StoreInvalidPattern(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "test",
		Fields: []TemplateField{
			{Name: "bad", Pattern: `[invalid`, Required: true},
		},
	}

	err := tc.Store(template)
	if err == nil {
		t.Error("expected error for invalid pattern")
	}
	if _, ok := err.(*InvalidPatternError); !ok {
		t.Errorf("expected InvalidPatternError, got %T", err)
	}
}

func TestTemplateCache_Apply(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "go test",
		Fields: []TemplateField{
			{Name: "status", Pattern: `(PASS|FAIL)`, Required: true},
			{Name: "package", Pattern: `ok\s+(\S+)`, Required: false},
		},
	}

	tc.Store(template)

	output := []byte("ok  mypackage  0.005s\nPASS")
	result, err := tc.Apply(template, output, nil)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if len(result.Fields["status"]) != 1 || result.Fields["status"][0] != "PASS" {
		t.Errorf("expected status PASS, got %v", result.Fields["status"])
	}
	if len(result.Fields["package"]) != 1 || result.Fields["package"][0] != "mypackage" {
		t.Errorf("expected package mypackage, got %v", result.Fields["package"])
	}
}

func TestTemplateCache_ApplyNilTemplate(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	_, err := tc.Apply(nil, []byte("test"), nil)
	if err != ErrNilTemplate {
		t.Errorf("expected ErrNilTemplate, got %v", err)
	}
}

func TestTemplateCache_Delete(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "test",
		Fields:      []TemplateField{},
	}
	tc.Store(template)

	if err := tc.Delete("test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if got := tc.Get("test"); got != nil {
		t.Error("expected nil after delete")
	}
}

func TestTemplateCache_List(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	templates := []*ParseTemplate{
		{ToolPattern: "a", Fields: []TemplateField{}},
		{ToolPattern: "b", Fields: []TemplateField{}},
		{ToolPattern: "c", Fields: []TemplateField{}},
	}

	for _, tpl := range templates {
		tc.Store(tpl)
	}

	list := tc.List()
	if len(list) != 3 {
		t.Errorf("expected 3 templates, got %d", len(list))
	}
}

func TestTemplateCache_MaxSize(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{MaxSize: 2})

	tc.Store(&ParseTemplate{ToolPattern: "a", Fields: []TemplateField{}, LastUsed: time.Now().Add(-2 * time.Hour)})
	tc.Store(&ParseTemplate{ToolPattern: "b", Fields: []TemplateField{}, LastUsed: time.Now().Add(-1 * time.Hour)})
	tc.Store(&ParseTemplate{ToolPattern: "c", Fields: []TemplateField{}, LastUsed: time.Now()})

	if len(tc.cache) > 2 {
		t.Errorf("expected max 2 templates, got %d", len(tc.cache))
	}

	// oldest should be evicted
	if tc.Get("a") != nil {
		t.Error("expected 'a' to be evicted")
	}
}

func TestTemplateCache_Stats(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{MaxSize: 50})

	tc.Store(&ParseTemplate{
		ToolPattern:  "test",
		Fields:       []TemplateField{{Name: "x", Pattern: `\d+`}},
		SuccessCount: 5,
		FailureCount: 1,
	})

	stats := tc.Stats()
	if stats.TemplateCount != 1 {
		t.Errorf("expected 1 template, got %d", stats.TemplateCount)
	}
	if stats.TotalSuccess != 5 {
		t.Errorf("expected 5 success, got %d", stats.TotalSuccess)
	}
	if stats.MaxSize != 50 {
		t.Errorf("expected maxSize 50, got %d", stats.MaxSize)
	}
}

func TestTemplateCache_ExportImport(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	tc.Store(&ParseTemplate{
		ToolPattern: "export-test",
		Fields:      []TemplateField{{Name: "num", Pattern: `\d+`}},
	})

	data, err := tc.Export()
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	tc2 := NewTemplateCache(TemplateCacheConfig{})
	if err := tc2.Import(data); err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	got := tc2.Get("export-test")
	if got == nil {
		t.Error("expected imported template")
	}
}

func TestTemplateCache_ValidationWithExample(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "test",
		Fields: []TemplateField{
			{Name: "count", Pattern: `count:\s*(\d+)`, Required: true},
		},
		Example: &TemplateExample{
			Input:    "count: 42",
			Expected: map[string]any{"count": "42"},
		},
	}

	if err := tc.Store(template); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
}

func TestTemplateCache_ValidationFailsWithBadExample(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "test",
		Fields: []TemplateField{
			{Name: "count", Pattern: `count:\s*(\d+)`, Required: true},
		},
		Example: &TemplateExample{
			Input:    "no match here",
			Expected: map[string]any{"count": "42"},
		},
	}

	err := tc.Store(template)
	if err == nil {
		t.Error("expected validation error")
	}
}

func TestTemplateCache_Concurrent(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{MaxSize: 100})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			pattern := "pattern-" + string(rune('A'+n%26))
			tc.Store(&ParseTemplate{
				ToolPattern: pattern,
				Fields:      []TemplateField{},
			})
			tc.Get(pattern)
			tc.List()
			tc.Stats()
		}(i)
	}
	wg.Wait()
}

func TestTemplateCache_RecordsUsage(t *testing.T) {
	tc := NewTemplateCache(TemplateCacheConfig{})

	template := &ParseTemplate{
		ToolPattern: "test",
		Fields:      []TemplateField{{Name: "num", Pattern: `(\d+)`}},
	}
	tc.Store(template)

	tc.Apply(template, []byte("123"), nil)
	tc.Apply(template, []byte("456"), nil)

	if template.SuccessCount != 2 {
		t.Errorf("expected 2 success, got %d", template.SuccessCount)
	}
}

type mockStore struct {
	templates map[string]*ParseTemplate
	loadErr   error
	saveErr   error
}

func (m *mockStore) Load(pattern string) (*ParseTemplate, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.templates[pattern], nil
}

func (m *mockStore) Save(t *ParseTemplate) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.templates[t.ToolPattern] = t
	return nil
}

func (m *mockStore) List() ([]*ParseTemplate, error) {
	result := make([]*ParseTemplate, 0, len(m.templates))
	for _, t := range m.templates {
		result = append(result, t)
	}
	return result, nil
}

func (m *mockStore) Delete(pattern string) error {
	delete(m.templates, pattern)
	return nil
}

func TestTemplateCache_WithStore(t *testing.T) {
	store := &mockStore{
		templates: map[string]*ParseTemplate{
			"preloaded": {ToolPattern: "preloaded", Fields: []TemplateField{}},
		},
	}

	tc := NewTemplateCache(TemplateCacheConfig{Store: store})

	// should load from store
	got := tc.Get("preloaded")
	if got == nil {
		t.Error("expected preloaded template")
	}

	// should save to store
	tc.Store(&ParseTemplate{ToolPattern: "new", Fields: []TemplateField{}})
	if _, ok := store.templates["new"]; !ok {
		t.Error("expected template to be saved to store")
	}
}
