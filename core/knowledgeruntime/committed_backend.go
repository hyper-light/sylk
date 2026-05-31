package knowledgeruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	bleve "github.com/blevesearch/bleve/v2"
)

// embedderIdleTTL is how long the loaded embedder model is kept after
// the last use before being unloaded to recover memory. Anchored to
// the canonical activation Warm→Cool transition window
// (PolicyDefaults().IdleToCool = 5 minutes): the embedder is a
// session-level warm body, and the same idle horizon that signals
// "the user has stepped away" for agent containers applies here.
// Reload cost (~100–500 ms model load) is negligible compared to
// this window.
//
// Expressed locally to avoid an import cycle into core/container/activation;
// keep in sync with PolicyDefaults().IdleToCool if those defaults change.
const embedderIdleTTL = 5 * time.Minute

// ErrCommittedBackendUnavailable is returned when committed-global retrieval
// is requested before the committed backend has been initialized.
var ErrCommittedBackendUnavailable = errors.New("committed knowledge backend is unavailable")

// TextDocumentIngestRequest describes a deterministic text document mutation
// into the committed-global document and knowledge stores.
type TextDocumentIngestRequest struct {
	DocumentID string
	Path       string
	Content    string
	DocType    search.DocumentType
	Language   string
	Domain     sylkdir.Domain
	Metadata   map[string]string
}

// CommittedSearchHit is a committed global knowledge hit enriched with node and
// edge metadata from the knowledge graph.
type CommittedSearchHit struct {
	search.ScoredDocument
	PrimaryNodeID   uint32   `json:"primary_node_id,omitempty"`
	PrimaryNodeType string   `json:"primary_node_type,omitempty"`
	Domain          string   `json:"domain,omitempty"`
	CanonicalKeys   []string `json:"canonical_keys,omitempty"`
	Symbols         []string `json:"symbols,omitempty"`
	NodeKinds       []string `json:"node_kinds,omitempty"`
	RelatedPaths    []string `json:"related_paths,omitempty"`
	RelatedSymbols  []string `json:"related_symbols,omitempty"`
}

// CommittedSearchResult is the committed-global search response returned to
// agents that need document + knowledge-graph-backed retrieval.
type CommittedSearchResult struct {
	Query       string               `json:"query"`
	HeadVersion string               `json:"head_version"`
	TotalHits   int64                `json:"total_hits"`
	SearchTime  time.Duration        `json:"search_time"`
	Hits        []CommittedSearchHit `json:"hits"`
}

type committedNodeMeta struct {
	ID           uint32
	Path         string
	Name         string
	CanonicalKey string
	Domain       sylkdir.Domain
	NodeType     sylkdir.NodeType
}

type committedPathMeta struct {
	PrimaryNodeID   uint32
	PrimaryNodeType string
	Domain          string
	CanonicalKeys   []string
	Symbols         []string
	NodeKinds       []string
	RelatedPaths    []string
	RelatedSymbols  []string

	// NodeIDs is the set of every node whose Path field equals this
	// path. Persisted alongside the meta so incremental refresh can
	// re-aggregate this path without scanning the entire node store
	// — an O(|nodeIDs in path|) read instead of O(N total nodes).
	// Sorted ascending after finalize() for deterministic encoding.
	NodeIDs []uint32

	canonicalSet     map[string]struct{}
	symbolSet        map[string]struct{}
	nodeKindSet      map[string]struct{}
	relatedPathSet   map[string]struct{}
	relatedSymbolSet map[string]struct{}
	nodeIDSet        map[uint32]struct{}
}

func newCommittedPathMeta() *committedPathMeta {
	return &committedPathMeta{
		canonicalSet:     make(map[string]struct{}),
		symbolSet:        make(map[string]struct{}),
		nodeKindSet:      make(map[string]struct{}),
		relatedPathSet:   make(map[string]struct{}),
		relatedSymbolSet: make(map[string]struct{}),
		nodeIDSet:        make(map[uint32]struct{}),
	}
}

func (m *committedPathMeta) addNodeID(id uint32) {
	if id == 0 {
		return
	}
	if _, ok := m.nodeIDSet[id]; ok {
		return
	}
	m.nodeIDSet[id] = struct{}{}
	m.NodeIDs = append(m.NodeIDs, id)
}

func (m *committedPathMeta) addCanonicalKey(value string) {
	if value == "" {
		return
	}
	if _, ok := m.canonicalSet[value]; ok {
		return
	}
	m.canonicalSet[value] = struct{}{}
	m.CanonicalKeys = append(m.CanonicalKeys, value)
}

func (m *committedPathMeta) addSymbol(value string) {
	if value == "" {
		return
	}
	if _, ok := m.symbolSet[value]; ok {
		return
	}
	m.symbolSet[value] = struct{}{}
	m.Symbols = append(m.Symbols, value)
}

func (m *committedPathMeta) addNodeKind(value string) {
	if value == "" {
		return
	}
	if _, ok := m.nodeKindSet[value]; ok {
		return
	}
	m.nodeKindSet[value] = struct{}{}
	m.NodeKinds = append(m.NodeKinds, value)
}

func (m *committedPathMeta) addRelatedPath(value string) {
	if value == "" {
		return
	}
	if _, ok := m.relatedPathSet[value]; ok {
		return
	}
	m.relatedPathSet[value] = struct{}{}
	m.RelatedPaths = append(m.RelatedPaths, value)
}

func (m *committedPathMeta) addRelatedSymbol(value string) {
	if value == "" {
		return
	}
	if _, ok := m.relatedSymbolSet[value]; ok {
		return
	}
	m.relatedSymbolSet[value] = struct{}{}
	m.RelatedSymbols = append(m.RelatedSymbols, value)
}

func (m *committedPathMeta) finalize() {
	sort.Strings(m.CanonicalKeys)
	sort.Strings(m.Symbols)
	sort.Strings(m.NodeKinds)
	sort.Strings(m.RelatedPaths)
	sort.Strings(m.RelatedSymbols)
	sort.Slice(m.NodeIDs, func(i, j int) bool { return m.NodeIDs[i] < m.NodeIDs[j] })
}

// committedMetadataIndex is the per-version derived enrichment cache.
//
// After buildCommittedMetadataIndex completes, byPath and nodeByID
// are nil — the byPath data has been persisted into store and is
// queried via store.Lookup(path) (LRU + bbolt mmap). This bounds the
// resident heap to the LRU regardless of repository scale; at JDK
// scale the previous all-in-heap shape held ~250 MiB of derived
// path metadata.
//
// Lookup is the only post-build accessor; it transparently routes
// to the persistent store when present.
type committedMetadataIndex struct {
	store    *committedMetaStore
	byPath   map[string]*committedPathMeta // build-only scratch; nil after build
	nodeByID map[uint32]committedNodeMeta  // build-only scratch; nil after build
}

