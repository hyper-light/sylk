package format

import (
	"sync"
	"testing"
)

func TestNewFormatterRegistry(t *testing.T) {
	t.Run("creates empty registry", func(t *testing.T) {
		registry := NewFormatterRegistry()

		if registry == nil {
			t.Fatal("NewFormatterRegistry returned nil")
		}

		if registry.formatters == nil {
			t.Error("formatters map is nil")
		}

		if registry.byExtension == nil {
			t.Error("byExtension map is nil")
		}

		if len(registry.formatters) != 0 {
			t.Errorf("expected empty formatters map, got %d entries", len(registry.formatters))
		}

		if len(registry.byExtension) != 0 {
			t.Errorf("expected empty byExtension map, got %d entries", len(registry.byExtension))
		}
	})

	t.Run("List returns empty slice for new registry", func(t *testing.T) {
		registry := NewFormatterRegistry()
		list := registry.List()

		if list == nil {
			t.Error("List returned nil for empty registry")
		}

		if len(list) != 0 {
			t.Errorf("expected empty list, got %d items", len(list))
		}
	})
}

func TestFormatterRegistry_Register(t *testing.T) {
	tests := []struct {
		name        string
		definitions []*FormatterDefinition
		wantErr     bool
		errID       FormatterID
	}{
		{
			name: "register single formatter",
			definitions: []*FormatterDefinition{
				{
					ID:         "gofmt",
					Name:       "Go Format",
					Command:    "gofmt",
					Extensions: []string{".go"},
				},
			},
			wantErr: false,
		},
		{
			name: "register multiple formatters",
			definitions: []*FormatterDefinition{
				{
					ID:         "gofmt",
					Name:       "Go Format",
					Command:    "gofmt",
					Extensions: []string{".go"},
				},
				{
					ID:         "prettier",
					Name:       "Prettier",
					Command:    "prettier",
					Extensions: []string{".js", ".ts"},
				},
			},
			wantErr: false,
		},
		{
			name: "register duplicate returns error",
			definitions: []*FormatterDefinition{
				{
					ID:         "gofmt",
					Name:       "Go Format",
					Command:    "gofmt",
					Extensions: []string{".go"},
				},
				{
					ID:         "gofmt",
					Name:       "Go Format Duplicate",
					Command:    "gofmt2",
					Extensions: []string{".go"},
				},
			},
			wantErr: true,
			errID:   "gofmt",
		},
		{
			name: "register formatter with empty ID",
			definitions: []*FormatterDefinition{
				{
					ID:         "",
					Name:       "Empty ID Formatter",
					Command:    "empty",
					Extensions: []string{".txt"},
				},
			},
			wantErr: false,
		},
		{
			name: "register formatter with no extensions",
			definitions: []*FormatterDefinition{
				{
					ID:         "noext",
					Name:       "No Extensions",
					Command:    "noext",
					Extensions: nil,
				},
			},
			wantErr: false,
		},
		{
			name: "register formatter with empty extensions slice",
			definitions: []*FormatterDefinition{
				{
					ID:         "emptyext",
					Name:       "Empty Extensions",
					Command:    "emptyext",
					Extensions: []string{},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewFormatterRegistry()
			var lastErr error

			for _, def := range tt.definitions {
				err := registry.Register(def)
				if err != nil {
					lastErr = err
				}
			}

			if tt.wantErr {
				if lastErr == nil {
					t.Error("expected error but got none")
					return
				}

				dupErr, ok := lastErr.(*DuplicateFormatterError)
				if !ok {
					t.Errorf("expected DuplicateFormatterError, got %T", lastErr)
					return
				}

				if dupErr.ID != tt.errID {
					t.Errorf("expected error ID %q, got %q", tt.errID, dupErr.ID)
				}
			} else {
				if lastErr != nil {
					t.Errorf("unexpected error: %v", lastErr)
				}
			}
		})
	}
}

func TestFormatterRegistry_Get(t *testing.T) {
	tests := []struct {
		name       string
		registered []*FormatterDefinition
		lookupID   FormatterID
		wantFound  bool
		wantID     FormatterID
	}{
		{
			name: "get existing formatter",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Name: "Go Format", Command: "gofmt"},
			},
			lookupID:  "gofmt",
			wantFound: true,
			wantID:    "gofmt",
		},
		{
			name: "get non-existing formatter",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Name: "Go Format", Command: "gofmt"},
			},
			lookupID:  "prettier",
			wantFound: false,
		},
		{
			name:       "get from empty registry",
			registered: nil,
			lookupID:   "gofmt",
			wantFound:  false,
		},
		{
			name: "get with empty string ID",
			registered: []*FormatterDefinition{
				{ID: "", Name: "Empty ID", Command: "empty"},
			},
			lookupID:  "",
			wantFound: true,
			wantID:    "",
		},
		{
			name: "lookup empty string when not registered",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Name: "Go Format", Command: "gofmt"},
			},
			lookupID:  "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewFormatterRegistry()

			for _, def := range tt.registered {
				_ = registry.Register(def)
			}

			got, found := registry.Get(tt.lookupID)

			if found != tt.wantFound {
				t.Errorf("Get() found = %v, want %v", found, tt.wantFound)
			}

			if tt.wantFound {
				if got == nil {
					t.Error("Get() returned nil for found formatter")
					return
				}
				if got.ID != tt.wantID {
					t.Errorf("Get() ID = %v, want %v", got.ID, tt.wantID)
				}
			} else {
				if got != nil {
					t.Errorf("Get() returned non-nil for not found: %+v", got)
				}
			}
		})
	}
}

