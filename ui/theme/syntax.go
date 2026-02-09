package theme

import "github.com/charmbracelet/lipgloss"

// SyntaxCategory groups tree-sitter node types into highlighting categories.
type SyntaxCategory string

const (
	CatKeyword    SyntaxCategory = "keyword"
	CatFunction   SyntaxCategory = "function"
	CatType       SyntaxCategory = "type"
	CatString     SyntaxCategory = "string"
	CatNumber     SyntaxCategory = "number"
	CatComment    SyntaxCategory = "comment"
	CatOperator   SyntaxCategory = "operator"
	CatVariable   SyntaxCategory = "variable"
	CatConstant   SyntaxCategory = "constant"
	CatPunctuation SyntaxCategory = "punctuation"
	CatTag        SyntaxCategory = "tag"
	CatAttribute  SyntaxCategory = "attribute"
	CatNamespace  SyntaxCategory = "namespace"
	CatBuiltin    SyntaxCategory = "builtin"
	CatProperty   SyntaxCategory = "property"
)

// NodeTypeToCategory maps tree-sitter node type names to syntax categories.
// This covers the common node types across Go, Python, TypeScript, JavaScript,
// Rust, Java, C, C++, Ruby, and other supported grammars.
var NodeTypeToCategory = map[string]SyntaxCategory{
	// Keywords
	"keyword":                CatKeyword,
	"if":                     CatKeyword,
	"else":                   CatKeyword,
	"for":                    CatKeyword,
	"while":                  CatKeyword,
	"return":                 CatKeyword,
	"switch":                 CatKeyword,
	"case":                   CatKeyword,
	"default":                CatKeyword,
	"break":                  CatKeyword,
	"continue":               CatKeyword,
	"import":                 CatKeyword,
	"package":                CatKeyword,
	"func":                   CatKeyword,
	"var":                    CatKeyword,
	"const":                  CatKeyword,
	"type_keyword":           CatKeyword,
	"struct":                 CatKeyword,
	"interface":              CatKeyword,
	"defer":                  CatKeyword,
	"go":                     CatKeyword,
	"select":                 CatKeyword,
	"chan":                    CatKeyword,
	"map":                    CatKeyword,
	"range":                  CatKeyword,
	"def":                    CatKeyword,
	"class":                  CatKeyword,
	"async":                  CatKeyword,
	"await":                  CatKeyword,
	"fn":                     CatKeyword,
	"let":                    CatKeyword,
	"mut":                    CatKeyword,
	"pub":                    CatKeyword,
	"impl":                   CatKeyword,
	"trait":                  CatKeyword,
	"enum":                   CatKeyword,
	"match":                  CatKeyword,

	// Functions
	"function_declaration":    CatFunction,
	"method_declaration":      CatFunction,
	"func_literal":            CatFunction,
	"function_definition":     CatFunction,
	"call_expression":         CatFunction,
	"function":                CatFunction,
	"arrow_function":          CatFunction,
	"function_item":           CatFunction,

	// Types
	"type_identifier":         CatType,
	"type_spec":               CatType,
	"primitive_type":          CatType,
	"builtin_type":            CatType,
	"generic_type":            CatType,
	"type_annotation":         CatType,
	"type_alias_declaration":  CatType,
	"interface_declaration":   CatType,
	"struct_type":             CatType,

	// Strings
	"string_literal":              CatString,
	"interpreted_string_literal":  CatString,
	"raw_string_literal":          CatString,
	"template_string":             CatString,
	"string_content":              CatString,
	"string":                      CatString,
	"rune_literal":                CatString,
	"char_literal":                CatString,

	// Numbers
	"int_literal":       CatNumber,
	"float_literal":     CatNumber,
	"integer":           CatNumber,
	"number":            CatNumber,
	"decimal_literal":   CatNumber,
	"hex_literal":       CatNumber,
	"octal_literal":     CatNumber,
	"binary_literal":    CatNumber,

	// Comments
	"comment":       CatComment,
	"line_comment":  CatComment,
	"block_comment": CatComment,

	// Operators
	"binary_expression": CatOperator,
	"unary_expression":  CatOperator,

	// Variables
	"identifier":          CatVariable,
	"field_identifier":    CatProperty,
	"property_identifier": CatProperty,

	// Constants
	"true":       CatConstant,
	"false":      CatConstant,
	"nil":        CatConstant,
	"null":       CatConstant,
	"undefined":  CatConstant,
	"iota":       CatConstant,
	"none":       CatConstant,

	// Strings (additional)
	"escape_sequence": CatString,

	// Constants (additional)
	"self":  CatConstant,
	"this":  CatConstant,
	"super": CatConstant,

	// Punctuation (leaf terminals)
	"(": CatPunctuation, ")": CatPunctuation,
	"{": CatPunctuation, "}": CatPunctuation,
	"[": CatPunctuation, "]": CatPunctuation,
	";": CatPunctuation, ",": CatPunctuation,
	".": CatPunctuation, ":": CatPunctuation,

	// Operators (leaf terminals)
	"=":  CatOperator, "==": CatOperator, "!=": CatOperator,
	"+":  CatOperator, "-": CatOperator, "*": CatOperator,
	"/":  CatOperator, "%": CatOperator,
	"<":  CatOperator, ">": CatOperator,
	"<=": CatOperator, ">=": CatOperator,
	"&&": CatOperator, "||": CatOperator,
	"!":  CatOperator, "&": CatOperator,
	"|":  CatOperator, "^": CatOperator,
	"<<": CatOperator, ">>": CatOperator,
	":=": CatOperator, "+=": CatOperator,
	"->": CatOperator, "=>": CatOperator,

	// Keywords (additional leaf types)
	"type": CatKeyword,

	// Namespace
	"package_identifier": CatNamespace,
	"module":             CatNamespace,
	"namespace":          CatNamespace,
}

// SyntaxStyles builds a lipgloss.Style map for syntax highlighting
// from the given palette.
func SyntaxStyles(p Palette) map[SyntaxCategory]lipgloss.Style {
	return map[SyntaxCategory]lipgloss.Style{
		CatKeyword:     lipgloss.NewStyle().Foreground(p.Secondary).Bold(true), // mauve — reserved words
		CatFunction:    lipgloss.NewStyle().Foreground(p.Primary),              // blue — functions & methods
		CatType:        lipgloss.NewStyle().Foreground(p.Warning),              // yellow — type names
		CatString:      lipgloss.NewStyle().Foreground(p.Success),              // green — string literals
		CatNumber:      lipgloss.NewStyle().Foreground(p.Peach),               // peach — numeric literals
		CatComment:     lipgloss.NewStyle().Foreground(p.Muted).Italic(true),  // muted — comments
		CatOperator:    lipgloss.NewStyle().Foreground(p.Subtext),             // subtext — structural
		CatVariable:    lipgloss.NewStyle().Foreground(p.Foreground),          // foreground — identifiers
		CatConstant:    lipgloss.NewStyle().Foreground(p.Peach).Bold(true),    // peach bold — constants
		CatPunctuation: lipgloss.NewStyle().Foreground(p.Subtext),             // subtext — delimiters
		CatTag:         lipgloss.NewStyle().Foreground(p.Error),               // red — HTML/JSX tags
		CatAttribute:   lipgloss.NewStyle().Foreground(p.Accent),              // pink — attributes
		CatNamespace:   lipgloss.NewStyle().Foreground(p.Teal),                // teal — packages/imports
		CatBuiltin:     lipgloss.NewStyle().Foreground(p.Teal),                // teal — built-in functions
		CatProperty:    lipgloss.NewStyle().Foreground(p.Lavender),            // lavender — field access
	}
}
