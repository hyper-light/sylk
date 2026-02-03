package lua

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"

	glua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Capacity limits (derived from practical plugin usage patterns)
// ---------------------------------------------------------------------------

// maxAutocmds caps the number of autocommand registrations.
const maxAutocmds = 512

// maxAugroups caps the number of autocommand groups.
const maxAugroups = 64

// Sentinel errors.
var (
	errAutocmdsFull = errors.New("autocmd store full")
	errAugroupsFull = errors.New("augroup store full")
)

// ---------------------------------------------------------------------------
// AutocmdEvent
// ---------------------------------------------------------------------------

// AutocmdEvent is a string identifying a Neovim-compatible event.
type AutocmdEvent string

// Supported autocommand events.
const (
	BufEnter       AutocmdEvent = "BufEnter"
	BufLeave       AutocmdEvent = "BufLeave"
	BufWritePre    AutocmdEvent = "BufWritePre"
	BufWritePost   AutocmdEvent = "BufWritePost"
	BufNewFile     AutocmdEvent = "BufNewFile"
	BufReadPost    AutocmdEvent = "BufReadPost"
	WinEnter       AutocmdEvent = "WinEnter"
	WinLeave       AutocmdEvent = "WinLeave"
	CursorMoved    AutocmdEvent = "CursorMoved"
	CursorMovedI   AutocmdEvent = "CursorMovedI"
	InsertEnter    AutocmdEvent = "InsertEnter"
	InsertLeave    AutocmdEvent = "InsertLeave"
	ModeChanged    AutocmdEvent = "ModeChanged"
	TextChanged    AutocmdEvent = "TextChanged"
	TextChangedI   AutocmdEvent = "TextChangedI"
	VimEnter       AutocmdEvent = "VimEnter"
	VimLeave       AutocmdEvent = "VimLeave"
	FileType       AutocmdEvent = "FileType"
	ColorScheme    AutocmdEvent = "ColorScheme"
	UserEvent      AutocmdEvent = "User"
)

// validEvents is the set of all recognised events for O(1) validation.
var validEvents = map[AutocmdEvent]struct{}{
	BufEnter: {}, BufLeave: {}, BufWritePre: {}, BufWritePost: {},
	BufNewFile: {}, BufReadPost: {}, WinEnter: {}, WinLeave: {},
	CursorMoved: {}, CursorMovedI: {}, InsertEnter: {}, InsertLeave: {},
	ModeChanged: {}, TextChanged: {}, TextChangedI: {}, VimEnter: {},
	VimLeave: {}, FileType: {}, ColorScheme: {}, UserEvent: {},
}

// ---------------------------------------------------------------------------
// Autocmd / AugroupDef
// ---------------------------------------------------------------------------

// Autocmd describes a single autocommand registration.
type Autocmd struct {
	ID       int
	Event    AutocmdEvent
	Pattern  string
	Group    string
	Callback any // string (ex-cmd) or *glua.LFunction
	Once     bool
	Desc     string
}

// AugroupDef describes an autocommand group.
type AugroupDef struct {
	ID    int
	Name  string
	Clear bool
}

// ---------------------------------------------------------------------------
// autocmdOptField — table-driven opts parser
// ---------------------------------------------------------------------------

// autocmdOptField maps an option-table key to a setter on the Autocmd.
type autocmdOptField struct {
	Key   string
	Apply func(ac *Autocmd, val glua.LValue)
}

// autocmdOptFields is the table-driven opts parser for nvim_create_autocmd.
var autocmdOptFields = []autocmdOptField{
	{Key: "pattern", Apply: func(ac *Autocmd, v glua.LValue) { ac.Pattern = v.String() }},
	{Key: "group", Apply: func(ac *Autocmd, v glua.LValue) { ac.Group = v.String() }},
	{Key: "once", Apply: func(ac *Autocmd, v glua.LValue) { ac.Once = luaToBool(v) }},
	{Key: "desc", Apply: func(ac *Autocmd, v glua.LValue) { ac.Desc = v.String() }},
	{Key: "callback", Apply: func(ac *Autocmd, v glua.LValue) { ac.Callback = resolveAutocmdCallback(v) }},
	{Key: "command", Apply: func(ac *Autocmd, v glua.LValue) { ac.Callback = v.String() }},
}

