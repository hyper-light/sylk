package lua

import (
	rt "github.com/arnodel/golua/runtime"
)

// ---------------------------------------------------------------------------
// Scope definitions (table-driven)
// ---------------------------------------------------------------------------

// optScope describes how to get/set values for a particular vim.* scope.
type optScope struct {
	Name   string
	Getter func(EditorAccess, string) rt.Value
	Setter func(EditorAccess, string, rt.Value)
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
func registerOptTables(_ *rt.Runtime, vim *rt.Table, editor EditorAccess) {
	for _, scope := range scopeTable {
		ud := newScopeUserdata(editor, scope)
		vim.Set(rt.StringValue(scope.Name), ud)
	}
}

// newScopeUserdata builds a Lua userdata whose metamethods proxy reads
// and writes through the scope's getter/setter pair.
func newScopeUserdata(editor EditorAccess, scope optScope) rt.Value {
	get := scope.Getter // capture for closure
	set := scope.Setter

	mt := rt.NewTable()

	setGoFunc(mt, "__index", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		// arg 0 = self (userdata), arg 1 = key
		key, err := c.StringArg(1)
		if err != nil {
			return c.PushingNext1(t.Runtime, rt.NilValue), nil
		}
		return c.PushingNext1(t.Runtime, get(editor, key)), nil
	}, 2, false)

	setGoFunc(mt, "__newindex", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		key, err := c.StringArg(1)
		if err != nil {
			return c.Next(), nil
		}
		val := c.Arg(2)
		set(editor, key, val)
		return c.Next(), nil
	}, 3, false)

	ud := rt.NewUserData(nil, mt)
	return rt.UserDataValue(ud)
}

// ---------------------------------------------------------------------------
// Global options (vim.opt / vim.o)
// ---------------------------------------------------------------------------

func globalOptGet(editor EditorAccess, name string) rt.Value {
	val, ok := editor.GetOption(name)
	if !ok {
		return rt.NilValue
	}
	return goToLua(val)
}

func globalOptSet(editor EditorAccess, name string, val rt.Value) {
	editor.SetOption(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Buffer-local options (vim.bo)
// ---------------------------------------------------------------------------

func bufOptGet(editor EditorAccess, name string) rt.Value {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return rt.NilValue
	}
	val, ok := buf.GetOption(name)
	if !ok {
		return rt.NilValue
	}
	return goToLua(val)
}

func bufOptSet(editor EditorAccess, name string, val rt.Value) {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return
	}
	buf.SetOption(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Window-local options (vim.wo)
// ---------------------------------------------------------------------------

func winOptGet(editor EditorAccess, name string) rt.Value {
	win := editor.CurrentWindow()
	if win == nil {
		return rt.NilValue
	}
	val, ok := win.GetOption(name)
	if !ok {
		return rt.NilValue
	}
	return goToLua(val)
}

func winOptSet(editor EditorAccess, name string, val rt.Value) {
	win := editor.CurrentWindow()
	if win == nil {
		return
	}
	win.SetOption(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Global variables (vim.g)
// ---------------------------------------------------------------------------

func globalVarGet(editor EditorAccess, name string) rt.Value {
	val, ok := editor.GetVar(name)
	if !ok {
		return rt.NilValue
	}
	return goToLua(val)
}

func globalVarSet(editor EditorAccess, name string, val rt.Value) {
	editor.SetVar(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Buffer-local variables (vim.b)
// ---------------------------------------------------------------------------

func bufVarGet(editor EditorAccess, name string) rt.Value {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return rt.NilValue
	}
	val, ok := buf.GetVar(name)
	if !ok {
		return rt.NilValue
	}
	return goToLua(val)
}

func bufVarSet(editor EditorAccess, name string, val rt.Value) {
	buf := editor.CurrentBuffer()
	if buf == nil {
		return
	}
	buf.SetVar(name, luaToGo(val))
}

// ---------------------------------------------------------------------------
// Window-local variables (vim.w)
// ---------------------------------------------------------------------------

func winVarGet(editor EditorAccess, name string) rt.Value {
	win := editor.CurrentWindow()
	if win == nil {
		return rt.NilValue
	}
	val, ok := win.GetOption(name)
	if !ok {
		return rt.NilValue
	}
	return goToLua(val)
}

func winVarSet(editor EditorAccess, name string, val rt.Value) {
	win := editor.CurrentWindow()
	if win == nil {
		return
	}
	win.SetOption(name, luaToGo(val))
}
