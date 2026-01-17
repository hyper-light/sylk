package archivalist

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ConflictType categorizes the type of conflict detected
type ConflictType string

const (
	ConflictTypeNone           ConflictType = "none"
	ConflictTypeFileState      ConflictType = "file_state"
	ConflictTypePattern        ConflictType = "pattern"
	ConflictTypePatternFailure ConflictType = "pattern_failure" // Pattern conflicts with failure
	ConflictTypeFailure        ConflictType = "failure"
	ConflictTypeIntent         ConflictType = "intent"
	ConflictTypeResume         ConflictType = "resume"
	ConflictTypeVersion        ConflictType = "version" // Base version mismatch
)

// ResolutionStrategy defines how to resolve a conflict
type ResolutionStrategy string

const (
	ResolutionAccept    ResolutionStrategy = "accept"    // Accept incoming
	ResolutionReject    ResolutionStrategy = "reject"    // Reject incoming
	ResolutionMerge     ResolutionStrategy = "merge"     // Merge both values
	ResolutionAppend    ResolutionStrategy = "append"    // Append to existing (for failures)
	ResolutionCombine   ResolutionStrategy = "combine"   // Combine as options
	ResolutionHierarchy ResolutionStrategy = "hierarchy" // Parent wins
	ResolutionHuman     ResolutionStrategy = "human"     // Requires human decision
)

// ConflictResult represents the outcome of conflict detection/resolution
type ConflictResult struct {
	Type           ConflictType       `json:"type"`
	Strategy       ResolutionStrategy `json:"strategy"`
	Resolved       bool               `json:"resolved"`
	MergedData     map[string]any     `json:"merged_data,omitempty"`
	Message        string             `json:"message"`
	ExistingValue  any                `json:"existing_value,omitempty"`
	IncomingValue  any                `json:"incoming_value,omitempty"`
	RelatedFailure *Failure           `json:"related_failure,omitempty"`
}

// ConflictDetector handles conflict detection and auto-resolution
type ConflictDetector struct {
	mu sync.RWMutex

	// Reference to agent context for checking failures, patterns, etc.
	agentContext *AgentContext

	// Reference to registry for hierarchy resolution
	registry *Registry

	// Protected categories that require human resolution if conflicting
	protectedCategories map[Scope]bool

	// Resolution rules by scope
	rules map[Scope]ResolutionRule
}

// ResolutionRule defines how to resolve conflicts for a scope
type ResolutionRule struct {
	DefaultStrategy   ResolutionStrategy
	CheckFailures     bool // Check failure records before accepting
	AllowMerge        bool // Allow merging values
	RequireHierarchy  bool // Use hierarchy for ties
	ProtectedCategory bool // Requires human for irreconcilable conflicts
}

// NewConflictDetector creates a new conflict detector
func NewConflictDetector(agentContext *AgentContext, registry *Registry) *ConflictDetector {
	cd := &ConflictDetector{
		agentContext: agentContext,
		registry:     registry,
		protectedCategories: map[Scope]bool{
			ScopeIntents: true, // User intents are protected
		},
		rules: make(map[Scope]ResolutionRule),
	}

	// Configure default rules
	cd.rules[ScopeFiles] = ResolutionRule{
		DefaultStrategy: ResolutionMerge,
		AllowMerge:      true,
	}
	cd.rules[ScopePatterns] = ResolutionRule{
		DefaultStrategy: ResolutionCombine,
		CheckFailures:   true,
		AllowMerge:      true,
	}
	cd.rules[ScopeFailures] = ResolutionRule{
		DefaultStrategy: ResolutionAppend,
		AllowMerge:      false, // Failures are append-only
	}
	cd.rules[ScopeIntents] = ResolutionRule{
		DefaultStrategy:   ResolutionHuman,
		ProtectedCategory: true,
	}
	cd.rules[ScopeResume] = ResolutionRule{
		DefaultStrategy:  ResolutionHierarchy,
		RequireHierarchy: true,
	}

	return cd
}

