package command

import (
	"errors"
	"strings"
	"sync"
)

// maxRegisteredCommands caps the registry to prevent unbounded growth.
// Derived from typical vim command count with headroom.
const maxRegisteredCommands = 256

// Handler is the function signature for command execution.
type Handler func(ctx *ExecContext) error

// ExecContext carries the execution environment for a command handler.
// Callback fields delegate actual work to the editor layer, avoiding
// direct package imports that would create dependency cycles.
type ExecContext struct {
	Range  *Range
	Bang   bool
	Args   string
	Output *strings.Builder

	// Editor callbacks — set by the wiring layer before dispatch.
	WriteBuffer     func(path string) error
	CloseWindow     func(force bool) error
	OpenFile        func(path string) error
	SplitHorizontal func() error
	SplitVertical   func() error
	NextBuffer      func() error
	PrevBuffer      func() error
	DeleteBuffer    func(force bool) error
	ListBuffers     func(out *strings.Builder)
	SetOption       func(args string) error
	CreateMapping   func(args string) error
	SourceFile      func(path string) error
	RunLua          func(code string) error
	ShellCommand    func(cmd string) (string, error)
	ClearHLSearch   func()
	Substitute      func(args string, r *Range) error
	GetLines        func(start, end int) []string
}

// CommandDef defines a single command that can be registered.
type CommandDef struct {
	Name         string
	Handler      Handler
	RangeAllowed bool
	BangAllowed  bool
}

// Registry holds all registered commands with thread-safe access.
type Registry struct {
	mu   sync.RWMutex
	defs []CommandDef
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{
		defs: make([]CommandDef, 0, maxRegisteredCommands),
	}
}

// Register adds a command definition to the registry.
// Returns an error if the registry is full.
func (r *Registry) Register(def CommandDef) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.defs) >= maxRegisteredCommands {
		return errors.New("registry: command limit reached")
	}
	r.defs = append(r.defs, def)
	return nil
}

// Lookup resolves a command name using exact match first, then unambiguous
// prefix matching.
func (r *Registry) Lookup(name string) (*CommandDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Exact match.
	for i := range r.defs {
		if r.defs[i].Name == name {
			return &r.defs[i], true
		}
	}
	// Unambiguous prefix match.
	return r.prefixMatch(name)
}

// prefixMatch finds a single command whose name starts with the given prefix.
// Returns nil if zero or more than one command matches (ambiguous).
// Caller must hold at least a read lock.
func (r *Registry) prefixMatch(prefix string) (*CommandDef, bool) {
	var match *CommandDef
	for i := range r.defs {
		if !strings.HasPrefix(r.defs[i].Name, prefix) {
			continue
		}
		if match != nil {
			return nil, false // ambiguous
		}
		match = &r.defs[i]
	}
	if match == nil {
		return nil, false
	}
	return match, true
}

// Execute looks up the parsed command and invokes its handler.
func (r *Registry) Execute(parsed *ParsedCommand, ctx *ExecContext) error {
	if parsed.Name == "" {
		return nil
	}
	def, ok := r.Lookup(parsed.Name)
	if !ok {
		return errors.New("unknown command: " + parsed.Name)
	}
	if err := validateFlags(parsed, def); err != nil {
		return err
	}
	return def.Handler(ctx)
}

// validateFlags checks that bang and range usage are permitted.
func validateFlags(parsed *ParsedCommand, def *CommandDef) error {
	if parsed.Bang && !def.BangAllowed {
		return errors.New(def.Name + ": trailing ! not allowed")
	}
	if parsed.Range != nil && !def.RangeAllowed {
		return errors.New(def.Name + ": range not allowed")
	}
	return nil
}

// All returns a copy of all registered command definitions.
func (r *Registry) All() []CommandDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CommandDef, len(r.defs))
	copy(out, r.defs)
	return out
}
