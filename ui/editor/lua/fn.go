package lua

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Table-driven vim.fn registration
// ---------------------------------------------------------------------------

// vimFn is a Go handler for one vim.fn.* function.
type vimFn func(L *glua.LState, rt *Runtime) int

// fnEntry pairs a vim function name with its handler.
type fnEntry struct {
	Name    string
	Handler vimFn
}

// fnTable is the authoritative list of vim.fn.* functions.
var fnTable = []fnEntry{
	// File / path.
	{Name: "expand", Handler: fnExpand},
	{Name: "glob", Handler: fnGlob},
	{Name: "filereadable", Handler: fnFilereadable},
	{Name: "isdirectory", Handler: fnIsdirectory},
	{Name: "fnamemodify", Handler: fnFnamemodify},
	{Name: "system", Handler: fnSystem},

	// String.
	{Name: "trim", Handler: fnTrim},
	{Name: "substitute", Handler: fnSubstitute},
	{Name: "match", Handler: fnMatch},
	{Name: "matchstr", Handler: fnMatchstr},
	{Name: "split", Handler: fnSplit},
	{Name: "join", Handler: fnJoin},

	// Value inspection.
	{Name: "len", Handler: fnLen},
	{Name: "empty", Handler: fnEmpty},
	{Name: "has", Handler: fnHas},
	{Name: "exists", Handler: fnExists},
	{Name: "type", Handler: fnType},

	// Cursor / line.
	{Name: "line", Handler: fnLine},
	{Name: "col", Handler: fnCol},
	{Name: "getline", Handler: fnGetline},
	{Name: "setline", Handler: fnSetline},
	{Name: "append", Handler: fnAppend},
	{Name: "indent", Handler: fnIndent},

	// Buffer / window.
	{Name: "bufnr", Handler: fnBufnr},
	{Name: "bufname", Handler: fnBufname},
	{Name: "winnr", Handler: fnWinnr},
	{Name: "tabpagenr", Handler: fnTabpagenr},
}

// fnLookup is built once from fnTable for O(1) dispatch.
var fnLookup map[string]vimFn

func init() {
	fnLookup = make(map[string]vimFn, len(fnTable))
	for _, e := range fnTable {
		fnLookup[e.Name] = e.Handler
	}
}

// registerFnTable sets up vim.fn with an __index metamethod so that
// vim.fn.foo(args) dispatches to the handler registered for "foo".
func registerFnTable(L *glua.LState, tbl *glua.LTable, rt *Runtime) {
	mt := L.NewTable()
	mt.RawSetString("__index", L.NewFunction(func(ls *glua.LState) int {
		name := ls.ToString(2)
		handler, ok := fnLookup[name]
		if !ok {
			ls.Push(glua.LNil)
			return 1
		}
		fn := handler // capture
		ls.Push(ls.NewFunction(func(inner *glua.LState) int {
			return fn(inner, rt)
		}))
		return 1
	}))
	L.SetMetatable(tbl, mt)
}

// ---------------------------------------------------------------------------
// Feature reporting for has()
// ---------------------------------------------------------------------------

// supportedFeatures enumerates features that `has()` reports as present.
var supportedFeatures = map[string]struct{}{
	"sylk":       {},
	"nvim":       {},
	"lua":        {},
	"syntax":     {},
	"autocmd":    {},
	"clipboard":  {},
	"unix":       {},
	"multi_byte": {},
}

// ---------------------------------------------------------------------------
// fnamemodify modifier dispatch
// ---------------------------------------------------------------------------

// fnameMod describes one :X modifier for fnamemodify().
type fnameMod struct {
	Suffix string
	Apply  func(string) string
}

// fnameModTable is the table-driven modifier set.
var fnameModTable = []fnameMod{
	{Suffix: ":p", Apply: func(p string) string { a, err := filepath.Abs(p); if err != nil { return p }; return a }},
	{Suffix: ":h", Apply: filepath.Dir},
	{Suffix: ":t", Apply: filepath.Base},
	{Suffix: ":r", Apply: func(p string) string { return strings.TrimSuffix(p, filepath.Ext(p)) }},
	{Suffix: ":e", Apply: func(p string) string { return strings.TrimPrefix(filepath.Ext(p), ".") }},
}

// ---------------------------------------------------------------------------
// Lua type numbers (vim convention)
// ---------------------------------------------------------------------------

