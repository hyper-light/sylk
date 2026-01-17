package lsp

import (
	"sync"
	"testing"
)

// TestServerIDConstants verifies all ServerID constants have correct values.
func TestServerIDConstants(t *testing.T) {
	tests := []struct {
		name     string
		serverID ServerID
		expected string
	}{
		{"ServerGopls", ServerGopls, "gopls"},
		{"ServerTypeScript", ServerTypeScript, "typescript-language-server"},
		{"ServerPyright", ServerPyright, "pyright"},
		{"ServerRustAna", ServerRustAna, "rust-analyzer"},
		{"ServerClangd", ServerClangd, "clangd"},
		{"ServerLuaLS", ServerLuaLS, "lua-language-server"},
		{"ServerZLS", ServerZLS, "zls"},
		{"ServerOCamlLSP", ServerOCamlLSP, "ocamllsp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.serverID) != tt.expected {
				t.Errorf("%s = %q, want %q", tt.name, tt.serverID, tt.expected)
			}
		})
	}
}

// TestAutoDownloadSourceConstants verifies all AutoDownload source constants.
func TestAutoDownloadSourceConstants(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		expected string
	}{
		{"SourceNPM", SourceNPM, "npm"},
		{"SourceGo", SourceGo, "go"},
		{"SourceGitHub", SourceGitHub, "github"},
		{"SourcePip", SourcePip, "pip"},
		{"SourceCargo", SourceCargo, "cargo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.source != tt.expected {
				t.Errorf("%s = %q, want %q", tt.name, tt.source, tt.expected)
			}
		})
	}
}

// TestClientStatusString verifies ClientStatus.String returns correct strings.
func TestClientStatusString(t *testing.T) {
	tests := []struct {
		name     string
		status   ClientStatus
		expected string
	}{
		{"StatusStarting", StatusStarting, "starting"},
		{"StatusReady", StatusReady, "ready"},
		{"StatusError", StatusError, "error"},
		{"StatusStopped", StatusStopped, "stopped"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("ClientStatus(%d).String() = %q, want %q", tt.status, got, tt.expected)
			}
		})
	}
}

// TestClientStatusStringUnknown verifies unknown ClientStatus returns "unknown".
func TestClientStatusStringUnknown(t *testing.T) {
	tests := []struct {
		name   string
		status ClientStatus
	}{
		{"negative value", ClientStatus(-1)},
		{"value 100", ClientStatus(100)},
		{"value 999", ClientStatus(999)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.String(); got != "unknown" {
				t.Errorf("ClientStatus(%d).String() = %q, want %q", tt.status, got, "unknown")
			}
		})
	}
}

// TestDiagnosticSeverityString verifies DiagnosticSeverity.String returns correct strings.
func TestDiagnosticSeverityString(t *testing.T) {
	tests := []struct {
		name     string
		severity DiagnosticSeverity
		expected string
	}{
		{"SeverityError", SeverityError, "error"},
		{"SeverityWarning", SeverityWarning, "warning"},
		{"SeverityInformation", SeverityInformation, "information"},
		{"SeverityHint", SeverityHint, "hint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.severity.String(); got != tt.expected {
				t.Errorf("DiagnosticSeverity(%d).String() = %q, want %q", tt.severity, got, tt.expected)
			}
		})
	}
}

// TestDiagnosticSeverityStringUnknown verifies unknown DiagnosticSeverity returns "unknown".
func TestDiagnosticSeverityStringUnknown(t *testing.T) {
	tests := []struct {
		name     string
		severity DiagnosticSeverity
	}{
		{"zero value", DiagnosticSeverity(0)},
		{"negative value", DiagnosticSeverity(-1)},
		{"value 5", DiagnosticSeverity(5)},
		{"value 100", DiagnosticSeverity(100)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.severity.String(); got != "unknown" {
				t.Errorf("DiagnosticSeverity(%d).String() = %q, want %q", tt.severity, got, "unknown")
			}
		})
	}
}

