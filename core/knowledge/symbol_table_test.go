package knowledge

import (
	"sync"
	"testing"
)

// =============================================================================
// Test Helpers
// =============================================================================

func makeTestEntity(name string, kind EntityKind) *ExtractedEntity {
	return &ExtractedEntity{
		Name:       name,
		Kind:       kind,
		FilePath:   "/test/file.go",
		StartLine:  1,
		EndLine:    10,
		Scope:      ScopeGlobal,
		Visibility: VisibilityPublic,
	}
}

// =============================================================================
// Scope Tests
// =============================================================================

func TestNewScope(t *testing.T) {
	scope := NewScope("test", nil)

	if scope.Name != "test" {
		t.Errorf("NewScope().Name = %q, want %q", scope.Name, "test")
	}
	if scope.Parent != nil {
		t.Errorf("NewScope().Parent = %v, want nil", scope.Parent)
	}
	if scope.Symbols == nil {
		t.Error("NewScope().Symbols should not be nil")
	}
	if len(scope.Symbols) != 0 {
		t.Errorf("NewScope().Symbols length = %d, want 0", len(scope.Symbols))
	}
	if scope.Children == nil {
		t.Error("NewScope().Children should not be nil")
	}
	if len(scope.Children) != 0 {
		t.Errorf("NewScope().Children length = %d, want 0", len(scope.Children))
	}
}

func TestNewScope_WithParent(t *testing.T) {
	parent := NewScope("parent", nil)
	child := NewScope("child", parent)

	if child.Parent != parent {
		t.Error("child.Parent should point to parent")
	}
	if len(parent.Children) != 1 {
		t.Errorf("parent.Children length = %d, want 1", len(parent.Children))
	}
	if parent.Children[0] != child {
		t.Error("parent.Children[0] should point to child")
	}
}

func TestScope_Define(t *testing.T) {
	scope := NewScope("test", nil)
	entity := makeTestEntity("foo", EntityKindFunction)

	err := scope.Define("foo", entity)
	if err != nil {
		t.Errorf("Define() error = %v, want nil", err)
	}

	if scope.Symbols["foo"] != entity {
		t.Error("Define() did not store entity correctly")
	}
}

func TestScope_Define_InvalidName(t *testing.T) {
	scope := NewScope("test", nil)
	entity := makeTestEntity("foo", EntityKindFunction)

	err := scope.Define("", entity)
	if err != ErrInvalidSymbolName {
		t.Errorf("Define(\"\") error = %v, want %v", err, ErrInvalidSymbolName)
	}
}

func TestScope_Define_NilEntity(t *testing.T) {
	scope := NewScope("test", nil)

	err := scope.Define("foo", nil)
	if err != ErrNilEntity {
		t.Errorf("Define(nil) error = %v, want %v", err, ErrNilEntity)
	}
}

func TestScope_Define_AlreadyDefined(t *testing.T) {
	scope := NewScope("test", nil)
	entity1 := makeTestEntity("foo", EntityKindFunction)
	entity2 := makeTestEntity("foo", EntityKindVariable)

	if err := scope.Define("foo", entity1); err != nil {
		t.Fatalf("First Define() error = %v", err)
	}

	err := scope.Define("foo", entity2)
	if err != ErrSymbolAlreadyDefined {
		t.Errorf("Second Define() error = %v, want %v", err, ErrSymbolAlreadyDefined)
	}
}

func TestScope_Lookup(t *testing.T) {
	scope := NewScope("test", nil)
	entity := makeTestEntity("foo", EntityKindFunction)
	scope.Define("foo", entity)

	found, ok := scope.Lookup("foo")
	if !ok {
		t.Error("Lookup(\"foo\") ok = false, want true")
	}
	if found != entity {
		t.Error("Lookup(\"foo\") returned wrong entity")
	}
}

func TestScope_Lookup_NotFound(t *testing.T) {
	scope := NewScope("test", nil)

	found, ok := scope.Lookup("nonexistent")
	if ok {
		t.Error("Lookup(\"nonexistent\") ok = true, want false")
	}
	if found != nil {
		t.Error("Lookup(\"nonexistent\") should return nil")
	}
}

