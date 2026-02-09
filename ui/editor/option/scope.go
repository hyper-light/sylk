package option

import (
	"errors"
	"strconv"
	"strings"
	"sync"
)

// ScopeLevel indicates where an option value is stored.
type ScopeLevel int

const (
	// ScopeGlobal applies to the entire editor.
	ScopeGlobal ScopeLevel = iota
	// ScopeBuffer applies per-buffer.
	ScopeBuffer
	// ScopeWindow applies per-window.
	ScopeWindow
)

// maxOptionsPerScope caps each scope map to prevent unbounded growth.
// Derived from the built-in option count (~24) with headroom for user additions.
const maxOptionsPerScope = 128

// Store holds option values at three scope levels with cascading resolution:
// window-local overrides buffer-local overrides global overrides default.
type Store struct {
	mu      sync.RWMutex
	global  map[string]any
	buffers map[int]map[string]any // keyed by buffer ID
	windows map[int]map[string]any // keyed by window ID
}

// NewStore creates an empty option store.
func NewStore() *Store {
	return &Store{
		global:  make(map[string]any, maxOptionsPerScope),
		buffers: make(map[int]map[string]any),
		windows: make(map[int]map[string]any),
	}
}

// SetGlobal sets a global-scope option value.
func (s *Store) SetGlobal(name string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setInMap(s.global, name, value)
}

// Set stores an option value at the given scope for the specified target ID.
// For ScopeGlobal the targetID is ignored.
func (s *Store) Set(name string, value any, scope ScopeLevel, targetID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	setter, ok := scopeSetterTable[scope]
	if !ok {
		return
	}
	setter(s, name, value, targetID)
}

// scopeSetterFunc writes a value for a given scope.
type scopeSetterFunc func(s *Store, name string, value any, targetID int)

// scopeSetterTable dispatches Set by scope.
var scopeSetterTable = map[ScopeLevel]scopeSetterFunc{
	ScopeGlobal: setGlobalScope,
	ScopeBuffer: setBufferScope,
	ScopeWindow: setWindowScope,
}

func setGlobalScope(s *Store, name string, value any, _ int) {
	s.setInMap(s.global, name, value)
}

func setBufferScope(s *Store, name string, value any, id int) {
	m := s.ensureScopeMap(s.buffers, id)
	s.setInMap(m, name, value)
}

func setWindowScope(s *Store, name string, value any, id int) {
	m := s.ensureScopeMap(s.windows, id)
	s.setInMap(m, name, value)
}

// Get checks window -> buffer -> global for the named option and returns the
// most specific value found.
func (s *Store) Get(name string, bufferID, windowID int) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Resolution order: window-local, buffer-local, global.
	if v, ok := s.lookupInTarget(s.windows, windowID, name); ok {
		return v, true
	}
	if v, ok := s.lookupInTarget(s.buffers, bufferID, name); ok {
		return v, true
	}
	if v, ok := s.global[name]; ok {
		return v, true
	}
	return nil, false
}

// GetEffective resolves through the scoping chain with a fallback to the
// option definition's default value.
func (s *Store) GetEffective(name string, bufferID, windowID int) any {
	if v, ok := s.Get(name, bufferID, windowID); ok {
		return v
	}
	def, ok := LookupDef(name)
	if !ok {
		return nil
	}
	return def.Default
}

// RemoveBuffer purges all buffer-local options for a given buffer ID.
func (s *Store) RemoveBuffer(bufferID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.buffers, bufferID)
}

// RemoveWindow purges all window-local options for a given window ID.
func (s *Store) RemoveWindow(windowID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.windows, windowID)
}

// ---------------------------------------------------------------------------
// :set argument parsing
// ---------------------------------------------------------------------------

// setArgAction classifies the kind of :set argument.
type setArgAction int

const (
	actionSet     setArgAction = iota // :set name=value or :set name
	actionUnset                       // :set noname
	actionQuery                       // :set name?
	actionToggle                      // :set name! or :set invname
)

// setArgEntry maps a pattern test to an action.
type setArgEntry struct {
	test   func(arg string) bool
	action setArgAction
}

// setArgTable is checked in order; first match wins.
var setArgTable = []setArgEntry{
	{test: func(a string) bool { return strings.HasSuffix(a, "?") }, action: actionQuery},
	{test: func(a string) bool { return strings.HasSuffix(a, "!") }, action: actionToggle},
	{test: func(a string) bool { return strings.HasPrefix(a, "inv") }, action: actionToggle},
	{test: func(a string) bool { return strings.HasPrefix(a, "no") }, action: actionUnset},
	{test: func(a string) bool { return strings.Contains(a, "=") }, action: actionSet},
	{test: func(_ string) bool { return true }, action: actionSet},
}

