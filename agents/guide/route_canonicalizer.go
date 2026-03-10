package guide

import (
	"fmt"
	"sort"
	"strings"
)

type routeCanonicalizationFeatures struct {
	query      string
	question   bool
	approval   bool
	status     bool
	health     bool
	workflow   bool
	tasks      bool
	design     bool
	code       bool
	files      bool
	research   bool
	fetch      bool
	history    bool
	patterns   bool
	failures   bool
	decisions  bool
	learnings  bool
	intents    bool
	testing    bool
	compliance bool
	security   bool
}

func extractRouteCanonicalizationFeatures(input string) routeCanonicalizationFeatures {
	query := normalizeGuideQuery(input)
	return routeCanonicalizationFeatures{
		query:      query,
		question:   strings.Contains(input, "?") || containsAny(query, "how ", "what ", "why ", "can you", "could you", "would you", "help"),
		approval:   containsAny(query, "go ahead", "ship it", "proceed", "approved", "lgtm", "execute", "run the plan", "kick it off"),
		status:     containsAny(query, "status", "progress", "running", "state", "healthy", "health", "budget", "tokens"),
		health:     containsAny(query, "health", "healthy", "unhealthy", "degraded", "budget", "tokens", "cost", "anomaly", "timeout"),
		workflow:   containsAny(query, "workflow", "pipeline", "dag", "orchestrator", "execution"),
		tasks:      containsAny(query, "task", "tasks", "break down", "decompose", "backlog", "work item", "handoff"),
		design:     containsAny(query, "plan", "design", "architecture", "architect", "structure", "implement", "build", "create"),
		code:       containsAny(query, "code", "function", "class", "method", "symbol", "repo", "repository", "package", "source"),
		files:      containsAny(query, "file", "path", "filename", "directory", "folder"),
		research:   containsAny(query, "research", "best practice", "best practices", "standard", "documentation", "paper", "oauth", "compare"),
		fetch:      containsAny(query, "fetch", "download", "clone", "crawl", "url", "document", "web"),
		history:    containsAny(query, "history", "last time", "previous", "before", "what happened", "why did we", "how did we"),
		patterns:   containsAny(query, "pattern", "convention", "idiom", "recurring"),
		failures:   containsAny(query, "failure", "failed", "regression", "bug", "incident", "outage", "error"),
		decisions:  containsAny(query, "decision", "decide", "rationale", "trade-off", "tradeoff", "why did we"),
		learnings:  containsAny(query, "learning", "learnings", "lesson", "lessons", "insight"),
		intents:    containsAny(query, "intent", "wip", "work in progress", "working on", "in progress"),
		testing:    containsAny(query, "test", "testing", "qa", "coverage", "integration", "e2e", "unit test"),
		compliance: containsAny(query, "compliance", "requirement", "requirements", "review", "audit", "lint", "verify", "validation"),
		security:   containsAny(query, "security", "credential", "injection", "protect", "rollback", "safety"),
	}
}

func (g *Guide) canonicalizeClassificationForTarget(
	input string,
	classification *RouteResult,
	targetAgentID string,
) *RouteResult {
	if classification == nil || strings.TrimSpace(targetAgentID) == "" || g.isGuideTarget(targetAgentID) {
		return classification
	}
	agent := g.resolveAgent(targetAgentID)
	if agent == nil {
		return classification
	}
	normalized := *classification
	normalized.TargetAgent = TargetAgent(targetAgentID)
	if agent.Accepts(&normalized) {
		return &normalized
	}

	features := extractRouteCanonicalizationFeatures(input)
	intentCandidates := canonicalIntentCandidates(agent, &normalized)
	domainCandidates := canonicalDomainCandidates(agent, &normalized)

	best := &normalized
	bestScore := -1
	for _, intent := range intentCandidates {
		for _, domain := range domainCandidates {
			candidate := normalized
			candidate.Intent = intent
			candidate.Domain = domain
			candidate.TargetAgent = TargetAgent(targetAgentID)
			if !agent.Accepts(&candidate) {
				continue
			}
			score := canonicalizationScore(&normalized, &candidate, agent, features)
			if score > bestScore {
				bestScore = score
				best = &candidate
			}
		}
	}
	if bestScore < 0 {
		return &normalized
	}
	if best.Intent == normalized.Intent && best.Domain == normalized.Domain {
		return best
	}

	promoted := *best
	promoted.ClassificationMethod = appendClassificationMethod(promoted.ClassificationMethod, "target_canonicalized")
	promoted.Reason = canonicalizationReason(normalized.Intent, normalized.Domain, promoted.Intent, promoted.Domain, targetAgentID)
	return &promoted
}

