package lua

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	rt "github.com/arnodel/golua/runtime"
)

// ---------------------------------------------------------------------------
// Table-driven vim.fn registration
// ---------------------------------------------------------------------------

// vimFn is a Go handler for one vim.fn.* function.
type vimFn func(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error)

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
func registerFnTable(_ *rt.Runtime, tbl *rt.Table, luaRT *Runtime) {
	mt := rt.NewTable()
	setGoFunc(mt, "__index", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		name, err := c.StringArg(1) // arg 0 = self, arg 1 = key
		if err != nil {
			return c.PushingNext1(t.Runtime, rt.NilValue), nil
		}
		handler, ok := fnLookup[name]
		if !ok {
			return c.PushingNext1(t.Runtime, rt.NilValue), nil
		}
		fn := handler // capture
		goFn := rt.NewGoFunction(func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			return fn(t, c, luaRT)
		}, name, defaultMaxArgs, true)
		return c.PushingNext1(t.Runtime, rt.FunctionValue(goFn)), nil
	}, 2, false)
	tbl.SetMetatable(mt)
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
	{Suffix: ":p", Apply: func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		return a
	}},
	{Suffix: ":h", Apply: filepath.Dir},
	{Suffix: ":t", Apply: filepath.Base},
	{Suffix: ":r", Apply: func(p string) string { return strings.TrimSuffix(p, filepath.Ext(p)) }},
	{Suffix: ":e", Apply: func(p string) string { return strings.TrimPrefix(filepath.Ext(p), ".") }},
}

// ---------------------------------------------------------------------------
// Vim type numbers — resolved via Try* methods rather than a type map
// ---------------------------------------------------------------------------

// vimTypeOf maps a golua Value to a vim-convention type integer.
func vimTypeOf(v rt.Value) int {
	type typeCheck struct {
		check  func(rt.Value) bool
		typeID int
	}
	checks := []typeCheck{
		{check: func(v rt.Value) bool { return v.IsNil() }, typeID: 7},
		{check: func(v rt.Value) bool { _, ok := v.TryInt(); return ok }, typeID: 0},
		{check: func(v rt.Value) bool { _, ok := v.TryFloat(); return ok }, typeID: 0},
		{check: func(v rt.Value) bool { _, ok := v.TryString(); return ok }, typeID: 1},
		{check: func(v rt.Value) bool { _, ok := v.TryCallable(); return ok }, typeID: 2},
		{check: func(v rt.Value) bool { _, ok := v.TryTable(); return ok }, typeID: 3},
		{check: func(v rt.Value) bool { _, ok := v.TryBool(); return ok }, typeID: 6},
	}
	for _, tc := range checks {
		if tc.check(v) {
			return tc.typeID
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Handler implementations
// ---------------------------------------------------------------------------

func fnExpand(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	expr, _ := c.StringArg(0)
	result := expandExpr(expr, luaRT)
	return c.PushingNext1(t.Runtime, rt.StringValue(result)), nil
}

// expandExpr resolves vim-style expansion tokens.
func expandExpr(expr string, luaRT *Runtime) string {
	type expander struct {
		Token   string
		Resolve func(*Runtime) string
	}
	expanders := []expander{
		{Token: "%", Resolve: func(r *Runtime) string {
			buf := r.editor.CurrentBuffer()
			if buf == nil {
				return ""
			}
			return buf.Name()
		}},
		{Token: "#", Resolve: func(_ *Runtime) string { return "" }},
	}
	for _, e := range expanders {
		if expr == e.Token {
			return e.Resolve(luaRT)
		}
	}
	return expr
}

func fnGlob(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	pattern, _ := c.StringArg(0)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(strings.Join(matches, "\n"))), nil
}

func fnFilereadable(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	path, _ := c.StringArg(0)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
}

func fnIsdirectory(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	path, _ := c.StringArg(0)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
}

func fnFnamemodify(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	path, _ := c.StringArg(0)
	mods, _ := c.StringArg(1)
	result := applyFnameModifiers(path, mods)
	return c.PushingNext1(t.Runtime, rt.StringValue(result)), nil
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
			break
		}
	}
	return result
}