// DetectConflict checks if incoming write conflicts with existing state
func (cd *ConflictDetector) DetectConflict(
	scope Scope,
	key string,
	incoming map[string]any,
	baseVersion string,
	agentID string,
) *ConflictResult {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	rule, ok := cd.ruleForScope(scope)
	if !ok {
		return cd.acceptNoConflict()
	}

	return cd.dispatchConflict(scope, key, incoming, agentID, rule)
}

func (cd *ConflictDetector) ruleForScope(scope Scope) (ResolutionRule, bool) {
	rule, ok := cd.rules[scope]
	return rule, ok
}

func (cd *ConflictDetector) dispatchConflict(scope Scope, key string, incoming map[string]any, agentID string, rule ResolutionRule) *ConflictResult {
	handlers := cd.conflictHandlers(key, incoming, agentID, rule)
	handler, ok := handlers[scope]
	if !ok {
		return cd.acceptNoConflict()
	}
	return handler()
}

type conflictHandler func() *ConflictResult

type conflictHandlers map[Scope]conflictHandler

func (cd *ConflictDetector) conflictHandlers(key string, incoming map[string]any, agentID string, rule ResolutionRule) conflictHandlers {
	return conflictHandlers{
		ScopeFiles: func() *ConflictResult {
			return cd.detectFileConflict(key, incoming, agentID)
		},
		ScopePatterns: func() *ConflictResult {
			return cd.detectPatternConflict(key, incoming, agentID, rule)
		},
		ScopeFailures: func() *ConflictResult {
			return cd.detectFailureConflict(key, incoming, agentID)
		},
		ScopeIntents: func() *ConflictResult {
			return cd.detectIntentConflict(key, incoming, agentID, rule)
		},
		ScopeResume: func() *ConflictResult {
			return cd.detectResumeConflict(incoming, agentID, rule)
		},
	}
}

// detectFileConflict handles file state conflicts (always mergeable)
func (cd *ConflictDetector) detectFileConflict(
	filePath string,
	incoming map[string]any,
	agentID string,
) *ConflictResult {
	if cd.agentContext == nil {
		return cd.acceptNoConflict()
	}

	existing := cd.agentContext.GetFileState(filePath)
	if existing == nil {
		return cd.acceptNoConflict()
	}

	// Check if same agent is modifying - no conflict with self
	if cd.registry != nil {
		incomingAgent := cd.registry.Get(agentID)
		if incomingAgent != nil && incomingAgent.Source == existing.Agent {
			return cd.acceptNoConflict()
		}
	}

	return cd.mergeFileState(filePath, existing, incoming)
}

func (cd *ConflictDetector) mergeFileState(filePath string, existing *FileState, incoming map[string]any) *ConflictResult {
	merged := map[string]any{
		"path":        filePath,
		"status":      existing.Status,
		"modified_by": cd.mergeModifiers(existing, incoming),
		"changes":     cd.mergeChanges(existing, incoming),
	}
	return &ConflictResult{
		Type: ConflictTypeFileState, Strategy: ResolutionMerge,
		Resolved: true, MergedData: merged,
		Message: "merged file state from multiple agents",
	}
}

func (cd *ConflictDetector) mergeModifiers(existing *FileState, incoming map[string]any) []string {
	modifiedBy := make([]string, 0)
	if existing.ModifiedBy != "" {
		modifiedBy = append(modifiedBy, existing.ModifiedBy)
	}
	incomingModifier, _ := incoming["modified_by"].(string)
	if incomingModifier != "" && incomingModifier != existing.ModifiedBy {
		modifiedBy = append(modifiedBy, incomingModifier)
	}
	return modifiedBy
}

func (cd *ConflictDetector) mergeChanges(existing *FileState, incoming map[string]any) []map[string]any {
	changes := cd.existingChangesToMaps(existing.Changes)
	return cd.appendIncomingChanges(changes, incoming)
}

func (cd *ConflictDetector) existingChangesToMaps(existingChanges []FileChange) []map[string]any {
	changes := make([]map[string]any, len(existingChanges))
	for i, ch := range existingChanges {
		changes[i] = map[string]any{"description": ch.Description, "lines": ch.Lines, "agent": ch.AgentID}
	}
	return changes
}

