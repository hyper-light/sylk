package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// Archivalist is the main agent for managing AI-generated content and conversation memory
type Archivalist struct {
	store        *Store
	archive      *Archive
	client       *Client
	agentContext *AgentContext
	config       Config

	// Concurrency components
	registry         *Registry
	eventLog         *EventLog
	conflictDetector *ConflictDetector
	conflictHistory  *ConflictHistory

	// RAG components
	queryCache  *QueryCache
	embeddings  *EmbeddingStore
	retriever   *SemanticRetriever
	synthesizer *Synthesizer
	memory      *MemoryManager
	toolHandler *ToolHandler

	// Event bus integration
	bus          guide.EventBus
	channels     *guide.AgentChannels
	requestSub   guide.Subscription
	responseSub  guide.Subscription
	registrySub  guide.Subscription
	running      bool
	knownAgents  map[string]*guide.AgentAnnouncement

	// LLM Skills and Hooks
	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry
}

// Config holds configuration for the Archivalist agent
type Config struct {
	// Anthropic API configuration
	AnthropicAPIKey string
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Storage configuration
	ArchivePath    string // Path to SQLite archive, defaults to .sylk/archive.db
	TokenThreshold int    // Token threshold for archiving, defaults to 750K

	// Feature flags
	EnableArchive bool // Enable SQLite archive (L2 storage)
	EnableRAG     bool // Enable RAG components (query cache, embeddings, synthesis)

	// Concurrency configuration
	MaxEvents           int           // Max events in event log (default: 10000)
	IdleTimeout         time.Duration // Time before agent marked idle (default: 5m)
	InactiveTimeout     time.Duration // Time before agent marked inactive (default: 30m)
	ConflictHistorySize int           // Max conflicts to track (default: 100)

	// RAG configuration
	EmbeddingsPath    string  // Path to embeddings database
	QueryCacheSize    int     // Max cached queries (default: 10000)
	SimilarityThreshold float64 // Query similarity threshold (default: 0.95)
}

// New creates a new Archivalist agent
func New(cfg Config) (*Archivalist, error) {
	cfg = applyConfigDefaults(cfg)
	components, err := createComponents(cfg)
	if err != nil {
		return nil, err
	}
	archivalist := assembleArchivalist(cfg, components)
	if cfg.EnableRAG {
		if err := archivalist.initRAG(cfg); err != nil {
			return nil, fmt.Errorf("failed to initialize RAG: %w", err)
		}
	}
	return archivalist, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.TokenThreshold == 0 {
		cfg.TokenThreshold = DefaultTokenThreshold
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 10000
	}
	if cfg.ConflictHistorySize == 0 {
		cfg.ConflictHistorySize = 100
	}
	return cfg
}

// archivalistComponents holds intermediate components for assembly
type archivalistComponents struct {
	client           *Client
	archive          *Archive
	store            *Store
	agentContext     *AgentContext
	registry         *Registry
	eventLog         *EventLog
	conflictDetector *ConflictDetector
	conflictHistory  *ConflictHistory
}

