package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// LLM Skill System
// =============================================================================
//
// Skills are capabilities that LLMs can invoke during generation via tool_use.
// Each skill defines:
// - Name and description (for LLM understanding)
// - Input schema (JSON Schema for parameters)
// - Handler function (executes when LLM invokes the skill)
//
// Skills support progressive loading - they can be loaded/unloaded dynamically
// to manage context window and memory.

// Skill represents an LLM-invocable capability
type Skill struct {
	// Identity
	Name        string `json:"name"`
	Description string `json:"description"`

	// Input schema (JSON Schema format for Anthropic tool_use)
	InputSchema *InputSchema `json:"input_schema"`

	// Handler executes the skill
	Handler Handler `json:"-"`

	// ProviderTool declares that this skill should be exposed as a native
	// provider-backed tool instead of a locally executed function tool.
	ProviderTool *ProviderTool `json:"provider_tool,omitempty"`

	// Loading state
	Loaded   bool `json:"loaded"`
	Priority int  `json:"priority"` // Higher = load first

	// Categorization for progressive loading
	Domain   string   `json:"domain,omitempty"`   // e.g., "memory", "code", "testing"
	Keywords []string `json:"keywords,omitempty"` // Trigger words

	// Usage tracking
	InvokeCount int64 `json:"invoke_count"`

	// Optional explicit token estimate for progressive loading budgets.
	EstimatedTokens int `json:"estimated_tokens,omitempty"`

	// Documentation for markdown generation
	UsageDoc      string   `json:"usage_doc,omitempty"`
	BestPractices []string `json:"best_practices,omitempty"`
	Examples      []string `json:"examples,omitempty"`
	Requirements  []string `json:"requirements,omitempty"`
	Satisfies     []string `json:"satisfies,omitempty"`
	Avoids        []string `json:"avoids,omitempty"`

	// Contract declares the protocol-state artifacts this skill produces
	// and consumes. Used by the dispatcher to enforce invariants at the
	// tool-return boundary so missing-artifact errors surface on the
	// skill that should have produced the artifact, not three turns
	// later when a terminal validator notices it's absent.
	//
	// Nil for skills that have no protocol-state side effects (the
	// common case — ordinary read/search/compute skills).
	Contract *SkillContract `json:"contract,omitempty"`

	// ActionRequirements is the action→required-fields map populated by
	// Builder.ActionRequires. Used by façade skills whose underlying
	// delegates have per-action required fields that the JSON Schema
	// framework cannot express structurally (no if/then/else).
	// ValidateActionRequirements consults this map before a façade
	// handler dispatches to its delegate, returning a schema-echoing
	// error that tells the LLM exactly which fields are required for the
	// chosen action and where in the tool definition to look.
	ActionRequirements map[string][]string `json:"action_requirements,omitempty"`
}

// SkillContract declares the protocol-state artifacts a skill produces
// and consumes. The dispatcher (tool runtime) reads this after each
// tool return: if the skill claimed `Produces: ["test_snapshot"]` but
// the return payload did not populate a test snapshot field, the error
// surfaces on THAT tool call, not three turns later when
// finalize_pipeline notices.
//
// Artifacts are named with short, stable strings (`test_snapshot`,
// `pending_challenge`, `verification_artifact`). The protocol layer
// defines the canonical set; skills reference them by name.
type SkillContract struct {
	// Produces lists the artifact names this skill creates when it
	// succeeds. The dispatcher validates each is present in the return
	// payload (via the protocol state projection) after success.
	Produces []string `json:"produces,omitempty"`
	// Consumes lists artifact names this skill requires to be present
	// in the current protocol state before it's legal to invoke. The
	// dispatcher validates these at call-time; a missing artifact
	// surfaces as a ProtocolContractViolation error with a clear
	// recovery_action pointing at the skill that should have produced
	// the artifact.
	Consumes []string `json:"consumes,omitempty"`
}

// InputSchema defines the JSON Schema for skill inputs
type InputSchema struct {
	Type       string               `json:"type"` // Always "object"
	Properties map[string]*Property `json:"properties"`
	Required   []string             `json:"required,omitempty"`
}

// Property defines a single input property
type Property struct {
	Type        string               `json:"type"`
	Description string               `json:"description,omitempty"`
	Enum        []string             `json:"enum,omitempty"`
	Items       *Property            `json:"items,omitempty"`      // For arrays
	Properties  map[string]*Property `json:"properties,omitempty"` // For nested objects
	Required    []string             `json:"required,omitempty"`   // For nested object required sub-properties
	Default     any                  `json:"default,omitempty"`
}

// Handler executes a skill with the given input
type Handler func(ctx context.Context, input json.RawMessage) (any, error)

// ToolStatus is the structured control-flow status a skill handler can return
// through ToolOutcome. It separates deliberate suspension from failures.
type ToolStatus string

const (
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusYielded   ToolStatus = "yielded"
	ToolStatusCancelled ToolStatus = "cancelled"
	ToolStatusFailed    ToolStatus = "failed"
)

