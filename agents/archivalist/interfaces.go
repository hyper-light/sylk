package archivalist

import (
	"context"
	"encoding/json"

	"github.com/adalundhe/sylk/core/skills"
)

type ArchivalistService interface {
	Close() error
	StoreEntry(ctx context.Context, entry *Entry) SubmissionResult
	UpdateEntry(ctx context.Context, id string, updates func(*Entry)) error
	GetEntry(ctx context.Context, id string) (*Entry, bool)
	Query(ctx context.Context, query ArchiveQuery) ([]*Entry, error)
	QueryByCategory(ctx context.Context, category Category, limit int) []*Entry
	SearchText(ctx context.Context, text string, includeArchived bool, limit int) ([]*Entry, error)
	RestoreFromArchive(ctx context.Context, ids []string) error
	QueryCrossSession(ctx context.Context, query ArchiveQuery) ([]CrossSessionResult, error)
	QuerySessions(ctx context.Context, query ArchiveQuery) ([]*Session, error)
	GetSessionHistory(ctx context.Context, sessionID string, limit int) []*Event
	GetTokenSavings(sessionID string) TokenSavingsReport
	GetGlobalTokenSavings() TokenSavingsReport

	StoreTaskState(ctx context.Context, content string, source SourceModel) SubmissionResult
	StoreDecision(ctx context.Context, choice, rationale string, source SourceModel) SubmissionResult
	StoreIssue(ctx context.Context, problem string, source SourceModel) SubmissionResult
	StoreTimelineEvent(ctx context.Context, content string, source SourceModel) SubmissionResult
	StoreInsight(ctx context.Context, content string, source SourceModel) SubmissionResult
	StoreUserVoice(ctx context.Context, content string) SubmissionResult
	StoreHypothesis(ctx context.Context, content string, source SourceModel) SubmissionResult
	StoreOpenThread(ctx context.Context, content string, source SourceModel) SubmissionResult

	GenerateSummary(ctx context.Context, content string) (*Entry, error)
	GenerateSummaryFromEntries(ctx context.Context, query ArchiveQuery) (*Entry, error)

	GetSnapshot(ctx context.Context) *ChronicleSnapshot
	EndSession(ctx context.Context, summary string, primaryFocus string) error
	GetCurrentSession() *Session
	GetDefaultSession() string
	SetDefaultSession(sessionID string)
	GetRecentSessions(ctx context.Context, limit int) ([]*Session, error)
	Stats() StorageStats

	SubmitSummary(ctx context.Context, content string, source SourceModel, metadata map[string]any) SubmissionResult
	SubmitPromptResponse(ctx context.Context, prompt, response string, source SourceModel, metadata map[string]any) SubmissionResult

	RecordFileRead(path, summary string, agent SourceModel)
	RecordFileModified(path string, startLine, endLine int, description string, agent SourceModel)
	RecordFileCreated(path, summary string, agent SourceModel)
	WasFileRead(path string) bool
	GetFileState(path string) (*FileState, bool)
	GetModifiedFiles() []*FileState

	RegisterPattern(category, name, description, example string, agent SourceModel)
	GetPatterns() []*Pattern
	GetPatternsByCategory(category string) []*Pattern

	RecordFailure(approach, reason, taskContext string, agent SourceModel)
	RecordFailureWithResolution(approach, reason, taskContext, resolution string, agent SourceModel)
	CheckFailure(approach string) (*Failure, bool)
	GetRecentFailures(limit int) []*Failure

	RecordUserWants(content, priority, source string)
	RecordUserRejects(content, source string)
	GetUserWants() []*Intent
	GetUserRejects() []*Intent

	SetCurrentTask(task, objective string, agent SourceModel)
	CompleteStep(step string)
	SetNextSteps(steps []string)
	AddBlocker(blocker string)
	RemoveBlocker(blocker string)
	GetResumeState() *ResumeState
	GetAgentBriefing() *AgentBriefing

	RegisterAgent(name, sessionID, parentID string, source SourceModel) (*Response, error)
	UnregisterAgent(agentID string) error
	HandleRequest(ctx context.Context, req *Request) Response

	GetQueryCache() QueryCacheService
	GetEmbeddings() EmbeddingStoreService
	GetRetriever() SemanticRetrieverService
	GetSynthesizer() SynthesizerService
	GetMemory() MemoryManagerService
	GetToolHandler() ToolHandlerService
	GetRegistry() RegistryService
	GetEventLog() EventLogService
	GetUnresolvedConflicts() []*ConflictRecord

	RegisterPreStoreHook(name string, priority skills.HookPriority, fn skills.StoreHookFunc)
	RegisterPostStoreHook(name string, priority skills.HookPriority, fn skills.StoreHookFunc)
	RegisterPreQueryHook(name string, priority skills.HookPriority, fn skills.QueryHookFunc)
	RegisterPostQueryHook(name string, priority skills.HookPriority, fn skills.QueryHookFunc)
	ExecutePreStoreHooks(ctx context.Context, data *skills.StoreHookData) (*skills.StoreHookData, skills.HookResult, error)
	ExecutePostStoreHooks(ctx context.Context, data *skills.StoreHookData) (*skills.StoreHookData, skills.HookResult, error)
	ExecutePreQueryHooks(ctx context.Context, data *skills.QueryHookData) (*skills.QueryHookData, skills.HookResult, error)
	ExecutePostQueryHooks(ctx context.Context, data *skills.QueryHookData) (*skills.QueryHookData, skills.HookResult, error)
}

