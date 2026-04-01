package librarian

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	bleve "github.com/blevesearch/bleve/v2"
)

const defaultKnowledgeSyncDebounce = 500 * time.Millisecond

// KnowledgeSyncConfig configures the Librarian-owned background knowledge sync.
type KnowledgeSyncConfig struct {
	ProjectRoot string
	Watcher     interface {
		Events() <-chan git.StatusUpdate
	}
	Store   *knowledge.KnowledgeStore
	Backend interface {
		RefreshFromDisk(ctx context.Context) error
		RefreshWithBleveStore(ctx context.Context, store *sylkdir.GlobalVersionBleveStore) error
		SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error)
	}
	Scope    *concurrency.GoroutineScope
	Logger   *slog.Logger
	Debounce time.Duration

	// InitialSync requests an immediate run on startup. Useful when the initial
	// phase-4 knowledge boot failed and the background sync should recover.
	InitialSync bool

	// RunBoot is injectable for tests. Nil uses boot.BootWithConfig.
	RunBoot func(ctx context.Context) (*boot.PipelineResult, error)
}

// KnowledgeSyncService listens to git status updates and incrementally refreshes
// the committed knowledge graph/document store using the canonical boot pipeline.
type KnowledgeSyncService struct {
	cfg KnowledgeSyncConfig

	cancel          context.CancelFunc
	promotionCancel context.CancelFunc
	promotionSeq    uint64
	running         atomic.Bool
	closed          atomic.Bool
	gen             atomic.Uint64

	mu sync.Mutex
}