func canonicalizationScore(
	current *RouteResult,
	candidate *RouteResult,
	agent *AgentRegistration,
	features routeCanonicalizationFeatures,
) int {
	if current == nil || candidate == nil || agent == nil {
		return 0
	}
	score := canonicalIntentScore(current, candidate.Intent, agent, features)
	score += canonicalDomainScore(current.Domain, candidate.Domain, candidate.Intent, agent, features)
	return score
}

func canonicalIntentScore(
	current *RouteResult,
	candidate Intent,
	agent *AgentRegistration,
	features routeCanonicalizationFeatures,
) int {
	if current == nil {
		return 0
	}
	score := intentTransitionScore(current.Intent, candidate)
	if allowProtectedIntentFallback(current.ClassificationMethod) && protectConversationalIntent(current.Intent) {
		switch {
		case features.design && (candidate == IntentPlan || candidate == IntentDesign):
			score += 132
		case (features.code || features.files) && (candidate == IntentFind || candidate == IntentSearch || candidate == IntentLocate || candidate == IntentFetch):
			score += 118
		case features.research && (candidate == IntentRecall || candidate == IntentFetch):
			score += 116
		case (features.testing || features.compliance) && candidate == IntentCheck:
			score += 108
		case features.status && candidate == IntentStatus:
			score += 84
		}
	}
	if features.question && candidate == IntentHelp {
		score += 18
	}
	if features.research && candidate == IntentRecall {
		score += 18
	}
	if features.fetch && candidate == IntentFetch {
		score += 24
	}
	if features.status && candidate == IntentStatus {
		score += 26
	}
	if features.testing && candidate == IntentCheck {
		score += 18
	}
	if features.compliance && candidate == IntentCheck {
		score += 14
	}
	if features.approval && (candidate == IntentExecute || candidate == IntentComplete) {
		score += 26
	}
	if features.design && (candidate == IntentDesign || candidate == IntentPlan) {
		score += 16
	}
	score += intentPreferenceScore(agent, candidate)
	return score
}

