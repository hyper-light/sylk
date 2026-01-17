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

	if err := p.validateDSLPrefix(input); err != nil {
		return nil, err
	}

	if cmd := p.parseShortcutCommands(input); cmd != nil {
		return cmd, nil
	}

	if cmd, err := p.parseFullDSLIfMatched(input); cmd != nil || err != nil {
		return cmd, err
	}

	return p.unrecognizedDSL(input)
}

func (p *Parser) parseShortcutCommands(input string) *DSLCommand {
	if cmd := p.parseDirectTo(input); cmd != nil {
		return cmd
	}
	if cmd := p.parseDirectFrom(input); cmd != nil {
		return cmd
	}
	if cmd := p.parseGuideIntent(input); cmd != nil {
		return cmd
	}
	return p.parseActionShortcut(input)
}

func (p *Parser) validateDSLPrefix(input string) error {
	if p.IsDSL(input) {
		return nil
	}
	return &DSLParseError{
		Input:    input,
		Position: 0,
		Expected: "DSL command starting with " + p.prefix,
		Got:      input,
		Message:  "input does not start with DSL prefix",
	}
}

func (p *Parser) parseDirectTo(input string) *DSLCommand {
	matches := p.directToPattern.FindStringSubmatch(input)
	if matches == nil {
		return nil
	}
	return &DSLCommand{
		Type:        DSLCommandTypeDirect,
		Direction:   DSLDirectionTo,
		TargetAgent: p.resolveAgent(matches[1]),
		Query:       strings.TrimSpace(matches[2]),
		Raw:         input,
	}
}

func (p *Parser) parseDirectFrom(input string) *DSLCommand {
	matches := p.directFromPattern.FindStringSubmatch(input)
	if matches == nil {
		return nil
	}
	return &DSLCommand{
		Type:        DSLCommandTypeDirect,
		Direction:   DSLDirectionFrom,
		TargetAgent: p.resolveAgent(matches[1]),
		Query:       strings.TrimSpace(matches[2]),
		Raw:         input,
	}
}

func (p *Parser) parseGuideIntent(input string) *DSLCommand {
	matches := p.guidePattern.FindStringSubmatch(input)
	if matches == nil {
		return nil
	}
	return &DSLCommand{
		Type:  DSLCommandTypeIntent,
		Query: strings.TrimSpace(matches[1]),
		Raw:   input,
	}
}

func (p *Parser) parseActionShortcut(input string) *DSLCommand {
	matches := p.actionPattern.FindStringSubmatch(input)
	if matches == nil {
		return nil
	}
	return p.buildActionShortcutCommand(matches, input)
}

func (p *Parser) buildActionShortcutCommand(matches []string, input string) *DSLCommand {
	action := strings.ToLower(matches[1])
	shortcut, agentID, ok := p.resolveActionShortcut(action)
	if !ok {
		return nil
	}
	cmd := &DSLCommand{
		Type:        DSLCommandTypeAction,
		TargetAgent: agentID,
		Query:       strings.TrimSpace(matches[2]),
		Raw:         input,
	}
	p.applyActionShortcutDefaults(cmd, shortcut)
	return cmd
}

func (p *Parser) applyActionShortcutDefaults(cmd *DSLCommand, shortcut *ActionShortcut) {
	if shortcut == nil {
		return
	}
	if shortcut.DefaultIntent != "" {
		cmd.Intent = shortcut.DefaultIntent
	}
	if shortcut.DefaultDomain != "" {
		cmd.Domain = shortcut.DefaultDomain
	}
}

func (p *Parser) parseFullDSLIfMatched(input string) (*DSLCommand, error) {
	matches := p.fullDSLPattern.FindStringSubmatch(input)
	if matches == nil {
		return nil, nil
	}
	return p.parseFullDSL(input, matches)
}

func (p *Parser) unrecognizedDSL(input string) (*DSLCommand, error) {
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
	agentStr, intentStr, domainStr := p.parseDSLParts(matches)
	paramsStr := matches[4]
	dataStr := matches[5]

	resolved, err := p.resolveFullDSL(input, agentStr, intentStr, domainStr)
	if err != nil {
		return nil, err
	}

	cmd := &DSLCommand{
		Type:        DSLCommandTypeFull,
		TargetAgent: resolved.agent,
		Intent:      resolved.intent,
		Domain:      resolved.domain,
		Raw:         input,
	}

	p.applyFullDSLParams(cmd, paramsStr)
	if err := p.applyFullDSLData(cmd, input, dataStr); err != nil {
		return nil, err
	}

	return cmd, nil
}

type resolvedFullDSL struct {
	agent  string
	intent Intent
	domain Domain
}

func (p *Parser) parseDSLParts(matches []string) (string, string, string) {
	agentStr := strings.ToLower(matches[1])
	intentStr := strings.ToLower(matches[2])
	domainStr := strings.ToLower(matches[3])
	return agentStr, intentStr, domainStr
}

