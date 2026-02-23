package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	promptskill "github.com/adalundhe/sylk/core/promptskills"
	"github.com/adalundhe/sylk/core/providers"
)

type planningLLM interface {
	AnalyzeRequirements(ctx context.Context, query string, params map[string]any) (*Requirements, error)
	DesignArchitecture(ctx context.Context, requirements *Requirements, patterns *CodebasePatterns) (*SolutionArchitecture, error)
	GenerateTasks(ctx context.Context, architecture *SolutionArchitecture, constraints *PlanConstraints) ([]*AtomicTask, error)
	ComposeUserResponse(ctx context.Context, request plannerConversationRequest) (string, error)
}

type anthropicPlanner struct {
	provider           *providers.AnthropicProvider
	maxTokens          int
	thinkingBudget     int
	system             string
	conversationSystem string
	timeout            time.Duration
	logger             *slog.Logger
}

type plannerThoughtCallback func(stage string, thought string)

type plannerThoughtContextKey struct{}

var ErrArchitectPlannerAuthNotConfigured = errors.New("architect planner auth not configured")

const plannerJSONSystemPrompt = `You are the Sylk Architect planner.
Return strictly valid JSON with no markdown, no prose, and no extra keys.
Never wrap JSON in code fences.
Keep outputs concise and deterministic.`

func (a *Architect) initPlanner(cfg Config) error {
	if !cfg.EnableLLM {
		return nil
	}

	if a.ensurePlanner() == nil {
		a.logger.Warn("architect llm planner unavailable; using deterministic fallback")
	}
	return nil
}

func (a *Architect) ensurePlanner() planningLLM {
	if !a.config.EnableLLM {
		return nil
	}
	if planner := a.currentPlanner(); planner != nil {
		return planner
	}

	a.plannerMu.Lock()
	defer a.plannerMu.Unlock()

	if a.planner != nil {
		return a.planner
	}

	planner, err := newAnthropicPlanner(a.config, a.logger)
	if err != nil {
		if !errors.Is(err, ErrArchitectPlannerAuthNotConfigured) {
			a.logger.Warn("architect llm planner init failed", "error", err)
		}
		return nil
	}

	a.planner = planner
	a.logger.Info("architect llm planner enabled", "model", a.config.Model)
	return a.planner
}

func (a *Architect) currentPlanner() planningLLM {
	a.plannerMu.RLock()
	planner := a.planner
	a.plannerMu.RUnlock()
	return planner
}

func (a *Architect) tryAnalyzeRequirementsWithLLM(
	ctx context.Context,
	query string,
	params map[string]any,
) (*Requirements, bool) {
	planner := a.ensurePlanner()
	if planner == nil {
		return nil, false
	}
	requirements, err := planner.AnalyzeRequirements(ctx, query, params)
	if err != nil {
		a.logger.Warn("architect llm requirements fallback", "error", err)
		return nil, false
	}
	return requirements, true
}

func (a *Architect) tryDesignArchitectureWithLLM(
	ctx context.Context,
	requirements *Requirements,
	patterns *CodebasePatterns,
) (*SolutionArchitecture, bool) {
	planner := a.ensurePlanner()
	if planner == nil {
		return nil, false
	}
	architecture, err := planner.DesignArchitecture(ctx, requirements, patterns)
	if err != nil {
		a.logger.Warn("architect llm design fallback", "error", err)
		return nil, false
	}
	return architecture, true
}

func (a *Architect) tryGenerateTasksWithLLM(
	ctx context.Context,
	architecture *SolutionArchitecture,
	constraints *PlanConstraints,
) ([]*AtomicTask, bool) {
	planner := a.ensurePlanner()
	if planner == nil {
		return nil, false
	}
	tasks, err := planner.GenerateTasks(ctx, architecture, constraints)
	if err != nil {
		a.logger.Warn("architect llm task fallback", "error", err)
		return nil, false
	}
	return normalizeTaskGraph(tasks), true
}