// YieldContinuation carries diagnostic/routing metadata for a yielded tool.
// The continuation store remains the source of truth; this payload lets the
// runtime and telemetry distinguish yield from failure structurally.
type YieldContinuation struct {
	Kind        string         `json:"kind,omitempty"`
	ID          string         `json:"id,omitempty"`
	AwaitedIDs  []string       `json:"awaited_ids,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	ToolName    string         `json:"tool_name,omitempty"`
	Deadline    time.Time      `json:"deadline,omitempty"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ToolOutcome is the first-class return value for tool control flow.
// Err is reserved for real failures; Yielded must return Err nil.
type ToolOutcome struct {
	Result       any                `json:"result,omitempty"`
	Status       ToolStatus         `json:"status"`
	Continuation *YieldContinuation `json:"continuation,omitempty"`
	Err          error              `json:"-"`
}

func CompletedToolOutcome(result any) ToolOutcome {
	return ToolOutcome{Result: result, Status: ToolStatusCompleted}
}

func YieldToolOutcome(continuation *YieldContinuation) ToolOutcome {
	return ToolOutcome{Status: ToolStatusYielded, Continuation: continuation}
}

func FailedToolOutcome(err error) ToolOutcome {
	return ToolOutcome{Status: ToolStatusFailed, Err: err}
}

func NormalizeToolOutcome(value any) (ToolOutcome, bool) {
	switch typed := value.(type) {
	case ToolOutcome:
		return normalizeToolOutcome(typed), true
	case *ToolOutcome:
		if typed == nil {
			return ToolOutcome{}, false
		}
		return normalizeToolOutcome(*typed), true
	default:
		return ToolOutcome{}, false
	}
}

func normalizeToolOutcome(outcome ToolOutcome) ToolOutcome {
	if outcome.Status == "" {
		if outcome.Err != nil {
			outcome.Status = ToolStatusFailed
		} else {
			outcome.Status = ToolStatusCompleted
		}
	}
	return outcome
}

type toolOutcomeSinkKey struct{}

type ToolOutcomeSink struct {
	mu      sync.Mutex
	outcome ToolOutcome
	set     bool
}

func WithToolOutcomeSink(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(toolOutcomeSinkKey{}).(*ToolOutcomeSink); ok {
		return ctx
	}
	return context.WithValue(ctx, toolOutcomeSinkKey{}, &ToolOutcomeSink{})
}

func SetToolOutcomeOnContext(ctx context.Context, outcome ToolOutcome) {
	sink, ok := toolOutcomeSinkFromContext(ctx)
	if !ok {
		return
	}
	sink.mu.Lock()
	sink.outcome = normalizeToolOutcome(outcome)
	sink.set = true
	sink.mu.Unlock()
}

func ToolOutcomeFromContext(ctx context.Context) (ToolOutcome, bool) {
	sink, ok := toolOutcomeSinkFromContext(ctx)
	if !ok {
		return ToolOutcome{}, false
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.set {
		return ToolOutcome{}, false
	}
	return sink.outcome, true
}

func toolOutcomeSinkFromContext(ctx context.Context) (*ToolOutcomeSink, bool) {
	if ctx == nil {
		return nil, false
	}
	sink, ok := ctx.Value(toolOutcomeSinkKey{}).(*ToolOutcomeSink)
	return sink, ok && sink != nil
}

type ProviderToolKind string

const (
	ProviderToolKindFunction        ProviderToolKind = "function"
	ProviderToolKindNativeWebSearch ProviderToolKind = "native_web_search"
)

type WebSearchContextSize string

const (
	WebSearchContextSizeLow    WebSearchContextSize = "low"
	WebSearchContextSizeMedium WebSearchContextSize = "medium"
	WebSearchContextSizeHigh   WebSearchContextSize = "high"
)

type WebSearchUserLocation struct {
	City     string `json:"city,omitempty"`
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type WebSearchOptions struct {
	SearchContextSize WebSearchContextSize   `json:"search_context_size,omitempty"`
	UserLocation      *WebSearchUserLocation `json:"user_location,omitempty"`
	AllowedDomains    []string               `json:"allowed_domains,omitempty"`
	BlockedDomains    []string               `json:"blocked_domains,omitempty"`
	MaxUses           int                    `json:"max_uses,omitempty"`
	Strict            bool                   `json:"strict,omitempty"`
	DeferLoading      bool                   `json:"defer_loading,omitempty"`
	EnableURLContext  bool                   `json:"enable_url_context,omitempty"`
}

type ProviderTool struct {
	Kind        ProviderToolKind  `json:"kind,omitempty"`
	Description string            `json:"description,omitempty"`
	WebSearch   *WebSearchOptions `json:"web_search,omitempty"`
}

func (t ProviderTool) ResolvedKind() ProviderToolKind {
	if t.Kind == "" {
		return ProviderToolKindFunction
	}
	return t.Kind
}

func (t ProviderTool) Clone() ProviderTool {
	clone := t
	if t.WebSearch != nil {
		ws := *t.WebSearch
		if t.WebSearch.UserLocation != nil {
			location := *t.WebSearch.UserLocation
			ws.UserLocation = &location
		}
		if len(t.WebSearch.AllowedDomains) > 0 {
			ws.AllowedDomains = append([]string(nil), t.WebSearch.AllowedDomains...)
		}
		if len(t.WebSearch.BlockedDomains) > 0 {
			ws.BlockedDomains = append([]string(nil), t.WebSearch.BlockedDomains...)
		}
		clone.WebSearch = &ws
	}
	return clone
}

// Result is the result of a skill invocation
type Result struct {
	SkillName string       `json:"skill_name"`
	Success   bool         `json:"success"`
	Data      any          `json:"data,omitempty"`
	Error     string       `json:"error,omitempty"`
	Err       error        `json:"-"` // typed error preserved for sentinel checks
	Outcome   *ToolOutcome `json:"-"`
}

// =============================================================================
// Skill Builder (Fluent API)
// =============================================================================

// Builder provides a fluent API for building skills
type Builder struct {
	skill *Skill
}

// NewSkill creates a new skill builder
func NewSkill(name string) *Builder {
	return &Builder{
		skill: &Skill{
			Name: name,
			InputSchema: &InputSchema{
				Type:       "object",
				Properties: make(map[string]*Property),
			},
		},
	}
}

// Description sets the skill description
func (b *Builder) Description(desc string) *Builder {
	b.skill.Description = desc
	return b
}

// Domain sets the skill domain for progressive loading
func (b *Builder) Domain(domain string) *Builder {
	b.skill.Domain = domain
	return b
}

// Keywords sets trigger keywords
func (b *Builder) Keywords(keywords ...string) *Builder {
	b.skill.Keywords = keywords
	return b
}

// Usage sets the skill usage documentation
func (b *Builder) Usage(usage string) *Builder {
	b.skill.UsageDoc = usage
	return b
}

// Example adds an example
func (b *Builder) Example(example string) *Builder {
	b.skill.Examples = append(b.skill.Examples, example)
	return b
}

// BestPractice adds a best practice
func (b *Builder) BestPractice(practice string) *Builder {
	b.skill.BestPractices = append(b.skill.BestPractices, practice)
	return b
}

// Requirement adds a workflow or input precondition the model should satisfy
// before invoking the skill.
func (b *Builder) Requirement(requirement string) *Builder {
	b.skill.Requirements = append(b.skill.Requirements, requirement)
	return b
}

// Satisfies adds a concise description of what invoking the skill proves or
// advances in the surrounding workflow.
func (b *Builder) Satisfies(outcome string) *Builder {
	b.skill.Satisfies = append(b.skill.Satisfies, outcome)
	return b
}

// Avoid adds guidance for situations where the skill should not be used.
func (b *Builder) Avoid(advice string) *Builder {
	b.skill.Avoids = append(b.skill.Avoids, advice)
	return b
}

// Produces declares protocol-state artifacts this skill creates on
// success. The dispatcher validates the declared artifacts appear in the
// return payload after the handler completes; a missing declared
// artifact surfaces as an immediate ProtocolContractViolation on this
// call site, not on a downstream terminal validator that can't explain
// the drift.
//
// Artifact names are short, stable strings — the protocol layer owns
// the canonical set (see agents/shared/skill_contract_artifacts.go).
// Calling Produces multiple times appends to the list.
func (b *Builder) Produces(artifacts ...string) *Builder {
	if b.skill.Contract == nil {
		b.skill.Contract = &SkillContract{}
	}
	b.skill.Contract.Produces = append(b.skill.Contract.Produces, artifacts...)
	return b
}

// Consumes declares protocol-state artifacts this skill requires to be
// present before invocation. The dispatcher validates these at call
// time; a missing artifact raises a ProtocolContractViolation with a
// recovery_action pointing to the skill that should have produced it.
// Calling Consumes multiple times appends to the list.
func (b *Builder) Consumes(artifacts ...string) *Builder {
	if b.skill.Contract == nil {
		b.skill.Contract = &SkillContract{}
	}
	b.skill.Contract.Consumes = append(b.skill.Contract.Consumes, artifacts...)
	return b
}

// Priority sets loading priority
func (b *Builder) Priority(p int) *Builder {
	b.skill.Priority = p
	return b
}

// TokenEstimate sets an explicit token estimate for loader budgeting.
func (b *Builder) TokenEstimate(tokens int) *Builder {
	b.skill.EstimatedTokens = tokens
	return b
}

// StringParam adds a string parameter
func (b *Builder) StringParam(name, description string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "string",
		Description: description,
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// EnumParam adds an enum parameter
func (b *Builder) EnumParam(name, description string, values []string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "string",
		Description: description,
		Enum:        values,
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// IntParam adds an integer parameter
func (b *Builder) IntParam(name, description string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "integer",
		Description: description,
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// FloatParam adds a number (floating-point) parameter
func (b *Builder) FloatParam(name, description string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "number",
		Description: description,
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// BoolParam adds a boolean parameter
func (b *Builder) BoolParam(name, description string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "boolean",
		Description: description,
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// ArrayParam adds an array parameter
func (b *Builder) ArrayParam(name, description, itemType string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "array",
		Description: description,
		Items:       &Property{Type: itemType},
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// EnumArrayParam adds an array parameter whose items must be one of the
// provided enum values. Use this when a repeated field has a closed
// vocabulary (e.g. pipeline target_agents).
func (b *Builder) EnumArrayParam(name, description, itemType string, values []string, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "array",
		Description: description,
		Items: &Property{
			Type: itemType,
			Enum: append([]string(nil), values...),
		},
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// ArrayObjectParam adds an array parameter whose items are typed objects.
func (b *Builder) ArrayObjectParam(
	name, description string,
	properties map[string]*Property,
	itemRequired []string,
	required bool,
) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "array",
		Description: description,
		Items: &Property{
			Type:       "object",
			Properties: properties,
			Required:   append([]string(nil), itemRequired...),
		},
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// ObjectParam adds an object parameter
func (b *Builder) ObjectParam(name, description string, properties map[string]*Property, required bool) *Builder {
	b.skill.InputSchema.Properties[name] = &Property{
		Type:        "object",
		Description: description,
		Properties:  properties,
	}
	if required {
		b.skill.InputSchema.Required = append(b.skill.InputSchema.Required, name)
	}
	return b
}

// RequireNestedFields marks one or more sub-properties of a previously-added
// object parameter as required at the schema level. The provider serializes
// these into the JSON schema's nested `required` array so the LLM treats
// them as mandatory rather than optional, eliminating the failure class
// where the model satisfies the outer object but omits a sub-field the
// runtime handler enforces (`commentary.summary is required`,
// `payload.id is required`, etc.).
//
// No-op when paramName has not been registered as an object parameter.
func (b *Builder) RequireNestedFields(paramName string, fields ...string) *Builder {
	prop, ok := b.skill.InputSchema.Properties[paramName]
	if !ok || prop == nil || prop.Type != "object" {
		return b
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		seen := false
		for _, existing := range prop.Required {
			if existing == field {
				seen = true
				break
			}
		}
		if !seen {
			prop.Required = append(prop.Required, field)
		}
	}
	return b
}

// InheritSchemaFrom copies parameter definitions from a delegate skill
// onto this builder's skill, prefixing each inherited parameter's
// description with an action-scope tag. Used by façade skills that
// dispatch to multiple delegate handlers based on an action
// discriminator: the façade's schema automatically tracks each delegate's
// parameter shape, eliminating drift between façade and delegate.
//
// When the same parameter name exists on multiple delegates (e.g.
// `summary` shared across validate and process_validation), the action
// scopes merge — the description accumulates actions into a single
// `[action=X|Y]` prefix without duplicating the rest. The first inherited
// copy wins for structural fields (Type, Enum, Items, nested Properties);
// subsequent inheritances only extend the scope tag. Pass the canonical
// parameter shape first if you have a preference.
//
// Delegate-required fields are NOT promoted to the façade's top-level
// `required` array: the JSON Schema framework cannot express "required
// when action=X" structurally. Use PreDispatchValidator / ActionRequires
// to enforce action-scoped required fields at handler boundary.
func (b *Builder) InheritSchemaFrom(delegate *Skill, actionScope string) *Builder {
	if b == nil || delegate == nil || delegate.InputSchema == nil {
		return b
	}
	scope := strings.TrimSpace(actionScope)
	if scope == "" {
		return b
	}
	if b.skill.InputSchema == nil {
		b.skill.InputSchema = &InputSchema{
			Type:       "object",
			Properties: make(map[string]*Property),
		}
	}
	if b.skill.InputSchema.Properties == nil {
		b.skill.InputSchema.Properties = make(map[string]*Property)
	}
	for name, prop := range delegate.InputSchema.Properties {
		if prop == nil {
			continue
		}
		if existing, ok := b.skill.InputSchema.Properties[name]; ok && existing != nil {
			existing.Description = addActionScopeTag(existing.Description, scope)
			continue
		}
		clone := clonePropertyForInheritance(prop)
		clone.Description = addActionScopeTag(stripActionScopeTag(clone.Description), scope)
		b.skill.InputSchema.Properties[name] = clone
	}
	return b
}

// clonePropertyForInheritance returns a deep copy of a Property so the
// inheriting builder cannot mutate the delegate's schema in place. Nested
// Items, Properties, Enum, and Required slices are all duplicated.
func clonePropertyForInheritance(src *Property) *Property {
	if src == nil {
		return nil
	}
	dst := &Property{
		Type:        src.Type,
		Description: src.Description,
		Default:     src.Default,
	}
	if len(src.Enum) > 0 {
		dst.Enum = append([]string(nil), src.Enum...)
	}
	if src.Items != nil {
		dst.Items = clonePropertyForInheritance(src.Items)
	}
	if len(src.Properties) > 0 {
		dst.Properties = make(map[string]*Property, len(src.Properties))
		for k, v := range src.Properties {
			dst.Properties[k] = clonePropertyForInheritance(v)
		}
	}
	if len(src.Required) > 0 {
		dst.Required = append([]string(nil), src.Required...)
	}
	return dst
}

// actionScopeTagPrefix / actionScopeTagSuffix frame the tag so it can be
// parsed back out of a description without regexp. The convention is:
// `[action=X|Y|Z] rest of description`.
const (
	actionScopeTagPrefix = "[action="
	actionScopeTagSuffix = "] "
)

// addActionScopeTag inserts or extends the `[action=...]` prefix on a
// description. If the description already starts with a scope tag, the
// new action is appended to it (deduplicated, pipe-separated). Otherwise
// the tag is prepended.
func addActionScopeTag(description, action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return description
	}
	existing, rest := splitActionScopeTag(description)
	merged := mergeActionList(existing, action)
	if rest == "" {
		return actionScopeTagPrefix + merged + actionScopeTagSuffix[:1] + actionScopeTagSuffix[1:]
	}
	return actionScopeTagPrefix + merged + actionScopeTagSuffix + rest
}

// stripActionScopeTag removes any leading `[action=...] ` prefix.
func stripActionScopeTag(description string) string {
	_, rest := splitActionScopeTag(description)
	return rest
}

// splitActionScopeTag splits a description into (actions, rest) where
// `actions` is the pipe-separated action list from the tag (empty if no
// tag present) and `rest` is the unprefixed description body.
func splitActionScopeTag(description string) (string, string) {
	if !strings.HasPrefix(description, actionScopeTagPrefix) {
		return "", description
	}
	remainder := description[len(actionScopeTagPrefix):]
	closeIdx := strings.Index(remainder, actionScopeTagSuffix)
	if closeIdx < 0 {
		// Tag open but no terminator — treat as untagged.
		return "", description
	}
	actions := remainder[:closeIdx]
	rest := remainder[closeIdx+len(actionScopeTagSuffix):]
	return actions, rest
}

// mergeActionList adds a new action to a pipe-separated list, preserving
// the existing order and dropping duplicates.
func mergeActionList(existing, add string) string {
	add = strings.TrimSpace(add)
	if existing == "" {
		return add
	}
	parts := strings.Split(existing, "|")
	for _, p := range parts {
		if strings.TrimSpace(p) == add {
			return existing
		}
	}
	parts = append(parts, add)
	return strings.Join(parts, "|")
}

// ActionRequires declares that the named parameters are required when
// the façade is invoked with the given action. The skills framework has
// no native support for conditional required fields in JSON Schema, so
// this metadata drives the pre-dispatch validator attached via
// DispatchByAction: the façade rejects calls that omit the fields with
// a schema-echoing error before the delegate handler sees the input.
//
// Callable repeatedly — each call appends to the per-action set.
func (b *Builder) ActionRequires(action string, fields ...string) *Builder {
	if b == nil {
		return b
	}
	action = strings.TrimSpace(action)
	if action == "" || len(fields) == 0 {
		return b
	}
	if b.skill.ActionRequirements == nil {
		b.skill.ActionRequirements = make(map[string][]string)
	}
	existing := b.skill.ActionRequirements[action]
	seen := make(map[string]struct{}, len(existing)+len(fields))
	for _, f := range existing {
		seen[f] = struct{}{}
	}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		existing = append(existing, f)
	}
	b.skill.ActionRequirements[action] = existing
	return b
}

// Handler sets the skill handler
func (b *Builder) Handler(h Handler) *Builder {
	b.skill.Handler = h
	return b
}

// ProviderTool exposes the skill as a native provider-backed tool definition.
func (b *Builder) ProviderTool(tool ProviderTool) *Builder {
	toolCopy := tool.Clone()
	b.skill.ProviderTool = &toolCopy
	return b
}

// Build returns the constructed skill
func (b *Builder) Build() *Skill {
	return b.skill
}

// ValidateActionRequirements checks that the raw input JSON contains every
// field listed in the skill's ActionRequirements map for the given action.
// Returns nil when all required fields for the action are present (or when
// no requirements are declared). Returns a schema-echoing error otherwise
// whose message includes the action, the missing fields, and the exact
// Property definitions the façade exposes for those fields — so the LLM
// sees the expected shape in the error, not just the field name.
//
// Usage pattern inside a façade handler:
//
//	if err := skill.ValidateActionRequirements(action, input); err != nil {
//	    return nil, err
//	}
//	return delegate.Handler(ctx, input)
func (s *Skill) ValidateActionRequirements(action string, input json.RawMessage) error {
	if s == nil {
		return nil
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return nil
	}
	required := s.ActionRequirements[action]
	if len(required) == 0 {
		return nil
	}
	provided := map[string]json.RawMessage{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &provided); err != nil {
			return fmt.Errorf("invalid parameters: %w", err)
		}
	}
	var missing []string
	for _, field := range required {
		raw, ok := provided[field]
		if !ok || isJSONEmpty(raw) {
			missing = append(missing, field)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s", s.formatActionRequirementsError(action, missing))
}

// formatActionRequirementsError builds a schema-echoing error message for
// the given action + missing-fields set. For each missing field it
// includes the Property definition (type, enum, items, nested required)
// so the LLM can see the exact shape it should pass on retry.
func (s *Skill) formatActionRequirementsError(action string, missing []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "tool %q (action=%s): missing required field(s): %s", s.Name, action, strings.Join(missing, ", "))
	if s.InputSchema == nil || len(s.InputSchema.Properties) == 0 {
		return sb.String()
	}
	sb.WriteString(". Expected shape for this action:")
	for _, field := range missing {
		prop := s.InputSchema.Properties[field]
		if prop == nil {
			fmt.Fprintf(&sb, "\n  - %s: <field is declared required for action=%s but has no schema on the façade; this is a wiring bug>", field, action)
			continue
		}
		fmt.Fprintf(&sb, "\n  - %s: %s", field, describePropertyForError(prop))
	}
	return sb.String()
}

// describePropertyForError renders a Property into a compact shape
// description suitable for an error message. Focuses on the structural
// spine (type, enum, item type, required nested fields) rather than
// recapitulating the full description prose.
func describePropertyForError(prop *Property) string {
	if prop == nil {
		return "<nil>"
	}
	var sb strings.Builder
	sb.WriteString(prop.Type)
	switch prop.Type {
	case "array":
		if prop.Items != nil {
			sb.WriteString(" of ")
			sb.WriteString(describePropertyForError(prop.Items))
		}
	case "object":
		if len(prop.Properties) > 0 {
			sb.WriteString(" {")
			first := true
			requiredSet := make(map[string]struct{}, len(prop.Required))
			for _, r := range prop.Required {
				requiredSet[r] = struct{}{}
			}
			for name, sub := range prop.Properties {
				if !first {
					sb.WriteString(", ")
				}
				first = false
				sb.WriteString(name)
				if _, req := requiredSet[name]; !req {
					sb.WriteString("?")
				}
				sb.WriteString(": ")
				sb.WriteString(sub.Type)
			}
			sb.WriteString("}")
		}
	}
	if len(prop.Enum) > 0 {
		fmt.Fprintf(&sb, " ∈ {%s}", strings.Join(prop.Enum, "|"))
	}
	if strings.TrimSpace(prop.Description) != "" {
		fmt.Fprintf(&sb, " — %s", strings.TrimSpace(prop.Description))
	}
	return sb.String()
}

// isJSONEmpty reports whether the raw JSON value is empty (null, empty
// string, empty array, empty object). Used by ValidateActionRequirements
// so a caller that passes `"targets": []` is treated as missing, not as
// satisfied.
func isJSONEmpty(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "", "null", "\"\"", "[]", "{}":
		return true
	}
	return false
}

// =============================================================================
// Markdown Generation
// =============================================================================

// yamlSafeValue wraps a string in double quotes if it contains characters that
// would break unquoted YAML scalar parsing (colons, brackets, etc.).
func yamlSafeValue(v string) string {
	if strings.ContainsAny(v, ":{}[]#&*!|>'\",") {
		escaped := strings.ReplaceAll(v, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return v
}

// ToMarkdown generates a full SKILL.md document from the skill definition
func (s *Skill) ToMarkdown() string {
	var sb strings.Builder

	// Frontmatter — quote values that contain YAML-special characters
	// (colons, brackets, etc.) to prevent parse failures in ReadSkillString.
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", yamlSafeValue(s.Name)))
	sb.WriteString(fmt.Sprintf("description: %s\n", yamlSafeValue(s.Description)))
	if s.Domain != "" {
		sb.WriteString("metadata:\n")
		sb.WriteString(fmt.Sprintf("  category: %s\n", yamlSafeValue(s.Domain)))
		sb.WriteString("  version: 1.0.0\n")
	}
	sb.WriteString("---\n\n")

	// Title
	sb.WriteString(fmt.Sprintf("# %s Skill\n\n", s.Name))

	// Description or Usage
	if s.UsageDoc != "" {
		sb.WriteString(s.UsageDoc + "\n\n")
	} else {
		sb.WriteString(s.Description + "\n\n")
	}

	// Parameters
	if s.InputSchema != nil && len(s.InputSchema.Properties) > 0 {
		sb.WriteString("## Parameters\n\n")
		sb.WriteString("| Parameter | Type | Required | Description |\n")
		sb.WriteString("|-----------|------|----------|-------------|\n")

		reqMap := make(map[string]bool)
		for _, req := range s.InputSchema.Required {
			reqMap[req] = true
		}

		for name, prop := range s.InputSchema.Properties {
			reqStr := "No"
			if reqMap[name] {
				reqStr = "Yes"
			}
			desc := prop.Description
			if len(prop.Enum) > 0 {
				desc += fmt.Sprintf(" (One of: %v)", prop.Enum)
			}
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", name, prop.Type, reqStr, desc))
		}
		sb.WriteString("\n")
	}

	// Examples
	if len(s.Examples) > 0 {
		sb.WriteString("## Example Usage\n\n")
		for _, ex := range s.Examples {
			sb.WriteString(ex + "\n\n")
		}
	}

	// Best Practices
	if len(s.BestPractices) > 0 {
		sb.WriteString("## Best Practices\n\n")
		for i, bp := range s.BestPractices {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, bp))
		}
		sb.WriteString("\n")
	}

	if len(s.Requirements) > 0 {
		sb.WriteString("## Requirements\n\n")
		for i, requirement := range s.Requirements {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, requirement))
		}
		sb.WriteString("\n")
	}

	if len(s.Satisfies) > 0 {
		sb.WriteString("## Satisfies\n\n")
		for i, outcome := range s.Satisfies {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, outcome))
		}
		sb.WriteString("\n")
	}

	if len(s.Avoids) > 0 {
		sb.WriteString("## Avoid\n\n")
		for i, advice := range s.Avoids {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, advice))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()) + "\n"
}

// =============================================================================
// Skill Registry
// =============================================================================

// Registry manages available skills
type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill

	// Index by domain for progressive loading
	byDomain map[string][]*Skill

	// Index by keyword for trigger detection
	byKeyword map[string][]*Skill
}

// NewRegistry creates a new skill registry
func NewRegistry() *Registry {
	return &Registry{
		skills:    make(map[string]*Skill),
		byDomain:  make(map[string][]*Skill),
		byKeyword: make(map[string][]*Skill),
	}
}

// Register adds a skill to the registry
func (r *Registry) Register(skill *Skill) error {
	if skill.Name == "" {
		return fmt.Errorf("skill name is required")
	}
	if skill.Handler == nil && skill.ProviderTool == nil {
		return fmt.Errorf("skill handler or provider tool is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.skills[skill.Name] = skill

	// Index by domain
	if skill.Domain != "" {
		r.byDomain[skill.Domain] = append(r.byDomain[skill.Domain], skill)
	}

	// Index by keywords
	for _, kw := range skill.Keywords {
		r.byKeyword[kw] = append(r.byKeyword[kw], skill)
	}

	return nil
}

// Unregister removes a skill from the registry
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	skill, ok := r.skills[name]
	if !ok {
		return
	}

	delete(r.skills, name)
	r.removeDomainIndex(skill)
	r.removeKeywordIndex(skill)
}

func (r *Registry) removeDomainIndex(skill *Skill) {
	if skill.Domain == "" {
		return
	}

	skills := r.byDomain[skill.Domain]
	r.byDomain[skill.Domain] = removeSkillByName(skills, skill.Name)
}

func (r *Registry) removeKeywordIndex(skill *Skill) {
	for _, keyword := range skill.Keywords {
		skills := r.byKeyword[keyword]
		r.byKeyword[keyword] = removeSkillByName(skills, skill.Name)
	}
}

func removeSkillByName(skills []*Skill, name string) []*Skill {
	for index, existing := range skills {
		if existing.Name == name {
			return append(skills[:index], skills[index+1:]...)
		}
	}
	return skills
}

// Get returns a skill by name
func (r *Registry) Get(name string) *Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skills[name]
}

// GetByDomain returns all skills in a domain
func (r *Registry) GetByDomain(domain string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skills := r.byDomain[domain]
	result := make([]*Skill, len(skills))
	copy(result, skills)
	return result
}

// GetByKeyword returns skills matching a keyword
func (r *Registry) GetByKeyword(keyword string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skills := r.byKeyword[keyword]
	result := make([]*Skill, len(skills))
	copy(result, skills)
	return result
}

// GetAll returns all registered skills
func (r *Registry) GetAll() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		result = append(result, skill)
	}
	return result
}

