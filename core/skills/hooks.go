package skills

import (
	"context"
	"sync"
)

type HookPhase string

const (
	HookPhasePrePrompt    HookPhase = "pre_prompt"
	HookPhasePostPrompt   HookPhase = "post_prompt"
	HookPhasePreToolCall  HookPhase = "pre_tool_call"
	HookPhasePostToolCall HookPhase = "post_tool_call"
	HookPhasePreStore     HookPhase = "pre_store"
	HookPhasePostStore    HookPhase = "post_store"
	HookPhasePreQuery     HookPhase = "pre_query"
	HookPhasePostQuery    HookPhase = "post_query"
)

type HookPriority int

const (
	HookPriorityLow    HookPriority = 10
	HookPriorityNormal HookPriority = 50
	HookPriorityHigh   HookPriority = 100
)

type PromptHookData struct {
	AgentID        string
	ConversationID string
	TurnNumber     int

	SystemPrompt string
	UserMessage  string
	Messages     []MessageContent

	Response   string
	TokensUsed int
	StopReason string

	Metadata map[string]any
}

type MessageContent struct {
	Role    string
	Content string
}

type ToolCallHookData struct {
	ToolName string
	ToolID   string

	AgentID        string
	ConversationID string

	Input map[string]any

	Output any
	Error  error

	Metadata map[string]any
}

type StoreHookData struct {
	Entry  any
	Result any
	Error  error

	Metadata map[string]any
}

type QueryHookData struct {
	Query  any
	Result any
	Error  error

	Metadata map[string]any
}

type HookResult struct {
	Continue bool

	ModifiedPromptData   *PromptHookData
	ModifiedToolCallData *ToolCallHookData
	ModifiedStoreData    *StoreHookData
	ModifiedQueryData    *QueryHookData

	Error error

	InjectContext string

	SkipExecution bool
	SkipResponse  string
}

type PromptHook interface {
	Name() string
	Phase() HookPhase
	Priority() HookPriority
	Execute(ctx context.Context, data *PromptHookData) HookResult
}

type ToolCallHook interface {
	Name() string
	Phase() HookPhase
	Priority() HookPriority
	Execute(ctx context.Context, data *ToolCallHookData) HookResult
}

type StoreHook interface {
	Name() string
	Phase() HookPhase
	Priority() HookPriority
	Execute(ctx context.Context, data *StoreHookData) HookResult
}

type QueryHook interface {
	Name() string
	Phase() HookPhase
	Priority() HookPriority
	Execute(ctx context.Context, data *QueryHookData) HookResult
}

type PromptHookFunc func(ctx context.Context, data *PromptHookData) HookResult

type ToolCallHookFunc func(ctx context.Context, data *ToolCallHookData) HookResult

type StoreHookFunc func(ctx context.Context, data *StoreHookData) HookResult

type QueryHookFunc func(ctx context.Context, data *QueryHookData) HookResult

type funcPromptHook struct {
	name     string
	phase    HookPhase
	priority HookPriority
	fn       PromptHookFunc
}

func (h *funcPromptHook) Name() string           { return h.name }
func (h *funcPromptHook) Phase() HookPhase       { return h.phase }
func (h *funcPromptHook) Priority() HookPriority { return h.priority }
func (h *funcPromptHook) Execute(ctx context.Context, data *PromptHookData) HookResult {
	return h.fn(ctx, data)
}

type funcToolCallHook struct {
	name     string
	phase    HookPhase
	priority HookPriority
	fn       ToolCallHookFunc
}

func (h *funcToolCallHook) Name() string           { return h.name }
func (h *funcToolCallHook) Phase() HookPhase       { return h.phase }
func (h *funcToolCallHook) Priority() HookPriority { return h.priority }
func (h *funcToolCallHook) Execute(ctx context.Context, data *ToolCallHookData) HookResult {
	return h.fn(ctx, data)
}

type funcStoreHook struct {
	name     string
	phase    HookPhase
	priority HookPriority
	fn       StoreHookFunc
}

