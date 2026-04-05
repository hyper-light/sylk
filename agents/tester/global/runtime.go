package global

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	coretest "github.com/adalundhe/sylk/core/test"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

type globalTestHarnessState struct {
	FrameworkID        coretest.TestFrameworkID `json:"framework_id"`
	FrameworkName      string                   `json:"framework_name"`
	Language           string                   `json:"language"`
	RunCommand         string                   `json:"run_command"`
	CoverageCommand    string                   `json:"coverage_command"`
	ConfigFiles        []string                 `json:"config_files,omitempty"`
	MissingConfigFiles []string                 `json:"missing_config_files,omitempty"`
	TargetFiles        []string                 `json:"target_files,omitempty"`
	ExistingTestFiles  []string                 `json:"existing_test_files,omitempty"`
	RecommendedOutputs map[string]string        `json:"recommended_outputs,omitempty"`
	PackagePatterns    []string                 `json:"package_patterns,omitempty"`
	SetupRequired      bool                     `json:"setup_required"`
	SetupReason        string                   `json:"setup_reason,omitempty"`
	CreatedFiles       []string                 `json:"created_files,omitempty"`
	DetectedAt         time.Time                `json:"detected_at"`
}

type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func (gt *GlobalTester) AnalyzeRisks(ctx context.Context, files []string, taskSpec, workerType, diffPatch string) ([]testershared.RiskArea, error) {
	return gt.analyzeRisks(ctx, files, joinTaskSpecAndDiff(taskSpec, diffPatch), workerType), nil
}

func (gt *GlobalTester) PlanTests(ctx context.Context, riskAreas []testershared.RiskArea, taskSpec string, files []string) (*testershared.TestPlan, error) {
	harness := gt.currentHarnessState()
	if harness == nil {
		detected, err := gt.detectHarness(ctx, files, taskSpec, "")
		if err == nil {
			harness = detected
		}
	}
	return gt.buildPlan(files, taskSpec, riskAreas, harness), nil
}

func (gt *GlobalTester) RunTestSuite(ctx context.Context, req testershared.SuiteRunRequest) (map[string]any, error) {
	return gt.executeSuite(ctx, gt.currentHarnessState(), req.Packages, req.Files, req.TestNames, req.Race, req.Verbose, req.Timeout)
}

func (gt *GlobalTester) currentHarnessState() *globalTestHarnessState {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	if gt.executionHarness == nil {
		return nil
	}
	copyState := *gt.executionHarness
	copyState.ConfigFiles = append([]string(nil), gt.executionHarness.ConfigFiles...)
	copyState.MissingConfigFiles = append([]string(nil), gt.executionHarness.MissingConfigFiles...)
	copyState.TargetFiles = append([]string(nil), gt.executionHarness.TargetFiles...)
	copyState.ExistingTestFiles = append([]string(nil), gt.executionHarness.ExistingTestFiles...)
	copyState.PackagePatterns = append([]string(nil), gt.executionHarness.PackagePatterns...)
	copyState.CreatedFiles = append([]string(nil), gt.executionHarness.CreatedFiles...)
	if gt.executionHarness.RecommendedOutputs != nil {
		copyState.RecommendedOutputs = make(map[string]string, len(gt.executionHarness.RecommendedOutputs))
		for k, v := range gt.executionHarness.RecommendedOutputs {
			copyState.RecommendedOutputs[k] = v
		}
	}
	return &copyState
}

func (gt *GlobalTester) setHarnessState(state *globalTestHarnessState) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.executionHarness = state
}

func (gt *GlobalTester) detectHarness(ctx context.Context, files []string, taskSpec, workerType string) (*globalTestHarnessState, error) {
	targetFiles := gt.normalizeTargetFiles(files, taskSpec)
	if len(targetFiles) == 0 {
		return nil, fmt.Errorf("no target files available for harness detection")
	}

	root := gt.workingDir()
	projectRuntime := purevfs.DefaultCatalog().DetectProject(root)
	language := inferLanguage(targetFiles, workerType)
	def := selectFrameworkDefinition(root, language, projectRuntime)
	if def == nil {
		return nil, fmt.Errorf("no supported test framework detected for %s", language)
	}

	existingTests := gt.findExistingTestFiles(ctx, def, targetFiles)
	outputs := make(map[string]string, len(targetFiles))
	for _, target := range targetFiles {
		outputs[target] = recommendedOutputFile(def, target)
	}

	state := &globalTestHarnessState{
		FrameworkID:        def.ID,
		FrameworkName:      def.Name,
		Language:           def.Language,
		RunCommand:         def.RunCommand,
		CoverageCommand:    def.CoverageCommand,
		ConfigFiles:        append([]string(nil), def.ConfigFiles...),
		MissingConfigFiles: gt.missingConfigFiles(ctx, def),
		TargetFiles:        targetFiles,
		ExistingTestFiles:  existingTests,
		RecommendedOutputs: outputs,
		PackagePatterns:    derivePackagePatterns(root, def, targetFiles),
		DetectedAt:         time.Now(),
	}
	state.SetupRequired, state.SetupReason = harnessSetupRequirement(def, state.MissingConfigFiles)
	gt.setHarnessState(state)
	return state, nil
}