func fnSystem(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	cmdStr, _ := c.StringArg(0)
	out, err := exec.Command("sh", "-c", cmdStr).Output()
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(string(out))), nil
}

func fnTrim(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	s, _ := c.StringArg(0)
	return c.PushingNext1(t.Runtime, rt.StringValue(strings.TrimSpace(s))), nil
}

func fnSubstitute(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	str, _ := c.StringArg(0)
	pat, _ := c.StringArg(1)
	sub, _ := c.StringArg(2)
	flags, _ := c.StringArg(3)
	re, err := regexp.Compile(pat)
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.StringValue(str)), nil
	}
	result := applySubstitution(re, str, sub, flags)
	return c.PushingNext1(t.Runtime, rt.StringValue(result)), nil
}

// applySubstitution replaces matches in str.  The "g" flag replaces all.
func applySubstitution(re *regexp.Regexp, str, sub, flags string) string {
	if strings.Contains(flags, "g") {
		return re.ReplaceAllString(str, sub)
	}
	loc := re.FindStringIndex(str)
	if loc == nil {
		return str
	}
	return str[:loc[0]] + sub + str[loc[1]:]
}

func fnMatch(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	str, _ := c.StringArg(0)
	pat, _ := c.StringArg(1)
	re, err := regexp.Compile(pat)
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(-1)), nil
	}
	loc := re.FindStringIndex(str)
	if loc == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(-1)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(loc[0]))), nil
}

func fnMatchstr(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	str, _ := c.StringArg(0)
	pat, _ := c.StringArg(1)
	re, err := regexp.Compile(pat)
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	m := re.FindString(str)
	return c.PushingNext1(t.Runtime, rt.StringValue(m)), nil
}

func fnSplit(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	str, _ := c.StringArg(0)
	pat, _ := c.StringArg(1)
	re, err := regexp.Compile(pat)
	if err != nil {
		tbl := rt.NewTable()
		tbl.Set(rt.IntValue(1), rt.StringValue(str))
		return c.PushingNext1(t.Runtime, rt.TableValue(tbl)), nil
	}
	parts := re.Split(str, -1)
	return c.PushingNext1(t.Runtime, rt.TableValue(stringsToTable(parts))), nil
}

func fnJoin(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	listTbl, ok := c.Arg(0).TryTable()
	if !ok {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	sep, _ := c.StringArg(1)
	parts := luaTableToStrings(listTbl)
	return c.PushingNext1(t.Runtime, rt.StringValue(strings.Join(parts, sep))), nil
}

func fnLen(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	val := c.Arg(0)
	length := luaValueLen(val)
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(length))), nil
}

// luaValueLen returns the "length" of a Lua value (string len or table len).
func luaValueLen(v rt.Value) int {
	if s, ok := v.TryString(); ok {
		return len(s)
	}
	if tbl, ok := v.TryTable(); ok {
		return int(tbl.Len())
	}
	return 0
}

func fnEmpty(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	val := c.Arg(0)
	result := luaValueEmpty(val)
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(result))), nil
}

