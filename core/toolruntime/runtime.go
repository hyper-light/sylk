package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

const (
	SearchToolName      = "search_skills"
	defaultSearchLimit  = 5
	maxSearchResults    = 10
	maxDescriptionItems = 2
)

type Config struct {
	Registry             *skills.Registry
	Hooks                *skills.HookRegistry
	Manifest             *PolicyManifest
	State                *State
	SearchLimit          int
	GuardianControlPlane GuardianControlPlane
	// Scope is the agent's tracked-goroutine scope. When set, the
	// runtime stamps it onto every tool-invocation context via
	// concurrency.WithScope so skill handlers that want to dispatch
	// parallel work (e.g. batched workspace reads/writes) can do so
	// through the same primitive the agent uses for its own
	// goroutines — leak detection, budgeting, and lifetime enforcement
	// still apply. Handlers that don't need parallelism ignore it;
	// tests or unwired callers leave it nil and the batched paths fall
	// back to sequential execution.
	Scope *concurrency.GoroutineScope
}

type ExecutionResult struct {
	Output          string
	ToolName        string
	ToolDefsDirty   bool
	ActivatedSkills []string
}

type RawExecutionResult struct {
	Data            any
	ToolName        string
	ToolDefsDirty   bool
	ActivatedSkills []string
}

type Runtime struct {
	registry     *skills.Registry
	hooks        *skills.HookRegistry
	manifest     *PolicyManifest
	state        *State
	searchLimit  int
	worker       *SerialWorker
	controlPlane GuardianControlPlane
	scope        *concurrency.GoroutineScope
	// ownsScope is true when the runtime constructed its own scope
	// (because the caller didn't supply one). Close() shuts down an
	// owned scope; an injected scope is left alone — it belongs to
	// whoever passed it in and has its own lifecycle.
	ownsScope bool
}

// Surface is the tool-runtime execution/build surface for a single request
// context. Runtime implements the shared/session-scoped surface, while
// RequestView overlays transient request-only tool activation without mutating
// the shared runtime state.
type Surface interface {
	AgentID() string
	CapabilityScope() string
	Allows(name string) bool
	SyncActiveFromLoaded() []string
	BuildToolDefinitions() []providers.Tool
	ValidateBatch([]Invocation) error
	Execute(context.Context, Invocation) (ExecutionResult, error)
	ExecuteApproved(context.Context, Invocation, *GuardianControlGrant) (ExecutionResult, error)
}

type searchRequest struct {
	Query    string   `json:"query"`
	Limit    int      `json:"limit,omitempty"`
	Activate *bool    `json:"activate,omitempty"`
	Domains  []string `json:"domains,omitempty"`
}

