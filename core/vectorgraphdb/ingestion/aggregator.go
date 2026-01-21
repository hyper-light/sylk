package ingestion

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Aggregator
// =============================================================================

func Aggregate(rootPath string, files []MappedFile, parsed []ParsedFile) *CodeGraph {
	a := newAggregator(rootPath, len(files), len(parsed))
	return a.aggregate(files, parsed)
}

type aggregator struct {
	graph       *CodeGraph
	fileIDGen   atomic.Uint32
	symbolIDGen atomic.Uint32
	mu          sync.Mutex
}

func newAggregator(rootPath string, fileCount, parsedCount int) *aggregator {
	symbolCapacity := parsedCount * 10
	importCapacity := parsedCount * 5
	containsCapacity := symbolCapacity

	return &aggregator{
		graph: &CodeGraph{
			Files:         make([]FileNode, 0, fileCount),
			Symbols:       make([]SymbolNode, 0, symbolCapacity),
			ImportEdges:   make([]Edge, 0, importCapacity),
			ContainsEdges: make([]Edge, 0, containsCapacity),
			PathIndex:     make(map[string]uint32, fileCount),
			SymbolIndex:   make(map[string][]uint32, symbolCapacity),
			RootPath:      rootPath,
			CreatedAt:     time.Now(),
		},
	}
}

func (a *aggregator) aggregate(files []MappedFile, parsed []ParsedFile) *CodeGraph {
	parsedByPath := buildParsedIndex(parsed)

	workers := runtime.NumCPU()
	partitions := partitionMappedFiles(files, workers)

	var wg sync.WaitGroup
	wg.Add(workers)

	results := make([]aggregateResult, workers)

	for i := range workers {
		go func(idx int, partition []MappedFile) {
			defer wg.Done()
			results[idx] = a.processPartition(partition, parsedByPath)
		}(i, partitions[i])
	}

	wg.Wait()

	a.mergeResults(results)
	a.resolveImports(parsed)

	return a.graph
}

type aggregateResult struct {
	files         []FileNode
	symbols       []SymbolNode
	containsEdges []Edge
	totalLines    int
	totalBytes    int64
}

func (a *aggregator) processPartition(files []MappedFile, parsedByPath map[string]*ParsedFile) aggregateResult {
	result := aggregateResult{
		files:         make([]FileNode, 0, len(files)),
		symbols:       make([]SymbolNode, 0, len(files)*10),
		containsEdges: make([]Edge, 0, len(files)*10),
	}

	for _, f := range files {
		fileID := a.fileIDGen.Add(1)

		fileNode := FileNode{
			ID:        fileID,
			Path:      f.Path,
			Lang:      f.Lang,
			ByteCount: f.Size,
		}

		if p, ok := parsedByPath[f.Path]; ok {
			fileNode.LineCount = p.Lines
			result.totalLines += p.Lines
			symbols, edges := a.processSymbols(fileID, p.Symbols)
			result.symbols = append(result.symbols, symbols...)
			result.containsEdges = append(result.containsEdges, edges...)
		} else {
			fileNode.LineCount = CountLines(f.Data)
			result.totalLines += fileNode.LineCount
		}

		result.files = append(result.files, fileNode)
		result.totalBytes += f.Size
	}

	return result
}

func (a *aggregator) processSymbols(fileID uint32, symbols []Symbol) ([]SymbolNode, []Edge) {
	nodes := make([]SymbolNode, 0, len(symbols))
	edges := make([]Edge, 0, len(symbols))

	for _, s := range symbols {
		symbolID := a.symbolIDGen.Add(1)

		nodes = append(nodes, SymbolNode{
			ID:        symbolID,
			FileID:    fileID,
			Name:      s.Name,
			Kind:      s.Kind,
			StartLine: s.StartLine,
			EndLine:   s.EndLine,
			Signature: s.Signature,
		})

		edges = append(edges, Edge{
			SourceID: fileID,
			TargetID: symbolID,
			Kind:     EdgeKindContains,
		})
	}

	return nodes, edges
}

func (a *aggregator) mergeResults(results []aggregateResult) {
	for _, r := range results {
		a.graph.Files = append(a.graph.Files, r.files...)
		a.graph.Symbols = append(a.graph.Symbols, r.symbols...)
		a.graph.ContainsEdges = append(a.graph.ContainsEdges, r.containsEdges...)
		a.graph.TotalLines += r.totalLines
		a.graph.TotalBytes += r.totalBytes
	}

	for _, f := range a.graph.Files {
		a.graph.PathIndex[f.Path] = f.ID
	}

	for _, s := range a.graph.Symbols {
		a.graph.SymbolIndex[s.Name] = append(a.graph.SymbolIndex[s.Name], s.ID)
	}
}

func (a *aggregator) resolveImports(parsed []ParsedFile) {
	resolver := newImportResolver(a.graph)

	workers := runtime.NumCPU()
	partitions := partitionParsedFiles(parsed, workers)

	var wg sync.WaitGroup
	wg.Add(workers)

	edgeResults := make([][]Edge, workers)

	for i := range workers {
		go func(idx int, partition []ParsedFile) {
			defer wg.Done()
			edgeResults[idx] = a.resolvePartition(partition, resolver)
		}(i, partitions[i])
	}

	wg.Wait()

	for _, edges := range edgeResults {
		a.graph.ImportEdges = append(a.graph.ImportEdges, edges...)
	}
}