func newAnthropicPlanner(cfg Config, logger *slog.Logger) (planningLLM, error) {
	providerCfg := providers.AnthropicConfig{
		BaseConfig: providers.BaseConfig{
			APIKey:         cfg.AnthropicAPIKey,
			Model:          cfg.Model,
			MaxTokens:      cfg.MaxOutputTokens,
			Temperature:    0.7,
			MaxRetries:     cfg.LLMRetryMax,
			RetryBaseDelay: 200 * time.Millisecond,
			RetryMaxDelay:  time.Second,
		},
		EnableCaching:    !cfg.DisablePromptCache,
		PromptCacheTTL:   cfg.PromptCacheTTL,
		SystemPrompt:     buildPlannerSystemPrompt(cfg.SystemPrompt),
		ThinkingBudget:   cfg.ThinkingBudget,
		AdaptiveThinking: true,
	}
	if providerCfg.Model == "" {
		providerCfg.Model = DefaultArchitectModel
	}
	if providerCfg.MaxTokens == 0 {
		providerCfg.MaxTokens = DefaultMaxOutputTokens
	}

	promptSkills := promptskill.DiscoverAgentSkills("architect")
	provider, err := providers.NewAnthropicProvider(providerCfg, promptSkills...)
	if err != nil {
		if strings.Contains(err.Error(), "api_key") {
			return nil, ErrArchitectPlannerAuthNotConfigured
		}
		return nil, err
	}

	return &anthropicPlanner{
		provider:           provider,
		maxTokens:          cfg.MaxOutputTokens,
		thinkingBudget:     cfg.ThinkingBudget,
		system:             buildPlannerSystemPrompt(cfg.SystemPrompt),
		conversationSystem: buildPlannerConversationSystemPrompt(cfg.SystemPrompt),
		timeout:            cfg.LLMRequestTimeout,
		logger:             logger,
	}, nil
}

func buildPlannerSystemPrompt(base string) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return plannerJSONSystemPrompt
	}
	return plannerJSONSystemPrompt + "\n\nContext:\n" + trimmed
}

func withPlannerThoughtCallback(ctx context.Context, cb plannerThoughtCallback) context.Context {
	if cb == nil {
		return ctx
	}
	return context.WithValue(ctx, plannerThoughtContextKey{}, cb)
}

func emitPlannerThought(ctx context.Context, stage string, thought string) {
	if ctx == nil {
		return
	}
	cb, ok := ctx.Value(plannerThoughtContextKey{}).(plannerThoughtCallback)
	if !ok || cb == nil {
		return
	}
	stage = strings.TrimSpace(stage)
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return
	}
	cb(stage, thought)
}

func (p *anthropicPlanner) AnalyzeRequirements(
	ctx context.Context,
	query string,
	params map[string]any,
) (*Requirements, error) {
	prompt := buildRequirementsPrompt(query, params)
	var payload requirementsPayload
	if err := p.requestJSONWithBudgets(
		ctx,
		prompt,
		&payload,
		requirementsBudgets(p.maxTokens),
		p.systemForStage("requirements"),
		"requirements",
	); err != nil {
		return nil, err
	}
	return payload.toRequirements(query, params), nil
}

func (p *anthropicPlanner) DesignArchitecture(
	ctx context.Context,
	requirements *Requirements,
	patterns *CodebasePatterns,
) (*SolutionArchitecture, error) {
	prompt := buildDesignPrompt(requirements, patterns)
	var payload architecturePayload
	if err := p.requestJSONWithBudgets(
		ctx,
		prompt,
		&payload,
		designBudgets(p.maxTokens),
		p.systemForStage("design"),
		"design",
	); err != nil {
		return nil, err
	}
	return payload.toArchitecture(requirements), nil
}