func (gt *GlobalTester) normalizeTargetFiles(files []string, taskSpec string) []string {
	candidates := append([]string(nil), files...)
	if len(candidates) == 0 {
		gt.mu.RLock()
		batch := gt.batchContext
		gt.mu.RUnlock()
		if batch != nil {
			candidates = append(candidates, batch.ChangedFiles...)
			for _, spec := range batch.TaskSpecs {
				candidates = append(candidates, extractTaskFilesFromPrompt(spec)...)
			}
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, extractTaskFilesFromPrompt(taskSpec)...)
	}

	root := gt.workingDir()
	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		path := normalizePathCandidate(root, candidate)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func (gt *GlobalTester) analyzeRisks(_ context.Context, files []string, taskSpec, workerType string) []testershared.RiskArea {
	targetFiles := gt.normalizeTargetFiles(files, taskSpec)
	risks := make([]testershared.RiskArea, 0, len(targetFiles)*2)

	for _, file := range targetFiles {
		content := gt.readFileString(context.Background(), file)
		for _, risk := range inferFileRisks(file, content, taskSpec, workerType) {
			risks = append(risks, risk)
		}
	}

	if len(risks) == 0 && len(targetFiles) > 0 {
		for _, file := range targetFiles {
			risks = append(risks, testershared.RiskArea{
				File:        file,
				Category:    testershared.RiskLogic,
				Level:       testershared.RiskMedium,
				Description: "Merged behavior must be validated against the requested acceptance criteria and surrounding system contract.",
			})
		}
	}
	return dedupeRiskAreas(risks)
}

func (gt *GlobalTester) buildPlan(files []string, taskSpec string, risks []testershared.RiskArea, harness *globalTestHarnessState) *testershared.TestPlan {
	targetFiles := gt.normalizeTargetFiles(files, taskSpec)
	if harness == nil {
		harness, _ = gt.detectHarness(context.Background(), targetFiles, taskSpec, "")
	}

	cases := make([]testershared.PlannedTestCase, 0, len(risks)+len(targetFiles))
	criteria := append(extractPromptList(taskSpec, "Acceptance Criteria"), extractPromptList(taskSpec, "Success Criteria")...)
	for idx, criterion := range criteria {
		target := pickTargetFile(targetFiles, idx)
		if target == "" {
			continue
		}
		cases = append(cases, testershared.PlannedTestCase{
			Name:              formatPlannedTestName(target, criterion),
			Category:          string(mapCriterionCategory(criterion)),
			FailureHypothesis: fmt.Sprintf("The merged implementation does not satisfy criterion: %s", criterion),
			InputStrategy:     "Exercise the public contract with deterministic boundary inputs and realistic surrounding state.",
			ExpectedBehavior:  criterion,
			TargetFile:        target,
		})
	}

	for _, risk := range risks {
		cases = append(cases, testershared.PlannedTestCase{
			Name:              formatRiskTestName(risk),
			Category:          string(mapRiskToCategory(risk.Category)),
			FailureHypothesis: risk.Description,
			InputStrategy:     inputStrategyForRisk(risk.Category),
			ExpectedBehavior:  expectedBehaviorForRisk(risk),
			TargetFile:        risk.File,
		})
	}

	cases = dedupePlannedCases(cases)
	plan := &testershared.TestPlan{
		ID:          "plan_" + uuid.NewString()[:8],
		Rationale:   buildPlanRationale(taskSpec, risks, harness),
		RiskAreas:   append([]testershared.RiskArea(nil), risks...),
		PlannedCase: cases,
		CreatedAt:   time.Now(),
	}

	gt.mu.Lock()
	gt.currentPlan = plan
	gt.mu.Unlock()
	return plan
}

func (gt *GlobalTester) executeSuite(ctx context.Context, harness *globalTestHarnessState, packages, files, testNames []string, race, verbose bool, timeoutSeconds int) (map[string]any, error) {
	if harness == nil {
		var err error
		harness, err = gt.detectHarness(ctx, files, "", "")
		if err != nil {
			return nil, err
		}
	}
	switch harness.FrameworkID {
	case coretest.FrameworkGoTest:
		return gt.runGoTestSuite(ctx, harness, packages, files, testNames, race, verbose, timeoutSeconds)
	default:
		return gt.runGenericSuite(ctx, harness, packages, files, testNames, verbose, timeoutSeconds)
	}
}

func (gt *GlobalTester) executionPlan(ctx context.Context, harness *globalTestHarnessState, workDir string) (purevfs.ExecutionPlan, error) {
	overlay, overlayDeletes := globalTesterOverlayState(gt.fileAccess)
	planner := purevfs.NewExecutionPlanner(nil, gt.executionCapabilities(ctx))
	return planner.Plan(purevfs.ExecutionRequest{
		Mode:           purevfs.ExecutionModeStrictNoDisk,
		Intent:         purevfs.ExecutionIntentTest,
		Language:       harness.Language,
		FrameworkID:    string(harness.FrameworkID),
		WorkspaceRoot:  workDir,
		WorkingDir:     workDir,
		Overlay:        overlay,
		OverlayDeletes: overlayDeletes,
	})
}

func (gt *GlobalTester) runGoTestSuite(ctx context.Context, harness *globalTestHarnessState, packages, files, testNames []string, race, verbose bool, timeoutSeconds int) (map[string]any, error) {
	workDir := gt.workingDir()
	plan, err := gt.executionPlan(ctx, harness, workDir)
	if err != nil {
		return nil, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}

	args := []string{"test", "-json", "-count=1"}
	if plan.Strategy == purevfs.StrategyGoOverlayManifest {
		if overlayPath, overlayCleanup, overlayErr := gt.buildGoOverlay(ctx); overlayErr == nil && overlayPath != "" {
			defer overlayCleanup()
			args = append(args, "-overlay="+overlayPath)
		}
	}
	if race {
		args = append(args, "-race")
	}
	if verbose {
		args = append(args, "-v")
	}
	if timeoutSeconds > 0 {
		args = append(args, fmt.Sprintf("-timeout=%ds", timeoutSeconds))
	}
	if len(testNames) > 0 {
		args = append(args, "-run", strings.Join(testNames, "|"))
	}

	patterns := packages
	if len(patterns) == 0 {
		patterns = harness.PackagePatterns
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	args = append(args, patterns...)

	authReq := commandapproval.Request{
		Command:       "go " + strings.Join(args, " "),
		WorkingDir:    workDir,
		WorkspaceRoot: gt.workingDir(),
		ToolName:      "go_test",
		AgentID:       gt.id,
		AgentType:     "tester",
		SessionID:     versioning.SessionIDFromContext(ctx),
	}
	agentshared.PopulateCommandApprovalScope(ctx, &authReq)
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
		return nil, agentshared.WrapApprovalDenied(authReq.ToolName, err)
	}

	cmdCtx := ctx
	if timeoutSeconds > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		defer cancel()
	}
	if gt.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := gt.executionBroker.Run(cmdCtx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      append([]string{"go"}, args...),
		Workspace: gt.executionWorkspace(true),
	})
	if err != nil {
		return nil, fmt.Errorf("go test: %w", err)
	}
	output := append(append([]byte(nil), runResult.Stdout...), runResult.Stderr...)
	result := parseGoTestJSON(output)
	result["command"] = "go " + strings.Join(args, " ")
	result["execution_strategy"] = plan.Strategy
	result["execution_mode"] = plan.Mode
	result["run_dir"] = plan.WorkingDir
	result["materialized"] = plan.RequiresMaterialize
	result["truncated"] = runResult.StdoutTruncated || runResult.StderrTruncated
	if runResult.ExitCode != 0 {
		result["exit_code"] = runResult.ExitCode
	}
	return result, nil
}

