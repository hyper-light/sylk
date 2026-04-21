package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

func architectCoreSkillNames() []string {
	return filterSyntheticRuntimeTools(architectToolManifest().DefaultVisibleNames())
}

func architectAllSkillNames() []string {
	return architectToolManifest().AllowedNames()
}

func architectToolManifest() *toolruntime.PolicyManifest {
	return architectToolManifestForRegistry(nil)
}

func architectToolManifestForRegistry(registry *skills.Registry) *toolruntime.PolicyManifest {
	policies := []toolruntime.ToolPolicy{
		// Phase 2.K / CR-4 refactor: start_planning + plan_workflow
		// folded into plan (action=start|workflow).
		toolruntime.NewToolPolicy("plan", toolruntime.EffectMutating, toolruntime.DomainPlanning, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		// Phase 1 refactor: `consult`, `monitor_execution`,
		// `ask_user_question` removed. `consult_peer` (fabric) and
		// `ask_user_clarification` (shared) replace them;
		// `monitor_execution` uses `query_peer_activity`.
		toolruntime.NewToolPolicy("plan_mode", toolruntime.EffectMutating, toolruntime.DomainPlanning, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("interrupt_handler", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		// Phase 2.4 refactor: pre_delegation_declare + validate_pre_delegation
		// collapsed into delegation(action=declare|validate).
		toolruntime.NewToolPolicy("delegation", toolruntime.EffectMutating, toolruntime.DomainPlanning, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("validate_work", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("query_global_review_state", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal),
		// Phase 2.3 refactor: route_plan_acceptance + handle_plan_acceptance_result
		// + present_plan_approval_dialog collapsed into plan_acceptance.
		toolruntime.NewToolPolicy("plan_acceptance", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeGuardian, toolruntime.WithApprovalSensitive(), toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("ask_user_clarification", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeGuardian, toolruntime.WithApprovalSensitive(), toolruntime.WithVisibleByDefault()),
		// Phase 2.6 refactor: route_requirements_research + read_research_paper
		// collapsed into academic_research(action=request|read).
		toolruntime.NewToolPolicy("academic_research", toolruntime.EffectMutating, toolruntime.DomainKnowledge, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("reroute_request", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("self_diagnostic", toolruntime.EffectReadOnly, toolruntime.DomainSystem, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy(toolruntime.SearchToolName, toolruntime.EffectReadOnly, toolruntime.DomainDiscovery, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault(), toolruntime.WithSearchable(false)),
	}
	policies = shared.AppendMemoryForestToolPolicies(policies, registry, "architect")
	policies = fabric.AppendFabricAwarenessToolPolicies(policies, registry)
	return toolruntime.ApplyAuthorityProfile("architect", toolruntime.NewManifest("architect", "architect.default", policies...))
}

func filterSyntheticRuntimeTools(names []string) []string {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if name == toolruntime.SearchToolName {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func registerArchitectSafetyHook(hooks *skills.HookRegistry, _ *skills.Registry, allowed []string) {
	if hooks == nil {
		return
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	hooks.RegisterPreToolCallHook(
		"architect_safety_catch",
		skills.HookPriorityHigh,
		func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
			if data == nil {
				return skills.HookResult{Continue: true}
			}
			if !allowedSet[data.ToolName] {
				return skills.HookResult{
					Continue: false,
					Error:    fmt.Errorf("SECURITY VIOLATION: Architect is not permitted to execute tool %q", data.ToolName),
				}
			}
			return skills.HookResult{Continue: true}
		},
	)
}

// Skills returns the architect's skill registry.
func (a *Architect) Skills() *skills.Registry {
	return a.skills
}

// SkillLoader returns the architect's progressive skill loader.
func (a *Architect) SkillLoader() *skills.Loader {
	return a.skillLoader
}

// Hooks returns the architect's hook registry.
func (a *Architect) Hooks() *skills.HookRegistry {
	return a.hooks
}

// RegisterSkill registers a skill at runtime.
func (a *Architect) RegisterSkill(skill *skills.Skill) error {
	if skill == nil {
		return fmt.Errorf("skill is required")
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return fmt.Errorf("skill name is required")
	}
	if a.toolRuntime() == nil || !a.toolRuntime().Allows(name) {
		return fmt.Errorf("skill %q is outside architect capability scope %q", name, architectToolManifest().CapabilityScope)
	}
	if err := a.skills.Register(skill); err != nil {
		return err
	}
	a.toolDefsDirty = true
	return nil
}

// PrepareToolDefinitionsForRequest progressively loads relevant skills and returns loaded tool definitions.
func (a *Architect) PrepareToolDefinitionsForRequest(req *ArchitectRequest) []map[string]any {
	a.prepareSkillsForRequest(req)
	return a.GetLoadedSkillDefinitions()
}

// LoadSkillsForInput loads skills by keyword match.
func (a *Architect) LoadSkillsForInput(input string) []string {
	return a.skillLoader.LoadForInput(input)
}

// LoadSkillDomain loads a full domain of skills.
func (a *Architect) LoadSkillDomain(domain string) (int, bool) {
	return a.skillLoader.LoadDomain(domain)
}

// OptimizeSkillsForBudget unloads skills to fit loader budget.
func (a *Architect) OptimizeSkillsForBudget() int {
	return a.skillLoader.OptimizeForBudget()
}

// LoadSkillsForContext performs context-aware skill loading.
func (a *Architect) LoadSkillsForContext(ctx skills.LoadContext) skills.LoadResult {
	return a.skillLoader.LoadForContext(ctx)
}

// GetLoadedSkillDefinitions returns tool definitions for loaded skills.
func (a *Architect) GetLoadedSkillDefinitions() []map[string]any {
	return architectProviderToolsToDefinitions(a.buildToolDefinitions())
}

// GetToolDefinitions returns tool definitions for loaded skills.
func (a *Architect) GetToolDefinitions() []map[string]any {
	return architectProviderToolsToDefinitions(a.buildToolDefinitions())
}

// RegisterPrePromptHook registers a pre-prompt hook.
func (a *Architect) RegisterPrePromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	a.hooks.RegisterPrePromptHook(name, priority, fn)
}

// RegisterPostPromptHook registers a post-prompt hook.
func (a *Architect) RegisterPostPromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	a.hooks.RegisterPostPromptHook(name, priority, fn)
}

// RegisterPreToolCallHook registers a pre-tool hook.
func (a *Architect) RegisterPreToolCallHook(name string, priority skills.HookPriority, fn skills.ToolCallHookFunc) {
	a.hooks.RegisterPreToolCallHook(name, priority, fn)
}

// RegisterPostToolCallHook registers a post-tool hook.
func (a *Architect) RegisterPostToolCallHook(name string, priority skills.HookPriority, fn skills.ToolCallHookFunc) {
	a.hooks.RegisterPostToolCallHook(name, priority, fn)
}

// ExecutePrePromptHooks runs pre-prompt hooks.
func (a *Architect) ExecutePrePromptHooks(
	ctx context.Context,
	data *skills.PromptHookData,
) (*skills.PromptHookData, error) {
	return a.hooks.ExecutePrePromptHooks(ctx, data)
}

// ExecutePostPromptHooks runs post-prompt hooks.
func (a *Architect) ExecutePostPromptHooks(
	ctx context.Context,
	data *skills.PromptHookData,
) (*skills.PromptHookData, error) {
	return a.hooks.ExecutePostPromptHooks(ctx, data)
}

// InvokeSkill executes a skill with hook enforcement.
func (a *Architect) InvokeSkill(ctx context.Context, name string, input json.RawMessage) *skills.Result {
	if a.toolRuntime() == nil {
		return &skills.Result{SkillName: name, Success: false, Error: "tool runtime is not configured"}
	}
	if _, err := a.toolRuntime().Activate(name); err != nil {
		return &skills.Result{SkillName: name, Success: false, Error: err.Error()}
	}
	raw := strings.TrimSpace(string(input))
	if raw == "" {
		raw = "{}"
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = "invoke_" + uuid.NewString()
	}
	execResult, err := a.toolRuntime().ExecuteRaw(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        "invoke_" + uuid.NewString(),
			Name:      name,
			Arguments: raw,
		},
		AgentID:         a.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: a.toolRuntime().CapabilityScope(),
	})
	if err != nil {
		return &skills.Result{SkillName: name, Success: false, Error: err.Error()}
	}
	return &skills.Result{SkillName: name, Success: true, Data: execResult.Data}
}

func (a *Architect) runPreToolHooks(ctx context.Context, name string, input json.RawMessage) error {
	if a.hooks == nil {
		return nil
	}
	hookData := &skills.ToolCallHookData{
		ToolName: name,
		AgentID:  "architect",
		Input: map[string]any{
			"raw": string(input),
		},
	}
	_, result, err := a.hooks.ExecutePreToolCallHooks(ctx, hookData)
	if err != nil {
		return err
	}
	if !result.Continue {
		if result.Error != nil {
			return result.Error
		}
		return fmt.Errorf("tool call blocked: %s", name)
	}
	return nil
}

func (a *Architect) runPostToolHooks(
	ctx context.Context,
	name string,
	input json.RawMessage,
	result *skills.Result,
) {
	if a.hooks == nil {
		return
	}
	hookData := &skills.ToolCallHookData{
		ToolName: name,
		AgentID:  "architect",
		Input: map[string]any{
			"raw": string(input),
		},
	}
	if result != nil {
		hookData.Output = result.Data
		if !result.Success {
			hookData.Error = errors.New(result.Error)
		}
	}
	_, _, _ = a.hooks.ExecutePostToolCallHooks(ctx, hookData)
}

func (a *Architect) ensureSkillLoaded(name string) {
	if a.skills == nil {
		return
	}
	_ = a.skills.Load(name)
}

// ensureToolLoopSkillsLoaded loads core architect skills so they are available
// as tool definitions for the LLM tool-call loop. Remaining skills are
// demand-paged by the shared tool runtime when the LLM calls them — reducing initial
// tool definitions from ~47 (~5500 tokens) to ~14 (~1700 tokens).
func (a *Architect) ensureToolLoopSkillsLoaded() {
	if a.skills == nil {
		return
	}
	for _, name := range architectCoreSkillNames() {
		a.skills.Load(name)
	}
	if a.skillLoader != nil {
		a.skillLoader.OptimizeForBudget()
	}
	if a.tools != nil {
		a.tools.SyncActiveFromLoaded()
	}
}

func (a *Architect) prepareSkillsForRequest(req *ArchitectRequest) {
	if a == nil || req == nil || a.skillLoader == nil {
		return
	}
	ctx := skills.LoadContext{
		RecentInputs:    architectSkillInputs(req),
		ActiveDomains:   architectSkillDomains(req),
		RecentlyInvoked: a.recentlyInvokedArchitectSkills(8),
		TokenBudget:     0,
	}
	a.LoadSkillsForContext(ctx)
	a.OptimizeSkillsForBudget()
	for _, name := range architectPinnedSkills(req) {
		a.ensureSkillLoaded(name)
	}
	if a.tools != nil {
		a.tools.SyncActiveFromLoaded()
	}
}

func architectProviderToolsToDefinitions(tools []providers.Tool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.Parameters,
		})
	}
	return result
}

func architectSkillInputs(req *ArchitectRequest) []string {
	inputs := []string{}
	if req == nil {
		return inputs
	}
	if req.Query != "" {
		inputs = append(inputs, req.Query)
	}
	if req.Intent != "" {
		inputs = append(inputs, req.Intent.String())
	}
	for _, key := range sortedParamKeys(req.Params) {
		inputs = append(inputs, key)
	}
	return inputs
}

func sortedParamKeys(params map[string]any) []string {
	if len(params) == 0 {
		return nil
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func architectSkillDomains(req *ArchitectRequest) []string {
	domains := append([]string{}, architectIntentDomains(req.Intent)...)
	domains = append(domains, architectKeywordDomains(req.Query)...)
	domains = append(domains, architectParamDomains(req.Params)...)
	return uniqueDomains(domains)
}

func architectIntentDomains(intent ArchitectIntent) []string {
	switch intent {
	case IntentPlan:
		return []string{"planning", "consultation", "delegation", "coordination"}
	case IntentDesign:
		return []string{"design", "consultation"}
	case IntentGenerateTasks, IntentCreateDAG:
		return []string{"planning", "coordination"}
	case IntentRecall, IntentCheck:
		return []string{"filesystem", "git_read"}
	default:
		return []string{"planning"}
	}
}

func architectKeywordDomains(query string) []string {
	text := query
	domains := []string{}
	if containsAny(text, []string{"file", "grep", "glob", "read"}) {
		domains = append(domains, "filesystem")
	}
	if containsAny(text, []string{"git", "commit", "branch", "diff", "blame"}) {
		domains = append(domains, "git_read")
	}
	if containsAny(text, []string{"ast", "structural", "pattern"}) {
		domains = append(domains, "ast")
	}
	if containsAny(text, []string{"lsp", "definition", "references", "hover", "symbol"}) {
		domains = append(domains, "lsp")
	}
	if containsAny(text, []string{"research", "paper", "proposal", "best practice"}) {
		domains = append(domains, "research", "consultation")
	}
	if containsAny(text, []string{"fix", "correction", "interrupt", "pause", "resume"}) {
		domains = append(domains, "coordination", "planning")
	}
	return domains
}

func architectParamDomains(params map[string]any) []string {
	if len(params) == 0 {
		return nil
	}
	domains := []string{}
	if boolParam(params, "include_academic") {
		domains = append(domains, "research", "consultation")
	}
	if hasParam(params, "cross_domain") || hasParam(params, "is_multi_agent") {
		domains = append(domains, "coordination")
	}
	if hasParam(params, "paper_path") || hasParam(params, "research_slug") {
		domains = append(domains, "research")
	}
	return domains
}

func boolParam(params map[string]any, key string) bool {
	value, ok := params[key]
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	return ok && flag
}

func hasParam(params map[string]any, key string) bool {
	_, ok := params[key]
	return ok
}

func containsAny(text string, needles []string) bool {
	text = strings.ToLower(text)
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" {
			continue
		}
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func uniqueDomains(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	result := make([]string, 0, len(domains))
	seen := map[string]struct{}{}
	for _, domain := range domains {
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		result = append(result, domain)
	}
	return result
}

func architectPinnedSkills(req *ArchitectRequest) []string {
	if req == nil {
		return nil
	}
	pinned := []string{}
	if shouldPinResearchSkill(req) {
		pinned = append(pinned, "academic_research", "consult_peer")
	}
	if shouldPinFilesystemSkills(req) {
		pinned = append(pinned, "read_file", "glob", "grep")
	}
	return uniqueDomains(pinned)
}

func shouldPinResearchSkill(req *ArchitectRequest) bool {
	if req == nil {
		return false
	}
	if containsAny(req.Query, []string{"research", "paper", "proposal"}) {
		return true
	}
	return hasParam(req.Params, "paper_path") || hasParam(req.Params, "research_slug")
}

func shouldPinFilesystemSkills(req *ArchitectRequest) bool {
	if req == nil {
		return false
	}
	return containsAny(req.Query, []string{"file", "grep", "glob", "read"}) ||
		hasParam(req.Params, "file") ||
		hasParam(req.Params, "path")
}

func (a *Architect) recentlyInvokedArchitectSkills(limit int) []string {
	all := a.skills.GetAll()
	sort.Slice(all, func(i, j int) bool {
		return all[i].InvokeCount > all[j].InvokeCount
	})
	names := make([]string, 0, limit)
	for _, skill := range all {
		if skill.InvokeCount == 0 {
			continue
		}
		names = append(names, skill.Name)
		if len(names) >= limit {
			break
		}
	}
	return names
}
