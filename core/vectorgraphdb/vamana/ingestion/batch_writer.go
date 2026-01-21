package ingestion

import (
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type BatchVectorWriter struct {
	vectorStore *storage.VectorStore
	labelStore  *storage.LabelStore
	idMap       *storage.IDMap
	batchSize   int
	buffer      []bufferedVector
	mu          sync.Mutex
	written     atomic.Int64
	onProgress  func(written, total int64)
}

type bufferedVector struct {
	id       string
	vector   []float32
	domain   vectorgraphdb.Domain
	nodeType uint16
}

type BatchWriterConfig struct {
	VectorStore *storage.VectorStore
	LabelStore  *storage.LabelStore
	IDMap       *storage.IDMap
	BatchSize   int
	OnProgress  func(written, total int64)
}

func NewBatchVectorWriter(cfg BatchWriterConfig) *BatchVectorWriter {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	return &BatchVectorWriter{
		vectorStore: cfg.VectorStore,
		labelStore:  cfg.LabelStore,
		idMap:       cfg.IDMap,
		batchSize:   batchSize,
		buffer:      make([]bufferedVector, 0, batchSize),
		onProgress:  cfg.OnProgress,
	}
}

func (w *BatchVectorWriter) Add(id string, vector []float32, domain vectorgraphdb.Domain, nodeType uint16) error {
	w.mu.Lock()
	w.buffer = append(w.buffer, bufferedVector{
		id:       id,
		vector:   vector,
		domain:   domain,
		nodeType: nodeType,
	})
	shouldFlush := len(w.buffer) >= w.batchSize
	w.mu.Unlock()

	if shouldFlush {
		return w.Flush()
	}
	return nil
}

func (w *BatchVectorWriter) Flush() error {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return nil
	}
	batch := w.buffer
	w.buffer = make([]bufferedVector, 0, w.batchSize)
	w.mu.Unlock()

	for _, bv := range batch {
		internalID := w.idMap.Assign(bv.id)

		if _, err := w.vectorStore.Append(bv.vector); err != nil {
			return err
		}

		if err := w.labelStore.Set(internalID, uint8(bv.domain), bv.nodeType); err != nil {
			return err
		}

		w.written.Add(1)
	}

	if w.onProgress != nil {
		w.onProgress(w.written.Load(), -1)
	}

	return nil
}

func (w *BatchVectorWriter) Close() error {
	if err := w.Flush(); err != nil {
		return err
	}

	if err := w.vectorStore.Sync(); err != nil {
		return err
	}

	return w.labelStore.Sync()
}

func (w *BatchVectorWriter) Written() int64 {
	return w.written.Load()
}