// Lookup returns the metadata for path, or nil if no entry. Read
// path: persistent store (LRU + bbolt) when populated; build-time
// in-heap byPath as a fallback for callers that query an index
// mid-build (e.g., tests).
func (m *committedMetadataIndex) Lookup(path string) *committedPathMeta {
	if m == nil {
		return nil
	}
	if m.store != nil {
		if meta, ok := m.store.Lookup(path); ok {
			return meta
		}
		return nil
	}
	if m.byPath != nil {
		return m.byPath[path]
	}
	return nil
}

type committedKnowledgeState struct {
	head          sylkdir.SemanticVersion
	bleveStore    *sylkdir.GlobalVersionBleveStore
	nodeStore     *sylkdir.GlobalVersionNodeStore
	edgeStore     *sylkdir.GlobalVersionEdgeStore
	docStore      *sylkdir.GlobalVersionDocStore
	metaStore     *committedMetaStore
	externalBleve bool
	index         *committedMetadataIndex
}

// namedCloser pairs a sub-store close function with a label used for
// per-closer diagnostic logging. The label surfaces in shutdown
// timing logs so operators can see which store dominates the
// committed-knowledge close latency.
type namedCloser struct {
	name  string
	close func() error
}

// closeWarnThreshold is the per-closer wall-time over which the close
// gets logged as slow. Sized at the default per-subsystem budget in
// the TUI shutdown path (200ms): if a sub-store regularly exceeds
// this, it's the bottleneck the caller's budget is fighting.
const closeWarnThreshold = 200 * time.Millisecond

// Close fans out the five owned sub-stores in parallel goroutines and
// joins their errors. Each sub-store touches its own files/mmaps, so
// they are mutually independent at close time. Sequential closure
// would have to fit the sum of per-store latencies into the caller's
// shutdown budget; parallel closure fits within max(per-store).
//
// Per-closer timing is logged so a slow sub-store is identifiable
// from shutdown logs alone — no need to thread instrumentation
// through every layer.
func (s *committedKnowledgeState) Close() error {
	closers := []namedCloser{}
	if s.nodeStore != nil {
		closers = append(closers, namedCloser{name: "node_store", close: s.nodeStore.Close})
	}
	if s.edgeStore != nil {
		closers = append(closers, namedCloser{name: "edge_store", close: s.edgeStore.Close})
	}
	if s.docStore != nil {
		closers = append(closers, namedCloser{name: "doc_store", close: s.docStore.Close})
	}
	if s.bleveStore != nil {
		closers = append(closers, namedCloser{name: "bleve_store", close: s.bleveStore.CloseAll})
	}
	if s.metaStore != nil {
		closers = append(closers, namedCloser{name: "meta_store", close: s.metaStore.Close})
	}
	return runClosersInParallel(closers)
}

// runClosersInParallel invokes every closer in its own goroutine and
// joins the resulting errors after all complete. Each closer's wall
// time is logged at debug level; closers exceeding closeWarnThreshold
// are logged at warn so a slow sub-store stands out without verbose
// instrumentation. Empty and single-closer cases short-circuit.
func runClosersInParallel(closers []namedCloser) error {
	switch len(closers) {
	case 0:
		return nil
	case 1:
		return runOneCloser(closers[0])
	}
	errCh := make(chan error, len(closers))
	var wg sync.WaitGroup
	wg.Add(len(closers))
	for _, c := range closers {
		go func(closer namedCloser) {
			defer wg.Done()
			errCh <- runOneCloser(closer)
		}(c)
	}
	wg.Wait()
	close(errCh)
	errs := make([]error, 0, len(closers))
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func runOneCloser(c namedCloser) error {
	start := time.Now()
	err := c.close()
	elapsed := time.Since(start)
	// Log every closer at info so shutdown timing is visible from the
	// default log level, not just under the warn-on-threshold gate.
	// At shutdown the volume is small (one entry per closer), and the
	// data is exactly what's needed to diagnose the bottleneck without
	// re-instrumenting.
	if elapsed >= closeWarnThreshold {
		slog.Warn("committed_state_close_slow",
			"closer", c.name,
			"elapsed", elapsed,
			"threshold", closeWarnThreshold,
			"err", err,
		)
	} else {
		slog.Info("committed_state_close",
			"closer", c.name,
			"elapsed", elapsed,
			"err", err,
		)
	}
	return err
}

// CommittedKnowledgeBackend provides committed-global search plus runtime fetch
// ingestion on top of the `.sylk` global stores.
type CommittedKnowledgeBackend struct {
	projectRoot string
	logger      *slog.Logger

	refreshMu sync.Mutex

	stateMu   sync.RWMutex
	state     *committedKnowledgeState
	retired   []*committedKnowledgeState
	closeOnce sync.Once

	embedderMu sync.Mutex
	embedder   embedder.Embedder
	// Idle-unload state for the loaded embedder. embedderRefs counts
	// in-flight borrows from ensureEmbedder so the timer never closes
	// a model still in use; lastUsed (UnixNano) records the most
	// recent borrow or release; idleTimer (guarded by embedderMu)
	// fires unloadEmbedderIfIdle after embedderIdleTTL of no activity.
	embedderRefs    atomic.Int32
	embedderLastUse atomic.Int64
	embedderIdle    *time.Timer
}

// NewCommittedKnowledgeBackend creates a committed-global backend rooted at
// the current project.
func NewCommittedKnowledgeBackend(projectRoot string, logger *slog.Logger) *CommittedKnowledgeBackend {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommittedKnowledgeBackend{
		projectRoot: projectRoot,
		logger:      logger,
	}
}

// SearchInContext implements query.BleveIndex so the same backend can power
// the shared coordinator's Bleve searcher while also serving richer agent APIs.
func (b *CommittedKnowledgeBackend) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	state := b.currentState()
	if state == nil || state.bleveStore == nil {
		return &bleve.SearchResult{}, nil
	}
	index := state.bleveStore.RawIndex()
	if index == nil {
		return &bleve.SearchResult{}, nil
	}
	return index.SearchInContext(ctx, req)
}