func (cd *ConflictDetector) appendIncomingChanges(changes []map[string]any, incoming map[string]any) []map[string]any {
	incomingChanges, ok := incoming["changes"].([]any)
	if !ok {
		return changes
	}
	for _, ch := range incomingChanges {
		if chMap, ok := ch.(map[string]any); ok {
			changes = append(changes, chMap)
		}
	}
	return changes
}

// detectPatternConflict handles pattern conflicts with failure awareness
func (cd *ConflictDetector) detectPatternConflict(
	category string,
	incoming map[string]any,
	agentID string,
	rule ResolutionRule,
) *ConflictResult {
	if cd.agentContext == nil {
		return cd.acceptNoConflict()
	}

	incomingPattern, _ := incoming["pattern"].(string)

	if result := cd.checkIncomingPatternFailures(incomingPattern, rule); result != nil {
		return result
	}

	existing := cd.agentContext.GetPattern(category)
	if existing == nil {
		return cd.acceptNoConflict()
	}

	if existing.Pattern == incomingPattern {
		return cd.acceptNoConflict()
	}

	if result := cd.checkExistingPatternFailures(existing.Pattern, incomingPattern, incoming); result != nil {
		return result
	}

	return cd.combinePatternOptions(existing, incomingPattern, agentID, incoming)
}

func (cd *ConflictDetector) checkIncomingPatternFailures(pattern string, rule ResolutionRule) *ConflictResult {
	if !rule.CheckFailures {
		return nil
	}
	failures := cd.agentContext.GetRecentFailures(20)
	for _, failure := range failures {
		if cd.patternMatchesFailure(pattern, failure) {
			return &ConflictResult{
				Type:           ConflictTypePatternFailure,
				Strategy:       ResolutionReject,
				Resolved:       false,
				Message:        fmt.Sprintf("pattern '%s' matches failed approach: %s", pattern, failure.Approach),
				RelatedFailure: failure,
				IncomingValue:  pattern,
			}
		}
	}
	return nil
}

func (cd *ConflictDetector) checkExistingPatternFailures(existingPattern, incomingPattern string, incoming map[string]any) *ConflictResult {
	for _, failure := range cd.agentContext.GetRecentFailures(20) {
		if cd.patternMatchesFailure(existingPattern, failure) {
			return &ConflictResult{
				Type:       ConflictTypePattern,
				Strategy:   ResolutionAccept,
				Resolved:   true,
				Message:    fmt.Sprintf("existing pattern '%s' has failure record, accepting '%s'", existingPattern, incomingPattern),
				MergedData: incoming,
			}
		}
	}
	return nil
}

func (cd *ConflictDetector) combinePatternOptions(existing *Pattern, incomingPattern, agentID string, incoming map[string]any) *ConflictResult {
	merged := map[string]any{
		"status": "pending_decision",
		"options": []map[string]any{
			{
				"approach": existing.Pattern,
				"source":   existing.Source,
				"agent":    existing.EstablishedBy,
			},
			{
				"approach": incomingPattern,
				"source":   incoming["source"],
				"agent":    agentID,
			},
		},
		"note": "Two approaches proposed - next agent should decide or test",
	}

	return &ConflictResult{
		Type:          ConflictTypePattern,
		Strategy:      ResolutionCombine,
		Resolved:      true,
		MergedData:    merged,
		Message:       "combined as pending decision",
		ExistingValue: existing.Pattern,
		IncomingValue: incomingPattern,
	}
}

// patternMatchesFailure checks if a pattern matches a failure record
func (cd *ConflictDetector) patternMatchesFailure(pattern string, failure *Failure) bool {
	// Normalize for comparison
	patternLower := strings.ToLower(pattern)
	approachLower := strings.ToLower(failure.Approach)

	// Check for keyword overlap
	patternWords := strings.Fields(patternLower)
	for _, word := range patternWords {
		if len(word) > 3 && strings.Contains(approachLower, word) {
			return true
		}
	}

	return false
}

