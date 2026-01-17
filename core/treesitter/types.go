package treesitter

import (
	"sync"
)

type Parser struct {
	ptr      TSParser
	language *Language
	mu       sync.Mutex
}

type Language struct {
	ptr       TSLanguage
	name      string
	libPath   string
	libHandle uintptr
}

type Tree struct {
	ptr      TSTree
	language *Language
	source   []byte
}

type Node struct {
	raw    TSNode
	tree   *Tree
	source []byte
}

type Query struct {
	ptr      TSQuery
	language *Language
	pattern  string
}

type QueryCursor struct {
	ptr TSQueryCursor
}

type QueryMatch struct {
	PatternIndex uint16
	Captures     []QueryCapture
}

type QueryCapture struct {
	Name    string
	Node    *Node
	Content string
}

type Point struct {
	Row    uint32
	Column uint32
}

type Range struct {
	StartPoint Point
	EndPoint   Point
	StartByte  uint32
	EndByte    uint32
}

type InputEdit struct {
	StartByte   uint32
	OldEndByte  uint32
	NewEndByte  uint32
	StartPoint  Point
	OldEndPoint Point
	NewEndPoint Point
}

type NodeInfo struct {
	Type      string     `json:"type"`
	StartLine uint32     `json:"start_line"`
	EndLine   uint32     `json:"end_line"`
	StartCol  uint32     `json:"start_col"`
	EndCol    uint32     `json:"end_col"`
	Content   string     `json:"content,omitempty"`
	Children  []NodeInfo `json:"children,omitempty"`
}

type FunctionInfo struct {
	Name       string `json:"name"`
	StartLine  uint32 `json:"start_line"`
	EndLine    uint32 `json:"end_line"`
	Parameters string `json:"parameters"`
	ReturnType string `json:"return_type,omitempty"`
	IsMethod   bool   `json:"is_method"`
	Receiver   string `json:"receiver,omitempty"`
}

type TypeInfo struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	StartLine uint32   `json:"start_line"`
	EndLine   uint32   `json:"end_line"`
	Fields    []string `json:"fields,omitempty"`
}

type ImportInfo struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
	Line  uint32 `json:"line"`
}

type ParseError struct {
	Line    uint32 `json:"line"`
	Column  uint32 `json:"column"`
	Message string `json:"message"`
}

type ParseResult struct {
	Language    string         `json:"language"`
	FilePath    string         `json:"file_path"`
	RootNode    *NodeInfo      `json:"root_node"`
	Functions   []FunctionInfo `json:"functions,omitempty"`
	Types       []TypeInfo     `json:"types,omitempty"`
	Imports     []ImportInfo   `json:"imports,omitempty"`
	Errors      []ParseError   `json:"errors,omitempty"`
	ParseTimeMs int64          `json:"parse_time_ms"`
}

func NewParser() *Parser {
	ptr := ParserNew()
	return &Parser{ptr: ptr}
}

func (p *Parser) SetLanguage(lang *Language) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	ok := ParserSetLanguage(p.ptr, lang.ptr)
	if ok {
		p.language = lang
	}
	return ok
}

func (p *Parser) Parse(content []byte, oldTree *Tree) (*Tree, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var oldPtr TSTree
	if oldTree != nil {
		oldPtr = oldTree.ptr
	}

	treePtr := ParserParseString(p.ptr, oldPtr, content)
	if treePtr == 0 {
		return nil, ErrParseFailed
	}

	return &Tree{
		ptr:      treePtr,
		language: p.language,
		source:   content,
	}, nil
}

func (p *Parser) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	ParserReset(p.ptr)
}

func (p *Parser) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ptr != 0 {
		ParserDelete(p.ptr)
		p.ptr = 0
	}
}

func (p *Parser) Language() *Language {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.language
}

func (l *Language) Name() string {
	return l.name
}

func (l *Language) Version() uint32 {
	return LanguageVersion(l.ptr)
}

func (l *Language) SymbolCount() uint32 {
	return LanguageSymbolCount(l.ptr)
}

func (l *Language) FieldCount() uint32 {
	return LanguageFieldCount(l.ptr)
}

func (t *Tree) RootNode() *Node {
	raw := TreeRootNode(t.ptr)
	return &Node{raw: raw, tree: t, source: t.source}
}

func (t *Tree) Language() *Language {
	return t.language
}

func (t *Tree) Copy() *Tree {
	copied := TreeCopy(t.ptr)
	return &Tree{
		ptr:      copied,
		language: t.language,
		source:   t.source,
	}
}

func (t *Tree) Edit(edit *InputEdit) {
	tsEdit := &TSInputEdit{
		StartByte:   edit.StartByte,
		OldEndByte:  edit.OldEndByte,
		NewEndByte:  edit.NewEndByte,
		StartPoint:  TSPoint{Row: edit.StartPoint.Row, Column: edit.StartPoint.Column},
		OldEndPoint: TSPoint{Row: edit.OldEndPoint.Row, Column: edit.OldEndPoint.Column},
		NewEndPoint: TSPoint{Row: edit.NewEndPoint.Row, Column: edit.NewEndPoint.Column},
	}
	TreeEdit(t.ptr, tsEdit)
}

