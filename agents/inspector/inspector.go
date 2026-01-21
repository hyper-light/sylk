package inspector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// Inspector is the code quality validation agent for the Sylk system.
// It serves as the quality guardian for the TDD pipeline, implementing
// an 8-phase validation system and integrating with phases 1 and 4.
type Inspector struct {
	config InspectorConfig
	logger *slog.Logger

	// Validation state
	state         *InspectorState
	criteria      map[string]*InspectorCriteria // Keyed by task ID
	currentResult *InspectorResult
	mu            sync.RWMutex

	// Tool configurations for each validation phase
	toolConfigs map[string]*InspectorToolConfig

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement
}

// New creates a new Inspector agent
func New(cfg InspectorConfig) (*Inspector, error) {
	cfg = applyConfigDefaults(cfg)

	inspector := &Inspector{
		config:      cfg,
		logger:      slog.Default(),
		criteria:    make(map[string]*InspectorCriteria),
		toolConfigs: defaultToolConfigs(),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
	}

	inspector.initState()
	inspector.initSkills()

	return inspector, nil
}

func applyConfigDefaults(cfg InspectorConfig) InspectorConfig {
	if cfg.Model == "" {
		cfg.Model = "codex-5.2"
	}
	if cfg.Mode == "" {
		cfg.Mode = PipelineInternal
	}
	if cfg.CheckpointThreshold == 0 {
		cfg.CheckpointThreshold = 0.85
	}
	if cfg.CompactionThreshold == 0 {
		cfg.CompactionThreshold = 0.95
	}
	if cfg.MaxValidationLoops == 0 {
		cfg.MaxValidationLoops = 3
	}
	if cfg.ValidationTimeout == 0 {
		cfg.ValidationTimeout = 5 * time.Minute
	}
	if cfg.MemoryThreshold.MaxIssues == 0 {
		cfg.MemoryThreshold = DefaultMemoryThreshold()
	}
	if len(cfg.EnabledTools) == 0 {
		cfg.EnabledTools = []string{
			"run_linter",
			"run_type_checker",
			"run_formatter_check",
			"run_security_scan",
			"check_coverage",
			"analyze_complexity",
			"validate_docs",
		}
	}
	return cfg
}

func (i *Inspector) initState() {
	i.state = &InspectorState{
		ID:           uuid.New().String(),
		Mode:         i.config.Mode,
		StartedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
}

func (i *Inspector) initSkills() {
	i.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"run_linter",
		"run_type_checker",
		"run_formatter_check",
		"run_security_scan",
		"check_coverage",
		"analyze_complexity",
		"validate_docs",
	}
	loaderCfg.AutoLoadDomains = []string{"validation", "quality"}
	i.skillLoader = skills.NewLoader(i.skills, loaderCfg)

	i.registerCoreSkills()
}

func defaultToolConfigs() map[string]*InspectorToolConfig {
	return map[string]*InspectorToolConfig{
		"go_lint": {
			Name:        "golangci-lint",
			Command:     "golangci-lint run",
			CheckOnly:   "golangci-lint run --fast",
			Languages:   []string{"go"},
			ConfigFiles: []string{".golangci.yml", ".golangci.yaml"},
			Severity:    "high",
		},
		"go_vet": {
			Name:      "go vet",
			Command:   "go vet ./...",
			CheckOnly: "go vet ./...",
			Languages: []string{"go"},
			Severity:  "critical",
		},
		"go_fmt": {
			Name:      "gofmt",
			Command:   "gofmt -w .",
			CheckOnly: "gofmt -l .",
			Languages: []string{"go"},
			Severity:  "medium",
		},
		"go_imports": {
			Name:      "goimports",
			Command:   "goimports -w .",
			CheckOnly: "goimports -l .",
			Languages: []string{"go"},
			Severity:  "medium",
		},
		"staticcheck": {
			Name:      "staticcheck",
			Command:   "staticcheck ./...",
			CheckOnly: "staticcheck ./...",
			Languages: []string{"go"},
			Severity:  "high",
		},
		"gosec": {
			Name:        "gosec",
			Command:     "gosec ./...",
			CheckOnly:   "gosec -quiet ./...",
			Languages:   []string{"go"},
			ConfigFiles: []string{".gosec.json"},
			Severity:    "critical",
		},
	}
}