func TestFormatterRegistry_List(t *testing.T) {
	tests := []struct {
		name       string
		registered []*FormatterDefinition
		wantCount  int
	}{
		{
			name:       "empty registry",
			registered: nil,
			wantCount:  0,
		},
		{
			name: "single formatter",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Name: "Go Format"},
			},
			wantCount: 1,
		},
		{
			name: "multiple formatters",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Name: "Go Format"},
				{ID: "prettier", Name: "Prettier"},
				{ID: "black", Name: "Black"},
			},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewFormatterRegistry()

			for _, def := range tt.registered {
				_ = registry.Register(def)
			}

			list := registry.List()

			if len(list) != tt.wantCount {
				t.Errorf("List() returned %d items, want %d", len(list), tt.wantCount)
			}

			// Verify all registered formatters are in the list
			for _, def := range tt.registered {
				found := false
				for _, item := range list {
					if item.ID == def.ID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("List() missing formatter %q", def.ID)
				}
			}
		})
	}
}

func TestFormatterRegistry_GetByExtension(t *testing.T) {
	tests := []struct {
		name       string
		registered []*FormatterDefinition
		extension  string
		wantIDs    []FormatterID
	}{
		{
			name: "single formatter for extension",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Extensions: []string{".go"}},
			},
			extension: ".go",
			wantIDs:   []FormatterID{"gofmt"},
		},
		{
			name: "multiple formatters for same extension",
			registered: []*FormatterDefinition{
				{ID: "prettier", Extensions: []string{".js", ".ts"}},
				{ID: "eslint", Extensions: []string{".js", ".jsx"}},
			},
			extension: ".js",
			wantIDs:   []FormatterID{"prettier", "eslint"},
		},
		{
			name: "no formatter for extension",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Extensions: []string{".go"}},
			},
			extension: ".rs",
			wantIDs:   nil,
		},
		{
			name:       "empty registry",
			registered: nil,
			extension:  ".go",
			wantIDs:    nil,
		},
		{
			name: "empty extension string",
			registered: []*FormatterDefinition{
				{ID: "gofmt", Extensions: []string{".go"}},
			},
			extension: "",
			wantIDs:   nil,
		},
		{
			name: "formatter registered with empty extension",
			registered: []*FormatterDefinition{
				{ID: "special", Extensions: []string{""}},
			},
			extension: "",
			wantIDs:   []FormatterID{"special"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewFormatterRegistry()

			for _, def := range tt.registered {
				_ = registry.Register(def)
			}

			result := registry.GetByExtension(tt.extension)

			if tt.wantIDs == nil {
				if result != nil {
					t.Errorf("GetByExtension() = %v, want nil", result)
				}
				return
			}

			if len(result) != len(tt.wantIDs) {
				t.Errorf("GetByExtension() returned %d items, want %d", len(result), len(tt.wantIDs))
				return
			}

			for _, wantID := range tt.wantIDs {
				found := false
				for _, def := range result {
					if def.ID == wantID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("GetByExtension() missing formatter %q", wantID)
				}
			}
		})
	}
}

func TestFormatterRegistry_GetByExtension_ReturnsCopy(t *testing.T) {
	registry := NewFormatterRegistry()
	_ = registry.Register(&FormatterDefinition{
		ID:         "gofmt",
		Extensions: []string{".go"},
	})

	result1 := registry.GetByExtension(".go")
	result2 := registry.GetByExtension(".go")

	if len(result1) != 1 || len(result2) != 1 {
		t.Fatal("expected one formatter in each result")
	}

	// Modify result1 and verify result2 is unaffected
	result1[0] = &FormatterDefinition{ID: "modified"}

	if result2[0].ID == "modified" {
		t.Error("GetByExtension did not return a copy; modification affected other results")
	}
}

