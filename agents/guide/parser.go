package guide

import (
	"encoding/json"
	"regexp"
	"strings"
)

// =============================================================================
// DSL Parser
// =============================================================================

// Parser handles parsing of structured DSL commands
//
// Supported formats:
//   - @guide <query>              - Intent-based routing (LLM classification)
//   - @to:<agent> <query>         - Direct route TO an agent (no classification)
//   - @from:<agent> <response>    - Response routing FROM an agent
//   - @<action> <query>           - Action shortcut (registered by agents)
//   - @<agent>:<intent>:<domain>  - Full DSL with explicit intent/domain
type Parser struct {
	prefix string

	// Compiled patterns
	fullDSLPattern    *regexp.Regexp // @agent:intent:domain?params{data}
	directToPattern   *regexp.Regexp // @to:<agent> <query>
	directFromPattern *regexp.Regexp // @from:<agent> <response>
	guidePattern      *regexp.Regexp // @guide <query>
	actionPattern     *regexp.Regexp // @<action> <query> - dynamic based on registered actions
	paramPattern      *regexp.Regexp

	// Shortcuts for full DSL (base set, extended by agent registrations)
	agentShortcuts  map[string]string
	intentShortcuts map[string]Intent
	domainShortcuts map[string]Domain

	// Routing aggregator for dynamic shortcuts
	routing *RoutingAggregator
}

// NewParser creates a new DSL parser
func NewParser(prefix string) *Parser {
	return NewParserWithRouting(prefix, nil)
}

// NewParserWithRouting creates a new DSL parser with routing aggregator
func NewParserWithRouting(prefix string, routing *RoutingAggregator) *Parser {
	if prefix == "" {
		prefix = "@"
	}

	p := &Parser{
		prefix:  prefix,
		routing: routing,
		// Base agent shortcuts (Guide always available)
		agentShortcuts: map[string]string{
			"guide": "guide",
			"g":     "guide",
		},
		intentShortcuts: map[string]Intent{
			"recall":   IntentRecall,
			"r":        IntentRecall,
			"store":    IntentStore,
			"s":        IntentStore,
			"check":    IntentCheck,
			"c":        IntentCheck,
			"declare":  IntentDeclare,
			"d":        IntentDeclare,
			"complete": IntentComplete,
			"help":     IntentHelp,
			"?":        IntentHelp,
			"status":   IntentStatus,
			"!":        IntentStatus,
		},
		domainShortcuts: map[string]Domain{
			"patterns":  DomainPatterns,
			"pat":       DomainPatterns,
			"p":         DomainPatterns,
			"failures":  DomainFailures,
			"fail":      DomainFailures,
			"f":         DomainFailures,
			"decisions": DomainDecisions,
			"dec":       DomainDecisions,
			"files":     DomainFiles,
			"learnings": DomainLearnings,
			"learn":     DomainLearnings,
			"l":         DomainLearnings,
			"intents":   DomainIntents,
			"agents":    DomainAgents,
			"system":    DomainSystem,
			"sys":       DomainSystem,
		},
	}

	// Apply agent shortcuts from routing aggregator
	if routing != nil {
		for alias, agentID := range routing.GetAllAliases() {
			p.agentShortcuts[alias] = agentID
		}
	}

	// Compile patterns
	esc := regexp.QuoteMeta(prefix)

	// Full DSL: @agent:intent:domain?params{data}
	p.fullDSLPattern = regexp.MustCompile(
		`^` + esc + `(\w+):(\w+|\?|!):(\w+)(?:\?([^{]*))?(?:\{(.+)\})?$`,
	)

	// Direct routing: @to:<agent> <query> or @to:<agent>
	p.directToPattern = regexp.MustCompile(
		`^` + esc + `to:(\w+)(?:\s+(.*))?$`,
	)

	// Response routing: @from:<agent> <response> or @from:<agent>
	p.directFromPattern = regexp.MustCompile(
		`^` + esc + `from:(\w+)(?:\s+(.*))?$`,
	)

	// Guide (intent routing): @guide <query> or @guide
	p.guidePattern = regexp.MustCompile(
		`^` + esc + `guide(?:\s+(.*))?$`,
	)

	// Action pattern: @<action> <query> - matches any word after @
	// We'll validate against registered actions during parsing
	p.actionPattern = regexp.MustCompile(
		`^` + esc + `(\w+)(?:\s+(.*))?$`,
	)

	// Parameter pattern for full DSL
	p.paramPattern = regexp.MustCompile(`(\w+)=([^&]+)`)

	return p
}