func (p *Parser) resolveFullDSL(input string, agentStr string, intentStr string, domainStr string) (resolvedFullDSL, error) {
	resolved := resolvedFullDSL{}

	agent, err := p.resolveFullDSLAgent(input, agentStr)
	if err != nil {
		return resolved, err
	}
	resolved.agent = agent

	intent, err := p.resolveFullDSLIntent(input, agentStr, intentStr)
	if err != nil {
		return resolved, err
	}
	resolved.intent = intent

	domain, err := p.resolveFullDSLDomain(input, agentStr, intentStr, domainStr)
	if err != nil {
		return resolved, err
	}
	resolved.domain = domain

	return resolved, nil
}

func (p *Parser) resolveFullDSLAgent(input string, agentStr string) (string, error) {
	agent, ok := p.agentShortcuts[agentStr]
	if ok {
		return agent, nil
	}

	return "", &DSLParseError{
		Input:    input,
		Position: len(p.prefix),
		Expected: "valid agent (arch, guide)",
		Got:      agentStr,
		Message:  "unknown agent: " + agentStr,
	}
}

func (p *Parser) resolveFullDSLIntent(input string, agentStr string, intentStr string) (Intent, error) {
	intent, ok := p.intentShortcuts[intentStr]
	if ok {
		return intent, nil
	}

	return "", &DSLParseError{
		Input:    input,
		Position: len(p.prefix) + len(agentStr) + 1,
		Expected: "valid intent (recall, store, check, declare, complete, help, status)",
		Got:      intentStr,
		Message:  "unknown intent: " + intentStr,
	}
}

func (p *Parser) resolveFullDSLDomain(input string, agentStr string, intentStr string, domainStr string) (Domain, error) {
	domain, ok := p.domainShortcuts[domainStr]
	if ok {
		return domain, nil
	}

	return "", &DSLParseError{
		Input:    input,
		Position: len(p.prefix) + len(agentStr) + len(intentStr) + 2,
		Expected: "valid domain (patterns, failures, decisions, files, learnings, system, agents)",
		Got:      domainStr,
		Message:  "unknown domain: " + domainStr,
	}
}

func (p *Parser) applyFullDSLParams(cmd *DSLCommand, paramsStr string) {
	if paramsStr == "" {
		return
	}
	cmd.Params = p.parseParams(paramsStr)
}

func (p *Parser) applyFullDSLData(cmd *DSLCommand, input string, dataStr string) error {
	if dataStr == "" {
		return nil
	}

	data, err := p.parseData(dataStr)
	if err != nil {
		return &DSLParseError{
			Input:    input,
			Position: strings.Index(input, "{"),
			Expected: "valid JSON object",
			Got:      dataStr,
			Message:  "invalid JSON data: " + err.Error(),
		}
	}
	cmd.Data = data
	return nil
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
	parts := p.splitCommands(input)
	commands := make([]*DSLCommand, 0, len(parts))
	var errors []error

	for _, part := range parts {
		cmd, err := p.Parse(part)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		commands = append(commands, cmd)
	}

	return commands, errors
}

func (p *Parser) splitCommands(input string) []string {
	input = strings.TrimSpace(input)
	parts := strings.Split(input, ";")

	commands := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		commands = append(commands, part)
	}

	return commands
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
		cmd.applyFullDSL(result)
	case DSLCommandTypeDirect, DSLCommandTypeAction:
		cmd.applyDirectDSL(result)
	case DSLCommandTypeIntent:
		cmd.applyIntentDSL(result)
	}

	return result
}

func (cmd *DSLCommand) applyFullDSL(result *RouteResult) {
	result.Intent = cmd.Intent
	result.Domain = cmd.Domain
	result.TargetAgent = TargetAgent(cmd.TargetAgent)
	result.TemporalFocus = cmd.temporalFocus()
	result.Entities = cmd.ToEntities()
}

func (cmd *DSLCommand) temporalFocus() TemporalFocus {
	if cmd.Domain.IsHistoricalDomain() {
		return TemporalPast
	}
	return TemporalPresent
}

func (cmd *DSLCommand) applyDirectDSL(result *RouteResult) {
	result.TargetAgent = TargetAgent(cmd.TargetAgent)
	result.Intent = IntentUnknown
	result.Domain = DomainUnknown
	result.Entities = &ExtractedEntities{
		Query: cmd.Query,
		Data:  cmd.Data,
	}
}

func (cmd *DSLCommand) applyIntentDSL(result *RouteResult) {
	result.TargetAgent = TargetUnknown
	result.Intent = IntentUnknown
	result.Domain = DomainUnknown
	result.Confidence = 0
	result.Entities = &ExtractedEntities{
		Query: cmd.Query,
	}
}

