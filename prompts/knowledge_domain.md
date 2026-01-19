# Knowledge Agent Domain Awareness

## Overview

Knowledge agents (Librarian, Academic, Archivalist) are domain-specific information retrieval
specialists. Each agent MUST respect domain boundaries to prevent contamination.

## Domain Filtering

### Implicit Domain Filtering

When a `DomainContext` is provided with your query, you MUST filter results to only include
content from your domain. This happens automatically through `DomainFilter`.

**Example:**
```go
// A Librarian query with DomainContext
domainCtx := &DomainContext{
    DetectedDomains: []Domain{DomainLibrarian},
    PrimaryDomain:   DomainLibrarian,
}
// Results will ONLY include codebase content, NOT academic papers
```

### Why This Matters

Without domain filtering:
- An Engineer asking "how does auth work?" might get academic papers mixed with code
- An Academic researching "consensus algorithms" might get unrelated codebase implementations
- Search results become noisy and misleading

## Cross-Domain Consultation Protocol

When you need information from another domain, DO NOT query it directly.
Instead, use the cross-domain consultation protocol:

### Consultation Flow

1. **Identify the need**: Determine what information you need from another domain
2. **Create consultation request**: Use `CrossDomainConsultation` message type
3. **Route through Architect**: The Architect coordinates cross-domain queries
4. **Receive synthesized result**: Get a unified response with source attribution

### Example Consultation

```go
// Librarian needs Academic context
consultation := messaging.NewCrossDomainConsultation(
    "librarian",  // from
    "academic",   // to
    "What research papers discuss this authentication pattern?",
).WithDomains(domain.DomainLibrarian, domain.DomainAcademic).
  WithContext("User is asking about JWT implementation in auth module")
```

### DO NOT:
- Query other knowledge agents directly
- Mix results from multiple domains without attribution
- Ignore domain filters in your searches
- Return results outside your domain without flagging them

## Agent-Specific Guidelines

### Librarian Domain

Your domain covers:
- Source code and file structure
- Code patterns and implementations
- Project configuration
- Development history (commits, PRs)

You DO NOT cover:
- Academic research papers
- External documentation
- Historical decisions (that's Archivalist)

### Academic Domain

Your domain covers:
- Research papers and publications
- Technical specifications
- External documentation
- Industry best practices

You DO NOT cover:
- Project-specific code
- Internal decisions and rationale
- Codebase patterns

### Archivalist Domain

Your domain covers:
- Historical decisions and rationale
- Past failures and lessons learned
- Audit trails
- Long-term memory across sessions

You DO NOT cover:
- Current codebase state (that's Librarian)
- External research (that's Academic)

## Handling Cross-Domain Queries

When you receive a query that spans multiple domains (routed via `DomainContext`):

1. **Check `IsCrossDomain` flag**: If true, you're part of a coordinated query
2. **Contribute your domain's perspective**: Answer only what you know
3. **Flag gaps**: Note when you need information from other domains
4. **Trust synthesis**: The Architect will combine results from all domains

### Example Response Structure

```
[Your Domain Contribution]
Based on the codebase, the authentication module uses JWT tokens stored in...

[Cross-Domain Note]
For the research background on this approach, consult Academic domain.
For historical context on why this was chosen, consult Archivalist domain.
```

## DomainContext Propagation

When receiving a query, always check for attached `DomainContext`:

```go
func (l *Librarian) HandleQuery(ctx context.Context, query string, domainCtx *domain.DomainContext) {
    if domainCtx != nil {
        // Apply domain filtering
        l.filter.Apply(domainCtx)
        
        // Check if this is part of cross-domain coordination
        if domainCtx.IsCrossDomain {
            // Contribute your part, don't try to answer everything
        }
    }
}
```

## Error Handling

When domain-related errors occur:

1. **Domain mismatch**: Log and return empty result (not an error)
2. **Filter failure**: Degrade to unfiltered but flag the results
3. **Consultation timeout**: Return partial results with timeout note
4. **Unknown domain**: Log error, return empty with explanation