// SetRouting sets the routing aggregator (for dynamic shortcut updates)
func (p *Parser) SetRouting(routing *RoutingAggregator) {
	p.routing = routing
	// Refresh agent shortcuts
	if routing != nil {
		for alias, agentID := range routing.GetAllAliases() {
			p.agentShortcuts[alias] = agentID
		}
	}
}

// IsDSL checks if input looks like a DSL command
func (p *Parser) IsDSL(input string) bool {
	input = strings.TrimSpace(input)
	return strings.HasPrefix(input, p.prefix)
}

// Parse parses a DSL command string
func (p *Parser) Parse(input string) (*DSLCommand, error) {
	input = strings.TrimSpace(input)

	if !p.IsDSL(input) {
		return nil, &DSLParseError{
			Input:    input,
			Position: 0,
			Expected: "DSL command starting with " + p.prefix,
			Got:      input,
			Message:  "input does not start with DSL prefix",
		}
	}

	// Try each pattern in order of specificity

	// 1. Direct TO routing: @to:<agent> <query>
	if matches := p.directToPattern.FindStringSubmatch(input); matches != nil {
		return &DSLCommand{
			Type:        DSLCommandTypeDirect,
			Direction:   DSLDirectionTo,
			TargetAgent: p.resolveAgent(matches[1]),
			Query:       strings.TrimSpace(matches[2]),
			Raw:         input,
		}, nil
	}

	// 2. Direct FROM routing: @from:<agent> <response>
	if matches := p.directFromPattern.FindStringSubmatch(input); matches != nil {
		return &DSLCommand{
			Type:        DSLCommandTypeDirect,
			Direction:   DSLDirectionFrom,
			TargetAgent: p.resolveAgent(matches[1]),
			Query:       strings.TrimSpace(matches[2]),
			Raw:         input,
		}, nil
	}

	// 3. Guide (intent routing): @guide <query>
	if matches := p.guidePattern.FindStringSubmatch(input); matches != nil {
		return &DSLCommand{
			Type:  DSLCommandTypeIntent,
			Query: strings.TrimSpace(matches[1]),
			Raw:   input,
		}, nil
	}

	// 4. Action shortcuts: @<action> <query>
	// Check against registered action shortcuts from routing aggregator
	if matches := p.actionPattern.FindStringSubmatch(input); matches != nil {
		action := strings.ToLower(matches[1])
		if shortcut, agentID, ok := p.resolveActionShortcut(action); ok {
			cmd := &DSLCommand{
				Type:        DSLCommandTypeAction,
				TargetAgent: agentID,
				Query:       strings.TrimSpace(matches[2]),
				Raw:         input,
			}
			// Apply default intent/domain from shortcut if available
			if shortcut != nil {
				if shortcut.DefaultIntent != "" {
					cmd.Intent = shortcut.DefaultIntent
				}
				if shortcut.DefaultDomain != "" {
					cmd.Domain = shortcut.DefaultDomain
				}
			}
			return cmd, nil
		}
	}

	// 5. Full DSL: @agent:intent:domain?params{data}
	if matches := p.fullDSLPattern.FindStringSubmatch(input); matches != nil {
		return p.parseFullDSL(input, matches)
	}

	return nil, &DSLParseError{
		Input:    input,
		Position: 0,
		Expected: "@guide, @to:<agent>, @from:<agent>, @archive, or @agent:intent:domain",
		Got:      input,
		Message:  "unrecognized DSL command format",
	}
}

