// Package register implements the vim register store for the editor.
// Registers hold yanked and deleted text, system clipboard content,
// macro recordings, and special read-only values.
package register

import "sync"

// maxRegisterContent is the maximum number of runes stored in any single
// register. Derived from a reasonable upper bound on content that might be
// yanked or deleted in a single operation (approx 1 MB of UTF-8 text).
const maxRegisterContent = 1 << 20

// numberedRegisterCount is the number of numbered registers (0-9).
const numberedRegisterCount = 10

// Entry holds the content and metadata for a single register.
type Entry struct {
	Content  string
	Linewise bool
}

// RegisterType classifies a register name for dispatch.
type RegisterType int

const (
	TypeNamed     RegisterType = iota // a-z
	TypeAppend                        // A-Z (appends to lowercase)
	TypeNumbered                      // 0-9
	TypeSmallDel                      // "-
	TypeUnnamed                       // ""
	TypeYank                          // "0
	TypeClipboard                     // "* and "+
	TypeReadOnly                      // ". ": "% "#
	TypeSearch                        // "/
	TypeExpression                    // "=
	TypeUnknown
)

// registerTypeEntry maps a classification predicate to its type.
type registerTypeEntry struct {
	test func(r rune) bool
	typ  RegisterType
}

// registerTypeTable is checked in order; first match wins.
var registerTypeTable = []registerTypeEntry{
	{test: isLowerAlpha, typ: TypeNamed},
	{test: isUpperAlpha, typ: TypeAppend},
	{test: isDigit, typ: TypeNumbered},
	{test: func(r rune) bool { return r == '-' }, typ: TypeSmallDel},
	{test: func(r rune) bool { return r == '"' }, typ: TypeUnnamed},
	{test: func(r rune) bool { return r == '*' || r == '+' }, typ: TypeClipboard},
	{test: func(r rune) bool { return r == '.' || r == ':' || r == '%' || r == '#' }, typ: TypeReadOnly},
	{test: func(r rune) bool { return r == '/' }, typ: TypeSearch},
	{test: func(r rune) bool { return r == '=' }, typ: TypeExpression},
}

// classifyRegister returns the type for a register name rune.
func classifyRegister(r rune) RegisterType {
	for _, entry := range registerTypeTable {
		if entry.test(r) {
			return entry.typ
		}
	}
	return TypeUnknown
}

func isLowerAlpha(r rune) bool { return r >= 'a' && r <= 'z' }
func isUpperAlpha(r rune) bool { return r >= 'A' && r <= 'Z' }
func isDigit(r rune) bool      { return r >= '0' && r <= '9' }

// Store holds all vim registers with thread-safe access.
type Store struct {
	mu       sync.RWMutex
	named    map[rune]Entry
	numbered [numberedRegisterCount]Entry
	special  map[rune]Entry
	unnamed  Entry
}

// NewStore creates an empty register store.
func NewStore() *Store {
	return &Store{
		named:   make(map[rune]Entry, 26),
		special: make(map[rune]Entry, 8),
	}
}

// Get retrieves the content and linewise flag for a register.
func (s *Store) Get(name rune) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLocked(name)
}

// getLocked retrieves a register without locking (caller must hold lock).
func (s *Store) getLocked(name rune) (string, bool) {
	typ := classifyRegister(name)
	getter, ok := getterTable[typ]
	if !ok {
		return "", false
	}
	return getter(s, name)
}

// getterFunc retrieves content for a register type.
type getterFunc func(s *Store, name rune) (string, bool)

// getterTable dispatches Get by register type.
var getterTable = map[RegisterType]getterFunc{
	TypeNamed:     getNamed,
	TypeAppend:    getAppendAsNamed,
	TypeNumbered:  getNumbered,
	TypeSmallDel:  getSpecial,
	TypeUnnamed:   getUnnamed,
	TypeClipboard: getSpecial,
	TypeReadOnly:  getSpecial,
	TypeSearch:    getSpecial,
}