type StoreService interface {
	GetCurrentSession() *Session
	EndSession(summary string, primaryFocus string) error
	InsertEntry(entry *Entry) (string, error)
	InsertEntryInSession(sessionID string, entry *Entry) (string, error)
	UpdateEntry(id string, updates func(*Entry)) error
	GetEntry(id string) (*Entry, bool)
	Query(q ArchiveQuery) ([]*Entry, error)
	QueryByCategory(category Category, limit int) []*Entry
	SearchText(text string, includeArchived bool, limit int) ([]*Entry, error)
	RestoreFromArchive(ids []string) error
	Stats() StorageStats
}

type ArchiveService interface {
	Close() error
	ArchiveEntries(entries []*Entry) error
	GetEntry(id string) (*Entry, error)
	Query(q ArchiveQuery) ([]*Entry, error)
	SearchText(text string, limit int) ([]*Entry, error)
	SaveSession(session *Session) error
	GetSession(id string) (*Session, error)
	GetRecentSessions(limit int) ([]*Session, error)
	Stats() (map[string]int, error)
}

type ClientService interface {
	GenerateSummary(ctx context.Context, content string) (*GeneratedSummary, error)
	GenerateSummaryFromSubmissions(ctx context.Context, submissions []Submission) (*GeneratedSummary, error)
}

type RegistryService interface {
	Register(name, sessionID, parentID string, source SourceModel) (*RegisteredAgent, error)
	Unregister(id string) error
	Get(id string) *RegisteredAgent
	GetByName(name string) *RegisteredAgent
	GetBySession(sessionID string) []*RegisteredAgent
	Touch(id string) error
	UpdateVersion(id, version string) error
	IncrementGlobalClock() uint64
	GetGlobalClock() uint64
	SyncClock(id string, incomingClock uint64) uint64
	IncrementVersion() string
	GetVersion() string
	GetVersionNumber() uint64
	ParseVersion(v string) (uint64, error)
	IsVersionCurrent(v string) bool
	GetVersionDelta(v string) (int64, error)
	UpdateStatuses()
	GetActiveAgents() []*RegisteredAgent
	GetAgentHierarchy(id string) []*RegisteredAgent
	GetRootAgent(id string) *RegisteredAgent
	GetDescendants(id string) []*RegisteredAgent
	GetStats() RegistryStats
}

type EventLogService interface {
	Close()
	Append(event *Event) error
	Get(id string) *Event
	GetByVersion(version string) *Event
	GetRecent(n int) []*Event
	GetSinceVersion(version string) []*Event
	GetSinceClock(clock uint64) []*Event
	GetByAgent(agentID string, limit int) []*Event
	GetBySession(sessionID string, limit int) []*Event
	GetByType(eventType EventType, limit int) []*Event
	GetByScope(scope Scope, limit int) []*Event
	GetDelta(sinceVersion string, limit int) []DeltaEntry
	Query(q EventQuery) []*Event
	QueryWithContext(ctx context.Context, q EventQuery) ([]*Event, error)
	Len() int
	LastClock() uint64
	Stats() EventStats
}

type ConflictDetectorService interface {
	DetectConflict(scope Scope, key string, incoming map[string]any, version, agentID string) *ConflictResult
	Resolve(result *ConflictResult, existing, incoming map[string]any) map[string]any
}

type QueryCacheService interface {
	Get(ctx context.Context, query string, sessionID string) (*CachedResponse, bool)
	Store(ctx context.Context, query string, sessionID string, response []byte, queryType QueryType) error
	InvalidateBySession(sessionID string)
	InvalidateByType(queryType QueryType)
	Cleanup()
	Stats() QueryCacheStats
	StatsBySession(sessionID string) QueryCacheStats
}

type EmbeddingStoreService interface {
	Store(entry *EmbeddingEntry, embedding []float32) error
	Get(id string) (*EmbeddingEntry, []float32, bool)
	SearchSimilar(query []float32, opts SearchOptions) ([]*ScoredEntry, error)
	DeleteBySession(sessionID string) error
	Stats() EmbeddingStoreStats
	Close() error
}

