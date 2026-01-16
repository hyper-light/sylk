package archivalist

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// FileStatus represents the state of a file in the current session
type FileStatus string

const (
	FileStatusRead     FileStatus = "read"
	FileStatusModified FileStatus = "modified"
	FileStatusCreated  FileStatus = "created"
	FileStatusDeleted  FileStatus = "deleted"
)

// FileState tracks what an agent knows about a file
type FileState struct {
	Path         string            `json:"path"`
	Status       FileStatus        `json:"status"`
	Summary      string            `json:"summary"`       // What this file does/contains
	KeyFunctions []string          `json:"key_functions"` // Important functions/classes
	Imports      []string          `json:"imports"`       // Dependencies
	Exports      []string          `json:"exports"`       // Public API
	ModifiedAt   time.Time         `json:"modified_at"`
	ReadAt       time.Time         `json:"read_at"`
	Changes      []FileChange      `json:"changes,omitempty"` // What was changed
	Agent        SourceModel       `json:"agent"`             // Which agent processed this
	ModifiedBy   string            `json:"modified_by"`       // Agent ID that last modified
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// FileChange represents a specific change made to a file
type FileChange struct {
	StartLine   int         `json:"start_line"`
	EndLine     int         `json:"end_line"`
	Lines       string      `json:"lines,omitempty"` // e.g., "45-60"
	Description string      `json:"description"`
	Timestamp   time.Time   `json:"timestamp"`
	Agent       SourceModel `json:"agent"`
	AgentID     string      `json:"agent_id,omitempty"`
}

// Pattern represents a coding pattern to follow consistently
type Pattern struct {
	ID            string      `json:"id"`
	Category      string      `json:"category"`    // "error_handling", "naming", "testing", etc.
	Name          string      `json:"name"`        // "Wrap errors with context"
	Pattern       string      `json:"pattern"`     // The pattern value/description
	Description   string      `json:"description"` // Detailed explanation
	Example       string      `json:"example"`     // Code example
	AntiPattern   string      `json:"anti_pattern,omitempty"` // What NOT to do
	Source        SourceModel `json:"source"`      // Who established this pattern
	EstablishedBy string      `json:"established_by,omitempty"` // Agent ID
	CreatedAt     time.Time   `json:"created_at"`
}

// Failure represents an approach that was tried and failed
type Failure struct {
	ID          string      `json:"id"`
	Approach    string      `json:"approach"`    // What was tried
	Reason      string      `json:"reason"`      // Why it failed
	Context     string      `json:"context"`     // What was being attempted
	FilesAffected []string  `json:"files_affected,omitempty"`
	Agent       SourceModel `json:"agent"`       // Who tried this
	Timestamp   time.Time   `json:"timestamp"`
	Resolution  string      `json:"resolution,omitempty"` // What worked instead
}

// Intent represents what the user actually wants
type Intent struct {
	ID          string      `json:"id"`
	Type        IntentType  `json:"type"`
	Content     string      `json:"content"`     // The actual intent/preference
	Priority    string      `json:"priority"`    // "critical", "important", "nice_to_have"
	Source      string      `json:"source"`      // Quote or paraphrase from user
	CreatedAt   time.Time   `json:"created_at"`
}

// IntentType categorizes user intents
type IntentType string

const (
	IntentTypeWant     IntentType = "want"      // User wants this
	IntentTypeReject   IntentType = "reject"    // User rejected this
	IntentTypePrefer   IntentType = "prefer"    // User prefers this approach
	IntentTypeAvoid    IntentType = "avoid"     // User wants to avoid this
	IntentTypeDeadline IntentType = "deadline"  // Time constraint
	IntentTypeScope    IntentType = "scope"     // Scope constraint (MVP, full, etc.)
)

// ResumeState captures everything needed to continue work
type ResumeState struct {
	CurrentTask     string       `json:"current_task"`
	TaskObjective   string       `json:"task_objective"`
	CompletedSteps  []string     `json:"completed_steps"`
	CurrentStep     string       `json:"current_step"`
	NextSteps       []string     `json:"next_steps"`
	Blockers        []string     `json:"blockers,omitempty"`
	FilesToRead     []string     `json:"files_to_read"`      // Start by reading these
	RelevantContext []string     `json:"relevant_context"`   // Entry IDs to load
	LastAgent       SourceModel  `json:"last_agent"`
	LastUpdate      time.Time    `json:"last_update"`
	Notes           string       `json:"notes,omitempty"`    // Any handoff notes
}

// AgentContext provides agent-specific context tracking
type AgentContext struct {
	mu sync.RWMutex

	// File state tracking
	files map[string]*FileState

	// Pattern registry
	patterns map[string]*Pattern

	// Failure memory
	failures []*Failure

	// User intents
	intents []*Intent

	// Current resume state
	resumeState *ResumeState
}

// NewAgentContext creates a new agent context tracker
func NewAgentContext() *AgentContext {
	return &AgentContext{
		files:    make(map[string]*FileState),
		patterns: make(map[string]*Pattern),
		failures: make([]*Failure, 0),
		intents:  make([]*Intent, 0),
		resumeState: &ResumeState{
			CompletedSteps: make([]string, 0),
			NextSteps:      make([]string, 0),
			FilesToRead:    make([]string, 0),
		},
	}
}

// File State Methods

// RecordFileRead records that a file has been read
func (ac *AgentContext) RecordFileRead(path, summary string, agent SourceModel) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if existing, ok := ac.files[path]; ok {
		existing.ReadAt = time.Now()
		existing.Summary = summary
		return
	}

	ac.files[path] = &FileState{
		Path:    path,
		Status:  FileStatusRead,
		Summary: summary,
		ReadAt:  time.Now(),
		Agent:   agent,
	}
}

