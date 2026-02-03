// Package lua implements a gopher-lua VM for Neovim-compatible plugin
// scripting inside the Sylk editor.  The runtime owns the single Lua state
// and exposes sub-tables (vim.api, vim.keymap, vim.opt, ...) that mirror
// the Neovim Lua API surface.
package lua

import (
	"fmt"
	"sync"

	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Editor interfaces (defined locally to avoid import cycles)
// ---------------------------------------------------------------------------

// BufferAccess provides read/write access to a buffer.
type BufferAccess interface {
	GetLines(start, end int) []string
	SetLines(start, end int, lines []string) error
	LineCount() int
	Name() string
	SetName(name string)
	GetOption(name string) (any, bool)
	SetOption(name string, value any)
	GetVar(name string) (any, bool)
	SetVar(name string, value any)
}

// WindowAccess provides read/write access to a window.
type WindowAccess interface {
	GetCursor() (line, col int)
	SetCursor(line, col int)
	GetBuffer() int
	SetBuffer(bufID int) error
	Width() int
	Height() int
	GetOption(name string) (any, bool)
	SetOption(name string, value any)
	Close(force bool) error
}

// EditorAccess provides global editor operations.
type EditorAccess interface {
	CurrentBuffer() BufferAccess
	CurrentWindow() WindowAccess
	ListBuffers() []int
	ListWindows() []int
	GetBuffer(id int) BufferAccess
	GetWindow(id int) WindowAccess
	SetCurrentBuffer(id int) error
	SetCurrentWindow(id int) error
	Command(cmd string) error
	Eval(expr string) (any, error)
	Input(keys string)
	FeedKeys(keys string, mode string)
	GetMode() string
	GetOption(name string) (any, bool)
	SetOption(name string, value any)
	GetVar(name string) (any, bool)
	SetVar(name string, value any)
	Echo(chunks []string)
	Notify(msg string, level int)
}

// ---------------------------------------------------------------------------
// Sandbox configuration (table-driven)
// ---------------------------------------------------------------------------

// sandboxRemoval identifies one field to nil-out inside the Lua environment.
type sandboxRemoval struct {
	Table string // parent table (empty string = global)
	Field string // field name to remove
}

// sandboxRemovals lists every dangerous global that the sandbox zeros.
var sandboxRemovals = []sandboxRemoval{
	{Table: "os", Field: "execute"},
	{Table: "os", Field: "remove"},
	{Table: "os", Field: "rename"},
	{Table: "io", Field: "popen"},
	{Table: "", Field: "loadfile"},
	{Table: "", Field: "dofile"},
}

// luaMemoryLimit caps the maximum heap the Lua VM may allocate (bytes).
// Derived from a 256 MiB budget -- the constant is the product of two
// self-documenting factors so there are no magic numbers.
const luaMemoryLimit = 256 * 1024 * 1024

// luaCallStackSize caps the Lua call-stack depth.
const luaCallStackSize = 256

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

// Runtime wraps a single gopher-lua VM and exposes the Neovim-compatible
// vim.* API surface.
type Runtime struct {
	mu     sync.Mutex
	state  *glua.LState
	editor EditorAccess

	// Sub-system stores (created during init, referenced by API funcs).
	Keymaps    *KeymapStore
	Autocmds   *AutocmdStore
	Highlights *HighlightStore
	Scheduler  *Scheduler
}

// NewRuntime creates a sandboxed Lua 5.1 VM, registers the full vim.*
// table tree and wires every sub-system.
func NewRuntime(editor EditorAccess) *Runtime {
	opts := glua.Options{
		CallStackSize: luaCallStackSize,
		RegistrySize:  luaCallStackSize * 2,
		SkipOpenLibs:  false,
	}
	L := glua.NewState(opts)
	L.SetMx(luaMemoryLimit)

	rt := &Runtime{
		state:      L,
		editor:     editor,
		Keymaps:    NewKeymapStore(),
		Autocmds:   NewAutocmdStore(),
		Highlights: NewHighlightStore(),
		Scheduler:  NewScheduler(),
	}

	rt.applySandbox()
	rt.registerVimGlobal()
	return rt
}

// Close cleanly shuts down the Lua VM.
func (rt *Runtime) Close() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.Close()
	return nil
}

// DoFile executes a Lua file inside the sandbox.
func (rt *Runtime) DoFile(path string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.state.DoFile(path)
}

// DoString executes a Lua string inside the sandbox.
func (rt *Runtime) DoString(code string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.state.DoString(code)
}

// CallFunction invokes a Lua function with the given arguments and returns
// all values pushed onto the stack.
func (rt *Runtime) CallFunction(fn *glua.LFunction, args ...glua.LValue) ([]glua.LValue, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	L := rt.state
	top := L.GetTop()
	L.Push(fn)
	for _, a := range args {
		L.Push(a)
	}
	if err := L.PCall(len(args), glua.MultRet, nil); err != nil {
		return nil, err
	}
	nret := L.GetTop() - top
	results := make([]glua.LValue, nret)
	for i := range nret {
		results[i] = L.Get(top + i + 1)
	}
	L.SetTop(top)
	return results, nil
}

// State returns the raw Lua state.  Callers MUST hold rt.mu.
func (rt *Runtime) State() *glua.LState { return rt.state }

