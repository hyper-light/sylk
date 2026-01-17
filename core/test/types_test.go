package test

import (
	"sync"
	"testing"
)

// TestTestFrameworkIDConstants verifies all TestFrameworkID constants are defined correctly.
func TestTestFrameworkIDConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant TestFrameworkID
		expected string
	}{
		{"FrameworkGoTest", FrameworkGoTest, "go-test"},
		{"FrameworkJest", FrameworkJest, "jest"},
		{"FrameworkPytest", FrameworkPytest, "pytest"},
		{"FrameworkCargoTest", FrameworkCargoTest, "cargo-test"},
		{"FrameworkRSpec", FrameworkRSpec, "rspec"},
		{"FrameworkMocha", FrameworkMocha, "mocha"},
		{"FrameworkVitest", FrameworkVitest, "vitest"},
		{"FrameworkPHPUnit", FrameworkPHPUnit, "phpunit"},
		{"FrameworkJUnit", FrameworkJUnit, "junit"},
		{"FrameworkNUnit", FrameworkNUnit, "nunit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.constant)
			}
		})
	}
}

// TestTestStatusConstants verifies all TestStatus constants are defined correctly.
func TestTestStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant TestStatus
		expected string
	}{
		{"TestStatusPassed", TestStatusPassed, "passed"},
		{"TestStatusFailed", TestStatusFailed, "failed"},
		{"TestStatusSkipped", TestStatusSkipped, "skipped"},
		{"TestStatusError", TestStatusError, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.constant)
			}
		})
	}
}

// TestNewTestFrameworkRegistry verifies that NewTestFrameworkRegistry creates an empty registry.
func TestNewTestFrameworkRegistry(t *testing.T) {
	registry := NewTestFrameworkRegistry()

	if registry == nil {
		t.Fatal("expected non-nil registry")
	}

	if registry.frameworks == nil {
		t.Error("expected frameworks map to be initialized")
	}

	if len(registry.frameworks) != 0 {
		t.Errorf("expected empty frameworks map, got %d entries", len(registry.frameworks))
	}

	// Verify List returns empty slice
	list := registry.List()
	if len(list) != 0 {
		t.Errorf("expected List() to return empty slice, got %d entries", len(list))
	}
}

// TestTestFrameworkRegistryRegister tests the Register method with various inputs.
func TestTestFrameworkRegistryRegister(t *testing.T) {
	tests := []struct {
		name        string
		definitions []*TestFrameworkDefinition
		wantErr     error
		errContains string
	}{
		{
			name: "register valid framework",
			definitions: []*TestFrameworkDefinition{
				{
					ID:       FrameworkGoTest,
					Name:     "Go Test",
					Language: "go",
				},
			},
			wantErr: nil,
		},
		{
			name: "register nil definition",
			definitions: []*TestFrameworkDefinition{
				nil,
			},
			wantErr: ErrNilDefinition,
		},
		{
			name: "register empty ID",
			definitions: []*TestFrameworkDefinition{
				{
					ID:       "",
					Name:     "Empty ID Framework",
					Language: "go",
				},
			},
			wantErr: ErrEmptyFrameworkID,
		},
		{
			name: "register duplicate ID",
			definitions: []*TestFrameworkDefinition{
				{
					ID:       FrameworkJest,
					Name:     "Jest",
					Language: "javascript",
				},
				{
					ID:       FrameworkJest,
					Name:     "Jest Duplicate",
					Language: "javascript",
				},
			},
			errContains: "framework already registered: jest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewTestFrameworkRegistry()
			var lastErr error

			for _, def := range tt.definitions {
				lastErr = registry.Register(def)
			}

			if tt.wantErr != nil {
				if lastErr != tt.wantErr {
					t.Errorf("expected error %v, got %v", tt.wantErr, lastErr)
				}
			} else if tt.errContains != "" {
				if lastErr == nil {
					t.Errorf("expected error containing %q, got nil", tt.errContains)
				} else if lastErr.Error() != tt.errContains {
					t.Errorf("expected error %q, got %q", tt.errContains, lastErr.Error())
				}
			} else if lastErr != nil {
				t.Errorf("unexpected error: %v", lastErr)
			}
		})
	}
}

