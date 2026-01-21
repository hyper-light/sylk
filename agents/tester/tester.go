package tester

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

type Tester struct {
	config TesterConfig
	logger *slog.Logger

	state         *TesterState
	currentResult *TestSuiteResult
	mu            sync.RWMutex

	skills      *skills.Registry
	skillLoader *skills.Loader

	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement
}

func New(cfg TesterConfig) (*Tester, error) {
	cfg = applyConfigDefaults(cfg)

	tester := &Tester{
		config:      cfg,
		logger:      slog.Default(),
		knownAgents: make(map[string]*guide.AgentAnnouncement),
	}

	tester.initState()
	tester.initSkills()

	return tester, nil
}

func applyConfigDefaults(cfg TesterConfig) TesterConfig {
	if cfg.Model == "" {
		cfg.Model = "codex-5.2"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 30 * time.Second
	}
	if cfg.CoverageThreshold == 0 {
		cfg.CoverageThreshold = 80.0
	}
	if cfg.MutationScoreThreshold == 0 {
		cfg.MutationScoreThreshold = 70.0
	}
	if cfg.FlakyThreshold == 0 {
		cfg.FlakyThreshold = 20
	}
	if cfg.FlakyRunCount == 0 {
		cfg.FlakyRunCount = 5
	}
	if cfg.ParallelTests == 0 {
		cfg.ParallelTests = 4
	}
	if len(cfg.EnabledCategories) == 0 {
		cfg.EnabledCategories = ValidTestCategories()
	}
	if cfg.CheckpointThreshold == 0 {
		cfg.CheckpointThreshold = 0.85
	}
	if cfg.CompactionThreshold == 0 {
		cfg.CompactionThreshold = 0.95
	}
	return cfg
}

func (t *Tester) initState() {
	t.state = &TesterState{
		ID:           uuid.New().String(),
		StartedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
}

func (t *Tester) initSkills() {
	t.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"run_tests",
		"coverage_report",
		"mutation_test",
		"detect_flaky_tests",
		"prioritize_tests",
		"suggest_test_cases",
		"identify_coverage_gaps",
	}
	loaderCfg.AutoLoadDomains = []string{"testing", "quality"}
	t.skillLoader = skills.NewLoader(t.skills, loaderCfg)

	t.registerCoreSkills()
}

func (t *Tester) Close() error {
	t.Stop()
	return nil
}

func (t *Tester) Start(bus guide.EventBus) error {
	if t.running {
		return fmt.Errorf("tester is already running")
	}

	t.bus = bus
	t.channels = guide.NewAgentChannels("tester")

	var err error
	t.requestSub, err = bus.SubscribeAsync(t.channels.Requests, t.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", t.channels.Requests, err)
	}

	t.responseSub, err = bus.SubscribeAsync(t.channels.Responses, t.handleBusResponse)
	if err != nil {
		t.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", t.channels.Responses, err)
	}

	t.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, t.handleRegistryAnnouncement)
	if err != nil {
		t.requestSub.Unsubscribe()
		t.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	t.running = true
	t.logger.Info("tester started", "channels", t.channels)
	return nil
}

func (t *Tester) Stop() error {
	if !t.running {
		return nil
	}

	errs := t.unsubscribeAll()
	t.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	t.logger.Info("tester stopped")
	return nil
}

func (t *Tester) unsubscribeAll() []error {
	var errs []error
	if err := t.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := t.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := t.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (t *Tester) unsubscribeRequest() error {
	if t.requestSub == nil {
		return nil
	}
	err := t.requestSub.Unsubscribe()
	t.requestSub = nil
	return err
}

func (t *Tester) unsubscribeResponse() error {
	if t.responseSub == nil {
		return nil
	}
	err := t.responseSub.Unsubscribe()
	t.responseSub = nil
	return err
}

func (t *Tester) unsubscribeRegistry() error {
	if t.registrySub == nil {
		return nil
	}
	err := t.registrySub.Unsubscribe()
	t.registrySub = nil
	return err
}

func (t *Tester) IsRunning() bool {
	return t.running
}

func (t *Tester) Bus() guide.EventBus {
	return t.bus
}

func (t *Tester) Channels() *guide.AgentChannels {
	return t.channels
}

func (t *Tester) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	startTime := time.Now()

	result, err := t.processForwardedRequest(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "tester",
		RespondingAgentName: "tester",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			t.generateMessageID(),
			fwd.CorrelationID,
			"tester",
			err.Error(),
		)
		return t.bus.Publish(t.channels.Errors, errMsg)
	}

	resp.Data = result

	respMsg := guide.NewResponseMessage(t.generateMessageID(), resp)
	return t.bus.Publish(t.channels.Responses, respMsg)
}

