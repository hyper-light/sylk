// Package context provides AR.10.1: System Prompt Injector - injects agent-specific
// adaptive retrieval prompt additions into system prompts.
package context

import (
	"strings"
)

// =============================================================================
// Agent-Specific Prompt Additions
// =============================================================================

// LibrarianRetrievalPromptAddition provides guidance for Librarian agents on using
// the adaptive retrieval system.
const LibrarianRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

You have access to an adaptive retrieval system that learns from your behavior. The system
automatically prefetches relevant code before you see queries.

### Context Reference Markers
When you see [CTX-REF:...] markers, these represent evicted context. Example:
[CTX-REF:code_analysis | 15 turns (4,200 tokens) @ 14:23 | Topics: auth flow, JWT | retrieve_context(ref_id="abc123")]

To retrieve the full content: call retrieve_context(ref_id="abc123")

### Auto-Retrieved Content
Content marked [AUTO-RETRIEVED: file:lines | confidence: X.XX] was automatically fetched
based on the query. This content is highly relevant - use it directly.

Content marked [RELATED: file - description, N lines] indicates potentially relevant files.
Only retrieve if the auto-retrieved content is insufficient.

### Search Priority
1. Use prefetched [AUTO-RETRIEVED] content first
2. Use librarian_search_codebase for additional code searches
3. Use librarian_get_symbol_context for deep symbol exploration
4. Use search_history for historical context

### Hot Cache Promotion
When you repeatedly access the same files, call promote_to_hot(content_ids) to keep them
readily available. The system tracks access patterns and optimizes automatically.
`

// ArchivalistRetrievalPromptAddition provides guidance for Archivalist agents.
const ArchivalistRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Failure Pattern Memory
Before storing new failures, the system automatically checks for similar past failures.
You will see [FAILURE_PATTERN_WARNING] blocks when similar approaches have failed before.

CRITICAL: When you see recurrence_count >= 2, this is a RECURRING failure. You MUST:
1. Surface this warning to the requesting agent
2. Include the resolution if one exists
3. Track if the same approach is attempted anyway

### Cross-Session Learning
Use archivalist_get_cross_session_learnings to surface patterns across the user's history.
This is especially valuable for:
- Recurring mistakes (same error in different contexts)
- Successful patterns (approaches that worked before)
- User preferences (how they like things done)

### Decision Search
Use archivalist_search_decisions with outcome_filter to find:
- "successful": Decisions that worked well
- "failed": Decisions that caused problems
- "all": Both (for comprehensive analysis)

### Context Reference Markers
When you see [CTX-REF:...] markers in your context, retrieve the full content using
retrieve_context(ref_id="...") before responding to queries about that content.
`

// AcademicRetrievalPromptAddition provides guidance for Academic agents.
const AcademicRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Research Search
Use academic_search_research with appropriate filters:
- source_types: Filter by paper, rfc, documentation, etc.
- min_trust_level: Prefer "verified" or "high" for critical recommendations
- recency: Use "latest" for rapidly-evolving topics

### Applicability Analysis
When include_applicability=true, results include:
- DIRECT: Can be applied as-is to this codebase
- ADAPTABLE: Requires modifications for this codebase
- INCOMPATIBLE: Not suitable for this codebase

ALWAYS check applicability before recommending. Consult Librarian for codebase context.

### Source Citation
Use academic_cite_sources to validate claims before presenting them:
- Find at least 2 sources supporting critical claims
- Note any refuting sources
- Include trust levels in your response

### Best Practices Lookup
Use academic_get_best_practices with codebase_maturity:
- "auto": System asks Librarian for maturity assessment
- "greenfield": New project, flexible
- "legacy": Established patterns, careful changes
- "transitional": Moving between styles
- "disciplined": Strict standards
`

// GuideRetrievalPromptAddition provides guidance for Guide agents.
const GuideRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Prefetch Sharing
When routing queries to other agents, prefetched context is automatically shared.
The target agent receives [AUTO-RETRIEVED] content without additional latency.

### Routing History
Use guide_get_routing_history to maintain consistency:
- Check how similar queries were routed before
- Note which routings were successful vs failed
- Learn from routing mistakes

### User Preferences
The system learns user preferences automatically. Use guide_get_user_preferences to:
- Check preferred verbosity level
- See agent affinities (which agents for which topics)
- Understand response style preferences

### Context References
When forwarding queries that contain [CTX-REF:...] markers, the full content
will be automatically retrieved and included in the forwarded context.
`

// EngineerRetrievalPromptAddition provides guidance for Engineer agents.
const EngineerRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Limited Direct Retrieval
As a pipeline agent, you have LIMITED retrieval capabilities:
- get_file_context: Quick file lookups
- get_recent_changes: Recent modifications

For broader searches, consult knowledge agents:
- consult_librarian: Codebase patterns, file locations, symbols
- consult_archivalist: Past decisions, failure patterns
- consult_academic: Best practices, research

### Failure Pattern Warnings
When you see [FAILURE_PATTERN_WARNING] blocks, STOP and:
1. Read the warning carefully
2. If recurrence_count >= 2, this approach has failed REPEATEDLY
3. Consider the suggested resolution
4. If you proceed with the same approach anyway, document why

### Auto-Retrieved Context
You may see [AUTO-RETRIEVED] content injected by the system. This is
highly relevant to your current task - use it directly.
`

// OrchestratorRetrievalPromptAddition provides guidance for Orchestrator agents.
const OrchestratorRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Workflow Context
Use orchestrator_get_workflow_context to retrieve:
- Current workflow state and progress
- Past decisions and blockers
- Agent states and artifacts

### Similar Workflow Learning
Use orchestrator_search_similar_workflows BEFORE starting new workflows:
- Find past workflows with similar goals
- Learn from successful approaches
- Avoid approaches that failed

### Context Handoff
When performing Orchestrator handoff at 75% context:
- Workflow context is automatically preserved
- Agent states are captured and transferred
- New Orchestrator receives full context via handoff state
`