// resolveAutocmdCallback converts a Lua value to a callback representation.
func resolveAutocmdCallback(v glua.LValue) any {
	if fn, ok := v.(*glua.LFunction); ok {
		return fn
	}
	return v.String()
}

// ---------------------------------------------------------------------------
// AutocmdStore
// ---------------------------------------------------------------------------

// AutocmdStore manages all autocommand and augroup registrations.
type AutocmdStore struct {
	mu       sync.RWMutex
	autocmds map[int]Autocmd
	groups   map[string]AugroupDef
	nextID   atomic.Int64
	groupSeq atomic.Int64
}

// NewAutocmdStore returns a ready-to-use store.
func NewAutocmdStore() *AutocmdStore {
	return &AutocmdStore{
		autocmds: make(map[int]Autocmd, maxAutocmds),
		groups:   make(map[string]AugroupDef, maxAugroups),
	}
}

// Create registers a new autocommand and returns its unique ID.
func (as *AutocmdStore) Create(ac Autocmd) (int, error) {
	as.mu.Lock()
	defer as.mu.Unlock()
	if len(as.autocmds) >= maxAutocmds {
		return 0, errAutocmdsFull
	}
	id := int(as.nextID.Add(1))
	ac.ID = id
	as.autocmds[id] = ac
	return id, nil
}

// Delete removes an autocommand by ID.
func (as *AutocmdStore) Delete(id int) {
	as.mu.Lock()
	defer as.mu.Unlock()
	delete(as.autocmds, id)
}

// CreateGroup registers or resets an augroup and returns its unique ID.
func (as *AutocmdStore) CreateGroup(name string, clear bool) (int, error) {
	as.mu.Lock()
	defer as.mu.Unlock()
	if existing, ok := as.groups[name]; ok {
		if clear {
			as.clearGroupLocked(name)
		}
		return existing.ID, nil
	}
	if len(as.groups) >= maxAugroups {
		return 0, errAugroupsFull
	}
	id := int(as.groupSeq.Add(1))
	as.groups[name] = AugroupDef{ID: id, Name: name, Clear: clear}
	return id, nil
}

// DeleteGroup removes a group and all its autocommands.
func (as *AutocmdStore) DeleteGroup(name string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.clearGroupLocked(name)
	delete(as.groups, name)
}

// clearGroupLocked removes all autocmds belonging to the named group.
// Caller MUST hold as.mu.
func (as *AutocmdStore) clearGroupLocked(name string) {
	for id, ac := range as.autocmds {
		if ac.Group == name {
			delete(as.autocmds, id)
		}
	}
}

// Fire triggers every autocmd matching the event and pattern.  "Once"
// autocmds are removed after firing.  Returns an aggregate of all errors.
func (as *AutocmdStore) Fire(event AutocmdEvent, data map[string]any, rt *Runtime) error {
	as.mu.RLock()
	// Collect matching autocmds under read lock; execute after release.
	type match struct {
		ac   Autocmd
		once bool
	}
	matches := make([]match, 0, len(as.autocmds))
	for _, ac := range as.autocmds {
		if ac.Event != event {
			continue
		}
		if !patternMatches(ac.Pattern, data) {
			continue
		}
		matches = append(matches, match{ac: ac, once: ac.Once})
	}
	as.mu.RUnlock()

	var errs []error
	for _, m := range matches {
		if err := fireOne(m.ac, data, rt); err != nil {
			errs = append(errs, err)
		}
		if m.once {
			as.Delete(m.ac.ID)
		}
	}
	return errors.Join(errs...)
}

// patternMatches checks whether an autocmd pattern matches the event data.
func patternMatches(pattern string, data map[string]any) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	file, _ := data["file"].(string)
	if file == "" {
		return true
	}
	matched, err := filepath.Match(pattern, file)
	if err != nil {
		return false
	}
	return matched
}