// RefreshFromDisk reloads committed-global state from the current HEAD.
func (b *CommittedKnowledgeBackend) RefreshFromDisk(ctx context.Context) error {
	return b.refresh(ctx, nil, false, false, false)
}

// RefreshExistingFromDisk reloads committed-global state only from already
// materialized read indices. It does not rebuild missing Bleve state; startup
// callers use this so missing/corrupt derived indices are recovered by the
// post-boot mutating sync pipeline instead of blocking the TUI.
func (b *CommittedKnowledgeBackend) RefreshExistingFromDisk(ctx context.Context) error {
	return b.refresh(ctx, nil, false, false, true)
}

// RefreshWithBleveStore reloads committed-global metadata while reusing an
// already-open Bleve store, such as the boot BackgroundIndexer's live HEAD.
func (b *CommittedKnowledgeBackend) RefreshWithBleveStore(ctx context.Context, store *sylkdir.GlobalVersionBleveStore) error {
	if store == nil {
		return fmt.Errorf("committed backend: bleve store is required")
	}
	return b.refresh(ctx, store, true, false, false)
}

// AdoptOwnedBleve refreshes state from disk and closes any previously-retired
// external Bleve state that was kept alive while background indexing finished.
func (b *CommittedKnowledgeBackend) AdoptOwnedBleve(ctx context.Context) error {
	return b.refresh(ctx, nil, false, true, false)
}

// Search executes a committed-global document search and enriches hits with
// knowledge-graph metadata.
func (b *CommittedKnowledgeBackend) Search(ctx context.Context, req *search.SearchRequest) (*CommittedSearchResult, error) {
	if req == nil {
		return nil, fmt.Errorf("committed search: request is required")
	}
	if err := req.ValidateAndNormalize(); err != nil {
		return nil, err
	}

	state := b.currentState()
	if state == nil || state.bleveStore == nil {
		return nil, ErrCommittedBackendUnavailable
	}

	result, err := state.bleveStore.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &CommittedSearchResult{
			Query:       req.Query,
			HeadVersion: state.head.String(),
		}, nil
	}

	hits := make([]CommittedSearchHit, 0, len(result.Documents))
	for _, doc := range result.Documents {
		hits = append(hits, enrichCommittedHit(doc, state.index.Lookup(doc.Path)))
	}

	return &CommittedSearchResult{
		Query:       result.Query,
		HeadVersion: state.head.String(),
		TotalHits:   result.TotalHits,
		SearchTime:  result.SearchTime,
		Hits:        hits,
	}, nil
}

// IngestFetchedDocument ingests approved fetched content into the committed
// global stores, then refreshes the live committed backend to the new HEAD.
func (b *CommittedKnowledgeBackend) IngestFetchedDocument(ctx context.Context, entry *fetch.QuarantineEntry, provenance *fetch.Provenance, extracted *fetch.ExtractResult) error {
	if entry == nil {
		return fmt.Errorf("committed ingest: quarantine entry is required")
	}
	if provenance == nil {
		return fmt.Errorf("committed ingest: provenance is required")
	}

	content, language := composeFetchedDocumentContent(entry, provenance, extracted)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("committed ingest: extracted content is empty")
	}

	return b.upsertJointDocument(ctx, &sylkdir.JointDocRequest{
		DocID:    stableFetchedDocumentID(provenance.FetchURL),
		Path:     provenance.FetchURL,
		Content:  []byte(content),
		DocType:  search.DocTypeWebFetch,
		Language: language,
		Domain:   sylkdir.DomainResearch,
	})
}

// UpsertTextDocument deterministically inserts or replaces a text-backed
// document in the committed-global stores, then refreshes the live backend.
func (b *CommittedKnowledgeBackend) UpsertTextDocument(ctx context.Context, req *TextDocumentIngestRequest) error {
	if req == nil {
		return fmt.Errorf("committed ingest: request is required")
	}
	if strings.TrimSpace(req.Path) == "" {
		return fmt.Errorf("committed ingest: path is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return fmt.Errorf("committed ingest: content is required")
	}
	docType := req.DocType
	if !docType.IsValid() {
		docType = search.DocTypeNote
	}
	domain := req.Domain
	if domain == sylkdir.DomainCode && docType != search.DocTypeSourceCode {
		domain = sylkdir.DomainDoc
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" {
		documentID = stableTextDocumentID(req.Path, req.Content)
	}
	return b.upsertJointDocument(ctx, &sylkdir.JointDocRequest{
		DocID:    documentID,
		Path:     strings.TrimSpace(req.Path),
		Content:  []byte(req.Content),
		DocType:  docType,
		Language: strings.TrimSpace(req.Language),
		Domain:   domain,
		Metadata: cloneCommittedDocumentMetadata(req.Metadata),
	})
}

func (b *CommittedKnowledgeBackend) upsertJointDocument(ctx context.Context, req *sylkdir.JointDocRequest) error {
	if req == nil {
		return fmt.Errorf("committed ingest: joint request is required")
	}
	canonicalKey := sylkdir.DocumentCanonicalKey(req.DocType, req.Path)

	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()

	sd := sylkdir.New(b.projectRoot)
	if err := sd.Init(); err != nil {
		return fmt.Errorf("committed ingest: init sylkdir: %w", err)
	}
	if err := sd.Lock(); err != nil {
		return fmt.Errorf("committed ingest: lock sylkdir: %w", err)
	}
	defer sd.Unlock()

	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		return fmt.Errorf("committed ingest: load global meta: %w", err)
	}

	canon := sylkdir.NewCanonicalKeyIndexFromSylkDir(sd)
	if err := canon.Init(); err != nil {
		return fmt.Errorf("committed ingest: init canonical index: %w", err)
	}

	cwal, err := sylkdir.OpenCommitWAL(sylkdir.CommitWALConfig{
		Dir:         sd.CommitWALPath(),
		SyncOnWrite: true,
	})
	if err != nil {
		return fmt.Errorf("committed ingest: open commit wal: %w", err)
	}
	defer cwal.Close()

	if _, err := sylkdir.RunRecovery(sylkdir.RecoveryConfig{
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canon,
		CommitWAL:      cwal,
	}); err != nil {
		return fmt.Errorf("committed ingest: run recovery: %w", err)
	}

	var (
		bleveStore          *sylkdir.GlobalVersionBleveStore
		refreshAttached     bool
		transferredAttached bool
	)
	if state := b.currentState(); state == nil || state.bleveStore == nil {
		bleveStore = sylkdir.NewGlobalVersionBleveStore(sd, gm.GetHead())
		if err := bleveStore.OpenHead(); err != nil {
			return fmt.Errorf("committed ingest: open global bleve: %w", err)
		}
		refreshAttached = true
		defer func() {
			if !transferredAttached {
				_ = bleveStore.CloseAll()
			}
		}()
	}

	session, err := createCommittedIngestSession(sd, gm)
	if err != nil {
		return err
	}
	defer session.Close()
	_ = session.CloseBleve()
	session.BleveStore = nil

	ingestEmbedder, releaseEmbedder, ingestErr := b.ensureEmbedder(ctx)
	if ingestErr != nil {
		b.logger.Warn("committed ingest: embedder unavailable, proceeding without vectors", "error", ingestErr)
		ingestEmbedder = nil
	} else {
		defer releaseEmbedder()
	}

	if _, err := canon.Lookup(canonicalKey); err == nil {
		if _, err := session.InsertDocument(ctx, req, ingestEmbedder); err != nil {
			return fmt.Errorf("committed ingest: replace document: %w", err)
		}
	} else if !errors.Is(err, sylkdir.ErrKeyNotFound) {
		return fmt.Errorf("committed ingest: lookup canonical key: %w", err)
	} else if _, err := session.InsertDocument(ctx, req, ingestEmbedder); err != nil {
		return fmt.Errorf("committed ingest: insert document: %w", err)
	}

	if _, err := sylkdir.CommitToGlobal(ctx, sylkdir.CommitConfig{
		Session:          session,
		SylkDir:          sd,
		GlobalMeta:       gm,
		CanonicalIndex:   canon,
		GlobalBleveStore: bleveStore,
		CommitWAL:        cwal,
	}); err != nil {
		return fmt.Errorf("committed ingest: commit to global: %w", err)
	}

	attachedBleve := bleveStore
	if !refreshAttached {
		attachedBleve = nil
	}
	if err := b.refreshLocked(ctx, attachedBleve, false, true, false); err != nil {
		return fmt.Errorf("committed ingest: refresh backend: %w", err)
	}
	transferredAttached = refreshAttached
	return nil
}