// TestDiagnosticSeverityValues verifies severity constants match LSP spec values.
func TestDiagnosticSeverityValues(t *testing.T) {
	// LSP spec defines: Error=1, Warning=2, Information=3, Hint=4
	tests := []struct {
		name     string
		severity DiagnosticSeverity
		expected int
	}{
		{"SeverityError", SeverityError, 1},
		{"SeverityWarning", SeverityWarning, 2},
		{"SeverityInformation", SeverityInformation, 3},
		{"SeverityHint", SeverityHint, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.severity) != tt.expected {
				t.Errorf("%s = %d, want %d (per LSP spec)", tt.name, tt.severity, tt.expected)
			}
		})
	}
}

// TestNewLSPRegistry verifies NewLSPRegistry creates an empty registry.
func TestNewLSPRegistry(t *testing.T) {
	r := NewLSPRegistry()

	if r == nil {
		t.Fatal("NewLSPRegistry() returned nil")
	}

	if r.servers == nil {
		t.Error("NewLSPRegistry().servers is nil")
	}
	if r.byExtension == nil {
		t.Error("NewLSPRegistry().byExtension is nil")
	}
	if r.byLanguageID == nil {
		t.Error("NewLSPRegistry().byLanguageID is nil")
	}

	if len(r.servers) != 0 {
		t.Errorf("NewLSPRegistry().servers has %d entries, want 0", len(r.servers))
	}
	if len(r.byExtension) != 0 {
		t.Errorf("NewLSPRegistry().byExtension has %d entries, want 0", len(r.byExtension))
	}
	if len(r.byLanguageID) != 0 {
		t.Errorf("NewLSPRegistry().byLanguageID has %d entries, want 0", len(r.byLanguageID))
	}
}

// TestLSPRegistryRegister verifies Register adds a server to the registry.
func TestLSPRegistryRegister(t *testing.T) {
	r := NewLSPRegistry()

	def := &LanguageServerDefinition{
		ID:          ServerGopls,
		Name:        "Go Language Server",
		Command:     "gopls",
		Extensions:  []string{".go"},
		LanguageIDs: []string{"go"},
		RootMarkers: []string{"go.mod", "go.sum"},
	}

	r.Register(def)

	// Verify server is registered
	if !r.Has(ServerGopls) {
		t.Error("Has(ServerGopls) = false after Register")
	}

	// Verify Get returns the definition
	got := r.Get(ServerGopls)
	if got != def {
		t.Error("Get(ServerGopls) returned different pointer than registered")
	}

	// Verify extension index
	extServers := r.GetByExtension(".go")
	if len(extServers) != 1 || extServers[0] != ServerGopls {
		t.Errorf("GetByExtension(.go) = %v, want [%s]", extServers, ServerGopls)
	}

	// Verify language ID index
	langServers := r.GetByLanguageID("go")
	if len(langServers) != 1 || langServers[0] != ServerGopls {
		t.Errorf("GetByLanguageID(go) = %v, want [%s]", langServers, ServerGopls)
	}
}

// TestLSPRegistryRegisterReplace verifies Register replaces existing server.
func TestLSPRegistryRegisterReplace(t *testing.T) {
	r := NewLSPRegistry()

	def1 := &LanguageServerDefinition{
		ID:      ServerGopls,
		Name:    "Original",
		Command: "gopls-old",
	}
	def2 := &LanguageServerDefinition{
		ID:      ServerGopls,
		Name:    "Replacement",
		Command: "gopls-new",
	}

	r.Register(def1)
	r.Register(def2)

	got := r.Get(ServerGopls)
	if got.Name != "Replacement" {
		t.Errorf("Get(ServerGopls).Name = %q, want %q", got.Name, "Replacement")
	}
	if got.Command != "gopls-new" {
		t.Errorf("Get(ServerGopls).Command = %q, want %q", got.Command, "gopls-new")
	}
}