func NewKnowledgeSyncService(cfg KnowledgeSyncConfig) (*KnowledgeSyncService, error) {
	if cfg.ProjectRoot == "" {
		return nil, fmt.Errorf("knowledge sync: project root is required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("knowledge sync: knowledge store is required")
	}
	if cfg.Backend == nil {
		return nil, fmt.Errorf("knowledge sync: backend is required")
	}
	if cfg.Watcher == nil && !cfg.InitialSync {
		return nil, fmt.Errorf("knowledge sync: watcher is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = defaultKnowledgeSyncDebounce
	}
	if cfg.RunBoot == nil {
		cfg.RunBoot = func(ctx context.Context) (*boot.PipelineResult, error) {
			return boot.BootWithConfig(ctx, boot.PipelineConfig{
				ProjectRoot: cfg.ProjectRoot,
				OnProgress:  cfg.Store.NotifyProgress,
				Scope:       cfg.Scope,
			})
		}
	}
	return &KnowledgeSyncService{cfg: cfg}, nil
}

func (s *KnowledgeSyncService) Start() error {
	if s == nil {
		return nil
	}
	if s.closed.Load() {
		return fmt.Errorf("knowledge sync: service is closed")
	}
	if s.running.Swap(true) {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	if s.cfg.Scope != nil {
		if err := s.cfg.Scope.Go("librarian-knowledge-sync", 0, func(scopeCtx context.Context) error {
			runCtx, stop := bindKnowledgeSyncContext(scopeCtx, ctx)
			defer stop()
			s.loop(runCtx)
			return nil
		}); err != nil {
			s.cancel = nil
			s.running.Store(false)
			cancel()
			return fmt.Errorf("knowledge sync: start loop: %w", err)
		}
		return nil
	}

	go s.loop(ctx)
	return nil
}

func (s *KnowledgeSyncService) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	promotionCancel := s.promotionCancel
	s.cancel = nil
	s.promotionCancel = nil
	s.mu.Unlock()
	s.closed.Store(true)
	if promotionCancel != nil {
		promotionCancel()
	}
	if cancel != nil {
		cancel()
	}
	s.running.Store(false)
	return nil
}

func (s *KnowledgeSyncService) loop(ctx context.Context) {
	events := (<-chan git.StatusUpdate)(nil)
	if s.cfg.Watcher != nil {
		events = s.cfg.Watcher.Events()
	}

	timer := time.NewTimer(s.cfg.Debounce)
	if !s.cfg.InitialSync {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	timerActive := s.cfg.InitialSync
	pending := s.cfg.InitialSync
	var (
		lastFingerprint knowledgeSyncFingerprint
		haveFingerprint bool
	)

	for {
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case update, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			fingerprint := fingerprintKnowledgeSyncUpdate(update)
			if haveFingerprint && fingerprint == lastFingerprint {
				continue
			}
			lastFingerprint = fingerprint
			haveFingerprint = true
			pending = true
			if !timerActive {
				timer.Reset(s.cfg.Debounce)
				timerActive = true
			}
		case <-timer.C:
			timerActive = false
			if !pending {
				continue
			}
			pending = false
			if err := s.runSync(ctx); err != nil && s.cfg.Logger != nil {
				s.cfg.Logger.Warn("librarian knowledge sync failed", "error", err)
			}
		}
	}
}

func (s *KnowledgeSyncService) runSync(ctx context.Context) error {
	result, err := s.cfg.RunBoot(ctx)
	if err != nil {
		return err
	}

	if bgIdx := result.BackgroundIndexer; bgIdx != nil && bgIdx.BleveStore() != nil {
		if err := s.cfg.Backend.RefreshWithBleveStore(ctx, bgIdx.BleveStore()); err != nil {
			return fmt.Errorf("knowledge sync: refresh with bleve store: %w", err)
		}
	} else if err := s.cfg.Backend.RefreshFromDisk(ctx); err != nil {
		return fmt.Errorf("knowledge sync: refresh from disk: %w", err)
	}

	s.cfg.Store.PromotePartial(query.NewBleveSearcher(s.cfg.Backend), result.BackgroundIndexer, nil)

	gen := s.gen.Add(1)
	if result.BackgroundIndexer == nil {
		s.cancelPromotionWaiter()
		if s.gen.Load() == gen {
			s.cfg.Store.PromoteFull()
		}
		return nil
	}

	s.startFullPromotionWaiter(ctx, gen, result.BackgroundIndexer)
	return nil
}

func (s *KnowledgeSyncService) startFullPromotionWaiter(ctx context.Context, gen uint64, waiter knowledge.BackgroundIndexWaiter) {
	if waiter == nil {
		return
	}

	waiterCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	prevCancel := s.promotionCancel
	s.promotionSeq++
	seq := s.promotionSeq
	s.promotionCancel = cancel
	s.mu.Unlock()

	if prevCancel != nil {
		prevCancel()
	}

	if s.cfg.Scope != nil {
		if err := s.cfg.Scope.Go("librarian-knowledge-sync-promote-full", 0, func(scopeCtx context.Context) error {
			runCtx, stop := bindKnowledgeSyncContext(scopeCtx, waiterCtx)
			defer stop()
			s.awaitFullPromotion(runCtx, seq, gen, waiter)
			return nil
		}); err == nil {
			return
		}
		cancel()
		s.clearPromotionWaiter(seq)
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("knowledge sync: failed to start scoped full-promotion waiter")
		}
		return
	}

	go s.awaitFullPromotion(waiterCtx, seq, gen, waiter)
}

func (s *KnowledgeSyncService) cancelPromotionWaiter() {
	s.mu.Lock()
	cancel := s.promotionCancel
	s.promotionCancel = nil
	s.promotionSeq++
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *KnowledgeSyncService) clearPromotionWaiter(seq uint64) {
	s.mu.Lock()
	if s.promotionSeq == seq {
		s.promotionCancel = nil
	}
	s.mu.Unlock()
}

func (s *KnowledgeSyncService) awaitFullPromotion(ctx context.Context, seq uint64, gen uint64, waiter knowledge.BackgroundIndexWaiter) {
	if waiter == nil {
		return
	}
	defer s.clearPromotionWaiter(seq)
	select {
	case <-waiter.Ready():
		if ctx.Err() != nil {
			return
		}
		if s.gen.Load() == gen {
			s.cfg.Store.PromoteFull()
		}
	case <-ctx.Done():
	}
}

type knowledgeSyncFingerprint struct {
	statusLen   int
	statusHash  uint64
	trackedLen  int
	trackedHash uint64
	dirsLen     int
	dirsHash    uint64
}

func fingerprintKnowledgeSyncUpdate(update git.StatusUpdate) knowledgeSyncFingerprint {
	return knowledgeSyncFingerprint{
		statusLen:   len(update.StatusMap),
		statusHash:  knowledgeSyncStatusHash(update.StatusMap),
		trackedLen:  len(update.TrackedSet),
		trackedHash: knowledgeSyncSetHash(update.TrackedSet),
		dirsLen:     len(update.TrackedDirs),
		dirsHash:    knowledgeSyncSetHash(update.TrackedDirs),
	}
}

func knowledgeSyncStatusHash(m map[string]git.GitFileState) uint64 {
	const fnvBasis = 14695981039346656037
	const fnvPrime = 1099511628211

	var combined uint64
	for path, state := range m {
		h := uint64(fnvBasis)
		for i := 0; i < len(path); i++ {
			h ^= uint64(path[i])
			h *= fnvPrime
		}
		h ^= uint64(state)
		h *= fnvPrime
		combined ^= h
	}
	return combined
}

func knowledgeSyncSetHash(m map[string]struct{}) uint64 {
	const fnvBasis = 14695981039346656037
	const fnvPrime = 1099511628211

	var combined uint64
	for path := range m {
		h := uint64(fnvBasis)
		for i := 0; i < len(path); i++ {
			h ^= uint64(path[i])
			h *= fnvPrime
		}
		combined ^= h
	}
	return combined
}

func bindKnowledgeSyncContext(scopeCtx context.Context, localCtx context.Context) (context.Context, func()) {
	if scopeCtx == nil {
		return localCtx, func() {}
	}
	if localCtx == nil {
		return scopeCtx, func() {}
	}
	ctx, cancel := context.WithCancel(scopeCtx)
	stopLocal := context.AfterFunc(localCtx, cancel)
	return ctx, func() {
		stopLocal()
		cancel()
	}
}