// vimTypeNumber maps Lua type identifiers to vim-style type integers.
var vimTypeNumber = map[glua.LValueType]int{
	glua.LTNumber: 0,
	glua.LTString: 1,
	// func = 2
	glua.LTTable: 3,
	// dict = 4 (tables serve as both in Lua)
	glua.LTBool:   6,
	glua.LTNil:    7,
}

// ---------------------------------------------------------------------------
// Handler implementations
// ---------------------------------------------------------------------------

func fnExpand(L *glua.LState, rt *Runtime) int {
	expr := L.ToString(1)
	result := expandExpr(expr, rt)
	L.Push(glua.LString(result))
	return 1
}

// expandExpr resolves vim-style expansion tokens.
func expandExpr(expr string, rt *Runtime) string {
	type expander struct {
		Token   string
		Resolve func(*Runtime) string
	}
	expanders := []expander{
		{Token: "%", Resolve: func(rt *Runtime) string {
			buf := rt.editor.CurrentBuffer()
			if buf == nil { return "" }
			return buf.Name()
		}},
		{Token: "#", Resolve: func(_ *Runtime) string { return "" }}, // no alternate file concept yet
	}
	for _, e := range expanders {
		if expr == e.Token {
			return e.Resolve(rt)
		}
	}
	return expr
}

func fnGlob(L *glua.LState, _ *Runtime) int {
	pattern := L.ToString(1)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		L.Push(glua.LString(""))
		return 1
	}
	L.Push(glua.LString(strings.Join(matches, "\n")))
	return 1
}

func fnFilereadable(L *glua.LState, _ *Runtime) int {
	path := L.ToString(1)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		L.Push(glua.LNumber(0))
		return 1
	}
	L.Push(glua.LNumber(1))
	return 1
}

func fnIsdirectory(L *glua.LState, _ *Runtime) int {
	path := L.ToString(1)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		L.Push(glua.LNumber(0))
		return 1
	}
	L.Push(glua.LNumber(1))
	return 1
}

func fnFnamemodify(L *glua.LState, _ *Runtime) int {
	path := L.ToString(1)
	mods := L.ToString(2)
	result := applyFnameModifiers(path, mods)
	L.Push(glua.LString(result))
	return 1
}

// applyFnameModifiers iterates the modifier table, applying each matching
// suffix and consuming it from the mods string.
func applyFnameModifiers(path, mods string) string {
	result := path
	remaining := mods
	for remaining != "" {
		applied := false
		for _, m := range fnameModTable {
			if strings.HasPrefix(remaining, m.Suffix) {
				result = m.Apply(result)
				remaining = remaining[len(m.Suffix):]
				applied = true
				break
			}
		}
		if !applied {
			break // unknown modifier -- stop processing
		}
	}
	return result
}

func fnSystem(L *glua.LState, _ *Runtime) int {
	cmdStr := L.ToString(1)
	out, err := exec.Command("sh", "-c", cmdStr).Output()
	if err != nil {
		L.Push(glua.LString(""))
		return 1
	}
	L.Push(glua.LString(string(out)))
	return 1
}

func fnTrim(L *glua.LState, _ *Runtime) int {
	L.Push(glua.LString(strings.TrimSpace(L.ToString(1))))
	return 1
}

func fnSubstitute(L *glua.LState, _ *Runtime) int {
	str := L.ToString(1)
	pat := L.ToString(2)
	sub := L.ToString(3)
	flags := L.ToString(4)
	re, err := regexp.Compile(pat)
	if err != nil {
		L.Push(glua.LString(str))
		return 1
	}
	result := applySubstitution(re, str, sub, flags)
	L.Push(glua.LString(result))
	return 1
}

// applySubstitution replaces matches in str.  The "g" flag replaces all.
func applySubstitution(re *regexp.Regexp, str, sub, flags string) string {
	if strings.Contains(flags, "g") {
		return re.ReplaceAllString(str, sub)
	}
	// Replace only first occurrence.
	loc := re.FindStringIndex(str)
	if loc == nil {
		return str
	}
	return str[:loc[0]] + sub + str[loc[1]:]
}

func fnMatch(L *glua.LState, _ *Runtime) int {
	str := L.ToString(1)
	pat := L.ToString(2)
	re, err := regexp.Compile(pat)
	if err != nil {
		L.Push(glua.LNumber(-1))
		return 1
	}
	loc := re.FindStringIndex(str)
	if loc == nil {
		L.Push(glua.LNumber(-1))
		return 1
	}
	L.Push(glua.LNumber(loc[0]))
	return 1
}