// TestLSPRegistryGet verifies Get retrieves servers correctly.
func TestLSPRegistryGet(t *testing.T) {
	r := NewLSPRegistry()

	// Get from empty registry
	if got := r.Get(ServerGopls); got != nil {
		t.Errorf("Get from empty registry = %v, want nil", got)
	}

	// Register and get
	def := &LanguageServerDefinition{
		ID:   ServerGopls,
		Name: "Go Language Server",
	}
	r.Register(def)

	if got := r.Get(ServerGopls); got != def {
		t.Error("Get returned different pointer than registered")
	}

	// Get non-existent server
	if got := r.Get(ServerPyright); got != nil {
		t.Errorf("Get(ServerPyright) = %v, want nil", got)
	}
}

// TestLSPRegistryGetByExtension verifies GetByExtension works correctly.
func TestLSPRegistryGetByExtension(t *testing.T) {
	r := NewLSPRegistry()

	// Empty registry
	if got := r.GetByExtension(".go"); got != nil {
		t.Errorf("GetByExtension from empty registry = %v, want nil", got)
	}

	// Single server for extension
	r.Register(&LanguageServerDefinition{
		ID:         ServerGopls,
		Extensions: []string{".go"},
	})

	got := r.GetByExtension(".go")
	if len(got) != 1 || got[0] != ServerGopls {
		t.Errorf("GetByExtension(.go) = %v, want [%s]", got, ServerGopls)
	}

	// Non-existent extension
	if got := r.GetByExtension(".xyz"); got != nil {
		t.Errorf("GetByExtension(.xyz) = %v, want nil", got)
	}
}

// TestLSPRegistryGetByExtensionMultipleServers verifies multiple servers for same extension.
func TestLSPRegistryGetByExtensionMultipleServers(t *testing.T) {
	r := NewLSPRegistry()

	// Register two servers for .ts
	r.Register(&LanguageServerDefinition{
		ID:         ServerTypeScript,
		Extensions: []string{".ts", ".tsx"},
	})
	r.Register(&LanguageServerDefinition{
		ID:         ServerID("deno"),
		Extensions: []string{".ts", ".tsx"},
	})

	got := r.GetByExtension(".ts")
	if len(got) != 2 {
		t.Errorf("GetByExtension(.ts) returned %d servers, want 2", len(got))
	}

	// Check both servers are present
	hasTS := false
	hasDeno := false
	for _, id := range got {
		if id == ServerTypeScript {
			hasTS = true
		}
		if id == ServerID("deno") {
			hasDeno = true
		}
	}
	if !hasTS {
		t.Error("GetByExtension(.ts) missing ServerTypeScript")
	}
	if !hasDeno {
		t.Error("GetByExtension(.ts) missing deno")
	}
}

// TestLSPRegistryGetByLanguageID verifies GetByLanguageID works correctly.
func TestLSPRegistryGetByLanguageID(t *testing.T) {
	r := NewLSPRegistry()

	// Empty registry
	if got := r.GetByLanguageID("python"); got != nil {
		t.Errorf("GetByLanguageID from empty registry = %v, want nil", got)
	}

	// Register server with language ID
	r.Register(&LanguageServerDefinition{
		ID:          ServerPyright,
		LanguageIDs: []string{"python"},
	})

	got := r.GetByLanguageID("python")
	if len(got) != 1 || got[0] != ServerPyright {
		t.Errorf("GetByLanguageID(python) = %v, want [%s]", got, ServerPyright)
	}

	// Non-existent language ID
	if got := r.GetByLanguageID("fortran"); got != nil {
		t.Errorf("GetByLanguageID(fortran) = %v, want nil", got)
	}
}

// TestLSPRegistryGetByLanguageIDMultipleServers verifies multiple servers for same language.
func TestLSPRegistryGetByLanguageIDMultipleServers(t *testing.T) {
	r := NewLSPRegistry()

	// Register multiple Python LSP servers
	r.Register(&LanguageServerDefinition{
		ID:          ServerPyright,
		LanguageIDs: []string{"python"},
	})
	r.Register(&LanguageServerDefinition{
		ID:          ServerID("pylsp"),
		LanguageIDs: []string{"python"},
	})

	got := r.GetByLanguageID("python")
	if len(got) != 2 {
		t.Errorf("GetByLanguageID(python) returned %d servers, want 2", len(got))
	}
}