func createComponents(cfg Config) (*archivalistComponents, error) {
	client, err := NewClient(ClientConfig{
		AnthropicAPIKey: cfg.AnthropicAPIKey,
		SystemPrompt:    cfg.SystemPrompt,
		MaxOutputTokens: cfg.MaxOutputTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	archive, err := createArchive(cfg)
	if err != nil {
		return nil, err
	}

	agentContext := NewAgentContext()
	registry := NewRegistry(RegistryConfig{
		IdleTimeout:     cfg.IdleTimeout,
		InactiveTimeout: cfg.InactiveTimeout,
	})

	return &archivalistComponents{
		client:           client,
		archive:          archive,
		store:            NewStore(StoreConfig{TokenThreshold: cfg.TokenThreshold, Archive: archive}),
		agentContext:     agentContext,
		registry:         registry,
		eventLog:         NewEventLog(EventLogConfig{MaxEvents: cfg.MaxEvents}),
		conflictDetector: NewConflictDetector(agentContext, registry),
		conflictHistory:  NewConflictHistory(cfg.ConflictHistorySize),
	}, nil
}

func createArchive(cfg Config) (*Archive, error) {
	if !cfg.EnableArchive {
		return nil, nil
	}
	archive, err := NewArchive(ArchiveConfig{Path: cfg.ArchivePath})
	if err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}
	return archive, nil
}

func assembleArchivalist(cfg Config, c *archivalistComponents) *Archivalist {
	// Create skills registry and loader
	skillsRegistry := skills.NewRegistry()
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = []string{"store", "query", "briefing"}
	skillsLoaderCfg.AutoLoadDomains = []string{"memory", "chronicle"}
	skillLoader := skills.NewLoader(skillsRegistry, skillsLoaderCfg)

	// Create hook registry
	hookRegistry := skills.NewHookRegistry()

	a := &Archivalist{
		store:            c.store,
		archive:          c.archive,
		client:           c.client,
		agentContext:     c.agentContext,
		config:           cfg,
		registry:         c.registry,
		eventLog:         c.eventLog,
		conflictDetector: c.conflictDetector,
		conflictHistory:  c.conflictHistory,
		knownAgents:      make(map[string]*guide.AgentAnnouncement),
		skills:           skillsRegistry,
		skillLoader:      skillLoader,
		hooks:            hookRegistry,
	}

	// Register core archivalist skills
	a.registerCoreSkills()

	return a
}

// initRAG initializes RAG components
func (a *Archivalist) initRAG(cfg Config) error {
	// Create embedder (mock for now, can be replaced with real embedder)
	embedder := NewMockEmbedder(1536)

	// Create query cache
	cacheSize := cfg.QueryCacheSize
	if cacheSize == 0 {
		cacheSize = 10000
	}
	threshold := cfg.SimilarityThreshold
	if threshold == 0 {
		threshold = 0.95
	}
	a.queryCache = NewQueryCache(QueryCacheConfig{
		HitThreshold: threshold,
		MaxQueries:   cacheSize,
		UseEmbeddings: true,
	}, embedder)

	// Create embedding store
	embStore, err := NewEmbeddingStore(EmbeddingStoreConfig{
		DBPath:      cfg.EmbeddingsPath,
		MaxInMemory: 10000,
		Dimension:   1536,
	})
	if err != nil {
		return fmt.Errorf("failed to create embedding store: %w", err)
	}
	a.embeddings = embStore

	// Create memory manager
	a.memory = NewMemoryManager(DefaultTokenBudget())

	// Create semantic retriever
	retriever, err := NewSemanticRetriever(SemanticRetrieverConfig{
		DBPath:       cfg.ArchivePath,
		Embeddings:   a.embeddings,
		Embedder:     embedder,
		AgentContext: a.agentContext,
	})
	if err != nil {
		return fmt.Errorf("failed to create retriever: %w", err)
	}
	a.retriever = retriever

	// Create synthesizer
	a.synthesizer = NewSynthesizer(SynthesizerConfig{
		Client:     &a.client.anthropic,
		Retriever:  a.retriever,
		QueryCache: a.queryCache,
	})

	// Create tool handler
	a.toolHandler = NewToolHandler(a, a.synthesizer)

	return nil
}

// Close closes the archivalist and its resources
func (a *Archivalist) Close() error {
	// Stop event bus subscriptions first
	a.Stop()

	// Close event log first to flush any pending writes
	if a.eventLog != nil {
		a.eventLog.Close()
	}

	// Close RAG components
	if a.embeddings != nil {
		a.embeddings.Close()
	}
	if a.retriever != nil {
		a.retriever.Close()
	}

	// Close archive
	if a.archive != nil {
		return a.archive.Close()
	}
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The archivalist subscribes to its own channels and the registry topic.
func (a *Archivalist) Start(bus guide.EventBus) error {
	if a.running {
		return fmt.Errorf("archivalist is already running")
	}

	a.bus = bus
	a.channels = guide.NewAgentChannels("archivalist")

	// Subscribe to own request channel (archivalist.requests)
	var err error
	a.requestSub, err = bus.SubscribeAsync(a.channels.Requests, a.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	a.responseSub, err = bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		a.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	a.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, a.handleRegistryAnnouncement)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	a.running = true
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Archivalist) Stop() error {
	if !a.running {
		return nil
	}

	var errs []error

	if a.requestSub != nil {
		if err := a.requestSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
		a.requestSub = nil
	}

	if a.responseSub != nil {
		if err := a.responseSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
		a.responseSub = nil
	}

	if a.registrySub != nil {
		if err := a.registrySub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
		a.registrySub = nil
	}

	a.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

// IsRunning returns true if the archivalist is actively processing bus messages
func (a *Archivalist) IsRunning() bool {
	return a.running
}

// Bus returns the event bus used by the archivalist
func (a *Archivalist) Bus() guide.EventBus {
	return a.bus
}

// Channels returns the archivalist's channel configuration
func (a *Archivalist) Channels() *guide.AgentChannels {
	return a.channels
}

// handleBusRequest processes incoming forwarded requests from the event bus
func (a *Archivalist) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	// Process the request
	ctx := context.Background()
	startTime := time.Now()

	result, err := a.processForwardedRequest(ctx, fwd)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "archivalist",
		RespondingAgentName: "archivalist",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			fwd.CorrelationID,
			"archivalist",
			err.Error(),
		)
		return a.bus.Publish(a.channels.Errors, errMsg)
	}

	resp.Data = result

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(
		fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		resp,
	)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

// processForwardedRequest handles the actual request processing
func (a *Archivalist) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Route based on intent and domain
	switch fwd.Intent {
	case guide.IntentRecall:
		return a.handleRecall(ctx, fwd)
	case guide.IntentStore:
		return a.handleStore(ctx, fwd)
	case guide.IntentCheck:
		return a.handleCheck(ctx, fwd)
	case guide.IntentDeclare:
		return a.handleDeclare(ctx, fwd)
	case guide.IntentComplete:
		return a.handleComplete(ctx, fwd)
	default:
		return nil, fmt.Errorf("unsupported intent: %s", fwd.Intent)
	}
}

// handleRecall processes recall (query) requests
func (a *Archivalist) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	query := ArchiveQuery{}

	// Map domain to category
	switch fwd.Domain {
	case guide.DomainPatterns:
		query.Categories = []Category{CategoryGeneral}
	case guide.DomainFailures:
		query.Categories = []Category{CategoryIssue}
	case guide.DomainDecisions:
		query.Categories = []Category{CategoryDecision}
	case guide.DomainFiles:
		return a.agentContext.GetModifiedFiles(), nil
	case guide.DomainLearnings:
		query.Categories = []Category{CategoryInsight}
	}

	// Extract entities for filtering
	if fwd.Entities != nil {
		if fwd.Entities.Scope != "" {
			query.SearchText = fwd.Entities.Scope
		}
		if fwd.Entities.Limit > 0 {
			query.Limit = fwd.Entities.Limit
		}
	}

	if query.Limit == 0 {
		query.Limit = 10
	}

	return a.store.Query(query)
}

// handleStore processes store requests
func (a *Archivalist) handleStore(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	entry := &Entry{
		Content: fwd.Input,
		Source:  SourceModelClaudeOpus45, // Default, could be extracted from metadata
	}

	// Map domain to category
	switch fwd.Domain {
	case guide.DomainPatterns:
		entry.Category = CategoryGeneral
	case guide.DomainFailures:
		entry.Category = CategoryIssue
	case guide.DomainDecisions:
		entry.Category = CategoryDecision
	case guide.DomainLearnings:
		entry.Category = CategoryInsight
	default:
		entry.Category = CategoryGeneral
	}

	return a.StoreEntry(ctx, entry), nil
}

// handleCheck processes check (verification) requests
func (a *Archivalist) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Search for matching entries
	entries, err := a.store.SearchText(fwd.Input, true, 5)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"found": len(entries) > 0,
		"count": len(entries),
		"entries": entries,
	}, nil
}

// handleDeclare processes declare (intent announcement) requests
func (a *Archivalist) handleDeclare(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.agentContext.SetCurrentTask(fwd.Input, "", SourceModelClaudeOpus45)
	return map[string]any{"declared": true, "task": fwd.Input}, nil
}

// handleComplete processes complete (task completion) requests
func (a *Archivalist) handleComplete(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.agentContext.CompleteStep(fwd.Input)
	return map[string]any{"completed": true, "step": fwd.Input}, nil
}