// Close releases all live and retired committed-global resources.
// Live state, every retired state, and the embedder are closed in
// parallel — they own disjoint resources, so serial closure would
// just stack their shutdown latencies inside the caller's bounded
// budget. The embedder idle-timer is stopped synchronously up front
// because it manipulates the same embedder slot the parallel closer
// will read; doing both under embedderMu would serialize back into
// the caller's hot path.
func (b *CommittedKnowledgeBackend) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		b.stateMu.Lock()
		state := b.state
		retired := b.retired
		b.state = nil
		b.retired = nil
		b.stateMu.Unlock()

		b.embedderMu.Lock()
		if b.embedderIdle != nil {
			b.embedderIdle.Stop()
			b.embedderIdle = nil
		}
		embedderCloser, _ := b.embedder.(io.Closer)
		b.embedder = nil
		b.embedderMu.Unlock()

		closers := make([]namedCloser, 0, 2+len(retired))
		if state != nil {
			closers = append(closers, namedCloser{name: "state", close: state.Close})
		}
		for i, stale := range retired {
			if stale == nil {
				continue
			}
			name := fmt.Sprintf("retired[%d]", i)
			closers = append(closers, namedCloser{name: name, close: stale.Close})
		}
		if embedderCloser != nil {
			closers = append(closers, namedCloser{name: "embedder", close: embedderCloser.Close})
		}
		closeErr = runClosersInParallel(closers)
	})
	return closeErr
}

func (b *CommittedKnowledgeBackend) currentState() *committedKnowledgeState {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.state
}

// ensureEmbedder lazily loads the embedder model on first use and
// returns it along with a release function the caller MUST invoke
// when done. The release function decrements the borrow count and
// updates the last-use timestamp; while any borrow is outstanding the
// idle-timer skips unloading. The returned release is idempotent.
//
// On error the returned embedder and release are nil — callers should
// treat the embedder as unavailable and proceed without vectors.
func (b *CommittedKnowledgeBackend) ensureEmbedder(ctx context.Context) (embedder.Embedder, func(), error) {
	b.embedderMu.Lock()
	defer b.embedderMu.Unlock()

	if b.embedder == nil {
		result, err := embedder.NewEmbedder(ctx, embedder.FactoryConfig{})
		if err != nil {
			return nil, nil, err
		}
		b.embedder = result.Embedder
	}

	b.embedderRefs.Add(1)
	b.embedderLastUse.Store(time.Now().UnixNano())
	if b.embedderIdle == nil {
		b.embedderIdle = time.AfterFunc(embedderIdleTTL, b.unloadEmbedderIfIdle)
	} else {
		b.embedderIdle.Reset(embedderIdleTTL)
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			b.embedderRefs.Add(-1)
			b.embedderLastUse.Store(time.Now().UnixNano())
		})
	}
	return b.embedder, release, nil
}

// unloadEmbedderIfIdle is the timer callback that frees the embedder
// model when no borrows are outstanding and the idle TTL has truly
// elapsed. Reschedules itself when a borrow is still active (so the
// timer doesn't tear down an in-flight ingestion) or when the
// timestamp moved more recently than expected (defensive against the
// AfterFunc-vs-ensureEmbedder race).
func (b *CommittedKnowledgeBackend) unloadEmbedderIfIdle() {
	b.embedderMu.Lock()
	defer b.embedderMu.Unlock()

	if b.embedderIdle == nil || b.embedder == nil {
		return
	}
	if b.embedderRefs.Load() > 0 {
		b.embedderIdle.Reset(embedderIdleTTL)
		return
	}

	last := b.embedderLastUse.Load()
	if last > 0 {
		elapsed := time.Since(time.Unix(0, last))
		if elapsed < embedderIdleTTL {
			b.embedderIdle.Reset(embedderIdleTTL - elapsed)
			return
		}
	}

	if closer, ok := b.embedder.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			b.logger.Warn("embedder unload failed", "error", err)
		}
	}
	b.embedder = nil
	b.embedderIdle = nil
	b.logger.Info("embedder unloaded after idle ttl", "ttl", embedderIdleTTL)
}

func (b *CommittedKnowledgeBackend) refresh(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool, closeRetired bool, existingOnly bool) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	return b.refreshLocked(ctx, attachedBleve, externalBleve, closeRetired, existingOnly)
}

