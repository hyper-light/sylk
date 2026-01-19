# Guide Domain Classification System

## Overview

The Guide agent classifies incoming queries to determine which domain(s) should handle them.
This enables proper routing and prevents domain contamination (e.g., academic papers appearing
in codebase searches).

## Domain Types

### Knowledge Domains
These domains handle information retrieval and storage:

| Domain | Description | Primary Use |
|--------|-------------|-------------|
| **Librarian** | Codebase knowledge | Code patterns, file locations, project structure |
| **Academic** | Research and documentation | Papers, articles, technical specifications |
| **Archivalist** | Historical records | Past decisions, failure patterns, audit trails |

### Implementation Domains
These domains handle code generation and modification:

| Domain | Description | Primary Use |
|--------|-------------|-------------|
| **Architect** | System design | Cross-domain coordination, task decomposition |
| **Engineer** | Code implementation | Writing and modifying code |
| **Designer** | UI/UX implementation | Frontend, styling, user experience |
| **Inspector** | Code review | Quality checks, best practices |
| **Tester** | Test generation | Unit tests, integration tests |

### Orchestration Domains

| Domain | Description | Primary Use |
|--------|-------------|-------------|
| **Orchestrator** | Workflow management | Task scheduling, agent coordination |
| **Guide** | User interaction | Query routing, clarification |

## Classification Cascade

Queries are classified using a multi-stage cascade:

1. **Lexical Stage**: Fast keyword matching for obvious domains
2. **Embedding Stage**: Semantic similarity for nuanced queries  
3. **Context Stage**: Historical patterns from conversation
4. **LLM Stage**: Fallback for ambiguous cases

The cascade exits early when confidence exceeds threshold (default: 0.85).

## Cross-Domain Detection

A query is cross-domain when it requires information from multiple domains:

**Example Cross-Domain Query:**
> "How does the authentication module work and what research papers influenced its design?"

This requires:
- **Librarian**: Authentication module implementation
- **Academic**: Research papers on authentication

Cross-domain queries route to the **Architect** for coordination.

## Explicit @Agent Addressing

Users can bypass classification with explicit addressing:

```
@Librarian where is the config file?
@Academic find papers on consensus algorithms
@Archivalist what decisions led to this architecture?
```

When explicit addressing is used:
1. Classification is skipped
2. Query routes directly to specified agent
3. Domain filters still apply within that agent

## DomainContext Structure

Every classified query produces a `DomainContext`:

```go
type DomainContext struct {
    OriginalQuery        string              // The user's query
    DetectedDomains      []Domain            // All detected domains
    DomainConfidences    map[Domain]float64  // Confidence per domain
    IsCrossDomain        bool                // Multiple domains detected
    PrimaryDomain        Domain              // Highest confidence domain
    SecondaryDomains     []Domain            // Lower confidence domains
    ClassificationMethod string              // Which cascade stage
    Signals              map[Domain][]string // Why each domain matched
    Confidence           float64             // Overall confidence
}
```

## Routing Rules

| Condition | Route To | Reason |
|-----------|----------|--------|
| Single domain, high confidence | Domain's primary agent | Direct handling |
| Cross-domain detected | Architect | Needs coordination |
| Explicit @Agent | Specified agent | User override |
| Low confidence | Guide (ask user) | Clarification needed |
| Classification failure | Fallback to Librarian | Default safe choice |

## Guide Responsibilities

As the Guide, you MUST:

1. **Classify every query** before routing
2. **Propagate DomainContext** to all downstream agents
3. **Respect explicit addressing** when present
4. **Route cross-domain queries to Architect**
5. **Request clarification** when confidence is low
6. **Log classification decisions** for debugging

## Failure Handling

When domain classification fails:

1. Log the failure with query details
2. Default to Librarian domain (codebase context)
3. Set low confidence flag
4. Include failure reason in DomainContext

The system should degrade gracefully, not error out.