// TestLSPRegistryAll verifies All returns all registered servers.
func TestLSPRegistryAll(t *testing.T) {
	r := NewLSPRegistry()

	// Empty registry
	if got := r.All(); len(got) != 0 {
		t.Errorf("All() from empty registry = %d entries, want 0", len(got))
	}

	// Register servers
	defs := []*LanguageServerDefinition{
		{ID: ServerGopls, Name: "Go"},
		{ID: ServerPyright, Name: "Python"},
		{ID: ServerTypeScript, Name: "TypeScript"},
	}
	for _, def := range defs {
		r.Register(def)
	}

	got := r.All()
	if len(got) != 3 {
		t.Errorf("All() returned %d entries, want 3", len(got))
	}

	// Verify all servers are present
	ids := make(map[ServerID]bool)
	for _, def := range got {
		ids[def.ID] = true
	}
	for _, def := range defs {
		if !ids[def.ID] {
			t.Errorf("All() missing server %s", def.ID)
		}
	}
}

// TestLSPRegistryIDs verifies IDs returns all registered server IDs.
func TestLSPRegistryIDs(t *testing.T) {
	r := NewLSPRegistry()

	// Empty registry
	if got := r.IDs(); len(got) != 0 {
		t.Errorf("IDs() from empty registry = %d entries, want 0", len(got))
	}

	// Register servers
	expectedIDs := []ServerID{ServerGopls, ServerPyright, ServerRustAna}
	for _, id := range expectedIDs {
		r.Register(&LanguageServerDefinition{ID: id})
	}

	got := r.IDs()
	if len(got) != len(expectedIDs) {
		t.Errorf("IDs() returned %d entries, want %d", len(got), len(expectedIDs))
	}

	// Verify all IDs are present
	gotSet := make(map[ServerID]bool)
	for _, id := range got {
		gotSet[id] = true
	}
	for _, id := range expectedIDs {
		if !gotSet[id] {
			t.Errorf("IDs() missing %s", id)
		}
	}
}

// TestLSPRegistryHas verifies Has checks existence correctly.
func TestLSPRegistryHas(t *testing.T) {
	r := NewLSPRegistry()

	// Empty registry
	if r.Has(ServerGopls) {
		t.Error("Has(ServerGopls) = true on empty registry")
	}

	// Register server
	r.Register(&LanguageServerDefinition{ID: ServerGopls})

	if !r.Has(ServerGopls) {
		t.Error("Has(ServerGopls) = false after Register")
	}

	// Non-existent server
	if r.Has(ServerPyright) {
		t.Error("Has(ServerPyright) = true when not registered")
	}
}

// TestLSPRegistryRegisterMultipleExtensions verifies server with multiple extensions.
func TestLSPRegistryRegisterMultipleExtensions(t *testing.T) {
	r := NewLSPRegistry()

	r.Register(&LanguageServerDefinition{
		ID:         ServerTypeScript,
		Extensions: []string{".ts", ".tsx", ".js", ".jsx"},
	})

	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
		got := r.GetByExtension(ext)
		if len(got) != 1 || got[0] != ServerTypeScript {
			t.Errorf("GetByExtension(%s) = %v, want [%s]", ext, got, ServerTypeScript)
		}
	}
}

// TestLSPRegistryRegisterMultipleLanguageIDs verifies server with multiple language IDs.
func TestLSPRegistryRegisterMultipleLanguageIDs(t *testing.T) {
	r := NewLSPRegistry()

	r.Register(&LanguageServerDefinition{
		ID:          ServerTypeScript,
		LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"},
	})

	for _, langID := range []string{"typescript", "typescriptreact", "javascript", "javascriptreact"} {
		got := r.GetByLanguageID(langID)
		if len(got) != 1 || got[0] != ServerTypeScript {
			t.Errorf("GetByLanguageID(%s) = %v, want [%s]", langID, got, ServerTypeScript)
		}
	}
}

