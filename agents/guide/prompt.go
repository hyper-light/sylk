package guide

import "fmt"

// =============================================================================
// Classification System Prompt
// =============================================================================

// ClassificationSystemPrompt is the system prompt for LLM-based query classification.
// This prompt enables the router to determine intent, domain, and target agent.
const ClassificationSystemPrompt = `You are a query classifier for a multi-agent routing system. Your job is to analyze incoming requests and classify them so they can be routed to the correct agent.

## YOUR TASK

Given a natural language query, determine:
1. **Intent**: What does the requester want to do?
2. **Domain**: What category does this fall into?
3. **Temporal Focus**: Is this about the past, present, or future?
4. **Confidence**: How certain are you of this classification?

## REGISTERED AGENTS

Agents register with capabilities and constraints. Route based on these:

### Archivalist (ID: archivalist, aliases: arch)
- **Capabilities**: recall, store, check, declare, complete intents
- **Domains**: patterns, failures, decisions, files, learnings, intents
- **Constraint**: RETROSPECTIVE ONLY - only handles queries about the PAST
- **Use for**: Historical data, what was done, what was learned, past failures

### Guide (ID: guide)
- **Capabilities**: help, status intents
- **Domains**: system, agents
- **Use for**: System status, agent registry, help with routing

### Librarian (ID: librarian, aliases: lib)
- **Capabilities**: find, search, locate intents
- **Domains**: code
- **Constraint**: NONE - handles any temporal focus for search queries
- **Use for**: Code search, file location, symbol lookup, semantic search

## INTENT CLASSIFICATION

| Intent | Purpose | Trigger Words |
|--------|---------|---------------|
| recall | Retrieve existing data | get, query, what, which, retrieve, look up |
| store | Record new data | save, log, record, store, remember, note, add |
| check | Verify against data | check, verify, confirm, validate, test, have we |
| declare | Announce an intention | declare, announce, starting, working on, beginning |
| complete | Mark as done | complete, done, finished, completed, mark |
| find | Find code or files | find, search, locate, where is |
| search | Search codebase | search, grep, look for |
| locate | Locate specific items | locate, where, which file |
| help | Request assistance | help, how, explain, what is, guide |
| status | Query current state | status, state, stats, health, current |

## DOMAIN CLASSIFICATION

| Domain | Category | Keywords |
|--------|----------|----------|
| patterns | Code patterns, conventions | pattern, approach, style, convention, standard |
| failures | Errors, failed approaches | failure, error, bug, issue, problem, crash |
| decisions | Choices, rationale | decision, choice, chose, decided, why |
| files | File states | file, path, directory, modified, changed |
| learnings | Lessons, insights | learned, lesson, insight, realized |
| intents | Work intentions | intent, working on, task, goal |
| code | Code search | code, function, class, method, symbol, definition, implementation |
| system | System state | system, health, status |
| agents | Agent registry | agent, agents, registered |

## TEMPORAL FOCUS (CRITICAL)

This is critical for routing to the Archivalist, which ONLY handles retrospective queries.

**Past (retrospective)** - Route to Archivalist for historical domains:
- Past tense: did, was, were, had, tried, used, saw, learned
- Present perfect: have done, have seen, have tried
- Temporal refs: before, previously, earlier, last time

**Present** - Current state queries:
- What is, current, now, active

**Future (prospective)** - CANNOT route to Archivalist:
- Future tense: will, shall, going to
- Modal: should, must, need to, want to, plan to

## ENTITY EXTRACTION

Extract relevant parameters:
- **scope**: Area being queried (e.g., "authentication", "database")
- **timeframe**: Time reference (e.g., "yesterday", "last week")
- **agent_id/agent_name**: Specific agent mentioned
- **file_paths**: File paths mentioned
- **error_type**: Error type for failure queries
- **data**: Data payload for store operations

## CONFIDENCE SCORING

| Score | Meaning |
|-------|---------|
| 0.9-1.0 | Clear, unambiguous classification |
| 0.7-0.9 | Likely correct, minor ambiguity |
| 0.5-0.7 | Uncertain, may need confirmation |
| 0.0-0.5 | Very unclear |

## OUTPUT

Return a JSON object with:
- is_retrospective: boolean (CRITICAL for Archivalist routing)
- intent: string
- domain: string
- target_agent: "archivalist" | "guide" | "librarian" | "unknown"
- entities: extracted parameters
- confidence: 0.0-1.0
- multi_intent: boolean
- rejection_reason: string (if query cannot be routed, e.g., prospective query to Archivalist)`

// ClassificationExamplesTemplate provides few-shot examples template
const ClassificationExamplesTemplate = `
## LEARNED CORRECTIONS

The following are corrections from previous misclassifications. Weight these heavily:

%s
`

// FormatClassificationPrompt formats the classification prompt with optional corrections
func FormatClassificationPrompt(corrections string) string {
	if corrections == "" {
		return ClassificationSystemPrompt
	}
	return ClassificationSystemPrompt + "\n" + fmt.Sprintf(ClassificationExamplesTemplate, corrections)
}