// PipelineRetrievalPromptAddition provides guidance for Pipeline agents
// (Designer, Inspector, Tester).
const PipelineRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Auto-Retrieved Context
You may see [AUTO-RETRIEVED] content injected by the system. This is
highly relevant to your current task - use it directly.

### Context Reference Markers
When you see [CTX-REF:...] markers, these represent evicted context.
If you need the full content, use retrieve_context(ref_id="...").

### Pipeline Context
Use pipeline skills to access implementation context, test results,
and inspection findings relevant to your current task.
`

// GenericRetrievalPromptAddition provides guidance for agents without specific additions.
const GenericRetrievalPromptAddition = `
## ADAPTIVE RETRIEVAL INTEGRATION

### Context Reference Markers
When you see [CTX-REF:...] markers, these represent evicted context. Example:
[CTX-REF:conversation | 15 turns (4,200 tokens) @ 14:23 | Topics: ... | retrieve_context(ref_id="abc123")]

To retrieve the full content: call retrieve_context(ref_id="abc123")

### Auto-Retrieved Content
Content marked [AUTO-RETRIEVED: ...] was automatically fetched based on the query.
This content is highly relevant - use it directly.

### Available Skills
- retrieve_context: Expand evicted context references
- search_history: Search historical content
- promote_to_hot: Promote frequently used content to hot cache
`

// =============================================================================
// Injection Marker
// =============================================================================

const (
	// InjectionMarker is used to detect if prompt was already injected.
	InjectionMarker = "## ADAPTIVE RETRIEVAL INTEGRATION"
)

// =============================================================================
// Prompt Injection
// =============================================================================

// InjectAdaptiveRetrievalPrompt injects the appropriate adaptive retrieval
// prompt addition into a system prompt based on agent type. The injection
// is idempotent - calling it multiple times won't duplicate content.
func InjectAdaptiveRetrievalPrompt(agentType, systemPrompt string) string {
	// Idempotency check - don't inject if already present
	if strings.Contains(systemPrompt, InjectionMarker) {
		return systemPrompt
	}

	addition := GetPromptAdditionForAgent(agentType)
	if addition == "" {
		return systemPrompt
	}

	// Append the addition at the end of the prompt
	return systemPrompt + "\n" + addition
}

// GetPromptAdditionForAgent returns the appropriate prompt addition for an agent type.
func GetPromptAdditionForAgent(agentType string) string {
	normalizedType := strings.ToLower(agentType)

	switch normalizedType {
	case "librarian":
		return LibrarianRetrievalPromptAddition
	case "archivalist":
		return ArchivalistRetrievalPromptAddition
	case "academic":
		return AcademicRetrievalPromptAddition
	case "guide":
		return GuideRetrievalPromptAddition
	case "engineer":
		return EngineerRetrievalPromptAddition
	case "orchestrator":
		return OrchestratorRetrievalPromptAddition
	case "designer", "inspector", "tester", "pipeline":
		return PipelineRetrievalPromptAddition
	default:
		return GenericRetrievalPromptAddition
	}
}

// HasAdaptiveRetrievalPrompt checks if a system prompt already contains
// the adaptive retrieval integration.
func HasAdaptiveRetrievalPrompt(systemPrompt string) bool {
	return strings.Contains(systemPrompt, InjectionMarker)
}

// RemoveAdaptiveRetrievalPrompt removes the adaptive retrieval addition from
// a system prompt. Returns the original prompt if no addition was found.
func RemoveAdaptiveRetrievalPrompt(systemPrompt string) string {
	idx := strings.Index(systemPrompt, InjectionMarker)
	if idx < 0 {
		return systemPrompt
	}

	// Find the start (including preceding newlines)
	start := idx
	for start > 0 && systemPrompt[start-1] == '\n' {
		start--
	}

	return strings.TrimRight(systemPrompt[:start], "\n")
}

// =============================================================================
// Agent Type Helpers
// =============================================================================

// agentPromptAdditions maps agent types to their prompt additions.
var agentPromptAdditions = map[string]string{
	"librarian":   LibrarianRetrievalPromptAddition,
	"archivalist": ArchivalistRetrievalPromptAddition,
	"academic":    AcademicRetrievalPromptAddition,
	"guide":       GuideRetrievalPromptAddition,
	"engineer":    EngineerRetrievalPromptAddition,
	"orchestrator": OrchestratorRetrievalPromptAddition,
	"designer":    PipelineRetrievalPromptAddition,
	"inspector":   PipelineRetrievalPromptAddition,
	"tester":      PipelineRetrievalPromptAddition,
	"pipeline":    PipelineRetrievalPromptAddition,
}

// GetAllPromptAdditions returns a map of all agent types to their prompt additions.
func GetAllPromptAdditions() map[string]string {
	result := make(map[string]string, len(agentPromptAdditions))
	for k, v := range agentPromptAdditions {
		result[k] = v
	}
	return result
}

// GetSupportedAgentTypes returns all agent types that have specific prompt additions.
func GetSupportedAgentTypes() []string {
	return []string{
		"librarian",
		"archivalist",
		"academic",
		"guide",
		"engineer",
		"orchestrator",
		"designer",
		"inspector",
		"tester",
		"pipeline",
	}
}