// TestLSPRegistryRegisterWithAutoDownload verifies registration with AutoDownload config.
func TestLSPRegistryRegisterWithAutoDownload(t *testing.T) {
	r := NewLSPRegistry()

	def := &LanguageServerDefinition{
		ID:      ServerTypeScript,
		Command: "typescript-language-server",
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "typescript-language-server",
			Binary:  "typescript-language-server",
		},
	}

	r.Register(def)

	got := r.Get(ServerTypeScript)
	if got == nil {
		t.Fatal("Get(ServerTypeScript) = nil after Register")
	}
	if got.AutoDownload == nil {
		t.Fatal("AutoDownload is nil")
	}
	if got.AutoDownload.Source != SourceNPM {
		t.Errorf("AutoDownload.Source = %q, want %q", got.AutoDownload.Source, SourceNPM)
	}
	if got.AutoDownload.Package != "typescript-language-server" {
		t.Errorf("AutoDownload.Package = %q, want %q", got.AutoDownload.Package, "typescript-language-server")
	}
}

// TestLSPRegistryRegisterWithEnabledFunc verifies registration with Enabled function.
func TestLSPRegistryRegisterWithEnabledFunc(t *testing.T) {
	r := NewLSPRegistry()

	enabled := true
	def := &LanguageServerDefinition{
		ID:      ServerGopls,
		Enabled: func() bool { return enabled },
	}

	r.Register(def)

	got := r.Get(ServerGopls)
	if got == nil {
		t.Fatal("Get(ServerGopls) = nil after Register")
	}
	if got.Enabled == nil {
		t.Fatal("Enabled is nil")
	}
	if !got.Enabled() {
		t.Error("Enabled() = false, want true")
	}

	enabled = false
	if got.Enabled() {
		t.Error("Enabled() = true after setting enabled=false, want false")
	}
}

// TestLSPRegistryConcurrentOperations verifies thread safety of registry operations.
func TestLSPRegistryConcurrentOperations(t *testing.T) {
	r := NewLSPRegistry()
	var wg sync.WaitGroup

	// Pre-register some servers
	r.Register(&LanguageServerDefinition{
		ID:          ServerGopls,
		Extensions:  []string{".go"},
		LanguageIDs: []string{"go"},
	})

	// Concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.Get(ServerGopls)
				_ = r.Has(ServerGopls)
				_ = r.GetByExtension(".go")
				_ = r.GetByLanguageID("go")
				_ = r.All()
				_ = r.IDs()
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				r.Register(&LanguageServerDefinition{
					ID:          ServerID("test-server"),
					Extensions:  []string{".test"},
					LanguageIDs: []string{"test"},
				})
			}
		}(i)
	}

	wg.Wait()
}

// TestLSPRegistryNoDuplicateExtensions verifies appendUnique prevents duplicates.
func TestLSPRegistryNoDuplicateExtensions(t *testing.T) {
	r := NewLSPRegistry()

	// Register same server twice
	def := &LanguageServerDefinition{
		ID:         ServerGopls,
		Extensions: []string{".go"},
	}
	r.Register(def)
	r.Register(def)

	got := r.GetByExtension(".go")
	if len(got) != 1 {
		t.Errorf("GetByExtension(.go) after duplicate Register = %d entries, want 1", len(got))
	}
}

// TestLSPRegistryNoDuplicateLanguageIDs verifies appendUnique prevents duplicates in language IDs.
func TestLSPRegistryNoDuplicateLanguageIDs(t *testing.T) {
	r := NewLSPRegistry()

	// Register same server twice
	def := &LanguageServerDefinition{
		ID:          ServerGopls,
		LanguageIDs: []string{"go"},
	}
	r.Register(def)
	r.Register(def)

	got := r.GetByLanguageID("go")
	if len(got) != 1 {
		t.Errorf("GetByLanguageID(go) after duplicate Register = %d entries, want 1", len(got))
	}
}