func (gt *GlobalTester) runGenericSuite(ctx context.Context, harness *globalTestHarnessState, packages, files, testNames []string, verbose bool, timeoutSeconds int) (map[string]any, error) {
	workDir := gt.workingDir()
	plan, err := gt.executionPlan(ctx, harness, workDir)
	if err != nil {
		return nil, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}

	command := harness.RunCommand
	if len(files) == 1 && strings.TrimSpace(files[0]) != "" {
		command = strings.ReplaceAll(commandForFile(harness, files[0]), "{file}", files[0])
	}
	command = strings.ReplaceAll(command, "{test}", strings.Join(testNames, "|"))
	if issue, ok := agentshared.DetectShellControlOperator(command); ok {
		return nil, fmt.Errorf("run_test_suite generated unsupported shell syntax (%s); use run_shell_script only if the harness truly requires compound shell execution", issue)
	}

	args, err := commandapproval.SplitCommand(command)
	if err != nil {
		return nil, fmt.Errorf("invalid test command: %w", err)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty test command")
	}

	authReq := commandapproval.Request{
		Command:       command,
		WorkingDir:    workDir,
		WorkspaceRoot: gt.workingDir(),
		ToolName:      string(harness.FrameworkID),
		AgentID:       gt.id,
		AgentType:     "tester",
		SessionID:     versioning.SessionIDFromContext(ctx),
	}
	agentshared.PopulateCommandApprovalScope(ctx, &authReq)
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
		return nil, agentshared.WrapApprovalDenied(authReq.ToolName, err)
	}

	cmdCtx := ctx
	if timeoutSeconds > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		defer cancel()
	}
	if gt.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := gt.executionBroker.Run(cmdCtx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      args,
		Workspace: gt.executionWorkspace(true),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", args[0], err)
	}
	passed := runResult.ExitCode == 0
	return map[string]any{
		"framework":          harness.FrameworkID,
		"command":            command,
		"execution_strategy": plan.Strategy,
		"execution_mode":     plan.Mode,
		"packages":           packages,
		"files":              files,
		"passed":             boolToCount(passed),
		"failed":             boolToCount(!passed),
		"output":             string(runResult.Stdout),
		"stderr":             string(runResult.Stderr),
		"exit_code":          runResult.ExitCode,
		"truncated":          runResult.StdoutTruncated || runResult.StderrTruncated,
		"verbose":            verbose,
		"run_dir":            plan.WorkingDir,
		"materialized":       plan.RequiresMaterialize,
	}, nil
}

