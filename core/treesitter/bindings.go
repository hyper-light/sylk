// Package treesitter provides cgo-free tree-sitter bindings via purego.
// It enables language-agnostic AST parsing across all Sylk agents.
package treesitter

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// TSParser is a handle to a tree-sitter parser.
type TSParser uintptr

// TSTree is a handle to a parsed syntax tree.
type TSTree uintptr

// TSNode represents a node in the syntax tree.
type TSNode struct {
	Context [4]uint32
	ID      uintptr
	Tree    uintptr
}

// TSLanguage is a handle to a tree-sitter language grammar.
type TSLanguage uintptr

// TSQuery is a handle to a compiled query pattern.
type TSQuery uintptr

// TSQueryCursor is a handle to a query cursor for iteration.
type TSQueryCursor uintptr

// TSPoint represents a position in source code.
type TSPoint struct {
	Row    uint32
	Column uint32
}

// TSInputEdit describes an edit to source code for incremental parsing.
type TSInputEdit struct {
	StartByte   uint32
	OldEndByte  uint32
	NewEndByte  uint32
	StartPoint  TSPoint
	OldEndPoint TSPoint
	NewEndPoint TSPoint
}

// TSQueryMatch represents a match from a query execution.
type TSQueryMatch struct {
	ID           uint32
	PatternIndex uint16
	CaptureCount uint16
	Captures     uintptr
}

// TSQueryCapture represents a single capture within a match.
type TSQueryCapture struct {
	Node  TSNode
	Index uint32
}

// Function pointers loaded via purego - parser functions.
var (
	tsParserNew         func() TSParser
	tsParserDelete      func(TSParser)
	tsParserSetLanguage func(TSParser, TSLanguage) bool
	tsParserParseString func(TSParser, TSTree, *byte, uint32) TSTree
	tsParserReset       func(TSParser)
)

// Function pointers loaded via purego - tree functions.
var (
	tsTreeDelete   func(TSTree)
	tsTreeRootNode func(TSTree) TSNode
	tsTreeEdit     func(TSTree, *TSInputEdit)
	tsTreeCopy     func(TSTree) TSTree
	tsTreeLanguage func(TSTree) TSLanguage
)

// Function pointers loaded via purego - node functions.
var (
	tsNodeType                   func(TSNode) *byte
	tsNodeStartByte              func(TSNode) uint32
	tsNodeEndByte                func(TSNode) uint32
	tsNodeStartPoint             func(TSNode) TSPoint
	tsNodeEndPoint               func(TSNode) TSPoint
	tsNodeChildCount             func(TSNode) uint32
	tsNodeChild                  func(TSNode, uint32) TSNode
	tsNodeParent                 func(TSNode) TSNode
	tsNodeIsNull                 func(TSNode) bool
	tsNodeHasError               func(TSNode) bool
	tsNodeString                 func(TSNode) *byte
	tsNodeNamedChild             func(TSNode, uint32) TSNode
	tsNodeNamedChildCount        func(TSNode) uint32
	tsNodeChildByFieldName       func(TSNode, *byte, uint32) TSNode
	tsNodeNextSibling            func(TSNode) TSNode
	tsNodePrevSibling            func(TSNode) TSNode
	tsNodeNextNamedSibling       func(TSNode) TSNode
	tsNodePrevNamedSibling       func(TSNode) TSNode
	tsNodeDescendantForByteRange func(TSNode, uint32, uint32) TSNode
	tsNodeIsNamed                func(TSNode) bool
	tsNodeSymbol                 func(TSNode) uint16
	tsNodeFieldNameForChild      func(TSNode, uint32) *byte
)

// Function pointers loaded via purego - query functions.
var (
	tsQueryNew              func(TSLanguage, *byte, uint32, *uint32, *uint32) TSQuery
	tsQueryDelete           func(TSQuery)
	tsQueryCursorNew        func() TSQueryCursor
	tsQueryCursorDelete     func(TSQueryCursor)
	tsQueryCursorExec       func(TSQueryCursor, TSQuery, TSNode)
	tsQueryCursorNextMatch  func(TSQueryCursor, *TSQueryMatch) bool
	tsQueryCaptureCount     func(TSQuery) uint32
	tsQueryCaptureNameForID func(TSQuery, uint32, *uint32) *byte
	tsQueryPatternCount     func(TSQuery) uint32
	tsQueryStringCount      func(TSQuery) uint32
)