func (t *Tester) generateMessageID() string {
	return fmt.Sprintf("tester_msg_%s", uuid.New().String())
}

func (t *Tester) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := t.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (t *Tester) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentCheck:
		return t.handleRunTests, nil
	case guide.IntentRecall:
		return t.handleRecall, nil
	default:
		return nil, fmt.Errorf("unsupported intent: %s", intent)
	}
}

func (t *Tester) handleRunTests(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &TesterRequest{
		ID:        uuid.New().String(),
		Intent:    IntentRunTests,
		TesterID:  t.state.ID,
		Timestamp: time.Now(),
	}

	if fwd.Entities != nil {
		if len(fwd.Entities.FilePaths) > 0 {
			req.Files = fwd.Entities.FilePaths
		}
	}

	return t.Handle(ctx, req)
}

func (t *Tester) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return map[string]any{
		"state":          t.state,
		"current_result": t.currentResult,
	}, nil
}

func (t *Tester) handleBusResponse(msg *guide.Message) error {
	t.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

func (t *Tester) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		t.knownAgents[ann.AgentID] = ann
		t.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(t.knownAgents, ann.AgentID)
		t.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

func (t *Tester) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(t.knownAgents))
	for k, v := range t.knownAgents {
		result[k] = v
	}
	return result
}

func (t *Tester) Handle(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	t.updateState(req)

	switch req.Intent {
	case IntentRunTests:
		return t.runTests(ctx, req)
	case IntentCoverage:
		return t.generateCoverageReport(ctx, req)
	case IntentMutation:
		return t.runMutationTests(ctx, req)
	case IntentFlakyDetection:
		return t.detectFlakyTests(ctx, req)
	case IntentPrioritize:
		return t.prioritizeTests(ctx, req)
	case IntentSuggest:
		return t.suggestTestCases(ctx, req)
	case IntentAnalyze:
		return t.analyzeTestQuality(ctx, req)
	default:
		return t.runTests(ctx, req)
	}
}

func (t *Tester) updateState(req *TesterRequest) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.state.LastActiveAt = time.Now()
	if req.SessionID != "" {
		t.state.SessionID = req.SessionID
	}
}

func (t *Tester) runTests(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	startTime := time.Now()

	suiteResult := &TestSuiteResult{
		SuiteID:   uuid.New().String(),
		Name:      "test_run",
		StartedAt: startTime,
		Results:   []TestResult{},
	}

	packages := req.Packages
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	for _, category := range t.config.EnabledCategories {
		if !t.categoryEnabled(category, req.Categories) {
			continue
		}

		categoryResults := t.runCategoryTests(ctx, category, packages, req.Files)
		suiteResult.Results = append(suiteResult.Results, categoryResults...)
	}

	t.aggregateSuiteResults(suiteResult)
	suiteResult.CompletedAt = time.Now()
	suiteResult.Duration = suiteResult.CompletedAt.Sub(startTime)

	t.mu.Lock()
	t.currentResult = suiteResult
	t.state.TestsRun += suiteResult.TotalTests
	t.state.TestsPassed += suiteResult.Passed
	t.state.TestsFailed += suiteResult.Failed
	t.mu.Unlock()

	return &TesterResponse{
		ID:          uuid.New().String(),
		RequestID:   req.ID,
		Success:     suiteResult.Failed == 0,
		SuiteResult: suiteResult,
		Timestamp:   time.Now(),
	}, nil
}