func (gt *GlobalTester) buildGoOverlay(_ context.Context) (string, func(), error) {
	fa, ok := gt.fileAccess.(globalTesterOverlayAwareFileAccess)
	if !ok {
		return "", func() {}, nil
	}
	mods := fa.Modifications()
	if len(mods) == 0 {
		return "", func() {}, nil
	}

	tmpDir, err := os.MkdirTemp("", "sylk-global-go-overlay-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	type overlaySpec struct {
		Replace map[string]string `json:"Replace"`
	}
	spec := overlaySpec{Replace: make(map[string]string, len(mods))}
	root := gt.workingDir()

	for idx, mod := range mods {
		if mod.Operation == versioning.FileOpDelete {
			cleanup()
			return "", nil, fmt.Errorf("go overlay does not support deleted files")
		}
		rel, err := filepath.Rel(root, mod.OriginalPath)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		overlayPath := filepath.Join(tmpDir, fmt.Sprintf("%03d_%s", idx, filepath.Base(rel)))
		if err := os.WriteFile(overlayPath, mod.NewContent, 0o644); err != nil {
			cleanup()
			return "", nil, err
		}
		spec.Replace[mod.OriginalPath] = overlayPath
	}

	overlayJSON, err := json.Marshal(spec)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	overlayFile := filepath.Join(tmpDir, "overlay.json")
	if err := os.WriteFile(overlayFile, overlayJSON, 0o644); err != nil {
		cleanup()
		return "", nil, err
	}
	return overlayFile, cleanup, nil
}

