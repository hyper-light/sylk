// Package sylkdir provides session-aware storage with commit-to-global merge.
package sylkdir

import (
	"context"
	"log"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/search"
)

// BackgroundIndexer indexes documents into the global Bleve asynchronously
// after commit returns. This allows the boot pipeline to return control
// immediately while Bleve indexing proceeds in the background. Scorch makes
// each committed batch searchable as soon as it's introduced.
//
// On-demand promotion: PromoteDocs allows the query path to immediately
// index specific documents without waiting for the background worker.
// Deduplication is guaranteed by sync.Map LoadOrStore — each doc ID is
// indexed exactly once regardless of worker/PromoteDocs interleaving.
type BackgroundIndexer struct {
	store         *GlobalVersionBleveStore
	docs          map[string]*VersionDocument // immutable after creation
	docIDs        []string                    // ordered for deterministic batching
	claimed       sync.Map                    // docID → struct{}: tracks indexed docs
	supersededIDs []string                    // doc IDs to delete before indexing

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	ready  chan struct{} // closed when all indexing finishes

	onComplete func() error // called after all docs indexed (e.g. recordBleveIndexed)
	indexed    atomic.Int64
	total      int64
}

// NewBackgroundIndexer creates an indexer and starts a single tracked goroutine
// that deletes superseded docs, then indexes all docs in data-derived batches.
//
// Parameters:
//   - store: the global Bleve store to index into (must be open)
//   - docs: session documents to index (ownership transferred, not mutated)
//   - supersededIDs: doc IDs to delete from Bleve before indexing
//   - onComplete: callback invoked after all docs are indexed (may be nil)
func NewBackgroundIndexer(
	store *GlobalVersionBleveStore,
	docs []*VersionDocument,
	supersededIDs []string,
	onComplete func() error,
) *BackgroundIndexer {
	docMap := make(map[string]*VersionDocument, len(docs))
	docIDs := make([]string, 0, len(docs))
	for _, d := range docs {
		docMap[d.ID] = d
		docIDs = append(docIDs, d.ID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bi := &BackgroundIndexer{
		store:         store,
		docs:          docMap,
		docIDs:        docIDs,
		supersededIDs: supersededIDs,
		ctx:           ctx,
		cancel:        cancel,
		ready:         make(chan struct{}),
		onComplete:    onComplete,
		total:         int64(len(docs)),
	}

	bi.wg.Add(1)
	go bi.worker()

	return bi
}

// worker is the single background goroutine. It deletes superseded docs,
// then indexes all docs in ceil(total/NumCPU) batches. Each batch is
// immediately searchable after Scorch commits it.
func (bi *BackgroundIndexer) worker() {
	defer bi.wg.Done()
	defer close(bi.ready)

	if bi.store == nil {
		return
	}

	bi.deleteSuperseded()

	if len(bi.docIDs) == 0 {
		bi.invokeOnComplete()
		return
	}

	batchSize := max(1, (len(bi.docIDs)+runtime.NumCPU()-1)/runtime.NumCPU())
	for start := 0; start < len(bi.docIDs); start += batchSize {
		if bi.ctx.Err() != nil {
			return
		}
		end := min(start+batchSize, len(bi.docIDs))
		bi.indexBatch(bi.docIDs[start:end])
	}

	bi.invokeOnComplete()
}

// indexBatch claims and indexes a slice of doc IDs. Docs already claimed
// by PromoteDocs are skipped (deduplication via sync.Map LoadOrStore).
func (bi *BackgroundIndexer) indexBatch(ids []string) {
	toIndex := bi.claimDocs(bi.ctx, ids)
	if len(toIndex) == 0 {
		return
	}
	if err := bi.store.IndexBatch(bi.ctx, toIndex); err != nil {
		log.Printf("[background-indexer] batch error (graceful): %v", err)
		return
	}
	bi.indexed.Add(int64(len(toIndex)))
}

// claimDocs atomically claims doc IDs and converts them to search documents.
// Returns only docs that were successfully claimed (not previously indexed).
func (bi *BackgroundIndexer) claimDocs(ctx context.Context, ids []string) []*search.Document {
	var docs []*search.Document
	for _, id := range ids {
		if ctx.Err() != nil {
			return docs
		}
		if _, loaded := bi.claimed.LoadOrStore(id, struct{}{}); loaded {
			continue
		}
		vdoc, ok := bi.docs[id]
		if !ok {
			continue
		}
		docs = append(docs, ConvertToSearchDocument(vdoc))
	}
	return docs
}

// deleteSuperseded removes superseded doc IDs from the global Bleve.
func (bi *BackgroundIndexer) deleteSuperseded() {
	if len(bi.supersededIDs) == 0 {
		return
	}
	bleveStore := bi.store.Store()
	if bleveStore == nil {
		return
	}
	mgr := bleveStore.Manager()
	if mgr == nil {
		return
	}
	for _, id := range bi.supersededIDs {
		if bi.ctx.Err() != nil {
			return
		}
		_ = mgr.Delete(context.Background(), id)
	}
}

// invokeOnComplete calls the completion callback if set.
func (bi *BackgroundIndexer) invokeOnComplete() {
	if bi.onComplete == nil {
		return
	}
	if err := bi.onComplete(); err != nil {
		log.Printf("[background-indexer] onComplete error: %v", err)
	}
}

// PromoteDocs immediately indexes specific documents for on-demand query-time
// use. Documents already claimed by the background worker are skipped.
// This method is safe to call concurrently with the background worker.
func (bi *BackgroundIndexer) PromoteDocs(ctx context.Context, docIDs []string) error {
	if bi.store == nil {
		return nil
	}
	toIndex := bi.claimDocs(ctx, docIDs)
	if len(toIndex) == 0 {
		return nil
	}
	if err := bi.store.IndexBatch(ctx, toIndex); err != nil {
		return err
	}
	bi.indexed.Add(int64(len(toIndex)))
	return nil
}

// Ready returns a channel that is closed when all background indexing completes.
// Use this to check completion without blocking: select on Ready() alongside
// other channels, or read from it to block until done.
func (bi *BackgroundIndexer) Ready() <-chan struct{} {
	return bi.ready
}

// Wait blocks until the background worker goroutine completes.
func (bi *BackgroundIndexer) Wait() {
	bi.wg.Wait()
}

// Close cancels the background worker and waits for it to finish.
// Safe to call multiple times.
func (bi *BackgroundIndexer) Close() {
	bi.cancel()
	bi.wg.Wait()
}

// Progress returns the number of documents indexed so far and the total count.
func (bi *BackgroundIndexer) Progress() (indexed, total int64) {
	return bi.indexed.Load(), bi.total
}