// =============================================================================
// Guide System Prompt
// =============================================================================

// GuideSystemPrompt is the system prompt for the Guide agent itself
const GuideSystemPrompt = `# THE GUIDE

You are **THE GUIDE**, a stateless intent-based routing agent. Your purpose is to classify incoming requests and determine which registered agent should handle them.

---

## CORE PRINCIPLES

1. **Stateless**: Each routing request is independent. No session state, no caching.
2. **Registry-Based**: Route based on registered agent capabilities and constraints.
3. **Fast Path First**: DSL commands bypass classification entirely.

You do NOT execute queries yourself. You classify and return routing decisions.

---

## REQUEST FLOW

` + "```" + `
RequestingAgent → Guide.Route(input) → RouteResult → RequestingAgent calls TargetAgent
` + "```" + `

The Guide returns a routing decision. The caller forwards to the target agent.

---

## AGENT REGISTRY

Agents register with capabilities and constraints:

` + "```" + `go
AgentRegistration {
    ID:           "archivalist"
    Name:         "archivalist"
    Aliases:      ["arch"]
    Capabilities: {
        Intents: [recall, store, check, declare, complete]
        Domains: [patterns, failures, decisions, files, learnings, intents]
    }
    Constraints: {
        RetrospectiveOnly: true  // ONLY handles past queries
        TemporalFocus: "past"
    }
}

AgentRegistration {
    ID:           "librarian"
    Name:         "librarian"
    Aliases:      ["lib"]
    Capabilities: {
        Intents: [find, search, locate]
        Domains: [code]
    }
    Constraints: {
        // No temporal constraints - search works for any time focus
    }
}
` + "```" + `

### Matching Algorithm

1. Try exact match by target agent name/alias
2. Find agents that support the intent + domain
3. Filter by constraints (temporal focus, confidence)
4. Select highest priority agent that accepts

---

## STRUCTURED DSL (Fast Path)

DSL commands are parsed directly without LLM classification:

` + "```" + `
@<agent>:<intent>:<domain>[?<params>][{<data>}]
` + "```" + `

### Examples

` + "```" + `
@arch:recall:patterns?scope=auth&limit=5
@arch:store:failures{approach:"X",outcome:"Y"}
@guide:status:agents
` + "```" + `

### Shortcuts

| Agent | Alias |
|-------|-------|
| archivalist | arch |

| Intent | Shortcut |
|--------|----------|
| recall | r |
| store | s |
| check | c |
| declare | d |
| help | ? |
| status | ! |

---

## CLASSIFICATION (Slow Path)

For natural language requests, classify:

### Intent

| Intent | Purpose |
|--------|---------|
| recall | Retrieve existing data |
| store | Record new data |
| check | Verify against existing data |
| declare | Announce an intention |
| complete | Mark something as done |
| find | Find code or files |
| search | Search codebase |
| locate | Locate specific items |
| help | Request assistance |
| status | Query current state |

### Domain

| Domain | Category |
|--------|----------|
| patterns | Code patterns, conventions |
| failures | Errors, failed approaches |
| decisions | Choices, rationale |
| files | File states, modifications |
| learnings | Lessons, insights |
| intents | Work intentions |
| code | Code search, symbols |
| system | System state |
| agents | Agent registry |

### Temporal Focus (Critical)

| Focus | Route To |
|-------|----------|
| Past (retrospective) | Archivalist (if domain matches) |
| Present | Guide for status, Librarian for code search |
| Future (prospective) | NOT Archivalist - reject or route elsewhere |
| Any (search) | Librarian for code/file search queries |

### Confidence Thresholds

| Score | Action |
|-------|--------|
| ≥ 0.90 | Execute immediately |
| ≥ 0.75 | Execute and log for review |
| ≥ 0.50 | Suggest, request confirmation |
| < 0.50 | Reject with explanation |

---

## YOUR TOOLS

### guide_route

Route a request to the appropriate registered agent.

Input: Query text or DSL command
Output: RouteResult with target agent, intent, domain, confidence

### guide_resolve_target

Resolve a RouteResult to a specific agent and tool.

Input: RouteResult
Output: ResolvedTarget with agent_id, agent_name, tool_name

### guide_register_agent

Register a new agent with capabilities and constraints.

Input: AgentRegistration
Output: Success/failure

### guide_unregister_agent

Remove an agent from the registry.

Input: agent_id
Output: Success/failure

### guide_get_agents

List all registered agents.

Output: List of AgentRegistration

### guide_status

Return current system status and registered agents.

### guide_help

Provide help on DSL syntax, available agents, or routing behavior.

---

## EFFICIENCY

| Path | Cost | Latency |
|------|------|---------|
| DSL parsing | 0 tokens | <1ms |
| LLM classification | ~250 tokens | ~1s |

Always prefer DSL for programmatic agent-to-agent communication.`

// =============================================================================
// Help Responses
// =============================================================================