func (gt *GlobalTester) fileExists(ctx context.Context, path string) (bool, error) {
	if gt.fileAccess == nil {
		return false, fmt.Errorf("file access unavailable")
	}
	_, err := gt.fileAccess.ReadFile(ctx, path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (gt *GlobalTester) readExistingFile(ctx context.Context, path string) ([]byte, error) {
	if gt.fileAccess == nil {
		return nil, fmt.Errorf("file access unavailable")
	}
	return gt.fileAccess.ReadFile(ctx, path)
}

func (gt *GlobalTester) readFileString(ctx context.Context, path string) string {
	content, err := gt.readExistingFile(ctx, path)
	if err != nil {
		return ""
	}
	return string(content)
}

func (gt *GlobalTester) missingConfigFiles(ctx context.Context, def *coretest.TestFrameworkDefinition) []string {
	if len(def.ConfigFiles) == 0 {
		return nil
	}
	missing := make([]string, 0, len(def.ConfigFiles))
	for _, cfg := range def.ConfigFiles {
		exists, err := gt.fileExists(ctx, cfg)
		if err != nil || !exists {
			missing = append(missing, cfg)
		}
	}
	return missing
}

func (gt *GlobalTester) findExistingTestFiles(ctx context.Context, def *coretest.TestFrameworkDefinition, targets []string) []string {
	seen := make(map[string]struct{}, len(targets))
	found := make([]string, 0, len(targets))
	for _, target := range targets {
		output := recommendedOutputFile(def, target)
		ok, err := gt.fileExists(ctx, output)
		if err != nil || !ok {
			continue
		}
		if _, exists := seen[output]; exists {
			continue
		}
		seen[output] = struct{}{}
		found = append(found, output)
	}
	sort.Strings(found)
	return found
}

func selectFrameworkDefinition(root, language string, runtime purevfs.ProjectRuntimeSummary) *coretest.TestFrameworkDefinition {
	selector := coretest.NewTestFrameworkSelector()
	candidates := uniqueStrings([]string{
		language,
		coretest.CanonicalFrameworkLanguage(language),
		runtime.PrimaryLanguage,
		coretest.CanonicalFrameworkLanguage(runtime.PrimaryLanguage),
	})
	for _, candidate := range candidates {
		if def := selector.SelectFrameworkByLanguage(root, candidate); def != nil {
			return def
		}
	}
	for _, framework := range runtime.Frameworks {
		id := coretest.TestFrameworkID(framework.ID)
		if def := frameworkDefinition(id); def != nil && (def.Enabled == nil || def.Enabled(root)) {
			return def
		}
	}
	for _, candidate := range candidates {
		if def := fallbackFramework(candidate); def != nil {
			return def
		}
	}
	if def := selector.SelectFramework(root); def != nil {
		return def
	}
	for _, framework := range runtime.Frameworks {
		if def := frameworkDefinition(coretest.TestFrameworkID(framework.ID)); def != nil {
			return def
		}
	}
	return nil
}

func fallbackFramework(language string) *coretest.TestFrameworkDefinition {
	return coretest.FallbackFrameworkForLanguage(language)
}

func inferLanguage(files []string, workerType string) string {
	return purevfs.InferPrimaryLanguage(files, workerType)
}

func recommendedOutputFile(def *coretest.TestFrameworkDefinition, target string) string {
	if def == nil {
		dir := filepath.Dir(target)
		base := filepath.Base(target)
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		return filepath.Join(dir, stem+".test"+ext)
	}

	dir := filepath.Dir(target)
	base := filepath.Base(target)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	switch def.ID {
	case coretest.FrameworkGoTest:
		return filepath.Join(dir, stem+"_test.go")
	case coretest.FrameworkVitest, coretest.FrameworkJest, coretest.FrameworkMocha, coretest.FrameworkBunTest, coretest.FrameworkNodeTest:
		return filepath.Join(dir, stem+".test"+ext)
	case coretest.FrameworkPytest, coretest.FrameworkPythonUnitTest:
		return filepath.Join(dir, "test_"+stem+".py")
	case coretest.FrameworkCargoTest:
		return filepath.ToSlash(filepath.Join("tests", stem+"_test.rs"))
	case coretest.FrameworkRSpec:
		return filepath.Join(dir, stem+"_spec.rb")
	case coretest.FrameworkPHPUnit:
		return filepath.ToSlash(filepath.Join("tests", stem+"Test.php"))
	case coretest.FrameworkMavenTest, coretest.FrameworkGradleTest:
		return recommendedJVMTestPath(target)
	case coretest.FrameworkSBTTest, coretest.FrameworkMillTest:
		return recommendedScalaTestPath(target)
	case coretest.FrameworkDotNetTest:
		return filepath.Join(dir, stem+"Tests.cs")
	case coretest.FrameworkCTest:
		return filepath.Join(dir, stem+"_test"+ext)
	case coretest.FrameworkSwiftTest:
		return recommendedSwiftTestPath(target)
	case coretest.FrameworkZigTest:
		return filepath.Join(dir, stem+"_test.zig")
	case coretest.FrameworkDartTest:
		return recommendedDartTestPath(target)
	case coretest.FrameworkExUnit:
		return recommendedElixirTestPath(target)
	case coretest.FrameworkRebar3EUnit:
		return recommendedErlangTestPath(target)
	case coretest.FrameworkStackTest, coretest.FrameworkCabalTest:
		return filepath.Join(dir, stem+"Spec.hs")
	default:
		return filepath.Join(dir, stem+".test"+ext)
	}
}

func recommendedJVMTestPath(target string) string {
	slash := filepath.ToSlash(target)
	switch {
	case strings.HasPrefix(slash, "src/main/java/"):
		slash = strings.Replace(slash, "src/main/java/", "src/test/java/", 1)
	case strings.HasPrefix(slash, "src/main/kotlin/"):
		slash = strings.Replace(slash, "src/main/kotlin/", "src/test/kotlin/", 1)
	case strings.Contains(slash, "/src/main/java/"):
		slash = strings.Replace(slash, "/src/main/java/", "/src/test/java/", 1)
	case strings.Contains(slash, "/src/main/kotlin/"):
		slash = strings.Replace(slash, "/src/main/kotlin/", "/src/test/kotlin/", 1)
	}
	ext := filepath.Ext(slash)
	stem := strings.TrimSuffix(filepath.Base(slash), ext)
	dir := filepath.Dir(slash)
	return filepath.FromSlash(path.Join(dir, stem+"Test"+ext))
}

func recommendedSwiftTestPath(target string) string {
	slash := filepath.ToSlash(target)
	parts := strings.Split(strings.TrimPrefix(slash, "./"), "/")
	for idx := range parts {
		if parts[idx] != "Sources" || idx+1 >= len(parts) {
			continue
		}
		module := parts[idx+1]
		ext := filepath.Ext(slash)
		stem := strings.TrimSuffix(filepath.Base(slash), ext)
		return filepath.FromSlash(path.Join("Tests", module+"Tests", stem+"Tests"+ext))
	}
	ext := filepath.Ext(slash)
	stem := strings.TrimSuffix(filepath.Base(slash), ext)
	return filepath.FromSlash(path.Join("Tests", stem+"Tests"+ext))
}

func recommendedScalaTestPath(target string) string {
	slash := filepath.ToSlash(target)
	switch {
	case strings.HasPrefix(slash, "src/main/scala/"):
		slash = strings.Replace(slash, "src/main/scala/", "src/test/scala/", 1)
	case strings.Contains(slash, "/src/main/scala/"):
		slash = strings.Replace(slash, "/src/main/scala/", "/src/test/scala/", 1)
	}
	ext := filepath.Ext(slash)
	stem := strings.TrimSuffix(filepath.Base(slash), ext)
	dir := filepath.Dir(slash)
	return filepath.FromSlash(path.Join(dir, stem+"Test"+ext))
}

func recommendedDartTestPath(target string) string {
	slash := filepath.ToSlash(target)
	ext := filepath.Ext(slash)
	stem := strings.TrimSuffix(filepath.Base(slash), ext)
	switch {
	case strings.HasPrefix(slash, "lib/"):
		dir := strings.TrimPrefix(strings.TrimPrefix(path.Dir(slash), "lib"), "/")
		return filepath.FromSlash(path.Join("test", dir, stem+"_test.dart"))
	case strings.HasPrefix(slash, "test/"):
		return filepath.FromSlash(path.Join(path.Dir(slash), stem+"_test.dart"))
	default:
		return filepath.Join(filepath.Dir(target), stem+"_test.dart")
	}
}

func recommendedElixirTestPath(target string) string {
	slash := filepath.ToSlash(target)
	ext := filepath.Ext(slash)
	stem := strings.TrimSuffix(filepath.Base(slash), ext)
	if strings.HasPrefix(slash, "lib/") {
		dir := strings.TrimPrefix(strings.TrimPrefix(path.Dir(slash), "lib"), "/")
		return filepath.FromSlash(path.Join("test", dir, stem+"_test.exs"))
	}
	return filepath.Join(filepath.Dir(target), stem+"_test.exs")
}

func recommendedErlangTestPath(target string) string {
	slash := filepath.ToSlash(target)
	ext := filepath.Ext(slash)
	stem := strings.TrimSuffix(filepath.Base(slash), ext)
	if strings.HasPrefix(slash, "src/") {
		return filepath.FromSlash(path.Join("test", stem+"_tests.erl"))
	}
	return filepath.Join(filepath.Dir(target), stem+"_tests.erl")
}

func derivePackagePatterns(root string, def *coretest.TestFrameworkDefinition, files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	patterns := make([]string, 0, len(files))
	for _, file := range files {
		dir := filepath.Dir(file)
		var pattern string
		switch def.ID {
		case coretest.FrameworkGoTest:
			if dir == "." || dir == "" {
				pattern = "."
			} else {
				pattern = "./" + filepath.ToSlash(dir)
			}
		default:
			pattern = file
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	if len(patterns) == 0 {
		return []string{"./..."}
	}
	return patterns
}

func harnessSetupRequirement(def *coretest.TestFrameworkDefinition, missing []string) (bool, string) {
	switch def.ID {
	case coretest.FrameworkGoTest,
		coretest.FrameworkCargoTest,
		coretest.FrameworkRSpec,
		coretest.FrameworkPHPUnit,
		coretest.FrameworkSBTTest,
		coretest.FrameworkMillTest,
		coretest.FrameworkDotNetTest,
		coretest.FrameworkCTest,
		coretest.FrameworkSwiftTest,
		coretest.FrameworkZigTest,
		coretest.FrameworkDartTest,
		coretest.FrameworkExUnit,
		coretest.FrameworkRebar3EUnit,
		coretest.FrameworkStackTest,
		coretest.FrameworkCabalTest:
		return false, "framework provides a built-in harness"
	}
	if len(missing) > 0 {
		return true, "framework configuration is missing"
	}
	return false, "existing project tooling is sufficient"
}

func extractTaskFilesFromPrompt(prompt string) []string {
	lines := strings.Split(prompt, "\n")
	var (
		inSection bool
		files     []string
	)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(trimmed, "Affected Files:"),
			strings.EqualFold(trimmed, "Workspace Write Set:"),
			strings.EqualFold(trimmed, "Workspace Test Surface:"),
			strings.EqualFold(trimmed, "Workspace Read Set:"):
			inSection = true
			continue
		case strings.HasSuffix(trimmed, ":"):
			inSection = false
		}
		if !inSection || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		files = append(files, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
	}
	return files
}

func extractPromptList(prompt, title string) []string {
	lines := strings.Split(prompt, "\n")
	var (
		inSection bool
		items     []string
	)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, title+":") {
			inSection = true
			continue
		}
		if inSection && strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "- ") {
			break
		}
		if inSection && strings.HasPrefix(trimmed, "- ") {
			items = append(items, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		}
	}
	return items
}

func normalizePathCandidate(root, candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || !looksLikePath(candidate) {
		return ""
	}
	if filepath.IsAbs(candidate) {
		if rel, err := filepath.Rel(root, candidate); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		return filepath.ToSlash(candidate)
	}
	return filepath.ToSlash(filepath.Clean(candidate))
}

func looksLikePath(value string) bool {
	if strings.ContainsAny(value, "/\\") {
		return true
	}
	ext := filepath.Ext(value)
	return ext != "" && !strings.ContainsRune(ext, ' ')
}

func inferFileRisks(file, content, taskSpec, workerType string) []testershared.RiskArea {
	var risks []testershared.RiskArea
	lowerContent := strings.ToLower(content)
	lowerSpec := strings.ToLower(taskSpec)

	addRisk := func(category testershared.RiskCategory, level testershared.RiskLevel, description string) {
		risks = append(risks, testershared.RiskArea{
			File:        file,
			Category:    category,
			Level:       level,
			Description: description,
		})
	}

	if strings.Contains(lowerContent, "go ") || strings.Contains(lowerContent, "sync.") || strings.Contains(lowerSpec, "concurrent") {
		addRisk(testershared.RiskConcurrency, testershared.RiskHigh, "Concurrent access and goroutine coordination may violate the merged contract under load.")
	}
	if strings.Contains(lowerContent, "open(") || strings.Contains(lowerContent, "close(") || strings.Contains(lowerContent, "defer ") || strings.Contains(lowerContent, "context.") {
		addRisk(testershared.RiskResource, testershared.RiskMedium, "Resource lifecycle management needs coverage for cleanup, cancellation, and leak prevention.")
	}
	if strings.Contains(lowerContent, "json.") || strings.Contains(lowerContent, "strconv.") || strings.Contains(lowerContent, "len(") || strings.Contains(lowerSpec, "edge") || strings.Contains(lowerSpec, "boundary") {
		addRisk(testershared.RiskBoundary, testershared.RiskMedium, "Boundary inputs and malformed payloads should be exercised explicitly.")
	}
	if strings.Contains(lowerContent, "sql") || strings.Contains(lowerContent, "token") || strings.Contains(lowerContent, "auth") || strings.Contains(lowerSpec, "security") {
		addRisk(testershared.RiskSecurity, testershared.RiskHigh, "Security-sensitive inputs and authorization paths require negative coverage.")
	}
	if strings.Contains(lowerContent, "map[") || strings.Contains(lowerContent, "cache") || strings.Contains(lowerContent, "state") || strings.Contains(lowerSpec, "state") {
		addRisk(testershared.RiskState, testershared.RiskMedium, "State transitions and repeated operations can drift from the intended contract.")
	}
	if workerType == "designer" {
		addRisk(testershared.RiskAccessibility, testershared.RiskHigh, "User-facing output must preserve accessibility semantics and interaction affordances.")
	}
	return risks
}

func dedupeRiskAreas(risks []testershared.RiskArea) []testershared.RiskArea {
	seen := make(map[string]struct{}, len(risks))
	result := make([]testershared.RiskArea, 0, len(risks))
	for _, risk := range risks {
		key := fmt.Sprintf("%s|%s|%s", risk.File, risk.Category, risk.Description)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, risk)
	}
	return result
}

