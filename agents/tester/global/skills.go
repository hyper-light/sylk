package global

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// analyzeBatchSkill creates a skill that collects batch context from completed pipelines.
func analyzeBatchSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		PipelineIDs  []string          `json:"pipeline_ids"`
		ChangedFiles []string          `json:"changed_files,omitempty"`
		TaskSpecs    map[string]string `json:"task_specs,omitempty"`
	}

	return skills.NewSkill("analyze_batch").
		Description("Collect and analyze batch context from completed pipelines — changed files, task specs, pipeline results.").
		Domain("testing").
		Keywords("batch", "context", "pipeline", "collect", "analyze").
		Priority(95).
		Usage("Use first when the global task depends on multiple completed pipelines or cross-pipeline context. It assembles the concrete surface the rest of the global testing work should reason over.").
		Requirement("Provide the relevant pipeline IDs and any already-known changed files or task specs so later planning stays grounded in the actual batch.").
		Satisfies("Produces the shared batch context needed for integration-risk analysis, harness planning, and global validation reporting.").
		Avoid("Do not skip it and reason from memory when the request depends on multiple pipelines or accumulated task artifacts.").
		ArrayParam("pipeline_ids", "IDs of completed pipelines to analyze", "string", true).
		ArrayParam("changed_files", "Files changed across all pipelines", "string", false).
		ObjectParam("task_specs", "Optional task-spec map keyed by pipeline id.", nil, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			batch := gt.analyzeBatchContext(ctx, p.PipelineIDs, p.ChangedFiles, p.TaskSpecs)
			return map[string]any{
				"pipelines_analyzed":  len(batch.PipelineIDs),
				"pipeline_ids":        append([]string(nil), batch.PipelineIDs...),
				"changed_files":       append([]string(nil), batch.ChangedFiles...),
				"changed_file_count":  len(batch.ChangedFiles),
				"task_specs":          batch.TaskSpecs,
				"task_spec_count":     len(batch.TaskSpecs),
				"languages":           gt.batchLanguages(batch.ChangedFiles),
				"existing_test_files": gt.relatedExistingTests(ctx, batch.ChangedFiles),
			}, nil
		}).
		Build()
}

// analyzeIntegrationRisksSkill creates a skill for cross-pipeline risk analysis.
func analyzeIntegrationRisksSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		ChangedFiles []string `json:"changed_files"`
		PipelineIDs  []string `json:"pipeline_ids,omitempty"`
		FocusAreas   []string `json:"focus_areas,omitempty"`
	}

	return skills.NewSkill("analyze_integration_risks").
		Description("Analyze cross-pipeline integration risks — shared state mutations, API contract changes, cascading failures.").
		Domain("testing").
		Keywords("integration", "risk", "cross-pipeline", "shared state", "API contract").
		Priority(95).
		Usage("Use after assembling the relevant batch context or whenever a global task asks for integration, system, or cross-pipeline risk analysis.").
		Requirement("Provide the concrete changed files and, when possible, the relevant pipeline IDs or focus areas so the analysis stays scoped to the actual request.").
		Satisfies("Produces cross-pipeline risk evidence that should shape integration plans, harness work, execution, and escalation.").
		Avoid("Do not stop here when the request still requires authored global tests, execution evidence, or an escalation report.").
		ArrayParam("changed_files", "Files changed across pipelines", "string", true).
		ArrayParam("pipeline_ids", "Relevant pipeline IDs", "string", false).
		ArrayParam("focus_areas", "Specific risk areas to focus on", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			risks, files, err := gt.analyzeIntegrationRisks(ctx, p.ChangedFiles, p.PipelineIDs, p.FocusAreas)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"files_analyzed": len(files),
				"changed_files":  files,
				"risk_areas":     risks,
				"risk_count":     len(risks),
			}, nil
		}).
		Build()
}

