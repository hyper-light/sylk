package knowledgeruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	bleve "github.com/blevesearch/bleve/v2"
)

// ErrCommittedBackendUnavailable is returned when committed-global retrieval
// is requested before the committed backend has been initialized.
var ErrCommittedBackendUnavailable = errors.New("committed knowledge backend is unavailable")

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

	canonicalSet    map[string]struct{}
	symbolSet       map[string]struct{}
	nodeKindSet     map[string]struct{}
	relatedPathSet  map[string]struct{}
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

type committedMetadataIndex struct {
	byPath   map[string]*committedPathMeta
	nodeByID map[uint32]committedNodeMeta
}

type committedKnowledgeState struct {
	head        sylkdir.SemanticVersion
	bleveStore  *sylkdir.GlobalVersionBleveStore
	nodeStore   *sylkdir.GlobalVersionNodeStore
	edgeStore   *sylkdir.GlobalVersionEdgeStore
	docStore    *sylkdir.GlobalVersionDocStore
	externalBleve bool
	index       *committedMetadataIndex
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
	return errors.Join(errs...)
}

// CommittedKnowledgeBackend provides committed-global search plus runtime fetch
// ingestion on top of the `.sylk` global stores.
type CommittedKnowledgeBackend struct {
	projectRoot string
	logger      *slog.Logger

	refreshMu sync.Mutex

	stateMu  sync.RWMutex
	state    *committedKnowledgeState
	retired  []*committedKnowledgeState
	closeOnce sync.Once

	embedderMu sync.Mutex
	embedder   embedder.Embedder
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
		hits = append(hits, enrichCommittedHit(doc, state.index.byPath[doc.Path]))
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

	bleveStore := sylkdir.NewGlobalVersionBleveStore(sd, gm.GetHead())
	if err := bleveStore.OpenHead(); err != nil {
		return fmt.Errorf("committed ingest: open global bleve: %w", err)
	}
	transferredBleve := false
	defer func() {
		if !transferredBleve {
			_ = bleveStore.CloseAll()
		}
	}()

	session, err := createCommittedIngestSession(sd, gm)
	if err != nil {
		return err
	}
	defer session.Close()
	_ = session.CloseBleve()
	session.BleveStore = nil

	jointReq := &sylkdir.JointDocRequest{
		DocID:    stableFetchedDocumentID(provenance.FetchURL),
		Path:     provenance.FetchURL,
		Content:  []byte(content),
		DocType:  search.DocTypeWebFetch,
		Language: language,
		Domain:   sylkdir.DomainResearch,
	}

	ingestEmbedder, ingestErr := b.ensureEmbedder(ctx)
	if ingestErr != nil {
		b.logger.Warn("committed ingest: embedder unavailable, proceeding without vectors", "error", ingestErr)
		ingestEmbedder = nil
	}

	if _, err := session.InsertDocument(ctx, jointReq, ingestEmbedder); err != nil {
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

	if err := b.refresh(ctx, bleveStore, false, true); err != nil {
		return fmt.Errorf("committed ingest: refresh backend: %w", err)
	}
	transferredBleve = true
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

func (b *CommittedKnowledgeBackend) ensureEmbedder(ctx context.Context) (embedder.Embedder, error) {
	b.embedderMu.Lock()
	defer b.embedderMu.Unlock()
	if b.embedder != nil {
		return b.embedder, nil
	}
	result, err := embedder.NewEmbedder(ctx, embedder.FactoryConfig{})
	if err != nil {
		return nil, err
	}
	b.embedder = result.Embedder
	return b.embedder, nil
}

func (b *CommittedKnowledgeBackend) refresh(ctx context.Context, attachedBleve *sylkdir.GlobalVersionBleveStore, externalBleve bool, closeRetired bool) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()

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

	index, err := buildCommittedMetadataIndex(ctx, head, nodeStore, edgeStore)
	if err != nil {
		_ = nodeStore.Close()
		_ = edgeStore.Close()
		_ = docStore.Close()
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
		externalBleve: externalBleve,
		index:         index,
	}, nil
}

func buildCommittedMetadataIndex(ctx context.Context, head sylkdir.SemanticVersion, nodeStore *sylkdir.GlobalVersionNodeStore, edgeStore *sylkdir.GlobalVersionEdgeStore) (*committedMetadataIndex, error) {
	nodes, err := nodeStore.ReadAllFromVersion(head)
	if err != nil {
		return nil, fmt.Errorf("committed backend: load nodes: %w", err)
	}
	edges, err := edgeStore.ReadAllFromVersion(head)
	if err != nil {
		return nil, fmt.Errorf("committed backend: load edges: %w", err)
	}

	index := &committedMetadataIndex{
		byPath:   make(map[string]*committedPathMeta),
		nodeByID: make(map[uint32]committedNodeMeta, len(nodes)),
	}

	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
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
			continue
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
	}

	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source, ok := index.nodeByID[edge.SourceID]
		if !ok || strings.TrimSpace(source.Path) == "" {
			continue
		}
		target, targetOK := index.nodeByID[edge.TargetID]
		pathMeta := index.byPath[source.Path]
		if pathMeta == nil {
			continue
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
	}

	for _, meta := range index.byPath {
		meta.finalize()
	}
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
