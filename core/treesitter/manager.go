package treesitter

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
)

type TreeSitterManager struct {
	loader     *GrammarLoader
	treeCache  *TreeCache
	queryCache *QueryCache
	mu         sync.RWMutex
}

type TreeCache struct {
	entries map[versioning.VersionID]*CachedTree
	maxSize int
	mu      sync.RWMutex
}

type CachedTree struct {
	Tree           *Tree
	Language       string
	ParsedAt       time.Time
	GrammarVersion uint32
}

type QueryCache struct {
	entries map[queryCacheKey]*Query
	maxSize int
	mu      sync.RWMutex
}

type queryCacheKey struct {
	language string
	pattern  string
}

func NewTreeSitterManager(loader *GrammarLoader, treeCacheSize, queryCacheSize int) *TreeSitterManager {
	return &TreeSitterManager{
		loader:     loader,
		treeCache:  newTreeCache(treeCacheSize),
		queryCache: newQueryCache(queryCacheSize),
	}
}

func newTreeCache(maxSize int) *TreeCache {
	return &TreeCache{
		entries: make(map[versioning.VersionID]*CachedTree),
		maxSize: maxSize,
	}
}

func newQueryCache(maxSize int) *QueryCache {
	return &QueryCache{
		entries: make(map[queryCacheKey]*Query),
		maxSize: maxSize,
	}
}

func (m *TreeSitterManager) ParseVersion(ctx context.Context, fv *versioning.FileVersion, content []byte) (*Tree, error) {
	if cached := m.getCachedTree(fv.ID); cached != nil {
		return cached.Tree, nil
	}
	return m.parseAndCache(ctx, fv, content)
}

func (m *TreeSitterManager) getCachedTree(id versioning.VersionID) *CachedTree {
	m.treeCache.mu.RLock()
	defer m.treeCache.mu.RUnlock()
	return m.treeCache.entries[id]
}

func (m *TreeSitterManager) parseAndCache(ctx context.Context, fv *versioning.FileVersion, content []byte) (*Tree, error) {
	langName := detectLanguage(fv.FilePath)
	if langName == "" {
		return nil, ErrGrammarNotFound
	}

	tree, err := m.parseWithLanguage(ctx, langName, content)
	if err != nil {
		return nil, err
	}

	m.cacheTree(fv.ID, tree, langName)
	return tree, nil
}

func (m *TreeSitterManager) parseWithLanguage(ctx context.Context, langName string, content []byte) (*Tree, error) {
	lang, err := m.loader.LoadContext(ctx, langName)
	if err != nil {
		return nil, err
	}

	parser := NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(lang); err != nil {
		return nil, err
	}

	return parser.Parse(content, nil)
}

func (m *TreeSitterManager) cacheTree(id versioning.VersionID, tree *Tree, langName string) {
	m.treeCache.mu.Lock()
	defer m.treeCache.mu.Unlock()

	m.evictOldestIfNeeded()
	m.treeCache.entries[id] = &CachedTree{
		Tree:     tree,
		Language: langName,
		ParsedAt: time.Now(),
	}
}

func (m *TreeSitterManager) evictOldestIfNeeded() {
	if len(m.treeCache.entries) < m.treeCache.maxSize {
		return
	}

	oldest := m.findOldestEntry()
	if oldest != nil {
		delete(m.treeCache.entries, *oldest)
	}
}

func (m *TreeSitterManager) findOldestEntry() *versioning.VersionID {
	var oldestID *versioning.VersionID
	var oldestTime time.Time

	for id, cached := range m.treeCache.entries {
		if oldestID == nil || cached.ParsedAt.Before(oldestTime) {
			idCopy := id
			oldestID = &idCopy
			oldestTime = cached.ParsedAt
		}
	}
	return oldestID
}

func (m *TreeSitterManager) ParseIncremental(
	ctx context.Context,
	oldVersion *versioning.FileVersion,
	newContent []byte,
) (*Tree, error) {
	cached := m.getCachedTree(oldVersion.ID)
	if cached == nil {
		return m.parseWithLanguage(ctx, detectLanguage(oldVersion.FilePath), newContent)
	}

	oldTree := cached.Tree.Copy()
	return m.reparseWithOld(ctx, cached.Language, newContent, oldTree)
}

