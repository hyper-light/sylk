package treesitter

import (
	"context"
	"sync"
	"time"
)

type ParserPool struct {
	parsers  map[string]*parserPoolEntry
	registry *GrammarRegistry
	maxIdle  int
	mu       sync.Mutex
}

type parserPoolEntry struct {
	idle     []*Parser
	active   int
	language *Language
}

type ParserPoolConfig struct {
	MaxIdleParsersPerLanguage int
}

func NewParserPool(registry *GrammarRegistry) *ParserPool {
	return NewParserPoolWithConfig(registry, ParserPoolConfig{
		MaxIdleParsersPerLanguage: 4,
	})
}

func NewParserPoolWithConfig(registry *GrammarRegistry, cfg ParserPoolConfig) *ParserPool {
	return &ParserPool{
		parsers:  make(map[string]*parserPoolEntry),
		registry: registry,
		maxIdle:  cfg.MaxIdleParsersPerLanguage,
	}
}

func (p *ParserPool) Get(languageName string) (*Parser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getLocked(languageName)
}

func (p *ParserPool) getLocked(languageName string) (*Parser, error) {
	entry := p.getOrCreateEntry(languageName)
	if parser := p.popIdle(entry); parser != nil {
		entry.active++
		return parser, nil
	}
	return p.createNewParser(languageName, entry)
}

func (p *ParserPool) getOrCreateEntry(languageName string) *parserPoolEntry {
	entry, ok := p.parsers[languageName]
	if !ok {
		entry = &parserPoolEntry{idle: make([]*Parser, 0, p.maxIdle)}
		p.parsers[languageName] = entry
	}
	return entry
}

func (p *ParserPool) popIdle(entry *parserPoolEntry) *Parser {
	if len(entry.idle) == 0 {
		return nil
	}
	parser := entry.idle[len(entry.idle)-1]
	entry.idle = entry.idle[:len(entry.idle)-1]
	return parser
}

func (p *ParserPool) createNewParser(languageName string, entry *parserPoolEntry) (*Parser, error) {
	lang, err := p.registry.LoadLanguage(languageName)
	if err != nil {
		return nil, err
	}

	parser := NewParser()
	if !parser.SetLanguage(lang) {
		parser.Close()
		return nil, ErrLanguageNotLoaded
	}

	entry.language = lang
	entry.active++
	return parser, nil
}

func (p *ParserPool) Put(parser *Parser) {
	if parser == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.putLocked(parser)
}

func (p *ParserPool) putLocked(parser *Parser) {
	lang := parser.Language()
	if lang == nil {
		parser.Close()
		return
	}

	entry := p.parsers[lang.Name()]
	if entry == nil {
		parser.Close()
		return
	}

	entry.active--
	if len(entry.idle) >= p.maxIdle {
		parser.Close()
		return
	}

	parser.Reset()
	entry.idle = append(entry.idle, parser)
}

func (p *ParserPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, entry := range p.parsers {
		for _, parser := range entry.idle {
			parser.Close()
		}
		entry.idle = nil
	}
	p.parsers = make(map[string]*parserPoolEntry)
	return nil
}

func (p *ParserPool) Stats() map[string]ParserPoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := make(map[string]ParserPoolStats)
	for name, entry := range p.parsers {
		stats[name] = ParserPoolStats{
			Idle:   len(entry.idle),
			Active: entry.active,
		}
	}
	return stats
}

type ParserPoolStats struct {
	Idle   int `json:"idle"`
	Active int `json:"active"`
}

type IncrementalParser struct {
	pool      *ParserPool
	trees     map[string]*cachedTree
	maxCached int
	mu        sync.RWMutex
}

type cachedTree struct {
	tree       *Tree
	language   string
	parsedAt   time.Time
	sourceHash uint64
}

type IncrementalParserConfig struct {
	MaxCachedTrees int
}

func NewIncrementalParser(pool *ParserPool) *IncrementalParser {
	return NewIncrementalParserWithConfig(pool, IncrementalParserConfig{
		MaxCachedTrees: 100,
	})
}

func NewIncrementalParserWithConfig(pool *ParserPool, cfg IncrementalParserConfig) *IncrementalParser {
	return &IncrementalParser{
		pool:      pool,
		trees:     make(map[string]*cachedTree),
		maxCached: cfg.MaxCachedTrees,
	}
}