// detectFailureConflict handles failure records (append-only, never conflict)
func (cd *ConflictDetector) detectFailureConflict(
	key string,
	incoming map[string]any,
	agentID string,
) *ConflictResult {
	// Validate required fields
	approach, hasApproach := incoming["approach"].(string)
	reason, hasReason := incoming["reason"].(string)

	if !hasApproach || approach == "" {
		return &ConflictResult{
			Type:     ConflictTypeFailure,
			Strategy: ResolutionReject,
			Resolved: false,
			Message:  "failure record missing required 'approach' field",
		}
	}

	if !hasReason || reason == "" {
		return &ConflictResult{
			Type:     ConflictTypeFailure,
			Strategy: ResolutionReject,
			Resolved: false,
			Message:  "failure record missing required 'reason' field",
		}
	}

	// Check for duplicate failure (same approach already recorded)
	if cd.agentContext != nil {
		if existingFailure, found := cd.agentContext.CheckFailure(approach); found {
			// Not a conflict - just note that it's already recorded
			return &ConflictResult{
				Type:          ConflictTypeNone,
				Strategy:      ResolutionAppend,
				Resolved:      true,
				Message:       fmt.Sprintf("failure appended (similar to existing fail_%s)", existingFailure.ID),
				ExistingValue: existingFailure.Approach,
				IncomingValue: approach,
			}
		}
	}

	// Failures are always appended, never truly conflict
	return &ConflictResult{
		Type:     ConflictTypeNone,
		Strategy: ResolutionAppend,
		Resolved: true,
		Message:  "failure record appended",
	}
}

// intentInput holds extracted and validated intent data
type intentInput struct {
	content  string
	typ      string
	priority string
}

// detectIntentConflict handles user intent conflicts (protected category)
func (cd *ConflictDetector) detectIntentConflict(
	key string,
	incoming map[string]any,
	agentID string,
	rule ResolutionRule,
) *ConflictResult {
	if cd.agentContext == nil {
		return cd.acceptNoConflict()
	}

	input, err := cd.extractIntentInput(incoming)
	if err != nil {
		return cd.rejectIntent(err.Error())
	}

	sameType := cd.getIntentsByType(input.typ)
	if result := cd.checkSameTypeConflicts(sameType, input, rule); result != nil {
		return result
	}

	oppositeType := cd.getOppositeIntents(input.typ)
	if result := cd.checkCrossTypeConflicts(oppositeType, input, rule); result != nil {
		return result
	}

	if result := cd.checkCriticalOverride(key, sameType, input, incoming); result != nil {
		return result
	}

	return cd.acceptNoConflict()
}

func (cd *ConflictDetector) extractIntentInput(incoming map[string]any) (*intentInput, error) {
	content, _ := incoming["content"].(string)
	if content == "" {
		content, _ = incoming["description"].(string)
	}
	if content == "" {
		return nil, fmt.Errorf("intent missing required 'content' field")
	}

	typ, _ := incoming["type"].(string)
	if typ != "want" && typ != "reject" {
		typ = "want"
	}

	priority, _ := incoming["priority"].(string)
	return &intentInput{content: content, typ: typ, priority: priority}, nil
}

func (cd *ConflictDetector) getIntentsByType(typ string) []*Intent {
	if typ == "want" {
		return cd.agentContext.GetUserWants()
	}
	return cd.agentContext.GetUserRejects()
}

func (cd *ConflictDetector) getOppositeIntents(typ string) []*Intent {
	if typ == "want" {
		return cd.agentContext.GetUserRejects()
	}
	return cd.agentContext.GetUserWants()
}

func (cd *ConflictDetector) checkSameTypeConflicts(intents []*Intent, input *intentInput, rule ResolutionRule) *ConflictResult {
	for _, existing := range intents {
		if !cd.intentsContradict(existing.Content, input.content) {
			continue
		}
		if rule.ProtectedCategory {
			return cd.humanRequired(input.typ, existing.Content, input.content)
		}
		return cd.acceptWithReplace(existing.Content, input.content)
	}
	return nil
}