// SemanticRetrieverService defines the semantic retrieval interface
type SemanticRetrieverService interface {
	Close() error
	Retrieve(ctx context.Context, query string, opts RetrievalOptions) ([]*RetrievalResult, error)
	Index(ctx context.Context, id, content, category, contentType, sessionID string, metadata map[string]any) error
	Delete(ctx context.Context, id string) error
}

// SynthesizerService defines the synthesis interface
type SynthesizerService interface {
	Answer(ctx context.Context, query string, sessionID string, queryType QueryType) (*SynthesisResponse, error)
	AnswerWithContext(ctx context.Context, query string, contextResults []*RetrievalResult) (*SynthesisResponse, error)
	SynthesizePatterns(ctx context.Context, patterns []*Pattern, query string) (*SynthesisResponse, error)
	SynthesizeFailures(ctx context.Context, failures []*Failure, query string) (*SynthesisResponse, error)
	SynthesizeBriefing(ctx context.Context, briefing *AgentBriefing, query string) (*SynthesisResponse, error)
}

// MemoryManagerService defines the memory management interface
type MemoryManagerService interface {
	Add(item *MemoryItem) error
	Get(id string) (*MemoryItem, bool)
	Promote(id string)
	Demote(id string)
	Remove(id string)
	Stats() MemoryStats
}

// ToolHandlerService defines the tool handler interface
type ToolHandlerService interface {
	Handle(ctx context.Context, toolName string, input json.RawMessage) (string, error)
}

// AgentContextService defines the agent context interface
type AgentContextService interface {
	// File tracking
	RecordFileRead(path, summary string, agent SourceModel)
	RecordFileModified(path string, change FileChange, agent SourceModel)
	RecordFileCreated(path, summary string, agent SourceModel)
	GetFileState(path string) *FileState
	GetFileStateWithCheck(path string) (*FileState, bool)
	GetAllFiles() map[string]*FileState
	GetModifiedFiles() []*FileState
	WasFileRead(path string) bool

	// Pattern tracking
	RegisterPattern(p *Pattern)
	GetPattern(idOrCategory string) *Pattern
	GetPatternByID(id string) (*Pattern, bool)
	GetPatternsByCategory(category string) []*Pattern
	GetAllPatterns() []*Pattern

	// Failure tracking
	RecordFailure(approach, reason, context string, agent SourceModel) *Failure
	RecordFailureWithResolution(approach, reason, context, resolution string, agent SourceModel) *Failure
	GetAllFailures() []*Failure
	GetRecentFailures(limit int) []*Failure
	CheckFailure(approach string) (*Failure, bool)

	// Intent tracking
	RecordIntent(intentType IntentType, content, priority, source string) *Intent
	RecordUserWants(content, priority, source string) *Intent
	RecordUserRejects(content, source string) *Intent
	GetAllIntents() []*Intent
	GetIntentsByType(intentType IntentType) []*Intent
	GetUserWants() []*Intent
	GetUserRejects() []*Intent

	// Resume state
	UpdateResumeState(update func(*ResumeState))
	SetCurrentTask(task, objective string, agent SourceModel)
	CompleteStep(step string)
	SetCurrentStep(step string)
	SetNextSteps(steps []string)
	AddBlocker(blocker string)
	RemoveBlocker(blocker string)
	SetFilesToRead(files []string)
	GetResumeState() *ResumeState
	GetAgentBriefing() *AgentBriefing
}

// ConflictHistoryService defines the conflict history interface
type ConflictHistoryService interface {
	Record(record *ConflictRecord)
	GetRecent(n int) []*ConflictRecord
	GetUnresolved() []*ConflictRecord
}

// Ensure structs implement their interfaces (compile-time check)
var (
	_ StoreService             = (*Store)(nil)
	_ ArchiveService           = (*Archive)(nil)
	_ ClientService            = (*Client)(nil)
	_ RegistryService          = (*Registry)(nil)
	_ EventLogService          = (*EventLog)(nil)
	_ ConflictDetectorService  = (*ConflictDetector)(nil)
	_ QueryCacheService        = (*QueryCache)(nil)
	_ EmbeddingStoreService    = (*EmbeddingStore)(nil)
	_ SemanticRetrieverService = (*SemanticRetriever)(nil)
	_ SynthesizerService       = (*Synthesizer)(nil)
	_ MemoryManagerService     = (*MemoryManager)(nil)
	_ ToolHandlerService       = (*ToolHandler)(nil)
	_ AgentContextService      = (*AgentContext)(nil)
	_ ConflictHistoryService   = (*ConflictHistory)(nil)
)