func (b *CommittedKnowledgeBackend) refreshLocked(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool, closeRetired bool, existingOnly bool) error {
	nextState, err := b.buildState(ctx, attachedBleve, externalBleve, existingOnly)
	if err != nil {
		return err
	}

	b.stateMu.Lock()
	prev := b.state
	b.state = nextState
	if closeRetired {
		retired := b.retired
		b.retired = nil
		b.stateMu.Unlock()
		if err := closeCommittedStates(append(retired, prev)...); err != nil {
			return err
		}
		return nil
	}

	var toClose []*committedKnowledgeState
	if prev != nil {
		if prev.externalBleve && !externalBleve {
			b.retired = append(b.retired, prev)
		} else {
			toClose = append(toClose, prev)
		}
	}
	b.stateMu.Unlock()
	return closeCommittedStates(toClose...)
}

func (b *CommittedKnowledgeBackend) buildState(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool, existingOnly bool) (*committedKnowledgeState, error) {
	sd := sylkdir.New(b.projectRoot)
	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		return nil, fmt.Errorf("committed backend: load global meta: %w", err)
	}
	head := gm.GetHead()

	nodeStore, err := sylkdir.NewGlobalVersionNodeStore(sd, head)
	if err != nil {
		return nil, fmt.Errorf("committed backend: open node store: %w", err)
	}
	edgeStore, err := sylkdir.NewGlobalVersionEdgeStore(sd, head)
	if err != nil {
		_ = nodeStore.Close()
		return nil, fmt.Errorf("committed backend: open edge store: %w", err)
	}
	docStore, err := sylkdir.NewGlobalVersionDocStore(sd, head)
	if err != nil {
		_ = nodeStore.Close()
		_ = edgeStore.Close()
		return nil, fmt.Errorf("committed backend: open doc store: %w", err)
	}

	bleveStore := attachedBleve
	if bleveStore == nil {
		bleveStore = sylkdir.NewGlobalVersionBleveStore(sd, head)
		var err error
		if existingOnly {
			err = bleveStore.OpenExistingHead()
		} else {
			err = bleveStore.OpenHead()
		}
		if err != nil {
			_ = nodeStore.Close()
			_ = edgeStore.Close()
			_ = docStore.Close()
			return nil, fmt.Errorf("committed backend: open bleve store: %w", err)
		}
	}
	bleveStore.SetHead(head)

	// Open the persistent metadata store. Lives at
	// {globalDataPath}/committed_metadata.bolt and is rewritten in
	// full on each refresh — single file, mmap-backed, OS page-cached.
	metaStorePath := filepath.Join(sd.GlobalDataPath(), "committed_metadata.bolt")
	metaStore, err := newCommittedMetaStore(metaStorePath)
	if err != nil {
		_ = nodeStore.Close()
		_ = edgeStore.Close()
		_ = docStore.Close()
		if attachedBleve == nil {
			_ = bleveStore.CloseAll()
		}
		return nil, fmt.Errorf("committed backend: open meta store: %w", err)
	}

	index, err := buildCommittedMetadataIndex(ctx, head, nodeStore, edgeStore, metaStore)
	if err != nil {
		_ = nodeStore.Close()
		_ = edgeStore.Close()
		_ = docStore.Close()
		_ = metaStore.Close()
		if attachedBleve == nil {
			_ = bleveStore.CloseAll()
		}
		return nil, err
	}

	return &committedKnowledgeState{
		head:          head,
		bleveStore:    bleveStore,
		nodeStore:     nodeStore,
		edgeStore:     edgeStore,
		docStore:      docStore,
		metaStore:     metaStore,
		externalBleve: externalBleve,
		index:         index,
	}, nil
}

// incrementalMaxPathFraction is the upper bound on the affected-path
// set size (as a fraction of the total node count's distinct paths)
// at which we still consider incremental cheaper than full rebuild.
// Above this, a full streaming rebuild has lower constant overhead
// because the inner per-path re-aggregation each pays a fixed
// fan-out into nodeStore + edgeStore that adds up.
//
// Anchored to the typical commit shape: most commits touch < 5% of
// paths in the repo. 0.50 leaves headroom for refactor commits that
// touch many files; beyond that, full rebuild's sequential scan of
// the offset index + edges.bin is faster than O(|paths|) random
// per-path reads.
const incrementalMaxPathFraction = 0.50