// Close closes the inspector and its resources
func (i *Inspector) Close() error {
	i.Stop()
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
func (i *Inspector) Start(bus guide.EventBus) error {
	if i.running {
		return fmt.Errorf("inspector is already running")
	}

	i.bus = bus
	i.channels = guide.NewAgentChannels("inspector")

	var err error
	i.requestSub, err = bus.SubscribeAsync(i.channels.Requests, i.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", i.channels.Requests, err)
	}

	i.responseSub, err = bus.SubscribeAsync(i.channels.Responses, i.handleBusResponse)
	if err != nil {
		i.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", i.channels.Responses, err)
	}

	i.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, i.handleRegistryAnnouncement)
	if err != nil {
		i.requestSub.Unsubscribe()
		i.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	i.running = true
	i.logger.Info("inspector started", "channels", i.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (i *Inspector) Stop() error {
	if !i.running {
		return nil
	}

	errs := i.unsubscribeAll()
	i.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	i.logger.Info("inspector stopped")
	return nil
}

func (i *Inspector) unsubscribeAll() []error {
	var errs []error
	if err := i.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := i.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := i.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (i *Inspector) unsubscribeRequest() error {
	if i.requestSub == nil {
		return nil
	}
	err := i.requestSub.Unsubscribe()
	i.requestSub = nil
	return err
}

func (i *Inspector) unsubscribeResponse() error {
	if i.responseSub == nil {
		return nil
	}
	err := i.responseSub.Unsubscribe()
	i.responseSub = nil
	return err
}

func (i *Inspector) unsubscribeRegistry() error {
	if i.registrySub == nil {
		return nil
	}
	err := i.registrySub.Unsubscribe()
	i.registrySub = nil
	return err
}

// IsRunning returns true if the inspector is actively processing bus messages
func (i *Inspector) IsRunning() bool {
	return i.running
}

// Bus returns the event bus used by the inspector
func (i *Inspector) Bus() guide.EventBus {
	return i.bus
}

// Channels returns the inspector's channel configuration
func (i *Inspector) Channels() *guide.AgentChannels {
	return i.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus
func (i *Inspector) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	startTime := time.Now()

	result, err := i.processForwardedRequest(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "inspector",
		RespondingAgentName: "inspector",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			i.generateMessageID(),
			fwd.CorrelationID,
			"inspector",
			err.Error(),
		)
		return i.bus.Publish(i.channels.Errors, errMsg)
	}

	resp.Data = result

	respMsg := guide.NewResponseMessage(i.generateMessageID(), resp)
	return i.bus.Publish(i.channels.Responses, respMsg)
}

func (i *Inspector) generateMessageID() string {
	return fmt.Sprintf("inspector_msg_%s", uuid.New().String())
}

// processForwardedRequest handles the actual request processing
func (i *Inspector) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := i.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (i *Inspector) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentCheck:
		return i.handleCheck, nil
	case guide.IntentRecall:
		return i.handleRecall, nil
	default:
		return nil, fmt.Errorf("unsupported intent: %s", intent)
	}
}

// handleCheck processes validation check requests
func (i *Inspector) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &InspectorRequest{
		ID:          uuid.New().String(),
		Intent:      IntentCheck,
		InspectorID: i.state.ID,
		Timestamp:   time.Now(),
	}

	if fwd.Entities != nil {
		if len(fwd.Entities.FilePaths) > 0 {
			req.Files = fwd.Entities.FilePaths
		}
	}

	return i.Handle(ctx, req)
}

// handleRecall processes query requests for validation results
func (i *Inspector) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return map[string]any{
		"state":          i.state,
		"current_result": i.currentResult,
		"criteria_count": len(i.criteria),
	}, nil
}

// handleBusResponse processes responses to requests we made
func (i *Inspector) handleBusResponse(msg *guide.Message) error {
	i.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (i *Inspector) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		i.knownAgents[ann.AgentID] = ann
		i.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(i.knownAgents, ann.AgentID)
		i.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the inspector knows about
func (i *Inspector) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(i.knownAgents))
	for k, v := range i.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// Direct API Methods
// =============================================================================

// Handle processes an InspectorRequest directly (without event bus)
func (i *Inspector) Handle(ctx context.Context, req *InspectorRequest) (*InspectorResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	i.updateState(req)

	switch req.Intent {
	case IntentCheck:
		return i.runValidation(ctx, req)
	case IntentValidate:
		return i.runFullValidation(ctx, req)
	default:
		return i.runValidation(ctx, req)
	}
}

