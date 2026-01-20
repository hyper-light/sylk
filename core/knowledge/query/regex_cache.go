// Package query provides HQ.5.2: Domain-Filtered Hybrid Query.
// PF.1.2: Module-Level Compiled Regex Cache for performance optimization.
// This file provides pre-compiled regex patterns and a thread-safe cache
// for dynamic patterns to eliminate repeated regex compilation.
package query

import (
	"regexp"
	"sync"
)

// =============================================================================
// Pre-compiled Common Patterns (initialized at package load time)
// =============================================================================

var (
	// TokenizePattern matches non-alphanumeric characters for query tokenization.
	// Used in domain_filter.go:tokenizeQuery to split queries into words.
	TokenizePattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

	// EmailPattern matches standard email addresses.
	EmailPattern = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)

	// URLPattern matches HTTP/HTTPS URLs.
	URLPattern = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]]+`)

	// DateISOPattern matches ISO 8601 date format (YYYY-MM-DD).
	DateISOPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

	// DateSlashPattern matches slash-separated dates (MM/DD/YYYY or DD/MM/YYYY).
	DateSlashPattern = regexp.MustCompile(`\d{1,2}/\d{1,2}/\d{2,4}`)

	// TimePattern matches time in HH:MM or HH:MM:SS format.
	TimePattern = regexp.MustCompile(`\d{1,2}:\d{2}(:\d{2})?`)

	// FilePathPattern matches Unix-style file paths.
	FilePathPattern = regexp.MustCompile(`(/[a-zA-Z0-9._-]+)+/?`)

	// VersionPattern matches semantic version numbers (e.g., v1.2.3 or 1.2.3).
	VersionPattern = regexp.MustCompile(`v?\d+\.\d+(\.\d+)?(-[a-zA-Z0-9]+)?`)

	// UUIDPattern matches standard UUID format.
	UUIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

	// CamelCasePattern matches camelCase or PascalCase boundaries for tokenization.
	CamelCasePattern = regexp.MustCompile(`([a-z])([A-Z])`)

	// SnakeCasePattern matches snake_case separators.
	SnakeCasePattern = regexp.MustCompile(`[_-]+`)

	// WhitespacePattern matches one or more whitespace characters.
	WhitespacePattern = regexp.MustCompile(`\s+`)

	// PunctuationPattern matches common punctuation for removal.
	PunctuationPattern = regexp.MustCompile(`[.,;:!?'"(){}\[\]<>]`)

	// NumberPattern matches integer or decimal numbers.
	NumberPattern = regexp.MustCompile(`-?\d+(\.\d+)?`)

	// IdentifierPattern matches programming language identifiers.
	// Note: Use MatchString for substring matches, FindString for full extraction.
	IdentifierPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

	// HashTagPattern matches hashtags (e.g., #topic).
	HashTagPattern = regexp.MustCompile(`#[a-zA-Z0-9_]+`)

	// MentionPattern matches mentions (e.g., @username).
	MentionPattern = regexp.MustCompile(`@[a-zA-Z0-9_]+`)

	// CodeBlockPattern matches markdown code block markers.
	CodeBlockPattern = regexp.MustCompile("```[a-zA-Z]*")

	// FunctionCallPattern matches function call syntax (name followed by parentheses).
	FunctionCallPattern = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*\s*\(`)

	// ImportPattern matches common import statements across languages.
	ImportPattern = regexp.MustCompile(`(?i)^(import|from|require|include|using)\s+`)

	// GoFuncPattern matches Go function declarations.
	GoFuncPattern = regexp.MustCompile(`func\s+(\([^)]*\)\s+)?[a-zA-Z_][a-zA-Z0-9_]*\s*\(`)

	// GoTypePattern matches Go type declarations.
	GoTypePattern = regexp.MustCompile(`type\s+[a-zA-Z_][a-zA-Z0-9_]*\s+(struct|interface|=)`)

	// GoPackagePattern matches Go package declarations.
	GoPackagePattern = regexp.MustCompile(`package\s+[a-zA-Z_][a-zA-Z0-9_]*`)
)

// =============================================================================
// RegexCache - Dynamic Pattern Caching
// =============================================================================

// RegexCache provides a thread-safe cache for dynamically compiled regex patterns.
// Use this for patterns that are determined at runtime and may be reused.
// For common patterns known at compile time, prefer the pre-compiled package-level
// variables above.
type RegexCache struct {
	cache sync.Map // map[string]*regexp.Regexp
}

// globalRegexCache is the package-level default cache instance.
var globalRegexCache = NewRegexCache()

// NewRegexCache creates a new RegexCache instance.
func NewRegexCache() *RegexCache {
	return &RegexCache{}
}

// GetOrCompile returns a compiled regex for the given pattern, compiling and
// caching it if not already present. Returns an error if the pattern is invalid.
//
// This method is thread-safe and can be called concurrently from multiple
// goroutines. The same compiled pattern is shared across all callers.
//
// Example:
//
//	re, err := cache.GetOrCompile(`\d+`)
//	if err != nil {
//	    return err
//	}
//	matches := re.FindAllString(text, -1)
func (rc *RegexCache) GetOrCompile(pattern string) (*regexp.Regexp, error) {
	// Fast path: check if already cached
	if cached, ok := rc.cache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}

	// Slow path: compile and cache
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	// Store the compiled regex. If another goroutine stored it first,
	// LoadOrStore returns the existing value and we discard our compilation.
	// This is safe because regexp.Regexp is immutable after compilation.
	actual, _ := rc.cache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// MustGetOrCompile is like GetOrCompile but panics if the pattern is invalid.
//
// Deprecated: This function can crash the application when called from
// background goroutines with invalid patterns. Use GetOrCompile instead,
// which returns an error that can be handled gracefully. If you must use
// this function, wrap calls with SafeGetOrCompile for panic recovery.
//
// This function should only be used during initialization with patterns
// that are known to be valid at compile time.
func (rc *RegexCache) MustGetOrCompile(pattern string) *regexp.Regexp {
	re, err := rc.GetOrCompile(pattern)
	if err != nil {
		panic("regex_cache: invalid pattern: " + err.Error())
	}
	return re
}

// SafeGetOrCompile wraps GetOrCompile with panic recovery for use cases
// where patterns may be invalid and panics must be prevented. This is
// useful for background goroutines or when processing untrusted patterns.
//
// Returns the compiled regex and nil error on success, nil and error on
// compilation failure, or nil and a recovered panic error if a panic occurs.
func (rc *RegexCache) SafeGetOrCompile(pattern string) (re *regexp.Regexp, err error) {
	defer func() {
		if r := recover(); r != nil {
			re = nil
			err = recoverToError(r)
		}
	}()
	return rc.GetOrCompile(pattern)
}

// recoverToError converts a recovered panic value to an error.
func recoverToError(r interface{}) error {
	switch v := r.(type) {
	case error:
		return v
	case string:
		return &RegexPanicError{Message: v}
	default:
		return &RegexPanicError{Message: "unknown panic in regex compilation"}
	}
}

// RegexPanicError represents a recovered panic from regex operations.
type RegexPanicError struct {
	Message string
}

func (e *RegexPanicError) Error() string {
	return "regex_cache: recovered panic: " + e.Message
}

// Contains checks if a pattern is already cached without compiling it.
func (rc *RegexCache) Contains(pattern string) bool {
	_, ok := rc.cache.Load(pattern)
	return ok
}

// Get retrieves a cached pattern without compiling. Returns nil if not cached.
func (rc *RegexCache) Get(pattern string) *regexp.Regexp {
	if cached, ok := rc.cache.Load(pattern); ok {
		return cached.(*regexp.Regexp)
	}
	return nil
}

// Precompile compiles and caches multiple patterns. Returns errors for any
// patterns that fail to compile, but continues processing remaining patterns.
func (rc *RegexCache) Precompile(patterns ...string) []error {
	var errors []error
	for _, pattern := range patterns {
		if _, err := rc.GetOrCompile(pattern); err != nil {
			errors = append(errors, err)
		}
	}
	return errors
}

// Clear removes all cached patterns. Use with caution in concurrent contexts.
func (rc *RegexCache) Clear() {
	rc.cache = sync.Map{}
}

// Size returns the approximate number of cached patterns.
// Note: This is approximate due to concurrent access.
func (rc *RegexCache) Size() int {
	count := 0
	rc.cache.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// =============================================================================
// Global Cache Functions
// =============================================================================

// GetOrCompileGlobal compiles or retrieves a pattern from the global cache.
// This is a convenience function for the common case of using a shared cache.
func GetOrCompileGlobal(pattern string) (*regexp.Regexp, error) {
	return globalRegexCache.GetOrCompile(pattern)
}

// MustGetOrCompileGlobal is like GetOrCompileGlobal but panics on invalid patterns.
//
// Deprecated: This function can crash the application when called from
// background goroutines with invalid patterns. Use GetOrCompileGlobal instead,
// which returns an error that can be handled gracefully. For background
// goroutines, use SafeGetOrCompileGlobal which includes panic recovery.
func MustGetOrCompileGlobal(pattern string) *regexp.Regexp {
	return globalRegexCache.MustGetOrCompile(pattern)
}

// SafeGetOrCompileGlobal compiles or retrieves a pattern from the global cache
// with panic recovery. This is safe to use in background goroutines.
func SafeGetOrCompileGlobal(pattern string) (*regexp.Regexp, error) {
	return globalRegexCache.SafeGetOrCompile(pattern)
}

// PrecompileGlobal precompiles patterns into the global cache.
func PrecompileGlobal(patterns ...string) []error {
	return globalRegexCache.Precompile(patterns...)
}

// GlobalCacheSize returns the size of the global regex cache.
func GlobalCacheSize() int {
	return globalRegexCache.Size()
}

// =============================================================================
// Pattern Builder Utilities
// =============================================================================

// PatternBuilder provides a fluent API for constructing regex patterns.
type PatternBuilder struct {
	parts []string
}

// NewPatternBuilder creates a new PatternBuilder.
func NewPatternBuilder() *PatternBuilder {
	return &PatternBuilder{
		parts: make([]string, 0, 8),
	}
}

// Literal adds a literal string to the pattern, escaping special characters.
func (pb *PatternBuilder) Literal(s string) *PatternBuilder {
	pb.parts = append(pb.parts, regexp.QuoteMeta(s))
	return pb
}

// Raw adds a raw regex pattern string without escaping.
func (pb *PatternBuilder) Raw(pattern string) *PatternBuilder {
	pb.parts = append(pb.parts, pattern)
	return pb
}

// Group adds a non-capturing group containing the given pattern.
func (pb *PatternBuilder) Group(pattern string) *PatternBuilder {
	pb.parts = append(pb.parts, "(?:"+pattern+")")
	return pb
}

// Capture adds a capturing group containing the given pattern.
func (pb *PatternBuilder) Capture(pattern string) *PatternBuilder {
	pb.parts = append(pb.parts, "("+pattern+")")
	return pb
}

// Optional makes the previous element optional (adds ?).
func (pb *PatternBuilder) Optional() *PatternBuilder {
	if len(pb.parts) > 0 {
		pb.parts[len(pb.parts)-1] += "?"
	}
	return pb
}

// OneOrMore makes the previous element repeat one or more times (adds +).
func (pb *PatternBuilder) OneOrMore() *PatternBuilder {
	if len(pb.parts) > 0 {
		pb.parts[len(pb.parts)-1] += "+"
	}
	return pb
}

// ZeroOrMore makes the previous element repeat zero or more times (adds *).
func (pb *PatternBuilder) ZeroOrMore() *PatternBuilder {
	if len(pb.parts) > 0 {
		pb.parts[len(pb.parts)-1] += "*"
	}
	return pb
}

// Or adds an alternation with the given patterns.
func (pb *PatternBuilder) Or(patterns ...string) *PatternBuilder {
	if len(patterns) > 0 {
		combined := "(?:"
		for i, p := range patterns {
			if i > 0 {
				combined += "|"
			}
			combined += p
		}
		combined += ")"
		pb.parts = append(pb.parts, combined)
	}
	return pb
}

// String returns the constructed pattern string.
func (pb *PatternBuilder) String() string {
	result := ""
	for _, part := range pb.parts {
		result += part
	}
	return result
}

// Build compiles the pattern and returns the compiled regex.
func (pb *PatternBuilder) Build() (*regexp.Regexp, error) {
	return regexp.Compile(pb.String())
}

// BuildCached compiles the pattern using the provided cache.
func (pb *PatternBuilder) BuildCached(cache *RegexCache) (*regexp.Regexp, error) {
	return cache.GetOrCompile(pb.String())
}

// MustBuild compiles the pattern and panics on error.
//
// Deprecated: This function can crash the application when called from
// background goroutines with invalid patterns. Use Build instead, which
// returns an error that can be handled gracefully. For background goroutines,
// use SafeBuild which includes panic recovery.
func (pb *PatternBuilder) MustBuild() *regexp.Regexp {
	re, err := pb.Build()
	if err != nil {
		panic("PatternBuilder: invalid pattern: " + err.Error())
	}
	return re
}

// SafeBuild compiles the pattern with panic recovery for use in background
// goroutines or when processing untrusted patterns.
func (pb *PatternBuilder) SafeBuild() (re *regexp.Regexp, err error) {
	defer func() {
		if r := recover(); r != nil {
			re = nil
			err = recoverToError(r)
		}
	}()
	return pb.Build()
}

// =============================================================================
// Optimized Tokenization Functions (using pre-compiled patterns)
// =============================================================================

// TokenizeQueryOptimized tokenizes a query using the pre-compiled TokenizePattern.
// This replaces the original tokenizeQuery function that compiled regex on every call.
func TokenizeQueryOptimized(query string) []string {
	if query == "" {
		return nil
	}

	parts := TokenizePattern.Split(query, -1)

	// Filter empty strings
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// SplitOnWhitespace splits text on whitespace using the pre-compiled pattern.
func SplitOnWhitespace(text string) []string {
	if text == "" {
		return nil
	}
	return WhitespacePattern.Split(text, -1)
}

// RemovePunctuation removes common punctuation from text.
func RemovePunctuation(text string) string {
	return PunctuationPattern.ReplaceAllString(text, "")
}

// SplitCamelCase splits a camelCase or PascalCase identifier into words.
// Example: "getUserName" -> ["get", "User", "Name"]
func SplitCamelCase(identifier string) []string {
	// Insert space before uppercase letters that follow lowercase
	spaced := CamelCasePattern.ReplaceAllString(identifier, "${1} ${2}")
	return SplitOnWhitespace(spaced)
}

// SplitSnakeCase splits a snake_case or kebab-case identifier into words.
// Example: "get_user_name" -> ["get", "user", "name"]
func SplitSnakeCase(identifier string) []string {
	return SnakeCasePattern.Split(identifier, -1)
}

// identifierExtractPattern is a non-anchored version for extraction.
var identifierExtractPattern = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)

// ExtractIdentifiers extracts programming language identifiers from text.
func ExtractIdentifiers(text string) []string {
	return identifierExtractPattern.FindAllString(text, -1)
}

// ExtractEmails extracts email addresses from text.
func ExtractEmails(text string) []string {
	return EmailPattern.FindAllString(text, -1)
}

// ExtractURLs extracts URLs from text.
func ExtractURLs(text string) []string {
	return URLPattern.FindAllString(text, -1)
}

// ExtractVersions extracts version numbers from text.
func ExtractVersions(text string) []string {
	return VersionPattern.FindAllString(text, -1)
}

// ExtractUUIDs extracts UUIDs from text.
func ExtractUUIDs(text string) []string {
	return UUIDPattern.FindAllString(text, -1)
}