// RecordFileModified records that a file has been modified
func (ac *AgentContext) RecordFileModified(path string, change FileChange, agent SourceModel) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	change.Timestamp = time.Now()
	change.Agent = agent

	if existing, ok := ac.files[path]; ok {
		existing.Status = FileStatusModified
		existing.ModifiedAt = time.Now()
		existing.Changes = append(existing.Changes, change)
		existing.Agent = agent // Update to reflect last modifying agent
		return
	}

	ac.files[path] = &FileState{
		Path:       path,
		Status:     FileStatusModified,
		ModifiedAt: time.Now(),
		Changes:    []FileChange{change},
		Agent:      agent,
	}
}

// RecordFileCreated records that a file has been created
func (ac *AgentContext) RecordFileCreated(path, summary string, agent SourceModel) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	ac.files[path] = &FileState{
		Path:       path,
		Status:     FileStatusCreated,
		Summary:    summary,
		ModifiedAt: time.Now(),
		Agent:      agent,
	}
}

// GetFileState returns the state of a file (nil if not found)
func (ac *AgentContext) GetFileState(path string) *FileState {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.files[path]
}

// GetFileStateWithCheck returns the state of a file with existence check
func (ac *AgentContext) GetFileStateWithCheck(path string) (*FileState, bool) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	fs, ok := ac.files[path]
	return fs, ok
}

// GetAllFiles returns all tracked files
func (ac *AgentContext) GetAllFiles() map[string]*FileState {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	result := make(map[string]*FileState, len(ac.files))
	for k, v := range ac.files {
		result[k] = v
	}
	return result
}

// GetModifiedFiles returns only modified/created files
func (ac *AgentContext) GetModifiedFiles() []*FileState {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.getModifiedFilesLocked()
}

// getModifiedFilesLocked returns modified files (caller must hold lock)
func (ac *AgentContext) getModifiedFilesLocked() []*FileState {
	var result []*FileState
	for _, fs := range ac.files {
		if fs.Status == FileStatusModified || fs.Status == FileStatusCreated {
			result = append(result, fs)
		}
	}
	return result
}

// WasFileRead checks if a file has already been read
func (ac *AgentContext) WasFileRead(path string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	_, ok := ac.files[path]
	return ok
}

// Pattern Methods

// RegisterPattern adds a new pattern to follow
func (ac *AgentContext) RegisterPattern(p *Pattern) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if p.ID == "" {
		p.ID = fmt.Sprintf("pat_%d", len(ac.patterns)+1)
	}
	p.CreatedAt = time.Now()
	ac.patterns[p.ID] = p
}

// GetPattern retrieves a pattern by ID or category
// When used with a category, returns the first matching pattern
func (ac *AgentContext) GetPattern(idOrCategory string) *Pattern {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	// Try by ID first
	if p, ok := ac.patterns[idOrCategory]; ok {
		return p
	}

	// Try by category
	for _, p := range ac.patterns {
		if p.Category == idOrCategory {
			return p
		}
	}

	return nil
}

// GetPatternByID retrieves a pattern by ID with existence check
func (ac *AgentContext) GetPatternByID(id string) (*Pattern, bool) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	p, ok := ac.patterns[id]
	return p, ok
}