func (i *Inspector) updateState(req *InspectorRequest) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.state.LastActiveAt = time.Now()
	if req.TaskID != "" {
		i.state.CurrentTaskID = req.TaskID
	}
	if req.SessionID != "" {
		i.state.SessionID = req.SessionID
	}
}

// =============================================================================
// 8-Phase Validation System
// =============================================================================

// ValidationPhase represents a phase in the 8-phase validation system
type ValidationPhase string

const (
	PhaseLintCheck     ValidationPhase = "lint_check"
	PhaseTypeCheck     ValidationPhase = "type_check"
	PhaseFormatCheck   ValidationPhase = "format_check"
	PhaseSecurityScan  ValidationPhase = "security_scan"
	PhaseTestCoverage  ValidationPhase = "test_coverage"
	PhaseDocumentation ValidationPhase = "documentation"
	PhaseComplexity    ValidationPhase = "complexity"
	PhaseFinalReview   ValidationPhase = "final_review"
)

// PhaseResult contains the result of a single validation phase
type PhaseResult struct {
	Phase      ValidationPhase   `json:"phase"`
	Passed     bool              `json:"passed"`
	Issues     []ValidationIssue `json:"issues"`
	Duration   time.Duration     `json:"duration"`
	Skipped    bool              `json:"skipped"`
	SkipReason string            `json:"skip_reason,omitempty"`
}

// runValidation executes the 8-phase validation system
func (i *Inspector) runValidation(ctx context.Context, req *InspectorRequest) (*InspectorResponse, error) {
	startTime := time.Now()

	result := &InspectorResult{
		TaskID:             req.TaskID,
		Mode:               i.config.Mode,
		StartedAt:          startTime,
		QualityGateResults: make(map[string]bool),
	}

	phases := []ValidationPhase{
		PhaseLintCheck,
		PhaseTypeCheck,
		PhaseFormatCheck,
		PhaseSecurityScan,
		PhaseTestCoverage,
		PhaseDocumentation,
		PhaseComplexity,
		PhaseFinalReview,
	}

	var allIssues []ValidationIssue
	allPassed := true

	for _, phase := range phases {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			phaseResult := i.executePhase(ctx, phase, req.Files)
			result.QualityGateResults[string(phase)] = phaseResult.Passed

			if !phaseResult.Passed {
				allPassed = false
			}

			allIssues = append(allIssues, phaseResult.Issues...)

			// Stop on critical failures
			if i.hasCriticalFailure(phaseResult.Issues) {
				break
			}
		}
	}

	result.Passed = allPassed
	result.Issues = allIssues
	result.CompletedAt = time.Now()
	result.LoopCount = 1

	i.mu.Lock()
	i.currentResult = result
	i.state.IssuesFound += len(allIssues)
	i.state.LoopsCompleted++
	i.mu.Unlock()

	return &InspectorResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Result:    result,
		Timestamp: time.Now(),
	}, nil
}

// runFullValidation runs validation with feedback loops
func (i *Inspector) runFullValidation(ctx context.Context, req *InspectorRequest) (*InspectorResponse, error) {
	var feedbackHistory []InspectorFeedback
	loopCount := 0

	for loopCount < i.config.MaxValidationLoops {
		loopCount++

		resp, err := i.runValidation(ctx, req)
		if err != nil {
			return nil, err
		}

		feedback := InspectorFeedback{
			Loop:      loopCount,
			Timestamp: time.Now(),
			Issues:    resp.Result.Issues,
			Passed:    resp.Result.Passed,
		}

		if !resp.Result.Passed {
			feedback.Corrections = i.generateCorrections(resp.Result.Issues)
		}

		feedbackHistory = append(feedbackHistory, feedback)

		if resp.Result.Passed {
			resp.Result.FeedbackHistory = feedbackHistory
			resp.Result.LoopCount = loopCount
			return resp, nil
		}

		// Check if we have only non-blocking issues
		if !i.hasBlockingIssues(resp.Result.Issues) {
			resp.Result.FeedbackHistory = feedbackHistory
			resp.Result.LoopCount = loopCount
			return resp, nil
		}
	}

	// Max loops reached
	resp, _ := i.runValidation(ctx, req)
	resp.Result.FeedbackHistory = feedbackHistory
	resp.Result.LoopCount = loopCount
	return resp, nil
}