// TestTestFrameworkRegistryRegisterAddsFramework verifies that Register actually adds the framework.
func TestTestFrameworkRegistryRegisterAddsFramework(t *testing.T) {
	registry := NewTestFrameworkRegistry()
	def := &TestFrameworkDefinition{
		ID:       FrameworkPytest,
		Name:     "Pytest",
		Language: "python",
	}

	err := registry.Register(def)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the framework was added
	got, ok := registry.Get(FrameworkPytest)
	if !ok {
		t.Fatal("expected framework to be found after registration")
	}

	if got != def {
		t.Error("expected Get to return the same definition that was registered")
	}
}

// TestTestFrameworkRegistryGet tests the Get method.
func TestTestFrameworkRegistryGet(t *testing.T) {
	registry := NewTestFrameworkRegistry()

	// Register a framework
	def := &TestFrameworkDefinition{
		ID:       FrameworkCargoTest,
		Name:     "Cargo Test",
		Language: "rust",
	}
	_ = registry.Register(def)

	tests := []struct {
		name      string
		id        TestFrameworkID
		wantFound bool
	}{
		{"existing framework", FrameworkCargoTest, true},
		{"non-existing framework", FrameworkRSpec, false},
		{"empty ID", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := registry.Get(tt.id)

			if found != tt.wantFound {
				t.Errorf("expected found=%v, got found=%v", tt.wantFound, found)
			}

			if tt.wantFound && got == nil {
				t.Error("expected non-nil definition when found is true")
			}

			if !tt.wantFound && got != nil {
				t.Error("expected nil definition when found is false")
			}

			if tt.wantFound && got.ID != tt.id {
				t.Errorf("expected ID %q, got %q", tt.id, got.ID)
			}
		})
	}
}

// TestTestFrameworkRegistryList tests the List method.
func TestTestFrameworkRegistryList(t *testing.T) {
	tests := []struct {
		name     string
		register []TestFrameworkID
		wantLen  int
	}{
		{"empty registry", nil, 0},
		{"single framework", []TestFrameworkID{FrameworkGoTest}, 1},
		{"multiple frameworks", []TestFrameworkID{FrameworkGoTest, FrameworkJest, FrameworkPytest}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewTestFrameworkRegistry()

			for _, id := range tt.register {
				_ = registry.Register(&TestFrameworkDefinition{
					ID:       id,
					Name:     string(id),
					Language: "test",
				})
			}

			list := registry.List()
			if len(list) != tt.wantLen {
				t.Errorf("expected %d frameworks, got %d", tt.wantLen, len(list))
			}

			// Verify all registered frameworks are in the list
			for _, id := range tt.register {
				found := false
				for _, def := range list {
					if def.ID == id {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected framework %q to be in list", id)
				}
			}
		})
	}
}

// TestTestFrameworkRegistryListReturnsCopy verifies that List returns a copy.
func TestTestFrameworkRegistryListReturnsCopy(t *testing.T) {
	registry := NewTestFrameworkRegistry()
	_ = registry.Register(&TestFrameworkDefinition{
		ID:       FrameworkGoTest,
		Name:     "Go Test",
		Language: "go",
	})

	list1 := registry.List()
	list2 := registry.List()

	// Modifying the first list should not affect the second
	if len(list1) > 0 {
		list1[0] = nil
	}

	if list2[0] == nil {
		t.Error("modifying List result should not affect subsequent List calls")
	}
}

