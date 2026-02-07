package filetree

import (
	"context"
	"runtime"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Constants (derived)
// ---------------------------------------------------------------------------

// searchOutputBuf is the output channel capacity. Prevents scanner stall
// when the UI is briefly slow to consume.
// Derived from: 4 batches of in-flight results provides adequate decoupling.
const searchOutputBuf = 4

// scanChanBuf is the buffer size for internal file and result channels.
// Derived from: 512 entries keeps the pipeline flowing without excess memory.
const scanChanBuf = 512

// scanBatchSize is the number of matched files accumulated before sending
// a batch to the output channel.
// Derived from: 256 files keeps each batch under ~50ms wall time.
const scanBatchSize = 256

// scanWorkerCount is the number of parallel scanner goroutines.
// Derived from: runtime CPU count, capped at 8 to avoid I/O contention.
var scanWorkerCount = min(runtime.NumCPU(), 8)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// searchBatchMsg delivers one batch of progressive search results to the
// Bubble Tea Update loop. Used as a tea.Msg.
type searchBatchMsg struct {
	version int
	items   []searchItem
	cached  []batchCacheEntry
	matches int
	maxLine int
	done    bool
}

// batchCacheEntry carries file content back to the model for insertion into
// the session file cache. The scanner goroutine cannot touch the model's
// fileCache directly.
type batchCacheEntry struct {
	path  string
	lines []string
	size  int64
}

// fileScanResult holds the output of scanning a single file.
type fileScanResult struct {
	path    string
	matches []searchMatch
	lines   []string
	size    int64
}

// ---------------------------------------------------------------------------
// SearchWorker
// ---------------------------------------------------------------------------

// SearchWorker manages async file content search with a parallel goroutine
// pool. Follows the git status watcher pattern: output channel with Bubble
// Tea subscription, context-based cancellation, WaitGroup goroutine tracking.
type SearchWorker struct {
	output    chan searchBatchMsg
	closeOnce sync.Once

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSearchWorker creates a search worker with a buffered output channel.
func NewSearchWorker() *SearchWorker {
	return &SearchWorker{
		output: make(chan searchBatchMsg, searchOutputBuf),
	}
}

// Output returns the read-only channel for consuming progressive results.
func (w *SearchWorker) Output() <-chan searchBatchMsg {
	return w.output
}

// Start cancels any in-flight scan and launches a new parallel scan pipeline.
// Old goroutines see the cancelled context and exit; their stale batches are
// discarded by version check in the handler.
func (w *SearchWorker) Start(files []string, query string, cfg matchConfig, version int) {
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.mu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.runScan(ctx, files, query, cfg, version)
	}()
}

// Stop cancels any in-flight scan, waits for all goroutines to finish,
// drains and closes the output channel to unblock any subscription.
func (w *SearchWorker) Stop() {
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.mu.Unlock()

	w.wg.Wait()
	w.drainAndClose()
}

// drainAndClose empties the output channel and closes it. Safe to call
// multiple times — the close is guarded by sync.Once.
func (w *SearchWorker) drainAndClose() {
	w.closeOnce.Do(func() {
		for {
			select {
			case <-w.output:
			default:
				close(w.output)
				return
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Pipeline
// ---------------------------------------------------------------------------

// runScan is the pipeline entry point, running in a tracked goroutine.
// It fans out file scanning to a worker pool and aggregates results.
// All inner goroutines (feeder, scanners, closer) are tracked via pipeWG
// and awaited before this function returns, ensuring no untracked goroutines.
func (w *SearchWorker) runScan(ctx context.Context, files []string, query string, cfg matchConfig, version int) {
	fileCh := make(chan string, scanChanBuf)
	resultCh := make(chan fileScanResult, scanChanBuf)

	var pipeWG sync.WaitGroup

	// Feeder: sends file paths to the scanner pool.
	pipeWG.Add(1)
	go func() {
		defer pipeWG.Done()
		defer close(fileCh)
		feedFiles(ctx, files, fileCh)
	}()

	// Scanner pool: reads and matches files in parallel.
	var scanWG sync.WaitGroup
	for range scanWorkerCount {
		scanWG.Add(1)
		pipeWG.Add(1)
		go func() {
			defer pipeWG.Done()
			defer scanWG.Done()
			scanLoop(ctx, fileCh, resultCh, query, cfg)
		}()
	}

	// Closer: waits for all scanners to finish, then closes resultCh.
	pipeWG.Add(1)
	go func() {
		defer pipeWG.Done()
		scanWG.Wait()
		close(resultCh)
	}()

	// Aggregator runs in this goroutine.
	w.aggregate(ctx, resultCh, version)

	// Wait for all pipeline goroutines before returning. Context is
	// already cancelled (or aggregate finished normally), so all
	// goroutines exit promptly via their ctx.Done() select branches.
	pipeWG.Wait()
}

// feedFiles sends each file path to fileCh, respecting context cancellation.
func feedFiles(ctx context.Context, files []string, fileCh chan<- string) {
	for _, f := range files {
		select {
		case fileCh <- f:
		case <-ctx.Done():
			return
		}
	}
}

// scanLoop reads file paths from fileCh, scans each for matches, and sends
// results to resultCh. Runs as one of N scanner goroutines.
func scanLoop(ctx context.Context, fileCh <-chan string, resultCh chan<- fileScanResult, query string, cfg matchConfig) {
	for path := range fileCh {
		if ctx.Err() != nil {
			return
		}
		result := scanOneFile(path, query, cfg)
		if result == nil {
			continue
		}
		select {
		case resultCh <- *result:
		case <-ctx.Done():
			return
		}
	}
}

// scanOneFile reads a file and returns its matches, or nil if the file is
// unreadable, binary, or has no matches. Package-level pure function — no
// model access, safe for concurrent use. Reuses readSearchableFile and
// matchLines from model.go (same package).
func scanOneFile(path, query string, cfg matchConfig) *fileScanResult {
	data := readSearchableFile(path)
	if data == nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	matches := matchLines(lines, query, cfg)
	if len(matches) == 0 {
		return nil
	}
	return &fileScanResult{
		path:    path,
		matches: matches,
		lines:   lines,
		size:    int64(len(data)),
	}
}

// ---------------------------------------------------------------------------
// Aggregator
// ---------------------------------------------------------------------------

// aggregate reads from resultCh, accumulates batches of scanBatchSize files,
// and sends each batch to the output channel. Sends a final Done batch when
// all results have been processed or maxTotalMatches is reached.
func (w *SearchWorker) aggregate(ctx context.Context, resultCh <-chan fileScanResult, version int) {
	var batch searchBatchMsg
	batch.version = version
	totalMatches := 0
	batchFiles := 0

	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if totalMatches >= maxTotalMatches {
			break
		}
		remaining := maxTotalMatches - totalMatches
		added := appendResultToBatch(&batch, result, remaining)
		totalMatches += added
		batchFiles++

		if batchFiles >= scanBatchSize {
			w.sendBatch(ctx, batch)
			batch = searchBatchMsg{version: version}
			batchFiles = 0
		}
	}

	batch.done = true
	w.sendBatch(ctx, batch)
}

// sendBatch sends a batch to the output channel, respecting cancellation.
func (w *SearchWorker) sendBatch(ctx context.Context, batch searchBatchMsg) {
	select {
	case w.output <- batch:
	case <-ctx.Done():
	}
}

// appendResultToBatch adds a file's results to the batch message and returns
// the number of match items added.
func appendResultToBatch(batch *searchBatchMsg, result fileScanResult, remaining int) int {
	if len(batch.items) > 0 {
		batch.items = append(batch.items, searchItem{kind: searchItemGap})
	}
	batch.items = append(batch.items, searchItem{kind: searchItemFile, path: result.path})

	added := 0
	for _, match := range result.matches {
		if added >= remaining {
			break
		}
		batch.items = append(batch.items, searchItem{
			kind: searchItemMatch,
			path: result.path,
			line: match.line,
			text: match.text,
		})
		batch.maxLine = max(batch.maxLine, match.line)
		batch.matches++
		added++
	}

	batch.cached = append(batch.cached, batchCacheEntry{
		path:  result.path,
		lines: result.lines,
		size:  result.size,
	})
	return added
}