// handleBusResponse processes responses to requests we made
func (a *Archivalist) handleBusResponse(msg *guide.Message) error {
	// For now, just log responses to our requests
	// Future: implement callback handling for async sub-requests
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (a *Archivalist) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		a.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(a.knownAgents, ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the archivalist knows about
func (a *Archivalist) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(a.knownAgents))
	for k, v := range a.knownAgents {
		result[k] = v
	}
	return result
}

// PublishRequest publishes a request to the Guide for routing
func (a *Archivalist) PublishRequest(req *guide.RouteRequest) error {
	if !a.running {
		return fmt.Errorf("archivalist is not running")
	}

	req.SourceAgentID = "archivalist"
	req.SourceAgentName = "archivalist"

	msg := guide.NewRequestMessage(
		fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		req,
	)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// StoreEntry stores a chronicle entry
func (a *Archivalist) StoreEntry(ctx context.Context, entry *Entry) SubmissionResult {
	if !IsValidSource(entry.Source) && entry.Source != SourceModelUser && entry.Source != SourceModelArchivalist {
		return SubmissionResult{
			Success: false,
			Error:   fmt.Errorf("invalid source model: %s", entry.Source),
		}
	}

	id, err := a.store.InsertEntry(entry)
	if err != nil {
		return SubmissionResult{
			Success: false,
			Error:   err,
		}
	}

	return SubmissionResult{
		Success: true,
		ID:      id,
	}
}

// StoreTaskState stores a task state entry (convenience wrapper)
func (a *Archivalist) StoreTaskState(ctx context.Context, content string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category: CategoryTaskState,
		Content:  content,
		Source:   source,
	})
}

// StoreDecision stores a decision entry (convenience wrapper)
func (a *Archivalist) StoreDecision(ctx context.Context, choice, rationale string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category: CategoryDecision,
		Title:    choice,
		Content:  rationale,
		Source:   source,
	})
}

// StoreIssue stores an issue entry (convenience wrapper)
func (a *Archivalist) StoreIssue(ctx context.Context, problem string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category: CategoryIssue,
		Content:  problem,
		Source:   source,
	})
}

// StoreTimelineEvent stores a timeline event (convenience wrapper)
func (a *Archivalist) StoreTimelineEvent(ctx context.Context, content string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category:  CategoryTimeline,
		Content:   content,
		Source:    source,
		CreatedAt: time.Now(),
	})
}

// StoreInsight stores an insight
func (a *Archivalist) StoreInsight(ctx context.Context, content string, source SourceModel) SubmissionResult {
	entry := &Entry{
		Category: CategoryInsight,
		Content:  content,
		Source:   source,
	}
	return a.StoreEntry(ctx, entry)
}

// StoreUserVoice stores a user preference or quote
func (a *Archivalist) StoreUserVoice(ctx context.Context, content string) SubmissionResult {
	entry := &Entry{
		Category: CategoryUserVoice,
		Content:  content,
		Source:   SourceModelUser,
	}
	return a.StoreEntry(ctx, entry)
}

// StoreHypothesis stores an untested assumption
func (a *Archivalist) StoreHypothesis(ctx context.Context, content string, source SourceModel) SubmissionResult {
	entry := &Entry{
		Category: CategoryHypothesis,
		Content:  content,
		Source:   source,
	}
	return a.StoreEntry(ctx, entry)
}

// StoreOpenThread stores an unfinished item to revisit
func (a *Archivalist) StoreOpenThread(ctx context.Context, content string, source SourceModel) SubmissionResult {
	entry := &Entry{
		Category: CategoryOpenThread,
		Content:  content,
		Source:   source,
	}
	return a.StoreEntry(ctx, entry)
}

// UpdateEntry updates an existing entry
func (a *Archivalist) UpdateEntry(ctx context.Context, id string, updates func(*Entry)) error {
	return a.store.UpdateEntry(id, updates)
}

// Query retrieves entries matching the query parameters
func (a *Archivalist) Query(ctx context.Context, query ArchiveQuery) ([]*Entry, error) {
	return a.store.Query(query)
}

// QueryByCategory retrieves entries in a specific category
func (a *Archivalist) QueryByCategory(ctx context.Context, category Category, limit int) []*Entry {
	return a.store.QueryByCategory(category, limit)
}

// SearchText performs text search across all storage
func (a *Archivalist) SearchText(ctx context.Context, text string, includeArchived bool, limit int) ([]*Entry, error) {
	return a.store.SearchText(text, includeArchived, limit)
}

// GetEntry retrieves a single entry by ID
func (a *Archivalist) GetEntry(ctx context.Context, id string) (*Entry, bool) {
	return a.store.GetEntry(id)
}

// RestoreFromArchive pulls entries from archive back into hot memory
func (a *Archivalist) RestoreFromArchive(ctx context.Context, ids []string) error {
	return a.store.RestoreFromArchive(ids)
}

// GenerateSummary creates a summary using Claude Sonnet 4.5
func (a *Archivalist) GenerateSummary(ctx context.Context, content string) (*Entry, error) {
	generated, err := a.client.GenerateSummary(ctx, content)
	if err != nil {
		return nil, err
	}

	entry := &Entry{
		Category:       CategoryGeneral,
		Title:          "Generated Summary",
		Content:        generated.Content,
		Source:         SourceModelArchivalist,
		TokensEstimate: generated.TokensUsed,
	}

	_, err = a.store.InsertEntry(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to store generated summary: %w", err)
	}

	return entry, nil
}

// GenerateSummaryFromEntries creates a summary from stored entries matching the query
func (a *Archivalist) GenerateSummaryFromEntries(ctx context.Context, query ArchiveQuery) (*Entry, error) {
	entries, err := a.store.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries found matching query")
	}

	// Convert entries to submissions for the client
	var submissions []Submission
	for _, e := range entries {
		submissions = append(submissions, Submission{
			Type: SubmissionTypeSummary,
			Summary: &Summary{
				ID:        e.ID,
				Content:   e.Content,
				Source:    e.Source,
				CreatedAt: e.CreatedAt,
			},
		})
	}

	generated, err := a.client.GenerateSummaryFromSubmissions(ctx, submissions)
	if err != nil {
		return nil, err
	}

	entry := &Entry{
		Category:       CategoryGeneral,
		Title:          "Generated Summary",
		Content:        generated.Content,
		Source:         SourceModelArchivalist,
		TokensEstimate: generated.TokensUsed,
		RelatedIDs:     generated.SourceIDs,
	}

	_, err = a.store.InsertEntry(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to store generated summary: %w", err)
	}

	return entry, nil
}