// Phase 2.K / GT-B refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// plan_tests (shared unit) + plan_integration_tests + plan_e2e_tests
// collapsed into one plan_tests(level ∈ {unit, integration, e2e}) skill.
// The three underlying handlers stay; the outer skill routes on level.
func planTestsSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		Level        string               `json:"level"`
		RiskAreas    []shared.RiskArea    `json:"risk_areas"`
		TaskSpec     string               `json:"task_spec,omitempty"`
		Files        []string             `json:"files,omitempty"`
		BatchCtx     map[string]any       `json:"batch_context,omitempty"`
		HarnessNeeds []shared.HarnessNeed `json:"harness_needs,omitempty"`
	}

	// Delegate to the shared unit planner for level=unit. Built once.
	unitPlanner := shared.PlanTestsSkill(gt)

	return skills.NewSkill("plan_tests").
		Description("Design a test strategy. One primitive for every test level — select via `level`.\n\n"+
			"Levels:\n"+
			"- unit: focused per-component plans from risk analysis (params: risk_areas, task_spec?, files?).\n"+
			"- integration: cross-pipeline integration plans rooted in shared-state risks (params: risk_areas?, batch_context?).\n"+
			"- e2e: end-to-end flow plans covering full system scenarios (params: risk_areas?, harness_needs?).").
		Domain("testing").
		Keywords("plan", "test plan", "unit", "integration", "e2e", "end-to-end", "strategy", "hypothesis").
		Priority(95).
		Usage("Choose level based on the test surface: unit for per-component, integration for cross-component, e2e for full-system flows. Run after risk analysis so the plan maps to real defects.").
		Satisfies("Produces deliberate test-case structure and satisfies the tester planning stage at the requested level.").
		Avoid("Do not treat the plan itself as completion when the requested deliverable still requires test artifacts or suite execution.").
		EnumParam("level", "Test level (default: unit)", []string{"unit", "integration", "e2e"}, false).
		ArrayObjectParam("risk_areas", "Risk areas that should directly inform the planned coverage.", sharedRiskAreaProperties(), []string{"file", "category", "level", "description"}, false).
		StringParam("task_spec", "Task specification for context (level=unit)", false).
		ArrayParam("files", "Source files under test (level=unit)", "string", false).
		ObjectParam("batch_context", "Optional batch context returned by analyze_batch (level=integration)", batchContextProperties(), false).
		ArrayObjectParam("harness_needs", "Harness needs that affect end-to-end setup (level=e2e)", harnessNeedProperties(), []string{"type", "description", "target"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.ToLower(strings.TrimSpace(p.Level)) {
			case "", "unit":
				return unitPlanner.Handler(ctx, input)
			case "integration":
				plan, err := gt.planIntegrationTests(ctx, p.RiskAreas, p.BatchCtx)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"plan":       plan,
					"level":      "integration",
					"risk_count": len(plan.RiskAreas),
					"case_count": len(plan.PlannedCase),
				}, nil
			case "e2e", "end-to-end", "end_to_end":
				plan, err := gt.planE2ETests(ctx, p.RiskAreas, p.HarnessNeeds)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"plan":          plan,
					"level":         "e2e",
					"risk_count":    len(plan.RiskAreas),
					"case_count":    len(plan.PlannedCase),
					"harness_needs": len(p.HarnessNeeds),
				}, nil
			default:
				return nil, fmt.Errorf("unknown plan_tests level: %q (expected unit|integration|e2e)", p.Level)
			}
		}).
		Build()
}

// buildHarnessSkill creates a skill for constructing test infrastructure.
func buildHarnessSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		Files       []string          `json:"files,omitempty"`
		TaskSpec    string            `json:"task_spec,omitempty"`
		RiskAreas   []shared.RiskArea `json:"risk_areas,omitempty"`
		Fixtures    []TestFixture     `json:"fixtures,omitempty"`
		MockServers []MockServer      `json:"mock_servers,omitempty"`
		TestDBs     []TestDB          `json:"test_dbs,omitempty"`
	}

	return skills.NewSkill("build_harness").
		Description("Plan or register test fixtures, mock servers, and test databases for integration/e2e testing. If harness files must be created, materialize them separately with prepare_global_write_context and the global test write skills.").
		Domain("testing").
		Keywords("harness", "fixture", "mock", "infrastructure", "setup").
		Priority(88).
		Usage("Use when the global task requires reusable integration or end-to-end infrastructure before authored tests or execution can succeed.").
		Requirement("Detect the harness needs first and prepare leased write contexts separately for any files this harness work will materialize.").
		Satisfies("Produces reusable harness state and the concrete infrastructure plan needed for global test authoring and execution.").
		Avoid("Do not treat it as a direct file mutation step; actual harness files still belong to the leased global write tools.").
		ArrayParam("files", "Relevant changed files or system surfaces that need harness support.", "string", false).
		StringParam("task_spec", "Task specification or global validation brief.", false).
		ArrayObjectParam("risk_areas", "Risk areas that imply infrastructure needs.", sharedRiskAreaProperties(), []string{"file", "category", "level", "description"}, false).
		ArrayObjectParam("fixtures", "Explicit fixtures to register in the reusable harness.", fixtureProperties(), []string{"name", "type"}, false).
		ArrayObjectParam("mock_servers", "Explicit mock servers to register in the reusable harness.", mockServerProperties(), []string{"name"}, false).
		ArrayObjectParam("test_dbs", "Explicit test databases to register in the reusable harness.", testDBProperties(), []string{"name", "driver"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return gt.buildHarness(ctx, p.Files, p.TaskSpec, p.RiskAreas, p.Fixtures, p.MockServers, p.TestDBs)
		}).
		Build()
}

