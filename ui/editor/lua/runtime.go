// Package lua implements an arnodel/golua Lua 5.4 VM for Neovim-compatible
// plugin scripting inside the Sylk editor.  The runtime owns the single Lua
// state and exposes sub-tables (vim.api, vim.keymap, vim.opt, ...) that
// mirror the Neovim Lua API surface.
package lua

import (
	"fmt"
	"os"
	"sync"

	"github.com/arnodel/golua/lib"
	rt "github.com/arnodel/golua/runtime"
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

// luaMemoryLimit caps the maximum memory the Lua VM may allocate (bytes).
// Derived from a 256 MiB budget -- the constant is the product of two
// self-documenting factors so there are no magic numbers.
const luaMemoryLimit = 256 * 1024 * 1024

// defaultMaxArgs is the pre-allocated arg count for Go-backed Lua functions.
// Covers the largest handler (nvim_buf_set_lines with 5 params).
const defaultMaxArgs = 5

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

// Runtime wraps an arnodel/golua Lua 5.4 VM and exposes the
// Neovim-compatible vim.* API surface.
type Runtime struct {
	mu      sync.Mutex
	runtime *rt.Runtime
	thread  *rt.Thread
	cleanup func() // stdlib teardown
	editor  EditorAccess

	// Sub-system stores (created during init, referenced by API funcs).
	Keymaps    *KeymapStore
	Autocmds   *AutocmdStore
	Highlights *HighlightStore
	Scheduler  *Scheduler
}

// NewRuntime creates a sandboxed Lua 5.4 VM with memory quotas,
// registers the full vim.* table tree and wires every sub-system.
func NewRuntime(editor EditorAccess) *Runtime {
	r := rt.New(os.Stdout, rt.WithRuntimeContext(rt.RuntimeContextDef{
		HardLimits: rt.RuntimeResources{
			Memory: luaMemoryLimit,
		},
	}))

	cleanup := lib.LoadAll(r)

	luaRT := &Runtime{
		runtime:    r,
		thread:     r.MainThread(),
		cleanup:    cleanup,
		editor:     editor,
		Keymaps:    NewKeymapStore(),
		Autocmds:   NewAutocmdStore(),
		Highlights: NewHighlightStore(),
		Scheduler:  NewScheduler(),
	}

	luaRT.applySandbox()
	luaRT.registerVimGlobal()
	return luaRT
}

// Close cleanly shuts down the Lua VM.
func (luaRT *Runtime) Close() error {
	luaRT.mu.Lock()
	defer luaRT.mu.Unlock()
	if luaRT.cleanup != nil {
		luaRT.cleanup()
	}
	return nil
}

// DoFile executes a Lua file inside the sandbox.
func (luaRT *Runtime) DoFile(path string) error {
	luaRT.mu.Lock()
	defer luaRT.mu.Unlock()
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return luaRT.doChunk(path, source)
}

// DoString executes a Lua string inside the sandbox.
func (luaRT *Runtime) DoString(code string) error {
	luaRT.mu.Lock()
	defer luaRT.mu.Unlock()
	return luaRT.doChunk("=string", []byte(code))
}

// doChunk compiles and executes Lua source.  Caller MUST hold luaRT.mu.
func (luaRT *Runtime) doChunk(name string, source []byte) error {
	env := rt.TableValue(luaRT.runtime.GlobalEnv())
	chunk, err := luaRT.runtime.CompileAndLoadLuaChunk(name, source, env)
	if err != nil {
		return err
	}
	_, err = rt.Call1(luaRT.thread, rt.FunctionValue(chunk))
	return err
}

// CallFunction invokes a Lua callable with the given arguments.
func (luaRT *Runtime) CallFunction(fn rt.Value, args ...rt.Value) (rt.Value, error) {
	luaRT.mu.Lock()
	defer luaRT.mu.Unlock()
	return rt.Call1(luaRT.thread, fn, args...)
}

// Editor returns the wired EditorAccess.
func (luaRT *Runtime) Editor() EditorAccess { return luaRT.editor }

// ---------------------------------------------------------------------------
// Sandbox
// ---------------------------------------------------------------------------

// applySandbox nils out every entry listed in sandboxRemovals.
func (luaRT *Runtime) applySandbox() {
	env := luaRT.runtime.GlobalEnv()
	for _, removal := range sandboxRemovals {
		nilField(env, removal)
	}
}

// nilField removes a single field from either a global table or the global
// environment itself.
func nilField(env *rt.Table, r sandboxRemoval) {
	if r.Table == "" {
		env.Set(rt.StringValue(r.Field), rt.NilValue)
		return
	}
	tbl, ok := env.Get(rt.StringValue(r.Table)).TryTable()
	if !ok {
		return
	}
	tbl.Set(rt.StringValue(r.Field), rt.NilValue)
}

// ---------------------------------------------------------------------------
// vim.* table registration
// ---------------------------------------------------------------------------

// registerVimGlobal builds the top-level `vim` table and attaches every
// sub-table and function.
func (luaRT *Runtime) registerVimGlobal() {
	vim := rt.NewTable()

	apiTbl := rt.NewTable()
	keymapTbl := rt.NewTable()
	fnTbl := rt.NewTable()

	registerAPITable(luaRT.runtime, apiTbl, luaRT)
	registerKeymapTable(luaRT.runtime, keymapTbl, luaRT)
	registerFnTable(luaRT.runtime, fnTbl, luaRT)
	registerOptTables(luaRT.runtime, vim, luaRT.editor)
	registerAutocmdFunctions(luaRT.runtime, apiTbl, luaRT)
	registerHighlightFunctions(luaRT.runtime, apiTbl, luaRT)
	registerScheduleFunctions(luaRT.runtime, vim, luaRT)

	vim.Set(rt.StringValue("api"), rt.TableValue(apiTbl))
	vim.Set(rt.StringValue("keymap"), rt.TableValue(keymapTbl))
	vim.Set(rt.StringValue("fn"), rt.TableValue(fnTbl))

	luaRT.runtime.GlobalEnv().Set(rt.StringValue("vim"), rt.TableValue(vim))
}

// ---------------------------------------------------------------------------
// Registration helper
// ---------------------------------------------------------------------------

// setGoFunc registers a Go function on a Lua table.
func setGoFunc(tbl *rt.Table, name string, fn rt.GoFunctionFunc, nArgs int, hasEtc bool) {
	goFn := rt.NewGoFunction(fn, name, nArgs, hasEtc)
	tbl.Set(rt.StringValue(name), rt.FunctionValue(goFn))
}

// ---------------------------------------------------------------------------
// Lua <-> Go value helpers
// ---------------------------------------------------------------------------

// goConverter maps a Go type to its golua Value constructor.
type goConverter struct {
	match func(any) bool
	conv  func(any) rt.Value
}

// goConverters is the table-driven Go→Lua type mapping.
var goConverters = []goConverter{
	{match: func(v any) bool { _, ok := v.(bool); return ok },
		conv: func(v any) rt.Value { return rt.BoolValue(v.(bool)) }},
	{match: func(v any) bool { _, ok := v.(int); return ok },
		conv: func(v any) rt.Value { return rt.IntValue(int64(v.(int))) }},
	{match: func(v any) bool { _, ok := v.(int64); return ok },
		conv: func(v any) rt.Value { return rt.IntValue(v.(int64)) }},
	{match: func(v any) bool { _, ok := v.(float64); return ok },
		conv: func(v any) rt.Value { return rt.FloatValue(v.(float64)) }},
	{match: func(v any) bool { _, ok := v.(string); return ok },
		conv: func(v any) rt.Value { return rt.StringValue(v.(string)) }},
	{match: func(v any) bool { _, ok := v.([]string); return ok },
		conv: func(v any) rt.Value { return rt.TableValue(stringsToTable(v.([]string))) }},
}

// goToLua converts a Go value to the corresponding Lua representation.
func goToLua(v any) rt.Value {
	if v == nil {
		return rt.NilValue
	}
	return goToLuaTyped(v)
}

// goToLuaTyped performs the actual type dispatch.
func goToLuaTyped(v any) rt.Value {
	for _, c := range goConverters {
		if c.match(v) {
			return c.conv(v)
		}
	}
	return rt.StringValue(fmt.Sprintf("%v", v))
}

// luaExtractor tries to pull a Go value from a golua Value.
type luaExtractor struct {
	extract func(rt.Value) (any, bool)
}

// luaExtractors is the table-driven Lua→Go type mapping.
var luaExtractors = []luaExtractor{
	{extract: func(v rt.Value) (any, bool) { b, ok := v.TryBool(); return b, ok }},
	{extract: func(v rt.Value) (any, bool) {
		n, ok := v.TryInt()
		return float64(n), ok
	}},
	{extract: func(v rt.Value) (any, bool) { f, ok := v.TryFloat(); return f, ok }},
	{extract: func(v rt.Value) (any, bool) { s, ok := v.TryString(); return s, ok }},
}

// luaToGo converts a Lua value to a Go native type.
func luaToGo(v rt.Value) any {
	if v.IsNil() {
		return nil
	}
	for _, e := range luaExtractors {
		if val, ok := e.extract(v); ok {
			return val
		}
	}
	return nil
}

// stringsToTable converts a Go []string into a Lua table with integer keys.
func stringsToTable(ss []string) *rt.Table {
	tbl := rt.NewTable()
	for i, s := range ss {
		tbl.Set(rt.IntValue(int64(i+1)), rt.StringValue(s))
	}
	return tbl
}

// luaTableToStrings reads a Lua table of strings into a Go slice.
func luaTableToStrings(tbl *rt.Table) []string {
	n := int(tbl.Len())
	out := make([]string, 0, n)
	for i := range n {
		val := tbl.Get(rt.IntValue(int64(i + 1)))
		if s, ok := val.TryString(); ok {
			out = append(out, s)
		}
	}
	return out
}

// luaToBool extracts a Go bool from a golua Value.
func luaToBool(v rt.Value) bool {
	b, ok := v.TryBool()
	if !ok {
		return false
	}
	return b
}

// luaToInt extracts a Go int from a golua number Value.
func luaToInt(v rt.Value) int {
	n, ok := v.TryInt()
	if !ok {
		return 0
	}
	return int(n)
}