func (p *anthropicPlanner) GenerateTasks(
	ctx context.Context,
	architecture *SolutionArchitecture,
	constraints *PlanConstraints,
) ([]*AtomicTask, error) {
	prompt := buildTaskPrompt(architecture, constraints)
	tasks, err := p.parseTasksWithBudgets(
		ctx,
		prompt,
		taskBudgets(p.maxTokens),
		p.systemForStage("tasks"),
		"tasks",
	)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("planner returned zero tasks")
	}
	return tasks, nil
}

func (p *anthropicPlanner) parseTasksWithBudgets(
	ctx context.Context,
	prompt string,
	budgets []int,
	system string,
	stage string,
) ([]*AtomicTask, error) {
	var lastErr error
	for _, budget := range budgets {
		text, _, err := p.requestTextWithMaxTokens(ctx, prompt, budget, system, stage)
		if err != nil {
			lastErr = err
			continue
		}
		tasks, parseErr := parseTaskPayload(text)
		if parseErr == nil {
			return tasks, nil
		}
		lastErr = parseErr
	}
	return nil, lastErr
}

func (p *anthropicPlanner) requestJSONWithBudgets(
	ctx context.Context,
	prompt string,
	out any,
	budgets []int,
	system string,
	stage string,
) error {
	var lastErr error
	for _, budget := range budgets {
		text, _, err := p.requestTextWithMaxTokens(ctx, prompt, budget, system, stage)
		if err != nil {
			lastErr = err
			continue
		}
		decodeErr := decodeJSONPayload(text, out)
		if decodeErr == nil {
			return nil
		}
		lastErr = decodeErr
	}
	return lastErr
}

func (p *anthropicPlanner) requestTextWithMaxTokens(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
) (string, *providers.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.requestTextStreamingOnce(ctx, prompt, maxTokens, system, stage, nil)
}