// GetPatternsByCategory retrieves patterns by category
func (ac *AgentContext) GetPatternsByCategory(category string) []*Pattern {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	var result []*Pattern
	for _, p := range ac.patterns {
		if p.Category == category {
			result = append(result, p)
		}
	}
	return result
}

// GetAllPatterns returns all registered patterns
func (ac *AgentContext) GetAllPatterns() []*Pattern {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.getAllPatternsLocked()
}

// getAllPatternsLocked returns all patterns (caller must hold lock)
func (ac *AgentContext) getAllPatternsLocked() []*Pattern {
	result := make([]*Pattern, 0, len(ac.patterns))
	for _, p := range ac.patterns {
		result = append(result, p)
	}
	return result
}

// Failure Methods

// RecordFailure records an approach that failed
func (ac *AgentContext) RecordFailure(approach, reason, context string, agent SourceModel) *Failure {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	f := &Failure{
		ID:        fmt.Sprintf("fail_%d", len(ac.failures)+1),
		Approach:  approach,
		Reason:    reason,
		Context:   context,
		Agent:     agent,
		Timestamp: time.Now(),
	}
	ac.failures = append(ac.failures, f)
	return f
}

// RecordFailureWithResolution records a failure and what worked instead
func (ac *AgentContext) RecordFailureWithResolution(approach, reason, context, resolution string, agent SourceModel) *Failure {
	f := ac.RecordFailure(approach, reason, context, agent)
	f.Resolution = resolution
	return f
}

// GetAllFailures returns all recorded failures
func (ac *AgentContext) GetAllFailures() []*Failure {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	result := make([]*Failure, len(ac.failures))
	copy(result, ac.failures)
	return result
}

// GetRecentFailures returns the most recent failures
func (ac *AgentContext) GetRecentFailures(limit int) []*Failure {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.getRecentFailuresLocked(limit)
}

// getRecentFailuresLocked returns recent failures (caller must hold lock)
func (ac *AgentContext) getRecentFailuresLocked(limit int) []*Failure {
	if limit <= 0 || limit >= len(ac.failures) {
		result := make([]*Failure, len(ac.failures))
		copy(result, ac.failures)
		return result
	}

	start := len(ac.failures) - limit
	result := make([]*Failure, limit)
	copy(result, ac.failures[start:])
	return result
}

// CheckFailure checks if an approach has been tried and failed before
// Supports both exact and partial (substring) matching
func (ac *AgentContext) CheckFailure(approach string) (*Failure, bool) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	approachLower := strings.ToLower(approach)

	// First try exact match
	for _, f := range ac.failures {
		if f.Approach == approach {
			return f, true
		}
	}

	// Then try partial/substring match (case-insensitive)
	for _, f := range ac.failures {
		if strings.Contains(strings.ToLower(f.Approach), approachLower) {
			return f, true
		}
	}

	return nil, false
}

// Intent Methods

// RecordIntent records a user intent
func (ac *AgentContext) RecordIntent(intentType IntentType, content, priority, source string) *Intent {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	i := &Intent{
		ID:        fmt.Sprintf("int_%d", len(ac.intents)+1),
		Type:      intentType,
		Content:   content,
		Priority:  priority,
		Source:    source,
		CreatedAt: time.Now(),
	}
	ac.intents = append(ac.intents, i)
	return i
}

// RecordUserWants records something the user wants
func (ac *AgentContext) RecordUserWants(content, priority, source string) *Intent {
	return ac.RecordIntent(IntentTypeWant, content, priority, source)
}

// RecordUserRejects records something the user rejected
func (ac *AgentContext) RecordUserRejects(content, source string) *Intent {
	return ac.RecordIntent(IntentTypeReject, content, "critical", source)
}

// GetAllIntents returns all recorded intents
func (ac *AgentContext) GetAllIntents() []*Intent {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	result := make([]*Intent, len(ac.intents))
	copy(result, ac.intents)
	return result
}

// GetIntentsByType returns intents of a specific type
func (ac *AgentContext) GetIntentsByType(intentType IntentType) []*Intent {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.getIntentsByTypeLocked(intentType)
}

// getIntentsByTypeLocked returns intents by type (caller must hold lock)
func (ac *AgentContext) getIntentsByTypeLocked(intentType IntentType) []*Intent {
	var result []*Intent
	for _, i := range ac.intents {
		if i.Type == intentType {
			result = append(result, i)
		}
	}
	return result
}

// GetUserWants returns all "want" intents
func (ac *AgentContext) GetUserWants() []*Intent {
	return ac.GetIntentsByType(IntentTypeWant)
}