func (cd *ConflictDetector) checkCrossTypeConflicts(intents []*Intent, input *intentInput, rule ResolutionRule) *ConflictResult {
	for _, existing := range intents {
		if !cd.intentMatchesOpposite(input.content, existing.Content) {
			continue
		}
		if rule.ProtectedCategory {
			return cd.humanRequiredCross(input.typ, input.content, existing)
		}
	}
	return nil
}

func (cd *ConflictDetector) checkCriticalOverride(key string, intents []*Intent, input *intentInput, incoming map[string]any) *ConflictResult {
	if input.priority != "critical" || key == "" {
		return nil
	}
	for _, existing := range intents {
		if existing.Priority != "critical" && cd.intentsRelated(existing.Content, input.content) {
			return &ConflictResult{
				Type:       ConflictTypeIntent,
				Strategy:   ResolutionAccept,
				Resolved:   true,
				Message:    fmt.Sprintf("critical intent '%s' supersedes '%s'", input.content, existing.Content),
				MergedData: incoming,
			}
		}
	}
	return nil
}

func (cd *ConflictDetector) acceptNoConflict() *ConflictResult {
	return &ConflictResult{Type: ConflictTypeNone, Strategy: ResolutionAccept, Resolved: true}
}

func (cd *ConflictDetector) rejectIntent(msg string) *ConflictResult {
	return &ConflictResult{Type: ConflictTypeIntent, Strategy: ResolutionReject, Resolved: false, Message: msg}
}

func (cd *ConflictDetector) humanRequired(typ, existing, incoming string) *ConflictResult {
	return &ConflictResult{
		Type:          ConflictTypeIntent,
		Strategy:      ResolutionHuman,
		Resolved:      false,
		Message:       fmt.Sprintf("contradictory user %ss - requires human decision", typ),
		ExistingValue: existing,
		IncomingValue: incoming,
	}
}

func (cd *ConflictDetector) acceptWithReplace(existing, incoming string) *ConflictResult {
	return &ConflictResult{
		Type:          ConflictTypeIntent,
		Strategy:      ResolutionAccept,
		Resolved:      true,
		Message:       fmt.Sprintf("intent conflict auto-resolved (not protected): replacing '%s'", existing),
		ExistingValue: existing,
		IncomingValue: incoming,
	}
}

func (cd *ConflictDetector) humanRequiredCross(typ, content string, existing *Intent) *ConflictResult {
	return &ConflictResult{
		Type:          ConflictTypeIntent,
		Strategy:      ResolutionHuman,
		Resolved:      false,
		Message:       fmt.Sprintf("user %s '%s' conflicts with existing %s '%s' - requires human decision", typ, content, existing.Type, existing.Content),
		ExistingValue: existing.Content,
		IncomingValue: content,
	}
}

// intentMatchesOpposite checks if a want directly contradicts a reject
func (cd *ConflictDetector) intentMatchesOpposite(a, b string) bool {
	aLower := strings.ToLower(a)
	bLower := strings.ToLower(b)

	// Direct match (user wants X but also rejected X)
	if strings.Contains(aLower, bLower) || strings.Contains(bLower, aLower) {
		return true
	}

	return false
}

// intentsRelated checks if two intents are about the same topic
func (cd *ConflictDetector) intentsRelated(a, b string) bool {
	aLower := strings.ToLower(a)
	bLower := strings.ToLower(b)

	// Extract key words (3+ chars)
	aWords := strings.Fields(aLower)
	for _, word := range aWords {
		if len(word) > 3 && strings.Contains(bLower, word) {
			return true
		}
	}

	return false
}