func (p *anthropicPlanner) requestTextStreamingWithMaxTokens(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (string, *providers.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.requestTextStreamingOnce(ctx, prompt, maxTokens, system, stage, onChunk)
}

func (p *anthropicPlanner) requestTextStreamingOnce(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (string, *providers.Usage, error) {
	resolvedSystem := p.resolveSystemPrompt(system)
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
		MaxTokens:      maxTokens,
		SystemPrompt:   resolvedSystem,
		ThinkingBudget: p.resolveThinkingBudget(maxTokens),
	}

	var text strings.Builder
	var thoughts strings.Builder
	var finalUsage *providers.Usage
	err := p.provider.StreamWithHandler(ctx, req, func(chunk *providers.StreamChunk) error {
		switch chunk.Type {
		case providers.ChunkTypeStart:
			text.Reset()
			thoughts.Reset()
			if chunk.Usage != nil && chunk.Usage.InputTokens > 0 {
				emitArchitectEarlyUsage(ctx, chunk.Usage.InputTokens)
			}
		case providers.ChunkTypeText:
			text.WriteString(chunk.Text)
			if onChunk != nil {
				onChunk(chunk.Text)
			}
		case providers.ChunkTypeThought:
			if thought := appendThoughtDelta(&thoughts, chunk.Text); thought != "" {
				emitPlannerThought(ctx, stage, thought)
			}
		case providers.ChunkTypeEnd:
			if chunk.Usage != nil {
				finalUsage = chunk.Usage
				accumulateArchitectUsage(ctx, chunk.Usage)
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	result := strings.TrimSpace(text.String())
	if result == "" {
		return "", nil, fmt.Errorf("planner returned empty content")
	}
	return result, finalUsage, nil
}

func appendThoughtDelta(buffer *strings.Builder, delta string) string {
	if buffer == nil {
		return strings.TrimSpace(delta)
	}
	if delta != "" {
		buffer.WriteString(delta)
	}
	return strings.TrimSpace(buffer.String())
}

// resolveThinkingBudget returns the thinking budget for a planner request.
// Uses the configured thinkingBudget if set, otherwise falls back to
// maxTokens/3 dynamic computation. Clamps to maxTokens - 1.
func (p *anthropicPlanner) resolveThinkingBudget(maxTokens int) int {
	if maxTokens < 2048 {
		return 0
	}
	budget := p.thinkingBudget
	if budget <= 0 {
		budget = maxTokens / 3
		if budget < 1024 {
			budget = 1024
		}
	}
	if budget >= maxTokens {
		return maxTokens - 1
	}
	return budget
}

func (p *anthropicPlanner) systemForStage(stage string) string {
	stagePrompt := strings.TrimSpace(ArchitectPlannerPromptForStage(stage))
	if stagePrompt == "" {
		return p.system
	}
	return buildPlannerSystemPrompt(stagePrompt)
}

func (p *anthropicPlanner) resolveSystemPrompt(system string) string {
	trimmed := strings.TrimSpace(system)
	if trimmed != "" {
		return trimmed
	}
	return p.system
}

func requirementsBudgets(base int) []int {
	return compactBudgets(base, 1536, 3072, 4096)
}

func designBudgets(base int) []int {
	return compactBudgets(base, 4096, 6144, 8192)
}

func taskBudgets(base int) []int {
	return compactBudgets(base, 3072, 6144, 8192)
}

func compactBudgets(base int, a int, b int, c int) []int {
	values := []int{maxInt(base, a), maxInt(base*2, b), maxInt(base*3, c)}
	seen := map[int]struct{}{}
	budgets := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		budgets = append(budgets, value)
	}
	return budgets
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

type requirementsPayload struct {
	Goals                   json.RawMessage `json:"goals"`
	Constraints             json.RawMessage `json:"constraints"`
	Dependencies            json.RawMessage `json:"dependencies"`
	ClarificationQuestions  json.RawMessage `json:"clarification_questions"`
	Unknowns                json.RawMessage `json:"unknowns"`
	Recommendations         json.RawMessage `json:"provisional_recommendations"`
	Tradeoffs               json.RawMessage `json:"tradeoffs"`
	RecommendationNarrative string          `json:"recommendation_narrative"`
	Scope                   string          `json:"scope"`
	Priority                string          `json:"priority"`
}

func (p requirementsPayload) toRequirements(query string, params map[string]any) *Requirements {
	requirements := &Requirements{
		Query:        query,
		Goals:        parseStringList(p.Goals),
		Constraints:  parseStringList(p.Constraints),
		Dependencies: parseStringList(p.Dependencies),
		Scope:        nonEmptyString(p.Scope, "project"),
		Priority:     strings.TrimSpace(p.Priority),
	}
	requirements.Metadata = requirementsMetadataFromPayload(p)
	if len(requirements.Goals) == 0 {
		requirements.Goals = []string{query}
	}
	if params == nil {
		return requirements
	}
	applyRequirementOverrides(requirements, params)
	return requirements
}

func requirementsMetadataFromPayload(payload requirementsPayload) map[string]any {
	questions := parseStringList(payload.ClarificationQuestions)
	unknowns := parseStringList(payload.Unknowns)
	recommendations := parseStringList(payload.Recommendations)
	tradeoffs := parseStringList(payload.Tradeoffs)
	if len(questions) == 0 && len(unknowns) == 0 && len(recommendations) == 0 && len(tradeoffs) == 0 {
		return nil
	}
	metadata := map[string]any{}
	if len(questions) > 0 {
		metadata["clarification_questions"] = questions
	}
	if len(unknowns) > 0 {
		metadata["unknowns"] = unknowns
	}
	if len(recommendations) > 0 {
		metadata["provisional_recommendations"] = recommendations
	}
	if len(tradeoffs) > 0 {
		metadata["tradeoffs"] = tradeoffs
	}
	if narrative := strings.TrimSpace(payload.RecommendationNarrative); narrative != "" {
		metadata["recommendation_narrative"] = narrative
	}
	return metadata
}

func applyRequirementOverrides(requirements *Requirements, params map[string]any) {
	if requirements == nil || params == nil {
		return
	}
	if scope, ok := params["scope"].(string); ok && scope != "" {
		requirements.Scope = scope
	}
	if goals, ok := params["goals"].([]string); ok && len(goals) > 0 {
		requirements.Goals = goals
	}
	if constraints, ok := params["constraints"].([]string); ok && len(constraints) > 0 {
		requirements.Constraints = constraints
	}
}

type architecturePayload struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Components  []ComponentSpec     `json:"components"`
	Interfaces  []InterfaceSpec     `json:"interfaces"`
	Patterns    json.RawMessage     `json:"patterns"`
	Layers      []ArchitectureLayer `json:"layers"`
}

func (p architecturePayload) toArchitecture(requirements *Requirements) *SolutionArchitecture {
	name := strings.TrimSpace(p.Name)
	if name == "" && requirements != nil {
		name = fmt.Sprintf("Architecture for: %s", truncateString(requirements.Query, 50))
	}
	desc := strings.TrimSpace(p.Description)
	if desc == "" && requirements != nil {
		desc = requirements.Query
	}
	return &SolutionArchitecture{
		Name:        name,
		Description: desc,
		Components:  p.Components,
		Interfaces:  p.Interfaces,
		Patterns:    parseStringList(p.Patterns),
		Layers:      p.Layers,
	}
}

type taskListPayload struct {
	Tasks []taskPayload `json:"tasks"`
}

type taskPayload struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	AgentType       string   `json:"agent_type"`
	SuccessCriteria []string `json:"success_criteria"`
	Dependencies    []string `json:"dependencies"`
	EstimatedTokens int      `json:"estimated_tokens"`
	Complexity      string   `json:"complexity"`

	// Rich specification fields.
	AcceptanceCriteria  []acceptanceCriterionPayload `json:"acceptance_criteria"`
	Guidelines          []string                     `json:"guidelines"`
	ImplementationGuide string                       `json:"implementation_guide"`
	Examples            []taskExamplePayload         `json:"examples"`
	AffectedFiles       []taskFileTargetPayload      `json:"affected_files"`
	TestRequirements    []string                     `json:"test_requirements"`
	RiskFactors         []string                     `json:"risk_factors"`
}

type acceptanceCriterionPayload struct {
	Given    string `json:"given"`
	When     string `json:"when"`
	Then     string `json:"then"`
	Priority string `json:"priority"`
}

type taskExamplePayload struct {
	Label       string `json:"label"`
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
}

type taskFileTargetPayload struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

func (p taskPayload) toTask(index int) *AtomicTask {
	taskID := strings.TrimSpace(p.ID)
	if taskID == "" {
		taskID = fmt.Sprintf("task_%d", index+1)
	}
	return &AtomicTask{
		ID:                  taskID,
		Name:                strings.TrimSpace(p.Name),
		Description:         strings.TrimSpace(p.Description),
		AgentType:           normalizeTaskAgentType(p.AgentType),
		SuccessCriteria:     nonEmptySlice(p.SuccessCriteria),
		Dependencies:        nonEmptySlice(p.Dependencies),
		EstimatedTokens:     nonZeroInt(p.EstimatedTokens, 3000),
		Complexity:          parseComplexity(p.Complexity),
		Status:              TaskStatusPending,
		AcceptanceCriteria:  toAcceptanceCriteria(p.AcceptanceCriteria),
		Guidelines:          nonEmptySlice(p.Guidelines),
		ImplementationGuide: strings.TrimSpace(p.ImplementationGuide),
		Examples:            toTaskExamples(p.Examples),
		AffectedFiles:       toTaskFileTargets(p.AffectedFiles),
		TestRequirements:    nonEmptySlice(p.TestRequirements),
		RiskFactors:         nonEmptySlice(p.RiskFactors),
	}
}

func toAcceptanceCriteria(payloads []acceptanceCriterionPayload) []AcceptanceCriterion {
	if len(payloads) == 0 {
		return nil
	}
	criteria := make([]AcceptanceCriterion, 0, len(payloads))
	for _, p := range payloads {
		ac := AcceptanceCriterion{
			Given:    strings.TrimSpace(p.Given),
			When:     strings.TrimSpace(p.When),
			Then:     strings.TrimSpace(p.Then),
			Priority: normalizeACPriority(p.Priority),
		}
		if ac.Then == "" {
			continue
		}
		criteria = append(criteria, ac)
	}
	return criteria
}

func normalizeACPriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "must", "should", "could":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "must"
	}
}

func toTaskExamples(payloads []taskExamplePayload) []TaskExample {
	if len(payloads) == 0 {
		return nil
	}
	examples := make([]TaskExample, 0, len(payloads))
	for _, p := range payloads {
		ex := TaskExample{
			Label:       strings.TrimSpace(p.Label),
			Code:        strings.TrimSpace(p.Code),
			Explanation: strings.TrimSpace(p.Explanation),
		}
		if ex.Code == "" && ex.Label == "" {
			continue
		}
		examples = append(examples, ex)
	}
	return examples
}

func toTaskFileTargets(payloads []taskFileTargetPayload) []TaskFileTarget {
	if len(payloads) == 0 {
		return nil
	}
	targets := make([]TaskFileTarget, 0, len(payloads))
	for _, p := range payloads {
		ft := TaskFileTarget{
			Path:      strings.TrimSpace(p.Path),
			Operation: normalizeFileOp(p.Operation),
			Reason:    strings.TrimSpace(p.Reason),
		}
		if ft.Path == "" {
			continue
		}
		targets = append(targets, ft)
	}
	return targets
}

func normalizeFileOp(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "create", "modify", "delete":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "modify"
	}
}