// GetLoaded returns all currently loaded skills
func (r *Registry) GetLoaded() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Skill
	for _, skill := range r.skills {
		if skill.Loaded {
			result = append(result, skill)
		}
	}
	return result
}

// Invoke executes a skill by name
func (r *Registry) Invoke(ctx context.Context, name string, input json.RawMessage) *Result {
	r.mu.RLock()
	skill := r.skills[name]
	r.mu.RUnlock()

	if skill == nil {
		return &Result{
			SkillName: name,
			Success:   false,
			Error:     fmt.Sprintf("skill not found: %s", name),
		}
	}

	if skill.Handler == nil {
		return &Result{
			SkillName: name,
			Success:   false,
			Error:     fmt.Sprintf("skill has no handler: %s", name),
		}
	}

	// Execute handler
	result, err := skill.Handler(ctx, input)

	// Update invoke count
	r.mu.Lock()
	skill.InvokeCount++
	r.mu.Unlock()

	if err != nil {
		return &Result{
			SkillName: name,
			Success:   false,
			Error:     err.Error(),
			Err:       err,
		}
	}
	if outcome, ok := NormalizeToolOutcome(result); ok {
		switch outcome.Status {
		case ToolStatusFailed:
			err := outcome.Err
			if err == nil {
				err = fmt.Errorf("tool outcome failed")
			}
			return &Result{
				SkillName: name,
				Success:   false,
				Error:     err.Error(),
				Err:       err,
				Outcome:   &outcome,
			}
		default:
			return &Result{
				SkillName: name,
				Success:   true,
				Data:      outcome.Result,
				Outcome:   &outcome,
			}
		}
	}

	return &Result{
		SkillName: name,
		Success:   true,
		Data:      result,
	}
}