// GetSnapshot returns a full snapshot of current state
func (a *Archivalist) GetSnapshot(ctx context.Context) *ChronicleSnapshot {
	stats := a.store.Stats()
	session := a.store.GetCurrentSession()

	return &ChronicleSnapshot{
		Session:        session,
		Tasks:          a.store.QueryByCategory(CategoryTaskState, 10),
		OpenThreads:    a.store.QueryByCategory(CategoryOpenThread, 10),
		RecentTimeline: a.store.QueryByCategory(CategoryTimeline, 20),
		ActiveInsights: a.store.QueryByCategory(CategoryInsight, 10),
		UserVoice:      a.store.QueryByCategory(CategoryUserVoice, 10),
		Hypotheses:     a.store.QueryByCategory(CategoryHypothesis, 10),
		Stats:          stats,
	}
}

// EndSession ends the current session and archives its data
func (a *Archivalist) EndSession(ctx context.Context, summary string, primaryFocus string) error {
	return a.store.EndSession(summary, primaryFocus)
}

// GetCurrentSession returns the current session
func (a *Archivalist) GetCurrentSession() *Session {
	return a.store.GetCurrentSession()
}

// GetRecentSessions retrieves recent sessions from the archive
func (a *Archivalist) GetRecentSessions(ctx context.Context, limit int) ([]*Session, error) {
	if a.archive == nil {
		return nil, fmt.Errorf("archive not enabled")
	}
	return a.archive.GetRecentSessions(limit)
}

// Stats returns current storage statistics
func (a *Archivalist) Stats() StorageStats {
	return a.store.Stats()
}

// Legacy compatibility types for backward compatibility

// Summary represents a summary submitted by an external model (legacy)
type Summary struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	Source    SourceModel    `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// PromptResponse represents a prompt and its response from an external model (legacy)
type PromptResponse struct {
	ID        string         `json:"id"`
	Prompt    string         `json:"prompt"`
	Response  string         `json:"response"`
	Source    SourceModel    `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// SubmissionType categorizes what kind of data was submitted (legacy)
type SubmissionType string

const (
	SubmissionTypeSummary        SubmissionType = "summary"
	SubmissionTypePromptResponse SubmissionType = "prompt_response"
)

// Submission wraps either a Summary or PromptResponse for unified handling (legacy)
type Submission struct {
	Type           SubmissionType  `json:"type"`
	Summary        *Summary        `json:"summary,omitempty"`
	PromptResponse *PromptResponse `json:"prompt_response,omitempty"`
}

// SubmitSummary accepts a summary from an external AI model (legacy compatibility)
func (a *Archivalist) SubmitSummary(ctx context.Context, content string, source SourceModel, metadata map[string]any) SubmissionResult {
	entry := &Entry{
		Category: CategoryGeneral,
		Content:  content,
		Source:   source,
		Metadata: metadata,
	}
	return a.StoreEntry(ctx, entry)
}

// SubmitPromptResponse accepts a prompt/response pair from an external AI model (legacy compatibility)
func (a *Archivalist) SubmitPromptResponse(ctx context.Context, prompt, response string, source SourceModel, metadata map[string]any) SubmissionResult {
	entry := &Entry{
		Category: CategoryGeneral,
		Title:    prompt,
		Content:  response,
		Source:   source,
		Metadata: metadata,
	}
	return a.StoreEntry(ctx, entry)
}

// =============================================================================
// Agent Context Methods - For agent-to-agent coordination
// =============================================================================

// RecordFileRead records that a file has been read by an agent
func (a *Archivalist) RecordFileRead(path, summary string, agent SourceModel) {
	a.agentContext.RecordFileRead(path, summary, agent)
}

// RecordFileModified records that a file has been modified
func (a *Archivalist) RecordFileModified(path string, startLine, endLine int, description string, agent SourceModel) {
	a.agentContext.RecordFileModified(path, FileChange{
		StartLine:   startLine,
		EndLine:     endLine,
		Description: description,
	}, agent)
}

// RecordFileCreated records that a file has been created
func (a *Archivalist) RecordFileCreated(path, summary string, agent SourceModel) {
	a.agentContext.RecordFileCreated(path, summary, agent)
}

// WasFileRead checks if a file has already been read this session
func (a *Archivalist) WasFileRead(path string) bool {
	return a.agentContext.WasFileRead(path)
}

// GetFileState returns the tracked state of a file
func (a *Archivalist) GetFileState(path string) (*FileState, bool) {
	return a.agentContext.GetFileStateWithCheck(path)
}

// GetModifiedFiles returns all files that have been modified or created
func (a *Archivalist) GetModifiedFiles() []*FileState {
	return a.agentContext.GetModifiedFiles()
}

// RegisterPattern registers a coding pattern to follow
func (a *Archivalist) RegisterPattern(category, name, description, example string, agent SourceModel) {
	a.agentContext.RegisterPattern(&Pattern{
		Category:    category,
		Name:        name,
		Description: description,
		Example:     example,
		Source:      agent,
	})
}

// GetPatterns returns all registered patterns
func (a *Archivalist) GetPatterns() []*Pattern {
	return a.agentContext.GetAllPatterns()
}

// GetPatternsByCategory returns patterns for a specific category
func (a *Archivalist) GetPatternsByCategory(category string) []*Pattern {
	return a.agentContext.GetPatternsByCategory(category)
}

// RecordFailure records an approach that failed
func (a *Archivalist) RecordFailure(approach, reason, taskContext string, agent SourceModel) {
	a.agentContext.RecordFailure(approach, reason, taskContext, agent)
}

// RecordFailureWithResolution records a failure and what worked instead
func (a *Archivalist) RecordFailureWithResolution(approach, reason, taskContext, resolution string, agent SourceModel) {
	a.agentContext.RecordFailureWithResolution(approach, reason, taskContext, resolution, agent)
}

// CheckFailure checks if an approach has been tried and failed
func (a *Archivalist) CheckFailure(approach string) (*Failure, bool) {
	return a.agentContext.CheckFailure(approach)
}

// GetRecentFailures returns recent failures
func (a *Archivalist) GetRecentFailures(limit int) []*Failure {
	return a.agentContext.GetRecentFailures(limit)
}