// TestDefaultRegistry verifies DefaultRegistry is initialized.
func TestDefaultRegistry(t *testing.T) {
	if DefaultRegistry == nil {
		t.Fatal("DefaultRegistry is nil")
	}
	if DefaultRegistry.servers == nil {
		t.Error("DefaultRegistry.servers is nil")
	}
}

// TestClientStatusConstants verifies ClientStatus constants have expected iota values.
func TestClientStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   ClientStatus
		expected int
	}{
		{"StatusStarting", StatusStarting, 0},
		{"StatusReady", StatusReady, 1},
		{"StatusError", StatusError, 2},
		{"StatusStopped", StatusStopped, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.status) != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.status, tt.expected)
			}
		})
	}
}

// TestLanguageServerDefinitionFields verifies all fields can be set.
func TestLanguageServerDefinitionFields(t *testing.T) {
	def := LanguageServerDefinition{
		ID:          ServerGopls,
		Name:        "Go Language Server",
		Command:     "gopls",
		Args:        []string{"-remote=auto"},
		Extensions:  []string{".go"},
		LanguageIDs: []string{"go"},
		RootMarkers: []string{"go.mod", "go.sum"},
		Enabled:     func() bool { return true },
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceGo,
			Package: "golang.org/x/tools/gopls",
			Binary:  "gopls",
		},
	}

	if def.ID != ServerGopls {
		t.Errorf("ID = %q, want %q", def.ID, ServerGopls)
	}
	if def.Name != "Go Language Server" {
		t.Errorf("Name = %q, want %q", def.Name, "Go Language Server")
	}
	if def.Command != "gopls" {
		t.Errorf("Command = %q, want %q", def.Command, "gopls")
	}
	if len(def.Args) != 1 || def.Args[0] != "-remote=auto" {
		t.Errorf("Args = %v, want %v", def.Args, []string{"-remote=auto"})
	}
	if len(def.Extensions) != 1 || def.Extensions[0] != ".go" {
		t.Errorf("Extensions = %v, want %v", def.Extensions, []string{".go"})
	}
	if len(def.LanguageIDs) != 1 || def.LanguageIDs[0] != "go" {
		t.Errorf("LanguageIDs = %v, want %v", def.LanguageIDs, []string{"go"})
	}
	if len(def.RootMarkers) != 2 {
		t.Errorf("RootMarkers has %d items, want 2", len(def.RootMarkers))
	}
	if def.Enabled == nil || !def.Enabled() {
		t.Error("Enabled is nil or returns false")
	}
	if def.AutoDownload == nil {
		t.Error("AutoDownload is nil")
	}
}

// TestAutoDownloadConfigFields verifies all AutoDownloadConfig fields.
func TestAutoDownloadConfigFields(t *testing.T) {
	config := AutoDownloadConfig{
		Source:  SourceNPM,
		Package: "typescript-language-server",
		Binary:  "typescript-language-server",
	}

	if config.Source != "npm" {
		t.Errorf("Source = %q, want %q", config.Source, "npm")
	}
	if config.Package != "typescript-language-server" {
		t.Errorf("Package = %q, want %q", config.Package, "typescript-language-server")
	}
	if config.Binary != "typescript-language-server" {
		t.Errorf("Binary = %q, want %q", config.Binary, "typescript-language-server")
	}
}

// TestServerCapabilitiesFields verifies ServerCapabilities fields.
func TestServerCapabilitiesFields(t *testing.T) {
	caps := ServerCapabilities{
		HoverProvider:           true,
		DefinitionProvider:      true,
		ReferencesProvider:      true,
		CompletionProvider:      true,
		DiagnosticProvider:      true,
		DocumentSymbolProvider:  true,
		WorkspaceSymbolProvider: true,
		CodeActionProvider:      true,
		RenameProvider:          true,
	}

	if !caps.HoverProvider {
		t.Error("HoverProvider should be true")
	}
	if !caps.DefinitionProvider {
		t.Error("DefinitionProvider should be true")
	}
	if !caps.ReferencesProvider {
		t.Error("ReferencesProvider should be true")
	}
	if !caps.CompletionProvider {
		t.Error("CompletionProvider should be true")
	}
	if !caps.DiagnosticProvider {
		t.Error("DiagnosticProvider should be true")
	}
	if !caps.DocumentSymbolProvider {
		t.Error("DocumentSymbolProvider should be true")
	}
	if !caps.WorkspaceSymbolProvider {
		t.Error("WorkspaceSymbolProvider should be true")
	}
	if !caps.CodeActionProvider {
		t.Error("CodeActionProvider should be true")
	}
	if !caps.RenameProvider {
		t.Error("RenameProvider should be true")
	}
}