// ToEntities converts DSL params and data to extracted entities
func (cmd *DSLCommand) ToEntities() *ExtractedEntities {
	entities := &ExtractedEntities{}
	cmd.assignQuery(entities)
	cmd.assignParams(entities)
	cmd.assignData(entities)
	return entities
}

func (cmd *DSLCommand) assignQuery(entities *ExtractedEntities) {
	if cmd.Query != "" {
		entities.Query = cmd.Query
	}
}

func (cmd *DSLCommand) assignParams(entities *ExtractedEntities) {
	if cmd.Params == nil {
		return
	}
	cmd.applyParamAssignments(entities)
}

func (cmd *DSLCommand) applyParamAssignments(entities *ExtractedEntities) {
	cmd.assignParamScope(entities)
	cmd.assignParamAgent(entities)
	cmd.assignParamAgentID(entities)
	cmd.assignParamErrorType(entities)
	cmd.assignParamTimeframe(entities)
	cmd.assignParamQuery(entities)
	cmd.assignParamPath(entities)
	cmd.assignParamPaths(entities)
}

func (cmd *DSLCommand) assignParamScope(entities *ExtractedEntities) {
	if scope, ok := cmd.Params["scope"]; ok {
		entities.Scope = scope
	}
}

func (cmd *DSLCommand) assignParamAgent(entities *ExtractedEntities) {
	if agent, ok := cmd.Params["agent"]; ok {
		entities.AgentName = agent
	}
}

func (cmd *DSLCommand) assignParamAgentID(entities *ExtractedEntities) {
	if agentID, ok := cmd.Params["agent_id"]; ok {
		entities.AgentID = agentID
	}
}

func (cmd *DSLCommand) assignParamErrorType(entities *ExtractedEntities) {
	if errorType, ok := cmd.Params["error"]; ok {
		entities.ErrorType = errorType
	}
}

func (cmd *DSLCommand) assignParamTimeframe(entities *ExtractedEntities) {
	if timeframe, ok := cmd.Params["time"]; ok {
		entities.Timeframe = timeframe
	}
}

func (cmd *DSLCommand) assignParamQuery(entities *ExtractedEntities) {
	if query, ok := cmd.Params["q"]; ok {
		entities.Query = query
	}
}

func (cmd *DSLCommand) assignParamPath(entities *ExtractedEntities) {
	if path, ok := cmd.Params["path"]; ok {
		entities.FilePaths = []string{path}
	}
}

func (cmd *DSLCommand) assignParamPaths(entities *ExtractedEntities) {
	if paths, ok := cmd.Params["paths"]; ok {
		entities.FilePaths = strings.Split(paths, ",")
	}
}

func (cmd *DSLCommand) assignData(entities *ExtractedEntities) {
	if cmd.Data != nil {
		entities.Data = cmd.Data
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// FormatDSLCommand formats a route result back to DSL
func FormatDSLCommand(result *RouteResult) string {
	var sb strings.Builder
	agent := formatAgentPrefix(result.TargetAgent)
	formatDSLHeader(&sb, agent, result)
	formatDSLEntities(&sb, result.Entities)
	return sb.String()
}

func formatAgentPrefix(target TargetAgent) string {
	if target == TargetGuide {
		return "guide"
	}
	if target != "" {
		return string(target)
	}
	return "arch"
}

func formatDSLHeader(sb *strings.Builder, agent string, result *RouteResult) {
	sb.WriteString("@")
	sb.WriteString(agent)
	sb.WriteString(":")
	sb.WriteString(string(result.Intent))
	sb.WriteString(":")
	sb.WriteString(string(result.Domain))
}

func formatDSLEntities(sb *strings.Builder, entities *ExtractedEntities) {
	if entities == nil {
		return
	}
	params := formatDSLParams(entities)
	appendDSLParams(sb, params)
	appendDSLData(sb, entities.Data)
}

func formatDSLParams(entities *ExtractedEntities) []string {
	params := make([]string, 0)
	params = appendParam(params, "scope", entities.Scope)
	params = appendParam(params, "agent", entities.AgentName)
	params = appendParam(params, "error", entities.ErrorType)
	params = appendParam(params, "time", entities.Timeframe)
	if len(entities.FilePaths) > 0 {
		params = append(params, "paths="+strings.Join(entities.FilePaths, ","))
	}
	return params
}

func appendParam(params []string, key string, value string) []string {
	if value == "" {
		return params
	}
	return append(params, key+"="+value)
}

func appendDSLParams(sb *strings.Builder, params []string) {
	if len(params) == 0 {
		return
	}
	sb.WriteString("?")
	sb.WriteString(strings.Join(params, "&"))
}

func appendDSLData(sb *strings.Builder, data map[string]any) {
	if data == nil {
		return
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return
	}
	sb.WriteString(string(encoded))
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