func getNamed(s *Store, name rune) (string, bool) {
	e, ok := s.named[name]
	return e.Content, ok && e.Linewise
}

func getAppendAsNamed(s *Store, name rune) (string, bool) {
	lower := name - 'A' + 'a'
	return getNamed(s, lower)
}

func getNumbered(s *Store, name rune) (string, bool) {
	idx := int(name - '0')
	e := s.numbered[idx]
	return e.Content, e.Linewise
}

func getSpecial(s *Store, name rune) (string, bool) {
	e, ok := s.special[name]
	return e.Content, ok && e.Linewise
}

func getUnnamed(s *Store, _ rune) (string, bool) {
	return s.unnamed.Content, s.unnamed.Linewise
}

// Set writes content to a register, respecting type semantics.
func (s *Store) Set(name rune, content string, linewise bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLocked(name, content, linewise)
}

// setLocked sets a register without locking (caller must hold lock).
func (s *Store) setLocked(name rune, content string, linewise bool) {
	clamped := clampContent(content)
	entry := Entry{Content: clamped, Linewise: linewise}
	typ := classifyRegister(name)
	setter, ok := setterTable[typ]
	if !ok {
		return
	}
	setter(s, name, entry)
}

// setterFunc stores an entry for a register type.
type setterFunc func(s *Store, name rune, entry Entry)

// setterTable dispatches Set by register type.
var setterTable = map[RegisterType]setterFunc{
	TypeNamed:     setNamed,
	TypeAppend:    setAppend,
	TypeNumbered:  setNumbered,
	TypeSmallDel:  setSpecial,
	TypeUnnamed:   setUnnamed,
	TypeClipboard: setSpecial,
	TypeSearch:    setSpecial,
}

func setNamed(s *Store, name rune, entry Entry) {
	s.named[name] = entry
	s.unnamed = entry
}

func setAppend(s *Store, name rune, entry Entry) {
	lower := name - 'A' + 'a'
	existing := s.named[lower]
	combined := clampContent(existing.Content + entry.Content)
	merged := Entry{Content: combined, Linewise: entry.Linewise || existing.Linewise}
	s.named[lower] = merged
	s.unnamed = merged
}

func setNumbered(s *Store, name rune, entry Entry) {
	idx := int(name - '0')
	s.numbered[idx] = entry
}

func setSpecial(s *Store, name rune, entry Entry) {
	s.special[name] = entry
}

func setUnnamed(s *Store, _ rune, entry Entry) {
	s.unnamed = entry
}

// RecordYank stores yanked content in register "0 and the unnamed register.
func (s *Store) RecordYank(content string, linewise bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clamped := clampContent(content)
	entry := Entry{Content: clamped, Linewise: linewise}
	s.numbered[0] = entry
	s.unnamed = entry
}

// RecordDelete stores deleted content, shifting numbered registers 1-9 and
// routing small deletes (less than one line) to the "- register.
func (s *Store) RecordDelete(content string, linewise bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clamped := clampContent(content)
	entry := Entry{Content: clamped, Linewise: linewise}
	if !linewise && !containsNewline(clamped) {
		s.special['-'] = entry
		s.unnamed = entry
		return
	}
	s.shiftNumberedRegisters(entry)
	s.unnamed = entry
}

// shiftNumberedRegisters pushes a new entry into "1 and shifts "1 -> "2, etc.
func (s *Store) shiftNumberedRegisters(entry Entry) {
	// Shift 8 -> 9, 7 -> 8, ..., 1 -> 2.
	for i := numberedRegisterCount - 1; i > 1; i-- {
		s.numbered[i] = s.numbered[i-1]
	}
	s.numbered[1] = entry
}

// clampContent truncates content to maxRegisterContent runes.
func clampContent(content string) string {
	runes := []rune(content)
	if len(runes) <= maxRegisterContent {
		return content
	}
	return string(runes[:maxRegisterContent])
}

// containsNewline reports whether the string contains a newline character.
func containsNewline(s string) bool {
	for _, r := range s {
		if r == '\n' {
			return true
		}
	}
	return false
}