func intentTransitionScore(current Intent, candidate Intent) int {
	if current == candidate {
		return 140
	}
	switch current {
	case IntentChat:
		switch candidate {
		case IntentHelp:
			return 118
		case IntentStatus:
			return 95
		case IntentChat:
			return 140
		}
	case IntentHelp:
		switch candidate {
		case IntentChat:
			return 100
		case IntentStatus:
			return 92
		case IntentHelp:
			return 140
		}
	case IntentStatus:
		switch candidate {
		case IntentCheck:
			return 118
		case IntentHelp:
			return 94
		case IntentRecall:
			return 88
		}
	case IntentUnknown:
		switch candidate {
		case IntentHelp:
			return 110
		case IntentStatus:
			return 102
		case IntentRecall, IntentCheck:
			return 92
		case IntentChat:
			return 84
		}
	case IntentPlan:
		switch candidate {
		case IntentDesign:
			return 124
		case IntentExecute:
			return 112
		case IntentHelp:
			return 106
		case IntentRecall:
			return 96
		case IntentCheck:
			return 90
		}
	case IntentDesign:
		switch candidate {
		case IntentPlan:
			return 124
		case IntentHelp:
			return 110
		case IntentRecall:
			return 98
		case IntentCheck:
			return 92
		}
	case IntentExecute:
		switch candidate {
		case IntentComplete:
			return 122
		case IntentPlan:
			return 110
		case IntentStatus:
			return 96
		case IntentHelp:
			return 88
		}
	case IntentRecall:
		switch candidate {
		case IntentCheck:
			return 120
		case IntentFetch:
			return 114
		case IntentHelp:
			return 96
		case IntentFind, IntentSearch, IntentLocate:
			return 88
		}
	case IntentCheck:
		switch candidate {
		case IntentRecall:
			return 120
		case IntentStatus:
			return 112
		case IntentHelp:
			return 96
		case IntentFind, IntentSearch, IntentLocate:
			return 88
		}
	case IntentFetch:
		switch candidate {
		case IntentRecall:
			return 116
		case IntentSearch, IntentFind, IntentLocate:
			return 110
		case IntentHelp:
			return 92
		case IntentCheck:
			return 88
		}
	case IntentFind, IntentSearch, IntentLocate:
		switch candidate {
		case IntentFind, IntentSearch, IntentLocate:
			return 124
		case IntentFetch:
			return 110
		case IntentRecall, IntentCheck:
			return 92
		case IntentHelp:
			return 84
		}
	case IntentStore:
		switch candidate {
		case IntentDeclare, IntentComplete:
			return 112
		case IntentHelp, IntentCheck, IntentRecall:
			return 72
		}
	case IntentDeclare:
		switch candidate {
		case IntentStore, IntentComplete:
			return 118
		case IntentHelp:
			return 72
		}
	case IntentComplete:
		switch candidate {
		case IntentDeclare, IntentExecute:
			return 118
		case IntentStatus:
			return 90
		case IntentHelp:
			return 72
		}
	}
	return 0
}

func canonicalDomainScore(
	current Domain,
	candidate Domain,
	intent Intent,
	agent *AgentRegistration,
	features routeCanonicalizationFeatures,
) int {
	score := 0
	if current == candidate {
		score += 140
	} else {
		if routeDomainFamily(current) != "" && routeDomainFamily(current) == routeDomainFamily(candidate) {
			score += 114
		}
		switch current {
		case DomainPlanning:
			if candidate == DomainDesign || candidate == DomainTasks || candidate == DomainWorkflow {
				score += 110
			}
		case DomainLocal:
			if candidate == DomainCode || candidate == DomainFiles {
				score += 110
			}
		case DomainHistory:
			if isHistoryDomain(candidate) {
				score += 110
			}
		case DomainSystem:
			if candidate == DomainWorkflow || candidate == DomainHealth || candidate == DomainTasks {
				score += 108
			}
		case DomainTesting:
			if candidate == DomainCode {
				score += 104
			}
		case DomainCompliance:
			if candidate == DomainCode || candidate == DomainSystem {
				score += 100
			}
		case DomainGeneral, DomainUnknown:
			score += 18
		}
	}
	score += domainKeywordScore(candidate, intent, features)
	score += domainPreferenceScore(agent, candidate)
	return score
}

func domainKeywordScore(candidate Domain, intent Intent, features routeCanonicalizationFeatures) int {
	score := 0
	switch candidate {
	case DomainCode:
		if features.code {
			score += 20
		}
		if features.testing || features.compliance {
			score += 10
		}
	case DomainFiles:
		if features.files {
			score += 26
		}
	case DomainDesign:
		if features.design {
			score += 24
		}
		if intent == IntentPlan || intent == IntentDesign {
			score += 12
		}
	case DomainTasks:
		if features.tasks || features.workflow || features.approval {
			score += 24
		}
		if intent == IntentExecute {
			score += 18
		}
	case DomainWorkflow:
		if features.workflow || features.status {
			score += 26
		}
	case DomainHealth:
		if features.health {
			score += 30
		}
	case DomainSystem:
		if features.status || features.health {
			score += 18
		}
	case DomainResearch:
		if features.research {
			score += 24
		}
	case DomainCompliance:
		if features.compliance || features.security {
			score += 24
		}
	case DomainPatterns:
		if features.patterns {
			score += 26
		} else if features.history {
			score += 14
		}
	case DomainFailures:
		if features.failures {
			score += 30
		} else if features.history {
			score += 14
		}
	case DomainDecisions:
		if features.decisions {
			score += 32
		} else if features.history {
			score += 14
		}
	case DomainLearnings:
		if features.learnings {
			score += 28
		} else if features.history {
			score += 14
		}
	case DomainIntents:
		if features.intents {
			score += 28
		} else if features.history {
			score += 14
		}
	}
	return score
}