// RecordUserWants records something the user wants
func (a *Archivalist) RecordUserWants(content, priority, source string) {
	a.agentContext.RecordUserWants(content, priority, source)
}

// RecordUserRejects records something the user rejected
func (a *Archivalist) RecordUserRejects(content, source string) {
	a.agentContext.RecordUserRejects(content, source)
}

// GetUserWants returns all recorded user wants
func (a *Archivalist) GetUserWants() []*Intent {
	return a.agentContext.GetUserWants()
}

// GetUserRejects returns all recorded user rejections
func (a *Archivalist) GetUserRejects() []*Intent {
	return a.agentContext.GetUserRejects()
}

// SetCurrentTask sets the current task being worked on
func (a *Archivalist) SetCurrentTask(task, objective string, agent SourceModel) {
	a.agentContext.SetCurrentTask(task, objective, agent)
}

// CompleteStep marks a step as completed
func (a *Archivalist) CompleteStep(step string) {
	a.agentContext.CompleteStep(step)
}

// SetCurrentStep sets the current step being worked on
func (a *Archivalist) SetCurrentStep(step string) {
	a.agentContext.SetCurrentStep(step)
}

// SetNextSteps sets the upcoming steps
func (a *Archivalist) SetNextSteps(steps []string) {
	a.agentContext.SetNextSteps(steps)
}

// AddBlocker adds a blocker
func (a *Archivalist) AddBlocker(blocker string) {
	a.agentContext.AddBlocker(blocker)
}

// RemoveBlocker removes a blocker
func (a *Archivalist) RemoveBlocker(blocker string) {
	a.agentContext.RemoveBlocker(blocker)
}

// GetResumeState returns the current resume state for handoff
func (a *Archivalist) GetResumeState() *ResumeState {
	return a.agentContext.GetResumeState()
}

// GetAgentBriefing returns everything an agent needs to continue work
func (a *Archivalist) GetAgentBriefing() *AgentBriefing {
	return a.agentContext.GetAgentBriefing()
}

// =============================================================================
// Protocol Methods - Efficient RMC for agent-to-agent communication
// =============================================================================

// RegisterAgent registers a new agent and returns its ID and current version
func (a *Archivalist) RegisterAgent(name, sessionID, parentID string, source SourceModel) (*Response, error) {
	if sessionID == "" {
		sessionID = a.store.GetCurrentSession().ID
	}

	agent, err := a.registry.Register(name, sessionID, parentID, source)
	if err != nil {
		return nil, err
	}

	// Log the registration event
	a.eventLog.Append(&Event{
		Type:      EventTypeAgentRegister,
		Version:   a.registry.GetVersion(),
		AgentID:   agent.ID,
		SessionID: sessionID,
		Data: map[string]any{
			"name":      name,
			"parent_id": parentID,
			"source":    string(source),
		},
	})

	return &Response{
		Status:  StatusOK,
		Version: agent.LastVersion,
		AgentID: agent.ID,
	}, nil
}

// UnregisterAgent removes an agent from the registry
func (a *Archivalist) UnregisterAgent(agentID string) error {
	agent := a.registry.Get(agentID)
	if agent == nil {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Log the event
	a.eventLog.Append(&Event{
		Type:      EventTypeAgentUnregister,
		Version:   a.registry.GetVersion(),
		AgentID:   agentID,
		SessionID: agent.SessionID,
	})

	return a.registry.Unregister(agentID)
}

// HandleRequest processes a protocol request and returns a response
func (a *Archivalist) HandleRequest(ctx context.Context, req *Request) Response {
	switch req.GetMessageType() {
	case MsgTypeRegister:
		return a.handleRegister(ctx, req)
	case MsgTypeWrite:
		return a.handleWrite(ctx, req)
	case MsgTypeBatch:
		return a.handleBatch(ctx, req)
	case MsgTypeRead:
		return a.handleRead(ctx, req)
	case MsgTypeBriefing:
		return a.handleBriefing(ctx, req)
	default:
		return ErrorResponse("unknown request type")
	}
}

func (a *Archivalist) handleRegister(ctx context.Context, req *Request) Response {
	if req.Register == nil {
		return ErrorResponse("missing register data")
	}

	resp, err := a.RegisterAgent(
		req.Register.Name,
		req.Register.Session,
		req.Register.ParentID,
		SourceModelClaudeOpus45, // Default source
	)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	return *resp
}

func (a *Archivalist) handleWrite(ctx context.Context, req *Request) Response {
	if err := a.validateWriteRequest(req); err != nil {
		return ErrorResponse(err.Error())
	}

	a.registry.Touch(req.AgentID)
	scope, key := ParseScope(req.Write.Scope)

	if resp := a.checkVersionConflict(req); resp != nil {
		return *resp
	}

	conflictResult := a.detectAndRecordConflict(scope, key, req)
	if !conflictResult.Resolved {
		return ConflictResponse(a.registry.GetVersion(), string(conflictResult.Type), conflictResult.Message)
	}

	return a.executeWrite(ctx, scope, key, req, conflictResult)
}

func (a *Archivalist) validateWriteRequest(req *Request) error {
	if req.Write == nil {
		return fmt.Errorf("missing write data")
	}
	if req.AgentID == "" {
		return fmt.Errorf("agent_id required")
	}
	return nil
}

func (a *Archivalist) checkVersionConflict(req *Request) *Response {
	if req.Write.ExpectNoConflict || req.Version == "" {
		return nil
	}
	if a.registry.IsVersionCurrent(req.Version) {
		return nil
	}
	delta, _ := a.registry.GetVersionDelta(req.Version)
	resp := ConflictResponse(a.registry.GetVersion(), string(ConflictTypeVersion), fmt.Sprintf("version behind by %d", delta))
	return &resp
}

func (a *Archivalist) detectAndRecordConflict(scope Scope, key string, req *Request) *ConflictResult {
	result := a.conflictDetector.DetectConflict(scope, key, req.Write.Data, req.Version, req.AgentID)
	if result.Type != ConflictTypeNone {
		a.recordConflict(scope, key, req, result)
	}
	return result
}

func (a *Archivalist) recordConflict(scope Scope, key string, req *Request, result *ConflictResult) {
	a.conflictHistory.Record(&ConflictRecord{
		ID: uuid.New().String(), DetectedAt: time.Now(), Type: result.Type,
		Scope: scope, Key: key, AgentID: req.AgentID, BaseVersion: req.Version,
		Result: result, AutoResolved: result.Resolved,
	})
}

func (a *Archivalist) executeWrite(ctx context.Context, scope Scope, key string, req *Request, result *ConflictResult) Response {
	finalData := a.conflictDetector.Resolve(result, nil, req.Write.Data)
	if err := a.applyWrite(ctx, scope, key, finalData, req.AgentID); err != nil {
		return ErrorResponse(err.Error())
	}

	newVersion := a.registry.IncrementVersion()
	a.registry.UpdateVersion(req.AgentID, newVersion)
	a.logWriteEvent(scope, key, finalData, req.AgentID, newVersion)

	if result.Strategy == ResolutionMerge || result.Strategy == ResolutionCombine {
		return MergedResponse(newVersion, result.Message)
	}
	return OKResponse(newVersion)
}

func (a *Archivalist) logWriteEvent(scope Scope, key string, data map[string]any, agentID, version string) {
	a.eventLog.Append(&Event{
		Type: scopeToEventType(scope, false), Version: version,
		Clock: a.registry.GetGlobalClock(), AgentID: agentID,
		SessionID: a.store.GetCurrentSession().ID, Scope: scope, Key: key, Data: data,
	})
}

func (a *Archivalist) handleBatch(ctx context.Context, req *Request) Response {
	if req.Batch == nil || len(req.Batch.Writes) == 0 {
		return ErrorResponse("missing batch data")
	}
	if req.AgentID == "" {
		return ErrorResponse("agent_id required")
	}

	a.registry.Touch(req.AgentID)

	succeeded := 0
	failed := 0
	var lastVersion string

	for _, write := range req.Batch.Writes {
		writeReq := &Request{
			AgentID: req.AgentID,
			Version: req.Version,
			Write:   &write,
		}
		resp := a.handleWrite(ctx, writeReq)
		if resp.Status == StatusOK || resp.Status == StatusMerged {
			succeeded++
			lastVersion = resp.Version
			// Update version for next write in batch
			req.Version = resp.Version
		} else {
			failed++
		}
	}

	if failed == 0 {
		return BatchOKResponse(lastVersion, succeeded)
	}
	return PartialResponse(lastVersion, succeeded, failed)
}

func (a *Archivalist) handleRead(ctx context.Context, req *Request) Response {
	if req.Read == nil {
		return ErrorResponse("missing read data")
	}

	if req.AgentID != "" {
		a.registry.Touch(req.AgentID)
	}

	// Delta read
	if req.Read.Since != "" {
		delta := a.eventLog.GetDelta(req.Read.Since, req.Read.Limit)
		return Response{
			Status:  StatusOK,
			Version: a.registry.GetVersion(),
			Delta:   delta,
		}
	}

	// Full read (scoped)
	scope, key := ParseScope(req.Read.Scope)
	data := a.readScope(scope, key, req.Read.Limit)

	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Data:    data,
	}
}

