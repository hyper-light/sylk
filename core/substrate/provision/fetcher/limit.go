package fetcher

import (
	"context"
	"errors"
	"io"
)

// errLimitExceeded is the sentinel emitted when a bounded reader hits
// its byte cap. It is wrapped in ErrFetchTooLarge by the fetcher
// callers so consumers can errors.Is the public sentinel.
var errLimitExceeded = errors.New("fetcher: byte limit exceeded")

// wrapLimit returns r wrapped with a byte cap. Zero or negative
// maxBytes disables the cap (the underlying reader is returned
// unchanged).
func wrapLimit(r io.Reader, maxBytes int64) io.Reader {
	if maxBytes <= 0 {
		return r
	}
	return &limitedReader{src: r, remaining: maxBytes}
}

type limitedReader struct {
	src       io.Reader
	remaining int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, errLimitExceeded
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.src.Read(p)
	l.remaining -= int64(n)
	return n, err
}

// readWithProgress drains r until EOF or error, returning the full byte
// slice. If progress is non-nil, it is called periodically with the
// running byte count. ctx cancellation aborts the read with ctx.Err.
func readWithProgress(ctx context.Context, r io.Reader, progress ProgressFunc) ([]byte, error) {
	out := make([]byte, 0, defaultReadInitialCapacity)
	buf := make([]byte, defaultReadChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			notifyProgress(progress, int64(len(out)))
		}
		if isEOF(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func notifyProgress(progress ProgressFunc, total int64) {
	if progress == nil {
		return
	}
	progress(total)
}

func isEOF(err error) bool {
	return errors.Is(err, io.EOF)
}

// Sized for typical substrate fetches: most package archives are
// 100KB-50MB, with the tail extending to ~200MB for big runtimes. A
// 64KB chunk is the canonical read size for io.Copy-style loops, large
// enough to amortize per-call overhead and small enough to keep memory
// growth predictable. Initial capacity at 256KB pre-allocates for the
// median small archive without overcommitting for the rare empty fetch.
const (
	defaultReadChunkSize       = 64 * 1024
	defaultReadInitialCapacity = 256 * 1024
)