// Function pointers loaded via purego - language info.
var (
	tsLanguageVersion     func(TSLanguage) uint32
	tsLanguageSymbolCount func(TSLanguage) uint32
	tsLanguageSymbolName  func(TSLanguage, uint16) *byte
	tsLanguageSymbolType  func(TSLanguage, uint16) uint32
	tsLanguageFieldCount  func(TSLanguage) uint32
	tsLanguageFieldName   func(TSLanguage, uint16) *byte
)

var (
	initOnce  sync.Once
	initErr   error
	libHandle uintptr
)

// Initialize loads libtree-sitter via purego. This is called once.
// Returns an error if the library cannot be found or loaded.
func Initialize() error {
	initOnce.Do(func() {
		initErr = loadTreeSitterLibrary()
	})
	return initErr
}

// IsInitialized returns true if the library has been successfully loaded.
func IsInitialized() bool {
	return libHandle != 0 && initErr == nil
}

func loadTreeSitterLibrary() error {
	libPath := findTreeSitterLibrary()
	if libPath == "" {
		return fmt.Errorf("libtree-sitter not found; run 'sylk setup' to install")
	}

	if err := openLibrary(libPath); err != nil {
		return err
	}

	return registerAllFunctions()
}

func openLibrary(libPath string) error {
	var err error
	libHandle, err = purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("failed to load libtree-sitter: %w", err)
	}
	return nil
}

func registerAllFunctions() error {
	registrators := []func() error{
		registerParserFuncs,
		registerTreeFuncs,
		registerNodeFuncs,
		registerQueryFuncs,
		registerLanguageFuncs,
	}
	for _, register := range registrators {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}

func registerParserFuncs() error {
	purego.RegisterLibFunc(&tsParserNew, libHandle, "ts_parser_new")
	purego.RegisterLibFunc(&tsParserDelete, libHandle, "ts_parser_delete")
	purego.RegisterLibFunc(&tsParserSetLanguage, libHandle, "ts_parser_set_language")
	purego.RegisterLibFunc(&tsParserParseString, libHandle, "ts_parser_parse_string")
	purego.RegisterLibFunc(&tsParserReset, libHandle, "ts_parser_reset")
	return nil
}

func registerTreeFuncs() error {
	purego.RegisterLibFunc(&tsTreeDelete, libHandle, "ts_tree_delete")
	purego.RegisterLibFunc(&tsTreeRootNode, libHandle, "ts_tree_root_node")
	purego.RegisterLibFunc(&tsTreeEdit, libHandle, "ts_tree_edit")
	purego.RegisterLibFunc(&tsTreeCopy, libHandle, "ts_tree_copy")
	purego.RegisterLibFunc(&tsTreeLanguage, libHandle, "ts_tree_language")
	return nil
}

func registerNodeFuncs() error {
	purego.RegisterLibFunc(&tsNodeType, libHandle, "ts_node_type")
	purego.RegisterLibFunc(&tsNodeStartByte, libHandle, "ts_node_start_byte")
	purego.RegisterLibFunc(&tsNodeEndByte, libHandle, "ts_node_end_byte")
	purego.RegisterLibFunc(&tsNodeStartPoint, libHandle, "ts_node_start_point")
	purego.RegisterLibFunc(&tsNodeEndPoint, libHandle, "ts_node_end_point")
	purego.RegisterLibFunc(&tsNodeChildCount, libHandle, "ts_node_child_count")
	purego.RegisterLibFunc(&tsNodeChild, libHandle, "ts_node_child")
	purego.RegisterLibFunc(&tsNodeParent, libHandle, "ts_node_parent")
	purego.RegisterLibFunc(&tsNodeIsNull, libHandle, "ts_node_is_null")
	purego.RegisterLibFunc(&tsNodeHasError, libHandle, "ts_node_has_error")
	purego.RegisterLibFunc(&tsNodeString, libHandle, "ts_node_string")
	purego.RegisterLibFunc(&tsNodeNamedChild, libHandle, "ts_node_named_child")
	purego.RegisterLibFunc(&tsNodeNamedChildCount, libHandle, "ts_node_named_child_count")
	purego.RegisterLibFunc(&tsNodeChildByFieldName, libHandle, "ts_node_child_by_field_name")
	purego.RegisterLibFunc(&tsNodeNextSibling, libHandle, "ts_node_next_sibling")
	purego.RegisterLibFunc(&tsNodePrevSibling, libHandle, "ts_node_prev_sibling")
	purego.RegisterLibFunc(&tsNodeNextNamedSibling, libHandle, "ts_node_next_named_sibling")
	purego.RegisterLibFunc(&tsNodePrevNamedSibling, libHandle, "ts_node_prev_named_sibling")
	purego.RegisterLibFunc(&tsNodeDescendantForByteRange, libHandle, "ts_node_descendant_for_byte_range")
	purego.RegisterLibFunc(&tsNodeIsNamed, libHandle, "ts_node_is_named")
	purego.RegisterLibFunc(&tsNodeSymbol, libHandle, "ts_node_symbol")
	purego.RegisterLibFunc(&tsNodeFieldNameForChild, libHandle, "ts_node_field_name_for_child")
	return nil
}

func registerQueryFuncs() error {
	purego.RegisterLibFunc(&tsQueryNew, libHandle, "ts_query_new")
	purego.RegisterLibFunc(&tsQueryDelete, libHandle, "ts_query_delete")
	purego.RegisterLibFunc(&tsQueryCursorNew, libHandle, "ts_query_cursor_new")
	purego.RegisterLibFunc(&tsQueryCursorDelete, libHandle, "ts_query_cursor_delete")
	purego.RegisterLibFunc(&tsQueryCursorExec, libHandle, "ts_query_cursor_exec")
	purego.RegisterLibFunc(&tsQueryCursorNextMatch, libHandle, "ts_query_cursor_next_match")
	purego.RegisterLibFunc(&tsQueryCaptureCount, libHandle, "ts_query_capture_count")
	purego.RegisterLibFunc(&tsQueryCaptureNameForID, libHandle, "ts_query_capture_name_for_id")
	purego.RegisterLibFunc(&tsQueryPatternCount, libHandle, "ts_query_pattern_count")
	purego.RegisterLibFunc(&tsQueryStringCount, libHandle, "ts_query_string_count")
	return nil
}

func registerLanguageFuncs() error {
	purego.RegisterLibFunc(&tsLanguageVersion, libHandle, "ts_language_version")
	purego.RegisterLibFunc(&tsLanguageSymbolCount, libHandle, "ts_language_symbol_count")
	purego.RegisterLibFunc(&tsLanguageSymbolName, libHandle, "ts_language_symbol_name")
	purego.RegisterLibFunc(&tsLanguageSymbolType, libHandle, "ts_language_symbol_type")
	purego.RegisterLibFunc(&tsLanguageFieldCount, libHandle, "ts_language_field_count")
	purego.RegisterLibFunc(&tsLanguageFieldName, libHandle, "ts_language_field_name_for_id")
	return nil
}

func findTreeSitterLibrary() string {
	searchPaths := buildSearchPaths()
	for _, path := range searchPaths {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func buildSearchPaths() []string {
	paths := []string{
		filepath.Join(getSylkDataDir(), "lib", libraryName()),
		"/usr/local/lib/" + libraryName(),
		"/usr/lib/" + libraryName(),
	}

	if runtime.GOOS == "darwin" {
		paths = append(paths,
			"/opt/homebrew/lib/"+libraryName(),
			"/usr/local/opt/tree-sitter/lib/"+libraryName(),
		)
	}

	if runtime.GOOS == "linux" {
		paths = append(paths,
			"/usr/lib/x86_64-linux-gnu/"+libraryName(),
			"/usr/lib/aarch64-linux-gnu/"+libraryName(),
		)
	}

	return paths
}

func libraryName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libtree-sitter.dylib"
	case "windows":
		return "tree-sitter.dll"
	default:
		return "libtree-sitter.so"
	}
}

func getSylkDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "sylk")
	}
	return getPlatformDataDir()
}

func getPlatformDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return dataDirForOS(home)
}

func dataDirForOS(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "sylk")
	case "windows":
		return windowsDataDir(home)
	default:
		return filepath.Join(home, ".local", "share", "sylk")
	}
}

func windowsDataDir(home string) string {
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "sylk", "data")
	}
	return filepath.Join(home, "AppData", "Roaming", "sylk", "data")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Close releases the library handle. Should be called on shutdown.
func Close() error {
	if libHandle != 0 {
		if err := purego.Dlclose(libHandle); err != nil {
			return fmt.Errorf("failed to close libtree-sitter: %w", err)
		}
		libHandle = 0
	}
	return nil
}

// ParserNew creates a new parser instance.
func ParserNew() TSParser {
	return tsParserNew()
}

// ParserDelete frees a parser.
func ParserDelete(parser TSParser) {
	tsParserDelete(parser)
}

// ParserSetLanguage sets the language for a parser.
func ParserSetLanguage(parser TSParser, lang TSLanguage) bool {
	return tsParserSetLanguage(parser, lang)
}

// ParserParseString parses a string with optional old tree for incremental parsing.
func ParserParseString(parser TSParser, oldTree TSTree, content []byte) TSTree {
	if len(content) == 0 {
		return 0
	}
	return tsParserParseString(parser, oldTree, &content[0], uint32(len(content)))
}

