package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	coretest "github.com/adalundhe/sylk/core/test"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

type overlayAwareFileAccess interface {
	versioning.FileAccess
	RegisterVisiblePath(path string)
	VisiblePaths(root string) []string
	Modifications() []versioning.FileModification
}

type pipelineWritePlan struct {
	Path    string
	Content string
}

const maxPipelineWriteAttempts = 2

type testHarnessState struct {
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

type taskRuntimeSnapshot struct {
	task *agentshared.PipelineTaskInput
}

type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

type goSourceParts struct {
	packageName string
	imports     []string
	body        string
}

func (pt *PipelineTester) swapTaskRuntime(task *agentshared.PipelineTaskInput) taskRuntimeSnapshot {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	prev := taskRuntimeSnapshot{
		task: pt.currentTask,
	}
	pt.currentTask = task
	pt.lastSuiteResult = nil
	pt.currentPlan = nil
	pt.diagnoses = make(map[string]*testershared.DiagnosisReport)
	return prev
}

func (pt *PipelineTester) restoreTaskRuntime(prev taskRuntimeSnapshot) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.currentTask = prev.task
}

func (pt *PipelineTester) currentHarnessState() *testHarnessState {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if pt.harness == nil {
		return nil
	}
	copyState := *pt.harness
	copyState.TargetFiles = append([]string(nil), pt.harness.TargetFiles...)
	copyState.ConfigFiles = append([]string(nil), pt.harness.ConfigFiles...)
	copyState.MissingConfigFiles = append([]string(nil), pt.harness.MissingConfigFiles...)
	copyState.ExistingTestFiles = append([]string(nil), pt.harness.ExistingTestFiles...)
	copyState.PackagePatterns = append([]string(nil), pt.harness.PackagePatterns...)
	copyState.CreatedFiles = append([]string(nil), pt.harness.CreatedFiles...)
	if pt.harness.RecommendedOutputs != nil {
		copyState.RecommendedOutputs = make(map[string]string, len(pt.harness.RecommendedOutputs))
		for k, v := range pt.harness.RecommendedOutputs {
			copyState.RecommendedOutputs[k] = v
		}
	}
	return &copyState
}

func (pt *PipelineTester) setHarnessState(state *testHarnessState) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.harness = state
}

func (pt *PipelineTester) planSnapshot() *testershared.TestPlan {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if pt.currentPlan == nil {
		return nil
	}
	copyPlan := *pt.currentPlan
	copyPlan.RiskAreas = append([]testershared.RiskArea(nil), pt.currentPlan.RiskAreas...)
	copyPlan.PlannedCase = append([]testershared.PlannedTestCase(nil), pt.currentPlan.PlannedCase...)
	return &copyPlan
}

func (pt *PipelineTester) setLastSuiteResult(result *tester.TestSuiteResult) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if result == nil {
		pt.lastSuiteResult = nil
		return
	}
	copyResult := *result
	copyResult.Results = append([]tester.TestResult(nil), result.Results...)
	pt.lastSuiteResult = &copyResult
}

func (pt *PipelineTester) lastSuiteSnapshot() *tester.TestSuiteResult {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if pt.lastSuiteResult == nil {
		return nil
	}
	copyResult := *pt.lastSuiteResult
	copyResult.Results = append([]tester.TestResult(nil), pt.lastSuiteResult.Results...)
	return &copyResult
}

func (pt *PipelineTester) workingDir() string {
	if pt.fileAccess != nil && strings.TrimSpace(pt.fileAccess.WorkingDir()) != "" {
		return pt.fileAccess.WorkingDir()
	}
	return "."
}