// getUserWantsLocked returns user wants (caller must hold lock)
func (ac *AgentContext) getUserWantsLocked() []*Intent {
	return ac.getIntentsByTypeLocked(IntentTypeWant)
}

// GetUserRejects returns all "reject" intents
func (ac *AgentContext) GetUserRejects() []*Intent {
	return ac.GetIntentsByType(IntentTypeReject)
}

// getUserRejectsLocked returns user rejects (caller must hold lock)
func (ac *AgentContext) getUserRejectsLocked() []*Intent {
	return ac.getIntentsByTypeLocked(IntentTypeReject)
}

// Resume State Methods

// UpdateResumeState updates the current resume state
func (ac *AgentContext) UpdateResumeState(update func(*ResumeState)) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	update(ac.resumeState)
	ac.resumeState.LastUpdate = time.Now()
}

// SetCurrentTask sets the current task being worked on
func (ac *AgentContext) SetCurrentTask(task, objective string, agent SourceModel) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		rs.CurrentTask = task
		rs.TaskObjective = objective
		rs.LastAgent = agent
	})
}

// CompleteStep marks a step as completed
func (ac *AgentContext) CompleteStep(step string) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		rs.CompletedSteps = append(rs.CompletedSteps, step)
		// Remove from next steps if present
		for i, s := range rs.NextSteps {
			if s == step {
				rs.NextSteps = append(rs.NextSteps[:i], rs.NextSteps[i+1:]...)
				break
			}
		}
	})
}

// SetCurrentStep sets what's currently being worked on
func (ac *AgentContext) SetCurrentStep(step string) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		rs.CurrentStep = step
	})
}

// SetNextSteps sets the upcoming steps
func (ac *AgentContext) SetNextSteps(steps []string) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		rs.NextSteps = steps
	})
}

// AddBlocker adds a blocker
func (ac *AgentContext) AddBlocker(blocker string) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		rs.Blockers = append(rs.Blockers, blocker)
	})
}

// RemoveBlocker removes a blocker
func (ac *AgentContext) RemoveBlocker(blocker string) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		for i, b := range rs.Blockers {
			if b == blocker {
				rs.Blockers = append(rs.Blockers[:i], rs.Blockers[i+1:]...)
				break
			}
		}
	})
}

// SetFilesToRead sets which files should be read first
func (ac *AgentContext) SetFilesToRead(files []string) {
	ac.UpdateResumeState(func(rs *ResumeState) {
		rs.FilesToRead = files
	})
}

// GetResumeState returns the current resume state
func (ac *AgentContext) GetResumeState() *ResumeState {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.getResumeStateLocked()
}

// getResumeStateLocked returns resume state copy (caller must hold lock)
func (ac *AgentContext) getResumeStateLocked() *ResumeState {
	// Return a copy to prevent external modification
	rs := *ac.resumeState
	rs.CompletedSteps = make([]string, len(ac.resumeState.CompletedSteps))
	copy(rs.CompletedSteps, ac.resumeState.CompletedSteps)
	rs.NextSteps = make([]string, len(ac.resumeState.NextSteps))
	copy(rs.NextSteps, ac.resumeState.NextSteps)
	rs.Blockers = make([]string, len(ac.resumeState.Blockers))
	copy(rs.Blockers, ac.resumeState.Blockers)
	rs.FilesToRead = make([]string, len(ac.resumeState.FilesToRead))
	copy(rs.FilesToRead, ac.resumeState.FilesToRead)

	return &rs
}

// GetAgentBriefing returns a structured briefing for an agent taking over
func (ac *AgentContext) GetAgentBriefing() *AgentBriefing {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	return &AgentBriefing{
		ResumeState:    ac.getResumeStateLocked(),
		ModifiedFiles:  ac.getModifiedFilesLocked(),
		RecentFailures: ac.getRecentFailuresLocked(5),
		UserWants:      ac.getUserWantsLocked(),
		UserRejects:    ac.getUserRejectsLocked(),
		Patterns:       ac.getAllPatternsLocked(),
	}
}

// AgentBriefing contains everything an agent needs to continue work
type AgentBriefing struct {
	ResumeState    *ResumeState  `json:"resume_state"`
	ModifiedFiles  []*FileState  `json:"modified_files"`
	RecentFailures []*Failure    `json:"recent_failures"`
	UserWants      []*Intent     `json:"user_wants"`
	UserRejects    []*Intent     `json:"user_rejects"`
	Patterns       []*Pattern    `json:"patterns"`
}
