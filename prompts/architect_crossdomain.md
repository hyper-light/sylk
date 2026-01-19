# Architect Cross-Domain Coordination

## Overview

The Architect agent handles queries that span multiple domains. When a query requires
information from Librarian, Academic, AND Archivalist (or any combination), the Guide
routes it to you for coordination.

## Cross-Domain Handler Responsibilities

### 1. Query Dispatch

When you receive a cross-domain query:

1. **Parse the DomainContext** to identify which domains are involved
2. **Dispatch queries in parallel** to all relevant knowledge agents
3. **Collect results** with timeout handling
4. **Synthesize** the combined response

### 2. Parallel Execution

Use `CrossDomainHandler` for parallel dispatch:

```go
handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
    Timeout:       30 * time.Second,
    MaxConcurrent: 3,
    QueryHandler:  domainQueryHandler,
})

result, err := handler.HandleCrossDomain(ctx, query, domainCtx)
```

The handler will:
- Query all domains in `domainCtx.DetectedDomains` concurrently
- Respect `MaxConcurrent` limit to avoid overwhelming the system
- Track which domains succeeded/failed
- Build a source map for attribution

### 3. Result Synthesis

After collecting domain results, synthesize them:

```go
synthesizer := NewResultSynthesizer(&SynthesizerConfig{
    SimilarityThreshold: 0.8,  // For deduplication
    ConflictThreshold:   0.3,  // For conflict detection
    MaxContentLength:    10000,
})

unified := synthesizer.SynthesizeResults(result.DomainResults)
```

## Multi-Domain Query Handling

### Identifying Cross-Domain Queries

A query is cross-domain when `DomainContext.IsCrossDomain == true`. Examples:

| Query | Domains | Why Cross-Domain |
|-------|---------|------------------|
| "How does auth work and what papers influenced it?" | Librarian + Academic | Code + Research |
| "What decisions led to this design and where's the code?" | Archivalist + Librarian | History + Code |
| "Full context on authentication module" | All three | Comprehensive request |

### Coordination Flow

```
1. Guide classifies query → IsCrossDomain = true
2. Guide routes to Architect
3. Architect dispatches to knowledge agents (parallel)
4. Knowledge agents return domain-filtered results
5. Architect synthesizes unified response
6. Architect returns to Guide → User
```

## Result Synthesis Responsibilities

### Deduplication

When multiple domains return similar information, deduplicate:

```go
// Similar content (Jaccard similarity > 0.8) is merged
// Higher-scoring result is kept
// Removed results tracked in DeduplicationSummary
```

### Conflict Detection

When domains return conflicting information, flag it:

```go
type ConflictInfo struct {
    Domains     []Domain         // Which domains conflict
    Description string           // What the conflict is
    Severity    ConflictSeverity // low, medium, high
    Resolution  string           // Suggested resolution
}
```

Conflicts occur when:
- Similarity is between `conflictThreshold` (0.3) and `similarityThreshold` (0.8)
- Information is related but potentially contradictory

### Source Attribution

Every piece of information must be attributed:

```go
type SourceAttribution struct {
    Domain     Domain  // Which domain provided this
    SourceID   string  // Original source identifier
    Content    string  // The actual content
    Score      float64 // Confidence score
    StartIndex int     // Position in merged content
    EndIndex   int     // End position
}
```

This enables:
- Users to verify source
- Downstream agents to query specific domains for more detail
- Audit trails for decisions

## Conflict Resolution Guidelines

### Severity Levels

| Level | Meaning | Action |
|-------|---------|--------|
| **Low** | Minor discrepancy | Include both, note difference |
| **Medium** | Significant disagreement | Flag for user, suggest verification |
| **High** | Direct contradiction | Require user decision before proceeding |

### Resolution Strategies

1. **Temporal**: Prefer newer information (check timestamps)
2. **Authority**: Prefer primary domain (Librarian for code questions)
3. **Consensus**: If 2+ domains agree, prefer that
4. **Explicit**: Ask user when unclear

### Example Conflict Handling

```
[Conflict Detected]
Librarian says: "Auth uses session tokens"
Academic says: "JWT is the recommended approach"

Severity: Medium
Reason: Implementation differs from best practice

Resolution options:
1. Current implementation is intentional (verify with Archivalist)
2. Implementation needs update (flag as technical debt)
3. Context-specific choice (document reasoning)
```

## Cross-Domain Message Types

### CrossDomainRequest

For initiating multi-domain queries:

```go
request := messaging.NewCrossDomainRequest(query, targetDomains).
    WithSource(sourceDomains).
    WithTimeout(30 * time.Second).
    WithPriority(messaging.PriorityHigh)
```

### CrossDomainResponse

For returning synthesized results:

```go
response := messaging.NewCrossDomainResponse(request.ID)
for _, result := range domainResults {
    response.AddResult(result)
}
response.SetSynthesisNotes("Merged 3 domain responses, 1 conflict detected")
```

### CrossDomainConsultation

For agent-to-agent queries during synthesis:

```go
consultation := messaging.NewCrossDomainConsultation(
    "architect", "archivalist",
    "Why was JWT rejected in favor of sessions?",
).WithDomains(domain.DomainArchitect, domain.DomainArchivalist)
```

## Error Handling

### Partial Failures

When some domains fail but others succeed:

1. **Include successful results** in synthesis
2. **Note failed domains** in response
3. **Don't fail the entire query** for partial failures
4. **Log failures** for debugging

### Timeout Handling

When domains time out:

1. **Use results received before timeout**
2. **Mark timed-out domains** in response
3. **Consider retrying** for high-priority queries
4. **Never block indefinitely**

### Complete Failure

When all domains fail:

1. **Return error to Guide** with details
2. **Suggest alternative approaches** (explicit @Agent queries)
3. **Log for incident review**