// executePhase runs a single validation phase
func (i *Inspector) executePhase(ctx context.Context, phase ValidationPhase, files []string) *PhaseResult {
	startTime := time.Now()

	result := &PhaseResult{
		Phase: phase,
	}

	// Execute phase-specific validation
	switch phase {
	case PhaseLintCheck:
		result.Issues = i.runLintCheck(ctx, files)
	case PhaseTypeCheck:
		result.Issues = i.runTypeCheck(ctx, files)
	case PhaseFormatCheck:
		result.Issues = i.runFormatCheck(ctx, files)
	case PhaseSecurityScan:
		result.Issues = i.runSecurityScan(ctx, files)
	case PhaseTestCoverage:
		result.Issues = i.runCoverageCheck(ctx, files)
	case PhaseDocumentation:
		result.Issues = i.runDocumentationCheck(ctx, files)
	case PhaseComplexity:
		result.Issues = i.runComplexityAnalysis(ctx, files)
	case PhaseFinalReview:
		result.Issues = i.runFinalReview(ctx, files)
	}

	result.Duration = time.Since(startTime)
	result.Passed = len(result.Issues) == 0 || !i.hasBlockingIssues(result.Issues)

	return result
}

// Phase execution methods
func (i *Inspector) runLintCheck(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute actual linter
	return nil
}

func (i *Inspector) runTypeCheck(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute type checker
	return nil
}

func (i *Inspector) runFormatCheck(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute formatter check
	return nil
}

func (i *Inspector) runSecurityScan(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute security scanner
	return nil
}

func (i *Inspector) runCoverageCheck(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute coverage check
	return nil
}

func (i *Inspector) runDocumentationCheck(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute documentation check
	return nil
}

func (i *Inspector) runComplexityAnalysis(ctx context.Context, files []string) []ValidationIssue {
	// Placeholder - would execute complexity analysis
	return nil
}

func (i *Inspector) runFinalReview(ctx context.Context, files []string) []ValidationIssue {
	// Consolidate and review all findings
	return nil
}

// =============================================================================
// Severity Classification
// =============================================================================

// hasCriticalFailure checks if any issues are critical severity
func (i *Inspector) hasCriticalFailure(issues []ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == Critical {
			return true
		}
	}
	return false
}

// hasBlockingIssues checks if any issues are blocking (critical or high)
func (i *Inspector) hasBlockingIssues(issues []ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == Critical || issue.Severity == High {
			return true
		}
	}
	return false
}

// generateCorrections creates correction suggestions for issues
func (i *Inspector) generateCorrections(issues []ValidationIssue) []Correction {
	var corrections []Correction

	for _, issue := range issues {
		if issue.SuggestedFix != "" {
			corrections = append(corrections, Correction{
				IssueID:      issue.ID,
				Description:  fmt.Sprintf("Fix: %s", issue.Message),
				SuggestedFix: issue.SuggestedFix,
				File:         issue.File,
				LineStart:    issue.Line,
				LineEnd:      issue.Line,
			})
		}
	}

	return corrections
}

// =============================================================================
// TDD Pipeline Integration (Phases 1 and 4)
// =============================================================================

// DefineCriteria creates success criteria for a task (TDD Phase 1)
func (i *Inspector) DefineCriteria(taskID string, criteria *InspectorCriteria) {
	i.mu.Lock()
	defer i.mu.Unlock()

	criteria.TaskID = taskID
	criteria.CreatedAt = time.Now()
	i.criteria[taskID] = criteria
}

// GetCriteria retrieves the criteria for a task
func (i *Inspector) GetCriteria(taskID string) (*InspectorCriteria, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	criteria, ok := i.criteria[taskID]
	return criteria, ok
}