func fnMatchstr(L *glua.LState, _ *Runtime) int {
	str := L.ToString(1)
	pat := L.ToString(2)
	re, err := regexp.Compile(pat)
	if err != nil {
		L.Push(glua.LString(""))
		return 1
	}
	m := re.FindString(str)
	L.Push(glua.LString(m))
	return 1
}

func fnSplit(L *glua.LState, _ *Runtime) int {
	str := L.ToString(1)
	pat := L.ToString(2)
	re, err := regexp.Compile(pat)
	if err != nil {
		tbl := L.NewTable()
		tbl.Append(glua.LString(str))
		L.Push(tbl)
		return 1
	}
	parts := re.Split(str, -1)
	L.Push(stringsToTable(L, parts))
	return 1
}

func fnJoin(L *glua.LState, _ *Runtime) int {
	listTbl, ok := L.Get(1).(*glua.LTable)
	if !ok {
		L.Push(glua.LString(""))
		return 1
	}
	sep := L.ToString(2)
	parts := luaTableToStrings(listTbl)
	L.Push(glua.LString(strings.Join(parts, sep)))
	return 1
}

func fnLen(L *glua.LState, _ *Runtime) int {
	val := L.Get(1)
	length := luaValueLen(val)
	L.Push(glua.LNumber(length))
	return 1
}

// luaValueLen returns the "length" of a Lua value (string len or table len).
func luaValueLen(v glua.LValue) int {
	type measurer struct {
		Match func(glua.LValue) bool
		Len   func(glua.LValue) int
	}
	measurers := []measurer{
		{Match: func(v glua.LValue) bool { _, ok := v.(glua.LString); return ok },
			Len: func(v glua.LValue) int { return len(string(v.(glua.LString))) }},
		{Match: func(v glua.LValue) bool { _, ok := v.(*glua.LTable); return ok },
			Len: func(v glua.LValue) int { return v.(*glua.LTable).Len() }},
	}
	for _, m := range measurers {
		if m.Match(v) {
			return m.Len(v)
		}
	}
	return 0
}

func fnEmpty(L *glua.LState, _ *Runtime) int {
	val := L.Get(1)
	result := luaValueEmpty(val)
	L.Push(glua.LNumber(result))
	return 1
}

// luaValueEmpty returns 1 if the value is "empty" (vim semantics), 0 otherwise.
func luaValueEmpty(v glua.LValue) int {
	type checker struct {
		Match func(glua.LValue) bool
		Empty func(glua.LValue) bool
	}
	checkers := []checker{
		{Match: func(v glua.LValue) bool { return v == glua.LNil },
			Empty: func(_ glua.LValue) bool { return true }},
		{Match: func(v glua.LValue) bool { _, ok := v.(glua.LString); return ok },
			Empty: func(v glua.LValue) bool { return string(v.(glua.LString)) == "" }},
		{Match: func(v glua.LValue) bool { _, ok := v.(glua.LNumber); return ok },
			Empty: func(v glua.LValue) bool { return float64(v.(glua.LNumber)) == 0 }},
		{Match: func(v glua.LValue) bool { _, ok := v.(*glua.LTable); return ok },
			Empty: func(v glua.LValue) bool { return v.(*glua.LTable).Len() == 0 }},
	}
	for _, c := range checkers {
		if c.Match(v) {
			if c.Empty(v) {
				return 1
			}
			return 0
		}
	}
	return 1
}

func fnHas(L *glua.LState, _ *Runtime) int {
	feature := L.ToString(1)
	_, ok := supportedFeatures[feature]
	if ok {
		L.Push(glua.LNumber(1))
		return 1
	}
	L.Push(glua.LNumber(0))
	return 1
}

func fnExists(L *glua.LState, _ *Runtime) int {
	expr := L.ToString(1)
	// Check if the name exists as a vim.fn.* function.
	_, ok := fnLookup[expr]
	if ok {
		L.Push(glua.LNumber(1))
		return 1
	}
	L.Push(glua.LNumber(0))
	return 1
}

func fnType(L *glua.LState, _ *Runtime) int {
	val := L.Get(1)
	typeNum, ok := vimTypeNumber[val.Type()]
	if !ok {
		// Functions are type 2 in vim.
		if val.Type() == glua.LTFunction {
			L.Push(glua.LNumber(2))
			return 1
		}
		L.Push(glua.LNumber(-1))
		return 1
	}
	L.Push(glua.LNumber(typeNum))
	return 1
}

