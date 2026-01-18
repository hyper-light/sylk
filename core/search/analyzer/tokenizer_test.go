package analyzer

import (
	"testing"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewCodeTokenizer(t *testing.T) {
	t.Parallel()

	tokenizer := NewCodeTokenizer()
	assert.NotNil(t, tokenizer, "NewCodeTokenizer should return non-nil tokenizer")
}

func TestNewCodeTokenizerConstructor(t *testing.T) {
	t.Parallel()

	cache := registry.NewCache()
	tokenizer, err := NewCodeTokenizerConstructor(nil, cache)

	require.NoError(t, err, "constructor should not return error")
	assert.NotNil(t, tokenizer, "constructor should return non-nil tokenizer")
}

func TestNewCodeTokenizerConstructor_WithConfig(t *testing.T) {
	t.Parallel()

	cache := registry.NewCache()
	config := map[string]interface{}{
		"some_option": true,
	}
	tokenizer, err := NewCodeTokenizerConstructor(config, cache)

	require.NoError(t, err, "constructor should accept config without error")
	assert.NotNil(t, tokenizer, "constructor should return non-nil tokenizer")
}

// =============================================================================
// Registry Tests
// =============================================================================

func TestCodeTokenizer_RegistryName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "code_tokenizer", CodeTokenizerName, "registry name should be code_tokenizer")
}

func TestCodeTokenizer_RegisteredInBleve(t *testing.T) {
	t.Parallel()

	cache := registry.NewCache()
	constructor, err := cache.TokenizerNamed(CodeTokenizerName)

	// The tokenizer should be registered via init()
	require.NoError(t, err, "code_tokenizer should be registered")
	assert.NotNil(t, constructor, "constructor should be available")
}

// =============================================================================
// Simple Identifier Tests
// =============================================================================