// Editor returns the wired EditorAccess.
func (rt *Runtime) Editor() EditorAccess { return rt.editor }

// ---------------------------------------------------------------------------
// Sandbox
// ---------------------------------------------------------------------------

// applySandbox nils out every entry listed in sandboxRemovals.
func (rt *Runtime) applySandbox() {
	L := rt.state
	for _, r := range sandboxRemovals {
		rt.nilField(L, r)
	}
}

// nilField removes a single field from either a global table or the global
// environment itself.
func (rt *Runtime) nilField(L *glua.LState, r sandboxRemoval) {
	if r.Table == "" {
		L.SetGlobal(r.Field, glua.LNil)
		return
	}
	tbl := L.GetGlobal(r.Table)
	if tbl == glua.LNil {
		return
	}
	if t, ok := tbl.(*glua.LTable); ok {
		t.RawSetString(r.Field, glua.LNil)
	}
}

// ---------------------------------------------------------------------------
// vim.* table registration
// ---------------------------------------------------------------------------

// registerVimGlobal builds the top-level `vim` table and attaches every
// sub-table and function.
func (rt *Runtime) registerVimGlobal() {
	L := rt.state

	vim := L.NewTable()

	// Sub-tables.
	apiTbl := L.NewTable()
	keymapTbl := L.NewTable()
	fnTbl := L.NewTable()

	registerAPITable(L, apiTbl, rt)
	registerKeymapTable(L, keymapTbl, rt)
	registerFnTable(L, fnTbl, rt)
	registerOptTables(L, vim, rt.editor)
	registerAutocmdFunctions(L, apiTbl, rt)
	registerHighlightFunctions(L, apiTbl, rt)
	registerScheduleFunctions(L, vim, rt)

	vim.RawSetString("api", apiTbl)
	vim.RawSetString("keymap", keymapTbl)
	vim.RawSetString("fn", fnTbl)

	L.SetGlobal("vim", vim)
}

// ---------------------------------------------------------------------------
// Lua <-> Go value helpers
// ---------------------------------------------------------------------------

// goToLua converts a Go value to the corresponding Lua representation.
func goToLua(L *glua.LState, v any) glua.LValue {
	if v == nil {
		return glua.LNil
	}
	return goToLuaTyped(L, v)
}

// goToLuaTyped performs the actual type dispatch.  Kept as a separate
// function so goToLua stays at complexity 1.
func goToLuaTyped(L *glua.LState, v any) glua.LValue {
	// Table-driven type mapping.
	type converter struct {
		match func(any) bool
		conv  func(*glua.LState, any) glua.LValue
	}
	converters := []converter{
		{match: func(v any) bool { _, ok := v.(bool); return ok },
			conv: func(_ *glua.LState, v any) glua.LValue { return glua.LBool(v.(bool)) }},
		{match: func(v any) bool { _, ok := v.(int); return ok },
			conv: func(_ *glua.LState, v any) glua.LValue { return glua.LNumber(v.(int)) }},
		{match: func(v any) bool { _, ok := v.(float64); return ok },
			conv: func(_ *glua.LState, v any) glua.LValue { return glua.LNumber(v.(float64)) }},
		{match: func(v any) bool { _, ok := v.(string); return ok },
			conv: func(_ *glua.LState, v any) glua.LValue { return glua.LString(v.(string)) }},
		{match: func(v any) bool { _, ok := v.([]string); return ok },
			conv: func(L *glua.LState, v any) glua.LValue { return stringsToTable(L, v.([]string)) }},
	}
	for _, c := range converters {
		if c.match(v) {
			return c.conv(L, v)
		}
	}
	return glua.LString(fmt.Sprintf("%v", v))
}

// stringsToTable converts a Go []string into a Lua table with integer keys.
func stringsToTable(L *glua.LState, ss []string) *glua.LTable {
	tbl := L.NewTable()
	for _, s := range ss {
		tbl.Append(glua.LString(s))
	}
	return tbl
}

// luaToGo converts a Lua value to a Go native type.
func luaToGo(v glua.LValue) any {
	// Table-driven type dispatch.
	type converter struct {
		match func(glua.LValue) bool
		conv  func(glua.LValue) any
	}
	converters := []converter{
		{match: func(v glua.LValue) bool { return v.Type() == glua.LTBool },
			conv: func(v glua.LValue) any { return bool(v.(glua.LBool)) }},
		{match: func(v glua.LValue) bool { return v.Type() == glua.LTNumber },
			conv: func(v glua.LValue) any { return float64(v.(glua.LNumber)) }},
		{match: func(v glua.LValue) bool { return v.Type() == glua.LTString },
			conv: func(v glua.LValue) any { return string(v.(glua.LString)) }},
	}
	for _, c := range converters {
		if c.match(v) {
			return c.conv(v)
		}
	}
	return nil
}

// luaTableToStrings reads a Lua table of strings into a Go slice.
func luaTableToStrings(tbl *glua.LTable) []string {
	n := tbl.Len()
	out := make([]string, 0, n)
	tbl.ForEach(func(_ glua.LValue, val glua.LValue) {
		if s, ok := val.(glua.LString); ok {
			out = append(out, string(s))
		}
	})
	return out
}