// classifySetArg returns the action for a :set argument string.
func classifySetArg(arg string) setArgAction {
	for _, entry := range setArgTable {
		if entry.test(arg) {
			return entry.action
		}
	}
	return actionSet
}

// ParseSetResult holds the parsed :set argument.
type ParseSetResult struct {
	Name   string
	Value  any
	Query  bool
	Toggle bool
}

// ParseSetArgs parses a single :set argument into its name, value, and action.
// Supported forms:
//
//	:set expandtab          (boolean true)
//	:set noexpandtab        (boolean false)
//	:set shiftwidth=4       (integer assignment)
//	:set encoding=utf-8     (string assignment)
//	:set number?            (query)
//	:set number!            (toggle boolean)
//	:set invnumber          (toggle boolean)
func ParseSetArgs(args string) (*ParseSetResult, error) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		return nil, errors.New("set: empty argument")
	}
	action := classifySetArg(arg)
	handler, ok := parseActionTable[action]
	if !ok {
		return nil, errors.New("set: unknown action")
	}
	return handler(arg)
}

// parseActionHandler resolves a :set argument string into a ParseSetResult.
type parseActionHandler func(arg string) (*ParseSetResult, error)

// parseActionTable dispatches parsing by action type.
var parseActionTable = map[setArgAction]parseActionHandler{
	actionQuery:  parseQuery,
	actionToggle: parseToggle,
	actionUnset:  parseUnset,
	actionSet:    parseSetValue,
}

func parseQuery(arg string) (*ParseSetResult, error) {
	name := strings.TrimSuffix(arg, "?")
	return &ParseSetResult{Name: name, Query: true}, nil
}

func parseToggle(arg string) (*ParseSetResult, error) {
	name := strings.TrimSuffix(arg, "!")
	if strings.HasPrefix(name, "inv") {
		name = strings.TrimPrefix(name, "inv")
	}
	return &ParseSetResult{Name: name, Toggle: true}, nil
}

func parseUnset(arg string) (*ParseSetResult, error) {
	name := strings.TrimPrefix(arg, "no")
	return &ParseSetResult{Name: name, Value: false}, nil
}

func parseSetValue(arg string) (*ParseSetResult, error) {
	name, valStr, hasEquals := strings.Cut(arg, "=")
	if !hasEquals {
		// Bare name: boolean true.
		return &ParseSetResult{Name: name, Value: true}, nil
	}
	def, ok := LookupDef(name)
	if !ok {
		return &ParseSetResult{Name: name, Value: valStr}, nil
	}
	val, err := coerceValue(valStr, def.Type)
	if err != nil {
		return nil, err
	}
	return &ParseSetResult{Name: name, Value: val}, nil
}

// coerceValue converts a string to the target OptionType.
func coerceValue(raw string, typ OptionType) (any, error) {
	coercer, ok := coerceTable[typ]
	if !ok {
		return raw, nil
	}
	return coercer(raw)
}

// coerceFunc converts a raw string to the appropriate Go value.
type coerceFunc func(raw string) (any, error)

// coerceTable dispatches coercion by option type.
var coerceTable = map[OptionType]coerceFunc{
	TypeBool:   coerceBool,
	TypeInt:    coerceInt,
	TypeString: coerceString,
}

func coerceBool(raw string) (any, error) {
	return strconv.ParseBool(raw)
}

func coerceInt(raw string) (any, error) {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, errors.New("set: expected integer value")
	}
	return v, nil
}

func coerceString(raw string) (any, error) {
	return raw, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// setInMap inserts a value into a scope map, respecting the capacity bound.
func (s *Store) setInMap(m map[string]any, name string, value any) {
	if _, exists := m[name]; !exists && len(m) >= maxOptionsPerScope {
		return
	}
	m[name] = value
}

// ensureScopeMap returns or creates the per-target map for a scope.
func (s *Store) ensureScopeMap(targets map[int]map[string]any, id int) map[string]any {
	m, ok := targets[id]
	if ok {
		return m
	}
	m = make(map[string]any, len(builtinOptions))
	targets[id] = m
	return m
}

// lookupInTarget searches a per-target scope for a named option.
func (s *Store) lookupInTarget(targets map[int]map[string]any, id int, name string) (any, bool) {
	m, ok := targets[id]
	if !ok {
		return nil, false
	}
	v, found := m[name]
	return v, found
}