// TestTestFrameworkRegistryGetByLanguage tests the GetByLanguage method.
func TestTestFrameworkRegistryGetByLanguage(t *testing.T) {
	registry := NewTestFrameworkRegistry()

	// Register frameworks with various languages
	frameworks := []*TestFrameworkDefinition{
		{ID: FrameworkGoTest, Name: "Go Test", Language: "go"},
		{ID: FrameworkJest, Name: "Jest", Language: "javascript"},
		{ID: FrameworkMocha, Name: "Mocha", Language: "javascript"},
		{ID: FrameworkVitest, Name: "Vitest", Language: "javascript"},
		{ID: FrameworkPytest, Name: "Pytest", Language: "python"},
		{ID: FrameworkCargoTest, Name: "Cargo Test", Language: "rust"},
	}

	for _, def := range frameworks {
		_ = registry.Register(def)
	}

	tests := []struct {
		name     string
		language string
		wantLen  int
		wantIDs  []TestFrameworkID
	}{
		{"go frameworks", "go", 1, []TestFrameworkID{FrameworkGoTest}},
		{"javascript frameworks", "javascript", 3, []TestFrameworkID{FrameworkJest, FrameworkMocha, FrameworkVitest}},
		{"python frameworks", "python", 1, []TestFrameworkID{FrameworkPytest}},
		{"rust frameworks", "rust", 1, []TestFrameworkID{FrameworkCargoTest}},
		{"non-existing language", "ruby", 0, nil},
		{"empty language", "", 0, nil},
		{"case sensitive - JavaScript", "JavaScript", 0, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := registry.GetByLanguage(tt.language)

			if len(result) != tt.wantLen {
				t.Errorf("expected %d frameworks, got %d", tt.wantLen, len(result))
			}

			// Verify all expected IDs are present
			for _, wantID := range tt.wantIDs {
				found := false
				for _, def := range result {
					if def.ID == wantID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected framework %q to be in result", wantID)
				}
			}
		})
	}
}

// TestRegistryErrorError tests the RegistryError.Error method.
func TestRegistryErrorError(t *testing.T) {
	tests := []struct {
		name    string
		err     *RegistryError
		wantMsg string
	}{
		{
			name:    "simple message",
			err:     &RegistryError{Message: "test error"},
			wantMsg: "test error",
		},
		{
			name:    "empty message",
			err:     &RegistryError{Message: ""},
			wantMsg: "",
		},
		{
			name:    "ErrNilDefinition",
			err:     ErrNilDefinition,
			wantMsg: "cannot register nil framework definition",
		},
		{
			name:    "ErrEmptyFrameworkID",
			err:     ErrEmptyFrameworkID,
			wantMsg: "framework definition must have a non-empty ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.wantMsg {
				t.Errorf("expected %q, got %q", tt.wantMsg, got)
			}
		})
	}
}

// TestFrameworkExistsErrorError tests the FrameworkExistsError.Error method.
func TestFrameworkExistsErrorError(t *testing.T) {
	tests := []struct {
		name    string
		err     *FrameworkExistsError
		wantMsg string
	}{
		{
			name:    "go-test",
			err:     &FrameworkExistsError{ID: FrameworkGoTest},
			wantMsg: "framework already registered: go-test",
		},
		{
			name:    "jest",
			err:     &FrameworkExistsError{ID: FrameworkJest},
			wantMsg: "framework already registered: jest",
		},
		{
			name:    "empty ID",
			err:     &FrameworkExistsError{ID: ""},
			wantMsg: "framework already registered: ",
		},
		{
			name:    "custom ID",
			err:     &FrameworkExistsError{ID: "custom-framework"},
			wantMsg: "framework already registered: custom-framework",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.wantMsg {
				t.Errorf("expected %q, got %q", tt.wantMsg, got)
			}
		})
	}
}