// fireOne executes a single autocmd callback.
func fireOne(ac Autocmd, data map[string]any, rt *Runtime) error {
	if fn, ok := ac.Callback.(*glua.LFunction); ok {
		return fireLuaCallback(fn, data, rt)
	}
	if cmd, ok := ac.Callback.(string); ok {
		return rt.editor.Command(cmd)
	}
	return nil
}

// fireLuaCallback invokes a Lua function autocmd handler.
// It uses buildEventTable + CallFunction to avoid accessing rt.State()
// without holding the runtime mutex.
func fireLuaCallback(fn *glua.LFunction, data map[string]any, rt *Runtime) error {
	rt.mu.Lock()
	tbl := buildEventTable(rt.state, data)
	rt.mu.Unlock()

	_, err := rt.CallFunction(fn, tbl)
	return err
}

// buildEventTable constructs a Lua table from autocmd event data.
// Caller MUST hold rt.mu.
func buildEventTable(L *glua.LState, data map[string]any) *glua.LTable {
	tbl := L.NewTable()
	for k, v := range data {
		tbl.RawSetString(k, goToLua(L, v))
	}
	return tbl
}

// ---------------------------------------------------------------------------
// Lua registration
// ---------------------------------------------------------------------------

// registerAutocmdFunctions attaches autocmd API functions to vim.api.
func registerAutocmdFunctions(L *glua.LState, apiTbl *glua.LTable, rt *Runtime) {
	type registration struct {
		Name    string
		Handler func(*glua.LState) int
	}
	regs := []registration{
		{Name: "nvim_create_autocmd", Handler: func(ls *glua.LState) int { return luaCreateAutocmd(ls, rt) }},
		{Name: "nvim_del_autocmd", Handler: func(ls *glua.LState) int { return luaDelAutocmd(ls, rt) }},
		{Name: "nvim_create_augroup", Handler: func(ls *glua.LState) int { return luaCreateAugroup(ls, rt) }},
		{Name: "nvim_del_augroup_by_name", Handler: func(ls *glua.LState) int { return luaDelAugroup(ls, rt) }},
	}
	for _, r := range regs {
		fn := r.Handler
		apiTbl.RawSetString(r.Name, L.NewFunction(func(ls *glua.LState) int {
			return fn(ls)
		}))
	}
}

// luaCreateAutocmd implements nvim_create_autocmd(event, opts).
func luaCreateAutocmd(L *glua.LState, rt *Runtime) int {
	eventStr := L.ToString(1)
	event := AutocmdEvent(eventStr)
	if _, ok := validEvents[event]; !ok {
		L.Push(glua.LNumber(-1))
		return 1
	}

	ac := Autocmd{Event: event}

	if opts, ok := L.Get(2).(*glua.LTable); ok {
		applyAutocmdOpts(&ac, opts)
	}

	id, err := rt.Autocmds.Create(ac)
	if err != nil {
		L.Push(glua.LNumber(-1))
		return 1
	}
	L.Push(glua.LNumber(id))
	return 1
}

// applyAutocmdOpts walks autocmdOptFields and applies matching entries.
func applyAutocmdOpts(ac *Autocmd, opts *glua.LTable) {
	for _, f := range autocmdOptFields {
		val := opts.RawGetString(f.Key)
		if val != glua.LNil {
			f.Apply(ac, val)
		}
	}
}

func luaDelAutocmd(L *glua.LState, rt *Runtime) int {
	id := L.ToInt(1)
	rt.Autocmds.Delete(id)
	return 0
}

func luaCreateAugroup(L *glua.LState, rt *Runtime) int {
	name := L.ToString(1)
	clear := false
	if opts, ok := L.Get(2).(*glua.LTable); ok {
		val := opts.RawGetString("clear")
		clear = luaToBool(val)
	}
	id, err := rt.Autocmds.CreateGroup(name, clear)
	if err != nil {
		L.Push(glua.LNumber(-1))
		return 1
	}
	L.Push(glua.LNumber(id))
	return 1
}

func luaDelAugroup(L *glua.LState, rt *Runtime) int {
	name := L.ToString(1)
	rt.Autocmds.DeleteGroup(name)
	return 0
}