// TestDiagnosticFields verifies Diagnostic struct fields.
func TestDiagnosticFields(t *testing.T) {
	diag := Diagnostic{
		Range: Range{
			Start: Position{Line: 10, Character: 5},
			End:   Position{Line: 10, Character: 20},
		},
		Severity: SeverityError,
		Code:     "E001",
		Source:   "gopls",
		Message:  "undefined: foo",
		RelatedInformation: []DiagnosticRelatedInformation{
			{
				Location: Location{
					URI: "file:///path/to/file.go",
					Range: Range{
						Start: Position{Line: 5, Character: 0},
						End:   Position{Line: 5, Character: 10},
					},
				},
				Message: "defined here",
			},
		},
	}

	if diag.Range.Start.Line != 10 {
		t.Errorf("Range.Start.Line = %d, want 10", diag.Range.Start.Line)
	}
	if diag.Range.Start.Character != 5 {
		t.Errorf("Range.Start.Character = %d, want 5", diag.Range.Start.Character)
	}
	if diag.Severity != SeverityError {
		t.Errorf("Severity = %d, want %d", diag.Severity, SeverityError)
	}
	if diag.Code != "E001" {
		t.Errorf("Code = %q, want %q", diag.Code, "E001")
	}
	if diag.Source != "gopls" {
		t.Errorf("Source = %q, want %q", diag.Source, "gopls")
	}
	if diag.Message != "undefined: foo" {
		t.Errorf("Message = %q, want %q", diag.Message, "undefined: foo")
	}
	if len(diag.RelatedInformation) != 1 {
		t.Fatalf("RelatedInformation has %d items, want 1", len(diag.RelatedInformation))
	}
	if diag.RelatedInformation[0].Message != "defined here" {
		t.Errorf("RelatedInformation[0].Message = %q, want %q", diag.RelatedInformation[0].Message, "defined here")
	}
}

// TestDiagnosticResultFields verifies DiagnosticResult struct fields.
func TestDiagnosticResultFields(t *testing.T) {
	result := DiagnosticResult{
		ServerID: ServerGopls,
		FilePath: "/path/to/file.go",
		Diagnostics: []Diagnostic{
			{Message: "error 1"},
			{Message: "error 2"},
		},
	}

	if result.ServerID != ServerGopls {
		t.Errorf("ServerID = %q, want %q", result.ServerID, ServerGopls)
	}
	if result.FilePath != "/path/to/file.go" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "/path/to/file.go")
	}
	if len(result.Diagnostics) != 2 {
		t.Errorf("Diagnostics has %d items, want 2", len(result.Diagnostics))
	}
}

// TestLSPConfigFields verifies LSPConfig struct fields.
func TestLSPConfigFields(t *testing.T) {
	config := LSPConfig{
		Disabled:     true,
		Command:      "custom-gopls",
		Args:         []string{"-v"},
		AutoDownload: true,
		RootMarkers:  []string{"custom.mod"},
		InitializationOptions: map[string]interface{}{
			"gopls": map[string]interface{}{
				"staticcheck": true,
			},
		},
	}

	if !config.Disabled {
		t.Error("Disabled should be true")
	}
	if config.Command != "custom-gopls" {
		t.Errorf("Command = %q, want %q", config.Command, "custom-gopls")
	}
	if len(config.Args) != 1 || config.Args[0] != "-v" {
		t.Errorf("Args = %v, want %v", config.Args, []string{"-v"})
	}
	if !config.AutoDownload {
		t.Error("AutoDownload should be true")
	}
	if len(config.RootMarkers) != 1 || config.RootMarkers[0] != "custom.mod" {
		t.Errorf("RootMarkers = %v, want %v", config.RootMarkers, []string{"custom.mod"})
	}
	if config.InitializationOptions == nil {
		t.Error("InitializationOptions is nil")
	}
}