func canonicalIntentCandidates(agent *AgentRegistration, classification *RouteResult) []Intent {
	supported := supportedIntentsForRegistration(agent)
	if classification == nil {
		return supported
	}
	current := classification.Intent
	if !protectConversationalIntent(current) {
		return appendIntentUnique([]Intent{current}, supported...)
	}
	allowed := []Intent{current, IntentHelp, IntentChat, IntentStatus}
	if current == IntentUnknown || allowProtectedIntentFallback(classification.ClassificationMethod) {
		allowed = append(allowed, IntentRecall, IntentCheck)
	}
	if allowProtectedIntentFallback(classification.ClassificationMethod) {
		allowed = append(allowed, supported...)
	}
	if len(agent.Capabilities.Intents) == 0 {
		return appendIntentUnique(nil, allowed...)
	}
	result := make([]Intent, 0, len(allowed))
	for _, candidate := range appendIntentUnique(nil, allowed...) {
		if agent.Capabilities.SupportsIntent(candidate) {
			result = append(result, candidate)
		}
	}
	return result
}

func canonicalDomainCandidates(agent *AgentRegistration, classification *RouteResult) []Domain {
	supported := supportedDomainsForRegistration(agent)
	if classification == nil {
		return supported
	}
	return appendDomainUnique([]Domain{classification.Domain}, supported...)
}

func supportedIntentsForRegistration(agent *AgentRegistration) []Intent {
	if agent == nil || len(agent.Capabilities.Intents) == 0 {
		return AllIntents()
	}
	return append([]Intent(nil), agent.Capabilities.Intents...)
}

func supportedDomainsForRegistration(agent *AgentRegistration) []Domain {
	if agent == nil || len(agent.Capabilities.Domains) == 0 {
		return AllDomains()
	}
	return append([]Domain(nil), agent.Capabilities.Domains...)
}

func protectConversationalIntent(intent Intent) bool {
	switch intent {
	case IntentChat, IntentHelp, IntentStatus, IntentUnknown:
		return true
	default:
		return false
	}
}

func allowProtectedIntentFallback(method string) bool {
	return hasClassificationMethodToken(method, "session_flow") ||
		hasClassificationMethodToken(method, "work_state") ||
		hasClassificationMethodToken(method, "conversation_fast_path") ||
		hasClassificationMethodToken(method, "explicit")
}

func defaultRegisteredIntent(agent *AgentRegistration) Intent {
	if agent == nil || len(agent.Capabilities.Intents) == 0 {
		return IntentHelp
	}
	return agent.Capabilities.Intents[0]
}

func intentPreferenceScore(agent *AgentRegistration, candidate Intent) int {
	if agent == nil {
		return 0
	}
	for idx, value := range agent.Capabilities.Intents {
		if value == candidate {
			return len(agent.Capabilities.Intents) - idx
		}
	}
	return 0
}

func domainPreferenceScore(agent *AgentRegistration, candidate Domain) int {
	if agent == nil {
		return 0
	}
	for idx, value := range agent.Capabilities.Domains {
		if value == candidate {
			return len(agent.Capabilities.Domains) - idx
		}
	}
	return 0
}