func TestScope_Resolve(t *testing.T) {
	parent := NewScope("parent", nil)
	child := NewScope("child", parent)

	parentEntity := makeTestEntity("parentVar", EntityKindVariable)
	childEntity := makeTestEntity("childVar", EntityKindVariable)

	parent.Define("parentVar", parentEntity)
	child.Define("childVar", childEntity)

	// Child should resolve its own symbol
	found, ok := child.Resolve("childVar")
	if !ok || found != childEntity {
		t.Error("child.Resolve(\"childVar\") failed")
	}

	// Child should resolve parent's symbol
	found, ok = child.Resolve("parentVar")
	if !ok || found != parentEntity {
		t.Error("child.Resolve(\"parentVar\") failed to find parent symbol")
	}

	// Parent should not resolve child's symbol
	found, ok = parent.Resolve("childVar")
	if ok {
		t.Error("parent.Resolve(\"childVar\") should fail")
	}
}

func TestScope_Resolve_Shadowing(t *testing.T) {
	parent := NewScope("parent", nil)
	child := NewScope("child", parent)

	parentEntity := makeTestEntity("x", EntityKindVariable)
	parentEntity.Signature = "parent version"
	childEntity := makeTestEntity("x", EntityKindVariable)
	childEntity.Signature = "child version"

	parent.Define("x", parentEntity)
	child.Define("x", childEntity)

	// Child should resolve to child's version (shadowing)
	found, ok := child.Resolve("x")
	if !ok {
		t.Error("child.Resolve(\"x\") ok = false")
	}
	if found.Signature != "child version" {
		t.Errorf("child.Resolve(\"x\") returned parent version instead of child (shadowing failed)")
	}

	// Parent should still resolve to parent's version
	found, ok = parent.Resolve("x")
	if !ok {
		t.Error("parent.Resolve(\"x\") ok = false")
	}
	if found.Signature != "parent version" {
		t.Error("parent.Resolve(\"x\") returned wrong version")
	}
}

func TestScope_Path(t *testing.T) {
	root := NewScope("global", nil)
	pkg := NewScope("mypackage", root)
	fn := NewScope("myfunction", pkg)
	block := NewScope("block1", fn)

	tests := []struct {
		scope    *Scope
		expected string
	}{
		{root, "global"},
		{pkg, "global.mypackage"},
		{fn, "global.mypackage.myfunction"},
		{block, "global.mypackage.myfunction.block1"},
	}

	for _, tt := range tests {
		if got := tt.scope.Path(); got != tt.expected {
			t.Errorf("scope.Path() = %q, want %q", got, tt.expected)
		}
	}
}

func TestScope_Depth(t *testing.T) {
	root := NewScope("global", nil)
	level1 := NewScope("level1", root)
	level2 := NewScope("level2", level1)
	level3 := NewScope("level3", level2)

	tests := []struct {
		scope    *Scope
		expected int
	}{
		{root, 0},
		{level1, 1},
		{level2, 2},
		{level3, 3},
	}

	for _, tt := range tests {
		if got := tt.scope.Depth(); got != tt.expected {
			t.Errorf("scope.Depth() = %d, want %d", got, tt.expected)
		}
	}
}

func TestScope_AllSymbols(t *testing.T) {
	parent := NewScope("parent", nil)
	child := NewScope("child", parent)

	entity1 := makeTestEntity("a", EntityKindVariable)
	entity2 := makeTestEntity("b", EntityKindVariable)
	entity3 := makeTestEntity("c", EntityKindVariable)
	entity4 := makeTestEntity("a", EntityKindFunction) // shadows parent's "a"

	parent.Define("a", entity1)
	parent.Define("b", entity2)
	child.Define("c", entity3)
	child.Define("a", entity4) // shadows parent's "a"

	symbols := child.AllSymbols()

	if len(symbols) != 3 {
		t.Errorf("AllSymbols() length = %d, want 3", len(symbols))
	}

	// "a" should be child's version (shadowing)
	if symbols["a"].Kind != EntityKindFunction {
		t.Error("AllSymbols()[\"a\"] should be child's version (function)")
	}

	// "b" should be parent's
	if symbols["b"] != entity2 {
		t.Error("AllSymbols()[\"b\"] should be parent's entity")
	}

	// "c" should be child's
	if symbols["c"] != entity3 {
		t.Error("AllSymbols()[\"c\"] should be child's entity")
	}
}

