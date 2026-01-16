package archivalist

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Compression System
// =============================================================================

// CompressionType defines the compression algorithm
type CompressionType string

const (
	CompressionNone CompressionType = "none"
	CompressionGzip CompressionType = "gzip"
	CompressionZlib CompressionType = "zlib"
)

// CompressionLevel defines the compression level
type CompressionLevel int

const (
	CompressionBestSpeed       CompressionLevel = 1
	CompressionDefault         CompressionLevel = 6
	CompressionBestCompression CompressionLevel = 9
)

// Compressor handles data compression and decompression
type Compressor struct {
	mu sync.RWMutex

	// Configuration
	config CompressorConfig

	// Buffer pools for efficiency
	bufferPool sync.Pool

	// Statistics
	stats compressorStatsInternal
}

// CompressorConfig configures the compressor
type CompressorConfig struct {
	// Default compression type
	Type CompressionType `json:"type"`

	// Compression level
	Level CompressionLevel `json:"level"`

	// Minimum size to compress (smaller data not worth compressing)
	MinSizeToCompress int `json:"min_size_to_compress"`

	// Enable content-aware compression
	ContentAware bool `json:"content_aware"`
}

// DefaultCompressorConfig returns sensible defaults
func DefaultCompressorConfig() CompressorConfig {
	return CompressorConfig{
		Type:              CompressionGzip,
		Level:             CompressionDefault,
		MinSizeToCompress: 1024, // Don't compress small data
		ContentAware:      true,
	}
}

// compressorStatsInternal holds atomic counters
type compressorStatsInternal struct {
	totalCompressed    int64
	totalDecompressed  int64
	bytesIn            int64
	bytesOut           int64
	compressionRatio   float64
	compressionTimeNs  int64
	decompressionTimeNs int64
}

// CompressorStats contains compression statistics
type CompressorStats struct {
	TotalCompressed     int64   `json:"total_compressed"`
	TotalDecompressed   int64   `json:"total_decompressed"`
	BytesIn             int64   `json:"bytes_in"`
	BytesOut            int64   `json:"bytes_out"`
	CompressionRatio    float64 `json:"compression_ratio"`
	AvgCompressionMs    float64 `json:"avg_compression_ms"`
	AvgDecompressionMs  float64 `json:"avg_decompression_ms"`
}

// NewCompressor creates a new compressor
func NewCompressor(config CompressorConfig) *Compressor {
	if config.MinSizeToCompress == 0 {
		config = DefaultCompressorConfig()
	}

	return &Compressor{
		config: config,
		bufferPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}
}

// =============================================================================
// Compression
// =============================================================================

// CompressedData represents compressed data with metadata
type CompressedData struct {
	Type           CompressionType `json:"type"`
	OriginalSize   int             `json:"original_size"`
	CompressedSize int             `json:"compressed_size"`
	Data           []byte          `json:"data"`
}

// Compress compresses the given data
func (c *Compressor) Compress(data []byte) (*CompressedData, error) {
	// Skip compression for small data
	if len(data) < c.config.MinSizeToCompress {
		return &CompressedData{
			Type:           CompressionNone,
			OriginalSize:   len(data),
			CompressedSize: len(data),
			Data:           data,
		}, nil
	}

	var compressed []byte
	var err error

	switch c.config.Type {
	case CompressionGzip:
		compressed, err = c.compressGzip(data)
	case CompressionZlib:
		compressed, err = c.compressZlib(data)
	default:
		compressed = data
	}

	if err != nil {
		return nil, fmt.Errorf("compression failed: %w", err)
	}

	// If compressed size is larger, don't compress
	if len(compressed) >= len(data) {
		return &CompressedData{
			Type:           CompressionNone,
			OriginalSize:   len(data),
			CompressedSize: len(data),
			Data:           data,
		}, nil
	}

	atomic.AddInt64(&c.stats.totalCompressed, 1)
	atomic.AddInt64(&c.stats.bytesIn, int64(len(data)))
	atomic.AddInt64(&c.stats.bytesOut, int64(len(compressed)))

	return &CompressedData{
		Type:           c.config.Type,
		OriginalSize:   len(data),
		CompressedSize: len(compressed),
		Data:           compressed,
	}, nil
}

