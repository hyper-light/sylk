package lua

import (
	"errors"
	"sync"
	"sync/atomic"

	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Capacity limits
// ---------------------------------------------------------------------------

// maxHighlightGroups caps the total number of highlight groups across all
// namespaces.
const maxHighlightGroups = 512

// maxNamespaces caps the total number of highlight namespaces.
const maxNamespaces = 64

// Sentinel errors.
var (
	errHighlightsFull  = errors.New("highlight store full")
	errNamespacesFull  = errors.New("namespace store full")
)

// ---------------------------------------------------------------------------
// HighlightAttrs
// ---------------------------------------------------------------------------

// HighlightAttrs describes the visual attributes of a highlight group.
type HighlightAttrs struct {
	Foreground    string
	Background    string
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
	Reverse       bool
	Link          string
}

// hlAttrField maps a Lua table key to a setter on HighlightAttrs.
type hlAttrField struct {
	Key   string
	Apply func(attrs *HighlightAttrs, val glua.LValue)
}

// hlAttrFields is the table-driven parser for highlight attribute tables.
var hlAttrFields = []hlAttrField{
	{Key: "fg", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Foreground = v.String() }},
	{Key: "bg", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Background = v.String() }},
	{Key: "bold", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Bold = luaToBool(v) }},
	{Key: "italic", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Italic = luaToBool(v) }},
	{Key: "underline", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Underline = luaToBool(v) }},
	{Key: "strikethrough", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Strikethrough = luaToBool(v) }},
	{Key: "reverse", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Reverse = luaToBool(v) }},
	{Key: "link", Apply: func(a *HighlightAttrs, v glua.LValue) { a.Link = v.String() }},
}

// ---------------------------------------------------------------------------
// hlKey
// ---------------------------------------------------------------------------

// hlKey uniquely identifies a highlight group within a namespace.
type hlKey struct {
	Namespace int
	Name      string
}

// ---------------------------------------------------------------------------
// HighlightStore
// ---------------------------------------------------------------------------

// HighlightStore manages highlight groups and namespaces.
type HighlightStore struct {
	mu         sync.RWMutex
	highlights map[hlKey]HighlightAttrs
	namespaces map[string]int
	nsSeq      atomic.Int64
}

// NewHighlightStore returns a ready-to-use store with the default
// namespace (0) pre-registered.
func NewHighlightStore() *HighlightStore {
	ns := make(map[string]int, maxNamespaces)
	ns[""] = 0 // default namespace
	return &HighlightStore{
		highlights: make(map[hlKey]HighlightAttrs, maxHighlightGroups),
		namespaces: ns,
	}
}

// SetHL sets the highlight attributes for a group within a namespace.
func (hs *HighlightStore) SetHL(ns int, name string, attrs HighlightAttrs) error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	key := hlKey{Namespace: ns, Name: name}
	_, exists := hs.highlights[key]
	if !exists && len(hs.highlights) >= maxHighlightGroups {
		return errHighlightsFull
	}
	hs.highlights[key] = attrs
	return nil
}

// GetHL retrieves highlight attributes for a group.
func (hs *HighlightStore) GetHL(ns int, name string) (HighlightAttrs, bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	attrs, ok := hs.highlights[hlKey{Namespace: ns, Name: name}]
	return attrs, ok
}

// CreateNamespace registers a new namespace and returns its ID.
func (hs *HighlightStore) CreateNamespace(name string) (int, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if id, ok := hs.namespaces[name]; ok {
		return id, nil
	}
	if len(hs.namespaces) >= maxNamespaces {
		return 0, errNamespacesFull
	}
	id := int(hs.nsSeq.Add(1))
	hs.namespaces[name] = id
	return id, nil
}

// ---------------------------------------------------------------------------
// Lua registration
// ---------------------------------------------------------------------------

// registerHighlightFunctions attaches highlight API functions to vim.api.
func registerHighlightFunctions(L *glua.LState, apiTbl *glua.LTable, rt *Runtime) {
	type registration struct {
		Name    string
		Handler func(*glua.LState) int
	}
	regs := []registration{
		{Name: "nvim_set_hl", Handler: func(ls *glua.LState) int { return luaSetHL(ls, rt) }},
		{Name: "nvim_get_hl", Handler: func(ls *glua.LState) int { return luaGetHL(ls, rt) }},
		{Name: "nvim_create_namespace", Handler: func(ls *glua.LState) int { return luaCreateNamespace(ls, rt) }},
	}
	for _, r := range regs {
		fn := r.Handler
		apiTbl.RawSetString(r.Name, L.NewFunction(func(ls *glua.LState) int {
			return fn(ls)
		}))
	}
}

// luaSetHL implements nvim_set_hl(ns, name, val).
func luaSetHL(L *glua.LState, rt *Runtime) int {
	ns := L.ToInt(1)
	name := L.ToString(2)
	attrs := HighlightAttrs{}
	if valTbl, ok := L.Get(3).(*glua.LTable); ok {
		parseHLAttrs(&attrs, valTbl)
	}
	_ = rt.Highlights.SetHL(ns, name, attrs)
	return 0
}

// parseHLAttrs walks hlAttrFields to populate attrs from a Lua table.
func parseHLAttrs(attrs *HighlightAttrs, tbl *glua.LTable) {
	for _, f := range hlAttrFields {
		val := tbl.RawGetString(f.Key)
		if val != glua.LNil {
			f.Apply(attrs, val)
		}
	}
}

// luaGetHL implements nvim_get_hl(ns, opts) where opts.name is the group.
func luaGetHL(L *glua.LState, rt *Runtime) int {
	ns := L.ToInt(1)
	name := ""
	if opts, ok := L.Get(2).(*glua.LTable); ok {
		val := opts.RawGetString("name")
		if val != glua.LNil {
			name = val.String()
		}
	}
	attrs, ok := rt.Highlights.GetHL(ns, name)
	if !ok {
		L.Push(L.NewTable())
		return 1
	}
	L.Push(hlAttrsToTable(L, attrs))
	return 1
}

// hlAttrsToTable serialises HighlightAttrs into a Lua table.
func hlAttrsToTable(L *glua.LState, attrs HighlightAttrs) *glua.LTable {
	tbl := L.NewTable()
	// Table-driven serialisation matching hlAttrFields order.
	type field struct {
		Key string
		Val glua.LValue
	}
	fields := []field{
		{Key: "fg", Val: glua.LString(attrs.Foreground)},
		{Key: "bg", Val: glua.LString(attrs.Background)},
		{Key: "bold", Val: glua.LBool(attrs.Bold)},
		{Key: "italic", Val: glua.LBool(attrs.Italic)},
		{Key: "underline", Val: glua.LBool(attrs.Underline)},
		{Key: "strikethrough", Val: glua.LBool(attrs.Strikethrough)},
		{Key: "reverse", Val: glua.LBool(attrs.Reverse)},
		{Key: "link", Val: glua.LString(attrs.Link)},
	}
	for _, f := range fields {
		tbl.RawSetString(f.Key, f.Val)
	}
	return tbl
}

// luaCreateNamespace implements nvim_create_namespace(name).
func luaCreateNamespace(L *glua.LState, rt *Runtime) int {
	name := L.ToString(1)
	id, err := rt.Highlights.CreateNamespace(name)
	if err != nil {
		L.Push(glua.LNumber(-1))
		return 1
	}
	L.Push(glua.LNumber(id))
	return 1
}