// ParserReset resets the parser to a clean state.
func ParserReset(parser TSParser) {
	tsParserReset(parser)
}

// TreeDelete frees a syntax tree.
func TreeDelete(tree TSTree) {
	tsTreeDelete(tree)
}

// TreeRootNode returns the root node of a tree.
func TreeRootNode(tree TSTree) TSNode {
	return tsTreeRootNode(tree)
}

// TreeEdit applies an edit to a tree for incremental parsing.
func TreeEdit(tree TSTree, edit *TSInputEdit) {
	tsTreeEdit(tree, edit)
}

// TreeCopy creates a copy of a tree.
func TreeCopy(tree TSTree) TSTree {
	return tsTreeCopy(tree)
}

// TreeLanguage returns the language of a tree.
func TreeLanguage(tree TSTree) TSLanguage {
	return tsTreeLanguage(tree)
}

// NodeType returns the type of a node as a string.
func NodeType(node TSNode) string {
	ptr := tsNodeType(node)
	if ptr == nil {
		return ""
	}
	return cStringToGo(ptr)
}

// NodeStartByte returns the start byte offset of a node.
func NodeStartByte(node TSNode) uint32 {
	return tsNodeStartByte(node)
}

// NodeEndByte returns the end byte offset of a node.
func NodeEndByte(node TSNode) uint32 {
	return tsNodeEndByte(node)
}

// NodeStartPoint returns the start position of a node.
func NodeStartPoint(node TSNode) TSPoint {
	return tsNodeStartPoint(node)
}

// NodeEndPoint returns the end position of a node.
func NodeEndPoint(node TSNode) TSPoint {
	return tsNodeEndPoint(node)
}

// NodeChildCount returns the number of children of a node.
func NodeChildCount(node TSNode) uint32 {
	return tsNodeChildCount(node)
}

// NodeChild returns the child at the given index.
func NodeChild(node TSNode, index uint32) TSNode {
	return tsNodeChild(node, index)
}

// NodeParent returns the parent of a node.
func NodeParent(node TSNode) TSNode {
	return tsNodeParent(node)
}

// NodeIsNull returns true if the node is null.
func NodeIsNull(node TSNode) bool {
	return tsNodeIsNull(node)
}

// NodeHasError returns true if the node contains a parse error.
func NodeHasError(node TSNode) bool {
	return tsNodeHasError(node)
}

// NodeString returns a string representation of the node for debugging.
func NodeString(node TSNode) string {
	ptr := tsNodeString(node)
	if ptr == nil {
		return ""
	}
	return cStringToGo(ptr)
}

// NodeNamedChild returns the named child at the given index.
func NodeNamedChild(node TSNode, index uint32) TSNode {
	return tsNodeNamedChild(node, index)
}

// NodeNamedChildCount returns the number of named children.
func NodeNamedChildCount(node TSNode) uint32 {
	return tsNodeNamedChildCount(node)
}

// NodeChildByFieldName returns the child with the given field name.
func NodeChildByFieldName(node TSNode, name string) TSNode {
	if name == "" {
		return TSNode{}
	}
	nameBytes := []byte(name)
	return tsNodeChildByFieldName(node, &nameBytes[0], uint32(len(name)))
}

// NodeNextSibling returns the next sibling of a node.
func NodeNextSibling(node TSNode) TSNode {
	return tsNodeNextSibling(node)
}

// NodePrevSibling returns the previous sibling of a node.
func NodePrevSibling(node TSNode) TSNode {
	return tsNodePrevSibling(node)
}

// NodeNextNamedSibling returns the next named sibling.
func NodeNextNamedSibling(node TSNode) TSNode {
	return tsNodeNextNamedSibling(node)
}

// NodePrevNamedSibling returns the previous named sibling.
func NodePrevNamedSibling(node TSNode) TSNode {
	return tsNodePrevNamedSibling(node)
}

// NodeDescendantForByteRange returns the smallest descendant covering the range.
func NodeDescendantForByteRange(node TSNode, start, end uint32) TSNode {
	return tsNodeDescendantForByteRange(node, start, end)
}

// NodeIsNamed returns true if the node is a named node.
func NodeIsNamed(node TSNode) bool {
	return tsNodeIsNamed(node)
}

// NodeSymbol returns the symbol ID of a node.
func NodeSymbol(node TSNode) uint16 {
	return tsNodeSymbol(node)
}

// NodeFieldNameForChild returns the field name for a child at index.
func NodeFieldNameForChild(node TSNode, index uint32) string {
	ptr := tsNodeFieldNameForChild(node, index)
	if ptr == nil {
		return ""
	}
	return cStringToGo(ptr)
}

