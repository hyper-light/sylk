package treesitter

import (
	"sync"
)

type QueryEngine struct {
	queryCache map[queryCacheKey]*Query
	mu         sync.RWMutex
	maxCached  int
}

type queryCacheKey struct {
	language string
	pattern  string
}

type QueryResult struct {
	Matches      []QueryMatch `json:"matches"`
	PatternCount uint32       `json:"pattern_count"`
	CaptureCount uint32       `json:"capture_count"`
}

func NewQueryEngine(maxCached int) *QueryEngine {
	return &QueryEngine{
		queryCache: make(map[queryCacheKey]*Query),
		maxCached:  maxCached,
	}
}

func (e *QueryEngine) Execute(lang *Language, node *Node, pattern string) (*QueryResult, error) {
	query, err := e.getOrCreateQuery(lang, pattern)
	if err != nil {
		return nil, err
	}

	matches := executeQuery(query, node)
	return &QueryResult{
		Matches:      matches,
		PatternCount: query.PatternCount(),
		CaptureCount: query.CaptureCount(),
	}, nil
}

func (e *QueryEngine) getOrCreateQuery(lang *Language, pattern string) (*Query, error) {
	key := queryCacheKey{language: lang.name, pattern: pattern}

	e.mu.RLock()
	if q, ok := e.queryCache[key]; ok {
		e.mu.RUnlock()
		return q, nil
	}
	e.mu.RUnlock()

	return e.createAndCacheQuery(lang, pattern, key)
}

func (e *QueryEngine) createAndCacheQuery(lang *Language, pattern string, key queryCacheKey) (*Query, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if q, ok := e.queryCache[key]; ok {
		return q, nil
	}

	query, err := NewQuery(lang, pattern)
	if err != nil {
		return nil, err
	}

	e.evictIfNeeded()
	e.queryCache[key] = query
	return query, nil
}

func (e *QueryEngine) evictIfNeeded() {
	if len(e.queryCache) < e.maxCached {
		return
	}
	for k, q := range e.queryCache {
		q.Close()
		delete(e.queryCache, k)
		break
	}
}

func (e *QueryEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, q := range e.queryCache {
		q.Close()
	}
	e.queryCache = make(map[queryCacheKey]*Query)
}

func (e *QueryEngine) CacheSize() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.queryCache)
}

func executeQuery(query *Query, node *Node) []QueryMatch {
	cursor := NewQueryCursor()
	defer cursor.Close()

	cursor.Exec(query, node)
	return collectMatches(cursor, query, node)
}

func collectMatches(cursor *QueryCursor, query *Query, node *Node) []QueryMatch {
	matches := make([]QueryMatch, 0, 16)
	for {
		match, ok := nextMatchWithCaptures(cursor, query, node)
		if !ok {
			break
		}
		matches = append(matches, *match)
	}
	return matches
}

func nextMatchWithCaptures(cursor *QueryCursor, query *Query, node *Node) (*QueryMatch, bool) {
	rawMatch, ok := QueryCursorNextMatch(cursor.ptr)
	if !ok {
		return nil, false
	}
	return convertMatchWithCaptures(rawMatch, query, node), true
}

func convertMatchWithCaptures(raw *TSQueryMatch, query *Query, node *Node) *QueryMatch {
	captures := extractCaptures(raw, query, node)
	return &QueryMatch{
		PatternIndex: raw.PatternIndex,
		Captures:     captures,
	}
}

func extractCaptures(raw *TSQueryMatch, query *Query, node *Node) []QueryCapture {
	if raw.CaptureCount == 0 || raw.Captures == 0 {
		return nil
	}
	return buildCaptures(raw, query, node)
}

func buildCaptures(raw *TSQueryMatch, query *Query, node *Node) []QueryCapture {
	captures := make([]QueryCapture, 0, raw.CaptureCount)
	for i := range uint16(raw.CaptureCount) {
		cap := getCaptureAtIndex(raw.Captures, i, query, node)
		captures = append(captures, cap)
	}
	return captures
}

func getCaptureAtIndex(capturesPtr uintptr, idx uint16, query *Query, node *Node) QueryCapture {
	rawCapture := readCaptureAtIndex(capturesPtr, idx)
	captureName := query.CaptureNameForID(uint32(rawCapture.Index))
	capturedNode := wrapCapturedNode(rawCapture.Node, node)
	return QueryCapture{
		Name:    captureName,
		Node:    capturedNode,
		Content: capturedNode.Content(),
	}
}