func appendIntentUnique(base []Intent, values ...Intent) []Intent {
	seen := make(map[Intent]struct{}, len(base)+len(values))
	result := make([]Intent, 0, len(base)+len(values))
	for _, value := range base {
		if value == "" || value == IntentUnknown {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, value := range values {
		if value == "" || value == IntentUnknown {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func appendDomainUnique(base []Domain, values ...Domain) []Domain {
	seen := make(map[Domain]struct{}, len(base)+len(values))
	result := make([]Domain, 0, len(base)+len(values))
	for _, value := range base {
		if value == "" || value == DomainUnknown {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, value := range values {
		if value == "" || value == DomainUnknown {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func routeDomainFamily(domain Domain) string {
	switch domain {
	case DomainLocal, DomainCode, DomainFiles:
		return "local"
	case DomainHistory, DomainPatterns, DomainFailures, DomainDecisions, DomainLearnings, DomainIntents:
		return "history"
	case DomainResearch:
		return "research"
	case DomainPlanning, DomainDesign, DomainTasks:
		return "planning"
	case DomainSystem, DomainWorkflow, DomainHealth:
		return "system"
	case DomainCompliance:
		return "compliance"
	case DomainTesting:
		return "testing"
	case DomainGeneral:
		return "general"
	default:
		return ""
	}
}

func isHistoryDomain(domain Domain) bool {
	switch domain {
	case DomainHistory, DomainPatterns, DomainFailures, DomainDecisions, DomainLearnings, DomainIntents:
		return true
	default:
		return false
	}
}

func canonicalizationReason(
	fromIntent Intent,
	fromDomain Domain,
	toIntent Intent,
	toDomain Domain,
	targetAgentID string,
) string {
	if fromIntent == toIntent && fromDomain == toDomain {
		return ""
	}
	return fmt.Sprintf(
		"canonicalized %s/%s to %s/%s for %s",
		fromIntent,
		fromDomain,
		toIntent,
		toDomain,
		targetAgentID,
	)
}

func (g *Guide) classificationCapabilitySummary() string {
	if g == nil || g.registry == nil {
		return ""
	}
	regs := g.registry.GetAll()
	if len(regs) == 0 {
		return ""
	}

	type capabilityLine struct {
		name string
		text string
	}
	lines := make([]capabilityLine, 0, len(regs))
	seen := map[string]struct{}{}
	for _, reg := range regs {
		if reg == nil {
			continue
		}
		name := classificationCapabilityName(reg)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		lines = append(lines, capabilityLine{
			name: name,
			text: fmt.Sprintf("- %s: intents=%s; domains=%s",
				name,
				formatCapabilityIntentList(reg.Capabilities.Intents),
				formatCapabilityDomainList(reg.Capabilities.Domains),
			),
		})
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].name < lines[j].name
	})

	out := []string{
		"## Live Agent Capability Map",
		"These live registry constraints are authoritative.",
		"Always emit a target_agent, intent, and domain that the target supports.",
	}
	for _, line := range lines {
		out = append(out, line.text)
	}
	return strings.Join(out, "\n")
}

func classificationCapabilityName(reg *AgentRegistration) string {
	if reg == nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(reg.Name))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(reg.ID))
	}
	return name
}

func formatCapabilityIntentList(intents []Intent) string {
	if len(intents) == 0 {
		return "any"
	}
	items := make([]string, 0, len(intents))
	for _, intent := range intents {
		items = append(items, string(intent))
	}
	return strings.Join(items, ",")
}

func formatCapabilityDomainList(domains []Domain) string {
	if len(domains) == 0 {
		return "any"
	}
	items := make([]string, 0, len(domains))
	for _, domain := range domains {
		items = append(items, string(domain))
	}
	return strings.Join(items, ",")
}

func hasClassificationMethodToken(method string, token string) bool {
	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" {
		return false
	}
	for _, part := range strings.Split(strings.TrimSpace(method), "+") {
		if strings.TrimSpace(part) == trimmedToken {
			return true
		}
	}
	return false
}