func (m *TreeSitterManager) reparseWithOld(ctx context.Context, langName string, content []byte, oldTree *Tree) (*Tree, error) {
	lang, err := m.loader.LoadContext(ctx, langName)
	if err != nil {
		return nil, err
	}

	parser := NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(lang); err != nil {
		return nil, err
	}

	return parser.Parse(content, oldTree)
}

func (m *TreeSitterManager) ComputeNodePath(tree *Tree, offset uint) []string {
	if tree == nil {
		return nil
	}
	return computePathFromRoot(tree.RootNode(), offset)
}

func computePathFromRoot(root *Node, offset uint) []string {
	path := make([]string, 0, 8)
	current := root

	for current != nil && !current.IsNull() {
		path = append(path, current.Type())
		path = appendNodeName(path, current)
		current = findChildContaining(current, offset)
	}

	return path
}

func appendNodeName(path []string, node *Node) []string {
	name := extractNodeIdentifier(node)
	if name != "" {
		return append(path, name)
	}
	return path
}

func extractNodeIdentifier(node *Node) string {
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		return nameNode.Content()
	}
	if idNode := node.ChildByFieldName("identifier"); idNode != nil {
		return idNode.Content()
	}
	return ""
}

func findChildContaining(parent *Node, offset uint) *Node {
	count := parent.NamedChildCount()
	for i := range count {
		child := parent.NamedChild(uint(i))
		if childContainsOffset(child, offset) {
			return child
		}
	}
	return nil
}

func childContainsOffset(child *Node, offset uint) bool {
	return child != nil && child.StartByte() <= offset && offset < child.EndByte()
}

func (m *TreeSitterManager) ResolveNodePath(tree *Tree, path []string) (*Node, error) {
	if tree == nil || len(path) == 0 {
		return nil, ErrInvalidPath
	}
	return resolveFromRoot(tree.RootNode(), path)
}

func resolveFromRoot(root *Node, path []string) (*Node, error) {
	current := root
	i := 0

	for i < len(path) {
		current, i = resolvePathStep(current, path, i)
		if current == nil {
			return nil, ErrPathNotFound
		}
	}

	return current, nil
}

func resolvePathStep(current *Node, path []string, i int) (*Node, int) {
	if current == nil || current.IsNull() {
		return nil, i
	}

	nodeType := path[i]
	i++

	name, i := extractNameFromPath(path, i)
	return findMatchingChild(current, nodeType, name), i
}

func extractNameFromPath(path []string, i int) (string, int) {
	if i < len(path) && !isNodeType(path[i]) {
		return path[i], i + 1
	}
	return "", i
}

func findMatchingChild(parent *Node, nodeType, name string) *Node {
	count := parent.NamedChildCount()
	for i := range count {
		child := parent.NamedChild(uint(i))
		if matchesTypeAndName(child, nodeType, name) {
			return child
		}
	}
	return nil
}

func matchesTypeAndName(node *Node, nodeType, name string) bool {
	if node.Type() != nodeType {
		return false
	}
	if name == "" {
		return true
	}
	return extractNodeIdentifier(node) == name
}

func isNodeType(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !isNodeTypeChar(c) {
			return false
		}
	}
	return true
}

func isNodeTypeChar(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z')
}

func (m *TreeSitterManager) Query(ctx context.Context, tree *Tree, langName, pattern string) ([]QueryMatch, error) {
	query, err := m.getOrCreateQuery(ctx, langName, pattern)
	if err != nil {
		return nil, err
	}

	cursor := NewQueryCursor()
	defer cursor.Close()

	return cursor.Matches(query, tree.RootNode(), tree.Source()), nil
}

func (m *TreeSitterManager) getOrCreateQuery(ctx context.Context, langName, pattern string) (*Query, error) {
	key := queryCacheKey{language: langName, pattern: pattern}

	m.queryCache.mu.RLock()
	if q, ok := m.queryCache.entries[key]; ok {
		m.queryCache.mu.RUnlock()
		return q, nil
	}
	m.queryCache.mu.RUnlock()

	return m.createAndCacheQuery(ctx, langName, pattern, key)
}