func buildPlanRationale(taskSpec string, risks []testershared.RiskArea, harness *globalTestHarnessState) string {
	var parts []string
	if harness != nil {
		parts = append(parts, fmt.Sprintf("Use %s as the execution harness.", harness.FrameworkName))
	}
	if len(risks) > 0 {
		parts = append(parts, fmt.Sprintf("Prioritize %d identified risk areas before lower-signal coverage.", len(risks)))
	}
	if criteria := len(extractPromptList(taskSpec, "Acceptance Criteria")) + len(extractPromptList(taskSpec, "Success Criteria")); criteria > 0 {
		parts = append(parts, "Map tests directly to the requested merged-state contract so failures stay specification-driven.")
	}
	if len(parts) == 0 {
		return "Generate a small, specification-driven test set that exercises the most defect-prone merged behavior first."
	}
	return strings.Join(parts, " ")
}

func pickTargetFile(files []string, idx int) string {
	if len(files) == 0 {
		return ""
	}
	return files[idx%len(files)]
}

func mapCriterionCategory(criterion string) testershared.RiskCategory {
	lower := strings.ToLower(criterion)
	switch {
	case strings.Contains(lower, "accessib"), strings.Contains(lower, "contrast"):
		return testershared.RiskAccessibility
	case strings.Contains(lower, "token"):
		return testershared.RiskTokenMisuse
	case strings.Contains(lower, "error"), strings.Contains(lower, "invalid"):
		return testershared.RiskBoundary
	default:
		return testershared.RiskLogic
	}
}