func parseTaskPayload(text string) ([]*AtomicTask, error) {
	entries, err := decodeTaskEntries(text)
	if err != nil {
		return nil, err
	}
	tasks := make([]*AtomicTask, 0, len(entries))
	for i := range entries {
		tasks = append(tasks, entries[i].toTask(i))
	}
	return tasks, nil
}

func decodeTaskEntries(text string) ([]taskPayload, error) {
	var payload taskListPayload
	if err := decodeJSONPayload(text, &payload); err == nil && len(payload.Tasks) > 0 {
		return payload.Tasks, nil
	}
	var list []taskPayload
	if err := decodeJSONPayload(text, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func decodeJSONPayload(text string, out any) error {
	for _, candidate := range jsonCandidates(text) {
		if json.Unmarshal([]byte(candidate), out) == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to decode planner json")
}

func jsonCandidates(text string) []string {
	candidates := []string{strings.TrimSpace(text)}
	fenced := extractFencedJSON(text)
	if fenced != "" {
		candidates = append(candidates, fenced)
	}
	object := extractJSONObject(text)
	if object != "" {
		candidates = append(candidates, object)
	}
	return uniqueNonEmptyStrings(candidates)
}

func extractFencedJSON(text string) string {
	start := strings.Index(text, "```")
	if start == -1 {
		return ""
	}
	rest := text[start+3:]
	if strings.HasPrefix(rest, "json") {
		rest = rest[4:]
	}
	end := strings.Index(rest, "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func extractJSONObject(text string) string {
	start := strings.IndexAny(text, "{[")
	if start == -1 {
		return ""
	}
	snippet := strings.TrimSpace(text[start:])
	for i := len(snippet); i > 0; i-- {
		candidate := strings.TrimSpace(snippet[:i])
		var raw json.RawMessage
		if json.Unmarshal([]byte(candidate), &raw) == nil {
			return candidate
		}
	}
	return ""
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func buildRequirementsPrompt(query string, params map[string]any) string {
	base := fmt.Sprintf(RequirementsAnalysisPrompt, query, mustJSON(params))
	return base + `

Return JSON only, exactly:
{
  "goals": ["..."],
  "constraints": ["..."],
  "dependencies": ["..."],
  "provisional_recommendations": ["..."],
  "tradeoffs": ["..."],
  "recommendation_narrative": "...",
  "clarification_questions": ["..."],
  "unknowns": ["..."],
  "scope": "project|module|file",
  "priority": "low|medium|high|critical"
}

Hard limits:
- At most 6 goals, 6 constraints, 6 dependencies
- At most 5 clarification_questions and 5 unknowns
- Each string must be <= 20 words
- recommendation_narrative must be <= 90 words
- If the request is underspecified, include concrete clarification_questions
- If the user asks for recommendations or preferences, include opinionated provisional_recommendations plus concise tradeoffs
- For recommendation questions, include at least 2 provisional_recommendations and 2 tradeoffs
`
}

func buildDesignPrompt(requirements *Requirements, patterns *CodebasePatterns) string {
	base := fmt.Sprintf(ArchitectureDesignPrompt, mustJSON(requirements), mustJSON(patterns))
	return base + `

Return JSON only, exactly:
{
  "name": "...",
  "description": "...",
  "components": [
    {
      "name": "...",
      "type": "backend|frontend|data|integration|test",
      "description": "...",
      "dependencies": ["component_name"],
      "interfaces": ["interface_name"],
      "file_path": ""
    }
  ],
  "interfaces": [
    {
      "name": "...",
      "from": "...",
      "to": "...",
      "type": "api|event|internal",
      "description": "...",
      "methods": [{"name":"...","parameters":["..."],"returns":"..."}]
    }
  ],
  "patterns": ["..."],
  "layers": [{"name":"...","components":["..."],"order":1}]
}

Hard limits:
- At most 6 components, 6 interfaces, 6 patterns, 4 layers
- Keep all descriptions <= 24 words
- Use Go-style file paths when file_path is set (e.g. core/providers/token_rotation.go)
`
}

func buildTaskPrompt(architecture *SolutionArchitecture, constraints *PlanConstraints) string {
	base := fmt.Sprintf(TaskDecompositionPrompt, mustJSON(architecture))
	return base + "\n\nConstraints:\n" + mustJSON(constraints) + `

Return JSON only, exactly:
{
  "tasks": [
    {
      "id": "task_1",
      "name": "Short imperative name",
      "description": "Detailed implementation description. Include what to build, how it fits the architecture, and key design decisions. Be specific enough that an agent can implement without follow-up questions.",
      "agent_type": "engineer|designer|tester|inspector|architect",
      "success_criteria": ["Criterion 1", "Criterion 2"],
      "dependencies": ["task_id"],
      "estimated_tokens": 3000,
      "complexity": "low|medium|high|critical",
      "acceptance_criteria": [
        {
          "given": "Precondition or initial state",
          "when": "Action or trigger",
          "then": "Expected observable outcome",
          "priority": "must|should|could"
        }
      ],
      "guidelines": [
        "Implementation constraint or convention to follow"
      ],
      "implementation_guide": "Step-by-step implementation instructions. Reference specific functions, types, and patterns from the codebase. Include the sequence of operations, error handling strategy, and integration points.",
      "examples": [
        {
          "label": "What the example demonstrates",
          "code": "func Example() { ... }",
          "explanation": "Why this pattern applies"
        }
      ],
      "affected_files": [
        {
          "path": "core/module/file.go",
          "operation": "create|modify|delete",
          "reason": "Why this file is affected"
        }
      ],
      "test_requirements": [
        "Specific test case that must pass"
      ],
      "risk_factors": [
        "Potential blocker or failure mode"
      ]
    }
  ]
}

Hard limits:
- At most 10 tasks
- Each task MUST have at least 2 acceptance_criteria with priority "must"
- Each task MUST have at least 1 affected_files entry
- Each task MUST have implementation_guide (minimum 2 sentences)
- guidelines: 1-4 items per task
- test_requirements: 1-4 items per task
- examples: 0-2 per task (include for non-trivial tasks)
- risk_factors: 0-3 per task
- success_criteria: 2-4 items per task
`
}

func mustJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func nonEmptySlice(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func nonEmptyString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func nonZeroInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func parseComplexity(raw string) TaskComplexity {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return ComplexityLow
	case "high":
		return ComplexityHigh
	case "critical":
		return ComplexityCritical
	default:
		return ComplexityMedium
	}
}

func parseStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return nonEmptySlice(list)
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return nonEmptySlice([]string{single})
	}
	var maps []map[string]any
	if json.Unmarshal(raw, &maps) == nil {
		return extractStringValues(maps)
	}
	return nil
}

func extractStringValues(items []map[string]any) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, firstNonEmptyMapString(item)...)
	}
	return nonEmptySlice(values)
}

