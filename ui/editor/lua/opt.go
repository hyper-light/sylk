package lua

import (
	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Scope definitions (table-driven)
// ---------------------------------------------------------------------------

// optScope describes how to get/set values for a particular vim.* scope.
type optScope struct {
	Name   string
	Getter func(EditorAccess, *glua.LState, string) glua.LValue
	Setter func(EditorAccess, *glua.LState, string, glua.LValue)
}

// scopeTable is the authoritative mapping of Lua table name to scope
// get/set behaviour.  Every entry is wired into the vim global as a
// userdata with __index / __newindex metamethods.
var scopeTable = []optScope{
	{Name: "opt", Getter: globalOptGet, Setter: globalOptSet},
	{Name: "o", Getter: globalOptGet, Setter: globalOptSet},
	{Name: "bo", Getter: bufOptGet, Setter: bufOptSet},
	{Name: "wo", Getter: winOptGet, Setter: winOptSet},
	{Name: "g", Getter: globalVarGet, Setter: globalVarSet},
	{Name: "b", Getter: bufVarGet, Setter: bufVarSet},
	{Name: "w", Getter: winVarGet, Setter: winVarSet},
}

// registerOptTables creates userdata with metamethods for every scope
// entry and attaches them to the vim table.
func registerOptTables(L *glua.LState, vim *glua.LTable, editor EditorAccess) {
	for _, scope := range scopeTable {
		ud := newScopeUserdata(L, editor, scope)
		vim.RawSetString(scope.Name, ud)
	}
}

// newScopeUserdata builds a Lua userdata whose metamethods proxy reads
// and writes through the scope's getter/setter pair.
func newScopeUserdata(L *glua.LState, editor EditorAccess, scope optScope) *glua.LUserData {
	ud := L.NewUserData()
	mt := L.NewTable()
	get := scope.Getter // capture for closure
	set := scope.Setter
	mt.RawSetString("__index", L.NewFunction(func(ls *glua.LState) int {
		key := ls.ToString(2)
		ls.Push(get(editor, ls, key))
		return 1
	}))
	mt.RawSetString("__newindex", L.NewFunction(func(ls *glua.LState) int {
		key := ls.ToString(2)
		val := ls.Get(3)
		set(editor, ls, key, val)
		return 0
	}))
	L.SetMetatable(ud, mt)
	return ud
}

// ---------------------------------------------------------------------------
// Global options (vim.opt / vim.o)
// ---------------------------------------------------------------------------

func globalOptGet(editor EditorAccess, L *glua.LState, name string) glua.LValue {
	val, ok := editor.GetOption(name)
	if !ok {
		return glua.LNil
	}
	return goToLua(L, val)
}

func globalOptSet(editor EditorAccess, _ *glua.LState, name string, val glua.LValue) {
	editor.SetOption(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Buffer-local options (vim.bo)
// ---------------------------------------------------------------------------

func bufOptGet(editor EditorAccess, L *glua.LState, name string) glua.LValue {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return glua.LNil
	}
	val, ok := buf.GetOption(name)
	if !ok {
		return glua.LNil
	}
	return goToLua(L, val)
}

func bufOptSet(editor EditorAccess, _ *glua.LState, name string, val glua.LValue) {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return
	}
	buf.SetOption(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Window-local options (vim.wo)
// ---------------------------------------------------------------------------

func winOptGet(editor EditorAccess, L *glua.LState, name string) glua.LValue {
	win := editor.CurrentWindow()
	if win == nil {
		return glua.LNil
	}
	val, ok := win.GetOption(name)
	if !ok {
		return glua.LNil
	}
	return goToLua(L, val)
}

func winOptSet(editor EditorAccess, _ *glua.LState, name string, val glua.LValue) {
	win := editor.CurrentWindow()
	if win == nil {
		return
	}
	win.SetOption(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Global variables (vim.g)
// ---------------------------------------------------------------------------

func globalVarGet(editor EditorAccess, L *glua.LState, name string) glua.LValue {
	val, ok := editor.GetVar(name)
	if !ok {
		return glua.LNil
	}
	return goToLua(L, val)
}

func globalVarSet(editor EditorAccess, _ *glua.LState, name string, val glua.LValue) {
	editor.SetVar(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Buffer-local variables (vim.b)
// ---------------------------------------------------------------------------

func bufVarGet(editor EditorAccess, L *glua.LState, name string) glua.LValue {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return glua.LNil
	}
	val, ok := buf.GetVar(name)
	if !ok {
		return glua.LNil
	}
	return goToLua(L, val)
}

func bufVarSet(editor EditorAccess, _ *glua.LState, name string, val glua.LValue) {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return
	}
	buf.SetVar(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Window-local variables (vim.w)
// ---------------------------------------------------------------------------

func winVarGet(editor EditorAccess, L *glua.LState, name string) glua.LValue {
	win := editor.CurrentWindow()
	if win == nil {
		return glua.LNil
	}
	val, ok := win.GetOption(name) // windows expose vars through option iface
	if !ok {
		return glua.LNil
	}
	return goToLua(L, val)
}

func winVarSet(editor EditorAccess, _ *glua.LState, name string, val glua.LValue) {
	win := editor.CurrentWindow()
	if win == nil {
		return
	}
	win.SetOption(name, luaToGo(val))
}