func (ip *IncrementalParser) Parse(ctx context.Context, filePath string, content []byte) (*Tree, error) {
	langName, ok := DetectLanguageForFile(filePath)
	if !ok {
		return nil, ErrGrammarNotFound
	}

	ip.mu.RLock()
	cached := ip.trees[filePath]
	ip.mu.RUnlock()

	if cached != nil && cached.sourceHash == hashContent(content) {
		return cached.tree.Copy(), nil
	}

	return ip.parseWithCache(ctx, filePath, content, langName, cached)
}

func (ip *IncrementalParser) parseWithCache(
	ctx context.Context,
	filePath string,
	content []byte,
	langName string,
	cached *cachedTree,
) (*Tree, error) {
	parser, err := ip.pool.Get(langName)
	if err != nil {
		return nil, err
	}
	defer ip.pool.Put(parser)

	var oldTree *Tree
	if cached != nil {
		oldTree = cached.tree
	}

	tree, err := parser.Parse(content, oldTree)
	if err != nil {
		return nil, err
	}

	ip.cacheTree(filePath, tree, langName, content)
	return tree.Copy(), nil
}

func (ip *IncrementalParser) cacheTree(filePath string, tree *Tree, langName string, content []byte) {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	if len(ip.trees) >= ip.maxCached {
		ip.evictOldest()
	}

	ip.trees[filePath] = &cachedTree{
		tree:       tree,
		language:   langName,
		parsedAt:   time.Now(),
		sourceHash: hashContent(content),
	}
}

func (ip *IncrementalParser) evictOldest() {
	oldestPath := ip.findOldestEntry()
	if oldestPath == "" {
		return
	}
	ip.removeEntry(oldestPath)
}

func (ip *IncrementalParser) findOldestEntry() string {
	var oldestPath string
	var oldestTime time.Time

	for path, entry := range ip.trees {
		if oldestPath == "" || entry.parsedAt.Before(oldestTime) {
			oldestPath = path
			oldestTime = entry.parsedAt
		}
	}
	return oldestPath
}

func (ip *IncrementalParser) removeEntry(path string) {
	entry := ip.trees[path]
	if entry != nil && entry.tree != nil {
		entry.tree.Close()
	}
	delete(ip.trees, path)
}

func (ip *IncrementalParser) ParseIncremental(
	ctx context.Context,
	filePath string,
	newContent []byte,
	edit *InputEdit,
) (*Tree, error) {
	langName, ok := DetectLanguageForFile(filePath)
	if !ok {
		return nil, ErrGrammarNotFound
	}

	ip.mu.RLock()
	cached := ip.trees[filePath]
	ip.mu.RUnlock()

	if cached == nil {
		return ip.Parse(ctx, filePath, newContent)
	}

	return ip.doIncrementalParse(ctx, filePath, newContent, langName, cached, edit)
}

func (ip *IncrementalParser) doIncrementalParse(
	ctx context.Context,
	filePath string,
	newContent []byte,
	langName string,
	cached *cachedTree,
	edit *InputEdit,
) (*Tree, error) {
	parser, err := ip.pool.Get(langName)
	if err != nil {
		return nil, err
	}
	defer ip.pool.Put(parser)

	oldTree := cached.tree.Copy()
	oldTree.Edit(edit)

	tree, err := parser.Parse(newContent, oldTree)
	oldTree.Close()
	if err != nil {
		return nil, err
	}

	ip.cacheTree(filePath, tree, langName, newContent)
	return tree.Copy(), nil
}

func (ip *IncrementalParser) Invalidate(filePath string) {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	if entry := ip.trees[filePath]; entry != nil && entry.tree != nil {
		entry.tree.Close()
	}
	delete(ip.trees, filePath)
}

func (ip *IncrementalParser) InvalidateAll() {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	for _, entry := range ip.trees {
		if entry.tree != nil {
			entry.tree.Close()
		}
	}
	ip.trees = make(map[string]*cachedTree)
}

func (ip *IncrementalParser) Close() error {
	ip.InvalidateAll()
	return nil
}

func (ip *IncrementalParser) CachedCount() int {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return len(ip.trees)
}

func hashContent(content []byte) uint64 {
	var hash uint64 = 14695981039346656037
	for _, b := range content {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return hash
}

func CreateInputEdit(startByte, oldEndByte, newEndByte uint32, startRow, startCol, oldEndRow, oldEndCol, newEndRow, newEndCol uint32) *InputEdit {
	return &InputEdit{
		StartByte:   startByte,
		OldEndByte:  oldEndByte,
		NewEndByte:  newEndByte,
		StartPoint:  Point{Row: startRow, Column: startCol},
		OldEndPoint: Point{Row: oldEndRow, Column: oldEndCol},
		NewEndPoint: Point{Row: newEndRow, Column: newEndCol},
	}
}