func readCaptureAtIndex(ptr uintptr, idx uint16) TSQueryCapture {
	return readTSQueryCapture(ptr, int(idx))
}

func wrapCapturedNode(raw TSNode, parent *Node) *Node {
	return &Node{
		raw:    raw,
		tree:   parent.tree,
		source: parent.source,
	}
}

func (q *Query) Execute(node *Node) []QueryMatch {
	return executeQuery(q, node)
}

func (q *Query) Pattern() string {
	return q.pattern
}

type QueryBuilder struct {
	patterns []string
}

func NewQueryBuilder() *QueryBuilder {
	return &QueryBuilder{patterns: make([]string, 0, 4)}
}

func (b *QueryBuilder) Add(pattern string) *QueryBuilder {
	b.patterns = append(b.patterns, pattern)
	return b
}

func (b *QueryBuilder) Build() string {
	return joinPatterns(b.patterns)
}

func joinPatterns(patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	return concatStrings(patterns)
}

func concatStrings(strs []string) string {
	total := calculateTotalLength(strs)
	result := make([]byte, 0, total)
	for i, s := range strs {
		if i > 0 {
			result = append(result, '\n')
		}
		result = append(result, s...)
	}
	return string(result)
}

func calculateTotalLength(strs []string) int {
	total := 0
	for _, s := range strs {
		total += len(s) + 1
	}
	return total
}

var CommonQueries = map[string]map[string]string{
	"go": {
		"functions":    "(function_declaration name: (identifier) @name) @func",
		"methods":      "(method_declaration name: (field_identifier) @name) @method",
		"types":        "(type_declaration (type_spec name: (type_identifier) @name)) @type",
		"imports":      "(import_spec path: (interpreted_string_literal) @path)",
		"interfaces":   "(type_declaration (type_spec name: (type_identifier) @name type: (interface_type))) @interface",
		"structs":      "(type_declaration (type_spec name: (type_identifier) @name type: (struct_type))) @struct",
		"calls":        "(call_expression function: (identifier) @func)",
		"method_calls": "(call_expression function: (selector_expression field: (field_identifier) @method))",
	},
	"python": {
		"functions": "(function_definition name: (identifier) @name) @func",
		"classes":   "(class_definition name: (identifier) @name) @class",
		"imports":   "(import_statement (dotted_name) @module)",
		"calls":     "(call function: (identifier) @func)",
		"methods":   "(function_definition name: (identifier) @name) @method",
	},
	"javascript": {
		"functions":      "(function_declaration name: (identifier) @name) @func",
		"arrow_funcs":    "(arrow_function) @arrow",
		"classes":        "(class_declaration name: (identifier) @name) @class",
		"imports":        "(import_statement source: (string) @source)",
		"exports":        "(export_statement) @export",
		"calls":          "(call_expression function: (identifier) @func)",
		"jsx_elements":   "(jsx_element open_tag: (jsx_opening_element name: (identifier) @name))",
		"jsx_self_close": "(jsx_self_closing_element name: (identifier) @name)",
	},
	"typescript": {
		"functions":   "(function_declaration name: (identifier) @name) @func",
		"arrow_funcs": "(arrow_function) @arrow",
		"classes":     "(class_declaration name: (identifier) @name) @class",
		"interfaces":  "(interface_declaration name: (type_identifier) @name) @interface",
		"types":       "(type_alias_declaration name: (type_identifier) @name) @type",
		"imports":     "(import_statement source: (string) @source)",
		"exports":     "(export_statement) @export",
	},
	"rust": {
		"functions": "(function_item name: (identifier) @name) @func",
		"structs":   "(struct_item name: (type_identifier) @name) @struct",
		"enums":     "(enum_item name: (type_identifier) @name) @enum",
		"traits":    "(trait_item name: (type_identifier) @name) @trait",
		"impls":     "(impl_item type: (type_identifier) @type) @impl",
		"uses":      "(use_declaration argument: (scoped_identifier) @path)",
		"mods":      "(mod_item name: (identifier) @name) @mod",
	},
}

func GetCommonQuery(language, queryName string) (string, bool) {
	langQueries, ok := CommonQueries[language]
	if !ok {
		return "", false
	}
	query, ok := langQueries[queryName]
	return query, ok
}

func ListCommonQueries(language string) []string {
	langQueries, ok := CommonQueries[language]
	if !ok {
		return nil
	}
	return extractQueryNames(langQueries)
}

func extractQueryNames(queries map[string]string) []string {
	names := make([]string, 0, len(queries))
	for name := range queries {
		names = append(names, name)
	}
	return names
}