// =============================================================================
// Tool Definition (for Anthropic API)
// =============================================================================

// ToToolDefinition converts a skill to provider tool format.
func (s *Skill) ToToolDefinition() map[string]any {
	schema := s.InputSchema
	if schema == nil {
		schema = &InputSchema{Type: "object", Properties: make(map[string]*Property)}
	} else if schema.Properties == nil {
		schema.Properties = make(map[string]*Property)
	}
	return map[string]any{
		"name":         s.Name,
		"description":  s.Description,
		"input_schema": schema,
	}
}

// EstimatedTokenCost returns an approximate token cost for loading this skill
// into model tool context.
func (s *Skill) EstimatedTokenCost() int {
	if s == nil {
		return 0
	}
	if s.EstimatedTokens > 0 {
		return s.EstimatedTokens
	}
	payload, err := json.Marshal(s.ToToolDefinition())
	if err != nil {
		return 120
	}
	estimated := len(payload) / 4
	if estimated < 60 {
		return 60
	}
	return estimated
}

// ToToolDefinitions converts multiple skills to Anthropic tool format
func (r *Registry) ToToolDefinitions(skills []*Skill) []map[string]any {
	result := make([]map[string]any, len(skills))
	for i, skill := range skills {
		result[i] = skill.ToToolDefinition()
	}
	return result
}