// intentsContradict checks if two intents are contradictory
func (cd *ConflictDetector) intentsContradict(a, b string) bool {
	// Normalize
	aLower := strings.ToLower(a)
	bLower := strings.ToLower(b)

	// Check for opposing keywords
	opposites := map[string][]string{
		"simple":     {"enterprise", "complex", "advanced"},
		"basic":      {"enterprise", "complex", "advanced"},
		"monolith":   {"microservice", "distributed"},
		"orm":        {"raw sql", "no orm"},
		"typescript": {"javascript only", "no typescript"},
	}

	for key, antonyms := range opposites {
		aHasKey := strings.Contains(aLower, key)
		bHasKey := strings.Contains(bLower, key)

		for _, antonym := range antonyms {
			aHasAntonym := strings.Contains(aLower, antonym)
			bHasAntonym := strings.Contains(bLower, antonym)

			// If one has key and other has antonym, they contradict
			if (aHasKey && bHasAntonym) || (bHasKey && aHasAntonym) {
				return true
			}
		}
	}

	return false
}

// resumeInput holds extracted resume task details
type resumeInput struct {
	task string
	step string
}

// detectResumeConflict handles resume state conflicts (hierarchy wins)
func (cd *ConflictDetector) detectResumeConflict(
	incoming map[string]any,
	agentID string,
	rule ResolutionRule,
) *ConflictResult {
	if cd.agentContext == nil {
		return cd.acceptNoConflict()
	}

	existing := cd.agentContext.GetResumeState()
	if existing == nil || existing.CurrentTask == "" {
		return cd.acceptNoConflict()
	}

	input := cd.extractResumeInput(incoming)

	if !rule.RequireHierarchy || cd.registry == nil {
		return cd.resolveWithoutHierarchy(existing, input, incoming)
	}

	return cd.resolveWithHierarchy(existing, input, incoming, agentID)
}

func (cd *ConflictDetector) extractResumeInput(incoming map[string]any) resumeInput {
	task, _ := incoming["current_task"].(string)
	step, _ := incoming["current_step"].(string)
	return resumeInput{task: task, step: step}
}

func (cd *ConflictDetector) resolveWithoutHierarchy(existing *ResumeState, input resumeInput, incoming map[string]any) *ConflictResult {
	if input.task == existing.CurrentTask {
		return cd.resumeAccept("resume state updated (same task)")
	}

	if input.step == "" && len(existing.NextSteps) == 0 {
		return cd.resumeAccept("new task accepted (previous task complete)")
	}

	if input.task != "" && existing.CurrentTask != "" {
		return cd.resumeMergeTaskSwitch(existing, input)
	}

	return cd.resumeAccept("accepted (last-write-wins, hierarchy not enforced)")
}

func (cd *ConflictDetector) resolveWithHierarchy(existing *ResumeState, input resumeInput, incoming map[string]any, agentID string) *ConflictResult {
	incomingAgent := cd.registry.Get(agentID)
	if incomingAgent == nil {
		return cd.resumeAccept("accepted (incoming agent not registered)")
	}

	existingAgent := cd.registry.GetByName(string(existing.LastAgent))
	if existingAgent == nil {
		return cd.resumeAccept("accepted (previous agent not found in registry)")
	}

	return cd.resolveAgentRelationship(incomingAgent, existingAgent, existing, incoming)
}

func (cd *ConflictDetector) resolveAgentRelationship(incoming, existing *RegisteredAgent, state *ResumeState, data map[string]any) *ConflictResult {
	if incoming.ID == existing.ID {
		return cd.acceptNoConflict()
	}

	if existing.ParentID == incoming.ID {
		return cd.resumeAccept("parent agent state accepted (overrides child)")
	}

	if incoming.ParentID == existing.ID {
		return cd.resumeRejectChild(incoming.Name, existing.Name, state, data)
	}

	if incoming.ParentID != "" && incoming.ParentID == existing.ParentID {
		return cd.resumeMergeSiblings(incoming.Name, existing.Name)
	}

	return cd.resumeAccept(fmt.Sprintf("accepted (agents '%s' and '%s' at same hierarchy level)", incoming.Name, existing.Name))
}

func (cd *ConflictDetector) resumeAccept(msg string) *ConflictResult {
	return &ConflictResult{Type: ConflictTypeResume, Strategy: ResolutionAccept, Resolved: true, Message: msg}
}