// QueryNew creates a new query from a pattern string.
func QueryNew(lang TSLanguage, pattern string) (TSQuery, uint32, uint32, error) {
	if pattern == "" {
		return 0, 0, 0, fmt.Errorf("empty query pattern")
	}
	patternBytes := []byte(pattern)
	var errorOffset, errorType uint32
	query := tsQueryNew(lang, &patternBytes[0], uint32(len(pattern)), &errorOffset, &errorType)
	if query == 0 {
		return 0, errorOffset, errorType, fmt.Errorf("query error at offset %d, type %d", errorOffset, errorType)
	}
	return query, 0, 0, nil
}

// QueryDelete frees a query.
func QueryDelete(query TSQuery) {
	tsQueryDelete(query)
}

// QueryCursorNew creates a new query cursor.
func QueryCursorNew() TSQueryCursor {
	return TSQueryCursor(tsQueryCursorNew())
}

// QueryCursorDelete frees a query cursor.
func QueryCursorDelete(cursor TSQueryCursor) {
	tsQueryCursorDelete(cursor)
}

// QueryCursorExec starts a query on a node.
func QueryCursorExec(cursor TSQueryCursor, query TSQuery, node TSNode) {
	tsQueryCursorExec(cursor, query, node)
}

// QueryCursorNextMatch gets the next match.
func QueryCursorNextMatch(cursor TSQueryCursor) (*TSQueryMatch, bool) {
	var match TSQueryMatch
	ok := tsQueryCursorNextMatch(cursor, &match)
	if !ok {
		return nil, false
	}
	return &match, true
}

// QueryCaptureCount returns the number of captures in a query.
func QueryCaptureCount(query TSQuery) uint32 {
	return tsQueryCaptureCount(query)
}

// QueryCaptureNameForID returns the name of a capture.
func QueryCaptureNameForID(query TSQuery, id uint32) string {
	var length uint32
	ptr := tsQueryCaptureNameForID(query, id, &length)
	if ptr == nil {
		return ""
	}
	return cStringToGoN(ptr, int(length))
}

// QueryPatternCount returns the number of patterns in a query.
func QueryPatternCount(query TSQuery) uint32 {
	return tsQueryPatternCount(query)
}

// LanguageVersion returns the ABI version of a language.
func LanguageVersion(lang TSLanguage) uint32 {
	return tsLanguageVersion(lang)
}

// LanguageSymbolCount returns the number of symbols in a language.
func LanguageSymbolCount(lang TSLanguage) uint32 {
	return tsLanguageSymbolCount(lang)
}

// LanguageSymbolName returns the name of a symbol.
func LanguageSymbolName(lang TSLanguage, symbol uint16) string {
	ptr := tsLanguageSymbolName(lang, symbol)
	if ptr == nil {
		return ""
	}
	return cStringToGo(ptr)
}

// LanguageFieldCount returns the number of fields in a language.
func LanguageFieldCount(lang TSLanguage) uint32 {
	return tsLanguageFieldCount(lang)
}

// LanguageFieldName returns the name of a field.
func LanguageFieldName(lang TSLanguage, id uint16) string {
	ptr := tsLanguageFieldName(lang, id)
	if ptr == nil {
		return ""
	}
	return cStringToGo(ptr)
}

func cStringToGo(ptr *byte) string {
	if ptr == nil {
		return ""
	}
	length := cStringLength(ptr)
	if length == 0 {
		return ""
	}
	return copyBytesToString(ptr, length)
}

func cStringLength(ptr *byte) int {
	var length int
	for p := ptr; *p != 0; p = addOffset(p, 1) {
		length++
	}
	return length
}

func copyBytesToString(ptr *byte, length int) string {
	bytes := make([]byte, length)
	for i := range length {
		bytes[i] = *addOffset(ptr, i)
	}
	return string(bytes)
}

func cStringToGoN(ptr *byte, length int) string {
	if ptr == nil || length == 0 {
		return ""
	}
	return copyBytesToString(ptr, length)
}

// addOffset adds an offset to a pointer.
func addOffset(ptr *byte, offset int) *byte {
	return (*byte)(unsafe.Add(unsafe.Pointer(ptr), offset))
}

// readTSQueryCapture reads a TSQueryCapture from a captures array at the given index.
func readTSQueryCapture(capturesPtr uintptr, index int) TSQueryCapture {
	captureSize := unsafe.Sizeof(TSQueryCapture{})
	addr := capturesPtr + uintptr(index)*captureSize
	return *(*TSQueryCapture)(unsafe.Pointer(addr))
}