// =============================================================================
// SymbolTable Tests
// =============================================================================

func TestNewSymbolTable(t *testing.T) {
	st := NewSymbolTable()

	if st.RootScope == nil {
		t.Error("NewSymbolTable().RootScope should not be nil")
	}
	if st.CurrentScope == nil {
		t.Error("NewSymbolTable().CurrentScope should not be nil")
	}
	if st.CurrentScope != st.RootScope {
		t.Error("NewSymbolTable().CurrentScope should equal RootScope")
	}
	if st.RootScope.Name != "global" {
		t.Errorf("RootScope.Name = %q, want %q", st.RootScope.Name, "global")
	}
	if len(st.Scopes) != 1 {
		t.Errorf("len(Scopes) = %d, want 1", len(st.Scopes))
	}
}

func TestSymbolTable_Define(t *testing.T) {
	st := NewSymbolTable()
	entity := makeTestEntity("foo", EntityKindFunction)

	err := st.Define("foo", entity)
	if err != nil {
		t.Errorf("Define() error = %v", err)
	}

	if st.RootScope.Symbols["foo"] != entity {
		t.Error("Define() did not store entity in current scope")
	}
}

func TestSymbolTable_Define_Error(t *testing.T) {
	st := NewSymbolTable()
	entity := makeTestEntity("foo", EntityKindFunction)

	st.Define("foo", entity)
	err := st.Define("foo", entity)
	if err != ErrSymbolAlreadyDefined {
		t.Errorf("Define() duplicate error = %v, want %v", err, ErrSymbolAlreadyDefined)
	}
}

func TestSymbolTable_Resolve(t *testing.T) {
	st := NewSymbolTable()
	entity := makeTestEntity("foo", EntityKindFunction)
	st.Define("foo", entity)

	found, ok := st.Resolve("foo")
	if !ok {
		t.Error("Resolve(\"foo\") ok = false, want true")
	}
	if found != entity {
		t.Error("Resolve(\"foo\") returned wrong entity")
	}
}

func TestSymbolTable_Resolve_NotFound(t *testing.T) {
	st := NewSymbolTable()

	found, ok := st.Resolve("nonexistent")
	if ok {
		t.Error("Resolve(\"nonexistent\") ok = true, want false")
	}
	if found != nil {
		t.Error("Resolve(\"nonexistent\") should return nil")
	}
}

func TestSymbolTable_EnterScope(t *testing.T) {
	st := NewSymbolTable()

	child := st.EnterScope("child1")

	if child == nil {
		t.Fatal("EnterScope() returned nil")
	}
	if child.Name != "child1" {
		t.Errorf("EnterScope().Name = %q, want %q", child.Name, "child1")
	}
	if child.Parent != st.RootScope {
		t.Error("EnterScope().Parent should be RootScope")
	}
	if st.CurrentScope != child {
		t.Error("CurrentScope should be updated to child")
	}
	if len(st.Scopes) != 2 {
		t.Errorf("len(Scopes) = %d, want 2", len(st.Scopes))
	}
}

func TestSymbolTable_EnterScope_Nested(t *testing.T) {
	st := NewSymbolTable()

	level1 := st.EnterScope("level1")
	level2 := st.EnterScope("level2")
	level3 := st.EnterScope("level3")

	if st.CurrentScope != level3 {
		t.Error("CurrentScope should be level3")
	}
	if level3.Parent != level2 {
		t.Error("level3.Parent should be level2")
	}
	if level2.Parent != level1 {
		t.Error("level2.Parent should be level1")
	}
	if level1.Parent != st.RootScope {
		t.Error("level1.Parent should be RootScope")
	}
	if len(st.Scopes) != 4 {
		t.Errorf("len(Scopes) = %d, want 4", len(st.Scopes))
	}
}