// TestErrNilDefinition verifies the ErrNilDefinition error value.
func TestErrNilDefinition(t *testing.T) {
	if ErrNilDefinition == nil {
		t.Fatal("ErrNilDefinition should not be nil")
	}

	if ErrNilDefinition.Message != "cannot register nil framework definition" {
		t.Errorf("unexpected message: %q", ErrNilDefinition.Message)
	}

	// Verify it implements error interface
	var err error = ErrNilDefinition
	if err.Error() != "cannot register nil framework definition" {
		t.Errorf("unexpected Error() result: %q", err.Error())
	}
}

// TestErrEmptyFrameworkID verifies the ErrEmptyFrameworkID error value.
func TestErrEmptyFrameworkID(t *testing.T) {
	if ErrEmptyFrameworkID == nil {
		t.Fatal("ErrEmptyFrameworkID should not be nil")
	}

	if ErrEmptyFrameworkID.Message != "framework definition must have a non-empty ID" {
		t.Errorf("unexpected message: %q", ErrEmptyFrameworkID.Message)
	}

	// Verify it implements error interface
	var err error = ErrEmptyFrameworkID
	if err.Error() != "framework definition must have a non-empty ID" {
		t.Errorf("unexpected Error() result: %q", err.Error())
	}
}

// TestTestFrameworkRegistryConcurrentAccess tests thread safety of the registry.
func TestTestFrameworkRegistryConcurrentAccess(t *testing.T) {
	registry := NewTestFrameworkRegistry()

	// Number of goroutines for each operation
	const numGoroutines = 100

	var wg sync.WaitGroup

	// Concurrent Register operations
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			id := TestFrameworkID("framework-" + string(rune('a'+idx%26)))
			_ = registry.Register(&TestFrameworkDefinition{
				ID:       id,
				Name:     "Framework " + string(rune('A'+idx%26)),
				Language: "test",
			})
		}(i)
	}

	// Concurrent Get operations
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			id := TestFrameworkID("framework-" + string(rune('a'+idx%26)))
			_, _ = registry.Get(id)
		}(i)
	}

	// Concurrent List operations
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = registry.List()
		}()
	}

	// Concurrent GetByLanguage operations
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = registry.GetByLanguage("test")
		}()
	}

	wg.Wait()

	// Verify the registry is in a consistent state
	list := registry.List()
	if len(list) == 0 {
		t.Error("expected some frameworks to be registered")
	}

	// Verify each framework in the list can be retrieved
	for _, def := range list {
		got, ok := registry.Get(def.ID)
		if !ok {
			t.Errorf("framework %q in List() but not found via Get()", def.ID)
		}
		if got != def {
			t.Errorf("Get() returned different definition for %q", def.ID)
		}
	}
}

// TestTestFrameworkRegistryConcurrentRegisterDuplicate tests concurrent duplicate registration.
func TestTestFrameworkRegistryConcurrentRegisterDuplicate(t *testing.T) {
	registry := NewTestFrameworkRegistry()

	const numGoroutines = 100
	var wg sync.WaitGroup

	successCount := 0
	var mu sync.Mutex

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			err := registry.Register(&TestFrameworkDefinition{
				ID:       FrameworkGoTest,
				Name:     "Go Test",
				Language: "go",
			})
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one registration should succeed
	if successCount != 1 {
		t.Errorf("expected exactly 1 successful registration, got %d", successCount)
	}

	// Verify the framework was registered
	_, ok := registry.Get(FrameworkGoTest)
	if !ok {
		t.Error("expected framework to be registered")
	}
}