// tryIncrementalRefresh attempts a Phase-5 incremental rebuild.
// Returns (true, nil) when the rebuild succeeded and the metaStore
// reflects the new HEAD; returns (false, nil) when the conditions
// for incremental aren't met (no prior version, prior version
// equals HEAD which is the Phase-4 case already, the prior version
// can't be loaded, or the affected-path set exceeds the heuristic).
// Returns (false, err) only on hard errors that should abort the
// refresh entirely — soft incremental failures fall back to full
// rebuild silently.
func tryIncrementalRefresh(
	ctx context.Context,
	head sylkdir.SemanticVersion,
	nodeStore *sylkdir.GlobalVersionNodeStore,
	edgeStore *sylkdir.GlobalVersionEdgeStore,
	metaStore *committedMetaStore,
) (bool, error) {
	prevVersionStr, ok := metaStore.BuiltVersion()
	if !ok || prevVersionStr == "" {
		return false, nil // first build — full path
	}
	if prevVersionStr == head.String() {
		// Caller already handled this via the Phase-4 fast path; not
		// reachable in normal flow but guard anyway.
		return true, nil
	}
	prevVersion, err := sylkdir.ParseSemanticVersion(prevVersionStr)
	if err != nil {
		return false, nil // can't reason about the prior version → full
	}

	// Diff the two versions' live-id sets.
	prevIDs, err := nodeStore.LiveNodeIDsAtVersion(prevVersion)
	if err != nil {
		// Soft failure — prior version's index may be missing on disk.
		return false, nil
	}
	currIDs, err := nodeStore.LiveNodeIDsAtVersion(head)
	if err != nil {
		return false, fmt.Errorf("incremental: live ids at head: %w", err)
	}
	addedNodeIDs, removedNodeIDs := diffSortedIDs(prevIDs, currIDs)

	// Build the affected-path set. Sources of affected paths:
	//   1. Each added node's current path.
	//   2. Each removed node's prior path (resolved via metaStore's
	//      reverse n bucket — the one snapshot that knows which path
	//      a now-tombstoned node previously contributed to).
	//   3. Source path of any edge created since the last build —
	//      Edge.CreatedAt > built_timestamp.
	//   4. Source paths of every incoming edge to any added/removed
	//      node — those source paths' meta references the changed
	//      node and must re-aggregate.
	affectedPaths := make(map[string]struct{})

	for _, id := range addedNodeIDs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		node, err := nodeStore.ReadFromVersion(head, id)
		if err != nil || node == nil {
			continue
		}
		if node.Path != "" {
			affectedPaths[node.Path] = struct{}{}
		}
	}
	for _, id := range removedNodeIDs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if path, ok := metaStore.LookupNodePath(id); ok {
			affectedPaths[path] = struct{}{}
		}
	}

	// Edge delta via CreatedAt > built_timestamp. Source paths picked
	// up here cover the case of new edges between existing nodes
	// (no node delta on either endpoint, but source-path's meta now
	// references a new target).
	if builtTS, ok := metaStore.BuiltTimestamp(); ok {
		err := edgeStore.IterateEdgesFromVersion(head, func(edge *sylkdir.Edge) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if int64(edge.CreatedAt) <= builtTS {
				return nil
			}
			source, err := nodeStore.ReadFromVersion(head, edge.SourceID)
			if err != nil || source == nil || source.Path == "" {
				return nil
			}
			affectedPaths[source.Path] = struct{}{}
			return nil
		})
		if err != nil {
			return false, fmt.Errorf("incremental: edge delta: %w", err)
		}
	} else {
		// No timestamp recorded → first-ever incremental candidate
		// from a pre-Phase-5 store. Bail to full rebuild rather than
		// risk missing edge-only changes.
		return false, nil
	}

	// Cross-reference affected nodes via incoming edges: each
	// added/removed node may be the target of edges whose source
	// path's meta references it. Walk incoming edges (current
	// version's tombstone applied) and add source paths.
	for _, id := range addedNodeIDs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		incoming, err := edgeStore.GetIncomingFromVersion(head, id)
		if err != nil {
			return false, fmt.Errorf("incremental: incoming edges of added node %d: %w", id, err)
		}
		for _, e := range incoming {
			source, err := nodeStore.ReadFromVersion(head, e.SourceID)
			if err != nil || source == nil || source.Path == "" {
				continue
			}
			affectedPaths[source.Path] = struct{}{}
		}
	}
	for _, id := range removedNodeIDs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		// For removed nodes, incoming edges in the prior version
		// (whose source still exists in HEAD) referenced this node.
		// Use the prior version since the node is dead in HEAD.
		incoming, err := edgeStore.GetIncomingFromVersion(prevVersion, id)
		if err != nil {
			// Prior-version reads can fail if the version was
			// snapshotted away; bail to full rebuild on hard error.
			return false, nil
		}
		for _, e := range incoming {
			// Resolve source path against HEAD — the source might
			// itself have been tombstoned, in which case its path
			// is likely already in affectedPaths from another arm.
			source, err := nodeStore.ReadFromVersion(head, e.SourceID)
			if err != nil || source == nil || source.Path == "" {
				if path, ok := metaStore.LookupNodePath(e.SourceID); ok {
					affectedPaths[path] = struct{}{}
				}
				continue
			}
			affectedPaths[source.Path] = struct{}{}
		}
	}

	// Heuristic: if affected paths cover too much of the graph, full
	// rebuild has lower wall-time. Estimate "total paths" as the
	// number of distinct paths the prior index covered, reading the
	// "p" bucket KeyN. This is O(1) — no scan.
	priorPaths := metaStore.PathCount()
	if priorPaths > 0 {
		ratio := float64(len(affectedPaths)) / float64(priorPaths)
		if ratio > incrementalMaxPathFraction {
			return false, nil
		}
	}

	// Re-aggregate each affected path against HEAD.
	deltas := make([]PathDelta, 0, len(affectedPaths))
	for path := range affectedPaths {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		meta, err := reaggregatePath(ctx, head, path, nodeStore, edgeStore)
		if err != nil {
			return false, fmt.Errorf("incremental: re-aggregate %q: %w", path, err)
		}
		deltas = append(deltas, PathDelta{Path: path, Meta: meta})
	}

	if err := metaStore.ApplyDelta(deltas, head.String()); err != nil {
		return false, fmt.Errorf("incremental: apply delta: %w", err)
	}
	return true, nil
}