type searchMatch struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Domain      string   `json:"domain,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Active      bool     `json:"active"`
	Loaded      bool     `json:"loaded"`
	Score       int      `json:"score"`
}

func New(cfg Config) (*Runtime, error) {
	searchLimit := cfg.SearchLimit
	if searchLimit <= 0 {
		searchLimit = defaultSearchLimit
	}
	if searchLimit > maxSearchResults {
		searchLimit = maxSearchResults
	}

	state := cfg.State
	if state == nil {
		state = NewState()
	}

	runtime := &Runtime{
		registry:     cfg.Registry,
		hooks:        cfg.Hooks,
		manifest:     cfg.Manifest,
		state:        state,
		searchLimit:  searchLimit,
		controlPlane: cfg.GuardianControlPlane,
	}
	runtime.scope, runtime.ownsScope = resolveToolRuntimeScope(cfg)
	// SerialWorker is now scope-tracked — no raw `go worker.run()`
	// spawns. Construction requires a scope; runtime always has one
	// (either caller-supplied or self-owned via resolveToolRuntimeScope).
	worker, workerErr := NewSerialWorker(runtime.scope, 16)
	if workerErr != nil {
		if runtime.ownsScope && runtime.scope != nil {
			_ = runtime.scope.Shutdown(time.Second, 2*time.Second)
		}
		return nil, fmt.Errorf("tool runtime: init serial worker: %w", workerErr)
	}
	runtime.worker = worker
	if err := runtime.manifest.Validate(runtime.registry, runtime.controlPlane != nil); err != nil {
		runtime.worker.Close()
		if runtime.ownsScope && runtime.scope != nil {
			_ = runtime.scope.Shutdown(time.Second, 2*time.Second)
		}
		return nil, err
	}
	runtime.state.Activate(runtime.manifest.DefaultVisibleNames()...)
	return runtime, nil
}

// resolveToolRuntimeScope returns the scope the runtime should use and
// whether it owns the scope (for Close-time cleanup). Caller-supplied
// scope takes precedence: the agent owns shutdown, we just borrow. When
// no scope is supplied, we construct one rooted at context.Background()
// with the manifest's CapabilityScope as the agent ID so per-runtime
// goroutines remain attributable in leak diagnostics even for agents
// that haven't explicitly wired a scope yet. Budget is nil (unlimited)
// unless the caller opts in by passing their own pre-budgeted scope.
func resolveToolRuntimeScope(cfg Config) (*concurrency.GoroutineScope, bool) {
	if cfg.Scope != nil {
		return cfg.Scope, false
	}
	name := "toolruntime"
	if cfg.Manifest != nil && strings.TrimSpace(cfg.Manifest.CapabilityScope) != "" {
		name = strings.TrimSpace(cfg.Manifest.CapabilityScope)
	}
	return concurrency.NewGoroutineScope(context.Background(), name, nil), true
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	if r.worker != nil {
		r.worker.Close()
	}
	if r.ownsScope && r.scope != nil {
		// Graceful → forced shutdown of any in-flight tool workers so
		// an agent's Close() path never leaks tracked goroutines. Most
		// tool workers are sub-second; 1s grace + 2s hard is generous.
		_ = r.scope.Shutdown(time.Second, 2*time.Second)
	}
}

func (r *Runtime) CapabilityScope() string {
	if r == nil || r.manifest == nil {
		return ""
	}
	return r.manifest.CapabilityScope
}

func (r *Runtime) AgentID() string {
	if r == nil || r.manifest == nil {
		return ""
	}
	return r.manifest.AgentID
}

func (r *Runtime) Allows(name string) bool {
	if r == nil || r.manifest == nil {
		return false
	}
	_, ok := r.manifest.Policy(name)
	return ok
}

func (r *Runtime) Activate(names ...string) ([]string, error) {
	if r == nil || r.manifest == nil {
		return nil, fmt.Errorf("tool runtime is not configured")
	}
	allowed, err := r.allowedToolNames(names...)
	if err != nil {
		return nil, err
	}
	return r.activateNames(allowed, nil, false), nil
}

func (r *Runtime) RequestView(names ...string) (*RequestView, error) {
	return r.newRequestView(false, names...)
}

func (r *Runtime) ScopedView(names ...string) (*RequestView, error) {
	return r.newRequestView(true, names...)
}

func (r *Runtime) newRequestView(restricted bool, names ...string) (*RequestView, error) {
	if r == nil || r.manifest == nil {
		return nil, fmt.Errorf("tool runtime is not configured")
	}
	allowed, err := r.allowedToolNames(names...)
	if err != nil {
		return nil, err
	}
	view := &RequestView{
		runtime:         r,
		transientActive: make(map[string]struct{}, len(allowed)),
		restricted:      restricted,
	}
	for _, name := range allowed {
		view.transientActive[name] = struct{}{}
	}
	return view, nil
}

func (r *Runtime) allowedToolNames(names ...string) ([]string, error) {
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := r.manifest.Policy(name); !ok {
			return nil, fmt.Errorf("SECURITY VIOLATION: agent %q is not permitted to activate tool %q", r.manifest.AgentID, name)
		}
		allowed = append(allowed, name)
	}
	return allowed, nil
}

func (r *Runtime) SyncActiveFromLoaded() []string {
	return r.syncActiveFromLoaded(nil, false)
}

func (r *Runtime) syncActiveFromLoaded(transientActive map[string]struct{}, restricted bool) []string {
	if r == nil || r.registry == nil || r.manifest == nil {
		return nil
	}
	if restricted {
		return nil
	}
	loaded := r.registry.GetLoaded()
	names := make([]string, 0, len(loaded))
	for _, skill := range loaded {
		if skill == nil {
			continue
		}
		if _, ok := r.manifest.Policy(skill.Name); !ok {
			continue
		}
		names = append(names, skill.Name)
	}
	return r.activateNames(names, transientActive, restricted)
}

func (r *Runtime) BuildToolDefinitions() []providers.Tool {
	return r.buildToolDefinitions(nil, false)
}

func (r *Runtime) buildToolDefinitions(transientActive map[string]struct{}, restricted bool) []providers.Tool {
	if r == nil {
		return nil
	}
	tools := make([]providers.Tool, 0)
	for _, name := range r.activeSnapshot(transientActive, restricted) {
		policy, ok := r.manifest.Policy(name)
		if !ok {
			continue
		}
		if name == SearchToolName {
			tools = append(tools, searchToolDefinition(policy))
			continue
		}
		skill := r.registry.Get(name)
		if skill == nil {
			continue
		}
		tools = append(tools, r.skillToProviderTool(skill, policy))
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

// ValidateBatch validates identity and scope for each invocation in the batch.
// Mixed effect domains are allowed; schedulers may use domain metadata later to
// decide ordering or parallelism, but admission should not reject them.
func (r *Runtime) ValidateBatch(invocations []Invocation) error {
	if r == nil || len(invocations) == 0 {
		return nil
	}
	for _, inv := range invocations {
		if _, _, err := r.validateInvocation(inv); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) Execute(ctx context.Context, inv Invocation) (ExecutionResult, error) {
	rawResult, err := r.ExecuteRaw(ctx, inv)
	result := ExecutionResult{
		ToolName:        rawResult.ToolName,
		ToolDefsDirty:   rawResult.ToolDefsDirty,
		ActivatedSkills: append([]string(nil), rawResult.ActivatedSkills...),
	}
	if rawResult.Data != nil {
		output, marshalErr := marshalOutput(rawResult.Data)
		if marshalErr != nil {
			if err == nil {
				return ExecutionResult{}, marshalErr
			}
		} else {
			result.Output = output
		}
	}
	return result, err
}

func (r *Runtime) ExecuteRaw(ctx context.Context, inv Invocation) (RawExecutionResult, error) {
	return r.executeRaw(ctx, inv, nil, nil, false)
}

// recordToolCallStart appends a tool_call:started artifact to the
// claims TestamentAccumulator on ctx (when one is present). Returns
// the call-tracking ID to thread into the matching completion.
//
// Tool calls are EVIDENCE of the agent's processing of its parent
// claim — they belong on the testament as artifacts. This recorder
// is the source of truth for the chat panel's child-row rendering:
// each artifact emitted here fans through the ArtifactProgressSink
// to a child-row message under the parent claim.
// toolCallTrace bundles the IDs that pair the started artifact with
// its eventual completed artifact. Returned by recordToolCallStart and
// passed verbatim to recordToolCallEnd. Empty when no accumulator is
// available on ctx (in which case the wrap is a no-op).
type toolCallTrace struct {
	startedArtifactID string
	callID            string
}

func recordToolCallStart(ctx context.Context, inv Invocation, agentID string) toolCallTrace {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return toolCallTrace{}
	}
	callID := strings.TrimSpace(inv.ToolCall.ID)
	if callID == "" {
		callID = uuid.NewString()
	}
	startedID := uuid.NewString()
	acc.RecordArtifact(&claims.Artifact{
		ID:        startedID,
		AgentID:   agentID,
		Kind:      "tool_started",
		Reference: strings.TrimSpace(inv.ToolCall.Name),
		Metadata: map[string]any{
			"call_id":         callID,
			"tool_name":       strings.TrimSpace(inv.ToolCall.Name),
			"args":            inv.ToolCall.Arguments,
			"correlation_id":  strings.TrimSpace(inv.CorrelationID),
			"capability_scope": strings.TrimSpace(inv.CapabilityScope),
			"started_at":      time.Now().UTC().Format(time.RFC3339Nano),
		},
		Ephemeral: true,
	})
	return toolCallTrace{startedArtifactID: startedID, callID: callID}
}

// recordToolCallEnd appends a tool_completed artifact paired with its
// matching tool_started via Relation{completes}. The chat panel reads
// only the relation (no fuzzy matching) to flip the in-progress row.
func recordToolCallEnd(ctx context.Context, agentID string, trace toolCallTrace, toolName string, output any, execErr error, started time.Time) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return
	}
	now := time.Now().UTC()
	outcome := "success"
	switch {
	case execErr == nil:
		outcome = "success"
	case errors.Is(execErr, context.Canceled):
		outcome = "cancelled"
	case errors.Is(execErr, context.DeadlineExceeded):
		outcome = "timeout"
	default:
		outcome = "failure"
	}
	metadata := map[string]any{
		"outcome":     outcome,
		"call_id":     trace.callID,
		"tool_name":   strings.TrimSpace(toolName),
		"started_at":  started.UTC().Format(time.RFC3339Nano),
		"ended_at":    now.Format(time.RFC3339Nano),
		"duration_ms": now.Sub(started).Milliseconds(),
	}
	if execErr != nil {
		metadata["error"] = execErr.Error()
	}
	if output != nil {
		// Render output to a compact summary for the artifact reference.
		// Full output is in the testament's artifact metadata for audit;
		// the reference is what the chat row renders.
		switch v := output.(type) {
		case string:
			metadata["output"] = truncateForRef(v, 4096)
		default:
			if blob, err := json.Marshal(v); err == nil {
				metadata["output"] = truncateForRef(string(blob), 4096)
			}
		}
	}
	artifact := &claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   agentID,
		Kind:      "tool_completed",
		Reference: strings.TrimSpace(toolName),
		Metadata:  metadata,
		Ephemeral: true,
	}
	if trace.startedArtifactID != "" {
		artifact.Relations = []claims.Relation{
			{
				Related:      trace.startedArtifactID,
				RelatedType:  claims.RelatedTypeArtifact,
				Relationship: claims.RelationshipCompletes,
			},
		}
	}
	acc.RecordArtifact(artifact)
}

// truncateForRef caps a reference string at max bytes with an ellipsis
// suffix. Used to keep the in-progress artifact reference small —
// the full output remains available on the testament for audit.
func truncateForRef(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func (r *Runtime) executeRaw(
	ctx context.Context,
	inv Invocation,
	approvedGrant *GuardianControlGrant,
	transientActive map[string]struct{},
	restricted bool,
) (rawResult RawExecutionResult, retErr error) {
	if r == nil {
		return RawExecutionResult{}, fmt.Errorf("tool runtime is not configured")
	}
	// Stamp the runtime's GoroutineScope onto ctx so skill handlers
	// that want tracked parallelism (batched workspace ops, concurrent
	// peer lookups, etc.) can reach for the scope via
	// concurrency.ScopeFromContext without having to receive it through
	// every skill-config boundary. Every Runtime has a scope: either
	// the one the caller passed in Config.Scope (shared agent scope),
	// or one the runtime constructed for itself (auto-created).
	if r.scope != nil {
		ctx = concurrency.WithScope(ctx, r.scope)
	}
	// Tool-call evidence: record start now, end after the invocation
	// returns (success or failure). The artifacts flow through the
	// claims TestamentAccumulator on ctx into the chat panel as child
	// rows under the parent claim. The deferred end-recorder reads the
	// resolved tool name (which a pre-hook may have rewritten) so the
	// completion artifact pairs correctly with the start.
	toolCallStart := time.Now().UTC()
	resolvedToolName := strings.TrimSpace(inv.ToolCall.Name)
	toolCallTrace := recordToolCallStart(ctx, inv, inv.AgentID)
	defer func() {
		var output any
		if rawResult.Data != nil {
			output = rawResult.Data
		}
		recordToolCallEnd(ctx, inv.AgentID, toolCallTrace, resolvedToolName, output, retErr, toolCallStart)
	}()
	name, policy, err := r.validateInvocation(inv)
	if err != nil {
		return RawExecutionResult{}, err
	}
	resolvedToolName = name
	if !r.isActive(name, transientActive, restricted) {
		return RawExecutionResult{}, fmt.Errorf("tool %q is outside the active tool set for capability scope %q", name, r.manifest.CapabilityScope)
	}

	inputMap, raw, err := decodeInputObject(inv.ToolCall.Arguments)
	if err != nil {
		return RawExecutionResult{}, fmt.Errorf("tool arguments for %q: %w", name, err)
	}

	toolData := &skills.ToolCallHookData{
		ToolName: name,
		ToolID:   inv.ToolCall.ID,
		AgentID:  inv.AgentID,
		Input:    cloneMap(inputMap),
		Metadata: map[string]any{
			"raw_json":         raw,
			"correlation_id":   inv.CorrelationID,
			"capability_scope": inv.CapabilityScope,
		},
	}

	if r.hooks != nil {
		updated, hookResult, hookErr := r.hooks.ExecutePreToolCallHooks(ctx, toolData)
		if hookErr != nil {
			return RawExecutionResult{}, hookErr
		}
		if updated != nil {
			toolData = updated
		}
		if toolData.ToolName == "" {
			toolData.ToolName = name
		}
		if toolData.ToolID == "" {
			toolData.ToolID = inv.ToolCall.ID
		}
		if toolData.AgentID == "" {
			toolData.AgentID = inv.AgentID
		}
		if toolData.Metadata == nil {
			toolData.Metadata = map[string]any{}
		}
		toolData.Metadata["correlation_id"] = inv.CorrelationID
		toolData.Metadata["capability_scope"] = inv.CapabilityScope
		toolData.Metadata["raw_json"] = raw
		if hookResult.SkipExecution {
			output := strings.TrimSpace(hookResult.SkipResponse)
			if postErr := r.runPostHooks(ctx, toolData, output, nil); postErr != nil {
				return RawExecutionResult{}, postErr
			}
			return RawExecutionResult{Data: output, ToolName: toolData.ToolName}, nil
		}
		if !hookResult.Continue {
			if hookResult.Error != nil {
				return RawExecutionResult{}, hookResult.Error
			}
			return RawExecutionResult{}, fmt.Errorf("tool call blocked: %s", toolData.ToolName)
		}

		name = strings.TrimSpace(toolData.ToolName)
		nextPolicy, ok := r.manifest.Policy(name)
		if !ok {
			return RawExecutionResult{}, fmt.Errorf("SECURITY VIOLATION: agent %q is not permitted to execute tool %q", inv.AgentID, name)
		}
		policy = nextPolicy
		if !r.isActive(name, transientActive, restricted) {
			return RawExecutionResult{}, fmt.Errorf("tool %q is outside the active tool set for capability scope %q", name, r.manifest.CapabilityScope)
		}
		// A pre-hook may have rewritten the tool name. Update the
		// resolved name so the deferred end-recorder pairs the
		// completion artifact under the actual tool that ran.
		resolvedToolName = name
	}

	if name == SearchToolName {
		payload, activated, execErr := r.executeSearch(toolData.Input, transientActive, restricted)
		if postErr := r.runPostHooks(ctx, toolData, payload, execErr); postErr != nil {
			return RawExecutionResult{}, postErr
		}
		if execErr != nil {
			return RawExecutionResult{}, execErr
		}
		return RawExecutionResult{
			Data:            payload,
			ToolName:        name,
			ToolDefsDirty:   len(activated) > 0,
			ActivatedSkills: activated,
		}, nil
	}

	if policy.ApprovalSensitive || policy.Execution == ExecutionModeGuardian {
		if approvedGrant != nil {
			req := guardianControlRequest(inv, policy, raw, toolData.Input, r.manifest)
			if err := approvedGrant.Validate(req); err != nil {
				if postErr := r.runPostHooks(ctx, toolData, nil, err); postErr != nil {
					return RawExecutionResult{}, postErr
				}
				return RawExecutionResult{}, err
			}
		} else if err := r.obtainGuardianGrant(ctx, inv, policy, raw, toolData.Input); err != nil {
			payload, _ := skills.DelegatedPayload(err)
			if postErr := r.runPostHooks(ctx, toolData, payload, err); postErr != nil {
				return RawExecutionResult{}, postErr
			}
			return RawExecutionResult{Data: payload, ToolName: name}, err
		}
	}

	skill := r.resolveSkill(name)
	// Consumes check — refuse to invoke when a required artifact is
	// missing. Surfacing here (rather than in a downstream terminal
	// validator) means the LLM sees the violation on the call it just
	// made, with a RecoveryAction naming the producer skill.
	if err := skills.ValidateContractConsumes(ctx, skill); err != nil {
		if postErr := r.runPostHooks(ctx, toolData, nil, err); postErr != nil {
			return RawExecutionResult{}, postErr
		}
		return RawExecutionResult{}, err
	}

	result, err := r.executePolicy(ctx, name, toolData.Input, policy)
	if postErr := r.runPostHooks(ctx, toolData, resultData(result), resultError(result)); postErr != nil {
		return RawExecutionResult{}, postErr
	}
	if err != nil {
		return RawExecutionResult{}, err
	}
	if result == nil {
		return RawExecutionResult{}, fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return RawExecutionResult{}, resultToError(name, result)
	}
	// Produces check — the handler succeeded; verify each declared
	// artifact is now visible in the protocol state. The presence
	// checker reads the live projection so its answer reflects any
	// mutation the handler made.
	if err := skills.ValidateContractProduces(ctx, skill); err != nil {
		return RawExecutionResult{}, err
	}
	return RawExecutionResult{
		Data:     result.Data,
		ToolName: name,
	}, nil
}

// resolveSkill returns the registered skill by name, or nil when the
// registry is unavailable or the skill is not registered. Used for
// contract enforcement — a nil skill means no Contract to enforce.
func (r *Runtime) resolveSkill(name string) *skills.Skill {
	if r == nil || r.registry == nil {
		return nil
	}
	return r.registry.Get(name)
}

func (r *Runtime) validateInvocation(inv Invocation) (string, ToolPolicy, error) {
	if r.manifest == nil {
		return "", ToolPolicy{}, fmt.Errorf("tool policy manifest is not configured")
	}
	name := strings.TrimSpace(inv.ToolCall.Name)
	if name == "" {
		return "", ToolPolicy{}, fmt.Errorf("tool name is required")
	}
	if strings.TrimSpace(inv.AgentID) == "" {
		return "", ToolPolicy{}, fmt.Errorf("agent_id is required for tool invocation")
	}
	if strings.TrimSpace(inv.CorrelationID) == "" {
		return "", ToolPolicy{}, fmt.Errorf("correlation_id is required for tool invocation")
	}
	if strings.TrimSpace(inv.CapabilityScope) == "" {
		return "", ToolPolicy{}, fmt.Errorf("capability_scope is required for tool invocation")
	}
	if inv.AgentID != r.manifest.AgentID {
		return "", ToolPolicy{}, fmt.Errorf("tool invocation agent mismatch: %q != %q", inv.AgentID, r.manifest.AgentID)
	}
	if inv.CapabilityScope != r.manifest.CapabilityScope {
		return "", ToolPolicy{}, fmt.Errorf("tool invocation capability scope mismatch: %q != %q", inv.CapabilityScope, r.manifest.CapabilityScope)
	}
	policy, ok := r.manifest.Policy(name)
	if !ok {
		return "", ToolPolicy{}, fmt.Errorf("SECURITY VIOLATION: agent %q is not permitted to execute tool %q", inv.AgentID, name)
	}
	return name, policy, nil
}

func (r *Runtime) executePolicy(ctx context.Context, name string, input map[string]any, policy ToolPolicy) (*skills.Result, error) {
	run := func() (any, error) {
		return r.invokeLocal(ctx, name, input)
	}
	switch policy.Execution {
	case ExecutionModeLocal:
		value, err := runWorkerFn(name, run)
		if err != nil {
			return nil, err
		}
		return coerceSkillsResult(name, policy.Execution, value)
	case ExecutionModeLocalWorker, ExecutionModeGuardian:
		value, err := r.worker.Do(ctx, name, run)
		if err != nil {
			return nil, err
		}
		return coerceSkillsResult(name, policy.Execution, value)
	default:
		return nil, fmt.Errorf("unsupported execution mode %q for tool %q", policy.Execution, name)
	}
}

func coerceSkillsResult(toolName string, mode ExecutionMode, value any) (*skills.Result, error) {
	result, ok := value.(*skills.Result)
	if !ok {
		return nil, fmt.Errorf("tool %q returned invalid runtime result type %T in %q mode", toolName, value, mode)
	}
	if result == nil {
		return nil, fmt.Errorf("tool %q returned nil runtime result in %q mode", toolName, mode)
	}
	return result, nil
}

func (r *Runtime) invokeLocal(ctx context.Context, name string, input map[string]any) (*skills.Result, error) {
	if r.registry == nil {
		return nil, fmt.Errorf("tool registry is not available")
	}
	skill := r.registry.Get(name)
	if skill == nil {
		return nil, fmt.Errorf("tool %q is not registered", name)
	}
	if skill.ProviderTool != nil && skill.ProviderTool.ResolvedKind() != skills.ProviderToolKindFunction {
		return nil, fmt.Errorf("tool %q is provider-native and cannot be executed locally", name)
	}
	if !skill.Loaded {
		_ = r.registry.Load(name)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal tool input for %q: %w", name, err)
	}
	return r.registry.Invoke(ctx, name, payload), nil
}

func guardianControlRequest(
	inv Invocation,
	policy ToolPolicy,
	raw string,
	input map[string]any,
	manifest *PolicyManifest,
) GuardianControlRequest {
	fingerprint := ""
	if manifest != nil {
		fingerprint = manifest.Fingerprint()
	}
	return GuardianControlRequest{
		AgentID:           inv.AgentID,
		CorrelationID:     inv.CorrelationID,
		CapabilityScope:   inv.CapabilityScope,
		ToolName:          inv.ToolCall.Name,
		ToolID:            inv.ToolCall.ID,
		Arguments:         raw,
		ArgumentsHash:     HashArguments(raw),
		Input:             cloneMap(input),
		Policy:            policy,
		PolicyFingerprint: fingerprint,
		Timestamp:         time.Now(),
	}
}

func (r *Runtime) obtainGuardianGrant(
	ctx context.Context,
	inv Invocation,
	policy ToolPolicy,
	raw string,
	input map[string]any,
) (retErr error) {
	if r.manifest != nil && r.manifest.AgentID == "guardian" {
		return nil
	}
	if r.controlPlane == nil {
		return fmt.Errorf("tool %q requires guardian-controlled execution but no guardian control plane is configured", inv.ToolCall.Name)
	}
	req := guardianControlRequest(inv, policy, raw, input, r.manifest)
	// Per docs/CLAIMS_UI.md §5.3 step 1: post a guardian-check claim
	// with subject=guardian and capture its ID. The bridge nests
	// guardian's later artifacts (its own tool calls during
	// assessment) under this claim so the chat tree shows the
	// guardian's decision-making INSIDE the calling tool's row,
	// matching the spec's hierarchy. The claim ID is also stamped
	// onto the AwaitingGuardian agent_state artifact metadata so
	// the bridge's resolver can attribute the wait correctly.
	guardianClaimID := postGuardianCheckClaim(ctx, inv)
	// Centralized guardian-check artifact pair (UI_DESIGN.md §4.1):
	// every approval-gated tool emits a started artifact onto the
	// caller's accumulator the moment the gate is invoked, and a
	// matching completed artifact via Relation{completes} the moment
	// the grant returns (any outcome). The chat panel renders these as
	// a nested guardian row under the parent tool call.
	guardianTrace := recordGuardianCheckStart(ctx, inv, guardianClaimID)
	started := time.Now().UTC()
	defer func() {
		recordGuardianCheckEnd(ctx, inv, guardianTrace, started, retErr)
	}()
	// Push: narrate the wait on the caller's parent claim and record an
	// agent_state artifact. The chat panel surfaces "Awaiting guardian
	// grant" on the agent's row; the artifact stays on the testament for
	// audit / replay. Best-effort — no-op when no accumulator/board/
	// claim is on context. See docs/CLAIMS_UI.md "AwaitingGuardian".
	recordGuardianWaitContext(ctx, inv, guardianClaimID)
	grant, err := r.controlPlane.RequestGrant(ctx, req)
	if err != nil {
		return err
	}
	return grant.Validate(req)
}

// postGuardianCheckClaim posts a structured guardian-check claim with
// subject=guardian, returning the claim ID. The claim represents
// "agent <X> has invoked tool <T> and awaits guardian's decision";
// guardian-side processing produces a testament that satisfies the
// claim's Inspection validation. Best-effort — returns "" when no
// session board / accumulator is reachable. See docs/CLAIMS_UI.md
// §5.3.
func postGuardianCheckClaim(ctx context.Context, inv Invocation) string {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return ""
	}
	board := acc.Board()
	if board == nil {
		return ""
	}
	callerAgentID := strings.TrimSpace(inv.AgentID)
	if callerAgentID == "" {
		callerAgentID = acc.AgentID()
	}
	toolName := strings.TrimSpace(inv.ToolCall.Name)
	relations := []claims.Relation{
		{Related: callerAgentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		{Related: "guardian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
	}
	if parent := strings.TrimSpace(acc.ClaimID()); parent != "" {
		relations = append(relations, claims.Relation{
			Related: parent, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy,
		})
	}
	claim := claims.Claim{
		Title:       "Guardian review: " + toolName,
		Description: "Approval gate for " + toolName + " requested by " + callerAgentID,
		Scope:       []claims.ClaimScopeEntry{{Kind: "tool", Key: toolName}},
		ActionType:  claims.ActionTypeGuardianCheck,
		Relations:   relations,
		Validations: []*claims.Validation{{
			Type:        claims.ValidationTypeInspection,
			Required:    true,
			Description: "Guardian renders a grant verdict (allow / deny)",
			QualityBar:  "verdict.received",
			Status:      claims.ValidationStatusPending,
		}},
	}
	posted := []claims.Claim{claim}
	action := claims.Action{AgentID: callerAgentID, Type: claims.ActionTypeGuardianCheck}
	if err := board.PostAction(ctx, action, posted); err != nil {
		return ""
	}
	if len(posted) > 0 {
		return strings.TrimSpace(posted[0].ID)
	}
	return ""
}

// recordGuardianWaitContext narrates the AwaitingGuardian transition
// on the caller's claim and appends an agent_state artifact onto the
// in-flight accumulator. Mirrors agents/shared.RecordAgentState but
// runs inline because core/toolruntime cannot import agents/shared
// without creating an import cycle. Best-effort everywhere.
//
// guardianClaimID identifies the structured guardian-check claim
// posted just prior; it travels on the artifact metadata so the
// bridge can attribute the wait to the right claim hierarchy.
func recordGuardianWaitContext(ctx context.Context, inv Invocation, guardianClaimID string) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return
	}
	detail := "Awaiting guardian grant for " + strings.TrimSpace(inv.ToolCall.Name)
	if board := acc.Board(); board != nil {
		_ = board.SetClaimContext(ctx, acc.ClaimID(), detail)
	}
	metadata := map[string]any{
		"state":     "awaiting_guardian",
		"tool_name": strings.TrimSpace(inv.ToolCall.Name),
		"claim_id":  acc.ClaimID(),
		"at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if guardianClaimID != "" {
		metadata["guardian_claim_id"] = guardianClaimID
	}
	acc.RecordArtifact(&claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   acc.AgentID(),
		SessionID: acc.SessionID(),
		Kind:      claims.ArtifactKindAgentState,
		Reference: detail,
		Metadata:  metadata,
		Ephemeral: false,
	})
}

// guardianCheckTrace bundles the IDs that pair the started guardian
// artifact with its eventual completed artifact.
type guardianCheckTrace struct {
	startedArtifactID string
}

func recordGuardianCheckStart(ctx context.Context, inv Invocation, guardianClaimID string) guardianCheckTrace {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return guardianCheckTrace{}
	}
	startedID := uuid.NewString()
	metadata := map[string]any{
		"tool_name":      strings.TrimSpace(inv.ToolCall.Name),
		"tool_call_id":   strings.TrimSpace(inv.ToolCall.ID),
		"correlation_id": strings.TrimSpace(inv.CorrelationID),
		"started_at":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if guardianClaimID != "" {
		// Stamp the structured guardian-check claim ID so the bridge's
		// claimToInvocationArtifact index nests guardian's processing
		// artifacts under this artifact row when guardian's testament
		// flushes. See docs/CLAIMS_UI.md §5.3.
		metadata["claim_id"] = guardianClaimID
	}
	acc.RecordArtifact(&claims.Artifact{
		ID:        startedID,
		AgentID:   inv.AgentID,
		Kind:      "guardian_check_started",
		Reference: strings.TrimSpace(inv.ToolCall.Name),
		Metadata:  metadata,
		Ephemeral: true,
	})
	return guardianCheckTrace{startedArtifactID: startedID}
}

func recordGuardianCheckEnd(ctx context.Context, inv Invocation, trace guardianCheckTrace, started time.Time, grantErr error) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return
	}
	now := time.Now().UTC()
	outcome := "success"
	switch {
	case grantErr == nil:
		outcome = "success"
	case errors.Is(grantErr, context.Canceled):
		outcome = "cancelled"
	case errors.Is(grantErr, context.DeadlineExceeded):
		outcome = "timeout"
	default:
		outcome = "failure"
	}
	metadata := map[string]any{
		"outcome":     outcome,
		"tool_name":   strings.TrimSpace(inv.ToolCall.Name),
		"tool_call_id": strings.TrimSpace(inv.ToolCall.ID),
		"started_at":  started.UTC().Format(time.RFC3339Nano),
		"ended_at":    now.Format(time.RFC3339Nano),
		"duration_ms": now.Sub(started).Milliseconds(),
	}
	if grantErr != nil {
		metadata["error"] = grantErr.Error()
	}
	artifact := &claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   inv.AgentID,
		Kind:      "guardian_check_completed",
		Reference: strings.TrimSpace(inv.ToolCall.Name),
		Metadata:  metadata,
		Ephemeral: true,
	}
	if trace.startedArtifactID != "" {
		artifact.Relations = []claims.Relation{
			{
				Related:      trace.startedArtifactID,
				RelatedType:  claims.RelatedTypeArtifact,
				Relationship: claims.RelationshipCompletes,
			},
		}
	}
	acc.RecordArtifact(artifact)
}

func (r *Runtime) executeSearch(input map[string]any, transientActive map[string]struct{}, restricted bool) (map[string]any, []string, error) {
	var req searchRequest
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal search request: %w", err)
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, nil, fmt.Errorf("invalid search request: %w", err)
		}
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, nil, fmt.Errorf("query is required")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = r.searchLimit
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	activate := true
	if req.Activate != nil {
		activate = *req.Activate
	}
	domainFilter := make(map[string]struct{}, len(req.Domains))
	for _, domain := range req.Domains {
		domain = strings.TrimSpace(strings.ToLower(domain))
		if domain == "" {
			continue
		}
		domainFilter[domain] = struct{}{}
	}

	type rankedSkill struct {
		skill  *skills.Skill
		policy ToolPolicy
		score  int
	}
	ranked := make([]rankedSkill, 0)
	queryTerms := tokenize(query)
	for _, skill := range r.registry.GetAll() {
		if skill == nil {
			continue
		}
		policy, ok := r.manifest.Policy(skill.Name)
		if !ok || !policy.Searchable {
			continue
		}
		if len(domainFilter) > 0 {
			if _, ok := domainFilter[strings.ToLower(strings.TrimSpace(string(policy.Domain)))]; !ok {
				continue
			}
		}
		score := scoreSkill(query, queryTerms, skill)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, rankedSkill{skill: skill, policy: policy, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].skill.Name < ranked[j].skill.Name
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	matches := make([]searchMatch, 0, len(ranked))
	activateNames := make([]string, 0, len(ranked))
	for _, entry := range ranked {
		matches = append(matches, searchMatch{
			Name:        entry.skill.Name,
			Description: compileToolDescription(entry.skill, entry.policy),
			Domain:      string(entry.policy.Domain),
			Keywords:    append([]string(nil), entry.skill.Keywords...),
			Active:      r.isActive(entry.skill.Name, transientActive, restricted),
			Loaded:      entry.skill.Loaded,
			Score:       entry.score,
		})
		if activate {
			activateNames = append(activateNames, entry.skill.Name)
		}
	}
	activated := r.activateNames(activateNames, transientActive, restricted)
	payload := map[string]any{
		"query":              query,
		"matches":            matches,
		"activated_skills":   activated,
		"result_count":       len(matches),
		"active_skill_count": len(r.activeSnapshot(transientActive, restricted)),
	}
	return payload, activated, nil
}

func (r *Runtime) isActive(name string, transientActive map[string]struct{}, restricted bool) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	if transientActive != nil {
		if _, ok := transientActive[trimmed]; ok {
			return true
		}
	}
	if restricted {
		return false
	}
	return r.state.IsActive(trimmed)
}

func (r *Runtime) activateNames(names []string, transientActive map[string]struct{}, restricted bool) []string {
	if len(names) == 0 {
		return nil
	}
	if transientActive == nil {
		return r.state.Activate(names...)
	}

	activated := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || r.isActive(trimmed, transientActive, restricted) {
			continue
		}
		transientActive[trimmed] = struct{}{}
		activated = append(activated, trimmed)
	}
	return activated
}

func (r *Runtime) activeSnapshot(transientActive map[string]struct{}, restricted bool) []string {
	if restricted {
		names := make([]string, 0, len(transientActive))
		for name := range transientActive {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			names = append(names, trimmed)
		}
		sort.Strings(names)
		return names
	}
	base := r.state.Snapshot()
	if len(transientActive) == 0 {
		return base
	}
	union := make(map[string]struct{}, len(base)+len(transientActive))
	for _, name := range base {
		union[name] = struct{}{}
	}
	for name := range transientActive {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		union[trimmed] = struct{}{}
	}
	names := make([]string, 0, len(union))
	for name := range union {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Runtime) runPostHooks(ctx context.Context, toolData *skills.ToolCallHookData, output any, err error) error {
	if r.hooks == nil || toolData == nil {
		return nil
	}
	postData := &skills.ToolCallHookData{
		ToolName: toolData.ToolName,
		ToolID:   toolData.ToolID,
		AgentID:  toolData.AgentID,
		Input:    cloneMap(toolData.Input),
		Output:   output,
		Error:    err,
		Metadata: cloneMap(toolData.Metadata),
	}
	_, _, hookErr := r.hooks.ExecutePostToolCallHooks(ctx, postData)
	return hookErr
}

func (r *Runtime) skillToProviderTool(skill *skills.Skill, policy ToolPolicy) providers.Tool {
	if skill != nil && skill.ProviderTool != nil {
		return skillProviderToolToProviderTool(skill, policy)
	}
	parameters := inputSchemaToMap(skill)
	if len(parameters) == 0 {
		parameters = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return providers.Tool{
		Kind:        providers.ToolKindFunction,
		Name:        strings.TrimSpace(skill.Name),
		Description: compileToolDescription(skill, policy),
		Parameters:  parameters,
	}
}

func skillProviderToolToProviderTool(skill *skills.Skill, policy ToolPolicy) providers.Tool {
	if skill == nil || skill.ProviderTool == nil {
		return providers.Tool{}
	}
	tool := providers.Tool{
		Name: strings.TrimSpace(skill.Name),
	}
	if description := strings.TrimSpace(skill.ProviderTool.Description); description != "" {
		tool.Description = description
	} else {
		tool.Description = compileToolDescription(skill, policy)
	}
	switch skill.ProviderTool.ResolvedKind() {
	case skills.ProviderToolKindNativeWebSearch:
		tool.Kind = providers.ToolKindNativeWebSearch
		if skill.ProviderTool.WebSearch != nil {
			tool.WebSearch = &providers.WebSearchOptions{
				SearchContextSize: providers.WebSearchContextSize(skill.ProviderTool.WebSearch.SearchContextSize),
				AllowedDomains:    append([]string(nil), skill.ProviderTool.WebSearch.AllowedDomains...),
				BlockedDomains:    append([]string(nil), skill.ProviderTool.WebSearch.BlockedDomains...),
				MaxUses:           skill.ProviderTool.WebSearch.MaxUses,
				Strict:            skill.ProviderTool.WebSearch.Strict,
				DeferLoading:      skill.ProviderTool.WebSearch.DeferLoading,
				EnableURLContext:  skill.ProviderTool.WebSearch.EnableURLContext,
			}
			if skill.ProviderTool.WebSearch.UserLocation != nil {
				tool.WebSearch.UserLocation = &providers.WebSearchUserLocation{
					City:     skill.ProviderTool.WebSearch.UserLocation.City,
					Country:  skill.ProviderTool.WebSearch.UserLocation.Country,
					Region:   skill.ProviderTool.WebSearch.UserLocation.Region,
					Timezone: skill.ProviderTool.WebSearch.UserLocation.Timezone,
				}
			}
		}
	default:
		tool.Kind = providers.ToolKindFunction
		tool.Parameters = inputSchemaToMap(skill)
	}
	return tool
}

func searchToolDefinition(policy ToolPolicy) providers.Tool {
	return providers.Tool{
		Name: SearchToolName,
		Description: strings.Join([]string{
			"Search the current agent's scoped capability manifest.",
			"Use this when the right tool is not active yet and you need to discover or activate additional tools for later turns.",
			"Effect: " + string(policy.Effect) + ".",
			"Domain: " + string(policy.Domain) + ".",
		}, " "),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural-language description of the capability you need.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of matches to return.",
					"default":     defaultSearchLimit,
				},
				"activate": map[string]any{
					"type":        "boolean",
					"description": "When true, matching tools are activated into the current active tool set for subsequent turns.",
					"default":     true,
				},
				"domains": map[string]any{
					"type":        "array",
					"description": "Optional effect-domain filters.",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
			"required": []string{"query"},
		},
	}
}

func decodeInputObject(arguments string) (map[string]any, string, error) {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return nil, raw, fmt.Errorf("must be valid JSON")
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, raw, fmt.Errorf("invalid JSON: %w", err)
	}
	if decoded == nil {
		return map[string]any{}, raw, nil
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, raw, fmt.Errorf("must be a JSON object")
	}
	return object, raw, nil
}

func inputSchemaToMap(skill *skills.Skill) map[string]any {
	if skill == nil {
		return nil
	}
	schema := skill.InputSchema
	if schema == nil {
		schema = &skills.InputSchema{
			Type:       "object",
			Properties: map[string]*skills.Property{},
		}
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*skills.Property{}
	}
	payload, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil
	}
	return result
}

func compileToolDescription(skill *skills.Skill, policy ToolPolicy) string {
	if skill == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	if base := strings.TrimSpace(skill.Description); base != "" {
		parts = append(parts, base)
	}
	parts = append(parts, "Effect: "+string(policy.Effect)+".")
	parts = append(parts, "Domain: "+string(policy.Domain)+".")
	parts = append(parts, "Execution: "+string(policy.Execution)+".")
	if usage := compactSentence(skill.UsageDoc); usage != "" {
		parts = append(parts, "Use when: "+usage)
	}
	if len(skill.BestPractices) > 0 {
		parts = append(parts, "Best practices: "+joinExamples(skill.BestPractices))
	}
	if len(skill.Requirements) > 0 {
		parts = append(parts, "Requirements: "+joinExamples(skill.Requirements))
	}
	if len(skill.Satisfies) > 0 {
		parts = append(parts, "Satisfies: "+joinExamples(skill.Satisfies))
	}
	if len(skill.Avoids) > 0 {
		parts = append(parts, "Avoid: "+joinExamples(skill.Avoids))
	}
	if len(skill.Examples) > 0 {
		parts = append(parts, "Examples: "+joinExamples(skill.Examples))
	}
	if len(skill.Keywords) > 0 {
		parts = append(parts, "Keywords: "+strings.Join(uniqueSortedStrings(skill.Keywords), ", ")+".")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func compactSentence(text string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return ""
	}
	if len(text) > 220 {
		text = strings.TrimSpace(text[:220]) + "..."
	}
	if strings.HasSuffix(text, ".") {
		return text
	}
	return text + "."
}

func joinExamples(items []string) string {
	trimmed := make([]string, 0, maxDescriptionItems)
	for _, item := range items {
		item = compactSentence(item)
		if item == "" {
			continue
		}
		trimmed = append(trimmed, item)
		if len(trimmed) >= maxDescriptionItems {
			break
		}
	}
	return strings.Join(trimmed, " ")
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	replacer := strings.NewReplacer("_", " ", "-", " ", "/", " ", ".", " ", ",", " ", ":", " ")
	text = replacer.Replace(text)
	return uniqueSortedStrings(strings.Fields(text))
}

func scoreSkill(query string, queryTerms []string, skill *skills.Skill) int {
	if skill == nil {
		return 0
	}
	query = strings.ToLower(strings.TrimSpace(query))
	name := strings.ToLower(skill.Name)
	description := strings.ToLower(skill.Description)
	domain := strings.ToLower(skill.Domain)
	usage := strings.ToLower(skill.UsageDoc)
	keywords := strings.ToLower(strings.Join(skill.Keywords, " "))
	examples := strings.ToLower(strings.Join(skill.Examples, " "))
	practices := strings.ToLower(strings.Join(skill.BestPractices, " "))
	properties := strings.ToLower(strings.Join(schemaPropertyTerms(skill.InputSchema), " "))

	score := 0
	if query != "" {
		if strings.Contains(name, query) {
			score += 60
		}
		if strings.Contains(description, query) {
			score += 30
		}
		if strings.Contains(usage, query) {
			score += 24
		}
	}
	for _, term := range queryTerms {
		switch {
		case term == "":
			continue
		case strings.Contains(name, term):
			score += 14
		}
		if strings.Contains(keywords, term) {
			score += 12
		}
		if strings.Contains(domain, term) {
			score += 10
		}
		if strings.Contains(description, term) {
			score += 8
		}
		if strings.Contains(usage, term) {
			score += 7
		}
		if strings.Contains(properties, term) {
			score += 6
		}
		if strings.Contains(examples, term) {
			score += 4
		}
		if strings.Contains(practices, term) {
			score += 4
		}
	}
	if skill.Loaded {
		score += 2
	}
	return score
}

func schemaPropertyTerms(schema *skills.InputSchema) []string {
	if schema == nil {
		return nil
	}
	terms := make([]string, 0, len(schema.Properties)*2)
	for name, prop := range schema.Properties {
		if name != "" {
			terms = append(terms, name)
		}
		if prop != nil && prop.Description != "" {
			terms = append(terms, prop.Description)
		}
	}
	return terms
}

func marshalOutput(data any) (string, error) {
	switch typed := data.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		payload, err := json.Marshal(data)
		if err != nil {
			return "", fmt.Errorf("marshal tool output: %w", err)
		}
		return string(payload), nil
	}
}

func resultToError(name string, result *skills.Result) error {
	if result == nil {
		return fmt.Errorf("tool %q returned nil", name)
	}
	if result.Success {
		return nil
	}
	if result.Err != nil {
		return fmt.Errorf("tool %q failed: %w", name, result.Err)
	}
	if msg := strings.TrimSpace(result.Error); msg != "" {
		return fmt.Errorf("tool %q failed: %s", name, msg)
	}
	return fmt.Errorf("tool %q failed", name)
}

func resultData(result *skills.Result) any {
	if result == nil {
		return nil
	}
	return result.Data
}

func resultError(result *skills.Result) error {
	if result == nil {
		return fmt.Errorf("tool returned nil result")
	}
	if result.Success {
		return nil
	}
	if result.Err != nil {
		return result.Err
	}
	if msg := strings.TrimSpace(result.Error); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("tool execution failed")
}

func uniqueSortedStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		set[item] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for item := range set {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
