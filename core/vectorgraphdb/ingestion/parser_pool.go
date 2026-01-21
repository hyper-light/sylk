package ingestion

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/treesitter"
)

type ParserPool struct {
	tool       *treesitter.TreeSitterTool
	downloader *treesitter.GrammarDownloader
	workers    int
}

func NewParserPool(workers int) *ParserPool {
	downloader, err := treesitter.NewGrammarDownloader(
		treesitter.WithPermissionCallback(autoGrantPermission),
	)

	var opts []treesitter.LoaderOption
	if err == nil && downloader != nil {
		opts = append(opts,
			treesitter.WithAutoDownload(true),
			treesitter.WithDownloader(downloader),
		)
	}

	return &ParserPool{
		tool:       treesitter.NewTreeSitterTool(opts...),
		downloader: downloader,
		workers:    workers,
	}
}

func NewParserPoolWithoutDownload(workers int) *ParserPool {
	return &ParserPool{
		tool:    treesitter.NewTreeSitterTool(),
		workers: workers,
	}
}

func autoGrantPermission(ctx context.Context, req treesitter.PermissionRequest) (treesitter.PermissionResponse, error) {
	return treesitter.PermissionResponse{
		Granted:    true,
		Scope:      treesitter.PermissionGrantedSession,
		Reason:     "auto-granted for batch ingestion",
		ApprovedBy: "ingestion-system",
	}, nil
}

func (p *ParserPool) Close() {
	p.tool.Close()
}

func (p *ParserPool) PreWarmGrammars(ctx context.Context, files []MappedFile) {
	langs := make(map[string]struct{})
	for _, f := range files {
		if f.Lang != "" {
			langs[f.Lang] = struct{}{}
		}
	}

	var wg sync.WaitGroup
	for lang := range langs {
		wg.Add(1)
		go func(l string) {
			defer wg.Done()
			p.tool.ParseFast(ctx, "dummy."+extForLang(l), []byte{})
		}(lang)
	}
	wg.Wait()
}

func extForLang(lang string) string {
	for ext, l := range SupportedLanguages {
		if l == lang {
			return ext[1:]
		}
	}
	return lang
}

func (p *ParserPool) ParseAll(ctx context.Context, files []MappedFile) ([]ParsedFile, []FileParseError) {
	results := NewAccumulator[ParsedFile](len(files))
	errors := NewAccumulator[FileParseError](0)

	partitions := PartitionFiles(files, p.workers)

	var wg sync.WaitGroup
	wg.Add(p.workers)
	for i := range p.workers {
		go p.parseWorker(ctx, partitions[i], results, errors, &wg)
	}
	wg.Wait()

	return results.Items(), errors.Items()
}

func groupByLanguage(files []MappedFile) map[string][]MappedFile {
	byLang := make(map[string][]MappedFile)
	for _, f := range files {
		if f.Lang != "" {
			byLang[f.Lang] = append(byLang[f.Lang], f)
		}
	}
	return byLang
}

func (p *ParserPool) parseWorker(
	ctx context.Context,
	files []MappedFile,
	results *Accumulator[ParsedFile],
	errors *Accumulator[FileParseError],
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	if len(files) == 0 {
		return
	}

	parser := treesitter.NewParser()
	defer parser.Close()

	var currentLang string

	for _, f := range files {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result, err := p.tool.ParseWithParser(ctx, parser, f.Path, f.Data, &currentLang)
		if err != nil {
			errors.Append(FileParseError{Path: f.Path, Error: err.Error()})
			continue
		}
		results.Append(convertParseResult(f, result))
	}
}

func convertParseResult(f MappedFile, result *treesitter.ParseResult) ParsedFile {
	parsed := ParsedFile{
		Path:  f.Path,
		Lang:  f.Lang,
		Lines: CountLines(f.Data),
	}

	parsed.Symbols = convertFunctions(result.Functions)
	parsed.Symbols = append(parsed.Symbols, convertTypes(result.Types)...)
	parsed.Imports = convertImports(result.Imports)
	parsed.Errors = convertErrors(result.Errors)

	return parsed
}

func convertFunctions(funcs []treesitter.FunctionInfo) []Symbol {
	symbols := make([]Symbol, 0, len(funcs))
	for _, f := range funcs {
		kind := SymbolKindFunction
		if f.IsMethod {
			kind = SymbolKindMethod
		}
		symbols = append(symbols, Symbol{
			Name:      f.Name,
			Kind:      kind,
			StartLine: f.StartLine,
			EndLine:   f.EndLine,
			Signature: buildSignature(f),
		})
	}
	return symbols
}

func buildSignature(f treesitter.FunctionInfo) string {
	if f.Receiver != "" {
		return f.Receiver + "." + f.Name + f.Parameters
	}
	return f.Name + f.Parameters
}

func convertTypes(types []treesitter.TypeInfo) []Symbol {
	symbols := make([]Symbol, 0, len(types))
	for _, t := range types {
		kind := kindFromTypeName(t.Kind)
		symbols = append(symbols, Symbol{
			Name:      t.Name,
			Kind:      kind,
			StartLine: t.StartLine,
			EndLine:   t.EndLine,
		})
	}
	return symbols
}

func kindFromTypeName(name string) SymbolKind {
	switch name {
	case "interface_type":
		return SymbolKindInterface
	default:
		return SymbolKindType
	}
}

func convertImports(imports []treesitter.ImportInfo) []Import {
	result := make([]Import, 0, len(imports))
	for _, i := range imports {
		result = append(result, Import{
			Path:  normalizeImportPath(i.Path),
			Alias: i.Alias,
			Line:  i.Line,
		})
	}
	return result
}

func normalizeImportPath(path string) string {
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		return path[1 : len(path)-1]
	}
	return path
}

func convertErrors(errors []treesitter.ParseError) []ParseError {
	result := make([]ParseError, 0, len(errors))
	for _, e := range errors {
		result = append(result, ParseError{
			Line:    e.Line,
			Column:  e.Column,
			Message: e.Message,
		})
	}
	return result
}