// luaValueEmpty returns 1 if the value is "empty" (vim semantics), 0 otherwise.
func luaValueEmpty(v rt.Value) int {
	type checker struct {
		Match func(rt.Value) bool
		Empty func(rt.Value) bool
	}
	checkers := []checker{
		{Match: func(v rt.Value) bool { return v.IsNil() },
			Empty: func(_ rt.Value) bool { return true }},
		{Match: func(v rt.Value) bool { _, ok := v.TryString(); return ok },
			Empty: func(v rt.Value) bool { s, _ := v.TryString(); return s == "" }},
		{Match: func(v rt.Value) bool { _, ok := v.TryInt(); return ok },
			Empty: func(v rt.Value) bool { n, _ := v.TryInt(); return n == 0 }},
		{Match: func(v rt.Value) bool { _, ok := v.TryFloat(); return ok },
			Empty: func(v rt.Value) bool { f, _ := v.TryFloat(); return f == 0 }},
		{Match: func(v rt.Value) bool { _, ok := v.TryTable(); return ok },
			Empty: func(v rt.Value) bool { tbl, _ := v.TryTable(); return tbl.Len() == 0 }},
	}
	for _, ck := range checkers {
		if ck.Match(v) {
			if ck.Empty(v) {
				return 1
			}
			return 0
		}
	}
	return 1
}

func fnHas(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	feature, _ := c.StringArg(0)
	_, ok := supportedFeatures[feature]
	if ok {
		return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func fnExists(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	expr, _ := c.StringArg(0)
	_, ok := fnLookup[expr]
	if ok {
		return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func fnType(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	val := c.Arg(0)
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(vimTypeOf(val)))), nil
}

func fnLine(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	expr, _ := c.StringArg(0)
	line := resolveLineExpr(expr, luaRT)
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(line))), nil
}

// resolveLineExpr maps vim-style line expressions to actual line numbers.
func resolveLineExpr(expr string, luaRT *Runtime) int {
	type lineResolver struct {
		Token   string
		Resolve func(*Runtime) int
	}
	resolvers := []lineResolver{
		{Token: ".", Resolve: func(r *Runtime) int {
			win := r.editor.CurrentWindow()
			if win == nil {
				return 0
			}
			line, _ := win.GetCursor()
			return line
		}},
		{Token: "$", Resolve: func(r *Runtime) int {
			buf := r.editor.CurrentBuffer()
			if buf == nil {
				return 0
			}
			return buf.LineCount()
		}},
	}
	for _, r := range resolvers {
		if expr == r.Token {
			return r.Resolve(luaRT)
		}
	}
	return 0
}

func fnCol(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	expr, _ := c.StringArg(0)
	col := resolveColExpr(expr, luaRT)
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(col))), nil
}

// resolveColExpr maps vim-style column expressions.
func resolveColExpr(expr string, luaRT *Runtime) int {
	if expr == "." {
		win := luaRT.editor.CurrentWindow()
		if win == nil {
			return 0
		}
		_, col := win.GetCursor()
		return col
	}
	return 0
}

func fnGetline(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	lnum, _ := c.IntArg(0)
	buf := luaRT.editor.CurrentBuffer()
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	lines := buf.GetLines(int(lnum)-1, int(lnum))
	if len(lines) == 0 {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(lines[0])), nil
}

func fnSetline(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	lnum, _ := c.IntArg(0)
	text, _ := c.StringArg(1)
	buf := luaRT.editor.CurrentBuffer()
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
	}
	err := buf.SetLines(int(lnum)-1, int(lnum), []string{text})
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func fnAppend(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	lnum, _ := c.IntArg(0)
	text, _ := c.StringArg(1)
	buf := luaRT.editor.CurrentBuffer()
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
	}
	err := buf.SetLines(int(lnum), int(lnum), []string{text})
	if err != nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func fnIndent(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	lnum, _ := c.IntArg(0)
	buf := luaRT.editor.CurrentBuffer()
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	lines := buf.GetLines(int(lnum)-1, int(lnum))
	if len(lines) == 0 {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	level := countLeadingSpaces(lines[0])
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(level))), nil
}

// countLeadingSpaces counts the number of leading space/tab characters.
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

func fnBufnr(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func fnBufname(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	buf := luaRT.editor.CurrentBuffer()
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(buf.Name())), nil
}

func fnWinnr(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
}

func fnTabpagenr(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	return c.PushingNext1(t.Runtime, rt.IntValue(1)), nil
}
