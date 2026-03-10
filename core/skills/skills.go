package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
	SkillName string `json:"skill_name"`
	Success   bool   `json:"success"`
	Data      any    `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
	Err       error  `json:"-"` // typed error preserved for sentinel checks
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