func (t *Tester) categoryEnabled(category TestCategory, requested []TestCategory) bool {
	if len(requested) == 0 {
		return true
	}
	for _, c := range requested {
		if c == category {
			return true
		}
	}
	return false
}

func (t *Tester) runCategoryTests(ctx context.Context, category TestCategory, packages, files []string) []TestResult {
	switch category {
	case CategoryUnit:
		return t.runUnitTests(ctx, packages, files)
	case CategoryIntegration:
		return t.runIntegrationTests(ctx, packages, files)
	case CategoryEndToEnd:
		return t.runEndToEndTests(ctx, packages, files)
	case CategoryProperty:
		return t.runPropertyTests(ctx, packages, files)
	case CategoryMutation:
		return nil
	case CategoryFlaky:
		return nil
	default:
		return nil
	}
}

func (t *Tester) runUnitTests(ctx context.Context, packages, files []string) []TestResult {
	return nil
}

func (t *Tester) runIntegrationTests(ctx context.Context, packages, files []string) []TestResult {
	return nil
}

func (t *Tester) runEndToEndTests(ctx context.Context, packages, files []string) []TestResult {
	return nil
}

func (t *Tester) runPropertyTests(ctx context.Context, packages, files []string) []TestResult {
	return nil
}

func (t *Tester) aggregateSuiteResults(suite *TestSuiteResult) {
	suite.TotalTests = len(suite.Results)
	for _, result := range suite.Results {
		switch result.Status {
		case StatusPassed:
			suite.Passed++
		case StatusFailed:
			suite.Failed++
		case StatusSkipped:
			suite.Skipped++
		case StatusError:
			suite.Errors++
		case StatusFlaky:
			suite.FlakyDetected++
		}
	}
}

func (t *Tester) generateCoverageReport(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	report := &CoverageReport{
		ID:                 uuid.New().String(),
		FileCoverage:       make(map[string]*FileCoverage),
		PackageCoverage:    make(map[string]float64),
		UncoveredLines:     make(map[string][]int),
		CoverageByCategory: make(map[TestCategory]float64),
		GeneratedAt:        time.Now(),
	}

	t.mu.Lock()
	t.state.CurrentCoverage = report.CoveragePercent
	t.mu.Unlock()

	return &TesterResponse{
		ID:             uuid.New().String(),
		RequestID:      req.ID,
		Success:        true,
		CoverageReport: report,
		Timestamp:      time.Now(),
	}, nil
}

func (t *Tester) runMutationTests(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	if !t.config.EnableMutationTesting {
		return &TesterResponse{
			ID:        uuid.New().String(),
			RequestID: req.ID,
			Success:   false,
			Error:     "mutation testing is disabled",
			Timestamp: time.Now(),
		}, nil
	}

	result := &MutationResult{
		ID:                    uuid.New().String(),
		Mutants:               []Mutant{},
		WeakTests:             []string{},
		StrongTests:           []string{},
		SuggestedImprovements: []TestImprovement{},
		GeneratedAt:           time.Now(),
	}

	if result.TotalMutants > 0 {
		result.MutationScore = float64(result.KilledMutants) / float64(result.TotalMutants) * 100
	}

	t.mu.Lock()
	t.state.MutationScore = result.MutationScore
	t.mu.Unlock()

	return &TesterResponse{
		ID:             uuid.New().String(),
		RequestID:      req.ID,
		Success:        true,
		MutationResult: result,
		Timestamp:      time.Now(),
	}, nil
}

func (t *Tester) detectFlakyTests(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	if !t.config.EnableFlakyDetection {
		return &TesterResponse{
			ID:        uuid.New().String(),
			RequestID: req.ID,
			Success:   false,
			Error:     "flaky detection is disabled",
			Timestamp: time.Now(),
		}, nil
	}

	var flakyResults []FlakyTestResult

	flakyCount := 0
	for _, result := range flakyResults {
		if result.IsFlaky {
			flakyCount++
		}
	}

	t.mu.Lock()
	t.state.FlakyTestsFound += flakyCount
	t.mu.Unlock()

	return &TesterResponse{
		ID:           uuid.New().String(),
		RequestID:    req.ID,
		Success:      true,
		FlakyResults: flakyResults,
		Timestamp:    time.Now(),
	}, nil
}