func TestSymbolTable_ExitScope(t *testing.T) {
	st := NewSymbolTable()
	st.EnterScope("child")

	parent := st.ExitScope()

	if parent != st.RootScope {
		t.Error("ExitScope() should return RootScope")
	}
	if st.CurrentScope != st.RootScope {
		t.Error("CurrentScope should be RootScope after ExitScope()")
	}
}

func TestSymbolTable_ExitScope_AtRoot(t *testing.T) {
	st := NewSymbolTable()

	result := st.ExitScope()

	if result != nil {
		t.Error("ExitScope() at root should return nil")
	}
	if st.CurrentScope != st.RootScope {
		t.Error("CurrentScope should remain RootScope")
	}
}

func TestSymbolTable_EnterExitScope_DefineResolve(t *testing.T) {
	st := NewSymbolTable()

	// Define in root
	rootEntity := makeTestEntity("rootVar", EntityKindVariable)
	st.Define("rootVar", rootEntity)

	// Enter child scope
	st.EnterScope("child")

	// Can resolve parent's symbol
	found, ok := st.Resolve("rootVar")
	if !ok || found != rootEntity {
		t.Error("Should resolve parent symbol from child scope")
	}

	// Define in child
	childEntity := makeTestEntity("childVar", EntityKindVariable)
	st.Define("childVar", childEntity)

	// Can resolve child's symbol
	found, ok = st.Resolve("childVar")
	if !ok || found != childEntity {
		t.Error("Should resolve child symbol")
	}

	// Exit child scope
	st.ExitScope()

	// Cannot resolve child's symbol from parent
	_, ok = st.Resolve("childVar")
	if ok {
		t.Error("Should not resolve child symbol from parent scope")
	}

	// Can still resolve root's symbol
	found, ok = st.Resolve("rootVar")
	if !ok || found != rootEntity {
		t.Error("Should still resolve root symbol")
	}
}

func TestSymbolTable_Shadowing(t *testing.T) {
	st := NewSymbolTable()

	// Define x in root
	rootX := makeTestEntity("x", EntityKindVariable)
	rootX.Signature = "root x"
	st.Define("x", rootX)

	// Enter child and define x again
	st.EnterScope("child")
	childX := makeTestEntity("x", EntityKindVariable)
	childX.Signature = "child x"
	st.Define("x", childX)

	// Resolve should return child's x
	found, ok := st.Resolve("x")
	if !ok {
		t.Error("Resolve(\"x\") ok = false")
	}
	if found.Signature != "child x" {
		t.Error("Resolve(\"x\") should return child's version (shadowing)")
	}

	// Exit child
	st.ExitScope()

	// Now should resolve to root's x
	found, ok = st.Resolve("x")
	if !ok {
		t.Error("Resolve(\"x\") ok = false after ExitScope")
	}
	if found.Signature != "root x" {
		t.Error("Resolve(\"x\") should return root's version after ExitScope")
	}
}

func TestSymbolTable_ResolveQualified(t *testing.T) {
	st := NewSymbolTable()

	// Create package scope
	st.EnterScope("mypackage")
	pkgEntity := makeTestEntity("MyFunc", EntityKindFunction)
	st.Define("MyFunc", pkgEntity)
	st.ExitScope()

	// Create another package
	st.EnterScope("otherpackage")
	otherEntity := makeTestEntity("OtherFunc", EntityKindFunction)
	st.Define("OtherFunc", otherEntity)
	st.ExitScope()

	// Resolve qualified
	found, ok := st.ResolveQualified("mypackage", "MyFunc")
	if !ok {
		t.Error("ResolveQualified(\"mypackage\", \"MyFunc\") ok = false")
	}
	if found != pkgEntity {
		t.Error("ResolveQualified returned wrong entity")
	}

	// Resolve other package
	found, ok = st.ResolveQualified("otherpackage", "OtherFunc")
	if !ok {
		t.Error("ResolveQualified(\"otherpackage\", \"OtherFunc\") ok = false")
	}
	if found != otherEntity {
		t.Error("ResolveQualified returned wrong entity for otherpackage")
	}

	// Should not resolve wrong combination
	_, ok = st.ResolveQualified("mypackage", "OtherFunc")
	if ok {
		t.Error("ResolveQualified should not find OtherFunc in mypackage")
	}
}