func (h *funcStoreHook) Name() string           { return h.name }
func (h *funcStoreHook) Phase() HookPhase       { return h.phase }
func (h *funcStoreHook) Priority() HookPriority { return h.priority }
func (h *funcStoreHook) Execute(ctx context.Context, data *StoreHookData) HookResult {
	return h.fn(ctx, data)
}

type funcQueryHook struct {
	name     string
	phase    HookPhase
	priority HookPriority
	fn       QueryHookFunc
}

func (h *funcQueryHook) Name() string           { return h.name }
func (h *funcQueryHook) Phase() HookPhase       { return h.phase }
func (h *funcQueryHook) Priority() HookPriority { return h.priority }
func (h *funcQueryHook) Execute(ctx context.Context, data *QueryHookData) HookResult {
	return h.fn(ctx, data)
}

type HookRegistry struct {
	mu sync.RWMutex

	prePromptHooks  []PromptHook
	postPromptHooks []PromptHook

	preToolCallHooks  []ToolCallHook
	postToolCallHooks []ToolCallHook

	preStoreHooks  []StoreHook
	postStoreHooks []StoreHook

	preQueryHooks  []QueryHook
	postQueryHooks []QueryHook

	promptHooksByName   map[string]PromptHook
	toolCallHooksByName map[string]ToolCallHook
	storeHooksByName    map[string]StoreHook
	queryHooksByName    map[string]QueryHook
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		prePromptHooks:      make([]PromptHook, 0),
		postPromptHooks:     make([]PromptHook, 0),
		preToolCallHooks:    make([]ToolCallHook, 0),
		postToolCallHooks:   make([]ToolCallHook, 0),
		preStoreHooks:       make([]StoreHook, 0),
		postStoreHooks:      make([]StoreHook, 0),
		preQueryHooks:       make([]QueryHook, 0),
		postQueryHooks:      make([]QueryHook, 0),
		promptHooksByName:   make(map[string]PromptHook),
		toolCallHooksByName: make(map[string]ToolCallHook),
		storeHooksByName:    make(map[string]StoreHook),
		queryHooksByName:    make(map[string]QueryHook),
	}
}

func (r *HookRegistry) RegisterPromptHook(hook PromptHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.promptHooksByName[hook.Name()] = hook

	switch hook.Phase() {
	case HookPhasePrePrompt:
		r.prePromptHooks = insertPromptHookSorted(r.prePromptHooks, hook)
	case HookPhasePostPrompt:
		r.postPromptHooks = insertPromptHookSorted(r.postPromptHooks, hook)
	}
}

func (r *HookRegistry) RegisterToolCallHook(hook ToolCallHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.toolCallHooksByName[hook.Name()] = hook

	switch hook.Phase() {
	case HookPhasePreToolCall:
		r.preToolCallHooks = insertToolCallHookSorted(r.preToolCallHooks, hook)
	case HookPhasePostToolCall:
		r.postToolCallHooks = insertToolCallHookSorted(r.postToolCallHooks, hook)
	}
}

func (r *HookRegistry) RegisterStoreHook(hook StoreHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.storeHooksByName[hook.Name()] = hook

	switch hook.Phase() {
	case HookPhasePreStore:
		r.preStoreHooks = insertStoreHookSorted(r.preStoreHooks, hook)
	case HookPhasePostStore:
		r.postStoreHooks = insertStoreHookSorted(r.postStoreHooks, hook)
	}
}

func (r *HookRegistry) RegisterQueryHook(hook QueryHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.queryHooksByName[hook.Name()] = hook

	switch hook.Phase() {
	case HookPhasePreQuery:
		r.preQueryHooks = insertQueryHookSorted(r.preQueryHooks, hook)
	case HookPhasePostQuery:
		r.postQueryHooks = insertQueryHookSorted(r.postQueryHooks, hook)
	}
}