func (gt *GlobalTester) analyzeBatchContext(
	ctx context.Context,
	pipelineIDs, changedFiles []string,
	taskSpecs map[string]string,
) *shared.BatchContext {
	batch := gt.currentBatchContext()
	if batch == nil {
		batch = &shared.BatchContext{}
	}

	if len(pipelineIDs) > 0 {
		batch.PipelineIDs = uniqueSortedStrings(pipelineIDs)
	}
	if len(taskSpecs) > 0 {
		normalized := make(map[string]string, len(taskSpecs))
		for key, value := range taskSpecs {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			normalized[key] = value
		}
		if len(normalized) > 0 {
			batch.TaskSpecs = normalized
		}
	}

	if len(changedFiles) == 0 {
		changedFiles = batch.ChangedFiles
	}
	if len(changedFiles) == 0 {
		changedFiles = gt.changedFilesFromOverlay()
	}

	taskSpec := joinBatchTaskSpecs(batch.TaskSpecs)
	batch.ChangedFiles = gt.normalizeTargetFiles(changedFiles, taskSpec)

	gt.mu.Lock()
	gt.batchContext = batch
	gt.mu.Unlock()
	return gt.currentBatchContext()
}

func (gt *GlobalTester) currentBatchContext() *shared.BatchContext {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	if gt.batchContext == nil {
		return nil
	}
	copyBatch := *gt.batchContext
	copyBatch.PipelineIDs = append([]string(nil), gt.batchContext.PipelineIDs...)
	copyBatch.ChangedFiles = append([]string(nil), gt.batchContext.ChangedFiles...)
	if gt.batchContext.TaskSpecs != nil {
		copyBatch.TaskSpecs = make(map[string]string, len(gt.batchContext.TaskSpecs))
		for key, value := range gt.batchContext.TaskSpecs {
			copyBatch.TaskSpecs[key] = value
		}
	}
	return &copyBatch
}