func firstNonEmptyMapString(item map[string]any) []string {
	keys := []string{"description", "name", "id", "value", "text"}
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return []string{text}
		}
	}
	return nil
}

func normalizeTaskGraph(tasks []*AtomicTask) []*AtomicTask {
	ensureTaskIDs(tasks)
	idSet := buildTaskIDSet(tasks)
	nameIndex := buildTaskNameIndex(tasks)
	for _, task := range tasks {
		normalizeTask(task, idSet, nameIndex)
	}
	return tasks
}

func ensureTaskIDs(tasks []*AtomicTask) {
	for i, task := range tasks {
		if task == nil {
			continue
		}
		if strings.TrimSpace(task.ID) == "" {
			task.ID = fmt.Sprintf("task_%d", i+1)
		}
	}
}

func buildTaskIDSet(tasks []*AtomicTask) map[string]struct{} {
	idSet := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		idSet[strings.TrimSpace(task.ID)] = struct{}{}
	}
	return idSet
}

func buildTaskNameIndex(tasks []*AtomicTask) map[string]string {
	index := make(map[string]string, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		key := canonicalTaskKey(task.Name)
		if key != "" {
			index[key] = task.ID
		}
	}
	return index
}

func normalizeTask(task *AtomicTask, idSet map[string]struct{}, nameIndex map[string]string) {
	if task == nil {
		return
	}
	task.AgentType = normalizeTaskAgentType(task.AgentType)
	task.Dependencies = normalizeDependencies(task.Dependencies, idSet, nameIndex)
	if len(task.SuccessCriteria) == 0 {
		task.SuccessCriteria = []string{"Task completed"}
	}
}

func normalizeDependencies(dependencies []string, idSet map[string]struct{}, nameIndex map[string]string) []string {
	result := make([]string, 0, len(dependencies))
	seen := map[string]struct{}{}
	for _, dependency := range dependencies {
		mapped := mapDependency(dependency, idSet, nameIndex)
		if mapped == "" {
			continue
		}
		if _, ok := seen[mapped]; ok {
			continue
		}
		seen[mapped] = struct{}{}
		result = append(result, mapped)
	}
	return result
}

func mapDependency(dependency string, idSet map[string]struct{}, nameIndex map[string]string) string {
	trimmed := strings.TrimSpace(dependency)
	if trimmed == "" {
		return ""
	}
	if _, ok := idSet[trimmed]; ok {
		return trimmed
	}
	key := canonicalTaskKey(trimmed)
	if value, ok := nameIndex[key]; ok {
		return value
	}
	return ""
}

func canonicalTaskKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "implement ")
	return strings.TrimSpace(value)
}

func normalizeTaskAgentType(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "engineer", "designer", "tester", "inspector", "architect":
		return strings.ToLower(strings.TrimSpace(agentType))
	default:
		return "engineer"
	}
}