func (pt *PipelineTester) detectHarness(ctx context.Context, files []string, taskSpec, workerType string) (*testHarnessState, error) {
	targetFiles := pt.normalizeTargetFiles(files, taskSpec)
	if len(targetFiles) == 0 {
		return nil, fmt.Errorf("no target files available for harness detection")
	}

	root := pt.workingDir()
	projectRuntime := purevfs.DefaultCatalog().DetectProject(root)
	language := inferLanguage(targetFiles, workerType)
	def := selectFrameworkDefinition(root, language, projectRuntime)
	if def == nil {
		return nil, fmt.Errorf("no supported test framework detected for %s", language)
	}

	existingTests := pt.findExistingTestFiles(ctx, def, targetFiles)
	outputs := make(map[string]string, len(targetFiles))
	for _, target := range targetFiles {
		outputs[target] = recommendedOutputFile(def, target)
	}

	state := &testHarnessState{
		FrameworkID:        def.ID,
		FrameworkName:      def.Name,
		Language:           def.Language,
		RunCommand:         def.RunCommand,
		CoverageCommand:    def.CoverageCommand,
		ConfigFiles:        append([]string(nil), def.ConfigFiles...),
		MissingConfigFiles: pt.missingConfigFiles(ctx, def),
		TargetFiles:        targetFiles,
		ExistingTestFiles:  existingTests,
		RecommendedOutputs: outputs,
		PackagePatterns:    derivePackagePatterns(root, def, targetFiles),
		DetectedAt:         time.Now(),
	}
	state.SetupRequired, state.SetupReason = harnessSetupRequirement(def, state.MissingConfigFiles)
	pt.setHarnessState(state)
	return state, nil
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

func (pt *PipelineTester) missingConfigFiles(ctx context.Context, def *coretest.TestFrameworkDefinition) []string {
	if len(def.ConfigFiles) == 0 {
		return nil
	}
	missing := make([]string, 0, len(def.ConfigFiles))
	for _, cfg := range def.ConfigFiles {
		exists, err := pt.fileExists(ctx, cfg)
		if err != nil || !exists {
			missing = append(missing, cfg)
		}
	}
	return missing
}

func (pt *PipelineTester) findExistingTestFiles(ctx context.Context, def *coretest.TestFrameworkDefinition, targets []string) []string {
	seen := make(map[string]struct{}, len(targets))
	found := make([]string, 0, len(targets))
	for _, target := range targets {
		output := recommendedOutputFile(def, target)
		ok, err := pt.fileExists(ctx, output)
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

func (pt *PipelineTester) normalizeTargetFiles(files []string, taskSpec string) []string {
	candidates := append([]string(nil), files...)
	if len(candidates) == 0 {
		pt.mu.RLock()
		task := pt.currentTask
		pt.mu.RUnlock()
		if task != nil {
			candidates = append(candidates, taskContextFiles(task)...)
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, extractTaskFilesFromPrompt(taskSpec)...)
	}

	root := pt.workingDir()
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

func taskContextFiles(task *agentshared.PipelineTaskInput) []string {
	if task == nil || task.Context == nil {
		return nil
	}
	var files []string
	appendStrings := func(raw any) {
		switch typed := raw.(type) {
		case []string:
			files = append(files, typed...)
		case []any:
			for _, item := range typed {
				switch value := item.(type) {
				case string:
					files = append(files, value)
				case map[string]any:
					if path, _ := value["path"].(string); strings.TrimSpace(path) != "" {
						files = append(files, path)
					}
				}
			}
		}
	}

	appendStrings(task.Context["affected_files"])
	if workspace, _ := task.Context["workspace"].(map[string]any); workspace != nil {
		appendStrings(workspace["test_surface"])
		appendStrings(workspace["write_set"])
		appendStrings(workspace["read_set"])
	}
	if packets, _ := task.Context["worker_packets"].([]any); len(packets) > 0 {
		for _, raw := range packets {
			packet, _ := raw.(map[string]any)
			if packet == nil {
				continue
			}
			appendStrings(packet["write_set"])
			appendStrings(packet["read_set"])
		}
	}
	return files
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

func (pt *PipelineTester) analyzeRisks(_ context.Context, files []string, taskSpec, workerType string) []testershared.RiskArea {
	targetFiles := pt.normalizeTargetFiles(files, taskSpec)
	risks := make([]testershared.RiskArea, 0, len(targetFiles)*2)

	for _, file := range targetFiles {
		content := pt.readFileString(context.Background(), file)
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
				Description: "Task-specific behavior must be validated against the stated acceptance criteria.",
			})
		}
	}
	return dedupeRiskAreas(risks)
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
		addRisk(testershared.RiskConcurrency, testershared.RiskHigh, "Concurrent access and goroutine coordination may violate task requirements under load.")
	}
	if strings.Contains(lowerContent, "open(") || strings.Contains(lowerContent, "close(") || strings.Contains(lowerContent, "defer ") || strings.Contains(lowerContent, "context.") {
		addRisk(testershared.RiskResource, testershared.RiskMedium, "Resource lifecycle management needs tests for cleanup, cancellation, and leak prevention.")
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
		addRisk(testershared.RiskAccessibility, testershared.RiskHigh, "Design output must preserve accessibility semantics and interaction affordances.")
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

func (pt *PipelineTester) buildPlan(files []string, taskSpec string, risks []testershared.RiskArea, harness *testHarnessState) *testershared.TestPlan {
	targetFiles := pt.normalizeTargetFiles(files, taskSpec)
	if harness == nil {
		harness, _ = pt.detectHarness(context.Background(), targetFiles, taskSpec, "")
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
			FailureHypothesis: fmt.Sprintf("The implementation does not satisfy criterion: %s", criterion),
			InputStrategy:     "Exercise the contract through its public entry points with deterministic boundary inputs.",
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

	pt.mu.Lock()
	pt.currentPlan = plan
	pt.mu.Unlock()
	return plan
}

func buildPlanRationale(taskSpec string, risks []testershared.RiskArea, harness *testHarnessState) string {
	var parts []string
	if harness != nil {
		parts = append(parts, fmt.Sprintf("Use %s as the execution harness.", harness.FrameworkName))
	}
	if len(risks) > 0 {
		parts = append(parts, fmt.Sprintf("Prioritize %d identified risk areas before lower-signal coverage.", len(risks)))
	}
	if criteria := len(extractPromptList(taskSpec, "Acceptance Criteria")) + len(extractPromptList(taskSpec, "Success Criteria")); criteria > 0 {
		parts = append(parts, "Map tests directly to the task contract so failures stay specification-driven.")
	}
	if len(parts) == 0 {
		return "Generate a small, specification-driven test set that exercises the most defect-prone behavior first."
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
		return "The implementation satisfies the task contract under the identified risk."
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

func (pt *PipelineTester) prepareHarness(
	ctx context.Context,
	state *testHarnessState,
	writeBasis map[string]versioning.WorkspaceWriteBasis,
) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("test harness has not been detected")
	}
	var created []string
	writeDefault := func(plan pipelineWritePlan) error {
		if err := pt.validatePipelineWritePlan(ctx, plan.Path, writeBasis); err != nil {
			return err
		}
		basis, store, err := pipelineWriteBasisRef(writeBasis, plan.Path)
		if err != nil {
			return err
		}
		for attempt := 0; attempt < maxPipelineWriteAttempts; attempt++ {
			exists, existsErr := pt.fileExists(ctx, plan.Path)
			if existsErr == nil && exists {
				store()
				return nil
			}
			if err := pt.ensurePipelineWriteBasis(ctx, plan.Path, basis); err != nil {
				return err
			}
			if err := pt.invokePipelineWriteSkill(ctx, plan.Path, plan.Content, basis); err != nil {
				if !pt.refreshRetryablePipelineWriteBasis(ctx, plan.Path, basis, err) {
					return err
				}
				continue
			}
			store()
			created = append(created, plan.Path)
			return nil
		}
		return fmt.Errorf("write_pipeline_file did not settle for %s after %d attempts", plan.Path, maxPipelineWriteAttempts)
	}

	for _, plan := range pt.harnessWritePlans(state) {
		if err := writeDefault(plan); err != nil {
			return nil, err
		}
	}

	if len(created) > 0 {
		state.CreatedFiles = append(state.CreatedFiles, created...)
		state.MissingConfigFiles = pt.missingConfigFiles(ctx, frameworkDefinition(state.FrameworkID))
		state.SetupRequired = len(state.MissingConfigFiles) > 0
	}
	pt.setHarnessState(state)
	return created, nil
}

func frameworkDefinition(id coretest.TestFrameworkID) *coretest.TestFrameworkDefinition {
	for _, def := range coretest.BuiltinFrameworks {
		if def.ID == id {
			return def
		}
	}
	return nil
}

func (pt *PipelineTester) writeTestArtifact(
	ctx context.Context,
	harness *testHarnessState,
	testCase testershared.PlannedTestCase,
	outputFile, content string,
	basis *versioning.WorkspaceWriteBasis,
) (string, error) {
	if harness == nil {
		return "", fmt.Errorf("test harness has not been detected")
	}
	if strings.TrimSpace(outputFile) == "" {
		outputFile = harness.RecommendedOutputs[testCase.TargetFile]
	}
	if strings.TrimSpace(outputFile) == "" {
		outputFile = recommendedOutputFile(frameworkDefinition(harness.FrameworkID), testCase.TargetFile)
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("test content is required")
	}
	activeBasis := basis
	if activeBasis == nil {
		activeBasis = &versioning.WorkspaceWriteBasis{}
	}
	for attempt := 0; attempt < maxPipelineWriteAttempts; attempt++ {
		if err := pt.ensurePipelineWriteBasis(ctx, outputFile, activeBasis); err != nil {
			return "", err
		}
		existing, _ := pt.readExistingFile(ctx, outputFile)
		var merged string
		switch harness.FrameworkID {
		case coretest.FrameworkGoTest:
			packageName := pt.goPackageName(ctx, testCase.TargetFile, outputFile)
			merged = mergeGoTestContent(existing, packageName, content)
		default:
			merged = mergeGenericTestContent(existing, content)
		}
		if err := pt.invokePipelineWriteSkill(ctx, outputFile, merged, activeBasis); err != nil {
			if !pt.refreshRetryablePipelineWriteBasis(ctx, outputFile, activeBasis, err) {
				return "", err
			}
			continue
		}
		if basis != nil {
			*basis = *activeBasis
		}
		if harness != nil {
			harness.CreatedFiles = appendIfMissing(harness.CreatedFiles, outputFile)
			pt.setHarnessState(harness)
		}
		return outputFile, nil
	}
	return "", fmt.Errorf("write_pipeline_file did not settle for %s after %d attempts", outputFile, maxPipelineWriteAttempts)
}

func (pt *PipelineTester) harnessWritePlans(state *testHarnessState) []pipelineWritePlan {
	if state == nil || len(state.MissingConfigFiles) == 0 {
		return nil
	}
	switch state.FrameworkID {
	case coretest.FrameworkVitest:
		return []pipelineWritePlan{{
			Path:    "vitest.config.ts",
			Content: "import { defineConfig } from 'vitest/config'\n\nexport default defineConfig({\n  test: {\n    environment: 'node',\n  },\n})\n",
		}}
	case coretest.FrameworkJest:
		return []pipelineWritePlan{{
			Path:    "jest.config.cjs",
			Content: "module.exports = {\n  testEnvironment: 'node',\n};\n",
		}}
	case coretest.FrameworkPytest:
		return []pipelineWritePlan{{
			Path:    "pytest.ini",
			Content: "[pytest]\npython_files = test_*.py *_test.py\n",
		}}
	default:
		return nil
	}
}

func (pt *PipelineTester) validatePipelineWritePlan(
	ctx context.Context,
	path string,
	writeBasis map[string]versioning.WorkspaceWriteBasis,
) error {
	if len(writeBasis) == 0 {
		return nil
	}
	key := normalizePipelineWritePath(path)
	basis, ok := writeBasis[key]
	if !ok {
		return fmt.Errorf("prepare_pipeline_write_context is required for %s", path)
	}
	if err := pt.ensurePipelineWriteBasis(ctx, path, &basis); err != nil {
		return err
	}
	writeBasis[key] = basis
	return nil
}

func (pt *PipelineTester) validatePipelineWriteBasis(
	ctx context.Context,
	path string,
	basis *versioning.WorkspaceWriteBasis,
) error {
	if basis == nil {
		return nil
	}
	return versioning.ValidateWorkspaceWriteBasis(
		ctx,
		pt.workspaceViews,
		versioning.WorkspaceWriteScopePipeline,
		path,
		*basis,
	)
}

func (pt *PipelineTester) ensurePipelineWriteBasis(
	ctx context.Context,
	path string,
	basis *versioning.WorkspaceWriteBasis,
) error {
	if basis == nil {
		return fmt.Errorf("basis is required")
	}
	if pipelineWriteBasisNeedsRefresh(*basis) {
		return pt.refreshPipelineWriteBasis(ctx, path, basis)
	}
	if err := pt.validatePipelineWriteBasis(ctx, path, basis); err != nil {
		if !pt.retryablePipelineWriteBasisError(err) {
			return err
		}
		return pt.refreshPipelineWriteBasis(ctx, path, basis)
	}
	return nil
}

func (pt *PipelineTester) refreshPipelineWriteBasis(
	ctx context.Context,
	path string,
	basis *versioning.WorkspaceWriteBasis,
) error {
	if basis == nil {
		return nil
	}
	if err := pt.registerWritablePath(path); err != nil {
		return err
	}
	pipelineID := strings.TrimSpace(basis.PipelineID)
	if pipelineID == "" {
		pipelineID = strings.TrimSpace(pt.pipelineID)
	}
	refreshed, err := versioning.RefreshWorkspaceWriteBasis(
		ctx,
		pt.workspaceViews,
		versioning.WorkspaceWriteScopePipeline,
		path,
		pipelineID,
	)
	if err != nil {
		return err
	}
	*basis = refreshed
	return nil
}

func (pt *PipelineTester) retryablePipelineWriteBasisError(err error) bool {
	if errors.Is(err, versioning.ErrWorkspaceWriteLeaseExpired) {
		return true
	}
	_, ok := versioning.WorkspaceWriteStale(err)
	return ok
}

func (pt *PipelineTester) refreshRetryablePipelineWriteBasis(
	ctx context.Context,
	path string,
	basis *versioning.WorkspaceWriteBasis,
	err error,
) bool {
	if !pt.retryablePipelineWriteBasisError(err) {
		return false
	}
	if refreshErr := pt.refreshPipelineWriteBasis(ctx, path, basis); refreshErr != nil {
		return false
	}
	return true
}

func (pt *PipelineTester) invokePipelineWriteSkill(
	ctx context.Context,
	path, content string,
	basis *versioning.WorkspaceWriteBasis,
) error {
	if basis == nil {
		return fmt.Errorf("basis is required")
	}
	payload, err := json.Marshal(map[string]any{
		"path":    path,
		"content": content,
		"basis":   *basis,
	})
	if err != nil {
		return fmt.Errorf("marshal write_pipeline_file payload: %w", err)
	}
	result := pt.skills.Invoke(ctx, "write_pipeline_file", payload)
	if err := pipelineWriteSkillError("write_pipeline_file", result); err != nil {
		return err
	}
	if nextBasis, ok := pipelineNextBasis(result.Data); ok {
		*basis = nextBasis
	}
	return nil
}

func pipelineWriteBasisNeedsRefresh(basis versioning.WorkspaceWriteBasis) bool {
	return basis.Scope == "" &&
		strings.TrimSpace(basis.Path) == "" &&
		basis.TargetView == ""
}

func pipelineWriteBasisRef(
	index map[string]versioning.WorkspaceWriteBasis,
	path string,
) (*versioning.WorkspaceWriteBasis, func(), error) {
	if len(index) == 0 {
		return &versioning.WorkspaceWriteBasis{}, func() {}, nil
	}
	key := normalizePipelineWritePath(path)
	current, ok := index[key]
	if !ok {
		return nil, nil, fmt.Errorf("prepare_pipeline_write_context is required for %s", path)
	}
	basis := current
	return &basis, func() { index[key] = basis }, nil
}

func pipelineWriteSkillError(name string, result *skills.Result) error {
	if result == nil {
		return fmt.Errorf("%s returned no result", name)
	}
	if result.Success {
		return nil
	}
	if result.Err != nil {
		return result.Err
	}
	if msg := strings.TrimSpace(result.Error); msg != "" {
		return fmt.Errorf("%s: %s", name, msg)
	}
	return fmt.Errorf("%s failed", name)
}

func pipelineNextBasis(data any) (versioning.WorkspaceWriteBasis, bool) {
	mapped, ok := data.(map[string]any)
	if !ok {
		return versioning.WorkspaceWriteBasis{}, false
	}
	switch typed := mapped["next_basis"].(type) {
	case *versioning.WorkspaceWriteBasis:
		if typed == nil {
			return versioning.WorkspaceWriteBasis{}, false
		}
		return *typed, true
	case versioning.WorkspaceWriteBasis:
		return typed, true
	default:
		return versioning.WorkspaceWriteBasis{}, false
	}
}

func normalizePipelineWritePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func mergeGenericTestContent(existing []byte, content string) string {
	existingText := strings.TrimSpace(string(existing))
	newText := strings.TrimSpace(content)
	switch {
	case existingText == "":
		return newText + "\n"
	case strings.Contains(existingText, newText):
		return existingText + "\n"
	default:
		return existingText + "\n\n" + newText + "\n"
	}
}

var (
	goPkgRE         = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z0-9_]+)\b`)
	goImportBlockRE = regexp.MustCompile(`(?ms)^\s*import\s*\((.*?)\)\s*`)
	goImportLineRE  = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"\s*$`)
	goStringRE      = regexp.MustCompile(`"([^"]+)"`)
)

func mergeGoTestContent(existing []byte, packageName, content string) string {
	existingParts := parseGoSourceParts(string(existing))
	newParts := parseGoSourceParts(content)

	if existingParts.packageName == "" {
		existingParts.packageName = packageName
	}
	if newParts.packageName != "" && existingParts.packageName == "" {
		existingParts.packageName = newParts.packageName
	}
	if existingParts.packageName == "" {
		existingParts.packageName = "main"
	}

	imports := uniqueStrings(append(existingParts.imports, newParts.imports...))
	if len(imports) == 0 && strings.Contains(content, "*testing.T") {
		imports = []string{"testing"}
	}

	body := strings.TrimSpace(existingParts.body)
	newBody := strings.TrimSpace(newParts.body)
	if body == "" {
		body = newBody
	} else if newBody != "" && !strings.Contains(body, newBody) {
		body = strings.TrimSpace(body + "\n\n" + newBody)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", existingParts.packageName)
	if len(imports) > 0 {
		b.WriteString(renderGoImports(imports))
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

func parseGoSourceParts(content string) goSourceParts {
	parts := goSourceParts{body: strings.TrimSpace(content)}
	if content == "" {
		return parts
	}
	if match := goPkgRE.FindStringSubmatch(content); len(match) == 2 {
		parts.packageName = strings.TrimSpace(match[1])
		parts.body = strings.TrimSpace(goPkgRE.ReplaceAllString(parts.body, ""))
	}

	for _, match := range goImportBlockRE.FindAllStringSubmatch(content, -1) {
		parts.imports = append(parts.imports, extractGoImports(match[1])...)
	}
	parts.body = strings.TrimSpace(goImportBlockRE.ReplaceAllString(parts.body, ""))

	for _, match := range goImportLineRE.FindAllStringSubmatch(content, -1) {
		if len(match) == 2 {
			parts.imports = append(parts.imports, strings.TrimSpace(match[1]))
		}
	}
	parts.body = strings.TrimSpace(goImportLineRE.ReplaceAllString(parts.body, ""))
	parts.imports = uniqueStrings(parts.imports)
	return parts
}

func extractGoImports(block string) []string {
	matches := goStringRE.FindAllStringSubmatch(block, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			result = append(result, strings.TrimSpace(match[1]))
		}
	}
	return result
}

func renderGoImports(imports []string) string {
	imports = uniqueStrings(imports)
	sort.Strings(imports)
	if len(imports) == 1 {
		return fmt.Sprintf("import %q", imports[0])
	}
	var b strings.Builder
	b.WriteString("import (\n")
	for _, imp := range imports {
		fmt.Fprintf(&b, "\t%q\n", imp)
	}
	b.WriteString(")")
	return b.String()
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

func (pt *PipelineTester) goPackageName(ctx context.Context, targetFile, outputFile string) string {
	for _, candidate := range []string{outputFile, targetFile} {
		if content, err := pt.readExistingFile(ctx, candidate); err == nil {
			if match := goPkgRE.FindStringSubmatch(string(content)); len(match) == 2 {
				return match[1]
			}
		}
	}
	return "main"
}

func appendIfMissing(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func (pt *PipelineTester) registerWritablePath(path string) error {
	if pt.fileAccess == nil {
		return fmt.Errorf("file access not configured")
	}
	if pt.fileAccess.IsReadOnly() {
		return fmt.Errorf("file writes are disabled")
	}
	if fa, ok := pt.fileAccess.(overlayAwareFileAccess); ok {
		fa.RegisterVisiblePath(path)
	}
	return nil
}

func (pt *PipelineTester) fileExists(ctx context.Context, path string) (bool, error) {
	if pt.fileAccess != nil {
		return pt.fileAccess.Exists(ctx, path)
	}
	_, err := os.Stat(filepath.Join(pt.workingDir(), path))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (pt *PipelineTester) readExistingFile(ctx context.Context, path string) ([]byte, error) {
	if pt.fileAccess == nil {
		return os.ReadFile(filepath.Join(pt.workingDir(), path))
	}
	return pt.fileAccess.ReadFile(ctx, path)
}

func (pt *PipelineTester) readFileString(ctx context.Context, path string) string {
	content, err := pt.readExistingFile(ctx, path)
	if err != nil {
		return ""
	}
	return string(content)
}

func (pt *PipelineTester) writeRawFile(ctx context.Context, path, content string) error {
	if pt.fileAccess == nil {
		return fmt.Errorf("file access not configured")
	}
	if err := pt.registerWritablePath(path); err != nil {
		return err
	}
	return pt.fileAccess.WriteFile(ctx, path, []byte(content))
}

func (pt *PipelineTester) createTests(ctx context.Context, req *tester.TesterRequest) (*tester.TesterResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	files := pt.normalizeTargetFiles(req.Files, req.TaskPrompt)
	start := time.Now()
	if len(files) == 0 && pt.getProvider() == nil {
		pt.mu.Lock()
		pt.currentPlan = &testershared.TestPlan{
			ID:        "plan_" + uuid.NewString()[:8],
			Rationale: "No scoped task files were provided, so test creation was deferred until concrete workspace inputs are available.",
			CreatedAt: time.Now(),
		}
		pt.mu.Unlock()
		return &tester.TesterResponse{
			ID:        uuid.NewString(),
			RequestID: req.ID,
			Success:   true,
			SuiteResult: &tester.TestSuiteResult{
				SuiteID:     uuid.NewString(),
				Name:        "create_tests",
				StartedAt:   start,
				CompletedAt: time.Now(),
				Duration:    time.Since(start),
			},
			Timestamp: time.Now(),
		}, nil
	}

	if pt.getProvider() != nil {
		if err := pt.createTestsWithProvider(ctx, req, files); err != nil {
			return nil, err
		}
	} else {
		if err := pt.createTestsDeterministically(ctx, req, files); err != nil {
			return nil, err
		}
	}

	plan := pt.planSnapshot()
	created := pt.createdArtifacts()
	results := make([]tester.TestResult, 0)
	if plan != nil {
		for _, tc := range plan.PlannedCase {
			results = append(results, tester.TestResult{
				TestID:    tc.Name,
				Name:      tc.Name,
				Status:    tester.StatusPassed,
				Output:    "test file written",
				Timestamp: time.Now(),
			})
		}
	}

	suite := &tester.TestSuiteResult{
		SuiteID:     uuid.NewString(),
		Name:        "create_tests",
		Results:     results,
		TotalTests:  len(results),
		Passed:      len(results),
		StartedAt:   start,
		CompletedAt: time.Now(),
		Duration:    time.Since(start),
	}

	return &tester.TesterResponse{
		ID:           uuid.NewString(),
		RequestID:    req.ID,
		Success:      len(created) > 0 || len(results) > 0,
		SuiteResult:  suite,
		CreatedFiles: created,
		Timestamp:    time.Now(),
	}, nil
}

func (pt *PipelineTester) createTestsWithProvider(ctx context.Context, req *tester.TesterRequest, files []string) error {
	userMessage := buildCreateTestsUserPrompt(req, files)
	pt.prepareSkillsForInput(userMessage)
	tools := pt.buildToolDefinitions()

	pt.mu.RLock()
	model := pt.config.Model
	maxTokens := pt.config.MaxTokens
	pt.mu.RUnlock()

	providerReq := &providers.Request{
		SystemPrompt: testershared.PipelineTesterSystemPromptForWorkerAndContract(req.WorkerType, nil),
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		Model:     model,
		MaxTokens: maxTokens,
		Tools:     tools,
	}
	pt.applyLLMRuntimeProfile(providerReq, "creation")

	_, err := agentshared.ExecuteTurnLoop(
		agentshared.SteeringLedgerFromContext(ctx),
		providerReq,
		func() (string, error) {
			return pt.executeToolLoop(ctx, providerReq, agentshared.SteeringLedgerFromContext(ctx))
		},
	)
	if err != nil {
		return fmt.Errorf("create tests tool loop: %w", err)
	}
	if len(pt.createdArtifacts()) == 0 {
		return fmt.Errorf("tester did not write any test artifacts")
	}
	return nil
}

func buildCreateTestsUserPrompt(req *tester.TesterRequest, files []string) string {
	var b strings.Builder
	b.WriteString("This is the create-tests stage of the pipeline.\n")
	b.WriteString("Synthesize executable tests from the task brief and write them into the task-local VFS.\n")
	b.WriteString("Do not stop at analysis. You must write concrete runnable test code, not TODOs, skips, or placeholders.\n")
	b.WriteString("If a target implementation file is missing, treat that as valid red-phase state: continue planning and writing specification-driven tests anyway.\n")
	b.WriteString("Use the tester tool definitions as the workflow contract for this stage.\n")
	b.WriteString("That usually means understanding the current criteria and challenge, discovering and preparing the harness when needed, analyzing risks, forming a real test plan, and then writing concrete tests through leased pipeline write contexts.\n")
	b.WriteString("Before the first `write_test` for a file, call `prepare_pipeline_write_context` and pass that basis into `write_test`. Reuse `next_basis` for subsequent writes to the same file while its lease remains active.\n")
	if len(files) > 0 {
		b.WriteString("\nTarget files:\n")
		for _, file := range files {
			fmt.Fprintf(&b, "- %s\n", file)
		}
	}
	if strings.TrimSpace(req.TaskPrompt) != "" {
		b.WriteString("\nTask brief:\n")
		b.WriteString(strings.TrimSpace(req.TaskPrompt))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (pt *PipelineTester) createTestsDeterministically(ctx context.Context, req *tester.TesterRequest, files []string) error {
	harness, err := pt.detectHarnessWithTools(ctx, files, req.TaskPrompt, req.WorkerType)
	if err != nil {
		return err
	}
	if pt.fileAccess == nil {
		risks, riskErr := pt.analyzeRisksWithTools(ctx, files, req.TaskPrompt, req.WorkerType)
		if riskErr != nil {
			return riskErr
		}
		if _, planErr := pt.buildPlanWithTools(ctx, files, req.TaskPrompt, risks); planErr != nil {
			return planErr
		}
		return nil
	}
	if err := pt.prepareHarnessWithTools(ctx, harness, files, req.TaskPrompt, req.WorkerType); err != nil {
		return err
	}
	risks, err := pt.analyzeRisksWithTools(ctx, files, req.TaskPrompt, req.WorkerType)
	if err != nil {
		return err
	}
	plan, err := pt.buildPlanWithTools(ctx, files, req.TaskPrompt, risks)
	if err != nil {
		return err
	}
	writeBasis := make(map[string]versioning.WorkspaceWriteBasis)
	for _, tc := range plan.PlannedCase {
		content := deterministicTestContent(harness, tc)
		outputFile := harness.RecommendedOutputs[tc.TargetFile]
		nextBasis, writeErr := pt.writeDeterministicTestWithTools(ctx, tc, outputFile, content, writeBasis)
		if writeErr != nil {
			return writeErr
		}
		if strings.TrimSpace(outputFile) != "" {
			writeBasis[normalizePipelineWritePath(outputFile)] = nextBasis
		}
	}
	return nil
}

func (pt *PipelineTester) detectHarnessWithTools(
	ctx context.Context,
	files []string,
	taskSpec string,
	workerType string,
) (*testHarnessState, error) {
	result, err := pt.runDeterministicToolCall(ctx, "detect_test_harness", map[string]any{
		"files":       files,
		"task_spec":   taskSpec,
		"worker_type": workerType,
	})
	if err != nil {
		return nil, err
	}
	var state testHarnessState
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		return nil, fmt.Errorf("decode detect_test_harness result: %w", err)
	}
	pt.setHarnessState(&state)
	return &state, nil
}

func (pt *PipelineTester) analyzeRisksWithTools(
	ctx context.Context,
	files []string,
	taskSpec string,
	workerType string,
) ([]testershared.RiskArea, error) {
	result, err := pt.runDeterministicToolCall(ctx, "analyze_risk", map[string]any{
		"files":       files,
		"task_spec":   taskSpec,
		"worker_type": workerType,
	})
	if err != nil {
		return nil, err
	}
	var payload struct {
		RiskAreas []testershared.RiskArea `json:"risk_areas"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return nil, fmt.Errorf("decode analyze_risk result: %w", err)
	}
	return payload.RiskAreas, nil
}

func (pt *PipelineTester) buildPlanWithTools(
	ctx context.Context,
	files []string,
	taskSpec string,
	risks []testershared.RiskArea,
) (*testershared.TestPlan, error) {
	result, err := pt.runDeterministicToolCall(ctx, "plan_tests", map[string]any{
		"files":      files,
		"task_spec":  taskSpec,
		"risk_areas": risks,
	})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Plan *testershared.TestPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return nil, fmt.Errorf("decode plan_tests result: %w", err)
	}
	if payload.Plan == nil {
		return nil, fmt.Errorf("plan_tests returned no plan")
	}
	return payload.Plan, nil
}

func deterministicTestContent(harness *testHarnessState, tc testershared.PlannedTestCase) string {
	switch harness.FrameworkID {
	case coretest.FrameworkGoTest:
		return fmt.Sprintf("func %s(t *testing.T) {\n\tt.Helper()\n\tt.Fatalf(%q)\n}\n", tc.Name, "specification not implemented yet: "+tc.ExpectedBehavior)
	case coretest.FrameworkVitest, coretest.FrameworkJest, coretest.FrameworkBunTest, coretest.FrameworkNodeTest:
		return fmt.Sprintf("test(%q, async () => {\n  throw new Error(%q)\n})\n", tc.Name, "specification not implemented yet: "+tc.ExpectedBehavior)
	case coretest.FrameworkPytest, coretest.FrameworkPythonUnitTest:
		return fmt.Sprintf("def %s():\n    raise AssertionError(%q)\n", strings.ToLower(tc.Name), "specification not implemented yet: "+tc.ExpectedBehavior)
	case coretest.FrameworkExUnit:
		return fmt.Sprintf("test %q do\n  flunk(%q)\nend\n", tc.Name, "specification not implemented yet: "+tc.ExpectedBehavior)
	default:
		return fmt.Sprintf("// %s\n", tc.ExpectedBehavior)
	}
}

func (pt *PipelineTester) prepareHarnessWithTools(
	ctx context.Context,
	harness *testHarnessState,
	files []string,
	taskSpec string,
	workerType string,
) error {
	if harness == nil {
		return fmt.Errorf("test harness has not been detected")
	}
	plans := pt.harnessWritePlans(harness)
	contexts := make([]map[string]any, 0, len(plans))
	for _, plan := range plans {
		if err := pt.registerWritablePath(plan.Path); err != nil {
			return err
		}
		prepared, err := pt.runDeterministicToolCall(ctx, "prepare_pipeline_write_context", map[string]any{
			"path": plan.Path,
		})
		if err != nil {
			return err
		}
		var writeCtx versioning.PreparedWorkspaceWriteContext
		if err := json.Unmarshal([]byte(prepared), &writeCtx); err != nil {
			return fmt.Errorf("decode prepare_pipeline_write_context: %w", err)
		}
		contexts = append(contexts, map[string]any{
			"path":  plan.Path,
			"basis": writeCtx.Basis,
		})
	}
	_, err := pt.runDeterministicToolCall(ctx, "prepare_test_harness", map[string]any{
		"files":          files,
		"task_spec":      taskSpec,
		"worker_type":    workerType,
		"write_contexts": contexts,
	})
	return err
}

func (pt *PipelineTester) writeDeterministicTestWithTools(
	ctx context.Context,
	tc testershared.PlannedTestCase,
	outputFile string,
	content string,
	writeBasis map[string]versioning.WorkspaceWriteBasis,
) (versioning.WorkspaceWriteBasis, error) {
	outputFile = strings.TrimSpace(outputFile)
	if outputFile == "" {
		return versioning.WorkspaceWriteBasis{}, fmt.Errorf("output_file is required")
	}
	key := normalizePipelineWritePath(outputFile)
	basis, ok := writeBasis[key]
	if !ok {
		if err := pt.registerWritablePath(outputFile); err != nil {
			return versioning.WorkspaceWriteBasis{}, err
		}
		prepared, err := pt.runDeterministicToolCall(ctx, "prepare_pipeline_write_context", map[string]any{
			"path": outputFile,
		})
		if err != nil {
			return versioning.WorkspaceWriteBasis{}, err
		}
		var writeCtx versioning.PreparedWorkspaceWriteContext
		if err := json.Unmarshal([]byte(prepared), &writeCtx); err != nil {
			return versioning.WorkspaceWriteBasis{}, fmt.Errorf("decode prepare_pipeline_write_context: %w", err)
		}
		basis = writeCtx.Basis
	}
	written, err := pt.runDeterministicToolCall(ctx, "write_test", map[string]any{
		"test_case":   tc,
		"target_file": tc.TargetFile,
		"output_file": outputFile,
		"content":     content,
		"basis":       basis,
	})
	if err != nil {
		return versioning.WorkspaceWriteBasis{}, err
	}
	var payload struct {
		NextBasis versioning.WorkspaceWriteBasis `json:"next_basis"`
	}
	if err := json.Unmarshal([]byte(written), &payload); err != nil {
		return versioning.WorkspaceWriteBasis{}, fmt.Errorf("decode write_test result: %w", err)
	}
	return payload.NextBasis, nil
}

func (pt *PipelineTester) runDeterministicToolCall(
	ctx context.Context,
	name string,
	payload map[string]any,
) (string, error) {
	arguments, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal %s arguments: %w", name, err)
	}
	call := providers.ToolCall{
		ID:        "det_" + uuid.NewString()[:8],
		Name:      name,
		Arguments: string(arguments),
	}
	execCtx := agentshared.WithActiveToolCall(ctx, call)
	return agentshared.TimedToolCall(execCtx, "tester-pipeline", call, func() (string, error) {
		execResult, execErr := pt.executeToolCall(execCtx, call)
		if execErr != nil {
			return "", execErr
		}
		return execResult.Output, nil
	})
}

func (pt *PipelineTester) createdArtifacts() []string {
	seen := make(map[string]struct{})
	created := make([]string, 0)
	if harness := pt.currentHarnessState(); harness != nil {
		for _, file := range harness.CreatedFiles {
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			created = append(created, file)
		}
	}
	if fa, ok := pt.fileAccess.(overlayAwareFileAccess); ok {
		root := pt.workingDir()
		for _, mod := range fa.Modifications() {
			rel, err := filepath.Rel(root, mod.OriginalPath)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if !isTestArtifact(rel) {
				continue
			}
			if _, ok := seen[rel]; ok {
				continue
			}
			seen[rel] = struct{}{}
			created = append(created, rel)
		}
	}
	sort.Strings(created)
	return created
}

func isTestArtifact(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.Contains(base, ".test."),
		strings.Contains(base, ".spec."),
		strings.HasSuffix(base, "Spec.scala"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.HasSuffix(base, "Test.java"),
		strings.HasSuffix(base, "Test.kt"),
		strings.HasSuffix(base, "Test.scala"),
		strings.HasSuffix(base, "Spec.hs"),
		strings.HasSuffix(base, "Test.hs"),
		strings.HasSuffix(base, "Tests.cs"),
		strings.HasSuffix(base, "_test.dart"),
		strings.HasSuffix(base, "_test.exs"),
		strings.HasSuffix(base, "_tests.erl"),
		strings.HasSuffix(base, "Tests.swift"),
		strings.HasSuffix(base, "_test.zig"),
		strings.HasSuffix(base, "_spec.rb"),
		strings.HasSuffix(base, "Test.php"),
		strings.HasPrefix(filepath.ToSlash(path), "testharness/"),
		base == "vitest.config.ts",
		base == "jest.config.cjs",
		base == "pytest.ini":
		return true
	default:
		return false
	}
}

func (pt *PipelineTester) executeSuite(ctx context.Context, harness *testHarnessState, packages, files, testNames []string, race, verbose bool, timeoutSeconds int) (map[string]any, error) {
	if harness == nil {
		var err error
		harness, err = pt.detectHarness(ctx, files, "", pt.workerType)
		if err != nil {
			return nil, err
		}
	}
	switch harness.FrameworkID {
	case coretest.FrameworkGoTest:
		return pt.runGoTestSuite(ctx, harness, packages, files, testNames, race, verbose, timeoutSeconds)
	default:
		return pt.runGenericSuite(ctx, harness, packages, files, testNames, verbose, timeoutSeconds)
	}
}

func (pt *PipelineTester) executionPlan(ctx context.Context, harness *testHarnessState, workDir string) (purevfs.ExecutionPlan, error) {
	overlay, overlayDeletes := overlayState(pt.fileAccess)
	planner := purevfs.NewExecutionPlanner(nil, pt.executionCapabilities(ctx))
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

func (pt *PipelineTester) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if pt.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := pt.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (pt *PipelineTester) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if pt.fileAccess == nil {
		return versioning.NewDiskFileAccess(pt.workingDir(), true)
	}
	if allowWrites {
		if testerWorkspaceWritesAllowed(pt.fileAccess) {
			return versioning.TagExecutionFS(pt.fileAccess, versioning.WorkspaceMutationOriginCommandExecution)
		}
		return versioning.TagExecutionFS(purevfs.ReadOnlyExecutionFS(pt.fileAccess), versioning.WorkspaceMutationOriginCommandExecution)
	}
	return purevfs.ReadOnlyExecutionFS(pt.fileAccess)
}

func testerWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
	if fa == nil || fa.IsReadOnly() {
		return false
	}
	switch versioning.Underlying(fa).(type) {
	case *versioning.DiskFileAccess:
		return false
	default:
		return true
	}
}

func overlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(overlayAwareFileAccess)
	if !ok {
		return false, false
	}
	mods := overlay.Modifications()
	if len(mods) == 0 {
		return false, false
	}
	hasDeletes := false
	for _, mod := range mods {
		if mod.Operation == versioning.FileOpDelete {
			hasDeletes = true
			break
		}
	}
	return true, hasDeletes
}

func (pt *PipelineTester) runGoTestSuite(ctx context.Context, harness *testHarnessState, packages, files, testNames []string, race, verbose bool, timeoutSeconds int) (map[string]any, error) {
	workDir := pt.workingDir()
	plan, err := pt.executionPlan(ctx, harness, workDir)
	if err != nil {
		return nil, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}

	args := []string{"test", "-json", "-count=1"}
	if plan.Strategy == purevfs.StrategyGoOverlayManifest {
		if overlayPath, overlayCleanup, overlayErr := pt.buildGoOverlay(ctx); overlayErr == nil && overlayPath != "" {
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
		WorkspaceRoot: pt.workingDir(),
		ToolName:      "go_test",
		AgentID:       pt.id,
		AgentType:     "tester-pipeline",
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

	output := []byte(nil)
	if pt.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := pt.executionBroker.Run(cmdCtx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      append([]string{"go"}, args...),
		Workspace: pt.executionWorkspace(true),
	})
	if err != nil {
		return nil, fmt.Errorf("go test: %w", err)
	}
	output = append(output, runResult.Stdout...)
	output = append(output, runResult.Stderr...)
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

func (pt *PipelineTester) buildGoOverlay(ctx context.Context) (string, func(), error) {
	fa, ok := pt.fileAccess.(overlayAwareFileAccess)
	if !ok {
		return "", func() {}, nil
	}
	mods := fa.Modifications()
	if len(mods) == 0 {
		return "", func() {}, nil
	}

	tmpDir, err := os.MkdirTemp("", "sylk-go-overlay-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	type overlaySpec struct {
		Replace map[string]string `json:"Replace"`
	}
	spec := overlaySpec{Replace: make(map[string]string, len(mods))}
	root := pt.workingDir()

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
		if err := os.WriteFile(overlayPath, mod.NewContent, 0644); err != nil {
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
	if err := os.WriteFile(overlayFile, overlayJSON, 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return overlayFile, cleanup, nil
}

func (pt *PipelineTester) runGenericSuite(ctx context.Context, harness *testHarnessState, packages, files, testNames []string, verbose bool, timeoutSeconds int) (map[string]any, error) {
	workDir := pt.workingDir()
	plan, err := pt.executionPlan(ctx, harness, workDir)
	if err != nil {
		return nil, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}

	command := harness.RunCommand
	resolvedFiles := pt.resolveSuiteFocusFiles(ctx, harness, files)
	focusedFile := ""
	if len(resolvedFiles) == 1 && strings.TrimSpace(resolvedFiles[0]) != "" {
		focusedFile = resolvedFiles[0]
		command = strings.ReplaceAll(commandForFile(harness, focusedFile), "{file}", focusedFile)
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
		WorkspaceRoot: pt.workingDir(),
		ToolName:      string(harness.FrameworkID),
		AgentID:       pt.id,
		AgentType:     "tester-pipeline",
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
	if pt.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := pt.executionBroker.Run(cmdCtx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      args,
		Workspace: pt.executionWorkspace(true),
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
		"resolved_files":     resolvedFiles,
		"focused_file":       focusedFile,
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

func (pt *PipelineTester) resolveSuiteFocusFiles(ctx context.Context, harness *testHarnessState, files []string) []string {
	if harness == nil || len(files) == 0 {
		return append([]string(nil), files...)
	}
	resolved := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		focused, ok := pt.resolveSuiteFocusFile(ctx, harness, file)
		if !ok {
			continue
		}
		if _, exists := seen[focused]; exists {
			continue
		}
		seen[focused] = struct{}{}
		resolved = append(resolved, focused)
	}
	return resolved
}

func (pt *PipelineTester) resolveSuiteFocusFile(ctx context.Context, harness *testHarnessState, file string) (string, bool) {
	normalized := normalizePathCandidate(pt.workingDir(), file)
	if normalized == "" {
		return "", false
	}
	def := frameworkDefinition(harness.FrameworkID)
	candidates := make([]string, 0, 4)
	addCandidate := func(path string) {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" {
			return
		}
		for _, existing := range candidates {
			if existing == path {
				return
			}
		}
		candidates = append(candidates, path)
	}

	if pt.isRunnableSuiteFocusFile(harness, def, normalized) {
		addCandidate(normalized)
	}
	companion := recommendedOutputFile(def, normalized)
	if companion != "" && companion != normalized {
		addCandidate(companion)
	}
	for _, created := range harness.CreatedFiles {
		if strings.EqualFold(strings.TrimSpace(created), companion) {
			addCandidate(created)
		}
	}
	for _, existing := range harness.ExistingTestFiles {
		if strings.EqualFold(strings.TrimSpace(existing), companion) {
			addCandidate(existing)
		}
	}

	for _, candidate := range candidates {
		exists, err := pt.fileExists(ctx, candidate)
		if err == nil && exists {
			return candidate, true
		}
	}
	return "", false
}

func (pt *PipelineTester) isRunnableSuiteFocusFile(harness *testHarnessState, def *coretest.TestFrameworkDefinition, path string) bool {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return false
	}
	if isTestArtifact(path) {
		return true
	}
	for _, file := range harness.CreatedFiles {
		if filepath.ToSlash(strings.TrimSpace(file)) == path {
			return true
		}
	}
	for _, file := range harness.ExistingTestFiles {
		if filepath.ToSlash(strings.TrimSpace(file)) == path {
			return true
		}
	}
	if def == nil {
		return false
	}
	base := filepath.Base(path)
	for _, pattern := range def.TestFilePatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
	}
	return false
}

func commandForFile(harness *testHarnessState, file string) string {
	if def := frameworkDefinition(harness.FrameworkID); def != nil && strings.TrimSpace(def.RunFileCommand) != "" {
		return strings.ReplaceAll(def.RunFileCommand, "{file}", file)
	}
	return harness.RunCommand
}

func needsMaterializedWorkspace(fa versioning.FileAccess) bool {
	overlay, ok := fa.(overlayAwareFileAccess)
	return ok && len(overlay.Modifications()) > 0
}

func (pt *PipelineTester) materializeWorkspace(ctx context.Context) (string, func(), error) {
	root := pt.workingDir()
	tmpDir, err := os.MkdirTemp("", "sylk-tester-workspace-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(tmpDir, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if err := os.Link(path, target); err == nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode())
	})
	if err != nil {
		cleanup()
		return "", nil, err
	}

	if fa, ok := pt.fileAccess.(overlayAwareFileAccess); ok {
		for _, mod := range fa.Modifications() {
			rel, err := filepath.Rel(root, mod.OriginalPath)
			if err != nil {
				continue
			}
			target := filepath.Join(tmpDir, rel)
			switch mod.Operation {
			case versioning.FileOpDelete:
				_ = os.Remove(target)
			default:
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					cleanup()
					return "", nil, err
				}
				if err := os.WriteFile(target, mod.NewContent, 0644); err != nil {
					cleanup()
					return "", nil, err
				}
			}
		}
	}

	return tmpDir, cleanup, nil
}

func parseGoTestJSON(output []byte) map[string]any {
	type goCase struct {
		Name    string
		Package string
		Status  tester.TestStatus
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
			tc.Status = tester.StatusPassed
			tc.Elapsed = time.Duration(evt.Elapsed * float64(time.Second))
			passed++
		case "fail":
			tc.Status = tester.StatusFailed
			tc.Elapsed = time.Duration(evt.Elapsed * float64(time.Second))
			failed++
		case "skip":
			tc.Status = tester.StatusSkipped
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

func suiteResultFromExecution(exec map[string]any, start time.Time) *tester.TestSuiteResult {
	suite := &tester.TestSuiteResult{
		SuiteID:     uuid.NewString(),
		Name:        "test_run",
		StartedAt:   start,
		CompletedAt: time.Now(),
		Duration:    time.Since(start),
	}

	suite.Passed = intValue(exec["passed"])
	suite.Failed = intValue(exec["failed"])
	suite.Skipped = intValue(exec["skipped"])
	suite.TotalTests = suite.Passed + suite.Failed + suite.Skipped

	if raw, ok := exec["results"].([]map[string]any); ok {
		suite.Results = make([]tester.TestResult, 0, len(raw))
		for _, item := range raw {
			status, _ := item["status"].(tester.TestStatus)
			if status == "" {
				status = tester.StatusError
			}
			name, _ := item["name"].(string)
			pkg, _ := item["package"].(string)
			output, _ := item["output"].(string)
			suite.Results = append(suite.Results, tester.TestResult{
				TestID:    name,
				Name:      name,
				Package:   pkg,
				Status:    status,
				Output:    output,
				Timestamp: time.Now(),
			})
		}
	}

	return suite
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