func (t *Tester) prioritizeTests(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	prioritization := &TestPrioritization{
		ID:           uuid.New().String(),
		OrderedTests: []PrioritizedTest{},
		GeneratedAt:  time.Now(),
	}

	return &TesterResponse{
		ID:             uuid.New().String(),
		RequestID:      req.ID,
		Success:        true,
		Prioritization: prioritization,
		Timestamp:      time.Now(),
	}, nil
}

func (t *Tester) suggestTestCases(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	var suggestions []TestSuggestion
	var coverageGaps []CoverageGap

	t.mu.Lock()
	t.state.SuggestionsGenerated += len(suggestions)
	t.mu.Unlock()

	return &TesterResponse{
		ID:           uuid.New().String(),
		RequestID:    req.ID,
		Success:      true,
		Suggestions:  suggestions,
		CoverageGaps: coverageGaps,
		Timestamp:    time.Now(),
	}, nil
}

func (t *Tester) analyzeTestQuality(ctx context.Context, req *TesterRequest) (*TesterResponse, error) {
	coverageResp, err := t.generateCoverageReport(ctx, req)
	if err != nil {
		return nil, err
	}

	var mutationResult *MutationResult
	if t.config.EnableMutationTesting {
		mutationResp, err := t.runMutationTests(ctx, req)
		if err == nil && mutationResp.MutationResult != nil {
			mutationResult = mutationResp.MutationResult
		}
	}

	suggestionsResp, _ := t.suggestTestCases(ctx, req)

	return &TesterResponse{
		ID:             uuid.New().String(),
		RequestID:      req.ID,
		Success:        true,
		CoverageReport: coverageResp.CoverageReport,
		MutationResult: mutationResult,
		Suggestions:    suggestionsResp.Suggestions,
		CoverageGaps:   suggestionsResp.CoverageGaps,
		Timestamp:      time.Now(),
	}, nil
}

func (t *Tester) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      "tester",
		Name:    "tester",
		Aliases: []string{"test", "testing", "qa"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "test",
				Description:   "Run tests on code",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "coverage",
				Description:   "Generate coverage report",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"test",
				"testing",
				"coverage",
				"mutation",
				"flaky",
				"unit test",
				"integration test",
				"e2e test",
				"property test",
			},
			WeakTriggers: []string{
				"run",
				"verify",
				"check",
				"quality",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {
					"test",
					"run tests",
					"coverage",
					"mutation test",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      "tester",
			Name:    "tester",
			Aliases: []string{"test", "testing", "qa"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentCheck,
					guide.IntentRecall,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
				},
				Tags:     []string{"testing", "quality", "coverage", "mutation", "flaky"},
				Keywords: []string{"test", "testing", "coverage", "mutation", "flaky", "unit", "integration", "e2e"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Test quality validation. 6-category test system for unit, integration, e2e, property, mutation, and flaky test detection.",
			Priority:    70,
		},
	}
}

func (t *Tester) PublishRequest(req *guide.RouteRequest) error {
	if !t.running {
		return fmt.Errorf("tester is not running")
	}

	req.SourceAgentID = "tester"
	req.SourceAgentName = "tester"

	msg := guide.NewRequestMessage(t.generateMessageID(), req)
	return t.bus.Publish(guide.TopicGuideRequests, msg)
}

func (t *Tester) Skills() *skills.Registry {
	return t.skills
}

func (t *Tester) GetToolDefinitions() []map[string]any {
	return t.skills.GetToolDefinitions()
}

func (t *Tester) GetState() *TesterState {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stateCopy := *t.state
	return &stateCopy
}

func (t *Tester) GetCurrentResult() *TestSuiteResult {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.currentResult
}
