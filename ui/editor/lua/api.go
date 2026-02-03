package lua

import (
	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Table-driven vim.api registration
// ---------------------------------------------------------------------------

// apiFunc is a Lua-callable handler for one vim.api.nvim_* function.
type apiFunc func(L *glua.LState, rt *Runtime) int

// apiEntry pairs a Neovim API function name with its Go handler.
type apiEntry struct {
	Name    string
	Handler apiFunc
}

// apiTable is the authoritative list of every function registered under
// vim.api.  Dispatch is table-driven -- no switch/case fan-out.
var apiTable = []apiEntry{
	// Buffer operations.
	{Name: "nvim_buf_get_lines", Handler: apiBufGetLines},
	{Name: "nvim_buf_set_lines", Handler: apiBufSetLines},
	{Name: "nvim_buf_get_name", Handler: apiBufGetName},
	{Name: "nvim_buf_set_name", Handler: apiBufSetName},
	{Name: "nvim_buf_line_count", Handler: apiBufLineCount},
	{Name: "nvim_buf_get_option", Handler: apiBufGetOption},
	{Name: "nvim_buf_set_option", Handler: apiBufSetOption},
	{Name: "nvim_buf_get_var", Handler: apiBufGetVar},
	{Name: "nvim_buf_set_var", Handler: apiBufSetVar},

	// Window operations.
	{Name: "nvim_win_get_cursor", Handler: apiWinGetCursor},
	{Name: "nvim_win_set_cursor", Handler: apiWinSetCursor},
	{Name: "nvim_win_get_buf", Handler: apiWinGetBuf},
	{Name: "nvim_win_set_buf", Handler: apiWinSetBuf},
	{Name: "nvim_win_get_width", Handler: apiWinGetWidth},
	{Name: "nvim_win_get_height", Handler: apiWinGetHeight},
	{Name: "nvim_win_close", Handler: apiWinClose},

	// Global operations.
	{Name: "nvim_command", Handler: apiCommand},
	{Name: "nvim_get_current_buf", Handler: apiGetCurrentBuf},
	{Name: "nvim_get_current_win", Handler: apiGetCurrentWin},
	{Name: "nvim_list_bufs", Handler: apiListBufs},
	{Name: "nvim_list_wins", Handler: apiListWins},
	{Name: "nvim_get_mode", Handler: apiGetMode},
	{Name: "nvim_echo", Handler: apiEcho},
	{Name: "nvim_notify", Handler: apiNotify},
}

// registerAPITable wires every entry in apiTable onto the Lua table.
func registerAPITable(L *glua.LState, tbl *glua.LTable, rt *Runtime) {
	for _, entry := range apiTable {
		fn := entry.Handler // capture for closure
		tbl.RawSetString(entry.Name, L.NewFunction(func(ls *glua.LState) int {
			return fn(ls, rt)
		}))
	}
}

// ---------------------------------------------------------------------------
// Buffer helpers
// ---------------------------------------------------------------------------

// resolveBuf returns the BufferAccess for the given Lua buf argument.
// buf == 0 means "current buffer".
func resolveBuf(rt *Runtime, bufArg int) BufferAccess {
	if bufArg == 0 {
		return rt.editor.CurrentBuffer()
	}
	return rt.editor.GetBuffer(bufArg)
}

// resolveWin returns the WindowAccess for the given Lua win argument.
// win == 0 means "current window".
func resolveWin(rt *Runtime, winArg int) WindowAccess {
	if winArg == 0 {
		return rt.editor.CurrentWindow()
	}
	return rt.editor.GetWindow(winArg)
}

// ---------------------------------------------------------------------------
// Buffer API handlers
// ---------------------------------------------------------------------------

func apiBufGetLines(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	start := L.ToInt(2)
	end := L.ToInt(3)
	// strict (arg 4) is accepted but not enforced in this implementation.
	if buf == nil {
		L.Push(glua.LNil)
		return 1
	}
	lines := buf.GetLines(start, end)
	L.Push(stringsToTable(L, lines))
	return 1
}

func apiBufSetLines(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	start := L.ToInt(2)
	end := L.ToInt(3)
	// strict (arg 4) accepted but not enforced.
	linesTbl, ok := L.Get(5).(*glua.LTable)
	if !ok || buf == nil {
		return 0
	}
	lines := luaTableToStrings(linesTbl)
	_ = buf.SetLines(start, end, lines)
	return 0
}

func apiBufGetName(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	if buf == nil {
		L.Push(glua.LString(""))
		return 1
	}
	L.Push(glua.LString(buf.Name()))
	return 1
}

func apiBufSetName(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	if buf == nil {
		return 0
	}
	buf.SetName(L.ToString(2))
	return 0
}

func apiBufLineCount(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	if buf == nil {
		L.Push(glua.LNumber(0))
		return 1
	}
	L.Push(glua.LNumber(buf.LineCount()))
	return 1
}

func apiBufGetOption(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	name := L.ToString(2)
	if buf == nil {
		L.Push(glua.LNil)
		return 1
	}
	val, ok := buf.GetOption(name)
	if !ok {
		L.Push(glua.LNil)
		return 1
	}
	L.Push(goToLua(L, val))
	return 1
}

func apiBufSetOption(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	if buf == nil {
		return 0
	}
	buf.SetOption(L.ToString(2), luaToGo(L.Get(3)))
	return 0
}

func apiBufGetVar(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	name := L.ToString(2)
	if buf == nil {
		L.Push(glua.LNil)
		return 1
	}
	val, ok := buf.GetVar(name)
	if !ok {
		L.Push(glua.LNil)
		return 1
	}
	L.Push(goToLua(L, val))
	return 1
}

func apiBufSetVar(L *glua.LState, rt *Runtime) int {
	buf := resolveBuf(rt, L.ToInt(1))
	if buf == nil {
		return 0
	}
	buf.SetVar(L.ToString(2), luaToGo(L.Get(3)))
	return 0
}

// ---------------------------------------------------------------------------
// Window API handlers
// ---------------------------------------------------------------------------

func apiWinGetCursor(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		L.Push(glua.LNil)
		return 1
	}
	line, col := win.GetCursor()
	tbl := L.NewTable()
	tbl.Append(glua.LNumber(line))
	tbl.Append(glua.LNumber(col))
	L.Push(tbl)
	return 1
}

