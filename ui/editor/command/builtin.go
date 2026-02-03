package command

import "errors"

// builtinTable defines all built-in editor commands. Each entry is a
// CommandDef ready for registration. Handlers delegate to callback fields
// on ExecContext rather than importing editor packages directly.
var builtinTable = []CommandDef{
	{Name: "write", Handler: cmdWrite, RangeAllowed: true, BangAllowed: true},
	{Name: "quit", Handler: cmdQuit, RangeAllowed: false, BangAllowed: true},
	{Name: "wq", Handler: cmdWriteQuit, RangeAllowed: false, BangAllowed: true},
	{Name: "edit", Handler: cmdEdit, RangeAllowed: false, BangAllowed: true},
	{Name: "split", Handler: cmdSplitH, RangeAllowed: false, BangAllowed: false},
	{Name: "vsplit", Handler: cmdSplitV, RangeAllowed: false, BangAllowed: false},
	{Name: "bnext", Handler: cmdBufNext, RangeAllowed: false, BangAllowed: false},
	{Name: "bprev", Handler: cmdBufPrev, RangeAllowed: false, BangAllowed: false},
	{Name: "bdelete", Handler: cmdBufDelete, RangeAllowed: false, BangAllowed: true},
	{Name: "ls", Handler: cmdListBufs, RangeAllowed: false, BangAllowed: false},
	{Name: "buffers", Handler: cmdListBufs, RangeAllowed: false, BangAllowed: false},
	{Name: "set", Handler: cmdSet, RangeAllowed: false, BangAllowed: false},
	{Name: "map", Handler: cmdMap, RangeAllowed: false, BangAllowed: false},
	{Name: "source", Handler: cmdSource, RangeAllowed: false, BangAllowed: false},
	{Name: "lua", Handler: cmdLua, RangeAllowed: false, BangAllowed: false},
	{Name: "!", Handler: cmdShell, RangeAllowed: true, BangAllowed: false},
	{Name: "nohlsearch", Handler: cmdNoHL, RangeAllowed: false, BangAllowed: false},
	{Name: "substitute", Handler: cmdSubstitute, RangeAllowed: true, BangAllowed: false},
}

// RegisterBuiltins registers all built-in commands with the given registry.
func RegisterBuiltins(reg *Registry) {
	for _, def := range builtinTable {
		_ = reg.Register(def)
	}
}

// ---------------------------------------------------------------------------
// Handler implementations
// ---------------------------------------------------------------------------

func cmdWrite(ctx *ExecContext) error {
	if ctx.WriteBuffer == nil {
		return errors.New("write: not available")
	}
	return ctx.WriteBuffer(ctx.Args)
}

func cmdQuit(ctx *ExecContext) error {
	if ctx.CloseWindow == nil {
		return errors.New("quit: not available")
	}
	return ctx.CloseWindow(ctx.Bang)
}

func cmdWriteQuit(ctx *ExecContext) error {
	if ctx.WriteBuffer == nil || ctx.CloseWindow == nil {
		return errors.New("wq: not available")
	}
	if err := ctx.WriteBuffer(ctx.Args); err != nil {
		return err
	}
	return ctx.CloseWindow(true)
}

func cmdEdit(ctx *ExecContext) error {
	if ctx.OpenFile == nil {
		return errors.New("edit: not available")
	}
	return ctx.OpenFile(ctx.Args)
}

func cmdSplitH(ctx *ExecContext) error {
	if ctx.SplitHorizontal == nil {
		return errors.New("split: not available")
	}
	return ctx.SplitHorizontal()
}

func cmdSplitV(ctx *ExecContext) error {
	if ctx.SplitVertical == nil {
		return errors.New("vsplit: not available")
	}
	return ctx.SplitVertical()
}

func cmdBufNext(ctx *ExecContext) error {
	if ctx.NextBuffer == nil {
		return errors.New("bnext: not available")
	}
	return ctx.NextBuffer()
}

func cmdBufPrev(ctx *ExecContext) error {
	if ctx.PrevBuffer == nil {
		return errors.New("bprev: not available")
	}
	return ctx.PrevBuffer()
}

func cmdBufDelete(ctx *ExecContext) error {
	if ctx.DeleteBuffer == nil {
		return errors.New("bdelete: not available")
	}
	return ctx.DeleteBuffer(ctx.Bang)
}

func cmdListBufs(ctx *ExecContext) error {
	if ctx.ListBuffers == nil {
		return errors.New("ls: not available")
	}
	ctx.ListBuffers(ctx.Output)
	return nil
}

func cmdSet(ctx *ExecContext) error {
	if ctx.SetOption == nil {
		return errors.New("set: not available")
	}
	return ctx.SetOption(ctx.Args)
}

func cmdMap(ctx *ExecContext) error {
	if ctx.CreateMapping == nil {
		return errors.New("map: not available")
	}
	return ctx.CreateMapping(ctx.Args)
}

func cmdSource(ctx *ExecContext) error {
	if ctx.SourceFile == nil {
		return errors.New("source: not available")
	}
	return ctx.SourceFile(ctx.Args)
}

func cmdLua(ctx *ExecContext) error {
	if ctx.RunLua == nil {
		return errors.New("lua: not available")
	}
	return ctx.RunLua(ctx.Args)
}

func cmdShell(ctx *ExecContext) error {
	if ctx.ShellCommand == nil {
		return errors.New("!: not available")
	}
	output, err := ctx.ShellCommand(ctx.Args)
	if err != nil {
		return err
	}
	if ctx.Output != nil {
		ctx.Output.WriteString(output)
	}
	return nil
}

func cmdNoHL(ctx *ExecContext) error {
	if ctx.ClearHLSearch == nil {
		return errors.New("nohlsearch: not available")
	}
	ctx.ClearHLSearch()
	return nil
}

func cmdSubstitute(ctx *ExecContext) error {
	if ctx.Substitute == nil {
		return errors.New("substitute: not available")
	}
	return ctx.Substitute(ctx.Args, ctx.Range)
}