// TestDetectionResultFields verifies DetectionResult struct fields.
func TestDetectionResultFields(t *testing.T) {
	result := DetectionResult{
		ServerID:   ServerGopls,
		Confidence: 0.95,
		Reason:     "Found go.mod in project root",
		RootPath:   "/path/to/project",
	}

	if result.ServerID != ServerGopls {
		t.Errorf("ServerID = %q, want %q", result.ServerID, ServerGopls)
	}
	if result.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", result.Confidence)
	}
	if result.Reason != "Found go.mod in project root" {
		t.Errorf("Reason = %q, want %q", result.Reason, "Found go.mod in project root")
	}
	if result.RootPath != "/path/to/project" {
		t.Errorf("RootPath = %q, want %q", result.RootPath, "/path/to/project")
	}
}

// TestLSPClientFields verifies LSPClient struct fields.
func TestLSPClientFields(t *testing.T) {
	client := LSPClient{
		ID:          "client-1",
		ServerID:    ServerGopls,
		ProjectRoot: "/path/to/project",
		Process:     nil,
		Capabilities: ServerCapabilities{
			HoverProvider: true,
		},
		Status: StatusReady,
	}

	if client.ID != "client-1" {
		t.Errorf("ID = %q, want %q", client.ID, "client-1")
	}
	if client.ServerID != ServerGopls {
		t.Errorf("ServerID = %q, want %q", client.ServerID, ServerGopls)
	}
	if client.ProjectRoot != "/path/to/project" {
		t.Errorf("ProjectRoot = %q, want %q", client.ProjectRoot, "/path/to/project")
	}
	if !client.Capabilities.HoverProvider {
		t.Error("Capabilities.HoverProvider should be true")
	}
	if client.Status != StatusReady {
		t.Errorf("Status = %d, want %d", client.Status, StatusReady)
	}
}

// TestPositionFields verifies Position struct fields.
func TestPositionFields(t *testing.T) {
	pos := Position{Line: 42, Character: 10}

	if pos.Line != 42 {
		t.Errorf("Line = %d, want 42", pos.Line)
	}
	if pos.Character != 10 {
		t.Errorf("Character = %d, want 10", pos.Character)
	}
}

// TestRangeFields verifies Range struct fields.
func TestRangeFields(t *testing.T) {
	r := Range{
		Start: Position{Line: 10, Character: 0},
		End:   Position{Line: 15, Character: 25},
	}

	if r.Start.Line != 10 {
		t.Errorf("Start.Line = %d, want 10", r.Start.Line)
	}
	if r.Start.Character != 0 {
		t.Errorf("Start.Character = %d, want 0", r.Start.Character)
	}
	if r.End.Line != 15 {
		t.Errorf("End.Line = %d, want 15", r.End.Line)
	}
	if r.End.Character != 25 {
		t.Errorf("End.Character = %d, want 25", r.End.Character)
	}
}

// TestLocationFields verifies Location struct fields.
func TestLocationFields(t *testing.T) {
	loc := Location{
		URI: "file:///path/to/file.go",
		Range: Range{
			Start: Position{Line: 10, Character: 5},
			End:   Position{Line: 10, Character: 15},
		},
	}

	if loc.URI != "file:///path/to/file.go" {
		t.Errorf("URI = %q, want %q", loc.URI, "file:///path/to/file.go")
	}
	if loc.Range.Start.Line != 10 {
		t.Errorf("Range.Start.Line = %d, want 10", loc.Range.Start.Line)
	}
}