func (t *Tree) Close() {
	if t.ptr != 0 {
		TreeDelete(t.ptr)
		t.ptr = 0
	}
}

func (n *Node) Type() string {
	return NodeType(n.raw)
}

func (n *Node) StartByte() uint32 {
	return NodeStartByte(n.raw)
}

func (n *Node) EndByte() uint32 {
	return NodeEndByte(n.raw)
}

func (n *Node) StartPoint() Point {
	p := NodeStartPoint(n.raw)
	return Point{Row: p.Row, Column: p.Column}
}

func (n *Node) EndPoint() Point {
	p := NodeEndPoint(n.raw)
	return Point{Row: p.Row, Column: p.Column}
}

func (n *Node) ChildCount() uint32 {
	return NodeChildCount(n.raw)
}

func (n *Node) Child(index uint32) *Node {
	raw := NodeChild(n.raw, index)
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) NamedChildCount() uint32 {
	return NodeNamedChildCount(n.raw)
}

func (n *Node) NamedChild(index uint32) *Node {
	raw := NodeNamedChild(n.raw, index)
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) Parent() *Node {
	raw := NodeParent(n.raw)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) NextSibling() *Node {
	raw := NodeNextSibling(n.raw)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) PrevSibling() *Node {
	raw := NodePrevSibling(n.raw)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) NextNamedSibling() *Node {
	raw := NodeNextNamedSibling(n.raw)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) PrevNamedSibling() *Node {
	raw := NodePrevNamedSibling(n.raw)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) ChildByFieldName(name string) *Node {
	raw := NodeChildByFieldName(n.raw, name)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) DescendantForByteRange(start, end uint32) *Node {
	raw := NodeDescendantForByteRange(n.raw, start, end)
	if NodeIsNull(raw) {
		return nil
	}
	return &Node{raw: raw, tree: n.tree, source: n.source}
}

func (n *Node) IsNull() bool {
	return NodeIsNull(n.raw)
}

func (n *Node) IsNamed() bool {
	return NodeIsNamed(n.raw)
}

func (n *Node) HasError() bool {
	return NodeHasError(n.raw)
}

func (n *Node) Symbol() uint16 {
	return NodeSymbol(n.raw)
}

func (n *Node) Content() string {
	start := n.StartByte()
	end := n.EndByte()
	if int(end) > len(n.source) {
		end = uint32(len(n.source))
	}
	if start >= end {
		return ""
	}
	return string(n.source[start:end])
}

func (n *Node) String() string {
	return NodeString(n.raw)
}

func (n *Node) FieldNameForChild(index uint32) string {
	return NodeFieldNameForChild(n.raw, index)
}

func (n *Node) ToInfo() *NodeInfo {
	startPoint := n.StartPoint()
	endPoint := n.EndPoint()
	return &NodeInfo{
		Type:      n.Type(),
		StartLine: startPoint.Row + 1,
		EndLine:   endPoint.Row + 1,
		StartCol:  startPoint.Column,
		EndCol:    endPoint.Column,
	}
}

func NewQuery(lang *Language, pattern string) (*Query, error) {
	ptr, errOffset, errType, err := QueryNew(lang.ptr, pattern)
	if err != nil {
		return nil, &QueryError{Offset: errOffset, Type: errType, Pattern: pattern}
	}
	return &Query{ptr: ptr, language: lang, pattern: pattern}, nil
}

func (q *Query) Close() {
	if q.ptr != 0 {
		QueryDelete(q.ptr)
		q.ptr = 0
	}
}

func (q *Query) CaptureCount() uint32 {
	return QueryCaptureCount(q.ptr)
}

func (q *Query) PatternCount() uint32 {
	return QueryPatternCount(q.ptr)
}

func (q *Query) CaptureNameForID(id uint32) string {
	return QueryCaptureNameForID(q.ptr, id)
}

func NewQueryCursor() *QueryCursor {
	return &QueryCursor{ptr: QueryCursorNew()}
}

func (c *QueryCursor) Close() {
	if c.ptr != 0 {
		QueryCursorDelete(c.ptr)
		c.ptr = 0
	}
}

func (c *QueryCursor) Exec(query *Query, node *Node) {
	QueryCursorExec(c.ptr, query.ptr, node.raw)
}

func (c *QueryCursor) NextMatch(query *Query, node *Node) (*QueryMatch, bool) {
	match, ok := QueryCursorNextMatch(c.ptr)
	if !ok {
		return nil, false
	}
	return convertMatch(match, query, node), true
}

func convertMatch(raw *TSQueryMatch, query *Query, node *Node) *QueryMatch {
	result := &QueryMatch{
		PatternIndex: raw.PatternIndex,
		Captures:     make([]QueryCapture, 0, raw.CaptureCount),
	}
	return result
}

func PointToTSPoint(p Point) TSPoint {
	return TSPoint{Row: p.Row, Column: p.Column}
}

func TSPointToPoint(p TSPoint) Point {
	return Point{Row: p.Row, Column: p.Column}
}

type QueryError struct {
	Offset  uint32
	Type    uint32
	Pattern string
}

func (e *QueryError) Error() string {
	return "query error at offset " + uintToString(e.Offset)
}

func uintToString(n uint32) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