func TestDuplicateFormatterError_Error(t *testing.T) {
	tests := []struct {
		name    string
		id      FormatterID
		wantMsg string
	}{
		{
			name:    "standard ID",
			id:      "gofmt",
			wantMsg: "formatter already registered: gofmt",
		},
		{
			name:    "empty ID",
			id:      "",
			wantMsg: "formatter already registered: ",
		},
		{
			name:    "ID with spaces",
			id:      "my formatter",
			wantMsg: "formatter already registered: my formatter",
		},
		{
			name:    "ID with special characters",
			id:      "fmt-v2.0",
			wantMsg: "formatter already registered: fmt-v2.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &DuplicateFormatterError{ID: tt.id}
			got := err.Error()

			if got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestFormatterRegistry_ThreadSafety(t *testing.T) {
	registry := NewFormatterRegistry()
	const numGoroutines = 100
	const numOperations = 50

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*numOperations)

	// Spawn goroutines that concurrently register formatters
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				def := &FormatterDefinition{
					ID:         FormatterID("formatter-" + string(rune('A'+goroutineID)) + "-" + string(rune('0'+j%10))),
					Name:       "Test Formatter",
					Command:    "test",
					Extensions: []string{".test"},
				}
				err := registry.Register(def)
				// Duplicates are expected, so we only track unexpected errors
				if err != nil {
					if _, ok := err.(*DuplicateFormatterError); !ok {
						errChan <- err
					}
				}
			}
		}(i)
	}

	// Spawn goroutines that concurrently read from the registry
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Test Get
				registry.Get(FormatterID("formatter-" + string(rune('A'+goroutineID)) + "-" + string(rune('0'+j%10))))

				// Test List
				registry.List()

				// Test GetByExtension
				registry.GetByExtension(".test")
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any unexpected errors
	for err := range errChan {
		t.Errorf("unexpected error during concurrent access: %v", err)
	}
}

func TestFormatterRegistry_ConcurrentRegisterAndGet(t *testing.T) {
	registry := NewFormatterRegistry()

	// Pre-register some formatters
	for i := 0; i < 10; i++ {
		_ = registry.Register(&FormatterDefinition{
			ID:         FormatterID("pre-" + string(rune('0'+i))),
			Extensions: []string{".pre"},
		})
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			select {
			case <-done:
				return
			default:
				_ = registry.Register(&FormatterDefinition{
					ID:         FormatterID("new-" + string(rune('A'+i%26)) + string(rune('0'+i%10))),
					Extensions: []string{".new"},
				})
			}
		}
	}()

	// Reader goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				select {
				case <-done:
					return
				default:
					registry.Get("pre-0")
					registry.List()
					registry.GetByExtension(".pre")
				}
			}
		}()
	}

	wg.Wait()
}

func TestFormatterDefinition_Fields(t *testing.T) {
	enabled := func(root string) bool {
		return root == "/test"
	}

	def := &FormatterDefinition{
		ID:          "test-formatter",
		Name:        "Test Formatter",
		Command:     "/usr/bin/testfmt",
		Args:        []string{"-w", "--check"},
		Extensions:  []string{".test", ".tst"},
		ConfigFiles: []string{".testfmtrc", "testfmt.config"},
		Priority:    100,
		Enabled:     enabled,
	}

	if def.ID != "test-formatter" {
		t.Errorf("ID = %v, want %v", def.ID, "test-formatter")
	}

	if def.Name != "Test Formatter" {
		t.Errorf("Name = %v, want %v", def.Name, "Test Formatter")
	}

	if def.Command != "/usr/bin/testfmt" {
		t.Errorf("Command = %v, want %v", def.Command, "/usr/bin/testfmt")
	}

	if len(def.Args) != 2 || def.Args[0] != "-w" || def.Args[1] != "--check" {
		t.Errorf("Args = %v, want [-w --check]", def.Args)
	}

	if len(def.Extensions) != 2 {
		t.Errorf("Extensions length = %d, want 2", len(def.Extensions))
	}

	if len(def.ConfigFiles) != 2 {
		t.Errorf("ConfigFiles length = %d, want 2", len(def.ConfigFiles))
	}

	if def.Priority != 100 {
		t.Errorf("Priority = %d, want 100", def.Priority)
	}

	if def.Enabled == nil {
		t.Error("Enabled function is nil")
	} else {
		if !def.Enabled("/test") {
			t.Error("Enabled(/test) = false, want true")
		}
		if def.Enabled("/other") {
			t.Error("Enabled(/other) = true, want false")
		}
	}
}