func fnLine(L *glua.LState, rt *Runtime) int {
	expr := L.ToString(1)
	line := resolveLineExpr(expr, rt)
	L.Push(glua.LNumber(line))
	return 1
}

// resolveLineExpr maps vim-style line expressions to actual line numbers.
func resolveLineExpr(expr string, rt *Runtime) int {
	type lineResolver struct {
		Token   string
		Resolve func(*Runtime) int
	}
	resolvers := []lineResolver{
		{Token: ".", Resolve: func(rt *Runtime) int {
			win := rt.editor.CurrentWindow()
			if win == nil { return 0 }
			line, _ := win.GetCursor()
			return line
		}},
		{Token: "$", Resolve: func(rt *Runtime) int {
			buf := rt.editor.CurrentBuffer()
			if buf == nil { return 0 }
			return buf.LineCount()
		}},
	}
	for _, r := range resolvers {
		if expr == r.Token {
			return r.Resolve(rt)
		}
	}
	return 0
}

func fnCol(L *glua.LState, rt *Runtime) int {
	expr := L.ToString(1)
	col := resolveColExpr(expr, rt)
	L.Push(glua.LNumber(col))
	return 1
}

// resolveColExpr maps vim-style column expressions.
func resolveColExpr(expr string, rt *Runtime) int {
	if expr == "." {
		win := rt.editor.CurrentWindow()
		if win == nil {
			return 0
		}
		_, col := win.GetCursor()
		return col
	}
	return 0
}

func fnGetline(L *glua.LState, rt *Runtime) int {
	lnum := L.ToInt(1)
	buf := rt.editor.CurrentBuffer()
	if buf == nil {
		L.Push(glua.LString(""))
		return 1
	}
	lines := buf.GetLines(lnum-1, lnum) // vim is 1-indexed
	if len(lines) == 0 {
		L.Push(glua.LString(""))
		return 1
	}
	L.Push(glua.LString(lines[0]))
	return 1
}

func fnSetline(L *glua.LState, rt *Runtime) int {
	lnum := L.ToInt(1)
	text := L.ToString(2)
	buf := rt.editor.CurrentBuffer()
	if buf == nil {
		L.Push(glua.LNumber(1))
		return 1
	}
	err := buf.SetLines(lnum-1, lnum, []string{text})
	if err != nil {
		L.Push(glua.LNumber(1))
		return 1
	}
	L.Push(glua.LNumber(0))
	return 1
}

func fnAppend(L *glua.LState, rt *Runtime) int {
	lnum := L.ToInt(1)
	text := L.ToString(2)
	buf := rt.editor.CurrentBuffer()
	if buf == nil {
		L.Push(glua.LNumber(1))
		return 1
	}
	// Append inserts a line after lnum.
	err := buf.SetLines(lnum, lnum, []string{text})
	if err != nil {
		L.Push(glua.LNumber(1))
		return 1
	}
	L.Push(glua.LNumber(0))
	return 1
}

func fnIndent(L *glua.LState, rt *Runtime) int {
	lnum := L.ToInt(1)
	buf := rt.editor.CurrentBuffer()
	if buf == nil {
		L.Push(glua.LNumber(0))
		return 1
	}
	lines := buf.GetLines(lnum-1, lnum)
	if len(lines) == 0 {
		L.Push(glua.LNumber(0))
		return 1
	}
	level := countLeadingSpaces(lines[0])
	L.Push(glua.LNumber(level))
	return 1
}

// countLeadingSpaces counts the number of leading space/tab characters,
// treating each tab as one indent unit.
func countLeadingSpaces(s string) int {
	count := 0
	for _, ch := range s {
		if ch != ' ' && ch != '\t' {
			break
		}
		count++
	}
	return count
}

func fnBufnr(L *glua.LState, _ *Runtime) int {
	// In Neovim, bufnr('%') returns current buffer id.
	// We return 0 (the convention for "current").
	L.Push(glua.LNumber(0))
	return 1
}

func fnBufname(L *glua.LState, rt *Runtime) int {
	buf := rt.editor.CurrentBuffer()
	if buf == nil {
		L.Push(glua.LString(""))
		return 1
	}
	L.Push(glua.LString(buf.Name()))
	return 1
}

func fnWinnr(L *glua.LState, _ *Runtime) int {
	L.Push(glua.LNumber(1)) // single-window model: always window 1
	return 1
}

func fnTabpagenr(L *glua.LState, _ *Runtime) int {
	L.Push(glua.LNumber(1)) // single-tab model: always tab 1
	return 1
}