func (cd *ConflictDetector) resumeMergeTaskSwitch(existing *ResumeState, input resumeInput) *ConflictResult {
	return &ConflictResult{
		Type:     ConflictTypeResume,
		Strategy: ResolutionMerge,
		Resolved: true,
		Message:  fmt.Sprintf("task switch from '%s' to '%s'", existing.CurrentTask, input.task),
		MergedData: map[string]any{
			"previous_task":    existing.CurrentTask,
			"previous_step":    existing.CurrentStep,
			"current_task":     input.task,
			"task_switched_at": existing.LastUpdate,
		},
	}
}

func (cd *ConflictDetector) resumeRejectChild(incomingName, existingName string, state *ResumeState, data map[string]any) *ConflictResult {
	return &ConflictResult{
		Type:          ConflictTypeResume,
		Strategy:      ResolutionReject,
		Resolved:      false,
		Message:       fmt.Sprintf("sub-agent '%s' cannot override parent '%s' resume state", incomingName, existingName),
		ExistingValue: state,
		IncomingValue: data,
	}
}

func (cd *ConflictDetector) resumeMergeSiblings(incomingName, existingName string) *ConflictResult {
	return &ConflictResult{
		Type:     ConflictTypeResume,
		Strategy: ResolutionMerge,
		Resolved: true,
		Message:  fmt.Sprintf("sibling agents '%s' and '%s' states merged", incomingName, existingName),
		MergedData: map[string]any{
			"merged_from": []string{existingName, incomingName},
			"note":        "Sibling agents working on same task - parent should reconcile",
		},
	}
}

// Resolve applies the resolution strategy and returns the final data
func (cd *ConflictDetector) Resolve(result *ConflictResult, existing, incoming map[string]any) map[string]any {
	switch result.Strategy {
	case ResolutionAccept:
		return incoming
	case ResolutionReject:
		return existing
	case ResolutionMerge, ResolutionCombine:
		if result.MergedData != nil {
			return result.MergedData
		}
		return incoming
	case ResolutionAppend:
		return incoming // Append handled by caller
	default:
		return incoming
	}
}

// ConflictRecord stores a record of a detected conflict for auditing
type ConflictRecord struct {
	ID           string          `json:"id"`
	DetectedAt   time.Time       `json:"detected_at"`
	Type         ConflictType    `json:"type"`
	Scope        Scope           `json:"scope"`
	Key          string          `json:"key,omitempty"`
	AgentID      string          `json:"agent_id"`
	BaseVersion  string          `json:"base_version"`
	Result       *ConflictResult `json:"result"`
	AutoResolved bool            `json:"auto_resolved"`
}

// ConflictHistory maintains a history of conflicts for debugging
type ConflictHistory struct {
	mu      sync.RWMutex
	records []*ConflictRecord
	limit   int
}

// NewConflictHistory creates a new conflict history tracker
func NewConflictHistory(limit int) *ConflictHistory {
	if limit <= 0 {
		limit = 100
	}
	return &ConflictHistory{
		records: make([]*ConflictRecord, 0, limit),
		limit:   limit,
	}
}

// Record adds a conflict to history
func (ch *ConflictHistory) Record(record *ConflictRecord) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	ch.records = append(ch.records, record)

	// Trim if over limit
	if len(ch.records) > ch.limit {
		ch.records = ch.records[len(ch.records)-ch.limit:]
	}
}

// GetRecent returns recent conflict records
func (ch *ConflictHistory) GetRecent(n int) []*ConflictRecord {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if n <= 0 || n > len(ch.records) {
		n = len(ch.records)
	}

	result := make([]*ConflictRecord, n)
	copy(result, ch.records[len(ch.records)-n:])
	return result
}

// GetUnresolved returns conflicts that required human resolution
func (ch *ConflictHistory) GetUnresolved() []*ConflictRecord {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	var unresolved []*ConflictRecord
	for _, r := range ch.records {
		if !r.AutoResolved {
			unresolved = append(unresolved, r)
		}
	}
	return unresolved
}