func (r *HookRegistry) RegisterPrePromptHook(name string, priority HookPriority, fn PromptHookFunc) {
	r.RegisterPromptHook(&funcPromptHook{
		name:     name,
		phase:    HookPhasePrePrompt,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPostPromptHook(name string, priority HookPriority, fn PromptHookFunc) {
	r.RegisterPromptHook(&funcPromptHook{
		name:     name,
		phase:    HookPhasePostPrompt,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPreToolCallHook(name string, priority HookPriority, fn ToolCallHookFunc) {
	r.RegisterToolCallHook(&funcToolCallHook{
		name:     name,
		phase:    HookPhasePreToolCall,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPostToolCallHook(name string, priority HookPriority, fn ToolCallHookFunc) {
	r.RegisterToolCallHook(&funcToolCallHook{
		name:     name,
		phase:    HookPhasePostToolCall,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPreStoreHook(name string, priority HookPriority, fn StoreHookFunc) {
	r.RegisterStoreHook(&funcStoreHook{
		name:     name,
		phase:    HookPhasePreStore,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPostStoreHook(name string, priority HookPriority, fn StoreHookFunc) {
	r.RegisterStoreHook(&funcStoreHook{
		name:     name,
		phase:    HookPhasePostStore,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPreQueryHook(name string, priority HookPriority, fn QueryHookFunc) {
	r.RegisterQueryHook(&funcQueryHook{
		name:     name,
		phase:    HookPhasePreQuery,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) RegisterPostQueryHook(name string, priority HookPriority, fn QueryHookFunc) {
	r.RegisterQueryHook(&funcQueryHook{
		name:     name,
		phase:    HookPhasePostQuery,
		priority: priority,
		fn:       fn,
	})
}

func (r *HookRegistry) UnregisterPromptHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.promptHooksByName[name]
	if !ok {
		return false
	}

	delete(r.promptHooksByName, name)

	switch hook.Phase() {
	case HookPhasePrePrompt:
		r.prePromptHooks = removePromptHook(r.prePromptHooks, name)
	case HookPhasePostPrompt:
		r.postPromptHooks = removePromptHook(r.postPromptHooks, name)
	}

	return true
}

func (r *HookRegistry) UnregisterToolCallHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.toolCallHooksByName[name]
	if !ok {
		return false
	}

	delete(r.toolCallHooksByName, name)

	switch hook.Phase() {
	case HookPhasePreToolCall:
		r.preToolCallHooks = removeToolCallHook(r.preToolCallHooks, name)
	case HookPhasePostToolCall:
		r.postToolCallHooks = removeToolCallHook(r.postToolCallHooks, name)
	}

	return true
}

func (r *HookRegistry) UnregisterStoreHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.storeHooksByName[name]
	if !ok {
		return false
	}

	delete(r.storeHooksByName, name)

	switch hook.Phase() {
	case HookPhasePreStore:
		r.preStoreHooks = removeStoreHook(r.preStoreHooks, name)
	case HookPhasePostStore:
		r.postStoreHooks = removeStoreHook(r.postStoreHooks, name)
	}

	return true
}

func (r *HookRegistry) UnregisterQueryHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.queryHooksByName[name]
	if !ok {
		return false
	}

	delete(r.queryHooksByName, name)

	switch hook.Phase() {
	case HookPhasePreQuery:
		r.preQueryHooks = removeQueryHook(r.preQueryHooks, name)
	case HookPhasePostQuery:
		r.postQueryHooks = removeQueryHook(r.postQueryHooks, name)
	}

	return true
}

func (r *HookRegistry) ExecutePrePromptHooks(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
	r.mu.RLock()
	hooks := make([]PromptHook, len(r.prePromptHooks))
	copy(hooks, r.prePromptHooks)
	r.mu.RUnlock()

	return r.executePromptHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePostPromptHooks(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
	r.mu.RLock()
	hooks := make([]PromptHook, len(r.postPromptHooks))
	copy(hooks, r.postPromptHooks)
	r.mu.RUnlock()

	return r.executePromptHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePreToolCallHooks(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]ToolCallHook, len(r.preToolCallHooks))
	copy(hooks, r.preToolCallHooks)
	r.mu.RUnlock()

	return r.executeToolCallHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePostToolCallHooks(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]ToolCallHook, len(r.postToolCallHooks))
	copy(hooks, r.postToolCallHooks)
	r.mu.RUnlock()

	return r.executeToolCallHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePreStoreHooks(ctx context.Context, data *StoreHookData) (*StoreHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]StoreHook, len(r.preStoreHooks))
	copy(hooks, r.preStoreHooks)
	r.mu.RUnlock()

	return r.executeStoreHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePostStoreHooks(ctx context.Context, data *StoreHookData) (*StoreHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]StoreHook, len(r.postStoreHooks))
	copy(hooks, r.postStoreHooks)
	r.mu.RUnlock()

	return r.executeStoreHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePreQueryHooks(ctx context.Context, data *QueryHookData) (*QueryHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]QueryHook, len(r.preQueryHooks))
	copy(hooks, r.preQueryHooks)
	r.mu.RUnlock()

	return r.executeQueryHooks(ctx, hooks, data)
}

func (r *HookRegistry) ExecutePostQueryHooks(ctx context.Context, data *QueryHookData) (*QueryHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]QueryHook, len(r.postQueryHooks))
	copy(hooks, r.postQueryHooks)
	r.mu.RUnlock()

	return r.executeQueryHooks(ctx, hooks, data)
}

func (r *HookRegistry) executePromptHooks(ctx context.Context, hooks []PromptHook, data *PromptHookData) (*PromptHookData, error) {
	current := data
	injectedContext := ""

	for _, hook := range hooks {
		if err := ensureHookContext(ctx); err != nil {
			return current, err
		}

		result := hook.Execute(ctx, current)
		if result.Error != nil {
			return current, result.Error
		}

		current = applyPromptHookResult(current, result, &injectedContext)
		if !result.Continue {
			break
		}
	}

	current = appendInjectedContext(current, injectedContext)
	return current, nil
}

func ensureHookContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func applyPromptHookResult(current *PromptHookData, result HookResult, injectedContext *string) *PromptHookData {
	if result.ModifiedPromptData != nil {
		current = result.ModifiedPromptData
	}

	if result.InjectContext != "" {
		*injectedContext += "\n" + result.InjectContext
	}

	return current
}

func appendInjectedContext(current *PromptHookData, injectedContext string) *PromptHookData {
	if injectedContext != "" {
		current.SystemPrompt += injectedContext
	}
	return current
}

func (r *HookRegistry) executeToolCallHooks(ctx context.Context, hooks []ToolCallHook, data *ToolCallHookData) (*ToolCallHookData, HookResult, error) {
	current := data
	var lastResult HookResult

	for _, hook := range hooks {
		select {
		case <-ctx.Done():
			return current, lastResult, ctx.Err()
		default:
		}

		result := hook.Execute(ctx, current)
		lastResult = result

		if result.Error != nil {
			return current, result, result.Error
		}

		if result.ModifiedToolCallData != nil {
			current = result.ModifiedToolCallData
		}

		if result.SkipExecution {
			return current, result, nil
		}

		if !result.Continue {
			break
		}
	}

	return current, lastResult, nil
}

func (r *HookRegistry) executeStoreHooks(ctx context.Context, hooks []StoreHook, data *StoreHookData) (*StoreHookData, HookResult, error) {
	current := data
	var lastResult HookResult

	for _, hook := range hooks {
		select {
		case <-ctx.Done():
			return current, lastResult, ctx.Err()
		default:
		}

		result := hook.Execute(ctx, current)
		lastResult = result

		if result.Error != nil {
			return current, result, result.Error
		}

		if result.SkipExecution {
			return current, result, nil
		}

		if result.ModifiedStoreData != nil {
			current = result.ModifiedStoreData
		}

		if !result.Continue {
			break
		}
	}

	return current, lastResult, nil
}

func (r *HookRegistry) executeQueryHooks(ctx context.Context, hooks []QueryHook, data *QueryHookData) (*QueryHookData, HookResult, error) {
	current := data
	var lastResult HookResult

	for _, hook := range hooks {
		select {
		case <-ctx.Done():
			return current, lastResult, ctx.Err()
		default:
		}

		result := hook.Execute(ctx, current)
		lastResult = result

		if result.Error != nil {
			return current, result, result.Error
		}

		if result.SkipExecution {
			return current, result, nil
		}

		if result.ModifiedQueryData != nil {
			current = result.ModifiedQueryData
		}

		if !result.Continue {
			break
		}
	}

	return current, lastResult, nil
}

func insertPromptHookSorted(hooks []PromptHook, hook PromptHook) []PromptHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertToolCallHookSorted(hooks []ToolCallHook, hook ToolCallHook) []ToolCallHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertStoreHookSorted(hooks []StoreHook, hook StoreHook) []StoreHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func insertQueryHookSorted(hooks []QueryHook, hook QueryHook) []QueryHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

func removePromptHook(hooks []PromptHook, name string) []PromptHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removeToolCallHook(hooks []ToolCallHook, name string) []ToolCallHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removeStoreHook(hooks []StoreHook, name string) []StoreHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

func removeQueryHook(hooks []QueryHook, name string) []QueryHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

type HookStats struct {
	PrePromptHooks    int `json:"pre_prompt_hooks"`
	PostPromptHooks   int `json:"post_prompt_hooks"`
	PreToolCallHooks  int `json:"pre_tool_call_hooks"`
	PostToolCallHooks int `json:"post_tool_call_hooks"`
	PreStoreHooks     int `json:"pre_store_hooks"`
	PostStoreHooks    int `json:"post_store_hooks"`
	PreQueryHooks     int `json:"pre_query_hooks"`
	PostQueryHooks    int `json:"post_query_hooks"`
	TotalHooks        int `json:"total_hooks"`
}

func (r *HookRegistry) Stats() HookStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return HookStats{
		PrePromptHooks:    len(r.prePromptHooks),
		PostPromptHooks:   len(r.postPromptHooks),
		PreToolCallHooks:  len(r.preToolCallHooks),
		PostToolCallHooks: len(r.postToolCallHooks),
		PreStoreHooks:     len(r.preStoreHooks),
		PostStoreHooks:    len(r.postStoreHooks),
		PreQueryHooks:     len(r.preQueryHooks),
		PostQueryHooks:    len(r.postQueryHooks),
		TotalHooks:        len(r.promptHooksByName) + len(r.toolCallHooksByName) + len(r.storeHooksByName) + len(r.queryHooksByName),
	}
}

func NewContextInjectionHook(name string, priority HookPriority, contextFn func(data *PromptHookData) string) PromptHook {
	return &funcPromptHook{
		name:     name,
		phase:    HookPhasePrePrompt,
		priority: priority,
		fn: func(ctx context.Context, data *PromptHookData) HookResult {
			injected := contextFn(data)
			return HookResult{
				Continue:      true,
				InjectContext: injected,
			}
		},
	}
}

func NewToolCallLoggerHook(name string, logFn func(data *ToolCallHookData)) ToolCallHook {
	return &funcToolCallHook{
		name:     name,
		phase:    HookPhasePreToolCall,
		priority: HookPriorityLow,
		fn: func(ctx context.Context, data *ToolCallHookData) HookResult {
			logFn(data)
			return HookResult{Continue: true}
		},
	}
}

func NewToolCallValidatorHook(name string, validateFn func(data *ToolCallHookData) error) ToolCallHook {
	return &funcToolCallHook{
		name:     name,
		phase:    HookPhasePreToolCall,
		priority: HookPriorityHigh,
		fn: func(ctx context.Context, data *ToolCallHookData) HookResult {
			if err := validateFn(data); err != nil {
				return HookResult{
					Continue: false,
					Error:    err,
				}
			}
			return HookResult{Continue: true}
		},
	}
}

func NewResponseTransformerHook(name string, priority HookPriority, transformFn func(response string) string) PromptHook {
	return &funcPromptHook{
		name:     name,
		phase:    HookPhasePostPrompt,
		priority: priority,
		fn: func(ctx context.Context, data *PromptHookData) HookResult {
			modified := *data
			modified.Response = transformFn(data.Response)
			return HookResult{
				Continue:           true,
				ModifiedPromptData: &modified,
			}
		},
	}
}
