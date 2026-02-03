// Package option implements vim-style option definitions with global,
// buffer-local, and window-local scoping for the editor.
package option

// OptionType classifies the value kind of an editor option.
type OptionType int

const (
	// TypeBool is a boolean toggle (e.g. :set number).
	TypeBool OptionType = iota
	// TypeInt is an integer value (e.g. :set shiftwidth=4).
	TypeInt
	// TypeString is a string value (e.g. :set encoding=utf-8).
	TypeString
)

// OptionDef describes a single editor option.
type OptionDef struct {
	Name    string
	Type    OptionType
	Default any
	Scope   ScopeLevel
	Aliases []string
}

// builtinOptions is the authoritative table of all supported editor options.
// Each entry is derived from vim's option set; defaults match vim/neovim
// where reasonable.
var builtinOptions = []OptionDef{
	// Indentation.
	{Name: "expandtab", Type: TypeBool, Default: false, Scope: ScopeBuffer, Aliases: []string{"et"}},
	{Name: "shiftwidth", Type: TypeInt, Default: 4, Scope: ScopeBuffer, Aliases: []string{"sw"}},
	{Name: "tabstop", Type: TypeInt, Default: 8, Scope: ScopeBuffer, Aliases: []string{"ts"}},
	{Name: "softtabstop", Type: TypeInt, Default: 0, Scope: ScopeBuffer, Aliases: []string{"sts"}},
	{Name: "autoindent", Type: TypeBool, Default: true, Scope: ScopeBuffer, Aliases: []string{"ai"}},
	{Name: "smartindent", Type: TypeBool, Default: false, Scope: ScopeBuffer, Aliases: []string{"si"}},

	// Display.
	{Name: "number", Type: TypeBool, Default: false, Scope: ScopeWindow, Aliases: []string{"nu"}},
	{Name: "relativenumber", Type: TypeBool, Default: false, Scope: ScopeWindow, Aliases: []string{"rnu"}},
	{Name: "wrap", Type: TypeBool, Default: true, Scope: ScopeWindow, Aliases: nil},
	{Name: "linebreak", Type: TypeBool, Default: false, Scope: ScopeWindow, Aliases: []string{"lbr"}},
	{Name: "list", Type: TypeBool, Default: false, Scope: ScopeWindow, Aliases: nil},
	{Name: "cursorline", Type: TypeBool, Default: false, Scope: ScopeWindow, Aliases: []string{"cul"}},
	{Name: "signcolumn", Type: TypeString, Default: "auto", Scope: ScopeWindow, Aliases: []string{"scl"}},

	// Scrolling.
	{Name: "scrolloff", Type: TypeInt, Default: 0, Scope: ScopeGlobal, Aliases: []string{"so"}},
	{Name: "sidescrolloff", Type: TypeInt, Default: 0, Scope: ScopeGlobal, Aliases: []string{"siso"}},

	// Search.
	{Name: "ignorecase", Type: TypeBool, Default: false, Scope: ScopeGlobal, Aliases: []string{"ic"}},
	{Name: "smartcase", Type: TypeBool, Default: false, Scope: ScopeGlobal, Aliases: []string{"scs"}},
	{Name: "incsearch", Type: TypeBool, Default: true, Scope: ScopeGlobal, Aliases: []string{"is"}},
	{Name: "hlsearch", Type: TypeBool, Default: true, Scope: ScopeGlobal, Aliases: []string{"hls"}},

	// File.
	{Name: "filetype", Type: TypeString, Default: "", Scope: ScopeBuffer, Aliases: []string{"ft"}},
	{Name: "syntax", Type: TypeString, Default: "", Scope: ScopeBuffer, Aliases: []string{"syn"}},
	{Name: "encoding", Type: TypeString, Default: "utf-8", Scope: ScopeGlobal, Aliases: []string{"enc"}},
	{Name: "fileformat", Type: TypeString, Default: "unix", Scope: ScopeBuffer, Aliases: []string{"ff"}},
}

// AllOptionDefs returns the complete list of built-in option definitions.
func AllOptionDefs() []OptionDef {
	out := make([]OptionDef, len(builtinOptions))
	copy(out, builtinOptions)
	return out
}

// LookupDef resolves an option name (or alias or unambiguous prefix) to its
// definition.
func LookupDef(name string) (*OptionDef, bool) {
	// Exact name or alias match first.
	for i := range builtinOptions {
		if matchExact(&builtinOptions[i], name) {
			return &builtinOptions[i], true
		}
	}
	// Unambiguous prefix match.
	return prefixLookup(name)
}

// matchExact reports whether name matches the option's canonical name or any alias.
func matchExact(def *OptionDef, name string) bool {
	if def.Name == name {
		return true
	}
	for _, alias := range def.Aliases {
		if alias == name {
			return true
		}
	}
	return false
}

// prefixLookup finds a single option whose canonical name starts with prefix.
// Returns nil if zero or more than one option matches (ambiguous).
func prefixLookup(prefix string) (*OptionDef, bool) {
	var match *OptionDef
	for i := range builtinOptions {
		if !hasPrefix(builtinOptions[i].Name, prefix) {
			continue
		}
		if match != nil {
			return nil, false // ambiguous
		}
		match = &builtinOptions[i]
	}
	if match == nil {
		return nil, false
	}
	return match, true
}

// hasPrefix reports whether s starts with prefix. Avoids importing strings
// for this single use.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