func TestCodeTokenizer_SimpleIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single word",
			input:    "hello",
			expected: []string{"hello"},
		},
		{
			name:     "single letter",
			input:    "x",
			expected: []string{"x"},
		},
		{
			name:     "underscore prefix",
			input:    "_private",
			expected: []string{"_private"},
		},
		{
			name:     "double underscore",
			input:    "__dunder__",
			expected: []string{"__dunder__"},
		},
		{
			name:     "multiple words",
			input:    "hello world",
			expected: []string{"hello", "world"},
		},
		{
			name:     "multiple spaces",
			input:    "a    b    c",
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// CamelCase and snake_case Tests
// =============================================================================

func TestCodeTokenizer_CamelCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "camelCase",
			input:    "handleRequest",
			expected: []string{"handleRequest"},
		},
		{
			name:     "PascalCase",
			input:    "HandleRequest",
			expected: []string{"HandleRequest"},
		},
		{
			name:     "mixed case",
			input:    "handleHTTPRequest",
			expected: []string{"handleHTTPRequest"},
		},
		{
			name:     "multiple camelCase",
			input:    "getUserById createOrder",
			expected: []string{"getUserById", "createOrder"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_SnakeCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "snake_case",
			input:    "get_user_by_id",
			expected: []string{"get_user_by_id"},
		},
		{
			name:     "SCREAMING_SNAKE",
			input:    "MAX_BUFFER_SIZE",
			expected: []string{"MAX_BUFFER_SIZE"},
		},
		{
			name:     "multiple underscores",
			input:    "my__var",
			expected: []string{"my__var"},
		},
		{
			name:     "trailing underscore",
			input:    "reserved_",
			expected: []string{"reserved_"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// String Literal Tests
// =============================================================================

func TestCodeTokenizer_DoubleQuotedStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple string",
			input:    `"hello world"`,
			expected: []string{`"hello world"`},
		},
		{
			name:     "empty string",
			input:    `""`,
			expected: []string{`""`},
		},
		{
			name:     "string with spaces",
			input:    `"hello    world"`,
			expected: []string{`"hello    world"`},
		},
		{
			name:     "string before identifier",
			input:    `"prefix" suffix`,
			expected: []string{`"prefix"`, "suffix"},
		},
		{
			name:     "identifier before string",
			input:    `prefix "suffix"`,
			expected: []string{"prefix", `"suffix"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_SingleQuotedStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple char",
			input:    `'a'`,
			expected: []string{`'a'`},
		},
		{
			name:     "single quote string",
			input:    `'hello world'`,
			expected: []string{`'hello world'`},
		},
		{
			name:     "empty single quote",
			input:    `''`,
			expected: []string{`''`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_BacktickStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "backtick string",
			input:    "`raw string`",
			expected: []string{"`raw string`"},
		},
		{
			name:     "multiline backtick",
			input:    "`line1\nline2`",
			expected: []string{"`line1\nline2`"},
		},
		{
			name:     "backtick with special chars",
			input:    "`$var ${expr}`",
			expected: []string{"`$var ${expr}`"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_StringsWithEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "escaped quote",
			input:    `"hello \"world\""`,
			expected: []string{`"hello \"world\""`},
		},
		{
			name:     "escaped backslash",
			input:    `"path\\to\\file"`,
			expected: []string{`"path\\to\\file"`},
		},
		{
			name:     "newline escape",
			input:    `"line1\nline2"`,
			expected: []string{`"line1\nline2"`},
		},
		{
			name:     "tab escape",
			input:    `"col1\tcol2"`,
			expected: []string{`"col1\tcol2"`},
		},
		{
			name:     "single quote escape",
			input:    `'it\'s'`,
			expected: []string{`'it\'s'`},
		},
		{
			name:     "unicode escape",
			input:    `"\u0041\u0042\u0043"`,
			expected: []string{`"\u0041\u0042\u0043"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// Numeric Literal Tests
// =============================================================================

func TestCodeTokenizer_DecimalNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single digit",
			input:    "5",
			expected: []string{"5"},
		},
		{
			name:     "integer",
			input:    "12345",
			expected: []string{"12345"},
		},
		{
			name:     "zero",
			input:    "0",
			expected: []string{"0"},
		},
		{
			name:     "multiple numbers",
			input:    "1 2 3",
			expected: []string{"1", "2", "3"},
		},
		{
			name:     "number with underscores",
			input:    "1_000_000",
			expected: []string{"1_000_000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_FloatingPointNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple float",
			input:    "3.14",
			expected: []string{"3.14"},
		},
		{
			name:     "leading zero float",
			input:    "0.5",
			expected: []string{"0.5"},
		},
		{
			name:     "multiple decimals",
			input:    "1.23 4.56",
			expected: []string{"1.23", "4.56"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_ScientificNotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase e",
			input:    "1e10",
			expected: []string{"1e10"},
		},
		{
			name:     "uppercase E",
			input:    "1E10",
			expected: []string{"1E10"},
		},
		{
			name:     "positive exponent",
			input:    "1e+10",
			expected: []string{"1e+10"},
		},
		{
			name:     "negative exponent",
			input:    "1e-10",
			expected: []string{"1e-10"},
		},
		{
			name:     "float with exponent",
			input:    "3.14e2",
			expected: []string{"3.14e2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_HexadecimalNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase hex",
			input:    "0xdeadbeef",
			expected: []string{"0xdeadbeef"},
		},
		{
			name:     "uppercase hex",
			input:    "0XDEADBEEF",
			expected: []string{"0XDEADBEEF"},
		},
		{
			name:     "mixed case hex",
			input:    "0xDeAdBeEf",
			expected: []string{"0xDeAdBeEf"},
		},
		{
			name:     "short hex",
			input:    "0xff",
			expected: []string{"0xff"},
		},
		{
			name:     "hex zero",
			input:    "0x0",
			expected: []string{"0x0"},
		},
		{
			name:     "hex with underscores",
			input:    "0xFF_FF_FF_FF",
			expected: []string{"0xFF_FF_FF_FF"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_BinaryNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase binary",
			input:    "0b1010",
			expected: []string{"0b1010"},
		},
		{
			name:     "uppercase binary",
			input:    "0B1010",
			expected: []string{"0B1010"},
		},
		{
			name:     "binary zero",
			input:    "0b0",
			expected: []string{"0b0"},
		},
		{
			name:     "binary one",
			input:    "0b1",
			expected: []string{"0b1"},
		},
		{
			name:     "long binary",
			input:    "0b11111111",
			expected: []string{"0b11111111"},
		},
		{
			name:     "binary with underscores",
			input:    "0b1111_0000",
			expected: []string{"0b1111_0000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_OctalNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase octal",
			input:    "0o755",
			expected: []string{"0o755"},
		},
		{
			name:     "uppercase octal",
			input:    "0O755",
			expected: []string{"0O755"},
		},
		{
			name:     "octal zero",
			input:    "0o0",
			expected: []string{"0o0"},
		},
		{
			name:     "file permissions",
			input:    "0o644",
			expected: []string{"0o644"},
		},
		{
			name:     "octal with underscores",
			input:    "0o7_5_5",
			expected: []string{"0o7_5_5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// Operator Tests
// =============================================================================

func TestCodeTokenizer_ArrowOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "arrow operator between identifiers",
			input:    "a->b",
			expected: []string{"a", "b"},
		},
		{
			name:     "fat arrow between identifiers",
			input:    "x=>y",
			expected: []string{"x", "y"},
		},
		{
			name:     "fat arrow with spaces",
			input:    "x => y",
			expected: []string{"x", "y"},
		},
		{
			name:     "arrow in method call",
			input:    "obj->method",
			expected: []string{"obj", "method"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_ScopeOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "scope resolution",
			input:    "std::vector",
			expected: []string{"std", "vector"},
		},
		{
			name:     "nested scope",
			input:    "a::b::c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "rust path",
			input:    "std::collections::HashMap",
			expected: []string{"std", "collections", "HashMap"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_SpreadOperator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "spread before identifier",
			input:    "...args",
			expected: []string{"args"},
		},
		{
			name:     "spread after identifier",
			input:    "array...",
			expected: []string{"array"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_ComparisonOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "equality",
			input:    "a==b",
			expected: []string{"a", "b"},
		},
		{
			name:     "inequality",
			input:    "a!=b",
			expected: []string{"a", "b"},
		},
		{
			name:     "less than or equal",
			input:    "a<=b",
			expected: []string{"a", "b"},
		},
		{
			name:     "greater than or equal",
			input:    "a>=b",
			expected: []string{"a", "b"},
		},
		{
			name:     "strict equality",
			input:    "a===b",
			expected: []string{"a", "b"},
		},
		{
			name:     "strict inequality",
			input:    "a!==b",
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_LogicalOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "logical and",
			input:    "a&&b",
			expected: []string{"a", "b"},
		},
		{
			name:     "logical or",
			input:    "a||b",
			expected: []string{"a", "b"},
		},
		{
			name:     "null coalescing",
			input:    "a??b",
			expected: []string{"a", "b"},
		},
		{
			name:     "optional chaining",
			input:    "a?.b",
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_AssignmentOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "add assign",
			input:    "a+=1",
			expected: []string{"a", "1"},
		},
		{
			name:     "subtract assign",
			input:    "a-=1",
			expected: []string{"a", "1"},
		},
		{
			name:     "multiply assign",
			input:    "a*=2",
			expected: []string{"a", "2"},
		},
		{
			name:     "divide assign",
			input:    "a/=2",
			expected: []string{"a", "2"},
		},
		{
			name:     "modulo assign",
			input:    "a%=2",
			expected: []string{"a", "2"},
		},
		{
			name:     "left shift assign",
			input:    "a<<=1",
			expected: []string{"a", "1"},
		},
		{
			name:     "right shift assign",
			input:    "a>>=1",
			expected: []string{"a", "1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_IncrementDecrement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "prefix increment",
			input:    "++i",
			expected: []string{"i"},
		},
		{
			name:     "postfix increment",
			input:    "i++",
			expected: []string{"i"},
		},
		{
			name:     "prefix decrement",
			input:    "--i",
			expected: []string{"i"},
		},
		{
			name:     "postfix decrement",
			input:    "i--",
			expected: []string{"i"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// Unicode Identifier Tests
// =============================================================================

func TestCodeTokenizer_UnicodeIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Greek letters",
			input:    "\u03b1 \u03b2 \u03b3",
			expected: []string{"\u03b1", "\u03b2", "\u03b3"},
		},
		{
			name:     "Chinese characters",
			input:    "\u4e2d\u6587\u53d8\u91cf",
			expected: []string{"\u4e2d\u6587\u53d8\u91cf"},
		},
		{
			name:     "Japanese hiragana",
			input:    "\u3053\u3093\u306b\u3061\u306f",
			expected: []string{"\u3053\u3093\u306b\u3061\u306f"},
		},
		{
			name:     "Cyrillic",
			input:    "\u043f\u0435\u0440\u0435\u043c\u0435\u043d\u043d\u0430\u044f",
			expected: []string{"\u043f\u0435\u0440\u0435\u043c\u0435\u043d\u043d\u0430\u044f"},
		},
		{
			name:     "mixed ASCII and unicode",
			input:    "hello\u4e16\u754c",
			expected: []string{"hello\u4e16\u754c"},
		},
		{
			name:     "accented characters",
			input:    "caf\u00e9",
			expected: []string{"caf\u00e9"},
		},
		{
			name:     "German umlauts",
			input:    "\u00fcberpr\u00fcfung",
			expected: []string{"\u00fcberpr\u00fcfung"},
		},
		{
			name:     "emoji identifier",
			input:    "func_\u2764",
			expected: []string{"func_"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestCodeTokenizer_EmptyInput(t *testing.T) {
	t.Parallel()

	tokenizer := NewCodeTokenizer()
	tokens := tokenizer.Tokenize([]byte(""))

	assert.Empty(t, tokens, "empty input should produce no tokens")
}

func TestCodeTokenizer_OnlyWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"spaces", "     "},
		{"tabs", "\t\t\t"},
		{"newlines", "\n\n\n"},
		{"mixed whitespace", "  \t\n  \t\n"},
		{"windows newlines", "\r\n\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assert.Empty(t, tokens, "whitespace-only input should produce no tokens")
		})
	}
}

func TestCodeTokenizer_OnlyPunctuation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"single bracket", "["},
		{"brackets", "[]"},
		{"parens", "()"},
		{"braces", "{}"},
		{"mixed punct", "[](){}"},
		{"operators", "+-*/"},
		{"comparison", "<>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assert.Empty(t, tokens, "punctuation-only input should produce no tokens")
		})
	}
}

func TestCodeTokenizer_UnterminatedString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "unterminated double quote",
			input:    `"hello`,
			expected: []string{`"hello`},
		},
		{
			name:     "unterminated single quote",
			input:    `'hello`,
			expected: []string{`'hello`},
		},
		{
			name:     "unterminated backtick",
			input:    "`hello",
			expected: []string{"`hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

func TestCodeTokenizer_ConsecutiveNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "numbers with operators",
			input:    "1+2",
			expected: []string{"1", "2"},
		},
		{
			name:     "array indices",
			input:    "arr[0]",
			expected: []string{"arr", "0"},
		},
		{
			name:     "function args",
			input:    "func(1,2,3)",
			expected: []string{"func", "1", "2", "3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// Token Position Tests
// =============================================================================

func TestCodeTokenizer_TokenPositions(t *testing.T) {
	t.Parallel()

	tokenizer := NewCodeTokenizer()
	input := "hello world"
	tokens := tokenizer.Tokenize([]byte(input))

	require.Len(t, tokens, 2, "should have 2 tokens")

	// First token
	assert.Equal(t, 1, tokens[0].Position, "first token position")
	assert.Equal(t, 0, tokens[0].Start, "first token start")
	assert.Equal(t, 5, tokens[0].End, "first token end")

	// Second token
	assert.Equal(t, 2, tokens[1].Position, "second token position")
	assert.Equal(t, 6, tokens[1].Start, "second token start")
	assert.Equal(t, 11, tokens[1].End, "second token end")
}

func TestCodeTokenizer_TokenStartEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []struct {
			term  string
			start int
			end   int
		}
	}{
		{
			name:  "simple tokens",
			input: "a b c",
			expected: []struct {
				term  string
				start int
				end   int
			}{
				{"a", 0, 1},
				{"b", 2, 3},
				{"c", 4, 5},
			},
		},
		{
			name:  "string literal",
			input: `x "test" y`,
			expected: []struct {
				term  string
				start int
				end   int
			}{
				{"x", 0, 1},
				{`"test"`, 2, 8},
				{"y", 9, 10},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))

			require.Len(t, tokens, len(tt.expected), "token count mismatch")
			for i, exp := range tt.expected {
				assert.Equal(t, exp.term, string(tokens[i].Term), "term mismatch at %d", i)
				assert.Equal(t, exp.start, tokens[i].Start, "start mismatch at %d", i)
				assert.Equal(t, exp.end, tokens[i].End, "end mismatch at %d", i)
			}
		})
	}
}

// =============================================================================
// Real Code Examples
// =============================================================================

func TestCodeTokenizer_RealCodeSnippets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Go function signature",
			input:    "func HandleRequest(ctx context.Context, req *Request) error",
			expected: []string{"func", "HandleRequest", "ctx", "context", "Context", "req", "Request", "error"},
		},
		{
			name:     "JavaScript arrow function",
			input:    "const add = (a, b) => a + b",
			expected: []string{"const", "add", "a", "b", "a", "b"},
		},
		{
			name:     "Python f-string",
			input:    `f"Hello {name}"`,
			expected: []string{"f", `"Hello {name}"`},
		},
		{
			name:     "Rust use statement",
			input:    "use std::collections::HashMap",
			expected: []string{"use", "std", "collections", "HashMap"},
		},
		{
			name:     "C++ template",
			input:    "std::vector<int>",
			expected: []string{"std", "vector", "int"},
		},
		{
			name:     "SQL query",
			input:    `SELECT * FROM users WHERE id = 123`,
			expected: []string{"SELECT", "FROM", "users", "WHERE", "id", "123"},
		},
		{
			name:     "JSON key access",
			input:    `data["key"]`,
			expected: []string{"data", `"key"`},
		},
		{
			name:     "Regex pattern",
			input:    `regex := "^[a-z]+$"`,
			expected: []string{"regex", `"^[a-z]+$"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tokenizer := NewCodeTokenizer()
			tokens := tokenizer.Tokenize([]byte(tt.input))
			assertTokenTerms(t, tokens, tt.expected)
		})
	}
}

// =============================================================================
// Character Classification Tests
// =============================================================================

func TestIsWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		char     rune
		expected bool
	}{
		{' ', true},
		{'\t', true},
		{'\n', true},
		{'\r', true},
		{'a', false},
		{'1', false},
		{'_', false},
		{'\u00A0', false}, // non-breaking space
	}

	for _, tt := range tests {
		t.Run(string(tt.char), func(t *testing.T) {
			t.Parallel()
			result := isWhitespace(tt.char)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsDigit(t *testing.T) {
	t.Parallel()

	for r := '0'; r <= '9'; r++ {
		assert.True(t, isDigit(r), "digit %c should return true", r)
	}

	assert.False(t, isDigit('a'), "letter should return false")
	assert.False(t, isDigit('A'), "uppercase letter should return false")
	assert.False(t, isDigit('_'), "underscore should return false")
}

func TestIsHexDigit(t *testing.T) {
	t.Parallel()

	validHex := "0123456789abcdefABCDEF"
	for _, r := range validHex {
		assert.True(t, isHexDigit(r), "hex digit %c should return true", r)
	}

	assert.False(t, isHexDigit('g'), "g should return false")
	assert.False(t, isHexDigit('G'), "G should return false")
	assert.False(t, isHexDigit('_'), "underscore should return false")
}

func TestIsBinaryDigit(t *testing.T) {
	t.Parallel()

	assert.True(t, isBinaryDigit('0'), "0 should return true")
	assert.True(t, isBinaryDigit('1'), "1 should return true")
	assert.False(t, isBinaryDigit('2'), "2 should return false")
	assert.False(t, isBinaryDigit('a'), "a should return false")
}

func TestIsOctalDigit(t *testing.T) {
	t.Parallel()

	for r := '0'; r <= '7'; r++ {
		assert.True(t, isOctalDigit(r), "octal digit %c should return true", r)
	}

	assert.False(t, isOctalDigit('8'), "8 should return false")
	assert.False(t, isOctalDigit('9'), "9 should return false")
}

func TestIsNumericPrefixChar(t *testing.T) {
	t.Parallel()

	validPrefixes := []rune{'x', 'X', 'b', 'B', 'o', 'O'}
	for _, r := range validPrefixes {
		assert.True(t, isNumericPrefixChar(r), "prefix %c should return true", r)
	}

	assert.False(t, isNumericPrefixChar('d'), "d should return false")
	assert.False(t, isNumericPrefixChar('1'), "1 should return false")
}

func TestIsIdentifierStart(t *testing.T) {
	t.Parallel()

	// Letters should be valid
	assert.True(t, isIdentifierStart('a'), "lowercase letter")
	assert.True(t, isIdentifierStart('Z'), "uppercase letter")
	assert.True(t, isIdentifierStart('_'), "underscore")
	assert.True(t, isIdentifierStart('\u03b1'), "Greek alpha")

	// Digits should not be valid start
	assert.False(t, isIdentifierStart('0'), "digit")
	assert.False(t, isIdentifierStart('9'), "digit")
}

func TestIsIdentifierContinue(t *testing.T) {
	t.Parallel()

	// Letters should be valid
	assert.True(t, isIdentifierContinue('a'), "lowercase letter")
	assert.True(t, isIdentifierContinue('Z'), "uppercase letter")
	assert.True(t, isIdentifierContinue('_'), "underscore")

	// Digits should be valid in continuation
	assert.True(t, isIdentifierContinue('0'), "digit")
	assert.True(t, isIdentifierContinue('9'), "digit")

	// Unicode digits should be valid
	assert.True(t, isIdentifierContinue('\u0661'), "Arabic-Indic digit")
}

func TestGetDigitValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prefix   rune
		validFor string
	}{
		{'x', "0123456789abcdefABCDEF"},
		{'X', "0123456789abcdefABCDEF"},
		{'b', "01"},
		{'B', "01"},
		{'o', "01234567"},
		{'O', "01234567"},
	}

	for _, tt := range tests {
		t.Run(string(tt.prefix), func(t *testing.T) {
			t.Parallel()
			validator := getDigitValidator(tt.prefix)
			for _, r := range tt.validFor {
				assert.True(t, validator(r), "validator for %c should accept %c", tt.prefix, r)
			}
		})
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkCodeTokenizer_SimpleText(b *testing.B) {
	tokenizer := NewCodeTokenizer()
	input := []byte("func handleRequest ctx context req Request error")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenizer.Tokenize(input)
	}
}

func BenchmarkCodeTokenizer_CodeWithOperators(b *testing.B) {
	tokenizer := NewCodeTokenizer()
	input := []byte("a := b + c * d - e / f && g || h == i != j <= k >= l")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenizer.Tokenize(input)
	}
}

func BenchmarkCodeTokenizer_StringLiterals(b *testing.B) {
	tokenizer := NewCodeTokenizer()
	input := []byte(`"hello world" 'a' "test \"escaped\" string" normal`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenizer.Tokenize(input)
	}
}

func BenchmarkCodeTokenizer_NumericLiterals(b *testing.B) {
	tokenizer := NewCodeTokenizer()
	input := []byte("0xDEADBEEF 0b10101010 0o755 123.456e-10 1_000_000")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenizer.Tokenize(input)
	}
}

func BenchmarkCodeTokenizer_LargeInput(b *testing.B) {
	tokenizer := NewCodeTokenizer()
	// Generate a large input with mixed content
	input := make([]byte, 0, 10000)
	snippet := []byte(`func main() { fmt.Println("Hello, World!") }` + "\n")
	for len(input) < 10000 {
		input = append(input, snippet...)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenizer.Tokenize(input)
	}
}

func BenchmarkCodeTokenizer_UnicodeHeavy(b *testing.B) {
	tokenizer := NewCodeTokenizer()
	input := []byte("\u03b1\u03b2\u03b3 \u4e2d\u6587 \u0440\u0443\u0441\u0441\u043a\u0438\u0439 caf\u00e9 na\u00efve")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenizer.Tokenize(input)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// assertTokenTerms asserts that the token terms match the expected strings.
func assertTokenTerms(t *testing.T, tokens analysis.TokenStream, expected []string) {
	t.Helper()

	terms := make([]string, len(tokens))
	for i, token := range tokens {
		terms[i] = string(token.Term)
	}

	assert.Equal(t, expected, terms, "token terms should match")
}
