// Package analyzer provides custom Bleve analyzers for code-aware text search.
// It includes tokenizers and filters optimized for source code, handling constructs
// like camelCase identifiers, snake_case variables, operators, and string literals.
package analyzer

import (
	"unicode"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
)

// CodeTokenizerName is the registry name for the code tokenizer.
const CodeTokenizerName = "code_tokenizer"

// Multi-character operators that should trigger token boundaries.
var multiCharOperators = []string{
	"...", // spread/rest operator
	"<<=", // left shift assignment
	">>=", // right shift assignment
	"<->", // bidirectional arrow
	"===", // strict equality
	"!==", // strict inequality
	"<=>", // spaceship operator
	"->",  // arrow/pointer member
	"=>",  // fat arrow
	"::",  // scope resolution
	"==",  // equality
	"!=",  // inequality
	"<=",  // less than or equal
	">=",  // greater than or equal
	"&&",  // logical and
	"||",  // logical or
	"++",  // increment
	"--",  // decrement
	"<<",  // left shift
	">>",  // right shift
	"+=",  // add assignment
	"-=",  // subtract assignment
	"*=",  // multiply assignment
	"/=",  // divide assignment
	"%=",  // modulo assignment
	"&=",  // and assignment
	"|=",  // or assignment
	"^=",  // xor assignment
	"??",  // null coalescing
	"?.",  // optional chaining
}

// CodeTokenizer implements Bleve's analysis.Tokenizer interface for source code.
// It handles string literals, numeric literals, operators, and Unicode identifiers.
type CodeTokenizer struct{}

// NewCodeTokenizer creates a new CodeTokenizer instance.
// This is the constructor function for direct usage.
func NewCodeTokenizer() *CodeTokenizer {
	return &CodeTokenizer{}
}

// NewCodeTokenizerConstructor is the Bleve registry constructor function.
func NewCodeTokenizerConstructor(config map[string]interface{}, cache *registry.Cache) (analysis.Tokenizer, error) {
	return NewCodeTokenizer(), nil
}

func init() {
	registry.RegisterTokenizer(CodeTokenizerName, NewCodeTokenizerConstructor)
}

// Tokenize splits the input bytes into a stream of tokens.
// It preserves string literals as single tokens, handles numeric literals,
// splits around operators, and properly handles Unicode identifiers.
func (t *CodeTokenizer) Tokenize(input []byte) analysis.TokenStream {
	scanner := newTokenScanner(input)
	return scanner.scanAll()
}

// tokenScanner holds the state for scanning input into tokens.
type tokenScanner struct {
	input    []byte
	position int // byte position in input
	tokens   analysis.TokenStream
	tokenPos int // token position counter
}

// newTokenScanner creates a scanner for the given input.
func newTokenScanner(input []byte) *tokenScanner {
	return &tokenScanner{
		input:    input,
		position: 0,
		tokens:   make(analysis.TokenStream, 0),
		tokenPos: 1,
	}
}

// scanAll processes all input and returns the token stream.
func (s *tokenScanner) scanAll() analysis.TokenStream {
	for s.position < len(s.input) {
		s.scanNext()
	}
	return s.tokens
}

// scanNext scans the next token from input.
func (s *tokenScanner) scanNext() {
	r, size := utf8.DecodeRune(s.input[s.position:])
	if size == 0 {
		s.position++
		return
	}

	switch {
	case isWhitespace(r):
		s.skipWhitespace()
	case r == '"' || r == '\'' || r == '`':
		s.scanStringLiteral(r)
	case isDigit(r) || (r == '0' && s.peekIsNumericPrefix()):
		s.scanNumericLiteral()
	case isIdentifierStart(r):
		s.scanIdentifier()
	default:
		s.scanOperatorOrPunctuation()
	}
}

// skipWhitespace advances past whitespace characters.
func (s *tokenScanner) skipWhitespace() {
	for s.position < len(s.input) {
		r, size := utf8.DecodeRune(s.input[s.position:])
		if !isWhitespace(r) {
			break
		}
		s.position += size
	}
}

// scanStringLiteral scans a string literal as a single token.
func (s *tokenScanner) scanStringLiteral(quote rune) {
	start := s.position
	s.position++ // skip opening quote

	for s.position < len(s.input) {
		r, size := utf8.DecodeRune(s.input[s.position:])
		s.position += size

		if r == '\\' && s.position < len(s.input) {
			// Skip escaped character
			_, escSize := utf8.DecodeRune(s.input[s.position:])
			s.position += escSize
			continue
		}

		if r == quote {
			break
		}
	}

	s.emitToken(start, s.position)
}

// scanNumericLiteral scans a numeric literal (decimal, hex, binary, octal).
func (s *tokenScanner) scanNumericLiteral() {
	start := s.position
	r, size := utf8.DecodeRune(s.input[s.position:])
	s.position += size

	// Check for hex, binary, or octal prefix
	if r == '0' && s.position < len(s.input) {
		prefix, prefixSize := utf8.DecodeRune(s.input[s.position:])
		if isNumericPrefixChar(prefix) {
			s.position += prefixSize
			s.scanNumericDigits(prefix)
			s.emitToken(start, s.position)
			return
		}
	}

	// Scan decimal digits
	s.scanDecimalDigits()
	s.emitToken(start, s.position)
}