func (gt *GlobalTester) changedFilesFromOverlay() []string {
	fa, ok := gt.fileAccess.(globalTesterOverlayAwareFileAccess)
	if !ok {
		return nil
	}
	root := gt.workingDir()
	seen := make(map[string]struct{})
	out := make([]string, 0, len(fa.Modifications()))
	for _, mod := range fa.Modifications() {
		path := strings.TrimSpace(mod.OriginalPath)
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) {
			if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
				path = rel
			}
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "." || path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func (gt *GlobalTester) batchLanguages(files []string) []string {
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, file := range files {
		lang := inferLanguage([]string{file}, "")
		if strings.TrimSpace(lang) == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

func (gt *GlobalTester) relatedExistingTests(ctx context.Context, files []string) []string {
	harness, err := gt.detectHarness(ctx, files, joinBatchTaskSpecs(nil), "")
	if err != nil || harness == nil {
		return nil
	}
	return append([]string(nil), harness.ExistingTestFiles...)
}

func (gt *GlobalTester) analyzeIntegrationRisks(
	ctx context.Context,
	changedFiles, pipelineIDs, focusAreas []string,
) ([]shared.RiskArea, []string, error) {
	batch := gt.analyzeBatchContext(ctx, pipelineIDs, changedFiles, nil)
	if batch == nil || len(batch.ChangedFiles) == 0 {
		return nil, nil, fmt.Errorf("changed_files are required for cross-pipeline risk analysis")
	}
	taskSpec := joinBatchTaskSpecs(batch.TaskSpecs)
	if len(focusAreas) > 0 {
		taskSpec = strings.TrimSpace(taskSpec + "\n\nFocus Areas:\n- " + strings.Join(uniqueSortedStrings(focusAreas), "\n- "))
	}

	risks := gt.analyzeRisks(ctx, batch.ChangedFiles, taskSpec, "")
	crossRiskFile := batch.ChangedFiles[0]
	if len(batch.PipelineIDs) > 1 {
		risks = append(risks, shared.RiskArea{
			File:        crossRiskFile,
			Category:    shared.RiskState,
			Level:       shared.RiskHigh,
			Description: "Changes from multiple pipelines can drift at shared boundaries and need merged-state integration coverage.",
		})
	}
	if spansMultipleDirectories(batch.ChangedFiles) {
		risks = append(risks, shared.RiskArea{
			File:        crossRiskFile,
			Category:    shared.RiskLogic,
			Level:       shared.RiskMedium,
			Description: "Changed files span multiple directories or layers, increasing the chance of cross-component contract drift.",
		})
	}
	for _, file := range batch.ChangedFiles {
		lower := strings.ToLower(file)
		switch {
		case strings.Contains(lower, "api"), strings.Contains(lower, "http"), strings.Contains(lower, "route"), strings.Contains(lower, "client"), strings.Contains(lower, "webhook"):
			risks = append(risks, shared.RiskArea{
				File:        file,
				Category:    shared.RiskBoundary,
				Level:       shared.RiskHigh,
				Description: "API and transport changes need integration coverage for request, response, and compatibility boundaries.",
			})
		case strings.Contains(lower, "migration"), strings.Contains(lower, "schema"), strings.Contains(lower, "sql"), strings.Contains(lower, "db"), strings.Contains(lower, "repo"):
			risks = append(risks, shared.RiskArea{
				File:        file,
				Category:    shared.RiskState,
				Level:       shared.RiskHigh,
				Description: "Persistence-layer changes need integration coverage for schema, state transitions, and backwards compatibility.",
			})
		case strings.Contains(lower, "config"), strings.Contains(lower, ".env"), strings.Contains(lower, "package.json"), strings.Contains(lower, "go.mod"):
			risks = append(risks, shared.RiskArea{
				File:        file,
				Category:    shared.RiskResource,
				Level:       shared.RiskMedium,
				Description: "Configuration and dependency changes can invalidate the runtime or test harness in merged state.",
			})
		}
	}
	return dedupeRiskAreas(risks), append([]string(nil), batch.ChangedFiles...), nil
}

func (gt *GlobalTester) planIntegrationTests(ctx context.Context, riskAreas []shared.RiskArea, batchCtx map[string]any) (*shared.TestPlan, error) {
	batch := gt.mergeBatchContext(batchCtx)
	if len(riskAreas) == 0 {
		var err error
		riskAreas, _, err = gt.analyzeIntegrationRisks(ctx, batch.ChangedFiles, batch.PipelineIDs, nil)
		if err != nil {
			return nil, err
		}
	}
	if len(batch.ChangedFiles) == 0 {
		for _, risk := range riskAreas {
			if strings.TrimSpace(risk.File) != "" {
				batch.ChangedFiles = append(batch.ChangedFiles, risk.File)
			}
		}
		batch.ChangedFiles = uniqueSortedStrings(batch.ChangedFiles)
	}

	harness := gt.currentHarnessState()
	if harness == nil && len(batch.ChangedFiles) > 0 {
		detected, err := gt.detectHarness(ctx, batch.ChangedFiles, joinBatchTaskSpecs(batch.TaskSpecs), "")
		if err == nil {
			harness = detected
		}
	}

	cases := make([]shared.PlannedTestCase, 0, len(riskAreas)+2)
	for _, risk := range riskAreas {
		cases = append(cases, shared.PlannedTestCase{
			Name:              "TestIntegration" + sanitizeIdentifier(filepath.Base(risk.File)+"_"+string(risk.Category)),
			Category:          mapRiskToCategory(risk.Category),
			FailureHypothesis: risk.Description,
			InputStrategy:     "Drive the changed component through its real boundary with adjacent components and shared state present.",
			ExpectedBehavior:  expectedBehaviorForRisk(risk),
			TargetFile:        risk.File,
		})
	}
	if len(batch.ChangedFiles) > 1 {
		cases = append(cases, shared.PlannedTestCase{
			Name:              "TestIntegrationMergedSurfaceCompatibility",
			Category:          shared.CategoryEdgeCase,
			FailureHypothesis: "Merged changes from multiple files or pipelines break the contract only when exercised together.",
			InputStrategy:     "Exercise the primary changed surfaces together through the real integration boundary and assert end-to-end compatibility.",
			ExpectedBehavior:  "Cross-file and cross-pipeline behavior remains compatible in the merged state.",
			TargetFile:        batch.ChangedFiles[0],
		})
	}
	cases = dedupePlannedCases(cases)
	plan := &shared.TestPlan{
		ID:          "plan_" + uuid.NewString()[:8],
		Rationale:   buildIntegrationPlanRationale(batch, riskAreas, harness),
		RiskAreas:   append([]shared.RiskArea(nil), riskAreas...),
		PlannedCase: cases,
		CreatedAt:   time.Now(),
	}
	gt.mu.Lock()
	gt.currentPlan = plan
	gt.mu.Unlock()
	return plan, nil
}

func (gt *GlobalTester) planE2ETests(ctx context.Context, riskAreas []shared.RiskArea, harnessNeeds []shared.HarnessNeed) (*shared.TestPlan, error) {
	batch := gt.currentBatchContext()
	if batch == nil {
		batch = &shared.BatchContext{}
	}
	if len(riskAreas) == 0 {
		var err error
		riskAreas, _, err = gt.analyzeIntegrationRisks(ctx, batch.ChangedFiles, batch.PipelineIDs, []string{"end-to-end"})
		if err != nil {
			return nil, err
		}
	}
	if len(harnessNeeds) == 0 {
		harnessNeeds = gt.inferHarnessNeeds(batch.ChangedFiles, riskAreas)
	}

	cases := make([]shared.PlannedTestCase, 0, len(riskAreas)+1)
	for _, risk := range riskAreas {
		cases = append(cases, shared.PlannedTestCase{
			Name:              "TestE2E" + sanitizeIdentifier(filepath.Base(risk.File)+"_"+string(risk.Category)),
			Category:          mapRiskToCategory(risk.Category),
			FailureHypothesis: risk.Description,
			InputStrategy:     buildE2EInputStrategy(risk, harnessNeeds),
			ExpectedBehavior:  "A realistic user or system flow stays correct across the merged system boundary.",
			TargetFile:        risk.File,
		})
	}
	if len(batch.ChangedFiles) > 0 {
		cases = append(cases, shared.PlannedTestCase{
			Name:              "TestE2EMergedPrimaryFlow",
			Category:          shared.CategoryEdgeCase,
			FailureHypothesis: "The primary merged user flow fails only when the changed surfaces are exercised together in a realistic environment.",
			InputStrategy:     buildPrimaryFlowStrategy(batch.ChangedFiles, harnessNeeds),
			ExpectedBehavior:  "The primary user-facing or system-facing flow succeeds across the merged surfaces without regressions.",
			TargetFile:        batch.ChangedFiles[0],
		})
	}
	cases = dedupePlannedCases(cases)
	plan := &shared.TestPlan{
		ID:          "plan_" + uuid.NewString()[:8],
		Rationale:   buildE2EPlanRationale(batch, riskAreas, harnessNeeds),
		RiskAreas:   append([]shared.RiskArea(nil), riskAreas...),
		PlannedCase: cases,
		CreatedAt:   time.Now(),
	}
	gt.mu.Lock()
	gt.currentPlan = plan
	gt.mu.Unlock()
	return plan, nil
}

func (gt *GlobalTester) buildHarness(
	ctx context.Context,
	files []string,
	taskSpec string,
	riskAreas []shared.RiskArea,
	fixtures []TestFixture,
	mockServers []MockServer,
	testDBs []TestDB,
) (map[string]any, error) {
	targetFiles := gt.normalizeTargetFiles(files, taskSpec)
	if len(targetFiles) == 0 {
		if batch := gt.currentBatchContext(); batch != nil {
			targetFiles = append(targetFiles, batch.ChangedFiles...)
		}
	}

	var harnessState *globalTestHarnessState
	if len(targetFiles) > 0 {
		detected, err := gt.detectHarness(ctx, targetFiles, taskSpec, "")
		if err != nil && gt.currentHarnessState() == nil {
			return nil, err
		}
		if err == nil {
			harnessState = detected
		}
	}
	if harnessState == nil {
		harnessState = gt.currentHarnessState()
	}

	needs := gt.inferHarnessNeeds(targetFiles, riskAreas)
	autoFixtures, autoMocks, autoDBs := inferHarnessResources(targetFiles, needs)

	gt.mu.Lock()
	if gt.harness == nil {
		gt.harness = NewTestHarness()
	}
	for _, fixture := range append(autoFixtures, fixtures...) {
		gt.harness.AddFixture(fixture)
	}
	for _, mock := range append(autoMocks, mockServers...) {
		gt.harness.AddMockServer(mock)
	}
	for _, db := range append(autoDBs, testDBs...) {
		gt.harness.AddTestDB(db)
	}
	summary := gt.harness.Summary()
	gt.mu.Unlock()

	plannedFiles := resourceFiles(autoFixtures, autoMocks, autoDBs, fixtures, mockServers, testDBs)
	requiresWriteContext := len(plannedFiles) > 0
	if harnessState != nil && harnessState.SetupRequired {
		plannedFiles = append(plannedFiles, harnessState.MissingConfigFiles...)
		requiresWriteContext = true
	}

	result := map[string]any{
		"built":                  true,
		"summary":                summary,
		"harness_needs":          needs,
		"fixtures":               append(autoFixtures, fixtures...),
		"mock_servers":           append(autoMocks, mockServers...),
		"test_dbs":               append(autoDBs, testDBs...),
		"requires_write_context": requiresWriteContext,
		"write_scope":            "global",
		"planned_files":          uniqueSortedStrings(plannedFiles),
	}
	if harnessState != nil {
		result["execution_harness"] = map[string]any{
			"framework_id":         string(harnessState.FrameworkID),
			"framework_name":       harnessState.FrameworkName,
			"language":             harnessState.Language,
			"run_command":          harnessState.RunCommand,
			"coverage_command":     harnessState.CoverageCommand,
			"config_files":         append([]string(nil), harnessState.ConfigFiles...),
			"missing_config_files": append([]string(nil), harnessState.MissingConfigFiles...),
			"existing_test_files":  append([]string(nil), harnessState.ExistingTestFiles...),
			"recommended_outputs":  cloneStringMap(harnessState.RecommendedOutputs),
			"package_patterns":     append([]string(nil), harnessState.PackagePatterns...),
			"setup_required":       harnessState.SetupRequired,
			"setup_reason":         harnessState.SetupReason,
		}
	}
	return result, nil
}

func (gt *GlobalTester) inferHarnessNeeds(files []string, riskAreas []shared.RiskArea) []shared.HarnessNeed {
	needs := make([]shared.HarnessNeed, 0, len(files))
	addNeed := func(kind, target, description string) {
		needs = append(needs, shared.HarnessNeed{
			Type:        kind,
			Target:      target,
			Description: description,
		})
	}
	for _, file := range files {
		lower := strings.ToLower(file)
		switch {
		case strings.Contains(lower, "api"), strings.Contains(lower, "http"), strings.Contains(lower, "client"), strings.Contains(lower, "webhook"):
			addNeed("mock_server", file, "Simulate external or cross-service dependencies while preserving integration boundaries.")
		case strings.Contains(lower, "db"), strings.Contains(lower, "sql"), strings.Contains(lower, "migration"), strings.Contains(lower, "repo"):
			addNeed("test_db", file, "Provide isolated persistence state for merged-state integration and e2e coverage.")
		case strings.Contains(lower, "config"), strings.Contains(lower, "fixture"), strings.Contains(lower, "seed"):
			addNeed("fixture", file, "Provide deterministic configuration or seed data for the changed surface.")
		}
	}
	for _, risk := range riskAreas {
		switch risk.Category {
		case shared.RiskSecurity, shared.RiskBoundary:
			addNeed("fixture", risk.File, "Exercise realistic negative and boundary payloads across the changed surface.")
		case shared.RiskState, shared.RiskResource:
			addNeed("test_db", risk.File, "Persist and reset realistic state transitions while validating merged behavior.")
		}
	}
	return dedupeHarnessNeeds(needs)
}

func inferHarnessResources(files []string, needs []shared.HarnessNeed) ([]TestFixture, []MockServer, []TestDB) {
	fixtures := make([]TestFixture, 0, len(needs))
	mocks := make([]MockServer, 0, len(needs))
	dbs := make([]TestDB, 0, len(needs))
	for _, need := range needs {
		name := sanitizeIdentifier(filepath.Base(need.Target))
		switch need.Type {
		case "mock_server":
			mocks = append(mocks, MockServer{
				Name:     firstNonEmpty(name, "MergedService"),
				Endpoint: "/" + strings.Trim(strings.ToLower(filepath.Base(need.Target)), "/"),
				Port:     8080,
				File:     filepath.ToSlash(filepath.Join("testdata", "mocks", strings.ToLower(firstNonEmpty(name, "merged_service"))+".json")),
			})
		case "test_db":
			dbs = append(dbs, TestDB{
				Name:   firstNonEmpty(name, "MergedState"),
				Driver: "sqlite",
				DSN:    "file::memory:?cache=shared",
				File:   filepath.ToSlash(filepath.Join("testdata", "fixtures", strings.ToLower(firstNonEmpty(name, "merged_state"))+".sql")),
			})
		default:
			fixtures = append(fixtures, TestFixture{
				Name:        firstNonEmpty(name, "MergedFixture"),
				Type:        "data",
				File:        filepath.ToSlash(filepath.Join("testdata", "fixtures", strings.ToLower(firstNonEmpty(name, "merged_fixture"))+".json")),
				Description: need.Description,
			})
		}
	}
	if len(fixtures) == 0 && len(files) > 0 {
		fixtures = append(fixtures, TestFixture{
			Name:        "MergedWorkspaceFixture",
			Type:        "data",
			File:        filepath.ToSlash(filepath.Join("testdata", "fixtures", "merged_workspace.json")),
			Description: "Representative merged-state inputs for the current changed surface.",
		})
	}
	return dedupeFixtures(fixtures), dedupeMockServers(mocks), dedupeTestDBs(dbs)
}

func buildE2EInputStrategy(risk shared.RiskArea, harnessNeeds []shared.HarnessNeed) string {
	if len(harnessNeeds) == 0 {
		return "Exercise the primary merged user or system flow through realistic entry points and assert the full downstream outcome."
	}
	return "Exercise the primary merged user or system flow with the required harness dependencies in place: " + strings.Join(harnessNeedLabels(harnessNeeds), ", ") + "."
}

func buildPrimaryFlowStrategy(files []string, harnessNeeds []shared.HarnessNeed) string {
	surfaces := append([]string(nil), files...)
	if len(surfaces) > 3 {
		surfaces = surfaces[:3]
	}
	message := "Exercise the main merged flow that crosses " + strings.Join(surfaces, ", ")
	if len(harnessNeeds) > 0 {
		message += " with " + strings.Join(harnessNeedLabels(harnessNeeds), ", ")
	}
	return message + "."
}

func buildIntegrationPlanRationale(batch *shared.BatchContext, risks []shared.RiskArea, harness *globalTestHarnessState) string {
	parts := []string{
		fmt.Sprintf("Focus the integration plan on %d merged risk areas across %d changed files.", len(risks), len(batch.ChangedFiles)),
	}
	if harness != nil {
		parts = append(parts, fmt.Sprintf("Use %s as the execution harness.", harness.FrameworkName))
	}
	if len(batch.PipelineIDs) > 1 {
		parts = append(parts, fmt.Sprintf("The merged state spans %d contributing pipelines, so prioritize compatibility edges first.", len(batch.PipelineIDs)))
	}
	return strings.Join(parts, " ")
}

func buildE2EPlanRationale(batch *shared.BatchContext, risks []shared.RiskArea, harnessNeeds []shared.HarnessNeed) string {
	parts := []string{
		fmt.Sprintf("Model realistic full-system flows over %d changed files and %d merged risk areas.", len(batch.ChangedFiles), len(risks)),
	}
	if len(harnessNeeds) > 0 {
		parts = append(parts, "Prepare the required harness dependencies before execution: "+strings.Join(harnessNeedLabels(harnessNeeds), ", ")+".")
	}
	return strings.Join(parts, " ")
}

func resourceFiles(autoFixtures []TestFixture, autoMocks []MockServer, autoDBs []TestDB, fixtures []TestFixture, mockServers []MockServer, testDBs []TestDB) []string {
	paths := make([]string, 0, len(autoFixtures)+len(autoMocks)+len(autoDBs)+len(fixtures)+len(mockServers)+len(testDBs))
	for _, item := range append(autoFixtures, fixtures...) {
		if strings.TrimSpace(item.File) != "" {
			paths = append(paths, item.File)
		}
	}
	for _, item := range append(autoMocks, mockServers...) {
		if strings.TrimSpace(item.File) != "" {
			paths = append(paths, item.File)
		}
	}
	for _, item := range append(autoDBs, testDBs...) {
		if strings.TrimSpace(item.File) != "" {
			paths = append(paths, item.File)
		}
	}
	return paths
}

func batchContextProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"pipeline_ids":  {Type: "array", Description: "Pipeline ids in the current batch.", Items: &skills.Property{Type: "string"}},
		"changed_files": {Type: "array", Description: "Changed files in the current batch.", Items: &skills.Property{Type: "string"}},
	}
}

func sharedRiskAreaProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"file":        {Type: "string", Description: "Path associated with the risk."},
		"function":    {Type: "string", Description: "Optional function associated with the risk."},
		"category":    {Type: "string", Description: "Risk category."},
		"level":       {Type: "string", Description: "Risk severity level."},
		"description": {Type: "string", Description: "Why this risk matters."},
	}
}

func harnessNeedProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"type":        {Type: "string", Description: "Harness need type such as fixture, mock_server, or test_db."},
		"description": {Type: "string", Description: "Why the harness need exists."},
		"target":      {Type: "string", Description: "Target file or surface that motivated the need."},
	}
}

func fixtureProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"name":        {Type: "string", Description: "Fixture name."},
		"type":        {Type: "string", Description: "Fixture type."},
		"file":        {Type: "string", Description: "Fixture file path, when applicable."},
		"description": {Type: "string", Description: "Fixture description."},
	}
}

func mockServerProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"name":     {Type: "string", Description: "Mock server name."},
		"endpoint": {Type: "string", Description: "Primary endpoint path served by the mock."},
		"port":     {Type: "integer", Description: "Preferred mock port."},
		"file":     {Type: "string", Description: "Optional backing fixture file."},
	}
}

func testDBProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"name":   {Type: "string", Description: "Database name."},
		"driver": {Type: "string", Description: "Database driver."},
		"dsn":    {Type: "string", Description: "Connection string or DSN."},
		"file":   {Type: "string", Description: "Optional schema or seed file."},
	}
}

func joinBatchTaskSpecs(taskSpecs map[string]string) string {
	if len(taskSpecs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(taskSpecs))
	for key := range taskSpecs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(taskSpecs[key]); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (gt *GlobalTester) mergeBatchContext(values map[string]any) *shared.BatchContext {
	base := gt.currentBatchContext()
	if base == nil {
		base = &shared.BatchContext{}
	}
	if len(values) == 0 {
		return base
	}
	data, err := json.Marshal(values)
	if err != nil {
		return base
	}
	var provided shared.BatchContext
	if err := json.Unmarshal(data, &provided); err != nil {
		return base
	}
	if len(provided.PipelineIDs) > 0 {
		base.PipelineIDs = uniqueSortedStrings(provided.PipelineIDs)
	}
	if len(provided.ChangedFiles) > 0 {
		base.ChangedFiles = gt.normalizeTargetFiles(provided.ChangedFiles, joinBatchTaskSpecs(provided.TaskSpecs))
	}
	if len(provided.TaskSpecs) > 0 {
		base.TaskSpecs = provided.TaskSpecs
	}
	return base
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func spansMultipleDirectories(files []string) bool {
	if len(files) < 2 {
		return false
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		dir := filepath.Dir(file)
		if dir == "." || dir == "" {
			dir = file
		}
		seen[dir] = struct{}{}
		if len(seen) > 1 {
			return true
		}
	}
	return false
}

func dedupeHarnessNeeds(needs []shared.HarnessNeed) []shared.HarnessNeed {
	seen := make(map[string]struct{}, len(needs))
	out := make([]shared.HarnessNeed, 0, len(needs))
	for _, need := range needs {
		key := need.Type + "|" + need.Target + "|" + need.Description
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, need)
	}
	return out
}

func dedupeFixtures(items []TestFixture) []TestFixture {
	seen := make(map[string]struct{}, len(items))
	out := make([]TestFixture, 0, len(items))
	for _, item := range items {
		key := item.Name + "|" + item.File + "|" + item.Type
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeMockServers(items []MockServer) []MockServer {
	seen := make(map[string]struct{}, len(items))
	out := make([]MockServer, 0, len(items))
	for _, item := range items {
		key := item.Name + "|" + item.Endpoint + "|" + item.File
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeTestDBs(items []TestDB) []TestDB {
	seen := make(map[string]struct{}, len(items))
	out := make([]TestDB, 0, len(items))
	for _, item := range items {
		key := item.Name + "|" + item.Driver + "|" + item.File
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func harnessNeedLabels(needs []shared.HarnessNeed) []string {
	labels := make([]string, 0, len(needs))
	for _, need := range needs {
		label := strings.TrimSpace(need.Type)
		if target := strings.TrimSpace(need.Target); target != "" {
			label += ":" + filepath.Base(target)
		}
		labels = append(labels, label)
	}
	return uniqueSortedStrings(labels)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// Phase 2.K / GT-A refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// report_to_orchestrator + report_to_architect + escalate_failure
// collapsed into one escalate_failure skill with a targets enum array.
// All three underlying routing paths (gt.reportToOrchestrator,
// gt.reportToArchitect, gt.escalateFailure) stay — the skill handler
// dispatches based on the targets array.
func escalateFailureSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		Targets       []string `json:"targets"`
		TestName      string   `json:"test_name"`
		Confidence    float64  `json:"confidence"`
		IsProductBug  bool     `json:"is_product_bug"`
		RootCause     string   `json:"root_cause"`
		SuggestedFix  string   `json:"suggested_fix"`
		AffectedTasks []string `json:"affected_tasks"`
	}

	return skills.NewSkill("escalate_failure").
		Description("Escalate a test failure to the orchestrator, the architect, or both. One primitive for every failure escalation path.\n\n" +
			"Targets:\n" +
			"- [\"orchestrator\"]: pause new work dispatching while the failure is investigated.\n" +
			"- [\"architect\"]: request plan-level correction with a suggested fix.\n" +
			"- [\"orchestrator\",\"architect\"]: critical failures that need both pause and plan fix.").
		Domain("testing").
		Keywords("escalate", "failure", "report", "orchestrator", "architect", "pause", "plan").
		Priority(95).
		Usage("Choose targets based on the failure's scope: orchestrator-only for dispatch pause, architect-only for plan revision, both for critical systemic failures.").
		Requirement("targets + test_name + confidence + is_product_bug + root_cause required. suggested_fix required when targets includes architect.").
		Satisfies("Publishes the failure escalation to each named target and returns the set of actually-routed targets.").
		Avoid("Do not use for speculative concerns or tentative evidence.").
		EnumArrayParam("targets", "Where to route the escalation", "string", []string{"orchestrator", "architect"}, true).
		StringParam("test_name", "Name of the failing test", true).
		FloatParam("confidence", "Confidence in the diagnosis (0-1)", true).
		BoolParam("is_product_bug", "Whether the failure is a product bug", true).
		StringParam("root_cause", "Root cause description", true).
		StringParam("suggested_fix", "Suggested fix approach (required when architect is targeted)", false).
		ArrayParam("affected_tasks", "Task IDs affected by the failure", "string", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			targets := normalizeEscalateFailureTargets(p.Targets)
			if len(targets) == 0 {
				return nil, fmt.Errorf("targets is required (expected any of: orchestrator, architect)")
			}
			wantArchitect := escalateFailureHasTarget(targets, "architect")
			wantOrchestrator := escalateFailureHasTarget(targets, "orchestrator")
			if wantArchitect && strings.TrimSpace(p.SuggestedFix) == "" {
				return nil, fmt.Errorf("suggested_fix is required when targets includes architect")
			}

			base := func() *shared.DiagnosisReport {
				r := &shared.DiagnosisReport{
					TestName:     p.TestName,
					Confidence:   p.Confidence,
					IsProductBug: p.IsProductBug,
					RootCauses: []shared.RootCause{{
						Description: p.RootCause,
					}},
					CreatedAt: time.Now(),
				}
				if strings.TrimSpace(p.SuggestedFix) != "" {
					r.SuggestedFix = []shared.SuggestedFix{{
						Description: p.SuggestedFix,
					}}
				}
				return r
			}

			switch {
			case wantOrchestrator && wantArchitect:
				if err := gt.escalateFailure(base(), p.AffectedTasks); err != nil {
					return nil, fmt.Errorf("escalate failure: %w", err)
				}
			case wantOrchestrator:
				if err := gt.reportToOrchestrator(base(), p.AffectedTasks); err != nil {
					return nil, fmt.Errorf("report to orchestrator: %w", err)
				}
			case wantArchitect:
				if err := gt.reportToArchitect(base(), p.AffectedTasks); err != nil {
					return nil, fmt.Errorf("report to architect: %w", err)
				}
			}

			return map[string]any{
				"escalated":      true,
				"targets":        targets,
				"test_name":      p.TestName,
				"affected_tasks": p.AffectedTasks,
			}, nil
		}).
		Build()
}

// normalizeEscalateFailureTargets dedupes + lowercases the enum array
// and keeps stable ordering (orchestrator-first) for result reporting.
func normalizeEscalateFailureTargets(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		switch t {
		case "orchestrator", "architect":
			seen[t] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	if _, ok := seen["orchestrator"]; ok {
		out = append(out, "orchestrator")
	}
	if _, ok := seen["architect"]; ok {
		out = append(out, "architect")
	}
	return out
}

func escalateFailureHasTarget(targets []string, target string) bool {
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}