func (m *TreeSitterManager) createAndCacheQuery(ctx context.Context, langName, pattern string, key queryCacheKey) (*Query, error) {
	m.queryCache.mu.Lock()
	defer m.queryCache.mu.Unlock()

	if q, ok := m.queryCache.entries[key]; ok {
		return q, nil
	}

	lang, err := m.loader.LoadContext(ctx, langName)
	if err != nil {
		return nil, err
	}

	query, err := NewQuery(lang, pattern)
	if err != nil {
		return nil, err
	}

	m.evictQueryIfNeeded()
	m.queryCache.entries[key] = query
	return query, nil
}

func (m *TreeSitterManager) evictQueryIfNeeded() {
	if len(m.queryCache.entries) < m.queryCache.maxSize {
		return
	}
	for k, q := range m.queryCache.entries {
		q.Close()
		delete(m.queryCache.entries, k)
		break
	}
}

func (m *TreeSitterManager) CreateTargetFromNode(node *Node, tree *Tree, langName string) versioning.Target {
	path := m.ComputeNodePath(tree, node.StartByte())
	return versioning.NewASTTarget(path, node.Type(), generateNodeID(node), langName)
}

func generateNodeID(node *Node) string {
	start := node.StartByte()
	end := node.EndByte()
	return uintToStr(start) + ":" + uintToStr(end)
}

func uintToStr(n uint) string {
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

func (m *TreeSitterManager) InvalidateCache(id versioning.VersionID) {
	m.treeCache.mu.Lock()
	defer m.treeCache.mu.Unlock()

	if cached, ok := m.treeCache.entries[id]; ok {
		cached.Tree.Close()
		delete(m.treeCache.entries, id)
	}
}

func (m *TreeSitterManager) Close() {
	m.treeCache.mu.Lock()
	for _, cached := range m.treeCache.entries {
		cached.Tree.Close()
	}
	m.treeCache.entries = make(map[versioning.VersionID]*CachedTree)
	m.treeCache.mu.Unlock()

	m.queryCache.mu.Lock()
	for _, q := range m.queryCache.entries {
		q.Close()
	}
	m.queryCache.entries = make(map[queryCacheKey]*Query)
	m.queryCache.mu.Unlock()
}

var extToLang = map[string]string{
	".go":         "go",
	".rs":         "rust",
	".py":         "python",
	".pyi":        "python",
	".js":         "javascript",
	".mjs":        "javascript",
	".cjs":        "javascript",
	".jsx":        "javascript",
	".ts":         "typescript",
	".mts":        "typescript",
	".tsx":        "tsx",
	".java":       "java",
	".c":          "c",
	".h":          "c",
	".cpp":        "cpp",
	".cc":         "cpp",
	".cxx":        "cpp",
	".hpp":        "cpp",
	".hxx":        "cpp",
	".rb":         "ruby",
	".rake":       "ruby",
	".swift":      "swift",
	".kt":         "kotlin",
	".kts":        "kotlin",
	".json":       "json",
	".yaml":       "yaml",
	".yml":        "yaml",
	".toml":       "toml",
	".html":       "html",
	".css":        "css",
	".bash":       "bash",
	".sh":         "bash",
	".md":         "markdown",
	".properties": "properties",
}

func detectLanguage(filePath string) string {
	ext := extractExtension(filePath)
	return extToLang[ext]
}

func extractExtension(path string) string {
	dotIdx := findLastDot(path)
	if dotIdx < 0 {
		return ""
	}
	return path[dotIdx:]
}

func findLastDot(path string) int {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return i
		}
		if isPathSeparator(path[i]) {
			return -1
		}
	}
	return -1
}

func isPathSeparator(c byte) bool {
	return c == '/' || c == '\\'
}

var (
	ErrInvalidPath  = &managerError{msg: "invalid path"}
	ErrPathNotFound = &managerError{msg: "path not found in tree"}
)

type managerError struct {
	msg string
}

func (e *managerError) Error() string {
	return e.msg
}

type ManagerOption func(*TreeSitterManager)

func WithTreeCacheSize(size int) ManagerOption {
	return func(m *TreeSitterManager) {
		m.treeCache.maxSize = size
	}
}

func WithQueryCacheSize(size int) ManagerOption {
	return func(m *TreeSitterManager) {
		m.queryCache.maxSize = size
	}
}
