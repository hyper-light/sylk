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

	canonicalSet     map[string]struct{}
	symbolSet        map[string]struct{}
	nodeKindSet      map[string]struct{}
	relatedPathSet   map[string]struct{}
	relatedSymbolSet map[string]struct{}
}

func newCommittedPathMeta() *committedPathMeta {
	return &committedPathMeta{
		canonicalSet:     make(map[string]struct{}),
		symbolSet:        make(map[string]struct{}),
		nodeKindSet:      make(map[string]struct{}),
		relatedPathSet:   make(map[string]struct{}),
		relatedSymbolSet: make(map[string]struct{}),
	}
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

func (s *committedKnowledgeState) Close() error {
	var errs []error
	if s.nodeStore != nil {
		if err := s.nodeStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.edgeStore != nil {
		if err := s.edgeStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.docStore != nil {
		if err := s.docStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.bleveStore != nil {
		if err := s.bleveStore.CloseAll(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.metaStore != nil {
		if err := s.metaStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	return b.refresh(ctx, nil, false, false)
}

// RefreshWithBleveStore reloads committed-global metadata while reusing an
// already-open Bleve store, such as the boot BackgroundIndexer's live HEAD.
func (b *CommittedKnowledgeBackend) RefreshWithBleveStore(ctx context.Context, store *sylkdir.GlobalVersionBleveStore) error {
	if store == nil {
		return fmt.Errorf("committed backend: bleve store is required")
	}
	return b.refresh(ctx, store, true, false)
}

// AdoptOwnedBleve refreshes state from disk and closes any previously-retired
// external Bleve state that was kept alive while background indexing finished.
func (b *CommittedKnowledgeBackend) AdoptOwnedBleve(ctx context.Context) error {
	return b.refresh(ctx, nil, false, true)
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
	if err := b.refreshLocked(ctx, attachedBleve, false, true); err != nil {
		return fmt.Errorf("committed ingest: refresh backend: %w", err)
	}
	transferredAttached = refreshAttached
	return nil
}

// Close releases all live and retired committed-global resources.
func (b *CommittedKnowledgeBackend) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		b.stateMu.Lock()
		state := b.state
		retired := b.retired
		b.state = nil
		b.retired = nil
		b.stateMu.Unlock()

		var errs []error
		if state != nil {
			if err := state.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		for _, stale := range retired {
			if stale == nil {
				continue
			}
			if err := stale.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		b.embedderMu.Lock()
		if b.embedderIdle != nil {
			b.embedderIdle.Stop()
			b.embedderIdle = nil
		}
		if closer, ok := b.embedder.(io.Closer); ok && closer != nil {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		b.embedder = nil
		b.embedderMu.Unlock()
		closeErr = errors.Join(errs...)
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

func (b *CommittedKnowledgeBackend) refresh(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool, closeRetired bool) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	return b.refreshLocked(ctx, attachedBleve, externalBleve, closeRetired)
}

func (b *CommittedKnowledgeBackend) refreshLocked(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool, closeRetired bool) error {
	nextState, err := b.buildState(ctx, attachedBleve, externalBleve)
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

func (b *CommittedKnowledgeBackend) buildState(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool) (*committedKnowledgeState, error) {
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
		if err := bleveStore.OpenHead(); err != nil {
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

// buildCommittedMetadataIndex constructs the per-path metadata
// derivation by streaming nodes and edges through the underlying
// stores. Build-time heap residency is bounded to one *Node or
// *Edge in flight plus the accumulating in-heap byPath / nodeByID —
// the latter are SCRATCH and dropped after the build commits the
// derived index to the persistent store.
//
// The two-pass shape (nodes first, edges second) is preserved
// because edge processing depends on the nodeByID lookup populated
// by the first pass.
//
// Steady-state heap residency after this function returns is
// O(LRU cache size) regardless of repository scale; the byPath
// payload lives in mmap'd bbolt and pages into OS cache as queries
// touch it. metaStore is optional — when nil, the index falls back
// to the in-heap byPath map (used by paths that don't allocate a
// SylkDir, e.g., tests).
func buildCommittedMetadataIndex(ctx context.Context, head sylkdir.SemanticVersion, nodeStore *sylkdir.GlobalVersionNodeStore, edgeStore *sylkdir.GlobalVersionEdgeStore, metaStore *committedMetaStore) (*committedMetadataIndex, error) {
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
	// LRU-bounded hot working set.
	if metaStore != nil {
		if err := metaStore.PersistAll(index.byPath); err != nil {
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