func (a *Archivalist) handleBriefing(ctx context.Context, req *Request) Response {
	if req.Briefing == nil {
		return ErrorResponse("missing briefing data")
	}

	if req.AgentID != "" {
		a.registry.Touch(req.AgentID)
	}

	tier := req.Briefing.Tier
	if tier == "" {
		tier = BriefingStandard
	}

	switch tier {
	case BriefingMicro:
		return a.getMicroBriefing()
	case BriefingStandard:
		return a.getStandardBriefing()
	case BriefingFull:
		return a.getFullBriefing()
	default:
		return a.getStandardBriefing()
	}
}

func (a *Archivalist) getMicroBriefing() Response {
	resume := a.agentContext.GetResumeState()
	if resume == nil {
		return Response{
			Status:   StatusOK,
			Version:  a.registry.GetVersion(),
			Briefing: "no-task:0/0:none:block=none",
		}
	}

	modFiles := a.agentContext.GetModifiedFiles()
	modPaths := make([]string, len(modFiles))
	for i, f := range modFiles {
		modPaths[i] = f.Path
	}

	blocker := ""
	if len(resume.Blockers) > 0 {
		blocker = resume.Blockers[0]
	}

	totalSteps := len(resume.CompletedSteps) + len(resume.NextSteps)
	briefing := MicroBriefing(
		resume.CurrentTask,
		len(resume.CompletedSteps),
		totalSteps,
		modPaths,
		blocker,
	)

	return Response{
		Status:   StatusOK,
		Version:  a.registry.GetVersion(),
		Briefing: briefing,
	}
}

func (a *Archivalist) getStandardBriefing() Response {
	briefing := a.agentContext.GetAgentBriefing()
	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Data:    briefing,
	}
}

func (a *Archivalist) getFullBriefing() Response {
	briefing := a.agentContext.GetAgentBriefing()
	snapshot := a.GetSnapshot(context.Background())

	fullBriefing := map[string]any{
		"agent_briefing": briefing,
		"snapshot":       snapshot,
		"registry_stats": a.registry.GetStats(),
		"event_stats":    a.eventLog.Stats(),
		"conflicts":      a.conflictHistory.GetUnresolved(),
	}

	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Data:    fullBriefing,
	}
}

func (a *Archivalist) applyWrite(ctx context.Context, scope Scope, key string, data map[string]any, agentID string) error {
	agent := a.registry.Get(agentID)
	source := SourceModelArchivalist
	if agent != nil {
		source = agent.Source
	}

	switch scope {
	case ScopeFiles:
		return a.applyFileWrite(key, data, source)
	case ScopePatterns:
		return a.applyPatternWrite(key, data, source, agentID)
	case ScopeFailures:
		return a.applyFailureWrite(data, source)
	case ScopeIntents:
		return a.applyIntentWrite(data)
	case ScopeResume:
		return a.applyResumeWrite(data, source)
	default:
		return fmt.Errorf("unknown scope: %s", scope)
	}
}

func (a *Archivalist) applyFileWrite(path string, data map[string]any, source SourceModel) error {
	status, _ := data["status"].(string)
	summary, _ := data["summary"].(string)

	switch FileStatus(status) {
	case FileStatusRead:
		a.agentContext.RecordFileRead(path, summary, source)
	case FileStatusModified:
		changes, _ := data["changes"].([]any)
		for _, ch := range changes {
			if chMap, ok := ch.(map[string]any); ok {
				startLine, _ := chMap["start_line"].(float64)
				endLine, _ := chMap["end_line"].(float64)
				desc, _ := chMap["description"].(string)
				a.agentContext.RecordFileModified(path, FileChange{
					StartLine:   int(startLine),
					EndLine:     int(endLine),
					Description: desc,
				}, source)
			}
		}
	case FileStatusCreated:
		a.agentContext.RecordFileCreated(path, summary, source)
	}
	return nil
}