func apiWinSetCursor(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		return 0
	}
	posTbl, ok := L.Get(2).(*glua.LTable)
	if !ok {
		return 0
	}
	lineVal, lineOK := posTbl.RawGetInt(1).(glua.LNumber)
	colVal, colOK := posTbl.RawGetInt(2).(glua.LNumber)
	if !lineOK || !colOK {
		return 0
	}
	win.SetCursor(int(lineVal), int(colVal))
	return 0
}

func apiWinGetBuf(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		L.Push(glua.LNumber(0))
		return 1
	}
	L.Push(glua.LNumber(win.GetBuffer()))
	return 1
}

func apiWinSetBuf(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		return 0
	}
	_ = win.SetBuffer(L.ToInt(2))
	return 0
}

func apiWinGetWidth(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		L.Push(glua.LNumber(0))
		return 1
	}
	L.Push(glua.LNumber(win.Width()))
	return 1
}

func apiWinGetHeight(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		L.Push(glua.LNumber(0))
		return 1
	}
	L.Push(glua.LNumber(win.Height()))
	return 1
}

func apiWinClose(L *glua.LState, rt *Runtime) int {
	win := resolveWin(rt, L.ToInt(1))
	if win == nil {
		return 0
	}
	force := L.ToBool(2)
	_ = win.Close(force)
	return 0
}

// ---------------------------------------------------------------------------
// Global API handlers
// ---------------------------------------------------------------------------

func apiCommand(L *glua.LState, rt *Runtime) int {
	cmd := L.ToString(1)
	_ = rt.editor.Command(cmd)
	return 0
}

func apiGetCurrentBuf(L *glua.LState, _ *Runtime) int {
	// 0 is the Neovim convention for "current buffer".
	L.Push(glua.LNumber(0))
	return 1
}

func apiGetCurrentWin(L *glua.LState, _ *Runtime) int {
	L.Push(glua.LNumber(0))
	return 1
}

func apiListBufs(L *glua.LState, rt *Runtime) int {
	ids := rt.editor.ListBuffers()
	tbl := L.NewTable()
	for _, id := range ids {
		tbl.Append(glua.LNumber(id))
	}
	L.Push(tbl)
	return 1
}

func apiListWins(L *glua.LState, rt *Runtime) int {
	ids := rt.editor.ListWindows()
	tbl := L.NewTable()
	for _, id := range ids {
		tbl.Append(glua.LNumber(id))
	}
	L.Push(tbl)
	return 1
}

func apiGetMode(L *glua.LState, rt *Runtime) int {
	m := rt.editor.GetMode()
	tbl := L.NewTable()
	tbl.RawSetString("mode", glua.LString(m))
	tbl.RawSetString("blocking", glua.LFalse)
	L.Push(tbl)
	return 1
}

func apiEcho(L *glua.LState, rt *Runtime) int {
	chunksTbl, ok := L.Get(1).(*glua.LTable)
	if !ok {
		return 0
	}
	chunks := luaTableToStrings(chunksTbl)
	rt.editor.Echo(chunks)
	return 0
}

func apiNotify(L *glua.LState, rt *Runtime) int {
	msg := L.ToString(1)
	level := L.ToInt(2)
	rt.editor.Notify(msg, level)
	return 0
}
