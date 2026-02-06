package lsp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Capacity limits
// ---------------------------------------------------------------------------

// maxHeaderLineBytes caps header line scanning to prevent unbounded reads.
// Derived from: "Content-Length: " (16) + max int64 digits (19) + CRLF (2) = 37,
// rounded up to 64 for safety.
const maxHeaderLineBytes = 64

// maxMessageBytes caps message body size.
// Derived from: largest realistic LSP responses (workspace/symbol) are
// typically under 4 MiB; 16 MiB provides generous headroom.
const maxMessageBytes = 16 * 1024 * 1024

// headerPrefix is the Content-Length header key as it appears on the wire.
const headerPrefix = "Content-Length: "

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

// Transport reads and writes Content-Length framed JSON-RPC messages over
// a pair of io streams. It is safe for concurrent writes but expects a
// single reader goroutine.
type Transport struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex // protects writer
}

// NewTransport creates a Transport over the given reader and writer.
// Typically r is the server's stdout and w is the server's stdin.
func NewTransport(r io.Reader, w io.Writer) *Transport {
	return &Transport{
		reader: bufio.NewReaderSize(r, maxMessageBytes),
		writer: w,
	}
}

// ReadMessage reads one Content-Length framed message from the stream.
// It blocks until a complete message is available or an error occurs.
func (t *Transport) ReadMessage() ([]byte, error) {
	contentLen, err := t.readContentLength()
	if err != nil {
		return nil, err
	}
	return t.readBody(contentLen)
}

// WriteMessage writes a Content-Length framed message to the stream.
// It is safe for concurrent callers.
func (t *Transport) WriteMessage(data []byte) error {
	header := headerPrefix + strconv.Itoa(len(data)) + "\r\n\r\n"

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, err := io.WriteString(t.writer, header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := t.writer.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

// readContentLength scans header lines until it finds Content-Length,
// then reads past the blank line terminating the header block.
func (t *Transport) readContentLength() (int, error) {
	contentLen := -1
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		// Blank line terminates the header block.
		if line == "" {
			break
		}

		if strings.HasPrefix(line, headerPrefix) {
			n, parseErr := strconv.Atoi(line[len(headerPrefix):])
			if parseErr != nil {
				return 0, fmt.Errorf("parse content length: %w", parseErr)
			}
			contentLen = n
		}
	}

	if contentLen < 0 {
		return 0, fmt.Errorf("missing Content-Length header")
	}
	if contentLen > maxMessageBytes {
		return 0, fmt.Errorf("message too large: %d bytes (max %d)", contentLen, maxMessageBytes)
	}
	return contentLen, nil
}

// readBody reads exactly n bytes from the reader.
func (t *Transport) readBody(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(t.reader, buf); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return buf, nil
}