func (a *Archivalist) applyPatternWrite(category string, data map[string]any, source SourceModel, agentID string) error {
	name, _ := data["name"].(string)
	description, _ := data["description"].(string)
	example, _ := data["example"].(string)
	pattern, _ := data["pattern"].(string)

	if pattern != "" {
		description = pattern
	}

	a.agentContext.RegisterPattern(&Pattern{
		Category:      category,
		Name:          name,
		Description:   description,
		Example:       example,
		Source:        source,
		EstablishedBy: agentID,
	})
	return nil
}

func (a *Archivalist) applyFailureWrite(data map[string]any, source SourceModel) error {
	approach, _ := data["approach"].(string)
	reason, _ := data["reason"].(string)
	taskContext, _ := data["context"].(string)
	resolution, _ := data["resolution"].(string)

	if resolution != "" {
		a.agentContext.RecordFailureWithResolution(approach, reason, taskContext, resolution, source)
	} else {
		a.agentContext.RecordFailure(approach, reason, taskContext, source)
	}
	return nil
}

func (a *Archivalist) applyIntentWrite(data map[string]any) error {
	intentType, _ := data["type"].(string)
	description, _ := data["description"].(string)
	priority, _ := data["priority"].(string)
	source, _ := data["source"].(string)

	if intentType == "want" {
		a.agentContext.RecordUserWants(description, priority, source)
	} else {
		a.agentContext.RecordUserRejects(description, source)
	}
	return nil
}

func (a *Archivalist) applyResumeWrite(data map[string]any, source SourceModel) error {
	a.applyTaskUpdate(data, source)
	a.applyCompletedSteps(data)
	a.applyNextSteps(data)
	a.applyBlockers(data)
	return nil
}

func (a *Archivalist) applyTaskUpdate(data map[string]any, source SourceModel) {
	task, _ := data["current_task"].(string)
	if task != "" {
		objective, _ := data["objective"].(string)
		a.agentContext.SetCurrentTask(task, objective, source)
	}
}

func (a *Archivalist) applyCompletedSteps(data map[string]any) {
	steps := extractStringSlice(data, "completed_steps")
	for _, step := range steps {
		a.agentContext.CompleteStep(step)
	}
}

func (a *Archivalist) applyNextSteps(data map[string]any) {
	steps := extractStringSlice(data, "next_steps")
	if len(steps) > 0 {
		a.agentContext.SetNextSteps(steps)
	}
}

func (a *Archivalist) applyBlockers(data map[string]any) {
	blockers := extractStringSlice(data, "blockers")
	for _, blocker := range blockers {
		a.agentContext.AddBlocker(blocker)
	}
}

func extractStringSlice(data map[string]any, key string) []string {
	items, ok := data[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func (a *Archivalist) readScope(scope Scope, key string, limit int) any {
	switch scope {
	case ScopeFiles:
		if key != "" {
			return a.agentContext.GetFileState(key)
		}
		return a.agentContext.GetModifiedFiles()
	case ScopePatterns:
		if key != "" {
			return a.agentContext.GetPattern(key)
		}
		return a.agentContext.GetAllPatterns()
	case ScopeFailures:
		return a.agentContext.GetRecentFailures(limit)
	case ScopeIntents:
		return map[string]any{
			"wants":   a.agentContext.GetUserWants(),
			"rejects": a.agentContext.GetUserRejects(),
		}
	case ScopeResume:
		return a.agentContext.GetResumeState()
	case ScopeAll:
		return a.agentContext.GetAgentBriefing()
	default:
		return nil
	}
}

func scopeToEventType(scope Scope, isNew bool) EventType {
	switch scope {
	case ScopeFiles:
		if isNew {
			return EventTypeFileCreate
		}
		return EventTypeFileModify
	case ScopePatterns:
		if isNew {
			return EventTypePatternAdd
		}
		return EventTypePatternUpdate
	case ScopeFailures:
		return EventTypeFailureRecord
	case ScopeIntents:
		return EventTypeIntentAdd
	case ScopeResume:
		return EventTypeResumeUpdate
	default:
		return EventTypeEntryStore
	}
}

// =============================================================================
// Concurrency Accessors
// =============================================================================

// GetRegistry returns the agent registry
func (a *Archivalist) GetRegistry() *Registry {
	return a.registry
}

// GetEventLog returns the event log
func (a *Archivalist) GetEventLog() *EventLog {
	return a.eventLog
}

// GetConflictHistory returns the conflict history
func (a *Archivalist) GetConflictHistory() *ConflictHistory {
	return a.conflictHistory
}

// GetActiveAgents returns all currently active agents
func (a *Archivalist) GetActiveAgents() []*RegisteredAgent {
	return a.registry.GetActiveAgents()
}

// GetEventsSince returns events since a given version
func (a *Archivalist) GetEventsSince(version string) []*Event {
	return a.eventLog.GetSinceVersion(version)
}

// GetUnresolvedConflicts returns conflicts awaiting human resolution
func (a *Archivalist) GetUnresolvedConflicts() []*ConflictRecord {
	return a.conflictHistory.GetUnresolved()
}

// =============================================================================
// RAG Methods
// =============================================================================

// QueryContext performs a RAG query and returns synthesized answer
func (a *Archivalist) QueryContext(ctx context.Context, query string) (*SynthesisResponse, error) {
	if a.synthesizer == nil {
		return nil, fmt.Errorf("RAG not enabled")
	}

	sessionID := a.store.GetCurrentSession().ID
	queryType := ClassifyQuery(query)

	return a.synthesizer.Answer(ctx, query, sessionID, queryType)
}

// HandleToolCall processes a tool call from an agent
func (a *Archivalist) HandleToolCall(ctx context.Context, toolName string, input []byte) (string, error) {
	if a.toolHandler == nil {
		return "", fmt.Errorf("tool handler not initialized")
	}

	return a.toolHandler.Handle(ctx, toolName, input)
}

// GetQueryCacheStats returns query cache statistics
func (a *Archivalist) GetQueryCacheStats() *QueryCacheStats {
	if a.queryCache == nil {
		return nil
	}
	stats := a.queryCache.Stats()
	return &stats
}

// GetMemoryStats returns memory manager statistics
func (a *Archivalist) GetMemoryStats() *MemoryStats {
	if a.memory == nil {
		return nil
	}
	stats := a.memory.Stats()
	return &stats
}

// GetToolDefinitions returns all available tool definitions
func (a *Archivalist) GetToolDefinitions() []ToolDefinition {
	return AllToolDefinitions()
}

// IndexContent adds content to the RAG retrieval index
func (a *Archivalist) IndexContent(ctx context.Context, id, content, category, contentType string) error {
	if a.retriever == nil {
		return fmt.Errorf("RAG not enabled")
	}

	sessionID := a.store.GetCurrentSession().ID
	return a.retriever.Index(ctx, id, content, category, contentType, sessionID, nil)
}

// InvalidateQueryCache invalidates cached queries by type
func (a *Archivalist) InvalidateQueryCache(queryType QueryType) {
	if a.queryCache != nil {
		a.queryCache.InvalidateByType(queryType)
	}
}

// CleanupRAG performs cleanup of RAG components
func (a *Archivalist) CleanupRAG() {
	if a.queryCache != nil {
		a.queryCache.Cleanup()
	}
}

// =============================================================================
// Skills and Hooks API
// =============================================================================

// Skills returns the Archivalist's skill registry
func (a *Archivalist) Skills() *skills.Registry {
	return a.skills
}

// SkillLoader returns the Archivalist's skill loader
func (a *Archivalist) SkillLoader() *skills.Loader {
	return a.skillLoader
}

// Hooks returns the Archivalist's hook registry
func (a *Archivalist) Hooks() *skills.HookRegistry {
	return a.hooks
}

// RegisterSkill registers a skill with the Archivalist's skill registry
func (a *Archivalist) RegisterSkill(skill *skills.Skill) error {
	return a.skills.Register(skill)
}

// LoadSkillsForInput loads skills based on input keywords
func (a *Archivalist) LoadSkillsForInput(input string) []string {
	return a.skillLoader.LoadForInput(input)
}

// GetLoadedSkillDefinitions returns tool definitions for all loaded skills
func (a *Archivalist) GetLoadedSkillDefinitions() []map[string]any {
	return a.skills.GetToolDefinitions()
}

// RegisterPrePromptHook registers a hook that runs before LLM prompts
func (a *Archivalist) RegisterPrePromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	a.hooks.RegisterPrePromptHook(name, priority, fn)
}

// RegisterPostPromptHook registers a hook that runs after LLM responses
func (a *Archivalist) RegisterPostPromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	a.hooks.RegisterPostPromptHook(name, priority, fn)
}

