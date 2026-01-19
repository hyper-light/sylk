package knowledge

import (
	"errors"
	"strings"
	"sync"
)

// =============================================================================
// Error Definitions
// =============================================================================

var (
	// ErrSymbolAlreadyDefined is returned when attempting to define a symbol
	// that already exists in the current scope.
	ErrSymbolAlreadyDefined = errors.New("symbol already defined in current scope")

	// ErrInvalidSymbolName is returned when a symbol name is empty or invalid.
	ErrInvalidSymbolName = errors.New("invalid symbol name")

	// ErrNilEntity is returned when attempting to define a nil entity.
	ErrNilEntity = errors.New("cannot define nil entity")

	// ErrNoParentScope is returned when attempting to exit the root scope.
	ErrNoParentScope = errors.New("cannot exit root scope: no parent scope")
)

// =============================================================================
// Scope Definition
// =============================================================================

// Scope represents a lexical scope in the symbol table hierarchy.
// Scopes form a tree structure where child scopes can shadow symbols
// from parent scopes. Resolution walks up the scope chain from the
// current scope to the root until a symbol is found.
type Scope struct {
	// Name is the identifier for this scope (e.g., function name, block label).
	Name string

	// Parent points to the enclosing scope. Nil for the root scope.
	Parent *Scope

	// Children contains all immediate child scopes.
	Children []*Scope

	// Symbols maps symbol names to their extracted entities within this scope.
	Symbols map[string]*ExtractedEntity
}

// NewScope creates a new scope with the given name and parent.
// The symbols map is initialized to an empty map.
func NewScope(name string, parent *Scope) *Scope {
	s := &Scope{
		Name:     name,
		Parent:   parent,
		Children: make([]*Scope, 0),
		Symbols:  make(map[string]*ExtractedEntity),
	}
	if parent != nil {
		parent.Children = append(parent.Children, s)
	}
	return s
}

// Define adds a symbol to this scope. Returns an error if the symbol
// already exists in this scope (not parent scopes - shadowing is allowed).
func (s *Scope) Define(name string, entity *ExtractedEntity) error {
	if name == "" {
		return ErrInvalidSymbolName
	}
	if entity == nil {
		return ErrNilEntity
	}
	if _, exists := s.Symbols[name]; exists {
		return ErrSymbolAlreadyDefined
	}
	s.Symbols[name] = entity
	return nil
}

// Lookup searches for a symbol in this scope only (not parent scopes).
func (s *Scope) Lookup(name string) (*ExtractedEntity, bool) {
	entity, found := s.Symbols[name]
	return entity, found
}

// Resolve searches for a symbol starting from this scope and walking
// up the parent chain until the symbol is found or the root is reached.
func (s *Scope) Resolve(name string) (*ExtractedEntity, bool) {
	current := s
	for current != nil {
		if entity, found := current.Symbols[name]; found {
			return entity, true
		}
		current = current.Parent
	}
	return nil, false
}

// Path returns the fully qualified path from the root to this scope,
// using "." as a separator (e.g., "pkg.func.block").
func (s *Scope) Path() string {
	if s == nil {
		return ""
	}
	if s.Parent == nil {
		return s.Name
	}

	// Build path from root to current
	parts := make([]string, 0)
	current := s
	for current != nil {
		if current.Name != "" {
			parts = append([]string{current.Name}, parts...)
		}
		current = current.Parent
	}
	return strings.Join(parts, ".")
}

// Depth returns the depth of this scope in the tree (root = 0).
func (s *Scope) Depth() int {
	depth := 0
	current := s
	for current.Parent != nil {
		depth++
		current = current.Parent
	}
	return depth
}

// AllSymbols returns all symbols visible from this scope, including
// those in parent scopes. Shadowed symbols are not included (inner
// scope symbols take precedence).
func (s *Scope) AllSymbols() map[string]*ExtractedEntity {
	result := make(map[string]*ExtractedEntity)

	// Walk from root to current, so inner scopes override outer
	scopes := make([]*Scope, 0)
	current := s
	for current != nil {
		scopes = append([]*Scope{current}, scopes...)
		current = current.Parent
	}

	for _, scope := range scopes {
		for name, entity := range scope.Symbols {
			result[name] = entity
		}
	}
	return result
}

// =============================================================================
// SymbolTable Definition
// =============================================================================

// SymbolTable manages a hierarchical collection of scopes for symbol resolution.
// It provides thread-safe operations for defining and resolving symbols across
// a scope tree, supporting features like shadowing and qualified name resolution.
type SymbolTable struct {
	// mu protects concurrent access to the symbol table.
	mu sync.RWMutex

	// Scopes maps scope paths to scope instances for O(1) lookup.
	Scopes map[string]*Scope

	// CurrentScope points to the active scope where new symbols are defined.
	CurrentScope *Scope

	// RootScope is the top-level global scope.
	RootScope *Scope
}