// TestTestFrameworkRegistryEdgeCases tests edge cases.
func TestTestFrameworkRegistryEdgeCases(t *testing.T) {
	t.Run("register framework with minimal fields", func(t *testing.T) {
		registry := NewTestFrameworkRegistry()
		def := &TestFrameworkDefinition{
			ID: "minimal",
		}
		err := registry.Register(def)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		got, ok := registry.Get("minimal")
		if !ok {
			t.Error("expected framework to be found")
		}
		if got.Name != "" {
			t.Error("expected empty Name")
		}
		if got.Language != "" {
			t.Error("expected empty Language")
		}
	})

	t.Run("register framework with all fields populated", func(t *testing.T) {
		registry := NewTestFrameworkRegistry()
		def := &TestFrameworkDefinition{
			ID:               FrameworkGoTest,
			Name:             "Go Test",
			Language:         "go",
			RunCommand:       "go test ./...",
			RunFileCommand:   "go test {file}",
			RunSingleCommand: "go test -run {test} {file}",
			CoverageCommand:  "go test -cover ./...",
			WatchCommand:     "",
			TestFilePatterns: []string{"*_test.go"},
			ConfigFiles:      []string{"go.mod"},
			Priority:         10,
			Enabled:          func(projectDir string) bool { return true },
			ParseOutput:      func(output []byte) (*TestResult, error) { return nil, nil },
		}
		err := registry.Register(def)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		got, ok := registry.Get(FrameworkGoTest)
		if !ok {
			t.Error("expected framework to be found")
		}
		if got.Priority != 10 {
			t.Errorf("expected Priority 10, got %d", got.Priority)
		}
		if len(got.TestFilePatterns) != 1 || got.TestFilePatterns[0] != "*_test.go" {
			t.Error("TestFilePatterns not preserved")
		}
	})

	t.Run("GetByLanguage returns nil for empty registry", func(t *testing.T) {
		registry := NewTestFrameworkRegistry()
		result := registry.GetByLanguage("go")
		if result != nil && len(result) != 0 {
			t.Error("expected nil or empty slice for empty registry")
		}
	})

	t.Run("Get returns false for empty string ID", func(t *testing.T) {
		registry := NewTestFrameworkRegistry()
		_, ok := registry.Get("")
		if ok {
			t.Error("expected ok=false for empty ID")
		}
	})

	t.Run("whitespace-only ID is valid but not recommended", func(t *testing.T) {
		registry := NewTestFrameworkRegistry()
		def := &TestFrameworkDefinition{
			ID: "   ",
		}
		err := registry.Register(def)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		got, ok := registry.Get("   ")
		if !ok {
			t.Error("expected framework to be found")
		}
		if got.ID != "   " {
			t.Errorf("expected ID '   ', got %q", got.ID)
		}
	})
}

// TestRegistryErrorImplementsError verifies RegistryError implements error interface.
func TestRegistryErrorImplementsError(t *testing.T) {
	var _ error = &RegistryError{}
	var _ error = ErrNilDefinition
	var _ error = ErrEmptyFrameworkID
}

// TestFrameworkExistsErrorImplementsError verifies FrameworkExistsError implements error interface.
func TestFrameworkExistsErrorImplementsError(t *testing.T) {
	var _ error = &FrameworkExistsError{}
	var _ error = &FrameworkExistsError{ID: FrameworkGoTest}
}

// TestTestFrameworkIDConversion tests type conversion for TestFrameworkID.
func TestTestFrameworkIDConversion(t *testing.T) {
	// String to TestFrameworkID
	id := TestFrameworkID("custom-framework")
	if string(id) != "custom-framework" {
		t.Errorf("expected 'custom-framework', got %q", string(id))
	}

	// Comparison
	if id == FrameworkGoTest {
		t.Error("expected different IDs to not be equal")
	}

	id2 := TestFrameworkID("custom-framework")
	if id != id2 {
		t.Error("expected same IDs to be equal")
	}
}

// TestTestStatusConversion tests type conversion for TestStatus.
func TestTestStatusConversion(t *testing.T) {
	// String to TestStatus
	status := TestStatus("custom-status")
	if string(status) != "custom-status" {
		t.Errorf("expected 'custom-status', got %q", string(status))
	}

	// Comparison
	if status == TestStatusPassed {
		t.Error("expected different statuses to not be equal")
	}

	status2 := TestStatus("custom-status")
	if status != status2 {
		t.Error("expected same statuses to be equal")
	}
}