func mapRiskToCategory(category testershared.RiskCategory) string {
	switch category {
	case testershared.RiskConcurrency:
		return testershared.CategoryRaceCondition
	case testershared.RiskSecurity:
		return testershared.CategoryNegative
	case testershared.RiskBoundary:
		return testershared.CategoryBoundary
	case testershared.RiskAccessibility:
		return testershared.CategoryAccessibility
	default:
		return testershared.CategoryEdgeCase
	}
}

func inputStrategyForRisk(category testershared.RiskCategory) string {
	switch category {
	case testershared.RiskConcurrency:
		return "Run concurrent callers against shared state and assert deterministic outcomes."
	case testershared.RiskSecurity:
		return "Send malformed and adversarial inputs that should be rejected."
	case testershared.RiskBoundary:
		return "Exercise zero, empty, nil, and maximum-size inputs."
	case testershared.RiskAccessibility:
		return "Verify required semantics and keyboard affordances in rendered output."
	default:
		return "Drive the smallest public API surface that can falsify the hypothesis."
	}
}

func expectedBehaviorForRisk(risk testershared.RiskArea) string {
	switch risk.Category {
	case testershared.RiskConcurrency:
		return "Concurrent execution remains race-free and preserves correctness."
	case testershared.RiskSecurity:
		return "Unsafe or unauthorized inputs are rejected safely."
	case testershared.RiskBoundary:
		return "Boundary inputs produce the documented success or error contract."
	case testershared.RiskAccessibility:
		return "The output remains accessible and semantically complete."
	default:
		return "The implementation satisfies the requested contract under the identified risk."
	}
}