// GetToolDefinitions returns tool definitions for all loaded skills
func (r *Registry) GetToolDefinitions() []map[string]any {
	return r.ToToolDefinitions(r.GetLoaded())
}

// =============================================================================
// Skill Loading
// =============================================================================

// Load marks a skill as loaded (included in LLM context)
func (r *Registry) Load(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	skill, ok := r.skills[name]
	if !ok {
		return false
	}
	skill.Loaded = true
	return true
}

// Unload marks a skill as unloaded (excluded from LLM context)
func (r *Registry) Unload(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	skill, ok := r.skills[name]
	if !ok {
		return false
	}
	skill.Loaded = false
	return true
}

// LoadDomain loads all skills in a domain
func (r *Registry) LoadDomain(domain string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for _, skill := range r.byDomain[domain] {
		if !skill.Loaded {
			skill.Loaded = true
			count++
		}
	}
	return count
}

// UnloadDomain unloads all skills in a domain
func (r *Registry) UnloadDomain(domain string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for _, skill := range r.byDomain[domain] {
		if skill.Loaded {
			skill.Loaded = false
			count++
		}
	}
	return count
}

// LoadAll loads all skills
func (r *Registry) LoadAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, skill := range r.skills {
		skill.Loaded = true
	}
}

// UnloadAll unloads all skills
func (r *Registry) UnloadAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, skill := range r.skills {
		skill.Loaded = false
	}
}