func (a *aggregator) resolvePartition(parsed []ParsedFile, resolver *importResolver) []Edge {
	edges := make([]Edge, 0, len(parsed)*5)

	for _, p := range parsed {
		sourceID, ok := a.graph.PathIndex[p.Path]
		if !ok {
			continue
		}

		for _, imp := range p.Imports {
			a.mu.Lock()
			targetID, found := resolver.resolve(p.Path, p.Lang, imp.Path)
			a.mu.Unlock()

			if !found {
				continue
			}

			edges = append(edges, Edge{
				SourceID: sourceID,
				TargetID: targetID,
				Kind:     EdgeKindImports,
			})
		}
	}

	return edges
}

func buildParsedIndex(parsed []ParsedFile) map[string]*ParsedFile {
	index := make(map[string]*ParsedFile, len(parsed))
	for i := range parsed {
		index[parsed[i].Path] = &parsed[i]
	}
	return index
}

func partitionMappedFiles(files []MappedFile, n int) [][]MappedFile {
	if n <= 0 {
		n = 1
	}
	partitions := make([][]MappedFile, n)
	for i, f := range files {
		partitions[i%n] = append(partitions[i%n], f)
	}
	return partitions
}

func partitionParsedFiles(files []ParsedFile, n int) [][]ParsedFile {
	if n <= 0 {
		n = 1
	}
	partitions := make([][]ParsedFile, n)
	for i, f := range files {
		partitions[i%n] = append(partitions[i%n], f)
	}
	return partitions
}

// =============================================================================
// Import Resolver
// =============================================================================

type importResolver struct {
	pathIndex      map[string]uint32
	dirSuffixIndex map[string]uint32
	importCache    map[string]uint32
}

func newImportResolver(graph *CodeGraph) *importResolver {
	pathIndex := make(map[string]uint32, len(graph.Files)*3)
	dirSuffixIndex := make(map[string]uint32, len(graph.Files))

	for _, f := range graph.Files {
		normalized := normalizePath(f.Path)
		pathIndex[normalized] = f.ID

		base := filepath.Base(f.Path)
		if _, exists := pathIndex[base]; !exists {
			pathIndex[base] = f.ID
		}

		if f.Lang == "go" {
			dir := normalizePath(filepath.Dir(f.Path))
			parts := strings.Split(dir, "/")
			for i := range parts {
				suffix := strings.Join(parts[i:], "/")
				if _, exists := dirSuffixIndex[suffix]; !exists {
					dirSuffixIndex[suffix] = f.ID
				}
			}
		}
	}

	return &importResolver{
		pathIndex:      pathIndex,
		dirSuffixIndex: dirSuffixIndex,
		importCache:    make(map[string]uint32, 1024),
	}
}

func (r *importResolver) resolve(sourcePath, lang, importPath string) (uint32, bool) {
	if id, ok := r.importCache[importPath]; ok {
		return id, true
	}

	id, found := r.resolveForLanguage(sourcePath, lang, importPath)
	if found {
		r.importCache[importPath] = id
	}

	return id, found
}

func (r *importResolver) resolveForLanguage(sourcePath, lang, importPath string) (uint32, bool) {
	switch lang {
	case "go":
		return r.resolveGoImport(importPath)
	case "python":
		return r.resolvePythonImport(sourcePath, importPath)
	case "typescript", "javascript", "tsx":
		return r.resolveJSImport(sourcePath, importPath)
	default:
		return r.resolveGeneric(importPath)
	}
}

func (r *importResolver) resolveGoImport(importPath string) (uint32, bool) {
	normalized := normalizePath(importPath)
	if id, ok := r.dirSuffixIndex[normalized]; ok {
		return id, true
	}
	return 0, false
}

func (r *importResolver) resolvePythonImport(sourcePath, importPath string) (uint32, bool) {
	modulePath := strings.ReplaceAll(importPath, ".", "/")

	candidates := []string{
		modulePath + ".py",
		modulePath + "/__init__.py",
		filepath.Join(filepath.Dir(sourcePath), modulePath+".py"),
	}

	for _, candidate := range candidates {
		if id, ok := r.pathIndex[normalizePath(candidate)]; ok {
			return id, true
		}
	}

	return 0, false
}

func (r *importResolver) resolveJSImport(sourcePath, importPath string) (uint32, bool) {
	if !strings.HasPrefix(importPath, ".") {
		return 0, false
	}

	dir := filepath.Dir(sourcePath)
	resolved := filepath.Join(dir, importPath)
	return r.tryJSExtensions(resolved)
}

func (r *importResolver) tryJSExtensions(basePath string) (uint32, bool) {
	extensions := []string{"", ".ts", ".tsx", ".js", ".jsx", "/index.ts", "/index.tsx", "/index.js"}

	for _, ext := range extensions {
		candidate := basePath + ext
		if id, ok := r.pathIndex[normalizePath(candidate)]; ok {
			return id, true
		}
	}

	return 0, false
}

func (r *importResolver) resolveGeneric(importPath string) (uint32, bool) {
	if id, ok := r.pathIndex[normalizePath(importPath)]; ok {
		return id, true
	}

	base := filepath.Base(importPath)
	if id, ok := r.pathIndex[base]; ok {
		return id, true
	}

	return 0, false
}

func normalizePath(path string) string {
	return strings.ToLower(filepath.ToSlash(path))
}
