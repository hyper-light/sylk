package knowledge

import (
	"fmt"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Page break marker
// ---------------------------------------------------------------------------

// pageBreakMarker is inserted between extracted pages to preserve document
// structure. Derived from a standard form-feed representation.
const pageBreakMarker = "\n--- Page %d ---\n"

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// PDFError wraps errors from the PDF extraction process with context.
type PDFError struct {
	Path string
	Err  error
}

func (e *PDFError) Error() string {
	return fmt.Sprintf("pdf extraction failed for %q: %v", e.Path, e.Err)
}

func (e *PDFError) Unwrap() error {
	return e.Err
}

// ---------------------------------------------------------------------------
// PDFExtractor
// ---------------------------------------------------------------------------

// maxPDFFileSize limits the size of PDF files we attempt to read.
// Derived from: 100 MiB is a practical upper bound for document PDFs
// (not scanned image PDFs which would be larger).
const maxPDFFileSize = 100 * 1024 * 1024

// PDFExtractor provides text extraction from PDF files.
// It uses a lightweight text-layer extraction approach suitable for
// PDFs that contain embedded text. Scanned/image-only PDFs will
// return empty content.
//
// NOTE: This is an adapter interface. The actual extraction implementation
// requires a PDF library (e.g., pdfcpu). The Extract method provides the
// contract and file validation; the parsing is delegated to extractPages.
type PDFExtractor struct{}

// NewPDFExtractor creates a new PDFExtractor.
func NewPDFExtractor() *PDFExtractor {
	return &PDFExtractor{}
}

// Extract reads a PDF file and returns its text content with page break
// markers between pages. Returns a PDFError for any extraction failure.
func (pe *PDFExtractor) Extract(path string) (string, error) {
	if err := pe.validatePath(path); err != nil {
		return "", err
	}

	pages, err := pe.extractPages(path)
	if err != nil {
		return "", &PDFError{Path: path, Err: err}
	}

	return pe.assemblePages(pages), nil
}

// validatePath checks that the file exists, is not a directory, is a .pdf
// file, and does not exceed the size limit.
func (pe *PDFExtractor) validatePath(path string) error {
	if !strings.HasSuffix(strings.ToLower(path), ".pdf") {
		return &PDFError{Path: path, Err: fmt.Errorf("not a PDF file")}
	}

	info, err := os.Stat(path)
	if err != nil {
		return &PDFError{Path: path, Err: fmt.Errorf("file not accessible: %w", err)}
	}

	if info.IsDir() {
		return &PDFError{Path: path, Err: fmt.Errorf("path is a directory")}
	}

	if info.Size() > maxPDFFileSize {
		return &PDFError{
			Path: path,
			Err:  fmt.Errorf("file size %d exceeds limit %d", info.Size(), maxPDFFileSize),
		}
	}

	return nil
}

// extractPages reads the PDF and returns per-page text content.
// This is the integration point for a PDF library. Currently returns
// a stub result indicating that PDF text extraction requires the pdfcpu
// dependency to be wired in.
//
// When pdfcpu is integrated, this method should:
//  1. Open the PDF with pdfcpu/api.ReadContextFile
//  2. Iterate pages
//  3. Extract text content from each page's content stream
//  4. Return the text as a slice of strings (one per page)
func (pe *PDFExtractor) extractPages(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Validate PDF header magic bytes.
	// PDF files must start with "%PDF-".
	pdfMagic := []byte("%PDF-")
	if len(data) < len(pdfMagic) {
		return nil, fmt.Errorf("file too small to be a valid PDF")
	}

	headerMatch := true
	for i, b := range pdfMagic {
		if data[i] != b {
			headerMatch = false
			break
		}
	}
	if !headerMatch {
		return nil, fmt.Errorf("invalid PDF header: missing %%PDF- magic")
	}

	// Extract raw text content by scanning for text-showing operators.
	// This is a lightweight heuristic that extracts BT...ET text blocks.
	text := extractTextBlocks(data)
	if text == "" {
		return []string{"[No extractable text content found in PDF]"}, nil
	}

	// For the lightweight extractor, return all text as a single page.
	return []string{text}, nil
}

// assemblePages joins page texts with page break markers.
func (pe *PDFExtractor) assemblePages(pages []string) string {
	if len(pages) == 0 {
		return ""
	}

	var b strings.Builder
	// Pre-allocate: average page length * count + markers.
	avgLen := 0
	for _, p := range pages {
		avgLen += len(p)
	}
	b.Grow(avgLen + len(pages)*len(pageBreakMarker))

	for i, page := range pages {
		if i > 0 {
			b.WriteString(fmt.Sprintf(pageBreakMarker, i+1))
		}
		b.WriteString(strings.TrimSpace(page))
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Lightweight PDF text extraction
// ---------------------------------------------------------------------------

// extractTextBlocks scans PDF binary data for text-showing operators within
// BT...ET blocks. This handles the common case of PDFs with embedded text
// but does not handle all PDF encoding variants (CIDFont, ToUnicode maps,
// etc.). For full extraction, integrate pdfcpu.
func extractTextBlocks(data []byte) string {
	var b strings.Builder
	inTextBlock := false
	i := 0
	dataLen := len(data)

	for i < dataLen-1 {
		// Detect BT (Begin Text) operator.
		if !inTextBlock && data[i] == 'B' && data[i+1] == 'T' {
			inTextBlock = true
			i += 2
			continue
		}

		// Detect ET (End Text) operator.
		if inTextBlock && data[i] == 'E' && data[i+1] == 'T' {
			inTextBlock = false
			b.WriteByte('\n')
			i += 2
			continue
		}

		// Inside text block, extract parenthesized strings: (text)
		if inTextBlock && data[i] == '(' {
			text, advance := extractParenString(data, i)
			if advance > 0 {
				b.WriteString(text)
				i += advance
				continue
			}
		}

		i++
	}

	return strings.TrimSpace(b.String())
}

// extractParenString extracts a parenthesized string starting at position
// pos in data. Handles escaped parentheses with backslash. Returns the
// extracted text and the number of bytes consumed.
func extractParenString(data []byte, pos int) (string, int) {
	if pos >= len(data) || data[pos] != '(' {
		return "", 0
	}

	var b strings.Builder
	depth := 1
	i := pos + 1

	for i < len(data) && depth > 0 {
		ch := data[i]

		// Handle escape sequences.
		if ch == '\\' && i+1 < len(data) {
			next := data[i+1]
			escaped, ok := pdfEscapeTable[next]
			if ok {
				b.WriteByte(escaped)
				i += 2
				continue
			}
			// Octal escape or unknown: skip the backslash.
			i++
			continue
		}

		if ch == '(' {
			depth++
		}
		if ch == ')' {
			depth--
			if depth == 0 {
				i++
				break
			}
		}

		// Filter to printable ASCII and common whitespace.
		if ch >= 32 && ch < 127 {
			b.WriteByte(ch)
		} else if ch == '\n' || ch == '\r' || ch == '\t' {
			b.WriteByte(' ')
		}

		i++
	}

	return b.String(), i - pos
}

// pdfEscapeTable maps PDF escape characters to their byte values.
var pdfEscapeTable = map[byte]byte{
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'b':  '\b',
	'f':  '\f',
	'(':  '(',
	')':  ')',
	'\\': '\\',
}