// Decompress decompresses the given compressed data
func (c *Compressor) Decompress(cd *CompressedData) ([]byte, error) {
	if cd.Type == CompressionNone {
		return cd.Data, nil
	}

	var decompressed []byte
	var err error

	switch cd.Type {
	case CompressionGzip:
		decompressed, err = c.decompressGzip(cd.Data)
	case CompressionZlib:
		decompressed, err = c.decompressZlib(cd.Data)
	default:
		return cd.Data, nil
	}

	if err != nil {
		return nil, fmt.Errorf("decompression failed: %w", err)
	}

	atomic.AddInt64(&c.stats.totalDecompressed, 1)

	return decompressed, nil
}

// compressGzip compresses data using gzip
func (c *Compressor) compressGzip(data []byte) ([]byte, error) {
	buf := c.bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer c.bufferPool.Put(buf)

	w, err := gzip.NewWriterLevel(buf, int(c.config.Level))
	if err != nil {
		return nil, err
	}

	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

// decompressGzip decompresses gzip data
func (c *Compressor) decompressGzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return io.ReadAll(r)
}

// compressZlib compresses data using zlib
func (c *Compressor) compressZlib(data []byte) ([]byte, error) {
	buf := c.bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer c.bufferPool.Put(buf)

	w, err := zlib.NewWriterLevel(buf, int(c.config.Level))
	if err != nil {
		return nil, err
	}

	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

// decompressZlib decompresses zlib data
func (c *Compressor) decompressZlib(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return io.ReadAll(r)
}

// =============================================================================
// Entry Compression
// =============================================================================

// CompressEntry compresses an entry's content
func (c *Compressor) CompressEntry(entry *Entry) (*CompressedEntry, error) {
	// Serialize entry for compression
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize entry: %w", err)
	}

	compressed, err := c.Compress(data)
	if err != nil {
		return nil, err
	}

	return &CompressedEntry{
		ID:            entry.ID,
		Category:      entry.Category,
		SessionID:     entry.SessionID,
		CreatedAt:     entry.CreatedAt,
		Compression:   compressed,
	}, nil
}

// DecompressEntry decompresses an entry
func (c *Compressor) DecompressEntry(ce *CompressedEntry) (*Entry, error) {
	data, err := c.Decompress(ce.Compression)
	if err != nil {
		return nil, err
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("failed to deserialize entry: %w", err)
	}

	return &entry, nil
}

// CompressedEntry represents a compressed entry
type CompressedEntry struct {
	ID          string          `json:"id"`
	Category    Category        `json:"category"`
	SessionID   string          `json:"session_id"`
	CreatedAt   time.Time       `json:"created_at"`
	Compression *CompressedData `json:"compression"`
}

// =============================================================================
// Batch Compression
// =============================================================================

// CompressEntries compresses multiple entries
func (c *Compressor) CompressEntries(entries []*Entry) ([]*CompressedEntry, error) {
	results := make([]*CompressedEntry, len(entries))
	var firstErr error
	var errOnce sync.Once

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4) // Limit concurrency

	for i, entry := range entries {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(idx int, e *Entry) {
			defer wg.Done()
			defer func() { <-semaphore }()

			compressed, err := c.CompressEntry(e)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			results[idx] = compressed
		}(i, entry)
	}

	wg.Wait()
	return results, firstErr
}

// DecompressEntries decompresses multiple entries
func (c *Compressor) DecompressEntries(entries []*CompressedEntry) ([]*Entry, error) {
	results := make([]*Entry, len(entries))
	var firstErr error
	var errOnce sync.Once

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4) // Limit concurrency

	for i, entry := range entries {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(idx int, e *CompressedEntry) {
			defer wg.Done()
			defer func() { <-semaphore }()

			decompressed, err := c.DecompressEntry(e)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			results[idx] = decompressed
		}(i, entry)
	}

	wg.Wait()
	return results, firstErr
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns compressor statistics
func (c *Compressor) Stats() CompressorStats {
	totalCompressed := atomic.LoadInt64(&c.stats.totalCompressed)
	bytesIn := atomic.LoadInt64(&c.stats.bytesIn)
	bytesOut := atomic.LoadInt64(&c.stats.bytesOut)

	var ratio float64
	if bytesIn > 0 {
		ratio = float64(bytesOut) / float64(bytesIn)
	}

	return CompressorStats{
		TotalCompressed:   totalCompressed,
		TotalDecompressed: atomic.LoadInt64(&c.stats.totalDecompressed),
		BytesIn:           bytesIn,
		BytesOut:          bytesOut,
		CompressionRatio:  ratio,
	}
}