// ValidateAgainstCriteria validates implementation against criteria (TDD Phase 4)
func (i *Inspector) ValidateAgainstCriteria(ctx context.Context, taskID string, files []string) (*InspectorResult, error) {
	criteria, ok := i.GetCriteria(taskID)
	if !ok {
		return nil, fmt.Errorf("no criteria found for task: %s", taskID)
	}

	req := &InspectorRequest{
		ID:          uuid.New().String(),
		Intent:      IntentValidate,
		TaskID:      taskID,
		Files:       files,
		Criteria:    criteria,
		InspectorID: i.state.ID,
		Timestamp:   time.Now(),
	}

	resp, err := i.runFullValidation(ctx, req)
	if err != nil {
		return nil, err
	}

	// Check criteria
	result := resp.Result
	for _, criterion := range criteria.SuccessCriteria {
		if criterion.Verifiable {
			met := i.verifyCriterion(criterion, result)
			if met {
				result.CriteriaMet = append(result.CriteriaMet, criterion.ID)
			} else {
				result.CriteriaFailed = append(result.CriteriaFailed, criterion.ID)
			}
		}
	}

	// Check quality gates
	for _, gate := range criteria.QualityGates {
		passed := i.checkQualityGate(gate, result)
		result.QualityGateResults[gate.Name] = passed
	}

	result.Passed = len(result.CriteriaFailed) == 0 && !i.hasBlockingIssues(result.Issues)

	return result, nil
}

func (i *Inspector) verifyCriterion(criterion SuccessCriterion, result *InspectorResult) bool {
	// Placeholder - would verify based on criterion type
	return true
}

func (i *Inspector) checkQualityGate(gate QualityGate, result *InspectorResult) bool {
	// Placeholder - would check gate based on metric
	return true
}

// =============================================================================
// Override Request Handling
// =============================================================================

// ProcessOverrideRequest handles a user override request
func (i *Inspector) ProcessOverrideRequest(override *OverrideRequest) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.currentResult == nil {
		return fmt.Errorf("no current validation result")
	}

	// Find the issue
	for idx, issue := range i.currentResult.Issues {
		if issue.ID == override.IssueID {
			if override.Approved {
				// Mark as overridden (remove from blocking)
				i.currentResult.Issues[idx].Severity = Info
				i.logger.Info("issue overridden",
					"issue_id", override.IssueID,
					"reason", override.Reason,
					"approved_by", override.ApprovedBy,
				)
			}
			return nil
		}
	}

	return fmt.Errorf("issue not found: %s", override.IssueID)
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the inspector's routing information for Guide registration
func (i *Inspector) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      "inspector",
		Name:    "inspector",
		Aliases: []string{"validate", "check", "quality"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "validate",
				Description:   "Validate code against quality standards",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "lint",
				Description:   "Run linter checks on code",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"validate",
				"check",
				"lint",
				"verify",
				"inspect",
				"quality",
				"coverage",
				"security scan",
				"type check",
				"format check",
			},
			WeakTriggers: []string{
				"test",
				"error",
				"warning",
				"issue",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {
					"validate",
					"verify",
					"check",
					"inspect",
					"lint",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      "inspector",
			Name:    "inspector",
			Aliases: []string{"validate", "check", "quality"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentCheck,
					guide.IntentRecall,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
				},
				Tags:     []string{"validation", "quality", "lint", "security", "coverage"},
				Keywords: []string{"validate", "check", "lint", "verify", "inspect", "quality", "coverage", "security"},
				Priority: 75,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Code quality validation. 8-phase validation system for lint, type check, format, security, coverage, documentation, complexity, and final review.",
			Priority:    75,
		},
	}
}

// PublishRequest publishes a request to the Guide for routing
func (i *Inspector) PublishRequest(req *guide.RouteRequest) error {
	if !i.running {
		return fmt.Errorf("inspector is not running")
	}

	req.SourceAgentID = "inspector"
	req.SourceAgentName = "inspector"

	msg := guide.NewRequestMessage(i.generateMessageID(), req)
	return i.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Skills System
// =============================================================================

// Skills returns the inspector's skill registry
func (i *Inspector) Skills() *skills.Registry {
	return i.skills
}

// GetToolDefinitions returns tool definitions for all loaded skills
func (i *Inspector) GetToolDefinitions() []map[string]any {
	return i.skills.GetToolDefinitions()
}

// GetState returns the current inspector state
func (i *Inspector) GetState() *InspectorState {
	i.mu.RLock()
	defer i.mu.RUnlock()

	stateCopy := *i.state
	return &stateCopy
}

// GetCurrentResult returns the most recent validation result
func (i *Inspector) GetCurrentResult() *InspectorResult {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i.currentResult
}