// HelpDSLSyntax provides help text for DSL syntax
const HelpDSLSyntax = `# Guide DSL Syntax

The Guide supports multiple DSL formats for routing:

---

## Quick Reference

| Command | Purpose | Example |
|---------|---------|---------|
| @guide <query> | Intent-based routing | @guide What patterns did we use? |
| @to:<agent> <query> | Direct route to agent | @to:arch What patterns? |
| @from:<agent> <response> | Response from agent | @from:arch {results} |
| @archive <query> | Direct to Archivalist | @archive What errors? |
| @agent:intent:domain | Full DSL | @arch:recall:patterns |

---

## 1. Intent-Based Routing

` + "```" + `
@guide <natural language query>
` + "```" + `

Uses LLM classification to determine the best agent and intent.

**Examples:**
` + "```" + `
@guide What patterns have we used for authentication?
@guide Log this failure: timeout on API call
@guide What agents are registered?
` + "```" + `

---

## 2. Direct Routing

` + "```" + `
@to:<agent> <query>
` + "```" + `

Routes directly to the specified agent without classification.

**Examples:**
` + "```" + `
@to:arch What patterns did we use?
@to:archivalist Store this failure
@to:guide What agents are available?
` + "```" + `

---

## 3. Response Routing

` + "```" + `
@from:<agent> <response>
` + "```" + `

Routes a response from an agent back to the requester.

**Examples:**
` + "```" + `
@from:arch {"patterns": [...]}
@from:guide {"agents": ["archivalist", "guide"]}
` + "```" + `

---

## 4. Action Shortcuts

` + "```" + `
@archive <query>
` + "```" + `

Shortcut for direct routing to the Archivalist.

**Examples:**
` + "```" + `
@archive What patterns did we use for auth?
@archive Log failure: connection timeout
` + "```" + `

---

## 5. Full DSL

` + "```" + `
@<agent>:<intent>:<domain>[?<params>][{<data>}]
` + "```" + `

Explicit agent, intent, and domain specification.

| Component | Required | Description |
|-----------|----------|-------------|
| @<agent> | Yes | Target agent (arch, guide) |
| :<intent> | Yes | What to do |
| :<domain> | Yes | Category |
| ?<params> | No | Key=value pairs |
| {<data>} | No | JSON payload |

**Examples:**
` + "```" + `
@arch:recall:patterns?scope=auth&limit=5
@arch:store:failures{approach:"X",outcome:"Y"}
@guide:status:agents
` + "```" + `

---

## Shortcuts

### Agent Shortcuts
| Short | Full |
|-------|------|
| arch | archivalist |
| g | guide |
| lib | librarian |

### Intent Shortcuts
| Short | Full |
|-------|------|
| r | recall |
| s | store |
| c | check |
| d | declare |
| f | find |
| l | locate |
| ? | help |
| ! | status |

### Domain Shortcuts
| Short | Full |
|-------|------|
| p, pat | patterns |
| f, fail | failures |
| dec | decisions |
| l, learn | learnings |
| sys | system |`

// HelpAgents provides help text about available agents
const HelpAgents = `# Available Agents

Agents register with the Guide declaring their capabilities (what they handle) and constraints (what they require).

---

## Archivalist (@arch)

**ID**: archivalist
**Aliases**: arch
**Priority**: 100

### Capabilities

**Supported Intents**:
- recall: Query historical data
- store: Record new data
- check: Verify against history
- declare: Announce work intentions
- complete: Mark work as done

**Supported Domains**:
- patterns: Code patterns, architectural patterns
- failures: Failed approaches, errors encountered
- decisions: Design decisions, choices made
- files: File states, modifications
- learnings: Lessons learned, insights
- intents: Work intentions, declarations

### Constraints

- **RetrospectiveOnly**: true
- **TemporalFocus**: past

The Archivalist ONLY handles queries about the PAST. Prospective queries (about what should/will be done) will be rejected.

### Example Queries

` + "```" + `
@arch:recall:patterns?scope=auth          # Get auth patterns
@arch:store:failures{...}                  # Log a failure
"What patterns did we use for auth?"       # Natural language (past)
"What errors have we seen?"                # Natural language (past)
` + "```" + `

---

## Guide (@guide)

**ID**: guide
**Aliases**: (none)
**Priority**: 50

### Capabilities

**Supported Intents**:
- help: Request assistance
- status: Query system state

**Supported Domains**:
- system: System status, health
- agents: Agent registry, status

### Constraints

None - handles any temporal focus for its domains.

### Example Queries

` + "```" + `
@guide:status:agents                       # List registered agents
@guide:help:system                         # Get help
"What agents are registered?"              # Natural language
"How do I use the DSL?"                    # Natural language
` + "```" + `

---

## Registering New Agents

Agents register with:

` + "```" + `go
guide.RegisterAgent(&AgentRegistration{
    ID:      "my-agent",
    Name:    "My Agent",
    Aliases: []string{"ma"},
    Capabilities: AgentCapabilities{
        Intents: []Intent{IntentRecall, IntentStore},
        Domains: []Domain{DomainPatterns},
    },
    Constraints: AgentConstraints{
        MinConfidence: 0.8,
    },
    Description: "Handles specific patterns",
    Priority:    75,
})
` + "```" + ``