// =============================================================================
// Skill Stats
// =============================================================================

// Stats contains statistics about skills
type Stats struct {
	Total      int            `json:"total"`
	Loaded     int            `json:"loaded"`
	ByDomain   map[string]int `json:"by_domain"`
	TopInvoked []Usage        `json:"top_invoked"`
}

// Usage tracks skill usage
type Usage struct {
	Name        string `json:"name"`
	InvokeCount int64  `json:"invoke_count"`
}

// Stats returns skill statistics
func (r *Registry) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := Stats{
		Total:    len(r.skills),
		ByDomain: make(map[string]int),
	}

	for _, skill := range r.skills {
		accumulateSkillStats(&stats, skill)
	}

	stats.TopInvoked = topInvokedSkills(r.skills)
	return stats
}

func accumulateSkillStats(stats *Stats, skill *Skill) {
	if skill.Loaded {
		stats.Loaded++
	}
	if skill.Domain != "" {
		stats.ByDomain[skill.Domain]++
	}
}

func topInvokedSkills(skills map[string]*Skill) []Usage {
	usages := collectSkillUsages(skills)
	sortTopInvoked(usages)
	return trimTopInvoked(usages, 5)
}

func collectSkillUsages(skills map[string]*Skill) []Usage {
	usages := make([]Usage, 0, len(skills))
	for _, skill := range skills {
		if skill.InvokeCount > 0 {
			usages = append(usages, Usage{Name: skill.Name, InvokeCount: skill.InvokeCount})
		}
	}
	return usages
}

func sortTopInvoked(usages []Usage) {
	for i := 0; i < len(usages) && i < 5; i++ {
		for j := i + 1; j < len(usages); j++ {
			if usages[j].InvokeCount > usages[i].InvokeCount {
				usages[i], usages[j] = usages[j], usages[i]
			}
		}
	}
}

func trimTopInvoked(usages []Usage, limit int) []Usage {
	if len(usages) > limit {
		return usages[:limit]
	}
	return usages
}