func TestFormatterResult_Fields(t *testing.T) {
	result := &FormatterResult{
		FormatterID: "gofmt",
		FilePath:    "/path/to/file.go",
		Success:     true,
		Changed:     true,
		Error:       nil,
		Duration:    100,
		Stdout:      "formatted",
		Stderr:      "",
	}

	if result.FormatterID != "gofmt" {
		t.Errorf("FormatterID = %v, want gofmt", result.FormatterID)
	}

	if result.FilePath != "/path/to/file.go" {
		t.Errorf("FilePath = %v, want /path/to/file.go", result.FilePath)
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}

	if !result.Changed {
		t.Error("Changed = false, want true")
	}

	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}

	if result.Duration != 100 {
		t.Errorf("Duration = %v, want 100", result.Duration)
	}

	if result.Stdout != "formatted" {
		t.Errorf("Stdout = %v, want formatted", result.Stdout)
	}

	if result.Stderr != "" {
		t.Errorf("Stderr = %v, want empty", result.Stderr)
	}
}

func TestFormatterConfig_Fields(t *testing.T) {
	config := &FormatterConfig{
		Disabled:   true,
		Command:    "/custom/path",
		Args:       []string{"--custom"},
		Extensions: []string{".custom"},
	}

	if !config.Disabled {
		t.Error("Disabled = false, want true")
	}

	if config.Command != "/custom/path" {
		t.Errorf("Command = %v, want /custom/path", config.Command)
	}

	if len(config.Args) != 1 || config.Args[0] != "--custom" {
		t.Errorf("Args = %v, want [--custom]", config.Args)
	}

	if len(config.Extensions) != 1 || config.Extensions[0] != ".custom" {
		t.Errorf("Extensions = %v, want [.custom]", config.Extensions)
	}
}

func TestDetectionResult_Fields(t *testing.T) {
	result := &DetectionResult{
		FormatterID: "prettier",
		Confidence:  0.95,
		Reason:      "Found .prettierrc",
	}

	if result.FormatterID != "prettier" {
		t.Errorf("FormatterID = %v, want prettier", result.FormatterID)
	}

	if result.Confidence != 0.95 {
		t.Errorf("Confidence = %v, want 0.95", result.Confidence)
	}

	if result.Reason != "Found .prettierrc" {
		t.Errorf("Reason = %v, want 'Found .prettierrc'", result.Reason)
	}
}

func TestFormatterID_TypeAlias(t *testing.T) {
	// Test that FormatterID is a string type
	var id FormatterID = "test"

	if string(id) != "test" {
		t.Errorf("FormatterID string conversion = %v, want test", string(id))
	}

	// Test comparison
	id2 := FormatterID("test")
	if id != id2 {
		t.Error("FormatterID comparison failed for equal values")
	}

	id3 := FormatterID("other")
	if id == id3 {
		t.Error("FormatterID comparison failed for different values")
	}
}

func TestFormatterRegistry_RegisterNilExtensions(t *testing.T) {
	registry := NewFormatterRegistry()

	def := &FormatterDefinition{
		ID:         "noext",
		Name:       "No Extensions Formatter",
		Command:    "noext",
		Extensions: nil,
	}

	err := registry.Register(def)
	if err != nil {
		t.Errorf("Register with nil extensions failed: %v", err)
	}

	got, found := registry.Get("noext")
	if !found {
		t.Error("formatter not found after register")
	}

	if got.Extensions != nil {
		t.Error("Extensions should remain nil")
	}
}

func TestFormatterRegistry_MultipleExtensionsSameFormatter(t *testing.T) {
	registry := NewFormatterRegistry()

	def := &FormatterDefinition{
		ID:         "prettier",
		Name:       "Prettier",
		Command:    "prettier",
		Extensions: []string{".js", ".ts", ".jsx", ".tsx", ".json"},
	}

	err := registry.Register(def)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Check each extension returns the formatter
	for _, ext := range def.Extensions {
		result := registry.GetByExtension(ext)
		if len(result) != 1 {
			t.Errorf("GetByExtension(%q) returned %d formatters, want 1", ext, len(result))
			continue
		}
		if result[0].ID != "prettier" {
			t.Errorf("GetByExtension(%q) returned wrong formatter: %v", ext, result[0].ID)
		}
	}
}

func TestDuplicateFormatterError_ImplementsError(t *testing.T) {
	var err error = &DuplicateFormatterError{ID: "test"}

	// Verify it implements the error interface
	if err.Error() != "formatter already registered: test" {
		t.Errorf("Error() = %v, want 'formatter already registered: test'", err.Error())
	}
}