// reaggregatePath rebuilds the committedPathMeta for a single path
// against the given HEAD version. Returns nil meta when the path
// has no live nodes in HEAD (caller deletes the entry).
//
// Implementation mirrors the full-build aggregation logic but
// scoped to one path: enumerate live nodes whose Path == path
// (via the same IterateNodes pass — yes, this scans all nodes;
// see note below), then for each such node walk its outgoing
// edges to derive related-path / related-symbol metadata.
//
// The single-path aggregation runs the IterateNodes scan once per
// path. With K affected paths this is K × N node reads. For K well
// below the heuristic threshold (≤ 0.5 × |paths|) this remains far
// cheaper than full rebuild's O(N + E) for typical commits where
// |delta paths| ≪ |paths|.
//
// A future optimization would be a path-keyed node index (paths →
// nodeIDs lookup directly from the node store). That requires
// invasive changes in sylkdir; for now the per-path scan dominates
// only above the heuristic threshold, at which point full rebuild
// already wins and we don't enter this function.
func reaggregatePath(
	ctx context.Context,
	head sylkdir.SemanticVersion,
	path string,
	nodeStore *sylkdir.GlobalVersionNodeStore,
	edgeStore *sylkdir.GlobalVersionEdgeStore,
) (*committedPathMeta, error) {
	pm := newCommittedPathMeta()
	nodeMetaByID := make(map[uint32]committedNodeMeta)

	// Pass 1: collect every live node whose Path == path. Same
	// streaming iteration as the full build, filtered.
	if err := nodeStore.IterateNodesFromVersion(head, func(node *sylkdir.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if node.Path != path {
			return nil
		}
		meta := committedNodeMeta{
			ID:           node.ID,
			Path:         node.Path,
			Name:         node.Name,
			CanonicalKey: node.CanonicalKey,
			Domain:       sylkdir.Domain(node.Domain),
			NodeType:     sylkdir.NodeType(node.NodeType),
		}
		nodeMetaByID[node.ID] = meta
		if pm.Domain == "" {
			pm.Domain = committedDomainName(meta.Domain)
		}
		pm.addNodeID(meta.ID)
		nodeKind := committedNodeTypeName(meta.NodeType)
		pm.addNodeKind(nodeKind)
		pm.addCanonicalKey(meta.CanonicalKey)
		switch meta.NodeType {
		case sylkdir.NodeTypeFile, sylkdir.NodeTypeDocument:
			if pm.PrimaryNodeID == 0 {
				pm.PrimaryNodeID = meta.ID
				pm.PrimaryNodeType = nodeKind
			}
		case sylkdir.NodeTypeFunction, sylkdir.NodeTypeMethod, sylkdir.NodeTypeType, sylkdir.NodeTypeInterface, sylkdir.NodeTypeConst, sylkdir.NodeTypeVar:
			pm.addSymbol(meta.Name)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if len(nodeMetaByID) == 0 {
		// Path has no live nodes in HEAD — caller deletes the entry.
		return nil, nil
	}

	// Pass 2: for each node at this path, walk its outgoing edges.
	// The aggregation logic is identical to the full build's
	// edge-pass — same pathMeta mutators, same target-path/name
	// derivation. Targets are resolved against HEAD via
	// ReadFromVersion since the streaming iterator only gave us
	// nodes whose Path == this path.
	for sourceID, source := range nodeMetaByID {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		outgoing, err := edgeStore.GetOutgoingFromVersion(head, sourceID)
		if err != nil {
			return nil, fmt.Errorf("get outgoing for %d: %w", sourceID, err)
		}
		for _, edge := range outgoing {
			target, err := nodeStore.ReadFromVersion(head, edge.TargetID)
			if err != nil || target == nil {
				continue
			}
			targetMeta := committedNodeMeta{
				ID:       target.ID,
				Path:     target.Path,
				Name:     target.Name,
				NodeType: sylkdir.NodeType(target.NodeType),
			}
			switch sylkdir.EdgeType(edge.Type) {
			case sylkdir.EdgeTypeContains:
				if targetMeta.Path == source.Path && isCommittedSymbolType(targetMeta.NodeType) {
					pm.addSymbol(targetMeta.Name)
				}
			case sylkdir.EdgeTypeImports:
				if targetMeta.Path != "" && targetMeta.Path != source.Path {
					pm.addRelatedPath(targetMeta.Path)
				}
			case sylkdir.EdgeTypeCalls, sylkdir.EdgeTypeReferences:
				if targetMeta.Path != "" && targetMeta.Path != source.Path {
					pm.addRelatedPath(targetMeta.Path)
				}
				if targetMeta.Name != "" {
					pm.addRelatedSymbol(targetMeta.Name)
				}
			}
		}
	}

	pm.finalize()
	return pm, nil
}

// diffSortedIDs returns (added, removed) where added = next \ prev
// and removed = prev \ next. Both inputs must be ascending. Linear
// merge-walk — O(|prev|+|next|).
func diffSortedIDs(prev, next []uint32) (added, removed []uint32) {
	i, j := 0, 0
	for i < len(prev) && j < len(next) {
		switch {
		case prev[i] == next[j]:
			i++
			j++
		case prev[i] < next[j]:
			removed = append(removed, prev[i])
			i++
		default:
			added = append(added, next[j])
			j++
		}
	}
	if i < len(prev) {
		removed = append(removed, prev[i:]...)
	}
	if j < len(next) {
		added = append(added, next[j:]...)
	}
	return added, removed
}

// buildCommittedMetadataIndex constructs the per-path metadata
// derivation. Three layered fast paths, all gated on metaStore:
//
//   - Phase 4 (fast path): metaStore.BuiltVersion == head → no work,
//     queries route through the existing on-disk index.
//
//   - Phase 5 (incremental): metaStore has a prior built_version V
//     and head ≠ V → compute the (added/removed) node delta + the
//     (since timestamp) edge delta, derive the affected-path set,
//     re-aggregate ONLY those paths, apply atomically. Cost is
//     O(|delta|), not O(|graph|).
//
//   - Full rebuild (cold path): no prior version, or incremental
//     was attempted and bailed (path-set too large, missing prior
//     index, etc.). Streams every live node + every live edge as
//     before.
//
// The full-rebuild stream remains the source of truth — the
// streaming N-pass + M-pass aggregation logic is identical to the
// pre-Phase-5 code. Incremental simply re-runs that aggregation
// scoped to one path at a time, against the same underlying stores.
func buildCommittedMetadataIndex(ctx context.Context, head sylkdir.SemanticVersion, nodeStore *sylkdir.GlobalVersionNodeStore, edgeStore *sylkdir.GlobalVersionEdgeStore, metaStore *committedMetaStore) (*committedMetadataIndex, error) {
	// Phase 4 fast path.
	if metaStore != nil {
		if persistedVersion, ok := metaStore.BuiltVersion(); ok && persistedVersion == head.String() {
			return &committedMetadataIndex{store: metaStore}, nil
		}
	}

	// Phase 5 incremental path.
	if metaStore != nil {
		if applied, err := tryIncrementalRefresh(ctx, head, nodeStore, edgeStore, metaStore); err != nil {
			return nil, err
		} else if applied {
			return &committedMetadataIndex{store: metaStore}, nil
		}
	}

	index := &committedMetadataIndex{
		store:    metaStore,
		byPath:   make(map[string]*committedPathMeta),
		nodeByID: make(map[uint32]committedNodeMeta),
	}

	if err := nodeStore.IterateNodesFromVersion(head, func(node *sylkdir.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		meta := committedNodeMeta{
			ID:           node.ID,
			Path:         node.Path,
			Name:         node.Name,
			CanonicalKey: node.CanonicalKey,
			Domain:       sylkdir.Domain(node.Domain),
			NodeType:     sylkdir.NodeType(node.NodeType),
		}
		index.nodeByID[node.ID] = meta
		if strings.TrimSpace(node.Path) == "" {
			return nil
		}
		pathMeta := index.byPath[node.Path]
		if pathMeta == nil {
			pathMeta = newCommittedPathMeta()
			index.byPath[node.Path] = pathMeta
		}
		if pathMeta.Domain == "" {
			pathMeta.Domain = committedDomainName(meta.Domain)
		}
		pathMeta.addNodeID(meta.ID)
		nodeKind := committedNodeTypeName(meta.NodeType)
		pathMeta.addNodeKind(nodeKind)
		pathMeta.addCanonicalKey(meta.CanonicalKey)
		switch meta.NodeType {
		case sylkdir.NodeTypeFile, sylkdir.NodeTypeDocument:
			if pathMeta.PrimaryNodeID == 0 {
				pathMeta.PrimaryNodeID = meta.ID
				pathMeta.PrimaryNodeType = nodeKind
			}
		case sylkdir.NodeTypeFunction, sylkdir.NodeTypeMethod, sylkdir.NodeTypeType, sylkdir.NodeTypeInterface, sylkdir.NodeTypeConst, sylkdir.NodeTypeVar:
			pathMeta.addSymbol(meta.Name)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("committed backend: stream nodes: %w", err)
	}

	if err := edgeStore.IterateEdgesFromVersion(head, func(edge *sylkdir.Edge) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, ok := index.nodeByID[edge.SourceID]
		if !ok || strings.TrimSpace(source.Path) == "" {
			return nil
		}
		target, targetOK := index.nodeByID[edge.TargetID]
		pathMeta := index.byPath[source.Path]
		if pathMeta == nil {
			return nil
		}
		switch sylkdir.EdgeType(edge.Type) {
		case sylkdir.EdgeTypeContains:
			if targetOK && target.Path == source.Path && isCommittedSymbolType(target.NodeType) {
				pathMeta.addSymbol(target.Name)
			}
		case sylkdir.EdgeTypeImports:
			if targetOK && target.Path != "" && target.Path != source.Path {
				pathMeta.addRelatedPath(target.Path)
			}
		case sylkdir.EdgeTypeCalls, sylkdir.EdgeTypeReferences:
			if targetOK {
				if target.Path != "" && target.Path != source.Path {
					pathMeta.addRelatedPath(target.Path)
				}
				if target.Name != "" {
					pathMeta.addRelatedSymbol(target.Name)
				}
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("committed backend: stream edges: %w", err)
	}

	for _, meta := range index.byPath {
		meta.finalize()
	}

	// Persist the derived path metadata to the store and drop the
	// in-heap scratch maps. After this point, queries route through
	// store.Lookup (LRU + bbolt mmap) and the heap holds only the
	// LRU-bounded hot working set. The HEAD version is stamped so
	// subsequent refreshes against the same HEAD short-circuit via
	// the fast path above.
	if metaStore != nil {
		if err := metaStore.PersistAll(index.byPath, head.String()); err != nil {
			return nil, fmt.Errorf("committed backend: persist meta: %w", err)
		}
		index.byPath = nil
	}
	// nodeByID is build-time only — drop regardless of metaStore.
	index.nodeByID = nil
	return index, nil
}

func enrichCommittedHit(doc search.ScoredDocument, meta *committedPathMeta) CommittedSearchHit {
	hit := CommittedSearchHit{ScoredDocument: doc}
	if meta == nil {
		return hit
	}
	hit.PrimaryNodeID = meta.PrimaryNodeID
	hit.PrimaryNodeType = meta.PrimaryNodeType
	hit.Domain = meta.Domain
	hit.CanonicalKeys = append([]string(nil), meta.CanonicalKeys...)
	hit.Symbols = append([]string(nil), meta.Symbols...)
	hit.NodeKinds = append([]string(nil), meta.NodeKinds...)
	hit.RelatedPaths = append([]string(nil), meta.RelatedPaths...)
	hit.RelatedSymbols = append([]string(nil), meta.RelatedSymbols...)
	return hit
}

func createCommittedIngestSession(sd *sylkdir.SylkDir, gm *sylkdir.GlobalMeta) (*sylkdir.Session, error) {
	startID, err := gm.AllocateNodeIDs(32)
	if err != nil {
		return nil, fmt.Errorf("committed ingest: allocate node ids: %w", err)
	}
	sessionID, err := gm.AllocateSessionID()
	if err != nil {
		return nil, fmt.Errorf("committed ingest: allocate session id: %w", err)
	}
	store := sylkdir.NewSessionStore(sd)
	session, err := store.Create(sessionID, &sylkdir.BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        startID,
	})
	if err != nil {
		return nil, fmt.Errorf("committed ingest: create session: %w", err)
	}
	return session, nil
}

func composeFetchedDocumentContent(entry *fetch.QuarantineEntry, provenance *fetch.Provenance, extracted *fetch.ExtractResult) (string, string) {
	if extracted == nil {
		return "", ""
	}
	var parts []string
	if title := strings.TrimSpace(extracted.Title); title != "" {
		parts = append(parts, title)
	}
	if url := strings.TrimSpace(provenance.FetchURL); url != "" {
		parts = append(parts, url)
	}
	if contentType := strings.TrimSpace(entry.ContentType); contentType != "" {
		parts = append(parts, "content-type: "+contentType)
	}
	if approvedBy := strings.TrimSpace(provenance.ApprovedBy); approvedBy != "" {
		parts = append(parts, "approved-by: "+approvedBy)
	}
	if findings := provenance.FindingCount; findings > 0 {
		parts = append(parts, fmt.Sprintf("guardian-findings: %d", findings))
	}
	if body := strings.TrimSpace(extracted.Text); body != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n"), strings.TrimSpace(extracted.Language)
}

func stableFetchedDocumentID(rawURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawURL)))
	return "fetch_" + hex.EncodeToString(sum[:8])
}

func stableTextDocumentID(path, content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(path) + "\n" + strings.TrimSpace(content)))
	return "doc_" + hex.EncodeToString(sum[:8])
}

func cloneCommittedDocumentMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

func committedDomainName(value sylkdir.Domain) string {
	switch value {
	case sylkdir.DomainCode:
		return "code"
	case sylkdir.DomainDoc:
		return "doc"
	case sylkdir.DomainResearch:
		return "research"
	default:
		return "unknown"
	}
}

func committedNodeTypeName(value sylkdir.NodeType) string {
	switch value {
	case sylkdir.NodeTypeFile:
		return "file"
	case sylkdir.NodeTypeFunction:
		return "function"
	case sylkdir.NodeTypeMethod:
		return "method"
	case sylkdir.NodeTypeType:
		return "type"
	case sylkdir.NodeTypeInterface:
		return "interface"
	case sylkdir.NodeTypeConst:
		return "const"
	case sylkdir.NodeTypeVar:
		return "var"
	case sylkdir.NodeTypeDocument:
		return "document"
	case sylkdir.NodeTypeChunk:
		return "chunk"
	default:
		return "unknown"
	}
}

func isCommittedSymbolType(value sylkdir.NodeType) bool {
	switch value {
	case sylkdir.NodeTypeFunction, sylkdir.NodeTypeMethod, sylkdir.NodeTypeType, sylkdir.NodeTypeInterface, sylkdir.NodeTypeConst, sylkdir.NodeTypeVar:
		return true
	default:
		return false
	}
}

func closeCommittedStates(states ...*committedKnowledgeState) error {
	var errs []error
	for _, state := range states {
		if state == nil {
			continue
		}
		if err := state.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
