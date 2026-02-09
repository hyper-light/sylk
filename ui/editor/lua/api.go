package lua

import (
	rt "github.com/arnodel/golua/runtime"
)

// ---------------------------------------------------------------------------
// Table-driven vim.api registration
// ---------------------------------------------------------------------------

// apiFunc is a Lua-callable handler for one vim.api.nvim_* function.
type apiFunc func(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error)

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
func registerAPITable(_ *rt.Runtime, tbl *rt.Table, luaRT *Runtime) {
	for _, entry := range apiTable {
		fn := entry.Handler // capture for closure
		setGoFunc(tbl, entry.Name, func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			return fn(t, c, luaRT)
		}, defaultMaxArgs, true)
	}
}

// ---------------------------------------------------------------------------
// Buffer helpers
// ---------------------------------------------------------------------------

// resolveBuf returns the BufferAccess for the given buf argument.
// buf == 0 means "current buffer".
func resolveBuf(luaRT *Runtime, bufArg int) BufferAccess {
	if bufArg == 0 {
		return luaRT.editor.CurrentBuffer()
	}
	return luaRT.editor.GetBuffer(bufArg)
}

// resolveWin returns the WindowAccess for the given win argument.
// win == 0 means "current window".
func resolveWin(luaRT *Runtime, winArg int) WindowAccess {
	if winArg == 0 {
		return luaRT.editor.CurrentWindow()
	}
	return luaRT.editor.GetWindow(winArg)
}

// ---------------------------------------------------------------------------
// Buffer API handlers
// ---------------------------------------------------------------------------

func apiBufGetLines(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	start, _ := c.IntArg(1)
	end, _ := c.IntArg(2)
	// strict (arg 3) is accepted but not enforced.
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.NilValue), nil
	}
	lines := buf.GetLines(int(start), int(end))
	return c.PushingNext1(t.Runtime, rt.TableValue(stringsToTable(lines))), nil
}

func apiBufSetLines(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	start, _ := c.IntArg(1)
	end, _ := c.IntArg(2)
	// strict (arg 3) accepted but not enforced.
	linesTbl, ok := c.Arg(4).TryTable()
	if !ok || buf == nil {
		return c.Next(), nil
	}
	lines := luaTableToStrings(linesTbl)
	_ = buf.SetLines(int(start), int(end), lines)
	return c.Next(), nil
}

func apiBufGetName(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.StringValue("")), nil
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(buf.Name())), nil
}

func apiBufSetName(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	if buf == nil {
		return c.Next(), nil
	}
	name, _ := c.StringArg(1)
	buf.SetName(name)
	return c.Next(), nil
}

func apiBufLineCount(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(buf.LineCount()))), nil
}

func apiBufGetOption(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	name, _ := c.StringArg(1)
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.NilValue), nil
	}
	val, ok := buf.GetOption(name)
	if !ok {
		return c.PushingNext1(t.Runtime, rt.NilValue), nil
	}
	return c.PushingNext1(t.Runtime, goToLua(val)), nil
}

func apiBufSetOption(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	if buf == nil {
		return c.Next(), nil
	}
	name, _ := c.StringArg(1)
	buf.SetOption(name, luaToGo(c.Arg(2)))
	return c.Next(), nil
}

func apiBufGetVar(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	name, _ := c.StringArg(1)
	if buf == nil {
		return c.PushingNext1(t.Runtime, rt.NilValue), nil
	}
	val, ok := buf.GetVar(name)
	if !ok {
		return c.PushingNext1(t.Runtime, rt.NilValue), nil
	}
	return c.PushingNext1(t.Runtime, goToLua(val)), nil
}

func apiBufSetVar(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	bufArg, _ := c.IntArg(0)
	buf := resolveBuf(luaRT, int(bufArg))
	if buf == nil {
		return c.Next(), nil
	}
	name, _ := c.StringArg(1)
	buf.SetVar(name, luaToGo(c.Arg(2)))
	return c.Next(), nil
}

