package guide

import (
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

type DirectAddressResult struct {
	IsDirectAddress bool
	TargetAgent     concurrency.AgentType
	TargetDomain    domain.Domain
	MatchedPattern  string
	CleanedQuery    string
	Confidence      float64
}

type DirectAddressDetector struct {
	atMentionPattern      *regexp.Regexp
	greetingPattern       *regexp.Regexp
	agentAliases          map[string]concurrency.AgentType
	caseSensitive         bool
	stripAddressFromQuery bool
}

type DirectAddressConfig struct {
	CaseSensitive         bool
	StripAddressFromQuery bool
	CustomAliases         map[string]concurrency.AgentType
}

func NewDirectAddressDetector(config *DirectAddressConfig) *DirectAddressDetector {
	d := &DirectAddressDetector{
		atMentionPattern:      regexp.MustCompile(`(?i)^@(\w+)\s*`),
		greetingPattern:       regexp.MustCompile(`(?i)^(?:hey|hi|hello|yo)\s+(\w+)[,:]?\s*`),
		agentAliases:          buildDefaultAliases(),
		caseSensitive:         false,
		stripAddressFromQuery: true,
	}

	if config != nil {
		d.caseSensitive = config.CaseSensitive
		d.stripAddressFromQuery = config.StripAddressFromQuery
		d.mergeAliases(config.CustomAliases)
	}

	return d
}

func buildDefaultAliases() map[string]concurrency.AgentType {
	return map[string]concurrency.AgentType{
		"librarian":    concurrency.AgentLibrarian,
		"lib":          concurrency.AgentLibrarian,
		"academic":     concurrency.AgentAcademic,
		"prof":         concurrency.AgentAcademic,
		"professor":    concurrency.AgentAcademic,
		"archivalist":  concurrency.AgentArchivalist,
		"archivist":    concurrency.AgentArchivalist,
		"archive":      concurrency.AgentArchivalist,
		"architect":    concurrency.AgentArchitect,
		"arch":         concurrency.AgentArchitect,
		"engineer":     "engineer",
		"eng":          "engineer",
		"designer":     "designer",
		"design":       "designer",
		"inspector":    "inspector",
		"inspect":      "inspector",
		"tester":       "tester",
		"test":         "tester",
		"orchestrator": concurrency.AgentOrchestrator,
		"orch":         concurrency.AgentOrchestrator,
		"guide":        concurrency.AgentGuide,
	}
}

func (d *DirectAddressDetector) mergeAliases(custom map[string]concurrency.AgentType) {
	for alias, agent := range custom {
		d.agentAliases[alias] = agent
	}
}

func (d *DirectAddressDetector) Detect(query string) *DirectAddressResult {
	result := &DirectAddressResult{
		CleanedQuery: query,
	}

	if atResult := d.detectAtMention(query); atResult.IsDirectAddress {
		return atResult
	}

	if greetResult := d.detectGreeting(query); greetResult.IsDirectAddress {
		return greetResult
	}

	return result
}

func (d *DirectAddressDetector) detectAtMention(query string) *DirectAddressResult {
	matches := d.atMentionPattern.FindStringSubmatch(query)
	if matches == nil {
		return &DirectAddressResult{CleanedQuery: query}
	}

	agentName := d.normalizeAgentName(matches[1])
	agent, ok := d.agentAliases[agentName]
	if !ok {
		return &DirectAddressResult{CleanedQuery: query}
	}

	return d.buildResult(query, matches[0], agent, 1.0)
}

func (d *DirectAddressDetector) detectGreeting(query string) *DirectAddressResult {
	matches := d.greetingPattern.FindStringSubmatch(query)
	if matches == nil {
		return &DirectAddressResult{CleanedQuery: query}
	}

	agentName := d.normalizeAgentName(matches[1])
	agent, ok := d.agentAliases[agentName]
	if !ok {
		return &DirectAddressResult{CleanedQuery: query}
	}

	return d.buildResult(query, matches[0], agent, 0.9)
}

func (d *DirectAddressDetector) buildResult(
	query string,
	matched string,
	agent concurrency.AgentType,
	confidence float64,
) *DirectAddressResult {
	cleanedQuery := query
	if d.stripAddressFromQuery {
		cleanedQuery = strings.TrimSpace(query[len(matched):])
	}

	targetDomain, _ := domain.DomainFromAgentType(agent)

	return &DirectAddressResult{
		IsDirectAddress: true,
		TargetAgent:     agent,
		TargetDomain:    targetDomain,
		MatchedPattern:  matched,
		CleanedQuery:    cleanedQuery,
		Confidence:      confidence,
	}
}

func (d *DirectAddressDetector) normalizeAgentName(name string) string {
	if d.caseSensitive {
		return name
	}
	return strings.ToLower(name)
}

func (d *DirectAddressDetector) IsDirectAddress(query string) bool {
	return d.Detect(query).IsDirectAddress
}

func (d *DirectAddressDetector) GetTargetAgent(query string) (concurrency.AgentType, bool) {
	result := d.Detect(query)
	if !result.IsDirectAddress {
		return "", false
	}
	return result.TargetAgent, true
}

func (d *DirectAddressDetector) AddAlias(alias string, agent concurrency.AgentType) {
	normalized := d.normalizeAgentName(alias)
	d.agentAliases[normalized] = agent
}

func (d *DirectAddressDetector) RemoveAlias(alias string) {
	normalized := d.normalizeAgentName(alias)
	delete(d.agentAliases, normalized)
}

func (d *DirectAddressDetector) HasAlias(alias string) bool {
	normalized := d.normalizeAgentName(alias)
	_, ok := d.agentAliases[normalized]
	return ok
}

func (d *DirectAddressDetector) ListAliases() map[string]concurrency.AgentType {
	result := make(map[string]concurrency.AgentType, len(d.agentAliases))
	for k, v := range d.agentAliases {
		result[k] = v
	}
	return result
}