// parseFullDSL parses the full DSL format: @agent:intent:domain?params{data}
func (p *Parser) parseFullDSL(input string, matches []string) (*DSLCommand, error) {
	agentStr := strings.ToLower(matches[1])
	intentStr := strings.ToLower(matches[2])
	domainStr := strings.ToLower(matches[3])
	paramsStr := matches[4]
	dataStr := matches[5]

	// Resolve agent
	agent, agentOk := p.agentShortcuts[agentStr]
	if !agentOk {
		return nil, &DSLParseError{
			Input:    input,
			Position: len(p.prefix),
			Expected: "valid agent (arch, guide)",
			Got:      agentStr,
			Message:  "unknown agent: " + agentStr,
		}
	}

	// Resolve intent
	intent, intentOk := p.intentShortcuts[intentStr]
	if !intentOk {
		return nil, &DSLParseError{
			Input:    input,
			Position: len(p.prefix) + len(agentStr) + 1,
			Expected: "valid intent (recall, store, check, declare, complete, help, status)",
			Got:      intentStr,
			Message:  "unknown intent: " + intentStr,
		}
	}

	// Resolve domain
	domain, domainOk := p.domainShortcuts[domainStr]
	if !domainOk {
		return nil, &DSLParseError{
			Input:    input,
			Position: len(p.prefix) + len(agentStr) + len(intentStr) + 2,
			Expected: "valid domain (patterns, failures, decisions, files, learnings, system, agents)",
			Got:      domainStr,
			Message:  "unknown domain: " + domainStr,
		}
	}

	cmd := &DSLCommand{
		Type:        DSLCommandTypeFull,
		TargetAgent: agent,
		Intent:      intent,
		Domain:      domain,
		Raw:         input,
	}

	// Parse parameters
	if paramsStr != "" {
		cmd.Params = p.parseParams(paramsStr)
	}

	// Parse data
	if dataStr != "" {
		data, err := p.parseData(dataStr)
		if err != nil {
			return nil, &DSLParseError{
				Input:    input,
				Position: strings.Index(input, "{"),
				Expected: "valid JSON object",
				Got:      dataStr,
				Message:  "invalid JSON data: " + err.Error(),
			}
		}
		cmd.Data = data
	}

	return cmd, nil
}

// resolveAgent resolves an agent shortcut to the full agent name
func (p *Parser) resolveAgent(agent string) string {
	agent = strings.ToLower(agent)

	// First check local shortcuts
	if resolved, ok := p.agentShortcuts[agent]; ok {
		return resolved
	}

	// Then check routing aggregator
	if p.routing != nil {
		if resolved, ok := p.routing.ResolveAgent(agent); ok {
			return resolved
		}
	}

	return agent // Return as-is if no shortcut
}

// resolveActionShortcut resolves an action to its shortcut details and agent ID
func (p *Parser) resolveActionShortcut(action string) (*ActionShortcut, string, bool) {
	if p.routing == nil {
		return nil, "", false
	}
	return p.routing.GetActionShortcut(action)
}

// parseParams parses key=value parameters
func (p *Parser) parseParams(paramsStr string) map[string]string {
	params := make(map[string]string)

	matches := p.paramPattern.FindAllStringSubmatch(paramsStr, -1)
	for _, match := range matches {
		if len(match) >= 3 {
			key := strings.TrimSpace(match[1])
			value := strings.TrimSpace(match[2])
			params[key] = value
		}
	}

	return params
}

// parseData parses JSON data
func (p *Parser) parseData(dataStr string) (map[string]any, error) {
	// Handle relaxed JSON (unquoted keys, single quotes)
	dataStr = p.normalizeJSON(dataStr)

	var data map[string]any
	if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
		return nil, err
	}

	return data, nil
}

// normalizeJSON converts relaxed JSON to strict JSON
func (p *Parser) normalizeJSON(s string) string {
	// Replace single quotes with double quotes
	s = strings.ReplaceAll(s, "'", "\"")

	// Add quotes around unquoted keys
	keyPattern := regexp.MustCompile(`(\w+)\s*:`)
	s = keyPattern.ReplaceAllString(s, `"$1":`)

	// Fix double-quoted keys that got double-quoted
	s = strings.ReplaceAll(s, `""`, `"`)

	return s
}

// ParseMultiple parses multiple DSL commands separated by semicolons
func (p *Parser) ParseMultiple(input string) ([]*DSLCommand, []error) {
	input = strings.TrimSpace(input)

	parts := strings.Split(input, ";")

	var commands []*DSLCommand
	var errors []error

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		cmd, err := p.Parse(part)
		if err != nil {
			errors = append(errors, err)
		} else {
			commands = append(commands, cmd)
		}
	}

	return commands, errors
}

// =============================================================================
// DSL Command Methods
// =============================================================================