// scanNumericDigits scans digits for a specific numeric base.
func (s *tokenScanner) scanNumericDigits(prefix rune) {
	validator := getDigitValidator(prefix)
	for s.position < len(s.input) {
		r, size := utf8.DecodeRune(s.input[s.position:])
		if !validator(r) && r != '_' {
			break
		}
		s.position += size
	}
}

// scanDecimalDigits scans decimal digits including floating point.
func (s *tokenScanner) scanDecimalDigits() {
	s.scanWhile(isDecimalDigit)

	// Handle decimal point
	if s.position < len(s.input) && s.input[s.position] == '.' {
		next := s.peekAt(s.position + 1)
		if isDigit(next) {
			s.position++ // consume '.'
			s.scanWhile(isDecimalDigit)
		}
	}

	// Handle exponent
	s.scanExponent()
}

// scanExponent scans an optional exponent part of a number.
func (s *tokenScanner) scanExponent() {
	if s.position >= len(s.input) {
		return
	}
	r, size := utf8.DecodeRune(s.input[s.position:])
	if r != 'e' && r != 'E' {
		return
	}

	s.position += size
	if s.position < len(s.input) {
		sign, signSize := utf8.DecodeRune(s.input[s.position:])
		if sign == '+' || sign == '-' {
			s.position += signSize
		}
	}
	s.scanWhile(isDecimalDigit)
}

// scanIdentifier scans a Unicode identifier.
func (s *tokenScanner) scanIdentifier() {
	start := s.position
	s.scanWhile(isIdentifierContinue)
	s.emitToken(start, s.position)
}

// scanOperatorOrPunctuation scans operators and punctuation.
func (s *tokenScanner) scanOperatorOrPunctuation() {
	// Check for multi-character operators
	for _, op := range multiCharOperators {
		if s.matchesAt(op) {
			s.position += len(op)
			return // operators are not emitted as tokens
		}
	}

	// Skip single character operator/punctuation
	_, size := utf8.DecodeRune(s.input[s.position:])
	s.position += size
}

// scanWhile advances while the predicate returns true.
func (s *tokenScanner) scanWhile(pred func(rune) bool) {
	for s.position < len(s.input) {
		r, size := utf8.DecodeRune(s.input[s.position:])
		if !pred(r) {
			break
		}
		s.position += size
	}
}

// emitToken creates and appends a token for the given byte range.
func (s *tokenScanner) emitToken(start, end int) {
	if start >= end {
		return
	}
	term := s.input[start:end]
	if len(term) == 0 {
		return
	}

	token := &analysis.Token{
		Term:     term,
		Position: s.tokenPos,
		Start:    start,
		End:      end,
		Type:     analysis.AlphaNumeric,
	}
	s.tokens = append(s.tokens, token)
	s.tokenPos++
}

// matchesAt checks if the input matches the given string at current position.
func (s *tokenScanner) matchesAt(str string) bool {
	if s.position+len(str) > len(s.input) {
		return false
	}
	return string(s.input[s.position:s.position+len(str)]) == str
}

// peekIsNumericPrefix checks if the next char is a numeric prefix (x, b, o).
func (s *tokenScanner) peekIsNumericPrefix() bool {
	if s.position+1 >= len(s.input) {
		return false
	}
	r, _ := utf8.DecodeRune(s.input[s.position+1:])
	return isNumericPrefixChar(r)
}

// peekNext returns the next rune without advancing.
func (s *tokenScanner) peekNext() rune {
	if s.position >= len(s.input) {
		return 0
	}
	r, _ := utf8.DecodeRune(s.input[s.position:])
	return r
}

// peekAt returns the rune at the given position without advancing.
func (s *tokenScanner) peekAt(pos int) rune {
	if pos >= len(s.input) {
		return 0
	}
	r, _ := utf8.DecodeRune(s.input[pos:])
	return r
}

// =============================================================================
// Character Classification Helpers
// =============================================================================

// isWhitespace returns true for whitespace characters.
func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// isDigit returns true for decimal digits.
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isDecimalDigit returns true for decimal digits and underscores.
func isDecimalDigit(r rune) bool {
	return isDigit(r) || r == '_'
}

// isHexDigit returns true for hexadecimal digits.
func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// isBinaryDigit returns true for binary digits.
func isBinaryDigit(r rune) bool {
	return r == '0' || r == '1'
}

// isOctalDigit returns true for octal digits.
func isOctalDigit(r rune) bool {
	return r >= '0' && r <= '7'
}

// isNumericPrefixChar returns true for numeric base prefixes (x, X, b, B, o, O).
func isNumericPrefixChar(r rune) bool {
	return r == 'x' || r == 'X' || r == 'b' || r == 'B' || r == 'o' || r == 'O'
}

// isIdentifierStart returns true for valid identifier start characters.
// Unicode letters and underscore are valid.
func isIdentifierStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

// isIdentifierContinue returns true for valid identifier continuation characters.
// Unicode letters, digits, and underscore are valid.
func isIdentifierContinue(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// getDigitValidator returns the appropriate digit validator for a numeric prefix.
func getDigitValidator(prefix rune) func(rune) bool {
	switch prefix {
	case 'x', 'X':
		return isHexDigit
	case 'b', 'B':
		return isBinaryDigit
	case 'o', 'O':
		return isOctalDigit
	default:
		return isDigit
	}
}