// ExecutePrePromptHooks runs all pre-prompt hooks
func (a *Archivalist) ExecutePrePromptHooks(ctx context.Context, data *skills.PromptHookData) (*skills.PromptHookData, error) {
	return a.hooks.ExecutePrePromptHooks(ctx, data)
}

// ExecutePostPromptHooks runs all post-prompt hooks
func (a *Archivalist) ExecutePostPromptHooks(ctx context.Context, data *skills.PromptHookData) (*skills.PromptHookData, error) {
	return a.hooks.ExecutePostPromptHooks(ctx, data)
}

// registerCoreSkills registers the archivalist's core skills
func (a *Archivalist) registerCoreSkills() {
	// Store skill - stores entries in the chronicle
	storeSkill := skills.NewSkill("store").
		Description("Store information in the chronicle. Use this to record decisions, insights, patterns, failures, and other important information.").
		Domain("chronicle").
		Keywords("store", "save", "record", "remember", "log").
		Priority(100).
		StringParam("content", "The content to store", true).
		EnumParam("category", "Category of the entry", []string{
			"decision", "insight", "pattern", "failure", "task_state",
			"timeline", "user_voice", "hypothesis", "open_thread", "general",
		}, true).
		StringParam("title", "Optional title for the entry", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Content  string `json:"content"`
				Category string `json:"category"`
				Title    string `json:"title"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			entry := &Entry{
				Content:  params.Content,
				Title:    params.Title,
				Category: Category(params.Category),
				Source:   SourceModelArchivalist,
			}
			result := a.StoreEntry(ctx, entry)
			return result, result.Error
		}).
		Build()

	// Query skill - retrieves entries from the chronicle
	querySkill := skills.NewSkill("query").
		Description("Query the chronicle for stored information. Use this to recall decisions, patterns, failures, and other recorded information.").
		Domain("chronicle").
		Keywords("query", "find", "search", "recall", "retrieve", "what", "when", "how").
		Priority(100).
		StringParam("search", "Text to search for", false).
		EnumParam("category", "Filter by category", []string{
			"decision", "insight", "pattern", "failure", "task_state",
			"timeline", "user_voice", "hypothesis", "open_thread", "general", "all",
		}, false).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Search   string `json:"search"`
				Category string `json:"category"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.Limit == 0 {
				params.Limit = 10
			}

			query := ArchiveQuery{
				SearchText: params.Search,
				Limit:      params.Limit,
			}

			if params.Category != "" && params.Category != "all" {
				query.Categories = []Category{Category(params.Category)}
			}

			return a.store.Query(query)
		}).
		Build()

	// Briefing skill - gets agent briefing for context
	briefingSkill := skills.NewSkill("briefing").
		Description("Get a briefing of the current session state. Use this to understand current task, progress, blockers, and context.").
		Domain("memory").
		Keywords("briefing", "status", "context", "state", "progress", "current").
		Priority(90).
		EnumParam("tier", "Level of detail", []string{"micro", "standard", "full"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Tier string `json:"tier"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			switch params.Tier {
			case "micro":
				return a.getMicroBriefing().Data, nil
			case "full":
				return a.getFullBriefing().Data, nil
			default:
				return a.getStandardBriefing().Data, nil
			}
		}).
		Build()

	// Register and load core skills
	a.skills.Register(storeSkill)
	a.skills.Register(querySkill)
	a.skills.Register(briefingSkill)

	a.skills.Load("store")
	a.skills.Load("query")
	a.skills.Load("briefing")
}