// NewSymbolTable creates a new symbol table with an initialized root scope.
// The root scope has the name "global" and serves as the top of the scope hierarchy.
func NewSymbolTable() *SymbolTable {
	root := NewScope("global", nil)
	return &SymbolTable{
		Scopes:       map[string]*Scope{"global": root},
		CurrentScope: root,
		RootScope:    root,
	}
}

// Define adds a symbol to the current scope. Returns an error if the symbol
// already exists in the current scope. Shadowing symbols from parent scopes
// is allowed.
func (st *SymbolTable) Define(name string, entity *ExtractedEntity) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.CurrentScope.Define(name, entity)
}

// Resolve searches for a symbol starting from the current scope and walking
// up the parent chain. Returns the entity and true if found, nil and false otherwise.
func (st *SymbolTable) Resolve(name string) (*ExtractedEntity, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	return st.CurrentScope.Resolve(name)
}

// EnterScope creates a new child scope under the current scope with the given name
// and makes it the current scope. Returns the newly created scope.
func (st *SymbolTable) EnterScope(name string) *Scope {
	st.mu.Lock()
	defer st.mu.Unlock()

	child := NewScope(name, st.CurrentScope)
	path := child.Path()
	st.Scopes[path] = child
	st.CurrentScope = child
	return child
}

// ExitScope returns to the parent scope. Returns the parent scope, or nil
// if already at the root scope. The current scope is updated to the parent.
func (st *SymbolTable) ExitScope() *Scope {
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.CurrentScope.Parent == nil {
		return nil
	}
	parent := st.CurrentScope.Parent
	st.CurrentScope = parent
	return parent
}

// ResolveQualified resolves a symbol using a package/namespace qualifier.
// It searches for a scope matching the package name and then looks for
// the symbol within that scope's direct symbols (not walking parents).
// This is useful for cross-package resolution where you want to access
// a symbol from a specific namespace.
func (st *SymbolTable) ResolveQualified(pkg, name string) (*ExtractedEntity, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	// First, try to find a scope with the exact package name as path
	if scope, exists := st.Scopes[pkg]; exists {
		if entity, found := scope.Lookup(name); found {
			return entity, true
		}
	}

	// Try to find a child scope of root with the package name
	for _, child := range st.RootScope.Children {
		if child.Name == pkg {
			if entity, found := child.Lookup(name); found {
				return entity, true
			}
		}
	}

	// Try to find any scope ending with the package name
	for path, scope := range st.Scopes {
		parts := strings.Split(path, ".")
		if len(parts) > 0 && parts[len(parts)-1] == pkg {
			if entity, found := scope.Lookup(name); found {
				return entity, true
			}
		}
	}

	return nil, false
}

// GetScope returns the scope at the given path, or nil if not found.
// The path uses "." as separator (e.g., "global.pkg.func").
func (st *SymbolTable) GetScope(path string) *Scope {
	st.mu.RLock()
	defer st.mu.RUnlock()

	if scope, exists := st.Scopes[path]; exists {
		return scope
	}
	return nil
}

// DefineInScope adds a symbol to a specific scope identified by path.
// Returns an error if the scope doesn't exist or the symbol already exists.
func (st *SymbolTable) DefineInScope(path, name string, entity *ExtractedEntity) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	scope, exists := st.Scopes[path]
	if !exists {
		return errors.New("scope not found: " + path)
	}
	return scope.Define(name, entity)
}

// ResolveInScope resolves a symbol starting from a specific scope.
// Useful for resolving references from a known context.
func (st *SymbolTable) ResolveInScope(path, name string) (*ExtractedEntity, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	scope, exists := st.Scopes[path]
	if !exists {
		return nil, false
	}
	return scope.Resolve(name)
}

// AllScopes returns a slice of all scope paths in the symbol table.
func (st *SymbolTable) AllScopes() []string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	paths := make([]string, 0, len(st.Scopes))
	for path := range st.Scopes {
		paths = append(paths, path)
	}
	return paths
}

// ScopeCount returns the total number of scopes in the symbol table.
func (st *SymbolTable) ScopeCount() int {
	st.mu.RLock()
	defer st.mu.RUnlock()

	return len(st.Scopes)
}

// SymbolCount returns the total number of symbols across all scopes.
func (st *SymbolTable) SymbolCount() int {
	st.mu.RLock()
	defer st.mu.RUnlock()

	count := 0
	for _, scope := range st.Scopes {
		count += len(scope.Symbols)
	}
	return count
}

// Reset clears all scopes and symbols, reinitializing with a fresh root scope.
func (st *SymbolTable) Reset() {
	st.mu.Lock()
	defer st.mu.Unlock()

	root := NewScope("global", nil)
	st.Scopes = map[string]*Scope{"global": root}
	st.CurrentScope = root
	st.RootScope = root
}

// CurrentPath returns the full path of the current scope.
func (st *SymbolTable) CurrentPath() string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	return st.CurrentScope.Path()
}

// Depth returns the depth of the current scope from the root.
func (st *SymbolTable) Depth() int {
	st.mu.RLock()
	defer st.mu.RUnlock()

	return st.CurrentScope.Depth()
}