func formatPlannedTestName(targetFile, criterion string) string {
	base := strings.TrimSuffix(filepath.Base(targetFile), filepath.Ext(targetFile))
	return "Test" + sanitizeIdentifier(base+"_"+criterion)
}

func formatRiskTestName(risk testershared.RiskArea) string {
	base := strings.TrimSuffix(filepath.Base(risk.File), filepath.Ext(risk.File))
	return "Test" + sanitizeIdentifier(base+"_"+string(risk.Category))
}

func sanitizeIdentifier(value string) string {
	var b strings.Builder
	capNext := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if capNext {
				b.WriteRune(unicode.ToUpper(r))
				capNext = false
			} else {
				b.WriteRune(r)
			}
			continue
		}
		capNext = true
	}
	if b.Len() == 0 {
		return "GeneratedCase"
	}
	return b.String()
}

func dedupePlannedCases(cases []testershared.PlannedTestCase) []testershared.PlannedTestCase {
	seen := make(map[string]struct{}, len(cases))
	result := make([]testershared.PlannedTestCase, 0, len(cases))
	for _, tc := range cases {
		key := tc.Name + "|" + tc.TargetFile
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, tc)
	}
	return result
}

func frameworkDefinition(id coretest.TestFrameworkID) *coretest.TestFrameworkDefinition {
	for _, def := range coretest.BuiltinFrameworks {
		if def.ID == id {
			return def
		}
	}
	return nil
}

func commandForFile(harness *globalTestHarnessState, file string) string {
	if def := frameworkDefinition(harness.FrameworkID); def != nil && strings.TrimSpace(def.RunFileCommand) != "" {
		return strings.ReplaceAll(def.RunFileCommand, "{file}", file)
	}
	return harness.RunCommand
}

func parseGoTestJSON(output []byte) map[string]any {
	type goCase struct {
		Name    string
		Package string
		Status  string
		Output  strings.Builder
		Elapsed time.Duration
	}

	cases := make(map[string]*goCase)
	var passed, failed, skipped int

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Bytes()
		var evt goTestEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if evt.Test == "" {
			continue
		}
		key := evt.Package + "::" + evt.Test
		tc, ok := cases[key]
		if !ok {
			tc = &goCase{Name: evt.Test, Package: evt.Package}
			cases[key] = tc
		}
		if evt.Output != "" {
			tc.Output.WriteString(evt.Output)
		}
		switch evt.Action {
		case "pass":
			tc.Status = "passed"
			tc.Elapsed = time.Duration(evt.Elapsed * float64(time.Second))
			passed++
		case "fail":
			tc.Status = "failed"
			tc.Elapsed = time.Duration(evt.Elapsed * float64(time.Second))
			failed++
		case "skip":
			tc.Status = "skipped"
			tc.Elapsed = time.Duration(evt.Elapsed * float64(time.Second))
			skipped++
		}
	}

	results := make([]map[string]any, 0, len(cases))
	for _, tc := range cases {
		results = append(results, map[string]any{
			"name":     tc.Name,
			"package":  tc.Package,
			"status":   tc.Status,
			"duration": tc.Elapsed.String(),
			"output":   strings.TrimSpace(tc.Output.String()),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		left := results[i]["package"].(string) + "::" + results[i]["name"].(string)
		right := results[j]["package"].(string) + "::" + results[j]["name"].(string)
		return left < right
	})

	return map[string]any{
		"passed":  passed,
		"failed":  failed,
		"skipped": skipped,
		"results": results,
		"output":  string(output),
	}
}

func boolToCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func joinTaskSpecAndDiff(taskSpec, diff string) string {
	if strings.TrimSpace(diff) == "" {
		return taskSpec
	}
	if strings.TrimSpace(taskSpec) == "" {
		return diff
	}
	return taskSpec + "\n\nDiff:\n" + diff
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

var (
	goPkgRE         = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z0-9_]+)\b`)
	goImportBlockRE = regexp.MustCompile(`(?ms)^\s*import\s*\((.*?)\)\s*`)
	goImportLineRE  = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"\s*$`)
	goStringRE      = regexp.MustCompile(`"([^"]+)"`)
)