func TestSymbolTable_ResolveQualified_NotFound(t *testing.T) {
	st := NewSymbolTable()

	_, ok := st.ResolveQualified("nonexistent", "Func")
	if ok {
		t.Error("ResolveQualified for nonexistent package should fail")
	}
}

func TestSymbolTable_GetScope(t *testing.T) {
	st := NewSymbolTable()
	st.EnterScope("pkg")
	st.EnterScope("func")
	st.ExitScope()
	st.ExitScope()

	// Get root scope
	scope := st.GetScope("global")
	if scope != st.RootScope {
		t.Error("GetScope(\"global\") should return RootScope")
	}

	// Get nested scope
	scope = st.GetScope("global.pkg.func")
	if scope == nil {
		t.Error("GetScope(\"global.pkg.func\") should not be nil")
	}
	if scope.Name != "func" {
		t.Errorf("GetScope().Name = %q, want %q", scope.Name, "func")
	}

	// Get nonexistent scope
	scope = st.GetScope("nonexistent")
	if scope != nil {
		t.Error("GetScope(\"nonexistent\") should return nil")
	}
}

func TestSymbolTable_DefineInScope(t *testing.T) {
	st := NewSymbolTable()
	st.EnterScope("pkg")
	st.ExitScope()

	entity := makeTestEntity("MyVar", EntityKindVariable)
	err := st.DefineInScope("global.pkg", "MyVar", entity)
	if err != nil {
		t.Errorf("DefineInScope() error = %v", err)
	}

	// Verify it was defined
	scope := st.GetScope("global.pkg")
	if scope.Symbols["MyVar"] != entity {
		t.Error("DefineInScope did not store entity")
	}
}

func TestSymbolTable_DefineInScope_NotFound(t *testing.T) {
	st := NewSymbolTable()
	entity := makeTestEntity("MyVar", EntityKindVariable)

	err := st.DefineInScope("nonexistent", "MyVar", entity)
	if err == nil {
		t.Error("DefineInScope for nonexistent scope should fail")
	}
}

func TestSymbolTable_ResolveInScope(t *testing.T) {
	st := NewSymbolTable()

	// Create a scope hierarchy
	st.EnterScope("pkg")
	pkgEntity := makeTestEntity("pkgVar", EntityKindVariable)
	st.Define("pkgVar", pkgEntity)

	st.EnterScope("func")
	funcEntity := makeTestEntity("funcVar", EntityKindVariable)
	st.Define("funcVar", funcEntity)
	st.ExitScope()
	st.ExitScope()

	// ResolveInScope from func scope should walk up to pkg
	found, ok := st.ResolveInScope("global.pkg.func", "pkgVar")
	if !ok {
		t.Error("ResolveInScope should find pkgVar from func scope")
	}
	if found != pkgEntity {
		t.Error("ResolveInScope returned wrong entity")
	}

	// ResolveInScope from pkg should not find funcVar
	_, ok = st.ResolveInScope("global.pkg", "funcVar")
	if ok {
		t.Error("ResolveInScope should not find funcVar from pkg scope")
	}
}

func TestSymbolTable_AllScopes(t *testing.T) {
	st := NewSymbolTable()
	st.EnterScope("a")
	st.EnterScope("b")
	st.ExitScope()
	st.ExitScope()
	st.EnterScope("c")
	st.ExitScope()

	paths := st.AllScopes()
	if len(paths) != 4 {
		t.Errorf("AllScopes() length = %d, want 4", len(paths))
	}

	expected := map[string]bool{
		"global":       true,
		"global.a":     true,
		"global.a.b":   true,
		"global.c":     true,
	}
	for _, path := range paths {
		if !expected[path] {
			t.Errorf("Unexpected scope path: %q", path)
		}
	}
}

