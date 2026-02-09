package lua

import (
	"errors"
	"sync"
	"sync/atomic"

	rt "github.com/arnodel/golua/runtime"
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
	errHighlightsFull = errors.New("highlight store full")
	errNamespacesFull = errors.New("namespace store full")
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
	Apply func(attrs *HighlightAttrs, val rt.Value)
}

// hlAttrFields is the table-driven parser for highlight attribute tables.
var hlAttrFields = []hlAttrField{
	{Key: "fg", Apply: func(a *HighlightAttrs, v rt.Value) {
		if s, ok := v.TryString(); ok {
			a.Foreground = s
		}
	}},
	{Key: "bg", Apply: func(a *HighlightAttrs, v rt.Value) {
		if s, ok := v.TryString(); ok {
			a.Background = s
		}
	}},
	{Key: "bold", Apply: func(a *HighlightAttrs, v rt.Value) { a.Bold = luaToBool(v) }},
	{Key: "italic", Apply: func(a *HighlightAttrs, v rt.Value) { a.Italic = luaToBool(v) }},
	{Key: "underline", Apply: func(a *HighlightAttrs, v rt.Value) { a.Underline = luaToBool(v) }},
	{Key: "strikethrough", Apply: func(a *HighlightAttrs, v rt.Value) { a.Strikethrough = luaToBool(v) }},
	{Key: "reverse", Apply: func(a *HighlightAttrs, v rt.Value) { a.Reverse = luaToBool(v) }},
	{Key: "link", Apply: func(a *HighlightAttrs, v rt.Value) {
		if s, ok := v.TryString(); ok {
			a.Link = s
		}
	}},
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
func registerHighlightFunctions(_ *rt.Runtime, apiTbl *rt.Table, luaRT *Runtime) {
	type registration struct {
		Name    string
		Handler func(*rt.Thread, *rt.GoCont) (rt.Cont, error)
	}
	regs := []registration{
		{Name: "nvim_set_hl", Handler: func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			return luaSetHL(t, c, luaRT)
		}},
		{Name: "nvim_get_hl", Handler: func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			return luaGetHL(t, c, luaRT)
		}},
		{Name: "nvim_create_namespace", Handler: func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			return luaCreateNamespace(t, c, luaRT)
		}},
	}
	for _, r := range regs {
		setGoFunc(apiTbl, r.Name, r.Handler, defaultMaxArgs, true)
	}
}

// luaSetHL implements nvim_set_hl(ns, name, val).
func luaSetHL(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	ns, _ := c.IntArg(0)
	name, _ := c.StringArg(1)
	attrs := HighlightAttrs{}
	valTblArg := c.Arg(2)
	if valTbl, ok := valTblArg.TryTable(); ok {
		parseHLAttrs(&attrs, valTbl)
	}
	_ = luaRT.Highlights.SetHL(int(ns), name, attrs)
	return c.Next(), nil
}

// parseHLAttrs walks hlAttrFields to populate attrs from a Lua table.
func parseHLAttrs(attrs *HighlightAttrs, tbl *rt.Table) {
	for _, f := range hlAttrFields {
		val := tbl.Get(rt.StringValue(f.Key))
		if !val.IsNil() {
			f.Apply(attrs, val)
		}
	}
}

// luaGetHL implements nvim_get_hl(ns, opts) where opts.name is the group.
func luaGetHL(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	ns, _ := c.IntArg(0)
	name := ""
	optsVal := c.Arg(1)
	if opts, ok := optsVal.TryTable(); ok {
		val := opts.Get(rt.StringValue("name"))
		if s, sOk := val.TryString(); sOk {
			name = s
		}
	}
	attrs, ok := luaRT.Highlights.GetHL(int(ns), name)
	if !ok {
		return c.PushingNext1(t.Runtime, rt.TableValue(rt.NewTable())), nil
	}
	return c.PushingNext1(t.Runtime, rt.TableValue(hlAttrsToTable(attrs))), nil
}

// hlAttrsToTable serialises HighlightAttrs into a Lua table.
func hlAttrsToTable(attrs HighlightAttrs) *rt.Table {
	tbl := rt.NewTable()
	type field struct {
		Key string
		Val rt.Value
	}
	fields := []field{
		{Key: "fg", Val: rt.StringValue(attrs.Foreground)},
		{Key: "bg", Val: rt.StringValue(attrs.Background)},
		{Key: "bold", Val: rt.BoolValue(attrs.Bold)},
		{Key: "italic", Val: rt.BoolValue(attrs.Italic)},
		{Key: "underline", Val: rt.BoolValue(attrs.Underline)},
		{Key: "strikethrough", Val: rt.BoolValue(attrs.Strikethrough)},
		{Key: "reverse", Val: rt.BoolValue(attrs.Reverse)},
		{Key: "link", Val: rt.StringValue(attrs.Link)},
	}
	for _, f := range fields {
		tbl.Set(rt.StringValue(f.Key), f.Val)
	}
	return tbl
}

// luaCreateNamespace implements nvim_create_namespace(name).
func luaCreateNamespace(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	name, _ := c.StringArg(0)
	id, err := luaRT.Highlights.CreateNamespace(name)
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(-1)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(id))), nil
}
