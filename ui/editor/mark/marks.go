// Package mark implements vim-style buffer marks and global marks for
// the editor. Marks record cursor positions that the user can jump to.
package mark

import "sync"

// namedMarkCapacity is the maximum number of named marks (a-z plus A-Z).
const namedMarkCapacity = 52

// specialMarkCapacity is the number of special mark slots.
const specialMarkCapacity = 8

// BufferMark records a cursor position within a buffer.
type BufferMark struct {
	Line int
	Col  int
}

// MarkType classifies a mark name for dispatch.
type MarkType int

const (
	TypeBuffer  MarkType = iota // a-z (buffer-local)
	TypeGlobal                  // A-Z (global across buffers)
	TypeSpecial                 // ' . < > [ ]
	TypeUnknown
)

// markTypeEntry maps a classification predicate to its type.
type markTypeEntry struct {
	test func(r rune) bool
	typ  MarkType
}

// markTypeTable is checked in order; first match wins.
var markTypeTable = []markTypeEntry{
	{test: isLowerAlpha, typ: TypeBuffer},
	{test: isUpperAlpha, typ: TypeGlobal},
	{test: isSpecialMark, typ: TypeSpecial},
}

func isLowerAlpha(r rune) bool { return r >= 'a' && r <= 'z' }
func isUpperAlpha(r rune) bool { return r >= 'A' && r <= 'Z' }

// specialMarkSet holds the valid special mark characters.
var specialMarkSet = map[rune]bool{
	'\'': true, // previous position
	'.':  true, // last edit
	'<':  true, // visual selection start
	'>':  true, // visual selection end
	'[':  true, // last change start
	']':  true, // last change end
	'"':  true, // last exit position
	'^':  true, // last insert position
}

func isSpecialMark(r rune) bool { return specialMarkSet[r] }

// classifyMark returns the type for a mark name rune.
func classifyMark(r rune) MarkType {
	for _, entry := range markTypeTable {
		if entry.test(r) {
			return entry.typ
		}
	}
	return TypeUnknown
}

// Store holds all buffer and global marks with thread-safe access.
type Store struct {
	mu      sync.RWMutex
	buffer  map[rune]BufferMark
	global  map[rune]BufferMark
	special map[rune]BufferMark
}

// NewStore creates an empty mark store with pre-allocated maps.
func NewStore() *Store {
	return &Store{
		buffer:  make(map[rune]BufferMark, namedMarkCapacity/2),
		global:  make(map[rune]BufferMark, namedMarkCapacity/2),
		special: make(map[rune]BufferMark, specialMarkCapacity),
	}
}

// Set places a mark at the given line and column.
func (s *Store) Set(name rune, line, col int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mark := BufferMark{Line: line, Col: col}
	typ := classifyMark(name)
	setter, ok := markSetterTable[typ]
	if !ok {
		return
	}
	setter(s, name, mark)
}

// markSetterFunc stores a mark for a given type.
type markSetterFunc func(s *Store, name rune, mark BufferMark)

// markSetterTable dispatches Set by mark type.
var markSetterTable = map[MarkType]markSetterFunc{
	TypeBuffer:  setBuffer,
	TypeGlobal:  setGlobal,
	TypeSpecial: setSpecialMark,
}

func setBuffer(s *Store, name rune, mark BufferMark) {
	s.buffer[name] = mark
}

func setGlobal(s *Store, name rune, mark BufferMark) {
	s.global[name] = mark
}

func setSpecialMark(s *Store, name rune, mark BufferMark) {
	s.special[name] = mark
}

// Get retrieves the mark position for the given name.
func (s *Store) Get(name rune) (BufferMark, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	typ := classifyMark(name)
	getter, ok := markGetterTable[typ]
	if !ok {
		return BufferMark{}, false
	}
	return getter(s, name)
}

// markGetterFunc retrieves a mark for a given type.
type markGetterFunc func(s *Store, name rune) (BufferMark, bool)

// markGetterTable dispatches Get by mark type.
var markGetterTable = map[MarkType]markGetterFunc{
	TypeBuffer:  getBuffer,
	TypeGlobal:  getGlobal,
	TypeSpecial: getSpecialMark,
}

func getBuffer(s *Store, name rune) (BufferMark, bool) {
	m, ok := s.buffer[name]
	return m, ok
}

func getGlobal(s *Store, name rune) (BufferMark, bool) {
	m, ok := s.global[name]
	return m, ok
}

func getSpecialMark(s *Store, name rune) (BufferMark, bool) {
	m, ok := s.special[name]
	return m, ok
}

// SetSpecial sets a special mark. This is a convenience wrapper that
// validates the name against the special mark set.
func (s *Store) SetSpecial(name rune, line, col int) {
	if !isSpecialMark(name) {
		return
	}
	s.Set(name, line, col)
}

// Delete removes a mark by name. Only buffer and global marks can be
// explicitly deleted.
func (s *Store) Delete(name rune) {
	s.mu.Lock()
	defer s.mu.Unlock()
	typ := classifyMark(name)
	deleter, ok := markDeleterTable[typ]
	if !ok {
		return
	}
	deleter(s, name)
}

// markDeleterFunc removes a mark for a given type.
type markDeleterFunc func(s *Store, name rune)

// markDeleterTable dispatches Delete by mark type.
var markDeleterTable = map[MarkType]markDeleterFunc{
	TypeBuffer: deleteBuffer,
	TypeGlobal: deleteGlobal,
}

func deleteBuffer(s *Store, name rune) { delete(s.buffer, name) }
func deleteGlobal(s *Store, name rune) { delete(s.global, name) }

// All returns all set marks as a slice of name-mark pairs, suitable for
// display in a :marks listing.
func (s *Store) All() []NamedMark {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Pre-allocate for the worst case.
	result := make([]NamedMark, 0, len(s.buffer)+len(s.global)+len(s.special))
	for name, mark := range s.buffer {
		result = append(result, NamedMark{Name: name, Mark: mark})
	}
	for name, mark := range s.global {
		result = append(result, NamedMark{Name: name, Mark: mark})
	}
	for name, mark := range s.special {
		result = append(result, NamedMark{Name: name, Mark: mark})
	}
	return result
}

// NamedMark pairs a mark name with its position.
type NamedMark struct {
	Name rune
	Mark BufferMark
}