func TestSymbolTable_ScopeCount(t *testing.T) {
	st := NewSymbolTable()
	if st.ScopeCount() != 1 {
		t.Errorf("Initial ScopeCount() = %d, want 1", st.ScopeCount())
	}

	st.EnterScope("a")
	st.EnterScope("b")
	st.ExitScope()
	st.ExitScope()

	if st.ScopeCount() != 3 {
		t.Errorf("ScopeCount() = %d, want 3", st.ScopeCount())
	}
}

func TestSymbolTable_SymbolCount(t *testing.T) {
	st := NewSymbolTable()
	if st.SymbolCount() != 0 {
		t.Errorf("Initial SymbolCount() = %d, want 0", st.SymbolCount())
	}

	st.Define("a", makeTestEntity("a", EntityKindVariable))
	st.Define("b", makeTestEntity("b", EntityKindVariable))

	st.EnterScope("child")
	st.Define("c", makeTestEntity("c", EntityKindVariable))
	st.ExitScope()

	if st.SymbolCount() != 3 {
		t.Errorf("SymbolCount() = %d, want 3", st.SymbolCount())
	}
}

func TestSymbolTable_Reset(t *testing.T) {
	st := NewSymbolTable()
	st.Define("foo", makeTestEntity("foo", EntityKindFunction))
	st.EnterScope("child")
	st.Define("bar", makeTestEntity("bar", EntityKindVariable))

	st.Reset()

	if st.ScopeCount() != 1 {
		t.Errorf("ScopeCount() after Reset = %d, want 1", st.ScopeCount())
	}
	if st.SymbolCount() != 0 {
		t.Errorf("SymbolCount() after Reset = %d, want 0", st.SymbolCount())
	}
	if st.CurrentScope != st.RootScope {
		t.Error("CurrentScope should be RootScope after Reset")
	}
	if st.RootScope.Name != "global" {
		t.Errorf("RootScope.Name = %q, want %q", st.RootScope.Name, "global")
	}
}

func TestSymbolTable_CurrentPath(t *testing.T) {
	st := NewSymbolTable()

	if st.CurrentPath() != "global" {
		t.Errorf("Initial CurrentPath() = %q, want %q", st.CurrentPath(), "global")
	}

	st.EnterScope("pkg")
	if st.CurrentPath() != "global.pkg" {
		t.Errorf("CurrentPath() = %q, want %q", st.CurrentPath(), "global.pkg")
	}

	st.EnterScope("func")
	if st.CurrentPath() != "global.pkg.func" {
		t.Errorf("CurrentPath() = %q, want %q", st.CurrentPath(), "global.pkg.func")
	}
}

func TestSymbolTable_Depth(t *testing.T) {
	st := NewSymbolTable()

	if st.Depth() != 0 {
		t.Errorf("Initial Depth() = %d, want 0", st.Depth())
	}

	st.EnterScope("a")
	if st.Depth() != 1 {
		t.Errorf("Depth() = %d, want 1", st.Depth())
	}

	st.EnterScope("b")
	if st.Depth() != 2 {
		t.Errorf("Depth() = %d, want 2", st.Depth())
	}

	st.ExitScope()
	if st.Depth() != 1 {
		t.Errorf("Depth() after ExitScope = %d, want 1", st.Depth())
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestSymbolTable_ConcurrentDefine(t *testing.T) {
	st := NewSymbolTable()
	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Launch 100 goroutines defining symbols
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := string(rune('a' + (idx % 26)))
			entity := makeTestEntity(name, EntityKindVariable)
			// Errors are expected for duplicate names
			_ = st.Define(name, entity)
		}(i)
	}

	wg.Wait()
	close(errors)

	// Should have at most 26 symbols (a-z)
	if st.SymbolCount() > 26 {
		t.Errorf("SymbolCount() = %d, want <= 26", st.SymbolCount())
	}
}

func TestSymbolTable_ConcurrentEnterExit(t *testing.T) {
	st := NewSymbolTable()
	var wg sync.WaitGroup

	// Launch multiple goroutines entering and exiting scopes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			st.EnterScope("scope")
			st.ExitScope()
		}(i)
	}

	wg.Wait()

	// Should not panic and table should still be usable
	st.Define("test", makeTestEntity("test", EntityKindFunction))
	if _, ok := st.Resolve("test"); !ok {
		t.Error("Symbol table should still be usable after concurrent operations")
	}
}