// ToRouteResult converts a DSL command to a route result
func (cmd *DSLCommand) ToRouteResult() *RouteResult {
	result := &RouteResult{
		Confidence:           1.0, // DSL commands have perfect confidence
		ClassificationMethod: "dsl",
		Action:               RouteActionExecute,
	}

	switch cmd.Type {
	case DSLCommandTypeFull:
		// Full DSL has explicit intent/domain
		result.Intent = cmd.Intent
		result.Domain = cmd.Domain
		result.TargetAgent = TargetAgent(cmd.TargetAgent)
		if cmd.Domain.IsHistoricalDomain() {
			result.TemporalFocus = TemporalPast
		} else {
			result.TemporalFocus = TemporalPresent
		}
		result.Entities = cmd.ToEntities()

	case DSLCommandTypeDirect, DSLCommandTypeAction:
		// Direct routing - target is specified, no classification needed
		result.TargetAgent = TargetAgent(cmd.TargetAgent)
		result.Intent = IntentUnknown // Will be determined by target agent
		result.Domain = DomainUnknown
		result.Entities = &ExtractedEntities{
			Query: cmd.Query,
			Data:  cmd.Data,
		}

	case DSLCommandTypeIntent:
		// Intent routing - needs LLM classification
		// This case should actually trigger classification, not return directly
		result.TargetAgent = TargetUnknown
		result.Intent = IntentUnknown
		result.Domain = DomainUnknown
		result.Confidence = 0 // Needs classification
		result.Entities = &ExtractedEntities{
			Query: cmd.Query,
		}
	}

	return result
}

// ToEntities converts DSL params and data to extracted entities
func (cmd *DSLCommand) ToEntities() *ExtractedEntities {
	entities := &ExtractedEntities{}

	// Set query if present
	if cmd.Query != "" {
		entities.Query = cmd.Query
	}

	// Extract from params
	if cmd.Params != nil {
		if scope, ok := cmd.Params["scope"]; ok {
			entities.Scope = scope
		}
		if agent, ok := cmd.Params["agent"]; ok {
			entities.AgentName = agent
		}
		if agentID, ok := cmd.Params["agent_id"]; ok {
			entities.AgentID = agentID
		}
		if errorType, ok := cmd.Params["error"]; ok {
			entities.ErrorType = errorType
		}
		if timeframe, ok := cmd.Params["time"]; ok {
			entities.Timeframe = timeframe
		}
		if query, ok := cmd.Params["q"]; ok {
			entities.Query = query
		}
		if path, ok := cmd.Params["path"]; ok {
			entities.FilePaths = []string{path}
		}
		if paths, ok := cmd.Params["paths"]; ok {
			entities.FilePaths = strings.Split(paths, ",")
		}
	}

	// Extract from data
	if cmd.Data != nil {
		entities.Data = cmd.Data
	}

	return entities
}

// =============================================================================
// Helper Functions
// =============================================================================

// FormatDSLCommand formats a route result back to DSL
func FormatDSLCommand(result *RouteResult) string {
	var sb strings.Builder

	// Determine agent prefix
	agent := "arch"
	if result.TargetAgent == TargetGuide {
		agent = "guide"
	} else if result.TargetAgent != "" {
		agent = string(result.TargetAgent)
	}

	sb.WriteString("@")
	sb.WriteString(agent)
	sb.WriteString(":")
	sb.WriteString(string(result.Intent))
	sb.WriteString(":")
	sb.WriteString(string(result.Domain))

	// Add params if present
	if result.Entities != nil {
		var params []string

		if result.Entities.Scope != "" {
			params = append(params, "scope="+result.Entities.Scope)
		}
		if result.Entities.AgentName != "" {
			params = append(params, "agent="+result.Entities.AgentName)
		}
		if result.Entities.ErrorType != "" {
			params = append(params, "error="+result.Entities.ErrorType)
		}
		if result.Entities.Timeframe != "" {
			params = append(params, "time="+result.Entities.Timeframe)
		}
		if len(result.Entities.FilePaths) > 0 {
			params = append(params, "paths="+strings.Join(result.Entities.FilePaths, ","))
		}

		if len(params) > 0 {
			sb.WriteString("?")
			sb.WriteString(strings.Join(params, "&"))
		}

		// Add data if present
		if result.Entities.Data != nil {
			data, err := json.Marshal(result.Entities.Data)
			if err == nil {
				sb.WriteString(string(data))
			}
		}
	}

	return sb.String()
}

// FormatDirectTo formats a direct routing command
func FormatDirectTo(agent string, query string) string {
	if query != "" {
		return "@to:" + agent + " " + query
	}
	return "@to:" + agent
}

// FormatDirectFrom formats a response routing command
func FormatDirectFrom(agent string, response string) string {
	if response != "" {
		return "@from:" + agent + " " + response
	}
	return "@from:" + agent
}

// FormatGuideQuery formats an intent-based routing command
func FormatGuideQuery(query string) string {
	if query != "" {
		return "@guide " + query
	}
	return "@guide"
}

// FormatArchiveQuery formats an archive command
func FormatArchiveQuery(query string) string {
	if query != "" {
		return "@archive " + query
	}
	return "@archive"
}