// ---------------------------------------------------------------------------
// Window API handlers
// ---------------------------------------------------------------------------

func apiWinGetCursor(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.PushingNext1(t.Runtime, rt.NilValue), nil
	}
	line, col := win.GetCursor()
	tbl := rt.NewTable()
	tbl.Set(rt.IntValue(1), rt.IntValue(int64(line)))
	tbl.Set(rt.IntValue(2), rt.IntValue(int64(col)))
	return c.PushingNext1(t.Runtime, rt.TableValue(tbl)), nil
}

func apiWinSetCursor(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.Next(), nil
	}
	posTbl, ok := c.Arg(1).TryTable()
	if !ok {
		return c.Next(), nil
	}
	lineVal := posTbl.Get(rt.IntValue(1))
	colVal := posTbl.Get(rt.IntValue(2))
	line, lineOK := lineVal.TryInt()
	col, colOK := colVal.TryInt()
	if !lineOK || !colOK {
		return c.Next(), nil
	}
	win.SetCursor(int(line), int(col))
	return c.Next(), nil
}

func apiWinGetBuf(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(win.GetBuffer()))), nil
}

func apiWinSetBuf(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.Next(), nil
	}
	bufID, _ := c.IntArg(1)
	_ = win.SetBuffer(int(bufID))
	return c.Next(), nil
}

func apiWinGetWidth(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(win.Width()))), nil
}

func apiWinGetHeight(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
	}
	return c.PushingNext1(t.Runtime, rt.IntValue(int64(win.Height()))), nil
}

func apiWinClose(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	winArg, _ := c.IntArg(0)
	win := resolveWin(luaRT, int(winArg))
	if win == nil {
		return c.Next(), nil
	}
	force, _ := c.BoolArg(1)
	_ = win.Close(force)
	return c.Next(), nil
}

// ---------------------------------------------------------------------------
// Global API handlers
// ---------------------------------------------------------------------------

func apiCommand(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	cmd, _ := c.StringArg(0)
	_ = luaRT.editor.Command(cmd)
	return c.Next(), nil
}

func apiGetCurrentBuf(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func apiGetCurrentWin(t *rt.Thread, c *rt.GoCont, _ *Runtime) (rt.Cont, error) {
	return c.PushingNext1(t.Runtime, rt.IntValue(0)), nil
}

func apiListBufs(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	ids := luaRT.editor.ListBuffers()
	tbl := rt.NewTable()
	for i, id := range ids {
		tbl.Set(rt.IntValue(int64(i+1)), rt.IntValue(int64(id)))
	}
	return c.PushingNext1(t.Runtime, rt.TableValue(tbl)), nil
}

func apiListWins(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	ids := luaRT.editor.ListWindows()
	tbl := rt.NewTable()
	for i, id := range ids {
		tbl.Set(rt.IntValue(int64(i+1)), rt.IntValue(int64(id)))
	}
	return c.PushingNext1(t.Runtime, rt.TableValue(tbl)), nil
}

func apiGetMode(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	m := luaRT.editor.GetMode()
	tbl := rt.NewTable()
	tbl.Set(rt.StringValue("mode"), rt.StringValue(m))
	tbl.Set(rt.StringValue("blocking"), rt.BoolValue(false))
	return c.PushingNext1(t.Runtime, rt.TableValue(tbl)), nil
}

func apiEcho(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	chunksTbl, ok := c.Arg(0).TryTable()
	if !ok {
		return c.Next(), nil
	}
	chunks := luaTableToStrings(chunksTbl)
	luaRT.editor.Echo(chunks)
	return c.Next(), nil
}

func apiNotify(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	msg, _ := c.StringArg(0)
	level, _ := c.IntArg(1)
	luaRT.editor.Notify(msg, int(level))
	return c.Next(), nil
}