func TestSymbolTable_ConcurrentResolve(t *testing.T) {
	st := NewSymbolTable()
	entity := makeTestEntity("shared", EntityKindVariable)
	st.Define("shared", entity)

	var wg sync.WaitGroup

	// Launch multiple goroutines resolving the same symbol
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found, ok := st.Resolve("shared")
			if !ok || found != entity {
				t.Error("Concurrent Resolve failed")
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestSymbolTable_DeepNesting(t *testing.T) {
	st := NewSymbolTable()
	depth := 100

	// Create deeply nested scopes
	for i := 0; i < depth; i++ {
		st.EnterScope("level")
	}

	// Define at deepest level
	entity := makeTestEntity("deep", EntityKindVariable)
	st.Define("deep", entity)

	// Should resolve
	found, ok := st.Resolve("deep")
	if !ok || found != entity {
		t.Error("Failed to resolve at deep nesting level")
	}

	// Exit all scopes
	for i := 0; i < depth; i++ {
		st.ExitScope()
	}

	if st.CurrentScope != st.RootScope {
		t.Error("Should be at RootScope after exiting all nested scopes")
	}
}

func TestSymbolTable_EmptyScopeName(t *testing.T) {
	st := NewSymbolTable()

	// Empty scope names should work but path will have double dots
	scope := st.EnterScope("")
	if scope == nil {
		t.Error("EnterScope(\"\") should not return nil")
	}

	// Define and resolve should still work
	entity := makeTestEntity("x", EntityKindVariable)
	st.Define("x", entity)

	found, ok := st.Resolve("x")
	if !ok || found != entity {
		t.Error("Should resolve symbol in empty-named scope")
	}
}

func TestSymbolTable_SpecialCharactersInName(t *testing.T) {
	st := NewSymbolTable()

	specialNames := []string{
		"_private",
		"__dunder__",
		"$special",
		"name.with.dots",
		"name-with-dashes",
		"name123",
	}

	for _, name := range specialNames {
		entity := makeTestEntity(name, EntityKindVariable)
		err := st.Define(name, entity)
		if err != nil {
			t.Errorf("Define(%q) error = %v", name, err)
		}

		found, ok := st.Resolve(name)
		if !ok {
			t.Errorf("Resolve(%q) ok = false", name)
		}
		if found != entity {
			t.Errorf("Resolve(%q) returned wrong entity", name)
		}
	}
}

func TestSymbolTable_ResolveQualified_FullPath(t *testing.T) {
	st := NewSymbolTable()

	// Create nested package structure
	st.EnterScope("pkg")
	st.EnterScope("subpkg")
	entity := makeTestEntity("Func", EntityKindFunction)
	st.Define("Func", entity)
	st.ExitScope()
	st.ExitScope()

	// Should be able to resolve using full path
	found, ok := st.ResolveQualified("global.pkg.subpkg", "Func")
	if !ok {
		t.Error("ResolveQualified with full path should work")
	}
	if found != entity {
		t.Error("ResolveQualified returned wrong entity")
	}
}

func TestSymbolTable_MultipleChildrenSameLevel(t *testing.T) {
	st := NewSymbolTable()

	// Create multiple children at same level
	st.EnterScope("a")
	st.Define("varA", makeTestEntity("varA", EntityKindVariable))
	st.ExitScope()

	st.EnterScope("b")
	st.Define("varB", makeTestEntity("varB", EntityKindVariable))
	st.ExitScope()

	st.EnterScope("c")
	st.Define("varC", makeTestEntity("varC", EntityKindVariable))
	st.ExitScope()

	// Root should have 3 children
	if len(st.RootScope.Children) != 3 {
		t.Errorf("RootScope.Children length = %d, want 3", len(st.RootScope.Children))
	}

	// Should be able to access each scope
	for _, name := range []string{"a", "b", "c"} {
		scope := st.GetScope("global." + name)
		if scope == nil {
			t.Errorf("GetScope(\"global.%s\") = nil", name)
		}
	}
}
